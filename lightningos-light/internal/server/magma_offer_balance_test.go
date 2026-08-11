package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Amboss never decrements total_size as orders land: what an offer can still
// sell only exists as total_size minus what its orders locked. Without it an
// offer reads "6,000,000 sat" forever, including the day it can no longer
// produce a single valid order.
func TestMagmaOfferRemainingIsDerivedFromLockedSize(t *testing.T) {
	// Verbatim shape of the live account on 2026-08-07: one enabled offer with
	// 4,584,070 of 6,000,000 sat already sold, and one long since exhausted.
	payload := `{"data":{"getUser":{"market":{"offers":{"list":[
      {"id":"d7628296-a1b5-4c34-9527-77a3eab7886a","status":"ENABLED","side":"SELL",
       "total_size":"6000000","min_size":"1000000","max_size":"3000000",
       "fee_rate":2400,"base_fee":0,"fee_rate_cap":900,"base_fee_cap":1,
       "min_block_length":4320,"onchain_priority":"HIGH","onchain_multiplier":2,
       "orders":{"locked_size":"4584070"},"conditions":[]},
      {"id":"f0beb0ff-b79c-4cf4-ba0b-7e2ad55e6c78","status":"DISABLED","side":"SELL",
       "total_size":"31500000","min_size":"3000000","max_size":"16000000",
       "fee_rate":2000,"base_fee":0,"fee_rate_cap":900,"base_fee_cap":1,
       "min_block_length":4320,"orders":{"locked_size":"31500000"},"conditions":[]}
    ]}}}}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	client := &magmaAmbossClient{endpoint: srv.URL, http: srv.Client()}
	offers, err := client.Offers(context.Background(), "token")
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if len(offers) != 2 {
		t.Fatalf("expected 2 offers, got %d", len(offers))
	}

	active := offers[0]
	if active.SoldSat != 4_584_070 {
		t.Fatalf("sold: want 4584070, got %d", active.SoldSat)
	}
	if active.RemainingSat != 1_415_930 {
		t.Fatalf("remaining: want 1415930, got %d", active.RemainingSat)
	}
	// total_size must survive untouched: it is what the operator advertised, and
	// the edit dialog writes it straight back to Amboss.
	if active.TotalSizeSat != 6_000_000 {
		t.Fatalf("total_size must not be rewritten, got %d", active.TotalSizeSat)
	}
	if sold := offers[1]; sold.RemainingSat != 0 {
		t.Fatalf("a fully sold offer has nothing left, got %d", sold.RemainingSat)
	}
}

// An offer with less left than its own minimum is finished, whatever its status
// says. Saying so beats waiting for orders that can no longer be created.
func TestMagmaOfferConflictsReportExhaustion(t *testing.T) {
	policy := defaultMagmaPolicy()
	policy.MinChannelSizeSat = 1_000_000
	policy.MaxChannelSizeSat = 3_000_000
	policy.MinPricePPM, policy.MinPricePPMPerDay, policy.MinRevenueSat = 0, 0, 0
	policy.MaxCommitmentDays = 60

	base := MagmaOffer{
		MinSizeSat: 1_000_000, MaxSizeSat: 3_000_000, TotalSizeSat: 6_000_000,
		FeeRatePPM: 2400, FeeRateCapPPM: 900, MinBlockLength: 4320,
	}

	t.Run("the live offer only warns that the ceiling moved", func(t *testing.T) {
		offer := base
		offer.SoldSat, offer.RemainingSat = 4_584_070, 1_415_930
		for _, c := range magmaOfferConflicts(offer, policy) {
			if c.Blocking {
				t.Fatalf("1,415,930 sat still clears the 1,000,000 minimum: %q", c.Message)
			}
		}
	})

	t.Run("below its own minimum the offer is blocked", func(t *testing.T) {
		offer := base
		offer.SoldSat, offer.RemainingSat = 5_200_000, 800_000
		blocking := false
		for _, c := range magmaOfferConflicts(offer, policy) {
			if c.Blocking {
				blocking = true
			}
		}
		if !blocking {
			t.Fatal("800,000 sat left against a 1,000,000 sat minimum sells nothing")
		}
	})

	t.Run("an untouched offer says nothing about its balance", func(t *testing.T) {
		offer := base
		offer.SoldSat, offer.RemainingSat = 0, 6_000_000
		if got := len(magmaOfferConflicts(offer, policy)); got != 0 {
			t.Fatalf("a fresh offer inside the policy is clean, got %d conflict(s)", got)
		}
	})
}

// Guards the wire contract: the derived fields must never be sent back to Amboss.
func TestMagmaOfferInputOmitsDerivedBalance(t *testing.T) {
	input := MagmaOffer{
		TotalSizeSat: 6_000_000, MinSizeSat: 1_000_000, MaxSizeSat: 3_000_000,
		FeeRatePPM: 2400, FeeRateCapPPM: 900, BaseFeeCapSat: 1,
		MinBlockLength: 4320, FixedFeeMode: magmaFixedFeeManual,
		// Set so the test would catch them being forwarded.
		SoldSat: 4_584_070, RemainingSat: 1_415_930,
	}.offerInput()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{"sold_sat", "remaining_sat", "locked_size", "orders"} {
		if strings.Contains(string(encoded), banned) {
			t.Fatalf("%q is derived by Amboss and must not be written back: %s", banned, encoded)
		}
	}
}
