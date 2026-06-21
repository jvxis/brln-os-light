package server

import "testing"

func TestCoinGeckoPricesToMarketPrices(t *testing.T) {
	prices, err := coinGeckoPricesToMarketPrices(coingeckoSimplePriceResponse{
		Bitcoin: map[string]float64{
			"usd":            100000,
			"usd_24h_change": 1.25,
			"brl":            550000,
			"brl_24h_change": -0.5,
			"eur":            92000,
			"eur_24h_change": 0.75,
		},
	})
	if err != nil {
		t.Fatalf("expected prices, got error: %v", err)
	}
	if len(prices) != 3 {
		t.Fatalf("expected 3 prices, got %d", len(prices))
	}
	if prices[0].Currency != "USD" || prices[0].Value != 100000 || prices[0].Change24H != 1.25 {
		t.Fatalf("unexpected USD price: %+v", prices[0])
	}
	if prices[1].Currency != "BRL" || prices[1].Value != 550000 || prices[1].Change24H != -0.5 {
		t.Fatalf("unexpected BRL price: %+v", prices[1])
	}
	if prices[2].Currency != "EUR" || prices[2].Value != 92000 || prices[2].Change24H != 0.75 {
		t.Fatalf("unexpected EUR price: %+v", prices[2])
	}
}

func TestCoinGeckoPricesToMarketPricesMissingBitcoin(t *testing.T) {
	if _, err := coinGeckoPricesToMarketPrices(coingeckoSimplePriceResponse{}); err == nil {
		t.Fatalf("expected missing price error")
	}
}
