package server

import (
	"strings"
	"testing"
)

func TestDefaultPeerswapConfigIncludesElementsDataDir(t *testing.T) {
	values := peerswapConfigValues{
		LndTLSPath:          "/data/lnd/tls.cert",
		LndMacaroonPath:     "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
		ElementsRPCUser:     "elements",
		ElementsRPCPass:     "secret",
		ElementsRPCHost:     "http://127.0.0.1",
		ElementsRPCPort:     7041,
		ElementsRPCWallet:   "peerswap",
		ElementsDataDir:     "/mnt/liquid-ssd/lightningos/elements",
		ElementsLiquidSwaps: "true",
		BitcoinSwaps:        "false",
	}
	raw := defaultPeerswapConfig(values)
	if !strings.Contains(raw, "elementsd.datadir=/mnt/liquid-ssd/lightningos/elements") {
		t.Fatalf("expected elements datadir in peerswap config:\n%s", raw)
	}
}

func TestUpdatePeerswapConfigUpdatesElementsDataDir(t *testing.T) {
	values := peerswapConfigValues{
		LndTLSPath:          "/data/lnd/tls.cert",
		LndMacaroonPath:     "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon",
		ElementsRPCUser:     "elements",
		ElementsRPCPass:     "secret",
		ElementsRPCHost:     "http://127.0.0.1",
		ElementsRPCPort:     7041,
		ElementsRPCWallet:   "peerswap",
		ElementsDataDir:     "/mnt/liquid-ssd/lightningos/elements",
		ElementsLiquidSwaps: "true",
		BitcoinSwaps:        "false",
	}
	raw := "elementsd.datadir=/data/elements\nbitcoinswaps=true\n"
	updated := updatePeerswapConfig(raw, values)
	if !strings.Contains(updated, "elementsd.datadir=/mnt/liquid-ssd/lightningos/elements") {
		t.Fatalf("expected updated elements datadir:\n%s", updated)
	}
	if strings.Contains(updated, "elementsd.datadir=/data/elements") {
		t.Fatalf("expected old elements datadir to be replaced:\n%s", updated)
	}
}
