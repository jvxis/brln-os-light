package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestAutofeeLowFeeDiscoveryRetainsDownstreamGuards(t *testing.T) {
	for _, scenario := range []string{"cooldown", "reversal", "idle refresh"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			cfg := goldenDefaultCfg()
			cfg.MinPpm, cfg.RebalCostMode = 0, "channel"
			cfg.ExplorerEnabled = false
			if scenario == "cooldown" {
				cfg.CooldownDownSec = 72 * 3600
			}
			e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
			ch := goldenChannel(501, 4_000_000, 3_960_000, 50, true)
			st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 50, LastSeed: 200, FirstSeen: now.Add(-60 * 24 * time.Hour), LastTs: now.Add(-48 * time.Hour)}
			if scenario == "reversal" {
				st.LastDir = "up"
			}
			d := evaluateIdleDiscoveryChannel(e, ch, st, nil)
			switch scenario {
			case "cooldown":
				if d.Apply || !containsTag(d.Tags, "cooldown") || !containsTag(d.Tags, "stale-noflow-micro-step") {
					t.Fatalf("micro-step must still wait for configured cooldown: apply=%v tags=%v", d.Apply, d.Tags)
				}
			case "reversal":
				if d.NewPpm < 50 || containsTag(d.Tags, "stale-noflow-micro-step") {
					t.Fatalf("micro-step must not bypass reversal: new=%d tags=%v", d.NewPpm, d.Tags)
				}
			case "idle refresh":
				e.cfg.IdleRefreshEnabled, e.cfg.NativeSeedEnabled = true, true
				e.nativeSeedCache[ch.RemotePubkey] = autofeeSeedResult{Seed: 200, Ok: true}
				d = e.maybeApplyIdleRefresh(context.Background(), ch, d, forwardStat{}, forwardStat{}, inboundStat{}, rebalStat{}, rebalStat{}, rebalStat{})
				if !d.Apply || d.NewPpm != 46 || !containsTag(d.Tags, "idle-refresh-preserve-decision") {
					t.Fatalf("idle refresh must preserve bounded micro-step: apply=%v new=%d tags=%v", d.Apply, d.NewPpm, d.Tags)
				}
			}
		})
	}
}

func TestAllowStaleNoFlowMicroStep(t *testing.T) {
	for _, tc := range []struct {
		name               string
		discovery, relaxed bool
		local, next        int
		lowSample, want    bool
	}{
		{"bounded low fee", true, true, 50, 46, false, true},
		{"minimum one ppm", true, true, 1, 0, false, true},
		{"disabled discovery", false, true, 50, 46, false, false},
		{"no approved experiment", true, false, 50, 46, false, false},
		{"increase", true, true, 50, 51, false, false},
		{"unchanged", true, true, 50, 50, false, false},
		{"negative", true, true, 1, -1, false, false},
		{"exceeds down cap", true, true, 50, 45, false, false},
		{"ordinary small change", true, true, 500, 499, false, false},
		{"normal cap reaches threshold", true, true, 60, 56, false, false},
		{"low sample bounded", true, true, 50, 47, true, true},
		{"low sample exceeds cap", true, true, 50, 46, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowStaleNoFlowMicroStep(tc.discovery, tc.relaxed, tc.local, tc.next, tc.lowSample); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestAutofeeLowFeeDiscoveryDailyProgressSurvivesStateReload(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	cfg := goldenDefaultCfg()
	cfg.MinPpm, cfg.RebalCostMode = 5, "channel"
	ch := goldenChannel(501, 4_000_000, 3_960_000, 10, true)
	st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 10, LastSeed: 200, FirstSeen: now.Add(-60 * 24 * time.Hour), LastTs: now.Add(-48 * time.Hour)}
	for want := 9; want >= 5; want-- {
		e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
		d := evaluateIdleDiscoveryChannel(e, ch, st, nil)
		if !d.Apply || d.NewPpm != want || !containsTag(d.Tags, "stale-noflow-micro-step") {
			t.Fatalf("want daily progress to %d: new=%d apply=%v tags=%v", want, d.NewPpm, d.Apply, d.Tags)
		}
		ch.FeeRatePpm = goldenInt64Ptr(int64(d.NewPpm))
		data, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		st = &autofeeChannelState{}
		if err := json.Unmarshal(data, st); err != nil {
			t.Fatal(err)
		}
		now = now.Add(25 * time.Hour)
	}
	e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
	d := evaluateIdleDiscoveryChannel(e, ch, st, nil)
	if d.NewPpm < cfg.MinPpm || containsTag(d.Tags, "stale-noflow-micro-step") {
		t.Fatalf("configured minimum must stop discovery: new=%d tags=%v", d.NewPpm, d.Tags)
	}
}

func TestAutofeeLowFeeDiscoveryProtectionMatrix(t *testing.T) {
	for _, scenario := range []string{"disabled", "zero fee forward", "low liquidity", "young inbound", "recent change", "recent rebalance", "weak rebalance", "HTLC pressure"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			cfg := goldenDefaultCfg()
			cfg.MinPpm, cfg.RebalCostMode = 0, "channel"
			ch := goldenChannel(501, 4_000_000, 3_960_000, 50, true)
			st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 50, LastSeed: 200, FirstSeen: now.Add(-60 * 24 * time.Hour), LastTs: now.Add(-48 * time.Hour)}
			fwd := map[uint64]forwardStat{}
			recent := map[uint64]recentRebalanceSignal{}
			htlc := map[uint64]htlcFailureSignal{}
			switch scenario {
			case "disabled":
				cfg.DiscoveryEnabled = false
			case "zero fee forward":
				fwd[ch.ChannelID] = forwardStat{Count: 1, AmtMsat: 100000}
			case "low liquidity":
				ch = goldenChannel(501, 4_000_000, 40_000, 50, true)
			case "young inbound":
				ch = goldenChannel(501, 4_000_000, 3_960_000, 50, false)
				st.FirstSeen = now.Add(-time.Hour)
			case "recent change":
				st.LastTs = now.Add(-23 * time.Hour)
			case "recent rebalance":
				recent[ch.ChannelID] = recentRebalanceSignal{Count: 1, AmtSat: 1000000, FeeMsat: 100000, LastAt: now.Add(-time.Hour)}
			case "weak rebalance":
				recent[ch.ChannelID] = recentRebalanceSignal{RelevanceChecked: true, WeakCount: 1, WeakLastAt: now.Add(-time.Hour)}
			case "HTLC pressure":
				htlc[ch.ChannelID] = htlcFailureSignal{Attempts60m: 30, LiquidityFails60m: 30, LiquidityHot: true}
			}
			e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
			d := e.evaluateChannel(ch, st, fwd, nil, fwd, nil, nil, nil, rebalStats{}, rebalStats{}, rebalStats{}, recent, htlc, 0, 0, false)
			if containsTag(d.Tags, "stale-noflow-micro-step") {
				t.Fatalf("protection must prevent micro-step: new=%d tags=%v", d.NewPpm, d.Tags)
			}
		})
	}
}

func TestAutofeeLowFeeDiscoveryStillEnforcesEconomicIntent(t *testing.T) {
	for _, floor := range []int{48, 50, 70} {
		now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
		cfg := goldenDefaultCfg()
		cfg.MinPpm, cfg.RebalCostMode = 0, "channel"
		e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
		ch := goldenChannel(501, 4_000_000, 3_960_000, 50, true)
		st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: 50, LastSeed: 200, FirstSeen: now.Add(-60 * 24 * time.Hour), LastTs: now.Add(-48 * time.Hour)}
		e.automationIntentConfig = AutomationIntentConfig{Mode: automationIntentModeEnforce, MinConfidence: .70}
		e.automationIntents = map[uint64][]AutomationIntent{ch.ChannelID: {{Kind: automationIntentKindProtectFeeFloor, Confidence: .95, FeeFloorPPM: int64(floor)}}}
		d := evaluateIdleDiscoveryChannel(e, ch, st, nil)
		if d.NewPpm < floor || !containsTag(d.Tags, "intent-protect-fee-floor") {
			t.Fatalf("economic floor %d lost: new=%d tags=%v", floor, d.NewPpm, d.Tags)
		}
	}
}

func TestAutofeeIdleDiscoveryLowFeeMakesBoundedProgress(t *testing.T) {
	for _, tc := range []struct {
		name             string
		local, min, want int
	}{
		{"50 ppm", 50, 0, 46},
		{"10 ppm", 10, 0, 9},
		{"last ppm", 1, 0, 0},
		{"operator minimum", 11, 10, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			cfg := goldenDefaultCfg()
			cfg.MinPpm, cfg.RebalCostMode = tc.min, "channel"
			e := newGoldenEngine(t, cfg, goldenDefaultCalib(), now)
			ch := goldenChannel(501, 4_000_000, 3_960_000, int64(tc.local), true)
			st := &autofeeChannelState{ChannelID: ch.ChannelID, LastPpm: tc.local, LastSeed: 200, FirstSeen: now.Add(-60 * 24 * time.Hour), LastTs: now.Add(-48 * time.Hour)}
			d := evaluateIdleDiscoveryChannel(e, ch, st, nil)
			if !d.Apply || d.NewPpm != tc.want || !containsTag(d.Tags, "stale-noflow-micro-step") {
				t.Fatalf("want bounded %d -> %d; apply=%v new=%d target=%d floor=%d tags=%v", tc.local, tc.want, d.Apply, d.NewPpm, d.TargetRaw, d.Floor, d.Tags)
			}
			if containsTag(d.Tags, "small-delta") || containsTag(d.Tags, "hold-small") {
				t.Fatalf("eligible micro-step still held: %v", d.Tags)
			}
			ch.FeeRatePpm = goldenInt64Ptr(int64(d.NewPpm))
			e.now = now.Add(2 * time.Hour)
			d = evaluateIdleDiscoveryChannel(e, ch, st, nil)
			if d.NewPpm != tc.want || containsTag(d.Tags, "stale-noflow-micro-step") {
				t.Fatalf("must not repeat daily discovery every scan: new=%d tags=%v", d.NewPpm, d.Tags)
			}
		})
	}
}
