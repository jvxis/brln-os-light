package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// AutoTarget adjusts each channel's target_outbound_pct automatically, running
// inside the autopilot cycle (runAutoScan) rather than in a separate loop. The
// design intent (aligned with the operator):
//
//   - UP raises the target of channels that are "selling well and fast" — high
//     drain velocity, real revenue, viable routes. UP only considers channels
//     that were selected as candidates in the current rebalance round, which are
//     supply-limited by construction (they are below target and actively
//     draining). This deliberately avoids the jun/2026 mistake of raising the
//     target of demand-limited channels (fill-and-hold) using past forward_out.
//   - DOWN lowers the target of managed channels that stopped selling
//     (fill-and-hold), route poorly, or keep hitting structural cooldowns.
//   - Capacity-aware: a per-channel effective max derived from an absolute
//     local-liquidity cap keeps a giant channel from getting a disproportionate
//     absolute target.
//   - Budget-aware: UP is throttled to AutoTargetMaxUpsPerCycle per cycle so a
//     wave of fills does not blow the rebalance budget.
//
// Feature is opt-in (AutoTargetEnabled, default OFF). Applied changes and
// throttled-UP intents are recorded in rebalance_auto_target_history for audit;
// plain holds are not persisted to keep the table lean.

const (
	autoTargetUp   = "up"
	autoTargetDown = "down"
	autoTargetNoop = "noop"

	// autoTargetHistoryRetention bounds how long AutoTarget decision rows are kept.
	autoTargetHistoryRetention = 90 * 24 * time.Hour

	// autoTargetDeadRouteAttempts: at/above this many recent attempts with zero
	// success, a target is treated as unrefillable and skipped for UP — raising a
	// target we literally cannot fill is pointless. Set above 0 success so a
	// hard-to-refill-but-earning channel (kappa) still qualifies.
	autoTargetDeadRouteAttempts = 8
)

// channelSellThrough is the per-target rebalance economics over the attribution
// window: how much was rebalanced in (SentSat), how much of that liquidity was
// forwarded back out (ForwardSat), and the resulting sell-through ratio. It
// answers "does the liquidity I put here actually sell?".
type channelSellThrough struct {
	SentSat       int64
	ForwardSat    int64
	ForwardFeeSat int64
	SellThrough   float64
}

// autoTargetNodeBaseline holds the node's own reference points, so AutoTarget
// thresholds are relative to THIS node instead of absolute constants that don't
// fit a hard-routing node. Computed each scan.
type autoTargetNodeBaseline struct {
	SellThrough        float64 // node-wide forward/sent
	MedianRevenue7dSat int64
	DrainP70           int64
}

// autoTargetSignals is the per-channel input to the pure decision function. All
// fields come from signals already computed during the autopilot scan.
type autoTargetSignals struct {
	ChannelID         uint64
	CurrentTargetPct  float64
	CapacitySat       int64
	DrainRateSatPerHr int64
	Revenue7dSat      int64
	SuccessRate       float64 // recent historical success rate for this target
	Attempts          int     // recent rebalance attempts (0 => no route history)
	SellThrough       float64 // forward/sent over the attribution window
	HasHistory        bool    // has rebalance history (SentSat > 0) => sell-through is meaningful
	NodeSellThrough   float64 // baseline snapshot, for audit
	IsRoundCandidate  bool    // selected as a candidate this round => supply-limited
}

func (sig autoTargetSignals) currentInt() int {
	return int(math.Round(sig.CurrentTargetPct))
}

type autoTargetDecision struct {
	Direction       string
	Delta           int
	NewTarget       int
	EffectiveMaxPct int
	Reason          string
}

// autoTargetEffectiveMaxPct is the per-channel upper bound on target_outbound_pct.
// It is the configured AutoTargetMaxPct, further capped so the absolute local
// liquidity target never exceeds AutoTargetMaxLocalSat — this is what prevents a
// large-capacity channel from getting a disproportionate absolute target.
func autoTargetEffectiveMaxPct(cfg RebalanceConfig, capacitySat int64) int {
	maxPct := cfg.AutoTargetMaxPct
	if cfg.AutoTargetMaxLocalSat > 0 && capacitySat > 0 {
		capPct := int((cfg.AutoTargetMaxLocalSat * 100) / capacitySat)
		if capPct < maxPct {
			maxPct = capPct
		}
	}
	if maxPct < cfg.AutoTargetMinPct {
		maxPct = cfg.AutoTargetMinPct
	}
	return maxPct
}

// decideAutoTargetAdjustment is the pure decision core (no I/O), so it can be
// unit-tested exhaustively. Signals are driven by SELL-THROUGH relative to the
// node's own baseline — not absolute success/drain constants, which don't fit a
// hard-routing node and turned v1 into a one-way demoter.
//
//   - UP raises the target of a supply-limited round candidate that sells its
//     liquidity BETTER than the node's typical channel (sell_through above
//     baseline × up_factor) and earns. This lets a hard-to-refill but high-earning
//     channel (kappa) rise even though its rebalance success rate is low.
//   - DOWN lowers ONLY channels that absorbed rebalance capital and did not sell
//     it back (has history, low revenue, sell_through below baseline × down_factor)
//     — true fill-and-hold waste. Channels never rebalanced are left alone, which
//     stops the node-wide flattening a quiet drain window used to cause.
//
// Hysteresis (up_factor > down_factor) leaves a neutral hold band.
func decideAutoTargetAdjustment(sig autoTargetSignals, baseline autoTargetNodeBaseline, cfg RebalanceConfig) autoTargetDecision {
	step := cfg.AutoTargetStepPct
	cur := sig.currentInt()
	effMax := autoTargetEffectiveMaxPct(cfg, sig.CapacitySat)
	minPct := cfg.AutoTargetMinPct
	dec := autoTargetDecision{Direction: autoTargetNoop, NewTarget: cur, EffectiveMaxPct: effMax, Reason: "hold"}

	upThresh := baseline.SellThrough * cfg.AutoTargetUpSellThroughFactor
	downThresh := baseline.SellThrough * cfg.AutoTargetDownSellThroughFactor
	deadRoute := sig.Attempts >= autoTargetDeadRouteAttempts && sig.SuccessRate <= 0

	// UP: only supply-limited round candidates that earn and sell above baseline.
	upReason := ""
	if sig.IsRoundCandidate && sig.Revenue7dSat >= cfg.AutoTargetMinRevenue7dSat && !deadRoute {
		switch {
		case sig.HasHistory && baseline.SellThrough > 0 && sig.SellThrough >= upThresh:
			upReason = "sells_above_node"
		case !sig.HasHistory &&
			baseline.MedianRevenue7dSat > 0 &&
			sig.Revenue7dSat >= int64(float64(baseline.MedianRevenue7dSat)*cfg.AutoTargetDrainFirstMultiplier) &&
			baseline.DrainP70 > 0 && sig.DrainRateSatPerHr >= baseline.DrainP70:
			// drain-first: no rebalance history yet, so bootstrap only a strong
			// above-node earner that is actively draining.
			upReason = "drain_first"
		}
	}
	if upReason != "" {
		newT := cur + step
		if newT > effMax {
			newT = effMax
		}
		if newT > cur {
			return autoTargetDecision{Direction: autoTargetUp, Delta: newT - cur, NewTarget: newT, EffectiveMaxPct: effMax, Reason: upReason}
		}
		dec.Reason = "at_effective_max"
		return dec
	}

	// DOWN: only channels that absorbed rebalance capital and did not sell it back.
	// The Revenue < min gate is the earning-hold: a channel still earning is never
	// auto-demoted, even on low sell-through.
	if sig.HasHistory &&
		sig.Revenue7dSat < cfg.AutoTargetMinRevenue7dSat &&
		baseline.SellThrough > 0 &&
		sig.SellThrough < downThresh {
		newT := cur - step
		if newT < minPct {
			newT = minPct
		}
		if newT < cur {
			return autoTargetDecision{Direction: autoTargetDown, Delta: newT - cur, NewTarget: newT, EffectiveMaxPct: effMax, Reason: "fill_and_hold"}
		}
		dec.Reason = "at_min"
		return dec
	}
	return dec
}

// evaluateAutoTarget runs one AutoTarget pass over the current scan. It is called
// from runAutoScan's sovereign branch, right after the round candidates are
// built, reusing the snapshots, pair stats and structural cooldowns already
// loaded for the autopilot. UP is evaluated over candidates in ranked order (so
// the strongest sellers win the limited UP slots); DOWN over the remaining
// managed channels.
func (s *RebalanceService) evaluateAutoTarget(
	ctx context.Context,
	cfg RebalanceConfig,
	scanAt time.Time,
	snapshots []RebalanceChannel,
	settings map[uint64]channelSetting,
	candidates []rebalanceTarget,
	pairStats map[uint64]rebalanceTargetPairStats,
	structuralCooldowns map[uint64]sovereignTargetStructuralCooldownStat,
) {
	if !cfg.AutoTargetEnabled {
		return
	}
	sellThrough, nodeSellThrough := s.loadChannelSellThrough7d(ctx, cfg)
	baseline := autoTargetNodeBaseline{
		SellThrough:        nodeSellThrough,
		MedianRevenue7dSat: autoTargetMedianRevenue(snapshots),
		DrainP70:           autoTargetDrainPercentile(snapshots, 70),
	}
	snapByID := make(map[uint64]RebalanceChannel, len(snapshots))
	for _, snap := range snapshots {
		snapByID[snap.ChannelID] = snap
	}
	lastDecided := s.loadAutoTargetLastDecided(ctx)
	interval := time.Duration(cfg.AutoTargetEvalIntervalHours) * time.Hour
	upsRemaining := cfg.AutoTargetMaxUpsPerCycle
	downsRemaining := cfg.AutoTargetMaxDownsPerCycle
	processed := make(map[uint64]bool, len(snapshots))

	handle := func(snap RebalanceChannel, isCandidate bool) {
		setting := settings[snap.ChannelID]
		if !setting.AutoTargetManaged {
			return
		}
		if isChannelAutomationParked(setting.AutomationMode) {
			return
		}
		if last, ok := lastDecided[snap.ChannelID]; ok && scanAt.Sub(last) < interval {
			return
		}
		rate, attempts := sovereignHistoricalSuccessRate(pairStats[snap.ChannelID])
		st := sellThrough[snap.ChannelID]
		sig := autoTargetSignals{
			ChannelID:         snap.ChannelID,
			CurrentTargetPct:  snap.TargetOutboundPct,
			CapacitySat:       snap.CapacitySat,
			DrainRateSatPerHr: snap.DrainRateSatPerHour,
			Revenue7dSat:      snap.Revenue7dSat,
			SuccessRate:       rate,
			Attempts:          attempts,
			SellThrough:       st.SellThrough,
			HasHistory:        st.SentSat > 0,
			NodeSellThrough:   nodeSellThrough,
			IsRoundCandidate:  isCandidate,
		}
		dec := decideAutoTargetAdjustment(sig, baseline, cfg)
		switch dec.Direction {
		case autoTargetUp:
			if upsRemaining <= 0 {
				s.persistAutoTargetDecision(ctx, snap, sig, cfg, autoTargetDecision{
					Direction: autoTargetNoop, NewTarget: sig.currentInt(),
					EffectiveMaxPct: dec.EffectiveMaxPct, Reason: "ups_throttled",
				}, false)
				return
			}
			if s.applyAutoTarget(ctx, snap, dec) {
				upsRemaining--
				s.persistAutoTargetDecision(ctx, snap, sig, cfg, dec, true)
			}
		case autoTargetDown:
			if downsRemaining <= 0 {
				s.persistAutoTargetDecision(ctx, snap, sig, cfg, autoTargetDecision{
					Direction: autoTargetNoop, NewTarget: sig.currentInt(),
					EffectiveMaxPct: dec.EffectiveMaxPct, Reason: "downs_throttled",
				}, false)
				return
			}
			if s.applyAutoTarget(ctx, snap, dec) {
				downsRemaining--
				s.persistAutoTargetDecision(ctx, snap, sig, cfg, dec, true)
			}
		default:
			// Plain hold: not persisted (keep the audit table lean).
		}
	}

	for _, cand := range candidates {
		chID := cand.Channel.ChannelID
		if processed[chID] {
			continue
		}
		processed[chID] = true
		snap, ok := snapByID[chID]
		if !ok {
			snap = cand.Channel
		}
		handle(snap, true)
	}
	for _, snap := range snapshots {
		if processed[snap.ChannelID] {
			continue
		}
		processed[snap.ChannelID] = true
		handle(snap, false)
	}
}

// autoTargetMedianRevenue returns the median Revenue7dSat across active channels.
func autoTargetMedianRevenue(snaps []RebalanceChannel) int64 {
	vals := make([]int64, 0, len(snaps))
	for _, s := range snaps {
		if s.Active {
			vals = append(vals, s.Revenue7dSat)
		}
	}
	if len(vals) == 0 {
		return 0
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return vals[len(vals)/2]
}

// autoTargetDrainPercentile returns the p-th percentile of drain rate across
// actively-draining channels (drain > 0), so "actively draining" is relative to
// this node rather than a fixed constant.
func autoTargetDrainPercentile(snaps []RebalanceChannel, p int) int64 {
	vals := make([]int64, 0, len(snaps))
	for _, s := range snaps {
		if s.Active && s.DrainRateSatPerHour > 0 {
			vals = append(vals, s.DrainRateSatPerHour)
		}
	}
	if len(vals) == 0 {
		return 0
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	idx := (p * len(vals)) / 100
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx]
}

// loadChannelSellThrough7d returns per-target sell-through (attributed forward
// volume / rebalanced volume) over the sovereign attribution window, plus the
// node-wide sell-through baseline. Mirrors fetchSovereignAutopilotEconomics7d but
// grouped by target_channel_id.
func (s *RebalanceService) loadChannelSellThrough7d(ctx context.Context, cfg RebalanceConfig) (map[uint64]channelSellThrough, float64) {
	out := map[uint64]channelSellThrough{}
	if s.db == nil {
		return out, 0
	}
	snapshot, err := s.loadSovereignAttributionSnapshot(ctx, cfg)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("auto-target sell-through load failed: %v", err)
		}
		return out, 0
	}
	feeByTargetMsat := map[uint64]int64{}
	var totalSent, totalForward int64
	for _, lot := range snapshot.Lots {
		if lot.TriggerReason != rebalanceSovereignReason || lot.CompletedAt.Before(snapshot.MetricSince) {
			continue
		}
		attributed := snapshot.Fast[lot.JobID]
		stat := out[lot.TargetChannelID]
		stat.SentSat += lot.SentSat
		stat.ForwardSat += attributed.ForwardAmountSat
		feeByTargetMsat[lot.TargetChannelID] += attributed.ForwardFeeMsat
		out[lot.TargetChannelID] = stat
		totalSent += lot.SentSat
		totalForward += attributed.ForwardAmountSat
	}
	for targetID, stat := range out {
		stat.ForwardFeeSat = feeByTargetMsat[targetID] / 1000
		if stat.SentSat > 0 {
			stat.SellThrough = float64(stat.ForwardSat) / float64(stat.SentSat)
		}
		out[targetID] = stat
	}
	node := 0.0
	if totalSent > 0 {
		node = float64(totalForward) / float64(totalSent)
	}
	return out, node
}

func (s *RebalanceService) applyAutoTarget(ctx context.Context, snap RebalanceChannel, dec autoTargetDecision) bool {
	newPct := float64(dec.NewTarget)
	if err := s.UpdateChannelTargetSettings(ctx, snap.ChannelID, snap.ChannelPoint, &newPct, nil, nil, nil); err != nil {
		if s.logger != nil {
			s.logger.Printf("auto-target apply failed ch=%d: %v", snap.ChannelID, err)
		}
		return false
	}
	return true
}

// loadAutoTargetLastDecided returns the last APPLIED change time per channel, used
// for the per-channel eval cooldown. Only applied up/down rows count, so a
// throttled-UP intent never blocks a channel from being reconsidered.
func (s *RebalanceService) loadAutoTargetLastDecided(ctx context.Context) map[uint64]time.Time {
	out := map[uint64]time.Time{}
	if s.db == nil {
		return out
	}
	rows, err := s.db.Query(ctx, `
select channel_id, max(decided_at)
from rebalance_auto_target_history
where applied = true
group by channel_id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var chID int64
		var at time.Time
		if err := rows.Scan(&chID, &at); err != nil {
			return out
		}
		out[uint64(chID)] = at
	}
	return out
}

func (s *RebalanceService) persistAutoTargetDecision(ctx context.Context, snap RebalanceChannel, sig autoTargetSignals, cfg RebalanceConfig, dec autoTargetDecision, applied bool) {
	if s.db == nil {
		return
	}
	signals := map[string]any{
		"drain_rate_sat_per_hr": sig.DrainRateSatPerHr,
		"success_rate":          sig.SuccessRate,
		"attempts":              sig.Attempts,
		"revenue_7d_sat":        sig.Revenue7dSat,
		"sell_through":          sig.SellThrough,
		"has_history":           sig.HasHistory,
		"node_sell_through":     sig.NodeSellThrough,
		"is_round_candidate":    sig.IsRoundCandidate,
		"effective_max_pct":     dec.EffectiveMaxPct,
		"capacity_sat":          sig.CapacitySat,
		"reason":                dec.Reason,
	}
	blob, err := json.Marshal(signals)
	if err != nil {
		blob = []byte("{}")
	}
	if _, err := s.db.Exec(ctx, `
insert into rebalance_auto_target_history
  (channel_id, channel_point, decided_at, prev_target_pct, new_target_pct, direction, applied, trigger_signals, measurement_window_hours)
values ($1,$2,now(),$3,$4,$5,$6,$7,$8)`,
		int64(snap.ChannelID), snap.ChannelPoint, sig.currentInt(), dec.NewTarget, dec.Direction, applied, blob, cfg.AutoTargetEvalIntervalHours,
	); err != nil && s.logger != nil {
		s.logger.Printf("auto-target history persist failed ch=%d: %v", snap.ChannelID, err)
	}
}

// RebalanceAutoTargetHistoryItem is one row of AutoTarget decision history.
type RebalanceAutoTargetHistoryItem struct {
	ID                     int64           `json:"id"`
	ChannelID              uint64          `json:"channel_id"`
	ChannelPoint           string          `json:"channel_point,omitempty"`
	DecidedAt              string          `json:"decided_at"`
	PrevTargetPct          int             `json:"prev_target_pct"`
	NewTargetPct           int             `json:"new_target_pct"`
	Direction              string          `json:"direction"`
	Applied                bool            `json:"applied"`
	TriggerSignals         json.RawMessage `json:"trigger_signals,omitempty"`
	MeasurementWindowHours int             `json:"measurement_window_hours"`
}

// AutoTargetHistory returns recent AutoTarget decisions, newest first, optionally
// filtered by channel and a since timestamp.
func (s *RebalanceService) AutoTargetHistory(ctx context.Context, channelID uint64, limit int, since time.Time) ([]RebalanceAutoTargetHistoryItem, error) {
	items := []RebalanceAutoTargetHistoryItem{}
	if s.db == nil {
		return items, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `
select id, channel_id, coalesce(channel_point,''), decided_at,
       coalesce(prev_target_pct,0), coalesce(new_target_pct,0), coalesce(direction,''),
       applied, trigger_signals, coalesce(measurement_window_hours,0)
from rebalance_auto_target_history`
	args := []any{}
	conds := []string{}
	if channelID != 0 {
		channelIDDB, ok := uint64ToInt64(channelID)
		if !ok {
			return items, errors.New("channel_id exceeds database range")
		}
		args = append(args, channelIDDB)
		conds = append(conds, fmt.Sprintf("channel_id = $%d", len(args)))
	}
	if !since.IsZero() {
		args = append(args, since)
		conds = append(conds, fmt.Sprintf("decided_at >= $%d", len(args)))
	}
	for i, c := range conds {
		if i == 0 {
			q += "\nwhere " + c
		} else {
			q += " and " + c
		}
	}
	args = append(args, limit)
	q += fmt.Sprintf("\norder by decided_at desc limit $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return items, err
	}
	defer rows.Close()
	for rows.Next() {
		var it RebalanceAutoTargetHistoryItem
		var chID int64
		var decidedAt time.Time
		var signals []byte
		if err := rows.Scan(&it.ID, &chID, &it.ChannelPoint, &decidedAt, &it.PrevTargetPct, &it.NewTargetPct, &it.Direction, &it.Applied, &signals, &it.MeasurementWindowHours); err != nil {
			return items, err
		}
		it.ChannelID = uint64(chID)
		it.DecidedAt = decidedAt.UTC().Format(time.RFC3339)
		if len(signals) > 0 {
			it.TriggerSignals = json.RawMessage(signals)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}
