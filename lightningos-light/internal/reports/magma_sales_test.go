package reports

import "testing"

// A node without the Magma app must report exactly what it reported before, so
// "not installed" has to stay distinguishable from "installed and sold nothing".
// Both look like zero revenue; only one may overwrite the metrics.
func TestWithMagmaSalesOnlyAppliedWhenInstalled(t *testing.T) {
	base := Metrics{
		ForwardFeeRevenueMsat: 1_200_000,
		RebalanceFeeCostMsat:  300_000,
		PaymentFeeCostMsat:    100_000,
		KeysendReceivedMsat:   50_000,
		NetRoutingProfitMsat:  800_000,
		NetRoutingProfitSat:   800,
	}

	// Not installed: attachMagmaSales returns the metrics untouched, so the
	// derived totals stay at whatever the routing scan produced.
	if base.SalesRevenueMsat != 0 || base.SalesCount != 0 {
		t.Fatal("base metrics must carry no sales")
	}

	withSales := base.WithMagmaSales(MagmaSalesRevenue{RevenueMsat: 3_566_000, Count: 1})
	if withSales.SalesRevenueSat != 3566 || withSales.SalesCount != 1 {
		t.Fatalf("sales not recorded: %+v", withSales)
	}
	// Installed but idle: an explicit zero is still a real answer.
	idle := base.WithMagmaSales(MagmaSalesRevenue{})
	idleWant := base.TotalRevenueMsat() - base.TotalCostMsat()
	if idle.SalesRevenueSat != 0 || idle.NetTotalMsat != idleWant {
		t.Fatalf("idle app must contribute nothing but still compute the total: %+v", idle)
	}
}

// The funding transaction fee is already inside OnchainFeeCost via the generic
// on-chain scan. Sales therefore contribute revenue only; if the cost were also
// attributed here it would be subtracted twice.
func TestMagmaSalesDoNotTouchCostFields(t *testing.T) {
	base := Metrics{
		OnchainFeeCostSat:    308,
		OnchainFeeCostMsat:   308_000,
		RebalanceFeeCostMsat: 300_000,
		PaymentFeeCostMsat:   100_000,
		NetRoutingProfitMsat: 800_000,
	}
	got := base.WithMagmaSales(MagmaSalesRevenue{RevenueMsat: 3_566_000, Count: 1})

	if got.OnchainFeeCostSat != base.OnchainFeeCostSat || got.OnchainFeeCostMsat != base.OnchainFeeCostMsat {
		t.Fatalf("on-chain cost must be left alone, got %d/%d", got.OnchainFeeCostSat, got.OnchainFeeCostMsat)
	}
	if got.RebalanceFeeCostMsat != base.RebalanceFeeCostMsat || got.PaymentFeeCostMsat != base.PaymentFeeCostMsat {
		t.Fatal("off-chain costs must be left alone")
	}
	// Routing profit keeps meaning routing alone; the sale lands in the total.
	if got.NetRoutingProfitMsat != base.NetRoutingProfitMsat {
		t.Fatalf("net routing profit must not absorb sales revenue, got %d", got.NetRoutingProfitMsat)
	}
	// The sale reaches the bottom line through revenue, never by touching a cost.
	if want := got.TotalRevenueMsat() - got.TotalCostMsat(); got.NetTotalMsat != want {
		t.Fatalf("net total: got %d, want %d", got.NetTotalMsat, want)
	}
	if got.TotalCostMsat() != base.TotalCostMsat() {
		t.Fatalf("total cost changed after a sale: %d vs %d", got.TotalCostMsat(), base.TotalCostMsat())
	}
}

// The total is every revenue minus every cost. It used to be built by adding
// revenues onto the routing profit, which quietly inherited two problems: the
// routing profit already had payment fees subtracted, and on-chain costs were
// never subtracted at all - so opening a channel was free in the one number
// meant to say whether the node made money.
func TestNetTotalIsEveryRevenueMinusEveryCost(t *testing.T) {
	base := Metrics{
		ForwardFeeRevenueMsat: 800_000,
		KeysendReceivedMsat:   50_000,
		RebalanceFeeCostMsat:  120_000,
		PaymentFeeCostMsat:    30_000,
		OnchainFeeCostMsat:    200_000,
	}
	got := base.WithMagmaSales(MagmaSalesRevenue{RevenueMsat: 3_566_000, Count: 1})

	wantRevenue := int64(800_000 + 50_000 + 3_566_000)
	wantCost := int64(120_000 + 30_000 + 200_000)
	if got.TotalRevenueMsat() != wantRevenue {
		t.Fatalf("revenue: got %d, want %d", got.TotalRevenueMsat(), wantRevenue)
	}
	if got.TotalCostMsat() != wantCost {
		t.Fatalf("cost: got %d, want %d", got.TotalCostMsat(), wantCost)
	}
	if want := wantRevenue - wantCost; got.NetTotalMsat != want {
		t.Fatalf("net total: got %d, want %d", got.NetTotalMsat, want)
	}
	if got.NetTotalSat != got.NetTotalMsat/1000 {
		t.Fatalf("sat and msat totals disagree: %d vs %d", got.NetTotalSat, got.NetTotalMsat)
	}
}

// The on-chain cost is the part that was missing, so it gets its own check: a
// period with nothing but a channel open must show a negative total.
func TestNetTotalSubtractsOnchainCost(t *testing.T) {
	metrics := Metrics{OnchainFeeCostMsat: 155_000}.WithMagmaSales(MagmaSalesRevenue{})
	if metrics.NetTotalMsat != -155_000 {
		t.Fatalf("a period with only an on-chain fee is a loss: got %d", metrics.NetTotalMsat)
	}
}
