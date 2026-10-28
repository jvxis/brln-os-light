package server

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"lightningos-light/internal/config"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLNURLPaymentModesRetainSpendingGuard(t *testing.T) {
	dsn := os.Getenv("LOS_SPENDING_GUARD_TEST_DSN")
	if dsn == "" {
		t.Skip("requires dedicated los_spending_guard_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_spending_guard_test_") {
		t.Fatal("dedicated test database required")
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	guard := NewSpendingGuardService(pool, nil)
	if err := guard.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `truncate spending_guard_entries;update spending_guard_settings set enabled=true,max_payment_sat=1,rolling_24h_limit_sat=1000 where id=1`); err != nil {
		t.Fatal(err)
	}
	client, _, rpc := lnurlInvoiceClient(t)
	lnurlInvoiceMerchant(t, func() string { return "lnbc1test" })
	logger := log.New(io.Discard, "", 0)
	auth := NewAuthService(&config.Config{}, logger)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	scope := lnurlInvoiceScope("alice@merchant.example", "lnbc1test", 100, 2, []uint64{})
	auth.sessions[session.ID].ReauthScopes[scope] = time.Now().Add(time.Minute)
	s := &Server{lnd: client, spendingGuard: guard, auth: auth, cfg: &config.Config{}, logger: logger, auditLogInitAt: time.Now()}
	call := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		payload, binding, err := s.prepareInvoiceFinancialIntent(ctx, path, []byte(`{"payment_request":"alice@merchant.example","amount_sat":100,"max_fee_sat":2}`))
		if err != nil {
			t.Fatal("prepare invoice for spending guard regression", err)
		}
		intent := financialHandlerApproval(t, s, session, path, payload, binding)
		r := httptest.NewRequest(http.MethodPost, "https://node.example"+path, strings.NewReader(string(intent.Request)))
		r.AddCookie(&http.Cookie{Name: authSessionCookieName, Value: session.ID})
		r.Header.Set("X-CSRF-Token", session.CSRFToken)
		r.Header.Set("Origin", "https://node.example")
		r.Header.Set(financialIntentHeader, intent.ID)
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/wallet/pay", "/api/wallet/pay/validated-route", "/api/wallet/pay/mpp"} {
		rec := call(path)
		if rec.Code != 429 || rpc.sends.Load() != 0 {
			t.Fatalf("%s skipped guard after scoped approval: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if _, err := pool.Exec(ctx, `update spending_guard_settings set max_payment_sat=1000 where id=1`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/wallet/pay", "/api/wallet/pay/mpp"} {
		before := rpc.sends.Load()
		rec := call(path)
		if rpc.sends.Load() != before+1 {
			t.Fatalf("%s did not reach the simulated payment rejection: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
