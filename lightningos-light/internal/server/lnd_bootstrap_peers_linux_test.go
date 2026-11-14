package server

import (
	"bytes"
	"context"
	"os"
	"syscall"
	"testing"
)

func TestLNDBootstrapPeerLinuxOwnershipIsPreserved(t *testing.T) {
	migration, _ := newBootstrapMigrationFixture(t)
	before, err := os.Stat(migration.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := migration.run(context.Background()); err != nil || count != 2 {
		t.Fatalf("Linux migration: %d/%v", count, err)
	}
	after, err := os.Stat(migration.configPath)
	if err != nil {
		t.Fatal(err)
	}
	left := before.Sys().(*syscall.Stat_t)
	right := after.Sys().(*syscall.Stat_t)
	if left.Uid != right.Uid || left.Gid != right.Gid || left.Ino != right.Ino || left.Nlink != right.Nlink || before.Mode() != after.Mode() {
		t.Fatal("migration changed ownership, inode, links or permissions")
	}
}

func TestLNDBootstrapPeerLinuxReadOnlyAndUnsafeBackupPermissions(t *testing.T) {
	for _, mode := range []string{"private-backup-required", "readonly-config"} {
		t.Run(mode, func(t *testing.T) {
			migration, original := newBootstrapMigrationFixture(t)
			if mode == "readonly-config" {
				if os.Geteuid() == 0 {
					t.Skip("read-only permission denial must also be run as an unprivileged Linux user")
				}
				if err := os.Chmod(migration.configPath, 0440); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(migration.backupDir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := migration.run(context.Background()); err == nil {
				t.Fatal("unsafe permissions were ignored")
			}
			stored, err := os.ReadFile(migration.configPath)
			if err != nil || !bytes.Equal(stored, original) {
				t.Fatal("failed permission preflight changed the configuration")
			}
		})
	}
}
