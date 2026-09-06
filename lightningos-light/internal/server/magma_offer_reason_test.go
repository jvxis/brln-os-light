package server

import (
	"strings"
	"testing"
)

// The offer taken down on 2026-09-06: 7,600,000 sat published, 7,464,090 sold,
// 135,910 left, and a 1,000,000 sat smallest channel. It could no longer produce
// a valid order, so disabling it was right - but the message said the on-chain
// balance no longer covered it, printed beside 3,727,500 sat of free balance.
// A correct decision explained by a contradiction sends the operator hunting a
// balance problem that does not exist.
func TestMagmaOfferDisableReasonNamesExhaustionNotBalance(t *testing.T) {
	offer := MagmaOffer{
		TotalSizeSat: 7_600_000, SoldSat: 7_464_090, RemainingSat: 135_910,
		MinSizeSat: 1_000_000, MaxSizeSat: 3_000_000,
	}
	reason := magmaOfferDisableReason(offer, true)
	if strings.Contains(strings.ToLower(reason), "balance") {
		t.Fatalf("an exhausted offer has nothing to do with the balance: %q", reason)
	}
	for _, want := range []string{"135,910", "1,000,000"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("the reason must show %s so the numbers agree with the words: %q", want, reason)
		}
	}
}

// When the balance really is the reason, the message says so - and quotes what
// the offer actually needs, which is the smallest order it can still produce
// rather than the remainder that always fits.
func TestMagmaOfferDisableReasonStillReportsRealShortfalls(t *testing.T) {
	offer := MagmaOffer{
		TotalSizeSat: 5_000_000, SoldSat: 0, RemainingSat: 5_000_000,
		MinSizeSat: 1_000_000, MaxSizeSat: 5_000_000,
	}
	reason := magmaOfferDisableReason(offer, false)
	if !strings.Contains(strings.ToLower(reason), "balance") {
		t.Fatalf("a genuine shortfall must still be named as one: %q", reason)
	}
	if !strings.Contains(reason, "5,000,000") {
		t.Fatalf("expected the amount the offer needs, got %q", reason)
	}
}
