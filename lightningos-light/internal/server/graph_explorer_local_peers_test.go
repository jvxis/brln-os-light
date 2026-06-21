package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestGraphExplorerBuildLocalOpenPeerSetDeduplicatesPubkeys(t *testing.T) {
	channels := []lndclient.ChannelInfo{
		{RemotePubkey: "02ABC"},
		{RemotePubkey: " 02abc "},
		{RemotePubkey: "03def"},
		{RemotePubkey: ""},
	}

	peers := graphExplorerBuildLocalOpenPeerSet(channels)
	if len(peers) != 2 {
		t.Fatalf("expected 2 unique local peers, got %d", len(peers))
	}
	if !graphExplorerHasLocalOpenChannel(peers, "02abc") {
		t.Fatal("expected 02abc to be marked as local open peer")
	}
	if !graphExplorerHasLocalOpenChannel(peers, "03DEF") {
		t.Fatal("expected 03DEF to be marked as local open peer")
	}
	if graphExplorerHasLocalOpenChannel(peers, "04ff") {
		t.Fatal("did not expect unrelated pubkey to be marked as local open peer")
	}
}

func TestGraphExplorerBuildLocalOpenChannelLookupKeepsChannelRefs(t *testing.T) {
	channels := []lndclient.ChannelInfo{
		{ChannelPoint: "tx1:0", ChannelID: 123, ChannelIDString: "123", RemotePubkey: "02ABC", PeerAlias: "Peer A", CapacitySat: 1000},
		{ChannelPoint: "tx2:1", ChannelID: 456, ChannelIDString: "456", RemotePubkey: "02abc", PeerAlias: "Peer A", CapacitySat: 2000},
		{ChannelPoint: "tx3:0", ChannelID: 789, RemotePubkey: "03def", CapacitySat: 3000},
	}

	lookup := graphExplorerBuildLocalOpenChannelLookup(channels)
	peerChannels := graphExplorerLocalChannelsForPeer(lookup, "02ABC")
	if len(peerChannels) != 2 {
		t.Fatalf("expected 2 local channel refs for 02abc, got %d", len(peerChannels))
	}
	if peerChannels[0].ChannelPoint != "tx1:0" || peerChannels[0].ChannelID != 123 {
		t.Fatalf("unexpected first channel ref: %+v", peerChannels[0])
	}
	if peerChannels[1].ChannelPoint != "tx2:1" || peerChannels[1].CapacitySat != 2000 {
		t.Fatalf("unexpected second channel ref: %+v", peerChannels[1])
	}
	if got := graphExplorerLocalChannelsForPeer(lookup, "04ff"); len(got) != 0 {
		t.Fatalf("expected no channel refs for unrelated peer, got %d", len(got))
	}
}
