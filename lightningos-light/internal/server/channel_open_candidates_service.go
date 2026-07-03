package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	channelOpenCandidatesCheckInterval        = 1 * time.Hour
	channelOpenCandidatesRefreshMaxAge        = 24 * time.Hour
	channelOpenCandidatesRefreshTimeout       = 75 * time.Second
	channelOpenCandidatesRouteLookback        = 30 * 24 * time.Hour
	channelOpenCandidatesPersistLimit         = 40
	channelOpenCandidatesRouteSeedLimit       = 160
	channelOpenCandidatesProblemLimit         = 140
	channelOpenCandidatesStrongLimit          = 100
	channelOpenCandidatesLocalPeerLoadTimeout = 5 * time.Second
)

var ErrChannelOpenCandidatesDBUnavailable = errors.New("channel open candidates db unavailable")

type ChannelOpenCandidateReason struct {
	Code string `json:"code"`
}

type ChannelOpenCandidateItem struct {
	PeerPubkey               string                       `json:"peer_pubkey"`
	PeerAlias                string                       `json:"peer_alias,omitempty"`
	RouteHitCount30d         int                          `json:"route_hit_count_30d"`
	RouteVolumeSat30d        int64                        `json:"route_volume_sat_30d"`
	RouteCostToMsat30d       int64                        `json:"route_cost_to_msat_30d"`
	RouteCostPpm30d          int                          `json:"route_cost_ppm_30d"`
	FailedAttempts30d        int                          `json:"failed_attempts_30d"`
	PaymentHitCount30d       int                          `json:"payment_hit_count_30d"`
	RebalanceHitCount30d     int                          `json:"rebalance_hit_count_30d"`
	SharedProblemPeerCount   int                          `json:"shared_problem_peer_count"`
	SharedProblemCapacitySat int64                        `json:"shared_problem_capacity_sat"`
	SharedStrongPeerCount    int                          `json:"shared_strong_peer_count"`
	SharedStrongCapacitySat  int64                        `json:"shared_strong_capacity_sat"`
	GraphChannelCount        int                          `json:"graph_channel_count"`
	GraphTotalCapacitySat    int64                        `json:"graph_total_capacity_sat"`
	BestOutboundFeePpm       int64                        `json:"best_outbound_fee_ppm"`
	BestInboundFeePpm        int64                        `json:"best_inbound_fee_ppm"`
	DemandScore              int                          `json:"demand_score"`
	ReliefScore              int                          `json:"relief_score"`
	GraphQualityScore        int                          `json:"graph_quality_score"`
	Score                    int                          `json:"score"`
	Confidence               int                          `json:"confidence"`
	Reasons                  []ChannelOpenCandidateReason `json:"reasons,omitempty"`
	ComputedAt               time.Time                    `json:"computed_at"`
}

type ChannelOpenCandidateStatus struct {
	Available      bool       `json:"available"`
	LastSyncAt     *time.Time `json:"last_sync_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CandidateCount int        `json:"candidate_count"`
}

type ChannelOpenCandidatesService struct {
	db     *pgxpool.Pool
	logger *log.Logger
	lnd    channelOpenCandidatesLND

	syncMu sync.Mutex
	mu     sync.Mutex

	lastSyncAt time.Time
	stopCh     chan struct{}
	doneCh     chan struct{}
}

type channelOpenCandidatesLND interface {
	CachedPubkey() string
	GetStatus(ctx context.Context) (lndclient.Status, error)
	ListChannels(ctx context.Context) ([]lndclient.ChannelInfo, error)
	ListPendingChannels(ctx context.Context) ([]lndclient.PendingChannelInfo, error)
}

type channelOpenRouteSignal struct {
	PubKey               string
	RouteHitCount30d     int
	RouteVolumeSat30d    int64
	RouteCostToMsat30d   int64
	FailedAttempts30d    int
	PaymentHitCount30d   int
	RebalanceHitCount30d int
}

type channelOpenNeighborSignal struct {
	PubKey            string
	SharedPeerCount   int
	SharedCapacitySat int64
}

type channelOpenGraphMetric struct {
	PubKey             string
	PeerAlias          string
	GraphChannelCount  int
	GraphTotalCapacity int64
	BestOutboundFeePpm int64
	BestInboundFeePpm  int64
}

type channelOpenRouteCoverage struct {
	DirectEvidenceNodes int
	SuccessfulHits      int
}

func NewChannelOpenCandidatesService(db *pgxpool.Pool, logger *log.Logger, lnd *lndclient.Client) *ChannelOpenCandidatesService {
	var lndAPI channelOpenCandidatesLND
	if lnd != nil {
		lndAPI = lnd
	}
	return &ChannelOpenCandidatesService{
		db:     db,
		logger: logger,
		lnd:    lndAPI,
	}
}

func (s *ChannelOpenCandidatesService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrChannelOpenCandidatesDBUnavailable
	}
	_, err := s.db.Exec(ctx, `
create table if not exists channel_open_candidates (
  peer_pubkey text primary key,
  peer_alias text,
  route_hit_count_30d integer not null default 0,
  route_volume_sat_30d bigint not null default 0,
  route_cost_to_msat_30d bigint not null default 0,
  route_cost_ppm_30d integer not null default 0,
  failed_attempts_30d integer not null default 0,
  payment_hit_count_30d integer not null default 0,
  rebalance_hit_count_30d integer not null default 0,
  shared_problem_peer_count integer not null default 0,
  shared_problem_capacity_sat bigint not null default 0,
  shared_strong_peer_count integer not null default 0,
  shared_strong_capacity_sat bigint not null default 0,
  graph_channel_count integer not null default 0,
  graph_total_capacity_sat bigint not null default 0,
  best_outbound_fee_ppm bigint not null default 0,
  best_inbound_fee_ppm bigint not null default 0,
  demand_score integer not null default 0,
  relief_score integer not null default 0,
  graph_quality_score integer not null default 0,
  score integer not null default 0,
  confidence integer not null default 0,
  reasons_json jsonb not null default '[]'::jsonb,
  computed_at timestamptz not null default now()
);
create index if not exists channel_open_candidates_score_idx on channel_open_candidates (score desc, confidence desc, route_cost_to_msat_30d desc);
create index if not exists channel_open_candidates_computed_idx on channel_open_candidates (computed_at desc);

create table if not exists channel_open_candidate_sync_state (
  id boolean primary key default true,
  last_sync_at timestamptz,
  last_error text,
  candidate_count integer not null default 0,
  updated_at timestamptz not null default now()
);
insert into channel_open_candidate_sync_state (id)
values (true)
on conflict (id) do nothing;
alter table channel_open_candidates add column if not exists demand_score integer not null default 0;
alter table channel_open_candidates add column if not exists relief_score integer not null default 0;
alter table channel_open_candidates add column if not exists graph_quality_score integer not null default 0;
`)
	return err
}

func (s *ChannelOpenCandidatesService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopCh != nil {
		s.mu.Unlock()
		return
	}
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	go func() {
		defer close(doneCh)
		s.runLoop(stopCh)
	}()
}

func (s *ChannelOpenCandidatesService) runLoop(stopCh <-chan struct{}) {
	s.refreshIfStaleBackground()
	ticker := time.NewTicker(channelOpenCandidatesCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			s.refreshIfStaleBackground()
		}
	}
}

func (s *ChannelOpenCandidatesService) refreshIfStaleBackground() {
	if s == nil || s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), channelOpenCandidatesRefreshTimeout)
	defer cancel()
	if err := s.RefreshIfStale(ctx, channelOpenCandidatesRefreshMaxAge); err != nil && s.logger != nil {
		s.logger.Printf("channel open candidates refresh failed: %v", err)
	}
}

func (s *ChannelOpenCandidatesService) RefreshIfStale(ctx context.Context, maxAge time.Duration) error {
	if s == nil {
		return nil
	}
	if maxAge <= 0 {
		maxAge = channelOpenCandidatesRefreshMaxAge
	}
	lastSyncAt, err := s.persistedLastSyncAt(ctx)
	if err != nil {
		return err
	}
	if lastSyncAt != nil && time.Since(*lastSyncAt) < maxAge {
		return nil
	}
	return s.Refresh(ctx)
}

func (s *ChannelOpenCandidatesService) Refresh(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrChannelOpenCandidatesDBUnavailable
	}

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	computedAt := time.Now().UTC()
	items, err := s.buildCandidates(ctx, computedAt)
	if err != nil {
		_ = s.recordSyncError(ctx, err.Error())
		return err
	}
	if err := s.persistSnapshot(ctx, items, computedAt); err != nil {
		_ = s.recordSyncError(ctx, err.Error())
		return err
	}

	s.mu.Lock()
	s.lastSyncAt = computedAt
	s.mu.Unlock()
	return nil
}

func (s *ChannelOpenCandidatesService) Status(ctx context.Context) (ChannelOpenCandidateStatus, error) {
	if s == nil || s.db == nil {
		return ChannelOpenCandidateStatus{}, ErrChannelOpenCandidatesDBUnavailable
	}
	var status ChannelOpenCandidateStatus
	var lastSyncAt sql.NullTime
	var lastError sql.NullString
	err := s.db.QueryRow(ctx, `
select last_sync_at, coalesce(last_error, ''), candidate_count
from channel_open_candidate_sync_state
where id=true
`).Scan(&lastSyncAt, &lastError, &status.CandidateCount)
	if err != nil {
		return ChannelOpenCandidateStatus{}, err
	}
	status.Available = lastSyncAt.Valid
	if lastSyncAt.Valid {
		ts := lastSyncAt.Time.UTC()
		status.LastSyncAt = &ts
	}
	if lastError.Valid {
		status.LastError = strings.TrimSpace(lastError.String)
	}
	return status, nil
}

func (s *ChannelOpenCandidatesService) List(ctx context.Context, limit int) ([]ChannelOpenCandidateItem, ChannelOpenCandidateStatus, error) {
	if s == nil || s.db == nil {
		return nil, ChannelOpenCandidateStatus{}, ErrChannelOpenCandidatesDBUnavailable
	}
	if limit <= 0 {
		limit = channelOpenCandidatesPersistLimit
	}
	if limit > 200 {
		limit = 200
	}
	status, err := s.Status(ctx)
	if err != nil {
		return nil, ChannelOpenCandidateStatus{}, err
	}
	rows, err := s.db.Query(ctx, `
select peer_pubkey, coalesce(peer_alias, ''), route_hit_count_30d, route_volume_sat_30d,
  route_cost_to_msat_30d, route_cost_ppm_30d, failed_attempts_30d, payment_hit_count_30d,
  rebalance_hit_count_30d, shared_problem_peer_count, shared_problem_capacity_sat,
  shared_strong_peer_count, shared_strong_capacity_sat, graph_channel_count,
  graph_total_capacity_sat, best_outbound_fee_ppm, best_inbound_fee_ppm, demand_score,
  relief_score, graph_quality_score,
  score, confidence, reasons_json, computed_at
from channel_open_candidates
order by score desc, confidence desc, route_cost_to_msat_30d desc, route_volume_sat_30d desc, peer_pubkey asc
limit 200
`)
	if err != nil {
		return nil, ChannelOpenCandidateStatus{}, err
	}
	defer rows.Close()
	items, err := scanChannelOpenCandidateItems(rows)
	if err != nil {
		return nil, ChannelOpenCandidateStatus{}, err
	}
	items = filterChannelOpenCandidatesForLocalPeers(items, s.loadLocalChannelPeerSet(ctx))
	status.CandidateCount = len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, status, nil
}

func (s *ChannelOpenCandidatesService) buildCandidates(ctx context.Context, computedAt time.Time) ([]ChannelOpenCandidateItem, error) {
	selfPubkey := s.localPubkey(ctx)
	routeSignals, err := s.fetchRouteSignals(ctx, computedAt.Add(-channelOpenCandidatesRouteLookback), selfPubkey)
	if err != nil {
		return nil, err
	}
	localChannelPeers := s.loadLocalChannelPeerSet(ctx)
	filterChannelOpenRouteSignalsForLocalPeers(routeSignals, localChannelPeers)
	routeCoverage := summarizeChannelOpenRouteCoverage(routeSignals)
	strictAdjacencyOnly := routeCoverage.DirectEvidenceNodes < 8 || routeCoverage.SuccessfulHits < 20
	problemSignals, err := s.fetchNeighborSignals(ctx, selfPubkey, false)
	if err != nil {
		return nil, err
	}
	filterChannelOpenNeighborSignalsForLocalPeers(problemSignals, localChannelPeers)
	strongSignals, err := s.fetchNeighborSignals(ctx, selfPubkey, true)
	if err != nil {
		return nil, err
	}
	filterChannelOpenNeighborSignalsForLocalPeers(strongSignals, localChannelPeers)

	itemsByPubkey := make(map[string]*ChannelOpenCandidateItem)
	for pubkey, signal := range routeSignals {
		item := ensureChannelOpenCandidate(itemsByPubkey, pubkey)
		item.RouteHitCount30d = signal.RouteHitCount30d
		item.RouteVolumeSat30d = signal.RouteVolumeSat30d
		item.RouteCostToMsat30d = signal.RouteCostToMsat30d
		item.FailedAttempts30d = signal.FailedAttempts30d
		item.PaymentHitCount30d = signal.PaymentHitCount30d
		item.RebalanceHitCount30d = signal.RebalanceHitCount30d
	}
	for pubkey, signal := range problemSignals {
		item := ensureChannelOpenCandidate(itemsByPubkey, pubkey)
		item.SharedProblemPeerCount = signal.SharedPeerCount
		item.SharedProblemCapacitySat = signal.SharedCapacitySat
	}
	for pubkey, signal := range strongSignals {
		item := ensureChannelOpenCandidate(itemsByPubkey, pubkey)
		item.SharedStrongPeerCount = signal.SharedPeerCount
		item.SharedStrongCapacitySat = signal.SharedCapacitySat
	}

	if len(itemsByPubkey) == 0 {
		return nil, nil
	}
	deleteChannelOpenCandidatesForLocalPeers(itemsByPubkey, localChannelPeers)
	if len(itemsByPubkey) == 0 {
		return nil, nil
	}

	if err := s.applyGraphMetrics(ctx, itemsByPubkey); err != nil {
		return nil, err
	}

	items := make([]ChannelOpenCandidateItem, 0, len(itemsByPubkey))
	for _, item := range itemsByPubkey {
		item.RouteCostPpm30d = channelOpenCostPpm(item.RouteCostToMsat30d, item.RouteVolumeSat30d)
		item.DemandScore, item.ReliefScore, item.GraphQualityScore, item.Score, item.Confidence = computeChannelOpenCandidateScore(item)
		item.Reasons = buildChannelOpenCandidateReasons(item)
		item.ComputedAt = computedAt
		if !shouldKeepChannelOpenCandidate(item, strictAdjacencyOnly) {
			continue
		}
		items = append(items, *item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Confidence != items[j].Confidence {
			return items[i].Confidence > items[j].Confidence
		}
		if items[i].RouteCostToMsat30d != items[j].RouteCostToMsat30d {
			return items[i].RouteCostToMsat30d > items[j].RouteCostToMsat30d
		}
		if items[i].RouteVolumeSat30d != items[j].RouteVolumeSat30d {
			return items[i].RouteVolumeSat30d > items[j].RouteVolumeSat30d
		}
		if items[i].SharedProblemCapacitySat != items[j].SharedProblemCapacitySat {
			return items[i].SharedProblemCapacitySat > items[j].SharedProblemCapacitySat
		}
		return items[i].PeerPubkey < items[j].PeerPubkey
	})

	if len(items) > channelOpenCandidatesPersistLimit {
		items = items[:channelOpenCandidatesPersistLimit]
	}
	return items, nil
}

func ensureChannelOpenCandidate(items map[string]*ChannelOpenCandidateItem, pubkey string) *ChannelOpenCandidateItem {
	key := graphExplorerNormalizePubkey(pubkey)
	if item, ok := items[key]; ok {
		return item
	}
	item := &ChannelOpenCandidateItem{PeerPubkey: key}
	items[key] = item
	return item
}

func (s *ChannelOpenCandidatesService) loadLocalChannelPeerSet(ctx context.Context) map[string]struct{} {
	if s == nil || s.lnd == nil {
		return nil
	}
	loadCtx := ctx
	cancel := func() {}
	if ctx == nil {
		loadCtx, cancel = context.WithTimeout(context.Background(), channelOpenCandidatesLocalPeerLoadTimeout)
	} else {
		loadCtx, cancel = context.WithTimeout(ctx, channelOpenCandidatesLocalPeerLoadTimeout)
	}
	defer cancel()

	peers := map[string]struct{}{}
	if channels, err := s.lnd.ListChannels(loadCtx); err == nil {
		addChannelOpenCandidateOpenPeers(peers, channels)
	} else if s.logger != nil {
		s.logger.Printf("channel open candidates: local open channel peer set unavailable: %v", err)
	}
	if pending, err := s.lnd.ListPendingChannels(loadCtx); err == nil {
		addChannelOpenCandidatePendingPeers(peers, pending)
	} else if loadCtx.Err() == nil && s.logger != nil {
		s.logger.Printf("channel open candidates: local pending channel peer set unavailable: %v", err)
	}
	if len(peers) == 0 {
		return nil
	}
	return peers
}

func addChannelOpenCandidateOpenPeers(peers map[string]struct{}, channels []lndclient.ChannelInfo) {
	for _, channel := range channels {
		pubkey := graphExplorerNormalizePubkey(channel.RemotePubkey)
		if pubkey == "" {
			continue
		}
		peers[pubkey] = struct{}{}
	}
}

func addChannelOpenCandidatePendingPeers(peers map[string]struct{}, channels []lndclient.PendingChannelInfo) {
	for _, channel := range channels {
		pubkey := graphExplorerNormalizePubkey(channel.RemotePubkey)
		if pubkey == "" {
			continue
		}
		peers[pubkey] = struct{}{}
	}
}

func filterChannelOpenCandidatesForLocalPeers(items []ChannelOpenCandidateItem, localPeers map[string]struct{}) []ChannelOpenCandidateItem {
	if len(items) == 0 || len(localPeers) == 0 {
		return items
	}
	filtered := items[:0]
	for _, item := range items {
		if _, ok := localPeers[graphExplorerNormalizePubkey(item.PeerPubkey)]; ok {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func filterChannelOpenRouteSignalsForLocalPeers(signals map[string]channelOpenRouteSignal, localPeers map[string]struct{}) {
	if len(signals) == 0 || len(localPeers) == 0 {
		return
	}
	for pubkey := range signals {
		if _, ok := localPeers[graphExplorerNormalizePubkey(pubkey)]; ok {
			delete(signals, pubkey)
		}
	}
}

func filterChannelOpenNeighborSignalsForLocalPeers(signals map[string]channelOpenNeighborSignal, localPeers map[string]struct{}) {
	if len(signals) == 0 || len(localPeers) == 0 {
		return
	}
	for pubkey := range signals {
		if _, ok := localPeers[graphExplorerNormalizePubkey(pubkey)]; ok {
			delete(signals, pubkey)
		}
	}
}

func deleteChannelOpenCandidatesForLocalPeers(items map[string]*ChannelOpenCandidateItem, localPeers map[string]struct{}) {
	if len(items) == 0 || len(localPeers) == 0 {
		return
	}
	for pubkey := range items {
		if _, ok := localPeers[graphExplorerNormalizePubkey(pubkey)]; ok {
			delete(items, pubkey)
		}
	}
}

func (s *ChannelOpenCandidatesService) fetchRouteSignals(ctx context.Context, since time.Time, selfPubkey string) (map[string]channelOpenRouteSignal, error) {
	rows, err := s.db.Query(ctx, `
with local_open_peers as (
  select distinct lower(peer_pubkey) as pubkey
  from channel_rankings
  where btrim(coalesce(peer_pubkey, '')) <> ''
),
route_hop_signal as (
  select lower(h.node_pubkey) as pubkey,
    count(*) filter (where a.attempt_status='SUCCEEDED')::int as route_hit_count_30d,
    coalesce(sum(case when a.attempt_status='SUCCEEDED' then h.amt_to_forward_msat / 1000 else 0 end), 0)::bigint as route_volume_sat_30d,
    coalesce(sum(case when a.attempt_status='SUCCEEDED' then h.cost_to_msat else 0 end), 0)::bigint as route_cost_to_msat_30d,
    count(*) filter (where a.attempt_status='SUCCEEDED' and a.payment_type in ('lightning', 'keysend'))::int as payment_hit_count_30d,
    count(*) filter (where a.attempt_status='SUCCEEDED' and a.payment_type='rebalance')::int as rebalance_hit_count_30d
  from payment_route_hops h
  join payment_route_attempts a
    on a.payment_hash = h.payment_hash
   and a.attempt_id = h.attempt_id
  where a.payment_created_at >= $1
    and a.payment_type <> 'probe'
    and btrim(coalesce(h.node_pubkey, '')) <> ''
    and not h.is_first_hop
    and lower(h.node_pubkey) <> $2
    and not exists (
      select 1 from local_open_peers lop where lop.pubkey = lower(h.node_pubkey)
    )
  group by lower(h.node_pubkey)
),
rebalance_failed_hop_signal as (
  select lower(a.fail_hop_pubkey) as pubkey,
    count(*)::int as failed_attempts_30d
  from rebalance_attempts a
  where coalesce(a.finished_at, a.started_at) >= $1
    and lower(coalesce(a.status, '')) <> 'succeeded'
    and btrim(coalesce(a.fail_hop_pubkey, '')) <> ''
    and lower(a.fail_hop_pubkey) <> $2
    and not exists (
      select 1 from local_open_peers lop where lop.pubkey = lower(a.fail_hop_pubkey)
    )
  group by lower(a.fail_hop_pubkey)
),
candidate_pubkeys as (
  select pubkey from route_hop_signal
  union
  select pubkey from rebalance_failed_hop_signal
)
select c.pubkey,
  coalesce(r.route_hit_count_30d, 0) as route_hit_count_30d,
  coalesce(r.route_volume_sat_30d, 0) as route_volume_sat_30d,
  coalesce(r.route_cost_to_msat_30d, 0) as route_cost_to_msat_30d,
  coalesce(f.failed_attempts_30d, 0) as failed_attempts_30d,
  coalesce(r.payment_hit_count_30d, 0) as payment_hit_count_30d,
  coalesce(r.rebalance_hit_count_30d, 0) as rebalance_hit_count_30d
from candidate_pubkeys c
left join route_hop_signal r on r.pubkey = c.pubkey
left join rebalance_failed_hop_signal f on f.pubkey = c.pubkey
order by route_cost_to_msat_30d desc, route_volume_sat_30d desc, route_hit_count_30d desc, failed_attempts_30d desc
limit $3
`, since.UTC(), graphExplorerNormalizePubkey(selfPubkey), channelOpenCandidatesRouteSeedLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]channelOpenRouteSignal)
	for rows.Next() {
		var item channelOpenRouteSignal
		if err := rows.Scan(
			&item.PubKey,
			&item.RouteHitCount30d,
			&item.RouteVolumeSat30d,
			&item.RouteCostToMsat30d,
			&item.FailedAttempts30d,
			&item.PaymentHitCount30d,
			&item.RebalanceHitCount30d,
		); err != nil {
			return nil, err
		}
		out[graphExplorerNormalizePubkey(item.PubKey)] = item
	}
	return out, rows.Err()
}

func summarizeChannelOpenRouteCoverage(signals map[string]channelOpenRouteSignal) channelOpenRouteCoverage {
	var out channelOpenRouteCoverage
	for _, signal := range signals {
		hasDirectEvidence := signal.RouteHitCount30d > 0 ||
			signal.RouteVolumeSat30d > 0 ||
			signal.PaymentHitCount30d > 0 ||
			signal.RebalanceHitCount30d > 0 ||
			signal.FailedAttempts30d > 0
		if !hasDirectEvidence {
			continue
		}
		out.DirectEvidenceNodes++
		out.SuccessfulHits += signal.RouteHitCount30d
	}
	return out
}

func (s *ChannelOpenCandidatesService) fetchNeighborSignals(ctx context.Context, selfPubkey string, strong bool) (map[string]channelOpenNeighborSignal, error) {
	baseWhere := `
state='expand' and score >= 72
`
	limit := channelOpenCandidatesStrongLimit
	if !strong {
		baseWhere = `
(state='close'
  or rebalance_dependence_score >= 65
  or score < 48
  or local_balance_pct <= 10
  or local_balance_pct >= 90
  or rebal_fee_7d_sat > forward_fee_7d_sat)
`
		limit = channelOpenCandidatesProblemLimit
	}

	rows, err := s.db.Query(ctx, `
with local_open_peers as (
  select distinct lower(peer_pubkey) as pubkey
  from channel_rankings
  where btrim(coalesce(peer_pubkey, '')) <> ''
),
base_peers as (
  select distinct lower(peer_pubkey) as peer_pubkey
  from channel_rankings
  where btrim(coalesce(peer_pubkey, '')) <> ''
    and `+baseWhere+`
)
select pubkey, shared_peer_count, shared_capacity_sat
from (
  select lower(case
      when lower(ch.node1_pubkey) = bp.peer_pubkey then ch.node2_pubkey
      else ch.node1_pubkey
    end) as pubkey,
    count(*)::int as shared_peer_count,
    coalesce(sum(ch.capacity_sat), 0)::bigint as shared_capacity_sat
  from base_peers bp
  join graph_channels ch
    on ch.status='open'
   and (lower(ch.node1_pubkey) = bp.peer_pubkey or lower(ch.node2_pubkey) = bp.peer_pubkey)
  group by 1
) q
where btrim(coalesce(pubkey, '')) <> ''
  and pubkey <> $1
  and not exists (
    select 1 from local_open_peers lop where lop.pubkey = q.pubkey
  )
order by shared_capacity_sat desc, shared_peer_count desc, pubkey asc
limit $2
`, graphExplorerNormalizePubkey(selfPubkey), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]channelOpenNeighborSignal)
	for rows.Next() {
		var item channelOpenNeighborSignal
		if err := rows.Scan(&item.PubKey, &item.SharedPeerCount, &item.SharedCapacitySat); err != nil {
			return nil, err
		}
		out[graphExplorerNormalizePubkey(item.PubKey)] = item
	}
	return out, rows.Err()
}

func (s *ChannelOpenCandidatesService) applyGraphMetrics(ctx context.Context, items map[string]*ChannelOpenCandidateItem) error {
	pubkeys := make([]string, 0, len(items))
	for pubkey := range items {
		if pubkey == "" {
			continue
		}
		pubkeys = append(pubkeys, pubkey)
	}
	if len(pubkeys) == 0 {
		return nil
	}

	rows, err := s.db.Query(ctx, `
with graph_policy as (
  select lower(advertising_pubkey) as pubkey,
    coalesce(min(fee_rate_ppm) filter (where not disabled), 0)::bigint as best_outbound_fee_ppm,
    coalesce(min(inbound_fee_rate_ppm) filter (where not disabled), 0)::bigint as best_inbound_fee_ppm
  from graph_channel_policy_current
  group by lower(advertising_pubkey)
)
select lower(n.pubkey), coalesce(n.alias, ''), n.channel_count, n.total_capacity_sat,
  coalesce(p.best_outbound_fee_ppm, 0), coalesce(p.best_inbound_fee_ppm, 0)
from graph_nodes n
left join graph_policy p on p.pubkey = lower(n.pubkey)
where lower(n.pubkey) = any($1::text[])
`, pubkeys)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var metric channelOpenGraphMetric
		if err := rows.Scan(
			&metric.PubKey,
			&metric.PeerAlias,
			&metric.GraphChannelCount,
			&metric.GraphTotalCapacity,
			&metric.BestOutboundFeePpm,
			&metric.BestInboundFeePpm,
		); err != nil {
			return err
		}
		item := items[graphExplorerNormalizePubkey(metric.PubKey)]
		if item == nil {
			continue
		}
		item.PeerAlias = strings.TrimSpace(metric.PeerAlias)
		item.GraphChannelCount = metric.GraphChannelCount
		item.GraphTotalCapacitySat = metric.GraphTotalCapacity
		item.BestOutboundFeePpm = metric.BestOutboundFeePpm
		item.BestInboundFeePpm = metric.BestInboundFeePpm
	}
	return rows.Err()
}

func computeChannelOpenCandidateScore(item *ChannelOpenCandidateItem) (int, int, int, int, int) {
	if item == nil {
		return 0, 0, 0, 0, 0
	}

	routeEvidence := channelOpenMinInt(30, item.RouteHitCount30d*3)
	routeVolume := channelOpenMinInt(20, int(item.RouteVolumeSat30d/250_000))
	routeCost := channelOpenMinInt(18, item.RouteCostPpm30d/50)
	rebalanceBonus := channelOpenMinInt(10, item.RebalanceHitCount30d*2)
	problemBonus := channelOpenMinInt(18, item.SharedProblemPeerCount*4+int(item.SharedProblemCapacitySat/25_000_000))
	strongBonus := channelOpenMinInt(10, item.SharedStrongPeerCount*2+int(item.SharedStrongCapacitySat/75_000_000))

	graphPresence := 0
	if item.GraphChannelCount >= 5 {
		graphPresence += channelOpenMinInt(6, 1+item.GraphChannelCount/20)
	}
	if item.GraphTotalCapacitySat >= 25_000_000 {
		graphPresence += channelOpenMinInt(6, 1+int(item.GraphTotalCapacitySat/150_000_000))
	}

	feeBonus := 0
	switch {
	case item.GraphChannelCount > 0 && item.BestOutboundFeePpm >= 0 && item.BestOutboundFeePpm <= 250:
		feeBonus = 8
	case item.GraphChannelCount > 0 && item.BestOutboundFeePpm > 250 && item.BestOutboundFeePpm <= 1000:
		feeBonus = 4
	}

	failurePenalty := 0
	if item.FailedAttempts30d >= 4 && item.FailedAttempts30d >= item.RouteHitCount30d {
		failurePenalty = channelOpenMinInt(10, item.FailedAttempts30d-item.RouteHitCount30d+4)
	}

	rawDemand := routeEvidence + routeVolume + routeCost + rebalanceBonus - failurePenalty
	demandScore := channelOpenClampInt((rawDemand*100)/78, 0, 100)

	rawRelief := problemBonus + strongBonus + channelOpenMinInt(5, rebalanceBonus/2)
	reliefScore := channelOpenClampInt((rawRelief*100)/33, 0, 100)

	rawGraphQuality := graphPresence + feeBonus
	graphQualityScore := channelOpenClampInt((rawGraphQuality*100)/20, 0, 100)

	score := routeEvidence + routeVolume + routeCost + rebalanceBonus + problemBonus + strongBonus + graphPresence + feeBonus - failurePenalty
	score = channelOpenClampInt(score, 0, 100)

	confidence := routeEvidence + channelOpenMinInt(16, routeVolume) + channelOpenMinInt(14, problemBonus+strongBonus)
	if item.RouteHitCount30d > 0 {
		confidence += 10
	}
	if item.GraphChannelCount > 0 {
		confidence += 15
	}
	confidence = channelOpenClampInt(confidence, 0, 100)

	return demandScore, reliefScore, graphQualityScore, score, confidence
}

func buildChannelOpenCandidateReasons(item *ChannelOpenCandidateItem) []ChannelOpenCandidateReason {
	reasons := make([]ChannelOpenCandidateReason, 0, 6)
	if item == nil {
		return reasons
	}
	if item.RouteHitCount30d > 0 {
		reasons = appendUniqueChannelOpenCandidateReason(reasons, ChannelOpenCandidateReason{Code: "route_flow_observed"})
	}
	if item.RouteCostPpm30d >= 150 {
		reasons = appendUniqueChannelOpenCandidateReason(reasons, ChannelOpenCandidateReason{Code: "route_cost_high"})
	}
	if item.RebalanceHitCount30d > 0 {
		reasons = appendUniqueChannelOpenCandidateReason(reasons, ChannelOpenCandidateReason{Code: "rebalance_path_observed"})
	}
	if item.SharedProblemPeerCount > 0 {
		reasons = appendUniqueChannelOpenCandidateReason(reasons, ChannelOpenCandidateReason{Code: "adjacent_to_problem_peer"})
	}
	if item.SharedStrongPeerCount > 0 {
		reasons = appendUniqueChannelOpenCandidateReason(reasons, ChannelOpenCandidateReason{Code: "adjacent_to_strong_peer"})
	}
	if item.GraphChannelCount >= 10 || item.GraphTotalCapacitySat >= 50_000_000 {
		reasons = appendUniqueChannelOpenCandidateReason(reasons, ChannelOpenCandidateReason{Code: "graph_presence_strong"})
	}
	if item.GraphChannelCount > 0 && item.BestOutboundFeePpm >= 0 && item.BestOutboundFeePpm <= 500 {
		reasons = appendUniqueChannelOpenCandidateReason(reasons, ChannelOpenCandidateReason{Code: "public_policy_competitive"})
	}
	if item.FailedAttempts30d >= 5 {
		reasons = appendUniqueChannelOpenCandidateReason(reasons, ChannelOpenCandidateReason{Code: "failed_routes_observed"})
	}
	return reasons
}

func shouldKeepChannelOpenCandidate(item *ChannelOpenCandidateItem, strictAdjacencyOnly bool) bool {
	if item == nil {
		return false
	}
	if strings.TrimSpace(item.PeerPubkey) == "" {
		return false
	}
	if item.Score < 18 || item.Confidence < 12 {
		return false
	}
	hasDirectDemand := item.RouteHitCount30d > 0 ||
		item.RouteVolumeSat30d > 0 ||
		item.PaymentHitCount30d > 0 ||
		item.RebalanceHitCount30d > 0 ||
		item.FailedAttempts30d > 0
	if hasDirectDemand {
		return true
	}

	meaningfulRelief := item.SharedProblemPeerCount >= 2 ||
		item.SharedProblemCapacitySat >= 50_000_000 ||
		item.ReliefScore >= 45
	strongGraph := item.GraphQualityScore >= 35 && item.GraphChannelCount >= 5

	if strictAdjacencyOnly {
		return meaningfulRelief && strongGraph && item.Score >= 28
	}
	return meaningfulRelief && strongGraph && item.Score >= 24
}

func appendUniqueChannelOpenCandidateReason(list []ChannelOpenCandidateReason, candidate ChannelOpenCandidateReason) []ChannelOpenCandidateReason {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item.Code), strings.TrimSpace(candidate.Code)) {
			return list
		}
	}
	return append(list, candidate)
}

func channelOpenCostPpm(costToMsat int64, volumeSat int64) int {
	if costToMsat <= 0 || volumeSat <= 0 {
		return 0
	}
	return int((costToMsat * 1000) / volumeSat)
}

func channelOpenMinInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func channelOpenClampInt(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func (s *ChannelOpenCandidatesService) localPubkey(ctx context.Context) string {
	if s == nil || s.lnd == nil {
		return ""
	}
	if cached := graphExplorerNormalizePubkey(s.lnd.CachedPubkey()); cached != "" {
		return cached
	}
	loadCtx := ctx
	cancel := func() {}
	if ctx == nil {
		loadCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	} else {
		loadCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	}
	defer cancel()
	status, err := s.lnd.GetStatus(loadCtx)
	if err != nil {
		return ""
	}
	return graphExplorerNormalizePubkey(status.Pubkey)
}

func (s *ChannelOpenCandidatesService) persistSnapshot(ctx context.Context, items []ChannelOpenCandidateItem, computedAt time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `delete from channel_open_candidates`); err != nil {
		return err
	}

	for _, item := range items {
		reasonsJSON, _ := marshalChannelOpenCandidateReasons(item.Reasons)
		_, err := tx.Exec(ctx, `
insert into channel_open_candidates (
  peer_pubkey, peer_alias, route_hit_count_30d, route_volume_sat_30d, route_cost_to_msat_30d,
  route_cost_ppm_30d, failed_attempts_30d, payment_hit_count_30d, rebalance_hit_count_30d,
  shared_problem_peer_count, shared_problem_capacity_sat, shared_strong_peer_count, shared_strong_capacity_sat,
  graph_channel_count, graph_total_capacity_sat, best_outbound_fee_ppm, best_inbound_fee_ppm,
  demand_score, relief_score, graph_quality_score,
  score, confidence, reasons_json, computed_at
) values (
  $1,$2,$3,$4,$5,
  $6,$7,$8,$9,
  $10,$11,$12,$13,
  $14,$15,$16,$17,
  $18,$19,$20,
  $21,$22,$23::jsonb,$24
)
`, item.PeerPubkey, nullableString(item.PeerAlias), item.RouteHitCount30d, item.RouteVolumeSat30d, item.RouteCostToMsat30d,
			item.RouteCostPpm30d, item.FailedAttempts30d, item.PaymentHitCount30d, item.RebalanceHitCount30d,
			item.SharedProblemPeerCount, item.SharedProblemCapacitySat, item.SharedStrongPeerCount, item.SharedStrongCapacitySat,
			item.GraphChannelCount, item.GraphTotalCapacitySat, item.BestOutboundFeePpm, item.BestInboundFeePpm,
			item.DemandScore, item.ReliefScore, item.GraphQualityScore,
			item.Score, item.Confidence, string(reasonsJSON), computedAt)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(ctx, `
update channel_open_candidate_sync_state
set last_sync_at=$1,
    last_error='',
    candidate_count=$2,
    updated_at=now()
where id=true
`, computedAt, len(items))
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *ChannelOpenCandidatesService) recordSyncError(ctx context.Context, msg string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(ctx, `
update channel_open_candidate_sync_state
set last_error=$1,
    updated_at=now()
where id=true
`, strings.TrimSpace(msg))
	return err
}

func (s *ChannelOpenCandidatesService) persistedLastSyncAt(ctx context.Context) (*time.Time, error) {
	var lastSyncAt sql.NullTime
	err := s.db.QueryRow(ctx, `
select last_sync_at
from channel_open_candidate_sync_state
where id=true
`).Scan(&lastSyncAt)
	if err != nil {
		return nil, err
	}
	if !lastSyncAt.Valid {
		return nil, nil
	}
	ts := lastSyncAt.Time.UTC()
	return &ts, nil
}

type channelOpenCandidateRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanChannelOpenCandidateItems(rows channelOpenCandidateRows) ([]ChannelOpenCandidateItem, error) {
	items := make([]ChannelOpenCandidateItem, 0)
	for rows.Next() {
		var item ChannelOpenCandidateItem
		var peerAlias sql.NullString
		var reasonsRaw []byte
		if err := rows.Scan(
			&item.PeerPubkey,
			&peerAlias,
			&item.RouteHitCount30d,
			&item.RouteVolumeSat30d,
			&item.RouteCostToMsat30d,
			&item.RouteCostPpm30d,
			&item.FailedAttempts30d,
			&item.PaymentHitCount30d,
			&item.RebalanceHitCount30d,
			&item.SharedProblemPeerCount,
			&item.SharedProblemCapacitySat,
			&item.SharedStrongPeerCount,
			&item.SharedStrongCapacitySat,
			&item.GraphChannelCount,
			&item.GraphTotalCapacitySat,
			&item.BestOutboundFeePpm,
			&item.BestInboundFeePpm,
			&item.DemandScore,
			&item.ReliefScore,
			&item.GraphQualityScore,
			&item.Score,
			&item.Confidence,
			&reasonsRaw,
			&item.ComputedAt,
		); err != nil {
			return nil, err
		}
		item.PeerPubkey = graphExplorerNormalizePubkey(item.PeerPubkey)
		if peerAlias.Valid {
			item.PeerAlias = strings.TrimSpace(peerAlias.String)
		}
		if len(reasonsRaw) > 0 {
			_ = json.Unmarshal(reasonsRaw, &item.Reasons)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func marshalChannelOpenCandidateReasons(reasons []ChannelOpenCandidateReason) ([]byte, error) {
	if len(reasons) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(reasons)
}
