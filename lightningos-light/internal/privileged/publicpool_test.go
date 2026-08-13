package privileged

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func testPublicPoolManager(t *testing.T, runner CommandRunner) *NativePublicPoolManager {
	t.Helper()
	root := t.TempDir()
	snapshot := filepath.Join(root, "privileged", appmanifest.PublicPoolID)
	return &NativePublicPoolManager{Runner: runner, Paths: PublicPoolPaths{
		SnapshotRoot: snapshot, DataDir: filepath.Join(root, "apps-data", appmanifest.PublicPoolID, "db"),
		ComposePath: filepath.Join(snapshot, appmanifest.PublicPoolComposeFile), EnvPath: filepath.Join(snapshot, appmanifest.PublicPoolEnvFile),
		CaddyfilePath:     filepath.Join(snapshot, appmanifest.PublicPoolCaddyfile),
		LegacyComposePath: filepath.Join(root, "legacy", appmanifest.PublicPoolID, appmanifest.PublicPoolComposeFile),
	}}
}

func testPublicPoolRuntime() appmanifest.PublicPoolRuntime {
	return appmanifest.PublicPoolRuntime{BitcoinMode: appmanifest.PublicPoolBitcoinRemote, BitcoinRPCURL: "http://bitcoin.example", BitcoinRPCPort: 8332, BitcoinRPCUser: "rpcuser", BitcoinRPCPass: "rpcpass", BitcoinZMQHost: "tcp://bitcoin.example:28332"}
}

func TestNativePublicPoolEnsureCreatesClosedSnapshotAndPreservesData(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := testPublicPoolManager(t, runner)
	if err := os.MkdirAll(manager.Paths.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(manager.Paths.DataDir, "shares.db")
	if err := os.WriteFile(dataPath, []byte("persistent-pool-data"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := manager.Ensure(context.Background(), PublicPoolEnsureParams{Runtime: testPublicPoolRuntime()}, false)
	if err != nil || !state.Installed {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	compose, _ := os.ReadFile(manager.Paths.ComposePath)
	expected, _ := appmanifest.PublicPoolCompose(appmanifest.PublicPoolComposePaths{DataDir: manager.Paths.DataDir, CaddyfilePath: manager.Paths.CaddyfilePath}, appmanifest.PublicPoolBitcoinRemote)
	if string(compose) != expected {
		t.Fatal("broker snapshot does not match catalog")
	}
	if err := manager.Remove(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dataPath)
	if err != nil || string(data) != "persistent-pool-data" {
		t.Fatalf("persistent data changed: %q/%v", data, err)
	}
	if _, err := os.Stat(manager.Paths.SnapshotRoot); !os.IsNotExist(err) {
		t.Fatal("execution snapshot was not removed")
	}
}

func TestNativePublicPoolLifecycleRejectsTamperedSnapshotBeforeDocker(t *testing.T) {
	runner := &composeRecordingRunner{}
	manager := testPublicPoolManager(t, runner)
	if _, err := manager.Ensure(context.Background(), PublicPoolEnsureParams{Runtime: testPublicPoolRuntime()}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.ComposePath, []byte("services: {evil: {privileged: true}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Lifecycle(context.Background(), AppLifecycleStart, false); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("tampered snapshot reached command runner: %#v", runner.commands)
	}
}

func TestNativePublicPoolLifecycleRejectsBroadSnapshotMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode gate")
	}
	runner := &composeRecordingRunner{}
	manager := testPublicPoolManager(t, runner)
	if _, err := manager.Ensure(context.Background(), PublicPoolEnsureParams{Runtime: testPublicPoolRuntime()}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manager.Paths.EnvPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Lifecycle(context.Background(), AppLifecycleStart, false); err == nil {
		t.Fatal("broad snapshot mode accepted")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsafe snapshot reached command runner: %#v", runner.commands)
	}
}

func TestNativePublicPoolLegacyRemoveFailsClosedOnAmbiguousContainer(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) > 1 && args[0] == "ps" {
			return "0123456789ab\nabcdef012345\n", nil, true
		}
		return "", nil, false
	}}
	manager := testPublicPoolManager(t, runner)
	if err := os.MkdirAll(filepath.Dir(manager.Paths.LegacyComposePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.LegacyComposePath, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), false); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected fail closed, got %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestNativePublicPoolFirewallUsesOnlyFixedCatalogPorts(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == ufwPath && reflect.DeepEqual(args, []string{"status"}) {
			return "Status: active", nil, true
		}
		return "", nil, false
	}}
	manager := testPublicPoolManager(t, runner)
	state, err := manager.EnsureFirewall(context.Background(), false)
	if err != nil || !state.UFWActive {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	want := [][]string{{"status"}, {"allow", "3333/tcp"}, {"allow", "8081/tcp"}}
	if len(runner.commands) != len(want) {
		t.Fatalf("commands=%#v", runner.commands)
	}
	for index := range want {
		if !reflect.DeepEqual(runner.commands[index].args, want[index]) {
			t.Fatalf("command[%d]=%#v", index, runner.commands[index])
		}
	}
}
