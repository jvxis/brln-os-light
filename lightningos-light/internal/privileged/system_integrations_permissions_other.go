//go:build !linux

package privileged

import (
	"errors"
	"os"
)

func validateSystemIntegrationFile(path string, exactMode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != exactMode.Perm() {
		return errors.New("system integration file does not have the required type and mode")
	}
	return nil
}
