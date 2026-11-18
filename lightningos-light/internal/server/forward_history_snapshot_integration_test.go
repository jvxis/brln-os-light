package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestForwardHistoryOptionalSnapshotPostgres(t *testing.T) {
	dsn := os.Getenv("LOS_FORWARD_HISTORY_TEST_DSN")
	if dsn == "" {
		t.Skip("requires an empty los_forward_history_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_forward_history_test_") {
		t.Fatal("dedicated forward history database required")
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
	n := &Notifier{db: db}
	if err := n.ensureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := db.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`insert into notifications
		(event_key, occurred_at, type, action, direction, status, amount_sat, fee_sat, fee_msat, chan_id_in, chan_id_out)
		values ('legacy', now() - interval '2 hours', 'forward', 'forward', 'out', 'settled', 1200, 2, 2000, 101, 201),
		       ('recent', now() - interval '1 hour', 'forward', 'forward', 'out', 'settled', 1000, 3, 3000, 102, 201)`)
	exec(`drop table forward_policy_snapshot`)

	s := &Server{db: db, logger: log.New(io.Discard, "", 0)}
	read := func() forwardHistoryResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/lnops/forward-history?chan_in=102", nil)
		s.handleLNForwardHistoryGet(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("history must remain available: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp forwardHistoryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Totals.Count != 2 || resp.Totals.AmountSat != 2200 || resp.Totals.FeeSat != 5 ||
			resp.Filtered.Count != 1 || resp.Filtered.FeeSat != 3 || len(resp.Items) != 1 || resp.Items[0].ChanIDIn != 102 {
			t.Fatalf("existing history or channel filtering changed: %+v", resp)
		}
		return resp
	}
	missing := read()
	if missing.Items[0].InboundDiscountMsat != nil || missing.Filtered.InboundDiscountSat != 0 {
		t.Fatalf("unavailable snapshot must not fabricate a discount: %+v", missing)
	}

	n.ensureForwardPolicySnapshotSchema(ctx)
	exec(`insert into forward_policy_snapshot (event_key, occurred_at, fee_policy_out_msat, inbound_fee_msat, inbound_discount_msat)
		values ('recent', now() - interval '1 hour', 4600, -1600, 1600)`)
	available := read()
	if got := available.Items[0].InboundDiscountMsat; got == nil || *got != 1600 ||
		available.Totals.InboundDiscountSat != 1 || available.Filtered.InboundDiscountSat != 1 {
		t.Fatalf("snapshot did not enrich the existing history: %+v", available)
	}
}
