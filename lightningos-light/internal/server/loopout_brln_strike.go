package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	loopOutBRLNStrikeAPIKeyEnv = "LOOPOUT_BRLN_STRIKE_API_KEY"
	loopOutBRLNStrikeAPIURL    = "https://api.strike.me/v1"

	strikeReturnPending        = "pending"
	strikeReturnPreparing      = "preparing"
	strikeReturnWaitingBalance = "waiting_balance"
	strikeReturnQuoted         = "quoted"
	strikeReturnSubmitted      = "submitted"
	strikeReturnCompleted      = "completed"
	strikeReturnFailed         = "failed"
	strikeReturnNeedsReview    = "needs_review"
)

type LoopOutBRLNStrikeReturn struct {
	ID                       int64      `json:"id"`
	JobID                    int64      `json:"job_id"`
	Automatic                bool       `json:"automatic"`
	Status                   string     `json:"status"`
	AmountSat                int64      `json:"amount_sat"`
	BTCAddress               string     `json:"btc_address,omitempty"`
	QuoteID                  string     `json:"quote_id,omitempty"`
	PaymentID                string     `json:"payment_id,omitempty"`
	TxID                     string     `json:"txid,omitempty"`
	FeeSat                   int64      `json:"fee_sat"`
	EstimatedDeliveryMinutes int        `json:"estimated_delivery_minutes"`
	LastError                string     `json:"last_error,omitempty"`
	NextCheckAt              time.Time  `json:"next_check_at"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
	CompletedAt              *time.Time `json:"completed_at,omitempty"`
	idempotencyKey           string
}

type loopOutBRLNStrikeMoney struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type loopOutBRLNStrikeBalance struct {
	Currency  string `json:"currency"`
	Available string `json:"available"`
}

type loopOutBRLNStrikeTier struct {
	ID                          string                  `json:"id"`
	EstimatedDeliveryDurationIn int                     `json:"estimatedDeliveryDurationInMin"`
	EstimatedFee                loopOutBRLNStrikeMoney  `json:"estimatedFee"`
	MinimumAmount               *loopOutBRLNStrikeMoney `json:"minimumAmount,omitempty"`
}

type loopOutBRLNStrikeQuote struct {
	PaymentQuoteID              string                  `json:"paymentQuoteId"`
	ValidUntil                  time.Time               `json:"validUntil"`
	TotalFee                    *loopOutBRLNStrikeMoney `json:"totalFee,omitempty"`
	TotalAmount                 loopOutBRLNStrikeMoney  `json:"totalAmount"`
	EstimatedDeliveryDurationIn int                     `json:"estimatedDeliveryDurationInMin,omitempty"`
}

type loopOutBRLNStrikePayment struct {
	PaymentID string `json:"paymentId"`
	State     string `json:"state"`
	Onchain   *struct {
		TxID string `json:"txnId"`
	} `json:"onchain,omitempty"`
}

type loopOutBRLNStrikeAPIError struct {
	Status  int
	Code    string
	Message string
	Values  map[string]any
}

func (e *loopOutBRLNStrikeAPIError) Error() string {
	if strings.TrimSpace(e.Code) != "" {
		return e.Code
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return fmt.Sprintf("Strike API returned HTTP %d", e.Status)
}

type loopOutBRLNStrikeClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func newLoopOutBRLNStrikeClient(apiKey string) *loopOutBRLNStrikeClient {
	return &loopOutBRLNStrikeClient{
		baseURL: loopOutBRLNStrikeAPIURL,
		apiKey:  strings.TrimSpace(apiKey),
		client:  &http.Client{Timeout: 25 * time.Second},
	}
}

func (c *loopOutBRLNStrikeClient) request(ctx context.Context, method, path, idempotencyKey string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.baseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("idempotency-key", idempotencyKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Data struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Values  map[string]any `json:"values"`
			} `json:"data"`
		}
		_ = json.Unmarshal(raw, &envelope)
		return &loopOutBRLNStrikeAPIError{
			Status: resp.StatusCode, Code: envelope.Data.Code, Message: envelope.Data.Message, Values: envelope.Data.Values,
		}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *loopOutBRLNStrikeClient) balances(ctx context.Context) ([]loopOutBRLNStrikeBalance, error) {
	var result []loopOutBRLNStrikeBalance
	err := c.request(ctx, http.MethodGet, "/balances", "", nil, &result)
	return result, err
}

func (c *loopOutBRLNStrikeClient) tiers(ctx context.Context, address string, amountSat int64) ([]loopOutBRLNStrikeTier, error) {
	var result []loopOutBRLNStrikeTier
	err := c.request(ctx, http.MethodPost, "/payment-quotes/onchain/tiers", "", map[string]any{
		"btcAddress": address,
		"amount":     loopOutBRLNStrikeMoney{Amount: satsToBTC(amountSat), Currency: "BTC"},
	}, &result)
	return result, err
}

func (c *loopOutBRLNStrikeClient) createQuote(ctx context.Context, address string, amountSat int64, tierID, idempotencyKey string) (loopOutBRLNStrikeQuote, error) {
	var result loopOutBRLNStrikeQuote
	err := c.request(ctx, http.MethodPost, "/payment-quotes/onchain", idempotencyKey, map[string]any{
		"btcAddress":     address,
		"sourceCurrency": "BTC",
		"description":    "Loop Out BRLN return to own LightningOS node",
		"amount": map[string]any{
			"amount":    satsToBTC(amountSat),
			"currency":  "BTC",
			"feePolicy": "INCLUSIVE",
		},
		"onchainTierId": tierID,
		"beneficiary": map[string]any{
			"isOwnDestination": true,
			"type":             "SELF",
			"destinationType":  "SELF_CUSTODY_WALLET",
		},
	}, &result)
	return result, err
}

func (c *loopOutBRLNStrikeClient) executeQuote(ctx context.Context, quoteID string) (loopOutBRLNStrikePayment, error) {
	var result loopOutBRLNStrikePayment
	err := c.request(ctx, http.MethodPatch, "/payment-quotes/"+quoteID+"/execute", "", nil, &result)
	return result, err
}

func (c *loopOutBRLNStrikeClient) payment(ctx context.Context, paymentID string) (loopOutBRLNStrikePayment, error) {
	var result loopOutBRLNStrikePayment
	err := c.request(ctx, http.MethodGet, "/payments/"+paymentID, "", nil, &result)
	return result, err
}

func isStrikeLightningAddress(address string) bool {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(address)), "@")
	return len(parts) == 2 && parts[0] != "" && parts[1] == "strike.me"
}

func satsToBTC(amountSat int64) string {
	if amountSat < 0 {
		amountSat = 0
	}
	return fmt.Sprintf("%d.%08d", amountSat/100_000_000, amountSat%100_000_000)
}

func strikeBTCToSats(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("empty BTC amount")
	}
	if strings.HasPrefix(value, "-") {
		return 0, errors.New("negative BTC amount")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return 0, errors.New("invalid BTC amount")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > loopOutBRLNMaxAmountSat/100_000_000 {
		return 0, errors.New("invalid BTC amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 8 {
		if strings.Trim(fraction[8:], "0") != "" {
			return 0, errors.New("BTC amount has sub-satoshi precision")
		}
		fraction = fraction[:8]
	}
	fraction += strings.Repeat("0", 8-len(fraction))
	fractionSat := int64(0)
	if fraction != "" {
		fractionSat, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, errors.New("invalid BTC amount")
		}
	}
	return whole*100_000_000 + fractionSat, nil
}

func newStrikeIdempotencyKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}

func (s *LoopOutBRLNService) strikeAPIKey() (string, error) {
	key, err := readEnvFileValue(secretsPath, loopOutBRLNStrikeAPIKeyEnv)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("Strike account is not connected")
	}
	if err != nil {
		return "", err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("Strike account is not connected")
	}
	return key, nil
}

func (s *LoopOutBRLNService) newStrikeClient() (*loopOutBRLNStrikeClient, error) {
	key, err := s.strikeAPIKey()
	if err != nil {
		return nil, err
	}
	return newLoopOutBRLNStrikeClient(key), nil
}

func insertLoopOutBRLNStrikeReturn(ctx context.Context, tx pgx.Tx, jobID, amountSat int64, automatic bool) error {
	_, err := tx.Exec(ctx, `
insert into loopout_brln_strike_returns (job_id,automatic,status,amount_sat,next_check_at)
values ($1,$2,'pending',$3,now())
on conflict (job_id) do nothing`, jobID, automatic, amountSat)
	return err
}

type loopOutBRLNStrikeReturnRow interface{ Scan(...any) error }

func scanLoopOutBRLNStrikeReturn(row loopOutBRLNStrikeReturnRow) (*LoopOutBRLNStrikeReturn, error) {
	var item LoopOutBRLNStrikeReturn
	err := row.Scan(&item.ID, &item.JobID, &item.Automatic, &item.Status, &item.AmountSat,
		&item.BTCAddress, &item.idempotencyKey, &item.QuoteID, &item.PaymentID, &item.TxID,
		&item.FeeSat, &item.EstimatedDeliveryMinutes, &item.LastError, &item.NextCheckAt,
		&item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *LoopOutBRLNService) getStrikeReturn(ctx context.Context, jobID int64) (*LoopOutBRLNStrikeReturn, error) {
	return scanLoopOutBRLNStrikeReturn(s.db.QueryRow(ctx, `
select id,job_id,automatic,status,amount_sat,btc_address,idempotency_key,quote_id,payment_id,txid,
 fee_sat,estimated_delivery_minutes,last_error,next_check_at,created_at,updated_at,completed_at
from loopout_brln_strike_returns where job_id=$1`, jobID))
}

func (s *LoopOutBRLNService) nextStrikeReturn(ctx context.Context) (*LoopOutBRLNStrikeReturn, error) {
	return scanLoopOutBRLNStrikeReturn(s.db.QueryRow(ctx, `
select id,job_id,automatic,status,amount_sat,btc_address,idempotency_key,quote_id,payment_id,txid,
 fee_sat,estimated_delivery_minutes,last_error,next_check_at,created_at,updated_at,completed_at
from loopout_brln_strike_returns
where status in ('pending','preparing','quoted','submitted','waiting_balance') and next_check_at <= now()
order by id asc limit 1`))
}

func (s *LoopOutBRLNService) RequestStrikeReturn(ctx context.Context, jobID int64) (*LoopOutBRLNStrikeReturn, error) {
	if _, err := s.newStrikeClient(); err != nil {
		return nil, err
	}
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if !isStrikeLightningAddress(job.LightningAddress) {
		return nil, errors.New("this loop did not use a @strike.me destination")
	}
	if job.Status != loopOutBRLNStatusCompleted && job.Status != loopOutBRLNStatusCancelled {
		return nil, errors.New("Strike return is available only after the loop has finished")
	}
	if job.SentSat <= 0 {
		return nil, errors.New("this loop has no successfully sent funds to return")
	}
	existing, err := s.getStrikeReturn(ctx, jobID)
	if err == nil {
		switch existing.Status {
		case strikeReturnFailed:
			if existing.PaymentID != "" || existing.QuoteID != "" {
				return nil, errors.New("the previous Strike return requires review before retrying")
			}
			_, err = s.db.Exec(ctx, `
update loopout_brln_strike_returns set status='pending',last_error='',next_check_at=now(),updated_at=now()
where id=$1`, existing.ID)
			if err != nil {
				return nil, err
			}
		case strikeReturnNeedsReview:
			return nil, errors.New("the Strike return has an ambiguous result and requires review")
		default:
			return existing, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	} else {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		if err := insertLoopOutBRLNStrikeReturn(ctx, tx, jobID, job.SentSat, false); err != nil {
			return nil, err
		}
		if err := insertLoopOutBRLNEvent(ctx, tx, jobID, "strike_return_queued", "info", "Manual Strike return queued", map[string]any{"amount_sat": job.SentSat}); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
	}
	s.signal()
	return s.getStrikeReturn(ctx, jobID)
}

func (s *LoopOutBRLNService) processStrikeReturnOnce(ctx context.Context) (bool, error) {
	item, err := s.nextStrikeReturn(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if item.Status == strikeReturnSubmitted {
		return true, s.reconcileStrikeReturn(ctx, item)
	}
	return true, s.prepareAndSubmitStrikeReturn(ctx, item)
}

func (s *LoopOutBRLNService) prepareAndSubmitStrikeReturn(ctx context.Context, item *LoopOutBRLNStrikeReturn) error {
	client, err := s.newStrikeClient()
	if err != nil {
		return s.deferStrikeReturn(ctx, item, strikeReturnWaitingBalance, err.Error(), time.Minute)
	}
	if item.BTCAddress == "" {
		addressCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		address, addressErr := s.lnd.NewAddress(addressCtx)
		cancel()
		if addressErr != nil {
			return s.deferStrikeReturn(ctx, item, strikeReturnPending, "failed to generate node address", time.Minute)
		}
		idempotencyKey, keyErr := newStrikeIdempotencyKey()
		if keyErr != nil {
			return keyErr
		}
		if _, err := s.db.Exec(ctx, `
update loopout_brln_strike_returns set status='preparing',btc_address=$2,idempotency_key=$3,last_error='',updated_at=now()
where id=$1`, item.ID, address, idempotencyKey); err != nil {
			return err
		}
		item.BTCAddress = address
		item.idempotencyKey = idempotencyKey
		s.appendEvent(ctx, item.JobID, "strike_return_preparing", "info", "Node address generated for Strike return", map[string]any{"amount_sat": item.AmountSat})
	}

	requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	balances, err := client.balances(requestCtx)
	cancel()
	if err != nil {
		return s.deferStrikeReturn(ctx, item, strikeReturnPreparing, "failed to read available Strike balance", time.Minute)
	}
	availableSat := int64(0)
	for _, balance := range balances {
		if strings.EqualFold(balance.Currency, "BTC") {
			availableSat, err = strikeBTCToSats(balance.Available)
			if err != nil {
				return s.failStrikeReturn(ctx, item, "Strike returned an invalid BTC balance", false)
			}
			break
		}
	}
	if availableSat < item.AmountSat {
		return s.deferStrikeReturn(ctx, item, strikeReturnWaitingBalance,
			fmt.Sprintf("available Strike BTC balance is %d sat; waiting for %d sat", availableSat, item.AmountSat), time.Minute)
	}

	requestCtx, cancel = context.WithTimeout(ctx, 25*time.Second)
	tiers, err := client.tiers(requestCtx, item.BTCAddress, item.AmountSat)
	cancel()
	if err != nil {
		return s.deferStrikeReturn(ctx, item, strikeReturnPreparing, "failed to retrieve Strike on-chain tiers", time.Minute)
	}
	var freeTier *loopOutBRLNStrikeTier
	for i := range tiers {
		if tiers[i].ID == "tier_free" {
			freeTier = &tiers[i]
			break
		}
	}
	if freeTier == nil {
		return s.failStrikeReturn(ctx, item, "Strike free on-chain tier is unavailable for this transfer", false)
	}
	estimatedFeeSat, err := strikeBTCToSats(freeTier.EstimatedFee.Amount)
	if err != nil || !strings.EqualFold(freeTier.EstimatedFee.Currency, "BTC") || estimatedFeeSat != 0 {
		return s.failStrikeReturn(ctx, item, "Strike free tier did not return a zero BTC fee", false)
	}

	requestCtx, cancel = context.WithTimeout(ctx, 25*time.Second)
	quote, err := client.createQuote(requestCtx, item.BTCAddress, item.AmountSat, freeTier.ID, item.idempotencyKey)
	cancel()
	if apiErr := new(loopOutBRLNStrikeAPIError); errors.As(err, &apiErr) && apiErr.Code == "DUPLICATE_PAYMENT_QUOTE" {
		quote.PaymentQuoteID, _ = apiErr.Values["paymentQuoteId"].(string)
		err = nil
	}
	if err != nil {
		if _, ok := err.(*loopOutBRLNStrikeAPIError); ok {
			return s.failStrikeReturn(ctx, item, "Strike rejected the free on-chain quote", false)
		}
		return s.deferStrikeReturn(ctx, item, strikeReturnPreparing, "failed to create Strike on-chain quote", time.Minute)
	}
	if quote.PaymentQuoteID == "" {
		return s.failStrikeReturn(ctx, item, "Strike quote did not include an identifier", false)
	}
	feeSat := int64(0)
	if quote.TotalFee != nil {
		feeSat, err = strikeBTCToSats(quote.TotalFee.Amount)
		if err != nil || !strings.EqualFold(quote.TotalFee.Currency, "BTC") {
			return s.failStrikeReturn(ctx, item, "Strike quote returned an invalid fee", false)
		}
	}
	if feeSat != 0 {
		return s.failStrikeReturn(ctx, item, "Strike quote is no longer free; payment was not executed", false)
	}
	totalAmountSat, err := strikeBTCToSats(quote.TotalAmount.Amount)
	if err != nil || !strings.EqualFold(quote.TotalAmount.Currency, "BTC") || totalAmountSat != item.AmountSat {
		return s.failStrikeReturn(ctx, item, "Strike quote changed the authorized total; payment was not executed", false)
	}
	estimatedMinutes := quote.EstimatedDeliveryDurationIn
	if estimatedMinutes <= 0 {
		estimatedMinutes = freeTier.EstimatedDeliveryDurationIn
	}
	if _, err := s.db.Exec(ctx, `
update loopout_brln_strike_returns set status='quoted',quote_id=$2,fee_sat=0,estimated_delivery_minutes=$3,last_error='',updated_at=now()
where id=$1`, item.ID, quote.PaymentQuoteID, estimatedMinutes); err != nil {
		return err
	}
	item.QuoteID = quote.PaymentQuoteID
	item.EstimatedDeliveryMinutes = estimatedMinutes
	s.appendEvent(ctx, item.JobID, "strike_return_quoted", "info", "Strike free on-chain quote created", map[string]any{
		"amount_sat": item.AmountSat, "fee_sat": 0, "estimated_delivery_minutes": estimatedMinutes,
	})

	requestCtx, cancel = context.WithTimeout(ctx, 25*time.Second)
	payment, err := client.executeQuote(requestCtx, item.QuoteID)
	cancel()
	if apiErr := new(loopOutBRLNStrikeAPIError); errors.As(err, &apiErr) && apiErr.Code == "PAYMENT_PROCESSED" {
		payment.PaymentID, _ = apiErr.Values["paymentId"].(string)
		payment.State = "PENDING"
		err = nil
	}
	if err != nil {
		if _, ok := err.(*loopOutBRLNStrikeAPIError); !ok {
			return s.failStrikeReturn(ctx, item, "Strike execution result is ambiguous; no retry was attempted", true)
		}
		return s.failStrikeReturn(ctx, item, "Strike rejected the on-chain payment", false)
	}
	if payment.PaymentID == "" {
		return s.failStrikeReturn(ctx, item, "Strike execution result did not include a payment ID", true)
	}
	return s.recordStrikePaymentState(ctx, item, payment)
}

func (s *LoopOutBRLNService) reconcileStrikeReturn(ctx context.Context, item *LoopOutBRLNStrikeReturn) error {
	if item.PaymentID == "" {
		return s.failStrikeReturn(ctx, item, "Strike payment ID is missing", true)
	}
	client, err := s.newStrikeClient()
	if err != nil {
		return s.deferStrikeReturn(ctx, item, strikeReturnSubmitted, err.Error(), 5*time.Minute)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	payment, err := client.payment(requestCtx, item.PaymentID)
	cancel()
	if err != nil {
		return s.deferStrikeReturn(ctx, item, strikeReturnSubmitted, "failed to refresh Strike payment status", 5*time.Minute)
	}
	return s.recordStrikePaymentState(ctx, item, payment)
}

func (s *LoopOutBRLNService) recordStrikePaymentState(ctx context.Context, item *LoopOutBRLNStrikeReturn, payment loopOutBRLNStrikePayment) error {
	switch strings.ToUpper(payment.State) {
	case "COMPLETED":
		txid := ""
		if payment.Onchain != nil {
			txid = strings.TrimSpace(payment.Onchain.TxID)
		}
		_, err := s.db.Exec(ctx, `
update loopout_brln_strike_returns set status='completed',payment_id=$2,txid=$3,last_error='',
 completed_at=now(),updated_at=now(),next_check_at=now() where id=$1`, item.ID, payment.PaymentID, txid)
		if err == nil {
			s.appendEvent(ctx, item.JobID, "strike_return_completed", "success", "Strike on-chain return completed", map[string]any{
				"amount_sat": item.AmountSat, "fee_sat": 0, "txid": txid,
			})
		}
		return err
	case "FAILED":
		return s.failStrikeReturn(ctx, item, "Strike on-chain payment failed", false)
	default:
		_, err := s.db.Exec(ctx, `
update loopout_brln_strike_returns set status='submitted',payment_id=$2,last_error='',next_check_at=now()+interval '5 minutes',updated_at=now()
where id=$1`, item.ID, payment.PaymentID)
		if err == nil && item.PaymentID == "" {
			s.appendEvent(ctx, item.JobID, "strike_return_submitted", "info", "Strike free on-chain payment submitted", map[string]any{
				"amount_sat": item.AmountSat, "fee_sat": 0, "estimated_delivery_minutes": item.EstimatedDeliveryMinutes,
			})
		}
		return err
	}
}

func (s *LoopOutBRLNService) deferStrikeReturn(ctx context.Context, item *LoopOutBRLNStrikeReturn, status, reason string, delay time.Duration) error {
	_, err := s.db.Exec(ctx, `
update loopout_brln_strike_returns set status=$2,last_error=$3,next_check_at=now()+$4::interval,updated_at=now()
where id=$1`, item.ID, status, reason, fmt.Sprintf("%d seconds", int(delay/time.Second)))
	return err
}

func (s *LoopOutBRLNService) failStrikeReturn(ctx context.Context, item *LoopOutBRLNStrikeReturn, reason string, needsReview bool) error {
	status := strikeReturnFailed
	eventKind := "strike_return_failed"
	if needsReview {
		status = strikeReturnNeedsReview
		eventKind = "strike_return_needs_review"
	}
	_, err := s.db.Exec(ctx, `
update loopout_brln_strike_returns set status=$2,last_error=$3,updated_at=now() where id=$1`,
		item.ID, status, reason)
	if err == nil {
		s.appendEvent(ctx, item.JobID, eventKind, "error", reason, nil)
	}
	return err
}
