package lndclient

import (
	"testing"

	"lightningos-light/lnrpc"
)

func addNeighborEdges(edges []*lnrpc.ChannelEdge, source, peer string, count int, capacity int64, inboundRate int32, outboundRate int64) []*lnrpc.ChannelEdge {
	for i := 0; i < count; i++ {
		edges = append(edges, &lnrpc.ChannelEdge{
			Node1Pub: source,
			Node2Pub: peer,
			Capacity: capacity,
			Node2Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: inboundRate,
				FeeRateMilliMsat:        outboundRate,
			},
		})
	}
	return edges
}

func TestBuildPeerNeighborRecommendationsAppliesMinimumThresholdsAndSorts(t *testing.T) {
	source := "source"
	var edges []*lnrpc.ChannelEdge

	edges = addNeighborEdges(edges, source, "peer-a", 12, 10_000_000, -40, 50)
	edges = addNeighborEdges(edges, source, "peer-b", 11, 12_000_000, -10, 10)
	edges = addNeighborEdges(edges, source, "peer-c", 15, 9_000_000, -10, 25)
	edges = addNeighborEdges(edges, source, "too-small", 12, 5_000_000, -200, 1)
	edges = addNeighborEdges(edges, source, "too-few", 10, 20_000_000, -200, 1)

	list := buildPeerNeighborRecommendations(source, edges, nil)
	if len(list) != 5 {
		t.Fatalf("expected 5 aggregated recommendations before tier filtering, got %d", len(list))
	}

	if list[0].PubKey != "too-few" {
		t.Fatalf("expected too-few first due to lowest inbound fee before tier filtering, got %s", list[0].PubKey)
	}
	if list[1].PubKey != "too-small" {
		t.Fatalf("expected too-small second due to next-lowest inbound fee before tier filtering, got %s", list[1].PubKey)
	}
	if list[2].PubKey != "peer-a" {
		t.Fatalf("expected peer-a third among aggregated candidates, got %s", list[2].PubKey)
	}

	selected, tier := selectPeerNeighborRecommendations(list, 5)
	if tier != "strict" {
		t.Fatalf("expected strict tier, got %s", tier)
	}
	if len(selected) != 3 {
		t.Fatalf("expected 3 strict recommendations, got %d", len(selected))
	}
	if selected[0].PubKey != "peer-a" {
		t.Fatalf("expected peer-a first in strict tier, got %s", selected[0].PubKey)
	}
	if selected[1].PubKey != "peer-c" {
		t.Fatalf("expected peer-c second in strict tier, got %s", selected[1].PubKey)
	}
	if selected[2].PubKey != "peer-b" {
		t.Fatalf("expected peer-b third in strict tier, got %s", selected[2].PubKey)
	}
}

func TestSelectPeerNeighborRecommendationsFallsBackWhenStrictIsEmpty(t *testing.T) {
	recommendations := []PeerNeighborRecommendation{
		{PubKey: "strict-miss-a", TotalCapacitySat: 90_000_000, ChannelCount: 10, HasClearnet: true},
		{PubKey: "balanced-a", TotalCapacitySat: 70_000_000, ChannelCount: 8, HasClearnet: true},
		{PubKey: "balanced-b", TotalCapacitySat: 55_000_000, ChannelCount: 6, HasClearnet: false},
	}

	selected, tier := selectPeerNeighborRecommendations(recommendations, 5)
	if tier != "fallback_balanced" {
		t.Fatalf("expected fallback_balanced tier, got %s", tier)
	}
	if len(selected) != 3 {
		t.Fatalf("expected 3 balanced recommendations, got %d", len(selected))
	}
	if selected[0].PubKey != "strict-miss-a" {
		t.Fatalf("expected strict-miss-a first in balanced fallback, got %s", selected[0].PubKey)
	}
}

func TestSelectPeerNeighborRecommendationsUsesStrictWhenAvailable(t *testing.T) {
	recommendations := []PeerNeighborRecommendation{
		{PubKey: "strict-a", TotalCapacitySat: 150_000_000, ChannelCount: 12, HasClearnet: true},
		{PubKey: "balanced-a", TotalCapacitySat: 70_000_000, ChannelCount: 8, HasClearnet: true},
	}

	selected, tier := selectPeerNeighborRecommendations(recommendations, 5)
	if tier != "strict" {
		t.Fatalf("expected strict tier, got %s", tier)
	}
	if len(selected) != 1 {
		t.Fatalf("expected only strict recommendations, got %d", len(selected))
	}
	if selected[0].PubKey != "strict-a" {
		t.Fatalf("expected strict-a, got %s", selected[0].PubKey)
	}
}

func TestBuildPeerNeighborRecommendationsSkipsExcludedAndDisabled(t *testing.T) {
	source := "source"
	var edges []*lnrpc.ChannelEdge

	edges = addNeighborEdges(edges, source, "keep", 12, 10_000_000, 5, 10)
	edges = addNeighborEdges(edges, source, "excluded", 12, 10_000_000, -10, 10)
	for i := 0; i < 12; i++ {
		edges = append(edges, &lnrpc.ChannelEdge{
			Node1Pub: source,
			Node2Pub: "disabled",
			Capacity: 10_000_000,
			Node2Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: -100,
				FeeRateMilliMsat:        1,
				Disabled:                true,
			},
		})
	}

	excluded := map[string]struct{}{
		"excluded": {},
	}

	list := buildPeerNeighborRecommendations(source, edges, excluded)
	if len(list) != 1 {
		t.Fatalf("expected 1 recommendation after filtering, got %d", len(list))
	}
	if list[0].PubKey != "keep" {
		t.Fatalf("expected remaining recommendation to be keep, got %s", list[0].PubKey)
	}
}

func TestSelectPreferredNodeAddressPrefersClearnet(t *testing.T) {
	host, hasClearnet := selectPreferredNodeAddress([]*lnrpc.NodeAddress{
		{Addr: "abcdef.onion:9735"},
		{Addr: "203.0.113.10:9735"},
	})
	if !hasClearnet {
		t.Fatal("expected clearnet to be detected")
	}
	if host != "203.0.113.10:9735" {
		t.Fatalf("expected clearnet host, got %s", host)
	}

	host, hasClearnet = selectPreferredNodeAddress([]*lnrpc.NodeAddress{
		{Addr: "onlytor.onion:9735"},
	})
	if hasClearnet {
		t.Fatal("did not expect clearnet on onion-only addresses")
	}
	if host != "onlytor.onion:9735" {
		t.Fatalf("expected onion fallback, got %s", host)
	}
}
