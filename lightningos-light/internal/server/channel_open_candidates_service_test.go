package server

import "testing"

func TestComputeChannelOpenCandidateScorePrefersStrongerEvidence(t *testing.T) {
	strong := &ChannelOpenCandidateItem{
		PeerPubkey:               "peer-strong",
		RouteHitCount30d:         12,
		RouteVolumeSat30d:        4_500_000,
		RouteCostToMsat30d:       1_350_000,
		RouteCostPpm30d:          channelOpenCostPpm(1_350_000, 4_500_000),
		RebalanceHitCount30d:     6,
		SharedProblemPeerCount:   3,
		SharedProblemCapacitySat: 180_000_000,
		SharedStrongPeerCount:    2,
		SharedStrongCapacitySat:  90_000_000,
		GraphChannelCount:        42,
		GraphTotalCapacitySat:    420_000_000,
		BestOutboundFeePpm:       120,
	}
	weak := &ChannelOpenCandidateItem{
		PeerPubkey:            "peer-weak",
		RouteHitCount30d:      1,
		RouteVolumeSat30d:     25_000,
		RouteCostToMsat30d:    2_000,
		RouteCostPpm30d:       channelOpenCostPpm(2_000, 25_000),
		SharedStrongPeerCount: 1,
		GraphChannelCount:     2,
		BestOutboundFeePpm:    2_000,
	}

	strongDemand, strongRelief, strongGraph, strongScore, strongConfidence := computeChannelOpenCandidateScore(strong)
	weakDemand, weakRelief, weakGraph, weakScore, weakConfidence := computeChannelOpenCandidateScore(weak)

	if strongScore <= weakScore {
		t.Fatalf("expected strong candidate score %d to exceed weak score %d", strongScore, weakScore)
	}
	if strongConfidence <= weakConfidence {
		t.Fatalf("expected strong candidate confidence %d to exceed weak confidence %d", strongConfidence, weakConfidence)
	}
	if strongDemand <= weakDemand {
		t.Fatalf("expected strong demand %d to exceed weak demand %d", strongDemand, weakDemand)
	}
	if strongRelief <= weakRelief {
		t.Fatalf("expected strong relief %d to exceed weak relief %d", strongRelief, weakRelief)
	}
	if strongGraph <= weakGraph {
		t.Fatalf("expected strong graph score %d to exceed weak graph score %d", strongGraph, weakGraph)
	}
}

func TestBuildChannelOpenCandidateReasonsIncludesExpectedSignals(t *testing.T) {
	item := &ChannelOpenCandidateItem{
		PeerPubkey:             "peer-1",
		RouteHitCount30d:       4,
		RouteCostPpm30d:        220,
		RebalanceHitCount30d:   2,
		SharedProblemPeerCount: 1,
		GraphChannelCount:      12,
		BestOutboundFeePpm:     300,
		FailedAttempts30d:      6,
	}

	reasons := buildChannelOpenCandidateReasons(item)
	codes := make(map[string]struct{}, len(reasons))
	for _, reason := range reasons {
		codes[reason.Code] = struct{}{}
	}

	expected := []string{
		"route_flow_observed",
		"route_cost_high",
		"rebalance_path_observed",
		"adjacent_to_problem_peer",
		"graph_presence_strong",
		"public_policy_competitive",
		"failed_routes_observed",
	}
	for _, code := range expected {
		if _, ok := codes[code]; !ok {
			t.Fatalf("expected reason %q in %+v", code, reasons)
		}
	}
}

func TestShouldKeepChannelOpenCandidateRejectsWeakGraphOnlyCandidate(t *testing.T) {
	item := &ChannelOpenCandidateItem{
		PeerPubkey:        "peer-graph-only",
		GraphChannelCount: 24,
		GraphQualityScore: 70,
		ReliefScore:       24,
		Score:             27,
		Confidence:        30,
	}

	if shouldKeepChannelOpenCandidate(item, true) {
		t.Fatalf("expected weak graph-only candidate to be rejected")
	}
}

func TestShouldKeepChannelOpenCandidateKeepsDirectDemandCandidate(t *testing.T) {
	item := &ChannelOpenCandidateItem{
		PeerPubkey:         "peer-direct-demand",
		RouteHitCount30d:   2,
		RouteVolumeSat30d:  250_000,
		DemandScore:        30,
		GraphQualityScore:  20,
		ReliefScore:        0,
		Score:              22,
		Confidence:         18,
		BestOutboundFeePpm: 800,
	}

	if !shouldKeepChannelOpenCandidate(item, true) {
		t.Fatalf("expected direct-demand candidate to be kept")
	}
}
