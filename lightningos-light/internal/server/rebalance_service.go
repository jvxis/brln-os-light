package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	rebalanceConfigID                 = 1
	rebalanceDefaultTargetOutboundPct = 50.0
	rebalanceForwardPageSize          = 50000
)

const (
	pairFailTTL    = 30 * time.Second
	pairSuccessTTL = 24 * time.Hour
)

const (
	queueLingerSeconds = 20
)

const (
	scanSkipDetailLimit = 50
)

const (
	autoTargetCooldownMin = 10 * time.Minute
)

const (
	paybackModePayback  = 1 << 0
	paybackModeTime     = 1 << 1
	paybackModeCritical = 1 << 2
)

var errManualRestartCooldown = errors.New("manual restart cooldown active")
var errManualBudgetExhausted = errors.New("manual budget exhausted")
var errManualBudgetInsufficient = errors.New("manual budget insufficient")

const (
	rebalanceBudgetModeRevenue24hPct = "revenue_24h_pct"
	rebalanceBudgetModeHybridRevenue = "hybrid_revenue"
)

const (
	rebalanceManualReserveModeFixedSat = "fixed_sat"
	rebalanceManualReserveModePct      = "pct"
)

type RebalanceConfig struct {
	AutoEnabled               bool    `json:"auto_enabled"`
	ScanIntervalSec           int     `json:"scan_interval_sec"`
	DeadbandPct               float64 `json:"deadband_pct"`
	SourceMinLocalPct         float64 `json:"source_min_local_pct"`
	EconRatio                 float64 `json:"econ_ratio"`
	EconRatioMaxPpm           int64   `json:"econ_ratio_max_ppm"`
	FeeLimitPpm               int64   `json:"fee_limit_ppm"`
	LostProfit                bool    `json:"lost_profit"`
	FailTolerancePpm          int64   `json:"fail_tolerance_ppm"`
	ROIMin                    float64 `json:"roi_min"`
	DailyBudgetPct            float64 `json:"daily_budget_pct"`
	BudgetMode                string  `json:"budget_mode"`
	ManualReserveEnabled      bool    `json:"manual_reserve_enabled"`
	ManualReserveMode         string  `json:"manual_reserve_mode"`
	ManualReserveValue        float64 `json:"manual_reserve_value"`
	MaxConcurrent             int     `json:"max_concurrent"`
	MinAmountSat              int64   `json:"min_amount_sat"`
	MaxAmountSat              int64   `json:"max_amount_sat"`
	MinSplitEnabled           bool    `json:"min_split_enabled"`
	MinProbeSat               int64   `json:"min_probe_sat"`
	MinExecuteSat             int64   `json:"min_execute_sat"`
	MppEnabled                bool    `json:"mpp_enabled"`
	MppMaxShards              int     `json:"mpp_max_shards"`
	MppParallelism            int     `json:"mpp_parallelism"`
	MppMinShardSat            int64   `json:"mpp_min_shard_sat"`
	MppRoundTimeoutSec        int     `json:"mpp_round_timeout_sec"`
	MppAutoOnly               bool    `json:"mpp_auto_only"`
	FeeLadderSteps            int     `json:"fee_ladder_steps"`
	AmountProbeSteps          int     `json:"amount_probe_steps"`
	AmountProbeAdaptive       bool    `json:"amount_probe_adaptive"`
	AttemptTimeoutSec         int     `json:"attempt_timeout_sec"`
	RebalanceTimeoutSec       int     `json:"rebalance_timeout_sec"`
	ManualRestartWatch        bool    `json:"manual_restart_watch"`
	MissionControlHalfLifeSec int64   `json:"mc_half_life_sec"`
	PaybackModeFlags          int     `json:"payback_mode_flags"`
	UnlockDays                int     `json:"unlock_days"`
	CriticalReleasePct        float64 `json:"critical_release_pct"`
	CriticalMinSources        int     `json:"critical_min_sources"`
	CriticalMinAvailableSats  int64   `json:"critical_min_available_sats"`
	CriticalCycles            int     `json:"critical_cycles"`
}

type RebalanceOverview struct {
	AutoEnabled                   bool                  `json:"auto_enabled"`
	LastScanAt                    string                `json:"last_scan_at,omitempty"`
	LastScanStatus                string                `json:"last_scan_status,omitempty"`
	LastScanDetail                string                `json:"last_scan_detail,omitempty"`
	LastScanCandidates            int                   `json:"last_scan_candidates"`
	LastScanRemainingBudgetSat    int64                 `json:"last_scan_remaining_budget_sat"`
	LastScanReasons               map[string]int        `json:"last_scan_reasons,omitempty"`
	LastScanTopScoreSat           int64                 `json:"last_scan_top_score_sat"`
	LastScanProfitSkipped         int                   `json:"last_scan_profit_skipped"`
	LastScanQueued                int                   `json:"last_scan_queued"`
	LastScanSkipped               []RebalanceSkipDetail `json:"last_scan_skipped,omitempty"`
	DailyBudgetSat                int64                 `json:"daily_budget_sat"`
	DailyBudgetBaseSat            int64                 `json:"daily_budget_base_sat"`
	DailyBudgetShortTermSat       int64                 `json:"daily_budget_short_term_sat"`
	DailySpentSat                 int64                 `json:"daily_spent_sat"`
	DailySpentAutoSat             int64                 `json:"daily_spent_auto_sat"`
	DailySpentManualSat           int64                 `json:"daily_spent_manual_sat"`
	RemainingTotalSat             int64                 `json:"remaining_total_sat"`
	RemainingForAutoSat           int64                 `json:"remaining_for_auto_sat"`
	ManualReserveEnabled          bool                  `json:"manual_reserve_enabled"`
	ManualReserveMode             string                `json:"manual_reserve_mode,omitempty"`
	ManualReserveValue            float64               `json:"manual_reserve_value,omitempty"`
	ManualReserveSat              int64                 `json:"manual_reserve_sat"`
	ManualReserveRemainingSat     int64                 `json:"manual_reserve_remaining_sat"`
	LiveCostSat                   int64                 `json:"live_cost_sat"`
	Effectiveness7d               float64               `json:"effectiveness_7d"`
	EffectivenessExecution7d      float64               `json:"effectiveness_execution_7d"`
	JobsWithoutAttempt7d          int64                 `json:"jobs_without_attempt_7d"`
	JobsWithoutAttemptRate7d      float64               `json:"jobs_without_attempt_rate_7d"`
	ROI7d                         float64               `json:"roi_7d"`
	SuccessAttempts24h            int64                 `json:"success_attempts_24h"`
	SuccessAmount24hSat           int64                 `json:"success_amount_24h_sat"`
	SuccessAvgAmount24hSat        int64                 `json:"success_avg_amount_24h_sat"`
	SuccessBelowMinAttempts24h    int64                 `json:"success_below_min_attempts_24h"`
	SuccessBelowMinAmount24hSat   int64                 `json:"success_below_min_amount_24h_sat"`
	SuccessBelowMinRate24h        float64               `json:"success_below_min_rate_24h"`
	PaybackRevenueSat             int64                 `json:"payback_revenue_sat"`
	PaybackRevenueRebalancedSat   int64                 `json:"payback_revenue_rebalanced_sat"`
	PaybackCostSat                int64                 `json:"payback_cost_sat"`
	PaybackProgress               float64               `json:"payback_progress"`
	PaybackProgressRebalanced     float64               `json:"payback_progress_rebalanced"`
	EligibleSources               int                   `json:"eligible_sources"`
	TargetsNeeding                int                   `json:"targets_needing"`
	MppShadowJobs24h              int64                 `json:"mpp_shadow_jobs_24h"`
	MppShadowPlanReady24h         int64                 `json:"mpp_shadow_plan_ready_24h"`
	MppShadowPlannedSat24h        int64                 `json:"mpp_shadow_planned_sat_24h"`
	MppShadowActualSentSat24h     int64                 `json:"mpp_shadow_actual_sent_sat_24h"`
	MppShadowInProgressJobs24h    int64                 `json:"mpp_shadow_in_progress_jobs_24h"`
	MppShadowSuccessJobs24h       int64                 `json:"mpp_shadow_success_jobs_24h"`
	MppShadowFailedJobs24h        int64                 `json:"mpp_shadow_failed_jobs_24h"`
	MppShadowPartialJobs24h       int64                 `json:"mpp_shadow_partial_jobs_24h"`
	MppShadowFloorBlocked24h      int64                 `json:"mpp_shadow_floor_blocked_sources_24h"`
	MppShadowAvgPlannedShards24h  float64               `json:"mpp_shadow_avg_planned_shards_24h"`
	MppShadowAvgActualAttempts24h float64               `json:"mpp_shadow_avg_actual_attempts_24h"`
}

type RebalanceSkipDetail struct {
	ChannelID         uint64  `json:"channel_id"`
	ChannelPoint      string  `json:"channel_point"`
	PeerAlias         string  `json:"peer_alias"`
	TargetOutboundPct float64 `json:"target_outbound_pct"`
	TargetAmountSat   int64   `json:"target_amount_sat"`
	ExpectedGainSat   int64   `json:"expected_gain_sat"`
	EstimatedCostSat  int64   `json:"estimated_cost_sat"`
	ExpectedROI       float64 `json:"expected_roi"`
	ExpectedROIValid  bool    `json:"expected_roi_valid"`
	Reason            string  `json:"reason"`
}

type RebalanceChannel struct {
	ChannelID             uint64   `json:"channel_id"`
	ChannelPoint          string   `json:"channel_point"`
	PeerAlias             string   `json:"peer_alias"`
	RemotePubkey          string   `json:"remote_pubkey"`
	Active                bool     `json:"active"`
	Private               bool     `json:"private"`
	CapacitySat           int64    `json:"capacity_sat"`
	LocalBalanceSat       int64    `json:"local_balance_sat"`
	RemoteBalanceSat      int64    `json:"remote_balance_sat"`
	LocalPct              float64  `json:"local_pct"`
	RemotePct             float64  `json:"remote_pct"`
	OutgoingFeePpm        int64    `json:"outgoing_fee_ppm"`
	OutgoingBaseMsat      int64    `json:"outgoing_base_msat"`
	PeerFeeRatePpm        int64    `json:"peer_fee_rate_ppm"`
	PeerBaseMsat          int64    `json:"peer_base_msat"`
	SpreadPpm             int64    `json:"spread_ppm"`
	TargetOutboundPct     float64  `json:"target_outbound_pct"`
	TargetAmountSat       int64    `json:"target_amount_sat"`
	AutoEnabled           bool     `json:"auto_enabled"`
	ManualRestartEnabled  bool     `json:"manual_restart_enabled"`
	UseDefaultEconRatio   bool     `json:"use_default_econ_ratio"`
	EconRatioOverride     *float64 `json:"econ_ratio_override,omitempty"`
	EligibleAsTarget      bool     `json:"eligible_as_target"`
	EligibleAsSource      bool     `json:"eligible_as_source"`
	ProtectedLiquiditySat int64    `json:"protected_liquidity_sat"`
	PaybackProgress       float64  `json:"payback_progress"`
	MaxSourceSat          int64    `json:"max_source_sat"`
	Revenue7dSat          int64    `json:"revenue_7d_sat"`
	RebalanceCost7dSat    int64    `json:"rebalance_cost_7d_sat"`
	RebalanceCost7dPpm    int64    `json:"rebalance_cost_7d_ppm"`
	RebalanceAmount7dSat  int64    `json:"rebalance_amount_7d_sat"`
	ROIEstimate           float64  `json:"roi_estimate"`
	ROIEstimateValid      bool     `json:"roi_estimate_valid"`
	ExcludedAsSource      bool     `json:"excluded_as_source"`
}

type RebalanceJob struct {
	ID                 int64   `json:"id"`
	CreatedAt          string  `json:"created_at"`
	CompletedAt        string  `json:"completed_at,omitempty"`
	Source             string  `json:"source"`
	Status             string  `json:"status"`
	Reason             string  `json:"reason,omitempty"`
	TargetChannelID    uint64  `json:"target_channel_id"`
	TargetChannelPoint string  `json:"target_channel_point"`
	TargetOutboundPct  float64 `json:"target_outbound_pct"`
	TargetAmountSat    int64   `json:"target_amount_sat"`
	TargetPeerAlias    string  `json:"target_peer_alias,omitempty"`
}

type RebalanceAttempt struct {
	ID              int64  `json:"id"`
	JobID           int64  `json:"job_id"`
	AttemptIndex    int    `json:"attempt_index"`
	SourceChannelID uint64 `json:"source_channel_id"`
	SourcePeerAlias string `json:"source_peer_alias,omitempty"`
	AmountSat       int64  `json:"amount_sat"`
	FeeLimitPpm     int64  `json:"fee_limit_ppm"`
	FeePaidSat      int64  `json:"fee_paid_sat"`
	Status          string `json:"status"`
	PaymentHash     string `json:"payment_hash,omitempty"`
	FailReason      string `json:"fail_reason,omitempty"`
	StartedAt       string `json:"started_at,omitempty"`
	FinishedAt      string `json:"finished_at,omitempty"`
}

type RebalanceEvent struct {
	Type    string `json:"type"`
	JobID   int64  `json:"job_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

type channelSetting struct {
	ChannelID            uint64
	ChannelPoint         string
	TargetOutboundPct    float64
	AutoEnabled          bool
	ManualRestartEnabled bool
	UseDefaultEconRatio  bool
	EconRatioOverride    float64
	EconRatioOverrideSet bool
}

type channelLedger struct {
	ChannelID        uint64
	PaidLiquiditySat int64
	PaidCostSat      int64
	PaidRevenueSat   int64
	LastRebalanceAt  time.Time
	LastForwardAt    time.Time
	LastUnlockAt     time.Time
}

type pairStat struct {
	SourceChannelID  uint64
	TargetChannelID  uint64
	LastSuccessAt    time.Time
	LastFailAt       time.Time
	SuccessAmountSat int64
	SuccessFeePpm    int64
}

type rebalanceCost7dStat struct {
	FeeSat    int64
	AmountSat int64
	FeePpm    int64
}

type rebalanceAttemptTelemetry24h struct {
	SuccessAttempts          int64
	SuccessAmountSat         int64
	SuccessAvgAmountSat      int64
	SuccessBelowMinAttempts  int64
	SuccessBelowMinAmountSat int64
	SuccessBelowMinRate      float64
}

type mppShadowShard struct {
	SourceChannelID uint64
	AmountSat       int64
}

type mppShadowPlan struct {
	EligibleSources     int
	PlannedSources      int
	PlannedShards       int
	PlannedTotalSat     int64
	PlannedRemainderSat int64
	Shards              []mppShadowShard
}

type mppShadowTelemetry24h struct {
	Jobs              int64
	PlanReadyJobs     int64
	PlannedSat        int64
	ActualSentSat     int64
	InProgressJobs    int64
	SuccessJobs       int64
	FailedJobs        int64
	PartialJobs       int64
	FloorBlocked      int64
	AvgPlannedShards  float64
	AvgActualAttempts float64
}

type paybackTotals7d struct {
	RevenueAllSat        int64
	RevenueRebalancedSat int64
	CostSat              int64
}

type manualRestartInfo struct {
	TargetChannelID uint64
}

type manualRestartHandle struct {
	cancel context.CancelFunc
}

type RebalanceService struct {
	db     *pgxpool.Pool
	lnd    *lndclient.Client
	logger *log.Logger

	mu                         sync.Mutex
	started                    bool
	stop                       chan struct{}
	wake                       chan struct{}
	subs                       map[chan RebalanceEvent]struct{}
	cfg                        RebalanceConfig
	cfgLoaded                  bool
	mcHalfLifeApplied          int64
	lastScan                   time.Time
	lastScanStatus             string
	lastScanDetail             string
	lastScanCandidates         int
	lastScanRemainingBudgetSat int64
	lastScanReasons            map[string]int
	lastScanTopScoreSat        int64
	lastScanProfitSkipped      int
	lastScanQueued             int
	lastScanSkipped            []RebalanceSkipDetail
	criticalMissCount          int
	sem                        chan struct{}
	channelLocks               map[uint64]bool
	jobCancel                  map[int64]context.CancelFunc
	manualRestart              map[int64]manualRestartInfo
	manualRestartCancel        map[uint64]*manualRestartHandle
	lastAutoByTarget           map[uint64]time.Time
}

func NewRebalanceService(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger) *RebalanceService {
	return &RebalanceService{
		db:                  db,
		lnd:                 lnd,
		logger:              logger,
		subs:                map[chan RebalanceEvent]struct{}{},
		channelLocks:        map[uint64]bool{},
		jobCancel:           map[int64]context.CancelFunc{},
		manualRestart:       map[int64]manualRestartInfo{},
		manualRestartCancel: map[uint64]*manualRestartHandle{},
		lastAutoByTarget:    map[uint64]time.Time{},
	}
}

func defaultRebalanceConfig() RebalanceConfig {
	return RebalanceConfig{
		AutoEnabled:               false,
		ScanIntervalSec:           600,
		DeadbandPct:               10,
		SourceMinLocalPct:         50,
		EconRatio:                 0.6,
		EconRatioMaxPpm:           0,
		FeeLimitPpm:               0,
		LostProfit:                false,
		FailTolerancePpm:          1000,
		ROIMin:                    1.1,
		DailyBudgetPct:            50,
		BudgetMode:                rebalanceBudgetModeRevenue24hPct,
		ManualReserveEnabled:      false,
		ManualReserveMode:         rebalanceManualReserveModeFixedSat,
		ManualReserveValue:        0,
		MaxConcurrent:             2,
		MinAmountSat:              20000,
		MaxAmountSat:              0,
		MinSplitEnabled:           false,
		MinProbeSat:               0,
		MinExecuteSat:             0,
		MppEnabled:                false,
		MppMaxShards:              8,
		MppParallelism:            6,
		MppMinShardSat:            1000,
		MppRoundTimeoutSec:        30,
		MppAutoOnly:               false,
		FeeLadderSteps:            4,
		AmountProbeSteps:          4,
		AmountProbeAdaptive:       true,
		AttemptTimeoutSec:         20,
		RebalanceTimeoutSec:       600,
		ManualRestartWatch:        false,
		MissionControlHalfLifeSec: 0,
		PaybackModeFlags:          paybackModePayback | paybackModeTime | paybackModeCritical,
		UnlockDays:                14,
		CriticalReleasePct:        20,
		CriticalMinSources:        2,
		CriticalMinAvailableSats:  0,
		CriticalCycles:            3,
	}
}

func (s *RebalanceService) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.stop = make(chan struct{})
	s.wake = make(chan struct{}, 1)
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	if err := s.ensureSchema(ctx); err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance disabled: failed to init schema: %v", err)
		}
		cancel()
		return
	}
	cancel()

	if _, err := s.loadConfig(context.Background()); err != nil && s.logger != nil {
		s.logger.Printf("rebalance config load failed: %v", err)
	}
	if s.lnd != nil {
		mcCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cfg, _ := s.loadConfig(mcCtx)
		s.applyMissionControlHalfLife(mcCtx, cfg)
		cancel()
	}

	s.resetSemaphore()

	go s.runAutoLoop()
	go s.runManualRestartWatchLoop()
}

func (s *RebalanceService) ResolveChannel(ctx context.Context, channelID uint64, channelPoint string) (uint64, string, error) {
	if s.lnd == nil {
		return 0, "", errors.New("lnd unavailable")
	}
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return 0, "", err
	}
	if channelID != 0 {
		for _, ch := range channels {
			if ch.ChannelID == channelID {
				return ch.ChannelID, ch.ChannelPoint, nil
			}
		}
	}
	trimmed := strings.TrimSpace(channelPoint)
	if trimmed != "" {
		for _, ch := range channels {
			if strings.EqualFold(ch.ChannelPoint, trimmed) {
				return ch.ChannelID, ch.ChannelPoint, nil
			}
		}
	}
	return 0, "", errors.New("channel not found")
}

func (s *RebalanceService) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	close(s.stop)
	s.stop = nil
	s.started = false
	s.mu.Unlock()
}

func (s *RebalanceService) Subscribe() chan RebalanceEvent {
	ch := make(chan RebalanceEvent, 50)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *RebalanceService) Unsubscribe(ch chan RebalanceEvent) {
	s.mu.Lock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
	s.mu.Unlock()
}

func (s *RebalanceService) broadcast(evt RebalanceEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- evt:
		default:
		}
	}
}

func (s *RebalanceService) GetConfig(ctx context.Context) (RebalanceConfig, error) {
	return s.loadConfig(ctx)
}

func (s *RebalanceService) UpdateConfig(ctx context.Context, updated RebalanceConfig) (RebalanceConfig, error) {
	prev, _ := s.loadConfig(ctx)
	updated = normalizeRebalanceConfig(updated)
	if err := s.upsertConfig(ctx, updated); err != nil {
		return RebalanceConfig{}, err
	}
	s.mu.Lock()
	s.cfg = updated
	s.cfgLoaded = true
	s.mu.Unlock()
	if s.lnd != nil {
		mcCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.applyMissionControlHalfLife(mcCtx, updated)
		cancel()
	}
	if prev.ManualRestartWatch && !updated.ManualRestartWatch {
		s.cancelAllManualRestarts()
	}
	s.resetSemaphore()
	s.triggerScan()
	return updated, nil
}

func (s *RebalanceService) applyMissionControlHalfLife(ctx context.Context, cfg RebalanceConfig) {
	if s.lnd == nil {
		return
	}
	if cfg.MissionControlHalfLifeSec < 0 {
		cfg.MissionControlHalfLifeSec = 0
	}
	s.mu.Lock()
	lastApplied := s.mcHalfLifeApplied
	s.mu.Unlock()
	if lastApplied == cfg.MissionControlHalfLifeSec {
		return
	}
	if err := s.lnd.UpdateMissionControlHalfLife(ctx, cfg.MissionControlHalfLifeSec); err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance mission control update failed: %v", err)
		}
		return
	}
	s.mu.Lock()
	s.mcHalfLifeApplied = cfg.MissionControlHalfLifeSec
	s.mu.Unlock()
}

func (s *RebalanceService) resetSemaphore() {
	s.mu.Lock()
	cfg := s.cfg
	if !s.cfgLoaded {
		cfg = defaultRebalanceConfig()
	}
	cap := cfg.MaxConcurrent
	if cap <= 0 {
		cap = 1
	}
	s.sem = make(chan struct{}, cap)
	s.mu.Unlock()
}

func (s *RebalanceService) triggerScan() {
	s.mu.Lock()
	wake := s.wake
	s.mu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (s *RebalanceService) runAutoLoop() {
	for {
		cfg, _ := s.loadConfig(context.Background())
		interval := rebalanceScanInterval(cfg)
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
			s.runAutoScan()
		case <-s.wake:
			if !timer.Stop() {
				<-timer.C
			}
			s.runAutoScan()
		case <-s.stop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func rebalanceScanInterval(cfg RebalanceConfig) time.Duration {
	interval := time.Duration(cfg.ScanIntervalSec) * time.Second
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	return interval
}

func manualRestartInterval(cfg RebalanceConfig) time.Duration {
	return rebalanceScanInterval(cfg)
}

func shouldEnforceManualRestartCooldown(source string, reason string) bool {
	return source == "manual" && reason == "auto-restart"
}

func normalizeChannelSetting(setting channelSetting) channelSetting {
	if setting.TargetOutboundPct <= 0 || setting.TargetOutboundPct > 100 {
		setting.TargetOutboundPct = rebalanceDefaultTargetOutboundPct
	}
	if setting.AutoEnabled && setting.ManualRestartEnabled {
		setting.ManualRestartEnabled = false
	}
	if !setting.UseDefaultEconRatio && (!setting.EconRatioOverrideSet || setting.EconRatioOverride < 0.01 || setting.EconRatioOverride > 0.99) {
		setting.UseDefaultEconRatio = true
		setting.EconRatioOverride = 0
		setting.EconRatioOverrideSet = false
	}
	if !setting.UseDefaultEconRatio && !setting.EconRatioOverrideSet {
		setting.UseDefaultEconRatio = true
	}
	if setting.UseDefaultEconRatio {
		setting.EconRatioOverride = 0
		setting.EconRatioOverrideSet = false
	}
	return setting
}

func normalizeRebalanceConfig(cfg RebalanceConfig) RebalanceConfig {
	def := defaultRebalanceConfig()
	if cfg.MinAmountSat < 0 {
		cfg.MinAmountSat = 0
	}
	if cfg.MaxAmountSat < 0 {
		cfg.MaxAmountSat = 0
	}
	if cfg.MinProbeSat < 0 {
		cfg.MinProbeSat = 0
	}
	if cfg.MinExecuteSat < 0 {
		cfg.MinExecuteSat = 0
	}
	if cfg.MppMaxShards <= 0 {
		cfg.MppMaxShards = def.MppMaxShards
	}
	if cfg.MppMaxShards > 20 {
		cfg.MppMaxShards = 20
	}
	if cfg.MppParallelism <= 0 {
		cfg.MppParallelism = def.MppParallelism
	}
	if cfg.MppParallelism > cfg.MppMaxShards {
		cfg.MppParallelism = cfg.MppMaxShards
	}
	if cfg.MppMinShardSat <= 0 {
		cfg.MppMinShardSat = def.MppMinShardSat
	}
	if cfg.MppRoundTimeoutSec <= 0 {
		cfg.MppRoundTimeoutSec = def.MppRoundTimeoutSec
	}
	cfg.BudgetMode = normalizeRebalanceBudgetMode(cfg.BudgetMode)
	cfg.ManualReserveMode = normalizeRebalanceManualReserveMode(cfg.ManualReserveMode)
	if cfg.ManualReserveValue < 0 {
		cfg.ManualReserveValue = 0
	}
	if cfg.ManualReserveMode == rebalanceManualReserveModePct && cfg.ManualReserveValue > 100 {
		cfg.ManualReserveValue = 100
	}
	return cfg
}

func normalizeRebalanceBudgetMode(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case rebalanceBudgetModeHybridRevenue:
		return rebalanceBudgetModeHybridRevenue
	default:
		return rebalanceBudgetModeRevenue24hPct
	}
}

func normalizeRebalanceManualReserveMode(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case rebalanceManualReserveModePct:
		return rebalanceManualReserveModePct
	default:
		return rebalanceManualReserveModeFixedSat
	}
}

func effectiveMinExecuteSat(cfg RebalanceConfig) int64 {
	if cfg.MinSplitEnabled && cfg.MinExecuteSat > 0 {
		return cfg.MinExecuteSat
	}
	if cfg.MinAmountSat > 0 {
		return cfg.MinAmountSat
	}
	return 0
}

func effectiveMinProbeSat(cfg RebalanceConfig) int64 {
	if cfg.MinSplitEnabled && cfg.MinProbeSat > 0 {
		return cfg.MinProbeSat
	}
	if cfg.MinAmountSat > 0 {
		return cfg.MinAmountSat
	}
	return 0
}

func effectiveStartAmountSat(cfg RebalanceConfig) int64 {
	// Keep legacy anchoring behavior: attempts start from min_amount when set.
	if cfg.MinAmountSat > 0 {
		return cfg.MinAmountSat
	}
	if v := effectiveMinExecuteSat(cfg); v > 0 {
		return v
	}
	if v := effectiveMinProbeSat(cfg); v > 0 {
		return v
	}
	return 0
}

func shouldRunMppExecute(cfg RebalanceConfig, jobSource string) bool {
	if !cfg.MppEnabled {
		return false
	}
	if cfg.MppAutoOnly && strings.TrimSpace(strings.ToLower(jobSource)) != "auto" {
		return false
	}
	return true
}

func shouldRunMppShadow(cfg RebalanceConfig, jobSource string) bool {
	return shouldRunMppExecute(cfg, jobSource)
}

func shouldUseRecentFailureCache(jobSource string, jobReason string) bool {
	jobSource = strings.TrimSpace(strings.ToLower(jobSource))
	if jobSource == "auto" {
		return true
	}
	jobReason = strings.TrimSpace(strings.ToLower(jobReason))
	return jobSource == "manual" && jobReason == "auto-restart"
}

func buildMppShadowPlan(targetAmountSat int64, sources []RebalanceChannel, cfg RebalanceConfig) mppShadowPlan {
	plan := mppShadowPlan{}
	if targetAmountSat <= 0 {
		return plan
	}

	maxShards := cfg.MppMaxShards
	if maxShards <= 0 {
		maxShards = 1
	}
	minShardSat := cfg.MppMinShardSat
	if minShardSat <= 0 {
		minShardSat = 1
	}

	capacityLeft := make([]int64, len(sources))
	for i := range sources {
		cap := sources[i].MaxSourceSat
		if cap < 0 {
			cap = 0
		}
		capacityLeft[i] = cap
		if cap >= minShardSat {
			plan.EligibleSources++
		}
	}

	remaining := targetAmountSat
	usedSource := make(map[int]bool, len(sources))

	selectSource := func(desired int64, requireDesired bool, preferUnused bool) int {
		bestIdx := -1
		bestCap := int64(0)
		for i, cap := range capacityLeft {
			if preferUnused && usedSource[i] {
				continue
			}
			if requireDesired {
				if cap < desired {
					continue
				}
			} else if cap < minShardSat {
				continue
			}
			if bestIdx < 0 || cap > bestCap {
				bestIdx = i
				bestCap = cap
			}
		}
		return bestIdx
	}

	for len(plan.Shards) < maxShards && remaining >= minShardSat {
		shardsLeft := maxShards - len(plan.Shards)
		desired := remaining / int64(shardsLeft)
		if remaining%int64(shardsLeft) != 0 {
			desired++
		}
		if desired < minShardSat {
			desired = minShardSat
		}
		if desired > remaining {
			desired = remaining
		}

		chosenIdx := selectSource(desired, true, true)
		if chosenIdx < 0 {
			// Prefer source diversity even when the exact desired shard size is unavailable.
			chosenIdx = selectSource(desired, false, true)
		}
		if chosenIdx < 0 {
			chosenIdx = selectSource(desired, true, false)
		}
		if chosenIdx < 0 {
			chosenIdx = selectSource(desired, false, false)
		}
		if chosenIdx < 0 {
			break
		}
		if desired > capacityLeft[chosenIdx] {
			desired = capacityLeft[chosenIdx]
		}
		if desired < minShardSat {
			break
		}

		if desired <= 0 || desired > capacityLeft[chosenIdx] {
			break
		}
		usedSource[chosenIdx] = true
		capacityLeft[chosenIdx] -= desired
		remaining -= desired
		plan.Shards = append(plan.Shards, mppShadowShard{
			SourceChannelID: sources[chosenIdx].ChannelID,
			AmountSat:       desired,
		})
		plan.PlannedTotalSat += desired
	}

	sourceSet := map[uint64]struct{}{}
	for _, shard := range plan.Shards {
		if shard.AmountSat <= 0 {
			continue
		}
		sourceSet[shard.SourceChannelID] = struct{}{}
	}
	plan.PlannedSources = len(sourceSet)
	plan.PlannedShards = len(plan.Shards)
	if remaining > 0 {
		plan.PlannedRemainderSat = remaining
	}
	return plan
}

func effectiveConfigForTarget(cfg RebalanceConfig, setting channelSetting) RebalanceConfig {
	normalized := normalizeChannelSetting(setting)
	effective := cfg
	if !normalized.UseDefaultEconRatio && normalized.EconRatioOverrideSet {
		effective.EconRatio = normalized.EconRatioOverride
	}
	return effective
}

func (s *RebalanceService) reconcileNewChannelDefaults(ctx context.Context, channels []lndclient.ChannelInfo, settings map[uint64]channelSetting, exclusions map[uint64]bool) {
	if s.db == nil || len(channels) == 0 {
		return
	}

	seeded, err := s.loadNewChannelExclusionSeeded(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance channel defaults: seed state unavailable: %v", err)
		}
		return
	}

	seedReady := true
	for _, ch := range channels {
		if ch.ChannelID == 0 {
			continue
		}
		if _, exists := settings[ch.ChannelID]; exists {
			continue
		}

		_, err := s.db.Exec(ctx, `
insert into rebalance_channel_settings (
  channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, updated_at
)
values ($1,$2,$3,false,false,true,now())
 on conflict (channel_id) do update set channel_point=excluded.channel_point, updated_at=now()
`, int64(ch.ChannelID), ch.ChannelPoint, rebalanceDefaultTargetOutboundPct)
		if err != nil {
			seedReady = false
			if s.logger != nil {
				s.logger.Printf("rebalance channel defaults: failed to upsert settings for %d: %v", ch.ChannelID, err)
			}
			continue
		}

		settings[ch.ChannelID] = normalizeChannelSetting(channelSetting{
			ChannelID:           ch.ChannelID,
			ChannelPoint:        ch.ChannelPoint,
			TargetOutboundPct:   rebalanceDefaultTargetOutboundPct,
			UseDefaultEconRatio: true,
		})

		if !seeded || exclusions[ch.ChannelID] {
			continue
		}

		_, err = s.db.Exec(ctx, `
insert into rebalance_source_exclusions (channel_id, channel_point, reason)
values ($1,$2,$3)
 on conflict (channel_id) do update set channel_point=excluded.channel_point, reason=excluded.reason
`, int64(ch.ChannelID), ch.ChannelPoint, "new_channel_default")
		if err != nil {
			if s.logger != nil {
				s.logger.Printf("rebalance channel defaults: failed to exclude channel %d: %v", ch.ChannelID, err)
			}
			continue
		}
		exclusions[ch.ChannelID] = true
	}

	if !seeded && seedReady {
		if err := s.markNewChannelExclusionSeeded(ctx); err != nil && s.logger != nil {
			s.logger.Printf("rebalance channel defaults: failed to mark seed state: %v", err)
		}
	}
}

func (s *RebalanceService) runManualRestartWatchLoop() {
	for {
		cfg, _ := s.loadConfig(context.Background())
		interval := manualRestartInterval(cfg)
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
			s.runManualRestartWatch()
		case <-s.stop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (s *RebalanceService) runManualRestartWatch() {
	cfg, err := s.loadConfig(context.Background())
	if err != nil || !cfg.ManualRestartWatch {
		return
	}
	if s.lnd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	settings, _ := s.loadChannelSettings(ctx)
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx)
	_ = s.applyForwardDeltas(ctx, ledger)
	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return
	}
	s.reconcileNewChannelDefaults(ctx, channels, settings, exclusions)

	for _, ch := range channels {
		setting := settings[ch.ChannelID]
		if !setting.ManualRestartEnabled {
			continue
		}
		if s.isChannelBusy(ch.ChannelID) {
			continue
		}
		snapshot := s.buildChannelSnapshot(ctx, cfg, false, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		if !snapshot.EligibleAsTarget {
			continue
		}
		deficit := computeDeficitAmount(ch, snapshot.TargetOutboundPct)
		if deficit <= 0 {
			continue
		}
		minExecuteSat := effectiveMinExecuteSat(cfg)
		if minExecuteSat > 0 && deficit < minExecuteSat {
			continue
		}
		_, err := s.startJob(ch.ChannelID, "manual", "auto-restart", 0, true)
		if err != nil && (err.Error() == "channel busy" || errors.Is(err, errManualRestartCooldown)) {
			continue
		}
	}
}

func (s *RebalanceService) runAutoScan() {
	cfg, err := s.loadConfig(context.Background())
	if err != nil {
		return
	}
	if !cfg.AutoEnabled {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := s.ensureDailyBudget(ctx, cfg); err != nil && s.logger != nil {
		s.logger.Printf("rebalance budget ensure failed: %v", err)
	}

	settings, _ := s.loadChannelSettings(ctx)
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx)

	_ = s.applyForwardDeltas(ctx, ledger)

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return
	}
	s.reconcileNewChannelDefaults(ctx, channels, settings, exclusions)
	scanAt := time.Now()
	scanStatus := "scanned"
	scanDetail := ""
	scanCandidates := 0
	scanRemainingBudget := int64(0)
	scanReasons := map[string]int{}
	profitSkipped := 0
	topScore := int64(0)
	queuedCount := 0
	skippedDetails := []RebalanceSkipDetail{}
	defer func() {
		s.mu.Lock()
		s.lastScan = scanAt
		s.lastScanStatus = scanStatus
		s.lastScanDetail = scanDetail
		s.lastScanCandidates = scanCandidates
		s.lastScanRemainingBudgetSat = scanRemainingBudget
		reasonsCopy := map[string]int{}
		for key, value := range scanReasons {
			reasonsCopy[key] = value
		}
		s.lastScanReasons = reasonsCopy
		s.lastScanTopScoreSat = topScore
		s.lastScanProfitSkipped = profitSkipped
		s.lastScanQueued = queuedCount
		if len(skippedDetails) > scanSkipDetailLimit {
			skippedDetails = skippedDetails[:scanSkipDetailLimit]
		}
		s.lastScanSkipped = skippedDetails
		s.mu.Unlock()
	}()

	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)
	lastAutoByTarget := s.loadLastAutoEnqueueTimes(ctx)
	s.mu.Lock()
	for channelID, last := range s.lastAutoByTarget {
		if existing, ok := lastAutoByTarget[channelID]; !ok || last.After(existing) {
			lastAutoByTarget[channelID] = last
		}
	}
	s.mu.Unlock()

	s.mu.Lock()
	criticalActive := cfg.CriticalCycles > 0 && s.criticalMissCount >= cfg.CriticalCycles
	s.mu.Unlock()

	candidates := []rebalanceTarget{}
	eligibleSources := 0
	totalAvailable := int64(0)
	roiSkipped := 0
	belowExecuteMinSkipped := 0
	topScoreSet := false
	for _, ch := range channels {
		setting := settings[ch.ChannelID]
		snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		if snapshot.EligibleAsSource {
			eligibleSources++
			totalAvailable += snapshot.MaxSourceSat
		}

		if setting.AutoEnabled && snapshot.EligibleAsTarget {
			targetCfg := effectiveConfigForTarget(cfg, setting)
			targetAmount := snapshot.TargetAmountSat
			minExecuteSat := effectiveMinExecuteSat(targetCfg)
			if targetCfg.MinSplitEnabled && minExecuteSat > 0 && targetAmount < minExecuteSat {
				belowExecuteMinSkipped++
				skippedDetails = append(skippedDetails, RebalanceSkipDetail{
					ChannelID:         snapshot.ChannelID,
					ChannelPoint:      snapshot.ChannelPoint,
					PeerAlias:         snapshot.PeerAlias,
					TargetOutboundPct: snapshot.TargetOutboundPct,
					TargetAmountSat:   targetAmount,
					Reason:            "below_execute_min",
				})
				continue
			}
			estimatedCost := estimateHistoricalCost(targetAmount, snapshot.RebalanceCost7dPpm)
			expectedGain := estimateTargetGain(targetAmount, snapshot.Revenue7dSat, snapshot.LocalBalanceSat, snapshot.CapacitySat)
			expectedROI, roiValid := estimateTargetROI(expectedGain, estimatedCost, targetAmount, snapshot.OutgoingFeePpm, snapshot.PeerFeeRatePpm)
			if cfg.ROIMin > 0 && roiValid && expectedROI < cfg.ROIMin {
				roiSkipped++
				skippedDetails = append(skippedDetails, RebalanceSkipDetail{
					ChannelID:         snapshot.ChannelID,
					ChannelPoint:      snapshot.ChannelPoint,
					PeerAlias:         snapshot.PeerAlias,
					TargetOutboundPct: snapshot.TargetOutboundPct,
					TargetAmountSat:   targetAmount,
					ExpectedGainSat:   expectedGain,
					EstimatedCostSat:  estimatedCost,
					ExpectedROI:       expectedROI,
					ExpectedROIValid:  roiValid,
					Reason:            "roi_guardrail",
				})
				continue
			}
			if expectedGain > 0 && estimatedCost > 0 && expectedGain < estimatedCost {
				profitSkipped++
				skippedDetails = append(skippedDetails, RebalanceSkipDetail{
					ChannelID:         snapshot.ChannelID,
					ChannelPoint:      snapshot.ChannelPoint,
					PeerAlias:         snapshot.PeerAlias,
					TargetOutboundPct: snapshot.TargetOutboundPct,
					TargetAmountSat:   targetAmount,
					ExpectedGainSat:   expectedGain,
					EstimatedCostSat:  estimatedCost,
					ExpectedROI:       expectedROI,
					ExpectedROIValid:  roiValid,
					Reason:            "profit_guardrail",
				})
				continue
			}
			score := expectedGain - estimatedCost
			candidates = append(candidates, rebalanceTarget{
				Channel:          snapshot,
				ExpectedGainSat:  expectedGain,
				EstimatedCostSat: estimatedCost,
				ExpectedROI:      expectedROI,
				ExpectedROIValid: roiValid,
				Score:            score,
				LastAutoAt:       lastAutoByTarget[snapshot.ChannelID],
			})
			if !topScoreSet || score > topScore {
				topScore = score
				topScoreSet = true
			}
		}
	}

	if eligibleSources == 0 ||
		(cfg.CriticalMinSources > 0 && eligibleSources < cfg.CriticalMinSources) ||
		(cfg.CriticalMinAvailableSats > 0 && totalAvailable < cfg.CriticalMinAvailableSats) {
		s.mu.Lock()
		s.criticalMissCount++
		s.mu.Unlock()
		scanStatus = "no_sources"
		return
	}

	if len(candidates) == 0 {
		s.mu.Lock()
		s.criticalMissCount++
		s.mu.Unlock()
		scanCandidates = 0
		scanRemainingBudget = 0
		scanReasons = map[string]int{}
		if belowExecuteMinSkipped > 0 {
			scanReasons["below_execute_min"] = belowExecuteMinSkipped
		}
		if profitSkipped > 0 {
			scanStatus = "profit_guardrail"
			if s.logger != nil {
				s.logger.Printf("rebalance scan: profit guardrail skipped all targets (skipped=%d, roi_skipped=%d)", profitSkipped, roiSkipped)
			}
		} else {
			scanStatus = "no_candidates"
		}
		return
	}

	s.mu.Lock()
	s.criticalMissCount = 0
	s.mu.Unlock()

	sort.Slice(candidates, func(i, j int) bool {
		a := candidates[i]
		b := candidates[j]
		if a.LastAutoAt.IsZero() != b.LastAutoAt.IsZero() {
			return a.LastAutoAt.IsZero()
		}
		if !a.LastAutoAt.IsZero() && !b.LastAutoAt.IsZero() {
			if !a.LastAutoAt.Equal(b.LastAutoAt) {
				return a.LastAutoAt.Before(b.LastAutoAt)
			}
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.ExpectedROI != b.ExpectedROI {
			return a.ExpectedROI > b.ExpectedROI
		}
		if a.Channel.TargetAmountSat != b.Channel.TargetAmountSat {
			return a.Channel.TargetAmountSat > b.Channel.TargetAmountSat
		}
		if a.Channel.LocalPct != b.Channel.LocalPct {
			return a.Channel.LocalPct < b.Channel.LocalPct
		}
		return a.Channel.ChannelID < b.Channel.ChannelID
	})

	cooldown := time.Duration(cfg.ScanIntervalSec) * time.Second
	if cooldown <= 0 {
		cooldown = autoTargetCooldownMin
	} else if cooldown < autoTargetCooldownMin {
		cooldown = autoTargetCooldownMin
	}
	recentSkipped := 0
	if len(candidates) > 1 {
		filtered := make([]rebalanceTarget, 0, len(candidates))
		for _, target := range candidates {
			if !target.LastAutoAt.IsZero() && scanAt.Sub(target.LastAutoAt) < cooldown {
				recentSkipped++
				continue
			}
			filtered = append(filtered, target)
		}
		if len(filtered) > 0 {
			candidates = filtered
		} else {
			recentSkipped = 0
		}
	}

	budget, _, spentManual, spentTotal := s.getDailyBudget(ctx)
	manualReserveSat := computeManualReserveSat(cfg, budget)
	_, remaining := computeRemainingForAuto(budget, spentTotal, spentManual, manualReserveSat)
	if remaining == 0 {
		scanCandidates = len(candidates)
		scanRemainingBudget = 0
		scanReasons = map[string]int{"budget_too_low": len(candidates)}
		scanStatus = "budget_exhausted"
		return
	}

	skipReasons := map[string]int{}
	noteSkip := func(key string) {
		if key == "" {
			return
		}
		skipReasons[key]++
	}
	for i := 0; i < recentSkipped; i++ {
		noteSkip("recently_attempted")
	}

	for _, target := range candidates {
		if remaining <= 0 {
			scanStatus = "budget_exhausted"
			break
		}
		targetCfg := effectiveConfigForTarget(cfg, settings[target.Channel.ChannelID])
		targetPolicy := lndclient.ChannelPolicySnapshot{
			FeeRatePpm:  target.Channel.OutgoingFeePpm,
			BaseFeeMsat: target.Channel.OutgoingBaseMsat,
		}
		maxFeeMsat, err := calcFeeLimitMsat(target.Channel.TargetAmountSat*1000, targetPolicy, nil, targetCfg)
		if err != nil || maxFeeMsat <= 0 {
			noteSkip("fee_cap_zero")
			continue
		}
		maxFeePpm := feeMsatToPpm(maxFeeMsat, target.Channel.TargetAmountSat)
		if maxFeePpm <= 0 {
			noteSkip("fee_cap_zero")
			continue
		}
		targetAmount := target.Channel.TargetAmountSat
		estimatedCost := estimateMaxCost(targetAmount, targetPolicy, targetCfg)
		amountOverride := int64(0)
		if estimatedCost > remaining {
			fitAmount := (remaining * 1_000_000) / maxFeePpm
			if fitAmount <= 0 {
				noteSkip("budget_too_low")
				continue
			}
			if fitAmount > targetAmount {
				fitAmount = targetAmount
			}
			minExecuteSat := effectiveMinExecuteSat(targetCfg)
			if minExecuteSat > 0 && fitAmount < minExecuteSat {
				noteSkip("budget_below_min")
				continue
			}
			amountOverride = fitAmount
			estimatedCost = estimateMaxCost(fitAmount, targetPolicy, targetCfg)
			if estimatedCost > remaining {
				noteSkip("budget_too_low")
				continue
			}
		}
		_, err = s.startJob(target.Channel.ChannelID, "auto", "", amountOverride, false)
		if err == nil {
			s.mu.Lock()
			s.lastAutoByTarget[target.Channel.ChannelID] = scanAt
			s.mu.Unlock()
			remaining -= estimatedCost
			queuedCount++
		} else {
			switch err.Error() {
			case "channel busy":
				noteSkip("channel_busy")
				skippedDetails = append(skippedDetails, RebalanceSkipDetail{
					ChannelID:         target.Channel.ChannelID,
					ChannelPoint:      target.Channel.ChannelPoint,
					PeerAlias:         target.Channel.PeerAlias,
					TargetOutboundPct: target.Channel.TargetOutboundPct,
					TargetAmountSat:   target.Channel.TargetAmountSat,
					ExpectedGainSat:   target.ExpectedGainSat,
					EstimatedCostSat:  target.EstimatedCostSat,
					ExpectedROI:       target.ExpectedROI,
					ExpectedROIValid:  target.ExpectedROIValid,
					Reason:            "channel_busy",
				})
			case "target already within range":
				noteSkip("target_already_balanced")
			case "target channel not found":
				noteSkip("target_not_found")
			default:
				noteSkip("start_error")
			}
		}
	}

	if queuedCount > 0 {
		scanStatus = "queued"
		scanCandidates = len(candidates)
		scanRemainingBudget = remaining
		scanReasons = map[string]int{}
	} else if remaining > 0 {
		budgetBlocked := skipReasons["budget_too_low"] + skipReasons["budget_below_min"]
		if budgetBlocked == len(candidates) {
			scanStatus = "budget_insufficient"
		} else {
			scanStatus = "no_queue"
		}
		scanCandidates = len(candidates)
		scanRemainingBudget = remaining
		reasonsCopy := map[string]int{}
		for key, value := range skipReasons {
			reasonsCopy[key] = value
		}
		scanReasons = reasonsCopy
		scanDetail = buildScanDetail(skipReasons, remaining, len(candidates))
	}

	if s.logger != nil {
		s.logger.Printf("rebalance scan: candidates=%d queued=%d profit_skipped=%d roi_skipped=%d top_score=%d sats", len(candidates), queuedCount, profitSkipped, roiSkipped, topScore)
	}
}

type rebalanceTarget struct {
	Channel          RebalanceChannel
	ExpectedGainSat  int64
	EstimatedCostSat int64
	ExpectedROI      float64
	ExpectedROIValid bool
	Score            int64
	LastAutoAt       time.Time
}

func (s *RebalanceService) startJob(targetChannelID uint64, source string, reason string, amountOverride int64, manualAutoRestart bool) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := s.loadConfig(ctx)
	if err != nil {
		cfg = defaultRebalanceConfig()
	}

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return 0, err
	}
	var target lndclient.ChannelInfo
	found := false
	for _, ch := range channels {
		if ch.ChannelID == targetChannelID {
			target = ch
			found = true
			break
		}
	}
	if !found {
		return 0, errors.New("target channel not found")
	}

	settings, _ := s.loadChannelSettings(ctx)
	setting := normalizeChannelSetting(settings[targetChannelID])
	targetPct := setting.TargetOutboundPct
	deficit := computeDeficitAmount(target, targetPct)
	if deficit <= 0 {
		return 0, errors.New("target already within range")
	}
	amount := deficit
	if amountOverride > 0 && amountOverride < amount {
		amount = amountOverride
	}

	if s.isChannelBusy(targetChannelID) {
		return 0, errors.New("channel busy")
	}
	if shouldEnforceManualRestartCooldown(source, reason) {
		if lastRestartAt, ok := s.lastManualAutoRestartAt(ctx, targetChannelID); ok {
			if time.Since(lastRestartAt) < manualRestartInterval(cfg) {
				return 0, errManualRestartCooldown
			}
		}
	}
	if strings.EqualFold(source, "manual") {
		budget, _, spentManual, spentTotal := s.getDailyBudget(ctx)
		if err := checkManualBudgetAllowance(cfg, setting, target, amount, budget, spentManual, spentTotal); err != nil {
			return 0, err
		}
	}

	jobID, err := s.insertJob(ctx, &target, source, reason, targetPct, amount)
	if err != nil {
		return 0, err
	}

	if manualAutoRestart && source == "manual" {
		s.mu.Lock()
		if s.manualRestart == nil {
			s.manualRestart = map[int64]manualRestartInfo{}
		}
		s.manualRestart[jobID] = manualRestartInfo{
			TargetChannelID: targetChannelID,
		}
		s.mu.Unlock()
	}

	go s.runJob(jobID, targetChannelID, amount, targetPct, source, reason)
	return jobID, nil
}

func (s *RebalanceService) runJob(jobID int64, targetChannelID uint64, amount int64, targetPct float64, jobSource string, jobReason string) {
	cfg, _ := s.loadConfig(context.Background())
	minExecuteSat := effectiveMinExecuteSat(cfg)
	minProbeSat := effectiveMinProbeSat(cfg)
	useRecentFailureCache := shouldUseRecentFailureCache(jobSource, jobReason)
	timeoutSec := cfg.RebalanceTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	s.mu.Lock()
	s.jobCancel[jobID] = cancel
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.jobCancel, jobID)
		s.mu.Unlock()
	}()
	shadowRecorded := false
	floorBlockedSources := map[uint64]struct{}{}
	defer func() {
		if !shadowRecorded {
			return
		}
		shadowCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.updateMppShadowFloorBlockedSources(shadowCtx, jobID, int64(len(floorBlockedSources))); err != nil && s.logger != nil {
			s.logger.Printf("rebalance mpp shadow floor telemetry update failed: job=%d err=%v", jobID, err)
		}
		if err := s.finalizeMppShadowPlan(shadowCtx, jobID); err != nil && s.logger != nil {
			s.logger.Printf("rebalance mpp shadow finalize failed: job=%d err=%v", jobID, err)
		}
	}()

	if !s.acquireSem(ctx) {
		s.finishJob(jobID, "failed", "no worker available")
		return
	}
	defer s.releaseSem()

	if !s.tryLockChannel(targetChannelID) {
		s.finishJob(jobID, "failed", "channel busy")
		return
	}
	defer s.unlockChannel(targetChannelID)

	s.markJobRunning(jobID)

	if minExecuteSat > 0 && amount < minExecuteSat {
		s.finishJob(jobID, "failed", "amount below minimum")
		return
	}
	if cfg.MaxAmountSat > 0 && amount > cfg.MaxAmountSat {
		amount = cfg.MaxAmountSat
	}

	settings, _ := s.loadChannelSettings(ctx)
	targetSetting := normalizeChannelSetting(settings[targetChannelID])
	feeCfg := effectiveConfigForTarget(cfg, targetSetting)
	minExecuteSat = effectiveMinExecuteSat(feeCfg)
	minProbeSat = effectiveMinProbeSat(feeCfg)
	startAmountSat := effectiveStartAmountSat(feeCfg)
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx)
	_ = s.applyForwardDeltas(ctx, ledger)

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		s.finishJob(jobID, "failed", "lnd unavailable")
		return
	}
	s.reconcileNewChannelDefaults(ctx, channels, settings, exclusions)

	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)

	targetFound := false
	channelSnapshots := []RebalanceChannel{}
	for _, ch := range channels {
		setting := settings[ch.ChannelID]
		snapshot := s.buildChannelSnapshot(ctx, cfg, false, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		if ch.ChannelID == targetChannelID {
			targetFound = true
			snapshot.TargetOutboundPct = targetPct
			deficitAmount := computeDeficitAmount(ch, targetPct)
			if amount <= 0 || amount > deficitAmount {
				amount = deficitAmount
			}
			if cfg.MaxAmountSat > 0 && amount > cfg.MaxAmountSat {
				amount = cfg.MaxAmountSat
			}
			snapshot.TargetAmountSat = amount
			deficitPct := snapshot.TargetOutboundPct - snapshot.LocalPct
			snapshot.EligibleAsTarget = snapshot.Active && deficitPct > cfg.DeadbandPct && snapshot.OutgoingFeePpm > snapshot.PeerFeeRatePpm
		}
		channelSnapshots = append(channelSnapshots, snapshot)
	}

	if !targetFound {
		s.finishJob(jobID, "failed", "target channel not found")
		return
	}

	if amount <= 0 {
		s.finishJob(jobID, "failed", "target already balanced")
		return
	}
	if minExecuteSat > 0 && amount < minExecuteSat {
		s.finishJob(jobID, "failed", "amount below minimum")
		return
	}

	targetSnapshot := RebalanceChannel{}
	for _, snap := range channelSnapshots {
		if snap.ChannelID == targetChannelID {
			targetSnapshot = snap
			break
		}
	}

	if !targetSnapshot.EligibleAsTarget {
		s.finishJob(jobID, "failed", "target not eligible")
		return
	}
	if strings.TrimSpace(targetSnapshot.RemotePubkey) == "" {
		s.finishJob(jobID, "failed", "target peer unavailable")
		return
	}

	sources := filterSources(channelSnapshots, targetChannelID)
	if shouldRunMppShadow(feeCfg, jobSource) {
		shadowPlan := buildMppShadowPlan(amount, sources, feeCfg)
		if err := s.insertMppShadowPlan(ctx, jobID, targetChannelID, jobSource, feeCfg, amount, shadowPlan); err != nil {
			if s.logger != nil {
				s.logger.Printf("rebalance mpp shadow insert failed: job=%d err=%v", jobID, err)
			}
		} else {
			shadowRecorded = true
			if s.logger != nil {
				s.logger.Printf(
					"rebalance mpp shadow: job=%d source=%s planned_shards=%d planned_total=%d remainder=%d eligible_sources=%d planned_sources=%d",
					jobID,
					jobSource,
					shadowPlan.PlannedShards,
					shadowPlan.PlannedTotalSat,
					shadowPlan.PlannedRemainderSat,
					shadowPlan.EligibleSources,
					shadowPlan.PlannedSources,
				)
			}
		}
	}
	if len(sources) == 0 {
		s.finishJob(jobID, "failed", "no eligible sources")
		return
	}

	sourceFloorPct := feeCfg.SourceMinLocalPct
	if sourceFloorPct <= 0 || sourceFloorPct > 100 {
		sourceFloorPct = rebalanceDefaultTargetOutboundPct
	}
	sourceBaseCap := make(map[uint64]int64, len(sources))
	sourceAvailable := make(map[uint64]int64, len(sources))
	for _, source := range sources {
		baseCap := source.MaxSourceSat
		if baseCap < 0 {
			baseCap = 0
		}
		sourceBaseCap[source.ChannelID] = baseCap
		sourceAvailable[source.ChannelID] = baseCap
	}
	lastSourceRefreshAt := time.Time{}
	refreshSourceAvailability := func(force bool) {
		if s.lnd == nil {
			return
		}
		if !force && !lastSourceRefreshAt.IsZero() && time.Since(lastSourceRefreshAt) < 5*time.Second {
			return
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		channelsNow, err := s.lnd.ListChannels(refreshCtx)
		if err != nil {
			return
		}
		byID := make(map[uint64]lndclient.ChannelInfo, len(channelsNow))
		for _, ch := range channelsNow {
			byID[ch.ChannelID] = ch
		}
		for _, source := range sources {
			prevCap := sourceAvailable[source.ChannelID]
			baseCap := sourceBaseCap[source.ChannelID]
			availableCap := baseCap
			blockedByDynamicFloor := false
			ch, ok := byID[source.ChannelID]
			if !ok || !ch.Active {
				availableCap = 0
			} else {
				dynamicCap := int64(float64(ch.LocalBalanceSat) - (float64(ch.CapacitySat) * (sourceFloorPct / 100)))
				if dynamicCap < 0 {
					dynamicCap = 0
				}
				if baseCap > 0 && dynamicCap <= 0 {
					blockedByDynamicFloor = true
				}
				if dynamicCap < availableCap {
					availableCap = dynamicCap
				}
			}
			if availableCap < 0 {
				availableCap = 0
			}
			sourceAvailable[source.ChannelID] = availableCap
			if blockedByDynamicFloor && prevCap > 0 {
				floorBlockedSources[source.ChannelID] = struct{}{}
			}
		}
		lastSourceRefreshAt = time.Now()
	}

	pairStats := s.loadPairStatsForTarget(ctx, targetChannelID)

	sort.Slice(sources, func(i, j int) bool {
		rank := func(ch RebalanceChannel) (bool, bool, int64, int64, time.Duration) {
			stat, ok := pairStats[ch.ChannelID]
			if !ok {
				return false, false, 0, 0, 0
			}
			hasRecentSuccess := false
			if !stat.LastSuccessAt.IsZero() && time.Since(stat.LastSuccessAt) <= pairSuccessTTL {
				if stat.LastFailAt.IsZero() || stat.LastSuccessAt.After(stat.LastFailAt) {
					hasRecentSuccess = true
				}
			}
			hasRecentFail := false
			if !stat.LastFailAt.IsZero() && time.Since(stat.LastFailAt) <= pairFailTTL {
				if stat.LastSuccessAt.IsZero() || stat.LastFailAt.After(stat.LastSuccessAt) {
					hasRecentFail = true
				}
			}
			age := time.Duration(0)
			if !stat.LastSuccessAt.IsZero() {
				age = time.Since(stat.LastSuccessAt)
			}
			return hasRecentSuccess, hasRecentFail, stat.SuccessFeePpm, stat.SuccessAmountSat, age
		}

		iSuccess, iFail, iFee, iAmt, iAge := rank(sources[i])
		jSuccess, jFail, jFee, jAmt, jAge := rank(sources[j])

		if iSuccess != jSuccess {
			return iSuccess
		}
		if iFail != jFail {
			return !iFail
		}
		if iSuccess && jSuccess {
			if iFee != jFee {
				return iFee < jFee
			}
			if iAge != jAge {
				return iAge < jAge
			}
			if iAmt != jAmt {
				return iAmt > jAmt
			}
		}
		return sources[i].MaxSourceSat > sources[j].MaxSourceSat
	})
	refreshSourceAvailability(true)

	targetPolicy := lndclient.ChannelPolicySnapshot{
		FeeRatePpm:  targetSnapshot.OutgoingFeePpm,
		BaseFeeMsat: targetSnapshot.OutgoingBaseMsat,
	}
	maxFeeMsat, feeErr := calcFeeLimitMsat(amount*1000, targetPolicy, nil, feeCfg)
	maxFeePpm := feeMsatToPpm(maxFeeMsat, amount)
	if feeErr != nil || maxFeeMsat <= 0 || maxFeePpm <= 0 {
		s.finishJob(jobID, "failed", "fee cap zero")
		return
	}

	selfPubkey, selfErr := s.lnd.SelfPubkey(ctx)
	if selfErr != nil || strings.TrimSpace(selfPubkey) == "" {
		s.finishJob(jobID, "failed", "local pubkey unavailable")
		return
	}

	warmSourceID := uint64(0)
	warmAmount := int64(0)
	warmFeePpm := int64(0)
	warmAt := time.Time{}
	for _, source := range sources {
		if sourceAvailable[source.ChannelID] <= 0 {
			continue
		}
		stat, ok := pairStats[source.ChannelID]
		if !ok {
			continue
		}
		if stat.SuccessAmountSat <= 0 || stat.SuccessFeePpm <= 0 {
			continue
		}
		if stat.LastSuccessAt.IsZero() || time.Since(stat.LastSuccessAt) > pairSuccessTTL {
			continue
		}
		if !stat.LastFailAt.IsZero() && stat.LastFailAt.After(stat.LastSuccessAt) {
			continue
		}
		sourcePolicy := lndclient.ChannelPolicySnapshot{
			FeeRatePpm:  source.OutgoingFeePpm,
			BaseFeeMsat: source.OutgoingBaseMsat,
		}
		maxFeeMsat, err := calcFeeLimitMsat(stat.SuccessAmountSat*1000, targetPolicy, &sourcePolicy, feeCfg)
		maxFeePpm := feeMsatToPpm(maxFeeMsat, stat.SuccessAmountSat)
		if err != nil || maxFeePpm <= 0 || stat.SuccessFeePpm > maxFeePpm {
			continue
		}
		if minExecuteSat > 0 && stat.SuccessAmountSat < minExecuteSat {
			continue
		}
		if stat.LastSuccessAt.After(warmAt) {
			warmAt = stat.LastSuccessAt
			warmSourceID = source.ChannelID
			warmAmount = stat.SuccessAmountSat
			warmFeePpm = stat.SuccessFeePpm
		}
	}

	remaining := amount
	anySuccess := false
	attemptedAny := false
	skippedByCache := 0
	attemptIndex := 0
	adaptiveMaxAmount := int64(0)
	attemptTimeoutSec := cfg.AttemptTimeoutSec
	if attemptTimeoutSec <= 0 {
		attemptTimeoutSec = 60
	}
	autoNoPathBackoff := time.Duration(0)
	autoNoPathBase := 1 * time.Second
	autoNoPathMax := 10 * time.Second
	autoNoPathCount := 0
	autoNoPathThreshold := 6
	autoNoPathResetDone := false
	ignoredEdgeSet := map[string]struct{}{}
	ignoredEdges := make([]*lnrpc.EdgeLocator, 0)
	ignoredPairSet := map[string]struct{}{}
	ignoredPairs := make([]*lnrpc.NodePair, 0)
	maxIgnoredEntries := 500

	sleepWithContext := func(d time.Duration) bool {
		if d <= 0 {
			return true
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
			return true
		case <-ctx.Done():
			return false
		}
	}

	noteAutoNoPath := func() {
		if jobSource != "auto" {
			return
		}
		autoNoPathCount++
		if !autoNoPathResetDone && autoNoPathCount >= autoNoPathThreshold {
			if s.lnd != nil {
				resetCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := s.lnd.ResetMissionControl(resetCtx)
				cancel()
				if err == nil {
					autoNoPathResetDone = true
					autoNoPathCount = 0
					if s.logger != nil {
						s.logger.Printf("rebalance auto: mission control reset after %d no-path attempts", autoNoPathThreshold)
					}
				} else if s.logger != nil {
					s.logger.Printf("rebalance auto: mission control reset failed: %v", err)
				}
			}
		}
		if autoNoPathBackoff <= 0 {
			autoNoPathBackoff = autoNoPathBase
		} else {
			autoNoPathBackoff *= 2
			if autoNoPathBackoff > autoNoPathMax {
				autoNoPathBackoff = autoNoPathMax
			}
		}
		_ = sleepWithContext(autoNoPathBackoff)
	}

	resetAutoNoPath := func() {
		if jobSource == "auto" {
			autoNoPathBackoff = 0
			autoNoPathCount = 0
		}
	}

	addIgnoredEdge := func(chanId uint64) {
		if chanId == 0 || len(ignoredEdges) >= maxIgnoredEntries {
			return
		}
		for _, dir := range []bool{false, true} {
			key := fmt.Sprintf("%d:%t", chanId, dir)
			if _, ok := ignoredEdgeSet[key]; ok {
				continue
			}
			ignoredEdgeSet[key] = struct{}{}
			ignoredEdges = append(ignoredEdges, &lnrpc.EdgeLocator{
				ChannelId:        chanId,
				DirectionReverse: dir,
			})
			if len(ignoredEdges) >= maxIgnoredEntries {
				return
			}
		}
	}

	addIgnoredPair := func(from, to string) {
		if len(ignoredPairs) >= maxIgnoredEntries {
			return
		}
		from = strings.TrimSpace(from)
		to = strings.TrimSpace(to)
		if from == "" || to == "" || strings.EqualFold(from, to) {
			return
		}
		key := strings.ToLower(from) + ":" + strings.ToLower(to)
		if _, ok := ignoredPairSet[key]; ok {
			return
		}
		fromBytes, err := hex.DecodeString(from)
		if err != nil {
			return
		}
		toBytes, err := hex.DecodeString(to)
		if err != nil {
			return
		}
		ignoredPairSet[key] = struct{}{}
		ignoredPairs = append(ignoredPairs, &lnrpc.NodePair{
			From: fromBytes,
			To:   toBytes,
		})
	}

	noteRouteFailure := func(route *lnrpc.Route, failureIndex uint32) {
		if route == nil || len(route.Hops) == 0 {
			return
		}
		idx := int(failureIndex) - 1
		if idx < 0 || idx >= len(route.Hops) {
			return
		}
		failedHop := route.Hops[idx]
		if failedHop == nil {
			return
		}
		addIgnoredEdge(failedHop.ChanId)
		fromPub := strings.TrimSpace(selfPubkey)
		if idx > 0 {
			if prevHop := route.Hops[idx-1]; prevHop != nil && strings.TrimSpace(prevHop.PubKey) != "" {
				fromPub = prevHop.PubKey
			}
		}
		if fromPub != "" && strings.TrimSpace(failedHop.PubKey) != "" {
			addIgnoredPair(fromPub, failedHop.PubKey)
			addIgnoredPair(failedHop.PubKey, fromPub)
		}
	}

	reconcileTimeoutPayment := func(paymentHash string, feeLimitPpm int64, source RebalanceChannel, fallbackAmount int64) (bool, int64) {
		if s.lnd == nil || strings.TrimSpace(paymentHash) == "" {
			return false, 0
		}
		lookupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pay, err := s.lnd.LookupPayment(lookupCtx, paymentHash, 2*time.Hour)
		cancel()
		if err != nil || pay == nil || pay.Status != lnrpc.Payment_SUCCEEDED {
			return false, 0
		}
		amountSent := pay.ValueSat
		if amountSent <= 0 && pay.ValueMsat > 0 {
			amountSent = pay.ValueMsat / 1000
		}
		if amountSent <= 0 {
			amountSent = fallbackAmount
		}
		feePaidSat := pay.FeeSat
		if feePaidSat <= 0 && pay.FeeMsat > 0 {
			feePaidSat = msatToSatCeil(pay.FeeMsat)
		}
		attemptIndex++
		_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountSent, feeLimitPpm, feePaidSat, "succeeded", paymentHash, "")
		s.recordPairSuccess(ctx, source.ChannelID, targetChannelID, amountSent, feeLimitPpm, feePaidSat)
		_ = s.applyRebalanceLedger(ctx, targetChannelID, amountSent, feePaidSat)
		_ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
		return true, amountSent
	}

	finishOnTimeout := func() {
		if anySuccess {
			reason := fmt.Sprintf("timeout with %d sats remaining", remaining)
			s.finishJob(jobID, "partial", reason)
			s.broadcast(RebalanceEvent{Type: "job", JobID: jobID, Status: "partial", Message: reason})
		} else {
			s.finishJob(jobID, "failed", "timeout")
		}
	}

	attemptPayment := func(source RebalanceChannel, amountTry int64, feeLimitMsat int64, logRouteFailure bool) (bool, bool, int64, bool, *lnrpc.Route, int64) {
		attemptedAny = true
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				finishOnTimeout()
			} else {
				s.finishJob(jobID, "cancelled", "cancelled")
			}
			return false, true, 0, false, nil, 0
		}
		if amountTry <= 0 {
			return false, false, 0, false, nil, 0
		}
		if feeCfg.MinSplitEnabled && minExecuteSat > 0 && amountTry < minExecuteSat {
			return false, false, 0, false, nil, 0
		}
		sourcePolicy := lndclient.ChannelPolicySnapshot{
			FeeRatePpm:  source.OutgoingFeePpm,
			BaseFeeMsat: source.OutgoingBaseMsat,
		}
		if feeLimitMsat <= 0 {
			maxFeeMsat, err := calcFeeLimitMsat(amountTry*1000, targetPolicy, &sourcePolicy, feeCfg)
			if err != nil || maxFeeMsat <= 0 {
				return false, false, 0, false, nil, 0
			}
			feeLimitMsat = maxFeeMsat
		}
		feeLimitPpm := feeMsatToPpm(feeLimitMsat, amountTry)
		var probeFeeMsat int64
		attemptCtx := ctx
		cancelAttempt := func() {}
		if attemptTimeoutSec > 0 {
			attemptCtx, cancelAttempt = context.WithTimeout(ctx, time.Duration(attemptTimeoutSec)*time.Second)
		}

		routes, err := s.lnd.QueryRoutes(attemptCtx, selfPubkey, amountTry, source.ChannelID, targetSnapshot.RemotePubkey, feeLimitMsat, 5, ignoredEdges, ignoredPairs)
		routeMaxSat := int64(0)
		if err != nil {
			cancelAttempt()
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					finishOnTimeout()
				} else {
					s.finishJob(jobID, "cancelled", "cancelled")
				}
				return false, true, 0, false, nil, 0
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", "attempt timeout")
				s.recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
				return false, false, 0, true, nil, 0
			}
			if isNoPathError(err) {
				noteAutoNoPath()
			} else {
				resetAutoNoPath()
			}
			if logRouteFailure {
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", err.Error())
				s.recordPairFailure(ctx, source.ChannelID, targetChannelID, err.Error())
			}
			return false, false, 0, false, nil, 0
		}
		var lastErr error
		var lastRouteFeeMsat int64
		var lastPaymentHash string
		var lastFeeLimitPpm int64
		var lastAmountTry int64
		for _, route := range routes {
			if route == nil {
				continue
			}
			routeAmount := amountTry
			routeFeeLimitMsat := feeLimitMsat
			routeFeeLimitPpm := feeLimitPpm
			activeRoute := route
			if cfg.AmountProbeSteps > 0 {
				maxAmount, probeErr := s.probeRoute(attemptCtx, route, amountTry, minProbeSat, feeCfg.AmountProbeSteps, targetPolicy, sourcePolicy, feeCfg)
				if probeErr != nil {
					lastErr = probeErr
					continue
				}
				if maxAmount <= 0 {
					lastErr = errors.New("probe returned no amount")
					continue
				}
				if maxAmount > routeAmount {
					maxAmount = routeAmount
				}
				if maxAmount != routeAmount {
					routeAmount = maxAmount
					if feeCfg.MinSplitEnabled && minExecuteSat > 0 && routeAmount < minExecuteSat {
						lastErr = errors.New("probe amount below execute minimum")
						continue
					}
					maxFeeMsat, err := calcFeeLimitMsat(routeAmount*1000, targetPolicy, &sourcePolicy, feeCfg)
					if err != nil || maxFeeMsat <= 0 {
						if err == nil {
							err = errors.New("invalid fee limit")
						}
						lastErr = err
						continue
					}
					routeFeeLimitMsat = maxFeeMsat
					routeFeeLimitPpm = feeMsatToPpm(routeFeeLimitMsat, routeAmount)
					rebuilt, rebuildErr := s.rebuildRouteForAmount(attemptCtx, route, routeAmount)
					if rebuildErr != nil {
						lastErr = rebuildErr
						continue
					}
					activeRoute = rebuilt
				}
			}
			_, paymentHash, paymentAddr, err := s.createRebalanceInvoice(attemptCtx, routeAmount, jobID, source.ChannelID, targetChannelID)
			if err != nil {
				cancelAttempt()
				if ctx.Err() != nil {
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						finishOnTimeout()
					} else {
						s.finishJob(jobID, "cancelled", "cancelled")
					}
					return false, true, 0, false, nil, 0
				}
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
					attemptIndex++
					_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, routeAmount, routeFeeLimitPpm, 0, "failed", "", "attempt timeout")
					s.recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
					return false, false, 0, true, nil, 0
				}
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, routeAmount, routeFeeLimitPpm, 0, "failed", "", err.Error())
				s.recordPairFailure(ctx, source.ChannelID, targetChannelID, err.Error())
				return false, false, 0, false, nil, 0
			}
			lastPaymentHash = paymentHash
			lastFeeLimitPpm = routeFeeLimitPpm
			lastAmountTry = routeAmount

			applyMppRecord(activeRoute, paymentAddr, routeAmount)
			routeFeeMsat := int64(0)
			if activeRoute.TotalFeesMsat > 0 {
				routeFeeMsat = activeRoute.TotalFeesMsat
			} else if activeRoute.TotalFees > 0 {
				routeFeeMsat = activeRoute.TotalFees * 1000
			}
			if routeFeeLimitMsat > 0 && routeFeeMsat > routeFeeLimitMsat {
				lastErr = fmt.Errorf("route fee exceeds limit")
				continue
			}

			routeMaxSat = s.maxAmountOnRouteSat(attemptCtx, activeRoute, selfPubkey)
			if routeFeeMsat > 0 {
				probeFeeMsat = routeFeeMsat
			}
			lastRouteFeeMsat = routeFeeMsat

			_, err = s.lnd.SendToRoute(attemptCtx, paymentHash, activeRoute)
			if err == nil {
				cancelAttempt()
				resetAutoNoPath()
				feePaidSat := msatToSatCeil(routeFeeMsat)
				if feePaidSat == 0 && probeFeeMsat > 0 {
					feePaidSat = msatToSatCeil(probeFeeMsat)
				}
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, routeAmount, routeFeeLimitPpm, feePaidSat, "succeeded", paymentHash, "")
				s.recordPairSuccess(ctx, source.ChannelID, targetChannelID, routeAmount, routeFeeLimitPpm, feePaidSat)
				_ = s.applyRebalanceLedger(ctx, targetChannelID, routeAmount, feePaidSat)
				_ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
				return true, false, routeMaxSat, false, activeRoute, routeAmount
			}

			var routeFailure lndclient.RouteFailureError
			if errors.As(err, &routeFailure) && routeFailure.Failure != nil {
				if routeFailure.Code == lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE {
					noteRouteFailure(activeRoute, routeFailure.FailureSourceIndex)
				}
				failureIdx := int(routeFailure.FailureSourceIndex) - 1
				if (routeFailure.Code == lnrpc.Failure_FEE_INSUFFICIENT || routeFailure.Code == lnrpc.Failure_INCORRECT_CLTV_EXPIRY) &&
					failureIdx >= 0 && failureIdx < len(activeRoute.Hops) {
					updatedRoute, rebuildErr := s.rebuildRouteForAmount(attemptCtx, activeRoute, routeAmount)
					if rebuildErr == nil && !compareHops(activeRoute.Hops[failureIdx], updatedRoute.Hops[failureIdx]) {
						if routeFeeLimitMsat > 0 && updatedRoute.TotalFeesMsat > routeFeeLimitMsat {
							lastErr = fmt.Errorf("route fee exceeds limit")
							continue
						}
						applyMppRecord(updatedRoute, paymentAddr, routeAmount)
						updatedRouteMax := s.maxAmountOnRouteSat(attemptCtx, updatedRoute, selfPubkey)
						_, retryErr := s.lnd.SendToRoute(attemptCtx, paymentHash, updatedRoute)
						if retryErr == nil {
							cancelAttempt()
							resetAutoNoPath()
							feePaidSat := msatToSatCeil(updatedRoute.TotalFeesMsat)
							if feePaidSat == 0 && probeFeeMsat > 0 {
								feePaidSat = msatToSatCeil(probeFeeMsat)
							}
							attemptIndex++
							_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, routeAmount, routeFeeLimitPpm, feePaidSat, "succeeded", paymentHash, "")
							s.recordPairSuccess(ctx, source.ChannelID, targetChannelID, routeAmount, routeFeeLimitPpm, feePaidSat)
							_ = s.applyRebalanceLedger(ctx, targetChannelID, routeAmount, feePaidSat)
							_ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
							return true, false, updatedRouteMax, false, updatedRoute, routeAmount
						}
						lastErr = retryErr
						continue
					}
				}

				if routeFailure.Code == lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE &&
					feeCfg.AmountProbeSteps > 0 &&
					int(routeFailure.FailureSourceIndex) == len(activeRoute.Hops)-2 {
					maxAmount, probeErr := s.probeRoute(attemptCtx, activeRoute, routeAmount, minProbeSat, feeCfg.AmountProbeSteps, targetPolicy, sourcePolicy, feeCfg)
					if probeErr == nil && maxAmount > 0 {
						if feeCfg.MinSplitEnabled && minExecuteSat > 0 && maxAmount < minExecuteSat {
							lastErr = errors.New("probe amount below execute minimum")
							continue
						}
						retryFeeMsat, retryErr := calcFeeLimitMsat(maxAmount*1000, targetPolicy, &sourcePolicy, feeCfg)
						if retryErr == nil && retryFeeMsat > 0 {
							retryFeePpm := feeMsatToPpm(retryFeeMsat, maxAmount)
							_, retryHash, retryAddr, retryInvErr := s.createRebalanceInvoice(attemptCtx, maxAmount, jobID, source.ChannelID, targetChannelID)
							if retryInvErr == nil {
								rebuilt, rebuildErr := s.rebuildRouteForAmount(attemptCtx, activeRoute, maxAmount)
								if rebuildErr == nil {
									if retryFeeMsat > 0 && rebuilt.TotalFeesMsat > retryFeeMsat {
										attemptIndex++
										_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, maxAmount, retryFeePpm, 0, "failed", retryHash, "route fee exceeds limit")
										s.recordPairFailure(ctx, source.ChannelID, targetChannelID, "route fee exceeds limit")
										continue
									}
									applyMppRecord(rebuilt, retryAddr, maxAmount)
									probeRouteMax := s.maxAmountOnRouteSat(attemptCtx, rebuilt, selfPubkey)
									_, retrySendErr := s.lnd.SendToRoute(attemptCtx, retryHash, rebuilt)
									if retrySendErr == nil {
										cancelAttempt()
										resetAutoNoPath()
										feePaidSat := msatToSatCeil(rebuilt.TotalFeesMsat)
										if feePaidSat == 0 && rebuilt.TotalFeesMsat > 0 {
											feePaidSat = msatToSatCeil(rebuilt.TotalFeesMsat)
										}
										attemptIndex++
										_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, maxAmount, retryFeePpm, feePaidSat, "succeeded", retryHash, "")
										s.recordPairSuccess(ctx, source.ChannelID, targetChannelID, maxAmount, retryFeePpm, feePaidSat)
										_ = s.applyRebalanceLedger(ctx, targetChannelID, maxAmount, feePaidSat)
										_ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
										return true, false, probeRouteMax, false, rebuilt, maxAmount
									}
									attemptIndex++
									_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, maxAmount, retryFeePpm, 0, "failed", retryHash, retrySendErr.Error())
									s.recordPairFailure(ctx, source.ChannelID, targetChannelID, retrySendErr.Error())
									lastErr = retrySendErr
								}
							}
						}
					}
				}
			}
			lastErr = err
		}

		cancelAttempt()
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				finishOnTimeout()
			} else {
				s.finishJob(jobID, "cancelled", "cancelled")
			}
			return false, true, 0, false, nil, 0
		}
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			timeoutHash := lastPaymentHash
			timeoutFeePpm := lastFeeLimitPpm
			timeoutAmount := lastAmountTry
			if timeoutFeePpm <= 0 {
				timeoutFeePpm = feeLimitPpm
			}
			if timeoutAmount <= 0 {
				timeoutAmount = amountTry
			}
			if ok, sent := reconcileTimeoutPayment(timeoutHash, timeoutFeePpm, source, timeoutAmount); ok {
				return true, false, 0, false, nil, sent
			}
			attemptIndex++
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, timeoutAmount, timeoutFeePpm, 0, "failed", timeoutHash, "attempt timeout")
			s.recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
			return false, false, 0, true, nil, 0
		}
		failReason := ""
		if lastErr != nil {
			failReason = lastErr.Error()
		} else if lastRouteFeeMsat > 0 {
			failReason = "all routes failed"
		}
		if logRouteFailure {
			failAmount := amountTry
			failFeePpm := feeLimitPpm
			if lastAmountTry > 0 {
				failAmount = lastAmountTry
			}
			if lastFeeLimitPpm > 0 {
				failFeePpm = lastFeeLimitPpm
			}
			attemptIndex++
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, failAmount, failFeePpm, 0, "failed", lastPaymentHash, failReason)
			s.recordPairFailure(ctx, source.ChannelID, targetChannelID, failReason)
		}
		return false, false, 0, false, nil, 0
	}

	attemptPaymentWithRoute := func(source RebalanceChannel, baseRoute *lnrpc.Route, amountTry int64, feeLimitMsat int64, logRouteFailure bool) (bool, bool, int64, bool, *lnrpc.Route, int64) {
		attemptedAny = true
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				finishOnTimeout()
			} else {
				s.finishJob(jobID, "cancelled", "cancelled")
			}
			return false, true, 0, false, nil, 0
		}
		if amountTry <= 0 {
			return false, false, 0, false, nil, 0
		}
		if feeCfg.MinSplitEnabled && minExecuteSat > 0 && amountTry < minExecuteSat {
			return false, false, 0, false, nil, 0
		}
		if baseRoute == nil {
			return false, false, 0, false, nil, 0
		}
		sourcePolicy := lndclient.ChannelPolicySnapshot{
			FeeRatePpm:  source.OutgoingFeePpm,
			BaseFeeMsat: source.OutgoingBaseMsat,
		}
		if feeLimitMsat <= 0 {
			maxFeeMsat, err := calcFeeLimitMsat(amountTry*1000, targetPolicy, &sourcePolicy, feeCfg)
			if err != nil || maxFeeMsat <= 0 {
				return false, false, 0, false, nil, 0
			}
			feeLimitMsat = maxFeeMsat
		}
		feeLimitPpm := feeMsatToPpm(feeLimitMsat, amountTry)

		attemptCtx := ctx
		cancelAttempt := func() {}
		if attemptTimeoutSec > 0 {
			attemptCtx, cancelAttempt = context.WithTimeout(ctx, time.Duration(attemptTimeoutSec)*time.Second)
		}

		route, err := s.rebuildRouteForAmount(attemptCtx, baseRoute, amountTry)
		if err != nil {
			cancelAttempt()
			if logRouteFailure {
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", err.Error())
				s.recordPairFailure(ctx, source.ChannelID, targetChannelID, err.Error())
			}
			return false, false, 0, false, nil, 0
		}
		routeFeeMsat := int64(0)
		if route.TotalFeesMsat > 0 {
			routeFeeMsat = route.TotalFeesMsat
		} else if route.TotalFees > 0 {
			routeFeeMsat = route.TotalFees * 1000
		}
		if feeLimitMsat > 0 && routeFeeMsat > feeLimitMsat {
			cancelAttempt()
			if logRouteFailure {
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", "route fee exceeds limit")
				s.recordPairFailure(ctx, source.ChannelID, targetChannelID, "route fee exceeds limit")
			}
			return false, false, 0, false, nil, 0
		}

		routeMaxSat := s.maxAmountOnRouteSat(attemptCtx, route, selfPubkey)
		_, paymentHash, paymentAddr, err := s.createRebalanceInvoice(attemptCtx, amountTry, jobID, source.ChannelID, targetChannelID)
		if err != nil {
			cancelAttempt()
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					finishOnTimeout()
				} else {
					s.finishJob(jobID, "cancelled", "cancelled")
				}
				return false, true, 0, false, nil, 0
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", "attempt timeout")
				s.recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
				return false, false, 0, true, nil, 0
			}
			attemptIndex++
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", err.Error())
			s.recordPairFailure(ctx, source.ChannelID, targetChannelID, err.Error())
			return false, false, 0, false, nil, 0
		}

		applyMppRecord(route, paymentAddr, amountTry)
		_, err = s.lnd.SendToRoute(attemptCtx, paymentHash, route)
		if err == nil {
			cancelAttempt()
			resetAutoNoPath()
			feePaidSat := msatToSatCeil(routeFeeMsat)
			attemptIndex++
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, feePaidSat, "succeeded", paymentHash, "")
			s.recordPairSuccess(ctx, source.ChannelID, targetChannelID, amountTry, feeLimitPpm, feePaidSat)
			_ = s.applyRebalanceLedger(ctx, targetChannelID, amountTry, feePaidSat)
			_ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
			return true, false, routeMaxSat, false, route, amountTry
		}

		var lastErr error
		var routeFailure lndclient.RouteFailureError
		if errors.As(err, &routeFailure) && routeFailure.Failure != nil {
			if routeFailure.Code == lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE {
				noteRouteFailure(route, routeFailure.FailureSourceIndex)
			}
			failureIdx := int(routeFailure.FailureSourceIndex) - 1
			if (routeFailure.Code == lnrpc.Failure_FEE_INSUFFICIENT || routeFailure.Code == lnrpc.Failure_INCORRECT_CLTV_EXPIRY) &&
				failureIdx >= 0 && failureIdx < len(route.Hops) {
				updatedRoute, rebuildErr := s.rebuildRouteForAmount(attemptCtx, route, amountTry)
				if rebuildErr == nil && !compareHops(route.Hops[failureIdx], updatedRoute.Hops[failureIdx]) {
					if feeLimitMsat > 0 && updatedRoute.TotalFeesMsat > feeLimitMsat {
						lastErr = fmt.Errorf("route fee exceeds limit")
					} else {
						applyMppRecord(updatedRoute, paymentAddr, amountTry)
						updatedRouteMax := s.maxAmountOnRouteSat(attemptCtx, updatedRoute, selfPubkey)
						_, retryErr := s.lnd.SendToRoute(attemptCtx, paymentHash, updatedRoute)
						if retryErr == nil {
							cancelAttempt()
							resetAutoNoPath()
							feePaidSat := msatToSatCeil(updatedRoute.TotalFeesMsat)
							attemptIndex++
							_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, feePaidSat, "succeeded", paymentHash, "")
							s.recordPairSuccess(ctx, source.ChannelID, targetChannelID, amountTry, feeLimitPpm, feePaidSat)
							_ = s.applyRebalanceLedger(ctx, targetChannelID, amountTry, feePaidSat)
							_ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
							return true, false, updatedRouteMax, false, updatedRoute, amountTry
						}
						lastErr = retryErr
					}
				} else if rebuildErr != nil {
					lastErr = rebuildErr
				}
			}
		}
		if lastErr == nil {
			lastErr = err
		}

		cancelAttempt()
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				finishOnTimeout()
			} else {
				s.finishJob(jobID, "cancelled", "cancelled")
			}
			return false, true, 0, false, nil, 0
		}
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			if ok, sent := reconcileTimeoutPayment(paymentHash, feeLimitPpm, source, amountTry); ok {
				return true, false, 0, false, nil, sent
			}
			attemptIndex++
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", paymentHash, "attempt timeout")
			s.recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
			return false, false, 0, true, nil, 0
		}
		failReason := ""
		if lastErr != nil {
			failReason = lastErr.Error()
		} else if routeFeeMsat > 0 {
			failReason = "route failed"
		}
		if logRouteFailure {
			attemptIndex++
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", paymentHash, failReason)
			s.recordPairFailure(ctx, source.ChannelID, targetChannelID, failReason)
		}
		return false, false, 0, false, nil, 0
	}

	applySuccess := func(amountSat int64, routeMax int64, routeCap *int64, sourceRemaining *int64) bool {
		anySuccess = true
		remaining -= amountSat
		*sourceRemaining -= amountSat
		if *sourceRemaining < 0 {
			*sourceRemaining = 0
		}
		if cfg.AmountProbeAdaptive {
			adaptiveMaxAmount = amountSat
		}
		if routeMax > 0 {
			if routeMax < amountSat {
				*routeCap = amountSat
			} else {
				*routeCap = routeMax
			}
		}
		if remaining <= 0 {
			s.finishJob(jobID, "succeeded", "")
			s.broadcast(RebalanceEvent{Type: "job", JobID: jobID, Status: "succeeded"})
			return true
		}
		return false
	}

	rapidRebalance := func(source RebalanceChannel, baseRoute *lnrpc.Route, startAmount int64, startRouteCap int64, sourceRemaining *int64) (bool, bool) {
		current := startAmount
		if current <= 0 || baseRoute == nil {
			return false, false
		}
		routeCap := startRouteCap
		if routeCap <= 0 {
			routeCap = s.maxAmountOnRouteSat(ctx, baseRoute, selfPubkey)
		}
		minAmount := minExecuteSat
		if minAmount <= 0 {
			minAmount = 1
		}
		if current > 0 && current < minAmount {
			minAmount = current
		}
		phase := "increase"
		consecutiveFailures := 0
		refreshAfterFailures := 2

		for remaining > 0 && *sourceRemaining > 0 {
			maxCap := remaining
			if *sourceRemaining < maxCap {
				maxCap = *sourceRemaining
			}
			if routeCap > 0 && routeCap < maxCap {
				maxCap = routeCap
			}
			if maxCap <= 0 {
				break
			}
			if current > maxCap {
				current = maxCap
			}
			if current <= 0 {
				break
			}

			switch phase {
			case "increase":
				next := current * 2
				if next < current {
					next = current
				}
				if next > maxCap {
					next = maxCap
				}
				if next <= current {
					phase = "steady"
					continue
				}
				success, fatal, routeMax, timedOut, _, amountSent := attemptPaymentWithRoute(source, baseRoute, next, 0, true)
				if fatal {
					return false, true
				}
				if timedOut {
					return false, false
				}
				if success {
					consecutiveFailures = 0
					if applySuccess(amountSent, routeMax, &routeCap, sourceRemaining) {
						return true, false
					}
					current = next
					continue
				}
				consecutiveFailures++
				if consecutiveFailures >= refreshAfterFailures {
					success, fatal, routeMax, timedOut, refreshedRoute, refreshedAmount := attemptPayment(source, next, 0, true)
					if fatal {
						return false, true
					}
					if timedOut {
						return false, false
					}
					if success {
						consecutiveFailures = 0
						if refreshedRoute != nil {
							baseRoute = refreshedRoute
						}
						if applySuccess(refreshedAmount, routeMax, &routeCap, sourceRemaining) {
							return true, false
						}
						current = refreshedAmount
						continue
					}
					consecutiveFailures = 0
				}
				phase = "steady"

			case "steady":
				success, fatal, routeMax, timedOut, _, amountSent := attemptPaymentWithRoute(source, baseRoute, current, 0, true)
				if fatal {
					return false, true
				}
				if timedOut {
					return false, false
				}
				if success {
					consecutiveFailures = 0
					if applySuccess(amountSent, routeMax, &routeCap, sourceRemaining) {
						return true, false
					}
					continue
				}
				consecutiveFailures++
				if consecutiveFailures >= refreshAfterFailures {
					success, fatal, routeMax, timedOut, refreshedRoute, refreshedAmount := attemptPayment(source, current, 0, true)
					if fatal {
						return false, true
					}
					if timedOut {
						return false, false
					}
					if success {
						consecutiveFailures = 0
						if refreshedRoute != nil {
							baseRoute = refreshedRoute
						}
						if applySuccess(refreshedAmount, routeMax, &routeCap, sourceRemaining) {
							return true, false
						}
						current = refreshedAmount
						continue
					}
					consecutiveFailures = 0
				}
				phase = "decrease"

			case "decrease":
				next := current / 2
				if minAmount > 0 && next < minAmount {
					next = minAmount
				}
				if next <= 0 || next >= current {
					return false, false
				}
				current = next
				if current > maxCap {
					current = maxCap
				}
				if current <= 0 {
					return false, false
				}
				success, fatal, routeMax, timedOut, _, amountSent := attemptPaymentWithRoute(source, baseRoute, current, 0, true)
				if fatal {
					return false, true
				}
				if timedOut {
					return false, false
				}
				if success {
					consecutiveFailures = 0
					if applySuccess(amountSent, routeMax, &routeCap, sourceRemaining) {
						return true, false
					}
					phase = "increase"
					continue
				}
				consecutiveFailures++
				if consecutiveFailures >= refreshAfterFailures {
					success, fatal, routeMax, timedOut, refreshedRoute, refreshedAmount := attemptPayment(source, current, 0, true)
					if fatal {
						return false, true
					}
					if timedOut {
						return false, false
					}
					if success {
						consecutiveFailures = 0
						if refreshedRoute != nil {
							baseRoute = refreshedRoute
						}
						if applySuccess(refreshedAmount, routeMax, &routeCap, sourceRemaining) {
							return true, false
						}
						current = refreshedAmount
						phase = "increase"
						continue
					}
					consecutiveFailures = 0
				}
			}
		}

		return false, false
	}

	type mppShardAttemptResult struct {
		ShardIndex      int
		Source          RebalanceChannel
		AmountRequested int64
		AmountSent      int64
		FeeLimitPpm     int64
		FeePaidSat      int64
		RouteMaxSat     int64
		PaymentHash     string
		FailReason      string
		Attempted       bool
		Succeeded       bool
		TimedOut        bool
		Fatal           bool
	}

	formatMppShardFailReason := func(reason string) string {
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" {
			trimmed = "failed"
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "mpp shard:") {
			return trimmed
		}
		return "mpp shard: " + trimmed
	}

	attemptMppShard := func(roundCtx context.Context, source RebalanceChannel, amountTry int64) mppShardAttemptResult {
		result := mppShardAttemptResult{
			Source:          source,
			AmountRequested: amountTry,
			AmountSent:      amountTry,
			Attempted:       true,
		}
		if amountTry <= 0 {
			result.Attempted = false
			return result
		}
		if feeCfg.MinSplitEnabled && minExecuteSat > 0 && amountTry < minExecuteSat {
			result.Attempted = false
			return result
		}
		if roundCtx.Err() != nil {
			if ctx.Err() != nil {
				result.Fatal = true
			} else {
				result.TimedOut = true
				result.FailReason = "mpp round timeout"
			}
			return result
		}

		sourcePolicy := lndclient.ChannelPolicySnapshot{
			FeeRatePpm:  source.OutgoingFeePpm,
			BaseFeeMsat: source.OutgoingBaseMsat,
		}
		feeLimitMsat, feeErr := calcFeeLimitMsat(amountTry*1000, targetPolicy, &sourcePolicy, feeCfg)
		if feeErr != nil || feeLimitMsat <= 0 {
			result.FailReason = "fee cap zero"
			return result
		}
		result.FeeLimitPpm = feeMsatToPpm(feeLimitMsat, amountTry)

		attemptCtx := roundCtx
		cancelAttempt := func() {}
		if attemptTimeoutSec > 0 {
			attemptCtx, cancelAttempt = context.WithTimeout(roundCtx, time.Duration(attemptTimeoutSec)*time.Second)
		}
		defer cancelAttempt()

		routes, err := s.lnd.QueryRoutes(attemptCtx, selfPubkey, amountTry, source.ChannelID, targetSnapshot.RemotePubkey, feeLimitMsat, 3, nil, nil)
		if err != nil {
			if ctx.Err() != nil {
				result.Fatal = true
				result.FailReason = "cancelled"
				return result
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || errors.Is(roundCtx.Err(), context.DeadlineExceeded) {
				result.TimedOut = true
				result.FailReason = "attempt timeout"
				return result
			}
			result.FailReason = err.Error()
			return result
		}

		var route *lnrpc.Route
		for _, candidate := range routes {
			if candidate != nil {
				route = candidate
				break
			}
		}
		if route == nil {
			result.FailReason = "no route returned"
			return result
		}

		routeAmount := amountTry
		if feeCfg.AmountProbeSteps > 0 {
			maxAmount, probeErr := s.probeRoute(attemptCtx, route, amountTry, minProbeSat, feeCfg.AmountProbeSteps, targetPolicy, sourcePolicy, feeCfg)
			if probeErr != nil {
				result.FailReason = probeErr.Error()
				return result
			}
			if maxAmount <= 0 {
				result.FailReason = "probe returned no amount"
				return result
			}
			if maxAmount < routeAmount {
				routeAmount = maxAmount
			}
		}
		if feeCfg.MinSplitEnabled && minExecuteSat > 0 && routeAmount < minExecuteSat {
			result.FailReason = "probe amount below execute minimum"
			return result
		}
		if routeAmount <= 0 {
			result.FailReason = "invalid shard amount"
			return result
		}

		if routeAmount != amountTry {
			rebuilt, rebuildErr := s.rebuildRouteForAmount(attemptCtx, route, routeAmount)
			if rebuildErr != nil {
				result.FailReason = rebuildErr.Error()
				return result
			}
			route = rebuilt
			result.AmountSent = routeAmount
			feeLimitMsat, feeErr = calcFeeLimitMsat(routeAmount*1000, targetPolicy, &sourcePolicy, feeCfg)
			if feeErr != nil || feeLimitMsat <= 0 {
				result.FailReason = "fee cap zero"
				return result
			}
			result.FeeLimitPpm = feeMsatToPpm(feeLimitMsat, routeAmount)
		}

		routeFeeMsat := int64(0)
		if route.TotalFeesMsat > 0 {
			routeFeeMsat = route.TotalFeesMsat
		} else if route.TotalFees > 0 {
			routeFeeMsat = route.TotalFees * 1000
		}
		if feeLimitMsat > 0 && routeFeeMsat > feeLimitMsat {
			result.FailReason = "route fee exceeds limit"
			return result
		}

		_, paymentHash, paymentAddr, invErr := s.createRebalanceInvoice(attemptCtx, routeAmount, jobID, source.ChannelID, targetChannelID)
		if invErr != nil {
			if ctx.Err() != nil {
				result.Fatal = true
				result.FailReason = "cancelled"
				return result
			}
			if errors.Is(invErr, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || errors.Is(roundCtx.Err(), context.DeadlineExceeded) {
				result.TimedOut = true
				result.FailReason = "attempt timeout"
				return result
			}
			result.FailReason = invErr.Error()
			return result
		}
		result.PaymentHash = paymentHash
		applyMppRecord(route, paymentAddr, routeAmount)
		result.RouteMaxSat = s.maxAmountOnRouteSat(attemptCtx, route, selfPubkey)

		_, sendErr := s.lnd.SendToRoute(attemptCtx, paymentHash, route)
		if sendErr != nil {
			if ctx.Err() != nil {
				result.Fatal = true
				result.FailReason = "cancelled"
				return result
			}
			if errors.Is(sendErr, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || errors.Is(roundCtx.Err(), context.DeadlineExceeded) {
				result.TimedOut = true
				result.FailReason = "attempt timeout"
				return result
			}
			result.FailReason = sendErr.Error()
			return result
		}

		result.Succeeded = true
		result.AmountSent = routeAmount
		result.FeePaidSat = msatToSatCeil(routeFeeMsat)
		return result
	}

	runMppPrepass := func() (bool, bool) {
		if !shouldRunMppExecute(feeCfg, jobSource) {
			return false, false
		}
		if remaining <= 0 {
			return true, false
		}
		refreshSourceAvailability(false)

		allowedSources := make([]RebalanceChannel, 0, len(sources))
		sourceByID := make(map[uint64]RebalanceChannel, len(sources))
		for _, source := range sources {
			availableCap := sourceAvailable[source.ChannelID]
			if availableCap <= 0 {
				continue
			}
			if useRecentFailureCache {
				if stat, ok := pairStats[source.ChannelID]; ok {
					if !stat.LastFailAt.IsZero() && time.Since(stat.LastFailAt) <= pairFailTTL && (stat.LastSuccessAt.IsZero() || stat.LastSuccessAt.Before(stat.LastFailAt)) {
						continue
					}
				}
			}
			cappedSource := source
			cappedSource.MaxSourceSat = availableCap
			allowedSources = append(allowedSources, cappedSource)
			sourceByID[source.ChannelID] = cappedSource
		}
		if len(allowedSources) == 0 {
			return false, false
		}

		plan := buildMppShadowPlan(remaining, allowedSources, feeCfg)
		if plan.PlannedShards <= 0 {
			return false, false
		}

		roundTimeoutSec := feeCfg.MppRoundTimeoutSec
		if roundTimeoutSec <= 0 {
			roundTimeoutSec = 20
		}
		roundCtx, cancelRound := context.WithTimeout(ctx, time.Duration(roundTimeoutSec)*time.Second)
		defer cancelRound()
		parallelism := feeCfg.MppParallelism
		if parallelism <= 0 {
			parallelism = 1
		}
		if parallelism > plan.PlannedShards {
			parallelism = plan.PlannedShards
		}

		type shardTask struct {
			ShardIndex int
			Source     RebalanceChannel
			AmountSat  int64
		}
		tasks := make([]shardTask, 0, plan.PlannedShards)
		planSourceRemaining := make(map[uint64]int64, len(allowedSources))
		for _, source := range allowedSources {
			planSourceRemaining[source.ChannelID] = source.MaxSourceSat
		}
		remainingForPlan := remaining
		for idx, shard := range plan.Shards {
			if remainingForPlan <= 0 {
				break
			}
			source, ok := sourceByID[shard.SourceChannelID]
			if !ok {
				continue
			}
			capLeft := planSourceRemaining[source.ChannelID]
			if capLeft <= 0 {
				continue
			}
			amountTry := shard.AmountSat
			if amountTry > remainingForPlan {
				amountTry = remainingForPlan
			}
			if amountTry > capLeft {
				amountTry = capLeft
			}
			if amountTry <= 0 {
				continue
			}
			if minExecuteSat > 0 && amountTry < minExecuteSat {
				continue
			}
			tasks = append(tasks, shardTask{
				ShardIndex: idx,
				Source:     source,
				AmountSat:  amountTry,
			})
			planSourceRemaining[source.ChannelID] -= amountTry
			remainingForPlan -= amountTry
		}
		if len(tasks) == 0 {
			return false, false
		}

		sem := make(chan struct{}, parallelism)
		resultsCh := make(chan mppShardAttemptResult, len(tasks))
		var wg sync.WaitGroup
		for _, task := range tasks {
			task := task
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				res := attemptMppShard(roundCtx, task.Source, task.AmountSat)
				res.ShardIndex = task.ShardIndex
				resultsCh <- res
			}()
		}
		wg.Wait()
		close(resultsCh)

		results := make([]mppShardAttemptResult, 0, len(tasks))
		for res := range resultsCh {
			results = append(results, res)
		}
		sort.Slice(results, func(i, j int) bool {
			return results[i].ShardIndex < results[j].ShardIndex
		})

		attemptedShards := 0
		succeededShards := 0
		sourceSuccessRemaining := make(map[uint64]int64, len(allowedSources))
		for _, source := range allowedSources {
			sourceSuccessRemaining[source.ChannelID] = source.MaxSourceSat
		}

		for _, res := range results {
			if res.Fatal {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					finishOnTimeout()
				} else {
					s.finishJob(jobID, "cancelled", "cancelled")
				}
				return false, true
			}
			if !res.Attempted {
				continue
			}
			attemptedAny = true
			attemptedShards++

			attemptIndex++
			attemptAmount := res.AmountRequested
			if res.AmountSent > 0 {
				attemptAmount = res.AmountSent
			}
			if res.Succeeded {
				_ = s.insertAttempt(ctx, jobID, attemptIndex, res.Source.ChannelID, attemptAmount, res.FeeLimitPpm, res.FeePaidSat, "succeeded", res.PaymentHash, "")
				s.recordPairSuccess(ctx, res.Source.ChannelID, targetChannelID, attemptAmount, res.FeeLimitPpm, res.FeePaidSat)
				_ = s.applyRebalanceLedger(ctx, targetChannelID, attemptAmount, res.FeePaidSat)
				_ = s.addBudgetSpend(ctx, res.FeePaidSat, jobSource)
				succeededShards++

				routeCap := int64(0)
				sourceCap := sourceSuccessRemaining[res.Source.ChannelID]
				if applySuccess(attemptAmount, res.RouteMaxSat, &routeCap, &sourceCap) {
					sourceSuccessRemaining[res.Source.ChannelID] = sourceCap
					sourceAvailable[res.Source.ChannelID] = sourceCap
					if s.logger != nil {
						s.logger.Printf("rebalance mpp execute: job=%d completed via parallel prepass shards=%d/%d", jobID, succeededShards, attemptedShards)
					}
					return true, false
				}
				sourceSuccessRemaining[res.Source.ChannelID] = sourceCap
				sourceAvailable[res.Source.ChannelID] = sourceCap
				continue
			}

			failReason := res.FailReason
			if strings.TrimSpace(failReason) == "" {
				if res.TimedOut {
					failReason = "attempt timeout"
				} else {
					failReason = "mpp shard failed"
				}
			}
			shardFailReason := formatMppShardFailReason(failReason)
			_ = s.insertAttempt(ctx, jobID, attemptIndex, res.Source.ChannelID, attemptAmount, res.FeeLimitPpm, 0, "failed", res.PaymentHash, shardFailReason)
			s.recordPairFailure(ctx, res.Source.ChannelID, targetChannelID, shardFailReason)
		}

		if s.logger != nil {
			s.logger.Printf(
				"rebalance mpp execute: job=%d parallel prepass attempted_shards=%d succeeded_shards=%d remaining=%d (fallback legacy)",
				jobID,
				attemptedShards,
				succeededShards,
				remaining,
			)
		}
		return false, false
	}

	if finished, fatal := runMppPrepass(); fatal {
		return
	} else if finished {
		return
	}

	passDelay := 5 * time.Second
	for {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				finishOnTimeout()
			} else {
				s.finishJob(jobID, "cancelled", "cancelled")
			}
			return
		}

		refreshSourceAvailability(false)
		remainingBefore := remaining

		for _, source := range sources {
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					finishOnTimeout()
				} else {
					s.finishJob(jobID, "cancelled", "cancelled")
				}
				return
			}

			if useRecentFailureCache {
				if stat, ok := pairStats[source.ChannelID]; ok {
					if !stat.LastFailAt.IsZero() && time.Since(stat.LastFailAt) <= pairFailTTL && (stat.LastSuccessAt.IsZero() || stat.LastSuccessAt.Before(stat.LastFailAt)) {
						skippedByCache++
						continue
					}
				}
			}

			maxFromSource := sourceAvailable[source.ChannelID]
			if maxFromSource <= 0 {
				continue
			}
			sourceRemaining := maxFromSource

			sendAmount := remaining
			if sendAmount > sourceRemaining {
				sendAmount = sourceRemaining
			}
			probeCap := computeProbeCap(remaining, startAmountSat, cfg.MaxAmountSat)
			if probeCap > 0 && probeCap < sendAmount {
				sendAmount = probeCap
			}
			if cfg.AmountProbeAdaptive && adaptiveMaxAmount > 0 {
				capAmount := adaptiveMaxAmount * 2
				if capAmount > 0 && capAmount < sendAmount {
					sendAmount = capAmount
				}
			}
			if minExecuteSat > 0 && sendAmount < minExecuteSat {
				continue
			}

			if source.ChannelID == warmSourceID && warmAmount > 0 && warmFeePpm > 0 {
				warmTry := warmAmount
				if warmTry > remaining {
					warmTry = remaining
				}
				if warmTry > sourceRemaining {
					warmTry = sourceRemaining
				}
				if warmTry > sendAmount {
					warmTry = sendAmount
				}
				if minExecuteSat > 0 && warmTry < minExecuteSat {
					warmTry = 0
				}
				if warmTry > 0 {
					warmFeeLimitMsat := ppmToFeeLimitMsat(warmTry, warmFeePpm)
					success, fatal, routeMax, timedOut, usedRoute, amountSent := attemptPayment(source, warmTry, warmFeeLimitMsat, true)
					if fatal {
						return
					}
					if timedOut {
						continue
					}
					if success {
						routeCap := int64(0)
						if applySuccess(amountSent, routeMax, &routeCap, &sourceRemaining) {
							sourceAvailable[source.ChannelID] = sourceRemaining
							return
						}
						if usedRoute != nil {
							finished, fatal := rapidRebalance(source, usedRoute, amountSent, routeCap, &sourceRemaining)
							sourceAvailable[source.ChannelID] = sourceRemaining
							if fatal {
								return
							}
							if finished {
								return
							}
						}
						sourceAvailable[source.ChannelID] = sourceRemaining
						continue
					}
				}
			}

			feeSteps := cfg.FeeLadderSteps
			if feeSteps <= 0 {
				feeSteps = 1
			}

			probeAmount := startAmountSat
			if probeAmount <= 0 {
				probeAmount = sendAmount
			}
			if feeCfg.MinSplitEnabled && minExecuteSat > 0 && probeAmount < minExecuteSat {
				probeAmount = minExecuteSat
			}
			if probeAmount > sendAmount {
				probeAmount = sendAmount
			}
			if probeAmount <= 0 {
				continue
			}
			if minProbeSat > 0 && probeAmount < minProbeSat {
				continue
			}

			sourceTimedOut := false
			sourcePolicy := lndclient.ChannelPolicySnapshot{
				FeeRatePpm:  source.OutgoingFeePpm,
				BaseFeeMsat: source.OutgoingBaseMsat,
			}
			maxFeeMsat, feeErr := calcFeeLimitMsat(probeAmount*1000, targetPolicy, &sourcePolicy, feeCfg)
			if feeErr != nil || maxFeeMsat <= 0 {
				continue
			}
			for step := 1; step <= feeSteps; step++ {
				feeLimitMsat := calcFeeStepMsat(maxFeeMsat, feeSteps, step)
				if feeLimitMsat <= 0 {
					feeLimitMsat = maxFeeMsat
				}

				success, fatal, routeMax, timedOut, usedRoute, amountSent := attemptPayment(source, probeAmount, feeLimitMsat, true)
				if fatal {
					return
				}
				if timedOut {
					sourceTimedOut = true
					break
				}
				if !success {
					continue
				}
				routeCap := int64(0)
				if applySuccess(amountSent, routeMax, &routeCap, &sourceRemaining) {
					sourceAvailable[source.ChannelID] = sourceRemaining
					return
				}
				if usedRoute != nil {
					finished, fatal := rapidRebalance(source, usedRoute, amountSent, routeCap, &sourceRemaining)
					sourceAvailable[source.ChannelID] = sourceRemaining
					if fatal {
						return
					}
					if finished {
						return
					}
				}
				sourceAvailable[source.ChannelID] = sourceRemaining
				break
			}
			if sourceTimedOut {
				sourceAvailable[source.ChannelID] = sourceRemaining
				continue
			}
			sourceAvailable[source.ChannelID] = sourceRemaining
		}

		if remaining <= 0 {
			return
		}
		if remaining == remainingBefore {
			break
		}
		if !sleepWithContext(passDelay) {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				finishOnTimeout()
			} else {
				s.finishJob(jobID, "cancelled", "cancelled")
			}
			return
		}
	}

	if anySuccess {
		s.finishJob(jobID, "partial", fmt.Sprintf("remaining %d sats", remaining))
		s.broadcast(RebalanceEvent{Type: "job", JobID: jobID, Status: "partial"})
		return
	}

	if !attemptedAny && skippedByCache > 0 {
		s.finishJob(jobID, "failed", "all sources skipped (recent failures)")
		return
	}
	s.finishJob(jobID, "failed", "all sources failed")
}

func (s *RebalanceService) createRebalanceInvoice(ctx context.Context, amount int64, jobID int64, sourceID uint64, targetID uint64) (string, string, []byte, error) {
	if s.lnd == nil {
		return "", "", nil, errors.New("lnd unavailable")
	}
	memo := fmt.Sprintf("rebalance:%d:%d:%d", jobID, sourceID, targetID)
	inv, err := s.lnd.CreateInvoice(ctx, amount, memo, 3600, nil)
	if err != nil {
		return "", "", nil, err
	}
	return inv.PaymentRequest, inv.PaymentHash, inv.PaymentAddr, nil
}

func (s *RebalanceService) maxAmountOnRouteSat(ctx context.Context, route *lnrpc.Route, selfPubkey string) int64 {
	if s.lnd == nil || route == nil || len(route.Hops) == 0 {
		return 0
	}
	prev := strings.TrimSpace(selfPubkey)
	if prev == "" {
		return 0
	}
	minMsat := int64(0)
	for _, hop := range route.Hops {
		if hop == nil {
			continue
		}
		next := strings.TrimSpace(hop.PubKey)
		if next == "" {
			break
		}
		maxMsat, err := s.lnd.GetMaxHtlcMsat(ctx, hop.ChanId, prev, next)
		if err != nil || maxMsat == 0 {
			prev = next
			continue
		}
		if minMsat == 0 || int64(maxMsat) < minMsat {
			minMsat = int64(maxMsat)
		}
		prev = next
	}
	if minMsat <= 0 {
		return 0
	}
	return minMsat / 1000
}

func applyMppRecord(route *lnrpc.Route, paymentAddr []byte, amountSat int64) {
	if route == nil || len(route.Hops) == 0 || len(paymentAddr) == 0 || amountSat <= 0 {
		return
	}
	lastHop := route.Hops[len(route.Hops)-1]
	if lastHop == nil {
		return
	}
	lastHop.MppRecord = &lnrpc.MPPRecord{
		PaymentAddr:  paymentAddr,
		TotalAmtMsat: amountSat * 1000,
	}
}

func (s *RebalanceService) rebuildRouteForAmount(ctx context.Context, route *lnrpc.Route, amountSat int64) (*lnrpc.Route, error) {
	if s.lnd == nil {
		return nil, errors.New("lnd unavailable")
	}
	if route == nil || len(route.Hops) == 0 {
		return nil, errors.New("route required")
	}
	hopPubkeys := make([]string, 0, len(route.Hops))
	for _, hop := range route.Hops {
		if hop == nil || strings.TrimSpace(hop.PubKey) == "" {
			return nil, errors.New("invalid hop pubkey")
		}
		hopPubkeys = append(hopPubkeys, hop.PubKey)
	}
	return s.lnd.BuildRoute(ctx, amountSat, route.Hops[0].ChanId, hopPubkeys)
}

func compareHops(hop1 *lnrpc.Hop, hop2 *lnrpc.Hop) bool {
	if hop1 == nil || hop2 == nil {
		return false
	}
	return hop1.ChanId == hop2.ChanId &&
		hop1.FeeMsat == hop2.FeeMsat &&
		hop1.Expiry == hop2.Expiry
}

func isNoPathError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unable to find a path") || strings.Contains(msg, "no route")
}

func (s *RebalanceService) applyRebalanceLedger(ctx context.Context, channelID uint64, amountSat int64, costSat int64) error {
	if s.db == nil {
		return nil
	}
	if amountSat <= 0 {
		return nil
	}
	now := time.Now().UTC()
	_, err := s.db.Exec(ctx, `
insert into rebalance_channel_ledger (
  channel_id, paid_liquidity_sats, paid_cost_sats, paid_revenue_sats,
  last_rebalance_at, last_forward_at, last_unlock_at
) values ($1,$2,$3,$4,$5,$6,$7)
 on conflict (channel_id) do update set
  paid_liquidity_sats = rebalance_channel_ledger.paid_liquidity_sats + excluded.paid_liquidity_sats,
  paid_cost_sats = rebalance_channel_ledger.paid_cost_sats + excluded.paid_cost_sats,
  last_rebalance_at = excluded.last_rebalance_at
`, channelID, amountSat, costSat, 0, now, now, pgtype.Timestamptz{})
	return err
}

func (s *RebalanceService) recordPairSuccess(ctx context.Context, sourceID uint64, targetID uint64, amountSat int64, feePpm int64, feePaidSat int64) {
	if s.db == nil || sourceID == 0 || targetID == 0 {
		return
	}
	now := time.Now().UTC()
	_, _ = s.db.Exec(ctx, `
insert into rebalance_pair_stats (
  source_channel_id, target_channel_id, last_success_at, success_amount_sat, success_fee_ppm, success_fee_paid_sat, success_count
) values ($1,$2,$3,$4,$5,$6,1)
 on conflict (source_channel_id, target_channel_id) do update set
  last_success_at = excluded.last_success_at,
  success_amount_sat = excluded.success_amount_sat,
  success_fee_ppm = excluded.success_fee_ppm,
  success_fee_paid_sat = excluded.success_fee_paid_sat,
  success_count = rebalance_pair_stats.success_count + 1
`, int64(sourceID), int64(targetID), now, amountSat, feePpm, feePaidSat)
}

func (s *RebalanceService) recordPairFailure(ctx context.Context, sourceID uint64, targetID uint64, reason string) {
	if s.db == nil || sourceID == 0 || targetID == 0 {
		return
	}
	now := time.Now().UTC()
	_, _ = s.db.Exec(ctx, `
insert into rebalance_pair_stats (
  source_channel_id, target_channel_id, last_fail_at, last_fail_reason, fail_count
) values ($1,$2,$3,$4,1)
 on conflict (source_channel_id, target_channel_id) do update set
  last_fail_at = excluded.last_fail_at,
  last_fail_reason = excluded.last_fail_reason,
  fail_count = rebalance_pair_stats.fail_count + 1
`, int64(sourceID), int64(targetID), now, nullableString(reason))
}

func (s *RebalanceService) loadPairStatsForTarget(ctx context.Context, targetID uint64) map[uint64]pairStat {
	stats := map[uint64]pairStat{}
	if s.db == nil || targetID == 0 {
		return stats
	}
	rows, err := s.db.Query(ctx, `
select source_channel_id, target_channel_id, last_success_at, last_fail_at, success_amount_sat, success_fee_ppm
from rebalance_pair_stats
where target_channel_id=$1
`, int64(targetID))
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID int64
		var targetIDRow int64
		var lastSuccess pgtype.Timestamptz
		var lastFail pgtype.Timestamptz
		var successAmount int64
		var successFee int64
		if err := rows.Scan(&sourceID, &targetIDRow, &lastSuccess, &lastFail, &successAmount, &successFee); err != nil {
			return stats
		}
		stat := pairStat{
			SourceChannelID:  uint64(sourceID),
			TargetChannelID:  uint64(targetIDRow),
			SuccessAmountSat: successAmount,
			SuccessFeePpm:    successFee,
		}
		if lastSuccess.Valid {
			stat.LastSuccessAt = lastSuccess.Time
		}
		if lastFail.Valid {
			stat.LastFailAt = lastFail.Time
		}
		stats[uint64(sourceID)] = stat
	}
	return stats
}

func (s *RebalanceService) acquireSem(ctx context.Context) bool {
	s.mu.Lock()
	sem := s.sem
	s.mu.Unlock()
	if sem == nil {
		return true
	}
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *RebalanceService) releaseSem() {
	s.mu.Lock()
	sem := s.sem
	s.mu.Unlock()
	if sem == nil {
		return
	}
	select {
	case <-sem:
	default:
	}
}

func (s *RebalanceService) tryLockChannel(channelID uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channelLocks[channelID] {
		return false
	}
	s.channelLocks[channelID] = true
	return true
}

func (s *RebalanceService) unlockChannel(channelID uint64) {
	s.mu.Lock()
	delete(s.channelLocks, channelID)
	s.mu.Unlock()
}

func (s *RebalanceService) isChannelBusy(channelID uint64) bool {
	s.mu.Lock()
	locked := s.channelLocks[channelID]
	s.mu.Unlock()
	if locked {
		return true
	}
	if s.db == nil {
		return false
	}
	var running int
	_ = s.db.QueryRow(context.Background(), `
select 1
from rebalance_jobs
where status in ('running','queued') and target_channel_id=$1
limit 1
`, int64(channelID)).Scan(&running)
	return running == 1
}

func (s *RebalanceService) lastManualAutoRestartAt(ctx context.Context, channelID uint64) (time.Time, bool) {
	if s.db == nil || channelID == 0 {
		return time.Time{}, false
	}
	var createdAt pgtype.Timestamptz
	err := s.db.QueryRow(ctx, `
select max(created_at)
from rebalance_jobs
where target_channel_id=$1
  and source='manual'
  and coalesce(reason, '')='auto-restart'
`, int64(channelID)).Scan(&createdAt)
	if err != nil || !createdAt.Valid {
		return time.Time{}, false
	}
	return createdAt.Time.UTC(), true
}

func (s *RebalanceService) finishJob(jobID int64, status string, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completedAt := time.Now().UTC()
	_, _ = s.db.Exec(ctx, `
update rebalance_jobs
set status=$2, reason=$3, completed_at=$4
where id=$1`, jobID, status, nullableString(reason), completedAt)
	s.broadcast(RebalanceEvent{Type: "job", JobID: jobID, Status: status, Message: reason})

	info, ok := s.takeManualRestart(jobID)
	if ok && s.shouldManualRestart(status, reason) {
		go s.scheduleManualRestart(info)
	}
}

func (s *RebalanceService) takeManualRestart(jobID int64) (manualRestartInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manualRestart == nil {
		return manualRestartInfo{}, false
	}
	info, ok := s.manualRestart[jobID]
	if ok {
		delete(s.manualRestart, jobID)
	}
	return info, ok
}

func (s *RebalanceService) setManualRestartCancel(channelID uint64, cancel context.CancelFunc) *manualRestartHandle {
	s.mu.Lock()
	if s.manualRestartCancel == nil {
		s.manualRestartCancel = map[uint64]*manualRestartHandle{}
	}
	if existing, ok := s.manualRestartCancel[channelID]; ok && existing != nil && existing.cancel != nil {
		existing.cancel()
	}
	handle := &manualRestartHandle{cancel: cancel}
	s.manualRestartCancel[channelID] = handle
	s.mu.Unlock()
	return handle
}

func (s *RebalanceService) clearManualRestartCancel(channelID uint64, handle *manualRestartHandle) {
	s.mu.Lock()
	if s.manualRestartCancel == nil {
		s.mu.Unlock()
		return
	}
	if existing, ok := s.manualRestartCancel[channelID]; ok && existing == handle {
		delete(s.manualRestartCancel, channelID)
	}
	s.mu.Unlock()
}

func (s *RebalanceService) cancelManualRestart(channelID uint64) {
	s.mu.Lock()
	if s.manualRestartCancel == nil {
		s.mu.Unlock()
		return
	}
	handle := s.manualRestartCancel[channelID]
	delete(s.manualRestartCancel, channelID)
	s.mu.Unlock()
	if handle != nil && handle.cancel != nil {
		handle.cancel()
	}
}

func (s *RebalanceService) cancelAllManualRestarts() {
	s.mu.Lock()
	if s.manualRestartCancel == nil || len(s.manualRestartCancel) == 0 {
		s.mu.Unlock()
		return
	}
	handles := make([]*manualRestartHandle, 0, len(s.manualRestartCancel))
	for _, handle := range s.manualRestartCancel {
		if handle != nil {
			handles = append(handles, handle)
		}
	}
	s.manualRestartCancel = map[uint64]*manualRestartHandle{}
	s.mu.Unlock()
	for _, handle := range handles {
		if handle.cancel != nil {
			handle.cancel()
		}
	}
}

func (s *RebalanceService) shouldManualRestart(status string, reason string) bool {
	if status == "partial" {
		return true
	}
	if status == "failed" {
		return true
	}
	return false
}

func (s *RebalanceService) scheduleManualRestart(info manualRestartInfo) {
	cfg, _ := s.loadConfig(context.Background())
	cooldown := manualRestartInterval(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	handle := s.setManualRestartCancel(info.TargetChannelID, cancel)
	defer s.clearManualRestartCancel(info.TargetChannelID, handle)
	timer := time.NewTimer(cooldown)
	select {
	case <-timer.C:
	case <-ctx.Done():
		if !timer.Stop() {
			<-timer.C
		}
		return
	case <-s.stop:
		if !timer.Stop() {
			<-timer.C
		}
		return
	}

	if s.lnd == nil {
		return
	}

	restartCtx, restartCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer restartCancel()

	cfg, _ = s.loadConfig(restartCtx)
	if !cfg.ManualRestartWatch {
		return
	}
	settings, _ := s.loadChannelSettings(restartCtx)
	exclusions, _ := s.loadExclusions(restartCtx)
	ledger, _ := s.loadLedger(restartCtx)
	revenueByChannel, _ := s.fetchChannelRevenue7d(restartCtx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(restartCtx)

	channels, err := s.lnd.ListChannels(restartCtx)
	if err != nil {
		return
	}
	s.reconcileNewChannelDefaults(restartCtx, channels, settings, exclusions)
	var target lndclient.ChannelInfo
	found := false
	for _, ch := range channels {
		if ch.ChannelID == info.TargetChannelID {
			target = ch
			found = true
			break
		}
	}
	if !found {
		return
	}

	setting := settings[target.ChannelID]
	if !setting.ManualRestartEnabled {
		return
	}
	snapshot := s.buildChannelSnapshot(restartCtx, cfg, false, target, setting, ledger[target.ChannelID], revenueByChannel[target.ChannelID], costByChannel[target.ChannelID], exclusions[target.ChannelID])
	deficit := computeDeficitAmount(target, snapshot.TargetOutboundPct)
	if deficit <= 0 {
		return
	}
	minExecuteSat := effectiveMinExecuteSat(cfg)
	if minExecuteSat > 0 && deficit < minExecuteSat {
		return
	}
	if !snapshot.EligibleAsTarget {
		return
	}

	_, err = s.startJob(target.ChannelID, "manual", "auto-restart", 0, true)
	if err != nil && err.Error() == "channel busy" {
		go s.scheduleManualRestart(info)
	}
}

func (s *RebalanceService) StopJob(jobID int64) {
	s.mu.Lock()
	cancel := s.jobCancel[jobID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	ctx, cancelCtx := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelCtx()
	_, _ = s.db.Exec(ctx, `
update rebalance_jobs
set status='cancelled', reason='cancelled', completed_at=now()
where id=$1 and status in ('running','queued')`, jobID)
}

func (s *RebalanceService) buildChannelSnapshot(ctx context.Context, cfg RebalanceConfig, criticalActive bool, ch lndclient.ChannelInfo, setting channelSetting, ledger *channelLedger, revenue7dSat int64, cost7d rebalanceCost7dStat, excluded bool) RebalanceChannel {
	setting = normalizeChannelSetting(setting)
	capacity := float64(ch.CapacitySat)
	localPct := 0.0
	remotePct := 0.0
	if capacity > 0 {
		localPct = float64(ch.LocalBalanceSat) / capacity * 100
		remotePct = float64(ch.RemoteBalanceSat) / capacity * 100
	}

	outgoingFee := int64(0)
	outgoingBaseMsat := int64(0)
	peerFeeRate := int64(0)
	peerBaseMsat := int64(0)
	if ch.FeeRatePpm != nil {
		outgoingFee = *ch.FeeRatePpm
	}
	policies, err := s.lnd.GetChannelPolicies(ctx, ch.ChannelID)
	if err == nil {
		outgoingFee = policies.Local.FeeRatePpm
		outgoingBaseMsat = policies.Local.BaseFeeMsat
		peerFeeRate = policies.Remote.FeeRatePpm
		peerBaseMsat = policies.Remote.BaseFeeMsat
	}

	spread := outgoingFee - peerFeeRate
	target := setting.TargetOutboundPct

	protected := int64(0)
	paidCost := int64(0)
	paidRevenue := int64(0)
	lastRebalance := time.Time{}
	if ledger != nil {
		protected = ledger.PaidLiquiditySat
		paidCost = ledger.PaidCostSat
		paidRevenue = ledger.PaidRevenueSat
		lastRebalance = ledger.LastRebalanceAt
	}

	paybackProgress := 0.0
	if paidCost > 0 {
		paybackProgress = float64(paidRevenue) / float64(paidCost)
	}

	eligibleTarget := false
	deficitPct := target - localPct
	if ch.Active && deficitPct > cfg.DeadbandPct && outgoingFee > peerFeeRate {
		eligibleTarget = true
	}

	sourceFloorPct := cfg.SourceMinLocalPct
	if sourceFloorPct <= 0 || sourceFloorPct > 100 {
		sourceFloorPct = rebalanceDefaultTargetOutboundPct
	}
	maxSource := int64(float64(ch.LocalBalanceSat) - (float64(ch.CapacitySat) * (sourceFloorPct / 100)))
	eligibleSource := maxSource > 0 && localPct >= sourceFloorPct
	effectiveProtected := protected
	if protected > 0 {
		unlocked := false
		if (cfg.PaybackModeFlags&paybackModePayback) != 0 && paidCost > 0 && paidRevenue >= paidCost {
			unlocked = true
		}
		if (cfg.PaybackModeFlags&paybackModeTime) != 0 && !lastRebalance.IsZero() && cfg.UnlockDays > 0 {
			if time.Since(lastRebalance) >= time.Duration(cfg.UnlockDays)*24*time.Hour {
				unlocked = true
			}
		}
		if !unlocked && criticalActive && (cfg.PaybackModeFlags&paybackModeCritical) != 0 {
			release := int64(math.Round(float64(protected) * (cfg.CriticalReleasePct / 100)))
			effectiveProtected = protected - release
			if effectiveProtected < 0 {
				effectiveProtected = 0
			}
		} else if unlocked {
			effectiveProtected = 0
		}
		maxSource -= effectiveProtected
		if maxSource < 0 {
			maxSource = 0
		}
	}
	if maxSource <= 0 || !ch.Active {
		eligibleSource = false
	}

	roiEstimate := 0.0
	roiEstimateValid := false
	targetAmount := computeDeficitAmount(ch, target)
	estCost := estimateHistoricalCost(targetAmount, cost7d.FeePpm)
	if estCost > 0 && revenue7dSat > 0 {
		roiEstimate = float64(revenue7dSat) / float64(estCost)
		roiEstimateValid = true
	} else if estCost == 0 && targetAmount > 0 && outgoingFee > peerFeeRate {
		// Cost is zero -> ROI is indeterminate; allow auto rebal by skipping ROI filter.
		roiEstimateValid = false
	}

	var econRatioOverride *float64
	if !setting.UseDefaultEconRatio && setting.EconRatioOverrideSet {
		ratio := setting.EconRatioOverride
		econRatioOverride = &ratio
	}

	return RebalanceChannel{
		ChannelID:             ch.ChannelID,
		ChannelPoint:          ch.ChannelPoint,
		PeerAlias:             ch.PeerAlias,
		RemotePubkey:          ch.RemotePubkey,
		Active:                ch.Active,
		Private:               ch.Private,
		CapacitySat:           ch.CapacitySat,
		LocalBalanceSat:       ch.LocalBalanceSat,
		RemoteBalanceSat:      ch.RemoteBalanceSat,
		LocalPct:              localPct,
		RemotePct:             remotePct,
		OutgoingFeePpm:        outgoingFee,
		OutgoingBaseMsat:      outgoingBaseMsat,
		PeerFeeRatePpm:        peerFeeRate,
		PeerBaseMsat:          peerBaseMsat,
		SpreadPpm:             spread,
		TargetOutboundPct:     target,
		TargetAmountSat:       targetAmount,
		AutoEnabled:           setting.AutoEnabled,
		ManualRestartEnabled:  setting.ManualRestartEnabled,
		UseDefaultEconRatio:   setting.UseDefaultEconRatio,
		EconRatioOverride:     econRatioOverride,
		EligibleAsTarget:      eligibleTarget,
		EligibleAsSource:      eligibleSource && !excluded,
		ProtectedLiquiditySat: protected,
		PaybackProgress:       paybackProgress,
		MaxSourceSat:          maxSource,
		Revenue7dSat:          revenue7dSat,
		RebalanceCost7dSat:    cost7d.FeeSat,
		RebalanceCost7dPpm:    cost7d.FeePpm,
		RebalanceAmount7dSat:  cost7d.AmountSat,
		ROIEstimate:           roiEstimate,
		ROIEstimateValid:      roiEstimateValid,
		ExcludedAsSource:      excluded,
	}
}

func computeDeficitAmount(ch lndclient.ChannelInfo, targetOutboundPct float64) int64 {
	if ch.CapacitySat <= 0 {
		return 0
	}
	capacity := float64(ch.CapacitySat)
	currentOutbound := float64(ch.LocalBalanceSat) / capacity * 100
	deficit := targetOutboundPct - currentOutbound
	if deficit <= 0 {
		return 0
	}
	amount := capacity * deficit / 100
	return int64(math.Round(amount))
}

func computeProbeCap(remaining int64, minAmount int64, maxAmount int64) int64 {
	if remaining <= 0 {
		return 0
	}
	if maxAmount > 0 {
		if maxAmount < remaining {
			return maxAmount
		}
		return remaining
	}
	if minAmount <= 0 {
		return remaining
	}
	chunks := remaining / minAmount
	if chunks <= 4 {
		return remaining
	}
	start := int64(math.Round(float64(remaining) / math.Sqrt(float64(chunks))))
	minStart := minAmount * 4
	if start < minStart {
		start = minStart
	}
	if start > remaining {
		start = remaining
	}
	if start < minAmount {
		start = minAmount
	}
	return start
}

func filterSources(channels []RebalanceChannel, targetID uint64) []RebalanceChannel {
	sources := []RebalanceChannel{}
	for _, ch := range channels {
		if ch.ChannelID == targetID {
			continue
		}
		if !ch.EligibleAsSource {
			continue
		}
		sources = append(sources, ch)
	}
	return sources
}

func calcFeeLimitMsat(amountMsat int64, targetPolicy lndclient.ChannelPolicySnapshot, sourcePolicy *lndclient.ChannelPolicySnapshot, cfg RebalanceConfig) (int64, error) {
	if amountMsat <= 0 {
		return 0, nil
	}
	if cfg.FeeLimitPpm > 0 {
		feeMsat := (amountMsat * cfg.FeeLimitPpm) / 1_000_000
		if feeMsat < 0 {
			return 0, errors.New("max fee less than zero")
		}
		return feeMsat, nil
	}
	if cfg.EconRatio <= 0 {
		return 0, errors.New("econ ratio <= 0")
	}
	basePlus := targetPolicy.BaseFeeMsat + (amountMsat * targetPolicy.FeeRatePpm)
	feeMsat := int64(float64(basePlus) * cfg.EconRatio / 1_000_000)
	if cfg.LostProfit && sourcePolicy != nil {
		lost := int64(float64(sourcePolicy.BaseFeeMsat+(amountMsat*sourcePolicy.FeeRatePpm)) / 1_000_000)
		feeMsat -= lost
	}
	if cfg.EconRatioMaxPpm > 0 {
		ppm := feeMsatToPpm(feeMsat, amountMsat/1000)
		if ppm > cfg.EconRatioMaxPpm {
			feeMsat = (amountMsat * cfg.EconRatioMaxPpm) / 1_000_000
		}
	}
	if feeMsat < 0 {
		return 0, errors.New("max fee less than zero")
	}
	return feeMsat, nil
}

func calcFeeStepMsat(maxFeeMsat int64, steps int, step int) int64 {
	if maxFeeMsat <= 0 {
		return 0
	}
	if steps <= 1 {
		return maxFeeMsat
	}
	minFee := int64(math.Round(float64(maxFeeMsat) * 0.8))
	if minFee < 1 {
		minFee = 1
	}
	if minFee > maxFeeMsat {
		minFee = maxFeeMsat
	}
	if step <= 1 {
		return minFee
	}
	if step >= steps {
		return maxFeeMsat
	}
	span := maxFeeMsat - minFee
	if span <= 0 {
		return maxFeeMsat
	}
	frac := float64(step-1) / float64(steps-1)
	fee := float64(minFee) + float64(span)*frac
	if fee < 1 {
		fee = 1
	}
	return int64(math.Ceil(fee))
}

func feeMsatToPpm(feeMsat int64, amountSat int64) int64 {
	if amountSat <= 0 || feeMsat <= 0 {
		return 0
	}
	amountMsat := float64(amountSat) * 1000
	return int64(math.Round(float64(feeMsat) * 1_000_000 / amountMsat))
}

func estimateMaxCost(amountSat int64, targetPolicy lndclient.ChannelPolicySnapshot, cfg RebalanceConfig) int64 {
	if amountSat <= 0 {
		return 0
	}
	feeMsat, err := calcFeeLimitMsat(amountSat*1000, targetPolicy, nil, cfg)
	if err != nil || feeMsat <= 0 {
		return 0
	}
	return msatToSatCeil(feeMsat)
}

func estimateHistoricalCost(amountSat int64, feePpm int64) int64 {
	if amountSat <= 0 || feePpm <= 0 {
		return 0
	}
	feeMsat := (amountSat * 1000 * feePpm) / 1_000_000
	if feeMsat <= 0 {
		return 0
	}
	return msatToSatCeil(feeMsat)
}

func estimateTargetGain(amountSat int64, revenue7dSat int64, localBalanceSat int64, capacitySat int64) int64 {
	if amountSat <= 0 || revenue7dSat <= 0 {
		return 0
	}
	denom := localBalanceSat
	if denom <= 0 {
		denom = capacitySat
	}
	if denom <= 0 {
		return 0
	}
	if amountSat > denom {
		denom = amountSat
	}
	gain := float64(revenue7dSat) * (float64(amountSat) / float64(denom))
	if gain <= 0 {
		return 0
	}
	return int64(math.Round(gain))
}

func buildScanDetail(reasons map[string]int, remaining int64, candidates int) string {
	if len(reasons) == 0 {
		return ""
	}
	type reasonEntry struct {
		key   string
		label string
	}
	ordered := []reasonEntry{
		{key: "channel_busy", label: "channel busy"},
		{key: "target_already_balanced", label: "target already balanced"},
		{key: "recently_attempted", label: "recently attempted"},
		{key: "fee_cap_zero", label: "fee cap zero"},
		{key: "below_execute_min", label: "below execute min amount"},
		{key: "budget_below_min", label: "budget below min amount"},
		{key: "budget_too_low", label: "budget too low"},
		{key: "target_not_found", label: "target not found"},
		{key: "start_error", label: "start error"},
	}
	parts := []string{}
	for _, entry := range ordered {
		if count := reasons[entry.key]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", entry.label, count))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	base := "No jobs queued."
	if candidates > 0 {
		base = fmt.Sprintf("No jobs queued. Candidates after guardrails: %d.", candidates)
	}
	if remaining > 0 {
		return fmt.Sprintf("%s Remaining budget %d sats. Reasons: %s.", base, remaining, strings.Join(parts, ", "))
	}
	return fmt.Sprintf("%s Reasons: %s.", base, strings.Join(parts, ", "))
}

func estimateTargetROI(expectedGainSat int64, estimatedCostSat int64, amountSat int64, outgoingFeePpm int64, peerFeeRatePpm int64) (float64, bool) {
	if expectedGainSat > 0 && estimatedCostSat > 0 {
		return float64(expectedGainSat) / float64(estimatedCostSat), true
	}
	if amountSat > 0 && outgoingFeePpm > peerFeeRatePpm {
		return 0, false
	}
	return 0, true
}

func ppmToFeeLimitMsat(amountSat int64, feePpm int64) int64 {
	if amountSat <= 0 || feePpm <= 0 {
		return 0
	}
	feeSat := (amountSat * feePpm) / 1_000_000
	return feeSat * 1000
}

func msatToSatCeil(msat int64) int64 {
	if msat <= 0 {
		return 0
	}
	return int64(math.Ceil(float64(msat) / 1000.0))
}

func absoluteDeltaPPM(base int64, amt int64) int64 {
	if base == 0 {
		return 0
	}
	delta := (base - amt) * 1_000_000 / base
	if delta < 0 {
		return -delta
	}
	return delta
}

func (s *RebalanceService) probeRoute(ctx context.Context, route *lnrpc.Route, amountSat int64, minAmountSat int64, steps int, targetPolicy lndclient.ChannelPolicySnapshot, sourcePolicy lndclient.ChannelPolicySnapshot, cfg RebalanceConfig) (int64, error) {
	if route == nil || amountSat <= 0 {
		return 0, errors.New("route required")
	}
	good := int64(0)
	start := amountSat / 2
	if minAmountSat > 0 && minAmountSat < amountSat {
		good = -minAmountSat - 1
		start = minAmountSat
	}
	return s.probeRouteRecursive(ctx, route, good, amountSat, start, steps, targetPolicy, sourcePolicy, cfg)
}

func (s *RebalanceService) probeRouteRecursive(ctx context.Context, route *lnrpc.Route, goodAmount int64, badAmount int64, amount int64, steps int, targetPolicy lndclient.ChannelPolicySnapshot, sourcePolicy lndclient.ChannelPolicySnapshot, cfg RebalanceConfig) (int64, error) {
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && goodAmount > 0 {
			return goodAmount, nil
		}
		return 0, ctx.Err()
	}
	if amount <= 0 {
		return 0, errors.New("invalid probe amount")
	}
	if cfg.FailTolerancePpm <= 0 {
		cfg.FailTolerancePpm = 1000
	}
	if absoluteDeltaPPM(badAmount, amount) <= cfg.FailTolerancePpm ||
		absoluteDeltaPPM(amount, goodAmount) <= cfg.FailTolerancePpm ||
		amount == -goodAmount {
		if goodAmount <= 0 {
			return 0, nil
		}
		return goodAmount, nil
	}

	probedRoute, err := s.rebuildRouteForAmount(ctx, route, amount)
	if err != nil {
		return 0, err
	}
	maxFeeMsat, err := calcFeeLimitMsat(amount*1000, targetPolicy, &sourcePolicy, cfg)
	if err != nil {
		return 0, err
	}
	if maxFeeMsat > 0 && probedRoute.TotalFeesMsat > maxFeeMsat {
		nextAmount := amount + (badAmount-amount)/2
		return s.probeRouteRecursive(ctx, route, -amount, badAmount, nextAmount, steps, targetPolicy, sourcePolicy, cfg)
	}

	paymentHash := lndclient.RandomPaymentHash()
	attempt, err := s.lnd.SendToRoute(ctx, paymentHash, probedRoute)
	if attempt == nil {
		if err != nil {
			return 0, err
		}
		return 0, errors.New("empty probe attempt")
	}
	if attempt.Status == lnrpc.HTLCAttempt_SUCCEEDED {
		return 0, errors.New("probe unexpectedly succeeded")
	}
	if attempt.Status == lnrpc.HTLCAttempt_FAILED && attempt.Failure != nil {
		switch attempt.Failure.Code {
		case lnrpc.Failure_INCORRECT_OR_UNKNOWN_PAYMENT_DETAILS:
			if steps <= 1 {
				return amount, nil
			}
			nextAmount := amount + (badAmount-amount)/2
			return s.probeRouteRecursive(ctx, route, amount, badAmount, nextAmount, steps-1, targetPolicy, sourcePolicy, cfg)
		case lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE:
			if steps <= 1 {
				if goodAmount <= 0 {
					return 0, nil
				}
				return goodAmount, nil
			}
			var nextAmount int64
			if goodAmount >= 0 {
				nextAmount = amount + (goodAmount-amount)/2
			} else {
				nextAmount = amount - (goodAmount+amount)/2
			}
			return s.probeRouteRecursive(ctx, route, goodAmount, amount, nextAmount, steps-1, targetPolicy, sourcePolicy, cfg)
		case lnrpc.Failure_FEE_INSUFFICIENT:
			return s.probeRouteRecursive(ctx, route, goodAmount, badAmount, amount, steps, targetPolicy, sourcePolicy, cfg)
		default:
			return 0, fmt.Errorf("probe failed: %s", attempt.Failure.Code.String())
		}
	}
	return 0, errors.New("probe failed")
}

func buildAmountProbe(maxAmount int64, minAmount int64, steps int) []int64 {
	if maxAmount <= 0 {
		return nil
	}
	if minAmount <= 0 {
		minAmount = 1
	}
	if steps <= 0 {
		steps = 1
	}
	if steps > 6 {
		steps = 6
	}

	amounts := make([]int64, 0, steps)
	current := maxAmount
	for i := 0; i < steps; i++ {
		if current < minAmount {
			current = minAmount
		}
		if len(amounts) == 0 || amounts[len(amounts)-1] != current {
			amounts = append(amounts, current)
		}
		if current == minAmount {
			break
		}
		next := (current + minAmount) / 2
		if next >= current {
			next = current - 1
		}
		if next < minAmount {
			next = minAmount
		}
		current = next
	}
	if len(amounts) == 0 || amounts[len(amounts)-1] != minAmount {
		amounts = append(amounts, minAmount)
	}
	return amounts
}

func (s *RebalanceService) ensureSchema(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db not configured")
	}
	_, err := s.db.Exec(ctx, `
do $$
begin
  if exists (
    select 1 from information_schema.columns
    where table_schema='public' and table_name='rebalance_channel_settings' and column_name='target_inbound_pct'
  ) and not exists (
    select 1 from information_schema.columns
    where table_schema='public' and table_name='rebalance_channel_settings' and column_name='target_outbound_pct'
  ) then
    alter table rebalance_channel_settings rename column target_inbound_pct to target_outbound_pct;
    update rebalance_channel_settings
      set target_outbound_pct = 100 - target_outbound_pct
      where target_outbound_pct between 0 and 100;
  elsif exists (
    select 1 from information_schema.columns
    where table_schema='public' and table_name='rebalance_channel_settings' and column_name='target_inbound_pct'
  ) and exists (
    select 1 from information_schema.columns
    where table_schema='public' and table_name='rebalance_channel_settings' and column_name='target_outbound_pct'
  ) then
    alter table rebalance_channel_settings drop column target_inbound_pct;
  end if;

  if exists (
    select 1 from information_schema.columns
    where table_schema='public' and table_name='rebalance_jobs' and column_name='target_inbound_pct'
  ) and not exists (
    select 1 from information_schema.columns
    where table_schema='public' and table_name='rebalance_jobs' and column_name='target_outbound_pct'
  ) then
    alter table rebalance_jobs rename column target_inbound_pct to target_outbound_pct;
    update rebalance_jobs
      set target_outbound_pct = 100 - target_outbound_pct
      where target_outbound_pct between 0 and 100;
  elsif exists (
    select 1 from information_schema.columns
    where table_schema='public' and table_name='rebalance_jobs' and column_name='target_inbound_pct'
  ) and exists (
    select 1 from information_schema.columns
    where table_schema='public' and table_name='rebalance_jobs' and column_name='target_outbound_pct'
  ) then
    alter table rebalance_jobs drop column target_inbound_pct;
  end if;
end $$;

  create table if not exists rebalance_config (
    id smallint primary key,
    auto_enabled boolean not null default false,
    scan_interval_sec integer not null default 600,
    deadband_pct double precision not null default 10,
    source_min_local_pct double precision not null default 50,
    econ_ratio double precision not null default 0.6,
    econ_ratio_max_ppm bigint not null default 0,
    fee_limit_ppm bigint not null default 0,
    lost_profit boolean not null default false,
    fail_tolerance_ppm bigint not null default 1000,
    roi_min double precision not null default 1.1,
    daily_budget_pct double precision not null default 50,
    budget_mode text not null default 'revenue_24h_pct',
    manual_reserve_enabled boolean not null default false,
    manual_reserve_mode text not null default 'fixed_sat',
    manual_reserve_value double precision not null default 0,
    max_concurrent integer not null default 2,
    min_amount_sat bigint not null default 20000,
    max_amount_sat bigint not null default 0,
    min_split_enabled boolean not null default false,
    min_probe_sat bigint not null default 0,
    min_execute_sat bigint not null default 0,
    mpp_enabled boolean not null default false,
    mpp_max_shards integer not null default 8,
    mpp_parallelism integer not null default 6,
    mpp_min_shard_sat bigint not null default 1000,
    mpp_round_timeout_sec integer not null default 30,
    mpp_auto_only boolean not null default false,
    fee_ladder_steps integer not null default 4,
    amount_probe_steps integer not null default 4,
    amount_probe_adaptive boolean not null default true,
    attempt_timeout_sec integer not null default 20,
    rebalance_timeout_sec integer not null default 600,
    manual_restart_watch boolean not null default false,
    mc_half_life_sec bigint not null default 0,
    payback_mode_flags integer not null default 7,
    unlock_days integer not null default 14,
    critical_release_pct double precision not null default 20,
    critical_min_sources integer not null default 2,
    critical_min_available_sats bigint not null default 0,
    critical_cycles integer not null default 3,
    updated_at timestamptz not null default now()
  );

  alter table rebalance_config
    add column if not exists source_min_local_pct double precision not null default 50;
  alter table rebalance_config
    add column if not exists econ_ratio_max_ppm bigint not null default 0;
  alter table rebalance_config
    add column if not exists fee_limit_ppm bigint not null default 0;
  alter table rebalance_config
    add column if not exists lost_profit boolean not null default false;
  alter table rebalance_config
    add column if not exists fail_tolerance_ppm bigint not null default 1000;
  alter table rebalance_config
    add column if not exists amount_probe_steps integer not null default 4;
  alter table rebalance_config
    add column if not exists budget_mode text not null default 'revenue_24h_pct';
  alter table rebalance_config
    add column if not exists manual_reserve_enabled boolean not null default false;
  alter table rebalance_config
    add column if not exists manual_reserve_mode text not null default 'fixed_sat';
  alter table rebalance_config
    add column if not exists manual_reserve_value double precision not null default 0;
  alter table rebalance_config
    add column if not exists min_split_enabled boolean not null default false;
  alter table rebalance_config
    add column if not exists min_probe_sat bigint not null default 0;
  alter table rebalance_config
    add column if not exists min_execute_sat bigint not null default 0;
  alter table rebalance_config
    add column if not exists mpp_enabled boolean not null default false;
  alter table rebalance_config
    add column if not exists mpp_max_shards integer not null default 8;
  alter table rebalance_config
    add column if not exists mpp_parallelism integer not null default 6;
  alter table rebalance_config
    add column if not exists mpp_min_shard_sat bigint not null default 1000;
  alter table rebalance_config
    add column if not exists mpp_round_timeout_sec integer not null default 30;
  alter table rebalance_config
    alter column mpp_max_shards set default 8;
  alter table rebalance_config
    alter column mpp_parallelism set default 6;
  alter table rebalance_config
    alter column mpp_round_timeout_sec set default 30;
  alter table rebalance_config
    add column if not exists mpp_auto_only boolean not null default false;
  alter table rebalance_config
    add column if not exists amount_probe_adaptive boolean not null default true;
  alter table rebalance_config
    add column if not exists attempt_timeout_sec integer not null default 20;
  alter table rebalance_config
    add column if not exists rebalance_timeout_sec integer not null default 600;
 alter table rebalance_config
    add column if not exists manual_restart_watch boolean not null default false;
  alter table rebalance_config
    add column if not exists mc_half_life_sec bigint not null default 0;
  alter table rebalance_config
    add column if not exists new_channel_exclusion_seeded boolean not null default false;

  alter table if exists rebalance_channel_settings
    add column if not exists manual_restart_enabled boolean not null default false;
  alter table if exists rebalance_channel_settings
    add column if not exists use_default_econ_ratio boolean not null default true;
  alter table if exists rebalance_channel_settings
    add column if not exists econ_ratio_override double precision;

create table if not exists rebalance_channel_settings (
  channel_id bigint primary key,
  channel_point text not null,
  target_outbound_pct double precision not null default 50,
  auto_enabled boolean not null default false,
  manual_restart_enabled boolean not null default false,
  use_default_econ_ratio boolean not null default true,
  econ_ratio_override double precision,
  updated_at timestamptz not null default now()
);

create table if not exists rebalance_source_exclusions (
  channel_id bigint primary key,
  channel_point text not null,
  reason text,
  created_at timestamptz not null default now()
);

create table if not exists rebalance_channel_ledger (
  channel_id bigint primary key,
  paid_liquidity_sats bigint not null default 0,
  paid_cost_sats bigint not null default 0,
  paid_revenue_sats bigint not null default 0,
  last_rebalance_at timestamptz,
  last_forward_at timestamptz,
  last_unlock_at timestamptz
);

create table if not exists rebalance_jobs (
  id bigserial primary key,
  created_at timestamptz not null default now(),
  completed_at timestamptz,
  source text not null,
  status text not null,
  reason text,
  target_channel_id bigint not null,
  target_channel_point text not null,
  target_outbound_pct double precision not null,
  target_amount_sat bigint not null,
  config_snapshot jsonb
);

create table if not exists rebalance_attempts (
  id bigserial primary key,
  job_id bigint not null references rebalance_jobs(id) on delete cascade,
  attempt_index integer not null,
  source_channel_id bigint not null,
  amount_sat bigint not null,
  fee_limit_ppm bigint not null,
  fee_paid_sat bigint not null default 0,
  status text not null,
  payment_hash text,
  fail_reason text,
  started_at timestamptz not null default now(),
  finished_at timestamptz
);

create table if not exists rebalance_mpp_shadow (
  job_id bigint primary key references rebalance_jobs(id) on delete cascade,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  target_channel_id bigint not null,
  job_source text not null,
  mpp_enabled boolean not null default false,
  mpp_auto_only boolean not null default false,
  max_shards integer not null default 0,
  parallelism integer not null default 0,
  min_shard_sat bigint not null default 0,
  round_timeout_sec integer not null default 0,
  target_amount_sat bigint not null default 0,
  eligible_sources integer not null default 0,
  planned_sources integer not null default 0,
  planned_shards integer not null default 0,
  planned_total_sat bigint not null default 0,
  planned_remainder_sat bigint not null default 0,
  actual_status text,
  actual_attempts integer not null default 0,
  actual_success_attempts integer not null default 0,
  actual_sent_sat bigint not null default 0,
  actual_fee_sat bigint not null default 0,
  actual_any_success boolean not null default false,
  actual_floor_blocked_sources integer not null default 0
);

alter table if exists rebalance_mpp_shadow
  add column if not exists actual_floor_blocked_sources integer not null default 0;

create table if not exists rebalance_pair_stats (
  source_channel_id bigint not null,
  target_channel_id bigint not null,
  last_success_at timestamptz,
  last_fail_at timestamptz,
  last_fail_reason text,
  success_amount_sat bigint not null default 0,
  success_fee_ppm bigint not null default 0,
  success_fee_paid_sat bigint not null default 0,
  success_count integer not null default 0,
  fail_count integer not null default 0,
  primary key (source_channel_id, target_channel_id)
);

create table if not exists rebalance_budget_daily (
  day date primary key,
  budget_sat bigint not null,
  spent_auto_sat bigint not null default 0,
  spent_manual_sat bigint not null default 0,
  spent_sat bigint not null default 0,
  updated_at timestamptz not null default now()
);

alter table rebalance_budget_daily
  add column if not exists spent_auto_sat bigint not null default 0;
alter table rebalance_budget_daily
  add column if not exists spent_manual_sat bigint not null default 0;

create index if not exists rebalance_jobs_status_idx on rebalance_jobs (status);
create index if not exists rebalance_jobs_created_idx on rebalance_jobs (created_at desc);
create index if not exists rebalance_jobs_target_completed_idx on rebalance_jobs (target_channel_id, completed_at desc);
create index if not exists rebalance_attempts_job_idx on rebalance_attempts (job_id);
create index if not exists rebalance_attempts_status_idx on rebalance_attempts (status);
create index if not exists rebalance_attempts_finished_status_idx on rebalance_attempts (finished_at desc, status);
create index if not exists rebalance_mpp_shadow_created_idx on rebalance_mpp_shadow (created_at desc);
create index if not exists rebalance_mpp_shadow_success_idx on rebalance_mpp_shadow (actual_any_success, created_at desc);
create index if not exists rebalance_pair_stats_fail_idx on rebalance_pair_stats (last_fail_at desc);
create index if not exists rebalance_pair_stats_success_idx on rebalance_pair_stats (last_success_at desc);
create index if not exists notifications_channel_id_idx on notifications (channel_id);
`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `insert into rebalance_config (id) values ($1) on conflict (id) do nothing`, rebalanceConfigID)
	return err
}

func (s *RebalanceService) loadConfig(ctx context.Context) (RebalanceConfig, error) {
	s.mu.Lock()
	if s.cfgLoaded {
		cfg := s.cfg
		s.mu.Unlock()
		return cfg, nil
	}
	s.mu.Unlock()

	if s.db == nil {
		return defaultRebalanceConfig(), errors.New("db unavailable")
	}

	row := s.db.QueryRow(ctx, `
  select auto_enabled, scan_interval_sec, deadband_pct, source_min_local_pct, econ_ratio, econ_ratio_max_ppm, fee_limit_ppm, lost_profit, fail_tolerance_ppm, roi_min, daily_budget_pct, budget_mode, manual_reserve_enabled, manual_reserve_mode, manual_reserve_value,
    max_concurrent, min_amount_sat, max_amount_sat, min_split_enabled, min_probe_sat, min_execute_sat, mpp_enabled, mpp_max_shards, mpp_parallelism, mpp_min_shard_sat, mpp_round_timeout_sec, mpp_auto_only,
    fee_ladder_steps, amount_probe_steps, amount_probe_adaptive, attempt_timeout_sec, rebalance_timeout_sec, manual_restart_watch, mc_half_life_sec, payback_mode_flags,
    unlock_days, critical_release_pct, critical_min_sources, critical_min_available_sats, critical_cycles
  from rebalance_config where id=$1`, rebalanceConfigID)

	cfg := defaultRebalanceConfig()
	err := row.Scan(
		&cfg.AutoEnabled,
		&cfg.ScanIntervalSec,
		&cfg.DeadbandPct,
		&cfg.SourceMinLocalPct,
		&cfg.EconRatio,
		&cfg.EconRatioMaxPpm,
		&cfg.FeeLimitPpm,
		&cfg.LostProfit,
		&cfg.FailTolerancePpm,
		&cfg.ROIMin,
		&cfg.DailyBudgetPct,
		&cfg.BudgetMode,
		&cfg.ManualReserveEnabled,
		&cfg.ManualReserveMode,
		&cfg.ManualReserveValue,
		&cfg.MaxConcurrent,
		&cfg.MinAmountSat,
		&cfg.MaxAmountSat,
		&cfg.MinSplitEnabled,
		&cfg.MinProbeSat,
		&cfg.MinExecuteSat,
		&cfg.MppEnabled,
		&cfg.MppMaxShards,
		&cfg.MppParallelism,
		&cfg.MppMinShardSat,
		&cfg.MppRoundTimeoutSec,
		&cfg.MppAutoOnly,
		&cfg.FeeLadderSteps,
		&cfg.AmountProbeSteps,
		&cfg.AmountProbeAdaptive,
		&cfg.AttemptTimeoutSec,
		&cfg.RebalanceTimeoutSec,
		&cfg.ManualRestartWatch,
		&cfg.MissionControlHalfLifeSec,
		&cfg.PaybackModeFlags,
		&cfg.UnlockDays,
		&cfg.CriticalReleasePct,
		&cfg.CriticalMinSources,
		&cfg.CriticalMinAvailableSats,
		&cfg.CriticalCycles,
	)
	if err != nil {
		return cfg, err
	}
	cfg = normalizeRebalanceConfig(cfg)

	s.mu.Lock()
	s.cfg = cfg
	s.cfgLoaded = true
	s.mu.Unlock()
	return cfg, nil
}

func (s *RebalanceService) upsertConfig(ctx context.Context, cfg RebalanceConfig) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	_, err := s.db.Exec(ctx, `
  insert into rebalance_config (
    id, auto_enabled, scan_interval_sec, deadband_pct, source_min_local_pct, econ_ratio, econ_ratio_max_ppm, fee_limit_ppm, lost_profit, fail_tolerance_ppm, roi_min, daily_budget_pct, budget_mode, manual_reserve_enabled, manual_reserve_mode, manual_reserve_value,
    max_concurrent, min_amount_sat, max_amount_sat, min_split_enabled, min_probe_sat, min_execute_sat, mpp_enabled, mpp_max_shards, mpp_parallelism, mpp_min_shard_sat, mpp_round_timeout_sec, mpp_auto_only,
    fee_ladder_steps, amount_probe_steps, amount_probe_adaptive, attempt_timeout_sec, rebalance_timeout_sec, manual_restart_watch, mc_half_life_sec, payback_mode_flags,
    unlock_days, critical_release_pct, critical_min_sources, critical_min_available_sats, critical_cycles, updated_at
  ) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,now())
   on conflict (id) do update set
    auto_enabled = excluded.auto_enabled,
    scan_interval_sec = excluded.scan_interval_sec,
    deadband_pct = excluded.deadband_pct,
    source_min_local_pct = excluded.source_min_local_pct,
    econ_ratio = excluded.econ_ratio,
    econ_ratio_max_ppm = excluded.econ_ratio_max_ppm,
    fee_limit_ppm = excluded.fee_limit_ppm,
    lost_profit = excluded.lost_profit,
    fail_tolerance_ppm = excluded.fail_tolerance_ppm,
    roi_min = excluded.roi_min,
    daily_budget_pct = excluded.daily_budget_pct,
    budget_mode = excluded.budget_mode,
    manual_reserve_enabled = excluded.manual_reserve_enabled,
    manual_reserve_mode = excluded.manual_reserve_mode,
    manual_reserve_value = excluded.manual_reserve_value,
    max_concurrent = excluded.max_concurrent,
    min_amount_sat = excluded.min_amount_sat,
    max_amount_sat = excluded.max_amount_sat,
    min_split_enabled = excluded.min_split_enabled,
    min_probe_sat = excluded.min_probe_sat,
    min_execute_sat = excluded.min_execute_sat,
    mpp_enabled = excluded.mpp_enabled,
    mpp_max_shards = excluded.mpp_max_shards,
    mpp_parallelism = excluded.mpp_parallelism,
    mpp_min_shard_sat = excluded.mpp_min_shard_sat,
    mpp_round_timeout_sec = excluded.mpp_round_timeout_sec,
    mpp_auto_only = excluded.mpp_auto_only,
    fee_ladder_steps = excluded.fee_ladder_steps,
    amount_probe_steps = excluded.amount_probe_steps,
    amount_probe_adaptive = excluded.amount_probe_adaptive,
    attempt_timeout_sec = excluded.attempt_timeout_sec,
    rebalance_timeout_sec = excluded.rebalance_timeout_sec,
    manual_restart_watch = excluded.manual_restart_watch,
    mc_half_life_sec = excluded.mc_half_life_sec,
    payback_mode_flags = excluded.payback_mode_flags,
    unlock_days = excluded.unlock_days,
    critical_release_pct = excluded.critical_release_pct,
    critical_min_sources = excluded.critical_min_sources,
    critical_min_available_sats = excluded.critical_min_available_sats,
    critical_cycles = excluded.critical_cycles,
    updated_at = now()
  `, rebalanceConfigID, cfg.AutoEnabled, cfg.ScanIntervalSec, cfg.DeadbandPct, cfg.SourceMinLocalPct, cfg.EconRatio, cfg.EconRatioMaxPpm, cfg.FeeLimitPpm, cfg.LostProfit, cfg.FailTolerancePpm, cfg.ROIMin, cfg.DailyBudgetPct, cfg.BudgetMode, cfg.ManualReserveEnabled, cfg.ManualReserveMode, cfg.ManualReserveValue, cfg.MaxConcurrent,
		cfg.MinAmountSat, cfg.MaxAmountSat, cfg.MinSplitEnabled, cfg.MinProbeSat, cfg.MinExecuteSat, cfg.MppEnabled, cfg.MppMaxShards, cfg.MppParallelism, cfg.MppMinShardSat, cfg.MppRoundTimeoutSec, cfg.MppAutoOnly, cfg.FeeLadderSteps, cfg.AmountProbeSteps, cfg.AmountProbeAdaptive, cfg.AttemptTimeoutSec, cfg.RebalanceTimeoutSec, cfg.ManualRestartWatch, cfg.MissionControlHalfLifeSec, cfg.PaybackModeFlags, cfg.UnlockDays, cfg.CriticalReleasePct, cfg.CriticalMinSources, cfg.CriticalMinAvailableSats, cfg.CriticalCycles,
	)
	return err
}

func (s *RebalanceService) loadChannelSettings(ctx context.Context) (map[uint64]channelSetting, error) {
	settings := map[uint64]channelSetting{}
	if s.db == nil {
		return settings, nil
	}
	rows, err := s.db.Query(ctx, `
select channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, econ_ratio_override from rebalance_channel_settings
`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var setting channelSetting
		var econRatioOverride pgtype.Float8
		if err := rows.Scan(&channelID, &setting.ChannelPoint, &setting.TargetOutboundPct, &setting.AutoEnabled, &setting.ManualRestartEnabled, &setting.UseDefaultEconRatio, &econRatioOverride); err != nil {
			return settings, err
		}
		if econRatioOverride.Valid {
			setting.EconRatioOverride = econRatioOverride.Float64
			setting.EconRatioOverrideSet = true
		}
		setting.ChannelID = uint64(channelID)
		settings[setting.ChannelID] = normalizeChannelSetting(setting)
	}
	return settings, rows.Err()
}

func (s *RebalanceService) loadNewChannelExclusionSeeded(ctx context.Context) (bool, error) {
	if s.db == nil {
		return false, errors.New("db unavailable")
	}
	var seeded bool
	if err := s.db.QueryRow(ctx, `
select new_channel_exclusion_seeded
from rebalance_config
where id=$1
`, rebalanceConfigID).Scan(&seeded); err != nil {
		return false, err
	}
	return seeded, nil
}

func (s *RebalanceService) markNewChannelExclusionSeeded(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	_, err := s.db.Exec(ctx, `
update rebalance_config
set new_channel_exclusion_seeded=true, updated_at=now()
where id=$1
`, rebalanceConfigID)
	return err
}

func (s *RebalanceService) loadExclusions(ctx context.Context) (map[uint64]bool, error) {
	excluded := map[uint64]bool{}
	if s.db == nil {
		return excluded, nil
	}
	rows, err := s.db.Query(ctx, `select channel_id from rebalance_source_exclusions`)
	if err != nil {
		return excluded, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		if err := rows.Scan(&channelID); err != nil {
			return excluded, err
		}
		excluded[uint64(channelID)] = true
	}
	return excluded, rows.Err()
}

func (s *RebalanceService) loadLedger(ctx context.Context) (map[uint64]*channelLedger, error) {
	ledger := map[uint64]*channelLedger{}
	if s.db == nil {
		return ledger, nil
	}
	rows, err := s.db.Query(ctx, `
select occurred_at, type,
  coalesce(rebal_target_chan_id, channel_id) as rebalance_channel_id,
  channel_id as forward_channel_id,
  amount_sat, fee_sat, fee_msat
from notifications
where (
    type='rebalance'
    and status in ('SETTLED', 'SUCCEEDED')
    and coalesce(rebal_target_chan_id, channel_id) is not null
  ) or (
    type='forward'
    and channel_id is not null
  )
order by occurred_at asc, id asc
`)
	if err != nil {
		return ledger, err
	}
	defer rows.Close()
	for rows.Next() {
		var occurredAt time.Time
		var typ string
		var rebalanceChannelID pgtype.Int8
		var forwardChannelID pgtype.Int8
		var amountSat int64
		var feeSat int64
		var feeMsat int64
		if err := rows.Scan(&occurredAt, &typ, &rebalanceChannelID, &forwardChannelID, &amountSat, &feeSat, &feeMsat); err != nil {
			return ledger, err
		}
		if feeMsat == 0 && feeSat != 0 {
			feeMsat = feeSat * 1000
		}
		if typ == "rebalance" {
			if !rebalanceChannelID.Valid || rebalanceChannelID.Int64 == 0 {
				continue
			}
			channelID := uint64(rebalanceChannelID.Int64)
			// channel_id is stored as bigint (signed). When the original chan_id
			// is a uint64 with the high bit set, scanning into int64 yields a
			// negative value, but uint64(channelID) still maps back to the original
			// chan_id.
			entry, ok := ledger[channelID]
			if !ok {
				entry = &channelLedger{ChannelID: channelID}
				ledger[channelID] = entry
			}
			if amountSat > 0 {
				entry.PaidLiquiditySat += amountSat
			}
			if feeMsat > 0 {
				entry.PaidCostSat += msatToSatCeil(feeMsat)
			}
			entry.LastRebalanceAt = occurredAt
			if entry.LastForwardAt.IsZero() {
				entry.LastForwardAt = occurredAt
			}
			continue
		}

		if typ != "forward" || !forwardChannelID.Valid || forwardChannelID.Int64 == 0 {
			continue
		}
		channelID := uint64(forwardChannelID.Int64)
		entry, ok := ledger[channelID]
		if !ok {
			continue
		}
		if amountSat > 0 {
			entry.PaidLiquiditySat -= amountSat
			if entry.PaidLiquiditySat < 0 {
				entry.PaidLiquiditySat = 0
			}
		}
		if feeMsat > 0 {
			entry.PaidRevenueSat += feeMsat / 1000
		}
		entry.LastForwardAt = occurredAt
	}
	return ledger, rows.Err()
}

func (s *RebalanceService) applyForwardDeltas(ctx context.Context, ledger map[uint64]*channelLedger) error {
	// Ledger is now derived directly from notifications in loadLedger.
	// Keep this as a no-op so existing call sites remain unchanged.
	return nil
}

func (s *RebalanceService) fetchChannelRevenue7d(ctx context.Context) (map[uint64]int64, error) {
	revenue := map[uint64]int64{}
	if s.db == nil {
		return revenue, nil
	}
	rows, err := s.db.Query(ctx, `
select channel_id, coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0)
from notifications
where type='forward'
  and occurred_at >= now() - interval '7 days'
  and channel_id is not null
group by channel_id`)
	if err != nil {
		return revenue, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var feeMsat int64
		if err := rows.Scan(&channelID, &feeMsat); err != nil {
			return revenue, err
		}
		revenue[uint64(channelID)] = feeMsat / 1000
	}
	return revenue, rows.Err()
}

func (s *RebalanceService) ensureDailyBudget(ctx context.Context, cfg RebalanceConfig) error {
	if s.db == nil {
		return nil
	}
	day := time.Now().In(time.Local).Format("2006-01-02")
	now := time.Now().In(time.Local)
	revenue24h, err := s.fetchForwardRevenue24h(ctx, now)
	if err != nil {
		return err
	}
	avgRevenue7d, err := s.fetchAvgRevenue7d(ctx, now.AddDate(0, 0, -6))
	if err != nil && s.logger != nil {
		s.logger.Printf("rebalance avg revenue 7d unavailable: %v", err)
	}
	budget, _, _ := computeDailyBudgetFromRevenue(cfg, revenue24h, avgRevenue7d)
	if budget < 0 {
		budget = 0
	}
	_, err = s.db.Exec(ctx, `
  insert into rebalance_budget_daily (day, budget_sat, spent_auto_sat, spent_manual_sat, spent_sat, updated_at)
  values ($1,$2,0,0,0,now())
   on conflict (day) do update set budget_sat=excluded.budget_sat, updated_at=now()
  `, day, budget)
	return err
}

func computeDailyBudgetFromRevenue(cfg RebalanceConfig, revenue24h int64, avgRevenue7d int64) (int64, int64, int64) {
	if revenue24h < 0 {
		revenue24h = 0
	}
	if avgRevenue7d < 0 {
		avgRevenue7d = 0
	}
	pct := cfg.DailyBudgetPct / 100
	if pct < 0 {
		pct = 0
	}
	baseBudget := int64(math.Round(float64(avgRevenue7d) * pct))
	shortTermBudget := int64(math.Round(float64(revenue24h) * pct))
	switch normalizeRebalanceBudgetMode(cfg.BudgetMode) {
	case rebalanceBudgetModeHybridRevenue:
		if avgRevenue7d <= 0 {
			return shortTermBudget, baseBudget, shortTermBudget
		}
		if revenue24h <= 0 {
			return baseBudget, baseBudget, shortTermBudget
		}
		total := int64(math.Round(0.70*float64(baseBudget) + 0.30*float64(shortTermBudget)))
		return total, baseBudget, shortTermBudget
	default:
		return shortTermBudget, baseBudget, shortTermBudget
	}
}

func computeManualReserveSat(cfg RebalanceConfig, totalBudgetSat int64) int64 {
	if !cfg.ManualReserveEnabled || totalBudgetSat <= 0 {
		return 0
	}
	switch normalizeRebalanceManualReserveMode(cfg.ManualReserveMode) {
	case rebalanceManualReserveModePct:
		pct := cfg.ManualReserveValue / 100
		if pct < 0 {
			pct = 0
		}
		if pct > 1 {
			pct = 1
		}
		return int64(math.Round(float64(totalBudgetSat) * pct))
	default:
		value := int64(math.Round(cfg.ManualReserveValue))
		if value < 0 {
			value = 0
		}
		if value > totalBudgetSat {
			value = totalBudgetSat
		}
		return value
	}
}

func computeRemainingForAuto(totalBudgetSat int64, spentTotalSat int64, spentManualSat int64, manualReserveSat int64) (int64, int64) {
	remainingTotal := totalBudgetSat - spentTotalSat
	if remainingTotal < 0 {
		remainingTotal = 0
	}
	manualReserveRemaining := manualReserveSat - spentManualSat
	if manualReserveRemaining < 0 {
		manualReserveRemaining = 0
	}
	remainingForAuto := remainingTotal - manualReserveRemaining
	if remainingForAuto < 0 {
		remainingForAuto = 0
	}
	return remainingTotal, remainingForAuto
}

func checkManualBudgetAllowance(cfg RebalanceConfig, setting channelSetting, target lndclient.ChannelInfo, amountSat int64, totalBudgetSat int64, spentManualSat int64, spentTotalSat int64) error {
	if amountSat <= 0 {
		return nil
	}
	manualReserveSat := computeManualReserveSat(cfg, totalBudgetSat)
	remainingTotal, _ := computeRemainingForAuto(totalBudgetSat, spentTotalSat, spentManualSat, manualReserveSat)
	if remainingTotal <= 0 {
		return errManualBudgetExhausted
	}
	feeCfg := effectiveConfigForTarget(cfg, setting)
	targetPolicy := lndclient.ChannelPolicySnapshot{
		FeeRatePpm:  int64Value(target.FeeRatePpm),
		BaseFeeMsat: int64Value(target.BaseFeeMsat),
	}
	estimatedCost := estimateMaxCost(amountSat, targetPolicy, feeCfg)
	if estimatedCost > 0 && estimatedCost > remainingTotal {
		return errManualBudgetInsufficient
	}
	return nil
}

func int64Value(ptr *int64) int64 {
	if ptr == nil {
		return 0
	}
	return *ptr
}

func (s *RebalanceService) fetchAvgRevenue7d(ctx context.Context, start time.Time) (int64, error) {
	if s.db == nil {
		return 0, nil
	}
	startDate := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	var total int64
	err := s.db.QueryRow(ctx, `
select coalesce(sum(forward_fee_revenue_sats), 0)
from reports_daily
where report_date >= $1
`, startDate).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total / 7, nil
}

func (s *RebalanceService) fetchForwardRevenue24h(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		now = time.Now().In(time.Local)
	}
	start := now.Add(-24 * time.Hour)

	if s.lnd != nil {
		feeMsat, err := s.fetchForwardRevenue24hFromLND(ctx, start, now)
		if err == nil {
			return msatToSatCeil(feeMsat), nil
		}
	}

	return s.fetchForwardRevenue24hFromNotifications(ctx, start, now)
}

func (s *RebalanceService) fetchForwardRevenue24hFromLND(ctx context.Context, start time.Time, end time.Time) (int64, error) {
	if s.lnd == nil {
		return 0, errors.New("lnd unavailable")
	}
	conn, err := s.lnd.DialLightning(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	var offset uint32
	var revenueMsat int64

	for {
		resp, err := client.ForwardingHistory(ctx, &lnrpc.ForwardingHistoryRequest{
			StartTime:    uint64(start.Unix()),
			EndTime:      uint64(end.Unix()),
			IndexOffset:  offset,
			NumMaxEvents: rebalanceForwardPageSize,
		})
		if err != nil {
			return 0, err
		}
		if resp == nil || len(resp.ForwardingEvents) == 0 {
			break
		}

		for _, evt := range resp.ForwardingEvents {
			if evt == nil {
				continue
			}
			if evt.FeeMsat != 0 {
				revenueMsat += int64(evt.FeeMsat)
			} else if evt.Fee != 0 {
				revenueMsat += int64(evt.Fee) * 1000
			}
		}

		if resp.LastOffsetIndex <= offset {
			break
		}
		offset = resp.LastOffsetIndex
		if len(resp.ForwardingEvents) < rebalanceForwardPageSize {
			break
		}
	}

	return revenueMsat, nil
}

func (s *RebalanceService) fetchForwardRevenue24hFromNotifications(ctx context.Context, start time.Time, end time.Time) (int64, error) {
	if s.db == nil {
		return 0, nil
	}
	var feeMsat int64
	err := s.db.QueryRow(ctx, `
select coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0)
from notifications
where type='forward' and occurred_at >= $1 and occurred_at <= $2
`, start, end).Scan(&feeMsat)
	if err != nil {
		return 0, err
	}
	return msatToSatCeil(feeMsat), nil
}

func (s *RebalanceService) getDailyBudget(ctx context.Context) (int64, int64, int64, int64) {
	if s.db == nil {
		return 0, 0, 0, 0
	}
	cfg, _ := s.loadConfig(ctx)
	_ = s.ensureDailyBudget(ctx, cfg)
	refreshedAuto, refreshedManual, refreshedTotal, refreshed := s.refreshDailySpend(ctx)

	day := time.Now().In(time.Local).Format("2006-01-02")
	var budget int64
	var spentAuto int64
	var spentManual int64
	var spentTotal int64
	err := s.db.QueryRow(ctx, `
select budget_sat, spent_auto_sat, spent_manual_sat, spent_sat
from rebalance_budget_daily
where day=$1`, day).Scan(&budget, &spentAuto, &spentManual, &spentTotal)
	if err != nil {
		return 0, 0, 0, 0
	}
	if refreshed {
		return budget, refreshedAuto, refreshedManual, refreshedTotal
	}
	if spentTotal <= 0 {
		spentTotal = spentAuto + spentManual
	}
	return budget, spentAuto, spentManual, spentTotal
}

func (s *RebalanceService) addBudgetSpend(ctx context.Context, amountSat int64, source string) error {
	if s.db == nil || amountSat <= 0 {
		return nil
	}
	day := time.Now().In(time.Local).Format("2006-01-02")
	autoSpend := int64(0)
	manualSpend := int64(0)
	if strings.EqualFold(source, "auto") {
		autoSpend = amountSat
	} else {
		manualSpend = amountSat
	}
	_, err := s.db.Exec(ctx, `
insert into rebalance_budget_daily (day, budget_sat, spent_auto_sat, spent_manual_sat, spent_sat, updated_at)
values ($1,0,$2,$3,$4,now())
 on conflict (day) do update set
  spent_auto_sat=rebalance_budget_daily.spent_auto_sat + excluded.spent_auto_sat,
  spent_manual_sat=rebalance_budget_daily.spent_manual_sat + excluded.spent_manual_sat,
  spent_sat=rebalance_budget_daily.spent_sat + excluded.spent_sat,
  updated_at=now()
  `, day, autoSpend, manualSpend, amountSat)
	return err
}

func (s *RebalanceService) refreshDailySpend(ctx context.Context) (int64, int64, int64, bool) {
	if s.db == nil {
		return 0, 0, 0, false
	}

	loc := time.Local
	now := time.Now().In(loc)
	dayStart := now.Add(-24 * time.Hour)
	dayEnd := now

	var autoMsat int64
	var manualMsat int64
	err := s.db.QueryRow(ctx, `
select
  coalesce(sum(case when source='auto' then fee_msat else 0 end), 0) as auto_msat,
  coalesce(sum(case when source='manual' then fee_msat else 0 end), 0) as manual_msat
from (
  select j.source as source,
    case
      when n.fee_msat > 0 then n.fee_msat
      when n.fee_sat > 0 then n.fee_sat * 1000
      when a.fee_paid_sat > 0 then a.fee_paid_sat * 1000
      else 0
    end as fee_msat
  from rebalance_attempts a
  join rebalance_jobs j on j.id = a.job_id
  left join notifications n on n.payment_hash = a.payment_hash and n.type='rebalance'
  where a.status in ('succeeded','partial')
    and coalesce(n.occurred_at, a.finished_at, a.started_at) >= $1
    and coalesce(n.occurred_at, a.finished_at, a.started_at) <= $2
) t
`, dayStart, dayEnd).Scan(&autoMsat, &manualMsat)
	if err != nil {
		return 0, 0, 0, false
	}

	autoSat := msatToSatCeil(autoMsat)
	manualSat := msatToSatCeil(manualMsat)
	totalSat := autoSat + manualSat

	dayKey := now.Format("2006-01-02")
	_, _ = s.db.Exec(ctx, `
update rebalance_budget_daily
set spent_auto_sat=$2,
  spent_manual_sat=$3,
  spent_sat=$4,
  updated_at=now()
where day=$1
`, dayKey, autoSat, manualSat, totalSat)

	return autoSat, manualSat, totalSat, true
}

func (s *RebalanceService) fetchAttemptTelemetry24h(ctx context.Context, minAmountSat int64) (rebalanceAttemptTelemetry24h, error) {
	metrics := rebalanceAttemptTelemetry24h{}
	if s.db == nil {
		return metrics, nil
	}
	end := time.Now().In(time.Local)
	start := end.Add(-24 * time.Hour)
	var successAttempts int64
	var successAmountSat int64
	var belowAttempts int64
	var belowAmountSat int64
	err := s.db.QueryRow(ctx, `
select
  coalesce(sum(case when status='succeeded' then 1 else 0 end), 0) as success_attempts,
  coalesce(sum(case when status='succeeded' then amount_sat else 0 end), 0) as success_amount_sat,
  coalesce(sum(case when status='succeeded' and $3 > 0 and amount_sat < $3 then 1 else 0 end), 0) as success_below_min_attempts,
  coalesce(sum(case when status='succeeded' and $3 > 0 and amount_sat < $3 then amount_sat else 0 end), 0) as success_below_min_amount_sat
from rebalance_attempts
where coalesce(finished_at, started_at) >= $1
  and coalesce(finished_at, started_at) <= $2
`, start, end, minAmountSat).Scan(&successAttempts, &successAmountSat, &belowAttempts, &belowAmountSat)
	if err != nil {
		return metrics, err
	}
	metrics.SuccessAttempts = successAttempts
	metrics.SuccessAmountSat = successAmountSat
	metrics.SuccessBelowMinAttempts = belowAttempts
	metrics.SuccessBelowMinAmountSat = belowAmountSat
	if successAttempts > 0 {
		metrics.SuccessAvgAmountSat = successAmountSat / successAttempts
		metrics.SuccessBelowMinRate = float64(belowAttempts) / float64(successAttempts)
	}
	return metrics, nil
}

func (s *RebalanceService) insertMppShadowPlan(ctx context.Context, jobID int64, targetChannelID uint64, jobSource string, cfg RebalanceConfig, targetAmountSat int64, plan mppShadowPlan) error {
	if s.db == nil || jobID <= 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
insert into rebalance_mpp_shadow (
  job_id, target_channel_id, job_source,
  mpp_enabled, mpp_auto_only, max_shards, parallelism, min_shard_sat, round_timeout_sec,
  target_amount_sat, eligible_sources, planned_sources, planned_shards, planned_total_sat, planned_remainder_sat, updated_at
) values (
  $1,$2,$3,
  $4,$5,$6,$7,$8,$9,
  $10,$11,$12,$13,$14,$15,now()
)
on conflict (job_id) do update set
  target_channel_id = excluded.target_channel_id,
  job_source = excluded.job_source,
  mpp_enabled = excluded.mpp_enabled,
  mpp_auto_only = excluded.mpp_auto_only,
  max_shards = excluded.max_shards,
  parallelism = excluded.parallelism,
  min_shard_sat = excluded.min_shard_sat,
  round_timeout_sec = excluded.round_timeout_sec,
  target_amount_sat = excluded.target_amount_sat,
  eligible_sources = excluded.eligible_sources,
  planned_sources = excluded.planned_sources,
  planned_shards = excluded.planned_shards,
  planned_total_sat = excluded.planned_total_sat,
  planned_remainder_sat = excluded.planned_remainder_sat,
  updated_at = now()
`, jobID, int64(targetChannelID), jobSource,
		cfg.MppEnabled, cfg.MppAutoOnly, cfg.MppMaxShards, cfg.MppParallelism, cfg.MppMinShardSat, cfg.MppRoundTimeoutSec,
		targetAmountSat, plan.EligibleSources, plan.PlannedSources, plan.PlannedShards, plan.PlannedTotalSat, plan.PlannedRemainderSat)
	return err
}

func (s *RebalanceService) updateMppShadowFloorBlockedSources(ctx context.Context, jobID int64, blockedSources int64) error {
	if s.db == nil || jobID <= 0 {
		return nil
	}
	if blockedSources < 0 {
		blockedSources = 0
	}
	_, err := s.db.Exec(ctx, `
update rebalance_mpp_shadow
set actual_floor_blocked_sources=$2,
  updated_at=now()
where job_id=$1
`, jobID, blockedSources)
	return err
}

func (s *RebalanceService) finalizeMppShadowPlan(ctx context.Context, jobID int64) error {
	if s.db == nil || jobID <= 0 {
		return nil
	}
	var status string
	if err := s.db.QueryRow(ctx, `select status from rebalance_jobs where id=$1`, jobID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	var attempts int64
	var successAttempts int64
	var sentSat int64
	var feeSat int64
	if err := s.db.QueryRow(ctx, `
select
  count(*) as attempts,
  coalesce(sum(case when status='succeeded' then 1 else 0 end), 0) as success_attempts,
  coalesce(sum(case when status='succeeded' then amount_sat else 0 end), 0) as sent_sat,
  coalesce(sum(case when status='succeeded' then fee_paid_sat else 0 end), 0) as fee_sat
from rebalance_attempts
where job_id=$1
`, jobID).Scan(&attempts, &successAttempts, &sentSat, &feeSat); err != nil {
		return err
	}

	anySuccess := successAttempts > 0
	_, err := s.db.Exec(ctx, `
update rebalance_mpp_shadow
set actual_status=$2,
  actual_attempts=$3,
  actual_success_attempts=$4,
  actual_sent_sat=$5,
  actual_fee_sat=$6,
  actual_any_success=$7,
  updated_at=now()
where job_id=$1
`, jobID, status, attempts, successAttempts, sentSat, feeSat, anySuccess)
	return err
}

func (s *RebalanceService) fetchMppShadowTelemetry24h(ctx context.Context) (mppShadowTelemetry24h, error) {
	metrics := mppShadowTelemetry24h{}
	if s.db == nil {
		return metrics, nil
	}
	err := s.db.QueryRow(ctx, `
select
  count(*) as jobs,
  coalesce(sum(case when planned_shards > 0 then 1 else 0 end), 0) as plan_ready_jobs,
  coalesce(sum(planned_total_sat), 0) as planned_sat,
  coalesce(sum(actual_sent_sat), 0) as actual_sent_sat,
  coalesce(sum(case when actual_status is null or actual_status in ('queued','running') then 1 else 0 end), 0) as in_progress_jobs,
  coalesce(sum(case when actual_any_success then 1 else 0 end), 0) as success_jobs,
  coalesce(sum(case when actual_status='failed' then 1 else 0 end), 0) as failed_jobs,
  coalesce(sum(case when actual_status='partial' then 1 else 0 end), 0) as partial_jobs,
  coalesce(sum(actual_floor_blocked_sources), 0) as floor_blocked_sources,
  coalesce(avg(case when planned_shards > 0 then planned_shards::double precision else null end), 0) as avg_planned_shards,
  coalesce(avg(case when actual_attempts > 0 then actual_attempts::double precision else null end), 0) as avg_actual_attempts
from rebalance_mpp_shadow
where created_at >= now() - interval '24 hours'
`).Scan(
		&metrics.Jobs,
		&metrics.PlanReadyJobs,
		&metrics.PlannedSat,
		&metrics.ActualSentSat,
		&metrics.InProgressJobs,
		&metrics.SuccessJobs,
		&metrics.FailedJobs,
		&metrics.PartialJobs,
		&metrics.FloorBlocked,
		&metrics.AvgPlannedShards,
		&metrics.AvgActualAttempts,
	)
	return metrics, err
}

func (s *RebalanceService) insertJob(ctx context.Context, target *lndclient.ChannelInfo, source string, reason string, targetPct float64, amount int64) (int64, error) {
	if s.db == nil {
		return 0, errors.New("db unavailable")
	}
	var jobID int64
	err := s.db.QueryRow(ctx, `
insert into rebalance_jobs (
  source, status, reason, target_channel_id, target_channel_point, target_outbound_pct, target_amount_sat, config_snapshot
) values ($1,'queued',$2,$3,$4,$5,$6,$7)
 returning id
`, source, nullableString(reason), int64(target.ChannelID), target.ChannelPoint, targetPct, amount, nil).Scan(&jobID)
	return jobID, err
}

func (s *RebalanceService) fetchChannelRebalanceCost7d(ctx context.Context) (map[uint64]rebalanceCost7dStat, error) {
	costs := map[uint64]rebalanceCost7dStat{}
	if s.db == nil {
		return costs, nil
	}
	rows, err := s.db.Query(ctx, `
select coalesce(rebal_target_chan_id, channel_id) as channel_id,
  coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0) as fee_msat,
  coalesce(sum(amount_sat), 0) as amount_sat
from notifications
where type='rebalance'
  and status in ('SETTLED', 'SUCCEEDED')
  and occurred_at >= now() - interval '7 days'
  and coalesce(rebal_target_chan_id, channel_id) is not null
group by coalesce(rebal_target_chan_id, channel_id)
`)
	if err != nil {
		return costs, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var feeMsat int64
		var amountSat int64
		if err := rows.Scan(&channelID, &feeMsat, &amountSat); err != nil {
			return costs, err
		}
		// channel_id is stored as bigint (signed). When the original chan_id is a
		// uint64 with the high bit set, scanning into int64 yields a negative
		// value, but uint64(channelID) still maps back to the original chan_id.
		// Only zero is invalid here.
		if channelID == 0 {
			continue
		}
		stat := rebalanceCost7dStat{
			FeeSat:    msatToSatCeil(feeMsat),
			AmountSat: amountSat,
			FeePpm:    feeMsatToPpm(feeMsat, amountSat),
		}
		costs[uint64(channelID)] = stat
	}
	return costs, rows.Err()
}

func (s *RebalanceService) fetchPaybackTotals7d(ctx context.Context) (paybackTotals7d, error) {
	if s.db == nil {
		return paybackTotals7d{}, nil
	}
	var revenueAllMsat int64
	var revenueRebalancedMsat int64
	var costMsat int64
	err := s.db.QueryRow(ctx, `
with rebalance_channels as (
  select distinct coalesce(rebal_target_chan_id, channel_id) as channel_id
  from notifications
  where type='rebalance'
    and status in ('SETTLED', 'SUCCEEDED')
    and occurred_at >= now() - interval '7 days'
    and coalesce(rebal_target_chan_id, channel_id) is not null
),
forward_fees as (
  select channel_id, case when fee_msat > 0 then fee_msat else fee_sat * 1000 end as fee_msat
  from notifications
  where type='forward'
    and occurred_at >= now() - interval '7 days'
    and channel_id is not null
)
select
  coalesce((select sum(fee_msat) from forward_fees), 0) as revenue_all_msat,
  coalesce((
    select sum(f.fee_msat)
    from forward_fees f
    join rebalance_channels r on r.channel_id = f.channel_id
  ), 0) as revenue_rebalanced_msat,
  coalesce((
    select sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end)
    from notifications
    where type='rebalance'
      and status in ('SETTLED', 'SUCCEEDED')
      and occurred_at >= now() - interval '7 days'
  ), 0) as cost_msat
`).Scan(&revenueAllMsat, &revenueRebalancedMsat, &costMsat)
	if err != nil {
		return paybackTotals7d{}, err
	}
	return paybackTotals7d{
		RevenueAllSat:        revenueAllMsat / 1000,
		RevenueRebalancedSat: revenueRebalancedMsat / 1000,
		CostSat:              msatToSatCeil(costMsat),
	}, nil
}

func (s *RebalanceService) markJobRunning(jobID int64) {
	if s.db == nil || jobID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = s.db.Exec(ctx, `
update rebalance_jobs
set status='running',
  reason=null,
  completed_at=null
where id=$1 and status='queued'`, jobID)
}

func (s *RebalanceService) insertAttempt(ctx context.Context, jobID int64, idx int, sourceChannelID uint64, amount int64, feePpm int64, feePaidSat int64, status string, paymentHash string, failReason string) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(ctx, `
insert into rebalance_attempts (
  job_id, attempt_index, source_channel_id, amount_sat, fee_limit_ppm, fee_paid_sat, status, payment_hash, fail_reason, finished_at
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
`, jobID, idx, int64(sourceChannelID), amount, feePpm, feePaidSat, status, nullableString(paymentHash), nullableString(failReason))
	return err
}

func (s *RebalanceService) Overview(ctx context.Context) (RebalanceOverview, error) {
	cfg, _ := s.loadConfig(ctx)
	budget, spentAuto, spentManual, spent := s.getDailyBudget(ctx)
	now := time.Now().In(time.Local)
	revenue24h, _ := s.fetchForwardRevenue24h(ctx, now)
	avgRevenue7d, _ := s.fetchAvgRevenue7d(ctx, now.AddDate(0, 0, -6))
	_, baseBudget, shortTermBudget := computeDailyBudgetFromRevenue(cfg, revenue24h, avgRevenue7d)
	manualReserveSat := computeManualReserveSat(cfg, budget)
	remainingTotal, remainingForAuto := computeRemainingForAuto(budget, spent, spentManual, manualReserveSat)
	manualReserveRemaining := manualReserveSat - spentManual
	if manualReserveRemaining < 0 {
		manualReserveRemaining = 0
	}
	liveCost := int64(0)
	if s.db != nil {
		var liveMsat int64
		_ = s.db.QueryRow(ctx, `
select coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0)
from notifications
where type='rebalance' and occurred_at >= now() - interval '1 day'
  and status in ('SETTLED', 'SUCCEEDED')
`).Scan(&liveMsat)
		liveCost = msatToSatCeil(liveMsat)
	}

	effectiveness := 0.0
	roi := 0.0
	paybackRevenue := int64(0)
	paybackRevenueRebalanced := int64(0)
	paybackCost := int64(0)
	paybackProgress := 0.0
	paybackProgressRebalanced := 0.0
	attemptTelemetry := rebalanceAttemptTelemetry24h{}
	mppShadowTelemetry := mppShadowTelemetry24h{}
	var successCount int64
	var totalCount int64
	var attemptedCount int64
	var jobsWithoutAttemptCount int64
	_ = s.db.QueryRow(ctx, `
with jobs_7d as (
  select id, status
  from rebalance_jobs
  where completed_at >= now() - interval '7 days'
),
jobs_with_attempts as (
  select j.id, j.status, exists (
    select 1 from rebalance_attempts a where a.job_id = j.id
  ) as has_attempt
  from jobs_7d j
)
select
  coalesce(sum(case when status in ('succeeded','partial') then 1 else 0 end), 0) as success_count,
  count(*) as total_count,
  coalesce(sum(case when has_attempt then 1 else 0 end), 0) as attempted_count,
  coalesce(sum(case when not has_attempt then 1 else 0 end), 0) as jobs_without_attempt_count
from jobs_with_attempts
`).Scan(&successCount, &totalCount, &attemptedCount, &jobsWithoutAttemptCount)
	if totalCount > 0 {
		effectiveness = float64(successCount) / float64(totalCount)
	}
	effectivenessExecution := 0.0
	if attemptedCount > 0 {
		effectivenessExecution = float64(successCount) / float64(attemptedCount)
	}
	jobsWithoutAttemptRate := 0.0
	if totalCount > 0 {
		jobsWithoutAttemptRate = float64(jobsWithoutAttemptCount) / float64(totalCount)
	}
	var revenue int64
	var cost int64
	_ = s.db.QueryRow(ctx, `
select
  coalesce(sum(forward_fee_revenue_sats), 0),
  coalesce(sum(rebalance_fee_cost_sats), 0)
from reports_daily
where report_date >= current_date - interval '6 days'
`).Scan(&revenue, &cost)
	if cost > 0 {
		roi = float64(revenue) / float64(cost)
	}
	if totals, err := s.fetchPaybackTotals7d(ctx); err == nil {
		paybackRevenue = totals.RevenueAllSat
		paybackRevenueRebalanced = totals.RevenueRebalancedSat
		paybackCost = totals.CostSat
	}
	if paybackCost > 0 {
		paybackProgress = float64(paybackRevenue) / float64(paybackCost)
		paybackProgressRebalanced = float64(paybackRevenueRebalanced) / float64(paybackCost)
	}
	if telemetry, err := s.fetchAttemptTelemetry24h(ctx, effectiveMinExecuteSat(cfg)); err == nil {
		attemptTelemetry = telemetry
	}
	if telemetry, err := s.fetchMppShadowTelemetry24h(ctx); err == nil {
		mppShadowTelemetry = telemetry
	}

	eligibleSources := 0
	targetsNeeding := 0
	if s.lnd != nil {
		eligibleSources, targetsNeeding = s.computeEligibilityCounts(ctx, cfg)
	}

	s.mu.Lock()
	lastScan := s.lastScan
	lastScanStatus := s.lastScanStatus
	lastScanDetail := s.lastScanDetail
	lastScanCandidates := s.lastScanCandidates
	lastScanRemainingBudgetSat := s.lastScanRemainingBudgetSat
	lastScanReasons := map[string]int{}
	for key, value := range s.lastScanReasons {
		lastScanReasons[key] = value
	}
	lastScanTopScore := s.lastScanTopScoreSat
	lastScanProfitSkipped := s.lastScanProfitSkipped
	lastScanQueued := s.lastScanQueued
	lastScanSkipped := append([]RebalanceSkipDetail(nil), s.lastScanSkipped...)
	s.mu.Unlock()

	overview := RebalanceOverview{
		AutoEnabled:                   cfg.AutoEnabled,
		DailyBudgetSat:                budget,
		DailyBudgetBaseSat:            baseBudget,
		DailyBudgetShortTermSat:       shortTermBudget,
		DailySpentSat:                 spent,
		DailySpentAutoSat:             spentAuto,
		DailySpentManualSat:           spentManual,
		RemainingTotalSat:             remainingTotal,
		RemainingForAutoSat:           remainingForAuto,
		ManualReserveEnabled:          cfg.ManualReserveEnabled,
		ManualReserveMode:             cfg.ManualReserveMode,
		ManualReserveValue:            cfg.ManualReserveValue,
		ManualReserveSat:              manualReserveSat,
		ManualReserveRemainingSat:     manualReserveRemaining,
		LiveCostSat:                   liveCost,
		Effectiveness7d:               effectiveness,
		EffectivenessExecution7d:      effectivenessExecution,
		JobsWithoutAttempt7d:          jobsWithoutAttemptCount,
		JobsWithoutAttemptRate7d:      jobsWithoutAttemptRate,
		ROI7d:                         roi,
		SuccessAttempts24h:            attemptTelemetry.SuccessAttempts,
		SuccessAmount24hSat:           attemptTelemetry.SuccessAmountSat,
		SuccessAvgAmount24hSat:        attemptTelemetry.SuccessAvgAmountSat,
		SuccessBelowMinAttempts24h:    attemptTelemetry.SuccessBelowMinAttempts,
		SuccessBelowMinAmount24hSat:   attemptTelemetry.SuccessBelowMinAmountSat,
		SuccessBelowMinRate24h:        attemptTelemetry.SuccessBelowMinRate,
		PaybackRevenueSat:             paybackRevenue,
		PaybackRevenueRebalancedSat:   paybackRevenueRebalanced,
		PaybackCostSat:                paybackCost,
		PaybackProgress:               paybackProgress,
		PaybackProgressRebalanced:     paybackProgressRebalanced,
		LastScanStatus:                lastScanStatus,
		LastScanDetail:                lastScanDetail,
		LastScanCandidates:            lastScanCandidates,
		LastScanRemainingBudgetSat:    lastScanRemainingBudgetSat,
		LastScanReasons:               lastScanReasons,
		LastScanTopScoreSat:           lastScanTopScore,
		LastScanProfitSkipped:         lastScanProfitSkipped,
		LastScanQueued:                lastScanQueued,
		LastScanSkipped:               lastScanSkipped,
		EligibleSources:               eligibleSources,
		TargetsNeeding:                targetsNeeding,
		MppShadowJobs24h:              mppShadowTelemetry.Jobs,
		MppShadowPlanReady24h:         mppShadowTelemetry.PlanReadyJobs,
		MppShadowPlannedSat24h:        mppShadowTelemetry.PlannedSat,
		MppShadowActualSentSat24h:     mppShadowTelemetry.ActualSentSat,
		MppShadowInProgressJobs24h:    mppShadowTelemetry.InProgressJobs,
		MppShadowSuccessJobs24h:       mppShadowTelemetry.SuccessJobs,
		MppShadowFailedJobs24h:        mppShadowTelemetry.FailedJobs,
		MppShadowPartialJobs24h:       mppShadowTelemetry.PartialJobs,
		MppShadowFloorBlocked24h:      mppShadowTelemetry.FloorBlocked,
		MppShadowAvgPlannedShards24h:  mppShadowTelemetry.AvgPlannedShards,
		MppShadowAvgActualAttempts24h: mppShadowTelemetry.AvgActualAttempts,
	}
	if !lastScan.IsZero() {
		overview.LastScanAt = lastScan.UTC().Format(time.RFC3339)
	}
	return overview, nil
}

func (s *RebalanceService) Channels(ctx context.Context) ([]RebalanceChannel, error) {
	cfg, _ := s.loadConfig(ctx)
	s.mu.Lock()
	criticalActive := cfg.CriticalCycles > 0 && s.criticalMissCount >= cfg.CriticalCycles
	s.mu.Unlock()
	settings, _ := s.loadChannelSettings(ctx)
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx)
	_ = s.applyForwardDeltas(ctx, ledger)

	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	s.reconcileNewChannelDefaults(ctx, channels, settings, exclusions)
	result := []RebalanceChannel{}
	seenIDs := map[uint64]bool{}
	seenPoints := map[string]bool{}
	for _, ch := range channels {
		if ch.ChannelID != 0 {
			if seenIDs[ch.ChannelID] {
				continue
			}
			seenIDs[ch.ChannelID] = true
		}
		if point := strings.TrimSpace(ch.ChannelPoint); point != "" {
			if seenPoints[point] {
				continue
			}
			seenPoints[point] = true
		}
		setting := settings[ch.ChannelID]
		snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		result = append(result, snapshot)
	}
	return result, nil
}

func (s *RebalanceService) computeEligibilityCounts(ctx context.Context, cfg RebalanceConfig) (int, int) {
	if s.lnd == nil {
		return 0, 0
	}
	settings, _ := s.loadChannelSettings(ctx)
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx)
	_ = s.applyForwardDeltas(ctx, ledger)

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return 0, 0
	}
	s.reconcileNewChannelDefaults(ctx, channels, settings, exclusions)
	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)

	s.mu.Lock()
	criticalActive := cfg.CriticalCycles > 0 && s.criticalMissCount >= cfg.CriticalCycles
	s.mu.Unlock()

	eligibleSources := 0
	targetsNeeding := 0
	for _, ch := range channels {
		setting := settings[ch.ChannelID]
		snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		if snapshot.EligibleAsSource {
			eligibleSources++
		}
		if setting.AutoEnabled && snapshot.EligibleAsTarget && (cfg.ROIMin <= 0 || !snapshot.ROIEstimateValid || snapshot.ROIEstimate >= cfg.ROIMin) {
			targetsNeeding++
		}
	}
	return eligibleSources, targetsNeeding
}

func (s *RebalanceService) loadLastAutoEnqueueTimes(ctx context.Context) map[uint64]time.Time {
	result := map[uint64]time.Time{}
	if s.db == nil {
		return result
	}
	rows, err := s.db.Query(ctx, `
select target_channel_id, max(created_at)
from rebalance_jobs
where source='auto'
group by target_channel_id
`)
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var last time.Time
		if err := rows.Scan(&channelID, &last); err != nil {
			return result
		}
		if channelID > 0 {
			result[uint64(channelID)] = last
		}
	}
	return result
}

func (s *RebalanceService) Queue(ctx context.Context) ([]RebalanceJob, []RebalanceAttempt, error) {
	jobs := []RebalanceJob{}
	attempts := []RebalanceAttempt{}
	if s.db == nil {
		return jobs, attempts, nil
	}
	s.cleanupStaleJobs(ctx)
	aliasMap := map[uint64]string{}
	if s.lnd != nil {
		if channels, err := s.lnd.ListChannels(ctx); err == nil {
			for _, ch := range channels {
				if ch.ChannelID != 0 && ch.PeerAlias != "" {
					aliasMap[ch.ChannelID] = ch.PeerAlias
				}
			}
		}
	}
	rows, err := s.db.Query(ctx, `
select id, created_at, completed_at, source, status, reason, target_channel_id,
  target_channel_point, target_outbound_pct, target_amount_sat
from rebalance_jobs
where status in ('running','queued')
   or completed_at >= now() - ($1::int * interval '1 second')
order by created_at desc
`, queueLingerSeconds)
	if err != nil {
		return jobs, attempts, err
	}
	defer rows.Close()
	for rows.Next() {
		var job RebalanceJob
		var created time.Time
		var completed pgtype.Timestamptz
		var reason pgtype.Text
		var targetChannelID int64
		if err := rows.Scan(&job.ID, &created, &completed, &job.Source, &job.Status, &reason, &targetChannelID,
			&job.TargetChannelPoint, &job.TargetOutboundPct, &job.TargetAmountSat); err != nil {
			return jobs, attempts, err
		}
		job.CreatedAt = created.UTC().Format(time.RFC3339)
		if completed.Valid {
			job.CompletedAt = completed.Time.UTC().Format(time.RFC3339)
		}
		if reason.Valid {
			job.Reason = reason.String
		}
		job.TargetChannelID = uint64(targetChannelID)
		job.TargetPeerAlias = aliasMap[job.TargetChannelID]
		jobs = append(jobs, job)
	}

	attemptRows, err := s.db.Query(ctx, `
select id, job_id, attempt_index, source_channel_id, amount_sat, fee_limit_ppm,
  fee_paid_sat, status, payment_hash, fail_reason, started_at, finished_at
from rebalance_attempts
where job_id in (
  select id
  from rebalance_jobs
  where status in ('running','queued')
     or completed_at >= now() - ($1::int * interval '1 second')
)
order by started_at desc
`, queueLingerSeconds)
	if err != nil {
		return jobs, attempts, err
	}
	defer attemptRows.Close()
	for attemptRows.Next() {
		var attempt RebalanceAttempt
		var sourceChannelID int64
		var started time.Time
		var finished pgtype.Timestamptz
		var paymentHash pgtype.Text
		var failReason pgtype.Text
		if err := attemptRows.Scan(&attempt.ID, &attempt.JobID, &attempt.AttemptIndex, &sourceChannelID, &attempt.AmountSat, &attempt.FeeLimitPpm,
			&attempt.FeePaidSat, &attempt.Status, &paymentHash, &failReason, &started, &finished); err != nil {
			return jobs, attempts, err
		}
		attempt.SourceChannelID = uint64(sourceChannelID)
		attempt.SourcePeerAlias = aliasMap[attempt.SourceChannelID]
		attempt.StartedAt = started.UTC().Format(time.RFC3339)
		if finished.Valid {
			attempt.FinishedAt = finished.Time.UTC().Format(time.RFC3339)
		}
		if paymentHash.Valid {
			attempt.PaymentHash = paymentHash.String
		}
		if failReason.Valid {
			attempt.FailReason = failReason.String
		}
		attempts = append(attempts, attempt)
	}

	return jobs, attempts, nil
}

func (s *RebalanceService) cleanupStaleJobs(ctx context.Context) {
	if s.db == nil {
		return
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		cfg = defaultRebalanceConfig()
	}
	timeoutSec := cfg.RebalanceTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	cutoff := time.Now().Add(-time.Duration(timeoutSec) * time.Second)
	_, _ = s.db.Exec(ctx, `
update rebalance_jobs
set status = case
  when exists (select 1 from rebalance_attempts a where a.job_id=rebalance_jobs.id and a.status='succeeded')
    then 'partial'
  else 'failed'
end,
reason = case
  when exists (select 1 from rebalance_attempts a where a.job_id=rebalance_jobs.id and a.status='succeeded')
    then 'timeout (partial)'
  else 'timeout'
end,
completed_at=now()
where status in ('running','queued') and created_at < $1
`, cutoff)
}

func (s *RebalanceService) History(ctx context.Context, limit int) ([]RebalanceJob, []RebalanceAttempt, error) {
	jobs := []RebalanceJob{}
	attempts := []RebalanceAttempt{}
	if s.db == nil {
		return jobs, attempts, nil
	}
	var err error
	aliasMap := map[uint64]string{}
	if s.lnd != nil {
		if channels, err := s.lnd.ListChannels(ctx); err == nil {
			for _, ch := range channels {
				if ch.ChannelID != 0 && ch.PeerAlias != "" {
					aliasMap[ch.ChannelID] = ch.PeerAlias
				}
			}
		}
	}
	if limit < 0 {
		limit = 0
	}
	baseQuery := `
select id, created_at, completed_at, source, status, reason, target_channel_id,
  target_channel_point, target_outbound_pct, target_amount_sat
from rebalance_jobs
where status in ('succeeded','failed','cancelled','partial')
  and completed_at >= now() - interval '1 day'
order by created_at desc`
	var rows pgx.Rows
	if limit > 0 {
		rows, err = s.db.Query(ctx, baseQuery+"\nlimit $1", limit)
	} else {
		rows, err = s.db.Query(ctx, baseQuery)
	}
	if err != nil {
		return jobs, attempts, err
	}
	defer rows.Close()
	for rows.Next() {
		var job RebalanceJob
		var created time.Time
		var completed pgtype.Timestamptz
		var reason pgtype.Text
		var targetChannelID int64
		if err := rows.Scan(&job.ID, &created, &completed, &job.Source, &job.Status, &reason, &targetChannelID,
			&job.TargetChannelPoint, &job.TargetOutboundPct, &job.TargetAmountSat); err != nil {
			return jobs, attempts, err
		}
		job.CreatedAt = created.UTC().Format(time.RFC3339)
		if completed.Valid {
			job.CompletedAt = completed.Time.UTC().Format(time.RFC3339)
		}
		if reason.Valid {
			job.Reason = reason.String
		}
		job.TargetChannelID = uint64(targetChannelID)
		job.TargetPeerAlias = aliasMap[job.TargetChannelID]
		jobs = append(jobs, job)
	}

	attemptBase := `
with recent as (
  select id from rebalance_jobs
  where status in ('succeeded','failed','cancelled','partial')
    and completed_at >= now() - interval '1 day'
  order by created_at desc
)
select id, job_id, attempt_index, source_channel_id, amount_sat, fee_limit_ppm,
  fee_paid_sat, status, payment_hash, fail_reason, started_at, finished_at
from rebalance_attempts
where job_id in (select id from recent)
order by started_at desc`
	var attemptRows pgx.Rows
	if limit > 0 {
		attemptRows, err = s.db.Query(ctx, attemptBase+"\nlimit $1", limit)
	} else {
		attemptRows, err = s.db.Query(ctx, attemptBase)
	}
	if err != nil {
		return jobs, attempts, err
	}
	defer attemptRows.Close()
	for attemptRows.Next() {
		var attempt RebalanceAttempt
		var sourceChannelID int64
		var started time.Time
		var finished pgtype.Timestamptz
		var paymentHash pgtype.Text
		var failReason pgtype.Text
		if err := attemptRows.Scan(&attempt.ID, &attempt.JobID, &attempt.AttemptIndex, &sourceChannelID, &attempt.AmountSat, &attempt.FeeLimitPpm,
			&attempt.FeePaidSat, &attempt.Status, &paymentHash, &failReason, &started, &finished); err != nil {
			return jobs, attempts, err
		}
		attempt.SourceChannelID = uint64(sourceChannelID)
		attempt.SourcePeerAlias = aliasMap[attempt.SourceChannelID]
		attempt.StartedAt = started.UTC().Format(time.RFC3339)
		if finished.Valid {
			attempt.FinishedAt = finished.Time.UTC().Format(time.RFC3339)
		}
		if paymentHash.Valid {
			attempt.PaymentHash = paymentHash.String
		}
		if failReason.Valid {
			attempt.FailReason = failReason.String
		}
		attempts = append(attempts, attempt)
	}

	return jobs, attempts, nil
}

func (s *RebalanceService) SetChannelTarget(ctx context.Context, channelID uint64, channelPoint string, targetPct float64) error {
	return s.UpdateChannelTargetSettings(ctx, channelID, channelPoint, &targetPct, nil, nil)
}

func (s *RebalanceService) UpdateChannelTargetSettings(ctx context.Context, channelID uint64, channelPoint string, targetPct *float64, useDefaultEconRatio *bool, econRatioOverride *float64) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if targetPct != nil && (*targetPct <= 0 || *targetPct > 100) {
		return errors.New("target outbound must be between 1 and 100")
	}
	if econRatioOverride != nil && (*econRatioOverride < 0.01 || *econRatioOverride > 0.99) {
		return errors.New("econ ratio override must be between 0.01 and 0.99")
	}

	current := normalizeChannelSetting(channelSetting{
		ChannelID:            channelID,
		ChannelPoint:         channelPoint,
		TargetOutboundPct:    rebalanceDefaultTargetOutboundPct,
		AutoEnabled:          false,
		ManualRestartEnabled: false,
		UseDefaultEconRatio:  true,
	})
	var currentOverride pgtype.Float8
	err := s.db.QueryRow(ctx, `
select channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, econ_ratio_override
from rebalance_channel_settings
where channel_id=$1
`, int64(channelID)).Scan(&current.ChannelPoint, &current.TargetOutboundPct, &current.AutoEnabled, &current.ManualRestartEnabled, &current.UseDefaultEconRatio, &currentOverride)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if currentOverride.Valid {
		current.EconRatioOverride = currentOverride.Float64
		current.EconRatioOverrideSet = true
	}
	current = normalizeChannelSetting(current)
	if strings.TrimSpace(channelPoint) != "" {
		current.ChannelPoint = channelPoint
	}

	if targetPct != nil {
		current.TargetOutboundPct = *targetPct
	}
	if useDefaultEconRatio != nil {
		current.UseDefaultEconRatio = *useDefaultEconRatio
	}
	if econRatioOverride != nil {
		current.EconRatioOverride = *econRatioOverride
		current.EconRatioOverrideSet = true
	}

	if current.UseDefaultEconRatio {
		current.EconRatioOverride = 0
		current.EconRatioOverrideSet = false
	} else if !current.EconRatioOverrideSet {
		return errors.New("econ ratio override required when not using default")
	} else if current.EconRatioOverride < 0.01 || current.EconRatioOverride > 0.99 {
		return errors.New("econ ratio override must be between 0.01 and 0.99")
	}

	var overrideValue any
	if current.EconRatioOverrideSet {
		overrideValue = current.EconRatioOverride
	} else {
		overrideValue = nil
	}

	_, err = s.db.Exec(ctx, `
insert into rebalance_channel_settings (
  channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, econ_ratio_override, updated_at
)
values ($1,$2,$3,$4,$5,$6,$7,now())
 on conflict (channel_id) do update set
  channel_point=excluded.channel_point,
  target_outbound_pct=excluded.target_outbound_pct,
  auto_enabled=excluded.auto_enabled,
  manual_restart_enabled=excluded.manual_restart_enabled,
  use_default_econ_ratio=excluded.use_default_econ_ratio,
  econ_ratio_override=excluded.econ_ratio_override,
  updated_at=now()
`, int64(channelID), current.ChannelPoint, current.TargetOutboundPct, current.AutoEnabled, current.ManualRestartEnabled, current.UseDefaultEconRatio, overrideValue)
	return err
}

func (s *RebalanceService) SetChannelAuto(ctx context.Context, channelID uint64, channelPoint string, autoEnabled bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if autoEnabled {
		s.cancelManualRestart(channelID)
	}
	_, err := s.db.Exec(ctx, `
  insert into rebalance_channel_settings (channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, updated_at)
  values ($1,$2,$3,$4,false,now())
   on conflict (channel_id) do update set
     channel_point=excluded.channel_point,
     auto_enabled=excluded.auto_enabled,
     manual_restart_enabled=case when excluded.auto_enabled then false else rebalance_channel_settings.manual_restart_enabled end,
     updated_at=now()
  `, int64(channelID), channelPoint, rebalanceDefaultTargetOutboundPct, autoEnabled)
	return err
}

func (s *RebalanceService) SetChannelManualRestart(ctx context.Context, channelID uint64, channelPoint string, enabled bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if !enabled {
		s.cancelManualRestart(channelID)
	}
	_, err := s.db.Exec(ctx, `
  insert into rebalance_channel_settings (channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, updated_at)
  values ($1,$2,$3,false,$4,now())
   on conflict (channel_id) do update set
     channel_point=excluded.channel_point,
     auto_enabled=case when excluded.manual_restart_enabled then false else rebalance_channel_settings.auto_enabled end,
     manual_restart_enabled=excluded.manual_restart_enabled,
     updated_at=now()
  `, int64(channelID), channelPoint, rebalanceDefaultTargetOutboundPct, enabled)
	return err
}

func (s *RebalanceService) SetSourceExcluded(ctx context.Context, channelID uint64, channelPoint string, excluded bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if excluded {
		_, err := s.db.Exec(ctx, `
insert into rebalance_source_exclusions (channel_id, channel_point, reason)
values ($1,$2,$3)
 on conflict (channel_id) do update set channel_point=excluded.channel_point, reason=excluded.reason
`, int64(channelID), channelPoint, "user")
		return err
	}
	_, err := s.db.Exec(ctx, `delete from rebalance_source_exclusions where channel_id=$1`, int64(channelID))
	return err
}
