package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lightningos-light/internal/lndclient"
)

// A sold channel carries two obligations for the length of the commitment:
// it must stay reachable at a routing fee no higher than the caps frozen into the
// order (locked_fee_rate_cap / locked_base_fee_cap), and Amboss measures every
// second spent above them in fee_above_cap_seconds.
//
// The protection here is deliberately blunt: take the channel out of Autofee for
// the commitment window, and fix the birth fee once if the node defaults already
// breach the caps. Leaving it inside Autofee with a clamp would mean trusting
// every future Autofee change to keep honouring a contract it knows nothing about.

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

	if err := s.disableAutofeeForChannel(ctx, channelID, channelPoint); err != nil {
		s.appendEvent(ctx, orderID, "fee_guard", "warning",
			fmt.Sprintf("could not take channel %s out of Autofee: %v", channelPoint, err), nil)
		return
	}

	changed, detail, err := s.enforceFeeCaps(ctx, channelPoint, feeRateCapPPM, baseFeeCapSat)
	if err != nil {
		s.appendEvent(ctx, orderID, "fee_guard", "warning",
			fmt.Sprintf("could not verify the fee caps on %s: %v", channelPoint, err), nil)
		return
	}
	message := fmt.Sprintf("Channel %s removed from Autofee for the commitment window", channelPoint)
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
	// Delete rather than set enabled=true: removing the row restores whatever the
	// node-wide default is, instead of pinning an opinion this app should not hold
	// once the obligation is over.
	if _, err := s.db.Exec(ctx,
		`delete from autofee_channel_settings where channel_id=$1`, int64(channelID)); err != nil {
		if s.logger != nil {
			s.logger.Printf("magma: failed to release fee guard for %s: %v", channelPoint, err)
		}
		return
	}
	s.appendEvent(ctx, orderID, "fee_guard", "info", fmt.Sprintf(
		"Commitment finished; channel %s handed back to Autofee", channelPoint), nil)
	if _, err := s.db.Exec(ctx,
		`update magma_orders set fee_guard_applied=false, updated_at=now() where order_id=$1`,
		orderID); err != nil && s.logger != nil {
		s.logger.Printf("magma: failed to clear fee guard for order %s: %v", orderID, err)
	}
}

// disableAutofeeForChannel writes straight into autofee_channel_settings. A
// channel with no row there is treated as enabled by Autofee, so a freshly opened
// sale would be picked up on the next run unless an explicit row says otherwise.
func (s *MagmaService) disableAutofeeForChannel(ctx context.Context, channelID uint64, channelPoint string) error {
	_, err := s.db.Exec(ctx, `
insert into autofee_channel_settings (channel_id, channel_point, enabled, updated_at)
values ($1, $2, false, now())
on conflict (channel_id) do update set enabled=false, channel_point=excluded.channel_point, updated_at=now()
`, int64(channelID), channelPoint)
	return err
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
