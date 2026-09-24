package server

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lightningos-light/internal/lndclient"
)

func autofeeExposureTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LOS_AUTOFEE_EXPOSURE_TEST_DSN")
	if dsn == "" {
		t.Skip("set LOS_AUTOFEE_EXPOSURE_TEST_DSN to a dedicated los_autofee_exposure_test_* database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_autofee_exposure_test_") {
		t.Fatal("dedicated exposure test database required")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()
	if err := (&AutofeeService{db: pool}).EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := (&Notifier{db: pool}).ensureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, autofeeExposureSchema); err != nil {
			t.Fatal(err)
		}
	}
	// The mandatory database-name guard above protects real notifications/data.
	if _, err := pool.Exec(ctx, `truncate autofee_policy_observations,autofee_policy_applications,autofee_outcomes,notifications`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestAutofeeExposurePersistenceAndExclusiveBoundaries(t *testing.T) {
	pool := autofeeExposureTestPool(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	snapshots := []autofeePolicySnapshot{exposureFixture(base), exposureFixture(base.Add(5 * time.Minute)), exposureFixture(base.Add(10 * time.Minute)), exposureFixture(base.Add(15 * time.Minute))}
	for _, snapshot := range snapshots {
		for i := 0; i < 2; i++ {
			if err := persistAutofeePolicySnapshots(ctx, pool, []autofeePolicySnapshot{snapshot}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from autofee_policy_observations`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("idempotence %d %v", count, err)
	}
	id := int64(snapshots[0].ChannelID)
	// Forwards exactly on adjacent boundaries belong to the later interval.
	for i, at := range []time.Time{base, base.Add(5 * time.Minute), base.Add(10 * time.Minute), base.Add(15 * time.Minute)} {
		_, err := pool.Exec(ctx, `insert into notifications(event_key,occurred_at,type,action,direction,status,chan_id_out,amount_out_msat,fee_msat) values($1,$2,'forward','forward','out','SETTLED',$3,100000,0)`, "zero-forward-"+at.Format(time.RFC3339), at, id)
		if err != nil {
			t.Fatal(i, err)
		}
	}
	_, err := pool.Exec(ctx, `insert into notifications(event_key,occurred_at,type,action,direction,status,chan_id_out,chan_id_in,amount_out_msat,fee_msat) values('assisted',$1,'forward','forward','out','SETTLED',2,$2,200000,1000)`, base.Add(2*time.Minute), id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `insert into notifications(event_key,occurred_at,type,action,direction,rebal_target_chan_id,status,amount_sat,fee_msat) values('purchase',$1,'rebalance','rebalance','in',$2,'SUCCEEDED',10,100)`, base.Add(time.Minute), id)
	if err != nil {
		t.Fatal(err)
	}
	application := lndclient.PolicyApplicationObservation{StartedAt: base.Add(6 * time.Minute), CompletedAt: base.Add(6*time.Minute + time.Second), Source: "manual", Acknowledged: true, Request: lndclient.UpdateChannelPolicyParams{ChannelPoint: snapshots[0].ChannelPoint, FeeRatePpm: 200}}
	for i := 0; i < 2; i++ {
		if err := persistAutofeePolicyApplication(ctx, pool, application); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `select count(*) from autofee_policy_applications`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("application idempotence %d %v", count, err)
	}
	items, err := readAutofeePolicyExposures(ctx, pool, snapshots[0].ChannelPoint, base, base.Add(15*time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want three adjacent windows, got %d", len(items))
	}
	var total int64
	for _, item := range items {
		total += item.Activity.ForwardCount
		if item.Activity.ForwardCount != 1 || item.Activity.ForwardFeeMsat != 0 || item.Activity.ForwardAmountMsat != 100000 {
			t.Fatalf("overlap/zero fee/assisted revenue: %+v", item.Activity)
		}
	}
	if total != 3 || items[1].Confidence != "unknown" || items[1].PolicyActivity != nil || len(items[1].Applications) != 1 {
		t.Fatalf("manual interval attributed: %+v", items[1])
	}
	if items[2].Activity.IncomingForwardCount != 1 || items[2].Activity.RebalanceCount != 1 || !containsTag(items[2].Flags, "rebalance_observed") {
		t.Fatalf("concurrent movement missing: %+v", items[2])
	}
	// No mutable in-memory exposure state is needed to reproduce measurements.
	reloaded, err := readAutofeePolicyExposures(ctx, pool, snapshots[0].ChannelPoint, base, base.Add(15*time.Minute), 100)
	if err != nil || !reflect.DeepEqual(items, reloaded) {
		t.Fatalf("read is not reproducible: %v", err)
	}
	page, err := readAutofeePolicyExposures(ctx, pool, snapshots[0].ChannelPoint, base, base.Add(15*time.Minute), 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("page: %v", err)
	}
	next, err := readAutofeePolicyExposures(ctx, pool, snapshots[0].ChannelPoint, base, page[0].Start, 1)
	if err != nil || len(next) != 1 || !next[0].End.Equal(page[0].Start) {
		t.Fatalf("pagination duplicated/skipped boundary: %v", err)
	}
	// The legacy 24h outcome retains its intentionally overlapping semantics.
	_, err = pool.Exec(ctx, `insert into autofee_outcomes(run_id,channel_id,channel_point,kind,decided_at,prev_ppm,new_ppm) values('legacy',$1,$2,'outbound',$3,90,100)`, id, snapshots[0].ChannelPoint, base)
	if err != nil {
		t.Fatal(err)
	}
	var outcomeID int64
	if err = pool.QueryRow(ctx, `select id from autofee_outcomes where run_id='legacy'`).Scan(&outcomeID); err != nil {
		t.Fatal(err)
	}
	if err = (&AutofeeService{db: pool}).measureOne(ctx, outcomeID, uint64(id), base); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `select fwd_count_24h_after from autofee_outcomes where id=$1`, outcomeID).Scan(&count); err != nil || count != 4 {
		t.Fatalf("legacy changed: %d %v", count, err)
	}
	// API IDs are strings, including IDs beyond JavaScript's exact integer range.
	raw, err := json.Marshal(items[0])
	if err != nil || !strings.Contains(string(raw), `"channel_id":"9007199254740993"`) {
		t.Fatal("unsafe JSON channel ID")
	}
}

func TestAutofeeExposureRestartAndDatabaseFailure(t *testing.T) {
	pool := autofeeExposureTestPool(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	before, after := exposureFixture(base), exposureFixture(base.Add(5*time.Minute))
	after.Session = "restarted"
	if err := persistAutofeePolicySnapshots(ctx, pool, []autofeePolicySnapshot{before, after}); err != nil {
		t.Fatal(err)
	}
	items, err := readAutofeePolicyExposures(ctx, pool, before.ChannelPoint, base, after.ObservedAt, 100)
	if err != nil || len(items) != 1 || items[0].Confidence != "unknown" || items[0].PolicyActivity != nil {
		t.Fatalf("restart gap invented continuity: %+v %v", items, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := persistAutofeePolicySnapshots(canceled, pool, []autofeePolicySnapshot{exposureFixture(base.Add(time.Hour))}); err == nil {
		t.Fatal("canceled write succeeded")
	}
	var count int
	if err := pool.QueryRow(ctx, `select count(*) from autofee_policy_observations`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed transaction changed ledger: %d %v", count, err)
	}
	// An out-of-order replay cannot replace an immutable observation.
	before.Fees.RatePPM = 999
	if err := persistAutofeePolicySnapshots(ctx, pool, []autofeePolicySnapshot{before}); err != nil {
		t.Fatal(err)
	}
	items, err = readAutofeePolicyExposures(ctx, pool, before.ChannelPoint, base, after.ObservedAt, 100)
	if err != nil || items[0].Before.Fees.RatePPM != 100 {
		t.Fatal("replay rewrote observation")
	}
}
