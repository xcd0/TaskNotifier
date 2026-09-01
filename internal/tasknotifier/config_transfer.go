package tasknotifier

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// ExportTaskConfig は実行状態や履歴を含めず、設定だけを指定ファイルへ保存する。
func ExportTaskConfig(path string, data TaskFile) error {
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
	if err := writeJSONAtomic(path, config); err != nil {
		return fmt.Errorf("設定をエクスポートできません: %w", err)
	}
	return nil
}

// ImportTaskConfig は設定専用JSONを読み込み、実行状態を持たないTaskFileへ変換する。
func ImportTaskConfig(path string) (TaskFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return TaskFile{}, fmt.Errorf("設定ファイルを開けません: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var config taskConfigFile
	if err := decoder.Decode(&config); err != nil {
		return TaskFile{}, fmt.Errorf("設定JSONが不正です: %w", err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return TaskFile{}, errors.New("設定JSONに複数の値があります")
		}
		return TaskFile{}, fmt.Errorf("設定JSON末尾が不正です: %w", err)
	}

	data := TaskFile{
		FormatVersion: config.FormatVersion,
		Periods:       append([]Period(nil), config.Periods...),
		Tasks:         make([]Task, 0, len(config.Tasks)),
		History:       []HistoryEntry{},
	}
	for _, task := range config.Tasks {
		data.Tasks = append(data.Tasks, Task{
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
	if _, err := validateForLoad(&data); err != nil {
		return TaskFile{}, fmt.Errorf("設定を検証できません: %w", err)
	}
	return data, nil
}

// ApplyImportedConfig はインポート設定へ現在の実行状態と履歴を可能な範囲で引き継ぐ。
func ApplyImportedConfig(current, imported TaskFile) TaskFile {
	states := make(map[string]State, len(current.Tasks))
	for _, task := range current.Tasks {
		states[task.ID] = task.State
	}
	for index := range imported.Tasks {
		if state, exists := states[imported.Tasks[index].ID]; exists {
			imported.Tasks[index].State = state
		}
	}
	imported.History = append([]HistoryEntry(nil), current.History...)
	return imported
}
