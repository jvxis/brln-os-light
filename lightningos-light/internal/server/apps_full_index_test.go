package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestFullIndexAvailabilityAllowsManagedFallbackAfterTransportTimeout(t *testing.T) {
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte(`{"result":{}}`))
	}))
	defer rpc.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	availability, allowManagedFallback := fullIndexBitcoinConfigAvailabilityWithFallbackHint(ctx, bitcoinRPCConfig{
		Host: rpc.URL,
		User: "rpc-user",
		Pass: "rpc-pass",
	})
	if availability.Available || availability.Reason != fullIndexUnavailableBitcoinRPC || !allowManagedFallback {
		t.Fatalf("availability=%#v allowManagedFallback=%v", availability, allowManagedFallback)
	}
}

func TestFullIndexAvailabilityKeepsRPCAuthenticationFailureClosed(t *testing.T) {
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer rpc.Close()

	availability, allowManagedFallback := fullIndexBitcoinConfigAvailabilityWithFallbackHint(t.Context(), bitcoinRPCConfig{
		Host: rpc.URL,
		User: "wrong-user",
		Pass: "wrong-pass",
	})
	if availability.Available || availability.Reason != fullIndexUnavailableBitcoinRPC || allowManagedFallback {
		t.Fatalf("availability=%#v allowManagedFallback=%v", availability, allowManagedFallback)
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

func TestManagedBitcoinFullIndexStatusAvailability(t *testing.T) {
	ready := bitcoinLocalStatus{
		Installed: true, Source: "app", RPCOk: true, Blocks: 900, Headers: 900,
		VerificationProgress: 1,
	}
	if availability := managedBitcoinFullIndexStatusAvailability(ready, "server=1\ntxindex=1\n"); !availability.Available {
		t.Fatalf("ready managed Bitcoin rejected: %#v", availability)
	}

	for _, test := range []struct {
		name   string
		status bitcoinLocalStatus
		config string
		reason string
	}{
		{name: "RPC unavailable", status: bitcoinLocalStatus{Installed: true, Source: "app"}, config: "txindex=1\n", reason: fullIndexUnavailableBitcoinRPC},
		{name: "pruned", status: func() bitcoinLocalStatus { value := ready; value.Pruned = true; return value }(), config: "txindex=1\n", reason: fullIndexUnavailableUnpruned},
		{name: "syncing", status: func() bitcoinLocalStatus { value := ready; value.Blocks = 899; return value }(), config: "txindex=1\n", reason: fullIndexUnavailableBitcoinSync},
		{name: "txindex missing", status: ready, config: "server=1\n", reason: fullIndexUnavailableTxIndex},
	} {
		t.Run(test.name, func(t *testing.T) {
			availability := managedBitcoinFullIndexStatusAvailability(test.status, test.config)
			if availability.Available || availability.Reason != test.reason {
				t.Fatalf("availability=%#v want reason=%q", availability, test.reason)
			}
		})
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
