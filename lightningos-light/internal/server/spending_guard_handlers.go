package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

func (s *Server) handleSpendingGuardGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.spendingGuardService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status, err := svc.Status(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to load spending guard")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleSpendingGuardUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled            bool   `json:"enabled"`
		MaxPaymentSat      int64  `json:"max_payment_sat"`
		Rolling24hLimitSat int64  `json:"rolling_24h_limit_sat"`
		ConfirmPassword    string `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	next := SpendingGuardSettings{Enabled: req.Enabled, MaxPaymentSat: req.MaxPaymentSat, Rolling24hLimitSat: req.Rolling24hLimitSat}
	if err := validateSpendingGuardSettings(next); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	svc, errMsg := s.spendingGuardService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	current, err := svc.Settings(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to load spending guard")
		return
	}
	if spendingGuardNeedsReauth(current, next) && !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}
	updated, err := svc.UpdateSettings(ctx, next)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update spending guard")
		return
	}
	s.recordAuditEventAsync(r, "wallet.spending_guard.updated", "lightning", map[string]any{
		"enabled": updated.Enabled, "max_payment_sat": updated.MaxPaymentSat, "rolling_24h_limit_sat": updated.Rolling24hLimitSat,
	})
	status, err := svc.Status(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, updated)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func suggestedSpendingGuardFeeSat(amountSat, requestedMaxFeeSat int64) int64 {
	if requestedMaxFeeSat > 0 {
		return requestedMaxFeeSat
	}
	if amountSat <= 0 {
		return 0
	}
	if amountSat <= 1_000 {
		return amountSat
	}
	fee := amountSat / 20
	if amountSat%20 != 0 {
		fee++
	}
	return fee
}

func (s *Server) reserveInvoiceSpending(ctx context.Context, paymentRequest string, requestedAmountSat, requestedMaxFeeSat int64, source string) (SpendingReservation, string, error) {
	decoded, err := s.lnd.DecodeInvoice(ctx, paymentRequest)
	if err != nil {
		return SpendingReservation{}, "", err
	}
	svc, errMsg := s.spendingGuardService()
	if svc == nil {
		if s.logger != nil {
			s.logger.Printf("spending guard unavailable; payment remains allowed because no active policy could be loaded: %s", errMsg)
		}
		return SpendingReservation{}, decoded.PaymentHash, nil
	}
	amountSat := decoded.AmountSat
	if amountSat <= 0 && decoded.AmountMsat > 0 {
		amountSat = (decoded.AmountMsat + 999) / 1000
	}
	if amountSat <= 0 {
		amountSat = requestedAmountSat
	}
	if amountSat <= 0 {
		return SpendingReservation{}, decoded.PaymentHash, errors.New("payment amount unavailable for spending guard")
	}
	maxFeeSat := suggestedSpendingGuardFeeSat(amountSat, requestedMaxFeeSat)
	reservation, err := svc.Reserve(ctx, SpendingIntent{
		Source: source, AmountSat: amountSat, MaxFeeSat: maxFeeSat, PaymentHash: decoded.PaymentHash,
	})
	return reservation, decoded.PaymentHash, err
}

func writeSpendingGuardError(w http.ResponseWriter, err error) bool {
	var limitErr *SpendingLimitError
	if !errors.As(err, &limitErr) {
		return false
	}
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error": limitErr.Error(), "code": "spending_guard_limit_exceeded", "details": limitErr,
	})
	return true
}

func (s *Server) finishSpendingReservation(reservation SpendingReservation, paymentHash string, paymentErr error) {
	if !reservation.Active {
		return
	}
	svc, _ := s.spendingGuardService()
	if svc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	details, found, _ := s.lnd.TrackPaymentDetails(ctx, paymentHash)
	if found && strings.EqualFold(details.Status, "SUCCEEDED") {
		feeSat := details.FeeSat
		if feeSat <= 0 && details.FeeMsat > 0 {
			feeSat = (details.FeeMsat + 999) / 1000
		}
		_ = svc.Settle(ctx, reservation, feeSat, paymentHash)
		return
	}
	if paymentErr == nil {
		// A successful send should already be indexed. If LND is briefly late,
		// retain the conservative maximum fee instead of under-counting.
		_ = svc.Settle(ctx, reservation, reservation.MaxFeeSat, paymentHash)
		return
	}
	if found && !strings.EqualFold(details.Status, "FAILED") {
		return
	}
	if isAmbiguousPaymentError(paymentErr) {
		return
	}
	_ = svc.Release(ctx, reservation, paymentErr.Error())
}

func isAmbiguousPaymentError(err error) bool {
	if err == nil {
		return false
	}
	if isTimeoutError(err) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "transport is closing") || strings.Contains(lower, "connection unavailable")
}

func paymentDetailsFeeSat(details lndclient.PaymentDetails, fallback int64) int64 {
	if details.FeeSat > 0 {
		return details.FeeSat
	}
	if details.FeeMsat > 0 {
		return (details.FeeMsat + 999) / 1000
	}
	return fallback
}
