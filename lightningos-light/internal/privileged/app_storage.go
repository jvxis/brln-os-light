package privileged

import (
	"context"
	"errors"
	"os/user"
	"strconv"
)

const (
	appStorageParentPath = "/var/lib"
	appStorageStateName  = "lightningos"
	appStorageAppsName   = "apps"
	appStorageDataName   = "apps-data"
)

type NativeAppStorageManager struct {
	parentPath        string
	stateName         string
	userName          string
	requireRootParent bool
	lookupIdentity    func(string) (int, int, error)
}

func NewNativeAppStorageManager() *NativeAppStorageManager {
	return &NativeAppStorageManager{
		parentPath:        appStorageParentPath,
		stateName:         appStorageStateName,
		userName:          ExpectedCaller,
		requireRootParent: true,
		lookupIdentity:    lookupAppStorageIdentity,
	}
}

func (manager *NativeAppStorageManager) Ensure(ctx context.Context, dryRun bool) (AppStorageState, error) {
	if manager == nil || manager.lookupIdentity == nil || manager.parentPath == "" || manager.stateName != appStorageStateName || manager.userName != ExpectedCaller {
		return AppStorageState{}, errors.New("app storage manager is unavailable")
	}
	uid, gid, err := manager.lookupIdentity(manager.userName)
	if err != nil || uid < 1 || gid < 1 {
		return AppStorageState{}, errors.New("app storage identity is unavailable")
	}
	return manager.ensure(ctx, uid, gid, dryRun)
}

func lookupAppStorageIdentity(name string) (int, int, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return 0, 0, err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return 0, 0, err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return 0, 0, err
	}
	return uid, gid, nil
}
