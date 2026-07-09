package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
)

// autoTargetSignals is the per-channel input to the pure decision function. All
// fields come from signals already computed during the autopilot scan.
type autoTargetSignals struct {
	ChannelID          uint64
	CurrentTargetPct   float64
	CapacitySat        int64
	DrainRateSatPerHr  int64
	Revenue7dSat       int64
	SuccessRate        float64 // recent historical success rate for this target
	Attempts           int     // recent rebalance attempts (0 => no route history)
	StructuralFails24h int
	IsRoundCandidate   bool // selected as a candidate this round => supply-limited
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
// unit-tested exhaustively. UP requires the channel to be a supply-limited round
// candidate with strong seller signals; DOWN fires for stalled/poor-route
// channels. Hysteresis (UpSuccessThreshold > DownSuccessThreshold) leaves a
// neutral hold band that prevents flapping.
func decideAutoTargetAdjustment(sig autoTargetSignals, cfg RebalanceConfig) autoTargetDecision {
	step := cfg.AutoTargetStepPct
	cur := sig.currentInt()
	effMax := autoTargetEffectiveMaxPct(cfg, sig.CapacitySat)
	minPct := cfg.AutoTargetMinPct
	dec := autoTargetDecision{Direction: autoTargetNoop, NewTarget: cur, EffectiveMaxPct: effMax, Reason: "hold"}

	// UP eligibility: only supply-limited round candidates that are selling fast.
	upReason := ""
	if sig.IsRoundCandidate &&
		sig.DrainRateSatPerHr >= cfg.AutoTargetMinDrainRateSatPerHr &&
		sig.StructuralFails24h == 0 {
		switch {
		case sig.Attempts > 0 &&
			sig.SuccessRate >= cfg.AutoTargetUpSuccessThreshold &&
			sig.Revenue7dSat >= cfg.AutoTargetMinRevenue7dSat:
			upReason = "sells_fast_viable"
		case sig.Attempts == 0 &&
			sig.DrainRateSatPerHr >= int64(float64(cfg.AutoTargetMinDrainRateSatPerHr)*cfg.AutoTargetDrainFirstMultiplier) &&
			sig.Revenue7dSat >= cfg.AutoTargetMinRevenue7dSat*2:
			// drain-first: no route history yet, so require a much stronger drain
			// and revenue prior before betting capital on the channel.
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

	// DOWN: stopped selling / poor routes / forcing too hard.
	downReason := ""
	switch {
	case sig.DrainRateSatPerHr < cfg.AutoTargetMinDrainRateSatPerHr/4:
		downReason = "drain_stalled"
	case sig.Attempts > 0 && sig.SuccessRate < cfg.AutoTargetDownSuccessThreshold:
		downReason = "low_success"
	case sig.StructuralFails24h >= 2:
		downReason = "structural_cooldowns"
	}
	// Do not demote a channel that is still earning. A quiet 24h drain window on a
	// bursty seller (WoS, exchanges, kappa) is not a reason to lower its target —
	// only demote channels that are BOTH idle AND unprofitable. This preserves the
	// original "fill-and-hold" intent (filled but not selling => low revenue => still
	// demoted) while sparing real earners that just had a lull.
	if downReason != "" && sig.Revenue7dSat >= cfg.AutoTargetMinRevenue7dSat {
		dec.Reason = "earning_hold"
		return dec
	}
	if downReason != "" {
		newT := cur - step
		if newT < minPct {
			newT = minPct
		}
		if newT < cur {
			return autoTargetDecision{Direction: autoTargetDown, Delta: newT - cur, NewTarget: newT, EffectiveMaxPct: effMax, Reason: downReason}
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
	snapByID := make(map[uint64]RebalanceChannel, len(snapshots))
	for _, snap := range snapshots {
		snapByID[snap.ChannelID] = snap
	}
	lastDecided := s.loadAutoTargetLastDecided(ctx)
	interval := time.Duration(cfg.AutoTargetEvalIntervalHours) * time.Hour
	upsRemaining := cfg.AutoTargetMaxUpsPerCycle
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
		sig := autoTargetSignals{
			ChannelID:          snap.ChannelID,
			CurrentTargetPct:   snap.TargetOutboundPct,
			CapacitySat:        snap.CapacitySat,
			DrainRateSatPerHr:  snap.DrainRateSatPerHour,
			Revenue7dSat:       snap.Revenue7dSat,
			SuccessRate:        rate,
			Attempts:           attempts,
			StructuralFails24h: structuralCooldowns[snap.ChannelID].Failures,
			IsRoundCandidate:   isCandidate,
		}
		dec := decideAutoTargetAdjustment(sig, cfg)
		switch dec.Direction {
		case autoTargetUp:
			if upsRemaining <= 0 {
				// Wanted to raise but the per-cycle throttle is spent. Record the
				// intent (not applied, so it does not start a cooldown) and move on.
				s.persistAutoTargetDecision(ctx, snap, sig, cfg, autoTargetDecision{
					Direction:       autoTargetNoop,
					NewTarget:       sig.currentInt(),
					EffectiveMaxPct: dec.EffectiveMaxPct,
					Reason:          "ups_throttled",
				}, false)
				return
			}
			if s.applyAutoTarget(ctx, snap, dec) {
				upsRemaining--
				s.persistAutoTargetDecision(ctx, snap, sig, cfg, dec, true)
			}
		case autoTargetDown:
			if s.applyAutoTarget(ctx, snap, dec) {
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
		"structural_fails_24h":  sig.StructuralFails24h,
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
		args = append(args, int64(channelID))
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
