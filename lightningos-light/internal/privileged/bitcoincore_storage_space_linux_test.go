//go:build linux

package privileged

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestBitcoinStorageSpacePreservesVerifiedExistingVolume(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to validate enrolled volume ownership")
	}
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *BitcoinCoreStorageManager, string)
		allow  bool
	}{
		{name: "matching enrolled volume", allow: true},
		{name: "missing marker", mutate: func(t *testing.T, _ *BitcoinCoreStorageManager, dir string) {
			mustStorageSpace(t, os.Remove(filepath.Join(dir, appmanifest.BitcoinCoreStorageMarker)))
		}},
		{name: "different volume", mutate: func(t *testing.T, _ *BitcoinCoreStorageManager, dir string) {
			mustStorageSpace(t, os.WriteFile(filepath.Join(dir, appmanifest.BitcoinCoreStorageMarker), []byte(strings.Repeat("b", 48)+"\n"), 0640))
		}},
		{name: "container owned marker", mutate: func(t *testing.T, _ *BitcoinCoreStorageManager, dir string) {
			mustStorageSpace(t, os.Chown(filepath.Join(dir, appmanifest.BitcoinCoreStorageMarker), 101, 101))
		}},
		{name: "writable marker", mutate: func(t *testing.T, _ *BitcoinCoreStorageManager, dir string) {
			mustStorageSpace(t, os.Chmod(filepath.Join(dir, appmanifest.BitcoinCoreStorageMarker), 0660))
		}},
		{name: "missing metadata", mutate: func(t *testing.T, manager *BitcoinCoreStorageManager, _ string) {
			mustStorageSpace(t, os.Remove(filepath.Join(manager.storageRoot(), bitcoinCoreStorageIDFile)))
		}},
		{name: "different enrolled path", mutate: func(t *testing.T, manager *BitcoinCoreStorageManager, dir string) {
			mustStorageSpace(t, os.WriteFile(filepath.Join(manager.storageRoot(), bitcoinCoreStorageDataDirFile), []byte(dir+"-other\n"), 0600))
		}},
		{name: "writable metadata", mutate: func(t *testing.T, manager *BitcoinCoreStorageManager, _ string) {
			mustStorageSpace(t, os.Chmod(filepath.Join(manager.storageRoot(), bitcoinCoreStorageIDFile), 0660))
		}},
		{name: "container owned metadata", mutate: func(t *testing.T, manager *BitcoinCoreStorageManager, _ string) {
			mustStorageSpace(t, os.Chown(filepath.Join(manager.storageRoot(), bitcoinCoreStorageIDFile), 101, 101))
		}},
		{name: "marker symlink", mutate: func(t *testing.T, _ *BitcoinCoreStorageManager, dir string) {
			marker := filepath.Join(dir, appmanifest.BitcoinCoreStorageMarker)
			mustStorageSpace(t, os.Rename(marker, marker+".original"))
			mustStorageSpace(t, os.Symlink(marker+".original", marker))
		}},
		{name: "volume symlink", mutate: func(t *testing.T, _ *BitcoinCoreStorageManager, dir string) {
			mustStorageSpace(t, os.Rename(dir, dir+".original"))
			mustStorageSpace(t, os.Symlink(dir+".original", dir))
		}},
		{name: "metadata ancestor symlink", mutate: func(t *testing.T, manager *BitcoinCoreStorageManager, _ string) {
			root := manager.PrivilegedAppsRoot
			mustStorageSpace(t, os.Rename(root, root+".original"))
			mustStorageSpace(t, os.Symlink(root+".original", root))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, dir := storageSpaceFixture(t)
			available, err := storageAvailableKiB(dir)
			mustStorageSpace(t, err)
			if available < 1024*1024 {
				t.Skip("requires at least 1 GiB available for the existing-volume policy")
			}
			// Force the initial-enrollment check to fail on this real filesystem.
			// Only a matching, trusted existing enrollment may be reused.
			manager.MinFreeKiB = ^uint64(0)
			if test.mutate != nil {
				test.mutate(t, manager, dir)
			}
			before, err := os.Lstat(filepath.Join(manager.storageRoot(), bitcoinCoreStorageIDFile))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			err = manager.validateFreeSpace(dir)
			if (err == nil) != test.allow {
				t.Fatalf("space validation: allowed=%v, expected=%v, error=%v", err == nil, test.allow, err)
			}
			if before != nil {
				after, err := os.Lstat(filepath.Join(manager.storageRoot(), bitcoinCoreStorageIDFile))
				mustStorageSpace(t, err)
				if !os.SameFile(before, after) || before.Mode() != after.Mode() || before.ModTime() != after.ModTime() {
					t.Fatal("space validation modified storage metadata")
				}
			}
		})
	}
}

func TestBitcoinStorageSpaceStillRequiresInitialEnrollmentReserve(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to validate enrolled volume ownership")
	}
	manager, dir := storageSpaceFixture(t)
	mustStorageSpace(t, os.Remove(filepath.Join(manager.storageRoot(), bitcoinCoreStorageIDFile)))
	mustStorageSpace(t, os.Remove(filepath.Join(manager.storageRoot(), bitcoinCoreStorageDataDirFile)))
	manager.MinFreeKiB = ^uint64(0)
	if err := manager.validateFreeSpace(dir); err == nil {
		t.Fatal("an unenrolled directory bypassed the initial free-space reserve")
	}
	manager.MinFreeKiB = 1
	if err := manager.validateFreeSpace(dir); err != nil {
		t.Fatalf("sufficient initial-install space rejected: %v", err)
	}
}

func storageSpaceFixture(t *testing.T) (*BitcoinCoreStorageManager, string) {
	t.Helper()
	base := t.TempDir()
	manager := &BitcoinCoreStorageManager{PrivilegedAppsRoot: filepath.Join(base, "privileged")}
	dir := filepath.Join(base, "bitcoin")
	mustStorageSpace(t, os.MkdirAll(manager.storageRoot(), 0700))
	mustStorageSpace(t, os.Mkdir(dir, 0750))
	id := strings.Repeat("a", 48)
	for name, value := range map[string]string{bitcoinCoreStorageDataDirFile: dir, bitcoinCoreStorageIDFile: id} {
		mustStorageSpace(t, os.WriteFile(filepath.Join(manager.storageRoot(), name), []byte(value+"\n"), 0600))
	}
	marker := filepath.Join(dir, appmanifest.BitcoinCoreStorageMarker)
	mustStorageSpace(t, os.WriteFile(marker, []byte(id+"\n"), 0640))
	mustStorageSpace(t, os.Chmod(marker, 0640))
	mustStorageSpace(t, os.Chown(marker, 0, 101))
	return manager, dir
}

func mustStorageSpace(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
