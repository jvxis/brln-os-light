//go:build linux

package privileged

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func (manager *NativeAppStorageManager) ensure(ctx context.Context, uid, gid int, dryRun bool) (AppStorageState, error) {
	if err := ctx.Err(); err != nil {
		return AppStorageState{}, err
	}
	if manager.requireRootParent {
		if err := validateRuntimeParent(manager.parentPath); err != nil {
			return AppStorageState{}, errors.New("app storage parent is unsafe")
		}
	}
	parentFD, err := unix.Open(manager.parentPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return AppStorageState{}, errors.New("open app storage parent failed")
	}
	defer unix.Close(parentFD)

	changed, stateFD, err := ensureAppStorageDirectoryAt(parentFD, manager.stateName, uid, gid, dryRun)
	if err != nil {
		return AppStorageState{}, err
	}
	if stateFD < 0 {
		return AppStorageState{Status: "validated", Changed: true}, nil
	}
	defer unix.Close(stateFD)
	for _, name := range []string{appStorageAppsName, appStorageDataName} {
		if err := ctx.Err(); err != nil {
			return AppStorageState{}, err
		}
		directoryChanged, directoryFD, err := ensureAppStorageDirectoryAt(stateFD, name, uid, gid, dryRun)
		if err != nil {
			return AppStorageState{}, err
		}
		changed = changed || directoryChanged
		if directoryFD >= 0 {
			_ = unix.Close(directoryFD)
		}
	}
	if !dryRun {
		if err := unix.Fsync(stateFD); err != nil {
			return AppStorageState{}, errors.New("sync app storage state directory failed")
		}
		if err := unix.Fsync(parentFD); err != nil {
			return AppStorageState{}, errors.New("sync app storage parent failed")
		}
	}
	status := "ready"
	if dryRun {
		status = "validated"
	}
	return AppStorageState{Status: status, Changed: changed}, nil
}

func ensureAppStorageDirectoryAt(parentFD int, name string, uid, gid int, dryRun bool) (bool, int, error) {
	if name == "" || name == "." || name == ".." {
		return false, -1, errors.New("app storage directory name is invalid")
	}
	directoryFD, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	created := false
	if errors.Is(err, unix.ENOENT) {
		if dryRun {
			return true, -1, nil
		}
		if err := unix.Mkdirat(parentFD, name, 0o750); err != nil {
			return false, -1, errors.New("create app storage directory failed")
		}
		created = true
		directoryFD, err = unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	}
	if err != nil {
		return false, -1, errors.New("app storage directory is unsafe")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(directoryFD, &stat); err != nil {
		_ = unix.Close(directoryFD)
		return false, -1, errors.New("inspect app storage directory failed")
	}
	changed := created || stat.Uid != uint32(uid) || stat.Gid != uint32(gid) || os.FileMode(stat.Mode).Perm() != 0o750
	if dryRun || !changed {
		return changed, directoryFD, nil
	}
	if err := unix.Fchown(directoryFD, uid, gid); err != nil {
		_ = unix.Close(directoryFD)
		return false, -1, errors.New("set app storage directory ownership failed")
	}
	if err := unix.Fchmod(directoryFD, 0o750); err != nil {
		_ = unix.Close(directoryFD)
		return false, -1, errors.New("set app storage directory mode failed")
	}
	if err := unix.Fsync(directoryFD); err != nil {
		_ = unix.Close(directoryFD)
		return false, -1, errors.New("sync app storage directory failed")
	}
	return true, directoryFD, nil
}
