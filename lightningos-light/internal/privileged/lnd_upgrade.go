package privileged

import (
	"context"
	"errors"
)

const (
	lndUpgradeHelperPath   = "/usr/local/sbin/lightningos-upgrade-lnd"
	lndUpgradeUnit         = "lightningos-lnd-upgrade"
	lndVerifyUnit          = "lightningos-lnd-verify"
	lndUpgradeHelperSHA256 = "187906eb44efd083d39a94247e62ee8c655e1d5b2bcfc5870a63dce51008b89e"
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
