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
		if got := matchingRouteCount(routes, toBase); got != 1 {
			t.Errorf("toBase=%v: got %d matching routes, want 1", toBase, got)
		}
	}
}
