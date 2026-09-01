//go:build windows

package tasknotifier

import (
	"log"
	"path/filepath"
	"strings"
	"time"
)

func (app *App) buildWebState() webAppState {
	data := app.snapshot()
	app.mu.RLock()
	statusText := app.statusText
	app.mu.RUnlock()
	now := time.Now()
	state := webAppState{Version: BuildVersion, Tasks: make([]webTaskView, 0, len(data.Tasks)), Periods: append([]Period(nil), data.Periods...), History: make([]webHistoryView, 0, len(data.History)), BatchRuns: []BatchRunView{}, Status: statusText}
	if runs, err := ListBatchRuns(app.paths, now); err != nil {
		log.Printf("BAT実行状況を取得できません: %v", err)
	} else {
		state.BatchRuns = runs
	}
	if state.Status == "" {
		state.Status = "起動中..."
	}
	if status, err := ReadAutostart(app.paths.Executable); err != nil {
		state.Autostart.Error = err.Error()
	} else {
		state.Autostart.Enabled = status.Enabled
		state.Autostart.Mismatch = status.Mismatch
	}
	for _, task := range data.Tasks {
		if task.Kind == "" {
			task.Kind = TaskKindNotification
		}
		if task.Kind == TaskKindTodo {
			stateText := "未完了"
			if task.State.Completed {
				stateText = "完了"
			}
			state.Tasks = append(state.Tasks, webTaskView{Task: task, ScheduleText: "TODO（通知なし）", ConditionText: stateText, NotificationText: "なし", NextText: "-", BATText: "なし"})
			continue
		}
		conditionParts := make([]string, 0, 2)
		if task.Condition.PeriodEnabled {
			if period, ok := PeriodByID(data.Periods, task.Condition.PeriodID); ok {
				conditionParts = append(conditionParts, "期間: "+period.Name)
			} else {
				conditionParts = append(conditionParts, "期間: 参照なし")
			}
		}
		if task.Condition.WeekdaysEnabled {
			conditionParts = append(conditionParts, "曜日: "+FormatWeekdays(task.Condition.Weekdays))
		}
		conditionText := "指定なし"
		if len(conditionParts) > 0 {
			conditionText = strings.Join(conditionParts, " / ")
		}
		notificationText := "通知OFF"
		if NotificationEnabled(task) {
			notificationText = "独自ダイアログ"
			if EffectiveNotificationMethod(task) == NotificationOS {
				notificationText = "OS通知"
			}
		}
		nextText := "-"
		if taskPaused(task, now) {
			if pausedUntil, err := time.Parse(time.RFC3339, task.State.PausedUntil); err == nil {
				nextText = "一時停止: " + pausedUntil.Format("01-02 15:04")
			} else {
				nextText = "一時停止中"
			}
		} else if task.Enabled && NotificationEnabled(task) {
			candidate := TaskFile{FormatVersion: FormatVersion, Periods: data.Periods, Tasks: []Task{task}}
			if next, ok := NextEvent(candidate, now); ok {
				if next.DueAt.After(now) {
					nextText = next.DueAt.Format("2006-01-02 15:04")
				} else {
					nextText = "通知待ち"
				}
			}
		}
		batText := "なし"
		if strings.TrimSpace(task.Action.BatPath) != "" {
			batText = filepath.Base(task.Action.BatPath)
		}
		state.Tasks = append(state.Tasks, webTaskView{Task: task, ScheduleText: FormatSchedule(task.Schedule), ConditionText: conditionText, NotificationText: notificationText, NextText: nextText, BATText: batText})
	}
	for index := len(data.History) - 1; index >= 0; index-- {
		entry := data.History[index]
		method := "独自ダイアログ"
		if entry.Method == NotificationOS {
			method = "OS通知"
		}
		state.History = append(state.History, webHistoryView{ScheduledAt: formatHistoryTime(entry.ScheduledAt), NotifiedAt: formatHistoryTime(entry.NotifiedAt), TaskTitle: entry.TaskTitle, Method: method, Result: entry.Result})
	}
	return state
}
