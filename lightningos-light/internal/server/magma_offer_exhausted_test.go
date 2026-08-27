package server

import "testing"

// The offer on Friendspool that prompted this: 100% sold, 4,236 sat left to
// sell, a 1,000,000 sat minimum channel, and 499,973 sat on-chain. It sat
// ENABLED with a red blocking dot next to it. The guard compared the wallet
// against what was left to sell - 4,236 fits anywhere - so it concluded the
// offer was affordable and left it up.
func TestMagmaOfferSmallestOrderIsTheMinimumNotTheRemainder(t *testing.T) {
	offer := MagmaOffer{
		TotalSizeSat: 7_500_000,
		SoldSat:      7_495_764,
		RemainingSat: 4_236,
		MinSizeSat:   1_000_000,
		MaxSizeSat:   5_000_000,
	}
	if got := magmaOfferSmallestOrder(offer); got != 1_000_000 {
		t.Fatalf("the only order this offer can produce is the 1,000,000 sat minimum, got %d", got)
	}
	// The wallet at the time. Measured against the remainder it looked covered;
	// measured against the smallest real order it plainly was not.
	const wallet = 499_973
	if magmaOfferRemaining(offer) > wallet {
		t.Fatal("the old measure has to look affordable, otherwise this test proves nothing")
	}
	if magmaOfferSmallestOrder(offer) <= wallet {
		t.Fatal("the new measure must show the offer cannot be honoured")
	}
}

// An offer with room left is still measured by what it has left to sell: that
// is the real ceiling on what it can still commit, and reserving the minimum
// instead would free capital the offer can genuinely spend.
func TestMagmaOfferSmallestOrderKeepsTheRemainderWhenItIsLarger(t *testing.T) {
	offer := MagmaOffer{
		TotalSizeSat: 7_500_000,
		SoldSat:      2_000_000,
		RemainingSat: 5_500_000,
		MinSizeSat:   1_000_000,
	}
	if got := magmaOfferSmallestOrder(offer); got != 5_500_000 {
		t.Fatalf("expected the remainder while it exceeds the minimum, got %d", got)
	}
}

// A fresh offer that has sold nothing reports no remainder, and must not be
// read as exhausted.
func TestMagmaOfferSmallestOrderHandlesAnUntouchedOffer(t *testing.T) {
	offer := MagmaOffer{TotalSizeSat: 5_000_000, MinSizeSat: 1_000_000}
	if got := magmaOfferSmallestOrder(offer); got != 5_000_000 {
		t.Fatalf("an untouched offer still has its whole size to sell, got %d", got)
	}
}

// The exhausted case was already detected and marked blocking for the UI. The
// guard now reads that same judgement, so the red dot and the action agree.
func TestMagmaOfferExhaustedIsUnfulfillable(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinChannelSizeSat, policy.MaxChannelSizeSat = 0, 0
	policy.MinPricePPM, policy.MinPricePPMPerDay, policy.MinFeeRateCapPPM = 0, 0, 0
	policy.MaxCommitmentDays = 0

	exhausted := MagmaOffer{
		TotalSizeSat: 7_500_000, SoldSat: 7_495_764, RemainingSat: 4_236,
		MinSizeSat: 1_000_000, MaxSizeSat: 5_000_000,
		FeeRatePPM: 1800, FeeRateCapPPM: 1500, BaseFeeCapSat: 1,
	}
	if !magmaOfferIsUnfulfillable(exhausted, policy) {
		t.Fatal("an offer with less left than its own minimum channel cannot be honoured")
	}

	healthy := exhausted
	healthy.SoldSat, healthy.RemainingSat = 2_000_000, 5_500_000
	if magmaOfferIsUnfulfillable(healthy, policy) {
		t.Fatal("an offer that can still produce a valid order must stay up")
	}
}
