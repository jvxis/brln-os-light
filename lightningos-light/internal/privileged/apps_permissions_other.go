//go:build !linux

package privileged

import (
	"errors"
	"os"
)

func validateSecretFileMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("secret file is invalid")
	}
	return nil
}

func securePrivilegedPathOwner(string) error {
	return nil
}

func privilegedPathOwnedByRoot(os.FileInfo) bool {
	return true
}
