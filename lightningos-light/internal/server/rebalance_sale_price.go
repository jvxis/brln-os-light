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
//
// 0.5.45: any sale in 30 days counts as a price (BitcoinVN 22 sold once at
// 879 ppm and, with the 5-forward minimum, was refilled at 1034), and a
// channel older than 30 days with no sale at all has no price to buy
// against: the autopilot skips it (no_sale_30d) until it sells something.
// Operator-initiated rebalances are not affected.
//
// Drained retest (0.5.45): a channel that is empty cannot sell, so its
// realized price cannot move. RA⚡KO, 2026-10-13: the discovery ladder found
// a route at 320 ppm, AutoFee raised the fee to 455, and the cap kept
// pricing on the 332 ppm the channel last sold at (0.8 × 332 = 265), so the
// refill never fit and the channel stayed empty. Below
// realizedPriceCapDrainedLocalPct the cap goes back to the advertised fee for
// one small lot (the keepalive size), which is what re-measures the price:
// if that lot sells, the realized price follows and the normal cap applies
// to the next one; if it does not, the stock gate and no_sale_30d hold.
const (
	realizedPriceCapMinForwards7d   = 3
	realizedPriceCapMinForwards30d  = 1
	realizedPriceCapNoSaleAgeHours  = 30 * 24
	realizedPriceCapDrainedLocalPct = 10.0
)

// realizedPriceCapDrained: nothing left to sell, so the realized price is
// stale by construction. The band is 10% of capacity or half the channel's
// target, whichever is larger: a seller with a 50% target that the retest
// lot lifted to 16% is still empty for its purpose, and on 16/10 RA⚡KO,
// PurpleWisteria and CoinPayments sat between 12% and 17%, out of the band,
// back on the realized cap and not refilling (the "10% cliff").
func realizedPriceCapDrained(cfg RebalanceConfig, localPct float64, targetPct float64) bool {
	return cfg.RealizedPriceCapEnabled && localPct < realizedPriceCapDrainedBandPct(targetPct)
}

func realizedPriceCapDrainedBandPct(targetPct float64) float64 {
	band := realizedPriceCapDrainedLocalPct
	if half := targetPct / 2; half > band {
		band = half
	}
	return band
}

// drainedRetestAmountSat bounds the lot bought at the advertised-fee cap to
// the keepalive size: enough to measure a price, not enough to restock a
// channel at a price it may not clear.
func drainedRetestAmountSat(channel RebalanceChannel, cfg RebalanceConfig, amount int64) int64 {
	lot := sovereignKeepaliveRefillAmount(channel, cfg)
	if lot <= 0 || amount <= lot {
		return amount
	}
	return lot
}

type channelSalePrice struct {
	Ppm7d    int64
	Count7d  int64
	Ppm30d   int64
	Count30d int64
	AgeHours float64
}

// NoSale reports a channel old enough to have a sale history and without
// any outbound forward in 30 days.
func (p channelSalePrice) NoSale() bool {
	return p.Count30d == 0 && p.AgeHours >= realizedPriceCapNoSaleAgeHours
}

// salePriceSnapshot is what one scan loads: prices per channel plus the
// block height that turns a short channel id into an age.
type salePriceSnapshot struct {
	prices      map[uint64]channelSalePrice
	blockHeight int64
}

func (s salePriceSnapshot) forChannel(ch lndclient.ChannelInfo) channelSalePrice {
	price := s.prices[ch.ChannelID]
	price.AgeHours = channelRankingChannelAgeHours(ch.ChannelID, s.blockHeight)
	return price
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
func feeCapReferencePpm(cfg RebalanceConfig, advertisedPpm int64, salePricePpm int64, localPct float64, targetPct float64) int64 {
	if !cfg.RealizedPriceCapEnabled || salePricePpm <= 0 || salePricePpm >= advertisedPpm {
		return advertisedPpm
	}
	if realizedPriceCapDrained(cfg, localPct, targetPct) {
		return advertisedPpm
	}
	return salePricePpm
}

// realizedFeeCapPolicy builds the target policy used by calcFeeLimitMsat.
func realizedFeeCapPolicy(ch RebalanceChannel, cfg RebalanceConfig) lndclient.ChannelPolicySnapshot {
	return lndclient.ChannelPolicySnapshot{
		FeeRatePpm:  feeCapReferencePpm(cfg, ch.OutgoingFeePpm, ch.SalePricePpm, ch.LocalPct, ch.TargetOutboundPct),
		BaseFeeMsat: ch.OutgoingBaseMsat,
	}
}

// loadSalePricesForSnapshots reads the sale prices only when the cap is on;
// with it off the snapshot keeps the advertised fee and no query runs. A
// failed read logs and leaves every channel on its advertised fee.
func (s *RebalanceService) loadSalePricesForSnapshots(ctx context.Context, cfg RebalanceConfig) salePriceSnapshot {
	snapshot := salePriceSnapshot{prices: map[uint64]channelSalePrice{}}
	if !cfg.RealizedPriceCapEnabled {
		return snapshot
	}
	prices, err := s.fetchChannelSalePrices(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance: sale prices unavailable, fee cap on advertised fee: %v", err)
		}
		return snapshot
	}
	snapshot.prices = prices
	// Without a height every channel reads as age 0 and no_sale_30d never
	// fires; that is the safe side.
	if s.lnd != nil {
		if status, statusErr := s.lnd.GetStatus(ctx); statusErr == nil && status.BlockHeight > 0 {
			snapshot.blockHeight = status.BlockHeight
		}
	}
	return snapshot
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

// autoTargetEligibility folds the two 0.5.45/0.5.46 rules on top of the cost
// gate: a drained retest lot is bounded by its size, so it does not wait for
// the historical cost to fit the budget (BitcoinVN, 15/10: one 1034 ppm lot
// in cost7d kept the 923×0.8 retest ineligible); a channel with no sale in
// 30 days is never an automatic target.
func autoTargetEligibility(eligibleTarget, drainedRetest, eligibleManualTarget, noSale30d bool) bool {
	if drainedRetest && eligibleManualTarget {
		eligibleTarget = true
	}
	if noSale30d {
		return false
	}
	return eligibleTarget
}
