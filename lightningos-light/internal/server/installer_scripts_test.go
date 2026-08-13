package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallersConfigureManagerFirewallWithoutPrompt(t *testing.T) {
	installers := []string{"install.sh", "install_existing.sh", "install_existing_pi.sh"}
	for _, name := range installers {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			if strings.Contains(string(raw), `"$MANAGER_FIREWALL_SCRIPT" --interactive`) {
				t.Fatalf("%s must accept the detected LAN CIDR without prompting", name)
			}
		})
	}
}

func TestExistingInstallersAuthorizeDetectedLNDService(t *testing.T) {
	installers := []string{"install_existing.sh", "install_existing_pi.sh"}
	for _, name := range installers {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			content := string(raw)
			for _, expected := range []string{
				`lnd_service="${LND_SERVICE:-lnd}"`,
				`restart ${lnd_service}`,
				`restart --no-block ${lnd_service}`,
			} {
				if !strings.Contains(content, expected) {
					t.Fatalf("%s must authorize the detected LND service; missing %q", name, expected)
				}
			}
		})
	}
}

func TestInstallAndUpgradeScriptsProvisionBrokerRuntimeDirectory(t *testing.T) {
	templatePath := filepath.Join("..", "..", "templates", "lightningos-privileged.tmpfiles.conf")
	templateRaw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read broker tmpfiles template: %v", err)
	}
	if strings.ReplaceAll(string(templateRaw), "\r\n", "\n") != "d /run/lock/lightningos 0750 root root -\n" {
		t.Fatalf("unexpected broker tmpfiles rule: %q", string(templateRaw))
	}

	scripts := []string{
		filepath.Join("..", "..", "install.sh"),
		filepath.Join("..", "..", "install_existing.sh"),
		filepath.Join("..", "..", "install_existing_pi.sh"),
		filepath.Join("assets", "upgrade-app.sh"),
	}
	for _, path := range scripts {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			content := strings.ReplaceAll(string(raw), "\r\n", "\n")
			for _, expected := range []string{
				`PRIVILEGED_TMPFILES_CONFIG="/etc/tmpfiles.d/lightningos-privileged.conf"`,
				"templates/lightningos-privileged.tmpfiles.conf",
				`/usr/bin/systemd-tmpfiles --create "$PRIVILEGED_TMPFILES_CONFIG"`,
			} {
				if !strings.Contains(content, expected) {
					t.Fatalf("%s does not provision the broker runtime directory; missing %q", path, expected)
				}
			}
			create := strings.Index(content, `/usr/bin/systemd-tmpfiles --create "$PRIVILEGED_TMPFILES_CONFIG"`)
			selfTest := strings.Index(content, `"operation":"self_test"`)
			if create < 0 || selfTest < 0 || create > selfTest {
				t.Fatalf("%s must create the runtime directory before broker self-test", path)
			}
		})
	}
}

func TestAppUpgradeMigratesManagerTLSBeforeRestart(t *testing.T) {
	path := filepath.Join("assets", "upgrade-app.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgrade app script: %v", err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, expected := range []string{
		`project_dir/internal/server/assets/setup-manager-tls-mdns.sh`,
		`configure_manager_tls_mdns`,
		`LIGHTNINGOS_MANAGER_GROUP="$manager_group"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("app upgrade must migrate manager TLS; missing %q", expected)
		}
	}

	migration := strings.LastIndex(content, "configure_manager_tls_mdns\n")
	restart := strings.LastIndex(content, `"$SYSTEMCTL_BIN" restart lightningos-manager`)
	if migration < 0 || restart < 0 || migration > restart {
		t.Fatal("manager TLS migration must run before lightningos-manager restarts")
	}
}

func TestAppUpgradeStagesReversiblePrivilegeCutoverBeforeRestart(t *testing.T) {
	path := filepath.Join("assets", "upgrade-app.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgrade app script: %v", err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, expected := range []string{
		`stage_privilege_cutover()`,
		`/var/lib/lightningos/rollback/0.5.3-privilege-cutover`,
		`/usr/local/sbin/lightningos-rollback-privilege-cutover`,
		`"$CP_BIN" -a -- "$config_path" "$config_tmp"`,
		`mode: \"enforce\"`,
		`gpasswd -d "$manager_user" docker`,
		`: > "$state_root/had-docker-group"`,
		`: > "$state_root/sudoers.existed"`,
		`/usr/local/sbin/lightningos-rollback-privilege-cutover || true`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("app upgrade privilege cutover is missing %q", expected)
		}
	}

	stage := strings.LastIndex(content, "if ! (stage_privilege_cutover && configure_manager_sudoers); then")
	restart := strings.LastIndex(content, `if "$SYSTEMCTL_BIN" restart lightningos-manager`)
	if stage < 0 || restart < 0 || stage > restart {
		t.Fatal("privilege cutover must be staged before lightningos-manager restarts")
	}
}

func TestPrivilegeCutoverRollbackRestoresOnlyAccessBoundary(t *testing.T) {
	path := filepath.Join("assets", "rollback-privilege-cutover.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read privilege cutover rollback: %v", err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, expected := range []string{
		`! -f "$STATE_ROOT/prepared"`,
		`cp -a --remove-destination -- "$backup" "$target"`,
		`restore_file "$STATE_ROOT/config.yaml" "$CONFIG_PATH"`,
		`restore_file "$STATE_ROOT/lightningos-manager.service" "$SERVICE_PATH"`,
		`restore_file "$STATE_ROOT/30-privilege-hardening.conf" "$DROPIN_PATH"`,
		`restore_file "$STATE_ROOT/sudoers" "$sudoers_path"`,
		`rm -f -- "$sudoers_path"`,
		`usermod -a -G docker "$manager_user"`,
		`systemctl restart lightningos-manager`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("privilege rollback is missing %q", expected)
		}
	}
	for _, forbidden := range []string{"/data/bitcoin", "/data/lnd", "/data/apps", "rm -rf"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("privilege rollback must not modify node or app data: found %q", forbidden)
		}
	}
}
