package privileged

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type recordingLNDUpgradeManager struct {
	params LNDUpgradeStartParams
	dryRun bool
	calls  int
	state  LNDUpgradeState
	err    error
}

func (manager *recordingLNDUpgradeManager) Start(_ context.Context, params LNDUpgradeStartParams, dryRun bool) (LNDUpgradeState, error) {
	manager.calls++
	manager.params = params
	manager.dryRun = dryRun
	return manager.state, manager.err
}

func TestLNDUpgradeRequestIsClosedAndBrokered(t *testing.T) {
	params := LNDUpgradeStartParams{Version: "0.21.1-beta", HelperContent: "trusted helper", VerifyOnly: true}
	raw, err := MarshalParams(params)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Version: ProtocolVersion, RequestID: "lnd_upgrade_test", Operation: OperationLNDUpgradeStart, DryRun: true, Params: raw}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("valid LND upgrade request rejected: %v", err)
	}

	manager := &recordingLNDUpgradeManager{state: LNDUpgradeState{Status: "validated", Unit: lndVerifyUnit, Version: params.Version, VerifyOnly: true}}
	locker := &recordingLocker{}
	broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker, LNDUpgrade: manager, Caller: "test"}
	response := broker.Handle(context.Background(), request)
	if !response.OK || manager.calls != 1 || !manager.dryRun || locker.locks != 0 {
		t.Fatalf("unexpected broker result: response=%#v manager=%#v locks=%d", response, manager, locker.locks)
	}

	request.DryRun = false
	response = broker.Handle(context.Background(), request)
	if !response.OK || manager.calls != 2 || manager.dryRun || locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("mutating LND upgrade was not serialized: response=%#v manager=%#v locker=%#v", response, manager, locker)
	}
}

func TestLNDUpgradeProtocolRejectsInjectedInputs(t *testing.T) {
	tests := []LNDUpgradeStartParams{
		{Version: "0.21.1-beta;reboot", HelperContent: "trusted helper"},
		{Version: "0.21.1-beta", HelperContent: ""},
		{Version: "0.21.1-beta", HelperContent: string(make([]byte, 49*1024))},
	}
	for _, params := range tests {
		raw, err := MarshalParams(params)
		if err != nil {
			t.Fatal(err)
		}
		request := Request{Version: ProtocolVersion, RequestID: "lnd_upgrade_bad", Operation: OperationLNDUpgradeStart, Params: raw}
		if err := ValidateRequest(request); err == nil {
			t.Fatalf("unsafe LND upgrade params accepted: %#v", params)
		}
	}

	raw := json.RawMessage(`{"version":"0.21.1-beta","helper_content":"trusted","url":"https://evil.invalid/lnd.tar.gz"}`)
	request := Request{Version: ProtocolVersion, RequestID: "lnd_upgrade_url", Operation: OperationLNDUpgradeStart, Params: raw}
	if err := ValidateRequest(request); err == nil {
		t.Fatal("caller-controlled LND download URL accepted")
	}
}

func TestClientLNDUpgradeUsesTypedRequestAndStrictResponse(t *testing.T) {
	transport := &fakeTransport{result: LNDUpgradeState{Status: "validated", Unit: lndVerifyUnit, Version: "0.21.1-beta", VerifyOnly: true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, unit, err := client.StartLNDUpgrade(context.Background(), "0.21.1-beta", "trusted helper", true, true)
	if err != nil || status != "validated" || unit != lndVerifyUnit || transport.request.Operation != OperationLNDUpgradeStart || !transport.request.DryRun {
		t.Fatalf("unexpected client result/request: %q %q %v %#v", status, unit, err, transport.request)
	}
	var params LNDUpgradeStartParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil || params.HelperContent != "trusted helper" || !params.VerifyOnly {
		t.Fatalf("typed LND upgrade payload was not preserved: %#v/%v", params, err)
	}

	transport.result = map[string]any{"status": "started", "unit": "ssh", "version": "0.21.1-beta", "verify_only": true}
	if _, _, err := client.StartLNDUpgrade(context.Background(), "0.21.1-beta", "trusted helper", true, false); err == nil {
		t.Fatal("client accepted an unexpected broker unit")
	}
}
