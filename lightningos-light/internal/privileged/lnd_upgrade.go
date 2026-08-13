package privileged

import (
	"context"
	"errors"
)

const (
	lndUpgradeHelperPath   = "/usr/local/sbin/lightningos-upgrade-lnd"
	lndUpgradeUnit         = "lightningos-lnd-upgrade"
	lndVerifyUnit          = "lightningos-lnd-verify"
	lndUpgradeHelperSHA256 = "cebaddb4383c031e9ade30ea0283b4fce8ca8c26179c68b1fce4854722d0c951"
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
