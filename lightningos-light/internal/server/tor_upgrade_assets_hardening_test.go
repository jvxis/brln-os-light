package server

import (
	"strings"
	"testing"
)

func TestTorUpgradeAuthenticatesPinnedRepositoryKeyBeforeInstallation(t *testing.T) {
	script := embeddedTorUpgradeScript
	required := []string{
		`TOR_REPO_KEY_FINGERPRINT="A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89"`,
		`--fingerprint "$TOR_REPO_KEY_FINGERPRINT"`,
		`if [[ "$imported_fingerprint" != "$TOR_REPO_KEY_FINGERPRINT" ]]`,
		`Signed-By: ${TOR_KEYRING}`,
		`--verify-only`,
		`--proto '=https' --tlsv1.2`,
		`gpgv --keyring "$keyring_file" "$inrelease_file"`,
	}
	for _, value := range required {
		if !strings.Contains(script, value) {
			t.Fatalf("Tor helper lacks required authenticity gate %q", value)
		}
	}
	if strings.Contains(script, `curl -fsSL "$TOR_REPO_KEY_URL" | gpg`) {
		t.Fatal("Tor helper still pipes an unverified remote key into the system keyring")
	}
	if !strings.Contains(script, `Official Tor Project repository is configured; re-authenticating it.`) {
		t.Fatal("Tor helper does not re-authenticate an already configured repository")
	}
	fingerprintIndex := strings.Index(script, `if [[ "$imported_fingerprint" != "$TOR_REPO_KEY_FINGERPRINT" ]]`)
	signatureIndex := strings.Index(script, `gpgv --keyring "$keyring_file" "$inrelease_file"`)
	installIndex := strings.Index(script, `install -o root -g root -m 0644 "$keyring_file" "$TOR_KEYRING"`)
	if fingerprintIndex < 0 || signatureIndex <= fingerprintIndex || installIndex <= signatureIndex {
		t.Fatal("Tor helper installs the keyring before fingerprint and repository-signature validation")
	}
}
