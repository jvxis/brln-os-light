//go:build linux

package privileged

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func testAppStorageManager(parent string, uid, gid int) *NativeAppStorageManager {
	return &NativeAppStorageManager{
		parentPath:        parent,
		stateName:         appStorageStateName,
		userName:          ExpectedCaller,
		requireRootParent: false,
		lookupIdentity: func(string) (int, int, error) {
			return uid, gid, nil
		},
	}
}

func TestAppStorageDryRunRejectsSymlinkAndDoesNotCreate(t *testing.T) {
	parent := t.TempDir()
	manager := testAppStorageManager(parent, 1234, 1234)
	state, err := manager.Ensure(context.Background(), true)
	if err != nil || state.Status != "validated" || !state.Changed {
		t.Fatalf("missing storage dry-run failed: state=%#v err=%v", state, err)
	}
	if _, err := os.Lstat(filepath.Join(parent, appStorageStateName)); !os.IsNotExist(err) {
		t.Fatalf("dry-run created app storage: %v", err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(parent, appStorageStateName)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Ensure(context.Background(), true); err == nil {
		t.Fatal("symlinked app storage root was accepted")
	}
}

func TestAppStorageEnsureUsesFixedTreeAndDescriptorOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root is required to exercise fchown")
	}
	parent := t.TempDir()
	manager := testAppStorageManager(parent, 12345, 12345)
	state, err := manager.Ensure(context.Background(), false)
	if err != nil || state.Status != "ready" || !state.Changed {
		t.Fatalf("app storage ensure failed: state=%#v err=%v", state, err)
	}
	for _, relative := range []string{appStorageStateName, filepath.Join(appStorageStateName, appStorageAppsName), filepath.Join(appStorageStateName, appStorageDataName)} {
		info, err := os.Lstat(filepath.Join(parent, relative))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o750 {
			t.Fatalf("unexpected app storage directory %s: info=%#v err=%v", relative, info, err)
		}
	}
}
