//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *BitcoinCoreConfigManager) Ensure(context.Context, string, string, bool, bool) (BitcoinCoreConfigState, error) {
	return BitcoinCoreConfigState{}, errors.New("bitcoin config is supported only on Linux")
}

func (manager *BitcoinCoreConfigManager) Credentials(context.Context, string) (BitcoinCoreCredentialsState, error) {
	return BitcoinCoreCredentialsState{}, errors.New("bitcoin credentials are supported only on Linux")
}

func (manager *BitcoinCoreConfigManager) EnsureCredentials(context.Context, string, bool) (BitcoinCoreCredentialsEnsureState, error) {
	return BitcoinCoreCredentialsEnsureState{}, errors.New("bitcoin credentials are supported only on Linux")
}

func (manager *BitcoinCoreConfigManager) EnsureElectrsCredentials(context.Context, string, bool) (BitcoinCoreElectrsCredentialsState, error) {
	return BitcoinCoreElectrsCredentialsState{}, errors.New("Electrs bitcoin credentials are supported only on Linux")
}

func (manager *BitcoinCoreConfigManager) Read(context.Context, string) (BitcoinCoreConfigState, error) {
	return BitcoinCoreConfigState{}, errors.New("bitcoin config is supported only on Linux")
}

func (manager *BitcoinCoreConfigManager) Write(context.Context, string, string, bool) (BitcoinCoreConfigState, error) {
	return BitcoinCoreConfigState{}, errors.New("bitcoin config is supported only on Linux")
}
