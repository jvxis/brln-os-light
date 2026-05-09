package server

import "testing"

func TestSuggestedStorageDataDir(t *testing.T) {
	tests := []struct {
		appID string
		mount string
		want  string
	}{
		{appID: bitcoinCoreAppID, mount: "/mnt/chain-ssd", want: "/mnt/chain-ssd/lightningos/bitcoin"},
		{appID: elementsAppID, mount: "/mnt/liquid-ssd/", want: "/mnt/liquid-ssd/lightningos/elements"},
	}
	for _, tc := range tests {
		t.Run(tc.appID, func(t *testing.T) {
			if got := suggestedStorageDataDir(tc.appID, tc.mount); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestStorageMountEligibility(t *testing.T) {
	eligibleMount := storageMount{
		Mount:     "/mnt/chain-ssd",
		FSType:    "ext4",
		Options:   "rw,relatime",
		FreeBytes: 20 * 1024 * 1024 * 1024,
	}
	if ok, reason := storageMountEligibility(bitcoinCoreAppID, eligibleMount, suggestedStorageDataDir(bitcoinCoreAppID, eligibleMount.Mount)); !ok {
		t.Fatalf("expected eligible mount, got reason %q", reason)
	}

	rootMount := eligibleMount
	rootMount.Mount = "/"
	if ok, _ := storageMountEligibility(bitcoinCoreAppID, rootMount, suggestedStorageDataDir(bitcoinCoreAppID, rootMount.Mount)); ok {
		t.Fatal("expected root filesystem to be ineligible")
	}

	exfatMount := eligibleMount
	exfatMount.FSType = "exfat"
	if ok, _ := storageMountEligibility(elementsAppID, exfatMount, suggestedStorageDataDir(elementsAppID, exfatMount.Mount)); ok {
		t.Fatal("expected non-Linux permissions filesystem to be ineligible")
	}
}
