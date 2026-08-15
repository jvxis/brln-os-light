package server

import (
	"context"
	"strings"
	"testing"

	"lightningos-light/internal/privileged"
)

func TestAppUpgradeRefreshesSecuritySensitiveSystemIntegrations(t *testing.T) {
	required := []string{
		"\"$INSTALL_BIN\" -m 0755 \"$src\" /usr/local/sbin/lightningos-terminal",
		"Web terminal disabled, read-only by default, isolated from Manager secrets",
		"\"$GPASSWD_BIN\" -d \"$operator_user\" \"$group\"",
		"\"$USERMOD_BIN\" -L \"$operator_user\"",
		"Restart=always",
		"20-lightningos-restart.conf",
		"\"$INSTALL_BIN\" -m 0755 \"$src\" /usr/local/sbin/lightningos-manager-firewall",
		"/usr/local/sbin/lightningos-manager-firewall",
		"/usr/local/sbin/lightningos-setup-manager-tls-mdns",
		"normalize_legacy_manager_identity",
		"finalize_legacy_manager_identity",
		"$project_dir/scripts/migrate-legacy-manager.sh",
		"Normalizing legacy Manager identity without restarting Bitcoin or LND",
		"--finalize",
	}
	for _, fragment := range required {
		if !strings.Contains(embeddedAppUpgradeScript, fragment) {
			t.Fatalf("embedded app upgrade script is missing %q", fragment)
		}
	}
	normalizeAt := strings.Index(embeddedAppUpgradeScript, "normalize_legacy_manager_identity\n")
	buildAt := strings.Index(embeddedAppUpgradeScript, "print_step \"Building manager binary\"")
	restartAt := strings.Index(embeddedAppUpgradeScript, "print_step \"Restarting lightningos-manager\"")
	finalizeAt := strings.LastIndex(embeddedAppUpgradeScript, "finalize_legacy_manager_identity\n")
	completeAt := strings.LastIndex(embeddedAppUpgradeScript, "cutover_prepared=0")
	if normalizeAt < 0 || buildAt < 0 || restartAt < 0 || finalizeAt < 0 || completeAt < 0 ||
		!(normalizeAt < buildAt && restartAt < finalizeAt && finalizeAt < completeAt) {
		t.Fatal("legacy Manager migration must wrap the authenticated build and privilege cutover")
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
		"TERMINAL_ALLOW_WRITE=0",
		"/etc/lightningos/terminal.env",
		"ufw --force delete allow \"${MANAGER_PORT}/tcp\"",
		"ufw allow from \"$lan_cidr\"",
		"ufw allow in on tailscale0",
		"Ignoring invalid saved local network",
		"so stdout contains only the selected CIDR",
		"Restart=always",
		"setup-manager-tls-mdns.sh",
		"/var/lib/lightningos-privileged/system-integrations-20260815-v7",
	} {
		combined := embeddedTerminalHelper + embeddedManagerFirewallHelper + embeddedManagerTLSMDNSHelper + embeddedAppUpgradeScript + systemIntegrationsMarkerPath
		if !strings.Contains(combined, fragment) {
			t.Fatalf("embedded system integration assets are missing %q", fragment)
		}
	}
}

func TestEmbeddedSystemIntegrationAssetsMatchBrokerCatalog(t *testing.T) {
	manager := privileged.NewNativeSystemIntegrationsManager(nil)
	assets := []privileged.SystemIntegrationAssetInstallParams{
		{Asset: privileged.SystemIntegrationAssetTerminal, Content: embeddedTerminalHelper},
		{Asset: privileged.SystemIntegrationAssetManagerFirewall, Content: embeddedManagerFirewallHelper},
		{Asset: privileged.SystemIntegrationAssetManagerTLSMDNS, Content: embeddedManagerTLSMDNSHelper},
	}
	for _, asset := range assets {
		state, err := manager.InstallAsset(context.Background(), asset, true)
		if err != nil || state.Status != "validated" || state.Changed {
			t.Fatalf("embedded asset %s does not match broker catalog: state=%+v err=%v", asset.Asset, state, err)
		}
	}
}
