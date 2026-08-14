package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

type fakeTransport struct {
	request  Request
	result   any
	error    *ResponseError
	err      error
	deadline time.Time
}

func (transport *fakeTransport) Do(ctx context.Context, request Request) (Response, error) {
	transport.request = request
	transport.deadline, _ = ctx.Deadline()
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

func TestClientManagerFirewallStatusUsesClosedReadOnlyRequest(t *testing.T) {
	state := ManagerFirewallState{Installed: true, Active: true, ConfiguredCIDR: "none", ConfigValid: true, TailscaleRule: true, ManagerAccessBound: true, StatusAvailable: true}
	transport := &fakeTransport{result: state}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	raw, err := client.ManagerFirewallStatus(context.Background())
	if err != nil || transport.request.Operation != OperationManagerFirewallStatus || transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("raw/error/request=%q/%v/%#v", raw, err, transport.request)
	}
	var got ManagerFirewallState
	if err := json.Unmarshal([]byte(raw), &got); err != nil || !reflect.DeepEqual(got, state) {
		t.Fatalf("decoded/error=%#v/%v", got, err)
	}
}

func TestClientBitcoinCoreConfigRequestsKeepSecretInTypedPayload(t *testing.T) {
	const dataDir = "/mnt/bitcoin-ssd/bitcoin"
	const content = "server=1\nrpcpassword=top-secret\n"
	transport := &fakeTransport{result: BitcoinCoreConfigState{Status: "ready"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)

	status, err := client.EnsureBitcoinCoreConfig(context.Background(), dataDir, content, false, false)
	if err != nil || status != "ready" || transport.request.Operation != OperationBitcoinConfigEnsure {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
	var writeParams BitcoinCoreConfigWriteParams
	if err := json.Unmarshal(transport.request.Params, &writeParams); err != nil {
		t.Fatal(err)
	}
	if writeParams.DataDir != dataDir || writeParams.Content != content || writeParams.GenerateRPCAuth {
		t.Fatalf("unexpected config params: %#v", writeParams)
	}

	transport.result = BitcoinCoreCredentialsState{Status: "ready", User: appmanifest.BitcoinCoreRPCUser, Password: strings.Repeat("a", 64)}
	user, password, err := client.ReadBitcoinCoreCredentials(context.Background(), dataDir)
	if err != nil || user != appmanifest.BitcoinCoreRPCUser || password != strings.Repeat("a", 64) ||
		transport.request.Operation != OperationBitcoinCredentialsRead || transport.request.DryRun {
		t.Fatalf("credentials/error/request=%q/%d/%v/%#v", user, len(password), err, transport.request)
	}

	transport.result = BitcoinCoreElectrsCredentialsState{
		Status: "restart_required", User: appmanifest.ElectrsBitcoinRPCUser, Password: strings.Repeat("b", 64), ConfigChanged: true,
	}
	electrsUser, electrsPassword, electrsStatus, changed, err := client.EnsureBitcoinCoreElectrsCredentials(context.Background(), dataDir, false)
	if err != nil || electrsUser != appmanifest.ElectrsBitcoinRPCUser || electrsPassword != strings.Repeat("b", 64) || electrsStatus != "restart_required" || !changed ||
		transport.request.Operation != OperationBitcoinElectrsCredentialsEnsure || transport.request.DryRun {
		t.Fatalf("Electrs credentials/error/request=%q/%d/%q/%v/%v/%#v", electrsUser, len(electrsPassword), electrsStatus, changed, err, transport.request)
	}

	transport.result = BitcoinCoreConfigState{Status: "ready", Content: content}
	read, err := client.ReadBitcoinCoreConfig(context.Background(), dataDir)
	if err != nil || read != content || transport.request.Operation != OperationBitcoinConfigRead || transport.request.DryRun {
		t.Fatalf("read/error/request=%q/%v/%#v", read, err, transport.request)
	}

	transport.result = BitcoinCoreConfigState{Status: "ready"}
	status, err = client.WriteBitcoinCoreConfig(context.Background(), dataDir, content, false)
	if err != nil || status != "ready" || transport.request.Operation != OperationBitcoinConfigWrite {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
}

func TestClientBitcoinCoreStatusUsesClosedReadOnlyRequest(t *testing.T) {
	state := BitcoinCoreStatusState{
		Chain: "main", Blocks: 100, Headers: 100, BestBlockTime: 1_780_000_000,
		VerificationProgress: 1, BestBlockHash: strings.Repeat("0", 64),
		SizeOnDisk: 123, NetworkOK: true, Version: 310100, Subversion: "/Satoshi:31.1.0/", Connections: 8,
	}
	transport := &fakeTransport{result: state}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	raw, err := client.BitcoinCoreStatus(context.Background())
	if err != nil || transport.request.Operation != OperationBitcoinStatus || transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("raw/error/request=%q/%v/%#v", raw, err, transport.request)
	}
	remaining := time.Until(transport.deadline)
	if remaining < privilegedBitcoinStatusTimeout-time.Second || remaining > privilegedBitcoinStatusTimeout {
		t.Fatalf("bitcoin status deadline has %v remaining, want %v", remaining, privilegedBitcoinStatusTimeout)
	}
	var got BitcoinCoreStatusState
	if err := json.Unmarshal([]byte(raw), &got); err != nil || !reflect.DeepEqual(got, state) {
		t.Fatalf("decoded/error=%#v/%v", got, err)
	}

	state.Chain = "regtest"
	transport.result = state
	if _, err := client.BitcoinCoreStatus(context.Background()); err == nil {
		t.Fatal("invalid broker status response was accepted")
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

func TestClientSystemIntegrationsUseClosedTypedRequests(t *testing.T) {
	transport := &fakeTransport{result: SystemIntegrationAssetState{Status: "ready", Changed: true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	changed, err := client.InstallSystemIntegrationAsset(context.Background(), "terminal", "fixed", false)
	if err != nil || !changed || transport.request.Operation != OperationSystemIntegrationAssetInstall || transport.request.DryRun {
		t.Fatalf("changed/error/request=%v/%v/%#v", changed, err, transport.request)
	}
	var params SystemIntegrationAssetInstallParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil || params.Asset != SystemIntegrationAssetTerminal || params.Content != "fixed" {
		t.Fatalf("unexpected params/error: %#v/%v", params, err)
	}

	transport.result = SystemIntegrationsState{Status: "ready", CertificateChanged: true, LNDPolicyChanged: true}
	certificateChanged, lndPolicyChanged, err := client.ApplySystemIntegrations(context.Background(), false)
	if err != nil || !certificateChanged || !lndPolicyChanged || transport.request.Operation != OperationSystemIntegrationsApply || string(transport.request.Params) != "{}" {
		t.Fatalf("certificate/lnd/error/request=%v/%v/%v/%#v", certificateChanged, lndPolicyChanged, err, transport.request)
	}

	transport.result = SystemIntegrationsState{Status: "ready"}
	ready, err := client.SystemIntegrationsStatus(context.Background())
	if err != nil || !ready || transport.request.Operation != OperationSystemIntegrationsStatus || transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("ready/error/request=%v/%v/%#v", ready, err, transport.request)
	}

	transport.result = SystemIntegrationsState{Status: "validated"}
	if err := client.FinalizeSystemIntegrations(context.Background(), true); err != nil || transport.request.Operation != OperationSystemIntegrationsFinalize || !transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("unexpected finalize error/request: %v/%#v", err, transport.request)
	}
}

func TestClientTerminalCredentialUsesTypedPayload(t *testing.T) {
	const password = "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	transport := &fakeTransport{result: TerminalCredentialState{Status: "applied", OperatorUser: "losop"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	operator, err := client.RotateTerminalCredential(context.Background(), "losop", password, false)
	if err != nil || operator != "losop" || transport.request.Operation != OperationTerminalCredentialRotate || transport.request.DryRun {
		t.Fatalf("operator/error/request=%q/%v/%#v", operator, err, transport.request)
	}
	var params TerminalCredentialRotateParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil || params.OperatorUser != "losop" || params.Password != password {
		t.Fatalf("unexpected params/error: operator=%q password_len=%d err=%v", params.OperatorUser, len(params.Password), err)
	}
}

func TestClientTerminalControlUsesTypedPayload(t *testing.T) {
	transport := &fakeTransport{result: TerminalControlState{Status: "applied", Enabled: true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	enabled, err := client.SetTerminalEnabled(context.Background(), true, false)
	if err != nil || !enabled || transport.request.Operation != OperationTerminalControl || transport.request.DryRun {
		t.Fatalf("enabled/error/request=%v/%v/%#v", enabled, err, transport.request)
	}
	var params TerminalControlParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil || params.Action != TerminalControlEnable {
		t.Fatalf("unexpected params/error: %#v/%v", params, err)
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
	remaining := time.Until(transport.deadline)
	if remaining < 500*time.Millisecond || remaining > time.Second {
		t.Fatalf("dry-run lifecycle client deadline has %v remaining, want configured short timeout", remaining)
	}
}

func TestClientBTCPayLifecycleBuildsTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: map[string]bool{"validated": true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if err := client.AppLifecycle(context.Background(), appmanifest.BTCPayID, "start", false); err != nil {
		t.Fatal(err)
	}
	var params AppLifecycleParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationAppLifecycle || transport.request.DryRun || params.AppID != appmanifest.BTCPayID || params.Action != AppLifecycleStart {
		t.Fatalf("unexpected BTCPay lifecycle request: %#v/%#v", transport.request, params)
	}
	remaining := time.Until(transport.deadline)
	if remaining < privilegedLongOperationTimeout-time.Second || remaining > privilegedLongOperationTimeout {
		t.Fatalf("BTCPay lifecycle client deadline has %v remaining, want %v", remaining, privilegedLongOperationTimeout)
	}
}

func TestClientBTCPaySnapshotBuildsTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: map[string]bool{"validated": true}}
	client := NewClientWithTransport(ModeShadow, time.Second, transport, nil)
	if err := client.SnapshotApp(context.Background(), appmanifest.BTCPayID, true); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationAppSnapshot || !transport.request.DryRun {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	var params AppSnapshotParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != appmanifest.BTCPayID {
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

func TestClientLNDgAdminResetBuildsTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: map[string]bool{"validated": true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if err := client.ResetAppAdmin(context.Background(), appmanifest.LNDgID, false); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationAppAdminReset || transport.request.DryRun {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	var params AppAdminResetParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != appmanifest.LNDgID {
		t.Fatalf("unexpected params: %#v", params)
	}
	remaining := time.Until(transport.deadline)
	if remaining < privilegedLongOperationTimeout-time.Second || remaining > privilegedLongOperationTimeout {
		t.Fatalf("LNDg admin reset client deadline has %v remaining, want %v", remaining, privilegedLongOperationTimeout)
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

func TestClientMDNSPackageFeatureBuildsClosedTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: PackageFeatureState{Status: "validated"}}
	client := NewClientWithTransport(ModeShadow, time.Second, transport, nil)
	status, err := client.EnsurePackageFeature(context.Background(), "mdns", true)
	if err != nil || status != "validated" || transport.request.Operation != OperationPackageEnsure || !transport.request.DryRun {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
	var params PackageFeatureParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil || params.Feature != PackageFeatureMDNS {
		t.Fatalf("params/error=%#v/%v", params, err)
	}
	transport.result = PackageFeatureState{Status: "ready"}
	status, err = client.PackageFeatureStatus(context.Background(), "mdns")
	if err != nil || status != "ready" || transport.request.Operation != OperationPackageStatus || transport.request.DryRun {
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
	remaining := time.Until(transport.deadline)
	if remaining < privilegedImageProbeTimeout-time.Second || remaining > privilegedImageProbeTimeout {
		t.Fatalf("image probe client deadline has %v remaining, want %v", remaining, privilegedImageProbeTimeout)
	}
}

func TestClientAppImageRejectsInvalidState(t *testing.T) {
	transport := &fakeTransport{result: AppImageState{Status: "root-shell"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if _, err := client.AppImageStatus(context.Background(), "cpuminer", "baseline"); err == nil {
		t.Fatal("expected invalid image state to fail")
	}
}

func TestClientAppFirewallBuildsClosedTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: AppFirewallState{Status: "active"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, err := client.EnsureAppFirewall(context.Background(), "robosats", false)
	if err != nil || status != "active" || transport.request.Operation != OperationAppFirewallEnsure || transport.request.DryRun {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
	var params AppFirewallParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != "robosats" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestClientAppFirewallRejectsInvalidState(t *testing.T) {
	transport := &fakeTransport{result: AppFirewallState{Status: "root-shell"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if _, err := client.EnsureAppFirewall(context.Background(), "robosats", false); err == nil {
		t.Fatal("expected invalid firewall state to fail")
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

func TestClientAppLogsBuildsClosedTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: AppLogsState{Lines: []string{"one", "two"}, Source: "docker:gatewayd"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	lines, source, err := client.AppLogs(context.Background(), appmanifest.FedimintGatewayID, 20, "2h")
	if err != nil || source != "docker:gatewayd" || !reflect.DeepEqual(lines, []string{"one", "two"}) {
		t.Fatalf("lines/source/error=%#v/%q/%v", lines, source, err)
	}
	if transport.request.Operation != OperationAppLogs || transport.request.DryRun {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	var params AppLogsParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != appmanifest.FedimintGatewayID || params.Lines != 20 || params.Since != "2h" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestClientAppLNDHostAccessBuildsClosedTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: map[string]any{"ready": true, "changed": true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	if err := client.EnsureAppLNDHostAccess(context.Background(), appmanifest.BTCPayID, false); err != nil {
		t.Fatal(err)
	}
	if transport.request.Operation != OperationAppLNDHostAccessEnsure || transport.request.DryRun {
		t.Fatalf("unexpected request: %#v", transport.request)
	}
	var params AppLNDHostAccessParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.AppID != appmanifest.BTCPayID {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestClientBitcoinStorageBuildsClosedTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: BitcoinCoreStorageState{Status: "ready"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, err := client.EnsureBitcoinCoreStorage(context.Background(), "/mnt/bitcoin-ssd/bitcoin", false)
	if err != nil || status != "ready" || transport.request.Operation != OperationBitcoinStorageEnsure || transport.request.DryRun {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
	var params BitcoinCoreStorageParams
	if err := json.Unmarshal(transport.request.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.DataDir != "/mnt/bitcoin-ssd/bitcoin" {
		t.Fatalf("unexpected params: %#v", params)
	}

	transport.result = BitcoinCoreStorageState{Status: "root-shell"}
	if _, err := client.EnsureBitcoinCoreStorage(context.Background(), "/mnt/bitcoin-ssd/bitcoin", false); err == nil {
		t.Fatal("expected invalid storage state to fail")
	}
}

func TestClientBitcoinConsumerNetworkBuildsClosedTypedRequest(t *testing.T) {
	transport := &fakeTransport{result: BitcoinConsumerNetworkState{Status: "ready"}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	status, err := client.EnsureBitcoinConsumerNetwork(context.Background(), false)
	if err != nil || status != "ready" || transport.request.Operation != OperationBitcoinConsumerNetworkEnsure || transport.request.DryRun || string(transport.request.Params) != "{}" {
		t.Fatalf("status/error/request=%q/%v/%#v", status, err, transport.request)
	}
	remaining := time.Until(transport.deadline)
	if remaining < privilegedLongOperationTimeout-time.Second || remaining > privilegedLongOperationTimeout {
		t.Fatalf("bitcoin consumer network deadline has %v remaining, want %v", remaining, privilegedLongOperationTimeout)
	}

	transport.result = BitcoinConsumerNetworkState{Status: "attacker-controlled"}
	if _, err := client.EnsureBitcoinConsumerNetwork(context.Background(), false); err == nil {
		t.Fatal("expected invalid network state to fail")
	}
}
