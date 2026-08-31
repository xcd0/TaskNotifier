//go:build windows

package tasknotifier

import (
	"errors"
	"fmt"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const (
	MainWindowTitle = "TaskNotifier"
	singleMutexName = `Local\TaskNotifier.1F47181E-65A5-4B99-AD18-693E121EE9C5`
)

type InstanceGuard struct {
	handle windows.Handle
}

func AcquireSingleInstance() (*InstanceGuard, bool, error) {
	name, err := windows.UTF16PtrFromString(singleMutexName)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, false, fmt.Errorf("多重起動防止Mutexを作成できません: %w", err)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(handle)
		return nil, true, nil
	}
	return &InstanceGuard{handle: handle}, false, nil
}

func (g *InstanceGuard) Close() error {
	if g == nil || g.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(g.handle)
	g.handle = 0
	return err
}

func RaiseExistingWindow() bool {
	title, err := windows.UTF16PtrFromString(MainWindowTitle)
	if err != nil {
		return false
	}
	handle := win.FindWindow(nil, title)
	if handle == 0 {
		return false
	}
	win.ShowWindow(handle, win.SW_RESTORE)
	return win.SetForegroundWindow(handle)
}
