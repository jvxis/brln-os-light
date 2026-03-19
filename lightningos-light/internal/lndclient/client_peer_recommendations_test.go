package lndclient

import (
	"testing"

	"lightningos-light/lnrpc"
)

func TestBuildPeerNeighborRecommendationsRanksByInboundAndCapacity(t *testing.T) {
	source := "source"
	edges := []*lnrpc.ChannelEdge{
		{
			Node1Pub: "source",
			Node2Pub: "peer-a",
			Capacity: 900000,
			Node2Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: -40,
				InboundFeeBaseMsat:      0,
				FeeRateMilliMsat:        15,
				FeeBaseMsat:             100,
			},
		},
		{
			Node1Pub: "source",
			Node2Pub: "peer-a",
			Capacity: 600000,
			Node2Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: -20,
				InboundFeeBaseMsat:      10,
				FeeRateMilliMsat:        10,
				FeeBaseMsat:             50,
			},
		},
		{
			Node1Pub: "peer-b",
			Node2Pub: "source",
			Capacity: 1500000,
			Node1Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: -10,
				InboundFeeBaseMsat:      0,
				FeeRateMilliMsat:        5,
				FeeBaseMsat:             0,
			},
		},
		{
			Node1Pub: "source",
			Node2Pub: "peer-c",
			Capacity: 1500000,
			Node2Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: -10,
				InboundFeeBaseMsat:      0,
				FeeRateMilliMsat:        25,
				FeeBaseMsat:             0,
			},
		},
	}

	list := buildPeerNeighborRecommendations(source, edges, nil)
	if len(list) != 3 {
		t.Fatalf("expected 3 recommendations, got %d", len(list))
	}

	if list[0].PubKey != "peer-a" {
		t.Fatalf("expected peer-a first due to lower inbound fee, got %s", list[0].PubKey)
	}
	if list[0].InboundFeeRatePpm != -40 {
		t.Fatalf("expected peer-a inbound fee -40, got %d", list[0].InboundFeeRatePpm)
	}
	if list[0].OutboundFeeRatePpm != 10 {
		t.Fatalf("expected peer-a outbound fee min 10, got %d", list[0].OutboundFeeRatePpm)
	}
	if list[0].TotalCapacitySat != 1500000 {
		t.Fatalf("expected peer-a total capacity 1500000, got %d", list[0].TotalCapacitySat)
	}

	if list[1].PubKey != "peer-b" {
		t.Fatalf("expected peer-b second due to same inbound fee but lower outbound than peer-c, got %s", list[1].PubKey)
	}
	if list[2].PubKey != "peer-c" {
		t.Fatalf("expected peer-c third, got %s", list[2].PubKey)
	}
}

func TestBuildPeerNeighborRecommendationsSkipsExcludedAndDisabled(t *testing.T) {
	source := "source"
	edges := []*lnrpc.ChannelEdge{
		{
			Node1Pub: "source",
			Node2Pub: "keep",
			Capacity: 500000,
			Node2Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: 5,
				FeeRateMilliMsat:        10,
			},
		},
		{
			Node1Pub: "source",
			Node2Pub: "excluded",
			Capacity: 700000,
			Node2Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: -10,
				FeeRateMilliMsat:        10,
			},
		},
		{
			Node1Pub: "source",
			Node2Pub: "disabled",
			Capacity: 900000,
			Node2Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: -100,
				FeeRateMilliMsat:        1,
				Disabled:                true,
			},
		},
		{
			Node1Pub: "other-a",
			Node2Pub: "other-b",
			Capacity: 900000,
			Node1Policy: &lnrpc.RoutingPolicy{
				InboundFeeRateMilliMsat: -100,
				FeeRateMilliMsat:        1,
			},
		},
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
