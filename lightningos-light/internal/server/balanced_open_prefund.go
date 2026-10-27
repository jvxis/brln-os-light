package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lightningos-light/internal/lndclient"
	"strings"
)

var ErrBalancedOpenPrefundUncertain = errors.New("balanced funding result unresolved; retry to reconcile the existing wallet output; no new transfer will be made")

// Write the unique derived script/key and an attempt marker before SendOutputs.
// A lost RPC/DB response must never cause a second prefunding transaction.
func (s *BalancedOpenService) ensureBalancedTransit(ctx context.Context, session BalancedOpenSession, side string) (balancedOpenMetadata, bool, error) {
	fresh, err := s.GetSession(ctx, session.SessionID)
	if err != nil {
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
			return meta, false, ErrBalancedOpenPrefundUncertain
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
	tag, err := s.db.Exec(ctx, `update balanced_open_sessions set metadata=metadata || $3::jsonb,updated_at=now() where session_id=$1 and not(metadata ? $2) and role=$4 and state=any($5::text[])`, fresh.SessionID, marker, string(patch), side, states)
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
