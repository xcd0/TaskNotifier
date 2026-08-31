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

type BatchResult struct {
	ExitCode int
}

// NormalizeBATPath はBAT/CMDパスを絶対パスへ正規化する。
// 旧設定の相対パスは互換性のためEXEディレクトリ基準で解決する。
func NormalizeBATPath(executableDirectory, configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return "", nil
	}
	path := configured
	if !filepath.IsAbs(path) {
		path = filepath.Join(executableDirectory, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("BATファイルの絶対パスを取得できません: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func BuildBatchLaunch(executableDirectory string, action TaskAction) (BatchLaunch, error) {
	path, err := NormalizeBATPath(executableDirectory, action.BatPath)
	if err != nil {
		return BatchLaunch{}, err
	}
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
	return BatchLaunch{ComSpec: comSpec, CommandText: commandText, Directory: filepath.Dir(path), HideWindow: !action.ShowConsole}, nil
}

// RunBatch は互換用にBAT/CMDを実行し、終了コードが0以外ならエラーを返す。
func RunBatch(executableDirectory string, action TaskAction) error {
	_, err := RunBatchWithResult(executableDirectory, action)
	return err
}

// RunBatchWithResult はBAT/CMDの終了まで待機し、終了コードを返す。
func RunBatchWithResult(executableDirectory string, action TaskAction) (BatchResult, error) {
	launch, err := BuildBatchLaunch(executableDirectory, action)
	if err != nil {
		return BatchResult{}, err
	}
	command := exec.Command(launch.ComSpec, "/D", "/S", "/C", launch.CommandText)
	command.Dir = launch.Directory
	if launch.HideWindow {
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	}
	if err := command.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return BatchResult{ExitCode: exitErr.ExitCode()}, fmt.Errorf("BATが終了コード%dで失敗しました: %w", exitErr.ExitCode(), err)
		}
		return BatchResult{}, fmt.Errorf("BATを実行できません: %w", err)
	}
	return BatchResult{ExitCode: command.ProcessState.ExitCode()}, nil
}
