package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	graphExplorerReconcileInterval   = 6 * time.Hour
	graphExplorerStreamRetryDelay    = 15 * time.Second
	graphExplorerRefreshTimeout      = 3 * time.Minute
	graphExplorerUpdateTimeout       = 45 * time.Second
	graphExplorerGraphReadyTimeout   = 5 * time.Second
	graphExplorerStartupRefreshDelay = 5 * time.Minute
	graphCloseClassifierInterval     = 2 * time.Minute
	graphCloseClassifierTimeout      = 45 * time.Second
	graphCloseClassifierBatchSize    = 32
	graphCloseClassifierMaxAttempts  = 3
	graphExplorerBatchSize           = 500
	graphExplorerConfigID            = 1
)

const (
	// graphExplorerPolicyHistoryRetentionDays bounds how much network-wide
	// policy history we keep. Nothing reads past this horizon (the fee report
	// caps at 89d, autofee at 21d), so 90 days keeps every consumer fully fed
	// while preventing graph_channel_policy_history from growing unbounded.
	graphExplorerPolicyHistoryRetentionDays = 90
	graphExplorerPolicyHistoryPruneBatch    = 50000
	graphExplorerPruneTimeout               = 10 * time.Minute
)

var (
	ErrGraphExplorerDBUnavailable  = errors.New("graph explorer db unavailable")
	ErrGraphExplorerGraphNotSynced = errors.New("lnd graph not synced")
)

type GraphExplorerStatus struct {
	Available             bool       `json:"available"`
	Running               bool       `json:"running"`
	FirstNativeCoverageAt *time.Time `json:"first_native_coverage_at,omitempty"`
	LastBootstrapAt       *time.Time `json:"last_bootstrap_at,omitempty"`
	LastStreamEventAt     *time.Time `json:"last_stream_event_at,omitempty"`
	LastReconcileAt       *time.Time `json:"last_reconcile_at,omitempty"`
	LastSnapshotAt        *time.Time `json:"last_snapshot_at,omitempty"`
	LastRefillAt          *time.Time `json:"last_refill_at,omitempty"`
	LastRefillProvider    string     `json:"last_refill_provider,omitempty"`
	LastSyncAt            *time.Time `json:"last_sync_at,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
	RefillEnabled         bool       `json:"refill_enabled"`
	RefillAvailable       bool       `json:"refill_available"`
	NodeCount             int        `json:"node_count"`
	ChannelCount          int        `json:"channel_count"`
	OpenChannelCount      int        `json:"open_channel_count"`
	ClosedChannelCount    int        `json:"closed_channel_count"`
}

type GraphExplorerService struct {
	db     *pgxpool.Pool
	logger *log.Logger
	lnd    *lndclient.Client

	syncMu sync.Mutex
	mu     sync.Mutex

	lastSyncAt time.Time
	lastError  string
	stopCh     chan struct{}
	doneCh     chan struct{}
}

type graphPolicyKey struct {
	ChanID            uint64
	AdvertisingPubKey string
}

type graphPolicySnapshot struct {
	ConnectingPubKey   string
	FeeBaseMsat        int64
	FeeRatePpm         int64
	TimeLockDelta      uint32
	MinHtlcMsat        int64
	MaxHtlcMsat        uint64
	Disabled           bool
	InboundBaseMsat    int64
	InboundFeeRatePpm  int64
	PolicyLastUpdateAt time.Time
}

func NewGraphExplorerService(db *pgxpool.Pool, logger *log.Logger, lnd *lndclient.Client) *GraphExplorerService {
	return &GraphExplorerService{
		db:     db,
		logger: logger,
		lnd:    lnd,
	}
}

func (s *GraphExplorerService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrGraphExplorerDBUnavailable
	}
	_, err := s.db.Exec(ctx, `
create table if not exists graph_explorer_config (
  id integer primary key,
  enabled boolean not null default true,
  refill_enabled boolean not null default false,
  refill_provider text,
  history_retention_days integer not null default 365,
  reconcile_interval_sec integer not null default 21600,
  snapshot_interval_sec integer not null default 86400,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
insert into graph_explorer_config (id)
values (`+fmt.Sprintf("%d", graphExplorerConfigID)+`)
on conflict (id) do nothing;

create table if not exists graph_sync_state (
  id boolean primary key default true,
  first_native_coverage_at timestamptz,
  last_bootstrap_at timestamptz,
  last_stream_event_at timestamptz,
  last_reconcile_at timestamptz,
  last_snapshot_at timestamptz,
  last_refill_at timestamptz,
  last_refill_provider text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
insert into graph_sync_state (id)
values (true)
on conflict (id) do nothing;

create table if not exists graph_nodes (
  pubkey text primary key,
  alias text,
  color text,
  addresses_json jsonb not null default '[]'::jsonb,
  features_json jsonb not null default '{}'::jsonb,
  channel_count integer not null default 0,
  total_capacity_sat bigint not null default 0,
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  last_graph_update_at timestamptz,
  last_indexed_at timestamptz not null default now(),
  source text not null default 'native'
);
create index if not exists graph_nodes_alias_idx on graph_nodes (lower(alias));
create index if not exists graph_nodes_last_seen_idx on graph_nodes (last_seen_at desc);

create table if not exists graph_channels (
  chan_id bigint primary key,
  chan_point text unique,
  node1_pubkey text,
  node2_pubkey text,
  capacity_sat bigint not null default 0,
  open_block_height integer not null default 0,
  status text not null default 'open',
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  closed_at timestamptz,
  closed_height integer,
  close_source text,
  close_type text,
  last_indexed_at timestamptz not null default now()
);
alter table graph_channels add column if not exists close_txid text;
alter table graph_channels add column if not exists close_confidence text;
alter table graph_channels add column if not exists classified_at timestamptz;
create index if not exists graph_channels_status_idx on graph_channels (status, last_seen_at desc);
create index if not exists graph_channels_node1_idx on graph_channels (node1_pubkey, status);
create index if not exists graph_channels_node2_idx on graph_channels (node2_pubkey, status);

create table if not exists graph_channel_policy_current (
  chan_id bigint not null,
  advertising_pubkey text not null,
  connecting_pubkey text not null,
  fee_base_msat bigint not null default 0,
  fee_rate_ppm bigint not null default 0,
  time_lock_delta integer not null default 0,
  min_htlc_msat bigint not null default 0,
  max_htlc_msat bigint not null default 0,
  disabled boolean not null default false,
  inbound_base_msat bigint not null default 0,
  inbound_fee_rate_ppm bigint not null default 0,
  policy_last_update_at timestamptz not null,
  last_indexed_at timestamptz not null default now(),
  primary key (chan_id, advertising_pubkey)
);
create index if not exists graph_channel_policy_current_connecting_idx on graph_channel_policy_current (connecting_pubkey, policy_last_update_at desc);

create table if not exists graph_channel_policy_history (
  id bigserial primary key,
  chan_id bigint not null,
  advertising_pubkey text not null,
  connecting_pubkey text not null,
  fee_base_msat bigint not null default 0,
  fee_rate_ppm bigint not null default 0,
  time_lock_delta integer not null default 0,
  min_htlc_msat bigint not null default 0,
  max_htlc_msat bigint not null default 0,
  disabled boolean not null default false,
  inbound_base_msat bigint not null default 0,
  inbound_fee_rate_ppm bigint not null default 0,
  captured_at timestamptz not null,
  source text not null default 'native'
);
create index if not exists graph_channel_policy_history_lookup_idx on graph_channel_policy_history (chan_id, advertising_pubkey, captured_at desc);
create index if not exists graph_channel_policy_history_advertising_pubkey_idx on graph_channel_policy_history (advertising_pubkey, captured_at desc);
create index if not exists graph_channel_policy_history_connecting_pubkey_idx on graph_channel_policy_history (connecting_pubkey, captured_at desc);
create index if not exists graph_channel_policy_history_captured_at_idx on graph_channel_policy_history (captured_at);

create table if not exists graph_close_events (
  id bigserial primary key,
  chan_id bigint not null,
  chan_point text,
  node1_pubkey text,
  node2_pubkey text,
  capacity_sat bigint not null default 0,
  closed_height integer not null default 0,
  observed_at timestamptz not null,
  close_source text not null default 'native',
  close_type text,
  metadata_json jsonb not null default '{}'::jsonb
);
alter table graph_close_events add column if not exists close_txid text;
alter table graph_close_events add column if not exists close_fee_sat bigint;
alter table graph_close_events add column if not exists close_classifier text;
alter table graph_close_events add column if not exists close_confidence text;
alter table graph_close_events add column if not exists close_reason text;
alter table graph_close_events add column if not exists classified_at timestamptz;
alter table graph_close_events add column if not exists classification_error text;
alter table graph_close_events add column if not exists classification_attempts integer not null default 0;
create index if not exists graph_close_events_chan_idx on graph_close_events (chan_id, observed_at desc);
create index if not exists graph_close_events_pending_idx on graph_close_events (classified_at, classification_attempts, observed_at desc);
create index if not exists graph_close_events_close_txid_idx on graph_close_events (close_txid);
`)
	return err
}

func (s *GraphExplorerService) Start() {
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
		var wg sync.WaitGroup
		wg.Add(4)
		go func() {
			defer wg.Done()
			s.runStartupRefresh(stopCh)
		}()
		go func() {
			defer wg.Done()
			s.runRefreshLoop(stopCh)
		}()
		go func() {
			defer wg.Done()
			s.runStreamLoop(stopCh)
		}()
		go func() {
			defer wg.Done()
			s.runCloseClassifierLoop(stopCh)
		}()
		wg.Wait()
	}()
}

func (s *GraphExplorerService) runStartupRefresh(stopCh <-chan struct{}) {
	if s == nil || s.db == nil || s.lnd == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	warmState := s.hasWarmState(ctx)
	cancel()
	if warmState {
		if !sleepWithStop(stopCh, graphExplorerStartupRefreshDelay) {
			return
		}
	}

	s.refreshBackground()
}

func (s *GraphExplorerService) runRefreshLoop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(graphExplorerReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			s.refreshBackground()
		}
	}
}

func (s *GraphExplorerService) refreshBackground() {
	if s == nil || s.db == nil || s.lnd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), graphExplorerRefreshTimeout)
	defer cancel()
	if err := s.Refresh(ctx); err != nil {
		if errors.Is(err, ErrGraphExplorerGraphNotSynced) {
			s.clearError()
			return
		}
		s.recordError(err)
		if s.logger != nil {
			s.logger.Printf("graph explorer refresh failed: %v", err)
		}
		return
	}
	s.clearError()

	pruneCtx, pruneCancel := context.WithTimeout(context.Background(), graphExplorerPruneTimeout)
	s.prunePolicyHistory(pruneCtx)
	pruneCancel()
}

// prunePolicyHistory enforces the 90-day retention horizon on
// graph_channel_policy_history. It runs on the reconcile cadence (startup + 6h),
// not on the manual refresh path, so spamming the UI button can't trigger it.
func (s *GraphExplorerService) prunePolicyHistory(ctx context.Context) {
	if s == nil || s.db == nil {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -graphExplorerPolicyHistoryRetentionDays)
	// Batched delete so a large backlog never holds a long lock. Each pass
	// removes up to graphExplorerPolicyHistoryPruneBatch rows; stop once a pass
	// deletes fewer than a full batch (nothing left past the horizon).
	for iter := 0; iter < 1000; iter++ {
		tag, err := s.db.Exec(ctx, `
delete from graph_channel_policy_history
where ctid in (
  select ctid from graph_channel_policy_history
  where captured_at < $1
  limit $2
)
`, cutoff, graphExplorerPolicyHistoryPruneBatch)
		if err != nil {
			if s.logger != nil {
				s.logger.Printf("graph explorer policy history prune failed: %v", err)
			}
			return
		}
		if tag.RowsAffected() < int64(graphExplorerPolicyHistoryPruneBatch) {
			return
		}
	}
}

func (s *GraphExplorerService) runStreamLoop(stopCh <-chan struct{}) {
	if s == nil || s.db == nil || s.lnd == nil {
		return
	}
	for {
		select {
		case <-stopCh:
			return
		default:
		}

		readyCtx, readyCancel := context.WithTimeout(context.Background(), graphExplorerGraphReadyTimeout)
		readyErr := s.requireNativeGraphReady(readyCtx)
		readyCancel()
		if readyErr != nil {
			if errors.Is(readyErr, ErrGraphExplorerGraphNotSynced) {
				s.clearError()
			} else {
				s.recordError(readyErr)
				if s.logger != nil {
					s.logger.Printf("graph explorer graph sync check failed: %v", readyErr)
				}
			}
			if !sleepWithStop(stopCh, graphExplorerStreamRetryDelay) {
				return
			}
			continue
		}
		coverageCtx, coverageCancel := context.WithTimeout(context.Background(), graphExplorerGraphReadyTimeout)
		hasCoverage := s.hasNativeCoverage(coverageCtx)
		coverageCancel()
		if !hasCoverage {
			s.refreshBackground()
		}

		ctx, cancel := context.WithCancel(context.Background())
		sub, err := s.lnd.SubscribeChannelGraph(ctx)
		if err != nil {
			cancel()
			s.recordError(err)
			if s.logger != nil {
				s.logger.Printf("graph explorer stream connect failed: %v", err)
			}
			if !sleepWithStop(stopCh, graphExplorerStreamRetryDelay) {
				return
			}
			continue
		}

		for {
			update, recvErr := sub.Recv()
			if recvErr != nil {
				_ = sub.Close()
				cancel()
				if errors.Is(recvErr, context.Canceled) || errors.Is(recvErr, io.EOF) {
					break
				}
				s.recordError(recvErr)
				if s.logger != nil {
					s.logger.Printf("graph explorer stream recv failed: %v", recvErr)
				}
				break
			}

			updateCtx, updateCancel := context.WithTimeout(context.Background(), graphExplorerUpdateTimeout)
			applyErr := s.applyGraphUpdate(updateCtx, update)
			updateCancel()
			if applyErr != nil {
				s.recordError(applyErr)
				if s.logger != nil {
					s.logger.Printf("graph explorer stream apply failed: %v", applyErr)
				}
				break
			}
			s.clearError()
		}

		if !sleepWithStop(stopCh, graphExplorerStreamRetryDelay) {
			return
		}
	}
}

func (s *GraphExplorerService) Refresh(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrGraphExplorerDBUnavailable
	}
	if s.lnd == nil {
		return errors.New("lnd unavailable")
	}
	if err := s.requireNativeGraphReady(ctx); err != nil {
		return err
	}

	refreshStartedAt := time.Now().UTC()
	snapshot, err := s.lnd.DescribeGraph(ctx)
	if err != nil {
		return err
	}

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	currentPolicies, err := loadCurrentGraphPolicies(ctx, tx)
	if err != nil {
		return err
	}

	if err := upsertGraphNodesSnapshot(ctx, tx, snapshot.Nodes, refreshStartedAt); err != nil {
		return err
	}
	if err := upsertGraphChannelsSnapshot(ctx, tx, snapshot.Channels, refreshStartedAt); err != nil {
		return err
	}
	if err := upsertGraphPoliciesSnapshot(ctx, tx, snapshot.Channels, refreshStartedAt, currentPolicies); err != nil {
		return err
	}
	localPubkey := s.loadLocalPubkey(ctx)
	if err := s.reconcileLocalOpenChannels(ctx, tx, localPubkey, refreshStartedAt); err != nil {
		return err
	}
	if err := s.reconcileLocalClosedChannels(ctx, tx, refreshStartedAt); err != nil {
		return err
	}
	if err := reconcileSnapshotClosedChannels(ctx, tx, refreshStartedAt, localPubkey); err != nil {
		return err
	}
	if err := recomputeGraphNodeAggregates(ctx, tx, refreshStartedAt); err != nil {
		return err
	}
	if err := upsertGraphSyncStateRefresh(ctx, tx, refreshStartedAt); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.setLastSyncAt(refreshStartedAt)
	return nil
}

func (s *GraphExplorerService) applyGraphUpdate(ctx context.Context, update lndclient.GraphUpdate) error {
	if s == nil || s.db == nil {
		return ErrGraphExplorerDBUnavailable
	}

	observedAt := update.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	affectedPubKeys := graphExplorerCollectAffectedPubKeysFromChannelUpdates(update.ChannelUpdates)
	closedChanIDs := graphExplorerCollectClosedChannelIDs(update.ClosedChannels)

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := upsertGraphNodeUpdates(ctx, tx, update.NodeUpdates, observedAt); err != nil {
		return err
	}
	if err := applyGraphChannelUpdates(ctx, tx, update.ChannelUpdates, observedAt); err != nil {
		return err
	}
	if err := applyGraphClosedChannelUpdates(ctx, tx, update.ClosedChannels, observedAt); err != nil {
		return err
	}
	if len(closedChanIDs) > 0 {
		closedPubKeys, err := loadGraphChannelPubKeysByChanIDs(ctx, tx, closedChanIDs)
		if err != nil {
			return err
		}
		affectedPubKeys = append(affectedPubKeys, closedPubKeys...)
	}
	if len(affectedPubKeys) > 0 {
		if err := recomputeGraphNodeAggregatesForPubKeys(ctx, tx, observedAt, affectedPubKeys); err != nil {
			return err
		}
	} else if len(update.ChannelUpdates) > 0 || len(update.ClosedChannels) > 0 {
		if err := recomputeGraphNodeAggregates(ctx, tx, observedAt); err != nil {
			return err
		}
	} else if len(update.NodeUpdates) == 0 {
		if err := recomputeGraphNodeAggregates(ctx, tx, observedAt); err != nil {
			return err
		}
	}
	if err := upsertGraphSyncStateStream(ctx, tx, observedAt); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.setLastSyncAt(observedAt)
	return nil
}

func (s *GraphExplorerService) Status(ctx context.Context) (GraphExplorerStatus, error) {
	if s == nil || s.db == nil {
		return GraphExplorerStatus{}, ErrGraphExplorerDBUnavailable
	}

	status := GraphExplorerStatus{Available: true}

	s.mu.Lock()
	status.Running = s.stopCh != nil
	status.LastError = strings.TrimSpace(s.lastError)
	if !s.lastSyncAt.IsZero() {
		value := s.lastSyncAt
		status.LastSyncAt = &value
	}
	s.mu.Unlock()

	var refillEnabled bool
	err := s.db.QueryRow(ctx, `
select coalesce(refill_enabled, false)
from graph_explorer_config
where id = $1
`, graphExplorerConfigID).Scan(&refillEnabled)
	if err != nil {
		return GraphExplorerStatus{}, err
	}
	status.RefillEnabled = refillEnabled

	var firstCoverage, lastBootstrap, lastStream, lastReconcile, lastSnapshot, lastRefill *time.Time
	var lastRefillProvider string
	err = s.db.QueryRow(ctx, `
select first_native_coverage_at,
  last_bootstrap_at,
  last_stream_event_at,
  last_reconcile_at,
  last_snapshot_at,
  last_refill_at,
  coalesce(last_refill_provider, '')
from graph_sync_state
where id = true
`).Scan(&firstCoverage, &lastBootstrap, &lastStream, &lastReconcile, &lastSnapshot, &lastRefill, &lastRefillProvider)
	if err != nil {
		return GraphExplorerStatus{}, err
	}
	status.FirstNativeCoverageAt = firstCoverage
	status.LastBootstrapAt = lastBootstrap
	status.LastStreamEventAt = lastStream
	status.LastReconcileAt = lastReconcile
	status.LastSnapshotAt = lastSnapshot
	status.LastRefillAt = lastRefill
	status.LastRefillProvider = strings.TrimSpace(lastRefillProvider)

	err = s.db.QueryRow(ctx, `
select
  coalesce((select count(*) from graph_nodes), 0),
  coalesce((select count(*) from graph_channels), 0),
  coalesce((select count(*) from graph_channels where status = 'open'), 0),
  coalesce((select count(*) from graph_channels where status = 'closed'), 0)
`).Scan(&status.NodeCount, &status.ChannelCount, &status.OpenChannelCount, &status.ClosedChannelCount)
	if err != nil {
		return GraphExplorerStatus{}, err
	}

	status.RefillAvailable = graphExplorerAmbossTokenAvailable(ctx, s.db)
	return status, nil
}

func (s *GraphExplorerService) hasWarmState(ctx context.Context) bool {
	if s == nil || s.db == nil {
		return false
	}

	var nodeCount int
	if err := s.db.QueryRow(ctx, `select count(*) from graph_nodes`).Scan(&nodeCount); err != nil {
		if s.logger != nil {
			s.logger.Printf("graph explorer warm-state check failed: %v", err)
		}
		return false
	}
	return nodeCount > 0
}

func (s *GraphExplorerService) hasNativeCoverage(ctx context.Context) bool {
	if s == nil || s.db == nil {
		return false
	}

	var ready bool
	if err := s.db.QueryRow(ctx, `
select first_native_coverage_at is not null
from graph_sync_state
where id = true
`).Scan(&ready); err != nil {
		if s.logger != nil {
			s.logger.Printf("graph explorer native coverage check failed: %v", err)
		}
		return false
	}
	return ready
}

func (s *GraphExplorerService) requireNativeGraphReady(ctx context.Context) error {
	if s == nil || s.lnd == nil {
		return errors.New("lnd unavailable")
	}
	synced, err := s.lnd.SyncedToGraph(ctx)
	if err != nil {
		return err
	}
	if !synced {
		return ErrGraphExplorerGraphNotSynced
	}
	return nil
}

func upsertGraphNodesSnapshot(ctx context.Context, tx pgx.Tx, nodes []lndclient.GraphNode, observedAt time.Time) error {
	if len(nodes) == 0 {
		return nil
	}
	const query = `
insert into graph_nodes (
  pubkey, alias, color, addresses_json, features_json, channel_count, total_capacity_sat,
  first_seen_at, last_seen_at, last_graph_update_at, last_indexed_at, source
) values ($1,$2,$3,$4::jsonb,$5::jsonb,0,0,$6,$6,$7,$6,'native')
on conflict (pubkey) do update set
  alias = case when excluded.last_graph_update_at >= coalesce(graph_nodes.last_graph_update_at, '-infinity'::timestamptz) then excluded.alias else graph_nodes.alias end,
  color = case when excluded.last_graph_update_at >= coalesce(graph_nodes.last_graph_update_at, '-infinity'::timestamptz) then excluded.color else graph_nodes.color end,
  addresses_json = case when excluded.last_graph_update_at >= coalesce(graph_nodes.last_graph_update_at, '-infinity'::timestamptz) then excluded.addresses_json else graph_nodes.addresses_json end,
  features_json = case when excluded.last_graph_update_at >= coalesce(graph_nodes.last_graph_update_at, '-infinity'::timestamptz) then excluded.features_json else graph_nodes.features_json end,
  last_seen_at = excluded.last_seen_at,
  last_graph_update_at = case when excluded.last_graph_update_at >= coalesce(graph_nodes.last_graph_update_at, '-infinity'::timestamptz) then excluded.last_graph_update_at else graph_nodes.last_graph_update_at end,
  last_indexed_at = excluded.last_indexed_at,
  source = 'native'
`

	batch := &pgx.Batch{}
	queued := 0
	for _, node := range nodes {
		pubkey := strings.TrimSpace(node.PubKey)
		if pubkey == "" {
			continue
		}
		graphUpdatedAt := node.LastUpdate
		if graphUpdatedAt.IsZero() {
			graphUpdatedAt = observedAt
		}
		batch.Queue(query,
			pubkey,
			strings.TrimSpace(node.Alias),
			strings.TrimSpace(node.Color),
			jsonString(node.Addresses, "[]"),
			jsonString(node.Features, "{}"),
			observedAt,
			graphUpdatedAt,
		)
		queued++
		if queued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, batch, queued); err != nil {
				return err
			}
			batch = &pgx.Batch{}
			queued = 0
		}
	}
	return executeBatch(ctx, tx, batch, queued)
}

func upsertGraphChannelsSnapshot(ctx context.Context, tx pgx.Tx, channels []lndclient.GraphChannel, observedAt time.Time) error {
	if len(channels) == 0 {
		return nil
	}
	const query = `
insert into graph_channels (
  chan_id, chan_point, node1_pubkey, node2_pubkey, capacity_sat, open_block_height,
  status, first_seen_at, last_seen_at, last_indexed_at
) values ($1,$2,$3,$4,$5,$6,'open',$7,$7,$7)
on conflict (chan_id) do update set
  chan_point = excluded.chan_point,
  node1_pubkey = excluded.node1_pubkey,
  node2_pubkey = excluded.node2_pubkey,
  capacity_sat = excluded.capacity_sat,
  open_block_height = excluded.open_block_height,
  last_seen_at = excluded.last_seen_at,
  last_indexed_at = excluded.last_indexed_at,
  status = case when graph_channels.closed_at is not null and graph_channels.closed_at > $8 then graph_channels.status else 'open' end,
  closed_at = case when graph_channels.closed_at is not null and graph_channels.closed_at > $8 then graph_channels.closed_at else null end,
  closed_height = case when graph_channels.closed_at is not null and graph_channels.closed_at > $8 then graph_channels.closed_height else null end,
  close_source = case when graph_channels.closed_at is not null and graph_channels.closed_at > $8 then graph_channels.close_source else null end,
  close_type = case when graph_channels.closed_at is not null and graph_channels.closed_at > $8 then graph_channels.close_type else null end,
  close_txid = case when graph_channels.closed_at is not null and graph_channels.closed_at > $8 then graph_channels.close_txid else null end,
  close_confidence = case when graph_channels.closed_at is not null and graph_channels.closed_at > $8 then graph_channels.close_confidence else null end,
  classified_at = case when graph_channels.closed_at is not null and graph_channels.closed_at > $8 then graph_channels.classified_at else null end
`

	batch := &pgx.Batch{}
	queued := 0
	for _, channel := range channels {
		if channel.ChannelID == 0 {
			continue
		}
		batch.Queue(query,
			int64(channel.ChannelID),
			strings.TrimSpace(channel.ChanPoint),
			strings.TrimSpace(channel.Node1PubKey),
			strings.TrimSpace(channel.Node2PubKey),
			channel.CapacitySat,
			channelBlockHeight(channel.ChannelID),
			observedAt,
			observedAt,
		)
		queued++
		if queued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, batch, queued); err != nil {
				return err
			}
			batch = &pgx.Batch{}
			queued = 0
		}
	}
	if err := executeBatch(ctx, tx, batch, queued); err != nil {
		return err
	}
	return cleanupGraphSnapshotCloseEventsForRecentlyOpenChannels(ctx, tx, observedAt)
}

func cleanupGraphSnapshotCloseEventsForRecentlyOpenChannels(ctx context.Context, tx pgx.Tx, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
delete from graph_close_events e
using graph_channels ch
where ch.status = 'open'
  and ch.last_seen_at = $1
  and ((e.chan_id > 0 and e.chan_id = ch.chan_id) or (coalesce(ch.chan_point, '') <> '' and e.chan_point = ch.chan_point))
  and (
    e.close_source = 'native+snapshot'
    or e.metadata_json ->> 'source' = 'snapshot_missing'
  )
`, observedAt)
	return err
}

func upsertGraphPoliciesSnapshot(ctx context.Context, tx pgx.Tx, channels []lndclient.GraphChannel, observedAt time.Time, currentPolicies map[graphPolicyKey]graphPolicySnapshot) error {
	currentBatch := &pgx.Batch{}
	currentQueued := 0
	historyBatch := &pgx.Batch{}
	historyQueued := 0

	for _, channel := range channels {
		for _, candidate := range graphPoliciesForChannel(channel, observedAt) {
			key := graphPolicyKey{ChanID: channel.ChannelID, AdvertisingPubKey: candidate.AdvertisingPubKey}
			current, ok := currentPolicies[key]
			if ok && !candidate.shouldReplace(current) {
				continue
			}

			queueGraphPolicyCurrentUpsert(currentBatch, channel.ChannelID, candidate)
			currentQueued++
			if currentQueued >= graphExplorerBatchSize {
				if err := executeBatch(ctx, tx, currentBatch, currentQueued); err != nil {
					return err
				}
				currentBatch = &pgx.Batch{}
				currentQueued = 0
			}

			if !ok || !candidate.snapshot.equal(current) {
				queueGraphPolicyHistoryInsert(historyBatch, channel.ChannelID, candidate)
				historyQueued++
				if historyQueued >= graphExplorerBatchSize {
					if err := executeBatch(ctx, tx, historyBatch, historyQueued); err != nil {
						return err
					}
					historyBatch = &pgx.Batch{}
					historyQueued = 0
				}
			}

			currentPolicies[key] = candidate.snapshot
		}
	}

	if err := executeBatch(ctx, tx, currentBatch, currentQueued); err != nil {
		return err
	}
	return executeBatch(ctx, tx, historyBatch, historyQueued)
}

func upsertGraphNodeUpdates(ctx context.Context, tx pgx.Tx, updates []lndclient.GraphNodeUpdate, observedAt time.Time) error {
	if len(updates) == 0 {
		return nil
	}
	const query = `
insert into graph_nodes (
  pubkey, alias, color, addresses_json, features_json, channel_count, total_capacity_sat,
  first_seen_at, last_seen_at, last_graph_update_at, last_indexed_at, source
) values ($1,$2,$3,$4::jsonb,$5::jsonb,0,0,$6,$6,$6,$6,'native')
on conflict (pubkey) do update set
  alias = excluded.alias,
  color = excluded.color,
  addresses_json = excluded.addresses_json,
  features_json = excluded.features_json,
  last_seen_at = excluded.last_seen_at,
  last_graph_update_at = excluded.last_graph_update_at,
  last_indexed_at = excluded.last_indexed_at,
  source = 'native'
`

	batch := &pgx.Batch{}
	queued := 0
	for _, update := range updates {
		pubkey := strings.TrimSpace(update.PubKey)
		if pubkey == "" {
			continue
		}
		batch.Queue(query,
			pubkey,
			strings.TrimSpace(update.Alias),
			strings.TrimSpace(update.Color),
			jsonString(update.Addresses, "[]"),
			jsonString(update.Features, "{}"),
			observedAt,
		)
		queued++
		if queued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, batch, queued); err != nil {
				return err
			}
			batch = &pgx.Batch{}
			queued = 0
		}
	}
	return executeBatch(ctx, tx, batch, queued)
}

func applyGraphChannelUpdates(ctx context.Context, tx pgx.Tx, updates []lndclient.GraphChannelUpdate, observedAt time.Time) error {
	if len(updates) == 0 {
		return nil
	}
	const channelQuery = `
insert into graph_channels (
  chan_id, chan_point, node1_pubkey, node2_pubkey, capacity_sat, open_block_height,
  status, first_seen_at, last_seen_at, last_indexed_at
) values ($1,$2,$3,$4,$5,$6,'open',$7,$7,$7)
on conflict (chan_id) do update set
  chan_point = case when excluded.chan_point <> '' then excluded.chan_point else graph_channels.chan_point end,
  node1_pubkey = case when excluded.node1_pubkey <> '' then excluded.node1_pubkey else graph_channels.node1_pubkey end,
  node2_pubkey = case when excluded.node2_pubkey <> '' then excluded.node2_pubkey else graph_channels.node2_pubkey end,
  capacity_sat = case when excluded.capacity_sat > 0 then excluded.capacity_sat else graph_channels.capacity_sat end,
  open_block_height = case when excluded.open_block_height > 0 then excluded.open_block_height else graph_channels.open_block_height end,
  status = 'open',
  last_seen_at = excluded.last_seen_at,
  last_indexed_at = excluded.last_indexed_at,
  closed_at = null,
  closed_height = null,
  close_source = null,
  close_type = null,
  close_txid = null,
  close_confidence = null,
  classified_at = null
`

	channelBatch := &pgx.Batch{}
	channelQueued := 0
	currentBatch := &pgx.Batch{}
	currentQueued := 0
	historyBatch := &pgx.Batch{}
	historyQueued := 0
	cleanupBatch := &pgx.Batch{}
	cleanupQueued := 0
	const closeCleanupQuery = `
delete from graph_close_events
where ((chan_id > 0 and chan_id = $1) or ($2 <> '' and chan_point = $2))
  and (
    close_source = 'native+snapshot'
    or metadata_json ->> 'source' = 'snapshot_missing'
  )
`

	for _, update := range updates {
		if update.ChannelID == 0 {
			continue
		}
		node1, node2 := canonicalPubKeyPair(update.AdvertisingNode, update.ConnectingNode)
		channelBatch.Queue(channelQuery,
			int64(update.ChannelID),
			strings.TrimSpace(update.ChanPoint),
			node1,
			node2,
			update.CapacitySat,
			channelBlockHeight(update.ChannelID),
			observedAt,
		)
		channelQueued++
		if channelQueued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, channelBatch, channelQueued); err != nil {
				return err
			}
			channelBatch = &pgx.Batch{}
			channelQueued = 0
		}
		cleanupBatch.Queue(closeCleanupQuery, int64(update.ChannelID), strings.TrimSpace(update.ChanPoint))
		cleanupQueued++
		if cleanupQueued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, cleanupBatch, cleanupQueued); err != nil {
				return err
			}
			cleanupBatch = &pgx.Batch{}
			cleanupQueued = 0
		}

		candidate := graphPolicyCandidateFromUpdate(update, observedAt)
		if candidate == nil {
			continue
		}
		current, ok, err := loadCurrentGraphPolicy(ctx, tx, graphPolicyKey{
			ChanID:            update.ChannelID,
			AdvertisingPubKey: candidate.AdvertisingPubKey,
		})
		if err != nil {
			return err
		}
		if ok && !candidate.shouldReplace(current) {
			continue
		}

		queueGraphPolicyCurrentUpsert(currentBatch, update.ChannelID, *candidate)
		currentQueued++
		if currentQueued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, currentBatch, currentQueued); err != nil {
				return err
			}
			currentBatch = &pgx.Batch{}
			currentQueued = 0
		}

		if !ok || !candidate.snapshot.equal(current) {
			queueGraphPolicyHistoryInsert(historyBatch, update.ChannelID, *candidate)
			historyQueued++
			if historyQueued >= graphExplorerBatchSize {
				if err := executeBatch(ctx, tx, historyBatch, historyQueued); err != nil {
					return err
				}
				historyBatch = &pgx.Batch{}
				historyQueued = 0
			}
		}
	}

	if err := executeBatch(ctx, tx, channelBatch, channelQueued); err != nil {
		return err
	}
	if err := executeBatch(ctx, tx, currentBatch, currentQueued); err != nil {
		return err
	}
	if err := executeBatch(ctx, tx, historyBatch, historyQueued); err != nil {
		return err
	}
	return executeBatch(ctx, tx, cleanupBatch, cleanupQueued)
}

func applyGraphClosedChannelUpdates(ctx context.Context, tx pgx.Tx, updates []lndclient.GraphClosedChannelUpdate, observedAt time.Time) error {
	if len(updates) == 0 {
		return nil
	}
	const channelQuery = `
insert into graph_channels (
  chan_id, chan_point, capacity_sat, open_block_height, status, first_seen_at, last_seen_at, closed_at, closed_height, close_source, last_indexed_at
) values ($1,$2,$3,$4,'closed',$5,$5,$5,$6,'native',$5)
on conflict (chan_id) do update set
  chan_point = case when excluded.chan_point <> '' then excluded.chan_point else graph_channels.chan_point end,
  capacity_sat = case when excluded.capacity_sat > 0 then excluded.capacity_sat else graph_channels.capacity_sat end,
  status = 'closed',
  closed_at = coalesce(graph_channels.closed_at, excluded.closed_at),
  closed_height = case when excluded.closed_height > 0 then excluded.closed_height else graph_channels.closed_height end,
  close_source = 'native',
  last_indexed_at = excluded.last_indexed_at
`
	const eventUpdateQuery = `
update graph_close_events
set node1_pubkey = case when coalesce((select node1_pubkey from graph_channels where chan_id = $1), '') <> '' then (select node1_pubkey from graph_channels where chan_id = $1) else node1_pubkey end,
    node2_pubkey = case when coalesce((select node2_pubkey from graph_channels where chan_id = $1), '') <> '' then (select node2_pubkey from graph_channels where chan_id = $1) else node2_pubkey end,
    capacity_sat = case when $3 > 0 then $3 else capacity_sat end,
    closed_height = case when $4 > 0 then $4 else closed_height end,
    observed_at = coalesce(observed_at, $5),
    close_source = 'native',
    metadata_json = coalesce(metadata_json, '{}'::jsonb)
where (chan_id > 0 and chan_id = $1)
   or ($2 <> '' and chan_point = $2)
`
	const eventInsertQuery = `
insert into graph_close_events (
  chan_id, chan_point, node1_pubkey, node2_pubkey, capacity_sat, closed_height, observed_at, close_source, metadata_json
) select
  $1,
  $2,
  (select node1_pubkey from graph_channels where chan_id = $1),
  (select node2_pubkey from graph_channels where chan_id = $1),
  $3,
  $4,
  $5,
  'native',
  '{}'::jsonb
where not exists (
  select 1
  from graph_close_events
  where (chan_id > 0 and chan_id = $1)
     or ($2 <> '' and chan_point = $2)
)
`

	channelBatch := &pgx.Batch{}
	channelQueued := 0
	eventBatch := &pgx.Batch{}
	eventQueued := 0

	for _, update := range updates {
		if update.ChannelID == 0 {
			continue
		}
		channelBatch.Queue(channelQuery,
			int64(update.ChannelID),
			strings.TrimSpace(update.ChanPoint),
			update.CapacitySat,
			channelBlockHeight(update.ChannelID),
			observedAt,
			int(update.ClosedHeight),
		)
		channelQueued++
		if channelQueued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, channelBatch, channelQueued); err != nil {
				return err
			}
			channelBatch = &pgx.Batch{}
			channelQueued = 0
		}

		eventBatch.Queue(eventUpdateQuery,
			int64(update.ChannelID),
			strings.TrimSpace(update.ChanPoint),
			update.CapacitySat,
			int(update.ClosedHeight),
			observedAt,
		)
		eventQueued++
		if eventQueued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, eventBatch, eventQueued); err != nil {
				return err
			}
			eventBatch = &pgx.Batch{}
			eventQueued = 0
		}

		eventBatch.Queue(eventInsertQuery,
			int64(update.ChannelID),
			strings.TrimSpace(update.ChanPoint),
			update.CapacitySat,
			int(update.ClosedHeight),
			observedAt,
		)
		eventQueued++
		if eventQueued >= graphExplorerBatchSize {
			if err := executeBatch(ctx, tx, eventBatch, eventQueued); err != nil {
				return err
			}
			eventBatch = &pgx.Batch{}
			eventQueued = 0
		}
	}

	if err := executeBatch(ctx, tx, channelBatch, channelQueued); err != nil {
		return err
	}
	return executeBatch(ctx, tx, eventBatch, eventQueued)
}

func recomputeGraphNodeAggregates(ctx context.Context, tx pgx.Tx, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
with stats as (
  select pubkey, count(*)::integer as channel_count, coalesce(sum(capacity_sat), 0)::bigint as total_capacity_sat
  from (
    select node1_pubkey as pubkey, capacity_sat
    from graph_channels
    where status = 'open' and node1_pubkey is not null and node1_pubkey <> ''
    union all
    select node2_pubkey as pubkey, capacity_sat
    from graph_channels
    where status = 'open' and node2_pubkey is not null and node2_pubkey <> ''
  ) flattened
  group by pubkey
)
update graph_nodes n
set channel_count = coalesce(stats.channel_count, 0),
  total_capacity_sat = coalesce(stats.total_capacity_sat, 0),
  last_indexed_at = $1
from stats
where n.pubkey = stats.pubkey
`, observedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
update graph_nodes n
set channel_count = 0,
  total_capacity_sat = 0,
  last_indexed_at = $1
where not exists (
  select 1
  from graph_channels ch
  where ch.status = 'open'
    and (ch.node1_pubkey = n.pubkey or ch.node2_pubkey = n.pubkey)
)
`, observedAt)
	return err
}

func recomputeGraphNodeAggregatesForPubKeys(ctx context.Context, tx pgx.Tx, observedAt time.Time, pubkeys []string) error {
	pubkeys = graphExplorerUniquePubKeys(pubkeys)
	if len(pubkeys) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
with affected as (
  select distinct pubkey
  from unnest($2::text[]) as items(pubkey)
  where pubkey <> ''
),
stats as (
  select pubkey, count(*)::integer as channel_count, coalesce(sum(capacity_sat), 0)::bigint as total_capacity_sat
  from (
    select ch.node1_pubkey as pubkey, ch.capacity_sat
    from graph_channels ch
    join affected a on a.pubkey = ch.node1_pubkey
    where ch.status = 'open' and ch.node1_pubkey is not null and ch.node1_pubkey <> ''
    union all
    select ch.node2_pubkey as pubkey, ch.capacity_sat
    from graph_channels ch
    join affected a on a.pubkey = ch.node2_pubkey
    where ch.status = 'open' and ch.node2_pubkey is not null and ch.node2_pubkey <> ''
  ) flattened
  group by pubkey
)
update graph_nodes n
set channel_count = coalesce(stats.channel_count, 0),
  total_capacity_sat = coalesce(stats.total_capacity_sat, 0),
  last_indexed_at = $1
from affected
left join stats on stats.pubkey = affected.pubkey
where n.pubkey = affected.pubkey
`, observedAt, pubkeys)
	return err
}

func upsertGraphSyncStateRefresh(ctx context.Context, tx pgx.Tx, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
insert into graph_sync_state (id, first_native_coverage_at, last_bootstrap_at, last_reconcile_at, updated_at)
values (true, $1, $1, $1, now())
on conflict (id) do update set
  first_native_coverage_at = coalesce(graph_sync_state.first_native_coverage_at, excluded.first_native_coverage_at),
  last_bootstrap_at = excluded.last_bootstrap_at,
  last_reconcile_at = excluded.last_reconcile_at,
  updated_at = now()
`, observedAt)
	return err
}

func upsertGraphSyncStateStream(ctx context.Context, tx pgx.Tx, observedAt time.Time) error {
	_, err := tx.Exec(ctx, `
insert into graph_sync_state (id, first_native_coverage_at, last_stream_event_at, updated_at)
values (true, $1, $1, now())
on conflict (id) do update set
  first_native_coverage_at = coalesce(graph_sync_state.first_native_coverage_at, excluded.first_native_coverage_at),
  last_stream_event_at = excluded.last_stream_event_at,
  updated_at = now()
`, observedAt)
	return err
}

func loadCurrentGraphPolicies(ctx context.Context, tx pgx.Tx) (map[graphPolicyKey]graphPolicySnapshot, error) {
	rows, err := tx.Query(ctx, `
select chan_id,
  advertising_pubkey,
  connecting_pubkey,
  fee_base_msat,
  fee_rate_ppm,
  time_lock_delta,
  min_htlc_msat,
  max_htlc_msat,
  disabled,
  inbound_base_msat,
  inbound_fee_rate_ppm,
  policy_last_update_at
from graph_channel_policy_current
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[graphPolicyKey]graphPolicySnapshot)
	for rows.Next() {
		var chanID int64
		var key graphPolicyKey
		var snapshot graphPolicySnapshot
		if err := rows.Scan(
			&chanID,
			&key.AdvertisingPubKey,
			&snapshot.ConnectingPubKey,
			&snapshot.FeeBaseMsat,
			&snapshot.FeeRatePpm,
			&snapshot.TimeLockDelta,
			&snapshot.MinHtlcMsat,
			&snapshot.MaxHtlcMsat,
			&snapshot.Disabled,
			&snapshot.InboundBaseMsat,
			&snapshot.InboundFeeRatePpm,
			&snapshot.PolicyLastUpdateAt,
		); err != nil {
			return nil, err
		}
		key.ChanID = uint64(chanID)
		key.AdvertisingPubKey = strings.TrimSpace(key.AdvertisingPubKey)
		result[key] = snapshot
	}
	return result, rows.Err()
}

func loadGraphChannelPubKeysByChanIDs(ctx context.Context, tx pgx.Tx, chanIDs []uint64) ([]string, error) {
	chanIDs = graphExplorerUniqueChanIDs(chanIDs)
	if len(chanIDs) == 0 {
		return nil, nil
	}
	values := make([]int64, 0, len(chanIDs))
	for _, chanID := range chanIDs {
		if chanID == 0 {
			continue
		}
		values = append(values, int64(chanID))
	}
	if len(values) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
select node1_pubkey, node2_pubkey
from graph_channels
where chan_id = any($1)
`, values)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pubkeys := make([]string, 0, len(values)*2)
	for rows.Next() {
		var node1 string
		var node2 string
		if err := rows.Scan(&node1, &node2); err != nil {
			return nil, err
		}
		pubkeys = append(pubkeys, node1, node2)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return graphExplorerUniquePubKeys(pubkeys), nil
}

func loadCurrentGraphPolicy(ctx context.Context, tx pgx.Tx, key graphPolicyKey) (graphPolicySnapshot, bool, error) {
	var snapshot graphPolicySnapshot
	var chanID int64
	err := tx.QueryRow(ctx, `
select chan_id,
  connecting_pubkey,
  fee_base_msat,
  fee_rate_ppm,
  time_lock_delta,
  min_htlc_msat,
  max_htlc_msat,
  disabled,
  inbound_base_msat,
  inbound_fee_rate_ppm,
  policy_last_update_at
from graph_channel_policy_current
where chan_id = $1 and advertising_pubkey = $2
`, int64(key.ChanID), strings.TrimSpace(key.AdvertisingPubKey)).Scan(
		&chanID,
		&snapshot.ConnectingPubKey,
		&snapshot.FeeBaseMsat,
		&snapshot.FeeRatePpm,
		&snapshot.TimeLockDelta,
		&snapshot.MinHtlcMsat,
		&snapshot.MaxHtlcMsat,
		&snapshot.Disabled,
		&snapshot.InboundBaseMsat,
		&snapshot.InboundFeeRatePpm,
		&snapshot.PolicyLastUpdateAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return graphPolicySnapshot{}, false, nil
		}
		return graphPolicySnapshot{}, false, err
	}
	return snapshot, true, nil
}

type graphPolicyCandidate struct {
	AdvertisingPubKey string
	snapshot          graphPolicySnapshot
}

func graphPoliciesForChannel(channel lndclient.GraphChannel, observedAt time.Time) []graphPolicyCandidate {
	items := make([]graphPolicyCandidate, 0, 2)
	if candidate := graphPolicyCandidateFromSnapshot(channel.Node1PubKey, channel.Node2PubKey, channel.Node1Policy, observedAt); candidate != nil {
		items = append(items, *candidate)
	}
	if candidate := graphPolicyCandidateFromSnapshot(channel.Node2PubKey, channel.Node1PubKey, channel.Node2Policy, observedAt); candidate != nil {
		items = append(items, *candidate)
	}
	return items
}

func graphPolicyCandidateFromSnapshot(advertisingPubKey, connectingPubKey string, policy *lndclient.GraphRoutingPolicy, observedAt time.Time) *graphPolicyCandidate {
	if policy == nil {
		return nil
	}
	advertisingPubKey = strings.TrimSpace(advertisingPubKey)
	connectingPubKey = strings.TrimSpace(connectingPubKey)
	if advertisingPubKey == "" || connectingPubKey == "" {
		return nil
	}
	policyTime := policy.LastUpdate
	if policyTime.IsZero() {
		policyTime = observedAt
	}
	return &graphPolicyCandidate{
		AdvertisingPubKey: advertisingPubKey,
		snapshot: graphPolicySnapshot{
			ConnectingPubKey:   connectingPubKey,
			FeeBaseMsat:        policy.FeeBaseMsat,
			FeeRatePpm:         policy.FeeRatePpm,
			TimeLockDelta:      policy.TimeLockDelta,
			MinHtlcMsat:        policy.MinHtlcMsat,
			MaxHtlcMsat:        policy.MaxHtlcMsat,
			Disabled:           policy.Disabled,
			InboundBaseMsat:    policy.InboundFeeBaseMsat,
			InboundFeeRatePpm:  policy.InboundFeeRatePpm,
			PolicyLastUpdateAt: policyTime,
		},
	}
}

func graphPolicyCandidateFromUpdate(update lndclient.GraphChannelUpdate, observedAt time.Time) *graphPolicyCandidate {
	if update.RoutingPolicy == nil {
		return nil
	}
	return graphPolicyCandidateFromSnapshot(update.AdvertisingNode, update.ConnectingNode, update.RoutingPolicy, observedAt)
}

func (c graphPolicyCandidate) shouldReplace(current graphPolicySnapshot) bool {
	if current.PolicyLastUpdateAt.IsZero() {
		return true
	}
	return !c.snapshot.PolicyLastUpdateAt.Before(current.PolicyLastUpdateAt)
}

func (s graphPolicySnapshot) equal(other graphPolicySnapshot) bool {
	return strings.TrimSpace(s.ConnectingPubKey) == strings.TrimSpace(other.ConnectingPubKey) &&
		s.FeeBaseMsat == other.FeeBaseMsat &&
		s.FeeRatePpm == other.FeeRatePpm &&
		s.TimeLockDelta == other.TimeLockDelta &&
		s.MinHtlcMsat == other.MinHtlcMsat &&
		s.MaxHtlcMsat == other.MaxHtlcMsat &&
		s.Disabled == other.Disabled &&
		s.InboundBaseMsat == other.InboundBaseMsat &&
		s.InboundFeeRatePpm == other.InboundFeeRatePpm &&
		s.PolicyLastUpdateAt.Equal(other.PolicyLastUpdateAt)
}

func queueGraphPolicyCurrentUpsert(batch *pgx.Batch, chanID uint64, candidate graphPolicyCandidate) {
	const query = `
insert into graph_channel_policy_current (
  chan_id, advertising_pubkey, connecting_pubkey, fee_base_msat, fee_rate_ppm,
  time_lock_delta, min_htlc_msat, max_htlc_msat, disabled,
  inbound_base_msat, inbound_fee_rate_ppm, policy_last_update_at, last_indexed_at
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)
on conflict (chan_id, advertising_pubkey) do update set
  connecting_pubkey = excluded.connecting_pubkey,
  fee_base_msat = excluded.fee_base_msat,
  fee_rate_ppm = excluded.fee_rate_ppm,
  time_lock_delta = excluded.time_lock_delta,
  min_htlc_msat = excluded.min_htlc_msat,
  max_htlc_msat = excluded.max_htlc_msat,
  disabled = excluded.disabled,
  inbound_base_msat = excluded.inbound_base_msat,
  inbound_fee_rate_ppm = excluded.inbound_fee_rate_ppm,
  policy_last_update_at = excluded.policy_last_update_at,
  last_indexed_at = excluded.last_indexed_at
`
	batch.Queue(query,
		int64(chanID),
		candidate.AdvertisingPubKey,
		candidate.snapshot.ConnectingPubKey,
		candidate.snapshot.FeeBaseMsat,
		candidate.snapshot.FeeRatePpm,
		int(candidate.snapshot.TimeLockDelta),
		candidate.snapshot.MinHtlcMsat,
		int64(candidate.snapshot.MaxHtlcMsat),
		candidate.snapshot.Disabled,
		candidate.snapshot.InboundBaseMsat,
		candidate.snapshot.InboundFeeRatePpm,
		candidate.snapshot.PolicyLastUpdateAt,
	)
}

func queueGraphPolicyHistoryInsert(batch *pgx.Batch, chanID uint64, candidate graphPolicyCandidate) {
	const query = `
insert into graph_channel_policy_history (
  chan_id, advertising_pubkey, connecting_pubkey, fee_base_msat, fee_rate_ppm,
  time_lock_delta, min_htlc_msat, max_htlc_msat, disabled,
  inbound_base_msat, inbound_fee_rate_ppm, captured_at, source
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'native')
`
	batch.Queue(query,
		int64(chanID),
		candidate.AdvertisingPubKey,
		candidate.snapshot.ConnectingPubKey,
		candidate.snapshot.FeeBaseMsat,
		candidate.snapshot.FeeRatePpm,
		int(candidate.snapshot.TimeLockDelta),
		candidate.snapshot.MinHtlcMsat,
		int64(candidate.snapshot.MaxHtlcMsat),
		candidate.snapshot.Disabled,
		candidate.snapshot.InboundBaseMsat,
		candidate.snapshot.InboundFeeRatePpm,
		candidate.snapshot.PolicyLastUpdateAt,
	)
}

func executeBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch, queued int) error {
	if queued <= 0 {
		return nil
	}
	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < queued; i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

func channelBlockHeight(channelID uint64) int {
	if channelID == 0 {
		return 0
	}
	return int(channelID >> 40)
}

func graphExplorerCollectAffectedPubKeysFromChannelUpdates(updates []lndclient.GraphChannelUpdate) []string {
	pubkeys := make([]string, 0, len(updates)*2)
	for _, update := range updates {
		node1, node2 := canonicalPubKeyPair(
			graphExplorerNormalizePubkey(update.AdvertisingNode),
			graphExplorerNormalizePubkey(update.ConnectingNode),
		)
		pubkeys = append(pubkeys, node1, node2)
	}
	return graphExplorerUniquePubKeys(pubkeys)
}

func graphExplorerCollectClosedChannelIDs(updates []lndclient.GraphClosedChannelUpdate) []uint64 {
	if len(updates) == 0 {
		return nil
	}
	chanIDs := make([]uint64, 0, len(updates))
	for _, update := range updates {
		if update.ChannelID == 0 {
			continue
		}
		chanIDs = append(chanIDs, update.ChannelID)
	}
	return graphExplorerUniqueChanIDs(chanIDs)
}

func graphExplorerUniquePubKeys(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		pubkey := graphExplorerNormalizePubkey(value)
		if pubkey == "" {
			continue
		}
		if _, ok := seen[pubkey]; ok {
			continue
		}
		seen[pubkey] = struct{}{}
		result = append(result, pubkey)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func graphExplorerUniqueChanIDs(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[uint64]struct{}, len(values))
	result := make([]uint64, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func canonicalPubKeyPair(left, right string) (string, string) {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right, left
	}
	if right == "" {
		return left, right
	}
	if strings.Compare(left, right) <= 0 {
		return left, right
	}
	return right, left
}

func jsonString(value any, fallback string) string {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 {
		return fallback
	}
	return string(raw)
}

func graphExplorerAmbossTokenAvailable(ctx context.Context, db *pgxpool.Pool) bool {
	if db == nil {
		return false
	}
	var raw string
	err := db.QueryRow(ctx, `
select coalesce(amboss_token, '')
from autofee_config
where id = $1
`, autofeeConfigID).Scan(&raw)
	if err != nil {
		return false
	}
	return strings.TrimSpace(raw) != ""
}

func sleepWithStop(stopCh <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stopCh:
		return false
	case <-timer.C:
		return true
	}
}

func (s *GraphExplorerService) setLastSyncAt(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSyncAt = at
}

func (s *GraphExplorerService) recordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = strings.TrimSpace(err.Error())
}

func (s *GraphExplorerService) clearError() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = ""
}
