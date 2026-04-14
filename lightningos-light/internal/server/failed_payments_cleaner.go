package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	failedPaymentsCleanerConfigID             = 1
	failedPaymentsCleanerDefaultIntervalHours = 24
	failedPaymentsCleanerMinIntervalHours     = 1
	failedPaymentsCleanerMaxIntervalHours     = 168
	failedPaymentsCleanerCountTimeout         = 5 * time.Minute
	failedPaymentsCleanerDeleteTimeout        = 15 * time.Minute
)

var errInvalidFailedPaymentsCleanerConfig = errors.New("invalid failed payments cleaner config")

type FailedPaymentsCleanerConfig struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"interval_hours"`
}

type FailedPaymentsCleanerConfigUpdate struct {
	Enabled       *bool
	IntervalHours *int
	RunNow        bool
}

type failedPaymentsCleanerStatusPayload struct {
	Enabled          bool   `json:"enabled"`
	Status           string `json:"status"`
	IntervalHours    int    `json:"interval_hours"`
	LastAttemptAt    string `json:"last_attempt_at,omitempty"`
	LastOkAt         string `json:"last_ok_at,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	LastErrorAt      string `json:"last_error_at,omitempty"`
	LastDeletedCount int    `json:"last_deleted_count,omitempty"`
}

type failedPaymentsCleanerTrigger struct {
	force bool
}

type failedPaymentsCleanerLND interface {
	CountFailedPayments(ctx context.Context) (int, error)
	DeleteFailedPayments(ctx context.Context) error
}

type FailedPaymentsCleaner struct {
	db     *pgxpool.Pool
	lnd    failedPaymentsCleanerLND
	logger *log.Logger

	mu               sync.Mutex
	config           FailedPaymentsCleanerConfig
	lastAttempt      time.Time
	lastOK           time.Time
	lastError        string
	lastErrorAt      time.Time
	lastDeletedCount int
	inFlight         bool
	started          bool
	stop             chan struct{}
	wake             chan failedPaymentsCleanerTrigger
	intervalUpdated  chan struct{}
}

func NewFailedPaymentsCleaner(db *pgxpool.Pool, lnd failedPaymentsCleanerLND, logger *log.Logger) *FailedPaymentsCleaner {
	return &FailedPaymentsCleaner{
		db:     db,
		lnd:    lnd,
		logger: logger,
		config: defaultFailedPaymentsCleanerConfig(),
	}
}

func defaultFailedPaymentsCleanerConfig() FailedPaymentsCleanerConfig {
	return FailedPaymentsCleanerConfig{
		Enabled:       false,
		IntervalHours: failedPaymentsCleanerDefaultIntervalHours,
	}
}

func normalizeFailedPaymentsCleanerConfig(cfg FailedPaymentsCleanerConfig) FailedPaymentsCleanerConfig {
	if cfg.IntervalHours < failedPaymentsCleanerMinIntervalHours {
		cfg.IntervalHours = failedPaymentsCleanerMinIntervalHours
	}
	if cfg.IntervalHours > failedPaymentsCleanerMaxIntervalHours {
		cfg.IntervalHours = failedPaymentsCleanerMaxIntervalHours
	}
	return cfg
}

func validateFailedPaymentsCleanerConfig(cfg FailedPaymentsCleanerConfig) error {
	if cfg.IntervalHours < failedPaymentsCleanerMinIntervalHours || cfg.IntervalHours > failedPaymentsCleanerMaxIntervalHours {
		return fmt.Errorf(
			"%w: interval_hours must be between %d and %d",
			errInvalidFailedPaymentsCleanerConfig,
			failedPaymentsCleanerMinIntervalHours,
			failedPaymentsCleanerMaxIntervalHours,
		)
	}
	return nil
}

func (m *FailedPaymentsCleaner) EnsureSchema(ctx context.Context) error {
	if m.db == nil {
		return errors.New("db unavailable")
	}

	if _, err := m.db.Exec(ctx, `
create table if not exists failed_payments_cleaner_config (
  id integer primary key,
  enabled boolean not null default false,
  interval_hours integer not null default 24,
  updated_at timestamptz not null default now()
);
`); err != nil {
		return err
	}

	_, err := m.db.Exec(ctx, `
insert into failed_payments_cleaner_config (id)
values ($1)
on conflict (id) do nothing
`, failedPaymentsCleanerConfigID)
	return err
}

func (m *FailedPaymentsCleaner) GetConfig(ctx context.Context) (FailedPaymentsCleanerConfig, error) {
	cfg := defaultFailedPaymentsCleanerConfig()
	if m.db == nil {
		return cfg, errors.New("db unavailable")
	}

	err := m.db.QueryRow(ctx, `
select enabled, interval_hours
from failed_payments_cleaner_config
where id = $1
`, failedPaymentsCleanerConfigID).Scan(
		&cfg.Enabled,
		&cfg.IntervalHours,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cfg, nil
		}
		return cfg, err
	}

	return normalizeFailedPaymentsCleanerConfig(cfg), nil
}

func (m *FailedPaymentsCleaner) upsertConfig(ctx context.Context, cfg FailedPaymentsCleanerConfig) error {
	if m.db == nil {
		return errors.New("db unavailable")
	}

	_, err := m.db.Exec(ctx, `
insert into failed_payments_cleaner_config (id, enabled, interval_hours, updated_at)
values ($1, $2, $3, now())
on conflict (id) do update set
  enabled = excluded.enabled,
  interval_hours = excluded.interval_hours,
  updated_at = now()
`, failedPaymentsCleanerConfigID, cfg.Enabled, cfg.IntervalHours)
	return err
}

func (m *FailedPaymentsCleaner) Start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.stop = make(chan struct{})
	m.wake = make(chan failedPaymentsCleanerTrigger, 1)
	m.intervalUpdated = make(chan struct{}, 1)
	m.mu.Unlock()

	if err := m.reloadConfig(); err != nil && m.logger != nil {
		m.logger.Printf("failed-payments-cleaner: config load failed: %v", err)
	}

	go m.run()
}

func (m *FailedPaymentsCleaner) Stop() {
	m.mu.Lock()
	if !m.started || m.stop == nil {
		m.mu.Unlock()
		return
	}
	close(m.stop)
	m.stop = nil
	m.started = false
	m.mu.Unlock()
}

func (m *FailedPaymentsCleaner) reloadConfig() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := m.GetConfig(ctx)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.config = cfg
	m.mu.Unlock()
	return nil
}

func (m *FailedPaymentsCleaner) UpdateConfig(ctx context.Context, update FailedPaymentsCleanerConfigUpdate) (failedPaymentsCleanerStatusPayload, error) {
	current, err := m.GetConfig(ctx)
	if err != nil {
		return failedPaymentsCleanerStatusPayload{}, err
	}

	if update.Enabled != nil {
		current.Enabled = *update.Enabled
	}
	if update.IntervalHours != nil {
		current.IntervalHours = *update.IntervalHours
	}

	if err := validateFailedPaymentsCleanerConfig(current); err != nil {
		return failedPaymentsCleanerStatusPayload{}, err
	}
	if err := m.upsertConfig(ctx, current); err != nil {
		return failedPaymentsCleanerStatusPayload{}, err
	}

	m.mu.Lock()
	m.config = current
	intervalUpdated := m.intervalUpdated
	m.mu.Unlock()

	if intervalUpdated != nil {
		select {
		case intervalUpdated <- struct{}{}:
		default:
		}
	}
	if update.RunNow {
		m.trigger(true)
	}

	return m.Snapshot(), nil
}

func (m *FailedPaymentsCleaner) Snapshot() failedPaymentsCleanerStatusPayload {
	m.mu.Lock()
	cfg := m.config
	lastAttempt := m.lastAttempt
	lastOK := m.lastOK
	lastError := m.lastError
	lastErrorAt := m.lastErrorAt
	lastDeleted := m.lastDeletedCount
	inFlight := m.inFlight
	m.mu.Unlock()

	status := "disabled"
	if cfg.Enabled {
		status = "checking"
		interval := time.Duration(localMaxInt(cfg.IntervalHours, 1)) * time.Hour
		if inFlight {
			status = "checking"
		} else if lastError != "" && (lastOK.IsZero() || lastErrorAt.After(lastOK)) {
			status = "warn"
		} else if !lastOK.IsZero() {
			status = "ok"
			if time.Since(lastOK) > interval*2 {
				status = "warn"
			}
		}
	}

	payload := failedPaymentsCleanerStatusPayload{
		Enabled:          cfg.Enabled,
		Status:           status,
		IntervalHours:    cfg.IntervalHours,
		LastDeletedCount: lastDeleted,
	}
	if !lastAttempt.IsZero() {
		payload.LastAttemptAt = lastAttempt.UTC().Format(time.RFC3339)
	}
	if !lastOK.IsZero() {
		payload.LastOkAt = lastOK.UTC().Format(time.RFC3339)
	}
	if lastError != "" {
		payload.LastError = lastError
	}
	if !lastErrorAt.IsZero() {
		payload.LastErrorAt = lastErrorAt.UTC().Format(time.RFC3339)
	}
	return payload
}

func (m *FailedPaymentsCleaner) trigger(force bool) {
	m.mu.Lock()
	wake := m.wake
	m.mu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- failedPaymentsCleanerTrigger{force: force}:
	default:
	}
}

func (m *FailedPaymentsCleaner) currentInterval() time.Duration {
	m.mu.Lock()
	intervalHours := m.config.IntervalHours
	m.mu.Unlock()
	if intervalHours <= 0 {
		intervalHours = failedPaymentsCleanerDefaultIntervalHours
	}
	return time.Duration(intervalHours) * time.Hour
}

func (m *FailedPaymentsCleaner) run() {
	timer := time.NewTimer(m.currentInterval())
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			m.tick(false)
			timer.Reset(m.currentInterval())
		case trigger := <-m.wake:
			m.tick(trigger.force)
		case <-m.intervalUpdated:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(m.currentInterval())
		case <-m.stop:
			return
		}
	}
}

func (m *FailedPaymentsCleaner) tick(force bool) {
	m.mu.Lock()
	enabled := m.config.Enabled
	if (!enabled && !force) || m.inFlight {
		m.mu.Unlock()
		return
	}
	m.inFlight = true
	m.lastAttempt = time.Now().UTC()
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.inFlight = false
		m.mu.Unlock()
	}()

	countCtx, countCancel := context.WithTimeout(context.Background(), failedPaymentsCleanerCountTimeout)
	deleted, err := m.lnd.CountFailedPayments(countCtx)
	countCancel()
	if err != nil {
		m.recordFailure(failedPaymentsCleanerErrorMessage("counting failed payments", failedPaymentsCleanerCountTimeout, err))
		return
	}
	if deleted == 0 {
		m.recordSuccess(0)
		return
	}

	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), failedPaymentsCleanerDeleteTimeout)
	err = m.lnd.DeleteFailedPayments(deleteCtx)
	deleteCancel()
	if err != nil {
		m.recordFailure(failedPaymentsCleanerErrorMessage("deleting failed payments", failedPaymentsCleanerDeleteTimeout, err))
		return
	}

	m.recordSuccess(deleted)
}

func failedPaymentsCleanerErrorMessage(stage string, timeout time.Duration, err error) string {
	if err == nil {
		return "failed payments cleanup failed"
	}
	if errors.Is(err, lndclient.ErrFailedPaymentsCleanupUnsupported) {
		return err.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err) {
		return fmt.Sprintf("failed payments cleanup timed out after %s while %s", formatFailedPaymentsCleanerTimeout(timeout), stage)
	}
	msg := strings.TrimSpace(lndDetailedErrorMessage(err))
	if msg == "" {
		msg = strings.TrimSpace(err.Error())
	}
	if msg == "" {
		return "failed payments cleanup failed"
	}
	if stage == "" {
		return msg
	}
	return fmt.Sprintf("%s: %s", stage, msg)
}

func formatFailedPaymentsCleanerTimeout(timeout time.Duration) string {
	if timeout <= 0 {
		return "0s"
	}
	if timeout%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(timeout/time.Minute))
	}
	if timeout%time.Second == 0 {
		return fmt.Sprintf("%ds", int(timeout/time.Second))
	}
	return timeout.String()
}

func (m *FailedPaymentsCleaner) recordFailure(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "failed payments cleanup failed"
	}

	m.mu.Lock()
	m.lastError = msg
	m.lastErrorAt = time.Now().UTC()
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Printf("failed-payments-cleaner: %s", msg)
	}
}

func (m *FailedPaymentsCleaner) recordSuccess(deleted int) {
	m.mu.Lock()
	hadError := m.lastError != ""
	m.lastOK = time.Now().UTC()
	m.lastError = ""
	m.lastErrorAt = time.Time{}
	m.lastDeletedCount = deleted
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Printf("failed-payments-cleaner: deleted=%d", deleted)
		if hadError {
			m.logger.Printf("failed-payments-cleaner: recovered")
		}
	}
}

func (s *Server) handleLNFailedPaymentsCleanerGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.failedPaymentsCleanerService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "failed payments cleaner unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	writeJSON(w, http.StatusOK, svc.Snapshot())
}

func (s *Server) handleLNFailedPaymentsCleanerPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.failedPaymentsCleanerService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "failed payments cleaner unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}

	var req struct {
		Enabled       *bool `json:"enabled"`
		IntervalHours *int  `json:"interval_hours"`
		RunNow        bool  `json:"run_now"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	payload, err := svc.UpdateConfig(ctx, FailedPaymentsCleanerConfigUpdate{
		Enabled:       req.Enabled,
		IntervalHours: req.IntervalHours,
		RunNow:        req.RunNow,
	})
	if err != nil {
		if errors.Is(err, errInvalidFailedPaymentsCleanerConfig) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, payload)
}
