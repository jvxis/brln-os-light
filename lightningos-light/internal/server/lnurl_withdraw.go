package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"lightningos-light/internal/lndclient"
)

const lnurlWithdrawTTL = 10 * time.Minute
const lnurlWithdrawMaxMsat int64 = 2_100_000_000_000_000_000

// Bearer LNURLs, k1 and callback URLs stay in bounded, short-lived memory only.
// The browser receives an opaque session-bound ticket instead of provider secrets.
type lnurlWithdrawal struct {
	mu        sync.Mutex
	owner     string
	expires   time.Time
	callback  *url.URL
	k1        string
	minSat    int64
	maxSat    int64
	amountSat int64
	invoice   lndclient.CreatedInvoice
	status    string
}

type lnurlWithdrawDescriptor struct {
	Tag         string `json:"tag"`
	Callback    string `json:"callback"`
	K1          string `json:"k1"`
	Min         int64  `json:"minWithdrawable"`
	Max         int64  `json:"maxWithdrawable"`
	Description string `json:"defaultDescription"`
	Status      string `json:"status"`
}

func decodeWithdrawLNURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 7000 {
		return nil, errors.New("invalid LNURL")
	}
	if strings.HasPrefix(strings.ToLower(raw), "lightning:") {
		raw = raw[len("lightning:"):]
	}
	hrp, data, err := bech32.DecodeNoLimit(raw)
	if err != nil || hrp != "lnurl" {
		return nil, errors.New("invalid LNURL")
	}
	canonical, err := bech32.Encode(hrp, data)
	if err != nil || canonical != strings.ToLower(raw) {
		return nil, errors.New("invalid LNURL")
	}
	decoded, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return nil, errors.New("invalid LNURL")
	}
	return validateLNURLCallback(string(decoded))
}

// Do not return URL-bearing transport errors or provider-supplied reasons: both
// can contain bearer credentials. All errors returned to callers are fixed text.
func withdrawJSON(ctx context.Context, endpoint *url.URL, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errors.New("lnurl_withdraw_unavailable")
	}
	client := safeLNURLClient()
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("lnurl_withdraw_unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("lnurl_withdraw_unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32769))
	if err != nil || len(body) > 32768 || json.Unmarshal(body, target) != nil {
		return errors.New("lnurl_withdraw_invalid_response")
	}
	return nil
}

func validateWithdrawDescriptor(d lnurlWithdrawDescriptor) (*url.URL, int64, int64, error) {
	if d.Status == "ERROR" {
		return nil, 0, 0, errors.New("lnurl_withdraw_provider_error")
	}
	if d.Tag != "withdrawRequest" {
		return nil, 0, 0, errors.New("lnurl_withdraw_wrong_type")
	}
	if d.Min < 0 || d.Max < d.Min || d.Max > lnurlWithdrawMaxMsat {
		return nil, 0, 0, errors.New("lnurl_withdraw_invalid_response")
	}
	// This wallet uses whole satoshis. Round inward, never above the provider's limit.
	minSat, maxSat := (d.Min+999)/1000, d.Max/1000
	if minSat < 1 {
		minSat = 1
	}
	if maxSat < minSat {
		return nil, 0, 0, errors.New("lnurl_withdraw_no_balance")
	}
	callback, err := validateLNURLCallback(d.Callback)
	if err != nil || len(d.K1) == 0 || len(d.K1) > 1024 || len(d.Description) > 640 {
		return nil, 0, 0, errors.New("lnurl_withdraw_invalid_response")
	}
	return callback, minSat, maxSat, nil
}

func withdrawOwner(r *http.Request) string {
	session, _ := authSessionFromContext(r.Context())
	return session.ID
}

func (s *Server) handleLNURLWithdrawPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		LNURL string `json:"lnurl"`
	}
	if readJSON(r, &req) != nil {
		writeError(w, 400, "lnurl_withdraw_invalid_input")
		return
	}
	endpoint, err := decodeWithdrawLNURL(req.LNURL)
	if err != nil {
		writeError(w, 400, "lnurl_withdraw_invalid_input")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lnurlRequestTimeout)
	defer cancel()
	var d lnurlWithdrawDescriptor
	if err := withdrawJSON(ctx, endpoint, &d); err != nil {
		writeError(w, 502, err.Error())
		return
	}
	callback, minSat, maxSat, err := validateWithdrawDescriptor(d)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeError(w, 500, "lnurl_withdraw_unavailable")
		return
	}
	token := hex.EncodeToString(tokenBytes)
	now := time.Now()
	s.lnurlWithdrawMu.Lock()
	if s.lnurlWithdrawals == nil {
		s.lnurlWithdrawals = make(map[string]*lnurlWithdrawal)
	}
	for key, entry := range s.lnurlWithdrawals {
		if now.After(entry.expires) {
			delete(s.lnurlWithdrawals, key)
		}
	}
	if len(s.lnurlWithdrawals) >= 64 {
		s.lnurlWithdrawMu.Unlock()
		writeError(w, 429, "lnurl_withdraw_busy")
		return
	}
	s.lnurlWithdrawals[token] = &lnurlWithdrawal{owner: withdrawOwner(r), expires: now.Add(lnurlWithdrawTTL), callback: callback, k1: d.K1, minSat: minSat, maxSat: maxSat}
	s.lnurlWithdrawMu.Unlock()
	writeJSON(w, 200, map[string]any{"token": token, "service": endpoint.Host, "callback_service": callback.Host, "description": d.Description, "min_sat": minSat, "max_sat": maxSat, "expires_at": now.Add(lnurlWithdrawTTL).UTC().Format(time.RFC3339)})
}

func (s *Server) handleLNURLWithdraw(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Token     string `json:"token"`
		AmountSat int64  `json:"amount_sat"`
	}
	if readJSON(r, &req) != nil {
		writeError(w, 400, "lnurl_withdraw_invalid_input")
		return
	}
	s.lnurlWithdrawMu.Lock()
	entry := s.lnurlWithdrawals[req.Token]
	valid := entry != nil && entry.owner == withdrawOwner(r) && time.Now().Before(entry.expires)
	s.lnurlWithdrawMu.Unlock()
	if !valid {
		writeError(w, 400, "lnurl_withdraw_expired")
		return
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if time.Now().After(entry.expires) {
		writeError(w, 400, "lnurl_withdraw_expired")
		return
	}
	if req.AmountSat < entry.minSat || req.AmountSat > entry.maxSat || entry.amountSat != 0 && entry.amountSat != req.AmountSat {
		writeError(w, 400, "lnurl_withdraw_amount_invalid")
		return
	}
	// Finish bounded work even if the browser disconnects. Concurrent retries
	// serialize on this ticket and reuse the exact same invoice and amount.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	created, contacted, err := entry.redeem(ctx, s.lnd, req.AmountSat)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	if created {
		s.recordWalletActivity(entry.invoice.PaymentHash)
	}
	if contacted {
		s.recordAuditEventAsync(r, "wallet.lnurl_withdraw."+entry.status, entry.callback.Hostname(), map[string]any{"amount_sat": entry.amountSat, "payment_hash": entry.invoice.PaymentHash})
	}
	// Provider acknowledgement is not proof of settlement. The existing wallet
	// activity/summary reads LND to show when the incoming invoice actually settles.
	writeJSON(w, 200, map[string]any{"status": entry.status, "payment_hash": entry.invoice.PaymentHash, "amount_sat": entry.amountSat})
}

// Caller holds entry.mu. This operation only creates a receiving invoice and
// submits it to the provider; it never invokes a payment/send RPC.
type withdrawInvoiceCreator interface {
	CreateInvoice(context.Context, int64, string, int64, *lndclient.CreateInvoiceOptions) (lndclient.CreatedInvoice, error)
}

func (entry *lnurlWithdrawal) redeem(ctx context.Context, creator withdrawInvoiceCreator, amountSat int64) (created, contacted bool, err error) {
	if entry.invoice.PaymentRequest == "" {
		invoice, err := creator.CreateInvoice(ctx, amountSat, "LNURL withdrawal", 600, nil)
		if err != nil {
			return false, false, errors.New("lnurl_withdraw_invoice_failed")
		}
		entry.invoice = invoice
		entry.amountSat = amountSat
		created = true
	}
	if entry.status != "accepted" {
		contacted = true
		callback := *entry.callback
		q := callback.Query()
		q.Set("k1", entry.k1)
		q.Set("pr", entry.invoice.PaymentRequest)
		callback.RawQuery = q.Encode()
		var result struct {
			Status string `json:"status"`
		}
		entry.status = "unknown"
		if err := withdrawJSON(ctx, &callback, &result); err == nil {
			if result.Status == "OK" {
				entry.status = "accepted"
			} else if result.Status == "ERROR" {
				entry.status = "rejected"
			}
		}
	}
	return created, contacted, nil
}
