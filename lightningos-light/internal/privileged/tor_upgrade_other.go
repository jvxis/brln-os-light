//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *NativeTorUpgradeManager) start(context.Context, TorUpgradeStartParams, bool) (TorUpgradeState, error) {
	return TorUpgradeState{}, errors.New("Tor upgrades are supported only on Linux")
}
