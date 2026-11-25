package main

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	sdk "github.com/breez/breez-sdk-spark-go/breez_sdk_spark"
)

func TestTreasuryWorkerHealthDoesNotRequireACredential(t *testing.T) {
	var result bytes.Buffer
	if err := bridgeWorker(strings.NewReader(`{"action":"health"}`), &result); err != nil {
		t.Fatal(err)
	}
	var got bridgeResult
	if json.Unmarshal(result.Bytes(), &got) != nil || got.Status != "ready" {
		t.Fatal("expected health response")
	}
	for _, invalid := range []string{`{"action":"health","unexpected":1}`, `{"action":"health"}{}`} {
		if err := bridgeWorker(strings.NewReader(invalid), &result); err == nil {
			t.Fatal("invalid private request was accepted")
		}
	}
}

func TestTreasuryWorkerFiltersBothDirectionsToOfficialSolanaUSDC(t *testing.T) {
	mint := solanaUSDCMint
	otherMint := "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
	official := sdk.BridgeRoutePairSide{Chain: "solana", Asset: "USDC", Decimals: 6, ContractAddress: &mint}
	other := official
	other.ContractAddress = &otherMint
	lightning := sdk.BridgeRoutePairSide{Chain: "lightning", Asset: "BTC"}
	wrongChain := official
	wrongChain.Chain = "base"
	routes := []sdk.BridgeRoutePair{
		{Source: lightning, Destination: official},
		{Source: official, Destination: lightning},
		{Source: lightning, Destination: other},
		{Source: wrongChain, Destination: lightning},
		{Source: official, Destination: other},
	}
	if got := matchingSolanaRoutes(routes, true); len(got) != 1 || got[0].Destination.ContractAddress != &mint {
		t.Fatal("incorrect native USDC destination")
	}
	if got := matchingSolanaRoutes(routes, false); len(got) != 1 || got[0].Source.ContractAddress != &mint {
		t.Fatal("incorrect native USDC source")
	}
	if !validSolanaAddress(mint) || validSolanaAddress(strings.Repeat("1", 32)) ||
		validSolanaAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913") || validSolanaAddress(mint[:30]) {
		t.Fatal("incorrect Solana public address validation")
	}
}

func TestTreasuryWorkerPreservesReportedFeeUnits(t *testing.T) {
	usdc := "USDC"
	six := uint32(6)
	for _, test := range []struct {
		name  string
		input sdk.CrossChainReceiveInfo
		want  *bridgeFee
	}{
		{"sats", sdk.CrossChainReceiveInfo{ServiceFeeAmount: big.NewInt(3)}, &bridgeFee{Amount: 3, Asset: "sat"}},
		{"usdc", sdk.CrossChainReceiveInfo{ServiceFeeAmount: big.NewInt(20842), ServiceFeeAsset: &usdc, ServiceFeeAssetDecimals: &six},
			&bridgeFee{Amount: 20842, Asset: "USDC", Decimals: 6}},
		{"zero", sdk.CrossChainReceiveInfo{ServiceFeeAmount: big.NewInt(0)}, &bridgeFee{Amount: 0, Asset: "sat"}},
		{"missing", sdk.CrossChainReceiveInfo{}, nil},
		{"ambiguous", sdk.CrossChainReceiveInfo{ServiceFeeAmount: big.NewInt(3), ServiceFeeAsset: &usdc}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			fee := workerServiceFee(test.input)
			if (fee == nil) != (test.want == nil) || fee != nil && *fee != *test.want {
				t.Fatalf("unexpected fee: %+v", fee)
			}
		})
	}
}
