package main

import (
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"

	sdk "github.com/breez/breez-sdk-spark-go/breez_sdk_spark"
)

// The worker's one-shot protocol is private to the Manager. Keys and lookup
// tokens arrive on stdin so they do not appear in command-line arguments.
type bridgeCommand struct {
	Action      string `json:"action"`
	APIKey      string `json:"api_key"`
	Direction   string `json:"direction"`
	Recipient   string `json:"recipient"`
	Refund      string `json:"refund"`
	Amount      int64  `json:"amount"`
	BridgeID    string `json:"bridge_id"`
	StatusToken string `json:"status_token"`
	Cluster     string `json:"cluster"`
	Transaction string `json:"transaction"`
}

type bridgeResult struct {
	BridgeID           string     `json:"bridge_id,omitempty"`
	StatusToken        string     `json:"status_token,omitempty"`
	Invoice            string     `json:"invoice,omitempty"`
	Deposit            string     `json:"deposit,omitempty"`
	AmountIn           int64      `json:"amount_in,omitempty"`
	AmountOut          int64      `json:"amount_out,omitempty"`
	ExpiresAt          int64      `json:"expires_at,omitempty"`
	Status             string     `json:"status,omitempty"`
	OrderID            string     `json:"order_id,omitempty"`
	DepositRef         string     `json:"deposit_ref,omitempty"`
	DeliveryRef        string     `json:"delivery_ref,omitempty"`
	RefundRef          string     `json:"refund_ref,omitempty"`
	ProviderFee        *bridgeFee `json:"provider_fee,omitempty"`
	Cluster            string     `json:"cluster,omitempty"`
	Mint               string     `json:"mint,omitempty"`
	Sponsor            string     `json:"sponsor,omitempty"`
	FeeAccount         string     `json:"fee_account,omitempty"`
	Blockhash          string     `json:"blockhash,omitempty"`
	NetworkFeeMicros   int64      `json:"network_fee_micros,omitempty"`
	NetworkFeeLamports int64      `json:"network_fee_lamports,omitempty"`
}

type bridgeFee struct {
	Amount   int64  `json:"amount"`
	Asset    string `json:"asset"`
	Decimals uint32 `json:"decimals"`
}

func bridgeWorker(input io.Reader, output io.Writer) error {
	var req bridgeCommand
	reader := json.NewDecoder(io.LimitReader(input, 16<<10))
	reader.DisallowUnknownFields()
	if reader.Decode(&req) != nil || reader.Decode(new(any)) != io.EOF {
		return errors.New("invalid bridge request")
	}
	if req.Action == "health" && req == (bridgeCommand{Action: "health"}) {
		return json.NewEncoder(output).Encode(bridgeResult{Status: "ready"})
	}
	if len(req.APIKey) < 1 || len(req.APIKey) > 4096 || strings.ContainsAny(req.APIKey, "\r\n\x00") {
		return errors.New("invalid bridge credential")
	}
	if req.Action == "sponsor-preflight" || req.Action == "sponsor-estimate" {
		result, err := sponsorRequest(req)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(result)
	}
	client, err := sdk.NewBridgingClient(sdk.DefaultBridgingConfig(req.APIKey))
	if err != nil {
		return errors.New("bridge initialization failed")
	}
	defer client.Destroy()
	var result bridgeResult
	switch req.Action {
	case "quote":
		result, err = workerQuote(client, req)
	case "status":
		result, err = workerStatus(client, req)
	default:
		return errors.New("unknown bridge action")
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func workerQuote(client *sdk.BridgingClient, req bridgeCommand) (bridgeResult, error) {
	toSolana := req.Direction == "to_solana"
	if !toSolana && req.Direction != "to_lightning" || req.Amount <= 0 ||
		req.Recipient == "" || req.Refund == "" || len(req.Recipient) > 320 || len(req.Refund) > 4096 {
		return bridgeResult{}, errors.New("invalid bridge quote")
	}
	if toSolana && (!validSolanaAddress(req.Recipient) || !strings.HasPrefix(req.Refund, "lnbc")) ||
		!toSolana && (!validSolanaAddress(req.Refund) || !strings.Contains(req.Recipient, "@")) {
		return bridgeResult{}, errors.New("invalid bridge destination")
	}
	res, err := client.GetRoutes(sdk.GetBridgeRoutesRequest{Recipient: req.Recipient})
	if err != nil {
		return bridgeResult{}, errors.New("bridge routes unavailable")
	}
	routes := matchingSolanaRoutes(res.Routes, toSolana)
	if len(routes) != 1 {
		return bridgeResult{}, errors.New("bridge route unavailable or ambiguous")
	}
	fee, slippage := sdk.FeePolicyFeesExcluded, uint32(100)
	quote, err := client.CreateBridge(sdk.CreateBridgeRequest{
		Recipient: req.Recipient, Route: routes[0], Amount: big.NewInt(req.Amount),
		RefundAddress: &req.Refund, FeePolicy: &fee, MaxSlippageBps: &slippage,
	})
	if err != nil {
		return bridgeResult{}, errors.New("provider rejected the quote")
	}
	info := quote.Info
	result := bridgeResult{
		BridgeID: quote.BridgeId, StatusToken: bridgeString(quote.StatusToken),
		AmountIn: bridgeAmount(info.DepositAmount), AmountOut: bridgeAmount(info.ExpectedReceivedAmount),
		ExpiresAt: int64(info.ExpiresAt), ProviderFee: workerServiceFee(info),
	}
	if result.BridgeID == "" || result.StatusToken == "" || result.AmountIn <= 0 || result.AmountOut <= 0 {
		return bridgeResult{}, errors.New("invalid provider quote")
	}
	if toSolana {
		if info.DestinationAsset != "USDC" || !strings.HasPrefix(quote.PaymentRequest, "lnbc") || quote.PaymentRequest != info.DepositAddress {
			return bridgeResult{}, errors.New("invalid provider invoice")
		}
		result.Invoice = quote.PaymentRequest
	} else {
		if info.DestinationAsset != "BTC" || !validSolanaAddress(info.DepositAddress) ||
			quote.PaymentRequest != info.DepositAddress {
			return bridgeResult{}, errors.New("invalid provider deposit")
		}
		result.Deposit = info.DepositAddress
	}
	return result, nil
}

func workerServiceFee(info sdk.CrossChainReceiveInfo) *bridgeFee {
	amount := info.ServiceFeeAmount
	if amount == nil || !amount.IsInt64() || amount.Sign() < 0 {
		return nil
	}
	asset := bridgeString(info.ServiceFeeAsset)
	if asset == "" && info.ServiceFeeAssetDecimals == nil {
		return &bridgeFee{Amount: amount.Int64(), Asset: "sat"}
	}
	if asset == "USDC" && info.ServiceFeeAssetDecimals != nil && *info.ServiceFeeAssetDecimals == 6 {
		return &bridgeFee{Amount: amount.Int64(), Asset: asset, Decimals: 6}
	}
	return nil
}

func workerStatus(client *sdk.BridgingClient, req bridgeCommand) (bridgeResult, error) {
	if req.BridgeID == "" || len(req.BridgeID) > 256 || req.StatusToken == "" || len(req.StatusToken) > 4096 {
		return bridgeResult{}, errors.New("invalid bridge lookup")
	}
	res, err := client.GetBridgeStatus(sdk.GetBridgeStatusRequest{
		BridgeId: req.BridgeID, StatusToken: &req.StatusToken,
	})
	if err != nil {
		return bridgeResult{}, errors.New("bridge lookup unavailable")
	}
	var status string
	switch res.Status {
	case sdk.BridgeStatusCreated:
		status = "created"
	case sdk.BridgeStatusPending:
		status = "pending"
	case sdk.BridgeStatusRefunding:
		status = "refunding"
	case sdk.BridgeStatusCompleted:
		status = "completed"
	case sdk.BridgeStatusRefunded:
		status = "refunded"
	case sdk.BridgeStatusFailed:
		status = "failed"
	case sdk.BridgeStatusExpired:
		status = "expired"
	default:
		return bridgeResult{}, errors.New("unknown provider state")
	}
	return bridgeResult{
		Status: status, OrderID: bridgeString(res.OrderId), AmountIn: bridgeOptionalAmount(res.AmountIn),
		AmountOut: bridgeOptionalAmount(res.AmountOut), DepositRef: bridgeString(res.DepositTxHash),
		DeliveryRef: bridgeString(res.DeliveryTxHash), RefundRef: bridgeString(res.RefundTxHash),
	}, nil
}

func bridgeString(v *string) string {
	if v != nil {
		return *v
	}
	return ""
}

func bridgeAmount(v *big.Int) int64 {
	if v != nil && v.Sign() > 0 && v.IsInt64() {
		return v.Int64()
	}
	return 0
}

func bridgeOptionalAmount(v **big.Int) int64 {
	if v == nil {
		return 0
	}
	return bridgeAmount(*v)
}
