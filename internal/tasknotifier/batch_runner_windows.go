//go:build windows

package tasknotifier

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	batchRunsDirectoryName = "runs"
	batchRequestSuffix     = ".request.json"
	batchResultSuffix      = ".result.json"
)

type batchRunRequest struct {
	RunID           string     `json:"run_id"`
	EventKey        string     `json:"event_key"`
	TaskID          string     `json:"task_id"`
	TaskTitle       string     `json:"task_title"`
	ScheduledAt     string     `json:"scheduled_at"`
	RequestedAt     string     `json:"requested_at"`
	RunnerPID       int        `json:"runner_pid,omitempty"`
	RunnerStartedAt string     `json:"runner_started_at,omitempty"`
	BatchStartedAt  string     `json:"batch_started_at,omitempty"`
	Action          TaskAction `json:"action"`
}

type batchRunResult struct {
	RunID       string `json:"run_id"`
	EventKey    string `json:"event_key"`
	TaskID      string `json:"task_id"`
	TaskTitle   string `json:"task_title"`
	ScheduledAt string `json:"scheduled_at"`
	FinishedAt  string `json:"finished_at"`
	Started     bool   `json:"started"`
	Interrupted bool   `json:"interrupted,omitempty"`
	ExitCode    int    `json:"exit_code"`
	Error       string `json:"error,omitempty"`
}

// BatchRunView は管理画面へ表示するBAT/CMD実行状況を表す。
type BatchRunView struct {
	RunID      string `json:"run_id"`
	TaskTitle  string `json:"task_title"`
	BATPath    string `json:"bat_path"`
	StartedAt  string `json:"started_at"`
	Status     string `json:"status"`
	StatusText string `json:"status_text"`
}

// StartPersistentBatchRun は実行要求を永続化し、完了監視用の別TaskNotifierプロセスを開始する。
func StartPersistentBatchRun(paths Paths, event Event, action TaskAction) (string, error) {
	runID, err := NewTaskID()
	if err != nil {
		return "", fmt.Errorf("BAT実行IDを生成できません: %w", err)
	}
	if err := validateBatchRunID(runID); err != nil {
		return "", err
	}

	normalized, err := NormalizeBATPath(paths.Directory, action.BatPath)
	if err != nil {
		return "", err
	}
	action.BatPath = normalized
	if _, err := BuildBatchLaunch(paths.Directory, action); err != nil {
		return "", err
	}

	request := batchRunRequest{
		RunID:       runID,
		EventKey:    event.Key,
		TaskID:      event.TaskID,
		TaskTitle:   event.TaskTitle,
		ScheduledAt: event.ScheduledAt.Format(time.RFC3339),
		RequestedAt: time.Now().Format(time.RFC3339),
		Action:      action,
	}
	requestPath := batchRunRequestPath(paths, runID)
	if err := writeJSONAtomic(requestPath, request); err != nil {
		return "", fmt.Errorf("BAT実行要求を保存できません: %w", err)
	}

	command := exec.Command(paths.Executable, "--batch-runner", runID)
	command.Dir = paths.Directory
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | 0x00000200,
	}
	if err := command.Start(); err != nil {
		_ = os.Remove(requestPath)
		return "", fmt.Errorf("BAT完了監視プロセスを開始できません: %w", err)
	}
	// 子プロセスは親TaskNotifierの終了後も継続するため、親側のプロセスハンドルだけを解放する。
	_ = command.Process.Release()
	return runID, nil
}

// RunBatchRunner は保存済み要求のBAT/CMDを実行し、完了結果をrunsディレクトリへ保存する。
func RunBatchRunner(executable, runID string) error {
	if err := validateBatchRunID(runID); err != nil {
		return err
	}
	paths, err := ResolvePaths(executable)
	if err != nil {
		return err
	}
	return runBatchRunnerWithPaths(paths, runID)
}

func runBatchRunnerWithPaths(paths Paths, runID string) error {
	request, err := readBatchRunRequest(batchRunRequestPath(paths, runID))
	if err != nil {
		return fmt.Errorf("BAT実行要求を読み込めません: %w", err)
	}
	if request.RunID != runID {
		return errors.New("BAT実行要求のrun_idが一致しません")
	}

	runnerStartedAt, startTimeErr := currentProcessStartTime()
	if startTimeErr != nil {
		runnerStartedAt = time.Now()
	}
	request.RunnerPID = os.Getpid()
	request.RunnerStartedAt = runnerStartedAt.Format(time.RFC3339Nano)
	if err := writeJSONAtomic(batchRunRequestPath(paths, runID), request); err != nil {
		return fmt.Errorf("BAT実行要求へrunner情報を保存できません: %w", err)
	}

	result := batchRunResult{
		RunID:       request.RunID,
		EventKey:    request.EventKey,
		TaskID:      request.TaskID,
		TaskTitle:   request.TaskTitle,
		ScheduledAt: request.ScheduledAt,
		ExitCode:    -1,
	}
	process, startErr := StartBatch(paths.Directory, request.Action)
	if startErr == nil {
		result.Started = true
		request.BatchStartedAt = time.Now().Format(time.RFC3339)
		if err := writeJSONAtomic(batchRunRequestPath(paths, runID), request); err != nil {
			result.Error = "BAT開始時刻を保存できません: " + err.Error()
		}
		batchResult, waitErr := process.Wait()
		result.ExitCode = batchResult.ExitCode
		if waitErr != nil {
			result.Error = waitErr.Error()
		}
	} else {
		result.Error = startErr.Error()
	}
	result.FinishedAt = time.Now().Format(time.RFC3339)

	if err := writeJSONAtomic(batchRunResultPath(paths, runID), result); err != nil {
		return fmt.Errorf("BAT実行結果を保存できません: %w", err)
	}
	return nil
}

// MergePendingBatchRunResults は未取り込みの完了結果を通知履歴へ重ねる。
// 同じrun_idの結果はEventKeyで検出し、何度読み込んでも重複追加しない。
func MergePendingBatchRunResults(paths Paths, data TaskFile) (TaskFile, []string, bool, error) {
	directory := batchRunsDirectory(paths)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil, false, nil
	}
	if err != nil {
		return data, nil, false, fmt.Errorf("BAT実行結果ディレクトリを確認できません: %w", err)
	}

	resultNames := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), batchResultSuffix) {
			resultNames = append(resultNames, entry.Name())
		}
	}
	sort.Strings(resultNames)

	merged := cloneTaskFile(data)
	consumed := make([]string, 0, len(resultNames))
	changed := false
	for _, name := range resultNames {
		path := filepath.Join(directory, name)
		result, err := readBatchRunResult(path)
		if err != nil {
			return data, nil, false, fmt.Errorf("BAT実行結果 %q を読み込めません: %w", name, err)
		}
		if err := validateBatchRunID(result.RunID); err != nil {
			return data, nil, false, fmt.Errorf("BAT実行結果 %q: %w", name, err)
		}
		historyKey := batchHistoryEventKey(result.EventKey, result.RunID)
		if !historyContainsEventKey(merged.History, historyKey) {
			merged.History = append(merged.History, HistoryEntry{
				EventKey:    historyKey,
				TaskID:      result.TaskID,
				TaskTitle:   result.TaskTitle,
				ScheduledAt: result.ScheduledAt,
				NotifiedAt:  result.FinishedAt,
				Method:      NotificationDialog,
				Result:      formatBatchRunResult(result),
			})
			if len(merged.History) > HistoryLimit {
				merged.History = append([]HistoryEntry(nil), merged.History[len(merged.History)-HistoryLimit:]...)
			}
			changed = true
		}
		consumed = append(consumed, result.RunID)
	}
	return merged, consumed, changed, nil
}

// CleanupBatchRunResults はstate.jsonへの取り込み完了後に要求・結果ファイルを削除する。
func CleanupBatchRunResults(paths Paths, runIDs []string) error {
	var failures []string
	for _, runID := range runIDs {
		if err := validateBatchRunID(runID); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		for _, path := range []string{batchRunRequestPath(paths, runID), batchRunResultPath(paths, runID)} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Sprintf("%s: %v", path, err))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("BAT実行結果ファイルを整理できません: %s", strings.Join(failures, " / "))
	}
	return nil
}

func batchRunsDirectory(paths Paths) string {
	return filepath.Join(filepath.Dir(paths.Tasks), batchRunsDirectoryName)
}

func batchRunRequestPath(paths Paths, runID string) string {
	return filepath.Join(batchRunsDirectory(paths), runID+batchRequestSuffix)
}

func batchRunResultPath(paths Paths, runID string) string {
	return filepath.Join(batchRunsDirectory(paths), runID+batchResultSuffix)
}

func validateBatchRunID(runID string) error {
	if len(runID) != 32 {
		return fmt.Errorf("BAT実行IDが不正です: %q", runID)
	}
	decoded, err := hex.DecodeString(runID)
	if err != nil || len(decoded) != 16 {
		return fmt.Errorf("BAT実行IDが不正です: %q", runID)
	}
	return nil
}

// RecoverInterruptedBatchRuns は監視プロセスが消失した実行要求を中断結果へ変換する。
func RecoverInterruptedBatchRuns(paths Paths, now time.Time) error {
	directory := batchRunsDirectory(paths)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("BAT実行ディレクトリを確認できません: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), batchRequestSuffix) {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), batchRequestSuffix)
		if _, err := os.Stat(batchRunResultPath(paths, runID)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		request, err := readBatchRunRequest(batchRunRequestPath(paths, runID))
		if err != nil {
			return fmt.Errorf("BAT実行要求 %q を読み込めません: %w", runID, err)
		}
		requestedAt, _ := time.Parse(time.RFC3339, request.RequestedAt)
		if request.RunnerPID == 0 && now.Sub(requestedAt) < 15*time.Second {
			continue
		}
		if sameRunningProcess(request.RunnerPID, request.RunnerStartedAt) {
			continue
		}
		result := batchRunResult{
			RunID:       request.RunID,
			EventKey:    request.EventKey,
			TaskID:      request.TaskID,
			TaskTitle:   request.TaskTitle,
			ScheduledAt: request.ScheduledAt,
			FinishedAt:  now.Format(time.RFC3339),
			Started:     request.BatchStartedAt != "",
			Interrupted: true,
			ExitCode:    -1,
			Error:       "BAT完了監視プロセスが終了しました",
		}
		if err := writeJSONAtomic(batchRunResultPath(paths, runID), result); err != nil {
			return fmt.Errorf("BAT中断結果を保存できません: %w", err)
		}
	}
	return nil
}

// ListBatchRuns は現在のBAT/CMD実行要求を表示用に列挙する。
func ListBatchRuns(paths Paths, now time.Time) ([]BatchRunView, error) {
	if err := RecoverInterruptedBatchRuns(paths, now); err != nil {
		return nil, err
	}
	directory := batchRunsDirectory(paths)
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []BatchRunView{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("BAT実行ディレクトリを確認できません: %w", err)
	}
	views := make([]BatchRunView, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), batchRequestSuffix) {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), batchRequestSuffix)
		request, err := readBatchRunRequest(batchRunRequestPath(paths, runID))
		if err != nil {
			continue
		}
		view := BatchRunView{RunID: runID, TaskTitle: request.TaskTitle, BATPath: request.Action.BatPath, StartedAt: request.BatchStartedAt, Status: "starting", StatusText: "起動中"}
		if view.StartedAt == "" {
			view.StartedAt = request.RequestedAt
		}
		if result, err := readBatchRunResult(batchRunResultPath(paths, runID)); err == nil {
			switch {
			case result.Interrupted:
				view.Status, view.StatusText = "interrupted", "中断"
			case result.Error == "" && result.ExitCode == 0:
				view.Status, view.StatusText = "success", "完了"
			default:
				view.Status, view.StatusText = "failed", "失敗"
			}
		} else if sameRunningProcess(request.RunnerPID, request.RunnerStartedAt) {
			view.Status, view.StatusText = "running", "実行中"
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool { return views[i].StartedAt > views[j].StartedAt })
	return views, nil
}

func readBatchRunRequest(path string) (batchRunRequest, error) {
	var request batchRunRequest
	if err := readStrictJSON(path, &request); err != nil {
		return batchRunRequest{}, err
	}
	if request.RunID == "" || request.EventKey == "" || request.TaskID == "" || request.TaskTitle == "" || request.ScheduledAt == "" || request.RequestedAt == "" || request.Action.BatPath == "" {
		return batchRunRequest{}, errors.New("BAT実行要求の必須項目が不足しています")
	}
	if _, err := time.Parse(time.RFC3339, request.ScheduledAt); err != nil {
		return batchRunRequest{}, fmt.Errorf("scheduled_atが不正です: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, request.RequestedAt); err != nil {
		return batchRunRequest{}, fmt.Errorf("requested_atが不正です: %w", err)
	}
	if request.RunnerStartedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, request.RunnerStartedAt); err != nil {
			return batchRunRequest{}, fmt.Errorf("runner_started_atが不正です: %w", err)
		}
	}
	if request.BatchStartedAt != "" {
		if _, err := time.Parse(time.RFC3339, request.BatchStartedAt); err != nil {
			return batchRunRequest{}, fmt.Errorf("batch_started_atが不正です: %w", err)
		}
	}
	return request, nil
}

func readBatchRunResult(path string) (batchRunResult, error) {
	var result batchRunResult
	if err := readStrictJSON(path, &result); err != nil {
		return batchRunResult{}, err
	}
	if result.RunID == "" || result.EventKey == "" || result.TaskID == "" || result.TaskTitle == "" || result.ScheduledAt == "" || result.FinishedAt == "" {
		return batchRunResult{}, errors.New("BAT実行結果の必須項目が不足しています")
	}
	if _, err := time.Parse(time.RFC3339, result.ScheduledAt); err != nil {
		return batchRunResult{}, fmt.Errorf("scheduled_atが不正です: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, result.FinishedAt); err != nil {
		return batchRunResult{}, fmt.Errorf("finished_atが不正です: %w", err)
	}
	return result, nil
}

func readStrictJSON(path string, value interface{}) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSONに複数の値があります")
		}
		return err
	}
	return nil
}

func historyContainsEventKey(history []HistoryEntry, eventKey string) bool {
	for _, entry := range history {
		if entry.EventKey == eventKey {
			return true
		}
	}
	return false
}

func batchHistoryEventKey(eventKey, runID string) string {
	return eventKey + ":batch:" + runID
}

func formatBatchRunResult(result batchRunResult) string {
	if result.Interrupted {
		return "BAT実行中断"
	}
	if !result.Started {
		if result.Error == "" {
			return "BAT起動失敗"
		}
		return "BAT起動失敗: " + result.Error
	}
	if result.Error == "" && result.ExitCode == 0 {
		return "BAT実行成功 (exit=0)"
	}
	if result.ExitCode != 0 {
		return fmt.Sprintf("BAT実行失敗 (exit=%d): %s", result.ExitCode, result.Error)
	}
	return "BAT実行失敗: " + result.Error
}
