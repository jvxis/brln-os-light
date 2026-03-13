package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	closeManagerPollInterval       = 45 * time.Second
	closeManagerRefreshMinAge      = 15 * time.Second
	closeManagerDefaultListLimit   = 100
	closeManagerMaxListLimit       = 500
	closeManagerDefaultEventLimit  = 100
	closeManagerMaxEventLimit      = 500
	closeManagerTerminalRecentDays = 7
)

const (
	closeManagerStateCoopRequested         = "coop_requested"
	closeManagerStateCoopBlockedByHTLCs    = "coop_blocked_by_htlcs"
	closeManagerStateWaitingCloseNoTxid    = "waiting_close_no_txid"
	closeManagerStateClosingTxSeen         = "closing_tx_seen_unconfirmed"
	closeManagerStateForceCloseRequested   = "force_close_requested"
	closeManagerStateForceCloseActive      = "force_close_active"
	closeManagerStateOutputsTimelocked     = "outputs_timelocked"
	closeManagerStateSweepPending          = "sweep_pending"
	closeManagerStateSweepStuck            = "sweep_stuck"
	closeManagerStateFundsRecovered        = "funds_recovered"
	closeManagerStateClosedTerminal        = "closed_terminal"
	closeManagerStateFailedManualAttention = "failed_manual_attention"
)

type CloseManagerSession struct {
	ID                            int64      `json:"id"`
	ChannelPoint                  string     `json:"channel_point"`
	ChannelID                     int64      `json:"channel_id"`
	PeerPubkey                    string     `json:"peer_pubkey,omitempty"`
	PeerAlias                     string     `json:"peer_alias,omitempty"`
	Source                        string     `json:"source"`
	SourceRef                     string     `json:"source_ref,omitempty"`
	State                         string     `json:"state"`
	ActionRequired                string     `json:"action_required,omitempty"`
	ActionRecommended             string     `json:"action_recommended,omitempty"`
	Decision                      string     `json:"decision,omitempty"`
	RiskLevel                     string     `json:"risk_level,omitempty"`
	CloseMode                     string     `json:"close_mode,omitempty"`
	CloseTxid                     string     `json:"close_txid,omitempty"`
	CloseTxHexAvailable           bool       `json:"close_tx_hex_available"`
	SweepTxid                     string     `json:"sweep_txid,omitempty"`
	LimboBalanceSat               int64      `json:"limbo_balance_sat"`
	PendingHtlcCount              int        `json:"pending_htlc_count"`
	PendingHtlcFirstSeenAt        *time.Time `json:"pending_htlc_first_seen_at,omitempty"`
	PendingHtlcAgeSec             int64      `json:"pending_htlc_age_sec"`
	BlocksTilMaturity             *int32     `json:"blocks_til_maturity,omitempty"`
	MaturityETAAt                 *time.Time `json:"maturity_eta_at,omitempty"`
	SweepPendingCount             int        `json:"sweep_pending_count"`
	SweepBroadcastAttempts        int        `json:"sweep_broadcast_attempts"`
	SweepRequestedFeeRateSatVB    int64      `json:"sweep_requested_fee_rate_sat_vb"`
	SweepFeeRateSatVB             int64      `json:"sweep_fee_rate_sat_vb"`
	MempoolTargetSatVB            int64      `json:"mempool_target_sat_vb"`
	LastError                     string     `json:"last_error,omitempty"`
	WaitingCloseAttempts          int        `json:"waiting_close_attempts"`
	WaitingCloseLastAttemptAt     *time.Time `json:"waiting_close_last_attempt_at,omitempty"`
	WaitingCloseLastResult        string     `json:"waiting_close_last_result,omitempty"`
	WaitingCloseLastError         string     `json:"waiting_close_last_error,omitempty"`
	WaitingCloseLastRecoveredTxid string     `json:"waiting_close_last_recovered_txid,omitempty"`
	WaitingCloseSuggestForceClose bool       `json:"waiting_close_suggest_force_close"`
	LastProgressAt                *time.Time `json:"last_progress_at,omitempty"`
	CreatedAt                     time.Time  `json:"created_at"`
	UpdatedAt                     time.Time  `json:"updated_at"`
	ClosedAt                      *time.Time `json:"closed_at,omitempty"`
}

type CloseManagerEvent struct {
	ID        int64           `json:"id"`
	SessionID int64           `json:"session_id"`
	EventType string          `json:"event_type"`
	Severity  string          `json:"severity"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type CloseManagerStatus struct {
	Available           bool           `json:"available"`
	LastSyncAt          *time.Time     `json:"last_sync_at,omitempty"`
	ActiveCount         int            `json:"active_count"`
	ActionRequiredCount int            `json:"action_required_count"`
	WaitingCloseCount   int            `json:"waiting_close_count"`
	HTLCBlockedCount    int            `json:"htlc_blocked_count"`
	NodeRetirementCount int            `json:"node_retirement_count"`
	StateCounts         map[string]int `json:"state_counts"`
}

type closeManagerSourceRef struct {
	source    string
	sourceRef string
}

type closeManagerSweepStatus struct {
	PendingCount          int
	BroadcastAttempts     int
	RequestedFeeRateSatVB int64
	FeeRateSatVB          int64
	MempoolTargetSatVB    int64
	SweepTxSeen           bool
	Stuck                 bool
}

type CloseManagerService struct {
	db         *pgxpool.Pool
	logger     *log.Logger
	lnd        *lndclient.Client
	notifier   *Notifier
	mu         sync.Mutex
	started    bool
	lastSyncAt time.Time
}

var ErrCloseManagerDBUnavailable = errors.New("close manager db unavailable")

func NewCloseManagerService(db *pgxpool.Pool, logger *log.Logger, lnd *lndclient.Client, notifier *Notifier) *CloseManagerService {
	return &CloseManagerService{
		db:       db,
		logger:   logger,
		lnd:      lnd,
		notifier: notifier,
	}
}

func (s *CloseManagerService) Start() {
	if s == nil || s.db == nil || s.lnd == nil {
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

func (s *CloseManagerService) runLoop() {
	ticker := time.NewTicker(closeManagerPollInterval)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		if err := s.RefreshNow(ctx); err != nil && s.logger != nil {
			s.logger.Printf("close manager refresh failed: %v", err)
		}
		cancel()
		<-ticker.C
	}
}

func (s *CloseManagerService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrCloseManagerDBUnavailable
	}
	_, err := s.db.Exec(ctx, `
create table if not exists close_sessions (
  id bigserial primary key,
  channel_point text not null unique,
  channel_id bigint not null default 0,
  peer_pubkey text not null default '',
  peer_alias text not null default '',
  source text not null default 'lightning_ops',
  source_ref text not null default '',
  state text not null,
  action_required text not null default '',
  action_recommended text not null default '',
  decision text not null default '',
  risk_level text not null default 'info',
  close_mode text not null default '',
  close_txid text not null default '',
  close_tx_hex_available boolean not null default false,
  sweep_txid text not null default '',
  limbo_balance_sat bigint not null default 0,
  pending_htlc_count integer not null default 0,
  pending_htlc_first_seen_at timestamptz,
  pending_htlc_age_sec bigint not null default 0,
  blocks_til_maturity integer,
  maturity_eta_at timestamptz,
  sweep_pending_count integer not null default 0,
  sweep_broadcast_attempts integer not null default 0,
  sweep_requested_fee_rate_sat_vb bigint not null default 0,
  sweep_fee_rate_sat_vb bigint not null default 0,
  mempool_target_sat_vb bigint not null default 0,
  last_error text not null default '',
  waiting_close_attempts integer not null default 0,
  waiting_close_last_attempt_at timestamptz,
  waiting_close_last_result text not null default '',
  waiting_close_last_error text not null default '',
  waiting_close_last_recovered_txid text not null default '',
  waiting_close_suggest_force_close boolean not null default false,
  last_progress_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  closed_at timestamptz
);

create index if not exists close_sessions_state_updated_idx on close_sessions (state, updated_at desc);
create index if not exists close_sessions_source_updated_idx on close_sessions (source, updated_at desc);

create table if not exists close_events (
  id bigserial primary key,
  session_id bigint not null references close_sessions(id) on delete cascade,
  event_type text not null,
  severity text not null default 'info',
  payload_json jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create index if not exists close_events_session_created_idx on close_events (session_id, created_at desc);

alter table close_sessions add column if not exists sweep_pending_count integer not null default 0;
alter table close_sessions add column if not exists sweep_broadcast_attempts integer not null default 0;
alter table close_sessions add column if not exists sweep_requested_fee_rate_sat_vb bigint not null default 0;
`)
	return err
}

func (s *CloseManagerService) RefreshIfStale(ctx context.Context, maxAge time.Duration) error {
	s.mu.Lock()
	lastSyncAt := s.lastSyncAt
	s.mu.Unlock()
	if !lastSyncAt.IsZero() && time.Since(lastSyncAt) < maxAge {
		return nil
	}
	return s.RefreshNow(ctx)
}

func (s *CloseManagerService) RefreshNow(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrCloseManagerDBUnavailable
	}
	if s.lnd == nil {
		return errors.New("close manager lnd unavailable")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	openChannels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}
	pendingChannels, err := s.lnd.ListPendingChannels(ctx)
	if err != nil {
		return err
	}
	closedChannels, err := s.lnd.ListClosedChannels(ctx)
	if err != nil {
		return err
	}
	pendingSweeps, err := s.lnd.ListPendingSweeps(ctx)
	if err != nil {
		return err
	}
	sweeps, err := s.lnd.ListSweeps(ctx)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	existingByPoint, err := s.loadSessionsByPoint(ctx)
	if err != nil {
		return err
	}
	nodeRetirementRefs, _ := s.loadNodeRetirementRefs(ctx)
	mempoolTargetSatVB := int64(0)
	feeCtx, feeCancel := context.WithTimeout(ctx, 4*time.Second)
	var fees mempoolFeeRecommendation
	if err := fetchMempoolJSON(feeCtx, "https://mempool.space/api/v1/fees/recommended", &fees); err == nil {
		switch {
		case fees.HourFee > 0:
			mempoolTargetSatVB = int64(fees.HourFee)
		case fees.HalfHourFee > 0:
			mempoolTargetSatVB = int64(fees.HalfHourFee)
		case fees.FastestFee > 0:
			mempoolTargetSatVB = int64(fees.FastestFee)
		case fees.EconomyFee > 0:
			mempoolTargetSatVB = int64(fees.EconomyFee)
		case fees.MinimumFee > 0:
			mempoolTargetSatVB = int64(fees.MinimumFee)
		}
	}
	feeCancel()
	sweepTxids := make(map[string]struct{}, len(sweeps))
	for _, item := range sweeps {
		txid := strings.ToLower(strings.TrimSpace(item.Txid))
		if txid == "" {
			continue
		}
		sweepTxids[txid] = struct{}{}
	}
	pendingSweepsBySourceTxid := make(map[string][]lndclient.PendingSweepInfo)
	for _, item := range pendingSweeps {
		txid := strings.ToLower(strings.TrimSpace(item.Txid))
		if txid == "" {
			continue
		}
		pendingSweepsBySourceTxid[txid] = append(pendingSweepsBySourceTxid[txid], item)
	}

	openByPoint := make(map[string]lndclient.ChannelInfo, len(openChannels))
	for _, ch := range openChannels {
		point := strings.TrimSpace(ch.ChannelPoint)
		if point == "" {
			continue
		}
		openByPoint[point] = ch
	}

	closedByPoint := make(map[string]lndclient.ClosedChannelInfo, len(closedChannels))
	for _, ch := range closedChannels {
		point := strings.TrimSpace(ch.ChannelPoint)
		if point == "" {
			continue
		}
		closedByPoint[point] = ch
	}

	seen := make(map[string]struct{}, len(pendingChannels)+len(closedChannels))
	for _, item := range pendingChannels {
		point := strings.TrimSpace(item.ChannelPoint)
		if point == "" {
			continue
		}
		if item.Status != "closing" && item.Status != "force_closing" && item.Status != "waiting_close" {
			continue
		}
		prev := existingByPoint[point]
		live := openByPoint[point]
		src := nodeRetirementRefs[point]
		sweepStatus := deriveCloseManagerSweepStatus(strings.TrimSpace(item.ClosingTxid), prev.SweepTxid, pendingSweepsBySourceTxid, sweepTxids, mempoolTargetSatVB)
		session := s.derivePendingSession(now, item, live, prev, src, sweepStatus)
		if err := s.upsertSession(ctx, prev, session); err != nil {
			return err
		}
		seen[point] = struct{}{}
	}

	for point, closed := range closedByPoint {
		prev := existingByPoint[point]
		if prev.ID == 0 && closed.TimeLockedBalanceSat <= 0 {
			continue
		}
		src := nodeRetirementRefs[point]
		baseSweepTxid := deriveClosedSessionSweepTxid(closed)
		sweepStatus := deriveCloseManagerSweepStatus(strings.TrimSpace(closed.ClosingTxHash), firstNonEmpty(baseSweepTxid, prev.SweepTxid), pendingSweepsBySourceTxid, sweepTxids, mempoolTargetSatVB)
		session := s.deriveClosedSession(now, closed, prev, src, sweepStatus)
		if err := s.upsertSession(ctx, prev, session); err != nil {
			return err
		}
		seen[point] = struct{}{}
	}

	for point, prev := range existingByPoint {
		if _, ok := seen[point]; ok {
			continue
		}
		if isCloseManagerTerminalState(prev.State) {
			continue
		}
		if closed, ok := closedByPoint[point]; ok {
			baseSweepTxid := deriveClosedSessionSweepTxid(closed)
			sweepStatus := deriveCloseManagerSweepStatus(strings.TrimSpace(closed.ClosingTxHash), firstNonEmpty(baseSweepTxid, prev.SweepTxid), pendingSweepsBySourceTxid, sweepTxids, mempoolTargetSatVB)
			session := s.deriveClosedSession(now, closed, prev, nodeRetirementRefs[point], sweepStatus)
			if err := s.upsertSession(ctx, prev, session); err != nil {
				return err
			}
		}
	}

	s.lastSyncAt = now
	return nil
}

func (s *CloseManagerService) GetStatus(ctx context.Context) (CloseManagerStatus, error) {
	if s == nil || s.db == nil {
		return CloseManagerStatus{}, ErrCloseManagerDBUnavailable
	}
	rows, err := s.db.Query(ctx, `
select state, source, action_required
from close_sessions
where closed_at is null
   or updated_at >= now() - ($1::text)::interval
`, fmt.Sprintf("%d day", closeManagerTerminalRecentDays))
	if err != nil {
		return CloseManagerStatus{}, err
	}
	defer rows.Close()

	status := CloseManagerStatus{
		Available:   true,
		LastSyncAt:  s.lastSyncAtPtr(),
		StateCounts: map[string]int{},
	}
	for rows.Next() {
		var state string
		var source string
		var actionRequired string
		if err := rows.Scan(&state, &source, &actionRequired); err != nil {
			return CloseManagerStatus{}, err
		}
		status.StateCounts[state]++
		if !isCloseManagerTerminalState(state) {
			status.ActiveCount++
		}
		if strings.TrimSpace(actionRequired) != "" {
			status.ActionRequiredCount++
		}
		if state == closeManagerStateWaitingCloseNoTxid {
			status.WaitingCloseCount++
		}
		if state == closeManagerStateCoopBlockedByHTLCs {
			status.HTLCBlockedCount++
		}
		if source == "node_retirement" {
			status.NodeRetirementCount++
		}
	}
	if err := rows.Err(); err != nil {
		return CloseManagerStatus{}, err
	}
	return status, nil
}

func (s *CloseManagerService) ListSessions(ctx context.Context, limit int) ([]CloseManagerSession, error) {
	if s == nil || s.db == nil {
		return nil, ErrCloseManagerDBUnavailable
	}
	limit = normalizeCloseManagerListLimit(limit)
	rows, err := s.db.Query(ctx, `
select
  id, channel_point, channel_id, peer_pubkey, peer_alias, source, source_ref, state,
  action_required, action_recommended, decision, risk_level, close_mode, close_txid,
  close_tx_hex_available, sweep_txid, limbo_balance_sat, pending_htlc_count,
  pending_htlc_first_seen_at, pending_htlc_age_sec, blocks_til_maturity, maturity_eta_at,
  sweep_pending_count, sweep_broadcast_attempts, sweep_requested_fee_rate_sat_vb,
  sweep_fee_rate_sat_vb, mempool_target_sat_vb, last_error, waiting_close_attempts,
  waiting_close_last_attempt_at, waiting_close_last_result, waiting_close_last_error,
  waiting_close_last_recovered_txid, waiting_close_suggest_force_close, last_progress_at,
  created_at, updated_at, closed_at
from close_sessions
where closed_at is null
   or updated_at >= now() - ($2::text)::interval
order by
  case when closed_at is null then 0 else 1 end,
  updated_at desc
limit $1
`, limit, fmt.Sprintf("%d day", closeManagerTerminalRecentDays))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCloseManagerSessions(rows)
}

func (s *CloseManagerService) GetSession(ctx context.Context, id int64) (*CloseManagerSession, error) {
	if s == nil || s.db == nil {
		return nil, ErrCloseManagerDBUnavailable
	}
	rows, err := s.db.Query(ctx, `
select
  id, channel_point, channel_id, peer_pubkey, peer_alias, source, source_ref, state,
  action_required, action_recommended, decision, risk_level, close_mode, close_txid,
  close_tx_hex_available, sweep_txid, limbo_balance_sat, pending_htlc_count,
  pending_htlc_first_seen_at, pending_htlc_age_sec, blocks_til_maturity, maturity_eta_at,
  sweep_pending_count, sweep_broadcast_attempts, sweep_requested_fee_rate_sat_vb,
  sweep_fee_rate_sat_vb, mempool_target_sat_vb, last_error, waiting_close_attempts,
  waiting_close_last_attempt_at, waiting_close_last_result, waiting_close_last_error,
  waiting_close_last_recovered_txid, waiting_close_suggest_force_close, last_progress_at,
  created_at, updated_at, closed_at
from close_sessions
where id = $1
limit 1
`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanCloseManagerSessions(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, sql.ErrNoRows
	}
	return &items[0], nil
}

func (s *CloseManagerService) ListEvents(ctx context.Context, sessionID int64, limit int) ([]CloseManagerEvent, error) {
	if s == nil || s.db == nil {
		return nil, ErrCloseManagerDBUnavailable
	}
	limit = normalizeCloseManagerEventLimit(limit)
	rows, err := s.db.Query(ctx, `
select id, session_id, event_type, severity, payload_json, created_at
from close_events
where session_id = $1
order by created_at desc
limit $2
`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]CloseManagerEvent, 0, limit)
	for rows.Next() {
		var item CloseManagerEvent
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

func (s *CloseManagerService) lastSyncAtPtr() *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSyncAt.IsZero() {
		return nil
	}
	value := s.lastSyncAt
	return &value
}

func (s *CloseManagerService) loadSessionsByPoint(ctx context.Context) (map[string]CloseManagerSession, error) {
	rows, err := s.db.Query(ctx, `
select
  id, channel_point, channel_id, peer_pubkey, peer_alias, source, source_ref, state,
  action_required, action_recommended, decision, risk_level, close_mode, close_txid,
  close_tx_hex_available, sweep_txid, limbo_balance_sat, pending_htlc_count,
  pending_htlc_first_seen_at, pending_htlc_age_sec, blocks_til_maturity, maturity_eta_at,
  sweep_pending_count, sweep_broadcast_attempts, sweep_requested_fee_rate_sat_vb,
  sweep_fee_rate_sat_vb, mempool_target_sat_vb, last_error, waiting_close_attempts,
  waiting_close_last_attempt_at, waiting_close_last_result, waiting_close_last_error,
  waiting_close_last_recovered_txid, waiting_close_suggest_force_close, last_progress_at,
  created_at, updated_at, closed_at
from close_sessions
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanCloseManagerSessions(rows)
	if err != nil {
		return nil, err
	}
	byPoint := make(map[string]CloseManagerSession, len(items))
	for _, item := range items {
		point := strings.TrimSpace(item.ChannelPoint)
		if point == "" {
			continue
		}
		byPoint[point] = item
	}
	return byPoint, nil
}

func (s *CloseManagerService) loadNodeRetirementRefs(ctx context.Context) (map[string]closeManagerSourceRef, error) {
	rows, err := s.db.Query(ctx, `
select c.channel_point, c.session_id
from node_retirement_channels c
join node_retirement_sessions sess on sess.session_id = c.session_id
where sess.state not in ('completed', 'failed', 'canceled', 'dry_run_completed')
`)
	if err != nil {
		return map[string]closeManagerSourceRef{}, err
	}
	defer rows.Close()
	refs := make(map[string]closeManagerSourceRef)
	for rows.Next() {
		var channelPoint string
		var sessionID string
		if err := rows.Scan(&channelPoint, &sessionID); err != nil {
			return map[string]closeManagerSourceRef{}, err
		}
		refs[strings.TrimSpace(channelPoint)] = closeManagerSourceRef{
			source:    "node_retirement",
			sourceRef: strings.TrimSpace(sessionID),
		}
	}
	return refs, rows.Err()
}

func (s *CloseManagerService) derivePendingSession(now time.Time, item lndclient.PendingChannelInfo, live lndclient.ChannelInfo, prev CloseManagerSession, src closeManagerSourceRef, sweep closeManagerSweepStatus) CloseManagerSession {
	point := strings.TrimSpace(item.ChannelPoint)
	closeTxid := strings.TrimSpace(item.ClosingTxid)
	var waitingInfo *waitingCloseRecoveryResponse
	if s.notifier != nil {
		if info, ok := s.notifier.getWaitingCloseRecoveryInfo(point); ok {
			waitingInfo = buildWaitingCloseRecoveryResponse(info)
		}
	}

	pendingHtlcCount := live.PendingHtlcCount
	var firstSeen *time.Time
	if pendingHtlcCount > 0 {
		if prev.PendingHtlcFirstSeenAt != nil {
			value := *prev.PendingHtlcFirstSeenAt
			firstSeen = &value
		} else {
			value := now
			firstSeen = &value
		}
	}
	pendingAgeSec := int64(0)
	if firstSeen != nil {
		pendingAgeSec = int64(now.Sub(*firstSeen).Seconds())
		if pendingAgeSec < 0 {
			pendingAgeSec = 0
		}
	}

	state := deriveCloseManagerPendingState(item.Status, closeTxid, pendingHtlcCount)
	actionRequired := ""
	actionRecommended := "monitor"
	riskLevel := "info"
	lastError := ""

	switch state {
	case closeManagerStateCoopBlockedByHTLCs:
		actionRecommended = "wait"
		riskLevel = "warn"
	case closeManagerStateWaitingCloseNoTxid:
		riskLevel = "warn"
		if waitingInfo != nil && waitingInfo.SuggestForceClose {
			actionRequired = "force_close"
			actionRecommended = "force_close"
		} else {
			actionRecommended = "recover_or_monitor"
		}
		if waitingInfo != nil {
			lastError = strings.TrimSpace(waitingInfo.LastError)
		}
	case closeManagerStateForceCloseActive, closeManagerStateOutputsTimelocked:
		actionRecommended = "wait_maturity"
		riskLevel = "warn"
	}
	if state == closeManagerStateForceCloseActive || state == closeManagerStateOutputsTimelocked {
		if sweep.PendingCount > 0 {
			state = closeManagerStateSweepPending
			actionRecommended = "monitor"
			riskLevel = "warn"
		}
		if sweep.Stuck {
			state = closeManagerStateSweepStuck
			actionRecommended = "review_sweep"
			riskLevel = "error"
			if lastError == "" {
				lastError = deriveCloseManagerSweepReason(sweep)
			}
		}
	}

	session := CloseManagerSession{
		ID:                            prev.ID,
		ChannelPoint:                  point,
		ChannelID:                     int64(live.ChannelID),
		PeerPubkey:                    firstNonEmpty(item.RemotePubkey, live.RemotePubkey, prev.PeerPubkey),
		PeerAlias:                     firstNonEmpty(item.PeerAlias, live.PeerAlias, prev.PeerAlias),
		Source:                        sourceOrDefault(src, prev.Source),
		SourceRef:                     sourceRefOrDefault(src, prev.SourceRef),
		State:                         state,
		ActionRequired:                actionRequired,
		ActionRecommended:             actionRecommended,
		Decision:                      prev.Decision,
		RiskLevel:                     riskLevel,
		CloseMode:                     closeManagerModeFromPendingStatus(item.Status),
		CloseTxid:                     closeTxid,
		CloseTxHexAvailable:           false,
		SweepTxid:                     prev.SweepTxid,
		LimboBalanceSat:               item.LimboBalance,
		PendingHtlcCount:              pendingHtlcCount,
		PendingHtlcFirstSeenAt:        firstSeen,
		PendingHtlcAgeSec:             pendingAgeSec,
		SweepPendingCount:             sweep.PendingCount,
		SweepBroadcastAttempts:        sweep.BroadcastAttempts,
		SweepRequestedFeeRateSatVB:    sweep.RequestedFeeRateSatVB,
		SweepFeeRateSatVB:             prev.SweepFeeRateSatVB,
		MempoolTargetSatVB:            prev.MempoolTargetSatVB,
		LastError:                     firstNonEmpty(lastError, prev.LastError),
		WaitingCloseAttempts:          0,
		WaitingCloseLastResult:        "",
		WaitingCloseLastError:         "",
		WaitingCloseLastRecoveredTxid: "",
		WaitingCloseSuggestForceClose: false,
		CreatedAt:                     prev.CreatedAt,
		UpdatedAt:                     now,
	}
	if sweep.FeeRateSatVB > 0 {
		session.SweepFeeRateSatVB = sweep.FeeRateSatVB
	}
	if sweep.MempoolTargetSatVB > 0 {
		session.MempoolTargetSatVB = sweep.MempoolTargetSatVB
	}
	if item.BlocksTilMaturity > 0 {
		blocks := item.BlocksTilMaturity
		session.BlocksTilMaturity = &blocks
		eta := now.Add(time.Duration(blocks) * 10 * time.Minute)
		session.MaturityETAAt = &eta
	}
	if waitingInfo != nil {
		session.WaitingCloseAttempts = waitingInfo.Attempts
		session.WaitingCloseLastResult = strings.TrimSpace(waitingInfo.LastResult)
		session.WaitingCloseLastError = strings.TrimSpace(waitingInfo.LastError)
		session.WaitingCloseLastRecoveredTxid = strings.TrimSpace(waitingInfo.LastRecoveredTxid)
		session.WaitingCloseSuggestForceClose = waitingInfo.SuggestForceClose
	}
	if prev.WaitingCloseLastAttemptAt != nil {
		value := *prev.WaitingCloseLastAttemptAt
		session.WaitingCloseLastAttemptAt = &value
	}
	if waitingInfo != nil && waitingInfo.LastAttemptAt != "" {
		if parsed, err := time.Parse(time.RFC3339, waitingInfo.LastAttemptAt); err == nil {
			session.WaitingCloseLastAttemptAt = &parsed
		}
	}
	s.applyProgressMeta(now, prev, &session)
	return session
}

func (s *CloseManagerService) deriveClosedSession(now time.Time, item lndclient.ClosedChannelInfo, prev CloseManagerSession, src closeManagerSourceRef, sweep closeManagerSweepStatus) CloseManagerSession {
	state := closeManagerStateClosedTerminal
	actionRecommended := ""
	riskLevel := "info"
	if item.TimeLockedBalanceSat > 0 {
		state = closeManagerStateOutputsTimelocked
		actionRecommended = "wait_maturity"
		riskLevel = "warn"
	}
	if item.TimeLockedBalanceSat == 0 && (prev.State == closeManagerStateOutputsTimelocked || prev.State == closeManagerStateForceCloseActive || prev.State == closeManagerStateSweepPending || prev.State == closeManagerStateSweepStuck) {
		state = closeManagerStateFundsRecovered
	}

	sweepTxid := deriveClosedSessionSweepTxid(item)
	if item.TimeLockedBalanceSat > 0 && sweep.PendingCount > 0 {
		state = closeManagerStateSweepPending
		actionRecommended = "monitor"
		riskLevel = "warn"
	}
	if item.TimeLockedBalanceSat > 0 && sweep.Stuck {
		state = closeManagerStateSweepStuck
		actionRecommended = "review_sweep"
		riskLevel = "error"
	}
	lastError := ""
	if sweep.Stuck {
		lastError = deriveCloseManagerSweepReason(sweep)
	}
	blocksTilMaturity, maturityETA := closeManagerCarryForwardMaturity(now, prev)

	session := CloseManagerSession{
		ID:                         prev.ID,
		ChannelPoint:               strings.TrimSpace(item.ChannelPoint),
		ChannelID:                  int64(item.ChanID),
		PeerPubkey:                 firstNonEmpty(item.RemotePubkey, prev.PeerPubkey),
		PeerAlias:                  firstNonEmpty(item.PeerAlias, prev.PeerAlias),
		Source:                     sourceOrDefault(src, prev.Source),
		SourceRef:                  sourceRefOrDefault(src, prev.SourceRef),
		State:                      state,
		ActionRequired:             "",
		ActionRecommended:          actionRecommended,
		Decision:                   prev.Decision,
		RiskLevel:                  riskLevel,
		CloseMode:                  firstNonEmpty(prev.CloseMode, closeManagerModeFromClosedChannel(item)),
		CloseTxid:                  firstNonEmpty(item.ClosingTxHash, prev.CloseTxid),
		CloseTxHexAvailable:        false,
		SweepTxid:                  firstNonEmpty(sweepTxid, prev.SweepTxid),
		LimboBalanceSat:            item.TimeLockedBalanceSat,
		PendingHtlcCount:           0,
		PendingHtlcAgeSec:          0,
		BlocksTilMaturity:          blocksTilMaturity,
		MaturityETAAt:              maturityETA,
		SweepPendingCount:          sweep.PendingCount,
		SweepBroadcastAttempts:     sweep.BroadcastAttempts,
		SweepRequestedFeeRateSatVB: sweep.RequestedFeeRateSatVB,
		SweepFeeRateSatVB:          prev.SweepFeeRateSatVB,
		MempoolTargetSatVB:         prev.MempoolTargetSatVB,
		LastError:                  firstNonEmpty(lastError, prev.LastError),
		CreatedAt:                  prev.CreatedAt,
		UpdatedAt:                  now,
	}
	if sweep.FeeRateSatVB > 0 {
		session.SweepFeeRateSatVB = sweep.FeeRateSatVB
	}
	if sweep.MempoolTargetSatVB > 0 {
		session.MempoolTargetSatVB = sweep.MempoolTargetSatVB
	}
	closedAt := now
	if item.ClosedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, item.ClosedAt); err == nil {
			closedAt = parsed
		}
	}
	if isCloseManagerTerminalState(state) {
		session.ClosedAt = &closedAt
	}
	s.applyProgressMeta(now, prev, &session)
	return session
}

func (s *CloseManagerService) applyProgressMeta(now time.Time, prev CloseManagerSession, session *CloseManagerSession) {
	if session == nil {
		return
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	progressChanged := prev.ID == 0 ||
		prev.State != session.State ||
		prev.CloseTxid != session.CloseTxid ||
		prev.SweepTxid != session.SweepTxid ||
		prev.SweepPendingCount != session.SweepPendingCount ||
		prev.SweepBroadcastAttempts != session.SweepBroadcastAttempts ||
		prev.PendingHtlcCount != session.PendingHtlcCount ||
		prev.LimboBalanceSat != session.LimboBalanceSat ||
		prev.WaitingCloseSuggestForceClose != session.WaitingCloseSuggestForceClose ||
		prev.ActionRecommended != session.ActionRecommended
	if progressChanged {
		value := now
		session.LastProgressAt = &value
	} else if prev.LastProgressAt != nil {
		value := *prev.LastProgressAt
		session.LastProgressAt = &value
	}
	if !isCloseManagerTerminalState(session.State) {
		session.ClosedAt = nil
	}
}

func (s *CloseManagerService) upsertSession(ctx context.Context, prev CloseManagerSession, next CloseManagerSession) error {
	if strings.TrimSpace(next.ChannelPoint) == "" {
		return nil
	}
	if next.CreatedAt.IsZero() {
		next.CreatedAt = time.Now().UTC()
	}
	if prev.ID == 0 {
		err := s.db.QueryRow(ctx, `
insert into close_sessions (
  channel_point, channel_id, peer_pubkey, peer_alias, source, source_ref, state,
  action_required, action_recommended, decision, risk_level, close_mode, close_txid,
  close_tx_hex_available, sweep_txid, limbo_balance_sat, pending_htlc_count,
  pending_htlc_first_seen_at, pending_htlc_age_sec, blocks_til_maturity, maturity_eta_at,
  sweep_pending_count, sweep_broadcast_attempts, sweep_requested_fee_rate_sat_vb,
  sweep_fee_rate_sat_vb, mempool_target_sat_vb, last_error, waiting_close_attempts,
  waiting_close_last_attempt_at, waiting_close_last_result, waiting_close_last_error,
  waiting_close_last_recovered_txid, waiting_close_suggest_force_close, last_progress_at,
  created_at, updated_at, closed_at
) values (
  $1,$2,$3,$4,$5,$6,$7,
  $8,$9,$10,$11,$12,$13,
  $14,$15,$16,$17,
  $18,$19,$20,$21,
  $22,$23,$24,$25,
  $26,$27,$28,$29,
  $30,$31,$32,
  $33,$34,$35,
  $36,$37
)
returning id
`, next.ChannelPoint, next.ChannelID, next.PeerPubkey, next.PeerAlias, next.Source, next.SourceRef, next.State,
			next.ActionRequired, next.ActionRecommended, next.Decision, next.RiskLevel, next.CloseMode, next.CloseTxid,
			next.CloseTxHexAvailable, next.SweepTxid, next.LimboBalanceSat, next.PendingHtlcCount,
			next.PendingHtlcFirstSeenAt, next.PendingHtlcAgeSec, next.BlocksTilMaturity, next.MaturityETAAt,
			next.SweepPendingCount, next.SweepBroadcastAttempts, next.SweepRequestedFeeRateSatVB,
			next.SweepFeeRateSatVB, next.MempoolTargetSatVB, next.LastError, next.WaitingCloseAttempts,
			next.WaitingCloseLastAttemptAt, next.WaitingCloseLastResult, next.WaitingCloseLastError,
			next.WaitingCloseLastRecoveredTxid, next.WaitingCloseSuggestForceClose, next.LastProgressAt,
			next.CreatedAt, next.UpdatedAt, next.ClosedAt,
		).Scan(&next.ID)
		if err != nil {
			return err
		}
		return s.insertEvent(ctx, next.ID, "session_detected", "info", map[string]any{
			"state":         next.State,
			"channel_point": next.ChannelPoint,
			"source":        next.Source,
		})
	}

	next.ID = prev.ID
	_, err := s.db.Exec(ctx, `
update close_sessions
set channel_id = $2,
    peer_pubkey = $3,
    peer_alias = $4,
    source = $5,
    source_ref = $6,
    state = $7,
    action_required = $8,
    action_recommended = $9,
    decision = $10,
    risk_level = $11,
    close_mode = $12,
    close_txid = $13,
    close_tx_hex_available = $14,
    sweep_txid = $15,
    limbo_balance_sat = $16,
    pending_htlc_count = $17,
    pending_htlc_first_seen_at = $18,
    pending_htlc_age_sec = $19,
    blocks_til_maturity = $20,
    maturity_eta_at = $21,
    sweep_pending_count = $22,
    sweep_broadcast_attempts = $23,
    sweep_requested_fee_rate_sat_vb = $24,
    sweep_fee_rate_sat_vb = $25,
    mempool_target_sat_vb = $26,
    last_error = $27,
    waiting_close_attempts = $28,
    waiting_close_last_attempt_at = $29,
    waiting_close_last_result = $30,
    waiting_close_last_error = $31,
    waiting_close_last_recovered_txid = $32,
    waiting_close_suggest_force_close = $33,
    last_progress_at = $34,
    updated_at = $35,
    closed_at = $36
where id = $1
`, next.ID, next.ChannelID, next.PeerPubkey, next.PeerAlias, next.Source, next.SourceRef, next.State,
		next.ActionRequired, next.ActionRecommended, next.Decision, next.RiskLevel, next.CloseMode, next.CloseTxid,
		next.CloseTxHexAvailable, next.SweepTxid, next.LimboBalanceSat, next.PendingHtlcCount,
		next.PendingHtlcFirstSeenAt, next.PendingHtlcAgeSec, next.BlocksTilMaturity, next.MaturityETAAt,
		next.SweepPendingCount, next.SweepBroadcastAttempts, next.SweepRequestedFeeRateSatVB,
		next.SweepFeeRateSatVB, next.MempoolTargetSatVB, next.LastError, next.WaitingCloseAttempts,
		next.WaitingCloseLastAttemptAt, next.WaitingCloseLastResult, next.WaitingCloseLastError,
		next.WaitingCloseLastRecoveredTxid, next.WaitingCloseSuggestForceClose, next.LastProgressAt,
		next.UpdatedAt, next.ClosedAt,
	)
	if err != nil {
		return err
	}
	if prev.State != next.State {
		if err := s.insertEvent(ctx, next.ID, "state_changed", severityFromRisk(next.RiskLevel), map[string]any{
			"from": prev.State,
			"to":   next.State,
		}); err != nil {
			return err
		}
	}
	if prev.CloseTxid != next.CloseTxid && next.CloseTxid != "" {
		if err := s.insertEvent(ctx, next.ID, "close_txid_detected", "info", map[string]any{
			"txid": next.CloseTxid,
		}); err != nil {
			return err
		}
	}
	if prev.SweepTxid != next.SweepTxid && next.SweepTxid != "" {
		if err := s.insertEvent(ctx, next.ID, "sweep_txid_detected", "info", map[string]any{
			"txid": next.SweepTxid,
		}); err != nil {
			return err
		}
	}
	if prev.PendingHtlcCount != next.PendingHtlcCount && next.PendingHtlcCount > 0 {
		if err := s.insertEvent(ctx, next.ID, "pending_htlc_changed", severityFromRisk(next.RiskLevel), map[string]any{
			"count":   next.PendingHtlcCount,
			"age_sec": next.PendingHtlcAgeSec,
		}); err != nil {
			return err
		}
	}
	if prev.SweepPendingCount != next.SweepPendingCount && next.SweepPendingCount > 0 {
		if err := s.insertEvent(ctx, next.ID, "pending_sweeps_changed", severityFromRisk(next.RiskLevel), map[string]any{
			"count":              next.SweepPendingCount,
			"broadcast_attempts": next.SweepBroadcastAttempts,
			"fee_rate_sat_vb":    next.SweepFeeRateSatVB,
			"target_sat_vb":      next.MempoolTargetSatVB,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *CloseManagerService) insertEvent(ctx context.Context, sessionID int64, eventType string, severity string, payload map[string]any) error {
	if sessionID <= 0 {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
insert into close_events (session_id, event_type, severity, payload_json)
values ($1, $2, $3, $4)
`, sessionID, strings.TrimSpace(eventType), severityFromRisk(severity), raw)
	return err
}

type pgxRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanCloseManagerSessions(rows pgxRows) ([]CloseManagerSession, error) {
	items := make([]CloseManagerSession, 0)
	for rows.Next() {
		var item CloseManagerSession
		var peerPubkey sql.NullString
		var peerAlias sql.NullString
		var source sql.NullString
		var sourceRef sql.NullString
		var actionRequired sql.NullString
		var actionRecommended sql.NullString
		var decision sql.NullString
		var riskLevel sql.NullString
		var closeMode sql.NullString
		var closeTxid sql.NullString
		var sweepTxid sql.NullString
		var lastError sql.NullString
		var pendingFirstSeen sql.NullTime
		var blocksTilMaturity sql.NullInt32
		var maturityETA sql.NullTime
		var waitingLastAttempt sql.NullTime
		var waitingLastResult sql.NullString
		var waitingLastError sql.NullString
		var waitingLastRecoveredTxid sql.NullString
		var lastProgress sql.NullTime
		var closedAt sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.ChannelPoint, &item.ChannelID, &peerPubkey, &peerAlias, &source, &sourceRef, &item.State,
			&actionRequired, &actionRecommended, &decision, &riskLevel, &closeMode, &closeTxid,
			&item.CloseTxHexAvailable, &sweepTxid, &item.LimboBalanceSat, &item.PendingHtlcCount,
			&pendingFirstSeen, &item.PendingHtlcAgeSec, &blocksTilMaturity, &maturityETA,
			&item.SweepPendingCount, &item.SweepBroadcastAttempts, &item.SweepRequestedFeeRateSatVB,
			&item.SweepFeeRateSatVB, &item.MempoolTargetSatVB, &lastError, &item.WaitingCloseAttempts,
			&waitingLastAttempt, &waitingLastResult, &waitingLastError,
			&waitingLastRecoveredTxid, &item.WaitingCloseSuggestForceClose, &lastProgress,
			&item.CreatedAt, &item.UpdatedAt, &closedAt,
		); err != nil {
			return nil, err
		}
		item.PeerPubkey = nullStringValue(peerPubkey)
		item.PeerAlias = nullStringValue(peerAlias)
		item.Source = nullStringValue(source)
		if item.Source == "" {
			item.Source = "lightning_ops"
		}
		item.SourceRef = nullStringValue(sourceRef)
		item.ActionRequired = nullStringValue(actionRequired)
		item.ActionRecommended = nullStringValue(actionRecommended)
		item.Decision = nullStringValue(decision)
		item.RiskLevel = nullStringValue(riskLevel)
		item.CloseMode = nullStringValue(closeMode)
		item.CloseTxid = nullStringValue(closeTxid)
		item.SweepTxid = nullStringValue(sweepTxid)
		item.LastError = nullStringValue(lastError)
		item.WaitingCloseLastResult = nullStringValue(waitingLastResult)
		item.WaitingCloseLastError = nullStringValue(waitingLastError)
		item.WaitingCloseLastRecoveredTxid = nullStringValue(waitingLastRecoveredTxid)
		if pendingFirstSeen.Valid {
			value := pendingFirstSeen.Time
			item.PendingHtlcFirstSeenAt = &value
		}
		if blocksTilMaturity.Valid {
			value := blocksTilMaturity.Int32
			item.BlocksTilMaturity = &value
		}
		if maturityETA.Valid {
			value := maturityETA.Time
			item.MaturityETAAt = &value
		}
		if waitingLastAttempt.Valid {
			value := waitingLastAttempt.Time
			item.WaitingCloseLastAttemptAt = &value
		}
		if lastProgress.Valid {
			value := lastProgress.Time
			item.LastProgressAt = &value
		}
		if closedAt.Valid {
			value := closedAt.Time
			item.ClosedAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func closeManagerCarryForwardMaturity(now time.Time, prev CloseManagerSession) (*int32, *time.Time) {
	if prev.MaturityETAAt == nil {
		if prev.BlocksTilMaturity == nil || *prev.BlocksTilMaturity <= 0 {
			return nil, nil
		}
		value := *prev.BlocksTilMaturity
		return &value, nil
	}

	eta := prev.MaturityETAAt.UTC()
	if !eta.After(now) {
		return nil, nil
	}

	remaining := eta.Sub(now)
	blocks := int32(math.Ceil(remaining.Seconds() / 600.0))
	if blocks < 1 {
		blocks = 1
	}
	return &blocks, &eta
}

func deriveClosedSessionSweepTxid(item lndclient.ClosedChannelInfo) string {
	for _, res := range item.Resolutions {
		txid := strings.ToLower(strings.TrimSpace(res.SweepTxid))
		if txid != "" {
			return txid
		}
	}
	return ""
}

func deriveCloseManagerSweepStatus(closeTxid string, knownSweepTxid string, pendingBySourceTxid map[string][]lndclient.PendingSweepInfo, sweepTxids map[string]struct{}, mempoolTargetSatVB int64) closeManagerSweepStatus {
	status := closeManagerSweepStatus{MempoolTargetSatVB: mempoolTargetSatVB}
	normalizedCloseTxid := strings.ToLower(strings.TrimSpace(closeTxid))
	if normalizedCloseTxid != "" {
		for _, item := range pendingBySourceTxid[normalizedCloseTxid] {
			status.PendingCount++
			if attempts := int(item.BroadcastAttempts); attempts > status.BroadcastAttempts {
				status.BroadcastAttempts = attempts
			}
			if item.RequestedSatPerVbyte > status.RequestedFeeRateSatVB {
				status.RequestedFeeRateSatVB = item.RequestedSatPerVbyte
			}
			if item.SatPerVbyte > status.FeeRateSatVB {
				status.FeeRateSatVB = item.SatPerVbyte
			}
		}
	}

	normalizedSweepTxid := strings.ToLower(strings.TrimSpace(knownSweepTxid))
	if normalizedSweepTxid != "" {
		if _, ok := sweepTxids[normalizedSweepTxid]; ok {
			status.SweepTxSeen = true
		}
	}

	switch {
	case status.PendingCount == 0:
		status.Stuck = false
	case status.BroadcastAttempts >= 6:
		status.Stuck = true
	case status.BroadcastAttempts >= 3 && status.MempoolTargetSatVB > 0 && status.FeeRateSatVB > 0 && status.FeeRateSatVB < status.MempoolTargetSatVB:
		status.Stuck = true
	default:
		status.Stuck = false
	}

	return status
}

func deriveCloseManagerSweepReason(status closeManagerSweepStatus) string {
	switch {
	case status.BroadcastAttempts >= 6:
		return fmt.Sprintf("sweep has %d broadcast attempts without confirmation", status.BroadcastAttempts)
	case status.BroadcastAttempts >= 3 && status.MempoolTargetSatVB > 0 && status.FeeRateSatVB > 0 && status.FeeRateSatVB < status.MempoolTargetSatVB:
		return fmt.Sprintf("sweep fee %d sat/vB is below mempool target %d sat/vB", status.FeeRateSatVB, status.MempoolTargetSatVB)
	default:
		return ""
	}
}

func deriveCloseManagerPendingState(status string, closeTxid string, pendingHtlcCount int) string {
	switch status {
	case "waiting_close":
		if strings.TrimSpace(closeTxid) == "" {
			if pendingHtlcCount > 0 {
				return closeManagerStateCoopBlockedByHTLCs
			}
			return closeManagerStateWaitingCloseNoTxid
		}
		return closeManagerStateClosingTxSeen
	case "closing":
		if pendingHtlcCount > 0 {
			return closeManagerStateCoopBlockedByHTLCs
		}
		if strings.TrimSpace(closeTxid) != "" {
			return closeManagerStateClosingTxSeen
		}
		return closeManagerStateCoopRequested
	case "force_closing":
		if strings.TrimSpace(closeTxid) == "" {
			return closeManagerStateForceCloseActive
		}
		return closeManagerStateOutputsTimelocked
	default:
		return closeManagerStateCoopRequested
	}
}

func closeManagerModeFromPendingStatus(status string) string {
	switch status {
	case "force_closing":
		return "force"
	default:
		return "coop"
	}
}

func closeManagerModeFromClosedChannel(item lndclient.ClosedChannelInfo) string {
	label := strings.ToUpper(strings.TrimSpace(item.CloseTypeLabel))
	if strings.Contains(label, "FORCE") || strings.Contains(label, "BREACH") {
		return "force"
	}
	return "coop"
}

func isCloseManagerTerminalState(state string) bool {
	switch strings.TrimSpace(state) {
	case closeManagerStateFundsRecovered, closeManagerStateClosedTerminal:
		return true
	default:
		return false
	}
}

func normalizeCloseManagerListLimit(value int) int {
	if value <= 0 {
		return closeManagerDefaultListLimit
	}
	if value > closeManagerMaxListLimit {
		return closeManagerMaxListLimit
	}
	return value
}

func normalizeCloseManagerEventLimit(value int) int {
	if value <= 0 {
		return closeManagerDefaultEventLimit
	}
	if value > closeManagerMaxEventLimit {
		return closeManagerMaxEventLimit
	}
	return value
}

func sourceOrDefault(src closeManagerSourceRef, fallback string) string {
	if strings.TrimSpace(src.source) != "" {
		return strings.TrimSpace(src.source)
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return "lightning_ops"
}

func sourceRefOrDefault(src closeManagerSourceRef, fallback string) string {
	if strings.TrimSpace(src.sourceRef) != "" {
		return strings.TrimSpace(src.sourceRef)
	}
	return strings.TrimSpace(fallback)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func nullStringValue(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func severityFromRisk(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "err", "error":
		return "error"
	case "warn", "warning":
		return "warn"
	default:
		return "info"
	}
}
