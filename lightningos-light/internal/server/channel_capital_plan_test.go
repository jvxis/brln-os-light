package server

import "testing"

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
