package privileged

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

const testNetworkID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"

func bridgeRunner(subnet string) *composeRecordingRunner {
	return &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) == 5 && args[0] == "network" && args[4] == "{{.Id}}" {
			return testNetworkID, nil, true
		}
		if path == dockerPath && len(args) == 5 && args[0] == "network" && strings.Contains(args[4], ".IPAM.Config") {
			return subnet, nil, true
		}
		return "", nil, false
	}}
}

func ufwMutations(runner *composeRecordingRunner) [][]string {
	var mutations [][]string
	for _, command := range runner.commands {
		if command.path == ufwPath {
			mutations = append(mutations, command.args)
		}
	}
	return mutations
}

func withBridges(t *testing.T, live ...string) {
	t.Helper()
	previous := bridgeExists
	bridgeExists = func(name string) bool {
		for _, bridge := range live {
			if bridge == name {
				return true
			}
		}
		return false
	}
	t.Cleanup(func() { bridgeExists = previous })
}

// Issue #264: the app's own broad rule (and its IPv6 twin) goes, and the restricted one takes
// its place; a broad rule on another app's live bridge is that app's to replace.
func TestManagerBridgeRuleReplacesTheBroadOneAndLeavesOtherLiveApps(t *testing.T) {
	withBridges(t, "br-0123456789ab", "br-aaaaaaaaaaaa")
	status := strings.Join([]string{
		"Status: active",
		"",
		"To                         Action      From",
		"--                         ------      ----",
		"8443/tcp on br-0123456789ab ALLOW       Anywhere",
		"8443/tcp on br-aaaaaaaaaaaa ALLOW       Anywhere",
		"8443/tcp on br-bbbbbbbbbbbb ALLOW       Anywhere",
		"8443 on tailscale0         ALLOW       Anywhere",
		"8443/tcp (v6) on br-0123456789ab ALLOW       Anywhere (v6)",
	}, "\n")
	runner := bridgeRunner("172.20.0.0/16 fd00::/64\n")
	if err := allowManagerFromAppBridge(context.Background(), runner, "brln-community", status); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"--force", "delete", "allow", "in", "on", "br-0123456789ab", "to", "any", "port", "8443", "proto", "tcp"},
		// br-bbbbbbbbbbbb no longer exists: a leftover of a recreated network.
		{"--force", "delete", "allow", "in", "on", "br-bbbbbbbbbbbb", "to", "any", "port", "8443", "proto", "tcp"},
		{"allow", "in", "on", "br-0123456789ab", "from", "172.20.0.0/16", "to", "any", "port", "8443", "proto", "tcp"},
	}
	if got := ufwMutations(runner); !reflect.DeepEqual(got, want) {
		t.Fatalf("ufw commands:\n%#v\nwant\n%#v", got, want)
	}
}

func TestManagerBridgeRuleIsIdempotent(t *testing.T) {
	withBridges(t, "br-0123456789ab")
	status := "Status: active\n8443/tcp on br-0123456789ab ALLOW       172.20.0.0/16\n"
	runner := bridgeRunner("172.20.0.0/16\n")
	if err := allowManagerFromAppBridge(context.Background(), runner, "bark-wallet", status); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"allow", "in", "on", "br-0123456789ab", "from", "172.20.0.0/16", "to", "any", "port", "8443", "proto", "tcp"}}
	if got := ufwMutations(runner); !reflect.DeepEqual(got, want) {
		t.Fatalf("a second start deletes nothing and only re-asserts the rule: %#v", got)
	}
}

// The subnet comes from Docker and is never assumed, so whatever comes back is checked before
// it reaches ufw.
func TestManagerBridgeRuleRejectsAnUnusableSubnet(t *testing.T) {
	withBridges(t, "br-0123456789ab")
	for _, subnet := range []string{"", "fd00::/64", "8.8.8.0/24", "0.0.0.0/0", "172.20.0.1/16", "172.20.0.0/16;reboot", "not a subnet"} {
		runner := bridgeRunner(subnet)
		status := "Status: active\n8443/tcp on br-0123456789ab ALLOW       Anywhere\n"
		err := allowManagerFromAppBridge(context.Background(), runner, "brln-community", status)
		if err == nil || !strings.Contains(err.Error(), "subnet") {
			t.Fatalf("%q accepted: %v", subnet, err)
		}
		if got := ufwMutations(runner); len(got) != 0 {
			t.Fatalf("%q: firewall changed before the subnet was known: %#v", subnet, got)
		}
	}
}

// The exposure check is what flagged the old rule; the new one must not trip it, while a real
// broad rule still does.
func TestRestrictedBridgeRuleIsNotBroadExposure(t *testing.T) {
	restricted := parseManagerFirewallOutput("Status: active\n8443/tcp on br-0123456789ab ALLOW       172.20.0.0/16\n8443 on tailscale0 ALLOW Anywhere\n", ManagerFirewallState{ConfiguredCIDR: "none", ConfigValid: true})
	if restricted.BroadRulePresent || !restricted.ManagerAccessBound {
		t.Fatalf("restricted bridge rule flagged: %#v", restricted)
	}
	broad := parseManagerFirewallOutput("Status: active\n8443/tcp on br-0123456789ab ALLOW       Anywhere\n", ManagerFirewallState{ConfiguredCIDR: "none", ConfigValid: true})
	if !broad.BroadRulePresent {
		t.Fatal("the old broad rule is no longer detected")
	}
}

func TestBroadRuleParsingIgnoresWhatIsNotABridge(t *testing.T) {
	status := "8443/tcp on br-0123456789ab;reboot ALLOW Anywhere\n8443/tcp on eth0 ALLOW Anywhere\n8443/tcp ALLOW Anywhere\n4448/tcp on br-0123456789ab ALLOW Anywhere\n"
	if got := broadManagerBridgeRules(status); len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}
