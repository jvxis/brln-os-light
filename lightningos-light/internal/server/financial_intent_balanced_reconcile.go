package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Financial completion and protocol completion are different facts. A lost
// prefunding/signing response can leave a known wallet output but an unfinished
// proposal. Locate that exact output without calling the funding/signing/send
// methods again, and record an explicit partial result. Continuing the workflow
// then requires a new review of its updated, now-funded snapshot.
func (s *Server) observeBalancedFinancialPrefund(ctx context.Context, record *financialIntentRecord, binding financialIntentBinding, session BalancedOpenSession) (int, []byte, error) {
	if s.lnd == nil || !balancedFinancialContinuationMatches(binding.Snapshot, session) {
		return 0, nil, errFinancialIntentPending
	}
	meta, err := decodeBalancedOpenMetadata(session.Metadata)
	if err != nil {
		return 0, nil, errFinancialIntentPending
	}
	key, transit := meta.InitiatorMultisigKey, meta.InitiatorTransit
	if session.Role == balancedOpenRoleAccepter {
		key, transit = meta.AccepterMultisigKey, meta.AccepterTransit
	}
	if !isValidPubkeyHex(key.PublicKey) || !isValidPubkeyHex(transit.Key.PublicKey) || !isBalancedOpenScriptHex(transit.OutputScript) || transit.OutputSat <= 0 {
		return 0, nil, errFinancialIntentPending
	}
	expected, err := balancedTransitContribution(session.CapacitySat, balancedEffectiveFeeRate(session.FeeRateSatVb))
	if err != nil || expected != transit.OutputSat {
		return 0, nil, errFinancialIntentPending
	}
	utxos, err := s.lnd.ListOnchainUtxos(ctx, 0, 2147483647)
	if err != nil {
		return 0, nil, errFinancialIntentPending
	}
	known := false
	for _, utxo := range utxos {
		if !strings.EqualFold(utxo.PkScript, transit.OutputScript) || utxo.AmountSat != transit.OutputSat || !isBalancedOpenTxID(utxo.Txid) {
			continue
		}
		if known || transit.TxID != "" && (transit.TxID != utxo.Txid || transit.Vout != utxo.Vout) {
			return 0, nil, errFinancialIntentPending
		}
		transit.TxID, transit.Vout = utxo.Txid, utxo.Vout
		known = true
	}
	if !known {
		history, err := s.lnd.ListAllTransactions(ctx, 0, -1)
		if err != nil {
			return 0, nil, errFinancialIntentPending
		}
		found, err := balancedTransitFromWalletHistory(transit, history, fmt.Sprintf("balanced-open-%s-%s", session.SessionID, session.Role))
		if err != nil {
			return 0, nil, errFinancialIntentPending
		}
		if transit.TxID != "" && (found.TxID != transit.TxID || found.Vout != transit.Vout) {
			return 0, nil, errFinancialIntentPending
		}
		transit = found
	}
	svc, _ := s.balancedOpenService()
	if svc == nil {
		return 0, nil, errFinancialIntentPending
	}
	if err := svc.persistBalancedTransit(ctx, session.SessionID, session.Role, key, transit); err != nil {
		return 0, nil, errFinancialIntentPending
	}
	current, err := svc.GetSession(ctx, session.SessionID)
	if err != nil || !balancedFinancialContinuationMatches(binding.Snapshot, current) {
		return 0, nil, errFinancialIntentPending
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		return 0, nil, errFinancialIntentPending
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(encoded, &result) != nil {
		return 0, nil, errFinancialIntentPending
	}
	result["financial_intent_needs_review"] = json.RawMessage(`true`)
	result["financial_intent_message"], _ = json.Marshal("Existing workflow funding was located. Review and confirm continuation; no second transfer was submitted.")
	return financialObservedResult(200, result)
}

func balancedFinancialContinuationMatches(snapshot json.RawMessage, current BalancedOpenSession) bool {
	var original, now map[string]json.RawMessage
	if json.Unmarshal(snapshot, &original) != nil || json.Unmarshal(financialSnapshot(balancedFinancialTerms(current)), &now) != nil {
		return false
	}
	oldMeta, newMeta := original["metadata"], now["metadata"]
	delete(original, "metadata")
	delete(now, "metadata")
	if string(financialSnapshot(original)) != string(financialSnapshot(now)) {
		return false
	}
	var before, after map[string]json.RawMessage
	if json.Unmarshal(oldMeta, &before) != nil || json.Unmarshal(newMeta, &after) != nil {
		return false
	}
	for name, value := range before {
		if name == "last_execution_err" || name == "last_execution_error_unix" {
			continue
		}
		if name == current.Role+"_transit" {
			var oldTransit, newTransit balancedOpenTransitDetails
			if json.Unmarshal(value, &oldTransit) != nil || json.Unmarshal(after[name], &newTransit) != nil {
				return false
			}
			if oldTransit.TxID == "" {
				oldTransit.TxID, oldTransit.Vout = newTransit.TxID, newTransit.Vout
			}
			if string(financialSnapshot(oldTransit)) != string(financialSnapshot(newTransit)) {
				return false
			}
			continue
		}
		if name == "pending_chan_id" && string(value) == `""` {
			continue
		}
		if string(financialSnapshot(map[string]json.RawMessage{"value": value})) != string(financialSnapshot(map[string]json.RawMessage{"value": after[name]})) {
			return false
		}
	}
	return true
}
