package tasknotifier

import (
	"fmt"
	"time"
)

// Acknowledge は保存前コピーへ確認済み状態を反映する。
func Acknowledge(data TaskFile, event Event) (TaskFile, error) {
	data = cloneTaskFile(data)
	index := taskIndex(data, event.TaskID)
	if index < 0 {
		return data, fmt.Errorf("対象タスクが見つかりません: %s", event.TaskID)
	}
	data.Tasks[index].State.LastFiredEvent = event.Key
	data.Tasks[index].State.SnoozeUntil = ""
	return data, nil
}

// Snooze は保存前コピーへ10分後の再通知状態を反映する。
func Snooze(data TaskFile, event Event, now time.Time, duration time.Duration) (TaskFile, error) {
	data = cloneTaskFile(data)
	index := taskIndex(data, event.TaskID)
	if index < 0 {
		return data, fmt.Errorf("対象タスクが見つかりません: %s", event.TaskID)
	}
	data.Tasks[index].State.SnoozeUntil = now.Add(duration).Format(time.RFC3339)
	return data, nil
}

func taskIndex(data TaskFile, id string) int {
	for i := range data.Tasks {
		if data.Tasks[i].ID == id {
			return i
		}
	}
	return -1
}

func cloneTaskFile(data TaskFile) TaskFile {
	cloned := data
	cloned.Periods = append([]Period(nil), data.Periods...)
	cloned.Tasks = append([]Task(nil), data.Tasks...)
	cloned.History = append([]HistoryEntry(nil), data.History...)
	return cloned
}
