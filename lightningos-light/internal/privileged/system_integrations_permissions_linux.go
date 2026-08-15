//go:build linux

package privileged

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func validateSystemIntegrationFile(path string, exactMode os.FileMode) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Uid != 0 || stat.Gid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || os.FileMode(stat.Mode).Perm() != exactMode.Perm() {
		return errors.New("system integration file is not root-owned with the required mode")
	}
	return nil
}
