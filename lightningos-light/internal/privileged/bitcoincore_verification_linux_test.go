//go:build linux

package privileged

import (
	"context"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

// Executes the real release verifier, deliberately failing before image build.
// Never enable privileged integration gates on a production host.
func TestBitcoinCoreReleaseVerificationFailurePreservesExistingImage(t *testing.T) {
	if os.Getenv("LIGHTNINGOS_BITCOIN_VERIFY_FAILURE_GATE") != "1" {
		t.Skip("requires explicitly owned disposable Linux/Docker laboratory")
	}
	marker, err := os.ReadFile("/etc/los-regtest/LAB_ONLY")
	if err != nil || strings.TrimSpace(string(marker)) != "LOS_REGTEST_ONLY" || os.Geteuid() != 0 {
		t.Fatal("verification failure gate requires root in the marked disposable lab")
	}
	runner := &ExecCommandRunner{}
	manager := NewComposeAppManager(runner)
	attestation := manager.bitcoinCoreImageAttestationPath()
	before, err := os.ReadFile(attestation)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	imageBefore, err := runner.Run(ctx, dockerPath, "image", "inspect", "--format", "{{.Id}}", appmanifest.BitcoinCoreImage)
	if err != nil {
		t.Fatal("verified image must be prepared first")
	}
	artifact, err := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"checksum", "signature"} {
		t.Run(mode, func(t *testing.T) {
			testArtifact := artifact
			if mode == "checksum" {
				testArtifact.ArchiveSHA256 = strings.Repeat("0", 64)
			}
			script := bitcoinCoreImageBuildScript(testArtifact, attestation)
			if mode == "signature" {
				lines := strings.Split(script, "\n")
				for i, line := range lines {
					if strings.HasPrefix(line, "/usr/bin/curl ") && strings.Contains(line, "/SHA256SUMS.asc") {
						lines[i] = `printf 'deliberately invalid fixture signature\n' > "$work/SHA256SUMS.asc"`
					}
				}
				script = strings.Join(lines, "\n")
			}
			if _, err := runner.Run(ctx, "/bin/sh", "-c", script); err == nil {
				t.Fatal("invalid release verification succeeded")
			}
			after, err := os.ReadFile(attestation)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed verification replaced the trusted attestation")
			}
			imageAfter, err := runner.Run(ctx, dockerPath, "image", "inspect", "--format", "{{.Id}}", appmanifest.BitcoinCoreImage)
			if err != nil || imageBefore != imageAfter {
				t.Fatal("failed verification changed the existing image")
			}
		})
	}
}
