//go:build !linux

package server

import (
	"context"

	"lightningos-light/internal/config"
)

func legacyPrivilegeTransitionCandidate(_ *config.Config, _ string) bool {
	return false
}

func startLegacyPrivilegeTransition(_ context.Context, _ *config.Config, _ appReleaseInfo, _ string) (legacyTransitionState, error) {
	return legacyTransitionNotApplicable, nil
}

func legacyTransitionUnitRunning(_ context.Context) bool {
	return false
}
