package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type bitcoinVerboseBlockRPCResponse struct {
	Result *bitcoinVerboseBlock `json:"result"`
	Error  *rpcErrorDetail      `json:"error"`
}

type bitcoinVerboseTransactionRPCResponse struct {
	Result *bitcoinVerboseTransaction `json:"result"`
	Error  *rpcErrorDetail            `json:"error"`
}

type bitcoinVerboseBlock struct {
	Hash   string                      `json:"hash"`
	Height int64                       `json:"height"`
	Tx     []bitcoinVerboseTransaction `json:"tx"`
}

type bitcoinVerboseTransaction struct {
	TxID string                   `json:"txid"`
	Hash string                   `json:"hash"`
	Vin  []bitcoinVerboseTxInput  `json:"vin"`
	Vout []bitcoinVerboseTxOutput `json:"vout"`
}

type bitcoinVerboseTxInput struct {
	TxID     string `json:"txid"`
	Vout     uint32 `json:"vout"`
	Coinbase string `json:"coinbase,omitempty"`
}

type bitcoinVerboseTxOutput struct {
	Value        float64 `json:"value"`
	N            uint32  `json:"n"`
	ScriptPubKey struct {
		Type      string   `json:"type"`
		Address   string   `json:"address"`
		Addresses []string `json:"addresses"`
	} `json:"scriptPubKey"`
}

func parseGraphCloseFundingOutpoint(chanPoint string) (string, uint32, error) {
	parts := strings.Split(strings.TrimSpace(chanPoint), ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid chan_point: %q", chanPoint)
	}
	txid := strings.ToLower(strings.TrimSpace(parts[0]))
	if len(txid) != 64 {
		return "", 0, fmt.Errorf("invalid funding txid in chan_point: %q", chanPoint)
	}
	vout, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid funding vout in chan_point: %q", chanPoint)
	}
	return txid, uint32(vout), nil
}

func fetchBitcoinVerboseBlockByHeightRPC(ctx context.Context, cfg bitcoinRPCConfig, height uint32) (bitcoinVerboseBlock, error) {
	hash, err := fetchBitcoinBlockHashRPC(ctx, cfg.Host, cfg.User, cfg.Pass, height)
	if err != nil {
		return bitcoinVerboseBlock{}, err
	}
	return fetchBitcoinVerboseBlockRPC(ctx, cfg.Host, cfg.User, cfg.Pass, hash)
}

func fetchBitcoinVerboseBlockRPC(ctx context.Context, host, user, pass, hash string) (bitcoinVerboseBlock, error) {
	body, err := fetchBitcoinRPCParams(ctx, host, user, pass, "getblock", []any{hash, 2})
	if err != nil {
		return bitcoinVerboseBlock{}, err
	}
	var payload bitcoinVerboseBlockRPCResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return bitcoinVerboseBlock{}, err
	}
	if payload.Error != nil {
		return bitcoinVerboseBlock{}, errors.New(strings.TrimSpace(payload.Error.Message))
	}
	if payload.Result == nil {
		return bitcoinVerboseBlock{}, fmt.Errorf("bitcoin getblock returned empty result")
	}
	payload.Result.Hash = strings.ToLower(strings.TrimSpace(payload.Result.Hash))
	for index := range payload.Result.Tx {
		payload.Result.Tx[index].TxID = strings.ToLower(strings.TrimSpace(payload.Result.Tx[index].TxID))
		payload.Result.Tx[index].Hash = strings.ToLower(strings.TrimSpace(payload.Result.Tx[index].Hash))
		for vinIndex := range payload.Result.Tx[index].Vin {
			payload.Result.Tx[index].Vin[vinIndex].TxID = strings.ToLower(strings.TrimSpace(payload.Result.Tx[index].Vin[vinIndex].TxID))
		}
	}
	return *payload.Result, nil
}

func fetchBitcoinVerboseTransactionRPC(ctx context.Context, host, user, pass, txid string) (bitcoinVerboseTransaction, error) {
	body, err := fetchBitcoinRPCParams(ctx, host, user, pass, "getrawtransaction", []any{txid, true})
	if err != nil {
		return bitcoinVerboseTransaction{}, err
	}
	var payload bitcoinVerboseTransactionRPCResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return bitcoinVerboseTransaction{}, err
	}
	if payload.Error != nil {
		return bitcoinVerboseTransaction{}, errors.New(strings.TrimSpace(payload.Error.Message))
	}
	if payload.Result == nil {
		return bitcoinVerboseTransaction{}, fmt.Errorf("bitcoin getrawtransaction returned empty result")
	}
	payload.Result.TxID = strings.ToLower(strings.TrimSpace(payload.Result.TxID))
	payload.Result.Hash = strings.ToLower(strings.TrimSpace(payload.Result.Hash))
	for index := range payload.Result.Vin {
		payload.Result.Vin[index].TxID = strings.ToLower(strings.TrimSpace(payload.Result.Vin[index].TxID))
	}
	return *payload.Result, nil
}

func findSpendingTransactionInBlock(block bitcoinVerboseBlock, fundingTxID string, fundingVout uint32) *bitcoinVerboseTransaction {
	fundingTxID = strings.ToLower(strings.TrimSpace(fundingTxID))
	if fundingTxID == "" {
		return nil
	}
	for index := range block.Tx {
		tx := &block.Tx[index]
		for _, vin := range tx.Vin {
			if vin.TxID == fundingTxID && vin.Vout == fundingVout {
				return tx
			}
		}
	}
	return nil
}

func resolveGraphCloseSpendingTransaction(ctx context.Context, cfg bitcoinRPCConfig, closedHeight int, fundingTxID string, fundingVout uint32) (*bitcoinVerboseTransaction, error) {
	for _, height := range graphCloseSearchHeights(closedHeight) {
		block, err := fetchBitcoinVerboseBlockByHeightRPC(ctx, cfg, height)
		if err != nil {
			return nil, err
		}
		if tx := findSpendingTransactionInBlock(block, fundingTxID, fundingVout); tx != nil {
			return tx, nil
		}
	}
	return nil, nil
}

func graphCloseSearchHeights(closedHeight int) []uint32 {
	if closedHeight <= 0 {
		return nil
	}
	heights := make([]uint32, 0, 3)
	seen := make(map[uint32]struct{}, 3)
	for _, raw := range []int{closedHeight, closedHeight - 1, closedHeight + 1} {
		if raw <= 0 {
			continue
		}
		height := uint32(raw)
		if _, ok := seen[height]; ok {
			continue
		}
		seen[height] = struct{}{}
		heights = append(heights, height)
	}
	return heights
}

func classifyGraphCloseTransactionShape(tx bitcoinVerboseTransaction) (string, string, string) {
	outputCount := len(tx.Vout)
	if outputCount == 0 {
		return "unknown", "", "close_tx_has_no_outputs"
	}

	anchorOutputs := 0
	encumberedOutputs := 0
	smallOutputs := 0
	safePayoutOnly := true

	for _, output := range tx.Vout {
		valueSat := bitcoinValueToSats(output.Value)
		scriptType := strings.ToLower(strings.TrimSpace(output.ScriptPubKey.Type))
		if valueSat >= 250 && valueSat <= 450 {
			anchorOutputs++
		}
		if valueSat > 0 && valueSat <= 1000 {
			smallOutputs++
		}
		if graphCloseLooksLikeEncumberedScript(scriptType) {
			encumberedOutputs++
		}
		if !graphCloseLooksLikeDirectPayoutScript(scriptType) {
			safePayoutOnly = false
		}
	}

	switch {
	case anchorOutputs > 0:
		return "force_close", "high", "anchor_outputs_detected"
	case encumberedOutputs > 0 && outputCount >= 2:
		return "force_close", "medium", "encumbered_commitment_outputs"
	case outputCount >= 4:
		return "force_close", "medium", "many_close_outputs"
	case outputCount <= 2 && encumberedOutputs == 0 && safePayoutOnly:
		return "mutual_close", "high", "direct_payout_outputs"
	case outputCount == 3 && encumberedOutputs == 0 && safePayoutOnly && smallOutputs == 0:
		return "mutual_close", "medium", "three_way_direct_payout"
	default:
		return "unknown", "", "close_tx_shape_unresolved"
	}
}

func estimateGraphCloseFeeSat(ctx context.Context, cfg bitcoinRPCConfig, fundingTxID string, fundingVout uint32, closeTx bitcoinVerboseTransaction) (*int64, error) {
	fundingTx, err := fetchBitcoinVerboseTransactionRPC(ctx, cfg.Host, cfg.User, cfg.Pass, fundingTxID)
	if err != nil {
		return nil, err
	}
	if !graphCloseFundingOutputInRange(fundingVout, len(fundingTx.Vout)) {
		return nil, fmt.Errorf("funding vout %d out of range", fundingVout)
	}

	inputSat := bitcoinValueToSats(fundingTx.Vout[fundingVout].Value)
	var outputSat int64
	for _, output := range closeTx.Vout {
		outputSat += bitcoinValueToSats(output.Value)
	}
	feeSat := inputSat - outputSat
	if feeSat < 0 {
		return nil, fmt.Errorf("negative close fee computed")
	}
	return &feeSat, nil
}

func graphCloseFundingOutputInRange(fundingVout uint32, outputCount int) bool {
	return outputCount > 0 && uint64(fundingVout) < uint64(outputCount)
}

func graphCloseLooksLikeDirectPayoutScript(scriptType string) bool {
	switch strings.ToLower(strings.TrimSpace(scriptType)) {
	case "pubkeyhash", "scripthash", "witness_v0_keyhash", "witness_v1_taproot":
		return true
	default:
		return false
	}
}

func graphCloseLooksLikeEncumberedScript(scriptType string) bool {
	scriptType = strings.ToLower(strings.TrimSpace(scriptType))
	switch scriptType {
	case "witness_v0_scripthash", "witness_unknown", "nonstandard":
		return true
	default:
		return false
	}
}

func bitcoinValueToSats(value float64) int64 {
	return int64(math.Round(value * 100_000_000))
}
