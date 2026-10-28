package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
	"strings"

	"github.com/btcsuite/btcd/wire"
)

var ErrBalancedOpenPrefundUncertain = errors.New("balanced funding result unresolved; retry to reconcile the existing wallet output; no new transfer will be made")

// Write the unique derived script/key and an attempt marker before SendOutputs.
// A lost RPC/DB response must never cause a second prefunding transaction.
func (s *BalancedOpenService) ensureBalancedTransit(ctx context.Context, session BalancedOpenSession, side string) (balancedOpenMetadata, bool, error) {
	fresh, err := s.GetSession(ctx, session.SessionID)
	if err != nil {
		return balancedOpenMetadata{}, false, err
	}
	if err := validateFinancialSnapshot(ctx, "balanced_open", balancedFinancialTerms(fresh)); err != nil {
		return balancedOpenMetadata{}, false, err
	}
	meta, err := decodeBalancedOpenMetadata(fresh.Metadata)
	if err != nil {
		return meta, false, err
	}
	var key balancedOpenKeyDescriptor
	var transit balancedOpenTransitDetails
	if side == balancedOpenRoleInitiator {
		key = meta.InitiatorMultisigKey
		transit = meta.InitiatorTransit
	} else {
		key = meta.AccepterMultisigKey
		transit = meta.AccepterTransit
	}
	if isValidPubkeyHex(key.PublicKey) && hasBalancedTransit(transit, true) {
		return meta, false, nil
	}
	raw, err := decodeBalancedOpenMetadataMap(fresh.Metadata)
	if err != nil {
		return meta, false, err
	}
	marker := side + "_prefund_attempted"
	if _, attempted := raw[marker]; attempted {
		if !isValidPubkeyHex(key.PublicKey) || !isValidPubkeyHex(transit.Key.PublicKey) || !isBalancedOpenScriptHex(transit.OutputScript) || transit.OutputSat <= 0 {
			return meta, false, ErrBalancedOpenPrefundUncertain
		}
		utxos, err := s.lnd.ListOnchainUtxos(ctx, 0, 2147483647)
		if err != nil {
			return meta, false, ErrBalancedOpenPrefundUncertain
		}
		found := false
		for _, utxo := range utxos {
			if strings.EqualFold(utxo.PkScript, transit.OutputScript) && utxo.AmountSat == transit.OutputSat && isBalancedOpenTxID(utxo.Txid) {
				if found {
					return meta, false, ErrBalancedOpenPrefundUncertain
				}
				found = true
				transit.TxID = utxo.Txid
				transit.Vout = utxo.Vout
			}
		}
		if !found {
			// LND's custom key-family outputs need not appear in ListUnspent.
			// Its originating wallet transaction still records the exact output
			// and our label. Recover the identity without authoring a new send.
			history, historyErr := s.lnd.ListAllTransactions(ctx, 0, -1)
			if historyErr != nil {
				return meta, false, ErrBalancedOpenPrefundUncertain
			}
			transit, historyErr = balancedTransitFromWalletHistory(transit, history, fmt.Sprintf("balanced-open-%s-%s", fresh.SessionID, side))
			if historyErr != nil {
				return meta, false, ErrBalancedOpenPrefundUncertain
			}
		}
		if side == balancedOpenRoleInitiator {
			meta.InitiatorTransit = transit
		} else {
			meta.AccepterTransit = transit
		}
		if err := s.persistBalancedTransit(ctx, fresh.SessionID, side, key, transit); err != nil {
			return meta, false, ErrBalancedOpenPrefundUncertain
		}
		return meta, true, nil
	}
	states := []string{balancedOpenStateSessionCreated, balancedOpenStateProposalSent}
	if side == balancedOpenRoleAccepter {
		states = []string{balancedOpenStateProposalReceived, balancedOpenStateFundingTxHalfSigned}
	}
	if fresh.Role != side || isBalancedOpenTerminalState(fresh.State) {
		return meta, false, ErrBalancedOpenInvalidAction
	}
	rate := balancedEffectiveFeeRate(fresh.FeeRateSatVb)
	amount, err := balancedTransitContribution(fresh.CapacitySat, rate)
	if err != nil {
		return meta, false, err
	}
	if _, err = s.ensureBalancedOnchainBudget(ctx, amount, balancedOpenAnchorSafetySat); err != nil {
		return meta, false, err
	}
	multisig, err := s.lnd.DeriveNextKey(ctx, balancedOpenMultiSigKeyFamily)
	if err != nil {
		return meta, false, err
	}
	derived, err := s.lnd.DeriveNextKey(ctx, balancedOpenTransitKeyFamily)
	if err != nil {
		return meta, false, err
	}
	script, err := balancedP2WPKHScriptHex(derived.PublicKey)
	if err != nil {
		return meta, false, err
	}
	key = balancedOpenKeyDescriptor{PublicKey: multisig.PublicKey, Family: multisig.Family, Index: multisig.Index}
	transit = balancedOpenTransitDetails{OutputSat: amount, OutputScript: script, Key: balancedOpenKeyDescriptor{PublicKey: derived.PublicKey, Family: derived.Family, Index: derived.Index}}
	patch, err := json.Marshal(map[string]any{"execution_mode": balancedOpenExecutionModeDual, side + "_multisig_key": key, side + "_transit": transit, marker: true})
	if err != nil {
		return meta, false, err
	}
	if err := commitFinancialWorkflow(ctx, fresh.SessionID); err != nil {
		return meta, false, err
	}
	tag, err := s.db.Exec(ctx, `update balanced_open_sessions set metadata=metadata || $3::jsonb,updated_at=now() where session_id=$1 and not(metadata ? $2) and role=$4 and state=any($5::text[]) and metadata=$6::jsonb`, fresh.SessionID, marker, string(patch), side, states, string(fresh.Metadata))
	if err != nil {
		return meta, false, err
	}
	if tag.RowsAffected() != 1 {
		return meta, false, ErrBalancedOpenPrefundUncertain
	}
	result, err := s.lnd.SendOutputScript(ctx, lndclient.SendOutputScriptParams{SatPerVbyte: rate, OutputScriptHex: script, AmountSat: amount, Label: fmt.Sprintf("balanced-open-%s-%s", fresh.SessionID, side), MinConfs: 0, SpendUnconfirmed: true})
	if err != nil {
		return meta, false, ErrBalancedOpenPrefundUncertain
	}
	transit.TxID = result.TxID
	transit.Vout = result.Vout
	if err = s.persistBalancedTransit(ctx, fresh.SessionID, side, key, transit); err != nil {
		return meta, false, ErrBalancedOpenPrefundUncertain
	}
	if side == balancedOpenRoleInitiator {
		meta.InitiatorMultisigKey = key
		meta.InitiatorTransit = transit
	} else {
		meta.AccepterMultisigKey = key
		meta.AccepterTransit = transit
	}
	meta.ExecutionMode = balancedOpenExecutionModeDual
	return meta, true, nil
}

// Wallet history can identify outputs outside the managed UTXO set. It is not
// a chain-wide proof of unspentness: reject known spends and let LND validate
// any later spend. Never repeat SendOutputs if the outcome remains uncertain.
func balancedTransitFromWalletHistory(transit balancedOpenTransitDetails, history []*lnrpc.Transaction, label string) (balancedOpenTransitDetails, error) {
	script, err := hex.DecodeString(transit.OutputScript)
	if err != nil || len(script) == 0 || transit.OutputSat <= 0 || label == "" {
		return transit, ErrBalancedOpenPrefundUncertain
	}
	spent := make(map[wire.OutPoint]bool)
	candidates := make(map[wire.OutPoint]bool)
	for _, record := range history {
		if record == nil || record.NumConfirmations < 0 {
			continue
		}
		if record.RawTxHex == "" {
			if record.Label == label {
				return transit, ErrBalancedOpenPrefundUncertain
			}
			continue
		}
		raw, err := hex.DecodeString(record.RawTxHex)
		if err != nil {
			return transit, ErrBalancedOpenPrefundUncertain
		}
		var tx wire.MsgTx
		if err := tx.Deserialize(bytes.NewReader(raw)); err != nil || len(tx.TxIn) == 0 || len(tx.TxOut) == 0 {
			return transit, ErrBalancedOpenPrefundUncertain
		}
		txid := tx.TxHash()
		if record.TxHash != "" && !strings.EqualFold(record.TxHash, txid.String()) {
			return transit, ErrBalancedOpenPrefundUncertain
		}
		for _, input := range tx.TxIn {
			spent[input.PreviousOutPoint] = true
		}
		if record.Label != label {
			continue
		}
		for index, output := range tx.TxOut {
			if output.Value == transit.OutputSat && bytes.Equal(output.PkScript, script) {
				candidates[wire.OutPoint{Hash: txid, Index: uint32(index)}] = true
			}
		}
	}
	if len(candidates) != 1 {
		return transit, ErrBalancedOpenPrefundUncertain
	}
	for outpoint := range candidates {
		if spent[outpoint] {
			return transit, ErrBalancedOpenPrefundUncertain
		}
		transit.TxID = outpoint.Hash.String()
		transit.Vout = outpoint.Index
	}
	return transit, nil
}
func (s *BalancedOpenService) persistBalancedTransit(ctx context.Context, id, side string, key balancedOpenKeyDescriptor, transit balancedOpenTransitDetails) error {
	raw, err := json.Marshal(map[string]any{side + "_multisig_key": key, side + "_transit": transit})
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `update balanced_open_sessions set metadata=metadata || $2::jsonb,updated_at=now() where session_id=$1`, id, string(raw))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrBalancedOpenSessionNotFound
	}
	return nil
}
func (s *BalancedOpenService) reconcileBalancedPrefund(ctx context.Context, session BalancedOpenSession, meta balancedOpenMetadata) (balancedOpenMetadata, error) {
	raw, err := decodeBalancedOpenMetadataMap(session.Metadata)
	if err != nil {
		return meta, err
	}
	if _, exists := raw[session.Role+"_prefund_attempted"]; !exists {
		return meta, nil
	}
	next, _, err := s.ensureBalancedTransit(ctx, session, session.Role)
	return next, err
}
