package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"lightningos-light/internal/lndclient"
	"net/http"
	"strings"
	"time"
)

const lnurlInvoiceScopePrefix = "lnurl_invoice:"

type cachedLNURLInvoice struct {
	invoice string
	expires time.Time
}

func walletLightningAddress(raw string) string {
	value := strings.TrimSpace(normalizePaymentRequest(raw))
	if strings.HasPrefix(strings.ToLower(value), "lightning:") {
		value = value[len("lightning:"):]
	}
	if isLightningAddress(value) {
		return value
	}
	return ""
}

// Resolution is shared by preview and execution for this session and amount.
// No callback is repeated behind an approval; expiry or changed parameters
// produce a different invoice scope and require a new confirmation.
func (s *Server) resolveCachedLNURLInvoice(ctx context.Context, address string, amount int64, comment string) (string, error) {
	session, _ := authSessionFromContext(ctx)
	encoded, _ := json.Marshal([]any{session.ID, address, amount, comment})
	digest := sha256.Sum256(encoded)
	key := hex.EncodeToString(digest[:])
	result, err, _ := s.lnurlInvoiceResolutions.Do(key, func() (any, error) {
		now := time.Now()
		s.lnurlInvoiceMu.Lock()
		for key, item := range s.lnurlInvoices {
			if !now.Before(item.expires) {
				delete(s.lnurlInvoices, key)
			}
		}
		if item, ok := s.lnurlInvoices[key]; ok {
			s.lnurlInvoiceMu.Unlock()
			return item.invoice, nil
		}
		if len(s.lnurlInvoices) >= 256 {
			s.lnurlInvoiceMu.Unlock()
			return nil, errors.New("too many pending Lightning Address invoices; retry later")
		}
		s.lnurlInvoiceMu.Unlock()
		var expiry time.Time
		invoice, err := resolveLightningAddress(ctx, address, amount, comment, func(ctx context.Context, invoice string) (lndclient.DecodedInvoice, error) {
			decoded, err := s.lnd.DecodeInvoice(ctx, invoice)
			if err == nil {
				expiry = time.Unix(decoded.Timestamp+decoded.Expiry, 0)
			}
			return decoded, err
		})
		if err != nil {
			return nil, err
		}
		if expiry.After(now.Add(5 * time.Minute)) {
			expiry = now.Add(5 * time.Minute)
		}
		s.lnurlInvoiceMu.Lock()
		defer s.lnurlInvoiceMu.Unlock()
		if len(s.lnurlInvoices) >= 256 {
			return nil, errors.New("too many pending Lightning Address invoices; retry later")
		}
		if s.lnurlInvoices == nil {
			s.lnurlInvoices = make(map[string]cachedLNURLInvoice)
		}
		s.lnurlInvoices[key] = cachedLNURLInvoice{invoice: invoice, expires: expiry}
		return invoice, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func lnurlInvoiceScope(address, invoice string, amount, maxFee int64, channels []uint64) string {
	payload, _ := json.Marshal([]any{address, invoice, amount, maxFee, channels})
	hash := sha256.Sum256(payload)
	return lnurlInvoiceScopePrefix + hex.EncodeToString(hash[:])
}

func (s *Server) requireResolvedLNURLReauth(w http.ResponseWriter, r *http.Request, raw, invoice string, amount, maxFee int64, channels []uint64, password string) bool {
	address := walletLightningAddress(raw)
	if address == "" {
		return true
	}
	return s.requireSensitiveReauth(w, r, lnurlInvoiceScope(address, invoice, amount, maxFee, channels), password, "lightning_funds_reauth_required", "password confirmation required for this Lightning Address invoice")
}
