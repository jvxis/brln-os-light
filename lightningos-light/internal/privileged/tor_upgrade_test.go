package privileged

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

type recordingTorUpgradeManager struct {
	operation string
	params    TorUpgradeStartParams
	dryRun    bool
	state     TorUpgradeState
	err       error
}

func (manager *recordingTorUpgradeManager) Refresh(_ context.Context, dryRun bool) (TorUpgradeState, error) {
	manager.operation, manager.dryRun = "refresh", dryRun
	return manager.state, manager.err
}

func (manager *recordingTorUpgradeManager) Start(_ context.Context, params TorUpgradeStartParams, dryRun bool) (TorUpgradeState, error) {
	manager.operation, manager.params, manager.dryRun = "start", params, dryRun
	return manager.state, manager.err
}

func TestTorMetadataRefreshUsesOnlyFixedAPTCommand(t *testing.T) {
	runner := &recordingRunner{}
	manager := NewNativeTorUpgradeManager(runner)
	state, err := manager.Refresh(context.Background(), true)
	if err != nil || state.Status != "validated" || len(runner.commands) != 0 {
		t.Fatalf("unexpected dry-run: state=%#v err=%v commands=%#v", state, err, runner.commands)
	}
	state, err = manager.Refresh(context.Background(), false)
	want := []recordedCommand{{path: torAptGetPath, args: []string{"-o", "DPkg::Lock::Timeout=60", "update"}}}
	if err != nil || state.Status != "refreshed" || !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected refresh: state=%#v err=%v commands=%#v", state, err, runner.commands)
	}
}

func TestTorUpgradeRequestsAreClosedAndBrokered(t *testing.T) {
	for _, test := range []struct {
		operation Operation
		params    any
		state     TorUpgradeState
	}{
		{OperationTorMetadataRefresh, struct{}{}, TorUpgradeState{Status: "validated"}},
		{OperationTorUpgradeStart, TorUpgradeStartParams{HelperContent: "trusted", VerifyOnly: true}, TorUpgradeState{Status: "validated", Unit: torVerifyUnit, VerifyOnly: true}},
	} {
		raw, err := MarshalParams(test.params)
		if err != nil {
			t.Fatal(err)
		}
		request := Request{Version: ProtocolVersion, RequestID: "tor_upgrade_test", Operation: test.operation, DryRun: true, Params: raw}
		if err := ValidateRequest(request); err != nil {
			t.Fatalf("valid Tor request rejected: %v", err)
		}
		manager := &recordingTorUpgradeManager{state: test.state}
		locker := &recordingLocker{}
		broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker, TorUpgrade: manager, Caller: "test"}
		response := broker.Handle(context.Background(), request)
		if !response.OK || !manager.dryRun || locker.locks != 0 {
			t.Fatalf("unexpected dry-run dispatch: response=%#v manager=%#v locker=%#v", response, manager, locker)
		}
		request.DryRun = false
		response = broker.Handle(context.Background(), request)
		if !response.OK || manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
			t.Fatalf("Tor mutation was not serialized: response=%#v manager=%#v locker=%#v", response, manager, locker)
		}
	}
}

func TestTorUpgradeProtocolRejectsInjectedInputs(t *testing.T) {
	request := Request{Version: ProtocolVersion, RequestID: "tor_bad", Operation: OperationTorMetadataRefresh, Params: json.RawMessage(`{"packages":["ssh"]}`)}
	if err := ValidateRequest(request); err == nil {
		t.Fatal("caller-selected Tor package accepted")
	}
	request.Operation = OperationTorUpgradeStart
	request.Params = json.RawMessage(`{"helper_content":"trusted","url":"https://evil.invalid/script"}`)
	if err := ValidateRequest(request); err == nil {
		t.Fatal("caller-selected Tor helper URL accepted")
	}
	request.Params, _ = MarshalParams(TorUpgradeStartParams{HelperContent: string(make([]byte, 49*1024))})
	if err := ValidateRequest(request); err == nil {
		t.Fatal("oversized Tor helper accepted")
	}
}

func TestClientTorUpgradeUsesTypedRequestsAndStrictResponses(t *testing.T) {
	transport := &fakeTransport{result: TorUpgradeState{Status: "validated"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if status, err := client.RefreshTorMetadata(context.Background(), true); err != nil || status != "validated" || transport.request.Operation != OperationTorMetadataRefresh || !transport.request.DryRun {
		t.Fatalf("unexpected Tor refresh result/request: %q %v %#v", status, err, transport.request)
	}
	transport.result = TorUpgradeState{Status: "validated", Unit: torVerifyUnit, VerifyOnly: true}
	status, unit, err := client.StartTorUpgrade(context.Background(), "trusted", true, true)
	if err != nil || status != "validated" || unit != torVerifyUnit || transport.request.Operation != OperationTorUpgradeStart {
		t.Fatalf("unexpected Tor start result/request: %q %q %v %#v", status, unit, err, transport.request)
	}
	if _, _, err := client.StartTorUpgrade(context.Background(), "trusted", true, false); err == nil {
		t.Fatal("client accepted a dry-run Tor status for a real request")
	}
	transport.result = map[string]any{"status": "started", "unit": "ssh", "verify_only": true}
	if _, _, err := client.StartTorUpgrade(context.Background(), "trusted", true, false); err == nil {
		t.Fatal("client accepted an unexpected Tor unit")
	}
}
