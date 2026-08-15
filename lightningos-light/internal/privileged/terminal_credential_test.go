package privileged

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type terminalCredentialRunner struct {
	commands []recordedCommand
	user     string
	err      error
}

func TestWaitForTerminalTCPReady(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForTerminalTCPReady(ctx, address); err != nil {
		_ = listener.Close()
		t.Fatalf("ready listener was rejected: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer timeoutCancel()
	if err := waitForTerminalTCPReady(timeoutCtx, address); err == nil {
		t.Fatal("closed listener was reported ready")
	}
}

func TestTerminalControlManagerUsesExistingCredentialAndTypedState(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	runtimePath := filepath.Join(t.TempDir(), "terminal.env")
	if err := os.WriteFile(runtimePath, []byte(terminalRuntimeEnvContent("0", "losop", password)), 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	manager := &NativeTerminalCredentialManager{
		Runner:         &terminalCredentialRunner{user: "losop"},
		RuntimeEnvPath: runtimePath,
		ApplyControl: func(_ context.Context, path string, operatorUser string, appliedPassword string, enabled bool) error {
			called = true
			if path != runtimePath || operatorUser != "losop" || appliedPassword != password || !enabled {
				t.Fatalf("unexpected control input: path=%q user=%q password_len=%d enabled=%v", path, operatorUser, len(appliedPassword), enabled)
			}
			return nil
		},
	}
	state, err := manager.SetEnabled(context.Background(), TerminalControlParams{Action: TerminalControlEnable}, false)
	if err != nil || !called || state.Status != "applied" || !state.Enabled {
		t.Fatalf("state/called/error=%+v/%v/%v", state, called, err)
	}
	called = false
	state, err = manager.SetEnabled(context.Background(), TerminalControlParams{Action: TerminalControlDisable}, true)
	if err != nil || called || state.Status != "validated" || state.Enabled {
		t.Fatalf("dry-run state/called/error=%+v/%v/%v", state, called, err)
	}
}

func TestTerminalControlManagerFailsClosedBeforeMutation(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	for _, test := range []struct {
		name    string
		user    string
		content string
		action  TerminalControlAction
	}{
		{name: "root service", user: "root", content: terminalRuntimeEnvContent("0", "root", password), action: TerminalControlEnable},
		{name: "credential mismatch", user: "other", content: terminalRuntimeEnvContent("0", "losop", password), action: TerminalControlEnable},
		{name: "invalid credential", user: "losop", content: "TERMINAL_CREDENTIAL=losop:short\n", action: TerminalControlEnable},
		{name: "invalid action", user: "losop", content: terminalRuntimeEnvContent("0", "losop", password), action: "restart"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtimePath := filepath.Join(t.TempDir(), "terminal.env")
			if err := os.WriteFile(runtimePath, []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			called := false
			manager := &NativeTerminalCredentialManager{
				Runner:         &terminalCredentialRunner{user: test.user},
				RuntimeEnvPath: runtimePath,
				ApplyControl: func(context.Context, string, string, string, bool) error {
					called = true
					return nil
				},
			}
			if _, err := manager.SetEnabled(context.Background(), TerminalControlParams{Action: test.action}, false); err == nil || called {
				t.Fatalf("unsafe control was not rejected: err=%v called=%v", err, called)
			}
		})
	}
}

func (runner *terminalCredentialRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	runner.commands = append(runner.commands, recordedCommand{path: path, args: append([]string(nil), args...)})
	return runner.user + "\n", runner.err
}

func TestTerminalCredentialManagerWritesOnlyDedicatedRuntimeCredential(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	runner := &terminalCredentialRunner{user: "losop"}
	var appliedPath, appliedUser, appliedPassword string
	manager := &NativeTerminalCredentialManager{
		Runner:         runner,
		RuntimeEnvPath: "/fixed/terminal.env",
		ApplyCredential: func(path string, operatorUser string, password string) error {
			appliedPath, appliedUser, appliedPassword = path, operatorUser, password
			return nil
		},
	}
	state, err := manager.Rotate(context.Background(), TerminalCredentialRotateParams{OperatorUser: "losop", Password: password}, false)
	if err != nil || state.Status != "applied" || state.OperatorUser != "losop" {
		t.Fatalf("state/error=%+v/%v", state, err)
	}
	if appliedPath != "/fixed/terminal.env" || appliedUser != "losop" || appliedPassword != password {
		t.Fatalf("unexpected fixed apply input: path=%q user=%q password_len=%d", appliedPath, appliedUser, len(appliedPassword))
	}
	for _, command := range runner.commands {
		if strings.Contains(command.path, password) || strings.Contains(strings.Join(command.args, " "), password) {
			t.Fatal("terminal password entered a command path or argument")
		}
	}
}

func TestTerminalCredentialManagerFailsClosedBeforeRuntimeMutation(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	for _, test := range []struct {
		name   string
		user   string
		params TerminalCredentialRotateParams
	}{
		{name: "service mismatch", user: "other", params: TerminalCredentialRotateParams{OperatorUser: "losop", Password: password}},
		{name: "root service forbidden", user: "root", params: TerminalCredentialRotateParams{OperatorUser: "root", Password: password}},
		{name: "invalid requested user", user: "losop", params: TerminalCredentialRotateParams{OperatorUser: "root;id", Password: password}},
		{name: "invalid password", user: "losop", params: TerminalCredentialRotateParams{OperatorUser: "losop", Password: "short"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			applied := false
			manager := &NativeTerminalCredentialManager{
				Runner:          &terminalCredentialRunner{user: test.user},
				ApplyCredential: func(string, string, string) error { applied = true; return nil },
			}
			if _, err := manager.Rotate(context.Background(), test.params, false); err == nil || applied {
				t.Fatalf("unsafe request was not rejected before mutation: err=%v applied=%v", err, applied)
			}
		})
	}
}

func TestTerminalRuntimeEnvironmentIsSecretMinimalAndReadOnly(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	content := terminalRuntimeEnvContent("1", "losop", password)
	for _, required := range []string{
		"TERMINAL_ENABLED=1\n",
		"TERMINAL_CREDENTIAL=losop:" + password + "\n",
		"TERMINAL_ALLOW_WRITE=0\n",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("terminal runtime environment is missing %q", required)
		}
	}
	for _, forbidden := range []string{"NOTIFICATIONS_TG_BOT_TOKEN", "BITCOIN_RPC_PASS", "TERMINAL_OPERATOR_PASSWORD"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("terminal runtime environment contains forbidden key %q", forbidden)
		}
	}
	if strings.Contains(terminalRuntimeEnvContent("unexpected", "losop", password), "TERMINAL_ENABLED=1") {
		t.Fatal("unexpected enabled value did not fail closed")
	}
}

func TestTerminalCredentialManagerReportsDedicatedRuntimeWriteFailure(t *testing.T) {
	manager := &NativeTerminalCredentialManager{
		Runner:          &terminalCredentialRunner{user: "losop"},
		ApplyCredential: func(string, string, string) error { return errors.New("write failed") },
	}
	_, err := manager.Rotate(context.Background(), TerminalCredentialRotateParams{OperatorUser: "losop", Password: "AbCdEfGhIjKlMnOpQrStUvWxYz012345"}, false)
	if err == nil || strings.Contains(err.Error(), "write failed") {
		t.Fatalf("runtime write failure was not safely wrapped: %v", err)
	}
}

type recordingTerminalCredentialManager struct {
	params TerminalCredentialRotateParams
	dryRun bool
	state  TerminalCredentialState
	err    error
}

type recordingTerminalControlManager struct {
	params TerminalControlParams
	dryRun bool
	state  TerminalControlState
}

func (manager *recordingTerminalControlManager) SetEnabled(_ context.Context, params TerminalControlParams, dryRun bool) (TerminalControlState, error) {
	manager.params, manager.dryRun = params, dryRun
	return manager.state, nil
}

func (manager *recordingTerminalCredentialManager) Rotate(_ context.Context, params TerminalCredentialRotateParams, dryRun bool) (TerminalCredentialState, error) {
	manager.params, manager.dryRun = params, dryRun
	return manager.state, manager.err
}

func TestTerminalControlProtocolAndBrokerAreTypedAndLocked(t *testing.T) {
	request := requestWithParams(t, OperationTerminalControl, TerminalControlParams{Action: TerminalControlEnable}, false)
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("valid terminal control request rejected: %v", err)
	}
	manager := &recordingTerminalControlManager{state: TerminalControlState{Status: "applied", Enabled: true}}
	locker := &recordingLocker{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker, TerminalControl: manager, Caller: "test"}
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.params.Action != TerminalControlEnable || manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("unexpected broker dispatch: response=%+v manager=%+v locker=%+v", response, manager, locker)
	}
	for _, raw := range []string{
		`{"action":"restart"}`,
		`{"action":"enable","command":"/bin/sh"}`,
		`{"action":"enable","path":"/etc/shadow"}`,
	} {
		invalid := request
		invalid.Params = []byte(raw)
		if err := ValidateRequest(invalid); err == nil {
			t.Fatalf("unsafe terminal control accepted: %s", raw)
		}
	}
}

func TestTerminalCredentialProtocolAndBrokerAreTypedLockedAndSecretFreeInAudit(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	request := requestWithParams(t, OperationTerminalCredentialRotate, TerminalCredentialRotateParams{OperatorUser: "losop", Password: password}, false)
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("valid terminal credential request rejected: %v", err)
	}
	manager := &recordingTerminalCredentialManager{state: TerminalCredentialState{Status: "applied", OperatorUser: "losop"}}
	audit := &recordingAudit{}
	locker := &recordingLocker{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: audit, Locker: locker, TerminalCredential: manager, Caller: "test"}
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.params.Password != password || manager.params.OperatorUser != "losop" || manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("unexpected broker dispatch: response=%+v manager=%+v locker=%+v", response, manager, locker)
	}
	for _, event := range audit.events {
		if strings.Contains(string(event.Operation), password) || strings.Contains(event.RequestID, password) || strings.Contains(event.ErrorCode, password) {
			t.Fatal("terminal password entered the structured audit event")
		}
	}

	for _, raw := range []string{
		`{"operator_user":"root","password":"AbCdEfGhIjKlMnOpQrStUvWxYz012345","command":"/bin/sh"}`,
		`{"operator_user":"losop;id","password":"AbCdEfGhIjKlMnOpQrStUvWxYz012345"}`,
		`{"operator_user":"losop","password":"short"}`,
	} {
		invalid := request
		invalid.Params = []byte(raw)
		if err := ValidateRequest(invalid); err == nil {
			t.Fatalf("unsafe terminal credential request accepted: %s", raw)
		}
	}
}
