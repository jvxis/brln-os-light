package privileged

import (
	"context"
	"errors"
)

const (
	lndUpgradeHelperPath   = "/usr/local/sbin/lightningos-upgrade-lnd"
	lndUpgradeUnit         = "lightningos-lnd-upgrade"
	lndVerifyUnit          = "lightningos-lnd-verify"
	lndUpgradeHelperSHA256 = "aa7eaf131e4894c0f15beacfab101102e9426ed951062746d5a75fe456a1afb1"
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
