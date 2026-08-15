//go:build !linux

package privileged

import (
	"context"
	"errors"
)

const (
	DefaultAuditPath = "/var/log/lightningos-privileged/audit.jsonl"
	DefaultLockPath  = "/run/lock/lightningos/privileged.lock"
	ExpectedCaller   = "lightningos"
)

type FileAudit struct{}

func NewFileAudit(path string) (*FileAudit, error) {
	return nil, errors.New("privileged broker is supported only on Linux")
}

func (audit *FileAudit) Write(event AuditEvent) error {
	return errors.New("privileged broker is supported only on Linux")
}

func (audit *FileAudit) Close() error { return nil }

type FileLocker struct{}

func NewFileLocker(path string) (*FileLocker, error) {
	return nil, errors.New("privileged broker is supported only on Linux")
}

func (locker *FileLocker) Lock(ctx context.Context) (func(), error) {
	return nil, errors.New("privileged broker is supported only on Linux")
}

func AuthorizeCaller() (string, error) {
	return "", errors.New("privileged broker is supported only on Linux")
}
