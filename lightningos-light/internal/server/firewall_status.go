package server

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"

	"lightningos-light/internal/system"
)

const managerFirewallConfigPath = "/etc/lightningos/manager-firewall.conf"

type managerFirewallStatus struct {
	Installed          bool   `json:"installed"`
	Active             bool   `json:"active"`
	ConfiguredCIDR     string `json:"configured_cidr"`
	ConfigValid        bool   `json:"config_valid"`
	LANRulePresent     bool   `json:"lan_rule_present"`
	TailscaleRule      bool   `json:"tailscale_rule"`
	BroadRulePresent   bool   `json:"broad_rule_present"`
	ManagerAccessBound bool   `json:"manager_access_bound"`
	StatusAvailable    bool   `json:"status_available"`
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
	raw, handled, err := system.ReadManagerFirewallStatusWithBroker(ctx)
	if !handled || err != nil {
		return status
	}
	var brokerStatus managerFirewallStatus
	if err := json.Unmarshal([]byte(raw), &brokerStatus); err != nil {
		return status
	}
	return brokerStatus
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
