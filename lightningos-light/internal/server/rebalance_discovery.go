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
	// sovereignDiscoveryBackoffReason is counted in the scan's skip reasons
	// when a target's ladder is skipped because of the no-route backoff.
	sovereignDiscoveryBackoffReason = "discovery_backoff"
	sovereignDiscoveryShadowReason  = "would_discover"
	// One probe per scan and one in flight at a time: each one is a broad
	// pathfinding call, and a burst of them is what saturates LND.
	sovereignDiscoveryMaxPerCycle = 1
	// One ladder per channel per window, whatever its outcome. The window is
	// anchored at the ladder's first probe, not sliding: a sliding count let
	// the first probe of yesterday's ladder expire while the second was still
	// counted, so every later ladder skipped step 1 and probed twice at the
	// ceiling (Ripio 900/900 ppm on 2026-10-22, 1263/1263 on 10-23).
	sovereignDiscoveryWindow = 24 * time.Hour
	// History read to find where the current ladder started and how many
	// ladders before it ended without a route.
	sovereignDiscoveryHistory = 7 * sovereignDiscoveryWindow
	// A target whose last ladders all climbed to the ceiling without a route
	// is not "unprobed", it is unreachable for now. After this many such
	// ladders in a row the next one waits 2 windows, then 4, capped at
	// sovereignDiscoveryBackoffMaxWindows (two Friendspool targets re-ran the
	// same two no-route steps every day from 2026-11-03 to 11-09, taking the
	// one-probe-per-scan slot and the budget reservation from targets that
	// did have a route).
	sovereignDiscoveryBackoffAfterLadders = 2
	sovereignDiscoveryBackoffMaxWindows   = 4

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
	// Expired holds the target's finished ladders, oldest first, for the
	// no-route backoff. Empty when the target was never probed in the history.
	Expired []sovereignDiscoveryLadder
}

// sovereignDiscoveryLadder summarises one finished ladder.
type sovereignDiscoveryLadder struct {
	Start   time.Time
	Jobs    int
	NoRoute bool // every probe failed for lack of a route: no success, no skip, none in flight
}

type sovereignDiscoveryJob struct {
	At        time.Time
	Succeeded bool
	NoRoute   bool
}

// sovereignDiscoveryCurrentLadder groups a target's discovery jobs (oldest
// first) into ladders: a job at least one window after the start of the
// running ladder opens a new one. It returns the steps of the ladder that is
// still open at now, or an empty stat when the last ladder has expired.
func sovereignDiscoveryCurrentLadder(jobs []sovereignDiscoveryJob, now time.Time) sovereignDiscoveryTargetStat {
	var start time.Time
	var stat sovereignDiscoveryTargetStat
	var expired []sovereignDiscoveryLadder
	ladder := sovereignDiscoveryLadder{NoRoute: true}
	for _, job := range jobs {
		if start.IsZero() || !job.At.Before(start.Add(sovereignDiscoveryWindow)) {
			if !start.IsZero() {
				expired = append(expired, ladder)
			}
			start = job.At
			stat = sovereignDiscoveryTargetStat{}
			ladder = sovereignDiscoveryLadder{Start: job.At, NoRoute: true}
		}
		stat.Jobs++
		stat.Succeeded = stat.Succeeded || job.Succeeded
		stat.LastAt = job.At
		ladder.Jobs++
		ladder.NoRoute = ladder.NoRoute && job.NoRoute && !job.Succeeded
	}
	if start.IsZero() {
		return sovereignDiscoveryTargetStat{}
	}
	if !now.Before(start.Add(sovereignDiscoveryWindow)) {
		return sovereignDiscoveryTargetStat{Expired: append(expired, ladder)}
	}
	stat.Expired = expired
	return stat
}

// sovereignDiscoveryBackoffUntil returns when a target whose recent ladders
// all ran out of steps without a route may open a new ladder. It counts the
// finished ladders from the newest backwards while each probed every step of
// the current configuration and every probe failed for lack of a route; a
// success, a skipped probe or a ladder cut short (budget) ends the streak.
// Below sovereignDiscoveryBackoffAfterLadders there is no backoff.
func sovereignDiscoveryBackoffUntil(cfg RebalanceConfig, stat sovereignDiscoveryTargetStat) (time.Time, bool) {
	if cfg.DiscoverySteps <= 0 || len(stat.Expired) == 0 {
		return time.Time{}, false
	}
	streak := 0
	for i := len(stat.Expired) - 1; i >= 0; i-- {
		ladder := stat.Expired[i]
		if !ladder.NoRoute || ladder.Jobs < cfg.DiscoverySteps {
			break
		}
		streak++
	}
	if streak < sovereignDiscoveryBackoffAfterLadders {
		return time.Time{}, false
	}
	windows := 1 << (streak - 1)
	if windows > sovereignDiscoveryBackoffMaxWindows {
		windows = sovereignDiscoveryBackoffMaxWindows
	}
	last := stat.Expired[len(stat.Expired)-1]
	return last.Start.Add(time.Duration(windows) * sovereignDiscoveryWindow), true
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
// discovery job of the current ladder is one spent step. It stops at the
// first success and after the last step, until the ladder's window expires.
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
	if intent == nil || intent.Kind != automationIntentKindRefillTarget || !isSovereignDiscoveryIntentReason(intent.ReasonCode) {
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

// isSovereignDiscoveryIntentReason: the ladder measures a price for a channel
// that is not refilling. Keepalive only acts on drained/extreme-drained; the
// ladder also takes low_outbound (PurpleWisteria at 6% local, Unwetter at 7%
// on 2026-10-14 sat in source failures all day with that intent and never
// got a probe).
func isSovereignDiscoveryIntentReason(reason string) bool {
	if isKeepaliveRefillIntentReason(reason) {
		return true
	}
	return strings.TrimSpace(reason) == "autofee_low_outbound_target"
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
  created_at,
  status in ('succeeded','partial') as succeeded,
  status in ('queued','running') as in_flight,
  status = 'failed' as no_route
from rebalance_jobs
where source='auto'
  and coalesce(trigger_reason, reason, '')=$1
  and created_at >= $2
order by target_channel_id, created_at
`, sovereignDiscoveryReason, now.Add(-sovereignDiscoveryHistory))
	if err != nil {
		state.Unavailable = true
		return state
	}
	jobsByTarget := map[uint64][]sovereignDiscoveryJob{}
	for rows.Next() {
		var channelID int64
		var job sovereignDiscoveryJob
		var inFlight bool
		if err := rows.Scan(&channelID, &job.At, &job.Succeeded, &inFlight, &job.NoRoute); err != nil {
			rows.Close()
			state.Unavailable = true
			return state
		}
		if inFlight {
			state.InFlight++
		}
		if channelID > 0 {
			jobsByTarget[uint64(channelID)] = append(jobsByTarget[uint64(channelID)], job)
		}
	}
	rows.Close()
	if rows.Err() != nil {
		state.Unavailable = true
		return state
	}
	for channelID, jobs := range jobsByTarget {
		if stat := sovereignDiscoveryCurrentLadder(jobs, now); stat.Jobs > 0 || len(stat.Expired) > 0 {
			state.ByTarget[channelID] = stat
		}
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
	stat := run.state.ByTarget[target.Channel.ChannelID]
	step, ok := sovereignDiscoveryNextStep(targetCfg, stat)
	if !ok {
		return false
	}
	if until, backoff := sovereignDiscoveryBackoffUntil(targetCfg, stat); backoff && now.Before(until) {
		decision.DiscoveryBackoffUntil = until
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
