package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

type recordedCommand struct {
	path string
	args []string
}

type recordingRunner struct {
	commands []recordedCommand
	output   string
	err      error
}

type blockingRunner struct{}

func (runner *blockingRunner) Run(ctx context.Context, path string, args ...string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (runner *recordingRunner) Run(ctx context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	return runner.output, runner.err
}

type recordingAudit struct {
	events []AuditEvent
	err    error
}

func (audit *recordingAudit) Write(event AuditEvent) error {
	if audit.err != nil {
		return audit.err
	}
	audit.events = append(audit.events, event)
	return nil
}

type recordingLocker struct {
	locks   int
	unlocks int
	err     error
}

type recordingConfigFiles struct {
	calls   int
	dryRun  bool
	changed bool
	err     error
}

type recordingBitcoinStorage struct {
	calls   int
	dataDir string
	dryRun  bool
	state   BitcoinCoreStorageState
	err     error
}

func (storage *recordingBitcoinStorage) Ensure(_ context.Context, dataDir string, dryRun bool) (BitcoinCoreStorageState, error) {
	storage.calls++
	storage.dataDir = dataDir
	storage.dryRun = dryRun
	return storage.state, storage.err
}

type recordingApps struct {
	calls             int
	inspectCalls      int
	removeCalls       int
	dockerCalls       int
	dockerStatusCalls int
	prepareCalls      int
	statusCalls       int
	probeCalls        int
	firewallCalls     int
	appID             string
	action            AppLifecycleAction
	dryRun            bool
	inspection        AppInspection
	err               error
	imageState        AppImageState
	imageProbe        AppImageProbe
	firewallState     AppFirewallState
	dockerState       DockerRuntimeState
	variant           appmanifest.CPUMinerImageVariant
}

func (apps *recordingApps) EnsureDockerRuntime(_ context.Context, dryRun bool) (DockerRuntimeState, error) {
	apps.dockerCalls++
	apps.dryRun = dryRun
	return apps.dockerState, apps.err
}

func (apps *recordingApps) DockerRuntimeStatus(_ context.Context) (DockerRuntimeState, error) {
	apps.dockerStatusCalls++
	return apps.dockerState, apps.err
}

func (apps *recordingApps) PrepareImage(_ context.Context, appID string, variant appmanifest.CPUMinerImageVariant, dryRun bool) (AppImageState, error) {
	apps.prepareCalls++
	apps.appID = appID
	apps.variant = variant
	apps.dryRun = dryRun
	return apps.imageState, apps.err
}

func (apps *recordingApps) ImageStatus(_ context.Context, appID string, variant appmanifest.CPUMinerImageVariant) (AppImageState, error) {
	apps.statusCalls++
	apps.appID = appID
	apps.variant = variant
	return apps.imageState, apps.err
}

func (apps *recordingApps) ProbeImage(_ context.Context, appID string, variant appmanifest.CPUMinerImageVariant, dryRun bool) (AppImageProbe, error) {
	apps.probeCalls++
	apps.appID = appID
	apps.variant = variant
	apps.dryRun = dryRun
	return apps.imageProbe, apps.err
}

func (apps *recordingApps) EnsureFirewallAccess(_ context.Context, appID string, dryRun bool) (AppFirewallState, error) {
	apps.firewallCalls++
	apps.appID = appID
	apps.dryRun = dryRun
	return apps.firewallState, apps.err
}

func (apps *recordingApps) Inspect(_ context.Context, appID string) (AppInspection, error) {
	apps.inspectCalls++
	apps.appID = appID
	return apps.inspection, apps.err
}

func (apps *recordingApps) Lifecycle(_ context.Context, appID string, action AppLifecycleAction, dryRun bool) error {
	apps.calls++
	apps.appID = appID
	apps.action = action
	apps.dryRun = dryRun
	return apps.err
}

func (apps *recordingApps) Remove(_ context.Context, appID string, dryRun bool) error {
	apps.removeCalls++
	apps.appID = appID
	apps.dryRun = dryRun
	return apps.err
}

func (files *recordingConfigFiles) EnableLogin(ctx context.Context, dryRun bool) (bool, error) {
	files.calls++
	files.dryRun = dryRun
	return files.changed, files.err
}

func (locker *recordingLocker) Lock(ctx context.Context) (func(), error) {
	if locker.err != nil {
		return nil, locker.err
	}
	locker.locks++
	return func() { locker.unlocks++ }, nil
}

func TestBrokerServiceRestartUsesFixedCommand(t *testing.T) {
	runner := &recordingRunner{}
	audit := &recordingAudit{}
	locker := &recordingLocker{}
	broker := testBroker(runner, audit, locker)
	request := serviceRestartRequest(t, false, true)

	response := broker.Handle(context.Background(), request)
	if !response.OK {
		t.Fatalf("unexpected response error: %+v", response.Error)
	}
	want := []recordedCommand{{path: systemctlPath, args: []string{"restart", "--no-block", "lnd"}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
	if locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("lock counts = %d/%d, want 1/1", locker.locks, locker.unlocks)
	}
	if len(audit.events) != 2 || audit.events[0].Phase != "start" || audit.events[1].Phase != "complete" {
		t.Fatalf("unexpected audit events: %#v", audit.events)
	}
	if audit.events[1].Operation != OperationServiceRestart || !audit.events[1].Success {
		t.Fatalf("unexpected completion event: %#v", audit.events[1])
	}
}

func TestBrokerManagerRestartUsesFixedTransientUnit(t *testing.T) {
	runner := &recordingRunner{}
	audit := &recordingAudit{}
	locker := &recordingLocker{}
	broker := testBroker(runner, audit, locker)
	params, err := MarshalParams(ServiceRestartParams{Unit: "lightningos-manager", NoBlock: true})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Version: ProtocolVersion, RequestID: "manager_restart_1", Operation: OperationServiceRestart, Params: params,
	}

	response := broker.Handle(context.Background(), request)
	if !response.OK {
		t.Fatalf("unexpected response error: %+v", response.Error)
	}
	want := []recordedCommand{{path: systemdRunPath, args: []string{
		"--quiet",
		"--collect",
		"--unit=lightningos-manager-restart-manager_restart_1",
		"--on-active=1s",
		systemctlPath,
		"restart",
		"lightningos-manager",
	}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
	if locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("lock counts = %d/%d, want 1/1", locker.locks, locker.unlocks)
	}
	if len(audit.events) != 2 || audit.events[1].Phase != "complete" || !audit.events[1].Success {
		t.Fatalf("unexpected audit events: %#v", audit.events)
	}
}

func TestBrokerDryRunValidatesWithoutExecutionOrLock(t *testing.T) {
	runner := &recordingRunner{}
	locker := &recordingLocker{}
	broker := testBroker(runner, &recordingAudit{}, locker)
	request := serviceRestartRequest(t, true, false)

	response := broker.Handle(context.Background(), request)
	if !response.OK {
		t.Fatalf("unexpected response error: %+v", response.Error)
	}
	if len(runner.commands) != 0 || locker.locks != 0 {
		t.Fatalf("dry run executed command or lock: commands=%d locks=%d", len(runner.commands), locker.locks)
	}
}

func TestBrokerAuditFailurePreventsMutation(t *testing.T) {
	runner := &recordingRunner{}
	broker := testBroker(runner, &recordingAudit{err: errors.New("disk full")}, &recordingLocker{})
	response := broker.Handle(context.Background(), serviceRestartRequest(t, false, false))
	if response.OK || response.Error == nil || response.Error.Code != "audit_unavailable" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(runner.commands) != 0 {
		t.Fatal("mutation executed without an audit start record")
	}
}

func TestBrokerServiceStatusAcceptsInactiveExit(t *testing.T) {
	runner := &recordingRunner{output: "inactive\n", err: errors.New("exit status 3")}
	broker := testBroker(runner, &recordingAudit{}, &recordingLocker{})
	params, err := MarshalParams(ServiceStatusParams{Unit: "lnd"})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "status_1", Operation: OperationServiceStatus, Params: params,
	})
	if !response.OK {
		t.Fatalf("unexpected response error: %+v", response.Error)
	}
	want := []recordedCommand{{path: systemctlPath, args: []string{"is-active", "lnd"}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestBrokerReportsExecutionTimeout(t *testing.T) {
	audit := &recordingAudit{}
	broker := testBroker(&blockingRunner{}, audit, &recordingLocker{})
	broker.Timeout = time.Millisecond
	response := broker.Handle(context.Background(), serviceRestartRequest(t, false, false))
	if response.OK || response.Error == nil || response.Error.Code != "timeout" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(audit.events) != 2 || audit.events[1].ErrorCode != "timeout" {
		t.Fatalf("unexpected audit events: %#v", audit.events)
	}
}

func TestBrokerEnableLoginUsesTypedFileManagerAndLock(t *testing.T) {
	files := &recordingConfigFiles{changed: true}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Files = files
	request := emptyParamsRequest(t, OperationFilesEnableLogin, false)
	response := broker.Handle(context.Background(), request)
	if !response.OK {
		t.Fatalf("unexpected response: %#v", response)
	}
	if files.calls != 1 || files.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("files=%#v locker=%#v", files, locker)
	}
}

func TestBrokerEnableLoginDryRunDoesNotLock(t *testing.T) {
	files := &recordingConfigFiles{changed: true}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Files = files
	response := broker.Handle(context.Background(), emptyParamsRequest(t, OperationFilesEnableLogin, true))
	if !response.OK || files.calls != 1 || !files.dryRun || locker.locks != 0 {
		t.Fatalf("response=%#v files=%#v locker=%#v", response, files, locker)
	}
}

func TestBrokerEnableLoginFailureIsGeneric(t *testing.T) {
	files := &recordingConfigFiles{err: errors.New("sensitive internal detail")}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Files = files
	response := broker.Handle(context.Background(), emptyParamsRequest(t, OperationFilesEnableLogin, false))
	if response.OK || response.Error == nil || response.Error.Code != "file_update_failed" || response.Error.Message != "enable login config update failed" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBrokerAppLifecycleUsesTypedManagerAndLock(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppLifecycleParams{AppID: "cpuminer", Action: AppLifecycleStart})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_start_1", Operation: OperationAppLifecycle, Params: params,
	})
	if !response.OK {
		t.Fatalf("unexpected response: %#v", response)
	}
	if apps.calls != 1 || apps.appID != "cpuminer" || apps.action != AppLifecycleStart || apps.dryRun {
		t.Fatalf("unexpected app call: %#v", apps)
	}
	if locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("lock counts = %d/%d, want 1/1", locker.locks, locker.unlocks)
	}
}

func TestBrokerAppLifecycleDryRunDoesNotLock(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppLifecycleParams{AppID: "cpuminer", Action: AppLifecycleStop})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_stop_1", Operation: OperationAppLifecycle, DryRun: true, Params: params,
	})
	if !response.OK || apps.calls != 1 || !apps.dryRun || locker.locks != 0 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerDockerStatusDoesNotLock(t *testing.T) {
	apps := &recordingApps{dockerState: DockerRuntimeState{Status: "starting"}}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	response := broker.Handle(context.Background(), emptyParamsRequest(t, OperationDockerStatus, false))
	if !response.OK || apps.dockerStatusCalls != 1 || locker.locks != 0 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerAppLifecycleFailureIsGeneric(t *testing.T) {
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Apps = &recordingApps{err: errors.New("sensitive compose output")}
	params, err := MarshalParams(AppLifecycleParams{AppID: "cpuminer", Action: AppLifecycleStart})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_failure_1", Operation: OperationAppLifecycle, Params: params,
	})
	if response.OK || response.Error == nil || response.Error.Code != "app_lifecycle_failed" || response.Error.Message != "app lifecycle operation failed" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBrokerAppFirewallUsesTypedManagerAndLock(t *testing.T) {
	apps := &recordingApps{firewallState: AppFirewallState{Status: "active"}}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppFirewallParams{AppID: appmanifest.RoboSatsID})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_firewall_1", Operation: OperationAppFirewallEnsure, Params: params,
	})
	if !response.OK || apps.firewallCalls != 1 || apps.appID != appmanifest.RoboSatsID || apps.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
	var state AppFirewallState
	if err := json.Unmarshal(response.Result, &state); err != nil || state.Status != "active" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
}

func TestBrokerAppFirewallDryRunDoesNotLockOrExecute(t *testing.T) {
	apps := &recordingApps{firewallState: AppFirewallState{Status: "validated"}}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppFirewallParams{AppID: appmanifest.RoboSatsID})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_firewall_dry_1", Operation: OperationAppFirewallEnsure, DryRun: true, Params: params,
	})
	if !response.OK || apps.firewallCalls != 1 || !apps.dryRun || locker.locks != 0 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerAppFirewallFailureIsGeneric(t *testing.T) {
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Apps = &recordingApps{err: errors.New("sensitive ufw output")}
	params, err := MarshalParams(AppFirewallParams{AppID: appmanifest.RoboSatsID})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{Version: ProtocolVersion, RequestID: "app_firewall_failure_1", Operation: OperationAppFirewallEnsure, Params: params})
	if response.OK || response.Error == nil || response.Error.Code != "app_firewall_failed" || response.Error.Message != "app firewall operation failed" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBrokerAppInspectUsesTypedManagerWithoutLock(t *testing.T) {
	apps := &recordingApps{inspection: AppInspection{Status: "running", CPUPercentRaw: 42.5}}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppInspectParams{AppID: "cpuminer"})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_inspect_1", Operation: OperationAppInspect, Params: params,
	})
	if !response.OK {
		t.Fatalf("unexpected response: %#v", response)
	}
	if apps.inspectCalls != 1 || apps.appID != "cpuminer" || locker.locks != 0 {
		t.Fatalf("apps=%#v locker=%#v", apps, locker)
	}
	var result AppInspection
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, apps.inspection) {
		t.Fatalf("result=%#v want=%#v", result, apps.inspection)
	}
}

func TestBrokerAppInspectFailureIsGeneric(t *testing.T) {
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Apps = &recordingApps{err: errors.New("sensitive docker output")}
	params, err := MarshalParams(AppInspectParams{AppID: "cpuminer"})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_inspect_failure_1", Operation: OperationAppInspect, Params: params,
	})
	if response.OK || response.Error == nil || response.Error.Code != "app_inspection_failed" || response.Error.Message != "app inspection failed" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBrokerAppRemoveUsesTypedManagerAndLock(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppRemoveParams{AppID: "cpuminer"})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_remove_1", Operation: OperationAppRemove, Params: params,
	})
	if !response.OK || apps.removeCalls != 1 || apps.appID != "cpuminer" || apps.dryRun {
		t.Fatalf("response=%#v apps=%#v", response, apps)
	}
	if locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("lock counts = %d/%d, want 1/1", locker.locks, locker.unlocks)
	}
}

func TestBrokerAppRemoveDryRunDoesNotLock(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppRemoveParams{AppID: "cpuminer"})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_remove_dry_1", Operation: OperationAppRemove, DryRun: true, Params: params,
	})
	if !response.OK || apps.removeCalls != 1 || !apps.dryRun || locker.locks != 0 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerAppRemoveFailureIsGeneric(t *testing.T) {
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Apps = &recordingApps{err: errors.New("sensitive compose output")}
	params, err := MarshalParams(AppRemoveParams{AppID: "cpuminer"})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "app_remove_failure_1", Operation: OperationAppRemove, Params: params,
	})
	if response.OK || response.Error == nil || response.Error.Code != "app_remove_failed" || response.Error.Message != "app remove operation failed" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBrokerDockerEnsureUsesTypedManagerAndLock(t *testing.T) {
	apps := &recordingApps{dockerState: DockerRuntimeState{Status: "ready"}}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	response := broker.Handle(context.Background(), emptyParamsRequest(t, OperationDockerEnsure, false))
	if !response.OK || apps.dockerCalls != 1 || apps.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerDockerEnsureDryRunDoesNotLock(t *testing.T) {
	apps := &recordingApps{dockerState: DockerRuntimeState{Status: "validated"}}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	response := broker.Handle(context.Background(), emptyParamsRequest(t, OperationDockerEnsure, true))
	if !response.OK || apps.dockerCalls != 1 || !apps.dryRun || locker.locks != 0 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerAppImageOperationsUseTypedManagerAndLocks(t *testing.T) {
	params, err := MarshalParams(AppImageParams{AppID: "cpuminer", Variant: appmanifest.CPUMinerImageBaseline})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		operation Operation
		state     AppImageState
		probe     AppImageProbe
		wantLock  int
		wantCall  func(*recordingApps) int
	}{
		{name: "prepare", operation: OperationAppImagePrepare, state: AppImageState{Status: "preparing"}, wantLock: 1, wantCall: func(apps *recordingApps) int { return apps.prepareCalls }},
		{name: "status", operation: OperationAppImageStatus, state: AppImageState{Status: "ready"}, wantCall: func(apps *recordingApps) int { return apps.statusCalls }},
		{name: "probe", operation: OperationAppImageProbe, probe: AppImageProbe{Runnable: true}, wantLock: 1, wantCall: func(apps *recordingApps) int { return apps.probeCalls }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apps := &recordingApps{imageState: test.state, imageProbe: test.probe}
			locker := &recordingLocker{}
			broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
			broker.Apps = apps
			response := broker.Handle(context.Background(), Request{Version: ProtocolVersion, RequestID: "image_1", Operation: test.operation, Params: params})
			if !response.OK || test.wantCall(apps) != 1 || apps.appID != "cpuminer" || apps.variant != appmanifest.CPUMinerImageBaseline || locker.locks != test.wantLock {
				t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
			}
		})
	}
}

func TestBrokerAppImageFailureIsGeneric(t *testing.T) {
	params, err := MarshalParams(AppImageParams{AppID: "cpuminer", Variant: appmanifest.CPUMinerImageBaseline})
	if err != nil {
		t.Fatal(err)
	}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Apps = &recordingApps{err: errors.New("sensitive registry output")}
	response := broker.Handle(context.Background(), Request{Version: ProtocolVersion, RequestID: "image_failure_1", Operation: OperationAppImagePrepare, Params: params})
	if response.OK || response.Error == nil || response.Error.Code != "app_image_prepare_failed" || response.Error.Message != "app image preparation failed" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBrokerBitcoinStorageEnrollmentIsTypedLockedAndSanitized(t *testing.T) {
	params, err := MarshalParams(BitcoinCoreStorageParams{DataDir: "/mnt/bitcoin-ssd/bitcoin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		dryRun    bool
		storage   *recordingBitcoinStorage
		wantOK    bool
		wantLocks int
	}{
		{name: "real", storage: &recordingBitcoinStorage{state: BitcoinCoreStorageState{Status: "ready"}}, wantOK: true, wantLocks: 1},
		{name: "dry run", dryRun: true, storage: &recordingBitcoinStorage{state: BitcoinCoreStorageState{Status: "validated"}}, wantOK: true},
		{name: "failure", storage: &recordingBitcoinStorage{err: errors.New("sensitive mount detail")}, wantLocks: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			locker := &recordingLocker{}
			broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
			broker.BitcoinStorage = test.storage
			response := broker.Handle(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "bitcoin_storage_1", Operation: OperationBitcoinStorageEnsure,
				DryRun: test.dryRun, Params: params,
			})
			if response.OK != test.wantOK || locker.locks != test.wantLocks || test.storage.calls != 1 || test.storage.dataDir != "/mnt/bitcoin-ssd/bitcoin" || test.storage.dryRun != test.dryRun {
				t.Fatalf("response/locker/storage=%#v/%#v/%#v", response, locker, test.storage)
			}
			if !test.wantOK && (response.Error == nil || response.Error.Code != "bitcoin_storage_failed" || response.Error.Message != "bitcoin storage enrollment failed") {
				t.Fatalf("unsanitized failure response: %#v", response)
			}
		})
	}
}

func testBroker(runner CommandRunner, audit AuditSink, locker Locker) *Broker {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	return &Broker{
		Runner: runner, Audit: audit, Locker: locker, Caller: "lightningos", Timeout: time.Second,
		Now: func() time.Time {
			now = now.Add(time.Millisecond)
			return now
		},
	}
}

func serviceRestartRequest(t *testing.T, dryRun bool, noBlock bool) Request {
	t.Helper()
	params, err := MarshalParams(ServiceRestartParams{Unit: "lnd", NoBlock: noBlock})
	if err != nil {
		t.Fatal(err)
	}
	return Request{
		Version: ProtocolVersion, RequestID: "restart_1", Operation: OperationServiceRestart, DryRun: dryRun, Params: params,
	}
}

func emptyParamsRequest(t *testing.T, operation Operation, dryRun bool) Request {
	t.Helper()
	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	return Request{Version: ProtocolVersion, RequestID: "empty_params_1", Operation: operation, DryRun: dryRun, Params: params}
}
