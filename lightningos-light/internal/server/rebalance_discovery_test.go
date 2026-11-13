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

func TestDiscoveryLadderRestartsAtStepOneEachWindow(t *testing.T) {
	cfg := discoveryTestConfig()
	// Ripio on the Friendspool: a two-step ladder on 10-21, then one probe a
	// day. With a sliding 24h count the 10-21 step 2 was still counted when
	// step 1 expired, so every later ladder started at the ceiling.
	day1 := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	jobs := []sovereignDiscoveryJob{{At: day1}, {At: day1.Add(15 * time.Minute)}}

	stat := sovereignDiscoveryCurrentLadder(jobs, day1.Add(20*time.Minute))
	if stat.Jobs != 2 {
		t.Fatalf("both probes belong to the open ladder, got %+v", stat)
	}
	if _, ok := sovereignDiscoveryNextStep(cfg, stat); ok {
		t.Fatal("the ladder is spent until its window expires")
	}

	// 24h05 after the first probe: step 2 is only 23h50 old, but the ladder
	// it belonged to has expired. A new ladder starts at step 1.
	next := day1.Add(24*time.Hour + 5*time.Minute)
	stat = sovereignDiscoveryCurrentLadder(jobs, next)
	if step, ok := sovereignDiscoveryNextStep(cfg, stat); !ok || step != 1 {
		t.Fatalf("a new window starts the ladder at step 1, got %d %v (%+v)", step, ok, stat)
	}

	// Step 1 of the new ladder ran; the next scan climbs to step 2.
	jobs = append(jobs, sovereignDiscoveryJob{At: next})
	stat = sovereignDiscoveryCurrentLadder(jobs, next.Add(15*time.Minute))
	if step, ok := sovereignDiscoveryNextStep(cfg, stat); !ok || step != 2 {
		t.Fatalf("the second probe of the new ladder is step 2, got %d %v (%+v)", step, ok, stat)
	}

	// A success ends the ladder for its window, not for a sliding 24h.
	success := []sovereignDiscoveryJob{{At: day1, Succeeded: true}}
	if _, ok := sovereignDiscoveryNextStep(cfg, sovereignDiscoveryCurrentLadder(success, day1.Add(23*time.Hour))); ok {
		t.Fatal("a successful probe ends the ladder until its window expires")
	}
	if step, ok := sovereignDiscoveryNextStep(cfg, sovereignDiscoveryCurrentLadder(success, day1.Add(24*time.Hour))); !ok || step != 1 {
		t.Fatalf("after the window a successful target can be probed again from step 1, got %d %v", step, ok)
	}

	if stat := sovereignDiscoveryCurrentLadder(nil, day1); stat.Jobs != 0 {
		t.Fatalf("no jobs, no ladder: %+v", stat)
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
	if !sovereignDiscoveryDrained(lowOutbound, cfg, now) {
		t.Fatal("a low_outbound intent older than the keepalive window qualifies for the ladder")
	}
	productive := drained
	productive.AutomationIntent = &AutomationIntent{Kind: automationIntentKindRefillTarget, ReasonCode: "autofee_productive_refill_target", FirstSeenAt: now.Add(-72 * time.Hour)}
	if sovereignDiscoveryDrained(productive, cfg, now) {
		t.Fatal("a productive refill is not a stuck channel")
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
	// A 1,000-sat probe measures base fees, not the route: the floor holds.
	cfg.MinAmountSat = 1_000
	cfg.MinExecuteSat = 1_000
	if got := sovereignDiscoveryAmount(cfg, RebalanceChannel{TargetAmountSat: 2_000_000}); got != sovereignDiscoveryMinAmountSat {
		t.Fatalf("the probe must not go below %d sats, got %d", sovereignDiscoveryMinAmountSat, got)
	}
	if got := sovereignDiscoveryAmount(cfg, RebalanceChannel{TargetAmountSat: 15_000}); got != 0 {
		t.Fatalf("a deficit below the floor cannot be probed, got %d", got)
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
		"parallel channels": {sovereignTargetStructuralCooldownReason, func(_ *sovereignDiscoveryRun, tg *rebalanceTarget, _ *RebalanceConfig) {
			tg.PeerHasParallelChannels = true
		}},
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

func TestDiscoveryBacksOffAfterRepeatedNoRouteLadders(t *testing.T) {
	cfg := discoveryTestConfig() // 2 steps
	day := func(d int) time.Time { return time.Date(2026, 11, 3, 3, 30, 0, 0, time.UTC).AddDate(0, 0, d) }
	noRoute := func(at time.Time) []sovereignDiscoveryJob {
		return []sovereignDiscoveryJob{{At: at, NoRoute: true}, {At: at.Add(15 * time.Minute), NoRoute: true}}
	}

	// One full no-route ladder: the next window probes again as before.
	jobs := noRoute(day(0))
	stat := sovereignDiscoveryCurrentLadder(jobs, day(1))
	if len(stat.Expired) != 1 || !stat.Expired[0].NoRoute || stat.Expired[0].Jobs != 2 {
		t.Fatalf("expired ladder not summarised: %+v", stat)
	}
	if _, ok := sovereignDiscoveryBackoffUntil(cfg, stat); ok {
		t.Fatal("a single no-route ladder must not back off")
	}

	// Second ladder in a row without a route: wait two windows from its start.
	jobs = append(jobs, noRoute(day(1))...)
	stat = sovereignDiscoveryCurrentLadder(jobs, day(2))
	until, ok := sovereignDiscoveryBackoffUntil(cfg, stat)
	if !ok || !until.Equal(day(1).Add(2*sovereignDiscoveryWindow)) {
		t.Fatalf("two no-route ladders must back off two windows, got %v %v", until, ok)
	}
	if step, ok := sovereignDiscoveryNextStep(cfg, stat); !ok || step != 1 {
		t.Fatalf("the ladder itself is still at step 1 after the window, got %d %v", step, ok)
	}

	// Third: four windows. Fourth and later: still four (cap).
	jobs = append(jobs, noRoute(day(3))...)
	stat = sovereignDiscoveryCurrentLadder(jobs, day(4))
	if until, ok := sovereignDiscoveryBackoffUntil(cfg, stat); !ok || !until.Equal(day(3).Add(4*sovereignDiscoveryWindow)) {
		t.Fatalf("three no-route ladders must back off four windows, got %v %v", until, ok)
	}
	jobs = append(jobs, noRoute(day(7))...)
	stat = sovereignDiscoveryCurrentLadder(jobs, day(8))
	if until, ok := sovereignDiscoveryBackoffUntil(cfg, stat); !ok || !until.Equal(day(7).Add(4*sovereignDiscoveryWindow)) {
		t.Fatalf("the backoff is capped at four windows, got %v %v", until, ok)
	}

	// A success anywhere in the newest ladder ends the streak.
	success := append(noRoute(day(0)), noRoute(day(1))...)
	success = append(success, sovereignDiscoveryJob{At: day(2), NoRoute: true}, sovereignDiscoveryJob{At: day(2).Add(15 * time.Minute), Succeeded: true})
	if _, ok := sovereignDiscoveryBackoffUntil(cfg, sovereignDiscoveryCurrentLadder(success, day(3))); ok {
		t.Fatal("a successful probe must reset the no-route streak")
	}

	// A ladder cut short (one probe, budget ran out) or a skipped probe does
	// not count as "no route at every step".
	short := append(noRoute(day(0)), sovereignDiscoveryJob{At: day(1), NoRoute: true})
	if _, ok := sovereignDiscoveryBackoffUntil(cfg, sovereignDiscoveryCurrentLadder(short, day(2))); ok {
		t.Fatal("a ladder that did not reach every step must not count toward the backoff")
	}
	skipped := append(noRoute(day(0)), sovereignDiscoveryJob{At: day(1), NoRoute: true}, sovereignDiscoveryJob{At: day(1).Add(15 * time.Minute)})
	if _, ok := sovereignDiscoveryBackoffUntil(cfg, sovereignDiscoveryCurrentLadder(skipped, day(2))); ok {
		t.Fatal("a skipped probe (fast path unavailable) must not count as no route")
	}

	// While a ladder is open the expired ones are still carried, but the open
	// ladder is what gates the next step.
	open := append(noRoute(day(0)), noRoute(day(1))...)
	open = append(open, sovereignDiscoveryJob{At: day(3), NoRoute: true})
	stat = sovereignDiscoveryCurrentLadder(open, day(3).Add(20*time.Minute))
	if stat.Jobs != 1 || len(stat.Expired) != 2 {
		t.Fatalf("open ladder must keep its own count and the expired list: %+v", stat)
	}

	// Discovery off: no backoff computed.
	off := cfg
	off.DiscoverySteps = 0
	if _, ok := sovereignDiscoveryBackoffUntil(off, sovereignDiscoveryCurrentLadder(jobs, day(8))); ok {
		t.Fatal("no backoff without a ladder configured")
	}
}
