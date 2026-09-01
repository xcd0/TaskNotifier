//go:build windows

package tasknotifier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBatchRunnerPersistsAndMergesResult(t *testing.T) {
	directory := t.TempDir()
	paths := Paths{
		Executable: os.Args[0],
		Directory:  directory,
		Tasks:      filepath.Join(directory, TaskFileName),
	}
	runID := "0123456789abcdef0123456789abcdef"
	cmdPath := filepath.Join(directory, "exit7.cmd")
	if err := os.WriteFile(cmdPath, []byte("@echo off\r\nexit /b 7\r\n"), 0o600); err != nil {
		t.Fatalf("テスト用CMDを作成できません: %v", err)
	}

	scheduledAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	request := batchRunRequest{
		RunID:       runID,
		EventKey:    "event-1",
		TaskID:      "task-1",
		TaskTitle:   "テストBAT",
		ScheduledAt: scheduledAt.Format(time.RFC3339),
		RequestedAt: time.Now().Format(time.RFC3339),
		Action:      TaskAction{BatPath: cmdPath},
	}
	if err := writeJSONAtomic(batchRunRequestPath(paths, runID), request); err != nil {
		t.Fatalf("BAT実行要求を保存できません: %v", err)
	}
	if err := runBatchRunnerWithPaths(paths, runID); err != nil {
		t.Fatalf("BAT runnerを実行できません: %v", err)
	}

	result, err := readBatchRunResult(batchRunResultPath(paths, runID))
	if err != nil {
		t.Fatalf("BAT実行結果を読み込めません: %v", err)
	}
	if !result.Started {
		t.Fatal("BATが開始済みとして記録されていません")
	}
	if result.ExitCode != 7 {
		t.Fatalf("終了コードが不正です: got=%d want=7", result.ExitCode)
	}
	if result.Error == "" {
		t.Fatal("非0終了コードのエラー内容が記録されていません")
	}

	data := EmptyTaskFile()
	merged, runIDs, changed, err := MergePendingBatchRunResults(paths, data)
	if err != nil {
		t.Fatalf("BAT実行結果をマージできません: %v", err)
	}
	if !changed {
		t.Fatal("初回マージが変更なしになっています")
	}
	if len(runIDs) != 1 || runIDs[0] != runID {
		t.Fatalf("取り込み対象run_idが不正です: %#v", runIDs)
	}
	if len(merged.History) != 1 {
		t.Fatalf("履歴件数が不正です: got=%d want=1", len(merged.History))
	}
	if !strings.Contains(merged.History[0].Result, "exit=7") {
		t.Fatalf("履歴へ終了コードが記録されていません: %q", merged.History[0].Result)
	}

	mergedAgain, runIDsAgain, changedAgain, err := MergePendingBatchRunResults(paths, merged)
	if err != nil {
		t.Fatalf("BAT実行結果を再マージできません: %v", err)
	}
	if changedAgain {
		t.Fatal("同じrun_idが二重に履歴へ追加されました")
	}
	if len(runIDsAgain) != 1 || len(mergedAgain.History) != 1 {
		t.Fatalf("再マージ結果が不正です: run_ids=%#v history=%d", runIDsAgain, len(mergedAgain.History))
	}

	if err := CleanupBatchRunResults(paths, runIDsAgain); err != nil {
		t.Fatalf("BAT実行結果を整理できません: %v", err)
	}
	if _, err := os.Stat(batchRunRequestPath(paths, runID)); !os.IsNotExist(err) {
		t.Fatalf("request.jsonが残っています: %v", err)
	}
	if _, err := os.Stat(batchRunResultPath(paths, runID)); !os.IsNotExist(err) {
		t.Fatalf("result.jsonが残っています: %v", err)
	}
}

func TestRecoverInterruptedBatchRun(t *testing.T) {
	directory := t.TempDir()
	paths := Paths{Executable: os.Args[0], Directory: directory, Tasks: filepath.Join(directory, TaskFileName)}
	runID := "fedcba9876543210fedcba9876543210"
	now := time.Now().Truncate(time.Second)
	request := batchRunRequest{
		RunID:       runID,
		EventKey:    "event-interrupted",
		TaskID:      "task-interrupted",
		TaskTitle:   "中断テスト",
		ScheduledAt: now.Add(-time.Minute).Format(time.RFC3339),
		RequestedAt: now.Add(-time.Minute).Format(time.RFC3339),
		Action:      TaskAction{BatPath: filepath.Join(directory, "dummy.cmd")},
	}
	if err := writeJSONAtomic(batchRunRequestPath(paths, runID), request); err != nil {
		t.Fatalf("実行要求を保存できません: %v", err)
	}
	if err := RecoverInterruptedBatchRuns(paths, now); err != nil {
		t.Fatalf("中断を復旧できません: %v", err)
	}
	result, err := readBatchRunResult(batchRunResultPath(paths, runID))
	if err != nil {
		t.Fatalf("中断結果を読み込めません: %v", err)
	}
	if !result.Interrupted {
		t.Fatalf("中断結果になっていません: %+v", result)
	}
	if got := formatBatchRunResult(result); got != "BAT実行中断" {
		t.Fatalf("中断履歴文言が不正です: %q", got)
	}
}
