//go:build windows

package tasknotifier

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type BatchLaunch struct {
	ComSpec     string
	CommandText string
	Directory   string
	HideWindow  bool
}

func BuildBatchLaunch(executableDirectory string, action TaskAction) (BatchLaunch, error) {
	path := ResolveBATPath(executableDirectory, action.BatPath)
	if path == "" {
		return BatchLaunch{}, errors.New("BATファイルが登録されていません")
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".bat" && extension != ".cmd" {
		return BatchLaunch{}, errors.New("実行できる拡張子は.batと.cmdだけです")
	}
	info, err := os.Stat(path)
	if err != nil {
		return BatchLaunch{}, fmt.Errorf("BATファイルを確認できません: %w", err)
	}
	if info.IsDir() {
		return BatchLaunch{}, errors.New("BATパスがディレクトリを指しています")
	}
	comSpec := strings.TrimSpace(os.Getenv("ComSpec"))
	if comSpec == "" {
		comSpec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	if _, err := os.Stat(comSpec); err != nil {
		return BatchLaunch{}, fmt.Errorf("cmd.exeを確認できません: %w", err)
	}
	commandText := `""` + path + `""`
	return BatchLaunch{ComSpec: comSpec, CommandText: commandText, Directory: executableDirectory, HideWindow: !action.ShowConsole}, nil
}

func RunBatch(executableDirectory string, action TaskAction) error {
	launch, err := BuildBatchLaunch(executableDirectory, action)
	if err != nil {
		return err
	}
	command := exec.Command(launch.ComSpec, "/D", "/S", "/C", launch.CommandText)
	command.Dir = launch.Directory
	if launch.HideWindow {
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("BATを起動できません: %w", err)
	}
	_ = command.Process.Release()
	return nil
}
