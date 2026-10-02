package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGoToolchainPreparationFixtures(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux permissions/flock/tar fixtures; also runnable directly with bash scripts/tests/go-toolchain-test.sh")
	}
	script := filepath.Join("..", "..", "scripts", "tests", "go-toolchain-test.sh")
	output, err := exec.Command("bash", script).CombinedOutput()
	if err != nil {
		t.Fatalf("Go preparation fixtures: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func TestAppUpgradePreparesGoBeforeMutationsAndBuilds(t *testing.T) {
	script := embeddedAppUpgradeScript
	authenticated := strings.Index(script, `print_ok "Using authenticated project directory: $project_dir"`)
	prepare := strings.Index(script, `lightningos_prepare_go "$project_dir"`)
	identity := strings.Index(script, "\nnormalize_legacy_manager_identity\n")
	build := strings.Index(script, `print_step "Building manager binary"`)
	verifyOnly := strings.Index(script, "LightningOS release source verified; no application files or services were changed.")
	if verifyOnly < 0 || authenticated <= verifyOnly || prepare <= authenticated || identity <= prepare || build <= identity {
		t.Fatal("Go preparation must follow source authentication and verify-only exit, before identity migration and builds")
	}
	if strings.Contains(script, `Required command missing: go`) {
		t.Fatal("an existing Go must not be required before preparation")
	}
	for _, asset := range []string{"scripts/install-artifact-verification.sh", "scripts/prepare-go-toolchain.sh", "scripts/go-toolchain.conf"} {
		if !strings.Contains(script, asset) {
			t.Fatalf("missing authenticated preparation asset %s", asset)
		}
	}
}

func TestBridgeReleaseRetainsOldGoRequirement(t *testing.T) {
	version, err := os.ReadFile(filepath.Join("..", "..", "ui", "public", "version.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(version)), "0.5.40") {
		t.Skip("bridge-only constraint; subsequent releases can raise their Go requirement")
	}
	module, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	canonical := strings.ReplaceAll(string(module), "\r\n", "\n")
	if !strings.Contains(canonical, "\ngo 1.24\n") || !strings.Contains(canonical, "\ntoolchain go1.24.12\n") {
		t.Fatal("0.5.40 must remain installable with the Go version provided by the legacy installers")
	}
}
