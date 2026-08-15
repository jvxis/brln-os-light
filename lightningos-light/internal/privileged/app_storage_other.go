//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *NativeAppStorageManager) ensure(context.Context, int, int, bool) (AppStorageState, error) {
	return AppStorageState{}, errors.New("app storage repair is supported only on Linux")
}
