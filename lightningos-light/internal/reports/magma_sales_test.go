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
	if idle.SalesRevenueSat != 0 || idle.NetTotalMsat != base.NetRoutingProfitMsat+base.KeysendReceivedMsat {
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
	if want := base.NetRoutingProfitMsat + 3_566_000; got.NetTotalMsat != want {
		t.Fatalf("net total: got %d, want %d", got.NetTotalMsat, want)
	}
}

func TestNetTotalIncludesKeysendAndSales(t *testing.T) {
	base := Metrics{
		NetRoutingProfitMsat: 800_000,
		KeysendReceivedMsat:  50_000,
	}
	got := base.WithMagmaSales(MagmaSalesRevenue{RevenueMsat: 3_566_000, Count: 1})
	if want := int64(800_000 + 50_000 + 3_566_000); got.NetTotalMsat != want {
		t.Fatalf("net total: got %d, want %d", got.NetTotalMsat, want)
	}
	if got.NetTotalSat != got.NetTotalMsat/1000 {
		t.Fatalf("sat and msat totals disagree: %d vs %d", got.NetTotalSat, got.NetTotalMsat)
	}
}
