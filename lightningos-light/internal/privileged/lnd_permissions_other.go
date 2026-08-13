//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *NativeLNDPermissionsManager) repair(context.Context, int, int, bool) (LNDPermissionsState, error) {
	return LNDPermissionsState{}, errors.New("LND permissions repair is supported only on Linux")
}
