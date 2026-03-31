package lndclient

import (
	"testing"

	"lightningos-light/lnrpc"
)

func TestConvertGraphNodeAddressesDeduplicatesByAddress(t *testing.T) {
	items := []*lnrpc.NodeAddress{
		{Network: "tcp", Addr: "203.0.113.10:9735"},
		{Network: "tcp", Addr: "203.0.113.10:9735"},
		{Network: "tcp", Addr: " 203.0.113.10:9735 "},
		{Network: "torv3", Addr: "exampleonionaddress.onion:9735"},
		{Network: "tcp", Addr: ""},
		nil,
	}

	addresses := convertGraphNodeAddresses(items)
	if len(addresses) != 2 {
		t.Fatalf("expected 2 unique addresses, got %d", len(addresses))
	}
	if addresses[0].Addr != "203.0.113.10:9735" {
		t.Fatalf("expected first address to be normalized, got %q", addresses[0].Addr)
	}
	if addresses[1].Addr != "exampleonionaddress.onion:9735" {
		t.Fatalf("expected second address to be preserved, got %q", addresses[1].Addr)
	}
}
