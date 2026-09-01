//go:build windows

package tasknotifier

import (
	"errors"
	"fmt"
	"github.com/lxn/walk"
	"strings"
	"time"
)

func (app *App) saveTaskFromWeb(candidate Task) error {
	data := app.snapshot()
	if candidate.Kind == "" {
		candidate.Kind = TaskKindNotification
	}
	candidate.Title = strings.TrimSpace(candidate.Title)
	if candidate.Kind == TaskKindTodo {
		candidate.Schedule = Schedule{Type: ScheduleDailyFixed, Time: "09:00", IntervalMinutes: 60}
		candidate.Condition = TaskCondition{}
		candidate.Notification = NotificationSettings{}
		candidate.Action = TaskAction{}
	}
	candidate.Schedule.Time = strings.TrimSpace(candidate.Schedule.Time)
	candidate.Schedule.EndTime = strings.TrimSpace(candidate.Schedule.EndTime)
	candidate.Condition.PeriodID = strings.TrimSpace(candidate.Condition.PeriodID)
	for index := range candidate.Condition.Weekdays {
		candidate.Condition.Weekdays[index] = strings.TrimSpace(strings.ToLower(candidate.Condition.Weekdays[index]))
	}
	candidate.Action.BatPath = strings.TrimSpace(candidate.Action.BatPath)
	if candidate.Schedule.IntervalMinutes < 1 || candidate.Schedule.IntervalMinutes > 1440 {
		candidate.Schedule.IntervalMinutes = 60
	}
	if !candidate.Schedule.RepeatEnabled {
		candidate.Schedule.EndEnabled = false
	}
	if !candidate.Condition.PeriodEnabled {
		candidate.Condition.PeriodID = ""
	}
	if !candidate.Condition.WeekdaysEnabled {
		candidate.Condition.Weekdays = []string{}
	}
	if candidate.Kind == TaskKindNotification && candidate.Notification.Enabled == nil {
		candidate.Notification.Enabled = boolPointer(true)
	}
	if candidate.Notification.Method == "" {
		candidate.Notification.Method = NotificationOS
	}
	if candidate.ID == "" {
		created, err := NewTask()
		if err != nil {
			return fmt.Errorf("タスクIDを生成できません: %w", err)
		}
		candidate.ID = created.ID
		candidate.State = State{}
		if err := candidate.Validate(); err != nil {
			return err
		}
		data.Tasks = append(data.Tasks, candidate)
	} else {
		index := taskIndex(data, candidate.ID)
		if index < 0 {
			return errors.New("編集対象のタスクが見つかりません")
		}
		before := data.Tasks[index]
		candidate.ID = before.ID
		candidate.State = before.State
		candidate = ApplyEdit(before, candidate)
		if err := candidate.Validate(); err != nil {
			return err
		}
		data.Tasks[index] = candidate
		app.queue.RemoveTask(candidate.ID)
	}
	if err := app.save(data); err != nil {
		return fmt.Errorf("タスクを保存できません: %w", err)
	}
	app.reconcileActive(data)
	app.scan(false)
	return nil
}

func (app *App) duplicateTaskFromWeb(id string) error {
	data, _, err := DuplicateTask(app.snapshot(), id)
	if err != nil {
		return err
	}
	if err := app.save(data); err != nil {
		return fmt.Errorf("タスクを複製できません: %w", err)
	}
	app.scan(false)
	return nil
}

func (app *App) pauseTaskFromWeb(id, mode string) error {
	data, err := PauseTask(app.snapshot(), id, mode, time.Now())
	if err != nil {
		return err
	}
	if err := app.save(data); err != nil {
		return fmt.Errorf("一時停止状態を保存できません: %w", err)
	}
	if mode != PauseModeResume {
		app.queue.RemoveTask(id)
	}
	app.reconcileActive(data)
	app.scan(false)
	return nil
}

func (app *App) exportConfigFromWeb() error {
	dialog := walk.FileDialog{Title: "TaskNotifier設定をエクスポート", Filter: "JSON files (*.json)|*.json", FilePath: fmt.Sprintf("TaskNotifier-settings-%s.json", time.Now().Format("20060102"))}
	selected, err := dialog.ShowSave(app.mw)
	if err != nil {
		return fmt.Errorf("エクスポート先を選択できません: %w", err)
	}
	if !selected {
		return nil
	}
	if err := ExportTaskConfig(dialog.FilePath, app.snapshot()); err != nil {
		return err
	}
	app.setStatus("設定をエクスポートしました: " + dialog.FilePath)
	return nil
}

func (app *App) importConfigFromWeb() error {
	dialog := walk.FileDialog{Title: "TaskNotifier設定をインポート", Filter: "JSON files (*.json)|*.json"}
	selected, err := dialog.ShowOpen(app.mw)
	if err != nil {
		return fmt.Errorf("インポート元を選択できません: %w", err)
	}
	if !selected {
		return nil
	}
	imported, err := ImportTaskConfig(dialog.FilePath)
	if err != nil {
		return err
	}
	for index := range imported.Tasks {
		if strings.TrimSpace(imported.Tasks[index].Action.BatPath) == "" {
			continue
		}
		normalized, err := NormalizeBATPath(app.paths.Directory, imported.Tasks[index].Action.BatPath)
		if err != nil {
			return fmt.Errorf("%qのBATパスを解決できません: %w", imported.Tasks[index].Title, err)
		}
		imported.Tasks[index].Action.BatPath = normalized
	}
	data := ApplyImportedConfig(app.snapshot(), imported)
	if err := app.save(data); err != nil {
		return fmt.Errorf("インポート設定を保存できません: %w", err)
	}
	app.queue = NewNotificationQueue()
	app.reconcileActive(data)
	app.setStatus("設定をインポートしました: " + dialog.FilePath)
	app.scan(false)
	return nil
}

func (app *App) toggleTaskCompletedFromWeb(id string) error {
	data := app.snapshot()
	index := taskIndex(data, id)
	if index < 0 {
		return errors.New("切り替えるタスクが見つかりません")
	}
	if data.Tasks[index].Kind != TaskKindTodo {
		return errors.New("完了状態を変更できるのはTODOタスクだけです")
	}
	data.Tasks[index].State.Completed = !data.Tasks[index].State.Completed
	if data.Tasks[index].State.Completed {
		data.Tasks[index].State.CompletedAt = time.Now().Format(time.RFC3339)
	} else {
		data.Tasks[index].State.CompletedAt = ""
	}
	if err := app.save(data); err != nil {
		return fmt.Errorf("TODOの完了状態を保存できません: %w", err)
	}
	return nil
}

func (app *App) deleteTaskFromWeb(id string) error {
	data := app.snapshot()
	index := taskIndex(data, id)
	if index < 0 {
		return errors.New("削除対象のタスクが見つかりません")
	}
	data.Tasks = append(data.Tasks[:index], data.Tasks[index+1:]...)
	if err := app.save(data); err != nil {
		return fmt.Errorf("タスクを削除できません: %w", err)
	}
	app.queue.RemoveTask(id)
	app.reconcileActive(data)
	app.scan(false)
	return nil
}
func (app *App) toggleTaskFromWeb(id string) error {
	data := app.snapshot()
	index := taskIndex(data, id)
	if index < 0 {
		return errors.New("切り替えるタスクが見つかりません")
	}
	data.Tasks[index].Enabled = !data.Tasks[index].Enabled
	if err := app.save(data); err != nil {
		return fmt.Errorf("タスクを保存できません: %w", err)
	}
	if !data.Tasks[index].Enabled {
		app.queue.RemoveTask(id)
	}
	app.reconcileActive(data)
	app.scan(false)
	return nil
}
func (app *App) toggleNotificationFromWeb(id string) error {
	data := app.snapshot()
	index := taskIndex(data, id)
	if index < 0 {
		return errors.New("切り替えるタスクが見つかりません")
	}
	if data.Tasks[index].Kind == TaskKindTodo {
		return errors.New("TODOには通知設定がありません")
	}
	enabled := !NotificationEnabled(data.Tasks[index])
	data.Tasks[index].Notification.Enabled = boolPointer(enabled)
	if err := app.save(data); err != nil {
		return fmt.Errorf("通知設定を保存できません: %w", err)
	}
	if !enabled {
		app.queue.RemoveTask(id)
	}
	app.reconcileActive(data)
	app.scan(false)
	return nil
}
