package server

import (
	"sort"
	"strings"
	"time"
)

const (
	channelCapitalActionRefill    = "refill"
	channelCapitalActionRecycle   = "recycle_source"
	channelCapitalActionReprice   = "reprice"
	channelCapitalActionExpand    = "expand"
	channelCapitalActionMaintain  = "maintain"
	channelCapitalActionObserve   = "observe"
	channelCapitalActionRotate    = "rotate"
	channelCapitalActionParked    = "parked"
	channelCapitalActionProtected = "protected"
)

type ChannelCapitalPlanAction struct {
	Code         string `json:"code"`
	TargetModule string `json:"target_module,omitempty"`
}

type ChannelCapitalPlanItem struct {
	Channel             ChannelRankingItem        `json:"channel"`
	Action              string                    `json:"action"`
	Priority            int                       `json:"priority"`
	Eligible            bool                      `json:"eligible"`
	Blockers            []string                  `json:"blockers,omitempty"`
	ObservationDays     int                       `json:"observation_days"`
	ObservationRequired int                       `json:"observation_required_days"`
	RecoverableLocalSat int64                     `json:"recoverable_local_sat"`
	PrimaryAction       *ChannelCapitalPlanAction `json:"primary_action,omitempty"`
	MagmaCommitment     *MagmaChannelCommitment   `json:"magma_commitment,omitempty"`
	AutomationReady     bool                      `json:"automation_ready"`
	AutomationBlockers  []string                  `json:"automation_blockers,omitempty"`
	TargetOutboundPct   float64                   `json:"target_outbound_pct,omitempty"`
	TargetAmountSat     int64                     `json:"target_amount_sat,omitempty"`
	ActiveRefillIntent  bool                      `json:"active_refill_intent,omitempty"`
}

type ChannelCapitalPlanSummary struct {
	TotalChannels        int            `json:"total_channels"`
	ActionCounts         map[string]int `json:"action_counts"`
	ProductiveCapitalSat int64          `json:"productive_capital_sat"`
	ProtectedCapitalSat  int64          `json:"protected_capital_sat"`
	ParkedCapitalSat     int64          `json:"parked_capital_sat"`
	RecoverableLocalSat  int64          `json:"recoverable_local_sat"`
}

type ChannelCapitalPlan struct {
	Summary ChannelCapitalPlanSummary `json:"summary"`
	Items   []ChannelCapitalPlanItem  `json:"items"`
}

func buildChannelCapitalPlan(items []ChannelRankingItem, commitments []MagmaChannelCommitment, magmaStateKnown bool) ChannelCapitalPlan {
	byPoint := make(map[string]MagmaChannelCommitment, len(commitments))
	for _, commitment := range commitments {
		point := strings.ToLower(strings.TrimSpace(commitment.ChannelPoint))
		if point != "" {
			byPoint[point] = commitment
		}
	}

	plan := ChannelCapitalPlan{
		Summary: ChannelCapitalPlanSummary{
			TotalChannels: len(items),
			ActionCounts:  map[string]int{},
		},
		Items: make([]ChannelCapitalPlanItem, 0, len(items)),
	}

	for _, channel := range items {
		item := buildChannelCapitalPlanItem(channel)
		if commitment, ok := byPoint[strings.ToLower(strings.TrimSpace(channel.ChannelPoint))]; ok {
			item.Eligible = false
			item.Action = channelCapitalActionProtected
			item.Blockers = appendUniqueCapitalPlanBlocker(item.Blockers, "magma_active")
			commitmentCopy := commitment
			item.MagmaCommitment = &commitmentCopy
		} else if !magmaStateKnown && item.Action == channelCapitalActionRotate {
			item.Eligible = false
			item.Action = channelCapitalActionProtected
			item.Blockers = appendUniqueCapitalPlanBlocker(item.Blockers, "magma_state_unavailable")
		}

		if isChannelAutomationParked(channel.AutomationMode) {
			item.Eligible = false
			item.Action = channelCapitalActionParked
			item.Blockers = appendUniqueCapitalPlanBlocker(item.Blockers, "parked")
		}

		item.Priority = channelCapitalPlanPriority(item)
		plan.Summary.ActionCounts[item.Action]++
		switch item.Action {
		case channelCapitalActionExpand, channelCapitalActionMaintain, channelCapitalActionRefill, channelCapitalActionRecycle, channelCapitalActionReprice:
			plan.Summary.ProductiveCapitalSat += rankingMaxInt64(0, channel.CapacitySat)
		case channelCapitalActionProtected:
			plan.Summary.ProtectedCapitalSat += rankingMaxInt64(0, channel.CapacitySat)
		case channelCapitalActionParked:
			plan.Summary.ParkedCapitalSat += rankingMaxInt64(0, channel.CapacitySat)
		}
		if item.Action == channelCapitalActionRotate && item.Eligible {
			plan.Summary.RecoverableLocalSat += item.RecoverableLocalSat
		}
		plan.Items = append(plan.Items, item)
	}

	sort.SliceStable(plan.Items, func(i, j int) bool {
		if plan.Items[i].Priority != plan.Items[j].Priority {
			return plan.Items[i].Priority > plan.Items[j].Priority
		}
		if plan.Items[i].Channel.Score != plan.Items[j].Channel.Score {
			return plan.Items[i].Channel.Score > plan.Items[j].Channel.Score
		}
		return strings.ToLower(plan.Items[i].Channel.PeerAlias) < strings.ToLower(plan.Items[j].Channel.PeerAlias)
	})
	return plan
}

func buildChannelCapitalPlanItem(channel ChannelRankingItem) ChannelCapitalPlanItem {
	observationDays := rankingMaxInt(0, channel.PeerSampleCount30d/24)
	if observationDays > 30 {
		observationDays = 30
	}
	item := ChannelCapitalPlanItem{
		Channel:             channel,
		Action:              channelCapitalActionObserve,
		Eligible:            true,
		ObservationDays:     observationDays,
		ObservationRequired: 7,
		RecoverableLocalSat: rankingMaxInt64(0, channel.LocalBalanceSat),
	}

	if !channelRankingHasFull7dObservation(channel.PeerSampleCount30d) {
		item.Eligible = false
		item.Blockers = append(item.Blockers, "observation_7d")
		return item
	}

	switch strings.TrimSpace(channel.State) {
	case "expand":
		if channelCapitalPlanNeedsRefill(channel) {
			item.Action = channelCapitalActionRefill
			item.PrimaryAction = &ChannelCapitalPlanAction{Code: "manual_rebalance", TargetModule: "rebalance"}
		} else {
			item.Action = channelCapitalActionExpand
			item.PrimaryAction = &ChannelCapitalPlanAction{Code: "review_expansion", TargetModule: "lightning-ops"}
		}
	case "maintain":
		if channelCapitalPlanNeedsRefill(channel) {
			item.Action = channelCapitalActionRefill
			item.PrimaryAction = &ChannelCapitalPlanAction{Code: "manual_rebalance", TargetModule: "rebalance"}
		} else if channelCapitalPlanNeedsSourceRecycle(channel) {
			item.Action = channelCapitalActionRecycle
			item.PrimaryAction = &ChannelCapitalPlanAction{Code: "review_source_liquidity", TargetModule: "rebalance-sources"}
		} else if channelCapitalPlanNeedsReprice(channel) {
			item.Action = channelCapitalActionReprice
			item.PrimaryAction = &ChannelCapitalPlanAction{Code: "review_fees", TargetModule: "autofee"}
		} else {
			item.Action = channelCapitalActionMaintain
		}
	case "close":
		item.Action = channelCapitalActionRotate
		item.ObservationRequired = 30
		item.PrimaryAction = &ChannelCapitalPlanAction{Code: "prepare_coop_close", TargetModule: "close-manager"}
		if !channelRankingHasFull30dObservation(channel.PeerSampleCount30d) {
			item.Eligible = false
			item.Blockers = append(item.Blockers, "observation_30d")
		}
	case "monitor":
		if channelCapitalPlanNeedsSourceRecycle(channel) {
			item.Action = channelCapitalActionRecycle
			item.PrimaryAction = &ChannelCapitalPlanAction{Code: "review_source_liquidity", TargetModule: "rebalance-sources"}
		} else if channelCapitalPlanNeedsReprice(channel) {
			item.Action = channelCapitalActionReprice
			item.PrimaryAction = &ChannelCapitalPlanAction{Code: "review_fees", TargetModule: "autofee"}
		} else {
			item.Action = channelCapitalActionObserve
		}
	}
	return item
}

func channelCapitalPlanNeedsRefill(channel ChannelRankingItem) bool {
	state := strings.TrimSpace(strings.ToLower(channel.LiquidityState))
	stateFresh := true
	if channel.LiquidityStateAt != nil && !channel.LiquidityStateAt.IsZero() {
		reference := channel.ComputedAt
		if reference.IsZero() {
			reference = time.Now()
		}
		stateFresh = !channel.LiquidityStateAt.Before(reference.Add(-12 * time.Hour))
	}
	return (stateFresh && (state == autofeeLiquidityStateDrained || state == autofeeLiquidityStateExtremeDrained)) ||
		(channel.LocalBalancePct < 25 && channel.ProfitFee7dSat > 0)
}

// A source reservoir can look deceptively healthy in the economic score: it
// assists other channels and has no rebalance cost, while most of its capital
// remains local. Surface the operational role without ever turning the
// recommendation into an automatic close.
func channelCapitalPlanNeedsSourceRecycle(channel ChannelRankingItem) bool {
	return applySourceReservoirCapitalPenalty(100, channel.CapacitySat, channel.LocalBalancePct,
		channelTrafficStat{AmountSat: channel.ForwardAmt7dSat, FeeSat: channel.ForwardFee7dSat},
		channelTrafficStat{AmountSat: channel.AssistedForwardAmt7dSat, FeeSat: channel.AssistedForwardFee7dSat}, 7) < 100
}

func enrichChannelCapitalPlanAutomation(plan *ChannelCapitalPlan, channels []RebalanceChannel, intents map[uint64][]AutomationIntent) {
	if plan == nil {
		return
	}
	byID := make(map[uint64]RebalanceChannel, len(channels))
	byPoint := make(map[string]RebalanceChannel, len(channels))
	for _, channel := range channels {
		byID[channel.ChannelID] = channel
		byPoint[strings.ToLower(strings.TrimSpace(channel.ChannelPoint))] = channel
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Action != channelCapitalActionRefill {
			continue
		}
		channel, ok := byID[uint64(item.Channel.ChannelID)]
		if !ok {
			channel, ok = byPoint[strings.ToLower(strings.TrimSpace(item.Channel.ChannelPoint))]
		}
		if !ok {
			item.AutomationBlockers = append(item.AutomationBlockers, "rebalance_state_unavailable")
			continue
		}
		item.TargetOutboundPct = channel.TargetOutboundPct
		item.TargetAmountSat = channel.TargetAmountSat
		if !channel.AutoEnabled && !channel.ManualRestartEnabled {
			item.AutomationBlockers = append(item.AutomationBlockers, "rebalance_not_automated")
		}
		if channel.TargetAmountSat <= 0 {
			item.AutomationBlockers = append(item.AutomationBlockers, "target_satisfied")
		}
		if !channel.EligibleAsTarget {
			item.AutomationBlockers = append(item.AutomationBlockers, "rebalance_target_ineligible")
		}
		item.AutomationReady = len(item.AutomationBlockers) == 0
		item.ActiveRefillIntent = selectRefillTargetIntent(intents[channel.ChannelID], 0) != nil
	}
}

func channelCapitalPlanNeedsReprice(channel ChannelRankingItem) bool {
	for _, recommendation := range channel.Recommendations {
		switch strings.TrimSpace(recommendation.Code) {
		case "review_autofee_bounds", "review_fee_positioning":
			return true
		}
	}
	return false
}

func channelCapitalPlanPriority(item ChannelCapitalPlanItem) int {
	score := clampInt(item.Channel.Score, 0, 100)
	switch item.Action {
	case channelCapitalActionRotate:
		if item.Eligible {
			return 600 + (100 - score)
		}
		return 100
	case channelCapitalActionRefill:
		return 500 + score
	case channelCapitalActionRecycle:
		return 450 + score
	case channelCapitalActionExpand:
		return 400 + score
	case channelCapitalActionReprice:
		return 300 + score
	case channelCapitalActionMaintain:
		return 200 + score
	case channelCapitalActionObserve:
		return 100 + score
	default:
		return score
	}
}

func appendUniqueCapitalPlanBlocker(blockers []string, blocker string) []string {
	blocker = strings.TrimSpace(blocker)
	if blocker == "" {
		return blockers
	}
	for _, current := range blockers {
		if current == blocker {
			return blockers
		}
	}
	return append(blockers, blocker)
}
