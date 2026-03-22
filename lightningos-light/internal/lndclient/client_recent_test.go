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

func TestRecentPaymentPageSizes(t *testing.T) {
	got := recentPaymentPageSizes(1000)
	want := []uint64{1000, 500, 200, 100, 50}
	if len(got) != len(want) {
		t.Fatalf("expected %d sizes, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected size %d at position %d, got %d", want[i], i, got[i])
		}
	}
}

func TestRecentPaymentPageSizesDeduplicatesLimit(t *testing.T) {
	got := recentPaymentPageSizes(200)
	want := []uint64{200, 500, 100, 50}
	if len(got) != len(want) {
		t.Fatalf("expected %d sizes, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected size %d at position %d, got %d", want[i], i, got[i])
		}
	}
}
