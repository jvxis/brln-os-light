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

func TestInstallerEntryPointsDefaultToLatestPublishedRelease(t *testing.T) {
	helperRaw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install-release-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	helper := strings.ReplaceAll(string(helperRaw), "\r\n", "\n")
	for _, expected := range []string{
		`local install_source="${LIGHTNINGOS_INSTALL_SOURCE:-latest}"`,
		`latest) ;;`,
		`checkout) return 0 ;;`,
		`BRLN_INSTALLER="$installer"`,
		`LIGHTNINGOS_RELEASE_BOOTSTRAPPED=1`,
		`bash "$bootstrap" "$@"`,
	} {
		if !strings.Contains(helper, expected) {
			t.Fatalf("release bootstrap is missing latest-release policy %q", expected)
		}
	}

	for _, installer := range []string{"install.sh", "install_existing.sh", "install_existing_pi.sh"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", installer))
		if err != nil {
			t.Fatal(err)
		}
		content := strings.ReplaceAll(string(raw), "\r\n", "\n")
		expected := `lightningos_bootstrap_latest_release "` + installer + `" "$@"`
		if !strings.Contains(content, `source "$REPO_ROOT/scripts/install-release-bootstrap.sh"`) || !strings.Contains(content, expected) {
			t.Fatalf("%s does not enter the latest published release bootstrap", installer)
		}
	}
}

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

func TestManagedInstallerRepairsStaleNotificationsAdminCredentials(t *testing.T) {
	path := filepath.Join("..", "..", "install.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read managed installer: %v", err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, expected := range []string{
		`ensure_notifications_admin()`,
		`PGCONNECT_TIMEOUT=5 psql -X "$current" -tAc "select 1"`,
		`psql_exec "Alter notifications admin password"`,
		`update_notifications_admin_dsn "$admin_user" "$pw"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("managed installer notifications recovery is missing %q", expected)
		}
	}
}

func TestInstallersUseAuthenticatedLNDReleaseHelper(t *testing.T) {
	canonicalPath := filepath.Join("assets", "upgrade-lnd.sh")
	canonicalRaw, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	canonical := strings.ReplaceAll(string(canonicalRaw), "\r\n", "\n")
	for _, expected := range []string{
		`MIN_REQUIRED_SIGNATURES=5`,
		`--proto '=https' --proto-redir '=https' --tlsv1.2`,
		`if [[ "$valid_signatures" -lt "$MIN_REQUIRED_SIGNATURES" ]]`,
		`actual_hash=$(sha256sum`,
		`tar --no-same-owner --no-same-permissions -xzf`,
		`--install-new`,
		`Refusing new LND installation over existing binaries.`,
	} {
		if !strings.Contains(canonical, expected) {
			t.Fatalf("canonical LND installer helper lacks %q", expected)
		}
	}

	for _, name := range []string{"install.sh", "install_existing.sh", "install_existing_pi.sh"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		content := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if !strings.Contains(content, `"$REPO_ROOT/internal/server/assets/upgrade-lnd.sh"`) {
			t.Fatalf("%s does not install the canonical authenticated LND helper", name)
		}
		if strings.Contains(content, `"$REPO_ROOT/scripts/upgrade-lnd.sh"`) {
			t.Fatalf("%s still installs the obsolete unauthenticated LND helper", name)
		}
	}

	freshRaw, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := strings.ReplaceAll(string(freshRaw), "\r\n", "\n")
	for _, expected := range []string{
		`LND_VERSION="0.21.1-beta"`,
		`"$LND_UPGRADE_SCRIPT" --version "$LND_VERSION" --install-new`,
		`Unable to authenticate the installed LND version; refusing replacement`,
	} {
		if !strings.Contains(fresh, expected) {
			t.Fatalf("fresh installer lacks closed LND policy %q", expected)
		}
	}
	for _, forbidden := range []string{
		`LND_VERSION="${LND_VERSION:-`,
		`LND_URL`,
		`curl -L "$LND_URL"`,
		`tar -xzf "$tmp/lnd.tar.gz"`,
	} {
		if strings.Contains(fresh, forbidden) {
			t.Fatalf("fresh installer retains unauthenticated LND behavior %q", forbidden)
		}
	}

	if _, err := os.Stat(filepath.Join("..", "..", "scripts", "upgrade-lnd.sh")); !os.IsNotExist(err) {
		t.Fatalf("obsolete unauthenticated LND helper still exists: %v", err)
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
				`NODESOURCE_INRELEASE_URL="https://deb.nodesource.com/node_${NODE_VERSION}.x/dists/nodistro/InRelease"`,
				`lightningos_install_authenticated_apt_key "$NODESOURCE_KEY_URL" "$NODESOURCE_KEY_FINGERPRINT" "$NODESOURCE_KEY_SHA256"`,
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
		`lightningos_install_authenticated_apt_key "$I2PD_KEY_URL" "$I2PD_KEY_FINGERPRINT" "$I2PD_KEY_SHA256"`,
		`"https://repo.i2pd.xyz/ubuntu/dists/${codename}/InRelease"`,
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
		`lightningos_install_authenticated_apt_key()`,
		`lightningos_verify_inrelease_envelope()`,
		`primary_count=$(printf '%s\n' "$listing" | grep -c '^pub:' || true)`,
		`actual=$(printf '%s\n' "$listing" | grep '^fpr:' | head -n1 | cut -d: -f10 || true)`,
		`if [[ "$primary_count" != "1" || "$actual" != "$expected" ]]`,
		`lightningos_verify_sha256 "$tmp/key.asc" "$expected_sha256" "${label} key"`,
		`--proto '=https' --proto-redir '=https' --tlsv1.2`,
		`gpg --batch --yes --dearmor`,
		`gpgv --keyring "$tmp/keyring.gpg" "$tmp/InRelease"`,
		`Repository metadata has trailing or malformed bytes`,
	} {
		if !strings.Contains(helper, expected) {
			t.Fatalf("artifact verifier is missing OpenPGP fail-closed policy %q", expected)
		}
	}
}

func TestInstallersAuthenticatePGDGAndTorRepositories(t *testing.T) {
	for _, name := range []string{"install.sh", "install_existing.sh", "install_existing_pi.sh"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		content := strings.ReplaceAll(string(raw), "\r\n", "\n")
		for _, expected := range []string{
			`PGDG_KEY_URL="https://www.postgresql.org/media/keys/ACCC4CF8.asc"`,
			`PGDG_KEY_FINGERPRINT="B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8"`,
			`PGDG_KEY_SHA256="0144068502a1eddd2a0280ede10ef607d1ec592ce819940991203941564e8e76"`,
			`lightningos_install_authenticated_apt_key "$PGDG_KEY_URL" "$PGDG_KEY_FINGERPRINT" "$PGDG_KEY_SHA256"`,
			`"https://apt.postgresql.org/pub/repos/apt/dists/${codename}-pgdg/InRelease"`,
			`URIs: https://apt.postgresql.org/pub/repos/apt`,
			`Signed-By: ${PGDG_KEYRING}`,
		} {
			if !strings.Contains(content, expected) {
				t.Fatalf("%s lacks authenticated PGDG policy %q", name, expected)
			}
		}
		for _, forbidden := range []string{
			`http://apt.postgresql.org`,
			`ACCC4CF8.asc | gpg`,
			`> /etc/apt/sources.list.d/pgdg.list`,
		} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("%s retains unauthenticated PGDG behavior %q", name, forbidden)
			}
		}
	}

	freshRaw, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := strings.ReplaceAll(string(freshRaw), "\r\n", "\n")
	for _, expected := range []string{
		`TOR_KEY_FINGERPRINT="A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89"`,
		`TOR_KEY_SHA256="3a17ae045c544aecc065cf401a4ed96dfc99b6081c8b8989716772773c4f2d1d"`,
		`lightningos_install_authenticated_apt_key "$TOR_KEY_URL" "$TOR_KEY_FINGERPRINT" "$TOR_KEY_SHA256"`,
		`"${TOR_REPO_URL}/dists/${codename}/InRelease"`,
		`URIs: ${TOR_REPO_URL}/`,
		`Signed-By: ${TOR_KEYRING}`,
	} {
		if !strings.Contains(fresh, expected) {
			t.Fatalf("fresh installer lacks authenticated Tor policy %q", expected)
		}
	}
	for _, forbidden := range []string{
		`falling back to jammy`,
		`| tee /usr/share/keyrings/deb.torproject.org-keyring.gpg`,
		`cat > /etc/apt/sources.list.d/tor.list`,
	} {
		if strings.Contains(fresh, forbidden) {
			t.Fatalf("fresh installer retains unauthenticated Tor behavior %q", forbidden)
		}
	}

	helperRaw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install-artifact-verification.sh"))
	if err != nil {
		t.Fatal(err)
	}
	helper := strings.ReplaceAll(string(helperRaw), "\r\n", "\n")
	metadataAuth := strings.Index(helper, `gpgv --keyring "$tmp/keyring.gpg" "$tmp/InRelease"`)
	systemInstall := strings.LastIndex(helper, `install -o root -g root -m 0644 "$tmp/keyring.gpg" "$destination"`)
	if metadataAuth < 0 || systemInstall <= metadataAuth {
		t.Fatal("APT keyring can be installed before repository metadata authentication")
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

func TestExistingInstallersRequireCanonicalSocketBrokerCutover(t *testing.T) {
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
				`ensure_privileged_broker_units "$manager_user"`,
				`systemctl enable --now lightningos-privileged.socket`,
				`manager_user" != "lightningos`,
			} {
				if !strings.Contains(content, expected) {
					t.Fatalf("%s must use the canonical socket broker cutover; missing %q", name, expected)
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
	wantTmpfiles := "d /run/lock/lightningos 0750 root root -\n" +
		"d /run/lightningos-privileged 0750 root lightningos -\n"
	if strings.ReplaceAll(string(templateRaw), "\r\n", "\n") != wantTmpfiles {
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

func TestManagerAndBrokerSystemdBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
	managerRaw, err := os.ReadFile(filepath.Join(root, "templates", "systemd", "lightningos-manager.service"))
	if err != nil {
		t.Fatal(err)
	}
	manager := string(managerRaw)
	for _, expected := range []string{
		"Wants=network-online.target lightningos-privileged.socket",
		"NoNewPrivileges=true",
		"PrivateDevices=true",
		"ProtectSystem=strict",
		"ProtectKernelTunables=true",
		"ProtectKernelModules=true",
		"ProtectControlGroups=true",
		"CapabilityBoundingSet=",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
		"SystemCallFilter=~@clock @cpu-emulation @debug @module @mount @obsolete @raw-io @reboot @swap",
	} {
		if !strings.Contains(manager, expected) {
			t.Errorf("manager unit is missing %q", expected)
		}
	}
	if strings.Contains(manager, "/etc/ufw") || strings.Contains(manager, "SupplementaryGroups=docker") {
		t.Fatal("manager unit retains a legacy privileged writable path or group")
	}

	socketRaw, err := os.ReadFile(filepath.Join(root, "templates", "systemd", "lightningos-privileged.socket"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"SocketGroup=lightningos", "SocketMode=0660", "Accept=yes", "RemoveOnStop=true"} {
		if !strings.Contains(string(socketRaw), expected) {
			t.Errorf("broker socket unit is missing %q", expected)
		}
	}

	serviceRaw, err := os.ReadFile(filepath.Join(root, "templates", "systemd", "lightningos-privileged@.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"ExecStart=/usr/local/libexec/lightningos-privileged",
		"StandardInput=socket",
		"StandardOutput=socket",
		"ProtectSystem=full",
		"ReadWritePaths=/usr/local /etc/lightningos /etc/ufw /etc/systemd/system -/etc/avahi/services",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
	} {
		if !strings.Contains(string(serviceRaw), expected) {
			t.Errorf("broker service unit is missing %q", expected)
		}
	}
	if strings.Contains(string(serviceRaw), "ReadWritePaths=/etc ") {
		t.Fatal("broker service makes all of /etc writable")
	}
}

func TestInstallersProvisionFixedNativeAppIdentities(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, relative := range []string{
		"install.sh",
		"install_existing.sh",
		"install_existing_pi.sh",
		filepath.Join("internal", "server", "assets", "upgrade-app.sh"),
	} {
		raw, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		for _, identity := range []string{"lightningos-loop", "lightningos-elements", "lightningos-peerswap"} {
			if !strings.Contains(content, "ensure_native_app_identity "+identity+" ") {
				t.Errorf("%s does not provision fixed identity %s", relative, identity)
			}
		}
		if !strings.Contains(content, "--no-create-home --shell /usr/sbin/nologin") {
			t.Errorf("%s does not constrain native application accounts", relative)
		}
	}
}

func TestReleaseBootstrapRequiresImmutableAttestedPublishedRelease(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "lo_bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{
		`RELEASE_TAG_API_BASE="https://api.github.com/repos/jvxis/brln-os-light/releases/tags"`,
		`--proto '=https'`,
		`"immutable"[[:space:]]*:[[:space:]]*true`,
		`"draft"[[:space:]]*:[[:space:]]*false`,
		`Immutable LightningOS release attestation verified`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("release bootstrap lacks immutable-release gate %q", required)
		}
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

func TestExistingInstallersKeepDataDirectoryDiagnosticsOutOfCapturedPath(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "install_existing.sh"),
		filepath.Join("..", "..", "install_existing_pi.sh"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		for _, required := range []string{
			`print_warn "${label} directory not found at ${default}" >&2`,
			`print_ok "Symlink created: ${default} -> ${dir}" >&2`,
			`echo "$dir"`,
		} {
			if !strings.Contains(content, required) {
				t.Errorf("%s can contaminate resolve_data_dir stdout; missing %q", path, required)
			}
		}
	}
}

func TestExistingInstallersUseTransactionalPrivilegeCutover(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "install_existing.sh"),
		filepath.Join("..", "..", "install_existing_pi.sh"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		prepare := strings.Index(content, `upgrade-app.sh" --prepare-cutover-only`)
		stage := strings.Index(content, `upgrade-app.sh" --stage-cutover-only`)
		build := -1
		if prepare >= 0 {
			if relative := strings.Index(content[prepare:], "\n    build_manager"); relative >= 0 {
				build = prepare + relative
			}
		}
		if prepare < 0 || build < 0 || stage < 0 || stage < build {
			t.Errorf("%s does not prepare rollback before build and stage cutover afterward", path)
		}
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

func TestAppUpgradeTrustedCheckoutIsRootOnlyAndCommitPinned(t *testing.T) {
	path := filepath.Join("assets", "upgrade-app.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgrade app script: %v", err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, expected := range []string{
		`require_root`,
		`--trusted-checkout`,
		`Trusted checkout HEAD does not match --commit.`,
		`diff --quiet --no-ext-diff --`,
		`diff --cached --quiet --no-ext-diff --`,
		`archive "$EXPECTED_COMMIT" | "$TAR_BIN" -x`,
		`"$INSTALL_BIN" -d -o root -g root -m 0700 "$worktree_dir"`,
		`available_kib < 3145728`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("trusted checkout upgrade boundary is missing %q", expected)
		}
	}
	if strings.Contains(content, `verify_immutable_release ||`) {
		t.Fatal("release attestation must remain fail-closed for API-triggered upgrades")
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
		`prepare_privilege_cutover()`,
		`stage_privilege_cutover()`,
		`/var/lib/lightningos/rollback/0.5.3-privilege-cutover`,
		`/usr/local/sbin/lightningos-rollback-privilege-cutover`,
		`"$CP_BIN" -a -- "$config_path" "$config_tmp"`,
		`mode: \"enforce\"`,
		`gpasswd -d "$manager_user" docker`,
		`: > "$state_root/had-docker-group"`,
		`capture_optional_file "$sudoers_path" "$state_root" "sudoers"`,
		`capture_optional_file "/opt/lightningos/manager/lightningos-manager" "$state_root" "lightningos-manager"`,
		`capture_optional_file "/opt/lightningos/manager/.build_stamp" "$state_root" "manager-build-stamp"`,
		`/usr/local/sbin/lightningos-upgrade-lnd`,
		`/usr/local/sbin/lightningos-upgrade-app`,
		`saw_lnd_upgrade == saw_app_upgrade`,
		`"$RM_BIN" -f -- "$sudoers_path" "$auth_sudoers_path"`,
		`NoNewPrivileges=true`,
		`RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK`,
		`lightningos-manager broker-self-test`,
		`[[ -S /run/lightningos-privileged/broker.sock ]]`,
		`for _ in $(seq 1 20)`,
		`capture_lnd_manager_credential_boundary`,
		`capture_optional_file "$credential_path" "$state_root" "lnd-manager-macaroon"`,
		`capture_optional_file "$credential_state_path" "$state_root" "lnd-manager-state"`,
		`upgrade_lnd_manager_credential_rollback_state`,
		`: > "$state_root/schema-v3"`,
		`: > "$state_root/schema-v4"`,
		`: > "$state_root/schema-v5"`,
		`: > "$state_root/schema-v6"`,
		`write_manager_build_stamp`,
		`printf '%s %s\n' "$EXPECTED_COMMIT" "$VERSION"`,
		`capture_manager_ui_boundary`,
		`manager-ui.tar`,
		`lightningos-manager lnd-manager-credential-ensure`,
		`/usr/local/sbin/lightningos-rollback-privilege-cutover || true`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("app upgrade privilege cutover is missing %q", expected)
		}
	}

	stage := strings.LastIndex(content, "if ! stage_privilege_cutover; then")
	prepare := strings.LastIndex(content, "prepare_privilege_cutover\n")
	uiInstall := strings.LastIndex(content, `"$CP_BIN" -a "$project_dir/ui/dist/." /opt/lightningos/ui/`)
	restart := strings.LastIndex(content, `if "$SYSTEMCTL_BIN" restart lightningos-manager; then`)
	if prepare < 0 || uiInstall < 0 || prepare > uiInstall {
		t.Fatal("rollback state must be prepared before the manager UI is replaced")
	}
	if stage < 0 || restart < 0 || stage > restart {
		t.Fatal("privilege cutover must be staged before lightningos-manager restarts")
	}
	if !strings.Contains(content, `curl -sk --max-time 3 https://127.0.0.1:8443/api/health`) {
		t.Fatal("privilege cutover must wait for the manager health endpoint before acceptance")
	}
}

func TestAppUpgradeRecognizesDocumentedLegacyDockerSudoers(t *testing.T) {
	path := filepath.Join("assets", "upgrade-app.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upgrade app script: %v", err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, expected := range []string{`*/docker\ \*`, `*/docker-compose\ \*`, `count <= 7`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("documented legacy App Store sudoers is missing %q", expected)
		}
	}
	if !strings.Contains(content, `((saw_broker == 0)) || return 1`) || strings.Contains(content, `saw_broker == 1 && saw_lnd_upgrade`) {
		t.Fatal("legacy sudoers must accept zero or one known broker command, never duplicates")
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
		`restore_or_remove "lightningos-manager.service" "$SERVICE_PATH"`,
		`restore_or_remove "30-privilege-hardening.conf" "$DROPIN_PATH"`,
		`restore_or_remove "lightningos-manager" "$MANAGER_BIN"`,
		`restore_or_remove "manager-build-stamp" "$BUILD_STAMP"`,
		`tar -C /opt/lightningos -xpf "$STATE_ROOT/manager-ui.tar"`,
		`restore_or_remove "lightningos-privileged" "$BROKER_BIN"`,
		`restore_or_remove "lightningos-privileged.socket" "$SOCKET_UNIT"`,
		`systemctl cat lightningos-privileged.socket`,
		`restore_or_remove "rollback-command" "$ROLLBACK_BIN"`,
		`restore_file "$STATE_ROOT/sudoers" "$sudoers_path"`,
		`rm -f -- "$sudoers_path"`,
		`usermod -a -G docker "$manager_user"`,
		`systemctl restart lightningos-manager`,
		`curl -sk --max-time 3 https://127.0.0.1:8443/api/health`,
		`The previous LightningOS Manager did not respond after rollback.`,
		`! -f "$STATE_ROOT/schema-v6"`,
		`runuser -u lightningos -- "$MANAGER_BIN" lnd-manager-credential-rollback`,
		`chown "$admin_uid:$admin_gid" "$LND_ADMIN_MACAROON"`,
		`chmod "$admin_mode" "$LND_ADMIN_MACAROON"`,
		`chown lnd:lnd "$LND_ADMIN_MACAROON"`,
		`chmod 0640 "$LND_ADMIN_MACAROON"`,
		`rm -f -- "$LND_MANAGER_MACAROON"`,
		`rm -f -- "$LND_MANAGER_STATE"`,
		`restore_file "$STATE_ROOT/lnd-manager-macaroon" "$LND_MANAGER_MACAROON"`,
		`restore_file "$STATE_ROOT/lnd-manager-state" "$LND_MANAGER_STATE"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("privilege rollback is missing %q", expected)
		}
	}
	for _, forbidden := range []string{"/data/bitcoin", "/data/apps", "rm -rf", "cp -a -- /data/lnd", "rm -f -- /data/lnd"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("privilege rollback must not modify node or app data: found %q", forbidden)
		}
	}
}

func TestLegacyManagerMigrationIsFailClosedAndPreservesNodeServices(t *testing.T) {
	path := filepath.Join("..", "..", "scripts", "migrate-legacy-manager.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read legacy Manager migration: %v", err)
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, expected := range []string{
		`MODE="check"`,
		`--apply`,
		`--finalize`,
		`--rollback`,
		`/var/lib/lightningos/rollback/0.5.6-legacy-manager-normalization`,
		`legacy_sudoers="$(legacy_manager_sudoers_path "$user")"`,
		`validate_legacy_auth_enable_sudoers "$AUTH_SUDOERS_PATH" "$user"`,
		`validate_legacy_app_upgrade_sudoers "$APP_UPGRADE_SUDOERS_PATH"`,
		`validate_tls_backups_tree || return 1`,
		`[[ "$path" == /etc/lightningos/tls/backups ]]`,
		`chmod 0700 "$path"`,
		`/etc/sudoers.d/lightningos-${user}`,
		`capture_optional "$legacy_sudoers" legacy-sudoers`,
		`capture_optional "$APP_UPGRADE_SUDOERS_PATH" app-upgrade-sudoers`,
		`restore_optional "$legacy_sudoers" legacy-sudoers`,
		`restore_optional "$APP_UPGRADE_SUDOERS_PATH" app-upgrade-sudoers`,
		`visudo -cf "$path"`,
		`root_regular_file "$fragment"`,
		`[[ "$version" == 0.5.2-beta* || "$version" == 0.5.3-beta* || "$version" == 0.5.4-beta* ]]`,
		`protected_snapshot "$STATE_ROOT/protected-services.before"`,
		`verify_protected_snapshot "$STATE_ROOT/protected-services.before"`,
		`User=$CANONICAL_USER`,
		`Group=$CANONICAL_GROUP`,
		`systemctl restart "$SERVICE"`,
		`wait_cutover`,
		`NoNewPrivileges`,
		`lightningos-privileged.socket`,
		`[[ ! -e "$SUDOERS_PATH" ]]`,
		`use the privilege-cutover rollback command`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("legacy Manager migration is missing %q", expected)
		}
	}

	for _, forbidden := range []string{
		`systemctl stop lnd`,
		`systemctl stop bitcoind`,
		`systemctl restart bitcoind`,
		`systemctl restart bitcoin`,
		`systemctl kill lnd`,
		`systemctl kill bitcoind`,
		`rm -rf`,
		`chown -R "$CANONICAL_USER:$CANONICAL_GROUP" /data`,
		`chown -R "$CANONICAL_USER:$CANONICAL_GROUP" /etc/lightningos/tls/backups`,
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("legacy Manager migration must not modify protected node services or data: found %q", forbidden)
		}
	}
}
