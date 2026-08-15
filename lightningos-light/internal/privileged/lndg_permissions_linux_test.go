//go:build linux

package privileged

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestPrepareLNDgWritableDataPreservesPrivateManagerSecrets(t *testing.T) {
	dataDir := t.TempDir()
	logPath := filepath.Join(dataDir, "lndg-controller.log")
	secretPath := filepath.Join(dataDir, "lndg-admin.txt")
	if err := os.WriteFile(logPath, []byte("log"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	secretBefore, err := os.Lstat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	secretOwnerBefore := secretBefore.Sys().(*syscall.Stat_t).Uid
	if err := prepareLNDgWritableData(dataDir); err != nil {
		t.Fatal(err)
	}
	logInfo, err := os.Lstat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	secretInfo, err := os.Lstat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 && logInfo.Sys().(*syscall.Stat_t).Uid != appmanifest.LNDgContainerUID {
		t.Fatalf("log owner=%d want=%d", logInfo.Sys().(*syscall.Stat_t).Uid, appmanifest.LNDgContainerUID)
	}
	if secretInfo.Sys().(*syscall.Stat_t).Uid != secretOwnerBefore || secretInfo.Mode().Perm() != 0600 {
		t.Fatalf("manager secret ownership/mode changed: uid=%d mode=%o", secretInfo.Sys().(*syscall.Stat_t).Uid, secretInfo.Mode().Perm())
	}
}

func TestPrepareLNDgWritableDataRejectsSymlink(t *testing.T) {
	dataDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dataDir, "unsafe")); err != nil {
		t.Fatal(err)
	}
	if err := prepareLNDgWritableData(dataDir); err == nil {
		t.Fatal("expected symlinked LNDg data entry to be rejected")
	}
}
