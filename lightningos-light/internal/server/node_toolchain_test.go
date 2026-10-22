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
