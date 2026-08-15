//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (files *AtomicConfigFiles) EnableLogin(ctx context.Context, dryRun bool) (bool, error) {
	return false, errors.New("privileged config files are supported only on Linux")
}

func (files *AtomicConfigFiles) SetLNDMacaroonPath(context.Context, string, bool) (bool, error) {
	return false, errors.New("privileged config files are supported only on Linux")
}

func (files *AtomicConfigFiles) LNDMacaroonPath(context.Context) (string, error) {
	return "", errors.New("privileged config files are supported only on Linux")
}
