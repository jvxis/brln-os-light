package privileged

import (
	"context"
	"errors"
)

const (
	lndPermissionsParentPath = "/data"
	lndPermissionsRootName   = "lnd"
	lndPermissionsUserName   = "lnd"
)

type NativeLNDPermissionsManager struct {
	parentPath        string
	lndName           string
	userName          string
	requireRootParent bool
	lookupIdentity    func(string) (int, int, error)
}

func NewNativeLNDPermissionsManager() *NativeLNDPermissionsManager {
	return &NativeLNDPermissionsManager{
		parentPath:        lndPermissionsParentPath,
		lndName:           lndPermissionsRootName,
		userName:          lndPermissionsUserName,
		requireRootParent: true,
		lookupIdentity:    lookupAppStorageIdentity,
	}
}

func (manager *NativeLNDPermissionsManager) Repair(ctx context.Context, dryRun bool) (LNDPermissionsState, error) {
	if manager == nil || manager.lookupIdentity == nil || manager.lndName != lndPermissionsRootName || manager.userName != lndPermissionsUserName || manager.parentPath == "" {
		return LNDPermissionsState{}, errors.New("LND permissions manager is unavailable")
	}
	if manager.requireRootParent && manager.parentPath != lndPermissionsParentPath {
		return LNDPermissionsState{}, errors.New("LND permissions parent policy is invalid")
	}
	uid, gid, err := manager.lookupIdentity(manager.userName)
	if err != nil || uid < 1 || gid < 1 {
		return LNDPermissionsState{}, errors.New("LND service identity is unavailable")
	}
	return manager.repair(ctx, uid, gid, dryRun)
}
