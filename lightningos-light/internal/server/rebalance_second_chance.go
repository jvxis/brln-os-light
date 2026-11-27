package server

// Two cases the entry guardrails (roi_guardrail / profit_guardrail) got wrong
// by construction, both visible as targets that never leave the guardrail:
//
//  1. A measured seller. Its own realized rebalance cost already covers the
//     lot and it sold at least the lot in the window. That cost paid the
//     peer's last hop, and the gain cut the same hop again, so a channel
//     bought below its sale price still scored below 1.
//  2. A target with no cost of its own. Its cost is assumed at the reference
//     or at the whole fee budget, while the gain is at most the cold-start
//     share of the spread. Whenever the peer charges more than a small part
//     of our fee the estimate is negative, and the only way to replace the
//     assumption with a measurement — buying once — is what the guardrail
//     forbids.
//
// peer_fee_as_cost_enabled fixes (1) for every channel at once, including the
// ones that have no sale: they then pass on the cold-start prior at the full
// lot size. These helpers keep the two cases apart instead.

// sovereignMeasuredSeller reports a target whose economics need no prior: the
// channel's own realized cost covers the lot, and the volume it sold in the
// window covers it too.
func sovereignMeasuredSeller(channel RebalanceChannel, amountSat int64) bool {
	if amountSat <= 0 || channel.RebalanceCost7dPpm <= 0 {
		return false
	}
	if !hasReliableSovereignCostHistory(amountSat, channel.RebalanceAmount7dSat) {
		return false
	}
	return gainV3ObservedVolumeSat(channel.Revenue7dSat, channel.OutgoingFeePpm, channel.DrainRateSatPerHour) >= float64(amountSat)
}

// sovereignTargetEconomicsConfig returns the config the gain and the cost
// floor are estimated with. For a measured seller the peer's hop is counted
// once, on the cost side, exactly as peer_fee_as_cost_enabled does.
func sovereignTargetEconomicsConfig(cfg RebalanceConfig, channel RebalanceChannel, amountSat int64) RebalanceConfig {
	if cfg.PeerFeeAsCostEnabled || cfg.GainModelVersion < 3 {
		return cfg
	}
	if sovereignMeasuredSeller(channel, amountSat) {
		cfg.PeerFeeAsCostEnabled = true
	}
	return cfg
}

// rebalanceCandidateFailsEntryEconomics mirrors the two entry guardrails of
// buildAndOrderRebalanceCandidates.
func rebalanceCandidateFailsEntryEconomics(cfg RebalanceConfig, expectedGainSat int64, estimatedCostSat int64, expectedROI float64, roiValid bool) bool {
	if cfg.ROIMin > 0 && roiValid && expectedROI < cfg.ROIMin {
		return true
	}
	lossTolerantROI := cfg.ROIMin > 0 && cfg.ROIMin < 1
	return !lossTolerantROI && expectedGainSat > 0 && estimatedCostSat > 0 && expectedGainSat < estimatedCostSat
}

// sovereignSecondChanceEligible reports a target that may be kept for the
// exploration draw although it failed the entry guardrails: exploration is
// on, the channel has a spread to earn (gain above zero) and its cost is an
// assumption, not a measurement. A channel whose own measured cost says it
// loses stays out.
func sovereignSecondChanceEligible(cfg RebalanceConfig, channel RebalanceChannel, amountSat int64, expectedGainSat int64) bool {
	if cfg.SovereignExplorationSlotPct <= 0 || amountSat <= 0 || expectedGainSat <= 0 {
		return false
	}
	return !hasReliableSovereignCostHistory(amountSat, channel.RebalanceAmount7dSat)
}

// sovereignSecondChanceAmountSat sizes the second-chance lot like the
// exploration probe that follows an unsold batch
// (sovereignUnsoldInventoryAllowance): the operator's start amount, never
// below the execution floor or above the lot it replaces.
func sovereignSecondChanceAmountSat(cfg RebalanceConfig, amountSat int64) int64 {
	probe := effectiveStartAmountSat(cfg)
	if minimum := effectiveMinExecuteSat(cfg); probe < minimum {
		probe = minimum
	}
	if probe > 0 && probe < amountSat {
		return probe
	}
	return amountSat
}

// withoutSecondChanceOnlyTargets drops the targets held for the exploration
// draw, for consumers that mean "passed the guardrails" by "candidate".
func withoutSecondChanceOnlyTargets(candidates []rebalanceTarget) []rebalanceTarget {
	held := 0
	for _, candidate := range candidates {
		if candidate.SecondChanceOnly {
			held++
		}
	}
	if held == 0 {
		return candidates
	}
	kept := make([]rebalanceTarget, 0, len(candidates)-held)
	for _, candidate := range candidates {
		if !candidate.SecondChanceOnly {
			kept = append(kept, candidate)
		}
	}
	return kept
}
