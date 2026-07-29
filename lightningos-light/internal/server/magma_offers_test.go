package server

import (
	"strings"
	"testing"
)

// A bad offer is published to a public marketplace, so it is worth failing here
// rather than sending it and reading back an opaque GraphQL error.
func TestMagmaOfferValidate(t *testing.T) {
	valid := MagmaOffer{
		TotalSizeSat: 2_000_000, MinSizeSat: 1_000_000, MaxSizeSat: 1_000_000,
		FeeRatePPM: 2998, BaseFeeSat: 568,
		FeeRateCapPPM: 900, BaseFeeCapSat: 1, MinBlockLength: 8640,
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("the reference offer should validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*MagmaOffer)
		expect string
	}{
		{"no stock", func(o *MagmaOffer) { o.TotalSizeSat = 0 }, "total size"},
		{"no minimum size", func(o *MagmaOffer) { o.MinSizeSat = 0 }, "minimum channel size must be positive"},
		{
			name:   "max below min",
			mutate: func(o *MagmaOffer) { o.MinSizeSat = 5_000_000; o.MaxSizeSat = 1_000_000 },
			expect: "cannot be below the minimum",
		},
		{
			// Advertising a channel bigger than the stock can never be filled.
			name:   "minimum channel larger than the stock",
			mutate: func(o *MagmaOffer) { o.TotalSizeSat = 500_000 },
			expect: "cannot exceed the total size",
		},
		{"no duration", func(o *MagmaOffer) { o.MinBlockLength = 0 }, "duration"},
		{
			name: "unknown condition",
			mutate: func(o *MagmaOffer) {
				o.Conditions = []MagmaOfferCondition{{Condition: "NODE_AGE", Operator: "EQUAL_TO", Value: "1"}}
			},
			expect: "unknown buyer condition",
		},
		{
			name: "unknown operator",
			mutate: func(o *MagmaOffer) {
				o.Conditions = []MagmaOfferCondition{{Condition: "NODE_CAPACITY", Operator: "ABOVE", Value: "1"}}
			},
			expect: "unknown condition operator",
		},
		{
			name: "condition without a value",
			mutate: func(o *MagmaOffer) {
				o.Conditions = []MagmaOfferCondition{{Condition: "NODE_CAPACITY", Operator: "GREATER_THAN", Value: "  "}}
			},
			expect: "needs a value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			offer := valid
			tc.mutate(&offer)
			err := offer.validate()
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tc.expect) {
				t.Fatalf("error %q does not mention %q", err, tc.expect)
			}
		})
	}
}

// Manual and automatic are mutually exclusive on the wire, mirroring the form.
// These two fields were missing from the first version of offerInput: since an
// update sends the whole offer, omitting them would have quietly reset an
// automatic offer to a manual fee of whatever base_fee happened to hold — and 34
// of 35 real offers on this account are automatic.
func TestMagmaOfferInputFixedFeeMode(t *testing.T) {
	base := MagmaOffer{TotalSizeSat: 2_000_000, MinSizeSat: 1_000_000, MinBlockLength: 4320}

	t.Run("automatic sends priority and multiplier, not the number", func(t *testing.T) {
		offer := base
		offer.FixedFeeMode = magmaFixedFeeAutomatic
		offer.OnchainPriority = "HIGH"
		offer.OnchainMultiplier = 2
		offer.BaseFeeSat = 1136 // whatever Amboss last computed
		input := offer.offerInput()
		if input["onchain_priority"] != "HIGH" || input["onchain_multiplier"] != int64(2) {
			t.Fatalf("automatic must carry priority and multiplier: %#v", input)
		}
		if _, present := input["base_fee"]; present {
			t.Fatal("a computed base fee must not be sent back as a manual one")
		}
		if err := offer.validate(); err != nil {
			t.Fatalf("valid automatic offer rejected: %v", err)
		}
	})

	t.Run("manual sends the number, not priority", func(t *testing.T) {
		offer := base
		offer.FixedFeeMode = magmaFixedFeeManual
		offer.BaseFeeSat = 568
		input := offer.offerInput()
		if input["base_fee"] != int64(568) {
			t.Fatalf("manual must carry the fixed fee: %#v", input["base_fee"])
		}
		for _, key := range []string{"onchain_priority", "onchain_multiplier"} {
			if _, present := input[key]; present {
				t.Fatalf("%s must be absent in manual mode", key)
			}
		}
	})

	t.Run("automatic rejects a bad priority or multiplier", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			priority   string
			multiplier int64
			expect     string
		}{
			{"unknown priority", "URGENT", 2, "unknown mempool priority"},
			{"multiplier below range", "HIGH", 0, "between 1 and 5"},
			{"multiplier above range", "HIGH", 6, "between 1 and 5"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				offer := base
				offer.FixedFeeMode = magmaFixedFeeAutomatic
				offer.OnchainPriority = tc.priority
				offer.OnchainMultiplier = tc.multiplier
				err := offer.validate()
				if err == nil {
					t.Fatal("expected a validation error")
				}
				if !strings.Contains(err.Error(), tc.expect) {
					t.Fatalf("error %q does not mention %q", err, tc.expect)
				}
			})
		}
	})

	// Manual mode must not be validated against the automatic enums.
	t.Run("manual ignores priority validation", func(t *testing.T) {
		offer := base
		offer.FixedFeeMode = magmaFixedFeeManual
		offer.OnchainPriority = "GARBAGE"
		if err := offer.validate(); err != nil {
			t.Fatalf("manual mode should not check the priority: %v", err)
		}
	})
}

// Conditions are always sent, including empty, so clearing them in the form
// actually clears them instead of leaving the previous set live on Amboss.
func TestMagmaOfferInputAlwaysSendsConditions(t *testing.T) {
	offer := MagmaOffer{TotalSizeSat: 1_000_000, MinSizeSat: 1_000_000, MinBlockLength: 4320}
	input := offer.offerInput()
	conditions, ok := input["conditions"].([]map[string]any)
	if !ok {
		t.Fatalf("conditions must always be present, got %T", input["conditions"])
	}
	if len(conditions) != 0 {
		t.Fatalf("expected an empty list, got %d entries", len(conditions))
	}
	// max_size is omitted rather than sent as zero, which Amboss would read as a
	// real ceiling of zero.
	if _, present := input["max_size"]; present {
		t.Fatal("max_size must be omitted when unset")
	}
}

// The whole point of the conflict report: a policy tighter than the offer accepts
// nothing, and neither screen says so on its own.
func TestMagmaOfferConflicts(t *testing.T) {
	offer := MagmaOffer{
		TotalSizeSat: 2_000_000, MinSizeSat: 1_000_000, MaxSizeSat: 1_000_000,
		FeeRatePPM: 2998, FeeRateCapPPM: 900, MinBlockLength: 8640,
	}

	t.Run("aligned policy reports nothing", func(t *testing.T) {
		policy := MagmaPolicy{
			MinChannelSizeSat: 1_000_000, MaxChannelSizeSat: 1_000_000,
			MaxCommitmentDays: 60, MaxDailySizeSat: 2_000_000,
		}
		if got := magmaOfferConflicts(offer, policy); len(got) != 0 {
			t.Fatalf("expected no conflicts, got %+v", got)
		}
	})

	blocking := []struct {
		name   string
		policy MagmaPolicy
		expect string
	}{
		{
			name:   "policy maximum below the smallest channel sold",
			policy: MagmaPolicy{MaxChannelSizeSat: 500_000},
			expect: "every order would be rejected",
		},
		{
			name:   "policy minimum above the largest channel sold",
			policy: MagmaPolicy{MinChannelSizeSat: 5_000_000},
			expect: "every order would be rejected",
		},
		{
			name:   "commitment longer than the policy allows",
			policy: MagmaPolicy{MaxCommitmentDays: 30},
			expect: "commits the channel for 60 days",
		},
		{
			name:   "offer priced below the policy floor",
			policy: MagmaPolicy{MinPricePPM: 5000},
			expect: "below the policy minimum of 5000 ppm",
		},
		{
			name:   "routing ceiling below the policy floor",
			policy: MagmaPolicy{MinFeeRateCapPPM: 1000},
			expect: "caps our routing fee",
		},
		{
			// 2998 ppm over 60 days is ~50 ppm/day.
			name:   "price per day below the policy floor",
			policy: MagmaPolicy{MinPricePPMPerDay: 100},
			expect: "ppm/day",
		},
		{
			name:   "one channel exceeds the daily cap",
			policy: MagmaPolicy{MaxDailySizeSat: 500_000},
			expect: "daily cap",
		},
	}
	for _, tc := range blocking {
		t.Run(tc.name, func(t *testing.T) {
			got := magmaOfferConflicts(offer, tc.policy)
			if len(got) == 0 {
				t.Fatal("expected a conflict")
			}
			found := false
			for _, conflict := range got {
				if strings.Contains(conflict.Message, tc.expect) {
					found = true
					if !conflict.Blocking {
						t.Errorf("conflict %q should be blocking", conflict.Message)
					}
				}
			}
			if !found {
				t.Fatalf("no conflict mentioned %q; got %+v", tc.expect, got)
			}
		})
	}

	// A partial overlap still sells, so it warns instead of blocking.
	t.Run("partial overlap warns without blocking", func(t *testing.T) {
		wide := MagmaOffer{
			TotalSizeSat: 20_000_000, MinSizeSat: 500_000, MaxSizeSat: 10_000_000,
			FeeRatePPM: 2998, MinBlockLength: 8640,
		}
		policy := MagmaPolicy{MinChannelSizeSat: 1_000_000, MaxChannelSizeSat: 5_000_000}
		got := magmaOfferConflicts(wide, policy)
		if len(got) == 0 {
			t.Fatal("expected warnings about the trimmed range")
		}
		for _, conflict := range got {
			if conflict.Blocking {
				t.Fatalf("a partial overlap must not block: %q", conflict.Message)
			}
		}
	})

	// An empty policy imposes nothing, so it cannot conflict with anything.
	t.Run("empty policy never conflicts", func(t *testing.T) {
		if got := magmaOfferConflicts(offer, MagmaPolicy{}); len(got) != 0 {
			t.Fatalf("expected no conflicts, got %+v", got)
		}
	})
}
