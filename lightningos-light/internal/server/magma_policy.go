package server

import (
	"context"
	"fmt"
	"strings"
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
}

// evaluateMagmaOrder splits refusals into permanent and temporary on purpose.
// Rejecting a good order because the mempool spiked would burn a sale that would
// have been fine an hour later; leaving a structurally bad order pending would
// earn a failure record. Neither mistake is recoverable, so the distinction is
// the core of this function.
func evaluateMagmaOrder(policy MagmaPolicy, in magmaPolicyInputs) magmaDecision {
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

	// --- Temporary: the terms are fine, we are not ready. Defer, never reject. ---
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
	err := s.db.QueryRow(ctx, `
select count(*), coalesce(sum(size_sat),0)
from magma_orders
where local_state <> $1 and local_state <> $2
  and updated_at >= date_trunc('day', now())
`, magmaStateObserved, magmaStateRejected).Scan(&count, &size)
	return count, size, err
}

func (s *MagmaService) concurrentOpens(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRow(ctx,
		`select count(*) from magma_orders where local_state = any($1)`,
		magmaCommittedStates).Scan(&count)
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
	if _, err := s.OpenChannelForOrder(ctx, orderID, MagmaOpenRequest{SatPerVbyte: preview.SatPerVbyte}); err != nil {
		if s.logger != nil {
			s.logger.Printf("magma: auto open failed for %s: %v", orderID, err)
		}
	}
	return true
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
		}
		if previewErr == nil {
			inputs.SatPerVbyte = preview.SatPerVbyte
			inputs.EstimatedFeeSat = preview.EstimatedFeeSat
		}

		decision := evaluateMagmaOrder(policy, inputs)
		switch {
		case decision.Accept:
			if _, err := s.AcceptOrder(ctx, orderID); err != nil {
				s.appendEvent(ctx, orderID, "auto_error", "warning",
					fmt.Sprintf("auto accept failed: %v", err), nil)
				return
			}
			s.appendEvent(ctx, orderID, "auto_accepted", "info",
				"auto mode accepted: "+decision.Reason, nil)
			// One acceptance per pass keeps a bad policy from committing the whole
			// wallet before the operator sees the first result.
			return
		case decision.Reject && policy.AutoRejectDeclined:
			if _, err := s.RejectOrder(ctx, orderID); err != nil {
				s.appendEvent(ctx, orderID, "auto_error", "warning",
					fmt.Sprintf("auto reject failed: %v", err), nil)
				continue
			}
			s.appendEvent(ctx, orderID, "auto_rejected", "info",
				"auto mode rejected: "+decision.Reason, nil)
			s.notifyTelegram(ctx, order, fmt.Sprintf(
				"Order %s rejected automatically: %s", orderID, decision.Reason))
		default:
			s.recordDeferral(ctx, orderID, decision.Reason)
		}
	}
}

// recordDeferral logs a wait only when the reason changed. A 90s poll would
// otherwise write the same "waiting for funds" line forever.
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

// magmaPolicySummary is a short human description used in the UI and in the
// confirmation shown when auto mode is switched on.
func magmaPolicySummary(policy MagmaPolicy) string {
	return fmt.Sprintf(
		"accepts %s-%s sat channels, at least %d ppm and %d ppm/day, "+
			"on-chain cost up to %d%% of the sale, max %d orders and %s sat per day",
		formatInt(policy.MinChannelSizeSat), formatInt(policy.MaxChannelSizeSat),
		policy.MinPricePPM, policy.MinPricePPMPerDay, policy.MaxOnchainCostPct,
		policy.MaxDailyOrders, formatInt(policy.MaxDailySizeSat))
}
