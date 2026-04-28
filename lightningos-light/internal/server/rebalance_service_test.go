package server

import (
	"errors"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func ptrInt64(v int64) *int64 { return &v }

func TestDefaultRebalanceConfigSplitCompatibility(t *testing.T) {
	cfg := defaultRebalanceConfig()
	if cfg.MinSplitEnabled {
		t.Fatalf("expected split mode disabled by default")
	}
	if cfg.MinProbeSat != 0 {
		t.Fatalf("expected default min_probe_sat=0, got %d", cfg.MinProbeSat)
	}
	if cfg.MinExecuteSat != 0 {
		t.Fatalf("expected default min_execute_sat=0, got %d", cfg.MinExecuteSat)
	}
	if cfg.MinAmountSat <= 0 {
		t.Fatalf("expected default min_amount_sat > 0, got %d", cfg.MinAmountSat)
	}
	if effectiveMinExecuteSat(cfg) != cfg.MinAmountSat {
		t.Fatalf("expected effective execute min to match legacy min when split is off")
	}
	if effectiveMinProbeSat(cfg) != cfg.MinAmountSat {
		t.Fatalf("expected effective probe min to match legacy min when split is off")
	}
	if cfg.MppEnabled {
		t.Fatalf("expected MSPR disabled by default")
	}
	if cfg.MppMaxShards != 8 {
		t.Fatalf("expected mpp_max_shards default=8, got %d", cfg.MppMaxShards)
	}
	if cfg.MppParallelism != 6 {
		t.Fatalf("expected mpp_parallelism default=6, got %d", cfg.MppParallelism)
	}
	if cfg.MppMinShardSat != 1000 {
		t.Fatalf("expected mpp_min_shard_sat default=1000, got %d", cfg.MppMinShardSat)
	}
	if cfg.MppRoundTimeoutSec != 30 {
		t.Fatalf("expected mpp_round_timeout_sec default=30, got %d", cfg.MppRoundTimeoutSec)
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
	if got.MppMaxShards != 8 {
		t.Fatalf("expected MppMaxShards fallback=8, got %d", got.MppMaxShards)
	}
	if got.MppParallelism != 6 {
		t.Fatalf("expected MppParallelism fallback=6, got %d", got.MppParallelism)
	}
	if got.MppMinShardSat != 1000 {
		t.Fatalf("expected MppMinShardSat fallback=1000, got %d", got.MppMinShardSat)
	}
	if got.MppRoundTimeoutSec != 30 {
		t.Fatalf("expected MppRoundTimeoutSec fallback=30, got %d", got.MppRoundTimeoutSec)
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
	if !shouldCooldownTargetRecentFailures(recentCooldownStat{}, noAttempt, recentCooldownStat{}, now) {
		t.Fatalf("expected repeated no-attempt target failures to trigger target cooldown")
	}

	noAttempt.Successes = 1
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, noAttempt, recentCooldownStat{}, now) {
		t.Fatalf("did not expect no-attempt cooldown after a recent target success")
	}

	noAttempt.Successes = 0
	noAttempt.LastAttemptAt = now.Add(-recentCooldownTTL - time.Second)
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, noAttempt, recentCooldownStat{}, now) {
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
	if !shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, failed, now) {
		t.Fatalf("expected repeated all-sources-failed jobs to trigger target cooldown")
	}

	failed.Attempts = targetFailedCooldownMinFailures - 1
	failed.Failures = failed.Attempts
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, failed, now) {
		t.Fatalf("did not expect all-sources-failed cooldown below failure threshold")
	}

	failed.Attempts = targetFailedCooldownMinFailures
	failed.Failures = targetFailedCooldownMinFailures
	failed.Successes = 1
	if shouldCooldownTargetRecentFailures(recentCooldownStat{}, recentCooldownStat{}, failed, now) {
		t.Fatalf("did not expect all-sources-failed cooldown after a recent target success")
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

func TestShouldEnforceManualRestartBudgetHonorsAutoOnlyMode(t *testing.T) {
	cfg := RebalanceConfig{}
	if !shouldEnforceManualRestartBudget(cfg, "manual", "", true) {
		t.Fatalf("expected manual restart budget enforcement when auto-only mode is disabled")
	}
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
