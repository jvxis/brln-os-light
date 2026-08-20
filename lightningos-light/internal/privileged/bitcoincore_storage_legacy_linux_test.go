//go:build linux

package privileged

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestLegacyBitcoinStorageIdentityAcceptsAndConvertsVersion052Token(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to validate Bitcoin storage marker ownership")
	}
	root := t.TempDir()
	dataDir := filepath.Join(root, "bitcoin")
	if err := os.Mkdir(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, "storage_id")
	legacyID := "AbCdEfGhIjKlMnOpQrStUvWxYz01-_23"
	if err := os.WriteFile(legacyPath, []byte(legacyID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(dataDir, appmanifest.BitcoinCoreStorageMarker)
	if err := os.WriteFile(markerPath, []byte(legacyID+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(markerPath, 101, 101); err != nil {
		t.Fatal(err)
	}

	got, exists, err := readLegacyBitcoinCoreStorageID(legacyPath, dataDir)
	if err != nil || !exists || got != legacyID || !validLegacyStorageID(got) || validStorageID(got) {
		t.Fatalf("0.5.2 legacy adoption id/exists/error=%q/%v/%v", got, exists, err)
	}
	managedID, err := newBitcoinCoreStorageID()
	if err != nil || !validStorageID(managedID) || managedID == legacyID {
		t.Fatalf("managed identity/error=%q/%v", managedID, err)
	}
	if err := syncLegacyBitcoinCoreStorageID(legacyPath, managedID); err != nil {
		t.Fatal(err)
	}
	if err := writeBitcoinCoreStorageMarker(dataDir, managedID); err != nil {
		t.Fatal(err)
	}
	legacyRaw, err := os.ReadFile(legacyPath)
	if err != nil || strings.TrimSpace(string(legacyRaw)) != managedID {
		t.Fatalf("legacy identity after conversion=%q/%v", legacyRaw, err)
	}
	markerRaw, err := os.ReadFile(markerPath)
	if err != nil || strings.TrimSpace(string(markerRaw)) != managedID {
		t.Fatalf("marker after conversion=%q/%v", markerRaw, err)
	}
	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if markerInfo.Mode().Perm() != 0o640 {
		t.Fatalf("converted marker mode=%o", markerInfo.Mode().Perm())
	}
}

func TestLegacyBitcoinStorageIdentityRejectsInvalidVersion052Token(t *testing.T) {
	for _, value := range []string{
		strings.Repeat("a", 31),
		strings.Repeat("a", 33),
		strings.Repeat("a", 31) + "+",
		strings.Repeat("a", 31) + "/",
	} {
		if validLegacyStorageID(value) {
			t.Fatalf("invalid 0.5.2 storage identity accepted: %q", value)
		}
	}
}

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
