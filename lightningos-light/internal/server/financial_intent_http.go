package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const financialIntentHeader = "X-LOS-Financial-Intent"

type financialIntentContextKey struct{}
type financialIntentExecution struct {
	store      *financialIntentStore
	record     *financialIntentRecord
	postCommit func()
}
type financialOperation struct{ scope, code string }

func coreFinancialOperation(path string) (financialOperation, bool) {
	switch path {
	case "/api/wallet/spending-guard":
		return financialOperation{authScopeSpendingGuardPolicy, "spending_guard_reauth_required"}, true
	case "/api/lnops/node-retirement/sessions":
		return financialOperation{authScopeNodeRetirement, nodeRetirementControlReauthRequiredCode}, true
	case "/api/lnops/succession/config":
		return financialOperation{authScopeSuccessionLive, successionLiveControlReauthRequiredCode}, true
	}
	if path == "/api/wallet/send" {
		return financialOperation{authScopeWalletSendExternal, "wallet_send_external_reauth_required"}, true
	}
	if path == "/api/onchain/utxos/bump" {
		return financialOperation{authScopeWalletSendExternal, "utxo_bump_reauth_required"}, true
	}
	if utxoLockRequiresReauth() && (path == "/api/onchain/utxos/lock" || path == "/api/onchain/utxos/unlock") {
		return financialOperation{authScopeWalletSendExternal, "utxo_lock_reauth_required"}, true
	}
	switch path {
	case "/api/wallet/pay", "/api/wallet/pay/validated-route", "/api/wallet/pay/mpp", "/api/chat/send",
		"/api/lnops/channel/open", "/api/lnops/channel/open-batch", "/api/lnops/channel/close", "/api/lnops/channel/pending-open/bump-fee", "/api/lnops/channel/scb/restore":
		return financialOperation{authScopeLightningFunds, "lightning_funds_reauth_required"}, true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "lnops" && parts[3] == "sessions" && parts[4] != "" {
		if parts[2] == "node-retirement" && (parts[5] == "confirm-coop" || parts[5] == "decision") {
			return financialOperation{authScopeNodeRetirement, nodeRetirementControlReauthRequiredCode}, true
		}
		if parts[2] == "balanced-open" && (parts[5] == "propose" || parts[5] == "accept" || parts[5] == "execute") || parts[2] == "close-manager" && (parts[5] == "force-close" || parts[5] == "bump-fee") {
			return financialOperation{authScopeLightningFunds, "lightning_funds_reauth_required"}, true
		}
	}
	return financialOperation{}, false
}

func financialOperationMethod(operation financialOperation) string {
	if operation.scope == authScopeSpendingGuardPolicy {
		return http.MethodPut
	}
	return http.MethodPost
}

func financialOperationConditional(operation financialOperation) bool {
	return operation.scope == authScopeSpendingGuardPolicy || operation.scope == authScopeNodeRetirement || operation.scope == authScopeSuccessionLive
}

func (s *Server) financialIntentService() (*financialIntentStore, error) {
	s.financialIntentOnce.Do(func() {
		if s.financialIntents != nil {
			return
		}
		dir := s.financialIntentDir
		if dir == "" {
			dir = "/var/lib/lightningos/financial-intents"
		}
		s.financialIntents, s.financialIntentErr = openFinancialIntentStore(dir)
	})
	return s.financialIntents, s.financialIntentErr
}

func financialIntentApproved(r *http.Request, scope string) bool {
	execution, ok := r.Context().Value(financialIntentContextKey{}).(*financialIntentExecution)
	return ok && execution.record != nil && (execution.record.BaseScope == scope || execution.record.BaseScope == authScopeLightningFunds && strings.HasPrefix(scope, lnurlInvoiceScopePrefix))
}

func financialIntentChallenge(w http.ResponseWriter, operation financialOperation, id string) {
	payload := map[string]any{"error": "prepare and confirm the exact financial operation before execution", "code": operation.code, "requires_password_confirmation": true, "requires_financial_intent": true}
	if id != "" {
		payload["intent_id"] = id
		payload["scope"] = financialIntentScopePrefix + id
	}
	writeJSON(w, http.StatusPreconditionRequired, payload)
}

func (s *Server) financialIntentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		operation, covered := coreFinancialOperation(path)
		if !covered || r.Method != financialOperationMethod(operation) || s.auth == nil || !s.auth.Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		setNoStore(w)
		session, ok := authSessionFromContext(r.Context())
		if !ok {
			writeErrorCode(w, 401, "auth_required", "authentication required")
			return
		}
		id := r.Header.Get(financialIntentHeader)
		if id == "" {
			// Simulations, policy tightening and disabling live automation keep
			// their existing nonfinancial behavior. If the handler determines
			// approval is needed, requireSensitiveReauth issues this protocol's
			// challenge instead of accepting a reusable broad control grant.
			if financialOperationConditional(operation) {
				next.ServeHTTP(w, r)
				return
			}
			financialIntentChallenge(w, operation, "")
			return
		}
		if !financialIntentIDValid(id) {
			writeErrorCode(w, 400, "financial_intent_invalid", "invalid financial intent identifier")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, financialIntentMaxRecordBytes+1))
		if err != nil || len(raw) > financialIntentMaxRecordBytes {
			writeError(w, 400, "financial request body unavailable")
			return
		}
		payload, err := canonicalFinancialPayload(raw)
		if err != nil {
			writeErrorCode(w, 400, "financial_intent_payload_invalid", err.Error())
			return
		}
		store, err := s.financialIntentService()
		if err != nil {
			writeErrorCode(w, 503, "financial_intent_storage_unavailable", "durable financial intent storage unavailable; no operation submitted")
			return
		}
		if r.Context().Err() != nil {
			return
		}
		record, started, err := store.claim(id, session.ID, path, payload, func(scope string) bool { return s.auth.consumeFinancialApproval(session.ID, scope) }, r.Method)
		switch {
		case errors.Is(err, errFinancialIntentApproval):
			financialIntentChallenge(w, operation, id)
			return
		case errors.Is(err, errFinancialIntentMismatch):
			writeErrorCode(w, 409, "financial_intent_mismatch", err.Error())
			return
		case errors.Is(err, errFinancialIntentExpired):
			writeErrorCode(w, 409, "financial_intent_expired", err.Error())
			return
		case errors.Is(err, errFinancialIntentCancelled):
			writeErrorCode(w, 409, "financial_intent_cancelled", err.Error())
			return
		case errors.Is(err, errFinancialIntentMissing):
			writeErrorCode(w, 404, "financial_intent_not_found", err.Error())
			return
		case errors.Is(err, errFinancialIntentPending):
			writeJSON(w, 409, map[string]any{"error": err.Error(), "code": "financial_intent_pending", "intent_id": id, "state": record.State})
			return
		case err != nil:
			writeErrorCode(w, 503, "financial_intent_storage_unavailable", "financial intent could not be committed; no operation submitted")
			return
		}
		if !started {
			writeFinancialIntentResult(w, record)
			return
		}
		// Once execution is committed, a lost browser connection must not
		// cancel it into an untracked retry. Every RPC retains its own bound.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
		defer cancel()
		execution := &financialIntentExecution{store: store, record: record}
		ctx = context.WithValue(ctx, financialIntentContextKey{}, execution)
		r = r.WithContext(ctx)
		r.Body = io.NopCloser(bytes.NewReader(payload))
		r.ContentLength = int64(len(payload))
		buffer := &financialIntentResponse{header: make(http.Header)}
		finished := false
		defer func() {
			if !finished {
				_ = store.finish(id, 503, []byte(`{"error":"financial execution interrupted; reconcile before retrying","code":"financial_intent_pending"}`), true)
			}
		}()
		next.ServeHTTP(buffer, r)
		if buffer.status == 0 {
			buffer.status = 200
		}
		uncertain := buffer.status >= 500 || buffer.truncated
		if buffer.status >= 400 {
			latest, _, _ := store.get(id)
			if latest != nil && len(latest.Proof) > 0 {
				var proof financialNativeProof
				if json.Unmarshal(latest.Proof, &proof) == nil && proof.DispatchStarted {
					uncertain = true
				}
			}
		}
		if buffer.truncated {
			buffer.status = 503
			buffer.body.Reset()
			buffer.body.WriteString(`{"error":"financial result requires reconciliation","code":"financial_intent_pending"}`)
		}
		if err := store.finish(id, buffer.status, buffer.body.Bytes(), uncertain); err != nil {
			finished = true
			writeJSON(w, 503, map[string]any{"error": "financial result could not be committed; reconcile before retrying", "code": "financial_intent_pending", "intent_id": id})
			return
		}
		finished = true
		if execution.postCommit != nil && !uncertain && buffer.status < 400 {
			execution.postCommit()
		}
		s.recordAuditEventAsync(r, "financial_intent.executed", id, map[string]any{"operation": record.Path, "status": buffer.status, "uncertain": uncertain})
		if contentType := buffer.header.Get("Content-Type"); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(buffer.status)
		w.Write(buffer.body.Bytes())
	})
}

type financialIntentResponse struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	truncated bool
}

func (w *financialIntentResponse) Header() http.Header { return w.header }
func (w *financialIntentResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *financialIntentResponse) Write(raw []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(raw) > 2<<20 {
		w.truncated = true
		return 0, io.ErrShortWrite
	}
	return w.body.Write(raw)
}

func writeFinancialIntentResult(w http.ResponseWriter, record *financialIntentRecord) {
	setNoStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-LOS-Financial-Replay", "true")
	w.WriteHeader(record.ResponseStatus)
	w.Write(record.ResponseBody)
}

func (s *Server) handleFinancialIntentStatus(w http.ResponseWriter, r *http.Request) {
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
	// The node has one administrative identity. A fresh authenticated session
	// may inspect prior results, but it cannot inherit an execution approval.
	session, authenticated := authSessionFromContext(r.Context())
	payload := map[string]any{"intent_id": id, "method": record.Method, "operation": record.Path, "state": record.State, "running": running, "expires_at": record.ExpiresAt, "can_confirm": authenticated && store.approvalAllowed(id, session.ID)}
	var binding financialIntentBinding
	if json.Unmarshal(record.Binding, &binding) == nil {
		payload["summary"] = binding.Summary
	}
	if authenticated && store.approvalAllowed(id, session.ID) {
		if json.Unmarshal(record.Binding, &binding) == nil {
			payload["payload"] = json.RawMessage(record.Request)
			payload["summary"] = binding.Summary
			payload["scope"] = financialIntentScopePrefix + id
		}
	}
	if record.State == financialIntentCompletedState || record.State == financialIntentRejectedState || record.State == financialIntentCancelledState {
		payload["status"] = record.ResponseStatus
		payload["result"] = json.RawMessage(record.ResponseBody)
	}
	writeJSON(w, 200, payload)
}

func (s *Server) handleFinancialIntentCancel(w http.ResponseWriter, r *http.Request) {
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
	_, err = store.cancelDraft(id)
	if errors.Is(err, errFinancialIntentPending) {
		writeErrorCode(w, 409, "financial_intent_pending", "an operation already started and must be reconciled instead of cancelled")
		return
	}
	if errors.Is(err, errFinancialIntentMissing) {
		writeErrorCode(w, 404, "financial_intent_not_found", "financial intent not found")
		return
	}
	if err != nil {
		writeErrorCode(w, 503, "financial_intent_storage_unavailable", "cancellation could not be committed; do not prepare another transfer")
		return
	}
	s.recordAuditEventAsync(r, "financial_intent.cancelled", id, nil)
	writeJSON(w, 200, map[string]any{"intent_id": id, "state": financialIntentCancelledState})
}
