package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestGraphExplorerSnapshotChannelIDs(t *testing.T) {
	channels := []lndclient.GraphChannel{
		{ChannelID: 11},
		{ChannelID: 0},
		{ChannelID: 22},
		{ChannelID: 11},
	}

	got := graphExplorerSnapshotChannelIDs(channels)
	if len(got) != 2 || got[0] != 11 || got[1] != 22 {
		t.Fatalf("graphExplorerSnapshotChannelIDs() = %v, want [11 22]", got)
	}
}

func TestGraphExplorerSnapshotChannelIDsRejectsEmptySnapshot(t *testing.T) {
	channels := []lndclient.GraphChannel{{ChannelID: 0}}
	if got := graphExplorerSnapshotChannelIDs(channels); len(got) != 0 {
		t.Fatalf("graphExplorerSnapshotChannelIDs() = %v, want empty", got)
	}
}
