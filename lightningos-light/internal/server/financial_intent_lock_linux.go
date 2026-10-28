//go:build linux

package server

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockFinancialIntentFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
func syncFinancialIntentDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
