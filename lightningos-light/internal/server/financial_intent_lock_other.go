//go:build !linux && !windows

package server

import (
	"errors"
	"os"
)

func lockFinancialIntentFile(*os.File) error {
	return errors.New("durable financial execution is unsupported on this platform")
}
func syncFinancialIntentDir(string) error {
	return errors.New("durable financial execution is unsupported on this platform")
}
