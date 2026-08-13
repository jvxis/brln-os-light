//go:build linux

package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	DefaultAuditPath = "/var/log/lightningos-privileged/audit.jsonl"
	DefaultLockPath  = "/run/lock/lightningos/privileged.lock"
	ExpectedCaller   = "lightningos"
)

type FileAudit struct {
	file *os.File
}

func NewFileAudit(path string) (*FileAudit, error) {
	if err := validateRuntimeParent(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open privileged audit: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open privileged audit failed")
	}
	if err := validateRootFile(fd, 0o077); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &FileAudit{file: file}, nil
}

func (audit *FileAudit) Write(event AuditEvent) error {
	if audit == nil || audit.file == nil {
		return errors.New("audit unavailable")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	fd := int(audit.file.Fd())
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	if _, err := audit.file.Write(data); err != nil {
		return err
	}
	return audit.file.Sync()
}

func (audit *FileAudit) Close() error {
	if audit == nil || audit.file == nil {
		return nil
	}
	return audit.file.Close()
}

type FileLocker struct {
	path string
}

func NewFileLocker(path string) (*FileLocker, error) {
	if err := validateRuntimeParent(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("runtime lock must be a regular file")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return nil, errors.New("runtime lock must be root-owned")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("runtime lock permissions are too broad")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat runtime lock: %w", err)
	}
	return &FileLocker{path: path}, nil
}

func (locker *FileLocker) Lock(ctx context.Context) (func(), error) {
	if locker == nil || locker.path == "" {
		return nil, errors.New("lock unavailable")
	}
	fd, err := unix.Open(locker.path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := validateRootFile(fd, 0o077); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	for {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return func() {
				_ = unix.Flock(fd, unix.LOCK_UN)
				_ = unix.Close(fd)
			}, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = unix.Close(fd)
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = unix.Close(fd)
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func AuthorizeCaller() (string, error) {
	if credentials, err := unix.GetsockoptUcred(int(os.Stdin.Fd()), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
		return authorizeSocketCaller(os.Geteuid(), credentials.Uid, func() (string, error) {
			expected, lookupErr := user.Lookup(ExpectedCaller)
			if lookupErr != nil {
				return "", lookupErr
			}
			return expected.Uid, nil
		})
	}
	return authorizeCaller(os.Geteuid(), os.Getenv("SUDO_UID"), os.Getenv("SUDO_USER"), func() (string, error) {
		expected, err := user.Lookup(ExpectedCaller)
		if err != nil {
			return "", err
		}
		return expected.Uid, nil
	})
}

func authorizeSocketCaller(effectiveUID int, peerUID uint32, lookupExpectedUID func() (string, error)) (string, error) {
	if effectiveUID != 0 {
		return "", errors.New("privileged broker must run as root")
	}
	expectedUIDValue, err := lookupExpectedUID()
	if err != nil {
		return "", errors.New("expected broker caller is unavailable")
	}
	expectedUID, err := strconv.ParseUint(expectedUIDValue, 10, 32)
	if err != nil || uint64(peerUID) != expectedUID {
		return "", errors.New("unauthorized privileged broker caller")
	}
	return ExpectedCaller, nil
}

func authorizeCaller(effectiveUID int, sudoUID string, sudoUser string, lookupExpectedUID func() (string, error)) (string, error) {
	if effectiveUID != 0 {
		return "", errors.New("privileged broker must run as root")
	}
	if sudoUID == "" && sudoUser == "" {
		return "root-direct", nil
	}
	if sudoUID == "" || sudoUser != ExpectedCaller {
		return "", errors.New("unauthorized privileged broker caller")
	}
	expectedUIDValue, err := lookupExpectedUID()
	if err != nil {
		return "", errors.New("expected broker caller is unavailable")
	}
	actualUID, err := strconv.ParseUint(sudoUID, 10, 32)
	if err != nil {
		return "", errors.New("invalid privileged broker caller uid")
	}
	expectedUID, err := strconv.ParseUint(expectedUIDValue, 10, 32)
	if err != nil || actualUID != expectedUID {
		return "", errors.New("unauthorized privileged broker caller")
	}
	return ExpectedCaller, nil
}

func validateRuntimeParent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat runtime parent: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("runtime parent must be a directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return errors.New("runtime parent must be root-owned")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("runtime parent must not be group/world writable")
	}
	return nil
}

func validateRootFile(fd int, forbiddenMode os.FileMode) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("runtime file must be regular")
	}
	if stat.Uid != 0 {
		return errors.New("runtime file must be root-owned")
	}
	if os.FileMode(stat.Mode).Perm()&forbiddenMode != 0 {
		return errors.New("runtime file permissions are too broad")
	}
	return nil
}
