package server

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func spendingPolicyTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LOS_SPENDING_GUARD_TEST_DSN")
	if dsn == "" {
		t.Skip("requires a dedicated los_spending_guard_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_spending_guard_test_") {
		t.Fatal("dedicated spending guard test database required")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	svc := NewSpendingGuardService(pool, nil)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `truncate spending_guard_entries; update spending_guard_settings set enabled=true,max_payment_sat=100,rolling_24h_limit_sat=200 where id=1`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestSpendingPolicyAPIRequiresIndependentApproval(t *testing.T) {
	pool := spendingPolicyTestPool(t)
	svc := NewSpendingGuardService(pool, nil)
	now := time.Now()
	auth := &AuthService{enabled: true, now: time.Now, sessions: map[string]*authSession{"test": {ID: "test", ExpiresAt: now.Add(time.Hour), ReauthScopes: map[string]time.Time{authScopeLightningFunds: now.Add(time.Minute)}}}}
	s := &Server{auth: auth, spendingGuard: svc, auditLogInitAt: now}
	for i := 0; i < 2; i++ {
		if _, err := svc.Reserve(context.Background(), SpendingIntent{AmountSat: 100}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Reserve(context.Background(), SpendingIntent{AmountSat: 1}); !errors.Is(err, ErrSpendingGuardLimit) {
		t.Fatalf("rolling limit did not block before update: %v", err)
	}
	call := func(body string, intentIDs ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/wallet/spending-guard", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), authSessionContextKey, authSessionSnapshot{ID: "test"}))
		if len(intentIDs) > 0 {
			req.Header.Set(financialIntentHeader, intentIDs[0])
		}
		rec := httptest.NewRecorder()
		s.financialIntentMiddleware(http.HandlerFunc(s.handleSpendingGuardUpdate)).ServeHTTP(rec, req)
		return rec
	}
	for _, body := range []string{`{"enabled":true,"max_payment_sat":0,"rolling_24h_limit_sat":200}`, `{"enabled":true,"max_payment_sat":100,"rolling_24h_limit_sat":0}`, `{"enabled":false,"max_payment_sat":100,"rolling_24h_limit_sat":200}`} {
		rec := call(body)
		if rec.Code != 428 || !strings.Contains(rec.Body.String(), "spending_guard_reauth_required") {
			t.Fatalf("expected policy challenge, got %d %s", rec.Code, rec.Body.String())
		}
	}
	settings, err := svc.Settings(context.Background())
	if err != nil || settings.MaxPaymentSat != 100 || settings.Rolling24hLimitSat != 200 || !settings.Enabled {
		t.Fatalf("unauthorized policy changed: %+v %v", settings, err)
	}
	if _, err := svc.Reserve(context.Background(), SpendingIntent{AmountSat: 101}); !errors.Is(err, ErrSpendingGuardLimit) {
		t.Fatalf("per payment limit no longer applies: %v", err)
	}
	if _, err := svc.Reserve(context.Background(), SpendingIntent{AmountSat: 1}); !errors.Is(err, ErrSpendingGuardLimit) {
		t.Fatalf("rolling limit did not survive rejected update: %v", err)
	}
	if rec := call(`{"enabled":true,"max_payment_sat":50,"rolling_24h_limit_sat":150}`); rec.Code != 200 {
		t.Fatalf("tightening: %d %s", rec.Code, rec.Body.String())
	}
	auth.sessions["test"].ReauthScopes[authScopeSpendingGuardPolicy] = now.Add(time.Minute)
	body := `{"enabled":true,"max_payment_sat":50,"rolling_24h_limit_sat":0}`
	if rec := call(body); rec.Code != 428 {
		t.Fatal("broad spending-policy grant authorized a changed policy")
	}
	intent := financialHandlerApproval(t, s, authSessionSnapshot{ID: "test"}, "/api/wallet/spending-guard", []byte(body), financialIntentBinding{Kind: "request"})
	if rec := call(string(intent.Request), intent.ID); rec.Code != 200 {
		t.Fatalf("approved removal: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := svc.Reserve(context.Background(), SpendingIntent{AmountSat: 1}); err != nil {
		t.Fatalf("approved rolling removal did not take effect: %v", err)
	}
}

func TestSpendingPolicyUpdateChecksLockedCurrentState(t *testing.T) {
	pool := spendingPolicyTestPool(t)
	ctx := context.Background()
	svc := NewSpendingGuardService(pool, nil)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update spending_guard_settings set max_payment_sat=50 where id=1`); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := svc.UpdateSettings(ctx, SpendingGuardSettings{Enabled: true, MaxPaymentSat: 75, Rolling24hLimitSat: 200}, false)
		result <- err
	}()
	// Wait until PostgreSQL reports that the competing update is waiting for the
	// locked row. This avoids proving a race using an arbitrary sleep.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and pid<>pg_backend_pid())`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("update did not wait for policy lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrSpendingGuardReauth) {
		t.Fatalf("stale authorization allowed a relaxation: %v", err)
	}
	settings, err := svc.Settings(ctx)
	if err != nil || settings.MaxPaymentSat != 50 {
		t.Fatalf("newer policy overwritten: %+v %v", settings, err)
	}
}
