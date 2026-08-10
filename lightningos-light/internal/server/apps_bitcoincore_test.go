package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

func TestBitcoinCoreImageIsPinned(t *testing.T) {
	if bitcoinCoreImage != appmanifest.BitcoinCoreImage {
		t.Fatalf("server image %q differs from catalog image %q", bitcoinCoreImage, appmanifest.BitcoinCoreImage)
	}
	if strings.HasSuffix(bitcoinCoreImage, ":latest") {
		t.Fatalf("Bitcoin Core image must not use latest: %q", bitcoinCoreImage)
	}
}

func TestEnsureBitcoinCoreImageEnforceUsesBroker(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", imageStatus: "ready"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureBitcoinCoreImage(context.Background()); err != nil {
		t.Fatalf("ensure image: %v", err)
	}
	if client.prepareCalls != 1 || client.appID != appmanifest.BitcoinCoreID || client.imageVariant != string(appmanifest.BitcoinCoreImageNode) || client.imageDryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
}

func TestEnsureBitcoinCoreImageFailsClosedOnBrokerError(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", imageErr: errors.New("rejected")}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := ensureBitcoinCoreImage(context.Background()); err == nil {
		t.Fatal("expected broker failure")
	}
	if client.prepareCalls != 1 {
		t.Fatalf("unexpected broker calls: %#v", client)
	}
}

func TestEnsureBitcoinCoreImageFailsClosedOutsideEnforce(t *testing.T) {
	for _, mode := range []string{"", "disabled", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "" {
				system.ConfigurePrivilegedClient(nil)
			} else {
				system.ConfigurePrivilegedClient(&cpuMinerPrivilegedClient{mode: mode})
			}
			t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

			err := ensureBitcoinCoreImage(context.Background())
			if err == nil || !strings.Contains(err.Error(), "requires privileged broker enforce mode") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateBitcoinCoreStorageEnforceUsesBroker(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := validateBitcoinCoreInstallDataDir(context.Background(), "/mnt/bitcoin-ssd/bitcoin"); err != nil {
		t.Fatalf("validate storage: %v", err)
	}
	if client.storageCalls != 1 || client.storageDataDir != "/mnt/bitcoin-ssd/bitcoin" || client.storageDryRun {
		t.Fatalf("unexpected broker call: %#v", client)
	}
}

func TestNormalizeBitcoinCoreDataDir(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "blank defaults", value: "", want: bitcoinCoreDefaultDataDir},
		{name: "cleans path", value: "/mnt/bitcoin/../bitcoin/data/", want: "/mnt/bitcoin/data"},
		{name: "rejects relative", value: "mnt/bitcoin/data", wantErr: true},
		{name: "rejects root", value: "/", wantErr: true},
		{name: "rejects data root", value: "/data", wantErr: true},
		{name: "rejects system dir", value: "/var/lib/bitcoin", wantErr: true},
		{name: "rejects lnd dir", value: "/data/lnd/bitcoin", wantErr: true},
		{name: "rejects elements dir", value: "/data/elements/bitcoin", wantErr: true},
		{name: "rejects bitcoin subdir", value: "/data/bitcoin/custom", wantErr: true},
		{name: "rejects spaces", value: "/mnt/bitcoin ssd/data", wantErr: true},
		{name: "rejects shell chars", value: "/mnt/bitcoin;ssd/data", wantErr: true},
		{name: "allows default", value: "/data/bitcoin", want: "/data/bitcoin"},
		{name: "allows sibling under data", value: "/data/bitcoin-ssd", want: "/data/bitcoin-ssd"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeBitcoinCoreDataDir(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestBitcoinCoreComposeContentsUsesConfiguredDataDir(t *testing.T) {
	paths := bitcoinCorePaths{
		DataDir: "/mnt/bitcoin-ssd/bitcoin",
	}
	raw := bitcoinCoreComposeContents(paths)
	if !strings.Contains(raw, "- /mnt/bitcoin-ssd/bitcoin:/home/bitcoin/.bitcoin") {
		t.Fatalf("compose does not mount configured data dir:\n%s", raw)
	}
	for _, expected := range []string{
		`entrypoint: ["/bin/sh", "/lightningos-storage-guard.sh"]`,
		`command: ["bitcoind"]`,
		"- " + appmanifest.BitcoinCoreExecutionRoot + "/storage-guard.sh:/lightningos-storage-guard.sh:ro",
		"- " + appmanifest.BitcoinCoreStorageIDPath + ":/lightningos-expected-storage-id:ro",
	} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("compose is missing storage guard %q:\n%s", expected, raw)
		}
	}
}

func TestBitcoinCoreStorageGuardRefusesWrongVolume(t *testing.T) {
	raw := bitcoinCoreStorageGuardContents()
	for _, expected := range []string{
		".lightningos-storage-id",
		`[ "$actual" != "$expected" ]`,
		"refusing to start bitcoind",
		`exec /entrypoint.sh "$@"`,
	} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("storage guard is missing %q:\n%s", expected, raw)
		}
	}
}

func TestBitcoinCoreLifecycleAndStatusRequireEnforceBroker(t *testing.T) {
	client := &cpuMinerPrivilegedClient{mode: "enforce", inspectStatus: "running"}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	if err := runBitcoinCoreLifecycle(context.Background(), "restart"); err != nil {
		t.Fatal(err)
	}
	if client.appCalls != 1 || client.appID != appmanifest.BitcoinCoreID || client.action != "restart" || client.dryRun {
		t.Fatalf("unexpected lifecycle broker call: %#v", client)
	}
	status, err := inspectBitcoinCoreStatus(context.Background())
	if err != nil || status != "running" || client.inspectCalls != 1 || client.inspectAppID != appmanifest.BitcoinCoreID {
		t.Fatalf("status/error/client=%q/%v/%#v", status, err, client)
	}
}

func TestBitcoinCoreLifecycleAndStatusFailClosedOutsideEnforce(t *testing.T) {
	for _, mode := range []string{"disabled", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			client := &cpuMinerPrivilegedClient{mode: mode}
			system.ConfigurePrivilegedClient(client)
			t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
			if err := runBitcoinCoreLifecycle(context.Background(), "start"); err == nil {
				t.Fatal("lifecycle unexpectedly used legacy fallback")
			}
			if _, err := inspectBitcoinCoreStatus(context.Background()); err == nil {
				t.Fatal("status unexpectedly used legacy fallback")
			}
		})
	}
}
