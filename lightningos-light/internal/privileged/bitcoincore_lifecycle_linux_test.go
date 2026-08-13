//go:build linux

package privileged

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

const lifecycleTestBitcoinCoreImageID = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestBitcoinCoreLifecycleUsesOnlyBrokerOwnedExecutionAssets(t *testing.T) {
	manager, runner, privilegedRoot, dataDir := newTestBitcoinCoreLifecycleManager(t)
	managerOwnedRoot := filepath.Join(t.TempDir(), appmanifest.BitcoinCoreID)
	if err := os.MkdirAll(managerOwnedRoot, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managerOwnedRoot, appmanifest.BitcoinCoreComposeFile), []byte("services:\n  bitcoind:\n    privileged: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager.AppsRoot = filepath.Dir(managerOwnedRoot)

	if err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	expected, err := appmanifest.BitcoinCoreCompose(dataDir, filepath.ToSlash(filepath.Join(privilegedRoot, appmanifest.BitcoinCoreID)))
	if err != nil {
		t.Fatal(err)
	}
	if runner.composeSnapshot != expected || strings.Contains(runner.composeSnapshot, "privileged: true") {
		t.Fatalf("broker executed an unexpected manifest:\n%s", runner.composeSnapshot)
	}
	last := runner.commands[len(runner.commands)-1]
	if !hasArgsSuffix(last.args, "up", "-d") {
		t.Fatalf("unexpected start command: %#v", last)
	}
	for _, arg := range last.args {
		if strings.Contains(arg, manager.AppsRoot) {
			t.Fatalf("manager-owned path reached Docker: %q", arg)
		}
	}
	for _, name := range []string{appmanifest.BitcoinCoreComposeFile, appmanifest.BitcoinCoreStorageGuardFile} {
		path := filepath.Join(privilegedRoot, appmanifest.BitcoinCoreID, name)
		if err := validateRootOwnedRegularFile(path, 0600); err != nil {
			t.Fatalf("broker asset %s is unsafe: %v", name, err)
		}
	}

	runner.commands = nil
	if err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleRestart, false); err != nil {
		t.Fatal(err)
	}
	if !hasArgsSuffix(runner.commands[len(runner.commands)-1].args, "up", "-d", "--force-recreate", "--no-deps", "bitcoind") {
		t.Fatalf("unexpected restart command: %#v", runner.commands)
	}
}

func TestBitcoinCoreInspectAndRemovePreserveEnrollmentAndAttestation(t *testing.T) {
	manager, runner, privilegedRoot, _ := newTestBitcoinCoreLifecycleManager(t)
	if err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	runner.runningServices = "bitcoind\n"
	inspection, err := manager.Inspect(context.Background(), appmanifest.BitcoinCoreID)
	if err != nil || inspection.Status != "running" {
		t.Fatalf("inspection/error=%#v/%v", inspection, err)
	}
	if err := manager.Remove(context.Background(), appmanifest.BitcoinCoreID, false); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(privilegedRoot, appmanifest.BitcoinCoreID)
	for _, name := range []string{bitcoinCoreStorageDataDirFile, bitcoinCoreStorageIDFile, bitcoinCoreImageAttestationFile} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("persistent security state %s was removed: %v", name, err)
		}
	}
	for _, name := range []string{appmanifest.BitcoinCoreComposeFile, appmanifest.BitcoinCoreStorageGuardFile} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("execution asset %s remains after remove: %v", name, err)
		}
	}
	if !hasArgsSuffix(runner.commands[len(runner.commands)-1].args, "down", "--remove-orphans", "--timeout", "10") {
		t.Fatalf("unexpected remove command: %#v", runner.commands)
	}
}

func TestBitcoinCoreLogsUseBrokerOwnedInspectionSnapshot(t *testing.T) {
	manager, runner, privilegedRoot, _ := newTestBitcoinCoreLifecycleManager(t)
	runner.hook = func(path string, args []string) (string, error, bool) {
		if hasArgsSuffix(args, "logs", "--no-color", "--tail", "25", "--since", "2h", appmanifest.BitcoinCorePrimaryService) {
			return "line one\nline two\n", nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", "--format", "{{.Id}}", appmanifest.BitcoinCoreImage}) {
			return lifecycleTestBitcoinCoreImageID + "\n", nil, true
		}
		return "", nil, false
	}

	state, err := manager.Logs(context.Background(), appmanifest.BitcoinCoreID, 25, "2h")
	if err != nil {
		t.Fatal(err)
	}
	if state.Source != "docker:"+appmanifest.BitcoinCorePrimaryService || !reflect.DeepEqual(state.Lines, []string{"line one", "line two"}) {
		t.Fatalf("unexpected Bitcoin log state: %#v", state)
	}
	last := runner.commands[len(runner.commands)-1]
	joined := strings.Join(last.args, " ")
	expectedRoot := filepath.Join(privilegedRoot, appmanifest.BitcoinCoreID)
	if !strings.Contains(joined, "--project-directory ") || strings.Contains(joined, "/var/lib/lightningos/apps/") || strings.Contains(joined, "--follow") {
		t.Fatalf("Bitcoin logs escaped the closed broker snapshot: %#v", last)
	}
	if strings.Contains(joined, expectedRoot+"/../") {
		t.Fatalf("Bitcoin log snapshot traversal accepted: %#v", last)
	}
}

func TestBitcoinCoreStatusUsesCookieBackedFixedCLICommands(t *testing.T) {
	manager, runner, _, _ := newTestBitcoinCoreLifecycleManager(t)
	containerID := strings.Repeat("b", 64)
	hash := strings.Repeat("0", 64)
	runner.runningServices = appmanifest.BitcoinCorePrimaryService + "\n"
	runner.hook = func(path string, args []string) (string, error, bool) {
		if hasArgsSuffix(args, "ps", "-q", appmanifest.BitcoinCorePrimaryService) {
			return containerID + "\n", nil, true
		}
		if path != dockerPath {
			return "", nil, false
		}
		prefix := []string{"exec", "-i", containerID, "bitcoin-cli", "-datadir=" + appmanifest.BitcoinCoreContainerDataDir, "-conf=" + appmanifest.BitcoinCoreContainerConfig, "-rpcclienttimeout=40"}
		if len(args) < len(prefix) || !reflect.DeepEqual(args[:len(prefix)], prefix) {
			return "", nil, false
		}
		switch {
		case reflect.DeepEqual(args[len(prefix):], []string{"getblockchaininfo"}):
			return fmt.Sprintf(`{"chain":"main","blocks":954700,"headers":954701,"verificationprogress":0.999,"initialblockdownload":true,"bestblockhash":"%s","pruned":false,"size_on_disk":123}`, hash), nil, true
		case reflect.DeepEqual(args[len(prefix):], []string{"getnetworkinfo"}):
			return `{"version":310100,"subversion":"/Satoshi:31.1.0/","connections":12}`, nil, true
		case reflect.DeepEqual(args[len(prefix):], []string{"getblockheader", hash, "true"}):
			return `{"time":1780000000}`, nil, true
		default:
			return "", nil, false
		}
	}

	state, err := manager.BitcoinCoreStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.Chain != "main" || state.Blocks != 954700 || state.Headers != 954701 || state.VerificationProgress != 0.999 ||
		!state.NetworkOK || state.Version != 310100 || state.Connections != 12 || state.BestBlockTime != 1780000000 {
		t.Fatalf("unexpected bitcoin status: %+v", state)
	}
	for _, command := range runner.commands {
		joined := strings.Join(command.args, " ")
		if strings.Contains(joined, "rpcuser") || strings.Contains(joined, "rpcpassword") || strings.Contains(joined, "rpcauth") {
			t.Fatalf("credential material reached command arguments: %#v", command)
		}
	}
}

func TestBitcoinCoreStatusRejectsUntrustedContainerIDBeforeExec(t *testing.T) {
	manager, runner, _, _ := newTestBitcoinCoreLifecycleManager(t)
	runner.runningServices = appmanifest.BitcoinCorePrimaryService + "\n"
	runner.hook = func(_ string, args []string) (string, error, bool) {
		if hasArgsSuffix(args, "ps", "-q", appmanifest.BitcoinCorePrimaryService) {
			return "abc;reboot\n", nil, true
		}
		return "", nil, false
	}
	if _, err := manager.BitcoinCoreStatus(context.Background()); err == nil {
		t.Fatal("untrusted container ID was accepted")
	}
	for _, command := range runner.commands {
		if command.path == dockerPath && len(command.args) > 0 && command.args[0] == "exec" {
			t.Fatalf("invalid container ID reached docker exec: %#v", command)
		}
	}
}

func TestBitcoinCoreLifecycleRejectsWrongStorageIdentityBeforeDocker(t *testing.T) {
	manager, runner, _, dataDir := newTestBitcoinCoreLifecycleManager(t)
	marker := filepath.Join(dataDir, appmanifest.BitcoinCoreStorageMarker)
	if err := os.WriteFile(marker, []byte(strings.Repeat("b", 48)+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(marker, 0, 101); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleStart, false); err == nil {
		t.Fatal("wrong storage identity was accepted")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("rejected storage reached Docker: %#v", runner.commands)
	}
}

func newTestBitcoinCoreLifecycleManager(t *testing.T) (*ComposeAppManager, *composeRecordingRunner, string, string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root ownership assertions require root")
	}
	dataDir, err := os.MkdirTemp("/mnt", "lightningos-bitcoin-lifecycle-test-")
	if err != nil {
		t.Skipf("isolated /mnt test directory unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	if err := os.Chown(dataDir, 101, 101); err != nil || os.Chmod(dataDir, 0750) != nil {
		t.Fatalf("secure data directory: %v", err)
	}

	privilegedRoot := t.TempDir()
	root := filepath.Join(privilegedRoot, appmanifest.BitcoinCoreID)
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	storageID := strings.Repeat("a", 48)
	for name, content := range map[string]string{
		bitcoinCoreStorageDataDirFile: dataDir + "\n",
		bitcoinCoreStorageIDFile:      storageID + "\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(dataDir, appmanifest.BitcoinCoreStorageMarker)
	if err := os.WriteFile(marker, []byte(storageID+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(marker, 0, 101); err != nil {
		t.Fatalf("secure storage marker: %v", err)
	}
	config := filepath.Join(dataDir, bitcoinCoreConfigFile)
	if err := os.WriteFile(config, []byte("server=1\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(config, 0, 101); err != nil {
		t.Fatalf("secure bitcoin config: %v", err)
	}
	artifact, err := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	attestation := fmt.Sprintf("image_id=%s\nrelease=%s\narchive_sha256=%s\nbase_image=%s\nsignatures=%d\n",
		lifecycleTestBitcoinCoreImageID, appmanifest.BitcoinCoreRelease, artifact.ArchiveSHA256, artifact.BaseImage, appmanifest.BitcoinCoreSignatureThreshold)
	if err := os.WriteFile(filepath.Join(root, bitcoinCoreImageAttestationFile), []byte(attestation), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", "--format", "{{.Id}}", appmanifest.BitcoinCoreImage}) {
			return lifecycleTestBitcoinCoreImageID + "\n", nil, true
		}
		return "", nil, false
	}}
	return &ComposeAppManager{Runner: runner, PrivilegedAppsRoot: privilegedRoot, TempRoot: t.TempDir()}, runner, privilegedRoot, dataDir
}
