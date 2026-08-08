package server

import (
	"strings"
	"testing"
	"time"
)

func TestSpendingGuardAlertDecisionRateLimitsBySourceAndReason(t *testing.T) {
	s := &Server{spendingGuardAlerts: make(map[string]spendingGuardAlertState)}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	first := s.spendingGuardAlertDecision("wallet_invoice:per_payment", now)
	if !first.SendTelegram || first.Attempts != 1 {
		t.Fatalf("first decision = %+v", first)
	}
	second := s.spendingGuardAlertDecision("wallet_invoice:per_payment", now.Add(time.Minute))
	if second.SendTelegram || second.Attempts != 2 {
		t.Fatalf("second decision = %+v", second)
	}
	third := s.spendingGuardAlertDecision("wallet_invoice:per_payment", now.Add(spendingGuardAlertCooldown))
	if !third.SendTelegram || third.Suppressed != 1 {
		t.Fatalf("third decision = %+v", third)
	}
	other := s.spendingGuardAlertDecision("chat_keysend:per_payment", now.Add(time.Minute))
	if !other.SendTelegram {
		t.Fatalf("different source should alert independently: %+v", other)
	}
}

func TestSpendingGuardTelegramAlertContainsSafeOperationalDetails(t *testing.T) {
	msg := spendingGuardTelegramAlert(SpendingIntent{
		Source: "chat_keysend", AmountSat: 1_000, MaxFeeSat: 50, PaymentHash: "must-not-appear",
	}, SpendingLimitError{
		Reason: "rolling_24h", RequestedSat: 1_050, LimitSat: 10_000, RemainingSat: 500,
	}, 3)
	for _, want := range []string{"Chat Keysend", "1050 sats", "rolling 24-hour limit", "500 sats", "payment was not submitted", "3"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("alert missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "must-not-appear") {
		t.Fatal("payment hash must not be included in the security alert")
	}
}
