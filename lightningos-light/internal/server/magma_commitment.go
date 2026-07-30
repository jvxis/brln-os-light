package server

import (
	"context"
	"strings"
	"sync"
	"time"
)

// A sold channel carries two obligations until Amboss reports the commitment
// finished: it must stay open, and its routing fee must not exceed the ceiling
// frozen into the order. Both are invisible from the channel list, which is
// where an operator decides to close a channel or raise a fee.
//
// This file is the read side of that: a cheap, cached lookup other parts of the
// app consult without needing to know anything about Magma.

// MagmaChannelCommitment describes one sold channel still under contract.
type MagmaChannelCommitment struct {
	OrderID       string `json:"order_id"`
	ChannelPoint  string `json:"channel_point"`
	ChannelSCID   string `json:"channel_scid,omitempty"`
	BuyerAlias    string `json:"buyer_alias,omitempty"`
	BuyerPubkey   string `json:"buyer_pubkey,omitempty"`
	SizeSat       int64  `json:"size_sat"`
	RevenueSat    int64  `json:"revenue_sat"`
	FeeRateCapPPM int64  `json:"fee_rate_cap_ppm"`
	BaseFeeCapSat int64  `json:"base_fee_cap_sat"`
	// BlocksRemaining is what Amboss reports; absent on orders it has not scored
	// yet, in which case only the total commitment is known.
	BlocksRemaining  *int64 `json:"blocks_remaining,omitempty"`
	CommitmentBlocks int64  `json:"commitment_blocks"`
	MagmaStatus      string `json:"magma_status"`
}

// magmaCommitmentCache keeps the lookup cheap enough to call from the autofee
// hot path, which runs over every channel on every round.
type magmaCommitmentCache struct {
	mu        sync.RWMutex
	byPoint   map[string]MagmaChannelCommitment
	byChannel map[uint64]MagmaChannelCommitment
	loadedAt  time.Time
}

const magmaCommitmentCacheTTL = 60 * time.Second

var magmaCommitments = &magmaCommitmentCache{}

// ActiveCommitments returns the sold channels still under contract, keyed by
// channel point. An order counts while Amboss has not marked it finished: the
// terminal statuses are the ones that release the obligation.
func (s *MagmaService) ActiveCommitments(ctx context.Context) ([]MagmaChannelCommitment, error) {
	terminal := make([]string, 0, len(magmaTerminalStatuses))
	for status := range magmaTerminalStatuses {
		terminal = append(terminal, status)
	}
	rows, err := s.db.Query(ctx, `
select order_id, channel_point, coalesce(channel_scid,''), coalesce(buyer_alias,''),
       buyer_pubkey, size_sat, revenue_sat, fee_rate_cap_ppm, base_fee_cap_sat,
       blocks_until_can_be_closed, commitment_blocks, magma_status
from magma_orders
where channel_point <> '' and not (magma_status = any($1))
`, terminal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]MagmaChannelCommitment, 0, 8)
	for rows.Next() {
		var item MagmaChannelCommitment
		if err := rows.Scan(&item.OrderID, &item.ChannelPoint, &item.ChannelSCID,
			&item.BuyerAlias, &item.BuyerPubkey, &item.SizeSat, &item.RevenueSat,
			&item.FeeRateCapPPM, &item.BaseFeeCapSat, &item.BlocksRemaining,
			&item.CommitmentBlocks, &item.MagmaStatus); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// refreshCommitmentCache rebuilds the shared lookup. Called from the poller so
// readers never pay for the query.
func (s *MagmaService) refreshCommitmentCache(ctx context.Context) {
	items, err := s.ActiveCommitments(ctx)
	if err != nil {
		return
	}
	byPoint := make(map[string]MagmaChannelCommitment, len(items))
	byChannel := make(map[uint64]MagmaChannelCommitment, len(items))
	for _, item := range items {
		byPoint[strings.ToLower(strings.TrimSpace(item.ChannelPoint))] = item
	}
	// The short channel id is resolved from the node: Amboss reports it in its
	// own textual form, and autofee identifies channels by the numeric id.
	if s.lnd != nil {
		if channels, err := s.lnd.ListChannels(ctx); err == nil {
			for _, channel := range channels {
				key := strings.ToLower(strings.TrimSpace(channel.ChannelPoint))
				if item, ok := byPoint[key]; ok {
					byChannel[channel.ChannelID] = item
				}
			}
		}
	}
	magmaCommitments.mu.Lock()
	magmaCommitments.byPoint = byPoint
	magmaCommitments.byChannel = byChannel
	magmaCommitments.loadedAt = time.Now()
	magmaCommitments.mu.Unlock()
}

// magmaCommitmentForChannel is the hot-path lookup used while applying fees.
// It never blocks on the database and never fails: an empty cache means "no
// known commitment", which leaves the caller's behaviour unchanged.
func magmaCommitmentForChannel(channelID uint64, channelPoint string) (MagmaChannelCommitment, bool) {
	magmaCommitments.mu.RLock()
	defer magmaCommitments.mu.RUnlock()
	if magmaCommitments.byChannel != nil {
		if item, ok := magmaCommitments.byChannel[channelID]; ok {
			return item, true
		}
	}
	if magmaCommitments.byPoint != nil {
		key := strings.ToLower(strings.TrimSpace(channelPoint))
		if item, ok := magmaCommitments.byPoint[key]; ok {
			return item, true
		}
	}
	return MagmaChannelCommitment{}, false
}

// magmaClampChannelFees holds a sold channel inside the ceiling it was sold
// under. Returns the values to apply and whether anything was capped.
//
// This replaces excluding the channel from Autofee. Excluding froze the fee at
// whatever it was at open time, which forfeits the routing income the channel
// was bought to carry; clamping lets Autofee price freely underneath the
// contractual ceiling. The ceiling itself is not ours to negotiate: Amboss
// measures time spent above it in fee_above_cap_seconds.
func magmaClampChannelFees(channelID uint64, channelPoint string, baseFeeMsat, feeRatePPM int64) (int64, int64, bool) {
	commitment, ok := magmaCommitmentForChannel(channelID, channelPoint)
	if !ok {
		return baseFeeMsat, feeRatePPM, false
	}
	clamped := false
	if commitment.FeeRateCapPPM > 0 && feeRatePPM > commitment.FeeRateCapPPM {
		feeRatePPM = magmaFeeUnderCap(commitment.FeeRateCapPPM)
		clamped = true
	}
	// The order states the base fee ceiling in sats; LND takes msat.
	capBaseMsat := commitment.BaseFeeCapSat * 1000
	if baseFeeMsat > capBaseMsat {
		baseFeeMsat = capBaseMsat
		clamped = true
	}
	return baseFeeMsat, feeRatePPM, clamped
}
