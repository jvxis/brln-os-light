package server

import (
	"testing"
	"time"
)

func TestAttributeRebalanceForwardsFIFOOverlappingJobsConsumeForwardOnce(t *testing.T) {
	base := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	lots := []rebalanceAttributionLot{
		{JobID: 1, TargetChannelID: 10, CompletedAt: base, SentSat: 100},
		{JobID: 2, TargetChannelID: 10, CompletedAt: base.Add(10 * time.Minute), SentSat: 100},
	}
	forwards := []rebalanceAttributionForward{
		{ID: 50, TargetChannelID: 10, OccurredAt: base.Add(20 * time.Minute), AmountSat: 150, FeeMsat: 1500},
	}

	got := attributeRebalanceForwardsFIFO(lots, forwards, time.Hour)
	if got[1].ForwardAmountSat != 100 || got[1].ForwardFeeMsat != 1000 {
		t.Fatalf("job 1 attribution = %+v, want amount=100 fee_msat=1000", got[1])
	}
	if got[2].ForwardAmountSat != 50 || got[2].ForwardFeeMsat != 500 {
		t.Fatalf("job 2 attribution = %+v, want amount=50 fee_msat=500", got[2])
	}
	if total := got[1].ForwardAmountSat + got[2].ForwardAmountSat; total != 150 {
		t.Fatalf("forward was double counted: total attributed=%d, want 150", total)
	}
}

func TestAttributeRebalanceForwardsFIFOCapsAtAvailableLiquidity(t *testing.T) {
	base := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	lots := []rebalanceAttributionLot{
		{JobID: 1, TargetChannelID: 10, CompletedAt: base, SentSat: 100},
		{JobID: 2, TargetChannelID: 10, CompletedAt: base, SentSat: 100},
	}
	forwards := []rebalanceAttributionForward{
		{ID: 50, TargetChannelID: 10, OccurredAt: base.Add(time.Minute), AmountSat: 300, FeeMsat: 3000},
	}

	got := attributeRebalanceForwardsFIFO(lots, forwards, time.Hour)
	amount := got[1].ForwardAmountSat + got[2].ForwardAmountSat
	fee := got[1].ForwardFeeMsat + got[2].ForwardFeeMsat
	if amount != 200 {
		t.Fatalf("attributed amount=%d, want available liquidity 200", amount)
	}
	if fee != 2000 {
		t.Fatalf("attributed fee_msat=%d, want proportional fee 2000", fee)
	}
}

func TestAttributeRebalanceForwardsFIFOHonorsExpiryAndTarget(t *testing.T) {
	base := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	lots := []rebalanceAttributionLot{
		{JobID: 1, TargetChannelID: 10, CompletedAt: base, SentSat: 100},
		{JobID: 2, TargetChannelID: 10, CompletedAt: base.Add(2 * time.Hour), SentSat: 100},
		{JobID: 3, TargetChannelID: 20, CompletedAt: base, SentSat: 100},
	}
	forwards := []rebalanceAttributionForward{
		{ID: 50, TargetChannelID: 10, OccurredAt: base.Add(150 * time.Minute), AmountSat: 80, FeeMsat: 800},
		{ID: 51, TargetChannelID: 20, OccurredAt: base.Add(30 * time.Minute), AmountSat: 60, FeeMsat: 600},
	}

	got := attributeRebalanceForwardsFIFO(lots, forwards, time.Hour)
	if got[1].ForwardAmountSat != 0 {
		t.Fatalf("expired job 1 received attribution: %+v", got[1])
	}
	if got[2].ForwardAmountSat != 80 {
		t.Fatalf("job 2 attribution = %+v, want amount=80", got[2])
	}
	if got[3].ForwardAmountSat != 60 {
		t.Fatalf("target-isolated job 3 attribution = %+v, want amount=60", got[3])
	}
}
