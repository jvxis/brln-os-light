package server

import (
	"path/filepath"
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
	paths := loopAppPaths()
	service := loopServiceContents(paths)
	for _, expected := range []string{
		"User=lightningos-loop", "Group=lightningos-loop", "NoNewPrivileges=true",
		"PrivateDevices=true", "ProtectSystem=full", "ProtectHome=true", "ReadWritePaths=", "UMask=0027",
		"ExecStartPost=/bin/sh -c", paths.ClientTLSCert, paths.ClientMacaroon,
	} {
		if !strings.Contains(service, expected) {
			t.Fatalf("service missing %q", expected)
		}
	}
	if strings.Contains(service, "SupplementaryGroups=lnd") {
		t.Fatal("Loop service must not require the conventional lnd group on existing-node installs")
	}
}

func TestLoopAPIUsesManagerReadableClientMaterial(t *testing.T) {
	paths := loopAppPaths()
	if paths.ClientTLSCert == paths.LoopTLSCert || paths.ClientMacaroon == paths.LoopMacaroon {
		t.Fatal("manager client material must be separate from daemon-owned credentials")
	}
	if !strings.HasPrefix(paths.ClientTLSCert, paths.Root+string(filepath.Separator)) ||
		!strings.HasPrefix(paths.ClientMacaroon, paths.Root+string(filepath.Separator)) {
		t.Fatal("manager client material must stay inside the Loop app directory")
	}
	syncScript := loopClientMaterialSyncScript(paths, false)
	for _, expected := range []string{"mkdir -p", paths.ClientDir, "chmod 2750", paths.ClientTLSCert, paths.ClientMacaroon, "chmod 0640"} {
		if !strings.Contains(syncScript, expected) {
			t.Fatalf("client material sync missing %q", expected)
		}
	}
	if strings.Contains(syncScript, "sleep 0.2") {
		t.Fatal("on-demand client material repair must not wait when daemon material is absent")
	}
	waitScript := loopClientMaterialSyncScript(paths, true)
	if !strings.Contains(waitScript, "sleep 0.2") || !strings.Contains(waitScript, `while [ "$i" -lt 100 ]`) {
		t.Fatal("service startup must wait briefly for daemon client material")
	}
}

func TestLoopDirectorySetupProvisionsDedicatedServiceAccount(t *testing.T) {
	paths := loopAppPaths()
	script := loopDirectorySetupScript(paths, "1001")
	for _, expected := range []string{
		"getent group 'lightningos-loop'",
		"groupadd --system 'lightningos-loop'",
		"id -u 'lightningos-loop'",
		"useradd --system --gid 'lightningos-loop'",
		"--no-create-home --shell /usr/sbin/nologin 'lightningos-loop'",
		"chown -R lightningos-loop:1001",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("directory setup missing %q:\n%s", expected, script)
		}
	}
	if strings.Contains(script, "losop") {
		t.Fatal("Loop daemon setup must not depend on the terminal operator")
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
