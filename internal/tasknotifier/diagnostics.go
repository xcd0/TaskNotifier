package tasknotifier

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var BuildVersion = "development"

func StartDiagnosticLogging(preferredPath string) (*os.File, string, error) {
	fallbackPath := filepath.Join(os.TempDir(), "TaskNotifier", "TaskNotifier.log")
	candidates := []string{filepath.Clean(preferredPath)}
	if !sameCleanPath(preferredPath, fallbackPath) {
		candidates = append(candidates, fallbackPath)
	}

	var failures []string
	for _, candidate := range candidates {
		if sameCleanPath(candidate, fallbackPath) {
			if err := os.MkdirAll(filepath.Dir(candidate), 0o700); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", candidate, err))
				continue
			}
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

func sameCleanPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
