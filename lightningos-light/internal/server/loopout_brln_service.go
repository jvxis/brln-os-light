package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	loopOutBRLNStatusRunning          = "running"
	loopOutBRLNStatusWaitingLiquidity = "waiting_liquidity"
	loopOutBRLNStatusPauseRequested   = "pause_requested"
	loopOutBRLNStatusPaused           = "paused"
	loopOutBRLNStatusCancelRequested  = "cancel_requested"
	loopOutBRLNStatusCompleted        = "completed"
	loopOutBRLNStatusCancelled        = "cancelled"
	loopOutBRLNStatusFailed           = "failed"

	loopOutBRLNPaymentResolving   = "resolving"
	loopOutBRLNPaymentSending     = "sending"
	loopOutBRLNPaymentReconciling = "reconciling"
	loopOutBRLNPaymentSucceeded   = "succeeded"
	loopOutBRLNPaymentFailed      = "failed"

	loopOutBRLNDefaultRetryDelay = 5 * time.Minute
	loopOutBRLNUnknownGrace      = 2 * time.Minute
	loopOutBRLNWorkerInterval    = 2 * time.Second
	loopOutBRLNMaxAmountSat      = int64(9_007_199_254_740_991)
)

var loopOutBRLNActiveStatuses = []string{
	loopOutBRLNStatusRunning,
	loopOutBRLNStatusWaitingLiquidity,
	loopOutBRLNStatusPauseRequested,
	loopOutBRLNStatusPaused,
	loopOutBRLNStatusCancelRequested,
}

type loopOutBRLNLND interface {
	ListChannels(context.Context) ([]lndclient.ChannelInfo, error)
	DecodeInvoice(context.Context, string) (lndclient.DecodedInvoice, error)
	PayInvoice(context.Context, string, []uint64, int64) error
	TrackPaymentDetails(context.Context, string) (lndclient.PaymentDetails, bool, error)
	NewAddress(context.Context) (string, error)
}

type LoopOutBRLNRequest struct {
	LightningAddress       string   `json:"lightning_address"`
	TotalSat               int64    `json:"total_sat"`
	TrancheSat             int64    `json:"tranche_sat"`
	IntervalSeconds        int      `json:"interval_seconds"`
	TimeoutSeconds         int      `json:"timeout_seconds"`
	MaxFeePPM              int64    `json:"max_fee_ppm"`
	MinLocalPercent        float64  `json:"min_local_percent"`
	Comment                string   `json:"comment,omitempty"`
	SelectedChannelIDs     []string `json:"selected_channel_ids,omitempty"`
	SuppressFailedTelegram bool     `json:"suppress_failed_telegram,omitempty"`
	StrikeReturnEnabled    bool     `json:"strike_return_enabled,omitempty"`
	ConfirmPassword        string   `json:"confirm_password,omitempty"`
}

type LoopOutBRLNChannelPreview struct {
	ChannelID        string  `json:"channel_id"`
	ChannelPoint     string  `json:"channel_point"`
	PeerAlias        string  `json:"peer_alias"`
	RemotePubkey     string  `json:"remote_pubkey"`
	CapacitySat      int64   `json:"capacity_sat"`
	LocalBalanceSat  int64   `json:"local_balance_sat"`
	LocalPercent     float64 `json:"local_percent"`
	ReserveTargetSat int64   `json:"reserve_target_sat"`
	DrainableSat     int64   `json:"drainable_sat"`
	EligibleFirst    bool    `json:"eligible_first"`
	Reason           string  `json:"reason,omitempty"`
}

type LoopOutBRLNPreview struct {
	LightningAddress  string                      `json:"lightning_address"`
	TotalSat          int64                       `json:"total_sat"`
	TrancheSat        int64                       `json:"tranche_sat"`
	LastTrancheSat    int64                       `json:"last_tranche_sat"`
	EstimatedParts    int                         `json:"estimated_parts"`
	MaxFeeTotalSat    int64                       `json:"max_fee_total_sat"`
	TotalDrainableSat int64                       `json:"total_drainable_sat"`
	CanStart          bool                        `json:"can_start"`
	Warnings          []string                    `json:"warnings,omitempty"`
	Channels          []LoopOutBRLNChannelPreview `json:"channels"`
}

type LoopOutBRLNAddressValidation struct {
	LightningAddress string `json:"lightning_address"`
	MinSendableMsat  int64  `json:"min_sendable_msat"`
	MaxSendableMsat  int64  `json:"max_sendable_msat"`
	CommentAllowed   int    `json:"comment_allowed"`
}

type LoopOutBRLNJob struct {
	ID                     int64      `json:"id"`
	LightningAddress       string     `json:"lightning_address"`
	TotalSat               int64      `json:"total_sat"`
	TrancheSat             int64      `json:"tranche_sat"`
	IntervalSeconds        int        `json:"interval_seconds"`
	TimeoutSeconds         int        `json:"timeout_seconds"`
	MaxFeePPM              int64      `json:"max_fee_ppm"`
	MinLocalPercent        float64    `json:"min_local_percent"`
	Comment                string     `json:"comment,omitempty"`
	SelectedChannelIDs     []string   `json:"selected_channel_ids,omitempty"`
	SuppressFailedTelegram bool       `json:"suppress_failed_telegram"`
	StrikeReturnEnabled    bool       `json:"strike_return_enabled"`
	Status                 string     `json:"status"`
	SentSat                int64      `json:"sent_sat"`
	FeeSat                 int64      `json:"fee_sat"`
	AttemptCount           int        `json:"attempt_count"`
	RetryRound             int        `json:"retry_round"`
	LastError              string     `json:"last_error,omitempty"`
	NextAttemptAt          time.Time  `json:"next_attempt_at"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	CompletedAt            *time.Time `json:"completed_at,omitempty"`
}

type LoopOutBRLNPayment struct {
	ID            int64      `json:"id"`
	JobID         int64      `json:"job_id"`
	SequenceNo    int        `json:"sequence_no"`
	RetryRound    int        `json:"retry_round"`
	AttemptNo     int        `json:"attempt_no"`
	AmountSat     int64      `json:"amount_sat"`
	PaymentHash   string     `json:"payment_hash,omitempty"`
	Status        string     `json:"status"`
	FeeSat        int64      `json:"fee_sat"`
	FeeMsat       int64      `json:"fee_msat"`
	ChannelID     string     `json:"channel_id,omitempty"`
	ChannelPoint  string     `json:"channel_point,omitempty"`
	ChannelAlias  string     `json:"channel_alias,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type LoopOutBRLNEvent struct {
	ID        int64          `json:"id"`
	JobID     int64          `json:"job_id"`
	Kind      string         `json:"kind"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type LoopOutBRLNJobDetail struct {
	Job          LoopOutBRLNJob           `json:"job"`
	Payments     []LoopOutBRLNPayment     `json:"payments"`
	Events       []LoopOutBRLNEvent       `json:"events"`
	StrikeReturn *LoopOutBRLNStrikeReturn `json:"strike_return,omitempty"`
}

type LoopOutBRLNStatus struct {
	Installed bool            `json:"installed"`
	Enabled   bool            `json:"enabled"`
	ActiveJob *LoopOutBRLNJob `json:"active_job,omitempty"`
}

type loopOutBRLNCandidate struct {
	channel   lndclient.ChannelInfo
	drainable int64
	reserve   int64
}

type LoopOutBRLNService struct {
	db            *pgxpool.Pool
	lnd           loopOutBRLNLND
	logger        *log.Logger
	spendingGuard lightningSpendingGuard
	wake          chan struct{}
	start         sync.Once
	workMu        sync.Mutex
}

func NewLoopOutBRLNService(db *pgxpool.Pool, lnd loopOutBRLNLND, logger *log.Logger) *LoopOutBRLNService {
	return &LoopOutBRLNService{db: db, lnd: lnd, logger: logger, wake: make(chan struct{}, 1)}
}

func (s *LoopOutBRLNService) AttachSpendingGuard(guard lightningSpendingGuard) {
	s.workMu.Lock()
	s.spendingGuard = guard
	s.workMu.Unlock()
}

func (s *LoopOutBRLNService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("Loop Out BRLN database unavailable")
	}
	_, err := s.db.Exec(ctx, `
create table if not exists loopout_brln_settings (
  id smallint primary key check (id = 1),
  app_installed boolean not null default false,
  app_enabled boolean not null default false,
  updated_at timestamptz not null default now()
);
insert into loopout_brln_settings (id) values (1) on conflict (id) do nothing;

create table if not exists loopout_brln_jobs (
  id bigserial primary key,
  lightning_address text not null,
  total_sat bigint not null,
  tranche_sat bigint not null,
  interval_seconds integer not null default 15,
  timeout_seconds integer not null default 120,
  max_fee_ppm bigint not null default 2500,
  min_local_percent double precision not null default 60,
  comment text not null default '',
  selected_channel_ids jsonb not null default '[]'::jsonb,
  suppress_failed_telegram boolean not null default false,
  strike_return_enabled boolean not null default false,
  status text not null,
  sent_sat bigint not null default 0,
  fee_sat bigint not null default 0,
  attempt_count integer not null default 0,
  retry_round integer not null default 0,
  last_error text not null default '',
  next_attempt_at timestamptz not null default now(),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  started_at timestamptz,
  completed_at timestamptz
);
alter table loopout_brln_jobs
  add column if not exists suppress_failed_telegram boolean not null default false;
alter table loopout_brln_jobs
  add column if not exists strike_return_enabled boolean not null default false;

create table if not exists loopout_brln_payments (
  id bigserial primary key,
  job_id bigint not null references loopout_brln_jobs(id) on delete cascade,
  sequence_no integer not null,
  retry_round integer not null default 0,
  attempt_no integer not null,
  amount_sat bigint not null,
  payment_hash text not null default '',
  status text not null,
  fee_sat bigint not null default 0,
  fee_msat bigint not null default 0,
  channel_id text not null default '',
  channel_point text not null default '',
  channel_alias text not null default '',
  failure_reason text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  completed_at timestamptz
);

create table if not exists loopout_brln_events (
  id bigserial primary key,
  job_id bigint not null references loopout_brln_jobs(id) on delete cascade,
  kind text not null,
  level text not null default 'info',
  message text not null default '',
  metadata jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create table if not exists loopout_brln_strike_returns (
  id bigserial primary key,
  job_id bigint not null unique references loopout_brln_jobs(id) on delete cascade,
  automatic boolean not null default false,
  status text not null default 'pending',
  amount_sat bigint not null,
  btc_address text not null default '',
  idempotency_key text not null default '',
  quote_id text not null default '',
  payment_id text not null default '',
  txid text not null default '',
  fee_sat bigint not null default 0,
  estimated_delivery_minutes integer not null default 0,
  last_error text not null default '',
  next_check_at timestamptz not null default now(),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  completed_at timestamptz
);

create unique index if not exists loopout_brln_one_active_job_idx
  on loopout_brln_jobs ((1))
  where status in ('running','waiting_liquidity','pause_requested','paused','cancel_requested');
create unique index if not exists loopout_brln_payment_hash_idx
  on loopout_brln_payments (payment_hash) where payment_hash <> '';
create index if not exists loopout_brln_jobs_created_idx on loopout_brln_jobs (created_at desc);
create index if not exists loopout_brln_payments_job_idx on loopout_brln_payments (job_id, id desc);
create index if not exists loopout_brln_events_job_idx on loopout_brln_events (job_id, id desc);
create index if not exists loopout_brln_strike_returns_due_idx
  on loopout_brln_strike_returns (next_check_at, id)
  where status in ('pending','preparing','quoted','submitted','waiting_balance');
`)
	return err
}

func (s *LoopOutBRLNService) AppState(ctx context.Context) (bool, bool, error) {
	var installed, enabled bool
	err := s.db.QueryRow(ctx, `select app_installed, app_enabled from loopout_brln_settings where id=1`).Scan(&installed, &enabled)
	return installed, enabled, err
}

func (s *LoopOutBRLNService) SetAppInstalled(ctx context.Context, installed, enabled bool) error {
	if !installed {
		enabled = false
	}
	if _, err := s.db.Exec(ctx, `update loopout_brln_settings set app_installed=$1, app_enabled=$2, updated_at=now() where id=1`, installed, enabled); err != nil {
		return err
	}
	if !enabled {
		if err := s.pauseAll(ctx); err != nil {
			return err
		}
	}
	s.signal()
	return nil
}

func (s *LoopOutBRLNService) SetAppEnabled(ctx context.Context, enabled bool) error {
	var installed bool
	if err := s.db.QueryRow(ctx, `select app_installed from loopout_brln_settings where id=1`).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return errors.New("Loop Out BRLN is not installed")
	}
	if _, err := s.db.Exec(ctx, `update loopout_brln_settings set app_enabled=$1, updated_at=now() where id=1`, enabled); err != nil {
		return err
	}
	if !enabled {
		if err := s.pauseAll(ctx); err != nil {
			return err
		}
	}
	s.signal()
	return nil
}

func (s *LoopOutBRLNService) pauseAll(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `
update loopout_brln_jobs j
set status = case when exists (
  select 1 from loopout_brln_payments p
  where p.job_id=j.id and p.status in ('resolving','sending','reconciling')
) then 'pause_requested' else 'paused' end,
updated_at=now()
where status in ('running','waiting_liquidity','pause_requested')`)
	return err
}

func normalizeLoopOutBRLNRequest(req LoopOutBRLNRequest) (LoopOutBRLNRequest, error) {
	req.LightningAddress = strings.ToLower(strings.TrimSpace(req.LightningAddress))
	if _, _, err := splitLightningAddress(req.LightningAddress); err != nil {
		return req, err
	}
	if req.TotalSat <= 0 {
		return req, errors.New("total_sat must be positive")
	}
	if req.TotalSat > loopOutBRLNMaxAmountSat {
		return req, errors.New("total_sat is too large")
	}
	if req.TrancheSat <= 0 || req.TrancheSat > req.TotalSat {
		return req, errors.New("tranche_sat must be positive and no greater than total_sat")
	}
	if req.IntervalSeconds < 0 || req.IntervalSeconds > 86400 {
		return req, errors.New("interval_seconds must be between 0 and 86400")
	}
	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = 120
	}
	if req.TimeoutSeconds < 30 || req.TimeoutSeconds > 600 {
		return req, errors.New("timeout_seconds must be between 30 and 600")
	}
	if req.MaxFeePPM < 1 || req.MaxFeePPM > 1_000_000 {
		return req, errors.New("max_fee_ppm must be between 1 and 1000000")
	}
	if math.IsNaN(req.MinLocalPercent) || math.IsInf(req.MinLocalPercent, 0) || req.MinLocalPercent < 0 || req.MinLocalPercent >= 100 {
		return req, errors.New("min_local_percent must be between 0 and 99.99")
	}
	req.Comment = strings.TrimSpace(req.Comment)
	if len(req.Comment) > 512 {
		return req, errors.New("comment must be at most 512 characters")
	}
	seen := map[string]struct{}{}
	selected := make([]string, 0, len(req.SelectedChannelIDs))
	for _, raw := range req.SelectedChannelIDs {
		value := strings.TrimSpace(raw)
		id, err := strconv.ParseUint(value, 10, 64)
		if err != nil || id == 0 {
			return req, fmt.Errorf("invalid selected channel id %q", raw)
		}
		value = strconv.FormatUint(id, 10)
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		selected = append(selected, value)
	}
	req.SelectedChannelIDs = selected
	if req.StrikeReturnEnabled && !isStrikeLightningAddress(req.LightningAddress) {
		return req, errors.New("automatic Strike return requires a @strike.me destination")
	}
	return req, nil
}

func loopOutBRLNFeeLimitSat(amountSat, ppm int64) int64 {
	if amountSat <= 0 || ppm <= 0 {
		return 0
	}
	whole := (amountSat / 1_000_000) * ppm
	remainder := amountSat % 1_000_000
	return whole + (remainder*ppm+999_999)/1_000_000
}

func loopOutBRLNParts(totalSat, trancheSat int64) (int, int64) {
	if totalSat <= 0 || trancheSat <= 0 {
		return 0, 0
	}
	parts := int(1 + (totalSat-1)/trancheSat)
	last := totalSat - int64(parts-1)*trancheSat
	return parts, last
}

func loopOutBRLNMaxFeeTotal(totalSat, trancheSat, ppm int64) int64 {
	parts, last := loopOutBRLNParts(totalSat, trancheSat)
	if parts == 0 {
		return 0
	}
	return int64(parts-1)*loopOutBRLNFeeLimitSat(trancheSat, ppm) + loopOutBRLNFeeLimitSat(last, ppm)
}

func loopOutBRLNReserveTarget(ch lndclient.ChannelInfo, minPercent float64) int64 {
	target := int64(math.Ceil(float64(ch.CapacitySat) * minPercent / 100))
	if ch.LocalChanReserveSat > target {
		target = ch.LocalChanReserveSat
	}
	return target
}

func loopOutBRLNSelectedSet(ids []string) map[uint64]struct{} {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[uint64]struct{}, len(ids))
	for _, raw := range ids {
		if id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64); err == nil && id > 0 {
			out[id] = struct{}{}
		}
	}
	return out
}

func loopOutBRLNCandidates(channels []lndclient.ChannelInfo, selected map[uint64]struct{}, amountSat, feeLimitSat int64, minPercent float64) []loopOutBRLNCandidate {
	out := make([]loopOutBRLNCandidate, 0, len(channels))
	for _, ch := range channels {
		if ch.ChannelID == 0 || !ch.Active || ch.LocalDisabled || ch.CapacitySat <= 0 {
			continue
		}
		if len(selected) > 0 {
			if _, ok := selected[ch.ChannelID]; !ok {
				continue
			}
		}
		reserve := loopOutBRLNReserveTarget(ch, minPercent)
		drainable := ch.LocalBalanceSat - reserve
		if drainable < amountSat+feeLimitSat {
			continue
		}
		out = append(out, loopOutBRLNCandidate{channel: ch, drainable: drainable, reserve: reserve})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].drainable == out[j].drainable {
			return out[i].channel.ChannelID < out[j].channel.ChannelID
		}
		return out[i].drainable > out[j].drainable
	})
	return out
}

func (s *LoopOutBRLNService) Preview(ctx context.Context, raw LoopOutBRLNRequest) (LoopOutBRLNPreview, error) {
	req, err := normalizeLoopOutBRLNRequest(raw)
	if err != nil {
		return LoopOutBRLNPreview{}, err
	}
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return LoopOutBRLNPreview{}, err
	}
	parts, last := loopOutBRLNParts(req.TotalSat, req.TrancheSat)
	preview := LoopOutBRLNPreview{
		LightningAddress: req.LightningAddress,
		TotalSat:         req.TotalSat,
		TrancheSat:       req.TrancheSat,
		LastTrancheSat:   last,
		EstimatedParts:   parts,
		MaxFeeTotalSat:   loopOutBRLNMaxFeeTotal(req.TotalSat, req.TrancheSat, req.MaxFeePPM),
		Channels:         make([]LoopOutBRLNChannelPreview, 0, len(channels)),
	}
	selected := loopOutBRLNSelectedSet(req.SelectedChannelIDs)
	firstFee := loopOutBRLNFeeLimitSat(req.TrancheSat, req.MaxFeePPM)
	for _, ch := range channels {
		if ch.ChannelID == 0 || ch.CapacitySat <= 0 {
			continue
		}
		if len(selected) > 0 {
			if _, ok := selected[ch.ChannelID]; !ok {
				continue
			}
		}
		reserve := loopOutBRLNReserveTarget(ch, req.MinLocalPercent)
		drainable := ch.LocalBalanceSat - reserve
		if drainable < 0 {
			drainable = 0
		}
		item := LoopOutBRLNChannelPreview{
			ChannelID: strconv.FormatUint(ch.ChannelID, 10), ChannelPoint: ch.ChannelPoint,
			PeerAlias: ch.PeerAlias, RemotePubkey: ch.RemotePubkey, CapacitySat: ch.CapacitySat,
			LocalBalanceSat: ch.LocalBalanceSat, LocalPercent: float64(ch.LocalBalanceSat) * 100 / float64(ch.CapacitySat),
			ReserveTargetSat: reserve, DrainableSat: drainable,
		}
		switch {
		case !ch.Active:
			item.Reason = "inactive"
		case ch.LocalDisabled:
			item.Reason = "disabled"
		case drainable < req.TrancheSat+firstFee:
			item.Reason = "insufficient_for_tranche"
		default:
			item.EligibleFirst = true
		}
		if ch.Active && !ch.LocalDisabled {
			preview.TotalDrainableSat += drainable
		}
		preview.Channels = append(preview.Channels, item)
	}
	sort.Slice(preview.Channels, func(i, j int) bool { return preview.Channels[i].DrainableSat > preview.Channels[j].DrainableSat })
	firstEligible := false
	for _, ch := range preview.Channels {
		if ch.EligibleFirst {
			firstEligible = true
			break
		}
	}
	preview.CanStart = firstEligible && preview.TotalDrainableSat >= req.TotalSat+preview.MaxFeeTotalSat
	if !firstEligible {
		preview.Warnings = append(preview.Warnings, "no_channel_can_send_the_first_tranche")
	}
	if preview.TotalDrainableSat < req.TotalSat+preview.MaxFeeTotalSat {
		preview.Warnings = append(preview.Warnings, "total_drainable_liquidity_may_be_insufficient")
	}
	return preview, nil
}

func (s *LoopOutBRLNService) CreateJob(ctx context.Context, raw LoopOutBRLNRequest) (LoopOutBRLNJob, error) {
	req, err := normalizeLoopOutBRLNRequest(raw)
	if err != nil {
		return LoopOutBRLNJob{}, err
	}
	installed, enabled, err := s.AppState(ctx)
	if err != nil {
		return LoopOutBRLNJob{}, err
	}
	if !installed || !enabled {
		return LoopOutBRLNJob{}, errors.New("Loop Out BRLN app is not running")
	}
	if req.StrikeReturnEnabled {
		if _, err := s.strikeAPIKey(); err != nil {
			return LoopOutBRLNJob{}, err
		}
	}
	preview, err := s.Preview(ctx, req)
	if err != nil {
		return LoopOutBRLNJob{}, err
	}
	if !preview.CanStart {
		return LoopOutBRLNJob{}, errors.New("available source liquidity cannot safely complete this loop")
	}
	selectedRaw, _ := json.Marshal(req.SelectedChannelIDs)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return LoopOutBRLNJob{}, err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `
insert into loopout_brln_jobs (
  lightning_address,total_sat,tranche_sat,interval_seconds,timeout_seconds,max_fee_ppm,
  min_local_percent,comment,selected_channel_ids,suppress_failed_telegram,strike_return_enabled,status,started_at,next_attempt_at
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,'running',now(),now()) returning id`,
		req.LightningAddress, req.TotalSat, req.TrancheSat, req.IntervalSeconds, req.TimeoutSeconds,
		req.MaxFeePPM, req.MinLocalPercent, req.Comment, string(selectedRaw), req.SuppressFailedTelegram,
		req.StrikeReturnEnabled).Scan(&id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "loopout_brln_one_active_job_idx") {
			return LoopOutBRLNJob{}, errors.New("another Loop Out BRLN job is still active")
		}
		return LoopOutBRLNJob{}, err
	}
	if err := insertLoopOutBRLNEvent(ctx, tx, id, "created", "info", "Loop created and approved", map[string]any{
		"total_sat": req.TotalSat, "tranche_sat": req.TrancheSat, "max_fee_ppm": req.MaxFeePPM,
		"suppress_failed_telegram": req.SuppressFailedTelegram, "strike_return_enabled": req.StrikeReturnEnabled,
	}); err != nil {
		return LoopOutBRLNJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LoopOutBRLNJob{}, err
	}
	s.signal()
	return s.GetJob(ctx, id)
}

func (s *LoopOutBRLNService) Status(ctx context.Context) (LoopOutBRLNStatus, error) {
	installed, enabled, err := s.AppState(ctx)
	if err != nil {
		return LoopOutBRLNStatus{}, err
	}
	status := LoopOutBRLNStatus{Installed: installed, Enabled: enabled}
	job, err := s.activeJob(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return status, err
	}
	if err == nil {
		status.ActiveJob = &job
	}
	return status, nil
}

func (s *LoopOutBRLNService) activeJob(ctx context.Context) (LoopOutBRLNJob, error) {
	return scanLoopOutBRLNJob(s.db.QueryRow(ctx, `
select id,lightning_address,total_sat,tranche_sat,interval_seconds,timeout_seconds,max_fee_ppm,
 min_local_percent,comment,selected_channel_ids,suppress_failed_telegram,strike_return_enabled,status,sent_sat,fee_sat,attempt_count,retry_round,
 last_error,next_attempt_at,created_at,updated_at,started_at,completed_at
from loopout_brln_jobs where status = any($1) order by id desc limit 1`, loopOutBRLNActiveStatuses))
}

func (s *LoopOutBRLNService) GetJob(ctx context.Context, id int64) (LoopOutBRLNJob, error) {
	if id <= 0 {
		return LoopOutBRLNJob{}, errors.New("invalid job id")
	}
	return scanLoopOutBRLNJob(s.db.QueryRow(ctx, `
select id,lightning_address,total_sat,tranche_sat,interval_seconds,timeout_seconds,max_fee_ppm,
 min_local_percent,comment,selected_channel_ids,suppress_failed_telegram,strike_return_enabled,status,sent_sat,fee_sat,attempt_count,retry_round,
 last_error,next_attempt_at,created_at,updated_at,started_at,completed_at
from loopout_brln_jobs where id=$1`, id))
}

type loopOutBRLNRow interface{ Scan(...any) error }

func scanLoopOutBRLNJob(row loopOutBRLNRow) (LoopOutBRLNJob, error) {
	var job LoopOutBRLNJob
	var selectedRaw []byte
	err := row.Scan(&job.ID, &job.LightningAddress, &job.TotalSat, &job.TrancheSat, &job.IntervalSeconds,
		&job.TimeoutSeconds, &job.MaxFeePPM, &job.MinLocalPercent, &job.Comment, &selectedRaw,
		&job.SuppressFailedTelegram, &job.StrikeReturnEnabled, &job.Status,
		&job.SentSat, &job.FeeSat, &job.AttemptCount, &job.RetryRound, &job.LastError, &job.NextAttemptAt,
		&job.CreatedAt, &job.UpdatedAt, &job.StartedAt, &job.CompletedAt)
	if err != nil {
		return job, err
	}
	_ = json.Unmarshal(selectedRaw, &job.SelectedChannelIDs)
	return job, nil
}

func (s *LoopOutBRLNService) ListJobs(ctx context.Context, limit int) ([]LoopOutBRLNJob, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
select id,lightning_address,total_sat,tranche_sat,interval_seconds,timeout_seconds,max_fee_ppm,
 min_local_percent,comment,selected_channel_ids,suppress_failed_telegram,strike_return_enabled,status,sent_sat,fee_sat,attempt_count,retry_round,
 last_error,next_attempt_at,created_at,updated_at,started_at,completed_at
from loopout_brln_jobs order by id desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LoopOutBRLNJob, 0, limit)
	for rows.Next() {
		job, err := scanLoopOutBRLNJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *LoopOutBRLNService) JobDetail(ctx context.Context, id int64) (LoopOutBRLNJobDetail, error) {
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return LoopOutBRLNJobDetail{}, err
	}
	payments, err := s.listPayments(ctx, id)
	if err != nil {
		return LoopOutBRLNJobDetail{}, err
	}
	events, err := s.listEvents(ctx, id)
	if err != nil {
		return LoopOutBRLNJobDetail{}, err
	}
	strikeReturn, err := s.getStrikeReturn(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return LoopOutBRLNJobDetail{}, err
	}
	return LoopOutBRLNJobDetail{Job: job, Payments: payments, Events: events, StrikeReturn: strikeReturn}, nil
}

func (s *LoopOutBRLNService) listPayments(ctx context.Context, jobID int64) ([]LoopOutBRLNPayment, error) {
	rows, err := s.db.Query(ctx, `
select id,job_id,sequence_no,retry_round,attempt_no,amount_sat,payment_hash,status,fee_sat,fee_msat,
 channel_id,channel_point,channel_alias,failure_reason,created_at,updated_at,completed_at
from loopout_brln_payments where job_id=$1 order by id desc limit 500`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoopOutBRLNPayment{}
	for rows.Next() {
		var item LoopOutBRLNPayment
		if err := rows.Scan(&item.ID, &item.JobID, &item.SequenceNo, &item.RetryRound, &item.AttemptNo,
			&item.AmountSat, &item.PaymentHash, &item.Status, &item.FeeSat, &item.FeeMsat,
			&item.ChannelID, &item.ChannelPoint, &item.ChannelAlias, &item.FailureReason,
			&item.CreatedAt, &item.UpdatedAt, &item.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *LoopOutBRLNService) listEvents(ctx context.Context, jobID int64) ([]LoopOutBRLNEvent, error) {
	rows, err := s.db.Query(ctx, `select id,job_id,kind,level,message,metadata,created_at from loopout_brln_events where job_id=$1 order by id desc limit 500`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoopOutBRLNEvent{}
	for rows.Next() {
		var item LoopOutBRLNEvent
		var raw []byte
		if err := rows.Scan(&item.ID, &item.JobID, &item.Kind, &item.Level, &item.Message, &raw, &item.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.Metadata)
		out = append(out, item)
	}
	return out, rows.Err()
}

func insertLoopOutBRLNEvent(ctx context.Context, tx pgx.Tx, jobID int64, kind, level, message string, metadata map[string]any) error {
	raw, _ := json.Marshal(metadata)
	_, err := tx.Exec(ctx, `insert into loopout_brln_events (job_id,kind,level,message,metadata) values ($1,$2,$3,$4,$5::jsonb)`,
		jobID, kind, level, message, string(raw))
	return err
}

func (s *LoopOutBRLNService) PauseJob(ctx context.Context, id int64) (LoopOutBRLNJob, error) {
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return LoopOutBRLNJob{}, err
	}
	switch job.Status {
	case loopOutBRLNStatusRunning, loopOutBRLNStatusWaitingLiquidity:
		var inflight bool
		err = s.db.QueryRow(ctx, `select exists(select 1 from loopout_brln_payments where job_id=$1 and status in ('resolving','sending','reconciling'))`, id).Scan(&inflight)
		if err != nil {
			return LoopOutBRLNJob{}, err
		}
		next := loopOutBRLNStatusPaused
		if inflight {
			next = loopOutBRLNStatusPauseRequested
		}
		if _, err := s.db.Exec(ctx, `update loopout_brln_jobs set status=$2,updated_at=now() where id=$1`, id, next); err != nil {
			return LoopOutBRLNJob{}, err
		}
		if next == loopOutBRLNStatusPauseRequested {
			s.appendEvent(ctx, id, "pause_requested", "info", "Pause requested", nil)
		} else {
			s.appendEvent(ctx, id, "paused", "info", "Loop paused", nil)
		}
	case loopOutBRLNStatusPauseRequested, loopOutBRLNStatusPaused:
		return job, nil
	default:
		return LoopOutBRLNJob{}, fmt.Errorf("job cannot be paused from status %s", job.Status)
	}
	return s.GetJob(ctx, id)
}

func (s *LoopOutBRLNService) ResumeJob(ctx context.Context, id int64) (LoopOutBRLNJob, error) {
	installed, enabled, err := s.AppState(ctx)
	if err != nil {
		return LoopOutBRLNJob{}, err
	}
	if !installed || !enabled {
		return LoopOutBRLNJob{}, errors.New("Loop Out BRLN app is not running")
	}
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return LoopOutBRLNJob{}, err
	}
	if job.Status != loopOutBRLNStatusPaused {
		return LoopOutBRLNJob{}, fmt.Errorf("job cannot be resumed from status %s", job.Status)
	}
	if _, err := s.db.Exec(ctx, `update loopout_brln_jobs set status='running',last_error='',next_attempt_at=now(),updated_at=now() where id=$1`, id); err != nil {
		return LoopOutBRLNJob{}, err
	}
	s.appendEvent(ctx, id, "resumed", "info", "Loop resumed", nil)
	s.signal()
	return s.GetJob(ctx, id)
}

func (s *LoopOutBRLNService) CancelJob(ctx context.Context, id int64) (LoopOutBRLNJob, error) {
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return LoopOutBRLNJob{}, err
	}
	eventKind := "cancelled"
	eventMessage := "Loop cancelled"
	switch job.Status {
	case loopOutBRLNStatusRunning, loopOutBRLNStatusWaitingLiquidity, loopOutBRLNStatusPauseRequested:
		var inflight bool
		err = s.db.QueryRow(ctx, `select exists(select 1 from loopout_brln_payments where job_id=$1 and status in ('resolving','sending','reconciling'))`, id).Scan(&inflight)
		if err != nil {
			return LoopOutBRLNJob{}, err
		}
		next := loopOutBRLNStatusCancelled
		if inflight {
			next = loopOutBRLNStatusCancelRequested
			eventKind = "cancel_requested"
			eventMessage = "Cancellation requested"
		}
		if _, err := s.db.Exec(ctx, `update loopout_brln_jobs set status=$2,updated_at=now(),completed_at=case when $2='cancelled' then now() else completed_at end where id=$1`, id, next); err != nil {
			return LoopOutBRLNJob{}, err
		}
	case loopOutBRLNStatusPaused:
		if _, err := s.db.Exec(ctx, `update loopout_brln_jobs set status='cancelled',updated_at=now(),completed_at=now() where id=$1`, id); err != nil {
			return LoopOutBRLNJob{}, err
		}
	case loopOutBRLNStatusCancelRequested, loopOutBRLNStatusCancelled:
		return job, nil
	default:
		return LoopOutBRLNJob{}, fmt.Errorf("job cannot be cancelled from status %s", job.Status)
	}
	s.appendEvent(ctx, id, eventKind, "warning", eventMessage, nil)
	s.signal()
	return s.GetJob(ctx, id)
}

func (s *LoopOutBRLNService) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.start.Do(func() { go s.run(ctx) })
}

func (s *LoopOutBRLNService) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *LoopOutBRLNService) run(ctx context.Context) {
	ticker := time.NewTicker(loopOutBRLNWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
		if err := s.processOnce(ctx); err != nil && s.logger != nil && !errors.Is(err, context.Canceled) {
			s.logger.Printf("loopout brln worker: %v", err)
		}
	}
}

func (s *LoopOutBRLNService) processOnce(ctx context.Context) error {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	installed, enabled, err := s.AppState(ctx)
	if err != nil || !installed || !enabled {
		return err
	}
	job, err := s.nextDueJob(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		_, returnErr := s.processStrikeReturnOnce(ctx)
		return returnErr
	}
	if err != nil {
		return err
	}
	if payment, ok, err := s.activePayment(ctx, job.ID); err != nil {
		return err
	} else if ok {
		return s.reconcilePayment(ctx, job, payment)
	}
	if job.Status == loopOutBRLNStatusPauseRequested {
		_, err := s.db.Exec(ctx, `update loopout_brln_jobs set status='paused',updated_at=now() where id=$1`, job.ID)
		if err == nil {
			s.appendEvent(ctx, job.ID, "paused", "info", "Loop paused", nil)
		}
		return err
	}
	if job.Status == loopOutBRLNStatusCancelRequested {
		_, err := s.db.Exec(ctx, `update loopout_brln_jobs set status='cancelled',completed_at=now(),updated_at=now() where id=$1`, job.ID)
		if err == nil {
			s.appendEvent(ctx, job.ID, "cancelled", "warning", "Loop cancelled", nil)
		}
		return err
	}
	return s.executeNextAttempt(ctx, job)
}

func (s *LoopOutBRLNService) nextDueJob(ctx context.Context) (LoopOutBRLNJob, error) {
	return scanLoopOutBRLNJob(s.db.QueryRow(ctx, `
select id,lightning_address,total_sat,tranche_sat,interval_seconds,timeout_seconds,max_fee_ppm,
 min_local_percent,comment,selected_channel_ids,suppress_failed_telegram,strike_return_enabled,status,sent_sat,fee_sat,attempt_count,retry_round,
 last_error,next_attempt_at,created_at,updated_at,started_at,completed_at
from loopout_brln_jobs
where status in ('running','waiting_liquidity','pause_requested','cancel_requested')
  and (next_attempt_at <= now() or status in ('pause_requested','cancel_requested'))
order by id asc limit 1`))
}

func (s *LoopOutBRLNService) activePayment(ctx context.Context, jobID int64) (LoopOutBRLNPayment, bool, error) {
	var item LoopOutBRLNPayment
	err := s.db.QueryRow(ctx, `
select id,job_id,sequence_no,retry_round,attempt_no,amount_sat,payment_hash,status,fee_sat,fee_msat,
 channel_id,channel_point,channel_alias,failure_reason,created_at,updated_at,completed_at
from loopout_brln_payments where job_id=$1 and status in ('resolving','sending','reconciling') order by id desc limit 1`, jobID).
		Scan(&item.ID, &item.JobID, &item.SequenceNo, &item.RetryRound, &item.AttemptNo, &item.AmountSat,
			&item.PaymentHash, &item.Status, &item.FeeSat, &item.FeeMsat, &item.ChannelID,
			&item.ChannelPoint, &item.ChannelAlias, &item.FailureReason, &item.CreatedAt,
			&item.UpdatedAt, &item.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoopOutBRLNPayment{}, false, nil
	}
	return item, err == nil, err
}

func (s *LoopOutBRLNService) reconcilePayment(ctx context.Context, job LoopOutBRLNJob, payment LoopOutBRLNPayment) error {
	if strings.TrimSpace(payment.PaymentHash) == "" {
		return s.finalizeFailure(ctx, job, payment, "payment hash missing during recovery")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	details, found, err := s.lnd.TrackPaymentDetails(checkCtx, payment.PaymentHash)
	if err != nil {
		_, _ = s.db.Exec(ctx, `update loopout_brln_payments set status='reconciling',failure_reason=$2,updated_at=now() where id=$1`, payment.ID, err.Error())
		_, _ = s.db.Exec(ctx, `update loopout_brln_jobs set last_error=$2,next_attempt_at=now()+interval '30 seconds',updated_at=now() where id=$1`, job.ID, err.Error())
		return nil
	}
	if !found {
		if time.Since(payment.CreatedAt) < loopOutBRLNUnknownGrace {
			_, _ = s.db.Exec(ctx, `update loopout_brln_payments set status='reconciling',updated_at=now() where id=$1`, payment.ID)
			_, _ = s.db.Exec(ctx, `update loopout_brln_jobs set next_attempt_at=now()+interval '15 seconds',updated_at=now() where id=$1`, job.ID)
			return nil
		}
		return s.finalizeFailure(ctx, job, payment, "payment not found in LND after recovery grace period")
	}
	switch strings.ToUpper(details.Status) {
	case "SUCCEEDED":
		return s.finalizeSuccess(ctx, job, payment, details)
	case "FAILED":
		return s.finalizeFailure(ctx, job, payment, "payment failed")
	default:
		_, _ = s.db.Exec(ctx, `update loopout_brln_payments set status='reconciling',updated_at=now() where id=$1`, payment.ID)
		_, _ = s.db.Exec(ctx, `update loopout_brln_jobs set next_attempt_at=now()+interval '15 seconds',updated_at=now() where id=$1`, job.ID)
		return nil
	}
}

func (s *LoopOutBRLNService) executeNextAttempt(ctx context.Context, job LoopOutBRLNJob) error {
	remaining := job.TotalSat - job.SentSat
	if remaining <= 0 {
		_, err := s.db.Exec(ctx, `update loopout_brln_jobs set status='completed',completed_at=coalesce(completed_at,now()),updated_at=now() where id=$1`, job.ID)
		return err
	}
	amount := job.TrancheSat
	if remaining < amount {
		amount = remaining
	}
	feeLimit := loopOutBRLNFeeLimitSat(amount, job.MaxFeePPM)
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return s.waitForLiquidity(ctx, job, "failed to refresh channels: "+err.Error(), false)
	}
	candidates := loopOutBRLNCandidates(channels, loopOutBRLNSelectedSet(job.SelectedChannelIDs), amount, feeLimit, job.MinLocalPercent)
	sequence := s.successCount(ctx, job.ID) + 1
	attempted, err := s.attemptedChannels(ctx, job.ID, sequence, job.RetryRound)
	if err != nil {
		return err
	}
	available := candidates[:0]
	for _, candidate := range candidates {
		if _, seen := attempted[candidate.channel.ChannelID]; !seen {
			available = append(available, candidate)
		}
	}
	if len(available) == 0 {
		newRound := len(candidates) > 0 && len(attempted) > 0
		reason := "No eligible channel can send the next tranche while preserving the liquidity floor"
		return s.waitForLiquidity(ctx, job, reason, newRound)
	}
	candidate := available[0]
	resolveCtx, resolveCancel := context.WithTimeout(ctx, 30*time.Second)
	invoice, err := resolveLightningAddress(resolveCtx, job.LightningAddress, amount, job.Comment)
	resolveCancel()
	if err != nil {
		return s.waitForLiquidity(ctx, job, "lightning address: "+err.Error(), false)
	}
	decodeCtx, decodeCancel := context.WithTimeout(ctx, 10*time.Second)
	decoded, err := s.lnd.DecodeInvoice(decodeCtx, invoice)
	decodeCancel()
	if err != nil {
		return s.waitForLiquidity(ctx, job, "invalid invoice returned by Lightning Address: "+err.Error(), false)
	}
	if decoded.AmountMsat != amount*1000 {
		return s.waitForLiquidity(ctx, job, fmt.Sprintf("Lightning Address returned invoice for %d msat, expected %d msat", decoded.AmountMsat, amount*1000), false)
	}
	if decoded.PaymentHash == "" {
		return s.waitForLiquidity(ctx, job, "Lightning Address returned invoice without payment hash", false)
	}
	if decoded.Timestamp > 0 && decoded.Expiry > 0 && time.Now().Unix() >= decoded.Timestamp+decoded.Expiry-5 {
		return s.waitForLiquidity(ctx, job, "Lightning Address returned an expired invoice", false)
	}
	reservation := SpendingReservation{}
	if s.spendingGuard != nil {
		reservation, err = s.spendingGuard.Reserve(ctx, SpendingIntent{
			Source: "loop_out_brln", AmountSat: amount, MaxFeeSat: feeLimit, PaymentHash: decoded.PaymentHash,
		})
		if err != nil {
			if errors.Is(err, ErrSpendingGuardLimit) {
				return s.waitForLiquidity(ctx, job, "Spending Guard: "+err.Error(), false)
			}
			return err
		}
	}
	payment, err := s.beginAttempt(ctx, job, sequence, amount, candidate)
	if err != nil {
		if s.spendingGuard != nil {
			_ = s.spendingGuard.Release(ctx, reservation, "failed to persist Loop Out attempt")
		}
		return err
	}
	if _, err := s.db.Exec(ctx, `update loopout_brln_payments set payment_hash=$2,status='sending',updated_at=now() where id=$1`, payment.ID, decoded.PaymentHash); err != nil {
		if s.spendingGuard != nil {
			_ = s.spendingGuard.Release(ctx, reservation, "failed before payment submission")
		}
		return err
	}
	payment.PaymentHash = decoded.PaymentHash
	payment.Status = loopOutBRLNPaymentSending
	var currentStatus string
	if err := s.db.QueryRow(ctx, `select status from loopout_brln_jobs where id=$1`, job.ID).Scan(&currentStatus); err != nil {
		return err
	}
	if currentStatus == loopOutBRLNStatusPauseRequested || currentStatus == loopOutBRLNStatusCancelRequested || currentStatus == loopOutBRLNStatusPaused || currentStatus == loopOutBRLNStatusCancelled {
		if s.spendingGuard != nil {
			_ = s.spendingGuard.Release(ctx, reservation, "payment stopped before submission")
		}
		return s.finalizeFailure(ctx, job, payment, "payment stopped before submission")
	}
	payCtx, payCancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	err = s.lnd.PayInvoice(payCtx, invoice, []uint64{candidate.channel.ChannelID}, feeLimit)
	payCancel()
	trackCtx, trackCancel := context.WithTimeout(ctx, 10*time.Second)
	details, found, trackErr := s.lnd.TrackPaymentDetails(trackCtx, decoded.PaymentHash)
	trackCancel()
	if found && strings.EqualFold(details.Status, "SUCCEEDED") {
		if s.spendingGuard != nil {
			_ = s.spendingGuard.Settle(ctx, reservation, paymentDetailsFeeSat(details, feeLimit), decoded.PaymentHash)
		}
		return s.finalizeSuccess(ctx, job, payment, details)
	}
	if found && !strings.EqualFold(details.Status, "FAILED") {
		_, _ = s.db.Exec(ctx, `update loopout_brln_payments set status='reconciling',failure_reason=$2,updated_at=now() where id=$1`, payment.ID, errorText(err, trackErr))
		_, _ = s.db.Exec(ctx, `update loopout_brln_jobs set last_error=$2,next_attempt_at=now()+interval '15 seconds',updated_at=now() where id=$1`, job.ID, errorText(err, trackErr))
		return nil
	}
	if err == nil {
		if s.spendingGuard != nil {
			_ = s.spendingGuard.Settle(ctx, reservation, feeLimit, decoded.PaymentHash)
		}
		// SendPaymentV2 returned success; a delayed tracking index must not turn a
		// paid invoice into a retry. Keep it in reconciliation until LND confirms.
		_, _ = s.db.Exec(ctx, `update loopout_brln_payments set status='reconciling',updated_at=now() where id=$1`, payment.ID)
		_, _ = s.db.Exec(ctx, `update loopout_brln_jobs set next_attempt_at=now()+interval '5 seconds',updated_at=now() where id=$1`, job.ID)
		return nil
	}
	if s.spendingGuard != nil {
		_ = s.spendingGuard.Release(ctx, reservation, errorText(err, trackErr))
	}
	return s.finalizeFailure(ctx, job, payment, errorText(err, trackErr))
}

func errorText(values ...error) string {
	for _, err := range values {
		if err != nil && strings.TrimSpace(err.Error()) != "" {
			return strings.TrimSpace(err.Error())
		}
	}
	return "payment failed"
}

func (s *LoopOutBRLNService) successCount(ctx context.Context, jobID int64) int {
	var count int
	_ = s.db.QueryRow(ctx, `select count(*) from loopout_brln_payments where job_id=$1 and status='succeeded'`, jobID).Scan(&count)
	return count
}

func (s *LoopOutBRLNService) attemptedChannels(ctx context.Context, jobID int64, sequence, retryRound int) (map[uint64]struct{}, error) {
	rows, err := s.db.Query(ctx, `select channel_id from loopout_brln_payments where job_id=$1 and sequence_no=$2 and retry_round=$3 and status='failed'`, jobID, sequence, retryRound)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint64]struct{}{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil {
			out[id] = struct{}{}
		}
	}
	return out, rows.Err()
}

func (s *LoopOutBRLNService) beginAttempt(ctx context.Context, job LoopOutBRLNJob, sequence int, amount int64, candidate loopOutBRLNCandidate) (LoopOutBRLNPayment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return LoopOutBRLNPayment{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `select status from loopout_brln_jobs where id=$1 for update`, job.ID).Scan(&status); err != nil {
		return LoopOutBRLNPayment{}, err
	}
	if status != loopOutBRLNStatusRunning && status != loopOutBRLNStatusWaitingLiquidity {
		return LoopOutBRLNPayment{}, fmt.Errorf("job is no longer runnable: %s", status)
	}
	var attemptNo int
	if err := tx.QueryRow(ctx, `select coalesce(max(attempt_no),0)+1 from loopout_brln_payments where job_id=$1`, job.ID).Scan(&attemptNo); err != nil {
		return LoopOutBRLNPayment{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
insert into loopout_brln_payments (job_id,sequence_no,retry_round,attempt_no,amount_sat,status,channel_id,channel_point,channel_alias)
values ($1,$2,$3,$4,$5,'resolving',$6,$7,$8) returning id`, job.ID, sequence, job.RetryRound,
		attemptNo, amount, strconv.FormatUint(candidate.channel.ChannelID, 10), candidate.channel.ChannelPoint, candidate.channel.PeerAlias).Scan(&id)
	if err != nil {
		return LoopOutBRLNPayment{}, err
	}
	if _, err := tx.Exec(ctx, `update loopout_brln_jobs set status='running',last_error='',updated_at=now() where id=$1`, job.ID); err != nil {
		return LoopOutBRLNPayment{}, err
	}
	if err := insertLoopOutBRLNEvent(ctx, tx, job.ID, "payment_started", "info", "Payment attempt started", map[string]any{
		"sequence_no": sequence, "attempt_no": attemptNo, "amount_sat": amount,
		"channel_id": strconv.FormatUint(candidate.channel.ChannelID, 10), "channel_alias": candidate.channel.PeerAlias,
	}); err != nil {
		return LoopOutBRLNPayment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LoopOutBRLNPayment{}, err
	}
	return LoopOutBRLNPayment{ID: id, JobID: job.ID, SequenceNo: sequence, RetryRound: job.RetryRound,
		AttemptNo: attemptNo, AmountSat: amount, Status: loopOutBRLNPaymentResolving,
		ChannelID: strconv.FormatUint(candidate.channel.ChannelID, 10), ChannelPoint: candidate.channel.ChannelPoint,
		ChannelAlias: candidate.channel.PeerAlias, CreatedAt: time.Now()}, nil
}

func (s *LoopOutBRLNService) waitForLiquidity(ctx context.Context, job LoopOutBRLNJob, reason string, nextRound bool) error {
	retryRound := job.RetryRound
	if nextRound {
		retryRound++
	}
	_, err := s.db.Exec(ctx, `
update loopout_brln_jobs set status='waiting_liquidity',retry_round=$2,last_error=$3,
 next_attempt_at=now()+$4::interval,updated_at=now() where id=$1`,
		job.ID, retryRound, reason, fmt.Sprintf("%d seconds", int(loopOutBRLNDefaultRetryDelay/time.Second)))
	if err != nil {
		return err
	}
	if job.Status != loopOutBRLNStatusWaitingLiquidity || job.LastError != reason {
		s.appendEvent(ctx, job.ID, "waiting_liquidity", "warning", reason, map[string]any{"retry_at_seconds": int(loopOutBRLNDefaultRetryDelay / time.Second)})
	}
	return nil
}

func (s *LoopOutBRLNService) finalizeSuccess(ctx context.Context, job LoopOutBRLNJob, payment LoopOutBRLNPayment, details lndclient.PaymentDetails) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	current, err := scanLoopOutBRLNJob(tx.QueryRow(ctx, `
select id,lightning_address,total_sat,tranche_sat,interval_seconds,timeout_seconds,max_fee_ppm,
 min_local_percent,comment,selected_channel_ids,suppress_failed_telegram,strike_return_enabled,status,sent_sat,fee_sat,attempt_count,retry_round,
 last_error,next_attempt_at,created_at,updated_at,started_at,completed_at
from loopout_brln_jobs where id=$1 for update`, job.ID))
	if err != nil {
		return err
	}
	feeSat := details.FeeSat
	if feeSat <= 0 && details.FeeMsat > 0 {
		feeSat = (details.FeeMsat + 999) / 1000
	}
	if _, err := tx.Exec(ctx, `update loopout_brln_payments set status='succeeded',fee_sat=$2,fee_msat=$3,failure_reason='',completed_at=now(),updated_at=now() where id=$1`,
		payment.ID, feeSat, details.FeeMsat); err != nil {
		return err
	}
	newSent := current.SentSat + payment.AmountSat
	nextStatus := loopOutBRLNStatusRunning
	completed := false
	switch {
	case newSent >= current.TotalSat:
		nextStatus, completed = loopOutBRLNStatusCompleted, true
	case current.Status == loopOutBRLNStatusCancelRequested:
		nextStatus, completed = loopOutBRLNStatusCancelled, true
	case current.Status == loopOutBRLNStatusPauseRequested:
		nextStatus = loopOutBRLNStatusPaused
	}
	_, err = tx.Exec(ctx, `
update loopout_brln_jobs set sent_sat=$2,fee_sat=fee_sat+$3,attempt_count=attempt_count+1,
 status=$4,last_error='',next_attempt_at=now()+$5::interval,updated_at=now(),
 completed_at=case when $6 then now() else completed_at end where id=$1`,
		current.ID, newSent, feeSat, nextStatus, fmt.Sprintf("%d seconds", current.IntervalSeconds), completed)
	if err != nil {
		return err
	}
	if err := insertLoopOutBRLNEvent(ctx, tx, current.ID, "payment_succeeded", "success", "Payment completed", map[string]any{
		"sequence_no": payment.SequenceNo, "amount_sat": payment.AmountSat, "fee_sat": feeSat,
		"payment_hash": payment.PaymentHash, "channel_id": payment.ChannelID, "channel_alias": payment.ChannelAlias,
	}); err != nil {
		return err
	}
	if nextStatus == loopOutBRLNStatusCompleted {
		if err := insertLoopOutBRLNEvent(ctx, tx, current.ID, "completed", "success", "Loop completed", map[string]any{"sent_sat": newSent, "fee_sat": current.FeeSat + feeSat}); err != nil {
			return err
		}
		if current.StrikeReturnEnabled {
			if err := insertLoopOutBRLNStrikeReturn(ctx, tx, current.ID, newSent, true); err != nil {
				return err
			}
			if err := insertLoopOutBRLNEvent(ctx, tx, current.ID, "strike_return_queued", "info", "Automatic Strike return queued", map[string]any{"amount_sat": newSent}); err != nil {
				return err
			}
		}
	} else if nextStatus == loopOutBRLNStatusPaused {
		_ = insertLoopOutBRLNEvent(ctx, tx, current.ID, "paused", "info", "Loop paused after the current payment", nil)
	} else if nextStatus == loopOutBRLNStatusCancelled {
		_ = insertLoopOutBRLNEvent(ctx, tx, current.ID, "cancelled", "warning", "Loop cancelled after the current payment", nil)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.signal()
	return nil
}

func (s *LoopOutBRLNService) finalizeFailure(ctx context.Context, job LoopOutBRLNJob, payment LoopOutBRLNPayment, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "payment failed"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentStatus string
	if err := tx.QueryRow(ctx, `select status from loopout_brln_jobs where id=$1 for update`, job.ID).Scan(&currentStatus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update loopout_brln_payments set status='failed',failure_reason=$2,completed_at=now(),updated_at=now() where id=$1`, payment.ID, reason); err != nil {
		return err
	}
	nextStatus := loopOutBRLNStatusRunning
	completed := false
	if currentStatus == loopOutBRLNStatusPauseRequested {
		nextStatus = loopOutBRLNStatusPaused
	} else if currentStatus == loopOutBRLNStatusPaused {
		nextStatus = loopOutBRLNStatusPaused
	} else if currentStatus == loopOutBRLNStatusCancelRequested {
		nextStatus, completed = loopOutBRLNStatusCancelled, true
	} else if currentStatus == loopOutBRLNStatusCancelled {
		nextStatus, completed = loopOutBRLNStatusCancelled, true
	}
	if _, err := tx.Exec(ctx, `
update loopout_brln_jobs set status=$2,attempt_count=attempt_count+1,last_error=$3,
 next_attempt_at=now()+interval '5 seconds',updated_at=now(),
 completed_at=case when $4 then now() else completed_at end where id=$1`, job.ID, nextStatus, reason, completed); err != nil {
		return err
	}
	if err := insertLoopOutBRLNEvent(ctx, tx, job.ID, "payment_failed", "error", reason, map[string]any{
		"sequence_no": payment.SequenceNo, "attempt_no": payment.AttemptNo, "channel_id": payment.ChannelID,
		"channel_alias": payment.ChannelAlias,
	}); err != nil {
		return err
	}
	if nextStatus == loopOutBRLNStatusPaused {
		_ = insertLoopOutBRLNEvent(ctx, tx, job.ID, "paused", "info", "Loop paused", nil)
	} else if nextStatus == loopOutBRLNStatusCancelled {
		_ = insertLoopOutBRLNEvent(ctx, tx, job.ID, "cancelled", "warning", "Loop cancelled", nil)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.signal()
	return nil
}

func (s *LoopOutBRLNService) appendEvent(ctx context.Context, jobID int64, kind, level, message string, metadata map[string]any) {
	raw, _ := json.Marshal(metadata)
	if _, err := s.db.Exec(ctx, `insert into loopout_brln_events (job_id,kind,level,message,metadata) values ($1,$2,$3,$4,$5::jsonb)`,
		jobID, kind, level, message, string(raw)); err != nil && s.logger != nil {
		s.logger.Printf("loopout brln event insert failed job=%d kind=%s: %v", jobID, kind, err)
	}
}
