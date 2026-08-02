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

func TestValidateLNDNetworkCombinationRejectsIsolationWithProxyBypass(t *testing.T) {
	invalid := strings.Replace(testLNDConfig, "tor.skip-proxy-for-clearnet-targets=false", "tor.skip-proxy-for-clearnet-targets=true", 1)
	if err := validateLNDNetworkCombination(invalid); err == nil {
		t.Fatal("expected invalid Tor combination to be rejected")
	}
}
