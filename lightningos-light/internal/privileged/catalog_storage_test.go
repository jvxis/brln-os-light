package privileged

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCatalogMigrationTargetAllowsAppMutation(t *testing.T) {
	target := filepath.Join(t.TempDir(), "db")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(target, "index")
	if err := os.WriteFile(index, []byte("verified-copy"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index, []byte("compacted-by-running-app"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := validateCatalogMigrationTarget(target); err != nil {
		t.Fatalf("safe non-empty target mutation rejected: %v", err)
	}
}

func TestValidateCatalogMigrationTargetRejectsEmptyOrMissingTarget(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(empty, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := validateCatalogMigrationTarget(empty); err == nil {
		t.Fatal("empty migration target accepted")
	}
	if err := validateCatalogMigrationTarget(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing migration target accepted")
	}
}
