package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type rebalanceConfigPayload struct {
	AutoEnabled                            *bool    `json:"auto_enabled,omitempty"`
	SchedulerMode                          *string  `json:"scheduler_mode,omitempty"`
	SovereignCandidateScope                *string  `json:"sovereign_candidate_scope,omitempty"`
	SovereignMaxJobsPerCycle               *int     `json:"sovereign_max_jobs_per_cycle,omitempty"`
	SovereignMinExpectedProfitSat          *int64   `json:"sovereign_min_expected_profit_sat,omitempty"`
	SovereignLowSuccessMinRate             *float64 `json:"sovereign_low_success_min_rate,omitempty"`
	SovereignLowSuccessMinProfitCostRatio  *float64 `json:"sovereign_low_success_min_profit_cost_ratio,omitempty"`
	SovereignBudgetEfficiencyMinRatio      *float64 `json:"sovereign_budget_efficiency_min_ratio,omitempty"`
	SovereignRouteDeadSourceShare          *float64 `json:"sovereign_route_dead_source_share,omitempty"`
	SovereignRiskScoreFloor                *float64 `json:"sovereign_risk_score_floor,omitempty"`
	SovereignGainV3ColdStartPct            *float64 `json:"sovereign_gain_v3_cold_start_pct,omitempty"`
	FastPathMaxTimeoutSec                  *int     `json:"fast_path_max_timeout_sec,omitempty"`
	SovereignTopBucketPct                  *int     `json:"sovereign_top_bucket_pct,omitempty"`
	SovereignAttributionWindowHours        *int     `json:"sovereign_attribution_window_hours,omitempty"`
	SovereignSlowSellerWindowHours         *int     `json:"sovereign_slow_seller_window_hours,omitempty"`
	SovereignTargetSourceQuarantineHours   *int     `json:"sovereign_target_source_quarantine_hours,omitempty"`
	SovereignStructuralCooldownRepeatHours *int     `json:"sovereign_structural_cooldown_repeat_hours,omitempty"`
	SovereignExplorationSlotPct            *int     `json:"sovereign_exploration_slot_pct,omitempty"`
	SovereignSourceOpportunityCostEnabled  *bool    `json:"sovereign_source_opportunity_cost_enabled,omitempty"`
	SovereignSlowSellerEnabled             *bool    `json:"sovereign_slow_seller_enabled,omitempty"`
	ScanIntervalSec                        *int     `json:"scan_interval_sec,omitempty"`
	DeadbandPct                            *float64 `json:"deadband_pct,omitempty"`
	SourceMinLocalPct                      *float64 `json:"source_min_local_pct,omitempty"`
	EconRatio                              *float64 `json:"econ_ratio,omitempty"`
	EconRatioMaxPpm                        *int64   `json:"econ_ratio_max_ppm,omitempty"`
	FeeLimitPpm                            *int64   `json:"fee_limit_ppm,omitempty"`
	LostProfit                             *bool    `json:"lost_profit,omitempty"`
	FailTolerancePpm                       *int64   `json:"fail_tolerance_ppm,omitempty"`
	ROIMin                                 *float64 `json:"roi_min,omitempty"`
	DailyBudgetPct                         *float64 `json:"daily_budget_pct,omitempty"`
	BudgetMode                             *string  `json:"budget_mode,omitempty"`
	BudgetUnlimited                        *bool    `json:"budget_unlimited,omitempty"`
	BudgetAutoOnly                         *bool    `json:"budget_auto_only,omitempty"`
	ManualReserveEnabled                   *bool    `json:"manual_reserve_enabled,omitempty"`
	ManualReserveMode                      *string  `json:"manual_reserve_mode,omitempty"`
	ManualReserveValue                     *float64 `json:"manual_reserve_value,omitempty"`
	MaxConcurrent                          *int     `json:"max_concurrent,omitempty"`
	MinAmountSat                           *int64   `json:"min_amount_sat,omitempty"`
	MaxAmountSat                           *int64   `json:"max_amount_sat,omitempty"`
	MinSplitEnabled                        *bool    `json:"min_split_enabled,omitempty"`
	MinProbeSat                            *int64   `json:"min_probe_sat,omitempty"`
	MinExecuteSat                          *int64   `json:"min_execute_sat,omitempty"`
	MppEnabled                             *bool    `json:"mpp_enabled,omitempty"`
	MppMaxShards                           *int     `json:"mpp_max_shards,omitempty"`
	MppParallelism                         *int     `json:"mpp_parallelism,omitempty"`
	MppMinShardSat                         *int64   `json:"mpp_min_shard_sat,omitempty"`
	MppRoundTimeoutSec                     *int     `json:"mpp_round_timeout_sec,omitempty"`
	MppAutoOnly                            *bool    `json:"mpp_auto_only,omitempty"`
	FeeLadderSteps                         *int     `json:"fee_ladder_steps,omitempty"`
	AmountProbeSteps                       *int     `json:"amount_probe_steps,omitempty"`
	AmountProbeAdaptive                    *bool    `json:"amount_probe_adaptive,omitempty"`
	AttemptTimeoutSec                      *int     `json:"attempt_timeout_sec,omitempty"`
	RebalanceTimeoutSec                    *int     `json:"rebalance_timeout_sec,omitempty"`
	ManualRestartWatch                     *bool    `json:"manual_restart_watch,omitempty"`
	ManualRestartIgnoreEconomicGates       *bool    `json:"manual_restart_ignore_economic_gates,omitempty"`
	CooldownProbeEnabled                   *bool    `json:"cooldown_probe_enabled,omitempty"`
	MissionControlHalfLifeSec              *int64   `json:"mc_half_life_sec,omitempty"`
	PaybackModeFlags                       *int     `json:"payback_mode_flags,omitempty"`
	FreshPaidLiquidityLockEnabled          *bool    `json:"fresh_paid_liquidity_lock_enabled,omitempty"`
	FreshPaidLiquidityLockHours            *int     `json:"fresh_paid_liquidity_lock_hours,omitempty"`
	UnlockDays                             *int     `json:"unlock_days,omitempty"`
	CriticalReleasePct                     *float64 `json:"critical_release_pct,omitempty"`
	CriticalMinSources                     *int     `json:"critical_min_sources,omitempty"`
	CriticalMinAvailableSats               *int64   `json:"critical_min_available_sats,omitempty"`
	CriticalCycles                         *int     `json:"critical_cycles,omitempty"`
	RebalanceCostFloorPpm                  *int64   `json:"rebalance_cost_floor_ppm,omitempty"`
	SourceMinPaybackProgress               *float64 `json:"source_min_payback_progress,omitempty"`
	MissionControlReinforce                *bool    `json:"mission_control_reinforce,omitempty"`
	GainModelVersion                       *int     `json:"gain_model_version,omitempty"`
	VelocityWeight                         *float64 `json:"velocity_weight,omitempty"`
	SovereignEVWeightedScoring             *bool    `json:"sovereign_ev_weighted_scoring,omitempty"`
	AutofeeSettlingWindowSec               *int64   `json:"autofee_settling_window_sec,omitempty"`
	AutofeeSettlingMultiplier              *float64 `json:"autofee_settling_multiplier,omitempty"`
	DelegatedFastPathEnabled               *bool    `json:"delegated_fast_path_enabled,omitempty"`
	DelegatedFastPathStrictPayback         *bool    `json:"delegated_fast_path_strict_payback,omitempty"`
	AutoTargetEnabled                      *bool    `json:"auto_target_enabled,omitempty"`
	AutoTargetMaxPct                       *int     `json:"auto_target_max_pct,omitempty"`
	AutoTargetMinPct                       *int     `json:"auto_target_min_pct,omitempty"`
	AutoTargetStepPct                      *int     `json:"auto_target_step_pct,omitempty"`
	AutoTargetEvalIntervalHours            *int     `json:"auto_target_eval_interval_hours,omitempty"`
	AutoTargetMaxUpsPerCycle               *int     `json:"auto_target_max_ups_per_cycle,omitempty"`
	AutoTargetMaxLocalSat                  *int64   `json:"auto_target_max_local_sat,omitempty"`
	AutoTargetMinDrainRateSatPerHr         *int64   `json:"auto_target_min_drain_rate_sat_per_hr,omitempty"`
	AutoTargetMinRevenue7dSat              *int64   `json:"auto_target_min_revenue_7d_sat,omitempty"`
	AutoTargetUpSuccessThreshold           *float64 `json:"auto_target_up_success_threshold,omitempty"`
	AutoTargetDownSuccessThreshold         *float64 `json:"auto_target_down_success_threshold,omitempty"`
	AutoTargetDrainFirstMultiplier         *float64 `json:"auto_target_drain_first_multiplier,omitempty"`
	AutoTargetUpSellThroughFactor          *float64 `json:"auto_target_up_sellthrough_factor,omitempty"`
	AutoTargetDownSellThroughFactor        *float64 `json:"auto_target_down_sellthrough_factor,omitempty"`
	AutoTargetMaxDownsPerCycle             *int     `json:"auto_target_max_downs_per_cycle,omitempty"`
}

type rebalanceRunPayload struct {
	ChannelID         uint64   `json:"channel_id"`
	ChannelPoint      string   `json:"channel_point"`
	TargetOutboundPct *float64 `json:"target_outbound_pct,omitempty"`
	AutoRestart       *bool    `json:"auto_restart,omitempty"`
}

type rebalanceChannelTargetPayload struct {
	ChannelID           uint64   `json:"channel_id"`
	ChannelPoint        string   `json:"channel_point"`
	TargetOutboundPct   *float64 `json:"target_outbound_pct,omitempty"`
	UseDefaultEconRatio *bool    `json:"use_default_econ_ratio,omitempty"`
	EconRatioOverride   *float64 `json:"econ_ratio_override,omitempty"`
	AutoBypassCostGate  *bool    `json:"auto_bypass_cost_gate,omitempty"`
}

type rebalanceChannelAutoPayload struct {
	ChannelID    uint64 `json:"channel_id"`
	ChannelPoint string `json:"channel_point"`
	AutoEnabled  bool   `json:"auto_enabled"`
}

type rebalanceChannelGuaranteedPayload struct {
	ChannelID    uint64 `json:"channel_id"`
	ChannelIDStr string `json:"channel_id_str"`
	ChannelPoint string `json:"channel_point"`
	Enabled      bool   `json:"enabled"`
}

type rebalanceChannelManualRestartPayload struct {
	ChannelID    uint64 `json:"channel_id"`
	ChannelPoint string `json:"channel_point"`
	Enabled      bool   `json:"enabled"`
}

type rebalanceExcludePayload struct {
	ChannelID    uint64 `json:"channel_id"`
	ChannelPoint string `json:"channel_point"`
	Excluded     bool   `json:"excluded"`
}

type rebalanceChannelAutoTargetPayload struct {
	ChannelID    uint64 `json:"channel_id"`
	ChannelPoint string `json:"channel_point"`
	Managed      bool   `json:"managed"`
}

type rebalanceStopPayload struct {
	JobID int64 `json:"job_id"`
}

func (s *Server) handleRebalanceConfigGet(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cfg, err := s.rebalance.GetConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleRebalanceConfigPost(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceConfigPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	if err := validateRebalanceConfigPayload(payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	cfg, err := s.rebalance.GetConfig(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg = applyRebalanceConfigPayload(cfg, payload)

	updated, err := s.rebalance.UpdateConfig(ctx, cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleRebalanceProfilePost is the header selector action. A named profile
// (conservative/balanced/aggressive) turns the autopilot ON and applies that
// posture composed with the node calibration; "custom" freezes the current
// effective values. The UI confirms with the operator before calling this.
func (s *Server) handleRebalanceProfilePost(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload struct {
		Profile string `json:"profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	updated, err := s.rebalance.ApplyProfile(ctx, payload.Profile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// --- Config snapshots: save the current config and restore it later, for
// experimenting with profiles / custom tweaks without losing hand-tuning. ---

func (s *Server) handleRebalanceConfigSnapshotsGet(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	snaps, err := s.rebalance.listConfigSnapshots(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) handleRebalanceConfigSnapshotSave(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	snap, err := s.rebalance.saveConfigSnapshot(ctx, "manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleRebalanceConfigSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	cfg, err := s.rebalance.restoreConfigSnapshot(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleRebalanceConfigSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	if err := s.rebalance.deleteConfigSnapshot(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func applyRebalanceConfigPayload(cfg RebalanceConfig, payload rebalanceConfigPayload) RebalanceConfig {
	if payload.AutoEnabled != nil {
		cfg.AutoEnabled = *payload.AutoEnabled
	}
	if payload.SchedulerMode != nil {
		cfg.SchedulerMode = *payload.SchedulerMode
	}
	if payload.SovereignCandidateScope != nil {
		cfg.SovereignCandidateScope = *payload.SovereignCandidateScope
	}
	if payload.SovereignMaxJobsPerCycle != nil {
		cfg.SovereignMaxJobsPerCycle = *payload.SovereignMaxJobsPerCycle
	}
	if payload.SovereignMinExpectedProfitSat != nil {
		cfg.SovereignMinExpectedProfitSat = *payload.SovereignMinExpectedProfitSat
	}
	if payload.SovereignLowSuccessMinRate != nil {
		cfg.SovereignLowSuccessMinRate = *payload.SovereignLowSuccessMinRate
	}
	if payload.SovereignLowSuccessMinProfitCostRatio != nil {
		cfg.SovereignLowSuccessMinProfitCostRatio = *payload.SovereignLowSuccessMinProfitCostRatio
	}
	if payload.SovereignBudgetEfficiencyMinRatio != nil {
		cfg.SovereignBudgetEfficiencyMinRatio = *payload.SovereignBudgetEfficiencyMinRatio
	}
	if payload.SovereignRouteDeadSourceShare != nil {
		cfg.SovereignRouteDeadSourceShare = *payload.SovereignRouteDeadSourceShare
	}
	if payload.SovereignRiskScoreFloor != nil {
		cfg.SovereignRiskScoreFloor = *payload.SovereignRiskScoreFloor
	}
	if payload.SovereignGainV3ColdStartPct != nil {
		cfg.SovereignGainV3ColdStartPct = *payload.SovereignGainV3ColdStartPct
	}
	if payload.FastPathMaxTimeoutSec != nil {
		cfg.FastPathMaxTimeoutSec = *payload.FastPathMaxTimeoutSec
	}
	if payload.SovereignTopBucketPct != nil {
		cfg.SovereignTopBucketPct = *payload.SovereignTopBucketPct
	}
	if payload.SovereignAttributionWindowHours != nil {
		cfg.SovereignAttributionWindowHours = *payload.SovereignAttributionWindowHours
	}
	if payload.SovereignSlowSellerWindowHours != nil {
		cfg.SovereignSlowSellerWindowHours = *payload.SovereignSlowSellerWindowHours
	}
	if payload.SovereignTargetSourceQuarantineHours != nil {
		cfg.SovereignTargetSourceQuarantineHours = *payload.SovereignTargetSourceQuarantineHours
	}
	if payload.SovereignStructuralCooldownRepeatHours != nil {
		cfg.SovereignStructuralCooldownRepeatHours = *payload.SovereignStructuralCooldownRepeatHours
	}
	if payload.SovereignExplorationSlotPct != nil {
		cfg.SovereignExplorationSlotPct = *payload.SovereignExplorationSlotPct
	}
	if payload.SovereignSourceOpportunityCostEnabled != nil {
		cfg.SovereignSourceOpportunityCostEnabled = *payload.SovereignSourceOpportunityCostEnabled
	}
	if payload.SovereignSlowSellerEnabled != nil {
		cfg.SovereignSlowSellerEnabled = *payload.SovereignSlowSellerEnabled
	}
	if payload.ScanIntervalSec != nil {
		cfg.ScanIntervalSec = *payload.ScanIntervalSec
	}
	if payload.DeadbandPct != nil {
		cfg.DeadbandPct = *payload.DeadbandPct
	}
	if payload.SourceMinLocalPct != nil {
		cfg.SourceMinLocalPct = *payload.SourceMinLocalPct
	}
	if payload.EconRatio != nil {
		cfg.EconRatio = *payload.EconRatio
	}
	if payload.EconRatioMaxPpm != nil {
		cfg.EconRatioMaxPpm = *payload.EconRatioMaxPpm
	}
	if payload.FeeLimitPpm != nil {
		cfg.FeeLimitPpm = *payload.FeeLimitPpm
	}
	if payload.LostProfit != nil {
		cfg.LostProfit = *payload.LostProfit
	}
	if payload.FailTolerancePpm != nil {
		cfg.FailTolerancePpm = *payload.FailTolerancePpm
	}
	if payload.ROIMin != nil {
		cfg.ROIMin = *payload.ROIMin
	}
	if payload.DailyBudgetPct != nil {
		cfg.DailyBudgetPct = *payload.DailyBudgetPct
	}
	if payload.BudgetMode != nil {
		cfg.BudgetMode = *payload.BudgetMode
	}
	if payload.BudgetUnlimited != nil {
		cfg.BudgetUnlimited = *payload.BudgetUnlimited
	}
	if payload.BudgetAutoOnly != nil {
		cfg.BudgetAutoOnly = *payload.BudgetAutoOnly
	}
	if payload.ManualReserveEnabled != nil {
		cfg.ManualReserveEnabled = *payload.ManualReserveEnabled
	}
	if payload.ManualReserveMode != nil {
		cfg.ManualReserveMode = *payload.ManualReserveMode
	}
	if payload.ManualReserveValue != nil {
		cfg.ManualReserveValue = *payload.ManualReserveValue
	}
	if payload.MaxConcurrent != nil {
		cfg.MaxConcurrent = *payload.MaxConcurrent
	}
	if payload.MinAmountSat != nil {
		cfg.MinAmountSat = *payload.MinAmountSat
	}
	if payload.MaxAmountSat != nil {
		cfg.MaxAmountSat = *payload.MaxAmountSat
	}
	if payload.MinSplitEnabled != nil {
		cfg.MinSplitEnabled = *payload.MinSplitEnabled
	}
	if payload.MinProbeSat != nil {
		cfg.MinProbeSat = *payload.MinProbeSat
	}
	if payload.MinExecuteSat != nil {
		cfg.MinExecuteSat = *payload.MinExecuteSat
	}
	if payload.MppEnabled != nil {
		cfg.MppEnabled = *payload.MppEnabled
	}
	if payload.MppMaxShards != nil {
		cfg.MppMaxShards = *payload.MppMaxShards
	}
	if payload.MppParallelism != nil {
		cfg.MppParallelism = *payload.MppParallelism
	}
	if payload.MppMinShardSat != nil {
		cfg.MppMinShardSat = *payload.MppMinShardSat
	}
	if payload.MppRoundTimeoutSec != nil {
		cfg.MppRoundTimeoutSec = *payload.MppRoundTimeoutSec
	}
	if payload.MppAutoOnly != nil {
		cfg.MppAutoOnly = *payload.MppAutoOnly
	}
	if payload.FeeLadderSteps != nil {
		cfg.FeeLadderSteps = *payload.FeeLadderSteps
	}
	if payload.AmountProbeSteps != nil {
		cfg.AmountProbeSteps = *payload.AmountProbeSteps
	}
	if payload.AmountProbeAdaptive != nil {
		cfg.AmountProbeAdaptive = *payload.AmountProbeAdaptive
	}
	if payload.AttemptTimeoutSec != nil {
		cfg.AttemptTimeoutSec = *payload.AttemptTimeoutSec
	}
	if payload.RebalanceTimeoutSec != nil {
		cfg.RebalanceTimeoutSec = *payload.RebalanceTimeoutSec
	}
	if payload.ManualRestartWatch != nil {
		cfg.ManualRestartWatch = *payload.ManualRestartWatch
	}
	if payload.ManualRestartIgnoreEconomicGates != nil {
		cfg.ManualRestartIgnoreEconomicGates = *payload.ManualRestartIgnoreEconomicGates
	}
	if payload.CooldownProbeEnabled != nil {
		cfg.CooldownProbeEnabled = *payload.CooldownProbeEnabled
	}
	if payload.MissionControlHalfLifeSec != nil {
		cfg.MissionControlHalfLifeSec = *payload.MissionControlHalfLifeSec
	}
	if payload.PaybackModeFlags != nil {
		cfg.PaybackModeFlags = *payload.PaybackModeFlags
	}
	if payload.FreshPaidLiquidityLockEnabled != nil {
		cfg.FreshPaidLiquidityLockEnabled = *payload.FreshPaidLiquidityLockEnabled
	}
	if payload.FreshPaidLiquidityLockHours != nil {
		cfg.FreshPaidLiquidityLockHours = *payload.FreshPaidLiquidityLockHours
	}
	if payload.UnlockDays != nil {
		cfg.UnlockDays = *payload.UnlockDays
	}
	if payload.CriticalReleasePct != nil {
		cfg.CriticalReleasePct = *payload.CriticalReleasePct
	}
	if payload.CriticalMinSources != nil {
		cfg.CriticalMinSources = *payload.CriticalMinSources
	}
	if payload.CriticalMinAvailableSats != nil {
		cfg.CriticalMinAvailableSats = *payload.CriticalMinAvailableSats
	}
	if payload.CriticalCycles != nil {
		cfg.CriticalCycles = *payload.CriticalCycles
	}
	if payload.RebalanceCostFloorPpm != nil {
		cfg.RebalanceCostFloorPpm = *payload.RebalanceCostFloorPpm
	}
	if payload.SourceMinPaybackProgress != nil {
		cfg.SourceMinPaybackProgress = *payload.SourceMinPaybackProgress
	}
	if payload.MissionControlReinforce != nil {
		cfg.MissionControlReinforce = *payload.MissionControlReinforce
	}
	if payload.GainModelVersion != nil {
		cfg.GainModelVersion = *payload.GainModelVersion
	}
	if payload.VelocityWeight != nil {
		cfg.VelocityWeight = *payload.VelocityWeight
	}
	if payload.SovereignEVWeightedScoring != nil {
		cfg.SovereignEVWeightedScoring = *payload.SovereignEVWeightedScoring
	}
	if payload.AutofeeSettlingWindowSec != nil {
		cfg.AutofeeSettlingWindowSec = *payload.AutofeeSettlingWindowSec
	}
	if payload.AutofeeSettlingMultiplier != nil {
		cfg.AutofeeSettlingMultiplier = *payload.AutofeeSettlingMultiplier
	}
	if payload.DelegatedFastPathEnabled != nil {
		cfg.DelegatedFastPathEnabled = *payload.DelegatedFastPathEnabled
	}
	if payload.DelegatedFastPathStrictPayback != nil {
		cfg.DelegatedFastPathStrictPayback = *payload.DelegatedFastPathStrictPayback
	}
	if payload.AutoTargetEnabled != nil {
		cfg.AutoTargetEnabled = *payload.AutoTargetEnabled
	}
	if payload.AutoTargetMaxPct != nil {
		cfg.AutoTargetMaxPct = *payload.AutoTargetMaxPct
	}
	if payload.AutoTargetMinPct != nil {
		cfg.AutoTargetMinPct = *payload.AutoTargetMinPct
	}
	if payload.AutoTargetStepPct != nil {
		cfg.AutoTargetStepPct = *payload.AutoTargetStepPct
	}
	if payload.AutoTargetEvalIntervalHours != nil {
		cfg.AutoTargetEvalIntervalHours = *payload.AutoTargetEvalIntervalHours
	}
	if payload.AutoTargetMaxUpsPerCycle != nil {
		cfg.AutoTargetMaxUpsPerCycle = *payload.AutoTargetMaxUpsPerCycle
	}
	if payload.AutoTargetMaxLocalSat != nil {
		cfg.AutoTargetMaxLocalSat = *payload.AutoTargetMaxLocalSat
	}
	if payload.AutoTargetMinDrainRateSatPerHr != nil {
		cfg.AutoTargetMinDrainRateSatPerHr = *payload.AutoTargetMinDrainRateSatPerHr
	}
	if payload.AutoTargetMinRevenue7dSat != nil {
		cfg.AutoTargetMinRevenue7dSat = *payload.AutoTargetMinRevenue7dSat
	}
	if payload.AutoTargetUpSuccessThreshold != nil {
		cfg.AutoTargetUpSuccessThreshold = *payload.AutoTargetUpSuccessThreshold
	}
	if payload.AutoTargetDownSuccessThreshold != nil {
		cfg.AutoTargetDownSuccessThreshold = *payload.AutoTargetDownSuccessThreshold
	}
	if payload.AutoTargetDrainFirstMultiplier != nil {
		cfg.AutoTargetDrainFirstMultiplier = *payload.AutoTargetDrainFirstMultiplier
	}
	if payload.AutoTargetUpSellThroughFactor != nil {
		cfg.AutoTargetUpSellThroughFactor = *payload.AutoTargetUpSellThroughFactor
	}
	if payload.AutoTargetDownSellThroughFactor != nil {
		cfg.AutoTargetDownSellThroughFactor = *payload.AutoTargetDownSellThroughFactor
	}
	if payload.AutoTargetMaxDownsPerCycle != nil {
		cfg.AutoTargetMaxDownsPerCycle = *payload.AutoTargetMaxDownsPerCycle
	}
	return cfg
}

func validateRebalanceConfigPayload(payload rebalanceConfigPayload) error {
	if payload.SchedulerMode != nil && normalizeRebalanceSchedulerMode(*payload.SchedulerMode) != strings.TrimSpace(strings.ToLower(*payload.SchedulerMode)) {
		return errors.New("scheduler_mode must be rules_auto, sovereign_shadow, or sovereign_live")
	}
	if payload.SovereignCandidateScope != nil && normalizeRebalanceSovereignScope(*payload.SovereignCandidateScope) != strings.TrimSpace(strings.ToLower(*payload.SovereignCandidateScope)) {
		return errors.New("sovereign_candidate_scope must be auto_only or auto_and_manual_restart")
	}
	if err := validateOptionalInt("sovereign_max_jobs_per_cycle", payload.SovereignMaxJobsPerCycle, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("sovereign_min_expected_profit_sat", payload.SovereignMinExpectedProfitSat, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("sovereign_low_success_min_rate", payload.SovereignLowSuccessMinRate, 0, 1); err != nil {
		return err
	}
	if err := validateOptionalFloat("sovereign_low_success_min_profit_cost_ratio", payload.SovereignLowSuccessMinProfitCostRatio, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("sovereign_budget_efficiency_min_ratio", payload.SovereignBudgetEfficiencyMinRatio, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("sovereign_route_dead_source_share", payload.SovereignRouteDeadSourceShare, 0.01, 1); err != nil {
		return err
	}
	if err := validateOptionalFloat("sovereign_risk_score_floor", payload.SovereignRiskScoreFloor, 0.001, 0.2); err != nil {
		return err
	}
	if err := validateOptionalFloat("sovereign_gain_v3_cold_start_pct", payload.SovereignGainV3ColdStartPct, sovereignGainV3ColdStartPctMin, sovereignGainV3ColdStartPctMax); err != nil {
		return err
	}
	if err := validateOptionalInt("fast_path_max_timeout_sec", payload.FastPathMaxTimeoutSec, fastPathMaxTimeoutSecMin, fastPathMaxTimeoutSecMax); err != nil {
		return err
	}
	if err := validateOptionalInt("sovereign_top_bucket_pct", payload.SovereignTopBucketPct, sovereignTopBucketPctMin, sovereignTopBucketPctMax); err != nil {
		return err
	}
	if err := validateOptionalInt("sovereign_attribution_window_hours", payload.SovereignAttributionWindowHours, 24, sovereignWindowMaxHours); err != nil {
		return err
	}
	if err := validateOptionalInt("sovereign_slow_seller_window_hours", payload.SovereignSlowSellerWindowHours, 24, sovereignWindowMaxHours); err != nil {
		return err
	}
	if err := validateOptionalInt("sovereign_target_source_quarantine_hours", payload.SovereignTargetSourceQuarantineHours, 0, sovereignWindowMaxHours); err != nil {
		return err
	}
	if err := validateOptionalInt("sovereign_structural_cooldown_repeat_hours", payload.SovereignStructuralCooldownRepeatHours, 1, sovereignTargetStructuralCooldownRepeatMaxHours); err != nil {
		return err
	}
	if err := validateOptionalInt("sovereign_exploration_slot_pct", payload.SovereignExplorationSlotPct, 0, sovereignExplorationSlotPctMax); err != nil {
		return err
	}
	if err := validateOptionalInt("scan_interval_sec", payload.ScanIntervalSec, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("deadband_pct", payload.DeadbandPct, 0, 100); err != nil {
		return err
	}
	if err := validateOptionalFloat("source_min_local_pct", payload.SourceMinLocalPct, 0, 100); err != nil {
		return err
	}
	if err := validateOptionalFloat("econ_ratio", payload.EconRatio, 0.01, 1); err != nil {
		return err
	}
	if err := validateOptionalInt64("econ_ratio_max_ppm", payload.EconRatioMaxPpm, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("fee_limit_ppm", payload.FeeLimitPpm, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("fail_tolerance_ppm", payload.FailTolerancePpm, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("roi_min", payload.ROIMin, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("daily_budget_pct", payload.DailyBudgetPct, 0, 100); err != nil {
		return err
	}
	if payload.BudgetMode != nil && normalizeRebalanceBudgetMode(*payload.BudgetMode) != strings.TrimSpace(strings.ToLower(*payload.BudgetMode)) {
		return errors.New("budget_mode must be revenue_24h_pct or hybrid_revenue")
	}
	if payload.ManualReserveMode != nil && normalizeRebalanceManualReserveMode(*payload.ManualReserveMode) != strings.TrimSpace(strings.ToLower(*payload.ManualReserveMode)) {
		return errors.New("manual_reserve_mode must be fixed_sat or pct")
	}
	if err := validateOptionalFloat("manual_reserve_value", payload.ManualReserveValue, 0, 0); err != nil {
		return err
	}
	if payload.ManualReserveMode != nil && strings.TrimSpace(strings.ToLower(*payload.ManualReserveMode)) == rebalanceManualReserveModePct {
		if err := validateOptionalFloat("manual_reserve_value", payload.ManualReserveValue, 0, 100); err != nil {
			return err
		}
	}
	if err := validateOptionalInt("max_concurrent", payload.MaxConcurrent, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("min_amount_sat", payload.MinAmountSat, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("max_amount_sat", payload.MaxAmountSat, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("min_probe_sat", payload.MinProbeSat, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("min_execute_sat", payload.MinExecuteSat, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("mpp_max_shards", payload.MppMaxShards, 1, 20); err != nil {
		return err
	}
	if err := validateOptionalInt("mpp_parallelism", payload.MppParallelism, 1, 20); err != nil {
		return err
	}
	if err := validateOptionalInt64("mpp_min_shard_sat", payload.MppMinShardSat, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("mpp_round_timeout_sec", payload.MppRoundTimeoutSec, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("fee_ladder_steps", payload.FeeLadderSteps, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("amount_probe_steps", payload.AmountProbeSteps, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("attempt_timeout_sec", payload.AttemptTimeoutSec, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("rebalance_timeout_sec", payload.RebalanceTimeoutSec, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("mc_half_life_sec", payload.MissionControlHalfLifeSec, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("payback_mode_flags", payload.PaybackModeFlags, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("fresh_paid_liquidity_lock_hours", payload.FreshPaidLiquidityLockHours, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("unlock_days", payload.UnlockDays, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("critical_release_pct", payload.CriticalReleasePct, 0, 100); err != nil {
		return err
	}
	if err := validateOptionalInt("critical_min_sources", payload.CriticalMinSources, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("critical_min_available_sats", payload.CriticalMinAvailableSats, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("critical_cycles", payload.CriticalCycles, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("rebalance_cost_floor_ppm", payload.RebalanceCostFloorPpm, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("source_min_payback_progress", payload.SourceMinPaybackProgress, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt("gain_model_version", payload.GainModelVersion, 1, 3); err != nil {
		return err
	}
	if err := validateOptionalFloat("velocity_weight", payload.VelocityWeight, 0, 1); err != nil {
		return err
	}
	if err := validateOptionalInt64("autofee_settling_window_sec", payload.AutofeeSettlingWindowSec, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("autofee_settling_multiplier", payload.AutofeeSettlingMultiplier, 0, 1); err != nil {
		return err
	}
	if err := validateOptionalInt("auto_target_max_pct", payload.AutoTargetMaxPct, 10, 90); err != nil {
		return err
	}
	if err := validateOptionalInt("auto_target_min_pct", payload.AutoTargetMinPct, 1, 89); err != nil {
		return err
	}
	if err := validateOptionalInt("auto_target_step_pct", payload.AutoTargetStepPct, 1, 25); err != nil {
		return err
	}
	if err := validateOptionalInt("auto_target_eval_interval_hours", payload.AutoTargetEvalIntervalHours, 1, 168); err != nil {
		return err
	}
	if err := validateOptionalInt("auto_target_max_ups_per_cycle", payload.AutoTargetMaxUpsPerCycle, 1, 50); err != nil {
		return err
	}
	if err := validateOptionalInt64("auto_target_max_local_sat", payload.AutoTargetMaxLocalSat, 1, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("auto_target_min_drain_rate_sat_per_hr", payload.AutoTargetMinDrainRateSatPerHr, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalInt64("auto_target_min_revenue_7d_sat", payload.AutoTargetMinRevenue7dSat, 0, 0); err != nil {
		return err
	}
	if err := validateOptionalFloat("auto_target_up_success_threshold", payload.AutoTargetUpSuccessThreshold, 0.01, 1); err != nil {
		return err
	}
	if err := validateOptionalFloat("auto_target_down_success_threshold", payload.AutoTargetDownSuccessThreshold, 0.01, 1); err != nil {
		return err
	}
	if err := validateOptionalFloat("auto_target_drain_first_multiplier", payload.AutoTargetDrainFirstMultiplier, 1, 20); err != nil {
		return err
	}
	if err := validateOptionalFloat("auto_target_up_sellthrough_factor", payload.AutoTargetUpSellThroughFactor, 0.1, 5); err != nil {
		return err
	}
	if err := validateOptionalFloat("auto_target_down_sellthrough_factor", payload.AutoTargetDownSellThroughFactor, 0.05, 5); err != nil {
		return err
	}
	if err := validateOptionalInt("auto_target_max_downs_per_cycle", payload.AutoTargetMaxDownsPerCycle, 1, 50); err != nil {
		return err
	}
	if payload.AutoTargetMinPct != nil && payload.AutoTargetMaxPct != nil && *payload.AutoTargetMinPct >= *payload.AutoTargetMaxPct {
		return errors.New("auto_target_min_pct must be < auto_target_max_pct")
	}
	if payload.AutoTargetDownSellThroughFactor != nil && payload.AutoTargetUpSellThroughFactor != nil && *payload.AutoTargetDownSellThroughFactor >= *payload.AutoTargetUpSellThroughFactor {
		return errors.New("auto_target_down_sellthrough_factor must be < auto_target_up_sellthrough_factor")
	}
	if payload.AutoTargetDownSuccessThreshold != nil && payload.AutoTargetUpSuccessThreshold != nil && *payload.AutoTargetDownSuccessThreshold >= *payload.AutoTargetUpSuccessThreshold {
		return errors.New("auto_target_down_success_threshold must be < auto_target_up_success_threshold")
	}
	return nil
}

func validateOptionalInt(field string, value *int, min int, max int) error {
	if value == nil {
		return nil
	}
	if *value < min {
		return fmt.Errorf("%s must be >= %d", field, min)
	}
	if max > 0 && *value > max {
		return fmt.Errorf("%s must be <= %d", field, max)
	}
	return nil
}

func validateOptionalInt64(field string, value *int64, min int64, max int64) error {
	if value == nil {
		return nil
	}
	if *value < min {
		return fmt.Errorf("%s must be >= %d", field, min)
	}
	if max > 0 && *value > max {
		return fmt.Errorf("%s must be <= %d", field, max)
	}
	return nil
}

func validateOptionalFloat(field string, value *float64, min float64, max float64) error {
	if value == nil {
		return nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) {
		return fmt.Errorf("%s must be finite", field)
	}
	if *value < min {
		return fmt.Errorf("%s must be >= %.2f", field, min)
	}
	if max > 0 && *value > max {
		return fmt.Errorf("%s must be <= %.2f", field, max)
	}
	return nil
}

func (s *Server) handleRebalanceOverview(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	overview, err := s.rebalance.Overview(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleRebalanceMissionControlReset(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	state, err := s.rebalance.ResetMissionControl(ctx, "manual")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleRebalanceChannels(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	channels, err := s.rebalance.Channels(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (s *Server) handleRebalancePairStats(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	rawTargetID := strings.TrimSpace(r.URL.Query().Get("target_channel_id"))
	targetPoint := strings.TrimSpace(r.URL.Query().Get("target_channel_point"))
	if rawTargetID == "" && targetPoint == "" {
		writeError(w, http.StatusBadRequest, "target_channel_id or target_channel_point required")
		return
	}
	var targetID uint64
	if rawTargetID != "" {
		parsed, err := strconv.ParseUint(rawTargetID, 10, 64)
		if err != nil || parsed == 0 {
			writeError(w, http.StatusBadRequest, "invalid target_channel_id")
			return
		}
		targetID = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	resolvedID, _, err := s.rebalance.ResolveChannel(ctx, targetID, targetPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stats, err := s.rebalance.PairStats(ctx, resolvedID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pairs": stats})
}

func (s *Server) handleRebalanceQueue(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	jobs, attempts, err := s.rebalance.Queue(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "attempts": attempts})
}

func (s *Server) handleRebalanceHistory(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	jobs, attempts, err := s.rebalance.History(ctx, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "attempts": attempts})
}

func (s *Server) handleRebalanceSovereignHistory(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	includeDecisions := strings.EqualFold(r.URL.Query().Get("include_decisions"), "true") ||
		r.URL.Query().Get("include_decisions") == "1" ||
		strings.EqualFold(r.URL.Query().Get("decisions"), "true") ||
		r.URL.Query().Get("decisions") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	history, err := s.rebalance.SovereignHistory(ctx, limit, includeDecisions)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": history})
}

func (s *Server) handleRebalanceAutoTargetHistory(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	var channelID uint64
	if raw := r.URL.Query().Get("channel_id"); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
			channelID = parsed
		}
	}
	var since time.Time
	if raw := r.URL.Query().Get("since"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			since = parsed
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	items, err := s.rebalance.AutoTargetHistory(ctx, channelID, limit, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleRebalanceRun(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceRunPayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if payload.ChannelID == 0 && strings.TrimSpace(payload.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "channel_id or channel_point required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	targetPct := payload.TargetOutboundPct
	if targetPct != nil {
		if *targetPct <= 0 || *targetPct > 100 {
			writeError(w, http.StatusBadRequest, "target_outbound_pct must be 1-100")
			return
		}
		_ = s.rebalance.SetChannelTarget(ctx, resolvedID, resolvedPoint, *targetPct)
	}
	autoRestart := payload.AutoRestart != nil && *payload.AutoRestart
	// Operator-triggered "Manual Rebal In": bypasses budget/cooldown gates (the
	// operator is acting deliberately) — just queues and executes. The busy
	// guard and per-route fee limit still apply.
	jobID, err := s.rebalance.startOperatorJob(resolvedID, 0, autoRestart)
	if err != nil {
		switch {
		case errors.Is(err, errChannelAutomationParked), errors.Is(err, errManualRestartCooldown), errors.Is(err, errManualBudgetExhausted), errors.Is(err, errManualBudgetInsufficient):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID})
}

func (s *Server) handleRebalanceStop(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceStopPayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if payload.JobID <= 0 {
		writeError(w, http.StatusBadRequest, "job_id required")
		return
	}
	s.rebalance.StopJob(payload.JobID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceChannelTarget(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceChannelTargetPayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if payload.ChannelID == 0 && strings.TrimSpace(payload.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "channel_id or channel_point required")
		return
	}
	targetPct := payload.TargetOutboundPct
	useDefaultEconRatio := payload.UseDefaultEconRatio
	econRatioOverride := payload.EconRatioOverride
	autoBypassCostGate := payload.AutoBypassCostGate
	if targetPct == nil && useDefaultEconRatio == nil && econRatioOverride == nil && autoBypassCostGate == nil {
		writeError(w, http.StatusBadRequest, "target_outbound_pct, econ ratio, or auto cost gate update required")
		return
	}
	if targetPct != nil && (*targetPct <= 0 || *targetPct > 100) {
		writeError(w, http.StatusBadRequest, "target_outbound_pct must be 1-100")
		return
	}
	if econRatioOverride != nil && (*econRatioOverride < 0.01 || *econRatioOverride > 0.99) {
		writeError(w, http.StatusBadRequest, "econ_ratio_override must be 0.01-0.99")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.rebalance.UpdateChannelTargetSettings(ctx, resolvedID, resolvedPoint, targetPct, useDefaultEconRatio, econRatioOverride, autoBypassCostGate); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceChannelAuto(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceChannelAutoPayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.rebalance.SetChannelAuto(ctx, resolvedID, resolvedPoint, payload.AutoEnabled); err != nil {
		if errors.Is(err, errChannelAutomationParked) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceChannelAutoTargetManaged(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceChannelAutoTargetPayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if payload.ChannelID == 0 && strings.TrimSpace(payload.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "channel_id or channel_point required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.rebalance.SetChannelAutoTargetManaged(ctx, resolvedID, resolvedPoint, payload.Managed); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceChannelGuaranteed(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceChannelGuaranteedPayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	channelID := payload.ChannelID
	if raw := strings.TrimSpace(payload.ChannelIDStr); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || parsed == 0 {
			writeError(w, http.StatusBadRequest, "channel_id_str must be a positive uint64")
			return
		}
		channelID = parsed
	}
	if channelID == 0 {
		writeError(w, http.StatusBadRequest, "channel_id or channel_id_str required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, channelID, payload.ChannelPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if resolvedID != channelID {
		writeError(w, http.StatusBadRequest, "channel_id does not match channel_point")
		return
	}
	if err := s.rebalance.SetChannelGuaranteedRebalance(ctx, resolvedID, resolvedPoint, payload.Enabled); err != nil {
		if errors.Is(err, errChannelAutomationParked) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceChannelManualRestart(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceChannelManualRestartPayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if payload.ChannelID == 0 && strings.TrimSpace(payload.ChannelPoint) == "" {
		writeError(w, http.StatusBadRequest, "channel_id or channel_point required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.rebalance.SetChannelManualRestart(ctx, resolvedID, resolvedPoint, payload.Enabled); err != nil {
		if errors.Is(err, errChannelAutomationParked) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceExclude(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	var payload rebalanceExcludePayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	resolvedID, resolvedPoint, err := s.rebalance.ResolveChannel(ctx, payload.ChannelID, payload.ChannelPoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.rebalance.SetSourceExcluded(ctx, resolvedID, resolvedPoint, payload.Excluded); err != nil {
		if errors.Is(err, errChannelAutomationParked) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRebalanceMetricsBaseline(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			days = parsed
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	metrics, err := s.rebalance.BaselineMetrics(ctx, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) handleRebalanceStream(w http.ResponseWriter, r *http.Request) {
	if s.rebalance == nil {
		writeError(w, http.StatusServiceUnavailable, "rebalance unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.rebalance.Subscribe()
	defer s.rebalance.Unsubscribe(ch)

	_, _ = w.Write([]byte("event: ready\ndata: {}\n\n"))
	flusher.Flush()

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-ch:
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-ticker.C:
			_, _ = w.Write([]byte("event: heartbeat\ndata: {}\n\n"))
			flusher.Flush()
		}
	}
}
