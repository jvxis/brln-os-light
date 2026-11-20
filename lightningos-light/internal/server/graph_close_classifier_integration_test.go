package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGraphCloseClassifierPostgresRecovery(t *testing.T) {
	dsn := os.Getenv("LOS_GRAPH_CLOSE_TEST_DSN")
	if dsn == "" {
		t.Skip("requires an empty los_graph_close_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_graph_close_test_") {
		t.Fatal("dedicated graph close test database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tables int
	if err := db.QueryRow(ctx, `select count(*) from information_schema.tables where table_schema = 'public'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("fixture requires an empty database")
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate an existing graph with pending and previously terminal events.
	exec(`create table graph_close_events (
		id bigserial primary key, chan_id bigint not null, chan_point text,
		node1_pubkey text, node2_pubkey text, closed_height integer, observed_at timestamptz not null,
		close_type text, close_source text, classification_attempts integer not null default 0
	);
	insert into graph_close_events (chan_id, chan_point, closed_height, observed_at, classification_attempts)
	values (1, repeat('a', 64) || ':0', 100, now() - interval '4 hours', 0),
	       (2, repeat('b', 64) || ':0', 101, now() - interval '3 hours', 0),
	       (3, repeat('c', 64) || ':0', 102, now() - interval '2 hours', 0),
	       (4, null, 0, now() - interval '1 hour', 3)`)
	s := NewGraphExplorerService(db, nil, nil)
	for i := 0; i < 2; i++ {
		if err := s.EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
	}
	exec(`insert into graph_channels (chan_id, first_seen_at, last_seen_at) values (1, now(), now()), (2, now(), now()), (3, now(), now())`)
	load := func() []graphCloseClassificationCandidate {
		t.Helper()
		candidates, err := loadPendingGraphCloseClassificationCandidates(ctx, db, "", 32)
		if err != nil {
			t.Fatal(err)
		}
		return candidates
	}

	// A broken block response in the middle must not drop either valid event.
	var failBlock atomic.Bool
	failBlock.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Params) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch request.Method {
		case "getblockhash":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": string(request.Params[0])})
		case "getblock":
			var height string
			_ = json.Unmarshal(request.Params[0], &height)
			if height == "101" && failBlock.Load() {
				w.Header().Set("Content-Length", "100")
				_, _ = io.WriteString(w, `{"result":`)
				return
			}
			txID := map[string]string{"100": "a", "101": "b", "102": "c"}[height]
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"tx": []any{map[string]any{
				"txid": "close-" + height, "vin": []any{map[string]any{"txid": strings.Repeat(txID, 64), "vout": 0}},
				"vout": []any{map[string]any{"value": 0.01, "scriptPubKey": map[string]string{"type": "witness_v0_keyhash"}}},
			}}}})
		case "getrawtransaction":
			// The optional close fee is unavailable; classification still succeeds.
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	bitcoin := func(ctx context.Context, candidate graphCloseClassificationCandidate) (graphCloseClassificationResult, bool, string, error) {
		return classifyGraphCloseCandidateWithBitcoin(ctx, candidate, bitcoinRPCConfig{Host: srv.URL})
	}
	run := func(candidates []graphCloseClassificationCandidate, classifier graphCloseBitcoinClassifier) {
		t.Helper()
		resolved, attempts := collectGraphCloseClassifications(ctx, candidates, "", graphExplorerLocalClosedChannelLookup{}, classifier)
		if err := s.persistGraphCloseClassifications(ctx, resolved, attempts); err != nil {
			t.Fatal(err)
		}
	}
	run(load(), bitcoin)
	var classified, channels int
	if err := db.QueryRow(ctx, `select count(*) from graph_close_events where classified_at is not null and close_classifier = 'bitcoind' and close_fee_sat is null`).Scan(&classified); err != nil || classified != 2 {
		t.Fatalf("expected both healthy classifications; got %d, %v", classified, err)
	}
	if err := db.QueryRow(ctx, `select count(*) from graph_channels where classified_at is not null and close_type = 'mutual_close'`).Scan(&channels); err != nil || channels != 2 {
		t.Fatalf("expected both graph channel projections; got %d, %v", channels, err)
	}

	// An extended outage does not exhaust the three-attempt terminal limit.
	deferRetry := func() {
		t.Helper()
		if pending := load(); len(pending) != 0 {
			t.Fatalf("backoff and terminal limit must exclude pending rows: %+v", pending)
		}
		exec(`update graph_close_events set classification_retry_at = now() - interval '1 second' where chan_id = 2`)
	}
	for i := 0; i < graphCloseClassifierMaxAttempts; i++ {
		deferRetry()
		run(load(), func(context.Context, graphCloseClassificationCandidate) (graphCloseClassificationResult, bool, string, error) {
			return graphCloseClassificationResult{}, false, "", errors.New("fixture RPC offline")
		})
	}
	var retries, attempts int
	if err := db.QueryRow(ctx, `select classification_rpc_failures, classification_attempts from graph_close_events where chan_id = 2`).Scan(&retries, &attempts); err != nil || retries <= graphCloseClassifierMaxAttempts || attempts != 0 {
		t.Fatalf("RPC errors exhausted semantic attempts: retries=%d attempts=%d err=%v", retries, attempts, err)
	}
	deferRetry()
	failBlock.Store(false)
	run(load(), bitcoin)
	var recovered bool
	if err := db.QueryRow(ctx, `select classified_at is not null and classification_retry_at is null and classification_error is null from graph_close_events where chan_id = 2`).Scan(&recovered); err != nil || !recovered {
		t.Fatalf("event did not recover after the RPC came back: %v", err)
	}
}
