//go:build windows

package tasknotifier

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jchv/go-webview2/pkg/edge"
	"github.com/lxn/walk"
)

//go:embed webui_dist/index.html
var embeddedWebUI string

type webRPCRequest struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type webRPCResponse struct {
	ID     int         `json:"id"`
	OK     bool        `json:"ok"`
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

type webTaskView struct {
	Task             Task   `json:"task"`
	ScheduleText     string `json:"schedule_text"`
	ConditionText    string `json:"condition_text"`
	NotificationText string `json:"notification_text"`
	NextText         string `json:"next_text"`
	BATText          string `json:"bat_text"`
}

type webHistoryView struct {
	ScheduledAt string `json:"scheduled_at"`
	NotifiedAt  string `json:"notified_at"`
	TaskTitle   string `json:"task_title"`
	Method      string `json:"method"`
	Result      string `json:"result"`
}

type webAutostartView struct {
	Enabled  bool   `json:"enabled"`
	Mismatch bool   `json:"mismatch"`
	Error    string `json:"error"`
}

type webAppState struct {
	Tasks     []webTaskView    `json:"tasks"`
	Periods   []Period         `json:"periods"`
	History   []webHistoryView `json:"history"`
	Status    string           `json:"status"`
	Autostart webAutostartView `json:"autostart"`
	Version   string           `json:"version"`
}

type webIDParams struct {
	ID string `json:"id"`
}

type webTaskParams struct {
	Task Task `json:"task"`
}

type webPeriodParams struct {
	Period Period `json:"period"`
}

type webAutostartParams struct {
	Enabled bool `json:"enabled"`
}

type webFrontendLogParams struct {
	Stage  string `json:"stage"`
	Detail string `json:"detail"`
}

// createWebView はWalkのメインウィンドウ内へWebView2コントローラーを埋め込む。
func (app *App) createWebView() error {
	htmlHash := sha256.Sum256([]byte(embeddedWebUI))
	log.Printf("webview create begin host_hwnd=%#x host_bounds=%+v html_bytes=%d html_sha256=%x", app.webHost.Handle(), app.webHost.BoundsPixels(), len(embeddedWebUI), htmlHash)
	dataPath := filepath.Join(os.TempDir(), "TaskNotifier", "WebView2")
	if err := os.MkdirAll(dataPath, 0o700); err != nil {
		return fmt.Errorf("WebView2の一時データ領域を作成できません: %w", err)
	}

	browser := edge.NewChromium()
	browser.DataPath = dataPath
	browser.MessageCallback = app.handleWebMessage
	browser.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		log.Printf("webview navigation completed")
	}
	browser.SetGlobalPermission(edge.CoreWebView2PermissionStateDeny)
	app.browser = browser

	log.Printf("webview embed begin data_path=%q", dataPath)
	if !browser.Embed(uintptr(app.webHost.Handle())) {
		app.browser = nil
		return errors.New("WebView2 Runtimeを開始できません。Windows Updateを確認してください")
	}
	log.Printf("webview embed complete")
	settings, err := browser.GetSettings()
	if err != nil {
		return fmt.Errorf("WebView2設定を取得できません: %w", err)
	}
	for _, setting := range []struct {
		name  string
		apply func() error
	}{
		{name: "開発者ツール", apply: func() error { return settings.PutAreDevToolsEnabled(false) }},
		{name: "コンテキストメニュー", apply: func() error { return settings.PutAreDefaultContextMenusEnabled(false) }},
		{name: "ステータスバー", apply: func() error { return settings.PutIsStatusBarEnabled(false) }},
		{name: "ズーム操作", apply: func() error { return settings.PutIsZoomControlEnabled(false) }},
		{name: "ブラウザーショートカット", apply: func() error { return settings.PutAreBrowserAcceleratorKeysEnabled(false) }},
		{name: "スクリプトダイアログ", apply: func() error { return settings.PutAreDefaultScriptDialogsEnabled(true) }},
	} {
		if err := setting.apply(); err != nil {
			return fmt.Errorf("WebView2の%s設定を変更できません: %w", setting.name, err)
		}
		log.Printf("webview setting applied name=%q", setting.name)
	}
	app.webHost.SizeChanged().Attach(browser.Resize)
	browser.Resize()
	if err := browser.Show(); err != nil {
		return fmt.Errorf("WebView2コントローラーを表示できません: %w", err)
	}
	log.Printf("webview navigate begin")
	browser.NavigateToString(embeddedWebUI)
	log.Printf("webview create complete")
	return nil
}

func (app *App) handleWebMessage(message string) {
	var request webRPCRequest
	if err := json.Unmarshal([]byte(message), &request); err != nil {
		log.Printf("Web UI要求を解析できません: %v", err)
		return
	}
	log.Printf("web rpc begin id=%d method=%q", request.ID, request.Method)
	result, err := app.dispatchWebRequest(request)
	if request.ID == 0 {
		if err != nil {
			log.Printf("web rpc one-way failed method=%q error=%v", request.Method, err)
		}
		return
	}
	response := webRPCResponse{ID: request.ID, OK: err == nil, Result: result}
	if err != nil {
		response.Error = err.Error()
		log.Printf("web rpc failed id=%d method=%q error=%v", request.ID, request.Method, err)
	} else {
		log.Printf("web rpc complete id=%d method=%q", request.ID, request.Method)
	}
	app.sendWebResponse(response)
}

func (app *App) dispatchWebRequest(request webRPCRequest) (interface{}, error) {
	switch request.Method {
	case "frontend_log":
		params, err := decodeWebParams[webFrontendLogParams](request.Params)
		if err != nil {
			return nil, err
		}
		params.Stage = strings.TrimSpace(params.Stage)
		params.Detail = strings.TrimSpace(params.Detail)
		params.Stage = truncateRunes(params.Stage, 128)
		params.Detail = truncateRunes(params.Detail, 2048)
		log.Printf("web frontend stage=%q detail=%q", params.Stage, params.Detail)
		if params.Stage == "frontend_ready" {
			app.frontendReady = true
		}
		return nil, nil
	case "get_state":
		return app.buildWebState(), nil
	case "save_task":
		params, err := decodeWebParams[webTaskParams](request.Params)
		if err != nil { return nil, err }
		if err := app.saveTaskFromWeb(params.Task); err != nil { return nil, err }
		return app.buildWebState(), nil
	case "delete_task":
		params, err := decodeWebParams[webIDParams](request.Params); if err != nil { return nil, err }
		if err := app.deleteTaskFromWeb(params.ID); err != nil { return nil, err }
		return app.buildWebState(), nil
	case "toggle_task":
		params, err := decodeWebParams[webIDParams](request.Params); if err != nil { return nil, err }
		if err := app.toggleTaskFromWeb(params.ID); err != nil { return nil, err }
		return app.buildWebState(), nil
	case "toggle_notification":
		params, err := decodeWebParams[webIDParams](request.Params); if err != nil { return nil, err }
		if err := app.toggleNotificationFromWeb(params.ID); err != nil { return nil, err }
		return app.buildWebState(), nil
	case "toggle_completed":
		params, err := decodeWebParams[webIDParams](request.Params); if err != nil { return nil, err }
		if err := app.toggleTaskCompletedFromWeb(params.ID); err != nil { return nil, err }
		return app.buildWebState(), nil
	case "save_period":
		params, err := decodeWebParams[webPeriodParams](request.Params); if err != nil { return nil, err }
		if err := app.savePeriodFromWeb(params.Period); err != nil { return nil, err }
		return app.buildWebState(), nil
	case "delete_period":
		params, err := decodeWebParams[webIDParams](request.Params); if err != nil { return nil, err }
		if err := app.deletePeriodFromWeb(params.ID); err != nil { return nil, err }
		return app.buildWebState(), nil
	case "clear_history":
		data := app.snapshot(); data.History = []HistoryEntry{}
		if err := app.save(data); err != nil { return nil, fmt.Errorf("通知履歴を消去できません: %w", err) }
		return app.buildWebState(), nil
	case "reload":
		if err := app.reload(true); err != nil { return nil, err }
		return app.buildWebState(), nil
	case "open_tasks":
		if err := exec.Command("notepad.exe", app.paths.Tasks).Start(); err != nil { return nil, fmt.Errorf("tasks.jsonを開けません: %w", err) }
		return nil, nil
	case "schedule_test":
		app.scheduleTestNotification(); return app.buildWebState(), nil
	case "pomodoro_notify":
		params, err := decodeWebParams[webFrontendLogParams](request.Params); if err != nil { return nil, err }
		title := strings.TrimSpace(params.Stage); if title == "" { title = "TaskNotifier Pomodoro" }
		if err := app.notifyIcon.ShowInfo(truncateRunes(title, 80), truncateRunes(strings.TrimSpace(params.Detail), 256)); err != nil { return nil, fmt.Errorf("Pomodoro通知を表示できません: %w", err) }
		return nil, nil
	case "set_autostart":
		params, err := decodeWebParams[webAutostartParams](request.Params); if err != nil { return nil, err }
		if err := SetAutostart(app.paths.Executable, params.Enabled); err != nil { return nil, fmt.Errorf("自動起動設定を変更できません: %w", err) }
		app.refreshAll(); return app.buildWebState(), nil
	case "update_autostart":
		if err := SetAutostart(app.paths.Executable, true); err != nil { return nil, fmt.Errorf("自動起動の登録パスを更新できません: %w", err) }
		app.setStatus("自動起動のEXEパスを更新しました"); return app.buildWebState(), nil
	case "choose_batch":
		return app.chooseBatchFile()
	default:
		return nil, fmt.Errorf("未対応の画面操作です: %s", request.Method)
	}
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum { return value }
	return string(runes[:maximum])
}

func decodeWebParams[T any](raw json.RawMessage) (T, error) {
	var params T
	if len(raw) == 0 { return params, nil }
	if err := json.Unmarshal(raw, &params); err != nil { return params, fmt.Errorf("画面からの入力が不正です: %w", err) }
	return params, nil
}

func (app *App) sendWebResponse(response webRPCResponse) {
	if app.browser == nil { return }
	encoded, err := json.Marshal(response)
	if err != nil { log.Printf("Web UI応答を生成できません: %v", err); return }
	app.browser.Eval("window.taskNotifierBridge.receive(" + string(encoded) + ");")
}

func (app *App) publishWebState() {
	if app.browser == nil { return }
	encoded, err := json.Marshal(app.buildWebState())
	if err != nil { log.Printf("Web UI状態を生成できません: %v", err); return }
	app.browser.Eval("window.taskNotifierApp&&window.taskNotifierApp.setState(" + string(encoded) + ");")
}

func (app *App) buildWebState() webAppState {
	data := app.snapshot()
	app.mu.RLock(); statusText := app.statusText; app.mu.RUnlock()
	now := time.Now()
	state := webAppState{Version: BuildVersion, Tasks: make([]webTaskView, 0, len(data.Tasks)), Periods: append([]Period(nil), data.Periods...), History: make([]webHistoryView, 0, len(data.History)), Status: statusText}
	if state.Status == "" { state.Status = "起動中..." }
	if status, err := ReadAutostart(app.paths.Executable); err != nil { state.Autostart.Error = err.Error() } else { state.Autostart.Enabled = status.Enabled; state.Autostart.Mismatch = status.Mismatch }
	for _, task := range data.Tasks {
		if task.Kind == "" { task.Kind = TaskKindNotification }
		if task.Kind == TaskKindTodo {
			stateText := "未完了"; if task.State.Completed { stateText = "完了" }
			state.Tasks = append(state.Tasks, webTaskView{Task: task, ScheduleText: "TODO（通知なし）", ConditionText: stateText, NotificationText: "なし", NextText: "-", BATText: "なし"})
			continue
		}
		conditionParts := make([]string, 0, 2)
		if task.Condition.PeriodEnabled { if period, ok := PeriodByID(data.Periods, task.Condition.PeriodID); ok { conditionParts = append(conditionParts, "期間: "+period.Name) } else { conditionParts = append(conditionParts, "期間: 参照なし") } }
		if task.Condition.WeekdaysEnabled { conditionParts = append(conditionParts, "曜日: "+FormatWeekdays(task.Condition.Weekdays)) }
		conditionText := "指定なし"; if len(conditionParts) > 0 { conditionText = strings.Join(conditionParts, " / ") }
		notificationText := "通知OFF"; if NotificationEnabled(task) { notificationText = "独自ダイアログ"; if EffectiveNotificationMethod(task) == NotificationOS { notificationText = "OS通知" } }
		nextText := "-"
		if task.Enabled && NotificationEnabled(task) { candidate := TaskFile{FormatVersion: FormatVersion, Periods: data.Periods, Tasks: []Task{task}}; if next, ok := NextEvent(candidate, now); ok { if next.DueAt.After(now) { nextText = next.DueAt.Format("2006-01-02 15:04") } else { nextText = "通知待ち" } } }
		batText := "なし"; if strings.TrimSpace(task.Action.BatPath) != "" { batText = filepath.Base(task.Action.BatPath) }
		state.Tasks = append(state.Tasks, webTaskView{Task: task, ScheduleText: FormatSchedule(task.Schedule), ConditionText: conditionText, NotificationText: notificationText, NextText: nextText, BATText: batText})
	}
	for index := len(data.History)-1; index >= 0; index-- { entry := data.History[index]; method := "独自ダイアログ"; if entry.Method == NotificationOS { method = "OS通知" }; state.History = append(state.History, webHistoryView{ScheduledAt: formatHistoryTime(entry.ScheduledAt), NotifiedAt: formatHistoryTime(entry.NotifiedAt), TaskTitle: entry.TaskTitle, Method: method, Result: entry.Result}) }
	return state
}

func (app *App) saveTaskFromWeb(candidate Task) error {
	data := app.snapshot(); if candidate.Kind == "" { candidate.Kind = TaskKindNotification }; candidate.Title = strings.TrimSpace(candidate.Title)
	if candidate.Kind == TaskKindTodo { candidate.Schedule = Schedule{Type: ScheduleDailyFixed, Time: "09:00", IntervalMinutes: 60}; candidate.Condition = TaskCondition{}; candidate.Notification = NotificationSettings{}; candidate.Action = TaskAction{} }
	candidate.Schedule.Time = strings.TrimSpace(candidate.Schedule.Time); candidate.Schedule.EndTime = strings.TrimSpace(candidate.Schedule.EndTime); candidate.Condition.PeriodID = strings.TrimSpace(candidate.Condition.PeriodID)
	for index := range candidate.Condition.Weekdays { candidate.Condition.Weekdays[index] = strings.TrimSpace(strings.ToLower(candidate.Condition.Weekdays[index])) }
	candidate.Action.BatPath = strings.TrimSpace(candidate.Action.BatPath)
	if candidate.Schedule.IntervalMinutes < 1 || candidate.Schedule.IntervalMinutes > 1440 { candidate.Schedule.IntervalMinutes = 60 }
	if !candidate.Schedule.RepeatEnabled { candidate.Schedule.EndEnabled = false }
	if !candidate.Condition.PeriodEnabled { candidate.Condition.PeriodID = "" }
	if !candidate.Condition.WeekdaysEnabled { candidate.Condition.Weekdays = []string{} }
	if candidate.Kind == TaskKindNotification && candidate.Notification.Enabled == nil { candidate.Notification.Enabled = boolPointer(true) }
	if candidate.Notification.Method == "" { candidate.Notification.Method = NotificationOS }
	if candidate.ID == "" { created, err := NewTask(); if err != nil { return fmt.Errorf("タスクIDを生成できません: %w", err) }; candidate.ID = created.ID; candidate.State = State{}; if err := candidate.Validate(); err != nil { return err }; data.Tasks = append(data.Tasks, candidate) } else { index := taskIndex(data, candidate.ID); if index < 0 { return errors.New("編集対象のタスクが見つかりません") }; before := data.Tasks[index]; candidate.ID = before.ID; candidate.State = before.State; candidate = ApplyEdit(before, candidate); if err := candidate.Validate(); err != nil { return err }; data.Tasks[index] = candidate; app.queue.RemoveTask(candidate.ID) }
	if err := app.save(data); err != nil { return fmt.Errorf("タスクを保存できません: %w", err) }
	app.reconcileActive(data); app.scan(false); return nil
}

func (app *App) toggleTaskCompletedFromWeb(id string) error {
	data := app.snapshot(); index := taskIndex(data, id); if index < 0 { return errors.New("切り替えるタスクが見つかりません") }; if data.Tasks[index].Kind != TaskKindTodo { return errors.New("完了状態を変更できるのはTODOタスクだけです") }
	data.Tasks[index].State.Completed = !data.Tasks[index].State.Completed; if data.Tasks[index].State.Completed { data.Tasks[index].State.CompletedAt = time.Now().Format(time.RFC3339) } else { data.Tasks[index].State.CompletedAt = "" }
	if err := app.save(data); err != nil { return fmt.Errorf("TODOの完了状態を保存できません: %w", err) }; return nil
}

func (app *App) deleteTaskFromWeb(id string) error { data := app.snapshot(); index := taskIndex(data, id); if index < 0 { return errors.New("削除対象のタスクが見つかりません") }; data.Tasks = append(data.Tasks[:index], data.Tasks[index+1:]...); if err := app.save(data); err != nil { return fmt.Errorf("タスクを削除できません: %w", err) }; app.queue.RemoveTask(id); app.reconcileActive(data); app.scan(false); return nil }
func (app *App) toggleTaskFromWeb(id string) error { data := app.snapshot(); index := taskIndex(data, id); if index < 0 { return errors.New("切り替えるタスクが見つかりません") }; data.Tasks[index].Enabled = !data.Tasks[index].Enabled; if err := app.save(data); err != nil { return fmt.Errorf("タスクを保存できません: %w", err) }; if !data.Tasks[index].Enabled { app.queue.RemoveTask(id) }; app.reconcileActive(data); app.scan(false); return nil }
func (app *App) toggleNotificationFromWeb(id string) error { data := app.snapshot(); index := taskIndex(data, id); if index < 0 { return errors.New("切り替えるタスクが見つかりません") }; if data.Tasks[index].Kind == TaskKindTodo { return errors.New("TODOには通知設定がありません") }; enabled := !NotificationEnabled(data.Tasks[index]); data.Tasks[index].Notification.Enabled = boolPointer(enabled); if err := app.save(data); err != nil { return fmt.Errorf("通知設定を保存できません: %w", err) }; if !enabled { app.queue.RemoveTask(id) }; app.reconcileActive(data); app.scan(false); return nil }

func (app *App) savePeriodFromWeb(candidate Period) error {
	data := app.snapshot(); candidate.Name = strings.TrimSpace(candidate.Name); candidate.StartTime = strings.TrimSpace(candidate.StartTime); candidate.EndTime = strings.TrimSpace(candidate.EndTime)
	if candidate.ID == "" { created, err := NewPeriod(); if err != nil { return fmt.Errorf("期間IDを生成できません: %w", err) }; candidate.ID = created.ID; if err := candidate.Validate(); err != nil { return err }; data.Periods = append(data.Periods, candidate) } else { index := periodIndex(data.Periods, candidate.ID); if index < 0 { return errors.New("編集対象の期間が見つかりません") }; before := data.Periods[index]; candidate.ID = before.ID; if err := candidate.Validate(); err != nil { return err }; data.Periods[index] = candidate; if before.StartEnabled != candidate.StartEnabled || before.StartTime != candidate.StartTime || before.EndEnabled != candidate.EndEnabled || before.EndTime != candidate.EndTime { for taskIndex := range data.Tasks { if data.Tasks[taskIndex].Condition.PeriodEnabled && data.Tasks[taskIndex].Condition.PeriodID == candidate.ID { data.Tasks[taskIndex].State = State{} } } } }
	if err := app.save(data); err != nil { return fmt.Errorf("期間を保存できません: %w", err) }; app.reconcileActive(data); app.scan(false); return nil
}

func (app *App) deletePeriodFromWeb(id string) error {
	data := app.snapshot(); index := periodIndex(data.Periods, id); if index < 0 { return errors.New("削除対象の期間が見つかりません") }; data.Periods = append(data.Periods[:index], data.Periods[index+1:]...)
	for taskIndex := range data.Tasks { if data.Tasks[taskIndex].Condition.PeriodID == id { data.Tasks[taskIndex].Condition = TaskCondition{}; data.Tasks[taskIndex].State = State{} } }
	if err := app.save(data); err != nil { return fmt.Errorf("期間を削除できません: %w", err) }; app.reconcileActive(data); app.scan(false); return nil
}

func (app *App) chooseBatchFile() (string, error) { dialog := walk.FileDialog{Title: "BATまたはCMDファイルを選択", Filter: "Batch files (*.bat;*.cmd)|*.bat;*.cmd|All files (*.*)|*.*"}; selected, err := dialog.ShowOpen(app.mw); if err != nil { return "", fmt.Errorf("ファイルを選択できません: %w", err) }; if !selected { return "", nil }; return dialog.FilePath, nil }
func periodIndex(periods []Period, id string) int { for index := range periods { if periods[index].ID == id { return index } }; return -1 }
func formatHistoryTime(value string) string { parsed, err := time.Parse(time.RFC3339, value); if err != nil { return value }; return parsed.Local().Format("2006-01-02 15:04:05") }
