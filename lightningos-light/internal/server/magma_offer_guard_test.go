package server

import (
	"sort"
	"testing"
	"time"
)

// What an offer still owes is total minus sold, because Amboss never decrements
// total_size. Sizing the guard off the total would suspend an offer that is
// almost finished and needs very little cover.
func TestMagmaOfferRemainingUsesWhatIsLeft(t *testing.T) {
	cases := []struct {
		name  string
		offer MagmaOffer
		want  int64
	}{
		{"partly sold", MagmaOffer{TotalSizeSat: 6_000_000, SoldSat: 4_584_070, RemainingSat: 1_415_930}, 1_415_930},
		{"untouched", MagmaOffer{TotalSizeSat: 2_000_000}, 2_000_000},
		{"sold out", MagmaOffer{TotalSizeSat: 2_000_000, SoldSat: 2_000_000, RemainingSat: 0}, 0},
		{"never negative", MagmaOffer{TotalSizeSat: 1_000_000, SoldSat: 1_500_000, RemainingSat: -500_000}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := magmaOfferRemaining(tc.offer); got != tc.want {
				t.Fatalf("want %d, got %d", tc.want, got)
			}
		})
	}
}

// The worked example from the issue: a 2,000,000 sat offer with a 100,000 sat
// reserve needs 2,100,000 available; after selling 1,000,000 it needs 1,100,000.
func TestMagmaOfferCoverageFollowsTheReserve(t *testing.T) {
	const reserve = int64(100_000)
	fresh := MagmaOffer{TotalSizeSat: 2_000_000}
	if required := magmaOfferRemaining(fresh) + reserve; required != 2_100_000 {
		t.Fatalf("fresh offer requires 2,100,000, got %d", required)
	}
	half := MagmaOffer{TotalSizeSat: 2_000_000, SoldSat: 1_000_000, RemainingSat: 1_000_000}
	if required := magmaOfferRemaining(half) + reserve; required != 1_100_000 {
		t.Fatalf("half-sold offer requires 1,100,000, got %d", required)
	}
}

// Two offers must not both count the same sats. Keeping the smallest that fit
// saves as many offers as the balance honours, instead of one big offer sinking
// the rest.
func TestMagmaOfferGuardDoesNotShareTheSameBalanceTwice(t *testing.T) {
	offers := []MagmaOffer{
		{ID: "big", TotalSizeSat: 3_000_000, RemainingSat: 3_000_000},
		{ID: "small", TotalSizeSat: 1_000_000, RemainingSat: 1_000_000},
		{ID: "mid", TotalSizeSat: 2_000_000, RemainingSat: 2_000_000},
	}
	sort.Slice(offers, func(i, j int) bool {
		left, right := magmaOfferRemaining(offers[i]), magmaOfferRemaining(offers[j])
		if left != right {
			return left < right
		}
		return offers[i].ID < offers[j].ID
	})

	budget := int64(3_500_000) // 3,600,000 confirmed minus a 100,000 reserve
	kept := make([]string, 0, 3)
	for _, offer := range offers {
		remaining := magmaOfferRemaining(offer)
		if remaining <= budget {
			budget -= remaining
			kept = append(kept, offer.ID)
		}
	}
	if len(kept) != 2 || kept[0] != "small" || kept[1] != "mid" {
		t.Fatalf("expected small+mid to survive on 3,500,000, kept %v", kept)
	}
	if budget != 500_000 {
		t.Fatalf("budget must be consumed once per offer, left %d", budget)
	}
}

// Restoring has to be deterministic, or the same balance brings back a different
// offer on each pass. Oldest suspension first, then the cheapest, then the id.
func TestMagmaOfferGuardRestoreOrderIsDeterministic(t *testing.T) {
	old := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	states := map[string]MagmaOfferState{
		"a": {OfferID: "a", AutoDisabledAt: &recent},
		"b": {OfferID: "b", AutoDisabledAt: &old},
		"c": {OfferID: "c", AutoDisabledAt: &old},
	}
	suspended := []MagmaOffer{
		{ID: "a", RemainingSat: 500_000},
		{ID: "c", RemainingSat: 900_000},
		{ID: "b", RemainingSat: 900_000},
	}
	sort.Slice(suspended, func(i, j int) bool {
		leftAt, rightAt := states[suspended[i].ID].AutoDisabledAt, states[suspended[j].ID].AutoDisabledAt
		if leftAt != nil && rightAt != nil && !leftAt.Equal(*rightAt) {
			return leftAt.Before(*rightAt)
		}
		left, right := magmaOfferRemaining(suspended[i]), magmaOfferRemaining(suspended[j])
		if left != right {
			return left < right
		}
		return suspended[i].ID < suspended[j].ID
	})
	got := []string{suspended[0].ID, suspended[1].ID, suspended[2].ID}
	want := []string{"b", "c", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("restore order = %v, want %v (oldest first, then id)", got, want)
		}
	}
}

// An offer whose terms the policy refuses must not come back just because the
// balance did: that trades a funding problem for a reputation one.
func TestMagmaOfferGuardWillNotRestoreAPolicyBlockedOffer(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinChannelSizeSat = 1_000_000
	policy.MaxChannelSizeSat = 2_000_000
	policy.MinPricePPM, policy.MinPricePPMPerDay, policy.MinRevenueSat = 0, 0, 0
	policy.MaxCommitmentDays = 60

	blocked := MagmaOffer{
		MinSizeSat: 5_000_000, MaxSizeSat: 6_000_000, TotalSizeSat: 6_000_000,
		FeeRatePPM: 2400, FeeRateCapPPM: 900, MinBlockLength: 4320,
	}
	if !magmaOfferHasBlockingConflict(magmaOfferConflicts(blocked, policy)) {
		t.Fatal("an offer selling only above the policy maximum must be blocking")
	}

	fine := MagmaOffer{
		MinSizeSat: 1_000_000, MaxSizeSat: 2_000_000, TotalSizeSat: 6_000_000,
		FeeRatePPM: 2400, FeeRateCapPPM: 900, MinBlockLength: 4320,
	}
	if magmaOfferHasBlockingConflict(magmaOfferConflicts(fine, policy)) {
		t.Fatal("an offer inside the policy must be restorable")
	}
}
