package server

import (
	"context"
	"testing"
	"time"
)

func secondChanceConfig() RebalanceConfig {
	cfg := defaultRebalanceConfig()
	cfg.AutoEnabled = true
	cfg.GainModelVersion = 3
	cfg.ROIMin = 1.0
	cfg.EconRatio = 0.8
	cfg.MinAmountSat = 100_000
	cfg.MinExecuteSat = 10_000
	cfg.MaxAmountSat = 1_000_000
	cfg.SovereignExplorationSlotPct = 20
	cfg.SovereignGainV3ColdStartPct = 0.95
	cfg.RebalanceCostFloorPpm = 50
	return cfg
}

func secondChancePlan(t *testing.T, cfg RebalanceConfig, target RebalanceChannel, referencePpm int64) rebalanceAutoScanCandidatePlan {
	t.Helper()
	channels := []RebalanceChannel{rebalanceGoldenSource(1, 5_000_000), target}
	settings := map[uint64]channelSetting{}
	for _, ch := range channels {
		settings[ch.ChannelID] = channelSetting{ChannelID: ch.ChannelID, TargetOutboundPct: ch.TargetOutboundPct, AutoEnabled: true, UseDefaultEconRatio: true}
	}
	return buildAndOrderRebalanceCandidates(rebalanceAutoScanCandidateInput{
		Channels:         channels,
		Settings:         settings,
		Cfg:              cfg,
		ScanAt:           time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
		SovereignRanking: true,
		ReferenceCostPpm: referencePpm,
	})
}

// A drained channel that never had a paid refill: its cost is assumed at the
// fee budget and the peer charges a quarter of our fee, so the estimate is
// negative by construction.
func secondChanceUnmeasuredTarget() RebalanceChannel {
	return RebalanceChannel{
		ChannelID:         301,
		ChannelPoint:      "unmeasured-point",
		PeerAlias:         "unmeasured",
		Active:            true,
		CapacitySat:       5_000_000,
		LocalBalanceSat:   50_000,
		RemoteBalanceSat:  4_950_000,
		LocalPct:          1,
		OutgoingFeePpm:    400,
		PeerFeeRatePpm:    100,
		TargetOutboundPct: 20,
		TargetAmountSat:   950_000,
		EligibleAsTarget:  true,
	}
}

func TestMeasuredSellerNeedsOwnCostAndOwnSales(t *testing.T) {
	seller := peerFeeCostSellerTarget()
	if !sovereignMeasuredSeller(seller, 1_000_000) {
		t.Fatal("own cost and own sales covering the lot make a measured seller")
	}
	thinCost := seller
	thinCost.RebalanceAmount7dSat = 200_000
	if sovereignMeasuredSeller(thinCost, 1_000_000) {
		t.Fatal("a cost measured on less than the lot is not a measurement of the lot")
	}
	noSales := seller
	noSales.Revenue7dSat = 0
	noSales.DrainRateSatPerHour = 0
	if sovereignMeasuredSeller(noSales, 1_000_000) {
		t.Fatal("a channel that did not sell must not have the peer fee taken out of the gain")
	}
	// The option on, or an older gain model, leaves the config alone.
	cfg := secondChanceConfig()
	if got := sovereignTargetEconomicsConfig(cfg, seller, 1_000_000); !got.PeerFeeAsCostEnabled {
		t.Fatal("measured seller must count the peer hop once")
	}
	if got := sovereignTargetEconomicsConfig(cfg, noSales, 1_000_000); got.PeerFeeAsCostEnabled {
		t.Fatal("unsold channel must keep the legacy gain")
	}
	cfg.GainModelVersion = 2
	if got := sovereignTargetEconomicsConfig(cfg, seller, 1_000_000); got.PeerFeeAsCostEnabled {
		t.Fatal("gain models before v3 are untouched")
	}
}

func TestUnmeasuredTargetIsHeldForExplorationOnly(t *testing.T) {
	cfg := secondChanceConfig()
	plan := secondChancePlan(t, cfg, secondChanceUnmeasuredTarget(), 600)
	if len(plan.Candidates) != 1 || !plan.Candidates[0].SecondChanceOnly || plan.ROISkipped != 0 {
		t.Fatalf("unmeasured target must stay for the exploration draw: roi_skipped=%d candidates=%+v", plan.ROISkipped, plan.Candidates)
	}
	if got := withoutSecondChanceOnlyTargets(plan.Candidates); len(got) != 0 {
		t.Fatalf("held targets are not round candidates: %+v", got)
	}

	// Exploration off: nothing to draw, the guardrail drops it as before.
	off := cfg
	off.SovereignExplorationSlotPct = 0
	plan = secondChancePlan(t, off, secondChanceUnmeasuredTarget(), 600)
	if len(plan.Candidates) != 0 || plan.ROISkipped != 1 {
		t.Fatalf("exploration off must keep the guardrail: roi_skipped=%d candidates=%d", plan.ROISkipped, len(plan.Candidates))
	}

	// A channel whose own measured cost is above what it earns is a measured
	// loser, not an unknown: no second chance.
	loser := secondChanceUnmeasuredTarget()
	loser.RebalanceCost7dPpm = 390
	loser.RebalanceAmount7dSat = 2_000_000
	plan = secondChancePlan(t, cfg, loser, 600)
	if len(plan.Candidates) != 0 || plan.ROISkipped != 1 {
		t.Fatalf("measured loser must be dropped: roi_skipped=%d candidates=%+v", plan.ROISkipped, plan.Candidates)
	}

	// No spread to earn (the peer charges our whole fee): gain is zero,
	// nothing to measure.
	noSpread := secondChanceUnmeasuredTarget()
	noSpread.PeerFeeRatePpm = 400
	plan = secondChancePlan(t, cfg, noSpread, 600)
	for _, candidate := range plan.Candidates {
		if candidate.SecondChanceOnly {
			t.Fatalf("no spread must not get a second chance: %+v", candidate)
		}
	}
}

func TestExecuteSovereignSecondChanceRunsOnlyOnExplorationSlot(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	cfg := secondChanceConfig()
	cfg.BudgetUnlimited = true
	cfg.SovereignMaxJobsPerCycle = 1
	cfg.SovereignMinExpectedProfitSat = 5
	channel := secondChanceUnmeasuredTarget()
	run := func(exploration bool) RebalanceSovereignDecision {
		t.Helper()
		svc := NewRebalanceService(nil, nil, nil)
		plan := rebalanceAutoScanCandidatePlan{ReferenceCostPpm: 600, Candidates: []rebalanceTarget{{
			Channel:         channel,
			ExpectedGainSat: 270, EstimatedCostSat: 304, BudgetCostSat: 304,
			ExpectedROI: 0.89, ExpectedROIValid: true,
			SecondChanceOnly: true, ExplorationSlot: exploration,
		}}}
		result := svc.executeSovereignAutopilot(context.Background(), cfg, nil, plan, now, false)
		if len(result.Decisions) != 1 {
			t.Fatalf("missing decision: %+v", result)
		}
		return result.Decisions[0]
	}

	held := run(false)
	if held.Selected || held.Reason != "roi_guardrail" || held.SecondChance {
		t.Fatalf("without the exploration slot the guardrail must hold: %+v", held)
	}

	chance := run(true)
	if !chance.Selected || chance.Reason != "would_queue" || !chance.SecondChance {
		t.Fatalf("the exploration slot must buy the second chance: %+v", chance)
	}
	// One lot at the operator's start amount, not the 950k deficit chunk.
	if chance.AmountSat != cfg.MinAmountSat {
		t.Fatalf("second chance must be one start-amount lot, got %d", chance.AmountSat)
	}
	if chance.ExpectedProfitSat >= 0 {
		t.Fatalf("the test target must carry a negative estimate, got %d", chance.ExpectedProfitSat)
	}
	// Still bought under the fee cap: the budget never exceeds econ_ratio of
	// the outgoing fee.
	if maxBudget := int64(float64(chance.AmountSat) * float64(channel.OutgoingFeePpm) * cfg.EconRatio / 1_000_000); chance.BudgetCostSat > maxBudget+1 {
		t.Fatalf("second chance above the fee cap: budget %d max %d", chance.BudgetCostSat, maxBudget)
	}
}

// A normal exploration candidate (not held by the guardrail) keeps the
// profit >= 0 rule.
func TestExecuteSovereignExplorationStillRefusesEstimatedLoss(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	cfg := secondChanceConfig()
	cfg.BudgetUnlimited = true
	cfg.SovereignMaxJobsPerCycle = 1
	cfg.ROIMin = 0
	svc := NewRebalanceService(nil, nil, nil)
	plan := rebalanceAutoScanCandidatePlan{ReferenceCostPpm: 600, Candidates: []rebalanceTarget{{
		Channel:         secondChanceUnmeasuredTarget(),
		ExpectedGainSat: 270, EstimatedCostSat: 304, BudgetCostSat: 304,
		ExplorationSlot: true,
	}}}
	result := svc.executeSovereignAutopilot(context.Background(), cfg, nil, plan, now, false)
	if len(result.Decisions) != 1 || result.Decisions[0].Selected || result.Decisions[0].Reason != "expected_profit_below_min" {
		t.Fatalf("plain exploration must not buy an estimated loss: %+v", result.Decisions)
	}
}
