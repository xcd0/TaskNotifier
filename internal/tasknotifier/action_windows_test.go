//go:build windows

package tasknotifier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartBatchAndWait(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "exit7.cmd")
	if err := os.WriteFile(path, []byte("@echo off\r\nexit /b 7\r\n"), 0o600); err != nil {
		t.Fatalf("テスト用CMDを作成できません: %v", err)
	}

	process, err := StartBatch(directory, TaskAction{BatPath: path})
	if err != nil {
		t.Fatalf("CMDを開始できません: %v", err)
	}
	result, err := process.Wait()
	if err == nil {
		t.Fatal("終了コード7をエラーとして取得できませんでした")
	}
	if result.ExitCode != 7 {
		t.Fatalf("終了コードが不正です: got=%d want=7", result.ExitCode)
	}
}
