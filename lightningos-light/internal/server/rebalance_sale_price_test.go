package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestChannelSalePriceReference(t *testing.T) {
	cases := []struct {
		name    string
		price   channelSalePrice
		wantPpm int64
		wantSrc string
	}{
		{name: "7d with enough forwards", price: channelSalePrice{Ppm7d: 860, Count7d: 3, Ppm30d: 900, Count30d: 40}, wantPpm: 860, wantSrc: "7d"},
		{name: "thin 7d falls back to 30d", price: channelSalePrice{Ppm7d: 1200, Count7d: 2, Ppm30d: 900, Count30d: 12}, wantPpm: 900, wantSrc: "30d"},
		{name: "a single sale in 30d is a price", price: channelSalePrice{Ppm30d: 879, Count30d: 1}, wantPpm: 879, wantSrc: "30d"},
		{name: "nothing sold", price: channelSalePrice{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ppm, src := tc.price.referencePpm()
			if ppm != tc.wantPpm || src != tc.wantSrc {
				t.Fatalf("got %d/%q want %d/%q", ppm, src, tc.wantPpm, tc.wantSrc)
			}
		})
	}
}

func TestFeeCapReferencePpm(t *testing.T) {
	off := RebalanceConfig{}
	on := RebalanceConfig{RealizedPriceCapEnabled: true}
	// bfx-lnd0, 30 days to 2026-10-10: advertised 1737, sold at 1619.
	if got := feeCapReferencePpm(off, 1737, 1619); got != 1737 {
		t.Fatalf("cap off must keep the advertised fee, got %d", got)
	}
	if got := feeCapReferencePpm(on, 1737, 1619); got != 1619 {
		t.Fatalf("cap on must price on the realized sale, got %d", got)
	}
	if got := feeCapReferencePpm(on, 700, 1619); got != 700 {
		t.Fatalf("a realized price above the advertised fee never raises the cap, got %d", got)
	}
	if got := feeCapReferencePpm(on, 700, 0); got != 700 {
		t.Fatalf("no sale history keeps the advertised fee, got %d", got)
	}
}

func TestRealizedFeeCapPolicyFeedsFeeLimit(t *testing.T) {
	cfg := RebalanceConfig{EconRatio: 0.8, RealizedPriceCapEnabled: true}
	ch := RebalanceChannel{OutgoingFeePpm: 1737, OutgoingBaseMsat: 0, SalePricePpm: 1619}
	feeMsat, err := calcFeeLimitMsat(300_000*1000, realizedFeeCapPolicy(ch, cfg), nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := feeMsatToPpm(feeMsat, 300_000); got != 1295 {
		t.Fatalf("expected cap 1295 ppm (0.8 × 1619), got %d", got)
	}
	cfg.RealizedPriceCapEnabled = false
	feeMsat, _ = calcFeeLimitMsat(300_000*1000, realizedFeeCapPolicy(ch, cfg), nil, cfg)
	if got := feeMsatToPpm(feeMsat, 300_000); got != 1390 {
		t.Fatalf("expected cap 1390 ppm (0.8 × 1737) with the flag off, got %d", got)
	}
}

func TestChannelSalePriceNoSale(t *testing.T) {
	old := float64(realizedPriceCapNoSaleAgeHours)
	if !(channelSalePrice{AgeHours: old}).NoSale() {
		t.Fatal("30-day-old channel without a sale has no price to buy against")
	}
	if (channelSalePrice{AgeHours: old - 1}).NoSale() {
		t.Fatal("a channel younger than 30 days may simply not have sold yet")
	}
	if (channelSalePrice{AgeHours: old * 3, Count30d: 1, Ppm30d: 879}).NoSale() {
		t.Fatal("one sale is a price")
	}
	if (channelSalePrice{}).NoSale() {
		t.Fatal("unknown age (no block height) must not block")
	}
}

func TestSalePriceSnapshotForChannelFillsAge(t *testing.T) {
	snap := salePriceSnapshot{prices: map[uint64]channelSalePrice{1 << 40: {Ppm30d: 879, Count30d: 1}}, blockHeight: 1 + 6*24*40}
	price := snap.forChannel(lndclient.ChannelInfo{ChannelID: 1 << 40})
	if price.Ppm30d != 879 || price.AgeHours < 40*24-1 || price.AgeHours > 40*24+1 {
		t.Fatalf("unexpected price/age: %+v", price)
	}
	// A channel absent from the map sold nothing; it still carries its age,
	// which is exactly what no_sale_30d needs.
	if got := snap.forChannel(lndclient.ChannelInfo{ChannelID: 2 << 40}); got.Count30d != 0 || got.AgeHours <= 0 || !got.NoSale() {
		t.Fatalf("unknown channel: %+v", got)
	}
}
