package privileged

import (
	"context"
	"errors"
)

const (
	lndUpgradeHelperPath   = "/usr/local/sbin/lightningos-upgrade-lnd"
	lndUpgradeUnit         = "lightningos-lnd-upgrade"
	lndVerifyUnit          = "lightningos-lnd-verify"
	lndUpgradeHelperSHA256 = "7bb3df07386fbad1701d3843a6907de189b20bd960fb80ecb4ed5980d3ea28fa"
)

type NativeLNDUpgradeManager struct {
	runner CommandRunner
}

func NewNativeLNDUpgradeManager(runner CommandRunner) *NativeLNDUpgradeManager {
	return &NativeLNDUpgradeManager{runner: runner}
}

func (manager *NativeLNDUpgradeManager) Start(ctx context.Context, params LNDUpgradeStartParams, dryRun bool) (LNDUpgradeState, error) {
	if manager == nil || manager.runner == nil {
		return LNDUpgradeState{}, errors.New("LND upgrade manager is unavailable")
	}
	return manager.start(ctx, params, dryRun)
}
