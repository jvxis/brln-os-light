package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

type composeRecordingRunner struct {
	commands        []recordedCommand
	composeSnapshot string
	envSnapshot     string
	standalone      bool
	runningServices string
	containerID     string
	cpuPercent      string
	statsErr        error
	imageErr        error
	hook            func(path string, args []string) (string, error, bool)
}

func (runner *composeRecordingRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	if runner.hook != nil {
		if output, err, handled := runner.hook(path, args); handled {
			return output, err
		}
	}
	if path == dockerPath && reflect.DeepEqual(args, []string{"compose", "version"}) {
		if runner.standalone {
			return "", errors.New("compose plugin unavailable")
		}
		return "Docker Compose version v2", nil
	}
	if path == dockerComposePath && reflect.DeepEqual(args, []string{"version"}) {
		return "docker-compose version 1", nil
	}
	if path == dockerPath && len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
		return "", runner.imageErr
	}
	for index, arg := range args {
		if arg == "-f" && index+1 < len(args) {
			raw, _ := os.ReadFile(args[index+1])
			runner.composeSnapshot = string(raw)
		}
		if arg == "--env-file" && index+1 < len(args) {
			raw, _ := os.ReadFile(args[index+1])
			runner.envSnapshot = string(raw)
		}
	}
	if hasArgsSuffix(args, "ps", "--services", "--filter", "status=running") {
		return runner.runningServices, nil
	}
	if hasArgsSuffix(args, "ps", "-q", appmanifest.CPUMinerID) {
		return runner.containerID, nil
	}
	if path == dockerPath && len(args) == 5 && reflect.DeepEqual(args[:4], []string{"stats", "--no-stream", "--format", "{{.CPUPerc}}"}) {
		return runner.cpuPercent, runner.statsErr
	}
	return "", nil
}

func TestComposeAppEnsureDockerRuntimeUsesFixedCommands(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.EnsureDockerRuntime(context.Background(), false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	want := []recordedCommand{
		{path: dockerPath, args: []string{"info"}},
		{path: dockerPath, args: []string{"compose", "version"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands=%#v want=%#v", runner.commands, want)
	}
}

func TestComposeAppEnsureDockerRuntimeStartsInstalledDaemon(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"info"}) {
			return "", errors.New("inactive"), true
		}
		if path == systemctlPath && reflect.DeepEqual(args, []string{"is-active", "docker"}) {
			return "inactive\n", errors.New("inactive"), true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.EnsureDockerRuntime(context.Background(), false)
	if err != nil || state.Status != "starting" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 3 || runner.commands[2].path != systemctlPath || !reflect.DeepEqual(runner.commands[2].args, []string{"start", "--no-block", "docker"}) {
		t.Fatalf("unexpected runtime command sequence: %#v", runner.commands)
	}
}

func TestComposeAppEnsureDockerRuntimeDryRunExecutesNothing(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.EnsureDockerRuntime(context.Background(), true)
	if err != nil || state.Status != "validated" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry run executed commands: %#v", runner.commands)
	}
}

func TestComposeAppPrepareImageSchedulesFixedTransientPull(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
			return "", errors.New("missing"), true
		}
		if path == systemctlPath && len(args) > 0 && args[0] == "show" {
			return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found"), true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.PrepareImage(context.Background(), "cpuminer", appmanifest.CPUMinerImageBaseline, false)
	if err != nil || state.Status != "preparing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands=%#v", runner.commands)
	}
	want := recordedCommand{path: systemdRunPath, args: []string{
		"--quiet", "--collect", "--unit=lightningos-cpuminer-image-baseline",
		"--property=Type=exec", "--property=RuntimeMaxSec=10min",
		dockerPath, "pull", "jvx1971/cpu-lottery-miner:v1",
	}}
	if !reflect.DeepEqual(runner.commands[2], want) {
		t.Fatalf("pull command=%#v want=%#v", runner.commands[2], want)
	}
}

func TestComposeAppPrepareImageReturnsCachedOrInProgressState(t *testing.T) {
	for _, test := range []struct {
		name       string
		inspectErr error
		show       string
		want       string
		wantCalls  int
	}{
		{name: "ready", want: "ready", wantCalls: 1},
		{name: "preparing", inspectErr: errors.New("missing"), show: "LoadState=loaded\nActiveState=active\nSubState=running\n", want: "preparing", wantCalls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
				if path == dockerPath && len(args) == 3 && args[0] == "image" && args[1] == "inspect" {
					return "", test.inspectErr, true
				}
				if path == systemctlPath {
					return test.show, nil, true
				}
				return "", nil, false
			}}
			manager := &ComposeAppManager{Runner: runner}
			state, err := manager.PrepareImage(context.Background(), "cpuminer", appmanifest.CPUMinerImageFastLatest, false)
			if err != nil || state.Status != test.want || len(runner.commands) != test.wantCalls {
				t.Fatalf("state=%#v err=%v commands=%#v", state, err, runner.commands)
			}
		})
	}
}

func TestComposeAppImageStatusAndProbeUseAllowlistedImage(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.ImageStatus(context.Background(), "cpuminer", appmanifest.CPUMinerImageFastPinned)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	probe, err := manager.ProbeImage(context.Background(), "cpuminer", appmanifest.CPUMinerImageFastPinned, false)
	if err != nil || !probe.Runnable {
		t.Fatalf("probe/error=%#v/%v", probe, err)
	}
	image := "cniweb/cpuminer-opt@sha256:8aba97834d6a6e1946b2a61c8939eee8907b7be97d8e77c1174f66579d5bd90b"
	if len(runner.commands) != 3 || !reflect.DeepEqual(runner.commands[2], recordedCommand{path: dockerPath, args: []string{"run", "--rm", image, "cpuminer", "--algo", "sha256d", "--benchmark", "--time-limit", "2"}}) {
		t.Fatalf("unexpected image commands: %#v", runner.commands)
	}
}

func TestComposeAppImageOperationsRejectUntrustedInputsBeforeCommand(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner}
	if _, err := manager.PrepareImage(context.Background(), "mempool", appmanifest.CPUMinerImageBaseline, false); err == nil {
		t.Fatal("expected unknown app to fail")
	}
	if _, err := manager.ImageStatus(context.Background(), "cpuminer", "../../evil"); err == nil {
		t.Fatal("expected unknown image variant to fail")
	}
	if _, err := manager.ProbeImage(context.Background(), "cpuminer", "latest;reboot", false); err == nil {
		t.Fatal("expected injected image variant to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("rejected image input executed commands: %#v", runner.commands)
	}
}

func hasArgsSuffix(args []string, suffix ...string) bool {
	return len(args) >= len(suffix) && reflect.DeepEqual(args[len(args)-len(suffix):], suffix)
}

func TestComposeAppLifecycleUsesValidatedSnapshotAndFixedCommand(t *testing.T) {
	appsRoot, validEnv := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if runner.commands[0].path != dockerPath || !reflect.DeepEqual(runner.commands[0].args, []string{"image", "inspect", "jvx1971/cpu-lottery-miner:v1"}) {
		t.Fatalf("unexpected image probe: %#v", runner.commands[0])
	}
	if runner.commands[1].path != dockerPath || !reflect.DeepEqual(runner.commands[1].args, []string{"compose", "version"}) {
		t.Fatalf("unexpected compose probe: %#v", runner.commands[1])
	}
	lifecycle := runner.commands[2]
	if lifecycle.path != dockerPath || len(lifecycle.args) < 11 || lifecycle.args[0] != "compose" || lifecycle.args[len(lifecycle.args)-2] != "up" || lifecycle.args[len(lifecycle.args)-1] != "-d" {
		t.Fatalf("unexpected lifecycle command: %#v", lifecycle)
	}
	if runner.composeSnapshot != appmanifest.CPUMinerCompose() || runner.envSnapshot != validEnv {
		t.Fatal("broker did not execute the validated manifest snapshot")
	}
	for _, arg := range lifecycle.args {
		if strings.Contains(arg, appsRoot) {
			t.Fatalf("manager-writable path reached Docker command: %q", arg)
		}
	}
}

func TestComposeAppLifecycleStartRequiresLocalImage(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{imageErr: errors.New("missing")}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStart, false); err == nil {
		t.Fatal("expected missing local image to fail")
	}
	if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].args, []string{"image", "inspect", "jvx1971/cpu-lottery-miner:v1"}) {
		t.Fatalf("missing image reached Compose: %#v", runner.commands)
	}
}

func TestComposeAppLifecycleSupportsFixedStandaloneBinary(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{standalone: true}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStop, false); err != nil {
		t.Fatal(err)
	}
	wantTail := []string{"stop", "--timeout", "2"}
	if len(runner.commands) != 3 {
		t.Fatalf("unexpected standalone command sequence: %#v", runner.commands)
	}
	gotArgs := runner.commands[2].args
	if runner.commands[2].path != dockerComposePath || len(gotArgs) < len(wantTail) || !reflect.DeepEqual(gotArgs[len(gotArgs)-len(wantTail):], wantTail) {
		t.Fatalf("unexpected standalone command sequence: %#v", runner.commands)
	}
}

func TestComposeAppLifecycleDryRunValidatesWithoutCommand(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStart, true); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry run executed commands: %#v", runner.commands)
	}
}

func TestComposeAppLifecycleRejectsUntrustedInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		appID  string
		action AppLifecycleAction
	}{
		{name: "unknown app", appID: "mempool", action: AppLifecycleStart},
		{name: "unknown action", appID: "cpuminer", action: "exec"},
		{name: "modified compose", appID: "cpuminer", action: AppLifecycleStart, mutate: func(t *testing.T, root string) {
			t.Helper()
			path := filepath.Join(root, "cpuminer", appmanifest.CPUMinerComposeFile)
			if err := os.WriteFile(path, []byte(appmanifest.CPUMinerCompose()+"    privileged: true\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "modified environment", appID: "cpuminer", action: AppLifecycleStart, mutate: func(t *testing.T, root string) {
			t.Helper()
			path := filepath.Join(root, "cpuminer", appmanifest.CPUMinerEnvFile)
			if err := os.WriteFile(path, []byte("CPUMINER_IMAGE=evil/root:latest\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked compose", appID: "cpuminer", action: AppLifecycleStart, mutate: func(t *testing.T, root string) {
			t.Helper()
			path := filepath.Join(root, "cpuminer", appmanifest.CPUMinerComposeFile)
			target := filepath.Join(root, "copy.yaml")
			if err := os.WriteFile(target, []byte(appmanifest.CPUMinerCompose()), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			appsRoot, _ := writeTestCPUMinerApp(t)
			if test.mutate != nil {
				test.mutate(t, appsRoot)
			}
			runner := &composeRecordingRunner{}
			manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
			if err := manager.Lifecycle(context.Background(), test.appID, test.action, true); err == nil {
				t.Fatal("expected validation error")
			}
			if len(runner.commands) != 0 {
				t.Fatalf("rejected request executed commands: %#v", runner.commands)
			}
		})
	}
}

func TestComposeAppRemoveUsesValidatedSnapshotAndFixedCommand(t *testing.T) {
	appsRoot, validEnv := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Remove(context.Background(), "cpuminer", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 || !hasArgsSuffix(runner.commands[1].args, "down", "--remove-orphans", "--timeout", "2") {
		t.Fatalf("unexpected remove commands: %#v", runner.commands)
	}
	if runner.composeSnapshot != appmanifest.CPUMinerCompose() || runner.envSnapshot != validEnv {
		t.Fatal("broker did not remove through the validated manifest snapshot")
	}
	for _, arg := range runner.commands[1].args {
		if strings.Contains(arg, appsRoot) {
			t.Fatalf("manager-writable path reached Docker command: %q", arg)
		}
	}
}

func TestComposeAppRemoveDryRunAndTamperFailBeforeCommand(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
	if err := manager.Remove(context.Background(), "cpuminer", true); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("dry run executed commands: %#v", runner.commands)
	}
	path := filepath.Join(appsRoot, appmanifest.CPUMinerID, appmanifest.CPUMinerEnvFile)
	if err := os.WriteFile(path, []byte("CPUMINER_IMAGE=evil/root:latest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), "cpuminer", false); err == nil {
		t.Fatal("expected tampered environment to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("tampered remove executed commands: %#v", runner.commands)
	}
}

func TestComposeAppInspectStoppedUsesValidatedSnapshot(t *testing.T) {
	appsRoot, validEnv := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	inspection, err := manager.Inspect(context.Background(), "cpuminer")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "stopped" || inspection.CPUPercentRaw != 0 || len(runner.commands) != 2 {
		t.Fatalf("inspection=%#v commands=%#v", inspection, runner.commands)
	}
	if !hasArgsSuffix(runner.commands[1].args, "ps", "--services", "--filter", "status=running") {
		t.Fatalf("unexpected status command: %#v", runner.commands[1])
	}
	if runner.composeSnapshot != appmanifest.CPUMinerCompose() || runner.envSnapshot != validEnv {
		t.Fatal("broker did not inspect the validated manifest snapshot")
	}
	for _, arg := range runner.commands[1].args {
		if strings.Contains(arg, appsRoot) {
			t.Fatalf("manager-writable path reached Docker command: %q", arg)
		}
	}
}

func TestComposeAppInspectRunningReturnsDockerCPU(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	containerID := strings.Repeat("a", 64)
	runner := &composeRecordingRunner{
		runningServices: "cpuminer\n",
		containerID:     containerID + "\n",
		cpuPercent:      "125,75%\n",
	}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	inspection, err := manager.Inspect(context.Background(), "cpuminer")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "running" || inspection.CPUPercentRaw != 125.75 || len(runner.commands) != 4 {
		t.Fatalf("inspection=%#v commands=%#v", inspection, runner.commands)
	}
	wantStats := recordedCommand{path: dockerPath, args: []string{"stats", "--no-stream", "--format", "{{.CPUPerc}}", containerID}}
	if !reflect.DeepEqual(runner.commands[3], wantStats) {
		t.Fatalf("stats command=%#v want=%#v", runner.commands[3], wantStats)
	}
}

func TestComposeAppInspectDegradesStatsFailureToZero(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{
		runningServices: "cpuminer\n",
		containerID:     strings.Repeat("b", 64),
		statsErr:        errors.New("stats unavailable"),
	}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	inspection, err := manager.Inspect(context.Background(), "cpuminer")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != "running" || inspection.CPUPercentRaw != 0 {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}
}

func TestComposeAppInspectRejectsInvalidContainerIDBeforeStats(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{runningServices: "cpuminer\n", containerID: "abc;reboot\n"}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if _, err := manager.Inspect(context.Background(), "cpuminer"); err == nil {
		t.Fatal("expected invalid container ID to fail")
	}
	if len(runner.commands) != 3 {
		t.Fatalf("invalid ID reached stats command: %#v", runner.commands)
	}
}

func TestComposeAppInspectRejectsTamperedManifestBeforeCommand(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	path := filepath.Join(appsRoot, appmanifest.CPUMinerID, appmanifest.CPUMinerComposeFile)
	if err := os.WriteFile(path, []byte(appmanifest.CPUMinerCompose()+"    privileged: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if _, err := manager.Inspect(context.Background(), "cpuminer"); err == nil {
		t.Fatal("expected tampered manifest to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("rejected manifest executed commands: %#v", runner.commands)
	}
}

func TestParseDockerContainerID(t *testing.T) {
	valid := strings.Repeat("c", 64)
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: valid + "\n", want: valid},
		{input: strings.Repeat("d", 12), want: strings.Repeat("d", 12)},
		{input: "ABCDEF123456"},
		{input: "abc;reboot"},
		{input: valid + "\n" + valid},
	} {
		if got := parseDockerContainerID(test.input); got != test.want {
			t.Fatalf("parseDockerContainerID(%q)=%q want=%q", test.input, got, test.want)
		}
	}
}

func TestParseDockerCPUPercent(t *testing.T) {
	for _, test := range []struct {
		input string
		want  float64
	}{
		{input: "0.00%", want: 0},
		{input: "125.75%\n", want: 125.75},
		{input: "7,5%", want: 7.5},
		{input: "NaN%", want: 0},
		{input: "1%; reboot", want: 0},
	} {
		if got := parseDockerCPUPercent(test.input); got != test.want {
			t.Fatalf("parseDockerCPUPercent(%q)=%v want=%v", test.input, got, test.want)
		}
	}
}

func writeTestCPUMinerApp(t *testing.T) (string, string) {
	t.Helper()
	appsRoot := t.TempDir()
	appRoot := filepath.Join(appsRoot, appmanifest.CPUMinerID)
	if err := os.MkdirAll(appRoot, 0750); err != nil {
		t.Fatal(err)
	}
	validEnv := "CPUMINER_IMAGE=jvx1971/cpu-lottery-miner:v1\nPOOL_MODE=brln\nSTRATUM_HOST=btcpool.br-ln.com\nSTRATUM_PORT=3332\nMINING_ADDRESS=bc1qexampleaddress000000000000000000000000\nWORKER_NAME=cpu-lottery\nTHREADS=1\n"
	if err := os.WriteFile(filepath.Join(appRoot, appmanifest.CPUMinerComposeFile), []byte(appmanifest.CPUMinerCompose()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, appmanifest.CPUMinerEnvFile), []byte(validEnv), 0600); err != nil {
		t.Fatal(err)
	}
	return appsRoot, validEnv
}
