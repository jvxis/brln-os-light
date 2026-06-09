package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	depixProviderCreatePath = "/v1/dep_split_lock"
	depixProviderStatusPath = "/v1/deposits/%s"

	depixDefaultPurpose        = "Buy DePix"
	depixTerminalRecheckWindow = 2 * time.Hour
)

var (
	depixUserKeyPattern       = regexp.MustCompile(`^[a-zA-Z0-9_-]{12,80}$`)
	depixLiquidAddressPattern = regexp.MustCompile(`^(lq1|ex1|tlq1|tex1)[ac-hj-np-z02-9]{20,120}$`)
)

type DepixService struct {
	db     *pgxpool.Pool
	logger *log.Logger
	client *http.Client
}

type depixConfig struct {
	Enabled             bool   `json:"enabled"`
	Timezone            string `json:"timezone"`
	MinAmountCents      int64  `json:"min_amount_cents"`
	MaxAmountCents      int64  `json:"max_amount_cents"`
	DailyLimitCents     int64  `json:"daily_limit_cents"`
	DailyUsedCents      int64  `json:"daily_used_cents"`
	DailyRemainingCents int64  `json:"daily_remaining_cents"`
	EulenFeeCents       int64  `json:"eulen_fee_cents"`
	BrlnFeeBPS          int64  `json:"brln_fee_bps"`
	BrlnFeePercent      string `json:"brln_fee_percent"`
}

type depixCreateRequest struct {
	UserKey       string `json:"user_key"`
	Timezone      string `json:"timezone"`
	LiquidAddress string `json:"liquid_address"`
	AmountBRL     string `json:"amount_brl"`
}

type depixOrder struct {
	ID                int64      `json:"id"`
	UserKey           string     `json:"user_key"`
	Timezone          string     `json:"timezone"`
	DepositID         string     `json:"deposit_id"`
	LiquidAddress     string     `json:"liquid_address"`
	Purpose           string     `json:"purpose"`
	GrossCents        int64      `json:"gross_cents"`
	EulenFeeCents     int64      `json:"eulen_fee_cents"`
	BrlnFeeBPS        int64      `json:"brln_fee_bps"`
	BrlnFeeCents      int64      `json:"brln_fee_cents"`
	NetCents          int64      `json:"net_cents"`
	Status            string     `json:"status"`
	TimeoutSeconds    int64      `json:"timeout_seconds"`
	RemainingSeconds  int64      `json:"remaining_seconds"`
	QRCopy            string     `json:"qr_copy"`
	QRImageURL        string     `json:"qr_image_url"`
	CheckoutURL       string     `json:"checkout_url"`
	ProviderStatusURL string     `json:"provider_status_url"`
	PayerName         string     `json:"payer_name,omitempty"`
	PayerTaxNumber    string     `json:"payer_tax_number,omitempty"`
	BankTxID          string     `json:"bank_tx_id,omitempty"`
	BlockchainTxID    string     `json:"blockchain_txid,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	ConfirmedAt       *time.Time `json:"confirmed_at,omitempty"`
}

type depixProviderCreateResponse struct {
	DepositID                string `json:"deposit_id"`
	AmountCents              int64  `json:"amount_cents"`
	Purpose                  string `json:"purpose"`
	QRCopyPaste              string `json:"qrCopyPaste"`
	QRImageURL               string `json:"qrImageUrl"`
	TimeoutSeconds           int64  `json:"timeout_seconds"`
	CheckoutURL              string `json:"checkout_url"`
	StatusURL                string `json:"status_url"`
	SplitFee                 string `json:"splitFee"`
	SplitAmountCentsEstimate int64  `json:"splitAmountCentsEstimate"`
	DepixSplitAddress        string `json:"depixSplitAddress"`
	ServicePercent           string `json:"servicePercent"`
	LockMode                 bool   `json:"lockMode"`
	Error                    string `json:"error"`
}

type depixProviderStatusResponse struct {
	DepositID        string `json:"deposit_id"`
	Status           string `json:"status"`
	RemainingSeconds int64  `json:"remaining_seconds"`
	PayerName        string `json:"payerName"`
	PayerTaxNumber   string `json:"payerTaxNumber"`
	BankTxID         string `json:"bankTxId"`
	BlockchainTxID   string `json:"blockchainTxID"`
	Error            string `json:"error"`
}

func NewDepixService(db *pgxpool.Pool, logger *log.Logger) *DepixService {
	return &DepixService{
		db:     db,
		logger: logger,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *DepixService) EnsureSchema(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	defaults := depixDefaultSettings()
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `
create table if not exists depix_settings (
  id integer primary key,
  app_installed boolean not null default false,
  app_enabled boolean not null default false,
  provider_base_url text not null default '',
  provider_api_key text not null default '',
  default_timezone text not null default 'America/Sao_Paulo',
  min_amount_cents bigint not null default 10000,
  max_amount_cents bigint not null default 50000,
  daily_limit_cents bigint not null default 300000,
  eulen_fee_cents bigint not null default 99,
  brln_fee_bps bigint not null default 150,
  updated_at timestamptz not null default now()
);`); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
alter table depix_settings
  alter column max_amount_cents set default 50000,
  alter column daily_limit_cents set default 300000;
`); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
insert into depix_settings (
  id, app_installed, app_enabled, provider_base_url, provider_api_key, default_timezone,
  min_amount_cents, max_amount_cents, daily_limit_cents, eulen_fee_cents, brln_fee_bps, updated_at
)
values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now())
on conflict (id) do nothing;
`, depixSettingsID, defaults.AppInstalled, defaults.AppEnabled, defaults.ProviderBaseURL, defaults.ProviderAPIKey, defaults.DefaultTimezone, defaults.MinAmountCents, defaults.MaxAmountCents, defaults.DailyLimitCents, defaults.EulenFeeCents, defaults.BrlnFeeBPS); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
update depix_settings
set
  provider_base_url = coalesce(nullif(provider_base_url, ''), $2),
  provider_api_key = coalesce(nullif(provider_api_key, ''), $3),
  default_timezone = coalesce(nullif(default_timezone, ''), $4),
  min_amount_cents = case when min_amount_cents <= 0 then $5 else min_amount_cents end,
  max_amount_cents = case when max_amount_cents <= 0 or max_amount_cents > $6 then $6 else max_amount_cents end,
  daily_limit_cents = case when daily_limit_cents <= 0 or daily_limit_cents > $7 then $7 else daily_limit_cents end,
  eulen_fee_cents = case when eulen_fee_cents < 0 then $8 else eulen_fee_cents end,
  brln_fee_bps = case when brln_fee_bps <= 0 then $9 else brln_fee_bps end
where id = $1;
`, depixSettingsID, defaults.ProviderBaseURL, defaults.ProviderAPIKey, defaults.DefaultTimezone, defaults.MinAmountCents, defaults.MaxAmountCents, defaults.DailyLimitCents, defaults.EulenFeeCents, defaults.BrlnFeeBPS); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
create table if not exists depix_orders (
  id bigserial primary key,
  user_key text not null,
  timezone text not null,
  deposit_id text unique not null,
  liquid_address text not null,
  purpose text not null,
  gross_cents bigint not null,
  eulen_fee_cents bigint not null,
  brln_fee_bps bigint not null,
  brln_fee_cents bigint not null,
  net_cents bigint not null,
  status text not null,
  timeout_seconds bigint not null default 0,
  remaining_seconds bigint not null default 0,
  qr_copy text not null default '',
  qr_image_url text not null default '',
  checkout_url text not null default '',
  provider_status_url text not null default '',
  payer_name text not null default '',
  payer_tax_number text not null default '',
  bank_tx_id text not null default '',
  blockchain_txid text not null default '',
  error_message text not null default '',
  provider_payload_json text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  last_checked_at timestamptz,
  confirmed_at timestamptz
);`); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `create index if not exists depix_orders_user_created_idx on depix_orders (user_key, created_at desc);`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `create index if not exists depix_orders_status_idx on depix_orders (status, created_at desc);`); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *DepixService) Config(ctx context.Context, userKeyRaw, timezoneRaw string) (depixConfig, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return depixConfig{}, err
	}
	loc, tz := depixResolveTimezone(timezoneRaw, settings.DefaultTimezone)
	cfg := depixConfig{
		Enabled:             settings.AppInstalled && settings.AppEnabled,
		Timezone:            tz,
		MinAmountCents:      settings.MinAmountCents,
		MaxAmountCents:      settings.MaxAmountCents,
		DailyLimitCents:     settings.DailyLimitCents,
		EulenFeeCents:       settings.EulenFeeCents,
		BrlnFeeBPS:          settings.BrlnFeeBPS,
		BrlnFeePercent:      depixFeePercentText(settings.BrlnFeeBPS),
		DailyUsedCents:      0,
		DailyRemainingCents: settings.DailyLimitCents,
	}
	if strings.TrimSpace(userKeyRaw) == "" || s.db == nil {
		return cfg, nil
	}
	userKey, err := depixNormalizeUserKey(userKeyRaw)
	if err != nil {
		return cfg, err
	}
	now := time.Now()
	used, err := s.dailyUsedCents(ctx, userKey, loc, now)
	if err != nil {
		return cfg, err
	}
	cfg.DailyUsedCents = used
	if used >= settings.DailyLimitCents {
		cfg.DailyRemainingCents = 0
	} else {
		cfg.DailyRemainingCents = settings.DailyLimitCents - used
	}
	return cfg, nil
}

func (s *DepixService) AppState(ctx context.Context) (bool, bool, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return false, false, err
	}
	return settings.AppInstalled, settings.AppEnabled, nil
}

func (s *DepixService) SetAppInstalled(ctx context.Context, installed bool, enabled bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if !installed {
		enabled = false
	}
	_, err := s.db.Exec(ctx, `
update depix_settings
set app_installed=$2, app_enabled=$3, updated_at=now()
where id=$1
`, depixSettingsID, installed, enabled)
	return err
}

func (s *DepixService) SetAppEnabled(ctx context.Context, enabled bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	_, err := s.db.Exec(ctx, `
update depix_settings
set app_enabled=$2, updated_at=now()
where id=$1 and app_installed=true
`, depixSettingsID, enabled)
	return err
}

func (s *DepixService) CreateOrder(ctx context.Context, req depixCreateRequest) (depixOrder, error) {
	if s.db == nil {
		return depixOrder{}, errors.New("depix unavailable: db not configured")
	}
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return depixOrder{}, err
	}
	if !settings.AppInstalled || !settings.AppEnabled {
		return depixOrder{}, errors.New("Buy DePix is disabled")
	}
	userKey, err := depixNormalizeUserKey(req.UserKey)
	if err != nil {
		return depixOrder{}, err
	}
	amountCents, err := depixParseBRLToCents(req.AmountBRL)
	if err != nil {
		return depixOrder{}, err
	}
	if amountCents < settings.MinAmountCents || amountCents > settings.MaxAmountCents {
		return depixOrder{}, fmt.Errorf("amount must be between %.2f and %.2f BRL", depixCentsToBRL(settings.MinAmountCents), depixCentsToBRL(settings.MaxAmountCents))
	}
	liquidAddress, err := depixNormalizeLiquidAddress(req.LiquidAddress)
	if err != nil {
		return depixOrder{}, err
	}
	loc, timezone := depixResolveTimezone(req.Timezone, settings.DefaultTimezone)
	baseURL := strings.TrimRight(strings.TrimSpace(settings.ProviderBaseURL), "/")
	apiKey := strings.TrimSpace(settings.ProviderAPIKey)
	if strings.TrimSpace(apiKey) == "" {
		return depixOrder{}, errors.New("depix provider API key is not configured")
	}

	eulenFee := settings.EulenFeeCents
	brlnFee := depixBrlnFeeCents(amountCents, settings.BrlnFeeBPS)
	netCents := amountCents - eulenFee - brlnFee
	if netCents <= 0 {
		return depixOrder{}, errors.New("net amount must be positive")
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return depixOrder{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, userKey); err != nil {
		return depixOrder{}, err
	}
	now := time.Now()
	usedToday, err := s.dailyUsedCentsTx(ctx, tx, userKey, loc, now)
	if err != nil {
		return depixOrder{}, err
	}
	if usedToday+amountCents > settings.DailyLimitCents {
		return depixOrder{}, fmt.Errorf("daily limit exceeded: available %.2f BRL", depixCentsToBRL(maxInt64(0, settings.DailyLimitCents-usedToday)))
	}

	providerResp, providerRaw, err := s.createProviderLockedDeposit(ctx, baseURL, apiKey, userKey, timezone, liquidAddress, amountCents)
	if err != nil {
		return depixOrder{}, err
	}
	if strings.TrimSpace(providerResp.DepositID) == "" {
		return depixOrder{}, errors.New("depix provider did not return deposit_id")
	}

	order := depixOrder{
		UserKey:           userKey,
		Timezone:          timezone,
		DepositID:         strings.TrimSpace(providerResp.DepositID),
		LiquidAddress:     liquidAddress,
		Purpose:           depixDefaultPurpose,
		GrossCents:        amountCents,
		EulenFeeCents:     eulenFee,
		BrlnFeeBPS:        settings.BrlnFeeBPS,
		BrlnFeeCents:      brlnFee,
		NetCents:          netCents,
		Status:            "pending",
		TimeoutSeconds:    providerResp.TimeoutSeconds,
		RemainingSeconds:  providerResp.TimeoutSeconds,
		QRCopy:            providerResp.QRCopyPaste,
		QRImageURL:        providerResp.QRImageURL,
		CheckoutURL:       providerResp.CheckoutURL,
		ProviderStatusURL: providerResp.StatusURL,
		CreatedAt:         now.UTC(),
		UpdatedAt:         now.UTC(),
	}

	row := tx.QueryRow(ctx, `
insert into depix_orders (
  user_key, timezone, deposit_id, liquid_address, purpose,
  gross_cents, eulen_fee_cents, brln_fee_bps, brln_fee_cents, net_cents,
  status, timeout_seconds, remaining_seconds,
  qr_copy, qr_image_url, checkout_url, provider_status_url,
  provider_payload_json, created_at, updated_at
) values (
  $1,$2,$3,$4,$5,
  $6,$7,$8,$9,$10,
  $11,$12,$13,
  $14,$15,$16,$17,
  $18, now(), now()
)
returning id, created_at, updated_at
`, order.UserKey, order.Timezone, order.DepositID, order.LiquidAddress, order.Purpose,
		order.GrossCents, order.EulenFeeCents, order.BrlnFeeBPS, order.BrlnFeeCents, order.NetCents,
		order.Status, order.TimeoutSeconds, order.RemainingSeconds,
		order.QRCopy, order.QRImageURL, order.CheckoutURL, order.ProviderStatusURL,
		providerRaw,
	)
	if err := row.Scan(&order.ID, &order.CreatedAt, &order.UpdatedAt); err != nil {
		return depixOrder{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return depixOrder{}, err
	}
	return order, nil
}

func (s *DepixService) ListOrders(ctx context.Context, userKeyRaw string, limit int) ([]depixOrder, error) {
	if s.db == nil {
		return nil, errors.New("depix unavailable: db not configured")
	}
	userKey, err := depixNormalizeUserKey(userKeyRaw)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
select
  id, user_key, timezone, deposit_id, liquid_address, purpose,
  gross_cents, eulen_fee_cents, brln_fee_bps, brln_fee_cents, net_cents,
  status, timeout_seconds, remaining_seconds,
  qr_copy, qr_image_url, checkout_url, provider_status_url,
  payer_name, payer_tax_number, bank_tx_id, blockchain_txid, error_message,
  created_at, updated_at, last_checked_at, confirmed_at
from depix_orders
where user_key=$1
order by created_at desc
limit $2
`, userKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]depixOrder, 0, limit)
	for rows.Next() {
		item, scanErr := scanDepixOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *DepixService) GetOrder(ctx context.Context, userKeyRaw string, id int64, refresh bool) (depixOrder, error) {
	if s.db == nil {
		return depixOrder{}, errors.New("depix unavailable: db not configured")
	}
	userKey, err := depixNormalizeUserKey(userKeyRaw)
	if err != nil {
		return depixOrder{}, err
	}
	order, err := s.getOrderByID(ctx, userKey, id)
	if err != nil {
		return depixOrder{}, err
	}
	if refresh && depixShouldRefreshStatus(order, time.Now()) {
		updated, refreshErr := s.refreshOrderStatus(ctx, order)
		if refreshErr == nil {
			order = updated
		} else if s.logger != nil {
			s.logger.Printf("depix: refresh order %d failed: %v", order.ID, refreshErr)
		}
	}
	return order, nil
}

func (s *DepixService) getOrderByID(ctx context.Context, userKey string, id int64) (depixOrder, error) {
	row := s.db.QueryRow(ctx, `
select
  id, user_key, timezone, deposit_id, liquid_address, purpose,
  gross_cents, eulen_fee_cents, brln_fee_bps, brln_fee_cents, net_cents,
  status, timeout_seconds, remaining_seconds,
  qr_copy, qr_image_url, checkout_url, provider_status_url,
  payer_name, payer_tax_number, bank_tx_id, blockchain_txid, error_message,
  created_at, updated_at, last_checked_at, confirmed_at
from depix_orders
where id=$1 and user_key=$2
`, id, userKey)
	item, err := scanDepixOrder(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return depixOrder{}, errors.New("order not found")
		}
		return depixOrder{}, err
	}
	return item, nil
}

func (s *DepixService) refreshOrderStatus(ctx context.Context, order depixOrder) (depixOrder, error) {
	settings, err := s.loadSettings(ctx)
	if err != nil {
		return order, err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(settings.ProviderBaseURL), "/")
	apiKey := strings.TrimSpace(settings.ProviderAPIKey)
	if strings.TrimSpace(apiKey) == "" {
		return order, errors.New("depix provider API key is not configured")
	}
	statusResp, raw, err := s.fetchProviderStatus(ctx, baseURL, apiKey, order.DepositID)
	if err != nil {
		return order, err
	}
	nextStatus := strings.TrimSpace(strings.ToLower(statusResp.Status))
	if nextStatus == "" {
		nextStatus = order.Status
	}
	row := s.db.QueryRow(ctx, `
update depix_orders
set
  status=$2,
  remaining_seconds=$3,
  payer_name=$4,
  payer_tax_number=$5,
  bank_tx_id=$6,
  blockchain_txid=$7,
  error_message=$8,
  provider_payload_json=$9,
  updated_at=now(),
  last_checked_at=now(),
  confirmed_at=case when $2='depix_sent' and confirmed_at is null then now() else confirmed_at end
where id=$1 and user_key=$10
returning
  id, user_key, timezone, deposit_id, liquid_address, purpose,
  gross_cents, eulen_fee_cents, brln_fee_bps, brln_fee_cents, net_cents,
  status, timeout_seconds, remaining_seconds,
  qr_copy, qr_image_url, checkout_url, provider_status_url,
  payer_name, payer_tax_number, bank_tx_id, blockchain_txid, error_message,
  created_at, updated_at, last_checked_at, confirmed_at
`, order.ID, nextStatus, statusResp.RemainingSeconds, statusResp.PayerName, statusResp.PayerTaxNumber, statusResp.BankTxID, statusResp.BlockchainTxID, statusResp.Error, raw, order.UserKey)
	updated, scanErr := scanDepixOrder(row)
	if scanErr != nil {
		return depixOrder{}, scanErr
	}
	return updated, nil
}

func (s *DepixService) dailyUsedCents(ctx context.Context, userKey string, loc *time.Location, now time.Time) (int64, error) {
	startUTC, endUTC := depixDayRange(now, loc)
	var used int64
	err := s.db.QueryRow(ctx, `
select coalesce(sum(gross_cents), 0)
from depix_orders
where user_key=$1 and created_at >= $2 and created_at < $3
`, userKey, startUTC, endUTC).Scan(&used)
	return used, err
}

func (s *DepixService) dailyUsedCentsTx(ctx context.Context, tx pgx.Tx, userKey string, loc *time.Location, now time.Time) (int64, error) {
	startUTC, endUTC := depixDayRange(now, loc)
	var used int64
	err := tx.QueryRow(ctx, `
select coalesce(sum(gross_cents), 0)
from depix_orders
where user_key=$1 and created_at >= $2 and created_at < $3
`, userKey, startUTC, endUTC).Scan(&used)
	return used, err
}

func (s *DepixService) createProviderLockedDeposit(ctx context.Context, baseURL, apiKey, userKey, timezone, liquidAddress string, amountCents int64) (depixProviderCreateResponse, string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + depixProviderCreatePath
	payload := map[string]any{
		"amount_cents":  amountCents,
		"depix_address": liquidAddress,
		"purpose":       depixDefaultPurpose,
		"metadata": map[string]any{
			"source":   "lightningos-light",
			"user_key": userKey,
			"timezone": timezone,
		},
	}
	status, body, err := s.doProviderJSON(ctx, http.MethodPost, endpoint, apiKey, payload)
	if err != nil {
		return depixProviderCreateResponse{}, "", err
	}
	var out depixProviderCreateResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return depixProviderCreateResponse{}, "", fmt.Errorf("invalid provider response: %w", err)
	}
	if status != http.StatusOK {
		msg := strings.TrimSpace(out.Error)
		if msg == "" {
			msg = fmt.Sprintf("provider create failed with status %d", status)
		}
		return depixProviderCreateResponse{}, "", errors.New(msg)
	}
	if strings.TrimSpace(out.DepositID) == "" {
		return depixProviderCreateResponse{}, "", errors.New("provider create response missing deposit_id")
	}
	return out, string(body), nil
}

func (s *DepixService) fetchProviderStatus(ctx context.Context, baseURL, apiKey, depositID string) (depixProviderStatusResponse, string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + fmt.Sprintf(depixProviderStatusPath, depositID)
	status, body, err := s.doProviderJSON(ctx, http.MethodGet, endpoint, apiKey, nil)
	if err != nil {
		return depixProviderStatusResponse{}, "", err
	}
	var out depixProviderStatusResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return depixProviderStatusResponse{}, "", fmt.Errorf("invalid provider status response: %w", err)
	}
	if status == http.StatusNotFound {
		out.Status = "not_found"
		return out, string(body), nil
	}
	if status != http.StatusOK {
		msg := strings.TrimSpace(out.Error)
		if msg == "" {
			msg = fmt.Sprintf("provider status failed with status %d", status)
		}
		return depixProviderStatusResponse{}, "", errors.New(msg)
	}
	return out, string(body), nil
}

func (s *DepixService) doProviderJSON(ctx context.Context, method, endpoint, apiKey string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, raw, nil
}

func (s *DepixService) loadSettings(ctx context.Context) (depixSettings, error) {
	settings := depixDefaultSettings()
	if s.db == nil {
		return settings, errors.New("db unavailable")
	}
	err := s.db.QueryRow(ctx, `
select
  app_installed, app_enabled, provider_base_url, provider_api_key, default_timezone,
  min_amount_cents, max_amount_cents, daily_limit_cents, eulen_fee_cents, brln_fee_bps
from depix_settings
where id=$1
`, depixSettingsID).Scan(
		&settings.AppInstalled,
		&settings.AppEnabled,
		&settings.ProviderBaseURL,
		&settings.ProviderAPIKey,
		&settings.DefaultTimezone,
		&settings.MinAmountCents,
		&settings.MaxAmountCents,
		&settings.DailyLimitCents,
		&settings.EulenFeeCents,
		&settings.BrlnFeeBPS,
	)
	if err != nil {
		return settings, err
	}
	if strings.TrimSpace(settings.ProviderBaseURL) == "" {
		settings.ProviderBaseURL = depixProviderBaseDefault
	}
	if strings.TrimSpace(settings.ProviderAPIKey) == "" {
		settings.ProviderAPIKey = depixProviderAPIKeyDefault
	}
	if strings.TrimSpace(settings.DefaultTimezone) == "" {
		settings.DefaultTimezone = depixDefaultTimezone
	}
	if settings.MinAmountCents <= 0 {
		settings.MinAmountCents = depixDefaultMinAmountCents
	}
	if settings.MaxAmountCents <= 0 || settings.MaxAmountCents > depixDefaultMaxAmountCents {
		settings.MaxAmountCents = depixDefaultMaxAmountCents
	}
	if settings.DailyLimitCents <= 0 || settings.DailyLimitCents > depixDefaultDailyLimitCents {
		settings.DailyLimitCents = depixDefaultDailyLimitCents
	}
	if settings.EulenFeeCents < 0 {
		settings.EulenFeeCents = depixDefaultEulenFeeCents
	}
	if settings.BrlnFeeBPS <= 0 {
		settings.BrlnFeeBPS = depixDefaultBrlnFeeBPS
	}
	return settings, nil
}

func depixResolveTimezone(raw string, fallback string) (*time.Location, string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		if strings.TrimSpace(fallback) != "" {
			value = strings.TrimSpace(fallback)
		}
	}
	if value == "" {
		value = depixDefaultTimezone
	}
	loc, err := time.LoadLocation(value)
	if err != nil {
		loc = time.FixedZone(depixDefaultTimezone, -3*60*60)
		value = depixDefaultTimezone
	}
	return loc, value
}

func depixNormalizeUserKey(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("user_key required")
	}
	if !depixUserKeyPattern.MatchString(value) {
		return "", errors.New("invalid user_key")
	}
	return value, nil
}

func depixNormalizeLiquidAddress(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "", errors.New("liquid_address required")
	}
	if !depixLiquidAddressPattern.MatchString(value) {
		return "", errors.New("invalid liquid address")
	}
	return value, nil
}

func depixParseBRLToCents(raw string) (int64, error) {
	value := strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	if value == "" {
		return 0, errors.New("amount_brl required")
	}
	if strings.HasPrefix(value, ".") {
		value = "0" + value
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return 0, errors.New("invalid amount_brl")
	}
	intPart := parts[0]
	if intPart == "" {
		intPart = "0"
	}
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(intPart) {
		return 0, errors.New("invalid amount_brl")
	}
	fracPart := "00"
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) > 2 || !regexp.MustCompile(`^[0-9]+$`).MatchString(frac) {
			return 0, errors.New("invalid amount_brl")
		}
		if len(frac) == 1 {
			fracPart = frac + "0"
		} else {
			fracPart = frac
		}
	}
	intVal, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, errors.New("invalid amount_brl")
	}
	fracVal, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, errors.New("invalid amount_brl")
	}
	return intVal*100 + fracVal, nil
}

func depixBrlnFeeCents(grossCents int64, bps int64) int64 {
	// Round half-up for percentage fee.
	return (grossCents*bps + 5000) / 10000
}

func depixFeePercentText(bps int64) string {
	whole := bps / 100
	frac := bps % 100
	if frac == 0 {
		return strconv.FormatInt(whole, 10)
	}
	if frac%10 == 0 {
		return fmt.Sprintf("%d.%d", whole, frac/10)
	}
	return fmt.Sprintf("%d.%02d", whole, frac)
}

func depixDayRange(now time.Time, loc *time.Location) (time.Time, time.Time) {
	local := now.In(loc)
	startLocal := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return startLocal.UTC(), startLocal.Add(24 * time.Hour).UTC()
}

func depixStatusIsTerminal(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "depix_sent", "expired", "timeout", "canceled", "refunded", "error", "not_found":
		return true
	default:
		return false
	}
}

func depixShouldRefreshStatus(order depixOrder, now time.Time) bool {
	status := strings.TrimSpace(strings.ToLower(order.Status))
	if !depixStatusIsTerminal(status) {
		return true
	}
	switch status {
	case "timeout", "expired", "error", "not_found":
		baseWindow := depixTerminalRecheckWindow
		if order.TimeoutSeconds > 0 {
			baseWindow += time.Duration(order.TimeoutSeconds) * time.Second
		}
		if order.CreatedAt.IsZero() {
			return true
		}
		return now.Before(order.CreatedAt.Add(baseWindow))
	default:
		return false
	}
}

func depixCentsToBRL(cents int64) float64 {
	return float64(cents) / 100.0
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type depixOrderScanner interface {
	Scan(dest ...any) error
}

func scanDepixOrder(scanner depixOrderScanner) (depixOrder, error) {
	var out depixOrder
	var lastChecked pgtype.Timestamptz
	var confirmed pgtype.Timestamptz
	err := scanner.Scan(
		&out.ID,
		&out.UserKey,
		&out.Timezone,
		&out.DepositID,
		&out.LiquidAddress,
		&out.Purpose,
		&out.GrossCents,
		&out.EulenFeeCents,
		&out.BrlnFeeBPS,
		&out.BrlnFeeCents,
		&out.NetCents,
		&out.Status,
		&out.TimeoutSeconds,
		&out.RemainingSeconds,
		&out.QRCopy,
		&out.QRImageURL,
		&out.CheckoutURL,
		&out.ProviderStatusURL,
		&out.PayerName,
		&out.PayerTaxNumber,
		&out.BankTxID,
		&out.BlockchainTxID,
		&out.ErrorMessage,
		&out.CreatedAt,
		&out.UpdatedAt,
		&lastChecked,
		&confirmed,
	)
	if err != nil {
		return depixOrder{}, err
	}
	if lastChecked.Valid {
		t := lastChecked.Time.UTC()
		out.LastCheckedAt = &t
	}
	if confirmed.Valid {
		t := confirmed.Time.UTC()
		out.ConfirmedAt = &t
	}
	return out, nil
}
