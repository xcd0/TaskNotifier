//go:build !windows

package tasknotifier

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
