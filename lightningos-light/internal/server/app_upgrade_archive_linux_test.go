//go:build linux

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustedCheckoutArchiveUsesSafeRootPermissions(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root tar extraction regression; exercise in disposable Linux lab")
	}
	start := strings.Index(embeddedAppUpgradeScript, `"$GIT_BIN" -C "$source_repo_dir" archive "$EXPECTED_COMMIT"`)
	if start < 0 {
		t.Fatal("trusted checkout archive command missing")
	}
	end := strings.Index(embeddedAppUpgradeScript[start:], `TAG="trusted-checkout"`)
	if end < 0 {
		t.Fatal("trusted checkout extraction block missing")
	}
	block := embeddedAppUpgradeScript[start : start+end]
	repository := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repository
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v: %s", err, output)
		}
	}
	runGit("init", "--quiet")
	runGit("config", "tar.umask", "0002")
	for name, mode := range map[string]os.FileMode{"prepare-go-toolchain.sh": 0644, "executable.sh": 0755} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(repository, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	runGit("add", ".")
	runGit("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture")
	for _, mask := range []string{"022", "077"} {
		t.Run(mask, func(t *testing.T) {
			destination := t.TempDir()
			script := "set -euo pipefail\numask " + mask + "\nsource_repo_dir=$1\nworktree_dir=$2\nGIT_BIN=$(command -v git)\nTAR_BIN=$(command -v tar)\nEXPECTED_COMMIT=HEAD\n" + block
			if output, err := exec.Command("bash", "-c", script, "--", repository, destination).CombinedOutput(); err != nil {
				t.Fatalf("actual extraction: %v: %s", err, output)
			}
			for name, mode := range map[string]os.FileMode{"prepare-go-toolchain.sh": 0644, "executable.sh": 0755} {
				info, err := os.Stat(filepath.Join(destination, name))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != mode {
					t.Errorf("%s: got mode %o, want %o", name, info.Mode().Perm(), mode)
				}
			}
		})
	}
}
