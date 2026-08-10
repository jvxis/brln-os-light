//go:build linux

package privileged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (files *AtomicConfigFiles) EnableLogin(ctx context.Context, dryRun bool) (bool, error) {
	if files == nil || files.path != DefaultManagerConfigPath {
		return false, errors.New("manager config target is not allowed")
	}
	parent := filepath.Dir(files.path)
	if err := validateRuntimeParent(parent); err != nil {
		return false, fmt.Errorf("validate manager config parent: %w", err)
	}

	fd, err := unix.Open(files.path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, errors.New("open manager config failed")
	}
	file := os.NewFile(uintptr(fd), files.path)
	if file == nil {
		_ = unix.Close(fd)
		return false, errors.New("open manager config failed")
	}
	defer file.Close()

	var original unix.Stat_t
	if err := unix.Fstat(fd, &original); err != nil {
		return false, errors.New("stat manager config failed")
	}
	if original.Mode&unix.S_IFMT != unix.S_IFREG || original.Uid != 0 {
		return false, errors.New("manager config must be a root-owned regular file")
	}
	if os.FileMode(original.Mode).Perm()&0o022 != 0 {
		return false, errors.New("manager config permissions are too broad")
	}
	if original.Size < 0 || original.Size > maxManagerConfigBytes {
		return false, errors.New("manager config is too large")
	}

	data, err := io.ReadAll(io.LimitReader(file, maxManagerConfigBytes+1))
	if err != nil {
		return false, errors.New("read manager config failed")
	}
	updated, changed, err := prepareEnableLoginConfig(data)
	if err != nil {
		return false, err
	}
	if !changed || dryRun {
		return changed, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	temporary, err := os.CreateTemp(parent, ".config.yaml.lightningos-")
	if err != nil {
		return false, errors.New("create manager config temporary file failed")
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	mode := os.FileMode(original.Mode).Perm()
	if err := temporary.Chmod(mode); err != nil {
		return false, errors.New("set manager config mode failed")
	}
	if err := temporary.Chown(int(original.Uid), int(original.Gid)); err != nil {
		return false, errors.New("set manager config ownership failed")
	}
	if written, err := temporary.Write(updated); err != nil || written != len(updated) {
		return false, errors.New("write manager config failed")
	}
	if err := temporary.Sync(); err != nil {
		return false, errors.New("sync manager config failed")
	}
	if err := temporary.Close(); err != nil {
		return false, errors.New("close manager config failed")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	var current unix.Stat_t
	if err := unix.Lstat(files.path, &current); err != nil {
		return false, errors.New("revalidate manager config failed")
	}
	if current.Mode&unix.S_IFMT != unix.S_IFREG ||
		current.Dev != original.Dev || current.Ino != original.Ino ||
		current.Size != original.Size || current.Mtim != original.Mtim || current.Ctim != original.Ctim ||
		current.Uid != original.Uid || current.Gid != original.Gid || current.Mode != original.Mode {
		return false, errors.New("manager config changed during update")
	}
	if err := os.Rename(temporaryPath, files.path); err != nil {
		return false, errors.New("commit manager config failed")
	}
	committed = true

	directoryFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, errors.New("open manager config directory failed")
	}
	defer unix.Close(directoryFD)
	if err := unix.Fsync(directoryFD); err != nil {
		return false, errors.New("sync manager config directory failed")
	}
	return true, nil
}
