//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *BitcoinCoreConfigManager) Ensure(context.Context, string, string, bool) (BitcoinCoreConfigState, error) {
	return BitcoinCoreConfigState{}, errors.New("bitcoin config is supported only on Linux")
}

func (manager *BitcoinCoreConfigManager) Read(context.Context, string) (BitcoinCoreConfigState, error) {
	return BitcoinCoreConfigState{}, errors.New("bitcoin config is supported only on Linux")
}

func (manager *BitcoinCoreConfigManager) Write(context.Context, string, string, bool) (BitcoinCoreConfigState, error) {
	return BitcoinCoreConfigState{}, errors.New("bitcoin config is supported only on Linux")
}
