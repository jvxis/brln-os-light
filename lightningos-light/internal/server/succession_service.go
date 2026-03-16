package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	successionConfigID                      = 1
	successionDefaultCheckPeriodDays        = 30
	successionDefaultReminderDays           = 30
	successionDefaultStuckHTLCSec     int64 = 86400
	successionDefaultSweepMinConfs          = 3
	successionDefaultSweepSatPerVbyte int64 = 0
	successionLoopInterval                  = 1 * time.Minute
	successionTickTimeout                   = 20 * time.Second
)

var (
	ErrSuccessionDBUnavailable          = errors.New("succession db unavailable")
	ErrSuccessionInvalidAction          = errors.New("invalid succession simulation action")
	ErrSuccessionTelegramMirrorRequired = errors.New("enable Telegram activity mirror in Notifications before enabling succession mode")
	ErrSuccessionDestinationRequired    = errors.New("set an external on-chain destination address before enabling succession mode")
)

type SuccessionService struct {
	db      *pgxpool.Pool
	logger  *log.Logger
	mu      sync.Mutex
	started bool
}

type SuccessionConfig struct {
	Enabled               bool       `json:"enabled"`
	DryRun                bool       `json:"dry_run"`
	DestinationAddress    string     `json:"destination_address"`
	PreapproveFCOffline   bool       `json:"preapprove_fc_offline"`
	PreapproveFCStuckHTLC bool       `json:"preapprove_fc_stuck_htlc"`
	StuckHTLCThresholdSec int64      `json:"stuck_htlc_threshold_sec"`
	SweepMinConfs         int        `json:"sweep_min_confs"`
	SweepSatPerVbyte      int64      `json:"sweep_sat_per_vbyte"`
	CheckPeriodDays       int        `json:"check_period_days"`
	ReminderPeriodDays    int        `json:"reminder_period_days"`
	LastAliveAt           *time.Time `json:"last_alive_at,omitempty"`
	NextCheckAt           *time.Time `json:"next_check_at,omitempty"`
	DeadlineAt            *time.Time `json:"deadline_at,omitempty"`
	Status                string     `json:"status"`
	UpdatedAt             *time.Time `json:"updated_at,omitempty"`
}

type SuccessionConfigUpdate struct {
	Enabled               *bool
	DryRun                *bool
	DestinationAddress    *string
	PreapproveFCOffline   *bool
	PreapproveFCStuckHTLC *bool
	StuckHTLCThresholdSec *int64
	SweepMinConfs         *int
	SweepSatPerVbyte      *int64
	CheckPeriodDays       *int
	ReminderPeriodDays    *int
}

type SuccessionStatus struct {
	Available bool              `json:"available"`
	Config    *SuccessionConfig `json:"config,omitempty"`
}

func successionScheduleFrom(lastAlive time.Time, checkDays int, reminderDays int) (time.Time, time.Time) {
	next := lastAlive.AddDate(0, 0, checkDays)
	deadline := next.AddDate(0, 0, reminderDays)
	return next, deadline
}

func NewSuccessionService(db *pgxpool.Pool, logger *log.Logger) *SuccessionService {
	return &SuccessionService{db: db, logger: logger}
}

func (s *SuccessionService) Start() {
	if s == nil || s.db == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	go s.runLoop()
}

func (s *SuccessionService) runLoop() {
	ticker := time.NewTicker(successionLoopInterval)
	defer ticker.Stop()

	s.runTick()
	for range ticker.C {
		s.runTick()
	}
}

func (s *SuccessionService) runTick() {
	if s == nil || s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), successionTickTimeout)
	defer cancel()
	if err := s.processScheduler(ctx); err != nil && s.logger != nil {
		s.logger.Printf("succession: scheduler tick failed: %v", err)
	}
}

func (s *SuccessionService) processScheduler(ctx context.Context) error {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}

	now := time.Now().UTC()
	if err := s.ensureSchedule(ctx, &cfg, now); err != nil {
		return err
	}

	if cfg.NextCheckAt != nil && now.Before(cfg.NextCheckAt.UTC()) {
		return s.updateStatus(ctx, "armed")
	}
	if cfg.DeadlineAt != nil && now.Before(cfg.DeadlineAt.UTC()) {
		if err := s.updateStatus(ctx, "reminder_window"); err != nil {
			return err
		}
		return s.maybeSendReminder(ctx, cfg, now)
	}
	return s.maybeTriggerRetirement(ctx, cfg, now)
}

func (s *SuccessionService) ensureSchedule(ctx context.Context, cfg *SuccessionConfig, now time.Time) error {
	if cfg == nil {
		return nil
	}
	changed := false
	if cfg.CheckPeriodDays <= 0 {
		cfg.CheckPeriodDays = successionDefaultCheckPeriodDays
		changed = true
	}
	if cfg.ReminderPeriodDays <= 0 {
		cfg.ReminderPeriodDays = successionDefaultReminderDays
		changed = true
	}
	if cfg.LastAliveAt == nil {
		last := now
		cfg.LastAliveAt = &last
		changed = true
	}
	if cfg.NextCheckAt == nil {
		next := cfg.LastAliveAt.AddDate(0, 0, cfg.CheckPeriodDays)
		cfg.NextCheckAt = &next
		changed = true
	}
	if cfg.DeadlineAt == nil {
		deadline := cfg.NextCheckAt.AddDate(0, 0, cfg.ReminderPeriodDays)
		cfg.DeadlineAt = &deadline
		changed = true
	}
	if !changed {
		return nil
	}

	_, err := s.db.Exec(ctx, `
update succession_config
set check_period_days = $2,
  reminder_period_days = $3,
  last_alive_at = $4,
  next_check_at = $5,
  deadline_at = $6,
  updated_at = now()
where id = $1
`, successionConfigID, cfg.CheckPeriodDays, cfg.ReminderPeriodDays, cfg.LastAliveAt, cfg.NextCheckAt, cfg.DeadlineAt)
	if err != nil {
		return err
	}
	_ = s.insertEvent(ctx, "schedule_normalized", "scheduler", map[string]any{
		"check_period_days":    cfg.CheckPeriodDays,
		"reminder_period_days": cfg.ReminderPeriodDays,
	})
	return nil
}

func (s *SuccessionService) maybeSendReminder(ctx context.Context, cfg SuccessionConfig, now time.Time) error {
	sentToday, err := s.reminderAlreadySentToday(ctx, now)
	if err != nil {
		return err
	}
	if sentToday {
		return nil
	}

	lines := []string{
		"Succession mode proof-of-life reminder.",
		"Reply with /alive or send \"estou vivo\" to confirm.",
	}
	if cfg.DeadlineAt != nil {
		lines = append(lines, "Automatic retirement deadline: "+cfg.DeadlineAt.UTC().Format(time.RFC3339))
	}
	if cfg.DryRun {
		lines = append(lines, "Dry-run is enabled for succession mode.")
	}
	message := strings.Join(lines, "\n")

	if err := s.sendTelegram(message); err != nil {
		_ = s.insertEvent(ctx, "reminder_send_failed", "scheduler", map[string]any{
			"error": err.Error(),
		})
		return nil
	}

	_ = s.insertEvent(ctx, "reminder_sent", "scheduler", map[string]any{
		"deadline_at": cfg.DeadlineAt,
	})
	return nil
}

func (s *SuccessionService) reminderAlreadySentToday(ctx context.Context, now time.Time) (bool, error) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var count int
	if err := s.db.QueryRow(ctx, `
select count(*)
from succession_events
where event_type = 'reminder_sent'
  and created_at >= $1
`, start).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *SuccessionService) maybeTriggerRetirement(ctx context.Context, cfg SuccessionConfig, now time.Time) error {
	switch cfg.Status {
	case "retirement_triggered", "retirement_running", "retirement_waiting", "retirement_completed", "dry_run_triggered", "dry_run_completed", "retirement_failed":
		return s.refreshRetirementProgress(ctx, cfg)
	}

	status := "retirement_triggered"
	if cfg.DryRun {
		status = "dry_run_triggered"
	}

	configRaw, _ := json.Marshal(map[string]any{
		"trigger":                  "succession_timeout",
		"triggered_at":             now.Format(time.RFC3339),
		"check_period_days":        cfg.CheckPeriodDays,
		"reminder_period_days":     cfg.ReminderPeriodDays,
		"destination_address":      cfg.DestinationAddress,
		"preapprove_fc_offline":    cfg.PreapproveFCOffline,
		"preapprove_fc_stuck_htlc": cfg.PreapproveFCStuckHTLC,
		"stuck_htlc_threshold_sec": cfg.StuckHTLCThresholdSec,
		"sweep_min_confs":          cfg.SweepMinConfs,
		"sweep_sat_per_vbyte":      cfg.SweepSatPerVbyte,
		"succession_dry_run":       cfg.DryRun,
		"succession_deadline_at_rfc3339": func() string {
			if cfg.DeadlineAt == nil {
				return ""
			}
			return cfg.DeadlineAt.UTC().Format(time.RFC3339)
		}(),
	})

	retirement := NewNodeRetirementService(s.db, s.logger, nil, nil, nil)
	session, err := retirement.CreateSession(ctx, NodeRetirementCreateParams{
		Source:             nodeRetirementSourceSuccession,
		DryRun:             cfg.DryRun,
		DisclaimerAccepted: true,
		Config:             configRaw,
	})
	if err != nil {
		if errors.Is(err, ErrNodeRetirementActiveSession) {
			_ = s.updateStatus(ctx, "retirement_waiting")
			_ = s.insertEvent(ctx, "retirement_waiting_active_session", "scheduler", map[string]any{
				"error": err.Error(),
			})
			return nil
		}
		_ = s.updateStatus(ctx, "retirement_failed")
		_ = s.insertEvent(ctx, "retirement_trigger_failed", "scheduler", map[string]any{
			"error": err.Error(),
		})
		return nil
	}

	_ = s.updateStatus(ctx, status)
	_ = s.insertEvent(ctx, "retirement_triggered", "scheduler", map[string]any{
		"session_id": session.SessionID,
		"dry_run":    session.DryRun,
	})
	_ = s.sendTelegram("Succession mode triggered node retirement session " + session.SessionID + ".")
	return nil
}

func (s *SuccessionService) refreshRetirementProgress(ctx context.Context, cfg SuccessionConfig) error {
	var sessionID string
	var state string
	err := s.db.QueryRow(ctx, `
select session_id, state
from node_retirement_sessions
where source = $1
order by created_at desc
limit 1
`, nodeRetirementSourceSuccession).Scan(&sessionID, &state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	nextStatus := "retirement_running"
	switch strings.TrimSpace(state) {
	case nodeRetirementStateCompleted:
		nextStatus = "retirement_completed"
	case nodeRetirementStateDryRunCompleted:
		nextStatus = "dry_run_completed"
	case nodeRetirementStateFailed:
		nextStatus = "retirement_failed"
	case nodeRetirementStateCanceled:
		nextStatus = "retirement_failed"
	default:
		if cfg.DryRun {
			nextStatus = "dry_run_triggered"
		}
	}

	if err := s.updateStatus(ctx, nextStatus); err != nil {
		return err
	}

	if nextStatus == "retirement_completed" || nextStatus == "dry_run_completed" {
		alreadyNotified, notifyErr := s.completionAlreadyNotified(ctx, sessionID)
		if notifyErr == nil && !alreadyNotified {
			_ = s.insertEvent(ctx, "retirement_completed", "scheduler", map[string]any{
				"session_id": sessionID,
				"state":      state,
			})
			_ = s.sendTelegram("Node retirement session " + sessionID + " finished. Review funds reconciliation in the UI.")
		}
	}
	return nil
}

func (s *SuccessionService) completionAlreadyNotified(ctx context.Context, sessionID string) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx, `
select count(*)
from succession_events
where event_type = 'retirement_completed'
  and payload_json ->> 'session_id' = $1
`, strings.TrimSpace(sessionID)).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *SuccessionService) updateStatus(ctx context.Context, status string) error {
	trimmed := strings.TrimSpace(status)
	if trimmed == "" {
		return nil
	}
	_, err := s.db.Exec(ctx, `
update succession_config
set status = $2,
  updated_at = now()
where id = $1
  and status <> $2
`, successionConfigID, trimmed)
	return err
}

func (s *SuccessionService) sendTelegram(text string) error {
	cfg := readTelegramBackupConfig()
	if !cfg.configured() {
		return errors.New("telegram not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return sendTelegramMessage(ctx, cfg.BotToken, cfg.ChatID, strings.TrimSpace(text))
}

func (s *SuccessionService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrSuccessionDBUnavailable
	}

	_, err := s.db.Exec(ctx, `
create table if not exists succession_config (
  id integer primary key,
  enabled boolean not null default false,
  dry_run boolean not null default false,
  destination_address text not null default '',
  preapprove_fc_offline boolean not null default false,
  preapprove_fc_stuck_htlc boolean not null default false,
  stuck_htlc_threshold_sec bigint not null default 86400,
  sweep_min_confs integer not null default 3,
  sweep_sat_per_vbyte bigint not null default 0,
  check_period_days integer not null default 30,
  reminder_period_days integer not null default 30,
  last_alive_at timestamptz,
  next_check_at timestamptz,
  deadline_at timestamptz,
  status text not null default 'disabled',
  updated_at timestamptz not null default now()
);

create table if not exists succession_events (
  id bigserial primary key,
  event_type text not null,
  source text not null default '',
  payload_json jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create index if not exists succession_events_created_idx on succession_events (created_at desc);

alter table succession_config add column if not exists sweep_min_confs integer not null default 3;
alter table succession_config add column if not exists sweep_sat_per_vbyte bigint not null default 0;
`)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
insert into succession_config (
  id, enabled, dry_run, destination_address, preapprove_fc_offline,
  preapprove_fc_stuck_htlc, stuck_htlc_threshold_sec, sweep_min_confs, sweep_sat_per_vbyte, check_period_days,
  reminder_period_days, status
)
values ($1, false, false, '', false, false, $2, $3, $4, $5, $6, 'disabled')
on conflict (id) do nothing
`, successionConfigID, successionDefaultStuckHTLCSec, successionDefaultSweepMinConfs, successionDefaultSweepSatPerVbyte, successionDefaultCheckPeriodDays, successionDefaultReminderDays)
	return err
}

func (s *SuccessionService) Status(ctx context.Context) (SuccessionStatus, error) {
	if s == nil || s.db == nil {
		return SuccessionStatus{Available: false}, ErrSuccessionDBUnavailable
	}
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return SuccessionStatus{Available: false}, err
	}
	return SuccessionStatus{Available: true, Config: &cfg}, nil
}

func (s *SuccessionService) GetConfig(ctx context.Context) (SuccessionConfig, error) {
	if s == nil || s.db == nil {
		return SuccessionConfig{}, ErrSuccessionDBUnavailable
	}

	var cfg SuccessionConfig
	err := s.db.QueryRow(ctx, `
select enabled, dry_run, destination_address, preapprove_fc_offline, preapprove_fc_stuck_htlc,
  stuck_htlc_threshold_sec, sweep_min_confs, sweep_sat_per_vbyte, check_period_days, reminder_period_days,
  last_alive_at, next_check_at, deadline_at, status, updated_at
from succession_config
where id = $1
`, successionConfigID).Scan(
		&cfg.Enabled,
		&cfg.DryRun,
		&cfg.DestinationAddress,
		&cfg.PreapproveFCOffline,
		&cfg.PreapproveFCStuckHTLC,
		&cfg.StuckHTLCThresholdSec,
		&cfg.SweepMinConfs,
		&cfg.SweepSatPerVbyte,
		&cfg.CheckPeriodDays,
		&cfg.ReminderPeriodDays,
		scanOptionalTime(&cfg.LastAliveAt),
		scanOptionalTime(&cfg.NextCheckAt),
		scanOptionalTime(&cfg.DeadlineAt),
		&cfg.Status,
		scanOptionalTime(&cfg.UpdatedAt),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SuccessionConfig{}, ErrSuccessionDBUnavailable
		}
		return SuccessionConfig{}, err
	}
	cfg.DestinationAddress = strings.TrimSpace(cfg.DestinationAddress)
	cfg.Status = strings.TrimSpace(cfg.Status)
	if cfg.Status == "" {
		cfg.Status = "disabled"
	}
	if cfg.SweepMinConfs <= 0 {
		cfg.SweepMinConfs = successionDefaultSweepMinConfs
	}
	if cfg.SweepSatPerVbyte < 0 {
		cfg.SweepSatPerVbyte = successionDefaultSweepSatPerVbyte
	}
	return cfg, nil
}

func (s *SuccessionService) UpdateConfig(ctx context.Context, update SuccessionConfigUpdate) (SuccessionConfig, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return SuccessionConfig{}, err
	}
	wasEnabled := cfg.Enabled

	if update.Enabled != nil {
		cfg.Enabled = *update.Enabled
	}
	if update.DryRun != nil {
		cfg.DryRun = *update.DryRun
	}
	if update.DestinationAddress != nil {
		cfg.DestinationAddress = strings.TrimSpace(*update.DestinationAddress)
	}
	if update.PreapproveFCOffline != nil {
		cfg.PreapproveFCOffline = *update.PreapproveFCOffline
	}
	if update.PreapproveFCStuckHTLC != nil {
		cfg.PreapproveFCStuckHTLC = *update.PreapproveFCStuckHTLC
	}
	if update.StuckHTLCThresholdSec != nil {
		cfg.StuckHTLCThresholdSec = *update.StuckHTLCThresholdSec
	}
	if update.SweepMinConfs != nil {
		cfg.SweepMinConfs = *update.SweepMinConfs
	}
	if update.SweepSatPerVbyte != nil {
		cfg.SweepSatPerVbyte = *update.SweepSatPerVbyte
	}
	if update.CheckPeriodDays != nil {
		cfg.CheckPeriodDays = *update.CheckPeriodDays
	}
	if update.ReminderPeriodDays != nil {
		cfg.ReminderPeriodDays = *update.ReminderPeriodDays
	}
	if !wasEnabled && cfg.Enabled {
		mirrorEnabled, mirrorErr := s.telegramActivityMirrorEnabled(ctx)
		if mirrorErr != nil {
			if s.logger != nil {
				s.logger.Printf("succession: failed to verify telegram activity mirror: %v", mirrorErr)
			}
			return SuccessionConfig{}, ErrSuccessionTelegramMirrorRequired
		}
		if !mirrorEnabled {
			return SuccessionConfig{}, ErrSuccessionTelegramMirrorRequired
		}
	}
	if cfg.Enabled && strings.TrimSpace(cfg.DestinationAddress) == "" {
		return SuccessionConfig{}, ErrSuccessionDestinationRequired
	}

	if cfg.CheckPeriodDays <= 0 {
		cfg.CheckPeriodDays = successionDefaultCheckPeriodDays
	}
	if cfg.ReminderPeriodDays <= 0 {
		cfg.ReminderPeriodDays = successionDefaultReminderDays
	}
	if cfg.StuckHTLCThresholdSec <= 0 {
		cfg.StuckHTLCThresholdSec = successionDefaultStuckHTLCSec
	}
	if cfg.SweepMinConfs <= 0 {
		cfg.SweepMinConfs = successionDefaultSweepMinConfs
	}
	if cfg.SweepSatPerVbyte < 0 {
		cfg.SweepSatPerVbyte = successionDefaultSweepSatPerVbyte
	}
	if !cfg.Enabled {
		cfg.Status = "disabled"
	} else {
		cfg.Status = "armed"
	}

	now := time.Now().UTC()
	if cfg.Enabled {
		// Treat config saves from the UI/API as an operator proof-of-life and re-arm the full schedule.
		lastAlive := now
		next, deadline := successionScheduleFrom(lastAlive, cfg.CheckPeriodDays, cfg.ReminderPeriodDays)
		cfg.LastAliveAt = &lastAlive
		cfg.NextCheckAt = &next
		cfg.DeadlineAt = &deadline
	} else {
		if cfg.LastAliveAt == nil {
			cfg.LastAliveAt = &now
		}
		if cfg.NextCheckAt == nil {
			next, deadline := successionScheduleFrom(*cfg.LastAliveAt, cfg.CheckPeriodDays, cfg.ReminderPeriodDays)
			cfg.NextCheckAt = &next
			if cfg.DeadlineAt == nil {
				cfg.DeadlineAt = &deadline
			}
		}
		if cfg.DeadlineAt == nil {
			_, deadline := successionScheduleFrom(*cfg.NextCheckAt, 0, cfg.ReminderPeriodDays)
			cfg.DeadlineAt = &deadline
		}
	}

	_, err = s.db.Exec(ctx, `
update succession_config
set enabled = $2,
  dry_run = $3,
  destination_address = $4,
  preapprove_fc_offline = $5,
  preapprove_fc_stuck_htlc = $6,
  stuck_htlc_threshold_sec = $7,
  sweep_min_confs = $8,
  sweep_sat_per_vbyte = $9,
  check_period_days = $10,
  reminder_period_days = $11,
  last_alive_at = $12,
  next_check_at = $13,
  deadline_at = $14,
  status = $15,
  updated_at = now()
where id = $1
`, successionConfigID, cfg.Enabled, cfg.DryRun, cfg.DestinationAddress, cfg.PreapproveFCOffline,
		cfg.PreapproveFCStuckHTLC, cfg.StuckHTLCThresholdSec, cfg.SweepMinConfs, cfg.SweepSatPerVbyte,
		cfg.CheckPeriodDays, cfg.ReminderPeriodDays, cfg.LastAliveAt, cfg.NextCheckAt, cfg.DeadlineAt, cfg.Status)
	if err != nil {
		return SuccessionConfig{}, err
	}

	_ = s.insertEvent(ctx, "config_updated", "api", map[string]any{
		"enabled":             cfg.Enabled,
		"dry_run":             cfg.DryRun,
		"sweep_min_confs":     cfg.SweepMinConfs,
		"sweep_sat_per_vbyte": cfg.SweepSatPerVbyte,
		"last_alive_at":       cfg.LastAliveAt,
		"next_check_at":       cfg.NextCheckAt,
		"deadline_at":         cfg.DeadlineAt,
	})

	return s.GetConfig(ctx)
}

func (s *SuccessionService) MarkAlive(ctx context.Context, source string) (SuccessionConfig, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return SuccessionConfig{}, err
	}

	now := time.Now().UTC()
	next, deadline := successionScheduleFrom(now, cfg.CheckPeriodDays, cfg.ReminderPeriodDays)

	status := cfg.Status
	if cfg.Enabled {
		status = "armed"
	} else {
		status = "disabled"
	}

	_, err = s.db.Exec(ctx, `
update succession_config
set last_alive_at = $2,
  next_check_at = $3,
  deadline_at = $4,
  status = $5,
  updated_at = now()
where id = $1
`, successionConfigID, now, next, deadline, status)
	if err != nil {
		return SuccessionConfig{}, err
	}

	_ = s.insertEvent(ctx, "alive_confirmed", normalizeSuccessionSource(source), map[string]any{
		"last_alive_at": now.Format(time.RFC3339),
		"next_check_at": next.Format(time.RFC3339),
		"deadline_at":   deadline.Format(time.RFC3339),
	})
	return s.GetConfig(ctx)
}

func (s *SuccessionService) Simulate(ctx context.Context, action string, source string) error {
	normalized := strings.TrimSpace(strings.ToLower(action))
	if normalized != "alive" && normalized != "not_alive" {
		return ErrSuccessionInvalidAction
	}
	src := normalizeSuccessionSource(source)
	if err := s.insertEvent(ctx, "simulate_"+normalized, src, map[string]any{
		"action": normalized,
	}); err != nil {
		return err
	}

	if normalized == "alive" {
		_, err := s.MarkAlive(ctx, src)
		return err
	}

	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	// Simulation always triggers a dry-run retirement session.
	configRaw, _ := json.Marshal(map[string]any{
		"trigger":                  "succession_simulate_not_alive",
		"triggered_at":             now.Format(time.RFC3339),
		"check_period_days":        cfg.CheckPeriodDays,
		"reminder_period_days":     cfg.ReminderPeriodDays,
		"destination_address":      cfg.DestinationAddress,
		"preapprove_fc_offline":    cfg.PreapproveFCOffline,
		"preapprove_fc_stuck_htlc": cfg.PreapproveFCStuckHTLC,
		"stuck_htlc_threshold_sec": cfg.StuckHTLCThresholdSec,
		"sweep_min_confs":          cfg.SweepMinConfs,
		"sweep_sat_per_vbyte":      cfg.SweepSatPerVbyte,
		"succession_dry_run":       true,
	})

	retirement := NewNodeRetirementService(s.db, s.logger, nil, nil, nil)
	session, createErr := retirement.CreateSession(ctx, NodeRetirementCreateParams{
		Source:             nodeRetirementSourceSuccession,
		DryRun:             true,
		DisclaimerAccepted: true,
		Config:             configRaw,
	})
	if createErr != nil {
		if errors.Is(createErr, ErrNodeRetirementActiveSession) {
			_ = s.updateStatus(ctx, "retirement_waiting")
			_ = s.insertEvent(ctx, "retirement_waiting_active_session", src, map[string]any{
				"error": createErr.Error(),
			})
			return nil
		}
		return createErr
	}

	_ = s.updateStatus(ctx, "dry_run_triggered")
	_ = s.insertEvent(ctx, "retirement_triggered", src, map[string]any{
		"session_id": session.SessionID,
		"dry_run":    session.DryRun,
		"simulated":  true,
	})
	return nil
}

func (s *SuccessionService) insertEvent(ctx context.Context, eventType string, source string, payload any) error {
	if s == nil || s.db == nil {
		return ErrSuccessionDBUnavailable
	}
	raw := []byte(`{}`)
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = encoded
	}
	_, err := s.db.Exec(ctx, `
insert into succession_events (event_type, source, payload_json)
values ($1, $2, $3::jsonb)
`, strings.TrimSpace(eventType), normalizeSuccessionSource(source), string(raw))
	return err
}

func (s *SuccessionService) telegramActivityMirrorEnabled(ctx context.Context) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrSuccessionDBUnavailable
	}
	settings, err := loadTelegramNotificationSettings(ctx, s.db)
	if err != nil {
		return false, err
	}
	return settings.ActivityMirrorEnabled, nil
}

func normalizeSuccessionSource(source string) string {
	normalized := strings.TrimSpace(strings.ToLower(source))
	if normalized == "" {
		return "unknown"
	}
	return normalized
}

func nullableTimeToNull(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value.UTC(), Valid: true}
}
