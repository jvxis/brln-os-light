package server

import (
	"strings"
	"testing"
)

// Offer 60cca37b was created with both routing caps at 0 and sold a 1,000,000
// sat channel for 2,284 sat. The channel is now contractually held at 0 ppm and
// 0 base for 8,640 blocks - sixty days of routing for free.
//
// The form defaulted those fields to 0, nothing required them, and the one check
// that existed skipped a zero cap outright.
func TestZeroRoutingCapIsAlwaysReported(t *testing.T) {
	// No policy floor at all, which is how the live account was configured.
	policy := defaultMagmaPolicy()
	policy.MinFeeRateCapPPM = 0
	policy.MinPricePPM, policy.MinPricePPMPerDay, policy.MinRevenueSat = 0, 0, 0
	policy.MinChannelSizeSat, policy.MaxChannelSizeSat = 1_000_000, 1_000_000
	policy.MaxCommitmentDays = 60
	policy.MaxDailySizeSat = 0

	offer := MagmaOffer{
		MinSizeSat: 1_000_000, MaxSizeSat: 1_000_000, TotalSizeSat: 1_000_000,
		FeeRatePPM: 2000, MinBlockLength: 8640,
		FeeRateCapPPM: 0, BaseFeeCapSat: 0,
	}
	conflicts := magmaOfferConflicts(offer, policy)
	if len(conflicts) == 0 {
		t.Fatal("a zero routing cap must never pass unremarked, even with no policy floor")
	}
	var mentioned bool
	for _, conflict := range conflicts {
		if strings.Contains(conflict.Message, "routes for free") {
			mentioned = true
			// Deliberately choosing 0 is legitimate - a loss leader, a favour -
			// so this warns without blocking.
			if conflict.Blocking {
				t.Error("a zero cap is a valid choice and must not be blocked, only surfaced")
			}
		}
	}
	if !mentioned {
		t.Fatalf("expected the warning to say the channel routes for free, got %+v", conflicts)
	}
}

// The floor exists to catch caps that are too low. Zero is the lowest of all,
// and the old `offer.FeeRateCapPPM > 0` guard made it the one value exempt.
func TestZeroCapNoLongerEscapesThePolicyFloor(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinFeeRateCapPPM = 100
	policy.MinPricePPM, policy.MinPricePPMPerDay, policy.MinRevenueSat = 0, 0, 0
	policy.MinChannelSizeSat, policy.MaxChannelSizeSat = 1_000_000, 5_000_000
	policy.MaxCommitmentDays = 60
	policy.MaxDailySizeSat = 0

	base := MagmaOffer{
		MinSizeSat: 1_000_000, MaxSizeSat: 2_000_000, TotalSizeSat: 5_000_000,
		FeeRatePPM: 2000, MinBlockLength: 8640, BaseFeeCapSat: 1,
	}

	t.Run("zero is caught by the floor", func(t *testing.T) {
		offer := base
		offer.FeeRateCapPPM = 0
		if !magmaOfferHasBlockingConflict(magmaOfferConflicts(offer, policy)) {
			t.Fatal("0 ppm is below a 100 ppm floor and must block")
		}
	})
	t.Run("a low but non-zero cap still blocks", func(t *testing.T) {
		offer := base
		offer.FeeRateCapPPM = 50
		if !magmaOfferHasBlockingConflict(magmaOfferConflicts(offer, policy)) {
			t.Fatal("50 ppm is below the floor and must block")
		}
	})
	t.Run("a cap above the floor is clean", func(t *testing.T) {
		offer := base
		offer.FeeRateCapPPM = 1500
		for _, conflict := range magmaOfferConflicts(offer, policy) {
			if conflict.Blocking {
				t.Fatalf("1500 ppm clears the floor: %s", conflict.Message)
			}
		}
	})
}
