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
