package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

// Phase 2 of Insights: every recommendation the operator acted on (done) or
// rejected (dismissed, except "later") gets a before/after measurement seven
// days later. The "before" is captured at decision time from the notification
// ledger (forwards and settled rebalances, kept for a year), so no daily
// snapshot is needed; the "after" is the same window starting at the decision.
// Nothing here changes a rule: the scorecard only reorders them.
const (
	insightOutcomeWindow        = 7 * 24 * time.Hour
	insightOutcomeStatusPending = "pending"
	insightOutcomeStatusDone    = "measured"
	insightOutcomeStatusVoid    = "cancelled"
	insightOutcomeDecisionDone  = "done"
	insightOutcomeDecisionSkip  = "dismissed"

	// Verdicts for a followed recommendation.
	insightVerdictHit     = "hit"     // realized at least half of the predicted value
	insightVerdictNeutral = "neutral" // something, but less than half
	insightVerdictMiss    = "miss"    // nothing, or worse than before
	// Verdicts for a dismissed one: did the channel improve without the action?
	insightVerdictConfirmed = "confirmed" // it did not: the recommendation stood
	insightVerdictUnneeded  = "unneeded"  // it did: the action was not needed

	insightOutcomeHitShare      = 0.5
	insightOutcomeStopBuyShare  = 0.5
	insightScorecardMinMeasured = 5
	insightRuleWeightMin        = 0.5
	insightRuleWeightMax        = 1.5
	insightOutcomesRecentLimit  = 60
	insightOutcomeLoopInterval  = time.Hour
	insightOutcomeLoopFirstWait = 90 * time.Second
)

// insightWindowMetrics is what the ledger says about one channel (or one
// peer) over a window: sats earned selling, sats earned as the incoming hop,
// volume sold and what the autopilot paid to refill it.
type insightWindowMetrics struct {
	DirectFeeSat   int64 `json:"direct_fee_sat"`
	AssistedFeeSat int64 `json:"assisted_fee_sat"`
	OutAmountSat   int64 `json:"out_amount_sat"`
	RebalCostSat   int64 `json:"rebal_cost_sat"`
	RebalAmountSat int64 `json:"rebal_amount_sat"`
}

func (m insightWindowMetrics) netSat() int64 {
	return m.DirectFeeSat + m.AssistedFeeSat - m.RebalCostSat
}

type InsightOutcome struct {
	ID               int64                 `json:"id"`
	InsightID        string                `json:"insight_id"`
	Rule             string                `json:"rule"`
	Kind             string                `json:"kind"`
	ChannelID        uint64                `json:"channel_id,omitempty"`
	ChannelPoint     string                `json:"channel_point,omitempty"`
	PeerAlias        string                `json:"peer_alias,omitempty"`
	PeerPubkey       string                `json:"peer_pubkey,omitempty"`
	Decision         string                `json:"decision"`
	DecisionReason   string                `json:"decision_reason,omitempty"`
	DecidedAt        time.Time             `json:"decided_at"`
	MeasureAt        time.Time             `json:"measure_at"`
	WindowDays       int                   `json:"window_days"`
	PredictedSatDay  int64                 `json:"predicted_sat_day"`
	ActionCostSat    int64                 `json:"action_cost_sat"`
	Before           insightWindowMetrics  `json:"before"`
	After            *insightWindowMetrics `json:"after,omitempty"`
	RealizedSatDay   *int64                `json:"realized_sat_day,omitempty"`
	ErrorPct         *float64              `json:"error_pct,omitempty"`
	Verdict          string                `json:"verdict,omitempty"`
	Status           string                `json:"status"`
	MeasuredAt       *time.Time            `json:"measured_at,omitempty"`
	MeasurementError string                `json:"measurement_error,omitempty"`
}

type InsightRuleScore struct {
	Rule          string  `json:"rule"`
	Pending       int     `json:"pending"`
	Measured      int     `json:"measured"`
	Followed      int     `json:"followed"`
	Hits          int     `json:"hits"`
	Neutral       int     `json:"neutral"`
	Misses        int     `json:"misses"`
	Dismissed     int     `json:"dismissed"`
	Confirmed     int     `json:"confirmed"`
	Unneeded      int     `json:"unneeded"`
	Accuracy      float64 `json:"accuracy"`       // (hits + confirmed) / measured
	MeanErrorPct  float64 `json:"mean_error_pct"` // followed only
	Weight        float64 `json:"weight"`
	WeightApplied bool    `json:"weight_applied"`
}

type InsightScorecard struct {
	Rules    []InsightRuleScore `json:"rules"`
	Measured int                `json:"measured"`
	Pending  int                `json:"pending"`
	Hits     int                `json:"hits"`
	Followed int                `json:"followed"`
	Recent   []InsightOutcome   `json:"recent"`
}

func (s *InsightsService) ensureOutcomeSchema(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
create table if not exists insights_outcomes (
    id bigserial primary key,
    insight_id text not null,
    rule text not null,
    kind text not null,
    channel_id bigint,
    channel_point text,
    peer_alias text,
    peer_pubkey text,
    decision text not null,
    decision_reason text,
    decided_at timestamptz not null,
    measure_at timestamptz not null,
    window_days integer not null default 7,
    predicted_sat_day bigint not null default 0,
    action_cost_sat bigint not null default 0,
    before_metrics jsonb not null,
    after_metrics jsonb,
    realized_sat_day bigint,
    error_pct double precision,
    verdict text,
    status text not null default 'pending',
    measured_at timestamptz,
    measurement_error text,
    snapshot jsonb
);
create index if not exists insights_outcomes_status_idx on insights_outcomes (status, measure_at);
create index if not exists insights_outcomes_insight_idx on insights_outcomes (insight_id, decided_at desc);
`)
	return err
}

// measureInsightWindow reads the ledger for one channel. With no channel id
// (open-channel recommendations) it falls back to the peer: every forward that
// left through, or arrived from, that node.
func (s *InsightsService) measureInsightWindow(ctx context.Context, channelID uint64, peerPubkey string, from, to time.Time) (insightWindowMetrics, error) {
	var m insightWindowMetrics
	if s == nil || s.db == nil {
		return m, errors.New("insights database unavailable")
	}
	const feeExpr = `case when fee_msat>0 then fee_msat when fee_sat>0 then fee_sat*1000
 when amount_in_msat>0 and amount_out_msat>0 and amount_in_msat>amount_out_msat then amount_in_msat-amount_out_msat else 0 end`
	const amtExpr = `case when amount_out_msat>0 then amount_out_msat else amount_sat*1000 end`
	// sum(bigint) is numeric in Postgres; cast back so pgx scans into int64.
	if channelID > 0 {
		id := int64(channelID)
		err := s.db.QueryRow(ctx, `select
 (coalesce(sum(`+feeExpr+`) filter (where type='forward' and coalesce(chan_id_out,channel_id)=$1),0)/1000)::bigint,
 (coalesce(sum(`+feeExpr+`) filter (where type='forward' and chan_id_in=$1),0)/1000)::bigint,
 (coalesce(sum(`+amtExpr+`) filter (where type='forward' and coalesce(chan_id_out,channel_id)=$1),0)/1000)::bigint,
 (coalesce(sum(`+feeExpr+`) filter (where type='rebalance' and status in ('SETTLED','SUCCEEDED') and coalesce(rebal_target_chan_id,channel_id)=$1),0)/1000)::bigint,
 coalesce(sum(amount_sat) filter (where type='rebalance' and status in ('SETTLED','SUCCEEDED') and coalesce(rebal_target_chan_id,channel_id)=$1),0)::bigint
 from notifications where occurred_at >= $2 and occurred_at < $3
 and ((type='forward' and (chan_id_out=$1 or (chan_id_out is null and channel_id=$1) or chan_id_in=$1))
 or (type='rebalance' and (rebal_target_chan_id=$1 or (rebal_target_chan_id is null and channel_id=$1))))`,
			id, from, to).Scan(&m.DirectFeeSat, &m.AssistedFeeSat, &m.OutAmountSat, &m.RebalCostSat, &m.RebalAmountSat)
		return m, err
	}
	peer := strings.ToLower(strings.TrimSpace(peerPubkey))
	if peer == "" {
		return m, errors.New("no channel or peer to measure")
	}
	err := s.db.QueryRow(ctx, `select
 (coalesce(sum(`+feeExpr+`) filter (where lower(peer_pubkey_out)=$1),0)/1000)::bigint,
 (coalesce(sum(`+feeExpr+`) filter (where lower(peer_pubkey_in)=$1),0)/1000)::bigint,
 (coalesce(sum(`+amtExpr+`) filter (where lower(peer_pubkey_out)=$1),0)/1000)::bigint
 from notifications where type='forward' and occurred_at >= $2 and occurred_at < $3
 and (lower(peer_pubkey_out)=$1 or lower(peer_pubkey_in)=$1)`,
		peer, from, to).Scan(&m.DirectFeeSat, &m.AssistedFeeSat, &m.OutAmountSat)
	return m, err
}

// EnqueueOutcome opens the measurement of a decision: captures the "before"
// window now and schedules the "after" for a week later. A pending outcome
// for the same recommendation is replaced so a reopen+redo does not double
// count. Informational recommendations are not measured.
func (s *InsightsService) EnqueueOutcome(ctx context.Context, id, decision, reason string, now time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("insights database unavailable")
	}
	var raw []byte
	if err := s.db.QueryRow(ctx, `select snapshot from insights_recommendations where id = $1`, id).Scan(&raw); err != nil {
		return err
	}
	var item Insight
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
	}
	if item.Kind == insightKindExpectedBlock || item.Rule == "" {
		return nil
	}
	before, err := s.measureInsightWindow(ctx, item.ChannelID, item.PeerPubkey, now.Add(-insightOutcomeWindow), now)
	if err != nil {
		return err
	}
	beforeJSON, _ := json.Marshal(before)
	if _, err := s.db.Exec(ctx, `update insights_outcomes set status = $2 where insight_id = $1 and status = $3`, id, insightOutcomeStatusVoid, insightOutcomeStatusPending); err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
insert into insights_outcomes (insight_id, rule, kind, channel_id, channel_point, peer_alias, peer_pubkey, decision, decision_reason, decided_at, measure_at, window_days, predicted_sat_day, action_cost_sat, before_metrics, status, snapshot)
values ($1,$2,$3,nullif($4,0),nullif($5,''),nullif($6,''),nullif($7,''),$8,nullif($9,''),$10,$11,$12,$13,$14,$15,$16,$17)`,
		id, item.Rule, item.Kind, int64(item.ChannelID), item.ChannelPoint, item.PeerAlias, item.PeerPubkey, decision, reason, now, now.Add(insightOutcomeWindow),
		int(insightOutcomeWindow.Hours()/24), item.ValueSatDay, item.CostSat, beforeJSON, insightOutcomeStatusPending, raw)
	return err
}

// CancelPendingOutcome voids the open measurement when the operator reopens
// a recommendation: the decision it measured no longer stands.
func (s *InsightsService) CancelPendingOutcome(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(ctx, `update insights_outcomes set status = $2 where insight_id = $1 and status = $3`, id, insightOutcomeStatusVoid, insightOutcomeStatusPending)
	return err
}

// insightOutcomeVerdict is the pure judgement. realized is the change in the
// channel's net (direct + assisted − refill cost) per day between the two
// windows; for "stop buying" the question is whether the spending stopped.
func insightOutcomeVerdict(kind, decision string, predictedSatDay int64, before, after insightWindowMetrics, windowDays int) (verdict string, realizedSatDay int64, errorPct *float64) {
	if windowDays <= 0 {
		windowDays = 7
	}
	days := float64(windowDays)
	realized := int64(math.Round(float64(after.netSat()-before.netSat()) / days))
	achieved := false
	failed := false
	switch kind {
	case insightKindStopBuying:
		beforeCost := float64(before.RebalCostSat) / days
		afterCost := float64(after.RebalCostSat) / days
		// What the operator saves is the spending that stopped.
		realized = int64(math.Round(beforeCost - afterCost))
		achieved = beforeCost > 0 && afterCost <= beforeCost*insightOutcomeStopBuyShare
		failed = beforeCost > 0 && afterCost >= beforeCost
		if beforeCost == 0 {
			achieved = afterCost == 0
		}
	default:
		target := float64(predictedSatDay) * insightOutcomeHitShare
		achieved = realized > 0 && float64(realized) >= target
		failed = realized <= 0
	}
	if decision == insightOutcomeDecisionSkip {
		if achieved {
			verdict = insightVerdictUnneeded
		} else {
			verdict = insightVerdictConfirmed
		}
		return verdict, realized, nil
	}
	switch {
	case achieved:
		verdict = insightVerdictHit
	case failed:
		verdict = insightVerdictMiss
	default:
		verdict = insightVerdictNeutral
	}
	if predictedSatDay > 0 {
		pct := math.Abs(float64(realized-predictedSatDay)) * 100 / float64(predictedSatDay)
		pct = math.Round(pct*10) / 10
		errorPct = &pct
	}
	return verdict, realized, errorPct
}

// RunDueOutcomes closes every measurement whose week has passed.
func (s *InsightsService) RunDueOutcomes(ctx context.Context, now time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	rows, err := s.db.Query(ctx, `select id, kind, decision, coalesce(channel_id,0), coalesce(peer_pubkey,''), decided_at, window_days, predicted_sat_day, before_metrics
 from insights_outcomes where status = $1 and measure_at <= $2 order by measure_at limit 200`, insightOutcomeStatusPending, now)
	if err != nil {
		return 0, err
	}
	type due struct {
		id         int64
		kind       string
		decision   string
		channelID  int64
		peer       string
		decidedAt  time.Time
		windowDays int
		predicted  int64
		before     insightWindowMetrics
	}
	var items []due
	for rows.Next() {
		var d due
		var beforeRaw []byte
		if err := rows.Scan(&d.id, &d.kind, &d.decision, &d.channelID, &d.peer, &d.decidedAt, &d.windowDays, &d.predicted, &beforeRaw); err != nil {
			rows.Close()
			return 0, err
		}
		_ = json.Unmarshal(beforeRaw, &d.before)
		items = append(items, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	closed := 0
	for _, d := range items {
		window := time.Duration(d.windowDays) * 24 * time.Hour
		if window <= 0 {
			window = insightOutcomeWindow
		}
		after, err := s.measureInsightWindow(ctx, uint64(d.channelID), d.peer, d.decidedAt, d.decidedAt.Add(window))
		if err != nil {
			_, _ = s.db.Exec(ctx, `update insights_outcomes set measurement_error = $2 where id = $1`, d.id, err.Error())
			continue
		}
		verdict, realized, errPct := insightOutcomeVerdict(d.kind, d.decision, d.predicted, d.before, after, d.windowDays)
		afterJSON, _ := json.Marshal(after)
		if _, err := s.db.Exec(ctx, `update insights_outcomes set after_metrics = $2, realized_sat_day = $3, error_pct = $4, verdict = $5, status = $6, measured_at = $7, measurement_error = null where id = $1`,
			d.id, afterJSON, realized, errPct, verdict, insightOutcomeStatusDone, now); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// Scorecard aggregates the measured outcomes per rule and lists the recent
// ones. Weights start to apply once a rule has enough measurements.
func (s *InsightsService) Scorecard(ctx context.Context) (InsightScorecard, error) {
	card := InsightScorecard{Rules: []InsightRuleScore{}, Recent: []InsightOutcome{}}
	if s == nil || s.db == nil {
		return card, nil
	}
	rows, err := s.db.Query(ctx, `select id, insight_id, rule, kind, coalesce(channel_id,0), coalesce(channel_point,''), coalesce(peer_alias,''), coalesce(peer_pubkey,''), decision, coalesce(decision_reason,''),
 decided_at, measure_at, window_days, predicted_sat_day, action_cost_sat, before_metrics, after_metrics, realized_sat_day, error_pct, coalesce(verdict,''), status, measured_at, coalesce(measurement_error,'')
 from insights_outcomes where status <> $1 order by decided_at desc limit 2000`, insightOutcomeStatusVoid)
	if err != nil {
		return card, err
	}
	defer rows.Close()
	byRule := map[string]*InsightRuleScore{}
	errSum := map[string]float64{}
	errN := map[string]int{}
	for rows.Next() {
		var o InsightOutcome
		var channelID int64
		var beforeRaw, afterRaw []byte
		if err := rows.Scan(&o.ID, &o.InsightID, &o.Rule, &o.Kind, &channelID, &o.ChannelPoint, &o.PeerAlias, &o.PeerPubkey, &o.Decision, &o.DecisionReason,
			&o.DecidedAt, &o.MeasureAt, &o.WindowDays, &o.PredictedSatDay, &o.ActionCostSat, &beforeRaw, &afterRaw, &o.RealizedSatDay, &o.ErrorPct, &o.Verdict, &o.Status, &o.MeasuredAt, &o.MeasurementError); err != nil {
			return card, err
		}
		if channelID > 0 {
			o.ChannelID = uint64(channelID)
		}
		_ = json.Unmarshal(beforeRaw, &o.Before)
		if len(afterRaw) > 0 {
			var after insightWindowMetrics
			if json.Unmarshal(afterRaw, &after) == nil {
				o.After = &after
			}
		}
		score := byRule[o.Rule]
		if score == nil {
			score = &InsightRuleScore{Rule: o.Rule}
			byRule[o.Rule] = score
		}
		if o.Status == insightOutcomeStatusPending {
			score.Pending++
			card.Pending++
		} else {
			score.Measured++
			card.Measured++
			if o.Decision == insightOutcomeDecisionDone {
				score.Followed++
				card.Followed++
				if o.ErrorPct != nil {
					errSum[o.Rule] += *o.ErrorPct
					errN[o.Rule]++
				}
			} else {
				score.Dismissed++
			}
			switch o.Verdict {
			case insightVerdictHit:
				score.Hits++
				card.Hits++
			case insightVerdictNeutral:
				score.Neutral++
			case insightVerdictMiss:
				score.Misses++
			case insightVerdictConfirmed:
				score.Confirmed++
			case insightVerdictUnneeded:
				score.Unneeded++
			}
		}
		if len(card.Recent) < insightOutcomesRecentLimit {
			card.Recent = append(card.Recent, o)
		}
	}
	if err := rows.Err(); err != nil {
		return card, err
	}
	for _, rule := range []string{"R1", "R2", "R3", "R4", "R5", "R6", "R7"} {
		score := byRule[rule]
		if score == nil {
			score = &InsightRuleScore{Rule: rule}
		}
		if score.Measured > 0 {
			score.Accuracy = math.Round(float64(score.Hits+score.Confirmed)/float64(score.Measured)*1000) / 1000
		}
		if n := errN[rule]; n > 0 {
			score.MeanErrorPct = math.Round(errSum[rule]/float64(n)*10) / 10
		}
		score.Weight, score.WeightApplied = insightRuleWeight(*score)
		card.Rules = append(card.Rules, *score)
	}
	return card, nil
}

// insightRuleWeight turns a rule's accuracy into a priority multiplier once it
// has enough measurements: 1.0 at 50% accuracy, down to 0.5 when it never
// lands and up to 1.5 when it always does. Below the minimum sample the rule
// keeps its designed priority.
func insightRuleWeight(score InsightRuleScore) (float64, bool) {
	if score.Measured < insightScorecardMinMeasured {
		return 1, false
	}
	weight := 0.5 + score.Accuracy
	if weight < insightRuleWeightMin {
		weight = insightRuleWeightMin
	}
	if weight > insightRuleWeightMax {
		weight = insightRuleWeightMax
	}
	return math.Round(weight*100) / 100, true
}

func insightRuleWeightsFromScorecard(card InsightScorecard) map[string]float64 {
	weights := make(map[string]float64, len(card.Rules))
	for _, score := range card.Rules {
		if score.WeightApplied {
			weights[score.Rule] = score.Weight
		}
	}
	return weights
}

// startInsightsOutcomeLoop closes due measurements every hour. Started once,
// after the schema exists; a failing run only logs and tries again later.
func (s *Server) startInsightsOutcomeLoop(svc *InsightsService) {
	go func() {
		timer := time.NewTimer(insightOutcomeLoopFirstWait)
		defer timer.Stop()
		for {
			<-timer.C
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			closed, err := svc.RunDueOutcomes(ctx, time.Now().UTC())
			cancel()
			if err != nil && s.logger != nil {
				s.logger.Printf("insights: outcome measurement failed: %v", err)
			} else if closed > 0 && s.logger != nil {
				s.logger.Printf("insights: measured %d outcome(s)", closed)
				s.insightsCacheMu.Lock()
				s.insightsCache = nil
				s.insightsCacheMu.Unlock()
			}
			timer.Reset(insightOutcomeLoopInterval)
		}
	}()
}
