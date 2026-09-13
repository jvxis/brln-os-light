package server

import (
	"context"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func TestAutofeeStaleReferenceFloorSafetyMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, src, base                                                              string
		target, floor                                                                int
		liquidity, elapsed                                                           float64
		market, flow, out, rebal, weak, cost, pressure, forwardHot, bootstrap, young bool
		wantRelax                                                                    bool
	}{
		{name: "rescue seed above current", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, wantRelax: true},
		{name: "amboss or native seed flat target", src: "no-signal", base: "seed", target: 740, floor: 740, liquidity: .49, elapsed: 48, wantRelax: true},
		{name: "ranking wrapper", src: "rank", base: "seed", target: 278, floor: 740, liquidity: .49, elapsed: 48, wantRelax: true},
		{name: "revenue fallback wrapper", src: "revfloor", base: "seed", target: 278, floor: 740, liquidity: .49, elapsed: 48, wantRelax: true},
		{name: "real rescue cost", src: "rescue", base: "rebal", target: 278, floor: 821, liquidity: .49, elapsed: 48},
		{name: "real revenue reference", src: "revfloor", base: "outrate", target: 278, floor: 821, liquidity: .49, elapsed: 48},
		{name: "hard cost", src: "rebal-sink", base: "rebal", target: 278, floor: 821, liquidity: .49, elapsed: 48},
		{name: "low liquidity", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .05, elapsed: 48},
		{name: "just changed", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 0},
		{name: "future last change", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: -1},
		{name: "recent change", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 23},
		{name: "up intent", src: "rescue", base: "seed", target: 800, floor: 821, liquidity: .49, elapsed: 48},
		{name: "not blocked", src: "rescue", base: "seed", target: 278, floor: 700, liquidity: .49, elapsed: 48},
		{name: "market refill", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, market: true},
		{name: "flow", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, flow: true},
		{name: "out rate", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, out: true},
		{name: "rebalance history", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, rebal: true},
		{name: "weak recent rebalance", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, weak: true},
		{name: "recent cost", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, cost: true},
		{name: "liquidity pressure", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, pressure: true},
		{name: "forward pressure", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, forwardHot: true},
		{name: "new inbound", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, bootstrap: true},
		{name: "young outbound", src: "rescue", base: "seed", target: 278, floor: 821, liquidity: .49, elapsed: 48, young: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flag := func(b bool) int {
				if b {
					return 1
				}
				return 0
			}
			age := float64(30 * 24)
			if tc.young {
				age = 1
			}
			floor, src, tags := relaxStaleNoFlowAdvisoryFloor(tc.market, 740, tc.target, tc.floor, tc.src, tc.base,
				outRatioNormalizationMeta{Raw: .99, Effective: tc.liquidity}, .20, !tc.flow,
				flag(tc.out), flag(tc.rebal), 0, flag(tc.weak), flag(tc.cost), tc.pressure, tc.forwardHot,
				tc.bootstrap, age, defaultBootstrapHours, tc.elapsed, 10)
			if tc.wantRelax {
				if floor != 710 || src != "stale-noflow" || !containsTag(tags, "advisory-floor-relax") {
					t.Fatalf("expected bounded 30 ppm experiment: floor=%d src=%s tags=%v", floor, src, tags)
				}
			} else if floor != tc.floor || src != tc.src || len(tags) != 0 {
				t.Fatalf("protection changed: floor=%d src=%s tags=%v", floor, src, tags)
			}
		})
	}
}

func evaluateIdleDiscoveryChannel(e *autofeeEngine, ch lndclient.ChannelInfo, st *autofeeChannelState, fwd7d map[uint64]forwardStat) *decision {
	return e.evaluateChannel(ch, st, fwd7d, nil, fwd7d, nil, nil, nil,
		rebalStats{}, rebalStats{}, rebalStats{}, nil, nil, 0, 0, false)
}

func TestAutofeeIdleDiscoveryFullChannelProgressesBelowSeed(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	cfg := goldenDefaultCfg()
	cfg.RebalCostMode = "channel"
	e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
	ch := goldenChannel(310, 4_000_000, 3_960_000, 1000, true)
	st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 1000, LastSeed: 1400, FirstSeen: now.Add(-60 * 24 * time.Hour), LastTs: now.Add(-48 * time.Hour)}
	for round := 0; round < 3; round++ {
		before := int(*ch.FeeRatePpm)
		d := evaluateIdleDiscoveryChannel(e, ch, st, nil)
		if !d.Apply || d.NewPpm >= before || before-d.NewPpm > staleNoFlowDownMaxStepPpm || !containsTag(d.Tags, "stale-noflow-target-relax") {
			t.Fatalf("round %d: expected gradual down below unchanged seed: local=%d new=%d floor=%d/%s raw=%d tags=%v", round, before, d.NewPpm, d.Floor, d.FloorSrc, d.TargetRaw, d.Tags)
		}
		ch.FeeRatePpm = goldenInt64Ptr(int64(d.NewPpm))
		// A second scan must not turn the daily experiment into per-run decay.
		e.now = e.now.Add(time.Hour)
		recheck := evaluateIdleDiscoveryChannel(e, ch, st, nil)
		if containsTag(recheck.Tags, "stale-noflow-target-relax") {
			t.Fatalf("round %d: stale experiment repeated too soon: %v", round, recheck.Tags)
		}
		e.now = e.now.Add(25 * time.Hour)
	}
}

func TestAutofeeIdleDiscoveryStillEnforcesInterlockFloor(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	cfg := goldenDefaultCfg()
	cfg.RebalCostMode = "channel"
	e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
	ch := goldenChannel(310, 4_000_000, 3_960_000, 1000, true)
	e.automationIntentConfig = AutomationIntentConfig{Mode: automationIntentModeEnforce, MinConfidence: .70}
	e.automationIntents = map[uint64][]AutomationIntent{ch.ChannelID: {{Kind: automationIntentKindProtectFeeFloor, Confidence: .95, FeeFloorPPM: 1000}}}
	st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 1000, LastSeed: 1400, FirstSeen: now.Add(-60 * 24 * time.Hour), LastTs: now.Add(-48 * time.Hour)}
	d := evaluateIdleDiscoveryChannel(e, ch, st, nil)
	if !containsTag(d.Tags, "stale-noflow-target-relax") || !containsTag(d.Tags, "intent-protect-fee-floor") || d.NewPpm < 1000 {
		t.Fatalf("discovery must remain subordinate to economic protection: new=%d tags=%v", d.NewPpm, d.Tags)
	}
}

func TestAutofeeIdleRefreshCannotUndoDiscoveryWithCheaperProtectedReference(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	cfg := goldenDefaultCfg()
	cfg.IdleRefreshEnabled = true
	cfg.NativeSeedEnabled = true
	e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
	ch := goldenChannel(310, 4_000_000, 3_960_000, 1000, true)
	e.nativeSeedCache[ch.RemotePubkey] = autofeeSeedResult{Seed: 500, Ok: true}
	e.automationIntentConfig = AutomationIntentConfig{Mode: automationIntentModeEnforce, MinConfidence: .70}
	e.automationIntents = map[uint64][]AutomationIntent{ch.ChannelID: {{Kind: automationIntentKindProtectFeeFloor, Confidence: .95, FeeFloorPPM: 700}}}
	st := &autofeeChannelState{LastPpm: 960, LastTs: now}
	d := &decision{LocalPpm: 1000, NewPpm: 960, Target: 960, Apply: true, State: st}
	d = e.maybeApplyIdleRefresh(context.Background(), ch, d, forwardStat{}, forwardStat{}, inboundStat{}, rebalStat{}, rebalStat{}, rebalStat{})
	if d.NewPpm != 960 || !d.Apply || !st.LastIdleRefreshTs.IsZero() || !containsTag(d.Tags, "idle-refresh-preserve-decision") {
		t.Fatalf("700 ppm floor is not permission to replace a bounded 960 ppm decrease: %+v", d)
	}
}

func TestAutofeeIdleDiscoveryPreservesZeroFeeTrafficAndDisabledDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name      string
		discovery bool
		fwd       forwardStat
	}{
		{"zero fee traffic", true, forwardStat{Count: 1, AmtMsat: 100000}},
		{"discovery disabled", false, forwardStat{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
			cfg := goldenDefaultCfg()
			cfg.DiscoveryEnabled = tc.discovery
			cfg.RebalCostMode = "channel"
			e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
			ch := goldenChannel(310, 4_000_000, 3_960_000, 1000, true)
			st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 1000, LastSeed: 1400, FirstSeen: now.Add(-60 * 24 * time.Hour), LastTs: now.Add(-48 * time.Hour)}
			d := evaluateIdleDiscoveryChannel(e, ch, st, map[uint64]forwardStat{ch.ChannelID: tc.fwd})
			if containsTag(d.Tags, "stale-noflow-target-relax") {
				t.Fatalf("unexpected no-demand experiment: %v", d.Tags)
			}
		})
	}
}

func TestAutofeeIdleRefreshPreservesNormalDecision(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		next, target, refresh int
		good                  bool
		src, base             string
		floor                 int
		want                  bool
	}{
		{"normal down vs seed up", 670, 600, 728, true, "seed", "seed", 500, true},
		{"normal down vs larger seed down", 670, 600, 400, true, "seed", "seed", 500, true},
		{"down intent held", 700, 600, 728, false, "rescue", "seed", 730, true},
		{"good liquidity no demand", 700, 700, 728, true, "no-signal", "seed", 700, true},
		{"low liquidity bootstrap unchanged", 700, 700, 728, false, "seed", "seed", 700, false},
		{"hard floor", 700, 700, 600, false, "rebal-sink", "rebal", 700, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &decision{LocalPpm: 700, NewPpm: tc.next, Target: tc.target, Floor: tc.floor, FloorSrc: tc.src, FloorBaseSrc: tc.base}
			if got := shouldPreserveAutofeeDecisionOnIdleRefresh(d, tc.refresh, tc.good); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestAutofeeIdleRefreshEngineIntervalAndEconomicFloor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		floor   int
		mode    string
		want    int
		tag     string
	}{
		{"changed seed waits", time.Hour, 0, automationIntentModeOff, 700, "idle-refresh-wait"},
		{"interval expires", 7 * 24 * time.Hour, 0, automationIntentModeOff, 910, "idle-refresh"},
		{"enforced floor bypasses interval", time.Hour, 1000, automationIntentModeEnforce, 1000, "idle-refresh-intent-floor"},
		{"shadow cannot bypass interval", time.Hour, 1000, automationIntentModeShadow, 700, "idle-refresh-wait"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
			cfg := goldenDefaultCfg()
			cfg.IdleRefreshEnabled = true
			cfg.NativeSeedEnabled = true
			e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
			ch := goldenChannel(310, 4_000_000, 40_000, 700, false)
			e.nativeSeedCache[ch.RemotePubkey] = autofeeSeedResult{Seed: 700, Ok: true}
			e.automationIntentConfig = AutomationIntentConfig{Mode: tc.mode, MinConfidence: .70}
			e.automationIntents = map[uint64][]AutomationIntent{ch.ChannelID: {{Kind: automationIntentKindProtectFeeFloor, Confidence: .95, FeeFloorPPM: int64(tc.floor)}}}
			st := &autofeeChannelState{LastIdleRefreshTs: now.Add(-tc.elapsed), LastIdleRefreshPpm: 650}
			d := &decision{LocalPpm: 700, NewPpm: 700, Target: 700, State: st}
			d = e.maybeApplyIdleRefresh(context.Background(), ch, d, forwardStat{}, forwardStat{}, inboundStat{}, rebalStat{}, rebalStat{}, rebalStat{})
			if d.NewPpm != tc.want || !containsTag(d.Tags, tc.tag) {
				t.Fatalf("got %d want %d tag=%s tags=%v", d.NewPpm, tc.want, tc.tag, d.Tags)
			}
		})
	}
}
