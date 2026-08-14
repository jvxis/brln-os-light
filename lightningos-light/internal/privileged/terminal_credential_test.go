package privileged

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type terminalCredentialRunner struct {
	commands []recordedCommand
	user     string
	err      error
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

func (manager *recordingTerminalCredentialManager) Rotate(_ context.Context, params TerminalCredentialRotateParams, dryRun bool) (TerminalCredentialState, error) {
	manager.params, manager.dryRun = params, dryRun
	return manager.state, manager.err
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
