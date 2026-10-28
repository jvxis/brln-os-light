//go:build windows

package server

import (
	"golang.org/x/sys/windows"
	"os"
)

func lockFinancialIntentFile(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}

// The application is installed on Linux; Windows runs repository regressions.
// Directory fsync is checked by the mandatory native Linux validation.
func syncFinancialIntentDir(string) error { return nil }
