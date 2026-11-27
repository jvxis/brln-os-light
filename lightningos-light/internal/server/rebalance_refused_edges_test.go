package server

import (
	"testing"
	"time"

	"lightningos-light/lnrpc"
)

func refusedEdgeIDs(edges []*lnrpc.EdgeLocator) []uint64 {
	ids := make([]uint64, 0, len(edges))
	for _, edge := range edges {
		ids = append(ids, edge.ChannelId)
	}
	return ids
}

func TestRefusedEdgeMemoryFirstRefusalLastsTheSeed(t *testing.T) {
	m := newRefusedEdgeMemory()
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	seed := 30 * time.Minute
	m.note(&lnrpc.EdgeLocator{ChannelId: 7, DirectionReverse: true}, start, seed)

	got := m.active(start.Add(29*time.Minute), 10)
	if len(got) != 1 || got[0].ChannelId != 7 || !got[0].DirectionReverse {
		t.Fatalf("expected the refused direction to be remembered inside the seed, got %+v", got)
	}
	if got := m.active(start.Add(31*time.Minute), 10); len(got) != 0 {
		t.Fatalf("expected the direction to be retested after the seed, got %+v", got)
	}
}

func TestRefusedEdgeMemoryRetestHoldsForTimeSeenDry(t *testing.T) {
	m := newRefusedEdgeMemory()
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	seed := 30 * time.Minute
	edge := &lnrpc.EdgeLocator{ChannelId: 7}
	m.note(edge, start, seed)

	// The next job for the same target comes 135 minutes later, long after
	// the seed, retests the direction and is refused again: it has been dry
	// for 135 minutes and is held for another 135.
	retest := start.Add(135 * time.Minute)
	if got := m.active(retest, 10); len(got) != 0 {
		t.Fatalf("expected the direction to be open for a retest, got %+v", got)
	}
	m.note(edge, retest, seed)
	if got := m.active(retest.Add(134*time.Minute), 10); len(got) != 1 {
		t.Fatalf("expected a hold as long as the time seen dry, got %+v", got)
	}
	if got := m.active(retest.Add(136*time.Minute), 10); len(got) != 0 {
		t.Fatalf("expected the hold to end after the time seen dry, got %+v", got)
	}

	// Third refusal 300 minutes after the first: held for 300 minutes.
	third := start.Add(300 * time.Minute)
	m.note(edge, third, seed)
	if got := m.active(third.Add(299*time.Minute), 10); len(got) != 1 {
		t.Fatalf("expected the hold to keep growing with the time seen dry, got %+v", got)
	}
}

func TestRefusedEdgeMemoryForgetsAfterTheAgingHorizon(t *testing.T) {
	m := newRefusedEdgeMemory()
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	seed := 30 * time.Minute
	edge := &lnrpc.EdgeLocator{ChannelId: 7}
	m.note(edge, start, seed)

	// Unrefused for longer than the horizon: the history is gone and a new
	// refusal starts over from the seed instead of holding for days.
	later := start.Add(permanentFailScoreHalfLife + time.Hour)
	m.note(edge, later, seed)
	if got := m.active(later.Add(29*time.Minute), 10); len(got) != 1 {
		t.Fatalf("expected the new refusal to be remembered, got %+v", got)
	}
	if got := m.active(later.Add(31*time.Minute), 10); len(got) != 0 {
		t.Fatalf("expected a forgotten direction to restart from the seed, got %+v", got)
	}
	if got := m.active(later.Add(permanentFailScoreHalfLife+time.Hour), 10); len(got) != 0 || len(m.entries) != 0 {
		t.Fatalf("expected forgotten entries to be dropped, got %+v and %d entries", got, len(m.entries))
	}
}

func TestRefusedEdgeMemoryClearsDirectionsAProbeGotThrough(t *testing.T) {
	self := "02self"
	route := &lnrpc.Route{Hops: []*lnrpc.Hop{
		{ChanId: 10, PubKey: "02aa"},
		{ChanId: 20, PubKey: "03bb"},
		{ChanId: 30, PubKey: "03cc"},
		{ChanId: 40, PubKey: self},
	}}
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	seed := time.Hour
	fill := func() *refusedEdgeMemory {
		m := newRefusedEdgeMemory()
		m.note(rebalanceProbeFailedEdge(route, 1, self), start, seed)
		m.note(rebalanceProbeFailedEdge(route, 2, self), start, seed)
		return m
	}

	// Refused at the third hop: the second hop forwarded, the third did not.
	m := fill()
	m.clearForwarded(route, 2, self)
	got := refusedEdgeIDs(m.active(start.Add(time.Minute), 10))
	if len(got) != 1 || got[0] != 30 {
		t.Fatalf("expected only the hop before the refusal to be cleared, got %v", got)
	}

	// The probe found an amount: every third-party hop forwarded.
	m = fill()
	m.clearForwarded(route, -1, self)
	if got := m.active(start.Add(time.Minute), 10); len(got) != 0 {
		t.Fatalf("expected the whole route to be cleared, got %+v", got)
	}

	// A cleared direction starts over from the seed.
	m.note(rebalanceProbeFailedEdge(route, 1, self), start.Add(10*time.Hour), seed)
	if got := m.active(start.Add(11*time.Hour+time.Minute), 10); len(got) != 0 {
		t.Fatalf("expected a cleared direction to restart from the seed, got %+v", got)
	}
}

func TestRefusedEdgeMemoryNeedsASeed(t *testing.T) {
	m := newRefusedEdgeMemory()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	m.note(&lnrpc.EdgeLocator{ChannelId: 7}, now, 0)
	m.note(nil, now, time.Hour)
	m.note(&lnrpc.EdgeLocator{}, now, time.Hour)
	if got := m.active(now, 10); len(got) != 0 {
		t.Fatalf("expected nothing remembered without a seed or an edge, got %+v", got)
	}
	var missing *refusedEdgeMemory
	missing.note(&lnrpc.EdgeLocator{ChannelId: 7}, now, time.Hour)
	if got := missing.active(now, 10); got != nil {
		t.Fatalf("expected a nil memory to be inert, got %+v", got)
	}
}

func TestRefusedEdgeMemoryKeepsDirectionsApartAndNewestFirst(t *testing.T) {
	m := newRefusedEdgeMemory()
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	seed := time.Hour
	m.note(&lnrpc.EdgeLocator{ChannelId: 1}, start, seed)
	m.note(&lnrpc.EdgeLocator{ChannelId: 2}, start.Add(time.Minute), seed)
	m.note(&lnrpc.EdgeLocator{ChannelId: 3}, start.Add(2*time.Minute), seed)
	m.note(&lnrpc.EdgeLocator{ChannelId: 3, DirectionReverse: true}, start.Add(3*time.Minute), seed)

	got := m.active(start.Add(10*time.Minute), 10)
	if len(got) != 4 {
		t.Fatalf("expected both directions of a channel to be separate entries, got %+v", got)
	}
	if !got[0].DirectionReverse || got[0].ChannelId != 3 {
		t.Fatalf("expected the newest refusal first, got %+v", got[0])
	}
	limited := refusedEdgeIDs(m.active(start.Add(10*time.Minute), 2))
	if len(limited) != 2 || limited[0] != 3 || limited[1] != 3 {
		t.Fatalf("expected the limit to keep the newest refusals, got %v", limited)
	}
}

func TestRebalanceProbeFailureOnOwnChannel(t *testing.T) {
	route := &lnrpc.Route{Hops: []*lnrpc.Hop{
		{ChanId: 10, PubKey: "02aa"},
		{ChanId: 20, PubKey: "03bb"},
		{ChanId: 30, PubKey: "02SELF"},
	}}
	if !rebalanceProbeFailureOnOwnChannel(route, 0, "02self") {
		t.Fatalf("the first hop is this node's source channel")
	}
	if rebalanceProbeFailureOnOwnChannel(route, 1, "02self") {
		t.Fatalf("a middle hop is a third-party channel")
	}
	if !rebalanceProbeFailureOnOwnChannel(route, 2, "02self") {
		t.Fatalf("the hop paying back into this node is the target channel")
	}
	if !rebalanceProbeFailureOnOwnChannel(route, 3, "02self") || !rebalanceProbeFailureOnOwnChannel(nil, 0, "02self") {
		t.Fatalf("an unusable index must never be remembered")
	}
}
