package server

import (
	"strings"
	"testing"
)

const testLNDConfig = `[Application Options]
alias=Test Node
color=#ff9900
numgraphsyncpeers=5
no-disconnect-on-pong-failure=true

[Bitcoin]
bitcoin.mainnet=1

[tor]
tor.active=true
tor.skip-proxy-for-clearnet-targets=false
tor.streamisolation=true
`

func TestParseLNDUserConfNetworkSettings(t *testing.T) {
	conf := parseLNDUserConf(testLNDConfig)
	if conf.networkMode() != "private" {
		t.Fatalf("expected private mode, got %q", conf.networkMode())
	}
	if conf.GraphSyncPeers != 5 {
		t.Fatalf("expected 5 graph sync peers, got %d", conf.GraphSyncPeers)
	}
	if !conf.NoDisconnectOnPongFailure {
		t.Fatal("expected no-disconnect-on-pong-failure to be enabled")
	}
}

func TestUpdateLNDNetworkOptionsHybrid(t *testing.T) {
	mode := "hybrid"
	peers := 8
	disconnect := true
	updated := updateLNDNetworkOptions(testLNDConfig, &mode, &peers, &disconnect)
	conf := parseLNDUserConf(updated)

	if conf.networkMode() != "hybrid" {
		t.Fatalf("expected hybrid mode, got %q\n%s", conf.networkMode(), updated)
	}
	if conf.GraphSyncPeers != 8 {
		t.Fatalf("expected 8 graph sync peers, got %d", conf.GraphSyncPeers)
	}
	if conf.NoDisconnectOnPongFailure {
		t.Fatal("expected unresponsive peers to be disconnected")
	}
	if strings.Count(updated, "tor.skip-proxy-for-clearnet-targets=") != 1 {
		t.Fatalf("expected one skip-proxy setting\n%s", updated)
	}
	if !strings.Contains(updated, "[Bitcoin]\nbitcoin.mainnet=1") {
		t.Fatalf("unrelated sections were changed\n%s", updated)
	}
	if err := validateLNDNetworkCombination(updated); err != nil {
		t.Fatalf("hybrid preset should be valid: %v", err)
	}
}

func TestUpdateLNDNetworkOptionsAddsMissingSections(t *testing.T) {
	mode := "private"
	peers := 5
	disconnect := true
	updated := updateLNDNetworkOptions("# empty config\n", &mode, &peers, &disconnect)
	conf := parseLNDUserConf(updated)

	if conf.networkMode() != "private" {
		t.Fatalf("expected private mode, got %q\n%s", conf.networkMode(), updated)
	}
	if !strings.Contains(updated, "[Application Options]") || !strings.Contains(updated, "[tor]") {
		t.Fatalf("expected missing sections to be added\n%s", updated)
	}
}

func TestBuildLNDConfigUpdatePreservesExistingWalletUnlockPath(t *testing.T) {
	const customPasswordPath = "/srv/existing-lnd/secrets/wallet-password"
	raw := strings.Replace(
		testLNDConfig,
		"alias=Test Node",
		"alias=Test Node\nwallet-unlock-password-file="+customPasswordPath+"\nwallet-unlock-allow-create=true",
		1,
	)
	mode := "hybrid"
	peers := 1
	disconnect := false

	updated, err := buildLNDConfigUpdate(
		raw, false, "", "", 0, 0, &mode, &peers, &disconnect,
	)
	if err != nil {
		t.Fatalf("build config update: %v", err)
	}

	wantPasswordLine := "wallet-unlock-password-file=" + customPasswordPath
	if strings.Count(updated, wantPasswordLine) != 1 {
		t.Fatalf("custom wallet password path was not preserved\n%s", updated)
	}
	if strings.Contains(updated, "wallet-unlock-password-file="+lndPasswordPath) {
		t.Fatalf("custom wallet password path was replaced\n%s", updated)
	}
	if strings.Count(updated, "wallet-unlock-allow-create=true") != 1 {
		t.Fatalf("wallet unlock create setting was not preserved\n%s", updated)
	}
}

func TestBuildLNDConfigUpdateDoesNotAddWalletUnlock(t *testing.T) {
	mode := "hybrid"
	peers := 1
	disconnect := false

	updated, err := buildLNDConfigUpdate(
		testLNDConfig, false, "", "", 0, 0,
		&mode, &peers, &disconnect,
	)
	if err != nil {
		t.Fatalf("build config update: %v", err)
	}

	if strings.Contains(updated, "wallet-unlock-password-file=") {
		t.Fatalf("network update added wallet password file\n%s", updated)
	}
	if strings.Contains(updated, "wallet-unlock-allow-create=") {
		t.Fatalf("network update added wallet unlock create setting\n%s", updated)
	}
}

func TestValidateLNDNetworkCombinationRejectsIsolationWithProxyBypass(t *testing.T) {
	invalid := strings.Replace(testLNDConfig, "tor.skip-proxy-for-clearnet-targets=false", "tor.skip-proxy-for-clearnet-targets=true", 1)
	if err := validateLNDNetworkCombination(invalid); err == nil {
		t.Fatal("expected invalid Tor combination to be rejected")
	}
}

func TestNormalizeBoostPeerLimit(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		want      int
	}{
		{name: "default", requested: 0, want: boostPeersDefaultLimit},
		{name: "negative uses default", requested: -1, want: boostPeersDefaultLimit},
		{name: "requested value", requested: 2, want: 2},
		{name: "capped", requested: boostPeersMaxLimit + 1, want: boostPeersMaxLimit},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeBoostPeerLimit(test.requested); got != test.want {
				t.Fatalf("normalizeBoostPeerLimit(%d) = %d, want %d", test.requested, got, test.want)
			}
		})
	}

	modeTests := []struct {
		name      string
		requested int
		permanent bool
		want      int
	}{
		{name: "temporary default", requested: 0, permanent: false, want: boostPeersDefaultLimit},
		{name: "temporary requested", requested: 5, permanent: false, want: 5},
		{name: "persistent default is capped", requested: 0, permanent: true, want: boostPeersPersistentLimit},
		{name: "persistent request is capped", requested: 10, permanent: true, want: boostPeersPersistentLimit},
	}
	for _, test := range modeTests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeBoostPeerModeLimit(test.requested, test.permanent); got != test.want {
				t.Fatalf("normalizeBoostPeerModeLimit(%d, %t) = %d, want %d", test.requested, test.permanent, got, test.want)
			}
		})
	}
}
