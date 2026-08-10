//go:build linux

package privileged

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func storageDeviceID(path string) (uint64, error) {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Dev), nil
}

func storageAvailableKiB(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize) / 1024, nil
}

func validateRootOwnedDirectory(path string, maxMode os.FileMode) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFDIR || os.FileMode(stat.Mode)&0077 > maxMode&0077 {
		return errors.New("directory is not root-only")
	}
	return nil
}

func validateRootOwnedRegularFile(path string, exactMode os.FileMode) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	if stat.Uid != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || os.FileMode(stat.Mode).Perm() != exactMode.Perm() {
		return errors.New("file is not root-owned with the required mode")
	}
	return nil
}
