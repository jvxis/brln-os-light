package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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

type recordingBitcoinConfig struct {
	operation          string
	dataDir            string
	content            string
	generateRPCAuth    bool
	dryRun             bool
	state              BitcoinCoreConfigState
	credentials        BitcoinCoreCredentialsState
	ensuredCredentials BitcoinCoreCredentialsEnsureState
	electrsCredentials BitcoinCoreElectrsCredentialsState
	err                error
}

type recordingLoopManager struct {
	operation string
	action    AppLifecycleAction
	params    LoopEnsureParams
	dryRun    bool
	state     LoopState
	err       error
}

type recordingPeerSwapManager struct {
	operation   string
	action      AppLifecycleAction
	params      PeerSwapEnsureParams
	source      PeerSwapSource
	dryRun      bool
	state       PeerSwapState
	sourceState PeerSwapSourceState
	err         error
}

func (manager *recordingPeerSwapManager) Status(context.Context) (PeerSwapState, error) {
	manager.operation = "status"
	return manager.state, manager.err
}

func (manager *recordingPeerSwapManager) Source(context.Context) (PeerSwapSourceState, error) {
	manager.operation = "source-read"
	return manager.sourceState, manager.err
}

func (manager *recordingPeerSwapManager) WriteSource(_ context.Context, source PeerSwapSource, dryRun bool) error {
	manager.operation, manager.source, manager.dryRun = "source-write", source, dryRun
	return manager.err
}

func (manager *recordingPeerSwapManager) Ensure(_ context.Context, params PeerSwapEnsureParams, dryRun bool) (PeerSwapState, error) {
	manager.operation, manager.params, manager.dryRun = "ensure", params, dryRun
	return manager.state, manager.err
}

func (manager *recordingPeerSwapManager) Lifecycle(_ context.Context, action AppLifecycleAction, dryRun bool) (PeerSwapState, error) {
	manager.operation, manager.action, manager.dryRun = "lifecycle", action, dryRun
	return manager.state, manager.err
}

func (manager *recordingPeerSwapManager) Remove(_ context.Context, dryRun bool) error {
	manager.operation, manager.dryRun = "remove", dryRun
	return manager.err
}

func (manager *recordingLoopManager) Status(context.Context) (LoopState, error) {
	manager.operation = "status"
	return manager.state, manager.err
}

func (manager *recordingLoopManager) Ensure(_ context.Context, params LoopEnsureParams, dryRun bool) (LoopState, error) {
	manager.operation = "ensure"
	manager.params = params
	manager.dryRun = dryRun
	return manager.state, manager.err
}

func (manager *recordingLoopManager) Lifecycle(_ context.Context, action AppLifecycleAction, dryRun bool) (LoopState, error) {
	manager.operation = "lifecycle"
	manager.action = action
	manager.dryRun = dryRun
	return manager.state, manager.err
}

func (manager *recordingLoopManager) Remove(_ context.Context, dryRun bool) error {
	manager.operation = "remove"
	manager.dryRun = dryRun
	return manager.err
}

func (manager *recordingLoopManager) EnsurePermissions(_ context.Context, dryRun bool) error {
	manager.operation = "permissions"
	manager.dryRun = dryRun
	return manager.err
}

func (manager *recordingLoopManager) EnsureClientMaterial(_ context.Context, dryRun bool) error {
	manager.operation = "client-material"
	manager.dryRun = dryRun
	return manager.err
}

func (config *recordingBitcoinConfig) Ensure(_ context.Context, dataDir string, content string, generateRPCAuth bool, dryRun bool) (BitcoinCoreConfigState, error) {
	config.operation = "ensure"
	config.dataDir = dataDir
	config.content = content
	config.generateRPCAuth = generateRPCAuth
	config.dryRun = dryRun
	return config.state, config.err
}

func (config *recordingBitcoinConfig) Credentials(_ context.Context, dataDir string) (BitcoinCoreCredentialsState, error) {
	config.operation = "credentials"
	config.dataDir = dataDir
	return config.credentials, config.err
}

func (config *recordingBitcoinConfig) EnsureCredentials(_ context.Context, dataDir string, dryRun bool) (BitcoinCoreCredentialsEnsureState, error) {
	config.operation = "credentials-ensure"
	config.dataDir = dataDir
	config.dryRun = dryRun
	return config.ensuredCredentials, config.err
}

func (config *recordingBitcoinConfig) EnsureElectrsCredentials(_ context.Context, dataDir string, dryRun bool) (BitcoinCoreElectrsCredentialsState, error) {
	config.operation = "electrs-credentials"
	config.dataDir = dataDir
	config.dryRun = dryRun
	return config.electrsCredentials, config.err
}

func (config *recordingBitcoinConfig) Read(_ context.Context, dataDir string) (BitcoinCoreConfigState, error) {
	config.operation = "read"
	config.dataDir = dataDir
	return config.state, config.err
}

func (config *recordingBitcoinConfig) Write(_ context.Context, dataDir string, content string, dryRun bool) (BitcoinCoreConfigState, error) {
	config.operation = "write"
	config.dataDir = dataDir
	config.content = content
	config.dryRun = dryRun
	return config.state, config.err
}

func (storage *recordingBitcoinStorage) Ensure(_ context.Context, dataDir string, dryRun bool) (BitcoinCoreStorageState, error) {
	storage.calls++
	storage.dataDir = dataDir
	storage.dryRun = dryRun
	return storage.state, storage.err
}

type recordingApps struct {
	calls                   int
	inspectCalls            int
	snapshotCalls           int
	removeCalls             int
	adminResetCalls         int
	dockerCalls             int
	dockerStatusCalls       int
	prepareCalls            int
	statusCalls             int
	probeCalls              int
	firewallCalls           int
	consumerNetworkCalls    int
	bitcoinStatusCalls      int
	appID                   string
	action                  AppLifecycleAction
	dryRun                  bool
	inspection              AppInspection
	err                     error
	imageState              AppImageState
	imageProbe              AppImageProbe
	firewallState           AppFirewallState
	consumerNetworkState    BitcoinConsumerNetworkState
	consumerNetworkDeadline time.Time
	bitcoinStatusState      BitcoinCoreStatusState
	dockerState             DockerRuntimeState
	variant                 appmanifest.CPUMinerImageVariant
	lifecycleDeadline       time.Time
}

func (apps *recordingApps) EnsureBitcoinConsumerNetwork(ctx context.Context, dryRun bool) (BitcoinConsumerNetworkState, error) {
	apps.consumerNetworkCalls++
	apps.dryRun = dryRun
	apps.consumerNetworkDeadline, _ = ctx.Deadline()
	return apps.consumerNetworkState, apps.err
}

func (apps *recordingApps) BitcoinCoreStatus(_ context.Context) (BitcoinCoreStatusState, error) {
	apps.bitcoinStatusCalls++
	return apps.bitcoinStatusState, apps.err
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

func (apps *recordingApps) Lifecycle(ctx context.Context, appID string, action AppLifecycleAction, dryRun bool) error {
	apps.calls++
	apps.appID = appID
	apps.lifecycleDeadline, _ = ctx.Deadline()
	apps.action = action
	apps.dryRun = dryRun
	return apps.err
}

func (apps *recordingApps) Snapshot(_ context.Context, appID string, dryRun bool) error {
	apps.snapshotCalls++
	apps.appID = appID
	apps.dryRun = dryRun
	return apps.err
}

func (apps *recordingApps) Remove(_ context.Context, appID string, dryRun bool) error {
	apps.removeCalls++
	apps.appID = appID
	apps.dryRun = dryRun
	return apps.err
}

func (apps *recordingApps) ResetAdmin(_ context.Context, appID string, dryRun bool) error {
	apps.adminResetCalls++
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

func TestBrokerBTCPayLifecycleUsesTypedManagerAndLock(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppLifecycleParams{AppID: appmanifest.BTCPayID, Action: AppLifecycleStart})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "btcpay_start_1", Operation: OperationAppLifecycle, Params: params,
	})
	if !response.OK || apps.calls != 1 || apps.appID != appmanifest.BTCPayID || apps.action != AppLifecycleStart || apps.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerBTCPayLifecycleAllowsBoundedContainerRecreate(t *testing.T) {
	apps := &recordingApps{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Apps = apps
	broker.Timeout = 15 * time.Second
	params, err := MarshalParams(AppLifecycleParams{AppID: appmanifest.BTCPayID, Action: AppLifecycleStart})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "btcpay_long_start_1", Operation: OperationAppLifecycle, Params: params,
	})
	if !response.OK {
		t.Fatalf("unexpected response: %#v", response)
	}
	remaining := time.Until(apps.lifecycleDeadline)
	if remaining < privilegedLongOperationTimeout-time.Second || apps.lifecycleDeadline.After(started.Add(privilegedLongOperationTimeout+time.Second)) {
		t.Fatalf("BTCPay lifecycle deadline = %v (remaining %v), want bounded %v window", apps.lifecycleDeadline, remaining, privilegedLongOperationTimeout)
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
	remaining := time.Until(apps.lifecycleDeadline)
	if remaining < 500*time.Millisecond || remaining > time.Second {
		t.Fatalf("dry-run lifecycle deadline has %v remaining, want configured short timeout", remaining)
	}
}

func TestBrokerBTCPaySnapshotUsesTypedManagerAndLock(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppSnapshotParams{AppID: appmanifest.BTCPayID})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "btcpay_snapshot_1", Operation: OperationAppSnapshot, Params: params,
	})
	if !response.OK || apps.snapshotCalls != 1 || apps.appID != appmanifest.BTCPayID || apps.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerBTCPaySnapshotDryRunAndFailureAreSafe(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppSnapshotParams{AppID: appmanifest.BTCPayID})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "btcpay_snapshot_dry_1", Operation: OperationAppSnapshot, DryRun: true, Params: params,
	})
	if !response.OK || apps.snapshotCalls != 1 || !apps.dryRun || locker.locks != 0 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
	apps.err = errors.New("secret macaroon bytes")
	response = broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "btcpay_snapshot_fail_1", Operation: OperationAppSnapshot, Params: params,
	})
	if response.OK || response.Error == nil || response.Error.Code != "app_snapshot_failed" || response.Error.Message != "app snapshot operation failed" {
		t.Fatalf("snapshot failure leaked detail: %#v", response)
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

func TestBrokerLNDgAdminResetUsesTypedManagerAndLock(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppAdminResetParams{AppID: appmanifest.LNDgID})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "lndg_admin_reset_1", Operation: OperationAppAdminReset, Params: params,
	})
	if !response.OK || apps.adminResetCalls != 1 || apps.appID != appmanifest.LNDgID || apps.dryRun {
		t.Fatalf("response=%#v apps=%#v", response, apps)
	}
	if locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("lock counts = %d/%d, want 1/1", locker.locks, locker.unlocks)
	}
}

func TestBrokerLNDgAdminResetDryRunDoesNotLock(t *testing.T) {
	apps := &recordingApps{}
	locker := &recordingLocker{}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
	broker.Apps = apps
	params, err := MarshalParams(AppAdminResetParams{AppID: appmanifest.LNDgID})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "lndg_admin_reset_dry_1", Operation: OperationAppAdminReset, DryRun: true, Params: params,
	})
	if !response.OK || apps.adminResetCalls != 1 || !apps.dryRun || locker.locks != 0 {
		t.Fatalf("response=%#v apps=%#v locker=%#v", response, apps, locker)
	}
}

func TestBrokerLNDgAdminResetFailureIsGeneric(t *testing.T) {
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Apps = &recordingApps{err: errors.New("sensitive database output")}
	params, err := MarshalParams(AppAdminResetParams{AppID: appmanifest.LNDgID})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "lndg_admin_reset_failure_1", Operation: OperationAppAdminReset, Params: params,
	})
	if response.OK || response.Error == nil || response.Error.Code != "app_admin_reset_failed" || response.Error.Message != "app admin reset failed" {
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

func TestBrokerBitcoinConsumerNetworkIsClosedLockedAndSanitized(t *testing.T) {
	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		dryRun    bool
		apps      *recordingApps
		wantOK    bool
		wantLocks int
	}{
		{name: "real", apps: &recordingApps{consumerNetworkState: BitcoinConsumerNetworkState{Status: "ready"}}, wantOK: true, wantLocks: 1},
		{name: "dry run", dryRun: true, apps: &recordingApps{consumerNetworkState: BitcoinConsumerNetworkState{Status: "validated"}}, wantOK: true},
		{name: "failure", apps: &recordingApps{err: errors.New("sensitive docker detail")}, wantLocks: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			locker := &recordingLocker{}
			broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
			broker.Apps = test.apps
			response := broker.Handle(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "bitcoin_consumer_network_1", Operation: OperationBitcoinConsumerNetworkEnsure,
				DryRun: test.dryRun, Params: params,
			})
			if response.OK != test.wantOK || locker.locks != test.wantLocks || test.apps.consumerNetworkCalls != 1 || test.apps.dryRun != test.dryRun {
				t.Fatalf("response/locker/apps=%#v/%#v/%#v", response, locker, test.apps)
			}
			remaining := time.Until(test.apps.consumerNetworkDeadline)
			if test.dryRun {
				if remaining < 500*time.Millisecond || remaining > time.Second {
					t.Fatalf("dry-run consumer network deadline has %v remaining, want configured short timeout", remaining)
				}
			} else if remaining < privilegedLongOperationTimeout-time.Second || remaining > privilegedLongOperationTimeout {
				t.Fatalf("consumer network deadline has %v remaining, want %v", remaining, privilegedLongOperationTimeout)
			}
			if !test.wantOK && (response.Error == nil || response.Error.Code != "bitcoin_consumer_network_failed" || response.Error.Message != "bitcoin consumer network ensure failed") {
				t.Fatalf("unsanitized failure response: %#v", response)
			}
		})
	}
}

func TestBrokerBitcoinStatusIsReadOnlyAndSanitizesFailures(t *testing.T) {
	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	state := BitcoinCoreStatusState{
		Chain: "main", Blocks: 100, Headers: 100, VerificationProgress: 1,
		BestBlockHash: strings.Repeat("0", 64), NetworkOK: true, Version: 310100,
		Subversion: "/Satoshi:31.1.0/", Connections: 8,
	}
	for _, test := range []struct {
		name   string
		apps   *recordingApps
		wantOK bool
	}{
		{name: "ready", apps: &recordingApps{bitcoinStatusState: state}, wantOK: true},
		{name: "failure", apps: &recordingApps{err: errors.New("cookie and container detail")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			locker := &recordingLocker{}
			broker := testBroker(&recordingRunner{}, &recordingAudit{}, locker)
			broker.Apps = test.apps
			response := broker.Handle(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "bitcoin_status_1", Operation: OperationBitcoinStatus, Params: params,
			})
			if response.OK != test.wantOK || test.apps.bitcoinStatusCalls != 1 || locker.locks != 0 {
				t.Fatalf("response/apps/locker=%#v/%#v/%#v", response, test.apps, locker)
			}
			if !test.wantOK && (response.Error == nil || response.Error.Code != "bitcoin_status_failed" || response.Error.Message != "bitcoin core status failed") {
				t.Fatalf("unsanitized status failure: %#v", response)
			}
		})
	}
}

func TestBrokerBitcoinConfigOperationsLockMutationsAndNeverAuditSecrets(t *testing.T) {
	const content = "server=1\nrpcpassword=never-audit-me\n"
	for _, test := range []struct {
		name      string
		operation Operation
		dryRun    bool
		state     BitcoinCoreConfigState
		wantCall  string
		wantLocks int
	}{
		{name: "ensure", operation: OperationBitcoinConfigEnsure, state: BitcoinCoreConfigState{Status: "ready"}, wantCall: "ensure", wantLocks: 1},
		{name: "ensure dry run", operation: OperationBitcoinConfigEnsure, dryRun: true, state: BitcoinCoreConfigState{Status: "validated"}, wantCall: "ensure"},
		{name: "read", operation: OperationBitcoinConfigRead, state: BitcoinCoreConfigState{Status: "ready", Content: content}, wantCall: "read"},
		{name: "write", operation: OperationBitcoinConfigWrite, state: BitcoinCoreConfigState{Status: "ready"}, wantCall: "write", wantLocks: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			audit := &recordingAudit{}
			locker := &recordingLocker{}
			config := &recordingBitcoinConfig{state: test.state}
			broker := testBroker(&recordingRunner{}, audit, locker)
			broker.BitcoinConfig = config
			var params json.RawMessage
			var err error
			if test.operation == OperationBitcoinConfigRead {
				params, err = MarshalParams(BitcoinCoreConfigTargetParams{DataDir: "/data/bitcoin"})
			} else {
				params, err = MarshalParams(BitcoinCoreConfigWriteParams{DataDir: "/data/bitcoin", Content: content})
			}
			if err != nil {
				t.Fatal(err)
			}
			response := broker.Handle(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "bitcoin_config_1", Operation: test.operation, DryRun: test.dryRun, Params: params,
			})
			if !response.OK || config.operation != test.wantCall || config.dataDir != "/data/bitcoin" || locker.locks != test.wantLocks {
				t.Fatalf("response/config/locker=%#v/%#v/%#v", response, config, locker)
			}
			encodedAudit, err := json.Marshal(audit.events)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encodedAudit), "never-audit-me") {
				t.Fatalf("secret leaked into audit: %s", encodedAudit)
			}
		})
	}
}

func TestBrokerBitcoinConfigFailureIsGeneric(t *testing.T) {
	const secret = "rpcpassword=do-not-echo\n"
	params, err := MarshalParams(BitcoinCoreConfigWriteParams{DataDir: "/data/bitcoin", Content: secret})
	if err != nil {
		t.Fatal(err)
	}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.BitcoinConfig = &recordingBitcoinConfig{err: errors.New("failed while handling " + secret)}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "bitcoin_config_failure", Operation: OperationBitcoinConfigWrite, Params: params,
	})
	if response.OK || response.Error == nil || response.Error.Code != "bitcoin_config_failed" || response.Error.Message != "bitcoin config update failed" || strings.Contains(response.Error.Message, "do-not-echo") {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBrokerBitcoinCredentialsReadIsTypedUnlockedAndNeverAudited(t *testing.T) {
	const password = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	params, err := MarshalParams(BitcoinCoreConfigTargetParams{DataDir: "/data/bitcoin"})
	if err != nil {
		t.Fatal(err)
	}
	audit := &recordingAudit{}
	locker := &recordingLocker{}
	config := &recordingBitcoinConfig{credentials: BitcoinCoreCredentialsState{
		Status: "ready", User: appmanifest.BitcoinCoreRPCUser, Password: password,
	}}
	broker := testBroker(&recordingRunner{}, audit, locker)
	broker.BitcoinConfig = config
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "bitcoin_credentials_1", Operation: OperationBitcoinCredentialsRead, Params: params,
	})
	if !response.OK || config.operation != "credentials" || config.dataDir != "/data/bitcoin" || locker.locks != 0 {
		t.Fatalf("response/config/locker=%#v/%#v/%#v", response, config, locker)
	}
	if strings.Contains(fmt.Sprintf("%#v", audit.events), password) {
		t.Fatal("RPC password leaked into audit")
	}
}

func TestBrokerBitcoinElectrsCredentialsEnsureIsTypedLockedAndNeverAudited(t *testing.T) {
	const password = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	params, err := MarshalParams(BitcoinCoreConfigTargetParams{DataDir: "/data/bitcoin"})
	if err != nil {
		t.Fatal(err)
	}
	audit := &recordingAudit{}
	locker := &recordingLocker{}
	config := &recordingBitcoinConfig{electrsCredentials: BitcoinCoreElectrsCredentialsState{
		Status: "restart_required", User: appmanifest.ElectrsBitcoinRPCUser, Password: password, ConfigChanged: true,
	}}
	broker := testBroker(&recordingRunner{}, audit, locker)
	broker.BitcoinConfig = config
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "bitcoin_electrs_credentials_1", Operation: OperationBitcoinElectrsCredentialsEnsure, Params: params,
	})
	if !response.OK || config.operation != "electrs-credentials" || config.dataDir != "/data/bitcoin" || locker.locks != 1 {
		t.Fatalf("response/config/locker=%#v/%#v/%#v", response, config, locker)
	}
	if strings.Contains(fmt.Sprintf("%#v", audit.events), password) {
		t.Fatal("Electrs RPC password leaked into audit")
	}
}

func TestBrokerLoopOperationsAreTypedLockedAndNeverAuditCredentialBytes(t *testing.T) {
	const certificate = "loop-certificate-never-audit"
	const macaroon = "loop-macaroon-never-audit"
	for _, test := range []struct {
		name      string
		operation Operation
		dryRun    bool
		params    any
		wantCall  string
		wantLocks int
	}{
		{name: "status", operation: OperationLoopStatus, params: struct{}{}, wantCall: "status"},
		{name: "ensure", operation: OperationLoopEnsure, params: LoopEnsureParams{LNDTLSCertificate: []byte(certificate), LNDMacaroon: []byte(macaroon)}, wantCall: "ensure", wantLocks: 1},
		{name: "ensure dry run", operation: OperationLoopEnsure, dryRun: true, params: LoopEnsureParams{LNDTLSCertificate: []byte(certificate)}, wantCall: "ensure"},
		{name: "lifecycle", operation: OperationLoopLifecycle, params: LoopLifecycleParams{Action: AppLifecycleStart}, wantCall: "lifecycle", wantLocks: 1},
		{name: "remove", operation: OperationLoopRemove, params: struct{}{}, wantCall: "remove", wantLocks: 1},
		{name: "permissions", operation: OperationLoopPermissionsEnsure, params: struct{}{}, wantCall: "permissions", wantLocks: 1},
		{name: "client material", operation: OperationLoopClientMaterialEnsure, params: struct{}{}, wantCall: "client-material", wantLocks: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			rawParams, err := MarshalParams(test.params)
			if err != nil {
				t.Fatal(err)
			}
			audit := &recordingAudit{}
			locker := &recordingLocker{}
			loop := &recordingLoopManager{state: LoopState{Installed: true, Status: "running", HasLNDMacaroon: true}}
			if test.dryRun {
				loop.state = LoopState{Status: "validated"}
			}
			broker := testBroker(&recordingRunner{}, audit, locker)
			broker.Loop = loop
			response := broker.Handle(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "loop_broker_1", Operation: test.operation, DryRun: test.dryRun, Params: rawParams,
			})
			if !response.OK || loop.operation != test.wantCall || loop.dryRun != test.dryRun || locker.locks != test.wantLocks {
				t.Fatalf("response/loop/locker=%#v/%#v/%#v", response, loop, locker)
			}
			encoded, err := json.Marshal(audit.events)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), certificate) || strings.Contains(string(encoded), macaroon) {
				t.Fatalf("Loop secret leaked into audit: %s", encoded)
			}
		})
	}
}

func TestBrokerLoopFailureIsGeneric(t *testing.T) {
	const secret = "loop-secret-do-not-echo"
	params, err := MarshalParams(LoopEnsureParams{LNDTLSCertificate: []byte(secret)})
	if err != nil {
		t.Fatal(err)
	}
	broker := testBroker(&recordingRunner{}, &recordingAudit{}, &recordingLocker{})
	broker.Loop = &recordingLoopManager{err: errors.New("failed while handling " + secret)}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "loop_failure_1", Operation: OperationLoopEnsure, Params: params,
	})
	if response.OK || response.Error == nil || response.Error.Code != "loop_ensure_failed" || strings.Contains(response.Error.Message, secret) {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestBrokerPeerSwapOperationsAreTypedLockedAndNeverAuditSecrets(t *testing.T) {
	params := peerSwapBrokerTestEnsureParams(t)
	source := PeerSwapSource{Mode: appmanifest.PeerSwapElementsModeRemote, URL: "https://elements.example:7041", User: "rpc-user", Password: "peerswap-secret-never-audit", Wallet: "peerswap_node"}
	for _, test := range []struct {
		name      string
		operation Operation
		dryRun    bool
		params    any
		wantCall  string
		wantLocks int
	}{
		{name: "status", operation: OperationPeerSwapStatus, params: struct{}{}, wantCall: "status"},
		{name: "source read", operation: OperationPeerSwapSourceRead, params: struct{}{}, wantCall: "source-read"},
		{name: "source write", operation: OperationPeerSwapSourceWrite, params: source, wantCall: "source-write", wantLocks: 1},
		{name: "source write dry run", operation: OperationPeerSwapSourceWrite, dryRun: true, params: source, wantCall: "source-write"},
		{name: "ensure", operation: OperationPeerSwapEnsure, params: params, wantCall: "ensure", wantLocks: 1},
		{name: "ensure dry run", operation: OperationPeerSwapEnsure, dryRun: true, params: params, wantCall: "ensure"},
		{name: "lifecycle", operation: OperationPeerSwapLifecycle, params: PeerSwapLifecycleParams{Action: AppLifecycleRestart}, wantCall: "lifecycle", wantLocks: 1},
		{name: "remove", operation: OperationPeerSwapRemove, params: struct{}{}, wantCall: "remove", wantLocks: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			rawParams, err := MarshalParams(test.params)
			if err != nil {
				t.Fatal(err)
			}
			audit := &recordingAudit{}
			locker := &recordingLocker{}
			manager := &recordingPeerSwapManager{
				state:       PeerSwapState{Installed: true, Status: "stopped", HasLNDMacaroon: true, ElementsMode: appmanifest.PeerSwapElementsModeRemote},
				sourceState: PeerSwapSourceState{Configured: true, Source: source},
			}
			if test.dryRun && test.operation == OperationPeerSwapEnsure {
				manager.state = PeerSwapState{Status: "validated", ElementsMode: appmanifest.PeerSwapElementsModeRemote}
			}
			broker := testBroker(&recordingRunner{}, audit, locker)
			broker.PeerSwap = manager
			response := broker.Handle(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "peerswap_broker_1", Operation: test.operation, DryRun: test.dryRun, Params: rawParams,
			})
			if !response.OK || manager.operation != test.wantCall || manager.dryRun != test.dryRun || locker.locks != test.wantLocks {
				t.Fatalf("response/manager/locker=%#v/%#v/%#v", response, manager, locker)
			}
			encoded, err := json.Marshal(audit.events)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{source.Password, string(params.LNDMacaroon), string(params.LNDTLSCertificate)} {
				if strings.Contains(string(encoded), secret) {
					t.Fatalf("PeerSwap secret leaked into audit: %s", encoded)
				}
			}
		})
	}
}

func peerSwapBrokerTestEnsureParams(t *testing.T) PeerSwapEnsureParams {
	t.Helper()
	paths := appmanifest.DefaultPeerSwapPaths()
	config := "host=127.0.0.1:42069\n" +
		"lnd.host=127.0.0.1:10009\n" +
		"lnd.tlscertpath=" + paths.LNDTLSCertPath + "\n" +
		"lnd.macaroonpath=" + paths.LNDMacaroonPath + "\n" +
		"elementsd.rpcuser=rpc-user\n" +
		"elementsd.rpcpass=peerswap-secret-never-audit\n" +
		"elementsd.rpchost=https://elements.example\n" +
		"elementsd.rpcport=7041\n" +
		"elementsd.rpcwallet=peerswap_node\n" +
		"elementsd.datadir=/media/liquid/elements\n" +
		"elementsd.liquidswaps=true\n" +
		"bitcoinswaps=false\n"
	web, err := json.Marshal(map[string]any{
		"DataDir": paths.RuntimeDir, "LightningDir": paths.LNDDir, "Chain": "mainnet",
		"ElementsUser": "rpc-user", "ElementsPass": "peerswap-secret-never-audit", "ElementsWallet": "peerswap_node",
		"ElementsDir": "/media/liquid/elements", "ElementsDirMapped": "/media/liquid/elements",
		"ElementsHost": "https://elements.example", "ElementsPort": "7041", "BitcoinSwaps": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return PeerSwapEnsureParams{
		ElementsMode: appmanifest.PeerSwapElementsModeRemote, Config: config, WebConfig: string(web),
		LNDTLSCertificate: []byte("peerswap-certificate-never-audit"), LNDMacaroon: []byte("peerswap-macaroon-never-audit"),
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
