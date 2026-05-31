package server

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestUpdatePeerswapConfigUpdatesRemoteWallet(t *testing.T) {
	values := testPeerswapConfigValues()
	values.ElementsRPCWallet = "peerswap_02" + strings.Repeat("a", 64)
	raw := "elementsd.rpcwallet=peerswap\nelementsd.rpchost=http://127.0.0.1\n"
	updated := updatePeerswapConfig(raw, values)
	if !strings.Contains(updated, "elementsd.rpcwallet="+values.ElementsRPCWallet) {
		t.Fatalf("expected derived remote wallet in peerswap config:\n%s", updated)
	}
	if strings.Contains(updated, "elementsd.rpcwallet=peerswap\n") {
		t.Fatalf("expected legacy peerswap wallet to be replaced:\n%s", updated)
	}
}

func TestApplyPeerswapConfigOverridesPreservesBitcoinSwaps(t *testing.T) {
	values := applyPeerswapConfigOverrides(testPeerswapConfigValues(), "bitcoinswaps=true\n")
	if values.BitcoinSwaps != "true" {
		t.Fatalf("expected bitcoinswaps override to be preserved, got %q", values.BitcoinSwaps)
	}

	updated := updatePeerswapConfig("bitcoinswaps=true\n", values)
	if !strings.Contains(updated, "bitcoinswaps=true") {
		t.Fatalf("expected updated peerswap config to keep bitcoinswaps=true:\n%s", updated)
	}

	cfg := map[string]any{}
	paths := peerswapPaths{ConfigDir: "/home/losop/.peerswap"}
	if !updatePSWebConfigMap(cfg, paths, values, bitcoinRPCConfig{}, false) {
		t.Fatalf("expected psweb config to change")
	}
	if cfg["BitcoinSwaps"] != true {
		t.Fatalf("expected psweb BitcoinSwaps=true, got %#v", cfg["BitcoinSwaps"])
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

func TestPeerswapServiceOmitsElementsDependencyForRemoteSource(t *testing.T) {
	paths := peerswapPaths{BinDir: "/opt/lightningos/apps/peerswap/bin"}
	raw := peerswapServiceContents(paths, peerswapElementsModeRemote)
	if strings.Contains(raw, "lightningos-elements.service") {
		t.Fatalf("expected remote peerswap service to omit local elements dependency:\n%s", raw)
	}

	local := peerswapServiceContents(paths, peerswapElementsModeLocal)
	if !strings.Contains(local, "lightningos-elements.service") {
		t.Fatalf("expected local peerswap service to depend on elements:\n%s", local)
	}
}

func TestNormalizePeerswapRemoteEndpointSplitsHostAndPort(t *testing.T) {
	endpoint, err := normalizePeerswapRemoteEndpoint("http://elements.br-ln.com:8086/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if endpoint.URL != "http://elements.br-ln.com:8086" {
		t.Fatalf("unexpected url: %s", endpoint.URL)
	}
	if endpoint.Host != "http://elements.br-ln.com" || endpoint.Port != 8086 {
		t.Fatalf("unexpected host/port: %s %d", endpoint.Host, endpoint.Port)
	}
}

func TestPeerswapRemoteWalletNameFromPubkey(t *testing.T) {
	pubkey := "02" + strings.Repeat("A", 64)
	wallet, err := peerswapRemoteWalletNameFromPubkey(pubkey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "peerswap_02" + strings.Repeat("a", 64)
	if wallet != expected {
		t.Fatalf("unexpected wallet name: %s", wallet)
	}
}

func TestTestPeerswapRemoteElementsRPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "elements" || pass != "secret" {
			t.Fatalf("unexpected basic auth user=%q pass=%q ok=%v", user, pass, ok)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"chain":"liquidv1"},"error":null}`))
	}))
	defer server.Close()

	chain, err := testPeerswapRemoteElementsRPC(context.Background(), peerswapElementsSource{
		Mode:     peerswapElementsModeRemote,
		URL:      server.URL,
		User:     "elements",
		Password: "secret",
		Wallet:   "peerswap",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chain != "liquidv1" {
		t.Fatalf("unexpected chain: %s", chain)
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
