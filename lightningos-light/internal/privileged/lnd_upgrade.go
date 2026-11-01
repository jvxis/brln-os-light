package privileged

import (
	"context"
	"errors"
)

const (
	lndUpgradeHelperPath   = "/usr/local/sbin/lightningos-upgrade-lnd"
	lndUpgradeUnit         = "lightningos-lnd-upgrade"
	lndVerifyUnit          = "lightningos-lnd-verify"
	lndUpgradeHelperSHA256 = "bf187e1a029667fe93535ec5059e851a06e725d090f606135d05943e7f1765ba"
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
