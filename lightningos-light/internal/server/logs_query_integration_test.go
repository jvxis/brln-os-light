package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lightningos-light/internal/system"
)

func TestLogQueryPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("LOS_LOGS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("requires a disposable local PostgreSQL database via LOS_LOGS_TEST_PG_DSN")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" && !strings.HasPrefix(cfg.ConnConfig.Host, "/") {
		t.Fatal("only a local disposable PostgreSQL fixture is allowed")
	}
	cfg.MaxConns = 1 // session-local TEMP table; no persistent application schema
	ctx := context.Background()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TEMP TABLE autofee_logs (id bigserial, run_id bigint, occurred_at timestamptz, seq int, line text);
INSERT INTO autofee_logs(run_id,occurred_at,seq,line)
SELECT i, '2026-10-08 00:00:00+00'::timestamptz + i * interval '1 second', 1,
CASE WHEN i IN (100,300) THEN '[ERR] fixture-query error ' || i ELSE '[INF] ordinary ' || i END
FROM generate_series(1,1500) AS i;
INSERT INTO autofee_logs(run_id,occurred_at,seq,line) VALUES
(300, '2026-10-08 00:05:00+00', 2, 'seed algorithm preserved');`)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{db: db}
	q := logQuery{Service: "autofee", Limit: 1, Until: time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC), Level: "error", Text: "fixture-query"}
	result, err := s.queryLogs(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 2 || len(result.Entries) != 1 || !result.ResultLimited || result.Partial || !strings.Contains(result.Entries[0].Message, "error 300 | seed algorithm preserved") {
		t.Fatalf("database filter before output limit/group preservation: %+v", result)
	}
	q.Since, q.Until = time.Date(2026, 10, 8, 0, 1, 40, 0, time.UTC), time.Date(2026, 10, 8, 0, 1, 40, 0, time.UTC)
	result, err = s.queryLogs(ctx, q)
	if err != nil || len(result.Entries) != 1 || !strings.Contains(result.Entries[0].Message, "error 100") {
		t.Fatalf("inclusive database bounds: %+v %v", result, err)
	}
	legacy, err := s.readAutofeeLogLines(ctx, 200)
	// Legacy order is max(id), so the later inserted seed makes run 300 first.
	if err != nil || len(legacy) != 200 || !strings.Contains(legacy[0], "error 300") || !strings.Contains(legacy[1], "ordinary 1500") {
		t.Fatalf("legacy Autofee tail changed: %d %v", len(legacy), err)
	}
}

type logQueryFixtureBroker struct {
	cpuMinerPrivilegedClient
	lines int
	since string
}

func (b *logQueryFixtureBroker) AppLogs(_ context.Context, id string, lines int, since string) ([]string, string, error) {
	b.lines, b.since = lines, since
	return []string{"[INF] fixture app log", "[ERR] connection refused token=fixture-secret"}, "docker:" + id, nil
}

func TestLogQueryAppAdapterIntegration(t *testing.T) {
	if os.Getenv("LOS_LOGS_TEST_JOURNAL") != "disposable" {
		t.Skip("requires a disposable Linux VM for fixed app paths")
	}
	// Do not replace installed app declarations. Only these new placeholder files
	// and their empty directories are removed by cleanup, never an app data tree.
	paths := []string{bitcoinCoreAppPaths().ComposePath, fedimintGuardianAppPaths().ComposePath, fedimintGatewayAppPaths().ComposePath}
	for _, path := range paths {
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatalf("fixture app directory already exists: %s", filepath.Dir(path))
		}
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# synthetic log adapter fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(path); _ = os.Remove(filepath.Dir(path)) })
	}
	broker := &logQueryFixtureBroker{cpuMinerPrivilegedClient: cpuMinerPrivilegedClient{mode: "enforce"}}
	system.ConfigurePrivilegedClient(broker)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
	s := &Server{}
	for _, service := range []string{"bitcoin", "fedimint-guardian", "fedimint-gateway"} {
		q := logQuery{Service: service, Limit: 1000, Until: time.Now()}
		result, err := s.queryLogs(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(result)
		if broker.lines != 500 || broker.since != "" || !result.Partial || result.Reason != "source_tail" || result.Capabilities.Period || result.Capabilities.Filters || len(result.Entries) != 2 || strings.Contains(string(encoded), "fixture-secret") {
			t.Fatalf("app adapter: %+v", result)
		}
		q.Text = "fixture"
		if _, err := s.queryLogs(context.Background(), q); err != errLogQueryUnsupported {
			t.Fatalf("incomplete tail search accepted: %v", err)
		}
		r := httptest.NewRecorder()
		s.handleLogs(r, httptest.NewRequest("GET", "/api/logs?service="+service+"&lines=200&since=1h", nil))
		if r.Code != 200 || broker.lines != 200 || broker.since != "1h" {
			t.Fatalf("legacy app log contract changed: %d", r.Code)
		}
	}
}

// scripts/test-log-query-linux.sh provisions synthetic units only after checking
// that the host is disposable and no real LND/LOS/Bitcoin unit exists.
func TestLogQueryJournalIntegration(t *testing.T) {
	if os.Getenv("LOS_LOGS_TEST_JOURNAL") != "disposable" {
		t.Skip("requires synthetic journal fixtures on a disposable Linux VM")
	}
	s := &Server{}
	q := logQuery{Service: "lnd", Until: time.Now().UTC(), Limit: 1, Level: "error", Event: "connection", Text: "los-query-test"}
	result, err := s.queryLogs(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 2 || len(result.Entries) != 1 || !result.ResultLimited || result.Partial || !strings.Contains(result.Entries[0].Message, "fixture-300") {
		t.Fatalf("journal query: %+v", result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "fixture-secret") || len(result.Entries[0].Context) != 7 {
		t.Fatalf("context not sanitized/preserved: %s", encoded)
	}
	stamp := result.Entries[0].Time
	r := httptest.NewRecorder()
	s.handleLogsQuery(r, httptest.NewRequest("GET", "/api/logs/query?service=lnd&since="+url.QueryEscape(stamp)+"&until="+url.QueryEscape(stamp)+"&q=los-query-test", nil))
	if r.Code != 200 {
		t.Fatalf("real journal exact timestamp query: %d %s", r.Code, r.Body.String())
	}
	var bounded logQueryResponse
	if err := json.Unmarshal(r.Body.Bytes(), &bounded); err != nil {
		t.Fatal(err)
	}
	if len(bounded.Entries) != 1 || bounded.Entries[0].Time != stamp {
		t.Fatalf("inclusive journal timestamp: %+v", bounded)
	}
	for _, service := range []string{"lnd", "lnd-upgrade", "app-upgrade", "tor-upgrade"} {
		r := httptest.NewRecorder()
		s.handleLogs(r, httptest.NewRequest("GET", "/api/logs?service="+service+"&lines=2&since="+url.QueryEscape(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)), nil))
		if r.Code != 200 {
			t.Fatalf("legacy %s: %d %s", service, r.Code, r.Body.String())
		}
		var tail struct {
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &tail); err != nil {
			t.Fatal(err)
		}
		if len(tail.Lines) != 2 {
			t.Fatalf("legacy %s tail: %+v", service, tail)
		}
	}
}
