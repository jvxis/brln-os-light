package server

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	// Stock gate ("sold, then refill"): pause Sovereign purchases into a peer
	// while the paid liquidity already bought there has not sold. The working
	// stock (stock_gate_min_stock_pct of capacity, and never fewer than
	// sovereignStockGateMinLots unsold lots) is always allowed, so the first
	// refills of a drained channel are never held back.
	stockGateMinStockPctDefault = 10.0
	stockGateMinStockPctMin     = 1.0
	stockGateMinStockPctMax     = 50.0
	stockGateCoverDaysDefault   = 3
	stockGateCoverDaysMax       = 14
	sovereignStockGateMinLots   = 2
	sovereignStockGateReason    = "paid_stock_gate"
	// The gate remembers lots for twice the inventory window. With the 7-day
	// window alone, stock bought eight days ago vanished from the count while
	// it was still sitting in the channel, and the gate let the autopilot buy
	// again on top of it (LQWD-Australia, 2026-10-04: 2.4M unsold, 7 sats
	// sold in a week, and 0.5M more bought).
	sovereignStockGateWindowFactor = 2
)

// sovereignStockLevel is the paid liquidity the Sovereign autopilot bought for
// one peer and has not sold yet, next to what that peer actually sells. It is
// aggregated per peer: LND forwards through any channel of the peer, so three
// drained channels to the same node share one demand and one stock.
type sovereignStockLevel struct {
	Known      bool
	UnsoldSat  int64
	UnsoldLots int
	DemandSat  int64
	WindowDays float64
}

type sovereignStockChannel struct {
	ChannelID       uint64
	PeerPubkey      string
	LocalBalanceSat int64
}

// sovereignStockGateWindow: how far back paid lots count as stock. Demand
// (sales per day) keeps the inventory window so cover_days means what the
// label says.
func sovereignStockGateWindow(cfg RebalanceConfig) time.Duration {
	return sovereignStockGateWindowFactor * sovereignUnsoldInventoryWindow(cfg)
}

func stockGateMinStockPctForConfig(cfg RebalanceConfig) float64 {
	if cfg.StockGateMinStockPct < stockGateMinStockPctMin || cfg.StockGateMinStockPct > stockGateMinStockPctMax {
		return stockGateMinStockPctDefault
	}
	return cfg.StockGateMinStockPct
}

// buildSovereignStockLevels measures, per peer, the unsold remainder of every
// Sovereign lot inside the inventory window (FIFO, same attribution the unsold
// cooldown uses) and the forward volume the peer sold in that window. The
// remainder is capped by the peer's current local balance: liquidity that left
// by another path (payments, being used as a source) is not stock anymore.
func buildSovereignStockLevels(lots []rebalanceAttributionLot, forwards []rebalanceAttributionForward, channels []sovereignStockChannel, cfg RebalanceConfig, now time.Time) map[uint64]sovereignStockLevel {
	levels := make(map[uint64]sovereignStockLevel, len(channels))
	if len(channels) == 0 {
		return levels
	}
	if now.IsZero() {
		now = time.Now()
	}
	window := sovereignUnsoldInventoryWindow(cfg)
	since := now.Add(-window)
	stockSince := now.Add(-sovereignStockGateWindow(cfg))
	validLots := make([]rebalanceAttributionLot, 0, len(lots))
	for _, lot := range lots {
		if !lot.CompletedAt.After(now) {
			validLots = append(validLots, lot)
		}
	}
	validForwards := make([]rebalanceAttributionForward, 0, len(forwards))
	for _, forward := range forwards {
		if !forward.OccurredAt.After(now) {
			validForwards = append(validForwards, forward)
		}
	}
	// A lot stays eligible for sales for as long as the gate remembers it,
	// otherwise a sale against 10-day-old stock would be attributed to a
	// newer lot and the old one would look unsold forever.
	attributed := attributeRebalanceForwardsFIFO(validLots, validForwards, sovereignStockGateWindow(cfg))

	peerOf := make(map[uint64]string, len(channels))
	peerLocal := map[string]int64{}
	for _, ch := range channels {
		if ch.ChannelID == 0 {
			continue
		}
		peer := strings.TrimSpace(ch.PeerPubkey)
		if peer == "" {
			peer = "chan:" + strconv.FormatUint(ch.ChannelID, 10)
		}
		peerOf[ch.ChannelID] = peer
		if ch.LocalBalanceSat > 0 {
			peerLocal[peer] += ch.LocalBalanceSat
		}
	}

	type peerStock struct {
		unsold int64
		lots   int
		demand int64
	}
	peers := map[string]*peerStock{}
	stockOf := func(channelID uint64) *peerStock {
		peer, ok := peerOf[channelID]
		if !ok {
			return nil
		}
		if peers[peer] == nil {
			peers[peer] = &peerStock{}
		}
		return peers[peer]
	}
	for _, lot := range validLots {
		if lot.JobID <= 0 || lot.SentSat <= 0 || !lot.CompletedAt.After(stockSince) || lot.TriggerReason != rebalanceSovereignReason {
			continue
		}
		stock := stockOf(lot.TargetChannelID)
		if stock == nil {
			continue
		}
		remaining := lot.SentSat - attributed[lot.JobID].ForwardAmountSat
		if remaining <= 0 {
			continue
		}
		stock.unsold += remaining
		// A lot counts as unsold while more than half of it is still there.
		if remaining*2 >= lot.SentSat {
			stock.lots++
		}
	}
	for _, forward := range validForwards {
		if forward.AmountSat <= 0 || !forward.OccurredAt.After(since) {
			continue
		}
		if stock := stockOf(forward.TargetChannelID); stock != nil {
			stock.demand += forward.AmountSat
		}
	}

	windowDays := window.Hours() / 24
	for channelID, peer := range peerOf {
		level := sovereignStockLevel{Known: true, WindowDays: windowDays}
		if stock := peers[peer]; stock != nil {
			level.UnsoldSat = stock.unsold
			level.UnsoldLots = stock.lots
			level.DemandSat = stock.demand
			if local := peerLocal[peer]; level.UnsoldSat > local {
				level.UnsoldSat = local
			}
		}
		levels[channelID] = level
	}
	return levels
}

// sovereignStockGateAllowedSat is the unsold paid stock a peer may hold before
// purchases pause: the working stock (a share of the target channel capacity)
// or cover_days of what the peer actually sells per day, whichever is larger.
// A proven seller therefore keeps a deeper stock than a channel with no sales.
func sovereignStockGateAllowedSat(level sovereignStockLevel, capacitySat int64, cfg RebalanceConfig) int64 {
	if capacitySat <= 0 {
		return 0
	}
	allowed := int64(math.Round(float64(capacitySat) * stockGateMinStockPctForConfig(cfg) / 100))
	if cfg.StockGateCoverDays > 0 && level.WindowDays > 0 && level.DemandSat > 0 {
		cover := int64(math.Round(float64(level.DemandSat) / level.WindowDays * float64(cfg.StockGateCoverDays)))
		if cover > allowed {
			allowed = cover
		}
	}
	return allowed
}

// sovereignStockGateBlocks reports whether a new Sovereign purchase must wait
// for sales. It never fires before the peer holds sovereignStockGateMinLots
// unsold lots, so a drained channel always receives its first refills.
func sovereignStockGateBlocks(level sovereignStockLevel, capacitySat int64, cfg RebalanceConfig) bool {
	if !cfg.StockGateEnabled || !level.Known || capacitySat <= 0 {
		return false
	}
	if level.UnsoldLots < sovereignStockGateMinLots {
		return false
	}
	return level.UnsoldSat >= sovereignStockGateAllowedSat(level, capacitySat, cfg)
}

// loadSovereignStockLevels loads the attribution inputs for the Sovereign
// targets and every sibling channel of the same peers. Opt-in: with the gate
// off it costs nothing. On a DB error the levels stay unknown and the gate
// does not block; the unsold-inventory guard already pauses on that error.
func (s *RebalanceService) loadSovereignStockLevels(ctx context.Context, cfg RebalanceConfig, snapshots []RebalanceChannel, targetIDs []uint64, now time.Time) map[uint64]sovereignStockLevel {
	if !cfg.StockGateEnabled || s.db == nil || len(targetIDs) == 0 {
		return nil
	}
	targets := make(map[uint64]struct{}, len(targetIDs))
	for _, id := range targetIDs {
		if id != 0 {
			targets[id] = struct{}{}
		}
	}
	peers := map[string]struct{}{}
	for _, snapshot := range snapshots {
		if _, ok := targets[snapshot.ChannelID]; !ok {
			continue
		}
		if peer := strings.TrimSpace(snapshot.RemotePubkey); peer != "" {
			peers[peer] = struct{}{}
		}
	}
	channels := make([]sovereignStockChannel, 0, len(targetIDs))
	ids := make([]uint64, 0, len(targetIDs))
	for _, snapshot := range snapshots {
		if snapshot.ChannelID == 0 {
			continue
		}
		_, isTarget := targets[snapshot.ChannelID]
		_, isSibling := peers[strings.TrimSpace(snapshot.RemotePubkey)]
		if !isTarget && !isSibling {
			continue
		}
		channels = append(channels, sovereignStockChannel{
			ChannelID:       snapshot.ChannelID,
			PeerPubkey:      snapshot.RemotePubkey,
			LocalBalanceSat: snapshot.LocalBalanceSat,
		})
		ids = append(ids, snapshot.ChannelID)
	}
	if len(ids) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	lots, forwards, err := s.loadRebalanceAttributionInputs(ctx, now.Add(-2*sovereignStockGateWindow(cfg)), ids)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance stock gate unavailable: %v", err)
		}
		return nil
	}
	return buildSovereignStockLevels(lots, forwards, channels, cfg, now)
}
