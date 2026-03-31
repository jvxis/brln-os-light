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
