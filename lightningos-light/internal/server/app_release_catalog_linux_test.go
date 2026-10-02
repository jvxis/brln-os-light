//go:build linux

package server

import (
	"os/exec"
	"strings"
	"testing"
)

func TestAppUpgradeHelperSelectsSameImmutableSourceCatalog(t *testing.T) {
	start := strings.Index(embeddedAppUpgradeScript, "release_core=")
	end := strings.Index(embeddedAppUpgradeScript, "\nPRIVILEGED_BROKER=")
	if start < 0 || end <= start {
		t.Fatal("helper catalog selection block missing")
	}
	for _, test := range []struct{ version, repository string }{
		{"", appLegacyCatalog}, {"0.5.33-Beta", appLegacyCatalog},
		{"0.5.40-Beta", appLegacyCatalog}, {"0.5.41-Beta", appModernCatalog},
		{"0.5.100", appModernCatalog}, {"1.0.0", appModernCatalog},
	} {
		t.Run(test.version, func(t *testing.T) {
			script := "set -euo pipefail\nVERSION=$1\nCUTOVER_ONLY_MODE=\nREPO_URL=https://github.com/" + appLegacyCatalog + ".git\nRELEASE_TAG_API_BASE=https://api.github.com/repos/" + appLegacyCatalog + "/releases/tags\n" + embeddedAppUpgradeScript[start:end] + "\nprintf '%s\\n%s\\n' \"$REPO_URL\" \"$RELEASE_TAG_API_BASE\"\n"
			output, err := exec.Command("bash", "-c", script, "--", test.version).CombinedOutput()
			if err != nil {
				t.Fatalf("helper routing failed: %v: %s", err, output)
			}
			want := "https://github.com/" + test.repository + ".git\nhttps://api.github.com/repos/" + test.repository + "/releases/tags\n"
			if string(output) != want {
				t.Fatalf("source and attestation repository mismatch: %s", output)
			}
		})
	}
}
