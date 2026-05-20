package server

import (
	"testing"
	"time"
)

func TestNotificationIsHistoricalCatchup(t *testing.T) {
	startedAt := time.Date(2026, time.January, 23, 13, 33, 0, 0, time.UTC)

	if !notificationIsHistoricalCatchup(startedAt, startedAt.Add(-10*time.Minute), invoiceCatchupLiveGrace) {
		t.Fatal("expected old event to be treated as catch-up")
	}
	if notificationIsHistoricalCatchup(startedAt, startedAt.Add(-time.Minute), invoiceCatchupLiveGrace) {
		t.Fatal("did not expect near-live event to be treated as catch-up")
	}
}

func TestShouldContinuePaymentsCatchup(t *testing.T) {
	if !shouldContinuePaymentsCatchup(paymentsPollPageSize) {
		t.Fatal("expected full payment page to keep catch-up active")
	}
	if shouldContinuePaymentsCatchup(paymentsPollPageSize - 1) {
		t.Fatal("did not expect short payment page to keep catch-up active")
	}
	if shouldContinuePaymentsCatchup(0) {
		t.Fatal("did not expect empty payment page to keep catch-up active")
	}
}

func TestShouldSuppressHistoricalTelegramActivityMirror(t *testing.T) {
	now := time.Date(2026, time.January, 23, 13, 33, 0, 0, time.UTC)
	oldRebalance := Notification{
		OccurredAt: now.Add(-10 * time.Minute),
		Type:       "rebalance",
		Action:     "rebalanced",
		Status:     "SETTLED",
	}
	oldKeysend := Notification{
		OccurredAt: now.Add(-10 * time.Minute),
		Type:       "keysend",
		Action:     "sent",
		Status:     "SUCCEEDED",
	}

	oldLightningSent := Notification{
		OccurredAt: now.Add(-10 * time.Minute),
		Type:       "lightning",
		Action:     "sent",
		Status:     "SUCCEEDED",
	}

	if !shouldSuppressHistoricalTelegramActivityMirror(oldRebalance, now, true, false, "", "") {
		t.Fatal("expected old inserted rebalance to be suppressed")
	}
	if !shouldSuppressHistoricalTelegramActivityMirror(oldKeysend, now, true, false, "", "") {
		t.Fatal("expected old inserted keysend to be suppressed")
	}
	if !shouldSuppressHistoricalTelegramActivityMirror(oldRebalance, now, false, true, "lightning", "sent") {
		t.Fatal("expected old lightning-to-rebalance conversion to be suppressed")
	}
	if !shouldSuppressHistoricalTelegramActivityMirror(oldRebalance, now, false, true, "rebalance", "rebalanced") {
		t.Fatal("expected old rebalance reconciled from a previous session to be suppressed")
	}
	if !shouldSuppressHistoricalTelegramActivityMirror(oldLightningSent, now, false, true, "lightning", "sent") {
		t.Fatal("expected old lightning sent reconciliation to be suppressed")
	}
}

func TestShouldSuppressHistoricalTelegramActivityMirrorDoesNotAffectLive(t *testing.T) {
	now := time.Date(2026, time.January, 23, 13, 33, 0, 0, time.UTC)
	liveRebalance := Notification{
		OccurredAt: now.Add(-time.Minute),
		Type:       "rebalance",
		Action:     "rebalanced",
		Status:     "SETTLED",
	}
	oldForward := Notification{
		OccurredAt: now.Add(-10 * time.Minute),
		Type:       "forward",
		Action:     "forwarded",
		Status:     "SETTLED",
	}

	if shouldSuppressHistoricalTelegramActivityMirror(liveRebalance, now, true, false, "", "") {
		t.Fatal("did not expect live rebalance to be suppressed")
	}
	if !shouldSuppressHistoricalTelegramActivityMirror(oldForward, now, true, false, "", "") {
		t.Fatal("expected old inserted forward to be suppressed")
	}
}
