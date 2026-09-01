//go:build windows

package tasknotifier

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

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
		if err != nil {
			return nil, err
		}
		if err := app.saveTaskFromWeb(params.Task); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "delete_task":
		params, err := decodeWebParams[webIDParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := app.deleteTaskFromWeb(params.ID); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "duplicate_task":
		params, err := decodeWebParams[webIDParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := app.duplicateTaskFromWeb(params.ID); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "pause_task":
		params, err := decodeWebParams[webPauseParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := app.pauseTaskFromWeb(params.ID, params.Mode); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "toggle_task":
		params, err := decodeWebParams[webIDParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := app.toggleTaskFromWeb(params.ID); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "toggle_notification":
		params, err := decodeWebParams[webIDParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := app.toggleNotificationFromWeb(params.ID); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "toggle_completed":
		params, err := decodeWebParams[webIDParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := app.toggleTaskCompletedFromWeb(params.ID); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "save_period":
		params, err := decodeWebParams[webPeriodParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := app.savePeriodFromWeb(params.Period); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "delete_period":
		params, err := decodeWebParams[webIDParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := app.deletePeriodFromWeb(params.ID); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "clear_history":
		data := app.snapshot()
		data.History = []HistoryEntry{}
		if err := app.save(data); err != nil {
			return nil, fmt.Errorf("通知履歴を消去できません: %w", err)
		}
		return app.buildWebState(), nil
	case "reload":
		if err := app.reload(true); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "open_tasks":
		if err := exec.Command("notepad.exe", app.paths.Tasks).Start(); err != nil {
			return nil, fmt.Errorf("tasks.jsonを開けません: %w", err)
		}
		return nil, nil
	case "export_config":
		if err := app.exportConfigFromWeb(); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "import_config":
		if err := app.importConfigFromWeb(); err != nil {
			return nil, err
		}
		return app.buildWebState(), nil
	case "schedule_test":
		app.scheduleTestNotification()
		return app.buildWebState(), nil
	case "pomodoro_notify":
		params, err := decodeWebParams[webFrontendLogParams](request.Params)
		if err != nil {
			return nil, err
		}
		title := strings.TrimSpace(params.Stage)
		if title == "" {
			title = "TaskNotifier Pomodoro"
		}
		if err := app.notifyIcon.ShowInfo(truncateRunes(title, 80), truncateRunes(strings.TrimSpace(params.Detail), 256)); err != nil {
			return nil, fmt.Errorf("Pomodoro通知を表示できません: %w", err)
		}
		return nil, nil
	case "set_autostart":
		params, err := decodeWebParams[webAutostartParams](request.Params)
		if err != nil {
			return nil, err
		}
		if err := SetAutostart(app.paths.Executable, params.Enabled); err != nil {
			return nil, fmt.Errorf("自動起動設定を変更できません: %w", err)
		}
		app.refreshAll()
		return app.buildWebState(), nil
	case "update_autostart":
		if err := SetAutostart(app.paths.Executable, true); err != nil {
			return nil, fmt.Errorf("自動起動の登録パスを更新できません: %w", err)
		}
		app.setStatus("自動起動のEXEパスを更新しました")
		return app.buildWebState(), nil
	case "choose_batch":
		return app.chooseBatchFile()
	default:
		return nil, fmt.Errorf("未対応の画面操作です: %s", request.Method)
	}
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

func decodeWebParams[T any](raw json.RawMessage) (T, error) {
	var params T
	if len(raw) == 0 {
		return params, nil
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return params, fmt.Errorf("画面からの入力が不正です: %w", err)
	}
	return params, nil
}

func (app *App) sendWebResponse(response webRPCResponse) {
	if app.browser == nil {
		return
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		log.Printf("Web UI応答を生成できません: %v", err)
		return
	}
	app.browser.Eval("window.taskNotifierBridge.receive(" + string(encoded) + ");")
}

func (app *App) publishWebState() {
	if app.browser == nil {
		return
	}
	encoded, err := json.Marshal(app.buildWebState())
	if err != nil {
		log.Printf("Web UI状態を生成できません: %v", err)
		return
	}
	app.browser.Eval("window.taskNotifierApp&&window.taskNotifierApp.setState(" + string(encoded) + ");")
}
