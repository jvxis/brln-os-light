package server

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNodeToolchainSecurityFixtures(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("APT/permission behavior requires Linux")
	}
	output, err := exec.Command("bash", filepath.Join("..", "..", "scripts", "tests", "node-toolchain-test.sh")).CombinedOutput()
	if err != nil {
		t.Fatalf("Node fixtures: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func TestNodeVersionDiagnostics(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Node preparation shell diagnostics require Linux Bash")
	}
	output, err := exec.Command("bash", filepath.Join("..", "..", "scripts", "tests", "node-version-diagnostics-test.sh")).CombinedOutput()
	if err != nil {
		t.Fatalf("Node version diagnostics: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func TestNodeBuildCompatibility(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Node build compatibility fixtures require Linux Bash")
	}
	output, err := exec.Command("bash", filepath.Join("..", "..", "scripts", "tests", "node-build-compatibility-test.sh")).CombinedOutput()
	if err != nil {
		t.Fatalf("Node compatibility: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func TestNodeRecoveryInventory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Recovery inventory fixture requires the installer Python environment")
	}
	output, err := exec.Command("python3", filepath.Join("..", "..", "scripts", "tests", "node-recovery-inventory-test.py")).CombinedOutput()
	if err != nil {
		t.Fatalf("Node recovery inventory: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func TestAppUpgradePreparesNodeBeforeIdentityAndBuild(t *testing.T) {
	script := embeddedAppUpgradeScript
	authenticated := strings.Index(script, `print_ok "Using authenticated project directory: $project_dir"`)
	prepare := strings.Index(script, "lightningos_prepare_node ||")
	identity := strings.Index(script, "\nnormalize_legacy_manager_identity\n")
	if authenticated < 0 || prepare <= authenticated || identity <= prepare {
		t.Fatal("prepare Node only after release authentication and before identity/build mutations")
	}
	if strings.Contains(script, "Required command missing: npm") || strings.Contains(script, "resolve_bin npm") {
		t.Fatal("the updater must prepare global npm before selecting its explicit path")
	}
}
