package lndclient

import (
	"testing"
	"time"

	"lightningos-light/lnrpc"
)

func TestRecentPaymentTimestampPrefersCreationDate(t *testing.T) {
	pay := &lnrpc.Payment{
		CreationDate:   1_710_000_000,
		CreationTimeNs: 1_720_000_000_123_000_000,
	}

	got := recentPaymentTimestamp(pay)
	want := time.Unix(pay.CreationDate, 0).UTC()
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestRecentPaymentTimestampFallsBackToCreationTimeNs(t *testing.T) {
	pay := &lnrpc.Payment{
		CreationTimeNs: 1_720_000_000_123_000_000,
	}

	got := recentPaymentTimestamp(pay)
	want := time.Unix(0, pay.CreationTimeNs).UTC()
	if !got.Equal(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
