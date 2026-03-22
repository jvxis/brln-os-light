package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestResolvePendingOpenBumpPlanUsesCurrentFeeFloor(t *testing.T) {
	plan, err := resolvePendingOpenBumpPlan(lndclient.PendingChannelInfo{
		FundingFeeRateSatVbyte: 7,
	}, "economic", 0, &mempoolFeeRecommendation{
		EconomyFee: 3,
		HourFee:    5,
		FastestFee: 9,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.SatPerVbyte != 8 {
		t.Fatalf("expected sat/vB 8, got %d", plan.SatPerVbyte)
	}
	if plan.Immediate {
		t.Fatalf("expected economic preset to avoid immediate sweep")
	}
}

func TestResolvePendingOpenBumpPlanUrgentUsesFastestAndImmediate(t *testing.T) {
	plan, err := resolvePendingOpenBumpPlan(lndclient.PendingChannelInfo{
		FundingFeeRateSatVbyte: 1,
	}, "urgent", 0, &mempoolFeeRecommendation{
		FastestFee: 12,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.SatPerVbyte != 12 {
		t.Fatalf("expected sat/vB 12, got %d", plan.SatPerVbyte)
	}
	if !plan.Immediate {
		t.Fatalf("expected urgent preset to use immediate sweep")
	}
	if plan.EstimatedFeeSat != 12*pendingOpenBumpReferenceVbytes {
		t.Fatalf("unexpected estimated fee: %d", plan.EstimatedFeeSat)
	}
}
