package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRPCAllowListContainsIPCoveredByCIDR(t *testing.T) {
	lines := []string{
		"rpcallowip=127.0.0.1",
		"rpcallowip=172.21.0.0/16",
	}
	if !rpcAllowListContains(lines, "172.21.0.42") {
		t.Fatalf("expected IP to be allowed by CIDR")
	}
}

func TestRPCAllowListContainsCIDRExactMatch(t *testing.T) {
	lines := []string{
		"rpcallowip=172.22.0.0/16",
	}
	if !rpcAllowListContains(lines, "172.22.0.0/16") {
		t.Fatalf("expected CIDR exact match to be detected")
	}
}

func TestEnsureBitcoinCoreRPCAllowListAvoidsDuplicateCIDR(t *testing.T) {
	raw := "rpcallowip=127.0.0.1\nrpcallowip=172.23.0.0/16\n"
	updated, changed := ensureBitcoinCoreRPCAllowList(raw, []string{"172.23.0.0/16"})
	if changed {
		t.Fatalf("expected no change when CIDR already exists, got: %q", updated)
	}
}

func TestEnsureBitcoinCoreRPCAllowListSkipsInvalidAllowEntry(t *testing.T) {
	raw := "server=1\n"
	updated, changed := ensureBitcoinCoreRPCAllowList(raw, []string{"invalid IP"})
	if changed {
		t.Fatalf("expected invalid allow entry to be ignored, got: %q", updated)
	}
	if strings.Contains(updated, "invalid IP") {
		t.Fatalf("expected invalid allow entry to be absent, got: %q", updated)
	}
}

func TestEnsureBitcoinCoreRPCAllowListRemovesInvalidExistingAllowIP(t *testing.T) {
	raw := "server=1\nrpcallowip=invalid IP\nrpcallowip=127.0.0.1\n"
	updated, changed := ensureBitcoinCoreRPCAllowList(raw, []string{"invalid IP", "127.0.0.1"})
	if !changed {
		t.Fatalf("expected invalid existing rpcallowip to be removed")
	}
	if strings.Contains(updated, "invalid IP") {
		t.Fatalf("expected invalid rpcallowip to be removed, got: %q", updated)
	}
	if count := strings.Count(updated, "rpcallowip=127.0.0.1"); count != 1 {
		t.Fatalf("expected one localhost allow entry, got %d in %q", count, updated)
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
	if status.Version != 0 || status.Subversion != "" || status.Connections != 0 {
		t.Fatalf("expected network metadata to stay unset without getnetworkinfo: %+v", status)
	}
}

func TestApplyBitcoinCLIChainInfoToStatusIncludesPruneFields(t *testing.T) {
	status := bitcoinStatus{}
	info := bitcoinCLIChainInfo{
		Chain:                "main",
		Blocks:               456,
		Headers:              456,
		VerificationProgress: 1,
		InitialBlockDownload: false,
		BestBlockHash:        "0000abc",
		Pruned:               true,
		PruneHeight:          150,
		PruneTargetSize:      12_345,
		SizeOnDisk:           67_890,
	}

	applyBitcoinCLIChainInfoToStatus(&status, info)

	if !status.Pruned {
		t.Fatalf("expected pruned=true to be copied to active bitcoin status")
	}
	if status.PruneHeight != info.PruneHeight || status.PruneTargetSize != info.PruneTargetSize || status.SizeOnDisk != info.SizeOnDisk {
		t.Fatalf("expected prune metadata to be copied: %+v", status)
	}
}

func TestApplyBitcoinCLINetworkInfoToStatusIncludesConnections(t *testing.T) {
	status := bitcoinStatus{}
	info := bitcoinCLINetworkInfo{
		Version:     300000,
		Subversion:  "/Satoshi:30.0.0/",
		Connections: 11,
	}

	applyBitcoinCLINetworkInfoToStatus(&status, info)

	if status.Connections != info.Connections {
		t.Fatalf("expected connections %d, got %d", info.Connections, status.Connections)
	}
	if status.Version != info.Version || status.Subversion != info.Subversion {
		t.Fatalf("expected network metadata to be copied: %+v", status)
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
