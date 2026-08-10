package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBIP110VersionSignals(t *testing.T) {
	tests := []struct {
		name    string
		version int64
		want    bool
	}{
		{name: "bip9 bit four", version: 0x20000010, want: true},
		{name: "bip9 without bit four", version: 0x20000000, want: false},
		{name: "bit four without bip9 prefix", version: 0x10000010, want: false},
		{name: "different bit", version: 0x20000008, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := bip110VersionSignals(test.version); got != test.want {
				t.Fatalf("bip110VersionSignals(%x) = %v, want %v", test.version, got, test.want)
			}
		})
	}
}

func TestBIP110MonitorComparesInternalWithPublic(t *testing.T) {
	var headerRequests atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/public", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "bip":"110","tip":2,"chainTip":2,"periodNum":0,"periodStart":0,"periodEnd":2015,
  "totalBlocks":3,"signalingCount":2,"pct":66.67,"synced":true,"updatedAt":"2026-07-22T12:00:00Z","periods":[]
}`))
	})
	mux.HandleFunc("/enforcing-tip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("1"))
	})
	mux.HandleFunc("/non-enforcing-tip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("2"))
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if len(raw) > 0 && raw[0] == '[' {
			var requests []bip110RPCRequest
			if err := json.Unmarshal(raw, &requests); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			responses := make([]map[string]any, 0, len(requests))
			for _, request := range requests {
				var result any
				switch request.Method {
				case "getblockhash":
					height := int64(request.Params[0].(float64))
					result = fmt.Sprintf("hash-%d", height)
				case "getblockheader":
					headerRequests.Add(1)
					hash := request.Params[0].(string)
					height, _ := strconv.ParseInt(strings.TrimPrefix(hash, "hash-"), 10, 64)
					version := int64(0x20000000)
					if height == 0 || height == 2 {
						version = 0x20000010
					}
					result = map[string]any{"version": version}
				default:
					http.Error(w, "unexpected batch method", http.StatusBadRequest)
					return
				}
				responses = append(responses, map[string]any{"id": request.ID, "result": result, "error": nil})
			}
			_ = json.NewEncoder(w).Encode(responses)
			return
		}

		var request bip110RPCRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result any
		switch request.Method {
		case "getblockchaininfo":
			result = map[string]any{"chain": "main", "blocks": 2, "bestblockhash": "hash-2", "chainwork": "000003"}
		case "getnetworkinfo":
			result = map[string]any{"subversion": "/Satoshi:test/"}
		case "getdeploymentinfo":
			result = map[string]any{"deployments": map[string]any{}}
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": request.ID, "result": result, "error": nil})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	service := newBIP110MonitorService(nil)
	service.client = server.Client()
	service.publicURL = server.URL + "/public"
	service.nonEnforcingURL = server.URL + "/non-enforcing-tip"
	service.enforcingURL = server.URL + "/enforcing-tip"
	service.now = func() time.Time { return time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) }
	service.loadRPCConfig = func(context.Context) (bitcoinRPCConfig, string, error) {
		return bitcoinRPCConfig{Host: server.URL + "/rpc"}, "test_bitcoind", nil
	}

	status := service.fetchStatus(context.Background(), service.now())
	if !status.Internal.Available || !status.Public.Available {
		t.Fatalf("expected both sources available: internal=%+v public=%+v", status.Internal, status.Public)
	}
	if status.Internal.SignalingCount != 2 || status.Internal.TotalBlocks != 3 {
		t.Fatalf("unexpected internal sample: %+v", status.Internal)
	}
	if !status.Comparison.Comparable || !status.Comparison.Matches || status.Comparison.Status != "matched" {
		t.Fatalf("expected matching comparison, got %+v", status.Comparison)
	}
	if status.Internal.BestBlockHash != "hash-2" || status.Internal.Chainwork != "000003" {
		t.Fatalf("missing internal chain identity: %+v", status.Internal)
	}
	if got := headerRequests.Load(); got != 3 {
		t.Fatalf("initial header requests = %d, want 3", got)
	}
	second := service.fetchStatus(context.Background(), service.now())
	if !second.Comparison.Matches {
		t.Fatalf("second comparison did not match: %+v", second.Comparison)
	}
	if got := headerRequests.Load(); got != 3 {
		t.Fatalf("cached refresh fetched %d total headers, want 3", got)
	}
}

func TestCompareBIP110SourcesRequiresSameSampleHeight(t *testing.T) {
	internal := bip110SourceStatus{
		Available: true, Tip: 100, SampledTip: 99, PeriodStart: 0, PeriodEnd: 2015,
		TotalBlocks: 100, SignalingCount: 1, Pct: 1,
	}
	public := bip110SourceStatus{
		Available: true, Tip: 100, SampledTip: 100, PeriodStart: 0, PeriodEnd: 2015,
		TotalBlocks: 101, SignalingCount: 1, Pct: 0.99,
	}
	comparison := compareBIP110Sources(internal, public)
	if comparison.Comparable || comparison.Status != "tip_mismatch" {
		t.Fatalf("expected tip mismatch, got %+v", comparison)
	}
}

func TestBIP110RiskLevelNearMandatoryWindow(t *testing.T) {
	source := bip110SourceStatus{Available: true, Pct: 1, Synced: true}
	comparison := bip110Comparison{Status: "matched", Comparable: true, Matches: true}
	if got := bip110RiskLevel(bip110MandatoryStartHeight-100, source, source, comparison, "voluntary_signaling"); got != "elevated" {
		t.Fatalf("risk before mandatory window = %q, want elevated", got)
	}
	if got := bip110RiskLevel(bip110MandatoryStartHeight+100, source, source, comparison, "mandatory_signaling"); got != "high" {
		t.Fatalf("risk during mandatory window = %q, want high", got)
	}
}

func TestBIP110RiskLevelIsLowForBitcoinCore(t *testing.T) {
	enforces := false
	source := bip110SourceStatus{Available: true, Pct: 0, Synced: true, EnforcesBIP110: &enforces}
	comparison := bip110Comparison{Status: "matched", Comparable: true, Matches: true}
	if got := bip110RiskLevel(bip110MandatoryStartHeight+100, source, source, comparison, "mandatory_signaling"); got != "low" {
		t.Fatalf("Bitcoin Core risk during mandatory window = %q, want low", got)
	}
	comparison.Status = "signal_mismatch"
	if got := bip110RiskLevel(bip110MandatoryStartHeight+100, source, source, comparison, "mandatory_signaling"); got != "watch" {
		t.Fatalf("Bitcoin Core risk with source mismatch = %q, want watch", got)
	}
}

func TestBIP110SourceStatusKeepsZeroScoreInJSON(t *testing.T) {
	payload, err := json.Marshal(bip110SourceStatus{Available: true, Source: "test", TotalBlocks: 93})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"pct", "signaling_count", "total_blocks"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("zero-valued %s missing from %s", field, payload)
		}
	}
}

func TestBuildBIP110ForkScoreUsesRealBranchTips(t *testing.T) {
	status := buildBIP110ForkScore(
		961725, nil, "https://mempool.space/api/blocks/tip/height",
		961633, nil, "https://bip110.mempool.guide/api/blocks/tip/height",
	)
	if !status.Available {
		t.Fatalf("expected fork score available: %+v", status)
	}
	if status.SplitHeight != 961631 || status.NonEnforcingBlocks != 94 || status.EnforcingBlocks != 2 {
		t.Fatalf("unexpected fork score: %+v", status)
	}
}

func TestBIP110EnforcingTipUsesDedicatedExplorer(t *testing.T) {
	const expected = "https://bip110.mempool.guide/api/blocks/tip/height"
	if bip110EnforcingTipURL != expected {
		t.Fatalf("enforcing tip URL = %q, want %q", bip110EnforcingTipURL, expected)
	}
}

func TestBuildBIP110ForkScoreDoesNotInventMissingBranch(t *testing.T) {
	status := buildBIP110ForkScore(961725, nil, "non-enforcing", 0, errors.New("source unavailable"), "enforcing")
	if status.Available || status.EnforcingBlocks != 0 || status.Error == "" {
		t.Fatalf("missing enforcing branch should remain unavailable: %+v", status)
	}
}

func TestBIP110EffectiveMilestonesUsesEarlyCompletedThreshold(t *testing.T) {
	public := bip110SourceStatus{RecentPeriods: []bip110Period{{
		StartBlock:     100,
		EndBlock:       2115,
		SignalingCount: bip110ThresholdCount,
		TotalBlocks:    int(bip110PeriodLength),
	}}}
	lockIn, activation := bip110EffectiveMilestones(2116, bip110SourceStatus{}, public)
	if lockIn != 2116 || activation != 4132 {
		t.Fatalf("milestones = (%d, %d), want (2116, 4132)", lockIn, activation)
	}
	if phase := bip110Phase(2116, lockIn, activation); phase != "locked_in" {
		t.Fatalf("phase at early lock-in = %q, want locked_in", phase)
	}
}
