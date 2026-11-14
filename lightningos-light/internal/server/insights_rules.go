package server

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

// Insights cross-reads the signals that other modules already compute
// (channel ranking, rebalance, automation intents, open candidates, Magma)
// and turns them into a short list of recommendations. It never executes
// anything: every action points at the screen that already does the work.
//
// The backend speaks in codes only (kind, evidence keys, tools, guards); the
// UI owns every sentence through i18n, so the same payload renders in both
// languages.

const (
	insightKindRefillSeller    = "repor_vendedor"
	insightKindMoreCapacity    = "mais_capacidade"
	insightKindStopBuying      = "parar_compra"
	insightKindIdleCapital     = "capital_parado"
	insightKindOpenChannel     = "abrir_canal"
	insightKindPriceOff        = "preco_fora"
	insightKindExpectedBlock   = "bloqueio_esperado"
	insightKindInboundDiscount = "desconto_entrada"
	insightConfidenceHigh      = "high"
	insightConfidenceMedium    = "medium"
	insightConfidenceLow       = "low"
	insightToolRebalance       = "rebalance_target"
	insightToolLoopIn          = "loop_in"
	insightToolEconOverride    = "econ_override"
	insightToolOpenChannel     = "open_channel"
	insightToolCloseChannel    = "close_channel"
	insightToolAutofeeReview   = "autofee_review"
	// The fee itself is edited on the channel card in Lightning Ops; the Fee
	// Center holds the AutoFee parameters around it.
	insightToolSetFee         = "set_fee"
	insightToolUseAsSource    = "use_as_source"
	insightToolNone           = "none"
	insightStatusOpen         = "open"
	insightStatusDismissed    = "dismissed"
	insightStatusDone         = "done"
	insightStatusExpired      = "expired"
	insightSellerMinSample    = 5
	insightSellerPercentile   = 0.8
	insightRefillIntentMinAge = 12 * time.Hour
	// Loop In quotes: keep a small on-chain buffer for the swap's own chain
	// fee, and never suggest a swap below the smallest useful lot.
	insightLoopInOnchainBufferSat int64 = 20_000
	insightLoopInMinSat           int64 = 100_000
	insightStuckJobsMin                 = 3
	insightAssistedShare                = 0.10
	insightPriceOverMult                = 1.3
	insightStopBuyingSellMax            = 0.20
	insightStopBuyingMinStock           = 15.0
	insightTurnoverPerWeek              = 2.0
	insightOpenMinScore                 = 60
	insightOpenMinRouteHits             = 3
	insightOpenMaxItems                 = 5
	insightPriceUnderGap                = 0.10
	insightEconOverrideRatio            = 0.9
	insightEconNearCapShare             = 0.9
	insightRequiredDays                 = 7
)

// insightScale holds the thresholds that cannot be the same number of sats
// on every node. They are derived from the node calibration that AutoFee
// and the Rebalance Center already share (classifyRebalanceNode), so a 20-
// channel node and a 150-channel node are judged on their own scale.
type insightScale struct {
	NodeClass           string  `json:"node_class"`
	LiquidityClass      string  `json:"liquidity_class"`
	IdleMinCapacitySat  int64   `json:"idle_min_capacity_sat"`
	IdleLocalPct        float64 `json:"idle_local_pct"`
	OpenMinVolumeSat    int64   `json:"open_min_volume_sat"`
	StopBuyingMinFeeSat int64   `json:"stop_buying_min_fee_sat"`
}

// insightReadiness says whether the node has enough history for the rules
// to mean anything. Every rule reads 7-day windows, so a node observed for
// less than a full week produces nothing instead of noise.
type insightReadiness struct {
	Ready        bool `json:"ready"`
	ObservedDays int  `json:"observed_days"`
	RequiredDays int  `json:"required_days"`
}

// insightNodeInfo is the calibration the payload shows the operator, so the
// page can say which scale the thresholds were read on.
type insightNodeInfo struct {
	insightReadiness
	insightScale
	ChannelCount     int     `json:"channel_count"`
	TotalCapacitySat int64   `json:"total_capacity_sat"`
	LocalRatio       float64 `json:"local_ratio"`
}

// insightCalibrate classifies the node from the ranking rows with the same
// function the Rebalance Center uses on the live channel list, then maps the
// class onto the sats thresholds. "large" keeps the values the rules were
// calibrated on; the other classes scale from there.
func insightCalibrate(ranking []ChannelRankingItem) (RebalanceNodeCalibration, insightScale) {
	channels := make([]lndclient.ChannelInfo, 0, len(ranking))
	for _, item := range ranking {
		channels = append(channels, lndclient.ChannelInfo{
			Active:           item.Active,
			CapacitySat:      item.CapacitySat,
			LocalBalanceSat:  item.LocalBalanceSat,
			RemoteBalanceSat: item.RemoteBalanceSat,
		})
	}
	calib := classifyRebalanceNode(channels)
	scale := insightScale{NodeClass: calib.NodeClass, LiquidityClass: calib.LiquidityClass}
	switch calib.NodeClass {
	case "xl":
		scale.IdleMinCapacitySat, scale.OpenMinVolumeSat, scale.StopBuyingMinFeeSat = 2_000_000, 2_000_000, 200
	case "large":
		scale.IdleMinCapacitySat, scale.OpenMinVolumeSat, scale.StopBuyingMinFeeSat = 1_000_000, 1_000_000, 100
	case "medium":
		scale.IdleMinCapacitySat, scale.OpenMinVolumeSat, scale.StopBuyingMinFeeSat = 500_000, 500_000, 60
	default: // small, unknown
		scale.IdleMinCapacitySat, scale.OpenMinVolumeSat, scale.StopBuyingMinFeeSat = 250_000, 250_000, 30
	}
	// A node made of small channels should still see its idle capital: never
	// ask for more than half the average channel, with a floor of 100k.
	if calib.AvgCapacitySat > 0 {
		if half := calib.AvgCapacitySat / 2; half < scale.IdleMinCapacitySat {
			scale.IdleMinCapacitySat = half
		}
	}
	if scale.IdleMinCapacitySat < 100_000 {
		scale.IdleMinCapacitySat = 100_000
	}
	// On a node that is mostly local, 60% local is the norm rather than a
	// signal; on a drained node even 50% local stands out.
	switch calib.LiquidityClass {
	case "full":
		scale.IdleLocalPct = 70
	case "drained":
		scale.IdleLocalPct = 50
	default:
		scale.IdleLocalPct = 60
	}
	return calib, scale
}

// insightObservedDays is the longest observation window the ranking has for
// any channel of this node, in whole days (capped by the 30-day window the
// ranking keeps). A fresh install starts at zero.
func insightObservedDays(ranking []ChannelRankingItem) int {
	maxSamples := 0
	for _, item := range ranking {
		if item.PeerSampleCount30d > maxSamples {
			maxSamples = item.PeerSampleCount30d
		}
	}
	return maxSamples / 24
}

func insightReadinessFor(ranking []ChannelRankingItem) insightReadiness {
	days := insightObservedDays(ranking)
	return insightReadiness{Ready: days >= insightRequiredDays, ObservedDays: days, RequiredDays: insightRequiredDays}
}

func insightNodeInfoFor(ranking []ChannelRankingItem) insightNodeInfo {
	calib, scale := insightCalibrate(ranking)
	return insightNodeInfo{
		insightReadiness: insightReadinessFor(ranking),
		insightScale:     scale,
		ChannelCount:     calib.ChannelCount,
		TotalCapacitySat: calib.TotalCapacitySat,
		LocalRatio:       math.Round(calib.LocalRatio*1000) / 1000,
	}
}

type InsightEvidence struct {
	Key     string  `json:"key"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit,omitempty"`
	Compare float64 `json:"compare,omitempty"`
	Extra   string  `json:"extra,omitempty"`
	Source  string  `json:"source"`
}

type InsightAction struct {
	Step   int               `json:"step"`
	Tool   string            `json:"tool"`
	Params map[string]string `json:"params,omitempty"`
}

type InsightGuard struct {
	Code   string `json:"code"`
	Passed bool   `json:"passed"`
	// Advisory guards inform a cost or a caveat; they never block the
	// recommendation, and the UI shows a failed one as a warning, not a stop.
	Advisory bool `json:"advisory,omitempty"`
}

type Insight struct {
	ID               string            `json:"id"`
	Kind             string            `json:"kind"`
	Rule             string            `json:"rule"`
	Priority         float64           `json:"priority"`
	Confidence       string            `json:"confidence"`
	ConfidenceReason map[string]any    `json:"confidence_reason,omitempty"`
	ChannelID        uint64            `json:"channel_id,omitempty"`
	ChannelPoint     string            `json:"channel_point,omitempty"`
	PeerAlias        string            `json:"peer_alias,omitempty"`
	PeerPubkey       string            `json:"peer_pubkey,omitempty"`
	ValueSatDay      int64             `json:"value_sat_day"`
	CostSat          int64             `json:"cost_sat"`
	Evidence         []InsightEvidence `json:"evidence"`
	Actions          []InsightAction   `json:"actions"`
	Guards           []InsightGuard    `json:"guards,omitempty"`
	InvalidateWhen   string            `json:"invalidate_when"`
	Status           string            `json:"status"`
	DismissReason    string            `json:"dismiss_reason,omitempty"`
	FirstSeenAt      time.Time         `json:"first_seen_at"`
	LastSeenAt       time.Time         `json:"last_seen_at"`
	ResolvedAt       *time.Time        `json:"resolved_at,omitempty"`
}

// insightsInput is everything the rule engine looks at. It is assembled by
// the server from in-process services so the engine stays pure and testable.
type insightsInput struct {
	Ranking         []ChannelRankingItem
	Rebalance       []RebalanceChannel
	Jobs24h         []RebalanceJob
	RefillIntents   map[uint64][]AutomationIntent
	OpenCandidates  []ChannelOpenCandidateItem
	Commitments     []MagmaChannelCommitment
	MagmaStateKnown bool
	LoopInstalled   bool
	LoopKnown       bool
	// Loop In sizing (R1): the rebalance lot cap and the confirmed on-chain
	// balance bound the amount the card quotes, so a quote never asks for
	// the whole deficit when neither the lot nor the wallet covers it.
	RebalanceMaxAmountSat int64
	OnchainSpendableSat   int64
	OnchainKnown          bool
	AutofeeProfile        autofeeProfile
	// AutofeeInboundPassive mirrors the Fee Center's "inbound passive" switch
	// (R8); AutofeeConfigKnown is false when the config could not be read.
	AutofeeInboundPassive bool
	AutofeeConfigKnown    bool
	// RuleWeights come from the measured outcomes (phase 2): a rule that keeps
	// missing drops in the order, one that keeps landing rises. Absent = 1.
	RuleWeights map[string]float64
	scale       insightScale // filled by buildInsights from the ranking
}

type insightChannel struct {
	rank    ChannelRankingItem
	reb     RebalanceChannel
	hasReb  bool
	intent  *AutomationIntent
	jobs    []RebalanceJob
	magma   *MagmaChannelCommitment
	parked  bool
	seller  bool
	private bool
}

type insightJobTally struct {
	total, failed, partial, succeeded, stuck int
}

func (t insightJobTally) allStuck() bool {
	return t.total >= insightStuckJobsMin && t.succeeded == 0 && t.stuck == t.total
}

func buildInsights(in insightsInput, now time.Time) []Insight {
	// A node observed for less than a week has no 7-day window to judge by:
	// stay silent rather than recommend on a partial picture.
	if !insightReadinessFor(in.Ranking).Ready {
		return nil
	}
	_, in.scale = insightCalibrate(in.Ranking)
	channels := assembleInsightChannels(in)
	if len(channels) == 0 && len(in.OpenCandidates) == 0 {
		return nil
	}
	sellerFloor := insightSellerThreshold(channels)
	nodeAvgDirect30d, nodeOutPpm30d := insightNodeAverages(channels)
	peers := map[string]bool{}
	for _, ch := range channels {
		if key := strings.ToLower(strings.TrimSpace(ch.rank.PeerPubkey)); key != "" {
			peers[key] = true
		}
	}

	out := make([]Insight, 0, 16)
	for i := range channels {
		ch := &channels[i]
		if ch.private || !ch.rank.Active {
			continue
		}
		ch.seller = sellerFloor > 0 && ch.rank.ForwardFee7dSat >= sellerFloor
		if ins, ok := insightRefillSeller(ch, in, now); ok {
			out = append(out, ins)
		} else if ins, ok := insightExpectedBlock(ch, now); ok {
			out = append(out, ins)
		}
		if ins, ok := insightMoreCapacity(ch, now); ok {
			out = append(out, ins)
		}
		if ins, ok := insightStopBuying(ch, in.scale, now); ok {
			out = append(out, ins)
		}
		if ins, ok := insightIdleCapital(ch, in, nodeAvgDirect30d, nodeOutPpm30d, now); ok {
			out = append(out, ins)
		}
		if ins, ok := insightPriceOff(ch, in.AutofeeProfile, now); ok {
			out = append(out, ins)
		}
		if ins, ok := insightInboundDiscount(ch, in, now); ok {
			out = append(out, ins)
		}
	}
	// The candidate list is long by design; the page shows only the few
	// peers that would move real volume.
	opens := make([]Insight, 0, len(in.OpenCandidates))
	for _, cand := range in.OpenCandidates {
		if ins, ok := insightOpenChannel(cand, peers, nodeOutPpm30d, in.scale, now); ok {
			opens = append(opens, ins)
		}
	}
	sort.SliceStable(opens, func(i, j int) bool { return opens[i].ValueSatDay > opens[j].ValueSatDay })
	if len(opens) > insightOpenMaxItems {
		opens = opens[:insightOpenMaxItems]
	}
	out = append(out, opens...)
	for i := range out {
		out[i].Priority = insightPriorityWeighted(out[i], in.RuleWeights)
		out[i].Status = insightStatusOpen
		out[i].FirstSeenAt = now
		out[i].LastSeenAt = now
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func assembleInsightChannels(in insightsInput) []insightChannel {
	rebByPoint := make(map[string]RebalanceChannel, len(in.Rebalance))
	for _, r := range in.Rebalance {
		rebByPoint[insightPointKey(r.ChannelPoint)] = r
	}
	jobsByID := map[uint64][]RebalanceJob{}
	for _, j := range in.Jobs24h {
		if j.TargetChannelID != 0 {
			jobsByID[j.TargetChannelID] = append(jobsByID[j.TargetChannelID], j)
		}
	}
	magmaByPoint := map[string]MagmaChannelCommitment{}
	for _, c := range in.Commitments {
		magmaByPoint[insightPointKey(c.ChannelPoint)] = c
	}
	out := make([]insightChannel, 0, len(in.Ranking))
	for _, item := range in.Ranking {
		ch := insightChannel{rank: item, private: item.Private, parked: isChannelAutomationParked(item.AutomationMode)}
		if reb, ok := rebByPoint[insightPointKey(item.ChannelPoint)]; ok {
			ch.reb = reb
			ch.hasReb = true
			ch.jobs = jobsByID[reb.ChannelID]
			ch.intent = selectRefillTargetIntent(in.RefillIntents[reb.ChannelID], 0)
			if isChannelAutomationParked(reb.AutomationMode) {
				ch.parked = true
			}
		}
		if c, ok := magmaByPoint[insightPointKey(item.ChannelPoint)]; ok {
			copyC := c
			ch.magma = &copyC
		}
		out = append(out, ch)
	}
	return out
}

func insightPointKey(point string) string {
	return strings.ToLower(strings.TrimSpace(point))
}

// insightSellerThreshold is the 80th percentile of 7d forward revenue among
// channels that sold anything. With fewer than five sellers the percentile is
// meaningless, so the floor falls back to the third-best seller.
func insightSellerThreshold(channels []insightChannel) int64 {
	fees := make([]int64, 0, len(channels))
	for _, ch := range channels {
		if ch.rank.ForwardFee7dSat > 0 && ch.rank.Active && !ch.private {
			fees = append(fees, ch.rank.ForwardFee7dSat)
		}
	}
	if len(fees) == 0 {
		return 0
	}
	sort.Slice(fees, func(i, j int) bool { return fees[i] < fees[j] })
	if len(fees) < insightSellerMinSample {
		idx := len(fees) - 3
		if idx < 0 {
			idx = 0
		}
		return fees[idx]
	}
	idx := int(math.Ceil(insightSellerPercentile*float64(len(fees)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(fees) {
		idx = len(fees) - 1
	}
	return fees[idx]
}

func insightNodeAverages(channels []insightChannel) (avgDirect30d float64, outPpm30d int64) {
	var fee, amt int64
	n := 0
	for _, ch := range channels {
		if !ch.rank.Active || ch.private {
			continue
		}
		fee += ch.rank.ForwardFee30dSat
		amt += ch.rank.ForwardAmt30dSat
		n++
	}
	if n > 0 {
		avgDirect30d = float64(fee) / float64(n)
	}
	if amt > 0 {
		outPpm30d = int64(math.Round(float64(fee) * 1e6 / float64(amt)))
	}
	return avgDirect30d, outPpm30d
}

func insightTallyJobs(jobs []RebalanceJob) insightJobTally {
	var t insightJobTally
	for _, j := range jobs {
		t.total++
		reason := strings.ToLower(j.Reason)
		switch j.Status {
		case "succeeded":
			t.succeeded++
		case "partial":
			t.partial++
		case "failed":
			t.failed++
		}
		if j.Status != "succeeded" && (strings.Contains(reason, "all sources failed") || strings.Contains(reason, "no route") || j.Status == "partial") {
			t.stuck++
		}
	}
	return t
}

func insightConfidenceFromSales(rank ChannelRankingItem) (string, map[string]any) {
	reason := map[string]any{"sales_7d": rank.ForwardOutCount7d, "observed_days": rank.PeerSampleCount30d / 24}
	switch {
	case rank.ForwardOutCount7d >= 7 && channelRankingHasFull7dObservation(rank.PeerSampleCount30d):
		return insightConfidenceHigh, reason
	case rank.ForwardOutCount7d >= 3:
		return insightConfidenceMedium, reason
	default:
		return insightConfidenceLow, reason
	}
}

func insightConfidenceWeight(level string) float64 {
	switch level {
	case insightConfidenceHigh:
		return 1
	case insightConfidenceMedium:
		return 0.6
	default:
		return 0.3
	}
}

func insightKindFloor(kind string) float64 {
	switch kind {
	case insightKindRefillSeller:
		return 500
	case insightKindStopBuying:
		return 400
	case insightKindPriceOff:
		return 300
	case insightKindIdleCapital:
		return 250
	case insightKindMoreCapacity:
		return 200
	case insightKindOpenChannel:
		return 150
	case insightKindInboundDiscount:
		return 250
	default:
		return 0
	}
}

func insightPriority(ins Insight) float64 {
	value := float64(ins.ValueSatDay)
	if value < 0 {
		value = 0
	}
	return math.Round(insightKindFloor(ins.Kind) + value*insightConfidenceWeight(ins.Confidence))
}

// insightPriorityWeighted applies the rule's measured weight on top of the
// designed priority. The kind floor is weighted too, so a rule that never
// lands can fall below a cheaper rule that does.
func insightPriorityWeighted(ins Insight, weights map[string]float64) float64 {
	base := insightPriority(ins)
	if w, ok := weights[ins.Rule]; ok && w > 0 {
		return math.Round(base * w)
	}
	return base
}

func insightID(rule, key string) string {
	return fmt.Sprintf("%s:%s", strings.ToLower(rule), strings.ToLower(strings.TrimSpace(key)))
}

func insightBase(rule, kind string, ch *insightChannel) Insight {
	ins := Insight{
		ID:           insightID(rule, ch.rank.ChannelPoint),
		Kind:         kind,
		Rule:         rule,
		ChannelPoint: ch.rank.ChannelPoint,
		PeerAlias:    ch.rank.PeerAlias,
		PeerPubkey:   ch.rank.PeerPubkey,
		Evidence:     []InsightEvidence{},
		Actions:      []InsightAction{},
	}
	if ch.rank.ChannelID > 0 {
		ins.ChannelID = uint64(ch.rank.ChannelID)
	}
	if ch.hasReb && ch.reb.ChannelID != 0 {
		ins.ChannelID = ch.reb.ChannelID
	}
	return ins
}

func insightCommonGuards(ch *insightChannel, in insightsInput) []InsightGuard {
	guards := []InsightGuard{{Code: "not_parked", Passed: !ch.parked}}
	if in.MagmaStateKnown {
		guards = append(guards, InsightGuard{Code: "magma_commitment", Passed: ch.magma == nil})
	}
	return guards
}

func insightGuardsPass(guards []InsightGuard) bool {
	for _, g := range guards {
		if !g.Passed && !g.Advisory {
			return false
		}
	}
	return true
}

// R1: a top seller sits below target, a refill intent has been open for a
// while and the rebalance keeps failing to find a route at the cap. The
// rebalance is not the tool for this one; suggest the ones that are.
func insightRefillSeller(ch *insightChannel, in insightsInput, now time.Time) (Insight, bool) {
	if !ch.seller || !ch.hasReb || ch.parked {
		return Insight{}, false
	}
	reb := ch.reb
	if reb.TargetAmountSat <= 0 || reb.LocalPct > reb.TargetOutboundPct {
		return Insight{}, false
	}
	intentAge := time.Duration(0)
	if ch.intent != nil && !ch.intent.FirstSeenAt.IsZero() {
		intentAge = now.Sub(ch.intent.FirstSeenAt)
	} else if ch.rank.DrainedSince != nil && !ch.rank.DrainedSince.IsZero() {
		intentAge = now.Sub(*ch.rank.DrainedSince)
	}
	if intentAge < insightRefillIntentMinAge {
		return Insight{}, false
	}
	tally := insightTallyJobs(ch.jobs)
	reasonStuck := false
	switch strings.TrimSpace(reb.AutopilotReason) {
	case "target_structural_cooldown", "route_dead_opportunity_below_floor", "low_success_opportunity_below_floor":
		reasonStuck = true
	}
	if !tally.allStuck() && !reasonStuck {
		return Insight{}, false
	}

	ins := insightBase("R1", insightKindRefillSeller, ch)
	ins.Confidence, ins.ConfidenceReason = insightConfidenceFromSales(ch.rank)
	sellPpm := int64(ch.rank.OutPpm7d)
	if reb.SalePricePpm > 0 {
		sellPpm = reb.SalePricePpm
	}
	refillPpm := reb.RebalanceCost7dPpm
	if refillPpm <= 0 {
		refillPpm = reb.FeeBudgetPpm
	}
	volumeDay := float64(ch.rank.ForwardAmt7dSat) / 7
	if refillPpm > 0 && sellPpm > refillPpm {
		ins.ValueSatDay = int64(math.Round(volumeDay * float64(sellPpm-refillPpm) / 1e6))
	}
	ins.Evidence = append(ins.Evidence,
		InsightEvidence{Key: "local_pct", Value: round1(reb.LocalPct), Unit: "pct", Compare: reb.TargetOutboundPct, Source: "rebalance"},
		InsightEvidence{Key: "sell_ppm_7d", Value: float64(sellPpm), Unit: "ppm", Source: "ranking"},
		InsightEvidence{Key: "rebal_cost_ppm_7d", Value: float64(reb.RebalanceCost7dPpm), Unit: "ppm", Compare: float64(reb.FeeBudgetPpm), Source: "rebalance"},
		InsightEvidence{Key: "fee_7d_sat", Value: float64(ch.rank.ForwardFee7dSat), Unit: "sat", Source: "ranking"},
		InsightEvidence{Key: "intent_age_h", Value: math.Floor(intentAge.Hours()), Unit: "h", Source: "interlock"},
	)
	if tally.total > 0 {
		ins.Evidence = append(ins.Evidence, InsightEvidence{Key: "jobs_24h", Value: float64(tally.total), Compare: float64(tally.failed + tally.partial), Extra: fmt.Sprintf("%d/%d", tally.failed, tally.partial), Source: "rebalance"})
	}
	if reb.AutopilotReason != "" {
		ins.Evidence = append(ins.Evidence, InsightEvidence{Key: "autopilot_reason", Extra: reb.AutopilotReason, Source: "rebalance"})
	}

	step := 1
	ins.Actions = append(ins.Actions, InsightAction{Step: step, Tool: insightToolRebalance, Params: map[string]string{"channel_point": reb.ChannelPoint}})
	step++
	// Only suggest a tighter econ ratio when the last paid price is already
	// near the cap and the sale still covers the AutoFee floor at the new cap.
	if reb.RebalanceCost7dPpm > 0 && reb.FeeBudgetPpm > 0 && float64(reb.RebalanceCost7dPpm) >= insightEconNearCapShare*float64(reb.FeeBudgetPpm) && reb.EffectiveEconRatio < insightEconOverrideRatio {
		newCap := int64(math.Floor(float64(sellPpm) * insightEconOverrideRatio))
		floorAtCap := rebalSeedFloorFromCost(int(newCap), ch.rank.ClassLabel, in.AutofeeProfile, false)
		if sellPpm >= int64(floorAtCap) && newCap > reb.FeeBudgetPpm {
			margin := 0.0
			if newCap > 0 {
				margin = round1(float64(sellPpm-newCap) * 100 / float64(newCap))
			}
			ins.Actions = append(ins.Actions, InsightAction{Step: step, Tool: insightToolEconOverride, Params: map[string]string{
				"channel_point": reb.ChannelPoint,
				"ratio":         fmt.Sprintf("%.2f", insightEconOverrideRatio),
				"cap_ppm":       fmt.Sprintf("%d", newCap),
				"margin_pct":    fmt.Sprintf("%.1f", margin),
			}})
			step++
			ins.Guards = append(ins.Guards, InsightGuard{Code: "sell_covers_floor", Passed: true})
		}
	}
	loopAmount, loopLimit := insightLoopInAmount(reb.TargetAmountSat, in.RebalanceMaxAmountSat, in.OnchainSpendableSat, in.OnchainKnown)
	loop := InsightAction{Step: step, Tool: insightToolLoopIn, Params: map[string]string{
		"last_hop_pubkey": reb.RemotePubkey,
		"amount_sat":      fmt.Sprintf("%d", loopAmount),
		"amount_limit":    loopLimit,
		// The same ceiling the rebalance pays: a quote above it does not pay.
		"cap_ppm": fmt.Sprintf("%d", reb.FeeBudgetPpm),
	}}
	if in.OnchainKnown {
		loop.Params["onchain_sat"] = fmt.Sprintf("%d", in.OnchainSpendableSat)
	}
	ins.Actions = append(ins.Actions, loop)
	if in.LoopKnown {
		ins.Guards = append(ins.Guards, InsightGuard{Code: "loop_installed", Passed: in.LoopInstalled})
	}
	if in.OnchainKnown {
		ins.Guards = append(ins.Guards, InsightGuard{Code: "onchain_funds", Passed: loopLimit != insightLoopLimitOnchainShort, Advisory: true})
	}
	ins.Guards = append(ins.Guards, insightCommonGuards(ch, in)...)
	ins.InvalidateWhen = "local_at_target_24h"
	return ins, true
}

const (
	insightLoopLimitTarget       = "target"
	insightLoopLimitRebalanceLot = "rebalance_lot"
	insightLoopLimitOnchain      = "onchain"
	insightLoopLimitOnchainShort = "onchain_short"
)

// insightLoopInAmount sizes the Loop In the card quotes: the deficit to the
// target, bounded by the rebalance lot cap and by what the wallet can spend.
// It returns the amount and which bound won, so the card can say so. When the
// wallet cannot even cover the minimum lot, the amount stays at that minimum
// and the limit says the wallet is short; the guard reports it.
func insightLoopInAmount(deficitSat, maxLotSat, onchainSat int64, onchainKnown bool) (int64, string) {
	amount, limit := deficitSat, insightLoopLimitTarget
	if maxLotSat > 0 && maxLotSat < amount {
		amount, limit = maxLotSat, insightLoopLimitRebalanceLot
	}
	if onchainKnown {
		available := onchainSat - insightLoopInOnchainBufferSat
		if available < insightLoopInMinSat {
			return minInt64(amount, insightLoopInMinSat), insightLoopLimitOnchainShort
		}
		if available < amount {
			amount, limit = available, insightLoopLimitOnchain
		}
	}
	if amount < insightLoopInMinSat {
		amount = minInt64(deficitSat, insightLoopInMinSat)
	}
	return amount, limit
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// R7: a good seller is below target but a known rebalance guard is holding
// it on purpose. Nothing to do; tell the operator why the system waits.
func insightExpectedBlock(ch *insightChannel, now time.Time) (Insight, bool) {
	if !ch.seller || !ch.hasReb || ch.parked {
		return Insight{}, false
	}
	reb := ch.reb
	if reb.TargetAmountSat <= 0 || reb.LocalPct > reb.TargetOutboundPct {
		return Insight{}, false
	}
	switch strings.TrimSpace(reb.AutopilotReason) {
	case "paid_stock_gate", "cost_gate", "paid_liquidity_unsold_cooldown", "target_cooldown", "autofee_settling_target", "roi_guardrail", "budget_efficiency_below_floor", "no_sale_30d":
	default:
		return Insight{}, false
	}
	ins := insightBase("R7", insightKindExpectedBlock, ch)
	ins.Confidence, ins.ConfidenceReason = insightConfidenceFromSales(ch.rank)
	ins.Evidence = append(ins.Evidence,
		InsightEvidence{Key: "local_pct", Value: round1(reb.LocalPct), Unit: "pct", Compare: reb.TargetOutboundPct, Source: "rebalance"},
		InsightEvidence{Key: "autopilot_reason", Extra: reb.AutopilotReason, Source: "rebalance"},
		InsightEvidence{Key: "fee_7d_sat", Value: float64(ch.rank.ForwardFee7dSat), Unit: "sat", Source: "ranking"},
	)
	ins.Actions = append(ins.Actions, InsightAction{Step: 1, Tool: insightToolNone, Params: map[string]string{"channel_point": reb.ChannelPoint, "reason": reb.AutopilotReason}})
	ins.InvalidateWhen = "guard_released"
	return ins, true
}

// R2: the channel turns its whole capacity over more than twice a week and
// still runs dry. More capacity with the same peer is the structural answer;
// the card warns that a parallel channel leaves the fast path.
func insightMoreCapacity(ch *insightChannel, now time.Time) (Insight, bool) {
	if !ch.seller || ch.parked || ch.rank.CapacitySat <= 0 {
		return Insight{}, false
	}
	turnover := float64(ch.rank.ForwardAmt7dSat) / float64(ch.rank.CapacitySat)
	if turnover < insightTurnoverPerWeek || ch.rank.LocalBalancePct >= 20 {
		return Insight{}, false
	}
	ins := insightBase("R2", insightKindMoreCapacity, ch)
	ins.Confidence, ins.ConfidenceReason = insightConfidenceFromSales(ch.rank)
	if ins.Confidence == insightConfidenceHigh {
		ins.Confidence = insightConfidenceMedium
	}
	ins.ValueSatDay = int64(math.Round(float64(ch.rank.ForwardFee7dSat) / 7 * 0.5))
	ins.Evidence = append(ins.Evidence,
		InsightEvidence{Key: "turnover_7d", Value: round1(turnover), Unit: "x", Compare: insightTurnoverPerWeek, Source: "ranking"},
		InsightEvidence{Key: "local_pct", Value: round1(ch.rank.LocalBalancePct), Unit: "pct", Compare: 20, Source: "ranking"},
		InsightEvidence{Key: "fee_7d_sat", Value: float64(ch.rank.ForwardFee7dSat), Unit: "sat", Source: "ranking"},
		InsightEvidence{Key: "capacity_sat", Value: float64(ch.rank.CapacitySat), Unit: "sat", Source: "ranking"},
	)
	if ch.hasReb && ch.reb.DrainRateSatPerHour > 0 {
		hours := float64(ch.reb.LocalBalanceSat) / float64(ch.reb.DrainRateSatPerHour)
		ins.Evidence = append(ins.Evidence, InsightEvidence{Key: "hours_to_drain", Value: round1(hours), Unit: "h", Source: "rebalance"})
	}
	ins.Actions = append(ins.Actions,
		InsightAction{Step: 1, Tool: insightToolOpenChannel, Params: map[string]string{"peer_pubkey": ch.rank.PeerPubkey, "channel_point": ch.rank.ChannelPoint}},
	)
	ins.Guards = append(ins.Guards, InsightGuard{Code: "parallel_channel_leaves_fast_path", Passed: false, Advisory: true})
	ins.InvalidateWhen = "turnover_below_2x"
	return ins, true
}

// R3: the rebalance keeps paying for stock that does not sell. Stop buying
// for this target until the lot moves.
func insightStopBuying(ch *insightChannel, scale insightScale, now time.Time) (Insight, bool) {
	if ch.parked || ch.rank.RebalFee7dSat < scale.StopBuyingMinFeeSat || ch.rank.RebalAmt7dSat <= 0 {
		return Insight{}, false
	}
	if ch.rank.LocalBalancePct < insightStopBuyingMinStock {
		return Insight{}, false
	}
	soldShare := 0.0
	if ch.rank.RebalAmt7dSat > 0 {
		soldShare = float64(ch.rank.ForwardAmt7dSat) / float64(ch.rank.RebalAmt7dSat)
	}
	if soldShare >= insightStopBuyingSellMax {
		return Insight{}, false
	}
	if ch.rank.ForwardFee7dSat >= ch.rank.RebalFee7dSat {
		return Insight{}, false
	}
	if ch.hasReb && !ch.reb.AutoEnabled && !ch.reb.ManualRestartEnabled && !ch.reb.GuaranteedRebalanceEnabled {
		return Insight{}, false
	}
	ins := insightBase("R3", insightKindStopBuying, ch)
	ins.Confidence = insightConfidenceHigh
	if !channelRankingHasFull7dObservation(ch.rank.PeerSampleCount30d) {
		ins.Confidence = insightConfidenceMedium
	}
	ins.ConfidenceReason = map[string]any{"observed_days": ch.rank.PeerSampleCount30d / 24}
	ins.ValueSatDay = int64(math.Round(float64(ch.rank.RebalFee7dSat-ch.rank.ForwardFee7dSat) / 7))
	ins.Evidence = append(ins.Evidence,
		InsightEvidence{Key: "local_pct", Value: round1(ch.rank.LocalBalancePct), Unit: "pct", Source: "ranking"},
		InsightEvidence{Key: "lot_sold_pct", Value: round1(soldShare * 100), Unit: "pct", Compare: insightStopBuyingSellMax * 100, Source: "ranking"},
		InsightEvidence{Key: "rebal_fee_7d_sat", Value: float64(ch.rank.RebalFee7dSat), Unit: "sat", Compare: float64(scale.StopBuyingMinFeeSat), Source: "ranking"},
		InsightEvidence{Key: "fee_7d_sat", Value: float64(ch.rank.ForwardFee7dSat), Unit: "sat", Source: "ranking"},
	)
	ins.Actions = append(ins.Actions,
		InsightAction{Step: 1, Tool: insightToolRebalance, Params: map[string]string{"channel_point": ch.rank.ChannelPoint, "suggest": "disable_target"}},
		InsightAction{Step: 2, Tool: insightToolAutofeeReview, Params: map[string]string{"channel_point": ch.rank.ChannelPoint}},
	)
	ins.InvalidateWhen = "lot_sold_50pct"
	return ins, true
}

// R4: most of the capital sits on our side and nothing sold in 30 days,
// not even assisted. Candidate to close or to use as a source.
func insightIdleCapital(ch *insightChannel, in insightsInput, nodeAvgDirect30d float64, nodeOutPpm30d int64, now time.Time) (Insight, bool) {
	rank := ch.rank
	if ch.parked || rank.CapacitySat < in.scale.IdleMinCapacitySat || rank.LocalBalancePct < in.scale.IdleLocalPct {
		return Insight{}, false
	}
	if rank.ForwardFee30dSat > 0 {
		return Insight{}, false
	}
	if nodeAvgDirect30d > 0 && float64(rank.AssistedForwardFee30dSat) >= insightAssistedShare*nodeAvgDirect30d {
		return Insight{}, false
	}
	if !channelRankingHasFull7dObservation(rank.PeerSampleCount30d) {
		return Insight{}, false
	}
	ins := insightBase("R4", insightKindIdleCapital, ch)
	ins.Confidence = insightConfidenceMedium
	if channelRankingHasFull30dObservation(rank.PeerSampleCount30d) {
		ins.Confidence = insightConfidenceHigh
	}
	ins.ConfidenceReason = map[string]any{"observed_days": rank.PeerSampleCount30d / 24}
	if nodeOutPpm30d > 0 {
		// What the same sats earn on average elsewhere in this node, per day.
		ins.ValueSatDay = int64(math.Round(float64(rank.LocalBalanceSat) * float64(nodeOutPpm30d) / 1e6 / 30))
	}
	ins.Evidence = append(ins.Evidence,
		InsightEvidence{Key: "local_sat", Value: float64(rank.LocalBalanceSat), Unit: "sat", Source: "ranking"},
		InsightEvidence{Key: "local_pct", Value: round1(rank.LocalBalancePct), Unit: "pct", Compare: in.scale.IdleLocalPct, Source: "ranking"},
		InsightEvidence{Key: "fee_30d_sat", Value: 0, Unit: "sat", Source: "ranking"},
		InsightEvidence{Key: "assisted_fee_30d_sat", Value: float64(rank.AssistedForwardFee30dSat), Unit: "sat", Compare: math.Round(insightAssistedShare * nodeAvgDirect30d), Source: "ranking"},
		InsightEvidence{Key: "rebal_fee_30d_sat", Value: float64(rank.RebalFee30dSat), Unit: "sat", Source: "ranking"},
	)
	ins.Actions = append(ins.Actions,
		InsightAction{Step: 1, Tool: insightToolUseAsSource, Params: map[string]string{"channel_point": rank.ChannelPoint}},
		InsightAction{Step: 2, Tool: insightToolCloseChannel, Params: map[string]string{"channel_point": rank.ChannelPoint}},
	)
	ins.Guards = append(ins.Guards, insightCommonGuards(ch, in)...)
	if ch.hasReb {
		// Who pays the cooperative close. Peer-funded passes (closing costs us
		// nothing); funded by us is a cost warning, not a blocker.
		ins.Guards = append(ins.Guards, InsightGuard{Code: "peer_funded_close_free", Passed: !ch.reb.Initiator, Advisory: true})
	}
	ins.InvalidateWhen = "sales_30d_positive"
	return ins, true
}

// R5: a peer the node keeps routing through, with a strong open-candidate
// score and no channel yet.
func insightOpenChannel(cand ChannelOpenCandidateItem, peers map[string]bool, nodeOutPpm30d int64, scale insightScale, now time.Time) (Insight, bool) {
	key := strings.ToLower(strings.TrimSpace(cand.PeerPubkey))
	if key == "" || peers[key] {
		return Insight{}, false
	}
	if cand.Score < insightOpenMinScore || cand.RouteHitCount30d < insightOpenMinRouteHits || cand.RouteVolumeSat30d < scale.OpenMinVolumeSat {
		return Insight{}, false
	}
	ins := Insight{
		ID:         insightID("R5", cand.PeerPubkey),
		Kind:       insightKindOpenChannel,
		Rule:       "R5",
		PeerAlias:  cand.PeerAlias,
		PeerPubkey: cand.PeerPubkey,
		Evidence:   []InsightEvidence{},
		Actions:    []InsightAction{},
	}
	switch {
	case cand.Confidence >= 70 && cand.RouteHitCount30d >= 10:
		ins.Confidence = insightConfidenceHigh
	case cand.Confidence >= 40:
		ins.Confidence = insightConfidenceMedium
	default:
		ins.Confidence = insightConfidenceLow
	}
	ins.ConfidenceReason = map[string]any{"route_hits_30d": cand.RouteHitCount30d, "candidate_confidence": cand.Confidence}
	if nodeOutPpm30d > 0 {
		ins.ValueSatDay = int64(math.Round(float64(cand.RouteVolumeSat30d) / 30 * float64(nodeOutPpm30d) / 1e6))
	}
	ins.Evidence = append(ins.Evidence,
		InsightEvidence{Key: "route_hits_30d", Value: float64(cand.RouteHitCount30d), Compare: insightOpenMinRouteHits, Source: "open_candidates"},
		InsightEvidence{Key: "route_volume_30d_sat", Value: float64(cand.RouteVolumeSat30d), Unit: "sat", Compare: float64(scale.OpenMinVolumeSat), Source: "open_candidates"},
		InsightEvidence{Key: "candidate_score", Value: float64(cand.Score), Compare: insightOpenMinScore, Source: "open_candidates"},
		InsightEvidence{Key: "route_cost_ppm_30d", Value: float64(cand.RouteCostPpm30d), Unit: "ppm", Source: "open_candidates"},
	)
	ins.Actions = append(ins.Actions,
		InsightAction{Step: 1, Tool: insightToolOpenChannel, Params: map[string]string{"peer_pubkey": cand.PeerPubkey}},
	)
	ins.InvalidateWhen = "channel_opened_or_candidate_gone"
	return ins, true
}

// R6: the fee sits far above the price the channel actually sells at while
// it still holds stock, or below the floor that the rebalance cost implies.
// The over-price case only applies to a fee the operator fixed by hand:
// AutoFee already caps its own channels by the real price, and second-
// guessing it here would be noise. The under-floor case needs a gap that
// matters, not a few ppm of rounding.
func insightPriceOff(ch *insightChannel, profile autofeeProfile, now time.Time) (Insight, bool) {
	if !ch.hasReb || ch.parked {
		return Insight{}, false
	}
	reb := ch.reb
	fee := reb.OutgoingFeePpm
	if fee <= 0 {
		return Insight{}, false
	}
	fixedFee := ch.rank.FixedFeePPM != nil || reb.FixedFeePPM != nil
	over := fixedFee && reb.SalePricePpm > 0 && float64(fee) >= insightPriceOverMult*float64(reb.SalePricePpm) && reb.LocalPct >= 20
	floor := 0
	if reb.RebalanceCost7dPpm > 0 {
		floor = rebalSeedFloorFromCost(int(reb.RebalanceCost7dPpm), ch.rank.ClassLabel, profile, false)
	}
	under := floor > 0 && reb.RebalanceAmount7dSat > 0 && float64(int64(floor)-fee) >= insightPriceUnderGap*float64(floor)
	if !over && !under {
		return Insight{}, false
	}
	ins := insightBase("R6", insightKindPriceOff, ch)
	ins.Confidence, ins.ConfidenceReason = insightConfidenceFromSales(ch.rank)
	ins.Evidence = append(ins.Evidence,
		InsightEvidence{Key: "fee_ppm", Value: float64(fee), Unit: "ppm", Source: "rebalance"},
	)
	direction := "over"
	if over {
		ins.Evidence = append(ins.Evidence,
			InsightEvidence{Key: "sell_ppm_7d", Value: float64(reb.SalePricePpm), Unit: "ppm", Compare: math.Round(insightPriceOverMult * float64(reb.SalePricePpm)), Extra: reb.SalePriceSource, Source: "rebalance"},
			InsightEvidence{Key: "local_pct", Value: round1(reb.LocalPct), Unit: "pct", Source: "rebalance"},
		)
	} else {
		direction = "under"
		ins.Evidence = append(ins.Evidence,
			InsightEvidence{Key: "floor_ppm", Value: float64(floor), Unit: "ppm", Source: "autofee"},
			InsightEvidence{Key: "rebal_cost_ppm_7d", Value: float64(reb.RebalanceCost7dPpm), Unit: "ppm", Source: "rebalance"},
		)
		ins.ValueSatDay = int64(math.Round(float64(int64(floor)-fee) * float64(reb.RebalanceAmount7dSat) / 7 / 1e6))
	}
	ins.Actions = append(ins.Actions,
		InsightAction{Step: 1, Tool: insightToolSetFee, Params: map[string]string{"channel_point": reb.ChannelPoint, "direction": direction}},
		InsightAction{Step: 2, Tool: insightToolAutofeeReview, Params: map[string]string{"channel_point": reb.ChannelPoint, "direction": direction}},
	)
	ins.InvalidateWhen = "fee_within_band"
	return ins, true
}

// R8: a drained top seller that the rebalance cannot refill may refill for
// free through an inbound fee discount: peers routing INTO us through that
// channel pay less, so flow arrives on its own (coinos went 1%->47% in a day
// that way). AutoFee already sets that discount for a "sink" with margin when
// inbound passive is on, so the rule only speaks when nothing will: the
// switch is off, or the channel's fee is pinned by hand (AutoFee not managing
// it). Thresholds mirror the engine's: sink, local at or under the reach-out
// ratio, at least 5 sales in 7 days, no discount in place yet.
func insightInboundDiscount(ch *insightChannel, in insightsInput, now time.Time) (Insight, bool) {
	if !ch.hasReb || ch.parked || !ch.seller || !in.AutofeeConfigKnown {
		return Insight{}, false
	}
	reb := ch.reb
	if !strings.EqualFold(strings.TrimSpace(ch.rank.ClassLabel), "sink") || reb.OutgoingFeePpm <= 0 || reb.InboundFeePpm < 0 {
		return Insight{}, false
	}
	reachOut := in.AutofeeProfile.InboundDiscountReachOutRatio
	if reachOut <= 0 {
		reachOut = 0.10
	}
	if reb.LocalPct > reachOut*100 || ch.rank.ForwardOutCount7d < 5 {
		return Insight{}, false
	}
	fixedFee := ch.rank.FixedFeePPM != nil || reb.FixedFeePPM != nil
	if in.AutofeeInboundPassive && !fixedFee {
		// AutoFee owns the discount here; a missing one is its decision.
		return Insight{}, false
	}
	sellPpm := int64(ch.rank.OutPpm7d)
	if reb.SalePricePpm > 0 {
		sellPpm = reb.SalePricePpm
	}
	if sellPpm <= 0 {
		return Insight{}, false
	}
	ins := insightBase("R8", insightKindInboundDiscount, ch)
	ins.Confidence, ins.ConfidenceReason = insightConfidenceFromSales(ch.rank)
	ins.Guards = insightCommonGuards(ch, in)
	if !insightGuardsPass(ins.Guards) {
		return Insight{}, false
	}
	// What the channel earns per day when it has stock is what a free refill
	// keeps alive; the rebalance it replaces is not spent at all.
	ins.ValueSatDay = ch.rank.ForwardFee7dSat / 7
	ins.Evidence = append(ins.Evidence,
		InsightEvidence{Key: "local_pct", Value: round1(reb.LocalPct), Unit: "pct", Compare: math.Round(reachOut * 100), Source: "rebalance"},
		InsightEvidence{Key: "sell_ppm_7d", Value: float64(sellPpm), Unit: "ppm", Extra: reb.SalePriceSource, Source: "rebalance"},
		InsightEvidence{Key: "fee_ppm", Value: float64(reb.OutgoingFeePpm), Unit: "ppm", Source: "rebalance"},
		InsightEvidence{Key: "inbound_fee_ppm", Value: float64(reb.InboundFeePpm), Unit: "ppm", Source: "rebalance"},
	)
	anchor := int(reb.RebalanceCost7dPpm)
	if anchor <= 0 {
		anchor = int(reb.FeeBudgetPpm)
	}
	if anchor > 0 {
		spread := in.AutofeeProfile.InboundDiscountMinRetainedSpreadFrac
		if budget := computeInboundDiscountBudget(anchor, int(reb.OutgoingFeePpm), defaultInboundDiscountMaxRatio, spread); budget > 0 {
			ins.Evidence = append(ins.Evidence, InsightEvidence{Key: "inbound_discount_suggested_ppm", Value: float64(budget), Unit: "ppm", Extra: "rebal_cost_anchor", Source: "autofee"})
		}
	}
	reason := "inbound_passive_off"
	if in.AutofeeInboundPassive {
		reason = "fixed_fee"
	}
	if fixedFee {
		ins.Actions = append(ins.Actions,
			InsightAction{Step: 1, Tool: insightToolSetFee, Params: map[string]string{"channel_point": reb.ChannelPoint, "direction": "inbound_discount", "reason": reason}},
		)
	} else {
		ins.Actions = append(ins.Actions,
			InsightAction{Step: 1, Tool: insightToolAutofeeReview, Params: map[string]string{"channel_point": reb.ChannelPoint, "direction": "inbound_passive", "reason": reason}},
		)
	}
	ins.InvalidateWhen = "inbound_discount_set_or_refilled"
	return ins, true
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
