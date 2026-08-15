//go:build linux

package privileged

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"lightningos-light/internal/appmanifest"
)

func readLegacyBitcoinCoreStorageID(path string, dataDir string) (string, bool, error) {
	raw, err := readRegularFile(path, 128)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("legacy bitcoin storage identity is unsafe")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return "", false, errors.New("legacy bitcoin storage identity is unsafe")
	}
	storageID := strings.TrimSpace(string(raw))
	if !validStorageID(storageID) {
		return "", false, errors.New("legacy bitcoin storage identity is invalid")
	}
	markerPath := filepath.Join(dataDir, appmanifest.BitcoinCoreStorageMarker)
	var markerStat unix.Stat_t
	if err := unix.Lstat(markerPath, &markerStat); err != nil || markerStat.Mode&unix.S_IFMT != unix.S_IFREG ||
		markerStat.Uid != 0 || markerStat.Gid != 101 || os.FileMode(markerStat.Mode).Perm() != 0o640 {
		return "", false, errors.New("legacy bitcoin storage marker is unsafe")
	}
	markerRaw, err := readRegularFile(markerPath, 128)
	if err != nil || strings.TrimSpace(string(markerRaw)) != storageID {
		return "", false, errors.New("legacy bitcoin storage identity does not match the data volume")
	}
	return storageID, true, nil
}

func syncLegacyBitcoinCoreStorageID(path string, storageID string) error {
	if !validStorageID(storageID) {
		return errors.New("bitcoin storage identity is invalid")
	}
	var before unix.Stat_t
	if err := unix.Lstat(path, &before); os.IsNotExist(err) {
		return nil
	} else if err != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || os.FileMode(before.Mode).Perm() != 0o600 ||
		before.Size < 1 || before.Size > 128 {
		return errors.New("legacy bitcoin storage identity is unsafe")
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("legacy bitcoin storage identity update failed")
	}
	file := os.NewFile(uintptr(fd), filepath.Base(path))
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("legacy bitcoin storage identity update failed")
	}
	defer file.Close()
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || opened.Mode&unix.S_IFMT != unix.S_IFREG ||
		opened.Dev != before.Dev || opened.Ino != before.Ino {
		return errors.New("legacy bitcoin storage identity changed during update")
	}
	raw := []byte(storageID + "\n")
	if err := file.Truncate(0); err != nil {
		return errors.New("legacy bitcoin storage identity update failed")
	}
	if _, err := file.Seek(0, 0); err != nil {
		return errors.New("legacy bitcoin storage identity update failed")
	}
	if written, err := file.Write(raw); err != nil || written != len(raw) {
		return errors.New("legacy bitcoin storage identity update failed")
	}
	if err := file.Sync(); err != nil {
		return errors.New("legacy bitcoin storage identity update failed")
	}
	var final unix.Stat_t
	if err := unix.Fstat(fd, &final); err != nil || final.Size != int64(len(raw)) || final.Dev != opened.Dev || final.Ino != opened.Ino {
		return errors.New("legacy bitcoin storage identity changed during update")
	}
	return nil
}

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
