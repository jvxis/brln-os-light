//go:build linux

package privileged

import (
	"context"
	"os"
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
