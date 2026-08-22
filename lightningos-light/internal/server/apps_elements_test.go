package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lightningos-light/internal/system"
)

type elementsPrivilegedTestClient struct {
	cpuMinerPrivilegedClient
	statusJSON       string
	statusErr        error
	statusCalls      int
	statusDataDir    string
	config           string
	configErr        error
	lifecycleCalls   int
	lifecycleAction  string
	lifecycleDataDir string
}

func (client *elementsPrivilegedTestClient) ElementsStatus(_ context.Context, dataDir string) (string, error) {
	client.statusCalls++
	client.statusDataDir = dataDir
	return client.statusJSON, client.statusErr
}

func (client *elementsPrivilegedTestClient) ReadElementsConfig(_ context.Context, _ string) (string, error) {
	return client.config, client.configErr
}

func (client *elementsPrivilegedTestClient) EnsureElements(_ context.Context, _ string, _ string, _ bool) (string, error) {
	return "ready", nil
}

func (client *elementsPrivilegedTestClient) ElementsLifecycle(_ context.Context, dataDir string, action string, _ bool) (string, error) {
	client.lifecycleCalls++
	client.lifecycleAction = action
	client.lifecycleDataDir = dataDir
	return "ready", nil
}

func (client *elementsPrivilegedTestClient) RemoveElements(_ context.Context, _ string, _ bool) error {
	return nil
}

func configureElementsTestClient(t *testing.T, client *elementsPrivilegedTestClient) {
	t.Helper()
	client.mode = "enforce"
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
}

func TestElementsInfoUsesBrokerAsAuthoritativeInstallationState(t *testing.T) {
	client := &elementsPrivilegedTestClient{
		statusJSON: `{"installed":true,"status":"running","data_dir":"/data/elements","rpc_ok":true}`,
	}
	configureElementsTestClient(t, client)

	info, err := (elementsApp{}).Info(context.Background())
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if !info.Installed || info.Status != "running" {
		t.Fatalf("expected broker-installed running Elements, got %+v", info)
	}
	if client.statusCalls != 1 || client.statusDataDir != elementsAppPaths().DataDir {
		t.Fatalf("unexpected broker status request: calls=%d data_dir=%q", client.statusCalls, client.statusDataDir)
	}
}

func TestElementsInfoNormalizesBrokerNotInstalledState(t *testing.T) {
	client := &elementsPrivilegedTestClient{
		statusJSON: `{"installed":false,"status":"stopped","data_dir":"/data/elements"}`,
	}
	configureElementsTestClient(t, client)

	info, err := (elementsApp{}).Info(context.Background())
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if info.Installed || info.Status != "not_installed" {
		t.Fatalf("expected normalized not-installed state, got %+v", info)
	}
}

func TestElementsStatusHandlerUsesSingleBrokerSnapshot(t *testing.T) {
	client := &elementsPrivilegedTestClient{
		statusJSON: `{"installed":true,"status":"running","data_dir":"/data/elements","rpc_ok":true,"chain":"liquidv1","blocks":123,"headers":124,"verification_progress":0.99,"initial_block_download":true,"peers":7,"version":230200,"subversion":"/Elements:23.2.0/","size_on_disk":456}`,
		config:     "mainchainrpchost=bitcoin.example\nmainchainrpcport=8332\n",
	}
	configureElementsTestClient(t, client)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/elements/status", nil)
	(&Server{}).handleElementsStatus(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", recorder.Code)
	}
	var response elementsStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Installed || response.Status != "running" || !response.RPCOk {
		t.Fatalf("unexpected Elements status response: %+v", response)
	}
	if response.Blocks != 123 || response.Peers != 7 || response.MainchainRPCHost != "bitcoin.example" {
		t.Fatalf("broker snapshot fields were not preserved: %+v", response)
	}
	if client.statusCalls != 1 {
		t.Fatalf("expected one authoritative broker snapshot, got %d calls", client.statusCalls)
	}
}

func TestPeerswapLocalElementsReadinessUsesBrokerState(t *testing.T) {
	client := &elementsPrivilegedTestClient{
		statusJSON: `{"installed":true,"status":"running","data_dir":"/data/elements"}`,
	}
	configureElementsTestClient(t, client)

	ready, status := peerswapLocalElementsReady(context.Background())
	if !ready || status != "running" {
		t.Fatalf("expected local Elements ready from broker, got ready=%v status=%q", ready, status)
	}
}

func TestStopElementsUsesBrokerInstallationState(t *testing.T) {
	client := &elementsPrivilegedTestClient{
		statusJSON: `{"installed":true,"status":"running","data_dir":"/data/elements"}`,
	}
	configureElementsTestClient(t, client)

	if err := (&Server{}).stopElements(context.Background()); err != nil {
		t.Fatalf("stopElements returned error: %v", err)
	}
	if client.lifecycleCalls != 1 || client.lifecycleAction != "stop" || client.lifecycleDataDir != elementsAppPaths().DataDir {
		t.Fatalf("unexpected lifecycle request: calls=%d action=%q data_dir=%q", client.lifecycleCalls, client.lifecycleAction, client.lifecycleDataDir)
	}
}

func TestStopElementsRejectsBrokerNotInstalledState(t *testing.T) {
	client := &elementsPrivilegedTestClient{
		statusJSON: `{"installed":false,"status":"stopped","data_dir":"/data/elements"}`,
	}
	configureElementsTestClient(t, client)

	err := (&Server{}).stopElements(context.Background())
	if err == nil || err.Error() != "Elements is not installed" {
		t.Fatalf("expected not-installed error, got %v", err)
	}
	if client.lifecycleCalls != 0 {
		t.Fatalf("lifecycle must not run for an uninstalled app, got %d calls", client.lifecycleCalls)
	}
}

func TestElementsBrokerStatusRejectsInvalidResponse(t *testing.T) {
	client := &elementsPrivilegedTestClient{statusJSON: "{"}
	configureElementsTestClient(t, client)

	_, err := elementsBrokerStatus(context.Background(), elementsAppPaths())
	if err == nil || err.Error() != "invalid Elements broker status" {
		t.Fatalf("expected invalid broker response error, got %v", err)
	}
}

func TestElementsDefinitionHasNoLNDDisclaimer(t *testing.T) {
	if notices := elementsDefinition().SecurityNotices; len(notices) != 0 {
		t.Fatalf("Elements must not declare an unrelated LND disclaimer: %v", notices)
	}
}

func TestParseElementsLocalBitcoinRPCConfigFromLNDConfPrefersActiveLocal(t *testing.T) {
	raw := `[Bitcoind]
bitcoind.rpchost=127.0.0.1:18443
bitcoind.rpcuser=active-user
bitcoind.rpcpass=active-pass

# LightningOS Bitcoin Local
# bitcoind.rpchost=127.0.0.1:8332
# bitcoind.rpcuser=tagged-user
# bitcoind.rpcpass=tagged-pass
`

	cfg, ok := parseElementsLocalBitcoinRPCConfigFromLNDConf(raw)
	if !ok {
		t.Fatalf("expected local config")
	}
	if cfg.Host != "127.0.0.1:18443" || cfg.User != "active-user" || cfg.Pass != "active-pass" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestParseElementsLocalBitcoinRPCConfigFromLNDConfFallsBackToTaggedLocal(t *testing.T) {
	raw := `[Bitcoind]
# LightningOS Bitcoin Remote
bitcoind.rpchost=bitcoin.br-ln.com:8085
bitcoind.rpcuser=remote-user
bitcoind.rpcpass=remote-pass

# LightningOS Bitcoin Local
# bitcoind.rpchost=127.0.0.1:8333
# bitcoind.rpcuser=local-user
# bitcoind.rpcpass=local-pass
# bitcoind.zmqpubrawblock=tcp://127.0.0.1:28334
# bitcoind.zmqpubrawtx=tcp://127.0.0.1:28335
`

	cfg, ok := parseElementsLocalBitcoinRPCConfigFromLNDConf(raw)
	if !ok {
		t.Fatalf("expected tagged local config")
	}
	if cfg.Host != "127.0.0.1:8333" || cfg.User != "local-user" || cfg.Pass != "local-pass" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.ZMQBlock != "tcp://127.0.0.1:28334" || cfg.ZMQTx != "tcp://127.0.0.1:28335" {
		t.Fatalf("unexpected zmq config: %+v", cfg)
	}
}

func TestParseElementsLocalBitcoinRPCConfigFromLNDConfRejectsMissingCredentials(t *testing.T) {
	raw := `[Bitcoind]
bitcoind.rpchost=127.0.0.1:8332
bitcoind.rpcuser=local-user
`

	if cfg, ok := parseElementsLocalBitcoinRPCConfigFromLNDConf(raw); ok {
		t.Fatalf("expected missing password to be rejected, got %+v", cfg)
	}
}

func TestParseBitcoindRPCConfigFromLNDConfNormalizesURLHost(t *testing.T) {
	raw := `[Bitcoind]
bitcoind.rpchost=http://127.0.0.1:8332
bitcoind.rpcuser=local-user
bitcoind.rpcpass=local-pass
`

	cfg, ok := parseBitcoindRPCConfigFromLNDConf(raw)
	if !ok {
		t.Fatalf("expected config")
	}
	if cfg.Host != "127.0.0.1:8332" {
		t.Fatalf("expected normalized host, got %q", cfg.Host)
	}
}

func TestParseBitcoindRPCConfigFromLNDConfAcceptsGlobalExistingNodeOptions(t *testing.T) {
	raw := `bitcoin.active=1
bitcoin.mainnet=1
bitcoind.rpchost=remote.example:8332
bitcoind.rpcuser=existing-user
bitcoind.rpcpass=existing-pass
bitcoind.zmqpubrawblock=tcp://remote.example:28332
bitcoind.zmqpubrawtx=tcp://remote.example:28333
`

	cfg, ok := parseBitcoindRPCConfigFromLNDConf(raw)
	if !ok {
		t.Fatalf("expected global existing-node config")
	}
	if cfg.Host != "remote.example:8332" || cfg.User != "existing-user" || cfg.Pass != "existing-pass" {
		t.Fatalf("unexpected global config: %+v", cfg)
	}
}

func TestLocalBitcoinConfigCandidatesIncludesAdminBitcoinConf(t *testing.T) {
	paths := bitcoinCorePaths{ConfigPath: "/data/bitcoin/bitcoin.conf"}
	candidates := localBitcoinConfigCandidates(paths)
	if !stringInSlice("/home/admin/.bitcoin/bitcoin.conf", candidates) {
		t.Fatalf("expected /home/admin/.bitcoin/bitcoin.conf in candidates: %#v", candidates)
	}
}

func TestNormalizeElementsDataDir(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "blank defaults", value: "", want: elementsDefaultDataDir},
		{name: "cleans path", value: "/mnt/liquid/../liquid/elements/", want: "/mnt/liquid/elements"},
		{name: "rejects relative", value: "mnt/liquid/elements", wantErr: true},
		{name: "rejects root", value: "/", wantErr: true},
		{name: "rejects system dir", value: "/var/lib/elements", wantErr: true},
		{name: "rejects bitcoin dir", value: "/data/bitcoin/elements", wantErr: true},
		{name: "rejects spaces", value: "/mnt/liquid ssd/elements", wantErr: true},
		{name: "rejects shell chars", value: "/mnt/liquid;ssd/elements", wantErr: true},
		{name: "allows data elements default", value: "/data/elements", want: "/data/elements"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeElementsDataDir(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
