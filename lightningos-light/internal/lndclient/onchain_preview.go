package lndclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/mempool"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/walletrpc"
)

const previewOnchainMaxConfs int32 = 2147483647

const (
	redeemP2PKHInputSize           = 32 + 4 + 1 + 107 + 4
	redeemP2WPKHInputSize          = 32 + 4 + 1 + 0 + 4
	redeemP2TRInputSize            = 32 + 4 + 1 + 0 + 4
	redeemNestedP2WPKHScriptSize   = 1 + 1 + 1 + 20
	redeemNestedP2WPKHInputSize    = 32 + 4 + 1 + redeemNestedP2WPKHScriptSize + 4
	redeemP2WPKHInputWitnessWeight = 1 + 1 + 73 + 1 + 33
	redeemP2TRInputWitnessWeight   = 1 + 1 + 65
)

type OnchainSendPreview struct {
	Address            string `json:"address"`
	SweepAll           bool   `json:"sweep_all"`
	RequestedAmountSat int64  `json:"requested_amount_sat"`
	RecipientAmountSat int64  `json:"recipient_amount_sat"`
	FeeSat             int64  `json:"fee_sat"`
	ChangeSat          int64  `json:"change_sat"`
	TotalDebitSat      int64  `json:"total_debit_sat"`
	SpendableSat       int64  `json:"spendable_sat"`
	SpendableUtxoCount int    `json:"spendable_utxo_count"`
	SelectedInputCount int    `json:"selected_input_count"`
	SelectedInputSat   int64  `json:"selected_input_sat"`
	EstimatedVbytes    int64  `json:"estimated_vbytes"`
	SatPerVbyte        int64  `json:"sat_per_vbyte"`
	EnoughFunds        bool   `json:"enough_funds"`
	Exact              bool   `json:"exact"`
	Message            string `json:"message,omitempty"`
}

type previewInput struct {
	pkScript    []byte
	addressType string
}

func (c *Client) PreviewOnchainSend(ctx context.Context, address string, amountSat int64, satPerVbyte int64, sweepAll bool, outpoints []string) (OnchainSendPreview, error) {
	preview := OnchainSendPreview{
		Address:            strings.TrimSpace(address),
		SweepAll:           sweepAll,
		RequestedAmountSat: amountSat,
		SatPerVbyte:        satPerVbyte,
	}
	if preview.Address == "" {
		preview.Message = "destination address required"
		return preview, nil
	}
	if satPerVbyte <= 0 {
		preview.Message = "sat_per_vbyte must be positive"
		return preview, nil
	}
	if !sweepAll && amountSat <= 0 {
		preview.Message = "amount_sat must be positive"
		return preview, nil
	}

	utxos, err := c.ListOnchainUtxos(ctx, 1, previewOnchainMaxConfs)
	if err != nil {
		return preview, err
	}

	if filter, hasFilter, err := buildOutpointFilter(outpoints); err != nil {
		preview.Message = err.Error()
		return preview, nil
	} else if hasFilter {
		filtered := utxos[:0]
		for _, utxo := range utxos {
			if _, keep := filter[strings.ToLower(strings.TrimSpace(utxo.Outpoint))]; keep {
				filtered = append(filtered, utxo)
			}
		}
		if len(filtered) != len(filter) {
			preview.Message = "one or more selected outpoints are unavailable"
			preview.SpendableUtxoCount = len(filtered)
			return preview, nil
		}
		utxos = filtered
	}

	preview.SpendableUtxoCount = len(utxos)

	utxoByOutpoint := make(map[string]OnchainUtxo, len(utxos))
	for _, utxo := range utxos {
		preview.SpendableSat += utxo.AmountSat
		if utxo.Outpoint != "" {
			utxoByOutpoint[utxo.Outpoint] = utxo
		}
	}
	if len(utxos) == 0 {
		preview.Message = "no confirmed UTXOs available"
		return preview, nil
	}

	script, err := previewAddressScript(preview.Address)
	if err != nil {
		preview.Message = "invalid destination address"
		return preview, nil
	}

	if sweepAll {
		return buildSweepAllPreview(preview, utxos, script), nil
	}
	if preview.SpendableSat < amountSat {
		preview.Message = "insufficient confirmed balance"
		return preview, nil
	}

	return c.previewFundedSend(ctx, preview, utxoByOutpoint, utxos)
}

func (c *Client) previewFundedSend(ctx context.Context, preview OnchainSendPreview, utxoByOutpoint map[string]OnchainUtxo, candidateUtxos []OnchainUtxo) (OnchainSendPreview, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return preview, err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	template := &walletrpc.TxTemplate{
		Outputs: map[string]uint64{
			preview.Address: uint64(preview.RequestedAmountSat),
		},
	}
	// When the caller pinned a UTXO selection, force LND to fund the PSBT
	// from exactly those inputs. Without this LND would fall back to its own
	// coin-selection strategy and could pick other UTXOs.
	if len(candidateUtxos) > 0 && len(candidateUtxos) <= len(utxoByOutpoint) {
		inputs := make([]*lnrpc.OutPoint, 0, len(candidateUtxos))
		for _, utxo := range candidateUtxos {
			point, err := parseOutPoint(utxo.Outpoint)
			if err != nil {
				continue
			}
			inputs = append(inputs, point)
		}
		if len(inputs) > 0 {
			template.Inputs = inputs
		}
	}
	req := &walletrpc.FundPsbtRequest{
		Template: &walletrpc.FundPsbtRequest_Raw{
			Raw: template,
		},
		Fees: &walletrpc.FundPsbtRequest_SatPerVbyte{
			SatPerVbyte: uint64(preview.SatPerVbyte),
		},
		MinConfs: 1,
	}

	resp, err := client.FundPsbt(ctx, req)
	if err != nil {
		if isPreviewSoftFailure(err) {
			preview.Message = strings.TrimSpace(err.Error())
			return preview, nil
		}
		return preview, err
	}
	defer releasePreviewLeases(ctx, client, resp.GetLockedUtxos())

	packet, err := psbt.NewFromRawBytes(bytes.NewReader(resp.GetFundedPsbt()), false)
	if err != nil {
		return preview, err
	}
	if packet == nil || packet.UnsignedTx == nil {
		return preview, errors.New("empty funded psbt")
	}

	leaseByOutpoint := make(map[string]*walletrpc.UtxoLease, len(resp.GetLockedUtxos()))
	inputScripts := make([]previewInput, 0, len(packet.UnsignedTx.TxIn))
	for _, lease := range resp.GetLockedUtxos() {
		if lease == nil {
			continue
		}
		_, _, outpoint := walletKitOutpointInfo(lease.GetOutpoint())
		if outpoint == "" {
			continue
		}
		leaseByOutpoint[outpoint] = lease
	}

	outputTotal := int64(0)
	for _, txOut := range packet.UnsignedTx.TxOut {
		if txOut == nil {
			continue
		}
		outputTotal += txOut.Value
	}

	changeIndex := int(resp.GetChangeOutputIndex())
	for _, txIn := range packet.UnsignedTx.TxIn {
		if txIn == nil {
			continue
		}
		outpoint := fmt.Sprintf("%s:%d", strings.ToLower(txIn.PreviousOutPoint.Hash.String()), txIn.PreviousOutPoint.Index)
		if lease := leaseByOutpoint[outpoint]; lease != nil {
			preview.SelectedInputSat += int64(lease.GetValue())
			inputScripts = append(inputScripts, previewInput{pkScript: append([]byte(nil), lease.GetPkScript()...)})
			continue
		}
		if utxo, ok := utxoByOutpoint[outpoint]; ok {
			preview.SelectedInputSat += utxo.AmountSat
			inputScripts = append(inputScripts, previewInput{
				pkScript:    decodeHexOrNil(utxo.PkScript),
				addressType: utxo.AddressType,
			})
		}
	}

	preview.SelectedInputCount = len(packet.UnsignedTx.TxIn)
	preview.FeeSat = preview.SelectedInputSat - outputTotal
	if preview.FeeSat < 0 {
		preview.FeeSat = 0
	}
	preview.ChangeSat = txOutputValueAt(packet.UnsignedTx.TxOut, changeIndex)
	preview.RecipientAmountSat = preview.RequestedAmountSat
	preview.TotalDebitSat = preview.RecipientAmountSat + preview.FeeSat
	preview.EstimatedVbytes = estimatePreviewVirtualSize(inputScripts, packet.UnsignedTx.TxOut)
	preview.EnoughFunds = preview.TotalDebitSat <= preview.SpendableSat && preview.RecipientAmountSat > 0
	preview.Exact = true
	return preview, nil
}

func buildSweepAllPreview(preview OnchainSendPreview, utxos []OnchainUtxo, script []byte) OnchainSendPreview {
	inputs := make([]previewInput, 0, len(utxos))
	for _, utxo := range utxos {
		preview.SelectedInputSat += utxo.AmountSat
		inputs = append(inputs, previewInput{
			pkScript:    decodeHexOrNil(utxo.PkScript),
			addressType: utxo.AddressType,
		})
	}
	preview.SelectedInputCount = len(inputs)

	outputs := []*wire.TxOut{wire.NewTxOut(1, script)}
	preview.EstimatedVbytes = estimatePreviewVirtualSize(inputs, outputs)
	preview.FeeSat = preview.EstimatedVbytes * preview.SatPerVbyte
	preview.TotalDebitSat = preview.SelectedInputSat
	preview.RecipientAmountSat = preview.TotalDebitSat - preview.FeeSat
	dustThreshold := mempool.GetDustThreshold(wire.NewTxOut(1, script))

	if preview.RecipientAmountSat <= dustThreshold {
		preview.RecipientAmountSat = 0
		preview.Message = "remaining amount after fees would be dust"
		preview.EnoughFunds = false
	} else {
		preview.EnoughFunds = true
	}
	preview.Exact = false
	return preview
}

func previewAddressScript(address string) ([]byte, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return nil, errors.New("address required")
	}

	networks := []*chaincfg.Params{
		&chaincfg.MainNetParams,
		&chaincfg.TestNet3Params,
		&chaincfg.RegressionNetParams,
		&chaincfg.SigNetParams,
	}
	for _, net := range networks {
		decoded, err := btcutil.DecodeAddress(trimmed, net)
		if err != nil || decoded == nil || !decoded.IsForNet(net) {
			continue
		}
		return txscript.PayToAddrScript(decoded)
	}

	return nil, errors.New("invalid address")
}

func estimatePreviewVirtualSize(inputs []previewInput, outputs []*wire.TxOut) int64 {
	numP2PKH := 0
	numP2TR := 0
	numP2WPKH := 0
	numNestedP2WPKH := 0
	for _, input := range inputs {
		switch classifyPreviewInput(input) {
		case "p2tr":
			numP2TR++
		case "p2wkh":
			numP2WPKH++
		case "np2wkh":
			numNestedP2WPKH++
		default:
			numP2PKH++
		}
	}

	baseSize := 8 +
		wire.VarIntSerializeSize(uint64(len(inputs))) +
		wire.VarIntSerializeSize(uint64(len(outputs))) +
		numP2PKH*redeemP2PKHInputSize +
		numP2WPKH*redeemP2WPKHInputSize +
		numP2TR*redeemP2TRInputSize +
		numNestedP2WPKH*redeemNestedP2WPKHInputSize
	for _, txOut := range outputs {
		if txOut == nil {
			continue
		}
		baseSize += txOut.SerializeSize()
	}

	witnessWeight := 0
	witnessInputs := numP2TR + numP2WPKH + numNestedP2WPKH
	if witnessInputs > 0 {
		witnessWeight = 2 +
			wire.VarIntSerializeSize(uint64(witnessInputs)) +
			numP2WPKH*redeemP2WPKHInputWitnessWeight +
			numP2TR*redeemP2TRInputWitnessWeight +
			numNestedP2WPKH*redeemP2WPKHInputWitnessWeight
	}

	return int64(baseSize + (witnessWeight+blockchain.WitnessScaleFactor-1)/blockchain.WitnessScaleFactor)
}

func classifyPreviewInput(input previewInput) string {
	if len(input.pkScript) > 0 {
		switch {
		case txscript.IsPayToTaproot(input.pkScript):
			return "p2tr"
		case txscript.IsPayToWitnessPubKeyHash(input.pkScript):
			return "p2wkh"
		case txscript.IsPayToScriptHash(input.pkScript):
			return "np2wkh"
		default:
			return "p2pkh"
		}
	}

	switch strings.ToLower(strings.TrimSpace(input.addressType)) {
	case "p2tr":
		return "p2tr"
	case "p2wkh":
		return "p2wkh"
	case "np2wkh":
		return "np2wkh"
	default:
		return "p2pkh"
	}
}

func txOutputValueAt(outputs []*wire.TxOut, idx int) int64 {
	if idx < 0 || idx >= len(outputs) || outputs[idx] == nil {
		return 0
	}
	return outputs[idx].Value
}

func decodeHexOrNil(value string) []byte {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil
	}
	return raw
}

func isPreviewSoftFailure(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lower, "insufficient") ||
		strings.Contains(lower, "not enough") ||
		strings.Contains(lower, "dust") ||
		strings.Contains(lower, "no utxos") ||
		strings.Contains(lower, "invalid address")
}

func buildOutpointFilter(outpoints []string) (map[string]struct{}, bool, error) {
	if len(outpoints) == 0 {
		return nil, false, nil
	}
	filter := make(map[string]struct{}, len(outpoints))
	for _, raw := range outpoints {
		trimmed := strings.ToLower(strings.TrimSpace(raw))
		if trimmed == "" {
			continue
		}
		if _, err := parseOutPoint(trimmed); err != nil {
			return nil, true, fmt.Errorf("invalid outpoint %q", raw)
		}
		filter[trimmed] = struct{}{}
	}
	if len(filter) == 0 {
		return nil, false, nil
	}
	return filter, true, nil
}

func releasePreviewLeases(ctx context.Context, client walletrpc.WalletKitClient, leases []*walletrpc.UtxoLease) {
	for _, lease := range leases {
		if lease == nil || lease.GetOutpoint() == nil || len(lease.GetId()) == 0 {
			continue
		}
		_, _ = client.ReleaseOutput(ctx, &walletrpc.ReleaseOutputRequest{
			Id:       lease.GetId(),
			Outpoint: lease.GetOutpoint(),
		})
	}
}
