package privileged

import (
	"context"
	"errors"
)

const (
	torUpgradeHelperPath   = "/usr/local/sbin/lightningos-check-tor-update"
	torUpgradeUnit         = "lightningos-tor-upgrade"
	torVerifyUnit          = "lightningos-tor-verify"
	torUpgradeHelperSHA256 = "3ba5b795d3a45403abc47126639ae76d0e3a0b2beaf0f8b231d51a87832240c5"
	torAptGetPath          = "/usr/bin/apt-get"
)

type NativeTorUpgradeManager struct {
	runner CommandRunner
}

func NewNativeTorUpgradeManager(runner CommandRunner) *NativeTorUpgradeManager {
	return &NativeTorUpgradeManager{runner: runner}
}

func (manager *NativeTorUpgradeManager) Refresh(ctx context.Context, dryRun bool) (TorUpgradeState, error) {
	if manager == nil || manager.runner == nil {
		return TorUpgradeState{}, errors.New("Tor upgrade manager is unavailable")
	}
	state := TorUpgradeState{Status: "validated"}
	if dryRun {
		return state, nil
	}
	if _, err := manager.runner.Run(ctx, torAptGetPath, "-o", "DPkg::Lock::Timeout=60", "update"); err != nil {
		return TorUpgradeState{}, errors.New("fixed Tor package metadata refresh failed")
	}
	state.Status = "refreshed"
	return state, nil
}

func (manager *NativeTorUpgradeManager) Start(ctx context.Context, params TorUpgradeStartParams, dryRun bool) (TorUpgradeState, error) {
	if manager == nil || manager.runner == nil {
		return TorUpgradeState{}, errors.New("Tor upgrade manager is unavailable")
	}
	return manager.start(ctx, params, dryRun)
}
