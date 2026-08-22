package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestGraphChannelSnapshotRowsFiltersAndNormalizes(t *testing.T) {
	channelID := uint64(840_000)<<40 | 7
	channels := []lndclient.GraphChannel{
		{},
		{
			ChannelID:   channelID,
			ChanPoint:   "  txid:0  ",
			Node1PubKey: "  node-1  ",
			Node2PubKey: "  node-2  ",
			CapacitySat: 125_000,
		},
		{
			ChannelID:   channelID,
			ChanPoint:   "duplicate:1",
			Node1PubKey: "duplicate-1",
			Node2PubKey: "duplicate-2",
			CapacitySat: 1,
		},
	}

	rows := graphChannelSnapshotRows(channels)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if got := row[0]; got != int64(channelID) {
		t.Fatalf("channel id = %v, want %d", got, channelID)
	}
	if got := row[1]; got != "txid:0" {
		t.Fatalf("channel point = %v, want txid:0", got)
	}
	if got := row[2]; got != "node-1" {
		t.Fatalf("node 1 = %v, want node-1", got)
	}
	if got := row[3]; got != "node-2" {
		t.Fatalf("node 2 = %v, want node-2", got)
	}
	if got := row[4]; got != int64(125_000) {
		t.Fatalf("capacity = %v, want 125000", got)
	}
	if got := row[5]; got != 840_000 {
		t.Fatalf("open block height = %v, want 840000", got)
	}
}

func TestGraphChannelSnapshotRowsEmpty(t *testing.T) {
	if rows := graphChannelSnapshotRows(nil); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
}
