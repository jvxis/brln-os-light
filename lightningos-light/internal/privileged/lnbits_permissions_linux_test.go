//go:build linux

package privileged

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestPrepareLNbitsWritableDataAssignsClosedContainerUser(t *testing.T) {
	dataDir := t.TempDir()
	databasePath := filepath.Join(dataDir, "database.sqlite3")
	extensionDir := filepath.Join(dataDir, "extensions")
	if err := os.Mkdir(extensionDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databasePath, []byte("database"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := prepareLNbitsWritableData(dataDir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dataDir, extensionDir, databasePath} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if os.Geteuid() == 0 && info.Sys().(*syscall.Stat_t).Uid != appmanifest.LNbitsContainerUID {
			t.Fatalf("%s owner=%d want=%d", path, info.Sys().(*syscall.Stat_t).Uid, appmanifest.LNbitsContainerUID)
		}
	}
	if info, _ := os.Lstat(databasePath); info.Mode().Perm() != 0640 {
		t.Fatalf("database mode=%o want=640", info.Mode().Perm())
	}
}

func TestPrepareLNbitsWritableDataRejectsSymlink(t *testing.T) {
	dataDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dataDir, "unsafe")); err != nil {
		t.Fatal(err)
	}
	if err := prepareLNbitsWritableData(dataDir); err == nil {
		t.Fatal("expected symlinked LNbits data entry to be rejected")
	}
}

func TestValidateLNbitsHostIdentityFilesRejectsNumericCollisions(t *testing.T) {
	cleanPasswd := []byte("root:x:0:0:root:/root:/bin/sh\n")
	cleanGroup := []byte("root:x:0:\n")
	if err := validateLNbitsHostIdentityFiles(cleanPasswd, cleanGroup); err != nil {
		t.Fatal(err)
	}
	passwdCollision := []byte("lnbits-host:x:65532:100::/nonexistent:/usr/sbin/nologin\n")
	if err := validateLNbitsHostIdentityFiles(passwdCollision, cleanGroup); err == nil {
		t.Fatal("expected host UID collision to be rejected")
	}
	groupCollision := []byte("lnbits-host:x:65532:\n")
	if err := validateLNbitsHostIdentityFiles(cleanPasswd, groupCollision); err == nil {
		t.Fatal("expected host GID collision to be rejected")
	}
}
