//go:build linux

package privileged

import (
	"os"
	"path/filepath"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestLegacyBitcoinStorageIdentityAdoptionAndInPlaceSync(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to validate Bitcoin storage marker ownership")
	}
	root := t.TempDir()
	dataDir := filepath.Join(root, "bitcoin")
	if err := os.Mkdir(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, "storage_id")
	legacyID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.WriteFile(legacyPath, []byte(legacyID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dataDir, appmanifest.BitcoinCoreStorageMarker)
	if err := os.WriteFile(markerPath, []byte(legacyID+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(markerPath, 0, 101); err != nil {
		t.Fatal(err)
	}

	got, exists, err := readLegacyBitcoinCoreStorageID(legacyPath, dataDir)
	if err != nil || !exists || got != legacyID {
		t.Fatalf("legacy adoption id/exists/error=%q/%v/%v", got, exists, err)
	}
	before, err := os.Stat(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	managedID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := syncLegacyBitcoinCoreStorageID(legacyPath, managedID); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("legacy identity sync replaced the bind-mounted inode")
	}
	raw, err := os.ReadFile(legacyPath)
	if err != nil || string(raw) != managedID+"\n" || after.Mode().Perm() != 0o600 {
		t.Fatalf("legacy sync content/mode/error=%q/%o/%v", raw, after.Mode().Perm(), err)
	}
}

func TestLegacyBitcoinStorageIdentityRejectsMarkerMismatch(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to validate Bitcoin storage marker ownership")
	}
	root := t.TempDir()
	dataDir := filepath.Join(root, "bitcoin")
	if err := os.Mkdir(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, "storage_id")
	if err := os.WriteFile(legacyPath, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dataDir, appmanifest.BitcoinCoreStorageMarker)
	if err := os.WriteFile(markerPath, []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(markerPath, 0, 101); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLegacyBitcoinCoreStorageID(legacyPath, dataDir); err == nil {
		t.Fatal("mismatched legacy identity and root-owned marker were accepted")
	}
}
