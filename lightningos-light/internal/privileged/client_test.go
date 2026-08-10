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

func TestClientRemoveAppBuildsTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: map[string]bool{"validated": true}}
	client := NewClientWithTransport(ModeShadow, time.Second, transport, nil)
	if err := client.RemoveApp(context.Background(), "cpuminer", true); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationAppRemove || !transport.request.DryRun {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	var params AppRemoveParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != "cpuminer" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestClientDockerRuntimeBuildsEmptyTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: DockerRuntimeState{Status: "validated"}}
	client := NewClientWithTransport(ModeShadow, time.Second, transport, nil)
	status, err := client.EnsureDockerRuntime(context.Background(), true)
	if err != nil || status != "validated" {
		t.Fatalf("status/error=%q/%v", status, err)
	}
	if transport.request.Operation != OperationDockerEnsure || !transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	transport.result = DockerRuntimeState{Status: "ready"}
	status, err = client.DockerRuntimeStatus(context.Background())
	if err != nil || status != "ready" || transport.request.Operation != OperationDockerStatus || transport.request.DryRun {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
}

func TestClientPackageFeatureBuildsClosedTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: PackageFeatureState{Status: "validated"}}
	client := NewClientWithTransport(ModeShadow, time.Second, transport, nil)
	status, err := client.EnsurePackageFeature(context.Background(), "docker_runtime", true)
	if err != nil || status != "validated" || transport.request.Operation != OperationPackageEnsure || !transport.request.DryRun {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
	var params PackageFeatureParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Feature != PackageFeatureDockerRuntime {
		t.Fatalf("unexpected params: %#v", params)
	}
	transport.result = PackageFeatureState{Status: "indexed"}
	status, err = client.PackageFeatureStatus(context.Background(), "docker_runtime")
	if err != nil || status != "indexed" || transport.request.Operation != OperationPackageStatus || transport.request.DryRun {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
}

func TestClientAppImageOperationsBuildTypedRequests(t *testing.T) {
	transport := &fakeTransport{result: AppImageState{Status: "preparing"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, err := client.PrepareAppImage(context.Background(), "cpuminer", "baseline", false)
	if err != nil || status != "preparing" || transport.request.Operation != OperationAppImagePrepare {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
	var params AppImageParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != "cpuminer" || params.Variant != "baseline" {
		t.Fatalf("unexpected params: %#v", params)
	}

	transport.result = AppImageState{Status: "ready"}
	status, err = client.AppImageStatus(context.Background(), "cpuminer", "baseline")
	if err != nil || status != "ready" || transport.request.Operation != OperationAppImageStatus || transport.request.DryRun {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}

	transport.result = AppImageProbe{Runnable: true}
	runnable, err := client.ProbeAppImage(context.Background(), "cpuminer", "fast_pinned", false)
	if err != nil || !runnable || transport.request.Operation != OperationAppImageProbe {
		t.Fatalf("runnable/error/request=%v/%v/%#v", runnable, err, transport.request)
	}
}

func TestClientAppImageRejectsInvalidState(t *testing.T) {
	transport := &fakeTransport{result: AppImageState{Status: "root-shell"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if _, err := client.AppImageStatus(context.Background(), "cpuminer", "baseline"); err == nil {
		t.Fatal("expected invalid image state to fail")
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
