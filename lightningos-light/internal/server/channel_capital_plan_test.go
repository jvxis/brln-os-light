package server

import (
	"testing"
	"time"
)

func TestBuildChannelCapitalPlanUsesRankingWithoutChangingIt(t *testing.T) {
	channel := ChannelRankingItem{
		ChannelPoint:       "funding:0",
		PeerAlias:          "productive-drained",
		State:              "expand",
		Score:              88,
		CapacitySat:        5_000_000,
		LocalBalanceSat:    200_000,
		LocalBalancePct:    4,
		ProfitFee7dSat:     1_200,
		PeerSampleCount30d: channelRankingFull30dObservationSamples,
		LiquidityState:     autofeeLiquidityStateDrained,
		AutomationMode:     channelAutomationModeNormal,
	}

	plan := buildChannelCapitalPlan([]ChannelRankingItem{channel}, nil, true)
	if len(plan.Items) != 1 {
		t.Fatalf("expected one plan item, got %d", len(plan.Items))
	}
	item := plan.Items[0]
	if item.Action != channelCapitalActionRefill || !item.Eligible {
		t.Fatalf("expected eligible refill, got action=%q eligible=%v", item.Action, item.Eligible)
	}
	if item.Channel.State != "expand" || item.Channel.Score != 88 {
		t.Fatalf("economic ranking was changed: state=%q score=%d", item.Channel.State, item.Channel.Score)
	}
	if item.PrimaryAction == nil || item.PrimaryAction.TargetModule != "rebalance" {
		t.Fatalf("expected rebalance primary action, got %#v", item.PrimaryAction)
	}
}

func TestBuildChannelCapitalPlanProtectsMagmaAndParkedChannels(t *testing.T) {
	base := ChannelRankingItem{
		ChannelPoint:       "sold:1",
		State:              "close",
		Score:              12,
		CapacitySat:        10_000_000,
		LocalBalanceSat:    7_000_000,
		PeerSampleCount30d: channelRankingFull30dObservationSamples,
		AutomationMode:     channelAutomationModeNormal,
	}
	commitment := MagmaChannelCommitment{OrderID: "order-1", ChannelPoint: base.ChannelPoint}
	plan := buildChannelCapitalPlan([]ChannelRankingItem{base}, []MagmaChannelCommitment{commitment}, true)
	item := plan.Items[0]
	if item.Action != channelCapitalActionProtected || item.Eligible {
		t.Fatalf("expected protected Magma channel, got action=%q eligible=%v", item.Action, item.Eligible)
	}
	if item.MagmaCommitment == nil || item.MagmaCommitment.OrderID != "order-1" {
		t.Fatalf("expected Magma commitment metadata, got %#v", item.MagmaCommitment)
	}
	if plan.Summary.RecoverableLocalSat != 0 || plan.Summary.ProtectedCapitalSat != base.CapacitySat {
		t.Fatalf("protected capital was accounted incorrectly: %#v", plan.Summary)
	}

	base.AutomationMode = channelAutomationModeParked
	parked := buildChannelCapitalPlan([]ChannelRankingItem{base}, []MagmaChannelCommitment{commitment}, true).Items[0]
	if parked.Action != channelCapitalActionParked || parked.Eligible {
		t.Fatalf("expected Parked policy to remain the effective operator state, got action=%q eligible=%v", parked.Action, parked.Eligible)
	}
	if parked.MagmaCommitment == nil {
		t.Fatal("Parked channel must still expose its Magma restriction")
	}
}

func TestBuildChannelCapitalPlanFailsClosedForCloseWhenMagmaUnknown(t *testing.T) {
	channel := ChannelRankingItem{
		ChannelPoint:       "candidate:2",
		State:              "close",
		Score:              9,
		LocalBalanceSat:    1_500_000,
		PeerSampleCount30d: channelRankingFull30dObservationSamples,
	}
	item := buildChannelCapitalPlan([]ChannelRankingItem{channel}, nil, false).Items[0]
	if item.Action != channelCapitalActionProtected || item.Eligible {
		t.Fatalf("expected unknown Magma state to protect close candidate, got action=%q eligible=%v", item.Action, item.Eligible)
	}
	if len(item.Blockers) != 1 || item.Blockers[0] != "magma_state_unavailable" {
		t.Fatalf("unexpected blockers: %#v", item.Blockers)
	}
}

func TestBuildChannelCapitalPlanRequiresInitialObservation(t *testing.T) {
	channel := ChannelRankingItem{
		ChannelPoint:       "young:0",
		State:              "expand",
		Score:              90,
		PeerSampleCount30d: channelRankingFull7dObservationSamples - 1,
	}
	item := buildChannelCapitalPlan([]ChannelRankingItem{channel}, nil, true).Items[0]
	if item.Action != channelCapitalActionObserve || item.Eligible {
		t.Fatalf("expected observation gate, got action=%q eligible=%v", item.Action, item.Eligible)
	}
	if len(item.Blockers) != 1 || item.Blockers[0] != "observation_7d" {
		t.Fatalf("unexpected blockers: %#v", item.Blockers)
	}
}

func TestBuildChannelCapitalPlanRecognizesUsefulSourceReservoir(t *testing.T) {
	channel := ChannelRankingItem{
		ChannelPoint:            "source:0",
		State:                   "maintain",
		Score:                   61,
		CapacitySat:             22_000_000,
		LocalBalanceSat:         18_000_000,
		LocalBalancePct:         81.8,
		ForwardAmt7dSat:         0,
		AssistedForwardAmt7dSat: 766_506,
		PeerSampleCount30d:      channelRankingFull30dObservationSamples,
	}
	item := buildChannelCapitalPlan([]ChannelRankingItem{channel}, nil, true).Items[0]
	if item.Action != channelCapitalActionRecycle || !item.Eligible {
		t.Fatalf("expected source recycle recommendation, got action=%q eligible=%v", item.Action, item.Eligible)
	}
	if item.PrimaryAction == nil || item.PrimaryAction.Code != "review_source_liquidity" {
		t.Fatalf("unexpected primary action: %#v", item.PrimaryAction)
	}
}

func TestChannelCapitalPlanIgnoresStaleAutofeeDrainState(t *testing.T) {
	computedAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	stateAt := computedAt.Add(-24 * time.Hour)
	channel := ChannelRankingItem{
		State: "maintain", CapacitySat: 5_000_000, LocalBalancePct: 60,
		LiquidityState: autofeeLiquidityStateDrained, LiquidityStateAt: &stateAt,
		ComputedAt: computedAt, PeerSampleCount30d: channelRankingFull30dObservationSamples,
	}
	if item := buildChannelCapitalPlanItem(channel); item.Action == channelCapitalActionRefill {
		t.Fatalf("stale liquidity state produced refill: %#v", item)
	}
}

func TestEnrichChannelCapitalPlanSeparatesRecommendationFromAutomationReadiness(t *testing.T) {
	plan := ChannelCapitalPlan{Items: []ChannelCapitalPlanItem{{
		Channel: ChannelRankingItem{ChannelID: 42, ChannelPoint: "target:0"},
		Action:  channelCapitalActionRefill,
	}}}
	intent := AutomationIntent{ChannelID: 42, Kind: automationIntentKindRefillTarget, Confidence: .9}
	enrichChannelCapitalPlanAutomation(&plan, []RebalanceChannel{{
		ChannelID: 42, ChannelPoint: "target:0", AutoEnabled: true, EligibleAsTarget: true,
		TargetOutboundPct: 20, TargetAmountSat: 500_000,
	}}, map[uint64][]AutomationIntent{42: {intent}})
	item := plan.Items[0]
	if !item.AutomationReady || !item.ActiveRefillIntent || item.TargetAmountSat != 500_000 {
		t.Fatalf("expected actionable refill metadata, got %#v", item)
	}

	plan.Items[0].AutomationBlockers = nil
	enrichChannelCapitalPlanAutomation(&plan, []RebalanceChannel{{
		ChannelID: 42, ChannelPoint: "target:0", AutoEnabled: true, EligibleAsTarget: false,
	}}, nil)
	if plan.Items[0].AutomationReady || len(plan.Items[0].AutomationBlockers) == 0 {
		t.Fatalf("strategic recommendation must expose automation blockers: %#v", plan.Items[0])
	}
}
