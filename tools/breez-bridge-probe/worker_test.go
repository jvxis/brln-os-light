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
