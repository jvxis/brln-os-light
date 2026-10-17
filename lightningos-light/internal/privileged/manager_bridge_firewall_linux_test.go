//go:build linux

package privileged

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type bridgeFailureRunner struct {
	CommandRunner
	fail string
}

func (r bridgeFailureRunner) Run(ctx context.Context, path string, args ...string) (string, error) {
	if path == ufwPath && len(args) > 2 && args[0] == r.fail {
		return "", errors.New("injected firewall command failure")
	}
	return r.CommandRunner.Run(ctx, path, args...)
}

// Opt-in destructive integration gate: only on a disposable Linux host with no
// node services, no containers, an inactive/empty UFW, and no existing app networks.
// Uses real Docker/UFW and a dummy HTTP listener, NOT wallets or a real Manager.
func TestManagerBridgeFirewallLinuxGate(t *testing.T) {
	if os.Getenv("LIGHTNINGOS_BRIDGE_FIREWALL_GATE") != "1" {
		t.Skip("set LIGHTNINGOS_BRIDGE_FIREWALL_GATE=1 only on a disposable Linux host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root required on disposable host")
	}
	t.Setenv("LC_ALL", "C")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r := &ExecCommandRunner{}
	run := func(path string, args ...string) string {
		t.Helper()
		out, err := r.Run(ctx, path, args...)
		if err != nil {
			t.Fatalf("%s %v: %v: %s", path, args, err, out)
		}
		return out
	}
	for _, unit := range []string{"lnd", "lightningos-manager", "bitcoind", "bitcoin"} {
		if _, err := r.Run(ctx, "/usr/bin/systemctl", "is-active", "--quiet", unit); err == nil {
			t.Fatalf("refusing active node service %s", unit)
		}
	}
	if strings.TrimSpace(run(dockerPath, "ps", "-q")) != "" {
		t.Fatal("refusing a host with running containers")
	}
	if !strings.Contains(run(ufwPath, "status"), "Status: inactive") || !strings.Contains(run(ufwPath, "show", "added"), "(None)") {
		t.Fatal("requires inactive UFW without pre-existing user rules")
	}
	if _, err := r.Run(ctx, "/usr/sbin/ip", "link", "show", "tailscale0"); err == nil {
		t.Fatal("refusing an existing Tailscale interface")
	}
	helper := os.Getenv("LIGHTNINGOS_MANAGER_FIREWALL_TEST_HELPER")
	if info, err := os.Stat(helper); err != nil || !info.Mode().IsRegular() {
		t.Fatal("provide the checkout's lightningos-manager-firewall.sh through LIGHTNINGOS_MANAGER_FIREWALL_TEST_HELPER")
	}
	image := "busybox@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"
	run(dockerPath, "image", "inspect", image)
	projects := []string{"brln-community", "bark-wallet", "los-firewall-unrelated"}
	for _, project := range projects {
		if strings.TrimSpace(run(dockerPath, "network", "ls", "--filter", "name=^"+project+"_default$", "--format", "{{.Name}}")) != "" {
			t.Fatalf("refusing existing network for %s", project)
		}
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:8443")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("bridge-ok")) }), ReadHeaderTimeout: 3 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	// Every mutation is confined to this fresh disposable host. Restore the inactive
	// state first, then delete only rules created by this gate. No UFW reset is used.
	var rules [][]string
	var networks []string
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if out, err := r.Run(cleanupCtx, ufwPath, "--force", "disable"); err != nil {
			t.Errorf("disable test UFW: %v: %s", err, out)
		}
		for _, rule := range rules {
			if out, err := r.Run(cleanupCtx, ufwPath, append([]string{"--force", "delete"}, rule...)...); err != nil {
				t.Errorf("remove test rule %v: %v: %s", rule, err, out)
			}
		}
		for _, network := range networks {
			if out, err := r.Run(cleanupCtx, dockerPath, "network", "rm", network); err != nil {
				t.Errorf("remove test network %s: %v: %s", network, err, out)
			}
		}
		_, _ = r.Run(cleanupCtx, "/usr/sbin/ip", "link", "delete", "tailscale0")
	})
	add := func(args ...string) { rules = append(rules, args); run(ufwPath, args...) }
	add("allow", "22/tcp") // Preserve SSH throughout the test.
	run(ufwPath, "--force", "enable")
	bridges := make(map[string]string)
	subnets := make(map[string]string)
	for _, project := range projects {
		run(dockerPath, "network", "create", project+"_default")
		networks = append(networks, project+"_default")
		bridges[project], err = appBridge(ctx, r, project)
		if err != nil {
			t.Fatal(err)
		}
		subnets[project], err = appSubnet(ctx, r, project)
		if err != nil {
			t.Fatal(err)
		}
		rules = append(rules, []string{"allow", "in", "on", bridges[project], "from", subnets[project], "to", "any", "port", "8443", "proto", "tcp"})
	}
	probe := func(project string, wantOK bool) {
		t.Helper()
		gateway := strings.TrimSpace(run(dockerPath, "network", "inspect", project+"_default", "--format", "{{(index .IPAM.Config 0).Gateway}}"))
		out, err := r.Run(ctx, dockerPath, "run", "--rm", "--network", project+"_default", image, "wget", "-q", "-T", "3", "-O", "-", "http://"+gateway+":8443/")
		if wantOK && (err != nil || out != "bridge-ok") {
			t.Fatalf("%s lost Manager-port access: %v: %s", project, err, out)
		}
		if !wantOK && err == nil {
			t.Fatalf("%s unexpectedly reaches Manager port", project)
		}
	}
	probe("brln-community", false)
	for _, project := range []string{"brln-community", "bark-wallet"} {
		add("allow", "in", "on", bridges[project], "to", "any", "port", "8443", "proto", "tcp")
		probe(project, true)
	}
	// Unrelated Bitcoin/LND/app rules and an orphaned Manager rule must survive.
	for _, port := range []string{"8332", "8333", "28332", "28333", "10009", "5000", "8999", "1984"} {
		add("allow", "in", "on", bridges["los-firewall-unrelated"], "to", "any", "port", port, "proto", "tcp")
	}
	add("allow", "in", "on", "br-fffffffffffe", "to", "any", "port", "8443", "proto", "tcp")
	baselineOther := run(ufwPath, "show", "added")
	status := func() string { return run(ufwPath, "status") }
	if err := allowManagerFromAppBridge(ctx, bridgeFailureRunner{r, "allow"}, "brln-community", status()); err == nil {
		t.Fatal("expected injected allow failure")
	}
	probe("brln-community", true)
	if err := allowManagerFromAppBridge(ctx, bridgeFailureRunner{r, "--force"}, "brln-community", status()); err == nil {
		t.Fatal("expected injected delete failure")
	}
	probe("brln-community", true)
	// Retry through the real native app entry points, including their own public ports.
	rules = append(rules, []string{"allow", "4448/tcp"}, []string{"allow", "4004/tcp"})
	for i := 0; i < 2; i++ {
		if _, err := (&NativeBRLNCommunityManager{Runner: r}).EnsureFirewall(ctx, false); err != nil {
			t.Fatal(err)
		}
		if _, err := (&NativeBarkWalletManager{Runner: r}).EnsureFirewall(ctx, false); err != nil {
			t.Fatal(err)
		}
		probe("brln-community", true)
		probe("bark-wallet", true)
		probe("los-firewall-unrelated", false)
	}
	after := run(ufwPath, "show", "added")
	for _, line := range strings.Split(baselineOther, "\n") {
		if strings.Contains(line, bridges["los-firewall-unrelated"]) || strings.Contains(line, "br-fffffffffffe") || strings.Contains(line, "22/tcp") {
			if !strings.Contains(after, line) {
				t.Fatalf("unrelated rule removed: %s", line)
			}
		}
	}
	for _, project := range []string{"brln-community", "bark-wallet"} {
		for _, old := range broadManagerBridgeRules(status()) {
			if old == bridges[project] {
				t.Fatalf("broad/v6 app rule remains: %s", old)
			}
		}
	}
	// An orphan is deliberately NOT hidden from exposure validation. Remove only
	// the orphan explicitly created by this fixture before testing the VPN policy.
	run(ufwPath, "--force", "delete", "allow", "in", "on", "br-fffffffffffe", "to", "any", "port", "8443", "proto", "tcp")
	run("/usr/sbin/ip", "link", "add", "tailscale0", "type", "dummy")
	rules = append(rules, []string{"allow", "in", "on", "tailscale0", "to", "any", "port", "8443", "proto", "tcp"})
	t.Setenv("LIGHTNINGOS_FIREWALL_CONFIG", filepath.Join(t.TempDir(), "manager-firewall.conf"))
	for i := 0; i < 2; i++ {
		run("/usr/bin/bash", helper, "--mode", "vpn")
		state := parseManagerFirewallOutput(status(), ManagerFirewallState{ConfiguredCIDR: "none", ConfigValid: true, AccessMode: "vpn"})
		if !state.ManagerAccessBound || state.BroadRulePresent {
			t.Fatalf("VPN policy rejected restricted app rules: %+v", state)
		}
		probe("brln-community", true)
		probe("bark-wallet", true)
	}
	// Recreate only this fixture's empty network, as a reinstall would. No old
	// bridge rule is reclassified as ours merely because its interface disappeared.
	oldBridge := bridges["brln-community"]
	run(dockerPath, "network", "rm", "brln-community_default")
	run(dockerPath, "network", "create", "brln-community_default")
	newBridge, err := appBridge(ctx, r, "brln-community")
	if err != nil || newBridge == oldBridge {
		t.Fatalf("network was not recreated: %s %v", newBridge, err)
	}
	newSubnet, err := appSubnet(ctx, r, "brln-community")
	if err != nil {
		t.Fatal(err)
	}
	rules = append(rules, []string{"allow", "in", "on", newBridge, "from", newSubnet, "to", "any", "port", "8443", "proto", "tcp"})
	if _, err := (&NativeBRLNCommunityManager{Runner: r}).EnsureFirewall(ctx, false); err != nil {
		t.Fatal(err)
	}
	probe("brln-community", true)
	probe("bark-wallet", true)
	probe("los-firewall-unrelated", false)
	// The LAN policy must also preserve both restricted app rules, then permit
	// returning to VPN-only without relying on an actual Tailscale daemon.
	rules = append(rules, []string{"allow", "from", "192.168.250.0/24", "to", "any", "port", "8443", "proto", "tcp"},
		[]string{"allow", "from", "192.168.250.0/24", "to", "224.0.0.251", "port", "5353", "proto", "udp"})
	run("/usr/bin/bash", helper, "--mode", "lan", "--lan-cidr", "192.168.250.0/24")
	probe("brln-community", true)
	probe("bark-wallet", true)
	run("/usr/bin/bash", helper, "--mode", "vpn")
	t.Log("real UFW/Docker: blocked-before-allow, two-app migration, failure/retry, unrelated rules, IPv6 cleanup, repeated VPN policy and host connectivity passed")
}
