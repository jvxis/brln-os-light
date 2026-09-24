package server

import (
	"context"
	"testing"
	"time"
)

func TestSovereignUnsoldInventoryAccumulatesActualPartialBatches(t *testing.T) {
	cfg := defaultRebalanceConfig()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	var lots []rebalanceAttributionLot
	for i, status := range []string{"succeeded", "partial", "failed"} {
		// Historical jobs may have persisted multi-million-sat deficits.
		// All actual successful attempts count, irrespective of job fill ratio.
		lots = append(lots, rebalanceAttributionLot{JobID: int64(i + 1), TargetChannelID: 1,
			Status: status, TriggerReason: rebalanceSovereignReason,
			CompletedAt: now.Add(-time.Duration(3-i) * time.Hour), SentSat: 300_000, FeePaidSat: 150})
	}
	stats := buildSovereignUnsoldInventory(lots, nil, cfg, now)
	got := stats[1]
	if got.SentSat != 900_000 || got.TargetAmountSat != 900_000 || got.FeePaidSat != 450 {
		t.Fatalf("expected all three actual purchases, got %+v", got)
	}
	if !got.CompletedAt.Equal(lots[0].CompletedAt) || !got.LastPaidAt.Equal(lots[2].CompletedAt) {
		t.Fatalf("expected independent oldest-stock and last-purchase clocks, got %+v", got)
	}
	if amount, reason := sovereignUnsoldInventoryAllowance(got, cfg, now, true, 300_000); amount != 0 || reason != sovereignUnsoldPaidLiquidityReason {
		t.Fatalf("fresh accumulated stock must also pace exploration: %d %s", amount, reason)
	}
}

func TestSovereignUnsoldInventoryFIFORecoveryIsPerLot(t *testing.T) {
	cfg := defaultRebalanceConfig()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	lots := []rebalanceAttributionLot{
		{JobID: 1, TargetChannelID: 1, TriggerReason: rebalanceSovereignReason, CompletedAt: now.Add(-6 * time.Hour), SentSat: 50_000, FeePaidSat: 100},
		{JobID: 2, TargetChannelID: 1, TriggerReason: rebalanceSovereignReason, CompletedAt: now.Add(-5 * time.Hour), SentSat: 300_000, FeePaidSat: 300},
	}
	for _, tc := range []struct {
		name                                        string
		amount, fee, wantSent, wantForward, wantFee int64
	}{
		{"small old lot reaches sale threshold", 5_000, 0, 300_000, 0, 0},
		{"small old lot reaches payback threshold", 100, 25_000, 300_000, 0, 0},
		{"one forward spans both lots only once", 51_000, 51_000, 300_000, 1_000, 1_000},
		{"material FIFO sale releases both lots", 80_000, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forwards := []rebalanceAttributionForward{{ID: 1, TargetChannelID: 1, OccurredAt: now.Add(-time.Hour), AmountSat: tc.amount, FeeMsat: tc.fee}}
			got := buildSovereignUnsoldInventory(lots, forwards, cfg, now)[1]
			if got.SentSat != tc.wantSent || got.ForwardAmountSat != tc.wantForward || got.ForwardFeeMsat != tc.wantFee {
				t.Fatalf("unexpected FIFO cohort: %+v", got)
			}
		})
	}
}

func TestSovereignUnsoldInventoryOlderAndManualLotsConsumeSales(t *testing.T) {
	cfg := defaultRebalanceConfig()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	window := sovereignUnsoldInventoryWindow(cfg)
	for _, tc := range []struct {
		name, trigger string
		age           time.Duration
	}{
		{"older context", rebalanceSovereignReason, window + time.Hour},
		{"manual purchase", "manual", 6 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldAt := now.Add(-tc.age)
			lots := []rebalanceAttributionLot{
				{JobID: 1, TargetChannelID: 1, TriggerReason: tc.trigger, CompletedAt: oldAt, SentSat: 300_000, FeePaidSat: 100},
				{JobID: 2, TargetChannelID: 1, TriggerReason: rebalanceSovereignReason, CompletedAt: now.Add(-5 * time.Hour), SentSat: 300_000, FeePaidSat: 100},
			}
			forwards := []rebalanceAttributionForward{
				{ID: 1, TargetChannelID: 1, OccurredAt: now.Add(-4 * time.Hour), AmountSat: 250_000, FeeMsat: 100_000},
				{ID: 2, TargetChannelID: 2, OccurredAt: now.Add(-3 * time.Hour), AmountSat: 900_000, FeeMsat: 900_000},
			}
			got := buildSovereignUnsoldInventory(lots, forwards, cfg, now)[1]
			if got.SentSat != 300_000 || got.ForwardAmountSat != 0 || !got.CompletedAt.Equal(lots[1].CompletedAt) {
				t.Fatalf("old/manual lot or other target must not clear current inventory: %+v", got)
			}
		})
	}
}

func TestSovereignUnsoldInventoryBoundariesAndFreePurchase(t *testing.T) {
	cfg := defaultRebalanceConfig()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	lots := []rebalanceAttributionLot{
		{JobID: 1, TargetChannelID: 1, TriggerReason: rebalanceSovereignReason, CompletedAt: now.Add(-sovereignUnsoldInventoryWindow(cfg)), SentSat: 300_000},
		{JobID: 2, TargetChannelID: 1, TriggerReason: rebalanceSovereignReason, CompletedAt: now.Add(time.Hour), SentSat: 300_000},
		{JobID: 3, TargetChannelID: 2, TriggerReason: "manual", CompletedAt: now.Add(-time.Hour), SentSat: 300_000},
		{JobID: 4, TargetChannelID: 3, TriggerReason: rebalanceSovereignReason, CompletedAt: now.Add(-time.Hour), SentSat: 0},
		{JobID: 5, TargetChannelID: 4, TriggerReason: rebalanceSovereignReason, CompletedAt: now.Add(-time.Hour), SentSat: 300_000},
	}
	forwards := []rebalanceAttributionForward{{ID: 1, TargetChannelID: 4, OccurredAt: now.Add(time.Minute), AmountSat: 300_000}}
	stats := buildSovereignUnsoldInventory(lots, forwards, cfg, now)
	if len(stats) != 1 || stats[4].SentSat != 300_000 || stats[4].ForwardAmountSat != 0 {
		t.Fatalf("exclude expired/future/empty/manual lots; zero-fee inventory still needs sales: %+v", stats)
	}
}

func TestSovereignUnsoldInventoryAllowance(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.MinSplitEnabled = true
	cfg.MinExecuteSat = 20_000
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name           string
		oldest, latest time.Duration
		exploration    bool
		amount, want   int64
		blocked        bool
	}{
		{"fresh normal", time.Minute, time.Minute, false, 300_000, 0, true},
		{"fresh exploration", 40 * time.Minute, 40 * time.Minute, true, 300_000, 0, true},
		{"new purchase cannot reset oldest age", 80 * time.Hour, time.Hour, true, 300_000, 0, true},
		{"exact observation boundary", 4 * time.Hour, 4 * time.Hour, true, 300_000, 50_000, false},
		{"regular soft penalty period", 5 * time.Hour, 5 * time.Hour, false, 300_000, 300_000, false},
		{"severe old normal stock", 80 * time.Hour, 5 * time.Hour, false, 300_000, 0, true},
		{"severe old exploration remains possible", 80 * time.Hour, 5 * time.Hour, true, 300_000, 50_000, false},
		{"respect operator minimum", 5 * time.Hour, 5 * time.Hour, true, 100_000, 50_000, false},
		{"never increase deficit", 5 * time.Hour, 5 * time.Hour, true, 10_000, 10_000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stat := sovereignUnsoldLiquidityStat{CompletedAt: now.Add(-tc.oldest), LastPaidAt: now.Add(-tc.latest), SentSat: 900_000, TargetAmountSat: 900_000, FeePaidSat: 450}
			amount, reason := sovereignUnsoldInventoryAllowance(stat, cfg, now, tc.exploration, tc.amount)
			if amount != tc.want || (reason == sovereignUnsoldPaidLiquidityReason) != tc.blocked {
				t.Fatalf("got amount=%d reason=%q; want %d blocked=%v", amount, reason, tc.want, tc.blocked)
			}
		})
	}
	if amount, reason := sovereignUnsoldInventoryAllowance(sovereignUnsoldLiquidityStat{}, cfg, now, true, 300_000); amount != 300_000 || reason != "" {
		t.Fatalf("first exploration purchase must stay unchanged: %d %s", amount, reason)
	}
}

func TestSovereignInventoryProbeOperatorMinimum(t *testing.T) {
	now := time.Now()
	stat := sovereignUnsoldLiquidityStat{CompletedAt: now.Add(-5 * time.Hour), SentSat: 900_000, TargetAmountSat: 900_000, FeePaidSat: 450}
	for _, tc := range []struct {
		name                           string
		minimum, execute, amount, want int64
		split                          bool
	}{
		{"Friendspool", 50_000, 10_000, 300_000, 50_000, true},
		{"BRLN", 1_000, 1_000, 100_000, 1_000, true},
		{"no fixed ten percent", 80_000, 10_000, 300_000, 80_000, true},
		{"execution floor higher", 5_000, 10_000, 300_000, 10_000, true},
		{"unset start", 0, 10_000, 300_000, 10_000, true},
		{"limited by remaining batch", 50_000, 10_000, 20_000, 20_000, true},
		{"split off", 50_000, 10_000, 300_000, 50_000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultRebalanceConfig()
			cfg.MinAmountSat, cfg.MinExecuteSat, cfg.MinSplitEnabled = tc.minimum, tc.execute, tc.split
			got, reason := sovereignUnsoldInventoryAllowance(stat, cfg, now, true, tc.amount)
			if got != tc.want || reason != "" {
				t.Fatalf("amount=%d reason=%s, want=%d", got, reason, tc.want)
			}
		})
	}
}

func TestExecuteSovereignUnsoldInventoryProbeEconomics(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		age        time.Duration
		peerPPM    int64
		roi        float64
		wantAmount int64
		wantReason string
	}{
		{"pace fresh exploration", time.Hour, 0, 1, 300_000, sovereignUnsoldPaidLiquidityReason},
		{"bounded profitable probe", 5 * time.Hour, 0, 1, 50_000, "would_queue"},
		{"probe still respects ROI", 5 * time.Hour, 0, 10, 50_000, "roi_guardrail"},
		{"probe cannot buy guaranteed loss", 5 * time.Hour, 990, 0, 50_000, "expected_profit_below_min"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewRebalanceService(nil, nil, nil)
			cfg := defaultRebalanceConfig()
			cfg.BudgetUnlimited = true
			cfg.SovereignMaxJobsPerCycle = 1
			cfg.MaxAmountSat = 300_000
			cfg.MinSplitEnabled = true
			cfg.MinExecuteSat = 20_000
			cfg.GainModelVersion = 2
			cfg.FeeLimitPpm = 400
			cfg.ROIMin = tc.roi
			cfg.SovereignMinExpectedProfitSat = 0
			plan := rebalanceAutoScanCandidatePlan{Candidates: []rebalanceTarget{{
				Channel:         RebalanceChannel{ChannelID: 1, ChannelPoint: "test:0", TargetAmountSat: 2_000_000, OutgoingFeePpm: 1_000, PeerFeeRatePpm: tc.peerPPM},
				ExpectedGainSat: 300, EstimatedCostSat: 120, BudgetCostSat: 120,
				ExpectedROI: 2.5, ExpectedROIValid: true, ExplorationSlot: true,
				UnsoldLiquidity: sovereignUnsoldLiquidityStat{CompletedAt: now.Add(-tc.age), LastPaidAt: now.Add(-tc.age), SentSat: 900_000, TargetAmountSat: 900_000, FeePaidSat: 450},
			}}}
			result := svc.executeSovereignAutopilot(context.Background(), cfg, nil, plan, now, false)
			if len(result.Decisions) != 1 {
				t.Fatalf("missing decision: %+v", result)
			}
			d := result.Decisions[0]
			if d.AmountSat != tc.wantAmount || d.Reason != tc.wantReason || d.Selected != (tc.wantReason == "would_queue") {
				t.Fatalf("unexpected decision: %+v", d)
			}
			if d.UnsoldPaidSat != 900_000 || d.UnsoldOldestAt == "" || d.UnsoldLastPaidAt == "" || d.InventoryProbe != (tc.wantAmount == 50_000) {
				t.Fatalf("missing inventory evidence: %+v", d)
			}
			if d.InventoryProbe && (d.ExpectedGainSat != estimateTargetGainForConfig(cfg, plan.Candidates[0].Channel, d.AmountSat) || d.BudgetCostSat >= 120) {
				t.Fatalf("probe kept full-batch economics: %+v", d)
			}
		})
	}
}

func TestExecuteSovereignUnsoldInventoryUnavailableFailsClosed(t *testing.T) {
	svc := NewRebalanceService(nil, nil, nil)
	cfg := defaultRebalanceConfig()
	cfg.BudgetUnlimited = true
	cfg.SovereignMaxJobsPerCycle = 1
	for _, exploration := range []bool{false, true} {
		plan := rebalanceAutoScanCandidatePlan{Candidates: []rebalanceTarget{{
			Channel:         RebalanceChannel{ChannelID: 1, TargetAmountSat: 300_000},
			ExpectedGainSat: 300, EstimatedCostSat: 120, BudgetCostSat: 120,
			ExplorationSlot: exploration, UnsoldLiquidity: sovereignUnsoldLiquidityStat{Unavailable: true},
		}}}
		result := svc.executeSovereignAutopilot(context.Background(), cfg, nil, plan, time.Now(), false)
		if result.Selected != 0 || len(result.Decisions) != 1 || result.Decisions[0].Reason != sovereignUnsoldInventoryUnavailableReason {
			t.Fatalf("unavailable inventory must not authorize another purchase: %+v", result)
		}
	}
}

func TestSovereignUnsoldInventoryPaybackRetainsMillisatoshiPrecision(t *testing.T) {
	cfg := defaultRebalanceConfig()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	lots := []rebalanceAttributionLot{{JobID: 1, TargetChannelID: 1, TriggerReason: rebalanceSovereignReason,
		CompletedAt: now.Add(-time.Hour), SentSat: 300_000, FeePaidSat: 1}}
	for _, feeMsat := range []int64{249, 250} {
		forwards := []rebalanceAttributionForward{{ID: 1, TargetChannelID: 1, OccurredAt: now, AmountSat: 100, FeeMsat: feeMsat}}
		stats := buildSovereignUnsoldInventory(lots, forwards, cfg, now)
		if (len(stats) == 0) != (feeMsat == 250) {
			t.Fatalf("25%% fee recovery must use msat precision, fee=%d stats=%+v", feeMsat, stats)
		}
	}
}

func TestExecuteSovereignUnsoldInventoryProbeRespectsBudget(t *testing.T) {
	svc := NewRebalanceService(nil, nil, nil) // no available automatic budget
	cfg := defaultRebalanceConfig()
	cfg.BudgetUnlimited = false
	cfg.SovereignMaxJobsPerCycle = 1
	cfg.MaxAmountSat = 300_000
	cfg.MinAmountSat = 20_000
	cfg.GainModelVersion = 2
	cfg.FeeLimitPpm = 400
	now := time.Now()
	plan := rebalanceAutoScanCandidatePlan{Candidates: []rebalanceTarget{{
		Channel:         RebalanceChannel{ChannelID: 1, TargetAmountSat: 2_000_000, OutgoingFeePpm: 1_000},
		ExpectedGainSat: 300, EstimatedCostSat: 120, BudgetCostSat: 120, ExplorationSlot: true,
		UnsoldLiquidity: sovereignUnsoldLiquidityStat{CompletedAt: now.Add(-5 * time.Hour), SentSat: 300_000, TargetAmountSat: 300_000, FeePaidSat: 120},
	}}}
	result := svc.executeSovereignAutopilot(context.Background(), cfg, nil, plan, now, false)
	if result.Selected != 0 || len(result.Decisions) != 1 || result.Decisions[0].Reason != "budget_below_min" || !result.Decisions[0].InventoryProbe {
		t.Fatalf("inventory probe must not bypass budget/execution minimum: %+v", result)
	}
	// An already selected operator-guaranteed slot remains part of the round,
	// even when Sovereign's independent inventory/budget gates select no jobs.
	result = includeGuaranteedRebalanceSlot(result, guaranteedRebalanceSlotResult{
		Queued: true, Decision: RebalanceSovereignDecision{ChannelID: 2, Selected: true, Reason: "guaranteed_slot_queued"},
	})
	if result.Selected != 1 || result.Decisions[0].Reason != "guaranteed_slot_queued" {
		t.Fatalf("inventory policy must not erase the operator's guaranteed slot: %+v", result)
	}
}
