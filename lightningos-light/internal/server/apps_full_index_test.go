package server

import (
	"net/http/httptest"
	"testing"
)

func TestFullIndexAvailabilityAcceptsNativeLocalBitcoinRPC(t *testing.T) {
	rpc := httptest.NewServer(bitcoinRPCMockHandler(t, true, false, false))
	defer rpc.Close()
	availability := fullIndexBitcoinConfigAvailability(t.Context(), bitcoinRPCConfig{
		Host: rpc.URL,
		User: "rpc-user",
		Pass: "rpc-pass",
	})
	if !availability.Available {
		t.Fatalf("native local Bitcoin RPC rejected: %#v", availability)
	}
}

func TestFullIndexAvailabilityRejectsUnsafeNativeBitcoinModes(t *testing.T) {
	for _, test := range []struct {
		name    string
		txIndex bool
		pruned  bool
		syncing bool
		reason  string
	}{
		{name: "pruned", txIndex: true, pruned: true, reason: fullIndexUnavailableUnpruned},
		{name: "syncing", txIndex: true, syncing: true, reason: fullIndexUnavailableBitcoinSync},
		{name: "txindex missing", reason: fullIndexUnavailableTxIndex},
	} {
		t.Run(test.name, func(t *testing.T) {
			rpc := httptest.NewServer(bitcoinRPCMockHandler(t, test.txIndex, test.pruned, test.syncing))
			defer rpc.Close()
			availability := fullIndexBitcoinConfigAvailability(t.Context(), bitcoinRPCConfig{Host: rpc.URL, User: "u", Pass: "p"})
			if availability.Available || availability.Reason != test.reason {
				t.Fatalf("availability=%#v, want reason %q", availability, test.reason)
			}
		})
	}
}

func TestParseBitcoinCoreBool(t *testing.T) {
	raw := `
server=1
txindex=1
# txindex=0
`
	if !parseBitcoinCoreBool(raw, "txindex") {
		t.Fatal("expected txindex=true")
	}
}

func TestParseBitcoinCoreBoolFalseWhenMissing(t *testing.T) {
	if parseBitcoinCoreBool("server=1\n", "txindex") {
		t.Fatal("expected txindex=false")
	}
}

func TestParseBitcoinCoreTxIndexInfoSynced(t *testing.T) {
	ready, known, err := parseBitcoinCoreTxIndexInfo(`{"txindex":{"synced":true,"best_block_height":892044}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !known || !ready {
		t.Fatalf("expected known and ready, got known=%v ready=%v", known, ready)
	}
}

func TestParseBitcoinCoreTxIndexInfoRPCEnvelopeSynced(t *testing.T) {
	ready, known, err := parseBitcoinCoreTxIndexInfo(`{"result":{"txindex":{"synced":true,"best_block_height":892044}}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !known || !ready {
		t.Fatalf("expected known and ready, got known=%v ready=%v", known, ready)
	}
}

func TestParseBitcoinCoreTxIndexInfoMissing(t *testing.T) {
	ready, known, err := parseBitcoinCoreTxIndexInfo(`{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if known || ready {
		t.Fatalf("expected missing txindex, got known=%v ready=%v", known, ready)
	}
}
