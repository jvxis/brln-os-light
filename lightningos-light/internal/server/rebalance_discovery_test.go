package server

import (
	"context"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func discoveryTestConfig() RebalanceConfig {
	cfg := defaultRebalanceConfig()
	cfg.DelegatedFastPathEnabled = true
	cfg.DiscoverySteps = 2
	cfg.DiscoveryCeilingPct = 150
	cfg.DiscoveryDailyBudgetSat = 300
	cfg.MinAmountSat = 100_000
	return cfg
}

func TestDiscoveryIsOptInAndNeedsFastPathAndBudget(t *testing.T) {
	def := defaultRebalanceConfig()
	if def.DiscoverySteps != 0 || sovereignDiscoveryConfigured(def) {
		t.Fatal("discovery must be off by default")
	}
	cfg := discoveryTestConfig()
	if !sovereignDiscoveryConfigured(cfg) {
		t.Fatal("expected discovery configured")
	}
	noFastPath := cfg
	noFastPath.DelegatedFastPathEnabled = false
	if sovereignDiscoveryConfigured(noFastPath) {
		t.Fatal("discovery only runs through the delegated fast path")
	}
	noBudget := cfg
	noBudget.DiscoveryDailyBudgetSat = 0
	if sovereignDiscoveryConfigured(noBudget) {
		t.Fatal("a zero discovery budget must disable discovery")
	}
}

func TestDiscoveryStepsClimbToTheCeiling(t *testing.T) {
	cfg := discoveryTestConfig()
	if got := sovereignDiscoveryStepPct(cfg, 1); got != 125 {
		t.Fatalf("step 1 of 2 with a 150%% ceiling must be 125%%, got %d", got)
	}
	if got := sovereignDiscoveryStepPct(cfg, 2); got != 150 {
		t.Fatalf("last step must be the ceiling, got %d", got)
	}
	if got := sovereignDiscoveryStepPct(cfg, 9); got != 150 {
		t.Fatalf("a step beyond the ladder must clamp to the ceiling, got %d", got)
	}
	cfg.DiscoverySteps = 4
	cfg.DiscoveryCeilingPct = 200
	want := []int{125, 150, 175, 200}
	for i, pct := range want {
		if got := sovereignDiscoveryStepPct(cfg, i+1); got != pct {
			t.Fatalf("step %d: want %d, got %d", i+1, pct, got)
		}
	}
	if got := sovereignDiscoveryFeeCapPpm(800, 125); got != 1000 {
		t.Fatalf("125%% of an 800 ppm cap must be 1000 ppm, got %d", got)
	}
	if got := sovereignDiscoveryFeeCapPpm(800, 100); got != 0 {
		t.Fatalf("a step at or below the normal cap is not discovery, got %d", got)
	}
	if got := sovereignDiscoveryCostSat(100_000, 1000); got != 100 {
		t.Fatalf("100k at 1000 ppm costs 100 sats, got %d", got)
	}
}

func TestDiscoveryNextStepStopsOnSuccessAndAfterTheLadder(t *testing.T) {
	cfg := discoveryTestConfig()
	if step, ok := sovereignDiscoveryNextStep(cfg, sovereignDiscoveryTargetStat{}); !ok || step != 1 {
		t.Fatalf("a fresh target starts at step 1, got %d %v", step, ok)
	}
	if step, ok := sovereignDiscoveryNextStep(cfg, sovereignDiscoveryTargetStat{Jobs: 1}); !ok || step != 2 {
		t.Fatalf("after one failed probe the next step is 2, got %d %v", step, ok)
	}
	if _, ok := sovereignDiscoveryNextStep(cfg, sovereignDiscoveryTargetStat{Jobs: 2}); ok {
		t.Fatal("the ladder is spent after its last step until the window expires")
	}
	if _, ok := sovereignDiscoveryNextStep(cfg, sovereignDiscoveryTargetStat{Jobs: 1, Succeeded: true}); ok {
		t.Fatal("a successful probe ends the ladder")
	}
}

func TestDiscoveryOnlyForLongDrainedRouteGatedTargets(t *testing.T) {
	cfg := discoveryTestConfig()
	cfg.KeepaliveRefillAfterHours = 48
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	drained := rebalanceTarget{AutomationIntent: &AutomationIntent{
		Kind: automationIntentKindRefillTarget, ReasonCode: "autofee_drained_target", FirstSeenAt: now.Add(-72 * time.Hour)}}
	if !sovereignDiscoveryDrained(drained, cfg, now) {
		t.Fatal("a target drained for 72h must be eligible")
	}
	recent := drained
	recent.AutomationIntent = &AutomationIntent{Kind: automationIntentKindRefillTarget, ReasonCode: "autofee_drained_target", FirstSeenAt: now.Add(-6 * time.Hour)}
	if sovereignDiscoveryDrained(recent, cfg, now) {
		t.Fatal("a target drained for 6h must wait for the normal ladder")
	}
	lowOutbound := drained
	lowOutbound.AutomationIntent = &AutomationIntent{Kind: automationIntentKindRefillTarget, ReasonCode: "autofee_low_outbound_target", FirstSeenAt: now.Add(-72 * time.Hour)}
	if sovereignDiscoveryDrained(lowOutbound, cfg, now) {
		t.Fatal("only drained/extreme-drained intents qualify")
	}
	if sovereignDiscoveryDrained(rebalanceTarget{}, cfg, now) {
		t.Fatal("no intent, no discovery")
	}

	for reason, want := range map[string]bool{
		sovereignTargetStructuralCooldownReason:    true,
		sovereignRouteDeadOpportunityReason:        true,
		sovereignLowSuccessOpportunityReason:       true,
		sovereignStockGateReason:                   false,
		sovereignUnsoldPaidLiquidityReason:         false,
		sovereignBudgetEfficiencyOpportunityReason: false,
		"expected_profit_below_min":                false,
		"cycle_limit":                              false,
	} {
		if got := isSovereignRouteGateReason(reason); got != want {
			t.Fatalf("route gate reason %q: want %v, got %v", reason, want, got)
		}
	}
}

func TestDiscoveryAmountIsTheOperatorMinimum(t *testing.T) {
	cfg := discoveryTestConfig()
	if got := sovereignDiscoveryAmount(cfg, RebalanceChannel{TargetAmountSat: 2_000_000}); got != 100_000 {
		t.Fatalf("discovery uses min_amount_sat, got %d", got)
	}
	if got := sovereignDiscoveryAmount(cfg, RebalanceChannel{TargetAmountSat: 40_000}); got != 0 {
		t.Fatalf("a deficit below the minimum cannot be probed, got %d", got)
	}
	cfg.MinAmountSat = 50_000
	if got := sovereignDiscoveryAmount(cfg, RebalanceChannel{TargetAmountSat: 2_000_000}); got != 50_000 {
		t.Fatalf("lowering the minimum lowers the probe, got %d", got)
	}
}

func TestDiscoveryJobIdentityAndReasons(t *testing.T) {
	if !isSovereignDiscoveryJob("auto", sovereignDiscoveryReason) {
		t.Fatal("expected a discovery job")
	}
	if isSovereignDiscoveryJob("auto", rebalanceSovereignReason) || isSovereignDiscoveryJob("manual", sovereignDiscoveryReason) {
		t.Fatal("normal sovereign and manual jobs are not discovery")
	}
	// A failed probe must not look like the structural failure the target
	// cooldown counts, nor like a normal Sovereign lot.
	if reason := sovereignDiscoveryNoRouteReason(1250); reason == "all sources failed" || reason != "discovery: no route up to 1250 ppm" {
		t.Fatalf("unexpected no-route reason %q", reason)
	}
	if sovereignDiscoveryReason == rebalanceSovereignReason {
		t.Fatal("discovery lots must not count as Sovereign paid stock")
	}

	bad := discoveryTestConfig()
	bad.DiscoverySteps = 9
	bad.DiscoveryCeilingPct = 500
	bad.DiscoveryDailyBudgetSat = -1
	bad = normalizeRebalanceConfig(bad)
	if bad.DiscoverySteps != 0 || bad.DiscoveryCeilingPct != discoveryCeilingPctDefault || bad.DiscoveryDailyBudgetSat != discoveryDailyBudgetSatDefault {
		t.Fatalf("out-of-range discovery knobs must fall back safely: %d %d %d", bad.DiscoverySteps, bad.DiscoveryCeilingPct, bad.DiscoveryDailyBudgetSat)
	}
}

func TestDiscoveryNeverQueuesWithoutKnownState(t *testing.T) {
	cfg := discoveryTestConfig()
	cfg.KeepaliveRefillAfterHours = 48
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	target := rebalanceTarget{
		Channel: RebalanceChannel{ChannelID: 1, CapacitySat: 5_000_000, TargetAmountSat: 500_000, OutgoingFeePpm: 1_000},
		AutomationIntent: &AutomationIntent{Kind: automationIntentKindRefillTarget, ReasonCode: "autofee_drained_target",
			FirstSeenAt: now.Add(-72 * time.Hour)},
	}
	policy := lndclient.ChannelPolicySnapshot{FeeRatePpm: 1_000}
	// No database: the ladder history and today's spend are unknown, so the
	// probe must not be queued, and the decision must stay untouched.
	svc := NewRebalanceService(nil, nil, nil)
	run := &sovereignDiscoveryRun{}
	decision := RebalanceSovereignDecision{ChannelID: 1, Reason: sovereignTargetStructuralCooldownReason}
	if svc.maybeQueueSovereignDiscovery(context.Background(), &decision, target, cfg, policy, run, now, false) {
		t.Fatal("discovery must fail closed when its state is unavailable")
	}
	if decision.Discovery || decision.Reason != sovereignTargetStructuralCooldownReason || run.queued != 0 {
		t.Fatalf("decision must be left as it was: %+v", decision)
	}

	known := func() *sovereignDiscoveryRun {
		return &sovereignDiscoveryRun{loaded: true, state: sovereignDiscoveryState{ByTarget: map[uint64]sovereignDiscoveryTargetStat{}}}
	}
	// Shadow mode with a known, empty history: step 1 at 125% of the cap.
	run = known()
	decision = RebalanceSovereignDecision{ChannelID: 1, Reason: sovereignTargetStructuralCooldownReason}
	if !svc.maybeQueueSovereignDiscovery(context.Background(), &decision, target, cfg, policy, run, now, false) {
		t.Fatal("expected a shadow discovery decision")
	}
	baseMsat, _ := calcFeeLimitMsat(100_000*1000, policy, nil, cfg)
	wantCap := feeMsatToPpm(baseMsat, 100_000) * 125 / 100
	if !decision.Discovery || decision.DiscoveryStep != 1 || decision.DiscoveryFeeCapPpm != wantCap || decision.AmountSat != 100_000 || decision.Reason != sovereignDiscoveryShadowReason || decision.Selected {
		t.Fatalf("unexpected discovery decision (want cap %d): %+v", wantCap, decision)
	}
	// One probe per scan.
	second := RebalanceSovereignDecision{ChannelID: 2, Reason: sovereignRouteDeadOpportunityReason}
	if svc.maybeQueueSovereignDiscovery(context.Background(), &second, target, cfg, policy, run, now, false) {
		t.Fatal("only one discovery probe per scan")
	}

	// Not a route gate, a probe already in flight, a spent budget, a channel
	// not drained for long enough: no discovery.
	for name, tc := range map[string]struct {
		reason string
		mutate func(*sovereignDiscoveryRun, *rebalanceTarget, *RebalanceConfig)
	}{
		"economic skip":   {"expected_profit_below_min", func(*sovereignDiscoveryRun, *rebalanceTarget, *RebalanceConfig) {}},
		"stock gate":      {sovereignStockGateReason, func(*sovereignDiscoveryRun, *rebalanceTarget, *RebalanceConfig) {}},
		"probe in flight": {sovereignTargetStructuralCooldownReason, func(r *sovereignDiscoveryRun, _ *rebalanceTarget, _ *RebalanceConfig) { r.state.InFlight = 1 }},
		"budget spent":    {sovereignTargetStructuralCooldownReason, func(r *sovereignDiscoveryRun, _ *rebalanceTarget, _ *RebalanceConfig) { r.state.SpentTodaySat = 250 }},
		"ladder spent": {sovereignTargetStructuralCooldownReason, func(r *sovereignDiscoveryRun, _ *rebalanceTarget, _ *RebalanceConfig) {
			r.state.ByTarget[1] = sovereignDiscoveryTargetStat{Jobs: 2}
		}},
		"recently drained": {sovereignTargetStructuralCooldownReason, func(_ *sovereignDiscoveryRun, tg *rebalanceTarget, _ *RebalanceConfig) {
			tg.AutomationIntent = &AutomationIntent{Kind: automationIntentKindRefillTarget, ReasonCode: "autofee_drained_target", FirstSeenAt: now.Add(-time.Hour)}
		}},
		"discovery off": {sovereignTargetStructuralCooldownReason, func(_ *sovereignDiscoveryRun, _ *rebalanceTarget, c *RebalanceConfig) { c.DiscoverySteps = 0 }},
	} {
		r := known()
		tg := target
		c := cfg
		tc.mutate(r, &tg, &c)
		d := RebalanceSovereignDecision{ChannelID: 1, Reason: tc.reason}
		if svc.maybeQueueSovereignDiscovery(context.Background(), &d, tg, c, policy, r, now, false) || d.Discovery || d.Reason != tc.reason {
			t.Fatalf("%s: discovery must not be queued: %+v", name, d)
		}
	}
}
