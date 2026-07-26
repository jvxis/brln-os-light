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
