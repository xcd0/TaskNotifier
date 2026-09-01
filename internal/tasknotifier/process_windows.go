//go:build windows

package tasknotifier

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

// currentProcessStartTime は現在のプロセス開始時刻を取得する。
func currentProcessStartTime() (time.Time, error) {
	return processStartTime(uint32(windows.GetCurrentProcessId()))
}

// processStartTime は指定PIDのプロセス開始時刻を取得する。
func processStartTime(pid uint32) (time.Time, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}, err
	}
	defer windows.CloseHandle(handle)

	var creation windows.Filetime
	var exit windows.Filetime
	var kernel windows.Filetime
	var user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, creation.Nanoseconds()), nil
}

// sameRunningProcess はPIDと開始時刻の両方が一致する実行中プロセスか確認する。
func sameRunningProcess(pid int, startedAt string) bool {
	if pid <= 0 || startedAt == "" {
		return false
	}
	expected, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return false
	}
	actual, err := processStartTime(uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	return actual.Sub(expected) < time.Second && expected.Sub(actual) < time.Second
}
