package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	autofeeConfigID              = 1
	autofeeMinLookbackDays       = 5
	autofeeMaxLookbackDays       = 21
	autofeeMinCooldownSec        = 3600
	autofeeNativeSeedMinDays     = 3
	autofeeNativeSeedMinSamples  = 6
	autofeeIdleRefreshWindowDays = 7
	autofeeRefreshRebalMarkup    = 0.10
	autofeeMinApplyDeltaPpm      = 5
	autofeeMinApplyDeltaFrac     = 0.01
)

const autofeeRebalanceSettlingWindow = 30 * time.Minute

const superSourceBaseFeeMsatDefault = 1000
const rebalCostModeDefault = "blend"
const htlcModeDefault = "full"
const autofeeOperationModeDefault = "balanced"

const (
	autofeeOperationModeBalanced     = "balanced"
	autofeeOperationModeMarketRefill = "market_refill"
)

const (
	htlcModeObserveOnly = "observe_only"
	htlcModePolicyOnly  = "policy_only"
	htlcModeFull        = "full"
)

const (
	defaultLowOutProtectThresh          = 0.10
	defaultSinkMinMargin                = 150
	defaultMinSoftCeiling               = 100
	defaultSeedCeilingMult              = 1.50
	defaultSeedFloorMult                = 1.10
	defaultSeedShockMinFrac             = 0.35
	seedShockConfirmRounds              = 2
	defaultSeedP95Boost                 = 1.15
	defaultSourceSeedTargetFrac         = 0.55
	defaultProfitProtectOutRatio        = 0.10
	defaultProfitProtectRelaxHours      = 72
	defaultProfitProtectRelaxMaxFwds    = 1
	defaultProfitProtectRelaxStepFrac   = 0.015
	defaultProfitProtectRelaxMinStepPpm = 15
	defaultGlobalNegLockSoften          = true
	defaultSoftenMinOutRatio            = 0.45
	defaultSoftenRequirePosChanMargin   = true
	defaultSoftenMaxDropToPegFrac       = 0.95
	stagnationOutRatioMin               = 0.30
	stagnationNoForwardRoundsPhase1     = 2
	stagnationNoForwardRoundsPhase2     = 4
	stagnationOutrateTriggerMult        = 1.20
	stagnationRebalTriggerMult          = 1.15
	stagnationOutrateHeadroom           = 1.05
	stagnationRebalHeadroom             = 1.08
	stagnationSeedBlendPhase1           = 0.20
	stagnationSeedBlendPhase2           = 0.10
	lowOutFactorDrained                 = 1.20
	lowOutFactorBalanced                = 0.95
	lowOutFactorFull                    = 0.85
	lowOutFactorMin                     = 0.75
	lowOutFactorMax                     = 1.30
	lowOutFactorRatioAdjMax             = 0.10
	lowOutThreshMin                     = 0.03
	lowOutThreshMax                     = 0.35
	lowOutNoFlowBumpCap                 = 0.08
	lowOutNoFlowBoostMult               = 1.005
	lowOutNoFlowUpCapFrac               = 0.03
	lowOutNoFlowUpperRatio              = 0.15
	outFallback21dMinFwds               = 5
	outFallback21dMinOutSat             = 50000
	outFallback21dMinOutCapFrac         = 0.005
	rebalFallback21dMinAmtSat           = 30000
	rebalFallback21dMinAmtCapFrac       = 0.003
	stagnationHighOutRatio              = 0.50
	stagnationExitMinFwds1d             = 3
	stagnationExitMinOutSat1d           = 20000
	stagnationExitMinOutCapFrac         = 0.005
	surgeConfirmMinRounds               = 2
	surgeConfirmRebalCapFrac            = 0.015
	floorRebalMinCapFrac                = 0.015
	floorRebalMinSuccessCount           = int64(2)
	weakRebalanceAttemptCountWeight     = 0.35
	weakRebalanceAttemptAmtWeight       = 0.25
	defaultSurgeHoldMaxRounds           = 6
	defaultSurgeHoldUnlockStepPpm       = 15
	defaultBootstrapHours               = 48
	defaultBootstrapOutRatioMax         = 0.40
	defaultBootstrapCooldownUpSec       = 3600
	defaultBootstrapCooldownDownSec     = 5400
	defaultBootstrapMinStepUpPpm        = 15
	defaultBootstrapSurgeHoldMaxRounds  = 2
	matureEmptySinkHistoryHours         = 7 * 24
	stallFloorRelaxMinRounds            = 1
	stallFloorRelaxGapFrac              = 0.20
	stallFloorRelaxStepFracBase         = 0.04
	stallFloorRelaxStepFracMax          = 0.06
	stallFloorRelaxStepFracRoundWindow  = 4
	stallFloorRelaxMinStepPpm           = 15
	stallFloorRelaxMaxStepPpm           = 180
	stallFloorRelaxMinOutRatio          = 0.20
	stallAlertMinRounds                 = 2
	stallAlertGapFrac                   = 0.50
	floorDrivenSmallUpMinStepPpm        = 10
	reversalConfirmMinRounds            = 2
	reversalFastTrackStallMinRounds     = 2
	rescueEnterGapFrac                  = 0.20
	rescuePriorityEnterGapFrac          = 0.35
	rescueExitGapFrac                   = 0.08
	rescueOutrateEnterMult              = 1.03
	rescueOutrateExitMult               = 1.05
	rescueTargetOutrateDivergenceFrac   = 0.03
	rescueSoftRebalFloorMult            = 1.02
	rescueScoreMax                      = 55
	rescueRevShareMax                   = 0.02
	rescueMinRounds                     = 3
	rescueMinActiveHours                = 12
	rescueMaxActiveHours                = 10 * 24
	rescueReentryCooldownHours          = 24
	rescueExitConfirmRounds             = 2
	htlcLowSampleMaxDownFrac            = 0.05
	generalMaxDownFrac                  = 0.08
	channelSizeRatioExponent            = 0.35
	channelSizeRatioFactorMin           = 0.45
	channelSizeRatioFactorMax           = 1.80
	channelOutlierBlendFullAtRatio      = 3.00
	channelOutlierAbsBlendMax           = 0.60
	channelLargeAbsLocalMinAvg          = 0.60
	channelLargeAbsLocalFullAvg         = 1.00
	channelLargeAbsOutRatioMin          = 0.30
	channelLargeAbsOutRatioMax          = 0.45
	channelNodeRatioLow                 = 0.30
	channelNodeRatioHigh                = 0.70
	channelNodeRatioAdjMax              = 0.05
	channelOutlierLargeCapRelMin        = 2.00
	channelOutlierSmallCapRelMax        = 0.50
	channelOutNormTagDiffMin            = 0.04
	seedFullHoldOutRatioMin             = 0.90
	classificationBiasEMAAlpha          = 0.60
	classificationSinkBiasMin           = 0.45
	classificationSourceBiasMax         = -0.35
	classificationRouterBiasAbsMax      = 0.25
	classificationLabelSwitchMinDelta   = 0.07
	defaultInboundDiscountMaxRatio      = 0.90
)

func normalizeRebalCostMode(value string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case "blend", "channel", "global":
		return mode
	default:
		return rebalCostModeDefault
	}
}

func normalizeHTLCMode(value string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case htlcModeObserveOnly, htlcModePolicyOnly, htlcModeFull:
		return mode
	default:
		return htlcModeDefault
	}
}

func normalizeAutofeeOperationMode(value string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case autofeeOperationModeBalanced, autofeeOperationModeMarketRefill:
		return mode
	default:
		return autofeeOperationModeDefault
	}
}

type AutofeeConfig struct {
	Enabled                                      bool                              `json:"enabled"`
	OperationMode                                string                            `json:"operation_mode"`
	Profile                                      string                            `json:"profile"`
	LookbackDays                                 int                               `json:"lookback_days"`
	RunIntervalSec                               int                               `json:"run_interval_sec"`
	CooldownUpSec                                int                               `json:"cooldown_up_sec"`
	CooldownDownSec                              int                               `json:"cooldown_down_sec"`
	StepCapOverride                              float64                           `json:"step_cap_override"`
	DiscoveryStepCapDownOverride                 float64                           `json:"discovery_step_cap_down_override"`
	StallFloorRelaxGapFracOverride               float64                           `json:"stall_floor_relax_gap_frac_override"`
	InboundDiscountMaxRatioOverride              float64                           `json:"inbound_discount_max_ratio_override"`
	InboundDiscountReachOutRatioOverride         float64                           `json:"inbound_discount_reach_out_ratio_override"`
	InboundDiscountMinRetainedSpreadFracOverride float64                           `json:"inbound_discount_min_retained_spread_frac_override"`
	OutrateFloorFactorLowOverride                float64                           `json:"outrate_floor_factor_low_override"`
	SoftenMinOutRatioOverride                    float64                           `json:"soften_min_out_ratio_override"`
	SoftenMaxDropToPegFracOverride               float64                           `json:"soften_max_drop_to_peg_frac_override"`
	HTLCMinAttempts60mOverride                   int                               `json:"htlc_min_attempts_60m_override"`
	HTLCPolicyFailRateOverride                   float64                           `json:"htlc_policy_fail_rate_override"`
	HTLCLiquidityFailRateOverride                float64                           `json:"htlc_liquidity_fail_rate_override"`
	RebalCostMode                                string                            `json:"rebal_cost_mode"`
	NativeSeedEnabled                            bool                              `json:"native_seed_enabled"`
	AmbossEnabled                                bool                              `json:"amboss_enabled"`
	AmbossTokenSet                               bool                              `json:"amboss_token_set"`
	InboundPassiveEnabled                        bool                              `json:"inbound_passive_enabled"`
	DiscoveryEnabled                             bool                              `json:"discovery_enabled"`
	ExplorerEnabled                              bool                              `json:"explorer_enabled"`
	IdleRefreshEnabled                           bool                              `json:"idle_refresh_enabled"`
	SuperSourceEnabled                           bool                              `json:"super_source_enabled"`
	SuperSourceBaseFeeMsat                       int                               `json:"super_source_base_fee_msat"`
	RevfloorEnabled                              bool                              `json:"revfloor_enabled"`
	CircuitBreakerEnabled                        bool                              `json:"circuit_breaker_enabled"`
	ExtremeDrainEnabled                          bool                              `json:"extreme_drain_enabled"`
	HTLCSignalEnabled                            bool                              `json:"htlc_signal_enabled"`
	HTLCMode                                     string                            `json:"htlc_mode"`
	MinPpm                                       int                               `json:"min_ppm"`
	MaxPpm                                       int                               `json:"max_ppm"`
	ProfileDefaults                              map[string]AutofeeProfileDefaults `json:"profile_defaults,omitempty"`
}

type AutofeeProfileDefaults struct {
	RunIntervalSec                       int     `json:"run_interval_sec"`
	CooldownUpSec                        int     `json:"cooldown_up_sec"`
	CooldownDownSec                      int     `json:"cooldown_down_sec"`
	StepCap                              float64 `json:"step_cap"`
	DiscoveryStepCapDown                 float64 `json:"discovery_step_cap_down"`
	StallFloorRelaxGapFrac               float64 `json:"stall_floor_relax_gap_frac"`
	InboundDiscountMaxRatio              float64 `json:"inbound_discount_max_ratio"`
	InboundDiscountReachOutRatio         float64 `json:"inbound_discount_reach_out_ratio"`
	InboundDiscountMinRetainedSpreadFrac float64 `json:"inbound_discount_min_retained_spread_frac"`
	OutrateFloorFactorLow                float64 `json:"outrate_floor_factor_low"`
	SoftenMinOutRatio                    float64 `json:"soften_min_out_ratio"`
	SoftenMaxDropToPegFrac               float64 `json:"soften_max_drop_to_peg_frac"`
	HTLCMinAttempts60m                   int     `json:"htlc_min_attempts_60m"`
	HTLCPolicyFailRate                   float64 `json:"htlc_policy_fail_rate"`
	HTLCLiquidityFailRate                float64 `json:"htlc_liquidity_fail_rate"`
}

type AutofeeConfigUpdate struct {
	Enabled                                      *bool    `json:"enabled,omitempty"`
	OperationMode                                *string  `json:"operation_mode,omitempty"`
	Profile                                      *string  `json:"profile,omitempty"`
	LookbackDays                                 *int     `json:"lookback_days,omitempty"`
	RunIntervalSec                               *int     `json:"run_interval_sec,omitempty"`
	CooldownUpSec                                *int     `json:"cooldown_up_sec,omitempty"`
	CooldownDownSec                              *int     `json:"cooldown_down_sec,omitempty"`
	StepCapOverride                              *float64 `json:"step_cap_override,omitempty"`
	DiscoveryStepCapDownOverride                 *float64 `json:"discovery_step_cap_down_override,omitempty"`
	StallFloorRelaxGapFracOverride               *float64 `json:"stall_floor_relax_gap_frac_override,omitempty"`
	InboundDiscountMaxRatioOverride              *float64 `json:"inbound_discount_max_ratio_override,omitempty"`
	InboundDiscountReachOutRatioOverride         *float64 `json:"inbound_discount_reach_out_ratio_override,omitempty"`
	InboundDiscountMinRetainedSpreadFracOverride *float64 `json:"inbound_discount_min_retained_spread_frac_override,omitempty"`
	OutrateFloorFactorLowOverride                *float64 `json:"outrate_floor_factor_low_override,omitempty"`
	SoftenMinOutRatioOverride                    *float64 `json:"soften_min_out_ratio_override,omitempty"`
	SoftenMaxDropToPegFracOverride               *float64 `json:"soften_max_drop_to_peg_frac_override,omitempty"`
	HTLCMinAttempts60mOverride                   *int     `json:"htlc_min_attempts_60m_override,omitempty"`
	HTLCPolicyFailRateOverride                   *float64 `json:"htlc_policy_fail_rate_override,omitempty"`
	HTLCLiquidityFailRateOverride                *float64 `json:"htlc_liquidity_fail_rate_override,omitempty"`
	RebalCostMode                                *string  `json:"rebal_cost_mode,omitempty"`
	NativeSeedEnabled                            *bool    `json:"native_seed_enabled,omitempty"`
	AmbossEnabled                                *bool    `json:"amboss_enabled,omitempty"`
	AmbossToken                                  *string  `json:"amboss_token,omitempty"`
	InboundPassiveEnabled                        *bool    `json:"inbound_passive_enabled,omitempty"`
	DiscoveryEnabled                             *bool    `json:"discovery_enabled,omitempty"`
	ExplorerEnabled                              *bool    `json:"explorer_enabled,omitempty"`
	IdleRefreshEnabled                           *bool    `json:"idle_refresh_enabled,omitempty"`
	SuperSourceEnabled                           *bool    `json:"super_source_enabled,omitempty"`
	SuperSourceBaseFeeMsat                       *int     `json:"super_source_base_fee_msat,omitempty"`
	RevfloorEnabled                              *bool    `json:"revfloor_enabled,omitempty"`
	CircuitBreakerEnabled                        *bool    `json:"circuit_breaker_enabled,omitempty"`
	ExtremeDrainEnabled                          *bool    `json:"extreme_drain_enabled,omitempty"`
	HTLCSignalEnabled                            *bool    `json:"htlc_signal_enabled,omitempty"`
	HTLCMode                                     *string  `json:"htlc_mode,omitempty"`
	MinPpm                                       *int     `json:"min_ppm,omitempty"`
	MaxPpm                                       *int     `json:"max_ppm,omitempty"`
}

type AutofeeStatus struct {
	Running   bool   `json:"running"`
	LastRunAt string `json:"last_run_at,omitempty"`
	NextRunAt string `json:"next_run_at,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type AutofeeChannelSettingEntry struct {
	ChannelID    uint64
	ChannelPoint string
	Enabled      bool
}

type AutofeeRefreshItem struct {
	ChannelID              uint64 `json:"channel_id"`
	ChannelPoint           string `json:"channel_point"`
	Alias                  string `json:"alias,omitempty"`
	CurrentPpm             int    `json:"current_ppm"`
	TargetPpm              int    `json:"target_ppm,omitempty"`
	ReferencePpm           int    `json:"reference_ppm,omitempty"`
	Source                 string `json:"source,omitempty"`
	CurrentInboundDiscount int    `json:"current_inbound_discount,omitempty"`
	TargetInboundDiscount  int    `json:"target_inbound_discount,omitempty"`
	InboundSource          string `json:"inbound_source,omitempty"`
	Changed                bool   `json:"changed"`
	Applied                bool   `json:"applied"`
	Reason                 string `json:"reason,omitempty"`
	Error                  string `json:"error,omitempty"`
}

type AutofeeRefreshResult struct {
	Total          int                  `json:"total"`
	Updated        int                  `json:"updated"`
	InboundUpdated int                  `json:"inbound_updated"`
	Same           int                  `json:"same"`
	Skipped        int                  `json:"skipped"`
	Errors         int                  `json:"errors"`
	DryRun         bool                 `json:"dry_run"`
	IncludeInbound bool                 `json:"include_inbound"`
	RebalMarkupPct float64              `json:"rebal_markup_pct"`
	Items          []AutofeeRefreshItem `json:"items,omitempty"`
}

type autofeeLogItem struct {
	Kind                     string   `json:"kind"`
	Category                 string   `json:"category,omitempty"`
	Reason                   string   `json:"reason,omitempty"`
	OperationMode            string   `json:"operation_mode,omitempty"`
	DryRun                   bool     `json:"dry_run,omitempty"`
	Timestamp                string   `json:"timestamp,omitempty"`
	NodeClass                string   `json:"node_class,omitempty"`
	LiquidityClass           string   `json:"liquidity_class,omitempty"`
	ChannelCount             int      `json:"channel_count,omitempty"`
	TotalCapacitySat         int64    `json:"total_capacity_sat,omitempty"`
	AvgCapacitySat           int64    `json:"avg_capacity_sat,omitempty"`
	LocalCapacitySat         int64    `json:"local_capacity_sat,omitempty"`
	LocalRatio               float64  `json:"local_ratio,omitempty"`
	RevfloorBaseline         int      `json:"revfloor_baseline,omitempty"`
	RevfloorMinAbs           int      `json:"revfloor_min_abs,omitempty"`
	Up                       int      `json:"up,omitempty"`
	Down                     int      `json:"down,omitempty"`
	Flat                     int      `json:"flat,omitempty"`
	Cooldown                 int      `json:"cooldown,omitempty"`
	Small                    int      `json:"small,omitempty"`
	Same                     int      `json:"same,omitempty"`
	Disabled                 int      `json:"disabled,omitempty"`
	Inactive                 int      `json:"inactive,omitempty"`
	InboundDisc              int      `json:"inbound_disc"`
	SuperSource              int      `json:"super_source,omitempty"`
	HTLCLiqHot               int      `json:"htlc_liq_hot,omitempty"`
	HTLCPolicyHot            int      `json:"htlc_policy_hot,omitempty"`
	HTLCForwardHot           int      `json:"htlc_forward_hot,omitempty"`
	HTLCSampleLow            int      `json:"htlc_sample_low,omitempty"`
	ReversalBlocked          int      `json:"reversal_blocked,omitempty"`
	ReversalConfirmed        int      `json:"reversal_confirmed,omitempty"`
	DowncapGeneral           int      `json:"downcap_general,omitempty"`
	DowncapLowSample         int      `json:"downcap_low_sample,omitempty"`
	FloorRelaxApplied        int      `json:"floor_relax_applied,omitempty"`
	StallAlert               int      `json:"stall_alert,omitempty"`
	HTLCAttemptsTotal        int      `json:"htlc_attempts_total,omitempty"`
	HTLCLinkFailsTotal       int      `json:"htlc_link_fails_total,omitempty"`
	HTLCForwardFailsTotal    int      `json:"htlc_forward_fails_total,omitempty"`
	HTLCOtherFailsTotal      int      `json:"htlc_other_fails_total,omitempty"`
	HTLCClassifiedTotal      int      `json:"htlc_classified_total,omitempty"`
	HTLCClassifiedRatio      float64  `json:"htlc_classified_ratio,omitempty"`
	HTLCUnclassifiedTotal    int      `json:"htlc_unclassified_total,omitempty"`
	HTLCNoisySampleApplied   bool     `json:"htlc_noisy_sample_applied,omitempty"`
	HTLCTopReasons           []string `json:"htlc_top_reasons,omitempty"`
	HTLCWindowMin            int      `json:"htlc_window_min,omitempty"`
	HTLCMinAttempts          int      `json:"htlc_min_attempts,omitempty"`
	HTLCMinPolicyFails       int      `json:"htlc_min_policy_fails,omitempty"`
	HTLCMinLiquidityFails    int      `json:"htlc_min_liquidity_fails,omitempty"`
	HTLCMinForwardFails      int      `json:"htlc_min_forward_fails,omitempty"`
	HTLCPolicyFailRateMin    float64  `json:"htlc_policy_fail_rate_min,omitempty"`
	HTLCLiquidityFailRateMin float64  `json:"htlc_liquidity_fail_rate_min,omitempty"`
	HTLCForwardFailRateMin   float64  `json:"htlc_forward_fail_rate_min,omitempty"`
	HTLCGlobalCountFactor    float64  `json:"htlc_global_count_factor,omitempty"`
	HTLCGlobalRateFactor     float64  `json:"htlc_global_rate_factor,omitempty"`
	HTLCNodeFactor           float64  `json:"htlc_node_factor,omitempty"`
	HTLCLiquidityFactor      float64  `json:"htlc_liquidity_factor,omitempty"`
	HTLCThresholdFactor      float64  `json:"htlc_threshold_factor,omitempty"`
	LowOutThresh             float64  `json:"low_out_thresh,omitempty"`
	LowOutProtectThresh      float64  `json:"low_out_protect_thresh,omitempty"`
	LowOutFactor             float64  `json:"low_out_factor,omitempty"`
	Native                   int      `json:"native,omitempty"`
	NativeInsufficient       int      `json:"native_insufficient,omitempty"`
	NativeErr                int      `json:"native_err,omitempty"`
	Amboss                   int      `json:"amboss,omitempty"`
	Missing                  int      `json:"missing,omitempty"`
	Err                      int      `json:"err,omitempty"`
	Empty                    int      `json:"empty,omitempty"`
	Outrate                  int      `json:"outrate,omitempty"`
	Mem                      int      `json:"mem,omitempty"`
	Default                  int      `json:"default,omitempty"`
	CooldownIgnored          bool     `json:"cooldown_ignored,omitempty"`
	UpdatedCount             int      `json:"updated_count,omitempty"`
	SameCount                int      `json:"same_count,omitempty"`
	SkippedCount             int      `json:"skipped_count,omitempty"`
	ErrorCount               int      `json:"error_count,omitempty"`
	RebalMarkupPct           float64  `json:"rebal_markup_pct,omitempty"`
	Alias                    string   `json:"alias,omitempty"`
	ChannelID                uint64   `json:"channel_id,omitempty"`
	ChannelPoint             string   `json:"channel_point,omitempty"`
	LocalPpm                 int      `json:"local_ppm,omitempty"`
	NewPpm                   int      `json:"new_ppm,omitempty"`
	Target                   int      `json:"target,omitempty"`
	TargetRaw                int      `json:"target_raw,omitempty"`
	TargetFinal              int      `json:"target_final,omitempty"`
	OutRatio                 float64  `json:"out_ratio,omitempty"`
	OutRatioEffective        float64  `json:"out_ratio_effective,omitempty"`
	OutPpm7d                 int      `json:"out_ppm7d,omitempty"`
	RebalPpm7d               int      `json:"rebal_ppm7d,omitempty"`
	Seed                     int      `json:"seed,omitempty"`
	Floor                    int      `json:"floor,omitempty"`
	FloorSrc                 string   `json:"floor_src,omitempty"`
	FloorBasePpm             int      `json:"floor_base_ppm,omitempty"`
	FloorBaseSrc             string   `json:"floor_base_src,omitempty"`
	RebalCostMode            string   `json:"rebal_cost_mode,omitempty"`
	Margin                   int      `json:"margin,omitempty"`
	RevShare                 float64  `json:"rev_share,omitempty"`
	Tags                     []string `json:"tags,omitempty"`
	InboundDiscount          int      `json:"inbound_discount,omitempty"`
	PrevInboundDiscount      int      `json:"prev_inbound_discount,omitempty"`
	ClassLabel               string   `json:"class_label,omitempty"`
	SkipReason               string   `json:"skip_reason,omitempty"`
	Error                    string   `json:"error,omitempty"`
	Delta                    int      `json:"delta,omitempty"`
	DeltaPct                 float64  `json:"delta_pct,omitempty"`
	PredictionCode           string   `json:"prediction_code,omitempty"`
	PredictionCooldownHours  int      `json:"prediction_cooldown_hours,omitempty"`
	NewInbound               bool     `json:"new_inbound,omitempty"`
	ChannelAgeHours          float64  `json:"channel_age_hours,omitempty"`
	HTLCAttempts             int      `json:"htlc_attempts,omitempty"`
	HTLCPolicyFails          int      `json:"htlc_policy_fails,omitempty"`
	HTLCLiquidityFails       int      `json:"htlc_liquidity_fails,omitempty"`
	HTLCForwardFails         int      `json:"htlc_forward_fails,omitempty"`
	HTLCUnclassifiedFails    int      `json:"htlc_unclassified_fails,omitempty"`
	HTLCWindowMinChannel     int      `json:"htlc_window_min_channel,omitempty"`
	StagnationPhase          int      `json:"stagnation_phase,omitempty"`
	StagnationRounds         int      `json:"stagnation_rounds,omitempty"`
	StagnationCap            int      `json:"stagnation_cap,omitempty"`
	StalledRounds            int      `json:"stalled_rounds,omitempty"`
	HoursSinceLastChange     float64  `json:"hours_since_last_change,omitempty"`
	TargetGapPpm             int      `json:"target_gap_ppm,omitempty"`
	TargetGapPct             float64  `json:"target_gap_pct,omitempty"`
	ReferencePpm             int      `json:"reference_ppm,omitempty"`
	RefreshSource            string   `json:"refresh_source,omitempty"`
	CurrentInboundDiscount   int      `json:"current_inbound_discount"`
	TargetInboundDiscount    int      `json:"target_inbound_discount"`
	InboundSource            string   `json:"inbound_source,omitempty"`
	IncludeInbound           bool     `json:"include_inbound"`
}

type autofeeProfile struct {
	Name                                 string
	StepCap                              float64
	LowOutThresh                         float64
	LowOutProtectThresh                  float64
	HighOutThresh                        float64
	SurgeBumpMax                         float64
	RunIntervalSec                       int
	CooldownUpSec                        int
	CooldownDownSec                      int
	CooldownUpDrainedSec                 int
	CooldownUpExtremeSec                 int
	CooldownUpDrainedOutRatioMax         float64
	CooldownUpExtremeOutRatioMax         float64
	DiscoveryStepCapDown                 float64
	StallFloorRelaxGapFrac               float64
	SeedGuardMaxJump                     float64
	OutratePegGraceHours                 int
	ProfitDownMarginMin                  int
	ProfitDownFwdsMin                    int
	ProfitDownExtraHours                 int
	NegMarginSurgeBump                   float64
	NegMarginSurgeMinFwds                int
	NegMarginSurgeFwdsRatio              float64
	SinkExtraFloorMargin                 float64
	RevfloorBaselineThresh               int
	RevfloorMinAbs                       int
	RevfloorBaselineScale                float64
	RevfloorMinAbsScale                  float64
	DiscHarddropDaysNoBase               int
	DiscHarddropCapFrac                  float64
	DiscHarddropCushion                  int
	DiscRequireExplorer                  bool
	DiscAfterExplorerDays                int
	OutrateFloorFactorLow                float64
	ExplorerSkipCooldownDown             bool
	CircuitBreakerDropRatio              float64
	CircuitBreakerReduceStep             float64
	CircuitBreakerGraceDays              int
	ExtremeDrainStreak                   int
	ExtremeDrainOutMax                   float64
	ExtremeDrainStepCap                  float64
	ExtremeDrainMinStepPpm               int
	ExtremeDrainTurboStreak              int
	ExtremeDrainTurboOutMax              float64
	ExtremeDrainTurboStepCap             float64
	ExtremeDrainTurboMinStepPpm          int
	SinkMinMargin                        int
	MinSoftCeiling                       int
	SeedCeilingMult                      float64
	SeedFloorMult                        float64
	SeedP95Boost                         float64
	SeedOutrateCapMult                   float64
	SeedRebalCapMult                     float64
	SourceSeedTargetFrac                 float64
	ProfitProtectOutRatio                float64
	ProfitProtectMarginPpm               int
	ProfitProtectRelaxHours              int
	ProfitProtectRelaxMaxFwds            int
	ProfitProtectRelaxMarginPpm          int
	ProfitProtectRelaxStepFrac           float64
	ProfitProtectRelaxMinStepPpm         int
	InboundDiscountReachOutRatio         float64
	InboundDiscountMinRetainedSpreadFrac float64
	MarketRefillNetInboundTargetFrac     float64
	MarketRefillCandidateOutRatioMax     float64
	MarketRefillExploratoryOutRatioMax   float64
	MarketRefillExploratoryFwds1dMax     int
	MarketRefillBaseTargetMult           float64
	MarketRefillBaseTargetAddPpm         int
	MarketRefillLowTargetMult            float64
	MarketRefillLowTargetAddPpm          int
	MarketRefillDrainedTargetMult        float64
	MarketRefillDrainedTargetAddPpm      int
	MarketRefillLocalHoldFrac            float64
	MarketRefillLocalCapMult             float64
	MarketRefillMinOutboundPpm           int
	MarketRefillMinOutboundMaxPpmFrac    float64
	MarketRefillOutrateFloorFrac         float64
	GlobalNegLockSoften                  bool
	SoftenMinOutRatio                    float64
	SoftenRequirePosChanMargin           bool
	SoftenMaxDropToPegFrac               float64
	HTLCMinAttempts60m                   int
	HTLCPolicyFailRate                   float64
	HTLCPolicyMinFails                   int
	HTLCLiquidityFailRate                float64
	HTLCLiquidityMinFails                int
	HTLCLiquidityHotBump                 float64
	HTLCLiquidityHotNoDownOutRatio       float64
	HTLCPolicyHotBump                    float64
	HTLCPolicyHotNoDownMarginPpm         int
	HTLCHotStepCapBoost                  float64
	LargeGapStepCapBoostPctMin           float64
	LargeGapStepCapBoost                 float64
	LargeGapStepCapBoostPctStrong        float64
	LargeGapStepCapBoostStrong           float64
	SurgeHoldMaxRounds                   int
	SurgeHoldUnlockStepPpm               int
	BootstrapHours                       int
	BootstrapOutRatioMax                 float64
	BootstrapCooldownUpSec               int
	BootstrapCooldownDownSec             int
	BootstrapMinStepUpPpm                int
	BootstrapSurgeHoldMaxRounds          int
	HoldSmallMinDeltaPpm                 int
	HoldSmallMinRelFrac                  float64
	HoldSmallGapBypassPpm                int
	HoldSmallGapBypassFrac               float64
	HoldSmallStallBypassRounds           int
	ReversalConfirmMinRounds             int
	ReversalFastTrackStallRounds         int
	ReversalFastTrackGapFrac             float64
	AntiFlipWindowHours                  int
	AntiFlipExtraConfirmRounds           int
	AntiFlipStrongGapFrac                float64
	DrainedExplorerAfterDays             int
	DrainedExplorerOutRatioMax           float64
	DrainedExplorerRecentFwds1dMax       int
	DrainedExplorerMaxHours              int
	DrainedExplorerMaxRounds             int
	DrainedExplorerStepPpm               int
	BalancedUpOutRatioMin                float64
	BalancedFloorUpCap                   float64
	OutrateTargetOutRatioMin             float64
	OutrateTargetMinFwds                 int
	OutrateTargetFloorFrac               float64
	OutrateTargetBlendFrac               float64
	RebalFailOutRatioMax                 float64
	RebalFailNoDownMinAttempts           int
	RebalFailUpMinAttempts               int
	RebalFailUpStepFrac                  float64
	RebalFailUpMinStepPpm                int
	RebalFailWindowHours                 int
	RebalFailCampaignGapMin              int
}

type superSourceThresholds struct {
	OutRatioMin  float64
	OutAmt1dMult float64
	OutAmt7dMult float64
	MinFwds7d    int
	EnterHours   int
	ExitHours    int
}

var autofeeProfiles = map[string]autofeeProfile{
	"conservative": {
		Name:                                 "conservative",
		StepCap:                              0.05,
		LowOutThresh:                         0.08,
		LowOutProtectThresh:                  0.08,
		HighOutThresh:                        0.25,
		SurgeBumpMax:                         0.10,
		RunIntervalSec:                       8 * 3600,
		CooldownUpSec:                        8 * 3600,
		CooldownDownSec:                      2 * 3600,
		CooldownUpDrainedSec:                 4 * 3600,
		CooldownUpExtremeSec:                 2 * 3600,
		CooldownUpDrainedOutRatioMax:         0.05,
		CooldownUpExtremeOutRatioMax:         0.01,
		DiscoveryStepCapDown:                 0.15,
		StallFloorRelaxGapFrac:               0.15,
		SeedGuardMaxJump:                     0.30,
		OutratePegGraceHours:                 24,
		ProfitDownMarginMin:                  20,
		ProfitDownFwdsMin:                    10,
		ProfitDownExtraHours:                 4,
		NegMarginSurgeBump:                   0.06,
		NegMarginSurgeMinFwds:                6,
		NegMarginSurgeFwdsRatio:              0.25,
		SinkExtraFloorMargin:                 0.06,
		RevfloorBaselineThresh:               80,
		RevfloorMinAbs:                       160,
		RevfloorBaselineScale:                1.2,
		RevfloorMinAbsScale:                  1.1,
		DiscHarddropDaysNoBase:               8,
		DiscHarddropCapFrac:                  0.10,
		DiscHarddropCushion:                  15,
		DiscRequireExplorer:                  true,
		DiscAfterExplorerDays:                14,
		OutrateFloorFactorLow:                0.90,
		ExplorerSkipCooldownDown:             false,
		CircuitBreakerDropRatio:              0.75,
		CircuitBreakerReduceStep:             0.08,
		CircuitBreakerGraceDays:              10,
		ExtremeDrainStreak:                   32,
		ExtremeDrainOutMax:                   0.03,
		ExtremeDrainStepCap:                  0.10,
		ExtremeDrainMinStepPpm:               10,
		ExtremeDrainTurboStreak:              400,
		ExtremeDrainTurboOutMax:              0.01,
		ExtremeDrainTurboStepCap:             0.15,
		ExtremeDrainTurboMinStepPpm:          15,
		SinkMinMargin:                        180,
		MinSoftCeiling:                       150,
		SeedCeilingMult:                      1.40,
		SeedFloorMult:                        1.10,
		SeedP95Boost:                         1.10,
		SeedOutrateCapMult:                   1.45,
		SeedRebalCapMult:                     1.35,
		SourceSeedTargetFrac:                 0.50,
		ProfitProtectOutRatio:                0.08,
		ProfitProtectMarginPpm:               0,
		ProfitProtectRelaxHours:              96,
		ProfitProtectRelaxMaxFwds:            0,
		ProfitProtectRelaxMarginPpm:          -10,
		ProfitProtectRelaxStepFrac:           0.010,
		ProfitProtectRelaxMinStepPpm:         10,
		InboundDiscountReachOutRatio:         0.10,
		InboundDiscountMinRetainedSpreadFrac: 0.20,
		MarketRefillNetInboundTargetFrac:     0.35,
		MarketRefillCandidateOutRatioMax:     0.10,
		MarketRefillExploratoryOutRatioMax:   0.18,
		MarketRefillExploratoryFwds1dMax:     1,
		MarketRefillBaseTargetMult:           1.25,
		MarketRefillBaseTargetAddPpm:         60,
		MarketRefillLowTargetMult:            1.35,
		MarketRefillLowTargetAddPpm:          160,
		MarketRefillDrainedTargetMult:        1.90,
		MarketRefillDrainedTargetAddPpm:      350,
		MarketRefillLocalHoldFrac:            0.40,
		MarketRefillLocalCapMult:             1.25,
		MarketRefillMinOutboundPpm:           300,
		MarketRefillMinOutboundMaxPpmFrac:    0.10,
		MarketRefillOutrateFloorFrac:         1.00,
		GlobalNegLockSoften:                  true,
		SoftenMinOutRatio:                    0.25,
		SoftenRequirePosChanMargin:           true,
		SoftenMaxDropToPegFrac:               0.85,
		HTLCMinAttempts60m:                   20,
		HTLCPolicyFailRate:                   0.20,
		HTLCPolicyMinFails:                   4,
		HTLCLiquidityFailRate:                0.25,
		HTLCLiquidityMinFails:                4,
		HTLCLiquidityHotBump:                 0.03,
		HTLCLiquidityHotNoDownOutRatio:       0.08,
		HTLCPolicyHotBump:                    0.015,
		HTLCPolicyHotNoDownMarginPpm:         0,
		HTLCHotStepCapBoost:                  0.01,
		LargeGapStepCapBoostPctMin:           30,
		LargeGapStepCapBoost:                 0.02,
		LargeGapStepCapBoostPctStrong:        50,
		LargeGapStepCapBoostStrong:           0.04,
		SurgeHoldMaxRounds:                   7,
		SurgeHoldUnlockStepPpm:               15,
		BootstrapHours:                       48,
		BootstrapOutRatioMax:                 0.35,
		BootstrapCooldownUpSec:               3600,
		BootstrapCooldownDownSec:             7200,
		BootstrapMinStepUpPpm:                15,
		BootstrapSurgeHoldMaxRounds:          2,
		HoldSmallMinDeltaPpm:                 15,
		HoldSmallMinRelFrac:                  0.04,
		HoldSmallGapBypassPpm:                180,
		HoldSmallGapBypassFrac:               0.18,
		HoldSmallStallBypassRounds:           2,
		ReversalConfirmMinRounds:             2,
		ReversalFastTrackStallRounds:         3,
		ReversalFastTrackGapFrac:             0.50,
		AntiFlipWindowHours:                  6,
		AntiFlipExtraConfirmRounds:           2,
		AntiFlipStrongGapFrac:                0.60,
		DrainedExplorerAfterDays:             10,
		DrainedExplorerOutRatioMax:           0.03,
		DrainedExplorerRecentFwds1dMax:       1,
		DrainedExplorerMaxHours:              48,
		DrainedExplorerMaxRounds:             3,
		DrainedExplorerStepPpm:               5,
		BalancedUpOutRatioMin:                0.25,
		BalancedFloorUpCap:                   0.04,
		OutrateTargetOutRatioMin:             0.25,
		OutrateTargetMinFwds:                 8,
		OutrateTargetFloorFrac:               0.70,
		OutrateTargetBlendFrac:               0.35,
		RebalFailOutRatioMax:                 0.08,
		RebalFailNoDownMinAttempts:           4,
		RebalFailUpMinAttempts:               6,
		RebalFailUpStepFrac:                  0.02,
		RebalFailUpMinStepPpm:                10,
		RebalFailWindowHours:                 8,
		RebalFailCampaignGapMin:              120,
	},
	"moderate": {
		Name:                                 "moderate",
		StepCap:                              0.08,
		LowOutThresh:                         0.10,
		LowOutProtectThresh:                  0.10,
		HighOutThresh:                        0.20,
		SurgeBumpMax:                         0.20,
		RunIntervalSec:                       4 * 3600,
		CooldownUpSec:                        6 * 3600,
		CooldownDownSec:                      1 * 3600,
		CooldownUpDrainedSec:                 3 * 3600,
		CooldownUpExtremeSec:                 1 * 3600,
		CooldownUpDrainedOutRatioMax:         0.05,
		CooldownUpExtremeOutRatioMax:         0.01,
		DiscoveryStepCapDown:                 0.20,
		StallFloorRelaxGapFrac:               0.10,
		SeedGuardMaxJump:                     0.50,
		OutratePegGraceHours:                 16,
		ProfitDownMarginMin:                  10,
		ProfitDownFwdsMin:                    8,
		ProfitDownExtraHours:                 2,
		NegMarginSurgeBump:                   0.08,
		NegMarginSurgeMinFwds:                4,
		NegMarginSurgeFwdsRatio:              0.20,
		SinkExtraFloorMargin:                 0.04,
		RevfloorBaselineThresh:               60,
		RevfloorMinAbs:                       140,
		RevfloorBaselineScale:                1.0,
		RevfloorMinAbsScale:                  1.0,
		DiscHarddropDaysNoBase:               6,
		DiscHarddropCapFrac:                  0.20,
		DiscHarddropCushion:                  10,
		DiscRequireExplorer:                  true,
		DiscAfterExplorerDays:                10,
		OutrateFloorFactorLow:                0.85,
		ExplorerSkipCooldownDown:             true,
		CircuitBreakerDropRatio:              0.70,
		CircuitBreakerReduceStep:             0.10,
		CircuitBreakerGraceDays:              7,
		ExtremeDrainStreak:                   24,
		ExtremeDrainOutMax:                   0.04,
		ExtremeDrainStepCap:                  0.12,
		ExtremeDrainMinStepPpm:               12,
		ExtremeDrainTurboStreak:              300,
		ExtremeDrainTurboOutMax:              0.01,
		ExtremeDrainTurboStepCap:             0.20,
		ExtremeDrainTurboMinStepPpm:          20,
		SinkMinMargin:                        150,
		MinSoftCeiling:                       100,
		SeedCeilingMult:                      1.50,
		SeedFloorMult:                        1.10,
		SeedP95Boost:                         1.15,
		SeedOutrateCapMult:                   1.20,
		SeedRebalCapMult:                     1.10,
		SourceSeedTargetFrac:                 0.55,
		ProfitProtectOutRatio:                0.10,
		ProfitProtectMarginPpm:               0,
		ProfitProtectRelaxHours:              72,
		ProfitProtectRelaxMaxFwds:            1,
		ProfitProtectRelaxMarginPpm:          -30,
		ProfitProtectRelaxStepFrac:           0.015,
		ProfitProtectRelaxMinStepPpm:         15,
		InboundDiscountReachOutRatio:         0.15,
		InboundDiscountMinRetainedSpreadFrac: 0.12,
		MarketRefillNetInboundTargetFrac:     0.20,
		MarketRefillCandidateOutRatioMax:     0.15,
		MarketRefillExploratoryOutRatioMax:   0.25,
		MarketRefillExploratoryFwds1dMax:     2,
		MarketRefillBaseTargetMult:           1.15,
		MarketRefillBaseTargetAddPpm:         100,
		MarketRefillLowTargetMult:            1.50,
		MarketRefillLowTargetAddPpm:          250,
		MarketRefillDrainedTargetMult:        2.20,
		MarketRefillDrainedTargetAddPpm:      600,
		MarketRefillLocalHoldFrac:            0.35,
		MarketRefillLocalCapMult:             1.40,
		MarketRefillMinOutboundPpm:           500,
		MarketRefillMinOutboundMaxPpmFrac:    0.15,
		MarketRefillOutrateFloorFrac:         1.00,
		GlobalNegLockSoften:                  true,
		SoftenMinOutRatio:                    0.20,
		SoftenRequirePosChanMargin:           true,
		SoftenMaxDropToPegFrac:               0.75,
		HTLCMinAttempts60m:                   12,
		HTLCPolicyFailRate:                   0.15,
		HTLCPolicyMinFails:                   3,
		HTLCLiquidityFailRate:                0.16,
		HTLCLiquidityMinFails:                2,
		HTLCLiquidityHotBump:                 0.05,
		HTLCLiquidityHotNoDownOutRatio:       0.12,
		HTLCPolicyHotBump:                    0.025,
		HTLCPolicyHotNoDownMarginPpm:         25,
		HTLCHotStepCapBoost:                  0.02,
		LargeGapStepCapBoostPctMin:           25,
		LargeGapStepCapBoost:                 0.03,
		LargeGapStepCapBoostPctStrong:        45,
		LargeGapStepCapBoostStrong:           0.06,
		SurgeHoldMaxRounds:                   5,
		SurgeHoldUnlockStepPpm:               15,
		BootstrapHours:                       48,
		BootstrapOutRatioMax:                 0.40,
		BootstrapCooldownUpSec:               3600,
		BootstrapCooldownDownSec:             5400,
		BootstrapMinStepUpPpm:                15,
		BootstrapSurgeHoldMaxRounds:          2,
		HoldSmallMinDeltaPpm:                 15,
		HoldSmallMinRelFrac:                  0.04,
		HoldSmallGapBypassPpm:                120,
		HoldSmallGapBypassFrac:               0.12,
		HoldSmallStallBypassRounds:           1,
		ReversalConfirmMinRounds:             2,
		ReversalFastTrackStallRounds:         2,
		ReversalFastTrackGapFrac:             0.35,
		AntiFlipWindowHours:                  4,
		AntiFlipExtraConfirmRounds:           1,
		AntiFlipStrongGapFrac:                0.45,
		DrainedExplorerAfterDays:             7,
		DrainedExplorerOutRatioMax:           0.04,
		DrainedExplorerRecentFwds1dMax:       2,
		DrainedExplorerMaxHours:              48,
		DrainedExplorerMaxRounds:             4,
		DrainedExplorerStepPpm:               10,
		BalancedUpOutRatioMin:                0.20,
		BalancedFloorUpCap:                   0.05,
		OutrateTargetOutRatioMin:             0.20,
		OutrateTargetMinFwds:                 6,
		OutrateTargetFloorFrac:               0.80,
		OutrateTargetBlendFrac:               0.55,
		RebalFailOutRatioMax:                 0.10,
		RebalFailNoDownMinAttempts:           3,
		RebalFailUpMinAttempts:               5,
		RebalFailUpStepFrac:                  0.03,
		RebalFailUpMinStepPpm:                12,
		RebalFailWindowHours:                 6,
		RebalFailCampaignGapMin:              90,
	},
	"aggressive": {
		Name:                                 "aggressive",
		StepCap:                              0.10,
		LowOutThresh:                         0.12,
		LowOutProtectThresh:                  0.12,
		HighOutThresh:                        0.18,
		SurgeBumpMax:                         0.30,
		RunIntervalSec:                       2 * 3600,
		CooldownUpSec:                        3 * 3600,
		CooldownDownSec:                      1 * 3600,
		CooldownUpDrainedSec:                 90 * 60,
		CooldownUpExtremeSec:                 60 * 60,
		CooldownUpDrainedOutRatioMax:         0.05,
		CooldownUpExtremeOutRatioMax:         0.01,
		DiscoveryStepCapDown:                 0.25,
		StallFloorRelaxGapFrac:               0.08,
		SeedGuardMaxJump:                     0.70,
		OutratePegGraceHours:                 8,
		ProfitDownMarginMin:                  5,
		ProfitDownFwdsMin:                    5,
		ProfitDownExtraHours:                 1,
		NegMarginSurgeBump:                   0.12,
		NegMarginSurgeMinFwds:                3,
		NegMarginSurgeFwdsRatio:              0.15,
		SinkExtraFloorMargin:                 0.03,
		RevfloorBaselineThresh:               40,
		RevfloorMinAbs:                       120,
		RevfloorBaselineScale:                0.8,
		RevfloorMinAbsScale:                  0.9,
		DiscHarddropDaysNoBase:               3,
		DiscHarddropCapFrac:                  0.25,
		DiscHarddropCushion:                  5,
		DiscRequireExplorer:                  false,
		DiscAfterExplorerDays:                5,
		OutrateFloorFactorLow:                0.80,
		ExplorerSkipCooldownDown:             true,
		CircuitBreakerDropRatio:              0.60,
		CircuitBreakerReduceStep:             0.15,
		CircuitBreakerGraceDays:              5,
		ExtremeDrainStreak:                   16,
		ExtremeDrainOutMax:                   0.05,
		ExtremeDrainStepCap:                  0.15,
		ExtremeDrainMinStepPpm:               15,
		ExtremeDrainTurboStreak:              300,
		ExtremeDrainTurboOutMax:              0.01,
		ExtremeDrainTurboStepCap:             0.20,
		ExtremeDrainTurboMinStepPpm:          20,
		SinkMinMargin:                        120,
		MinSoftCeiling:                       80,
		SeedCeilingMult:                      1.60,
		SeedFloorMult:                        1.10,
		SeedP95Boost:                         1.20,
		SeedOutrateCapMult:                   1.25,
		SeedRebalCapMult:                     1.20,
		SourceSeedTargetFrac:                 0.60,
		ProfitProtectOutRatio:                0.12,
		ProfitProtectMarginPpm:               -20,
		ProfitProtectRelaxHours:              48,
		ProfitProtectRelaxMaxFwds:            2,
		ProfitProtectRelaxMarginPpm:          -50,
		ProfitProtectRelaxStepFrac:           0.020,
		ProfitProtectRelaxMinStepPpm:         20,
		InboundDiscountReachOutRatio:         0.18,
		InboundDiscountMinRetainedSpreadFrac: 0.08,
		MarketRefillNetInboundTargetFrac:     0.10,
		MarketRefillCandidateOutRatioMax:     0.20,
		MarketRefillExploratoryOutRatioMax:   0.30,
		MarketRefillExploratoryFwds1dMax:     2,
		MarketRefillBaseTargetMult:           1.20,
		MarketRefillBaseTargetAddPpm:         150,
		MarketRefillLowTargetMult:            1.75,
		MarketRefillLowTargetAddPpm:          400,
		MarketRefillDrainedTargetMult:        2.60,
		MarketRefillDrainedTargetAddPpm:      900,
		MarketRefillLocalHoldFrac:            0.30,
		MarketRefillLocalCapMult:             1.60,
		MarketRefillMinOutboundPpm:           800,
		MarketRefillMinOutboundMaxPpmFrac:    0.20,
		MarketRefillOutrateFloorFrac:         1.00,
		GlobalNegLockSoften:                  true,
		SoftenMinOutRatio:                    0.15,
		SoftenRequirePosChanMargin:           false,
		SoftenMaxDropToPegFrac:               0.70,
		HTLCMinAttempts60m:                   8,
		HTLCPolicyFailRate:                   0.10,
		HTLCPolicyMinFails:                   2,
		HTLCLiquidityFailRate:                0.15,
		HTLCLiquidityMinFails:                2,
		HTLCLiquidityHotBump:                 0.07,
		HTLCLiquidityHotNoDownOutRatio:       0.18,
		HTLCPolicyHotBump:                    0.035,
		HTLCPolicyHotNoDownMarginPpm:         50,
		HTLCHotStepCapBoost:                  0.03,
		LargeGapStepCapBoostPctMin:           20,
		LargeGapStepCapBoost:                 0.05,
		LargeGapStepCapBoostPctStrong:        40,
		LargeGapStepCapBoostStrong:           0.10,
		SurgeHoldMaxRounds:                   4,
		SurgeHoldUnlockStepPpm:               20,
		BootstrapHours:                       72,
		BootstrapOutRatioMax:                 0.45,
		BootstrapCooldownUpSec:               1800,
		BootstrapCooldownDownSec:             3600,
		BootstrapMinStepUpPpm:                20,
		BootstrapSurgeHoldMaxRounds:          1,
		HoldSmallMinDeltaPpm:                 12,
		HoldSmallMinRelFrac:                  0.03,
		HoldSmallGapBypassPpm:                80,
		HoldSmallGapBypassFrac:               0.08,
		HoldSmallStallBypassRounds:           1,
		ReversalConfirmMinRounds:             2,
		ReversalFastTrackStallRounds:         1,
		ReversalFastTrackGapFrac:             0.20,
		AntiFlipWindowHours:                  3,
		AntiFlipExtraConfirmRounds:           1,
		AntiFlipStrongGapFrac:                0.30,
		DrainedExplorerAfterDays:             5,
		DrainedExplorerOutRatioMax:           0.05,
		DrainedExplorerRecentFwds1dMax:       2,
		DrainedExplorerMaxHours:              48,
		DrainedExplorerMaxRounds:             5,
		DrainedExplorerStepPpm:               15,
		BalancedUpOutRatioMin:                0.18,
		BalancedFloorUpCap:                   0.06,
		OutrateTargetOutRatioMin:             0.18,
		OutrateTargetMinFwds:                 4,
		OutrateTargetFloorFrac:               0.90,
		OutrateTargetBlendFrac:               0.70,
		RebalFailOutRatioMax:                 0.12,
		RebalFailNoDownMinAttempts:           2,
		RebalFailUpMinAttempts:               4,
		RebalFailUpStepFrac:                  0.04,
		RebalFailUpMinStepPpm:                15,
		RebalFailWindowHours:                 4,
		RebalFailCampaignGapMin:              60,
	},
}

const (
	outratePegHeadroom           = 1.05
	outratePegSeedMult           = 1.10
	outratePegRebalSpreadMinFrac = 0.20
	outrateFloorFactor           = 1.00
	outrateFloorMinFwds          = 4
	outrateFloorDisableBelowFwds = 5
	outrateFloorLowFwds          = 10
)

const (
	// Global HTLC sensitivity knobs applied after profile/node/liquidity calibration.
	// Keep them shared across all profiles so behavior stays consistent network-wide.
	htlcGlobalMinCountFactor        = 0.55
	htlcGlobalRateFactor            = 0.75
	htlcGlobalMinAttemptsFloor      = 4
	htlcGlobalMinFailsFloor         = 2
	htlcGlobalPolicyRateFloor       = 0.08
	htlcGlobalLiquidityRateFloor    = 0.10
	htlcForwardSoftCountFactor      = 0.45
	htlcForwardSoftRateFactor       = 0.50
	htlcForwardSoftRateFloor        = 0.10
	htlcForwardSoftMinFailsFloor    = 2
	htlcClassifiedMinRatio          = 0.05
	htlcNoisySampleForwardRateMult  = 1.5
	htlcNoisySampleForwardCountMult = 1.5
)

var htlcPolicyFailureTokens = []string{
	"FEE INSUFFICIENT",
	"INCORRECT CLTV EXPIRY",
	"AMOUNT BELOW MINIMUM",
	"EXPIRY TOO SOON",
	"EXPIRY TOO FAR",
	"FINAL INCORRECT CLTV EXPIRY",
	"FINAL INCORRECT HTLC AMOUNT",
	"REQUIRED NODE FEATURE MISSING",
	"REQUIRED CHANNEL FEATURE MISSING",
	"INVALID ONION VERSION",
	"INVALID ONION HMAC",
	"INVALID ONION KEY",
	"INVALID ONION PAYLOAD",
}

var htlcLiquidityFailureTokens = []string{
	"TEMPORARY CHANNEL FAILURE",
	"TEMPORARY NODE FAILURE",
	"CHANNEL DISABLED",
	"UNKNOWN NEXT PEER",
	"PERMANENT CHANNEL FAILURE",
	"INSUFFICIENT BALANCE",
	"NO ROUTE",
}

var superSourceThresholdsByProfile = map[string]superSourceThresholds{
	"conservative": {
		OutRatioMin:  0.65,
		OutAmt1dMult: 0.70,
		OutAmt7dMult: 5.0,
		MinFwds7d:    15,
		EnterHours:   6,
		ExitHours:    96,
	},
	"moderate": {
		OutRatioMin:  0.60,
		OutAmt1dMult: 0.50,
		OutAmt7dMult: 4.0,
		MinFwds7d:    10,
		EnterHours:   0,
		ExitHours:    72,
	},
	"aggressive": {
		OutRatioMin:  0.55,
		OutAmt1dMult: 0.35,
		OutAmt7dMult: 3.0,
		MinFwds7d:    7,
		EnterHours:   0,
		ExitHours:    48,
	},
}

type AutofeeService struct {
	db                 *pgxpool.Pool
	lnd                *lndclient.Client
	notifier           *Notifier
	rebalance          *RebalanceService
	htlcFailedProvider htlcFailedProvider
	logger             loggerLike

	mu        sync.Mutex
	started   bool
	running   bool
	stop      chan struct{}
	wake      chan struct{}
	lastRunAt time.Time
	nextRunAt time.Time
	lastError string
}

type loggerLike interface {
	Printf(format string, v ...any)
}

type htlcFailedProvider interface {
	Failed(limit int) []htlcManagerFailedEntry
}

type autofeeLogEntry struct {
	Line    string
	Payload *autofeeLogItem
}

func NewAutofeeService(db *pgxpool.Pool, lnd *lndclient.Client, notifier *Notifier, htlcProvider htlcFailedProvider, logger loggerLike) *AutofeeService {
	return &AutofeeService{
		db:                 db,
		lnd:                lnd,
		notifier:           notifier,
		htlcFailedProvider: htlcProvider,
		logger:             logger,
	}
}

func (s *AutofeeService) lastRunFromLogs(ctx context.Context) (time.Time, bool) {
	var ts pgtype.Timestamptz
	err := s.db.QueryRow(ctx, `
select max(occurred_at)
from autofee_logs
where seq = 0
  and coalesce(payload->>'dry_run', 'false') <> 'true'
  and coalesce(payload->>'reason', '') <> 'refresh'
`).Scan(&ts)
	if err != nil || !ts.Valid {
		return time.Time{}, false
	}
	return ts.Time, true
}
func (s *AutofeeService) EnsureSchema(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db not configured")
	}

	_, err := s.db.Exec(ctx, `
create table if not exists autofee_config (
  id integer primary key,
  enabled boolean not null default false,
  operation_mode text not null default 'balanced',
  profile text not null default 'moderate',
  lookback_days integer not null default 7,
  run_interval_sec integer not null default 14400,
  cooldown_up_sec integer not null default 10800,
  cooldown_down_sec integer not null default 14400,
  step_cap_override double precision not null default 0,
  discovery_step_cap_down_override double precision not null default 0,
  stall_floor_relax_gap_frac_override double precision not null default 0,
  inbound_discount_max_ratio_override double precision not null default 0,
  outrate_floor_factor_low_override double precision not null default 0,
  soften_min_out_ratio_override double precision not null default 0,
  soften_max_drop_to_peg_frac_override double precision not null default 0,
  htlc_min_attempts_60m_override integer not null default 0,
  htlc_policy_fail_rate_override double precision not null default 0,
  htlc_liquidity_fail_rate_override double precision not null default 0,
  rebal_cost_mode text not null default 'blend',
  native_seed_enabled boolean not null default false,
  amboss_enabled boolean not null default false,
  amboss_token text,
  inbound_passive_enabled boolean not null default false,
  discovery_enabled boolean not null default true,
  explorer_enabled boolean not null default true,
  idle_refresh_enabled boolean not null default false,
  super_source_enabled boolean not null default false,
  super_source_base_fee_msat integer not null default 1000,
  revfloor_enabled boolean not null default true,
  circuit_breaker_enabled boolean not null default true,
  extreme_drain_enabled boolean not null default true,
  htlc_signal_enabled boolean not null default true,
  htlc_mode text not null default 'full',
  min_ppm integer not null default 10,
  max_ppm integer not null default 2000,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists autofee_channel_settings (
  channel_id bigint primary key,
  channel_point text not null,
  enabled boolean not null default true,
  updated_at timestamptz not null default now()
);

create table if not exists autofee_state (
  channel_id bigint primary key,
  last_ppm integer,
  last_inbound_discount_ppm integer,
  last_seed_ppm integer,
  seed_shock_pending_ppm integer,
  seed_shock_rounds integer not null default 0,
  seed_shock_dir text,
  last_outrate_ppm integer,
  last_outrate_ts timestamptz,
  last_rebal_cost_ppm integer,
  last_rebal_cost_ts timestamptz,
  last_idle_refresh_ts timestamptz,
  last_idle_refresh_ppm integer,
  last_idle_refresh_source text,
  last_ts timestamptz,
  last_dir text,
  low_streak integer not null default 0,
  stalled_rounds integer not null default 0,
  baseline_fwd7d integer not null default 0,
  class_label text,
  class_conf real,
  bias_ema real,
  first_seen_ts timestamptz,
  ss_active boolean,
  ss_ok_since timestamptz,
  ss_bad_since timestamptz,
  explorer_state jsonb
);

create index if not exists autofee_channel_settings_enabled_idx on autofee_channel_settings (enabled);

create table if not exists autofee_logs (
  id bigserial primary key,
  occurred_at timestamptz not null default now(),
  run_id text,
  seq integer,
  line text not null,
  payload jsonb
);
create index if not exists autofee_logs_occurred_at_idx on autofee_logs (occurred_at desc);
create index if not exists autofee_logs_run_idx on autofee_logs (run_id, seq);

create table if not exists autofee_market_refill_fee_snapshot (
  channel_id bigint primary key,
  channel_point text not null,
  base_fee_msat bigint not null default 0,
  fee_rate_ppm bigint not null default 0,
  time_lock_delta bigint not null default 0,
  inbound_enabled boolean not null default false,
  inbound_base_msat bigint not null default 0,
  inbound_fee_rate_ppm bigint not null default 0,
  captured_at timestamptz not null default now()
);
create index if not exists autofee_market_refill_fee_snapshot_captured_idx on autofee_market_refill_fee_snapshot (captured_at desc);

create table if not exists autofee_outcomes (
  id bigserial primary key,
  run_id text not null,
  channel_id bigint not null,
  channel_point text not null,
  kind text not null,
  decided_at timestamptz not null,
  prev_ppm integer not null,
  new_ppm integer not null,
  target_ppm integer,
  floor_ppm integer,
  floor_src text,
  class_label text,
  tags jsonb not null default '[]'::jsonb,
  out_ratio_pre real,
  out_ppm7d_pre integer,
  margin_ppm7d_pre integer,
  fwd_count_7d_pre integer,
  measured_at timestamptz,
  fwd_count_24h_after integer,
  fwd_amt_msat_24h_after bigint,
  fwd_fee_msat_24h_after bigint,
  rebal_amt_msat_24h_after bigint,
  rebal_fee_msat_24h_after bigint,
  measurement_status text not null default 'pending',
  measurement_error text,
  unique (run_id, channel_id, kind)
);
create index if not exists autofee_outcomes_status_idx on autofee_outcomes (measurement_status, decided_at);
create index if not exists autofee_outcomes_channel_idx on autofee_outcomes (channel_id, decided_at desc);
create index if not exists autofee_outcomes_decided_at_idx on autofee_outcomes (decided_at desc);

alter table autofee_config add column if not exists super_source_enabled boolean not null default false;
alter table autofee_config add column if not exists operation_mode text not null default 'balanced';
alter table autofee_config add column if not exists market_refill_rebalance_prev_saved boolean not null default false;
alter table autofee_config add column if not exists market_refill_rebalance_prev_auto boolean not null default false;
alter table autofee_config add column if not exists market_refill_rebalance_prev_manual_restart boolean not null default false;
alter table autofee_config add column if not exists super_source_base_fee_msat integer not null default 1000;
alter table autofee_config add column if not exists revfloor_enabled boolean not null default true;
alter table autofee_config add column if not exists circuit_breaker_enabled boolean not null default true;
alter table autofee_config add column if not exists extreme_drain_enabled boolean not null default true;
alter table autofee_config add column if not exists htlc_signal_enabled boolean not null default true;
alter table autofee_config add column if not exists htlc_mode text not null default 'full';
alter table autofee_config add column if not exists rebal_cost_mode text not null default 'blend';
alter table autofee_config add column if not exists step_cap_override double precision not null default 0;
alter table autofee_config add column if not exists discovery_step_cap_down_override double precision not null default 0;
alter table autofee_config add column if not exists stall_floor_relax_gap_frac_override double precision not null default 0;
alter table autofee_config add column if not exists inbound_discount_max_ratio_override double precision not null default 0;
alter table autofee_config add column if not exists inbound_discount_reach_out_ratio_override double precision not null default 0;
alter table autofee_config add column if not exists inbound_discount_min_retained_spread_frac_override double precision not null default 0;
alter table autofee_config add column if not exists outrate_floor_factor_low_override double precision not null default 0;
alter table autofee_config add column if not exists soften_min_out_ratio_override double precision not null default 0;
alter table autofee_config add column if not exists soften_max_drop_to_peg_frac_override double precision not null default 0;
alter table autofee_config add column if not exists htlc_min_attempts_60m_override integer not null default 0;
alter table autofee_config add column if not exists htlc_policy_fail_rate_override double precision not null default 0;
alter table autofee_config add column if not exists htlc_liquidity_fail_rate_override double precision not null default 0;
alter table autofee_config add column if not exists native_seed_enabled boolean not null default false;
alter table autofee_config add column if not exists idle_refresh_enabled boolean not null default false;
alter table autofee_state add column if not exists ss_active boolean;
alter table autofee_state add column if not exists ss_ok_since timestamptz;
alter table autofee_state add column if not exists ss_bad_since timestamptz;
alter table autofee_state add column if not exists last_dir text;
alter table autofee_state add column if not exists stalled_rounds integer not null default 0;
alter table autofee_state add column if not exists apply_error_streak integer not null default 0;
alter table autofee_state add column if not exists last_apply_error text;
alter table autofee_state add column if not exists apply_error_alerted boolean not null default false;
alter table autofee_state add column if not exists last_idle_refresh_ts timestamptz;
alter table autofee_state add column if not exists last_idle_refresh_ppm integer;
alter table autofee_state add column if not exists last_idle_refresh_source text;
alter table autofee_state add column if not exists seed_shock_pending_ppm integer;
alter table autofee_state add column if not exists seed_shock_rounds integer not null default 0;
alter table autofee_state add column if not exists seed_shock_dir text;
alter table autofee_logs add column if not exists payload jsonb;
`)
	if err != nil {
		return err
	}

	// On new installs, enable native seed by default. Existing rows are not
	// touched (on conflict do nothing) — users who already configured the
	// service keep whatever they had.
	_, err = s.db.Exec(ctx, `
insert into autofee_config (id, native_seed_enabled)
values ($1, true)
on conflict (id) do nothing
`, autofeeConfigID)
	return err
}

func (s *AutofeeService) defaultConfig() AutofeeConfig {
	p := autofeeProfiles["moderate"]
	return AutofeeConfig{
		Enabled:                         false,
		OperationMode:                   autofeeOperationModeDefault,
		Profile:                         p.Name,
		LookbackDays:                    7,
		RunIntervalSec:                  p.RunIntervalSec,
		CooldownUpSec:                   p.CooldownUpSec,
		CooldownDownSec:                 p.CooldownDownSec,
		InboundDiscountMaxRatioOverride: 0,
		RebalCostMode:                   rebalCostModeDefault,
		NativeSeedEnabled:               true,
		AmbossEnabled:                   false,
		AmbossTokenSet:                  false,
		InboundPassiveEnabled:           false,
		DiscoveryEnabled:                true,
		ExplorerEnabled:                 true,
		IdleRefreshEnabled:              false,
		SuperSourceEnabled:              false,
		SuperSourceBaseFeeMsat:          superSourceBaseFeeMsatDefault,
		RevfloorEnabled:                 true,
		CircuitBreakerEnabled:           true,
		ExtremeDrainEnabled:             true,
		HTLCSignalEnabled:               true,
		HTLCMode:                        htlcModeDefault,
		MinPpm:                          10,
		MaxPpm:                          2000,
	}
}

func autofeeProfileDefaultsPayload() map[string]AutofeeProfileDefaults {
	defaults := make(map[string]AutofeeProfileDefaults, len(autofeeProfiles))
	for name, p := range autofeeProfiles {
		defaults[name] = AutofeeProfileDefaults{
			RunIntervalSec:                       p.RunIntervalSec,
			CooldownUpSec:                        p.CooldownUpSec,
			CooldownDownSec:                      p.CooldownDownSec,
			StepCap:                              p.StepCap,
			DiscoveryStepCapDown:                 p.DiscoveryStepCapDown,
			StallFloorRelaxGapFrac:               p.StallFloorRelaxGapFrac,
			InboundDiscountMaxRatio:              defaultInboundDiscountMaxRatio,
			InboundDiscountReachOutRatio:         p.InboundDiscountReachOutRatio,
			InboundDiscountMinRetainedSpreadFrac: p.InboundDiscountMinRetainedSpreadFrac,
			OutrateFloorFactorLow:                p.OutrateFloorFactorLow,
			SoftenMinOutRatio:                    p.SoftenMinOutRatio,
			SoftenMaxDropToPegFrac:               p.SoftenMaxDropToPegFrac,
			HTLCMinAttempts60m:                   p.HTLCMinAttempts60m,
			HTLCPolicyFailRate:                   p.HTLCPolicyFailRate,
			HTLCLiquidityFailRate:                p.HTLCLiquidityFailRate,
		}
	}
	return defaults
}

func autofeeConfigWithProfileDefaults(cfg AutofeeConfig) AutofeeConfig {
	cfg.ProfileDefaults = autofeeProfileDefaultsPayload()
	return cfg
}

func (s *AutofeeService) GetConfig(ctx context.Context) (AutofeeConfig, error) {
	cfg := s.defaultConfig()
	if s.db == nil {
		return autofeeConfigWithProfileDefaults(cfg), errors.New("db unavailable")
	}

	var ambossToken pgtype.Text
	err := s.db.QueryRow(ctx, `
	select enabled, operation_mode, profile, lookback_days, run_interval_sec, cooldown_up_sec, cooldown_down_sec,
  step_cap_override, discovery_step_cap_down_override, stall_floor_relax_gap_frac_override, inbound_discount_max_ratio_override,
  inbound_discount_reach_out_ratio_override, inbound_discount_min_retained_spread_frac_override, outrate_floor_factor_low_override,
  soften_min_out_ratio_override, soften_max_drop_to_peg_frac_override, htlc_min_attempts_60m_override,
  htlc_policy_fail_rate_override, htlc_liquidity_fail_rate_override,
  rebal_cost_mode, native_seed_enabled, amboss_enabled, amboss_token, inbound_passive_enabled, discovery_enabled, explorer_enabled, idle_refresh_enabled,
  super_source_enabled, super_source_base_fee_msat, revfloor_enabled, circuit_breaker_enabled, extreme_drain_enabled,
  htlc_signal_enabled, htlc_mode, min_ppm, max_ppm
from autofee_config where id=$1
`, autofeeConfigID).Scan(
		&cfg.Enabled,
		&cfg.OperationMode,
		&cfg.Profile,
		&cfg.LookbackDays,
		&cfg.RunIntervalSec,
		&cfg.CooldownUpSec,
		&cfg.CooldownDownSec,
		&cfg.StepCapOverride,
		&cfg.DiscoveryStepCapDownOverride,
		&cfg.StallFloorRelaxGapFracOverride,
		&cfg.InboundDiscountMaxRatioOverride,
		&cfg.InboundDiscountReachOutRatioOverride,
		&cfg.InboundDiscountMinRetainedSpreadFracOverride,
		&cfg.OutrateFloorFactorLowOverride,
		&cfg.SoftenMinOutRatioOverride,
		&cfg.SoftenMaxDropToPegFracOverride,
		&cfg.HTLCMinAttempts60mOverride,
		&cfg.HTLCPolicyFailRateOverride,
		&cfg.HTLCLiquidityFailRateOverride,
		&cfg.RebalCostMode,
		&cfg.NativeSeedEnabled,
		&cfg.AmbossEnabled,
		&ambossToken,
		&cfg.InboundPassiveEnabled,
		&cfg.DiscoveryEnabled,
		&cfg.ExplorerEnabled,
		&cfg.IdleRefreshEnabled,
		&cfg.SuperSourceEnabled,
		&cfg.SuperSourceBaseFeeMsat,
		&cfg.RevfloorEnabled,
		&cfg.CircuitBreakerEnabled,
		&cfg.ExtremeDrainEnabled,
		&cfg.HTLCSignalEnabled,
		&cfg.HTLCMode,
		&cfg.MinPpm,
		&cfg.MaxPpm,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return autofeeConfigWithProfileDefaults(cfg), nil
		}
		return autofeeConfigWithProfileDefaults(cfg), err
	}
	cfg.AmbossTokenSet = ambossToken.Valid && strings.TrimSpace(ambossToken.String) != ""
	if cfg.Profile == "" {
		cfg.Profile = "moderate"
	}
	cfg.OperationMode = normalizeAutofeeOperationMode(cfg.OperationMode)
	cfg.RebalCostMode = normalizeRebalCostMode(cfg.RebalCostMode)
	cfg.HTLCMode = normalizeHTLCMode(cfg.HTLCMode)
	if cfg.LookbackDays < autofeeMinLookbackDays {
		cfg.LookbackDays = autofeeMinLookbackDays
	}
	if cfg.LookbackDays > autofeeMaxLookbackDays {
		cfg.LookbackDays = autofeeMaxLookbackDays
	}
	if cfg.MinPpm <= 0 {
		cfg.MinPpm = 0
	}
	if cfg.MaxPpm <= 0 {
		cfg.MaxPpm = 2000
	}
	return autofeeConfigWithProfileDefaults(cfg), nil
}

func (s *AutofeeService) UpdateConfig(ctx context.Context, req AutofeeConfigUpdate) (AutofeeConfig, error) {
	current, err := s.GetConfig(ctx)
	if err != nil {
		return current, err
	}
	previousRebalMode := current.RebalCostMode

	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.OperationMode != nil {
		current.OperationMode = normalizeAutofeeOperationMode(*req.OperationMode)
	}
	if req.Profile != nil && strings.TrimSpace(*req.Profile) != "" {
		current.Profile = strings.ToLower(strings.TrimSpace(*req.Profile))
		if _, ok := autofeeProfiles[current.Profile]; !ok {
			current.Profile = "moderate"
		}
	}
	if req.LookbackDays != nil {
		current.LookbackDays = *req.LookbackDays
	}
	if req.RunIntervalSec != nil {
		current.RunIntervalSec = *req.RunIntervalSec
	}
	if req.CooldownUpSec != nil {
		current.CooldownUpSec = *req.CooldownUpSec
	}
	if req.CooldownDownSec != nil {
		current.CooldownDownSec = *req.CooldownDownSec
	}
	if req.StepCapOverride != nil {
		current.StepCapOverride = *req.StepCapOverride
	}
	if req.DiscoveryStepCapDownOverride != nil {
		current.DiscoveryStepCapDownOverride = *req.DiscoveryStepCapDownOverride
	}
	if req.StallFloorRelaxGapFracOverride != nil {
		current.StallFloorRelaxGapFracOverride = *req.StallFloorRelaxGapFracOverride
	}
	if req.InboundDiscountMaxRatioOverride != nil {
		current.InboundDiscountMaxRatioOverride = *req.InboundDiscountMaxRatioOverride
	}
	if req.InboundDiscountReachOutRatioOverride != nil {
		current.InboundDiscountReachOutRatioOverride = *req.InboundDiscountReachOutRatioOverride
	}
	if req.InboundDiscountMinRetainedSpreadFracOverride != nil {
		current.InboundDiscountMinRetainedSpreadFracOverride = *req.InboundDiscountMinRetainedSpreadFracOverride
	}
	if req.OutrateFloorFactorLowOverride != nil {
		current.OutrateFloorFactorLowOverride = *req.OutrateFloorFactorLowOverride
	}
	if req.SoftenMinOutRatioOverride != nil {
		current.SoftenMinOutRatioOverride = *req.SoftenMinOutRatioOverride
	}
	if req.SoftenMaxDropToPegFracOverride != nil {
		current.SoftenMaxDropToPegFracOverride = *req.SoftenMaxDropToPegFracOverride
	}
	if req.HTLCMinAttempts60mOverride != nil {
		current.HTLCMinAttempts60mOverride = *req.HTLCMinAttempts60mOverride
	}
	if req.HTLCPolicyFailRateOverride != nil {
		current.HTLCPolicyFailRateOverride = *req.HTLCPolicyFailRateOverride
	}
	if req.HTLCLiquidityFailRateOverride != nil {
		current.HTLCLiquidityFailRateOverride = *req.HTLCLiquidityFailRateOverride
	}
	if req.RebalCostMode != nil {
		current.RebalCostMode = normalizeRebalCostMode(*req.RebalCostMode)
	}
	if req.NativeSeedEnabled != nil {
		current.NativeSeedEnabled = *req.NativeSeedEnabled
	}
	if req.AmbossEnabled != nil {
		current.AmbossEnabled = *req.AmbossEnabled
	}
	if req.InboundPassiveEnabled != nil {
		current.InboundPassiveEnabled = *req.InboundPassiveEnabled
	}
	if req.DiscoveryEnabled != nil {
		current.DiscoveryEnabled = *req.DiscoveryEnabled
	}
	if req.ExplorerEnabled != nil {
		current.ExplorerEnabled = *req.ExplorerEnabled
	}
	if req.IdleRefreshEnabled != nil {
		current.IdleRefreshEnabled = *req.IdleRefreshEnabled
	}
	if req.SuperSourceEnabled != nil {
		current.SuperSourceEnabled = *req.SuperSourceEnabled
	}
	if req.SuperSourceBaseFeeMsat != nil {
		current.SuperSourceBaseFeeMsat = *req.SuperSourceBaseFeeMsat
	}
	if req.RevfloorEnabled != nil {
		current.RevfloorEnabled = *req.RevfloorEnabled
	}
	if req.CircuitBreakerEnabled != nil {
		current.CircuitBreakerEnabled = *req.CircuitBreakerEnabled
	}
	if req.ExtremeDrainEnabled != nil {
		current.ExtremeDrainEnabled = *req.ExtremeDrainEnabled
	}
	if req.HTLCSignalEnabled != nil {
		current.HTLCSignalEnabled = *req.HTLCSignalEnabled
	}
	if req.HTLCMode != nil {
		current.HTLCMode = normalizeHTLCMode(*req.HTLCMode)
	}
	if req.MinPpm != nil {
		current.MinPpm = *req.MinPpm
	}
	if req.MaxPpm != nil {
		current.MaxPpm = *req.MaxPpm
	}

	if current.LookbackDays < autofeeMinLookbackDays {
		current.LookbackDays = autofeeMinLookbackDays
	}
	if current.LookbackDays > autofeeMaxLookbackDays {
		current.LookbackDays = autofeeMaxLookbackDays
	}
	if current.MinPpm <= 0 {
		current.MinPpm = 0
	}
	if current.MaxPpm <= 0 {
		current.MaxPpm = 2000
	}
	if current.SuperSourceBaseFeeMsat < 0 {
		current.SuperSourceBaseFeeMsat = 0
	}
	current.RebalCostMode = normalizeRebalCostMode(current.RebalCostMode)
	current.HTLCMode = normalizeHTLCMode(current.HTLCMode)
	current.OperationMode = normalizeAutofeeOperationMode(current.OperationMode)

	if current.RunIntervalSec < 3600 {
		current.RunIntervalSec = 3600
	}
	if current.RunIntervalSec > 86400 {
		current.RunIntervalSec = 86400
	}
	if current.CooldownUpSec < autofeeMinCooldownSec {
		current.CooldownUpSec = autofeeMinCooldownSec
	}
	if current.CooldownDownSec < autofeeMinCooldownSec {
		current.CooldownDownSec = autofeeMinCooldownSec
	}
	if current.StepCapOverride < 0 {
		current.StepCapOverride = 0
	} else if current.StepCapOverride > 0 {
		current.StepCapOverride = clampFloat(current.StepCapOverride, 0.01, 0.30)
	}
	if current.DiscoveryStepCapDownOverride < 0 {
		current.DiscoveryStepCapDownOverride = 0
	} else if current.DiscoveryStepCapDownOverride > 0 {
		current.DiscoveryStepCapDownOverride = clampFloat(current.DiscoveryStepCapDownOverride, 0.01, 0.40)
	}
	if current.StallFloorRelaxGapFracOverride < 0 {
		current.StallFloorRelaxGapFracOverride = 0
	} else if current.StallFloorRelaxGapFracOverride > 0 {
		current.StallFloorRelaxGapFracOverride = clampFloat(current.StallFloorRelaxGapFracOverride, 0.01, 0.80)
	}
	if current.InboundDiscountMaxRatioOverride < 0 {
		current.InboundDiscountMaxRatioOverride = 0
	} else if current.InboundDiscountMaxRatioOverride > 0 {
		current.InboundDiscountMaxRatioOverride = clampFloat(current.InboundDiscountMaxRatioOverride, 0.50, 1.00)
	}
	if current.InboundDiscountReachOutRatioOverride < 0 {
		current.InboundDiscountReachOutRatioOverride = 0
	} else if current.InboundDiscountReachOutRatioOverride > 0 {
		current.InboundDiscountReachOutRatioOverride = clampFloat(current.InboundDiscountReachOutRatioOverride, 0.05, 0.50)
	}
	if current.InboundDiscountMinRetainedSpreadFracOverride < 0 {
		current.InboundDiscountMinRetainedSpreadFracOverride = 0
	} else if current.InboundDiscountMinRetainedSpreadFracOverride > 0 {
		current.InboundDiscountMinRetainedSpreadFracOverride = clampFloat(current.InboundDiscountMinRetainedSpreadFracOverride, 0.01, 0.50)
	}
	if current.OutrateFloorFactorLowOverride < 0 {
		current.OutrateFloorFactorLowOverride = 0
	} else if current.OutrateFloorFactorLowOverride > 0 {
		current.OutrateFloorFactorLowOverride = clampFloat(current.OutrateFloorFactorLowOverride, 0.50, 1.00)
	}
	if current.SoftenMinOutRatioOverride < 0 {
		current.SoftenMinOutRatioOverride = 0
	} else if current.SoftenMinOutRatioOverride > 0 {
		current.SoftenMinOutRatioOverride = clampFloat(current.SoftenMinOutRatioOverride, 0.05, 0.95)
	}
	if current.SoftenMaxDropToPegFracOverride < 0 {
		current.SoftenMaxDropToPegFracOverride = 0
	} else if current.SoftenMaxDropToPegFracOverride > 0 {
		current.SoftenMaxDropToPegFracOverride = clampFloat(current.SoftenMaxDropToPegFracOverride, 0.50, 1.00)
	}
	if current.HTLCMinAttempts60mOverride < 0 {
		current.HTLCMinAttempts60mOverride = 0
	} else if current.HTLCMinAttempts60mOverride > 0 {
		current.HTLCMinAttempts60mOverride = clampInt(current.HTLCMinAttempts60mOverride, 1, 100)
	}
	if current.HTLCPolicyFailRateOverride < 0 {
		current.HTLCPolicyFailRateOverride = 0
	} else if current.HTLCPolicyFailRateOverride > 0 {
		current.HTLCPolicyFailRateOverride = clampFloat(current.HTLCPolicyFailRateOverride, 0.05, 0.90)
	}
	if current.HTLCLiquidityFailRateOverride < 0 {
		current.HTLCLiquidityFailRateOverride = 0
	} else if current.HTLCLiquidityFailRateOverride > 0 {
		current.HTLCLiquidityFailRateOverride = clampFloat(current.HTLCLiquidityFailRateOverride, 0.05, 0.90)
	}

	var ambossToken string
	if req.AmbossToken != nil {
		ambossToken = strings.TrimSpace(*req.AmbossToken)
	} else {
		var raw pgtype.Text
		_ = s.db.QueryRow(ctx, `select amboss_token from autofee_config where id=$1`, autofeeConfigID).Scan(&raw)
		if raw.Valid {
			ambossToken = raw.String
		}
	}

	_, err = s.db.Exec(ctx, `
update autofee_config
set enabled=$2,
  operation_mode=$3,
  profile=$4,
  lookback_days=$5,
  run_interval_sec=$6,
  cooldown_up_sec=$7,
  cooldown_down_sec=$8,
  step_cap_override=$9,
  discovery_step_cap_down_override=$10,
  stall_floor_relax_gap_frac_override=$11,
  inbound_discount_max_ratio_override=$12,
  inbound_discount_reach_out_ratio_override=$13,
  inbound_discount_min_retained_spread_frac_override=$14,
  outrate_floor_factor_low_override=$15,
  soften_min_out_ratio_override=$16,
  soften_max_drop_to_peg_frac_override=$17,
  htlc_min_attempts_60m_override=$18,
  htlc_policy_fail_rate_override=$19,
  htlc_liquidity_fail_rate_override=$20,
  rebal_cost_mode=$21,
  native_seed_enabled=$22,
  amboss_enabled=$23,
  amboss_token=$24,
  inbound_passive_enabled=$25,
  discovery_enabled=$26,
  explorer_enabled=$27,
  idle_refresh_enabled=$28,
  super_source_enabled=$29,
  super_source_base_fee_msat=$30,
  revfloor_enabled=$31,
  circuit_breaker_enabled=$32,
  extreme_drain_enabled=$33,
  htlc_signal_enabled=$34,
  htlc_mode=$35,
  min_ppm=$36,
  max_ppm=$37,
  updated_at=now()
where id=$1
`, autofeeConfigID,
		current.Enabled,
		current.OperationMode,
		current.Profile,
		current.LookbackDays,
		current.RunIntervalSec,
		current.CooldownUpSec,
		current.CooldownDownSec,
		current.StepCapOverride,
		current.DiscoveryStepCapDownOverride,
		current.StallFloorRelaxGapFracOverride,
		current.InboundDiscountMaxRatioOverride,
		current.InboundDiscountReachOutRatioOverride,
		current.InboundDiscountMinRetainedSpreadFracOverride,
		current.OutrateFloorFactorLowOverride,
		current.SoftenMinOutRatioOverride,
		current.SoftenMaxDropToPegFracOverride,
		current.HTLCMinAttempts60mOverride,
		current.HTLCPolicyFailRateOverride,
		current.HTLCLiquidityFailRateOverride,
		current.RebalCostMode,
		current.NativeSeedEnabled,
		current.AmbossEnabled,
		ambossToken,
		current.InboundPassiveEnabled,
		current.DiscoveryEnabled,
		current.ExplorerEnabled,
		current.IdleRefreshEnabled,
		current.SuperSourceEnabled,
		current.SuperSourceBaseFeeMsat,
		current.RevfloorEnabled,
		current.CircuitBreakerEnabled,
		current.ExtremeDrainEnabled,
		current.HTLCSignalEnabled,
		current.HTLCMode,
		current.MinPpm,
		current.MaxPpm,
	)
	if err != nil {
		return autofeeConfigWithProfileDefaults(current), err
	}
	if previousRebalMode != current.RebalCostMode {
		_, resetErr := s.db.Exec(ctx, `
update autofee_state
set last_rebal_cost_ppm = null,
  last_rebal_cost_ts = null
`)
		if resetErr != nil {
			return autofeeConfigWithProfileDefaults(current), resetErr
		}
	}
	current.AmbossTokenSet = strings.TrimSpace(ambossToken) != ""
	s.nudgeScheduler()
	return autofeeConfigWithProfileDefaults(current), nil
}

func (s *AutofeeService) SaveMarketRefillRebalanceBackup(ctx context.Context, autoEnabled bool, manualRestartWatch bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	_, err := s.db.Exec(ctx, `
update autofee_config
set market_refill_rebalance_prev_saved=true,
  market_refill_rebalance_prev_auto=$2,
  market_refill_rebalance_prev_manual_restart=$3,
  updated_at=now()
where id=$1
`, autofeeConfigID, autoEnabled, manualRestartWatch)
	return err
}

func (s *AutofeeService) SaveMarketRefillFeeSnapshot(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if s.lnd == nil {
		return errors.New("lnd unavailable")
	}
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `delete from autofee_market_refill_fee_snapshot`); err != nil {
		return err
	}

	batch := &pgx.Batch{}
	queued := 0
	for _, ch := range channels {
		if ch.ChannelID == 0 || strings.TrimSpace(ch.ChannelPoint) == "" {
			continue
		}
		policy, err := s.lnd.GetChannelPolicy(ctx, ch.ChannelPoint)
		if err != nil {
			return fmt.Errorf("snapshot policy %s: %w", ch.ChannelPoint, err)
		}
		inboundEnabled := policy.InboundBaseMsat != 0 || policy.InboundFeeRatePpm != 0
		batch.Queue(`
insert into autofee_market_refill_fee_snapshot (
  channel_id, channel_point, base_fee_msat, fee_rate_ppm, time_lock_delta,
  inbound_enabled, inbound_base_msat, inbound_fee_rate_ppm, captured_at
)
values ($1,$2,$3,$4,$5,$6,$7,$8,now())
on conflict (channel_id) do update set
  channel_point=excluded.channel_point,
  base_fee_msat=excluded.base_fee_msat,
  fee_rate_ppm=excluded.fee_rate_ppm,
  time_lock_delta=excluded.time_lock_delta,
  inbound_enabled=excluded.inbound_enabled,
  inbound_base_msat=excluded.inbound_base_msat,
  inbound_fee_rate_ppm=excluded.inbound_fee_rate_ppm,
  captured_at=excluded.captured_at
`, int64(ch.ChannelID), ch.ChannelPoint, policy.BaseFeeMsat, policy.FeeRatePpm, policy.TimeLockDelta, inboundEnabled, policy.InboundBaseMsat, policy.InboundFeeRatePpm)
		queued++
	}
	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < queued; i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *AutofeeService) LoadMarketRefillRebalanceBackup(ctx context.Context) (bool, bool, bool, error) {
	if s.db == nil {
		return false, false, false, errors.New("db unavailable")
	}
	var saved bool
	var autoEnabled bool
	var manualRestartWatch bool
	err := s.db.QueryRow(ctx, `
select market_refill_rebalance_prev_saved, market_refill_rebalance_prev_auto, market_refill_rebalance_prev_manual_restart
from autofee_config
where id=$1
`, autofeeConfigID).Scan(&saved, &autoEnabled, &manualRestartWatch)
	if err != nil {
		return false, false, false, err
	}
	return saved, autoEnabled, manualRestartWatch, nil
}

func (s *AutofeeService) ClearMarketRefillRebalanceBackup(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	_, err := s.db.Exec(ctx, `
update autofee_config
set market_refill_rebalance_prev_saved=false,
  market_refill_rebalance_prev_auto=false,
  market_refill_rebalance_prev_manual_restart=false,
  updated_at=now()
where id=$1
`, autofeeConfigID)
	return err
}

func (s *AutofeeService) RestoreMarketRefillFeeSnapshot(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if s.lnd == nil {
		return errors.New("lnd unavailable")
	}
	rows, err := s.db.Query(ctx, `
select channel_point, base_fee_msat, fee_rate_ppm, time_lock_delta, inbound_enabled, inbound_base_msat, inbound_fee_rate_ppm
from autofee_market_refill_fee_snapshot
order by channel_id asc
`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var channelPoint string
		var baseFeeMsat int64
		var feeRatePpm int64
		var timeLockDelta int64
		var inboundEnabled bool
		var inboundBaseMsat int64
		var inboundFeeRatePpm int64
		if err := rows.Scan(&channelPoint, &baseFeeMsat, &feeRatePpm, &timeLockDelta, &inboundEnabled, &inboundBaseMsat, &inboundFeeRatePpm); err != nil {
			return err
		}
		if strings.TrimSpace(channelPoint) == "" {
			continue
		}
		if timeLockDelta <= 0 {
			timeLockDelta = 144
		}
		if !inboundEnabled {
			inboundBaseMsat = 0
			inboundFeeRatePpm = 0
		}
		if err := s.lnd.UpdateChannelFees(ctx, channelPoint, false, baseFeeMsat, feeRatePpm, timeLockDelta, true, inboundBaseMsat, inboundFeeRatePpm); err != nil {
			return fmt.Errorf("restore policy %s: %w", channelPoint, err)
		}
	}
	return rows.Err()
}

func (s *AutofeeService) ClearMarketRefillFeeSnapshot(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	_, err := s.db.Exec(ctx, `delete from autofee_market_refill_fee_snapshot`)
	return err
}

func (s *AutofeeService) LoadChannelSettings(ctx context.Context) (map[uint64]bool, error) {
	settings := map[uint64]bool{}
	if s.db == nil {
		return settings, errors.New("db unavailable")
	}
	rows, err := s.db.Query(ctx, `select channel_id, enabled from autofee_channel_settings`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var enabled bool
		if err := rows.Scan(&channelID, &enabled); err != nil {
			return settings, err
		}
		settings[uint64(channelID)] = enabled
	}
	return settings, rows.Err()
}

func (s *AutofeeService) loadRebalanceChannelSettings(ctx context.Context) (map[uint64]autofeeRebalanceChannelSetting, error) {
	settings := map[uint64]autofeeRebalanceChannelSetting{}
	if s.db == nil {
		return settings, errors.New("db unavailable")
	}
	rows, err := s.db.Query(ctx, `
select channel_id, auto_enabled, manual_restart_enabled
from rebalance_channel_settings
`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var item autofeeRebalanceChannelSetting
		if err := rows.Scan(&channelID, &item.AutoEnabled, &item.ManualRestartEnabled); err != nil {
			return settings, err
		}
		if channelID <= 0 {
			continue
		}
		settings[uint64(channelID)] = item
	}
	return settings, rows.Err()
}

func (s *AutofeeService) SetChannelEnabled(ctx context.Context, channelID uint64, channelPoint string, enabled bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	trimmedPoint := strings.TrimSpace(channelPoint)
	if trimmedPoint != "" && s.lnd != nil {
		if resolved, ok := s.resolveChannelID(ctx, trimmedPoint); ok {
			channelID = resolved
		} else if channelID == 0 {
			return errors.New("channel_id lookup failed")
		}
	}
	if channelID == 0 && trimmedPoint == "" {
		return errors.New("channel_id or channel_point required")
	}
	_, err := s.db.Exec(ctx, `
insert into autofee_channel_settings (channel_id, channel_point, enabled, updated_at)
values ($1, $2, $3, now())
on conflict (channel_id) do update set enabled=excluded.enabled, channel_point=excluded.channel_point, updated_at=excluded.updated_at
`, int64(channelID), trimmedPoint, enabled)
	return err
}

func (s *AutofeeService) resolveChannelID(ctx context.Context, channelPoint string) (uint64, bool) {
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return 0, false
	}
	for _, ch := range channels {
		if ch.ChannelPoint == channelPoint {
			return ch.ChannelID, true
		}
	}
	return 0, false
}

func (s *AutofeeService) SetAllChannelsEnabled(ctx context.Context, enabled bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if s.lnd == nil {
		return errors.New("lnd unavailable")
	}
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for _, ch := range channels {
		batch.Queue(`
insert into autofee_channel_settings (channel_id, channel_point, enabled, updated_at)
values ($1, $2, $3, now())
on conflict (channel_id) do update set enabled=excluded.enabled, channel_point=excluded.channel_point, updated_at=excluded.updated_at
`, int64(ch.ChannelID), ch.ChannelPoint, enabled)
	}
	br := s.db.SendBatch(ctx, batch)
	defer br.Close()
	for range channels {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *AutofeeService) Status() AutofeeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := AutofeeStatus{
		Running:   s.running,
		LastError: s.lastError,
	}
	if !s.lastRunAt.IsZero() {
		status.LastRunAt = s.lastRunAt.UTC().Format(time.RFC3339)
	}
	if !s.nextRunAt.IsZero() {
		status.NextRunAt = s.nextRunAt.UTC().Format(time.RFC3339)
	}
	return status
}

func (s *AutofeeService) appendAutofeeLines(ctx context.Context, runID string, entries []autofeeLogEntry) error {
	if s.db == nil || len(entries) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	now := time.Now().UTC()
	for i, entry := range entries {
		var payload any
		if entry.Payload != nil {
			raw, _ := json.Marshal(entry.Payload)
			payload = raw
		}
		batch.Queue(`insert into autofee_logs (occurred_at, run_id, seq, line, payload) values ($1,$2,$3,$4,$5)`,
			now, runID, i, entry.Line, payload)
	}
	br := s.db.SendBatch(ctx, batch)
	defer br.Close()
	for range entries {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *AutofeeService) loadRecentChangeStats(ctx context.Context, lookback time.Duration) (map[uint64]autofeeRecentChangeStats, error) {
	stats := map[uint64]autofeeRecentChangeStats{}
	if s.db == nil {
		return stats, nil
	}
	if lookback <= 0 {
		lookback = 24 * time.Hour
	}
	rows, err := s.db.Query(ctx, `
select
  payload->>'channel_id' as channel_id,
  payload->>'local_ppm' as local_ppm,
  payload->>'new_ppm' as new_ppm
from autofee_logs
where occurred_at >= now() - $1::interval
  and coalesce(payload->>'dry_run', 'false') <> 'true'
  and coalesce(payload->>'kind', '') = 'decision'
  and coalesce(payload->>'channel_id', '') <> ''
  and coalesce(payload->>'local_ppm', '') <> ''
  and coalesce(payload->>'new_ppm', '') <> ''
order by occurred_at asc, seq asc
`, formatIntervalForPostgres(lookback))
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	lastDirByChannel := map[uint64]string{}
	for rows.Next() {
		var channelIDRaw string
		var localRaw string
		var newRaw string
		if err := rows.Scan(&channelIDRaw, &localRaw, &newRaw); err != nil {
			return stats, err
		}
		channelID, err1 := strconv.ParseUint(strings.TrimSpace(channelIDRaw), 10, 64)
		localPpm, err2 := strconv.Atoi(strings.TrimSpace(localRaw))
		newPpm, err3 := strconv.Atoi(strings.TrimSpace(newRaw))
		if err1 != nil || err2 != nil || err3 != nil || channelID == 0 || newPpm == localPpm {
			continue
		}
		item := stats[channelID]
		item.ChangeCount24h++
		dir := "down"
		if newPpm > localPpm {
			dir = "up"
			item.UpCount24h++
			if lastDirByChannel[channelID] == "up" {
				item.ConsecutiveUp24h++
			} else {
				item.ConsecutiveUp24h = 1
			}
		} else {
			item.DownCount24h++
			item.ConsecutiveUp24h = 0
		}
		lastDirByChannel[channelID] = dir
		stats[channelID] = item
	}
	return stats, rows.Err()
}

func formatIntervalForPostgres(d time.Duration) string {
	seconds := int64(math.Round(d.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func (s *AutofeeService) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.stop = make(chan struct{})
	s.wake = make(chan struct{}, 1)
	stop := s.stop
	s.mu.Unlock()

	go s.loop()
	go s.measurementLoop(stop)
}

func (s *AutofeeService) Stop() {
	s.mu.Lock()
	if s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	s.mu.Unlock()
}

const (
	autofeeOutcomeMeasurementWindow   = 24 * time.Hour
	autofeeOutcomeMeasurementInterval = 30 * time.Minute
	autofeeOutcomeMeasurementBatch    = 50
)

// Threshold for the apply-failure alert. After this many consecutive failed
// applies on the same channel a Telegram alert fires once. Successful apply
// resets the streak.
const autofeeApplyErrorAlertThreshold = 3

// measurementLoop periodically processes pending autofee_outcomes rows whose
// 24h window has elapsed. Runs independently from the decision loop so it
// keeps measuring even when Autofee is disabled.
func (s *AutofeeService) measurementLoop(stop <-chan struct{}) {
	if stop == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.Printf("autofee: measurementLoop panic recovered: %v", r)
		}
	}()

	// Initial run on startup so we don't wait the full interval after a restart.
	s.runMeasurementTick()

	ticker := time.NewTicker(autofeeOutcomeMeasurementInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.runMeasurementTick()
		}
	}
}

func (s *AutofeeService) runMeasurementTick() {
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.Printf("autofee: measurement tick panic recovered: %v", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.MeasureOutcomes(ctx); err != nil && s.logger != nil {
		s.logger.Printf("autofee: measurement tick failed: %v", err)
	}
}

// MeasureOutcomes scans up to autofeeOutcomeMeasurementBatch pending rows
// older than 24h, computes the post-decision metrics, and updates each row.
// Failures on individual rows are recorded as measurement_status='error' so
// the batch does not abort. Safe to call manually.
func (s *AutofeeService) MeasureOutcomes(ctx context.Context) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}

	rows, err := s.db.Query(ctx, `
select id, channel_id, decided_at
from autofee_outcomes
where measurement_status='pending'
  and decided_at < now() - interval '24 hours'
order by decided_at asc
limit $1
`, autofeeOutcomeMeasurementBatch)
	if err != nil {
		return err
	}

	type pending struct {
		ID        int64
		ChannelID int64
		DecidedAt time.Time
	}
	items := make([]pending, 0, autofeeOutcomeMeasurementBatch)
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.DecidedAt); err != nil {
			rows.Close()
			return err
		}
		items = append(items, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, it := range items {
		if err := s.measureOne(ctx, it.ID, uint64(it.ChannelID), it.DecidedAt); err != nil {
			if s.logger != nil {
				s.logger.Printf("autofee: measure outcome id=%d failed: %v", it.ID, err)
			}
			updCtx, updCancel := context.WithTimeout(ctx, 2*time.Second)
			_, _ = s.db.Exec(updCtx, `
update autofee_outcomes
set measurement_status='error',
    measurement_error=$2,
    measured_at=now()
where id=$1
`, it.ID, err.Error())
			updCancel()
		}
	}
	return nil
}

func (s *AutofeeService) measureOne(ctx context.Context, id int64, channelID uint64, decidedAt time.Time) error {
	windowStart := decidedAt
	windowEnd := decidedAt.Add(autofeeOutcomeMeasurementWindow)

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var fwdFee, fwdAmt int64
	var fwdCount int
	err := s.db.QueryRow(queryCtx, `
select
  coalesce(sum(
    case
      when fee_msat > 0 then fee_msat
      when fee_sat > 0 then fee_sat * 1000
      when amount_in_msat > 0 and amount_out_msat > 0 and amount_in_msat > amount_out_msat then amount_in_msat - amount_out_msat
      else 0
    end
  ), 0),
  coalesce(sum(case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end), 0),
  count(*)
from notifications
where type='forward'
  and coalesce(chan_id_out, channel_id) = $1
  and occurred_at >= $2
  and occurred_at < $3
`, int64(channelID), windowStart, windowEnd).Scan(&fwdFee, &fwdAmt, &fwdCount)
	if err != nil {
		return fmt.Errorf("forward stats: %w", err)
	}

	var rebalFee, rebalAmtSat int64
	err = s.db.QueryRow(queryCtx, `
select
  coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0),
  coalesce(sum(amount_sat), 0)
from notifications
where type='rebalance'
  and coalesce(rebal_target_chan_id, channel_id) = $1
  and status in ('SETTLED', 'SUCCEEDED')
  and occurred_at >= $2
  and occurred_at < $3
`, int64(channelID), windowStart, windowEnd).Scan(&rebalFee, &rebalAmtSat)
	if err != nil {
		return fmt.Errorf("rebalance stats: %w", err)
	}
	rebalAmtMsat := rebalAmtSat * 1000

	updCtx, updCancel := context.WithTimeout(ctx, 5*time.Second)
	defer updCancel()
	_, err = s.db.Exec(updCtx, `
update autofee_outcomes
set measurement_status='measured',
    measured_at=now(),
    fwd_count_24h_after=$2,
    fwd_amt_msat_24h_after=$3,
    fwd_fee_msat_24h_after=$4,
    rebal_amt_msat_24h_after=$5,
    rebal_fee_msat_24h_after=$6,
    measurement_error=null
where id=$1
`, id, fwdCount, fwdAmt, fwdFee, rebalAmtMsat, rebalFee)
	return err
}

func (s *AutofeeService) nudgeScheduler() {
	s.mu.Lock()
	wake := s.wake
	started := s.started
	s.mu.Unlock()
	if !started || wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (s *AutofeeService) loop() {
	for {
		cfg, err := s.GetConfig(context.Background())
		if err != nil {
			s.logger.Printf("autofee: config load failed: %v", err)
		}
		interval := time.Duration(cfg.RunIntervalSec) * time.Second
		if interval < time.Hour {
			interval = time.Hour
		}
		if interval > 24*time.Hour {
			interval = 24 * time.Hour
		}
		now := time.Now()
		base := now
		s.mu.Lock()
		lastRun := s.lastRunAt
		s.mu.Unlock()
		if !lastRun.IsZero() {
			base = lastRun
		} else if s.db != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if ts, ok := s.lastRunFromLogs(ctx); ok {
				base = ts
				s.mu.Lock()
				s.lastRunAt = ts
				s.mu.Unlock()
			}
			cancel()
		}

		next := base.Add(interval)
		if !base.IsZero() && base.Before(now) {
			elapsed := now.Sub(base)
			steps := int64(elapsed/interval) + 1
			next = base.Add(time.Duration(steps) * interval)
		}
		jitter := time.Duration(rand.Int63n(int64(interval/10)+1)) - time.Duration(int64(interval/20))
		next = next.Add(jitter)
		if next.Before(now.Add(time.Minute)) {
			next = now.Add(time.Minute)
		}
		s.mu.Lock()
		s.nextRunAt = next
		s.mu.Unlock()

		timer := time.NewTimer(time.Until(next))
		select {
		case <-s.stop:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-s.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case <-timer.C:
			_ = s.Run(context.Background(), false, "scheduled")
		}
	}
}

func (s *AutofeeService) Run(ctx context.Context, dryRun bool, reason string) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if s.lnd == nil {
		return errors.New("lnd unavailable")
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("autofee already running")
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		if !dryRun {
			s.lastRunAt = time.Now()
		}
		s.mu.Unlock()
		if !dryRun {
			s.nudgeScheduler()
		}
	}()

	cfg, err := s.GetConfig(ctx)
	if err != nil {
		s.setLastError(err)
		return err
	}
	if !cfg.Enabled && reason != "manual" {
		return nil
	}

	if status, err := s.lnd.GetStatus(ctx); err == nil {
		if !status.SyncedToChain || !status.SyncedToGraph {
			if reason == "manual" {
				runID := fmt.Sprintf("%d", time.Now().UnixNano())
				now := time.Now().UTC()
				header := fmt.Sprintf("⚡ Autofee %s [%s] | %s", strings.ToUpper(reason), strings.ToUpper(cfg.OperationMode), now.Format(time.RFC3339))
				if dryRun {
					header = header + " (dry-run)"
				}
				entries := []autofeeLogEntry{
					{Line: header, Payload: &autofeeLogItem{Kind: "header", Reason: reason, OperationMode: cfg.OperationMode, DryRun: dryRun, Timestamp: now.Format(time.RFC3339)}},
					{Line: "📊 up 0 | down 0 | flat 0 | cooldown 0 | small 0 | same 0 | disabled 0 | inactive 0 | inb_disc 0 | super_source 0", Payload: &autofeeLogItem{
						Kind: "summary",
						Up:   0, Down: 0, Flat: 0, Cooldown: 0, Small: 0, Same: 0, Disabled: 0, Inactive: 0, InboundDisc: 0, SuperSource: 0,
					}},
					{Line: "🌱 seed amboss=0 missing=0 err=0 empty=0 outrate=0 mem=0 default=0", Payload: &autofeeLogItem{
						Kind:   "seed",
						Amboss: 0, Missing: 0, Err: 0, Empty: 0, Outrate: 0, Mem: 0, Default: 0, CooldownIgnored: false,
					}},
					{Line: "⚠️ skipped: lnd not synced"},
				}
				_ = s.appendAutofeeLines(ctx, runID, entries)
			}
			return nil
		}
	}

	engine := newAutofeeEngine(s, cfg)
	if reason == "manual" {
		engine.ignoreCooldown = true
	}
	err = engine.Execute(ctx, dryRun, reason)
	if err != nil {
		s.setLastError(err)
		return err
	}
	s.setLastError(nil)
	return nil
}

func (s *AutofeeService) setLastError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	s.lastError = ""
}

// ===== Engine =====

type autofeeEngine struct {
	svc              *AutofeeService
	cfg              AutofeeConfig
	profile          autofeeProfile
	superSource      superSourceThresholds
	ignoreCooldown   bool
	calib            autofeeCalibration
	ranking          map[string]autofeeRankingSnapshot
	rebalanceConfig  map[uint64]autofeeRebalanceChannelSetting
	rebalanceRuntime map[uint64]autofeeRebalanceRuntimeSnapshot
	rebalanceROIMin  float64
	rebalanceStatus  string
	recentChanges    map[uint64]autofeeRecentChangeStats
	now              time.Time
	nativeSeedCache  map[string]autofeeSeedResult
	policyCache      map[string]lndclient.ChannelPolicy
	ambossToken      string
	ambossTokenErr   error
	ambossTokenLoad  bool
}

type autofeeRebalanceChannelSetting struct {
	AutoEnabled          bool
	ManualRestartEnabled bool
}

type autofeeRebalanceRuntimeSnapshot struct {
	AutoEnabled          bool
	ManualRestartEnabled bool
	EligibleAsTarget     bool
	PaybackProgress      float64
	ROIEstimate          float64
	ROIEstimateValid     bool
	Revenue7dSat         int64
	RebalanceCost7dPpm   int64
}

type autofeeRecentChangeStats struct {
	ChangeCount24h   int
	UpCount24h       int
	DownCount24h     int
	ConsecutiveUp24h int
}

type autofeeSeedResult struct {
	Seed           float64
	SeedP95        float64
	PeerMarketSkew float64
	Tags           []string
	Ok             bool
	Err            error
}

type autofeeCalibration struct {
	RevfloorBaseline      int
	RevfloorMinAbs        int
	NodeClass             string
	LiquidityClass        string
	ChannelCount          int
	TotalCapacitySat      int64
	AvgCapacitySat        int64
	LocalCapacitySat      int64
	LocalRatio            float64
	HTLCNodeFactor        float64
	HTLCLiquidityFactor   float64
	HTLCThresholdFactor   float64
	HTLCMinAttempts       int
	HTLCMinPolicyFails    int
	HTLCMinLiquidityFails int
	HTLCMinForwardFails   int
	HTLCPolicyRateMin     float64
	HTLCLiquidityRateMin  float64
	HTLCForwardRateMin    float64
	HTLCGlobalCountFactor float64
	HTLCGlobalRateFactor  float64
	HTLCWindowMin         int
	HTLCClassifiedRatio   float64
	HTLCNoisySample       bool
	LowOutThresh          float64
	LowOutProtectThresh   float64
	LowOutFactor          float64
}

type autofeeRunSummary struct {
	total                  int
	inactive               int
	disabled               int
	eligible               int
	applied                int
	applyErrors            int
	changedUp              int
	changedDown            int
	kept                   int
	skippedCooldown        int
	skippedSmall           int
	skippedSame            int
	skippedOther           int
	seedNative             int
	seedNativeInsufficient int
	seedNativeError        int
	seedAmboss             int
	seedAmbossMissing      int
	seedAmbossError        int
	seedAmbossEmpty        int
	seedOutrate            int
	seedMem                int
	seedDefault            int
	superSource            int
	inboundDiscount        int
	htlcLiqHot             int
	htlcPolicyHot          int
	htlcForwardHot         int
	htlcSampleLow          int
	reversalBlocked        int
	reversalConfirmed      int
	downcapGeneral         int
	downcapLowSample       int
	floorRelaxApplied      int
	stallAlert             int
	htlcAttemptsTotal      int
	htlcLinkFailsTotal     int
	htlcForwardFailsTotal  int
	htlcOtherFailsTotal    int
	htlcClassifiedTotal    int
	htlcClassifiedRatio    float64
	htlcUnclassifiedTotal  int
	htlcNoisySampleApplied bool
	htlcTopReasons         []string
	htlcWindowMin          int
	htlcMinAttempts        int
	htlcMinPolicyFails     int
	htlcMinLiquidityFails  int
	htlcMinForwardFails    int
	htlcPolicyRateMin      float64
	htlcLiquidityRateMin   float64
	htlcForwardRateMin     float64
	htlcGlobalCountFactor  float64
	htlcGlobalRateFactor   float64
	htlcNodeFactor         float64
	htlcLiquidityFactor    float64
	htlcThresholdFactor    float64
}

func (s *autofeeRunSummary) addTags(tags []string) {
	for _, tag := range tags {
		switch tag {
		case "seed:native":
			s.seedNative++
		case "seed:native-insufficient", "seed:native-empty":
			s.seedNativeInsufficient++
		case "seed:native-error":
			s.seedNativeError++
		case "seed:amboss":
			s.seedAmboss++
		case "seed:amboss-missing":
			s.seedAmbossMissing++
		case "seed:amboss-error":
			s.seedAmbossError++
		case "seed:amboss-empty":
			s.seedAmbossEmpty++
		case "seed:outrate":
			s.seedOutrate++
		case "seed:mem":
			s.seedMem++
		case "seed:default":
			s.seedDefault++
		case "cooldown":
			s.skippedCooldown++
		case "hold-small":
			s.skippedSmall++
		case "same-ppm":
			s.skippedSame++
		case "autofee_settling":
			s.skippedOther++
		case "htlc-liquidity-hot":
			s.htlcLiqHot++
		case "htlc-policy-hot":
			s.htlcPolicyHot++
		case "htlc-forward-hot":
			s.htlcForwardHot++
		case "htlc-sample-low":
			s.htlcSampleLow++
		case "reversal-guard":
			s.reversalBlocked++
		case "reversal-confirmed":
			s.reversalConfirmed++
		case "downcap-general":
			s.downcapGeneral++
		case "htlc-low-sample-downcap":
			s.downcapLowSample++
		case "floor-relax-stall", "stagnation-floor-relax", "no-signal-floor-relax":
			s.floorRelaxApplied++
		case "stall-alert":
			s.stallAlert++
		}
	}
}

func newAutofeeEngine(svc *AutofeeService, cfg AutofeeConfig) *autofeeEngine {
	p := autofeeProfiles[cfg.Profile]
	if p.Name == "" {
		p = autofeeProfiles["moderate"]
	}
	if cfg.StepCapOverride > 0 {
		p.StepCap = clampFloat(cfg.StepCapOverride, 0.01, 0.30)
	}
	if cfg.DiscoveryStepCapDownOverride > 0 {
		p.DiscoveryStepCapDown = clampFloat(cfg.DiscoveryStepCapDownOverride, 0.01, 0.40)
	}
	if cfg.OutrateFloorFactorLowOverride > 0 {
		p.OutrateFloorFactorLow = clampFloat(cfg.OutrateFloorFactorLowOverride, 0.50, 1.00)
	}
	if cfg.SoftenMinOutRatioOverride > 0 {
		p.SoftenMinOutRatio = clampFloat(cfg.SoftenMinOutRatioOverride, 0.05, 0.95)
	}
	if cfg.SoftenMaxDropToPegFracOverride > 0 {
		p.SoftenMaxDropToPegFrac = clampFloat(cfg.SoftenMaxDropToPegFracOverride, 0.50, 1.00)
	}
	if cfg.HTLCMinAttempts60mOverride > 0 {
		p.HTLCMinAttempts60m = clampInt(cfg.HTLCMinAttempts60mOverride, 1, 100)
	}
	if cfg.HTLCPolicyFailRateOverride > 0 {
		p.HTLCPolicyFailRate = clampFloat(cfg.HTLCPolicyFailRateOverride, 0.05, 0.90)
	}
	if cfg.HTLCLiquidityFailRateOverride > 0 {
		p.HTLCLiquidityFailRate = clampFloat(cfg.HTLCLiquidityFailRateOverride, 0.05, 0.90)
	}
	ss := superSourceThresholdsByProfile[p.Name]
	if ss.OutRatioMin == 0 {
		ss = superSourceThresholdsByProfile["moderate"]
	}
	return &autofeeEngine{
		svc:             svc,
		cfg:             cfg,
		profile:         p,
		superSource:     ss,
		now:             time.Now().UTC(),
		nativeSeedCache: make(map[string]autofeeSeedResult),
		policyCache:     make(map[string]lndclient.ChannelPolicy),
	}
}

func (e *autofeeEngine) calibrateNode(channels []lndclient.ChannelInfo, state map[uint64]*autofeeChannelState, forwardStats map[uint64]forwardStat) {
	baselineVals := []float64{}
	seedVals := []float64{}
	totalCap := int64(0)
	totalLocal := int64(0)
	chanCount := 0
	for _, ch := range channels {
		if !ch.Active {
			continue
		}
		chanCount++
		totalCap += ch.CapacitySat
		totalLocal += ch.LocalBalanceSat
		st := state[ch.ChannelID]
		baseline := 0
		if st != nil && st.BaselineFwd7d > 0 {
			baseline = st.BaselineFwd7d
		}
		if fs, ok := forwardStats[ch.ChannelID]; ok && int(fs.Count) > baseline {
			baseline = int(fs.Count)
		}
		if baseline > 0 {
			baselineVals = append(baselineVals, float64(baseline))
		}
		if st != nil && st.LastSeed > 0 {
			seedVals = append(seedVals, float64(st.LastSeed))
		}
	}

	p70 := percentile(baselineVals, 0.70)
	revfloorBaseline := int(math.Round(p70))
	if revfloorBaseline < 5 {
		revfloorBaseline = 5
	}
	if e.profile.RevfloorBaselineScale > 0 {
		revfloorBaseline = int(math.Round(float64(revfloorBaseline) * e.profile.RevfloorBaselineScale))
		if revfloorBaseline < 5 {
			revfloorBaseline = 5
		}
	}

	medianSeed := percentile(seedVals, 0.50)
	revfloorMinAbs := 0
	if medianSeed > 0 {
		revfloorMinAbs = int(math.Round(medianSeed * 0.80))
	} else {
		revfloorMinAbs = e.profile.RevfloorMinAbs
	}
	if e.profile.RevfloorMinAbsScale > 0 {
		revfloorMinAbs = int(math.Round(float64(revfloorMinAbs) * e.profile.RevfloorMinAbsScale))
	}
	if revfloorMinAbs < 60 {
		revfloorMinAbs = 60
	}

	avgCap := int64(0)
	if chanCount > 0 {
		avgCap = int64(math.Round(float64(totalCap) / float64(chanCount)))
	}
	localRatio := 0.0
	if totalCap > 0 {
		localRatio = float64(totalLocal) / float64(totalCap)
	}

	nodeClass := "unknown"
	if chanCount > 0 && totalCap > 0 {
		switch {
		case totalCap < 50_000_000 || chanCount < 20:
			nodeClass = "small"
		case totalCap < 200_000_000 || chanCount < 60:
			nodeClass = "medium"
		case totalCap < 1_500_000_000 || chanCount < 150:
			nodeClass = "large"
		default:
			nodeClass = "xl"
		}
	}

	liquidityClass := "balanced"
	if totalCap > 0 {
		if localRatio < 0.25 {
			liquidityClass = "drained"
		} else if localRatio > 0.75 {
			liquidityClass = "full"
		}
	}
	lowOutThresh, lowOutProtectThresh, lowOutFactor := effectiveLowOutThresholds(
		e.profile.LowOutThresh,
		e.profile.LowOutProtectThresh,
		liquidityClass,
		localRatio,
	)

	e.calib.RevfloorBaseline = revfloorBaseline
	e.calib.RevfloorMinAbs = revfloorMinAbs
	e.calib.NodeClass = nodeClass
	e.calib.LiquidityClass = liquidityClass
	e.calib.ChannelCount = chanCount
	e.calib.TotalCapacitySat = totalCap
	e.calib.LocalCapacitySat = totalLocal
	e.calib.AvgCapacitySat = avgCap
	e.calib.LocalRatio = localRatio
	e.calib.LowOutThresh = lowOutThresh
	e.calib.LowOutProtectThresh = lowOutProtectThresh
	e.calib.LowOutFactor = lowOutFactor
}

type autofeeChannelState struct {
	ChannelID           uint64
	LastPpm             int
	LastInboundDiscount int
	LastSeed            int
	SeedShockPendingPpm int
	SeedShockRounds     int
	SeedShockDir        string
	LastOutrate         int
	LastOutrateTs       time.Time
	LastRebalCost       int
	LastRebalCostTs     time.Time
	LastIdleRefreshTs   time.Time
	LastIdleRefreshPpm  int
	LastIdleRefreshSrc  string
	LastTs              time.Time
	LastDir             string
	LowStreak           int
	StalledRounds       int
	BaselineFwd7d       int
	ClassLabel          string
	ClassConf           float64
	BiasEma             float64
	FirstSeen           time.Time
	SuperSourceActive   bool
	SuperSourceOkSince  time.Time
	SuperSourceBadSince time.Time
	ExplorerState       explorerState
	ApplyErrorStreak    int
	LastApplyError      string
	ApplyErrorAlerted   bool
}

type autofeeRankingSnapshot struct {
	Score                   int
	Score30d                int
	State                   string
	TrendDirection          string
	TrendDelta              int
	ProfitFee7dSat          int64
	ProfitFee30dSat         int64
	OutPpm30d               int
	RebalPpm30d             int
	PeerStabilityScore      int
	LocalBalancePct         float64
	ForwardInCount7d        int
	ForwardInAmountSat7d    int64
	ForwardOutCount7d       int
	ForwardOutAmountSat7d   int64
	AssistedForwardFee7dSat int64
	RebalanceDependence     int
}

type explorerState struct {
	Active                bool   `json:"active"`
	StartedTs             int64  `json:"started_ts"`
	Rounds                int    `json:"rounds"`
	FwdsAtStart           int    `json:"fwds_at_start"`
	LastExitTs            int64  `json:"last_exit_ts"`
	Seen                  bool   `json:"seen"`
	DrainedActive         bool   `json:"drained_active,omitempty"`
	DrainedStartedTs      int64  `json:"drained_started_ts,omitempty"`
	DrainedRounds         int    `json:"drained_rounds,omitempty"`
	DrainedFwdsAtStart    int    `json:"drained_fwds_at_start,omitempty"`
	DrainedLastExitTs     int64  `json:"drained_last_exit_ts,omitempty"`
	DrainedSeen           bool   `json:"drained_seen,omitempty"`
	StagnationNoFwdRounds int    `json:"stagnation_no_fwd_rounds,omitempty"`
	StagnationPhase       int    `json:"stagnation_phase,omitempty"`
	SurgeGateRounds       int    `json:"surge_gate_rounds,omitempty"`
	SurgeGatePpm          int    `json:"surge_gate_ppm,omitempty"`
	ReversalPendingDir    string `json:"reversal_pending_dir,omitempty"`
	ReversalPendingRounds int    `json:"reversal_pending_rounds,omitempty"`
	LastReversalDir       string `json:"last_reversal_dir,omitempty"`
	LastReversalTs        int64  `json:"last_reversal_ts,omitempty"`
	RescueActive          bool   `json:"rescue_active,omitempty"`
	RescueStartedTs       int64  `json:"rescue_started_ts,omitempty"`
	RescueRounds          int    `json:"rescue_rounds,omitempty"`
	RescueRecoverRounds   int    `json:"rescue_recover_rounds,omitempty"`
	RescueLastExitTs      int64  `json:"rescue_last_exit_ts,omitempty"`
}

func (s *AutofeeService) LoadChannelSettingsDetailed(ctx context.Context) ([]AutofeeChannelSettingEntry, error) {
	entries := []AutofeeChannelSettingEntry{}
	if s.db == nil {
		return entries, errors.New("db unavailable")
	}
	rows, err := s.db.Query(ctx, `select channel_id, channel_point, enabled from autofee_channel_settings`)
	if err != nil {
		return entries, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var channelPoint string
		var enabled bool
		if err := rows.Scan(&channelID, &channelPoint, &enabled); err != nil {
			return entries, err
		}
		entries = append(entries, AutofeeChannelSettingEntry{
			ChannelID:    uint64(channelID),
			ChannelPoint: strings.TrimSpace(channelPoint),
			Enabled:      enabled,
		})
	}
	return entries, rows.Err()
}

func autofeeChannelEnabled(settings map[uint64]bool, channelID uint64) bool {
	if channelID == 0 {
		return false
	}
	enabled, ok := settings[channelID]
	return !ok || enabled
}

func (s *AutofeeService) RefreshReferenceFees(ctx context.Context, dryRun bool, includeInbound bool) (AutofeeRefreshResult, error) {
	runAt := time.Now().UTC()
	result := AutofeeRefreshResult{DryRun: dryRun, IncludeInbound: includeInbound, RebalMarkupPct: autofeeRefreshRebalMarkup * 100}
	if s.db == nil {
		return result, errors.New("db unavailable")
	}
	if s.lnd == nil {
		return result, errors.New("lnd unavailable")
	}

	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return result, err
	}
	engine := newAutofeeEngine(s, cfg)

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return result, err
	}
	result.Total = len(channels)

	settings, err := s.LoadChannelSettings(ctx)
	if err != nil {
		return result, err
	}

	forwardStats7d, err := engine.fetchForwardStats(ctx, 7)
	if err != nil {
		return result, err
	}
	forwardStats1d := map[uint64]forwardStat{}
	if includeInbound {
		forwardStats1d, err = engine.fetchForwardStats(ctx, 1)
		if err != nil {
			return result, err
		}
	}
	forwardStats21d, err := engine.fetchForwardStats(ctx, 21)
	if err != nil {
		return result, err
	}
	rebalStats7d, err := engine.fetchRebalanceStats(ctx, 7)
	if err != nil {
		return result, err
	}
	rebalStats21d, err := engine.fetchRebalanceStats(ctx, 21)
	if err != nil {
		return result, err
	}

	totalCap := int64(0)
	totalLocal := int64(0)
	for _, ch := range channels {
		totalCap += ch.CapacitySat
		totalLocal += ch.LocalBalanceSat
	}
	avgCap := int64(0)
	nodeLocalRatio := 0.0
	if len(channels) > 0 {
		avgCap = totalCap / int64(len(channels))
	}
	if totalCap > 0 {
		nodeLocalRatio = float64(totalLocal) / float64(totalCap)
	}

	result.Items = make([]AutofeeRefreshItem, 0, len(channels))
	for _, ch := range channels {
		item := AutofeeRefreshItem{
			ChannelID:              ch.ChannelID,
			ChannelPoint:           strings.TrimSpace(ch.ChannelPoint),
			Alias:                  strings.TrimSpace(ch.PeerAlias),
			CurrentInboundDiscount: 0,
			TargetInboundDiscount:  0,
		}
		if item.Alias == "" {
			item.Alias = strings.TrimSpace(ch.RemotePubkey)
		}
		if ch.FeeRatePpm != nil {
			item.CurrentPpm = int(*ch.FeeRatePpm)
		}
		if item.ChannelPoint == "" || ch.ChannelID == 0 {
			item.Reason = "invalid-channel"
			result.Skipped++
			result.Items = append(result.Items, item)
			continue
		}
		if !autofeeChannelEnabled(settings, ch.ChannelID) {
			item.Reason = "autofee-disabled"
			result.Skipped++
			result.Items = append(result.Items, item)
			continue
		}

		targetPpm, referencePpm, source, ok := selectAutofeeRefreshReference(
			ch.CapacitySat,
			forwardStats7d[ch.ChannelID],
			forwardStats21d[ch.ChannelID],
			rebalStats7d.ByChannel[ch.ChannelID],
			rebalStats21d.ByChannel[ch.ChannelID],
			autofeeRefreshRebalMarkup,
		)
		if !ok {
			seedPpm, seedSource, seedErr := engine.refreshSeedForChannel(ctx, strings.TrimSpace(ch.RemotePubkey))
			if seedErr != nil {
				item.Error = seedErr.Error()
				item.Reason = "seed-error"
				result.Errors++
				result.Items = append(result.Items, item)
				continue
			}
			if seedPpm > 0 {
				targetPpm = int(math.Round(seedPpm))
				referencePpm = targetPpm
				source = seedSource
				ok = true
			}
		}
		if !ok || targetPpm <= 0 {
			item.Reason = "missing-reference"
			result.Skipped++
			result.Items = append(result.Items, item)
			continue
		}

		targetPpm = clampInt(targetPpm, cfg.MinPpm, cfg.MaxPpm)
		item.TargetPpm = targetPpm
		item.ReferencePpm = referencePpm
		item.Source = source
		outboundChanged := targetPpm != item.CurrentPpm

		policy, err := s.lnd.GetChannelPolicy(ctx, item.ChannelPoint)
		if err != nil {
			item.Error = err.Error()
			item.Reason = "policy-error"
			result.Errors++
			result.Items = append(result.Items, item)
			continue
		}
		item.CurrentInboundDiscount = currentInboundDiscountFromPolicy(policy)
		item.TargetInboundDiscount = item.CurrentInboundDiscount
		if includeInbound {
			rawOutRatio := 0.5
			if ch.CapacitySat > 0 {
				rawOutRatio = float64(ch.LocalBalanceSat) / float64(ch.CapacitySat)
			}
			effectiveOutRatio, _ := effectiveChannelOutRatio(rawOutRatio, ch.LocalBalanceSat, ch.CapacitySat, avgCap, nodeLocalRatio)
			if targetInboundDiscount, inboundSource, applyInbound := selectAutofeeRefreshInboundDiscount(
				cfg,
				engine.profile,
				rawOutRatio,
				effectiveOutRatio,
				forwardStats1d[ch.ChannelID],
				forwardStats7d[ch.ChannelID],
				ch.CapacitySat,
				referencePpm,
				targetPpm,
			); applyInbound {
				item.TargetInboundDiscount = targetInboundDiscount
				item.InboundSource = inboundSource
			} else if normalizedInboundDiscount, normalized := normalizeStaleInboundDiscount(
				item.CurrentInboundDiscount,
				targetPpm,
				inboundDiscountMaxRatioFromConfig(cfg),
			); normalized {
				item.TargetInboundDiscount = normalizedInboundDiscount
				item.InboundSource = "balanced-normalize"
			}
		}
		inboundChanged := item.TargetInboundDiscount != item.CurrentInboundDiscount
		item.Changed = outboundChanged || inboundChanged

		if !item.Changed {
			item.Reason = "same"
			result.Same++
			result.Items = append(result.Items, item)
			continue
		}

		if !dryRun {
			timeLockDelta := policy.TimeLockDelta
			if timeLockDelta <= 0 {
				timeLockDelta = 144
			}
			inboundEnabled, inboundFeeRatePpm := inboundFeeUpdateForDiscount(item.CurrentInboundDiscount, item.TargetInboundDiscount)
			if err := s.lnd.UpdateChannelFees(
				ctx,
				item.ChannelPoint,
				false,
				policy.BaseFeeMsat,
				int64(targetPpm),
				timeLockDelta,
				inboundEnabled,
				0,
				inboundFeeRatePpm,
			); err != nil {
				item.Error = err.Error()
				item.Reason = "apply-error"
				result.Errors++
				result.Items = append(result.Items, item)
				continue
			}
			item.Applied = true
		}

		result.Updated++
		if inboundChanged {
			result.InboundUpdated++
		}
		result.Items = append(result.Items, item)
	}

	if entries := buildAutofeeRefreshLogEntries(cfg, runAt, result); len(entries) > 0 {
		logCtx, logCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.appendAutofeeLines(logCtx, fmt.Sprintf("%d", time.Now().UnixNano()), entries); err != nil && s.logger != nil {
			s.logger.Printf("autofee refresh: log insert failed: %v", err)
		}
		logCancel()
	}
	if !dryRun {
		s.sendTelegramAutofeeRefreshSummary(cfg, runAt, result)
	}

	return result, nil
}

func autofeeRefreshCategory(item AutofeeRefreshItem) string {
	if strings.TrimSpace(item.Error) != "" {
		return "error"
	}
	if item.Changed {
		return "changed"
	}
	if strings.TrimSpace(item.Reason) == "same" {
		return "kept"
	}
	return "skipped"
}

func formatAutofeeRefreshChannelLine(item AutofeeRefreshItem, dryRun bool) string {
	alias := strings.TrimSpace(item.Alias)
	if alias == "" {
		alias = fmt.Sprintf("chan-%d", item.ChannelID)
	}
	category := autofeeRefreshCategory(item)
	prefix := "🫤"
	switch category {
	case "changed":
		if item.TargetPpm > item.CurrentPpm {
			prefix = "✅🔺"
		} else if item.TargetPpm < item.CurrentPpm {
			prefix = "✅🔻"
		} else {
			prefix = "✅➡️"
		}
	case "skipped":
		prefix = "⏭️"
	case "error":
		prefix = "❌"
	}
	if category == "error" {
		return fmt.Sprintf("%s %s: %s", prefix, alias, strings.TrimSpace(item.Error))
	}
	action := fmt.Sprintf("keep %d ppm", item.CurrentPpm)
	if category == "changed" {
		if dryRun {
			action = fmt.Sprintf("dry-set %d→%d ppm", item.CurrentPpm, item.TargetPpm)
		} else {
			action = fmt.Sprintf("set %d→%d ppm", item.CurrentPpm, item.TargetPpm)
		}
	}
	delta := item.TargetPpm - item.CurrentPpm
	deltaPct := 0.0
	if item.CurrentPpm > 0 && item.TargetPpm != item.CurrentPpm {
		deltaPct = math.Abs(float64(delta)) / float64(item.CurrentPpm) * 100.0
	}
	deltaStr := ""
	if item.CurrentPpm > 0 && item.TargetPpm != item.CurrentPpm {
		deltaStr = fmt.Sprintf(" (%+d, %.1f%%)", delta, deltaPct)
	}
	line := fmt.Sprintf("%s %s: %s%s", prefix, alias, action, deltaStr)
	if item.ReferencePpm > 0 {
		line += fmt.Sprintf(" | ref≈%d", item.ReferencePpm)
	}
	if src := strings.TrimSpace(item.Source); src != "" {
		line += fmt.Sprintf(" | source %s", src)
	}
	if item.CurrentInboundDiscount != item.TargetInboundDiscount {
		line += fmt.Sprintf(" | ↘️ inb %d→%d", item.CurrentInboundDiscount, item.TargetInboundDiscount)
		if src := strings.TrimSpace(item.InboundSource); src != "" {
			line += fmt.Sprintf(" (%s)", src)
		}
	} else if item.TargetInboundDiscount > 0 {
		line += fmt.Sprintf(" | ↘️ inb %d", item.TargetInboundDiscount)
	}
	if category == "skipped" && strings.TrimSpace(item.Reason) != "" {
		line += fmt.Sprintf(" | %s", item.Reason)
	}
	return line
}

func buildAutofeeRefreshChannelLogEntry(item AutofeeRefreshItem, dryRun bool) autofeeLogEntry {
	category := autofeeRefreshCategory(item)
	targetPpm := item.TargetPpm
	if targetPpm <= 0 {
		targetPpm = item.CurrentPpm
	}
	delta := targetPpm - item.CurrentPpm
	deltaPct := 0.0
	if item.CurrentPpm > 0 && targetPpm != item.CurrentPpm {
		deltaPct = math.Abs(float64(delta)) / float64(item.CurrentPpm) * 100.0
	}
	payload := &autofeeLogItem{
		Kind:                   "channel",
		Category:               category,
		Reason:                 "refresh",
		DryRun:                 dryRun,
		Alias:                  item.Alias,
		ChannelID:              item.ChannelID,
		ChannelPoint:           item.ChannelPoint,
		LocalPpm:               item.CurrentPpm,
		NewPpm:                 targetPpm,
		Target:                 targetPpm,
		TargetRaw:              targetPpm,
		TargetFinal:            targetPpm,
		ReferencePpm:           item.ReferencePpm,
		RefreshSource:          item.Source,
		InboundDiscount:        item.TargetInboundDiscount,
		PrevInboundDiscount:    item.CurrentInboundDiscount,
		CurrentInboundDiscount: item.CurrentInboundDiscount,
		TargetInboundDiscount:  item.TargetInboundDiscount,
		InboundSource:          item.InboundSource,
		SkipReason:             item.Reason,
		Delta:                  delta,
		DeltaPct:               deltaPct,
	}
	if strings.TrimSpace(item.Error) != "" {
		payload.Error = item.Error
	}
	return autofeeLogEntry{Line: formatAutofeeRefreshChannelLine(item, dryRun), Payload: payload}
}

func buildAutofeeRefreshLogEntries(cfg AutofeeConfig, runAt time.Time, result AutofeeRefreshResult) []autofeeLogEntry {
	header := fmt.Sprintf("⚡ Autofee REFRESH [%s] | %s", strings.ToUpper(normalizeAutofeeOperationMode(cfg.OperationMode)), runAt.Format(time.RFC3339))
	if result.DryRun {
		header += " (dry-run)"
	}
	summaryLine := fmt.Sprintf("🔄 updated %d | same %d | skipped %d | errors %d | rebal_markup +%.0f%%", result.Updated, result.Same, result.Skipped, result.Errors, result.RebalMarkupPct)
	if result.IncludeInbound {
		summaryLine += fmt.Sprintf(" | inbound %d", result.InboundUpdated)
	}
	entries := []autofeeLogEntry{
		{Line: header, Payload: &autofeeLogItem{Kind: "header", Reason: "refresh", OperationMode: cfg.OperationMode, DryRun: result.DryRun, Timestamp: runAt.Format(time.RFC3339), IncludeInbound: result.IncludeInbound}},
		{Line: summaryLine, Payload: &autofeeLogItem{Kind: "refresh_summary", Reason: "refresh", DryRun: result.DryRun, UpdatedCount: result.Updated, SameCount: result.Same, SkippedCount: result.Skipped, ErrorCount: result.Errors, RebalMarkupPct: result.RebalMarkupPct, InboundDisc: result.InboundUpdated, IncludeInbound: result.IncludeInbound}},
	}
	changedLines := []autofeeLogEntry{}
	keptLines := []autofeeLogEntry{}
	skippedLines := []autofeeLogEntry{}
	errorLines := []autofeeLogEntry{}
	for _, item := range result.Items {
		entry := buildAutofeeRefreshChannelLogEntry(item, result.DryRun)
		switch autofeeRefreshCategory(item) {
		case "changed":
			changedLines = append(changedLines, entry)
		case "kept":
			keptLines = append(keptLines, entry)
		case "error":
			errorLines = append(errorLines, entry)
		default:
			skippedLines = append(skippedLines, entry)
		}
	}
	if len(changedLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "✅", Payload: &autofeeLogItem{Kind: "section", Category: "changed", Reason: "refresh"}})
		entries = append(entries, changedLines...)
	}
	if len(keptLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "🫤", Payload: &autofeeLogItem{Kind: "section", Category: "kept", Reason: "refresh"}})
		entries = append(entries, keptLines...)
	}
	if len(skippedLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "⏭️", Payload: &autofeeLogItem{Kind: "section", Category: "skipped", Reason: "refresh"}})
		entries = append(entries, skippedLines...)
	}
	if len(errorLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "❌", Payload: &autofeeLogItem{Kind: "section", Category: "error", Reason: "refresh"}})
		entries = append(entries, errorLines...)
	}
	return entries
}

func (e *autofeeEngine) Execute(ctx context.Context, dryRun bool, reason string) error {
	channels, err := e.svc.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}

	settings, err := e.svc.LoadChannelSettings(ctx)
	if err != nil {
		return err
	}

	state, err := e.loadState(ctx)
	if err != nil {
		return err
	}
	if snapshots, err := e.loadChannelRankingSnapshots(ctx); err == nil {
		e.ranking = snapshots
	} else if e.svc.logger != nil {
		e.svc.logger.Printf("autofee: channel ranking snapshot unavailable: %v", err)
	}
	if rebalanceCfg, err := e.svc.loadRebalanceChannelSettings(ctx); err == nil {
		e.rebalanceConfig = rebalanceCfg
	} else if e.svc.logger != nil {
		e.svc.logger.Printf("autofee: rebalance channel settings unavailable: %v", err)
	}
	if recentChanges, err := e.svc.loadRecentChangeStats(ctx, 24*time.Hour); err == nil {
		e.recentChanges = recentChanges
	} else if e.svc.logger != nil {
		e.svc.logger.Printf("autofee: recent change stats unavailable: %v", err)
	}
	if e.svc.rebalance != nil {
		if rebalanceCfg, err := e.svc.rebalance.GetConfig(ctx); err == nil {
			e.rebalanceROIMin = rebalanceCfg.ROIMin
		} else if e.svc.logger != nil {
			e.svc.logger.Printf("autofee: rebalance config unavailable: %v", err)
		}
		if overview, err := e.svc.rebalance.Overview(ctx); err == nil {
			e.rebalanceStatus = strings.TrimSpace(overview.LastScanStatus)
		} else if e.svc.logger != nil {
			e.svc.logger.Printf("autofee: rebalance overview unavailable: %v", err)
		}
		if channels, err := e.svc.rebalance.Channels(ctx); err == nil {
			snapshots := make(map[uint64]autofeeRebalanceRuntimeSnapshot, len(channels))
			for _, item := range channels {
				snapshots[item.ChannelID] = autofeeRebalanceRuntimeSnapshot{
					AutoEnabled:          item.AutoEnabled,
					ManualRestartEnabled: item.ManualRestartEnabled,
					EligibleAsTarget:     item.EligibleAsTarget,
					PaybackProgress:      item.PaybackProgress,
					ROIEstimate:          item.ROIEstimate,
					ROIEstimateValid:     item.ROIEstimateValid,
					Revenue7dSat:         item.Revenue7dSat,
					RebalanceCost7dPpm:   item.RebalanceCost7dPpm,
				}
			}
			e.rebalanceRuntime = snapshots
		} else if e.svc.logger != nil {
			e.svc.logger.Printf("autofee: rebalance channel runtime unavailable: %v", err)
		}
	}

	forwardStats, err := e.fetchForwardStats(ctx, e.cfg.LookbackDays)
	if err != nil {
		return err
	}
	idleRefreshStatsReady := true
	forwardStats1d, err := e.fetchForwardStats(ctx, 1)
	if err != nil {
		return err
	}
	forwardStats7d := forwardStats
	if e.cfg.LookbackDays != autofeeIdleRefreshWindowDays {
		if stats7d, err := e.fetchForwardStats(ctx, autofeeIdleRefreshWindowDays); err == nil {
			forwardStats7d = stats7d
		} else {
			idleRefreshStatsReady = false
			if e.svc.logger != nil {
				e.svc.logger.Printf("autofee: forwardStats7d unavailable: %v", err)
			}
		}
	}
	forwardStats21d := map[uint64]forwardStat{}
	if stats21d, err := e.fetchForwardStats(ctx, 21); err == nil {
		forwardStats21d = stats21d
	} else if e.svc.logger != nil {
		e.svc.logger.Printf("autofee: forwardStats21d unavailable: %v", err)
	}
	inboundStats, err := e.fetchInboundStats(ctx, e.cfg.LookbackDays)
	if err != nil {
		return err
	}
	inboundStats7d := inboundStats
	if e.cfg.LookbackDays != autofeeIdleRefreshWindowDays {
		if stats7d, err := e.fetchInboundStats(ctx, autofeeIdleRefreshWindowDays); err == nil {
			inboundStats7d = stats7d
		} else {
			idleRefreshStatsReady = false
			if e.svc.logger != nil {
				e.svc.logger.Printf("autofee: inboundStats7d unavailable: %v", err)
			}
		}
	}
	rebalStats, err := e.fetchRebalanceStats(ctx, e.cfg.LookbackDays)
	if err != nil {
		return err
	}
	rebalStats7d := rebalStats
	if e.cfg.LookbackDays != autofeeIdleRefreshWindowDays {
		if stats7d, err := e.fetchRebalanceStats(ctx, autofeeIdleRefreshWindowDays); err == nil {
			rebalStats7d = stats7d
		} else {
			idleRefreshStatsReady = false
			if e.svc.logger != nil {
				e.svc.logger.Printf("autofee: rebalStats7d unavailable: %v", err)
			}
		}
	}
	rebalMovementStats7d := rebalStats7d.ByChannel
	if e.cfg.IdleRefreshEnabled && normalizeAutofeeOperationMode(e.cfg.OperationMode) == autofeeOperationModeBalanced {
		if movementStats, err := e.fetchRebalanceMovementStats(ctx, autofeeIdleRefreshWindowDays); err == nil {
			rebalMovementStats7d = movementStats
		} else {
			idleRefreshStatsReady = false
			if e.svc.logger != nil {
				e.svc.logger.Printf("autofee: rebalMovementStats7d unavailable: %v", err)
			}
		}
	}
	rebalStats21d := rebalStats
	if stats21d, err := e.fetchRebalanceStats(ctx, 21); err == nil {
		rebalStats21d = stats21d
	} else if e.svc.logger != nil {
		e.svc.logger.Printf("autofee: rebalStats21d unavailable: %v", err)
	}
	recentRebalanceTouches := map[uint64]recentRebalanceSignal{}
	if touches, err := e.fetchRecentRebalanceTouches(ctx, e.htlcSignalWindow(), e.rebalanceFailureSignalWindow()); err == nil {
		recentRebalanceTouches = touches
	} else if e.svc.logger != nil {
		e.svc.logger.Printf("autofee: recent rebalance touches unavailable: %v", err)
	}

	totalOutFeeMsat := int64(0)
	totalOutAmtMsat := int64(0)
	for _, item := range forwardStats {
		totalOutFeeMsat += item.FeeMsat
		totalOutAmtMsat += item.AmtMsat
	}
	rebalGlobal := rebalStats.Global
	rebalGlobalPpm := ppmMsat(rebalGlobal.FeeMsat, rebalGlobal.AmtMsat)
	outPpmTotal := ppmMsat(totalOutFeeMsat, totalOutAmtMsat)
	negMarginGlobal := rebalGlobalPpm > 0 && outPpmTotal > 0 && outPpmTotal < rebalGlobalPpm
	e.calibrateNode(channels, state, forwardStats)
	htlcSignals, htlcMeta := e.buildHTLCFailureSignals(channels)

	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	header := fmt.Sprintf("⚡ Autofee %s [%s] | %s", strings.ToUpper(reason), strings.ToUpper(e.cfg.OperationMode), e.now.UTC().Format(time.RFC3339))
	if dryRun {
		header = header + " (dry-run)"
	}
	summary := autofeeRunSummary{total: len(channels)}
	summary.htlcWindowMin = htlcMeta.WindowMin
	summary.htlcMinAttempts = htlcMeta.MinAttempts
	summary.htlcMinPolicyFails = htlcMeta.MinPolicyFails
	summary.htlcMinLiquidityFails = htlcMeta.MinLiquidityFails
	summary.htlcMinForwardFails = htlcMeta.MinForwardFails
	summary.htlcAttemptsTotal = htlcMeta.AttemptsTotal
	summary.htlcLinkFailsTotal = htlcMeta.LinkFailsTotal
	summary.htlcForwardFailsTotal = htlcMeta.ForwardFailsTotal
	summary.htlcOtherFailsTotal = htlcMeta.OtherFailsTotal
	summary.htlcClassifiedTotal = htlcMeta.ClassifiedTotal
	summary.htlcClassifiedRatio = htlcMeta.ClassifiedRatio
	summary.htlcUnclassifiedTotal = htlcMeta.UnclassifiedTotal
	summary.htlcNoisySampleApplied = htlcMeta.NoisySampleApplied
	summary.htlcTopReasons = append([]string{}, htlcMeta.TopReasons...)
	summary.htlcPolicyRateMin = htlcMeta.PolicyRateMin
	summary.htlcLiquidityRateMin = htlcMeta.LiquidityRateMin
	summary.htlcForwardRateMin = htlcMeta.ForwardRateMin
	summary.htlcGlobalCountFactor = htlcMeta.GlobalCountFactor
	summary.htlcGlobalRateFactor = htlcMeta.GlobalRateFactor
	summary.htlcNodeFactor = htlcMeta.NodeFactor
	summary.htlcLiquidityFactor = htlcMeta.LiquidityFactor
	summary.htlcThresholdFactor = htlcMeta.ThresholdFactor
	changedDecisions := []*decision{}
	changedLines := []autofeeLogEntry{}
	keptLines := []autofeeLogEntry{}
	skippedLines := []autofeeLogEntry{}
	errorLines := []autofeeLogEntry{}
	explorerLines := []autofeeLogEntry{}
	for _, ch := range channels {
		if !ch.Active {
			summary.inactive++
			continue
		}
		if ch.ChannelID == 0 {
			continue
		}
		if !autofeeChannelEnabled(settings, ch.ChannelID) {
			summary.disabled++
			continue
		}

		st := state[ch.ChannelID]
		decision := e.evaluateChannel(ch, st, forwardStats, forwardStats1d, forwardStats7d, forwardStats21d, inboundStats, rebalStats, rebalStats21d, recentRebalanceTouches, htlcSignals, totalOutFeeMsat, rebalGlobalPpm, negMarginGlobal)
		if decision == nil {
			continue
		}
		if decision.Alias == "" {
			decision.Alias = strings.TrimSpace(ch.RemotePubkey)
		}
		if idleRefreshStatsReady {
			decision = e.maybeApplyIdleRefresh(
				ctx,
				ch,
				decision,
				forwardStats7d[ch.ChannelID],
				forwardStats21d[ch.ChannelID],
				inboundStats7d[ch.ChannelID],
				rebalMovementStats7d[ch.ChannelID],
				rebalStats7d.ByChannel[ch.ChannelID],
				rebalStats21d.ByChannel[ch.ChannelID],
			)
		}
		summary.eligible++
		summary.addTags(decision.Tags)
		if decision.SuperSourceActive {
			summary.superSource++
		}
		if decision.InboundDiscount > 0 {
			summary.inboundDiscount++
		}

		e.persistState(ctx, decision.State)

		if decision.Apply {
			summary.applied++
			if decision.NewPpm > decision.LocalPpm {
				summary.changedUp++
			} else if decision.NewPpm < decision.LocalPpm {
				summary.changedDown++
			} else {
				summary.kept++
			}
			if dryRun {
				changedLines = append(changedLines, buildAutofeeChannelLogEntry(decision, "changed", true, nil))
				continue
			}
			if err := e.applyDecision(ctx, ch, decision); err != nil {
				summary.applyErrors++
				errorLines = append(errorLines, buildAutofeeChannelLogEntry(decision.withError(err), "error", dryRun, err))
				if st := decision.State; st != nil {
					st.ApplyErrorStreak++
					st.LastApplyError = err.Error()
					crossedThreshold := st.ApplyErrorStreak >= autofeeApplyErrorAlertThreshold && !st.ApplyErrorAlerted
					if crossedThreshold {
						st.ApplyErrorAlerted = true
					}
					e.persistApplyErrorState(ctx, st.ChannelID, st.ApplyErrorStreak, st.LastApplyError, st.ApplyErrorAlerted)
					if crossedThreshold {
						go e.svc.sendTelegramAutofeeApplyAlert(decision.Alias, decision.ChannelPoint, st.ApplyErrorStreak, st.LastApplyError)
					}
				}
				continue
			}
			if st := decision.State; st != nil && (st.ApplyErrorStreak > 0 || st.ApplyErrorAlerted || st.LastApplyError != "") {
				st.ApplyErrorStreak = 0
				st.LastApplyError = ""
				st.ApplyErrorAlerted = false
				e.persistApplyErrorState(ctx, st.ChannelID, 0, "", false)
			}
			changedDecisions = append(changedDecisions, decision)
			changedLines = append(changedLines, buildAutofeeChannelLogEntry(decision, "changed", dryRun, nil))
			e.recordOutcome(ctx, runID, decision, e.now)
		} else {
			if decision.NewPpm == decision.LocalPpm {
				summary.kept++
			}
			cat := "kept"
			if hasAutofeeSkipTag(decision.Tags) {
				cat = "skipped"
			}
			entry := buildAutofeeChannelLogEntry(decision, cat, dryRun, nil)
			if cat == "skipped" {
				skippedLines = append(skippedLines, entry)
			} else {
				keptLines = append(keptLines, entry)
			}
			if containsTag(decision.Tags, "explorer") || containsTag(decision.Tags, "drained-explorer") {
				label := "explorer"
				if containsTag(decision.Tags, "drained-explorer") && !containsTag(decision.Tags, "explorer") {
					label = "drained explorer"
				}
				explorerLines = append(explorerLines, autofeeLogEntry{
					Line: fmt.Sprintf("🧭 %s %s: ON", decision.Alias, label),
					Payload: &autofeeLogItem{
						Kind:     "explorer",
						Category: "explorer",
						Alias:    decision.Alias,
					},
				})
			}
		}
	}

	summaryText := fmt.Sprintf(
		"📊 up %d | down %d | flat %d | cooldown %d | small %d | same %d | disabled %d | inactive %d | inb_disc %d | super_source %d | htlc_liq_hot %d | htlc_policy_hot %d | htlc_forward_hot %d | htlc_low_sample %d | reversal_blocked %d | reversal_confirmed %d | downcap_general %d | downcap_low_sample %d | floor_relax %d | stall_alert %d | htlc_window %dm | htlc_min a>=%d p>=%d l>=%d f>=%d | htlc_rate p>=%.1f%% l>=%.1f%% f>=%.1f%% | htlc_global c×%.2f r×%.2f | htlc_events link=%d forward=%d other=%d | htlc_classified %d/%d | htlc_unclassified %d",
		summary.changedUp, summary.changedDown, summary.kept,
		summary.skippedCooldown, summary.skippedSmall, summary.skippedSame,
		summary.disabled, summary.inactive, summary.inboundDiscount, summary.superSource,
		summary.htlcLiqHot, summary.htlcPolicyHot, summary.htlcForwardHot, summary.htlcSampleLow,
		summary.reversalBlocked, summary.reversalConfirmed, summary.downcapGeneral, summary.downcapLowSample, summary.floorRelaxApplied, summary.stallAlert,
		summary.htlcWindowMin,
		summary.htlcMinAttempts, summary.htlcMinPolicyFails, summary.htlcMinLiquidityFails, summary.htlcMinForwardFails,
		summary.htlcPolicyRateMin*100, summary.htlcLiquidityRateMin*100,
		summary.htlcForwardRateMin*100,
		summary.htlcGlobalCountFactor, summary.htlcGlobalRateFactor,
		summary.htlcLinkFailsTotal, summary.htlcForwardFailsTotal, summary.htlcOtherFailsTotal,
		summary.htlcClassifiedTotal, summary.htlcAttemptsTotal, summary.htlcUnclassifiedTotal,
	)
	seedText := fmt.Sprintf(
		"🌱 seed amboss=%d missing=%d err=%d empty=%d outrate=%d mem=%d default=%d",
		summary.seedAmboss+summary.seedNative, summary.seedAmbossMissing+summary.seedNativeInsufficient, summary.seedAmbossError+summary.seedNativeError, summary.seedAmbossEmpty,
		summary.seedOutrate, summary.seedMem, summary.seedDefault,
	)
	if e.ignoreCooldown {
		seedText = seedText + " | cooldown_ignored=1"
	}

	entries := []autofeeLogEntry{
		{Line: header, Payload: &autofeeLogItem{Kind: "header", Reason: reason, OperationMode: e.cfg.OperationMode, DryRun: dryRun, Timestamp: e.now.UTC().Format(time.RFC3339)}},
		{Line: summaryText, Payload: &autofeeLogItem{
			Kind:                     "summary",
			Up:                       summary.changedUp,
			Down:                     summary.changedDown,
			Flat:                     summary.kept,
			Cooldown:                 summary.skippedCooldown,
			Small:                    summary.skippedSmall,
			Same:                     summary.skippedSame,
			Disabled:                 summary.disabled,
			Inactive:                 summary.inactive,
			InboundDisc:              summary.inboundDiscount,
			SuperSource:              summary.superSource,
			HTLCLiqHot:               summary.htlcLiqHot,
			HTLCPolicyHot:            summary.htlcPolicyHot,
			HTLCForwardHot:           summary.htlcForwardHot,
			HTLCSampleLow:            summary.htlcSampleLow,
			ReversalBlocked:          summary.reversalBlocked,
			ReversalConfirmed:        summary.reversalConfirmed,
			DowncapGeneral:           summary.downcapGeneral,
			DowncapLowSample:         summary.downcapLowSample,
			FloorRelaxApplied:        summary.floorRelaxApplied,
			StallAlert:               summary.stallAlert,
			HTLCAttemptsTotal:        summary.htlcAttemptsTotal,
			HTLCLinkFailsTotal:       summary.htlcLinkFailsTotal,
			HTLCForwardFailsTotal:    summary.htlcForwardFailsTotal,
			HTLCOtherFailsTotal:      summary.htlcOtherFailsTotal,
			HTLCClassifiedTotal:      summary.htlcClassifiedTotal,
			HTLCClassifiedRatio:      summary.htlcClassifiedRatio,
			HTLCUnclassifiedTotal:    summary.htlcUnclassifiedTotal,
			HTLCNoisySampleApplied:   summary.htlcNoisySampleApplied,
			HTLCTopReasons:           append([]string{}, summary.htlcTopReasons...),
			HTLCWindowMin:            summary.htlcWindowMin,
			HTLCMinAttempts:          summary.htlcMinAttempts,
			HTLCMinPolicyFails:       summary.htlcMinPolicyFails,
			HTLCMinLiquidityFails:    summary.htlcMinLiquidityFails,
			HTLCMinForwardFails:      summary.htlcMinForwardFails,
			HTLCPolicyFailRateMin:    summary.htlcPolicyRateMin,
			HTLCLiquidityFailRateMin: summary.htlcLiquidityRateMin,
			HTLCForwardFailRateMin:   summary.htlcForwardRateMin,
			HTLCGlobalCountFactor:    summary.htlcGlobalCountFactor,
			HTLCGlobalRateFactor:     summary.htlcGlobalRateFactor,
			HTLCNodeFactor:           summary.htlcNodeFactor,
			HTLCLiquidityFactor:      summary.htlcLiquidityFactor,
			HTLCThresholdFactor:      summary.htlcThresholdFactor,
		}},
		{Line: seedText, Payload: &autofeeLogItem{
			Kind:               "seed",
			Native:             summary.seedNative,
			NativeInsufficient: summary.seedNativeInsufficient,
			NativeErr:          summary.seedNativeError,
			Amboss:             summary.seedAmboss,
			Missing:            summary.seedAmbossMissing,
			Err:                summary.seedAmbossError,
			Empty:              summary.seedAmbossEmpty,
			Outrate:            summary.seedOutrate,
			Mem:                summary.seedMem,
			Default:            summary.seedDefault,
			CooldownIgnored:    e.ignoreCooldown,
		}},
	}
	calibLine := fmt.Sprintf("⚙️ calib node=%s channels=%d cap=%d avg=%d local=%d (%.0f%%) revfloor_thr=%d revfloor_min=%d liq=%s | low_out x%.2f t<%.1f%% p<%.1f%% | htlc_k node=%.2f liq=%.2f total=%.2f | htlc_rate p>=%.1f%% l>=%.1f%% f>=%.1f%% | htlc_global c×%.2f r×%.2f",
		e.calib.NodeClass, e.calib.ChannelCount, e.calib.TotalCapacitySat, e.calib.AvgCapacitySat,
		e.calib.LocalCapacitySat, e.calib.LocalRatio*100, e.calib.RevfloorBaseline, e.calib.RevfloorMinAbs, e.calib.LiquidityClass,
		e.calib.LowOutFactor, e.calib.LowOutThresh*100, e.calib.LowOutProtectThresh*100,
		e.calib.HTLCNodeFactor, e.calib.HTLCLiquidityFactor, e.calib.HTLCThresholdFactor,
		e.calib.HTLCPolicyRateMin*100, e.calib.HTLCLiquidityRateMin*100,
		e.calib.HTLCForwardRateMin*100,
		e.calib.HTLCGlobalCountFactor, e.calib.HTLCGlobalRateFactor,
	)
	entries = append(entries, autofeeLogEntry{
		Line: calibLine,
		Payload: &autofeeLogItem{
			Kind:                     "calib",
			NodeClass:                e.calib.NodeClass,
			LiquidityClass:           e.calib.LiquidityClass,
			ChannelCount:             e.calib.ChannelCount,
			TotalCapacitySat:         e.calib.TotalCapacitySat,
			AvgCapacitySat:           e.calib.AvgCapacitySat,
			LocalCapacitySat:         e.calib.LocalCapacitySat,
			LocalRatio:               e.calib.LocalRatio,
			RevfloorBaseline:         e.calib.RevfloorBaseline,
			RevfloorMinAbs:           e.calib.RevfloorMinAbs,
			HTLCWindowMin:            e.calib.HTLCWindowMin,
			HTLCMinAttempts:          e.calib.HTLCMinAttempts,
			HTLCMinPolicyFails:       e.calib.HTLCMinPolicyFails,
			HTLCMinLiquidityFails:    e.calib.HTLCMinLiquidityFails,
			HTLCMinForwardFails:      e.calib.HTLCMinForwardFails,
			HTLCPolicyFailRateMin:    e.calib.HTLCPolicyRateMin,
			HTLCLiquidityFailRateMin: e.calib.HTLCLiquidityRateMin,
			HTLCForwardFailRateMin:   e.calib.HTLCForwardRateMin,
			HTLCClassifiedRatio:      e.calib.HTLCClassifiedRatio,
			HTLCNoisySampleApplied:   e.calib.HTLCNoisySample,
			HTLCGlobalCountFactor:    e.calib.HTLCGlobalCountFactor,
			HTLCGlobalRateFactor:     e.calib.HTLCGlobalRateFactor,
			HTLCNodeFactor:           e.calib.HTLCNodeFactor,
			HTLCLiquidityFactor:      e.calib.HTLCLiquidityFactor,
			HTLCThresholdFactor:      e.calib.HTLCThresholdFactor,
			LowOutThresh:             e.calib.LowOutThresh,
			LowOutProtectThresh:      e.calib.LowOutProtectThresh,
			LowOutFactor:             e.calib.LowOutFactor,
		},
	})
	if summary.htlcAttemptsTotal > 0 {
		diagParts := []string{
			fmt.Sprintf("htlc_classified %d/%d", summary.htlcClassifiedTotal, summary.htlcAttemptsTotal),
			fmt.Sprintf("htlc_forward %d", summary.htlcForwardFailsTotal),
			fmt.Sprintf("htlc_unclassified %d", summary.htlcUnclassifiedTotal),
		}
		diagParts = append(diagParts, fmt.Sprintf("htlc_classified_ratio %.1f%%", summary.htlcClassifiedRatio*100))
		if summary.htlcNoisySampleApplied {
			diagParts = append(diagParts, "htlc_noisy_sample")
		}
		if len(summary.htlcTopReasons) > 0 {
			diagParts = append(diagParts, "htlc_unclassified_top "+strings.Join(summary.htlcTopReasons, " | "))
		}
		entries = append(entries, autofeeLogEntry{
			Line: "🧪 " + strings.Join(diagParts, " | "),
			Payload: &autofeeLogItem{
				Kind:                   "htlc_diag",
				Category:               "htlc_diag",
				HTLCTopReasons:         append([]string{}, summary.htlcTopReasons...),
				HTLCForwardFailsTotal:  summary.htlcForwardFailsTotal,
				HTLCLinkFailsTotal:     summary.htlcLinkFailsTotal,
				HTLCOtherFailsTotal:    summary.htlcOtherFailsTotal,
				HTLCUnclassifiedTotal:  summary.htlcUnclassifiedTotal,
				HTLCClassifiedTotal:    summary.htlcClassifiedTotal,
				HTLCClassifiedRatio:    summary.htlcClassifiedRatio,
				HTLCNoisySampleApplied: summary.htlcNoisySampleApplied,
				HTLCAttemptsTotal:      summary.htlcAttemptsTotal,
				HTLCWindowMin:          summary.htlcWindowMin,
			},
		})
	}

	if len(changedLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "✅", Payload: &autofeeLogItem{Kind: "section", Category: "changed"}})
		entries = append(entries, changedLines...)
	}
	if len(keptLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "🫤", Payload: &autofeeLogItem{Kind: "section", Category: "kept"}})
		entries = append(entries, keptLines...)
	}
	if len(skippedLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "⏭️", Payload: &autofeeLogItem{Kind: "section", Category: "skipped"}})
		entries = append(entries, skippedLines...)
	}
	if len(explorerLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "🧭", Payload: &autofeeLogItem{Kind: "section", Category: "explorer"}})
		entries = append(entries, explorerLines...)
	}
	if len(errorLines) > 0 {
		entries = append(entries, autofeeLogEntry{Line: "❌", Payload: &autofeeLogItem{Kind: "section", Category: "error"}})
		entries = append(entries, errorLines...)
	}

	logCtx, logCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer logCancel()
	if err := e.svc.appendAutofeeLines(logCtx, runID, entries); err != nil {
		e.svc.logger.Printf("autofee: log insert failed: %v", err)
	}
	if !dryRun && len(changedDecisions) > 0 {
		e.svc.sendTelegramAutofeeRunSummary(e.cfg, reason, e.now, summary, e.calib, changedDecisions)
	}
	return nil
}

type forwardStat struct {
	FeeMsat int64
	AmtMsat int64
	Count   int64
}

type inboundStat struct {
	AmtMsat int64
	Count   int64
}

type rebalStat struct {
	FeeMsat int64
	AmtMsat int64
	Count   int64
}

type rebalStats struct {
	ByChannel map[uint64]rebalStat
	Global    rebalStat
}

type recentRebalanceSignal struct {
	Count      int
	AmtSat     int64
	FeeMsat    int64
	LastAt     time.Time
	WeakCount  int
	WeakAmtSat int64
	WeakLastAt time.Time
}

type recentWeakRebalanceJob struct {
	ChannelID uint64
	Ts        time.Time
	AmtSat    int64
}

func (s recentRebalanceSignal) surgeConfirmInputs() (int, int64) {
	// Success protects cost basis; only unresolved failed campaigns confirm a surge.
	if s.WeakCount <= 0 {
		return 0, 0
	}
	if s.Count > 0 && !s.LastAt.IsZero() && (s.WeakLastAt.IsZero() || !s.WeakLastAt.After(s.LastAt)) {
		return 0, 0
	}
	weightedCount := float64(s.WeakCount) * weakRebalanceAttemptCountWeight
	weightedAmt := float64(s.WeakAmtSat) * weakRebalanceAttemptAmtWeight
	return int(math.Round(weightedCount)), int64(math.Round(weightedAmt))
}

func (s recentRebalanceSignal) costPpm() int {
	return ppmMsat(s.FeeMsat, s.AmtSat*1000)
}

func (s recentRebalanceSignal) latestAt() time.Time {
	latest := time.Time{}
	if s.Count > 0 && !s.LastAt.IsZero() {
		latest = s.LastAt
	}
	if s.WeakCount > 0 && !s.WeakLastAt.IsZero() && (latest.IsZero() || s.WeakLastAt.After(latest)) {
		latest = s.WeakLastAt
	}
	return latest
}

func shouldHoldAutofeeForRebalanceSettling(sig recentRebalanceSignal, now time.Time, window time.Duration) bool {
	if window <= 0 {
		return false
	}
	latest := sig.latestAt()
	if latest.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if latest.After(now) {
		return true
	}
	return now.Sub(latest) <= window
}

func collapseWeakRebalanceCampaigns(jobs []recentWeakRebalanceJob, campaignGap time.Duration) map[uint64]recentRebalanceSignal {
	out := map[uint64]recentRebalanceSignal{}
	if len(jobs) == 0 {
		return out
	}
	if campaignGap <= 0 {
		campaignGap = 90 * time.Minute
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].ChannelID != jobs[j].ChannelID {
			return jobs[i].ChannelID < jobs[j].ChannelID
		}
		return jobs[i].Ts.Before(jobs[j].Ts)
	})
	var currentChan uint64
	var lastTs time.Time
	var campaignLastTs time.Time
	var campaignAmt int64
	flush := func() {
		if currentChan == 0 {
			return
		}
		sig := out[currentChan]
		sig.WeakCount++
		sig.WeakAmtSat += campaignAmt
		if !campaignLastTs.IsZero() && (sig.WeakLastAt.IsZero() || campaignLastTs.After(sig.WeakLastAt)) {
			sig.WeakLastAt = campaignLastTs
		}
		out[currentChan] = sig
	}
	for _, job := range jobs {
		if job.ChannelID == 0 || job.Ts.IsZero() {
			continue
		}
		if currentChan == 0 || job.ChannelID != currentChan || job.Ts.Sub(lastTs) > campaignGap {
			flush()
			currentChan = job.ChannelID
			lastTs = job.Ts
			campaignLastTs = job.Ts
			campaignAmt = job.AmtSat
			continue
		}
		lastTs = job.Ts
		campaignLastTs = job.Ts
		if job.AmtSat > campaignAmt {
			campaignAmt = job.AmtSat
		}
	}
	flush()
	return out
}

type htlcFailureSignal struct {
	Attempts60m          int
	LinkFails60m         int
	ForwardFails60m      int
	PolicyFails60m       int
	LiquidityFails60m    int
	UnclassifiedFails60m int
	ForwardFailRate60m   float64
	PolicyFailRate60m    float64
	LiquidityFailRate60m float64
	WindowMin            int
	SampleLow            bool
	NoisySample          bool
	PolicyHot            bool
	LiquidityHot         bool
	ForwardHot           bool
}

type htlcSignalMeta struct {
	WindowMin          int
	MinAttempts        int
	MinPolicyFails     int
	MinLiquidityFails  int
	MinForwardFails    int
	AttemptsTotal      int
	LinkFailsTotal     int
	ForwardFailsTotal  int
	OtherFailsTotal    int
	ClassifiedTotal    int
	ClassifiedRatio    float64
	UnclassifiedTotal  int
	NoisySampleApplied bool
	TopReasons         []string
	PolicyRateMin      float64
	LiquidityRateMin   float64
	ForwardRateMin     float64
	GlobalCountFactor  float64
	GlobalRateFactor   float64
	NodeFactor         float64
	LiquidityFactor    float64
	ThresholdFactor    float64
}

func applyHTLCSignalMetaToCalibration(calib *autofeeCalibration, meta htlcSignalMeta) {
	if calib == nil {
		return
	}
	calib.HTLCWindowMin = meta.WindowMin
	calib.HTLCMinAttempts = meta.MinAttempts
	calib.HTLCMinPolicyFails = meta.MinPolicyFails
	calib.HTLCMinLiquidityFails = meta.MinLiquidityFails
	calib.HTLCMinForwardFails = meta.MinForwardFails
	calib.HTLCPolicyRateMin = meta.PolicyRateMin
	calib.HTLCLiquidityRateMin = meta.LiquidityRateMin
	calib.HTLCForwardRateMin = meta.ForwardRateMin
	calib.HTLCClassifiedRatio = meta.ClassifiedRatio
	calib.HTLCNoisySample = meta.NoisySampleApplied
	calib.HTLCGlobalCountFactor = meta.GlobalCountFactor
	calib.HTLCGlobalRateFactor = meta.GlobalRateFactor
	calib.HTLCNodeFactor = meta.NodeFactor
	calib.HTLCLiquidityFactor = meta.LiquidityFactor
	calib.HTLCThresholdFactor = meta.ThresholdFactor
}

func applyHTLCNoisySampleDampening(attemptsTotal int, classifiedTotal int, minForwardFails int, forwardRateThreshold float64) (int, float64, bool, float64) {
	if attemptsTotal <= 0 {
		return minForwardFails, forwardRateThreshold, false, 0
	}
	classifiedRatio := float64(classifiedTotal) / float64(attemptsTotal)
	if classifiedRatio >= htlcClassifiedMinRatio {
		return minForwardFails, forwardRateThreshold, false, classifiedRatio
	}
	dampenedFails := int(math.Ceil(float64(maxInt(1, minForwardFails)) * htlcNoisySampleForwardCountMult))
	dampenedRate := forwardRateThreshold
	if dampenedRate > 0 {
		dampenedRate = math.Min(1, dampenedRate*htlcNoisySampleForwardRateMult)
	}
	return maxInt(minForwardFails, dampenedFails), dampenedRate, true, classifiedRatio
}

func (e *autofeeEngine) buildHTLCFailureSignals(channels []lndclient.ChannelInfo) (map[uint64]htlcFailureSignal, htlcSignalMeta) {
	signals := map[uint64]htlcFailureSignal{}
	window := e.htlcSignalWindow()
	windowMin := int(window / time.Minute)
	nodeFactor, liquidityFactor, thresholdFactor := e.htlcThresholdFactors()
	minAttempts := scaleHTLCThresholdByWindow(e.profile.HTLCMinAttempts60m, windowMin, 12)
	minPolicyFails := scaleHTLCThresholdByWindow(e.profile.HTLCPolicyMinFails, windowMin, 3)
	minLiquidityFails := scaleHTLCThresholdByWindow(e.profile.HTLCLiquidityMinFails, windowMin, 4)
	minAttempts = applyHTLCThresholdFactor(minAttempts, thresholdFactor)
	minPolicyFails = applyHTLCThresholdFactor(minPolicyFails, thresholdFactor)
	minLiquidityFails = applyHTLCThresholdFactor(minLiquidityFails, thresholdFactor)
	minAttempts = applyHTLCGlobalCountFactor(minAttempts, htlcGlobalMinCountFactor, htlcGlobalMinAttemptsFloor)
	minPolicyFails = applyHTLCGlobalCountFactor(minPolicyFails, htlcGlobalMinCountFactor, htlcGlobalMinFailsFloor)
	minLiquidityFails = applyHTLCGlobalCountFactor(minLiquidityFails, htlcGlobalMinCountFactor, htlcGlobalMinFailsFloor)
	minForwardFails := applyHTLCGlobalCountFactor(
		int(math.Round(float64(maxInt(1, minLiquidityFails))*htlcForwardSoftCountFactor)),
		htlcGlobalMinCountFactor,
		htlcForwardSoftMinFailsFloor,
	)
	// Policy/liquidity rates are measured against link-fail population only.
	// Keep per-signal gates to avoid overreacting to tiny samples while preserving
	// the configured sensitivity for each signal.
	minPolicyLinkFails := maxInt(1, minPolicyFails)
	minLiquidityLinkFails := maxInt(1, minLiquidityFails)
	policyRateThreshold := applyHTLCGlobalRateFactor(e.profile.HTLCPolicyFailRate, htlcGlobalRateFactor, htlcGlobalPolicyRateFloor)
	liquidityRateThreshold := applyHTLCGlobalRateFactor(e.profile.HTLCLiquidityFailRate, htlcGlobalRateFactor, htlcGlobalLiquidityRateFloor)
	forwardRateThresholdBase := applyHTLCGlobalRateFactor(
		e.profile.HTLCLiquidityFailRate*htlcForwardSoftRateFactor,
		htlcGlobalRateFactor,
		htlcForwardSoftRateFloor,
	)
	// Keep forward-hot proportional to node/liquidity profile scaling too.
	forwardRateThreshold := applyHTLCGlobalRateFactor(
		forwardRateThresholdBase,
		thresholdFactor,
		htlcForwardSoftRateFloor,
	)
	meta := htlcSignalMeta{
		WindowMin:         windowMin,
		MinAttempts:       minAttempts,
		MinPolicyFails:    minPolicyFails,
		MinLiquidityFails: minLiquidityFails,
		MinForwardFails:   minForwardFails,
		PolicyRateMin:     policyRateThreshold,
		LiquidityRateMin:  liquidityRateThreshold,
		ForwardRateMin:    forwardRateThreshold,
		GlobalCountFactor: htlcGlobalMinCountFactor,
		GlobalRateFactor:  htlcGlobalRateFactor,
		NodeFactor:        nodeFactor,
		LiquidityFactor:   liquidityFactor,
		ThresholdFactor:   thresholdFactor,
	}
	applyHTLCSignalMetaToCalibration(&e.calib, meta)
	if !e.cfg.HTLCSignalEnabled {
		return signals, meta
	}
	if e.svc.htlcFailedProvider == nil {
		return signals, meta
	}

	entries := e.svc.htlcFailedProvider.Failed(htlcManagerMaxLogLimit)
	if len(entries) == 0 {
		return signals, meta
	}

	attempts := map[uint64]int{}
	linkFails := map[uint64]int{}
	forwardFails := map[uint64]int{}
	policyFails := map[uint64]int{}
	liquidityFails := map[uint64]int{}
	unclassifiedFails := map[uint64]int{}
	unclassifiedReasons := map[string]int{}
	attemptsTotal := 0
	linkFailsTotal := 0
	forwardFailsTotal := 0
	otherFailsTotal := 0
	classifiedTotal := 0
	unclassifiedTotal := 0
	cutoff := e.now.Add(-window)

	for _, entry := range entries {
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Timestamp))
		if err != nil || ts.IsZero() {
			continue
		}
		if ts.Before(cutoff) {
			continue
		}
		chanID, ok := parseShortChannelID(entry.OutgoingChannelID)
		if !ok || chanID == 0 {
			continue
		}
		attempts[chanID]++
		attemptsTotal++
		switch normalizeHTLCFailureEvent(entry) {
		case "forward_fail":
			forwardFails[chanID]++
			forwardFailsTotal++
		case "link_fail":
			linkFails[chanID]++
			linkFailsTotal++
			policy, liquidity := classifyHTLCFailure(entry)
			if policy {
				policyFails[chanID]++
			}
			if liquidity {
				liquidityFails[chanID]++
			}
			if policy || liquidity {
				classifiedTotal++
			} else {
				unclassifiedFails[chanID]++
				unclassifiedTotal++
				reasonKey := htlcFailureReasonKey(entry)
				unclassifiedReasons[reasonKey]++
			}
		default:
			otherFailsTotal++
			unclassifiedFails[chanID]++
			unclassifiedTotal++
			reasonKey := htlcFailureReasonKey(entry)
			unclassifiedReasons[reasonKey]++
		}
	}
	meta.AttemptsTotal = attemptsTotal
	meta.LinkFailsTotal = linkFailsTotal
	meta.ForwardFailsTotal = forwardFailsTotal
	meta.OtherFailsTotal = otherFailsTotal
	meta.ClassifiedTotal = classifiedTotal
	meta.UnclassifiedTotal = unclassifiedTotal
	meta.TopReasons = summarizeTopReasonCounts(unclassifiedReasons, 3)
	minForwardFails, forwardRateThreshold, meta.NoisySampleApplied, meta.ClassifiedRatio = applyHTLCNoisySampleDampening(
		attemptsTotal,
		classifiedTotal,
		minForwardFails,
		forwardRateThreshold,
	)
	meta.MinForwardFails = minForwardFails
	meta.ForwardRateMin = forwardRateThreshold
	applyHTLCSignalMetaToCalibration(&e.calib, meta)

	for _, ch := range channels {
		if ch.ChannelID == 0 {
			continue
		}
		total := attempts[ch.ChannelID]
		if total == 0 {
			continue
		}
		lnkFails := linkFails[ch.ChannelID]
		fwdFails := forwardFails[ch.ChannelID]
		polFails := policyFails[ch.ChannelID]
		liqFails := liquidityFails[ch.ChannelID]
		uncFails := unclassifiedFails[ch.ChannelID]
		linkBase := maxInt(1, lnkFails)
		polRate := float64(polFails) / float64(linkBase)
		liqRate := float64(liqFails) / float64(linkBase)
		fwdRate := float64(fwdFails) / float64(total)
		sampleLow := total < minAttempts
		policyLinkGateMet := lnkFails >= minPolicyLinkFails
		liquidityLinkGateMet := lnkFails >= minLiquidityLinkFails
		policyHot := !sampleLow &&
			policyLinkGateMet &&
			polFails >= minPolicyFails &&
			polRate >= policyRateThreshold
		liquidityHot := !sampleLow &&
			liquidityLinkGateMet &&
			liqFails >= minLiquidityFails &&
			liqRate >= liquidityRateThreshold
		forwardHot := !sampleLow &&
			fwdFails >= minForwardFails &&
			fwdRate >= forwardRateThreshold
		signals[ch.ChannelID] = htlcFailureSignal{
			Attempts60m:          total,
			LinkFails60m:         lnkFails,
			ForwardFails60m:      fwdFails,
			PolicyFails60m:       polFails,
			LiquidityFails60m:    liqFails,
			UnclassifiedFails60m: uncFails,
			ForwardFailRate60m:   fwdRate,
			PolicyFailRate60m:    polRate,
			LiquidityFailRate60m: liqRate,
			WindowMin:            windowMin,
			SampleLow:            sampleLow,
			NoisySample:          meta.NoisySampleApplied,
			PolicyHot:            policyHot,
			LiquidityHot:         liquidityHot,
			ForwardHot:           forwardHot,
		}
	}
	return signals, meta
}

func (e *autofeeEngine) htlcSignalWindow() time.Duration {
	secs := e.cfg.RunIntervalSec
	if secs <= 0 {
		secs = e.profile.RunIntervalSec
	}
	if secs <= 0 {
		secs = 3600
	}
	if secs < 3600 {
		secs = 3600
	}
	return time.Duration(secs) * time.Second
}

func (e *autofeeEngine) rebalanceFailureSignalWindow() time.Duration {
	hours := e.profile.RebalFailWindowHours
	if hours <= 0 {
		hours = 6
	}
	window := time.Duration(hours) * time.Hour
	successWindow := e.htlcSignalWindow()
	if window < successWindow {
		window = successWindow
	}
	return window
}

func (e *autofeeEngine) rebalanceFailureCampaignGap() time.Duration {
	mins := e.profile.RebalFailCampaignGapMin
	if mins <= 0 {
		mins = 90
	}
	gap := time.Duration(mins) * time.Minute
	if gap > e.rebalanceFailureSignalWindow() {
		gap = e.rebalanceFailureSignalWindow()
	}
	return gap
}

func (e *autofeeEngine) htlcThresholdFactors() (float64, float64, float64) {
	nodeFactor := 1.0
	switch strings.ToLower(strings.TrimSpace(e.calib.NodeClass)) {
	case "small":
		nodeFactor = 0.70
	case "medium":
		nodeFactor = 0.85
	case "large":
		nodeFactor = 1.00
	case "xl":
		nodeFactor = 1.20
	}

	liquidityFactor := 1.0
	switch strings.ToLower(strings.TrimSpace(e.calib.LiquidityClass)) {
	case "drained":
		liquidityFactor = 0.85
	case "balanced":
		liquidityFactor = 1.00
	case "full":
		liquidityFactor = 1.10
	}

	thresholdFactor := nodeFactor * liquidityFactor
	if thresholdFactor < 0.60 {
		thresholdFactor = 0.60
	}
	if thresholdFactor > 1.40 {
		thresholdFactor = 1.40
	}
	return nodeFactor, liquidityFactor, thresholdFactor
}

func effectiveLowOutThresholds(baseLow float64, baseProtect float64, liquidityClass string, localRatio float64) (float64, float64, float64) {
	if baseLow <= 0 {
		baseLow = defaultLowOutProtectThresh
	}
	if baseProtect <= 0 {
		baseProtect = defaultLowOutProtectThresh
	}

	factor := 1.0
	switch strings.ToLower(strings.TrimSpace(liquidityClass)) {
	case "drained":
		factor = lowOutFactorDrained
	case "balanced":
		factor = lowOutFactorBalanced
	case "full":
		factor = lowOutFactorFull
	}

	if localRatio > 0 && localRatio < 1 {
		if localRatio < 0.25 {
			factor += ((0.25 - localRatio) / 0.25) * lowOutFactorRatioAdjMax
		} else if localRatio > 0.75 {
			factor -= ((localRatio - 0.75) / 0.25) * lowOutFactorRatioAdjMax
		}
	}
	if factor < lowOutFactorMin {
		factor = lowOutFactorMin
	}
	if factor > lowOutFactorMax {
		factor = lowOutFactorMax
	}

	lowOut := baseLow * factor
	protect := baseProtect * factor
	if lowOut < lowOutThreshMin {
		lowOut = lowOutThreshMin
	}
	if lowOut > lowOutThreshMax {
		lowOut = lowOutThreshMax
	}
	if protect < lowOutThreshMin {
		protect = lowOutThreshMin
	}
	if protect > lowOutThreshMax {
		protect = lowOutThreshMax
	}
	return lowOut, protect, factor
}

type outRatioNormalizationMeta struct {
	Raw          float64
	Effective    float64
	CapRel       float64
	LocalToAvg   float64
	NodeAdj      float64
	OutlierLarge bool
	OutlierSmall bool
	AbsFloorUsed bool
}

// effectiveChannelOutRatio blends per-channel ratio with absolute/local and node-wide liquidity context.
// This keeps large/small outlier channels from being over-classified as drained/full using only local/capacity.
func effectiveChannelOutRatio(outRatio float64, localBalSat int64, capacitySat int64, avgCapacitySat int64, nodeLocalRatio float64) (float64, outRatioNormalizationMeta) {
	effective := clampFloat(outRatio, 0.0, 1.0)
	meta := outRatioNormalizationMeta{
		Raw:       effective,
		Effective: effective,
	}
	if capacitySat <= 0 || avgCapacitySat <= 0 {
		return effective, meta
	}

	capRel := float64(capacitySat) / float64(avgCapacitySat)
	if capRel <= 0 {
		return effective, meta
	}
	meta.CapRel = capRel
	meta.OutlierLarge = capRel >= channelOutlierLargeCapRelMin
	meta.OutlierSmall = capRel <= channelOutlierSmallCapRelMax
	localSat := math.Max(0.0, float64(localBalSat))
	meta.LocalToAvg = localSat / float64(avgCapacitySat)
	if !meta.OutlierLarge && !meta.OutlierSmall {
		return effective, meta
	}
	sizeFactor := math.Pow(capRel, channelSizeRatioExponent)
	sizeFactor = clampFloat(sizeFactor, channelSizeRatioFactorMin, channelSizeRatioFactorMax)
	ratioAdj := clampFloat(effective*sizeFactor, 0.0, 1.0)
	absScore := 0.0
	if localSat > 0 {
		absScore = localSat / (localSat + float64(avgCapacitySat))
	}

	blendWeight := 0.0
	blendDen := math.Log(channelOutlierBlendFullAtRatio)
	if blendDen > 0 {
		blendWeight = math.Abs(math.Log(capRel)) / blendDen
		blendWeight = clampFloat(blendWeight, 0.0, channelOutlierAbsBlendMax)
	}
	effective = ratioAdj*(1.0-blendWeight) + absScore*blendWeight

	// If absolute local liquidity is large versus node average, avoid treating the channel as severely drained.
	localToAvg := meta.LocalToAvg
	if localToAvg >= channelLargeAbsLocalMinAvg {
		t := 0.0
		if channelLargeAbsLocalFullAvg > channelLargeAbsLocalMinAvg {
			t = (localToAvg - channelLargeAbsLocalMinAvg) / (channelLargeAbsLocalFullAvg - channelLargeAbsLocalMinAvg)
		}
		t = clampFloat(t, 0.0, 1.0)
		floor := channelLargeAbsOutRatioMin + t*(channelLargeAbsOutRatioMax-channelLargeAbsOutRatioMin)
		if effective < floor {
			effective = floor
			meta.AbsFloorUsed = true
		}
	}

	// Node-wide local liquidity shifts sensitivity slightly.
	nodeAdj := 0.0
	if nodeLocalRatio > 0 && nodeLocalRatio < 1 {
		if nodeLocalRatio < channelNodeRatioLow {
			lack := (channelNodeRatioLow - nodeLocalRatio) / channelNodeRatioLow
			nodeAdj = -(clampFloat(lack, 0.0, 1.0) * channelNodeRatioAdjMax)
		} else if nodeLocalRatio > channelNodeRatioHigh {
			headroom := (nodeLocalRatio - channelNodeRatioHigh) / (1.0 - channelNodeRatioHigh)
			nodeAdj = clampFloat(headroom, 0.0, 1.0) * channelNodeRatioAdjMax
		}
		effective += nodeAdj
	}

	effective = clampFloat(effective, 0.0, 1.0)
	meta.NodeAdj = nodeAdj
	meta.Effective = effective
	return effective, meta
}

func minStagnationRecoveryOutSat(capacitySat int64) int64 {
	byCap := int64(math.Ceil(float64(capacitySat) * stagnationExitMinOutCapFrac))
	if byCap < stagnationExitMinOutSat1d {
		byCap = stagnationExitMinOutSat1d
	}
	return byCap
}

func hasStagnationRecoveryFlow(forwardCount1d int, outAmt1dSat int64, capacitySat int64) bool {
	if forwardCount1d < stagnationExitMinFwds1d {
		return false
	}
	return outAmt1dSat >= minStagnationRecoveryOutSat(capacitySat)
}

func minOutFallback21dSat(capacitySat int64) int64 {
	byCap := int64(math.Ceil(float64(capacitySat) * outFallback21dMinOutCapFrac))
	if byCap < outFallback21dMinOutSat {
		byCap = outFallback21dMinOutSat
	}
	return byCap
}

func hasOutFallback21dSignal(forwardCount21d int, outAmt21dSat int64, capacitySat int64) bool {
	if forwardCount21d < outFallback21dMinFwds {
		return false
	}
	return outAmt21dSat >= minOutFallback21dSat(capacitySat)
}

func minRebalFallback21dSat(capacitySat int64) int64 {
	byCap := int64(math.Ceil(float64(capacitySat) * rebalFallback21dMinAmtCapFrac))
	if byCap < rebalFallback21dMinAmtSat {
		byCap = rebalFallback21dMinAmtSat
	}
	return byCap
}

func hasRebalFallback21dSignal(rebalAmt21dSat int64, capacitySat int64) bool {
	return rebalAmt21dSat >= minRebalFallback21dSat(capacitySat)
}

func applyAutofeeRefreshRebalMarkup(referencePpm int, markupFrac float64) int {
	if referencePpm <= 0 {
		return 0
	}
	if markupFrac < 0 {
		markupFrac = 0
	}
	return int(math.Ceil(float64(referencePpm) * (1.0 + markupFrac)))
}

func currentInboundDiscountFromPolicy(policy lndclient.ChannelPolicy) int {
	return currentInboundDiscountFromRate(policy.InboundFeeRatePpm)
}

func currentInboundDiscountFromRate(inboundFeeRatePpm int64) int {
	if inboundFeeRatePpm >= 0 {
		return 0
	}
	return absInt(int(-inboundFeeRatePpm))
}

func inboundFeeUpdateForDiscount(currentDiscount int, targetDiscount int) (bool, int64) {
	if targetDiscount > 0 {
		return true, int64(-absInt(targetDiscount))
	}
	if currentDiscount != targetDiscount {
		return true, 0
	}
	return false, 0
}

func inboundDiscountMaxRatioFromConfig(cfg AutofeeConfig) float64 {
	if cfg.InboundDiscountMaxRatioOverride > 0 {
		return cfg.InboundDiscountMaxRatioOverride
	}
	return defaultInboundDiscountMaxRatio
}

func maxInboundDiscountForAppliedPpm(appliedPpm int, maxRatio float64) int {
	if appliedPpm <= 0 {
		return 0
	}
	if maxRatio <= 0 {
		maxRatio = defaultInboundDiscountMaxRatio
	}
	return int(math.Ceil(float64(appliedPpm) * maxRatio))
}

func normalizeStaleInboundDiscount(currentDiscount int, appliedPpm int, maxRatio float64) (int, bool) {
	currentDiscount = absInt(currentDiscount)
	if currentDiscount <= 0 {
		return 0, false
	}
	capDiscount := maxInboundDiscountForAppliedPpm(appliedPpm, maxRatio)
	if capDiscount <= 0 {
		return 0, true
	}
	if currentDiscount > capDiscount {
		return capDiscount, true
	}
	return currentDiscount, false
}

func selectAutofeeRefreshReference(capacitySat int64, forward7d forwardStat, forward21d forwardStat, rebal7d rebalStat, rebal21d rebalStat, rebalMarkupFrac float64) (int, int, string, bool) {
	outPpm7d := ppmMsat(forward7d.FeeMsat, forward7d.AmtMsat)
	rebalPpm7d := ppmMsat(rebal7d.FeeMsat, rebal7d.AmtMsat)
	outPpm21d := ppmMsat(forward21d.FeeMsat, forward21d.AmtMsat)
	hasOut21d := outPpm21d > 0 && hasOutFallback21dSignal(int(forward21d.Count), forward21d.AmtMsat/1000, capacitySat)
	rebalPpm21d := ppmMsat(rebal21d.FeeMsat, rebal21d.AmtMsat)
	hasRebal21d := rebalPpm21d > 0 && hasRebalFallback21dSignal(rebal21d.AmtMsat/1000, capacitySat)

	outRef := 0
	outSource := ""
	switch {
	case outPpm7d > 0:
		outRef = outPpm7d
		outSource = "outppm7d"
	case hasOut21d:
		outRef = outPpm21d
		outSource = "outppm21d"
	}

	rebalRef := 0
	rebalSource := ""
	switch {
	case rebalPpm7d > 0:
		rebalRef = rebalPpm7d
		rebalSource = "rebalppm7d"
	case hasRebal21d:
		rebalRef = rebalPpm21d
		rebalSource = "rebalppm21d"
	}

	if outRef > 0 {
		if rebalRef > outRef {
			return rebalRef, rebalRef, rebalSource, true
		}
		return outRef, outRef, outSource, true
	}

	if rebalRef > 0 {
		return applyAutofeeRefreshRebalMarkup(rebalRef, rebalMarkupFrac), rebalRef, rebalSource + "+10%", true
	}

	return 0, 0, "", false
}

func hasAutofeeForwardMovement(st forwardStat) bool {
	return st.Count > 0 || st.AmtMsat > 0 || st.FeeMsat > 0
}

func hasAutofeeInboundMovement(st inboundStat) bool {
	return st.Count > 0 || st.AmtMsat > 0
}

func hasAutofeeRebalanceMovement(st rebalStat) bool {
	return st.Count > 0 || st.AmtMsat > 0 || st.FeeMsat > 0
}

func shouldAutofeeIdleRefreshChannel(cfg AutofeeConfig, forward7d forwardStat, inbound7d inboundStat, rebal7d rebalStat) bool {
	if !cfg.IdleRefreshEnabled {
		return false
	}
	if normalizeAutofeeOperationMode(cfg.OperationMode) != autofeeOperationModeBalanced {
		return false
	}
	return !hasAutofeeForwardMovement(forward7d) &&
		!hasAutofeeInboundMovement(inbound7d) &&
		!hasAutofeeRebalanceMovement(rebal7d)
}

func shouldSkipAutofeeIdleRefreshRepeat(st *autofeeChannelState, now time.Time, localPpm int, targetPpm int) bool {
	if st == nil || targetPpm <= 0 || st.LastIdleRefreshTs.IsZero() {
		return false
	}
	if targetPpm != localPpm && !shouldHoldAutofeeSmallDelta(localPpm, targetPpm) {
		return false
	}
	if st.LastIdleRefreshPpm > 0 && st.LastIdleRefreshPpm != targetPpm {
		return false
	}
	elapsed := now.Sub(st.LastIdleRefreshTs)
	if elapsed < 0 {
		return true
	}
	return elapsed < time.Duration(autofeeIdleRefreshWindowDays)*24*time.Hour
}

func minAutofeeApplyDeltaPpm(localPpm int) int {
	minDelta := autofeeMinApplyDeltaPpm
	if localPpm > 0 {
		relDelta := int(math.Ceil(float64(localPpm) * autofeeMinApplyDeltaFrac))
		if relDelta > minDelta {
			minDelta = relDelta
		}
	}
	return minDelta
}

func shouldHoldAutofeeSmallDelta(localPpm int, nextPpm int) bool {
	if nextPpm == localPpm {
		return false
	}
	return absInt(nextPpm-localPpm) < minAutofeeApplyDeltaPpm(localPpm)
}

func appendAutofeeTagOnce(tags []string, tag string) []string {
	tag = strings.TrimSpace(tag)
	if tag == "" || containsTag(tags, tag) {
		return tags
	}
	return append(tags, tag)
}

func removeAutofeeTags(tags []string, drop map[string]bool) []string {
	if len(tags) == 0 || len(drop) == 0 {
		return tags
	}
	out := tags[:0]
	for _, tag := range tags {
		if drop[tag] {
			continue
		}
		out = append(out, tag)
	}
	return out
}

func applyAutofeeIdleRefreshDecision(d *decision, now time.Time, targetPpm int, referencePpm int, source string) {
	if d == nil || targetPpm <= 0 {
		return
	}
	source = strings.TrimSpace(source)
	if referencePpm <= 0 {
		referencePpm = targetPpm
	}
	d.Tags = removeAutofeeTags(d.Tags, map[string]bool{
		"autofee_settling": true,
		"cooldown":         true,
		"cooldown-profit":  true,
		"floor-lock":       true,
		"hold-small":       true,
		"same-ppm":         true,
		"stepcap":          true,
		"stepcap-lock":     true,
	})
	d.Tags = appendAutofeeTagOnce(d.Tags, "idle-refresh")
	if source != "" {
		d.Tags = appendAutofeeTagOnce(d.Tags, "idle-refresh:"+source)
	}
	d.Target = targetPpm
	d.TargetRaw = targetPpm
	d.TargetFinal = targetPpm
	d.NewPpm = targetPpm
	d.Floor = targetPpm
	d.FloorSrc = "idle-refresh"
	d.FloorBasePpm = referencePpm
	d.FloorBaseSrc = source
	if strings.HasPrefix(source, "seed:") {
		d.Seed = referencePpm
	}
	d.TargetGapPpm = targetPpm - d.LocalPpm
	d.TargetGapPct = calcTargetGapPct(d.LocalPpm, targetPpm)

	outboundChanged := targetPpm != d.LocalPpm
	inboundChanged := d.InboundDiscount != d.PrevInboundDiscount
	smallDeltaHeld := shouldHoldAutofeeSmallDelta(d.LocalPpm, targetPpm)
	if smallDeltaHeld {
		d.TargetFinal = d.LocalPpm
		d.NewPpm = d.LocalPpm
		d.Tags = appendAutofeeTagOnce(d.Tags, "hold-small")
		d.Tags = appendAutofeeTagOnce(d.Tags, "small-delta")
	}
	d.Apply = outboundChanged || inboundChanged
	if smallDeltaHeld {
		d.Apply = inboundChanged
	} else if !outboundChanged {
		d.Tags = appendAutofeeTagOnce(d.Tags, "same-ppm")
	}

	st := d.State
	if st == nil {
		return
	}
	st.LastIdleRefreshTs = now
	st.LastIdleRefreshPpm = targetPpm
	st.LastIdleRefreshSrc = source
	if smallDeltaHeld {
		st.LastPpm = d.LocalPpm
		return
	}
	st.LastPpm = targetPpm
	if outboundChanged {
		prevDir := st.LastDir
		st.LastTs = now
		if targetPpm > d.LocalPpm {
			st.LastDir = "up"
		} else {
			st.LastDir = "down"
		}
		if prevDir != "" && st.LastDir != "" && prevDir != st.LastDir {
			st.ExplorerState.LastReversalDir = st.LastDir
			st.ExplorerState.LastReversalTs = now.Unix()
		}
		st.StalledRounds = 0
	} else if d.Target == d.LocalPpm {
		st.StalledRounds = 0
	}
}

func (e *autofeeEngine) maybeApplyIdleRefresh(ctx context.Context, ch lndclient.ChannelInfo, d *decision, forward7d forwardStat, forward21d forwardStat, inbound7d inboundStat, rebalMovement7d rebalStat, rebal7d rebalStat, rebal21d rebalStat) *decision {
	if d == nil || !shouldAutofeeIdleRefreshChannel(e.cfg, forward7d, inbound7d, rebalMovement7d) {
		return d
	}
	targetPpm, referencePpm, source, ok := selectAutofeeRefreshReference(
		ch.CapacitySat,
		forward7d,
		forward21d,
		rebal7d,
		rebal21d,
		autofeeRefreshRebalMarkup,
	)
	if !ok {
		seedPpm, seedSource, err := e.refreshSeedForChannel(ctx, strings.TrimSpace(ch.RemotePubkey))
		if err != nil {
			if e.svc != nil && e.svc.logger != nil {
				e.svc.logger.Printf("autofee: idle refresh seed unavailable for %s: %v", ch.ChannelPoint, err)
			}
			return d
		}
		if seedPpm <= 0 {
			return d
		}
		targetPpm = int(math.Round(seedPpm))
		referencePpm = targetPpm
		source = seedSource
		ok = true
	}
	if !ok || targetPpm <= 0 {
		return d
	}
	targetPpm = clampInt(targetPpm, e.cfg.MinPpm, e.cfg.MaxPpm)
	if shouldSkipAutofeeIdleRefreshRepeat(d.State, e.now, d.LocalPpm, targetPpm) {
		return d
	}
	applyAutofeeIdleRefreshDecision(d, e.now, targetPpm, referencePpm, source)
	return d
}

func selectAutofeeRefreshInboundDiscount(cfg AutofeeConfig, profile autofeeProfile, rawOutRatio float64, effectiveOutRatio float64, forward1d forwardStat, forward7d forwardStat, capacitySat int64, baseCostPpm int, appliedPpm int) (int, string, bool) {
	if appliedPpm <= 0 || baseCostPpm <= 0 {
		return 0, "", false
	}

	inboundDiscountMaxRatio := defaultInboundDiscountMaxRatio
	if cfg.InboundDiscountMaxRatioOverride > 0 {
		inboundDiscountMaxRatio = cfg.InboundDiscountMaxRatioOverride
	}
	inboundDiscountMinRetainedSpreadFrac := profile.InboundDiscountMinRetainedSpreadFrac
	if inboundDiscountMinRetainedSpreadFrac <= 0 {
		inboundDiscountMinRetainedSpreadFrac = 0.12
	}
	if cfg.InboundDiscountMinRetainedSpreadFracOverride > 0 {
		inboundDiscountMinRetainedSpreadFrac = cfg.InboundDiscountMinRetainedSpreadFracOverride
	}

	switch normalizeAutofeeOperationMode(cfg.OperationMode) {
	case autofeeOperationModeMarketRefill:
		recentForwards1d := int(forward1d.Count)
		noFlow1d := recentForwards1d == 0 && forward1d.AmtMsat <= 0
		weakRecentFlow := !hasStagnationRecoveryFlow(recentForwards1d, forward1d.AmtMsat/1000, capacitySat)
		discount, _ := computeMarketRefillInboundDiscount(
			true,
			effectiveOutRatio,
			recentForwards1d,
			noFlow1d,
			weakRecentFlow,
			0,
			baseCostPpm,
			appliedPpm,
			inboundDiscountMaxRatio,
			inboundDiscountMinRetainedSpreadFrac,
			profile,
		)
		return discount, "market-refill", true
	default:
		if !cfg.InboundPassiveEnabled {
			return 0, "balanced-disabled", true
		}
		inboundDiscountReachOutRatio := profile.InboundDiscountReachOutRatio
		if inboundDiscountReachOutRatio <= 0 {
			inboundDiscountReachOutRatio = 0.10
		}
		if cfg.InboundDiscountReachOutRatioOverride > 0 {
			inboundDiscountReachOutRatio = cfg.InboundDiscountReachOutRatioOverride
		}
		fwdCount := int(forward7d.Count)
		outPpm7d := ppmMsat(forward7d.FeeMsat, forward7d.AmtMsat)
		marginPpm7d := outPpm7d - int(math.Ceil(float64(baseCostPpm)*1.10))
		discount, source := computeBalancedInboundDiscount(
			true,
			"sink",
			rawOutRatio,
			fwdCount,
			marginPpm7d,
			baseCostPpm,
			appliedPpm,
			inboundDiscountMaxRatio,
			inboundDiscountReachOutRatio,
			inboundDiscountMinRetainedSpreadFrac,
		)
		if discount <= 0 {
			return 0, "", false
		}
		return discount, source, true
	}
}

func slowCycle30dMinScore(profile autofeeProfile) int {
	switch profile.Name {
	case "conservative":
		return 65
	case "aggressive":
		return 45
	default:
		return 50
	}
}

func slowCycle30dUnderrepFrac(profile autofeeProfile) float64 {
	switch profile.Name {
	case "conservative":
		return 0.45
	case "aggressive":
		return 0.75
	default:
		return 0.60
	}
}

func slowCycle30dRebalCostHeadroom(profile autofeeProfile) float64 {
	switch profile.Name {
	case "conservative":
		return 1.00
	case "aggressive":
		return 1.10
	default:
		return 1.05
	}
}

func slowCycle30dOutRatioMax(profile autofeeProfile) float64 {
	switch profile.Name {
	case "conservative":
		return 0.25
	case "aggressive":
		return 0.55
	default:
		return 0.40
	}
}

func slowCycle30dOutBlendFrac(profile autofeeProfile, effectiveOutRatio float64) float64 {
	base := 0.30
	switch profile.Name {
	case "conservative":
		base = 0.20
	case "aggressive":
		base = 0.40
	}
	switch {
	case effectiveOutRatio <= 0.10:
		base += 0.10
	case effectiveOutRatio <= 0.20:
		base += 0.05
	case effectiveOutRatio >= 0.60:
		base -= 0.10
	case effectiveOutRatio >= 0.45:
		base -= 0.05
	}
	return clampFloat(base, 0.15, 0.55)
}

func slowCycle30dFallbackFrac(profile autofeeProfile, effectiveOutRatio float64) float64 {
	base := 0.60
	switch profile.Name {
	case "conservative":
		base = 0.45
	case "aggressive":
		base = 0.75
	}
	switch {
	case effectiveOutRatio <= 0.10:
		base += 0.10
	case effectiveOutRatio <= 0.20:
		base += 0.05
	case effectiveOutRatio >= 0.60:
		base -= 0.10
	case effectiveOutRatio >= 0.45:
		base -= 0.05
	}
	return clampFloat(base, 0.35, 0.85)
}

func shouldUseSlowCycle30d(profile autofeeProfile, ranking autofeeRankingSnapshot, hasRanking bool, effectiveOutRatio float64, outPpm7d int, rebalPpm7d int) bool {
	if !hasRanking {
		return false
	}
	if ranking.ProfitFee30dSat <= 0 || ranking.Score30d < slowCycle30dMinScore(profile) {
		return false
	}
	if ranking.PeerStabilityScore > 0 && ranking.PeerStabilityScore < 45 {
		return false
	}
	if ranking.OutPpm30d <= 0 || ranking.RebalPpm30d <= 0 {
		return false
	}
	if float64(ranking.RebalPpm30d) > float64(ranking.OutPpm30d)*slowCycle30dRebalCostHeadroom(profile) {
		return false
	}
	if effectiveOutRatio > slowCycle30dOutRatioMax(profile) {
		return false
	}
	if outPpm7d <= 0 {
		return true
	}
	if float64(outPpm7d) <= float64(ranking.OutPpm30d)*slowCycle30dUnderrepFrac(profile) {
		return true
	}
	return ranking.ProfitFee7dSat <= 0 && rebalPpm7d > 0 &&
		float64(outPpm7d) <= float64(ranking.OutPpm30d)*(slowCycle30dUnderrepFrac(profile)+0.10)
}

func assistChannelMaxPpm(profile autofeeProfile) int {
	switch profile.Name {
	case "conservative":
		return 150
	case "aggressive":
		return 400
	default:
		return 250
	}
}

func shouldUseAssistChannel(profile autofeeProfile, ranking autofeeRankingSnapshot, hasRanking bool, classLabel string, localPpm int) bool {
	if !hasRanking || localPpm < 0 {
		return false
	}
	role := strings.TrimSpace(strings.ToLower(classLabel))
	if role == "sink" {
		return false
	}
	if ranking.RebalanceDependence > 60 {
		return false
	}
	if ranking.ProfitFee7dSat < -500 {
		return false
	}
	if localPpm > assistChannelMaxPpm(profile) {
		return false
	}
	if ranking.AssistedForwardFee7dSat < 100 {
		return false
	}
	if ranking.ForwardInCount7d < 4 && ranking.ForwardInAmountSat7d < 1_000_000 {
		return false
	}
	if ranking.ForwardOutAmountSat7d > 0 && ranking.ForwardInAmountSat7d < int64(math.Ceil(float64(ranking.ForwardOutAmountSat7d)*0.90)) {
		return false
	}
	if ranking.State == "close" && ranking.ProfitFee30dSat <= 0 {
		return false
	}
	return true
}

func shouldPreserveAssistChannelUpwardPressure(active bool, recentRebalanceCount int, htlcLiquidityHot bool) bool {
	if !active {
		return false
	}
	if recentRebalanceCount > 0 || htlcLiquidityHot {
		return false
	}
	return true
}

func rankingRebalanceCostHeavy(ranking autofeeRankingSnapshot) bool {
	if ranking.RebalanceDependence >= 50 {
		return true
	}
	if ranking.RebalPpm30d <= 0 {
		return false
	}
	if ranking.OutPpm30d > 0 && ranking.RebalPpm30d >= int(math.Round(float64(ranking.OutPpm30d)*0.85)) {
		return true
	}
	return ranking.ProfitFee30dSat <= 0
}

func deriveRankingPolicy(ranking autofeeRankingSnapshot, hasRanking bool, recentRebalanceCount int, htlcLiquidityHot bool, surgeConfirmSignal bool) ([]string, bool) {
	if !hasRanking {
		return nil, false
	}
	tags := []string{}
	switch ranking.State {
	case "expand":
		tags = append(tags, "rank-expand")
	case "maintain":
		tags = append(tags, "rank-maintain")
	case "monitor":
		tags = append(tags, "rank-monitor")
	case "close":
		tags = append(tags, "rank-close")
	}
	if recentRebalanceCount > 0 || htlcLiquidityHot || surgeConfirmSignal {
		return tags, false
	}
	switch ranking.State {
	case "close":
		if ranking.ProfitFee7dSat <= 0 && ranking.ProfitFee30dSat <= 0 {
			return tags, true
		}
		if ranking.TrendDirection == "worsening" && ranking.ProfitFee7dSat <= 0 && ranking.Score <= 30 {
			return tags, true
		}
	case "monitor":
		if ranking.TrendDirection == "worsening" &&
			ranking.ProfitFee7dSat <= 0 &&
			ranking.Score <= 55 &&
			(ranking.Score30d <= 65 || ranking.ProfitFee30dSat <= 0) {
			return tags, true
		}
		if ranking.ProfitFee7dSat <= 0 && rankingRebalanceCostHeavy(ranking) {
			tags = append(tags, "rank-monitor-loss-noup")
			return tags, true
		}
	}
	return tags, false
}

func applySlowCycle30dReferences(profile autofeeProfile, ranking autofeeRankingSnapshot, hasRanking bool, effectiveOutRatio float64, outPpm7d int, rebalPpm7d int) (int, int, bool, []string) {
	if !shouldUseSlowCycle30d(profile, ranking, hasRanking, effectiveOutRatio, outPpm7d, rebalPpm7d) {
		return outPpm7d, rebalPpm7d, false, nil
	}
	tags := []string{"slow-cycle-30d"}
	outBlendFrac := slowCycle30dOutBlendFrac(profile, effectiveOutRatio)
	outFallbackFrac := slowCycle30dFallbackFrac(profile, effectiveOutRatio)

	nextOut := outPpm7d
	if outPpm7d > 0 {
		nextOut = int(math.Round(float64(outPpm7d)*(1.0-outBlendFrac) + float64(ranking.OutPpm30d)*outBlendFrac))
	} else {
		nextOut = int(math.Round(float64(ranking.OutPpm30d) * outFallbackFrac))
	}

	nextRebal := rebalPpm7d
	if rebalPpm7d > 0 {
		blendedRebal := int(math.Round(float64(rebalPpm7d)*(1.0-outBlendFrac) + float64(ranking.RebalPpm30d)*outBlendFrac))
		nextRebal = maxInt(rebalPpm7d, blendedRebal)
	} else {
		nextRebal = int(math.Round(float64(ranking.RebalPpm30d) * outFallbackFrac))
	}

	if nextOut > outPpm7d {
		tags = append(tags, "slow-cycle-30d-out")
	}
	if nextRebal > rebalPpm7d {
		tags = append(tags, "slow-cycle-30d-rebal")
	}
	return nextOut, nextRebal, true, tags
}

func minSurgeConfirmRebalSat(capacitySat int64) int64 {
	if capacitySat <= 0 {
		return 0
	}
	return int64(math.Ceil(float64(capacitySat) * surgeConfirmRebalCapFrac))
}

func minFloorRebalSat(capacitySat int64) int64 {
	if capacitySat <= 0 {
		return 0
	}
	return int64(math.Ceil(float64(capacitySat) * floorRebalMinCapFrac))
}

func hasFloorRebalSignal(rebalAmtSat int64, capacitySat int64, successCount int64) bool {
	if rebalAmtSat <= 0 {
		return false
	}
	if successCount < floorRebalMinSuccessCount {
		return false
	}
	minAmtSat := minFloorRebalSat(capacitySat)
	if minAmtSat <= 0 {
		return true
	}
	return rebalAmtSat >= minAmtSat
}

func hasRecentRebalanceCostFloor(sig recentRebalanceSignal, capacitySat int64) bool {
	if sig.Count <= 0 || sig.AmtSat <= 0 || sig.FeeMsat <= 0 {
		return false
	}
	minAmtSat := minFloorRebalSat(capacitySat)
	if minAmtSat > 0 && sig.AmtSat < minAmtSat {
		return false
	}
	return sig.costPpm() > 0
}

func applyRecentRebalanceCostHardFloor(finalPpm int, recentCostPpm int, minPpm int, maxPpm int) (int, bool) {
	if recentCostPpm <= 0 || finalPpm >= recentCostPpm {
		return finalPpm, false
	}
	return clampInt(recentCostPpm, minPpm, maxPpm), true
}

func deriveNegMarginProtectedFloor(floorPpm int, baseCostPpm int, recentCostPpm int, minPpm int, maxPpm int) int {
	protected := floorPpm
	if baseCostPpm > 0 {
		protected = maxInt(protected, int(math.Ceil(float64(baseCostPpm)*1.10)))
	}
	if recentCostPpm > 0 {
		protected = maxInt(protected, recentCostPpm)
	}
	if protected <= 0 {
		return 0
	}
	return clampInt(protected, minPpm, maxPpm)
}

func applyNegMarginProtectedDown(targetPpm int, localPpm int, protectedFloor int, strongPressure bool) (int, string) {
	if targetPpm >= localPpm {
		return targetPpm, ""
	}
	if protectedFloor <= 0 || localPpm <= protectedFloor || strongPressure {
		return localPpm, "no-down-neg-margin"
	}
	if targetPpm < protectedFloor {
		return protectedFloor, "neg-margin-floor-down"
	}
	return targetPpm, "neg-margin-floor-down"
}

func applyNoisySurgeFollowUpUpHold(localPpm int, finalPpm int, floorPpm int, lastDir string, lastTs time.Time, now time.Time, followUpWindow time.Duration, htlcSampleLow bool, htlcNoisySample bool, htlcHotSignal bool, htlcForwardHot bool, recentRebalanceCount int, surgeConfirmSignal bool, surgeRoundConfirmSignal bool) (int, bool) {
	if finalPpm <= localPpm || !surgeConfirmSignal || surgeRoundConfirmSignal {
		return finalPpm, false
	}
	if !(htlcSampleLow || htlcNoisySample) {
		return finalPpm, false
	}
	if htlcHotSignal || htlcForwardHot || recentRebalanceCount > 0 {
		return finalPpm, false
	}
	if strings.TrimSpace(lastDir) != "up" || lastTs.IsZero() {
		return finalPpm, false
	}
	if followUpWindow <= 0 {
		return finalPpm, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(lastTs)
	if age < 0 || age > followUpWindow {
		return finalPpm, false
	}
	heldPpm := localPpm
	if floorPpm > heldPpm {
		heldPpm = floorPpm
	}
	if heldPpm >= finalPpm {
		return finalPpm, false
	}
	return heldPpm, true
}

func rebalFloorConfidence(successCount int64) float64 {
	if successCount <= 0 {
		return 0
	}
	confidence := float64(successCount) / float64(floorRebalMinSuccessCount)
	if confidence > 1 {
		return 1
	}
	return confidence
}

func hasSurgeConfirmSignal(recentRebalanceCount int, recentRebalanceAmtSat int64, capacitySat int64) bool {
	if recentRebalanceCount <= 0 {
		return false
	}
	minAmtSat := minSurgeConfirmRebalSat(capacitySat)
	if minAmtSat <= 0 {
		return true
	}
	return recentRebalanceAmtSat >= minAmtSat
}

func applySurgeConfirmationGate(st *autofeeChannelState, localPpm int, target int, surgeApplied bool, confirmSignal bool, roundConfirmSignal bool) (int, string) {
	if st == nil {
		return target, ""
	}

	if !surgeApplied || target <= localPpm {
		st.ExplorerState.SurgeGateRounds = 0
		st.ExplorerState.SurgeGatePpm = 0
		return target, ""
	}

	if st.ExplorerState.SurgeGatePpm != localPpm {
		st.ExplorerState.SurgeGatePpm = localPpm
		st.ExplorerState.SurgeGateRounds = 0
	}

	if confirmSignal {
		st.ExplorerState.SurgeGateRounds = 0
		return target, "surge-confirmed"
	}

	st.ExplorerState.SurgeGateRounds++
	if st.ExplorerState.SurgeGateRounds < surgeConfirmMinRounds {
		return localPpm, "surge-hold"
	}

	if !roundConfirmSignal {
		return localPpm, "surge-hold-flow"
	}

	st.ExplorerState.SurgeGateRounds = 0
	return target, "surge-confirmed-rounds"
}

func directionFromMove(localPpm int, nextPpm int) string {
	switch {
	case nextPpm > localPpm:
		return "up"
	case nextPpm < localPpm:
		return "down"
	default:
		return ""
	}
}

func calcTargetGapPct(localPpm int, targetPpm int) float64 {
	if localPpm <= 0 || targetPpm == localPpm {
		return 0
	}
	return math.Abs(float64(targetPpm-localPpm)) / float64(localPpm) * 100.0
}

func largeGapStepCapBoost(profile autofeeProfile, localPpm int, targetPpm int) (float64, bool) {
	if localPpm <= 0 || targetPpm == localPpm {
		return 0, false
	}
	minGapPct := profile.LargeGapStepCapBoostPctMin
	if minGapPct <= 0 {
		return 0, false
	}
	gapPct := calcTargetGapPct(localPpm, targetPpm)
	if gapPct < minGapPct {
		return 0, false
	}
	boost := profile.LargeGapStepCapBoost
	strong := false
	if profile.LargeGapStepCapBoostPctStrong > 0 &&
		gapPct >= profile.LargeGapStepCapBoostPctStrong &&
		profile.LargeGapStepCapBoostStrong > boost {
		boost = profile.LargeGapStepCapBoostStrong
		strong = true
	}
	if boost <= 0 {
		return 0, false
	}
	return boost, strong
}

func shouldEmitStallAlert(stalledRounds int, targetGapPct float64) bool {
	return stalledRounds >= stallAlertMinRounds && targetGapPct >= (stallAlertGapFrac*100.0)
}

func reversalConfirmRoundsForChannel(profile autofeeProfile, st *autofeeChannelState, targetGapPct float64) int {
	confirmRounds := profile.ReversalConfirmMinRounds
	if confirmRounds < 1 {
		confirmRounds = reversalConfirmMinRounds
	}
	fastTrackStallRounds := profile.ReversalFastTrackStallRounds
	if fastTrackStallRounds < 1 {
		fastTrackStallRounds = reversalFastTrackStallMinRounds
	}
	fastTrackGapPct := profile.ReversalFastTrackGapFrac * 100.0
	if fastTrackGapPct <= 0 {
		fastTrackGapPct = stallAlertGapFrac * 100.0
	}
	if st != nil &&
		st.StalledRounds >= fastTrackStallRounds &&
		targetGapPct >= fastTrackGapPct {
		confirmRounds--
	}
	if confirmRounds < 1 {
		confirmRounds = 1
	}
	return confirmRounds
}

func hasAntiFlipStrongSignal(tags []string) bool {
	for _, tag := range tags {
		switch tag {
		case "htlc-liquidity-hot", "surge+20%", "surge-timeout-release", "surge-confirmed-rounds", "extreme", "extreme-turbo":
			return true
		}
	}
	return false
}

func antiFlipExtraConfirmRoundsForChannel(profile autofeeProfile, st *autofeeChannelState, now time.Time, localPpm int, nextPpm int, targetGapPct float64, tags []string) (int, []string) {
	if st == nil || now.IsZero() {
		return 0, nil
	}
	extraRounds := profile.AntiFlipExtraConfirmRounds
	if extraRounds <= 0 {
		return 0, nil
	}
	windowHours := profile.AntiFlipWindowHours
	if windowHours <= 0 {
		return 0, nil
	}
	proposedDir := directionFromMove(localPpm, nextPpm)
	if proposedDir == "" || st.LastDir == "" || proposedDir == st.LastDir {
		return 0, nil
	}
	lastReversalDir := strings.TrimSpace(st.ExplorerState.LastReversalDir)
	lastReversalTs := st.ExplorerState.LastReversalTs
	if lastReversalDir == "" || lastReversalTs <= 0 || proposedDir == lastReversalDir {
		return 0, nil
	}
	age := now.Sub(time.Unix(lastReversalTs, 0))
	if age < 0 || age > time.Duration(windowHours)*time.Hour {
		return 0, nil
	}
	strongGapPct := profile.AntiFlipStrongGapFrac * 100.0
	if strongGapPct > 0 && targetGapPct >= strongGapPct {
		return 0, nil
	}
	if hasAntiFlipStrongSignal(tags) {
		return 0, nil
	}
	return extraRounds, []string{"anti-flip-window"}
}

func dynamicGoodOutRatio(profile autofeeProfile, liquidityClass string, nodeLocalRatio float64, meta outRatioNormalizationMeta) float64 {
	base := profile.BalancedUpOutRatioMin
	if profile.OutrateTargetOutRatioMin > base {
		base = profile.OutrateTargetOutRatioMin
	}
	if base <= 0 {
		base = profile.HighOutThresh
	}
	if base <= 0 {
		base = 0.20
	}

	factor := 1.0
	switch strings.ToLower(strings.TrimSpace(liquidityClass)) {
	case "drained":
		factor *= 0.82
	case "full":
		factor *= 1.12
	default:
		factor *= 1.0
	}

	if nodeLocalRatio > 0 && nodeLocalRatio < 1 {
		if nodeLocalRatio < 0.50 {
			lack := (0.50 - nodeLocalRatio) / 0.50
			factor *= 1.0 - clampFloat(lack, 0.0, 1.0)*0.20
		} else if nodeLocalRatio > 0.50 {
			headroom := (nodeLocalRatio - 0.50) / 0.50
			factor *= 1.0 + clampFloat(headroom, 0.0, 1.0)*0.12
		}
	}

	if meta.OutlierSmall {
		factor *= 0.92
	} else if meta.OutlierLarge {
		factor *= 1.08
	}

	return clampFloat(base*factor, 0.08, 0.35)
}

func dynamicUpwardPressureOutRatio(rawOutRatio float64, effectiveOutRatio float64, meta outRatioNormalizationMeta) float64 {
	if meta.OutlierLarge || meta.OutlierSmall {
		return effectiveOutRatio
	}
	return rawOutRatio
}

func capBalancedFloorDrivenUp(profile autofeeProfile, classLabel string, outRatio float64, goodOutRatio float64, localPpm int, targetPpm int, finalPpm int) (int, []string) {
	if finalPpm <= localPpm || targetPpm > localPpm || localPpm <= 0 {
		return finalPpm, nil
	}
	if goodOutRatio <= 0 {
		goodOutRatio = profile.BalancedUpOutRatioMin
	}
	if goodOutRatio <= 0 {
		goodOutRatio = 0.20
	}
	if outRatio < goodOutRatio {
		return finalPpm, nil
	}
	capFrac := profile.BalancedFloorUpCap
	if capFrac <= 0 {
		capFrac = 0.05
	}
	capped := applyStepCap(localPpm, finalPpm, capFrac, 5, localPpm)
	if capped < finalPpm {
		return capped, []string{"balanced-floor-up-cap"}
	}
	return finalPpm, nil
}

func hasStrongUpwardPressureSignal(recentRebalanceCount int, htlcLiquidityHot bool, surgeConfirmSignal bool) bool {
	return recentRebalanceCount > 0 || htlcLiquidityHot || surgeConfirmSignal
}

func applyOutnormFallbackUpHold(marketRefillMode bool, newInboundBootstrap bool, localPpm int, targetPpm int, finalPpm int, meta outRatioNormalizationMeta, outFrom21dFallback bool, rebalFrom21dFallback bool, observedOutSignal bool, observedRebalSignal bool, recentRebalanceCount int, htlcLiquidityHot bool, surgeConfirmSignal bool) (int, int, []string) {
	if marketRefillMode || newInboundBootstrap || finalPpm <= localPpm {
		return targetPpm, finalPpm, nil
	}
	if !meta.OutlierLarge && !meta.OutlierSmall {
		return targetPpm, finalPpm, nil
	}
	if observedOutSignal || observedRebalSignal {
		return targetPpm, finalPpm, nil
	}
	if !outFrom21dFallback && !rebalFrom21dFallback {
		return targetPpm, finalPpm, nil
	}
	if hasStrongUpwardPressureSignal(recentRebalanceCount, htlcLiquidityHot, surgeConfirmSignal) {
		return targetPpm, finalPpm, nil
	}
	return localPpm, localPpm, []string{"outnorm-fallback-hold"}
}

func applyHistoryReferenceUpCap(marketRefillMode bool, newInboundBootstrap bool, localPpm int, targetPpm int, finalPpm int, outPpm7d int, rebalHistoryRefPpm int, recentRebalanceCount int, htlcLiquidityHot bool, surgeConfirmSignal bool) (int, int, []string) {
	if marketRefillMode || newInboundBootstrap || finalPpm <= localPpm {
		return targetPpm, finalPpm, nil
	}
	historyRef := maxInt(outPpm7d, rebalHistoryRefPpm)
	if historyRef <= 0 {
		return targetPpm, finalPpm, nil
	}
	if hasStrongUpwardPressureSignal(recentRebalanceCount, htlcLiquidityHot, surgeConfirmSignal) {
		return targetPpm, finalPpm, nil
	}
	if localPpm >= historyRef {
		return localPpm, localPpm, []string{"history-up-hold"}
	}
	if finalPpm > historyRef {
		capped := maxInt(localPpm, historyRef)
		return minInt(targetPpm, historyRef), capped, []string{"history-up-cap"}
	}
	return targetPpm, finalPpm, nil
}

func shouldPauseOutratePegHeadroom(outPpm7d int, rebalHistoryRefPpm int, marginPpm7d int) bool {
	if outPpm7d <= 0 || rebalHistoryRefPpm <= 0 {
		return false
	}
	if marginPpm7d < 0 {
		return false
	}
	return float64(outPpm7d) >= float64(rebalHistoryRefPpm)*(1.0+outratePegRebalSpreadMinFrac)
}

func applyOutrateTargetAnchor(profile autofeeProfile, targetPpm int, outPpm7d int, outRatio float64, goodOutRatio float64, fwdCount int) (int, []string) {
	if targetPpm <= 0 || outPpm7d <= 0 {
		return targetPpm, nil
	}
	if goodOutRatio <= 0 {
		goodOutRatio = profile.OutrateTargetOutRatioMin
	}
	if goodOutRatio <= 0 {
		goodOutRatio = 0.20
	}
	if outRatio < goodOutRatio {
		return targetPpm, nil
	}
	minFwds := profile.OutrateTargetMinFwds
	if minFwds <= 0 {
		minFwds = 6
	}
	if fwdCount < minFwds {
		return targetPpm, nil
	}
	floorFrac := profile.OutrateTargetFloorFrac
	if floorFrac <= 0 {
		floorFrac = 0.80
	}
	blendFrac := profile.OutrateTargetBlendFrac
	if blendFrac <= 0 {
		blendFrac = 0.50
	}
	floorTarget := int(math.Round(float64(outPpm7d) * floorFrac))
	anchored := int(math.Round((1.0-blendFrac)*float64(targetPpm) + blendFrac*float64(outPpm7d)))
	if anchored < floorTarget {
		anchored = floorTarget
	}
	if anchored > outPpm7d {
		anchored = outPpm7d
	}
	if anchored > targetPpm {
		return anchored, []string{"outrate-target-anchor"}
	}
	return targetPpm, nil
}

func applySeedSignalCaps(profile autofeeProfile, seed float64, outPpm7d int, rebalPpm7d int, rebalFloorPpm int) (float64, []string) {
	if seed <= 0 {
		return seed, nil
	}
	maxAllowed := 0.0
	tags := []string{}
	if outPpm7d > 0 {
		mult := profile.SeedOutrateCapMult
		if mult <= 0 {
			mult = 1.35
		}
		maxAllowed = math.Max(maxAllowed, float64(outPpm7d)*mult)
	}
	if rebalPpm7d > 0 {
		mult := profile.SeedRebalCapMult
		if mult <= 0 {
			mult = 1.25
		}
		rebalRef := rebalPpm7d
		if rebalFloorPpm > rebalRef {
			rebalRef = rebalFloorPpm
			tags = append(tags, "seed:rebalfloor")
		}
		maxAllowed = math.Max(maxAllowed, float64(rebalRef)*mult)
	}
	if maxAllowed > 0 && seed > maxAllowed {
		if outPpm7d > 0 {
			tags = append(tags, "seed:outcap")
		}
		if rebalPpm7d > 0 {
			tags = append(tags, "seed:rebalcap")
		}
		seed = maxAllowed
	}
	return seed, tags
}

func shouldApplyFailedRebalancePressure(profile autofeeProfile, outRatio float64, lowOutProtectThresh float64, recentSuccessCount int, recentFailCount int) bool {
	if recentSuccessCount > 0 || recentFailCount <= 0 {
		return false
	}
	minAttempts := profile.RebalFailNoDownMinAttempts
	if minAttempts <= 0 {
		minAttempts = 3
	}
	if recentFailCount < minAttempts {
		return false
	}
	maxOutRatio := profile.RebalFailOutRatioMax
	if maxOutRatio <= 0 {
		maxOutRatio = lowOutProtectThresh
	}
	if lowOutProtectThresh > 0 && maxOutRatio < lowOutProtectThresh {
		maxOutRatio = lowOutProtectThresh
	}
	return outRatio <= maxOutRatio
}

func applyFailedRebalancePressure(profile autofeeProfile, localPpm int, targetPpm int, recentFailCount int, noFlow1d bool, htlcPressureSignal bool) (int, []string) {
	if localPpm <= 0 || recentFailCount <= 0 {
		return targetPpm, nil
	}
	tags := []string{}
	if targetPpm < localPpm {
		targetPpm = localPpm
		tags = append(tags, "rebal-fail-nodown")
	}
	minUpAttempts := profile.RebalFailUpMinAttempts
	if minUpAttempts <= 0 {
		minUpAttempts = maxInt(1, profile.RebalFailNoDownMinAttempts+2)
	}
	if recentFailCount < minUpAttempts || !(noFlow1d || htlcPressureSignal) {
		return targetPpm, tags
	}
	stepFrac := profile.RebalFailUpStepFrac
	if stepFrac <= 0 {
		stepFrac = 0.03
	}
	minStep := profile.RebalFailUpMinStepPpm
	if minStep <= 0 {
		minStep = 10
	}
	bumped := int(math.Ceil(float64(localPpm) * (1.0 + stepFrac)))
	if bumped < localPpm+minStep {
		bumped = localPpm + minStep
	}
	if bumped > targetPpm {
		targetPpm = bumped
		tags = append(tags, "rebal-fail-pressure")
	}
	return targetPpm, tags
}

func effectiveCooldownUpSecForChannel(profile autofeeProfile, baseCooldownUpSec int, effectiveOutRatio float64, holdUpOnRecentRebalance bool) int {
	cooldownSec := maxInt(autofeeMinCooldownSec, baseCooldownUpSec)
	if holdUpOnRecentRebalance {
		return cooldownSec
	}
	extremeOutMax := profile.CooldownUpExtremeOutRatioMax
	if extremeOutMax <= 0 {
		extremeOutMax = profile.ExtremeDrainTurboOutMax
	}
	if extremeOutMax > 0 && effectiveOutRatio <= extremeOutMax {
		if profile.CooldownUpExtremeSec > 0 {
			return minInt(cooldownSec, maxInt(autofeeMinCooldownSec, profile.CooldownUpExtremeSec))
		}
		return cooldownSec
	}
	drainedOutMax := profile.CooldownUpDrainedOutRatioMax
	if drainedOutMax <= 0 {
		drainedOutMax = profile.ExtremeDrainOutMax
	}
	if drainedOutMax > 0 && effectiveOutRatio <= drainedOutMax {
		if profile.CooldownUpDrainedSec > 0 {
			return minInt(cooldownSec, maxInt(autofeeMinCooldownSec, profile.CooldownUpDrainedSec))
		}
	}
	return cooldownSec
}

func strictCooldownBypassDetachedMult(profile autofeeProfile) float64 {
	switch {
	case profile.StepCap <= 0.05:
		return 1.20
	case profile.StepCap >= 0.10:
		return 1.30
	default:
		return 1.25
	}
}

func shouldBypassStrictCooldownUp(profile autofeeProfile, recentRebalanceCount int, htlcLiquidityHot bool, localPpm int, outPpm7d int, rebalRefPpm int, baseCostPpm int, baseCostSrc string) bool {
	if htlcLiquidityHot {
		return true
	}
	if recentRebalanceCount <= 0 {
		return false
	}
	if localPpm <= 0 {
		return true
	}
	refPpm := maxInt(outPpm7d, rebalRefPpm)
	if (isRebalanceBaseCostSource(baseCostSrc) || isOutrateBaseCostSource(baseCostSrc)) && baseCostPpm > refPpm {
		refPpm = baseCostPpm
	}
	if refPpm <= 0 {
		return true
	}
	return float64(localPpm) <= float64(refPpm)*strictCooldownBypassDetachedMult(profile)
}

func isMoveTowardTarget(localPpm int, nextPpm int, targetPpm int) bool {
	return math.Abs(float64(targetPpm-nextPpm)) < math.Abs(float64(targetPpm-localPpm))
}

func shouldHoldSmallStep(profile autofeeProfile, st *autofeeChannelState, localPpm int, nextPpm int, targetPpm int, allowSmallStep bool) bool {
	if allowSmallStep || localPpm <= 0 || nextPpm == localPpm {
		return false
	}
	delta := int(math.Abs(float64(nextPpm - localPpm)))
	minDelta := profile.HoldSmallMinDeltaPpm
	if minDelta <= 0 {
		minDelta = 15
	}
	minRel := profile.HoldSmallMinRelFrac
	if minRel <= 0 {
		minRel = 0.04
	}
	rel := float64(delta) / float64(maxInt(1, localPpm))
	if delta >= minDelta || rel >= minRel {
		return false
	}
	if !isMoveTowardTarget(localPpm, nextPpm, targetPpm) {
		return true
	}
	gapPpm := int(math.Abs(float64(targetPpm - localPpm)))
	gapBypassPpm := profile.HoldSmallGapBypassPpm
	if gapBypassPpm <= 0 {
		gapBypassPpm = minDelta * 8
	}
	if gapPpm >= gapBypassPpm {
		return false
	}
	gapBypassFrac := profile.HoldSmallGapBypassFrac
	if gapBypassFrac > 0 && calcTargetGapPct(localPpm, targetPpm) >= gapBypassFrac*100.0 {
		return false
	}
	stallBypassRounds := profile.HoldSmallStallBypassRounds
	if stallBypassRounds > 0 && st != nil && st.StalledRounds >= stallBypassRounds && gapPpm > 0 {
		return false
	}
	return true
}

func applyDirectionReversalGuard(st *autofeeChannelState, localPpm int, nextPpm int, confirmMinRounds int) (int, []string) {
	if st == nil {
		return nextPpm, nil
	}
	if confirmMinRounds < 1 {
		confirmMinRounds = 1
	}
	proposedDir := directionFromMove(localPpm, nextPpm)
	if proposedDir == "" {
		st.ExplorerState.ReversalPendingDir = ""
		st.ExplorerState.ReversalPendingRounds = 0
		return nextPpm, nil
	}
	if st.LastDir == "" || st.LastDir == proposedDir {
		st.ExplorerState.ReversalPendingDir = ""
		st.ExplorerState.ReversalPendingRounds = 0
		return nextPpm, nil
	}

	if st.ExplorerState.ReversalPendingDir != proposedDir {
		st.ExplorerState.ReversalPendingDir = proposedDir
		st.ExplorerState.ReversalPendingRounds = 1
	} else {
		st.ExplorerState.ReversalPendingRounds++
	}

	if st.ExplorerState.ReversalPendingRounds < confirmMinRounds {
		return localPpm, []string{"reversal-guard", fmt.Sprintf("reversal-pending-r%d", st.ExplorerState.ReversalPendingRounds)}
	}

	if confirmMinRounds < reversalConfirmMinRounds {
		return nextPpm, []string{"reversal-confirmed", "reversal-fasttrack"}
	}
	return nextPpm, []string{"reversal-confirmed"}
}

func capDownMoveForLowHTLCSample(localPpm int, nextPpm int, htlcSampleLow bool) (int, bool) {
	if !htlcSampleLow || localPpm <= 0 || nextPpm >= localPpm {
		return nextPpm, false
	}
	maxDownPpm := int(math.Round(float64(localPpm) * htlcLowSampleMaxDownFrac))
	if maxDownPpm < 1 {
		maxDownPpm = 1
	}
	minAllowed := localPpm - maxDownPpm
	if nextPpm < minAllowed {
		return minAllowed, true
	}
	return nextPpm, false
}

func capDownMoveGeneral(localPpm int, nextPpm int, htlcSampleLow bool) (int, bool) {
	if htlcSampleLow || localPpm <= 0 || nextPpm >= localPpm {
		return nextPpm, false
	}
	maxDownPpm := int(math.Round(float64(localPpm) * generalMaxDownFrac))
	if maxDownPpm < 1 {
		maxDownPpm = 1
	}
	minAllowed := localPpm - maxDownPpm
	if nextPpm < minAllowed {
		return minAllowed, true
	}
	return nextPpm, false
}

func scaleHTLCThresholdByWindow(base int, windowMin int, fallback int) int {
	if base <= 0 {
		base = fallback
	}
	if base <= 0 {
		return 1
	}
	if windowMin < 60 {
		windowMin = 60
	}
	// Use sublinear growth so long windows do not over-penalize lower-volume nodes.
	// 60m -> 1.00x, 180m -> 1.73x, 240m -> 2.00x.
	factor := math.Sqrt(float64(windowMin) / 60.0)
	scaled := int(math.Ceil(float64(base) * factor))
	if scaled < base {
		scaled = base
	}
	return maxInt(1, scaled)
}

func applyHTLCThresholdFactor(base int, factor float64) int {
	if base <= 0 {
		base = 1
	}
	if factor <= 0 {
		return base
	}
	scaled := int(math.Ceil(float64(base) * factor))
	return maxInt(1, scaled)
}

func applyHTLCGlobalCountFactor(base int, factor float64, floor int) int {
	if base <= 0 {
		base = 1
	}
	if factor <= 0 {
		factor = 1.0
	}
	scaled := int(math.Round(float64(base) * factor))
	return maxInt(maxInt(1, floor), scaled)
}

func applyHTLCGlobalRateFactor(base float64, factor float64, floor float64) float64 {
	if base <= 0 {
		return floor
	}
	if factor <= 0 {
		factor = 1.0
	}
	scaled := base * factor
	if scaled < floor {
		return floor
	}
	return scaled
}

func blendTargetWithSeed(base int, seed float64, weight float64) int {
	if base <= 0 || seed <= 0 || weight <= 0 {
		return base
	}
	w := weight
	if w > 1 {
		w = 1
	}
	return int(math.Round((1.0-w)*float64(base) + w*seed))
}

func applySeedSoftEnvelope(target int, seed float64, floorMult float64, ceilingMult float64, allowFloor bool) (int, []string) {
	if target <= 0 || seed <= 0 {
		return target, nil
	}
	if floorMult <= 0 {
		floorMult = defaultSeedFloorMult
	}
	if ceilingMult <= 0 {
		ceilingMult = defaultSeedCeilingMult
	}
	if ceilingMult < floorMult {
		ceilingMult = floorMult
	}

	seedFloor := int(math.Round(seed * floorMult))
	seedCeil := int(math.Round(seed * ceilingMult))
	if seedFloor <= 0 {
		seedFloor = int(math.Round(seed))
	}
	if seedCeil < seedFloor {
		seedCeil = seedFloor
	}

	tags := []string{}
	if target > seedCeil {
		target = seedCeil
		tags = append(tags, "seed:soft-ceil")
	}
	if allowFloor && target < seedFloor {
		target = seedFloor
		tags = append(tags, "seed:soft-floor")
	}
	return target, tags
}

func shouldEnableSeedEnvelope(seed float64, noFlow1d bool, weakRecentFlow bool, recentForwards1d int, recentRebalanceCount int, htlcPressureSignal bool, strongOutSignal bool, strongRebalSignal bool, discoveryHit bool, explorerActive bool, stagnationActive bool, superSourceActive bool) bool {
	if seed <= 0 || recentRebalanceCount != 0 || htlcPressureSignal || strongOutSignal || strongRebalSignal || discoveryHit || explorerActive || stagnationActive || superSourceActive {
		return false
	}
	if noFlow1d {
		return true
	}
	return weakRecentFlow && recentForwards1d <= 1
}

func shouldAllowBootstrapSeedSoftFloor(newInboundBootstrap bool, outRatio float64, minOutRatio float64) bool {
	if !newInboundBootstrap {
		return false
	}
	if minOutRatio <= 0 {
		minOutRatio = lowOutNoFlowUpperRatio
	}
	return outRatio >= minOutRatio
}

func seedShockMinFrac(profile autofeeProfile) float64 {
	threshold := defaultSeedShockMinFrac
	if profile.SeedGuardMaxJump > 0 && profile.SeedGuardMaxJump < threshold {
		threshold = profile.SeedGuardMaxJump
	}
	if threshold <= 0 {
		return defaultSeedShockMinFrac
	}
	return threshold
}

func resetSeedShock(st *autofeeChannelState) {
	if st == nil {
		return
	}
	st.SeedShockPendingPpm = 0
	st.SeedShockRounds = 0
	st.SeedShockDir = ""
}

func sameSeedShockCandidate(st *autofeeChannelState, seedPpm int, dir string) bool {
	if st == nil || st.SeedShockPendingPpm <= 0 || st.SeedShockDir != dir {
		return false
	}
	tolerance := maxInt(10, int(math.Round(float64(seedPpm)*0.05)))
	return absInt(st.SeedShockPendingPpm-seedPpm) <= tolerance
}

func applySeedShockGuard(st *autofeeChannelState, profile autofeeProfile, seed float64, tags []string, matureNoLocalSignal bool, hasLocalSignal bool) (float64, []string) {
	if st == nil || seed <= 0 || st.LastSeed <= 0 {
		return seed, tags
	}
	if !matureNoLocalSignal || hasLocalSignal {
		resetSeedShock(st)
		return seed, tags
	}
	previousSeed := st.LastSeed
	shockFrac := math.Abs(seed-float64(previousSeed)) / float64(previousSeed)
	if shockFrac < seedShockMinFrac(profile) {
		resetSeedShock(st)
		return seed, tags
	}

	seedPpm := int(math.Round(seed))
	if seedPpm <= 0 {
		return seed, tags
	}
	dir := "down"
	if seedPpm > previousSeed {
		dir = "up"
	}
	tags = append(tags, "seed:shock-"+dir)
	if sameSeedShockCandidate(st, seedPpm, dir) {
		st.SeedShockRounds++
	} else {
		st.SeedShockPendingPpm = seedPpm
		st.SeedShockRounds = 1
		st.SeedShockDir = dir
	}
	if st.SeedShockRounds >= seedShockConfirmRounds {
		resetSeedShock(st)
		tags = append(tags, "seed:shock-confirmed")
		return seed, tags
	}
	tags = append(tags, "seed:shock-hold")
	return float64(previousSeed), tags
}

func shouldRelaxNegMarginForSeedSoftEnvelope(seedEnvelopeActive bool, tags []string, localPpm int, target int, seed float64, ceilingMult float64) bool {
	if !seedEnvelopeActive || localPpm <= 0 || target >= localPpm || seed <= 0 {
		return false
	}
	if containsTag(tags, "seed:soft-ceil") {
		return true
	}
	if ceilingMult <= 0 {
		ceilingMult = defaultSeedCeilingMult
	}
	seedCeil := int(math.Round(seed * ceilingMult))
	if seedCeil <= 0 {
		return false
	}
	minGap := maxInt(100, int(math.Round(float64(localPpm)*0.05)))
	return localPpm > seedCeil && (localPpm-seedCeil) >= minGap
}

func shouldHoldUpOnRecentRebalance(classLabel string, outRatio float64, lowOutProtectThresh float64, recentRebalanceCount int) bool {
	if recentRebalanceCount <= 0 {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(classLabel), "sink") {
		return false
	}
	if lowOutProtectThresh <= 0 {
		lowOutProtectThresh = defaultLowOutProtectThresh
	}
	return outRatio < lowOutProtectThresh
}

func shouldHoldMatureEmptySinkUpwardPressure(classLabel string, channelAgeHours float64, bootstrapHours int, outRatio float64, lowOutProtectThresh float64, outPpm7d int, rebalRefPpm int, recentForwards1d int, recentRebalanceCount int, recentRebalanceWeakCount int, htlcPressureSignal bool, htlcForwardHot bool) bool {
	if !strings.EqualFold(strings.TrimSpace(classLabel), "sink") {
		return false
	}
	if lowOutProtectThresh <= 0 {
		lowOutProtectThresh = defaultLowOutProtectThresh
	}
	if outRatio > lowOutProtectThresh {
		return false
	}
	if outPpm7d <= 0 && rebalRefPpm <= 0 {
		return false
	}
	if recentForwards1d > 0 || htlcPressureSignal || htlcForwardHot {
		return false
	}
	if recentRebalanceCount > 0 || recentRebalanceWeakCount > 0 {
		return false
	}
	matureHours := float64(matureEmptySinkHistoryHours)
	if bootstrapHours > 0 {
		matureHours = math.Max(matureHours, float64(bootstrapHours*2))
	}
	return channelAgeHours >= matureHours
}

func matureEmptySinkHistoryAnchorParams(profile autofeeProfile) (float64, float64) {
	switch {
	case profile.StepCap <= 0.05:
		return 1.55, 1.30
	case profile.StepCap >= 0.10:
		return 1.30, 1.20
	default:
		return 1.40, 1.22
	}
}

func deriveMatureEmptySinkHistoryAnchor(profile autofeeProfile, localPpm int, outPpm7d int, rebalRefPpm int, baseCostPpm int, baseCostSrc string) (int, bool) {
	if localPpm <= 0 {
		return 0, false
	}
	refPpm := maxInt(outPpm7d, rebalRefPpm)
	if (isRebalanceBaseCostSource(baseCostSrc) || isOutrateBaseCostSource(baseCostSrc)) && baseCostPpm > refPpm {
		refPpm = baseCostPpm
	}
	if refPpm <= 0 {
		return 0, false
	}
	triggerMult, anchorMult := matureEmptySinkHistoryAnchorParams(profile)
	if float64(localPpm) < float64(refPpm)*triggerMult {
		return 0, false
	}
	anchor := int(math.Ceil(float64(refPpm) * anchorMult))
	if anchor < refPpm {
		anchor = refPpm
	}
	minGap := maxInt(75, int(math.Round(float64(localPpm)*0.05)))
	if localPpm-anchor < minGap {
		return 0, false
	}
	return anchor, anchor < localPpm
}

func shouldRelaxFloorForMatureEmptySinkAnchor(floorSrc string) bool {
	switch strings.TrimSpace(floorSrc) {
	case "rebal", "rebal-sink", "rebal-hold", "outrate", "outrate-sink", "peg", "no-signal", "seed-soft":
		return true
	default:
		return false
	}
}

func shouldRelaxFloorForRankingGuard(floorSrc string) bool {
	switch strings.TrimSpace(floorSrc) {
	case "rebal", "rebal-sink", "rebal-hold", "outrate", "outrate-sink", "peg", "no-signal", "seed-soft":
		return true
	default:
		return false
	}
}

func shouldProtectRebalanceFloorFromStallRelax(classLabel string, floorSrc string, rebalPpm7d int, ranking autofeeRankingSnapshot, hasRanking bool, outPpm7d int) bool {
	if !strings.EqualFold(strings.TrimSpace(classLabel), "sink") {
		return false
	}
	switch strings.TrimSpace(floorSrc) {
	case "rebal", "rebal-sink", "rebal-hold":
	default:
		return false
	}
	if rebalPpm7d <= 0 {
		return false
	}
	if hasRanking && ranking.RebalanceDependence >= 60 {
		return true
	}
	if outPpm7d > 0 && rebalPpm7d >= outPpm7d {
		return true
	}
	if hasRanking && ranking.RebalPpm30d > 0 && ranking.OutPpm30d > 0 && ranking.RebalPpm30d >= ranking.OutPpm30d {
		return true
	}
	return false
}

func deriveRebalanceExecutionPolicy(profile autofeeProfile, runtime autofeeRebalanceRuntimeSnapshot, hasRuntime bool, roiMin float64, rebalanceStatus string, classLabel string, channelAgeHours float64, bootstrapHours int, outRatio float64, lowOutProtectThresh float64, outPpm7d int, rebalHistoryRefPpm int, baseCostPpm int, baseCostSrc string, recentRebalanceCount int, recentRebalanceWeakCount int, htlcPressureSignal bool, htlcForwardHot bool, localPpm int) ([]string, bool, int, bool) {
	if !strings.EqualFold(classLabel, "sink") {
		return nil, false, 0, false
	}
	if channelAgeHours <= float64(maxInt(bootstrapHours, 1)) {
		return nil, false, 0, false
	}
	if outRatio >= lowOutProtectThresh {
		return nil, false, 0, false
	}
	if outPpm7d <= 0 && rebalHistoryRefPpm <= 0 {
		return nil, false, 0, false
	}
	if recentRebalanceCount > 0 || recentRebalanceWeakCount > 0 || htlcPressureSignal || htlcForwardHot {
		return nil, false, 0, false
	}

	tags := []string{"rebal-exec"}
	reason := ""
	switch {
	case hasRuntime && !runtime.AutoEnabled && !runtime.ManualRestartEnabled:
		reason = "disabled"
	case hasRuntime && runtime.AutoEnabled && runtime.ROIEstimateValid && roiMin > 0 && runtime.ROIEstimate < roiMin:
		reason = "roi"
	case hasRuntime && runtime.AutoEnabled && runtime.EligibleAsTarget && (rebalanceStatus == "budget_exhausted" || rebalanceStatus == "budget_insufficient"):
		reason = "budget"
	case hasRuntime && runtime.AutoEnabled && runtime.EligibleAsTarget && runtime.PaybackProgress > 0 && runtime.PaybackProgress < 1.0 && runtime.RebalanceCost7dPpm > 0 && runtime.Revenue7dSat <= 0:
		reason = "payback"
	}
	if reason == "" {
		return nil, false, 0, false
	}
	tags = append(tags, "rebal-exec-"+reason)

	anchor, ok := deriveMatureEmptySinkHistoryAnchor(profile, localPpm, outPpm7d, rebalHistoryRefPpm, baseCostPpm, baseCostSrc)
	if !ok {
		return tags, true, 0, false
	}
	return tags, true, anchor, true
}

func maxAutofeeChanges24h(profile autofeeProfile) int {
	switch {
	case profile.StepCap <= 0.05:
		return 2
	case profile.StepCap >= 0.10:
		return 4
	default:
		return 3
	}
}

func maxAutofeeConsecutiveUps24h(profile autofeeProfile) int {
	switch {
	case profile.StepCap <= 0.05:
		return 1
	case profile.StepCap >= 0.10:
		return 3
	default:
		return 2
	}
}

func shouldHoldForAutofeeChurn(profile autofeeProfile, recent autofeeRecentChangeStats, localPpm int, nextPpm int, recentRebalanceCount int, htlcLiquidityHot bool) []string {
	if nextPpm <= localPpm {
		return nil
	}
	if recentRebalanceCount > 0 || htlcLiquidityHot {
		return nil
	}
	if recent.ConsecutiveUp24h >= maxAutofeeConsecutiveUps24h(profile) {
		return []string{"churn-up-lock"}
	}
	if recent.ChangeCount24h >= maxAutofeeChanges24h(profile) {
		return []string{"churn-24h-lock"}
	}
	return nil
}

func hasRecentAutofeeOutrateMemory(st *autofeeChannelState, now time.Time) bool {
	if st == nil || st.LastOutrate <= 0 || st.LastOutrateTs.IsZero() {
		return false
	}
	return now.Sub(st.LastOutrateTs) <= 21*24*time.Hour
}

func hasRecentAutofeeRebalanceMemory(st *autofeeChannelState, now time.Time) bool {
	if st == nil || st.LastRebalCost <= 0 || st.LastRebalCostTs.IsZero() {
		return false
	}
	return now.Sub(st.LastRebalCostTs) <= 21*24*time.Hour
}

func hasFreshAutofeePressureSignal(recentForwards1d int, outAmt1dSat int64, recentRebalanceCount int, recentRebalanceWeakCount int, htlcPressureSignal bool, htlcForwardHot bool, newInboundBootstrap bool) bool {
	if newInboundBootstrap || htlcPressureSignal || htlcForwardHot {
		return true
	}
	if recentRebalanceCount > 0 || recentRebalanceWeakCount > 0 {
		return true
	}
	return recentForwards1d > 0 || outAmt1dSat > 0
}

func shouldBlockAutofeeIdleUpwardPressure(marketRefillMode bool, noFlow1d bool, target int, localPpm int, recentRebalanceCount int, recentRebalanceWeakCount int, htlcPressureSignal bool, htlcForwardHot bool, newInboundBootstrap bool, observedOutSignal bool, observedRebalSignal bool) bool {
	if marketRefillMode || !noFlow1d || target <= localPpm || newInboundBootstrap {
		return false
	}
	if recentRebalanceCount > 0 || recentRebalanceWeakCount > 0 {
		return false
	}
	if htlcPressureSignal || htlcForwardHot {
		return false
	}
	return !observedOutSignal && !observedRebalSignal
}

func shouldHoldSeedDrivenUpOnFullChannel(marketRefillMode bool, rawOutRatio float64, target int, localPpm int, outPpm7d int, rebalHistoryRefPpm int, baseCostSrc string) bool {
	if marketRefillMode || target <= localPpm {
		return false
	}
	if rawOutRatio < seedFullHoldOutRatioMin {
		return false
	}
	if outPpm7d > 0 || rebalHistoryRefPpm > 0 {
		return false
	}
	return strings.TrimSpace(baseCostSrc) == "seed"
}

func shouldRefreshAutofeeOutrateMemory(outPpm7d int, recentForwards1d int, outAmt1dSat int64) bool {
	if outPpm7d <= 0 {
		return false
	}
	return recentForwards1d > 0 || outAmt1dSat > 0
}

func isRebalanceBaseCostSource(src string) bool {
	switch strings.TrimSpace(src) {
	case "rebal", "rebal-global", "rebal-21d", "rebal-mem", "rebal-blend", "rebal-recent":
		return true
	default:
		return false
	}
}

func isOutrateBaseCostSource(src string) bool {
	switch strings.TrimSpace(src) {
	case "outrate", "outrate-mem":
		return true
	default:
		return false
	}
}

func floorSourceFromBaseCost(src string, marketRefillMode bool) string {
	if marketRefillMode {
		return "market"
	}
	switch {
	case isOutrateBaseCostSource(src):
		return "outrate"
	case strings.TrimSpace(src) == "seed":
		return "seed"
	case strings.TrimSpace(src) == "min":
		return "min"
	default:
		return "rebal"
	}
}

func formatAutofeeFloorSource(floorSrc string, floorBaseSrc string, floorBasePpm int) string {
	floorSrc = strings.TrimSpace(floorSrc)
	floorBaseSrc = strings.TrimSpace(floorBaseSrc)
	switch {
	case floorSrc == "" && floorBaseSrc == "":
		return ""
	case floorSrc == "":
		floorSrc = floorBaseSrc
	}
	if floorBaseSrc != "" && floorBasePpm > 0 {
		switch {
		case floorBaseSrc == floorSrc:
			floorSrc = fmt.Sprintf("%s≈%d", floorSrc, floorBasePpm)
		case floorSrc == "rebal" && strings.HasPrefix(floorBaseSrc, "rebal-"):
			floorSrc = fmt.Sprintf("%s≈%d", floorBaseSrc, floorBasePpm)
		case floorSrc == "outrate" && strings.HasPrefix(floorBaseSrc, "outrate-"):
			floorSrc = fmt.Sprintf("%s≈%d", floorBaseSrc, floorBasePpm)
		default:
			floorSrc = fmt.Sprintf("%s; base %s≈%d", floorSrc, floorBaseSrc, floorBasePpm)
		}
	}
	return fmt.Sprintf("(%s)", floorSrc)
}

func parseShortChannelID(raw string) (uint64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	if strings.Contains(trimmed, "x") {
		parts := strings.Split(trimmed, "x")
		if len(parts) != 3 {
			return 0, false
		}
		block, err1 := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
		tx, err2 := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		out, err3 := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return 0, false
		}
		if tx > 0xFFFFFF || out > 0xFFFF {
			return 0, false
		}
		return (block << 40) | (tx << 16) | out, true
	}
	chanID, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil || chanID == 0 {
		return 0, false
	}
	return chanID, true
}

func classifyHTLCFailure(entry htlcManagerFailedEntry) (bool, bool) {
	blob := htlcFailureBlob(entry)
	if blob == "" {
		return false, false
	}
	policy := containsAnyFailureToken(blob, htlcPolicyFailureTokens)
	liquidity := containsAnyFailureToken(blob, htlcLiquidityFailureTokens)
	return policy, liquidity
}

func htlcFailureBlob(entry htlcManagerFailedEntry) string {
	parts := []string{
		strings.TrimSpace(entry.FailureCode),
		strings.TrimSpace(entry.FailureDetail),
		strings.TrimSpace(entry.FailureReason),
		strings.TrimSpace(entry.Event),
	}
	normalized := []string{}
	for _, p := range parts {
		if p == "" {
			continue
		}
		token := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(p, "_", " "), "-", " "))
		normalized = append(normalized, token)
	}
	return strings.Join(normalized, " | ")
}

func normalizeHTLCFailureEvent(entry htlcManagerFailedEntry) string {
	event := strings.ToLower(strings.TrimSpace(entry.Event))
	switch event {
	case "link_fail", "forward_fail":
		return event
	}
	if strings.EqualFold(strings.TrimSpace(entry.FailureCode), "FORWARD_FAIL") {
		return "forward_fail"
	}
	if strings.TrimSpace(entry.FailureCode) != "" || strings.TrimSpace(entry.FailureDetail) != "" {
		return "link_fail"
	}
	if event == "" {
		return "unknown"
	}
	return event
}

func htlcFailureReasonKey(entry htlcManagerFailedEntry) string {
	candidates := []string{
		strings.TrimSpace(entry.FailureDetail),
		strings.TrimSpace(entry.FailureCode),
		strings.TrimSpace(entry.FailureReason),
		strings.TrimSpace(entry.Event),
	}
	for _, raw := range candidates {
		if raw == "" {
			continue
		}
		token := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(raw, "_", " "), "-", " "))
		token = strings.Join(strings.Fields(token), " ")
		if token == "" {
			continue
		}
		if len(token) > 64 {
			token = token[:64]
		}
		return token
	}
	return "UNKNOWN"
}

func summarizeTopReasonCounts(counts map[string]int, limit int) []string {
	if limit <= 0 || len(counts) == 0 {
		return nil
	}
	type item struct {
		key   string
		count int
	}
	items := make([]item, 0, len(counts))
	for key, count := range counts {
		if count <= 0 {
			continue
		}
		items = append(items, item{key: key, count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].key < items[j].key
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, fmt.Sprintf("%s:%d", it.key, it.count))
	}
	return out
}

func containsAnyFailureToken(blob string, terms []string) bool {
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(blob, term) {
			return true
		}
	}
	return false
}

func (e *autofeeEngine) fetchForwardStats(ctx context.Context, lookback int) (map[uint64]forwardStat, error) {
	rows, err := e.svc.db.Query(ctx, `
select coalesce(chan_id_out, channel_id) as chan_id,
  coalesce(sum(
    case
      when fee_msat > 0 then fee_msat
      when fee_sat > 0 then fee_sat * 1000
      when amount_in_msat > 0 and amount_out_msat > 0 and amount_in_msat > amount_out_msat then amount_in_msat - amount_out_msat
      else 0
    end
  ), 0),
  coalesce(sum(case when amount_out_msat > 0 then amount_out_msat else amount_sat * 1000 end), 0),
  count(*)
from notifications
where type='forward' and occurred_at >= now() - ($1 * interval '1 day')
  and coalesce(chan_id_out, channel_id) is not null
group by coalesce(chan_id_out, channel_id)
`, lookback)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint64]forwardStat{}
	for rows.Next() {
		var chanID int64
		var feeMsat int64
		var amtMsat int64
		var count int64
		if err := rows.Scan(&chanID, &feeMsat, &amtMsat, &count); err != nil {
			return nil, err
		}
		out[uint64(chanID)] = forwardStat{FeeMsat: feeMsat, AmtMsat: amtMsat, Count: count}
	}
	return out, rows.Err()
}

func (e *autofeeEngine) fetchInboundStats(ctx context.Context, lookback int) (map[uint64]inboundStat, error) {
	rows, err := e.svc.db.Query(ctx, `
select chan_id_in, coalesce(sum(amount_in_msat), 0), count(*)
from notifications
where type='forward' and occurred_at >= now() - ($1 * interval '1 day')
  and chan_id_in is not null
group by chan_id_in
`, lookback)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint64]inboundStat{}
	for rows.Next() {
		var chanID int64
		var amtMsat int64
		var count int64
		if err := rows.Scan(&chanID, &amtMsat, &count); err != nil {
			return nil, err
		}
		out[uint64(chanID)] = inboundStat{AmtMsat: amtMsat, Count: count}
	}
	return out, rows.Err()
}

func (e *autofeeEngine) fetchRebalanceStats(ctx context.Context, lookback int) (rebalStats, error) {
	stats := rebalStats{ByChannel: map[uint64]rebalStat{}}
	rows, err := e.svc.db.Query(ctx, `
select coalesce(rebal_target_chan_id, channel_id) as chan_id,
  coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0),
  coalesce(sum(amount_sat), 0),
  count(*)::bigint
from notifications
where type='rebalance' and occurred_at >= now() - ($1 * interval '1 day')
  and status in ('SETTLED', 'SUCCEEDED')
  and coalesce(rebal_target_chan_id, channel_id) is not null
group by coalesce(rebal_target_chan_id, channel_id)
`, lookback)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var chanID int64
		var feeMsat int64
		var amtSat int64
		var count int64
		if err := rows.Scan(&chanID, &feeMsat, &amtSat, &count); err != nil {
			return stats, err
		}
		stats.ByChannel[uint64(chanID)] = rebalStat{FeeMsat: feeMsat, AmtMsat: amtSat * 1000, Count: count}
	}
	if err := rows.Err(); err != nil {
		return stats, err
	}

	err = e.svc.db.QueryRow(ctx, `
select coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0),
  coalesce(sum(amount_sat), 0),
  count(*)::bigint
from notifications
where type='rebalance' and occurred_at >= now() - ($1 * interval '1 day')
  and status in ('SETTLED', 'SUCCEEDED')
`, lookback).Scan(&stats.Global.FeeMsat, &stats.Global.AmtMsat, &stats.Global.Count)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return stats, err
	}
	stats.Global.AmtMsat = stats.Global.AmtMsat * 1000
	return stats, nil
}

func (e *autofeeEngine) fetchRebalanceMovementStats(ctx context.Context, lookback int) (map[uint64]rebalStat, error) {
	rows, err := e.svc.db.Query(ctx, `
select chan_id,
  coalesce(sum(amount_sat), 0),
  count(*)::bigint
from (
  select coalesce(rebal_target_chan_id, channel_id) as chan_id, amount_sat
  from notifications
  where type='rebalance' and occurred_at >= now() - ($1 * interval '1 day')
    and status in ('SETTLED', 'SUCCEEDED')
    and coalesce(rebal_target_chan_id, channel_id) is not null
  union all
  select rebal_source_chan_id as chan_id, amount_sat
  from notifications
  where type='rebalance' and occurred_at >= now() - ($1 * interval '1 day')
    and status in ('SETTLED', 'SUCCEEDED')
    and rebal_source_chan_id is not null
) movement
where chan_id is not null
group by chan_id
`, lookback)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uint64]rebalStat{}
	for rows.Next() {
		var chanID int64
		var amtSat int64
		var count int64
		if err := rows.Scan(&chanID, &amtSat, &count); err != nil {
			return out, err
		}
		if chanID <= 0 {
			continue
		}
		out[uint64(chanID)] = rebalStat{AmtMsat: amtSat * 1000, Count: count}
	}
	return out, rows.Err()
}

func (e *autofeeEngine) fetchRecentRebalanceTouches(ctx context.Context, successWindow time.Duration, weakWindow time.Duration) (map[uint64]recentRebalanceSignal, error) {
	touches := map[uint64]recentRebalanceSignal{}
	if successWindow <= 0 {
		successWindow = time.Hour
	}
	if weakWindow <= 0 {
		weakWindow = successWindow
	}
	if weakWindow < successWindow {
		weakWindow = successWindow
	}
	successSeconds := int(math.Ceil(successWindow.Seconds()))
	weakSeconds := int(math.Ceil(weakWindow.Seconds()))
	if successSeconds < 60 {
		successSeconds = 60
	}
	if weakSeconds < successSeconds {
		weakSeconds = successSeconds
	}
	successRows, err := e.svc.db.Query(ctx, `
select
  j.target_channel_id as chan_id,
  count(*)::bigint as success_count,
  coalesce(sum(coalesce(a.amount_sat, 0)), 0)::bigint as success_amt_sat,
  coalesce(sum(coalesce(a.fee_paid_sat, 0) * 1000), 0)::bigint as success_fee_msat,
  max(coalesce(a.finished_at, a.started_at)) as last_success_at
from rebalance_attempts a
join rebalance_jobs j on j.id = a.job_id
where j.target_channel_id is not null
  and lower(coalesce(a.status, '')) = 'succeeded'
  and coalesce(a.finished_at, a.started_at) >= now() - ($1 * interval '1 second')
group by j.target_channel_id
`, successSeconds)
	if err != nil {
		return touches, err
	}
	defer successRows.Close()

	for successRows.Next() {
		var chanID int64
		var successCount int64
		var successAmtSat int64
		var successFeeMsat int64
		var lastSuccessAt time.Time
		if err := successRows.Scan(&chanID, &successCount, &successAmtSat, &successFeeMsat, &lastSuccessAt); err != nil {
			return touches, err
		}
		if chanID <= 0 {
			continue
		}
		sig := touches[uint64(chanID)]
		sig.Count += int(successCount)
		sig.AmtSat += successAmtSat
		sig.FeeMsat += successFeeMsat
		if !lastSuccessAt.IsZero() && (sig.LastAt.IsZero() || lastSuccessAt.After(sig.LastAt)) {
			sig.LastAt = lastSuccessAt
		}
		touches[uint64(chanID)] = sig
	}
	if err := successRows.Err(); err != nil {
		return touches, err
	}

	weakRows, err := e.svc.db.Query(ctx, `
select
  j.target_channel_id as chan_id,
  coalesce(j.completed_at, j.created_at) as event_ts,
  coalesce(j.target_amount_sat, 0)::bigint as amount_sat
from rebalance_jobs j
where j.target_channel_id is not null
  and lower(coalesce(j.status, '')) in ('failed', 'cancelled')
  and coalesce(j.completed_at, j.created_at) >= now() - ($1 * interval '1 second')
order by j.target_channel_id, coalesce(j.completed_at, j.created_at)
`, weakSeconds)
	if err != nil {
		return touches, err
	}
	defer weakRows.Close()

	weakJobs := []recentWeakRebalanceJob{}
	for weakRows.Next() {
		var chanID int64
		var eventTs time.Time
		var amountSat int64
		if err := weakRows.Scan(&chanID, &eventTs, &amountSat); err != nil {
			return touches, err
		}
		if chanID <= 0 || eventTs.IsZero() {
			continue
		}
		if sig, ok := touches[uint64(chanID)]; ok && sig.Count > 0 && !sig.LastAt.IsZero() && !eventTs.After(sig.LastAt) {
			continue
		}
		weakJobs = append(weakJobs, recentWeakRebalanceJob{
			ChannelID: uint64(chanID),
			Ts:        eventTs,
			AmtSat:    amountSat,
		})
	}
	if err := weakRows.Err(); err != nil {
		return touches, err
	}

	for chanID, weakSig := range collapseWeakRebalanceCampaigns(weakJobs, e.rebalanceFailureCampaignGap()) {
		sig := touches[chanID]
		sig.WeakCount += weakSig.WeakCount
		sig.WeakAmtSat += weakSig.WeakAmtSat
		if !weakSig.WeakLastAt.IsZero() && (sig.WeakLastAt.IsZero() || weakSig.WeakLastAt.After(sig.WeakLastAt)) {
			sig.WeakLastAt = weakSig.WeakLastAt
		}
		touches[chanID] = sig
	}
	return touches, nil
}

func (e *autofeeEngine) loadState(ctx context.Context) (map[uint64]*autofeeChannelState, error) {
	items := map[uint64]*autofeeChannelState{}
	rows, err := e.svc.db.Query(ctx, `
select channel_id, last_ppm, last_inbound_discount_ppm, last_seed_ppm, last_outrate_ppm, last_outrate_ts,
  last_rebal_cost_ppm, last_rebal_cost_ts, last_idle_refresh_ts, last_idle_refresh_ppm, last_idle_refresh_source,
  last_ts, last_dir, low_streak, stalled_rounds, baseline_fwd7d, class_label, class_conf, bias_ema,
  first_seen_ts, ss_active, ss_ok_since, ss_bad_since, explorer_state,
  apply_error_streak, last_apply_error, apply_error_alerted,
  seed_shock_pending_ppm, seed_shock_rounds, seed_shock_dir
from autofee_state
`)
	if err != nil {
		return items, err
	}
	defer rows.Close()

	for rows.Next() {
		var channelID int64
		var st autofeeChannelState
		var lastPpm pgtype.Int4
		var lastInb pgtype.Int4
		var lastSeed pgtype.Int4
		var seedShockPending pgtype.Int4
		var seedShockRounds int
		var seedShockDir pgtype.Text
		var lastOut pgtype.Int4
		var lastOutTs pgtype.Timestamptz
		var lastRebal pgtype.Int4
		var lastRebalTs pgtype.Timestamptz
		var lastIdleRefreshTs pgtype.Timestamptz
		var lastIdleRefreshPpm pgtype.Int4
		var lastIdleRefreshSource pgtype.Text
		var lastTs pgtype.Timestamptz
		var lastDir pgtype.Text
		var lowStreak int
		var stalledRounds int
		var baseline int
		var classLabel pgtype.Text
		var classConf pgtype.Float8
		var biasEma pgtype.Float8
		var firstSeen pgtype.Timestamptz
		var ssActive pgtype.Bool
		var ssOkSince pgtype.Timestamptz
		var ssBadSince pgtype.Timestamptz
		var explorerRaw []byte
		var applyErrorStreak int
		var lastApplyError pgtype.Text
		var applyErrorAlerted bool
		if err := rows.Scan(&channelID, &lastPpm, &lastInb, &lastSeed, &lastOut, &lastOutTs, &lastRebal, &lastRebalTs,
			&lastIdleRefreshTs, &lastIdleRefreshPpm, &lastIdleRefreshSource, &lastTs, &lastDir,
			&lowStreak, &stalledRounds, &baseline, &classLabel, &classConf, &biasEma, &firstSeen, &ssActive, &ssOkSince, &ssBadSince, &explorerRaw,
			&applyErrorStreak, &lastApplyError, &applyErrorAlerted,
			&seedShockPending, &seedShockRounds, &seedShockDir); err != nil {
			return items, err
		}
		st.ApplyErrorStreak = applyErrorStreak
		if lastApplyError.Valid {
			st.LastApplyError = lastApplyError.String
		}
		st.ApplyErrorAlerted = applyErrorAlerted
		st.ChannelID = uint64(channelID)
		if lastPpm.Valid {
			st.LastPpm = int(lastPpm.Int32)
		}
		if lastInb.Valid {
			st.LastInboundDiscount = int(lastInb.Int32)
		}
		if lastSeed.Valid {
			st.LastSeed = int(lastSeed.Int32)
		}
		if seedShockPending.Valid {
			st.SeedShockPendingPpm = int(seedShockPending.Int32)
		}
		st.SeedShockRounds = seedShockRounds
		if seedShockDir.Valid {
			st.SeedShockDir = seedShockDir.String
		}
		if lastOut.Valid {
			st.LastOutrate = int(lastOut.Int32)
		}
		if lastOutTs.Valid {
			st.LastOutrateTs = lastOutTs.Time
		}
		if lastRebal.Valid {
			st.LastRebalCost = int(lastRebal.Int32)
		}
		if lastRebalTs.Valid {
			st.LastRebalCostTs = lastRebalTs.Time
		}
		if lastIdleRefreshTs.Valid {
			st.LastIdleRefreshTs = lastIdleRefreshTs.Time
		}
		if lastIdleRefreshPpm.Valid {
			st.LastIdleRefreshPpm = int(lastIdleRefreshPpm.Int32)
		}
		if lastIdleRefreshSource.Valid {
			st.LastIdleRefreshSrc = lastIdleRefreshSource.String
		}
		if lastTs.Valid {
			st.LastTs = lastTs.Time
		}
		if lastDir.Valid {
			st.LastDir = lastDir.String
		}
		st.LowStreak = lowStreak
		st.StalledRounds = stalledRounds
		st.BaselineFwd7d = baseline
		if classLabel.Valid {
			st.ClassLabel = classLabel.String
		}
		if classConf.Valid {
			st.ClassConf = classConf.Float64
		}
		if biasEma.Valid {
			st.BiasEma = biasEma.Float64
		}
		if firstSeen.Valid {
			st.FirstSeen = firstSeen.Time
		}
		if ssActive.Valid {
			st.SuperSourceActive = ssActive.Bool
		}
		if ssOkSince.Valid {
			st.SuperSourceOkSince = ssOkSince.Time
		}
		if ssBadSince.Valid {
			st.SuperSourceBadSince = ssBadSince.Time
		}
		if len(explorerRaw) > 0 {
			_ = json.Unmarshal(explorerRaw, &st.ExplorerState)
		}
		items[uint64(channelID)] = &st
	}
	return items, rows.Err()
}

func (e *autofeeEngine) loadChannelRankingSnapshots(ctx context.Context) (map[string]autofeeRankingSnapshot, error) {
	items := map[string]autofeeRankingSnapshot{}
	if e == nil || e.svc == nil || e.svc.db == nil {
		return items, nil
	}
	rows, err := e.svc.db.Query(ctx, `
select channel_point, score, score_30d, state, trend_direction, trend_delta, profit_fee_7d_sat, profit_fee_30d_sat, out_ppm_30d, rebal_ppm_30d, peer_stability_score_30d, local_balance_pct,
       forward_in_count_7d, forward_in_amount_sat_7d, forward_out_count_7d, forward_out_amount_sat_7d, assisted_forward_fee_7d_sat, rebalance_dependence_score
from channel_rankings
`)
	if err != nil {
		return items, err
	}
	defer rows.Close()

	for rows.Next() {
		var point string
		var snap autofeeRankingSnapshot
		var state sql.NullString
		var trend sql.NullString
		if err := rows.Scan(
			&point,
			&snap.Score,
			&snap.Score30d,
			&state,
			&trend,
			&snap.TrendDelta,
			&snap.ProfitFee7dSat,
			&snap.ProfitFee30dSat,
			&snap.OutPpm30d,
			&snap.RebalPpm30d,
			&snap.PeerStabilityScore,
			&snap.LocalBalancePct,
			&snap.ForwardInCount7d,
			&snap.ForwardInAmountSat7d,
			&snap.ForwardOutCount7d,
			&snap.ForwardOutAmountSat7d,
			&snap.AssistedForwardFee7dSat,
			&snap.RebalanceDependence,
		); err != nil {
			return items, err
		}
		point = normalizeChannelPointKey(point)
		if point == "" {
			continue
		}
		if state.Valid {
			snap.State = strings.TrimSpace(strings.ToLower(state.String))
		}
		if trend.Valid {
			snap.TrendDirection = strings.TrimSpace(strings.ToLower(trend.String))
		}
		items[point] = snap
	}
	return items, rows.Err()
}

func (e *autofeeEngine) persistState(ctx context.Context, st *autofeeChannelState) {
	if st == nil {
		return
	}
	rawExplorer, _ := json.Marshal(st.ExplorerState)
	_, _ = e.svc.db.Exec(ctx, `
insert into autofee_state (
  channel_id, last_ppm, last_inbound_discount_ppm, last_seed_ppm, last_outrate_ppm, last_outrate_ts,
  last_rebal_cost_ppm, last_rebal_cost_ts, last_idle_refresh_ts, last_idle_refresh_ppm, last_idle_refresh_source,
  last_ts, last_dir, low_streak, stalled_rounds, baseline_fwd7d, class_label, class_conf, bias_ema,
  first_seen_ts, ss_active, ss_ok_since, ss_bad_since, explorer_state,
  apply_error_streak, last_apply_error, apply_error_alerted,
  seed_shock_pending_ppm, seed_shock_rounds, seed_shock_dir
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)
`+
		`on conflict (channel_id) do update set
  last_ppm=excluded.last_ppm,
  last_inbound_discount_ppm=excluded.last_inbound_discount_ppm,
  last_seed_ppm=excluded.last_seed_ppm,
  seed_shock_pending_ppm=excluded.seed_shock_pending_ppm,
  seed_shock_rounds=excluded.seed_shock_rounds,
  seed_shock_dir=excluded.seed_shock_dir,
  last_outrate_ppm=excluded.last_outrate_ppm,
  last_outrate_ts=excluded.last_outrate_ts,
  last_rebal_cost_ppm=excluded.last_rebal_cost_ppm,
  last_rebal_cost_ts=excluded.last_rebal_cost_ts,
  last_idle_refresh_ts=excluded.last_idle_refresh_ts,
  last_idle_refresh_ppm=excluded.last_idle_refresh_ppm,
  last_idle_refresh_source=excluded.last_idle_refresh_source,
  last_ts=excluded.last_ts,
  last_dir=excluded.last_dir,
  low_streak=excluded.low_streak,
  stalled_rounds=excluded.stalled_rounds,
  baseline_fwd7d=excluded.baseline_fwd7d,
  class_label=excluded.class_label,
  class_conf=excluded.class_conf,
  bias_ema=excluded.bias_ema,
  first_seen_ts=excluded.first_seen_ts,
  ss_active=excluded.ss_active,
  ss_ok_since=excluded.ss_ok_since,
  ss_bad_since=excluded.ss_bad_since,
  explorer_state=excluded.explorer_state,
  apply_error_streak=excluded.apply_error_streak,
  last_apply_error=excluded.last_apply_error,
  apply_error_alerted=excluded.apply_error_alerted
`, int64(st.ChannelID), nullableInt(int64(st.LastPpm)), nullableInt(int64(st.LastInboundDiscount)),
		nullableInt(int64(st.LastSeed)), nullableInt(int64(st.LastOutrate)), nullableTime(st.LastOutrateTs),
		nullableInt(int64(st.LastRebalCost)), nullableTime(st.LastRebalCostTs), nullableTime(st.LastIdleRefreshTs),
		nullableInt(int64(st.LastIdleRefreshPpm)), nullableString(st.LastIdleRefreshSrc), nullableTime(st.LastTs),
		nullableString(st.LastDir), st.LowStreak, st.StalledRounds, st.BaselineFwd7d, nullableString(st.ClassLabel), nullableFloat(st.ClassConf),
		nullableFloat(st.BiasEma), nullableTime(st.FirstSeen), st.SuperSourceActive,
		nullableTime(st.SuperSourceOkSince), nullableTime(st.SuperSourceBadSince), rawExplorer,
		st.ApplyErrorStreak, nullableString(st.LastApplyError), st.ApplyErrorAlerted,
		nullableInt(int64(st.SeedShockPendingPpm)), st.SeedShockRounds, nullableString(st.SeedShockDir),
	)
}

// persistApplyErrorState updates only the apply-error tracking fields. Used
// after applyDecision so the streak survives across runs without rewriting
// the rest of the state row (which was already persisted before apply).
func (e *autofeeEngine) persistApplyErrorState(ctx context.Context, channelID uint64, streak int, lastError string, alerted bool) {
	if e == nil || e.svc == nil || e.svc.db == nil {
		return
	}
	upCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := e.svc.db.Exec(upCtx, `
update autofee_state
set apply_error_streak=$2, last_apply_error=$3, apply_error_alerted=$4
where channel_id=$1
`, int64(channelID), streak, nullableString(lastError), alerted); err != nil && e.svc.logger != nil {
		e.svc.logger.Printf("autofee: persistApplyErrorState failed for channel %d: %v", channelID, err)
	}
}

// ===== decisions =====

type decision struct {
	ChannelID               uint64
	ChannelPoint            string
	Alias                   string
	LocalPpm                int
	NewPpm                  int
	Target                  int
	TargetRaw               int
	TargetFinal             int
	Floor                   int
	FloorSrc                string
	FloorBasePpm            int
	FloorBaseSrc            string
	RebalCostMode           string
	Tags                    []string
	InboundDiscount         int
	PrevInboundDiscount     int
	SuperSourceActive       bool
	OutRatio                float64
	OutRatioEffective       float64
	OutPpm7d                int
	RebalPpm                int
	Seed                    int
	Margin                  int
	RevShare                float64
	ClassLabel              string
	FwdCount                int
	NegMarginGlobal         bool
	PredictionCode          string
	PredictionCooldownHours int
	NewInbound              bool
	ChannelAgeHours         float64
	HTLCAttempts            int
	HTLCForwardFails        int
	HTLCPolicyFails         int
	HTLCLiquidityFails      int
	HTLCUnclassifiedFails   int
	HTLCWindowMin           int
	StagnationPhase         int
	StagnationRounds        int
	StagnationCap           int
	StalledRounds           int
	HoursSinceLastChange    float64
	TargetGapPpm            int
	TargetGapPct            float64
	Apply                   bool
	Error                   error
	State                   *autofeeChannelState
}

func rescueCandidate(r autofeeRankingSnapshot, localPpm int, target int, outPpm7d int, revShare float64, topRevenue bool, slowCycleProtected bool) (bool, bool) {
	if topRevenue || slowCycleProtected {
		return false, false
	}
	stateWeak := r.State == "close" || (r.State == "monitor" && r.TrendDirection == "worsening")
	if !stateWeak || r.ProfitFee7dSat > 0 || r.Score > rescueScoreMax || revShare > rescueRevShareMax {
		return false, false
	}
	if localPpm < int(math.Ceil(float64(maxInt(target, 1))*(1.0+rescueEnterGapFrac))) {
		return false, false
	}
	priority := r.State == "close" && localPpm >= int(math.Ceil(float64(maxInt(target, 1))*(1.0+rescuePriorityEnterGapFrac)))
	if outPpm7d > 0 {
		if localPpm >= int(math.Ceil(float64(outPpm7d)*rescueOutrateEnterMult)) {
			return true, priority
		}
		if target <= int(math.Floor(float64(outPpm7d)*(1.0-rescueTargetOutrateDivergenceFrac))) {
			return true, priority
		}
	}
	return outPpm7d <= 0, priority
}

func rescueExitReady(es explorerState, now time.Time) bool {
	if !es.RescueActive || es.RescueStartedTs <= 0 {
		return false
	}
	activeHours := now.Sub(time.Unix(es.RescueStartedTs, 0).UTC()).Hours()
	return es.RescueRounds >= rescueMinRounds || activeHours >= rescueMinActiveHours
}

func rescueRecovered(r autofeeRankingSnapshot, localPpm int, target int, outPpm7d int, revShare float64, topRevenue bool) bool {
	if topRevenue || revShare > rescueRevShareMax {
		return true
	}
	if r.ProfitFee7dSat > 0 && (r.State == "expand" || r.Score > rescueScoreMax || r.TrendDirection != "worsening") {
		return true
	}
	exitTarget := int(math.Ceil(float64(maxInt(target, 1)) * (1.0 + rescueExitGapFrac)))
	exitFloor := exitTarget
	if outPpm7d > 0 {
		outExit := int(math.Ceil(float64(outPpm7d) * rescueOutrateExitMult))
		if outExit > exitFloor {
			exitFloor = outExit
		}
	}
	return localPpm <= exitFloor
}

func manageRescueState(st *autofeeChannelState, now time.Time, balancedMode bool, ranking autofeeRankingSnapshot, hasRanking bool, localPpm int, target int, outPpm7d int, revShare float64, topRevenue bool, slowCycleProtected bool) (bool, []string) {
	if st == nil {
		return false, nil
	}
	es := &st.ExplorerState
	if !balancedMode {
		if es.RescueActive {
			es.RescueActive = false
			es.RescueRounds = 0
			es.RescueRecoverRounds = 0
			es.RescueStartedTs = 0
			es.RescueLastExitTs = now.Unix()
		}
		return false, nil
	}
	if !hasRanking {
		if es.RescueActive {
			es.RescueRounds++
			return true, []string{"rescue", fmt.Sprintf("rescue-r%d", maxInt(1, es.RescueRounds))}
		}
		return false, nil
	}
	candidate, priorityCandidate := rescueCandidate(ranking, localPpm, target, outPpm7d, revShare, topRevenue, slowCycleProtected)
	if es.RescueActive {
		es.RescueRounds++
		activeHours := 0.0
		if es.RescueStartedTs > 0 {
			activeHours = now.Sub(time.Unix(es.RescueStartedTs, 0).UTC()).Hours()
		}
		if activeHours >= rescueMaxActiveHours {
			es.RescueActive = false
			es.RescueLastExitTs = now.Unix()
			es.RescueStartedTs = 0
			es.RescueRounds = 0
			es.RescueRecoverRounds = 0
			return false, []string{"rescue-expired"}
		}
		if rescueExitReady(*es, now) && (!candidate || rescueRecovered(ranking, localPpm, target, outPpm7d, revShare, topRevenue)) {
			es.RescueRecoverRounds++
		} else {
			es.RescueRecoverRounds = 0
		}
		if es.RescueRecoverRounds >= rescueExitConfirmRounds {
			es.RescueActive = false
			es.RescueLastExitTs = now.Unix()
			es.RescueStartedTs = 0
			es.RescueRounds = 0
			es.RescueRecoverRounds = 0
			return false, []string{"rescue-exit"}
		}
		return true, []string{"rescue", fmt.Sprintf("rescue-r%d", maxInt(1, es.RescueRounds))}
	}
	if !candidate {
		return false, nil
	}
	if !priorityCandidate && es.RescueLastExitTs > 0 && now.Sub(time.Unix(es.RescueLastExitTs, 0).UTC()).Hours() < rescueReentryCooldownHours {
		return false, nil
	}
	es.RescueActive = true
	es.RescueStartedTs = now.Unix()
	es.RescueRounds = 1
	es.RescueRecoverRounds = 0
	return true, []string{"rescue", "rescue-enter", "rescue-r1"}
}

func applyRescueFloorRelax(active bool, localPpm int, target int, floor int, floorSrc string, outPpm7d int, baseCostPpm int) (int, string, []string) {
	if !active || target >= localPpm || floor <= target {
		return floor, floorSrc, nil
	}
	relaxed := target
	if outPpm7d > 0 {
		relaxed = maxInt(relaxed, outPpm7d)
	}
	if baseCostPpm > 0 {
		relaxed = maxInt(relaxed, int(math.Ceil(float64(baseCostPpm)*rescueSoftRebalFloorMult)))
	}
	if relaxed >= floor {
		return floor, floorSrc, nil
	}
	return relaxed, "rescue", []string{"rescue-floor-relax"}
}

func (d *decision) withError(err error) *decision {
	d.Error = err
	return d
}

func formatAutofeeDecisionLine(d *decision, dryRun bool, isError bool) (string, string) {
	if d == nil {
		return "", "kept"
	}
	alias := strings.TrimSpace(d.Alias)
	if alias == "" {
		alias = fmt.Sprintf("chan-%d", d.ChannelID)
	}
	dir := "➡️"
	if d.NewPpm > d.LocalPpm {
		dir = "🔺"
	} else if d.NewPpm < d.LocalPpm {
		dir = "🔻"
	}
	action := ""
	category := "kept"
	if isError {
		action = fmt.Sprintf("erro: %v", d.Error)
		category = "error"
	} else if d.Apply {
		if dryRun {
			action = fmt.Sprintf("DRY set %d→%d ppm", d.LocalPpm, d.NewPpm)
		} else {
			action = fmt.Sprintf("set %d→%d ppm", d.LocalPpm, d.NewPpm)
		}
		category = "changed"
	} else {
		action = fmt.Sprintf("mantém %d ppm", d.LocalPpm)
		if hasAutofeeSkipTag(d.Tags) {
			category = "skipped"
		}
	}

	deltaStr := ""
	if d.LocalPpm > 0 && d.NewPpm != d.LocalPpm {
		delta := d.NewPpm - d.LocalPpm
		pct := math.Abs(float64(delta)) / float64(d.LocalPpm) * 100.0
		deltaStr = fmt.Sprintf(" (%+d, %.1f%%)", delta, pct)
	}

	floorSrc := formatAutofeeFloorSource(d.FloorSrc, d.FloorBaseSrc, d.FloorBasePpm)
	tagLine := formatAutofeeTags(d)
	if d.InboundDiscount > 0 {
		tagLine = strings.ReplaceAll(tagLine, fmt.Sprintf(" ↘️inb-%d", d.InboundDiscount), "")
		tagLine = strings.ReplaceAll(tagLine, fmt.Sprintf("↘️inb-%d ", d.InboundDiscount), "")
		tagLine = strings.TrimSpace(tagLine)
	}
	if tagLine == "" {
		tagLine = "-"
	}
	prefix := "🫤"
	if category == "changed" {
		prefix = "✅" + dir
	} else if category == "skipped" {
		if containsTag(d.Tags, "cooldown") {
			prefix = "⏭️⏳"
		} else if containsTag(d.Tags, "hold-small") {
			prefix = "⏭️🧊"
		} else {
			prefix = "⏭️"
		}
	} else if category == "error" {
		prefix = "❌"
	} else if containsTag(d.Tags, "same-ppm") {
		prefix = "🫤⏸️"
	}

	targetRaw := d.TargetRaw
	if targetRaw <= 0 {
		targetRaw = d.Target
	}
	targetFinal := d.TargetFinal
	if targetFinal <= 0 {
		targetFinal = d.NewPpm
	}
	targetDisplay := fmt.Sprintf("%d", targetRaw)
	if targetFinal != targetRaw {
		targetDisplay = fmt.Sprintf("%d→%d", targetRaw, targetFinal)
	}
	line := fmt.Sprintf("%s %s: %s%s | alvo %s | out_ratio %.2f | out_ppm7d≈%d | rebal_ppm7d≈%d | seed≈%d | floor≥%d%s | marg≈%d | rev_share≈%.2f | %s",
		prefix,
		alias,
		action,
		deltaStr,
		targetDisplay,
		d.OutRatio,
		d.OutPpm7d,
		d.RebalPpm,
		d.Seed,
		d.Floor,
		floorSrc,
		d.Margin,
		d.RevShare,
		tagLine,
	)
	if d.PrevInboundDiscount != d.InboundDiscount {
		line = line + fmt.Sprintf(" | ↘️ inb %d→%d", d.PrevInboundDiscount, d.InboundDiscount)
	} else if d.InboundDiscount > 0 {
		line = line + fmt.Sprintf(" | ↘️ inb %d", d.InboundDiscount)
	}
	if d.NewInbound {
		line = line + fmt.Sprintf(" | NEW inbound %.1fh", d.ChannelAgeHours)
	}
	if d.HTLCAttempts > 0 {
		line = line + fmt.Sprintf(" | htlc%dm a=%d p=%d l=%d f=%d u=%d",
			maxInt(1, d.HTLCWindowMin), d.HTLCAttempts, d.HTLCPolicyFails, d.HTLCLiquidityFails, d.HTLCForwardFails, d.HTLCUnclassifiedFails)
	}
	if d.StagnationPhase > 0 {
		line = line + fmt.Sprintf(" | stagnation p%d r%d", d.StagnationPhase, maxInt(0, d.StagnationRounds))
		if d.StagnationCap > 0 {
			line = line + fmt.Sprintf(" cap≤%d", d.StagnationCap)
		}
	}
	if d.NewPpm == d.LocalPpm && (d.StalledRounds > 0 || d.TargetGapPpm != 0) {
		line = line + fmt.Sprintf(" | stall r=%d h=%.1f gap=%+d(%.1f%%)",
			d.StalledRounds, d.HoursSinceLastChange, d.TargetGapPpm, d.TargetGapPct)
	}
	return strings.TrimSpace(line), category
}

func buildAutofeeChannelLogEntry(d *decision, category string, dryRun bool, err error) autofeeLogEntry {
	if d == nil {
		return autofeeLogEntry{}
	}
	if category == "" {
		category = "kept"
	}
	line, _ := formatAutofeeDecisionLine(d, dryRun, err != nil)
	delta := d.NewPpm - d.LocalPpm
	deltaPct := 0.0
	if d.LocalPpm > 0 && d.NewPpm != d.LocalPpm {
		deltaPct = math.Abs(float64(delta)) / float64(d.LocalPpm) * 100.0
	}
	skipReason := ""
	if !d.Apply {
		if containsTag(d.Tags, "cooldown") {
			skipReason = "cooldown"
		} else if containsTag(d.Tags, "small-delta") {
			skipReason = "small-delta"
		} else if containsTag(d.Tags, "hold-small") {
			skipReason = "hold-small"
		} else if containsTag(d.Tags, "autofee_settling") {
			skipReason = "autofee_settling"
		} else if containsTag(d.Tags, "same-ppm") {
			skipReason = "same-ppm"
		}
	}
	payload := &autofeeLogItem{
		Kind:                    "channel",
		Category:                category,
		DryRun:                  dryRun,
		Alias:                   d.Alias,
		ChannelID:               d.ChannelID,
		ChannelPoint:            d.ChannelPoint,
		LocalPpm:                d.LocalPpm,
		NewPpm:                  d.NewPpm,
		Target:                  d.Target,
		TargetRaw:               d.TargetRaw,
		TargetFinal:             d.TargetFinal,
		OutRatio:                d.OutRatio,
		OutRatioEffective:       d.OutRatioEffective,
		OutPpm7d:                d.OutPpm7d,
		RebalPpm7d:              d.RebalPpm,
		Seed:                    d.Seed,
		Floor:                   d.Floor,
		FloorSrc:                d.FloorSrc,
		FloorBasePpm:            d.FloorBasePpm,
		FloorBaseSrc:            d.FloorBaseSrc,
		RebalCostMode:           d.RebalCostMode,
		Margin:                  d.Margin,
		RevShare:                d.RevShare,
		Tags:                    append([]string{}, d.Tags...),
		InboundDiscount:         d.InboundDiscount,
		PrevInboundDiscount:     d.PrevInboundDiscount,
		ClassLabel:              d.ClassLabel,
		SkipReason:              skipReason,
		Delta:                   delta,
		DeltaPct:                deltaPct,
		PredictionCode:          d.PredictionCode,
		PredictionCooldownHours: d.PredictionCooldownHours,
		NewInbound:              d.NewInbound,
		ChannelAgeHours:         d.ChannelAgeHours,
		HTLCAttempts:            d.HTLCAttempts,
		HTLCForwardFails:        d.HTLCForwardFails,
		HTLCPolicyFails:         d.HTLCPolicyFails,
		HTLCLiquidityFails:      d.HTLCLiquidityFails,
		HTLCUnclassifiedFails:   d.HTLCUnclassifiedFails,
		HTLCWindowMinChannel:    d.HTLCWindowMin,
		StagnationPhase:         d.StagnationPhase,
		StagnationRounds:        d.StagnationRounds,
		StagnationCap:           d.StagnationCap,
		StalledRounds:           d.StalledRounds,
		HoursSinceLastChange:    d.HoursSinceLastChange,
		TargetGapPpm:            d.TargetGapPpm,
		TargetGapPct:            d.TargetGapPct,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return autofeeLogEntry{Line: line, Payload: payload}
}

func (s *AutofeeService) sendTelegramAutofeeRunSummary(cfg AutofeeConfig, reason string, runAt time.Time, summary autofeeRunSummary, calib autofeeCalibration, changed []*decision) {
	if s.db == nil || len(changed) == 0 {
		return
	}
	tgCfg := readTelegramBackupConfig()
	if strings.TrimSpace(tgCfg.BotToken) == "" || strings.TrimSpace(tgCfg.ChatID) == "" {
		return
	}

	loadCtx, loadCancel := context.WithTimeout(context.Background(), 5*time.Second)
	settings, err := loadTelegramNotificationSettings(loadCtx, s.db)
	loadCancel()
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("autofee: telegram settings unavailable: %v", err)
		}
		return
	}
	if !settings.AutofeeSummaryEnabled {
		return
	}

	messages := buildTelegramAutofeeRunMessages(cfg, reason, runAt, summary, calib, changed)
	if len(messages) == 0 {
		return
	}

	sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer sendCancel()
	for _, msg := range messages {
		if err := sendTelegramMessages(sendCtx, tgCfg.BotToken, tgCfg.ChatID, msg); err != nil {
			if s.logger != nil {
				s.logger.Printf("autofee: telegram autofee summary send failed: %v", err)
			}
			return
		}
	}
}

// sendTelegramAutofeeApplyAlert notifies operators when a channel has failed
// to receive its policy update for autofeeApplyErrorAlertThreshold consecutive
// runs. We send the message only on the threshold crossing; further failures
// stay silent until a successful apply resets the streak.
func (s *AutofeeService) sendTelegramAutofeeApplyAlert(alias, channelPoint string, streak int, lastError string) {
	if s.db == nil {
		return
	}
	tgCfg := readTelegramBackupConfig()
	if strings.TrimSpace(tgCfg.BotToken) == "" || strings.TrimSpace(tgCfg.ChatID) == "" {
		return
	}
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 5*time.Second)
	settings, err := loadTelegramNotificationSettings(loadCtx, s.db)
	loadCancel()
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("autofee: telegram settings unavailable for apply alert: %v", err)
		}
		return
	}
	if !settings.AutofeeSummaryEnabled {
		return
	}
	displayAlias := strings.TrimSpace(alias)
	if displayAlias == "" {
		displayAlias = channelPoint
	}
	msg := fmt.Sprintf(
		"🚨 *Autofee* — falha persistente ao aplicar política\n\n"+
			"*Canal:* %s\n"+
			"*Channel point:* `%s`\n"+
			"*Falhas consecutivas:* %d\n"+
			"*Último erro:* `%s`\n\n"+
			"Verifique a conectividade com o LND e o canal. Após sucesso o alerta volta a armar.",
		displayAlias, channelPoint, streak, lastError,
	)
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer sendCancel()
	if err := sendTelegramMessages(sendCtx, tgCfg.BotToken, tgCfg.ChatID, msg); err != nil && s.logger != nil {
		s.logger.Printf("autofee: telegram apply alert send failed: %v", err)
	}
}

func (s *AutofeeService) sendTelegramAutofeeRefreshSummary(cfg AutofeeConfig, runAt time.Time, result AutofeeRefreshResult) {
	if s.db == nil || result.Total == 0 {
		return
	}
	tgCfg := readTelegramBackupConfig()
	if strings.TrimSpace(tgCfg.BotToken) == "" || strings.TrimSpace(tgCfg.ChatID) == "" {
		return
	}

	loadCtx, loadCancel := context.WithTimeout(context.Background(), 5*time.Second)
	settings, err := loadTelegramNotificationSettings(loadCtx, s.db)
	loadCancel()
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("autofee refresh: telegram settings unavailable: %v", err)
		}
		return
	}
	if !settings.AutofeeSummaryEnabled {
		return
	}

	messages := buildTelegramAutofeeRefreshMessages(cfg, runAt, result)
	if len(messages) == 0 {
		return
	}

	sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer sendCancel()
	for _, msg := range messages {
		if err := sendTelegramMessages(sendCtx, tgCfg.BotToken, tgCfg.ChatID, msg); err != nil {
			if s.logger != nil {
				s.logger.Printf("autofee refresh: telegram summary send failed: %v", err)
			}
			return
		}
	}
}

func buildTelegramAutofeeRunMessages(cfg AutofeeConfig, reason string, runAt time.Time, summary autofeeRunSummary, calib autofeeCalibration, changed []*decision) []string {
	if len(changed) == 0 {
		return nil
	}
	ordered := append([]*decision(nil), changed...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left := absInt(ordered[i].NewPpm - ordered[i].LocalPpm)
		right := absInt(ordered[j].NewPpm - ordered[j].LocalPpm)
		if left != right {
			return left > right
		}
		return strings.ToLower(strings.TrimSpace(ordered[i].Alias)) < strings.ToLower(strings.TrimSpace(ordered[j].Alias))
	})

	sameFeeChanges := 0
	for _, d := range ordered {
		if d.NewPpm == d.LocalPpm {
			sameFeeChanges++
		}
	}

	profile := strings.TrimSpace(cfg.Profile)
	if profile == "" {
		profile = "moderate"
	}
	operationMode := normalizeAutofeeOperationMode(cfg.OperationMode)
	localRunAt := runAt.In(time.Local)
	summaryLines := []string{
		fmt.Sprintf("⚡ Autofee %s [%s] | %s", strings.ToUpper(strings.TrimSpace(reason)), strings.ToUpper(operationMode), localRunAt.Format("2006-01-02 15:04:05")),
		fmt.Sprintf("Profile: %s", strings.ToLower(profile)),
		fmt.Sprintf("Mode: %s", strings.ToLower(strings.ReplaceAll(operationMode, "_", " "))),
		fmt.Sprintf("✅ changed %d | 🔺 up %d | 🔻 down %d | ➡️ same-fee %d", len(ordered), summary.changedUp, summary.changedDown, sameFeeChanges),
		fmt.Sprintf("⏳ cooldown %d | 🧊 hold-small %d | 🟰 same-ppm %d", summary.skippedCooldown, summary.skippedSmall, summary.skippedSame),
		fmt.Sprintf("🧯 floor-relax %d | 🚨 stall-alert %d | 🧵 forward-hot %d", summary.floorRelaxApplied, summary.stallAlert, summary.htlcForwardHot),
		fmt.Sprintf("↘️ inbound-discount %d | 🔥 super-source %d", summary.inboundDiscount, summary.superSource),
		fmt.Sprintf("⚙️ node %s | liquidity %s | local %.1f%%", strings.ToLower(calib.NodeClass), strings.ToLower(calib.LiquidityClass), calib.LocalRatio*100),
	}
	messages := []string{strings.Join(summaryLines, "\n")}

	channelLines := make([]string, 0, len(ordered))
	for _, d := range ordered {
		channelLines = append(channelLines, buildTelegramAutofeeChangedChannelLineFull(d))
	}
	messages = append(messages, chunkTelegramAutofeeSection("✅ Changed channels", channelLines, telegramMessageMaxChars)...)
	return messages
}

func buildTelegramAutofeeRefreshMessages(cfg AutofeeConfig, runAt time.Time, result AutofeeRefreshResult) []string {
	localRunAt := runAt.In(time.Local)
	operationMode := normalizeAutofeeOperationMode(cfg.OperationMode)
	summaryLines := []string{
		fmt.Sprintf("🔄 Autofee REFRESH [%s] | %s", strings.ToUpper(operationMode), localRunAt.Format("2006-01-02 15:04:05")),
		fmt.Sprintf("Updated: %d | Same: %d | Skipped: %d | Errors: %d", result.Updated, result.Same, result.Skipped, result.Errors),
		fmt.Sprintf("Rebal markup: +%.0f%%", result.RebalMarkupPct),
	}
	if result.IncludeInbound {
		summaryLines = append(summaryLines, fmt.Sprintf("Inbound refresh: enabled | changed %d", result.InboundUpdated))
	}
	if result.DryRun {
		summaryLines = append(summaryLines, "Mode: dry-run")
	}
	messages := []string{strings.Join(summaryLines, "\n")}

	changed := make([]AutofeeRefreshItem, 0)
	errorsList := make([]AutofeeRefreshItem, 0)
	for _, item := range result.Items {
		switch autofeeRefreshCategory(item) {
		case "changed":
			changed = append(changed, item)
		case "error":
			errorsList = append(errorsList, item)
		}
	}
	sort.SliceStable(changed, func(i, j int) bool {
		left := absInt(changed[i].TargetPpm - changed[i].CurrentPpm)
		right := absInt(changed[j].TargetPpm - changed[j].CurrentPpm)
		if left != right {
			return left > right
		}
		return strings.ToLower(strings.TrimSpace(changed[i].Alias)) < strings.ToLower(strings.TrimSpace(changed[j].Alias))
	})
	if len(changed) > 0 {
		lines := make([]string, 0, len(changed))
		for _, item := range changed {
			lines = append(lines, buildTelegramAutofeeRefreshChannelLine(item))
		}
		messages = append(messages, chunkTelegramAutofeeSection("✅ Refresh changes", lines, telegramMessageMaxChars)...)
	}
	if len(errorsList) > 0 {
		lines := make([]string, 0, len(errorsList))
		for _, item := range errorsList {
			lines = append(lines, buildTelegramAutofeeRefreshChannelLine(item))
		}
		messages = append(messages, chunkTelegramAutofeeSection("❌ Refresh errors", lines, telegramMessageMaxChars)...)
	}
	return messages
}

func chunkTelegramAutofeeSection(header string, lines []string, maxChars int) []string {
	if len(lines) == 0 {
		return nil
	}
	if maxChars <= 0 {
		maxChars = telegramMessageMaxChars
	}
	chunks := make([]string, 0, 1)
	index := 0
	for index < len(lines) {
		chunkNo := len(chunks) + 1
		title := header
		if chunkNo > 1 {
			title = fmt.Sprintf("%s (cont. %d)", header, chunkNo)
		}
		builder := strings.Builder{}
		builder.WriteString(title)
		for index < len(lines) {
			line := lines[index]
			candidate := builder.String() + "\n" + line
			if telegramTextLen(candidate) > maxChars {
				if builder.String() == title {
					runes := []rune(line)
					available := maxChars - telegramTextLen(title) - 1
					if available < 32 {
						available = maxChars
						builder.Reset()
					}
					if len(runes) > available {
						line = string(runes[:available])
						lines[index] = string(runes[available:])
					} else {
						index++
					}
					if builder.Len() > 0 {
						builder.WriteString("\n")
					}
					builder.WriteString(line)
				}
				break
			}
			builder.WriteString("\n")
			builder.WriteString(line)
			index++
		}
		chunks = append(chunks, builder.String())
	}
	return chunks
}

func buildTelegramAutofeeRefreshChannelLine(item AutofeeRefreshItem) string {
	alias := telegramShortValue(strings.TrimSpace(item.Alias), 48)
	if alias == "" {
		alias = fmt.Sprintf("chan-%d", item.ChannelID)
	}
	category := autofeeRefreshCategory(item)
	prefix := "🫤"
	switch category {
	case "changed":
		if item.TargetPpm > item.CurrentPpm {
			prefix = "✅🔺"
		} else if item.TargetPpm < item.CurrentPpm {
			prefix = "✅🔻"
		} else {
			prefix = "✅➡️"
		}
	case "error":
		prefix = "❌"
	case "skipped":
		prefix = "⏭️"
	}
	if category == "error" {
		return fmt.Sprintf("%s %s: %s", prefix, alias, telegramShortValue(strings.TrimSpace(item.Error), 160))
	}
	action := fmt.Sprintf("keep %d ppm", item.CurrentPpm)
	if category == "changed" {
		action = fmt.Sprintf("set %d→%d ppm", item.CurrentPpm, item.TargetPpm)
	}
	delta := item.TargetPpm - item.CurrentPpm
	deltaPct := 0.0
	if item.CurrentPpm > 0 && item.TargetPpm != item.CurrentPpm {
		deltaPct = math.Abs(float64(delta)) / float64(item.CurrentPpm) * 100.0
	}
	deltaStr := ""
	if item.CurrentPpm > 0 && item.TargetPpm != item.CurrentPpm {
		deltaStr = fmt.Sprintf(" (%+d, %.1f%%)", delta, deltaPct)
	}
	line := fmt.Sprintf("%s %s: %s%s", prefix, alias, action, deltaStr)
	if item.ReferencePpm > 0 {
		line += fmt.Sprintf(" | ref≈%d", item.ReferencePpm)
	}
	if src := strings.TrimSpace(item.Source); src != "" {
		line += fmt.Sprintf(" | %s", src)
	}
	if item.CurrentInboundDiscount != item.TargetInboundDiscount {
		line += fmt.Sprintf(" | ↘️ inb %d→%d", item.CurrentInboundDiscount, item.TargetInboundDiscount)
		if src := strings.TrimSpace(item.InboundSource); src != "" {
			line += fmt.Sprintf(" (%s)", src)
		}
	} else if item.TargetInboundDiscount > 0 {
		line += fmt.Sprintf(" | ↘️ inb %d", item.TargetInboundDiscount)
	}
	if category == "skipped" && strings.TrimSpace(item.Reason) != "" {
		line += fmt.Sprintf(" | %s", item.Reason)
	}
	return line
}

func buildTelegramAutofeeChangedChannelLineFull(d *decision) string {
	if d == nil {
		return ""
	}
	alias := telegramShortValue(strings.TrimSpace(d.Alias), 48)
	if alias == "" {
		alias = fmt.Sprintf("chan-%d", d.ChannelID)
	}
	prefix := "✅➡️"
	if d.NewPpm > d.LocalPpm {
		prefix = "✅🔺"
	} else if d.NewPpm < d.LocalPpm {
		prefix = "✅🔻"
	}
	classLabel := strings.TrimSpace(d.ClassLabel)
	if classLabel == "" {
		classLabel = "unknown"
	}
	targetRaw := d.TargetRaw
	if targetRaw <= 0 {
		targetRaw = d.Target
	}
	targetFinal := d.TargetFinal
	if targetFinal <= 0 {
		targetFinal = d.NewPpm
	}
	targetDisplay := fmt.Sprintf("%d", targetRaw)
	if targetFinal != targetRaw {
		targetDisplay = fmt.Sprintf("%d→%d", targetRaw, targetFinal)
	}
	delta := d.NewPpm - d.LocalPpm
	deltaPct := 0.0
	if d.LocalPpm > 0 && d.NewPpm != d.LocalPpm {
		deltaPct = math.Abs(float64(delta)) / float64(d.LocalPpm) * 100.0
	}
	deltaStr := ""
	if d.LocalPpm > 0 && d.NewPpm != d.LocalPpm {
		deltaStr = fmt.Sprintf(" (%+d, %.1f%%)", delta, deltaPct)
	}
	floorSrc := formatAutofeeFloorSource(d.FloorSrc, d.FloorBaseSrc, d.FloorBasePpm)
	action := fmt.Sprintf("set %d→%d ppm", d.LocalPpm, d.NewPpm)
	tagLine := formatAutofeeTags(d)
	if tagLine == "" {
		tagLine = "-"
	}
	line := fmt.Sprintf("%s %s: %s%s | target %s | out_ratio %.2f | out_ppm7d≈%d | rebal_ppm7d≈%d | seed≈%d | floor≥%d%s | margin≈%d | rev_share≈%.2f | %s",
		prefix,
		alias,
		action,
		deltaStr,
		targetDisplay,
		d.OutRatio,
		d.OutPpm7d,
		d.RebalPpm,
		d.Seed,
		d.Floor,
		floorSrc,
		d.Margin,
		d.RevShare,
		tagLine,
	)
	if d.NewInbound {
		line += fmt.Sprintf(" | NEW inbound %.1fh", d.ChannelAgeHours)
	}
	if d.NewPpm == d.LocalPpm && (d.StalledRounds > 0 || d.TargetGapPpm != 0) {
		line += fmt.Sprintf(" | stall r=%d h=%.1f gap=%+d(%.1f%%)",
			d.StalledRounds,
			d.HoursSinceLastChange,
			d.TargetGapPpm,
			d.TargetGapPct,
		)
	}
	if d.HTLCAttempts > 0 {
		line += fmt.Sprintf(" | htlc%dm a=%d p=%d l=%d f=%d u=%d",
			maxInt(1, d.HTLCWindowMin),
			d.HTLCAttempts,
			d.HTLCPolicyFails,
			d.HTLCLiquidityFails,
			d.HTLCForwardFails,
			d.HTLCUnclassifiedFails,
		)
	}
	if prediction := formatTelegramAutofeePrediction(d.PredictionCode, d.PredictionCooldownHours); prediction != "" {
		line += " | " + prediction
	}
	if d.PrevInboundDiscount != d.InboundDiscount {
		line += fmt.Sprintf(" | ↘️ inb %d→%d", d.PrevInboundDiscount, d.InboundDiscount)
	} else if d.InboundDiscount > 0 {
		line += fmt.Sprintf(" | ↘️ inb %d", d.InboundDiscount)
	}
	_ = classLabel
	return line
}

func formatTelegramAutofeePrediction(code string, cooldownHours int) string {
	switch strings.TrimSpace(code) {
	case "hold_or_up":
		return "🔮 forecast: hold or up."
	case "reduce":
		return "🔮 forecast: reduction pressure."
	case "discovery_fast":
		return "🔮 forecast: fast discovery move."
	case "idle_reduce":
		return "🔮 forecast: idle reduction."
	case "bias_up":
		if cooldownHours > 0 {
			return fmt.Sprintf("🔮 forecast: upward bias after cooldown (%dh).", cooldownHours)
		}
		return "🔮 forecast: upward bias."
	case "bias_down":
		return "🔮 forecast: downward bias."
	case "stable":
		return "🔮 forecast: stable."
	default:
		return ""
	}
}

// ===== evaluation =====

func (e *autofeeEngine) evaluateChannel(ch lndclient.ChannelInfo, st *autofeeChannelState, forwardStats map[uint64]forwardStat,
	forwardStats1d map[uint64]forwardStat, forwardStats7d map[uint64]forwardStat, forwardStats21d map[uint64]forwardStat,
	inboundStats map[uint64]inboundStat, rebalStats rebalStats, rebalStats21d rebalStats, recentRebalanceTouches map[uint64]recentRebalanceSignal,
	htlcSignals map[uint64]htlcFailureSignal, totalOutFeeMsat int64, rebalGlobalPpm int, negMarginGlobal bool) *decision {

	localPpm := 0
	if ch.FeeRatePpm != nil {
		localPpm = int(*ch.FeeRatePpm)
	} else if st != nil && st.LastPpm > 0 {
		localPpm = st.LastPpm
	}

	if localPpm <= 0 {
		localPpm = e.cfg.MinPpm
	}

	rawOutRatio := 0.5
	if ch.CapacitySat > 0 {
		rawOutRatio = float64(ch.LocalBalanceSat) / float64(ch.CapacitySat)
	}
	outRatio, outNormMeta := effectiveChannelOutRatio(rawOutRatio, ch.LocalBalanceSat, ch.CapacitySat, e.calib.AvgCapacitySat, e.calib.LocalRatio)
	upwardPressureOutRatio := dynamicUpwardPressureOutRatio(rawOutRatio, outRatio, outNormMeta)
	goodOutRatio := dynamicGoodOutRatio(e.profile, e.calib.LiquidityClass, e.calib.LocalRatio, outNormMeta)
	ranking, hasRanking := e.ranking[normalizeChannelPointKey(ch.ChannelPoint)]

	fwd := forwardStats[ch.ChannelID]
	fwd1d := forwardStats1d[ch.ChannelID]
	fwd7d := forwardStats7d[ch.ChannelID]
	fwd21d := forwardStats21d[ch.ChannelID]
	inb := inboundStats[ch.ChannelID]
	outPpm7dRaw := ppmMsat(fwd.FeeMsat, fwd.AmtMsat)
	outPpm7d := outPpm7dRaw
	outPpm21d := ppmMsat(fwd21d.FeeMsat, fwd21d.AmtMsat)
	fwdCount21d := int(fwd21d.Count)
	outAmt21dSat := fwd21d.AmtMsat / 1000
	outFrom21dFallback := false
	if outPpm7d <= 0 && outPpm21d > 0 && hasOutFallback21dSignal(fwdCount21d, outAmt21dSat, ch.CapacitySat) {
		outPpm7d = outPpm21d
		outFrom21dFallback = true
	}
	fwdCount := int(fwd.Count)
	fwdCount7d := int(fwd7d.Count)

	outAmtSat := fwd.AmtMsat / 1000
	inAmtSat := inb.AmtMsat / 1000
	outAmt1dSat := fwd1d.AmtMsat / 1000
	outAmt7dSat := fwd7d.AmtMsat / 1000
	rebal := rebalStats.ByChannel[ch.ChannelID]
	rebal21d := rebalStats21d.ByChannel[ch.ChannelID]
	rebalAmtSat7d := rebal.AmtMsat / 1000
	perCost := 0
	perCost21d := 0
	rebalFrom21dFallback := false
	rebalFloorSignal := false
	rebalConfidence := rebalFloorConfidence(rebal.Count)
	rebalLowConfidence := false
	if rebal.AmtMsat > 0 {
		perCost = ppmMsat(rebal.FeeMsat, rebal.AmtMsat)
		rebalLowConfidence = rebal.Count < floorRebalMinSuccessCount
		rebalFloorSignal = hasFloorRebalSignal(rebalAmtSat7d, ch.CapacitySat, rebal.Count)
	}
	if rebal21d.AmtMsat > 0 && hasRebalFallback21dSignal(rebal21d.AmtMsat/1000, ch.CapacitySat) {
		perCost21d = ppmMsat(rebal21d.FeeMsat, rebal21d.AmtMsat)
		rebalFrom21dFallback = true
	}

	totalVal := outAmtSat + inAmtSat
	biasRaw := 0.0
	if totalVal > 0 {
		biasRaw = float64(outAmtSat-inAmtSat) / float64(totalVal)
	}

	if st == nil {
		st = &autofeeChannelState{ChannelID: ch.ChannelID}
	}
	prevInboundDiscount := st.LastInboundDiscount
	if ch.InboundFeeRatePpm != nil {
		prevInboundDiscount = currentInboundDiscountFromRate(*ch.InboundFeeRatePpm)
		st.LastInboundDiscount = prevInboundDiscount
	}
	if st.FirstSeen.IsZero() {
		freshState := st.LastPpm == 0 &&
			st.LastInboundDiscount == 0 &&
			st.LastSeed == 0 &&
			st.LastOutrate == 0 &&
			st.LastRebalCost == 0 &&
			st.BaselineFwd7d == 0 &&
			st.LastTs.IsZero() &&
			st.LastOutrateTs.IsZero() &&
			st.LastRebalCostTs.IsZero()
		switch {
		case !st.LastTs.IsZero():
			st.FirstSeen = st.LastTs
		case !st.LastOutrateTs.IsZero():
			st.FirstSeen = st.LastOutrateTs
		case !st.LastRebalCostTs.IsZero():
			st.FirstSeen = st.LastRebalCostTs
		case freshState:
			st.FirstSeen = e.now
		default:
			// Backdate legacy state so existing channels are not bootstrap-classified as new.
			st.FirstSeen = e.now.Add(-time.Duration(defaultBootstrapHours+1) * time.Hour)
		}
	}
	channelAgeHours := 0.0
	if !st.FirstSeen.IsZero() {
		channelAgeHours = e.now.Sub(st.FirstSeen).Hours()
		if channelAgeHours < 0 {
			channelAgeHours = 0
		}
	}
	bootstrapHours := e.profile.BootstrapHours
	if bootstrapHours <= 0 {
		bootstrapHours = defaultBootstrapHours
	}
	bootstrapOutRatioMax := e.profile.BootstrapOutRatioMax
	if bootstrapOutRatioMax <= 0 {
		bootstrapOutRatioMax = defaultBootstrapOutRatioMax
	}
	bootstrapMinStepUp := e.profile.BootstrapMinStepUpPpm
	if bootstrapMinStepUp <= 0 {
		bootstrapMinStepUp = defaultBootstrapMinStepUpPpm
	}
	newInboundBootstrap := !ch.Initiator && channelAgeHours <= float64(bootstrapHours) && outRatio <= bootstrapOutRatioMax

	biasEma := biasRaw
	if st.BiasEma != 0 {
		biasEma = (1.0-classificationBiasEMAAlpha)*st.BiasEma + classificationBiasEMAAlpha*biasRaw
	}
	st.BiasEma = biasEma

	classLabel, classConf := classifyChannel(biasEma, outRatio, inb.Count, fwd.Count, st.ClassLabel, st.ClassConf)
	st.ClassLabel = classLabel
	st.ClassConf = classConf

	stagnationClassEligible := strings.EqualFold(classLabel, "sink")
	recentForwards1d := int(fwd1d.Count)
	recoveryFlow := hasStagnationRecoveryFlow(recentForwards1d, outAmt1dSat, ch.CapacitySat)
	weakRecentFlow := !recoveryFlow
	if stagnationClassEligible && outRatio >= stagnationOutRatioMin {
		if weakRecentFlow {
			st.ExplorerState.StagnationNoFwdRounds++
		} else if st.ExplorerState.StagnationNoFwdRounds > 0 {
			// Hysteresis: require sustained flow to clear stagnation state.
			st.ExplorerState.StagnationNoFwdRounds--
			if st.ExplorerState.StagnationNoFwdRounds <= 0 {
				st.ExplorerState.StagnationNoFwdRounds = 0
				st.ExplorerState.StagnationPhase = 0
			}
		}
	} else {
		st.ExplorerState.StagnationNoFwdRounds = 0
		st.ExplorerState.StagnationPhase = 0
	}
	highOutStagnationPressure := stagnationClassEligible && outRatio >= stagnationHighOutRatio && weakRecentFlow

	rebalSeedFloorPpm := 0
	if perCost > 0 {
		rebalSeedFloorPpm = int(math.Ceil(float64(perCost) * 1.10))
		if strings.EqualFold(classLabel, "sink") && e.profile.SinkExtraFloorMargin > 0 && !highOutStagnationPressure {
			rebalSeedFloorPpm = maxInt(rebalSeedFloorPpm, int(math.Ceil(float64(perCost)*(1.10+e.profile.SinkExtraFloorMargin))))
		}
	}

	superSourceActive := false
	superSourceLike := false
	if e.cfg.SuperSourceEnabled {
		superSourceLike = classLabel == "router"
		ssRatio1d := 0.0
		ssRatio7d := 0.0
		if ch.CapacitySat > 0 {
			ssRatio1d = float64(outAmt1dSat) / float64(ch.CapacitySat)
			ssRatio7d = float64(outAmt7dSat) / float64(ch.CapacitySat)
		}
		ssVol1d := ssRatio1d >= e.superSource.OutAmt1dMult
		ssVol7d := ssRatio7d >= e.superSource.OutAmt7dMult
		ssOk := (classLabel == "source" || classLabel == "router") &&
			outRatio >= e.superSource.OutRatioMin &&
			ch.CapacitySat > 0 &&
			(ssVol1d || ssVol7d) &&
			fwdCount7d >= e.superSource.MinFwds7d

		okSince := st.SuperSourceOkSince
		badSince := st.SuperSourceBadSince
		active := st.SuperSourceActive
		if ssOk {
			badSince = time.Time{}
			if okSince.IsZero() {
				okSince = e.now
			}
			if e.now.Sub(okSince) >= time.Duration(e.superSource.EnterHours)*time.Hour {
				active = true
			}
		} else {
			okSince = time.Time{}
			if badSince.IsZero() {
				badSince = e.now
			}
			if active && e.now.Sub(badSince) >= time.Duration(e.superSource.ExitHours)*time.Hour {
				active = false
			}
		}
		superSourceActive = active
		st.SuperSourceActive = active
		st.SuperSourceOkSince = okSince
		st.SuperSourceBadSince = badSince
	}

	seed, _, peerMarketSkew, seedTags := e.seedForChannel(ch.RemotePubkey, st)
	if seed <= 0 {
		seed = 200
	}
	if e.cfg.OperationMode == autofeeOperationModeBalanced {
		var seedCapTags []string
		seed, seedCapTags = applySeedSignalCaps(e.profile, seed, outPpm7d, perCost, rebalSeedFloorPpm)
		if len(seedCapTags) > 0 {
			seedTags = append(seedTags, seedCapTags...)
		}
	}

	lowOutThresh := e.calib.LowOutThresh
	lowOutProtectThresh := e.calib.LowOutProtectThresh
	if lowOutThresh <= 0 || lowOutProtectThresh <= 0 {
		lowOutThresh, lowOutProtectThresh, _ = effectiveLowOutThresholds(
			e.profile.LowOutThresh,
			e.profile.LowOutProtectThresh,
			e.calib.LiquidityClass,
			e.calib.LocalRatio,
		)
	}
	sinkMinMargin := e.profile.SinkMinMargin
	if sinkMinMargin <= 0 {
		sinkMinMargin = defaultSinkMinMargin
	}
	profitProtectOutRatio := e.profile.ProfitProtectOutRatio
	if profitProtectOutRatio <= 0 {
		profitProtectOutRatio = defaultProfitProtectOutRatio
	}
	profitProtectMarginPpm := e.profile.ProfitProtectMarginPpm
	profitProtectRelaxHours := e.profile.ProfitProtectRelaxHours
	if profitProtectRelaxHours <= 0 {
		profitProtectRelaxHours = defaultProfitProtectRelaxHours
	}
	profitProtectRelaxMaxFwds := e.profile.ProfitProtectRelaxMaxFwds
	if profitProtectRelaxMaxFwds < 0 {
		profitProtectRelaxMaxFwds = defaultProfitProtectRelaxMaxFwds
	}
	profitProtectRelaxMarginPpm := e.profile.ProfitProtectRelaxMarginPpm
	profitProtectRelaxStepFrac := e.profile.ProfitProtectRelaxStepFrac
	if profitProtectRelaxStepFrac <= 0 {
		profitProtectRelaxStepFrac = defaultProfitProtectRelaxStepFrac
	}
	profitProtectRelaxMinStepPpm := e.profile.ProfitProtectRelaxMinStepPpm
	if profitProtectRelaxMinStepPpm <= 0 {
		profitProtectRelaxMinStepPpm = defaultProfitProtectRelaxMinStepPpm
	}
	globalNegLockSoften := e.profile.GlobalNegLockSoften
	softenMinOutRatio := e.profile.SoftenMinOutRatio
	if softenMinOutRatio <= 0 {
		softenMinOutRatio = defaultSoftenMinOutRatio
	}
	softenRequirePosChanMargin := e.profile.SoftenRequirePosChanMargin
	softenMaxDropToPegFrac := e.profile.SoftenMaxDropToPegFrac
	if softenMaxDropToPegFrac <= 0 {
		softenMaxDropToPegFrac = defaultSoftenMaxDropToPegFrac
	}
	if e.profile.SoftenMinOutRatio <= 0 && e.profile.SoftenMaxDropToPegFrac <= 0 && !e.profile.SoftenRequirePosChanMargin {
		globalNegLockSoften = defaultGlobalNegLockSoften
		softenRequirePosChanMargin = defaultSoftenRequirePosChanMargin
	}
	cooldownUpSec := e.cfg.CooldownUpSec
	cooldownDownSec := e.cfg.CooldownDownSec
	if newInboundBootstrap {
		if e.profile.BootstrapCooldownUpSec > 0 {
			cooldownUpSec = e.profile.BootstrapCooldownUpSec
		} else if cooldownUpSec <= 0 {
			cooldownUpSec = defaultBootstrapCooldownUpSec
		}
		if e.profile.BootstrapCooldownDownSec > 0 {
			cooldownDownSec = e.profile.BootstrapCooldownDownSec
		} else if cooldownDownSec <= 0 {
			cooldownDownSec = defaultBootstrapCooldownDownSec
		}
	}

	noFlow1d := recentForwards1d == 0 && outAmt1dSat <= 0
	lowOutSlowUp := false

	tags := []string{}
	if outNormMeta.OutlierLarge || outNormMeta.OutlierSmall || math.Abs(outNormMeta.Effective-outNormMeta.Raw) >= channelOutNormTagDiffMin {
		tags = append(tags,
			"outnorm",
			fmt.Sprintf("outnorm:raw=%.2f", outNormMeta.Raw),
			fmt.Sprintf("outnorm:eff=%.2f", outNormMeta.Effective),
		)
		if outNormMeta.CapRel > 0 {
			tags = append(tags, fmt.Sprintf("outnorm:capx=%.2f", outNormMeta.CapRel))
		}
		if outNormMeta.OutlierLarge {
			tags = append(tags, "outnorm:large")
		} else if outNormMeta.OutlierSmall {
			tags = append(tags, "outnorm:small")
		}
		if outNormMeta.AbsFloorUsed {
			tags = append(tags, "outnorm:abs-floor")
		}
		if math.Abs(outNormMeta.NodeAdj) >= 0.005 {
			tags = append(tags, fmt.Sprintf("outnorm:nodeadj=%+.2f", outNormMeta.NodeAdj))
		}
	}
	assistChannelActive := e.cfg.OperationMode == autofeeOperationModeBalanced && shouldUseAssistChannel(e.profile, ranking, hasRanking, classLabel, localPpm)
	assistPreserveActive := false
	if assistChannelActive {
		tags = append(tags, "assist-channel")
	}
	if newInboundBootstrap {
		tags = append(tags, "new-inbound", "bootstrap")
	}
	if outFrom21dFallback {
		tags = append(tags, "out-fallback-21d")
	}
	if rebalLowConfidence {
		tags = append(tags, "rebal-low-confidence")
	}
	if highOutStagnationPressure {
		tags = append(tags, "stagnation-pressure")
	}
	marketRefillMode := e.cfg.OperationMode == autofeeOperationModeMarketRefill
	slowCycle30dActive := false
	slowCycle30dOutApplied := false
	slowCycle30dRebalApplied := false
	if !marketRefillMode {
		var slowCycleTags []string
		outPpm7d, perCost, slowCycle30dActive, slowCycleTags = applySlowCycle30dReferences(e.profile, ranking, hasRanking, outNormMeta.Effective, outPpm7d, perCost)
		if len(slowCycleTags) > 0 {
			tags = append(tags, slowCycleTags...)
			slowCycle30dOutApplied = containsTag(slowCycleTags, "slow-cycle-30d-out")
			slowCycle30dRebalApplied = containsTag(slowCycleTags, "slow-cycle-30d-rebal")
		}
	}
	recentRebalanceCount := 0
	recentRebalanceWeakCount := 0
	surgeConfirmRebalanceCount := 0
	surgeConfirmRebalanceAmtSat := int64(0)
	recentRebalanceCostPpm := 0
	recentRebalanceCostAt := time.Time{}
	rebalanceTouch := recentRebalanceSignal{}
	if recentRebalanceTouches != nil {
		rebalanceTouch = recentRebalanceTouches[ch.ChannelID]
		recentRebalanceCount = rebalanceTouch.Count
		recentRebalanceWeakCount = rebalanceTouch.WeakCount
		surgeConfirmRebalanceCount, surgeConfirmRebalanceAmtSat = rebalanceTouch.surgeConfirmInputs()
		if hasRecentRebalanceCostFloor(rebalanceTouch, ch.CapacitySat) {
			recentRebalanceCostPpm = rebalanceTouch.costPpm()
			recentRebalanceCostAt = rebalanceTouch.LastAt
		}
	}
	if marketRefillMode {
		rebalanceTouch = recentRebalanceSignal{}
		recentRebalanceCount = 0
		recentRebalanceWeakCount = 0
		surgeConfirmRebalanceCount = 0
		surgeConfirmRebalanceAmtSat = 0
		recentRebalanceCostPpm = 0
		recentRebalanceCostAt = time.Time{}
	}
	holdUpOnRecentRebalance := false
	if !marketRefillMode {
		holdUpOnRecentRebalance = shouldHoldUpOnRecentRebalance(classLabel, outRatio, lowOutProtectThresh, recentRebalanceCount)
		if recentRebalanceCount > 0 {
			tags = append(tags, "rebal-recent")
		} else if recentRebalanceWeakCount > 0 {
			tags = append(tags, "rebal-attempt")
		}
	}
	rebalSetting, rebalSettingOk := e.rebalanceConfig[ch.ChannelID]
	rebalRuntime, rebalRuntimeOk := e.rebalanceRuntime[ch.ChannelID]
	htlcSampleLow := false
	htlcNoisySample := false
	htlcPolicyHot := false
	htlcLiquidityHot := false
	htlcForwardHot := false
	htlcAttempts := 0
	htlcForwardFails := 0
	htlcPolicyFails := 0
	htlcLiquidityFails := 0
	htlcUnclassifiedFails := 0
	htlcWindowMin := 0
	if superSourceActive {
		tags = append(tags, "super-source")
		if superSourceLike {
			tags = append(tags, "super-source-like")
		}
	}
	if signal, ok := htlcSignals[ch.ChannelID]; ok && signal.Attempts60m > 0 {
		htlcSampleLow = signal.SampleLow
		htlcNoisySample = signal.NoisySample
		htlcPolicyHot = signal.PolicyHot
		htlcLiquidityHot = signal.LiquidityHot
		htlcAttempts = signal.Attempts60m
		htlcForwardFails = signal.ForwardFails60m
		htlcPolicyFails = signal.PolicyFails60m
		htlcLiquidityFails = signal.LiquidityFails60m
		htlcUnclassifiedFails = signal.UnclassifiedFails60m
		htlcWindowMin = signal.WindowMin
		if signal.SampleLow {
			tags = append(tags, "htlc-sample-low")
		}
		if signal.NoisySample {
			tags = append(tags, "htlc-noisy-sample")
		}
		if signal.PolicyHot {
			tags = append(tags, "htlc-policy-hot")
		}
		if signal.LiquidityHot {
			tags = append(tags, "htlc-liquidity-hot")
		}
		if signal.ForwardHot {
			htlcForwardHot = true
			tags = append(tags, "htlc-forward-hot")
		}
		if signal.PolicyHot && signal.LiquidityHot {
			tags = append(tags, "htlc-neutral-lock")
		}
	}
	htlcPressureSignal := !htlcSampleLow && (htlcPolicyHot || htlcLiquidityHot)
	observedOutSignal := fwdCount > 0 || outAmtSat > 0 || outPpm7dRaw > 0
	observedRebalSignal := !marketRefillMode && (rebal.AmtMsat > 0 || perCost > 0)
	outrateMemoryActive := hasRecentAutofeeOutrateMemory(st, e.now)
	rebalMemoryActive := !marketRefillMode && hasRecentAutofeeRebalanceMemory(st, e.now)
	seedLocalSignal := observedOutSignal ||
		observedRebalSignal ||
		outFrom21dFallback ||
		rebalFrom21dFallback ||
		slowCycle30dOutApplied ||
		slowCycle30dRebalApplied ||
		outrateMemoryActive ||
		rebalMemoryActive ||
		recentRebalanceCount > 0 ||
		recentRebalanceWeakCount > 0 ||
		htlcPressureSignal ||
		htlcForwardHot
	seedMatureNoLocalSignal := !marketRefillMode &&
		!newInboundBootstrap &&
		channelAgeHours > float64(bootstrapHours)
	seed, seedTags = applySeedShockGuard(st, e.profile, seed, seedTags, seedMatureNoLocalSignal, seedLocalSignal)
	st.LastSeed = int(seed)
	target := int(seed)
	freshPressureSignal := hasFreshAutofeePressureSignal(
		recentForwards1d,
		outAmt1dSat,
		recentRebalanceCount,
		recentRebalanceWeakCount,
		htlcPressureSignal,
		htlcForwardHot,
		newInboundBootstrap,
	) || observedOutSignal || observedRebalSignal
	if freshPressureSignal {
		target += 25
	}
	if outRatio < lowOutProtectThresh {
		if freshPressureSignal {
			st.LowStreak++
		} else {
			st.LowStreak = 0
		}
	} else {
		st.LowStreak = 0
	}
	if st.LowStreak >= 1 && freshPressureSignal {
		bumpAcc := math.Min(0.25, float64(st.LowStreak)*0.05)
		if noFlow1d && bumpAcc > lowOutNoFlowBumpCap {
			bumpAcc = lowOutNoFlowBumpCap
			lowOutSlowUp = true
		}
		if target <= localPpm {
			target = int(math.Ceil(float64(localPpm) * (1.0 + bumpAcc)))
		} else {
			target = int(math.Ceil(float64(target) * (1.0 + bumpAcc)))
		}
	}
	if outRatio < lowOutThresh {
		if freshPressureSignal {
			upMult := 1.02
			if noFlow1d {
				upMult = lowOutNoFlowBoostMult
				lowOutSlowUp = true
			}
			target = int(math.Ceil(float64(target) * upMult))
		}
	} else if outRatio > e.profile.HighOutThresh {
		target = int(math.Floor(float64(target) * 0.98))
		if fwdCount == 0 && outRatio > 0.60 {
			target = int(math.Floor(float64(target) * 0.985))
		}
	}
	if lowOutSlowUp {
		tags = append(tags, "low-out-slow-up")
	}
	rebalHistoryRefPpm := perCost
	if rebalHistoryRefPpm <= 0 {
		rebalHistoryRefPpm = perCost21d
	}
	holdMatureEmptySinkUp := !marketRefillMode && shouldHoldMatureEmptySinkUpwardPressure(
		classLabel,
		channelAgeHours,
		bootstrapHours,
		outRatio,
		lowOutProtectThresh,
		outPpm7d,
		rebalHistoryRefPpm,
		recentForwards1d,
		recentRebalanceCount,
		recentRebalanceWeakCount,
		htlcPressureSignal,
		htlcForwardHot,
	)
	surgeApplied := false
	if outRatio < 0.10 && !holdMatureEmptySinkUp {
		lack := (0.10 - outRatio) / 0.10
		bump := math.Min(e.profile.SurgeBumpMax, 0.5*lack)
		if bump > 0 {
			target = int(math.Ceil(float64(target) * (1.0 + bump)))
			tags = append(tags, fmt.Sprintf("surge+%d%%", int(bump*100)))
			surgeApplied = true
		}
	}
	surgeConfirmSignal := hasSurgeConfirmSignal(surgeConfirmRebalanceCount, surgeConfirmRebalanceAmtSat, ch.CapacitySat)
	surgeConfirmAttemptsMin := e.profile.HTLCMinAttempts60m
	if surgeConfirmAttemptsMin <= 0 {
		surgeConfirmAttemptsMin = 1
	}
	if htlcWindowMin > 0 {
		surgeConfirmAttemptsMin = scaleHTLCThresholdByWindow(surgeConfirmAttemptsMin, htlcWindowMin, surgeConfirmAttemptsMin)
	}
	surgeRoundConfirmSignal := htlcForwardHot && htlcAttempts >= surgeConfirmAttemptsMin
	target, surgeGateTag := applySurgeConfirmationGate(st, localPpm, target, surgeApplied, surgeConfirmSignal, surgeRoundConfirmSignal)
	surgeHoldActive := surgeGateTag == "surge-hold" || surgeGateTag == "surge-hold-flow"
	if surgeGateTag != "" {
		tags = append(tags, surgeGateTag)
	}
	surgeHoldMaxRounds := e.profile.SurgeHoldMaxRounds
	if surgeHoldMaxRounds <= 0 {
		surgeHoldMaxRounds = defaultSurgeHoldMaxRounds
	}
	if newInboundBootstrap {
		bootstrapSurgeHoldMaxRounds := e.profile.BootstrapSurgeHoldMaxRounds
		if bootstrapSurgeHoldMaxRounds <= 0 {
			bootstrapSurgeHoldMaxRounds = defaultBootstrapSurgeHoldMaxRounds
		}
		if bootstrapSurgeHoldMaxRounds > 0 && bootstrapSurgeHoldMaxRounds < surgeHoldMaxRounds {
			surgeHoldMaxRounds = bootstrapSurgeHoldMaxRounds
		}
	}
	if surgeGateTag == "surge-hold-flow" && st.ExplorerState.SurgeGateRounds >= surgeHoldMaxRounds && outRatio < lowOutProtectThresh {
		unlockStep := e.profile.SurgeHoldUnlockStepPpm
		if unlockStep <= 0 {
			unlockStep = defaultSurgeHoldUnlockStepPpm
		}
		if newInboundBootstrap && bootstrapMinStepUp > unlockStep {
			unlockStep = bootstrapMinStepUp
		}
		if unlockStep < 5 {
			unlockStep = 5
		}
		target = localPpm + unlockStep
		surgeHoldActive = false
		tags = append(tags, "surge-timeout-release")
	}
	if holdUpOnRecentRebalance && target > localPpm {
		target = localPpm
		tags = append(tags, "rebal-recent-noup")
	}
	if !marketRefillMode && shouldApplyFailedRebalancePressure(e.profile, outRatio, lowOutProtectThresh, recentRebalanceCount, recentRebalanceWeakCount) {
		if pressuredTarget, pressureTags := applyFailedRebalancePressure(e.profile, localPpm, target, recentRebalanceWeakCount, noFlow1d, htlcPressureSignal); pressuredTarget != target || len(pressureTags) > 0 {
			target = pressuredTarget
			tags = append(tags, pressureTags...)
		}
	}
	revShare := 0.0
	if totalOutFeeMsat > 0 {
		revShare = float64(fwd.FeeMsat) / float64(totalOutFeeMsat)
	}
	if revShare >= 0.20 && outRatio < 0.30 {
		target = int(math.Ceil(float64(target) * 1.12))
		tags = append(tags, "top-rev")
	}
	if noFlow1d &&
		outRatio <= lowOutNoFlowUpperRatio &&
		target > localPpm &&
		recentRebalanceCount == 0 &&
		!htlcPressureSignal {
		capUp := int(math.Ceil(float64(localPpm) * (1.0 + lowOutNoFlowUpCapFrac)))
		minCapDelta := 5
		if newInboundBootstrap && bootstrapMinStepUp > minCapDelta {
			minCapDelta = bootstrapMinStepUp
		}
		if capUp < localPpm+minCapDelta {
			capUp = localPpm + minCapDelta
		}
		if target > capUp {
			target = capUp
			tags = append(tags, "low-out-noflow-cap")
		}
	}
	if newInboundBootstrap && target > localPpm && target < localPpm+bootstrapMinStepUp {
		target = localPpm + bootstrapMinStepUp
	}
	if marketRefillMode {
		if refillTarget, refillTags := applyMarketRefillOutboundBias(e.profile, localPpm, target, outPpm7d, outRatio, recentForwards1d, noFlow1d, weakRecentFlow, e.cfg.MinPpm, e.cfg.MaxPpm); refillTarget != target || len(refillTags) > 0 {
			target = refillTarget
			tags = append(tags, refillTags...)
		}
	}
	if holdMatureEmptySinkUp && target > localPpm {
		target = localPpm
		tags = append(tags, "empty-sink-hold")
		if rebalSettingOk && !rebalSetting.AutoEnabled && !rebalSetting.ManualRestartEnabled {
			tags = append(tags, "empty-sink-rebal-off")
		}
	}

	baseCostPpm := 0
	baseCostSrc := ""

	if marketRefillMode {
		if seed > 0 {
			baseCostPpm = int(math.Round(seed))
			baseCostSrc = "seed"
		} else if st.LastSeed > 0 {
			baseCostPpm = st.LastSeed
			baseCostSrc = "seed"
		} else {
			baseCostPpm = e.cfg.MinPpm
			baseCostSrc = "seed"
		}
	} else {
		switch normalizeRebalCostMode(e.cfg.RebalCostMode) {
		case "global":
			baseCostPpm = rebalGlobalPpm
			baseCostSrc = "rebal-global"
		case "channel":
			if perCost > 0 && rebalConfidence >= 1 {
				baseCostPpm = perCost
				baseCostSrc = "rebal"
				st.LastRebalCost = perCost
				st.LastRebalCostTs = e.now
			} else if perCost21d > 0 {
				baseCostPpm = perCost21d
				baseCostSrc = "rebal-21d"
			} else if rebalMemoryActive {
				baseCostPpm = st.LastRebalCost
				baseCostSrc = "rebal-mem"
			} else if outPpm7d > 0 && fwdCount >= 4 {
				baseCostPpm = outPpm7d
				baseCostSrc = "outrate"
			} else if outrateMemoryActive {
				baseCostPpm = st.LastOutrate
				baseCostSrc = "outrate-mem"
			} else if seed > 0 {
				baseCostPpm = int(seed)
				baseCostSrc = "seed"
			}
		default:
			baseCostPpm = rebalGlobalPpm
			baseCostSrc = "rebal-global"
			if perCost > 0 {
				capSat := ch.CapacitySat
				if capSat <= 0 {
					capSat = 20000
				}
				capThresh := int64(math.Round(float64(capSat) * 0.05))
				if capThresh < 20000 {
					capThresh = 20000
				}
				if capThresh > 500000 {
					capThresh = 500000
				}
				rebalAmtSat := rebal.AmtMsat / 1000
				weight := 0.0
				if capThresh > 0 {
					weight = float64(rebalAmtSat) / float64(capThresh)
				}
				if weight < 0 {
					weight = 0
				} else if weight > 1 {
					weight = 1
				}
				weight *= rebalConfidence
				blended := int(math.Round(weight*float64(perCost) + (1.0-weight)*float64(rebalGlobalPpm)))
				baseCostPpm = blended
				baseCostSrc = "rebal-blend"
				st.LastRebalCost = perCost
				st.LastRebalCostTs = e.now
			} else if perCost21d > 0 {
				baseCostPpm = perCost21d
				baseCostSrc = "rebal-21d"
			} else if rebalMemoryActive {
				baseCostPpm = st.LastRebalCost
				baseCostSrc = "rebal-mem"
			}
		}
	}
	if baseCostPpm <= 0 {
		baseCostPpm = e.cfg.MinPpm
		if baseCostSrc == "" || strings.TrimSpace(baseCostSrc) == "rebal-global" {
			baseCostSrc = "min"
		}
	} else if baseCostPpm < e.cfg.MinPpm {
		baseCostPpm = e.cfg.MinPpm
	}
	if baseCostSrc == "" {
		baseCostSrc = "min"
	}
	if recentRebalanceCostPpm > 0 {
		if recentRebalanceCostPpm > baseCostPpm {
			baseCostPpm = recentRebalanceCostPpm
			baseCostSrc = "rebal-recent"
			tags = append(tags, "rebal-recent-floor")
		}
		st.LastRebalCost = recentRebalanceCostPpm
		if recentRebalanceCostAt.IsZero() {
			st.LastRebalCostTs = e.now
		} else {
			st.LastRebalCostTs = recentRebalanceCostAt
		}
	}
	if shouldPreserveAssistChannelUpwardPressure(assistChannelActive, recentRebalanceCount, htlcLiquidityHot) && target > localPpm {
		target = localPpm
		assistPreserveActive = true
		tags = append(tags, "assist-preserve")
	}
	rebalExecutionDownAnchorActive := false
	rebalExecutionDownAnchorPpm := 0
	if !marketRefillMode {
		rebalExecTags, restrictUpward, downAnchorPpm, downAnchorActive := deriveRebalanceExecutionPolicy(
			e.profile,
			rebalRuntime,
			rebalRuntimeOk,
			e.rebalanceROIMin,
			e.rebalanceStatus,
			classLabel,
			channelAgeHours,
			bootstrapHours,
			outRatio,
			lowOutProtectThresh,
			outPpm7d,
			rebalHistoryRefPpm,
			baseCostPpm,
			baseCostSrc,
			recentRebalanceCount,
			recentRebalanceWeakCount,
			htlcPressureSignal,
			htlcForwardHot,
			localPpm,
		)
		if len(rebalExecTags) > 0 {
			tags = append(tags, rebalExecTags...)
		}
		if restrictUpward && target > localPpm {
			target = localPpm
			tags = append(tags, "rebal-exec-noup")
		}
		if downAnchorActive && downAnchorPpm > 0 {
			target = minInt(target, downAnchorPpm)
			rebalExecutionDownAnchorActive = true
			rebalExecutionDownAnchorPpm = downAnchorPpm
			tags = append(tags, "rebal-exec-down-anchor")
		}
	}
	matureEmptySinkDownAnchorActive := false
	matureEmptySinkDownAnchorPpm := 0
	if !marketRefillMode &&
		holdMatureEmptySinkUp &&
		rebalSettingOk &&
		!rebalSetting.AutoEnabled &&
		!rebalSetting.ManualRestartEnabled {
		if anchor, ok := deriveMatureEmptySinkHistoryAnchor(e.profile, localPpm, outPpm7d, rebalHistoryRefPpm, baseCostPpm, baseCostSrc); ok {
			target = minInt(target, anchor)
			matureEmptySinkDownAnchorActive = true
			matureEmptySinkDownAnchorPpm = anchor
			tags = append(tags, "empty-sink-down-anchor")
		}
	}
	if !marketRefillMode && rebalFrom21dFallback && perCost <= 0 {
		tags = append(tags, "rebal-fallback-21d")
	}
	hasOutSignal := observedOutSignal || outFrom21dFallback || slowCycle30dOutApplied || outrateMemoryActive
	hasRebalSignal := !marketRefillMode && (observedRebalSignal || rebalFrom21dFallback || slowCycle30dRebalApplied || rebalMemoryActive)
	strongOutSignal := observedOutSignal || (outrateMemoryActive && e.now.Sub(st.LastOutrateTs) <= 7*24*time.Hour)
	strongRebalSignal := !marketRefillMode && (observedRebalSignal || (rebalMemoryActive && e.now.Sub(st.LastRebalCostTs) <= 7*24*time.Hour))
	noSignalNoUpActive := false
	if shouldBlockAutofeeIdleUpwardPressure(
		marketRefillMode,
		noFlow1d,
		target,
		localPpm,
		recentRebalanceCount,
		recentRebalanceWeakCount,
		htlcPressureSignal,
		htlcForwardHot,
		newInboundBootstrap,
		observedOutSignal,
		observedRebalSignal,
	) {
		target = localPpm
		noSignalNoUpActive = true
		tags = append(tags, "no-signal-noup")
	} else if noFlow1d &&
		!marketRefillMode &&
		outRatio >= lowOutNoFlowUpperRatio &&
		target > localPpm &&
		recentRebalanceCount == 0 &&
		recentRebalanceWeakCount == 0 &&
		!htlcPressureSignal &&
		!htlcForwardHot &&
		!hasOutSignal &&
		!hasRebalSignal {
		target = localPpm
		noSignalNoUpActive = true
		tags = append(tags, "no-signal-noup")
	}
	marginPpm7d := outPpm7d - int(float64(baseCostPpm)*1.10)
	if marginPpm7d < 0 {
		tags = append(tags, "neg-margin")
		minFwds := e.profile.NegMarginSurgeMinFwds
		if e.profile.NegMarginSurgeFwdsRatio > 0 {
			baseFwds := st.BaselineFwd7d
			if baseFwds <= 0 {
				baseFwds = fwdCount
			}
			if baseFwds <= 0 {
				baseFwds = 1
			}
			ratioFwds := int(math.Round(float64(baseFwds) * e.profile.NegMarginSurgeFwdsRatio))
			if ratioFwds > minFwds {
				minFwds = ratioFwds
			}
		}
		if e.profile.NegMarginSurgeBump > 0 && fwdCount >= minFwds {
			target = int(math.Ceil(float64(target) * (1.0 + e.profile.NegMarginSurgeBump)))
			tags = append(tags, fmt.Sprintf("negm+%d%%", int(math.Round(e.profile.NegMarginSurgeBump*100))))
		}
	}

	htlcMode := normalizeHTLCMode(e.cfg.HTLCMode)
	applyHTLCPolicyHot := e.cfg.HTLCSignalEnabled && (htlcMode == htlcModePolicyOnly || htlcMode == htlcModeFull)
	applyHTLCLiquidityHot := e.cfg.HTLCSignalEnabled && htlcMode == htlcModeFull
	htlcHotSignal := !htlcSampleLow && ((applyHTLCPolicyHot && htlcPolicyHot) || (applyHTLCLiquidityHot && htlcLiquidityHot))
	negMarginDownStrongPressure := surgeConfirmSignal || surgeRoundConfirmSignal || surgeHoldActive || htlcHotSignal || htlcForwardHot
	if htlcHotSignal {
		if applyHTLCPolicyHot && htlcPolicyHot && applyHTLCLiquidityHot && htlcLiquidityHot {
			if target < localPpm {
				target = localPpm
				tags = append(tags, "htlc-neutral-nodown")
			}
		} else {
			if applyHTLCLiquidityHot && htlcLiquidityHot {
				liqBump := e.profile.HTLCLiquidityHotBump
				if liqBump > 0 {
					target = int(math.Ceil(float64(target) * (1.0 + liqBump)))
					tags = append(tags, fmt.Sprintf("htlc-liq+%d%%", int(math.Round(liqBump*100))))
				}
				liqNoDownOutRatio := e.profile.HTLCLiquidityHotNoDownOutRatio
				if liqNoDownOutRatio <= 0 {
					liqNoDownOutRatio = 0.10
				}
				if outRatio <= liqNoDownOutRatio && target < localPpm {
					target = localPpm
					tags = append(tags, "htlc-liq-nodown")
				}
			}
			if applyHTLCPolicyHot && htlcPolicyHot {
				policyBump := e.profile.HTLCPolicyHotBump
				if policyBump > 0 {
					target = int(math.Ceil(float64(target) * (1.0 + policyBump)))
					tags = append(tags, fmt.Sprintf("htlc-policy+%d%%", int(math.Round(policyBump*100))))
				}
				if marginPpm7d <= e.profile.HTLCPolicyHotNoDownMarginPpm && target < localPpm {
					target = localPpm
					tags = append(tags, "htlc-policy-nodown")
				}
			}
		}
	}

	stagnationRounds := st.ExplorerState.StagnationNoFwdRounds
	stagnationActive := false
	stagnationPhase := 0
	stagnationTargetCap := 0
	stagnationFloorCap := 0
	if stagnationClassEligible && outRatio >= stagnationOutRatioMin && weakRecentFlow {
		outRef := outPpm7d
		if outRef <= 0 && st.LastOutrate > 0 {
			outRef = st.LastOutrate
		}
		rebalRef := 0
		if !marketRefillMode {
			if perCost > 0 {
				rebalRef = perCost
			} else if perCost21d > 0 {
				rebalRef = perCost21d
			} else if st.LastRebalCost > 0 && e.now.Sub(st.LastRebalCostTs) <= 21*24*time.Hour {
				rebalRef = st.LastRebalCost
			} else if rebalGlobalPpm > 0 {
				rebalRef = rebalGlobalPpm
			}
		}

		if stagnationRounds >= stagnationNoForwardRoundsPhase1 && outRef > 0 {
			trigger := int(math.Ceil(float64(outRef) * stagnationOutrateTriggerMult))
			if localPpm > trigger {
				phase1Target := int(math.Ceil(float64(outRef) * stagnationOutrateHeadroom))
				phase1Target = blendTargetWithSeed(phase1Target, seed, stagnationSeedBlendPhase1)
				phase1Target = clampInt(phase1Target, e.cfg.MinPpm, e.cfg.MaxPpm)
				if phase1Target < target {
					target = phase1Target
				}
				stagnationActive = true
				stagnationPhase = 1
				stagnationTargetCap = phase1Target
				stagnationFloorCap = phase1Target
			}
		}

		if stagnationRounds >= stagnationNoForwardRoundsPhase2 && rebalRef > 0 {
			trigger := int(math.Ceil(float64(rebalRef) * stagnationRebalTriggerMult))
			if localPpm > trigger {
				phase2Target := int(math.Ceil(float64(rebalRef) * stagnationRebalHeadroom))
				phase2Target = blendTargetWithSeed(phase2Target, seed, stagnationSeedBlendPhase2)
				phase2Target = clampInt(phase2Target, e.cfg.MinPpm, e.cfg.MaxPpm)
				if phase2Target < target {
					target = phase2Target
				}
				stagnationActive = true
				stagnationPhase = 2
				stagnationTargetCap = phase2Target
				stagnationFloorCap = phase2Target
			}
		}
	}
	if stagnationActive {
		st.ExplorerState.StagnationPhase = stagnationPhase
		tags = append(tags, "stagnation")
		if stagnationPhase == 2 {
			tags = append(tags, "normalize-rebal")
		} else {
			tags = append(tags, "normalize-out")
		}
		if stagnationRounds > 0 {
			tags = append(tags, fmt.Sprintf("stagnation-r%d", stagnationRounds))
		}
		if stagnationTargetCap > 0 {
			tags = append(tags, fmt.Sprintf("stagnation-cap-%d", stagnationTargetCap))
		}
	} else {
		st.ExplorerState.StagnationPhase = 0
	}

	capRefPpm := 0
	if !marketRefillMode && perCost > 0 {
		capRefPpm = perCost
	} else if !marketRefillMode && rebalMemoryActive {
		capRefPpm = st.LastRebalCost
	} else if outPpm7d > 0 && fwdCount >= 4 {
		capRefPpm = outPpm7d
	} else if outrateMemoryActive {
		capRefPpm = st.LastOutrate
	} else if seed > 0 {
		capRefPpm = int(seed)
	}

	discoveryHit := false
	discoveryHard := false
	explorerActive := false
	drainedExplorerActive := false
	if e.cfg.ExplorerEnabled {
		explorerActive = e.evalExplorer(st, outRatio, fwdCount, ch.LocalBalanceSat, ch.CapacitySat, localPpm)
		if explorerActive {
			tags = append(tags, "explorer")
		}
		drainedExplorerActive = e.evalDrainedExplorer(st, outRatio, recentForwards1d, recentRebalanceCount)
	}
	if e.cfg.DiscoveryEnabled && fwdCount == 0 && outRatio > 0.40 {
		discoveryHit = true
		tags = append(tags, "discovery")
	}

	if discoveryHit {
		daysSinceFirst := 999.0
		if !st.FirstSeen.IsZero() {
			daysSinceFirst = e.now.Sub(st.FirstSeen).Hours() / 24.0
		}
		discoveryGateOk := true
		if e.profile.DiscRequireExplorer {
			discoveryGateOk = false
			if st.ExplorerState.Seen && st.ExplorerState.LastExitTs > 0 {
				lastExit := time.Unix(st.ExplorerState.LastExitTs, 0)
				if e.now.Sub(lastExit).Hours() >= float64(e.profile.DiscAfterExplorerDays*24) {
					discoveryGateOk = true
				}
			}
		}
		if discoveryGateOk && st.BaselineFwd7d == 0 && daysSinceFirst >= float64(e.profile.DiscHarddropDaysNoBase) {
			base := int(math.Round(seed)) + e.profile.DiscHarddropCushion
			if target > base {
				target = base + int(math.Round(0.5*float64(target-base)))
			}
			discoveryHard = true
			tags = append(tags, "discovery-hard")
		}
	}

	if outRatio < lowOutProtectThresh && target < localPpm {
		if matureEmptySinkDownAnchorActive {
			tags = append(tags, "empty-sink-low-relax")
		} else if rebalExecutionDownAnchorActive {
			tags = append(tags, "rebal-exec-low-relax")
		} else {
			target = localPpm
			tags = append(tags, "no-down-low")
		}
	}

	if superSourceActive {
		target = e.cfg.MinPpm
	}
	if adjustedTarget, explorerTags := applyDrainedExplorerTarget(e.profile, drainedExplorerActive, st.ExplorerState.DrainedRounds, localPpm, target, seed, baseCostPpm); len(explorerTags) > 0 {
		target = adjustedTarget
		tags = append(tags, explorerTags...)
	}
	allowOutrateTargetAnchor := outPpm7d > 0 &&
		recentRebalanceCount == 0 &&
		!htlcPressureSignal &&
		!strongOutSignal &&
		!strongRebalSignal &&
		!discoveryHit &&
		!explorerActive &&
		!stagnationActive &&
		!superSourceActive &&
		!highOutStagnationPressure
	if allowOutrateTargetAnchor {
		anchoredTarget, anchorTags := applyOutrateTargetAnchor(e.profile, target, outPpm7d, upwardPressureOutRatio, goodOutRatio, fwdCount)
		if anchoredTarget > target {
			target = anchoredTarget
			tags = append(tags, anchorTags...)
		}
	}
	seedEnvelopeActive := shouldEnableSeedEnvelope(
		seed,
		noFlow1d,
		weakRecentFlow,
		recentForwards1d,
		recentRebalanceCount,
		htlcPressureSignal,
		strongOutSignal,
		strongRebalSignal,
		discoveryHit,
		explorerActive,
		stagnationActive,
		superSourceActive,
	)
	if seedEnvelopeActive {
		allowSeedFloor := shouldAllowBootstrapSeedSoftFloor(newInboundBootstrap, outRatio, lowOutNoFlowUpperRatio)
		var seedEnvelopeTags []string
		target, seedEnvelopeTags = applySeedSoftEnvelope(target, seed, e.profile.SeedFloorMult, e.profile.SeedCeilingMult, allowSeedFloor)
		tags = append(tags, seedEnvelopeTags...)
	}
	seedSoftNegMarginRelax := shouldRelaxNegMarginForSeedSoftEnvelope(seedEnvelopeActive, tags, localPpm, target, seed, e.profile.SeedCeilingMult)

	negMarginProtectedFloor := deriveNegMarginProtectedFloor(0, baseCostPpm, recentRebalanceCostPpm, e.cfg.MinPpm, e.cfg.MaxPpm)
	target = clampInt(target, e.cfg.MinPpm, e.cfg.MaxPpm)
	if marginPpm7d < 0 && target < localPpm {
		if stagnationActive || highOutStagnationPressure {
			tags = append(tags, "stagnation-neg-override")
		} else if matureEmptySinkDownAnchorActive {
			tags = append(tags, "empty-sink-neg-relax")
		} else if rebalExecutionDownAnchorActive {
			tags = append(tags, "rebal-exec-neg-relax")
		} else if seedSoftNegMarginRelax {
			tags = append(tags, "seed:soft-neg-relax")
		} else {
			adjustedTarget, negMarginDownTag := applyNegMarginProtectedDown(target, localPpm, negMarginProtectedFloor, negMarginDownStrongPressure)
			target = adjustedTarget
			if negMarginDownTag != "" {
				tags = appendAutofeeTagOnce(tags, negMarginDownTag)
			}
		}
	}
	topRevenue := revShare >= 0.20 && outRatio < 0.30
	rescueActive, rescueTags := manageRescueState(
		st,
		e.now,
		!marketRefillMode,
		ranking,
		hasRanking,
		localPpm,
		target,
		outPpm7d,
		revShare,
		topRevenue,
		slowCycle30dActive,
	)
	if len(rescueTags) > 0 {
		tags = append(tags, rescueTags...)
	}
	rankingUpwardRestricted := false
	if !marketRefillMode {
		rankingPolicyTags, restrictUpward := deriveRankingPolicy(ranking, hasRanking, recentRebalanceCount, htlcLiquidityHot, surgeConfirmSignal)
		if len(rankingPolicyTags) > 0 {
			tags = append(tags, rankingPolicyTags...)
		}
		if restrictUpward && target > localPpm {
			target = localPpm
			rankingUpwardRestricted = true
			tags = append(tags, "rank-up-restrict")
		}
	}
	capFrac := e.profile.StepCap
	minStep := 5
	if newInboundBootstrap && bootstrapMinStepUp > minStep {
		minStep = bootstrapMinStepUp
	}
	if htlcHotSignal && target > localPpm && e.profile.HTLCHotStepCapBoost > 0 {
		capFrac = math.Max(capFrac, e.profile.StepCap+e.profile.HTLCHotStepCapBoost)
		tags = append(tags, "htlc-step-boost")
	}
	if outRatio < 0.03 {
		capFrac = math.Max(capFrac, 0.10)
	} else if outRatio < 0.05 {
		capFrac = math.Max(capFrac, 0.07)
	}
	if fwdCount == 0 && outRatio > 0.60 {
		capFrac = math.Max(capFrac, 0.12)
	}
	if boost, strong := largeGapStepCapBoost(e.profile, localPpm, target); boost > 0 {
		capFrac = math.Max(capFrac, e.profile.StepCap+boost)
		tags = append(tags, "large-gap-step-boost")
		if strong {
			tags = append(tags, "large-gap-step-boost-strong")
		}
	}
	if discoveryHit {
		capFrac = math.Max(capFrac, e.profile.DiscoveryStepCapDown)
	}
	if discoveryHard {
		capFrac = math.Max(capFrac, e.profile.DiscHarddropCapFrac)
	}
	if explorerActive {
		capFrac = math.Max(capFrac, e.profile.DiscoveryStepCapDown)
	}
	if marketRefillMode && target > localPpm {
		if marketCapFrac, marketCapTags := marketRefillStepCapFrac(e.profile, outRatio); marketCapFrac > capFrac {
			capFrac = marketCapFrac
			tags = append(tags, marketCapTags...)
		}
	}

	globalNegLockApplied := false
	lockSkipTag := ""
	if negMarginGlobal && !stagnationActive && !highOutStagnationPressure {
		hasRecentRebal := !marketRefillMode && (observedRebalSignal || rebalFrom21dFallback || slowCycle30dRebalApplied)
		hasRecentOutrate := (observedOutSignal && fwdCount >= 4) || outFrom21dFallback || slowCycle30dOutApplied
		canLockGlobally := hasRecentRebal || hasRecentOutrate
		if canLockGlobally {
			allowSoften := false
			if globalNegLockSoften {
				chanOk := outRatio >= softenMinOutRatio
				if softenRequirePosChanMargin {
					chanOk = chanOk && marginPpm7d >= 0
				}
				if chanOk && !discoveryHit {
					allowSoften = true
				}
			}
			if strings.EqualFold(classLabel, "sink") && marginPpm7d > sinkMinMargin {
				lockSkipTag = "lock-skip-sink-profit"
				capFrac = math.Max(capFrac, e.profile.StepCap)
			} else if target < localPpm && !discoveryHit {
				if rescueActive {
					lockSkipTag = "rescue-global-relax"
					if outPpm7d > 0 && target < outPpm7d {
						target = outPpm7d
					}
				} else if matureEmptySinkDownAnchorActive {
					lockSkipTag = "empty-sink-global-relax"
				} else if rebalExecutionDownAnchorActive {
					lockSkipTag = "rebal-exec-global-relax"
				} else if allowSoften {
					if outPpm7d > 0 {
						pegFloor := int(math.Round(float64(outPpm7d) * softenMaxDropToPegFrac))
						if target < pegFloor {
							target = pegFloor
						}
					}
				} else if seedSoftNegMarginRelax {
					lockSkipTag = "seed:soft-global-relax"
				} else {
					target = localPpm
					globalNegLockApplied = true
				}
			}
			capFrac = math.Max(capFrac, e.profile.StepCap+0.05)
		} else if target < localPpm && !discoveryHit {
			lockSkipTag = "lock-skip-no-chan-rebal"
		}
	} else if negMarginGlobal && (stagnationActive || highOutStagnationPressure) {
		tags = append(tags, "stagnation-neg-override")
	}
	if e.cfg.ExtremeDrainEnabled &&
		target <= localPpm &&
		surgeGateTag == "surge-hold-flow" &&
		e.profile.ExtremeDrainStreak > 0 &&
		st.LowStreak >= e.profile.ExtremeDrainStreak &&
		outRatio <= e.profile.ExtremeDrainOutMax {
		unlockStep := e.profile.ExtremeDrainMinStepPpm
		if unlockStep <= 0 {
			unlockStep = defaultSurgeHoldUnlockStepPpm
		}
		if newInboundBootstrap && bootstrapMinStepUp > unlockStep {
			unlockStep = bootstrapMinStepUp
		}
		if unlockStep < 5 {
			unlockStep = 5
		}
		target = localPpm + unlockStep
		surgeHoldActive = false
		tags = append(tags, "extreme-drain-unlock")
	}

	seedFullHoldActive := shouldHoldSeedDrivenUpOnFullChannel(
		marketRefillMode,
		rawOutRatio,
		target,
		localPpm,
		outPpm7d,
		rebalHistoryRefPpm,
		baseCostSrc,
	)
	if seedFullHoldActive {
		target = localPpm
		tags = append(tags, "seed-full-hold")
	}

	if holdMatureEmptySinkUp && target > localPpm {
		target = localPpm
		tags = append(tags, "empty-sink-hard-hold")
	}

	if target > localPpm {
		tags = append(tags, "trend-up")
	} else if target < localPpm {
		tags = append(tags, "trend-down")
	} else {
		tags = append(tags, "trend-flat")
	}

	if e.cfg.ExtremeDrainEnabled && target > localPpm && e.profile.ExtremeDrainStreak > 0 {
		if st.LowStreak >= e.profile.ExtremeDrainStreak && outRatio <= e.profile.ExtremeDrainOutMax {
			capFrac = math.Max(capFrac, e.profile.ExtremeDrainStepCap)
			if e.profile.ExtremeDrainMinStepPpm > minStep {
				minStep = e.profile.ExtremeDrainMinStepPpm
			}
			tags = append(tags, "extreme-drain")
			if st.LowStreak >= e.profile.ExtremeDrainTurboStreak && outRatio <= e.profile.ExtremeDrainTurboOutMax {
				capFrac = math.Max(capFrac, e.profile.ExtremeDrainTurboStepCap)
				if e.profile.ExtremeDrainTurboMinStepPpm > minStep {
					minStep = e.profile.ExtremeDrainTurboMinStepPpm
				}
				tags = append(tags, "extreme-drain-turbo")
			}
		}
	}

	minStepUp := minStep
	minStepDown := 5
	stepMin := minStepDown
	if target > localPpm {
		stepMin = minStepUp
	}

	rawStep := applyStepCap(localPpm, target, capFrac, stepMin, capRefPpm)
	if e.cfg.CircuitBreakerEnabled && st.LastDir == "up" && !st.LastTs.IsZero() {
		daysSince := e.now.Sub(st.LastTs).Hours() / 24.0
		if daysSince <= float64(e.profile.CircuitBreakerGraceDays) && st.BaselineFwd7d > 0 {
			if fwdCount < int(float64(st.BaselineFwd7d)*e.profile.CircuitBreakerDropRatio) {
				rawStep = int(math.Round(float64(rawStep) * (1.0 - e.profile.CircuitBreakerReduceStep)))
				rawStep = clampInt(rawStep, e.cfg.MinPpm, e.cfg.MaxPpm)
				tags = append(tags, "circuit-breaker")
			}
		}
	}

	floorBasePpm := baseCostPpm
	floorBaseSrc := baseCostSrc
	if !marketRefillMode && perCost > 0 && !rebalFloorSignal {
		fallbackFloorBase := floorBasePpm
		fallbackFloorSrc := floorBaseSrc
		if rebalGlobalPpm > 0 {
			fallbackFloorBase = rebalGlobalPpm
			fallbackFloorSrc = "rebal-global"
		} else if perCost21d > 0 {
			fallbackFloorBase = perCost21d
			fallbackFloorSrc = "rebal-21d"
		} else if outPpm7d > 0 {
			fallbackFloorBase = outPpm7d
			fallbackFloorSrc = "outrate"
		}
		if fallbackFloorBase < e.cfg.MinPpm {
			fallbackFloorBase = e.cfg.MinPpm
		}
		if fallbackFloorBase > 0 && fallbackFloorBase < floorBasePpm {
			floorBasePpm = fallbackFloorBase
			floorBaseSrc = fallbackFloorSrc
			if rebalLowConfidence {
				tags = append(tags, "rebal-floor-low-confidence")
			} else {
				tags = append(tags, "rebal-floor-low-volume")
			}
		}
	}
	floor := int(math.Ceil(float64(floorBasePpm) * 1.10))
	floorSrc := floorSourceFromBaseCost(floorBaseSrc, marketRefillMode)
	goodLiquidityHold := !marketRefillMode &&
		strings.EqualFold(classLabel, "sink") &&
		upwardPressureOutRatio >= goodOutRatio
	if strings.EqualFold(classLabel, "sink") && baseCostPpm > 0 && e.profile.SinkExtraFloorMargin > 0 && !highOutStagnationPressure && !holdMatureEmptySinkUp && !goodLiquidityHold {
		sinkFloor := int(math.Ceil(float64(baseCostPpm) * (1.10 + e.profile.SinkExtraFloorMargin)))
		if sinkFloor > floor {
			floor = sinkFloor
			if isOutrateBaseCostSource(baseCostSrc) {
				floorSrc = "outrate-sink"
			} else if strings.TrimSpace(baseCostSrc) == "seed" {
				floorSrc = "seed-sink"
			} else {
				floorSrc = "rebal-sink"
			}
			tags = append(tags, "sink-floor")
		}
	} else if goodLiquidityHold && strings.EqualFold(classLabel, "sink") && baseCostPpm > 0 && e.profile.SinkExtraFloorMargin > 0 && !highOutStagnationPressure && !holdMatureEmptySinkUp {
		tags = append(tags, "sink-floor-paused-goodliq")
	}
	if holdMatureEmptySinkUp && (floorSrc == "rebal" || floorSrc == "rebal-sink") && floor > localPpm {
		floor = localPpm
		floorSrc = "rebal-hold"
		tags = append(tags, "empty-sink-floor-hold")
	}
	if outPpm7d > 0 && !discoveryHit && !explorerActive {
		outrateFloorActive := true
		factor := outrateFloorFactor
		if highOutStagnationPressure {
			outrateFloorActive = false
			tags = append(tags, "stagnation-floor-relax")
		}
		if fwdCount < outrateFloorDisableBelowFwds {
			outrateFloorActive = false
		} else if fwdCount < outrateFloorLowFwds {
			factor = e.profile.OutrateFloorFactorLow
		}
		if outrateFloorActive && fwdCount >= outrateFloorMinFwds {
			outrateFloor := int(math.Ceil(float64(outPpm7d) * factor))
			if outrateFloor > floor {
				floor = outrateFloor
				floorSrc = "outrate"
				tags = append(tags, "outrate-floor")
			}
		}
	}
	if outPpm7d > 0 && fwdCount >= 4 && !highOutStagnationPressure {
		pegHeadroomPaused := shouldPauseOutratePegHeadroom(outPpm7d, rebalHistoryRefPpm, marginPpm7d)
		peg := outPpm7d
		if !pegHeadroomPaused {
			peg = int(math.Ceil(float64(outPpm7d) * outratePegHeadroom))
		}
		withinGrace := true
		if !st.LastTs.IsZero() && e.profile.OutratePegGraceHours > 0 {
			hoursSince := e.now.Sub(st.LastTs).Hours()
			withinGrace = hoursSince < float64(e.profile.OutratePegGraceHours)
		}
		demandPeg := seed > 0 && float64(outPpm7d) >= seed*outratePegSeedMult
		pegEligible := withinGrace || demandPeg
		if rescueActive && target < localPpm && peg > floor {
			tags = append(tags, "rescue-peg-paused")
		} else if matureEmptySinkDownAnchorActive && target < localPpm && peg > floor {
			tags = append(tags, "empty-sink-peg-paused")
		} else if rebalExecutionDownAnchorActive && target < localPpm && peg > floor {
			tags = append(tags, "rebal-exec-peg-paused")
		} else if goodLiquidityHold && peg > floor {
			tags = append(tags, "peg-paused-goodliq")
		} else if peg > floor && pegEligible {
			floor = peg
			if pegHeadroomPaused {
				floorSrc = "outrate"
				tags = append(tags, "peg-headroom-paused")
			} else {
				floorSrc = "peg"
				tags = append(tags, "peg")
				if withinGrace {
					tags = append(tags, "peg-grace")
				}
				if demandPeg {
					tags = append(tags, "peg-demand")
				}
			}
		} else if pegHeadroomPaused && pegEligible {
			tags = append(tags, "peg-headroom-paused")
		}
	} else if outPpm7d > 0 && fwdCount >= 4 && highOutStagnationPressure {
		tags = append(tags, "peg-paused-stagnation")
	}

	if explorerActive && !marketRefillMode {
		rebalFloorPpm := 0
		if perCost > 0 {
			rebalFloorPpm = int(math.Ceil(float64(perCost) * 1.10))
		} else if st.LastRebalCost > 0 && e.now.Sub(st.LastRebalCostTs) <= 21*24*time.Hour {
			rebalFloorPpm = int(math.Ceil(float64(st.LastRebalCost) * 1.10))
		}
		if rebalFloorPpm > 0 && rebalFloorPpm > floor {
			floor = rebalFloorPpm
			if floorSrc != "peg" && floorSrc != "outrate" {
				floorSrc = "rebal"
			}
		}
	}

	revfloorBaseline := e.profile.RevfloorBaselineThresh
	if e.calib.RevfloorBaseline > 0 {
		revfloorBaseline = e.calib.RevfloorBaseline
	}
	revfloorMinAbs := e.profile.RevfloorMinAbs
	if e.calib.RevfloorMinAbs > 0 {
		revfloorMinAbs = e.calib.RevfloorMinAbs
	}
	if e.cfg.RevfloorEnabled && !superSourceActive && revfloorBaseline > 0 && st.BaselineFwd7d >= revfloorBaseline {
		revFloor := int(math.Round(math.Max(float64(seed)*0.40, float64(revfloorMinAbs))))
		revFloor = clampInt(revFloor, e.cfg.MinPpm, e.cfg.MaxPpm)
		if revFloor > floor {
			floor = revFloor
			floorSrc = "revfloor"
			tags = append(tags, "revfloor")
		}
	}

	if !marketRefillMode &&
		floor > localPpm &&
		outPpm7d <= 0 &&
		rebalHistoryRefPpm <= 0 &&
		upwardPressureOutRatio >= goodOutRatio {
		switch floorSrc {
		case "seed", "seed-sink", "seed-soft":
			floor = localPpm
			floorSrc = "seed-goodliq-hold"
			tags = append(tags, "seed-goodliq-hold")
		}
	}

	if stagnationActive && !superSourceActive && stagnationFloorCap > 0 && floor > stagnationFloorCap {
		floor = stagnationFloorCap
		floorSrc = "stagnation"
		tags = append(tags, "stagnation-floor")
	}

	if superSourceActive {
		floor = e.cfg.MinPpm
		floorSrc = "super-source"
	}
	if noSignalNoUpActive && floor > localPpm {
		floor = localPpm
		floorSrc = "no-signal"
		tags = append(tags, "no-signal-floor-relax")
	}
	if seedFullHoldActive && floor > localPpm {
		switch floorSrc {
		case "seed", "seed-sink", "seed-soft":
			floor = localPpm
			floorSrc = "seed-hold"
			tags = append(tags, "seed-full-floor-hold")
		}
	}
	if holdMatureEmptySinkUp && floor > localPpm && shouldRelaxFloorForMatureEmptySinkAnchor(floorSrc) {
		floor = localPpm
		floorSrc = "empty-sink-hold"
		tags = append(tags, "empty-sink-floor-hard-hold")
	}
	if assistPreserveActive && floor > localPpm {
		floor = localPpm
		floorSrc = "assist"
	}
	if relaxedFloor, relaxedSrc, rescueFloorTags := applyRescueFloorRelax(rescueActive, localPpm, target, floor, floorSrc, outPpm7d, baseCostPpm); len(rescueFloorTags) > 0 {
		floor = relaxedFloor
		floorSrc = relaxedSrc
		tags = append(tags, rescueFloorTags...)
	}
	if rankingUpwardRestricted && floor > localPpm && shouldRelaxFloorForRankingGuard(floorSrc) {
		floor = localPpm
		floorSrc = "rank"
		tags = append(tags, "rank-floor-relax")
	}
	seedSoftCeilActive := seed > 0 &&
		noFlow1d &&
		outRatio >= lowOutNoFlowUpperRatio &&
		recentRebalanceCount == 0 &&
		!htlcPressureSignal &&
		!strongOutSignal &&
		!strongRebalSignal &&
		!discoveryHit &&
		!explorerActive &&
		!stagnationActive &&
		!superSourceActive
	if seedSoftCeilActive && (floorSrc == "rebal" || floorSrc == "rebal-sink" || floorSrc == "no-signal") {
		softFloor, softTags := applySeedSoftEnvelope(floor, seed, e.profile.SeedFloorMult, e.profile.SeedCeilingMult, false)
		if softFloor < floor {
			floor = softFloor
			floorSrc = "seed-soft"
			tags = append(tags, softTags...)
		}
	}
	if matureEmptySinkDownAnchorActive && matureEmptySinkDownAnchorPpm > 0 && floor > matureEmptySinkDownAnchorPpm && shouldRelaxFloorForMatureEmptySinkAnchor(floorSrc) {
		floor = matureEmptySinkDownAnchorPpm
		floorSrc = "empty-sink-anchor"
		tags = append(tags, "empty-sink-floor-anchor")
	}
	if rebalExecutionDownAnchorActive && rebalExecutionDownAnchorPpm > 0 && floor > rebalExecutionDownAnchorPpm && shouldRelaxFloorForMatureEmptySinkAnchor(floorSrc) {
		floor = rebalExecutionDownAnchorPpm
		floorSrc = "rebal-exec-anchor"
		tags = append(tags, "rebal-exec-floor-anchor")
	}
	if target < localPpm && floor >= localPpm {
		stallRelaxGapFrac := e.profile.StallFloorRelaxGapFrac
		if e.cfg.StallFloorRelaxGapFracOverride > 0 {
			stallRelaxGapFrac = e.cfg.StallFloorRelaxGapFracOverride
		}
		if stallRelaxGapFrac <= 0 {
			stallRelaxGapFrac = stallFloorRelaxGapFrac
		}
		bigGap := (localPpm - target) >= maxInt(100, int(math.Round(float64(localPpm)*stallRelaxGapFrac)))
		canRelaxFloor := st.StalledRounds >= stallFloorRelaxMinRounds &&
			bigGap &&
			outRatio >= stallFloorRelaxMinOutRatio &&
			marginPpm7d >= e.profile.ProfitDownMarginMin
		if canRelaxFloor {
			relaxFrac := stallFloorRelaxStepFracBase
			if st.StalledRounds > stallFloorRelaxMinRounds {
				extraBuckets := (st.StalledRounds - stallFloorRelaxMinRounds) / stallFloorRelaxStepFracRoundWindow
				relaxFrac += float64(extraBuckets) * 0.01
				if relaxFrac > stallFloorRelaxStepFracMax {
					relaxFrac = stallFloorRelaxStepFracMax
				}
			}
			relaxStep := int(math.Round(float64(localPpm) * relaxFrac))
			relaxStep = clampInt(relaxStep, stallFloorRelaxMinStepPpm, stallFloorRelaxMaxStepPpm)
			relaxedFloor := localPpm - relaxStep
			if relaxedFloor < e.cfg.MinPpm {
				relaxedFloor = e.cfg.MinPpm
			}
			if shouldProtectRebalanceFloorFromStallRelax(classLabel, floorSrc, perCost, ranking, hasRanking, outPpm7d) && relaxedFloor < perCost {
				relaxedFloor = perCost
				tags = append(tags, "stall-relax-rebal-guard")
			}
			if relaxedFloor < floor {
				floor = relaxedFloor
				floorSrc = "stall-relax"
				tags = append(tags, "floor-relax-stall")
			}
		}
	}

	negMarginProtectedFloor = deriveNegMarginProtectedFloor(floor, baseCostPpm, recentRebalanceCostPpm, e.cfg.MinPpm, e.cfg.MaxPpm)

	// Seed is a reference for target construction, not a hard fee ceiling.
	finalCandidate := clampInt(maxInt(rawStep, floor), e.cfg.MinPpm, e.cfg.MaxPpm)

	stepMinFinal := minStepDown
	if finalCandidate > localPpm {
		stepMinFinal = minStepUp
	}
	finalPpm := applyStepCap(localPpm, finalCandidate, capFrac, stepMinFinal, capRefPpm)
	if finalPpm < floor {
		finalPpm = floor
	}
	finalPpm = clampInt(finalPpm, e.cfg.MinPpm, e.cfg.MaxPpm)
	if heldPpm, held := applyNoisySurgeFollowUpUpHold(
		localPpm,
		finalPpm,
		floor,
		st.LastDir,
		st.LastTs,
		e.now,
		time.Duration(maxInt(1, cooldownUpSec))*time.Second,
		htlcSampleLow,
		htlcNoisySample,
		htlcHotSignal,
		htlcForwardHot,
		recentRebalanceCount,
		surgeConfirmSignal,
		surgeRoundConfirmSignal,
	); held {
		finalPpm = heldPpm
		tags = appendAutofeeTagOnce(tags, "surge-noisy-followup-hold")
	}

	profitProtectLocked := false
	profitProtectRelaxed := false
	if finalPpm < localPpm &&
		outRatio < profitProtectOutRatio &&
		marginPpm7d < profitProtectMarginPpm &&
		!discoveryHit &&
		!explorerActive &&
		!superSourceActive {
		canRelax := false
		if marginPpm7d >= profitProtectRelaxMarginPpm && fwdCount <= profitProtectRelaxMaxFwds && !st.LastTs.IsZero() {
			if e.now.Sub(st.LastTs) >= time.Duration(profitProtectRelaxHours)*time.Hour {
				relaxStep := int(math.Max(float64(profitProtectRelaxMinStepPpm), math.Round(float64(localPpm)*profitProtectRelaxStepFrac)))
				if relaxStep > 0 {
					minAllowed := localPpm - relaxStep
					if finalPpm < minAllowed {
						finalPpm = minAllowed
					}
					canRelax = true
				}
			}
		}
		if canRelax {
			profitProtectRelaxed = true
		} else {
			adjustedFinal, negMarginDownTag := applyNegMarginProtectedDown(finalPpm, localPpm, negMarginProtectedFloor, negMarginDownStrongPressure)
			if marginPpm7d < 0 && negMarginDownTag == "neg-margin-floor-down" {
				finalPpm = adjustedFinal
				profitProtectRelaxed = true
				tags = appendAutofeeTagOnce(tags, "profit-protect-floor-down")
				tags = appendAutofeeTagOnce(tags, negMarginDownTag)
			} else {
				finalPpm = localPpm
				profitProtectLocked = true
			}
		}
		finalPpm = clampInt(finalPpm, e.cfg.MinPpm, e.cfg.MaxPpm)
	}

	if cappedTarget, cappedPpm, capTags := applyOutnormFallbackUpHold(
		marketRefillMode,
		newInboundBootstrap,
		localPpm,
		target,
		finalPpm,
		outNormMeta,
		outFrom21dFallback,
		rebalFrom21dFallback,
		observedOutSignal,
		observedRebalSignal,
		recentRebalanceCount,
		htlcLiquidityHot,
		surgeConfirmSignal,
	); len(capTags) > 0 {
		target = cappedTarget
		finalPpm = cappedPpm
		tags = append(tags, capTags...)
	}

	if cappedTarget, cappedPpm, capTags := applyHistoryReferenceUpCap(
		marketRefillMode,
		newInboundBootstrap,
		localPpm,
		target,
		finalPpm,
		outPpm7d,
		rebalHistoryRefPpm,
		recentRebalanceCount,
		htlcLiquidityHot,
		surgeConfirmSignal,
	); len(capTags) > 0 {
		target = cappedTarget
		finalPpm = cappedPpm
		tags = append(tags, capTags...)
	}

	floorDrivenUp := finalPpm > localPpm && floor > localPpm && floor > target
	if floorDrivenUp {
		if cappedPpm, capTags := capBalancedFloorDrivenUp(e.profile, classLabel, upwardPressureOutRatio, goodOutRatio, localPpm, target, finalPpm); cappedPpm != finalPpm {
			finalPpm = cappedPpm
			tags = append(tags, capTags...)
			floorDrivenUp = finalPpm > localPpm && floor > localPpm && floor > target
		}
	}
	weakDemandSignal := !surgeConfirmSignal &&
		!(htlcForwardHot && htlcAttempts >= surgeConfirmAttemptsMin) &&
		!htlcHotSignal &&
		fwdCount < 4
	if floorDrivenUp && weakDemandSignal {
		if adjustedPpm, explorerTags := capWeakDemandFloorUpForDrainedExplorer(e.profile, drainedExplorerActive, localPpm, finalPpm); len(explorerTags) > 0 {
			finalPpm = adjustedPpm
			tags = append(tags, explorerTags...)
		} else {
			finalPpm = localPpm
			tags = append(tags, "floor-up-blocked-low-signal")
		}
	}

	if rawStep != target {
		dirSame := (target > localPpm && rawStep > localPpm) || (target < localPpm && rawStep < localPpm)
		if dirSame {
			tags = append(tags, "stepcap")
		}
	}
	if finalPpm == floor && target != floor {
		tags = append(tags, "floor-lock")
	}
	if finalPpm == localPpm && target != localPpm && floor <= localPpm {
		tags = append(tags, "stepcap-lock")
	}
	if globalNegLockApplied {
		tags = append(tags, "global-neg-lock")
	}
	if lockSkipTag != "" {
		tags = append(tags, lockSkipTag)
	}
	if profitProtectLocked {
		tags = append(tags, "profit-protect-lock")
	}
	if profitProtectRelaxed {
		tags = append(tags, "profit-protect-relax")
	}

	// Final safety lock: never reduce below current ppm while margin is negative.
	// This runs after all ceilings/floors/step caps to avoid late-stage overrides.
	if marginPpm7d < 0 && finalPpm < localPpm && !stagnationActive && !highOutStagnationPressure {
		if seedSoftNegMarginRelax {
			if !containsTag(tags, "seed:soft-neg-relax") {
				tags = append(tags, "seed:soft-neg-relax")
			}
		} else if rebalExecutionDownAnchorActive {
			if !containsTag(tags, "rebal-exec-neg-relax") {
				tags = append(tags, "rebal-exec-neg-relax")
			}
		} else {
			adjustedFinal, negMarginDownTag := applyNegMarginProtectedDown(finalPpm, localPpm, negMarginProtectedFloor, negMarginDownStrongPressure)
			finalPpm = adjustedFinal
			if negMarginDownTag != "" {
				tags = appendAutofeeTagOnce(tags, negMarginDownTag)
			}
		}
	}
	if holdUpOnRecentRebalance && finalPpm > localPpm {
		finalPpm = localPpm
		if !containsTag(tags, "rebal-recent-noup") {
			tags = append(tags, "rebal-recent-noup")
		}
	}
	if surgeHoldActive && finalPpm > localPpm {
		finalPpm = localPpm
		if !containsTag(tags, "surge-hold-lock") {
			tags = append(tags, "surge-hold-lock")
		}
	}
	if adjusted, clipped := capDownMoveGeneral(localPpm, finalPpm, htlcSampleLow); clipped {
		finalPpm = adjusted
		tags = append(tags, "downcap-general")
	}
	if adjusted, clipped := capDownMoveForLowHTLCSample(localPpm, finalPpm, htlcSampleLow); clipped {
		finalPpm = adjusted
		tags = append(tags, "htlc-low-sample-downcap")
	}
	recentRebalanceHardFloorApplied := false
	if adjusted, applied := applyRecentRebalanceCostHardFloor(finalPpm, recentRebalanceCostPpm, e.cfg.MinPpm, e.cfg.MaxPpm); applied {
		finalPpm = adjusted
		recentRebalanceHardFloorApplied = true
		tags = appendAutofeeTagOnce(tags, "rebal-recent-hard-floor")
	}
	targetGapPpm := target - localPpm
	targetGapPct := calcTargetGapPct(localPpm, target)
	reversalConfirmRounds := reversalConfirmRoundsForChannel(e.profile, st, targetGapPct)
	if antiFlipExtraRounds, antiFlipTags := antiFlipExtraConfirmRoundsForChannel(e.profile, st, e.now, localPpm, finalPpm, targetGapPct, tags); antiFlipExtraRounds > 0 {
		reversalConfirmRounds += antiFlipExtraRounds
		tags = append(tags, antiFlipTags...)
	}
	if marketRefillMode && shouldBypassMarketRefillReversalGuard(tags, targetGapPct, finalPpm, localPpm) {
		tags = append(tags, "market-refill-reversal-bypass")
	} else if guardedPpm, reversalTags := applyDirectionReversalGuard(st, localPpm, finalPpm, reversalConfirmRounds); true {
		finalPpm = guardedPpm
		if len(reversalTags) > 0 {
			tags = append(tags, reversalTags...)
		}
	}
	if shouldHoldAutofeeSmallDelta(localPpm, finalPpm) {
		finalPpm = localPpm
		tags = appendAutofeeTagOnce(tags, "hold-small")
		tags = appendAutofeeTagOnce(tags, "small-delta")
	}

	apply := true
	delta := int(math.Abs(float64(finalPpm - localPpm)))
	floorDrivenStepUp := finalPpm > localPpm && floor > localPpm
	surgeDrivenStepUp := containsTag(tags, "surge+20%") ||
		containsTag(tags, "surge-hold-flow") ||
		containsTag(tags, "surge-timeout-release") ||
		containsTag(tags, "surge-confirmed-rounds")
	allowSmallStep := newInboundBootstrap && finalPpm > localPpm
	if floorDrivenStepUp && finalPpm > localPpm {
		if delta >= floorDrivenSmallUpMinStepPpm || newInboundBootstrap || surgeDrivenStepUp {
			allowSmallStep = true
		}
	}
	if shouldHoldSmallStep(e.profile, st, localPpm, finalPpm, target, allowSmallStep) {
		apply = false
		tags = append(tags, "hold-small")
	}
	if adjusted, applied := applyRecentRebalanceCostHardFloor(finalPpm, recentRebalanceCostPpm, e.cfg.MinPpm, e.cfg.MaxPpm); applied {
		finalPpm = adjusted
		apply = true
		delta = int(math.Abs(float64(finalPpm - localPpm)))
		floorDrivenStepUp = finalPpm > localPpm && floor > localPpm
		recentRebalanceHardFloorApplied = true
		tags = appendAutofeeTagOnce(tags, "rebal-recent-hard-floor")
	}
	skipCooldownDown := explorerActive && finalPpm < localPpm && e.profile.ExplorerSkipCooldownDown
	if skipCooldownDown {
		tags = append(tags, "cooldown-skip")
	}
	if finalPpm != localPpm && !e.ignoreCooldown && !skipCooldownDown {
		cooldownHours := float64(maxInt(1, cooldownDownSec)) / 3600.0
		bypassUpCooldown := false
		if finalPpm > localPpm {
			cooldownHours = float64(maxInt(1, effectiveCooldownUpSecForChannel(e.profile, cooldownUpSec, outRatio, holdUpOnRecentRebalance))) / 3600.0
			bypassUpCooldown = recentRebalanceHardFloorApplied || shouldBypassStrictCooldownUp(
				e.profile,
				recentRebalanceCount,
				htlcLiquidityHot,
				localPpm,
				outPpm7d,
				rebalHistoryRefPpm,
				baseCostPpm,
				baseCostSrc,
			)
		}
		if !st.LastTs.IsZero() {
			hoursSince := e.now.Sub(st.LastTs).Hours()
			if hoursSince < cooldownHours && !(finalPpm > localPpm && bypassUpCooldown) {
				apply = false
				if !containsTag(tags, "cooldown") {
					tags = append(tags, "cooldown")
				}
			} else if finalPpm > localPpm && bypassUpCooldown {
				tags = append(tags, "cooldown-up-bypass")
			}
		}
	}

	if finalPpm < localPpm && !e.ignoreCooldown && !skipCooldownDown && !st.LastTs.IsZero() &&
		marginPpm7d >= e.profile.ProfitDownMarginMin && fwdCount >= e.profile.ProfitDownFwdsMin {
		hoursSince := e.now.Sub(st.LastTs).Hours()
		profitCooldown := float64(maxInt(1, cooldownDownSec))/3600.0 + float64(e.profile.ProfitDownExtraHours)
		if hoursSince < profitCooldown {
			apply = false
			if !containsTag(tags, "cooldown") {
				tags = append(tags, "cooldown")
			}
			if !containsTag(tags, "cooldown-profit") {
				tags = append(tags, "cooldown-profit")
			}
		}
	}
	if apply && finalPpm != localPpm {
		if churnTags := shouldHoldForAutofeeChurn(e.profile, e.recentChanges[ch.ChannelID], localPpm, finalPpm, recentRebalanceCount, htlcLiquidityHot); len(churnTags) > 0 {
			apply = false
			tags = append(tags, churnTags...)
		}
	}
	if apply && finalPpm != localPpm && shouldHoldAutofeeForRebalanceSettling(rebalanceTouch, e.now, autofeeRebalanceSettlingWindow) {
		if recentRebalanceHardFloorApplied && finalPpm > localPpm {
			tags = appendAutofeeTagOnce(tags, "rebal-recent-settling-bypass")
		} else {
			apply = false
			if !containsTag(tags, "autofee_settling") {
				tags = append(tags, "autofee_settling")
			}
		}
	}

	if fwdCount > 0 {
		if st.BaselineFwd7d > 0 {
			st.BaselineFwd7d = int(math.Round(0.7*float64(st.BaselineFwd7d) + 0.3*float64(fwdCount)))
		} else {
			st.BaselineFwd7d = fwdCount
		}
	}
	applyOutbound := apply && finalPpm != localPpm
	appliedPpm := localPpm
	if applyOutbound {
		appliedPpm = finalPpm
	}
	inboundDiscountMaxRatio := inboundDiscountMaxRatioFromConfig(e.cfg)
	inboundDiscountReachOutRatio := e.profile.InboundDiscountReachOutRatio
	if inboundDiscountReachOutRatio <= 0 {
		inboundDiscountReachOutRatio = 0.10
	}
	if e.cfg.InboundDiscountReachOutRatioOverride > 0 {
		inboundDiscountReachOutRatio = e.cfg.InboundDiscountReachOutRatioOverride
	}
	inboundDiscountMinRetainedSpreadFrac := e.profile.InboundDiscountMinRetainedSpreadFrac
	if inboundDiscountMinRetainedSpreadFrac <= 0 {
		inboundDiscountMinRetainedSpreadFrac = 0.12
	}
	if e.cfg.InboundDiscountMinRetainedSpreadFracOverride > 0 {
		inboundDiscountMinRetainedSpreadFrac = e.cfg.InboundDiscountMinRetainedSpreadFracOverride
	}
	inboundDiscount := 0
	if e.cfg.OperationMode == autofeeOperationModeMarketRefill {
		var inboundTags []string
		inboundDiscount, inboundTags = computeMarketRefillInboundDiscount(
			true,
			outNormMeta.Effective,
			recentForwards1d,
			noFlow1d,
			weakRecentFlow,
			peerMarketSkew,
			baseCostPpm,
			appliedPpm,
			inboundDiscountMaxRatio,
			inboundDiscountMinRetainedSpreadFrac,
			e.profile,
		)
		tags = append(tags, inboundTags...)
	} else {
		var inboundSource string
		inboundDiscount, inboundSource = computeBalancedInboundDiscount(
			e.cfg.InboundPassiveEnabled,
			classLabel,
			outRatio,
			fwdCount,
			marginPpm7d,
			baseCostPpm,
			appliedPpm,
			inboundDiscountMaxRatio,
			inboundDiscountReachOutRatio,
			inboundDiscountMinRetainedSpreadFrac,
		)
		switch inboundSource {
		case "balanced-low-sample":
			tags = appendAutofeeTagOnce(tags, "inbound-low-sample")
		case "balanced":
			tags = appendAutofeeTagOnce(tags, "inbound-balanced")
		}
	}
	if normalizeAutofeeOperationMode(e.cfg.OperationMode) != autofeeOperationModeMarketRefill && e.cfg.InboundPassiveEnabled && inboundDiscount <= 0 {
		if normalizedInboundDiscount, normalized := normalizeStaleInboundDiscount(prevInboundDiscount, appliedPpm, inboundDiscountMaxRatio); normalized {
			inboundDiscount = normalizedInboundDiscount
			tags = appendAutofeeTagOnce(tags, "inbound-normalize")
		} else {
			inboundDiscount = prevInboundDiscount
		}
	}
	inboundChanged := inboundDiscount != prevInboundDiscount
	st.LastInboundDiscount = inboundDiscount
	if outPpm7d > 0 {
		st.LastOutrate = outPpm7d
		if shouldRefreshAutofeeOutrateMemory(outPpm7d, recentForwards1d, outAmt1dSat) {
			st.LastOutrateTs = e.now
		}
	}

	if finalPpm == localPpm {
		tags = append(tags, "same-ppm")
	}
	if finalPpm == localPpm && target != localPpm {
		st.StalledRounds++
	} else if finalPpm != localPpm {
		st.StalledRounds = 0
	} else if target == localPpm {
		st.StalledRounds = 0
	}
	if finalPpm == localPpm && target != localPpm && shouldEmitStallAlert(st.StalledRounds, targetGapPct) {
		tags = append(tags, "stall-alert")
	}
	if applyOutbound {
		prevDir := st.LastDir
		st.LastTs = e.now
		if finalPpm > localPpm {
			st.LastDir = "up"
		} else if finalPpm < localPpm {
			st.LastDir = "down"
		}
		if prevDir != "" && st.LastDir != "" && prevDir != st.LastDir {
			st.ExplorerState.LastReversalDir = st.LastDir
			st.ExplorerState.LastReversalTs = e.now.Unix()
		}
	}

	if explorerActive && finalPpm < localPpm {
		st.ExplorerState.Rounds++
	}
	if drainedExplorerActive && finalPpm > localPpm {
		st.ExplorerState.DrainedRounds++
	}
	hoursSinceLastChange := 0.0
	if !st.LastTs.IsZero() {
		hoursSinceLastChange = e.now.Sub(st.LastTs).Hours()
		if hoursSinceLastChange < 0 {
			hoursSinceLastChange = 0
		}
	}

	cooldownRemaining := 0.0
	if containsTag(tags, "cooldown") && !st.LastTs.IsZero() {
		hoursSince := e.now.Sub(st.LastTs).Hours()
		cooldownHours := float64(maxInt(1, cooldownDownSec)) / 3600.0
		if finalPpm > localPpm {
			cooldownHours = float64(maxInt(1, effectiveCooldownUpSecForChannel(e.profile, cooldownUpSec, outRatio, holdUpOnRecentRebalance))) / 3600.0
		}
		remaining := cooldownHours - hoursSince
		if remaining > 0 {
			cooldownRemaining = remaining
		}
	}
	effectiveApply := applyOutbound || inboundChanged
	effectiveNewPpm := finalPpm
	if !applyOutbound {
		effectiveNewPpm = localPpm
	}
	st.LastPpm = effectiveNewPpm
	predictionTarget := target
	targetDir := 0
	if target > localPpm {
		targetDir = 1
	} else if target < localPpm {
		targetDir = -1
	}
	finalDir := 0
	if effectiveNewPpm > localPpm {
		finalDir = 1
	} else if effectiveNewPpm < localPpm {
		finalDir = -1
	}
	if (targetDir != 0 && finalDir != 0 && targetDir != finalDir) || (targetDir == 0 && finalDir != 0) {
		predictionTarget = effectiveNewPpm
	}
	predictionCode, predictionCooldownHours := buildAutofeePrediction(outRatio, marginPpm7d, predictionTarget, localPpm, effectiveNewPpm, fwdCount, negMarginGlobal, discoveryHit, cooldownRemaining)
	logStagnationPhase := 0
	logStagnationRounds := 0
	logStagnationCap := 0
	if stagnationActive {
		logStagnationPhase = stagnationPhase
		logStagnationRounds = stagnationRounds
		logStagnationCap = stagnationTargetCap
	}
	loggedRebalPpm := 0
	switch {
	case perCost > 0:
		loggedRebalPpm = perCost
	case perCost21d > 0:
		loggedRebalPpm = perCost21d
	case st.LastRebalCost > 0 && !st.LastRebalCostTs.IsZero() && e.now.Sub(st.LastRebalCostTs) <= 21*24*time.Hour:
		loggedRebalPpm = st.LastRebalCost
	case !marketRefillMode && normalizeRebalCostMode(e.cfg.RebalCostMode) == "global" && rebalGlobalPpm > 0:
		loggedRebalPpm = rebalGlobalPpm
	}

	tags = append(tags, seedTags...)
	return &decision{
		ChannelID:               ch.ChannelID,
		ChannelPoint:            ch.ChannelPoint,
		Alias:                   strings.TrimSpace(ch.PeerAlias),
		LocalPpm:                localPpm,
		NewPpm:                  effectiveNewPpm,
		Target:                  target,
		TargetRaw:               target,
		TargetFinal:             effectiveNewPpm,
		Floor:                   floor,
		FloorSrc:                floorSrc,
		FloorBasePpm:            floorBasePpm,
		FloorBaseSrc:            floorBaseSrc,
		RebalCostMode:           normalizeRebalCostMode(e.cfg.RebalCostMode),
		Tags:                    tags,
		InboundDiscount:         inboundDiscount,
		PrevInboundDiscount:     prevInboundDiscount,
		SuperSourceActive:       superSourceActive,
		OutRatio:                rawOutRatio,
		OutRatioEffective:       outRatio,
		OutPpm7d:                outPpm7d,
		RebalPpm:                loggedRebalPpm,
		Seed:                    int(seed),
		Margin:                  marginPpm7d,
		RevShare:                revShare,
		ClassLabel:              classLabel,
		FwdCount:                fwdCount,
		NegMarginGlobal:         negMarginGlobal,
		PredictionCode:          predictionCode,
		PredictionCooldownHours: predictionCooldownHours,
		NewInbound:              newInboundBootstrap,
		ChannelAgeHours:         channelAgeHours,
		HTLCAttempts:            htlcAttempts,
		HTLCForwardFails:        htlcForwardFails,
		HTLCPolicyFails:         htlcPolicyFails,
		HTLCLiquidityFails:      htlcLiquidityFails,
		HTLCUnclassifiedFails:   htlcUnclassifiedFails,
		HTLCWindowMin:           htlcWindowMin,
		StagnationPhase:         logStagnationPhase,
		StagnationRounds:        logStagnationRounds,
		StagnationCap:           logStagnationCap,
		StalledRounds:           st.StalledRounds,
		HoursSinceLastChange:    hoursSinceLastChange,
		TargetGapPpm:            targetGapPpm,
		TargetGapPct:            targetGapPct,
		Apply:                   effectiveApply,
		State:                   st,
	}
}

func (e *autofeeEngine) evalExplorer(st *autofeeChannelState, outRatio float64, fwdCount int, localBal int64, capacity int64, localPpm int) bool {
	nowTs := e.now.Unix()
	active := st.ExplorerState.Active
	if !active {
		daysSince := 999.0
		if !st.LastTs.IsZero() {
			daysSince = e.now.Sub(st.LastTs).Hours() / 24.0
		}
		if daysSince >= 7 && outRatio >= 0.50 && fwdCount <= 5 {
			stagnationNoFwdRounds := st.ExplorerState.StagnationNoFwdRounds
			stagnationPhase := st.ExplorerState.StagnationPhase
			surgeGateRounds := st.ExplorerState.SurgeGateRounds
			surgeGatePpm := st.ExplorerState.SurgeGatePpm
			drainedActive := st.ExplorerState.DrainedActive
			drainedStartedTs := st.ExplorerState.DrainedStartedTs
			drainedRounds := st.ExplorerState.DrainedRounds
			drainedFwdsAtStart := st.ExplorerState.DrainedFwdsAtStart
			drainedLastExitTs := st.ExplorerState.DrainedLastExitTs
			drainedSeen := st.ExplorerState.DrainedSeen
			st.ExplorerState = explorerState{
				Active:                true,
				StartedTs:             nowTs,
				Rounds:                0,
				FwdsAtStart:           fwdCount,
				Seen:                  true,
				DrainedActive:         drainedActive,
				DrainedStartedTs:      drainedStartedTs,
				DrainedRounds:         drainedRounds,
				DrainedFwdsAtStart:    drainedFwdsAtStart,
				DrainedLastExitTs:     drainedLastExitTs,
				DrainedSeen:           drainedSeen,
				StagnationNoFwdRounds: stagnationNoFwdRounds,
				StagnationPhase:       stagnationPhase,
				SurgeGateRounds:       surgeGateRounds,
				SurgeGatePpm:          surgeGatePpm,
			}
			return true
		}
		return false
	}

	hoursSince := float64(nowTs-st.ExplorerState.StartedTs) / 3600.0
	fwdsSince := fwdCount - st.ExplorerState.FwdsAtStart
	if fwdsSince >= 1 || hoursSince >= 48 || st.ExplorerState.Rounds >= 3 {
		st.ExplorerState.Active = false
		st.ExplorerState.LastExitTs = nowTs
		return false
	}
	return true
}

func (e *autofeeEngine) evalDrainedExplorer(st *autofeeChannelState, outRatio float64, recentForwards1d int, recentRebalanceCount int) bool {
	nowTs := e.now.Unix()
	active := st.ExplorerState.DrainedActive
	if !active {
		daysSince := 999.0
		if !st.LastTs.IsZero() {
			daysSince = e.now.Sub(st.LastTs).Hours() / 24.0
		}
		maxFwds := maxInt(0, e.profile.DrainedExplorerRecentFwds1dMax)
		if recentRebalanceCount == 0 &&
			daysSince >= float64(maxInt(1, e.profile.DrainedExplorerAfterDays)) &&
			outRatio <= e.profile.DrainedExplorerOutRatioMax &&
			recentForwards1d <= maxFwds {
			st.ExplorerState.DrainedActive = true
			st.ExplorerState.DrainedStartedTs = nowTs
			st.ExplorerState.DrainedRounds = 0
			st.ExplorerState.DrainedFwdsAtStart = recentForwards1d
			st.ExplorerState.DrainedSeen = true
			return true
		}
		return false
	}

	hoursSince := float64(nowTs-st.ExplorerState.DrainedStartedTs) / 3600.0
	fwdsSince := recentForwards1d - st.ExplorerState.DrainedFwdsAtStart
	maxFwds := maxInt(0, e.profile.DrainedExplorerRecentFwds1dMax)
	maxHours := maxInt(1, e.profile.DrainedExplorerMaxHours)
	maxRounds := maxInt(1, e.profile.DrainedExplorerMaxRounds)
	exitOutRatio := e.profile.DrainedExplorerOutRatioMax * 1.5
	if exitOutRatio <= 0 {
		exitOutRatio = e.profile.DrainedExplorerOutRatioMax
	}
	if recentRebalanceCount > 0 ||
		fwdsSince >= 1 ||
		recentForwards1d > maxFwds ||
		hoursSince >= float64(maxHours) ||
		st.ExplorerState.DrainedRounds >= maxRounds ||
		(exitOutRatio > 0 && outRatio > exitOutRatio) {
		st.ExplorerState.DrainedActive = false
		st.ExplorerState.DrainedLastExitTs = nowTs
		return false
	}
	return true
}

func applyDrainedExplorerTarget(profile autofeeProfile, active bool, rounds int, localPpm int, targetPpm int, seed float64, baseCostPpm int) (int, []string) {
	if !active || localPpm < 0 {
		return targetPpm, nil
	}
	stepPpm := maxInt(1, profile.DrainedExplorerStepPpm)
	tags := []string{"drained-explorer", fmt.Sprintf("drained-explorer-r%d", maxInt(1, rounds+1))}
	minTarget := localPpm + stepPpm
	if targetPpm < minTarget {
		targetPpm = minTarget
		tags = append(tags, "drained-explorer-step")
	}
	anchorPpm := maxInt(baseCostPpm, int(math.Round(seed)))
	if anchorPpm > 0 {
		ceiling := int(math.Ceil(float64(anchorPpm) * 1.10))
		if ceiling < minTarget {
			ceiling = minTarget
		}
		if targetPpm > ceiling {
			targetPpm = ceiling
			tags = append(tags, "drained-explorer-cap")
		}
	}
	return targetPpm, tags
}

func capWeakDemandFloorUpForDrainedExplorer(profile autofeeProfile, active bool, localPpm int, finalPpm int) (int, []string) {
	if !active || finalPpm <= localPpm {
		return finalPpm, nil
	}
	stepPpm := maxInt(1, profile.DrainedExplorerStepPpm)
	maxAllowed := localPpm + stepPpm
	if finalPpm > maxAllowed {
		return maxAllowed, []string{"drained-explorer-cap"}
	}
	return finalPpm, nil
}

func (e *autofeeEngine) seedForChannel(pubkey string, st *autofeeChannelState) (float64, float64, float64, []string) {
	tags := []string{}
	nativeAttempted := false
	if e.cfg.NativeSeedEnabled && pubkey != "" {
		nativeAttempted = true
		seed, seedP95, peerMarketSkew, nativeTags, ok, err := e.fetchNativeSeed(pubkey)
		tags = append(tags, nativeTags...)
		if err == nil && ok {
			if st != nil && st.LastSeed > 0 && e.profile.SeedGuardMaxJump > 0 {
				maxJump := 1.0 + e.profile.SeedGuardMaxJump
				maxAllowed := float64(st.LastSeed) * maxJump
				if seed > maxAllowed {
					seed = maxAllowed
					tags = append(tags, "seed:guard")
				}
			}
			return seed, seedP95, peerMarketSkew, tags
		}
	}
	if e.cfg.AmbossEnabled {
		token, err := e.cachedAmbossToken(context.Background())
		if err != nil {
			tags = append(tags, "seed:amboss-error")
		} else if token == "" {
			tags = append(tags, "seed:amboss-missing")
		} else if pubkey != "" {
			seed, seedP95, peerMarketSkew, seedTags, err := e.fetchAmbossSeed(pubkey, token)
			if err != nil {
				tags = append(tags, "seed:amboss-error")
			} else if seed > 0 {
				tags = append(tags, seedTags...)
				if st != nil && st.LastSeed > 0 && e.profile.SeedGuardMaxJump > 0 {
					maxJump := 1.0 + e.profile.SeedGuardMaxJump
					maxAllowed := float64(st.LastSeed) * maxJump
					if seed > maxAllowed {
						seed = maxAllowed
						tags = append(tags, "seed:guard")
					}
				}
				if nativeAttempted && !containsTag(tags, "seed:native") {
					tags = append(tags, "seed:native-fallback-amboss")
				}
				return seed, seedP95, peerMarketSkew, tags
			} else {
				tags = append(tags, "seed:amboss-empty")
			}
		}
	}

	if hasRecentAutofeeOutrateMemory(st, e.now) {
		return float64(st.LastOutrate), 0, 0, append(tags, "seed:outrate")
	}
	if st.LastSeed > 0 {
		return float64(st.LastSeed), 0, 0, append(tags, "seed:mem")
	}
	return 200.0, 0, 0, append(tags, "seed:default")
}

func (e *autofeeEngine) refreshSeedForChannel(ctx context.Context, pubkey string) (float64, string, error) {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return 0, "", nil
	}

	var nativeErr error
	if e.cfg.NativeSeedEnabled {
		seed, _, _, _, ok, err := e.fetchNativeSeed(pubkey)
		if err != nil {
			nativeErr = err
		} else if ok && seed > 0 {
			return seed, "seed:native", nil
		}
	}

	if e.cfg.AmbossEnabled {
		token, err := e.cachedAmbossToken(ctx)
		if err != nil {
			return 0, "", err
		}
		if token != "" {
			seed, _, _, _, err := e.fetchAmbossSeed(pubkey, token)
			if err != nil {
				return 0, "", err
			}
			if seed > 0 {
				return seed, "seed:amboss", nil
			}
		}
	}

	if nativeErr != nil {
		return 0, "", nativeErr
	}

	return 0, "", nil
}

func (e *autofeeEngine) fetchAmbossToken(ctx context.Context) (string, error) {
	var raw pgtype.Text
	err := e.svc.db.QueryRow(ctx, `select amboss_token from autofee_config where id=$1`, autofeeConfigID).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if raw.Valid {
		return strings.TrimSpace(raw.String), nil
	}
	return "", nil
}

func (e *autofeeEngine) cachedAmbossToken(ctx context.Context) (string, error) {
	if e.ambossTokenLoad {
		return e.ambossToken, e.ambossTokenErr
	}
	token, err := e.fetchAmbossToken(ctx)
	e.ambossToken = token
	e.ambossTokenErr = err
	e.ambossTokenLoad = true
	return token, err
}

func (e *autofeeEngine) fetchNativeSeed(pubkey string) (float64, float64, float64, []string, bool, error) {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return 0, 0, 0, nil, false, nil
	}
	if cached, ok := e.nativeSeedCache[pubkey]; ok {
		return cached.Seed, cached.SeedP95, cached.PeerMarketSkew, append([]string{}, cached.Tags...), cached.Ok, cached.Err
	}
	if e.svc == nil || e.svc.db == nil {
		result := autofeeSeedResult{Tags: []string{"seed:native-error"}, Err: errors.New("db unavailable")}
		e.nativeSeedCache[pubkey] = result
		return 0, 0, 0, append([]string{}, result.Tags...), false, result.Err
	}

	end := e.now.UTC().Truncate(24 * time.Hour)
	since := end.AddDate(0, 0, -maxInt(autofeeMinLookbackDays, e.cfg.LookbackDays))
	rows, err := e.svc.db.Query(context.Background(), `
select
  to_char(date_trunc('day', h.captured_at at time zone 'UTC'), 'YYYY-MM-DD') as day,
  coalesce(round(
    sum(case when h.connecting_pubkey = $1 and h.advertising_pubkey <> $1 then h.fee_rate_ppm::numeric * greatest(ch.capacity_sat, 0)::numeric else 0 end)
    / nullif(sum(case when h.connecting_pubkey = $1 and h.advertising_pubkey <> $1 then greatest(ch.capacity_sat, 0)::numeric else 0 end), 0)
  ), 0)::bigint as inbound_weighted_avg_ppm,
  count(*) filter (where h.connecting_pubkey = $1 and h.advertising_pubkey <> $1) as inbound_sample_count,
  coalesce(round(
    sum(case when h.advertising_pubkey = $1 then h.fee_rate_ppm::numeric * greatest(ch.capacity_sat, 0)::numeric else 0 end)
    / nullif(sum(case when h.advertising_pubkey = $1 then greatest(ch.capacity_sat, 0)::numeric else 0 end), 0)
  ), 0)::bigint as outbound_weighted_avg_ppm,
  count(*) filter (where h.advertising_pubkey = $1) as outbound_sample_count
from graph_channel_policy_history h
join graph_channels ch on ch.chan_id = h.chan_id
where (h.advertising_pubkey = $1 or h.connecting_pubkey = $1)
  and h.captured_at >= $2
  and h.captured_at < $3
group by 1
order by 1 desc
`, pubkey, since, end)
	if err != nil {
		result := autofeeSeedResult{Tags: []string{"seed:native-error"}, Err: err}
		e.nativeSeedCache[pubkey] = result
		return 0, 0, 0, append([]string{}, result.Tags...), false, err
	}
	defer rows.Close()

	inboundVals := make([]float64, 0, maxInt(autofeeMinLookbackDays, e.cfg.LookbackDays))
	outboundVals := make([]float64, 0, maxInt(autofeeMinLookbackDays, e.cfg.LookbackDays))
	totalInboundSamples := int64(0)
	for rows.Next() {
		var day string
		var inboundWeightedAvg int64
		var inboundSamples int64
		var outboundWeightedAvg int64
		var outboundSamples int64
		if err := rows.Scan(&day, &inboundWeightedAvg, &inboundSamples, &outboundWeightedAvg, &outboundSamples); err != nil {
			result := autofeeSeedResult{Tags: []string{"seed:native-error"}, Err: err}
			e.nativeSeedCache[pubkey] = result
			return 0, 0, 0, append([]string{}, result.Tags...), false, err
		}
		if inboundSamples > 0 {
			inboundVals = append(inboundVals, float64(inboundWeightedAvg))
			totalInboundSamples += inboundSamples
		}
		if outboundSamples > 0 {
			outboundVals = append(outboundVals, float64(outboundWeightedAvg))
		}
	}
	if err := rows.Err(); err != nil {
		result := autofeeSeedResult{Tags: []string{"seed:native-error"}, Err: err}
		e.nativeSeedCache[pubkey] = result
		return 0, 0, 0, append([]string{}, result.Tags...), false, err
	}
	if len(inboundVals) < autofeeNativeSeedMinDays || totalInboundSamples < autofeeNativeSeedMinSamples {
		result := autofeeSeedResult{Tags: []string{"seed:native-insufficient"}}
		e.nativeSeedCache[pubkey] = result
		return 0, 0, 0, append([]string{}, result.Tags...), false, nil
	}

	p65 := percentile(append([]float64{}, inboundVals...), 0.65)
	p95 := percentile(append([]float64{}, inboundVals...), 0.95)
	if p95 <= 0 {
		result := autofeeSeedResult{Tags: []string{"seed:native-empty"}}
		e.nativeSeedCache[pubkey] = result
		return 0, 0, 0, append([]string{}, result.Tags...), false, nil
	}

	seed := p65
	peerMarketSkew := 0.0
	tags := []string{}

	incMedian := percentile(append([]float64{}, inboundVals...), 0.50)
	incMean := averageFloat64(inboundVals)
	incStd := stddevFloat64(inboundVals, incMean)
	if incMedian > 0 {
		seed = (1.0-0.30)*seed + 0.30*incMedian
		tags = append(tags, "seed:native-med")
	}
	if incMean > 0 && incStd > 0 {
		sigmaMu := incStd / incMean
		pen := math.Min(0.15, 0.25*sigmaMu)
		if pen > 0 {
			seed = seed * (1.0 - pen)
			tags = append(tags, fmt.Sprintf("seed:native-vol-%d%%", int(math.Round(pen*100))))
		}
	}

	outMean := averageFloat64(outboundVals)
	if incMean > 0 && outMean > 0 {
		peerMarketSkew = outMean / incMean
		f := 1.0 + 0.20*(peerMarketSkew-1.0)
		if f < 0.80 {
			f = 0.80
		} else if f > 1.50 {
			f = 1.50
		}
		if math.Abs(f-1.0) > 0.001 {
			seed = seed * f
			tags = append(tags, fmt.Sprintf("seed:native-ratiox%.2f", f))
		}
	}

	if seed > p95 {
		seed = p95
		tags = append(tags, "seed:native-p95cap")
	}
	if e.cfg.MaxPpm > 0 && seed > float64(e.cfg.MaxPpm) {
		seed = float64(e.cfg.MaxPpm)
		tags = append(tags, "seed:maxppm")
	}
	tags = append(tags, "seed:native")
	result := autofeeSeedResult{
		Seed:           seed,
		SeedP95:        p95,
		PeerMarketSkew: peerMarketSkew,
		Tags:           append([]string{}, tags...),
		Ok:             true,
	}
	e.nativeSeedCache[pubkey] = result
	return seed, p95, peerMarketSkew, append([]string{}, tags...), true, nil
}

type ambossSeriesResp struct {
	Data struct {
		GetNodeMetrics struct {
			HistoricalSeries [][]any `json:"historical_series"`
		} `json:"getNodeMetrics"`
	} `json:"data"`
}

func (e *autofeeEngine) fetchAmbossSeed(pubkey string, token string) (float64, float64, float64, []string, error) {
	vals, err := fetchAmbossSeries(pubkey, token, e.cfg.LookbackDays, "incoming_fee_rate_metrics", "weighted_corrected_mean")
	if err != nil {
		return 0, 0, 0, nil, err
	}
	if len(vals) == 0 {
		return 0, 0, 0, nil, nil
	}
	p65 := percentile(vals, 0.65)
	p95 := percentile(vals, 0.95)
	seed := p65
	peerMarketSkew := 0.0
	tags := []string{}

	incMedian, _ := ambossAvgSeries(pubkey, token, e.cfg.LookbackDays, "incoming_fee_rate_metrics", "median")
	incMean, _ := ambossAvgSeries(pubkey, token, e.cfg.LookbackDays, "incoming_fee_rate_metrics", "mean")
	incStd, _ := ambossAvgSeries(pubkey, token, e.cfg.LookbackDays, "incoming_fee_rate_metrics", "std")

	if incMedian > 0 {
		seed = (1.0-0.30)*seed + 0.30*incMedian
		tags = append(tags, "seed:med")
	}
	if incMean > 0 && incStd > 0 {
		sigmaMu := incStd / incMean
		pen := math.Min(0.15, 0.25*sigmaMu)
		if pen > 0 {
			seed = seed * (1.0 - pen)
			tags = append(tags, fmt.Sprintf("seed:vol-%d%%", int(math.Round(pen*100))))
		}
	}

	incWcorr, _ := ambossAvgSeries(pubkey, token, e.cfg.LookbackDays, "incoming_fee_rate_metrics", "weighted_corrected_mean")
	outWcorr, _ := ambossAvgSeries(pubkey, token, e.cfg.LookbackDays, "outgoing_fee_rate_metrics", "weighted_corrected_mean")
	if incWcorr > 0 && outWcorr > 0 {
		peerMarketSkew = outWcorr / incWcorr
		f := 1.0 + 0.20*(peerMarketSkew-1.0)
		if f < 0.80 {
			f = 0.80
		} else if f > 1.50 {
			f = 1.50
		}
		if math.Abs(f-1.0) > 0.001 {
			seed = seed * f
			tags = append(tags, fmt.Sprintf("seed:ratio×%.2f", f))
		}
	}

	if p95 > 0 && seed > p95 {
		seed = p95
		tags = append(tags, "seed:p95cap")
	}
	if e.cfg.MaxPpm > 0 && seed > float64(e.cfg.MaxPpm) {
		seed = float64(e.cfg.MaxPpm)
		tags = append(tags, "seed:maxppm")
	}
	tags = append(tags, "seed:amboss")
	return seed, p95, peerMarketSkew, tags, nil
}

func ambossAvgSeries(pubkey string, token string, lookbackDays int, metric string, submetric string) (float64, error) {
	vals, err := fetchAmbossSeries(pubkey, token, lookbackDays, metric, submetric)
	if err != nil {
		return 0, err
	}
	if len(vals) == 0 {
		return 0, nil
	}
	total := 0.0
	for _, v := range vals {
		total += v
	}
	return total / float64(len(vals)), nil
}

func fetchAmbossSeries(pubkey string, token string, lookbackDays int, metric string, submetric string) ([]float64, error) {
	if pubkey == "" || token == "" {
		return nil, nil
	}
	fromDate := time.Now().UTC().Add(-time.Duration(lookbackDays) * 24 * time.Hour).Format("2006-01-02")
	payload := map[string]any{
		"query": `
        query GetNodeMetrics($from: String!, $metric: NodeMetricsKeys!, $pubkey: String!, $submetric: ChannelMetricsKeys) {
          getNodeMetrics(pubkey: $pubkey) {
            historical_series(from: $from, metric: $metric, submetric: $submetric)
          }
        }`,
		"variables": map[string]any{
			"from":      fromDate,
			"metric":    metric,
			"pubkey":    pubkey,
			"submetric": submetric,
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", "https://api.amboss.space/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("amboss status %d", resp.StatusCode)
	}
	var result ambossSeriesResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	rows := result.Data.GetNodeMetrics.HistoricalSeries
	vals := make([]float64, 0, len(rows))
	for _, row := range rows {
		if len(row) != 2 {
			continue
		}
		if v, ok := ambossValueToFloat(row[1]); ok {
			vals = append(vals, v)
		}
	}
	return vals, nil
}

func ambossValueToFloat(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		if err == nil {
			return f, true
		}
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}
func (e *autofeeEngine) applyDecision(ctx context.Context, ch lndclient.ChannelInfo, d *decision) error {
	if ch.ChannelPoint == "" {
		return errors.New("channel_point missing")
	}
	baseFee := int64(0)
	feeRate := int64(d.NewPpm)
	timeLock := int64(0)
	inboundEnabled := false
	inboundRate := int64(0)

	if ch.BaseFeeMsat != nil {
		baseFee = *ch.BaseFeeMsat
	}
	inboundEnabled, inboundRate = inboundFeeUpdateForDiscount(d.PrevInboundDiscount, d.InboundDiscount)

	if baseFee == 0 || timeLock == 0 {
		if policy, ok := e.fetchChannelPolicy(ctx, ch); ok {
			baseFee = policy.BaseFeeMsat
			timeLock = policy.TimeLockDelta
		}
	}
	if timeLock <= 0 {
		timeLock = 144
	}
	if d.SuperSourceActive && e.cfg.SuperSourceBaseFeeMsat > 0 {
		baseFee = int64(e.cfg.SuperSourceBaseFeeMsat)
	}

	return e.applyChannelFeesWithRetry(ctx, ch, baseFee, feeRate, timeLock, inboundEnabled, inboundRate)
}

// applyChannelFeesWithRetry wraps UpdateChannelFees with a small retry loop
// for transient errors (gRPC unavailable, deadline exceeded, transport reset).
// Permanent errors return immediately. Three attempts total (original + 2
// retries) with 200ms and 600ms backoffs. The retry only applies inside the
// caller's context — if the context is canceled we abort.
func (e *autofeeEngine) applyChannelFeesWithRetry(ctx context.Context, ch lndclient.ChannelInfo, baseFee, feeRate, timeLock int64, inboundEnabled bool, inboundRate int64) error {
	if e.svc == nil || e.svc.lnd == nil {
		return errors.New("lnd unavailable")
	}
	backoffs := []time.Duration{200 * time.Millisecond, 600 * time.Millisecond}
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		err := e.svc.lnd.UpdateChannelFees(ctx, ch.ChannelPoint, false, baseFee, feeRate, timeLock, inboundEnabled, 0, inboundRate)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientApplyError(err) {
			return err
		}
		if attempt >= len(backoffs) {
			break
		}
		if e.svc.logger != nil {
			e.svc.logger.Printf("autofee: transient apply error on %s (attempt %d/%d): %v", ch.ChannelPoint, attempt+1, len(backoffs)+1, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoffs[attempt]):
		}
	}
	return lastErr
}

// isTransientApplyError reports whether err looks like a transient transport
// or capacity issue worth retrying. We match on substrings so we don't have
// to import gRPC status here; the strings are stable across Go gRPC versions.
func isTransientApplyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	msg := strings.ToLower(err.Error())
	hints := []string{
		"unavailable",
		"deadline exceeded",
		"deadlineexceeded",
		"resource exhausted",
		"resourceexhausted",
		"transport is closing",
		"transport is broken",
		"connection reset",
		"connection refused",
		"i/o timeout",
		"no such host",
		"eof",
	}
	for _, hint := range hints {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

// fetchChannelPolicy returns the local-side policy for ch, using a per-Run
// cache so repeated lookups within the same Execute pass cost a single RPC.
// Prefers GetChannelPolicyByID (one GetChanInfo) over GetChannelPolicy (which
// re-runs ListChannels). On failure falls back to legacy method to preserve
// behavior on edge cases (e.g. graph entry not yet propagated).
func (e *autofeeEngine) fetchChannelPolicy(ctx context.Context, ch lndclient.ChannelInfo) (lndclient.ChannelPolicy, bool) {
	key := ch.ChannelPoint
	if key == "" {
		return lndclient.ChannelPolicy{}, false
	}
	if e.policyCache != nil {
		if cached, ok := e.policyCache[key]; ok {
			return cached, true
		}
	}
	if e.svc == nil || e.svc.lnd == nil {
		return lndclient.ChannelPolicy{}, false
	}
	if ch.ChannelID != 0 {
		if policy, err := e.svc.lnd.GetChannelPolicyByID(ctx, ch.ChannelID, ch.RemotePubkey, ch.ChannelPoint); err == nil {
			if e.policyCache != nil {
				e.policyCache[key] = policy
			}
			return policy, true
		}
	}
	policy, err := e.svc.lnd.GetChannelPolicy(ctx, ch.ChannelPoint)
	if err != nil {
		return lndclient.ChannelPolicy{}, false
	}
	if e.policyCache != nil {
		e.policyCache[key] = policy
	}
	return policy, true
}

// recordOutcome persists one row per kind ('outbound' or 'inbound') describing
// what changed in this decision plus the pre-decision channel snapshot. It is
// best-effort: any DB error is logged and swallowed so it can never break the
// run loop.
func (e *autofeeEngine) recordOutcome(ctx context.Context, runID string, d *decision, decidedAt time.Time) {
	if d == nil || e.svc == nil || e.svc.db == nil || runID == "" {
		return
	}
	tagsJSON, err := json.Marshal(d.Tags)
	if err != nil || len(tagsJSON) == 0 {
		tagsJSON = []byte("[]")
	}
	insertCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if d.NewPpm != d.LocalPpm {
		if _, err := e.svc.db.Exec(insertCtx, `
insert into autofee_outcomes (
  run_id, channel_id, channel_point, kind, decided_at,
  prev_ppm, new_ppm, target_ppm, floor_ppm, floor_src,
  class_label, tags,
  out_ratio_pre, out_ppm7d_pre, margin_ppm7d_pre, fwd_count_7d_pre
) values ($1,$2,$3,'outbound',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
on conflict (run_id, channel_id, kind) do nothing
`,
			runID, int64(d.ChannelID), d.ChannelPoint, decidedAt,
			d.LocalPpm, d.NewPpm, d.Target, d.Floor, d.FloorSrc,
			d.ClassLabel, tagsJSON,
			float32(d.OutRatio), d.OutPpm7d, d.Margin, d.FwdCount,
		); err != nil && e.svc.logger != nil {
			e.svc.logger.Printf("autofee: outcome insert (outbound) failed: %v", err)
		}
	}

	if d.InboundDiscount != d.PrevInboundDiscount {
		if _, err := e.svc.db.Exec(insertCtx, `
insert into autofee_outcomes (
  run_id, channel_id, channel_point, kind, decided_at,
  prev_ppm, new_ppm,
  class_label, tags,
  out_ratio_pre, out_ppm7d_pre, margin_ppm7d_pre, fwd_count_7d_pre
) values ($1,$2,$3,'inbound',$4,$5,$6,$7,$8,$9,$10,$11,$12)
on conflict (run_id, channel_id, kind) do nothing
`,
			runID, int64(d.ChannelID), d.ChannelPoint, decidedAt,
			d.PrevInboundDiscount, d.InboundDiscount,
			d.ClassLabel, tagsJSON,
			float32(d.OutRatio), d.OutPpm7d, d.Margin, d.FwdCount,
		); err != nil && e.svc.logger != nil {
			e.svc.logger.Printf("autofee: outcome insert (inbound) failed: %v", err)
		}
	}
}

func (e *autofeeEngine) logDecision(ctx context.Context, action string, d *decision) error {
	if d == nil {
		return nil
	}
	memo := fmt.Sprintf("autofee %s: %s %d->%d ppm | target=%d floor=%d | %s",
		action,
		d.ChannelPoint,
		d.LocalPpm,
		d.NewPpm,
		d.Target,
		d.Floor,
		strings.Join(d.Tags, " "),
	)
	evt := Notification{
		OccurredAt:   time.Now().UTC(),
		Type:         "autofee",
		Action:       action,
		Direction:    "neutral",
		Status:       "SETTLED",
		AmountSat:    0,
		FeeSat:       0,
		FeeMsat:      0,
		ChannelID:    int64(d.ChannelID),
		ChannelPoint: d.ChannelPoint,
		Memo:         memo,
	}
	eventKey := fmt.Sprintf("autofee:%d:%d", d.ChannelID, time.Now().UnixNano())
	if e.svc.notifier != nil {
		_, _ = e.svc.notifier.upsertNotification(ctx, eventKey, evt)
		return nil
	}
	_, err := e.svc.db.Exec(ctx, `
insert into notifications (event_key, occurred_at, type, action, direction, status, amount_sat, fee_sat, fee_msat, channel_id, channel_point, memo)
values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
on conflict (event_key) do nothing
`, eventKey, evt.OccurredAt, evt.Type, evt.Action, evt.Direction, evt.Status, evt.AmountSat, evt.FeeSat, evt.FeeMsat, evt.ChannelID, evt.ChannelPoint, evt.Memo)
	return err
}

func (e *autofeeEngine) logSummary(ctx context.Context, dryRun bool, reason, summary string) error {
	action := "summary"
	if dryRun {
		action = "summary-dry"
	}
	memo := fmt.Sprintf("autofee summary (%s): %s", reason, summary)
	evt := Notification{
		OccurredAt: time.Now().UTC(),
		Type:       "autofee",
		Action:     action,
		Direction:  "neutral",
		Status:     "SETTLED",
		Memo:       memo,
	}
	eventKey := fmt.Sprintf("autofee:summary:%d", time.Now().UnixNano())
	if e.svc.notifier != nil {
		_, _ = e.svc.notifier.upsertNotification(ctx, eventKey, evt)
		return nil
	}
	_, err := e.svc.db.Exec(ctx, `
insert into notifications (event_key, occurred_at, type, action, direction, status, amount_sat, fee_sat, fee_msat, memo)
values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
on conflict (event_key) do nothing
`, eventKey, evt.OccurredAt, evt.Type, evt.Action, evt.Direction, evt.Status, evt.AmountSat, evt.FeeSat, evt.FeeMsat, evt.Memo)
	return err
}

// ===== utils =====

func clampInt(v int, min int, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampFloat(v float64, min float64, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func applyStepCap(current int, target int, capFrac float64, minStep int, _ int) int {
	if current <= 0 {
		return target
	}
	cap := int(math.Max(float64(minStep), math.Abs(float64(current))*capFrac))
	delta := target - current
	if delta > cap {
		return current + cap
	}
	if delta < -cap {
		return current - cap
	}
	return target
}

func ppmMsat(feeMsat int64, amtMsat int64) int {
	if amtMsat <= 0 {
		return 0
	}
	return int(math.Round(float64(feeMsat) / float64(amtMsat) * 1_000_000))
}

func percentile(vals []float64, q float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	if len(vals) == 1 {
		return vals[0]
	}
	pos := q * float64(len(vals)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return vals[lo]
	}
	return vals[lo]*(float64(hi)-pos) + vals[hi]*(pos-float64(lo))
}

func averageFloat64(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	total := 0.0
	for _, v := range vals {
		total += v
	}
	return total / float64(len(vals))
}

func stddevFloat64(vals []float64, mean float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	total := 0.0
	for _, v := range vals {
		diff := v - mean
		total += diff * diff
	}
	return math.Sqrt(total / float64(len(vals)))
}

func classifyChannel(biasEma float64, outRatio float64, inCount int64, outCount int64, prevLabel string, prevConf float64) (string, float64) {
	label := "unknown"
	conf := 0.0
	if (inCount + outCount) >= 4 {
		if biasEma >= classificationSinkBiasMin && outRatio < 0.15 {
			label = "sink"
			conf = math.Min(1.0, (biasEma-classificationSinkBiasMin)/(1.0-classificationSinkBiasMin)+0.3)
		} else if biasEma <= classificationSourceBiasMax && outRatio > 0.55 {
			label = "source"
			conf = math.Min(1.0, ((-biasEma)-math.Abs(classificationSourceBiasMax))/(1.0-math.Abs(classificationSourceBiasMax))+0.3)
		} else if math.Abs(biasEma) <= classificationRouterBiasAbsMax && inCount > 0 && outCount > 0 {
			label = "router"
			conf = math.Min(1.0, (classificationRouterBiasAbsMax-math.Abs(biasEma))/classificationRouterBiasAbsMax+0.3)
		}
	}
	if label == "unknown" {
		return prevLabel, prevConf
	}
	if prevLabel == "" || prevLabel == "unknown" {
		return label, conf
	}
	if label != prevLabel {
		if conf >= prevConf+classificationLabelSwitchMinDelta {
			return label, conf
		}
		return prevLabel, prevConf
	}
	return label, math.Min(1.0, 0.5*prevConf+0.5*conf)
}

func computeInboundDiscount(enabled bool, classLabel string, outRatio float64, fwdCount int, marginPpm7d int, baseCostPpm int, appliedPpm int, maxRatio float64, reachOutRatio float64, retainedSpreadFrac float64) int {
	if !enabled || !strings.EqualFold(classLabel, "sink") || fwdCount < 5 || marginPpm7d < 200 || appliedPpm <= 0 {
		return 0
	}
	if reachOutRatio <= 0 {
		reachOutRatio = 0.10
	}
	if outRatio > reachOutRatio {
		return 0
	}
	if maxRatio <= 0 {
		maxRatio = defaultInboundDiscountMaxRatio
	}
	if retainedSpreadFrac <= 0 {
		retainedSpreadFrac = 0.12
	}
	return computeInboundDiscountBudget(baseCostPpm, appliedPpm, maxRatio, retainedSpreadFrac)
}

func computeBalancedInboundDiscount(enabled bool, classLabel string, outRatio float64, fwdCount int, marginPpm7d int, baseCostPpm int, appliedPpm int, maxRatio float64, reachOutRatio float64, retainedSpreadFrac float64) (int, string) {
	discount := computeInboundDiscount(enabled, classLabel, outRatio, fwdCount, marginPpm7d, baseCostPpm, appliedPpm, maxRatio, reachOutRatio, retainedSpreadFrac)
	if discount > 0 {
		return discount, "balanced"
	}
	if !enabled || !strings.EqualFold(classLabel, "sink") || fwdCount <= 0 || marginPpm7d <= 0 || appliedPpm <= 0 {
		return 0, ""
	}
	if reachOutRatio <= 0 {
		reachOutRatio = 0.10
	}
	if outRatio > reachOutRatio {
		return 0, ""
	}
	if maxRatio <= 0 {
		maxRatio = defaultInboundDiscountMaxRatio
	}
	if retainedSpreadFrac <= 0 {
		retainedSpreadFrac = 0.12
	}
	budget := computeInboundDiscountBudget(baseCostPpm, appliedPpm, maxRatio, retainedSpreadFrac)
	if budget <= 0 {
		return 0, ""
	}

	scale := 0.50
	if fwdCount >= 3 || marginPpm7d >= 100 {
		scale = 0.75
	}
	discount = int(math.Ceil(float64(budget) * scale))
	if discount < 25 {
		return 0, ""
	}
	return discount, "balanced-low-sample"
}

func computeInboundDiscountBudget(baseCostPpm int, appliedPpm int, maxRatio float64, retainedSpreadFrac float64) int {
	if appliedPpm <= 0 {
		return 0
	}
	if maxRatio <= 0 {
		maxRatio = defaultInboundDiscountMaxRatio
	}
	if retainedSpreadFrac <= 0 {
		retainedSpreadFrac = 0.12
	}
	anchor := int(math.Ceil(float64(baseCostPpm) * 1.002))
	maxDiscount := int(math.Ceil(float64(appliedPpm) * maxRatio))
	minRetainedSpread := int(math.Ceil(float64(appliedPpm) * retainedSpreadFrac))
	gap := appliedPpm - anchor
	if gap <= 0 {
		return 0
	}
	maxDiscountByRetainedSpread := appliedPpm - anchor - minRetainedSpread
	if maxDiscountByRetainedSpread <= 0 {
		return 0
	}
	return minInt(gap, minInt(maxDiscount, maxDiscountByRetainedSpread))
}

func adjustMarketRefillInboundTargetFracByPeerSkew(targetFrac float64, peerMarketSkew float64) (float64, []string) {
	if targetFrac <= 0 || peerMarketSkew < 3.0 {
		return targetFrac, nil
	}
	shrink := 1.0 + 0.20*(math.Log(peerMarketSkew)/math.Log(2))
	shrink = clampFloat(shrink, 1.0, 2.5)
	adjusted := targetFrac / shrink
	minTargetFrac := math.Max(0.03, targetFrac*0.35)
	if adjusted < minTargetFrac {
		adjusted = minTargetFrac
	}
	tags := []string{"market-refill-skew"}
	switch {
	case peerMarketSkew >= 20:
		tags = append(tags, "market-refill-skew-high")
	case peerMarketSkew >= 8:
		tags = append(tags, "market-refill-skew-med")
	default:
		tags = append(tags, "market-refill-skew-low")
	}
	return adjusted, tags
}

func computeMarketRefillInboundDiscount(enabled bool, effectiveOutRatio float64, recentForwards1d int, noFlow1d bool, weakRecentFlow bool, peerMarketSkew float64, baseCostPpm int, appliedPpm int, maxRatio float64, retainedSpreadFrac float64, profile autofeeProfile) (int, []string) {
	if !enabled || appliedPpm <= 0 {
		return 0, nil
	}
	if maxRatio <= 0 {
		maxRatio = defaultInboundDiscountMaxRatio
	}
	if retainedSpreadFrac <= 0 {
		retainedSpreadFrac = 0.12
	}
	targetFrac := profile.MarketRefillNetInboundTargetFrac
	if targetFrac <= 0 {
		targetFrac = 0.20
	}
	candidateReach := profile.MarketRefillCandidateOutRatioMax
	if candidateReach <= 0 {
		candidateReach = 0.15
	}
	exploratoryReach := profile.MarketRefillExploratoryOutRatioMax
	if exploratoryReach < candidateReach {
		exploratoryReach = candidateReach
	}
	exploratoryFwdsMax := profile.MarketRefillExploratoryFwds1dMax
	balancedTargetFrac := math.Min(0.24, math.Max(targetFrac+0.04, targetFrac*1.20))
	fullTargetFrac := math.Min(0.32, math.Max(targetFrac+0.08, targetFrac*1.40))
	fullishOutRatio := math.Max(exploratoryReach, 0.50)
	fullOutRatio := math.Max(fullishOutRatio+0.20, 0.70)
	tags := []string{"market-refill-inbound"}
	switch {
	case effectiveOutRatio <= candidateReach:
		// Keep the strongest refill pricing on the most drained channels.
	case effectiveOutRatio <= exploratoryReach && weakRecentFlow && recentForwards1d <= exploratoryFwdsMax:
		targetFrac = math.Max(0.03, targetFrac*0.70)
		tags = append(tags, "market-refill-explore")
	case effectiveOutRatio <= exploratoryReach && noFlow1d:
		targetFrac = math.Max(0.03, targetFrac*0.70)
		tags = append(tags, "market-refill-explore")
	case noFlow1d && recentForwards1d <= exploratoryFwdsMax:
		// In market-refill mode the node should test even full/dead channels
		// instead of leaving them untouched after disabling all rebalances.
		targetFrac = math.Min(balancedTargetFrac, math.Max(targetFrac+0.02, targetFrac*1.10))
		tags = append(tags, "market-refill-explore")
	case weakRecentFlow && recentForwards1d <= exploratoryFwdsMax:
		targetFrac = math.Min(balancedTargetFrac, math.Max(targetFrac+0.03, targetFrac*1.15))
	case effectiveOutRatio >= fullOutRatio:
		targetFrac = fullTargetFrac
	case effectiveOutRatio >= fullishOutRatio:
		blend := (effectiveOutRatio - fullishOutRatio) / math.Max(0.01, fullOutRatio-fullishOutRatio)
		targetFrac = balancedTargetFrac + (fullTargetFrac-balancedTargetFrac)*math.Max(0.0, math.Min(1.0, blend))
	default:
		midTop := math.Max(exploratoryReach+0.20, 0.50)
		blend := (effectiveOutRatio - exploratoryReach) / math.Max(0.01, midTop-exploratoryReach)
		targetFrac = targetFrac + (balancedTargetFrac-targetFrac)*math.Max(0.0, math.Min(1.0, blend))
	}
	if adjustedFrac, skewTags := adjustMarketRefillInboundTargetFracByPeerSkew(targetFrac, peerMarketSkew); len(skewTags) > 0 {
		targetFrac = adjustedFrac
		tags = append(tags, skewTags...)
	}
	anchor := int(math.Ceil(float64(baseCostPpm) * 1.002))
	maxDiscount := int(math.Ceil(float64(appliedPpm) * maxRatio))
	minRetainedSpread := int(math.Ceil(float64(appliedPpm) * retainedSpreadFrac))
	targetNetInbound := int(math.Ceil(float64(appliedPpm) * targetFrac))
	gapToTargetNet := appliedPpm - targetNetInbound
	if gapToTargetNet <= 0 {
		return 0, nil
	}
	maxDiscountByRetainedSpread := appliedPpm - anchor - minRetainedSpread
	if maxDiscountByRetainedSpread <= 0 {
		return 0, nil
	}
	return minInt(gapToTargetNet, minInt(maxDiscount, maxDiscountByRetainedSpread)), tags
}

func applyMarketRefillOutboundBias(profile autofeeProfile, localPpm int, targetPpm int, outPpm7d int, outRatio float64, recentForwards1d int, noFlow1d bool, weakRecentFlow bool, minPpm int, maxPpm int) (int, []string) {
	if outRatio < 0 || targetPpm <= 0 {
		return targetPpm, nil
	}
	candidateReach := profile.MarketRefillCandidateOutRatioMax
	if candidateReach <= 0 {
		candidateReach = 0.15
	}
	exploratoryReach := profile.MarketRefillExploratoryOutRatioMax
	if exploratoryReach < candidateReach {
		exploratoryReach = candidateReach
	}
	targetMult := profile.MarketRefillBaseTargetMult
	if targetMult <= 0 {
		targetMult = 1.50
	}
	targetAdd := profile.MarketRefillBaseTargetAddPpm
	if targetAdd <= 0 {
		targetAdd = 150
	}
	localHoldFrac := profile.MarketRefillLocalHoldFrac
	if localHoldFrac <= 0 || localHoldFrac > 1 {
		localHoldFrac = 0.35
	}
	localCapMult := profile.MarketRefillLocalCapMult
	if localCapMult <= 1 {
		localCapMult = 1.50
	}
	modeFloor := profile.MarketRefillMinOutboundPpm
	if profile.MarketRefillMinOutboundMaxPpmFrac > 0 && maxPpm > 0 {
		modeFloor = maxInt(modeFloor, int(math.Ceil(float64(maxPpm)*profile.MarketRefillMinOutboundMaxPpmFrac)))
	}
	// In market-refill, use a small bootstrap floor only. The main driver should be
	// the balanced target plus premium, not a hard node-wide anchor that collapses
	// all channels to the same target.
	softFloor := minPpm
	if modeFloor > 0 {
		softFloor = maxInt(softFloor, int(math.Ceil(float64(modeFloor)*0.25)))
	}
	outrateFloorFrac := profile.MarketRefillOutrateFloorFrac
	if outrateFloorFrac <= 0 {
		outrateFloorFrac = 1.0
	}
	floorFromOutrate := 0
	if outPpm7d > 0 {
		floorFromOutrate = int(math.Ceil(float64(outPpm7d) * outrateFloorFrac))
	}
	tags := []string{"market-refill-up"}
	switch {
	case outRatio <= candidateReach:
		targetMult = profile.MarketRefillDrainedTargetMult
		if targetMult <= 0 {
			targetMult = 3.50
		}
		targetAdd = profile.MarketRefillDrainedTargetAddPpm
		if targetAdd <= 0 {
			targetAdd = 1200
		}
		tags = append(tags, "market-refill-drained")
	case outRatio <= exploratoryReach:
		targetMult = profile.MarketRefillLowTargetMult
		if targetMult <= 0 {
			targetMult = 2.50
		}
		targetAdd = profile.MarketRefillLowTargetAddPpm
		if targetAdd <= 0 {
			targetAdd = 700
		}
		tags = append(tags, "market-refill-low")
	default:
		tags = append(tags, "market-refill-node")
	}
	if (noFlow1d || weakRecentFlow) && recentForwards1d <= profile.MarketRefillExploratoryFwds1dMax {
		tags = append(tags, "market-refill-explore")
	}
	localFloor := 0
	if localPpm > 0 {
		localFloor = int(math.Ceil(float64(localPpm) * localHoldFrac))
		if targetPpm > 0 {
			localFloorCap := int(math.Ceil(float64(targetPpm) * localCapMult))
			localFloor = minInt(localFloor, localFloorCap)
		}
	}
	effectiveBase := maxInt(targetPpm, localFloor)
	premiumTarget := maxInt(
		int(math.Ceil(float64(effectiveBase)*targetMult)),
		maxInt(effectiveBase+targetAdd, maxInt(softFloor, floorFromOutrate)),
	)
	if premiumTarget > maxPpm {
		premiumTarget = maxPpm
	}
	if premiumTarget > targetPpm {
		return premiumTarget, tags
	}
	return targetPpm, nil
}

func marketRefillStepCapFrac(profile autofeeProfile, outRatio float64) (float64, []string) {
	candidateReach := profile.MarketRefillCandidateOutRatioMax
	if candidateReach <= 0 {
		candidateReach = 0.15
	}
	exploratoryReach := profile.MarketRefillExploratoryOutRatioMax
	if exploratoryReach < candidateReach {
		exploratoryReach = candidateReach
	}
	baseCap := math.Max(profile.StepCap*2.0, 0.20)
	lowCap := math.Max(profile.StepCap*3.0, 0.30)
	drainedCap := math.Max(profile.StepCap*4.0, 0.40)
	switch {
	case outRatio <= candidateReach:
		return drainedCap, []string{"market-refill-stepcap", "market-refill-stepcap-drained"}
	case outRatio <= exploratoryReach:
		return lowCap, []string{"market-refill-stepcap", "market-refill-stepcap-low"}
	default:
		return baseCap, []string{"market-refill-stepcap"}
	}
}

func shouldBypassMarketRefillReversalGuard(tags []string, targetGapPct float64, nextPpm int, localPpm int) bool {
	if nextPpm <= localPpm || targetGapPct < 50 {
		return false
	}
	return containsTag(tags, "market-refill-up")
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func hasAutofeeSkipTag(tags []string) bool {
	return containsTag(tags, "cooldown") ||
		containsTag(tags, "small-delta") ||
		containsTag(tags, "hold-small") ||
		containsTag(tags, "autofee_settling")
}

func formatAutofeeTags(d *decision) string {
	if d == nil {
		return ""
	}
	tags := []string{}
	seen := map[string]struct{}{}
	add := func(tag string) {
		if tag == "" {
			return
		}
		if _, ok := seen[tag]; ok {
			return
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}

	switch strings.ToLower(strings.TrimSpace(d.ClassLabel)) {
	case "sink":
		add("🏷️sink")
	case "source":
		add("🏷️source")
	case "router":
		add("🏷️router")
	case "unknown":
		add("🏷️unknown")
	}

	for _, t := range d.Tags {
		if t == "" {
			continue
		}
		switch {
		case t == "discovery":
			add("🧭discovery")
		case t == "discovery-hard":
			add("🧨harddrop")
		case t == "explorer":
			add("🧭explorer")
		case t == "drained-explorer":
			add("🧭drained-explorer")
		case strings.HasPrefix(t, "drained-explorer-r"):
			add("🧭" + strings.TrimPrefix(t, "drained-explorer-"))
		case t == "drained-explorer-step":
			add("🧭step-up")
		case t == "drained-explorer-cap":
			add("🧭cap")
		case t == "idle-refresh":
			add("idle-refresh")
		case strings.HasPrefix(t, "idle-refresh:"):
			add(t)
		case t == "rescue":
			add("🛟rescue")
		case t == "rescue-enter":
			add("🛟enter")
		case t == "rescue-exit":
			add("🛟exit")
		case t == "rescue-expired":
			add("🛟expired")
		case strings.HasPrefix(t, "rescue-r"):
			add("🛟" + strings.TrimPrefix(t, "rescue-"))
		case t == "rescue-floor-relax":
			add("🛟floor-relax")
		case t == "rescue-global-relax":
			add("🛟global-relax")
		case t == "rescue-peg-paused":
			add("🛟peg-paused")
		case t == "market-refill-inbound":
			add("🌊market-refill")
		case t == "market-refill-up":
			add("🌊market-up")
		case t == "market-refill-node":
			add("🌊market-node")
		case t == "market-refill-low":
			add("🌊market-low")
		case t == "market-refill-drained":
			add("🌊market-drained")
		case t == "market-refill-stepcap":
			add("🌊market-cap")
		case t == "market-refill-stepcap-low":
			add("🌊market-cap-low")
		case t == "market-refill-stepcap-drained":
			add("🌊market-cap-drained")
		case t == "market-refill-reversal-bypass":
			add("🌊market-rev-bypass")
		case t == "market-refill-explore":
			add("🧪market-explore")
		case strings.HasPrefix(t, "surge"):
			add("📈" + t)
		case t == "top-rev":
			add("💎top-rev")
		case t == "neg-margin":
			add("⚠️neg-margin")
		case t == "rebal-recent":
			add("🔁rebal-recent")
		case t == "rebal-recent-floor":
			add("🔁rebal-floor")
		case t == "rebal-recent-hard-floor":
			add("🔁rebal-hard-floor")
		case t == "rebal-recent-settling-bypass":
			add("🔁rebal-settle-bypass")
		case t == "rebal-attempt":
			add("🔁rebal-attempt")
		case t == "rebal-recent-noup":
			add("🛑rebal-noup")
		case t == "rebal-fail-nodown":
			add("🛑rebal-fail")
		case t == "rebal-fail-pressure":
			add("🔁rebal-fail-pressure")
		case t == "assist-channel":
			add("🪄assist")
		case t == "assist-preserve":
			add("🪄assist-preserve")
		case t == "rank-expand":
			add("rank-expand")
		case t == "rank-maintain":
			add("rank-maintain")
		case t == "rank-monitor":
			add("rank-monitor")
		case t == "rank-close":
			add("rank-close")
		case t == "rank-up-restrict":
			add("rank-up-restrict")
		case t == "rank-floor-relax":
			add("rank-floor-relax")
		case t == "rank-monitor-loss-noup":
			add("rank-monitor-loss-noup")
		case t == "new-inbound":
			add("🆕NEW-inbound")
		case t == "bootstrap":
			add("🌱bootstrap")
		case t == "stagnation":
			add("🧪stagnation")
		case t == "stagnation-pressure":
			add("🧪stagnation-pressure")
		case t == "normalize-out":
			add("🎯norm-out")
		case t == "normalize-rebal":
			add("🎯norm-rebal")
		case t == "stagnation-floor":
			add("🧯stagnation-floor")
		case t == "stagnation-floor-relax":
			add("🧯stagnation-floor-relax")
		case t == "stagnation-neg-override":
			add("🧯stagnation-neg")
		case t == "htlc-policy-hot":
			add("🧾policy-hot")
		case t == "htlc-liquidity-hot":
			add("💧liq-hot")
		case t == "htlc-forward-hot":
			add("🧵forward-hot")
		case t == "htlc-sample-low":
			add("📉htlc-low-sample")
		case t == "htlc-noisy-sample":
			add("htlc-noisy")
		case t == "htlc-neutral-lock":
			add("🧯htlc-neutral")
		case strings.HasPrefix(t, "htlc-liq+"):
			add("💧" + t)
		case strings.HasPrefix(t, "htlc-policy+"):
			add("🧾" + t)
		case t == "htlc-liq-nodown":
			add("🛑liq-nodown")
		case t == "htlc-policy-nodown":
			add("🛑policy-nodown")
		case t == "htlc-neutral-nodown":
			add("🧯neutral-nodown")
		case t == "htlc-step-boost":
			add("⚡htlc-step")
		case t == "large-gap-step-boost":
			add("large-gap-step")
		case t == "large-gap-step-boost-strong":
			add("large-gap-step+")
		case t == "surge-noisy-followup-hold":
			add("surge-noisy-hold")
		case strings.HasPrefix(t, "negm+"):
			add("💹" + t)
		case t == "outrate-floor":
			add("📊outrate-floor")
		case t == "circuit-breaker":
			add("🧯cb")
		case t == "extreme-drain":
			add("⚡extreme")
		case t == "extreme-drain-unlock":
			add("⚡extreme-unlock")
		case t == "extreme-drain-turbo":
			add("🚀extreme-drain+")
		case t == "revfloor":
			add("🧱revfloor")
		case t == "peg":
			add("📌peg")
		case t == "peg-grace":
			add("📌peg-grace")
		case t == "peg-demand":
			add("📌peg-demand")
		case t == "peg-headroom-paused":
			add("📌peg-cap")
		case t == "peg-paused-stagnation":
			add("📌peg-paused")
		case t == "cooldown":
			add("⏳cooldown")
		case t == "cooldown-up-bypass":
			add("⏳up-bypass")
		case t == "cooldown-profit":
			add("⏳profit-hold")
		case t == "cooldown-skip":
			add("🧭skip-cooldown")
		case t == "hold-small":
			add("🧊hold-small")
		case t == "small-delta":
			add("🧊small-delta")
		case t == "churn-24h-lock":
			add("🧊churn-24h")
		case t == "churn-up-lock":
			add("🧊churn-up")
		case t == "autofee_settling":
			add("autofee-settling")
		case t == "same-ppm":
			add("🟰same-ppm")
		case t == "low-out-slow-up":
			add("🐢low-out-up")
		case t == "low-out-noflow-cap":
			add("🧯noflow-up-cap")
		case t == "empty-sink-hold":
			add("🧯empty-sink-hold")
		case t == "empty-sink-hard-hold":
			add("🧯empty-sink-hard")
		case t == "empty-sink-floor-hard-hold":
			add("🧱empty-sink-floor")
		case t == "seed-full-hold":
			add("🧭seed-full-hold")
		case t == "seed-full-floor-hold":
			add("🧱seed-full-floor")
		case t == "empty-sink-rebal-off":
			add("🧯empty-sink-rebal-off")
		case t == "empty-sink-down-anchor":
			add("🧯empty-sink-down")
		case t == "empty-sink-floor-hold":
			add("🧯empty-sink-floor-hold")
		case t == "empty-sink-floor-anchor":
			add("🧯empty-sink-floor")
		case t == "empty-sink-neg-relax":
			add("🧯empty-sink-neg")
		case t == "empty-sink-low-relax":
			add("🧯empty-sink-low")
		case t == "empty-sink-global-relax":
			add("🧯empty-sink-global")
		case t == "empty-sink-peg-paused":
			add("🧯empty-sink-peg")
		case t == "rebal-exec":
			add("rebal-exec")
		case t == "rebal-exec-disabled":
			add("rebal-disabled")
		case t == "rebal-exec-budget":
			add("rebal-budget")
		case t == "rebal-exec-roi":
			add("rebal-roi")
		case t == "rebal-exec-payback":
			add("rebal-payback")
		case t == "rebal-exec-noup":
			add("rebal-noup")
		case t == "rebal-exec-down-anchor":
			add("rebal-down")
		case t == "rebal-exec-floor-anchor":
			add("rebal-floor")
		case t == "rebal-exec-neg-relax":
			add("rebal-neg")
		case t == "rebal-exec-low-relax":
			add("rebal-low")
		case t == "rebal-exec-global-relax":
			add("rebal-global")
		case t == "rebal-exec-peg-paused":
			add("rebal-peg")
		case t == "out-fallback-21d":
			add("🕰️out-21d")
		case t == "rebal-fallback-21d":
			add("🕰️rebal-21d")
		case t == "slow-cycle-30d":
			add("🕰️slow-30d")
		case t == "slow-cycle-30d-out":
			add("🕰️slow-out")
		case t == "slow-cycle-30d-rebal":
			add("🕰️slow-rebal")
		case t == "no-signal-noup":
			add("🧯no-signal-noup")
		case t == "no-signal-floor-relax":
			add("🧯no-signal-floor")
		case t == "rebal-low-confidence":
			add("rebal-low-conf")
		case t == "rebal-floor-low-confidence":
			add("rebal-floor-low-conf")
		case t == "rebal-floor-low-volume":
			add("🧪rebal-low-volume")
		case t == "floor-up-blocked-low-signal":
			add("🛑floor-up-low-signal")
		case t == "balanced-floor-up-cap":
			add("⚖️balanced-up-cap")
		case t == "no-down-low":
			add("🚫down-low")
		case t == "no-down-neg-margin":
			add("🚫down-neg")
		case t == "neg-margin-floor-down":
			add("neg-floor-down")
		case t == "profit-protect-lock":
			add("🛡️profit-lock")
		case t == "profit-protect-relax":
			add("🕊️profit-relax")
		case t == "profit-protect-floor-down":
			add("profit-floor-down")
		case t == "super-source":
			add("🔥super-source")
		case t == "super-source-like":
			add("🔥super-source-like")
		case t == "sink-floor":
			add("🧱sink-floor")
		case t == "trend-up":
			add("📈trend-up")
		case t == "trend-down":
			add("📉trend-down")
		case t == "trend-flat":
			add("➡️trend-flat")
		case t == "stepcap":
			add("⛔stepcap")
		case t == "stepcap-lock":
			add("⛔stepcap-lock")
		case t == "floor-lock":
			add("🧱floor-lock")
		case t == "floor-relax-stall":
			add("🧯floor-relax")
		case t == "stall-relax-rebal-guard":
			add("🛡️stall-relax-rebal")
		case t == "reversal-guard":
			add("↩️reversal-guard")
		case t == "reversal-confirmed":
			add("↩️reversal-confirmed")
		case t == "reversal-fasttrack":
			add("↩️reversal-fasttrack")
		case t == "anti-flip-window":
			add("🪀anti-flip")
		case t == "downcap-general":
			add("📉downcap-general")
		case t == "htlc-low-sample-downcap":
			add("📉htlc-low-sample-downcap")
		case t == "stall-alert":
			add("🚨stall-alert")
		case t == "outnorm":
			add("🧮outnorm")
		case strings.HasPrefix(t, "outnorm:"):
			add("🧮" + strings.TrimPrefix(t, "outnorm:"))
		case t == "global-neg-lock":
			add("🛡️global-neg-lock")
		case t == "lock-skip-no-chan-rebal":
			add("🛡️lock-skip(no-chan-rebal)")
		case t == "lock-skip-sink-profit":
			add("🔓sink-global-neg")
		case strings.HasPrefix(t, "seed:amboss"):
			add("🌐" + strings.ReplaceAll(t, "seed:", "seed-"))
		case strings.HasPrefix(t, "seed:med"):
			add("📐seed-med")
		case strings.HasPrefix(t, "seed:vol"):
			add("📉" + strings.ReplaceAll(t, "seed:", "seed-"))
		case strings.HasPrefix(t, "seed:ratio"):
			add("🔁" + strings.ReplaceAll(t, "seed:", "seed-"))
		case strings.HasPrefix(t, "seed:outrate"):
			add("📊seed-outrate")
		case strings.HasPrefix(t, "seed:mem"):
			add("💾seed-mem")
		case strings.HasPrefix(t, "seed:default"):
			add("⚙️seed-default")
		case strings.HasPrefix(t, "seed:guard"):
			add("🛡️seed-guard")
		case strings.HasPrefix(t, "seed:shock"):
			add(strings.ReplaceAll(t, "seed:", "seed-"))
		case strings.HasPrefix(t, "seed:p95cap"):
			add("🧢seed-p95")
		case strings.HasPrefix(t, "seed:absmax"), strings.HasPrefix(t, "seed:maxppm"):
			add("🧱seed-cap")
		case strings.HasPrefix(t, "seed:soft-ceil"):
			add("🧭seed-soft-ceil")
		case strings.HasPrefix(t, "seed:soft-floor"):
			add("🧭seed-soft-floor")
		case strings.HasPrefix(t, "seed:soft-neg-relax"):
			add("🧭seed-soft-neg")
		case strings.HasPrefix(t, "seed:soft-global-relax"):
			add("🧭seed-soft-global")
		default:
			add(t)
		}
	}

	if d.InboundDiscount > 0 {
		add(fmt.Sprintf("↘️inb-%d", d.InboundDiscount))
	}

	return strings.Join(tags, " ")
}

func buildAutofeePrediction(outRatio float64, marginPpm7d int, target int, localPpm int, newPpm int, fwdCount int,
	negMarginGlobal bool, discoveryHit bool, cooldownRemainingHours float64) (string, int) {
	if target == localPpm && newPpm == localPpm {
		return "stable", 0
	}
	if discoveryHit && newPpm < localPpm {
		return "discovery_fast", 0
	}
	if target > localPpm && newPpm > localPpm {
		if cooldownRemainingHours > 0 {
			return "bias_up", int(math.Round(cooldownRemainingHours))
		}
		return "bias_up", 0
	}
	if target < localPpm && newPpm < localPpm {
		return "bias_down", 0
	}
	if outRatio < 0.05 && (marginPpm7d < 0 || negMarginGlobal) {
		return "hold_or_up", 0
	}
	if outRatio > 0.10 && marginPpm7d > 0 {
		return "reduce", 0
	}
	if fwdCount == 0 && outRatio > 0.60 {
		return "idle_reduce", 0
	}
	return "stable", 0
}

func nullableFloat(val float64) any {
	if val == 0 {
		return nil
	}
	return val
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
