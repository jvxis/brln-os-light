package reports

import "testing"

// A mark contributes the principal, never the fee. Every payment fee is already
// counted under costs whether or not the payment was classified, so adding it
// again with the mark would charge the same sats twice.
func TestMarksContributePrincipalOnly(t *testing.T) {
	base := Metrics{
		ForwardFeeRevenueMsat: 500_000,
		RebalanceFeeCostMsat:  100_000,
		PaymentFeeCostMsat:    2_000, // includes the fee of the invoice marked below
	}

	// The operator bought a channel: a 1,000,000 sat invoice paid, marked as an
	// operating cost. The 2,000 msat fee it paid is already in PaymentFeeCost.
	marked := base.WithActivityMarks(ActivityMarkTotals{CostMsat: 1_000_000_000, CostUnit: 1})

	if want := int64(100_000 + 2_000 + 1_000_000_000); marked.TotalCostMsat() != want {
		t.Fatalf("total cost: got %d, want %d", marked.TotalCostMsat(), want)
	}
	if marked.PaymentFeeCostMsat != base.PaymentFeeCostMsat {
		t.Fatal("the fee must not be touched by a mark")
	}
	// Routing is a separate question and marks say nothing about forwarding.
	if marked.ForwardFeeRevenueMsat-marked.RebalanceFeeCostMsat != 400_000 {
		t.Fatal("a mark must not reach the routing figure")
	}
}

// Revenue marks work the same way from the other side.
func TestRevenueMarkRaisesTheTotal(t *testing.T) {
	base := Metrics{ForwardFeeRevenueMsat: 100_000}.withNetTotal()
	marked := base.WithActivityMarks(ActivityMarkTotals{RevenueMsat: 50_000_000, RevenueUnit: 2})

	if marked.MarkedRevenueCount != 2 {
		t.Fatalf("count: got %d, want 2", marked.MarkedRevenueCount)
	}
	if want := base.NetTotalMsat + 50_000_000; marked.NetTotalMsat != want {
		t.Fatalf("net total: got %d, want %d", marked.NetTotalMsat, want)
	}
	if marked.MarkedRevenueSat != 50_000 {
		t.Fatalf("sat conversion: got %d", marked.MarkedRevenueSat)
	}
}

// Nothing marked must read exactly as before the feature existed.
func TestNoMarksChangesNothing(t *testing.T) {
	base := Metrics{
		ForwardFeeRevenueMsat: 500_000,
		RebalanceFeeCostMsat:  100_000,
		OnchainFeeCostMsat:    20_000,
	}.withNetTotal()
	empty := base.WithActivityMarks(ActivityMarkTotals{})

	if empty.NetTotalMsat != base.NetTotalMsat {
		t.Fatalf("an unmarked node must report what it always did: %d vs %d",
			empty.NetTotalMsat, base.NetTotalMsat)
	}
	if empty.TotalRevenueMsat() != base.TotalRevenueMsat() || empty.TotalCostMsat() != base.TotalCostMsat() {
		t.Fatal("totals moved with no marks present")
	}
}

func TestValidMarkClassification(t *testing.T) {
	for _, ok := range []string{MarkRevenue, MarkCost, " revenue ", " cost "} {
		if !ValidMarkClassification(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	// An empty classification is how a mark is cleared, handled before this
	// check; anything else is a typo and must not silently become a category.
	for _, bad := range []string{"", "income", "expense", "Revenue "} {
		if ValidMarkClassification(bad) {
			t.Errorf("%q must not be accepted as a classification", bad)
		}
	}
}
