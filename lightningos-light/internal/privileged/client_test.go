package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type fakeTransport struct {
	request Request
	result  any
	error   *ResponseError
	err     error
}

func (transport *fakeTransport) Do(ctx context.Context, request Request) (Response, error) {
	transport.request = request
	if transport.err != nil {
		return Response{}, transport.err
	}
	if transport.error != nil {
		return Response{Version: ProtocolVersion, RequestID: request.RequestID, OK: false, Error: transport.error}, nil
	}
	result, err := json.Marshal(transport.result)
	if err != nil {
		return Response{}, err
	}
	return Response{Version: ProtocolVersion, RequestID: request.RequestID, OK: true, Result: result}, nil
}

func TestClientRestartServiceBuildsTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: map[string]bool{"validated": true}}
	client := NewClientWithTransport(ModeShadow, time.Second, transport, nil)
	if err := client.RestartService(context.Background(), "lnd", true, true); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationServiceRestart || !transport.request.DryRun {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	var params ServiceRestartParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Unit != "lnd" || !params.NoBlock {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestClientRejectsDisabledAndBrokerErrors(t *testing.T) {
	client := NewClientWithTransport(ModeDisabled, time.Second, &fakeTransport{}, nil)
	if err := client.RestartService(context.Background(), "lnd", false, false); err == nil {
		t.Fatal("expected disabled client to fail")
	}

	transport := &fakeTransport{error: &ResponseError{Code: "execution_failed", Message: "service restart failed"}}
	client = NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	err := client.RestartService(context.Background(), "lnd", false, false)
	var clientErr *ClientError
	if !errors.As(err, &clientErr) || clientErr.Code != "execution_failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientEnableLoginBuildsEmptyTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: map[string]bool{"validated": true}}
	client := NewClientWithTransport(ModeShadow, time.Second, transport, nil)
	if err := client.EnableLogin(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationFilesEnableLogin || !transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
}

func TestClientAppLifecycleBuildsTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: map[string]bool{"validated": true}}
	client := NewClientWithTransport(ModeShadow, time.Second, transport, nil)
	if err := client.AppLifecycle(context.Background(), "cpuminer", "stop", true); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationAppLifecycle || !transport.request.DryRun {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	var params AppLifecycleParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != "cpuminer" || params.Action != AppLifecycleStop {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestClientInspectAppBuildsTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: AppInspection{Status: "running", CPUPercentRaw: 123.4}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, cpu, err := client.InspectApp(context.Background(), "cpuminer")
	if err != nil {
		t.Fatal(err)
	}
	if status != "running" || cpu != 123.4 || transport.request.Operation != OperationAppInspect || transport.request.DryRun {
		t.Fatalf("status=%q cpu=%v request=%#v", status, cpu, transport.request)
	}
	var params AppInspectParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != "cpuminer" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestClientInspectAppRejectsInvalidResult(t *testing.T) {
	tests := []AppInspection{
		{Status: "unknown"},
		{Status: "running", CPUPercentRaw: -1},
	}
	for _, result := range tests {
		transport := &fakeTransport{result: result}
		client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
		if _, _, err := client.InspectApp(context.Background(), "cpuminer"); err == nil {
			t.Fatalf("expected invalid result to fail: %#v", result)
		}
	}
}
