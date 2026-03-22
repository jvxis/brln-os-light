package lndclient

import (
	"context"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/mempool"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

var (
	openChannelPreviewFundingScript = append([]byte{txscript.OP_0, 32}, make([]byte, 32)...)
	openChannelPreviewChangeScript  = append([]byte{txscript.OP_0, 20}, make([]byte, 20)...)
)

type OpenChannelPreview struct {
	LocalFundingSat       int64  `json:"local_funding_sat"`
	PushSat               int64  `json:"push_sat"`
	FeeSat                int64  `json:"fee_sat"`
	TotalDebitSat         int64  `json:"total_debit_sat"`
	SpendableSat          int64  `json:"spendable_sat"`
	SpendableRemainingSat int64  `json:"spendable_remaining_sat"`
	SelectedInputCount    int    `json:"selected_input_count"`
	SelectedInputSat      int64  `json:"selected_input_sat"`
	EstimatedVbytes       int64  `json:"estimated_vbytes"`
	SatPerVbyte           int64  `json:"sat_per_vbyte"`
	EnoughFunds           bool   `json:"enough_funds"`
	Exact                 bool   `json:"exact"`
	HasChange             bool   `json:"has_change"`
	ReferenceOnly         bool   `json:"reference_only"`
	MessageCode           string `json:"message_code,omitempty"`
	Message               string `json:"message,omitempty"`
}

func (c *Client) PreviewOpenChannel(ctx context.Context, localFundingSat int64, pushSat int64, satPerVbyte int64) (OpenChannelPreview, error) {
	preview := OpenChannelPreview{
		LocalFundingSat: localFundingSat,
		PushSat:         pushSat,
		SatPerVbyte:     satPerVbyte,
	}
	if localFundingSat <= 0 {
		preview.MessageCode = "invalid_local_funding"
		preview.Message = "local_funding_sat must be positive"
		return preview, nil
	}
	if pushSat < 0 {
		preview.MessageCode = "invalid_push_sat"
		preview.Message = "push_sat must be zero or positive"
		return preview, nil
	}
	if pushSat > localFundingSat {
		preview.MessageCode = "push_exceeds_funding"
		preview.Message = "push_sat cannot exceed local_funding_sat"
		return preview, nil
	}
	if satPerVbyte <= 0 {
		preview.MessageCode = "invalid_sat_per_vbyte"
		preview.Message = "sat_per_vbyte must be positive"
		return preview, nil
	}

	utxos, err := c.ListOnchainUtxos(ctx, 1, previewOnchainMaxConfs)
	if err != nil {
		return preview, err
	}
	return buildOpenChannelPreview(preview, utxos), nil
}

func buildOpenChannelPreview(preview OpenChannelPreview, utxos []OnchainUtxo) OpenChannelPreview {
	sorted := append([]OnchainUtxo(nil), utxos...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].AmountSat == sorted[j].AmountSat {
			return strings.TrimSpace(sorted[i].Outpoint) < strings.TrimSpace(sorted[j].Outpoint)
		}
		return sorted[i].AmountSat > sorted[j].AmountSat
	})

	for _, utxo := range sorted {
		preview.SpendableSat += utxo.AmountSat
	}
	preview.SpendableRemainingSat = preview.SpendableSat

	if len(sorted) == 0 {
		preview = applyOpenChannelReferenceEstimate(preview)
		preview.MessageCode = "no_confirmed_utxos"
		preview.Message = "no confirmed UTXOs available"
		return preview
	}
	if preview.SpendableSat < preview.LocalFundingSat {
		preview = applyOpenChannelReferenceEstimate(preview)
		preview.MessageCode = "insufficient_confirmed_balance"
		preview.Message = "insufficient confirmed balance"
		return preview
	}

	changeDust := mempool.GetDustThreshold(wire.NewTxOut(1, openChannelPreviewChangeScript))
	inputs := make([]previewInput, 0, len(sorted))
	selectedSat := int64(0)

	for _, utxo := range sorted {
		selectedSat += utxo.AmountSat
		inputs = append(inputs, previewInput{
			pkScript:    decodeHexOrNil(utxo.PkScript),
			addressType: utxo.AddressType,
		})

		withChangeVbytes := estimatePreviewVirtualSize(inputs, openChannelPreviewOutputs(true))
		withChangeFee := withChangeVbytes * preview.SatPerVbyte
		changeSat := selectedSat - preview.LocalFundingSat - withChangeFee
		if changeSat >= changeDust {
			preview.SelectedInputCount = len(inputs)
			preview.SelectedInputSat = selectedSat
			preview.EstimatedVbytes = withChangeVbytes
			preview.FeeSat = withChangeFee
			preview.TotalDebitSat = preview.LocalFundingSat + preview.FeeSat
			preview.SpendableRemainingSat = maxInt64(0, preview.SpendableSat-preview.TotalDebitSat)
			preview.EnoughFunds = preview.TotalDebitSat <= preview.SpendableSat
			preview.Exact = false
			preview.HasChange = true
			return preview
		}

		withoutChangeVbytes := estimatePreviewVirtualSize(inputs, openChannelPreviewOutputs(false))
		withoutChangeFeeFloor := withoutChangeVbytes * preview.SatPerVbyte
		if selectedSat < preview.LocalFundingSat+withoutChangeFeeFloor {
			continue
		}

		extraFeeSat := selectedSat - preview.LocalFundingSat - withoutChangeFeeFloor
		if extraFeeSat <= changeDust {
			preview.SelectedInputCount = len(inputs)
			preview.SelectedInputSat = selectedSat
			preview.EstimatedVbytes = withoutChangeVbytes
			preview.FeeSat = selectedSat - preview.LocalFundingSat
			preview.TotalDebitSat = preview.LocalFundingSat + preview.FeeSat
			preview.SpendableRemainingSat = maxInt64(0, preview.SpendableSat-preview.TotalDebitSat)
			preview.EnoughFunds = preview.TotalDebitSat <= preview.SpendableSat
			preview.Exact = false
			preview.HasChange = false
			if extraFeeSat > 0 {
				preview.MessageCode = "dust_change_absorbed"
				preview.Message = "change would be dust and is absorbed into the fee estimate"
			}
			return preview
		}
	}

	preview = applyOpenChannelReferenceEstimate(preview)
	preview.MessageCode = "insufficient_balance_for_fees"
	preview.Message = "insufficient confirmed balance for funding plus fees"
	return preview
}

func openChannelPreviewOutputs(includeChange bool) []*wire.TxOut {
	outputs := []*wire.TxOut{
		wire.NewTxOut(1, openChannelPreviewFundingScript),
	}
	if includeChange {
		outputs = append(outputs, wire.NewTxOut(1, openChannelPreviewChangeScript))
	}
	return outputs
}

func applyOpenChannelReferenceEstimate(preview OpenChannelPreview) OpenChannelPreview {
	inputs := []previewInput{{addressType: "p2wkh"}}
	preview.EstimatedVbytes = estimatePreviewVirtualSize(inputs, openChannelPreviewOutputs(true))
	preview.FeeSat = preview.EstimatedVbytes * preview.SatPerVbyte
	preview.TotalDebitSat = preview.LocalFundingSat + preview.FeeSat
	preview.SpendableRemainingSat = maxInt64(0, preview.SpendableSat-preview.TotalDebitSat)
	preview.HasChange = true
	preview.ReferenceOnly = true
	preview.Exact = false
	preview.EnoughFunds = preview.TotalDebitSat > 0 && preview.SpendableSat >= preview.TotalDebitSat
	return preview
}

func maxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
