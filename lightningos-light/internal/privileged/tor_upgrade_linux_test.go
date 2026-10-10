//go:build linux

package privileged

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTorUpgradeManagerRejectsTamperedHelperBeforeExecution(t *testing.T) {
	trusted, err := os.ReadFile("../server/assets/check-tor-update.sh")
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	manager := NewNativeTorUpgradeManager(runner)
	params := TorUpgradeStartParams{HelperContent: string(trusted), VerifyOnly: true}
	state, err := manager.Start(context.Background(), params, true)
	if err != nil || state.Status != "validated" || state.Unit != torVerifyUnit || len(runner.commands) != 0 {
		t.Fatalf("trusted Tor helper dry-run failed: state=%#v err=%v commands=%#v", state, err, runner.commands)
	}
	params.HelperContent += "\n# tampered\n"
	if _, err := manager.Start(context.Background(), params, true); err == nil || len(runner.commands) != 0 {
		t.Fatalf("tampered Tor helper was not rejected before execution: err=%v commands=%#v", err, runner.commands)
	}
}

func TestTorRecoveryFileInstallationPreservesRootBoundary(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-only installation fixture")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "helper")
	if err := installTrustedTorFile(path, lndOnionHelper, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installTrustedTorFile(path, lndOnionHelper, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != lndOnionHelper {
		t.Fatal("installed helper differs from embedded payload")
	}
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := installTrustedTorFile(path, "replacement", 0o755); err == nil {
		t.Fatal("replaced a writable helper")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "unrelated")
	if err := os.WriteFile(target, []byte("preserve"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := installTrustedTorFile(path, "replacement", 0o755); err == nil {
		t.Fatal("followed a helper symlink")
	}
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "preserve" {
		t.Fatal("modified unrelated target")
	}
}
