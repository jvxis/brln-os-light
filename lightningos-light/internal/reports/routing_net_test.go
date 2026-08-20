package reports

import "testing"

// Issue #74. The routing net used to be
//
//	forwards - rebalances - paymentFees
//
// which answered the wrong question. Rebalances are what liquidity management
// costs so that forwarding can happen at all; the fee on an invoice the operator
// paid buys something for the operator. Counting the second against routing made
// a month of heavy personal use look like a month of bad routing.
func TestRoutingNetIgnoresPaymentFees(t *testing.T) {
	quiet := Metrics{
		ForwardFeeRevenueMsat: 500_000,
		RebalanceFeeCostMsat:  120_000,
	}
	busy := quiet
	busy.PaymentFeeCostMsat = 2_000_000 // the operator paid a lot of invoices

	quiet = quiet.withNetTotal()
	busy = busy.withNetTotal()

	// Routing did not change, so the routing figure must not change either.
	if quiet.ForwardFeeRevenueMsat-quiet.RebalanceFeeCostMsat != 380_000 {
		t.Fatal("premise: routing profit here is 380,000")
	}
	if quiet.NetTotalMsat == busy.NetTotalMsat {
		t.Fatal("the total must still feel those payment fees - they are real costs")
	}
	if diff := quiet.NetTotalMsat - busy.NetTotalMsat; diff != 2_000_000 {
		t.Fatalf("the total should differ by exactly the payment fees, got %d", diff)
	}
}

// The two figures answer different questions and must not be conflated: one is
// about the forwarding business, the other about the node as a whole.
func TestRoutingNetAndTotalAnswerDifferentQuestions(t *testing.T) {
	m := Metrics{
		ForwardFeeRevenueMsat: 206_000,
		RebalanceFeeCostMsat:  1_400_000,
		PaymentFeeCostMsat:    72_000,
		OnchainFeeCostMsat:    650_000,
		KeysendReceivedMsat:   0,
		SalesRevenueMsat:      2_284_000,
	}.withNetTotal()

	// Routing is losing money: rebalances cost more than the forwards earned.
	routing := m.ForwardFeeRevenueMsat - m.RebalanceFeeCostMsat
	if routing >= 0 {
		t.Fatalf("premise: routing is negative here, got %d", routing)
	}
	// The node as a whole is still ahead, because a channel sale carried it.
	if m.NetTotalMsat <= 0 {
		t.Fatalf("the sale should carry the total into profit, got %d", m.NetTotalMsat)
	}
	// And the total is not reachable by adding revenue onto the routing figure,
	// which is how it used to be built.
	if m.NetTotalMsat == routing+m.KeysendReceivedMsat+m.SalesRevenueMsat {
		t.Fatal("the total must subtract payment and on-chain costs, not just add revenue")
	}
}
