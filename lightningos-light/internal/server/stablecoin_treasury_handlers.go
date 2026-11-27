package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/jackc/pgx/v5"
)

const (
	treasuryToSolana    = "to_solana"
	treasuryToLightning = "to_lightning"
	treasuryMaxSats     = 200_000
	treasuryMaxMicros   = 100_000_000
	treasuryMaxSOL      = 3_000_000
	treasuryPendingSQL  = "'deposit_pending','delivery_pending','delivery_issue','refund_pending'"
)

type treasuryAmount struct {
	Atoms int64  `json:"atoms"`
	Asset string `json:"asset"`
}

type treasuryMarket struct {
	USD        float64        `json:"btc_usd"`
	Reference  treasuryAmount `json:"reference_receive"`
	Difference int64          `json:"difference_bps"`
}

type treasuryQuote struct {
	ID            string          `json:"id"`
	Direction     string          `json:"direction"`
	Destination   string          `json:"destination"`
	Debit         treasuryAmount  `json:"debit"`
	Receive       treasuryAmount  `json:"receive"`
	BridgeFee     *treasuryAmount `json:"bridge_fee,omitempty"`
	RoutingFee    *int64          `json:"lightning_fee_limit_sats,omitempty"`
	SolanaFee     *int64          `json:"solana_fee_lamports,omitempty"`
	SolanaAccount *int64          `json:"solana_account_lamports,omitempty"`
	Market        *treasuryMarket `json:"market,omitempty"`
	ExpiresAt     time.Time       `json:"expires_at"`
}

type treasuryOrder struct {
	Quote                                                                    treasuryQuote
	BridgeID, StatusToken, Wallet, Deposit, Invoice, PaymentHash, RefundHash string
	SignedOrder                                                              json.RawMessage
	Refund                                                                   *treasuryAmount
	Delivered                                                                *treasuryAmount
	ActualFee                                                                *treasuryAmount
	State                                                                    string `json:"-"`
	Reference                                                                string `json:"-"`
}

type treasuryOrderView struct {
	ID          string          `json:"id"`
	Direction   string          `json:"direction"`
	Destination string          `json:"destination"`
	State       string          `json:"state"`
	Debit       treasuryAmount  `json:"debit"`
	Receive     *treasuryAmount `json:"receive,omitempty"`
	Refund      *treasuryAmount `json:"refund,omitempty"`
	ActualFee   *treasuryAmount `json:"actual_fee,omitempty"`
}

func (o treasuryOrder) view() treasuryOrderView {
	return treasuryOrderView{ID: o.Quote.ID, Direction: o.Quote.Direction, Destination: o.Quote.Destination,
		State: o.State, Debit: o.Quote.Debit, Receive: o.Delivered, Refund: o.Refund, ActualFee: o.ActualFee}
}

func treasurySession(w http.ResponseWriter, r *http.Request, s *Server) (string, bool) {
	session, ok := authSessionFromContext(r.Context())
	if s.auth == nil || !s.auth.Enabled() || !ok || session.ID == "" {
		writeError(w, http.StatusForbidden, "USDC Treasury requires LightningOS login protection")
		return "", false
	}
	digest := sha256.Sum256([]byte(session.ID))
	return hex.EncodeToString(digest[:]), true
}

func treasuryFreshAuth(w http.ResponseWriter, r *http.Request, s *Server, password string) bool {
	session, ok := authSessionFromContext(r.Context())
	if !ok || strings.TrimSpace(password) == "" {
		writeErrorCode(w, http.StatusPreconditionRequired, "treasury_password_required", "Confirm with your LightningOS password")
		return false
	}
	if _, err := s.auth.reauth(w, r, session.ID, password, treasuryAppID); err != nil {
		if !errors.Is(err, errReauthRateLimited) {
			writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "Invalid password")
		}
		return false
	}
	return true
}

func mainnetTreasuryInvoice(invoice string) bool {
	return len(invoice) > 4 && strings.HasPrefix(invoice, "lnbc") && invoice[4] >= '0' && invoice[4] <= '9'
}

func validTreasuryWallet(value string) bool {
	if len(value) < 32 || len(value) > 44 {
		return false
	}
	key := base58.Decode(value)
	return len(key) == 32 && base58.Encode(key) == value && value != strings.Repeat("1", 32)
}

func treasuryRecipient(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 265 || walletLightningAddress(raw) != raw {
		return ""
	}
	user, host, ok := strings.Cut(raw, "@")
	if !ok || user == "" || host == "" {
		return ""
	}
	return user + "@" + strings.ToLower(host)
}

func treasuryID() (string, error) {
	var id [16]byte
	_, err := rand.Read(id[:])
	return hex.EncodeToString(id[:]), err
}

func treasuryValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

func (s *Server) treasuryEffectiveSettings(ctx context.Context) (treasurySettings, error) {
	settings, err := s.treasurySettings(ctx)
	if err != nil || !settings.Installed || !settings.Enabled || !treasuryRuntimeReady() {
		return settings, errors.New("Treasury is unavailable")
	}
	return settings, nil
}

func (s *Server) handleTreasurySettings(w http.ResponseWriter, r *http.Request, walletUpdate bool) {
	if _, ok := treasurySession(w, r, s); !ok {
		return
	}
	var req struct {
		Key      string `json:"api_key"`
		Wallet   string `json:"wallet"`
		Password string `json:"confirm_password"`
	}
	if readJSON(r, &req) != nil {
		writeError(w, http.StatusBadRequest, "Invalid Treasury settings")
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if (walletUpdate && (!validTreasuryWallet(req.Wallet) || req.Key != "")) ||
		(!walletUpdate && (len(req.Key) == 0 || len(req.Key) > 4096 || strings.ContainsAny(req.Key, "\r\n\x00") || req.Wallet != "")) {
		writeError(w, http.StatusBadRequest, "Invalid Treasury settings")
		return
	}
	if !treasuryFreshAuth(w, r, s, req.Password) {
		return
	}
	ctx := r.Context()
	if _, err := s.treasuryEffectiveSettings(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Treasury is unavailable")
		return
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Treasury settings unavailable")
		return
	}
	defer tx.Rollback(ctx)
	var active bool
	err = tx.QueryRow(ctx, "SELECT installed AND enabled FROM stablecoin_treasury_settings WHERE id=1 FOR UPDATE").Scan(&active)
	if err != nil || !active {
		writeError(w, http.StatusConflict, "Treasury is stopped")
		return
	}
	var pending bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM stablecoin_treasury_orders WHERE state IN ("+treasuryPendingSQL+"))").Scan(&pending); err != nil || pending {
		writeError(w, http.StatusConflict, "A conversion is awaiting reconciliation")
		return
	}
	if walletUpdate {
		_, err = tx.Exec(ctx, "UPDATE stablecoin_treasury_settings SET wallet=$1,saved_recipient=CASE WHEN wallet=$1 THEN saved_recipient ELSE '' END WHERE id=1", req.Wallet)
	} else {
		var sealed string
		sealed, err = sealTreasuryCredential(req.Key)
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE stablecoin_treasury_settings SET api_key=$1 WHERE id=1", sealed)
		}
	}
	if err == nil {
		_, err = tx.Exec(ctx, "DELETE FROM stablecoin_treasury_orders WHERE state='quoted'")
	}
	if err != nil || tx.Commit(ctx) != nil {
		writeError(w, http.StatusServiceUnavailable, "Treasury settings unavailable")
		return
	}
	s.recordAuditEventAsync(r, "treasury.settings.changed", "", map[string]any{"wallet_updated": walletUpdate})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTreasuryKey(w http.ResponseWriter, r *http.Request) {
	s.handleTreasurySettings(w, r, false)
}
func (s *Server) handleTreasuryWallet(w http.ResponseWriter, r *http.Request) {
	s.handleTreasurySettings(w, r, true)
}

func (s *Server) treasuryMarket(ctx context.Context, quote treasuryQuote) *treasuryMarket {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	status := s.bitcoinMarketStatusCached(ctx)
	if status.Stale {
		return nil
	}
	for _, p := range status.Prices {
		if p.Currency != "USD" || p.Value <= 0 || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
			continue
		}
		reference := float64(quote.Debit.Atoms) * p.Value / 100
		if quote.Direction == treasuryToLightning {
			reference = float64(quote.Debit.Atoms) * 100 / p.Value
		}
		if reference < 1 || reference > 1e12 {
			return nil
		}
		return &treasuryMarket{USD: p.Value, Reference: treasuryAmount{Atoms: int64(math.Round(reference)), Asset: quote.Receive.Asset},
			Difference: int64(math.Round((float64(quote.Receive.Atoms)/reference - 1) * 10000))}
	}
	return nil
}

func treasuryProviderFee(fee *treasuryBridgeFee) *treasuryAmount {
	if fee == nil || fee.Amount < 0 || fee.Amount > treasuryMaxMicros {
		return nil
	}
	if fee.Asset == "USDC" && fee.Decimals == 6 {
		return &treasuryAmount{Atoms: fee.Amount, Asset: "USDC"}
	}
	if strings.EqualFold(fee.Asset, "sat") && fee.Decimals == 0 {
		return &treasuryAmount{Atoms: fee.Amount, Asset: "sats"}
	}
	return nil
}

func treasurySaturateFee(amount int64) int64 {
	return min(200, max(2, (amount+99)/100))
}

func (s *Server) handleTreasuryQuote(w http.ResponseWriter, r *http.Request) {
	session, ok := treasurySession(w, r, s)
	if !ok {
		return
	}
	var req struct {
		Direction string `json:"direction"`
		Amount    int64  `json:"amount"`
		Address   string `json:"lightning_address"`
	}
	if readJSON(r, &req) != nil || req.Amount < 1 || req.Amount > treasuryMaxMicros ||
		(req.Direction != treasuryToSolana && req.Direction != treasuryToLightning) {
		writeError(w, http.StatusBadRequest, "Invalid amount or direction")
		return
	}
	ctx := r.Context()
	settings, err := s.treasuryEffectiveSettings(ctx)
	if err != nil || settings.Key == "" || !validTreasuryWallet(settings.Wallet) {
		writeError(w, http.StatusConflict, "Finish the Treasury setup first")
		return
	}
	if !s.treasuryQuoteMu.TryLock() {
		writeError(w, http.StatusTooManyRequests, "A Treasury quote is already being requested")
		return
	}
	defer s.treasuryQuoteMu.Unlock()
	var pending bool
	err = s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM stablecoin_treasury_orders WHERE state IN ("+treasuryPendingSQL+"))").Scan(&pending)
	if err != nil || pending {
		writeError(w, http.StatusConflict, "A conversion is awaiting reconciliation")
		return
	}
	var recent int
	if err = s.db.QueryRow(ctx, "SELECT count(*) FROM stablecoin_treasury_orders WHERE created_at>now()-interval '1 minute'").Scan(&recent); err != nil || recent >= 8 {
		writeError(w, http.StatusTooManyRequests, "Please wait before requesting more quotes")
		return
	}
	destination, refund := settings.Wallet, settings.Wallet
	order := treasuryOrder{Wallet: settings.Wallet}
	if req.Direction == treasuryToLightning {
		destination = treasuryRecipient(req.Address)
		if destination == "" {
			writeError(w, http.StatusBadRequest, "Invalid Lightning Address")
			return
		}
	} else {
		invoice, err := s.lnd.CreateInvoice(ctx, 0, "USDC Treasury refund", 30*24*3600, nil)
		if err != nil || !mainnetTreasuryInvoice(invoice.PaymentRequest) {
			writeError(w, http.StatusServiceUnavailable, "Mainnet Lightning refund invoice unavailable")
			return
		}
		refund, order.RefundHash = invoice.PaymentRequest, invoice.PaymentHash
	}
	result, err := treasuryCall(ctx, treasuryWorkerRequest{Action: "quote", APIKey: settings.Key, Direction: req.Direction, Amount: req.Amount, Recipient: destination, Refund: refund})
	expiry := time.Unix(result.ExpiresAt, 0).UTC()
	now := time.Now()
	if err != nil || result.BridgeID == "" || result.StatusToken == "" || result.AmountIn <= 0 || result.AmountOut <= 0 ||
		!expiry.After(now.Add(5*time.Second)) || expiry.After(now.Add(24*time.Hour)) {
		writeError(w, http.StatusBadGateway, "Breez quote unavailable")
		return
	}
	id, err := treasuryID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create Treasury quote")
		return
	}
	quote := treasuryQuote{ID: id, Direction: req.Direction, Destination: destination, ExpiresAt: expiry, BridgeFee: treasuryProviderFee(result.ProviderFee)}
	order.BridgeID, order.StatusToken = result.BridgeID, result.StatusToken
	if req.Direction == treasuryToSolana {
		fee := treasurySaturateFee(result.AmountIn)
		decoded, err := s.lnd.DecodeInvoice(ctx, result.Invoice)
		invoiceExpiry := time.Unix(decoded.Timestamp+decoded.Expiry, 0)
		if err != nil || result.AmountIn > treasuryMaxSats || result.AmountOut > treasuryMaxMicros ||
			!mainnetTreasuryInvoice(result.Invoice) || decoded.AmountMsat != 1000*result.AmountIn ||
			decoded.PaymentHash == "" || !invoiceExpiry.After(now.Add(5*time.Second)) {
			writeError(w, http.StatusBadGateway, "Invalid Lightning deposit invoice")
			return
		}
		if invoiceExpiry.Before(quote.ExpiresAt) {
			quote.ExpiresAt = invoiceExpiry
		}
		quote.Debit = treasuryAmount{Atoms: result.AmountIn, Asset: "sats"}
		quote.Receive = treasuryAmount{Atoms: result.AmountOut, Asset: "USDC"}
		quote.RoutingFee = &fee
		order.Invoice, order.PaymentHash = result.Invoice, decoded.PaymentHash
	} else {
		if result.AmountIn > treasuryMaxMicros || result.AmountOut > treasuryMaxSats || !validTreasuryWallet(result.Deposit) || settings.Wallet == result.Deposit {
			writeError(w, http.StatusBadGateway, "Invalid Solana deposit")
			return
		}
		quote.Debit = treasuryAmount{Atoms: result.AmountIn, Asset: "USDC"}
		quote.Receive = treasuryAmount{Atoms: result.AmountOut, Asset: "sats"}
		order.Deposit = result.Deposit
		order.Quote = quote
		prepared, fee, account, code := treasuryPrepareSolana(ctx, order)
		if code != "" {
			writeErrorCode(w, http.StatusConflict, code, "Solana funds or fee estimate unavailable")
			return
		}
		order.SignedOrder = prepared
		quote.SolanaFee, quote.SolanaAccount = &fee, &account
	}
	quote.Market = s.treasuryMarket(ctx, quote)
	order.Quote = quote
	payload, err := json.Marshal(order)
	if err == nil {
		tx, e := s.db.Begin(ctx)
		if e != nil {
			err = e
		} else {
			defer tx.Rollback(ctx)
			var active bool
			err = tx.QueryRow(ctx, "SELECT installed AND enabled AND api_key=$1 AND wallet=$2 FROM stablecoin_treasury_settings WHERE id=1 FOR UPDATE", settings.EncryptedKey, settings.Wallet).Scan(&active)
			if err == nil && !active {
				err = errors.New("settings changed")
			}
			if err == nil {
				_, err = tx.Exec(ctx, "UPDATE stablecoin_treasury_orders SET state='superseded' WHERE state='quoted' AND (session_hash=$1 OR expires_at<=now())", session)
			}
			if err == nil {
				_, err = tx.Exec(ctx, "DELETE FROM stablecoin_treasury_orders WHERE state='superseded' AND created_at<now()-interval '1 day'")
			}
			if err == nil {
				_, err = tx.Exec(ctx, "INSERT INTO stablecoin_treasury_orders(id,session_hash,expires_at,payload) VALUES($1,$2,$3,$4)", id, session, quote.ExpiresAt, payload)
			}
			if err == nil {
				err = tx.Commit(ctx)
			}
		}
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Could not save Treasury quote")
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

func treasuryPrepareSolana(ctx context.Context, order treasuryOrder) (json.RawMessage, int64, int64, string) {
	q := order.Quote
	request := map[string]any{"id": q.ID, "wallet": order.Wallet, "deposit": order.Deposit,
		"depositMicros": strconv.FormatInt(q.Debit.Atoms, 10), "expectedSats": strconv.FormatInt(q.Receive.Atoms, 10),
		"lightningAddress": q.Destination, "expiresAt": q.ExpiresAt.UnixMilli()}
	if q.BridgeFee != nil && q.BridgeFee.Asset == "USDC" {
		request["providerFeeMicros"] = strconv.FormatInt(q.BridgeFee.Atoms, 10)
	}
	body, _ := json.Marshal(request)
	workerRequest := treasuryWorkerRequest{Action: "solana-prepare", Order: body}
	res, err := treasuryCall(ctx, workerRequest)
	// Preparation only reads chain state. Reuse the same provider quote if
	// the socket or RPC failed and there is time left to review the result.
	if err != nil && ctx.Err() == nil && time.Until(q.ExpiresAt) > 35*time.Second {
		res, err = treasuryCall(ctx, workerRequest)
	}
	if res.Status == "insufficient_sol" || res.Status == "insufficient_usdc" {
		return nil, 0, 0, res.Status
	}
	var fees struct {
		Fee     string `json:"feeLamports"`
		Account string `json:"accountLamports"`
	}
	if err != nil || json.Unmarshal(res.Order, &fees) != nil {
		return nil, 0, 0, "solana_estimate_unavailable"
	}
	fee, e1 := strconv.ParseInt(fees.Fee, 10, 64)
	account, e2 := strconv.ParseInt(fees.Account, 10, 64)
	if e1 != nil || e2 != nil || fee <= 0 || fee > treasuryMaxSOL || account < 0 || account > treasuryMaxSOL-fee {
		return nil, 0, 0, "solana_estimate_unavailable"
	}
	return res.Order, fee, account, ""
}

func scanTreasuryOrder(row pgx.Row) (treasuryOrder, error) {
	var o treasuryOrder
	var body []byte
	if err := row.Scan(&body, &o.State, &o.Reference); err != nil {
		return o, err
	}
	return o, json.Unmarshal(body, &o)
}

func (s *Server) treasuryOrder(ctx context.Context, id, session string) (treasuryOrder, error) {
	return scanTreasuryOrder(s.db.QueryRow(ctx, "SELECT payload,state,reference FROM stablecoin_treasury_orders WHERE id=$1 AND session_hash=$2", id, session))
}
