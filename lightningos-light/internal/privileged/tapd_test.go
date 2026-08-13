package privileged

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func testTapdManager(t *testing.T, runner CommandRunner) *NativeTapdManager {
	t.Helper()
	root := t.TempDir()
	snapshot := filepath.Join(root, "privileged", "tapd")
	lndDir := filepath.Join(snapshot, appmanifest.TapdLNDDir)
	return &NativeTapdManager{Runner: runner, Paths: TapdPaths{
		SnapshotRoot:      snapshot,
		DataDir:           filepath.Join(root, "apps-data", "tapd", "data"),
		ComposePath:       filepath.Join(snapshot, appmanifest.TapdComposeFile),
		ConfigPath:        filepath.Join(snapshot, appmanifest.TapdConfigFile),
		LNDDir:            lndDir,
		TLSCertPath:       filepath.Join(lndDir, appmanifest.TapdTLSCertFile),
		MacaroonPath:      filepath.Join(lndDir, appmanifest.TapdMacaroonFile),
		LegacyComposePath: filepath.Join(root, "legacy", "tapd", appmanifest.TapdComposeFile),
	}}
}

func TestNativeTapdEnsureCreatesClosedRootOnlySnapshot(t *testing.T) {
	manager := testTapdManager(t, &composeRecordingRunner{})
	state, err := manager.Ensure(context.Background(), TapdEnsureParams{
		DatabasePassword:  "0123456789abcdef0123456789abcdef",
		LNDTLSCertificate: testLNDgCertificate(t, "localhost"),
		LNDMacaroon:       []byte("dedicated-tapd-macaroon"),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Installed || !state.HasLNDMacaroon {
		t.Fatalf("unexpected Tapd state: %#v", state)
	}
	compose, err := os.ReadFile(manager.Paths.ComposePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(compose) != appmanifest.TapdCompose(appmanifest.TapdComposePaths{
		DataDir: manager.Paths.DataDir, ConfigPath: manager.Paths.ConfigPath,
		TLSCertPath: manager.Paths.TLSCertPath, MacaroonPath: manager.Paths.MacaroonPath,
	}) {
		t.Fatal("broker snapshot does not match the closed Tapd catalog")
	}
	for _, forbidden := range []string{"admin.macaroon", "/data/lnd:/root/.lnd", "/var/run/docker.sock"} {
		if strings.Contains(string(compose), forbidden) {
			t.Fatalf("snapshot contains forbidden value %q", forbidden)
		}
	}
	for _, path := range []string{manager.Paths.ComposePath, manager.Paths.ConfigPath, manager.Paths.TLSCertPath, manager.Paths.MacaroonPath} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unsafe Tapd snapshot file %s: %v", path, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatalf("unexpected Tapd snapshot mode for %s: %v", path, info.Mode())
		}
	}
}

func TestNativeTapdEnsureRequiresDedicatedCredential(t *testing.T) {
	manager := testTapdManager(t, &composeRecordingRunner{})
	_, err := manager.Ensure(context.Background(), TapdEnsureParams{
		DatabasePassword:  "0123456789abcdef0123456789abcdef",
		LNDTLSCertificate: testLNDgCertificate(t, "localhost"),
	}, false)
	if err == nil {
		t.Fatal("Tapd accepted an install without a dedicated LND credential")
	}
}

func TestNativeTapdCLIUsesOnlyCatalogArgv(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		joined := strings.Join(args, " ")
		if path == dockerPath && strings.HasPrefix(joined, "ps --filter label=com.docker.compose.project=tapd") {
			return "0123456789ab\n", nil, true
		}
		if path == dockerPath && len(args) > 0 && args[0] == "exec" {
			return `{"synced_to_chain":true}`, nil, true
		}
		return "", nil, false
	}}
	manager := testTapdManager(t, runner)
	if err := os.MkdirAll(manager.Paths.SnapshotRoot, 0700); err != nil {
		t.Fatal(err)
	}
	config, _ := appmanifest.TapdConfig("0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(manager.Paths.ConfigPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.ComposePath, []byte("snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := manager.CLI(context.Background(), appmanifest.TapdCLIRequest{Command: appmanifest.TapdCLIGetInfo})
	if err != nil || output != `{"synced_to_chain":true}` {
		t.Fatalf("unexpected Tapd CLI result: %q %v", output, err)
	}
	last := runner.commands[len(runner.commands)-1]
	if got := strings.Join(last.args, " "); got != "exec 0123456789ab tapcli --network=mainnet getinfo" {
		t.Fatalf("unexpected privileged Tapd argv: %q", got)
	}
}

func TestNativeTapdLegacyRemoveFailsClosedOnAmbiguousContainer(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) > 1 && args[0] == "ps" {
			return "0123456789ab\nabcdef012345\n", nil, true
		}
		return "", nil, false
	}}
	manager := testTapdManager(t, runner)
	if err := os.MkdirAll(filepath.Dir(manager.Paths.LegacyComposePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.Paths.LegacyComposePath, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), false); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous legacy identity to fail closed, got %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("ambiguous legacy removal executed extra commands: %#v", runner.commands)
	}
}
