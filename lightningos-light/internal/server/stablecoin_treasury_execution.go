package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) treasuryClaim(ctx context.Context, order treasuryOrder, session, reference string) (treasuryOrder, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return treasuryOrder{}, false, err
	}
	defer tx.Rollback(ctx)
	var enabled bool
	err = tx.QueryRow(ctx, "SELECT installed AND enabled AND wallet=$1 FROM stablecoin_treasury_settings WHERE id=1 FOR UPDATE", order.Wallet).Scan(&enabled)
	if err != nil || !enabled {
		return treasuryOrder{}, false, errors.New("Treasury configuration changed")
	}
	current, err := scanTreasuryOrder(tx.QueryRow(ctx, "SELECT payload,state,reference FROM stablecoin_treasury_orders WHERE id=$1 AND session_hash=$2 FOR UPDATE", order.Quote.ID, session))
	if err != nil {
		return treasuryOrder{}, false, err
	}
	if current.State != "quoted" {
		return current, false, nil
	}
	if !current.Quote.ExpiresAt.After(time.Now()) || current.Wallet != order.Wallet ||
		string(current.SignedOrder) != string(order.SignedOrder) {
		return treasuryOrder{}, false, errors.New("Treasury quote changed or expired")
	}
	result, err := tx.Exec(ctx, "UPDATE stablecoin_treasury_orders SET state='deposit_pending',reference=$2 WHERE id=$1 AND expires_at>now()", order.Quote.ID, reference)
	if err != nil || result.RowsAffected() != 1 {
		return treasuryOrder{}, false, errors.New("Treasury has an active or expired conversion")
	}
	if err := tx.Commit(ctx); err != nil {
		return treasuryOrder{}, false, err
	}
	current.State, current.Reference = "deposit_pending", reference
	return current, true, nil
}

func (s *Server) handleTreasuryPrepare(w http.ResponseWriter, r *http.Request) {
	session, ok := treasurySession(w, r, s)
	if !ok {
		return
	}
	var req struct {
		QuoteID string `json:"quote_id"`
	}
	if readJSON(r, &req) != nil || !treasuryValidID(req.QuoteID) {
		writeError(w, http.StatusBadRequest, "Invalid Treasury quote")
		return
	}
	ctx := r.Context()
	if _, err := s.treasuryEffectiveSettings(ctx); err != nil {
		writeError(w, http.StatusConflict, "Treasury is unavailable")
		return
	}
	order, err := s.treasuryOrder(ctx, req.QuoteID, session)
	if err != nil || order.State != "quoted" || order.Quote.Direction != treasuryToLightning || !order.Quote.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusConflict, "Treasury quote expired")
		return
	}
	prepared, fee, account, code := treasuryPrepareSolana(ctx, order)
	if code != "" || order.Quote.SolanaFee == nil || order.Quote.SolanaAccount == nil || *order.Quote.SolanaFee != fee || *order.Quote.SolanaAccount != account {
		if code == "" {
			code = "treasury_cost_changed"
		}
		writeErrorCode(w, http.StatusConflict, code, "Review a new Treasury quote before signing")
		return
	}
	order.SignedOrder = prepared
	body, err := json.Marshal(order)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Treasury quote unavailable")
		return
	}
	result, err := s.db.Exec(ctx, "UPDATE stablecoin_treasury_orders SET payload=$3 WHERE id=$1 AND session_hash=$2 AND state='quoted' AND expires_at>now()", req.QuoteID, session, body)
	if err != nil || result.RowsAffected() != 1 {
		writeError(w, http.StatusConflict, "Treasury quote expired")
		return
	}
	writeJSON(w, http.StatusOK, prepared)
}

func (s *Server) handleTreasuryExecute(w http.ResponseWriter, r *http.Request) {
	session, ok := treasurySession(w, r, s)
	if !ok {
		return
	}
	var req struct {
		QuoteID  string          `json:"quote_id"`
		Password string          `json:"confirm_password"`
		Signed   json.RawMessage `json:"signed"`
	}
	if readJSON(r, &req) != nil || !treasuryValidID(req.QuoteID) || len(req.Signed) > 4096 {
		writeError(w, http.StatusBadRequest, "Invalid Treasury execution")
		return
	}
	ctx := r.Context()
	if err := s.ensureTreasurySchema(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Treasury is unavailable")
		return
	}
	order, err := s.treasuryOrder(ctx, req.QuoteID, session)
	if err != nil {
		writeError(w, http.StatusNotFound, "Treasury quote unavailable")
		return
	}
	if order.State != "quoted" {
		if order.State == "superseded" {
			writeError(w, http.StatusConflict, "Treasury quote expired")
			return
		}
		writeJSON(w, http.StatusAccepted, order.view())
		return
	}
	if _, err = s.treasuryEffectiveSettings(ctx); err != nil || !order.Quote.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusConflict, "Treasury is stopped or the quote expired")
		return
	}
	reference := order.PaymentHash
	var reservation SpendingReservation
	var reserved bool
	if order.Quote.Direction == treasuryToSolana {
		if len(req.Signed) != 0 {
			writeError(w, http.StatusBadRequest, "Unexpected Solana signature")
			return
		}
		if !treasuryFreshAuth(w, r, s, req.Password) {
			return
		}
		if order.Quote.RoutingFee == nil || reference == "" || order.Invoice == "" {
			writeError(w, http.StatusConflict, "Invalid Lightning quote")
			return
		}
	} else {
		if req.Password != "" || len(req.Signed) == 0 || len(order.SignedOrder) == 0 {
			writeError(w, http.StatusBadRequest, "Missing or invalid Solana signature")
			return
		}
		verified, err := treasuryCall(ctx, treasuryWorkerRequest{Action: "solana-validate", Order: order.SignedOrder, Signed: req.Signed})
		if err != nil || verified.Status != "verified" || len(verified.DepositRef) < 64 || len(verified.DepositRef) > 88 {
			writeError(w, http.StatusConflict, "Solana transaction could not be verified")
			return
		}
		reference = verified.DepositRef
	}
	order, claimed, err := s.treasuryClaim(ctx, order, session, reference)
	if err != nil {
		writeError(w, http.StatusConflict, "A conversion is active or the quote expired")
		return
	}
	if claimed && order.Quote.Direction == treasuryToSolana {
		var hash string
		reservation, hash, err = s.reserveInvoiceSpending(ctx, order.Invoice, order.Quote.Debit.Atoms, *order.Quote.RoutingFee, "treasury")
		if err != nil || hash != order.PaymentHash {
			if err == nil {
				s.finishSpendingReservation(reservation, hash, errors.New("Treasury payment hash mismatch"))
			}
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			s.treasuryUpdate(recovery, order, "deposit_failed")
			cancel()
			if err != nil && writeSpendingGuardError(w, err) {
				return
			}
			writeError(w, http.StatusServiceUnavailable, "Spending guard could not validate the conversion")
			return
		}
		reserved = true
	}
	if claimed {
		s.recordAuditEventAsync(r, "treasury.deposit.authorized", order.Quote.ID, map[string]any{"direction": order.Quote.Direction, "amount": order.Quote.Debit.Atoms})
		go s.treasurySubmit(order, req.Signed, reservation, reserved)
	}
	writeJSON(w, http.StatusAccepted, order.view())
}

func (s *Server) treasurySubmit(order treasuryOrder, signed json.RawMessage, reservation SpendingReservation, reserved bool) {
	expiry := order.Quote.ExpiresAt
	if !expiry.After(time.Now().Add(2 * time.Second)) {
		if reserved {
			s.finishSpendingReservation(reservation, order.PaymentHash, errFinancialNativeNotDispatched)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.treasuryUpdate(ctx, order, "deposit_failed")
		return
	}
	deadline := time.Now().Add(90 * time.Second)
	if expiry.Before(deadline) {
		deadline = expiry
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if order.Quote.Direction == treasuryToLightning {
		_, _ = treasuryCall(ctx, treasuryWorkerRequest{Action: "solana-broadcast", Order: order.SignedOrder, Signed: signed, Reference: order.Reference})
		return
	}
	if !reserved || order.Quote.RoutingFee == nil {
		return
	}
	err := s.lnd.PayInvoice(ctx, order.Invoice, nil, *order.Quote.RoutingFee)
	s.finishSpendingReservation(reservation, order.PaymentHash, err)
	s.recordWalletActivity(order.PaymentHash)
}

func (s *Server) treasuryUpdate(ctx context.Context, order treasuryOrder, state string) {
	payload, err := json.Marshal(order)
	if err != nil {
		return
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	if state == "delivered" && order.Quote.Direction == treasuryToLightning {
		if _, err = tx.Exec(ctx, "SELECT id FROM stablecoin_treasury_settings WHERE id=1 FOR UPDATE"); err != nil {
			return
		}
	}
	res, err := tx.Exec(ctx, "UPDATE stablecoin_treasury_orders SET payload=$2,state=$3 WHERE id=$1 AND state=$4 AND reference=$5", order.Quote.ID, payload, state, order.State, order.Reference)
	if err != nil || res.RowsAffected() != 1 {
		return
	}
	if state == "delivered" && order.Quote.Direction == treasuryToLightning {
		if _, err = tx.Exec(ctx, "UPDATE stablecoin_treasury_settings SET saved_recipient=$1 WHERE id=1 AND wallet=$2", order.Quote.Destination, order.Wallet); err != nil {
			return
		}
	}
	_ = tx.Commit(ctx)
}

func (s *Server) treasuryMonitor(ctx context.Context) {
	timer := time.NewTicker(15 * time.Second)
	defer timer.Stop()
	for {
		if treasuryRuntimeReady() && s.db != nil {
			s.treasuryRefresh(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (s *Server) treasuryRefresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 43*time.Second)
	defer cancel()
	settings, err := s.treasurySettings(ctx)
	if err != nil || settings.Key == "" {
		return
	}
	order, err := scanTreasuryOrder(s.db.QueryRow(ctx, "SELECT payload,state,reference FROM stablecoin_treasury_orders WHERE state IN ("+treasuryPendingSQL+") LIMIT 1"))
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return
	}
	if order.State == "deposit_pending" {
		s.treasuryRefreshDeposit(ctx, order)
		return
	}
	res, err := treasuryCall(ctx, treasuryWorkerRequest{Action: "status", APIKey: settings.Key, BridgeID: order.BridgeID, StatusToken: order.StatusToken})
	if err != nil || res.AmountIn != order.Quote.Debit.Atoms ||
		(order.Quote.Direction == treasuryToLightning && res.DepositRef != order.Reference) {
		return
	}
	switch res.Status {
	case "completed":
		if res.Arrived(order) && s.treasuryVerifyDelivery(ctx, order, res) {
			order.Delivered = &treasuryAmount{Atoms: res.AmountOut, Asset: order.Quote.Receive.Asset}
			s.treasuryUpdate(ctx, order, "delivered")
		}
	case "failed", "expired":
		s.treasuryUpdate(ctx, order, "delivery_issue")
	case "refunding":
		s.treasuryUpdate(ctx, order, "refund_pending")
	case "refunded":
		s.treasuryRefreshRefund(ctx, order, res)
	}
}

func (res treasuryWorkerResult) Arrived(order treasuryOrder) bool {
	if res.DeliveryRef == "" || res.AmountOut <= 0 {
		return false
	}
	// A provider receipt must remain inside the displayed one-percent slippage.
	expected := order.Quote.Receive.Atoms
	return res.AmountOut >= expected-expected/100 && res.AmountOut <= expected+expected/100
}

func (s *Server) treasuryVerifyDelivery(ctx context.Context, order treasuryOrder, res treasuryWorkerResult) bool {
	if order.Quote.Direction == treasuryToLightning {
		// Orchestra can supply a delivery ID rather than a BOLT11 payment hash.
		return true
	}
	proof, err := treasuryCall(ctx, treasuryWorkerRequest{Action: "solana-proof", Cluster: "mainnet", Recipient: order.Wallet, Reference: res.DeliveryRef, Amount: res.AmountOut})
	return err == nil && proof.Status == "verified" && proof.AmountOut == res.AmountOut && proof.DeliveryRef == res.DeliveryRef
}

func (s *Server) treasuryRefreshDeposit(ctx context.Context, order treasuryOrder) {
	if order.Quote.Direction == treasuryToSolana {
		pay, found, err := s.lnd.TrackPaymentDetails(ctx, order.Reference)
		if err != nil {
			return
		}
		if !found {
			if time.Now().After(order.Quote.ExpiresAt.Add(2 * time.Minute)) {
				s.treasuryUpdate(ctx, order, "deposit_failed")
			}
			return
		}
		switch pay.Status {
		case "SUCCEEDED":
			if pay.ValueMsat != 1000*order.Quote.Debit.Atoms {
				return
			}
			order.ActualFee = &treasuryAmount{Atoms: pay.FeeMsat, Asset: "msats"}
			s.treasuryUpdate(ctx, order, "delivery_pending")
		case "FAILED":
			s.treasuryUpdate(ctx, order, "deposit_failed")
		}
		return
	}
	final, err := treasuryCall(ctx, treasuryWorkerRequest{Action: "solana-finality", Reference: order.Reference, Order: order.SignedOrder})
	if err != nil {
		return
	}
	if final.Status == "failed" || final.Status == "expired" {
		s.treasuryUpdate(ctx, order, "deposit_failed")
		return
	}
	if final.Status != "finalized" {
		return
	}
	proof, err := treasuryCall(ctx, treasuryWorkerRequest{Action: "solana-proof", Cluster: "mainnet", Recipient: order.Deposit, Reference: order.Reference, Amount: order.Quote.Debit.Atoms})
	if err == nil && proof.Status == "verified" && proof.AmountOut == order.Quote.Debit.Atoms && proof.DeliveryRef == order.Reference {
		if final.SOL != nil && *final.SOL >= 0 && *final.SOL <= treasuryMaxSOL {
			order.ActualFee = &treasuryAmount{Atoms: *final.SOL, Asset: "SOL"}
		}
		s.treasuryUpdate(ctx, order, "delivery_pending")
	}
}

func (s *Server) treasuryRefreshRefund(ctx context.Context, order treasuryOrder, res treasuryWorkerResult) {
	if order.Quote.Direction == treasuryToLightning {
		proof, err := treasuryCall(ctx, treasuryWorkerRequest{Action: "solana-refund-proof", Cluster: "mainnet", Recipient: order.Wallet, Reference: res.RefundRef, Amount: order.Quote.Debit.Atoms})
		if err == nil && proof.Status == "verified" && proof.AmountOut > 0 && proof.AmountOut <= order.Quote.Debit.Atoms && proof.DeliveryRef == res.RefundRef {
			order.Refund = &treasuryAmount{Atoms: proof.AmountOut, Asset: "USDC"}
		}
	} else {
		paid, err := s.lnd.SettledInvoiceAmountSat(ctx, order.RefundHash)
		if err == nil && paid > 0 && paid <= order.Quote.Debit.Atoms {
			order.Refund = &treasuryAmount{Atoms: paid, Asset: "sats"}
		}
	}
	if order.Refund != nil {
		s.treasuryUpdate(ctx, order, "refunded")
		return
	}
	if order.State != "refund_pending" {
		s.treasuryUpdate(ctx, order, "refund_pending")
	}
}

func (s *Server) handleTreasuryStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := treasurySession(w, r, s); !ok {
		return
	}
	ctx := r.Context()
	settings, err := s.treasurySettings(ctx)
	if err != nil || !settings.Installed {
		writeError(w, http.StatusNotFound, "Treasury is not installed")
		return
	}
	rows, err := s.db.Query(ctx, "SELECT payload,state,reference FROM stablecoin_treasury_orders WHERE state NOT IN ('quoted','superseded') ORDER BY created_at DESC LIMIT 50")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Treasury history unavailable")
		return
	}
	orders := make([]treasuryOrderView, 0)
	for rows.Next() {
		o, err := scanTreasuryOrder(rows)
		if err != nil {
			rows.Close()
			writeError(w, http.StatusServiceUnavailable, "Treasury history unavailable")
			return
		}
		orders = append(orders, o.view())
	}
	rows.Close()
	if rows.Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "Treasury history unavailable")
		return
	}
	status := map[string]any{"enabled": settings.Enabled && treasuryRuntimeReady(), "breez_configured": settings.Key != "",
		"solana_address": settings.Wallet, "saved_recipient": settings.SavedRecipient, "orders": orders}
	if runtimeReady, ok := status["enabled"].(bool); ok && runtimeReady && settings.Wallet != "" {
		u, cancel := context.WithTimeout(ctx, 4*time.Second)
		if balances, err := s.lnd.GetBalances(u); err == nil {
			status["lightning_sats"] = balances.LightningSat
		}
		cancel()
		v, cancel := context.WithTimeout(ctx, 4*time.Second)
		if balances, err := treasuryCall(v, treasuryWorkerRequest{Action: "solana-balance", Recipient: settings.Wallet}); err == nil {
			if balances.SOL != nil {
				status["sol_lamports"] = *balances.SOL
			}
			if balances.USDC != nil {
				status["usdc_micros"] = *balances.USDC
			}
		}
		cancel()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleTreasurySigner(w http.ResponseWriter, r *http.Request) {
	if _, ok := treasurySession(w, r, s); !ok {
		return
	}
	if _, err := s.treasuryEffectiveSettings(r.Context()); err != nil {
		http.NotFound(w, r)
		return
	}
	page, pageErr := treasuryAsset("signer.html", 16<<10)
	js, jsErr := treasuryAsset("signer.js", 256<<10)
	css, cssErr := treasuryAsset("signer.css", 16<<10)
	if pageErr != nil || jsErr != nil || cssErr != nil ||
		strings.Contains(strings.ToLower(string(js)), "</script") ||
		strings.Contains(strings.ToLower(string(css)), "</style") {
		http.NotFound(w, r)
		return
	}
	const signerPolicy = "default-src 'none'; script-src 'sha256-%s'; style-src 'sha256-%s'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'; sandbox allow-scripts allow-downloads"
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(signerPolicy, treasuryCSPHash(js), treasuryCSPHash(css)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := strings.NewReplacer("__SIGNER_JS__", string(js), "__SIGNER_CSS__", string(css)).Replace(string(page))
	_, _ = w.Write([]byte(html))
}

func treasuryCSPHash(data []byte) string {
	digest := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(digest[:])
}

func treasuryAsset(name string, limit int64) ([]byte, error) {
	file, err := os.Open(treasuryBundle + "/" + name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("Treasury asset unavailable")
	}
	return io.ReadAll(io.LimitReader(file, limit+1))
}
