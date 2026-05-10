package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestApplyPeerAliasLookupsPreservesKnownAliasAndFallsBack(t *testing.T) {
	peers := []lndclient.PeerInfo{
		{PubKey: "02aaa", Alias: " channel alias "},
		{PubKey: "03bbb"},
		{PubKey: "04cccccccccccccccc"},
	}
	lookups := map[string]GraphExplorerNodeLookup{
		"03bbb": {PubKey: "03bbb", Alias: "Graph Alias"},
	}

	enriched := applyPeerAliasLookups(peers, lookups, true)

	if enriched[0].Alias != "channel alias" {
		t.Fatalf("expected existing alias to be trimmed and preserved, got %q", enriched[0].Alias)
	}
	if enriched[1].Alias != "Graph Alias" {
		t.Fatalf("expected graph alias, got %q", enriched[1].Alias)
	}
	if enriched[2].Alias != "04cccccccccc" {
		t.Fatalf("expected short pubkey fallback, got %q", enriched[2].Alias)
	}
	if peers[0].Alias != " channel alias " {
		t.Fatalf("expected input slice to remain unchanged")
	}
}

func TestApplyNetworkMapGraphLookupsUsesAliasAndClearnetSocket(t *testing.T) {
	states := map[string]*networkMapPeerState{
		"02aaa": {PubKey: "02aaa"},
		"03bbb": {PubKey: "03bbb"},
	}
	lookups := map[string]GraphExplorerNodeLookup{
		"02aaa": {
			PubKey: "02aaa",
			Alias:  "Graph Alias",
			Addresses: []lndclient.GraphNodeAddress{
				{Network: "torv3", Addr: "abcxyz.onion:9735"},
				{Network: "tcp", Addr: "198.51.100.10:9735"},
			},
		},
		"03bbb": {
			PubKey: "03bbb",
			Addresses: []lndclient.GraphNodeAddress{
				{Network: "torv3", Addr: "onlytor.onion:9735"},
			},
		},
	}

	applyNetworkMapGraphLookups(states, lookups)

	if states["02aaa"].Alias != "Graph Alias" {
		t.Fatalf("expected graph alias, got %q", states["02aaa"].Alias)
	}
	if states["02aaa"].Socket != "198.51.100.10:9735" {
		t.Fatalf("expected clearnet socket, got %q", states["02aaa"].Socket)
	}
	if !states["02aaa"].GraphHasOnion {
		t.Fatalf("expected onion presence to be tracked")
	}
	if states["03bbb"].Socket != "" || !states["03bbb"].GraphHasOnion {
		t.Fatalf("expected tor-only graph lookup to avoid clearnet socket and mark onion")
	}
}
