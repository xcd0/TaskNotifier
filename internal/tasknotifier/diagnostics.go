package tasknotifier

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxDiagnosticLogSize = 5 * 1024 * 1024

var BuildVersion = "development"

func StartDiagnosticLogging(preferredPath string) (*os.File, string, error) {
	fallbackPath := filepath.Join(os.TempDir(), "TaskNotifier", "TaskNotifier.log")
	candidates := []string{filepath.Clean(preferredPath)}
	if !sameCleanPath(preferredPath, fallbackPath) {
		candidates = append(candidates, fallbackPath)
	}

	var failures []string
	for _, candidate := range candidates {
		if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		if err := rotateDiagnosticLog(candidate); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		file, err := os.OpenFile(candidate, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		log.SetOutput(file)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
		log.Printf("========== TaskNotifier session begin ==========")
		log.Printf("runtime version=%q go=%q os=%q arch=%q pid=%d", BuildVersion, runtime.Version(), runtime.GOOS, runtime.GOARCH, os.Getpid())
		log.Printf("diagnostic log path=%q", candidate)
		for _, failure := range failures {
			log.Printf("diagnostic preferred path unavailable: %s", failure)
		}
		return file, candidate, nil
	}
	return nil, "", fmt.Errorf("診断ログを作成できません: %s", strings.Join(failures, " / "))
}

func rotateDiagnosticLog(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ログファイルを確認できません: %w", err)
	}
	if info.Size() <= maxDiagnosticLogSize {
		return nil
	}

	oldPath := path + ".old"
	if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("旧ログを削除できません: %w", err)
	}
	if err := os.Rename(path, oldPath); err != nil {
		return fmt.Errorf("ログをローテーションできません: %w", err)
	}
	return nil
}

func sameCleanPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
