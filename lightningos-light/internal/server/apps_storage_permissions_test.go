package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAppStorageInstallArgsUsesOnlyRequestedPathsAndManagerIdentity(t *testing.T) {
	got := appStorageInstallArgs(123, 456, appsRoot)
	want := []string{
		"/usr/bin/install",
		"-d",
		"-m", "0750",
		"-o", "123",
		"-g", "456",
		"--",
		"/var/lib/lightningos/apps",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appStorageInstallArgs() = %#v, want %#v", got, want)
	}
}

func TestEnsureWritableDirectoriesCreatesAndChecksRoots(t *testing.T) {
	base := t.TempDir()
	paths := []string{
		filepath.Join(base, "apps"),
		filepath.Join(base, "apps-data"),
	}
	if err := ensureWritableDirectories(paths...); err != nil {
		t.Fatalf("ensureWritableDirectories() error = %v", err)
	}
	for _, path := range paths {
		entries, err := os.ReadDir(path)
		if err != nil {
			t.Fatalf("ReadDir(%q): %v", path, err)
		}
		if len(entries) != 0 {
			t.Fatalf("permission probe left entries in %q: %v", path, entries)
		}
	}
}
