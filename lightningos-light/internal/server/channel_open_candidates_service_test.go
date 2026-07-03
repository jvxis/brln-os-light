package server

import (
	"context"
	"testing"

	"lightningos-light/internal/lndclient"
)

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

func TestComputeChannelOpenCandidateScorePenalizesFailedAttempts(t *testing.T) {
	base := &ChannelOpenCandidateItem{
		PeerPubkey:             "peer-1",
		RouteHitCount30d:       4,
		RouteVolumeSat30d:      1_000_000,
		RouteCostToMsat30d:     300_000,
		RouteCostPpm30d:        channelOpenCostPpm(300_000, 1_000_000),
		RebalanceHitCount30d:   2,
		SharedProblemPeerCount: 2,
		SharedStrongPeerCount:  1,
		GraphChannelCount:      18,
		BestOutboundFeePpm:     150,
	}
	withFailures := *base
	withFailures.FailedAttempts30d = 9

	baseDemand, baseRelief, baseGraph, baseScore, baseConfidence := computeChannelOpenCandidateScore(base)
	failedDemand, failedRelief, failedGraph, failedScore, failedConfidence := computeChannelOpenCandidateScore(&withFailures)

	if failedScore >= baseScore {
		t.Fatalf("expected failed-attempt score %d to be below base score %d", failedScore, baseScore)
	}
	if failedDemand >= baseDemand {
		t.Fatalf("expected failed-attempt demand %d to be below base demand %d", failedDemand, baseDemand)
	}
	if failedRelief != baseRelief {
		t.Fatalf("expected relief unaffected by failed attempts, got %d want %d", failedRelief, baseRelief)
	}
	if failedGraph != baseGraph {
		t.Fatalf("expected graph score unaffected by failed attempts, got %d want %d", failedGraph, baseGraph)
	}
	if failedConfidence != baseConfidence {
		t.Fatalf("expected confidence unaffected by failed attempts, got %d want %d", failedConfidence, baseConfidence)
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

func TestFilterChannelOpenCandidatesForLocalPeersRemovesOpenAndPendingPeers(t *testing.T) {
	localPeers := map[string]struct{}{
		"02open":    {},
		"03pending": {},
	}
	items := []ChannelOpenCandidateItem{
		{PeerPubkey: "02OPEN", Score: 90},
		{PeerPubkey: "03pending", Score: 80},
		{PeerPubkey: "02keep", Score: 70},
	}

	filtered := filterChannelOpenCandidatesForLocalPeers(items, localPeers)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 candidate after local peer filter, got %d: %+v", len(filtered), filtered)
	}
	if filtered[0].PeerPubkey != "02keep" {
		t.Fatalf("unexpected remaining candidate: %+v", filtered[0])
	}
}

func TestDeleteChannelOpenCandidatesForLocalPeersRemovesMapEntries(t *testing.T) {
	items := map[string]*ChannelOpenCandidateItem{
		"02open": {PeerPubkey: "02open"},
		"02keep": {PeerPubkey: "02keep"},
	}

	deleteChannelOpenCandidatesForLocalPeers(items, map[string]struct{}{"02open": {}})
	if _, ok := items["02open"]; ok {
		t.Fatalf("expected local peer candidate to be deleted")
	}
	if _, ok := items["02keep"]; !ok {
		t.Fatalf("expected non-local candidate to remain")
	}
}

func TestLoadLocalChannelPeerSetIncludesOpenAndPendingChannels(t *testing.T) {
	svc := &ChannelOpenCandidatesService{
		lnd: &fakeChannelOpenCandidatesLND{
			channels: []lndclient.ChannelInfo{
				{RemotePubkey: "02OPEN"},
				{RemotePubkey: ""},
			},
			pending: []lndclient.PendingChannelInfo{
				{RemotePubkey: "03Pending", Status: "opening"},
			},
		},
	}

	peers := svc.loadLocalChannelPeerSet(context.Background())
	if _, ok := peers["02open"]; !ok {
		t.Fatalf("expected open channel peer in local set: %+v", peers)
	}
	if _, ok := peers["03pending"]; !ok {
		t.Fatalf("expected pending channel peer in local set: %+v", peers)
	}
}

type fakeChannelOpenCandidatesLND struct {
	channels []lndclient.ChannelInfo
	pending  []lndclient.PendingChannelInfo
}

func (f *fakeChannelOpenCandidatesLND) CachedPubkey() string {
	return ""
}

func (f *fakeChannelOpenCandidatesLND) GetStatus(ctx context.Context) (lndclient.Status, error) {
	return lndclient.Status{}, nil
}

func (f *fakeChannelOpenCandidatesLND) ListChannels(ctx context.Context) ([]lndclient.ChannelInfo, error) {
	return f.channels, nil
}

func (f *fakeChannelOpenCandidatesLND) ListPendingChannels(ctx context.Context) ([]lndclient.PendingChannelInfo, error) {
	return f.pending, nil
}
