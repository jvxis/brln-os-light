package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const treasuryPaymentScopePrefix = "stablecoin_treasury:"

var treasuryCredentialPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

func (s *Server) checkTreasuryRequest(w http.ResponseWriter, r *http.Request) bool {
	setNoStore(w)
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || s.db == nil || s.lnd == nil {
		writeError(w, http.StatusServiceUnavailable, "stablecoin treasury unavailable")
		return false
	}
	if _, ok := authSessionFromContext(r.Context()); s.auth == nil || !s.auth.Enabled() || !ok {
		writeError(w, http.StatusUnauthorized, "enable login protection to use stablecoin treasury")
		return false
	}
	return true
}

func readTreasuryJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 12<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(dst) != nil || dec.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid treasury request")
		return false
	}
	return true
}

func (s *Server) handleTreasuryStatus(w http.ResponseWriter, r *http.Request) {
	if !s.checkTreasuryRequest(w, r) {
		return
	}
	var installed, enabled bool
	var addr string
	err := s.db.QueryRow(r.Context(), `SELECT installed, enabled, lightning_address
		FROM stablecoin_treasury_config WHERE singleton`).Scan(&installed, &enabled, &addr)
	if err != nil && !treasuryStateNotCreated(err) {
		writeError(w, http.StatusServiceUnavailable, "treasury state unavailable")
		return
	}
	key, _ := readEnvFileValue(secretsPath, treasuryKeyEnv)
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": installed, "enabled": enabled, "configured": key != "",
		"lightning_address": addr, "provider_available": treasuryBridgeAvailable(),
	})
}

func (s *Server) handleTreasuryConfigure(w http.ResponseWriter, r *http.Request) {
	if !s.checkTreasuryRequest(w, r) {
		return
	}
	var input struct {
		APIKey    string `json:"api_key"`
		Lightning string `json:"lightning_address"`
		Password  string `json:"confirm_password"`
	}
	if !readTreasuryJSON(w, r, &input) {
		return
	}
	input.Lightning = strings.TrimSpace(input.Lightning)
	input.APIKey = strings.TrimSpace(input.APIKey)
	if len(input.APIKey) > 4096 || input.APIKey != "" && !treasuryCredentialPattern.MatchString(input.APIKey) ||
		input.Lightning != "" && !isLightningAddress(input.Lightning) {
		writeError(w, http.StatusBadRequest, "invalid Breez API key or Lightning Address")
		return
	}
	if !s.requireSensitiveReauth(w, r, treasuryConfigScope, input.Password, "treasury_config_reauth_required", "confirm your LightningOS password") {
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "treasury state unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var installed bool
	if err := tx.QueryRow(ctx, `SELECT installed FROM stablecoin_treasury_config WHERE singleton FOR UPDATE`).Scan(&installed); err != nil || !installed {
		writeError(w, http.StatusConflict, "install stablecoin treasury first")
		return
	}
	key, _ := readEnvFileValue(secretsPath, treasuryKeyEnv)
	if input.APIKey != "" && input.APIKey != key {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stablecoin_treasury_orders
		    WHERE state NOT IN ('completed','refunded','failed','expired') AND
		    (state <> 'quoted' OR direction='to_lightning' OR expires_at > now()))`).Scan(&active); err != nil || active {
			writeError(w, http.StatusConflict, "resolve pending treasury orders before changing the key")
			return
		}
		if ensureSecretsDir() != nil || writeEnvFileValue(secretsPath, treasuryKeyEnv, input.APIKey) != nil {
			writeError(w, http.StatusServiceUnavailable, "could not securely store the Breez API key")
			return
		}
		key = input.APIKey
	}
	if _, err := tx.Exec(ctx, `UPDATE stablecoin_treasury_config SET lightning_address=$1 WHERE singleton`, input.Lightning); err != nil || tx.Commit(ctx) != nil {
		writeError(w, http.StatusServiceUnavailable, "could not save the Lightning Address")
		return
	}
	s.recordAuditEventAsync(r, "treasury.configured", treasuryAppID, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"configured": key != ""})
}

func (s *Server) handleTreasuryQuote(w http.ResponseWriter, r *http.Request) {
	if !s.checkTreasuryRequest(w, r) {
		return
	}
	var input treasuryQuoteRequest
	if !readTreasuryJSON(w, r, &input) {
		return
	}
	if !treasuryBridgeAvailable() {
		writeError(w, http.StatusServiceUnavailable, "isolated Breez bridge unavailable")
		return
	}
	session, _ := authSessionFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	order, err := s.makeTreasuryQuote(ctx, session.ID, input)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAuditEventAsync(r, "treasury.quoted", order.ID, map[string]any{"direction": order.Direction, "amount_in": order.AmountIn, "amount_out": order.AmountOut})
	writeJSON(w, http.StatusCreated, order)
}

func (s *Server) handleTreasuryOrders(w http.ResponseWriter, r *http.Request) {
	if !s.checkTreasuryRequest(w, r) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT public_data,state FROM stablecoin_treasury_orders ORDER BY created_at DESC LIMIT 100`)
	if treasuryStateNotCreated(err) {
		writeJSON(w, http.StatusOK, []treasuryOrder{})
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "treasury history unavailable")
		return
	}
	defer rows.Close()
	orders := make([]treasuryOrder, 0)
	for rows.Next() {
		var raw []byte
		var state string
		var order treasuryOrder
		if rows.Scan(&raw, &state) != nil || json.Unmarshal(raw, &order) != nil {
			writeError(w, http.StatusServiceUnavailable, "treasury history unavailable")
			return
		}
		order.State = state
		orders = append(orders, order)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "treasury history unavailable")
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

func (s *Server) handleTreasuryReconcile(w http.ResponseWriter, r *http.Request) {
	if !s.checkTreasuryRequest(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if !financialIntentIDValid(id) {
		writeError(w, http.StatusBadRequest, "invalid treasury order")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	record, err := readTreasuryOrder(s.db.QueryRow(ctx, selectTreasuryOrder, id))
	if err != nil {
		writeError(w, http.StatusNotFound, "treasury order unavailable")
		return
	}
	order, err := s.reconcileTreasuryOrder(ctx, record)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not reconcile treasury order")
		return
	}
	if order.State != record.State {
		s.recordAuditEventAsync(r, "treasury.reconciled", id, map[string]any{"state": order.State, "direction": order.Direction})
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *Server) handleTreasuryPay(w http.ResponseWriter, r *http.Request) {
	if !s.checkTreasuryRequest(w, r) {
		return
	}
	var input struct {
		Password string `json:"confirm_password"`
	}
	if !readTreasuryJSON(w, r, &input) {
		return
	}
	id := chi.URLParam(r, "id")
	if !financialIntentIDValid(id) {
		writeError(w, http.StatusBadRequest, "invalid treasury order")
		return
	}
	if !treasuryBridgeAvailable() {
		writeError(w, http.StatusServiceUnavailable, "isolated Breez bridge unavailable")
		return
	}
	if !s.requireSensitiveReauth(w, r, treasuryPaymentScopePrefix+id, input.Password, "treasury_pay_reauth_required", "confirm your LightningOS password to pay this quote") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lndWalletPaymentTimeout)
	defer cancel()
	session, _ := authSessionFromContext(r.Context())
	order, err := s.claimTreasuryPayment(ctx, id, session.ID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	reservation, paymentHash, reserveErr := s.reserveInvoiceSpending(ctx, order.private.Invoice, order.AmountIn, order.MaxFee, "wallet")
	if reserveErr != nil || order.invoiceHash != paymentHash {
		failed := order.treasuryOrder
		failed.State = "failed"
		saveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		if reserveErr == nil {
			s.finishSpendingReservation(reservation, paymentHash, errFinancialNativeNotDispatched)
		}
		_, _ = s.persistTreasuryObservation(saveCtx, order, failed)
		if !writeSpendingGuardError(w, reserveErr) {
			writeError(w, http.StatusConflict, "could not authorize the quoted Lightning debit")
		}
		return
	}
	if time.Until(order.Expires) < time.Second {
		s.finishSpendingReservation(reservation, paymentHash, errFinancialNativeNotDispatched)
		expired := order.treasuryOrder
		expired.State = "expired"
		saveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_, _ = s.persistTreasuryObservation(saveCtx, order, expired)
		writeError(w, http.StatusConflict, "quote expired before payment")
		return
	}
	s.recordAuditEventAsync(r, "treasury.lightning_dispatch", id, map[string]any{"amount_sat": order.AmountIn, "fee_limit_sat": order.MaxFee})
	paymentErr := s.lnd.PayInvoice(ctx, order.private.Invoice, nil, order.MaxFee)
	s.finishSpendingReservation(reservation, paymentHash, paymentErr)
	s.recordWalletActivity(paymentHash)
	// A transport error is ambiguous. The already-claimed order can only
	// advance from observations of the exact LND payment and provider order.
	if paymentErr != nil {
		writeJSON(w, http.StatusAccepted, order.treasuryOrder)
		return
	}
	saveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	funded := order.treasuryOrder
	funded.State = "funded"
	funded, err = s.persistTreasuryObservation(saveCtx, order, funded)
	if err != nil {
		writeJSON(w, http.StatusAccepted, order.treasuryOrder)
		return
	}
	writeJSON(w, http.StatusAccepted, funded)
}

func (s *Server) handleTreasuryBasePreparation(w http.ResponseWriter, r *http.Request) {
	if !s.checkTreasuryRequest(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if !financialIntentIDValid(id) {
		writeError(w, http.StatusBadRequest, "invalid treasury order")
		return
	}
	session, _ := authSessionFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	signing, err := s.prepareTreasuryBase(ctx, id, session.ID)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, signing)
}

func (s *Server) handleTreasuryBaseBroadcast(w http.ResponseWriter, r *http.Request) {
	if !s.checkTreasuryRequest(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	var input struct {
		Raw      string `json:"raw"`
		Password string `json:"confirm_password"`
	}
	if !financialIntentIDValid(id) {
		writeError(w, http.StatusBadRequest, "invalid treasury order")
		return
	}
	if !readTreasuryJSON(w, r, &input) || !s.requireSensitiveReauth(w, r, treasuryPaymentScopePrefix+id, input.Password,
		"treasury_broadcast_reauth_required", "confirm your LightningOS password to transmit the signed transfer") {
		return
	}
	if !treasuryBridgeAvailable() {
		writeError(w, http.StatusServiceUnavailable, "isolated Breez bridge unavailable")
		return
	}
	session, _ := authSessionFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	hash, err := s.submitTreasuryBase(ctx, id, session.ID, input.Raw)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAuditEventAsync(r, "treasury.base_dispatch", id, nil)
	writeJSON(w, http.StatusAccepted, map[string]string{"tx_hash": hash})
}
