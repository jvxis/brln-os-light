package server

import (
	"strings"
	"testing"
	"time"
)

// Order a8c8bea8 was announced on 2026-08-02 and never answered: 2,484,848 sat
// passed the 2,500,000 sat size gate but could not fit the 2,000,000 sat daily
// cap, so it was filed as a wait. The cap resets at midnight and the order still
// would not have fit, so the wait could never end. Amboss recorded
// SELLER_FAILED_TO_REACT 2h04m later - the first on an account with 89 orders.
func TestMagmaOrderLargerThanDailyCapIsRejectedNotDeferred(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinRevenueSat = 0
	policy.MinPricePPM = 0
	policy.MinPricePPMPerDay = 0
	policy.MinFeeRateCapPPM = 0
	policy.MaxChannelSizeSat = 2_500_000
	policy.MaxDailySizeSat = 2_000_000
	policy.MaxCommitmentDays = 60

	in := magmaPolicyInputs{
		Order: MagmaOrder{
			ID: "a8c8bea8-1e0e-4aff-b6c6-dc005bcc3c2a", SizeSat: 2_484_848,
			RevenueSat: 6284, PricePPM: 2300, CommitmentBlocks: 4320,
			FeeRateCapPPM: 900, BaseFeeCapSat: 1,
		},
		AvailableSat:     6_370_711,
		OnchainReachable: true,
		SatPerVbyte:      2,
		EstimatedFeeSat:  300,
	}

	decision := evaluateMagmaOrder(policy, in)
	if !decision.Reject {
		t.Fatalf("an order that can never fit the daily cap must be rejected, got accept=%t reason=%q",
			decision.Accept, decision.Reason)
	}
	if decision.Accept {
		t.Fatal("must not accept: the operator set the cap deliberately")
	}
}

// The everyday case stays a deferral: the day is partly used, so tomorrow - or a
// midnight a few minutes away - genuinely clears it.
func TestMagmaDailyCapPartiallyUsedStillDefers(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinRevenueSat, policy.MinPricePPM, policy.MinPricePPMPerDay = 0, 0, 0
	policy.MaxDailySizeSat = 5_000_000

	in := magmaPolicyInputs{
		Order:            MagmaOrder{SizeSat: 2_000_000, RevenueSat: 6000, CommitmentBlocks: 4320},
		AvailableSat:     9_000_000,
		SizeToday:        4_000_000,
		OnchainReachable: true,
	}
	decision := evaluateMagmaOrder(policy, in)
	if decision.Accept || decision.Reject {
		t.Fatalf("expected a deferral, got accept=%t reject=%t (%s)",
			decision.Accept, decision.Reject, decision.Reason)
	}
}

// Any deferral that survives to the deadline becomes an explicit refusal. Silence
// is the one outcome that costs the sale and the seller record together.
func TestMagmaDeferralBecomesRejectionAtTheDeadline(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinRevenueSat, policy.MinPricePPM, policy.MinPricePPMPerDay = 0, 0, 0
	policy.MaxSatPerVbyte = 10

	base := magmaPolicyInputs{
		Order:            MagmaOrder{SizeSat: 1_500_000, RevenueSat: 6000, CommitmentBlocks: 4320},
		AvailableSat:     9_000_000,
		OnchainReachable: true,
		// A mempool spike: a real transient, correctly deferred while there is time.
		SatPerVbyte: 40,
	}

	fresh := base
	fresh.PendingFor = 5 * time.Minute
	if d := evaluateMagmaOrder(policy, fresh); d.Reject {
		t.Fatalf("a fresh order must be given time to clear, got reject: %s", d.Reason)
	}

	late := base
	late.PendingFor = magmaApprovalGrace + time.Minute
	d := evaluateMagmaOrder(policy, late)
	if !d.Reject {
		t.Fatalf("past the grace period the order must be refused explicitly, got %q", d.Reason)
	}
	if d.Accept {
		t.Fatal("the blocker never cleared, so accepting would be wrong")
	}
}

// Without a creation time the age is unknown; refusing on a guessed age would
// throw away good orders the moment they appear.
func TestMagmaDeadlineIgnoredWhenAgeUnknown(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinRevenueSat, policy.MinPricePPM, policy.MinPricePPMPerDay = 0, 0, 0
	policy.MaxSatPerVbyte = 10

	in := magmaPolicyInputs{
		Order:            MagmaOrder{SizeSat: 1_500_000, RevenueSat: 6000, CommitmentBlocks: 4320},
		AvailableSat:     9_000_000,
		OnchainReachable: true,
		SatPerVbyte:      40,
	}
	if d := evaluateMagmaOrder(policy, in); d.Reject {
		t.Fatalf("PendingFor zero means unknown, not expired: %s", d.Reason)
	}
}

// The grace period is only useful if it lands comfortably inside the window
// Amboss actually allows.
func TestMagmaGraceLeavesRoomInsideTheWindow(t *testing.T) {
	if magmaApprovalGrace >= magmaApprovalWindow {
		t.Fatal("the grace period must expire before Amboss does")
	}
	if margin := magmaApprovalWindow - magmaApprovalGrace; margin < 30*time.Minute {
		t.Fatalf("only %s of margin: a 90s poll plus a slow accept needs more room", margin)
	}
}

// A limit set to zero is off. The summary is the only place the operator reads
// the policy back in prose, so "0 sat per day" there reads as "sell nothing" -
// the opposite of what the setting does.
func TestMagmaPolicySummaryOmitsDisabledLimits(t *testing.T) {
	policy := MagmaPolicy{
		MinChannelSizeSat: 1_000_000,
		MaxChannelSizeSat: 2_500_000,
		MaxOnchainCostPct: 60,
		MaxDailyOrders:    5,
		// The operator switched the daily size cap off on purpose: selling the whole
		// offer in one day is a good outcome, not a limit to enforce.
		MaxDailySizeSat: 0,
	}
	summary := magmaPolicySummary(policy)
	for _, unwanted := range []string{"0 sat per day", "0 ppm", "0 ppm/day"} {
		if strings.Contains(summary, unwanted) {
			t.Fatalf("summary still advertises a disabled limit as zero: %q in %q", unwanted, summary)
		}
	}
	if !strings.Contains(summary, "5 orders per day") {
		t.Fatalf("the daily order cap is still active and must stay visible: %q", summary)
	}

	// With every daily cap off, say so rather than going silent on the subject.
	policy.MaxDailyOrders = 0
	if summary := magmaPolicySummary(policy); !strings.Contains(summary, "no daily limit") {
		t.Fatalf("expected the summary to state there is no daily limit: %q", summary)
	}
}

// With the daily size cap off, the order that expired on 2026-08-02 sails through.
func TestMagmaDailyCapOffAcceptsTheOrderThatWasLost(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinRevenueSat, policy.MinPricePPM, policy.MinPricePPMPerDay, policy.MinFeeRateCapPPM = 0, 0, 0, 0
	policy.MaxChannelSizeSat = 2_500_000
	policy.MaxDailySizeSat = 0
	policy.MaxCommitmentDays = 60

	decision := evaluateMagmaOrder(policy, magmaPolicyInputs{
		Order: MagmaOrder{
			ID: "a8c8bea8-1e0e-4aff-b6c6-dc005bcc3c2a", SizeSat: 2_484_848,
			RevenueSat: 6284, PricePPM: 2300, CommitmentBlocks: 4320,
			FeeRateCapPPM: 900, BaseFeeCapSat: 1,
		},
		AvailableSat:     6_370_711,
		OnchainReachable: true,
		SatPerVbyte:      2,
		EstimatedFeeSat:  300,
		PendingFor:       time.Minute,
	})
	if !decision.Accept {
		t.Fatalf("expected the sale to go through, got reject=%t reason=%q", decision.Reject, decision.Reason)
	}
}
