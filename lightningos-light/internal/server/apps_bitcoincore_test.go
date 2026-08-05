package server

import (
	"strings"
	"testing"
)

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
		DataDir:          "/mnt/bitcoin-ssd/bitcoin",
		StorageGuardPath: "/var/lib/lightningos/apps/bitcoincore/storage-guard.sh",
		StorageIDPath:    "/var/lib/lightningos/apps-data/bitcoincore/storage_id",
	}
	raw := bitcoinCoreComposeContents(paths)
	if !strings.Contains(raw, "- /mnt/bitcoin-ssd/bitcoin:/home/bitcoin/.bitcoin") {
		t.Fatalf("compose does not mount configured data dir:\n%s", raw)
	}
	for _, expected := range []string{
		`entrypoint: ["/bin/sh", "/lightningos-storage-guard.sh"]`,
		"- /var/lib/lightningos/apps/bitcoincore/storage-guard.sh:/lightningos-storage-guard.sh:ro",
		"- /var/lib/lightningos/apps-data/bitcoincore/storage_id:/lightningos-expected-storage-id:ro",
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
