package server

import (
	"strings"
	"testing"
)

func TestAppUpgradeRefreshesSecuritySensitiveSystemIntegrations(t *testing.T) {
	required := []string{
		"\"$INSTALL_BIN\" -m 0755 \"$src\" /usr/local/sbin/lightningos-terminal",
		"Restart=always",
		"20-lightningos-restart.conf",
		"\"$INSTALL_BIN\" -m 0755 \"$src\" /usr/local/sbin/lightningos-manager-firewall",
		"/usr/local/sbin/lightningos-manager-firewall",
	}
	for _, fragment := range required {
		if !strings.Contains(embeddedAppUpgradeScript, fragment) {
			t.Fatalf("embedded app upgrade script is missing %q", fragment)
		}
	}
}

func TestEmbeddedSystemIntegrationAssetsAreSafe(t *testing.T) {
	if strings.Contains(embeddedTerminalHelper, "source \"$SECRETS_PATH\"") {
		t.Fatal("terminal helper must not source secrets.env")
	}
	for _, fragment := range []string{
		"read_env_value TERMINAL_CREDENTIAL",
		"ufw --force delete allow \"${MANAGER_PORT}/tcp\"",
		"ufw allow from \"$lan_cidr\"",
		"ufw allow in on tailscale0",
		"Restart=always",
		"system-integrations-20260731-v1",
	} {
		combined := embeddedTerminalHelper + embeddedManagerFirewallHelper + embeddedSystemIntegrationsReconciler + embeddedAppUpgradeScript + systemIntegrationsMarkerPath
		if !strings.Contains(combined, fragment) {
			t.Fatalf("embedded system integration assets are missing %q", fragment)
		}
	}
}
