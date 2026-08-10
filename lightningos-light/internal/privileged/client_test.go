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
