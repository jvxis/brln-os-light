package server

import (
	"path/filepath"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestBarkWalletDefinitionUsesClosedCatalogPort(t *testing.T) {
	definition := barkWalletDefinition()
	if definition.ID != appmanifest.BarkWalletID || definition.Port != appmanifest.BarkWalletPort {
		t.Fatalf("unexpected Bark Wallet definition: %#v", definition)
	}
	if len(definition.SecurityNotices) != 0 {
		t.Fatalf("Bark Wallet received unrelated LND notices: %#v", definition.SecurityNotices)
	}
}

func TestBarkWalletManagerPathsRemainLegacyCleanupOnly(t *testing.T) {
	paths := barkWalletAppPaths()
	if paths.Root != filepath.Join(appsRoot, appmanifest.BarkWalletID) {
		t.Fatalf("unexpected legacy app root: %q", paths.Root)
	}
	wantPassword := filepath.Join(appsDataRoot, appmanifest.BarkWalletID, "auth", "ui_password")
	if paths.AdminPasswordPath != wantPassword {
		t.Fatalf("unexpected password disclosure path: %q", paths.AdminPasswordPath)
	}
}
