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

// BatchProcess は開始済みのBAT/CMDプロセスを保持する。
type BatchProcess struct {
	command *exec.Cmd
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

// StartBatch はBAT/CMDを開始し、終了を待たずに開始済みプロセスを返す。
func StartBatch(executableDirectory string, action TaskAction) (*BatchProcess, error) {
	launch, err := BuildBatchLaunch(executableDirectory, action)
	if err != nil {
		return nil, err
	}
	command := exec.Command(launch.ComSpec, "/D", "/S", "/C", launch.CommandText)
	command.Dir = launch.Directory
	if launch.HideWindow {
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("BATを起動できません: %w", err)
	}
	return &BatchProcess{command: command}, nil
}

// Wait は開始済みBAT/CMDの終了を待ち、終了コードを返す。
func (process *BatchProcess) Wait() (BatchResult, error) {
	if process == nil || process.command == nil {
		return BatchResult{}, errors.New("BATプロセスが開始されていません")
	}
	if err := process.command.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return BatchResult{ExitCode: exitErr.ExitCode()}, fmt.Errorf("BATが終了コード%dで失敗しました: %w", exitErr.ExitCode(), err)
		}
		return BatchResult{}, fmt.Errorf("BATの終了を確認できません: %w", err)
	}
	return BatchResult{ExitCode: process.command.ProcessState.ExitCode()}, nil
}

// RunBatch は互換用にBAT/CMDを実行し、終了コードが0以外ならエラーを返す。
func RunBatch(executableDirectory string, action TaskAction) error {
	_, err := RunBatchWithResult(executableDirectory, action)
	return err
}

// RunBatchWithResult は互換用にBAT/CMDの終了まで待機して終了コードを返す。
func RunBatchWithResult(executableDirectory string, action TaskAction) (BatchResult, error) {
	process, err := StartBatch(executableDirectory, action)
	if err != nil {
		return BatchResult{}, err
	}
	return process.Wait()
}
