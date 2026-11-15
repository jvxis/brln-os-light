package server

import (
	"context"
	"math"
	"sort"

	"lightningos-light/internal/lndclient"
)

// Inbound discount exit cap (0.5.57).
//
// LND charges a forward max(0, outbound fee + inbound fee). A negative inbound
// fee on channel C therefore makes every exit whose outbound fee is below the
// discount forward for free. The balanced discount is budgeted on C alone
// (its fee against its cost anchor), so nothing stopped a sink priced at
// 3.500 ppm from carrying a discount of 1.869 while the flow it attracted
// left through sellers priced at 1.300–1.700 ppm. Those sellers are refilled
// by paid rebalances: the forward earned nothing, the stock had to be bought
// back, and C filled up with liquidity it sells slowly.
//
// The cap looks at where the inbound flow of C actually went in the engine's
// 7 and 21 day windows (notifications grouped by chan_id_in → chan_id_out).
// Exits that are refilled by paid rebalances in the same window and that
// carry at least InboundDiscountExitShareMin of the inbound volume bound the
// discount to their advertised fee minus the profile's retained spread. Exits
// that are sources or refill themselves do not bound anything: draining them
// cheaply into a seller is the mix working as intended. The lower of the two
// windows wins. Without inbound history there is no cap and nothing changes.
//
// Every number comes from the profile (share, spread), the node's own history
// (exits, paid refills) and the exits' current policies. An override of 100%
// for the share switches the cap off, so no extra switch is added.

type inboundExitCapResult struct {
	Ppm           int
	Window        string
	ExitChannelID uint64
	ExitFeePpm    int
	Share         float64
}

// inboundDiscountExitCap is the pure form: exits is chan_id_out → inbound msat
// that entered through the candidate channel, paidExit says whether an exit
// was refilled by paid rebalances in the same window, exitFeePpm is the exit's
// current advertised outbound fee.
func inboundDiscountExitCap(exits map[uint64]int64, paidExit func(uint64) bool, exitFeePpm func(uint64) int, shareMin float64, retainedSpreadFrac float64) (int, uint64, int, float64) {
	if len(exits) == 0 || shareMin <= 0 || shareMin >= 1.0 {
		return 0, 0, 0, 0
	}
	if retainedSpreadFrac <= 0 {
		retainedSpreadFrac = 0.12
	}
	total := int64(0)
	for _, amt := range exits {
		if amt > 0 {
			total += amt
		}
	}
	if total <= 0 {
		return 0, 0, 0, 0
	}
	ids := make([]uint64, 0, len(exits))
	for id := range exits {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	capPpm, capExit, capFee, capShare := 0, uint64(0), 0, 0.0
	for _, id := range ids {
		amt := exits[id]
		if amt <= 0 {
			continue
		}
		share := float64(amt) / float64(total)
		if share < shareMin {
			continue
		}
		if paidExit == nil || !paidExit(id) {
			continue
		}
		fee := 0
		if exitFeePpm != nil {
			fee = exitFeePpm(id)
		}
		if fee <= 0 {
			continue
		}
		bound := int(math.Floor(float64(fee) * (1.0 - retainedSpreadFrac)))
		if bound < 0 {
			bound = 0
		}
		if capExit == 0 || bound < capPpm {
			capPpm, capExit, capFee, capShare = bound, id, fee, share
		}
	}
	return capPpm, capExit, capFee, capShare
}

func inboundDiscountExitShareMinFor(cfg AutofeeConfig, profile autofeeProfile) float64 {
	share := profile.InboundDiscountExitShareMin
	if cfg.InboundDiscountExitShareMinOverride > 0 {
		share = cfg.InboundDiscountExitShareMinOverride
	}
	return share
}

// autofeeInboundRetainedSpreadFrac mirrors the resolution the engine does for
// the balanced discount: profile value, then the Fee Center override.
func autofeeInboundRetainedSpreadFrac(cfg AutofeeConfig, profile autofeeProfile) float64 {
	spread := profile.InboundDiscountMinRetainedSpreadFrac
	if spread <= 0 {
		spread = 0.12
	}
	if cfg.InboundDiscountMinRetainedSpreadFracOverride > 0 {
		spread = cfg.InboundDiscountMinRetainedSpreadFracOverride
	}
	return spread
}

func normalizeInboundDiscountExitShareMinOverride(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return clampFloat(value, 0.01, 1.00)
}

// fetchInboundExitStats: inbound msat per (chan_id_in, chan_id_out) pair over
// the lookback, from the same notifications the forward stats read.
func (e *autofeeEngine) fetchInboundExitStats(ctx context.Context, lookback int) (map[uint64]map[uint64]int64, error) {
	out := map[uint64]map[uint64]int64{}
	if e == nil || e.svc == nil || e.svc.db == nil {
		return out, nil
	}
	rows, err := e.svc.db.Query(ctx, `
select chan_id_in, coalesce(chan_id_out, channel_id) as chan_id_out,
  coalesce(sum(case when amount_in_msat > 0 then amount_in_msat else amount_sat * 1000 end), 0)
from notifications
where type='forward' and occurred_at >= now() - ($1 * interval '1 day')
  and chan_id_in is not null and coalesce(chan_id_out, channel_id) is not null
group by chan_id_in, coalesce(chan_id_out, channel_id)
`, lookback)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var in, exit, amt int64
		if err := rows.Scan(&in, &exit, &amt); err != nil {
			return out, err
		}
		if in == 0 || exit == 0 || amt <= 0 {
			continue
		}
		m := out[uint64(in)]
		if m == nil {
			m = map[uint64]int64{}
			out[uint64(in)] = m
		}
		m[uint64(exit)] += amt
	}
	return out, rows.Err()
}

// loadInboundExits fills the engine's exit maps and the advertised fee of
// every channel of the node for the run. A failed read logs and leaves the
// maps empty, which means no cap: the discount then behaves as before.
func (e *autofeeEngine) loadInboundExits(ctx context.Context, channels []lndclient.ChannelInfo) {
	e.channelFeePpm = map[uint64]int{}
	for _, ch := range channels {
		if ch.ChannelID == 0 || ch.FeeRatePpm == nil {
			continue
		}
		e.channelFeePpm[ch.ChannelID] = int(*ch.FeeRatePpm)
	}
	e.inboundExits7d = map[uint64]map[uint64]int64{}
	e.inboundExits21d = map[uint64]map[uint64]int64{}
	if !e.cfg.InboundPassiveEnabled {
		return
	}
	if exits, err := e.fetchInboundExitStats(ctx, 7); err == nil {
		e.inboundExits7d = exits
	} else if e.svc != nil && e.svc.logger != nil {
		e.svc.logger.Printf("autofee: inbound exits 7d unavailable: %v", err)
	}
	if exits, err := e.fetchInboundExitStats(ctx, 21); err == nil {
		e.inboundExits21d = exits
	} else if e.svc != nil && e.svc.logger != nil {
		e.svc.logger.Printf("autofee: inbound exits 21d unavailable: %v", err)
	}
}

// inboundDiscountExitCapFor resolves the cap for one channel on both windows
// and keeps the lower one. rebal7d/rebal21d say which exits were refilled by
// paid rebalances in each window.
func (e *autofeeEngine) inboundDiscountExitCapFor(channelID uint64, rebal7d rebalStats, rebal21d rebalStats, retainedSpreadFrac float64) inboundExitCapResult {
	result := inboundExitCapResult{}
	if e == nil || channelID == 0 {
		return result
	}
	shareMin := inboundDiscountExitShareMinFor(e.cfg, e.profile)
	feeOf := func(id uint64) int {
		if e.channelFeePpm == nil {
			return 0
		}
		return e.channelFeePpm[id]
	}
	paidIn := func(stats rebalStats) func(uint64) bool {
		return func(id uint64) bool {
			if stats.ByChannel == nil {
				return false
			}
			st, ok := stats.ByChannel[id]
			return ok && st.Count > 0 && st.FeeMsat > 0
		}
	}
	windows := []struct {
		name  string
		exits map[uint64]int64
		paid  func(uint64) bool
	}{
		{"7d", e.inboundExits7d[channelID], paidIn(rebal7d)},
		{"21d", e.inboundExits21d[channelID], paidIn(rebal21d)},
	}
	for _, w := range windows {
		capPpm, exit, fee, share := inboundDiscountExitCap(w.exits, w.paid, feeOf, shareMin, retainedSpreadFrac)
		if exit == 0 {
			continue
		}
		if result.ExitChannelID == 0 || capPpm < result.Ppm {
			result = inboundExitCapResult{Ppm: capPpm, Window: w.name, ExitChannelID: exit, ExitFeePpm: fee, Share: share}
		}
	}
	return result
}

// inboundDiscountFullCut: the channel sits at or above the outbound target the
// operator set for it in the Rebalance Center, so there is nothing left for a
// discount to attract. The discount is dropped at once instead of walking
// through grace and decay. Without a target (channel not managed there) the
// lifecycle is unchanged.
func inboundDiscountFullCut(outRatio float64, runtime autofeeRebalanceRuntimeSnapshot) bool {
	if runtime.TargetOutboundPct <= 0 {
		return false
	}
	return outRatio*100 >= runtime.TargetOutboundPct
}
