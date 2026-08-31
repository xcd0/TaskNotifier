package tasknotifier

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	temporaryFileSuffix = ".tasknotifier.tmp"
	stateFileName       = "state.json"
)

// FileStamp は外部編集検出に必要な最小情報を保持する。
type FileStamp struct {
	ModTime time.Time
	Size    int64
}

// Store はtasks.jsonとstate.jsonの直列化された読み書きを担当する。
type Store struct {
	path             string
	statePath        string
	mu               sync.Mutex
	lastWarnings     []string
	lastStamp        FileStamp
	hasLastStamp     bool
	writeBlocked     bool
	writeBlockReason string
}

type taskConfigFile struct {
	FormatVersion int          `json:"format_version"`
	Periods       []Period     `json:"periods"`
	Tasks         []taskConfig `json:"tasks"`
}

type taskConfig struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind,omitempty"`
	Enabled      bool                 `json:"enabled"`
	Title        string               `json:"title"`
	Schedule     Schedule             `json:"schedule"`
	Condition    TaskCondition        `json:"condition"`
	Notification NotificationSettings `json:"notification"`
	Action       TaskAction           `json:"action"`
}

type runtimeStateFile struct {
	FormatVersion int              `json:"format_version"`
	Tasks         map[string]State `json:"tasks"`
	History       []HistoryEntry   `json:"history"`
}

// NewStore は指定したタスク設定ファイルと同じディレクトリのstate.jsonを扱う保存器を返す。
func NewStore(path string) *Store {
	cleanPath := filepath.Clean(path)
	return &Store{
		path:      cleanPath,
		statePath: filepath.Join(filepath.Dir(cleanPath), stateFileName),
	}
}

// MigrateLegacyTaskFile は旧tasks.txtまたはEXE横tasks.jsonを、AppData側tasks.jsonがない場合だけ移行する。
func MigrateLegacyTaskFile(paths Paths) (bool, error) {
	if _, err := os.Stat(paths.Tasks); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("tasks.jsonを確認できません: %w", err)
	}
	if _, err := os.Stat(paths.LegacyTasks); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("旧タスク設定を確認できません: %w", err)
	}

	data, err := readTaskFile(paths.LegacyTasks)
	if err != nil {
		return false, fmt.Errorf("旧タスク設定を検証できません: %w", err)
	}
	if _, err := validateForLoad(&data); err != nil {
		return false, fmt.Errorf("旧タスク設定を検証できません: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Tasks), 0o700); err != nil {
		return false, fmt.Errorf("移行先ディレクトリを作成できません: %w", err)
	}
	if err := replaceFile(paths.LegacyTasks, paths.Tasks); err != nil {
		return false, fmt.Errorf("旧タスク設定を移行できません: %w", err)
	}
	return true, nil
}

// Path は管理対象のtasks.jsonパスを返す。
func (s *Store) Path() string {
	return s.path
}

// StatePath は管理対象のstate.jsonパスを返す。
func (s *Store) StatePath() string {
	return s.statePath
}

// RecoverTemporary は中断された保存の一時ファイルを安全に整理する。
func (s *Store) RecoverTemporary() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := recoverTemporaryJSON(s.path, func(path string) error {
		data, err := readTaskFile(path)
		if err != nil {
			return err
		}
		_, err = validateForLoad(&data)
		return err
	}); err != nil {
		return err
	}
	return recoverTemporaryJSON(s.statePath, func(path string) error {
		_, err := readRuntimeState(path)
		return err
	})
}

// Load はtasks.jsonを読み込み、state.jsonの実行状態と履歴を重ねる。
// state.jsonがまだない旧形式では、tasks.json内の状態をstate.jsonへ移して設定ファイルから分離する。
func (s *Store) Load() (TaskFile, FileStamp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := readTaskFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		if s.hasLastStamp {
			s.blockWritesLocked("読み込み後にtasks.jsonが削除されました")
			return TaskFile{}, FileStamp{}, s.writeBlockedErrorLocked()
		}
		s.clearWriteBlockLocked()
		data = EmptyTaskFile()
		if err := s.writeConfigLocked(data, false); err != nil {
			return TaskFile{}, FileStamp{}, err
		}
		if err := s.writeStateLocked(data); err != nil {
			return TaskFile{}, FileStamp{}, err
		}
	} else if err != nil {
		s.blockWritesLocked("tasks.jsonの読み込みに失敗しました")
		return TaskFile{}, FileStamp{}, err
	}

	warnings, err := validateForLoad(&data)
	if err != nil {
		s.blockWritesLocked("tasks.jsonの検証に失敗しました")
		return TaskFile{}, FileStamp{}, err
	}

	state, stateErr := readRuntimeState(s.statePath)
	if stateErr == nil {
		applyRuntimeState(&data, state)
	} else if errors.Is(stateErr, os.ErrNotExist) {
		// 旧tasks.jsonに含まれていた実行状態と履歴をそのままstate.jsonへ引き継ぐ。
		if err := s.writeStateLocked(data); err != nil {
			return TaskFile{}, FileStamp{}, fmt.Errorf("state.jsonへ実行状態を移行できません: %w", err)
		}
		if err := s.writeConfigLocked(data, true); err != nil {
			return TaskFile{}, FileStamp{}, fmt.Errorf("tasks.jsonから実行状態を分離できません: %w", err)
		}
	} else {
		return TaskFile{}, FileStamp{}, fmt.Errorf("state.jsonを読み込めません: %w", stateErr)
	}

	stamp, err := statStamp(s.path)
	if err != nil {
		s.blockWritesLocked("tasks.jsonの状態を確認できませんでした")
		return TaskFile{}, FileStamp{}, err
	}
	s.lastWarnings = warnings
	s.rememberStampLocked(stamp)
	return data, stamp, nil
}

// Save は設定と実行状態をそれぞれ原子的に保存する。
func (s *Store) Save(data TaskFile) (FileStamp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(data)
}

// Warnings は直近の読み込みで安全のため無効化した項目を返す。
func (s *Store) Warnings() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lastWarnings...)
}

func (s *Store) saveLocked(data TaskFile) (FileStamp, error) {
	if err := data.Validate(); err != nil {
		return FileStamp{}, fmt.Errorf("tasks.jsonを保存できません: %w", err)
	}
	if err := s.ensureWritableLocked(); err != nil {
		return FileStamp{}, err
	}
	if err := s.writeStateLocked(data); err != nil {
		return FileStamp{}, err
	}
	// JSON生成中に外部編集された場合も、設定置換直前に検出して利用者の変更を保護する。
	if err := s.ensureWritableLocked(); err != nil {
		return FileStamp{}, err
	}
	if err := s.writeConfigLocked(data, true); err != nil {
		return FileStamp{}, err
	}
	s.lastWarnings = nil

	stamp, err := statStamp(s.path)
	if err != nil {
		return FileStamp{}, err
	}
	s.rememberStampLocked(stamp)
	return stamp, nil
}

func (s *Store) writeConfigLocked(data TaskFile, backup bool) error {
	config := taskConfigFile{
		FormatVersion: data.FormatVersion,
		Periods:       append([]Period(nil), data.Periods...),
		Tasks:         make([]taskConfig, 0, len(data.Tasks)),
	}
	for _, task := range data.Tasks {
		config.Tasks = append(config.Tasks, taskConfig{
			ID:           task.ID,
			Kind:         task.Kind,
			Enabled:      task.Enabled,
			Title:        task.Title,
			Schedule:     task.Schedule,
			Condition:    task.Condition,
			Notification: task.Notification,
			Action:       task.Action,
		})
	}
	if backup {
		if err := backupFile(s.path); err != nil {
			return fmt.Errorf("tasks.jsonのバックアップを作成できません: %w", err)
		}
	}
	if err := writeJSONAtomic(s.path, config); err != nil {
		return fmt.Errorf("tasks.jsonを保存できません: %w", err)
	}
	return nil
}

func (s *Store) writeStateLocked(data TaskFile) error {
	state := runtimeStateFile{
		FormatVersion: FormatVersion,
		Tasks:         make(map[string]State, len(data.Tasks)),
		History:       append([]HistoryEntry(nil), data.History...),
	}
	for _, task := range data.Tasks {
		state.Tasks[task.ID] = task.State
	}
	if err := writeJSONAtomic(s.statePath, state); err != nil {
		return fmt.Errorf("state.jsonを保存できません: %w", err)
	}
	return nil
}

func applyRuntimeState(data *TaskFile, state runtimeStateFile) {
	for index := range data.Tasks {
		if taskState, exists := state.Tasks[data.Tasks[index].ID]; exists {
			data.Tasks[index].State = taskState
		}
	}
	data.History = append([]HistoryEntry(nil), state.History...)
	if data.History == nil {
		data.History = []HistoryEntry{}
	}
}

func readRuntimeState(path string) (runtimeStateFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return runtimeStateFile{}, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var state runtimeStateFile
	if err := decoder.Decode(&state); err != nil {
		return runtimeStateFile{}, fmt.Errorf("state.jsonのJSONが不正です: %w", err)
	}
	if state.FormatVersion != FormatVersion {
		return runtimeStateFile{}, fmt.Errorf("state.jsonのformat_versionが未対応です: %d", state.FormatVersion)
	}
	if state.Tasks == nil {
		state.Tasks = map[string]State{}
	}
	if state.History == nil {
		state.History = []HistoryEntry{}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return runtimeStateFile{}, errors.New("state.jsonに複数のJSON値があります")
		}
		return runtimeStateFile{}, fmt.Errorf("state.json末尾が不正です: %w", err)
	}
	return state, nil
}

func writeJSONAtomic(path string, value interface{}) error {
	encoded, err := json.MarshalIndent(value, "", "\t")
	if err != nil {
		return fmt.Errorf("JSONを生成できません: %w", err)
	}
	encoded = append(encoded, '\n')
	if bytes.HasPrefix(encoded, []byte{0xef, 0xbb, 0xbf}) {
		return errors.New("内部エラー: JSONにBOMが含まれています")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("保存先ディレクトリを準備できません: %w", err)
	}

	temporary := path + temporaryFileSuffix
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("一時ファイルを作成できません: %w", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("一時ファイルへ書き込めません: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("一時ファイルを同期できません: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("一時ファイルを閉じられません: %w", err)
	}
	if err := replaceFile(temporary, path); err != nil {
		return fmt.Errorf("ファイルを置換できません: %w", err)
	}
	removeTemporary = false
	return nil
}

func backupFile(path string) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	backup := path + ".bak"
	temporary := backup + temporaryFileSuffix
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return err
	}
	if err := replaceFile(temporary, backup); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func recoverTemporaryJSON(path string, validate func(string) error) error {
	temporary := path + temporaryFileSuffix
	if _, err := os.Stat(temporary); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("一時ファイルを確認できません: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(temporary); err != nil {
			return fmt.Errorf("不要な一時ファイルを削除できません: %w", err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("保存先ファイルを確認できません: %w", err)
	}
	if err := validate(temporary); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("残存一時ファイルは不正なため削除しました: %w", err)
	}
	if err := replaceFile(temporary, path); err != nil {
		return fmt.Errorf("残存一時ファイルから復旧できません: %w", err)
	}
	return nil
}

// ensureWritableLocked は未読ファイル、不正ファイル、外部変更を上書きしないように保存可否を確認する。
func (s *Store) ensureWritableLocked() error {
	if s.writeBlocked {
		return s.writeBlockedErrorLocked()
	}
	current, err := statStamp(s.path)
	if !s.hasLastStamp {
		if err == nil {
			s.blockWritesLocked("既存のtasks.jsonをまだ正常に読み込んでいません")
			return s.writeBlockedErrorLocked()
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		s.blockWritesLocked("tasks.jsonの状態を確認できませんでした")
		return s.writeBlockedErrorLocked()
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.blockWritesLocked("読み込み後にtasks.jsonが削除されました")
		} else {
			s.blockWritesLocked("tasks.jsonの状態を確認できませんでした")
		}
		return s.writeBlockedErrorLocked()
	}
	if !sameFileStamp(current, s.lastStamp) {
		s.blockWritesLocked("読み込み後にtasks.jsonが外部変更されました")
		return s.writeBlockedErrorLocked()
	}
	return nil
}

func (s *Store) rememberStampLocked(stamp FileStamp) {
	s.lastStamp = stamp
	s.hasLastStamp = true
	s.clearWriteBlockLocked()
}

func (s *Store) blockWritesLocked(reason string) {
	s.writeBlocked = true
	s.writeBlockReason = reason
}

func (s *Store) clearWriteBlockLocked() {
	s.writeBlocked = false
	s.writeBlockReason = ""
}

func (s *Store) writeBlockedErrorLocked() error {
	reason := s.writeBlockReason
	if reason == "" {
		reason = "tasks.jsonの安全性を確認できません"
	}
	return fmt.Errorf("tasks.jsonの保存を停止しています: %s。ファイルを修正または復元してから再読み込みしてください", reason)
}

func sameFileStamp(left, right FileStamp) bool {
	return left.Size == right.Size && left.ModTime.Equal(right.ModTime)
}

func validateForLoad(data *TaskFile) ([]string, error) {
	if data.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("未対応のformat_versionです: %d", data.FormatVersion)
	}
	if data.Tasks == nil {
		data.Tasks = []Task{}
	}
	if data.Periods == nil {
		data.Periods = DefaultPeriods()
	}
	if data.History == nil {
		data.History = []HistoryEntry{}
	}
	periodIDs := make(map[string]struct{}, len(data.Periods))
	for i := range data.Periods {
		if err := data.Periods[i].Validate(); err != nil {
			return nil, fmt.Errorf("periods[%d]: %w", i, err)
		}
		if _, exists := periodIDs[data.Periods[i].ID]; exists {
			return nil, fmt.Errorf("periods[%d]: idが重複しています: %s", i, data.Periods[i].ID)
		}
		periodIDs[data.Periods[i].ID] = struct{}{}
	}
	ids := make(map[string]struct{}, len(data.Tasks))
	var warnings []string
	for i := range data.Tasks {
		task := &data.Tasks[i]
		if task.ID == "" {
			return nil, fmt.Errorf("tasks[%d]: idが空です", i)
		}
		if _, exists := ids[task.ID]; exists {
			return nil, fmt.Errorf("tasks[%d]: idが重複しています: %s", i, task.ID)
		}
		ids[task.ID] = struct{}{}
		if task.Notification.Method == "" {
			task.Notification.Method = NotificationDialog
		}
		if task.Schedule.IntervalMinutes == 0 {
			task.Schedule.IntervalMinutes = 60
		}
		if task.Schedule.Type != ScheduleDailyFixed && task.Schedule.Type != ScheduleDailyRandomAfter && task.Schedule.Type != ScheduleDailyBefore {
			task.Enabled = false
			warnings = append(warnings, fmt.Sprintf("「%s」は未対応のschedule.type %qのため無効扱いです", task.Title, task.Schedule.Type))
			continue
		}
		if task.Notification.Method != NotificationDialog && task.Notification.Method != NotificationOS {
			task.Enabled = false
			warnings = append(warnings, fmt.Sprintf("「%s」は未対応の通知方法 %qのため無効扱いです", task.Title, task.Notification.Method))
			continue
		}
		if task.Condition.PeriodEnabled {
			if _, exists := periodIDs[task.Condition.PeriodID]; !exists {
				task.Enabled = false
				warnings = append(warnings, fmt.Sprintf("「%s」は期間条件の参照先がないため無効扱いです", task.Title))
				continue
			}
		}
		if err := task.Validate(); err != nil {
			return nil, fmt.Errorf("tasks[%d]: %w", i, err)
		}
	}
	return warnings, nil
}

// Changed は既知の更新日時とサイズから外部編集を検出する。
func (s *Store) Changed(previous FileStamp) (bool, error) {
	current, err := statStamp(s.path)
	if err != nil {
		return false, err
	}
	return !sameFileStamp(current, previous), nil
}

func readTaskFile(path string) (TaskFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return TaskFile{}, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var data TaskFile
	if err := decoder.Decode(&data); err != nil {
		return TaskFile{}, fmt.Errorf("tasks.jsonのJSONが不正です: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return TaskFile{}, errors.New("tasks.jsonに複数のJSON値があります")
		}
		return TaskFile{}, fmt.Errorf("tasks.json末尾が不正です: %w", err)
	}
	return data, nil
}

func statStamp(path string) (FileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileStamp{}, fmt.Errorf("tasks.jsonの状態を取得できません: %w", err)
	}
	return FileStamp{ModTime: info.ModTime(), Size: info.Size()}, nil
}
