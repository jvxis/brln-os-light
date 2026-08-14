package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalSecureDefaultsAndInstallMigrations(t *testing.T) {
	root := moduleRoot(t)
	read := func(relative string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	secretsTemplate := read("templates/secrets.env")
	for _, required := range []string{"TERMINAL_ENABLED=0", "TERMINAL_ALLOW_WRITE=0"} {
		if !strings.Contains(secretsTemplate, required) {
			t.Fatalf("terminal secrets template is missing %q", required)
		}
	}
	if strings.Contains(secretsTemplate, "TERMINAL_OPERATOR_PASSWORD") {
		t.Fatal("GoTTY and Linux operator credentials are still coupled in the secrets template")
	}

	for _, relative := range []string{"install.sh", "install_existing.sh", "install_existing_pi.sh"} {
		script := read(relative)
		for _, forbidden := range []string{
			"TERMINAL_ALLOW_WRITE=1",
			`set_env_value "TERMINAL_ENABLED" "1"`,
			`ensure_group_member "$user" sudo`,
			`ensure_group_membership "$user" lightningos sudo systemd-journal`,
			`echo "${user}:${pw}" | chpasswd`,
		} {
			if strings.Contains(script, forbidden) {
				t.Fatalf("%s retains insecure terminal behavior %q", relative, forbidden)
			}
		}
		for _, required := range []string{
			"/etc/lightningos/terminal.env",
			`[[ "$user" == "losop" ]]`,
			`gpasswd -d "$user" "$group"`,
			`usermod -L "$user"`,
		} {
			if !strings.Contains(script, required) {
				t.Fatalf("%s is missing terminal migration %q", relative, required)
			}
		}
	}

	upgrade := read("internal/server/assets/upgrade-app.sh")
	for _, required := range []string{
		"disable --now lightningos-terminal",
		"TERMINAL_ALLOW_WRITE=0",
		"/etc/lightningos/terminal.env",
		"-d \"$operator_user\" \"$group\"",
		"-L \"$operator_user\"",
		"-f -- /usr/local/sbin/lightningos-terminal-password",
	} {
		if !strings.Contains(upgrade, required) {
			t.Fatalf("upgrade migration is missing %q", required)
		}
	}
}

func TestTerminalUnitIsIsolatedFromManagerSecretsAndHostPrivileges(t *testing.T) {
	root := moduleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "templates", "systemd", "lightningos-terminal.service"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(raw)
	for _, forbidden := range []string{"/etc/lightningos/secrets.env", "SupplementaryGroups=", "User=root", "Group=lightningos"} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("terminal unit retains forbidden privilege/exposure %q", forbidden)
		}
	}
	for _, required := range []string{
		"User=losop",
		"Group=losop",
		"EnvironmentFile=/etc/lightningos/terminal.env",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ProtectHome=read-only",
		"CapabilityBoundingSet=",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
	} {
		if !strings.Contains(unit, required) {
			t.Fatalf("terminal unit is missing hardening %q", required)
		}
	}
}
