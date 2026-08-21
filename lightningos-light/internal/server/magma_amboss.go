package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	magmaAmbossURL     = "https://api.amboss.space/graphql"
	magmaAmbossTimeout = 25 * time.Second
)

// errMagmaUnauthorized separates "the token is bad or expired" from every other
// failure. The distinction matters: a 401 must never trigger a blind retry loop,
// and it must never let an order advance past the point where funds move.
var errMagmaUnauthorized = errors.New("Amboss rejected the API token (401)")

// magmaAPIError carries the messages Amboss returns inside a 200 response.
// Amboss reports business failures as HTTP 200 with a populated errors[] array,
// so status-code-only checks read those failures as success.
// magmaAPIError carries what Amboss actually returned. Messages is what a human
// reads; Details is the part that used to be thrown away.
//
// A GraphQL error is a message plus a `path` naming the field that failed and an
// `extensions` object that normally carries a machine-readable `code`. Keeping
// only the message cost real time: "Unable to find a route to this destination"
// repeated 75 times says nothing about whether the resolver crashed, rejected
// the input, or genuinely probed and failed - and those need different answers.
type magmaAPIError struct {
	Messages []string
	Details  []magmaAPIErrorDetail
}

// magmaAPIErrorDetail is one GraphQL error with the parts that identify it.
type magmaAPIErrorDetail struct {
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	Code    string `json:"code,omitempty"`
}

func (e *magmaAPIError) Error() string {
	if len(e.Messages) == 0 {
		return "Amboss returned an unspecified error"
	}
	return strings.Join(e.Messages, "; ")
}

// Diagnostic is the full error for logs and order events. It stays off Error()
// so the UI and Telegram keep the plain sentence, while the record that gets
// investigated later keeps the code that identifies the failure.
func (e *magmaAPIError) Diagnostic() string {
	parts := make([]string, 0, len(e.Details))
	for _, detail := range e.Details {
		text := detail.Message
		if detail.Code != "" {
			text += " [code=" + detail.Code + "]"
		}
		if detail.Path != "" {
			text += " [path=" + detail.Path + "]"
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return e.Error()
	}
	return strings.Join(parts, "; ")
}

// magmaJoinJSONPath renders a GraphQL error path, whose elements are field names
// or list indices, in the familiar dotted form.
func magmaJoinJSONPath(path []json.RawMessage) string {
	parts := make([]string, 0, len(path))
	for _, item := range path {
		if value := magmaUnquoteJSON(item); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, ".")
}

// magmaUnquoteJSON returns a JSON string's contents, or the raw token for
// anything else, so a numeric code or list index survives instead of being lost.
func magmaUnquoteJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	return trimmed
}

// magmaNumber decodes Amboss numeric fields. The API is inconsistent by design:
// the same conceptual quantity arrives as a JSON string on some fields
// (size, seller_invoice_amount, fee_above_cap_seconds) and as a JSON number on
// others (locked_fee_rate, locked_min_block_length), and as null or "" when the
// value does not apply yet. Decoding straight into int64 silently zeroes those,
// which would turn a real order into a free one.
type magmaNumber struct {
	Value int64
	Valid bool
}

func (m *magmaNumber) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*m = magmaNumber{}
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			*m = magmaNumber{}
			return nil
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			parsed, floatErr := strconv.ParseFloat(raw, 64)
			if floatErr != nil {
				return fmt.Errorf("invalid Amboss numeric value %q", raw)
			}
			value = int64(parsed)
		}
		*m = magmaNumber{Value: value, Valid: true}
		return nil
	}
	var parsed float64
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("invalid Amboss numeric value %s", trimmed)
	}
	*m = magmaNumber{Value: int64(parsed), Valid: true}
	return nil
}

// magmaRawOrder mirrors the wire shape. The typed projection lives in
// magmaOrder; keeping them apart means a schema change on the Amboss side
// surfaces as a decode problem in one place instead of spreading through the
// service.
type magmaRawOrder struct {
	ID                     string      `json:"id"`
	Status                 string      `json:"status"`
	Account                string      `json:"account"`
	Offer                  string      `json:"offer"`
	OfferSide              string      `json:"offer_side"`
	Size                   magmaNumber `json:"size"`
	SellerInvoiceAmount    magmaNumber `json:"seller_invoice_amount"`
	BuyerInvoiceAmount     magmaNumber `json:"buyer_invoice_amount"`
	AmbossFeeRate          magmaNumber `json:"amboss_fee_rate"`
	FixedFee               magmaNumber `json:"fixed_fee"`
	VariableFee            magmaNumber `json:"variable_fee"`
	LockedFeeRate          magmaNumber `json:"locked_fee_rate"`
	LockedBaseFee          magmaNumber `json:"locked_base_fee"`
	LockedFeeRateCap       magmaNumber `json:"locked_fee_rate_cap"`
	LockedBaseFeeCap       magmaNumber `json:"locked_base_fee_cap"`
	LockedMinBlockLength   magmaNumber `json:"locked_min_block_length"`
	BlocksUntilCanBeClosed magmaNumber `json:"blocks_until_can_be_closed"`
	ClosedBlocksBeforeMin  magmaNumber `json:"closed_blocks_before_min"`
	FeeAboveCapSeconds     magmaNumber `json:"fee_above_cap_seconds"`
	PaymentStatus          string      `json:"payment_status"`
	PaymentHash            string      `json:"payment_hash"`
	ChannelID              string      `json:"channel_id"`
	TransactionID          string      `json:"transaction_id"`
	CreatedAt              string      `json:"created_at"`
	UpdatedAt              string      `json:"updated_at"`
	IsAutomated            bool        `json:"is_automated"`
	ChatEnabled            bool        `json:"chat_enabled"`
	CancellationReason     string      `json:"cancellation_reason"`
	SellerCloseSide        string      `json:"seller_close_side"`
	BuyerCloseSide         string      `json:"buyer_close_side"`
}

// MagmaOrder is the normalized order used by the service and the UI.
//
// Naming trap worth spelling out: LockedFeeRate/LockedBaseFee are the *price of
// the sale* (ppm and sats the buyer pays), while LockedFeeRateCap/
// LockedBaseFeeCap are the *routing fee ceiling* the channel must respect for
// the commitment window. Similar names, opposite meanings.
type MagmaOrder struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	BuyerPubkey string `json:"buyer_pubkey"`
	// BuyerAlias is resolved from our own graph and cached locally; the API does
	// not carry it.
	BuyerAlias             string     `json:"buyer_alias,omitempty"`
	OfferID                string     `json:"offer_id"`
	SizeSat                int64      `json:"size_sat"`
	RevenueSat             int64      `json:"revenue_sat"`
	BuyerPaysSat           int64      `json:"buyer_pays_sat"`
	AmbossFeePPM           int64      `json:"amboss_fee_ppm"`
	PriceFixedSat          int64      `json:"price_fixed_sat"`
	PriceVariableSat       int64      `json:"price_variable_sat"`
	PricePPM               int64      `json:"price_ppm"`
	FeeRateCapPPM          int64      `json:"fee_rate_cap_ppm"`
	BaseFeeCapSat          int64      `json:"base_fee_cap_sat"`
	CommitmentBlocks       int64      `json:"commitment_blocks"`
	BlocksUntilCanBeClosed *int64     `json:"blocks_until_can_be_closed,omitempty"`
	ClosedBlocksBeforeMin  *int64     `json:"closed_blocks_before_min,omitempty"`
	FeeAboveCapSeconds     *int64     `json:"fee_above_cap_seconds,omitempty"`
	PaymentStatus          string     `json:"payment_status,omitempty"`
	PaymentHash            string     `json:"payment_hash,omitempty"`
	ChannelSCID            string     `json:"channel_scid,omitempty"`
	ChannelPoint           string     `json:"channel_point,omitempty"`
	CreatedAt              *time.Time `json:"created_at,omitempty"`
	UpdatedAt              *time.Time `json:"updated_at,omitempty"`
	IsAutomated            bool       `json:"is_automated"`
	ChatEnabled            bool       `json:"chat_enabled"`
	CancellationReason     string     `json:"cancellation_reason,omitempty"`
	SellerCloseSide        string     `json:"seller_close_side,omitempty"`
	BuyerCloseSide         string     `json:"buyer_close_side,omitempty"`

	// Locally tracked execution state. Zero on orders straight off the API; filled
	// in when the order is read back through ListOrders.
	LocalState  string `json:"local_state,omitempty"`
	FundingTxid string `json:"funding_txid,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

// PricePerDayPPM normalizes the sale price over the commitment window. A 180-day
// lock at the price of a 7-day one looks identical on PricePPM alone, and 180-day
// orders are the most common size in practice.
func (o MagmaOrder) PricePerDayPPM() float64 {
	if o.CommitmentBlocks <= 0 {
		return 0
	}
	days := float64(o.CommitmentBlocks) / 144.0
	if days <= 0 {
		return 0
	}
	return float64(o.PricePPM) / days
}

func (r magmaRawOrder) normalize() MagmaOrder {
	order := MagmaOrder{
		ID:                 strings.TrimSpace(r.ID),
		Status:             strings.TrimSpace(r.Status),
		BuyerPubkey:        strings.TrimSpace(r.Account),
		OfferID:            strings.TrimSpace(r.Offer),
		SizeSat:            r.Size.Value,
		RevenueSat:         r.SellerInvoiceAmount.Value,
		BuyerPaysSat:       r.BuyerInvoiceAmount.Value,
		AmbossFeePPM:       r.AmbossFeeRate.Value,
		PriceFixedSat:      r.FixedFee.Value,
		PriceVariableSat:   r.VariableFee.Value,
		PricePPM:           r.LockedFeeRate.Value,
		FeeRateCapPPM:      r.LockedFeeRateCap.Value,
		BaseFeeCapSat:      r.LockedBaseFeeCap.Value,
		CommitmentBlocks:   r.LockedMinBlockLength.Value,
		PaymentStatus:      strings.TrimSpace(r.PaymentStatus),
		PaymentHash:        strings.TrimSpace(r.PaymentHash),
		ChannelSCID:        strings.TrimSpace(r.ChannelID),
		ChannelPoint:       strings.TrimSpace(r.TransactionID),
		IsAutomated:        r.IsAutomated,
		ChatEnabled:        r.ChatEnabled,
		CancellationReason: strings.TrimSpace(r.CancellationReason),
		SellerCloseSide:    strings.TrimSpace(r.SellerCloseSide),
		BuyerCloseSide:     strings.TrimSpace(r.BuyerCloseSide),
	}
	if r.BlocksUntilCanBeClosed.Valid {
		value := r.BlocksUntilCanBeClosed.Value
		order.BlocksUntilCanBeClosed = &value
	}
	if r.ClosedBlocksBeforeMin.Valid {
		value := r.ClosedBlocksBeforeMin.Value
		order.ClosedBlocksBeforeMin = &value
	}
	if r.FeeAboveCapSeconds.Valid {
		value := r.FeeAboveCapSeconds.Value
		order.FeeAboveCapSeconds = &value
	}
	if parsed, ok := parseMagmaTime(r.CreatedAt); ok {
		order.CreatedAt = &parsed
	}
	if parsed, ok := parseMagmaTime(r.UpdatedAt); ok {
		order.UpdatedAt = &parsed
	}
	return order
}

func parseMagmaTime(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// MagmaMarketSummary is the cheap poll. pending_seller_orders is a scalar, so it
// gates the expensive order listing instead of fetching everything each tick.
type MagmaMarketSummary struct {
	Enabled             bool  `json:"enabled"`
	HasActiveOffers     bool  `json:"has_active_offers"`
	PendingSellerOrders int64 `json:"pending_seller_orders"`
	PendingBuyerOrders  int64 `json:"pending_buyer_orders"`
}

type magmaAmbossClient struct {
	endpoint string
	http     *http.Client
}

func newMagmaAmbossClient() *magmaAmbossClient {
	return &magmaAmbossClient{
		endpoint: magmaAmbossURL,
		http:     &http.Client{Timeout: magmaAmbossTimeout},
	}
}

func (c *magmaAmbossClient) do(ctx context.Context, token, query string, variables map[string]any, out any) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("Amboss API token is not configured")
	}
	payload := map[string]any{"query": query}
	if len(variables) > 0 {
		payload["variables"] = variables
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errMagmaUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Amboss returned HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			// Path elements are strings or ints and extensions vary by server, so
			// both are decoded loosely rather than assuming a shape Amboss never
			// promised.
			Path       []json.RawMessage `json:"path"`
			Extensions struct {
				Code json.RawMessage `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("invalid Amboss response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		messages := make([]string, 0, len(envelope.Errors))
		details := make([]magmaAPIErrorDetail, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			message := strings.TrimSpace(item.Message)
			if message != "" {
				messages = append(messages, message)
			}
			details = append(details, magmaAPIErrorDetail{
				Message: message,
				Path:    magmaJoinJSONPath(item.Path),
				Code:    magmaUnquoteJSON(item.Extensions.Code),
			})
		}
		if magmaMessagesIndicateAuthFailure(messages) {
			return errMagmaUnauthorized
		}
		return &magmaAPIError{Messages: messages, Details: details}
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 {
		return errors.New("Amboss returned an empty response")
	}
	return json.Unmarshal(envelope.Data, out)
}

// magmaMessagesIndicateAuthFailure catches the case where Amboss answers 200
// with an auth complaint in errors[] rather than a 401.
func magmaMessagesIndicateAuthFailure(messages []string) bool {
	for _, message := range messages {
		lowered := strings.ToLower(message)
		if strings.Contains(lowered, "unauthorized") ||
			strings.Contains(lowered, "unauthenticated") ||
			strings.Contains(lowered, "invalid token") ||
			strings.Contains(lowered, "expired") {
			return true
		}
	}
	return false
}

const magmaMarketSummaryQuery = `query MagmaMarketSummary {
  getUser {
    market {
      enabled
      has_active_offers
      pending_seller_orders
      pending_buyer_orders
    }
  }
}`

func (c *magmaAmbossClient) MarketSummary(ctx context.Context, token string) (MagmaMarketSummary, error) {
	var result struct {
		GetUser struct {
			Market struct {
				Enabled             bool        `json:"enabled"`
				HasActiveOffers     bool        `json:"has_active_offers"`
				PendingSellerOrders magmaNumber `json:"pending_seller_orders"`
				PendingBuyerOrders  magmaNumber `json:"pending_buyer_orders"`
			} `json:"market"`
		} `json:"getUser"`
	}
	if err := c.do(ctx, token, magmaMarketSummaryQuery, nil, &result); err != nil {
		return MagmaMarketSummary{}, err
	}
	market := result.GetUser.Market
	return MagmaMarketSummary{
		Enabled:             market.Enabled,
		HasActiveOffers:     market.HasActiveOffers,
		PendingSellerOrders: market.PendingSellerOrders.Value,
		PendingBuyerOrders:  market.PendingBuyerOrders.Value,
	}, nil
}

const magmaOrdersQuery = `query MagmaSellerOrders {
  getUser {
    market {
      offer_orders {
        list {
          id
          status
          account
          offer
          offer_side
          size
          seller_invoice_amount
          buyer_invoice_amount
          amboss_fee_rate
          fixed_fee
          variable_fee
          locked_fee_rate
          locked_base_fee
          locked_fee_rate_cap
          locked_base_fee_cap
          locked_min_block_length
          blocks_until_can_be_closed
          closed_blocks_before_min
          fee_above_cap_seconds
          payment_status
          payment_hash
          channel_id
          transaction_id
          created_at
          updated_at
          is_automated
          chat_enabled
          cancellation_reason
          seller_close_side
          buyer_close_side
        }
      }
    }
  }
}`

func (c *magmaAmbossClient) SellerOrders(ctx context.Context, token string) ([]MagmaOrder, error) {
	var result struct {
		GetUser struct {
			Market struct {
				OfferOrders struct {
					List []magmaRawOrder `json:"list"`
				} `json:"offer_orders"`
			} `json:"market"`
		} `json:"getUser"`
	}
	if err := c.do(ctx, token, magmaOrdersQuery, nil, &result); err != nil {
		return nil, err
	}
	raw := result.GetUser.Market.OfferOrders.List
	orders := make([]MagmaOrder, 0, len(raw))
	for _, item := range raw {
		normalized := item.normalize()
		if normalized.ID == "" {
			continue
		}
		orders = append(orders, normalized)
	}
	return orders, nil
}

// The three seller mutations. Signatures and argument names follow the bot that
// has been running these against the live API, so the variable names below are
// deliberately verbatim rather than tidied up.
const (
	magmaAcceptOrderMutation = `mutation AcceptOrder($sellerAcceptOrderId: String!, $request: String!) {
  sellerAcceptOrder(id: $sellerAcceptOrderId, request: $request)
}`
	magmaRejectOrderMutation = `mutation RejectOrder($sellerRejectOrderId: String!) {
  sellerRejectOrder(id: $sellerRejectOrderId)
}`
	magmaAddTransactionMutation = `mutation AddTransaction($sellerAddTransactionId: String!, $transaction: String!) {
  sellerAddTransaction(id: $sellerAddTransactionId, transaction: $transaction)
}`
)

// AcceptOrder hands Amboss the bolt11 invoice the buyer must pay. The mutation
// argument is named "request" and it takes the payment request, not the hash.
func (c *magmaAmbossClient) AcceptOrder(ctx context.Context, token, orderID, paymentRequest string) error {
	orderID = strings.TrimSpace(orderID)
	paymentRequest = strings.TrimSpace(paymentRequest)
	if orderID == "" {
		return errors.New("order id required")
	}
	if paymentRequest == "" {
		return errors.New("payment request required")
	}
	var result struct {
		SellerAcceptOrder any `json:"sellerAcceptOrder"`
	}
	if err := c.do(ctx, token, magmaAcceptOrderMutation, map[string]any{
		"sellerAcceptOrderId": orderID,
		"request":             paymentRequest,
	}, &result); err != nil {
		return err
	}
	if !magmaMutationSucceeded(result.SellerAcceptOrder) {
		return fmt.Errorf("Amboss did not accept the invoice for order %s", orderID)
	}
	return nil
}

// RejectOrder declines an order explicitly. Letting an unwanted order lapse is
// not free: Amboss records SELLER_FAILED_TO_REACT against the account.
func (c *magmaAmbossClient) RejectOrder(ctx context.Context, token, orderID string) error {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return errors.New("order id required")
	}
	var result struct {
		SellerRejectOrder any `json:"sellerRejectOrder"`
	}
	if err := c.do(ctx, token, magmaRejectOrderMutation, map[string]any{
		"sellerRejectOrderId": orderID,
	}, &result); err != nil {
		return err
	}
	if !magmaMutationSucceeded(result.SellerRejectOrder) {
		return fmt.Errorf("Amboss did not register the rejection of order %s", orderID)
	}
	return nil
}

// AddTransaction confirms the funding outpoint. It takes the full channel point
// (txid:vout): real orders carry both :0 and :1, so the vout is not decorative.
func (c *magmaAmbossClient) AddTransaction(ctx context.Context, token, orderID, channelPoint string) error {
	orderID = strings.TrimSpace(orderID)
	channelPoint = strings.TrimSpace(channelPoint)
	if orderID == "" {
		return errors.New("order id required")
	}
	if !magmaLooksLikeChannelPoint(channelPoint) {
		return fmt.Errorf("channel point must be txid:vout, got %q", channelPoint)
	}
	var result struct {
		SellerAddTransaction any `json:"sellerAddTransaction"`
	}
	if err := c.do(ctx, token, magmaAddTransactionMutation, map[string]any{
		"sellerAddTransactionId": orderID,
		"transaction":            channelPoint,
	}, &result); err != nil {
		return err
	}
	if !magmaMutationSucceeded(result.SellerAddTransaction) {
		return fmt.Errorf("Amboss did not register the channel point for order %s", orderID)
	}
	return nil
}

// magmaMutationSucceeded reads the scalar these mutations return. Amboss answers
// with a bare boolean or string rather than an object, and a false there is a
// real failure even though the HTTP call and the errors[] array both look clean.
func magmaMutationSucceeded(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed != "" && !strings.EqualFold(trimmed, "false")
	case float64:
		return typed != 0
	case nil:
		return false
	default:
		return true
	}
}

// magmaLooksLikeChannelPoint guards the single most expensive mistake available
// here: confirming a malformed or bare-txid outpoint after the channel is
// already funded.
func magmaLooksLikeChannelPoint(value string) bool {
	txid, vout, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found || len(txid) != 64 {
		return false
	}
	for _, char := range txid {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	if vout == "" {
		return false
	}
	if _, err := strconv.ParseUint(vout, 10, 32); err != nil {
		return false
	}
	return true
}

const magmaNodeAddressesQuery = `query MagmaNodeAddresses($pubkey: String!) {
  getNode(pubkey: $pubkey) {
    graph_info {
      node {
        addresses {
          addr
        }
      }
    }
  }
}`

// NodeAddresses is the fallback path for connecting to a buyer our own graph has
// not seen yet.
func (c *magmaAmbossClient) NodeAddresses(ctx context.Context, token, pubkey string) ([]string, error) {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return nil, errors.New("pubkey required")
	}
	var result struct {
		GetNode struct {
			GraphInfo struct {
				Node struct {
					Addresses []struct {
						Addr string `json:"addr"`
					} `json:"addresses"`
				} `json:"node"`
			} `json:"graph_info"`
		} `json:"getNode"`
	}
	if err := c.do(ctx, token, magmaNodeAddressesQuery, map[string]any{"pubkey": pubkey}, &result); err != nil {
		return nil, err
	}
	addresses := make([]string, 0, len(result.GetNode.GraphInfo.Node.Addresses))
	for _, item := range result.GetNode.GraphInfo.Node.Addresses {
		addr := strings.TrimSpace(item.Addr)
		if addr != "" {
			addresses = append(addresses, addr)
		}
	}
	return addresses, nil
}

// MagmaOfferCondition filters who may buy, enforced by Amboss before the order
// is created. That is strictly better than our policy rejecting it afterwards:
// a rejection costs a sellerRejectOrder and stays on the account, while a
// condition means the order never arrives.
type MagmaOfferCondition struct {
	Condition string `json:"condition"`
	Operator  string `json:"operator"`
	Value     string `json:"value"`
}

// MagmaOffer mirrors the Amboss sell-offer form.
type MagmaOffer struct {
	ID           string `json:"id,omitempty"`
	Status       string `json:"status,omitempty"`
	Side         string `json:"side,omitempty"`
	TotalSizeSat int64  `json:"total_size_sat"`
	// SoldSat is what the offer's orders have already locked, and RemainingSat is
	// what is left to sell. Both are read-only: Amboss derives them from the order
	// book, so they are ignored when an offer is created or updated.
	SoldSat        int64                 `json:"sold_sat"`
	RemainingSat   int64                 `json:"remaining_sat"`
	MinSizeSat     int64                 `json:"min_size_sat"`
	MaxSizeSat     int64                 `json:"max_size_sat"`
	FeeRatePPM     int64                 `json:"fee_rate_ppm"`
	BaseFeeSat     int64                 `json:"base_fee_sat"`
	FeeRateCapPPM  int64                 `json:"fee_rate_cap_ppm"`
	BaseFeeCapSat  int64                 `json:"base_fee_cap_sat"`
	MinBlockLength int64                 `json:"min_block_length"`
	Conditions     []MagmaOfferCondition `json:"conditions,omitempty"`
	SellerScore    string                `json:"seller_score,omitempty"`

	// The fixed fee is either a number the seller picks, or one Amboss derives
	// from the mempool. Automatic is priority x multiplier, where the multiplier
	// is how many on-chain transactions the sale should cover (2x = open + close).
	// Automatic is what protects the margin when fees spike: the price rises with
	// the mempool instead of the sale becoming unprofitable.
	FixedFeeMode      string `json:"fixed_fee_mode"`
	OnchainPriority   string `json:"onchain_priority,omitempty"`
	OnchainMultiplier int64  `json:"onchain_multiplier,omitempty"`
}

const (
	magmaFixedFeeManual    = "manual"
	magmaFixedFeeAutomatic = "automatic"
)

var magmaOnchainPriorityValues = map[string]bool{"HIGH": true, "MEDIUM": true, "LOW": true}

// magmaOfferConditionValues and magmaOfferOperatorValues are the enums the API
// accepts. Validated locally so a typo fails here instead of arriving as an
// opaque GraphQL error after the operator hit save.
var magmaOfferConditionValues = map[string]bool{
	"NODE_CAPACITY": true, "NODE_CHANNELS": true, "NODE_SOCKETS": true,
	"PARALLEL_CHANNELS": true, "TERMINAL_WEB_RANK": true,
}

var magmaOfferOperatorValues = map[string]bool{
	"CONTAINS": true, "DOES_NOT_CONTAIN": true, "EQUAL_TO": true,
	"GREATER_THAN": true, "GREATER_THAN_OR_EQUAL_TO": true,
	"LESS_THAN": true, "LESS_THAN_OR_EQUAL_TO": true, "NOT_EQUAL_TO": true,
}

func (o MagmaOffer) validate() error {
	if o.TotalSizeSat <= 0 {
		return errors.New("total size must be positive")
	}
	if o.MinSizeSat <= 0 {
		return errors.New("minimum channel size must be positive")
	}
	if o.MaxSizeSat > 0 && o.MaxSizeSat < o.MinSizeSat {
		return errors.New("maximum channel size cannot be below the minimum")
	}
	if o.MinSizeSat > o.TotalSizeSat {
		return errors.New("minimum channel size cannot exceed the total size on offer")
	}
	if o.MinBlockLength <= 0 {
		return errors.New("channel duration must be positive")
	}
	if o.FixedFeeMode == magmaFixedFeeAutomatic {
		if !magmaOnchainPriorityValues[o.OnchainPriority] {
			return fmt.Errorf("unknown mempool priority %q", o.OnchainPriority)
		}
		if o.OnchainMultiplier < 1 || o.OnchainMultiplier > 5 {
			return errors.New("the fee multiplier must be between 1 and 5")
		}
	}
	for _, condition := range o.Conditions {
		if !magmaOfferConditionValues[condition.Condition] {
			return fmt.Errorf("unknown buyer condition %q", condition.Condition)
		}
		if !magmaOfferOperatorValues[condition.Operator] {
			return fmt.Errorf("unknown condition operator %q", condition.Operator)
		}
		if strings.TrimSpace(condition.Value) == "" {
			return fmt.Errorf("condition %s needs a value", condition.Condition)
		}
	}
	return nil
}

// offerInput builds the mutation payload. Amboss types every amount as Float.
func (o MagmaOffer) offerInput() map[string]any {
	input := map[string]any{
		"total_size":       o.TotalSizeSat,
		"min_size":         o.MinSizeSat,
		"min_block_length": o.MinBlockLength,
		"fee_rate":         o.FeeRatePPM,
		"fee_rate_cap":     o.FeeRateCapPPM,
		"base_fee_cap":     o.BaseFeeCapSat,
	}
	// Mutually exclusive, mirroring the form: a manual fixed fee is a number, an
	// automatic one is a mempool priority and a multiplier. Sending both would
	// leave it ambiguous which one Amboss should honour.
	//
	// These two fields were missing from the first version of this call. Because
	// an update sends the whole offer, omitting them would have quietly reset an
	// automatic offer to a manual fee of whatever base_fee happened to hold.
	if o.FixedFeeMode == magmaFixedFeeAutomatic {
		input["onchain_priority"] = o.OnchainPriority
		input["onchain_multiplier"] = o.OnchainMultiplier
	} else {
		input["base_fee"] = o.BaseFeeSat
	}
	if o.MaxSizeSat > 0 {
		input["max_size"] = o.MaxSizeSat
	}
	conditions := make([]map[string]any, 0, len(o.Conditions))
	for _, condition := range o.Conditions {
		conditions = append(conditions, map[string]any{
			"condition": condition.Condition,
			"operator":  condition.Operator,
			"value":     strings.TrimSpace(condition.Value),
		})
	}
	// Always sent, so clearing every condition actually clears them instead of
	// leaving the previous set in place.
	input["conditions"] = conditions
	return input
}

const magmaOffersQuery = `query MagmaOffers {
  getUser {
    market {
      offers {
        list {
          id
          status
          side
          total_size
          min_size
          max_size
          fee_rate
          base_fee
          fee_rate_cap
          base_fee_cap
          min_block_length
          seller_score
          onchain_priority
          onchain_multiplier
          orders { locked_size }
          conditions { condition operator value }
        }
      }
    }
  }
}`

func (c *magmaAmbossClient) Offers(ctx context.Context, token string) ([]MagmaOffer, error) {
	var result struct {
		GetUser struct {
			Market struct {
				Offers struct {
					List []struct {
						ID                string      `json:"id"`
						Status            string      `json:"status"`
						Side              string      `json:"side"`
						TotalSize         magmaNumber `json:"total_size"`
						MinSize           magmaNumber `json:"min_size"`
						MaxSize           magmaNumber `json:"max_size"`
						FeeRate           magmaNumber `json:"fee_rate"`
						BaseFee           magmaNumber `json:"base_fee"`
						FeeRateCap        magmaNumber `json:"fee_rate_cap"`
						BaseFeeCap        magmaNumber `json:"base_fee_cap"`
						MinBlockLength    magmaNumber `json:"min_block_length"`
						SellerScore       string      `json:"seller_score"`
						OnchainPriority   string      `json:"onchain_priority"`
						OnchainMultiplier magmaNumber `json:"onchain_multiplier"`
						Orders            struct {
							LockedSize magmaNumber `json:"locked_size"`
						} `json:"orders"`
						Conditions []struct {
							Condition string `json:"condition"`
							Operator  string `json:"operator"`
							Value     string `json:"value"`
						} `json:"conditions"`
					} `json:"list"`
				} `json:"offers"`
			} `json:"market"`
		} `json:"getUser"`
	}
	if err := c.do(ctx, token, magmaOffersQuery, nil, &result); err != nil {
		return nil, err
	}
	raw := result.GetUser.Market.Offers.List
	offers := make([]MagmaOffer, 0, len(raw))
	for _, item := range raw {
		offer := MagmaOffer{
			ID:                strings.TrimSpace(item.ID),
			Status:            strings.TrimSpace(item.Status),
			Side:              strings.TrimSpace(item.Side),
			TotalSizeSat:      item.TotalSize.Value,
			MinSizeSat:        item.MinSize.Value,
			MaxSizeSat:        item.MaxSize.Value,
			FeeRatePPM:        item.FeeRate.Value,
			BaseFeeSat:        item.BaseFee.Value,
			FeeRateCapPPM:     item.FeeRateCap.Value,
			BaseFeeCapSat:     item.BaseFeeCap.Value,
			MinBlockLength:    item.MinBlockLength.Value,
			SellerScore:       strings.TrimSpace(item.SellerScore),
			OnchainPriority:   strings.TrimSpace(item.OnchainPriority),
			OnchainMultiplier: item.OnchainMultiplier.Value,
			SoldSat:           item.Orders.LockedSize.Value,
		}
		// Amboss never decrements total_size; what an offer can still sell is the
		// difference against what its orders have locked. Once that drops below
		// min_size the offer is dead in practice while still reading ENABLED.
		offer.RemainingSat = offer.TotalSizeSat - offer.SoldSat
		if offer.RemainingSat < 0 {
			offer.RemainingSat = 0
		}
		// A stored priority is what marks the offer as automatic; Amboss keeps a
		// computed base_fee alongside it, so base_fee alone cannot tell them apart.
		if offer.OnchainPriority != "" {
			offer.FixedFeeMode = magmaFixedFeeAutomatic
		} else {
			offer.FixedFeeMode = magmaFixedFeeManual
		}
		for _, condition := range item.Conditions {
			offer.Conditions = append(offer.Conditions, MagmaOfferCondition{
				Condition: condition.Condition,
				Operator:  condition.Operator,
				Value:     condition.Value,
			})
		}
		offers = append(offers, offer)
	}
	return offers, nil
}

const (
	magmaCreateOfferMutation = `mutation CreateOffer($input: CreateOffer!) {
  createOffer(input: $input)
}`
	magmaUpdateOfferMutation = `mutation UpdateOffer($input: UpdateOffer!) {
  updateOffer(input: $input)
}`
	magmaToggleOfferMutation = `mutation ToggleOffer($id: String!) {
  toggleOffer(id: $id)
}`
)

func (c *magmaAmbossClient) CreateOffer(ctx context.Context, token string, offer MagmaOffer) error {
	if err := offer.validate(); err != nil {
		return err
	}
	input := offer.offerInput()
	input["offer_side"] = "SELL"
	input["offer_type"] = "CHANNEL"
	var result struct {
		CreateOffer any `json:"createOffer"`
	}
	if err := c.do(ctx, token, magmaCreateOfferMutation, map[string]any{"input": input}, &result); err != nil {
		return err
	}
	if !magmaMutationSucceeded(result.CreateOffer) {
		return errors.New("Amboss did not create the offer")
	}
	return nil
}

func (c *magmaAmbossClient) UpdateOffer(ctx context.Context, token string, offer MagmaOffer) error {
	if strings.TrimSpace(offer.ID) == "" {
		return errors.New("offer id required")
	}
	if err := offer.validate(); err != nil {
		return err
	}
	input := offer.offerInput()
	input["offer"] = strings.TrimSpace(offer.ID)
	var result struct {
		UpdateOffer any `json:"updateOffer"`
	}
	if err := c.do(ctx, token, magmaUpdateOfferMutation, map[string]any{"input": input}, &result); err != nil {
		return err
	}
	if !magmaMutationSucceeded(result.UpdateOffer) {
		return errors.New("Amboss did not update the offer")
	}
	return nil
}

// ToggleOffer flips enabled/disabled. Amboss exposes no explicit setter, so the
// caller must read the current status to know what a toggle will do.
func (c *magmaAmbossClient) ToggleOffer(ctx context.Context, token, offerID string) error {
	offerID = strings.TrimSpace(offerID)
	if offerID == "" {
		return errors.New("offer id required")
	}
	var result struct {
		ToggleOffer any `json:"toggleOffer"`
	}
	if err := c.do(ctx, token, magmaToggleOfferMutation, map[string]any{"id": offerID}, &result); err != nil {
		return err
	}
	if !magmaMutationSucceeded(result.ToggleOffer) {
		return errors.New("Amboss did not toggle the offer")
	}
	return nil
}

// magmaTokenExpiry decodes the exp claim when the token is a JWT. Amboss issues
// JWTs (iss: amboss.tech) that expire, so a token that works today can stop
// working mid-sale without anything else changing. Tokens that are not JWTs are
// reported as having no known expiry rather than as invalid.
func magmaTokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0).UTC(), true
}
