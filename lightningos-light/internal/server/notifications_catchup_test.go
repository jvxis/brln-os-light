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
