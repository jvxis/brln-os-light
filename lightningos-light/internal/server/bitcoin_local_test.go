package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/system"
)

type bitcoinConfigTestClient struct {
	*cpuMinerPrivilegedClient
	operation       string
	dataDir         string
	content         string
	generateRPCAuth bool
	readContent     string
	dryRun          bool
	err             error
}

type bitcoinLocalStatusTestClient struct {
	*cpuMinerPrivilegedClient
	bitcoinStatusCalls int
	bitcoinStatusJSON  string
	bitcoinStatusErr   error
}

func (client *bitcoinLocalStatusTestClient) BitcoinCoreStatus(context.Context) (string, error) {
	client.bitcoinStatusCalls++
	return client.bitcoinStatusJSON, client.bitcoinStatusErr
}

func TestBitcoinLocalManagedStatusStopsBeforeRPCTelemetry(t *testing.T) {
	client := &bitcoinLocalStatusTestClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{
			mode:          "enforce",
			inspectStatus: "stopped",
		},
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	status := bitcoinLocalManagedStatus(context.Background(), bitcoinLocalStatus{
		Installed: true,
		Status:    "unknown",
		Source:    "app",
		DataDir:   "/data/bitcoin",
	})

	if status.Status != "stopped" || status.RPCOk {
		t.Fatalf("stopped app reported as %+v", status)
	}
	if client.inspectCalls != 1 || client.inspectAppID != bitcoinCoreAppID {
		t.Fatalf("unexpected lifecycle inspection: %#v", client.cpuMinerPrivilegedClient)
	}
	if client.bitcoinStatusCalls != 0 {
		t.Fatalf("stopped app reached RPC telemetry %d times", client.bitcoinStatusCalls)
	}
}

func TestBitcoinLocalManagedStatusReadsTelemetryOnlyWhenRunning(t *testing.T) {
	client := &bitcoinLocalStatusTestClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{
			mode:          "enforce",
			inspectStatus: "running",
		},
		bitcoinStatusJSON: `{"chain":"main","blocks":954700,"headers":954700,"verification_progress":1,"initial_block_download":false,"best_block_hash":"0000000000000000000000000000000000000000000000000000000000000000","pruned":false}`,
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	status := bitcoinLocalManagedStatus(context.Background(), bitcoinLocalStatus{
		Installed: true,
		Source:    "app",
		DataDir:   "/data/bitcoin",
	})

	if status.Status != "running" || !status.RPCOk || status.Blocks != 954700 {
		t.Fatalf("running app telemetry reported as %+v", status)
	}
	if client.inspectCalls != 1 || client.bitcoinStatusCalls != 1 {
		t.Fatalf("unexpected broker calls: inspect=%d status=%d", client.inspectCalls, client.bitcoinStatusCalls)
	}
}

func (client *bitcoinConfigTestClient) EnsureBitcoinCoreConfig(_ context.Context, dataDir string, content string, generateRPCAuth bool, dryRun bool) (string, error) {
	client.generateRPCAuth = generateRPCAuth
	client.recordConfig("ensure", dataDir, content, dryRun)
	return "ready", client.err
}

func (client *bitcoinConfigTestClient) ReadBitcoinCoreCredentials(context.Context, string) (string, string, error) {
	return "", "", client.err
}

func (client *bitcoinConfigTestClient) EnsureBitcoinCoreCredentials(context.Context, string, bool) (string, string, string, bool, error) {
	return "lightningos", strings.Repeat("a", 64), "ready", false, client.err
}

func (client *bitcoinConfigTestClient) EnsureBitcoinCoreElectrsCredentials(context.Context, string, bool) (string, string, string, bool, error) {
	return "electrs", strings.Repeat("b", 64), "ready", false, client.err
}

func (client *bitcoinConfigTestClient) ReadBitcoinCoreConfig(_ context.Context, dataDir string) (string, error) {
	client.recordConfig("read", dataDir, "", false)
	return client.readContent, client.err
}

func (client *bitcoinConfigTestClient) WriteBitcoinCoreConfig(_ context.Context, dataDir string, content string, dryRun bool) (string, error) {
	client.recordConfig("write", dataDir, content, dryRun)
	return "ready", client.err
}

func (client *bitcoinConfigTestClient) recordConfig(operation string, dataDir string, content string, dryRun bool) {
	client.operation = operation
	client.dataDir = dataDir
	client.content = content
	client.dryRun = dryRun
}

func TestBitcoinCoreConfigServerPathsRequireTypedBroker(t *testing.T) {
	const dataDir = "/data/bitcoin"
	const legacy = "server=1\nrpcpassword=preserve-me\n"
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, bitcoinCoreConfigFile), []byte(legacy), 0640); err != nil {
		t.Fatal(err)
	}
	client := &bitcoinConfigTestClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: "enforce"},
		readContent:              legacy,
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	paths := bitcoinCorePaths{Root: root, DataDir: dataDir}

	if err := ensureBitcoinCoreConfig(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if client.operation != "ensure" || client.dataDir != dataDir || client.content != legacy || client.generateRPCAuth || client.dryRun {
		t.Fatalf("unexpected ensure request: %#v", client)
	}
	if _, err := os.Lstat(filepath.Join(root, bitcoinCoreConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("legacy seed was not removed after broker success: %v", err)
	}

	read, err := readBitcoinCoreConfigRaw(context.Background(), paths)
	if err != nil || read != legacy || client.operation != "read" {
		t.Fatalf("read/error/client=%q/%v/%#v", read, err, client)
	}
	const updated = "server=1\nrpcpassword=preserve-me\nprune=2048\n"
	if err := writeBitcoinCoreConfig(context.Background(), paths, updated); err != nil {
		t.Fatal(err)
	}
	if client.operation != "write" || client.content != updated || client.dataDir != dataDir {
		t.Fatalf("unexpected write request: %#v", client)
	}
}

func TestNewBitcoinCoreConfigRequestsBrokerGeneratedRPCAuth(t *testing.T) {
	client := &bitcoinConfigTestClient{cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: "enforce"}}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	paths := bitcoinCorePaths{Root: t.TempDir(), DataDir: "/data/bitcoin"}

	if err := ensureBitcoinCoreConfig(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if client.operation != "ensure" || !client.generateRPCAuth ||
		strings.Contains(client.content, "rpcpassword=") || strings.Contains(client.content, "rpcuser=") || strings.Contains(client.content, "rpcauth=") {
		t.Fatalf("new install did not delegate rpcauth generation to broker: %#v", client)
	}
}

func TestLegacyBitcoinCoreSeedCleanupUsesOnlyRegularExactFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, bitcoinCoreConfigFile)
	const content = "server=1\nrpcpassword=legacy\n"
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
	read, exists, err := readLegacyBitcoinCoreSeedConfig(root)
	if err != nil || !exists || read != content {
		t.Fatalf("read/exists/error=%q/%v/%v", read, exists, err)
	}
	if err := removeLegacyBitcoinCoreSeedConfig(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy seed still exists: %v", err)
	}
}

func TestLegacyBitcoinCoreSeedRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, bitcoinCoreConfigFile), 0750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readLegacyBitcoinCoreSeedConfig(root); err == nil {
		t.Fatal("expected directory seed to be rejected")
	}
	if err := removeLegacyBitcoinCoreSeedConfig(root); err == nil {
		t.Fatal("expected directory cleanup target to be rejected")
	}
}

func TestApplyBitcoinCLIChainInfoToLocalStatusKeepsBasicFieldsWithoutNetwork(t *testing.T) {
	status := bitcoinLocalStatus{}
	info := bitcoinCLIChainInfo{
		Chain:                "main",
		Blocks:               123,
		Headers:              124,
		VerificationProgress: 0.98,
		InitialBlockDownload: true,
		BestBlockHash:        "0000local",
		Pruned:               true,
		PruneHeight:          100,
		PruneTargetSize:      200,
		SizeOnDisk:           300,
	}

	applyBitcoinCLIChainInfoToLocalStatus(&status, info)

	if !status.RPCOk {
		t.Fatalf("expected rpc_ok to be true after chain info is available")
	}
	if status.Chain != info.Chain || status.Blocks != info.Blocks || status.Headers != info.Headers {
		t.Fatalf("expected basic chain fields to be copied: %+v", status)
	}
	if status.BestBlockHash != info.BestBlockHash {
		t.Fatalf("expected best block hash %q, got %q", info.BestBlockHash, status.BestBlockHash)
	}
	if status.Version != 0 || status.Subversion != "" || status.Connections != 0 {
		t.Fatalf("expected network metadata to stay unset without getnetworkinfo: %+v", status)
	}
}

func TestParseBitcoinCoreBrokerStatusPreservesUnknownZeroValues(t *testing.T) {
	raw := `{"chain":"main","blocks":954700,"headers":954701,"best_block_time":1780000000,"block_cadence_window_sec":600,"block_cadence":[{"start_time":1779992800,"end_time":1779993400,"count":2}],"verification_progress":0.999,"initial_block_download":true,"best_block_hash":"0000000000000000000000000000000000000000000000000000000000000000","pruned":false,"network_ok":true,"version":310100,"subversion":"/Satoshi:31.1.0/","connections":12}`
	status, err := parseBitcoinCoreBrokerStatus(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !status.RPCOk || status.Chain != "main" || status.Blocks != 954700 || status.Headers != 954701 || status.VerificationProgress != 0.999 || status.Pruned {
		t.Fatalf("unexpected broker status: %+v", status)
	}
	if status.BlockCadenceWindowSec != 600 || len(status.BlockCadence) != 1 || status.BlockCadence[0].Count != 2 {
		t.Fatalf("broker cadence was not preserved: %+v", status)
	}
	for _, invalid := range []string{
		`{}`,
		`{"chain":"regtest","blocks":1,"headers":1,"verification_progress":1,"best_block_hash":"x"}`,
		`{"chain":"main","blocks":2,"headers":1,"verification_progress":1,"best_block_hash":"x"}`,
	} {
		if _, err := parseBitcoinCoreBrokerStatus(invalid); err == nil {
			t.Fatalf("invalid broker status accepted: %s", invalid)
		}
	}
}

func TestParseBitcoinLogTipUsesLatestValidUpdate(t *testing.T) {
	raw := `2026-08-02T15:41:53Z UpdateTip: new best=00000000000000000001aaaa height=939713 version=0x1 date='2026-03-07T12:36:32Z' progress=0.950235 cache=3.8MiB
malformed UpdateTip: new best=broken height=nope date='bad' progress=two
2026-08-02T15:42:14Z UpdateTip: new best=00000000000000000002bbbb height=939714 version=0x2 date='2026-03-07T12:41:24Z' progress=0.950236 cache=4.8MiB`

	tip, ok := parseBitcoinLogTip(raw)
	if !ok {
		t.Fatalf("expected a valid UpdateTip entry")
	}
	if tip.Hash != "00000000000000000002bbbb" || tip.Height != 939714 || tip.Progress != 0.950236 {
		t.Fatalf("unexpected parsed tip: %+v", tip)
	}
	wantTime, err := time.Parse(time.RFC3339, "2026-03-07T12:41:24Z")
	if err != nil {
		t.Fatal(err)
	}
	if tip.Time != wantTime.Unix() {
		t.Fatalf("tip time = %d, want %d", tip.Time, wantTime.Unix())
	}
}

func TestApplyBitcoinLogTipKeepsRPCUnavailable(t *testing.T) {
	status := bitcoinLocalStatus{RPCOk: false}
	applyBitcoinLogTipToLocalStatus(&status, bitcoinLogTip{
		Hash:     "0000fallback",
		Height:   939714,
		Time:     1_772_887_284,
		Progress: 0.950236,
	})

	if status.RPCOk {
		t.Fatalf("log fallback must not report RPC as available")
	}
	if status.Blocks != 939714 || status.Headers != 0 || status.BestBlockHash != "0000fallback" {
		t.Fatalf("unexpected fallback status: %+v", status)
	}
	if !status.InitialBlockDownload || status.VerificationProgress != 0.950236 {
		t.Fatalf("expected fallback sync progress, got %+v", status)
	}
}

func TestApplyBitcoinLocalStatusToStatusIncludesCadence(t *testing.T) {
	status := bitcoinStatus{
		Mode:    "local",
		RPCHost: "127.0.0.1:8332",
	}
	local := bitcoinLocalStatus{
		Installed:             true,
		Status:                "running",
		Source:                "app",
		DataDir:               "/data/bitcoin",
		RPCOk:                 true,
		Connections:           12,
		Chain:                 "main",
		Blocks:                954690,
		Headers:               954690,
		BestBlockHash:         "0000active",
		BestBlockTime:         1_780_000_000,
		BlockCadenceWindowSec: blockCadenceWindowSec,
		BlockCadence: []blockCadenceBucket{
			{StartTime: 1_779_992_800, EndTime: 1_779_993_400, Count: 2},
		},
		VerificationProgress: 1,
		Version:              300000,
		Subversion:           "/Satoshi:30.0.0/",
		Pruned:               true,
		PruneHeight:          100,
		PruneTargetSize:      200,
		SizeOnDisk:           300,
	}

	applyBitcoinLocalStatusToStatus(&status, local)

	if status.Mode != "local" || status.RPCHost != "127.0.0.1:8332" {
		t.Fatalf("expected active connection fields to be preserved, got %+v", status)
	}
	if !status.Installed || status.Status != local.Status || status.Source != local.Source || status.DataDir != local.DataDir {
		t.Fatalf("expected local metadata to be copied: %+v", status)
	}
	if status.BestBlockHash != local.BestBlockHash || status.BestBlockTime != local.BestBlockTime {
		t.Fatalf("expected best block metadata to be copied: %+v", status)
	}
	if status.Connections != local.Connections || status.Version != local.Version || status.Subversion != local.Subversion {
		t.Fatalf("expected network metadata to be copied: %+v", status)
	}
	if !status.Pruned || status.PruneHeight != local.PruneHeight || status.PruneTargetSize != local.PruneTargetSize || status.SizeOnDisk != local.SizeOnDisk {
		t.Fatalf("expected prune metadata to be copied: %+v", status)
	}
	if status.BlockCadenceWindowSec != local.BlockCadenceWindowSec || len(status.BlockCadence) != 1 || status.BlockCadence[0].Count != 2 {
		t.Fatalf("expected cadence to be copied: %+v", status)
	}
	local.BlockCadence[0].Count = 99
	if status.BlockCadence[0].Count != 2 {
		t.Fatalf("expected cadence slice to be copied defensively, got %+v", status.BlockCadence)
	}
}

func TestContextHasBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if contextHasBudget(ctx, 200*time.Millisecond) {
		t.Fatalf("expected insufficient budget before deadline")
	}
	if !contextHasBudget(ctx, 10*time.Millisecond) {
		t.Fatalf("expected small budget to fit before deadline")
	}
	if !contextHasBudget(context.Background(), time.Second) {
		t.Fatalf("expected background context to be treated as having budget")
	}
}
