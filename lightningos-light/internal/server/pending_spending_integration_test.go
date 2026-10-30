package server

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"strings"
	"testing"
	"time"
)

func pendingSpendingPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LOS_SPENDING_GUARD_TEST_DSN")
	if dsn == "" {
		t.Skip("requires dedicated los_spending_guard_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_spending_guard_test_") {
		t.Fatal("dedicated test database required")
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	svc := NewSpendingGuardService(p, nil)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(context.Background(), `truncate spending_guard_entries;update spending_guard_settings set enabled=true,max_payment_sat=100,rolling_24h_limit_sat=100 where id=1`); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPendingSpendingSurvivesWindowAndRestart(t *testing.T) {
	pool := pendingSpendingPool(t)
	ctx := context.Background()
	svc := NewSpendingGuardService(pool, nil)
	r, err := svc.Reserve(ctx, SpendingIntent{AmountSat: 80, MaxFeeSat: 20, PaymentHash: strings.Repeat("aa", 32)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update spending_guard_entries set created_at=now()-interval '49 hours' where id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	restarted := NewSpendingGuardService(pool, nil)
	if _, err := restarted.Reserve(ctx, SpendingIntent{AmountSat: 1}); !errors.Is(err, ErrSpendingGuardLimit) {
		t.Fatalf("old unresolved exposure forgotten: %v", err)
	}
	st, err := restarted.Status(ctx)
	if err != nil || st.ReservedSat != 100 || st.RemainingSat != 0 {
		t.Fatalf("wrong exposure: %+v %v", st, err)
	}
	if !st.WindowEndsHint.IsZero() {
		t.Fatal("unresolved payment advertised an automatic release time")
	}
	if err := restarted.Settle(ctx, r, 5, r.ID); err != nil {
		t.Fatal(err)
	}
	st, err = restarted.Status(ctx)
	if err != nil || st.UsedSat != 85 || st.ReservedSat != 0 || st.RemainingSat != 15 {
		t.Fatalf("settlement exposure: %+v %v", st, err)
	}
	if st.WindowEndsHint.Before(time.Now().Add(23 * time.Hour)) {
		t.Fatal("settlement window used old reservation timestamp")
	}
	if err := restarted.Settle(ctx, r, 5, r.ID); err == nil {
		t.Fatal("settlement repeated")
	}
	if err := restarted.Release(ctx, r, "duplicate failure"); err != nil {
		t.Fatal(err)
	}
	st, _ = restarted.Status(ctx)
	if st.UsedSat != 85 {
		t.Fatal("late failure released settled debit")
	}
	if _, err := pool.Exec(ctx, `update spending_guard_entries set settled_at=now()-interval '25 hours' where id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := restarted.Reserve(ctx, SpendingIntent{AmountSat: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Release(ctx, fresh, "definitive failure"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Release(ctx, fresh, "repeat"); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Reserve(ctx, SpendingIntent{AmountSat: 100}); err != nil {
		t.Fatal("confirmed release did not restore allowance", err)
	}
}

func TestPendingSpendingCannotBindInactiveReservation(t *testing.T) {
	pool := pendingSpendingPool(t)
	ctx := context.Background()
	guard := NewSpendingGuardService(pool, nil)
	reservation, err := guard.Reserve(ctx, SpendingIntent{AmountSat: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(ctx, reservation, "confirmed pre-submission failure"); err != nil {
		t.Fatal(err)
	}
	if err := guard.Bind(ctx, reservation, strings.Repeat("ab", 32)); err == nil {
		t.Fatal("inactive reservation reported durable hash binding")
	}
}
