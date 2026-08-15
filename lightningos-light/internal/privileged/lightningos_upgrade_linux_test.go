//go:build linux

package privileged

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestLightningOSUpgradeManagerRejectsTamperedHelperBeforeExecution(t *testing.T) {
	trusted, err := os.ReadFile("../server/assets/upgrade-app.sh")
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	manager := NewNativeLightningOSUpgradeManager(runner)
	params := LightningOSUpgradeStartParams{
		Version: "0.5.3-beta", Tag: "0.5.3-Beta", Commit: strings.Repeat("a", 40), HelperContent: string(trusted), VerifyOnly: true,
	}
	state, err := manager.Start(context.Background(), params, true)
	if err != nil || state.Status != "validated" || state.Unit != lightningOSVerifyUnit || len(runner.commands) != 0 {
		t.Fatalf("trusted LightningOS helper dry-run failed: state=%#v err=%v commands=%#v", state, err, runner.commands)
	}
	params.HelperContent += "\n# tampered\n"
	if _, err := manager.Start(context.Background(), params, true); err == nil || len(runner.commands) != 0 {
		t.Fatalf("tampered LightningOS helper was not rejected before execution: err=%v commands=%#v", err, runner.commands)
	}
}
