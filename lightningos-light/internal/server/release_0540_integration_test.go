package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Run only against a newly created, dedicated database, never a node database.
// This fixture has no LND client and cannot execute a payment.
func TestRelease0540PostgresCompatibility(t *testing.T) {
	dsn := os.Getenv("LOS_RELEASE_TEST_DSN")
	if dsn == "" {
		t.Skip("set LOS_RELEASE_TEST_DSN to an empty los_release_test_* database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_release_test_") {
		t.Fatal("dedicated release database required")
	}
	ctx := context.Background()
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tables int
	if err := db.QueryRow(ctx, `select count(*) from information_schema.tables where table_schema='public'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("fixture requires an empty database")
	}
	rb := NewRebalanceService(db, nil, nil)
	af := &AutofeeService{db: db}
	for i := 0; i < 2; i++ {
		if err := rb.ensureSchema(ctx); err != nil {
			t.Fatal(err)
		}
		if err := af.EnsureSchema(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rc := defaultRebalanceConfig()
	if err := rb.upsertConfig(ctx, rc); err != nil {
		t.Fatal(err)
	}
	got, err := rb.loadConfig(ctx)
	if err != nil || got.StockGateEnabled || got.DiscoverySteps != 0 {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	rc.StockGateEnabled, rc.StockGateMinStockPct, rc.StockGateCoverDays = true, 12, 4
	rc.DiscoverySteps, rc.DiscoveryCeilingPct, rc.DiscoveryDailyBudgetSat = 2, 150, 300
	if err := rb.upsertConfig(ctx, rc); err != nil {
		t.Fatal(err)
	}
	got, err = rb.loadConfig(ctx)
	if err != nil || !got.StockGateEnabled || got.StockGateMinStockPct != 12 || got.StockGateCoverDays != 4 || got.DiscoverySteps != 2 || got.DiscoveryCeilingPct != 150 || got.DiscoveryDailyBudgetSat != 300 {
		t.Fatalf("configuration roundtrip: %+v %v", got, err)
	}
	if err := af.SetChannelEnabled(ctx, 101, "fixture:0", false); err != nil {
		t.Fatal(err)
	}
	if err := af.SetChannelMinPpm(ctx, 101, "", 1200); err != nil {
		t.Fatal(err)
	}
	settings, err := af.LoadChannelSettingsDetailed(ctx)
	if err != nil || len(settings) != 1 || settings[0].Enabled || settings[0].MinPpm != 1200 || settings[0].ChannelPoint != "fixture:0" {
		t.Fatalf("floor must preserve disabled state and point: %+v %v", settings, err)
	}
	if floor := af.loadChannelMinPpm(ctx)[101]; floor != 1200 {
		t.Fatalf("loaded floor: %d", floor)
	}
	if err := af.SetChannelMinPpm(ctx, 101, "", 0); err != nil {
		t.Fatal(err)
	}
	if floor := af.loadChannelMinPpm(ctx)[101]; floor != 0 {
		t.Fatalf("cleared floor: %d", floor)
	}

	now := time.Now()
	insertJob := func(reason, status string, at time.Time) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRow(ctx, `insert into rebalance_jobs(created_at,completed_at,source,status,trigger_reason,reason,target_channel_id,target_channel_point,target_outbound_pct,target_amount_sat) values($1,$1,'auto',$2,$3,'all sources failed',101,'fixture:0',50,100000) returning id`, at, status, reason).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insertAttempt := func(id int64, status string, at time.Time, fee int64) {
		t.Helper()
		if _, err := db.Exec(ctx, `insert into rebalance_attempts(job_id,attempt_index,source_channel_id,amount_sat,fee_limit_ppm,fee_paid_sat,status,started_at,finished_at) values($1,1,201,100000,1500,$2,$3,$4,$4)`, id, fee, status, at); err != nil {
			t.Fatal(err)
		}
	}
	failedAt := now.Add(-time.Minute)
	failed := insertJob(rebalanceSovereignReason, "failed", failedAt)
	for i := 0; i < sovereignTargetStructuralCooldownMinAttempts; i++ {
		insertAttempt(failed, "failed", failedAt, 0)
	}
	before := rb.loadSovereignTargetStructuralCooldowns(ctx, []uint64{101}, rc, now)
	if len(before) != 1 {
		t.Fatalf("normal structural failure must remain visible: %+v", before)
	}
	discovery := insertJob(sovereignDiscoveryReason, "succeeded", now)
	insertAttempt(discovery, "succeeded", now, 100)
	insertJob(sovereignDiscoveryReason, "queued", now)
	state := rb.loadSovereignDiscoveryState(ctx, now)
	if state.Unavailable || state.InFlight != 1 || state.SpentTodaySat != 100 || state.ByTarget[101].Jobs != 2 || !state.ByTarget[101].Succeeded {
		t.Fatalf("discovery state: %+v", state)
	}
	after := rb.loadSovereignTargetStructuralCooldowns(ctx, []uint64{101}, rc, now)
	if len(after) != 1 || after[101].Failures != before[101].Failures {
		t.Fatalf("discovery success cleared normal cooldown: %+v", after)
	}
	sources := rb.loadSourceRouteabilityCooldowns(ctx, now.Add(-time.Hour))
	if sources[201].Failures != sovereignTargetStructuralCooldownMinAttempts || sources[201].Successes != 0 {
		t.Fatalf("discovery must not reset source failure evidence: %+v", sources)
	}
}
