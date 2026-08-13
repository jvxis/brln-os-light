//go:build linux

package privileged

import (
	"context"
	"os"
	"testing"
)

func TestLNDUpgradeManagerRejectsTamperedHelperBeforeExecution(t *testing.T) {
	trusted, err := os.ReadFile("../server/assets/upgrade-lnd.sh")
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	manager := NewNativeLNDUpgradeManager(runner)
	params := LNDUpgradeStartParams{Version: "0.21.1-beta", HelperContent: string(trusted), VerifyOnly: true}
	state, err := manager.Start(context.Background(), params, true)
	if err != nil || state.Status != "validated" || state.Unit != lndVerifyUnit || len(runner.commands) != 0 {
		t.Fatalf("trusted helper dry-run failed: state=%#v err=%v commands=%#v", state, err, runner.commands)
	}

	params.HelperContent += "\n# tampered\n"
	if _, err := manager.Start(context.Background(), params, true); err == nil || len(runner.commands) != 0 {
		t.Fatalf("tampered helper was not rejected before execution: err=%v commands=%#v", err, runner.commands)
	}
}
