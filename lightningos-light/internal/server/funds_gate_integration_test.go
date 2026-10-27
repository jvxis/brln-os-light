package server

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log"
	"os"
	"strings"
	"testing"
)

func TestFundsGateExplicitDisabledAndActivePolicy(t *testing.T) {
	dsn := os.Getenv("LOS_SPENDING_GUARD_TEST_DSN")
	if dsn == "" {
		t.Skip("requires dedicated los_spending_guard_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_spending_guard_test_") {
		t.Fatal("dedicated spending guard database required")
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
	if _, err := pool.Exec(ctx, `truncate spending_guard_entries;update spending_guard_settings set enabled=false,max_payment_sat=100,rolling_24h_limit_sat=200 where id=1`); err != nil {
		t.Fatal(err)
	}
	client, rpc := newFundsGateLND(t)
	logger := log.New(io.Discard, "", 0)
	chat := NewChatService(client, logger)
	chat.AttachSpendingGuard(guard)
	s := &Server{lnd: client, logger: logger, spendingGuard: guard}
	reservation, _, err := s.reserveInvoiceSpending(ctx, "test-invoice", 100, 1, "wallet")
	if err != nil || reservation.Active {
		t.Fatalf("explicitly disabled policy blocked existing payments: %+v %v", reservation, err)
	}
	_, err = chat.SendMessage(ctx, "02"+strings.Repeat("11", 32), 100, "test")
	if err == nil || rpc.sends.Load() != 1 {
		t.Fatalf("disabled policy did not reach simulated payment rejection: sends=%d err=%v", rpc.sends.Load(), err)
	}
	if _, err := pool.Exec(ctx, `update spending_guard_settings set enabled=true where id=1`); err != nil {
		t.Fatal(err)
	}
	_, err = chat.SendMessage(ctx, "02"+strings.Repeat("11", 32), 100, "test")
	if !errors.Is(err, ErrSpendingGuardLimit) || rpc.sends.Load() != 1 {
		t.Fatalf("active policy did not block payment before RPC: sends=%d err=%v", rpc.sends.Load(), err)
	}
	pool.Close()
	_, err = chat.SendMessage(ctx, "02"+strings.Repeat("11", 32), 1, "test")
	if err == nil || rpc.sends.Load() != 1 {
		t.Fatalf("database loss allowed payment: sends=%d err=%v", rpc.sends.Load(), err)
	}
}
