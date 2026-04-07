package server

import (
	"reflect"
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestGraphExplorerCollectAffectedPubKeysFromChannelUpdates(t *testing.T) {
	result := graphExplorerCollectAffectedPubKeysFromChannelUpdates([]lndclient.GraphChannelUpdate{
		{
			AdvertisingNode: "02BB",
			ConnectingNode:  "02aa",
		},
		{
			AdvertisingNode: "02aa",
			ConnectingNode:  "02BB",
		},
		{
			AdvertisingNode: "  ",
			ConnectingNode:  "02CC",
		},
	})

	expected := []string{"02aa", "02bb", "02cc"}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("unexpected pubkeys: got %v want %v", result, expected)
	}
}

func TestGraphExplorerCollectClosedChannelIDs(t *testing.T) {
	result := graphExplorerCollectClosedChannelIDs([]lndclient.GraphClosedChannelUpdate{
		{ChannelID: 0},
		{ChannelID: 101},
		{ChannelID: 202},
		{ChannelID: 101},
	})

	expected := []uint64{101, 202}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("unexpected chan ids: got %v want %v", result, expected)
	}
}
