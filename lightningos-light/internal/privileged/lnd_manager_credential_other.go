//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *NativeLNDManagerCredentialManager) ensure(context.Context, int, int, int, int, bool) (LNDManagerCredentialState, error) {
	return LNDManagerCredentialState{}, errors.New("LND manager credential migration is supported only on Linux")
}

func (manager *NativeLNDManagerCredentialManager) rollback(context.Context, int, int, int, int, bool) (LNDManagerCredentialState, error) {
	return LNDManagerCredentialState{}, errors.New("LND manager credential rollback is supported only on Linux")
}
