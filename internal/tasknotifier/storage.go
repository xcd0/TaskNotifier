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

const temporaryFileSuffix = ".tasknotifier.tmp"

// FileStamp は外部編集検出に必要な最小情報を保持する。
type FileStamp struct {
	ModTime time.Time
	Size    int64
}

// Store はtasks.jsonの直列化された読み書きを担当する。
type Store struct {
	path             string
	mu               sync.Mutex
	lastWarnings     []string
	lastStamp        FileStamp
	hasLastStamp     bool
	writeBlocked     bool
	writeBlockReason string
}

// NewStore は指定したタスク設定ファイルだけを扱う保存器を返す。
func NewStore(path string) *Store {
	return &Store{path: filepath.Clean(path)}
}

// MigrateLegacyTaskFile は旧tasks.txtを検証し、tasks.jsonがまだない場合だけ名前を変更する。
// 両方が存在する場合は、現在のtasks.jsonを優先してどちらも変更しない。
func MigrateLegacyTaskFile(paths Paths) (bool, error) {
	if _, err := os.Stat(paths.Tasks); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("tasks.jsonを確認できません: %w", err)
	}

	legacyStore := NewStore(paths.LegacyTasks)
	if err := legacyStore.RecoverTemporary(); err != nil {
		return false, fmt.Errorf("旧tasks.txtの一時ファイルを復旧できません: %w", err)
	}
	if _, err := os.Stat(paths.LegacyTasks); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("旧tasks.txtを確認できません: %w", err)
	}

	data, err := readTaskFile(paths.LegacyTasks)
	if err != nil {
		return false, fmt.Errorf("旧tasks.txtを検証できません: %w", err)
	}
	if _, err := validateForLoad(&data); err != nil {
		return false, fmt.Errorf("旧tasks.txtを検証できません: %w", err)
	}
	if err := replaceFile(paths.LegacyTasks, paths.Tasks); err != nil {
		return false, fmt.Errorf("旧tasks.txtをtasks.jsonへ変更できません: %w", err)
	}
	return true, nil
}

// Path は管理対象のtasks.jsonパスを返す。
func (s *Store) Path() string {
	return s.path
}

// RecoverTemporary は中断された保存の一時ファイルを安全に整理する。
func (s *Store) RecoverTemporary() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	temporary := s.path + temporaryFileSuffix
	if _, err := os.Stat(temporary); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("一時ファイルを確認できません: %w", err)
	}

	if _, err := os.Stat(s.path); err == nil {
		if err := os.Remove(temporary); err != nil {
			return fmt.Errorf("不要な一時ファイルを削除できません: %w", err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("tasks.jsonを確認できません: %w", err)
	}

	data, err := readTaskFile(temporary)
	if err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("残存一時ファイルは不正なため削除しました: %w", err)
	}
	if err := data.Validate(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("残存一時ファイルは不正なため削除しました: %w", err)
	}
	if err := replaceFile(temporary, s.path); err != nil {
		return fmt.Errorf("残存一時ファイルから復旧できません: %w", err)
	}
	return nil
}

// Load はtasks.jsonを読み込む。存在しない場合だけ空ファイルを作る。
func (s *Store) Load() (TaskFile, FileStamp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := readTaskFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		if s.hasLastStamp {
			s.blockWritesLocked("読み込み後にtasks.jsonが削除されました")
			return TaskFile{}, FileStamp{}, s.writeBlockedErrorLocked()
		}
		// 不正ファイルを利用者が削除して修復した場合は、空ファイルから再開できるようにする。
		s.clearWriteBlockLocked()
		data = EmptyTaskFile()
		if _, err := s.saveLocked(data); err != nil {
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
	stamp, err := statStamp(s.path)
	if err != nil {
		s.blockWritesLocked("tasks.jsonの状態を確認できませんでした")
		return TaskFile{}, FileStamp{}, err
	}
	s.lastWarnings = warnings
	s.rememberStampLocked(stamp)
	return data, stamp, nil
}

// Save は検証済みJSONを一時ファイル経由で原子的に置換する。
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

	encoded, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return FileStamp{}, fmt.Errorf("JSONを生成できません: %w", err)
	}
	encoded = append(encoded, '\n')
	if bytes.HasPrefix(encoded, []byte{0xef, 0xbb, 0xbf}) {
		return FileStamp{}, errors.New("内部エラー: JSONにBOMが含まれています")
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return FileStamp{}, fmt.Errorf("保存先ディレクトリを準備できません: %w", err)
	}
	temporary := s.path + temporaryFileSuffix
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return FileStamp{}, fmt.Errorf("一時ファイルを作成できません: %w", err)
	}

	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()

	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return FileStamp{}, fmt.Errorf("一時ファイルへ書き込めません: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return FileStamp{}, fmt.Errorf("一時ファイルを同期できません: %w", err)
	}
	if err := file.Close(); err != nil {
		return FileStamp{}, fmt.Errorf("一時ファイルを閉じられません: %w", err)
	}
	// JSON生成中に外部編集された場合も、置換直前に検出して利用者の変更を保護する。
	if err := s.ensureWritableLocked(); err != nil {
		return FileStamp{}, err
	}
	if err := replaceFile(temporary, s.path); err != nil {
		return FileStamp{}, fmt.Errorf("tasks.jsonを置換できません: %w", err)
	}
	removeTemporary = false
	s.lastWarnings = nil

	stamp, err := statStamp(s.path)
	if err != nil {
		return FileStamp{}, err
	}
	s.rememberStampLocked(stamp)
	return stamp, nil
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

// rememberStampLocked は正常に読み込んだ、または保存したファイルの状態を記録する。
func (s *Store) rememberStampLocked(stamp FileStamp) {
	s.lastStamp = stamp
	s.hasLastStamp = true
	s.clearWriteBlockLocked()
}

// blockWritesLocked は次回の正常な再読み込みまで保存を停止する。
func (s *Store) blockWritesLocked(reason string) {
	s.writeBlocked = true
	s.writeBlockReason = reason
}

// clearWriteBlockLocked は正常な再読み込みまたは新規作成後に保存停止を解除する。
func (s *Store) clearWriteBlockLocked() {
	s.writeBlocked = false
	s.writeBlockReason = ""
}

// writeBlockedErrorLocked は利用者へ復旧方法を含む保存停止エラーを返す。
func (s *Store) writeBlockedErrorLocked() error {
	reason := s.writeBlockReason
	if reason == "" {
		reason = "tasks.jsonの安全性を確認できません"
	}
	return fmt.Errorf("tasks.jsonの保存を停止しています: %s。ファイルを修正または復元してから再読み込みしてください", reason)
}

// sameFileStamp は更新日時とサイズが一致するかを確認する。
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
		// 旧形式のtasks.txtには期間がないため、初期3期間をメモリ上で補う。
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
