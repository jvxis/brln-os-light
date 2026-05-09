package server

import (
	"strings"
	"testing"
)

func TestDefaultPeerswapConfigIncludesElementsDataDir(t *testing.T) {
	values := testPeerswapConfigValues()
	raw := defaultPeerswapConfig(values)
	if !strings.Contains(raw, "elementsd.datadir=/mnt/liquid-ssd/lightningos/elements") {
		t.Fatalf("expected elements datadir in peerswap config:\n%s", raw)
	}
}

func TestUpdatePeerswapConfigUpdatesElementsDataDir(t *testing.T) {
	values := testPeerswapConfigValues()
	raw := "elementsd.datadir=/data/elements\nbitcoinswaps=true\n"
	updated := updatePeerswapConfig(raw, values)
	if !strings.Contains(updated, "elementsd.datadir=/mnt/liquid-ssd/lightningos/elements") {
		t.Fatalf("expected updated elements datadir:\n%s", updated)
	}
	if strings.Contains(updated, "elementsd.datadir=/data/elements") {
		t.Fatalf("expected old elements datadir to be replaced:\n%s", updated)
	}
}

func TestUpdatePSWebConfigMapNormalizesBitcoinHostAndElementsPaths(t *testing.T) {
	cfg := map[string]any{
		"ColorScheme":       "light",
		"BitcoinHost":       "http://127.0.0.1:8332:8332",
		"ElementsDir":       "home/losop/.elements",
		"ElementsDirMapped": "home/losop/.elements",
	}
	paths := peerswapPaths{ConfigDir: "/home/losop/.peerswap"}
	bitcoinCfg := bitcoinRPCConfig{
		Host: "http://127.0.0.1:8332",
		User: "minibolt",
		Pass: "secret",
	}

	if !updatePSWebConfigMap(cfg, paths, testPeerswapConfigValues(), bitcoinCfg, true) {
		t.Fatalf("expected psweb config to change")
	}
	if cfg["BitcoinHost"] != "http://127.0.0.1:8332" {
		t.Fatalf("expected normalized BitcoinHost, got %#v", cfg["BitcoinHost"])
	}
	if cfg["BitcoinUser"] != "minibolt" || cfg["BitcoinPass"] != "secret" {
		t.Fatalf("expected bitcoin credentials, got user=%#v pass=%#v", cfg["BitcoinUser"], cfg["BitcoinPass"])
	}
	if cfg["ElementsDir"] != "/mnt/liquid-ssd/lightningos/elements" || cfg["ElementsDirMapped"] != "/mnt/liquid-ssd/lightningos/elements" {
		t.Fatalf("expected absolute elements paths, got %#v and %#v", cfg["ElementsDir"], cfg["ElementsDirMapped"])
	}
	if cfg["ColorScheme"] != "light" {
		t.Fatalf("expected unrelated psweb settings to be preserved")
	}
}

func TestUpdatePSWebConfigMapSanitizesExistingDuplicateBitcoinHost(t *testing.T) {
	cfg := map[string]any{
		"BitcoinHost": "http://127.0.0.1:8332:8332",
	}
	paths := peerswapPaths{ConfigDir: "/home/losop/.peerswap"}

	if !updatePSWebConfigMap(cfg, paths, testPeerswapConfigValues(), bitcoinRPCConfig{}, false) {
		t.Fatalf("expected psweb config to change")
	}
	if cfg["BitcoinHost"] != "http://127.0.0.1:8332" {
		t.Fatalf("expected sanitized BitcoinHost, got %#v", cfg["BitcoinHost"])
	}
}

func TestPSWebServiceUsesExplicitDataDirAndUserEnv(t *testing.T) {
	paths := peerswapPaths{
		BinDir:    "/opt/lightningos/apps/peerswap/bin",
		ConfigDir: "/home/losop/.peerswap",
	}
	raw := pswebServiceContents(paths)
	if !strings.Contains(raw, "Environment=USER=losop") {
		t.Fatalf("expected USER env in psweb service:\n%s", raw)
	}
	if !strings.Contains(raw, "psweb -datadir /home/losop/.peerswap") {
		t.Fatalf("expected explicit psweb datadir:\n%s", raw)
	}
}

func testPeerswapConfigValues() peerswapConfigValues {
	return peerswapConfigValues{
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
}
