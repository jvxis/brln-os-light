package server

import (
	"context"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	// Discovery ladder: fee steps above the economic cap, at the minimum
	// rebalance amount, to measure the route price of a drained channel the
	// normal ladder cannot reach. "No route" is usually "no route at this
	// price"; one cheap probe turns that into a number AutoFee can price in.
	discoveryStepsMax              = 4
	discoveryCeilingPctDefault     = 150
	discoveryCeilingPctMin         = 110
	discoveryCeilingPctMax         = 200
	discoveryDailyBudgetSatDefault = 300
	discoveryDailyBudgetSatMax     = 100_000

	sovereignDiscoveryReason       = "sovereign-discovery"
	sovereignDiscoveryQueuedReason = "discovery_queued"
	sovereignDiscoveryShadowReason = "would_discover"
	// One probe per scan and one in flight at a time: each one is a broad
	// pathfinding call, and a burst of them is what saturates LND.
	sovereignDiscoveryMaxPerCycle = 1
	// One ladder per channel per window, whatever its outcome.
	sovereignDiscoveryWindow = 24 * time.Hour

	// A probe below this amount measures base fees, not the route price:
	// 1 sat on 1,000 sats is already 1,000 ppm. Operators with a smaller
	// min_amount_sat still probe at this floor.
	sovereignDiscoveryMinAmountSat = int64(20_000)

	sovereignDiscoveryNoRouteJobReason     = "discovery: no route up to "
	sovereignDiscoveryUnavailableJobReason = "discovery: fast path unavailable"
)

type sovereignDiscoveryTargetStat struct {
	Jobs      int
	Succeeded bool
	LastAt    time.Time
}

type sovereignDiscoveryState struct {
	Unavailable   bool
	InFlight      int
	SpentTodaySat int64
	ByTarget      map[uint64]sovereignDiscoveryTargetStat
}

// sovereignDiscoveryRun carries the per-scan discovery bookkeeping. The state
// is loaded on first use, so a scan with no route-gated target costs no query.
type sovereignDiscoveryRun struct {
	loaded      bool
	state       sovereignDiscoveryState
	queued      int
	reservedSat int64
}

// sovereignDiscoveryConfigured: the ladder is opt-in (discovery_steps > 0),
// needs its own budget and only runs through the delegated fast path, which is
// a single multi-source call and leaves no per-source failure behind.
func sovereignDiscoveryConfigured(cfg RebalanceConfig) bool {
	return cfg.DiscoverySteps > 0 && cfg.DiscoveryDailyBudgetSat > 0 && cfg.DelegatedFastPathEnabled
}

func isSovereignDiscoveryJob(source string, reason string) bool {
	return strings.EqualFold(strings.TrimSpace(source), "auto") && strings.TrimSpace(reason) == sovereignDiscoveryReason
}

// isSovereignRouteGateReason: the skip reasons that mean "the normal ladder
// could not reach this target at the economic fee cap".
func isSovereignRouteGateReason(reason string) bool {
	switch reason {
	case sovereignTargetStructuralCooldownReason, sovereignRouteDeadOpportunityReason, sovereignLowSuccessOpportunityReason:
		return true
	default:
		return false
	}
}

// sovereignDiscoveryStepPct spreads the steps evenly between the normal cap
// (100%) and discovery_ceiling_pct. Two steps with a 150% ceiling probe at
// 125% and 150%.
func sovereignDiscoveryStepPct(cfg RebalanceConfig, step int) int {
	steps := cfg.DiscoverySteps
	if steps <= 0 || step <= 0 {
		return 100
	}
	if step > steps {
		step = steps
	}
	ceiling := cfg.DiscoveryCeilingPct
	if ceiling < discoveryCeilingPctMin || ceiling > discoveryCeilingPctMax {
		ceiling = discoveryCeilingPctDefault
	}
	return 100 + (ceiling-100)*step/steps
}

// sovereignDiscoveryNextStep walks the ladder upwards across scans: every
// discovery job of the last window is one spent step. It stops at the first
// success and after the last step, until the window expires.
func sovereignDiscoveryNextStep(cfg RebalanceConfig, stat sovereignDiscoveryTargetStat) (int, bool) {
	if cfg.DiscoverySteps <= 0 || stat.Succeeded || stat.Jobs >= cfg.DiscoverySteps {
		return 0, false
	}
	return stat.Jobs + 1, true
}

// sovereignDiscoveryDrained requires the same evidence the keepalive refill
// uses: an AutoFee refill intent saying the channel is drained, first seen
// more than keepalive_refill_after_hours ago.
func sovereignDiscoveryDrained(target rebalanceTarget, cfg RebalanceConfig, now time.Time) bool {
	if target.CooldownProbe {
		return false
	}
	intent := target.AutomationIntent
	if intent == nil || intent.Kind != automationIntentKindRefillTarget || !isKeepaliveRefillIntentReason(intent.ReasonCode) {
		return false
	}
	if intent.FirstSeenAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	hours := cfg.KeepaliveRefillAfterHours
	if hours < keepaliveRefillAfterHoursMin || hours > keepaliveRefillAfterHoursMax {
		hours = keepaliveRefillAfterHoursDefault
	}
	return now.Sub(intent.FirstSeenAt) >= time.Duration(hours)*time.Hour
}

// sovereignDiscoveryAmount is the operator's minimum rebalance amount, never
// below sovereignDiscoveryMinAmountSat: below that the peer's base fee
// distorts the measured ppm. Zero when the channel's deficit is smaller.
func sovereignDiscoveryAmount(cfg RebalanceConfig, channel RebalanceChannel) int64 {
	amount := effectiveStartAmountSat(cfg)
	if minimum := effectiveMinExecuteSat(cfg); amount < minimum {
		amount = minimum
	}
	if amount < sovereignDiscoveryMinAmountSat {
		amount = sovereignDiscoveryMinAmountSat
	}
	if amount <= 0 || channel.TargetAmountSat < amount {
		return 0
	}
	return amount
}

func sovereignDiscoveryFeeCapPpm(basePpm int64, stepPct int) int64 {
	if basePpm <= 0 || stepPct <= 100 {
		return 0
	}
	return basePpm * int64(stepPct) / 100
}

func sovereignDiscoveryCostSat(amountSat int64, feeCapPpm int64) int64 {
	if amountSat <= 0 || feeCapPpm <= 0 {
		return 0
	}
	return (amountSat*feeCapPpm + 999_999) / 1_000_000
}

func sovereignDiscoveryNoRouteReason(feeCapPpm int64) string {
	return sovereignDiscoveryNoRouteJobReason + strconv.FormatInt(feeCapPpm, 10) + " ppm"
}

func (s *RebalanceService) loadSovereignDiscoveryState(ctx context.Context, now time.Time) sovereignDiscoveryState {
	state := sovereignDiscoveryState{ByTarget: map[uint64]sovereignDiscoveryTargetStat{}}
	if s.db == nil {
		state.Unavailable = true
		return state
	}
	if now.IsZero() {
		now = time.Now()
	}
	rows, err := s.db.Query(ctx, `
select target_channel_id,
  count(*) as jobs,
  coalesce(bool_or(status in ('succeeded','partial')), false) as succeeded,
  count(*) filter (where status in ('queued','running')) as in_flight,
  max(created_at) as last_at
from rebalance_jobs
where source='auto'
  and coalesce(trigger_reason, reason, '')=$1
  and created_at >= $2
group by target_channel_id
`, sovereignDiscoveryReason, now.Add(-sovereignDiscoveryWindow))
	if err != nil {
		state.Unavailable = true
		return state
	}
	for rows.Next() {
		var channelID int64
		var stat sovereignDiscoveryTargetStat
		var inFlight int
		if err := rows.Scan(&channelID, &stat.Jobs, &stat.Succeeded, &inFlight, &stat.LastAt); err != nil {
			rows.Close()
			state.Unavailable = true
			return state
		}
		state.InFlight += inFlight
		if channelID > 0 {
			state.ByTarget[uint64(channelID)] = stat
		}
	}
	rows.Close()
	if rows.Err() != nil {
		state.Unavailable = true
		return state
	}

	local := now.In(time.Local)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
	if err := s.db.QueryRow(ctx, `
select coalesce(sum(a.fee_paid_sat), 0)
from rebalance_attempts a
join rebalance_jobs j on j.id = a.job_id
where j.source='auto'
  and coalesce(j.trigger_reason, j.reason, '')=$1
  and j.created_at >= $2
  and a.status='succeeded'
`, sovereignDiscoveryReason, dayStart).Scan(&state.SpentTodaySat); err != nil {
		state.Unavailable = true
	}
	return state
}

// maybeQueueSovereignDiscovery turns a route-gated decision into one discovery
// probe when the target is drained for long enough and the ladder still has a
// step left today. It never competes with normal jobs for the cycle limit and
// spends only from discovery_daily_budget_sat. The probe is a separate job
// kind: its failure is not "all sources failed", so it feeds neither the
// target structural cooldown nor the source routeability quarantine.
func (s *RebalanceService) maybeQueueSovereignDiscovery(ctx context.Context, decision *RebalanceSovereignDecision, target rebalanceTarget, targetCfg RebalanceConfig, targetPolicy lndclient.ChannelPolicySnapshot, run *sovereignDiscoveryRun, now time.Time, live bool) bool {
	if decision == nil || run == nil || decision.Selected || !isSovereignRouteGateReason(decision.Reason) {
		return false
	}
	if !sovereignDiscoveryConfigured(targetCfg) || run.queued >= sovereignDiscoveryMaxPerCycle {
		return false
	}
	if !sovereignDiscoveryDrained(target, targetCfg, now) {
		return false
	}
	// The fast path refuses peers with parallel channels (it cannot pin the
	// incoming channel), so the probe would only be skipped and burn a step.
	if target.PeerHasParallelChannels {
		return false
	}
	amount := sovereignDiscoveryAmount(targetCfg, target.Channel)
	if amount <= 0 {
		return false
	}
	if !run.loaded {
		run.state = s.loadSovereignDiscoveryState(ctx, now)
		run.loaded = true
	}
	if run.state.Unavailable || run.state.InFlight > 0 {
		return false
	}
	step, ok := sovereignDiscoveryNextStep(targetCfg, run.state.ByTarget[target.Channel.ChannelID])
	if !ok {
		return false
	}
	baseFeeMsat, err := calcFeeLimitMsat(amount*1000, targetPolicy, nil, targetCfg)
	if err != nil || baseFeeMsat <= 0 {
		return false
	}
	feeCapPpm := sovereignDiscoveryFeeCapPpm(feeMsatToPpm(baseFeeMsat, amount), sovereignDiscoveryStepPct(targetCfg, step))
	costSat := sovereignDiscoveryCostSat(amount, feeCapPpm)
	if feeCapPpm <= 0 || costSat <= 0 {
		return false
	}
	if run.state.SpentTodaySat+run.reservedSat+costSat > targetCfg.DiscoveryDailyBudgetSat {
		return false
	}
	if s.isChannelBusy(target.Channel.ChannelID) {
		return false
	}
	if live {
		overrides := operatorRebalanceOverrides{AmountSet: true, FeeLimitSet: true, FeeLimitPpm: feeCapPpm}
		if _, err := s.startJobWithEconomics(target.Channel.ChannelID, "auto", sovereignDiscoveryReason, amount, false, false, rebalanceJobEconomics{}, overrides); err != nil {
			return false
		}
		decision.Reason = sovereignDiscoveryQueuedReason
	} else {
		decision.Reason = sovereignDiscoveryShadowReason
	}
	decision.Discovery = true
	decision.DiscoveryStep = step
	decision.DiscoveryFeeCapPpm = feeCapPpm
	decision.AmountSat = amount
	decision.BudgetCostSat = costSat
	run.queued++
	run.reservedSat += costSat
	return true
}

// finishDiscoveryWithoutRoute closes a discovery job the fast path did not
// settle. Discovery never falls back to the per-source legacy loop: walking
// every source against a target nothing reaches is what quarantines sources.
func (r *rebalanceJobRunner) finishDiscoveryWithoutRoute(st *rebalanceJobRunState) {
	if st != nil && st.fastPathAttempted {
		r.service.finishJob(r.jobID, "failed", sovereignDiscoveryNoRouteReason(r.overrides.FeeLimitPpm))
		return
	}
	r.service.finishJob(r.jobID, "skipped", sovereignDiscoveryUnavailableJobReason)
}
