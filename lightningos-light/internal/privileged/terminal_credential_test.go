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

func TestTerminalCredentialManagerKeepsSecretOutOfArguments(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	runner := &terminalCredentialRunner{user: "losop"}
	var appliedPath, appliedCredential string
	manager := &NativeTerminalCredentialManager{
		Runner:       runner,
		ChpasswdPath: "/fixed/chpasswd",
		ValidateExecutable: func(path string) error {
			if path != "/fixed/chpasswd" {
				t.Fatalf("unexpected executable: %q", path)
			}
			return nil
		},
		ApplyCredential: func(_ context.Context, path string, credential string) error {
			appliedPath, appliedCredential = path, credential
			return nil
		},
	}
	state, err := manager.Rotate(context.Background(), TerminalCredentialRotateParams{OperatorUser: "losop", Password: password}, false)
	if err != nil || state.Status != "applied" || state.OperatorUser != "losop" {
		t.Fatalf("state/error=%+v/%v", state, err)
	}
	if appliedPath != "/fixed/chpasswd" || appliedCredential != "losop:"+password+"\n" {
		t.Fatalf("unexpected fixed apply input: path=%q credential_len=%d", appliedPath, len(appliedCredential))
	}
	for _, command := range runner.commands {
		if strings.Contains(command.path, password) || strings.Contains(strings.Join(command.args, " "), password) {
			t.Fatal("terminal password entered a command path or argument")
		}
	}
}

func TestTerminalCredentialManagerFailsClosedBeforePasswordMutation(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	for _, test := range []struct {
		name     string
		user     string
		params   TerminalCredentialRotateParams
		validate func(string) error
	}{
		{name: "service mismatch", user: "other", params: TerminalCredentialRotateParams{OperatorUser: "losop", Password: password}},
		{name: "root service forbidden", user: "root", params: TerminalCredentialRotateParams{OperatorUser: "root", Password: password}},
		{name: "invalid requested user", user: "losop", params: TerminalCredentialRotateParams{OperatorUser: "root;id", Password: password}},
		{name: "invalid password", user: "losop", params: TerminalCredentialRotateParams{OperatorUser: "losop", Password: "short"}},
		{name: "unsafe executable", user: "losop", params: TerminalCredentialRotateParams{OperatorUser: "losop", Password: password}, validate: func(string) error { return errors.New("unsafe") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			applied := false
			validate := test.validate
			if validate == nil {
				validate = func(string) error { return nil }
			}
			manager := &NativeTerminalCredentialManager{
				Runner: &terminalCredentialRunner{user: test.user}, ValidateExecutable: validate,
				ApplyCredential: func(context.Context, string, string) error { applied = true; return nil },
			}
			if _, err := manager.Rotate(context.Background(), test.params, false); err == nil || applied {
				t.Fatalf("unsafe request was not rejected before mutation: err=%v applied=%v", err, applied)
			}
		})
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
