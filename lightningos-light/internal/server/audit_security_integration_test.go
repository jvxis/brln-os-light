package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func auditSecurityPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LOS_AUDIT_TEST_DSN")
	if dsn == "" {
		t.Skip("requires dedicated los_audit_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_audit_test_") {
		t.Fatal("dedicated audit test database required")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), `drop table if exists audit_events`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestAuditSecurityHistoricalMigrationAndAPI(t *testing.T) {
	p := auditSecurityPool(t)
	ctx := context.Background()
	svc := NewAuditService(p, nil)
	// Historical schema, before any security migration runs.
	_, err := p.Exec(ctx, `create table audit_events(id bigserial primary key,ts timestamptz not null default now(),session_id text not null default '',action text not null,target text not null default '',metadata jsonb not null default '{}',ip text not null default '');
 insert into audit_events(session_id,action,target,metadata,ts) select 'historical-cookie-secret','wallet.test','original-target','{"amount_sat":123}', '2026-01-01'::timestamptz from generate_series(1,505);
 insert into audit_events(session_id,action) values ('anonymous','anonymous.test'),('','empty.test');`)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = svc.EnsureSchema(ctx); err != nil {
		t.Fatal("repeat migration", err)
	}
	var count int
	var reference string
	if err = p.QueryRow(ctx, `select count(*),min(session_id) from audit_events where action='wallet.test' and target='original-target' and metadata->>'amount_sat'='123' and ts='2026-01-01'::timestamptz`).Scan(&count, &reference); err != nil || count != 505 {
		t.Fatal("historical event data changed", err, count)
	}
	if reference == "historical-cookie-secret" || !strings.HasPrefix(reference, "audit:v1:") {
		t.Fatal("raw historical session persisted")
	}
	if err = svc.Insert(ctx, AuditEventInsert{SessionID: "historical-cookie-secret", Action: "wallet.test", Target: "new-target"}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.List(ctx, AuditEventFilter{SessionID: reference, Limit: 500})
	if err != nil || len(items) != 500 {
		t.Fatal("session filter/limit regression", err, len(items))
	}
	for _, event := range items {
		if event.SessionID != reference {
			t.Fatal("session correlation changed")
		}
	}
	// Even a legacy writer after migration must never leak raw IDs via the API.
	if _, err = p.Exec(ctx, `insert into audit_events(session_id,action) values ('late-cookie-secret','late.test')`); err != nil {
		t.Fatal(err)
	}
	s := &Server{auditLog: svc}
	rec := httptest.NewRecorder()
	s.handleAuditEventsList(rec, httptest.NewRequest("GET", "/api/audit/events?limit=500", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "cookie-secret") {
		t.Fatal("audit API exposed credentials or failed")
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("sensitive audit response is cacheable")
	}
	var response struct {
		Items []AuditEvent `json:"items"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 500 || !strings.HasPrefix(response.Items[0].SessionID, "audit:v1:") {
		t.Fatal("API order/limit/correlation regression")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if svc.EnsureSchema(cancelled) == nil {
		t.Fatal("cancelled migration unexpectedly succeeded")
	}
	if err = svc.EnsureSchema(ctx); err != nil {
		t.Fatal("retry after cancellation", err)
	}
	if n, err := svc.PruneBefore(ctx, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil || n != 505 {
		t.Fatal("retention regression", err, n)
	}
}
