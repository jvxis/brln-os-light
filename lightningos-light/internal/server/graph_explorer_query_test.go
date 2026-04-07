package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestNormalizeGraphNodeAddressesDeduplicatesStoredDuplicates(t *testing.T) {
	addresses := []lndclient.GraphNodeAddress{
		{Network: "tcp", Addr: "198.51.100.12:9735"},
		{Network: "tcp", Addr: "198.51.100.12:9735"},
		{Network: "torv3", Addr: "abcxyz.onion:9735"},
		{Network: "torv3", Addr: " ABCXYZ.onion:9735 "},
		{Network: "tcp", Addr: ""},
	}

	normalized := normalizeGraphNodeAddresses(addresses)
	if len(normalized) != 2 {
		t.Fatalf("expected 2 unique addresses, got %d", len(normalized))
	}
	if normalized[0].Addr != "198.51.100.12:9735" {
		t.Fatalf("unexpected first address %q", normalized[0].Addr)
	}
	if normalized[1].Addr != "abcxyz.onion:9735" {
		t.Fatalf("unexpected second address %q", normalized[1].Addr)
	}
}

func TestNormalizeGraphExplorerSearchTextRemovesNoise(t *testing.T) {
	normalized := normalizeGraphExplorerSearchText("  ⚡️Liberty⚡️   Swiss  ")
	if normalized != "liberty swiss" {
		t.Fatalf("unexpected normalized query %q", normalized)
	}
}
