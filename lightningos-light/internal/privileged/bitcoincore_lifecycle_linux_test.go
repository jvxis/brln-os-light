//go:build linux

package privileged

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
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
	hash := fmt.Sprintf("%064x", 1)
	headers := make(map[string]string)
	for index := 0; index <= bitcoinCoreCadenceBucketCount+1; index++ {
		currentHash := fmt.Sprintf("%064x", index+1)
		previousHash := fmt.Sprintf("%064x", index+2)
		headers[currentHash] = fmt.Sprintf(`{"time":%d,"previousblockhash":"%s"}`, 1780000000-int64(index)*bitcoinCoreCadenceWindowSec, previousHash)
	}
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
		case len(args[len(prefix):]) == 3 && args[len(prefix)] == "getblockheader" && args[len(prefix)+2] == "true":
			if header, ok := headers[args[len(prefix)+1]]; ok {
				return header, nil, true
			}
			return "", errors.New("unknown test header"), true
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
	if state.BlockCadenceWindowSec != bitcoinCoreCadenceWindowSec || len(state.BlockCadence) != bitcoinCoreCadenceBucketCount {
		t.Fatalf("unexpected bitcoin cadence: %+v", state.BlockCadence)
	}
	total := 0
	for _, bucket := range state.BlockCadence {
		total += bucket.Count
	}
	if total != bitcoinCoreCadenceBucketCount {
		t.Fatalf("unexpected bitcoin cadence total: %d", total)
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

func TestBitcoinCoreLegacyMigrationSucceedsWithoutTouchingLND(t *testing.T) {
	manager, runner, _, dataDir := newTestBitcoinCoreLifecycleManager(t)
	legacyID := strings.Repeat("b", 64)
	newID := strings.Repeat("c", 64)
	legacyImageID := "sha256:" + strings.Repeat("d", 64)
	runner.containerID = newID
	baseHook := runner.hook
	newStarted := false
	runner.hook = func(path string, args []string) (string, error, bool) {
		if output, err, handled := baseHook(path, args); handled {
			return output, err, true
		}
		if path == dockerPath && len(args) > 1 && args[0] == "ps" && args[1] == "-a" {
			if newStarted {
				return newID + "\n", nil, true
			}
			return legacyID + "\n", nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"inspect", legacyID}) {
			return legacyBitcoinInspectJSON(legacyID, legacyImageID, dataDir), nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"inspect", newID}) {
			return managedBitcoinInspectJSON(newID, lifecycleTestBitcoinCoreImageID, dataDir), nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", legacyImageID}) {
			return legacyImageID, nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"commit", "--pause=false", legacyID, bitcoinCoreLegacyRollbackImage}) {
			return "sha256:" + strings.Repeat("e", 64), nil, true
		}
		if hasArgsSuffix(args, "up", "-d") {
			newStarted = true
			return "", nil, false
		}
		return "", nil, false
	}
	manager.BitcoinMigrationProbe = func(_ context.Context, containerID string) error {
		if containerID != newID {
			t.Fatalf("migration probed container %q, want %q", containerID, newID)
		}
		return nil
	}
	if err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	for _, command := range runner.commands {
		joined := command.path + " " + strings.Join(command.args, " ")
		if strings.Contains(joined, "/data/lnd") || (command.path == systemctlPath && slices.Contains(command.args, "lnd")) {
			t.Fatalf("Bitcoin migration touched LND: %s", joined)
		}
	}
}

func TestBitcoinCoreLegacyMigrationPreflightFailureDoesNotCutOver(t *testing.T) {
	manager, runner, _, dataDir := newTestBitcoinCoreLifecycleManager(t)
	legacyID := strings.Repeat("b", 64)
	legacyImageID := "sha256:" + strings.Repeat("d", 64)
	baseHook := runner.hook
	runner.hook = func(path string, args []string) (string, error, bool) {
		if output, err, handled := baseHook(path, args); handled {
			return output, err, true
		}
		if path == dockerPath && len(args) > 1 && args[0] == "ps" && args[1] == "-a" {
			return legacyID + "\n", nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"inspect", legacyID}) {
			return legacyBitcoinInspectJSON(legacyID, legacyImageID, dataDir), nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", legacyImageID}) {
			return legacyImageID, nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"commit", "--pause=false", legacyID, bitcoinCoreLegacyRollbackImage}) {
			return "", errors.New("simulated rollback capture failure"), true
		}
		return "", nil, false
	}

	err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleStart, false)
	if err == nil || !strings.Contains(err.Error(), "rollback image could not be captured") {
		t.Fatalf("unexpected migration result: %v", err)
	}
	for _, command := range runner.commands {
		joined := strings.Join(command.args, " ")
		if slices.Contains(command.args, "up") || slices.Contains(command.args, "down") || slices.Contains(command.args, "stop") || slices.Contains(command.args, "rm") {
			t.Fatalf("preflight failure reached lifecycle cutover: %s", joined)
		}
		if strings.Contains(joined, "/data/lnd") || (command.path == systemctlPath && slices.Contains(command.args, "lnd")) {
			t.Fatalf("preflight failure touched LND: %s", joined)
		}
	}
}

func TestValidatedLegacyBitcoinPortLines(t *testing.T) {
	type binding struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}
	valid := map[string][]binding{
		"8332/tcp":  {{HostIP: "127.0.0.1", HostPort: "8332"}},
		"8333/tcp":  {{HostIP: "0.0.0.0", HostPort: "8333"}},
		"28332/tcp": {{HostIP: "127.0.0.1", HostPort: "28332"}},
		"28333/tcp": {{HostIP: "::", HostPort: "28333"}},
	}
	if _, err := validatedLegacyBitcoinPortLines(valid); err != nil {
		t.Fatalf("valid legacy port topology rejected: %v", err)
	}
	for name, invalid := range map[string]map[string][]binding{
		"unknown container port": {"18443/tcp": {{HostPort: "18443"}}},
		"remapped host port":     {"8332/tcp": {{HostIP: "127.0.0.1", HostPort: "18332"}}},
		"unexpected host":        {"8332/tcp": {{HostIP: "192.0.2.10", HostPort: "8332"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validatedLegacyBitcoinPortLines(invalid); err == nil {
				t.Fatal("unsafe legacy port topology was accepted")
			}
		})
	}
}

func TestBitcoinCoreLegacyMigrationFailureRollsBack(t *testing.T) {
	manager, runner, _, dataDir := newTestBitcoinCoreLifecycleManager(t)
	legacyID := strings.Repeat("b", 64)
	legacyImageID := "sha256:" + strings.Repeat("d", 64)
	runner.containerID = legacyID
	baseHook := runner.hook
	newStartFailed := false
	runner.hook = func(path string, args []string) (string, error, bool) {
		if output, err, handled := baseHook(path, args); handled {
			return output, err, true
		}
		if path == dockerPath && len(args) > 1 && args[0] == "ps" && args[1] == "-a" {
			return legacyID + "\n", nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"inspect", legacyID}) {
			return legacyBitcoinInspectJSON(legacyID, legacyImageID, dataDir), nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", legacyImageID}) {
			return legacyImageID, nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, []string{"commit", "--pause=false", legacyID, bitcoinCoreLegacyRollbackImage}) {
			return "sha256:" + strings.Repeat("e", 64), nil, true
		}
		if hasArgsSuffix(args, "up", "-d") && !strings.Contains(strings.Join(args, " "), "bitcoincore-rollback-") {
			newStartFailed = true
			return "", errors.New("simulated cutover failure"), true
		}
		if path == dockerPath && len(args) > 2 && args[0] == "exec" && args[2] == legacyID {
			return `{"chain":"main"}`, nil, true
		}
		return "", nil, false
	}
	err := manager.Lifecycle(context.Background(), appmanifest.BitcoinCoreID, AppLifecycleStart, false)
	if err == nil || !strings.Contains(err.Error(), "restored automatically") || !newStartFailed {
		t.Fatalf("migration error/failure=%v/%v", err, newStartFailed)
	}
	rollbackSeen := false
	for _, command := range runner.commands {
		joined := strings.Join(command.args, " ")
		if strings.Contains(joined, "bitcoincore-rollback-") && hasArgsSuffix(command.args, "up", "-d", "--force-recreate", "--no-deps", "bitcoind") {
			rollbackSeen = true
		}
		if strings.Contains(joined, "/data/lnd") || (command.path == systemctlPath && slices.Contains(command.args, "lnd")) {
			t.Fatalf("rollback touched LND: %s", joined)
		}
	}
	if !rollbackSeen {
		t.Fatal("automatic rollback command was not executed")
	}
}

func legacyBitcoinInspectJSON(containerID, imageID, dataDir string) string {
	return fmt.Sprintf(`[{"Id":%q,"Image":%q,"Config":{"Image":"bitcoin/bitcoin:latest"},"State":{"Running":true},"HostConfig":{"NetworkMode":"bitcoincore_default","PortBindings":{}},"Mounts":[{"Type":"bind","Source":%q,"Destination":"/home/bitcoin/.bitcoin","RW":true}]}]`, containerID, imageID, dataDir)
}

func managedBitcoinInspectJSON(containerID, imageID, dataDir string) string {
	return fmt.Sprintf(`[{"Id":%q,"Image":%q,"Config":{"Image":%q},"State":{"Running":true},"HostConfig":{"NetworkMode":"bitcoincore_default","PortBindings":{}},"Mounts":[{"Type":"bind","Source":%q,"Destination":"/home/bitcoin/.bitcoin","RW":true}]}]`, containerID, imageID, appmanifest.BitcoinCoreImage, dataDir)
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
