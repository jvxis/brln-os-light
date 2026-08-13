//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *NativeLNDUpgradeManager) start(context.Context, LNDUpgradeStartParams, bool) (LNDUpgradeState, error) {
	return LNDUpgradeState{}, errors.New("LND upgrades are supported only on Linux")
}
