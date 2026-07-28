package server

import (
	"strings"
	"testing"
)

// magmaGoodOrder is an order that passes every default rule, so each test can
// break exactly one thing and see that rule fire.
func magmaGoodOrder() MagmaOrder {
	return MagmaOrder{
		ID:               "order-ok",
		SizeSat:          2_000_000,
		RevenueSat:       20_000,
		PricePPM:         10_000,
		FeeRateCapPPM:    500,
		CommitmentBlocks: 4320, // 30 days
	}
}

func magmaGoodInputs(order MagmaOrder) magmaPolicyInputs {
	return magmaPolicyInputs{
		Order:            order,
		AvailableSat:     10_000_000,
		SatPerVbyte:      5,
		EstimatedFeeSat:  2_000,
		OnchainReachable: true,
	}
}

func TestEvaluateMagmaOrderAcceptsAGoodOrder(t *testing.T) {
	decision := evaluateMagmaOrder(defaultMagmaPolicy(), magmaGoodInputs(magmaGoodOrder()))
	if !decision.Accept {
		t.Fatalf("expected accept, got %+v", decision)
	}
}

// The split between rejecting and deferring is the heart of this engine. Getting
// it backwards is expensive in both directions: rejecting over a fee spike burns
// a sale that would have been fine an hour later, and deferring on structurally
// bad terms lets the order lapse into SELLER_FAILED_TO_REACT.
func TestEvaluateMagmaOrderRejectsBadTermsPermanently(t *testing.T) {
	cases := []struct {
		name          string
		mutate        func(*MagmaOrder)
		reasonMatches string
	}{
		{
			name:          "channel too small",
			mutate:        func(o *MagmaOrder) { o.SizeSat = 100_000 },
			reasonMatches: "below the",
		},
		{
			name:          "channel too large",
			mutate:        func(o *MagmaOrder) { o.SizeSat = 50_000_000 },
			reasonMatches: "above the",
		},
		{
			name:          "revenue below floor",
			mutate:        func(o *MagmaOrder) { o.RevenueSat = 100 },
			reasonMatches: "revenue",
		},
		{
			name:          "price below floor",
			mutate:        func(o *MagmaOrder) { o.PricePPM = 100 },
			reasonMatches: "ppm is below",
		},
		{
			name:          "commitment longer than we accept",
			mutate:        func(o *MagmaOrder) { o.CommitmentBlocks = 300 * 144 },
			reasonMatches: "commitment",
		},
		{
			// The trap this rule exists for: a headline price that looks fine
			// until it is spread over six months of locked capital.
			name: "good ppm but stretched over 180 days",
			mutate: func(o *MagmaOrder) {
				o.PricePPM = 3000
				o.CommitmentBlocks = 25920
			},
			reasonMatches: "ppm/day",
		},
		{
			// Selling inbound cheap and being stuck under a low routing ceiling is
			// the worst combination, and it is invisible if only price is checked.
			name:          "routing fee ceiling too low to be worth it",
			mutate:        func(o *MagmaOrder) { o.FeeRateCapPPM = 10 },
			reasonMatches: "caps our routing fee",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order := magmaGoodOrder()
			tc.mutate(&order)
			decision := evaluateMagmaOrder(defaultMagmaPolicy(), magmaGoodInputs(order))
			if !decision.Reject {
				t.Fatalf("expected a rejection, got %+v", decision)
			}
			if decision.Accept {
				t.Fatal("a rejected order must not also be accepted")
			}
			if !strings.Contains(decision.Reason, tc.reasonMatches) {
				t.Fatalf("reason %q does not mention %q", decision.Reason, tc.reasonMatches)
			}
		})
	}
}

func TestEvaluateMagmaOrderDefersTransientConditions(t *testing.T) {
	cases := []struct {
		name          string
		mutate        func(*magmaPolicyInputs)
		reasonMatches string
	}{
		{
			name:          "wallet short right now",
			mutate:        func(in *magmaPolicyInputs) { in.AvailableSat = 10_000 },
			reasonMatches: "waiting for funds",
		},
		{
			name:          "balance unreadable",
			mutate:        func(in *magmaPolicyInputs) { in.OnchainReachable = false },
			reasonMatches: "on-chain balance",
		},
		{
			name:          "too many opens in flight",
			mutate:        func(in *magmaPolicyInputs) { in.ConcurrentOpens = 5 },
			reasonMatches: "awaiting a channel open",
		},
		{
			name:          "daily order cap reached",
			mutate:        func(in *magmaPolicyInputs) { in.OrdersToday = 99 },
			reasonMatches: "daily cap",
		},
		{
			name:          "daily size cap would be exceeded",
			mutate:        func(in *magmaPolicyInputs) { in.SizeToday = 19_500_000 },
			reasonMatches: "daily cap",
		},
		{
			name:          "mempool spike",
			mutate:        func(in *magmaPolicyInputs) { in.SatPerVbyte = 300 },
			reasonMatches: "cheaper fees",
		},
		{
			name:          "on-chain cost eats too much of the sale",
			mutate:        func(in *magmaPolicyInputs) { in.EstimatedFeeSat = 15_000 },
			reasonMatches: "cheaper fees",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputs := magmaGoodInputs(magmaGoodOrder())
			tc.mutate(&inputs)
			decision := evaluateMagmaOrder(defaultMagmaPolicy(), inputs)
			if decision.Accept {
				t.Fatalf("expected a deferral, got an acceptance: %+v", decision)
			}
			if decision.Reject {
				t.Fatalf("a transient condition must never reject the order: %+v", decision)
			}
			if !strings.Contains(decision.Reason, tc.reasonMatches) {
				t.Fatalf("reason %q does not mention %q", decision.Reason, tc.reasonMatches)
			}
		})
	}
}

// Bad terms must be rejected even while the wallet is empty. Otherwise the order
// would sit deferred until it lapsed, which is exactly the failure the reject
// path exists to avoid.
func TestEvaluateMagmaOrderPrefersRejectionOverDeferral(t *testing.T) {
	order := magmaGoodOrder()
	order.PricePPM = 10 // structurally unacceptable
	inputs := magmaGoodInputs(order)
	inputs.AvailableSat = 0 // also broke
	decision := evaluateMagmaOrder(defaultMagmaPolicy(), inputs)
	if !decision.Reject {
		t.Fatalf("bad terms must reject regardless of wallet state, got %+v", decision)
	}
}

// The reserve is what stops auto mode from draining the wallet into channels and
// leaving nothing for fees or force-close costs.
func TestEvaluateMagmaOrderHonoursOnchainReserve(t *testing.T) {
	policy := defaultMagmaPolicy()
	order := magmaGoodOrder()
	inputs := magmaGoodInputs(order)
	// Exactly enough for the channel and its fee, nothing left for the reserve.
	inputs.AvailableSat = order.SizeSat + inputs.EstimatedFeeSat

	if decision := evaluateMagmaOrder(policy, inputs); decision.Accept {
		t.Fatal("must not spend into the on-chain reserve")
	}
	inputs.AvailableSat = order.SizeSat + inputs.EstimatedFeeSat + policy.MinOnchainReserve
	if decision := evaluateMagmaOrder(policy, inputs); !decision.Accept {
		t.Fatalf("should accept once the reserve is covered: %+v", decision)
	}
}

// A policy with the limits switched off must not accidentally reject everything
// via zero-value comparisons.
func TestEvaluateMagmaOrderTreatsZeroLimitsAsDisabled(t *testing.T) {
	order := magmaGoodOrder()
	order.FeeRateCapPPM = 1
	order.CommitmentBlocks = 100 * 144
	inputs := magmaGoodInputs(order)
	inputs.SatPerVbyte = 5000
	inputs.EstimatedFeeSat = 1

	decision := evaluateMagmaOrder(MagmaPolicy{}, inputs)
	if !decision.Accept {
		t.Fatalf("an empty policy imposes no limits, got %+v", decision)
	}
}

// A cap of zero means "base fee must be zero", which is the majority case in real
// orders, and the shipped lnd.conf default of 1 sat breaches it.
func TestMagmaFeeUnderCap(t *testing.T) {
	cases := []struct {
		cap  int64
		want int64
	}{
		{cap: 0, want: 0},
		{cap: 1, want: 0},
		{cap: 100, want: 99},
		{cap: 500, want: 495},
		{cap: 900, want: 891},
	}
	for _, tc := range cases {
		if got := magmaFeeUnderCap(tc.cap); got != tc.want {
			t.Errorf("magmaFeeUnderCap(%d) = %d, want %d", tc.cap, got, tc.want)
		}
		if got := magmaFeeUnderCap(tc.cap); tc.cap > 0 && got >= tc.cap {
			t.Errorf("magmaFeeUnderCap(%d) = %d must stay below the cap", tc.cap, got)
		}
	}
}
