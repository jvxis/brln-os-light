package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
)

func TestPeerswapServiceLifecyclePersistsAcrossReboots(t *testing.T) {
	paths := appmanifest.DefaultPeerSwapPaths()
	for _, unit := range []string{appmanifest.PeerSwapServiceUnit(paths, peerswapElementsModeLocal), appmanifest.PeerSwapWebServiceUnit(paths)} {
		if !strings.Contains(unit, "WantedBy=multi-user.target") {
			t.Fatalf("PeerSwap service must remain enable-able across reboots:\n%s", unit)
		}
	}
}

func TestPeerswapBundleChecksumsMatchPackagedAssets(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate peerswap test source")
	}
	assetDir := filepath.Join(
		filepath.Dir(testFile),
		"..", "..", "assets", "binaries", "peerswap",
		peerswapAssetsVersion, peerswapAssetsArch,
	)
	if err := ensurePeerswapBinaryChecksums(assetDir); err != nil {
		t.Fatalf("packaged Peerswap assets do not match compiled bundle metadata: %v", err)
	}
}

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
	paths := peerswapPaths{ConfigDir: appmanifest.DefaultPeerSwapPaths().RuntimeDir}
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
	paths := peerswapPaths{ConfigDir: appmanifest.DefaultPeerSwapPaths().RuntimeDir}
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
	paths := peerswapPaths{ConfigDir: appmanifest.DefaultPeerSwapPaths().RuntimeDir}

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
		ConfigDir: appmanifest.DefaultPeerSwapPaths().RuntimeDir,
	}
	raw := pswebServiceContents(paths)
	if !strings.Contains(raw, "Environment=USER="+appmanifest.PeerSwapUser) {
		t.Fatalf("expected USER env in psweb service:\n%s", raw)
	}
	if !strings.Contains(raw, "psweb -datadir "+appmanifest.DefaultPeerSwapPaths().RuntimeDir) {
		t.Fatalf("expected explicit psweb datadir:\n%s", raw)
	}
	if strings.Contains(raw, "SupplementaryGroups=lnd") || strings.Contains(raw, "User=losop") {
		t.Fatalf("psweb must not inherit human/LND-group privileges:\n%s", raw)
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

	ipv6, err := normalizePeerswapRemoteEndpoint("https://[2001:db8::1]:7041")
	if err != nil {
		t.Fatalf("unexpected IPv6 error: %v", err)
	}
	if ipv6.URL != "https://[2001:db8::1]:7041" || ipv6.Host != "https://[2001:db8::1]" || ipv6.Port != 7041 {
		t.Fatalf("unexpected IPv6 endpoint: %+v", ipv6)
	}
}

func TestNormalizePeerswapRemoteEndpointRejectsRequestSmugglingParts(t *testing.T) {
	for _, raw := range []string{
		"ftp://elements.example:7041",
		"http://user:pass@elements.example:7041",
		"http://elements.example:7041/wallet/other",
		"http://elements.example:7041/?query=1",
		"http://elements.example:7041/#fragment",
		"http://elements_example:7041",
		"http://elements.example:99999",
		"http://elements.example:7041\nInjected: value",
	} {
		if _, err := normalizePeerswapRemoteEndpoint(raw); err == nil {
			t.Fatalf("unsafe remote Elements endpoint accepted: %q", raw)
		}
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

func TestNormalizePeerswapRemoteSourceDoesNotRequireLNDIdentity(t *testing.T) {
	source, err := normalizePeerswapElementsSourceRequest(peerswapElementsSourceRequest{
		Mode:     peerswapElementsModeRemote,
		URL:      "http://elements.br-ln.com:8086",
		User:     "elements",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source.URL != "http://elements.br-ln.com:8086" {
		t.Fatalf("unexpected URL: %s", source.URL)
	}
	if source.Wallet != "" {
		t.Fatalf("remote connectivity validation must not derive an LND wallet, got %q", source.Wallet)
	}
}

func TestIsPeerswapRemoteWalletName(t *testing.T) {
	valid := "peerswap_02" + strings.Repeat("a", 64)
	if !isPeerswapRemoteWalletName(valid) {
		t.Fatalf("expected valid remote wallet name")
	}
	for _, invalid := range []string{"peerswap", "peerswap_not-a-pubkey", "other_02" + strings.Repeat("a", 64)} {
		if isPeerswapRemoteWalletName(invalid) {
			t.Fatalf("expected invalid remote wallet name: %q", invalid)
		}
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

func TestTestPeerswapRemoteElementsRPCDoesNotFollowRedirects(t *testing.T) {
	redirectHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectHits++
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"chain":"liquidv1"},"error":null}`))
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	_, err := testPeerswapRemoteElementsRPC(context.Background(), peerswapElementsSource{
		Mode:     peerswapElementsModeRemote,
		URL:      source.URL,
		User:     "elements",
		Password: "secret",
	})
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("remote Elements redirect was not rejected: %v", err)
	}
	if redirectHits != 0 {
		t.Fatalf("remote Elements credentials followed a redirect (%d target hits)", redirectHits)
	}
}

func TestPeerSwapMacaroonPermissionsMatchUpstreamRPCInventory(t *testing.T) {
	got := lndclient.MacaroonPermissionStrings(peerSwapMacaroonPermissions())
	want := []string{
		"address:write",
		"info:read",
		"invoices:read",
		"invoices:write",
		"offchain:read",
		"offchain:write",
		"onchain:read",
		"onchain:write",
		"peers:read",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected PeerSwap macaroon permissions: got %#v want %#v", got, want)
	}
	for _, permission := range got {
		if permission == "macaroons:write" {
			t.Fatal("PeerSwap must never receive macaroon administration permission")
		}
	}
}

func testPeerswapConfigValues() peerswapConfigValues {
	return peerswapConfigValues{
		LndTLSPath:          appmanifest.DefaultPeerSwapPaths().LNDTLSCertPath,
		LndMacaroonPath:     appmanifest.DefaultPeerSwapPaths().LNDMacaroonPath,
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
