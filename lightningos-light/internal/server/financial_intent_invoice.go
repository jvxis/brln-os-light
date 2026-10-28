package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"lightningos-light/internal/lndclient"
	"math"
	"slices"
	"strings"
	"time"
)

type financialInvoiceRequest struct {
	PaymentRequest string   `json:"payment_request"`
	ChannelPoint   string   `json:"channel_point,omitempty"`
	ChannelPoints  []string `json:"channel_points,omitempty"`
	AmountSat      int64    `json:"amount_sat,omitempty"`
	Comment        string   `json:"comment,omitempty"`
	MaxFeeSat      int64    `json:"max_fee_sat"`
	RouteToken     string   `json:"route_token,omitempty"`
	MaxParts       uint32   `json:"max_parts,omitempty"`
	MaxShardSat    int64    `json:"max_shard_sat,omitempty"`
}

func (s *Server) prepareInvoiceFinancialIntent(ctx context.Context, path string, payload []byte) ([]byte, financialIntentBinding, error) {
	var request financialInvoiceRequest
	if err := decodeFinancialRequest(payload, &request); err != nil {
		return nil, financialIntentBinding{}, err
	}
	if s.lnd == nil || request.MaxFeeSat < 0 || request.MaxFeeSat > 21_000_000*100_000_000 || request.AmountSat < 0 || request.MaxShardSat < 0 || request.MaxShardSat > 21_000_000*100_000_000 || request.RouteToken != "" && path != "/api/wallet/pay/validated-route" || (request.MaxParts != 0 || request.MaxShardSat != 0) && path != "/api/wallet/pay/mpp" {
		return nil, financialIntentBinding{}, errors.New("invalid Lightning payment amount, mode or fee limit")
	}
	invoice, channels, err := s.resolveWalletPaymentInput(ctx, request.PaymentRequest, request.AmountSat, request.Comment, request.ChannelPoint, request.ChannelPoints)
	if err != nil {
		return nil, financialIntentBinding{}, errors.New("Lightning payment destination or selected channels unavailable")
	}
	decoded, err := s.lnd.DecodeInvoice(ctx, invoice)
	if err != nil {
		return nil, financialIntentBinding{}, errors.New("Lightning invoice could not be decoded")
	}
	if decoded.AmountMsat <= 0 && decoded.AmountSat > 0 && decoded.AmountSat <= 21_000_000*100_000_000 {
		decoded.AmountMsat = decoded.AmountSat * 1000
	}
	if decoded.AmountMsat <= 0 || decoded.AmountMsat > 21_000_000*100_000_000*1000 || decoded.Timestamp <= 0 || decoded.Expiry <= 0 || decoded.Expiry > math.MaxInt64-decoded.Timestamp || !time.Now().Before(time.Unix(decoded.Timestamp+decoded.Expiry, 0)) {
		return nil, financialIntentBinding{}, errors.New("invoice must specify a positive amount and must not be expired")
	}
	hash, err := hex.DecodeString(decoded.PaymentHash)
	if err != nil || len(hash) != 32 {
		return nil, financialIntentBinding{}, errors.New("invoice payment hash unavailable")
	}
	amount := (decoded.AmountMsat + 999) / 1000
	if request.AmountSat > 0 && request.AmountSat != amount {
		return nil, financialIntentBinding{}, errors.New("requested amount does not match the resolved invoice")
	}
	request.PaymentRequest, request.AmountSat = invoice, amount
	request.Comment = "" // The LNURL invoice is resolved once, before approval.
	feeMsat := lndclient.FinancialIntentPaymentFeeLimitMsat(decoded, request.MaxFeeSat)
	fee := feeMsat / 1000
	if feeMsat%1000 != 0 {
		fee++
	}
	binding := financialIntentBinding{Kind: "invoice_payment", OutgoingChanIDs: slices.Clone(channels), Summary: financialIntentSummary{Operation: strings.TrimPrefix(path, "/api/"), Destination: decoded.Destination, AmountSat: amount, AmountMsat: decoded.AmountMsat, MaxFeeSat: &fee, MaxFeeMsat: &feeMsat, PaymentHash: strings.ToLower(decoded.PaymentHash)}}
	normalized, err := json.Marshal(request)
	return normalized, binding, err
}

func validateFinancialInvoiceExecution(ctx context.Context, invoice string, channels []uint64, fee int64) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return nil
	}
	var binding financialIntentBinding
	var request financialInvoiceRequest
	if json.Unmarshal(execution.record.Binding, &binding) != nil || binding.Kind != "invoice_payment" || json.Unmarshal(execution.record.Request, &request) != nil || invoice != request.PaymentRequest || fee != request.MaxFeeSat || !slices.Equal(channels, binding.OutgoingChanIDs) {
		return errors.New("Lightning payment no longer matches the approved operation; review it again")
	}
	return nil
}

var errFinancialNativeNotDispatched = errors.New("financial dispatch could not be committed; native payment was not submitted")

func executeFinancialInvoicePayment(ctx context.Context, paymentHash, invoice string, channels []uint64, fee int64, send func() error) error {
	execution, bound := ctx.Value(financialIntentContextKey{}).(*financialIntentExecution)
	if !bound {
		return send()
	}
	if validateFinancialInvoiceExecution(ctx, invoice, channels, fee) != nil {
		return errFinancialNativeNotDispatched
	}
	var binding financialIntentBinding
	if json.Unmarshal(execution.record.Binding, &binding) != nil || binding.Kind != "invoice_payment" || binding.Summary.PaymentHash != strings.ToLower(paymentHash) {
		return errFinancialNativeNotDispatched
	}
	proof, _ := json.Marshal(financialNativeProof{Kind: binding.Kind, DispatchStarted: true, StartedAt: time.Now()})
	if execution.store.saveProof(execution.record.ID, proof) != nil {
		return errFinancialNativeNotDispatched
	}
	return send()
}
