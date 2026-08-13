//go:build linux

package privileged

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func testLNDPermissionsManager(parent string, uid, gid int) *NativeLNDPermissionsManager {
	return &NativeLNDPermissionsManager{
		parentPath:        parent,
		lndName:           lndPermissionsRootName,
		userName:          lndPermissionsUserName,
		requireRootParent: false,
		lookupIdentity: func(string) (int, int, error) {
			return uid, gid, nil
		},
	}
}

func TestLNDPermissionsDryRunRejectsSymlinkAndDoesNotCreate(t *testing.T) {
	parent := t.TempDir()
	manager := testLNDPermissionsManager(parent, 1234, 1234)
	state, err := manager.Repair(context.Background(), true)
	if err != nil || state.Status != "absent" || state.Changed {
		t.Fatalf("missing LND tree dry-run failed: state=%#v err=%v", state, err)
	}
	if _, err := os.Lstat(filepath.Join(parent, lndPermissionsRootName)); !os.IsNotExist(err) {
		t.Fatalf("dry-run created LND tree: %v", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(parent, lndPermissionsRootName)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Repair(context.Background(), true); err == nil {
		t.Fatal("symlinked LND root was accepted")
	}
}

func TestLNDPermissionsRepairChangesOnlyFixedMetadata(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root is required to exercise fchown")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, lndPermissionsRootName)
	mainnet := filepath.Join(root, "data", "chain", "bitcoin", "mainnet")
	if err := os.MkdirAll(mainnet, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{
		filepath.Join(root, "lnd.conf"):             []byte("config sentinel\n"),
		filepath.Join(root, "tls.cert"):             []byte("certificate sentinel\n"),
		filepath.Join(mainnet, "admin.macaroon"):    []byte("admin sentinel\n"),
		filepath.Join(mainnet, "readonly.macaroon"): []byte("readonly sentinel\n"),
		filepath.Join(mainnet, "channel.db"):        []byte("unmanaged sentinel\n"),
	}
	for path, content := range contents {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manager := testLNDPermissionsManager(parent, 12345, 12346)
	state, err := manager.Repair(context.Background(), false)
	if err != nil || state.Status != "ready" || !state.Changed {
		t.Fatalf("LND permissions repair failed: state=%#v err=%v", state, err)
	}
	directories := []string{root, filepath.Join(root, "data"), filepath.Join(root, "data", "chain"), filepath.Join(root, "data", "chain", "bitcoin"), mainnet}
	for _, path := range directories {
		assertLNDPermissionsMetadata(t, path, 12345, 12346, 0o750)
	}
	assertLNDPermissionsMetadata(t, filepath.Join(root, "lnd.conf"), 12345, 12346, 0o660)
	for _, path := range []string{filepath.Join(root, "tls.cert"), filepath.Join(mainnet, "admin.macaroon"), filepath.Join(mainnet, "readonly.macaroon")} {
		assertLNDPermissionsMetadata(t, path, 12345, 12346, 0o640)
	}
	unmanaged := filepath.Join(mainnet, "channel.db")
	assertLNDPermissionsMetadata(t, unmanaged, 0, 0, 0o600)
	for path, want := range contents {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("LND content changed for %s: %q/%v", path, got, err)
		}
	}
	outside := filepath.Join(parent, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(mainnet, "evil.macaroon")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Repair(context.Background(), true); err == nil {
		t.Fatal("symlinked LND macaroon was accepted")
	}
}

func assertLNDPermissionsMetadata(t *testing.T, path string, uid, gid uint32, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("missing Linux stat metadata for %s", path)
	}
	if stat.Uid != uid || stat.Gid != gid || info.Mode().Perm() != mode {
		t.Fatalf("unexpected metadata for %s: uid/gid/mode=%d/%d/%o", path, stat.Uid, stat.Gid, info.Mode().Perm())
	}
}
