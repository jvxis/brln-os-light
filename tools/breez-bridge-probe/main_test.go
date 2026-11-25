package main

import (
	"testing"

	sdk "github.com/breez/breez-sdk-spark-go/breez_sdk_spark"
)

func TestMatchingRouteCount(t *testing.T) {
	baseChain, otherChain := "8453", "1"
	contract, otherContract := baseUSDCContract, "0x0000000000000000000000000000000000000001"
	usdc := sdk.BridgeRoutePairSide{Chain: "base", Asset: "USDC", Decimals: 6, ChainId: &baseChain, ContractAddress: &contract}
	btc := sdk.BridgeRoutePairSide{Chain: "lightning", Asset: "BTC"}
	wrongChain, wrongToken := usdc, usdc
	wrongChain.ChainId = &otherChain
	wrongToken.ContractAddress = &otherContract
	routes := []sdk.BridgeRoutePair{
		{Source: btc, Destination: usdc},
		{Source: usdc, Destination: btc},
		{Source: btc, Destination: wrongChain},
		{Source: wrongToken, Destination: btc},
	}

	for _, toBase := range []bool{true, false} {
		if got := len(matchingRoutes(routes, toBase)); got != 1 {
			t.Errorf("toBase=%v: got %d matching routes, want 1", toBase, got)
		}
	}
}

func TestQuoteOptionsRequireRefundDestinations(t *testing.T) {
	base := "0x0000000000000000000000000000000000000001"
	lightning := "tester@example.org"
	for _, test := range []struct {
		name    string
		options quoteOptions
		valid   bool
	}{
		{"discovery", quoteOptions{}, true},
		{"quote", quoteOptions{10_000_000, base, lightning}, true},
		{"missing Base refund", quoteOptions{10_000_000, "", lightning}, false},
		{"missing Lightning refund", quoteOptions{10_000_000, base, ""}, false},
		{"no quote amount", quoteOptions{0, base, lightning}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.options.validate(); (err == nil) != test.valid {
				t.Fatalf("validate() returned %v, want valid=%v", err, test.valid)
			}
		})
	}
}
