package server

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func ptrInt64(v int64) *int64 { return &v }
func ptrInt(v int) *int       { return &v }
func ptrBool(v bool) *bool    { return &v }
func ptrFloat64(v float64) *float64 {
	return &v
}
func ptrString(v string) *string { return &v }

func TestDefaultRebalanceConfigStarterProfile(t *testing.T) {
	cfg := defaultRebalanceConfig()
	if cfg.AutoEnabled {
		t.Fatalf("expected auto mode disabled by default")
	}
	if cfg.SchedulerMode != rebalanceSchedulerModeRulesAuto {
		t.Fatalf("expected scheduler_mode default=%s, got %s", rebalanceSchedulerModeRulesAuto, cfg.SchedulerMode)
	}
	if cfg.SovereignCandidateScope != rebalanceSovereignScopeAutoAndManualRestart {
		t.Fatalf("expected sovereign_candidate_scope default=%s, got %s", rebalanceSovereignScopeAutoAndManualRestart, cfg.SovereignCandidateScope)
	}
	if cfg.SovereignMaxJobsPerCycle != 2 {
		t.Fatalf("expected sovereign_max_jobs_per_cycle default=2, got %d", cfg.SovereignMaxJobsPerCycle)
	}
	if cfg.SovereignMinExpectedProfitSat != 0 {
		t.Fatalf("expected sovereign_min_expected_profit_sat default=0, got %d", cfg.SovereignMinExpectedProfitSat)
	}
	if cfg.ScanIntervalSec != 900 {
		t.Fatalf("expected scan_interval_sec default=900, got %d", cfg.ScanIntervalSec)
	}
	if cfg.DeadbandPct != 5 {
		t.Fatalf("expected deadband default=5, got %f", cfg.DeadbandPct)
	}
	if cfg.SourceMinLocalPct != 35 {
		t.Fatalf("expected source_min_local_pct default=35, got %f", cfg.SourceMinLocalPct)
	}
	if cfg.DailyBudgetPct != 25 {
		t.Fatalf("expected daily_budget_pct default=25, got %f", cfg.DailyBudgetPct)
	}
	if cfg.BudgetMode != rebalanceBudgetModeHybridRevenue {
		t.Fatalf("expected budget_mode default=%s, got %s", rebalanceBudgetModeHybridRevenue, cfg.BudgetMode)
	}
	if cfg.BudgetUnlimited {
		t.Fatalf("expected budget_unlimited default=false")
	}
	if !cfg.BudgetAutoOnly {
		t.Fatalf("expected budget_auto_only default=true")
	}
	if cfg.MinAmountSat != 50000 {
		t.Fatalf("expected min_amount_sat default=50000, got %d", cfg.MinAmountSat)
	}
	if !cfg.MinSplitEnabled {
		t.Fatalf("expected split mode enabled by default")
	}
	if cfg.MinProbeSat != 5000 {
		t.Fatalf("expected default min_probe_sat=5000, got %d", cfg.MinProbeSat)
	}
	if cfg.MinExecuteSat != 10000 {
		t.Fatalf("expected default min_execute_sat=10000, got %d", cfg.MinExecuteSat)
	}
	if effectiveMinExecuteSat(cfg) != cfg.MinExecuteSat {
		t.Fatalf("expected effective execute min to use split execute min")
	}
	if effectiveMinProbeSat(cfg) != cfg.MinProbeSat {
		t.Fatalf("expected effective probe min to use split probe min")
	}
	if !cfg.MppEnabled {
		t.Fatalf("expected MSPR enabled by default")
	}
	if !cfg.MppAutoOnly {
		t.Fatalf("expected MSPR auto-only by default")
	}
	if cfg.MppMaxShards != 6 {
		t.Fatalf("expected mpp_max_shards default=6, got %d", cfg.MppMaxShards)
	}
	if cfg.MppParallelism != 3 {
		t.Fatalf("expected mpp_parallelism default=3, got %d", cfg.MppParallelism)
	}
	if cfg.MppMinShardSat != 10000 {
		t.Fatalf("expected mpp_min_shard_sat default=10000, got %d", cfg.MppMinShardSat)
	}
	if cfg.MppRoundTimeoutSec != 35 {
		t.Fatalf("expected mpp_round_timeout_sec default=35, got %d", cfg.MppRoundTimeoutSec)
	}
	if cfg.FeeLadderSteps != 1 {
		t.Fatalf("expected fee_ladder_steps default=1, got %d", cfg.FeeLadderSteps)
	}
	if cfg.AmountProbeSteps != 6 {
		t.Fatalf("expected amount_probe_steps default=6, got %d", cfg.AmountProbeSteps)
	}
	if cfg.AttemptTimeoutSec != 45 {
		t.Fatalf("expected attempt_timeout_sec default=45, got %d", cfg.AttemptTimeoutSec)
	}
	if cfg.UnlockDays != 7 {
		t.Fatalf("expected unlock_days default=7, got %d", cfg.UnlockDays)
	}
	if cfg.RebalanceCostFloorPpm != 250 {
		t.Fatalf("expected rebalance_cost_floor_ppm default=250, got %d", cfg.RebalanceCostFloorPpm)
	}
	if cfg.SourceMinPaybackProgress != 0.95 {
		t.Fatalf("expected source_min_payback_progress default=0.95, got %f", cfg.SourceMinPaybackProgress)
	}
	if cfg.GainModelVersion != 1 {
		t.Fatalf("expected gain_model_version default=1, got %d", cfg.GainModelVersion)
	}
	if cfg.VelocityWeight != 0.7 {
		t.Fatalf("expected velocity_weight default=0.7, got %f", cfg.VelocityWeight)
	}
	if !cfg.FreshPaidLiquidityLockEnabled {
		t.Fatalf("expected fresh_paid_liquidity_lock_enabled default=true")
	}
	if cfg.FreshPaidLiquidityLockHours != 6 {
		t.Fatalf("expected fresh_paid_liquidity_lock_hours default=6, got %d", cfg.FreshPaidLiquidityLockHours)
	}
	if !cfg.DelegatedFastPathStrictPayback {
		t.Fatalf("expected delegated_fast_path_strict_payback default=true")
	}
}

func TestNormalizeRebalanceConfigClampsRebalanceCostFloor(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.RebalanceCostFloorPpm = -100
	got := normalizeRebalanceConfig(cfg)
	if got.RebalanceCostFloorPpm != 0 {
		t.Fatalf("expected RebalanceCostFloorPpm clamped to 0, got %d", got.RebalanceCostFloorPpm)
	}
}

func TestNormalizeRebalanceConfigClampsSourceMinPaybackProgress(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.SourceMinPaybackProgress = -0.5
	got := normalizeRebalanceConfig(cfg)
	if got.SourceMinPaybackProgress != 0 {
		t.Fatalf("expected SourceMinPaybackProgress clamped to 0, got %f", got.SourceMinPaybackProgress)
	}
}

// TestComputeEffectiveProtectedFreshChannel: a channel with no significant
// historical paid_cost has zero protection regardless of payback progress.
func TestComputeEffectiveProtectedFreshChannel(t *testing.T) {
	cfg := defaultRebalanceConfig()
	got := computeEffectiveProtected(0, 0, 0, cfg, time.Time{}, false)
	if got != 0 {
		t.Fatalf("expected 0 protection for fresh channel (no liquidity), got %d", got)
	}
	got = computeEffectiveProtected(1_000_000, 100, 0, cfg, time.Time{}, false)
	if got != 0 {
		t.Fatalf("expected 0 protection when paid_cost <= 500 sat, got %d", got)
	}
}

// TestComputeEffectiveProtectedForwardDriven: a channel with small paid_liquidity
// (most of its local came from forwards) has only the rebalanced portion locked.
// Mirrors the Boltz case: 4.94M local but 200k from rebalance, payback=0.
func TestComputeEffectiveProtectedForwardDriven(t *testing.T) {
	cfg := defaultRebalanceConfig()
	got := computeEffectiveProtected(200_000, 1_000, 0, cfg, time.Time{}, false)
	if got != 200_000 {
		t.Fatalf("expected full lock of paid_liquidity (200k) at payback=0, got %d", got)
	}
}

// TestComputeEffectiveProtectedSinkChannel: a pure sink (large paid_liquidity,
// payback=0) has full lock. Mirrors the 1sats.com case that motivated Policy C.
func TestComputeEffectiveProtectedSinkChannel(t *testing.T) {
	cfg := defaultRebalanceConfig()
	got := computeEffectiveProtected(5_000_000, 50_000, 0, cfg, time.Time{}, false)
	if got != 5_000_000 {
		t.Fatalf("expected full lock for sink with payback=0, got %d", got)
	}
}

// TestComputeEffectiveProtectedRouterFullPayback: a router that recouped its
// rebalance cost via forwards has zero protection (free to be source again).
func TestComputeEffectiveProtectedRouterFullPayback(t *testing.T) {
	cfg := defaultRebalanceConfig()
	// payback = 1.25 (revenue exceeds cost) > threshold 0.95
	got := computeEffectiveProtected(2_000_000, 4_000, 1.25, cfg, time.Time{}, false)
	if got != 0 {
		t.Fatalf("expected 0 protection when payback >= threshold, got %d", got)
	}
	// At threshold exactly
	got = computeEffectiveProtected(2_000_000, 4_000, 0.95, cfg, time.Time{}, false)
	if got != 0 {
		t.Fatalf("expected 0 protection at payback == threshold, got %d", got)
	}
}

// TestComputeEffectiveProtectedRouterPartialPayback: with payback halfway to
// threshold, protection is roughly half of paid_liquidity.
func TestComputeEffectiveProtectedRouterPartialPayback(t *testing.T) {
	cfg := defaultRebalanceConfig()
	// payback = 0.5, threshold = 0.95 → unrecouped = 1 - 0.5/0.95 ≈ 0.4737
	got := computeEffectiveProtected(2_000_000, 10_000, 0.5, cfg, time.Time{}, false)
	expected := int64(math.Round(2_000_000 * (1 - 0.5/0.95)))
	if got != expected {
		t.Fatalf("expected proportional protection %d at payback=0.5, got %d", expected, got)
	}
}

// TestComputeEffectiveProtectedTimeUnlock: after UnlockDays without a fresh
// rebalance, time-mode releases all protection regardless of payback.
func TestComputeEffectiveProtectedTimeUnlock(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.UnlockDays = 7
	long := time.Now().Add(-30 * 24 * time.Hour)
	got := computeEffectiveProtected(2_000_000, 10_000, 0, cfg, long, false)
	if got != 0 {
		t.Fatalf("expected 0 protection after time-unlock, got %d", got)
	}
	// Within unlock window: still locked
	recent := time.Now().Add(-2 * 24 * time.Hour)
	got = computeEffectiveProtected(2_000_000, 10_000, 0, cfg, recent, false)
	if got != 2_000_000 {
		t.Fatalf("expected full lock within unlock window, got %d", got)
	}
}

// TestComputeEffectiveProtectedCriticalRelease: critical mode releases
// CriticalReleasePct of the still-protected amount.
func TestComputeEffectiveProtectedCriticalRelease(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.CriticalReleasePct = 20 // release 20% on critical
	got := computeEffectiveProtected(1_000_000, 10_000, 0, cfg, time.Time{}, true)
	expected := int64(1_000_000 - 1_000_000*0.20)
	if got != expected {
		t.Fatalf("expected %d after critical release of 20%%, got %d", expected, got)
	}
}

// TestComputeEffectiveProtectedDisabledPaybackMode: when paybackModePayback is
// off, payback progress is ignored and protection stays full.
func TestComputeEffectiveProtectedDisabledPaybackMode(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.PaybackModeFlags = 0 // all flags off
	got := computeEffectiveProtected(2_000_000, 10_000, 1.0, cfg, time.Time{}, false)
	if got != 2_000_000 {
		t.Fatalf("expected full lock when paybackModePayback off, got %d", got)
	}
}

func TestApplyFreshPaidLiquidityLock(t *testing.T) {
	cfg := defaultRebalanceConfig()
	got := applyFreshPaidLiquidityLock(0, 300_000, 1_000_000, cfg)
	if got != 300_000 {
		t.Fatalf("expected fresh lock to protect 300k, got %d", got)
	}
	got = applyFreshPaidLiquidityLock(600_000, 300_000, 1_000_000, cfg)
	if got != 600_000 {
		t.Fatalf("expected existing stronger payback lock to remain 600k, got %d", got)
	}
	got = applyFreshPaidLiquidityLock(0, 1_200_000, 1_000_000, cfg)
	if got != 1_000_000 {
		t.Fatalf("expected fresh lock capped at paid liquidity, got %d", got)
	}
	cfg.FreshPaidLiquidityLockEnabled = false
	got = applyFreshPaidLiquidityLock(0, 300_000, 1_000_000, cfg)
	if got != 0 {
		t.Fatalf("expected disabled fresh lock to leave protection unchanged, got %d", got)
	}
}

func TestFreshPaidLiquidityTrackerExpiresAndConsumes(t *testing.T) {
	base := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	tracker := newFreshPaidLiquidityTracker(6 * time.Hour)
	tracker.add(1, 1_000_000, base)
	tracker.consume(1, 250_000, base.Add(2*time.Hour))
	if got := tracker.total(1, base.Add(3*time.Hour)); got != 750_000 {
		t.Fatalf("expected 750k fresh liquidity after forward, got %d", got)
	}
	tracker.add(1, 500_000, base.Add(5*time.Hour))
	if got := tracker.total(1, base.Add(6*time.Hour-time.Second)); got != 1_250_000 {
		t.Fatalf("expected both lots inside fresh window, got %d", got)
	}
	if got := tracker.total(1, base.Add(6*time.Hour)); got != 500_000 {
		t.Fatalf("expected first lot to expire exactly at 6h, got %d", got)
	}
}

func TestNormalizeRebalanceConfigClampsGainModelAndVelocityWeight(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.GainModelVersion = 99
	cfg.VelocityWeight = 1.7
	got := normalizeRebalanceConfig(cfg)
	if got.GainModelVersion != 2 {
		t.Fatalf("expected GainModelVersion clamped to 2, got %d", got.GainModelVersion)
	}
	if got.VelocityWeight != 1 {
		t.Fatalf("expected VelocityWeight clamped to 1, got %f", got.VelocityWeight)
	}

	cfg.GainModelVersion = -1
	cfg.VelocityWeight = -0.1
	got = normalizeRebalanceConfig(cfg)
	if got.GainModelVersion != 1 {
		t.Fatalf("expected GainModelVersion fallback to 1, got %d", got.GainModelVersion)
	}
	if got.VelocityWeight != 0 {
		t.Fatalf("expected VelocityWeight clamped to 0, got %f", got.VelocityWeight)
	}
}

func TestNormalizeRebalanceConfigDefaultsMppMinShardFromExecuteMin(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.MinSplitEnabled = true
	cfg.MinExecuteSat = 25000
	cfg.MppMinShardSat = 0

	got := normalizeRebalanceConfig(cfg)
	if got.MppMinShardSat != cfg.MinExecuteSat {
		t.Fatalf("expected MppMinShardSat fallback=%d, got %d", cfg.MinExecuteSat, got.MppMinShardSat)
	}
}

func TestManualRestartWatchEligibilityHonorsPerChannelCostGateBypass(t *testing.T) {
	cfg := defaultRebalanceConfig()
	snapshot := RebalanceChannel{
		EligibleAsTarget:       false,
		EligibleAsManualTarget: true,
	}
	ok, reason := manualRestartWatchEligibility(snapshot, cfg)
	if ok || reason != "target_not_eligible" {
		t.Fatalf("expected cost gate to block without per-channel bypass, got ok=%v reason=%q", ok, reason)
	}

	snapshot.EligibleAsTarget = true
	ok, reason = manualRestartWatchEligibility(snapshot, cfg)
	if !ok || reason != "" {
		t.Fatalf("expected per-channel cost gate bypass to allow manual restart watch, got ok=%v reason=%q", ok, reason)
	}

	cfg.ROIMin = 1.4
	snapshot.ROIEstimateValid = true
	snapshot.ROIEstimate = 1.1
	ok, reason = manualRestartWatchEligibility(snapshot, cfg)
	if ok || reason != "roi_guardrail" {
		t.Fatalf("expected ROI guardrail to block manual restart watch, got ok=%v reason=%q", ok, reason)
	}
}

func TestValidateRebalanceConfigPayloadAllowsValidPartialPayload(t *testing.T) {
	payload := rebalanceConfigPayload{
		SchedulerMode:                 ptrString(rebalanceSchedulerModeSovereignShadow),
		SovereignCandidateScope:       ptrString(rebalanceSovereignScopeAutoAndManualRestart),
		SovereignMaxJobsPerCycle:      ptrInt(2),
		SovereignMinExpectedProfitSat: ptrInt64(0),
		DeadbandPct:                   ptrFloat64(4),
		BudgetMode:                    ptrString(rebalanceBudgetModeHybridRevenue),
		ManualReserveMode:             ptrString(rebalanceManualReserveModePct),
		ManualReserveValue:            ptrFloat64(25),
		MppMaxShards:                  ptrInt(6),
		MppParallelism:                ptrInt(3),
		GainModelVersion:              ptrInt(2),
		VelocityWeight:                ptrFloat64(0.4),
		AutofeeSettlingMultiplier:     ptrFloat64(0.5),
		AutofeeSettlingWindowSec:      ptrInt64(7200),
		CriticalMinAvailableSats:      ptrInt64(0),
		SourceMinPaybackProgress:      ptrFloat64(0.95),
		RebalanceCostFloorPpm:         ptrInt64(250),
		MissionControlHalfLifeSec:     ptrInt64(3600),
		FreshPaidLiquidityLockHours:   ptrInt(12),
	}

	if err := validateRebalanceConfigPayload(payload); err != nil {
		t.Fatalf("expected valid payload, got %v", err)
	}
}

func TestValidateRebalanceConfigPayloadRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name    string
		payload rebalanceConfigPayload
	}{
		{name: "deadband below range", payload: rebalanceConfigPayload{DeadbandPct: ptrFloat64(-1)}},
		{name: "mpp shards above range", payload: rebalanceConfigPayload{MppMaxShards: ptrInt(21)}},
		{name: "velocity above range", payload: rebalanceConfigPayload{VelocityWeight: ptrFloat64(1.2)}},
		{name: "fresh lock hours below range", payload: rebalanceConfigPayload{FreshPaidLiquidityLockHours: ptrInt(0)}},
		{name: "invalid scheduler mode", payload: rebalanceConfigPayload{SchedulerMode: ptrString("legacy")}},
		{name: "invalid sovereign scope", payload: rebalanceConfigPayload{SovereignCandidateScope: ptrString("manual_only")}},
		{name: "sovereign jobs below range", payload: rebalanceConfigPayload{SovereignMaxJobsPerCycle: ptrInt(0)}},
		{name: "sovereign min profit below range", payload: rebalanceConfigPayload{SovereignMinExpectedProfitSat: ptrInt64(-1)}},
		{name: "invalid budget mode", payload: rebalanceConfigPayload{BudgetMode: ptrString("invalid")}},
		{name: "manual reserve pct above range", payload: rebalanceConfigPayload{
			ManualReserveMode:  ptrString(rebalanceManualReserveModePct),
			ManualReserveValue: ptrFloat64(101),
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateRebalanceConfigPayload(tc.payload); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestEstimateTimeToPaybackHours(t *testing.T) {
	hours, valid := estimateTimeToPaybackHours(800, 1000, 840)
	if !valid {
		t.Fatalf("expected time-to-payback estimate to be valid")
	}
	if hours != 40 {
		t.Fatalf("expected 40 hours to payback, got %.2f", hours)
	}

	hours, valid = estimateTimeToPaybackHours(1000, 1000, 840)
	if !valid || hours != 0 {
		t.Fatalf("expected paid-back channel to return 0 valid hours, got %.2f valid=%v", hours, valid)
	}

	if _, valid = estimateTimeToPaybackHours(100, 1000, 0); valid {
		t.Fatalf("expected invalid estimate without recent revenue")
	}
}

func TestLimitRebalanceSkipDetailsCopiesAndCaps(t *testing.T) {
	details := []RebalanceSkipDetail{
		{ChannelID: 1, Reason: "a"},
		{ChannelID: 2, Reason: "b"},
		{ChannelID: 3, Reason: "c"},
	}
	got := limitRebalanceSkipDetails(details, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 details, got %d", len(got))
	}
	details[0].Reason = "mutated"
	if got[0].Reason != "a" {
		t.Fatalf("expected capped details to be copied, got %q", got[0].Reason)
	}
}

func TestApplyRebalanceConfigPayloadOnlyTouchesProvidedFields(t *testing.T) {
	cfg := defaultRebalanceConfig()
	deadband := 0.0
	budgetAutoOnly := false
	minExecuteSat := int64(25_000)
	gainModelVersion := 2
	velocityWeight := 0.35
	freshPaidLiquidityLockHours := 12
	schedulerMode := rebalanceSchedulerModeSovereignShadow
	sovereignScope := rebalanceSovereignScopeAutoOnly
	sovereignMaxJobs := 3
	sovereignMinProfit := int64(42)
	cfg.DelegatedFastPathStrictPayback = true

	got := applyRebalanceConfigPayload(cfg, rebalanceConfigPayload{
		SchedulerMode:                  &schedulerMode,
		SovereignCandidateScope:        &sovereignScope,
		SovereignMaxJobsPerCycle:       &sovereignMaxJobs,
		SovereignMinExpectedProfitSat:  &sovereignMinProfit,
		DeadbandPct:                    &deadband,
		BudgetUnlimited:                ptrBool(true),
		BudgetAutoOnly:                 &budgetAutoOnly,
		MinExecuteSat:                  &minExecuteSat,
		GainModelVersion:               &gainModelVersion,
		VelocityWeight:                 &velocityWeight,
		FreshPaidLiquidityLockEnabled:  ptrBool(false),
		FreshPaidLiquidityLockHours:    &freshPaidLiquidityLockHours,
		DelegatedFastPathStrictPayback: ptrBool(false),
	})

	if got.DeadbandPct != 0 {
		t.Fatalf("expected explicit zero deadband to be applied, got %f", got.DeadbandPct)
	}
	if got.BudgetAutoOnly {
		t.Fatalf("expected explicit false budget_auto_only to be applied")
	}
	if !got.BudgetUnlimited {
		t.Fatalf("expected explicit true budget_unlimited to be applied")
	}
	if got.MinExecuteSat != minExecuteSat {
		t.Fatalf("expected min_execute_sat=%d, got %d", minExecuteSat, got.MinExecuteSat)
	}
	if got.GainModelVersion != gainModelVersion {
		t.Fatalf("expected gain_model_version=%d, got %d", gainModelVersion, got.GainModelVersion)
	}
	if got.VelocityWeight != velocityWeight {
		t.Fatalf("expected velocity_weight=%f, got %f", velocityWeight, got.VelocityWeight)
	}
	if got.FreshPaidLiquidityLockEnabled {
		t.Fatalf("expected explicit false fresh_paid_liquidity_lock_enabled to be applied")
	}
	if got.FreshPaidLiquidityLockHours != freshPaidLiquidityLockHours {
		t.Fatalf("expected fresh_paid_liquidity_lock_hours=%d, got %d", freshPaidLiquidityLockHours, got.FreshPaidLiquidityLockHours)
	}
	if got.DelegatedFastPathStrictPayback {
		t.Fatalf("expected explicit false delegated_fast_path_strict_payback to be applied")
	}
	if got.SchedulerMode != schedulerMode {
		t.Fatalf("expected scheduler_mode=%s, got %s", schedulerMode, got.SchedulerMode)
	}
	if got.SovereignCandidateScope != sovereignScope {
		t.Fatalf("expected sovereign_candidate_scope=%s, got %s", sovereignScope, got.SovereignCandidateScope)
	}
	if got.SovereignMaxJobsPerCycle != sovereignMaxJobs {
		t.Fatalf("expected sovereign_max_jobs_per_cycle=%d, got %d", sovereignMaxJobs, got.SovereignMaxJobsPerCycle)
	}
	if got.SovereignMinExpectedProfitSat != sovereignMinProfit {
		t.Fatalf("expected sovereign_min_expected_profit_sat=%d, got %d", sovereignMinProfit, got.SovereignMinExpectedProfitSat)
	}
	if got.ScanIntervalSec != cfg.ScanIntervalSec {
		t.Fatalf("expected omitted scan_interval_sec to remain %d, got %d", cfg.ScanIntervalSec, got.ScanIntervalSec)
	}
}

func TestAutoTargetCostGateRequiresSpreadAboveExpectedCost(t *testing.T) {
	if passesAutoTargetCostGate(channelSetting{}, 131, 88) {
		t.Fatalf("expected cost gate to block when effective spread is below expected cost")
	}
	if !passesAutoTargetCostGate(channelSetting{}, 131, 132) {
		t.Fatalf("expected cost gate to pass when effective spread is above expected cost")
	}
	if !passesAutoTargetCostGate(channelSetting{}, 0, 0) {
		t.Fatalf("expected zero expected cost to pass")
	}
}

func TestAutoTargetCostGateBypassAllowsBelowCostSpread(t *testing.T) {
	setting := channelSetting{AutoBypassCostGate: true}
	if !passesAutoTargetCostGate(setting, 131, 88) {
		t.Fatalf("expected auto cost gate bypass to allow below-cost effective spread")
	}
}

func TestApplyAutofeeSettlingPenaltyDampensRecentTargets(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	cfg := defaultRebalanceConfig()
	cfg.AutofeeSettlingWindowSec = 7200
	cfg.AutofeeSettlingMultiplier = 0.5

	candidates := []rebalanceTarget{
		{Channel: RebalanceChannel{ChannelID: 100}, Score: 1000},
		{Channel: RebalanceChannel{ChannelID: 101}, Score: 800},
		{Channel: RebalanceChannel{ChannelID: 102}, Score: 600, CooldownProbe: true},
	}
	adjustments := map[uint64]time.Time{
		100: now.Add(-30 * time.Minute), // inside window → dampen
		101: now.Add(-3 * time.Hour),    // outside window → keep
		102: now.Add(-10 * time.Minute), // probe → never dampened
	}
	dampened := applyAutofeeSettlingPenalty(candidates, adjustments, cfg, now)
	if dampened != 1 {
		t.Fatalf("expected 1 dampened candidate, got %d", dampened)
	}
	if candidates[0].Score != 500 || !candidates[0].AutofeeDampened {
		t.Fatalf("candidate 100: got score=%d dampened=%v, want 500/true", candidates[0].Score, candidates[0].AutofeeDampened)
	}
	if candidates[1].Score != 800 || candidates[1].AutofeeDampened {
		t.Fatalf("candidate 101 (outside window): got score=%d dampened=%v, want 800/false", candidates[1].Score, candidates[1].AutofeeDampened)
	}
	if candidates[2].Score != 600 || candidates[2].AutofeeDampened {
		t.Fatalf("candidate 102 (probe): got score=%d dampened=%v, want 600/false", candidates[2].Score, candidates[2].AutofeeDampened)
	}
}

func TestApplyAutofeeSettlingPenaltyNoOpWhenWindowZero(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	cfg := defaultRebalanceConfig()
	cfg.AutofeeSettlingWindowSec = 0
	cfg.AutofeeSettlingMultiplier = 0.5

	candidates := []rebalanceTarget{{Channel: RebalanceChannel{ChannelID: 100}, Score: 1000}}
	adjustments := map[uint64]time.Time{100: now.Add(-1 * time.Minute)}
	if dampened := applyAutofeeSettlingPenalty(candidates, adjustments, cfg, now); dampened != 0 {
		t.Fatalf("expected no dampening when window=0, got %d", dampened)
	}
	if candidates[0].Score != 1000 || candidates[0].AutofeeDampened {
		t.Fatalf("candidate untouched expected, got score=%d dampened=%v", candidates[0].Score, candidates[0].AutofeeDampened)
	}
}

func TestApplyAutofeeSettlingPenaltyNoOpWhenMultiplierIsOne(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	cfg := defaultRebalanceConfig()
	cfg.AutofeeSettlingWindowSec = 7200
	cfg.AutofeeSettlingMultiplier = 1.0

	candidates := []rebalanceTarget{{Channel: RebalanceChannel{ChannelID: 100}, Score: 1000}}
	adjustments := map[uint64]time.Time{100: now.Add(-30 * time.Minute)}
	if dampened := applyAutofeeSettlingPenalty(candidates, adjustments, cfg, now); dampened != 0 {
		t.Fatalf("expected no dampening when multiplier=1, got %d", dampened)
	}
}

func TestNormalizeRebalanceConfigClampsAutofeeSettling(t *testing.T) {
	cfg := defaultRebalanceConfig()
	cfg.AutofeeSettlingWindowSec = -10
	cfg.AutofeeSettlingMultiplier = -0.5
	got := normalizeRebalanceConfig(cfg)
	if got.AutofeeSettlingWindowSec != 0 {
		t.Fatalf("expected window clamped to 0, got %d", got.AutofeeSettlingWindowSec)
	}
	if got.AutofeeSettlingMultiplier != 0 {
		t.Fatalf("expected multiplier clamped to 0, got %f", got.AutofeeSettlingMultiplier)
	}

	cfg2 := defaultRebalanceConfig()
	cfg2.AutofeeSettlingMultiplier = 5
	got2 := normalizeRebalanceConfig(cfg2)
	if got2.AutofeeSettlingMultiplier != 1 {
		t.Fatalf("expected multiplier capped to 1, got %f", got2.AutofeeSettlingMultiplier)
	}
}

func TestDefaultRebalanceConfigIncludesAutofeeSettlingDefaults(t *testing.T) {
	cfg := defaultRebalanceConfig()
	if cfg.AutofeeSettlingWindowSec != 7200 {
		t.Fatalf("expected default window 7200, got %d", cfg.AutofeeSettlingWindowSec)
	}
	if cfg.AutofeeSettlingMultiplier != 0.5 {
		t.Fatalf("expected default multiplier 0.5, got %f", cfg.AutofeeSettlingMultiplier)
	}
}

func TestEstimateTargetGainV2UsesSpreadEffectiveness(t *testing.T) {
	got := estimateTargetGainV2(1_000_000, 1_000, 250)
	if got != 750 {
		t.Fatalf("expected v2 gain 750 sats, got %d", got)
	}
	if got := estimateTargetGainV2(1_000_000, 1_000, 1_000); got != 0 {
		t.Fatalf("expected zero gain when peer fee erases spread, got %d", got)
	}
	if got := spreadEffectiveness(1_000, 250); got != 0.75 {
		t.Fatalf("expected spread effectiveness 0.75, got %f", got)
	}
}

func TestNormalizeRebalanceConfigClampsNegativeFields(t *testing.T) {
	cfg := RebalanceConfig{
		MinAmountSat:  -1,
		MaxAmountSat:  -2,
		MinProbeSat:   -3,
		MinExecuteSat: -4,
	}
	got := normalizeRebalanceConfig(cfg)

	if got.MinAmountSat != 0 {
		t.Fatalf("expected MinAmountSat clamped to 0, got %d", got.MinAmountSat)
	}
	if got.MaxAmountSat != 0 {
		t.Fatalf("expected MaxAmountSat clamped to 0, got %d", got.MaxAmountSat)
	}
	if got.MinProbeSat != 0 {
		t.Fatalf("expected MinProbeSat clamped to 0, got %d", got.MinProbeSat)
	}
	if got.MinExecuteSat != 0 {
		t.Fatalf("expected MinExecuteSat clamped to 0, got %d", got.MinExecuteSat)
	}
	if got.MppMaxShards != 6 {
		t.Fatalf("expected MppMaxShards fallback=6, got %d", got.MppMaxShards)
	}
	if got.MppParallelism != 3 {
		t.Fatalf("expected MppParallelism fallback=3, got %d", got.MppParallelism)
	}
	if got.MppMinShardSat != 10000 {
		t.Fatalf("expected MppMinShardSat fallback=10000, got %d", got.MppMinShardSat)
	}
	if got.MppRoundTimeoutSec != 35 {
		t.Fatalf("expected MppRoundTimeoutSec fallback=35, got %d", got.MppRoundTimeoutSec)
	}
}

func TestEffectiveMinsSplitDisabledUseLegacyMin(t *testing.T) {
	cfg := RebalanceConfig{
		MinSplitEnabled: false,
		MinAmountSat:    20000,
		MinProbeSat:     1000,
		MinExecuteSat:   30000,
	}
	if got := effectiveMinExecuteSat(cfg); got != 20000 {
		t.Fatalf("expected execute min=20000 with split disabled, got %d", got)
	}
	if got := effectiveMinProbeSat(cfg); got != 20000 {
		t.Fatalf("expected probe min=20000 with split disabled, got %d", got)
	}
}

func TestEffectiveMinsSplitEnabledUseDedicatedValues(t *testing.T) {
	cfg := RebalanceConfig{
		MinSplitEnabled: true,
		MinAmountSat:    20000,
		MinProbeSat:     1000,
		MinExecuteSat:   15000,
	}
	if got := effectiveMinExecuteSat(cfg); got != 15000 {
		t.Fatalf("expected execute min=15000 with split enabled, got %d", got)
	}
	if got := effectiveMinProbeSat(cfg); got != 1000 {
		t.Fatalf("expected probe min=1000 with split enabled, got %d", got)
	}
}

func TestEffectiveExecuteMinKeepsLegacyMinWhenExecuteUnsetInSplitMode(t *testing.T) {
	cfg := RebalanceConfig{
		MinSplitEnabled: true,
		MinAmountSat:    20000,
		MinProbeSat:     1500,
		MinExecuteSat:   0,
	}
	if got := effectiveMinExecuteSat(cfg); got != 20000 {
		t.Fatalf("expected execute min fallback to legacy min=20000, got %d", got)
	}
	if got := effectiveMinProbeSat(cfg); got != 1500 {
		t.Fatalf("expected probe min=1500, got %d", got)
	}
}

func TestEffectiveMinsSplitEnabledFallbackToLegacyWhenUnset(t *testing.T) {
	cfg := RebalanceConfig{
		MinSplitEnabled: true,
		MinAmountSat:    20000,
		MinProbeSat:     0,
		MinExecuteSat:   0,
	}
	if got := effectiveMinExecuteSat(cfg); got != 20000 {
		t.Fatalf("expected execute min fallback=20000, got %d", got)
	}
	if got := effectiveMinProbeSat(cfg); got != 20000 {
		t.Fatalf("expected probe min fallback=20000, got %d", got)
	}
}

func TestEffectiveStartAmountUsesMinAmountWithSplitEnabled(t *testing.T) {
	cfg := RebalanceConfig{
		MinSplitEnabled: true,
		MinAmountSat:    30000,
		MinProbeSat:     1000,
		MinExecuteSat:   1000,
	}
	if got := effectiveStartAmountSat(cfg); got != 30000 {
		t.Fatalf("expected start amount anchored at min_amount=30000, got %d", got)
	}
}

func TestEffectiveStartAmountFallbackOrder(t *testing.T) {
	cfg := RebalanceConfig{
		MinSplitEnabled: true,
		MinAmountSat:    0,
		MinProbeSat:     1000,
		MinExecuteSat:   5000,
	}
	if got := effectiveStartAmountSat(cfg); got != 5000 {
		t.Fatalf("expected fallback to execute min=5000, got %d", got)
	}

	cfg.MinExecuteSat = 0
	if got := effectiveStartAmountSat(cfg); got != 1000 {
		t.Fatalf("expected fallback to probe min=1000, got %d", got)
	}
}

func TestComputeProbeCapBehavior(t *testing.T) {
	if got := computeProbeCap(0, 20000, 0); got != 0 {
		t.Fatalf("expected cap=0 when remaining=0, got %d", got)
	}
	if got := computeProbeCap(100000, 20000, 50000); got != 50000 {
		t.Fatalf("expected cap constrained by max=50000, got %d", got)
	}
	if got := computeProbeCap(100000, 20000, 200000); got != 100000 {
		t.Fatalf("expected cap=remaining when max > remaining, got %d", got)
	}
	if got := computeProbeCap(100000, 0, 0); got != 100000 {
		t.Fatalf("expected cap=remaining when min<=0 and max=0, got %d", got)
	}
	if got := computeProbeCap(60000, 20000, 0); got != 60000 {
		t.Fatalf("expected cap=remaining when chunks<=4, got %d", got)
	}

	heuristic := computeProbeCap(200000, 20000, 0)
	if heuristic < 80000 {
		t.Fatalf("expected heuristic cap >= 4*min (80000), got %d", heuristic)
	}
	if heuristic > 200000 {
		t.Fatalf("expected heuristic cap <= remaining, got %d", heuristic)
	}
	if heuristic < 20000 {
		t.Fatalf("expected heuristic cap >= min, got %d", heuristic)
	}
}

func TestBuildScanDetailIncludesBelowExecuteMinReason(t *testing.T) {
	reasons := map[string]int{
		"below_execute_min": 3,
	}
	got := buildScanDetail(reasons, 0, 5)
	if got == "" {
		t.Fatalf("expected non-empty scan detail")
	}
	if !strings.Contains(got, "below execute min amount: 3") {
		t.Fatalf("expected below_execute_min reason in detail, got %q", got)
	}
}

func TestPairFailureTTLAdaptsByReasonAndFailureCount(t *testing.T) {
	if got := pairFailureTTL("unknown failure", 10); got != pairFailTTL {
		t.Fatalf("expected unknown failures to preserve default ttl, got %s", got)
	}
	if got := pairFailureTTL("rpc error: code = Unknown desc = unable to find a path to destination", 1); got != 20*time.Minute {
		t.Fatalf("expected no-path base ttl 20m, got %s", got)
	}
	if got := pairFailureTTL("mpp shard: probe returned no amount", 2); got != 30*time.Minute {
		t.Fatalf("expected probe ttl with one backoff to be 30m, got %s", got)
	}
	if got := pairFailureTTL("no matching outgoing channel available", 8); got != pairFailTTLMax {
		t.Fatalf("expected repeated structural failure to cap at %s, got %s", pairFailTTLMax, got)
	}
	if pairFailTTLMax > rebalanceMaxCooldown {
		t.Fatalf("expected pair failure ttl cap <= max rebalance cooldown, got %s", pairFailTTLMax)
	}
}

func TestPermanentFailScoreDecaysAndWeightsFailureTypes(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	decayed := decayedPermanentFailScore(8, now.Add(-permanentFailScoreHalfLife), now)
	if decayed < 3.99 || decayed > 4.01 {
		t.Fatalf("expected one half-life to halve score to about 4, got %.4f", decayed)
	}
	next := nextPermanentFailScore(8, now.Add(-permanentFailScoreHalfLife), now, 1)
	if next < 4.99 || next > 5.01 {
		t.Fatalf("expected decayed score plus increment to be about 5, got %.4f", next)
	}
	if got := permanentFailScoreIncrement("rpc error: code = Unknown desc = unable to find a path to destination"); got != 1 {
		t.Fatalf("expected structural failure increment 1, got %.2f", got)
	}
	if got := permanentFailScoreIncrement("temporary_channel_failure"); got != 0.25 {
		t.Fatalf("expected temporary channel failure increment 0.25, got %.2f", got)
	}
	if got := permanentFailScoreIncrement("route fee exceeds limit"); got != 0 {
		t.Fatalf("expected fee-limit failure to avoid permanent score, got %.2f", got)
	}
}

func TestShouldSkipPairForRecentFailureHonorsSuccessResetAndAdaptiveTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	stat := pairStat{
		LastFailAt:     now.Add(-10 * time.Minute),
		LastFailReason: "unable to find a path to destination",
		FailCount:      1,
	}
	if !shouldSkipPairForRecentFailure(stat, now) {
		t.Fatalf("expected recent no-path failure to be skipped")
	}

	stat.LastFailAt = now.Add(-25 * time.Minute)
	if shouldSkipPairForRecentFailure(stat, now) {
		t.Fatalf("did not expect expired no-path failure to be skipped")
	}

	stat.LastFailAt = now.Add(-10 * time.Minute)
	stat.LastSuccessAt = now.Add(-5 * time.Minute)
	if shouldSkipPairForRecentFailure(stat, now) {
		t.Fatalf("did not expect pair to be skipped after a newer success")
	}
}

func TestShouldSkipPairForRecentFailureUsesPermanentFailScoreTTL(t *testing.T) {
	// Score >= permanentFailScoreSkipThreshold (5) ativa o TTL extra do
	// permanent fail score (cap em permanentFailScoreTTLMax = 1h).
	// Pair TTL base para "unable to find a path" é 20min (failCount=1) — o
	// permanent score deve estender o skip além desse base.
	now := time.Unix(1_700_000_000, 0).UTC()
	stat := pairStat{
		LastFailAt:           now.Add(-30 * time.Minute),
		LastFailReason:       "unable to find a path to destination",
		FailCount:            1,
		PermanentFailScore:   5,
		PermanentFailUpdated: now,
	}
	if !shouldSkipPairForRecentFailure(stat, now) {
		t.Fatalf("expected permanent fail score to extend skip ttl beyond normal no-path ttl")
	}

	stat.LastFailAt = now.Add(-2 * time.Hour)
	if shouldSkipPairForRecentFailure(stat, now) {
		t.Fatalf("did not expect pair skip after permanent score ttl expires")
	}

	stat.LastFailAt = now.Add(-2 * time.Hour)
	stat.LastSuccessAt = now.Add(-1 * time.Minute)
	if shouldSkipPairForRecentFailure(stat, now) {
		t.Fatalf("did not expect permanent fail score to override a newer success")
	}
}

func TestIsStructuralRebalanceFailureNormalizesMppPrefix(t *testing.T) {
	if !isStructuralRebalanceFailure("mpp shard: rpc error: code = Unknown desc = unable to find a path to destination") {
		t.Fatalf("expected mpp no-path failure to be structural")
	}
	if !isStructuralRebalanceFailure("mpp structural failure") {
		t.Fatalf("expected mpp structural abort to be structural")
	}
	if !isStructuralRebalanceFailure("no route returned") {
		t.Fatalf("expected empty route result to be structural")
	}
	if isStructuralRebalanceFailure("route fee exceeds limit") {
		t.Fatalf("did not expect fee limit failure to be structural")
	}
}

func TestMissionControlStateExposesResetTelemetry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	svc := &RebalanceService{
		lastMCResetAt:     now.Add(-time.Minute),
		lastMCResetReason: "auto",
		mcResetCount:      3,
	}

	state := svc.missionControlState(now)
	if state.LastMCResetAt != svc.lastMCResetAt.UTC().Format(time.RFC3339) {
		t.Fatalf("unexpected last reset timestamp %q", state.LastMCResetAt)
	}
	if state.LastMCResetReason != "auto" {
		t.Fatalf("unexpected reset reason %q", state.LastMCResetReason)
	}
	if state.MCResetCount != 3 {
		t.Fatalf("unexpected reset count %d", state.MCResetCount)
	}
	if state.MCResetCooldownSec != int64(mcResetCooldown/time.Second) {
		t.Fatalf("unexpected cooldown seconds %d", state.MCResetCooldownSec)
	}
	if state.MCResetCooldownRemainingSec != int64((mcResetCooldown-time.Minute)/time.Second) {
		t.Fatalf("unexpected remaining cooldown %d", state.MCResetCooldownRemainingSec)
	}
}

func TestShouldBlockPairForCurrentJobFailure(t *testing.T) {
	cases := []struct {
		reason string
		want   bool
	}{
		{"rpc error: code = Unknown desc = unable to find a path to destination", true},
		{"mpp shard: probe returned no amount", true},
		{"route failed: TEMPORARY_CHANNEL_FAILURE", true},
		{"route fee exceeds limit", false},
	}
	for _, tc := range cases {
		if got := shouldBlockPairForCurrentJobFailure(tc.reason); got != tc.want {
			t.Fatalf("shouldBlockPairForCurrentJobFailure(%q)=%v want %v", tc.reason, got, tc.want)
		}
	}
}

func TestHasRebalanceFallbackCandidateSkipsBlockedMppSources(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	sources := []RebalanceChannel{
		{ChannelID: 1},
		{ChannelID: 2},
		{ChannelID: 3},
	}
	sourceAvailable := map[uint64]int64{
		1: 100_000,
		2: 100_000,
		3: 100_000,
	}
	pairStats := map[uint64]pairStat{
		1: {
			LastFailAt:     now.Add(-time.Minute),
			LastFailReason: "mpp shard: unable to find a path to destination",
			FailCount:      1,
		},
	}
	currentJobBlockedPairs := map[uint64]struct{}{
		2: {},
	}

	if !hasRebalanceFallbackCandidate(sources, sourceAvailable, pairStats, currentJobBlockedPairs, true, 50_000, now) {
		t.Fatalf("expected unblocked source to allow legacy fallback")
	}

	currentJobBlockedPairs[3] = struct{}{}
	if hasRebalanceFallbackCandidate(sources, sourceAvailable, pairStats, currentJobBlockedPairs, true, 50_000, now) {
		t.Fatalf("did not expect fallback when all available sources are blocked or cooling down")
	}

	sourceAvailable[3] = 40_000
	delete(currentJobBlockedPairs, 3)
	if hasRebalanceFallbackCandidate(sources, sourceAvailable, pairStats, currentJobBlockedPairs, true, 50_000, now) {
		t.Fatalf("did not expect fallback when the only open source is below execute minimum")
	}
}

func TestFilterExecutableSourcesSkipsBelowExecuteMinimum(t *testing.T) {
	sources := []RebalanceChannel{
		{ChannelID: 1},
		{ChannelID: 2},
		{ChannelID: 3},
		{ChannelID: 4},
		{ChannelID: 5},
	}
	sourceAvailable := map[uint64]int64{
		1: 1_038,
		2: 6_406,
		3: 50_000,
		4: 250_000,
		5: 0,
	}

	got := filterExecutableSources(sources, sourceAvailable, 10_000)
	if len(got) != 2 {
		t.Fatalf("expected 2 executable sources, got %d", len(got))
	}
	if got[0].ChannelID != 3 || got[1].ChannelID != 4 {
		t.Fatalf("unexpected executable source order: %+v", got)
	}
}

func TestDelegatedFastPathSourceIDsStrictPaybackRequiresFullAmount(t *testing.T) {
	sources := []RebalanceChannel{
		{ChannelID: 1},
		{ChannelID: 2},
		{ChannelID: 3},
		{ChannelID: 4},
	}
	sourceAvailable := map[uint64]int64{
		1: 9_000,
		2: 40_000,
		3: 120_000,
		4: 250_000,
	}

	loose := delegatedFastPathSourceIDs(sources, sourceAvailable, 100_000, 10_000, false)
	if len(loose) != 3 || loose[0] != 2 || loose[1] != 3 || loose[2] != 4 {
		t.Fatalf("unexpected loose fast-path source ids: %+v", loose)
	}

	strict := delegatedFastPathSourceIDs(sources, sourceAvailable, 100_000, 10_000, true)
	if len(strict) != 2 || strict[0] != 3 || strict[1] != 4 {
		t.Fatalf("unexpected strict fast-path source ids: %+v", strict)
	}
}

func TestTargetCooldownProbeHelpers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	if !shouldRunTargetCooldownProbe(time.Time{}, now) {
		t.Fatalf("expected missing last auto time to allow cooldown probe")
	}
	if shouldRunTargetCooldownProbe(now.Add(-targetCooldownProbeInterval+time.Second), now) {
		t.Fatalf("did not expect cooldown probe before interval")
	}
	if !shouldRunTargetCooldownProbe(now.Add(-targetCooldownProbeInterval-time.Second), now) {
		t.Fatalf("expected cooldown probe after interval")
	}
	if shouldRunTargetCooldownProbeAfter(now.Add(-targetCooldownProbeBackoffInterval+time.Second), now, targetCooldownProbeBackoffInterval) {
		t.Fatalf("did not expect cooldown probe before backoff interval")
	}
	if !shouldRunTargetCooldownProbeAfter(now.Add(-targetCooldownProbeBackoffInterval-time.Second), now, targetCooldownProbeBackoffInterval) {
		t.Fatalf("expected cooldown probe after backoff interval")
	}

	cfg := RebalanceConfig{MinSplitEnabled: true, MinProbeSat: 5_000, MinExecuteSat: 50_000, MinAmountSat: 10_000}
	if got := rebalanceCooldownProbeAmount(250_000, cfg); got != 50_000 {
		t.Fatalf("expected cooldown probe to use execute minimum, got %d", got)
	}
	if got := rebalanceCooldownProbeAmount(30_000, cfg); got != 30_000 {
		t.Fatalf("expected cooldown probe to cap at target amount, got %d", got)
	}
}

func TestTargetCooldownProbeIntervalBacksOffStrongFailurePressure(t *testing.T) {
	if got := targetCooldownProbeIntervalForStats(recentCooldownStat{}, recentCooldownStat{}, recentCooldownStat{}, recentCooldownStat{}); got != targetCooldownProbeInterval {
		t.Fatalf("expected base probe interval, got %v", got)
	}
	failed := recentCooldownStat{
		Attempts: targetFailedCooldownMinFailures,
		Failures: targetFailedCooldownMinFailures,
	}
	if got := targetCooldownProbeIntervalForStats(recentCooldownStat{}, recentCooldownStat{}, failed, recentCooldownStat{}); got != targetCooldownProbeBackoffInterval {
		t.Fatalf("expected backed-off probe interval for failed target pressure, got %v", got)
	}
	distinct := recentCooldownStat{DistinctSources: targetDistinctSourceMinFailures}
	if got := targetCooldownProbeIntervalForStats(recentCooldownStat{}, recentCooldownStat{}, recentCooldownStat{}, distinct); got != targetCooldownProbeBackoffInterval {
		t.Fatalf("expected backed-off probe interval for distinct source pressure, got %v", got)
	}
}

func TestCooldownProbeSemaphoreIsSingleSlotAndIndependent(t *testing.T) {
	svc := NewRebalanceService(nil, nil, nil)
	svc.stop = make(chan struct{})

	if !svc.cooldownProbeSlotAvailable() {
		t.Fatalf("expected empty cooldown probe slot to be available")
	}
	if !svc.acquireCooldownProbeSem(context.Background()) {
		t.Fatalf("expected first cooldown probe acquire to succeed")
	}
	if svc.cooldownProbeSlotAvailable() {
		t.Fatalf("did not expect cooldown probe slot to remain available")
	}
	if svc.acquireCooldownProbeSem(context.Background()) {
		t.Fatalf("did not expect second cooldown probe acquire to succeed")
	}

	svc.releaseCooldownProbeSem()
	if !svc.acquireCooldownProbeSem(context.Background()) {
		t.Fatalf("expected cooldown probe acquire after release to succeed")
	}
	svc.releaseCooldownProbeSem()
}

func TestBuildAndOrderRebalanceCandidatesBacksOffCooldownProbe(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := defaultRebalanceConfig()
	cfg.MinSplitEnabled = true
	cfg.MinExecuteSat = 10_000
	cfg.MinAmountSat = 50_000
	cfg.CooldownProbeEnabled = true

	input := rebalanceAutoScanCandidateInput{
		Channels: []RebalanceChannel{{
			ChannelID:         1,
			ChannelPoint:      "abc:0",
			PeerAlias:         "target",
			TargetAmountSat:   100_000,
			TargetOutboundPct: 20,
			EligibleAsTarget:  true,
		}},
		Settings: map[uint64]channelSetting{
			1: {AutoEnabled: true},
		},
		Cfg:              cfg,
		ScanAt:           now,
		LastAutoByTarget: map[uint64]time.Time{1: now.Add(-30 * time.Minute)},
		TargetFailedCooldowns: map[uint64]recentCooldownStat{
			1: {
				Attempts:      targetFailedCooldownMinFailures,
				Failures:      targetFailedCooldownMinFailures,
				LastAttemptAt: now.Add(-5 * time.Minute),
			},
		},
	}

	plan := buildAndOrderRebalanceCandidates(input)
	if len(plan.Candidates) != 0 {
		t.Fatalf("expected cooldown probe to stay backed off, got %d candidates", len(plan.Candidates))
	}
	if plan.SkipReasons["target_cooldown_probe_backoff"] != 1 {
		t.Fatalf("expected probe backoff skip reason, got %+v", plan.SkipReasons)
	}

	input.LastAutoByTarget[1] = now.Add(-targetCooldownProbeBackoffInterval - time.Second)
	plan = buildAndOrderRebalanceCandidates(input)
	if len(plan.Candidates) != 1 || !plan.Candidates[0].CooldownProbe {
		t.Fatalf("expected backed-off probe after interval, got %+v", plan.Candidates)
	}
}

func TestBuildAndOrderRebalanceCandidatesSkipsCooldownProbeWhenDisabled(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := defaultRebalanceConfig()
	cfg.MinSplitEnabled = true
	cfg.MinExecuteSat = 10_000
	cfg.MinAmountSat = 50_000
	cfg.CooldownProbeEnabled = false

	input := rebalanceAutoScanCandidateInput{
		Channels: []RebalanceChannel{{
			ChannelID:         1,
			ChannelPoint:      "abc:0",
			PeerAlias:         "target",
			TargetAmountSat:   100_000,
			TargetOutboundPct: 20,
			EligibleAsTarget:  true,
		}},
		Settings: map[uint64]channelSetting{
			1: {AutoEnabled: true},
		},
		Cfg:              cfg,
		ScanAt:           now,
		LastAutoByTarget: map[uint64]time.Time{1: now.Add(-2 * time.Hour)},
		TargetFailedCooldowns: map[uint64]recentCooldownStat{
			1: {
				Attempts:      targetFailedCooldownMinFailures,
				Failures:      targetFailedCooldownMinFailures,
				LastAttemptAt: now.Add(-5 * time.Minute),
			},
		},
	}

	plan := buildAndOrderRebalanceCandidates(input)
	if len(plan.Candidates) != 0 {
		t.Fatalf("expected disabled cooldown probe to produce no candidates, got %d", len(plan.Candidates))
	}
	if plan.SkipReasons["target_cooldown"] != 1 {
		t.Fatalf("expected target_cooldown skip when probes are disabled, got %+v", plan.SkipReasons)
	}
}

func TestBuildAndOrderRebalanceCandidatesCanIncludeManualRestartTargets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := defaultRebalanceConfig()
	cfg.ROIMin = 0
	cfg.MinSplitEnabled = true
	cfg.MinExecuteSat = 10_000
	cfg.MinAmountSat = 50_000

	channels := []RebalanceChannel{
		{
			ChannelID:         1,
			ChannelPoint:      "auto:0",
			PeerAlias:         "auto",
			CapacitySat:       1_000_000,
			LocalBalanceSat:   100_000,
			OutgoingFeePpm:    1_000,
			TargetAmountSat:   100_000,
			TargetOutboundPct: 20,
			EligibleAsTarget:  true,
			Revenue7dSat:      10_000,
		},
		{
			ChannelID:         2,
			ChannelPoint:      "manual:0",
			PeerAlias:         "manual",
			CapacitySat:       1_000_000,
			LocalBalanceSat:   100_000,
			OutgoingFeePpm:    1_000,
			TargetAmountSat:   100_000,
			TargetOutboundPct: 20,
			EligibleAsTarget:  true,
			Revenue7dSat:      10_000,
		},
	}
	settings := map[uint64]channelSetting{
		1: {AutoEnabled: true},
		2: {ManualRestartEnabled: true},
	}

	plan := buildAndOrderRebalanceCandidates(rebalanceAutoScanCandidateInput{
		Channels: channels,
		Settings: settings,
		Cfg:      cfg,
		ScanAt:   now,
	})
	if len(plan.Candidates) != 1 || plan.Candidates[0].Channel.ChannelID != 1 {
		t.Fatalf("expected only auto-enabled target by default, got %+v", plan.Candidates)
	}

	plan = buildAndOrderRebalanceCandidates(rebalanceAutoScanCandidateInput{
		Channels:                    channels,
		Settings:                    settings,
		Cfg:                         cfg,
		ScanAt:                      now,
		IncludeManualRestartTargets: true,
	})
	if len(plan.Candidates) != 2 {
		t.Fatalf("expected auto and manual-restart targets, got %+v", plan.Candidates)
	}
	seen := map[uint64]bool{}
	for _, candidate := range plan.Candidates {
		seen[candidate.Channel.ChannelID] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("expected candidates for channels 1 and 2, got %+v", plan.Candidates)
	}
}

func TestBuildAndOrderRebalanceCandidatesCanDisableCooldownProbePerPlan(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cfg := defaultRebalanceConfig()
	cfg.MinSplitEnabled = true
	cfg.MinExecuteSat = 10_000
	cfg.MinAmountSat = 50_000
	cfg.CooldownProbeEnabled = true

	input := rebalanceAutoScanCandidateInput{
		Channels: []RebalanceChannel{{
			ChannelID:         1,
			ChannelPoint:      "abc:0",
			PeerAlias:         "target",
			TargetAmountSat:   100_000,
			TargetOutboundPct: 20,
			EligibleAsTarget:  true,
		}},
		Settings: map[uint64]channelSetting{
			1: {AutoEnabled: true},
		},
		Cfg:                  cfg,
		ScanAt:               now,
		LastAutoByTarget:     map[uint64]time.Time{1: now.Add(-targetCooldownProbeBackoffInterval - time.Second)},
		DisableCooldownProbe: true,
		TargetFailedCooldowns: map[uint64]recentCooldownStat{
			1: {
				Attempts:      targetFailedCooldownMinFailures,
				Failures:      targetFailedCooldownMinFailures,
				LastAttemptAt: now.Add(-5 * time.Minute),
			},
		},
	}

	plan := buildAndOrderRebalanceCandidates(input)
	if len(plan.Candidates) != 0 {
		t.Fatalf("expected per-plan cooldown probe disable to produce no candidates, got %+v", plan.Candidates)
	}
	if plan.SkipReasons["target_cooldown"] != 1 {
		t.Fatalf("expected target_cooldown skip when plan disables probes, got %+v", plan.SkipReasons)
	}

	input.DisableCooldownProbe = false
	plan = buildAndOrderRebalanceCandidates(input)
	if len(plan.Candidates) != 1 || !plan.Candidates[0].CooldownProbe {
		t.Fatalf("expected normal cooldown probe when plan allows probes, got %+v", plan.Candidates)
	}
}

func TestExecuteSovereignAutopilotShadowRecordsWouldQueueAndSkips(t *testing.T) {
	svc := NewRebalanceService(nil, nil, nil)
	cfg := defaultRebalanceConfig()
	cfg.BudgetUnlimited = true
	cfg.SovereignMaxJobsPerCycle = 1
	cfg.SovereignMinExpectedProfitSat = 50

	plan := rebalanceAutoScanCandidatePlan{Candidates: []rebalanceTarget{
		{
			Channel:          RebalanceChannel{ChannelID: 1, ChannelPoint: "low:0", PeerAlias: "low", TargetAmountSat: 100_000},
			ExpectedGainSat:  120,
			EstimatedCostSat: 100,
			Score:            20,
		},
		{
			Channel:          RebalanceChannel{ChannelID: 2, ChannelPoint: "good:0", PeerAlias: "good", TargetAmountSat: 100_000},
			ExpectedGainSat:  300,
			EstimatedCostSat: 100,
			Score:            200,
		},
		{
			Channel:          RebalanceChannel{ChannelID: 3, ChannelPoint: "cap:0", PeerAlias: "cap", TargetAmountSat: 100_000},
			ExpectedGainSat:  500,
			EstimatedCostSat: 100,
			Score:            400,
		},
	}}

	result := svc.executeSovereignAutopilot(context.Background(), cfg, nil, plan, time.Now(), false)
	if result.Status != "sovereign_shadow" {
		t.Fatalf("expected shadow status, got %q", result.Status)
	}
	if result.Selected != 1 || result.ExpectedProfitSat != 200 {
		t.Fatalf("expected one selected decision with 200 sat expected profit, got selected=%d profit=%d", result.Selected, result.ExpectedProfitSat)
	}
	if len(result.Decisions) != 3 {
		t.Fatalf("expected 3 decisions, got %d", len(result.Decisions))
	}
	if result.Decisions[0].Reason != "expected_profit_below_min" {
		t.Fatalf("expected first decision below min profit, got %+v", result.Decisions[0])
	}
	if !result.Decisions[1].Selected || result.Decisions[1].Reason != "would_queue" {
		t.Fatalf("expected second decision to be a shadow queue, got %+v", result.Decisions[1])
	}
	if result.Decisions[2].Reason != "cycle_limit" {
		t.Fatalf("expected third decision to be cycle-limited, got %+v", result.Decisions[2])
	}
}

func TestExecuteSovereignAutopilotSkipsLowHistoricalSuccessRate(t *testing.T) {
	svc := NewRebalanceService(nil, nil, nil)
	cfg := defaultRebalanceConfig()
	cfg.BudgetUnlimited = true
	cfg.SovereignMaxJobsPerCycle = 1
	cfg.SovereignMinExpectedProfitSat = 50

	plan := rebalanceAutoScanCandidatePlan{Candidates: []rebalanceTarget{
		{
			Channel:          RebalanceChannel{ChannelID: 1, ChannelPoint: "dead:0", PeerAlias: "dead", TargetAmountSat: 100_000},
			ExpectedGainSat:  1_200,
			EstimatedCostSat: 100,
			Score:            1_100,
			PairStats: rebalanceTargetPairStats{
				Attempts:  sovereignHistoryMinAttempts,
				Successes: 0,
				Failures:  sovereignHistoryMinAttempts,
			},
		},
		{
			Channel:          RebalanceChannel{ChannelID: 2, ChannelPoint: "healthy:0", PeerAlias: "healthy", TargetAmountSat: 100_000},
			ExpectedGainSat:  200,
			EstimatedCostSat: 100,
			Score:            100,
			PairStats: rebalanceTargetPairStats{
				Attempts:  100,
				Successes: 5,
				Failures:  95,
			},
		},
	}}

	result := svc.executeSovereignAutopilot(context.Background(), cfg, nil, plan, time.Now(), false)
	if result.Selected != 1 {
		t.Fatalf("expected one selected decision, got %d", result.Selected)
	}
	if result.Decisions[0].Reason != sovereignHistoricalSuccessRateBelowReason {
		t.Fatalf("expected low success history skip, got %+v", result.Decisions[0])
	}
	if !result.Decisions[1].Selected || result.Decisions[1].Reason != "would_queue" {
		t.Fatalf("expected healthier candidate selected, got %+v", result.Decisions[1])
	}
}

func TestExecuteSovereignAutopilotLiveKeepsBudgetExhaustedStatus(t *testing.T) {
	svc := NewRebalanceService(nil, nil, nil)
	cfg := defaultRebalanceConfig()
	cfg.BudgetUnlimited = false
	cfg.MinSplitEnabled = true
	cfg.MinExecuteSat = 10_000
	cfg.SovereignMaxJobsPerCycle = 2

	plan := rebalanceAutoScanCandidatePlan{Candidates: []rebalanceTarget{{
		Channel: RebalanceChannel{
			ChannelID:       1,
			ChannelPoint:    "budget:0",
			PeerAlias:       "budget",
			TargetAmountSat: 100_000,
			OutgoingFeePpm:  1_000,
		},
		ExpectedGainSat: 1_000,
		Score:           1_000,
	}}}

	result := svc.executeSovereignAutopilot(context.Background(), cfg, nil, plan, time.Now(), true)
	if result.Status != "budget_exhausted" {
		t.Fatalf("expected budget_exhausted status, got %q", result.Status)
	}
	if result.Selected != 0 {
		t.Fatalf("expected no selected decisions, got %d", result.Selected)
	}
	if result.SkipReasons["budget_too_low"] == 0 && result.SkipReasons["budget_below_min"] == 0 {
		t.Fatalf("expected budget skip reason, got %+v", result.SkipReasons)
	}
}

func TestShouldCooldownRecentFailuresRequiresRecentFailurePressure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	stat := recentCooldownStat{
		Attempts:      25,
		Failures:      25,
		Successes:     0,
		LastAttemptAt: now.Add(-5 * time.Minute),
	}
	if !shouldCooldownRecentFailures(stat, 25, 0, now) {
		t.Fatalf("expected recent high-failure stat to enter cooldown")
	}

	stat.Attempts = 24
	if shouldCooldownRecentFailures(stat, 25, 0, now) {
		t.Fatalf("did not expect cooldown below attempt threshold")
	}

	stat.Attempts = 25
	stat.Successes = 1
	if shouldCooldownRecentFailures(stat, 25, 0, now) {
		t.Fatalf("did not expect cooldown above success threshold")
	}

	stat.Successes = 0
	stat.LastAttemptAt = now.Add(-recentCooldownTTL - time.Second)
	if shouldCooldownRecentFailures(stat, 25, 0, now) {
		t.Fatalf("did not expect expired failure pressure to remain in cooldown")
	}
}

func TestShouldCooldownRecentFailuresOnlySuccessAfterFailureResets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	stat := recentCooldownStat{
		Attempts:      5,
		Failures:      5,
		Successes:     1,
		LastAttemptAt: now.Add(-5 * time.Minute),
		LastFailureAt: now.Add(-5 * time.Minute),
		LastSuccessAt: now.Add(-20 * time.Minute),
	}
	if !shouldCooldownRecentFailures(stat, 5, 0, now) {
		t.Fatalf("expected success before the latest failure to keep cooldown active")
	}

	stat.LastSuccessAt = now.Add(-2 * time.Minute)
	if shouldCooldownRecentFailures(stat, 5, 0, now) {
		t.Fatalf("did not expect cooldown after a newer success")
	}
}

func TestShouldCooldownTargetRecentFailuresIncludesNoAttemptJobs(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	noAttempt := recentCooldownStat{
		Attempts:      targetNoAttemptCooldownMinFailures,
		Failures:      targetNoAttemptCooldownMinFailures,
		LastAttemptAt: now.Add(-5 * time.Minute),
	}
	if !shouldCooldownTargetRecentFailures(recentCooldownStat{}, noAttempt, recentCooldownStat{}, recentCooldownStat{}, now) {
		t.Fatalf("expected repeated no-attempt target failures to trigger target cooldown")
	}

	noAttempt.Successes = 1
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, noAttempt, recentCooldownStat{}, recentCooldownStat{}, now) {
		t.Fatalf("did not expect no-attempt cooldown after a recent target success")
	}

	noAttempt.Successes = 0
	noAttempt.LastAttemptAt = now.Add(-recentCooldownTTL - time.Second)
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, noAttempt, recentCooldownStat{}, recentCooldownStat{}, now) {
		t.Fatalf("did not expect expired no-attempt target failures to keep target in cooldown")
	}
}

func TestShouldCooldownTargetRecentFailuresIncludesAllSourcesFailedJobs(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	failed := recentCooldownStat{
		Attempts:      targetFailedCooldownMinFailures,
		Failures:      targetFailedCooldownMinFailures,
		LastAttemptAt: now.Add(-5 * time.Minute),
	}
	if !shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, failed, recentCooldownStat{}, now) {
		t.Fatalf("expected repeated all-sources-failed jobs to trigger target cooldown")
	}

	failed.Attempts = targetFailedCooldownMinFailures - 1
	failed.Failures = failed.Attempts
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, failed, recentCooldownStat{}, now) {
		t.Fatalf("did not expect all-sources-failed cooldown below failure threshold")
	}

	failed.Attempts = targetFailedCooldownMinFailures
	failed.Failures = targetFailedCooldownMinFailures
	failed.Successes = 1
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, failed, recentCooldownStat{}, now) {
		t.Fatalf("did not expect all-sources-failed cooldown after a recent target success")
	}
}

func TestShouldCooldownTargetRecentFailuresIncludesDistinctSourceFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	distinct := recentCooldownStat{
		Attempts:        targetDistinctSourceMinFailures,
		Failures:        targetDistinctSourceMinFailures,
		DistinctSources: targetDistinctSourceMinFailures,
		LastFailureAt:   now.Add(-5 * time.Minute),
	}
	if !shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, recentCooldownStat{}, distinct, now) {
		t.Fatalf("expected distinct source structural failures to trigger target cooldown")
	}

	distinct.DistinctSources = targetDistinctSourceMinFailures - 1
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, recentCooldownStat{}, distinct, now) {
		t.Fatalf("did not expect distinct source cooldown below source threshold")
	}

	distinct.DistinctSources = targetDistinctSourceMinFailures
	distinct.LastSuccessAt = now.Add(-2 * time.Minute)
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, recentCooldownStat{}, distinct, now) {
		t.Fatalf("did not expect distinct source cooldown after a newer target success")
	}
}

func TestNormalizeRebalanceConfigClampsMppBounds(t *testing.T) {
	cfg := RebalanceConfig{
		MppMaxShards:       99,
		MppParallelism:     99,
		MppMinShardSat:     1,
		MppRoundTimeoutSec: 1,
	}
	got := normalizeRebalanceConfig(cfg)
	if got.MppMaxShards != 20 {
		t.Fatalf("expected MppMaxShards clamped to 20, got %d", got.MppMaxShards)
	}
	if got.MppParallelism != got.MppMaxShards {
		t.Fatalf("expected MppParallelism clamped to MppMaxShards=%d, got %d", got.MppMaxShards, got.MppParallelism)
	}
	if got.MppRoundTimeoutSec != 1 {
		t.Fatalf("expected positive MppRoundTimeoutSec preserved, got %d", got.MppRoundTimeoutSec)
	}
}

func TestNormalizeRebalanceConfigNormalizesBudgetAndManualReserveModes(t *testing.T) {
	cfg := RebalanceConfig{
		BudgetMode:           "weird",
		ManualReserveEnabled: true,
		ManualReserveMode:    "odd",
		ManualReserveValue:   -15,
	}
	got := normalizeRebalanceConfig(cfg)
	if got.BudgetMode != rebalanceBudgetModeRevenue24hPct {
		t.Fatalf("expected budget mode fallback=%q, got %q", rebalanceBudgetModeRevenue24hPct, got.BudgetMode)
	}
	if got.ManualReserveMode != rebalanceManualReserveModeFixedSat {
		t.Fatalf("expected manual reserve mode fallback=%q, got %q", rebalanceManualReserveModeFixedSat, got.ManualReserveMode)
	}
	if got.ManualReserveValue != 0 {
		t.Fatalf("expected manual reserve value clamped to 0, got %v", got.ManualReserveValue)
	}
}

func TestComputeDailyBudgetFromRevenueHybrid(t *testing.T) {
	cfg := RebalanceConfig{
		DailyBudgetPct: 50,
		BudgetMode:     rebalanceBudgetModeHybridRevenue,
	}
	total, base, shortTerm := computeDailyBudgetFromRevenue(cfg, 1000, 2000)
	if base != 1000 {
		t.Fatalf("expected base budget 1000, got %d", base)
	}
	if shortTerm != 500 {
		t.Fatalf("expected short-term budget 500, got %d", shortTerm)
	}
	if total != 850 {
		t.Fatalf("expected hybrid total budget 850, got %d", total)
	}
}

func TestComputeRemainingForAutoRespectsManualReserve(t *testing.T) {
	remainingTotal := computeRemainingTotalBudget(1000, 400)
	remainingForAuto := computeRemainingForAuto(1000, 300, 400, 300, false)
	if remainingTotal != 600 {
		t.Fatalf("expected remaining total 600, got %d", remainingTotal)
	}
	if remainingForAuto != 400 {
		t.Fatalf("expected remaining for auto 400, got %d", remainingForAuto)
	}
}

func TestComputeRemainingForAutoUsesAutoSpendCap(t *testing.T) {
	remainingForAuto := computeRemainingForAuto(1000, 850, 850, 300, false)
	if remainingForAuto != 0 {
		t.Fatalf("expected remaining for auto 0 when auto spend exceeds auto cap, got %d", remainingForAuto)
	}
}

func TestComputeRemainingForAutoUsesTotalSpendWhenBudgetNotAutoOnly(t *testing.T) {
	remainingForAuto := computeRemainingForAuto(1000, 300, 1100, 0, false)
	if remainingForAuto != 0 {
		t.Fatalf("expected remaining for auto 0 when total budget exhausted, got %d", remainingForAuto)
	}
}

func TestComputeRemainingForAutoIgnoresManualSpendWhenBudgetAutoOnly(t *testing.T) {
	remainingForAuto := computeRemainingForAuto(1000, 300, 1100, 0, true)
	if remainingForAuto != 700 {
		t.Fatalf("expected remaining for auto to ignore manual spend in auto-only mode, got %d", remainingForAuto)
	}
}

func TestShouldEnforceAutoBudgetHonorsUnlimitedMode(t *testing.T) {
	cfg := RebalanceConfig{}
	if !shouldEnforceAutoBudget(cfg) {
		t.Fatalf("expected auto budget enforcement by default")
	}
	cfg.BudgetUnlimited = true
	if shouldEnforceAutoBudget(cfg) {
		t.Fatalf("expected unlimited budget to bypass auto budget enforcement")
	}
}

func TestCheckManualBudgetAllowanceBudgetExhausted(t *testing.T) {
	cfg := RebalanceConfig{
		DailyBudgetPct:       20,
		ManualReserveEnabled: true,
		ManualReserveMode:    rebalanceManualReserveModePct,
		ManualReserveValue:   10,
		EconRatio:            0.6,
	}
	target := lndclient.ChannelInfo{
		ChannelID:       1,
		CapacitySat:     1_000_000,
		LocalBalanceSat: 100_000,
		BaseFeeMsat:     ptrInt64(0),
		FeeRatePpm:      ptrInt64(500),
	}
	err := checkManualBudgetAllowance(cfg, channelSetting{}, target, 100_000, 1000, 100, 1000)
	if !errors.Is(err, errManualBudgetExhausted) {
		t.Fatalf("expected manual budget exhausted, got %v", err)
	}
}

func TestCheckManualBudgetAllowanceBudgetInsufficient(t *testing.T) {
	cfg := RebalanceConfig{
		EconRatio:      1,
		DailyBudgetPct: 20,
	}
	target := lndclient.ChannelInfo{
		ChannelID:       1,
		CapacitySat:     1_000_000,
		LocalBalanceSat: 100_000,
		BaseFeeMsat:     ptrInt64(0),
		FeeRatePpm:      ptrInt64(5000),
	}
	err := checkManualBudgetAllowance(cfg, channelSetting{}, target, 100_000, 400, 0, 100)
	if !errors.Is(err, errManualBudgetInsufficient) {
		t.Fatalf("expected manual budget insufficient, got %v", err)
	}
}

func TestCheckManualBudgetAllowanceAllowsWhenBudgetFits(t *testing.T) {
	cfg := RebalanceConfig{
		EconRatio:      0.5,
		DailyBudgetPct: 20,
	}
	target := lndclient.ChannelInfo{
		ChannelID:       1,
		CapacitySat:     1_000_000,
		LocalBalanceSat: 100_000,
		BaseFeeMsat:     ptrInt64(0),
		FeeRatePpm:      ptrInt64(500),
	}
	err := checkManualBudgetAllowance(cfg, channelSetting{}, target, 100_000, 1000, 0, 100)
	if err != nil {
		t.Fatalf("expected manual budget allowance, got %v", err)
	}
}

func TestIsManualRestartJobDistinguishesOneShotManual(t *testing.T) {
	if isManualRestartJob("manual", "", false) {
		t.Fatalf("expected one-shot manual run to bypass manual restart budget gate")
	}
	if !isManualRestartJob("manual", "", true) {
		t.Fatalf("expected manual run with auto restart enabled to use manual restart budget gate")
	}
	if !isManualRestartJob("manual", "auto-restart", false) {
		t.Fatalf("expected background auto-restart to use manual restart budget gate")
	}
	if isManualRestartJob("auto", "", false) {
		t.Fatalf("expected auto scan jobs to use auto budget gate, not manual restart gate")
	}
}

func TestShouldUseRecentFailureCacheIncludesManualAutoRestart(t *testing.T) {
	if !shouldUseRecentFailureCache("auto", "") {
		t.Fatalf("expected auto jobs to use recent failure cache")
	}
	if shouldUseRecentFailureCache("auto", targetCooldownProbeReason) {
		t.Fatalf("expected cooldown probes to bypass recent failure cache")
	}
	if !shouldUseRecentFailureCache("manual", "auto-restart") {
		t.Fatalf("expected manual auto-restart jobs to use recent failure cache")
	}
	if shouldUseRecentFailureCache("manual", "") {
		t.Fatalf("expected one-shot manual jobs to bypass recent failure cache")
	}
}

func TestShouldEnforceManualRestartBudgetHonorsAutoOnlyMode(t *testing.T) {
	cfg := RebalanceConfig{}
	if !shouldEnforceManualRestartBudget(cfg, "manual", "", true) {
		t.Fatalf("expected manual restart budget enforcement when auto-only mode is disabled")
	}
	cfg.BudgetUnlimited = true
	if shouldEnforceManualRestartBudget(cfg, "manual", "", true) {
		t.Fatalf("expected unlimited budget to bypass manual restart budget enforcement")
	}
	cfg.BudgetUnlimited = false
	cfg.BudgetAutoOnly = true
	if shouldEnforceManualRestartBudget(cfg, "manual", "", true) {
		t.Fatalf("expected manual restart budget to be bypassed when budget_auto_only is enabled")
	}
}

func TestShouldRunMppShadowRules(t *testing.T) {
	cfg := RebalanceConfig{}
	if shouldRunMppExecute(cfg, "auto") {
		t.Fatalf("expected execute disabled when mpp_enabled=false")
	}
	if shouldRunMppShadow(cfg, "auto") {
		t.Fatalf("expected shadow disabled when mpp_enabled=false")
	}

	cfg.MppEnabled = true
	cfg.MppAutoOnly = false
	if !shouldRunMppExecute(cfg, "manual") {
		t.Fatalf("expected execute enabled for manual when auto-only=false")
	}
	if !shouldRunMppShadow(cfg, "manual") {
		t.Fatalf("expected shadow enabled for manual when auto-only=false")
	}

	cfg.MppAutoOnly = true
	if shouldRunMppExecute(cfg, "manual") {
		t.Fatalf("expected execute disabled for manual when auto-only=true")
	}
	if shouldRunMppShadow(cfg, "manual") {
		t.Fatalf("expected shadow disabled for manual when auto-only=true")
	}
	if !shouldRunMppExecute(cfg, "auto") {
		t.Fatalf("expected execute enabled for auto when auto-only=true")
	}
	if !shouldRunMppShadow(cfg, "auto") {
		t.Fatalf("expected shadow enabled for auto when auto-only=true")
	}
}

func TestBuildMppShadowPlanRespectsShardFloorAndCapacity(t *testing.T) {
	cfg := RebalanceConfig{
		MppMaxShards:   3,
		MppMinShardSat: 1000,
	}
	sources := []RebalanceChannel{
		{ChannelID: 11, MaxSourceSat: 12000},
		{ChannelID: 22, MaxSourceSat: 10000},
		{ChannelID: 33, MaxSourceSat: 8000},
	}
	plan := buildMppShadowPlan(25000, sources, cfg)

	if plan.PlannedShards <= 0 {
		t.Fatalf("expected planned shards > 0")
	}
	if plan.PlannedShards > cfg.MppMaxShards {
		t.Fatalf("expected planned shards <= %d, got %d", cfg.MppMaxShards, plan.PlannedShards)
	}
	if plan.PlannedTotalSat+plan.PlannedRemainderSat != 25000 {
		t.Fatalf("expected planned+remainder to equal target, got %d+%d", plan.PlannedTotalSat, plan.PlannedRemainderSat)
	}
	for _, shard := range plan.Shards {
		if shard.AmountSat < cfg.MppMinShardSat {
			t.Fatalf("expected shard >= min shard (%d), got %d", cfg.MppMinShardSat, shard.AmountSat)
		}
	}
}

func TestBuildMppShadowPlanHandlesInsufficientSourceLiquidity(t *testing.T) {
	cfg := RebalanceConfig{
		MppMaxShards:   4,
		MppMinShardSat: 5000,
	}
	sources := []RebalanceChannel{
		{ChannelID: 1, MaxSourceSat: 4000},
		{ChannelID: 2, MaxSourceSat: 3000},
	}
	plan := buildMppShadowPlan(20000, sources, cfg)

	if plan.EligibleSources != 0 {
		t.Fatalf("expected no eligible sources, got %d", plan.EligibleSources)
	}
	if plan.PlannedShards != 0 || plan.PlannedTotalSat != 0 {
		t.Fatalf("expected no planned shards/sat, got shards=%d total=%d", plan.PlannedShards, plan.PlannedTotalSat)
	}
	if plan.PlannedRemainderSat != 20000 {
		t.Fatalf("expected full remainder=20000, got %d", plan.PlannedRemainderSat)
	}
}

// Wave 2.3: resetSemaphore must defer the resize when jobs are in flight, and
// the last release must rebuild the channel at the new desired capacity.
func TestResetSemaphoreDefersWhileInflight(t *testing.T) {
	s := &RebalanceService{}
	s.cfg = RebalanceConfig{MaxConcurrent: 2}
	s.cfgLoaded = true
	s.resetSemaphore()
	if s.sem == nil || cap(s.sem) != 2 {
		t.Fatalf("expected initial sem cap=2, got %v", cap(s.sem))
	}

	// Acquire 2 slots (simulate 2 in-flight jobs).
	if !s.acquireSem(context.Background()) {
		t.Fatalf("acquire slot 1 failed")
	}
	if !s.acquireSem(context.Background()) {
		t.Fatalf("acquire slot 2 failed")
	}
	if s.semInflight != 2 {
		t.Fatalf("expected inflight=2, got %d", s.semInflight)
	}

	// Bump MaxConcurrent to 5; reset must defer because inflight > 0.
	s.cfg.MaxConcurrent = 5
	s.resetSemaphore()
	if !s.semPendingResize {
		t.Fatalf("expected pending resize after concurrent jobs holding slots")
	}
	if cap(s.sem) != 2 {
		t.Fatalf("expected sem capacity unchanged (still 2) while jobs inflight, got %d", cap(s.sem))
	}

	// Release first slot — still inflight, capacity must remain 2.
	s.releaseSem()
	if cap(s.sem) != 2 {
		t.Fatalf("expected sem capacity 2 after partial release, got %d", cap(s.sem))
	}
	if !s.semPendingResize {
		t.Fatalf("expected pending resize still set, got cleared")
	}

	// Release last slot — resize must apply.
	s.releaseSem()
	if cap(s.sem) != 5 {
		t.Fatalf("expected sem capacity 5 after final release, got %d", cap(s.sem))
	}
	if s.semPendingResize {
		t.Fatalf("expected pending resize cleared after apply")
	}
	if s.semInflight != 0 {
		t.Fatalf("expected inflight=0, got %d", s.semInflight)
	}
}

// Wave 2.3: when no jobs are in flight, resetSemaphore must apply the new
// capacity immediately.
func TestResetSemaphoreAppliesImmediatelyWhenIdle(t *testing.T) {
	s := &RebalanceService{}
	s.cfg = RebalanceConfig{MaxConcurrent: 1}
	s.cfgLoaded = true
	s.resetSemaphore()

	s.cfg.MaxConcurrent = 4
	s.resetSemaphore()
	if cap(s.sem) != 4 {
		t.Fatalf("expected immediate resize to 4 when idle, got %d", cap(s.sem))
	}
	if s.semPendingResize {
		t.Fatalf("expected no pending resize after immediate apply")
	}
}
