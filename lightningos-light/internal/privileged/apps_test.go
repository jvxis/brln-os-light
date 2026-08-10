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
}

func (runner *composeRecordingRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	if path == dockerPath && reflect.DeepEqual(args, []string{"compose", "version"}) {
		if runner.standalone {
			return "", errors.New("compose plugin unavailable")
		}
		return "Docker Compose version v2", nil
	}
	if path == dockerComposePath && reflect.DeepEqual(args, []string{"version"}) {
		return "docker-compose version 1", nil
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
	return "", nil
}

func TestComposeAppLifecycleUsesValidatedSnapshotAndFixedCommand(t *testing.T) {
	appsRoot, validEnv := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if runner.commands[0].path != dockerPath || !reflect.DeepEqual(runner.commands[0].args, []string{"compose", "version"}) {
		t.Fatalf("unexpected compose probe: %#v", runner.commands[0])
	}
	lifecycle := runner.commands[1]
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

func TestComposeAppLifecycleSupportsFixedStandaloneBinary(t *testing.T) {
	appsRoot, _ := writeTestCPUMinerApp(t)
	runner := &composeRecordingRunner{standalone: true}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot, TempRoot: t.TempDir()}
	if err := manager.Lifecycle(context.Background(), "cpuminer", AppLifecycleStop, false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 || runner.commands[2].path != dockerComposePath || runner.commands[2].args[len(runner.commands[2].args)-1] != "stop" {
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
