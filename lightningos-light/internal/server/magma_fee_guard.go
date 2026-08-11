package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lightningos-light/internal/lndclient"
)

// A sold channel carries a contractual fee ceiling until its commitment ends,
// and Amboss measures every second spent above it in fee_above_cap_seconds.
//
// The channel stays inside Autofee and is held under that ceiling by a clamp in
// applyChannelFeesWithRetry, the single point where a fee reaches LND. An earlier
// version removed the channel from Autofee instead; that honoured the contract
// but froze the fee at whatever it was on the day of the open, forfeiting the
// routing income the buyer paid to unlock. Clamping keeps both: Autofee prices
// freely underneath the ceiling, and the ceiling is never crossed.
//
// What remains here is the one-shot correction at confirmation time, for the case
// where the node's own defaults already breach the caps before Autofee ever runs.

// magmaFeeGuardLND is the extra LND surface the guard needs.
type magmaFeeGuardLND interface {
	GetChannelPolicy(ctx context.Context, channelPoint string) (lndclient.ChannelPolicy, error)
	UpdateChannelPolicy(ctx context.Context, params lndclient.UpdateChannelPolicyParams) error
}

// applyFeeGuard runs once a sale is confirmed. Failures are reported but never
// abort the sale: the channel is already funded and confirmed by this point, so a
// guard problem is an alert, not a rollback.
func (s *MagmaService) applyFeeGuard(ctx context.Context, orderID string) {
	if s.lnd == nil {
		return
	}
	var channelPoint string
	var feeRateCapPPM, baseFeeCapSat int64
	if err := s.db.QueryRow(ctx, `
select channel_point, fee_rate_cap_ppm, base_fee_cap_sat from magma_orders where order_id=$1
`, orderID).Scan(&channelPoint, &feeRateCapPPM, &baseFeeCapSat); err != nil {
		return
	}
	channelPoint = strings.TrimSpace(channelPoint)
	if channelPoint == "" {
		return
	}

	channelID, err := s.resolveChannelID(ctx, channelPoint)
	if err != nil || channelID == 0 {
		// Normal right after the open: the channel is still pending and has no
		// short channel id yet. The next poll retries.
		return
	}

	changed, detail, err := s.enforceFeeCaps(ctx, channelPoint, feeRateCapPPM, baseFeeCapSat)
	if err != nil {
		s.appendEvent(ctx, orderID, "fee_guard", "warning",
			fmt.Sprintf("could not verify the fee caps on %s: %v", channelPoint, err), nil)
		return
	}
	message := fmt.Sprintf(
		"Channel %s is under commitment: Autofee may price it, capped at %d ppm / %d sat base",
		channelPoint, feeRateCapPPM, baseFeeCapSat)
	if changed {
		message += "; " + detail
	}
	s.appendEvent(ctx, orderID, "fee_guard", "info", message, map[string]any{
		"channel_id":       channelID,
		"fee_rate_cap_ppm": feeRateCapPPM,
		"base_fee_cap_sat": baseFeeCapSat,
	})
	if _, err := s.db.Exec(ctx,
		`update magma_orders set fee_guard_applied=true, updated_at=now() where order_id=$1`,
		orderID); err != nil && s.logger != nil {
		s.logger.Printf("magma: failed to record fee guard for order %s: %v", orderID, err)
	}
}

// releaseFeeGuard hands the channel back to Autofee once Amboss reports the
// commitment finished.
func (s *MagmaService) releaseFeeGuard(ctx context.Context, orderID string) {
	var channelPoint string
	if err := s.db.QueryRow(ctx,
		`select channel_point from magma_orders where order_id=$1`, orderID).Scan(&channelPoint); err != nil {
		return
	}
	channelPoint = strings.TrimSpace(channelPoint)
	if channelPoint == "" || s.lnd == nil {
		return
	}
	channelID, err := s.resolveChannelID(ctx, channelPoint)
	if err != nil || channelID == 0 {
		return
	}
	// Nothing to hand back: the channel never left Autofee. The clamp simply stops
	// applying once the commitment leaves the active set.
	_ = channelID
	s.appendEvent(ctx, orderID, "fee_guard", "info", fmt.Sprintf(
		"Commitment finished; the fee ceiling no longer applies to channel %s", channelPoint), nil)
	if _, err := s.db.Exec(ctx,
		`update magma_orders set fee_guard_applied=false, updated_at=now() where order_id=$1`,
		orderID); err != nil && s.logger != nil {
		s.logger.Printf("magma: failed to clear fee guard for order %s: %v", orderID, err)
	}
}

// ReapplyFeeCaps pulls every channel still under commitment back under its
// ceiling. It exists for the node-wide fee edit, which writes all channels in a
// single LND call and so cannot skip the sold ones: the fee lands everywhere and
// is walked back here, seconds later, rather than blocking an operation that is
// legitimate for the other ninety channels.
func (s *MagmaService) ReapplyFeeCaps(ctx context.Context) {
	if s.lnd == nil {
		return
	}
	commitments, err := s.ActiveCommitments(ctx)
	if err != nil {
		return
	}
	for _, commitment := range commitments {
		changed, detail, err := s.enforceFeeCaps(ctx, commitment.ChannelPoint,
			commitment.FeeRateCapPPM, commitment.BaseFeeCapSat)
		if err != nil {
			if s.logger != nil {
				s.logger.Printf("magma: could not restore the fee ceiling on %s: %v",
					commitment.ChannelPoint, err)
			}
			continue
		}
		if changed {
			s.appendEvent(ctx, commitment.OrderID, "fee_guard", "info", fmt.Sprintf(
				"A node-wide fee change crossed this channel's ceiling; %s", detail), nil)
		}
	}
}

// enforceFeeCaps lowers the outbound fee when the node defaults already breach the
// order's ceilings.
//
// With the shipped lnd.conf (feerate 1 ppm, basefee 1000 msat) the rate never
// trips, but base fee does: locked_base_fee_cap is 0 on more than half of real
// orders, against a 1 sat default. The rate check still matters because lnd.conf
// is editable from the UI.
func (s *MagmaService) enforceFeeCaps(ctx context.Context, channelPoint string, feeRateCapPPM, baseFeeCapSat int64) (bool, string, error) {
	guard, ok := s.lnd.(magmaFeeGuardLND)
	if !ok {
		return false, "", errors.New("LND client does not expose channel policy control")
	}
	policy, err := guard.GetChannelPolicy(ctx, channelPoint)
	if err != nil {
		return false, "", err
	}

	targetRate := policy.FeeRatePpm
	targetBaseMsat := policy.BaseFeeMsat
	changes := make([]string, 0, 2)

	if feeRateCapPPM > 0 && targetRate > feeRateCapPPM {
		// Sit just under the ceiling rather than exactly on it, so rounding on the
		// Amboss side cannot read the channel as non-compliant.
		lowered := magmaFeeUnderCap(feeRateCapPPM)
		changes = append(changes, fmt.Sprintf("fee rate %d ppm lowered to %d ppm (cap %d)",
			targetRate, lowered, feeRateCapPPM))
		targetRate = lowered
	}
	capBaseMsat := baseFeeCapSat * 1000
	if targetBaseMsat > capBaseMsat {
		changes = append(changes, fmt.Sprintf("base fee %d msat lowered to %d msat (cap %d sat)",
			targetBaseMsat, capBaseMsat, baseFeeCapSat))
		targetBaseMsat = capBaseMsat
	}
	if len(changes) == 0 {
		return false, "", nil
	}

	if err := guard.UpdateChannelPolicy(ctx, lndclient.UpdateChannelPolicyParams{
		ChannelPoint:      channelPoint,
		ApplyAll:          false,
		BaseFeeMsat:       targetBaseMsat,
		FeeRatePpm:        targetRate,
		TimeLockDelta:     policy.TimeLockDelta,
		InboundEnabled:    true,
		InboundBaseMsat:   policy.InboundBaseMsat,
		InboundFeeRatePpm: policy.InboundFeeRatePpm,
	}); err != nil {
		return false, "", err
	}
	return true, strings.Join(changes, "; "), nil
}

// magmaFeeUnderCap keeps a small margin below the contractual ceiling.
func magmaFeeUnderCap(cap int64) int64 {
	if cap <= 1 {
		return 0
	}
	margin := cap / 100
	if margin < 1 {
		margin = 1
	}
	return cap - margin
}

func (s *MagmaService) resolveChannelID(ctx context.Context, channelPoint string) (uint64, error) {
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return 0, err
	}
	for _, channel := range channels {
		if strings.EqualFold(strings.TrimSpace(channel.ChannelPoint), channelPoint) {
			return channel.ChannelID, nil
		}
	}
	return 0, nil
}

// syncFeeGuards is driven by the poller. Applying and releasing are both retried
// from persisted state rather than done once inline, because right after the open
// the channel has no short channel id yet and the guard cannot be applied.
func (s *MagmaService) syncFeeGuards(ctx context.Context) {
	rows, err := s.db.Query(ctx, `
select order_id, magma_status, fee_guard_applied
from magma_orders
where channel_point <> '' and local_state in ($1,$2)
`, magmaStateConfirming, magmaStateConfirmed)
	if err != nil {
		return
	}
	type guardRow struct {
		orderID string
		status  string
		applied bool
	}
	guardRows := make([]guardRow, 0, 8)
	for rows.Next() {
		var row guardRow
		if err := rows.Scan(&row.orderID, &row.status, &row.applied); err != nil {
			continue
		}
		guardRows = append(guardRows, row)
	}
	rows.Close()

	for _, row := range guardRows {
		finished := row.status == "CHANNEL_MONITORING_FINISHED"
		switch {
		case finished && row.applied:
			s.releaseFeeGuard(ctx, row.orderID)
		case !finished && !row.applied:
			s.applyFeeGuard(ctx, row.orderID)
		}
	}
}
