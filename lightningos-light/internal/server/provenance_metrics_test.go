package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lightningos-light/internal/electrs"
)

type provenanceMetricsFakeSource struct {
	name  string
	err   error
	tx    electrs.VerboseTx
	calls int
}

func (s *provenanceMetricsFakeSource) Name() string { return s.name }

func (s *provenanceMetricsFakeSource) Available(context.Context) bool { return s.err == nil }

func (s *provenanceMetricsFakeSource) GetTransaction(context.Context, string) (electrs.VerboseTx, error) {
	s.calls++
	return s.tx, s.err
}

func TestProvenanceMetricsWrappedSourceRecordsFallthroughAndHit(t *testing.T) {
	metrics := NewProvenanceMetrics()
	bitcoind := &provenanceMetricsFakeSource{name: "bitcoind", err: electrs.ErrSourceUnavailable}
	localElectrs := &provenanceMetricsFakeSource{name: "local electrs", tx: electrs.VerboseTx{Txid: "abc"}}

	chain := electrs.NewChainedSource(wrapProvenanceSourcesWithMetrics([]electrs.TxSource{bitcoind, localElectrs}, metrics))
	if _, err := chain.GetTransaction(context.Background(), "abc"); err != nil {
		t.Fatalf("expected fallback source to answer, got %v", err)
	}

	snapshot := metrics.Snapshot()
	bySource := provenanceMetricsBySource(snapshot)
	if snapshot.FallthroughsTotal != 1 {
		t.Fatalf("expected one fallthrough, got %d", snapshot.FallthroughsTotal)
	}
	if got := bySource["bitcoind"]; got.CallsTotal != 1 || got.HitsTotal != 0 || got.ErrorsTotal != 1 || got.FallthroughsTotal != 1 {
		t.Fatalf("unexpected bitcoind metrics: %+v", got)
	}
	if got := bySource["electrs"]; got.CallsTotal != 1 || got.HitsTotal != 1 || got.ErrorsTotal != 0 {
		t.Fatalf("unexpected electrs metrics: %+v", got)
	}
	if bitcoind.calls != 1 || localElectrs.calls != 1 {
		t.Fatalf("expected both sources to be called once, got bitcoind=%d electrs=%d", bitcoind.calls, localElectrs.calls)
	}
}

func TestProvenanceMetricsAggregatesPublicLabels(t *testing.T) {
	metrics := NewProvenanceMetrics()
	metrics.RecordSourceCall("public:one.example:50002", time.Millisecond, nil, false)
	metrics.RecordSourceCall("public:two.example:50001", 2*time.Millisecond, errors.New("dial failed"), false)

	got := provenanceMetricsBySource(metrics.Snapshot())["public"]
	if got.CallsTotal != 2 || got.HitsTotal != 1 || got.ErrorsTotal != 1 {
		t.Fatalf("unexpected public metrics: %+v", got)
	}
	if len(got.Labels) != 2 {
		t.Fatalf("expected two public labels, got %+v", got.Labels)
	}
}

func TestHandleProvenanceMetrics(t *testing.T) {
	metrics := NewProvenanceMetrics()
	metrics.RecordSourceCall("bitcoind", time.Millisecond, nil, false)
	s := &Server{provenanceMetrics: metrics}

	rr := httptest.NewRecorder()
	s.handleProvenanceMetrics(rr, httptest.NewRequest(http.MethodGet, "/api/onchain/provenance/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var snapshot ProvenanceMetricsSnapshot
	if err := json.NewDecoder(rr.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := provenanceMetricsBySource(snapshot)["bitcoind"]; got.HitsTotal != 1 {
		t.Fatalf("expected bitcoind hit in response, got %+v", got)
	}
}

func provenanceMetricsBySource(snapshot ProvenanceMetricsSnapshot) map[string]ProvenanceSourceMetricsSnapshot {
	out := make(map[string]ProvenanceSourceMetricsSnapshot, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		out[source.Source] = source
	}
	return out
}
