package privileged

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type recordingLNDPermissionsManager struct {
	calls  int
	dryRun bool
	state  LNDPermissionsState
}

type recordingLNDManagerCredentialManager struct {
	ensureCalls   int
	rollbackCalls int
	dryRun        bool
	state         LNDManagerCredentialState
}

func (manager *recordingLNDManagerCredentialManager) Ensure(_ context.Context, dryRun bool) (LNDManagerCredentialState, error) {
	manager.ensureCalls++
	manager.dryRun = dryRun
	return manager.state, nil
}

func (manager *recordingLNDManagerCredentialManager) Rollback(_ context.Context, dryRun bool) (LNDManagerCredentialState, error) {
	manager.rollbackCalls++
	manager.dryRun = dryRun
	return manager.state, nil
}

func (manager *recordingLNDPermissionsManager) Repair(_ context.Context, dryRun bool) (LNDPermissionsState, error) {
	manager.calls++
	manager.dryRun = dryRun
	return manager.state, nil
}

func TestLNDPermissionsProtocolIsClosedAndBrokerSerializesRepair(t *testing.T) {
	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, RequestID: "lnd_permissions_test", Operation: OperationLNDPermissionsRepair, Params: params}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("closed LND permissions request rejected: %v", err)
	}
	injected := request
	injected.Params = json.RawMessage(`{"path":"/etc","uid":0}`)
	if err := ValidateRequest(injected); err == nil {
		t.Fatal("LND permissions request accepted caller-selected metadata")
	}

	manager := &recordingLNDPermissionsManager{state: LNDPermissionsState{Status: "ready", Changed: true}}
	locker := &recordingLocker{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker, LNDPermissions: manager, Caller: "test"}
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.calls != 1 || manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("LND permissions repair was not serialized: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}
	request.DryRun = true
	manager.state = LNDPermissionsState{Status: "validated", Changed: true}
	response = broker.Handle(context.Background(), request)
	if !response.OK || manager.calls != 2 || !manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("LND permissions dry-run mutated or locked: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}
}

func TestClientLNDPermissionsRequiresExactState(t *testing.T) {
	transport := &fakeTransport{result: LNDPermissionsState{Status: "ready", Changed: true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, changed, err := client.RepairLNDPermissions(context.Background(), false)
	if err != nil || status != "ready" || !changed || transport.request.Operation != OperationLNDPermissionsRepair || transport.request.DryRun {
		t.Fatalf("unexpected LND permissions result/request: %q/%v/%v/%#v", status, changed, err, transport.request)
	}
	transport.result = LNDPermissionsState{Status: "validated"}
	if _, _, err := client.RepairLNDPermissions(context.Background(), false); err == nil {
		t.Fatal("client accepted dry-run LND permissions state for a real repair")
	}
	transport.result = LNDPermissionsState{Status: "absent"}
	if status, changed, err := client.RepairLNDPermissions(context.Background(), true); err != nil || status != "absent" || changed {
		t.Fatalf("client rejected safe absent state: %q/%v/%v", status, changed, err)
	}
}

func TestLNDManagerCredentialProtocolIsClosedAndSerialized(t *testing.T) {
	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, RequestID: "lnd_manager_credential", Operation: OperationLNDManagerCredentialEnsure, Params: params}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("closed credential request rejected: %v", err)
	}
	injected := request
	injected.Params = json.RawMessage(`{"path":"/tmp/admin.macaroon","permissions":["*:*"],"root_key_id":1}`)
	if err := ValidateRequest(injected); err == nil {
		t.Fatal("credential request accepted caller-selected authority")
	}

	manager := &recordingLNDManagerCredentialManager{state: LNDManagerCredentialState{
		Status: "ready", ConfiguredPath: DefaultLNDManagerMacaroonPath, AdminProtected: true,
	}}
	locker := &recordingLocker{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker, LNDManagerCredential: manager, Caller: "test"}
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.ensureCalls != 1 || manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("ensure was not serialized: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}

	request.Operation = OperationLNDManagerCredentialRollback
	request.DryRun = true
	manager.state = LNDManagerCredentialState{Status: "validated", Changed: true, ConfiguredPath: DefaultLNDManagerMacaroonPath}
	response = broker.Handle(context.Background(), request)
	if !response.OK || manager.rollbackCalls != 1 || !manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("rollback dry-run mutated or locked: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}
}

func TestClientLNDManagerCredentialRequiresExactState(t *testing.T) {
	transport := &fakeTransport{result: LNDManagerCredentialState{
		Status: "ready", Changed: true, ConfiguredPath: DefaultLNDManagerMacaroonPath, AdminProtected: true,
	}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	state, err := client.EnsureLNDManagerCredential(context.Background(), false)
	if err != nil || state.Status != "ready" || transport.request.Operation != OperationLNDManagerCredentialEnsure || transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("unexpected ensure state/error/request: %#v/%v/%#v", state, err, transport.request)
	}
	transport.result = LNDManagerCredentialState{Status: "ready", ConfiguredPath: DefaultLNDAdminMacaroonPath}
	if _, err := client.EnsureLNDManagerCredential(context.Background(), false); err == nil {
		t.Fatal("client accepted ready state without protected dedicated credential")
	}
	transport.result = LNDManagerCredentialState{Status: "rolled_back", Changed: true, ConfiguredPath: DefaultLNDAdminMacaroonPath}
	state, err = client.RollbackLNDManagerCredential(context.Background(), false)
	if err != nil || state.Status != "rolled_back" || transport.request.Operation != OperationLNDManagerCredentialRollback {
		t.Fatalf("unexpected rollback state/error/request: %#v/%v/%#v", state, err, transport.request)
	}
}
