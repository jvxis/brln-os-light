package lndclient

import (
	"testing"

	"lightningos-light/lnrpc"
)

func TestRouteEndsInChannel(t *testing.T) {
	route := &lnrpc.Route{Hops: []*lnrpc.Hop{
		{ChanId: 11},
		{ChanId: 22},
	}}

	if !routeEndsInChannel(route, 22) {
		t.Fatal("expected route to end in channel 22")
	}
	if routeEndsInChannel(route, 11) {
		t.Fatal("route prefix must not count as the target channel")
	}
	if routeEndsInChannel(nil, 22) {
		t.Fatal("nil route must not match")
	}
}

func TestRouteLastEdgeLocatorsOnlyExcludeParallelSibling(t *testing.T) {
	route := &lnrpc.Route{Hops: []*lnrpc.Hop{
		{ChanId: 11},
		{ChanId: 22},
		{ChanId: 33},
	}}

	got := routeLastEdgeLocators(route)
	if len(got) != 2 {
		t.Fatalf("last-edge locators = %d, want 2", len(got))
	}
	for _, locator := range got {
		if locator.ChannelId != 33 {
			t.Fatalf("excluded channel = %d, want only final channel 33", locator.ChannelId)
		}
	}
	if got[0].DirectionReverse == got[1].DirectionReverse {
		t.Fatal("both channel directions must be excluded")
	}
}

func TestRouteLastEdgeLocatorsRejectMalformedRoute(t *testing.T) {
	tests := []*lnrpc.Route{
		nil,
		{},
		{Hops: []*lnrpc.Hop{nil}},
		{Hops: []*lnrpc.Hop{{ChanId: 0}}},
	}
	for i, route := range tests {
		if got := routeLastEdgeLocators(route); len(got) != 0 {
			t.Fatalf("case %d returned %d locators, want none", i, len(got))
		}
	}
}

func TestExactTargetRouteHintsUsesRemoteToLocalPolicy(t *testing.T) {
	const (
		local  = "02local"
		remote = "03remote"
	)
	edge := &lnrpc.ChannelEdge{
		ChannelId: 77,
		Node1Pub:  local,
		Node2Pub:  remote,
		Node1Policy: &lnrpc.RoutingPolicy{
			FeeBaseMsat:      999,
			FeeRateMilliMsat: 888,
			TimeLockDelta:    70,
		},
		Node2Policy: &lnrpc.RoutingPolicy{
			FeeBaseMsat:      123,
			FeeRateMilliMsat: 456,
			TimeLockDelta:    40,
		},
	}

	hints := exactTargetRouteHints(edge, remote, local)
	if len(hints) != 1 || len(hints[0].HopHints) != 1 {
		t.Fatalf("unexpected route hints: %+v", hints)
	}
	hint := hints[0].HopHints[0]
	if hint.NodeId != remote || hint.ChanId != 77 {
		t.Fatalf("wrong exact edge: %+v", hint)
	}
	if hint.FeeBaseMsat != 123 || hint.FeeProportionalMillionths != 456 || hint.CltvExpiryDelta != 40 {
		t.Fatalf("wrong remote-to-local policy: %+v", hint)
	}
}

func TestExactTargetRouteHintsRejectsUnrelatedEdge(t *testing.T) {
	edge := &lnrpc.ChannelEdge{
		ChannelId:   77,
		Node1Pub:    "node-a",
		Node2Pub:    "node-b",
		Node1Policy: &lnrpc.RoutingPolicy{},
		Node2Policy: &lnrpc.RoutingPolicy{},
	}
	if got := exactTargetRouteHints(edge, "node-c", "node-d"); len(got) != 0 {
		t.Fatalf("unexpected hints for unrelated edge: %+v", got)
	}
}

func TestClampRouteHintUint32(t *testing.T) {
	if got := clampRouteHintUint32(-1); got != 0 {
		t.Fatalf("negative clamp = %d, want 0", got)
	}
	if got := clampRouteHintUint32(123); got != 123 {
		t.Fatalf("normal clamp = %d, want 123", got)
	}
	if got := clampRouteHintUint32(int64(^uint32(0)) + 1); got != ^uint32(0) {
		t.Fatalf("overflow clamp = %d, want %d", got, ^uint32(0))
	}
}
