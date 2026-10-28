package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/wire"
	"github.com/go-chi/chi/v5"
	"lightningos-light/lnrpc"
	"strings"
)

// Reconciliation observes native state and never dispatches another transfer.
func (s *Server) handleFinancialIntentReconcile(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	id := chi.URLParam(r, "id")
	if !financialIntentIDValid(id) {
		writeErrorCode(w, 400, "financial_intent_invalid", "invalid financial intent identifier")
		return
	}
	store, err := s.financialIntentService()
	if err != nil {
		writeErrorCode(w, 503, "financial_intent_storage_unavailable", "financial intent storage unavailable")
		return
	}
	record, running, err := store.get(id)
	if err != nil {
		writeErrorCode(w, 404, "financial_intent_not_found", "financial intent not found")
		return
	}
	if record.State == financialIntentCompletedState || record.State == financialIntentRejectedState {
		writeFinancialIntentResult(w, record)
		return
	}
	if running || record.State != financialIntentUncertainState {
		writeErrorCode(w, 409, "financial_intent_pending", "financial execution is still running or has not started")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if s.auth != nil && s.auth.Enabled() {
		session, ok := authSessionFromContext(r.Context())
		if !ok || s.auth.limiter == nil {
			writeErrorCode(w, 401, "auth_required", "authentication required")
			return
		}
		allowed, retry := s.auth.limiter.allow("financial_reconcile:"+auditCorrelationID(session.ID), s.auth.currentTime(), 4, 1.0/5)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
			writeErrorCode(w, 429, "financial_intent_rate_limited", "too many reconciliation requests; try again later")
			return
		}
	}
	status, body, err := s.observeFinancialIntent(ctx, record)
	if err != nil {
		if latest, _, getErr := store.get(id); getErr == nil && (latest.State == financialIntentCompletedState || latest.State == financialIntentRejectedState) {
			writeFinancialIntentResult(w, latest)
			return
		}
		writeJSON(w, 409, map[string]any{"code": "financial_intent_pending", "error": "native outcome remains uncertain; no new transfer was submitted", "intent_id": id})
		return
	}
	resolved, err := store.reconcile(id, status, body)
	if err != nil {
		writeErrorCode(w, 503, "financial_intent_storage_unavailable", "native result could not be committed; no new transfer was submitted")
		return
	}
	s.recordAuditEventAsync(r, "financial_intent.reconciled", id, map[string]any{"operation": record.Path, "status": resolved.ResponseStatus})
	if resolved.ResponseStatus == 200 && resolved.Path == "/api/wallet/send" {
		var request walletSendRequest
		var result struct {
			Txid string `json:"txid"`
		}
		if json.Unmarshal(resolved.Request, &request) == nil && json.Unmarshal(resolved.ResponseBody, &result) == nil && result.Txid != "" {
			label := request.Label
			if label == "" {
				label = "external"
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
			if err := s.financialNativeOperations().restoreLabel(cleanup, result.Txid, label); err != nil {
				s.recordAuditEventAsync(r, "financial_intent.label_restore_failed", id, map[string]any{"txid": result.Txid})
			}
			cancel()
		}
	}
	writeFinancialIntentResult(w, resolved)
}

func (s *Server) observeFinancialIntent(ctx context.Context, record *financialIntentRecord) (int, []byte, error) {
	var binding financialIntentBinding
	if json.Unmarshal(record.Binding, &binding) != nil {
		return 0, nil, errFinancialIntentPending
	}
	if binding.Kind == "invoice_payment" || binding.Kind == "chat_keysend" {
		return s.observeFinancialInvoice(ctx, record, binding)
	}
	if binding.Kind == "channel_open" || binding.Kind == "channel_open_batch" {
		return s.observeFinancialChannelOpen(ctx, record, binding)
	}
	if binding.Kind != "onchain_send" {
		return s.observeFinancialWorkflow(ctx, record, binding)
	}
	if len(record.Proof) == 0 {
		// The native dispatch barrier was never crossed. Do not reuse approval.
		return 409, []byte(`{"code":"financial_intent_not_submitted","error":"native transfer was not submitted; prepare and confirm again"}`), nil
	}
	var proof financialNativeProof
	if json.Unmarshal(record.Proof, &proof) != nil || !proof.DispatchStarted || proof.Kind != binding.Kind || proof.NativeLabel != "los-intent:"+record.ID || proof.NativeLabel != binding.NativeLabel {
		return 0, nil, errFinancialIntentPending
	}
	ops := s.financialNativeOperations()
	transactions, err := ops.findOnchain(ctx, binding.NativeLabel, binding.StartHeight)
	if err != nil || len(transactions) != 1 {
		return 0, nil, errFinancialIntentPending
	}
	transaction := transactions[0]
	if err := validateFinancialOnchainResult(binding, transaction); err != nil {
		return 0, nil, err
	}
	if transaction.GetNumConfirmations() <= 0 {
		accepted, err := ops.mempoolContains(ctx, transaction.GetTxHash())
		if err != nil || !accepted {
			return 0, nil, errFinancialIntentPending
		}
	}
	body, _ := json.Marshal(map[string]string{"txid": transaction.GetTxHash()})
	return 200, body, nil
}

func (s *Server) observeFinancialInvoice(ctx context.Context, record *financialIntentRecord, binding financialIntentBinding) (int, []byte, error) {
	if len(record.Proof) == 0 {
		return 409, []byte(`{"code":"financial_intent_not_submitted","error":"native payment was not submitted; prepare and confirm again"}`), nil
	}
	var proof financialNativeProof
	if json.Unmarshal(record.Proof, &proof) != nil || proof.Kind != binding.Kind || !proof.DispatchStarted || s.lnd == nil {
		return 0, nil, errFinancialIntentPending
	}
	if binding.Kind == "chat_keysend" {
		hash, err := hex.DecodeString(proof.PaymentHash)
		if err != nil || len(hash) != 32 {
			return 0, nil, errFinancialIntentPending
		}
		binding.Summary.PaymentHash = proof.PaymentHash
	}
	if binding.Summary.PaymentHash == "" {
		return 0, nil, errFinancialIntentPending
	}
	details, found, err := s.lnd.TrackPaymentDetails(ctx, binding.Summary.PaymentHash)
	if err != nil || !found || details.PaymentHash != binding.Summary.PaymentHash {
		return 0, nil, errFinancialIntentPending
	}
	switch strings.ToUpper(details.Status) {
	case "SUCCEEDED":
		if details.ValueMsat != binding.Summary.AmountMsat || binding.Summary.MaxFeeSat == nil || details.FeeMsat < 0 || details.FeeMsat > *binding.Summary.MaxFeeSat*1000 {
			return 0, nil, errFinancialIntentPending
		}
		if binding.Summary.MaxFeeMsat != nil && details.FeeMsat > *binding.Summary.MaxFeeMsat {
			return 0, nil, errFinancialIntentPending
		}
		if binding.Kind == "chat_keysend" {
			var request financialChatRequest
			if json.Unmarshal(record.Request, &request) != nil {
				return 0, nil, errFinancialIntentPending
			}
			msg := ChatMessage{Timestamp: details.CreatedAt, PeerPubkey: request.PeerPubkey, Direction: "out", Message: request.Message, Status: "sent", PaymentHash: proof.PaymentHash, AmountSat: binding.Summary.AmountSat, IdentitySource: "local", SenderVerified: true}
			body, _ := json.Marshal(msg)
			return 200, body, nil
		}
		return 200, []byte(`{"ok":true}`), nil
	case "FAILED":
		return 409, []byte(`{"code":"financial_intent_payment_failed","error":"LND reported that the original payment failed"}`), nil
	default:
		return 0, nil, errFinancialIntentPending
	}
}

func validateFinancialOnchainResult(binding financialIntentBinding, transaction *lnrpc.Transaction) error {
	invalid := errors.New("native transaction does not prove the approved financial operation")
	if transaction == nil || transaction.GetLabel() != binding.NativeLabel || binding.Summary.MaxFeeSat == nil {
		return invalid
	}
	raw, err := hex.DecodeString(transaction.GetRawTxHex())
	if err != nil || len(raw) == 0 || len(raw) > 4_000_000 {
		return invalid
	}
	reader := bytes.NewReader(raw)
	var tx wire.MsgTx
	if tx.Deserialize(reader) != nil || reader.Len() != 0 || tx.TxHash().String() != transaction.GetTxHash() || len(tx.TxIn) == 0 || len(tx.TxOut) == 0 || len(tx.TxOut) > 2 {
		return invalid
	}
	inputs := make(map[string]int64)
	for _, input := range binding.Inputs {
		inputs[input.Outpoint] = input.AmountSat
	}
	var totalIn, totalOut, destination int64
	for _, input := range tx.TxIn {
		point := input.PreviousOutPoint.String()
		value, ok := inputs[point]
		if !ok || value <= 0 || value > 21_000_000*100_000_000-totalIn {
			return invalid
		}
		delete(inputs, point)
		totalIn += value
	}
	script, err := hex.DecodeString(binding.DestinationScript)
	if err != nil || len(script) == 0 {
		return invalid
	}
	owned := map[int64]bool{}
	for _, output := range transaction.GetOutputDetails() {
		if output.GetIsOurAddress() {
			owned[output.GetOutputIndex()] = true
		}
	}
	destinations := 0
	for i, output := range tx.TxOut {
		if output.Value < 0 || output.Value > totalIn-totalOut {
			return invalid
		}
		totalOut += output.Value
		if bytes.Equal(output.PkScript, script) {
			destination += output.Value
			destinations++
		} else if !owned[int64(i)] {
			return invalid
		}
	}
	if destinations != 1 || destination <= 0 || totalIn-totalOut > *binding.Summary.MaxFeeSat {
		return invalid
	}
	if binding.Summary.AmountMode != "selected_maximum" && destination != binding.Summary.AmountSat {
		return invalid
	}
	return nil
}
