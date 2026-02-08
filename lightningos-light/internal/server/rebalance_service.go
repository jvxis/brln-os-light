
package server

import (
  "context"
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
  rebalanceConfigID = 1
  rebalanceDefaultTargetOutboundPct = 50.0
  rebalanceForwardPageSize = 50000
)

const (
  pairFailTTL = 6 * time.Hour
  pairSuccessTTL = 24 * time.Hour
)

const (
  paybackModePayback  = 1 << 0
  paybackModeTime     = 1 << 1
  paybackModeCritical = 1 << 2
)

type RebalanceConfig struct {
  AutoEnabled bool `json:"auto_enabled"`
  ScanIntervalSec int `json:"scan_interval_sec"`
  DeadbandPct float64 `json:"deadband_pct"`
  SourceMinLocalPct float64 `json:"source_min_local_pct"`
  EconRatio float64 `json:"econ_ratio"`
  ROIMin float64 `json:"roi_min"`
  DailyBudgetPct float64 `json:"daily_budget_pct"`
  MaxConcurrent int `json:"max_concurrent"`
  MinAmountSat int64 `json:"min_amount_sat"`
  MaxAmountSat int64 `json:"max_amount_sat"`
  FeeLadderSteps int `json:"fee_ladder_steps"`
  AmountProbeSteps int `json:"amount_probe_steps"`
  AmountProbeAdaptive bool `json:"amount_probe_adaptive"`
  AttemptTimeoutSec int `json:"attempt_timeout_sec"`
  RebalanceTimeoutSec int `json:"rebalance_timeout_sec"`
  PaybackModeFlags int `json:"payback_mode_flags"`
  UnlockDays int `json:"unlock_days"`
  CriticalReleasePct float64 `json:"critical_release_pct"`
  CriticalMinSources int `json:"critical_min_sources"`
  CriticalMinAvailableSats int64 `json:"critical_min_available_sats"`
  CriticalCycles int `json:"critical_cycles"`
}

type RebalanceOverview struct {
  AutoEnabled bool `json:"auto_enabled"`
  LastScanAt string `json:"last_scan_at,omitempty"`
  LastScanStatus string `json:"last_scan_status,omitempty"`
  LastScanTopScoreSat int64 `json:"last_scan_top_score_sat"`
  LastScanProfitSkipped int `json:"last_scan_profit_skipped"`
  DailyBudgetSat int64 `json:"daily_budget_sat"`
  DailySpentSat int64 `json:"daily_spent_sat"`
  DailySpentAutoSat int64 `json:"daily_spent_auto_sat"`
  DailySpentManualSat int64 `json:"daily_spent_manual_sat"`
  LiveCostSat int64 `json:"live_cost_sat"`
  Effectiveness7d float64 `json:"effectiveness_7d"`
  ROI7d float64 `json:"roi_7d"`
  EligibleSources int `json:"eligible_sources"`
  TargetsNeeding int `json:"targets_needing"`
}

type RebalanceChannel struct {
  ChannelID uint64 `json:"channel_id"`
  ChannelPoint string `json:"channel_point"`
  PeerAlias string `json:"peer_alias"`
  RemotePubkey string `json:"remote_pubkey"`
  Active bool `json:"active"`
  Private bool `json:"private"`
  CapacitySat int64 `json:"capacity_sat"`
  LocalBalanceSat int64 `json:"local_balance_sat"`
  RemoteBalanceSat int64 `json:"remote_balance_sat"`
  LocalPct float64 `json:"local_pct"`
  RemotePct float64 `json:"remote_pct"`
  OutgoingFeePpm int64 `json:"outgoing_fee_ppm"`
  PeerFeeRatePpm int64 `json:"peer_fee_rate_ppm"`
  SpreadPpm int64 `json:"spread_ppm"`
  TargetOutboundPct float64 `json:"target_outbound_pct"`
  TargetAmountSat int64 `json:"target_amount_sat"`
  AutoEnabled bool `json:"auto_enabled"`
  EligibleAsTarget bool `json:"eligible_as_target"`
  EligibleAsSource bool `json:"eligible_as_source"`
  ProtectedLiquiditySat int64 `json:"protected_liquidity_sat"`
  PaybackProgress float64 `json:"payback_progress"`
  MaxSourceSat int64 `json:"max_source_sat"`
  Revenue7dSat int64 `json:"revenue_7d_sat"`
  ROIEstimate float64 `json:"roi_estimate"`
  ROIEstimateValid bool `json:"roi_estimate_valid"`
  ExcludedAsSource bool `json:"excluded_as_source"`
}

type RebalanceJob struct {
  ID int64 `json:"id"`
  CreatedAt string `json:"created_at"`
  CompletedAt string `json:"completed_at,omitempty"`
  Source string `json:"source"`
  Status string `json:"status"`
  Reason string `json:"reason,omitempty"`
  TargetChannelID uint64 `json:"target_channel_id"`
  TargetChannelPoint string `json:"target_channel_point"`
  TargetOutboundPct float64 `json:"target_outbound_pct"`
  TargetAmountSat int64 `json:"target_amount_sat"`
  TargetPeerAlias string `json:"target_peer_alias,omitempty"`
}

type RebalanceAttempt struct {
  ID int64 `json:"id"`
  JobID int64 `json:"job_id"`
  AttemptIndex int `json:"attempt_index"`
  SourceChannelID uint64 `json:"source_channel_id"`
  AmountSat int64 `json:"amount_sat"`
  FeeLimitPpm int64 `json:"fee_limit_ppm"`
  FeePaidSat int64 `json:"fee_paid_sat"`
  Status string `json:"status"`
  PaymentHash string `json:"payment_hash,omitempty"`
  FailReason string `json:"fail_reason,omitempty"`
  StartedAt string `json:"started_at,omitempty"`
  FinishedAt string `json:"finished_at,omitempty"`
}

type RebalanceEvent struct {
  Type string `json:"type"`
  JobID int64 `json:"job_id,omitempty"`
  Status string `json:"status,omitempty"`
  Message string `json:"message,omitempty"`
}

type channelSetting struct {
  ChannelID uint64
  ChannelPoint string
  TargetOutboundPct float64
  AutoEnabled bool
}

type channelLedger struct {
  ChannelID uint64
  PaidLiquiditySat int64
  PaidCostSat int64
  PaidRevenueSat int64
  LastRebalanceAt time.Time
  LastForwardAt time.Time
  LastUnlockAt time.Time
}

type pairStat struct {
  SourceChannelID uint64
  TargetChannelID uint64
  LastSuccessAt time.Time
  LastFailAt time.Time
  SuccessAmountSat int64
  SuccessFeePpm int64
}

type RebalanceService struct {
  db *pgxpool.Pool
  lnd *lndclient.Client
  logger *log.Logger

  mu sync.Mutex
  started bool
  stop chan struct{}
  wake chan struct{}
  subs map[chan RebalanceEvent]struct{}
  cfg RebalanceConfig
  cfgLoaded bool
  lastScan time.Time
  lastScanStatus string
  lastScanTopScoreSat int64
  lastScanProfitSkipped int
  criticalMissCount int
  sem chan struct{}
  channelLocks map[uint64]bool
  jobCancel map[int64]context.CancelFunc
}

func NewRebalanceService(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger) *RebalanceService {
  return &RebalanceService{
    db: db,
    lnd: lnd,
    logger: logger,
    subs: map[chan RebalanceEvent]struct{}{},
    channelLocks: map[uint64]bool{},
    jobCancel: map[int64]context.CancelFunc{},
  }
}

func defaultRebalanceConfig() RebalanceConfig {
  return RebalanceConfig{
    AutoEnabled: false,
    ScanIntervalSec: 600,
    DeadbandPct: 10,
    SourceMinLocalPct: 50,
    EconRatio: 0.6,
    ROIMin: 1.1,
    DailyBudgetPct: 50,
    MaxConcurrent: 2,
    MinAmountSat: 20000,
    MaxAmountSat: 0,
    FeeLadderSteps: 4,
    AmountProbeSteps: 4,
    AmountProbeAdaptive: true,
    AttemptTimeoutSec: 20,
    RebalanceTimeoutSec: 600,
    PaybackModeFlags: paybackModePayback | paybackModeTime | paybackModeCritical,
    UnlockDays: 14,
    CriticalReleasePct: 20,
    CriticalMinSources: 2,
    CriticalMinAvailableSats: 0,
    CriticalCycles: 3,
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

  s.resetSemaphore()

  go s.runAutoLoop()
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
  if err := s.upsertConfig(ctx, updated); err != nil {
    return RebalanceConfig{}, err
  }
  s.mu.Lock()
  s.cfg = updated
  s.cfgLoaded = true
  s.mu.Unlock()
  s.resetSemaphore()
  s.triggerScan()
  return updated, nil
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
    interval := time.Duration(cfg.ScanIntervalSec) * time.Second
    if interval <= 0 {
      interval = 10 * time.Minute
    }
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
  scanAt := time.Now()
  scanStatus := "scanned"
  profitSkipped := 0
  topScore := int64(0)
  defer func() {
    s.mu.Lock()
    s.lastScan = scanAt
    s.lastScanStatus = scanStatus
    s.lastScanTopScoreSat = topScore
    s.lastScanProfitSkipped = profitSkipped
    s.mu.Unlock()
  }()

  revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)

  s.mu.Lock()
  criticalActive := cfg.CriticalCycles > 0 && s.criticalMissCount >= cfg.CriticalCycles
  s.mu.Unlock()

  candidates := []rebalanceTarget{}
  eligibleSources := 0
  totalAvailable := int64(0)
  roiSkipped := 0
  topScoreSet := false
  for _, ch := range channels {
    setting := settings[ch.ChannelID]
    targetPct := setting.TargetOutboundPct
    if targetPct <= 0 {
      targetPct = rebalanceDefaultTargetOutboundPct
    }

    snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], exclusions[ch.ChannelID])
    if snapshot.EligibleAsSource {
      eligibleSources++
      totalAvailable += snapshot.MaxSourceSat
    }

    if setting.AutoEnabled && snapshot.EligibleAsTarget {
      targetAmount := snapshot.TargetAmountSat
      estimatedCost := estimateMaxCost(targetAmount, snapshot.OutgoingFeePpm, cfg.EconRatio, snapshot.PeerFeeRatePpm)
      expectedGain := estimateTargetGain(targetAmount, snapshot.Revenue7dSat, snapshot.LocalBalanceSat, snapshot.CapacitySat)
      expectedROI, roiValid := estimateTargetROI(expectedGain, estimatedCost, targetAmount, snapshot.OutgoingFeePpm, snapshot.PeerFeeRatePpm)
      if cfg.ROIMin > 0 && roiValid && expectedROI < cfg.ROIMin {
        roiSkipped++
        continue
      }
      if expectedGain > 0 && estimatedCost > 0 && expectedGain < estimatedCost {
        profitSkipped++
        continue
      }
      score := expectedGain - estimatedCost
      candidates = append(candidates, rebalanceTarget{
        Channel: snapshot,
        ExpectedGainSat: expectedGain,
        EstimatedCostSat: estimatedCost,
        ExpectedROI: expectedROI,
        ExpectedROIValid: roiValid,
        Score: score,
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
    if candidates[i].Score != candidates[j].Score {
      return candidates[i].Score > candidates[j].Score
    }
    if candidates[i].ExpectedROI != candidates[j].ExpectedROI {
      return candidates[i].ExpectedROI > candidates[j].ExpectedROI
    }
    if candidates[i].Channel.TargetAmountSat != candidates[j].Channel.TargetAmountSat {
      return candidates[i].Channel.TargetAmountSat > candidates[j].Channel.TargetAmountSat
    }
    return candidates[i].Channel.LocalPct < candidates[j].Channel.LocalPct
  })

  budget, spentAuto, _, _ := s.getDailyBudget(ctx)
  remaining := budget - spentAuto
  if remaining < 0 {
    remaining = 0
  }
  if remaining == 0 {
    scanStatus = "budget_exhausted"
    return
  }

  started := 0
  for _, target := range candidates {
    if remaining <= 0 {
      scanStatus = "budget_exhausted"
      break
    }
    maxFeePpm := calcMaxFeePpm(target.Channel.OutgoingFeePpm, target.Channel.PeerFeeRatePpm, cfg.EconRatio)
    if maxFeePpm <= 0 {
      continue
    }
    targetAmount := target.Channel.TargetAmountSat
    estimatedCost := estimateMaxCost(targetAmount, target.Channel.OutgoingFeePpm, cfg.EconRatio, target.Channel.PeerFeeRatePpm)
    amountOverride := int64(0)
    if estimatedCost > remaining {
      fitAmount := (remaining * 1_000_000) / maxFeePpm
      if fitAmount <= 0 {
        continue
      }
      if fitAmount > targetAmount {
        fitAmount = targetAmount
      }
      if cfg.MinAmountSat > 0 && fitAmount < cfg.MinAmountSat {
        continue
      }
      amountOverride = fitAmount
      estimatedCost = estimateMaxCost(fitAmount, target.Channel.OutgoingFeePpm, cfg.EconRatio, target.Channel.PeerFeeRatePpm)
      if estimatedCost > remaining {
        continue
      }
    }
    _, err := s.startJob(target.Channel.ChannelID, "auto", "", amountOverride)
    if err == nil {
      remaining -= estimatedCost
      started++
    }
  }

  if started > 0 {
    scanStatus = "queued"
  } else if remaining > 0 {
    scanStatus = "budget_insufficient"
  }

  if s.logger != nil {
    s.logger.Printf("rebalance scan: candidates=%d profit_skipped=%d roi_skipped=%d top_score=%d sats", len(candidates), profitSkipped, roiSkipped, topScore)
  }
}

type rebalanceTarget struct {
  Channel RebalanceChannel
  ExpectedGainSat int64
  EstimatedCostSat int64
  ExpectedROI float64
  ExpectedROIValid bool
  Score int64
}

func (s *RebalanceService) startJob(targetChannelID uint64, source string, reason string, amountOverride int64) (int64, error) {
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()

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
  setting := settings[targetChannelID]
  targetPct := setting.TargetOutboundPct
  if targetPct <= 0 {
    targetPct = rebalanceDefaultTargetOutboundPct
  }
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

  jobID, err := s.insertJob(ctx, &target, source, reason, targetPct, amount)
  if err != nil {
    return 0, err
  }

  go s.runJob(jobID, targetChannelID, amount, targetPct, source)
  return jobID, nil
}

func (s *RebalanceService) runJob(jobID int64, targetChannelID uint64, amount int64, targetPct float64, jobSource string) {
  cfg, _ := s.loadConfig(context.Background())
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

  if cfg.MinAmountSat > 0 && amount < cfg.MinAmountSat {
    s.finishJob(jobID, "failed", "amount below minimum")
    return
  }
  if cfg.MaxAmountSat > 0 && amount > cfg.MaxAmountSat {
    amount = cfg.MaxAmountSat
  }

  settings, _ := s.loadChannelSettings(ctx)
  exclusions, _ := s.loadExclusions(ctx)
  ledger, _ := s.loadLedger(ctx)
  _ = s.applyForwardDeltas(ctx, ledger)

  channels, err := s.lnd.ListChannels(ctx)
  if err != nil {
    s.finishJob(jobID, "failed", "lnd unavailable")
    return
  }

  revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)

  targetFound := false
  channelSnapshots := []RebalanceChannel{}
  for _, ch := range channels {
    setting := settings[ch.ChannelID]
    snapshot := s.buildChannelSnapshot(ctx, cfg, false, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], exclusions[ch.ChannelID])
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
  if cfg.MinAmountSat > 0 && amount < cfg.MinAmountSat {
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
  if len(sources) == 0 {
    s.finishJob(jobID, "failed", "no eligible sources")
    return
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

  maxFeePpm := calcMaxFeePpm(targetSnapshot.OutgoingFeePpm, targetSnapshot.PeerFeeRatePpm, cfg.EconRatio)
  if maxFeePpm <= 0 {
    s.finishJob(jobID, "failed", "fee spread not positive")
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
    if stat.SuccessFeePpm > maxFeePpm {
      continue
    }
    if cfg.MinAmountSat > 0 && stat.SuccessAmountSat < cfg.MinAmountSat {
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
  attemptIndex := 0
  adaptiveMaxAmount := int64(0)
  attemptTimeoutSec := cfg.AttemptTimeoutSec
  if attemptTimeoutSec <= 0 {
    attemptTimeoutSec = 60
  }

  attemptPayment := func(sourceID uint64, amountTry int64, feePpm int64, logRouteFailure bool) (bool, bool, int64) {
    if ctx.Err() != nil {
      if errors.Is(ctx.Err(), context.DeadlineExceeded) {
        s.finishJob(jobID, "failed", "timeout")
      } else {
        s.finishJob(jobID, "cancelled", "cancelled")
      }
      return false, true, 0
    }
    if amountTry <= 0 {
      return false, false, 0
    }
    if cfg.MinAmountSat > 0 && amountTry < cfg.MinAmountSat {
      return false, false, 0
    }

    feeLimitMsat := ppmToFeeLimitMsat(amountTry, feePpm)
    var probeFeeMsat int64
    attemptCtx := ctx
    cancelAttempt := func() {}
    if attemptTimeoutSec > 0 {
      attemptCtx, cancelAttempt = context.WithTimeout(ctx, time.Duration(attemptTimeoutSec)*time.Second)
    }

    route, err := s.lnd.QueryRoute(attemptCtx, selfPubkey, amountTry, sourceID, targetSnapshot.RemotePubkey, feeLimitMsat)
    routeMaxSat := int64(0)
    if err != nil {
      cancelAttempt()
      if ctx.Err() != nil {
        if errors.Is(ctx.Err(), context.DeadlineExceeded) {
          s.finishJob(jobID, "failed", "timeout")
        } else {
          s.finishJob(jobID, "cancelled", "cancelled")
        }
        return false, true, 0
      }
      if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
        attemptIndex++
        _ = s.insertAttempt(ctx, jobID, attemptIndex, sourceID, amountTry, feePpm, 0, "failed", "", "attempt timeout")
        s.recordPairFailure(ctx, sourceID, targetChannelID, "attempt timeout")
        return false, false, 0
      }
      if logRouteFailure {
        attemptIndex++
        _ = s.insertAttempt(ctx, jobID, attemptIndex, sourceID, amountTry, feePpm, 0, "failed", "", err.Error())
        s.recordPairFailure(ctx, sourceID, targetChannelID, err.Error())
      }
      return false, false, 0
    }
    routeMaxSat = s.maxAmountOnRouteSat(attemptCtx, route, selfPubkey)
    if route != nil {
      if route.TotalFeesMsat > 0 {
        probeFeeMsat = route.TotalFeesMsat
      } else if route.TotalFees > 0 {
        probeFeeMsat = route.TotalFees * 1000
      }
    }

    paymentReq, paymentHash, err := s.createRebalanceInvoice(attemptCtx, amountTry, jobID, sourceID, targetChannelID)
    if err != nil {
      cancelAttempt()
      if ctx.Err() != nil {
        if errors.Is(ctx.Err(), context.DeadlineExceeded) {
          s.finishJob(jobID, "failed", "timeout")
        } else {
          s.finishJob(jobID, "cancelled", "cancelled")
        }
        return false, true, 0
      }
      if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
        attemptIndex++
        _ = s.insertAttempt(ctx, jobID, attemptIndex, sourceID, amountTry, feePpm, 0, "failed", "", "attempt timeout")
        s.recordPairFailure(ctx, sourceID, targetChannelID, "attempt timeout")
        return false, false, 0
      }
      attemptIndex++
      _ = s.insertAttempt(ctx, jobID, attemptIndex, sourceID, amountTry, feePpm, 0, "failed", "", err.Error())
      s.recordPairFailure(ctx, sourceID, targetChannelID, err.Error())
      return false, false, 0
    }

    payment, err := s.lnd.SendPaymentWithConstraints(attemptCtx, paymentReq, sourceID, targetSnapshot.RemotePubkey, feeLimitMsat, int32(attemptTimeoutSec), 3)
    if err != nil {
      cancelAttempt()
      if ctx.Err() != nil {
        if errors.Is(ctx.Err(), context.DeadlineExceeded) {
          s.finishJob(jobID, "failed", "timeout")
        } else {
          s.finishJob(jobID, "cancelled", "cancelled")
        }
        return false, true, 0
      }
      if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
        attemptIndex++
        _ = s.insertAttempt(ctx, jobID, attemptIndex, sourceID, amountTry, feePpm, 0, "failed", paymentHash, "attempt timeout")
        s.recordPairFailure(ctx, sourceID, targetChannelID, "attempt timeout")
        return false, false, 0
      }
      attemptIndex++
      _ = s.insertAttempt(ctx, jobID, attemptIndex, sourceID, amountTry, feePpm, 0, "failed", paymentHash, err.Error())
      s.recordPairFailure(ctx, sourceID, targetChannelID, err.Error())
      return false, false, 0
    }
    cancelAttempt()

    feePaidSat := payment.FeeSat
    if feePaidSat == 0 && payment.FeeMsat != 0 {
      feePaidSat = int64(math.Ceil(float64(payment.FeeMsat) / 1000.0))
    }
    if feePaidSat == 0 && len(payment.Htlcs) > 0 {
      var feeMsatSum int64
      var feeSatSum int64
      for _, htlc := range payment.Htlcs {
        if htlc == nil || htlc.Route == nil {
          continue
        }
        if htlc.Route.TotalFeesMsat > 0 {
          feeMsatSum += htlc.Route.TotalFeesMsat
        } else if htlc.Route.TotalFees > 0 {
          feeSatSum += htlc.Route.TotalFees
        }
      }
      if feeMsatSum > 0 {
        feePaidSat = int64(math.Ceil(float64(feeMsatSum) / 1000.0))
      } else if feeSatSum > 0 {
        feePaidSat = feeSatSum
      }
    }
    if feePaidSat == 0 && probeFeeMsat > 0 {
      feePaidSat = int64(math.Ceil(float64(probeFeeMsat) / 1000.0))
    }
    attemptIndex++
    _ = s.insertAttempt(ctx, jobID, attemptIndex, sourceID, amountTry, feePpm, feePaidSat, "succeeded", paymentHash, "")
    s.recordPairSuccess(ctx, sourceID, targetChannelID, amountTry, feePpm, feePaidSat)
    _ = s.applyRebalanceLedger(ctx, targetChannelID, amountTry, feePaidSat)
    _ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
    return true, false, routeMaxSat
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

  rapidRebalance := func(sourceID uint64, feePpm int64, startAmount int64, startRouteCap int64, sourceRemaining *int64) (bool, bool) {
    current := startAmount
    if current <= 0 {
      return false, false
    }
    if cfg.MinAmountSat > 0 && current < cfg.MinAmountSat {
      return false, false
    }
    routeCap := startRouteCap
    phase := "increase"

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
      if cfg.MinAmountSat > 0 && maxCap < cfg.MinAmountSat {
        break
      }
      if current > maxCap {
        current = maxCap
      }
      if cfg.MinAmountSat > 0 && current < cfg.MinAmountSat {
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
        success, fatal, routeMax := attemptPayment(sourceID, next, feePpm, true)
        if fatal {
          return false, true
        }
        if success {
          if applySuccess(next, routeMax, &routeCap, sourceRemaining) {
            return true, false
          }
          current = next
          continue
        }
        phase = "steady"

      case "steady":
        success, fatal, routeMax := attemptPayment(sourceID, current, feePpm, true)
        if fatal {
          return false, true
        }
        if success {
          if applySuccess(current, routeMax, &routeCap, sourceRemaining) {
            return true, false
          }
          continue
        }
        phase = "decrease"

      case "decrease":
        next := current / 2
        if cfg.MinAmountSat > 0 && next < cfg.MinAmountSat {
          next = cfg.MinAmountSat
        }
        if next <= 0 || next >= current {
          return false, false
        }
        current = next
        if current > maxCap {
          current = maxCap
        }
        if cfg.MinAmountSat > 0 && current < cfg.MinAmountSat {
          return false, false
        }
        success, fatal, routeMax := attemptPayment(sourceID, current, feePpm, true)
        if fatal {
          return false, true
        }
        if success {
          if applySuccess(current, routeMax, &routeCap, sourceRemaining) {
            return true, false
          }
          phase = "increase"
          continue
        }
      }
    }

    return false, false
  }

  for _, source := range sources {
    if ctx.Err() != nil {
      if errors.Is(ctx.Err(), context.DeadlineExceeded) {
        s.finishJob(jobID, "failed", "timeout")
      } else {
        s.finishJob(jobID, "cancelled", "cancelled")
      }
      return
    }

    if jobSource == "auto" {
      if stat, ok := pairStats[source.ChannelID]; ok {
        if !stat.LastFailAt.IsZero() && time.Since(stat.LastFailAt) <= pairFailTTL && (stat.LastSuccessAt.IsZero() || stat.LastSuccessAt.Before(stat.LastFailAt)) {
          continue
        }
      }
    }

    maxFromSource := source.MaxSourceSat
    if maxFromSource <= 0 {
      continue
    }
    sourceRemaining := maxFromSource

    sendAmount := remaining
    if sendAmount > sourceRemaining {
      sendAmount = sourceRemaining
    }
    if cfg.AmountProbeAdaptive && adaptiveMaxAmount > 0 {
      capAmount := adaptiveMaxAmount * 2
      if capAmount > 0 && capAmount < sendAmount {
        sendAmount = capAmount
      }
    }
    if cfg.MinAmountSat > 0 && sendAmount < cfg.MinAmountSat {
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
      if cfg.MinAmountSat > 0 && warmTry < cfg.MinAmountSat {
        warmTry = 0
      }
      if warmTry > 0 {
        success, fatal, routeMax := attemptPayment(source.ChannelID, warmTry, warmFeePpm, true)
        if fatal {
          return
        }
        if success {
          routeCap := int64(0)
          if applySuccess(warmTry, routeMax, &routeCap, &sourceRemaining) {
            return
          }
          finished, fatal := rapidRebalance(source.ChannelID, warmFeePpm, warmTry, routeCap, &sourceRemaining)
          if fatal {
            return
          }
          if finished {
            return
          }
          continue
        }
      }
    }

    feeSteps := cfg.FeeLadderSteps
    if feeSteps <= 0 {
      feeSteps = 1
    }
    amountSteps := cfg.AmountProbeSteps
    if amountSteps <= 0 {
      amountSteps = feeSteps
    }
    amountCandidates := buildAmountProbe(sendAmount, cfg.MinAmountSat, amountSteps)
    if len(amountCandidates) == 0 {
      continue
    }

    sourceSucceeded := false
    for step := 1; step <= feeSteps; step++ {
      feePpm := calcFeeStepPpm(maxFeePpm, feeSteps, step)
      if feePpm <= 0 {
        feePpm = 1
      }

      for idx, amountTry := range amountCandidates {
        logRouteFailure := step == feeSteps && idx == len(amountCandidates)-1
        success, fatal, routeMax := attemptPayment(source.ChannelID, amountTry, feePpm, logRouteFailure)
        if fatal {
          return
        }
        if !success {
          continue
        }
        sourceSucceeded = true
        routeCap := int64(0)
        if applySuccess(amountTry, routeMax, &routeCap, &sourceRemaining) {
          return
        }
        finished, fatal := rapidRebalance(source.ChannelID, feePpm, amountTry, routeCap, &sourceRemaining)
        if fatal {
          return
        }
        if finished {
          return
        }
        break
      }
      if sourceSucceeded {
        break
      }
    }
  }

  if anySuccess {
    s.finishJob(jobID, "partial", fmt.Sprintf("remaining %d sats", remaining))
    s.broadcast(RebalanceEvent{Type: "job", JobID: jobID, Status: "partial"})
    return
  }

  s.finishJob(jobID, "failed", "all sources failed")
}

func (s *RebalanceService) createRebalanceInvoice(ctx context.Context, amount int64, jobID int64, sourceID uint64, targetID uint64) (string, string, error) {
  if s.lnd == nil {
    return "", "", errors.New("lnd unavailable")
  }
  memo := fmt.Sprintf("rebalance:%d:%d:%d", jobID, sourceID, targetID)
  inv, err := s.lnd.CreateInvoice(ctx, amount, memo, 3600)
  if err != nil {
    return "", "", err
  }
  return inv.PaymentRequest, inv.PaymentHash, nil
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
      SourceChannelID: uint64(sourceID),
      TargetChannelID: uint64(targetIDRow),
      SuccessAmountSat: successAmount,
      SuccessFeePpm: successFee,
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

func (s *RebalanceService) finishJob(jobID int64, status string, reason string) {
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()
  completedAt := time.Now().UTC()
  _, _ = s.db.Exec(ctx, `
update rebalance_jobs
set status=$2, reason=$3, completed_at=$4
where id=$1`, jobID, status, nullableString(reason), completedAt)
  s.broadcast(RebalanceEvent{Type: "job", JobID: jobID, Status: status, Message: reason})
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

func (s *RebalanceService) buildChannelSnapshot(ctx context.Context, cfg RebalanceConfig, criticalActive bool, ch lndclient.ChannelInfo, setting channelSetting, ledger *channelLedger, revenue7dSat int64, excluded bool) RebalanceChannel {
  capacity := float64(ch.CapacitySat)
  localPct := 0.0
  remotePct := 0.0
  if capacity > 0 {
    localPct = float64(ch.LocalBalanceSat) / capacity * 100
    remotePct = float64(ch.RemoteBalanceSat) / capacity * 100
  }

  outgoingFee := int64(0)
  peerFeeRate := int64(0)
  if ch.FeeRatePpm != nil {
    outgoingFee = *ch.FeeRatePpm
  }
  policies, err := s.lnd.GetChannelPolicies(ctx, ch.ChannelID)
  if err == nil {
    outgoingFee = policies.Local.FeeRatePpm
    peerFeeRate = policies.Remote.FeeRatePpm
  }

  spread := outgoingFee - peerFeeRate
  target := setting.TargetOutboundPct
  if target <= 0 {
    target = rebalanceDefaultTargetOutboundPct
  }

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
  estCost := estimateMaxCost(targetAmount, outgoingFee, cfg.EconRatio, peerFeeRate)
  if estCost > 0 && revenue7dSat > 0 {
    roiEstimate = float64(revenue7dSat) / float64(estCost)
    roiEstimateValid = true
  } else if estCost == 0 && targetAmount > 0 && outgoingFee > peerFeeRate {
    // Cost is zero -> ROI is indeterminate; allow auto rebal by skipping ROI filter.
    roiEstimateValid = false
  }

  return RebalanceChannel{
    ChannelID: ch.ChannelID,
    ChannelPoint: ch.ChannelPoint,
    PeerAlias: ch.PeerAlias,
    RemotePubkey: ch.RemotePubkey,
    Active: ch.Active,
    Private: ch.Private,
    CapacitySat: ch.CapacitySat,
    LocalBalanceSat: ch.LocalBalanceSat,
    RemoteBalanceSat: ch.RemoteBalanceSat,
    LocalPct: localPct,
    RemotePct: remotePct,
    OutgoingFeePpm: outgoingFee,
    PeerFeeRatePpm: peerFeeRate,
    SpreadPpm: spread,
    TargetOutboundPct: target,
    TargetAmountSat: targetAmount,
    AutoEnabled: setting.AutoEnabled,
    EligibleAsTarget: eligibleTarget,
    EligibleAsSource: eligibleSource && !excluded,
    ProtectedLiquiditySat: protected,
    PaybackProgress: paybackProgress,
    MaxSourceSat: maxSource,
    Revenue7dSat: revenue7dSat,
    ROIEstimate: roiEstimate,
    ROIEstimateValid: roiEstimateValid,
    ExcludedAsSource: excluded,
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

func calcMaxFeePpm(outgoingFeePpm int64, peerFeeRatePpm int64, econRatio float64) int64 {
  spread := outgoingFeePpm - peerFeeRatePpm
  if spread <= 0 {
    return 0
  }
  scaled := int64(math.Round(float64(outgoingFeePpm) * econRatio))
  if scaled <= 0 {
    return 0
  }
  if scaled > spread {
    return spread
  }
  return scaled
}

func calcFeeStepPpm(maxFeePpm int64, steps int, step int) int64 {
  if maxFeePpm <= 0 {
    return 0
  }
  if steps <= 1 {
    return maxFeePpm
  }
  minFee := int64(math.Round(float64(maxFeePpm) * 0.7))
  if minFee < 1 {
    minFee = 1
  }
  if minFee > maxFeePpm {
    minFee = maxFeePpm
  }
  if step <= 1 {
    return minFee
  }
  if step >= steps {
    return maxFeePpm
  }
  span := maxFeePpm - minFee
  if span <= 0 {
    return maxFeePpm
  }
  frac := float64(step-1) / float64(steps-1)
  fee := float64(minFee) + float64(span)*frac
  if fee < 1 {
    fee = 1
  }
  return int64(math.Ceil(fee))
}

func estimateMaxCost(amountSat int64, outgoingFeePpm int64, econRatio float64, peerFeeRatePpm int64) int64 {
  maxFeePpm := calcMaxFeePpm(outgoingFeePpm, peerFeeRatePpm, econRatio)
  if maxFeePpm <= 0 || amountSat <= 0 {
    return 0
  }
  return (amountSat * maxFeePpm) / 1_000_000
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

func estimateTargetROI(expectedGainSat int64, estimatedCostSat int64, amountSat int64, outgoingFeePpm int64, peerFeeRatePpm int64) (float64, bool) {
  if estimatedCostSat > 0 {
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
    roi_min double precision not null default 1.1,
    daily_budget_pct double precision not null default 50,
    max_concurrent integer not null default 2,
    min_amount_sat bigint not null default 20000,
    max_amount_sat bigint not null default 0,
    fee_ladder_steps integer not null default 4,
    amount_probe_steps integer not null default 4,
    amount_probe_adaptive boolean not null default true,
    attempt_timeout_sec integer not null default 20,
    rebalance_timeout_sec integer not null default 600,
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
    add column if not exists amount_probe_steps integer not null default 4;
  alter table rebalance_config
    add column if not exists amount_probe_adaptive boolean not null default true;
  alter table rebalance_config
    add column if not exists attempt_timeout_sec integer not null default 20;
  alter table rebalance_config
    add column if not exists rebalance_timeout_sec integer not null default 600;

create table if not exists rebalance_channel_settings (
  channel_id bigint primary key,
  channel_point text not null,
  target_outbound_pct double precision not null default 50,
  auto_enabled boolean not null default false,
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
create index if not exists rebalance_attempts_job_idx on rebalance_attempts (job_id);
create index if not exists rebalance_attempts_status_idx on rebalance_attempts (status);
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
  select auto_enabled, scan_interval_sec, deadband_pct, source_min_local_pct, econ_ratio, roi_min, daily_budget_pct,
    max_concurrent, min_amount_sat, max_amount_sat, fee_ladder_steps, amount_probe_steps, amount_probe_adaptive, attempt_timeout_sec, rebalance_timeout_sec, payback_mode_flags,
    unlock_days, critical_release_pct, critical_min_sources, critical_min_available_sats, critical_cycles
  from rebalance_config where id=$1`, rebalanceConfigID)

  cfg := defaultRebalanceConfig()
  err := row.Scan(
    &cfg.AutoEnabled,
    &cfg.ScanIntervalSec,
    &cfg.DeadbandPct,
    &cfg.SourceMinLocalPct,
    &cfg.EconRatio,
    &cfg.ROIMin,
    &cfg.DailyBudgetPct,
    &cfg.MaxConcurrent,
    &cfg.MinAmountSat,
    &cfg.MaxAmountSat,
    &cfg.FeeLadderSteps,
    &cfg.AmountProbeSteps,
    &cfg.AmountProbeAdaptive,
    &cfg.AttemptTimeoutSec,
    &cfg.RebalanceTimeoutSec,
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
    id, auto_enabled, scan_interval_sec, deadband_pct, source_min_local_pct, econ_ratio, roi_min, daily_budget_pct,
    max_concurrent, min_amount_sat, max_amount_sat, fee_ladder_steps, amount_probe_steps, amount_probe_adaptive, attempt_timeout_sec, rebalance_timeout_sec, payback_mode_flags,
    unlock_days, critical_release_pct, critical_min_sources, critical_min_available_sats, critical_cycles, updated_at
  ) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,now())
   on conflict (id) do update set
    auto_enabled = excluded.auto_enabled,
    scan_interval_sec = excluded.scan_interval_sec,
    deadband_pct = excluded.deadband_pct,
    source_min_local_pct = excluded.source_min_local_pct,
    econ_ratio = excluded.econ_ratio,
    roi_min = excluded.roi_min,
    daily_budget_pct = excluded.daily_budget_pct,
    max_concurrent = excluded.max_concurrent,
    min_amount_sat = excluded.min_amount_sat,
    max_amount_sat = excluded.max_amount_sat,
    fee_ladder_steps = excluded.fee_ladder_steps,
    amount_probe_steps = excluded.amount_probe_steps,
    amount_probe_adaptive = excluded.amount_probe_adaptive,
    attempt_timeout_sec = excluded.attempt_timeout_sec,
    rebalance_timeout_sec = excluded.rebalance_timeout_sec,
    payback_mode_flags = excluded.payback_mode_flags,
    unlock_days = excluded.unlock_days,
    critical_release_pct = excluded.critical_release_pct,
    critical_min_sources = excluded.critical_min_sources,
    critical_min_available_sats = excluded.critical_min_available_sats,
  critical_cycles = excluded.critical_cycles,
  updated_at = now()
  `, rebalanceConfigID, cfg.AutoEnabled, cfg.ScanIntervalSec, cfg.DeadbandPct, cfg.SourceMinLocalPct, cfg.EconRatio, cfg.ROIMin, cfg.DailyBudgetPct, cfg.MaxConcurrent,
    cfg.MinAmountSat, cfg.MaxAmountSat, cfg.FeeLadderSteps, cfg.AmountProbeSteps, cfg.AmountProbeAdaptive, cfg.AttemptTimeoutSec, cfg.RebalanceTimeoutSec, cfg.PaybackModeFlags, cfg.UnlockDays, cfg.CriticalReleasePct, cfg.CriticalMinSources, cfg.CriticalMinAvailableSats, cfg.CriticalCycles,
  )
  return err
}

func (s *RebalanceService) loadChannelSettings(ctx context.Context) (map[uint64]channelSetting, error) {
  settings := map[uint64]channelSetting{}
  if s.db == nil {
    return settings, nil
  }
  rows, err := s.db.Query(ctx, `
select channel_id, channel_point, target_outbound_pct, auto_enabled from rebalance_channel_settings
`)
  if err != nil {
    return settings, err
  }
  defer rows.Close()
  for rows.Next() {
    var channelID int64
    var setting channelSetting
    if err := rows.Scan(&channelID, &setting.ChannelPoint, &setting.TargetOutboundPct, &setting.AutoEnabled); err != nil {
      return settings, err
    }
    setting.ChannelID = uint64(channelID)
    settings[setting.ChannelID] = setting
  }
  return settings, rows.Err()
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
select channel_id, paid_liquidity_sats, paid_cost_sats, paid_revenue_sats,
  last_rebalance_at, last_forward_at, last_unlock_at
from rebalance_channel_ledger
`)
  if err != nil {
    return ledger, err
  }
  defer rows.Close()
  for rows.Next() {
    var channelID int64
    var entry channelLedger
    var lastRebalance pgtype.Timestamptz
    var lastForward pgtype.Timestamptz
    var lastUnlock pgtype.Timestamptz
    if err := rows.Scan(&channelID, &entry.PaidLiquiditySat, &entry.PaidCostSat, &entry.PaidRevenueSat, &lastRebalance, &lastForward, &lastUnlock); err != nil {
      return ledger, err
    }
    entry.ChannelID = uint64(channelID)
    if lastRebalance.Valid {
      entry.LastRebalanceAt = lastRebalance.Time
    }
    if lastForward.Valid {
      entry.LastForwardAt = lastForward.Time
    }
    if lastUnlock.Valid {
      entry.LastUnlockAt = lastUnlock.Time
    }
    ledger[entry.ChannelID] = &entry
  }
  return ledger, rows.Err()
}

func (s *RebalanceService) applyForwardDeltas(ctx context.Context, ledger map[uint64]*channelLedger) error {
  if s.db == nil {
    return nil
  }
  for _, entry := range ledger {
    since := entry.LastForwardAt
    if since.IsZero() {
      since = entry.LastRebalanceAt
    }
    if since.IsZero() {
      continue
    }
    rows, err := s.db.Query(ctx, `
select occurred_at, amount_sat, fee_sat, fee_msat
from notifications
where type='forward' and channel_id=$1 and occurred_at > $2
order by occurred_at asc`, int64(entry.ChannelID), since)
    if err != nil {
      return err
    }
    var updated bool
    for rows.Next() {
      var occurredAt time.Time
      var amountSat int64
      var feeSat int64
      var feeMsat int64
      if err := rows.Scan(&occurredAt, &amountSat, &feeSat, &feeMsat); err != nil {
        rows.Close()
        return err
      }
      if amountSat > 0 {
        entry.PaidLiquiditySat -= amountSat
        if entry.PaidLiquiditySat < 0 {
          entry.PaidLiquiditySat = 0
        }
      }
      if feeMsat == 0 && feeSat != 0 {
        feeMsat = feeSat * 1000
      }
      if feeMsat > 0 {
        entry.PaidRevenueSat += feeMsat / 1000
      }
      entry.LastForwardAt = occurredAt
      updated = true
    }
    rows.Close()
    if updated {
      _, err := s.db.Exec(ctx, `
update rebalance_channel_ledger
set paid_liquidity_sats=$2,
  paid_revenue_sats=$3,
  last_forward_at=$4
where channel_id=$1
`, int64(entry.ChannelID), entry.PaidLiquiditySat, entry.PaidRevenueSat, entry.LastForwardAt)
      if err != nil {
        return err
      }
    }
  }
  return nil
}

func (s *RebalanceService) fetchChannelRevenue7d(ctx context.Context) (map[uint64]int64, error) {
  revenue := map[uint64]int64{}
  if s.db == nil {
    return revenue, nil
  }
  rows, err := s.db.Query(ctx, `
select channel_id, coalesce(sum(fee_msat), 0)
from notifications
where type='forward' and occurred_at >= now() - interval '7 days'
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
  revenue, err := s.fetchForwardRevenue24h(ctx, time.Now().In(time.Local))
  if err != nil {
    return err
  }
  budget := int64(math.Round(float64(revenue) * (cfg.DailyBudgetPct / 100)))
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
      StartTime: uint64(start.Unix()),
      EndTime: uint64(end.Unix()),
      IndexOffset: offset,
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

func (s *RebalanceService) insertJob(ctx context.Context, target *lndclient.ChannelInfo, source string, reason string, targetPct float64, amount int64) (int64, error) {
  if s.db == nil {
    return 0, errors.New("db unavailable")
  }
  var jobID int64
  err := s.db.QueryRow(ctx, `
insert into rebalance_jobs (
  source, status, reason, target_channel_id, target_channel_point, target_outbound_pct, target_amount_sat, config_snapshot
) values ($1,'running',$2,$3,$4,$5,$6,$7)
 returning id
`, source, nullableString(reason), int64(target.ChannelID), target.ChannelPoint, targetPct, amount, nil).Scan(&jobID)
  return jobID, err
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
  liveCost := int64(0)
  if s.db != nil {
    var liveMsat int64
    _ = s.db.QueryRow(ctx, `
select coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0)
from notifications
where type='rebalance' and occurred_at >= now() - interval '1 day'
`).Scan(&liveMsat)
    liveCost = msatToSatCeil(liveMsat)
  }

  effectiveness := 0.0
  roi := 0.0
  var successCount int64
  var totalCount int64
  _ = s.db.QueryRow(ctx, `
select
  coalesce(sum(case when status in ('succeeded','partial') then 1 else 0 end), 0),
  count(*)
from rebalance_jobs
where completed_at >= now() - interval '7 days'
`).Scan(&successCount, &totalCount)
  if totalCount > 0 {
    effectiveness = float64(successCount) / float64(totalCount)
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

  eligibleSources := 0
  targetsNeeding := 0
  if s.lnd != nil {
    eligibleSources, targetsNeeding = s.computeEligibilityCounts(ctx, cfg)
  }

  s.mu.Lock()
  lastScan := s.lastScan
  lastScanStatus := s.lastScanStatus
  lastScanTopScore := s.lastScanTopScoreSat
  lastScanProfitSkipped := s.lastScanProfitSkipped
  s.mu.Unlock()

  overview := RebalanceOverview{
    AutoEnabled: cfg.AutoEnabled,
    DailyBudgetSat: budget,
    DailySpentSat: spent,
    DailySpentAutoSat: spentAuto,
    DailySpentManualSat: spentManual,
    LiveCostSat: liveCost,
    Effectiveness7d: effectiveness,
    ROI7d: roi,
    LastScanStatus: lastScanStatus,
    LastScanTopScoreSat: lastScanTopScore,
    LastScanProfitSkipped: lastScanProfitSkipped,
    EligibleSources: eligibleSources,
    TargetsNeeding: targetsNeeding,
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
  channels, err := s.lnd.ListChannels(ctx)
  if err != nil {
    return nil, err
  }
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
    snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], exclusions[ch.ChannelID])
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
  revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)

  s.mu.Lock()
  criticalActive := cfg.CriticalCycles > 0 && s.criticalMissCount >= cfg.CriticalCycles
  s.mu.Unlock()

  eligibleSources := 0
  targetsNeeding := 0
  for _, ch := range channels {
    setting := settings[ch.ChannelID]
    snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], exclusions[ch.ChannelID])
    if snapshot.EligibleAsSource {
      eligibleSources++
    }
    if setting.AutoEnabled && snapshot.EligibleAsTarget && (cfg.ROIMin <= 0 || !snapshot.ROIEstimateValid || snapshot.ROIEstimate >= cfg.ROIMin) {
      targetsNeeding++
    }
  }
  return eligibleSources, targetsNeeding
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
order by created_at desc
`)
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
where job_id in (select id from rebalance_jobs where status in ('running','queued'))
order by started_at desc
`)
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
set status='failed', reason='timeout', completed_at=now()
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
  if s.db == nil {
    return errors.New("db unavailable")
  }
  if targetPct <= 0 || targetPct > 100 {
    return errors.New("target outbound must be between 1 and 100")
  }
  _, err := s.db.Exec(ctx, `
insert into rebalance_channel_settings (channel_id, channel_point, target_outbound_pct, auto_enabled, updated_at)
values ($1,$2,$3,false,now())
 on conflict (channel_id) do update set target_outbound_pct=excluded.target_outbound_pct, channel_point=excluded.channel_point, updated_at=now()
`, int64(channelID), channelPoint, targetPct)
  return err
}

func (s *RebalanceService) SetChannelAuto(ctx context.Context, channelID uint64, channelPoint string, autoEnabled bool) error {
  if s.db == nil {
    return errors.New("db unavailable")
  }
  _, err := s.db.Exec(ctx, `
  insert into rebalance_channel_settings (channel_id, channel_point, target_outbound_pct, auto_enabled, updated_at)
  values ($1,$2, $3, $4, now())
   on conflict (channel_id) do update set channel_point=excluded.channel_point, auto_enabled=excluded.auto_enabled, updated_at=now()
  `, int64(channelID), channelPoint, rebalanceDefaultTargetOutboundPct, autoEnabled)
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
