package privileged

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type recordingAppStorageManager struct {
	calls  int
	dryRun bool
	state  AppStorageState
	err    error
}

func (manager *recordingAppStorageManager) Ensure(_ context.Context, dryRun bool) (AppStorageState, error) {
	manager.calls++
	manager.dryRun = dryRun
	return manager.state, manager.err
}

func TestAppStorageProtocolIsClosedAndBrokerSerializesRepair(t *testing.T) {
	params, err := MarshalParams(struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, RequestID: "app_storage_test", Operation: OperationAppStorageEnsure, Params: params}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("closed app storage request rejected: %v", err)
	}
	injected := request
	injected.Params = json.RawMessage(`{"path":"/etc"}`)
	if err := ValidateRequest(injected); err == nil {
		t.Fatal("app storage request accepted a caller-selected path")
	}

	manager := &recordingAppStorageManager{state: AppStorageState{Status: "ready", Changed: true}}
	locker := &recordingLocker{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker, AppStorage: manager, Caller: "test"}
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.calls != 1 || manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("app storage mutation was not serialized: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}

	request.DryRun = true
	manager.state = AppStorageState{Status: "validated", Changed: true}
	response = broker.Handle(context.Background(), request)
	if !response.OK || manager.calls != 2 || !manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("app storage dry-run mutated or locked: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}
}

func TestClientAppStorageRequiresExactState(t *testing.T) {
	transport := &fakeTransport{result: AppStorageState{Status: "ready", Changed: true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, changed, err := client.EnsureAppStorage(context.Background(), false)
	if err != nil || status != "ready" || !changed || transport.request.Operation != OperationAppStorageEnsure || transport.request.DryRun {
		t.Fatalf("unexpected app storage response/request: %q/%v/%v/%#v", status, changed, err, transport.request)
	}
	transport.result = AppStorageState{Status: "validated"}
	if _, _, err := client.EnsureAppStorage(context.Background(), false); err == nil {
		t.Fatal("client accepted dry-run state for a real app storage repair")
	}
}
