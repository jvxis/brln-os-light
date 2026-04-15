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

	strongScore, strongConfidence := computeChannelOpenCandidateScore(strong)
	weakScore, weakConfidence := computeChannelOpenCandidateScore(weak)

	if strongScore <= weakScore {
		t.Fatalf("expected strong candidate score %d to exceed weak score %d", strongScore, weakScore)
	}
	if strongConfidence <= weakConfidence {
		t.Fatalf("expected strong candidate confidence %d to exceed weak confidence %d", strongConfidence, weakConfidence)
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
