package server

import "time"

const sovereignUnsoldInventoryUnavailableReason = "paid_liquidity_inventory_unavailable"

// The stock guard measures actual purchases, never the channel's strategic
// deficit. Older and non-sovereign lots still consume forwards FIFO, but only
// Sovereign lots contribute to its automatic replenishment guard.
func buildSovereignUnsoldInventory(lots []rebalanceAttributionLot, forwards []rebalanceAttributionForward, cfg RebalanceConfig, now time.Time) map[uint64]sovereignUnsoldLiquidityStat {
	window := sovereignUnsoldInventoryWindow(cfg)
	since := now.Add(-window)
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
	attributed := attributeRebalanceForwardsFIFO(validLots, validForwards, window)
	stats := make(map[uint64]sovereignUnsoldLiquidityStat)
	lastPaid := make(map[uint64]time.Time)
	for _, lot := range validLots {
		if lot.JobID <= 0 || lot.TargetChannelID == 0 || lot.SentSat <= 0 || !lot.CompletedAt.After(since) || lot.TriggerReason != rebalanceSovereignReason {
			continue
		}
		if lot.CompletedAt.After(lastPaid[lot.TargetChannelID]) {
			lastPaid[lot.TargetChannelID] = lot.CompletedAt
		}
		attr := attributed[lot.JobID]
		// Retain the existing material-sale/payback thresholds, but apply them
		// per FIFO lot. A small, recovered lot cannot clear a different purchase.
		if attr.ForwardAmountSat*100 >= lot.SentSat*sovereignUnsoldPaidLiquidityMinForwardPct ||
			(lot.FeePaidSat > 0 && attr.ForwardFeeMsat*100 >= lot.FeePaidSat*1000*sovereignUnsoldPaidLiquidityMinFeePaybackPct) {
			continue
		}
		stat := stats[lot.TargetChannelID]
		if stat.CompletedAt.IsZero() || lot.CompletedAt.Before(stat.CompletedAt) {
			stat.CompletedAt = lot.CompletedAt
		}
		stat.SentSat += lot.SentSat
		stat.TargetAmountSat += lot.SentSat // actual paid cohort, including historical partials
		stat.FeePaidSat += lot.FeePaidSat
		stat.ForwardAmountSat += attr.ForwardAmountSat
		stat.ForwardFeeMsat += attr.ForwardFeeMsat
		stat.ForwardFeeSat = stat.ForwardFeeMsat / 1000
		stats[lot.TargetChannelID] = stat
	}
	for id, stat := range stats {
		stat.LastPaidAt = lastPaid[id]
		stats[id] = stat
	}
	return stats
}

func sovereignUnsoldInventoryWindow(cfg RebalanceConfig) time.Duration {
	window := time.Duration(sovereignSlowSellerWindowHoursForConfig(cfg)) * time.Hour
	if window < sovereignUnsoldPaidLiquidityLookback {
		window = sovereignUnsoldPaidLiquidityLookback
	}
	return window
}

// Observation time is not permission to buy another full batch. Both ordinary
// selection and exploration wait after a paid batch that has not sold. Once
// observation ends, exploration can test again at the operator's start minimum
// (never below the execution floor or above the original batch). Operator/guaranteed
// slots do not use this gate.
func sovereignUnsoldInventoryAllowance(stat sovereignUnsoldLiquidityStat, cfg RebalanceConfig, now time.Time, exploration bool, amount int64) (int64, string) {
	if stat.Unavailable {
		return 0, sovereignUnsoldInventoryUnavailableReason
	}
	if now.IsZero() {
		now = time.Now()
	}
	if !hasSovereignUnsoldPaidLiquidity(stat, cfg, now) {
		return amount, ""
	}
	lastPaid := stat.LastPaidAt
	if lastPaid.IsZero() {
		lastPaid = stat.CompletedAt
	}
	if now.Sub(lastPaid) < sovereignUnsoldPaidLiquidityHardAge || (!exploration && shouldSkipSovereignUnsoldPaidLiquidity(stat, cfg, now)) {
		return 0, sovereignUnsoldPaidLiquidityReason
	}
	if exploration {
		probe := effectiveStartAmountSat(cfg)
		if minimum := effectiveMinExecuteSat(cfg); probe < minimum {
			probe = minimum
		}
		if probe > 0 && probe < amount {
			return probe, ""
		}
	}
	return amount, ""
}
