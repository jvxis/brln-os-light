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
		{appID: electrsAppID, mount: "/mnt/chain-ssd", want: "/mnt/chain-ssd/lightningos/electrs"},
		{appID: mempoolAppID, mount: "/mnt/chain-ssd", want: "/mnt/chain-ssd/lightningos/mempool"},
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
	electrsMount := eligibleMount
	electrsMount.FreeBytes = 99 * 1024 * 1024 * 1024
	if ok, _ := storageMountEligibility(electrsAppID, electrsMount, suggestedStorageDataDir(electrsAppID, electrsMount.Mount)); ok {
		t.Fatal("expected Electrs volume below 100 GiB free to be rejected")
	}
	electrsMount.FreeBytes = 101 * 1024 * 1024 * 1024
	if ok, reason := storageMountEligibility(electrsAppID, electrsMount, suggestedStorageDataDir(electrsAppID, electrsMount.Mount)); !ok {
		t.Fatalf("expected Electrs volume above minimum to be eligible: %s", reason)
	}
	mempoolMount := eligibleMount
	mempoolMount.FreeBytes = 19 * 1024 * 1024 * 1024
	if ok, _ := storageMountEligibility(mempoolAppID, mempoolMount, suggestedStorageDataDir(mempoolAppID, mempoolMount.Mount)); ok {
		t.Fatal("expected Mempool volume below 20 GiB free to be rejected")
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

func TestPreferStorageMountUsesRealFilesystemOverAutomount(t *testing.T) {
	automount := storageMount{
		Mount:   "/mnt/blockchain",
		Source:  "systemd-1",
		FSType:  "autofs",
		Options: "rw,relatime",
	}
	raidMount := storageMount{
		Mount:     "/mnt/blockchain",
		Source:    "/dev/md127",
		FSType:    "xfs",
		Options:   "rw,noatime",
		FreeBytes: 926 * 1024 * 1024 * 1024,
	}
	if !preferStorageMount(raidMount, automount) {
		t.Fatal("expected xfs raid mount to replace autofs automount")
	}
	if preferStorageMount(automount, raidMount) {
		t.Fatal("expected autofs automount not to replace xfs raid mount")
	}
}
