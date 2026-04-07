package server

import (
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func TestGraphExplorerShouldImportLocalClosedChannelWhenOpenGhostExists(t *testing.T) {
	openSet := graphExplorerChannelKeySet{
		byChanID: map[uint64]struct{}{
			1017025166040694786: {},
		},
		byChanPoint: map[string]struct{}{},
	}

	ok := graphExplorerShouldImportLocalClosedChannel(lndclient.ClosedChannelInfo{
		ChanID:       1017025166040694786,
		ChannelPoint: "087a68d80db2193d8a6d12a62d3ee7eaf6e717da6dcaea655097cc1682b39366:2",
	}, time.Now().UTC(), openSet)
	if !ok {
		t.Fatal("expected ghost-open local closed channel to be imported")
	}
}

func TestGraphExplorerShouldImportLocalClosedChannelWithinCoverage(t *testing.T) {
	coverageStart := time.Date(2026, 4, 6, 8, 0, 0, 0, time.UTC)
	ok := graphExplorerShouldImportLocalClosedChannel(lndclient.ClosedChannelInfo{
		ChanID:      1017025166040694786,
		ClosedAt:    "2026-04-06T09:48:18Z",
		CloseHeight: 943896,
	}, coverageStart, graphExplorerChannelKeySet{
		byChanID:    map[uint64]struct{}{},
		byChanPoint: map[string]struct{}{},
	})
	if !ok {
		t.Fatal("expected local closed channel inside coverage window to be imported")
	}
}

func TestGraphExplorerShouldNotImportLocalClosedChannelOutsideCoverageWithoutGhost(t *testing.T) {
	coverageStart := time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC)
	ok := graphExplorerShouldImportLocalClosedChannel(lndclient.ClosedChannelInfo{
		ChanID:      1017025166040694786,
		ClosedAt:    "2026-04-06T09:48:18Z",
		CloseHeight: 943896,
	}, coverageStart, graphExplorerChannelKeySet{
		byChanID:    map[uint64]struct{}{},
		byChanPoint: map[string]struct{}{},
	})
	if ok {
		t.Fatal("expected local closed channel outside coverage window to be ignored")
	}
}
