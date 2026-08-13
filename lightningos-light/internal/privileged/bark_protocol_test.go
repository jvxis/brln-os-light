package privileged

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBarkWalletProtocolIsClosed(t *testing.T) {
	tests := []struct {
		name      string
		operation Operation
		params    any
		dryRun    bool
		wantErr   bool
	}{
		{"status", OperationBarkWalletStatus, struct{}{}, false, false},
		{"ensure dry run", OperationBarkWalletEnsure, struct{}{}, true, false},
		{"start", OperationBarkWalletLifecycle, BarkWalletLifecycleParams{Action: AppLifecycleStart}, false, false},
		{"restart rejected", OperationBarkWalletLifecycle, BarkWalletLifecycleParams{Action: AppLifecycleRestart}, false, true},
		{"password read", OperationBarkWalletPasswordRead, struct{}{}, false, false},
		{"password read dry run", OperationBarkWalletPasswordRead, struct{}{}, true, true},
		{"password reset dry run", OperationBarkWalletPasswordReset, struct{}{}, true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := MarshalParams(test.params)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateRequest(Request{Version: ProtocolVersion, RequestID: "bark_test", Operation: test.operation, DryRun: test.dryRun, Params: raw})
			if (err != nil) != test.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, test.wantErr)
			}
		})
	}
	for _, payload := range []string{
		`{"version":1,"request_id":"bark_test","operation":"app.bark.ensure","params":{"path":"/etc/shadow"}}`,
		`{"version":1,"request_id":"bark_test","operation":"app.bark.lifecycle","params":{"action":"start","args":["--privileged"]}}`,
		`{"version":1,"request_id":"bark_test","operation":"app.bark.password.reset","params":{"password":"attacker"}}`,
	} {
		if _, err := DecodeRequest(strings.NewReader(payload)); err == nil {
			t.Fatalf("injected Bark request accepted: %s", payload)
		}
	}
}

type recordingBarkWalletManager struct {
	operation string
	action    AppLifecycleAction
	dryRun    bool
	state     BarkWalletState
	password  string
	err       error
}

func (manager *recordingBarkWalletManager) Status(context.Context) (BarkWalletState, error) {
	manager.operation = "status"
	return manager.state, manager.err
}
func (manager *recordingBarkWalletManager) Ensure(_ context.Context, dryRun bool) (BarkWalletState, error) {
	manager.operation, manager.dryRun = "ensure", dryRun
	return manager.state, manager.err
}
func (manager *recordingBarkWalletManager) Lifecycle(_ context.Context, action AppLifecycleAction, dryRun bool) (BarkWalletState, error) {
	manager.operation, manager.action, manager.dryRun = "lifecycle", action, dryRun
	return manager.state, manager.err
}
func (manager *recordingBarkWalletManager) Remove(_ context.Context, dryRun bool) error {
	manager.operation, manager.dryRun = "remove", dryRun
	return manager.err
}
func (manager *recordingBarkWalletManager) EnsureFirewall(_ context.Context, dryRun bool) (BarkWalletState, error) {
	manager.operation, manager.dryRun = "firewall", dryRun
	return manager.state, manager.err
}
func (manager *recordingBarkWalletManager) ReadPassword() (string, error) {
	manager.operation = "password-read"
	return manager.password, manager.err
}
func (manager *recordingBarkWalletManager) ResetPassword(dryRun bool) (BarkWalletState, error) {
	manager.operation, manager.dryRun = "password-reset", dryRun
	return manager.state, manager.err
}

func TestBrokerDispatchesBarkWalletOperationsWithoutAuditingPassword(t *testing.T) {
	const password = "Bark_Password_Never_Audit_123"
	tests := []struct {
		name          string
		operation     Operation
		params        any
		dryRun        bool
		wantOperation string
		wantLocks     int
	}{
		{"status", OperationBarkWalletStatus, struct{}{}, false, "status", 0},
		{"ensure", OperationBarkWalletEnsure, struct{}{}, false, "ensure", 1},
		{"lifecycle", OperationBarkWalletLifecycle, BarkWalletLifecycleParams{Action: AppLifecycleStart}, false, "lifecycle", 1},
		{"remove dry run", OperationBarkWalletRemove, struct{}{}, true, "remove", 0},
		{"firewall", OperationBarkWalletFirewall, struct{}{}, false, "firewall", 1},
		{"password read", OperationBarkWalletPasswordRead, struct{}{}, false, "password-read", 0},
		{"password reset", OperationBarkWalletPasswordReset, struct{}{}, false, "password-reset", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := &recordingBarkWalletManager{
				state:    BarkWalletState{Installed: true, Status: "stopped", PasswordAvailable: true},
				password: password,
			}
			audit := &recordingAudit{}
			locker := &recordingLocker{}
			broker := &Broker{Runner: &recordingRunner{}, Audit: audit, Locker: locker, BarkWallet: manager, Caller: "test"}
			params, err := MarshalParams(test.params)
			if err != nil {
				t.Fatal(err)
			}
			response := broker.Handle(context.Background(), Request{
				Version: ProtocolVersion, RequestID: "bark_broker", Operation: test.operation,
				DryRun: test.dryRun, Params: params,
			})
			if !response.OK || manager.operation != test.wantOperation || locker.locks != test.wantLocks {
				t.Fatalf("response=%#v operation=%q locks=%d", response, manager.operation, locker.locks)
			}
			encodedAudit, err := json.Marshal(audit.events)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encodedAudit), password) {
				t.Fatal("Bark password leaked into privileged audit")
			}
		})
	}
}
