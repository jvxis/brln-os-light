package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"lightningos-light/internal/lndclient"
)

// Price experiments (adaptive AutoFee, phase 2).
//
// The engine is good at protecting a price and weak at discovering one: a
// channel with stock that does not sell keeps its fee forever because every
// floor and hold says "do not sell cheap". The experiment does what the
// operator did by hand in October 2026: pick a channel with stock and no
// sales, cut once to a bounded test price, hold it there for a week, measure
// sales per day with stock available, and decide. Sold: the test price is the
// channel's new reference. Not sold: revert and remember the channel has no
// demand at that price.
//
// Modes: "off" does nothing; "shadow" records the experiments it would run and
// measures the channel at its unchanged fee, so the operator can audit the
// trigger before any fee moves; "enforce" applies the test fee.
const (
	autofeePriceExperimentModeOff     = "off"
	autofeePriceExperimentModeShadow  = "shadow"
	autofeePriceExperimentModeEnforce = "enforce"

	autofeePriceExperimentMaxActiveDefault    = 2
	autofeePriceExperimentDaysDefault         = 7
	autofeePriceExperimentFloorSeedPctDefault = 50

	autofeePriceExperimentStatusActive    = "active"
	autofeePriceExperimentStatusConcluded = "concluded"

	autofeePriceExperimentVerdictSold         = "sold"
	autofeePriceExperimentVerdictNoDemand     = "no_demand"
	autofeePriceExperimentVerdictInconclusive = "inconclusive"
	autofeePriceExperimentVerdictAborted      = "aborted"

	// Trigger: stock on hand and a week without meaningful sales.
	autofeePriceExperimentMinStockRatio = 0.10
	autofeePriceExperimentIdleDays      = 7
	autofeePriceExperimentMedianDays    = 30
	autofeePriceExperimentLowSalesFrac  = 0.25
	autofeePriceExperimentMinAgeHours   = 7 * 24
	autofeePriceExperimentMinLocalPpm   = 20
	// Cut: the test must be a real cut, and a sink never goes below the cost
	// of the liquidity it would need to buy back, plus the engine's margin.
	autofeePriceExperimentMinCutFrac    = 0.15
	autofeePriceExperimentSinkMarginMul = 1.10
	// Verdict: sold means sales per stocked day at least this many times the
	// baseline and at least this share of capacity moved; a channel that was
	// empty most of the window proves nothing.
	autofeePriceExperimentSoldMult         = 1.5
	autofeePriceExperimentSoldMinCapFrac   = 0.02
	autofeePriceExperimentEarlyStopCapFrac = 0.50
	autofeePriceExperimentMinStockedFrac   = 0.30
	// One experiment per channel per fortnight; a month after "no demand".
	autofeePriceExperimentCooldownDays         = 14
	autofeePriceExperimentNoDemandCooldownDays = 30

	// Loop corridor: two channels whose forwards feed each other (speedupln ↔
	// cyberdyne). Raising or testing the fee on either leg kills the loop.
	autofeeLoopCorridorMinShareFrac = 0.50
	autofeeLoopCorridorMinVolumeSat = 100_000
	autofeeLoopCorridorWindowDays   = 7
)

func normalizeAutofeePriceExperimentMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case autofeePriceExperimentModeShadow:
		return autofeePriceExperimentModeShadow
	case autofeePriceExperimentModeEnforce:
		return autofeePriceExperimentModeEnforce
	default:
		return autofeePriceExperimentModeOff
	}
}

// normalizeAutofeePriceExperimentConfig clamps the knobs: 1-10 channels at a
// time, 3-14 days per test, cut floor between 20% and 90% of the seed.
func normalizeAutofeePriceExperimentConfig(cfg *AutofeeConfig) {
	if cfg == nil {
		return
	}
	cfg.PriceExperimentsMode = normalizeAutofeePriceExperimentMode(cfg.PriceExperimentsMode)
	if cfg.PriceExperimentsMaxActive <= 0 {
		cfg.PriceExperimentsMaxActive = autofeePriceExperimentMaxActiveDefault
	}
	cfg.PriceExperimentsMaxActive = clampInt(cfg.PriceExperimentsMaxActive, 1, 10)
	if cfg.PriceExperimentsDays <= 0 {
		cfg.PriceExperimentsDays = autofeePriceExperimentDaysDefault
	}
	cfg.PriceExperimentsDays = clampInt(cfg.PriceExperimentsDays, 3, 14)
	if cfg.PriceExperimentsFloorSeedPct <= 0 {
		cfg.PriceExperimentsFloorSeedPct = autofeePriceExperimentFloorSeedPctDefault
	}
	cfg.PriceExperimentsFloorSeedPct = clampInt(cfg.PriceExperimentsFloorSeedPct, 20, 90)
}

type autofeePriceExperiment struct {
	ID                    int64     `json:"id"`
	ChannelID             uint64    `json:"channel_id"`
	ChannelPoint          string    `json:"channel_point"`
	Alias                 string    `json:"alias"`
	Mode                  string    `json:"mode"`
	Status                string    `json:"status"`
	StartedAt             time.Time `json:"started_at"`
	EndsAt                time.Time `json:"ends_at"`
	ConcludedAt           time.Time `json:"concluded_at,omitempty"`
	StartPpm              int       `json:"start_ppm"`
	TestPpm               int       `json:"test_ppm"`
	SeedPpm               int       `json:"seed_ppm"`
	ClassLabel            string    `json:"class_label"`
	CapacitySat           int64     `json:"capacity_sat"`
	StartLocalSat         int64     `json:"start_local_sat"`
	BaselineSaleSatPerDay float64   `json:"baseline_sale_sat_per_day"`
	BaselineFeeSatPerDay  float64   `json:"baseline_fee_sat_per_day"`
	MedianSaleSatPerDay   float64   `json:"median_sale_sat_per_day"`
	RunsTotal             int       `json:"runs_total"`
	RunsStocked           int       `json:"runs_stocked"`
	OutRatioSum           float64   `json:"out_ratio_sum"`
	SoldSat               int64     `json:"sold_sat"`
	SoldFeeSat            int64     `json:"sold_fee_sat"`
	Verdict               string    `json:"verdict,omitempty"`
	VerdictNote           string    `json:"verdict_note,omitempty"`
}

// StockedFrac is the share of engine runs during the window in which the
// channel held at least the minimum stock. Sales per day are divided by it so
// an empty channel is not read as "no demand".
func (x autofeePriceExperiment) StockedFrac() float64 {
	if x.RunsTotal <= 0 {
		return 0
	}
	return float64(x.RunsStocked) / float64(x.RunsTotal)
}

func (x autofeePriceExperiment) elapsedDays(now time.Time) float64 {
	d := now.Sub(x.StartedAt).Hours() / 24
	if d < 0 {
		return 0
	}
	return d
}

// SaleSatPerStockedDay: sold volume over the days the channel actually had
// stock. Zero when the channel was never stocked during the window.
func (x autofeePriceExperiment) SaleSatPerStockedDay(now time.Time) float64 {
	days := x.elapsedDays(now) * x.StockedFrac()
	if days <= 0 {
		return 0
	}
	return float64(x.SoldSat) / days
}

// autofeePriceExperimentRuntime is loaded once per engine run.
type autofeePriceExperimentRuntime struct {
	mode          string
	maxActive     int
	days          int
	floorSeedFrac float64
	active        map[uint64]*autofeePriceExperiment
	lastConcluded map[uint64]autofeePriceExperiment
	dailyMedian   map[uint64]float64
	corridors     map[uint64]bool
	known         bool
}

func (rt *autofeePriceExperimentRuntime) activeCount() int {
	if rt == nil {
		return 0
	}
	return len(rt.active)
}

// priceExperimentCandidateInput carries what the trigger needs, already
// computed by evaluateChannel. Kept as a struct so the rule is testable
// without an engine.
type priceExperimentCandidateInput struct {
	Now              time.Time
	LocalPpm         int
	Seed             int
	RebalCostPpm     int
	ClassLabel       string
	OutRatio         float64
	ChannelAgeHours  float64
	Forward7d        forwardStat
	Inbound7d        inboundStat
	MedianSalePerDay float64
	MedianKnown      bool
	LoopCorridor     bool
	SuperSource      bool
	MinPpm           int
	ChannelMinPpm    int
	FloorSeedFrac    float64
	LastConcluded    *autofeePriceExperiment
}

// priceExperimentCandidate decides whether a channel qualifies and at what
// fee. The reason names the first failing rule so the shadow log explains
// every skip.
func priceExperimentCandidate(in priceExperimentCandidateInput) (int, string, bool) {
	if in.LocalPpm < autofeePriceExperimentMinLocalPpm || in.LocalPpm <= in.MinPpm {
		return 0, "fee_already_low", false
	}
	if in.SuperSource {
		return 0, "super_source", false
	}
	if in.LoopCorridor {
		return 0, "loop_corridor", false
	}
	if in.ChannelAgeHours > 0 && in.ChannelAgeHours < autofeePriceExperimentMinAgeHours {
		return 0, "channel_too_young", false
	}
	if in.OutRatio < autofeePriceExperimentMinStockRatio {
		return 0, "no_stock", false
	}
	if in.LastConcluded != nil && !in.LastConcluded.ConcludedAt.IsZero() {
		cooldown := autofeePriceExperimentCooldownDays
		if in.LastConcluded.Verdict == autofeePriceExperimentVerdictNoDemand {
			cooldown = autofeePriceExperimentNoDemandCooldownDays
		}
		if in.Now.Sub(in.LastConcluded.ConcludedAt) < time.Duration(cooldown)*24*time.Hour {
			return 0, "cooldown", false
		}
	}
	salePerDay := float64(in.Forward7d.AmtMsat) / 1000 / autofeePriceExperimentIdleDays
	if in.Forward7d.Count > 0 {
		if !in.MedianKnown || in.MedianSalePerDay <= 0 {
			return 0, "selling", false
		}
		if salePerDay >= in.MedianSalePerDay*autofeePriceExperimentLowSalesFrac {
			return 0, "selling", false
		}
	}
	// Cut floor: a share of the market seed, never below the configured and
	// per-channel minimums. A sink also stays above its replenishment cost.
	floor := 0
	if in.Seed > 0 && in.FloorSeedFrac > 0 {
		floor = ceilPpm(float64(in.Seed) * in.FloorSeedFrac)
	}
	if in.ClassLabel == "sink" {
		if in.RebalCostPpm > 0 {
			costFloor := ceilPpm(float64(in.RebalCostPpm) * autofeePriceExperimentSinkMarginMul)
			if costFloor > floor {
				floor = costFloor
			}
		} else if in.Inbound7d.Count == 0 {
			// One-way sink without a known route: selling below an unknown
			// replenishment price is a loss; the ranking handles closing.
			return 0, "sink_no_cost_reference", false
		}
	}
	if floor <= 0 {
		return 0, "no_reference", false
	}
	floor = maxInt(floor, in.MinPpm)
	floor = maxInt(floor, in.ChannelMinPpm)
	maxTest := int(math.Floor(float64(in.LocalPpm) * (1 - autofeePriceExperimentMinCutFrac)))
	if floor > maxTest {
		return 0, "cut_too_small", false
	}
	return floor, "", true
}

// ceilPpm rounds a fee up, forgiving the float noise that turns 440.0 into
// 440.00000000000006.
func ceilPpm(v float64) int {
	return int(math.Ceil(v - 1e-6))
}

// priceExperimentVerdict reads the measured window. Shadow experiments
// measure the channel at its old fee, so their verdicts are prefixed to keep
// them out of any enforce-side logic.
func priceExperimentVerdict(x autofeePriceExperiment, now time.Time) (string, string) {
	stocked := x.StockedFrac()
	perDay := x.SaleSatPerStockedDay(now)
	capFloor := float64(x.CapacitySat) * autofeePriceExperimentSoldMinCapFrac
	baseline := x.BaselineSaleSatPerDay * autofeePriceExperimentSoldMult
	note := fmt.Sprintf("sold %d sat (%d fee) in %.1f days, stocked %.0f%% of runs, %.0f sat/stocked-day vs baseline %.0f", x.SoldSat, x.SoldFeeSat, x.elapsedDays(now), stocked*100, perDay, x.BaselineSaleSatPerDay)
	verdict := autofeePriceExperimentVerdictNoDemand
	switch {
	case float64(x.SoldSat) >= capFloor && perDay >= baseline && perDay > 0:
		verdict = autofeePriceExperimentVerdictSold
	case stocked < autofeePriceExperimentMinStockedFrac:
		verdict = autofeePriceExperimentVerdictInconclusive
	}
	if x.Mode == autofeePriceExperimentModeShadow {
		verdict = "shadow_" + verdict
	}
	return verdict, note
}

// detectLoopCorridors returns the channels that form crossed pairs: most of
// what leaves X came in through Y and most of what leaves Y came in through X.
func detectLoopCorridors(pairs map[[2]uint64]int64) map[uint64]bool {
	outVol := map[uint64]int64{}
	for key, amt := range pairs {
		outVol[key[1]] += amt
	}
	corridors := map[uint64]bool{}
	for key, amt := range pairs {
		in, out := key[0], key[1]
		if in == 0 || out == 0 || in == out {
			continue
		}
		if outVol[out] < autofeeLoopCorridorMinVolumeSat || float64(amt) < float64(outVol[out])*autofeeLoopCorridorMinShareFrac {
			continue
		}
		back := pairs[[2]uint64{out, in}]
		if outVol[in] < autofeeLoopCorridorMinVolumeSat || float64(back) < float64(outVol[in])*autofeeLoopCorridorMinShareFrac {
			continue
		}
		corridors[in] = true
		corridors[out] = true
	}
	return corridors
}

// dailySaleMedian takes per-day sold volumes (days without sales are absent)
// and the number of days the channel has existed inside the window. Missing
// days count as zero, because "no sale" is a data point.
func dailySaleMedian(daily []int64, days int) float64 {
	if days <= 0 {
		return 0
	}
	if len(daily) > days {
		days = len(daily)
	}
	values := make([]float64, 0, days)
	for _, v := range daily {
		values = append(values, float64(v))
	}
	for len(values) < days {
		values = append(values, 0)
	}
	sort.Float64s(values)
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

func (e *autofeeEngine) loadPriceExperimentRuntime(ctx context.Context, dryRun bool) {
	rt := &autofeePriceExperimentRuntime{
		mode:          normalizeAutofeePriceExperimentMode(e.cfg.PriceExperimentsMode),
		maxActive:     e.cfg.PriceExperimentsMaxActive,
		days:          e.cfg.PriceExperimentsDays,
		floorSeedFrac: float64(e.cfg.PriceExperimentsFloorSeedPct) / 100,
		active:        map[uint64]*autofeePriceExperiment{},
		lastConcluded: map[uint64]autofeePriceExperiment{},
		dailyMedian:   map[uint64]float64{},
		corridors:     map[uint64]bool{},
	}
	e.priceExperiments = rt
	if rt.mode == autofeePriceExperimentModeOff || e.svc == nil || e.svc.db == nil {
		return
	}
	if rt.maxActive <= 0 {
		rt.maxActive = autofeePriceExperimentMaxActiveDefault
	}
	if rt.days <= 0 {
		rt.days = autofeePriceExperimentDaysDefault
	}
	if rt.floorSeedFrac <= 0 {
		rt.floorSeedFrac = float64(autofeePriceExperimentFloorSeedPctDefault) / 100
	}
	// Market refill mode already holds every fee at a snapshot; experiments
	// would fight it.
	if e.cfg.OperationMode == autofeeOperationModeMarketRefill {
		return
	}
	experiments, err := e.svc.loadPriceExperiments(ctx)
	if err != nil {
		if e.svc.logger != nil {
			e.svc.logger.Printf("autofee: price experiments unavailable: %v", err)
		}
		return
	}
	for i := range experiments {
		x := experiments[i]
		switch x.Status {
		case autofeePriceExperimentStatusActive:
			// Experiments opened under another mode stop here: the operator
			// changed their mind, and the fee follows the engine again.
			if x.Mode != rt.mode {
				if !dryRun {
					e.svc.concludePriceExperiment(ctx, x.ID, autofeePriceExperimentVerdictAborted, "mode changed to "+rt.mode, e.now)
				}
				continue
			}
			rt.active[x.ChannelID] = &x
		case autofeePriceExperimentStatusConcluded:
			if prev, ok := rt.lastConcluded[x.ChannelID]; !ok || x.ConcludedAt.After(prev.ConcludedAt) {
				rt.lastConcluded[x.ChannelID] = x
			}
		}
	}
	medians, err := e.fetchDailySaleMedians(ctx)
	if err != nil {
		if e.svc.logger != nil {
			e.svc.logger.Printf("autofee: price experiment medians unavailable: %v", err)
		}
		return
	}
	rt.dailyMedian = medians
	pairs, err := e.fetchForwardPairs(ctx, autofeeLoopCorridorWindowDays)
	if err != nil {
		if e.svc.logger != nil {
			e.svc.logger.Printf("autofee: price experiment forward pairs unavailable: %v", err)
		}
		return
	}
	rt.corridors = detectLoopCorridors(pairs)
	rt.known = true
}

// applyPriceExperiment runs after evaluateChannel and the idle refresh. It
// progresses the channel's active experiment or opens one, and in enforce
// mode overrides the decision with the test fee. Without a complete runtime
// (database error) it only tags, so a bad read never moves a fee.
func (e *autofeeEngine) applyPriceExperiment(ctx context.Context, ch lndclient.ChannelInfo, d *decision, forward7d forwardStat, inbound7d inboundStat, dryRun bool) *decision {
	rt := e.priceExperiments
	if rt == nil || d == nil || rt.mode == autofeePriceExperimentModeOff || !rt.known {
		return d
	}
	if rt.corridors[ch.ChannelID] {
		d.Tags = appendAutofeeTagOnce(d.Tags, "loop-corridor")
	}
	if x, ok := rt.active[ch.ChannelID]; ok {
		return e.progressPriceExperiment(ctx, ch, d, x, dryRun)
	}
	if rt.activeCount() >= rt.maxActive {
		return d
	}
	median, medianKnown := rt.dailyMedian[ch.ChannelID]
	var last *autofeePriceExperiment
	if prev, ok := rt.lastConcluded[ch.ChannelID]; ok {
		last = &prev
	}
	testPpm, _, ok := priceExperimentCandidate(priceExperimentCandidateInput{
		Now:              e.now,
		LocalPpm:         d.LocalPpm,
		Seed:             d.Seed,
		RebalCostPpm:     d.RebalPpm,
		ClassLabel:       d.ClassLabel,
		OutRatio:         d.OutRatio,
		ChannelAgeHours:  d.ChannelAgeHours,
		Forward7d:        forward7d,
		Inbound7d:        inbound7d,
		MedianSalePerDay: median,
		MedianKnown:      medianKnown,
		LoopCorridor:     rt.corridors[ch.ChannelID],
		SuperSource:      d.SuperSourceActive,
		MinPpm:           e.cfg.MinPpm,
		ChannelMinPpm:    e.channelMinPpm[ch.ChannelID],
		FloorSeedFrac:    rt.floorSeedFrac,
		LastConcluded:    last,
	})
	if !ok {
		return d
	}
	x := &autofeePriceExperiment{
		ChannelID:             ch.ChannelID,
		ChannelPoint:          ch.ChannelPoint,
		Alias:                 d.Alias,
		Mode:                  rt.mode,
		Status:                autofeePriceExperimentStatusActive,
		StartedAt:             e.now,
		EndsAt:                e.now.Add(time.Duration(rt.days) * 24 * time.Hour),
		StartPpm:              d.LocalPpm,
		TestPpm:               testPpm,
		SeedPpm:               d.Seed,
		ClassLabel:            d.ClassLabel,
		CapacitySat:           ch.CapacitySat,
		StartLocalSat:         ch.LocalBalanceSat,
		BaselineSaleSatPerDay: float64(forward7d.AmtMsat) / 1000 / autofeePriceExperimentIdleDays,
		BaselineFeeSatPerDay:  float64(forward7d.FeeMsat) / 1000 / autofeePriceExperimentIdleDays,
		MedianSaleSatPerDay:   median,
	}
	if !dryRun {
		if err := e.svc.insertPriceExperiment(ctx, x); err != nil {
			if e.svc.logger != nil {
				e.svc.logger.Printf("autofee: price experiment insert failed: %v", err)
			}
			return d
		}
	}
	rt.active[ch.ChannelID] = x
	d.PriceExperiment = x
	if rt.mode == autofeePriceExperimentModeShadow {
		d.Tags = appendAutofeeTagOnce(d.Tags, fmt.Sprintf("price-experiment-shadow:%d→%d", d.LocalPpm, testPpm))
		return d
	}
	d.Tags = appendAutofeeTagOnce(d.Tags, fmt.Sprintf("price-experiment-start:%d→%d", d.LocalPpm, testPpm))
	return overridePriceExperimentFee(d, testPpm, e.now)
}

func (e *autofeeEngine) progressPriceExperiment(ctx context.Context, ch lndclient.ChannelInfo, d *decision, x *autofeePriceExperiment, dryRun bool) *decision {
	rt := e.priceExperiments
	d.PriceExperiment = x
	x.RunsTotal++
	if d.OutRatio >= autofeePriceExperimentMinStockRatio {
		x.RunsStocked++
	}
	x.OutRatioSum += d.OutRatio
	if !dryRun {
		sold, soldFee, err := e.fetchSoldSince(ctx, ch.ChannelID, x.StartedAt)
		if err == nil {
			x.SoldSat = sold
			x.SoldFeeSat = soldFee
		} else if e.svc.logger != nil {
			e.svc.logger.Printf("autofee: price experiment sales unavailable: %v", err)
		}
		e.svc.updatePriceExperimentProgress(ctx, x)
	}
	earlyStop := rt.mode == autofeePriceExperimentModeEnforce && x.StartLocalSat > 0 && float64(x.SoldSat) >= float64(x.StartLocalSat)*autofeePriceExperimentEarlyStopCapFrac
	if !e.now.Before(x.EndsAt) || earlyStop {
		verdict, note := priceExperimentVerdict(*x, e.now)
		x.Verdict = verdict
		x.VerdictNote = note
		x.Status = autofeePriceExperimentStatusConcluded
		x.ConcludedAt = e.now
		if !dryRun {
			e.svc.concludePriceExperiment(ctx, x.ID, verdict, note, e.now)
		}
		delete(rt.active, ch.ChannelID)
		rt.lastConcluded[ch.ChannelID] = *x
		d.Tags = appendAutofeeTagOnce(d.Tags, "price-experiment-verdict:"+verdict)
		if rt.mode != autofeePriceExperimentModeEnforce {
			return d
		}
		switch verdict {
		case autofeePriceExperimentVerdictSold:
			// The test price is the channel's reference now: it holds for
			// this run and the outrate memory follows it, so from the next
			// run the engine's own anchors keep the fee near what actually
			// sold and surge takes it up from there.
			if d.State != nil {
				d.State.LastOutrate = x.TestPpm
				d.State.LastOutrateTs = e.now
			}
			return overridePriceExperimentFee(d, x.TestPpm, e.now)
		default:
			d.Tags = appendAutofeeTagOnce(d.Tags, "price-experiment-revert")
			return overridePriceExperimentFee(d, x.StartPpm, e.now)
		}
	}
	if rt.mode == autofeePriceExperimentModeShadow {
		d.Tags = appendAutofeeTagOnce(d.Tags, fmt.Sprintf("price-experiment-shadow:%d→%d", x.StartPpm, x.TestPpm))
		return d
	}
	d.Tags = appendAutofeeTagOnce(d.Tags, fmt.Sprintf("price-experiment:%d", x.TestPpm))
	return overridePriceExperimentFee(d, x.TestPpm, e.now)
}

// finishOrphanPriceExperiments closes experiments whose channel the engine
// can no longer steer: closed, or taken out of AutoFee. An inactive peer is
// left alone; it comes back with the test fee still in place.
func (e *autofeeEngine) finishOrphanPriceExperiments(ctx context.Context, channels []lndclient.ChannelInfo, settings map[uint64]bool, dryRun bool) {
	rt := e.priceExperiments
	if rt == nil || rt.mode == autofeePriceExperimentModeOff || !rt.known {
		return
	}
	present := make(map[uint64]bool, len(channels))
	for _, ch := range channels {
		if ch.ChannelID != 0 {
			present[ch.ChannelID] = true
		}
	}
	for channelID, x := range rt.active {
		note := ""
		switch {
		case !present[channelID]:
			note = "channel closed"
		case !autofeeChannelEnabled(settings, channelID):
			note = "autofee disabled for the channel"
		default:
			continue
		}
		x.Status = autofeePriceExperimentStatusConcluded
		x.Verdict = autofeePriceExperimentVerdictAborted
		x.VerdictNote = note
		x.ConcludedAt = e.now
		if !dryRun {
			e.svc.concludePriceExperiment(ctx, x.ID, autofeePriceExperimentVerdictAborted, note, e.now)
		}
		delete(rt.active, channelID)
		rt.lastConcluded[channelID] = *x
	}
}

// overridePriceExperimentFee pins the outbound fee for the experiment. Holds,
// cooldowns and floors computed by evaluateChannel are bypassed for this
// channel only; the inbound discount decided by the engine is kept.
func overridePriceExperimentFee(d *decision, ppm int, now time.Time) *decision {
	if d == nil || ppm <= 0 {
		return d
	}
	d.NewPpm = ppm
	d.TargetFinal = ppm
	d.Apply = ppm != d.LocalPpm || d.InboundDiscount != d.PrevInboundDiscount
	if st := d.State; st != nil {
		st.LastPpm = ppm
		if ppm != d.LocalPpm {
			st.LastTs = now
			if ppm > d.LocalPpm {
				st.LastDir = "up"
			} else {
				st.LastDir = "down"
			}
		}
	}
	return d
}

// --- persistence ---

func (s *AutofeeService) loadPriceExperiments(ctx context.Context) ([]autofeePriceExperiment, error) {
	rows, err := s.db.Query(ctx, `
select id, channel_id, channel_point, coalesce(alias, ''), mode, status, started_at, ends_at, concluded_at,
  start_ppm, test_ppm, seed_ppm, coalesce(class_label, ''), capacity_sat, start_local_sat,
  baseline_sale_sat_per_day, baseline_fee_sat_per_day, median_sale_sat_per_day,
  runs_total, runs_stocked, out_ratio_sum, sold_sat, sold_fee_sat, coalesce(verdict, ''), coalesce(verdict_note, '')
from autofee_price_experiments
where status = 'active' or concluded_at >= now() - interval '60 days'
order by started_at desc
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPriceExperiments(rows)
}

func (s *AutofeeService) ListPriceExperiments(ctx context.Context, limit int) ([]autofeePriceExperiment, error) {
	if s.db == nil {
		return nil, errors.New("db unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
select id, channel_id, channel_point, coalesce(alias, ''), mode, status, started_at, ends_at, concluded_at,
  start_ppm, test_ppm, seed_ppm, coalesce(class_label, ''), capacity_sat, start_local_sat,
  baseline_sale_sat_per_day, baseline_fee_sat_per_day, median_sale_sat_per_day,
  runs_total, runs_stocked, out_ratio_sum, sold_sat, sold_fee_sat, coalesce(verdict, ''), coalesce(verdict_note, '')
from autofee_price_experiments
order by (status = 'active') desc, started_at desc
limit $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPriceExperiments(rows)
}

func scanPriceExperiments(rows pgx.Rows) ([]autofeePriceExperiment, error) {
	out := []autofeePriceExperiment{}
	for rows.Next() {
		var x autofeePriceExperiment
		var channelID int64
		var concluded pgtype.Timestamptz
		if err := rows.Scan(&x.ID, &channelID, &x.ChannelPoint, &x.Alias, &x.Mode, &x.Status, &x.StartedAt, &x.EndsAt, &concluded,
			&x.StartPpm, &x.TestPpm, &x.SeedPpm, &x.ClassLabel, &x.CapacitySat, &x.StartLocalSat,
			&x.BaselineSaleSatPerDay, &x.BaselineFeeSatPerDay, &x.MedianSaleSatPerDay,
			&x.RunsTotal, &x.RunsStocked, &x.OutRatioSum, &x.SoldSat, &x.SoldFeeSat, &x.Verdict, &x.VerdictNote); err != nil {
			return nil, err
		}
		x.ChannelID = uint64(channelID)
		if concluded.Valid {
			x.ConcludedAt = concluded.Time
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *AutofeeService) insertPriceExperiment(ctx context.Context, x *autofeePriceExperiment) error {
	return s.db.QueryRow(ctx, `
insert into autofee_price_experiments (
  channel_id, channel_point, alias, mode, status, started_at, ends_at,
  start_ppm, test_ppm, seed_ppm, class_label, capacity_sat, start_local_sat,
  baseline_sale_sat_per_day, baseline_fee_sat_per_day, median_sale_sat_per_day
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
returning id
`, int64(x.ChannelID), x.ChannelPoint, x.Alias, x.Mode, x.Status, x.StartedAt, x.EndsAt,
		x.StartPpm, x.TestPpm, x.SeedPpm, x.ClassLabel, x.CapacitySat, x.StartLocalSat,
		x.BaselineSaleSatPerDay, x.BaselineFeeSatPerDay, x.MedianSaleSatPerDay).Scan(&x.ID)
}

func (s *AutofeeService) updatePriceExperimentProgress(ctx context.Context, x *autofeePriceExperiment) {
	if x == nil || x.ID == 0 {
		return
	}
	_, err := s.db.Exec(ctx, `
update autofee_price_experiments
set runs_total=$2, runs_stocked=$3, out_ratio_sum=$4, sold_sat=$5, sold_fee_sat=$6, updated_at=now()
where id=$1
`, x.ID, x.RunsTotal, x.RunsStocked, x.OutRatioSum, x.SoldSat, x.SoldFeeSat)
	if err != nil && s.logger != nil {
		s.logger.Printf("autofee: price experiment progress update failed: %v", err)
	}
}

func (s *AutofeeService) concludePriceExperiment(ctx context.Context, id int64, verdict string, note string, at time.Time) {
	if id == 0 {
		return
	}
	_, err := s.db.Exec(ctx, `
update autofee_price_experiments
set status='concluded', verdict=$2, verdict_note=$3, concluded_at=$4, updated_at=now()
where id=$1 and status='active'
`, id, verdict, note, at)
	if err != nil && s.logger != nil {
		s.logger.Printf("autofee: price experiment conclude failed: %v", err)
	}
}

// fetchSoldSince: outbound forward volume and fee of one channel since the
// experiment started. Same fee fallback chain as fetchForwardStats.
func (e *autofeeEngine) fetchSoldSince(ctx context.Context, channelID uint64, since time.Time) (int64, int64, error) {
	var amtMsat, feeMsat int64
	err := e.svc.db.QueryRow(ctx, `
select coalesce(sum(case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end), 0),
  coalesce(sum(
    case
      when fee_msat > 0 then fee_msat
      when fee_sat > 0 then fee_sat * 1000
      when amount_in_msat > 0 and amount_out_msat > 0 and amount_in_msat > amount_out_msat then amount_in_msat - amount_out_msat
      else 0
    end
  ), 0)
from notifications
where type='forward' and occurred_at >= $2
  and coalesce(chan_id_out, channel_id) = $1
`, int64(channelID), since).Scan(&amtMsat, &feeMsat)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, err
	}
	return amtMsat / 1000, feeMsat / 1000, nil
}

// fetchDailySaleMedians: per channel, the median of daily outbound volume
// over the last 30 days, days without sales counted as zero from the first
// day the channel appears in the window.
func (e *autofeeEngine) fetchDailySaleMedians(ctx context.Context) (map[uint64]float64, error) {
	rows, err := e.svc.db.Query(ctx, `
select coalesce(chan_id_out, channel_id) as chan_id,
  date_trunc('day', occurred_at) as day,
  coalesce(sum(case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end), 0) / 1000
from notifications
where type='forward' and occurred_at >= now() - ($1 * interval '1 day')
  and coalesce(chan_id_out, channel_id) is not null
group by 1, 2
`, autofeePriceExperimentMedianDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	daily := map[uint64][]int64{}
	first := map[uint64]time.Time{}
	for rows.Next() {
		var chanID int64
		var day time.Time
		var amt int64
		if err := rows.Scan(&chanID, &day, &amt); err != nil {
			return nil, err
		}
		id := uint64(chanID)
		daily[id] = append(daily[id], amt)
		if t, ok := first[id]; !ok || day.Before(t) {
			first[id] = day
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[uint64]float64, len(daily))
	for id, values := range daily {
		days := autofeePriceExperimentMedianDays
		// A channel that only appears late in the window is measured from
		// its first sale, not from thirty days ago, so a young channel is
		// not read as "sells nothing most days".
		if t, ok := first[id]; ok {
			seen := int(math.Ceil(e.now.Sub(t).Hours()/24)) + 1
			if seen < days {
				days = seen
			}
		}
		out[id] = dailySaleMedian(values, days)
	}
	return out, nil
}

// fetchForwardPairs: outbound volume per (chan_id_in, chan_id_out) pair.
func (e *autofeeEngine) fetchForwardPairs(ctx context.Context, lookback int) (map[[2]uint64]int64, error) {
	rows, err := e.svc.db.Query(ctx, `
select chan_id_in, coalesce(chan_id_out, channel_id),
  coalesce(sum(case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end), 0) / 1000
from notifications
where type='forward' and occurred_at >= now() - ($1 * interval '1 day')
  and chan_id_in is not null and coalesce(chan_id_out, channel_id) is not null
group by 1, 2
`, lookback)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]uint64]int64{}
	for rows.Next() {
		var in, outID, amt int64
		if err := rows.Scan(&in, &outID, &amt); err != nil {
			return nil, err
		}
		out[[2]uint64{uint64(in), uint64(outID)}] = amt
	}
	return out, rows.Err()
}
