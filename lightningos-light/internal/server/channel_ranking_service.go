package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	channelRankingPollInterval               = 3 * time.Minute
	channelRankingAssistedRevenueWeight      = 0.5
	channelRankingClosePersistence           = 30 * 24 * time.Hour
	channelRankingHTLCPolicyHigh30d          = 25
	channelRankingHTLCLiquidityHigh30d       = 150
	channelRankingHTLCLinkHigh30d            = 200
	channelRankingRebalanceNoPaybackPenalty  = 30
	channelRankingRebalanceNoPaybackScoreCap = 23
)

var ErrChannelRankingDBUnavailable = errors.New("channel ranking db unavailable")

type ChannelRankingReason struct {
	Code string `json:"code"`
}

type ChannelRankingRecommendation struct {
	Code         string `json:"code"`
	TargetModule string `json:"target_module,omitempty"`
}

type ChannelRankingHistoryPoint struct {
	ComputedAt      time.Time `json:"computed_at"`
	Score           int       `json:"score"`
	Score7d         int       `json:"score_7d"`
	Score30d        int       `json:"score_30d"`
	TrendDirection  string    `json:"trend_direction,omitempty"`
	TrendDelta      int       `json:"trend_delta,omitempty"`
	State           string    `json:"state"`
	CloseCandidate  bool      `json:"close_candidate,omitempty"`
	ProfitFee7dSat  int64     `json:"profit_fee_7d_sat"`
	ProfitFee30dSat int64     `json:"profit_fee_30d_sat"`
}

type ChannelRankingFeedback struct {
	Direction   string    `json:"direction,omitempty"`
	ScoreDelta  int       `json:"score_delta,omitempty"`
	NetDeltaSat int64     `json:"net_delta_sat,omitempty"`
	BaselineAt  time.Time `json:"baseline_at,omitempty"`
	WindowHours int       `json:"window_hours,omitempty"`
}

type ChannelRankingFlowCounterparty struct {
	ChannelPoint       string `json:"channel_point,omitempty"`
	ChannelID          int64  `json:"channel_id"`
	PeerAlias          string `json:"peer_alias,omitempty"`
	ForwardCount7d     int    `json:"forward_count_7d"`
	ForwardAmountSat7d int64  `json:"forward_amount_sat_7d"`
}

type ChannelRankingPeerComparison struct {
	ChannelPoint    string `json:"channel_point"`
	ChannelID       int64  `json:"channel_id"`
	PeerAlias       string `json:"peer_alias,omitempty"`
	Score           int    `json:"score"`
	Score30d        int    `json:"score_30d"`
	TrendDirection  string `json:"trend_direction,omitempty"`
	TrendDelta      int    `json:"trend_delta,omitempty"`
	State           string `json:"state"`
	CapacitySat     int64  `json:"capacity_sat"`
	ProfitFee7dSat  int64  `json:"profit_fee_7d_sat"`
	ProfitFee30dSat int64  `json:"profit_fee_30d_sat"`
}

type ChannelRankingDetail struct {
	Item                ChannelRankingItem               `json:"item"`
	History             []ChannelRankingHistoryPoint     `json:"history,omitempty"`
	PeerChannels        []ChannelRankingPeerComparison   `json:"peer_channels,omitempty"`
	TopForwardInSources []ChannelRankingFlowCounterparty `json:"top_forward_in_sources,omitempty"`
	TopForwardOutSinks  []ChannelRankingFlowCounterparty `json:"top_forward_out_sinks,omitempty"`
	Feedback            *ChannelRankingFeedback          `json:"feedback,omitempty"`
}

type ChannelRankingItem struct {
	ChannelPoint             string                         `json:"channel_point"`
	ChannelID                int64                          `json:"channel_id"`
	PeerPubkey               string                         `json:"peer_pubkey,omitempty"`
	PeerAlias                string                         `json:"peer_alias,omitempty"`
	Active                   bool                           `json:"active"`
	Private                  bool                           `json:"private"`
	CapacitySat              int64                          `json:"capacity_sat"`
	LocalBalanceSat          int64                          `json:"local_balance_sat"`
	RemoteBalanceSat         int64                          `json:"remote_balance_sat"`
	LocalBalancePct          float64                        `json:"local_balance_pct"`
	RemoteBalancePct         float64                        `json:"remote_balance_pct"`
	InactiveDurationSec      int64                          `json:"inactive_duration_sec,omitempty"`
	PendingHtlcCount         int                            `json:"pending_htlc_count,omitempty"`
	ClassLabel               string                         `json:"class_label,omitempty"`
	ForwardInCount7d         int                            `json:"forward_in_count_7d"`
	ForwardInAmountSat7d     int64                          `json:"forward_in_amount_sat_7d"`
	ForwardOutCount7d        int                            `json:"forward_out_count_7d"`
	ForwardOutAmountSat7d    int64                          `json:"forward_out_amount_sat_7d"`
	ForwardFee7dSat          int64                          `json:"forward_fee_7d_sat"`
	ForwardAmt7dSat          int64                          `json:"forward_amt_7d_sat"`
	OutPpm7d                 int                            `json:"out_ppm_7d"`
	AssistedForwardFee7dSat  int64                          `json:"assisted_forward_fee_7d_sat"`
	AssistedForwardAmt7dSat  int64                          `json:"assisted_forward_amt_7d_sat"`
	ForwardFee30dSat         int64                          `json:"forward_fee_30d_sat"`
	ForwardAmt30dSat         int64                          `json:"forward_amt_30d_sat"`
	OutPpm30d                int                            `json:"out_ppm_30d"`
	AssistedForwardFee30dSat int64                          `json:"assisted_forward_fee_30d_sat"`
	AssistedForwardAmt30dSat int64                          `json:"assisted_forward_amt_30d_sat"`
	RebalFee7dSat            int64                          `json:"rebal_fee_7d_sat"`
	RebalAmt7dSat            int64                          `json:"rebal_amt_7d_sat"`
	RebalPpm7d               int                            `json:"rebal_ppm_7d"`
	RebalFee30dSat           int64                          `json:"rebal_fee_30d_sat"`
	RebalAmt30dSat           int64                          `json:"rebal_amt_30d_sat"`
	RebalPpm30d              int                            `json:"rebal_ppm_30d"`
	ProfitFee7dSat           int64                          `json:"profit_fee_7d_sat"`
	ProfitFee30dSat          int64                          `json:"profit_fee_30d_sat"`
	PeerStabilityScore30d    int                            `json:"peer_stability_score_30d"`
	PeerSampleCount30d       int                            `json:"peer_sample_count_30d,omitempty"`
	HTLCFailures30d          int                            `json:"htlc_failures_30d,omitempty"`
	HTLCPolicyFails30d       int                            `json:"htlc_policy_fails_30d,omitempty"`
	HTLCLiquidityFails30d    int                            `json:"htlc_liquidity_fails_30d,omitempty"`
	HTLCForwardFails30d      int                            `json:"htlc_forward_fails_30d,omitempty"`
	RebalanceDependenceScore int                            `json:"rebalance_dependence_score"`
	Score                    int                            `json:"score"`
	Score7d                  int                            `json:"score_7d"`
	Score30d                 int                            `json:"score_30d"`
	TrendDirection           string                         `json:"trend_direction,omitempty"`
	TrendDelta               int                            `json:"trend_delta,omitempty"`
	State                    string                         `json:"state"`
	Reasons                  []ChannelRankingReason         `json:"reasons,omitempty"`
	Recommendations          []ChannelRankingRecommendation `json:"recommendations,omitempty"`
	CloseCandidate           bool                           `json:"close_candidate,omitempty"`
	ComputedAt               time.Time                      `json:"computed_at"`
}

type ChannelRankingStatus struct {
	Available   bool           `json:"available"`
	LastSyncAt  *time.Time     `json:"last_sync_at,omitempty"`
	StateCounts map[string]int `json:"state_counts,omitempty"`
}

type channelTrafficStat struct {
	FeeSat    int64
	AmountSat int64
	Ppm       int
}

type channelPeerSample struct {
	Connected bool
	PingTime  int64
	HasError  bool
}

type channelPeerAggregate struct {
	Score30d    int
	SampleCount int
}

type channelHTLCAggregate struct {
	Total     int
	Policy    int
	Liquidity int
	Forward   int
}

type ChannelRankingService struct {
	db                 *pgxpool.Pool
	logger             *log.Logger
	lnd                *lndclient.Client
	htlcFailedProvider htlcFailedProvider
	mu                 sync.Mutex
	lastSyncAt         time.Time
	stopCh             chan struct{}
	doneCh             chan struct{}
}

func NewChannelRankingService(db *pgxpool.Pool, logger *log.Logger, lnd *lndclient.Client, htlcFailedProvider htlcFailedProvider) *ChannelRankingService {
	return &ChannelRankingService{
		db:                 db,
		logger:             logger,
		lnd:                lnd,
		htlcFailedProvider: htlcFailedProvider,
	}
}

func (s *ChannelRankingService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrChannelRankingDBUnavailable
	}
	_, err := s.db.Exec(ctx, `
create table if not exists channel_rankings (
  channel_point text primary key,
  channel_id bigint not null default 0,
  peer_pubkey text,
  peer_alias text,
  active boolean not null default false,
  private boolean not null default false,
  capacity_sat bigint not null default 0,
  local_balance_sat bigint not null default 0,
  remote_balance_sat bigint not null default 0,
  local_balance_pct double precision not null default 0,
  remote_balance_pct double precision not null default 0,
  inactive_duration_sec bigint not null default 0,
  pending_htlc_count integer not null default 0,
  class_label text,
  forward_in_count_7d integer not null default 0,
  forward_in_amount_sat_7d bigint not null default 0,
  forward_out_count_7d integer not null default 0,
  forward_out_amount_sat_7d bigint not null default 0,
  forward_fee_7d_sat bigint not null default 0,
  forward_amt_7d_sat bigint not null default 0,
  assisted_forward_fee_7d_sat bigint not null default 0,
  assisted_forward_amt_7d_sat bigint not null default 0,
  out_ppm_7d integer not null default 0,
  forward_fee_30d_sat bigint not null default 0,
  forward_amt_30d_sat bigint not null default 0,
  assisted_forward_fee_30d_sat bigint not null default 0,
  assisted_forward_amt_30d_sat bigint not null default 0,
  out_ppm_30d integer not null default 0,
  rebal_fee_7d_sat bigint not null default 0,
  rebal_amt_7d_sat bigint not null default 0,
  rebal_ppm_7d integer not null default 0,
  rebal_fee_30d_sat bigint not null default 0,
  rebal_amt_30d_sat bigint not null default 0,
  rebal_ppm_30d integer not null default 0,
  profit_fee_7d_sat bigint not null default 0,
  profit_fee_30d_sat bigint not null default 0,
  peer_stability_score_30d integer not null default 0,
  peer_sample_count_30d integer not null default 0,
  htlc_failures_30d integer not null default 0,
  htlc_policy_fails_30d integer not null default 0,
  htlc_liquidity_fails_30d integer not null default 0,
  htlc_forward_fails_30d integer not null default 0,
  rebalance_dependence_score integer not null default 0,
  score integer not null default 0,
  score_7d integer not null default 0,
  score_30d integer not null default 0,
  trend_direction text not null default 'stable',
  trend_delta integer not null default 0,
  state text not null default 'monitor',
  reasons_json jsonb not null default '[]'::jsonb,
  recommendations_json jsonb not null default '[]'::jsonb,
  computed_at timestamptz not null default now()
);
create index if not exists channel_rankings_state_score_idx on channel_rankings (state, score desc, profit_fee_7d_sat desc);
create index if not exists channel_rankings_score_idx on channel_rankings (score desc, profit_fee_7d_sat desc);
create table if not exists channel_ranking_history (
  id bigserial primary key,
  channel_point text not null,
  computed_bucket timestamptz not null,
  score integer not null default 0,
  score_7d integer not null default 0,
  score_30d integer not null default 0,
  trend_direction text not null default 'stable',
  trend_delta integer not null default 0,
  state text not null default 'monitor',
  close_candidate boolean not null default false,
  profit_fee_7d_sat bigint not null default 0,
  profit_fee_30d_sat bigint not null default 0,
  created_at timestamptz not null default now(),
  unique(channel_point, computed_bucket)
);
create index if not exists channel_ranking_history_point_idx on channel_ranking_history (channel_point, computed_bucket desc);
create table if not exists channel_ranking_peer_samples (
  peer_pubkey text not null,
  sampled_bucket timestamptz not null,
  connected boolean not null default false,
  ping_time bigint not null default 0,
  has_error boolean not null default false,
  created_at timestamptz not null default now(),
  primary key (peer_pubkey, sampled_bucket)
);
create index if not exists channel_ranking_peer_samples_time_idx on channel_ranking_peer_samples (sampled_bucket desc);
create table if not exists channel_ranking_htlc_failures (
  channel_id bigint not null,
  sampled_bucket timestamptz not null,
  total_count integer not null default 0,
  policy_count integer not null default 0,
  liquidity_count integer not null default 0,
  forward_count integer not null default 0,
  created_at timestamptz not null default now(),
  primary key (channel_id, sampled_bucket)
);
create index if not exists channel_ranking_htlc_failures_time_idx on channel_ranking_htlc_failures (sampled_bucket desc);
`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
alter table channel_rankings add column if not exists forward_fee_30d_sat bigint not null default 0;
alter table channel_rankings add column if not exists forward_amt_30d_sat bigint not null default 0;
alter table channel_rankings add column if not exists assisted_forward_fee_7d_sat bigint not null default 0;
alter table channel_rankings add column if not exists assisted_forward_amt_7d_sat bigint not null default 0;
alter table channel_rankings add column if not exists assisted_forward_fee_30d_sat bigint not null default 0;
alter table channel_rankings add column if not exists assisted_forward_amt_30d_sat bigint not null default 0;
alter table channel_rankings add column if not exists out_ppm_30d integer not null default 0;
alter table channel_rankings add column if not exists rebal_fee_30d_sat bigint not null default 0;
alter table channel_rankings add column if not exists rebal_amt_30d_sat bigint not null default 0;
alter table channel_rankings add column if not exists rebal_ppm_30d integer not null default 0;
alter table channel_rankings add column if not exists profit_fee_30d_sat bigint not null default 0;
alter table channel_rankings add column if not exists peer_stability_score_30d integer not null default 0;
alter table channel_rankings add column if not exists peer_sample_count_30d integer not null default 0;
alter table channel_rankings add column if not exists htlc_failures_30d integer not null default 0;
alter table channel_rankings add column if not exists htlc_policy_fails_30d integer not null default 0;
alter table channel_rankings add column if not exists htlc_liquidity_fails_30d integer not null default 0;
alter table channel_rankings add column if not exists htlc_forward_fails_30d integer not null default 0;
alter table channel_rankings add column if not exists rebalance_dependence_score integer not null default 0;
alter table channel_rankings add column if not exists score_7d integer not null default 0;
alter table channel_rankings add column if not exists score_30d integer not null default 0;
alter table channel_rankings add column if not exists trend_direction text not null default 'stable';
alter table channel_rankings add column if not exists trend_delta integer not null default 0;
alter table channel_ranking_history add column if not exists close_candidate boolean not null default false;
alter table channel_rankings add column if not exists forward_in_count_7d integer not null default 0;
alter table channel_rankings add column if not exists forward_in_amount_sat_7d bigint not null default 0;
alter table channel_rankings add column if not exists forward_out_count_7d integer not null default 0;
alter table channel_rankings add column if not exists forward_out_amount_sat_7d bigint not null default 0;
`)
	return err
}

func (s *ChannelRankingService) Start() {
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

func (s *ChannelRankingService) runLoop(stopCh <-chan struct{}) {
	s.refreshBackground()
	ticker := time.NewTicker(channelRankingPollInterval)
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

func (s *ChannelRankingService) refreshBackground() {
	if s == nil || s.db == nil || s.lnd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.Refresh(ctx); err != nil && s.logger != nil {
		s.logger.Printf("channel ranking refresh failed: %v", err)
	}
}

func (s *ChannelRankingService) lastSyncAtPtr() *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSyncAt.IsZero() {
		return nil
	}
	value := s.lastSyncAt
	return &value
}

func (s *ChannelRankingService) Refresh(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrChannelRankingDBUnavailable
	}
	if s.lnd == nil {
		return errors.New("lnd unavailable")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}

	labels, err := s.fetchAutofeeLabels(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking labels lookup failed: %v", err)
	}
	forwardStats, err := s.fetchForwardStats7d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking forward stats lookup failed: %v", err)
	}
	forwardStats30d, err := s.fetchForwardStats30d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking 30d forward stats lookup failed: %v", err)
	}
	assistedForwardStats, err := s.fetchAssistedForwardStats7d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking assisted forward stats lookup failed: %v", err)
	}
	assistedForwardStats30d, err := s.fetchAssistedForwardStats30d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking 30d assisted forward stats lookup failed: %v", err)
	}
	rebalStats, err := s.fetchRebalanceStats7d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking rebalance stats lookup failed: %v", err)
	}
	rebalStats30d, err := s.fetchRebalanceStats30d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking 30d rebalance stats lookup failed: %v", err)
	}
	movementStats7d, err := s.fetchMovementStats7d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking movement 7d lookup failed: %v", err)
	}
	peers, err := s.lnd.ListPeers(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking peer stability lookup failed: %v", err)
	}

	now := time.Now().UTC()
	if err := s.capturePeerSamples(ctx, now, channels, peers); err != nil && s.logger != nil {
		s.logger.Printf("channel ranking peer samples capture failed: %v", err)
	}
	if err := s.captureHTLCFailureSamples(ctx, now); err != nil && s.logger != nil {
		s.logger.Printf("channel ranking htlc failure capture failed: %v", err)
	}
	peerAggregates, err := s.loadPeerStability30d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking peer stability aggregate failed: %v", err)
	}
	htlcAggregates, err := s.loadHTLCFailureAggregates30d(ctx)
	if err != nil && s.logger != nil {
		s.logger.Printf("channel ranking htlc failure aggregate failed: %v", err)
	}

	points := make([]string, 0, len(channels))
	for _, ch := range channels {
		point := strings.TrimSpace(ch.ChannelPoint)
		if point == "" {
			continue
		}
		points = append(points, point)
		item := buildChannelRankingItem(
			now,
			ch,
			labels[ch.ChannelID],
			forwardStats[ch.ChannelID],
			forwardStats30d[ch.ChannelID],
			assistedForwardStats[ch.ChannelID],
			assistedForwardStats30d[ch.ChannelID],
			rebalStats[ch.ChannelID],
			rebalStats30d[ch.ChannelID],
			movementStats7d[ch.ChannelID],
			peerAggregates[strings.TrimSpace(ch.RemotePubkey)],
			htlcAggregates[ch.ChannelID],
		)
		if err := s.applyClosePersistenceGuard(ctx, &item); err != nil {
			return err
		}
		if err := s.upsertItem(ctx, item); err != nil {
			return err
		}
		if err := s.upsertHistoryPoint(ctx, item); err != nil {
			return err
		}
	}

	if len(points) == 0 {
		if _, err := s.db.Exec(ctx, `delete from channel_rankings`); err != nil {
			return err
		}
	} else {
		if _, err := s.db.Exec(ctx, `delete from channel_rankings where not (channel_point = any($1))`, points); err != nil {
			return err
		}
	}

	s.lastSyncAt = now
	return nil
}

func (s *ChannelRankingService) fetchAutofeeLabels(ctx context.Context) (map[uint64]string, error) {
	labels := map[uint64]string{}
	rows, err := s.db.Query(ctx, `
select channel_id, class_label
from autofee_state
where class_label is not null and class_label <> ''
`)
	if err != nil {
		return labels, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var label string
		if err := rows.Scan(&channelID, &label); err != nil {
			return labels, err
		}
		if channelID == 0 || strings.TrimSpace(label) == "" {
			continue
		}
		labels[uint64(channelID)] = strings.TrimSpace(label)
	}
	return labels, rows.Err()
}

func (s *ChannelRankingService) fetchForwardStats7d(ctx context.Context) (map[uint64]channelTrafficStat, error) {
	return s.fetchForwardStats(ctx, 7)
}

func (s *ChannelRankingService) fetchForwardStats30d(ctx context.Context) (map[uint64]channelTrafficStat, error) {
	return s.fetchForwardStats(ctx, 30)
}

func (s *ChannelRankingService) fetchAssistedForwardStats7d(ctx context.Context) (map[uint64]channelTrafficStat, error) {
	return s.fetchAssistedForwardStats(ctx, 7)
}

func (s *ChannelRankingService) fetchAssistedForwardStats30d(ctx context.Context) (map[uint64]channelTrafficStat, error) {
	return s.fetchAssistedForwardStats(ctx, 30)
}

func (s *ChannelRankingService) fetchForwardStats(ctx context.Context, days int) (map[uint64]channelTrafficStat, error) {
	stats := map[uint64]channelTrafficStat{}
	rows, err := s.db.Query(ctx, `
select
  coalesce(chan_id_out, channel_id) as chan_id,
  coalesce(sum(
    case
      when fee_msat > 0 then fee_msat
      when fee_sat > 0 then fee_sat * 1000
      when amount_in_msat > 0 and amount_out_msat > 0 and amount_in_msat > amount_out_msat then amount_in_msat - amount_out_msat
      else 0
    end
  ), 0) as fee_msat,
  coalesce(sum(case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end), 0) as amount_msat
from notifications
where type='forward'
  and occurred_at >= now() - ($1::int * interval '1 day')
  and coalesce(chan_id_out, channel_id) is not null
group by coalesce(chan_id_out, channel_id)
`, days)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var feeMsat int64
		var amountMsat int64
		if err := rows.Scan(&channelID, &feeMsat, &amountMsat); err != nil {
			return stats, err
		}
		if channelID == 0 {
			continue
		}
		stats[uint64(channelID)] = channelTrafficStat{
			FeeSat:    msatToSatCeil(feeMsat),
			AmountSat: amountMsat / 1000,
			Ppm:       ppmMsat(feeMsat, amountMsat),
		}
	}
	return stats, rows.Err()
}

func (s *ChannelRankingService) fetchAssistedForwardStats(ctx context.Context, days int) (map[uint64]channelTrafficStat, error) {
	stats := map[uint64]channelTrafficStat{}
	rows, err := s.db.Query(ctx, `
select
  chan_id_in as chan_id,
  coalesce(sum(
    case
      when fee_msat > 0 then fee_msat
      when fee_sat > 0 then fee_sat * 1000
      when amount_in_msat > 0 and amount_out_msat > 0 and amount_in_msat > amount_out_msat then amount_in_msat - amount_out_msat
      else 0
    end
  ), 0) as fee_msat,
  coalesce(sum(case when amount_in_msat > 0 then amount_in_msat else amount_sat * 1000 end), 0) as amount_msat
from notifications
where type='forward'
  and occurred_at >= now() - ($1::int * interval '1 day')
  and chan_id_in is not null
group by chan_id_in
`, days)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var feeMsat int64
		var amountMsat int64
		if err := rows.Scan(&channelID, &feeMsat, &amountMsat); err != nil {
			return stats, err
		}
		if channelID == 0 {
			continue
		}
		stats[uint64(channelID)] = channelTrafficStat{
			FeeSat:    msatToSatCeil(feeMsat),
			AmountSat: amountMsat / 1000,
			Ppm:       ppmMsat(feeMsat, amountMsat),
		}
	}
	return stats, rows.Err()
}

func (s *ChannelRankingService) fetchRebalanceStats7d(ctx context.Context) (map[uint64]channelTrafficStat, error) {
	return s.fetchRebalanceStats(ctx, 7)
}

func (s *ChannelRankingService) fetchRebalanceStats30d(ctx context.Context) (map[uint64]channelTrafficStat, error) {
	return s.fetchRebalanceStats(ctx, 30)
}

func (s *ChannelRankingService) fetchRebalanceStats(ctx context.Context, days int) (map[uint64]channelTrafficStat, error) {
	stats := map[uint64]channelTrafficStat{}
	rows, err := s.db.Query(ctx, `
select
  coalesce(rebal_target_chan_id, channel_id) as chan_id,
  coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0) as fee_msat,
  coalesce(sum(
    case
      when amount_sat > 0 then amount_sat * 1000
      when amount_out_msat > 0 then amount_out_msat
      else 0
    end
  ), 0) as amount_msat
from notifications
where type='rebalance'
  and status in ('SETTLED', 'SUCCEEDED')
  and occurred_at >= now() - ($1::int * interval '1 day')
  and coalesce(rebal_target_chan_id, channel_id) is not null
group by coalesce(rebal_target_chan_id, channel_id)
`, days)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var feeMsat int64
		var amountMsat int64
		if err := rows.Scan(&channelID, &feeMsat, &amountMsat); err != nil {
			return stats, err
		}
		if channelID == 0 {
			continue
		}
		stats[uint64(channelID)] = channelTrafficStat{
			FeeSat:    msatToSatCeil(feeMsat),
			AmountSat: amountMsat / 1000,
			Ppm:       ppmMsat(feeMsat, amountMsat),
		}
	}
	return stats, rows.Err()
}

func (s *ChannelRankingService) fetchMovementStats7d(ctx context.Context) (map[uint64]lndclient.ChannelMovement7d, error) {
	stats := make(map[uint64]lndclient.ChannelMovement7d)
	rows, err := s.db.Query(ctx, `
with movement as (
  select
    chan_id_in as channel_id,
    'forward_in'::text as metric,
    coalesce(case when amount_in_msat > 0 then amount_in_msat / 1000 else amount_sat end, 0) as amount_sat
  from notifications
  where type='forward'
    and occurred_at >= now() - interval '7 day'
    and chan_id_in is not null

  union all

  select
    chan_id_out as channel_id,
    'forward_out'::text as metric,
    coalesce(case when amount_out_msat > 0 then amount_out_msat / 1000 else amount_sat end, 0) as amount_sat
  from notifications
  where type='forward'
    and occurred_at >= now() - interval '7 day'
    and chan_id_out is not null
)
select channel_id, metric, count(*)::int, coalesce(sum(amount_sat), 0)
from movement
where channel_id > 0
group by channel_id, metric
`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	for rows.Next() {
		var channelID int64
		var metric string
		var count int
		var amountSat int64
		if err := rows.Scan(&channelID, &metric, &count, &amountSat); err != nil {
			return stats, err
		}
		if channelID <= 0 {
			continue
		}
		cid := uint64(channelID)
		entry := stats[cid]
		switch metric {
		case "forward_in":
			entry.ForwardInCount = count
			entry.ForwardInAmountSat = amountSat
		case "forward_out":
			entry.ForwardOutCount = count
			entry.ForwardOutAmountSat = amountSat
		}
		stats[cid] = entry
	}
	return stats, rows.Err()
}

func (s *ChannelRankingService) capturePeerSamples(ctx context.Context, now time.Time, channels []lndclient.ChannelInfo, peers []lndclient.PeerInfo) error {
	if s == nil || s.db == nil {
		return ErrChannelRankingDBUnavailable
	}
	bucket := now.UTC().Truncate(time.Hour)
	samples := make(map[string]channelPeerSample)
	for _, peer := range peers {
		pubkey := strings.TrimSpace(peer.PubKey)
		if pubkey == "" {
			continue
		}
		samples[pubkey] = channelPeerSample{
			Connected: true,
			PingTime:  rankingMaxInt64(0, peer.PingTime),
			HasError:  strings.TrimSpace(peer.LastError) != "",
		}
	}
	for _, ch := range channels {
		pubkey := strings.TrimSpace(ch.RemotePubkey)
		if pubkey == "" {
			continue
		}
		if _, ok := samples[pubkey]; ok {
			continue
		}
		samples[pubkey] = channelPeerSample{
			Connected: false,
			PingTime:  0,
			HasError:  rankingMaxInt64(0, ch.InactiveDurationSec) >= 3600,
		}
	}
	for pubkey, sample := range samples {
		if _, err := s.db.Exec(ctx, `
insert into channel_ranking_peer_samples (
  peer_pubkey, sampled_bucket, connected, ping_time, has_error, created_at
) values (
  $1, $2, $3, $4, $5, $6
)
on conflict (peer_pubkey, sampled_bucket) do update set
  connected = excluded.connected,
  ping_time = excluded.ping_time,
  has_error = excluded.has_error,
  created_at = excluded.created_at
`, pubkey, bucket, sample.Connected, sample.PingTime, sample.HasError, now); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(ctx, `delete from channel_ranking_peer_samples where sampled_bucket < now() - interval '30 day'`)
	return err
}

func (s *ChannelRankingService) loadPeerStability30d(ctx context.Context) (map[string]channelPeerAggregate, error) {
	out := make(map[string]channelPeerAggregate)
	rows, err := s.db.Query(ctx, `
select
  peer_pubkey,
  count(*) as sample_count,
  coalesce(avg(case when connected then 1.0 else 0.0 end), 0) as connected_ratio,
  coalesce(avg(case when has_error then 1.0 else 0.0 end), 0) as error_ratio,
  coalesce(avg(case when connected and ping_time > 0 then ping_time::double precision end), 0) as avg_ping
from channel_ranking_peer_samples
where sampled_bucket >= now() - interval '30 day'
group by peer_pubkey
`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var pubkey string
		var sampleCount int
		var connectedRatio float64
		var errorRatio float64
		var avgPing float64
		if err := rows.Scan(&pubkey, &sampleCount, &connectedRatio, &errorRatio, &avgPing); err != nil {
			return out, err
		}
		pubkey = strings.TrimSpace(pubkey)
		if pubkey == "" {
			continue
		}
		out[pubkey] = channelPeerAggregate{
			Score30d:    scorePeerStability30d(connectedRatio, errorRatio, avgPing),
			SampleCount: sampleCount,
		}
	}
	return out, rows.Err()
}

func scorePeerStability30d(connectedRatio float64, errorRatio float64, avgPing float64) int {
	connectedScore := clampInt(int(math.Round(clampFloat64(connectedRatio, 0, 1)*70)), 0, 70)
	errorPenalty := clampInt(int(math.Round(clampFloat64(errorRatio, 0, 1)*20)), 0, 20)
	pingScore := 10
	switch {
	case avgPing <= 0:
		pingScore = 5
	case avgPing <= 100:
		pingScore = 10
	case avgPing <= 250:
		pingScore = 8
	case avgPing <= 500:
		pingScore = 6
	case avgPing <= 1000:
		pingScore = 3
	default:
		pingScore = 1
	}
	return clampInt(connectedScore-errorPenalty+pingScore, 0, 100)
}

func (s *ChannelRankingService) captureHTLCFailureSamples(ctx context.Context, now time.Time) error {
	if s == nil || s.db == nil || s.htlcFailedProvider == nil {
		return nil
	}
	type bucketKey struct {
		ChannelID uint64
		Bucket    time.Time
	}
	type counts struct {
		Total     int
		Policy    int
		Liquidity int
		Forward   int
	}
	cutoff := now.Add(-30 * 24 * time.Hour)
	aggregates := make(map[bucketKey]counts)
	entries := s.htlcFailedProvider.Failed(htlcManagerMaxLogLimit)
	for _, entry := range entries {
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Timestamp))
		if err != nil || ts.IsZero() || ts.Before(cutoff) {
			continue
		}
		channelID, ok := parseShortChannelID(entry.OutgoingChannelID)
		if !ok || channelID == 0 {
			continue
		}
		key := bucketKey{ChannelID: channelID, Bucket: ts.UTC().Truncate(time.Hour)}
		current := aggregates[key]
		current.Total++
		switch normalizeHTLCFailureEvent(entry) {
		case "forward_fail":
			current.Forward++
		default:
			policy, liquidity := classifyHTLCFailure(entry)
			if policy {
				current.Policy++
			}
			if liquidity {
				current.Liquidity++
			}
		}
		aggregates[key] = current
	}
	for key, sample := range aggregates {
		if _, err := s.db.Exec(ctx, `
insert into channel_ranking_htlc_failures (
  channel_id, sampled_bucket, total_count, policy_count, liquidity_count, forward_count, created_at
) values (
  $1, $2, $3, $4, $5, $6, $7
)
on conflict (channel_id, sampled_bucket) do update set
  total_count = excluded.total_count,
  policy_count = excluded.policy_count,
  liquidity_count = excluded.liquidity_count,
  forward_count = excluded.forward_count,
  created_at = excluded.created_at
`, int64(key.ChannelID), key.Bucket, sample.Total, sample.Policy, sample.Liquidity, sample.Forward, now); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(ctx, `delete from channel_ranking_htlc_failures where sampled_bucket < now() - interval '30 day'`)
	return err
}

func (s *ChannelRankingService) loadHTLCFailureAggregates30d(ctx context.Context) (map[uint64]channelHTLCAggregate, error) {
	out := make(map[uint64]channelHTLCAggregate)
	rows, err := s.db.Query(ctx, `
select channel_id, sum(total_count), sum(policy_count), sum(liquidity_count), sum(forward_count)
from channel_ranking_htlc_failures
where sampled_bucket >= now() - interval '30 day'
group by channel_id
`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var aggregate channelHTLCAggregate
		if err := rows.Scan(&channelID, &aggregate.Total, &aggregate.Policy, &aggregate.Liquidity, &aggregate.Forward); err != nil {
			return out, err
		}
		if channelID <= 0 {
			continue
		}
		out[uint64(channelID)] = aggregate
	}
	return out, rows.Err()
}

func buildChannelRankingItem(
	now time.Time,
	ch lndclient.ChannelInfo,
	classLabel string,
	forward7d channelTrafficStat,
	forward30d channelTrafficStat,
	assisted7d channelTrafficStat,
	assisted30d channelTrafficStat,
	rebal7d channelTrafficStat,
	rebal30d channelTrafficStat,
	movement7d lndclient.ChannelMovement7d,
	peerAggregate channelPeerAggregate,
	htlcAggregate channelHTLCAggregate,
) ChannelRankingItem {
	capacity := rankingMaxInt64(0, ch.CapacitySat)
	localBalance := rankingMaxInt64(0, ch.LocalBalanceSat)
	remoteBalance := rankingMaxInt64(0, ch.RemoteBalanceSat)
	totalBalance := localBalance + remoteBalance
	if totalBalance <= 0 {
		totalBalance = capacity
	}
	localPct := 0.0
	remotePct := 0.0
	if totalBalance > 0 {
		localPct = clampFloat64((float64(localBalance)/float64(totalBalance))*100, 0, 100)
		remotePct = clampFloat64((float64(remoteBalance)/float64(totalBalance))*100, 0, 100)
	}
	profitSat7d := forward7d.FeeSat - rebal7d.FeeSat
	profitSat30d := forward30d.FeeSat - rebal30d.FeeSat
	assistedCredit7d := int64(math.Round(float64(assisted7d.FeeSat) * channelRankingAssistedRevenueWeight))
	assistedCredit30d := int64(math.Round(float64(assisted30d.FeeSat) * channelRankingAssistedRevenueWeight))
	effectiveProfitSat7d := profitSat7d + assistedCredit7d
	effectiveProfitSat30d := profitSat30d + assistedCredit30d
	effectiveForward7d := channelTrafficStat{
		FeeSat:    forward7d.FeeSat + assistedCredit7d,
		AmountSat: forward7d.AmountSat + int64(math.Round(float64(assisted7d.AmountSat)*channelRankingAssistedRevenueWeight)),
		Ppm:       forward7d.Ppm,
	}
	effectiveForward30d := channelTrafficStat{
		FeeSat:    forward30d.FeeSat + assistedCredit30d,
		AmountSat: forward30d.AmountSat + int64(math.Round(float64(assisted30d.AmountSat)*channelRankingAssistedRevenueWeight)),
		Ppm:       forward30d.Ppm,
	}
	rebalanceDependenceScore := computeRebalanceDependenceScore(forward30d, rebal30d)
	rebalanceNoPayback := channelRankingRebalanceNoPaybackAfter7d(forward7d, assisted7d, rebal7d, rebal30d, effectiveProfitSat30d)
	score7d := computeChannelRankingScore(ch, capacity, localPct, effectiveForward7d, rebal7d, effectiveProfitSat7d, peerAggregate.Score30d, htlcAggregate, rebalanceDependenceScore)
	score7d = applyRebalanceNoPaybackPenalty(score7d, rebalanceNoPayback)
	score30d := computeChannelRankingScore(ch, capacity, localPct, effectiveForward30d, rebal30d, effectiveProfitSat30d, peerAggregate.Score30d, htlcAggregate, rebalanceDependenceScore)
	trendDirection, trendDelta := computeChannelRankingTrend(score7d, score30d)
	state, reasons, recommendations := classifyChannelRanking(
		ch,
		capacity,
		localPct,
		forward7d,
		forward30d,
		effectiveForward7d,
		effectiveForward30d,
		assisted7d,
		assisted30d,
		rebal7d,
		rebal30d,
		profitSat7d,
		profitSat30d,
		effectiveProfitSat7d,
		effectiveProfitSat30d,
		score7d,
		score30d,
		peerAggregate.Score30d,
		peerAggregate.SampleCount,
		htlcAggregate,
		rebalanceDependenceScore,
		rebalanceNoPayback,
	)

	return ChannelRankingItem{
		ChannelPoint:             strings.TrimSpace(ch.ChannelPoint),
		ChannelID:                int64(ch.ChannelID),
		PeerPubkey:               strings.TrimSpace(ch.RemotePubkey),
		PeerAlias:                strings.TrimSpace(ch.PeerAlias),
		Active:                   ch.Active,
		Private:                  ch.Private,
		CapacitySat:              capacity,
		LocalBalanceSat:          localBalance,
		RemoteBalanceSat:         remoteBalance,
		LocalBalancePct:          localPct,
		RemoteBalancePct:         remotePct,
		InactiveDurationSec:      rankingMaxInt64(0, ch.InactiveDurationSec),
		PendingHtlcCount:         rankingMaxInt(0, ch.PendingHtlcCount),
		ClassLabel:               strings.TrimSpace(classLabel),
		ForwardInCount7d:         rankingMaxInt(0, movement7d.ForwardInCount),
		ForwardInAmountSat7d:     rankingMaxInt64(0, movement7d.ForwardInAmountSat),
		ForwardOutCount7d:        rankingMaxInt(0, movement7d.ForwardOutCount),
		ForwardOutAmountSat7d:    rankingMaxInt64(0, movement7d.ForwardOutAmountSat),
		ForwardFee7dSat:          forward7d.FeeSat,
		ForwardAmt7dSat:          forward7d.AmountSat,
		AssistedForwardFee7dSat:  assisted7d.FeeSat,
		AssistedForwardAmt7dSat:  assisted7d.AmountSat,
		OutPpm7d:                 forward7d.Ppm,
		ForwardFee30dSat:         forward30d.FeeSat,
		ForwardAmt30dSat:         forward30d.AmountSat,
		AssistedForwardFee30dSat: assisted30d.FeeSat,
		AssistedForwardAmt30dSat: assisted30d.AmountSat,
		OutPpm30d:                forward30d.Ppm,
		RebalFee7dSat:            rebal7d.FeeSat,
		RebalAmt7dSat:            rebal7d.AmountSat,
		RebalPpm7d:               rebal7d.Ppm,
		RebalFee30dSat:           rebal30d.FeeSat,
		RebalAmt30dSat:           rebal30d.AmountSat,
		RebalPpm30d:              rebal30d.Ppm,
		ProfitFee7dSat:           profitSat7d,
		ProfitFee30dSat:          profitSat30d,
		PeerStabilityScore30d:    peerAggregate.Score30d,
		PeerSampleCount30d:       peerAggregate.SampleCount,
		HTLCFailures30d:          htlcAggregate.Total,
		HTLCPolicyFails30d:       htlcAggregate.Policy,
		HTLCLiquidityFails30d:    htlcAggregate.Liquidity,
		HTLCForwardFails30d:      htlcAggregate.Forward,
		RebalanceDependenceScore: rebalanceDependenceScore,
		Score:                    score7d,
		Score7d:                  score7d,
		Score30d:                 score30d,
		TrendDirection:           trendDirection,
		TrendDelta:               trendDelta,
		State:                    state,
		Reasons:                  reasons,
		Recommendations:          recommendations,
		CloseCandidate:           state == "close",
		ComputedAt:               now,
	}
}

func computeChannelRankingScore(ch lndclient.ChannelInfo, capacity int64, localPct float64, forward channelTrafficStat, rebal channelTrafficStat, profitSat int64, peerStabilityScore30d int, htlcAggregate channelHTLCAggregate, rebalanceDependenceScore int) int {
	profitScore := scoreProfitability(profitSat)
	efficiencyScore := scoreCapitalEfficiency(profitSat, capacity)
	utilizationScore := scoreUtilization(capacity, localPct, forward.AmountSat)
	maintenanceScore := scoreMaintenance(forward, rebal, rebalanceDependenceScore)
	healthScore := scoreOperationalHealth(ch, peerStabilityScore30d, htlcAggregate)
	confidenceScore := scoreConfidence(ch, forward, rebal)

	total := profitScore + efficiencyScore + utilizationScore + maintenanceScore + healthScore + confidenceScore
	return clampInt(total, 0, 100)
}

func computeChannelRankingTrend(score7d int, score30d int) (string, int) {
	delta := score7d - score30d
	switch {
	case delta >= 8:
		return "improving", delta
	case delta <= -8:
		return "worsening", delta
	default:
		return "stable", delta
	}
}

func channelRankingRebalanceNoPaybackAfter7d(forward7d channelTrafficStat, assisted7d channelTrafficStat, rebal7d channelTrafficStat, rebal30d channelTrafficStat, effectiveProfitSat30d int64) bool {
	if rebal30d.FeeSat <= 0 || rebal30d.AmountSat <= 0 || effectiveProfitSat30d >= 0 {
		return false
	}
	if rebal7d.FeeSat > 0 || rebal7d.AmountSat > 0 {
		return false
	}
	if forward7d.FeeSat > 0 || forward7d.AmountSat > 0 || assisted7d.FeeSat > 0 || assisted7d.AmountSat > 0 {
		return false
	}
	return true
}

func applyRebalanceNoPaybackPenalty(score int, noPayback bool) int {
	if !noPayback {
		return score
	}
	return clampInt(score-channelRankingRebalanceNoPaybackPenalty, 0, channelRankingRebalanceNoPaybackScoreCap)
}

func appendUniqueRecommendation(list []ChannelRankingRecommendation, candidate ChannelRankingRecommendation) []ChannelRankingRecommendation {
	code := strings.TrimSpace(candidate.Code)
	target := strings.TrimSpace(candidate.TargetModule)
	if code == "" {
		return list
	}
	for _, existing := range list {
		if strings.TrimSpace(existing.Code) == code && strings.TrimSpace(existing.TargetModule) == target {
			return list
		}
	}
	return append(list, candidate)
}

func appendUniqueReason(list []ChannelRankingReason, candidate ChannelRankingReason) []ChannelRankingReason {
	code := strings.TrimSpace(candidate.Code)
	if code == "" {
		return list
	}
	for _, existing := range list {
		if strings.TrimSpace(existing.Code) == code {
			return list
		}
	}
	return append(list, ChannelRankingReason{Code: code})
}

func filterCloseActionRecommendations(list []ChannelRankingRecommendation) []ChannelRankingRecommendation {
	out := make([]ChannelRankingRecommendation, 0, len(list))
	for _, recommendation := range list {
		switch strings.TrimSpace(recommendation.Code) {
		case "prepare_coop_close", "review_with_close_manager", "stop_nonessential_rebalances":
			continue
		default:
			out = append(out, recommendation)
		}
	}
	return out
}

func (s *ChannelRankingService) applyClosePersistenceGuard(ctx context.Context, item *ChannelRankingItem) error {
	if item == nil || !item.CloseCandidate || strings.TrimSpace(item.State) != "close" {
		return nil
	}
	matured, err := s.closeCandidateMatured(ctx, item.ChannelPoint, item.ComputedAt)
	if err != nil {
		return err
	}
	if matured {
		return nil
	}

	item.State = "monitor"
	item.Reasons = appendUniqueReason(item.Reasons, ChannelRankingReason{Code: "close_candidate_pending_30d"})
	item.Recommendations = filterCloseActionRecommendations(item.Recommendations)
	item.Recommendations = appendUniqueRecommendation(item.Recommendations, ChannelRankingRecommendation{Code: "observe_30d_before_close", TargetModule: "lightning-ops"})
	if item.RebalFee7dSat > 0 && item.RebalFee7dSat >= rankingMaxInt64(50, item.ForwardFee7dSat) {
		item.Recommendations = appendUniqueRecommendation(item.Recommendations, ChannelRankingRecommendation{Code: "reduce_rebalance_priority", TargetModule: "rebalance"})
	}
	if item.ProfitFee7dSat <= 0 {
		item.Recommendations = appendUniqueRecommendation(item.Recommendations, ChannelRankingRecommendation{Code: "review_autofee_bounds", TargetModule: "autofee"})
	}
	if len(item.Recommendations) > 3 {
		item.Recommendations = item.Recommendations[:3]
	}
	return nil
}

func (s *ChannelRankingService) closeCandidateMatured(ctx context.Context, channelPoint string, now time.Time) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrChannelRankingDBUnavailable
	}
	channelPoint = strings.TrimSpace(channelPoint)
	if channelPoint == "" {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var candidateSince sql.NullTime
	err := s.db.QueryRow(ctx, `
with last_non_candidate as (
  select max(computed_bucket) as ts
  from channel_ranking_history
  where channel_point = $1
    and not (coalesce(close_candidate, false) or state = 'close')
),
candidate_run as (
  select min(computed_bucket) as since
  from channel_ranking_history
  where channel_point = $1
    and (coalesce(close_candidate, false) or state = 'close')
    and computed_bucket > coalesce((select ts from last_non_candidate), '-infinity'::timestamptz)
)
select since from candidate_run
`, channelPoint).Scan(&candidateSince)
	if err != nil {
		return false, err
	}
	if !candidateSince.Valid {
		return false, nil
	}
	return !candidateSince.Time.After(now.UTC().Add(-channelRankingClosePersistence)), nil
}

func channelRankingHTLCOperationalRiskHigh(aggregate channelHTLCAggregate) bool {
	linkFailures := rankingMaxInt(0, aggregate.Policy) + rankingMaxInt(0, aggregate.Liquidity)
	return rankingMaxInt(0, aggregate.Policy) >= channelRankingHTLCPolicyHigh30d ||
		rankingMaxInt(0, aggregate.Liquidity) >= channelRankingHTLCLiquidityHigh30d ||
		linkFailures >= channelRankingHTLCLinkHigh30d
}

func classifyChannelRanking(
	ch lndclient.ChannelInfo,
	capacity int64,
	localPct float64,
	forward7d channelTrafficStat,
	forward30d channelTrafficStat,
	effectiveForward7d channelTrafficStat,
	effectiveForward30d channelTrafficStat,
	assisted7d channelTrafficStat,
	assisted30d channelTrafficStat,
	rebal7d channelTrafficStat,
	rebal30d channelTrafficStat,
	profitSat7d int64,
	profitSat30d int64,
	effectiveProfitSat7d int64,
	effectiveProfitSat30d int64,
	score7d int,
	score30d int,
	peerStabilityScore30d int,
	peerSampleCount30d int,
	htlcAggregate channelHTLCAggregate,
	rebalanceDependenceScore int,
	rebalanceNoPayback bool,
) (string, []ChannelRankingReason, []ChannelRankingRecommendation) {
	reasons := make([]ChannelRankingReason, 0, 6)
	recommendations := make([]ChannelRankingRecommendation, 0, 4)

	if profitSat7d > 0 {
		reasons = append(reasons, ChannelRankingReason{Code: "positive_net_fees"})
	}
	if profitSat7d < 0 {
		reasons = append(reasons, ChannelRankingReason{Code: "negative_net_fees"})
	}
	if assisted7d.FeeSat > 0 || assisted30d.FeeSat > 0 {
		reasons = append(reasons, ChannelRankingReason{Code: "assisted_revenue_support"})
	}
	if capacity > 0 && forward7d.AmountSat >= rankingMaxInt64(50000, capacity/2) {
		reasons = append(reasons, ChannelRankingReason{Code: "strong_volume"})
	}
	if capacity > 0 && effectiveForward7d.AmountSat < rankingMaxInt64(25000, capacity/50) {
		reasons = append(reasons, ChannelRankingReason{Code: "low_usage"})
	}
	if rebal7d.FeeSat > 0 && rebal7d.FeeSat >= rankingMaxInt64(50, forward7d.FeeSat) {
		reasons = append(reasons, ChannelRankingReason{Code: "rebalance_cost_high"})
	}
	if localPct < 10 || localPct > 90 {
		reasons = append(reasons, ChannelRankingReason{Code: "liquidity_unbalanced"})
	}
	if !ch.Active || rankingMaxInt64(0, ch.InactiveDurationSec) >= 3600 {
		reasons = append(reasons, ChannelRankingReason{Code: "peer_inactive"})
	}
	if ch.PendingHtlcCount >= 5 {
		reasons = append(reasons, ChannelRankingReason{Code: "pending_htlc_pressure"})
	}
	if peerStabilityScore30d > 0 && peerStabilityScore30d < 45 {
		reasons = append(reasons, ChannelRankingReason{Code: "peer_stability_low"})
	}
	if htlcAggregate.Total >= 5 {
		reasons = append(reasons, ChannelRankingReason{Code: "htlc_failures_elevated"})
	}
	if rebalanceDependenceScore >= 65 {
		reasons = append(reasons, ChannelRankingReason{Code: "rebalance_dependence_high"})
	}
	if rebalanceNoPayback {
		reasons = append(reasons, ChannelRankingReason{Code: "rebalance_no_payback"})
	}
	if capacity > 0 {
		netPpm := (float64(effectiveProfitSat7d) / float64(capacity)) * 1_000_000
		if netPpm >= 75 {
			reasons = append(reasons, ChannelRankingReason{Code: "capital_efficiency_high"})
		}
		if netPpm < 0 {
			reasons = append(reasons, ChannelRankingReason{Code: "capital_efficiency_low"})
		}
	}

	state := "monitor"
	strongVolume := capacity > 0 && effectiveForward7d.AmountSat >= rankingMaxInt64(100000, capacity/5)
	rebalanceHeavy7d := rebal7d.FeeSat > 0 && rebal7d.FeeSat >= rankingMaxInt64(50, forward7d.FeeSat)
	rebalanceHeavy30d := rebal30d.FeeSat > 0 && rebal30d.FeeSat >= rankingMaxInt64(150, rankingMaxInt64(1, forward30d.FeeSat))
	longInactive := !ch.Active && rankingMaxInt64(0, ch.InactiveDurationSec) >= 7*24*3600
	unstablePeer := peerStabilityScore30d > 0 && peerStabilityScore30d < 45
	htlcFailuresHigh := channelRankingHTLCOperationalRiskHigh(htlcAggregate)
	persistentWeakEconomics := effectiveProfitSat30d <= 0 || score30d < 36 || rebalanceHeavy30d || rebalanceNoPayback
	severeOperationalRisk := longInactive || unstablePeer || htlcFailuresHigh || rebalanceDependenceScore >= 85 || rebalanceNoPayback
	closeWarmup := peerSampleCount30d > 0 && peerSampleCount30d < 336
	closeDeferredForWarmup := false

	switch {
	case ch.Active && score7d >= 72 && effectiveProfitSat7d > 0 && strongVolume && !unstablePeer:
		state = "expand"
	case score7d < 24 && (severeOperationalRisk || (persistentWeakEconomics && (effectiveProfitSat7d <= -150 || rebalanceHeavy7d || rebalanceDependenceScore >= 65))):
		if closeWarmup {
			state = "monitor"
			closeDeferredForWarmup = true
		} else {
			state = "close"
		}
	case ch.Active && score7d >= 48 && effectiveProfitSat7d >= -50 && !rebalanceHeavy7d && rebalanceDependenceScore < 65 && !htlcFailuresHigh && !unstablePeer:
		state = "maintain"
	default:
		state = "monitor"
	}

	switch state {
	case "expand":
		recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "consider_more_capacity", TargetModule: "lightning-ops"})
		recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "preserve_rebalance_priority", TargetModule: "rebalance"})
		recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "keep_autofee_active", TargetModule: "autofee"})
	case "maintain":
		recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "keep_current_policy", TargetModule: "lightning-ops"})
		recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "keep_autofee_active", TargetModule: "autofee"})
	case "close":
		recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "stop_nonessential_rebalances", TargetModule: "rebalance"})
		if rebalanceNoPayback {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "prepare_close_candidate", TargetModule: "lightning-ops"})
		} else {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "prepare_coop_close", TargetModule: "lightning-ops"})
		}
		recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "review_with_close_manager", TargetModule: "close-manager"})
	default:
		if closeDeferredForWarmup {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "observe_7d_before_close", TargetModule: "lightning-ops"})
		}
		if rebalanceNoPayback {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "stop_nonessential_rebalances", TargetModule: "rebalance"})
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "prepare_close_candidate", TargetModule: "lightning-ops"})
		}
		if rebalanceHeavy7d {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "reduce_rebalance_priority", TargetModule: "rebalance"})
		}
		if rebalanceDependenceScore >= 65 {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "reduce_rebalance_dependence", TargetModule: "rebalance"})
		}
		if effectiveProfitSat7d <= 0 {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "review_autofee_bounds", TargetModule: "autofee"})
		}
		if localPct < 10 || localPct > 90 {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "review_fee_positioning", TargetModule: "autofee"})
		}
		if !ch.Active || rankingMaxInt64(0, ch.InactiveDurationSec) >= 3600 {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "check_peer_stability", TargetModule: "lightning-ops"})
		}
		if htlcAggregate.Total >= 5 {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "review_htlc_failures", TargetModule: "htlc-manager"})
		}
		if capacity > 0 && effectiveForward7d.AmountSat < rankingMaxInt64(25000, capacity/50) {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "observe_7d_before_close", TargetModule: "lightning-ops"})
		}
		if profitSat7d < 0 && effectiveProfitSat7d > profitSat7d {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "review_inbound_assist_role", TargetModule: "lightning-ops"})
		}
		if ch.PendingHtlcCount >= 5 {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "review_with_close_manager", TargetModule: "close-manager"})
		}
		if len(recommendations) == 0 {
			recommendations = appendUniqueRecommendation(recommendations, ChannelRankingRecommendation{Code: "keep_channel_under_observation", TargetModule: "lightning-ops"})
		}
	}

	if len(reasons) > 5 {
		reasons = reasons[:5]
	}
	if len(recommendations) > 3 {
		recommendations = recommendations[:3]
	}
	return state, reasons, recommendations
}

func scoreProfitability(profitSat int64) int {
	switch {
	case profitSat >= 750:
		return 35
	case profitSat >= 400:
		return 30
	case profitSat >= 150:
		return 24
	case profitSat >= 0:
		return 18
	case profitSat >= -100:
		return 10
	case profitSat >= -300:
		return 4
	default:
		return 0
	}
}

func scoreCapitalEfficiency(profitSat int64, capacity int64) int {
	if capacity <= 0 {
		return 0
	}
	netPpm := (float64(profitSat) / float64(capacity)) * 1_000_000
	switch {
	case netPpm >= 150:
		return 20
	case netPpm >= 75:
		return 17
	case netPpm >= 25:
		return 14
	case netPpm >= 0:
		return 10
	case netPpm >= -25:
		return 6
	case netPpm >= -75:
		return 3
	default:
		return 0
	}
}

func scoreUtilization(capacity int64, localPct float64, forwardAmtSat int64) int {
	if capacity <= 0 {
		return 0
	}
	ratio := float64(forwardAmtSat) / float64(capacity)
	ratioScore := 0
	switch {
	case ratio >= 2:
		ratioScore = 12
	case ratio >= 1:
		ratioScore = 10
	case ratio >= 0.5:
		ratioScore = 8
	case ratio >= 0.2:
		ratioScore = 6
	case ratio >= 0.05:
		ratioScore = 3
	default:
		ratioScore = 0
	}
	balanceScore := 0
	switch {
	case localPct >= 20 && localPct <= 80:
		balanceScore = 3
	case localPct >= 10 && localPct <= 90:
		balanceScore = 1
	default:
		balanceScore = 0
	}
	return clampInt(ratioScore+balanceScore, 0, 15)
}

func computeRebalanceDependenceScore(forward channelTrafficStat, rebal channelTrafficStat) int {
	if rebal.AmountSat <= 0 && rebal.FeeSat <= 0 {
		return 0
	}
	if forward.AmountSat <= 0 && rebal.AmountSat > 0 {
		return 90
	}
	amountRatio := 0.0
	if forward.AmountSat > 0 {
		amountRatio = float64(rebal.AmountSat) / float64(forward.AmountSat)
	}
	feeRatio := 0.0
	if forward.FeeSat > 0 {
		feeRatio = float64(rebal.FeeSat) / float64(forward.FeeSat)
	} else if rebal.FeeSat > 0 {
		feeRatio = 2.0
	}
	score := 0
	switch {
	case amountRatio >= 1.5:
		score += 50
	case amountRatio >= 1.0:
		score += 38
	case amountRatio >= 0.5:
		score += 26
	case amountRatio >= 0.2:
		score += 14
	default:
		score += 6
	}
	switch {
	case feeRatio >= 1.5:
		score += 40
	case feeRatio >= 1.0:
		score += 32
	case feeRatio >= 0.5:
		score += 22
	case feeRatio > 0:
		score += 10
	}
	return clampInt(score, 0, 100)
}

func scoreMaintenance(forward channelTrafficStat, rebal channelTrafficStat, rebalanceDependenceScore int) int {
	score := 15
	if rebal.FeeSat <= 0 {
		return clampInt(score-(rebalanceDependenceScore/20), 0, 15)
	}
	if forward.FeeSat <= 0 {
		score = 2
	} else {
		relation := float64(rebal.FeeSat) / float64(rankingMaxInt64(1, forward.FeeSat))
		switch {
		case relation <= 0.25:
			score = 12
		case relation <= 0.5:
			score = 9
		case relation <= 1.0:
			score = 5
		case relation <= 1.5:
			score = 2
		default:
			score = 0
		}
	}
	if rebal.Ppm > 0 && forward.Ppm > 0 && rebal.Ppm > forward.Ppm {
		score -= 2
	}
	score -= rebalanceDependenceScore / 18
	return clampInt(score, 0, 15)
}

func scoreOperationalHealth(ch lndclient.ChannelInfo, peerStabilityScore30d int, htlcAggregate channelHTLCAggregate) int {
	if peerStabilityScore30d > 0 {
		base := 0
		switch {
		case peerStabilityScore30d >= 85:
			base = 10
		case peerStabilityScore30d >= 70:
			base = 8
		case peerStabilityScore30d >= 55:
			base = 6
		case peerStabilityScore30d >= 40:
			base = 4
		default:
			base = 2
		}
		switch {
		case htlcAggregate.Total >= 12 || htlcAggregate.Liquidity >= 6 || htlcAggregate.Policy >= 6:
			base -= 4
		case htlcAggregate.Total >= 6:
			base -= 2
		case htlcAggregate.Total >= 3:
			base -= 1
		}
		if !ch.Active && rankingMaxInt64(0, ch.InactiveDurationSec) >= 24*3600 {
			base -= 2
		}
		return clampInt(base, 0, 10)
	}
	switch {
	case ch.Active && ch.PendingHtlcCount < 3:
		return 10
	case ch.Active && ch.PendingHtlcCount < 8:
		return 7
	case !ch.Active && ch.InactiveDurationSec < 24*3600:
		return 4
	default:
		return 1
	}
}

func scoreConfidence(ch lndclient.ChannelInfo, forward channelTrafficStat, rebal channelTrafficStat) int {
	if forward.AmountSat > 0 || rebal.AmountSat > 0 {
		return 5
	}
	if ch.Active {
		return 3
	}
	return 1
}

func (s *ChannelRankingService) upsertItem(ctx context.Context, item ChannelRankingItem) error {
	reasonsRaw, err := json.Marshal(item.Reasons)
	if err != nil {
		return err
	}
	recommendationsRaw, err := json.Marshal(item.Recommendations)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
insert into channel_rankings (
  channel_point, channel_id, peer_pubkey, peer_alias, active, private, capacity_sat,
  local_balance_sat, remote_balance_sat, local_balance_pct, remote_balance_pct,
  inactive_duration_sec, pending_htlc_count, class_label,
  forward_in_count_7d, forward_in_amount_sat_7d, forward_out_count_7d, forward_out_amount_sat_7d,
  forward_fee_7d_sat, forward_amt_7d_sat, assisted_forward_fee_7d_sat, assisted_forward_amt_7d_sat, out_ppm_7d,
  forward_fee_30d_sat, forward_amt_30d_sat, assisted_forward_fee_30d_sat, assisted_forward_amt_30d_sat, out_ppm_30d,
  rebal_fee_7d_sat, rebal_amt_7d_sat, rebal_ppm_7d,
  rebal_fee_30d_sat, rebal_amt_30d_sat, rebal_ppm_30d,
  profit_fee_7d_sat, profit_fee_30d_sat,
  peer_stability_score_30d, peer_sample_count_30d,
  htlc_failures_30d, htlc_policy_fails_30d, htlc_liquidity_fails_30d, htlc_forward_fails_30d,
  rebalance_dependence_score,
  score, score_7d, score_30d, trend_direction, trend_delta, state, reasons_json, recommendations_json, computed_at
) values (
  $1, $2, $3, $4, $5, $6, $7,
  $8, $9, $10, $11,
  $12, $13, $14,
  $15, $16, $17, $18,
  $19, $20, $21, $22, $23,
  $24, $25, $26, $27, $28,
  $29, $30, $31,
  $32, $33, $34,
  $35, $36,
  $37, $38,
  $39, $40, $41, $42,
  $43,
  $44, $45, $46, $47, $48, $49, $50, $51, $52
)
on conflict (channel_point) do update set
  channel_id = excluded.channel_id,
  peer_pubkey = excluded.peer_pubkey,
  peer_alias = excluded.peer_alias,
  active = excluded.active,
  private = excluded.private,
  capacity_sat = excluded.capacity_sat,
  local_balance_sat = excluded.local_balance_sat,
  remote_balance_sat = excluded.remote_balance_sat,
  local_balance_pct = excluded.local_balance_pct,
  remote_balance_pct = excluded.remote_balance_pct,
  inactive_duration_sec = excluded.inactive_duration_sec,
  pending_htlc_count = excluded.pending_htlc_count,
  class_label = excluded.class_label,
  forward_in_count_7d = excluded.forward_in_count_7d,
  forward_in_amount_sat_7d = excluded.forward_in_amount_sat_7d,
  forward_out_count_7d = excluded.forward_out_count_7d,
  forward_out_amount_sat_7d = excluded.forward_out_amount_sat_7d,
  forward_fee_7d_sat = excluded.forward_fee_7d_sat,
  forward_amt_7d_sat = excluded.forward_amt_7d_sat,
  assisted_forward_fee_7d_sat = excluded.assisted_forward_fee_7d_sat,
  assisted_forward_amt_7d_sat = excluded.assisted_forward_amt_7d_sat,
  out_ppm_7d = excluded.out_ppm_7d,
  forward_fee_30d_sat = excluded.forward_fee_30d_sat,
  forward_amt_30d_sat = excluded.forward_amt_30d_sat,
  assisted_forward_fee_30d_sat = excluded.assisted_forward_fee_30d_sat,
  assisted_forward_amt_30d_sat = excluded.assisted_forward_amt_30d_sat,
  out_ppm_30d = excluded.out_ppm_30d,
  rebal_fee_7d_sat = excluded.rebal_fee_7d_sat,
  rebal_amt_7d_sat = excluded.rebal_amt_7d_sat,
  rebal_ppm_7d = excluded.rebal_ppm_7d,
  rebal_fee_30d_sat = excluded.rebal_fee_30d_sat,
  rebal_amt_30d_sat = excluded.rebal_amt_30d_sat,
  rebal_ppm_30d = excluded.rebal_ppm_30d,
  profit_fee_7d_sat = excluded.profit_fee_7d_sat,
  profit_fee_30d_sat = excluded.profit_fee_30d_sat,
  peer_stability_score_30d = excluded.peer_stability_score_30d,
  peer_sample_count_30d = excluded.peer_sample_count_30d,
  htlc_failures_30d = excluded.htlc_failures_30d,
  htlc_policy_fails_30d = excluded.htlc_policy_fails_30d,
  htlc_liquidity_fails_30d = excluded.htlc_liquidity_fails_30d,
  htlc_forward_fails_30d = excluded.htlc_forward_fails_30d,
  rebalance_dependence_score = excluded.rebalance_dependence_score,
  score = excluded.score,
  score_7d = excluded.score_7d,
  score_30d = excluded.score_30d,
  trend_direction = excluded.trend_direction,
  trend_delta = excluded.trend_delta,
  state = excluded.state,
  reasons_json = excluded.reasons_json,
  recommendations_json = excluded.recommendations_json,
  computed_at = excluded.computed_at
`, item.ChannelPoint, item.ChannelID, nullableString(item.PeerPubkey), nullableString(item.PeerAlias), item.Active, item.Private, item.CapacitySat,
		item.LocalBalanceSat, item.RemoteBalanceSat, item.LocalBalancePct, item.RemoteBalancePct,
		item.InactiveDurationSec, item.PendingHtlcCount, nullableString(item.ClassLabel),
		item.ForwardInCount7d, item.ForwardInAmountSat7d, item.ForwardOutCount7d, item.ForwardOutAmountSat7d,
		item.ForwardFee7dSat, item.ForwardAmt7dSat, item.AssistedForwardFee7dSat, item.AssistedForwardAmt7dSat, item.OutPpm7d,
		item.ForwardFee30dSat, item.ForwardAmt30dSat, item.AssistedForwardFee30dSat, item.AssistedForwardAmt30dSat, item.OutPpm30d,
		item.RebalFee7dSat, item.RebalAmt7dSat, item.RebalPpm7d,
		item.RebalFee30dSat, item.RebalAmt30dSat, item.RebalPpm30d,
		item.ProfitFee7dSat, item.ProfitFee30dSat,
		item.PeerStabilityScore30d, item.PeerSampleCount30d,
		item.HTLCFailures30d, item.HTLCPolicyFails30d, item.HTLCLiquidityFails30d, item.HTLCForwardFails30d,
		item.RebalanceDependenceScore,
		item.Score, item.Score7d, item.Score30d, item.TrendDirection, item.TrendDelta, item.State, reasonsRaw, recommendationsRaw, item.ComputedAt)
	return err
}

func (s *ChannelRankingService) upsertHistoryPoint(ctx context.Context, item ChannelRankingItem) error {
	computedBucket := item.ComputedAt.UTC().Truncate(time.Hour)
	_, err := s.db.Exec(ctx, `
insert into channel_ranking_history (
  channel_point, computed_bucket, score, score_7d, score_30d, trend_direction, trend_delta, state, close_candidate, profit_fee_7d_sat, profit_fee_30d_sat, created_at
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
on conflict (channel_point, computed_bucket) do update set
  score = excluded.score,
  score_7d = excluded.score_7d,
  score_30d = excluded.score_30d,
  trend_direction = excluded.trend_direction,
  trend_delta = excluded.trend_delta,
  state = excluded.state,
  close_candidate = excluded.close_candidate,
  profit_fee_7d_sat = excluded.profit_fee_7d_sat,
  profit_fee_30d_sat = excluded.profit_fee_30d_sat,
  created_at = excluded.created_at
`, item.ChannelPoint, computedBucket, item.Score, item.Score7d, item.Score30d, item.TrendDirection, item.TrendDelta, item.State, item.CloseCandidate, item.ProfitFee7dSat, item.ProfitFee30dSat, item.ComputedAt)
	return err
}

func (s *ChannelRankingService) List(ctx context.Context, limit int, stateFilter string) ([]ChannelRankingItem, ChannelRankingStatus, error) {
	if s == nil || s.db == nil {
		return nil, ChannelRankingStatus{}, ErrChannelRankingDBUnavailable
	}
	limit = normalizeCloseManagerListLimit(limit)
	stateFilter = strings.TrimSpace(strings.ToLower(stateFilter))

	args := []any{limit}
	query := `
select
  channel_point, channel_id, peer_pubkey, peer_alias, active, private, capacity_sat,
  local_balance_sat, remote_balance_sat, local_balance_pct, remote_balance_pct,
  inactive_duration_sec, pending_htlc_count, class_label,
  forward_in_count_7d, forward_in_amount_sat_7d, forward_out_count_7d, forward_out_amount_sat_7d,
  forward_fee_7d_sat, forward_amt_7d_sat, assisted_forward_fee_7d_sat, assisted_forward_amt_7d_sat, out_ppm_7d,
  forward_fee_30d_sat, forward_amt_30d_sat, assisted_forward_fee_30d_sat, assisted_forward_amt_30d_sat, out_ppm_30d,
  rebal_fee_7d_sat, rebal_amt_7d_sat, rebal_ppm_7d,
  rebal_fee_30d_sat, rebal_amt_30d_sat, rebal_ppm_30d,
  profit_fee_7d_sat, profit_fee_30d_sat, peer_stability_score_30d, peer_sample_count_30d,
  htlc_failures_30d, htlc_policy_fails_30d, htlc_liquidity_fails_30d, htlc_forward_fails_30d, rebalance_dependence_score,
  score, score_7d, score_30d, trend_direction, trend_delta, state, reasons_json, recommendations_json, computed_at
from channel_rankings
`
	if stateFilter != "" && stateFilter != "all" {
		query += ` where state = $2`
		args = append(args, stateFilter)
	}
	query += `
order by score desc, profit_fee_7d_sat desc, capacity_sat desc, peer_alias asc, channel_point asc
limit $1
`
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, ChannelRankingStatus{}, err
	}
	defer rows.Close()
	items, err := scanChannelRankingItems(rows)
	if err != nil {
		return nil, ChannelRankingStatus{}, err
	}
	status, err := s.Status(ctx)
	if err != nil {
		return nil, ChannelRankingStatus{}, err
	}
	return items, status, nil
}

func (s *ChannelRankingService) Get(ctx context.Context, channelPoint string) (*ChannelRankingItem, error) {
	if s == nil || s.db == nil {
		return nil, ErrChannelRankingDBUnavailable
	}
	rows, err := s.db.Query(ctx, `
select
  channel_point, channel_id, peer_pubkey, peer_alias, active, private, capacity_sat,
  local_balance_sat, remote_balance_sat, local_balance_pct, remote_balance_pct,
  inactive_duration_sec, pending_htlc_count, class_label,
  forward_in_count_7d, forward_in_amount_sat_7d, forward_out_count_7d, forward_out_amount_sat_7d,
  forward_fee_7d_sat, forward_amt_7d_sat, assisted_forward_fee_7d_sat, assisted_forward_amt_7d_sat, out_ppm_7d,
  forward_fee_30d_sat, forward_amt_30d_sat, assisted_forward_fee_30d_sat, assisted_forward_amt_30d_sat, out_ppm_30d,
  rebal_fee_7d_sat, rebal_amt_7d_sat, rebal_ppm_7d,
  rebal_fee_30d_sat, rebal_amt_30d_sat, rebal_ppm_30d,
  profit_fee_7d_sat, profit_fee_30d_sat, peer_stability_score_30d, peer_sample_count_30d,
  htlc_failures_30d, htlc_policy_fails_30d, htlc_liquidity_fails_30d, htlc_forward_fails_30d, rebalance_dependence_score,
  score, score_7d, score_30d, trend_direction, trend_delta, state, reasons_json, recommendations_json, computed_at
from channel_rankings
where channel_point = $1
limit 1
`, strings.TrimSpace(channelPoint))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanChannelRankingItems(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("channel ranking not found")
	}
	return &items[0], nil
}

func (s *ChannelRankingService) GetDetail(ctx context.Context, channelPoint string) (*ChannelRankingDetail, error) {
	item, err := s.Get(ctx, channelPoint)
	if err != nil {
		return nil, err
	}
	history, err := s.getHistory(ctx, item.ChannelPoint, 168)
	if err != nil {
		return nil, err
	}
	peerChannels, err := s.getPeerChannels(ctx, *item)
	if err != nil {
		return nil, err
	}
	topForwardInSources, err := s.getTopForwardInSources(ctx, *item, 5)
	if err != nil {
		return nil, err
	}
	topForwardOutSinks, err := s.getTopForwardOutSinks(ctx, *item, 5)
	if err != nil {
		return nil, err
	}
	return &ChannelRankingDetail{
		Item:                *item,
		History:             history,
		PeerChannels:        peerChannels,
		TopForwardInSources: topForwardInSources,
		TopForwardOutSinks:  topForwardOutSinks,
		Feedback:            buildChannelRankingFeedback(*item, history),
	}, nil
}

func (s *ChannelRankingService) Status(ctx context.Context) (ChannelRankingStatus, error) {
	if s == nil || s.db == nil {
		return ChannelRankingStatus{}, ErrChannelRankingDBUnavailable
	}
	persistedLastSyncAt, err := s.persistedLastSyncAt(ctx)
	if err != nil {
		return ChannelRankingStatus{}, err
	}
	rows, err := s.db.Query(ctx, `
select state, count(*)
from channel_rankings
group by state
`)
	if err != nil {
		return ChannelRankingStatus{}, err
	}
	defer rows.Close()
	status := ChannelRankingStatus{
		Available:   true,
		LastSyncAt:  latestChannelRankingSyncAt(persistedLastSyncAt, s.lastSyncAtPtr()),
		StateCounts: map[string]int{},
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return ChannelRankingStatus{}, err
		}
		status.StateCounts[state] = count
	}
	return status, rows.Err()
}

func (s *ChannelRankingService) persistedLastSyncAt(ctx context.Context) (*time.Time, error) {
	if s == nil || s.db == nil {
		return nil, ErrChannelRankingDBUnavailable
	}
	var computedAt sql.NullTime
	if err := s.db.QueryRow(ctx, `select max(computed_at) from channel_rankings`).Scan(&computedAt); err != nil {
		return nil, err
	}
	if !computedAt.Valid {
		return nil, nil
	}
	value := computedAt.Time.UTC()
	return &value, nil
}

func latestChannelRankingSyncAt(persisted *time.Time, inMemory *time.Time) *time.Time {
	switch {
	case persisted == nil:
		return cloneTimePtr(inMemory)
	case inMemory == nil:
		return cloneTimePtr(persisted)
	case persisted.After(*inMemory):
		return cloneTimePtr(persisted)
	default:
		return cloneTimePtr(inMemory)
	}
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func (s *ChannelRankingService) getHistory(ctx context.Context, channelPoint string, limit int) ([]ChannelRankingHistoryPoint, error) {
	if limit <= 0 {
		limit = 24
	}
	rows, err := s.db.Query(ctx, `
select computed_bucket, score, score_7d, score_30d, trend_direction, trend_delta, state, close_candidate, profit_fee_7d_sat, profit_fee_30d_sat
from channel_ranking_history
where channel_point = $1
order by computed_bucket desc
limit $2
`, strings.TrimSpace(channelPoint), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ChannelRankingHistoryPoint, 0)
	for rows.Next() {
		var item ChannelRankingHistoryPoint
		var trendDirection sql.NullString
		if err := rows.Scan(
			&item.ComputedAt,
			&item.Score,
			&item.Score7d,
			&item.Score30d,
			&trendDirection,
			&item.TrendDelta,
			&item.State,
			&item.CloseCandidate,
			&item.ProfitFee7dSat,
			&item.ProfitFee30dSat,
		); err != nil {
			return nil, err
		}
		if trendDirection.Valid {
			item.TrendDirection = strings.TrimSpace(trendDirection.String)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ChannelRankingService) getPeerChannels(ctx context.Context, item ChannelRankingItem) ([]ChannelRankingPeerComparison, error) {
	peerPubkey := strings.TrimSpace(item.PeerPubkey)
	if peerPubkey == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
select channel_point, channel_id, peer_alias, score, score_30d, trend_direction, trend_delta, state, capacity_sat, profit_fee_7d_sat, profit_fee_30d_sat
from channel_rankings
where peer_pubkey = $1
order by score desc, profit_fee_7d_sat desc, capacity_sat desc, channel_point asc
`, peerPubkey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ChannelRankingPeerComparison, 0)
	for rows.Next() {
		var comparison ChannelRankingPeerComparison
		var peerAlias sql.NullString
		var trendDirection sql.NullString
		if err := rows.Scan(
			&comparison.ChannelPoint,
			&comparison.ChannelID,
			&peerAlias,
			&comparison.Score,
			&comparison.Score30d,
			&trendDirection,
			&comparison.TrendDelta,
			&comparison.State,
			&comparison.CapacitySat,
			&comparison.ProfitFee7dSat,
			&comparison.ProfitFee30dSat,
		); err != nil {
			return nil, err
		}
		if peerAlias.Valid {
			comparison.PeerAlias = strings.TrimSpace(peerAlias.String)
		}
		if trendDirection.Valid {
			comparison.TrendDirection = strings.TrimSpace(trendDirection.String)
		}
		items = append(items, comparison)
	}
	return items, rows.Err()
}

func (s *ChannelRankingService) getTopForwardInSources(ctx context.Context, item ChannelRankingItem, limit int) ([]ChannelRankingFlowCounterparty, error) {
	return s.getTopForwardCounterparties(ctx, item.ChannelID, true, limit)
}

func (s *ChannelRankingService) getTopForwardOutSinks(ctx context.Context, item ChannelRankingItem, limit int) ([]ChannelRankingFlowCounterparty, error) {
	return s.getTopForwardCounterparties(ctx, item.ChannelID, false, limit)
}

func (s *ChannelRankingService) getTopForwardCounterparties(ctx context.Context, channelID int64, inbound bool, limit int) ([]ChannelRankingFlowCounterparty, error) {
	if s == nil || s.db == nil || channelID <= 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	selectCounterpartyID := "n.chan_id_out"
	selectCounterpartyPoint := "coalesce(n.channel_point_out, '')"
	filterColumn := "n.chan_id_in"
	if !inbound {
		selectCounterpartyID = "n.chan_id_in"
		selectCounterpartyPoint = "coalesce(n.channel_point_in, '')"
		filterColumn = "n.chan_id_out"
	}

	query := `
select
  counterparty_channel_point,
  counterparty_channel_id,
  coalesce(nullif(cr.peer_alias, ''), counterparty_channel_point, counterparty_channel_id::text) as peer_alias,
  forward_count_7d,
  forward_amount_sat_7d
from (
  select
    ` + selectCounterpartyPoint + ` as counterparty_channel_point,
    ` + selectCounterpartyID + ` as counterparty_channel_id,
    count(*)::int as forward_count_7d,
    coalesce(sum(case when n.amount_out_msat > 0 then n.amount_out_msat / 1000 else n.amount_sat end), 0) as forward_amount_sat_7d
  from notifications n
  where n.type='forward'
    and n.status='SETTLED'
    and n.occurred_at >= now() - interval '7 day'
    and ` + filterColumn + ` = $1
    and ` + selectCounterpartyID + ` is not null
  group by 1, 2
) agg
left join channel_rankings cr on cr.channel_id = agg.counterparty_channel_id
order by forward_amount_sat_7d desc, forward_count_7d desc, peer_alias asc
limit $2
`

	rows, err := s.db.Query(ctx, query, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]ChannelRankingFlowCounterparty, 0, limit)
	for rows.Next() {
		var entry ChannelRankingFlowCounterparty
		var point sql.NullString
		var alias sql.NullString
		if err := rows.Scan(
			&point,
			&entry.ChannelID,
			&alias,
			&entry.ForwardCount7d,
			&entry.ForwardAmountSat7d,
		); err != nil {
			return nil, err
		}
		if point.Valid {
			entry.ChannelPoint = strings.TrimSpace(point.String)
		}
		if alias.Valid {
			entry.PeerAlias = strings.TrimSpace(alias.String)
		}
		items = append(items, entry)
	}
	return items, rows.Err()
}

func buildChannelRankingFeedback(item ChannelRankingItem, history []ChannelRankingHistoryPoint) *ChannelRankingFeedback {
	if len(history) == 0 {
		return nil
	}
	oldest := history[len(history)-1]
	scoreDelta := item.Score - oldest.Score
	netDeltaSat := item.ProfitFee7dSat - oldest.ProfitFee7dSat
	direction := "stable"
	switch {
	case scoreDelta >= 6 || netDeltaSat >= 100:
		direction = "improving"
	case scoreDelta <= -6 || netDeltaSat <= -100:
		direction = "worsening"
	}
	windowHours := int(item.ComputedAt.Sub(oldest.ComputedAt).Hours())
	if windowHours < 1 {
		windowHours = 1
	}
	return &ChannelRankingFeedback{
		Direction:   direction,
		ScoreDelta:  scoreDelta,
		NetDeltaSat: netDeltaSat,
		BaselineAt:  oldest.ComputedAt,
		WindowHours: windowHours,
	}
}

type channelRankingRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanChannelRankingItems(rows channelRankingRows) ([]ChannelRankingItem, error) {
	items := make([]ChannelRankingItem, 0)
	for rows.Next() {
		var item ChannelRankingItem
		var peerPubkey sql.NullString
		var peerAlias sql.NullString
		var classLabel sql.NullString
		var trendDirection sql.NullString
		var reasonsRaw []byte
		var recommendationsRaw []byte
		if err := rows.Scan(
			&item.ChannelPoint, &item.ChannelID, &peerPubkey, &peerAlias, &item.Active, &item.Private, &item.CapacitySat,
			&item.LocalBalanceSat, &item.RemoteBalanceSat, &item.LocalBalancePct, &item.RemoteBalancePct,
			&item.InactiveDurationSec, &item.PendingHtlcCount, &classLabel,
			&item.ForwardInCount7d, &item.ForwardInAmountSat7d, &item.ForwardOutCount7d, &item.ForwardOutAmountSat7d,
			&item.ForwardFee7dSat, &item.ForwardAmt7dSat, &item.AssistedForwardFee7dSat, &item.AssistedForwardAmt7dSat, &item.OutPpm7d,
			&item.ForwardFee30dSat, &item.ForwardAmt30dSat, &item.AssistedForwardFee30dSat, &item.AssistedForwardAmt30dSat, &item.OutPpm30d,
			&item.RebalFee7dSat, &item.RebalAmt7dSat, &item.RebalPpm7d,
			&item.RebalFee30dSat, &item.RebalAmt30dSat, &item.RebalPpm30d,
			&item.ProfitFee7dSat, &item.ProfitFee30dSat, &item.PeerStabilityScore30d, &item.PeerSampleCount30d,
			&item.HTLCFailures30d, &item.HTLCPolicyFails30d, &item.HTLCLiquidityFails30d, &item.HTLCForwardFails30d, &item.RebalanceDependenceScore,
			&item.Score, &item.Score7d, &item.Score30d, &trendDirection, &item.TrendDelta, &item.State, &reasonsRaw, &recommendationsRaw, &item.ComputedAt,
		); err != nil {
			return nil, err
		}
		if peerPubkey.Valid {
			item.PeerPubkey = strings.TrimSpace(peerPubkey.String)
		}
		if peerAlias.Valid {
			item.PeerAlias = strings.TrimSpace(peerAlias.String)
		}
		if classLabel.Valid {
			item.ClassLabel = strings.TrimSpace(classLabel.String)
		}
		if trendDirection.Valid {
			item.TrendDirection = strings.TrimSpace(trendDirection.String)
		}
		if len(reasonsRaw) > 0 {
			_ = json.Unmarshal(reasonsRaw, &item.Reasons)
		}
		if len(recommendationsRaw) > 0 {
			_ = json.Unmarshal(recommendationsRaw, &item.Recommendations)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func clampFloat64(value float64, min float64, max float64) float64 {
	if math.IsNaN(value) {
		return min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func rankingMaxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func rankingMaxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func normalizeChannelRankingState(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "expand", "maintain", "monitor", "close":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}
