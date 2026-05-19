package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lightningos-light/internal/electrs"
)

func TestParseProvenancePrimary(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		invalid string
	}{
		{raw: "", want: provenancePrimaryChain},
		{raw: "chain", want: provenancePrimaryChain},
		{raw: "bitcoind", want: provenancePrimaryBitcoind},
		{raw: "bitcoin-core", want: provenancePrimaryBitcoind},
		{raw: "electrs", want: provenancePrimaryElectrs},
		{raw: "electrum", want: provenancePrimaryElectrs},
		{raw: "public", want: provenancePrimaryChain, invalid: "public"},
	}

	for _, tc := range tests {
		got, invalid := parseProvenancePrimary(tc.raw)
		if got != tc.want || invalid != tc.invalid {
			t.Fatalf("parseProvenancePrimary(%q) = (%q, %q), want (%q, %q)", tc.raw, got, invalid, tc.want, tc.invalid)
		}
	}
}

func TestBuildProvenanceSourceChainDefaultKeepsFallbackOrder(t *testing.T) {
	t.Setenv("PROVENANCE_PRIMARY", "")
	t.Setenv("PROVENANCE_PUBLIC_ELECTRUM", "disabled")
	bitcoind := NewBitcoinCoreSource(func(context.Context) (bool, string) { return true, "" })

	chain, _ := buildProvenanceSourceChain(true, bitcoind, nil)
	names := sourceNames(chain.Sources())
	if len(names) != 2 || names[0] != "bitcoind" || names[1] != "local electrs" {
		t.Fatalf("unexpected source order: %+v", names)
	}
}

func TestBuildProvenanceSourceChainForcedBitcoind(t *testing.T) {
	t.Setenv("PROVENANCE_PRIMARY", "bitcoind")
	t.Setenv("PROVENANCE_PUBLIC_ELECTRUM", "")
	bitcoind := NewBitcoinCoreSource(func(context.Context) (bool, string) { return true, "" })

	chain, _ := buildProvenanceSourceChain(true, bitcoind, nil)
	names := sourceNames(chain.Sources())
	if len(names) != 1 || names[0] != "bitcoind" {
		t.Fatalf("expected only bitcoind, got %+v", names)
	}
}

func TestBuildProvenanceSourceChainForcedElectrs(t *testing.T) {
	t.Setenv("PROVENANCE_PRIMARY", "electrs")
	t.Setenv("PROVENANCE_PUBLIC_ELECTRUM", "")
	bitcoind := NewBitcoinCoreSource(func(context.Context) (bool, string) { return true, "" })

	chain, _ := buildProvenanceSourceChain(true, bitcoind, nil)
	names := sourceNames(chain.Sources())
	if len(names) != 1 || names[0] != "local electrs" {
		t.Fatalf("expected only local electrs, got %+v", names)
	}
}

func TestProvenanceBitcoinCoreConfigAvailabilityAllowsExternalLocalBitcoind(t *testing.T) {
	server := httptest.NewServer(bitcoinRPCMockHandler(t, true, false, false))
	defer server.Close()

	avail := provenanceBitcoinCoreConfigAvailability(context.Background(), bitcoinRPCConfig{
		Host: server.URL,
		User: "user",
		Pass: "pass",
	})
	if !avail.Available || avail.Reason != "" {
		t.Fatalf("expected available external local bitcoind, got %+v", avail)
	}
}

func TestProvenanceBitcoinCoreConfigAvailabilityRequiresTxIndex(t *testing.T) {
	server := httptest.NewServer(bitcoinRPCMockHandler(t, false, false, false))
	defer server.Close()

	avail := provenanceBitcoinCoreConfigAvailability(context.Background(), bitcoinRPCConfig{
		Host: server.URL,
		User: "user",
		Pass: "pass",
	})
	if avail.Available || avail.Reason != fullIndexUnavailableTxIndex {
		t.Fatalf("expected txindex unavailable, got %+v", avail)
	}
}

func TestProvenanceBitcoinCoreConfigAvailabilityRejectsPruned(t *testing.T) {
	server := httptest.NewServer(bitcoinRPCMockHandler(t, true, true, false))
	defer server.Close()

	avail := provenanceBitcoinCoreConfigAvailability(context.Background(), bitcoinRPCConfig{
		Host: server.URL,
		User: "user",
		Pass: "pass",
	})
	if avail.Available || avail.Reason != fullIndexUnavailableUnpruned {
		t.Fatalf("expected pruned unavailable, got %+v", avail)
	}
}

func TestBitcoinInfoStillSyncing(t *testing.T) {
	if !bitcoinInfoStillSyncing(bitcoinInfo{InitialBlockDownload: true}) {
		t.Fatalf("expected IBD to be syncing")
	}
	if !bitcoinInfoStillSyncing(bitcoinInfo{Blocks: 10, Headers: 11, VerificationProgress: 1}) {
		t.Fatalf("expected headers ahead to be syncing")
	}
	if bitcoinInfoStillSyncing(bitcoinInfo{Blocks: 11, Headers: 11, VerificationProgress: 1}) {
		t.Fatalf("expected synced info")
	}
}

func sourceNames(sources []electrs.TxSource) []string {
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.Name())
	}
	return names
}

func bitcoinRPCMockHandler(t *testing.T, txIndexSynced bool, pruned bool, syncing bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode rpc request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "getblockchaininfo":
			progress := 1.0
			blocks := int64(100)
			headers := int64(100)
			if syncing {
				progress = 0.5
				blocks = 50
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"chain":                "main",
					"blocks":               blocks,
					"headers":              headers,
					"verificationprogress": progress,
					"initialblockdownload": syncing,
					"pruned":               pruned,
				},
			})
		case "getindexinfo":
			result := map[string]any{}
			if txIndexSynced {
				result["txindex"] = map[string]any{"synced": true}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
		default:
			t.Fatalf("unexpected rpc method %q", req.Method)
		}
	}
}
