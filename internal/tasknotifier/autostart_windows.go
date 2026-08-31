//go:build windows

package tasknotifier

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	autostartKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	autostartValueName = "TaskNotifier"
)

type AutostartStatus struct {
	Enabled  bool
	Mismatch bool
	Command  string
}

func ExpectedAutostartCommand(executable string) string {
	return `"` + executable + `" --background`
}

func ReadAutostart(executable string) (AutostartStatus, error) {
	return readAutostartValue(executable, autostartValueName)
}

func readAutostartValue(executable, valueName string) (AutostartStatus, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, autostartKeyPath, registry.QUERY_VALUE)
	if err == registry.ErrNotExist {
		return AutostartStatus{}, nil
	}
	if err != nil {
		return AutostartStatus{}, fmt.Errorf("自動起動設定を読み取れません: %w", err)
	}
	defer key.Close()
	command, _, err := key.GetStringValue(valueName)
	if err == registry.ErrNotExist {
		return AutostartStatus{}, nil
	}
	if err != nil {
		return AutostartStatus{}, fmt.Errorf("自動起動設定を読み取れません: %w", err)
	}
	expected := ExpectedAutostartCommand(executable)
	return AutostartStatus{Enabled: true, Mismatch: !strings.EqualFold(command, expected), Command: command}, nil
}

func SetAutostart(executable string, enabled bool) error {
	return setAutostartValue(executable, autostartValueName, enabled)
}

func setAutostartValue(executable, valueName string, enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autostartKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("自動起動設定を開けません: %w", err)
	}
	defer key.Close()
	if enabled {
		if err := key.SetStringValue(valueName, ExpectedAutostartCommand(executable)); err != nil {
			return fmt.Errorf("自動起動を登録できません: %w", err)
		}
		return nil
	}
	if err := key.DeleteValue(valueName); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("自動起動を解除できません: %w", err)
	}
	return nil
}
