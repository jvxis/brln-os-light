package server

import (
	"strings"
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestLoopAssetForArch(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "arm"} {
		asset, err := loopAssetForArch(arch)
		if err != nil {
			t.Fatalf("asset for %s: %v", arch, err)
		}
		if asset.Archive == "" || len(asset.SHA256) != 64 || !strings.Contains(asset.Archive, loopVersion) {
			t.Fatalf("invalid asset for %s: %+v", arch, asset)
		}
	}
	if _, err := loopAssetForArch("386"); err == nil {
		t.Fatal("expected unsupported architecture error")
	}
}

func TestNormalizeLoopManagerGroupID(t *testing.T) {
	groupID, err := normalizeLoopManagerGroupID(" 1001 ")
	if err != nil || groupID != "1001" {
		t.Fatalf("unexpected normalized group ID %q: %v", groupID, err)
	}
	for _, value := range []string{"", "lightningos", "1001;touch /tmp/pwned", "-1"} {
		if _, err := normalizeLoopManagerGroupID(value); err == nil {
			t.Fatalf("expected invalid group ID %q to fail", value)
		}
	}
}

func TestLoopConfigIsMainnetAndLoopbackOnly(t *testing.T) {
	config := loopConfigContents(loopAppPaths())
	for _, expected := range []string{
		"network=mainnet", "rpclisten=127.0.0.1:11010", "restlisten=127.0.0.1:18081",
		"lnd.host=127.0.0.1:10009", "lnd.macaroonpath=", "lnd.tlspath=",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config missing %q", expected)
		}
	}
	for _, forbidden := range []string{"0.0.0.0", "admin.macaroon", "autoloop"} {
		if strings.Contains(strings.ToLower(config), forbidden) {
			t.Fatalf("config contains unsafe value %q", forbidden)
		}
	}
}

func TestLoopServiceHardening(t *testing.T) {
	service := loopServiceContents(loopAppPaths())
	for _, expected := range []string{
		"User=losop", "SupplementaryGroups=lnd", "NoNewPrivileges=true",
		"PrivateDevices=true", "ProtectSystem=full", "ProtectHome=true", "ReadWritePaths=",
	} {
		if !strings.Contains(service, expected) {
			t.Fatalf("service missing %q", expected)
		}
	}
}

func TestLoopMacaroonIsDedicatedWithoutMacaroonAdmin(t *testing.T) {
	permissions := loopMacaroonPermissions()
	if len(permissions) == 0 {
		t.Fatal("expected Loop macaroon permissions")
	}
	for _, permission := range permissions {
		key := lndclient.MacaroonPermissionKey(permission)
		if strings.HasPrefix(key, "macaroon:") {
			t.Fatalf("Loop must not receive macaroon administration permission: %s", key)
		}
	}
}

func TestLoopPendingStates(t *testing.T) {
	for _, state := range []string{"INITIATED", "PREIMAGE_REVEALED", "HTLC_PUBLISHED", "INVOICE_SETTLED", ""} {
		if !isPendingLoopState(state) {
			t.Fatalf("expected %q to be pending", state)
		}
	}
	for _, state := range []string{"SUCCESS", "FAILED", " success "} {
		if isPendingLoopState(state) {
			t.Fatalf("expected %q to be final", state)
		}
	}
}

func TestLoopSwapPayloadPreservesChannelID(t *testing.T) {
	quote := loopQuoteResponse{
		Direction: "out", AmountSat: 100000, ConfTarget: 9, SwapFeeSat: 100,
		OnchainFeeSat: 200, PrepayAmountSat: 50, RoutingFeeLimitSat: 260,
		PrepayRoutingLimitSat: 11, ExpiresAt: "2030-01-01T00:00:00Z",
	}
	payload, err := loopSwapPayload(loopSwapRequest{
		OutgoingChannelIDs: []string{"1005750773843558400"}, MaxMinerFeeSat: 50000,
	}, quote)
	if err != nil {
		t.Fatal(err)
	}
	channels, ok := payload["outgoing_chan_set"].([]string)
	if !ok || len(channels) != 1 || channels[0] != "1005750773843558400" {
		t.Fatalf("channel ID lost precision: %#v", payload["outgoing_chan_set"])
	}
}
