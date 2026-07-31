package server

import (
	"encoding/base64"
	"os"
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
		"[Application Options]", "network=mainnet", "rpclisten=127.0.0.1:11010", "restlisten=127.0.0.1:18081",
		"[lnd]",
		"lnd.host=127.0.0.1:10009", "lnd.macaroonpath=", "lnd.tlspath=",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("config missing %q", expected)
		}
	}
	if strings.Index(config, "[Application Options]") > strings.Index(config, "network=mainnet") ||
		strings.Index(config, "[lnd]") > strings.Index(config, "lnd.host=") {
		t.Fatal("Loop config options must be placed under their upstream INI sections")
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
		"ExecStartPost=" + paths.ClientSyncPath + " --wait",
	} {
		if !strings.Contains(service, expected) {
			t.Fatalf("service missing %q", expected)
		}
	}
	if strings.Contains(service, "SupplementaryGroups=lnd") {
		t.Fatal("Loop service must not require the conventional lnd group on existing-node installs")
	}
	for _, line := range strings.Split(service, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			t.Fatalf("service contains an invalid physical line without a directive assignment: %q", line)
		}
	}
}

func TestParseLoopSystemdStateRejectsRestartLoop(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "healthy", output: "ActiveState=active\nSubState=running\n", want: "running"},
		{name: "restart loop", output: "ActiveState=activating\nSubState=auto-restart\n", want: "stopped"},
		{name: "failed", output: "ActiveState=failed\nSubState=failed\n", want: "stopped"},
		{name: "unknown", output: "ActiveState=maintenance\nSubState=dead\n", want: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := parseLoopSystemdState(test.output); got != test.want {
				t.Fatalf("parseLoopSystemdState() = %q, want %q", got, test.want)
			}
		})
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
	syncScript := loopClientMaterialSyncScript(paths)
	for _, expected := range []string{"test ! -L", "mkdir -p", paths.ClientDir, paths.ClientTLSCert, paths.ClientMacaroon, "chmod 0640"} {
		if !strings.Contains(syncScript, expected) {
			t.Fatalf("client material sync missing %q", expected)
		}
	}
	if !strings.Contains(syncScript, `if [ "${1:-}" = "--wait" ]`) ||
		!strings.Contains(syncScript, "sleep 0.2") || !strings.Contains(syncScript, `while [ "$i" -lt 100 ]`) {
		t.Fatal("client helper must wait for daemon material only when called with --wait")
	}
	for _, forbidden := range []string{"/data/lnd", "systemctl", "postgres", "bitcoin", "rm -", "chown -R", "usermod"} {
		if strings.Contains(syncScript, forbidden) {
			t.Fatalf("client material repair contains out-of-scope operation %q", forbidden)
		}
	}
}

func TestLoopPersistentSwapStateDetection(t *testing.T) {
	dir := t.TempDir()
	paths := loopPaths{
		LoopDBPath:   filepath.Join(dir, "mainnet", "loop_sqlite.db"),
		LegacyLoopDB: filepath.Join(dir, "mainnet", "loop.db"),
	}
	if loopPersistentSwapStateExists(paths) {
		t.Fatal("missing databases must be treated as a never-initialized installation")
	}
	if err := os.MkdirAll(filepath.Dir(paths.LoopDBPath), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LoopDBPath, nil, 0640); err != nil {
		t.Fatal(err)
	}
	if loopPersistentSwapStateExists(paths) {
		t.Fatal("an empty database must not block cleanup of a failed initialization")
	}
	if err := os.WriteFile(paths.LoopDBPath+"-wal", []byte("pending state"), 0640); err != nil {
		t.Fatal(err)
	}
	if !loopPersistentSwapStateExists(paths) {
		t.Fatal("a non-empty SQLite WAL must preserve the pending-swap safety block")
	}
}

func TestCompactLoopFailureLog(t *testing.T) {
	raw := "ignored\n\nfirst useful error\nsecond useful error\núltimo erro\x00\n"
	got := compactLoopFailureLog(raw, 2, 200)
	if strings.Contains(got, "ignored") || strings.Contains(got, "first useful") {
		t.Fatalf("expected only the last two non-empty lines, got %q", got)
	}
	if !strings.Contains(got, "second useful error | último erro") {
		t.Fatalf("unexpected compact log %q", got)
	}
	short := compactLoopFailureLog("abcdef", 5, 3)
	if short != "...def" {
		t.Fatalf("unexpected character limit result %q", short)
	}
}

func TestValidateLoopDaemonMaterial(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "tls.cert")
	if err := os.WriteFile(valid, []byte("certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateLoopDaemonMaterial(valid, "API certificate"); err != nil {
		t.Fatalf("valid material rejected: %v", err)
	}
	empty := filepath.Join(dir, "empty.macaroon")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateLoopDaemonMaterial(empty, "API macaroon"); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("expected explicit empty material error, got %v", err)
	}
	missing := filepath.Join(dir, "missing")
	if err := validateLoopDaemonMaterial(missing, "API certificate"); err == nil || !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("expected explicit missing material error, got %v", err)
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
		"DEBIAN_FRONTEND=noninteractive apt-get install -y acl",
		"setfacl -m u:'lightningos-loop':--x '/var/lib/lightningos'",
		"chown -R lightningos-loop:1001",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("directory setup missing %q:\n%s", expected, script)
		}
	}
	if strings.Contains(script, "losop") {
		t.Fatal("Loop daemon setup must not depend on the terminal operator")
	}
	if strings.Contains(script, "chmod 751 '/var/lib/lightningos'") || strings.Contains(script, "chown lightningos-loop '/var/lib/lightningos'") {
		t.Fatal("Loop setup must not broaden global state-directory permissions or ownership")
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
		loopQuoteRequest: loopQuoteRequest{OutgoingChannelIDs: []string{"1005750773843558400"}},
		MaxMinerFeeSat:   50000,
	}, quote)
	if err != nil {
		t.Fatal(err)
	}
	channels, ok := payload["outgoing_chan_set"].([]string)
	if !ok || len(channels) != 1 || channels[0] != "1005750773843558400" {
		t.Fatalf("channel ID lost precision: %#v", payload["outgoing_chan_set"])
	}
}

func TestDecodeLoopPaymentDestination(t *testing.T) {
	raw := make([]byte, 33)
	raw[0] = 2
	for i := 1; i < len(raw); i++ {
		raw[i] = byte(i)
	}
	destination, err := decodeLoopPaymentDestination(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(destination) != 66 || !strings.HasPrefix(destination, "02") {
		t.Fatalf("unexpected destination %q", destination)
	}
	for _, invalid := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(raw[:32])} {
		if _, err := decodeLoopPaymentDestination(invalid); err == nil {
			t.Fatalf("expected invalid destination %q to fail", invalid)
		}
	}
}

func TestParseLoopOutgoingChannelIDsPreservesUint64(t *testing.T) {
	channels, err := parseLoopOutgoingChannelIDs([]string{"1005750773843558400", "42"})
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 || channels[0] != 1005750773843558400 || channels[1] != 42 {
		t.Fatalf("unexpected channels: %#v", channels)
	}
	for _, invalid := range [][]string{{"0"}, {"-1"}, {"channel"}} {
		if _, err := parseLoopOutgoingChannelIDs(invalid); err == nil {
			t.Fatalf("expected invalid channels %#v to fail", invalid)
		}
	}
}

func TestHistoricalLoopRoutingEstimateUsesExactChannelSet(t *testing.T) {
	swaps := []loopSwapStatus{
		{Type: "LOOP_OUT", State: "SUCCESS", AmountSat: 250000, CostOffchainSat: 661, OutgoingChannelIDs: []string{"42", "7"}},
		{Type: "LOOP_OUT", State: "SUCCESS", AmountSat: 500000, CostOffchainSat: 1400, OutgoingChannelIDs: []string{"7", "42"}},
		{Type: "LOOP_OUT", State: "SUCCESS", AmountSat: 250000, CostOffchainSat: 1, OutgoingChannelIDs: []string{"99"}},
		{Type: "LOOP_OUT", State: "FAILED", AmountSat: 250000, CostOffchainSat: 2, OutgoingChannelIDs: []string{"7", "42"}},
	}
	estimate, samples, ok := historicalLoopRoutingEstimate(swaps, 250000, []uint64{42, 7})
	if !ok || samples != 2 || estimate != 681 {
		t.Fatalf("unexpected historical estimate: fee=%d samples=%d available=%v", estimate, samples, ok)
	}
	if _, _, ok := historicalLoopRoutingEstimate(swaps, 250000, []uint64{100}); ok {
		t.Fatal("estimate must not borrow history from a different channel set")
	}
}

func TestLoopTimestampSeconds(t *testing.T) {
	if got := loopTimestampSeconds(1_784_676_948_792_349_541); got != 1_784_676_948 {
		t.Fatalf("nanosecond timestamp converted to %d", got)
	}
	if got := loopTimestampSeconds(1_784_676_948); got != 1_784_676_948 {
		t.Fatalf("second timestamp converted to %d", got)
	}
}
