package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

type mempoolTestFixture struct {
	manager        *ComposeAppManager
	runner         *composeRecordingRunner
	appRoot        string
	privilegedRoot string
	runtime        appmanifest.MempoolRuntime
}

func writeTestMempoolApp(t *testing.T) *mempoolTestFixture {
	t.Helper()
	appsRoot := filepath.Join(t.TempDir(), "apps")
	privilegedRoot := filepath.Join(t.TempDir(), "privileged-apps")
	appRoot := filepath.Join(appsRoot, appmanifest.MempoolID)
	if err := os.MkdirAll(appRoot, 0750); err != nil {
		t.Fatal(err)
	}
	runtimeValues := appmanifest.MempoolRuntime{
		BitcoinMode: appmanifest.MempoolBitcoinModeNative, Network: "bitcoin", BitcoinRPCUser: "mempool-rpc",
		BitcoinRPCPass: "rpc-secret", DBPassword: "db-secret", DBRootPassword: "db-root-secret",
	}
	env, err := appmanifest.MempoolRuntimeEnv(runtimeValues)
	if err != nil {
		t.Fatal(err)
	}
	compose, err := appmanifest.MempoolCompose(runtimeValues)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteTestFile(t, filepath.Join(appRoot, appmanifest.MempoolEnvFile), []byte(env), 0600)
	mustWriteTestFile(t, filepath.Join(appRoot, appmanifest.MempoolComposeFile), []byte(compose), 0600)
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"inspect", "--format={{.State.Running}}", "electrs"}) {
			return "true\n", nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"inspect", "--format={{if index .NetworkSettings.Networks \"electrs_default\"}}connected{{end}}", "electrs"}) {
			return "connected\n", nil, true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{
		Runner: runner, AppsRoot: appsRoot, PrivilegedAppsRoot: privilegedRoot,
		ElectrsRPCProbe: func(_ context.Context, endpoint, user, password, chain string) error {
			if endpoint != "http://127.0.0.1:8332/" || user != "mempool-rpc" || password != "rpc-secret" || chain != "main" {
				return errors.New("unexpected Mempool Bitcoin probe contract")
			}
			return nil
		},
	}
	return &mempoolTestFixture{manager: manager, runner: runner, appRoot: appRoot, privilegedRoot: privilegedRoot, runtime: runtimeValues}
}

func TestMempoolLifecycleUsesRootOnlyClosedSnapshot(t *testing.T) {
	fixture := writeTestMempoolApp(t)
	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.MempoolID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fixture.runner.composeSnapshot, appmanifest.MempoolFrontendImage) ||
		!strings.Contains(fixture.runner.composeSnapshot, `user: "1000:1000"`) ||
		!strings.Contains(fixture.runner.composeSnapshot, `CORE_RPC_HOST: "172.31.253.1"`) {
		t.Fatalf("unexpected execution manifest:\n%s", fixture.runner.composeSnapshot)
	}
	if !strings.Contains(fixture.runner.envSnapshot, "MEMPOOL_BITCOIN_RPC_PASS=rpc-secret") {
		t.Fatal("broker snapshot did not preserve the runtime credential")
	}
	for _, command := range fixture.runner.commands {
		for _, arg := range command.args {
			if strings.Contains(arg, fixture.appRoot) || strings.Contains(arg, "rpc-secret") || strings.Contains(arg, "db-secret") {
				t.Fatalf("manager path or secret reached command arguments: %q", arg)
			}
		}
	}
	snapshotRoot := filepath.Join(fixture.privilegedRoot, appmanifest.MempoolID)
	if runtime.GOOS != "windows" {
		for _, name := range []string{appmanifest.MempoolComposeFile, appmanifest.MempoolEnvFile} {
			info, err := os.Stat(filepath.Join(snapshotRoot, name))
			if err != nil || info.Mode().Perm()&007 != 0 {
				t.Fatalf("snapshot asset %s is exposed: mode/error=%v/%v", name, info.Mode(), err)
			}
		}
	}
	last := fixture.runner.commands[len(fixture.runner.commands)-1]
	if !hasArgsSuffix(last.args, "up", "-d") || !strings.Contains(strings.Join(last.args, " "), snapshotRoot) {
		t.Fatalf("unexpected lifecycle command: %#v", last)
	}
}

func TestMempoolDependencyFailuresStopBeforeCompose(t *testing.T) {
	fixture := writeTestMempoolApp(t)
	fixture.manager.ElectrsRPCProbe = func(context.Context, string, string, string, string) error {
		return errors.New("Mempool requires an unpruned Bitcoin Full Node")
	}
	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.MempoolID, AppLifecycleStart, false); err == nil || !strings.Contains(err.Error(), "unpruned") {
		t.Fatalf("unexpected full-node error: %v", err)
	}
	if len(fixture.runner.commands) != 0 {
		t.Fatalf("failed full-node gate reached Docker: %#v", fixture.runner.commands)
	}

	fixture = writeTestMempoolApp(t)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"inspect", "--format={{.State.Running}}", "electrs"}) {
			return "false\n", nil, true
		}
		return "", nil, false
	}
	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.MempoolID, AppLifecycleStart, false); err == nil || !strings.Contains(err.Error(), "Electrs") {
		t.Fatalf("unexpected Electrs error: %v", err)
	}
	if len(fixture.runner.commands) != 1 {
		t.Fatalf("missing Electrs gate reached additional commands: %#v", fixture.runner.commands)
	}
}

func TestMempoolDeclarationTamperingFailsBeforeCommand(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *mempoolTestFixture)
	}{
		{"compose", func(t *testing.T, fixture *mempoolTestFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.appRoot, appmanifest.MempoolComposeFile), []byte("services:\n  attacker:\n    privileged: true\n"), 0600)
		}},
		{"environment", func(t *testing.T, fixture *mempoolTestFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.appRoot, appmanifest.MempoolEnvFile), []byte("MEMPOOL_BITCOIN_MODE=remote\n"), 0600)
		}},
		{"unexpected asset", func(t *testing.T, fixture *mempoolTestFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.appRoot, "override.yaml"), []byte("privileged: true\n"), 0600)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := writeTestMempoolApp(t)
			test.mutate(t, fixture)
			if err := fixture.manager.Lifecycle(context.Background(), appmanifest.MempoolID, AppLifecycleStart, true); err == nil {
				t.Fatal("expected tampered declaration rejection")
			}
			if len(fixture.runner.commands) != 0 {
				t.Fatalf("tampered declaration reached command runner: %#v", fixture.runner.commands)
			}
		})
	}
}

func TestMempoolRemoveUsesCatalogVolumePolicy(t *testing.T) {
	fixture := writeTestMempoolApp(t)
	if err := fixture.manager.Remove(context.Background(), appmanifest.MempoolID, false); err != nil {
		t.Fatal(err)
	}
	last := fixture.runner.commands[len(fixture.runner.commands)-1]
	if !hasArgsSuffix(last.args, "down", "--remove-orphans", "--timeout", "60", "--volumes") {
		t.Fatalf("unexpected remove command: %#v", last)
	}
	if _, err := os.Stat(filepath.Join(fixture.privilegedRoot, appmanifest.MempoolID)); !os.IsNotExist(err) {
		t.Fatalf("execution snapshot was not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.appRoot, appmanifest.MempoolComposeFile)); err != nil {
		t.Fatalf("broker removed manager-owned declaration: %v", err)
	}
}

func TestMempoolImageProbeUsesClosedNonRootRuntime(t *testing.T) {
	for _, test := range []struct {
		variant appmanifest.AppImageVariant
		output  string
	}{
		{appmanifest.MempoolImageFrontend, "nginx version: nginx/1.27.0"},
		{appmanifest.MempoolImageBackend, "v24.13.0"},
		{appmanifest.MempoolImageDatabase, "mariadbd  Ver 10.11.18-MariaDB"},
	} {
		runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
			if path == dockerPath && len(args) > 0 && args[0] == "run" {
				return test.output, nil, true
			}
			return "", nil, false
		}}
		manager := &ComposeAppManager{Runner: runner}
		probe, err := manager.ProbeImage(context.Background(), appmanifest.MempoolID, test.variant, false)
		if err != nil || !probe.Runnable {
			t.Fatalf("variant %s probe/error=%#v/%v", test.variant, probe, err)
		}
		command := runner.commands[len(runner.commands)-1]
		joined := strings.Join(command.args, " ")
		expectedUser := "--user 1000:1000"
		if test.variant == appmanifest.MempoolImageDatabase {
			expectedUser = "--user 999:999"
		}
		for _, required := range []string{"--network none", "--read-only", expectedUser, "--cap-drop ALL", "no-new-privileges"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("probe command missing %q: %#v", required, command)
			}
		}
	}
}
