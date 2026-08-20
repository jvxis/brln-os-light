package privileged

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type bitcoinRollbackCaptureCommand struct {
	path string
	args []string
}

type bitcoinRollbackCaptureRunner struct {
	commitOutput  string
	commitErr     error
	inspectOutput string
	inspectErr    error
	commands      []bitcoinRollbackCaptureCommand
}

func (runner *bitcoinRollbackCaptureRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, bitcoinRollbackCaptureCommand{path: path, args: append([]string(nil), args...)})
	if len(args) > 0 && args[0] == "commit" {
		return runner.commitOutput, runner.commitErr
	}
	if len(args) > 1 && args[0] == "image" && args[1] == "inspect" {
		return runner.inspectOutput, runner.inspectErr
	}
	return "", errors.New("unexpected command")
}

func TestCaptureLegacyBitcoinRollbackImageUsesExplicitInspect(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	rollbackImageID := "sha256:" + strings.Repeat("b", 64)
	runner := &bitcoinRollbackCaptureRunner{
		commitOutput:  "Docker informational output\n" + rollbackImageID + "\n",
		inspectOutput: "Docker informational output\n" + rollbackImageID + "\n",
	}
	manager := &ComposeAppManager{Runner: runner}

	if err := manager.captureLegacyBitcoinRollbackImage(context.Background(), containerID); err != nil {
		t.Fatal(err)
	}
	want := []bitcoinRollbackCaptureCommand{
		{path: dockerPath, args: []string{"commit", "--pause=false", containerID, bitcoinCoreLegacyRollbackImage}},
		{path: dockerPath, args: []string{"image", "inspect", "--format", "{{.Id}}", bitcoinCoreLegacyRollbackImage}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands=%#v, want %#v", runner.commands, want)
	}
}

func TestCaptureLegacyBitcoinRollbackImageRejectsCommitFailure(t *testing.T) {
	runner := &bitcoinRollbackCaptureRunner{commitErr: errors.New("simulated commit failure")}
	manager := &ComposeAppManager{Runner: runner}

	err := manager.captureLegacyBitcoinRollbackImage(context.Background(), strings.Repeat("a", 64))
	assertBitcoinRollbackCaptureError(t, err)
	if len(runner.commands) != 1 || runner.commands[0].args[0] != "commit" {
		t.Fatalf("unexpected commands after commit failure: %#v", runner.commands)
	}
}

func TestCaptureLegacyBitcoinRollbackImageRejectsUnverifiedImage(t *testing.T) {
	runner := &bitcoinRollbackCaptureRunner{
		commitOutput:  "commit completed",
		inspectOutput: "not-an-image-id",
	}
	manager := &ComposeAppManager{Runner: runner}

	err := manager.captureLegacyBitcoinRollbackImage(context.Background(), strings.Repeat("a", 64))
	assertBitcoinRollbackCaptureError(t, err)
	if len(runner.commands) != 2 || runner.commands[1].args[0] != "image" {
		t.Fatalf("rollback image was not inspected: %#v", runner.commands)
	}
}

func TestInspectedDockerImageIDRejectsAmbiguousOutput(t *testing.T) {
	output := "sha256:" + strings.Repeat("a", 64) + "\nsha256:" + strings.Repeat("b", 64) + "\n"
	if imageID := inspectedDockerImageID(output); imageID != "" {
		t.Fatalf("ambiguous image IDs were accepted: %q", imageID)
	}
}

func TestBitcoinLegacyMigrationFailureReasonUsesOnlySafeCategories(t *testing.T) {
	tests := []struct {
		cause    error
		wantCode string
	}{
		{cause: errBitcoinLegacyNetworkCutover, wantCode: "network_cutover"},
		{cause: errBitcoinLegacyNetworkRemoval, wantCode: "network_removal"},
		{cause: errBitcoinLegacyStart, wantCode: "start"},
		{cause: errBitcoinLegacyRuntimeUnavailable, wantCode: "runtime_lookup"},
		{cause: errBitcoinLegacyRuntimeIdentity, wantCode: "runtime_identity"},
		{cause: errBitcoinLegacyRPCUnavailable, wantCode: "rpc_readiness"},
		{cause: errBitcoinLegacyMainnetUnverified, wantCode: "mainnet"},
		{cause: errBitcoinLegacyEndpoints, wantCode: "loopback_endpoints"},
		{cause: errors.New("rpcpassword=must-not-leak"), wantCode: "verification"},
	}
	for _, test := range tests {
		code, message := bitcoinLegacyMigrationFailureReason(test.cause)
		if code != test.wantCode || code == "" || message == "" {
			t.Fatalf("failure reason=%q/%q want code=%q", code, message, test.wantCode)
		}
		if strings.Contains(message, "must-not-leak") || strings.Contains(message, "rpcpassword") {
			t.Fatalf("unsafe cause reached the operator message: %q", message)
		}
	}
}

func assertBitcoinRollbackCaptureError(t *testing.T, err error) {
	t.Helper()
	var migrationErr *bitcoinLegacyMigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Code != "bitcoin_legacy_rollback_capture_failed" {
		t.Fatalf("unexpected capture error: %v", err)
	}
}
