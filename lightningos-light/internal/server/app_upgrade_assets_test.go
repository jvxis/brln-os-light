package server

import (
	"strings"
	"testing"
)

func TestAppUpgradeRefreshesSecuritySensitiveSystemIntegrations(t *testing.T) {
	required := []string{
		"\"$INSTALL_BIN\" -m 0755 \"$src\" /usr/local/sbin/lightningos-terminal",
		"/usr/local/sbin/lightningos-terminal-password",
		"Restart=always",
		"20-lightningos-restart.conf",
		"\"$INSTALL_BIN\" -m 0755 \"$src\" /usr/local/sbin/lightningos-manager-firewall",
		"/usr/local/sbin/lightningos-manager-firewall",
		"/usr/local/sbin/lightningos-setup-manager-tls-mdns",
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
	if strings.Contains(embeddedTerminalHelper, "read_env_value") || strings.Contains(embeddedTerminalHelper, "/etc/lightningos/secrets.env") {
		t.Fatal("terminal helper must consume the systemd EnvironmentFile values instead of parsing secrets.env")
	}
	for _, fragment := range []string{
		"terminal_credential=\"${TERMINAL_CREDENTIAL:-}\"",
		"/usr/sbin/chpasswd",
		"ufw --force delete allow \"${MANAGER_PORT}/tcp\"",
		"ufw allow from \"$lan_cidr\"",
		"ufw allow in on tailscale0",
		"Ignoring invalid saved local network",
		"so stdout contains only the selected CIDR",
		"Restart=always",
		"setup-manager-tls-mdns.sh",
		"system-integrations-20260811-v4",
		"upgrade_bitcoin_storage",
		"app.bitcoincore.storage.ensure",
	} {
		combined := embeddedTerminalHelper + embeddedTerminalPasswordHelper + embeddedManagerFirewallHelper + embeddedSystemIntegrationsReconciler + embeddedAppUpgradeScript + systemIntegrationsMarkerPath
		if !strings.Contains(combined, fragment) {
			t.Fatalf("embedded system integration assets are missing %q", fragment)
		}
	}
}

func TestSystemIntegrationReconcilerEnrollsLegacyBitcoinWithoutRestart(t *testing.T) {
	for _, required := range []string{
		"reconcile_bitcoin_storage_enrollment",
		"app.bitcoincore.storage.ensure",
		"Existing Bitcoin Core storage enrolled without restart",
	} {
		if !strings.Contains(embeddedSystemIntegrationsReconciler, required) {
			t.Fatalf("system integration reconciler is missing %q", required)
		}
	}
	enrollment := strings.Index(embeddedSystemIntegrationsReconciler, "reconcile_bitcoin_storage_enrollment\n")
	marker := strings.Index(embeddedSystemIntegrationsReconciler, "touch \"$marker_path\"")
	if enrollment < 0 || marker < 0 || enrollment > marker {
		t.Fatal("Bitcoin storage enrollment must complete before the reconciliation marker")
	}
	for _, forbidden := range []string{"restart bitcoind", "restart bitcoin", "docker restart"} {
		if strings.Contains(strings.ToLower(embeddedSystemIntegrationsReconciler), forbidden) {
			t.Fatalf("legacy Bitcoin enrollment contains forbidden restart action %q", forbidden)
		}
	}
}
