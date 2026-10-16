package privileged

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
)

// An app whose proxy asks the Manager to authorize a request (BR⚡LN Community, Bark Wallet)
// needs its containers to reach port 8443 through the app's Docker bridge.
//
// Until 0.5.46 that permission was `ufw allow in on br-<id> to any port 8443`, which UFW lists
// as "ALLOW IN Anywhere". The Manager exposure check (manager_firewall.go and the
// lightningos-manager-firewall.sh helper) counts any 8443 rule from Anywhere outside tailscale0
// as broad, so the app's own rule made System Check report the Manager as exposed and made
// "Tailscale only" fail with firewall_configure_failed. Restricting the rule by hand did not
// last: the next app start added the broad rule again (issue #264).
//
// The rule is now limited to the subnet Docker gave the app's network, read from Docker and
// never assumed, and the old broad rule is removed: the app's own, and any left on a bridge
// that no longer exists (a network recreated by a reinstall gets a new bridge). A broad rule
// on another live bridge belongs to another app, and is that app's to replace when it starts.

// bridgeExists is replaced in tests.
var bridgeExists = func(name string) bool {
	_, err := os.Stat("/sys/class/net/" + name)
	return err == nil
}

// appBridge returns the bridge interface of a compose project's default network.
func appBridge(ctx context.Context, runner CommandRunner, project string) (string, error) {
	networkID, err := runner.Run(ctx, dockerPath, "network", "inspect", project+"_default", "--format", "{{.Id}}")
	if err != nil {
		return "", errors.New("network lookup failed")
	}
	id := strings.TrimSpace(networkID)
	if len(id) < 12 || len(id) > 64 {
		return "", errors.New("network ID is invalid")
	}
	for _, char := range id {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return "", errors.New("network ID is invalid")
		}
	}
	return "br-" + id[:12], nil
}

// appSubnet returns the private IPv4 subnet of a compose project's default network.
func appSubnet(ctx context.Context, runner CommandRunner, project string) (string, error) {
	output, err := runner.Run(ctx, dockerPath, "network", "inspect", project+"_default", "--format", "{{range .IPAM.Config}}{{.Subnet}} {{end}}")
	if err != nil {
		return "", errors.New("network subnet lookup failed")
	}
	for _, field := range strings.Fields(output) {
		ip, subnet, err := net.ParseCIDR(field)
		if err != nil || ip.To4() == nil || !ip.IsPrivate() || !subnet.IP.Equal(ip) {
			continue
		}
		return subnet.String(), nil
	}
	return "", errors.New("network subnet is invalid")
}

// broadManagerBridgeRules lists the bridges that hold an 8443 rule from Anywhere, as `ufw
// status` prints it: "8443/tcp on br-0123456789ab   ALLOW   Anywhere" (and the "(v6)" twin).
func broadManagerBridgeRules(ufwStatus string) []string {
	var bridges []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(ufwStatus, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if !strings.HasPrefix(line, "8443/tcp") || !strings.Contains(line, "allow") || !strings.Contains(line, "anywhere") {
			continue
		}
		fields := strings.Fields(line)
		for index, field := range fields {
			if field != "on" || index+1 >= len(fields) {
				continue
			}
			bridge := fields[index+1]
			if !validBridgeName(bridge) || seen[bridge] {
				continue
			}
			seen[bridge] = true
			bridges = append(bridges, bridge)
		}
	}
	return bridges
}

func validBridgeName(name string) bool {
	if !strings.HasPrefix(name, "br-") || len(name) != 15 {
		return false
	}
	for _, char := range name[3:] {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

// allowManagerFromAppBridge lets the app's containers, and only them, reach the Manager.
// ufwStatus is the `ufw status` output the caller already read to know UFW is active.
func allowManagerFromAppBridge(ctx context.Context, runner CommandRunner, project, ufwStatus string) error {
	bridge, err := appBridge(ctx, runner, project)
	if err != nil {
		return err
	}
	subnet, err := appSubnet(ctx, runner, project)
	if err != nil {
		return err
	}
	for _, old := range broadManagerBridgeRules(ufwStatus) {
		if old != bridge && bridgeExists(old) {
			continue
		}
		// Without a source address the rule was added for IPv4 and IPv6, and deleting it by
		// the same specification removes both.
		if _, err := runner.Run(ctx, ufwPath, "--force", "delete", "allow", "in", "on", old, "to", "any", "port", "8443", "proto", "tcp"); err != nil {
			return errors.New("broad manager authorization rule removal failed")
		}
	}
	if _, err := runner.Run(ctx, ufwPath, "allow", "in", "on", bridge, "from", subnet, "to", "any", "port", "8443", "proto", "tcp"); err != nil {
		return errors.New("manager authorization firewall preparation failed")
	}
	return nil
}
