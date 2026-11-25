package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
)

const (
	treasuryKeyEnv              = "BREEZ_TREASURY_API_KEY"
	treasuryConfigScope         = "stablecoin_treasury_config"
	treasuryToBase              = "to_base"
	treasuryToLightning         = "to_lightning"
	treasuryMaximumUSDCMicros   = 100_000_000
	treasuryLightningDailyLimit = 150_000
)

type treasuryQuoteRequest struct {
	Direction string `json:"direction"`
	Wallet    string `json:"wallet"`
	Amount    int64  `json:"usdc_micros"`
	MaxFee    int64  `json:"max_routing_sat"`
}

type treasuryOrder struct {
	ID               string               `json:"id"`
	Direction        string               `json:"direction"`
	Wallet           string               `json:"wallet"`
	Lightning        string               `json:"lightning_address,omitempty"`
	Deposit          string               `json:"deposit_address,omitempty"`
	AmountIn         int64                `json:"amount_in"`
	AmountOut        int64                `json:"amount_out"`
	ProviderFee      *treasuryProviderFee `json:"provider_fee,omitempty"`
	MaxFee           int64                `json:"max_routing_sat,omitempty"`
	EstimatedFeeMsat int64                `json:"estimated_routing_msat,omitempty"`
	Expires          time.Time            `json:"expires_at"`
	Created          time.Time            `json:"created_at"`
	State            string               `json:"state"`
	DeliveredAmount  int64                `json:"delivered_amount,omitempty"`
	DeliveryRef      string               `json:"delivery_ref,omitempty"`
	ActualFeeMsat    int64                `json:"actual_routing_msat,omitempty"`
	ActualBaseFeeWei string               `json:"actual_base_fee_wei,omitempty"`
	RefundedAmount   int64                `json:"refunded_amount,omitempty"`
}

type treasuryPrivateOrder struct {
	Session     string                  `json:"session"`
	Token       string                  `json:"status_token"`
	Invoice     string                  `json:"invoice,omitempty"`
	RefundHash  string                  `json:"refund_hash,omitempty"`
	BaseSigning *treasurySigningRequest `json:"base_signing,omitempty"`
	FundingRef  string                  `json:"funding_ref,omitempty"`
}

func (q *treasuryQuoteRequest) validate() bool {
	q.Wallet = treasuryBaseAddress(strings.TrimSpace(q.Wallet))
	if q.Wallet == "" || q.Amount <= 0 || q.Amount > treasuryMaximumUSDCMicros {
		return false
	}
	switch q.Direction {
	case treasuryToBase:
		return q.MaxFee >= 1 && q.MaxFee <= 1_000
	case treasuryToLightning:
		return q.MaxFee == 0
	default:
		return false
	}
}

func (s *Server) makeTreasuryQuote(ctx context.Context, session string, request treasuryQuoteRequest) (treasuryOrder, error) {
	if !request.validate() {
		return treasuryOrder{}, errors.New("invalid treasury quote")
	}
	rate, err := s.db.Exec(ctx, `UPDATE stablecoin_treasury_config SET next_quote_at=now()+interval '5 seconds'
		WHERE singleton AND installed AND enabled AND next_quote_at <= now()`)
	if err != nil || rate.RowsAffected() != 1 {
		return treasuryOrder{}, errors.New("treasury quote unavailable or requested too recently")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return treasuryOrder{}, errors.New("treasury state unavailable")
	}
	defer tx.Rollback(ctx)
	var installed, enabled bool
	var lightning string
	if err := tx.QueryRow(ctx, `SELECT installed, enabled, lightning_address FROM stablecoin_treasury_config
		WHERE singleton FOR UPDATE`).Scan(&installed, &enabled, &lightning); err != nil || !installed || !enabled {
		return treasuryOrder{}, errors.New("stablecoin treasury is stopped")
	}
	key, err := readEnvFileValue(secretsPath, treasuryKeyEnv)
	if err != nil || key == "" {
		return treasuryOrder{}, errors.New("configure the Breez API key first")
	}
	var allRecent, recent int
	if err := tx.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE created_at > now()-interval '1 minute')
		FROM stablecoin_treasury_orders WHERE created_at > now()-interval '1 day'`).Scan(&allRecent, &recent); err != nil || allRecent >= 100 || recent >= 4 {
		return treasuryOrder{}, errors.New("treasury quotation limit reached")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return treasuryOrder{}, errors.New("could not identify treasury order")
	}
	order := treasuryOrder{
		ID: hex.EncodeToString(id[:]), Direction: request.Direction, Wallet: request.Wallet,
		Created: time.Now().UTC(), State: "quoted", MaxFee: request.MaxFee,
	}
	private := treasuryPrivateOrder{Session: session}
	payload := treasuryBridgeRequest{
		Action: "quote", APIKey: key, Direction: request.Direction, Amount: request.Amount,
	}
	if request.Direction == treasuryToBase {
		invoice, err := s.lnd.CreateInvoice(ctx, 0, "", 7*24*60*60, nil)
		if err != nil || !strings.HasPrefix(invoice.PaymentRequest, "lnbc") ||
			treasuryNormalizeHex(invoice.PaymentHash, 32) == "" {
			return treasuryOrder{}, errors.New("could not create a local Lightning refund invoice")
		}
		private.RefundHash = invoice.PaymentHash
		payload.Recipient, payload.Refund = request.Wallet, invoice.PaymentRequest
	} else {
		if !isLightningAddress(lightning) {
			return treasuryOrder{}, errors.New("configure a Lightning Address pointing to this node")
		}
		order.Lightning = lightning
		payload.Recipient, payload.Refund = lightning, request.Wallet
	}
	quote, err := treasuryBridgeCall(ctx, payload)
	if err != nil {
		return treasuryOrder{}, err
	}
	order.Expires = time.Unix(quote.ExpiresAt, 0).UTC()
	if time.Until(order.Expires) < 20*time.Second || time.Until(order.Expires) > 10*time.Minute ||
		quote.BridgeID == "" || quote.StatusToken == "" || quote.AmountIn <= 0 || quote.AmountOut <= 0 {
		return treasuryOrder{}, errors.New("provider returned an invalid or short-lived quote")
	}
	private.Token = quote.StatusToken
	order.AmountIn, order.AmountOut, order.ProviderFee = quote.AmountIn, quote.AmountOut, quote.ProviderFee
	var invoiceHash *string
	if request.Direction == treasuryToBase {
		if quote.AmountIn > treasuryLightningDailyLimit-request.MaxFee || quote.AmountOut > treasuryMaximumUSDCMicros*2 {
			return treasuryOrder{}, errors.New("Lightning debit exceeds the treasury limit")
		}
		invoice, err := s.lnd.DecodeInvoice(ctx, quote.Invoice)
		if err != nil || !strings.HasPrefix(quote.Invoice, "lnbc") || invoice.AmountMsat != quote.AmountIn*1000 ||
			treasuryNormalizeHex(invoice.PaymentHash, 32) == "" ||
			time.Unix(invoice.Timestamp, 0).Add(time.Duration(invoice.Expiry)*time.Second).Before(order.Expires) {
			return treasuryOrder{}, errors.New("the provider invoice differs from the quote")
		}
		private.Invoice, invoiceHash = quote.Invoice, &invoice.PaymentHash
		routeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		preview, err := s.lnd.PreviewPayment(routeCtx, quote.Invoice, nil, request.MaxFee, 3)
		cancel()
		if err != nil || !preview.LiquidityValidated || preview.ValidatedRouteCount == 0 ||
			preview.SuggestedMaxFeeMsat > request.MaxFee*1000 || time.Until(order.Expires) < 15*time.Second {
			return treasuryOrder{}, errors.New("no Lightning route validated within the quoted fee limit")
		}
		order.EstimatedFeeMsat = preview.SuggestedMaxFeeMsat
	} else {
		order.Deposit = treasuryBaseAddress(quote.Deposit)
		if order.Deposit == "" || order.Deposit == order.Wallet || order.AmountIn > treasuryMaximumUSDCMicros*2 ||
			order.AmountOut > treasuryLightningDailyLimit*2 {
			return treasuryOrder{}, errors.New("invalid Base deposit")
		}
		identity, err := s.lnd.RuntimeInfo(ctx, false)
		if err != nil || identity.Pubkey == "" {
			return treasuryOrder{}, errors.New("local LND unavailable")
		}
		_, err = resolveLightningAddress(ctx, lightning, quote.AmountOut, "", func(ctx context.Context, invoice string) (lndclient.DecodedInvoice, error) {
			decoded, err := s.lnd.DecodeInvoice(ctx, invoice)
			if err == nil && !strings.EqualFold(decoded.Destination, identity.Pubkey) {
				return lndclient.DecodedInvoice{}, errors.New("Lightning Address points to a different node")
			}
			return decoded, err
		})
		if err != nil || time.Until(order.Expires) < 15*time.Second {
			return treasuryOrder{}, errors.New("could not verify that the Lightning Address pays this LND")
		}
	}
	if err := insertTreasuryQuote(ctx, tx, order, private, quote.BridgeID, invoiceHash); err != nil {
		return treasuryOrder{}, err
	}
	return order, nil
}

func insertTreasuryQuote(ctx context.Context, tx pgx.Tx, order treasuryOrder, private treasuryPrivateOrder, bridgeID string, invoiceHash *string) error {
	publicData, err := json.Marshal(order)
	if err != nil {
		return err
	}
	privateData, err := json.Marshal(private)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stablecoin_treasury_orders
		(id, bridge_id, invoice_hash, direction, state, created_at, expires_at, public_data, private_data)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		order.ID, bridgeID, invoiceHash, order.Direction, order.State, order.Created, order.Expires, publicData, privateData); err != nil {
		return errors.New("could not persist the treasury quote")
	}
	if tx.Commit(ctx) != nil {
		return errors.New("could not commit the treasury quote")
	}
	return nil
}
