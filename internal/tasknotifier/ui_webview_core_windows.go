//go:build windows

package tasknotifier

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jchv/go-webview2/pkg/edge"
	"github.com/lxn/walk"
	"log"
	"os"
	"path/filepath"
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
	BatchRuns []BatchRunView   `json:"batch_runs"`
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

type webPauseParams struct {
	ID   string `json:"id"`
	Mode string `json:"mode"`
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
