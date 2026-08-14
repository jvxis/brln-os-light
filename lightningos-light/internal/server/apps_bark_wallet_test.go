package server

import (
	"context"
	"path/filepath"
	"testing"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

type barkWalletTestClient struct {
	*cpuMinerPrivilegedClient
	installed      bool
	status         string
	ensureCalls    int
	lifecycleCalls int
}

func (client *barkWalletTestClient) BarkWalletStatus(context.Context) (bool, string, bool, bool, error) {
	return client.installed, client.status, false, false, nil
}
func (client *barkWalletTestClient) EnsureBarkWallet(context.Context, bool) (string, error) {
	client.ensureCalls++
	return "ready", nil
}
func (client *barkWalletTestClient) BarkWalletLifecycle(context.Context, string, bool) (string, error) {
	client.lifecycleCalls++
	return "stopped", nil
}
func (client *barkWalletTestClient) RemoveBarkWallet(context.Context, bool) error { return nil }
func (client *barkWalletTestClient) EnsureBarkWalletFirewall(context.Context, bool) (string, error) {
	return "ready", nil
}
func (client *barkWalletTestClient) ReadBarkWalletPassword(context.Context) (string, error) {
	return "", nil
}
func (client *barkWalletTestClient) ResetBarkWalletPassword(context.Context, bool) error { return nil }

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

func TestStopBarkWalletIsIdempotentWithoutMigratingStoppedInstall(t *testing.T) {
	client := &barkWalletTestClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: "enforce"},
		installed:                true,
		status:                   "stopped",
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := (&Server{}).stopBarkWallet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.ensureCalls != 0 || client.lifecycleCalls != 0 {
		t.Fatalf("stopped Bark Wallet was mutated: %#v", client)
	}
}
