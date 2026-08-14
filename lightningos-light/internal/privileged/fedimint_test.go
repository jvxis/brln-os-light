package privileged

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

type fedimintTestFixture struct {
	manager  *ComposeAppManager
	runner   *composeRecordingRunner
	appRoot  string
	lndRoot  string
	runtime  appmanifest.FedimintGatewayRuntime
	macaroon []byte
}

func writeTestFedimintGateway(t *testing.T) *fedimintTestFixture {
	t.Helper()
	root := t.TempDir()
	appsRoot := filepath.Join(root, "apps")
	dataRoot := filepath.Join(root, "data")
	privilegedRoot := filepath.Join(root, "privileged")
	lndRoot := filepath.Join(root, "lnd")
	appRoot := filepath.Join(appsRoot, appmanifest.FedimintGatewayID)
	for _, directory := range []string{appRoot, filepath.Join(lndRoot, "data", "chain", "bitcoin", "mainnet")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	runtime := appmanifest.FedimintGatewayRuntime{
		Bitcoin: appmanifest.FedimintBitcoinRuntime{
			Mode: appmanifest.FedimintBitcoinModeNative, URL: "http://host.docker.internal:8332", User: "rpc-user", Pass: "rpc-pass",
		},
		GatewayPasswordHash: "$2a$10$abcdefghijklmnopqrstuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu",
	}
	runtimeRaw, _ := appmanifest.FedimintGatewayRuntimeJSON(runtime)
	compose, _ := appmanifest.FedimintGatewayCompose(runtime)
	certificate := testLNDgCertificate(t, "host.docker.internal")
	macaroon := []byte("dedicated-fedimint-gateway-macaroon")
	for _, file := range []struct {
		path string
		raw  []byte
	}{
		{filepath.Join(appRoot, appmanifest.FedimintGatewayRuntimeFile), runtimeRaw},
		{filepath.Join(appRoot, appmanifest.FedimintGatewayComposeFile), []byte(compose)},
		{filepath.Join(appRoot, appmanifest.FedimintGatewayTLSFile), certificate},
		{filepath.Join(appRoot, appmanifest.FedimintGatewayMacaroonFile), macaroon},
		{filepath.Join(lndRoot, "tls.cert"), certificate},
		{filepath.Join(lndRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon"), []byte("native-admin-macaroon")},
	} {
		if err := os.WriteFile(file.path, file.raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{
		Runner: runner, AppsRoot: appsRoot, AppsDataRoot: dataRoot,
		PrivilegedAppsRoot: privilegedRoot, LNDDataRoot: lndRoot,
	}
	return &fedimintTestFixture{manager: manager, runner: runner, appRoot: appRoot, lndRoot: lndRoot, runtime: runtime, macaroon: macaroon}
}

func TestFedimintGatewaySnapshotUsesDedicatedCredentialAndClosedPaths(t *testing.T) {
	fixture := writeTestFedimintGateway(t)
	files, err := fixture.manager.validatedFedimintFiles(appmanifest.FedimintGatewayID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := fixture.manager.createFedimintSnapshot(appmanifest.FedimintGatewayID, files)
	if err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(snapshot.envPath)
	if err != nil {
		t.Fatal(err)
	}
	wantData := appmanifest.FedimintDataDirEnv + "=" + filepath.Join(fixture.manager.AppsDataRoot, appmanifest.FedimintGatewayID, "gatewayd")
	if !strings.Contains(string(env), wantData) || !strings.Contains(string(env), appmanifest.FedimintGatewayCredentialRoot+"="+filepath.Join(snapshot.root, "lnd")) {
		t.Fatalf("unexpected broker environment: %q", env)
	}
	credential, err := os.ReadFile(filepath.Join(snapshot.root, "lnd", appmanifest.FedimintGatewayMacaroonFile))
	if err != nil || string(credential) != string(fixture.macaroon) {
		t.Fatalf("dedicated credential was not preserved: %v", err)
	}
	compose, _ := os.ReadFile(snapshot.composePath)
	if strings.Contains(string(compose), "admin.macaroon") || strings.Contains(string(compose), "/data/lnd:/data/lnd") {
		t.Fatalf("snapshot exposed native LND material: %s", compose)
	}
}

func TestFedimintGatewayRejectsAdminMacaroonAndTamperedDeclaration(t *testing.T) {
	fixture := writeTestFedimintGateway(t)
	admin, _ := os.ReadFile(filepath.Join(fixture.lndRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon"))
	if err := os.WriteFile(filepath.Join(fixture.appRoot, appmanifest.FedimintGatewayMacaroonFile), admin, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.validatedFedimintFiles(appmanifest.FedimintGatewayID); err == nil || !strings.Contains(err.Error(), "must not use") {
		t.Fatalf("expected admin macaroon rejection, got %v", err)
	}
	fixture = writeTestFedimintGateway(t)
	composePath := filepath.Join(fixture.appRoot, appmanifest.FedimintGatewayComposeFile)
	compose, _ := os.ReadFile(composePath)
	compose = []byte(strings.Replace(string(compose), "read_only: true", "read_only: false", 1))
	if err := os.WriteFile(composePath, compose, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.validatedFedimintFiles(appmanifest.FedimintGatewayID); err == nil {
		t.Fatal("expected tampered Gateway declaration to be rejected")
	}
}

func TestFedimintInspectRecognizesLegacyRuntimeWithoutExecutingCompose(t *testing.T) {
	for _, test := range []struct {
		name       string
		appID      string
		compose    string
		project    string
		service    string
		dockerOut  string
		wantStatus string
	}{
		{name: "running guardian", appID: appmanifest.FedimintGuardianID, compose: appmanifest.FedimintGuardianComposeFile, project: appmanifest.FedimintGuardianProject, service: appmanifest.FedimintGuardianPrimaryService, dockerOut: strings.Repeat("a", 64) + "\n", wantStatus: "running"},
		{name: "stopped gateway", appID: appmanifest.FedimintGatewayID, compose: appmanifest.FedimintGatewayComposeFile, project: appmanifest.FedimintGatewayProject, service: appmanifest.FedimintGatewayPrimaryService, wantStatus: "stopped"},
	} {
		t.Run(test.name, func(t *testing.T) {
			appsRoot := t.TempDir()
			appRoot := filepath.Join(appsRoot, test.appID)
			if err := os.Mkdir(appRoot, 0750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(appRoot, test.compose), []byte("legacy declaration\n"), 0640); err != nil {
				t.Fatal(err)
			}
			runner := &composeRecordingRunner{}
			wantArgs := []string{
				"ps",
				"--filter", "label=com.docker.compose.project=" + test.project,
				"--filter", "label=com.docker.compose.service=" + test.service,
				"--format", "{{.ID}}",
			}
			runner.hook = func(path string, args []string) (string, error, bool) {
				if path == dockerPath && reflect.DeepEqual(args, wantArgs) {
					return test.dockerOut, nil, true
				}
				return "", nil, false
			}
			manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
			inspection, err := manager.Inspect(context.Background(), test.appID)
			if err != nil {
				t.Fatal(err)
			}
			if inspection.Status != test.wantStatus {
				t.Fatalf("status=%q want=%q", inspection.Status, test.wantStatus)
			}
			if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0].args, wantArgs) {
				t.Fatalf("unexpected legacy status command: %#v", runner.commands)
			}
		})
	}
}

func TestFedimintInspectRejectsUnexpectedLegacyAssets(t *testing.T) {
	appsRoot := t.TempDir()
	appRoot := filepath.Join(appsRoot, appmanifest.FedimintGuardianID)
	if err := os.Mkdir(appRoot, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, appmanifest.FedimintGuardianComposeFile), []byte("legacy declaration\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "unexpected.env"), []byte("unsafe\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
	if _, err := manager.Inspect(context.Background(), appmanifest.FedimintGuardianID); err == nil {
		t.Fatal("unexpected legacy assets were accepted")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("rejected legacy declaration reached Docker: %#v", runner.commands)
	}
}

func TestFedimintLifecycleStopsLegacyContainerWithoutExecutingCompose(t *testing.T) {
	appsRoot := t.TempDir()
	appRoot := filepath.Join(appsRoot, appmanifest.FedimintGuardianID)
	if err := os.Mkdir(appRoot, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, appmanifest.FedimintGuardianComposeFile), []byte("legacy declaration\n"), 0640); err != nil {
		t.Fatal(err)
	}
	containerID := strings.Repeat("d", 64)
	statusArgs := []string{
		"ps",
		"--filter", "label=com.docker.compose.project=" + appmanifest.FedimintGuardianProject,
		"--filter", "label=com.docker.compose.service=" + appmanifest.FedimintGuardianPrimaryService,
		"--format", "{{.ID}}",
	}
	stopArgs := []string{"stop", "--time", "60", containerID}
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, statusArgs) {
			return containerID + "\n", nil, true
		}
		if path == dockerPath && reflect.DeepEqual(args, stopArgs) {
			return containerID + "\n", nil, true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
	if err := manager.Lifecycle(context.Background(), appmanifest.FedimintGuardianID, AppLifecycleStop, false); err != nil {
		t.Fatal(err)
	}
	want := []recordedCommand{{path: dockerPath, args: statusArgs}, {path: dockerPath, args: stopArgs}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("legacy stop commands=%#v want=%#v", runner.commands, want)
	}
}

func TestFedimintLegacyLifecycleDryRunDoesNotReachDocker(t *testing.T) {
	appsRoot := t.TempDir()
	appRoot := filepath.Join(appsRoot, appmanifest.FedimintGatewayID)
	if err := os.Mkdir(appRoot, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, appmanifest.FedimintGatewayComposeFile), []byte("legacy declaration\n"), 0640); err != nil {
		t.Fatal(err)
	}
	runner := &composeRecordingRunner{}
	manager := &ComposeAppManager{Runner: runner, AppsRoot: appsRoot}
	if err := manager.Lifecycle(context.Background(), appmanifest.FedimintGatewayID, AppLifecycleStop, true); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("legacy dry run executed commands: %#v", runner.commands)
	}
}

func TestFedimintLogsUseFixedBrokerComposeCommand(t *testing.T) {
	fixture := writeTestFedimintGateway(t)
	fixture.runner.hook = func(path string, args []string) (string, error, bool) {
		if path == dockerPath && hasArgsSuffix(args, "logs", "--no-color", "--tail", "12", "--since", "2h", appmanifest.FedimintGatewayPrimaryService) {
			return "gatewayd | first\ngatewayd | second\n", nil, true
		}
		return "", nil, false
	}
	state, err := fixture.manager.Logs(context.Background(), appmanifest.FedimintGatewayID, 12, "2h")
	if err != nil {
		t.Fatal(err)
	}
	if state.Source != "docker:gatewayd" || len(state.Lines) != 2 {
		t.Fatalf("unexpected log result: %#v", state)
	}
	if _, err := fixture.manager.Logs(context.Background(), appmanifest.FedimintGatewayID, 12, "--all"); err == nil {
		t.Fatal("expected option-like since value to be rejected")
	}
}
