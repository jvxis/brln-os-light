package server

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"

	"lightningos-light/internal/system"
)

const managerFirewallConfigPath = "/etc/lightningos/manager-firewall.conf"

type managerFirewallStatus struct {
	Installed          bool
	Active             bool
	ConfiguredCIDR     string
	ConfigValid        bool
	LANRulePresent     bool
	TailscaleRule      bool
	BroadRulePresent   bool
	ManagerAccessBound bool
	StatusAvailable    bool
}

func inspectManagerFirewall(ctx context.Context) managerFirewallStatus {
	status := managerFirewallStatus{
		ConfiguredCIDR: readManagerFirewallCIDR(managerFirewallConfigPath),
		ConfigValid:    true,
	}
	if status.ConfiguredCIDR == "" {
		status.ConfigValid = false
	} else if !strings.EqualFold(status.ConfiguredCIDR, "none") {
		if _, _, err := net.ParseCIDR(status.ConfiguredCIDR); err != nil {
			status.ConfigValid = false
		}
	}
	if _, err := exec.LookPath("ufw"); err != nil {
		return status
	}
	status.Installed = true
	out, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil {
		return status
	}
	status.StatusAvailable = true
	return parseManagerFirewallStatus(out, status)
}

func readManagerFirewallCIDR(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == "LAN_CIDR" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseManagerFirewallStatus(output string, status managerFirewallStatus) managerFirewallStatus {
	configuredLower := strings.ToLower(strings.TrimSpace(status.ConfiguredCIDR))
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.ToLower(strings.TrimSpace(rawLine))
		if strings.HasPrefix(line, "status:") {
			status.Active = strings.Contains(line, "active") && !strings.Contains(line, "inactive")
		}
		if !strings.Contains(line, "8443") || !strings.Contains(line, "allow") {
			continue
		}
		if configuredLower != "" && configuredLower != "none" && strings.Contains(line, configuredLower) {
			status.LANRulePresent = true
		}
		if strings.Contains(line, "tailscale0") {
			status.TailscaleRule = true
		}
		if strings.Contains(line, "anywhere") && !strings.Contains(line, "tailscale0") {
			status.BroadRulePresent = true
		}
	}
	if status.Active && status.ConfigValid && !status.BroadRulePresent {
		if configuredLower == "none" {
			status.ManagerAccessBound = true
		} else {
			status.ManagerAccessBound = status.LANRulePresent
		}
	}
	return status
}
