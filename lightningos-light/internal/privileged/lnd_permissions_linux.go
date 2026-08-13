//go:build linux

package privileged

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

func (manager *NativeLNDPermissionsManager) repair(ctx context.Context, uid, gid int, dryRun bool) (LNDPermissionsState, error) {
	if err := ctx.Err(); err != nil {
		return LNDPermissionsState{}, err
	}
	if manager.requireRootParent {
		if err := validateRuntimeParent(manager.parentPath); err != nil {
			return LNDPermissionsState{}, errors.New("LND permissions parent is unsafe")
		}
	}
	parentFD, err := unix.Open(manager.parentPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return LNDPermissionsState{}, errors.New("open LND permissions parent failed")
	}
	defer unix.Close(parentFD)

	changed, lndFD, present, err := repairLNDDirectoryAt(parentFD, manager.lndName, uid, gid, 0o750, dryRun)
	if err != nil {
		return LNDPermissionsState{}, err
	}
	if !present {
		return LNDPermissionsState{Status: "absent"}, nil
	}
	defer unix.Close(lndFD)

	for _, file := range []struct {
		name string
		mode uint32
	}{{name: "lnd.conf", mode: 0o660}, {name: "tls.cert", mode: 0o640}} {
		fileChanged, _, err := repairLNDFileAt(lndFD, file.name, uid, gid, file.mode, dryRun)
		if err != nil {
			return LNDPermissionsState{}, err
		}
		changed = changed || fileChanged
	}

	currentFD := lndFD
	ownedFDs := make([]int, 0, 4)
	defer func() {
		for _, fd := range ownedFDs {
			_ = unix.Close(fd)
		}
	}()
	for _, name := range []string{"data", "chain", "bitcoin", "mainnet"} {
		directoryChanged, nextFD, directoryPresent, err := repairLNDDirectoryAt(currentFD, name, uid, gid, 0o750, dryRun)
		if err != nil {
			return LNDPermissionsState{}, err
		}
		changed = changed || directoryChanged
		if !directoryPresent {
			currentFD = -1
			break
		}
		ownedFDs = append(ownedFDs, nextFD)
		currentFD = nextFD
	}
	if currentFD >= 0 && len(ownedFDs) == 4 {
		macaroonChanged, err := repairLNDMacaroons(currentFD, uid, gid, dryRun)
		if err != nil {
			return LNDPermissionsState{}, err
		}
		changed = changed || macaroonChanged
	}
	if !dryRun && changed {
		if err := unix.Fsync(lndFD); err != nil {
			return LNDPermissionsState{}, errors.New("sync LND permissions root failed")
		}
		if err := unix.Fsync(parentFD); err != nil {
			return LNDPermissionsState{}, errors.New("sync LND permissions parent failed")
		}
	}
	status := "ready"
	if dryRun {
		status = "validated"
	}
	return LNDPermissionsState{Status: status, Changed: changed}, nil
}

func repairLNDDirectoryAt(parentFD int, name string, uid, gid int, mode uint32, dryRun bool) (bool, int, bool, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, -1, false, nil
	}
	if err != nil {
		return false, -1, false, errors.New("LND permissions directory is unsafe")
	}
	changed, err := repairLNDMetadata(fd, uid, gid, mode, unix.S_IFDIR, dryRun)
	if err != nil {
		_ = unix.Close(fd)
		return false, -1, false, err
	}
	return changed, fd, true, nil
}

func repairLNDFileAt(parentFD int, name string, uid, gid int, mode uint32, dryRun bool) (bool, bool, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, false, nil
	}
	if err != nil {
		return false, false, errors.New("LND permissions file is unsafe")
	}
	defer unix.Close(fd)
	changed, err := repairLNDMetadata(fd, uid, gid, mode, unix.S_IFREG, dryRun)
	return changed, true, err
}

func repairLNDMetadata(fd, uid, gid int, mode uint32, wantType uint32, dryRun bool) (bool, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return false, errors.New("inspect LND permissions target failed")
	}
	if stat.Mode&unix.S_IFMT != wantType {
		return false, errors.New("LND permissions target type is unsafe")
	}
	changed := stat.Uid != uint32(uid) || stat.Gid != uint32(gid) || uint32(os.FileMode(stat.Mode).Perm()) != mode
	if dryRun || !changed {
		return changed, nil
	}
	if err := unix.Fchown(fd, uid, gid); err != nil {
		return false, errors.New("set LND permissions ownership failed")
	}
	if err := unix.Fchmod(fd, mode); err != nil {
		return false, errors.New("set LND permissions mode failed")
	}
	if err := unix.Fsync(fd); err != nil {
		return false, errors.New("sync LND permissions target failed")
	}
	return true, nil
}

func repairLNDMacaroons(chainFD, uid, gid int, dryRun bool) (bool, error) {
	duplicate, err := unix.Dup(chainFD)
	if err != nil {
		return false, errors.New("duplicate LND chain directory failed")
	}
	directory := os.NewFile(uintptr(duplicate), "lnd-mainnet")
	if directory == nil {
		_ = unix.Close(duplicate)
		return false, errors.New("open LND chain directory failed")
	}
	entries, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return false, errors.New("enumerate LND macaroons failed")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".macaroon") && entry.Name() != ".macaroon" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	changed := false
	for _, name := range names {
		mode := uint32(0o640)
		if name == "admin.macaroon" {
			mode = 0o600
		}
		fileChanged, present, err := repairLNDFileAt(chainFD, name, uid, gid, mode, dryRun)
		if err != nil || !present {
			if err == nil {
				err = errors.New("LND macaroon disappeared during repair")
			}
			return false, err
		}
		changed = changed || fileChanged
	}
	return changed, nil
}
