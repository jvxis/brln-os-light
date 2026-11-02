package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/btcsuite/btcd/wire"
	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"
)

type onchainGuardRequest struct {
	onchainGuardSettings
	ConfirmPassword string `json:"confirm_password,omitempty"`
}

func (s *Server) handleOnchainSpendingGuardGet(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	store, err := s.financialIntentService()
	if err != nil {
		writeErrorCode(w, 503, "onchain_spending_guard_unavailable", errOnchainGuardUnavailable.Error())
		return
	}
	status, err := store.onchainGuardStatus()
	if err != nil {
		writeErrorCode(w, 503, "onchain_spending_guard_unavailable", errOnchainGuardUnavailable.Error())
		return
	}
	writeJSON(w, 200, status)
}

func (s *Server) handleOnchainSpendingGuardUpdate(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	var request onchainGuardRequest
	if readJSON(r, &request) != nil {
		writeError(w, 400, "invalid on-chain guard settings")
		return
	}
	if err := validateOnchainGuardSettings(request.onchainGuardSettings); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	store, err := s.financialIntentService()
	if err != nil {
		writeErrorCode(w, 503, "onchain_spending_guard_unavailable", errOnchainGuardUnavailable.Error())
		return
	}
	authorized := s.auth == nil || !s.auth.Enabled() || financialIntentApproved(r, authScopeOnchainGuardPolicy)
	err = store.updateOnchainGuard(request.onchainGuardSettings, authorized)
	if errors.Is(err, ErrSpendingGuardReauth) {
		if !s.requireSensitiveReauth(w, r, authScopeOnchainGuardPolicy, request.ConfirmPassword, "onchain_spending_guard_reauth_required", "password confirmation required to relax on-chain limits") {
			return
		}
		err = store.updateOnchainGuard(request.onchainGuardSettings, true)
	}
	if err != nil {
		writeErrorCode(w, 503, "onchain_spending_guard_unavailable", errOnchainGuardUnavailable.Error())
		return
	}
	s.recordAuditEventAsync(r, "wallet.onchain_spending_guard.updated", "onchain", map[string]any{"enabled": request.Enabled, "max_send_sat": request.MaxSendSat, "rolling_24h_limit_sat": request.Rolling24hLimitSat})
	status, err := store.onchainGuardStatus()
	if err != nil {
		writeErrorCode(w, 503, "onchain_spending_guard_unavailable", errOnchainGuardUnavailable.Error())
		return
	}
	writeJSON(w, 200, status)
}

func writeOnchainGuardError(w http.ResponseWriter, err error) bool {
	var limit *onchainGuardLimitError
	if errors.As(err, &limit) {
		writeJSON(w, 429, map[string]any{"code": "onchain_spending_guard_limit_exceeded", "error": limit.Error(), "reason": limit.Reason, "requested_sat": limit.Requested, "remaining_sat": limit.Remaining})
		return true
	}
	if errors.Is(err, errOnchainGuardUnavailable) {
		writeErrorCode(w, 503, "onchain_spending_guard_unavailable", errOnchainGuardUnavailable.Error())
		return true
	}
	return false
}

func (s *Server) beginOnchainGuard(ctx context.Context, store *financialIntentStore, id string, binding financialIntentBinding, label string) (bool, error) {
	status, err := store.onchainGuardStatus()
	if err != nil {
		return false, errOnchainGuardUnavailable
	}
	if !status.Enabled {
		return false, nil
	}
	if s.lnd != nil {
		controlled, err := s.lnd.IsControlledDefaultWalletAddress(ctx, binding.Summary.Destination)
		if err == nil && controlled {
			return false, nil
		}
	}
	return store.reserveOnchainGuard(id, binding, label)
}

// There is no financial-intent middleware when login is disabled. The guard
// still applies server-side; pin the funding snapshot and persist a reservation
// before native dispatch. A native error keeps the allowance reserved.
func (s *Server) executeUnboundOnchainGuard(ctx context.Context, request walletSendRequest) (bool, string, error) {
	store, err := s.financialIntentService()
	if err != nil {
		return true, "", errOnchainGuardUnavailable
	}
	status, err := store.onchainGuardStatus()
	if err != nil {
		return true, "", errOnchainGuardUnavailable
	}
	if !status.Enabled {
		return false, "", nil
	}
	id, err := newFinancialIntentID()
	if err != nil {
		return true, "", errOnchainGuardUnavailable
	}
	payload, _ := json.Marshal(request)
	normalized, binding, err := s.prepareOnchainFinancialIntent(ctx, id, payload)
	if err != nil {
		return true, "", err
	}
	var pinned walletSendRequest
	if json.Unmarshal(normalized, &pinned) != nil {
		return true, "", errOnchainGuardUnavailable
	}
	active, err := s.beginOnchainGuard(ctx, store, id, binding, request.Label)
	if err != nil {
		return true, "", err
	}
	label := binding.NativeLabel
	if !active {
		label = request.Label
	}
	txid, err := s.financialNativeOperations().sendOnchain(ctx, lndclient.SendCoinsParams{Address: pinned.Address, AmountSat: pinned.AmountSat, SatPerVbyte: pinned.SatPerVbyte, SendAll: pinned.SweepAll, Outpoints: pinned.Outpoints, Label: label})
	if err == nil && txid == "" {
		err = errors.New("native send outcome unavailable; reconcile before retrying")
	}
	if err == nil && txid != "" && active {
		if s.completeOnchainGuard(ctx, store, id, binding, txid) {
			s.restoreOnchainGuardLabel(ctx, txid, request.Label)
		}
	}
	return true, txid, err
}

func (s *Server) completeOnchainGuard(ctx context.Context, store *financialIntentStore, id string, binding financialIntentBinding, txid string) bool {
	amount := binding.Summary.AmountSat
	if binding.Summary.AmountMode == "selected_maximum" {
		transactions, err := s.financialNativeOperations().findOnchain(ctx, binding.NativeLabel, binding.StartHeight)
		if err != nil || len(transactions) != 1 || transactions[0].GetTxHash() != txid {
			return false
		}
		amount, err = onchainGuardTransactionAmount(binding, transactions[0])
		if err != nil {
			return false
		}
	}
	return store.finishOnchainGuard(id, txid, amount, false) == nil
}

// Validate native inputs/outputs before reducing a sweep's conservative hold.
// The destination output, never input debit or change, consumes the budget.
func onchainGuardTransactionAmount(binding financialIntentBinding, transaction *lnrpc.Transaction) (int64, error) {
	if err := validateFinancialOnchainResult(binding, transaction); err != nil {
		return 0, err
	}
	raw, _ := hex.DecodeString(transaction.GetRawTxHex())
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return 0, err
	}
	script, _ := hex.DecodeString(binding.DestinationScript)
	for _, output := range tx.TxOut {
		if bytes.Equal(output.PkScript, script) {
			return output.Value, nil
		}
	}
	return 0, errOnchainGuardUnavailable
}

func (s *Server) restoreOnchainGuardLabel(ctx context.Context, txid, label string) {
	if label == "" {
		label = "external"
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_ = s.financialNativeOperations().restoreLabel(cleanup, txid, label)
}

func (s *Server) reconcileOnchainSpendingGuard() {
	// Do not eagerly create/lock financial storage for a node that has never
	// configured this optional guard. In particular, keep startup unchanged.
	dir := s.financialIntentDir
	if dir == "" {
		dir = "/var/lib/lightningos/financial-intents"
	}
	if _, err := os.Lstat(filepath.Join(dir, onchainGuardFile)); err != nil {
		return
	}
	store, err := s.financialIntentService()
	if err != nil {
		return
	}
	entries, err := store.pendingOnchainGuard()
	if err != nil || len(entries) == 0 || s.lnd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.shutdownContext(), 20*time.Second)
	defer cancel()
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		// A crash before the existing durable native barrier is evidence that
		// this authenticated send was never dispatched. Age alone is not.
		record, running, recordErr := store.get(entry.ID)
		if recordErr == nil && !running && record.State == financialIntentUncertainState && len(record.Proof) == 0 {
			_ = store.finishOnchainGuard(entry.ID, "", 0, true)
			continue
		}
		transactions, err := s.financialNativeOperations().findOnchain(ctx, entry.Binding.NativeLabel, entry.Binding.StartHeight)
		if err != nil || len(transactions) != 1 {
			continue
		}
		tx := transactions[0]
		amount, err := onchainGuardTransactionAmount(entry.Binding, tx)
		if err != nil {
			continue
		}
		if tx.GetNumConfirmations() <= 0 {
			accepted, err := s.financialNativeOperations().mempoolContains(ctx, tx.GetTxHash())
			if err != nil || !accepted {
				continue
			}
		}
		if store.finishOnchainGuard(entry.ID, tx.GetTxHash(), amount, false) == nil && (recordErr != nil || record.State == financialIntentCompletedState) {
			s.restoreOnchainGuardLabel(ctx, tx.GetTxHash(), entry.OriginalLabel)
		}
	}
}
