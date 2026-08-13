package privileged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func requestWithParams(t *testing.T, operation Operation, params any, dryRun bool) Request {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return Request{Version: ProtocolVersion, RequestID: "system_integrations", Operation: operation, Params: raw, DryRun: dryRun}
}

type recordingSystemIntegrations struct {
	operation string
	params    SystemIntegrationAssetInstallParams
	dryRun    bool
	asset     SystemIntegrationAssetState
	state     SystemIntegrationsState
	err       error
}

func (manager *recordingSystemIntegrations) InstallAsset(_ context.Context, params SystemIntegrationAssetInstallParams, dryRun bool) (SystemIntegrationAssetState, error) {
	manager.operation, manager.params, manager.dryRun = "install", params, dryRun
	return manager.asset, manager.err
}

func (manager *recordingSystemIntegrations) Status(context.Context) (SystemIntegrationsState, error) {
	manager.operation = "status"
	return manager.state, manager.err
}

func (manager *recordingSystemIntegrations) Apply(_ context.Context, dryRun bool) (SystemIntegrationsState, error) {
	manager.operation, manager.dryRun = "apply", dryRun
	return manager.state, manager.err
}

func (manager *recordingSystemIntegrations) Finalize(_ context.Context, dryRun bool) (SystemIntegrationsState, error) {
	manager.operation, manager.dryRun = "finalize", dryRun
	return manager.state, manager.err
}

func TestSystemIntegrationProtocolRejectsCallerSelectedAssetsAndFields(t *testing.T) {
	valid := requestWithParams(t, OperationSystemIntegrationAssetInstall, SystemIntegrationAssetInstallParams{
		Asset: SystemIntegrationAssetTerminal, Content: "fixed",
	}, true)
	if err := ValidateRequest(valid); err != nil {
		t.Fatalf("valid asset request rejected: %v", err)
	}
	for _, raw := range []string{
		`{"asset":"attacker","content":"fixed"}`,
		`{"asset":"terminal","content":""}`,
		`{"asset":"terminal","content":"fixed","path":"/tmp/owned"}`,
	} {
		request := valid
		request.Params = []byte(raw)
		if err := ValidateRequest(request); err == nil {
			t.Fatalf("unsafe asset request accepted: %s", raw)
		}
	}
	for _, operation := range []Operation{OperationSystemIntegrationsApply, OperationSystemIntegrationsFinalize} {
		if err := ValidateRequest(emptyParamsRequest(t, operation, true)); err != nil {
			t.Fatalf("fixed %s request rejected: %v", operation, err)
		}
		request := emptyParamsRequest(t, operation, true)
		request.Params = []byte(`{"command":"/bin/sh"}`)
		if err := ValidateRequest(request); err == nil {
			t.Fatalf("caller-selected command accepted by %s", operation)
		}
	}
	if err := ValidateRequest(emptyParamsRequest(t, OperationSystemIntegrationsStatus, false)); err != nil {
		t.Fatalf("fixed status request rejected: %v", err)
	}
	if err := ValidateRequest(emptyParamsRequest(t, OperationSystemIntegrationsStatus, true)); err == nil {
		t.Fatal("system integrations status accepted dry_run")
	}
}

func TestSystemIntegrationBrokerDispatchesTypedMutations(t *testing.T) {
	locker := &recordingLocker{}
	audit := &recordingAudit{}
	manager := &recordingSystemIntegrations{asset: SystemIntegrationAssetState{Status: "ready", Changed: true}}
	broker := &Broker{Runner: &recordingRunner{}, Locker: locker, Audit: audit, SystemIntegrations: manager, Caller: "test"}
	request := requestWithParams(t, OperationSystemIntegrationAssetInstall, SystemIntegrationAssetInstallParams{
		Asset: SystemIntegrationAssetTerminal, Content: "fixed",
	}, false)
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.operation != "install" || manager.params.Asset != SystemIntegrationAssetTerminal || manager.params.Content != "fixed" || manager.dryRun {
		t.Fatalf("unexpected asset dispatch: response=%+v manager=%+v", response, manager)
	}
	if locker.locks != 1 || locker.unlocks != 1 || len(audit.events) != 2 {
		t.Fatalf("asset mutation was not locked and audited: locker=%+v audit=%+v", locker, audit.events)
	}

	manager.state = SystemIntegrationsState{Status: "validated"}
	response = broker.Handle(context.Background(), emptyParamsRequest(t, OperationSystemIntegrationsApply, true))
	if !response.OK || manager.operation != "apply" || !manager.dryRun || locker.locks != 1 {
		t.Fatalf("unexpected apply dry-run dispatch: response=%+v manager=%+v locker=%+v", response, manager, locker)
	}
}

type integrationCommandRunner struct {
	certificatePath string
	commands        []recordedCommand
}

func (runner *integrationCommandRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	joined := strings.Join(args, " ")
	switch {
	case path == systemctlPath && strings.Contains(joined, "--property=LoadState"):
		return "loaded\n", nil
	case path == systemctlPath && strings.Contains(joined, "--property=Group"):
		return "lightningos\n", nil
	case path == envExecutablePath:
		if err := os.WriteFile(runner.certificatePath, []byte("new certificate"), 0644); err != nil {
			return "", err
		}
		return "TLS ready", nil
	default:
		return "", nil
	}
}

func TestNativeSystemIntegrationsUsePinnedAssetsAndFinalizeLast(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("root ownership assertions require root on Linux")
	}
	root := t.TempDir()
	paths := SystemIntegrationPaths{
		TerminalHelper:         filepath.Join(root, "sbin", "lightningos-terminal"),
		TerminalPasswordHelper: filepath.Join(root, "sbin", "lightningos-terminal-password"),
		ManagerFirewallHelper:  filepath.Join(root, "sbin", "lightningos-manager-firewall"),
		ManagerTLSMDNSHelper:   filepath.Join(root, "sbin", "lightningos-setup-manager-tls-mdns"),
		LNDDropIn:              filepath.Join(root, "systemd", "lnd.service.d", "20-lightningos-restart.conf"),
		ManagerTLSCertificate:  filepath.Join(root, "tls", "server.crt"),
		Marker:                 filepath.Join(root, "state", "system-integrations-v5"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.ManagerTLSCertificate), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ManagerTLSCertificate, []byte("old certificate"), 0644); err != nil {
		t.Fatal(err)
	}
	content := "#!/bin/sh\nexit 0\n"
	digest := sha256.Sum256([]byte(content))
	digestHex := hex.EncodeToString(digest[:])
	digests := map[SystemIntegrationAsset]string{}
	for _, asset := range []SystemIntegrationAsset{
		SystemIntegrationAssetTerminal,
		SystemIntegrationAssetTerminalPassword,
		SystemIntegrationAssetManagerFirewall,
		SystemIntegrationAssetManagerTLSMDNS,
	} {
		digests[asset] = digestHex
	}
	runner := &integrationCommandRunner{certificatePath: paths.ManagerTLSCertificate}
	manager := &NativeSystemIntegrationsManager{Runner: runner, Paths: paths, Digests: digests}

	bad := SystemIntegrationAssetInstallParams{Asset: SystemIntegrationAssetTerminal, Content: content + "# changed\n"}
	if _, err := manager.InstallAsset(context.Background(), bad, true); err == nil {
		t.Fatal("digest-mismatched embedded asset accepted")
	}
	if _, err := os.Stat(paths.TerminalHelper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected asset changed the destination: %v", err)
	}

	for _, asset := range []SystemIntegrationAsset{
		SystemIntegrationAssetTerminal,
		SystemIntegrationAssetTerminalPassword,
		SystemIntegrationAssetManagerFirewall,
		SystemIntegrationAssetManagerTLSMDNS,
	} {
		state, err := manager.InstallAsset(context.Background(), SystemIntegrationAssetInstallParams{Asset: asset, Content: content}, false)
		if err != nil || state.Status != "ready" || !state.Changed {
			t.Fatalf("install %s: state=%+v err=%v", asset, state, err)
		}
	}
	state, err := manager.InstallAsset(context.Background(), SystemIntegrationAssetInstallParams{Asset: SystemIntegrationAssetTerminal, Content: content}, false)
	if err != nil || state.Changed {
		t.Fatalf("idempotent asset install changed state: state=%+v err=%v", state, err)
	}

	applied, err := manager.Apply(context.Background(), false)
	if err != nil || applied.Status != "ready" || !applied.CertificateChanged || !applied.LNDPolicyChanged {
		t.Fatalf("apply integrations: state=%+v err=%v", applied, err)
	}
	if raw, err := os.ReadFile(paths.LNDDropIn); err != nil || string(raw) != lndIntegrationDropIn {
		t.Fatalf("unexpected LND policy: %q err=%v", raw, err)
	}
	if _, err := os.Stat(paths.Marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("apply wrote final marker before external enrollment: %v", err)
	}
	if status, err := manager.Status(context.Background()); err != nil || status.Status != "absent" {
		t.Fatalf("integrations unexpectedly ready before finalization: state=%+v err=%v", status, err)
	}
	wantTLSCommand := recordedCommand{path: envExecutablePath, args: []string{
		"LIGHTNINGOS_MANAGER_GROUP=lightningos", "LIGHTNINGOS_MANAGER_PORT=8443", paths.ManagerTLSMDNSHelper,
	}}
	if !containsRecordedCommand(runner.commands, wantTLSCommand) || !containsRecordedCommand(runner.commands, recordedCommand{path: paths.ManagerFirewallHelper}) {
		t.Fatalf("fixed helpers were not invoked as catalogued: %+v", runner.commands)
	}

	final, err := manager.Finalize(context.Background(), false)
	if err != nil || final.Status != "ready" {
		t.Fatalf("finalize integrations: state=%+v err=%v", final, err)
	}
	if raw, err := os.ReadFile(paths.Marker); err != nil || string(raw) != systemIntegrationsMarkerContent {
		t.Fatalf("unexpected final marker: %q err=%v", raw, err)
	}
	if status, err := manager.Status(context.Background()); err != nil || status.Status != "ready" {
		t.Fatalf("integrations not ready after finalization: state=%+v err=%v", status, err)
	}
}

func TestSystemIntegrationsMarkerIsOutsideManagerSharedState(t *testing.T) {
	markerDir := filepath.Dir(defaultSystemIntegrationsMarker)
	if markerDir != filepath.Clean("/var/lib/lightningos-privileged") {
		t.Fatalf("system integrations marker is not in the broker-owned state root: %s", markerDir)
	}
	if isManagerSharedStorageRoot(markerDir) {
		t.Fatalf("system integrations marker would mutate a manager shared storage root: %s", markerDir)
	}
}

func containsRecordedCommand(commands []recordedCommand, expected recordedCommand) bool {
	for _, command := range commands {
		if command.path == expected.path && reflect.DeepEqual(command.args, expected.args) {
			return true
		}
	}
	return false
}
