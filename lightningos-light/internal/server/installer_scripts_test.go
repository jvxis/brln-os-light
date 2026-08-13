package server

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallersVerifyPinnedGoAndGoTTYBeforeExtraction(t *testing.T) {
	tests := []struct {
		name        string
		goArtifact  string
		goChecksum  string
		gottyAsset  string
		gottyDigest string
	}{
		{
			name:        "install.sh",
			goArtifact:  "go${GO_VERSION}.linux-amd64.tar.gz",
			goChecksum:  "bddf8e653c82429aea7aec2520774e79925d4bb929fe20e67ecc00dd5af44c50",
			gottyAsset:  "gotty_v${GOTTY_VERSION}_linux_amd64.tar.gz",
			gottyDigest: "9cf032e1f3a49d33da3ba32c79f49892aad94e52edc6417524a76b623ced2f5f",
		},
		{
			name:        "install_existing.sh",
			goArtifact:  "go${GO_VERSION}.linux-amd64.tar.gz",
			goChecksum:  "bddf8e653c82429aea7aec2520774e79925d4bb929fe20e67ecc00dd5af44c50",
			gottyAsset:  "gotty_v${GOTTY_VERSION}_linux_amd64.tar.gz",
			gottyDigest: "9cf032e1f3a49d33da3ba32c79f49892aad94e52edc6417524a76b623ced2f5f",
		},
		{
			name:        "install_existing_pi.sh",
			goArtifact:  "go${GO_VERSION}.linux-arm64.tar.gz",
			goChecksum:  "4e02e2979e53b40f3666bba9f7e5ea0b99ea5156e0824b343fd054742c25498d",
			gottyAsset:  "gotty_v${GOTTY_VERSION}_linux_arm64.tar.gz",
			gottyDigest: "fcef4efcf6cbdf81540c765ebdfd533ada86f7d58322b4f831573d098712b0dd",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", test.name))
			if err != nil {
				t.Fatalf("read %s: %v", test.name, err)
			}
			content := strings.ReplaceAll(string(raw), "\r\n", "\n")
			for _, expected := range []string{
				`source "$ARTIFACT_VERIFY_SCRIPT"`,
				`GO_VERSION="1.24.12"`,
				`GO_ARTIFACT="` + test.goArtifact + `"`,
				`GO_TARBALL_SHA256="` + test.goChecksum + `"`,
				`GOTTY_VERSION="1.8.0"`,
				`GOTTY_ARTIFACT="` + test.gottyAsset + `"`,
				`GOTTY_SHA256="` + test.gottyDigest + `"`,
			} {
				if !strings.Contains(content, expected) {
					t.Fatalf("%s is missing pinned artifact policy %q", test.name, expected)
				}
			}

			goVerify := strings.Index(content, `lightningos_download_verified_artifact "$GO_TARBALL_URL"`)
			goInspect := strings.Index(content, `tar -tzf "$archive"`)
			goReplace := strings.Index(content, `rm -rf /usr/local/go`)
			gottyVerify := strings.Index(content, `lightningos_download_verified_artifact "$GOTTY_URL"`)
			gottyExtract := strings.Index(content, `tar -xzf "$tmp/$GOTTY_ARTIFACT"`)
			if goVerify < 0 || goInspect < goVerify || goReplace < goInspect {
				t.Fatalf("%s must authenticate and inspect Go before replacing the installed toolchain", test.name)
			}
			if gottyVerify < 0 || gottyExtract < gottyVerify {
				t.Fatalf("%s must authenticate GoTTY before extraction", test.name)
			}
		})
	}
}

func TestInstallerArtifactVerificationRejectsModifiedBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash fixture runs in Linux CI and disposable installer gates")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	helper, err := filepath.Abs(filepath.Join("..", "..", "scripts", "install-artifact-verification.sh"))
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "artifact.tar.gz")
	original := []byte("authenticated installer artifact\n")
	if err := os.WriteFile(artifact, original, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(original)
	expected := hex.EncodeToString(digest[:])
	command := `set -Eeuo pipefail; source "$1"; lightningos_verify_sha256 "$2" "$3" fixture`
	if output, err := exec.Command(bash, "-c", command, "test", helper, artifact, expected).CombinedOutput(); err != nil {
		t.Fatalf("valid fixture rejected: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	if err := os.WriteFile(artifact, append(original, 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(bash, "-c", command, "test", helper, artifact, expected).Run(); err == nil {
		t.Fatal("modified fixture passed pinned SHA-256 verification")
	}
}

func TestInstallersUseFingerprintPinnedAPTRepositories(t *testing.T) {
	installers := []string{"install.sh", "install_existing.sh", "install_existing_pi.sh"}
	for _, name := range installers {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", name))
			if err != nil {
				t.Fatal(err)
			}
			content := strings.ReplaceAll(string(raw), "\r\n", "\n")
			for _, expected := range []string{
				`NODE_VERSION="24"`,
				`NODESOURCE_KEY_URL="https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key"`,
				`NODESOURCE_KEY_FINGERPRINT="6F71F525282841EEDAF851B42F59B5F99B1BE0B4"`,
				`NODESOURCE_KEY_SHA256="b42e0321dabdc24e892115da705cf061167eac12a317f23d329862d0aa0a271d"`,
				`lightningos_install_verified_apt_key "$NODESOURCE_KEY_URL" "$NODESOURCE_KEY_FINGERPRINT" "$NODESOURCE_KEY_SHA256"`,
				`URIs: https://deb.nodesource.com/node_${NODE_VERSION}.x`,
				`Suites: nodistro`,
				`Signed-By: ${NODESOURCE_KEYRING}`,
			} {
				if !strings.Contains(content, expected) {
					t.Fatalf("%s is missing closed NodeSource policy %q", name, expected)
				}
			}
			for _, forbidden := range []string{
				`deb.nodesource.com/setup_`,
				`resolve_node_version`,
			} {
				if strings.Contains(content, forbidden) {
					t.Fatalf("%s retains remote NodeSource setup behavior %q", name, forbidden)
				}
			}
		})
	}

	freshRaw, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := strings.ReplaceAll(string(freshRaw), "\r\n", "\n")
	for _, expected := range []string{
		`I2PD_KEY_URL="https://repo.i2pd.xyz/r4sas.gpg"`,
		`I2PD_KEY_FINGERPRINT="951928BB317024EFD053D73C66F6C87B98EBCFE2"`,
		`I2PD_KEY_SHA256="c9db4fa521b75bb2821c103e595173f289efe282aa5cbe9613f523983000140f"`,
		`lightningos_install_verified_apt_key "$I2PD_KEY_URL" "$I2PD_KEY_FINGERPRINT" "$I2PD_KEY_SHA256"`,
		`URIs: https://repo.i2pd.xyz/ubuntu`,
		`Signed-By: ${I2PD_KEYRING}`,
	} {
		if !strings.Contains(fresh, expected) {
			t.Fatalf("install.sh is missing closed i2pd repository policy %q", expected)
		}
	}
	if strings.Contains(fresh, "repo.i2pd.xyz/.help/add_repo") {
		t.Fatal("install.sh still executes the remote i2pd repository helper")
	}

	helperRaw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install-artifact-verification.sh"))
	if err != nil {
		t.Fatal(err)
	}
	helper := strings.ReplaceAll(string(helperRaw), "\r\n", "\n")
	for _, expected := range []string{
		`lightningos_verify_openpgp_primary_fingerprint()`,
		`primary_count=$(printf '%s\n' "$listing" | grep -c '^pub:' || true)`,
		`actual=$(printf '%s\n' "$listing" | grep '^fpr:' | head -n1 | cut -d: -f10 || true)`,
		`if [[ "$primary_count" != "1" || "$actual" != "$expected" ]]`,
		`lightningos_verify_sha256 "$tmp/key.asc" "$expected_sha256" "${label} key"`,
		`--proto '=https' --proto-redir '=https' --tlsv1.2`,
		`gpg --batch --yes --dearmor`,
	} {
		if !strings.Contains(helper, expected) {
			t.Fatalf("artifact verifier is missing OpenPGP fail-closed policy %q", expected)
		}
	}
}

func TestInstallersDoNotPipeRemoteContentToShell(t *testing.T) {
	for _, name := range []string{"install.sh", "install_existing.sh", "install_existing_pi.sh"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		for lineNumber, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			trimmed := strings.TrimSpace(line)
			if (strings.Contains(trimmed, "curl ") || strings.Contains(trimmed, "wget ")) &&
				strings.Contains(trimmed, "|") && (strings.Contains(trimmed, "bash") || strings.Contains(trimmed, "sh ")) {
				t.Fatalf("%s:%d pipes remote content to a shell: %s", name, lineNumber+1, trimmed)
			}
		}
	}
}

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

func TestInstallAndUpgradeBuildsDoNotRequireTrustedGitOwnership(t *testing.T) {
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
			buildLines := 0
			for _, line := range strings.Split(content, "\n") {
				if strings.Contains(line, "go build") || strings.Contains(line, `"$GO_BIN" build`) {
					buildLines++
					if !strings.Contains(line, "-buildvcs=false") {
						t.Fatalf("%s Go build may fail when sudo changes repository ownership: %q", path, line)
					}
				}
			}
			if buildLines < 2 {
				t.Fatalf("%s must build both the manager and privileged broker; found %d Go build lines", path, buildLines)
			}
		})
	}
}

func TestInstallersDoNotIssueSetupTokensIntoCapturedLogs(t *testing.T) {
	installers := []string{
		filepath.Join("..", "..", "install.sh"),
		filepath.Join("..", "..", "install_existing.sh"),
	}
	for _, path := range installers {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			content := strings.ReplaceAll(string(raw), "\r\n", "\n")
			guard := strings.Index(content, `if [[ ! -t 0 || ! -t 1 || ! -w /dev/tty ]]`)
			issue := strings.Index(content, `token_output=$("$MANAGER_BIN" auth setup-token new`)
			ttyOutput := strings.Index(content, `} > /dev/tty`)
			if guard < 0 || issue < 0 || ttyOutput < 0 || guard > issue || issue > ttyOutput {
				t.Fatalf("%s must issue setup tokens only after an interactive-terminal guard and write them directly to /dev/tty", path)
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
