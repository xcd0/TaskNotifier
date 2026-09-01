package tasknotifier

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	PauseModeOneHour  = "one_hour"
	PauseModeToday    = "today"
	PauseModeWorkEnd  = "work_end"
	PauseModeTomorrow = "tomorrow"
	PauseModeResume   = "resume"
)

// DuplicateTask は指定タスクを新しいIDと初期状態で複製する。
func DuplicateTask(data TaskFile, id string) (TaskFile, string, error) {
	data = cloneTaskFile(data)
	index := taskIndex(data, id)
	if index < 0 {
		return data, "", errors.New("複製対象のタスクが見つかりません")
	}

	duplicated := data.Tasks[index]
	newID, err := NewTaskID()
	if err != nil {
		return data, "", fmt.Errorf("複製タスクのIDを生成できません: %w", err)
	}
	duplicated.ID = newID
	duplicated.Title = strings.TrimSpace(duplicated.Title) + " (コピー)"
	duplicated.State = State{}

	data.Tasks = append(data.Tasks, Task{})
	copy(data.Tasks[index+2:], data.Tasks[index+1:])
	data.Tasks[index+1] = duplicated
	return data, newID, nil
}

// PauseTask は指定モードに応じてタスクの通知を一時停止または再開する。
func PauseTask(data TaskFile, id, mode string, now time.Time) (TaskFile, error) {
	data = cloneTaskFile(data)
	index := taskIndex(data, id)
	if index < 0 {
		return data, errors.New("一時停止対象のタスクが見つかりません")
	}
	if data.Tasks[index].Kind == TaskKindTodo {
		return data, errors.New("TODOタスクは一時停止の対象外です")
	}

	if mode == PauseModeResume {
		data.Tasks[index].State.PausedUntil = ""
		return data, nil
	}

	until, err := pauseUntil(data.Periods, mode, now)
	if err != nil {
		return data, err
	}
	data.Tasks[index].State.PausedUntil = until.Format(time.RFC3339)
	data.Tasks[index].State.SnoozeUntil = ""
	return data, nil
}

// pauseUntil は一時停止モードを具体的な終了日時へ変換する。
func pauseUntil(periods []Period, mode string, now time.Time) (time.Time, error) {
	switch mode {
	case PauseModeOneHour:
		return now.Add(time.Hour), nil
	case PauseModeToday:
		return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()), nil
	case PauseModeWorkEnd:
		endHour, endMinute := 18, 0
		if work, ok := PeriodByID(periods, "work"); ok && work.EndEnabled {
			if hour, minute, err := ParseClock(work.EndTime); err == nil {
				endHour, endMinute = hour, minute
			}
		}
		until := time.Date(now.Year(), now.Month(), now.Day(), endHour, endMinute, 0, 0, now.Location())
		if !until.After(now) {
			until = until.AddDate(0, 0, 1)
		}
		return until, nil
	case PauseModeTomorrow:
		return time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, now.Location()), nil
	default:
		return time.Time{}, fmt.Errorf("未対応の一時停止モードです: %q", mode)
	}
}
