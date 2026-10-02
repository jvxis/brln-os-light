package privileged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	lightningOSUpgradeHelperPath   = "/usr/local/sbin/lightningos-upgrade-app"
	lightningOSUpgradeUnit         = "lightningos-app-upgrade"
	lightningOSVerifyUnit          = "lightningos-app-verify"
	lightningOSUpgradeHelperSHA256 = "b474efd5737d1f90b12eb1f1c9ff4d7c8f2d1439b022741a87a3081d094cc9e6"
	// The exact shipped 0.5.33--0.5.39 helper. Recovery never accepts arbitrary
	// older scripts or disables the digest check. This exception only reaches
	// the 0.5.40 bridge; subsequent upgrades must use the new Manager's helper.
	legacyRecoveryHelperSHA256 = "6a6d39d79d642d4565aba4778bd381d9f72e24d96d105d1e597b9b2eb6ee1a4c"
)

func trustedLightningOSUpgradeHelper(content, version string) bool {
	digest := sha256.Sum256([]byte(content))
	return trustedLightningOSUpgradeDigest(hex.EncodeToString(digest[:]), version)
}

func trustedLightningOSUpgradeDigest(digest, version string) bool {
	return digest == lightningOSUpgradeHelperSHA256 || (digest == legacyRecoveryHelperSHA256 &&
		(strings.EqualFold(version, "0.5.40-beta") || version == "0.5.40"))
}

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
