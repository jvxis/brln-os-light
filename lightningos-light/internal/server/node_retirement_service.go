package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	nodeRetirementDefaultListLimit  = 50
	nodeRetirementMaxListLimit      = 500
	nodeRetirementDefaultEventLimit = 100
	nodeRetirementMaxEventLimit     = 1000
)

const (
	nodeRetirementStateCreated          = "created"
	nodeRetirementStateSnapshotTaken    = "snapshot_taken"
	nodeRetirementStateQuiescing        = "quiescing"
	nodeRetirementStateDrainingHTLCs    = "draining_htlcs"
	nodeRetirementStateReadyForConfirm  = "ready_for_coop_confirmation"
	nodeRetirementStateClosingCoop      = "closing_coop"
	nodeRetirementStateAwaitingDecision = "awaiting_user_decision"
	nodeRetirementStateForceClosing     = "force_closing"
	nodeRetirementStateMonitoring       = "monitoring_onchain"
	nodeRetirementStateFailed           = "failed"
	nodeRetirementStateCanceled         = "canceled"
	nodeRetirementStateCompleted        = "completed"
	nodeRetirementStateDryRunCompleted  = "dry_run_completed"
)

const (
	nodeRetirementSourceManual     = "manual"
	nodeRetirementSourceSuccession = "succession"
)

const (
	nodeRetirementDecisionWait       = "wait"
	nodeRetirementDecisionForceClose = "force_close"
)

var (
	ErrNodeRetirementDBUnavailable    = errors.New("node retirement db unavailable")
	ErrNodeRetirementSessionNotFound  = errors.New("node retirement session not found")
	ErrNodeRetirementInvalidSource    = errors.New("invalid node retirement source")
	ErrNodeRetirementActiveSession    = errors.New("node retirement already has an active session")
	ErrNodeRetirementInvalidState     = errors.New("invalid node retirement state transition")
	ErrNodeRetirementInvalidDecision  = errors.New("invalid node retirement channel decision")
	ErrNodeRetirementChannelNotFound  = errors.New("node retirement channel not found")
	ErrNodeRetirementTransferNotFound = errors.New("node retirement transfer not found")
)

type NodeRetirementService struct {
	db        *pgxpool.Pool
	logger    *log.Logger
	lnd       *lndclient.Client
	rebalance *RebalanceService
	autofee   *AutofeeService
	mu        sync.Mutex
	started   bool
}

type NodeRetirementSession struct {
	ID                 int64           `json:"id"`
	SessionID          string          `json:"session_id"`
	Source             string          `json:"source"`
	DryRun             bool            `json:"dry_run"`
	State              string          `json:"state"`
	DisclaimerAccepted bool            `json:"disclaimer_accepted"`
	Snapshot           json.RawMessage `json:"snapshot,omitempty"`
	Config             json.RawMessage `json:"config,omitempty"`
	Reconciliation     json.RawMessage `json:"reconciliation,omitempty"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	CompletedAt        *time.Time      `json:"completed_at,omitempty"`
	LastError          string          `json:"last_error,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type NodeRetirementEvent struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"session_id"`
	EventType string          `json:"event_type"`
	Severity  string          `json:"severity"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type NodeRetirementChannel struct {
	ID           int64           `json:"id"`
	SessionID    string          `json:"session_id"`
	ChannelPoint string          `json:"channel_point"`
	ChannelID    int64           `json:"channel_id"`
	InitialState json.RawMessage `json:"initial_state,omitempty"`
	CurrentState json.RawMessage `json:"current_state,omitempty"`
	Decision     string          `json:"decision,omitempty"`
	CloseMode    string          `json:"close_mode,omitempty"`
	CloseTxid    string          `json:"close_txid,omitempty"`
	LastError    string          `json:"last_error,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type NodeRetirementTransfer struct {
	ID                 int64      `json:"id"`
	SessionID          string     `json:"session_id"`
	DestinationAddress string     `json:"destination_address"`
	SweepAll           bool       `json:"sweep_all"`
	SatPerVbyte        int64      `json:"sat_per_vbyte"`
	MinConfirmations   int        `json:"min_confirmations"`
	Status             string     `json:"status"`
	Txid               string     `json:"txid,omitempty"`
	AmountSat          int64      `json:"amount_sat"`
	Attempts           int        `json:"attempts"`
	LastError          string     `json:"last_error,omitempty"`
	LastAttemptAt      *time.Time `json:"last_attempt_at,omitempty"`
	Confirmations      int32      `json:"confirmations,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type NodeRetirementCreateParams struct {
	Source             string
	DryRun             bool
	DisclaimerAccepted bool
	Config             json.RawMessage
}

type NodeRetirementStatus struct {
	Available       bool   `json:"available"`
	Active          bool   `json:"active"`
	ActiveSessionID string `json:"active_session_id,omitempty"`
	ActiveState     string `json:"active_state,omitempty"`
}

func NewNodeRetirementService(db *pgxpool.Pool, logger *log.Logger, lnd *lndclient.Client, rebalance *RebalanceService, autofee *AutofeeService) *NodeRetirementService {
	return &NodeRetirementService{
		db:        db,
		logger:    logger,
		lnd:       lnd,
		rebalance: rebalance,
		autofee:   autofee,
	}
}

func (s *NodeRetirementService) AttachRuntime(lnd *lndclient.Client, rebalance *RebalanceService, autofee *AutofeeService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.lnd = lnd
	s.rebalance = rebalance
	s.autofee = autofee
	s.mu.Unlock()
}

func (s *NodeRetirementService) Start() {
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

func (s *NodeRetirementService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrNodeRetirementDBUnavailable
	}

	_, err := s.db.Exec(ctx, `
create table if not exists node_retirement_sessions (
  id bigserial primary key,
  session_id text not null unique,
  source text not null,
  dry_run boolean not null default false,
  state text not null,
  disclaimer_accepted boolean not null default false,
  snapshot_json jsonb not null default '{}'::jsonb,
  config_json jsonb not null default '{}'::jsonb,
  reconciliation_json jsonb not null default '{}'::jsonb,
  started_at timestamptz,
  completed_at timestamptz,
  last_error text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists node_retirement_sessions_state_idx on node_retirement_sessions (state, created_at desc);
create index if not exists node_retirement_sessions_created_idx on node_retirement_sessions (created_at desc);

create table if not exists node_retirement_channels (
  id bigserial primary key,
  session_id text not null references node_retirement_sessions(session_id) on delete cascade,
  channel_point text not null,
  channel_id bigint not null default 0,
  initial_state_json jsonb not null default '{}'::jsonb,
  current_state_json jsonb not null default '{}'::jsonb,
  decision text not null default '',
  close_mode text not null default '',
  close_txid text not null default '',
  last_error text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique(session_id, channel_point)
);

create index if not exists node_retirement_channels_session_idx on node_retirement_channels (session_id, updated_at desc);

create table if not exists node_retirement_events (
  id bigserial primary key,
  session_id text not null references node_retirement_sessions(session_id) on delete cascade,
  event_type text not null,
  severity text not null default 'info',
  payload_json jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create index if not exists node_retirement_events_session_idx on node_retirement_events (session_id, created_at desc);

create table if not exists node_retirement_transfers (
  id bigserial primary key,
  session_id text not null unique references node_retirement_sessions(session_id) on delete cascade,
  destination_address text not null default '',
  sweep_all boolean not null default true,
  sat_per_vbyte bigint not null default 0,
  min_confirmations integer not null default 3,
  status text not null default 'waiting',
  txid text not null default '',
  amount_sat bigint not null default 0,
  attempts integer not null default 0,
  last_error text not null default '',
  last_attempt_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists node_retirement_transfers_status_idx on node_retirement_transfers (status, updated_at desc);
`)
	return err
}

func (s *NodeRetirementService) Status(ctx context.Context) (NodeRetirementStatus, error) {
	if s == nil || s.db == nil {
		return NodeRetirementStatus{Available: false}, ErrNodeRetirementDBUnavailable
	}

	status := NodeRetirementStatus{Available: true}

	var sessionID string
	var state string
	err := s.db.QueryRow(ctx, `
select session_id, state
from node_retirement_sessions
where state not in ('completed', 'dry_run_completed', 'failed', 'canceled')
order by created_at desc
limit 1
`).Scan(&sessionID, &state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return status, nil
		}
		return status, err
	}

	status.Active = true
	status.ActiveSessionID = sessionID
	status.ActiveState = state
	return status, nil
}

func (s *NodeRetirementService) CreateSession(ctx context.Context, params NodeRetirementCreateParams) (NodeRetirementSession, error) {
	if s == nil || s.db == nil {
		return NodeRetirementSession{}, ErrNodeRetirementDBUnavailable
	}

	source := strings.TrimSpace(strings.ToLower(params.Source))
	if source == "" {
		source = nodeRetirementSourceManual
	}
	if source != nodeRetirementSourceManual && source != nodeRetirementSourceSuccession {
		return NodeRetirementSession{}, ErrNodeRetirementInvalidSource
	}

	configRaw := params.Config
	if len(configRaw) == 0 {
		configRaw = []byte(`{}`)
	}

	sessionID, err := newNodeRetirementSessionID()
	if err != nil {
		return NodeRetirementSession{}, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return NodeRetirementSession{}, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	var activeCount int
	if err := tx.QueryRow(ctx, `
select count(*)
from node_retirement_sessions
where state not in ('completed', 'dry_run_completed', 'failed', 'canceled')
`).Scan(&activeCount); err != nil {
		return NodeRetirementSession{}, err
	}
	if activeCount > 0 {
		return NodeRetirementSession{}, ErrNodeRetirementActiveSession
	}

	var session NodeRetirementSession
	if err := tx.QueryRow(ctx, `
insert into node_retirement_sessions (
  session_id, source, dry_run, state, disclaimer_accepted, config_json
) values ($1, $2, $3, $4, $5, $6::jsonb)
returning id, session_id, source, dry_run, state, disclaimer_accepted,
  snapshot_json::text, config_json::text, reconciliation_json::text,
  started_at, completed_at, last_error, created_at, updated_at
`, sessionID, source, params.DryRun, nodeRetirementStateCreated, params.DisclaimerAccepted, string(configRaw)).
		Scan(
			&session.ID,
			&session.SessionID,
			&session.Source,
			&session.DryRun,
			&session.State,
			&session.DisclaimerAccepted,
			&session.Snapshot,
			&session.Config,
			&session.Reconciliation,
			scanOptionalTime(&session.StartedAt),
			scanOptionalTime(&session.CompletedAt),
			&session.LastError,
			&session.CreatedAt,
			&session.UpdatedAt,
		); err != nil {
		return NodeRetirementSession{}, err
	}

	payload := map[string]any{
		"source":              session.Source,
		"dry_run":             session.DryRun,
		"disclaimer_accepted": session.DisclaimerAccepted,
	}
	if err := s.insertEventTx(ctx, tx, session.SessionID, "session_created", "info", payload); err != nil {
		return NodeRetirementSession{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return NodeRetirementSession{}, err
	}
	tx = nil
	return session, nil
}

func (s *NodeRetirementService) GetSession(ctx context.Context, sessionID string) (NodeRetirementSession, error) {
	if s == nil || s.db == nil {
		return NodeRetirementSession{}, ErrNodeRetirementDBUnavailable
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return NodeRetirementSession{}, ErrNodeRetirementSessionNotFound
	}

	var session NodeRetirementSession
	err := s.db.QueryRow(ctx, `
select id, session_id, source, dry_run, state, disclaimer_accepted,
  snapshot_json::text, config_json::text, reconciliation_json::text,
  started_at, completed_at, last_error, created_at, updated_at
from node_retirement_sessions
where session_id = $1
`, trimmed).Scan(
		&session.ID,
		&session.SessionID,
		&session.Source,
		&session.DryRun,
		&session.State,
		&session.DisclaimerAccepted,
		&session.Snapshot,
		&session.Config,
		&session.Reconciliation,
		scanOptionalTime(&session.StartedAt),
		scanOptionalTime(&session.CompletedAt),
		&session.LastError,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return NodeRetirementSession{}, ErrNodeRetirementSessionNotFound
		}
		return NodeRetirementSession{}, err
	}
	return session, nil
}

func (s *NodeRetirementService) ListSessions(ctx context.Context, limit int) ([]NodeRetirementSession, error) {
	if s == nil || s.db == nil {
		return nil, ErrNodeRetirementDBUnavailable
	}
	if limit <= 0 {
		limit = nodeRetirementDefaultListLimit
	}
	if limit > nodeRetirementMaxListLimit {
		limit = nodeRetirementMaxListLimit
	}

	rows, err := s.db.Query(ctx, `
select id, session_id, source, dry_run, state, disclaimer_accepted,
  snapshot_json::text, config_json::text, reconciliation_json::text,
  started_at, completed_at, last_error, created_at, updated_at
from node_retirement_sessions
order by created_at desc
limit $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]NodeRetirementSession, 0)
	for rows.Next() {
		var item NodeRetirementSession
		if err := rows.Scan(
			&item.ID,
			&item.SessionID,
			&item.Source,
			&item.DryRun,
			&item.State,
			&item.DisclaimerAccepted,
			&item.Snapshot,
			&item.Config,
			&item.Reconciliation,
			scanOptionalTime(&item.StartedAt),
			scanOptionalTime(&item.CompletedAt),
			&item.LastError,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *NodeRetirementService) ListSessionEvents(ctx context.Context, sessionID string, limit int) ([]NodeRetirementEvent, error) {
	if s == nil || s.db == nil {
		return nil, ErrNodeRetirementDBUnavailable
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil, ErrNodeRetirementSessionNotFound
	}
	if limit <= 0 {
		limit = nodeRetirementDefaultEventLimit
	}
	if limit > nodeRetirementMaxEventLimit {
		limit = nodeRetirementMaxEventLimit
	}

	if _, err := s.GetSession(ctx, trimmed); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, `
select id, session_id, event_type, severity, payload_json::text, created_at
from node_retirement_events
where session_id = $1
order by created_at desc
limit $2
`, trimmed, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]NodeRetirementEvent, 0)
	for rows.Next() {
		var item NodeRetirementEvent
		if err := rows.Scan(&item.ID, &item.SessionID, &item.EventType, &item.Severity, &item.Payload, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *NodeRetirementService) ListSessionChannels(ctx context.Context, sessionID string) ([]NodeRetirementChannel, error) {
	if s == nil || s.db == nil {
		return nil, ErrNodeRetirementDBUnavailable
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return nil, ErrNodeRetirementSessionNotFound
	}
	if _, err := s.GetSession(ctx, trimmed); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, `
select id, session_id, channel_point, channel_id, initial_state_json::text, current_state_json::text,
  decision, close_mode, close_txid, last_error, created_at, updated_at
from node_retirement_channels
where session_id = $1
order by created_at asc, id asc
`, trimmed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]NodeRetirementChannel, 0, 16)
	for rows.Next() {
		var item NodeRetirementChannel
		if err := rows.Scan(
			&item.ID,
			&item.SessionID,
			&item.ChannelPoint,
			&item.ChannelID,
			&item.InitialState,
			&item.CurrentState,
			&item.Decision,
			&item.CloseMode,
			&item.CloseTxid,
			&item.LastError,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *NodeRetirementService) ConfirmCoopClose(ctx context.Context, sessionID string) (NodeRetirementSession, error) {
	if s == nil || s.db == nil {
		return NodeRetirementSession{}, ErrNodeRetirementDBUnavailable
	}
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return NodeRetirementSession{}, err
	}
	if session.State != nodeRetirementStateReadyForConfirm {
		return NodeRetirementSession{}, ErrNodeRetirementInvalidState
	}
	if err := s.updateSessionState(ctx, session.SessionID, nodeRetirementStateClosingCoop, ""); err != nil {
		return NodeRetirementSession{}, err
	}
	_ = s.insertEvent(ctx, session.SessionID, "coop_close_confirmed", "info", map[string]any{"source": "ui"})
	return s.GetSession(ctx, session.SessionID)
}

func (s *NodeRetirementService) SetChannelDecision(ctx context.Context, sessionID string, channelPoint string, decision string) error {
	if s == nil || s.db == nil {
		return ErrNodeRetirementDBUnavailable
	}
	normalizedDecision := strings.TrimSpace(strings.ToLower(decision))
	if normalizedDecision != nodeRetirementDecisionWait && normalizedDecision != nodeRetirementDecisionForceClose {
		return ErrNodeRetirementInvalidDecision
	}

	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.State != nodeRetirementStateAwaitingDecision && session.State != nodeRetirementStateForceClosing {
		return ErrNodeRetirementInvalidState
	}

	trimmedPoint := strings.TrimSpace(channelPoint)
	if trimmedPoint == "" {
		return ErrNodeRetirementChannelNotFound
	}

	tag, tagErr := s.channelExists(ctx, session.SessionID, trimmedPoint)
	if tagErr != nil {
		return tagErr
	}
	if !tag {
		return ErrNodeRetirementChannelNotFound
	}

	_, err = s.db.Exec(ctx, `
update node_retirement_channels
set decision = $3,
  updated_at = now()
where session_id = $1 and channel_point = $2
`, session.SessionID, trimmedPoint, normalizedDecision)
	if err != nil {
		return err
	}
	_ = s.insertEvent(ctx, session.SessionID, "channel_decision", "info", map[string]any{
		"channel_point": trimmedPoint,
		"decision":      normalizedDecision,
	})
	if normalizedDecision == nodeRetirementDecisionForceClose {
		_ = s.updateSessionState(ctx, session.SessionID, nodeRetirementStateForceClosing, "")
	}
	return nil
}

func (s *NodeRetirementService) GetSessionTransfer(ctx context.Context, sessionID string) (NodeRetirementTransfer, error) {
	if s == nil || s.db == nil {
		return NodeRetirementTransfer{}, ErrNodeRetirementDBUnavailable
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return NodeRetirementTransfer{}, ErrNodeRetirementSessionNotFound
	}
	if _, err := s.GetSession(ctx, trimmed); err != nil {
		return NodeRetirementTransfer{}, err
	}

	var item NodeRetirementTransfer
	err := s.db.QueryRow(ctx, `
select id, session_id, destination_address, sweep_all, sat_per_vbyte, min_confirmations,
  status, txid, amount_sat, attempts, last_error, last_attempt_at, created_at, updated_at
from node_retirement_transfers
where session_id = $1
`, trimmed).Scan(
		&item.ID,
		&item.SessionID,
		&item.DestinationAddress,
		&item.SweepAll,
		&item.SatPerVbyte,
		&item.MinConfirmations,
		&item.Status,
		&item.Txid,
		&item.AmountSat,
		&item.Attempts,
		&item.LastError,
		scanOptionalTime(&item.LastAttemptAt),
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return NodeRetirementTransfer{}, ErrNodeRetirementTransferNotFound
		}
		return NodeRetirementTransfer{}, err
	}

	if strings.TrimSpace(item.Txid) != "" && s.lnd != nil {
		txs, txErr := s.lnd.ListOnchainTransactions(ctx, 400)
		if txErr == nil {
			target := strings.ToLower(strings.TrimSpace(item.Txid))
			for _, tx := range txs {
				if strings.ToLower(strings.TrimSpace(tx.Txid)) != target {
					continue
				}
				item.Confirmations = tx.Confirmations
				break
			}
		}
	}
	return item, nil
}

func (s *NodeRetirementService) channelExists(ctx context.Context, sessionID string, channelPoint string) (bool, error) {
	var count int
	if err := s.db.QueryRow(ctx, `
select count(*)
from node_retirement_channels
where session_id = $1 and channel_point = $2
`, sessionID, channelPoint).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *NodeRetirementService) insertEvent(ctx context.Context, sessionID string, eventType string, severity string, payload any) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	if err := s.insertEventTx(ctx, tx, sessionID, eventType, severity, payload); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *NodeRetirementService) insertEventTx(ctx context.Context, tx pgx.Tx, sessionID string, eventType string, severity string, payload any) error {
	raw := []byte(`{}`)
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = encoded
	}

	_, err := tx.Exec(ctx, `
insert into node_retirement_events (session_id, event_type, severity, payload_json)
values ($1, $2, $3, $4::jsonb)
`, sessionID, strings.TrimSpace(eventType), strings.TrimSpace(severity), string(raw))
	return err
}

func newNodeRetirementSessionID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("nr_%s_%s", time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(buf)), nil
}

func scanOptionalTime(dest **time.Time) any {
	return &optionalTimeScanner{dest: dest}
}

type optionalTimeScanner struct {
	dest **time.Time
}

func (n *optionalTimeScanner) Scan(src any) error {
	if n == nil || n.dest == nil {
		return nil
	}
	var value sql.NullTime
	if err := value.Scan(src); err != nil {
		return err
	}
	if !value.Valid {
		*n.dest = nil
		return nil
	}
	t := value.Time.UTC()
	*n.dest = &t
	return nil
}
