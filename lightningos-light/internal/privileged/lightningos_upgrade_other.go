//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *NativeLightningOSUpgradeManager) start(context.Context, LightningOSUpgradeStartParams, bool) (LightningOSUpgradeState, error) {
	return LightningOSUpgradeState{}, errors.New("LightningOS upgrades are supported only on Linux")
}
