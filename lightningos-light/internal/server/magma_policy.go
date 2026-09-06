package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The policy engine decides, without a human, whether an order is worth taking.
// Every decision is explicit and carries a reason, because "nothing happened" is
// the hardest failure to debug in an automated seller.

// MagmaPolicy is the auto-mode configuration. Defaults are anchored on the real
// order distribution observed on a live seller account rather than invented:
// prices ran 1500-11000 ppm, sizes 1M-6M sats, and 180-day commitments were the
// most common by a wide margin.
type MagmaPolicy struct {
	MinRevenueSat      int64 `json:"min_revenue_sat"`
	MinPricePPM        int64 `json:"min_price_ppm"`
	MinPricePPMPerDay  int64 `json:"min_price_ppm_per_day"`
	MinFeeRateCapPPM   int64 `json:"min_fee_rate_cap_ppm"`
	MinChannelSizeSat  int64 `json:"min_channel_size_sat"`
	MaxChannelSizeSat  int64 `json:"max_channel_size_sat"`
	MaxCommitmentDays  int64 `json:"max_commitment_days"`
	MaxSatPerVbyte     int64 `json:"max_sat_per_vbyte"`
	MaxOnchainCostPct  int64 `json:"max_onchain_cost_pct"`
	MinOnchainReserve  int64 `json:"min_onchain_reserve_sat"`
	MaxConcurrentOpens int   `json:"max_concurrent_opens"`
	MaxDailyOrders     int   `json:"max_daily_orders"`
	MaxDailySizeSat    int64 `json:"max_daily_size_sat"`
	AutoRejectDeclined bool  `json:"auto_reject_declined"`
}

func defaultMagmaPolicy() MagmaPolicy {
	return MagmaPolicy{
		MinRevenueSat:     5000,
		MinPricePPM:       2000,
		MinPricePPMPerDay: 25,
		// Below this ceiling the channel is barely worth routing through once it
		// is open, which is invisible if only the sale price is considered.
		MinFeeRateCapPPM:  100,
		MinChannelSizeSat: 1_000_000,
		MaxChannelSizeSat: 10_000_000,
		MaxCommitmentDays: 180,
		MaxSatPerVbyte:    50,
		// On-chain cost comes straight out of the sale, so half is already
		// generous. The production bot only refused at 100%.
		MaxOnchainCostPct:  50,
		MinOnchainReserve:  100_000,
		MaxConcurrentOpens: 2,
		MaxDailyOrders:     5,
		MaxDailySizeSat:    20_000_000,
		// Declining explicitly avoids SELLER_FAILED_TO_REACT, which Amboss keeps
		// on the account. Silence is not free.
		AutoRejectDeclined: true,
	}
}

// Amboss gives the seller a limited window to answer, then records
// SELLER_FAILED_TO_REACT against the account. The one order that ever expired
// here was marked 2h04m after creation, so the window is about two hours.
//
// magmaApprovalGrace is deliberately well short of that. An explicit refusal
// costs one sale; silence costs the same sale AND leaves a permanent failure on
// the seller record, which is what buyers look at when choosing an offer.
const (
	magmaApprovalWindow = 2 * time.Hour
	magmaApprovalGrace  = 80 * time.Minute
)

// magmaWindowRemaining is how long Amboss is still expected to wait. It floors at
// zero because a pass running after the window already closed would otherwise
// announce a negative number of minutes left.
func magmaWindowRemaining(pendingFor time.Duration) time.Duration {
	if remaining := magmaApprovalWindow - pendingFor; remaining > 0 {
		return remaining
	}
	return 0
}

// magmaDecision is the outcome of evaluating one order.
type magmaDecision struct {
	Accept bool
	// Reject means "tell Amboss no". Deferring instead leaves the order alone so
	// a transient condition (a fee spike, a busy wallet) can clear on its own.
	Reject bool
	Reason string
}

// magmaPolicyInputs is everything the decision depends on, gathered up front so
// evaluation stays a pure function and is testable without a node or an API.
type magmaPolicyInputs struct {
	Order            MagmaOrder
	AvailableSat     int64
	ConcurrentOpens  int
	OrdersToday      int
	SizeToday        int64
	SatPerVbyte      int64
	EstimatedFeeSat  int64
	OnchainReachable bool
	// PendingFor is how long Amboss has had this order waiting on us. Zero when
	// the creation time is unknown, which disables the deadline rather than
	// guessing an age and refusing something that just arrived.
	PendingFor time.Duration
	// Deadline is Amboss's own instant for this order, when they publish one.
	// Everything below used to run on an estimate: the grace period was derived
	// from a single order that lapsed 2h04m after creation. A real deadline
	// replaces the guess; a missing one keeps it.
	Deadline *time.Time
	// BuyerUnreachable is set when the buyer's node did not answer. Phrased so the
	// zero value is the harmless one: a caller that never learned the answer must
	// not silently block every sale.
	//
	// It defers rather than refuses. An unreachable buyer has been observed paying
	// in full, so it predicts nothing on its own - but LND will not open a channel
	// to a peer that is not connected, so accepting while it is down promises a
	// delivery on a connection we do not have. Deferring costs nothing while there
	// is still window left, and the deadline turns it into a refusal if it lasts.
	BuyerUnreachable bool
}

// magmaTimeLeft is how long this order still has. It prefers Amboss's own
// deadline and falls back to the observed window, so an order is never judged by
// an estimate when the real number is available.
func magmaTimeLeft(in magmaPolicyInputs, now time.Time) (time.Duration, bool) {
	if in.Deadline != nil {
		return in.Deadline.Sub(now), true
	}
	if in.PendingFor <= 0 {
		// Age unknown, so no deadline can be applied without inventing one.
		return 0, false
	}
	return magmaApprovalWindow - in.PendingFor, true
}

// magmaShouldRefuseNow reports whether the window has closed far enough that an
// explicit refusal is the only thing still worth doing.
//
// The order matters: refusing early throws away a sale that might still land,
// and refusing late is not refusing at all - the lapse is recorded against the
// account as SELLER_FAILED_TO_REACT. So the refusal happens with time to spare,
// deliberately before the deadline rather than at it.
func magmaShouldRefuseNow(in magmaPolicyInputs, now time.Time) bool {
	left, known := magmaTimeLeft(in, now)
	if !known {
		return false
	}
	return left <= magmaApprovalWindow-magmaApprovalGrace
}

// evaluateMagmaOrder splits refusals into permanent and temporary on purpose.
// Rejecting a good order because the mempool spiked would burn a sale that would
// have been fine an hour later; leaving a structurally bad order pending would
// earn a failure record. Neither mistake is recoverable, so the distinction is
// the core of this function.
func evaluateMagmaOrder(policy MagmaPolicy, in magmaPolicyInputs) magmaDecision {
	decision := evaluateMagmaOrderTerms(policy, in)
	// A deferral is a bet that the blocker clears before Amboss loses patience.
	// Past the grace period that bet is lost, and the only thing left to choose is
	// whether the order ends as our explicit "no" or as a failure record.
	if !decision.Accept && !decision.Reject && magmaShouldRefuseNow(in, time.Now()) {
		left, _ := magmaTimeLeft(in, time.Now())
		if left < 0 {
			left = 0
		}
		return magmaDecision{Reject: true, Reason: fmt.Sprintf(
			"%s - refusing explicitly, the approval window closes in about %d minutes",
			decision.Reason, int(left/time.Minute))}
	}
	return decision
}

// evaluateMagmaOrderTerms judges the order on its merits alone, with no notion of
// how long it has been waiting.
func evaluateMagmaOrderTerms(policy MagmaPolicy, in magmaPolicyInputs) magmaDecision {
	order := in.Order

	// --- Permanent: the terms themselves are unacceptable. Reject outright. ---
	if policy.MinChannelSizeSat > 0 && order.SizeSat < policy.MinChannelSizeSat {
		return magmaDecision{Reject: true, Reason: fmt.Sprintf(
			"channel size %s sat is below the %s sat minimum",
			formatInt(order.SizeSat), formatInt(policy.MinChannelSizeSat))}
	}
	if policy.MaxChannelSizeSat > 0 && order.SizeSat > policy.MaxChannelSizeSat {
		return magmaDecision{Reject: true, Reason: fmt.Sprintf(
			"channel size %s sat is above the %s sat maximum",
			formatInt(order.SizeSat), formatInt(policy.MaxChannelSizeSat))}
	}
	if policy.MinRevenueSat > 0 && order.RevenueSat < policy.MinRevenueSat {
		return magmaDecision{Reject: true, Reason: fmt.Sprintf(
			"revenue %s sat is below the %s sat minimum",
			formatInt(order.RevenueSat), formatInt(policy.MinRevenueSat))}
	}
	if policy.MinPricePPM > 0 && order.PricePPM < policy.MinPricePPM {
		return magmaDecision{Reject: true, Reason: fmt.Sprintf(
			"price %d ppm is below the %d ppm minimum", order.PricePPM, policy.MinPricePPM)}
	}
	if policy.MaxCommitmentDays > 0 && order.CommitmentBlocks > policy.MaxCommitmentDays*144 {
		return magmaDecision{Reject: true, Reason: fmt.Sprintf(
			"commitment of %d days exceeds the %d day maximum",
			order.CommitmentBlocks/144, policy.MaxCommitmentDays)}
	}
	// Price per day is what separates a good 7-day deal from the same headline
	// price locked for six months.
	if policy.MinPricePPMPerDay > 0 && order.CommitmentBlocks > 0 {
		perDay := order.PricePerDayPPM()
		if perDay < float64(policy.MinPricePPMPerDay) {
			return magmaDecision{Reject: true, Reason: fmt.Sprintf(
				"price works out to %.1f ppm/day over %d days, below the %d ppm/day minimum",
				perDay, order.CommitmentBlocks/144, policy.MinPricePPMPerDay)}
		}
	}
	if policy.MinFeeRateCapPPM > 0 && order.FeeRateCapPPM > 0 && order.FeeRateCapPPM < policy.MinFeeRateCapPPM {
		return magmaDecision{Reject: true, Reason: fmt.Sprintf(
			"the order caps our routing fee at %d ppm, below the %d ppm minimum worth accepting",
			order.FeeRateCapPPM, policy.MinFeeRateCapPPM)}
	}
	// An order bigger than the entire daily allowance can never be accepted: the
	// cap resets at midnight, but the order still would not fit on an empty day.
	// Waiting for a moment that never arrives is how an order reaches the Amboss
	// timeout, so this is a permanent refusal, not a deferral. It also means the
	// policy contradicts itself whenever max_channel_size_sat is the larger of
	// the two, and the reason says so.
	if policy.MaxDailySizeSat > 0 && order.SizeSat > policy.MaxDailySizeSat {
		return magmaDecision{Reject: true, Reason: fmt.Sprintf(
			"channel size %s sat never fits the %s sat daily cap, not even on an empty day",
			formatInt(order.SizeSat), formatInt(policy.MaxDailySizeSat))}
	}

	// --- Temporary: the terms are fine, we are not ready. Defer, never reject. ---

	// Checked here and not earlier: an order whose terms are unacceptable is
	// refused on those terms whatever the peer is doing. Deferring it instead
	// would spend the window waiting for a node whose order we were never going
	// to take.
	if in.BuyerUnreachable {
		return magmaDecision{Reason: "the buyer's node is not answering, and LND cannot open " +
			"a channel to a peer that is not connected; waiting for it to return"}
	}
	if !in.OnchainReachable {
		return magmaDecision{Reason: "waiting: could not read the on-chain balance"}
	}
	needed := order.SizeSat + in.EstimatedFeeSat + policy.MinOnchainReserve
	if in.AvailableSat < needed {
		return magmaDecision{Reason: fmt.Sprintf(
			"waiting for funds: needs %s sat including reserve, %s sat free",
			formatInt(needed), formatInt(in.AvailableSat))}
	}
	if policy.MaxConcurrentOpens > 0 && in.ConcurrentOpens >= policy.MaxConcurrentOpens {
		return magmaDecision{Reason: fmt.Sprintf(
			"waiting: %d order(s) already awaiting a channel open, limit is %d",
			in.ConcurrentOpens, policy.MaxConcurrentOpens)}
	}
	if policy.MaxDailyOrders > 0 && in.OrdersToday >= policy.MaxDailyOrders {
		return magmaDecision{Reason: fmt.Sprintf(
			"waiting: daily cap of %d orders reached", policy.MaxDailyOrders)}
	}
	if policy.MaxDailySizeSat > 0 && in.SizeToday+order.SizeSat > policy.MaxDailySizeSat {
		return magmaDecision{Reason: fmt.Sprintf(
			"waiting: daily cap of %s sat would be exceeded", formatInt(policy.MaxDailySizeSat))}
	}
	if policy.MaxSatPerVbyte > 0 && in.SatPerVbyte > policy.MaxSatPerVbyte {
		return magmaDecision{Reason: fmt.Sprintf(
			"waiting for cheaper fees: mempool at %d sat/vB, ceiling is %d",
			in.SatPerVbyte, policy.MaxSatPerVbyte)}
	}
	if policy.MaxOnchainCostPct > 0 && order.RevenueSat > 0 && in.EstimatedFeeSat > 0 {
		share := in.EstimatedFeeSat * 100 / order.RevenueSat
		if share > policy.MaxOnchainCostPct {
			return magmaDecision{Reason: fmt.Sprintf(
				"waiting for cheaper fees: on-chain cost would eat %d%% of the sale, ceiling is %d%%",
				share, policy.MaxOnchainCostPct)}
		}
	}

	return magmaDecision{Accept: true, Reason: fmt.Sprintf(
		"%s sat for %s sat (%d ppm, %.1f ppm/day over %d days)",
		formatInt(order.SizeSat), formatInt(order.RevenueSat), order.PricePPM,
		order.PricePerDayPPM(), order.CommitmentBlocks/144)}
}

// loadPolicy reads the stored policy, falling back to defaults for a row that has
// never been written.
func (s *MagmaService) loadPolicy(ctx context.Context) (MagmaPolicy, error) {
	policy := defaultMagmaPolicy()
	err := s.db.QueryRow(ctx, `
select min_revenue_sat, min_price_ppm, min_price_ppm_per_day, min_fee_rate_cap_ppm,
       min_channel_size_sat, max_channel_size_sat, max_commitment_days, max_sat_per_vbyte,
       max_onchain_cost_pct, min_onchain_reserve_sat, max_concurrent_opens,
       max_daily_orders, max_daily_size_sat, auto_reject_declined
from magma_settings where id=1
`).Scan(
		&policy.MinRevenueSat, &policy.MinPricePPM, &policy.MinPricePPMPerDay,
		&policy.MinFeeRateCapPPM, &policy.MinChannelSizeSat, &policy.MaxChannelSizeSat,
		&policy.MaxCommitmentDays, &policy.MaxSatPerVbyte, &policy.MaxOnchainCostPct,
		&policy.MinOnchainReserve, &policy.MaxConcurrentOpens, &policy.MaxDailyOrders,
		&policy.MaxDailySizeSat, &policy.AutoRejectDeclined,
	)
	if err != nil {
		return defaultMagmaPolicy(), err
	}
	return policy, nil
}

// PolicyForUpdate returns the current policy so a partial update can be decoded
// on top of it.
func (s *MagmaService) PolicyForUpdate(ctx context.Context) (MagmaPolicy, error) {
	return s.loadPolicy(ctx)
}

func (s *MagmaService) UpdatePolicy(ctx context.Context, policy MagmaPolicy) (MagmaPolicy, error) {
	if policy.MinChannelSizeSat > 0 && policy.MaxChannelSizeSat > 0 &&
		policy.MinChannelSizeSat > policy.MaxChannelSizeSat {
		return MagmaPolicy{}, fmt.Errorf("min_channel_size_sat cannot exceed max_channel_size_sat")
	}
	if policy.MaxOnchainCostPct < 0 || policy.MaxOnchainCostPct > 100 {
		return MagmaPolicy{}, fmt.Errorf("max_onchain_cost_pct must be between 0 and 100")
	}
	if _, err := s.db.Exec(ctx, `
update magma_settings set
  min_revenue_sat=$1, min_price_ppm=$2, min_price_ppm_per_day=$3, min_fee_rate_cap_ppm=$4,
  min_channel_size_sat=$5, max_channel_size_sat=$6, max_commitment_days=$7,
  max_sat_per_vbyte=$8, max_onchain_cost_pct=$9, min_onchain_reserve_sat=$10,
  max_concurrent_opens=$11, max_daily_orders=$12, max_daily_size_sat=$13,
  auto_reject_declined=$14, updated_at=now()
where id=1
`,
		policy.MinRevenueSat, policy.MinPricePPM, policy.MinPricePPMPerDay, policy.MinFeeRateCapPPM,
		policy.MinChannelSizeSat, policy.MaxChannelSizeSat, policy.MaxCommitmentDays,
		policy.MaxSatPerVbyte, policy.MaxOnchainCostPct, policy.MinOnchainReserve,
		policy.MaxConcurrentOpens, policy.MaxDailyOrders, policy.MaxDailySizeSat,
		policy.AutoRejectDeclined,
	); err != nil {
		return MagmaPolicy{}, err
	}
	s.signal()
	return s.loadPolicy(ctx)
}

// magmaDailyUsage counts what auto mode has already committed today.
func (s *MagmaService) dailyUsage(ctx context.Context) (int, int64, error) {
	var count int
	var size int64
	// Counted from the append-only event trail, not from magma_orders.updated_at.
	// Every sync rewrites updated_at on every row, so a row-timestamp filter would
	// re-count yesterday's sales today — and keep re-counting them, until the
	// daily cap was permanently exhausted and auto mode stopped accepting anything.
	err := s.db.QueryRow(ctx, `
select count(*), coalesce(sum(o.size_sat), 0)
from magma_order_events e
join magma_orders o on o.order_id = e.order_id
where e.kind = 'accepted' and e.created_at >= date_trunc('day', now())
`).Scan(&count, &size)
	return count, size, err
}

func (s *MagmaService) concurrentOpens(ctx context.Context) (int, error) {
	var count int
	// This feeds MaxConcurrentOpens, so counting a dead order here does not just
	// misreport - it permanently spends one of the slots auto mode needs to work.
	err := s.db.QueryRow(ctx,
		`select count(*) from magma_orders
where local_state = any($1) and not (magma_status = any($2))`,
		magmaCommittedStates, magmaTerminalStatusList()).Scan(&count)
	return count, err
}

// runAutoMode drives orders end to end. It is intentionally conservative about
// what it will do in a single pass: one order per tick, so a systematic mistake
// costs one channel rather than the whole wallet before anyone notices.
func (s *MagmaService) runAutoMode(ctx context.Context, token string) {
	policy, err := s.loadPolicy(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("magma: auto mode could not load policy: %v", err)
		}
		return
	}
	if s.autoAdvanceOpen(ctx, token, policy) {
		return
	}
	s.autoEvaluatePending(ctx, token, policy)
}

// autoAdvanceOpen funds the oldest order whose buyer has already paid. Paid orders
// come first: the money is in, and the obligation is live.
func (s *MagmaService) autoAdvanceOpen(ctx context.Context, token string, policy MagmaPolicy) bool {
	var orderID string
	if err := s.db.QueryRow(ctx, `
select order_id from magma_orders
where local_state=$1 and magma_status='WAITING_FOR_CHANNEL_OPEN'
order by coalesce(order_created_at, first_seen_at) asc limit 1
`, magmaStateAccepted).Scan(&orderID); err != nil {
		return false
	}
	preview, err := s.OpenChannelPreview(ctx, orderID, 0)
	if err != nil {
		return false
	}
	if !preview.CanOpen {
		s.appendEvent(ctx, orderID, "auto_deferred", "info",
			"auto mode is waiting: "+strings.Join(preview.Blockers, "; "), nil)
		return false
	}
	if policy.MaxSatPerVbyte > 0 && preview.SatPerVbyte > policy.MaxSatPerVbyte {
		s.appendEvent(ctx, orderID, "auto_deferred", "info", fmt.Sprintf(
			"auto mode is waiting for cheaper fees: mempool at %d sat/vB, ceiling is %d",
			preview.SatPerVbyte, policy.MaxSatPerVbyte), nil)
		return false
	}
	if policy.MaxOnchainCostPct > 0 && preview.FeeShareOfRevenue > int(policy.MaxOnchainCostPct) {
		s.appendEvent(ctx, orderID, "auto_deferred", "info", fmt.Sprintf(
			"auto mode is waiting: on-chain cost would eat %d%% of the sale, ceiling is %d%%",
			preview.FeeShareOfRevenue, policy.MaxOnchainCostPct), nil)
		return false
	}
	if _, err := s.openChannelForOrderLocked(ctx, orderID, MagmaOpenRequest{SatPerVbyte: preview.SatPerVbyte}); err != nil {
		if s.logger != nil {
			s.logger.Printf("magma: auto open failed for %s: %v", orderID, err)
		}
	}
	return true
}

// orderDeadlines reads the deadlines Amboss published for these orders. A
// missing entry means no deadline is known, which leaves the estimate in place
// rather than inventing one.
func (s *MagmaService) orderDeadlines(ctx context.Context, orderIDs []string) map[string]time.Time {
	deadlines := make(map[string]time.Time, len(orderIDs))
	if len(orderIDs) == 0 {
		return deadlines
	}
	rows, err := s.db.Query(ctx,
		`select order_id, timeout_at from magma_orders where order_id = any($1) and timeout_at is not null`,
		orderIDs)
	if err != nil {
		return deadlines
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var when time.Time
		if err := rows.Scan(&id, &when); err == nil {
			deadlines[id] = when.UTC()
		}
	}
	return deadlines
}

// autoEvaluatePending runs the policy over orders waiting for our approval.
func (s *MagmaService) autoEvaluatePending(ctx context.Context, token string, policy MagmaPolicy) {
	rows, err := s.db.Query(ctx, `
select order_id from magma_orders
where local_state=$1 and magma_status='WAITING_FOR_SELLER_APPROVAL'
order by coalesce(order_created_at, first_seen_at) asc
`, magmaStateObserved)
	if err != nil {
		return
	}
	pending := make([]string, 0, 4)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			pending = append(pending, id)
		}
	}
	rows.Close()
	if len(pending) == 0 {
		return
	}

	capacity, capErr := s.Capacity(ctx)
	concurrent, _ := s.concurrentOpens(ctx)
	ordersToday, sizeToday, _ := s.dailyUsage(ctx)

	deadlines := s.orderDeadlines(ctx, pending)
	for _, orderID := range pending {
		order, err := s.orderByID(ctx, orderID)
		if err != nil {
			continue
		}
		preview, previewErr := s.OpenChannelPreview(ctx, orderID, 0)
		inputs := magmaPolicyInputs{
			Order:            order,
			AvailableSat:     capacity.AvailableSat,
			ConcurrentOpens:  concurrent,
			OrdersToday:      ordersToday,
			SizeToday:        sizeToday,
			OnchainReachable: capErr == nil,
			// Reaching the buyer is attempted once per pass. It is cheap when the
			// peer is already connected - a peer list, no dial - and when it is
			// not, this is exactly the retry that keeps the sale alive.
			BuyerUnreachable: s.reachBuyer(ctx, token, order.BuyerPubkey) != nil,
		}
		if when, ok := deadlines[orderID]; ok {
			inputs.Deadline = &when
		}
		if order.CreatedAt != nil {
			inputs.PendingFor = time.Since(*order.CreatedAt)
		}
		if previewErr == nil {
			inputs.SatPerVbyte = preview.SatPerVbyte
			inputs.EstimatedFeeSat = preview.EstimatedFeeSat
		}

		decision := evaluateMagmaOrder(policy, inputs)
		switch {
		case decision.Accept:
			if _, err := s.acceptOrderLocked(ctx, orderID); err != nil {
				s.appendEvent(ctx, orderID, "auto_error", "warning",
					fmt.Sprintf("auto accept failed: %v", err), nil)
				// Retrying is right while there is time: an invoice can be refused
				// for reasons that pass, on Amboss's side as often as ours. Past the
				// grace period it stops being a retry and becomes waiting for the
				// window to shut. The sale is lost either way by then, so take the
				// one outcome still available - silence costs the same sale AND a
				// SELLER_FAILED_TO_REACT that stays on the account.
				s.rejectAfterFailedAccept(ctx, order, inputs.PendingFor, policy, err)
				return
			}
			s.appendEvent(ctx, orderID, "auto_accepted", "info",
				"auto mode accepted: "+decision.Reason, nil)
			// One acceptance per pass keeps a bad policy from committing the whole
			// wallet before the operator sees the first result.
			return
		case decision.Reject && policy.AutoRejectDeclined:
			if _, err := s.rejectOrderLocked(ctx, orderID); err != nil {
				s.appendEvent(ctx, orderID, "auto_error", "warning",
					fmt.Sprintf("auto reject failed: %v", err), nil)
				continue
			}
			s.appendEvent(ctx, orderID, "auto_rejected", "info",
				"auto mode rejected: "+decision.Reason, nil)
			s.notifyTelegram(ctx, order, fmt.Sprintf(
				"Order %s rejected automatically: %s", orderID, decision.Reason))
		case decision.Reject:
			// Auto-rejection is switched off, so the order will be left to expire.
			// That is the operator's choice, but it is not free: say plainly that a
			// failure record is coming, instead of filing it as a routine wait.
			s.recordDeferral(ctx, orderID, "will not be accepted ("+decision.Reason+
				") and auto-reject is off, so Amboss will record a seller failure")
		default:
			s.recordDeferral(ctx, orderID, decision.Reason)
		}
	}
}

// warnExpiringApprovals is the monitor/assisted counterpart of the deadline in
// evaluateMagmaOrder. Those modes cannot answer on their own, so the only thing
// left is to tell the operator while there is still time to act. Auto mode does
// not need this: it refuses explicitly instead.
func (s *MagmaService) warnExpiringApprovals(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
select o.order_id
from magma_orders o
where o.magma_status = 'WAITING_FOR_SELLER_APPROVAL'
  and o.order_created_at is not null
  and now() - o.order_created_at >= $1
  and not exists (
    select 1 from magma_order_events e
    where e.order_id = o.order_id and e.kind = 'approval_expiring'
  )
`, magmaApprovalGrace)
	if err != nil {
		return
	}
	expiring := make([]string, 0, 2)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			expiring = append(expiring, id)
		}
	}
	rows.Close()

	for _, orderID := range expiring {
		order, err := s.orderByID(ctx, orderID)
		if err != nil {
			continue
		}
		message := fmt.Sprintf(
			"Order %s is still unanswered and the Amboss approval window closes in roughly %d minutes. "+
				"Accept or reject it now - letting it lapse records a seller failure on the account.",
			orderID, int((magmaApprovalWindow-magmaApprovalGrace)/time.Minute))
		s.appendEvent(ctx, orderID, "approval_expiring", "warning", message, nil)
		s.notifyTelegram(ctx, order, message)
	}
}

// recordDeferral logs a wait only when the reason changed. A 90s poll would
// otherwise write the same "waiting for funds" line forever.
// magmaAcceptFailureVerdict is what a failed accept means right now. The
// judgement is separated from the acting so it can be tested without a node, a
// database or Amboss - the same reason evaluateMagmaOrder is a pure function.
type magmaAcceptFailureVerdict int

const (
	// Keep trying: there is still time for the blocker to clear.
	magmaAcceptRetry magmaAcceptFailureVerdict = iota
	// Out of time and auto-rejection is on: refuse explicitly.
	magmaAcceptRefuse
	// Out of time but the operator turned auto-rejection off. Nothing to do but
	// say clearly that a seller failure is now coming.
	magmaAcceptWarnOnly
)

// classifyMagmaAcceptFailure decides between retrying and refusing. A zero
// pendingFor means the order's age is unknown, and that always retries: refusing
// on a guessed age would burn a sale that had only just arrived.
func classifyMagmaAcceptFailure(pendingFor time.Duration, autoReject bool) magmaAcceptFailureVerdict {
	if pendingFor < magmaApprovalGrace {
		return magmaAcceptRetry
	}
	if !autoReject {
		return magmaAcceptWarnOnly
	}
	return magmaAcceptRefuse
}

// rejectAfterFailedAccept turns a run of failed accepts into an explicit refusal
// once the approval window is nearly spent. It does nothing while there is still
// time to succeed, and nothing when the order's age is unknown - refusing on a
// guessed age would burn a sale that had only just arrived.
func (s *MagmaService) rejectAfterFailedAccept(
	ctx context.Context, order MagmaOrder, pendingFor time.Duration,
	policy MagmaPolicy, cause error,
) {
	verdict := classifyMagmaAcceptFailure(pendingFor, policy.AutoRejectDeclined)
	if verdict == magmaAcceptRetry {
		return
	}
	reason := fmt.Sprintf(
		"accepting keeps failing (%v) and the approval window closes in about %d minutes",
		cause, int(magmaWindowRemaining(pendingFor)/time.Minute))
	if verdict == magmaAcceptWarnOnly {
		// Auto-rejection is off, so the order is left to lapse. Say plainly that a
		// failure record is coming instead of filing it as a routine retry, which
		// is what kept the last one invisible until it was too late.
		s.recordDeferral(ctx, order.ID, reason+
			", but auto-reject is off, so Amboss will record a seller failure")
		return
	}
	if _, err := s.rejectOrderLocked(ctx, order.ID); err != nil {
		s.appendEvent(ctx, order.ID, "auto_error", "warning",
			fmt.Sprintf("auto reject after a failed accept also failed: %v", err), nil)
		return
	}
	s.appendEvent(ctx, order.ID, "auto_rejected", "info", "auto mode rejected: "+reason, nil)
	s.notifyTelegram(ctx, order, fmt.Sprintf(
		"Order %s rejected automatically: %s", order.ID, reason))
}

func (s *MagmaService) recordDeferral(ctx context.Context, orderID, reason string) {
	var lastReason string
	if err := s.db.QueryRow(ctx,
		`select last_error from magma_orders where order_id=$1`, orderID).Scan(&lastReason); err == nil {
		if lastReason == reason {
			return
		}
	}
	if _, err := s.db.Exec(ctx,
		`update magma_orders set last_error=$2, updated_at=now() where order_id=$1`,
		orderID, reason); err != nil {
		return
	}
	s.appendEvent(ctx, orderID, "auto_deferred", "info", reason, nil)
}

// magmaPolicyWarnings reports settings that quietly cancel each other out. These
// are not invalid - each limit is honoured exactly as written - but the
// combination refuses orders the operator almost certainly meant to accept, and
// that only becomes visible after a sale is already lost.
func magmaPolicyWarnings(policy MagmaPolicy) []string {
	warnings := make([]string, 0, 2)
	// The band between the daily cap and the per-channel maximum is dead: an order
	// in it passes the size gate and can never fit the day, so it is refused on
	// sight no matter how good the price is.
	if policy.MaxDailySizeSat > 0 && policy.MaxChannelSizeSat > policy.MaxDailySizeSat {
		warnings = append(warnings, fmt.Sprintf(
			"orders between %s and %s sat are refused on sight: they pass the channel size limit "+
				"but cannot fit the %s sat daily cap. Raise the daily cap to at least %s sat, "+
				"or lower the maximum channel size to match it.",
			formatInt(policy.MaxDailySizeSat+1), formatInt(policy.MaxChannelSizeSat),
			formatInt(policy.MaxDailySizeSat), formatInt(policy.MaxChannelSizeSat)))
	}
	if policy.MaxDailyOrders > 0 && policy.MaxConcurrentOpens > policy.MaxDailyOrders {
		warnings = append(warnings, fmt.Sprintf(
			"the concurrent open limit (%d) is above the daily order limit (%d), so it never applies",
			policy.MaxConcurrentOpens, policy.MaxDailyOrders))
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

// magmaPolicySummary is a short human description used in the UI and in the
// confirmation shown when auto mode is switched on.
func magmaPolicySummary(policy MagmaPolicy) string {
	// A zero means the limit is switched off, so printing the digit says the exact
	// opposite of what it does: "0 sat per day" reads as "sell nothing". Disabled
	// limits are left out of the sentence entirely, which also makes the ones that
	// are still active easy to spot.
	parts := make([]string, 0, 5)
	parts = append(parts, fmt.Sprintf("accepts %s-%s sat channels",
		formatInt(policy.MinChannelSizeSat), formatInt(policy.MaxChannelSizeSat)))

	price := make([]string, 0, 2)
	if policy.MinPricePPM > 0 {
		price = append(price, fmt.Sprintf("%d ppm", policy.MinPricePPM))
	}
	if policy.MinPricePPMPerDay > 0 {
		price = append(price, fmt.Sprintf("%d ppm/day", policy.MinPricePPMPerDay))
	}
	if len(price) > 0 {
		parts = append(parts, "at least "+strings.Join(price, " and "))
	}
	if policy.MinRevenueSat > 0 {
		parts = append(parts, fmt.Sprintf("at least %s sat of revenue", formatInt(policy.MinRevenueSat)))
	}
	if policy.MaxOnchainCostPct > 0 {
		parts = append(parts, fmt.Sprintf("on-chain cost up to %d%% of the sale", policy.MaxOnchainCostPct))
	}

	daily := make([]string, 0, 2)
	if policy.MaxDailyOrders > 0 {
		daily = append(daily, fmt.Sprintf("%d orders", policy.MaxDailyOrders))
	}
	if policy.MaxDailySizeSat > 0 {
		daily = append(daily, fmt.Sprintf("%s sat", formatInt(policy.MaxDailySizeSat)))
	}
	if len(daily) > 0 {
		parts = append(parts, "max "+strings.Join(daily, " and ")+" per day")
	} else {
		parts = append(parts, "no daily limit")
	}
	return strings.Join(parts, ", ")
}
