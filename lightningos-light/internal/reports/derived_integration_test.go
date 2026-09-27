package reports

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Opt-in tests use an isolated schema in a LOCAL disposable PostgreSQL only.
// Example: REPORTS_TEST_PG_DSN=postgres://postgres@127.0.0.1:55496/postgres?sslmode=disable
func derivedTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("REPORTS_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set REPORTS_TEST_PG_DSN to a disposable local PostgreSQL")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" {
		t.Fatal("integration tests require a local disposable database")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("los_reports_test_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "create schema "+ident); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, err := admin.Exec(ctx, "drop schema "+ident+" cascade")
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	if err = EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertLegacyDaily(t *testing.T, db *pgxpool.Pool, day time.Time) {
	t.Helper()
	balance := int64(123456)
	row := Row{ReportDate: day, Metrics: Metrics{
		ForwardFeeRevenueSat: 100, ForwardFeeRevenueMsat: 100123,
		RebalanceFeeCostSat: 20, RebalanceFeeCostMsat: 20009,
		PaymentFeeCostSat: 3, PaymentFeeCostMsat: 3003,
		OnchainFeeCostSat: 4, OnchainFeeCostMsat: 4000,
		KeysendReceivedSat: 6, KeysendReceivedMsat: 6007,
		KeysendSentSat: 7, KeysendSentMsat: 7008, KeysendSentCount: 1,
		SalesRevenueSat: 5, SalesRevenueMsat: 5000, SalesCount: 1,
		NetRoutingProfitSat: 999, NetRoutingProfitMsat: 999000,
		NetWithKeysendSat: 999, NetWithKeysendMsat: 999000, NetTotalSat: 999, NetTotalMsat: 999000,
		OnchainBalanceSat: &balance, LightningBalanceSat: &balance, TotalBalanceSat: &balance}}
	q, args := buildUpsertDaily(row)
	if _, err := db.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func rowJSON(t *testing.T, db *pgxpool.Pool, sourceOnly bool) string {
	t.Helper()
	expression := "to_jsonb(r)"
	if sourceOnly {
		expression += " - array['net_routing_profit_sats','net_routing_profit_msat','net_with_keysend_sats','net_with_keysend_msat','net_total_sats','net_total_msat','updated_at']"
	}
	var result string
	if err := db.QueryRow(context.Background(), "select "+expression+" from reports_daily r order by report_date limit 1").Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDerivedPostgresRepairAndHistoricalReads(t *testing.T) {
	db := derivedTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	loc := time.FixedZone("testUTC-3", -10800)
	insertLegacyDaily(t, db, day)
	original, source := rowJSON(t, db, false), rowJSON(t, db, true)
	changes, err := RepairDerived(ctx, db, day, day, loc, true)
	if err != nil || len(changes) != 1 {
		t.Fatalf("dry-run %v %v", changes, err)
	}
	if rowJSON(t, db, false) != original {
		t.Fatal("dry-run wrote data")
	}
	svc := NewService(db, nil, nil)
	rows, err := svc.CustomRange(ctx, day, day, loc)
	if err != nil || len(rows) != 1 || rows[0].Metrics.NetTotalMsat != 77110 {
		t.Fatalf("read %v %v", rows, err)
	}
	if rowJSON(t, db, false) != original {
		t.Fatal("GET-style read wrote data")
	}
	changes, err = RepairDerived(ctx, db, day, day, loc, false)
	if err != nil || len(changes) != 1 {
		t.Fatalf("apply %v %v", changes, err)
	}
	if rowJSON(t, db, true) != source {
		t.Fatal("repair changed source/balances")
	}
	changes, err = RepairDerived(ctx, db, day, day, loc, false)
	if err != nil || len(changes) != 0 {
		t.Fatalf("not idempotent %v %v", changes, err)
	}
	summary, err := svc.CustomSummary(ctx, day, day, loc)
	if err != nil || summary.Totals.NetTotalMsat != rows[0].Metrics.NetTotalMsat ||
		summary.Averages.KeysendSentMsat != 7008 {
		t.Fatalf("summary %v %v", summary, err)
	}
	for _, key := range []string{RangeD1, RangeMonth, RangeAll} {
		now := time.Date(2026, 9, 27, 12, 0, 0, 0, loc)
		series, _, err := svc.Range(ctx, key, now, loc)
		if err != nil || len(series) != 1 {
			t.Fatalf("range %s: %v", key, err)
		}
		aggregate, _, err := svc.Summary(ctx, key, now, loc)
		if err != nil || aggregate.Totals.NetTotalMsat != series[0].Metrics.NetTotalMsat {
			t.Fatalf("range/summary mismatch %s: %v", key, err)
		}
	}
	// Verify raw legacy msat zero is repaired, not hidden by read normalization.
	if _, err = db.Exec(ctx, "update reports_daily set net_total_msat=0"); err != nil {
		t.Fatal(err)
	}
	changes, err = RepairDerived(ctx, db, day, day, loc, false)
	if err != nil || len(changes) != 1 {
		t.Fatalf("zero-msat repair %v %v", changes, err)
	}
}

func TestDerivedPostgresMarksEditMoveRemoveAndCache(t *testing.T) {
	db := derivedTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	next := day.AddDate(0, 0, 1)
	loc := time.FixedZone("testUTC-3", -10800)
	insertLegacyDaily(t, db, day)
	insertLegacyDaily(t, db, next)
	stamp := time.Date(2026, 9, 26, 23, 30, 0, 0, loc) // next UTC day, same civil day
	svc := NewService(db, nil, nil)
	assertTotal := func(date time.Time, want int64) {
		t.Helper()
		rows, err := svc.CustomRange(ctx, date, date, loc)
		if err != nil || len(rows) != 1 || rows[0].Metrics.NetTotalMsat != want {
			t.Fatalf("read total %v %v want %d", rows, err, want)
		}
		var stored int64
		if err = db.QueryRow(ctx, "select net_total_msat from reports_daily where report_date=$1", normalizeReportDate(date)).Scan(&stored); err != nil || stored != want {
			t.Fatalf("stored %d %v", stored, err)
		}
	}
	if err := SetActivityMark(ctx, db, "synthetic-mark", MarkRevenue, 10000, stamp, loc); err != nil {
		t.Fatal(err)
	}
	assertTotal(day, 87110)
	if err := SetActivityMark(ctx, db, "synthetic-mark", MarkCost, 20000, stamp, loc); err != nil {
		t.Fatal(err)
	}
	assertTotal(day, 57110)
	snapshot := liveSnapshot{Range: reportDayRange(day, loc), Metrics: Metrics{ForwardFeeRevenueMsat: 100000, MarkedRevenueMsat: 999000}}
	_, live, err := svc.liveWithCurrentMarks(ctx, snapshot)
	if err != nil || live.NetTotalMsat != 80000 || live.MarkedRevenueMsat != 0 {
		t.Fatalf("live %v %v", live, err)
	}
	expired, cancel := context.WithCancel(ctx)
	cancel()
	_, live, err = svc.liveWithCurrentMarks(expired, snapshot)
	if err != nil || live.NetTotalMsat != 80000 {
		t.Fatalf("timeout fallback lost: %v", err)
	}
	if err := SetActivityMark(ctx, db, "synthetic-mark", MarkRevenue, 30000, stamp.AddDate(0, 0, 1), loc); err != nil {
		t.Fatal(err)
	}
	assertTotal(day, 77110)
	assertTotal(next, 107110)
	if err := SetActivityMark(ctx, db, "synthetic-mark", "", 0, time.Time{}, loc); err != nil {
		t.Fatal(err)
	}
	assertTotal(next, 77110)
	if err := SetActivityMark(ctx, db, "missing-day", MarkRevenue, 1, stamp.AddDate(0, 0, 10), loc); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, "select count(*) from reports_daily").Scan(&count); err != nil || count != 2 {
		t.Fatal("mark fabricated a daily row")
	}
}

func TestDerivedPostgresConcurrentDailyAndMark(t *testing.T) {
	db := derivedTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	day := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	insertLegacyDaily(t, db, day)
	// Simulate LND collection finished before the mark was edited.
	stale := Row{ReportDate: day, Metrics: Metrics{ForwardFeeRevenueMsat: 100000, MarkedRevenueMsat: 999999}}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if err = lockDerivedWrites(ctx, tx); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() {
		results <- SetActivityMark(ctx, db, "concurrent", MarkRevenue, 50000, day.Add(time.Hour), time.UTC)
	}()
	go func() { _, err := upsertDailyWithMarks(ctx, db, stale, time.UTC); results <- err }()
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var total int64
	if err = db.QueryRow(ctx, "select net_total_msat from reports_daily").Scan(&total); err != nil || total != 150000 {
		t.Fatalf("lost concurrent mark: %d %v", total, err)
	}
}

func TestDerivedPostgresErrorsAndDryRunNoSchema(t *testing.T) {
	db := derivedTestDB(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	insertLegacyDaily(t, db, day)
	before := rowJSON(t, db, false)
	// Trigger proves an apply failure rolls back; dry-run must never fire it.
	_, err := db.Exec(ctx, `create function refuse_report_update() returns trigger language plpgsql as
 $$ begin raise exception 'synthetic update failure'; end $$;
 create trigger refuse_update before update on reports_daily for each row execute function refuse_report_update();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RepairDerived(ctx, db, day, day, time.UTC, true); err != nil {
		t.Fatal(err)
	}
	if _, err = RepairDerived(ctx, db, day, day, time.UTC, false); err == nil {
		t.Fatal("expected failure")
	}
	if rowJSON(t, db, false) != before {
		t.Fatal("failed apply changed row")
	}
	if err = SetActivityMark(ctx, db, "rollback", MarkRevenue, 123, day.Add(time.Hour), time.UTC); err == nil {
		t.Fatal("expected mark refresh failure")
	}
	var count int
	if err = db.QueryRow(ctx, "select count(*) from report_activity_marks").Scan(&count); err != nil || count != 0 {
		t.Fatal("mark wasn't rolled back")
	}
	if _, err = db.Exec(ctx, "alter table report_activity_marks rename column amount_msat to unavailable"); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, nil, nil)
	if _, err = svc.CustomRange(ctx, day, day, time.UTC); err == nil {
		t.Fatal("mark query failure hidden")
	}
	if _, err = FetchActivityMarkTotals(ctx, db, day, day.AddDate(0, 0, 1)); err == nil {
		t.Fatal("live mark query failure hidden")
	}
	if _, err = db.Exec(ctx, "drop table reports_daily"); err != nil {
		t.Fatal(err)
	}
	if _, err = RepairDerived(ctx, db, day, day, time.UTC, true); err == nil {
		t.Fatal("missing schema should fail")
	}
	var table *string
	if err = db.QueryRow(ctx, "select to_regclass('reports_daily')::text").Scan(&table); err != nil || table != nil {
		t.Fatal("dry-run created schema")
	}
}
