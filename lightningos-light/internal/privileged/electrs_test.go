package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

type electrsTestFixture struct {
	manager        *ComposeAppManager
	runner         *composeRecordingRunner
	appRoot        string
	privilegedRoot string
	runtime        appmanifest.ElectrsRuntime
	cookie         string
}

func writeTestElectrsApp(t *testing.T, withAttestation bool) *electrsTestFixture {
	t.Helper()
	appsRoot := filepath.Join(t.TempDir(), "apps")
	privilegedRoot := filepath.Join(t.TempDir(), "privileged-apps")
	appRoot := filepath.Join(appsRoot, appmanifest.ElectrsID)
	if err := os.MkdirAll(appRoot, 0750); err != nil {
		t.Fatal(err)
	}
	runtime := appmanifest.ElectrsRuntime{BitcoinMode: appmanifest.ElectrsBitcoinModeNative, Network: "regtest"}
	env, err := appmanifest.ElectrsRuntimeEnv(runtime)
	if err != nil {
		t.Fatal(err)
	}
	compose, err := appmanifest.ElectrsCompose(runtime)
	if err != nil {
		t.Fatal(err)
	}
	cookie := "electrs-user:electrs-password"
	mustWriteTestFile(t, filepath.Join(appRoot, appmanifest.ElectrsComposeFile), []byte(compose), 0600)
	mustWriteTestFile(t, filepath.Join(appRoot, appmanifest.ElectrsEnvFile), []byte(env), 0600)
	mustWriteTestFile(t, filepath.Join(appRoot, appmanifest.ElectrsCookieFile), []byte(cookie), 0600)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{
		Runner:             runner,
		AppsRoot:           appsRoot,
		PrivilegedAppsRoot: privilegedRoot,
		ElectrsRPCProbe: func(_ context.Context, endpoint, user, password, chain string) error {
			if endpoint != "http://127.0.0.1:18443/" || user != "electrs-user" || password != "electrs-password" || chain != "regtest" {
				return errors.New("unexpected probe contract")
			}
			return nil
		},
	}
	if withAttestation {
		writeTestElectrsAttestation(t, privilegedRoot, "sha256:"+strings.Repeat("a", 64))
	}
	return &electrsTestFixture{manager: manager, runner: runner, appRoot: appRoot, privilegedRoot: privilegedRoot, runtime: runtime, cookie: cookie}
}

func writeTestElectrsAttestation(t *testing.T, privilegedRoot, imageID string) string {
	t.Helper()
	root := filepath.Join(privilegedRoot, appmanifest.ElectrsID)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	raw := "image_id=" + imageID + "\n" +
		"release=" + appmanifest.ElectrsRelease + "\n" +
		"tag_object=" + appmanifest.ElectrsTagObject + "\n" +
		"commit=" + appmanifest.ElectrsSourceCommit + "\n" +
		"source_sha256=" + appmanifest.ElectrsSourceSHA256 + "\n" +
		"base_image=" + appmanifest.ElectrsBaseImage + "\n"
	path := filepath.Join(root, electrsImageAttestationFile)
	mustWriteTestFile(t, path, []byte(raw), 0600)
	return path
}

func writeTestElectrsImageFailure(t *testing.T, privilegedRoot string) string {
	t.Helper()
	root := filepath.Join(privilegedRoot, appmanifest.ElectrsID)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	raw := "release=" + appmanifest.ElectrsRelease + "\n" +
		"tag_object=" + appmanifest.ElectrsTagObject + "\n" +
		"commit=" + appmanifest.ElectrsSourceCommit + "\n" +
		"source_sha256=" + appmanifest.ElectrsSourceSHA256 + "\n" +
		"base_image=" + appmanifest.ElectrsBaseImage + "\n"
	path := filepath.Join(root, electrsImageFailureFile)
	mustWriteTestFile(t, path, []byte(raw), 0600)
	return path
}

func TestElectrsImageBuildScriptPinsSourceAndFixedProbe(t *testing.T) {
	script := electrsImageBuildScript("/var/lib/lightningos-privileged/apps/electrs/image-attestation")
	for _, required := range []string{
		appmanifest.ElectrsSourceURL,
		appmanifest.ElectrsSourceSHA256,
		appmanifest.ElectrsSourceDir,
		appmanifest.ElectrsImage,
		"sha256sum --check --strict",
		"--no-same-owner --no-same-permissions",
		"--network none --read-only --cap-drop ALL --security-opt no-new-privileges",
		"--version",
		"image-failure",
		"trap cleanup EXIT",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("build script missing %q", required)
		}
	}
	for _, forbidden := range []string{"git clone", "git fetch", "checkout master", "docker pull lightningos/electrs"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("build script contains mutable source action %q", forbidden)
		}
	}
}

func TestElectrsImageFailureSurvivesCollectedUnitAndExplicitPrepareRetries(t *testing.T) {
	root := t.TempDir()
	failurePath := writeTestElectrsImageFailure(t, root)
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, PrivilegedAppsRoot: root}

	state, err := manager.ImageStatus(context.Background(), appmanifest.ElectrsID, appmanifest.ElectrsImageApp)
	if err != nil || state.Status != "failed" || len(runner.commands) != 0 {
		t.Fatalf("failure marker state/error/commands=%#v/%v/%#v", state, err, runner.commands)
	}

	state, err = manager.PrepareImage(context.Background(), appmanifest.ElectrsID, appmanifest.ElectrsImageApp, false)
	if err != nil || state.Status != "preparing" {
		t.Fatalf("retry state/error=%#v/%v", state, err)
	}
	if _, err := os.Lstat(failurePath); !os.IsNotExist(err) {
		t.Fatalf("failure marker survived explicit retry: %v", err)
	}
	if len(runner.commands) != 1 || runner.commands[0].path != systemdRunPath {
		t.Fatalf("unexpected retry commands: %#v", runner.commands)
	}
}

func TestElectrsImageFailureMarkerIsFailClosed(t *testing.T) {
	root := t.TempDir()
	path := writeTestElectrsImageFailure(t, root)
	if err := os.WriteFile(path, []byte("release=attacker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, PrivilegedAppsRoot: root}
	if _, err := manager.ImageStatus(context.Background(), appmanifest.ElectrsID, appmanifest.ElectrsImageApp); err == nil || len(runner.commands) != 0 {
		t.Fatalf("invalid failure marker did not fail closed: err=%v commands=%#v", err, runner.commands)
	}
	if _, err := manager.PrepareImage(context.Background(), appmanifest.ElectrsID, appmanifest.ElectrsImageApp, false); err == nil || len(runner.commands) != 0 {
		t.Fatalf("invalid failure marker was executed: err=%v commands=%#v", err, runner.commands)
	}
}

func TestElectrsImagePreparationUsesFixedSourceBuildUnit(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == systemctlPath && len(args) > 0 && args[0] == "show" {
			return "LoadState=not-found\n", errors.New("not found"), true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner, PrivilegedAppsRoot: t.TempDir()}
	state, err := manager.PrepareImage(context.Background(), appmanifest.ElectrsID, appmanifest.ElectrsImageApp, false)
	if err != nil || state.Status != "preparing" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("unexpected build command sequence: %#v", runner.commands)
	}
	command := runner.commands[1]
	if command.path != systemdRunPath || len(command.args) != 8 || command.args[0] != "--quiet" || command.args[2] != "--unit=lightningos-electrs-image-app" || command.args[4] != "--property=RuntimeMaxSec=20min" || command.args[5] != "/bin/sh" || command.args[6] != "-c" {
		t.Fatalf("unexpected fixed build command: %#v", command)
	}
	if !strings.Contains(command.args[7], appmanifest.ElectrsSourceSHA256) || !strings.Contains(command.args[7], appmanifest.ElectrsImage) {
		t.Fatalf("build unit did not receive the closed catalog script: %#v", command)
	}
	before := len(runner.commands)
	if _, err := manager.PrepareImage(context.Background(), appmanifest.ElectrsID, "latest", false); err == nil || len(runner.commands) != before {
		t.Fatalf("unknown variant was not rejected before execution: %#v", runner.commands)
	}
}

func TestElectrsImageStatusRequiresMatchingAttestationAndImageID(t *testing.T) {
	root := t.TempDir()
	imageID := "sha256:" + strings.Repeat("a", 64)
	attestationPath := writeTestElectrsAttestation(t, root, imageID)
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", "--format", "{{.Id}}", appmanifest.ElectrsImage}) {
			return imageID + "\n", nil, true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner, PrivilegedAppsRoot: root}
	state, err := manager.ImageStatus(context.Background(), appmanifest.ElectrsID, appmanifest.ElectrsImageApp)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}

	raw, err := os.ReadFile(attestationPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attestationPath, []byte(strings.Replace(string(raw), appmanifest.ElectrsSourceSHA256, strings.Repeat("0", 64), 1)), 0600); err != nil {
		t.Fatal(err)
	}
	runner.hook = func(path string, args []string) (string, error, bool) {
		if path == systemctlPath {
			return "LoadState=not-found\n", errors.New("not found"), true
		}
		return "", nil, false
	}
	state, err = manager.ImageStatus(context.Background(), appmanifest.ElectrsID, appmanifest.ElectrsImageApp)
	if err != nil || state.Status != "absent" {
		t.Fatalf("mismatched attestation state/error=%#v/%v", state, err)
	}
}

func TestElectrsLifecycleUsesRootSnapshotAndIndependentFullNodeGate(t *testing.T) {
	fixture := writeTestElectrsApp(t, true)
	imageID := "sha256:" + strings.Repeat("a", 64)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", "--format", "{{.Id}}", appmanifest.ElectrsImage}) {
			return imageID + "\n", nil, true
		}
		return "", nil, false
	}
	if err := fixture.manager.Lifecycle(context.Background(), appmanifest.ElectrsID, AppLifecycleStart, false); err != nil {
		t.Fatal(err)
	}
	if len(fixture.runner.commands) != 3 || !hasArgsSuffix(fixture.runner.commands[2].args, "up", "-d") {
		t.Fatalf("unexpected lifecycle commands: %#v", fixture.runner.commands)
	}
	for _, arg := range fixture.runner.commands[2].args {
		if strings.Contains(arg, fixture.appRoot) || strings.Contains(arg, fixture.cookie) || strings.Contains(arg, "electrs-password") {
			t.Fatalf("manager path or secret reached Docker arguments: %q", arg)
		}
	}
	snapshotRoot := filepath.Join(fixture.privilegedRoot, appmanifest.ElectrsID)
	if !strings.Contains(fixture.runner.composeSnapshot, "user: \"1000:1000\"") || !strings.Contains(fixture.runner.composeSnapshot, "--daemon-rpc-addr="+appmanifest.BitcoinConsumerHostGateway+":18443") {
		t.Fatalf("unexpected execution manifest:\n%s", fixture.runner.composeSnapshot)
	}
	cookieRaw, err := os.ReadFile(filepath.Join(snapshotRoot, appmanifest.ElectrsCookieFile))
	if err != nil || string(cookieRaw) != fixture.cookie {
		t.Fatalf("snapshot credential mismatch/error=%q/%v", cookieRaw, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(snapshotRoot, appmanifest.ElectrsCookieFile))
		if err != nil || info.Mode().Perm()&007 != 0 {
			t.Fatalf("snapshot credential is exposed: mode/error=%v/%v", info.Mode(), err)
		}
	}
}

func TestElectrsLifecycleFullNodeFailuresStopBeforeDocker(t *testing.T) {
	for _, reason := range []string{
		"Bitcoin RPC authentication failed",
		"Electrs requires an unpruned Bitcoin Full Node",
		"Electrs requires a fully synchronized Bitcoin Full Node",
		"Electrs requires Bitcoin txindex=1",
		"Electrs requires a fully synchronized Bitcoin txindex",
	} {
		t.Run(reason, func(t *testing.T) {
			fixture := writeTestElectrsApp(t, false)
			fixture.manager.ElectrsRPCProbe = func(context.Context, string, string, string, string) error {
				return errors.New(reason)
			}
			err := fixture.manager.Lifecycle(context.Background(), appmanifest.ElectrsID, AppLifecycleStart, false)
			if err == nil || err.Error() != reason {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(fixture.runner.commands) != 0 {
				t.Fatalf("failed full-node gate reached Docker: %#v", fixture.runner.commands)
			}
		})
	}
}

func TestElectrsDeclarationTamperingFailsBeforeCommand(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture *electrsTestFixture)
	}{
		{name: "compose", mutate: func(t *testing.T, fixture *electrsTestFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.appRoot, appmanifest.ElectrsComposeFile), []byte("services:\n  attacker:\n    privileged: true\n"), 0600)
		}},
		{name: "environment", mutate: func(t *testing.T, fixture *electrsTestFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.appRoot, appmanifest.ElectrsEnvFile), []byte("ELECTRS_BITCOIN_MODE=remote\nELECTRS_NETWORK=bitcoin\n"), 0600)
		}},
		{name: "cookie newline", mutate: func(t *testing.T, fixture *electrsTestFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.appRoot, appmanifest.ElectrsCookieFile), []byte(fixture.cookie+"\n"), 0600)
		}},
		{name: "unexpected asset", mutate: func(t *testing.T, fixture *electrsTestFixture) {
			mustWriteTestFile(t, filepath.Join(fixture.appRoot, "override.yaml"), []byte("privileged: true\n"), 0600)
		}},
		{name: "symlinked cookie", mutate: func(t *testing.T, fixture *electrsTestFixture) {
			path := filepath.Join(fixture.appRoot, appmanifest.ElectrsCookieFile)
			target := filepath.Join(filepath.Dir(fixture.appRoot), "cookie-copy")
			mustWriteTestFile(t, target, []byte(fixture.cookie), 0600)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := writeTestElectrsApp(t, false)
			test.mutate(t, fixture)
			if err := fixture.manager.Lifecycle(context.Background(), appmanifest.ElectrsID, AppLifecycleStart, true); err == nil {
				t.Fatal("expected tampered declaration rejection")
			}
			if len(fixture.runner.commands) != 0 {
				t.Fatalf("tampered declaration reached command runner: %#v", fixture.runner.commands)
			}
		})
	}
}

func TestElectrsRemoveUsesCatalogVolumePolicyAndPreservesAttestation(t *testing.T) {
	fixture := writeTestElectrsApp(t, true)
	attestationPath := filepath.Join(fixture.privilegedRoot, appmanifest.ElectrsID, electrsImageAttestationFile)
	if err := fixture.manager.Remove(context.Background(), appmanifest.ElectrsID, false); err != nil {
		t.Fatal(err)
	}
	if len(fixture.runner.commands) != 2 || !hasArgsSuffix(fixture.runner.commands[1].args, "down", "--remove-orphans", "--timeout", "60", "--volumes") {
		t.Fatalf("unexpected remove commands: %#v", fixture.runner.commands)
	}
	if _, err := os.Stat(attestationPath); err != nil {
		t.Fatalf("image attestation was not preserved: %v", err)
	}
	for _, name := range []string{appmanifest.ElectrsComposeFile, appmanifest.ElectrsEnvFile, appmanifest.ElectrsCookieFile} {
		if _, err := os.Stat(filepath.Join(fixture.privilegedRoot, appmanifest.ElectrsID, name)); !os.IsNotExist(err) {
			t.Fatalf("execution asset %s was not removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.appRoot, appmanifest.ElectrsComposeFile)); err != nil {
		t.Fatalf("broker removed manager-owned declaration: %v", err)
	}
}

func TestProbeElectrsBitcoinRPCFullNodeContract(t *testing.T) {
	type state struct {
		chain   string
		pruned  bool
		ibd     bool
		blocks  int64
		headers int64
		tx      *electrsIndexInfo
		auth    bool
	}
	tests := []struct {
		name    string
		state   state
		wantErr string
	}{
		{name: "ready", state: state{chain: "regtest", blocks: 101, headers: 101, tx: &electrsIndexInfo{Synced: true, BestBlockHeight: 101}}},
		{name: "wrong chain", state: state{chain: "main", blocks: 101, headers: 101, tx: &electrsIndexInfo{Synced: true, BestBlockHeight: 101}}, wantErr: "chain does not match"},
		{name: "pruned", state: state{chain: "regtest", pruned: true, blocks: 101, headers: 101, tx: &electrsIndexInfo{Synced: true, BestBlockHeight: 101}}, wantErr: "unpruned"},
		{name: "ibd", state: state{chain: "regtest", ibd: true, blocks: 100, headers: 101, tx: &electrsIndexInfo{Synced: true, BestBlockHeight: 100}}, wantErr: "fully synchronized Bitcoin Full Node"},
		{name: "missing txindex", state: state{chain: "regtest", blocks: 101, headers: 101}, wantErr: "txindex=1"},
		{name: "txindex syncing", state: state{chain: "regtest", blocks: 101, headers: 101, tx: &electrsIndexInfo{Synced: false, BestBlockHeight: 100}}, wantErr: "synchronized Bitcoin txindex"},
		{name: "bad auth", state: state{auth: true}, wantErr: "authentication failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				user, password, ok := request.BasicAuth()
				if test.state.auth || !ok || user != "rpc-user" || password != "rpc-password" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				var rpc struct {
					Method string `json:"method"`
				}
				if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
					t.Fatal(err)
				}
				var result any
				switch rpc.Method {
				case "getblockchaininfo":
					result = electrsBlockchainInfo{Chain: test.state.chain, Pruned: test.state.pruned, InitialBlockDownload: test.state.ibd, Blocks: test.state.blocks, Headers: test.state.headers, VerificationProgress: 1}
				case "getindexinfo":
					indexes := map[string]electrsIndexInfo{}
					if test.state.tx != nil {
						indexes["txindex"] = *test.state.tx
					}
					result = indexes
				default:
					t.Fatalf("unexpected RPC method %q", rpc.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": "electrs-gate"})
			}))
			defer server.Close()
			err := probeElectrsBitcoinRPC(context.Background(), server.URL, "rpc-user", "rpc-password", "regtest")
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error=%v want containing %q", err, test.wantErr)
			}
		})
	}
}
