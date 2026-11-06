package server

import (
	"testing"
	"time"
)

// The fixtures replay the real cases that motivated each rule (see
// docs/insights-recommendations-plan.md). A rule must catch its case and
// stay quiet on the others.

func insightTestNow() time.Time {
	return time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
}

func insightTestRank(point, alias string, fee7d, amt7d, capacity int64, localPct float64) ChannelRankingItem {
	return ChannelRankingItem{
		ChannelPoint:       point,
		ChannelID:          1,
		PeerPubkey:         "02" + alias,
		PeerAlias:          alias,
		Active:             true,
		CapacitySat:        capacity,
		LocalBalanceSat:    int64(float64(capacity) * localPct / 100),
		LocalBalancePct:    localPct,
		ForwardFee7dSat:    fee7d,
		ForwardAmt7dSat:    amt7d,
		ForwardOutCount7d:  12,
		ForwardFee30dSat:   fee7d * 4,
		ForwardAmt30dSat:   amt7d * 4,
		PeerSampleCount30d: 30 * 24,
		State:              "maintain",
	}
}

// Four background sellers so the percentile has a sample to work with.
func insightTestBackground() []ChannelRankingItem {
	return []ChannelRankingItem{
		insightTestRank("bg1:0", "bg1", 300, 1_000_000, 10_000_000, 50),
		insightTestRank("bg2:0", "bg2", 400, 1_000_000, 10_000_000, 50),
		insightTestRank("bg3:0", "bg3", 500, 1_000_000, 10_000_000, 50),
		insightTestRank("bg4:0", "bg4", 600, 1_000_000, 10_000_000, 50),
	}
}

func insightsByRule(items []Insight) map[string][]Insight {
	out := map[string][]Insight{}
	for _, item := range items {
		out[item.Rule] = append(out[item.Rule], item)
	}
	return out
}

func TestInsightRefillSellerStuckAtCap(t *testing.T) {
	now := insightTestNow()
	rank := insightTestRank("e960:1", "e960", 16342, 6_500_000, 5_000_000, 1.4)
	rank.ChannelID = 42
	rank.OutPpm7d = 2500
	rank.ClassLabel = "router"
	reb := RebalanceChannel{ChannelID: 42, ChannelPoint: "e960:1", RemotePubkey: "02e960", LocalPct: 1.4, TargetOutboundPct: 30,
		TargetAmountSat: 1_428_248, FeeBudgetPpm: 2000, EffectiveEconRatio: 0.8, SalePricePpm: 2500, SalePriceSource: "7d",
		RebalanceCost7dPpm: 1950, AutoEnabled: true, AutopilotReason: "target_structural_cooldown", OutgoingFeePpm: 2500}
	intentSince := now.Add(-20 * time.Hour)
	jobs := make([]RebalanceJob, 0, 6)
	for i := 0; i < 5; i++ {
		jobs = append(jobs, RebalanceJob{TargetChannelID: 42, Status: "failed", Reason: "all sources failed"})
	}
	jobs = append(jobs, RebalanceJob{TargetChannelID: 42, Status: "partial", Reason: "remaining 450000 sats"})
	in := insightsInput{
		Ranking:        append(insightTestBackground(), rank),
		Rebalance:      []RebalanceChannel{reb},
		Jobs24h:        jobs,
		RefillIntents:  map[uint64][]AutomationIntent{42: {{ChannelID: 42, Kind: automationIntentKindRefillTarget, Active: true, FirstSeenAt: intentSince, Consumer: automationIntentProducerRebalance}}},
		LoopKnown:      true,
		LoopInstalled:  true,
		AutofeeProfile: autofeeProfiles[autofeeProfileDefault],
	}
	byRule := insightsByRule(buildInsights(in, now))
	r1 := byRule["R1"]
	if len(r1) != 1 {
		t.Fatalf("expected one R1 insight, got %d (%v)", len(r1), byRule)
	}
	ins := r1[0]
	if ins.Kind != insightKindRefillSeller || ins.ChannelID != 42 {
		t.Fatalf("unexpected insight %+v", ins)
	}
	if ins.ValueSatDay <= 0 {
		t.Fatalf("expected positive value, got %d", ins.ValueSatDay)
	}
	tools := map[string]InsightAction{}
	for _, a := range ins.Actions {
		tools[a.Tool] = a
	}
	if _, ok := tools[insightToolRebalance]; !ok {
		t.Fatalf("expected rebalance step, got %+v", ins.Actions)
	}
	loop, ok := tools[insightToolLoopIn]
	if !ok || loop.Params["last_hop_pubkey"] != "02e960" || loop.Params["amount_sat"] != "1428248" {
		t.Fatalf("expected loop in with last hop, got %+v", ins.Actions)
	}
	econ, ok := tools[insightToolEconOverride]
	if !ok || econ.Params["cap_ppm"] != "2250" {
		t.Fatalf("expected econ override at 2250 ppm (paid 1950 of cap 2000, sale 2500), got %+v", ins.Actions)
	}
	if len(byRule["R7"]) != 0 {
		t.Fatalf("R1 and R7 must not fire together: %+v", byRule["R7"])
	}
	if ins.Priority < insightKindFloor(insightKindRefillSeller) {
		t.Fatalf("priority below kind floor: %v", ins.Priority)
	}

	// As a sink the AutoFee floor is cost × 1.14: 2250 × 1.14 = 2565 > 2500,
	// so the tighter cap would not be covered and only Loop In remains.
	sink := rank
	sink.ClassLabel = "sink"
	in.Ranking = append(insightTestBackground(), sink)
	for _, a := range insightsByRule(buildInsights(in, now))["R1"][0].Actions {
		if a.Tool == insightToolEconOverride {
			t.Fatalf("sink must not get an econ override that the floor does not cover: %+v", a)
		}
	}
}

func TestInsightRefillSellerNeedsAgedIntent(t *testing.T) {
	now := insightTestNow()
	rank := insightTestRank("x:1", "x", 9000, 3_000_000, 5_000_000, 2)
	rank.ChannelID = 7
	reb := RebalanceChannel{ChannelID: 7, ChannelPoint: "x:1", LocalPct: 2, TargetOutboundPct: 30, TargetAmountSat: 1_000_000, FeeBudgetPpm: 800, AutopilotReason: "target_structural_cooldown", AutoEnabled: true}
	in := insightsInput{
		Ranking:       append(insightTestBackground(), rank),
		Rebalance:     []RebalanceChannel{reb},
		RefillIntents: map[uint64][]AutomationIntent{7: {{ChannelID: 7, Kind: automationIntentKindRefillTarget, Active: true, FirstSeenAt: now.Add(-2 * time.Hour)}}},
	}
	if got := insightsByRule(buildInsights(in, now))["R1"]; len(got) != 0 {
		t.Fatalf("intent younger than 12h must not trigger R1: %+v", got)
	}
}

func TestInsightExpectedBlockForGuardedSeller(t *testing.T) {
	now := insightTestNow()
	rank := insightTestRank("k:1", "kappa", 6500, 2_000_000, 8_000_000, 5)
	rank.ChannelID = 9
	reb := RebalanceChannel{ChannelID: 9, ChannelPoint: "k:1", LocalPct: 5, TargetOutboundPct: 30, TargetAmountSat: 2_000_000, FeeBudgetPpm: 500, AutopilotReason: "paid_stock_gate", AutoEnabled: true}
	in := insightsInput{Ranking: append(insightTestBackground(), rank), Rebalance: []RebalanceChannel{reb}}
	byRule := insightsByRule(buildInsights(in, now))
	if len(byRule["R7"]) != 1 || byRule["R7"][0].Actions[0].Tool != insightToolNone {
		t.Fatalf("expected informational R7, got %+v", byRule)
	}
	if len(byRule["R1"]) != 0 {
		t.Fatalf("paid_stock_gate is a guard, not a stuck route: %+v", byRule["R1"])
	}
}

func TestInsightStopBuyingUnsoldStock(t *testing.T) {
	now := insightTestNow()
	// BitGo on 03/11: 2.25M bought for 612 sats, nothing sold in 7 days.
	rank := insightTestRank("bitgo:0", "BitGo", 0, 0, 10_000_000, 22)
	rank.ForwardOutCount7d = 0
	rank.RebalFee7dSat = 612
	rank.RebalAmt7dSat = 2_250_000
	reb := RebalanceChannel{ChannelID: 1, ChannelPoint: "bitgo:0", AutoEnabled: true, LocalPct: 22, TargetOutboundPct: 20}
	in := insightsInput{Ranking: append(insightTestBackground(), rank), Rebalance: []RebalanceChannel{reb}}
	byRule := insightsByRule(buildInsights(in, now))
	r3 := byRule["R3"]
	if len(r3) != 1 {
		t.Fatalf("expected R3 for unsold paid stock, got %+v", byRule)
	}
	if r3[0].ValueSatDay != 87 {
		t.Fatalf("avoided cost should be 612/7 = 87 sat/day, got %d", r3[0].ValueSatDay)
	}

	// Same channel but the lot sold: no R3.
	rank.ForwardAmt7dSat = 1_500_000
	rank.ForwardFee7dSat = 900
	in.Ranking = append(insightTestBackground(), rank)
	if got := insightsByRule(buildInsights(in, now))["R3"]; len(got) != 0 {
		t.Fatalf("sold lot must not trigger R3: %+v", got)
	}
}

func TestInsightIdleCapitalRespectsGuards(t *testing.T) {
	now := insightTestNow()
	rank := insightTestRank("bfx:0", "bfx-lnd0", 0, 0, 20_000_000, 75)
	rank.ForwardOutCount7d = 0
	rank.ForwardFee30dSat = 0
	rank.ForwardAmt30dSat = 0
	reb := RebalanceChannel{ChannelID: 1, ChannelPoint: "bfx:0", Initiator: true}
	base := insightsInput{Ranking: append(insightTestBackground(), rank), Rebalance: []RebalanceChannel{reb}, MagmaStateKnown: true}

	byRule := insightsByRule(buildInsights(base, now))
	r4 := byRule["R4"]
	if len(r4) != 1 {
		t.Fatalf("expected R4 for idle capital, got %+v", byRule)
	}
	if r4[0].Confidence != insightConfidenceHigh {
		t.Fatalf("30 days observed should be high confidence, got %s", r4[0].Confidence)
	}
	if !insightGuardsPass(r4[0].Guards) {
		t.Fatalf("guards should pass without magma or parked: %+v", r4[0].Guards)
	}

	// With a Magma commitment the guard fails but the card still explains it.
	withMagma := base
	withMagma.Commitments = []MagmaChannelCommitment{{ChannelPoint: "bfx:0", OrderID: "m1"}}
	r4 = insightsByRule(buildInsights(withMagma, now))["R4"]
	if len(r4) != 1 || insightGuardsPass(r4[0].Guards) {
		t.Fatalf("magma commitment must fail the guard: %+v", r4)
	}

	// Parked channels are the operator's business; no insight at all.
	parked := rank
	parked.AutomationMode = channelAutomationModeParked
	in := base
	in.Ranking = append(insightTestBackground(), parked)
	if got := insightsByRule(buildInsights(in, now))["R4"]; len(got) != 0 {
		t.Fatalf("parked channel must not produce R4: %+v", got)
	}

	// Assisted revenue above 10% of the node's average direct keeps it.
	assisted := rank
	assisted.AssistedForwardFee30dSat = 5000
	in.Ranking = append(insightTestBackground(), assisted)
	if got := insightsByRule(buildInsights(in, now))["R4"]; len(got) != 0 {
		t.Fatalf("assisted revenue must protect the channel: %+v", got)
	}
}

func TestInsightOpenChannelThinSampleStaysLow(t *testing.T) {
	now := insightTestNow()
	in := insightsInput{
		Ranking: insightTestBackground(),
		OpenCandidates: []ChannelOpenCandidateItem{
			{PeerPubkey: "02new", PeerAlias: "new", Score: 71, Confidence: 30, RouteHitCount30d: 3, RouteVolumeSat30d: 2_000_000},
			{PeerPubkey: "02bg1", PeerAlias: "bg1", Score: 90, Confidence: 90, RouteHitCount30d: 20},
			{PeerPubkey: "02weak", PeerAlias: "weak", Score: 40, Confidence: 90, RouteHitCount30d: 20},
		},
	}
	r5 := insightsByRule(buildInsights(in, now))["R5"]
	if len(r5) != 1 || r5[0].PeerPubkey != "02new" {
		t.Fatalf("expected only the new strong candidate, got %+v", r5)
	}
	if r5[0].Confidence != insightConfidenceLow {
		t.Fatalf("n=3 routes with candidate confidence 30 must stay low, got %s", r5[0].Confidence)
	}
}

func TestInsightPriceOffBothDirections(t *testing.T) {
	now := insightTestNow()
	fixed := int64(1300)
	over := insightTestRank("over:0", "over", 100, 200_000, 5_000_000, 40)
	over.FixedFeePPM = &fixed
	autofeeManaged := insightTestRank("auto:0", "auto", 100, 200_000, 5_000_000, 40)
	under := insightTestRank("under:0", "under", 100, 200_000, 5_000_000, 40)
	under.ClassLabel = "sink"
	nearFloor := insightTestRank("near:0", "near", 100, 200_000, 5_000_000, 40)
	rebs := []RebalanceChannel{
		{ChannelID: 1, ChannelPoint: "over:0", OutgoingFeePpm: 1300, SalePricePpm: 900, SalePriceSource: "7d", LocalPct: 40},
		{ChannelID: 2, ChannelPoint: "auto:0", OutgoingFeePpm: 1300, SalePricePpm: 900, SalePriceSource: "7d", LocalPct: 40},
		{ChannelID: 3, ChannelPoint: "under:0", OutgoingFeePpm: 500, RebalanceCost7dPpm: 600, RebalanceAmount7dSat: 700_000, LocalPct: 40},
		{ChannelID: 4, ChannelPoint: "near:0", OutgoingFeePpm: 372, RebalanceCost7dPpm: 329, RebalanceAmount7dSat: 700_000, LocalPct: 40},
	}
	in := insightsInput{Ranking: append(insightTestBackground(), over, autofeeManaged, under, nearFloor), Rebalance: rebs, AutofeeProfile: autofeeProfiles[autofeeProfileDefault]}
	r6 := insightsByRule(buildInsights(in, now))["R6"]
	if len(r6) != 2 {
		t.Fatalf("expected exactly the fixed-fee over and the clear under, got %+v", r6)
	}
	dirs := map[string]string{}
	for _, ins := range r6 {
		dirs[ins.Actions[0].Params["direction"]] = ins.ChannelPoint
	}
	if dirs["over"] != "over:0" || dirs["under"] != "under:0" {
		t.Fatalf("AutoFee-managed over-price and a 1%% floor gap must stay silent, got %v", dirs)
	}
}

func TestInsightOpenChannelKeepsOnlyTheStrongFew(t *testing.T) {
	now := insightTestNow()
	cands := make([]ChannelOpenCandidateItem, 0, 12)
	for i := 0; i < 10; i++ {
		cands = append(cands, ChannelOpenCandidateItem{PeerPubkey: "02c" + string(rune('a'+i)), Score: 90, Confidence: 90, RouteHitCount30d: 20, RouteVolumeSat30d: int64(2_000_000 + i*100_000)})
	}
	cands = append(cands, ChannelOpenCandidateItem{PeerPubkey: "02tiny", Score: 99, Confidence: 99, RouteHitCount30d: 50, RouteVolumeSat30d: 400})
	in := insightsInput{Ranking: insightTestBackground(), OpenCandidates: cands}
	r5 := insightsByRule(buildInsights(in, now))["R5"]
	if len(r5) != insightOpenMaxItems {
		t.Fatalf("expected the top %d candidates, got %d", insightOpenMaxItems, len(r5))
	}
	for _, ins := range r5 {
		if ins.PeerPubkey == "02tiny" {
			t.Fatalf("a candidate with 400 sat of volume must not be suggested")
		}
	}
	if r5[0].PeerPubkey != "02cj" {
		t.Fatalf("expected the highest volume first, got %s", r5[0].PeerPubkey)
	}
}

func TestInsightSellerThreshold(t *testing.T) {
	mk := func(fees ...int64) []insightChannel {
		out := make([]insightChannel, 0, len(fees))
		for _, f := range fees {
			out = append(out, insightChannel{rank: ChannelRankingItem{Active: true, ForwardFee7dSat: f}})
		}
		return out
	}
	if got := insightSellerThreshold(mk(100, 200, 300, 400, 500, 600, 700, 800, 900, 1000)); got != 800 {
		t.Fatalf("p80 of 10 values should be 800, got %d", got)
	}
	if got := insightSellerThreshold(mk(100, 900, 50)); got != 50 {
		t.Fatalf("with fewer than 5 sellers the third best is the floor, got %d", got)
	}
	if got := insightSellerThreshold(mk()); got != 0 {
		t.Fatalf("no sellers means no threshold, got %d", got)
	}
}

func TestInsightEffectiveStatus(t *testing.T) {
	now := insightTestNow()
	past := now.Add(-2 * time.Hour)
	future := now.Add(2 * time.Hour)
	old := now.Add(-8 * 24 * time.Hour)
	cases := []struct {
		name string
		row  insightRow
		want string
	}{
		{"snoozed", insightRow{Status: insightStatusDismissed, DismissReason: insightDismissLater, SnoozeUntil: &future}, insightStatusDismissed},
		{"snooze over", insightRow{Status: insightStatusDismissed, DismissReason: insightDismissLater, SnoozeUntil: &past}, insightStatusOpen},
		{"disagree sticks", insightRow{Status: insightStatusDismissed, DismissReason: insightDismissDisagree}, insightStatusDismissed},
		{"done recent", insightRow{Status: insightStatusDone, ResolvedAt: &past}, insightStatusDone},
		{"done old comes back", insightRow{Status: insightStatusDone, ResolvedAt: &old}, insightStatusOpen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := insightEffectiveStatus(tc.row, now)
			if got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

// A fresh install has no 7-day window to judge by: the engine must stay
// silent even when a channel looks like a textbook case.
func TestInsightsWarmingUpNodeStaysSilent(t *testing.T) {
	now := insightTestNow()
	ranking := insightTestBackground()
	idle := insightTestRank("idle:0", "idle", 0, 0, 5_000_000, 90)
	idle.ForwardFee30dSat, idle.ForwardAmt30dSat, idle.ForwardOutCount7d = 0, 0, 0
	ranking = append(ranking, idle)
	for i := range ranking {
		ranking[i].PeerSampleCount30d = 6 * 24 // six days observed
	}
	in := insightsInput{Ranking: ranking, RefillIntents: map[uint64][]AutomationIntent{}, AutofeeProfile: autofeeProfiles[autofeeProfileDefault]}
	if items := buildInsights(in, now); len(items) != 0 {
		t.Fatalf("expected no insights on a node observed for 6 days, got %d", len(items))
	}
	node := insightNodeInfoFor(ranking)
	if node.Ready || node.ObservedDays != 6 || node.RequiredDays != insightRequiredDays {
		t.Fatalf("unexpected readiness: %+v", node.insightReadiness)
	}
	ranking[len(ranking)-1].PeerSampleCount30d = 7 * 24 // the idle channel itself reaches a week
	in.Ranking = ranking
	if by := insightsByRule(buildInsights(in, now)); len(by["R4"]) != 1 {
		t.Fatalf("expected R4 once the node has a full week, got %+v", by)
	}
}

// The sats thresholds follow the node class AutoFee and the Rebalance Center
// already compute, so a small node sees its own idle capital and candidates.
func TestInsightScaleFollowsNodeClass(t *testing.T) {
	small := []ChannelRankingItem{
		insightTestRank("s1:0", "s1", 50, 100_000, 600_000, 50),
		insightTestRank("s2:0", "s2", 60, 100_000, 600_000, 50),
	}
	calib, scale := insightCalibrate(small)
	if calib.NodeClass != "small" || scale.IdleMinCapacitySat != 250_000 || scale.OpenMinVolumeSat != 250_000 || scale.StopBuyingMinFeeSat != 30 {
		t.Fatalf("small node scale: calib=%+v scale=%+v", calib, scale)
	}
	large := make([]ChannelRankingItem, 0, 70)
	for i := 0; i < 70; i++ {
		item := insightTestRank("l:0", "l", 100, 1_000_000, 5_000_000, 85)
		item.ChannelPoint = item.ChannelPoint + string(rune('a'+i%26))
		large = append(large, item)
	}
	calib, scale = insightCalibrate(large)
	if calib.NodeClass != "large" || calib.LiquidityClass != "full" || scale.IdleMinCapacitySat != 1_000_000 || scale.IdleLocalPct != 70 || scale.OpenMinVolumeSat != 1_000_000 || scale.StopBuyingMinFeeSat != 100 {
		t.Fatalf("large/full node scale: calib=%+v scale=%+v", calib, scale)
	}

	// The same 600k idle channel is invisible on a large node and a finding on a small one.
	now := insightTestNow()
	idle := insightTestRank("idle:0", "idle", 0, 0, 600_000, 90)
	idle.ForwardFee30dSat, idle.ForwardAmt30dSat, idle.ForwardOutCount7d = 0, 0, 0
	in := insightsInput{Ranking: append(append([]ChannelRankingItem{}, small...), idle), RefillIntents: map[uint64][]AutomationIntent{}, AutofeeProfile: autofeeProfiles[autofeeProfileDefault]}
	if by := insightsByRule(buildInsights(in, now)); len(by["R4"]) != 1 {
		t.Fatalf("small node should flag a 600k idle channel, got %+v", by)
	}
	in.Ranking = append(append([]ChannelRankingItem{}, large...), idle)
	if by := insightsByRule(buildInsights(in, now)); len(by["R4"]) != 0 {
		t.Fatalf("large node should ignore a 600k idle channel, got %d", len(by["R4"]))
	}
}
