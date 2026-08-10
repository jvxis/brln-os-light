package privileged

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
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

type recordingApps struct {
	calls  int
	appID  string
	action AppLifecycleAction
	dryRun bool
	err    error
}

func (apps *recordingApps) Lifecycle(_ context.Context, appID string, action AppLifecycleAction, dryRun bool) error {
	apps.calls++
	apps.appID = appID
	apps.action = action
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
