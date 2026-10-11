package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func testPriceExperimentInput(now time.Time) priceExperimentCandidateInput {
	return priceExperimentCandidateInput{
		Now:             now,
		LocalPpm:        713,
		Seed:            644,
		ClassLabel:      "router",
		OutRatio:        0.62,
		ChannelAgeHours: 24 * 90,
		Forward7d:       forwardStat{},
		Inbound7d:       inboundStat{Count: 3, AmtMsat: 500_000_000},
		MedianKnown:     true,
		MinPpm:          10,
		FloorSeedFrac:   0.5,
	}
}

func TestPriceExperimentCandidate(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		mutate  func(*priceExperimentCandidateInput)
		wantPpm int
		wantOK  bool
		reason  string
	}{
		{name: "router with stock and no sales cuts to half seed", wantPpm: 322, wantOK: true},
		{name: "no stock", mutate: func(in *priceExperimentCandidateInput) { in.OutRatio = 0.05 }, reason: "no_stock"},
		{name: "fee already low", mutate: func(in *priceExperimentCandidateInput) { in.LocalPpm = 15 }, reason: "fee_already_low"},
		{name: "super source", mutate: func(in *priceExperimentCandidateInput) { in.SuperSource = true }, reason: "super_source"},
		{name: "loop corridor", mutate: func(in *priceExperimentCandidateInput) { in.LoopCorridor = true }, reason: "loop_corridor"},
		{name: "young channel", mutate: func(in *priceExperimentCandidateInput) { in.ChannelAgeHours = 24 * 3 }, reason: "channel_too_young"},
		{
			name: "selling at a quarter of the median or more",
			mutate: func(in *priceExperimentCandidateInput) {
				in.Forward7d = forwardStat{Count: 4, AmtMsat: 7 * 300_000 * 1000}
				in.MedianSalePerDay = 1_000_000
			},
			reason: "selling",
		},
		{
			name: "selling far below the median still qualifies",
			mutate: func(in *priceExperimentCandidateInput) {
				in.Forward7d = forwardStat{Count: 1, AmtMsat: 7 * 100_000 * 1000}
				in.MedianSalePerDay = 1_000_000
			},
			wantPpm: 322,
			wantOK:  true,
		},
		{
			name: "any sale without a median is selling",
			mutate: func(in *priceExperimentCandidateInput) {
				in.Forward7d = forwardStat{Count: 1, AmtMsat: 1000}
				in.MedianKnown = false
			},
			reason: "selling",
		},
		{
			name: "cooldown after a verdict",
			mutate: func(in *priceExperimentCandidateInput) {
				in.LastConcluded = &autofeePriceExperiment{ConcludedAt: now.Add(-10 * 24 * time.Hour), Verdict: autofeePriceExperimentVerdictSold}
			},
			reason: "cooldown",
		},
		{
			name: "no demand keeps the channel out for a month",
			mutate: func(in *priceExperimentCandidateInput) {
				in.LastConcluded = &autofeePriceExperiment{ConcludedAt: now.Add(-20 * 24 * time.Hour), Verdict: autofeePriceExperimentVerdictNoDemand}
			},
			reason: "cooldown",
		},
		{
			name: "cooldown over",
			mutate: func(in *priceExperimentCandidateInput) {
				in.LastConcluded = &autofeePriceExperiment{ConcludedAt: now.Add(-15 * 24 * time.Hour), Verdict: autofeePriceExperimentVerdictSold}
			},
			wantPpm: 322,
			wantOK:  true,
		},
		{
			name:   "sink without cost reference and no inbound is not tested",
			mutate: func(in *priceExperimentCandidateInput) { in.ClassLabel = "sink"; in.Inbound7d = inboundStat{} },
			reason: "sink_no_cost_reference",
		},
		{
			name:    "sink with cheap route floors at cost plus margin",
			mutate:  func(in *priceExperimentCandidateInput) { in.ClassLabel = "sink"; in.RebalCostPpm = 400 },
			wantPpm: 440,
			wantOK:  true,
		},
		{
			name:    "sink with route cheaper than half seed keeps the seed floor",
			mutate:  func(in *priceExperimentCandidateInput) { in.ClassLabel = "sink"; in.RebalCostPpm = 120 },
			wantPpm: 322,
			wantOK:  true,
		},
		{
			name:    "per-channel minimum lifts the test fee",
			mutate:  func(in *priceExperimentCandidateInput) { in.ChannelMinPpm = 500 },
			wantPpm: 500,
			wantOK:  true,
		},
		{
			name:   "cut smaller than 15 percent is not an experiment",
			mutate: func(in *priceExperimentCandidateInput) { in.ChannelMinPpm = 650 },
			reason: "cut_too_small",
		},
		{
			name:   "no seed and no cost gives no reference",
			mutate: func(in *priceExperimentCandidateInput) { in.Seed = 0 },
			reason: "no_reference",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := testPriceExperimentInput(now)
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			ppm, reason, ok := priceExperimentCandidate(in)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (reason %q)", ok, tc.wantOK, reason)
			}
			if ok && ppm != tc.wantPpm {
				t.Fatalf("test ppm=%d want %d", ppm, tc.wantPpm)
			}
			if !ok && reason != tc.reason {
				t.Fatalf("reason=%q want %q", reason, tc.reason)
			}
		})
	}
}

func TestPriceExperimentVerdict(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(7 * 24 * time.Hour)
	base := autofeePriceExperiment{
		Mode:                  autofeePriceExperimentModeEnforce,
		StartedAt:             start,
		CapacitySat:           5_000_000,
		BaselineSaleSatPerDay: 10_000,
		RunsTotal:             168,
		RunsStocked:           168,
	}
	t.Run("sold", func(t *testing.T) {
		x := base
		x.SoldSat = 1_230_000
		verdict, _ := priceExperimentVerdict(x, now)
		if verdict != autofeePriceExperimentVerdictSold {
			t.Fatalf("verdict=%s", verdict)
		}
	})
	t.Run("no demand", func(t *testing.T) {
		x := base
		x.SoldSat = 20_000
		verdict, note := priceExperimentVerdict(x, now)
		if verdict != autofeePriceExperimentVerdictNoDemand {
			t.Fatalf("verdict=%s", verdict)
		}
		if !strings.Contains(note, "sold 20000 sat") {
			t.Fatalf("note=%q", note)
		}
	})
	t.Run("sold volume but below baseline multiple is no demand", func(t *testing.T) {
		x := base
		x.BaselineSaleSatPerDay = 200_000
		x.SoldSat = 1_230_000
		if verdict, _ := priceExperimentVerdict(x, now); verdict != autofeePriceExperimentVerdictNoDemand {
			t.Fatalf("verdict=%s", verdict)
		}
	})
	t.Run("empty channel most of the window is inconclusive", func(t *testing.T) {
		x := base
		x.RunsStocked = 20
		x.SoldSat = 20_000
		if verdict, _ := priceExperimentVerdict(x, now); verdict != autofeePriceExperimentVerdictInconclusive {
			t.Fatalf("verdict=%s", verdict)
		}
	})
	t.Run("sold while mostly empty is still sold", func(t *testing.T) {
		x := base
		x.RunsStocked = 20
		x.SoldSat = 1_230_000
		if verdict, _ := priceExperimentVerdict(x, now); verdict != autofeePriceExperimentVerdictSold {
			t.Fatalf("verdict=%s", verdict)
		}
	})
	t.Run("shadow verdicts are prefixed", func(t *testing.T) {
		x := base
		x.Mode = autofeePriceExperimentModeShadow
		x.SoldSat = 1_230_000
		if verdict, _ := priceExperimentVerdict(x, now); verdict != "shadow_sold" {
			t.Fatalf("verdict=%s", verdict)
		}
	})
}

func TestPriceExperimentSaleSatPerStockedDay(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	x := autofeePriceExperiment{StartedAt: start, RunsTotal: 10, RunsStocked: 5, SoldSat: 100_000}
	got := x.SaleSatPerStockedDay(start.Add(4 * 24 * time.Hour))
	if got != 50_000 {
		t.Fatalf("sale per stocked day=%v want 50000", got)
	}
	if (autofeePriceExperiment{}).SaleSatPerStockedDay(start) != 0 {
		t.Fatal("empty experiment must not divide by zero")
	}
}

func TestDetectLoopCorridors(t *testing.T) {
	pairs := map[[2]uint64]int64{
		{1, 2}: 800_000, // most of what leaves 2 came from 1
		{2, 1}: 600_000, // most of what leaves 1 came from 2
		{3, 1}: 100_000,
		{4, 5}: 900_000, // one way: 5 never feeds 4
		{5, 6}: 900_000,
		{7, 8}: 50_000, // crossed but tiny
		{8, 7}: 50_000,
	}
	got := detectLoopCorridors(pairs)
	for _, id := range []uint64{1, 2} {
		if !got[id] {
			t.Fatalf("channel %d should be a corridor: %v", id, got)
		}
	}
	for _, id := range []uint64{3, 4, 5, 6, 7, 8} {
		if got[id] {
			t.Fatalf("channel %d should not be a corridor: %v", id, got)
		}
	}
}

func TestDailySaleMedian(t *testing.T) {
	if got := dailySaleMedian([]int64{100, 300}, 30); got != 0 {
		t.Fatalf("two selling days in thirty: median=%v want 0", got)
	}
	if got := dailySaleMedian([]int64{100, 300, 200}, 3); got != 200 {
		t.Fatalf("median=%v want 200", got)
	}
	if got := dailySaleMedian([]int64{100, 300}, 4); got != 50 {
		t.Fatalf("median=%v want 50", got)
	}
	if got := dailySaleMedian(nil, 0); got != 0 {
		t.Fatalf("median=%v want 0", got)
	}
}

func TestNormalizeAutofeePriceExperimentConfig(t *testing.T) {
	cfg := AutofeeConfig{PriceExperimentsMode: "ENFORCE", PriceExperimentsMaxActive: 50, PriceExperimentsDays: 1, PriceExperimentsFloorSeedPct: 5}
	normalizeAutofeePriceExperimentConfig(&cfg)
	if cfg.PriceExperimentsMode != autofeePriceExperimentModeEnforce || cfg.PriceExperimentsMaxActive != 10 || cfg.PriceExperimentsDays != 3 || cfg.PriceExperimentsFloorSeedPct != 20 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	cfg = AutofeeConfig{PriceExperimentsMode: "anything"}
	normalizeAutofeePriceExperimentConfig(&cfg)
	if cfg.PriceExperimentsMode != autofeePriceExperimentModeOff || cfg.PriceExperimentsMaxActive != 2 || cfg.PriceExperimentsDays != 7 || cfg.PriceExperimentsFloorSeedPct != 50 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestOverridePriceExperimentFee(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	st := &autofeeChannelState{}
	d := &decision{LocalPpm: 713, NewPpm: 713, Apply: false, State: st}
	overridePriceExperimentFee(d, 322, now)
	if !d.Apply || d.NewPpm != 322 || d.TargetFinal != 322 {
		t.Fatalf("override not applied: %+v", d)
	}
	if st.LastDir != "down" || !st.LastTs.Equal(now) || st.LastPpm != 322 {
		t.Fatalf("state not updated: %+v", st)
	}
	d = &decision{LocalPpm: 322, NewPpm: 400, Apply: true, State: &autofeeChannelState{}}
	overridePriceExperimentFee(d, 322, now)
	if d.Apply {
		t.Fatal("same fee and same inbound discount must not apply")
	}
	d = &decision{LocalPpm: 322, NewPpm: 322, InboundDiscount: -50, PrevInboundDiscount: 0, State: &autofeeChannelState{}}
	overridePriceExperimentFee(d, 322, now)
	if !d.Apply {
		t.Fatal("inbound discount change still applies")
	}
}

func testPriceExperimentEngine(now time.Time, mode string) *autofeeEngine {
	return &autofeeEngine{
		now:           now,
		cfg:           AutofeeConfig{MinPpm: 10, MaxPpm: 2000},
		channelMinPpm: map[uint64]int{},
		priceExperiments: &autofeePriceExperimentRuntime{
			mode:          mode,
			maxActive:     2,
			days:          7,
			floorSeedFrac: 0.5,
			active:        map[uint64]*autofeePriceExperiment{},
			lastConcluded: map[uint64]autofeePriceExperiment{},
			dailyMedian:   map[uint64]float64{},
			corridors:     map[uint64]bool{},
			known:         true,
		},
	}
}

func testPriceExperimentDecision(channelID uint64) *decision {
	return &decision{
		ChannelID:       channelID,
		Alias:           "Open_Hand",
		LocalPpm:        713,
		NewPpm:          713,
		Seed:            644,
		ClassLabel:      "router",
		OutRatio:        0.62,
		ChannelAgeHours: 24 * 90,
		State:           &autofeeChannelState{ChannelID: channelID},
	}
}

func TestApplyPriceExperimentShadowOnlyTags(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	e := testPriceExperimentEngine(now, autofeePriceExperimentModeShadow)
	ch := lndclient.ChannelInfo{ChannelID: 1, ChannelPoint: "ab:0", CapacitySat: 5_000_000, LocalBalanceSat: 3_100_000}
	d := e.applyPriceExperiment(context.Background(), ch, testPriceExperimentDecision(1), forwardStat{}, inboundStat{Count: 2}, true)
	if d.Apply || d.NewPpm != 713 {
		t.Fatalf("shadow must not move the fee: %+v", d)
	}
	if !containsTag(d.Tags, "price-experiment-shadow:713→322") {
		t.Fatalf("tags=%v", d.Tags)
	}
	x := e.priceExperiments.active[1]
	if x == nil || x.TestPpm != 322 || x.StartPpm != 713 || x.Mode != autofeePriceExperimentModeShadow || x.StartLocalSat != 3_100_000 {
		t.Fatalf("experiment not opened: %+v", x)
	}
	if !x.EndsAt.Equal(now.Add(7 * 24 * time.Hour)) {
		t.Fatalf("ends_at=%v", x.EndsAt)
	}
}

func TestApplyPriceExperimentEnforceOverridesAndRespectsSlots(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	e := testPriceExperimentEngine(now, autofeePriceExperimentModeEnforce)
	ctx := context.Background()
	for id := uint64(1); id <= 3; id++ {
		ch := lndclient.ChannelInfo{ChannelID: id, ChannelPoint: "ab:0", CapacitySat: 5_000_000, LocalBalanceSat: 3_000_000}
		d := e.applyPriceExperiment(ctx, ch, testPriceExperimentDecision(id), forwardStat{}, inboundStat{Count: 2}, true)
		if id <= 2 {
			if !d.Apply || d.NewPpm != 322 || !containsTag(d.Tags, "price-experiment-start:713→322") {
				t.Fatalf("channel %d: expected override, got %+v", id, d)
			}
			continue
		}
		if d.Apply || d.NewPpm != 713 || d.PriceExperiment != nil {
			t.Fatalf("third channel must wait for a slot: %+v", d)
		}
	}
	if e.priceExperiments.activeCount() != 2 {
		t.Fatalf("active=%d", e.priceExperiments.activeCount())
	}
}

func TestApplyPriceExperimentHoldsTestFeeAndConcludes(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	e := testPriceExperimentEngine(now, autofeePriceExperimentModeEnforce)
	ctx := context.Background()
	ch := lndclient.ChannelInfo{ChannelID: 1, ChannelPoint: "ab:0", CapacitySat: 5_000_000, LocalBalanceSat: 3_000_000}
	x := &autofeePriceExperiment{
		ChannelID: 1, Mode: autofeePriceExperimentModeEnforce, Status: autofeePriceExperimentStatusActive,
		StartedAt: now.Add(-3 * 24 * time.Hour), EndsAt: now.Add(4 * 24 * time.Hour),
		StartPpm: 713, TestPpm: 322, CapacitySat: 5_000_000, StartLocalSat: 3_000_000,
		RunsTotal: 72, RunsStocked: 72,
	}
	e.priceExperiments.active[1] = x

	// Mid-window: the engine wants 650, the experiment pins 322.
	d := testPriceExperimentDecision(1)
	d.LocalPpm, d.NewPpm, d.Apply = 322, 650, true
	d = e.applyPriceExperiment(ctx, ch, d, forwardStat{}, inboundStat{}, true)
	if d.Apply || d.NewPpm != 322 || !containsTag(d.Tags, "price-experiment:322") {
		t.Fatalf("test fee not held: %+v", d)
	}

	// Window over without sales: revert to the starting fee.
	e.now = now.Add(5 * 24 * time.Hour)
	d = testPriceExperimentDecision(1)
	d.LocalPpm, d.NewPpm = 322, 322
	d = e.applyPriceExperiment(ctx, ch, d, forwardStat{}, inboundStat{}, true)
	if !d.Apply || d.NewPpm != 713 || !containsTag(d.Tags, "price-experiment-revert") {
		t.Fatalf("no demand must revert: %+v", d)
	}
	if _, still := e.priceExperiments.active[1]; still {
		t.Fatal("experiment must leave the active set")
	}
	if last := e.priceExperiments.lastConcluded[1]; last.Verdict != autofeePriceExperimentVerdictNoDemand {
		t.Fatalf("verdict=%q", last.Verdict)
	}

	// The next run must not reopen it: 30-day cooldown after no demand.
	d = testPriceExperimentDecision(1)
	d = e.applyPriceExperiment(ctx, ch, d, forwardStat{}, inboundStat{Count: 2}, true)
	if d.PriceExperiment != nil || d.Apply {
		t.Fatalf("cooldown ignored: %+v", d)
	}
}

func TestApplyPriceExperimentSoldKeepsTestFeeAsReference(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	e := testPriceExperimentEngine(now, autofeePriceExperimentModeEnforce)
	ch := lndclient.ChannelInfo{ChannelID: 1, ChannelPoint: "ab:0", CapacitySat: 5_000_000, LocalBalanceSat: 3_000_000}
	// dryRun skips the sales query, so the sold volume is seeded on the row;
	// the early stop (half the starting stock gone) fires before ends_at.
	x := &autofeePriceExperiment{
		ChannelID: 1, Mode: autofeePriceExperimentModeEnforce, Status: autofeePriceExperimentStatusActive,
		StartedAt: now.Add(-2 * 24 * time.Hour), EndsAt: now.Add(5 * 24 * time.Hour),
		StartPpm: 713, TestPpm: 322, CapacitySat: 5_000_000, StartLocalSat: 3_000_000,
		RunsTotal: 48, RunsStocked: 48, SoldSat: 1_600_000,
	}
	e.priceExperiments.active[1] = x
	d := testPriceExperimentDecision(1)
	d.LocalPpm, d.NewPpm = 322, 322
	d = e.applyPriceExperiment(context.Background(), ch, d, forwardStat{}, inboundStat{}, true)
	if d.Apply || d.NewPpm != 322 {
		t.Fatalf("sold must keep the test fee: %+v", d)
	}
	if !containsTag(d.Tags, "price-experiment-verdict:sold") {
		t.Fatalf("tags=%v", d.Tags)
	}
	if d.State.LastOutrate != 322 || !d.State.LastOutrateTs.Equal(e.now) {
		t.Fatalf("outrate memory must follow the sold price: %+v", d.State)
	}
}

func TestApplyPriceExperimentNeedsCompleteRuntime(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	ch := lndclient.ChannelInfo{ChannelID: 1, ChannelPoint: "ab:0", CapacitySat: 5_000_000, LocalBalanceSat: 3_000_000}
	e := testPriceExperimentEngine(now, autofeePriceExperimentModeEnforce)
	e.priceExperiments.known = false
	d := e.applyPriceExperiment(context.Background(), ch, testPriceExperimentDecision(1), forwardStat{}, inboundStat{Count: 2}, true)
	if d.Apply || d.PriceExperiment != nil || len(d.Tags) != 0 {
		t.Fatalf("incomplete runtime must be inert: %+v", d)
	}
	e = testPriceExperimentEngine(now, autofeePriceExperimentModeOff)
	d = e.applyPriceExperiment(context.Background(), ch, testPriceExperimentDecision(1), forwardStat{}, inboundStat{Count: 2}, true)
	if d.Apply || d.PriceExperiment != nil {
		t.Fatalf("mode off must be inert: %+v", d)
	}
	e = testPriceExperimentEngine(now, autofeePriceExperimentModeEnforce)
	e.priceExperiments.corridors[1] = true
	d = e.applyPriceExperiment(context.Background(), ch, testPriceExperimentDecision(1), forwardStat{}, inboundStat{Count: 2}, true)
	if d.Apply || d.PriceExperiment != nil || !containsTag(d.Tags, "loop-corridor") {
		t.Fatalf("corridor must be tagged and skipped: %+v", d)
	}
}

func TestFinishOrphanPriceExperiments(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	e := testPriceExperimentEngine(now, autofeePriceExperimentModeEnforce)
	for id := uint64(1); id <= 3; id++ {
		e.priceExperiments.active[id] = &autofeePriceExperiment{ChannelID: id, Status: autofeePriceExperimentStatusActive, StartedAt: now.Add(-24 * time.Hour), EndsAt: now.Add(6 * 24 * time.Hour)}
	}
	channels := []lndclient.ChannelInfo{{ChannelID: 1, Active: false}, {ChannelID: 2, Active: true}}
	settings := map[uint64]bool{2: false}
	e.finishOrphanPriceExperiments(context.Background(), channels, settings, true)
	if _, ok := e.priceExperiments.active[1]; !ok {
		t.Fatal("inactive peer keeps its experiment")
	}
	if x := e.priceExperiments.lastConcluded[2]; x.Verdict != autofeePriceExperimentVerdictAborted || x.VerdictNote != "autofee disabled for the channel" {
		t.Fatalf("disabled channel: %+v", x)
	}
	if x := e.priceExperiments.lastConcluded[3]; x.Verdict != autofeePriceExperimentVerdictAborted || x.VerdictNote != "channel closed" {
		t.Fatalf("closed channel: %+v", x)
	}
	if e.priceExperiments.activeCount() != 1 {
		t.Fatalf("active=%d", e.priceExperiments.activeCount())
	}
}
