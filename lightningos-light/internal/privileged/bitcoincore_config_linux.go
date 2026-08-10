//go:build linux

package privileged

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (manager *BitcoinCoreConfigManager) Ensure(ctx context.Context, dataDir string, content string, dryRun bool) (BitcoinCoreConfigState, error) {
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return BitcoinCoreConfigState{}, err
	}
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	defer unix.Close(directoryFD)

	existing, exists, original, legacyOwner, err := readBitcoinCoreConfigForEnsureAt(directoryFD)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	if exists {
		if err := validateBitcoinCoreConfigContent(existing); err != nil {
			return BitcoinCoreConfigState{}, errors.New("existing bitcoin config is invalid")
		}
		if legacyOwner {
			if dryRun {
				return BitcoinCoreConfigState{Status: "validated"}, nil
			}
			if err := writeBitcoinCoreConfigAt(ctx, directoryFD, existing, &original); err != nil {
				return BitcoinCoreConfigState{}, err
			}
		}
		return BitcoinCoreConfigState{Status: "ready"}, nil
	}
	if dryRun {
		return BitcoinCoreConfigState{Status: "validated"}, nil
	}
	if err := writeBitcoinCoreConfigAt(ctx, directoryFD, content, nil); err != nil {
		return BitcoinCoreConfigState{}, err
	}
	return BitcoinCoreConfigState{Status: "ready"}, nil
}

func (manager *BitcoinCoreConfigManager) Read(_ context.Context, dataDir string) (BitcoinCoreConfigState, error) {
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	defer unix.Close(directoryFD)
	content, exists, err := readBitcoinCoreConfigAt(directoryFD)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	if !exists {
		return BitcoinCoreConfigState{}, errors.New("bitcoin config does not exist")
	}
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return BitcoinCoreConfigState{}, errors.New("bitcoin config is invalid")
	}
	return BitcoinCoreConfigState{Status: "ready", Content: content}, nil
}

func (manager *BitcoinCoreConfigManager) Write(ctx context.Context, dataDir string, content string, dryRun bool) (BitcoinCoreConfigState, error) {
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return BitcoinCoreConfigState{}, err
	}
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	defer unix.Close(directoryFD)

	var original unix.Stat_t
	exists, err := statBitcoinCoreConfigAt(directoryFD, &original)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	if !exists {
		return BitcoinCoreConfigState{}, errors.New("bitcoin config does not exist")
	}
	if dryRun {
		return BitcoinCoreConfigState{Status: "validated"}, nil
	}
	if err := writeBitcoinCoreConfigAt(ctx, directoryFD, content, &original); err != nil {
		return BitcoinCoreConfigState{}, err
	}
	return BitcoinCoreConfigState{Status: "ready"}, nil
}

func (manager *BitcoinCoreConfigManager) openEnrolledDataDir(dataDir string) (int, error) {
	if err := validateBitcoinCoreConfigDataDir(dataDir); err != nil {
		return -1, err
	}
	root := manager.storageRoot()
	storedDataDir, err := readStorageMetadata(filepath.Join(root, bitcoinCoreStorageDataDirFile))
	if err != nil || storedDataDir != dataDir {
		return -1, errors.New("bitcoin config storage enrollment is invalid")
	}
	storageID, err := readStorageMetadata(filepath.Join(root, bitcoinCoreStorageIDFile))
	if err != nil || !validStorageID(storageID) {
		return -1, errors.New("bitcoin config storage identity is invalid")
	}

	directoryFD, err := unix.Openat2(unix.AT_FDCWD, dataDir, &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC),
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return -1, errors.New("open bitcoin config directory failed")
	}
	var directory unix.Stat_t
	if err := unix.Fstat(directoryFD, &directory); err != nil ||
		directory.Mode&unix.S_IFMT != unix.S_IFDIR || directory.Uid != 101 || directory.Gid != 101 ||
		os.FileMode(directory.Mode).Perm()&0o027 != 0 || os.FileMode(directory.Mode).Perm()&0o700 != 0o700 {
		_ = unix.Close(directoryFD)
		return -1, errors.New("bitcoin config directory is unsafe")
	}
	markerFD, err := unix.Openat(directoryFD, bitcoinCoreStorageMarkerFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		_ = unix.Close(directoryFD)
		return -1, errors.New("bitcoin config storage marker is unavailable")
	}
	marker := os.NewFile(uintptr(markerFD), bitcoinCoreStorageMarkerFile)
	if marker == nil {
		_ = unix.Close(markerFD)
		_ = unix.Close(directoryFD)
		return -1, errors.New("open bitcoin config storage marker failed")
	}
	defer marker.Close()
	var markerStat unix.Stat_t
	if err := unix.Fstat(markerFD, &markerStat); err != nil || markerStat.Mode&unix.S_IFMT != unix.S_IFREG ||
		markerStat.Uid != 0 || markerStat.Gid != 101 || os.FileMode(markerStat.Mode).Perm() != 0o640 || markerStat.Size > 128 {
		_ = unix.Close(directoryFD)
		return -1, errors.New("bitcoin config storage marker is unsafe")
	}
	raw, err := io.ReadAll(io.LimitReader(marker, 129))
	if err != nil || string(raw) != storageID+"\n" {
		_ = unix.Close(directoryFD)
		return -1, errors.New("bitcoin config storage marker does not match")
	}
	return directoryFD, nil
}

func readBitcoinCoreConfigForEnsureAt(directoryFD int) (string, bool, unix.Stat_t, bool, error) {
	var stat unix.Stat_t
	configFD, err := unix.Openat(directoryFD, bitcoinCoreConfigFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return "", false, stat, false, nil
	}
	if err != nil {
		return "", false, stat, false, errors.New("open bitcoin config failed")
	}
	file := os.NewFile(uintptr(configFD), bitcoinCoreConfigFile)
	if file == nil {
		_ = unix.Close(configFD)
		return "", false, stat, false, errors.New("open bitcoin config failed")
	}
	defer file.Close()
	if err := unix.Fstat(configFD, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		(stat.Uid != 0 && stat.Uid != 101) || stat.Gid != 101 || os.FileMode(stat.Mode).Perm() != 0o640 ||
		stat.Size <= 0 || stat.Size > maxBitcoinCoreConfigBytes {
		return "", false, stat, false, errors.New("bitcoin config file is unsafe")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBitcoinCoreConfigBytes+1))
	if err != nil || len(raw) > maxBitcoinCoreConfigBytes {
		return "", false, stat, false, errors.New("read bitcoin config failed")
	}
	return string(raw), true, stat, stat.Uid == 101, nil
}

func readBitcoinCoreConfigAt(directoryFD int) (string, bool, error) {
	configFD, err := unix.Openat(directoryFD, bitcoinCoreConfigFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("open bitcoin config failed")
	}
	file := os.NewFile(uintptr(configFD), bitcoinCoreConfigFile)
	if file == nil {
		_ = unix.Close(configFD)
		return "", false, errors.New("open bitcoin config failed")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := validateBitcoinCoreConfigStat(configFD, &stat); err != nil {
		return "", false, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBitcoinCoreConfigBytes+1))
	if err != nil || len(raw) > maxBitcoinCoreConfigBytes {
		return "", false, errors.New("read bitcoin config failed")
	}
	return string(raw), true, nil
}

func statBitcoinCoreConfigAt(directoryFD int, stat *unix.Stat_t) (bool, error) {
	return statBitcoinCoreConfigWithOwnerAt(directoryFD, stat, 0)
}

func statBitcoinCoreConfigWithOwnerAt(directoryFD int, stat *unix.Stat_t, expectedUID uint32) (bool, error) {
	configFD, err := unix.Openat(directoryFD, bitcoinCoreConfigFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("open bitcoin config failed")
	}
	defer unix.Close(configFD)
	if err := validateBitcoinCoreConfigStatWithOwner(configFD, stat, expectedUID); err != nil {
		return false, err
	}
	return true, nil
}

func validateBitcoinCoreConfigStat(fd int, stat *unix.Stat_t) error {
	return validateBitcoinCoreConfigStatWithOwner(fd, stat, 0)
}

func validateBitcoinCoreConfigStatWithOwner(fd int, stat *unix.Stat_t, expectedUID uint32) error {
	if err := unix.Fstat(fd, stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Uid != expectedUID || stat.Gid != 101 || os.FileMode(stat.Mode).Perm() != 0o640 ||
		stat.Size <= 0 || stat.Size > maxBitcoinCoreConfigBytes {
		return errors.New("bitcoin config file is unsafe")
	}
	return nil
}

func writeBitcoinCoreConfigAt(ctx context.Context, directoryFD int, content string, original *unix.Stat_t) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temporaryName, err := newBitcoinCoreConfigTemporaryName()
	if err != nil {
		return errors.New("bitcoin config temporary name generation failed")
	}
	temporaryFD, err := unix.Openat(directoryFD, temporaryName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("create bitcoin config temporary file failed")
	}
	committed := false
	defer func() {
		if !committed {
			_ = unix.Unlinkat(directoryFD, temporaryName, 0)
		}
	}()
	temporary := os.NewFile(uintptr(temporaryFD), temporaryName)
	if temporary == nil {
		_ = unix.Close(temporaryFD)
		return errors.New("create bitcoin config temporary file failed")
	}
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
	}()
	if err := temporary.Chown(0, 101); err != nil || temporary.Chmod(0o640) != nil {
		return errors.New("secure bitcoin config temporary file failed")
	}
	if written, err := temporary.WriteString(content); err != nil || written != len(content) {
		return errors.New("write bitcoin config temporary file failed")
	}
	if err := temporary.Sync(); err != nil || temporary.Close() != nil {
		return errors.New("sync bitcoin config temporary file failed")
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return err
	}

	var current unix.Stat_t
	expectedUID := uint32(0)
	if original != nil {
		expectedUID = original.Uid
	}
	exists, err := statBitcoinCoreConfigWithOwnerAt(directoryFD, &current, expectedUID)
	if err != nil {
		return err
	}
	if original == nil {
		if exists {
			return errors.New("bitcoin config appeared during creation")
		}
	} else if !exists || !sameBitcoinCoreConfigStat(*original, current) {
		return errors.New("bitcoin config changed during update")
	}
	if err := unix.Renameat(directoryFD, temporaryName, directoryFD, bitcoinCoreConfigFile); err != nil {
		return errors.New("commit bitcoin config failed")
	}
	committed = true
	if err := unix.Fsync(directoryFD); err != nil {
		return errors.New("sync bitcoin config directory failed")
	}
	return nil
}

func sameBitcoinCoreConfigStat(left unix.Stat_t, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode &&
		left.Uid == right.Uid && left.Gid == right.Gid && left.Size == right.Size &&
		left.Mtim == right.Mtim && left.Ctim == right.Ctim
}
