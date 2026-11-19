package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBitcoinRPCParamsReportsTruncatedHTTPBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, `{"result":`)
	}))
	defer srv.Close()

	for _, host := range []string{srv.URL, strings.TrimPrefix(srv.URL, "http://")} {
		body, err := fetchBitcoinRPCParams(context.Background(), host, "", "", "getblock", nil)
		if !errors.Is(err, io.ErrUnexpectedEOF) || body != nil {
			t.Fatalf("want a wrapped read error and no partial body; got %q, %v", body, err)
		}
	}
}

func TestGraphCloseBatchRetainsProgressAndContinuesAfterRPCError(t *testing.T) {
	candidates := []graphCloseClassificationCandidate{{EventID: 1}, {EventID: 2}, {EventID: 3}, {EventID: 4}}
	resolved, attempts := collectGraphCloseClassifications(context.Background(), candidates, "", graphExplorerLocalClosedChannelLookup{}, func(ctx context.Context, c graphCloseClassificationCandidate) (graphCloseClassificationResult, bool, string, error) {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > graphCloseCandidateTimeout {
			t.Fatal("missing per-candidate RPC deadline")
		}
		switch c.EventID {
		case 2:
			return graphCloseClassificationResult{}, false, "", context.DeadlineExceeded
		case 4:
			return graphCloseClassificationResult{}, false, "invalid_chan_point", nil
		default:
			return graphCloseClassificationResult{CloseType: "mutual_close"}, true, "", nil
		}
	})
	if len(resolved) != 2 || resolved[0].Candidate.EventID != 1 || resolved[1].Candidate.EventID != 3 {
		t.Fatalf("healthy events before and after the failure must survive: %+v", resolved)
	}
	if len(attempts) != 2 || attempts[0] != (graphCloseClassificationAttempt{EventID: 2, Error: "bitcoin_rpc_timeout", Retryable: true}) ||
		attempts[1] != (graphCloseClassificationAttempt{EventID: 4, Error: "invalid_chan_point"}) {
		t.Fatalf("expected distinct retryable/ordinary attempts: %+v", attempts)
	}
}

func TestGraphCloseBatchRetainsLocalResultsWhenRPCBudgetEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	localKey := "02" + strings.Repeat("a", 64)
	lookup := graphExplorerLocalClosedChannelLookup{
		byChanID: map[uint64]graphExplorerLocalClosedChannelInfo{3: {CloseType: "cooperative_close"}},
	}
	candidates := []graphCloseClassificationCandidate{{EventID: 1}, {EventID: 2}, {EventID: 3, ChanID: 3, Node1PubKey: localKey}}
	resolved, attempts := collectGraphCloseClassifications(ctx, candidates, localKey, lookup, func(_ context.Context, c graphCloseClassificationCandidate) (graphCloseClassificationResult, bool, string, error) {
		if c.EventID != 1 {
			t.Fatalf("no further RPC should start after budget ends; got event %d", c.EventID)
		}
		cancel()
		return graphCloseClassificationResult{}, false, "", context.DeadlineExceeded
	})
	if len(resolved) != 1 || resolved[0].Candidate.EventID != 3 || resolved[0].Result.CloseClassifier != "lnd" {
		t.Fatalf("local classification must not depend on RPC budget: %+v", resolved)
	}
	if len(attempts) != 1 || attempts[0].EventID != 1 || !attempts[0].Retryable {
		t.Fatalf("only the attempted RPC should be counted: %+v", attempts)
	}
}

func TestParseGraphCloseFundingOutpoint(t *testing.T) {
	txid, vout, err := parseGraphCloseFundingOutpoint("4157d99065512da061556906ff9a71fb40ebe5899113c3493a75751065e8a808:1")
	if err != nil {
		t.Fatalf("parseGraphCloseFundingOutpoint returned error: %v", err)
	}
	if txid != "4157d99065512da061556906ff9a71fb40ebe5899113c3493a75751065e8a808" {
		t.Fatalf("unexpected txid: %s", txid)
	}
	if vout != 1 {
		t.Fatalf("unexpected vout: %d", vout)
	}
}

func TestParseGraphCloseFundingOutpointRejectsInvalidValue(t *testing.T) {
	if _, _, err := parseGraphCloseFundingOutpoint("bad-value"); err == nil {
		t.Fatal("expected parseGraphCloseFundingOutpoint to reject invalid chan_point")
	}
}

func TestGraphCloseFundingOutputRangeRejectsUint32Overflow(t *testing.T) {
	if graphCloseFundingOutputInRange(^uint32(0), 1) {
		t.Fatal("maximum uint32 funding vout was accepted for a one-output transaction")
	}
	if !graphCloseFundingOutputInRange(0, 1) {
		t.Fatal("valid funding vout was rejected")
	}
}

func TestFindSpendingTransactionInBlock(t *testing.T) {
	block := bitcoinVerboseBlock{
		Tx: []bitcoinVerboseTransaction{
			{
				TxID: "close-tx",
				Vin: []bitcoinVerboseTxInput{
					{TxID: "funding-tx", Vout: 3},
				},
			},
		},
	}

	match := findSpendingTransactionInBlock(block, "funding-tx", 3)
	if match == nil {
		t.Fatal("expected to find spending transaction in block")
	}
	if match.TxID != "close-tx" {
		t.Fatalf("unexpected txid: %s", match.TxID)
	}
}

func TestGraphCloseSearchHeights(t *testing.T) {
	got := graphCloseSearchHeights(943353)
	want := []uint32{943353, 943352, 943354}
	if len(got) != len(want) {
		t.Fatalf("unexpected length: got %d want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("graphCloseSearchHeights mismatch at %d: got %d want %d", index, got[index], want[index])
		}
	}
}

func TestClassifyGraphCloseTransactionShapeMutual(t *testing.T) {
	tx := bitcoinVerboseTransaction{
		TxID: "mutual-close",
		Vout: []bitcoinVerboseTxOutput{
			{
				Value: 0.49,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v0_keyhash"},
			},
			{
				Value: 0.50,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v1_taproot"},
			},
		},
	}

	closeType, confidence, reason := classifyGraphCloseTransactionShape(tx)
	if closeType != "mutual_close" {
		t.Fatalf("unexpected closeType: %s", closeType)
	}
	if confidence != "high" {
		t.Fatalf("unexpected confidence: %s", confidence)
	}
	if reason != "direct_payout_outputs" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestClassifyGraphCloseTransactionShapeForce(t *testing.T) {
	tx := bitcoinVerboseTransaction{
		TxID: "force-close",
		Vout: []bitcoinVerboseTxOutput{
			{
				Value: 0.87283215,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v0_keyhash"},
			},
			{
				Value: 0.00000330,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v0_keyhash"},
			},
			{
				Value: 0.00000330,
				ScriptPubKey: struct {
					Type      string   `json:"type"`
					Address   string   `json:"address"`
					Addresses []string `json:"addresses"`
				}{Type: "witness_v0_keyhash"},
			},
		},
	}

	closeType, confidence, reason := classifyGraphCloseTransactionShape(tx)
	if closeType != "force_close" {
		t.Fatalf("unexpected closeType: %s", closeType)
	}
	if confidence != "high" {
		t.Fatalf("unexpected confidence: %s", confidence)
	}
	if reason != "anchor_outputs_detected" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}
