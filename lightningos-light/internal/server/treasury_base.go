package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	treasuryBaseChainID = "0x2105"
	treasuryBaseUSDC    = "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913"
	treasuryTransfer    = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	treasuryBaseRPC     = "https://base-rpc.publicnode.com"
)

type treasuryRPC struct {
	client *http.Client
	url    string
}

func newTreasuryRPC() treasuryRPC {
	return treasuryRPC{
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		url: treasuryBaseRPC,
	}
}

type treasuryBaseReceipt struct {
	Hash           string            `json:"transactionHash"`
	Status         string            `json:"status"`
	Block          string            `json:"blockNumber"`
	Logs           []treasuryBaseLog `json:"logs"`
	GasUsed        string            `json:"gasUsed"`
	EffectivePrice string            `json:"effectiveGasPrice"`
	L1Fee          string            `json:"l1Fee"`
}

type treasuryBaseLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
	Removed bool     `json:"removed"`
}

func (rpc treasuryRPC) call(ctx context.Context, method string, params []any, result any) error {
	if params == nil {
		params = []any{}
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpc.url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := rpc.client.Do(req)
	if err != nil {
		return errors.New("Base RPC unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("Base RPC unavailable")
	}
	const maxResponse = 512 << 10
	var envelope struct {
		Version string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	contents, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(contents) > maxResponse || json.Unmarshal(contents, &envelope) != nil ||
		envelope.Version != "2.0" || envelope.ID != 1 || len(envelope.Error) != 0 || len(envelope.Result) == 0 {
		return errors.New("invalid Base RPC response")
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return errors.New("invalid Base RPC result")
	}
	return nil
}

func treasuryNormalizeHex(value string, size int) string {
	value = strings.ToLower(value)
	if len(value) == 2*size {
		value = "0x" + value
	}
	if !strings.HasPrefix(value, "0x") || len(value) != 2+2*size {
		return ""
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return ""
	}
	return value
}

func treasuryBaseAddress(value string) string {
	addr := treasuryNormalizeHex(value, 20)
	if !strings.HasPrefix(value, "0x") || addr == "0x"+strings.Repeat("0", 40) {
		return ""
	}
	return addr
}

func treasuryAddressTopic(address string) string {
	return "0x" + strings.Repeat("0", 24) + address[2:]
}

// A provider completion alone does not establish that native USDC reached the
// quoted address. Count only logs from the fixed token contract and, for a
// reverse deposit, the exact wallet that funded the order.
func (rpc treasuryRPC) verifyTransfer(ctx context.Context, txHash, from, recipient string, micros int64) (bool, error) {
	received, err := rpc.transferCredit(ctx, txHash, from, recipient, micros)
	return err == nil && received == micros, err
}

func (rpc treasuryRPC) transferCredit(ctx context.Context, txHash, from, recipient string, micros int64) (int64, error) {
	txHash = treasuryNormalizeHex(txHash, 32)
	recipient = treasuryBaseAddress(recipient)
	if txHash == "" || recipient == "" || micros <= 0 || from != "" && treasuryBaseAddress(from) == "" {
		return 0, errors.New("invalid Base transfer proof")
	}
	var chain string
	if err := rpc.call(ctx, "eth_chainId", nil, &chain); err != nil || chain != treasuryBaseChainID {
		return 0, errors.New("could not verify Base chain ID")
	}
	var receipt *treasuryBaseReceipt
	if err := rpc.call(ctx, "eth_getTransactionReceipt", []any{txHash}, &receipt); err != nil {
		return 0, err
	}
	if receipt == nil || receipt.Status != "0x1" || !strings.EqualFold(receipt.Hash, txHash) || receipt.Block == "" {
		return 0, nil
	}
	block, err := strconv.ParseUint(strings.TrimPrefix(receipt.Block, "0x"), 16, 64)
	tip, tipErr := rpc.quantity(ctx, "eth_blockNumber", nil)
	if err != nil || tipErr != nil || tip < block || tip-block < 9 {
		return 0, nil
	}
	wantRecipient := treasuryAddressTopic(recipient)
	var received int64
	for _, event := range receipt.Logs {
		if event.Removed || !strings.EqualFold(event.Address, treasuryBaseUSDC) || len(event.Topics) != 3 ||
			!strings.EqualFold(event.Topics[0], treasuryTransfer) || !strings.EqualFold(event.Topics[2], wantRecipient) {
			continue
		}
		if from != "" && !strings.EqualFold(event.Topics[1], treasuryAddressTopic(strings.ToLower(from))) {
			continue
		}
		data := treasuryNormalizeHex(event.Data, 32)
		if data == "" {
			return 0, errors.New("invalid USDC transfer amount")
		}
		amount, err := strconv.ParseInt(data[2:], 16, 64)
		if err != nil || amount > micros-received {
			return 0, nil
		}
		received += amount
	}
	return received, nil
}

func (rpc treasuryRPC) actualBaseFee(ctx context.Context, tx string) string {
	hash := treasuryNormalizeHex(tx, 32)
	var receipt *treasuryBaseReceipt
	if hash == "" || rpc.call(ctx, "eth_getTransactionReceipt", []any{hash}, &receipt) != nil ||
		receipt == nil || !strings.EqualFold(receipt.Hash, hash) {
		return ""
	}
	gas, gasErr := strconv.ParseUint(strings.TrimPrefix(receipt.GasUsed, "0x"), 16, 64)
	price, priceOK := new(big.Int).SetString(strings.TrimPrefix(receipt.EffectivePrice, "0x"), 16)
	l1, l1OK := new(big.Int).SetString(strings.TrimPrefix(receipt.L1Fee, "0x"), 16)
	if gasErr != nil || !priceOK || price.Sign() < 0 || !l1OK || l1.Sign() < 0 || receipt.Block == "" {
		return ""
	}
	operator, err := rpc.quantity(ctx, "eth_call", []any{map[string]string{
		"to": treasuryGasOracle, "data": fmt.Sprintf("0x275aedd2%064x", gas),
	}, receipt.Block})
	if err != nil {
		return ""
	}
	total := new(big.Int).Mul(new(big.Int).SetUint64(gas), price)
	return total.Add(total, l1).Add(total, new(big.Int).SetUint64(operator)).String()
}

func treasuryTransferData(recipient string, micros int64) (string, error) {
	address := treasuryBaseAddress(recipient)
	if address == "" || micros <= 0 {
		return "", errors.New("invalid USDC deposit")
	}
	return fmt.Sprintf("0xa9059cbb%064s%064x", address[2:], micros), nil
}
