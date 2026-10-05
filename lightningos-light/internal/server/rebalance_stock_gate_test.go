package server

import (
	"context"
	"testing"
	"time"
)

func stockGateTestConfig() RebalanceConfig {
	cfg := defaultRebalanceConfig()
	cfg.StockGateEnabled = true
	cfg.StockGateMinStockPct = 10
	cfg.StockGateCoverDays = 3
	return cfg
}

func stockGateLot(jobID int64, channelID uint64, completedAt time.Time, sentSat int64) rebalanceAttributionLot {
	return rebalanceAttributionLot{JobID: jobID, TargetChannelID: channelID, Status: "succeeded",
		TriggerReason: rebalanceSovereignReason, CompletedAt: completedAt, SentSat: sentSat, FeePaidSat: sentSat / 2000}
}

func TestStockGateNeverBlocksTheFirstRefills(t *testing.T) {
	cfg := stockGateTestConfig()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	channels := []sovereignStockChannel{{ChannelID: 1, PeerPubkey: "peer-a", LocalBalanceSat: 5_000_000}}
	const capacity = int64(5_000_000)

	// One unsold lot bigger than the whole working stock: still allowed. The
	// operator asked that the gate never fires on the first rebalance.
	one := []rebalanceAttributionLot{stockGateLot(1, 1, now.Add(-6*time.Hour), 900_000)}
	level := buildSovereignStockLevels(one, nil, channels, cfg, now)[1]
	if !level.Known || level.UnsoldSat != 900_000 || level.UnsoldLots != 1 {
		t.Fatalf("unexpected level for a single lot: %+v", level)
	}
	if sovereignStockGateBlocks(level, capacity, cfg) {
		t.Fatal("a single unsold lot must never block the next purchase")
	}

	// Two small lots below the working stock (10% of 5M = 500k): allowed.
	small := []rebalanceAttributionLot{
		stockGateLot(1, 1, now.Add(-8*time.Hour), 150_000),
		stockGateLot(2, 1, now.Add(-6*time.Hour), 150_000),
	}
	level = buildSovereignStockLevels(small, nil, channels, cfg, now)[1]
	if sovereignStockGateBlocks(level, capacity, cfg) {
		t.Fatalf("300k unsold is below the 500k working stock, must not block: %+v", level)
	}

	// Two lots that fill the working stock with no sale: now it waits.
	full := []rebalanceAttributionLot{
		stockGateLot(1, 1, now.Add(-8*time.Hour), 250_000),
		stockGateLot(2, 1, now.Add(-6*time.Hour), 250_000),
	}
	level = buildSovereignStockLevels(full, nil, channels, cfg, now)[1]
	if !sovereignStockGateBlocks(level, capacity, cfg) {
		t.Fatalf("working stock reached with zero sales must block: %+v", level)
	}
}

func TestStockGateReopensWhenStockSells(t *testing.T) {
	cfg := stockGateTestConfig()
	cfg.StockGateCoverDays = 0
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	channels := []sovereignStockChannel{{ChannelID: 1, PeerPubkey: "peer-a", LocalBalanceSat: 5_000_000}}
	lots := []rebalanceAttributionLot{
		stockGateLot(1, 1, now.Add(-20*time.Hour), 300_000),
		stockGateLot(2, 1, now.Add(-18*time.Hour), 300_000),
	}
	blocked := buildSovereignStockLevels(lots, nil, channels, cfg, now)[1]
	if !sovereignStockGateBlocks(blocked, 5_000_000, cfg) {
		t.Fatalf("600k unsold over a 500k working stock must block: %+v", blocked)
	}
	forwards := []rebalanceAttributionForward{{ID: 1, TargetChannelID: 1, OccurredAt: now.Add(-2 * time.Hour), AmountSat: 400_000, FeeMsat: 400_000}}
	sold := buildSovereignStockLevels(lots, forwards, channels, cfg, now)[1]
	if sold.UnsoldSat != 200_000 || sold.DemandSat != 400_000 {
		t.Fatalf("FIFO must consume the oldest lot first: %+v", sold)
	}
	if sovereignStockGateBlocks(sold, 5_000_000, cfg) {
		t.Fatal("after selling 400k the channel must be refillable again")
	}
}

func TestStockGateProvenSellerKeepsDeeperStock(t *testing.T) {
	cfg := stockGateTestConfig()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	channels := []sovereignStockChannel{{ChannelID: 1, PeerPubkey: "peer-a", LocalBalanceSat: 8_000_000}}
	lots := []rebalanceAttributionLot{
		stockGateLot(1, 1, now.Add(-5*time.Hour), 600_000),
		stockGateLot(2, 1, now.Add(-4*time.Hour), 600_000),
	}
	// 7M sold in the 7-day window before the lots = 1M/day; cover 3 days = 3M.
	forwards := []rebalanceAttributionForward{{ID: 1, TargetChannelID: 1, OccurredAt: now.Add(-48 * time.Hour), AmountSat: 7_000_000, FeeMsat: 1}}
	level := buildSovereignStockLevels(lots, forwards, channels, cfg, now)[1]
	if level.UnsoldSat != 1_200_000 {
		t.Fatalf("a sale older than the lots must not consume them: %+v", level)
	}
	if allowed := sovereignStockGateAllowedSat(level, 8_000_000, cfg); allowed != 3_000_000 {
		t.Fatalf("expected 3 days of demand (3M) as allowance, got %d", allowed)
	}
	if sovereignStockGateBlocks(level, 8_000_000, cfg) {
		t.Fatal("a seller moving 1M/day must be allowed more than its 800k working stock")
	}
	cfg.StockGateCoverDays = 0
	if !sovereignStockGateBlocks(level, 8_000_000, cfg) {
		t.Fatal("without the demand cover the 800k working stock is the limit")
	}
}

func TestStockGateAggregatesChannelsOfTheSamePeer(t *testing.T) {
	cfg := stockGateTestConfig()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	channels := []sovereignStockChannel{
		{ChannelID: 1, PeerPubkey: "bfx", LocalBalanceSat: 1_300_000},
		{ChannelID: 2, PeerPubkey: "bfx", LocalBalanceSat: 4_300_000},
		{ChannelID: 3, PeerPubkey: "other", LocalBalanceSat: 2_000_000},
	}
	lots := []rebalanceAttributionLot{
		stockGateLot(1, 1, now.Add(-30*time.Hour), 100_000),
		stockGateLot(2, 2, now.Add(-20*time.Hour), 700_000),
		stockGateLot(3, 2, now.Add(-10*time.Hour), 700_000),
	}
	levels := buildSovereignStockLevels(lots, nil, channels, cfg, now)
	if levels[1].UnsoldSat != 1_500_000 || levels[1].UnsoldLots != 3 || levels[2] != levels[1] {
		t.Fatalf("both channels must see the peer total: %+v %+v", levels[1], levels[2])
	}
	// The 7M channel bought only 100k itself, but its peer already holds 1.5M
	// of unsold paid stock: more than its own 700k working stock.
	if !sovereignStockGateBlocks(levels[1], 7_000_000, cfg) {
		t.Fatal("unsold stock in a sibling channel must hold the small channel back")
	}
	if levels[3].UnsoldSat != 0 || sovereignStockGateBlocks(levels[3], 10_000_000, cfg) {
		t.Fatalf("another peer must be unaffected: %+v", levels[3])
	}
}

func TestStockGateIgnoresLiquidityThatAlreadyLeft(t *testing.T) {
	cfg := stockGateTestConfig()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	// 2M bought and never forwarded, but the channel holds only 150k now: the
	// liquidity left by another path, so it is not stock.
	channels := []sovereignStockChannel{{ChannelID: 1, PeerPubkey: "peer-a", LocalBalanceSat: 150_000}}
	lots := []rebalanceAttributionLot{
		stockGateLot(1, 1, now.Add(-30*time.Hour), 1_000_000),
		stockGateLot(2, 1, now.Add(-20*time.Hour), 1_000_000),
	}
	level := buildSovereignStockLevels(lots, nil, channels, cfg, now)[1]
	if level.UnsoldSat != 150_000 {
		t.Fatalf("unsold stock must be capped by the local balance: %+v", level)
	}
	if sovereignStockGateBlocks(level, 5_000_000, cfg) {
		t.Fatal("a channel that is physically empty must be refillable")
	}
}

func TestStockGateScopeAndDefaults(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	channels := []sovereignStockChannel{{ChannelID: 1, PeerPubkey: "peer-a", LocalBalanceSat: 5_000_000}}
	manual := stockGateLot(1, 1, now.Add(-9*time.Hour), 600_000)
	manual.TriggerReason = ""
	old := stockGateLot(2, 1, now.Add(-16*24*time.Hour), 600_000)
	lots := []rebalanceAttributionLot{manual, old,
		stockGateLot(3, 1, now.Add(-8*time.Hour), 300_000),
		stockGateLot(4, 1, now.Add(-7*time.Hour), 300_000),
	}
	cfg := stockGateTestConfig()
	level := buildSovereignStockLevels(lots, nil, channels, cfg, now)[1]
	if level.UnsoldSat != 600_000 || level.UnsoldLots != 2 {
		t.Fatalf("only Sovereign lots inside the gate window count: %+v", level)
	}
	if !sovereignStockGateBlocks(level, 5_000_000, cfg) {
		t.Fatal("expected the gate to hold with it enabled")
	}
	off := defaultRebalanceConfig()
	if off.StockGateEnabled || sovereignStockGateBlocks(level, 5_000_000, off) {
		t.Fatal("the stock gate is opt-in and must not change behavior when off")
	}
	if sovereignStockGateBlocks(sovereignStockLevel{}, 5_000_000, cfg) {
		t.Fatal("an unknown stock level must not block")
	}

	bad := cfg
	bad.StockGateMinStockPct = 0
	bad.StockGateCoverDays = 99
	bad = normalizeRebalanceConfig(bad)
	if bad.StockGateMinStockPct != stockGateMinStockPctDefault || bad.StockGateCoverDays != stockGateCoverDaysDefault {
		t.Fatalf("out-of-range knobs must fall back to defaults: %+v %+v", bad.StockGateMinStockPct, bad.StockGateCoverDays)
	}
}

func TestExecuteSovereignStockGateWaitsForSales(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	newPlan := func(level sovereignStockLevel) rebalanceAutoScanCandidatePlan {
		return rebalanceAutoScanCandidatePlan{
			Candidates: []rebalanceTarget{{
				Channel:         RebalanceChannel{ChannelID: 1, ChannelPoint: "test:0", CapacitySat: 5_000_000, TargetAmountSat: 300_000, OutgoingFeePpm: 1_000},
				ExpectedGainSat: 300, EstimatedCostSat: 120, BudgetCostSat: 120,
				ExpectedROI: 2.5, ExpectedROIValid: true,
			}},
			StockLevels: map[uint64]sovereignStockLevel{1: level},
		}
	}
	base := defaultRebalanceConfig()
	base.BudgetUnlimited = true
	base.SovereignMaxJobsPerCycle = 1
	base.SovereignMinExpectedProfitSat = 0
	full := sovereignStockLevel{Known: true, UnsoldSat: 600_000, UnsoldLots: 2, WindowDays: 7}

	svc := NewRebalanceService(nil, nil, nil)
	on := base
	on.StockGateEnabled = true
	result := svc.executeSovereignAutopilot(context.Background(), on, nil, newPlan(full), now, false)
	if len(result.Decisions) != 1 {
		t.Fatalf("missing decision: %+v", result)
	}
	d := result.Decisions[0]
	if d.Selected || d.Reason != sovereignStockGateReason || result.SkipReasons[sovereignStockGateReason] != 1 {
		t.Fatalf("unsold stock above the working stock must wait for sales: %+v", d)
	}
	if d.StockUnsoldSat != 600_000 || d.StockAllowedSat != 500_000 {
		t.Fatalf("decision must carry the stock evidence: %+v", d)
	}

	// First refill of a drained channel: one unsold lot never holds it back.
	first := sovereignStockLevel{Known: true, UnsoldSat: 600_000, UnsoldLots: 1, WindowDays: 7}
	result = svc.executeSovereignAutopilot(context.Background(), on, nil, newPlan(first), now, false)
	if len(result.Decisions) != 1 || !result.Decisions[0].Selected {
		t.Fatalf("the gate must not fire on the first rebalance: %+v", result.Decisions)
	}

	// Gate off: same stock, previous behavior.
	result = svc.executeSovereignAutopilot(context.Background(), base, nil, newPlan(full), now, false)
	if len(result.Decisions) != 1 || !result.Decisions[0].Selected || result.Decisions[0].StockUnsoldSat != 0 {
		t.Fatalf("with the gate off nothing may change: %+v", result.Decisions)
	}
}

func TestStockGateRemembersStockOlderThanTheInventoryWindow(t *testing.T) {
	cfg := stockGateTestConfig()
	cfg.StockGateCoverDays = 0
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	channels := []sovereignStockChannel{{ChannelID: 1, PeerPubkey: "lqwd", LocalBalanceSat: 2_400_000}}
	// Bought 9 and 10 days ago, never sold, still in the channel: with a 7-day
	// memory the gate forgot it and let the autopilot buy again.
	lots := []rebalanceAttributionLot{
		stockGateLot(1, 1, now.Add(-10*24*time.Hour), 600_000),
		stockGateLot(2, 1, now.Add(-9*24*time.Hour), 600_000),
	}
	level := buildSovereignStockLevels(lots, nil, channels, cfg, now)[1]
	if level.UnsoldSat != 1_200_000 || level.UnsoldLots != 2 {
		t.Fatalf("stock from 9-10 days ago must still count: %+v", level)
	}
	if !sovereignStockGateBlocks(level, 10_000_000, cfg) {
		t.Fatal("1.2M unsold over a 1M working stock must block")
	}
	// A sale 8 days after the purchase still clears that old lot.
	forwards := []rebalanceAttributionForward{{ID: 1, TargetChannelID: 1, OccurredAt: now.Add(-2 * 24 * time.Hour), AmountSat: 600_000, FeeMsat: 1}}
	sold := buildSovereignStockLevels(lots, forwards, channels, cfg, now)[1]
	if sold.UnsoldSat != 600_000 || sold.UnsoldLots != 1 {
		t.Fatalf("the late sale must consume the oldest lot: %+v", sold)
	}
	if sovereignStockGateBlocks(sold, 10_000_000, cfg) {
		t.Fatal("one unsold lot never blocks")
	}
	// Beyond twice the inventory window the lot is out of scope.
	stale := []rebalanceAttributionLot{stockGateLot(3, 1, now.Add(-15*24*time.Hour), 600_000), stockGateLot(4, 1, now.Add(-16*24*time.Hour), 600_000)}
	if got := buildSovereignStockLevels(stale, nil, channels, cfg, now)[1]; got.UnsoldSat != 0 {
		t.Fatalf("lots older than the gate window must not count: %+v", got)
	}
}
