package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lightningos-light/lnrpc"
)

func TestPaymentCursorPostgresRecoveryAndTelegramQueue(t *testing.T) {
	dsn := os.Getenv("LOS_PAYMENT_CURSOR_TEST_DSN")
	if dsn == "" {
		t.Skip("requires a dedicated los_payment_cursor_test_* PostgreSQL database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasPrefix(cfg.ConnConfig.Database, "los_payment_cursor_test_") {
		t.Fatal("dedicated cursor test database required")
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	n := NewNotifier(pool, nil, log.New(io.Discard, "", 0))
	if err := n.ensureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `truncate notification_cursors,notifications;insert into notification_cursors(key,value) values ('payments_index','1000'),('invoice_settle_index','77')`); err != nil {
		t.Fatal(err)
	}
	r := paymentCursorRecovery{PreviousCursor: "1000", ResumeAfter: 10, ReplayThrough: 20, RecoveredAtNS: time.Now().UnixNano()}
	if err := n.savePaymentCursorRecovery(ctx, "1000", r); err != nil {
		t.Fatal(err)
	}
	get := func(key string) string {
		v, err := n.getCursor(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if get("payments_index") != "10" || get("invoice_settle_index") != "77" {
		t.Fatal("cursor mutation crossed collector boundaries")
	}
	restored, err := decodePaymentCursorRecovery(get(paymentCursorRecoveryKey))
	if err != nil || restored != r {
		t.Fatal("missing recovery fence")
	}
	var count int
	countBackups := func() int {
		if err := pool.QueryRow(ctx, "select count(*) from notification_cursors where key like 'payments_cursor_recovery:%'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if countBackups() != 1 {
		t.Fatal("missing original cursor backup")
	}
	if err := n.savePaymentCursorRecovery(ctx, "1000", r); err == nil {
		t.Fatal("stale compare-and-swap replaced a concurrent cursor")
	}
	if countBackups() != 1 || get("payments_index") != "10" {
		t.Fatal("failed CAS mutated state")
	}
	if _, err := pool.Exec(ctx, `update notification_cursors set value='1000' where key='payments_index';
create or replace function los_cursor_test_reject() returns trigger language plpgsql as $$begin if NEW.key='payments_cursor_recovery' then raise exception 'injected checkpoint failure';end if;return NEW;end$$;
create trigger los_cursor_test_reject before insert on notification_cursors for each row execute function los_cursor_test_reject()`); err != nil {
		t.Fatal(err)
	}
	next := r
	next.RecoveredAtNS++
	if err := n.savePaymentCursorRecovery(ctx, "1000", next); err == nil {
		t.Fatal("expected injected failure")
	}
	if countBackups() != 1 || get("payments_index") != "1000" || get(paymentCursorRecoveryKey) == "" {
		t.Fatal("transaction did not roll back")
	}
	if _, err := pool.Exec(ctx, `drop trigger los_cursor_test_reject on notification_cursors;drop function los_cursor_test_reject()`); err != nil {
		t.Fatal(err)
	}
	if err := n.savePaymentCursorRecovery(ctx, "1000", next); err != nil {
		t.Fatal("retry failed", err)
	}
	if countBackups() != 2 {
		t.Fatal("retry did not retain both backups")
	}

	// All event times are AFTER manager startup: the older startup-only mirror
	// guard cannot mask a missing recovery fence in this test.
	n.startedAt = time.Now().Add(-time.Hour)
	n.setTelegramActivityMirrorEnabled(true)
	pay := &lnrpc.Payment{PaymentIndex: 15, CreationTimeNs: time.Now().Add(-time.Minute).UnixNano()}
	event := Notification{OccurredAt: time.Unix(0, pay.CreationTimeNs), Type: "rebalance", Action: "rebalanced", Direction: "out", Status: "SETTLED", AmountSat: 50000, FeeMsat: 13088, PaymentHash: "historical"}
	if _, err := n.upsertNotificationWithOptions(ctx, "payment:historical", event, notificationUpsertOptions{suppressMirror: next.suppressMirror(pay)}); err != nil {
		t.Fatal(err)
	}
	if len(n.telegramMirrorQueue) != 0 {
		t.Fatal("historical recovery enqueued Telegram")
	}
	raw, _ := json.Marshal(next)
	loaded, _ := decodePaymentCursorRecovery(string(raw))
	restarted := NewNotifier(pool, nil, log.New(io.Discard, "", 0))
	restarted.startedAt = n.startedAt
	restarted.setTelegramActivityMirrorEnabled(true)
	if _, err := restarted.upsertNotificationWithOptions(ctx, "payment:historical", event, notificationUpsertOptions{suppressMirror: loaded.suppressMirror(pay)}); err != nil {
		t.Fatal(err)
	}
	event.PaymentHash = "historical-missing"
	if _, err := restarted.upsertNotificationWithOptions(ctx, "payment:historical-missing", event, notificationUpsertOptions{suppressMirror: loaded.suppressMirror(pay)}); err != nil {
		t.Fatal(err)
	}
	if len(restarted.telegramMirrorQueue) != 0 {
		t.Fatal("restart replay enqueued historical alerts")
	}
	event.PaymentHash = "pending"
	event.Status = "IN_FLIGHT"
	if _, err := restarted.upsertNotification(ctx, "payment:pending", event); err != nil {
		t.Fatal(err)
	}
	event.Status = "SETTLED"
	pay.Htlcs = []*lnrpc.HTLCAttempt{{ResolveTimeNs: loaded.RecoveredAtNS + 1}}
	for i := 0; i < 2; i++ {
		if _, err := restarted.upsertNotificationWithOptions(ctx, "payment:pending", event, notificationUpsertOptions{suppressMirror: loaded.suppressMirror(pay)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(restarted.telegramMirrorQueue) != 1 {
		t.Fatal("live pending completion missing or duplicated")
	}
	queued := <-restarted.telegramMirrorQueue
	if queued.Type != "rebalance" || queued.Status != "SETTLED" || queued.AmountSat != 50000 || queued.FeeMsat != 13088 {
		t.Fatal("rebalance classification or values regressed")
	}
	var duplicates int
	if err := pool.QueryRow(ctx, "select count(*) from notifications where event_key='payment:historical'").Scan(&duplicates); err != nil || duplicates != 1 {
		t.Fatal("historical replay duplicated notification")
	}

	// Preserve the pre-existing invoice/payment reconciliation in either order.
	// This uses the actual SQL reconciliation, not a copied classification rule.
	restarted.setTelegramActivityMirrorEnabled(false)
	for i, invoiceFirst := range []bool{true, false} {
		hash := fmt.Sprintf("%064x", 1000+i)
		payment := Notification{OccurredAt: time.Now(), Type: "lightning", Action: "sent", Direction: "out", Status: "SUCCEEDED", AmountSat: 50000, FeeSat: 13, FeeMsat: 13088, PaymentHash: hash}
		invoice := Notification{OccurredAt: payment.OccurredAt, Type: "lightning", Action: "received", Direction: "in", Status: "SETTLED", AmountSat: 50000, PaymentHash: hash}
		for _, received := range []bool{invoiceFirst, !invoiceFirst} {
			event, key := payment, "payment:"+hash
			if received {
				event, key = invoice, "invoice:"+hash
			}
			if _, err := restarted.upsertNotification(ctx, key, event); err != nil {
				t.Fatal(err)
			}
			restarted.reconcileRebalance(ctx, hash)
		}
		restarted.reconcileRebalance(ctx, hash)
		var rows, amount, fee int64
		var kind, status string
		if err := pool.QueryRow(ctx, "select count(*) from notifications where payment_hash=$1", hash).Scan(&rows); err != nil || rows != 1 {
			t.Fatal("invoice/payment order duplicated rebalance")
		}
		if err := pool.QueryRow(ctx, "select type,status,amount_sat,fee_msat from notifications where payment_hash=$1", hash).Scan(&kind, &status, &amount, &fee); err != nil || kind != "rebalance" || status != "SETTLED" || amount != 50000 || fee != 13088 {
			t.Fatal("invoice/payment reconciliation changed classification or values", err)
		}
	}
}
