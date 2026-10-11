package server

import (
	"context"

	"lightningos-light/internal/lndclient"
)

// Realized price cap (opt-in, 0.5.43).
//
// The autopilot's fee cap is econ_ratio × the target's advertised fee. When
// the advertised fee sits above the price the channel actually clears at,
// the cap lets the autopilot pay almost what the channel really sells for.
// Friendspool, 30 days to 2026-10-10: 60% of the rebalance spend (44k of
// 74k sats) was paid above 80% of the channel's realized sale price;
// bfx-lnd0 paid 1.559 ppm to sell at 1.619 and lost 8.6k. With the cap on,
// the ratio applies to min(advertised fee, realized sale price), so the
// margin the operator configured is the margin that is actually earned.
//
// The realized price is the outbound forward ppm of the channel: 7 days
// when there are enough forwards, 30 days as fallback, otherwise the
// advertised fee (new channel, nothing sold yet).
const (
	realizedPriceCapMinForwards7d  = 3
	realizedPriceCapMinForwards30d = 5
)

type channelSalePrice struct {
	Ppm7d    int64
	Count7d  int64
	Ppm30d   int64
	Count30d int64
}

// referencePpm picks the realized sale price and names its window.
func (p channelSalePrice) referencePpm() (int64, string) {
	if p.Count7d >= realizedPriceCapMinForwards7d && p.Ppm7d > 0 {
		return p.Ppm7d, "7d"
	}
	if p.Count30d >= realizedPriceCapMinForwards30d && p.Ppm30d > 0 {
		return p.Ppm30d, "30d"
	}
	return 0, ""
}

// feeCapReferencePpm is the fee the autopilot prices its purchases against:
// the advertised fee, or the realized sale price when it is lower and the
// cap is on.
func feeCapReferencePpm(cfg RebalanceConfig, advertisedPpm int64, salePricePpm int64) int64 {
	if !cfg.RealizedPriceCapEnabled || salePricePpm <= 0 || salePricePpm >= advertisedPpm {
		return advertisedPpm
	}
	return salePricePpm
}

// realizedFeeCapPolicy builds the target policy used by calcFeeLimitMsat.
func realizedFeeCapPolicy(ch RebalanceChannel, cfg RebalanceConfig) lndclient.ChannelPolicySnapshot {
	return lndclient.ChannelPolicySnapshot{
		FeeRatePpm:  feeCapReferencePpm(cfg, ch.OutgoingFeePpm, ch.SalePricePpm),
		BaseFeeMsat: ch.OutgoingBaseMsat,
	}
}

// loadSalePricesForSnapshots reads the sale prices only when the cap is on;
// with it off the snapshot keeps the advertised fee and no query runs. A
// failed read logs and leaves every channel on its advertised fee.
func (s *RebalanceService) loadSalePricesForSnapshots(ctx context.Context, cfg RebalanceConfig) map[uint64]channelSalePrice {
	if !cfg.RealizedPriceCapEnabled {
		return map[uint64]channelSalePrice{}
	}
	prices, err := s.fetchChannelSalePrices(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance: sale prices unavailable, fee cap on advertised fee: %v", err)
		}
		return map[uint64]channelSalePrice{}
	}
	return prices
}

// fetchChannelSalePrices: outbound forward ppm and count per channel over
// 7 and 30 days, same fee fallback chain as the AutoFee forward stats.
func (s *RebalanceService) fetchChannelSalePrices(ctx context.Context) (map[uint64]channelSalePrice, error) {
	out := map[uint64]channelSalePrice{}
	if s == nil || s.db == nil {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
with f as (
  select coalesce(chan_id_out, channel_id) as chan_id,
    occurred_at,
    case
      when fee_msat > 0 then fee_msat
      when fee_sat > 0 then fee_sat * 1000
      when amount_in_msat > 0 and amount_out_msat > 0 and amount_in_msat > amount_out_msat then amount_in_msat - amount_out_msat
      else 0
    end as fee_msat,
    case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end as amt_msat
  from notifications
  where type='forward' and occurred_at >= now() - interval '30 days'
    and coalesce(chan_id_out, channel_id) is not null
)
select chan_id,
  coalesce(sum(fee_msat) filter (where occurred_at >= now() - interval '7 days'), 0),
  coalesce(sum(amt_msat) filter (where occurred_at >= now() - interval '7 days'), 0),
  count(*) filter (where occurred_at >= now() - interval '7 days'),
  coalesce(sum(fee_msat), 0),
  coalesce(sum(amt_msat), 0),
  count(*)
from f
group by chan_id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var chanID, fee7, amt7, n7, fee30, amt30, n30 int64
		if err := rows.Scan(&chanID, &fee7, &amt7, &n7, &fee30, &amt30, &n30); err != nil {
			return out, err
		}
		out[uint64(chanID)] = channelSalePrice{
			Ppm7d:    int64(ppmMsat(fee7, amt7)),
			Count7d:  n7,
			Ppm30d:   int64(ppmMsat(fee30, amt30)),
			Count30d: n30,
		}
	}
	return out, rows.Err()
}
