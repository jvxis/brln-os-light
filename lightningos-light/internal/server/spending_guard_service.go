package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const spendingGuardWindow = 24 * time.Hour

var (
	ErrSpendingGuardUnavailable = errors.New("spending guard unavailable")
	ErrSpendingGuardLimit       = errors.New("spending guard limit exceeded")
)

type SpendingGuardSettings struct {
	Enabled            bool      `json:"enabled"`
	MaxPaymentSat      int64     `json:"max_payment_sat"`
	Rolling24hLimitSat int64     `json:"rolling_24h_limit_sat"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

type SpendingGuardStatus struct {
	SpendingGuardSettings
	UsedSat        int64     `json:"used_sat"`
	ReservedSat    int64     `json:"reserved_sat"`
	RemainingSat   int64     `json:"remaining_sat"`
	WindowStart    time.Time `json:"window_start"`
	WindowEndsHint time.Time `json:"window_ends_hint,omitempty"`
	Scope          []string  `json:"scope"`
	ExcludedScope  []string  `json:"excluded_scope"`
}

type SpendingIntent struct {
	Source      string
	AmountSat   int64
	MaxFeeSat   int64
	PaymentHash string
}

type SpendingReservation struct {
	ID          string
	Active      bool
	AmountSat   int64
	MaxFeeSat   int64
	ReservedSat int64
}

type SpendingLimitError struct {
	Reason       string `json:"reason"`
	RequestedSat int64  `json:"requested_sat"`
	UsedSat      int64  `json:"used_sat"`
	ReservedSat  int64  `json:"reserved_sat"`
	LimitSat     int64  `json:"limit_sat"`
	RemainingSat int64  `json:"remaining_sat"`
}

func (e *SpendingLimitError) Error() string {
	if e == nil {
		return ErrSpendingGuardLimit.Error()
	}
	switch e.Reason {
	case "per_payment":
		return fmt.Sprintf("payment debit of %d sats exceeds the per-payment limit of %d sats", e.RequestedSat, e.LimitSat)
	default:
		return fmt.Sprintf("payment debit of %d sats exceeds the remaining rolling 24-hour allowance of %d sats", e.RequestedSat, e.RemainingSat)
	}
}

func (e *SpendingLimitError) Unwrap() error { return ErrSpendingGuardLimit }

type lightningSpendingGuard interface {
	Reserve(context.Context, SpendingIntent) (SpendingReservation, error)
	Bind(context.Context, SpendingReservation, string) error
	Settle(context.Context, SpendingReservation, int64, string) error
	Release(context.Context, SpendingReservation, string) error
}

type pendingSpendingReservation struct {
	SpendingReservation
	PaymentHash string
	CreatedAt   time.Time
}

type spendingGuardLimitHandler func(context.Context, SpendingIntent, SpendingLimitError)

type SpendingGuardService struct {
	db             *pgxpool.Pool
	logger         *log.Logger
	limitHandlerMu sync.RWMutex
	limitHandler   spendingGuardLimitHandler
}

func NewSpendingGuardService(db *pgxpool.Pool, logger *log.Logger) *SpendingGuardService {
	return &SpendingGuardService{db: db, logger: logger}
}

func (s *SpendingGuardService) SetLimitHandler(handler spendingGuardLimitHandler) {
	if s == nil {
		return
	}
	s.limitHandlerMu.Lock()
	s.limitHandler = handler
	s.limitHandlerMu.Unlock()
}

func (s *SpendingGuardService) reportLimit(ctx context.Context, intent SpendingIntent, limitErr SpendingLimitError) {
	if s == nil {
		return
	}
	s.limitHandlerMu.RLock()
	handler := s.limitHandler
	s.limitHandlerMu.RUnlock()
	if handler != nil {
		handler(ctx, intent, limitErr)
	}
}

func (s *SpendingGuardService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrSpendingGuardUnavailable
	}
	_, err := s.db.Exec(ctx, `
create table if not exists spending_guard_settings (
  id smallint primary key check (id = 1),
  enabled boolean not null default false,
  max_payment_sat bigint not null default 0 check (max_payment_sat >= 0),
  rolling_24h_limit_sat bigint not null default 0 check (rolling_24h_limit_sat >= 0),
  updated_at timestamptz not null default now()
);
insert into spending_guard_settings (id) values (1) on conflict (id) do nothing;

create table if not exists spending_guard_entries (
  id text primary key,
  source text not null,
  payment_hash text not null default '',
  amount_sat bigint not null check (amount_sat > 0),
  max_fee_sat bigint not null default 0 check (max_fee_sat >= 0),
  reserved_sat bigint not null check (reserved_sat > 0),
  actual_fee_sat bigint not null default 0 check (actual_fee_sat >= 0),
  actual_debit_sat bigint not null default 0 check (actual_debit_sat >= 0),
  status text not null check (status in ('reserved','settled','released')),
  release_reason text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  settled_at timestamptz
);
create index if not exists spending_guard_entries_window_idx
  on spending_guard_entries (status, created_at desc);
create index if not exists spending_guard_entries_hash_idx
  on spending_guard_entries (payment_hash) where payment_hash <> '';
`)
	return err
}

func validateSpendingGuardSettings(settings SpendingGuardSettings) error {
	if settings.MaxPaymentSat < 0 || settings.Rolling24hLimitSat < 0 {
		return errors.New("spending limits must be zero or positive")
	}
	if settings.Enabled && settings.MaxPaymentSat == 0 && settings.Rolling24hLimitSat == 0 {
		return errors.New("at least one spending limit is required when the guard is enabled")
	}
	return nil
}

func (s *SpendingGuardService) Settings(ctx context.Context) (SpendingGuardSettings, error) {
	if s == nil || s.db == nil {
		return SpendingGuardSettings{}, ErrSpendingGuardUnavailable
	}
	var settings SpendingGuardSettings
	err := s.db.QueryRow(ctx, `select enabled,max_payment_sat,rolling_24h_limit_sat,updated_at from spending_guard_settings where id=1`).Scan(
		&settings.Enabled, &settings.MaxPaymentSat, &settings.Rolling24hLimitSat, &settings.UpdatedAt,
	)
	return settings, err
}

func (s *SpendingGuardService) UpdateSettings(ctx context.Context, settings SpendingGuardSettings) (SpendingGuardSettings, error) {
	if err := validateSpendingGuardSettings(settings); err != nil {
		return SpendingGuardSettings{}, err
	}
	if s == nil || s.db == nil {
		return SpendingGuardSettings{}, ErrSpendingGuardUnavailable
	}
	var updated SpendingGuardSettings
	err := s.db.QueryRow(ctx, `
update spending_guard_settings
set enabled=$1,max_payment_sat=$2,rolling_24h_limit_sat=$3,updated_at=now()
where id=1
returning enabled,max_payment_sat,rolling_24h_limit_sat,updated_at`,
		settings.Enabled, settings.MaxPaymentSat, settings.Rolling24hLimitSat,
	).Scan(&updated.Enabled, &updated.MaxPaymentSat, &updated.Rolling24hLimitSat, &updated.UpdatedAt)
	return updated, err
}

func (s *SpendingGuardService) Status(ctx context.Context) (SpendingGuardStatus, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return SpendingGuardStatus{}, err
	}
	windowStart := time.Now().UTC().Add(-spendingGuardWindow)
	used, reserved, oldest, err := s.windowTotals(ctx, nil, windowStart)
	if err != nil {
		return SpendingGuardStatus{}, err
	}
	remaining := int64(0)
	if settings.Rolling24hLimitSat > 0 {
		remaining = settings.Rolling24hLimitSat - used - reserved
		if remaining < 0 {
			remaining = 0
		}
	}
	status := SpendingGuardStatus{
		SpendingGuardSettings: settings,
		UsedSat:               used, ReservedSat: reserved, RemainingSat: remaining, WindowStart: windowStart,
		Scope:         []string{"wallet_payments", "chat_keysend", "loop_out_brln"},
		ExcludedScope: []string{"routing_forwards", "rebalances", "channel_operations", "direct_lnd_apps"},
	}
	if oldest != nil {
		status.WindowEndsHint = oldest.Add(spendingGuardWindow)
	}
	return status, nil
}

func (s *SpendingGuardService) Reserve(ctx context.Context, intent SpendingIntent) (SpendingReservation, error) {
	if intent.AmountSat <= 0 {
		return SpendingReservation{}, errors.New("payment amount must be positive")
	}
	if intent.MaxFeeSat < 0 {
		return SpendingReservation{}, errors.New("maximum fee must be zero or positive")
	}
	if s == nil || s.db == nil {
		return SpendingReservation{}, ErrSpendingGuardUnavailable
	}
	reservedSat, err := safeSpendingDebit(intent.AmountSat, intent.MaxFeeSat)
	if err != nil {
		return SpendingReservation{}, err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SpendingReservation{}, err
	}
	defer tx.Rollback(ctx)

	var settings SpendingGuardSettings
	err = tx.QueryRow(ctx, `select enabled,max_payment_sat,rolling_24h_limit_sat,updated_at from spending_guard_settings where id=1 for update`).Scan(
		&settings.Enabled, &settings.MaxPaymentSat, &settings.Rolling24hLimitSat, &settings.UpdatedAt,
	)
	if err != nil {
		return SpendingReservation{}, err
	}
	if !settings.Enabled {
		if err := tx.Commit(ctx); err != nil {
			return SpendingReservation{}, err
		}
		return SpendingReservation{AmountSat: intent.AmountSat, MaxFeeSat: intent.MaxFeeSat, ReservedSat: reservedSat}, nil
	}
	if settings.MaxPaymentSat > 0 && reservedSat > settings.MaxPaymentSat {
		limitErr := SpendingLimitError{Reason: "per_payment", RequestedSat: reservedSat, LimitSat: settings.MaxPaymentSat, RemainingSat: settings.MaxPaymentSat}
		s.reportLimit(ctx, intent, limitErr)
		return SpendingReservation{}, &limitErr
	}
	windowStart := time.Now().UTC().Add(-spendingGuardWindow)
	used, reserved, _, err := s.windowTotals(ctx, tx, windowStart)
	if err != nil {
		return SpendingReservation{}, err
	}
	if settings.Rolling24hLimitSat > 0 {
		remaining := settings.Rolling24hLimitSat - used - reserved
		if remaining < 0 {
			remaining = 0
		}
		if reservedSat > remaining {
			limitErr := SpendingLimitError{Reason: "rolling_24h", RequestedSat: reservedSat, UsedSat: used, ReservedSat: reserved, LimitSat: settings.Rolling24hLimitSat, RemainingSat: remaining}
			s.reportLimit(ctx, intent, limitErr)
			return SpendingReservation{}, &limitErr
		}
	}
	id, err := newSpendingReservationID()
	if err != nil {
		return SpendingReservation{}, err
	}
	_, err = tx.Exec(ctx, `
insert into spending_guard_entries (id,source,payment_hash,amount_sat,max_fee_sat,reserved_sat,status)
values ($1,$2,$3,$4,$5,$6,'reserved')`, id, normalizeSpendingSource(intent.Source), strings.ToLower(strings.TrimSpace(intent.PaymentHash)), intent.AmountSat, intent.MaxFeeSat, reservedSat)
	if err != nil {
		return SpendingReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SpendingReservation{}, err
	}
	return SpendingReservation{ID: id, Active: true, AmountSat: intent.AmountSat, MaxFeeSat: intent.MaxFeeSat, ReservedSat: reservedSat}, nil
}

func (s *SpendingGuardService) Settle(ctx context.Context, reservation SpendingReservation, actualFeeSat int64, paymentHash string) error {
	if !reservation.Active || strings.TrimSpace(reservation.ID) == "" {
		return nil
	}
	if actualFeeSat < 0 {
		actualFeeSat = 0
	}
	actualDebit, err := safeSpendingDebit(reservation.AmountSat, actualFeeSat)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, `
update spending_guard_entries
set status='settled',payment_hash=case when $2='' then payment_hash else $2 end,
 actual_fee_sat=$3,actual_debit_sat=$4,settled_at=now(),updated_at=now()
where id=$1 and status='reserved'`, reservation.ID, strings.ToLower(strings.TrimSpace(paymentHash)), actualFeeSat, actualDebit)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return errors.New("spending reservation is no longer active")
	}
	return nil
}

func (s *SpendingGuardService) Bind(ctx context.Context, reservation SpendingReservation, paymentHash string) error {
	if !reservation.Active || strings.TrimSpace(reservation.ID) == "" || strings.TrimSpace(paymentHash) == "" {
		return nil
	}
	_, err := s.db.Exec(ctx, `update spending_guard_entries set payment_hash=$2,updated_at=now() where id=$1 and status='reserved'`,
		reservation.ID, strings.ToLower(strings.TrimSpace(paymentHash)))
	return err
}

func (s *SpendingGuardService) pending(ctx context.Context, limit int) ([]pendingSpendingReservation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
select id,amount_sat,max_fee_sat,reserved_sat,payment_hash,created_at
from spending_guard_entries where status='reserved'
order by created_at asc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]pendingSpendingReservation, 0)
	for rows.Next() {
		var item pendingSpendingReservation
		item.Active = true
		if err := rows.Scan(&item.ID, &item.AmountSat, &item.MaxFeeSat, &item.ReservedSat, &item.PaymentHash, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SpendingGuardService) Release(ctx context.Context, reservation SpendingReservation, reason string) error {
	if !reservation.Active || strings.TrimSpace(reservation.ID) == "" {
		return nil
	}
	_, err := s.db.Exec(ctx, `
update spending_guard_entries set status='released',release_reason=$2,updated_at=now()
where id=$1 and status='reserved'`, reservation.ID, strings.TrimSpace(reason))
	return err
}

type spendingGuardQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *SpendingGuardService) windowTotals(ctx context.Context, query spendingGuardQuerier, windowStart time.Time) (int64, int64, *time.Time, error) {
	if query == nil {
		query = s.db
	}
	var used, reserved int64
	var oldest *time.Time
	err := query.QueryRow(ctx, `
select
 coalesce(sum(case when status='settled' then actual_debit_sat else 0 end),0),
 coalesce(sum(case when status='reserved' then reserved_sat else 0 end),0),
 min(case when status in ('settled','reserved') then coalesce(settled_at,created_at) end)
from spending_guard_entries
where (status='settled' and settled_at >= $1) or (status='reserved' and created_at >= $1)`, windowStart).Scan(&used, &reserved, &oldest)
	return used, reserved, oldest, err
}

func safeSpendingDebit(amountSat, feeSat int64) (int64, error) {
	if amountSat <= 0 || feeSat < 0 {
		return 0, errors.New("invalid spending debit")
	}
	if feeSat > int64(^uint64(0)>>1)-amountSat {
		return 0, errors.New("spending debit is too large")
	}
	return amountSat + feeSat, nil
}

func normalizeSpendingSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "wallet", "chat_keysend", "loop_out_brln":
		return strings.ToLower(strings.TrimSpace(source))
	default:
		return "wallet"
	}
}

func newSpendingReservationID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func spendingGuardNeedsReauth(current, next SpendingGuardSettings) bool {
	if !current.Enabled {
		return false
	}
	if current.Enabled && !next.Enabled {
		return true
	}
	if next.MaxPaymentSat > current.MaxPaymentSat || next.Rolling24hLimitSat > current.Rolling24hLimitSat {
		return true
	}
	return false
}
