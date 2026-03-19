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
	if len(list) != 3 {
		t.Fatalf("expected 3 recommendations after thresholds, got %d", len(list))
	}

	if list[0].PubKey != "peer-a" {
		t.Fatalf("expected peer-a first due to lowest inbound fee, got %s", list[0].PubKey)
	}
	if list[0].TotalCapacitySat != 120_000_000 {
		t.Fatalf("expected peer-a total capacity 120000000, got %d", list[0].TotalCapacitySat)
	}
	if list[1].PubKey != "peer-c" {
		t.Fatalf("expected peer-c second due to higher capacity among equal inbound, got %s", list[1].PubKey)
	}
	if list[2].PubKey != "peer-b" {
		t.Fatalf("expected peer-b third, got %s", list[2].PubKey)
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
