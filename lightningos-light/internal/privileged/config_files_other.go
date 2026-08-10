//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (files *AtomicConfigFiles) EnableLogin(ctx context.Context, dryRun bool) (bool, error) {
	return false, errors.New("privileged config files are supported only on Linux")
}
