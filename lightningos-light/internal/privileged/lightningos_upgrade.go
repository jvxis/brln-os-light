package privileged

import (
	"context"
	"errors"
	"strings"
)

const (
	lightningOSUpgradeHelperPath   = "/usr/local/sbin/lightningos-upgrade-app"
	lightningOSUpgradeUnit         = "lightningos-app-upgrade"
	lightningOSVerifyUnit          = "lightningos-app-verify"
	lightningOSUpgradeHelperSHA256 = "6a6d39d79d642d4565aba4778bd381d9f72e24d96d105d1e597b9b2eb6ee1a4c"
)

type NativeLightningOSUpgradeManager struct {
	runner CommandRunner
}

func NewNativeLightningOSUpgradeManager(runner CommandRunner) *NativeLightningOSUpgradeManager {
	return &NativeLightningOSUpgradeManager{runner: runner}
}

func (manager *NativeLightningOSUpgradeManager) Start(ctx context.Context, params LightningOSUpgradeStartParams, dryRun bool) (LightningOSUpgradeState, error) {
	if manager == nil || manager.runner == nil {
		return LightningOSUpgradeState{}, errors.New("LightningOS upgrade manager is unavailable")
	}
	if !lndUpgradeVersionPattern.MatchString(params.Version) || !gitCommitPattern.MatchString(params.Commit) {
		return LightningOSUpgradeState{}, errors.New("LightningOS release identity is invalid")
	}
	tagVersion := strings.TrimPrefix(strings.TrimPrefix(params.Tag, "v"), "V")
	if !strings.EqualFold(tagVersion, params.Version) {
		return LightningOSUpgradeState{}, errors.New("LightningOS release tag does not match version")
	}
	return manager.start(ctx, params, dryRun)
}
