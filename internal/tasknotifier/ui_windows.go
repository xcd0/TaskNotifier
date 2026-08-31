//go:build windows

package tasknotifier

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// App はGUI、常駐スケジューラ、tasks.jsonの状態をまとめる小さな制御構造である。
type App struct {
	paths Paths
	store *Store

	mu    sync.RWMutex
	data  TaskFile
	stamp FileStamp

	mw         *walk.MainWindow
	webHost    *walk.Composite
	browser    *edge.Chromium
	notifyIcon *walk.NotifyIcon
	icon       *walk.Icon

	queue       *NotificationQueue
	active      *Event
	popup       *walk.Dialog
	wake        chan struct{}
	done        chan struct{}
	exiting     bool
	statusError string
	statusText  string
	osAttempts  map[string]time.Time

	frontendReady        bool
	frontendWarningShown bool
}

// RunApp はメインウィンドウと通知ループを開始する。
func RunApp(paths Paths, store *Store, data TaskFile, stamp FileStamp, background bool, startupError string) error {
	log.Printf("ui RunApp begin background=%t startup_error=%t", background, startupError != "")
	app := &App{
		paths:      paths,
		store:      store,
		data:       cloneTaskFile(data),
		stamp:      stamp,
		queue:      NewNotificationQueue(),
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
		osAttempts: make(map[string]time.Time),
	}
	if startupError != "" {
		app.statusError = startupError
	} else if warnings := store.Warnings(); len(warnings) > 0 {
		app.statusError = strings.Join(warnings, " / ")
	}

	if embedded, err := walk.NewIconFromResourceId(2); err == nil {
		app.icon = embedded
	} else {
		app.icon = walk.IconInformation()
	}
	if err := app.createMainWindow(); err != nil {
		return err
	}
	app.attachActivationWebViewInitialization()
	if err := app.createNotifyIcon(); err != nil {
		app.mw.Dispose()
		return err
	}
	defer app.notifyIcon.Dispose()

	app.mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		log.Printf("ui lifecycle closing reason=%d exiting=%t", reason, app.exiting)
		if app.exiting {
			return
		}
		if reason != walk.CloseReasonUser {
			return
		}
		*canceled = true
		app.mw.Hide()
		app.setStatus("タスクトレイで常駐中です")
	})
	app.mw.Starting().Attach(func() {
		log.Printf("ui lifecycle starting")
		app.refreshAll()
		app.scan(true)
		go app.schedulerLoop()
		// バックグラウンド起動直後に二重起動側から表示された場合も、WebView2を初期化する。
		if !background || app.mw.Visible() {
			app.showMainWindow()
		}
	})

	app.mw.Run()
	log.Printf("ui RunApp complete")
	return nil
}

func (app *App) createMainWindow() error {
	if err := (MainWindow{
		AssignTo: &app.mw,
		Title:    MainWindowTitle,
		Icon:     app.icon,
		Size:     Size{Width: 1180, Height: 720},
		MinSize:  Size{Width: 900, Height: 560},
		Visible:  false,
		Layout:   VBox{MarginsZero: true},
		Children: []Widget{
			Composite{
				AssignTo: &app.webHost,
				Layout:   VBox{MarginsZero: true},
			},
		},
	}).Create(); err != nil {
		return fmt.Errorf("メイン画面を作成できません: %w", err)
	}
	// WebView2が作成する子ウィンドウをWalk側の再描画で覆わないようにする。
	hostHandle := app.webHost.Handle()
	oldStyle := win.GetWindowLong(hostHandle, win.GWL_STYLE)
	newStyle := oldStyle | int32(win.WS_CLIPCHILDREN)
	win.SetWindowLong(hostHandle, win.GWL_STYLE, newStyle)
	win.SetWindowPos(hostHandle, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
	log.Printf("ui main window created hwnd=%#x host_hwnd=%#x host_style_before=%#x host_style_after=%#x host_bounds=%+v", app.mw.Handle(), hostHandle, uint32(oldStyle), uint32(newStyle), app.webHost.BoundsPixels())
	return nil
}

// attachActivationWebViewInitialization は二重起動側から復元・前面化されたウィンドウにもWebView2を作成する。
func (app *App) attachActivationWebViewInitialization() {
	app.mw.Activating().Attach(func() {
		if app.exiting || !app.mw.Visible() || app.browser != nil {
			return
		}
		log.Printf("ui main window activated without webview; initialization scheduled")
		// WM_ACTIVATEの通知処理中を避け、WalkのUIループへ戻ってからWebView2を作成する。
		app.mw.Synchronize(func() {
			if app.exiting || !app.mw.Visible() || app.browser != nil {
				return
			}
			if err := app.activateWebView(); err != nil {
				log.Printf("ui externally activated webview activation failed: %v", err)
				app.showError("WebView2管理画面を表示できません", err)
				return
			}
			log.Printf("ui externally activated webview activation complete host_bounds=%+v", app.webHost.BoundsPixels())
		})
	})
}

func (app *App) createNotifyIcon() error {
	log.Printf("ui notify icon create begin")
	notifyIcon, err := walk.NewNotifyIcon(app.mw)
	if err != nil {
		return fmt.Errorf("タスクトレイアイコンを作成できません: %w", err)
	}
	app.notifyIcon = notifyIcon
	if err := notifyIcon.SetIcon(app.icon); err != nil {
		return err
	}
	if err := notifyIcon.SetToolTip("TaskNotifier"); err != nil {
		return err
	}

	addAction := func(text string, handler func()) error {
		action := walk.NewAction()
		if err := action.SetText(text); err != nil {
			return err
		}
		action.Triggered().Attach(handler)
		return notifyIcon.ContextMenu().Actions().Add(action)
	}
	if err := addAction("開く", app.showMainWindow); err != nil {
		return err
	}
	if err := addAction("設定ファイルを開く", func() {
		if err := exec.Command("notepad.exe", app.paths.Tasks).Start(); err != nil {
			app.showError("tasks.jsonを開けません", err)
		}
	}); err != nil {
		return err
	}
	if err := addAction("設定フォルダを開く", func() {
		if err := exec.Command("explorer.exe", "/select,", app.paths.Tasks).Start(); err != nil {
			app.showError("設定フォルダを開けません", err)
		}
	}); err != nil {
		return err
	}
	if err := addAction("ログフォルダを開く", func() {
		if err := exec.Command("explorer.exe", "/select,", app.paths.Log).Start(); err != nil {
			app.showError("ログフォルダを開けません", err)
		}
	}); err != nil {
		return err
	}
	if err := addAction("1分後にテスト通知", app.scheduleTestNotification); err != nil {
		return err
	}
	if err := addAction("終了", app.exit); err != nil {
		return err
	}
	notifyIcon.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			app.showMainWindow()
		}
	})
	if err := notifyIcon.SetVisible(true); err != nil {
		return err
	}
	log.Printf("ui notify icon create complete")
	return nil
}

func (app *App) schedulerLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	timer := time.NewTimer(app.nextDelay())
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			app.synchronizeScan()
		case <-ticker.C:
			app.synchronizeScan()
		case <-app.wake:
		case <-app.done:
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(app.nextDelay())
	}
}

func (app *App) synchronizeScan() {
	app.mw.Synchronize(func() {
		if !app.exiting {
			app.scan(true)
		}
	})
}

func (app *App) nextDelay() time.Duration {
	now := time.Now()
	if next, ok := NextEvent(app.snapshot(), now); ok {
		delay := next.DueAt.Sub(now)
		if delay < 30*time.Second {
			// 期限超過イベントでも1秒周期にせず、保険チェックと同じ間隔にする。
			return 30 * time.Second
		}
		return delay
	}
	return 24 * time.Hour
}

func (app *App) scan(checkFile bool) {
	if checkFile {
		app.reload(false)
	}
	data := app.snapshot()
	due := DueEvents(data, time.Now())
	if app.active != nil {
		filtered := due[:0]
		for _, event := range due {
			// 独自ダイアログが残っているタスクは新しい繰り返し通知を積まない。
			if event.TaskID != app.active.TaskID {
				filtered = append(filtered, event)
			}
		}
		due = filtered
	}
	var dialogEvents []Event
	for _, event := range due {
		task, ok := taskFromData(data, event.TaskID)
		if ok && EffectiveNotificationMethod(task) == NotificationOS {
			app.sendOSNotification(event)
			continue
		}
		dialogEvents = append(dialogEvents, event)
	}
	app.queue.Add(dialogEvents...)
	app.showNextNotification()
	app.refreshAll()
}

func (app *App) showNextNotification() {
	if app.active != nil {
		return
	}
	event, ok := app.queue.Pop()
	if !ok {
		return
	}
	app.active = &event
	if err := app.showNotificationDialog(event); err != nil {
		app.active = nil
		app.showError("通知画面を表示できません", err)
	}
}

func (app *App) showNotificationDialog(event Event) error {
	var dialog *walk.Dialog
	handled := false
	buttons := []Widget{
		PushButton{Text: "確認済み", OnClicked: func() {
			app.finishPopup(dialog, event, &handled, false, false)
		}},
		PushButton{Text: "10分後", OnClicked: func() {
			app.finishPopup(dialog, event, &handled, true, false)
		}},
	}
	if !event.IsTest {
		if task, ok := app.taskByID(event.TaskID); ok && strings.TrimSpace(task.Action.BatPath) != "" {
			buttons = append(buttons, PushButton{Text: "実行して確認済み", OnClicked: func() {
				app.finishPopup(dialog, event, &handled, false, true)
			}})
		}
	}

	if err := (Dialog{
		AssignTo:  &dialog,
		Title:     "TaskNotifier - 確認してください",
		Icon:      app.icon,
		FixedSize: true,
		MinSize:   Size{Width: 430, Height: 190},
		Layout:    VBox{Margins: Margins{Left: 16, Top: 16, Right: 16, Bottom: 16}, Spacing: 10},
		Children: []Widget{
			Label{Text: event.TaskTitle, Font: Font{PointSize: 14, Bold: true}},
			Label{Text: "予定時刻: " + event.ScheduledAt.Format("2006-01-02 15:04")},
			Label{Text: "確認するまでこの通知は閉じません。"},
			Composite{Layout: HBox{MarginsZero: true, Spacing: 8}, Children: buttons},
		},
	}).Create(app.mw); err != nil {
		return err
	}
	app.popup = dialog
	dialog.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if handled || app.exiting || reason != walk.CloseReasonUser {
			return
		}
		*canceled = true
		app.finishPopup(dialog, event, &handled, true, false)
	})
	dialog.Show()
	placePopupBottomRight(dialog, app.mw)
	win.MessageBeep(win.MB_ICONINFORMATION)
	if err := app.recordHistory(event, NotificationDialog, "表示済み"); err != nil {
		app.showError("通知履歴を保存できません", err)
	}
	return nil
}

func (app *App) sendOSNotification(event Event) {
	if last, exists := app.osAttempts[event.Key]; exists && time.Since(last) < 5*time.Minute {
		return
	}
	app.osAttempts[event.Key] = time.Now()
	message := "予定時刻: " + event.ScheduledAt.Format("2006-01-02 15:04")
	if err := app.notifyIcon.ShowInfo(event.TaskTitle, message); err != nil {
		_ = app.recordHistory(event, NotificationOS, "送信失敗: "+err.Error())
		app.showError("OS通知を送信できません", err)
		return
	}

	current := app.snapshot()
	candidate, err := Acknowledge(current, event)
	if err == nil {
		candidate = AppendHistory(candidate, newHistoryEntry(event, NotificationOS, "送信済み", time.Now()))
		err = app.save(candidate)
	}
	if err != nil {
		app.showError("OS通知の状態を保存できません", err)
		return
	}
	delete(app.osAttempts, event.Key)
}

func (app *App) recordHistory(event Event, method, result string) error {
	data := AppendHistory(app.snapshot(), newHistoryEntry(event, method, result, time.Now()))
	return app.save(data)
}

func newHistoryEntry(event Event, method, result string, notifiedAt time.Time) HistoryEntry {
	return HistoryEntry{
		EventKey:    event.Key,
		TaskID:      event.TaskID,
		TaskTitle:   event.TaskTitle,
		ScheduledAt: event.ScheduledAt.Format(time.RFC3339),
		NotifiedAt:  notifiedAt.Format(time.RFC3339),
		Method:      method,
		Result:      result,
	}
}

func (app *App) finishPopup(dialog *walk.Dialog, event Event, handled *bool, snooze, runBAT bool) {
	if *handled {
		return
	}
	batchResultText := ""
	if runBAT {
		task, ok := app.taskByID(event.TaskID)
		if !ok {
			app.showErrorText("BAT実行", "対象タスクが見つかりません")
			return
		}
		result, err := RunBatchWithResult(app.paths.Directory, task.Action)
		if err != nil {
			resultText := fmt.Sprintf("BAT実行失敗 (exit=%d): %v", result.ExitCode, err)
			if historyErr := app.recordHistory(event, NotificationDialog, resultText); historyErr != nil {
				log.Printf("BAT実行失敗履歴を保存できません: %v", historyErr)
			}
			app.showError("BATを実行できません", err)
			return
		}
		batchResultText = fmt.Sprintf("BAT実行成功 (exit=%d)", result.ExitCode)
	}

	if event.IsTest {
		if snooze {
			app.scheduleTestAfter(event, 10*time.Minute)
		}
	} else {
		current := app.snapshot()
		var candidate TaskFile
		var err error
		if snooze {
			candidate, err = Snooze(current, event, time.Now(), 10*time.Minute)
		} else {
			candidate, err = Acknowledge(current, event)
		}
		if err == nil && batchResultText != "" {
			candidate = AppendHistory(candidate, newHistoryEntry(event, NotificationDialog, batchResultText, time.Now()))
		}
		if err == nil {
			err = app.save(candidate)
		}
		if err != nil {
			app.showError("通知状態を保存できません", err)
			return
		}
	}

	*handled = true
	app.active = nil
	app.popup = nil
	dialog.Accept()
	app.signalWake()
	app.scan(false)
}

func (app *App) scheduleTestNotification() {
	event := Event{
		TaskTitle:   "テスト通知",
		Key:         fmt.Sprintf("test:%d", time.Now().UnixNano()),
		ScheduledAt: time.Now().Add(time.Minute),
		DueAt:       time.Now().Add(time.Minute),
		IsTest:      true,
	}
	app.scheduleTestAfter(event, time.Minute)
	app.setStatus("テスト通知を1分後に予約しました")
}

func (app *App) scheduleTestAfter(event Event, delay time.Duration) {
	time.AfterFunc(delay, func() {
		app.mw.Synchronize(func() {
			if app.exiting {
				return
			}
			event.ScheduledAt = time.Now()
			event.DueAt = event.ScheduledAt
			app.queue.Add(event)
			app.showNextNotification()
		})
	})
}

func (app *App) save(data TaskFile) error {
	stamp, err := app.store.Save(data)
	if err != nil {
		return err
	}
	app.mu.Lock()
	app.data = cloneTaskFile(data)
	app.stamp = stamp
	app.statusError = strings.Join(app.store.Warnings(), " / ")
	app.mu.Unlock()
	app.refreshAll()
	app.signalWake()
	return nil
}

func (app *App) reload(showMessage bool) error {
	if !showMessage {
		app.mu.RLock()
		stamp := app.stamp
		app.mu.RUnlock()
		changed, err := app.store.Changed(stamp)
		if err == nil && !changed {
			return nil
		}
	}
	data, stamp, err := app.store.Load()
	if err != nil {
		app.mu.Lock()
		app.statusError = "再読み込み失敗: " + err.Error()
		app.mu.Unlock()
		app.refreshAll()
		return fmt.Errorf("tasks.jsonを再読み込みできません: %w", err)
	}
	app.mu.Lock()
	app.data = cloneTaskFile(data)
	app.stamp = stamp
	app.statusError = strings.Join(app.store.Warnings(), " / ")
	app.mu.Unlock()
	app.queue = NewNotificationQueue()
	app.reconcileActive(data)
	app.refreshAll()
	app.signalWake()
	if showMessage {
		app.setStatus("tasks.jsonを再読み込みしました")
		app.scan(false)
	}
	return nil
}

func (app *App) reconcileActive(data TaskFile) {
	if app.active == nil || app.active.IsTest {
		return
	}
	valid := false
	for _, event := range DueEvents(data, time.Now()) {
		if event.Key == app.active.Key && event.ScheduledAt.Equal(app.active.ScheduledAt) {
			valid = true
			break
		}
	}
	if valid {
		return
	}
	if app.popup != nil {
		app.popup.Close(walk.DlgCmdCancel)
	}
	app.active = nil
	app.popup = nil
	app.showNextNotification()
}

func (app *App) snapshot() TaskFile {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return cloneTaskFile(app.data)
}

func (app *App) taskByID(id string) (Task, bool) {
	return taskFromData(app.snapshot(), id)
}

func taskFromData(data TaskFile, id string) (Task, bool) {
	index := taskIndex(data, id)
	if index < 0 {
		return Task{}, false
	}
	return data.Tasks[index], true
}

func (app *App) refreshAll() {
	autostartMessage := ""
	status, err := ReadAutostart(app.paths.Executable)
	if err != nil {
		autostartMessage = "自動起動設定を確認できません: " + err.Error()
	} else if status.Mismatch {
		autostartMessage = "自動起動の登録先が現在のEXEと異なります。「登録パスを更新」を押してください"
	}

	app.mu.RLock()
	statusError := app.statusError
	data := cloneTaskFile(app.data)
	app.mu.RUnlock()
	message := ""
	switch {
	case statusError != "":
		message = statusError
	case autostartMessage != "":
		message = autostartMessage
	default:
		if next, ok := NextEvent(data, time.Now()); ok {
			periods := CurrentPeriodNames(data.Periods, time.Now())
			periodText := "該当期間なし"
			if len(periods) > 0 {
				periodText = strings.Join(periods, "・")
			}
			message = "現在: " + periodText + " / 次回通知: " + next.TaskTitle + " / " + next.DueAt.Format("2006-01-02 15:04")
		} else {
			message = "有効な通知はありません"
		}
	}
	app.setStatus(message)
}

func (app *App) setStatus(text string) {
	app.mu.Lock()
	app.statusText = text
	app.mu.Unlock()
	app.publishWebState()
}

func (app *App) showMainWindow() {
	log.Printf("ui show main window begin browser_created=%t", app.browser != nil)
	app.mw.Show()
	win.ShowWindow(app.mw.Handle(), win.SW_RESTORE)
	win.SetForegroundWindow(app.mw.Handle())
	log.Printf("ui main window shown window_bounds=%+v host_bounds=%+v", app.mw.BoundsPixels(), app.webHost.BoundsPixels())
	if err := app.activateWebView(); err != nil {
		log.Printf("ui webview activation failed: %v", err)
		app.showError("WebView2管理画面を表示できません", err)
		return
	}
	log.Printf("ui show main window complete host_bounds=%+v", app.webHost.BoundsPixels())
}

// activateWebView は表示済みのメインウィンドウへWebView2を作成し、表示状態を同期する。
func (app *App) activateWebView() error {
	if app.browser == nil {
		if err := app.createWebView(); err != nil {
			return err
		}
	}
	if err := app.browser.Show(); err != nil {
		log.Printf("ui webview show failed: %v", err)
	}
	if err := app.browser.NotifyParentWindowPositionChanged(); err != nil {
		log.Printf("ui webview parent position notification failed: %v", err)
	}
	app.browser.Resize()
	app.refreshAll()
	app.browser.Focus()
	app.scheduleFrontendWatchdog()
	return nil
}

// scheduleFrontendWatchdog はWeb UI初期化が止まった場合にログの確認先を案内する。
func (app *App) scheduleFrontendWatchdog() {
	time.AfterFunc(8*time.Second, func() {
		select {
		case <-app.done:
			return
		default:
		}
		app.mw.Synchronize(func() {
			if app.exiting || app.frontendReady || app.frontendWarningShown || !app.mw.Visible() {
				return
			}
			app.frontendWarningShown = true
			log.Printf("ui frontend watchdog timeout log_path=%q", app.paths.Log)
			walk.MsgBox(
				app.mw,
				"管理画面の初期化が完了していません",
				"Web UIから初期化完了を確認できませんでした。\n\n診断ログ:\n"+app.paths.Log,
				walk.MsgBoxOK|walk.MsgBoxIconWarning,
			)
		})
	})
}

func (app *App) exit() {
	if app.exiting {
		return
	}
	app.exiting = true
	log.Printf("ui exit begin")
	close(app.done)
	if app.popup != nil {
		app.popup.Close(walk.DlgCmdCancel)
	}
	app.notifyIcon.SetVisible(false)
	app.mw.Close()
}

func (app *App) signalWake() {
	select {
	case app.wake <- struct{}{}:
	default:
	}
}

func (app *App) showError(title string, err error) {
	log.Printf("%s: %v", title, err)
	app.showErrorText(title, err.Error())
}

func (app *App) showErrorText(title, message string) {
	walk.MsgBox(app.mw, title, message, walk.MsgBoxOK|walk.MsgBoxIconError)
}

func placePopupBottomRight(dialog *walk.Dialog, owner *walk.MainWindow) {
	var monitorInfo win.MONITORINFO
	monitorInfo.CbSize = uint32(unsafe.Sizeof(monitorInfo))
	monitor := win.MonitorFromWindow(owner.Handle(), win.MONITOR_DEFAULTTONEAREST)
	if monitor == 0 || !win.GetMonitorInfo(monitor, &monitorInfo) {
		win.SetWindowPos(dialog.Handle(), win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_SHOWWINDOW)
		return
	}
	bounds := dialog.BoundsPixels()
	x := monitorInfo.RcWork.Right - int32(bounds.Width) - 16
	y := monitorInfo.RcWork.Bottom - int32(bounds.Height) - 16
	win.SetWindowPos(dialog.Handle(), win.HWND_TOPMOST, x, y, int32(bounds.Width), int32(bounds.Height), win.SWP_SHOWWINDOW)
}
