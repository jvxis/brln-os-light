//go:build linux

package privileged

import (
	"errors"
	"os"
	"syscall"
)

func validateSecretFileMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("secret file permissions are too broad")
	}
	return nil
}

func securePrivilegedPathOwner(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return os.Chown(path, 0, 0)
}

func privilegedPathOwnedByRoot(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && stat.Gid == 0
}
