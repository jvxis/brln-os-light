package privileged

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestHostPowerProtocolRejectsCommandsAndUnknownActions(t *testing.T) {
	params, err := MarshalParams(HostPowerParams{Action: "poweroff"})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Version: ProtocolVersion, RequestID: "power_protocol_1", Operation: OperationHostPower, Params: params,
	}
	if err := ValidateRequest(request); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	for _, raw := range []string{
		`{"action":"shutdown"}`,
		`{"action":"poweroff","command":"/usr/bin/systemctl"}`,
		`{"action":"reboot","args":["--force"]}`,
	} {
		request.Params = json.RawMessage(raw)
		if err := ValidateRequest(request); err == nil {
			t.Fatalf("unsafe params accepted: %s", raw)
		}
	}
}

func TestBrokerHostPowerUsesFixedDelayedCommand(t *testing.T) {
	runner := &recordingRunner{}
	audit := &recordingAudit{}
	locker := &recordingLocker{}
	broker := testBroker(runner, audit, locker)
	params, err := MarshalParams(HostPowerParams{Action: "poweroff"})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Version: ProtocolVersion, RequestID: "poweroff_1", Operation: OperationHostPower, Params: params,
	}

	response := broker.Handle(context.Background(), request)
	if !response.OK {
		t.Fatalf("unexpected response error: %+v", response.Error)
	}
	want := []recordedCommand{{path: systemdRunPath, args: []string{
		"--quiet",
		"--collect",
		"--unit=lightningos-host-poweroff-poweroff_1",
		"--on-active=2s",
		systemctlPath,
		"poweroff",
	}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
	if locker.locks != 1 || locker.unlocks != 1 {
		t.Fatalf("lock counts = %d/%d, want 1/1", locker.locks, locker.unlocks)
	}
	if len(audit.events) != 2 || audit.events[1].Operation != OperationHostPower || !audit.events[1].Success {
		t.Fatalf("unexpected audit events: %#v", audit.events)
	}
}

func TestBrokerHostPowerDryRunDoesNotExecuteOrLock(t *testing.T) {
	runner := &recordingRunner{}
	locker := &recordingLocker{}
	broker := testBroker(runner, &recordingAudit{}, locker)
	params, err := MarshalParams(HostPowerParams{Action: "reboot"})
	if err != nil {
		t.Fatal(err)
	}
	response := broker.Handle(context.Background(), Request{
		Version: ProtocolVersion, RequestID: "reboot_dry_1", Operation: OperationHostPower, DryRun: true, Params: params,
	})
	if !response.OK {
		t.Fatalf("unexpected response error: %+v", response.Error)
	}
	if len(runner.commands) != 0 || locker.locks != 0 {
		t.Fatalf("dry run executed command or lock: commands=%d locks=%d", len(runner.commands), locker.locks)
	}
	var state HostPowerState
	if err := json.Unmarshal(response.Result, &state); err != nil || !state.Validated || state.Scheduled {
		t.Fatalf("unexpected state/error: %#v/%v", state, err)
	}
}

func TestClientPowerHostRequiresStrictScheduledState(t *testing.T) {
	transport := &fakeTransport{result: HostPowerState{Validated: true, Scheduled: true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if err := client.PowerHost(context.Background(), "reboot", false); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationHostPower || transport.request.DryRun {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	var params HostPowerParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil || params.Action != "reboot" {
		t.Fatalf("params/error = %#v/%v", params, err)
	}

	transport.result = HostPowerState{Validated: true}
	if err := client.PowerHost(context.Background(), "reboot", false); err == nil {
		t.Fatal("real host power accepted an unscheduled response")
	}
	transport.result = HostPowerState{Validated: true, Scheduled: true}
	if err := client.PowerHost(context.Background(), "reboot", true); err == nil {
		t.Fatal("dry-run host power accepted a scheduled response")
	}
}
