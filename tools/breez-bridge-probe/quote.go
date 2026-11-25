package main

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	sdk "github.com/breez/breez-sdk-spark-go/breez_sdk_spark"
)

type quoteOptions struct {
	amount          uint64
	baseRefund      string
	lightningRefund string
}

func (q quoteOptions) validate() error {
	if q.amount == 0 && q.baseRefund == "" && q.lightningRefund == "" {
		return nil
	}
	if q.amount == 0 || !validBaseAddress(q.baseRefund) || strings.TrimSpace(q.lightningRefund) == "" {
		return fmt.Errorf("quote mode requires positive --quote-usdc-micros, --base-refund and --lightning-refund")
	}
	return nil
}

func quoteUnfunded(client *sdk.BridgingClient, route sdk.BridgeRoutePair, recipient, refund string, toBase bool, amount uint64) error {
	fee := sdk.FeePolicyFeesExcluded
	slippage := uint32(100)
	quote, err := client.CreateBridge(sdk.CreateBridgeRequest{
		Recipient:      recipient,
		Route:          route,
		Amount:         new(big.Int).SetUint64(amount),
		RefundAddress:  &refund,
		FeePolicy:      &fee,
		MaxSlippageBps: &slippage,
	})
	if err != nil {
		return fmt.Errorf("quote creation failed (%T)", err)
	}
	depositUnit, deliveryUnit := "USDC micros", "sats"
	expectedAsset, prefix := "BTC", "ethereum:"
	if toBase {
		depositUnit, deliveryUnit = deliveryUnit, depositUnit
		expectedAsset, prefix = "USDC", "lnbc"
	}
	info := quote.Info
	expiresAt := time.Unix(int64(info.ExpiresAt), 0)
	if quote.BridgeId == "" || quote.StatusToken == nil || *quote.StatusToken == "" ||
		!strings.HasPrefix(strings.ToLower(quote.PaymentRequest), prefix) ||
		info.DepositAmount == nil || info.DepositAmount.Sign() <= 0 ||
		info.ExpectedReceivedAmount == nil || info.ExpectedReceivedAmount.Sign() <= 0 ||
		info.DestinationAsset != expectedAsset || !expiresAt.After(time.Now()) {
		return fmt.Errorf("quote returned incomplete or unexpected payment details")
	}

	status, err := client.GetBridgeStatus(sdk.GetBridgeStatusRequest{
		BridgeId: quote.BridgeId, StatusToken: quote.StatusToken,
	})
	if err != nil {
		return fmt.Errorf("quote status lookup failed (%T)", err)
	}
	if status.Status != sdk.BridgeStatusCreated || status.AmountIn != nil {
		return fmt.Errorf("quote was not confirmed in the unpaid state")
	}
	fmt.Printf("  Deposit: %s %s. Expected receipt: %s %s. Expires at %s. Unpaid.\n",
		info.DepositAmount, depositUnit, info.ExpectedReceivedAmount, deliveryUnit, expiresAt.UTC().Format(time.RFC3339))
	return nil
}
