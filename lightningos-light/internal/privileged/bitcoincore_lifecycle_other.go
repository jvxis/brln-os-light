//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *ComposeAppManager) createBitcoinCoreExecutionSnapshot(context.Context, bool, bool) (composeAppSnapshot, func(), error) {
	return composeAppSnapshot{}, func() {}, errors.New("bitcoin lifecycle is only supported on Linux")
}

func (manager *ComposeAppManager) createBitcoinCoreInspectionSnapshot(context.Context) (composeAppSnapshot, func(), error) {
	return composeAppSnapshot{}, func() {}, errors.New("bitcoin lifecycle is only supported on Linux")
}

func (manager *ComposeAppManager) validateBitcoinCoreLifecycleAttestation() error {
	return errors.New("bitcoin lifecycle is only supported on Linux")
}

func (manager *ComposeAppManager) removeBitcoinCoreExecutionSnapshot(string) error {
	return errors.New("bitcoin lifecycle is only supported on Linux")
}
