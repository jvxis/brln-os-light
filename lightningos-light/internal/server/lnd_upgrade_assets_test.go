package server

import (
	"strings"
	"testing"
)

func TestLNDUpgradeAuthenticatesReleaseBeforeExtraction(t *testing.T) {
	script := embeddedUpgradeScript
	required := []string{
		`MIN_REQUIRED_SIGNATURES=5`,
		`/v${VERSION}/scripts/keys/${username}.asc`,
		`--import "$key_path"`,
		`"VALIDSIG"`,
		`actual_hash=$(sha256sum`,
		`--no-same-owner --no-same-permissions`,
		`--verify-only`,
	}
	for _, value := range required {
		if !strings.Contains(script, value) {
			t.Fatalf("LND upgrade helper lacks required release gate %q", value)
		}
	}
	for _, forbidden := range []string{"LND_UPGRADE_URL", "--url)", "curl -fsSL \"$URL\" -o \"$tmp_dir/lnd.tar.gz\""} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("LND upgrade helper retains caller-controlled or unauthenticated path %q", forbidden)
		}
	}
	signatureIndex := strings.Index(script, `if [[ "$valid_signatures" -lt "$MIN_REQUIRED_SIGNATURES" ]]`)
	hashIndex := strings.Index(script, `actual_hash=$(sha256sum`)
	extractIndex := strings.Index(script, `tar --no-same-owner --no-same-permissions -xzf`)
	if signatureIndex < 0 || hashIndex <= signatureIndex || extractIndex <= hashIndex {
		t.Fatalf("LND helper does not authenticate manifest and checksum before extraction")
	}
}
