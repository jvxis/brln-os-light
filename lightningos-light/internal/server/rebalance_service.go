package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
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
	// rebalanceConfigID is the singleton row in rebalance_config.
	rebalanceConfigID                 = 1
	rebalanceDefaultTargetOutboundPct = 50.0
	// rebalanceForwardPageSize bounds forwarding_history pagination so long
	// lived nodes can compute revenue/drain signals without unbounded memory.
	rebalanceForwardPageSize       = 50000
	rebalanceDefaultMppMinShardSat = int64(10000)
)

const (
	// Recent pair failure cache: short enough to avoid retry storms inside a
	// job/scan burst, but capped to 30 minutes — alinhado com o AR-WaitPeriod do
	// LNDG, que retesta pares falhados a cada 30 min e tem ~3-8 % de success rate
	// em redes em que ficamos travados muito mais tempo.
	pairFailTTL    = 30 * time.Second
	pairFailTTLMax = 30 * time.Minute
	// Winning routes are considered warm for one day; after that we prefer a
	// fresh path discovery over leaning on stale network state.
	pairSuccessTTL = 24 * time.Hour
)

const (
	// Permanent fail score decays slowly so repeated structural failures remain
	// visible across days without making a pair dead forever. Threshold elevado
	// pra 5.0 (era 3.0) reduz falsos cooldowns em mercado lento — exige sinal
	// mais consistente antes de descartar um par. TTL max 1h (era 6h) alinha
	// com o ritmo de revisão do AR-WaitPeriod do LNDG.
	permanentFailScoreHalfLife      = 7 * 24 * time.Hour
	permanentFailScoreMax           = 20.0
	permanentFailScoreSkipThreshold = 5.0
	// Each score step extends the skip TTL by an hour, capped at one hour.
	permanentFailScoreTTLStep = time.Hour
	permanentFailScoreTTLMax  = time.Hour
)

const (
	// Cooldown windows protect targets/sources from repeated no-path attempts.
	// The short recent window catches immediate retries; target-specific
	// variants use the max cooldown for stronger structural signals.
	rebalanceMaxCooldown                = time.Hour
	recentCooldownWindow                = 30 * time.Minute
	recentCooldownTTL                   = 30 * time.Minute
	targetNoAttemptCooldownWindow       = rebalanceMaxCooldown
	targetFailedCooldownWindow          = rebalanceMaxCooldown
	targetDistinctSourceCooldownWindow  = rebalanceMaxCooldown
	sourceCooldownMinAttempts           = 25
	sourceCooldownMaxSuccess            = 1
	targetCooldownMinAttempts           = 25
	targetCooldownMaxSuccess            = 0
	targetNoAttemptCooldownMinFailures  = 3
	targetNoAttemptCooldownMaxSuccesses = 0
	targetFailedCooldownMinFailures     = 5
	targetFailedCooldownMaxSuccesses    = 0
	targetDistinctSourceMinFailures     = 6
	targetDistinctSourceMaxSuccesses    = 0
)

const (
	// Queue entries linger briefly in the UI after completion so operators can
	// see the transition before the queue view compacts.
	queueLingerSeconds = 20
)

const (
	// scanSkipDetailLimit caps persisted/rendered diagnostic rows for the last
	// scan while preserving aggregate skip counters separately.
	scanSkipDetailLimit = 50
)

const (
	// autoTargetCooldownMin is the minimum spacing between automatic enqueue
	// attempts for the same target outside the stronger failure cooldowns.
	autoTargetCooldownMin = 10 * time.Minute
)

const (
	// mcResetCooldown debounces ResetMissionControl across concurrent jobs so a
	// burst of failures does not thrash MC. Tuned for the 15-min scan cycle.
	mcResetCooldown = 5 * time.Minute
)

var errMissionControlResetCooldown = errors.New("mission control reset cooldown")

const (
	// rebalancePaybackMinCostSat is the floor below which a channel is treated
	// as "fresh" for the SourceMinPaybackProgress filter — a tiny amount of
	// historical paid cost (e.g. one cheap probe) should not lock the channel
	// out of being a source. Channels with paid_cost <= this bypass the payback
	// gate. Tuned conservatively: a single succeeded rebalance routinely costs
	// far more than this.
	rebalancePaybackMinCostSat int64 = 500
)

const (
	// drainRateCacheTTL is the freshness window for the per-channel drain
	// rate (sats/hour forwarded out, computed from the last 24h of
	// forwarding_history). Pulling forwarding_history is expensive enough
	// that we don't want to do it on every scan/job, but stale enough is
	// also fine — a 10 min TTL strikes the balance for the 15 min scan loop.
	drainRateCacheTTL = 10 * time.Minute
)

const (
	// pairStatsStaleAfter is how long a pair_stats row that has only ever
	// failed (last_success_at IS NULL) is kept around before cleanup. Older
	// rows are deleted daily so the source/target sort no longer carries
	// graveyard entries that bias the cooldown skip filter against
	// historically dead pairs that may have become routable again.
	pairStatsStaleAfter = 60 * 24 * time.Hour
	// pairStatsCleanupInterval is how often the cleanup loop fires.
	pairStatsCleanupInterval = 24 * time.Hour
)

const (
	// MSPR structural abort only fires after enough independent sources fail,
	// and only when most shards/sources look structural. This preserves legacy
	// fallback when at least one shard succeeded, failures look transient, or a
	// few repeated shards against the same source would otherwise dominate.
	mppStructuralAbortMinAttempts        = 4
	mppStructuralAbortMinDistinctSources = 4
	mppStructuralAbortRatio              = 0.70
)

const (
	// Cooldown probes are small automatic jobs used to periodically test a
	// target that is otherwise blocked by recent-failure cooldown.
	targetCooldownProbeReason          = "cooldown-probe"
	targetCooldownProbeInterval        = 15 * time.Minute
	targetCooldownProbeBackoffInterval = rebalanceMaxCooldown
	// With the 1h backoff below, testing four ranked sources gives the probe
	// enough coverage to reopen real opportunities while still reducing total
	// churn versus the old 15m x 2-source loop.
	targetCooldownProbeMaxSources     = 4
	cooldownProbeWorkerSlots          = 1
	targetCooldownProbeBackoffReason  = "target_cooldown_probe_backoff"
	targetCooldownProbeDeferredReason = "target_cooldown_probe_deferred"
	targetCooldownProbeBusyReason     = "target_cooldown_probe_busy"
	cooldownProbeDeferredJobReason    = "cooldown probe deferred"
	cooldownProbeBusyJobReason        = "cooldown probe busy"
	cooldownProbeDisabledJobReason    = "cooldown probe disabled"
)

const (
	// Payback flags decide when previously protected liquidity can become
	// available as source capacity again.
	paybackModePayback  = 1 << 0
	paybackModeTime     = 1 << 1
	paybackModeCritical = 1 << 2

	// Fresh paid liquidity is kept out of source capacity briefly even when the
	// channel's aggregate payback is already healthy. This avoids immediately
	// recycling freshly purchased liquidity into another rebalance.
	freshPaidLiquidityLockDefaultHours = 6
)

var errManualRestartCooldown = errors.New("manual restart cooldown active")
var errManualBudgetExhausted = errors.New("manual budget exhausted")
var errManualBudgetInsufficient = errors.New("manual budget insufficient")

const (
	rebalanceBudgetModeRevenue24hPct = "revenue_24h_pct"
	rebalanceBudgetModeHybridRevenue = "hybrid_revenue"
)

const (
	rebalanceSchedulerModeRulesAuto       = "rules_auto"
	rebalanceSchedulerModeSovereignShadow = "sovereign_shadow"
	rebalanceSchedulerModeSovereignLive   = "sovereign_live"

	rebalanceSovereignScopeAutoOnly             = "auto_only"
	rebalanceSovereignScopeAutoAndManualRestart = "auto_and_manual_restart"

	rebalanceSovereignReason = "sovereign-autopilot"
)

const (
	sovereignLowSuccessMinAttempts             = 100
	sovereignLowSuccessRate                    = 0.01
	sovereignLowSuccessVeryWeakRate            = 0.005
	sovereignLowSuccessWeakRate                = 0.01
	sovereignLowSuccessVeryWeakProfitCostRatio = 3.0
	sovereignLowSuccessWeakProfitCostRatio     = 1.5
	sovereignLowSuccessProfitCostRatio         = 1.10
	// Lifetime empirically-dead guards are only a fallback when recent job
	// stats were not loaded. Sovereign scans prefer the 7d job window below
	// so old cumulative pair stats do not permanently ban a recovered target.
	sovereignLowSuccessDeadZeroAttempts                 = 200   // 0 successes in N+ attempts → never routed
	sovereignLowSuccessDeadRate                         = 0.001 // 0.1% — empirically dead at this scale
	sovereignLowSuccessDeadAttempts                     = 1000  // need this many attempts to call it dead
	sovereignRecentTargetStatsWindow                    = 7 * 24 * time.Hour
	sovereignRecentLowSuccessMinJobs                    = 5
	sovereignRecentEmpiricalDeadMinJobs                 = 3
	sovereignRecentEmpiricalDeadFirst                   = 2 * time.Hour
	sovereignRecentEmpiricalDeadRepeat                  = 6 * time.Hour
	sovereignLowSuccessOpportunityReason                = "low_success_opportunity_below_floor"
	sovereignBudgetEfficiencyProfitCostRatio            = 0.20
	sovereignBudgetEfficiencyHighSuccessRate            = 0.05
	sovereignBudgetEfficiencyOpportunityReason          = "budget_efficiency_below_floor"
	sovereignRiskScoreFloor                             = 0.03
	sovereignGainV3ColdStartPct                         = 0.75
	sovereignGainV3ColdStartPctMin                      = 0.50
	sovereignGainV3ColdStartPctMax                      = 0.95
	fastPathMaxTimeoutSecDefault                        = 90
	fastPathMaxTimeoutSecMin                            = 30
	fastPathMaxTimeoutSecMax                            = 300
	sovereignTopBucketPctDefault                        = 30
	sovereignTopBucketPctMin                            = 5
	sovereignTopBucketPctMax                            = 80
	sovereignRouteDeadSourceShare                       = 0.10
	sovereignRouteDeadMediumSourceShare                 = 0.35
	sovereignRouteDeadHighSourceShare                   = 0.50
	sovereignRouteDeadSevereSourceShare                 = 0.65
	sovereignRouteDeadProfitCostRatio                   = 1.5
	sovereignRouteDeadMediumProfitCostRatio             = 2.0
	sovereignRouteDeadHighProfitCostRatio               = 3.0
	sovereignRouteDeadOpportunityReason                 = "route_dead_opportunity_below_floor"
	sovereignRouteDeadFallbackMinFailedSources          = 8
	sovereignRouteDeadFallbackSevereFailSources         = targetCooldownMinAttempts
	sovereignTargetStructuralCooldownReason             = "target_structural_cooldown"
	sovereignTargetStructuralCooldownMinAttempts        = 20
	sovereignTargetStructuralCooldownLookback           = 24 * time.Hour
	sovereignTargetStructuralCooldownFirst              = 2 * time.Hour
	sovereignTargetStructuralCooldownRepeatDefaultHours = 6
	sovereignTargetStructuralCooldownRepeatMaxHours     = 48
	sovereignExplorationSlotPctMax                      = 50

	// R5 — Exploration burnout: when an exploration-marked job for a given
	// target accumulates `BurnoutMinAttempts` attempts in `BurnoutWindow`
	// without any success, the target is excluded from the exploration pool
	// for `BurnoutDuration`. Any successful exploration job for that target
	// clears the burnout state.
	sovereignExplorationBurnoutMinAttempts       = 5
	sovereignExplorationBurnoutWindow            = 24 * time.Hour
	sovereignExplorationBurnoutDuration          = 12 * time.Hour
	sovereignExplorationBurnoutReason            = "exploration_burnout"
	sovereignCostReliableHistoryPct              = int64(100)
	sovereignCostConservativeBudgetPct           = int64(100)
	sovereignUnsoldPaidLiquidityReason           = "paid_liquidity_unsold_cooldown"
	sovereignUnsoldPaidLiquidityPenaltyReason    = "paid_liquidity_unsold_penalty"
	sovereignUnsoldPaidLiquidityLookback         = 24 * time.Hour
	sovereignUnsoldPaidLiquidityMinAge           = 2 * time.Hour
	sovereignUnsoldPaidLiquidityHardAge          = 4 * time.Hour
	sovereignUnsoldPaidLiquidityMinFillPct       = int64(25)
	sovereignUnsoldPaidLiquidityMinForwardPct    = int64(10)
	sovereignUnsoldPaidLiquidityMinFeePaybackPct = int64(25)
	sovereignUnsoldPaidLiquiditySevereFillPct    = int64(75)
	sovereignUnsoldPaidLiquiditySevereForwardPct = int64(2)
	sovereignUnsoldPaidLiquiditySeverePaybackPct = int64(10)
	sovereignRealizedEconomicsConfidenceJobs     = 3
)

const (
	sovereignAttributionWindowDefaultHours      = 72
	sovereignSlowSellerWindowDefaultHours       = 168
	sovereignTargetSourceQuarantineDefaultHours = 6
	sovereignWindowMaxHours                     = 720
	sovereignSourceOpportunityTargetBufferPct   = 5.0
	sovereignTargetClassFastSeller              = "fast_seller"
	sovereignTargetClassSlowHighMargin          = "slow_high_margin"
	sovereignTargetClassColdOrDead              = "cold_or_dead"
	sovereignTargetClassExploration             = "exploration"
	fastPathPreferredMaxSources                 = 8
	fastPathPreferredMinSources                 = 4
	fastPathPreferredTimeoutSec                 = int32(20)
)

const (
	rebalanceManualReserveModeFixedSat = "fixed_sat"
	rebalanceManualReserveModePct      = "pct"
)

type RebalanceConfig struct {
	AutoEnabled                           bool    `json:"auto_enabled"`
	Profile                               string  `json:"profile"`
	SchedulerMode                         string  `json:"scheduler_mode"`
	SovereignCandidateScope               string  `json:"sovereign_candidate_scope"`
	SovereignMaxJobsPerCycle              int     `json:"sovereign_max_jobs_per_cycle"`
	SovereignMinExpectedProfitSat         int64   `json:"sovereign_min_expected_profit_sat"`
	SovereignLowSuccessMinRate            float64 `json:"sovereign_low_success_min_rate"`
	SovereignLowSuccessMinProfitCostRatio float64 `json:"sovereign_low_success_min_profit_cost_ratio"`
	SovereignBudgetEfficiencyMinRatio     float64 `json:"sovereign_budget_efficiency_min_ratio"`
	SovereignRouteDeadSourceShare         float64 `json:"sovereign_route_dead_source_share"`
	SovereignRiskScoreFloor               float64 `json:"sovereign_risk_score_floor"`
	// SovereignGainV3ColdStartPct (range 0.50–0.95, default 0.75) controla o
	// prior do estimateTargetGainV3 para targets cold-start (sem revenue7d e
	// sem drainRate). Valores maiores são mais permissivos (mais candidatos
	// passam o profit_guardrail) e valores menores são mais conservadores.
	// Aplicado em todos os call-sites do gain v3.
	SovereignGainV3ColdStartPct float64 `json:"sovereign_gain_v3_cold_start_pct"`
	// FastPathMaxTimeoutSec é o cap em segundos pro delegated fast-path.
	// Default 90. Range 30-300. Valores menores cortam pathfinding morto
	// rapidamente; valores maiores deixam o LND explorar MPP mais profundo.
	FastPathMaxTimeoutSec int `json:"fast_path_max_timeout_sec"`
	// SovereignTopBucketPct controla a largura do top-bucket de scoring que
	// recebe ordenação por fairness (LastAutoAt). Candidatos com score dentro
	// de (top × (1 - pct/100)) competem por rotação ao invés de ranking puro
	// por score. Default 30. Range 5-80. Valores maiores = mais diversidade
	// (mais candidatos rotacionam); menores = mais rigor por score puro.
	SovereignTopBucketPct                  int `json:"sovereign_top_bucket_pct"`
	SovereignAttributionWindowHours        int `json:"sovereign_attribution_window_hours"`
	SovereignSlowSellerWindowHours         int `json:"sovereign_slow_seller_window_hours"`
	SovereignTargetSourceQuarantineHours   int `json:"sovereign_target_source_quarantine_hours"`
	SovereignStructuralCooldownRepeatHours int `json:"sovereign_structural_cooldown_repeat_hours"`
	// SovereignExplorationSlotPct reserves a fraction of max_jobs_per_cycle
	// for randomly chosen low-score candidates ("epsilon-greedy"). Default 0
	// disables exploration. Valid range [0,50]. Implementation: after sorting
	// candidates by score, the top (max_jobs * (100-pct)/100) keep priority;
	// the remaining slots are filled by randomly sampling from the lower-score
	// pool. Lets historically-blocked channels respiratorily get attempts.
	SovereignExplorationSlotPct           int     `json:"sovereign_exploration_slot_pct"`
	SovereignSourceOpportunityCostEnabled bool    `json:"sovereign_source_opportunity_cost_enabled"`
	SovereignSlowSellerEnabled            bool    `json:"sovereign_slow_seller_enabled"`
	ScanIntervalSec                       int     `json:"scan_interval_sec"`
	DeadbandPct                           float64 `json:"deadband_pct"`
	SourceMinLocalPct                     float64 `json:"source_min_local_pct"`
	EconRatio                             float64 `json:"econ_ratio"`
	EconRatioMaxPpm                       int64   `json:"econ_ratio_max_ppm"`
	FeeLimitPpm                           int64   `json:"fee_limit_ppm"`
	LostProfit                            bool    `json:"lost_profit"`
	FailTolerancePpm                      int64   `json:"fail_tolerance_ppm"`
	ROIMin                                float64 `json:"roi_min"`
	DailyBudgetPct                        float64 `json:"daily_budget_pct"`
	BudgetMode                            string  `json:"budget_mode"`
	BudgetUnlimited                       bool    `json:"budget_unlimited"`
	BudgetAutoOnly                        bool    `json:"budget_auto_only"`
	ManualReserveEnabled                  bool    `json:"manual_reserve_enabled"`
	ManualReserveMode                     string  `json:"manual_reserve_mode"`
	ManualReserveValue                    float64 `json:"manual_reserve_value"`
	MaxConcurrent                         int     `json:"max_concurrent"`
	MinAmountSat                          int64   `json:"min_amount_sat"`
	MaxAmountSat                          int64   `json:"max_amount_sat"`
	MinSplitEnabled                       bool    `json:"min_split_enabled"`
	MinProbeSat                           int64   `json:"min_probe_sat"`
	MinExecuteSat                         int64   `json:"min_execute_sat"`
	MppEnabled                            bool    `json:"mpp_enabled"`
	MppMaxShards                          int     `json:"mpp_max_shards"`
	MppParallelism                        int     `json:"mpp_parallelism"`
	MppMinShardSat                        int64   `json:"mpp_min_shard_sat"`
	MppRoundTimeoutSec                    int     `json:"mpp_round_timeout_sec"`
	MppAutoOnly                           bool    `json:"mpp_auto_only"`
	FeeLadderSteps                        int     `json:"fee_ladder_steps"`
	AmountProbeSteps                      int     `json:"amount_probe_steps"`
	AmountProbeAdaptive                   bool    `json:"amount_probe_adaptive"`
	AttemptTimeoutSec                     int     `json:"attempt_timeout_sec"`
	RebalanceTimeoutSec                   int     `json:"rebalance_timeout_sec"`
	ManualRestartWatch                    bool    `json:"manual_restart_watch"`
	// ManualRestartIgnoreEconomicGates ativa o "modo convicção": canais
	// manual restart passam a ignorar o cost gate (filtro 1.4) e o ROI
	// guardrail, enchendo persistentemente independente de retorno econômico
	// imediato. Mantém fee cap (outgoing × econ_ratio), cooldown, budget e
	// limites operacionais. Default false (preserva o comportamento com
	// guardrails de quem já usa manual restart).
	ManualRestartIgnoreEconomicGates bool    `json:"manual_restart_ignore_economic_gates"`
	CooldownProbeEnabled             bool    `json:"cooldown_probe_enabled"`
	MissionControlHalfLifeSec        int64   `json:"mc_half_life_sec"`
	PaybackModeFlags                 int     `json:"payback_mode_flags"`
	FreshPaidLiquidityLockEnabled    bool    `json:"fresh_paid_liquidity_lock_enabled"`
	FreshPaidLiquidityLockHours      int     `json:"fresh_paid_liquidity_lock_hours"`
	UnlockDays                       int     `json:"unlock_days"`
	CriticalReleasePct               float64 `json:"critical_release_pct"`
	CriticalMinSources               int     `json:"critical_min_sources"`
	CriticalMinAvailableSats         int64   `json:"critical_min_available_sats"`
	CriticalCycles                   int     `json:"critical_cycles"`
	RebalanceCostFloorPpm            int64   `json:"rebalance_cost_floor_ppm"`
	SourceMinPaybackProgress         float64 `json:"source_min_payback_progress"`
	MissionControlReinforce          bool    `json:"mission_control_reinforce"`
	GainModelVersion                 int     `json:"gain_model_version"`
	VelocityWeight                   float64 `json:"velocity_weight"`
	// SovereignEVWeightedScoring switches the score formula from the legacy
	// (gain - cost) to EV-weighted (gain × P(success) − cost × P(failure)).
	// Default OFF: with cumulative pair stats it filters too aggressively
	// because long-term observed rates under-represent current routing
	// success. Enable once windowed pair stats land.
	SovereignEVWeightedScoring bool `json:"sovereign_ev_weighted_scoring"`
	// Wave 6.1b: when AutoFee adjusted a target's outgoing fee within this
	// window, dampen its rebalance score by AutofeeSettlingMultiplier (a value
	// in (0,1]). 0 disables the dampening.
	AutofeeSettlingWindowSec  int64   `json:"autofee_settling_window_sec"`
	AutofeeSettlingMultiplier float64 `json:"autofee_settling_multiplier"`
	// DelegatedFastPathEnabled habilita o caminho rápido inspirado no LNDG: antes
	// do loop por source, monta a lista completa de sources elegíveis e entrega
	// numa única chamada SendPaymentV2 com OutgoingChanIds=[all]+MPP. O LND nativo
	// faz pathfinding multi-source e MPP em paralelo. Em sucesso, persistimos
	// uma attempt agregada e finalizamos o job. Em falha, caímos no loop legado
	// (per-source com BuildRoute/QueryRoutes).
	DelegatedFastPathEnabled bool `json:"delegated_fast_path_enabled"`
	// DelegatedFastPathStrictPayback limits fast-path to sources that can cover
	// the full job amount inside MaxSourceSat, which already excludes payback-
	// protected liquidity. This gives the LND-native pathfinder a safe
	// per-source envelope even though SendPaymentV2 has no per-source cap.
	DelegatedFastPathStrictPayback bool `json:"delegated_fast_path_strict_payback"`
	// AutoTarget: loop opt-in que roda DENTRO do ciclo do autopilot
	// (evaluateAutoTarget, chamado por runAutoScan) e ajusta o
	// target_outbound_pct por canal. UP nos canais que estão vendendo bem e
	// rápido — sempre entre os candidatos selecionados da rodada, que são
	// supply-limited por construção (abaixo do target e drenando). DOWN nos
	// canais managed que pararam de vender (encheu-e-segurou). Consciente de
	// capacidade (cap absoluto de liquidez local por canal, pra canal gigante
	// não receber target desproporcional) e de budget (throttle de UPs por
	// ciclo). Default OFF; toda decisão (up/down/noop) vai pra
	// rebalance_auto_target_history.
	AutoTargetEnabled              bool    `json:"auto_target_enabled"`
	AutoTargetMaxPct               int     `json:"auto_target_max_pct"`
	AutoTargetMinPct               int     `json:"auto_target_min_pct"`
	AutoTargetStepPct              int     `json:"auto_target_step_pct"`
	AutoTargetEvalIntervalHours    int     `json:"auto_target_eval_interval_hours"`
	AutoTargetMaxUpsPerCycle       int     `json:"auto_target_max_ups_per_cycle"`
	AutoTargetMaxLocalSat          int64   `json:"auto_target_max_local_sat"`
	AutoTargetMinDrainRateSatPerHr int64   `json:"auto_target_min_drain_rate_sat_per_hr"`
	AutoTargetMinRevenue7dSat      int64   `json:"auto_target_min_revenue_7d_sat"`
	AutoTargetUpSuccessThreshold   float64 `json:"auto_target_up_success_threshold"`
	AutoTargetDownSuccessThreshold float64 `json:"auto_target_down_success_threshold"`
	AutoTargetDrainFirstMultiplier float64 `json:"auto_target_drain_first_multiplier"`
}

type RebalanceOverview struct {
	AutoEnabled                 bool                         `json:"auto_enabled"`
	Profile                     string                       `json:"profile"`
	SchedulerMode               string                       `json:"scheduler_mode"`
	SovereignLastDecisionAt     string                       `json:"sovereign_last_decision_at,omitempty"`
	SovereignLastMode           string                       `json:"sovereign_last_mode,omitempty"`
	SovereignCandidates         int                          `json:"sovereign_candidates"`
	SovereignSelected           int                          `json:"sovereign_selected"`
	SovereignExpectedProfitSat  int64                        `json:"sovereign_expected_profit_sat"`
	SovereignBudgetRemainingSat int64                        `json:"sovereign_budget_remaining_sat"`
	SovereignDecisions          []RebalanceSovereignDecision `json:"sovereign_decisions,omitempty"`
	SovereignHistory24h         []RebalanceSovereignHistory  `json:"sovereign_history_24h,omitempty"`
	LastScanAt                  string                       `json:"last_scan_at,omitempty"`
	LastScanStatus              string                       `json:"last_scan_status,omitempty"`
	LastScanDetail              string                       `json:"last_scan_detail,omitempty"`
	LastScanCandidates          int                          `json:"last_scan_candidates"`
	LastScanRemainingBudgetSat  int64                        `json:"last_scan_remaining_budget_sat"`
	LastScanReasons             map[string]int               `json:"last_scan_reasons,omitempty"`
	LastScanTopScoreSat         int64                        `json:"last_scan_top_score_sat"`
	LastScanProfitSkipped       int                          `json:"last_scan_profit_skipped"`
	LastScanQueued              int                          `json:"last_scan_queued"`
	LastScanSkipped             []RebalanceSkipDetail        `json:"last_scan_skipped,omitempty"`
	LastManualRestartAt         string                       `json:"last_manual_restart_at,omitempty"`
	LastManualRestartQueued     int                          `json:"last_manual_restart_queued"`
	LastManualRestartReasons    map[string]int               `json:"last_manual_restart_reasons,omitempty"`
	LastMCResetAt               string                       `json:"last_mc_reset_at,omitempty"`
	LastMCResetReason           string                       `json:"last_mc_reset_reason,omitempty"`
	MCResetCount                int64                        `json:"mc_reset_count"`
	MCResetCooldownSec          int64                        `json:"mc_reset_cooldown_sec"`
	MCResetCooldownRemainingSec int64                        `json:"mc_reset_cooldown_remaining_sec,omitempty"`
	DailyBudgetSat              int64                        `json:"daily_budget_sat"`
	DailyBudgetBaseSat          int64                        `json:"daily_budget_base_sat"`
	DailyBudgetShortTermSat     int64                        `json:"daily_budget_short_term_sat"`
	DailySpentSat               int64                        `json:"daily_spent_sat"`
	DailySpentAutoSat           int64                        `json:"daily_spent_auto_sat"`
	DailySpentManualSat         int64                        `json:"daily_spent_manual_sat"`
	RemainingTotalSat           int64                        `json:"remaining_total_sat"`
	RemainingForAutoSat         int64                        `json:"remaining_for_auto_sat"`
	BudgetUnlimited             bool                         `json:"budget_unlimited"`
	BudgetAutoOnly              bool                         `json:"budget_auto_only"`
	ManualReserveEnabled        bool                         `json:"manual_reserve_enabled"`
	ManualReserveMode           string                       `json:"manual_reserve_mode,omitempty"`
	ManualReserveValue          float64                      `json:"manual_reserve_value,omitempty"`
	ManualReserveSat            int64                        `json:"manual_reserve_sat"`
	ManualReserveRemainingSat   int64                        `json:"manual_reserve_remaining_sat"`
	LiveCostSat                 int64                        `json:"live_cost_sat"`
	Effectiveness7d             float64                      `json:"effectiveness_7d"`
	EffectivenessExecution7d    float64                      `json:"effectiveness_execution_7d"`
	// 30d trailing baselines for the toggleable health-signal gauges
	// (effectiveness, fast-path, payback). Cached server-side; may be 0 until
	// the first background recompute completes.
	Effectiveness30d                    float64                       `json:"effectiveness_30d"`
	FastPathHitRate30d                  float64                       `json:"fast_path_hit_rate_30d"`
	PaybackProgressRebalanced30d        float64                       `json:"payback_progress_rebalanced_30d"`
	HealthSignals30dReady               bool                          `json:"health_signals_30d_ready"`
	JobsWithoutAttempt7d                int64                         `json:"jobs_without_attempt_7d"`
	JobsWithoutAttemptRate7d            float64                       `json:"jobs_without_attempt_rate_7d"`
	ROI7d                               float64                       `json:"roi_7d"`
	SovereignRebalanceAmount7dSat       int64                         `json:"sovereign_rebalance_amount_7d_sat"`
	SovereignRebalanceCost7dSat         int64                         `json:"sovereign_rebalance_cost_7d_sat"`
	SovereignRebalanceCost7dPpm         int64                         `json:"sovereign_rebalance_cost_7d_ppm"`
	SovereignForwardAmount7dSat         int64                         `json:"sovereign_forward_amount_7d_sat"`
	SovereignForwardFee7dSat            int64                         `json:"sovereign_forward_fee_7d_sat"`
	SovereignForwardFee7dPpm            int64                         `json:"sovereign_forward_fee_7d_ppm"`
	SovereignRealizedNet7dSat           int64                         `json:"sovereign_realized_net_7d_sat"`
	SovereignSellThrough7d              float64                       `json:"sovereign_sellthrough_7d"`
	SovereignForwardAmountSlow7dSat     int64                         `json:"sovereign_forward_amount_slow_7d_sat"`
	SovereignForwardFeeSlow7dSat        int64                         `json:"sovereign_forward_fee_slow_7d_sat"`
	SovereignRealizedNetSlow7dSat       int64                         `json:"sovereign_realized_net_slow_7d_sat"`
	SovereignSellThroughSlow7d          float64                       `json:"sovereign_sellthrough_slow_7d"`
	SovereignSellThroughWindowHours     int                           `json:"sovereign_sellthrough_window_hours"`
	SovereignSellThroughSlowWindowHours int                           `json:"sovereign_sellthrough_slow_window_hours"`
	Jobs24h                             int64                         `json:"jobs_24h"`
	SuccessJobs24h                      int64                         `json:"success_jobs_24h"`
	JobSuccessRate24h                   float64                       `json:"job_success_rate_24h"`
	Attempts24h                         int64                         `json:"attempts_24h"`
	FailedAttempts24h                   int64                         `json:"failed_attempts_24h"`
	SuccessAttempts24h                  int64                         `json:"success_attempts_24h"`
	SuccessAmount24hSat                 int64                         `json:"success_amount_24h_sat"`
	SuccessAvgAmount24hSat              int64                         `json:"success_avg_amount_24h_sat"`
	AttemptSuccessRate24h               float64                       `json:"attempt_success_rate_24h"`
	AttemptsPerSuccessAttempt24h        float64                       `json:"attempts_per_success_attempt_24h"`
	SuccessSatsPerAttempt24h            float64                       `json:"success_sats_per_attempt_24h"`
	SuccessBelowMinAttempts24h          int64                         `json:"success_below_min_attempts_24h"`
	SuccessBelowMinAmount24hSat         int64                         `json:"success_below_min_amount_24h_sat"`
	SuccessBelowMinRate24h              float64                       `json:"success_below_min_rate_24h"`
	FastPathAttempts24h                 int64                         `json:"fast_path_attempts_24h"`
	FastPathSuccesses24h                int64                         `json:"fast_path_successes_24h"`
	FastPathHitRate24h                  float64                       `json:"fast_path_hit_rate_24h"`
	FastPathFailures24h                 int64                         `json:"fast_path_failures_24h"`
	FastPathFallthroughs24h             int64                         `json:"fast_path_fallthroughs_24h"`
	FastPathDurationP50Ms               int64                         `json:"fast_path_duration_p50_ms"`
	FastPathDurationP95Ms               int64                         `json:"fast_path_duration_p95_ms"`
	FastPathFailReasons24h              []RebalanceFastPathFailReason `json:"fast_path_fail_reasons_24h,omitempty"`
	PaybackRevenueSat                   int64                         `json:"payback_revenue_sat"`
	PaybackRevenueRebalancedSat         int64                         `json:"payback_revenue_rebalanced_sat"`
	PaybackCostSat                      int64                         `json:"payback_cost_sat"`
	PaybackProgress                     float64                       `json:"payback_progress"`
	PaybackProgressRebalanced           float64                       `json:"payback_progress_rebalanced"`
	EligibleSources                     int                           `json:"eligible_sources"`
	TargetsNeeding                      int                           `json:"targets_needing"`
	MppShadowJobs24h                    int64                         `json:"mpp_shadow_jobs_24h"`
	MppShadowPlanReady24h               int64                         `json:"mpp_shadow_plan_ready_24h"`
	MppShadowPlannedSat24h              int64                         `json:"mpp_shadow_planned_sat_24h"`
	MppShadowActualSentSat24h           int64                         `json:"mpp_shadow_actual_sent_sat_24h"`
	MppShadowInProgressJobs24h          int64                         `json:"mpp_shadow_in_progress_jobs_24h"`
	MppShadowSuccessJobs24h             int64                         `json:"mpp_shadow_success_jobs_24h"`
	MppShadowFailedJobs24h              int64                         `json:"mpp_shadow_failed_jobs_24h"`
	MppShadowPartialJobs24h             int64                         `json:"mpp_shadow_partial_jobs_24h"`
	MppShadowFloorBlocked24h            int64                         `json:"mpp_shadow_floor_blocked_sources_24h"`
	MppShadowAvgPlannedShards24h        float64                       `json:"mpp_shadow_avg_planned_shards_24h"`
	MppShadowAvgActualAttempts24h       float64                       `json:"mpp_shadow_avg_actual_attempts_24h"`
	MppStructuralAbortJobs24h           int64                         `json:"mpp_structural_abort_jobs_24h"`
	TopFailureReasons30m                []RebalanceReasonStat         `json:"top_failure_reasons_30m,omitempty"`
	RouteDeadTargets30m                 []RebalanceTargetStat         `json:"route_dead_targets_30m,omitempty"`
	NodeCalibration                     RebalanceNodeCalibration      `json:"node_calibration"`
}

// RebalanceNodeCalibration classifies the node by size and liquidity, mirroring
// AutoFee's calibrateNode (autofee_service.go) so both engines share one mental
// model. Computed over ACTIVE channels only, like AutoFee. Phase 1 of the
// autopilot-profiles work: this is the foundation that downstream pieces
// (auto-calibrated telemetry markers, node-scaled knobs) read from.
type RebalanceNodeCalibration struct {
	NodeClass          string  `json:"node_class"`      // small | medium | large | xl | unknown
	LiquidityClass     string  `json:"liquidity_class"` // drained | balanced | full
	ChannelCount       int     `json:"channel_count"`   // active channels (basis for classification)
	TotalChannelCount  int     `json:"total_channel_count"`
	TotalCapacitySat   int64   `json:"total_capacity_sat"`
	LocalCapacitySat   int64   `json:"local_capacity_sat"`
	InboundCapacitySat int64   `json:"inbound_capacity_sat"`
	AvgCapacitySat     int64   `json:"avg_capacity_sat"`
	LocalRatio         float64 `json:"local_ratio"`
}

// --- Rebalance profiles (Phase 2): posture × node calibration ---
//
// A profile is a posture (conservative/balanced/aggressive). applyRebalanceProfile
// composes that posture with the node calibration (RebalanceNodeCalibration) so
// the SAME profile yields different knobs on different nodes — mirroring how
// AutoFee composes its profile with calibrateNode. "custom" is frozen: it is never
// re-derived (the operator's manual values stand). balanced @ medium/balanced node
// equals defaultRebalanceConfig(), so a fresh node reads as "balanced".
const (
	rebalanceProfileCustom       = "custom"
	rebalanceProfileConservative = "conservative"
	rebalanceProfileBalanced     = "balanced"
	rebalanceProfileAggressive   = "aggressive"
)

type rebalanceProfileBase struct {
	// pure posture — node-independent economic risk appetite
	ROIMin               float64
	EconRatio            float64
	ColdStartPct         float64
	MinExpectedProfitSat int64
	// node-modulated bases — combined with calibration multipliers below
	MaxJobsBase            int
	DailyBudgetPctBase     float64
	ExplorationSlotPctBase int
}

var rebalanceProfileBases = map[string]rebalanceProfileBase{
	rebalanceProfileConservative: {ROIMin: 1.2, EconRatio: 0.6, ColdStartPct: 0.75, MinExpectedProfitSat: 20, MaxJobsBase: 3, DailyBudgetPctBase: 30, ExplorationSlotPctBase: 5},
	rebalanceProfileBalanced:     {ROIMin: 1.0, EconRatio: 0.7, ColdStartPct: 0.85, MinExpectedProfitSat: 10, MaxJobsBase: 4, DailyBudgetPctBase: 50, ExplorationSlotPctBase: 15},
	rebalanceProfileAggressive:   {ROIMin: 0.9, EconRatio: 0.8, ColdStartPct: 0.95, MinExpectedProfitSat: 5, MaxJobsBase: 6, DailyBudgetPctBase: 75, ExplorationSlotPctBase: 30},
}

// rebalanceAmountFloors are the amount-mechanics knobs per node class. They are
// profile-independent (a small node uses small amounts whatever the posture).
type rebalanceAmountFloors struct {
	MinAmountSat   int64
	MinExecuteSat  int64
	MinProbeSat    int64
	MppMinShardSat int64
}

var rebalanceAmountFloorsByClass = map[string]rebalanceAmountFloors{
	"small":  {MinAmountSat: 5000, MinExecuteSat: 1000, MinProbeSat: 1000, MppMinShardSat: 1000},
	"medium": {MinAmountSat: 50000, MinExecuteSat: 10000, MinProbeSat: 5000, MppMinShardSat: 10000},
	"large":  {MinAmountSat: 100000, MinExecuteSat: 25000, MinProbeSat: 10000, MppMinShardSat: 25000},
	"xl":     {MinAmountSat: 200000, MinExecuteSat: 50000, MinProbeSat: 20000, MppMinShardSat: 50000},
}

func rebalanceSizeMultiplier(nodeClass string) float64 {
	switch nodeClass {
	case "small":
		return 0.5
	case "large":
		return 1.5
	case "xl":
		return 2.0
	default:
		return 1.0
	}
}

// rebalanceExplorationSizeFactor: smaller nodes explore a little more (fewer
// routes, more value in probing), bigger nodes a little less.
func rebalanceExplorationSizeFactor(nodeClass string) float64 {
	switch nodeClass {
	case "small":
		return 1.3
	case "large":
		return 0.85
	case "xl":
		return 0.7
	default:
		return 1.0
	}
}

// rebalanceBudgetLiquidityFactor: a drained node needs to rebalance more, a full
// node less. Mirrors AutoFee's liquidity-class factor pattern.
func rebalanceBudgetLiquidityFactor(liquidityClass string) float64 {
	switch liquidityClass {
	case "drained":
		return 1.2
	case "full":
		return 0.8
	default:
		return 1.0
	}
}

func rebalanceAmountFloorsForClass(nodeClass string) rebalanceAmountFloors {
	if f, ok := rebalanceAmountFloorsByClass[nodeClass]; ok {
		return f
	}
	return rebalanceAmountFloorsByClass["medium"]
}

// applyRebalanceProfile returns cfg with the profile's posture composed with the
// node calibration. An unknown/custom profile returns cfg unchanged (custom is
// frozen — never re-derived). Only the managed knobs are touched.
func applyRebalanceProfile(cfg RebalanceConfig, profileName string, calib RebalanceNodeCalibration) RebalanceConfig {
	p, ok := rebalanceProfileBases[profileName]
	if !ok {
		return cfg
	}
	// pure posture
	cfg.ROIMin = p.ROIMin
	cfg.EconRatio = p.EconRatio
	cfg.SovereignGainV3ColdStartPct = p.ColdStartPct
	cfg.SovereignMinExpectedProfitSat = p.MinExpectedProfitSat
	// size-modulated concurrency (max_concurrent tracks max_jobs so it never caps)
	maxJobs := min(max(int(math.Round(float64(p.MaxJobsBase)*rebalanceSizeMultiplier(calib.NodeClass))), 2), 12)
	cfg.SovereignMaxJobsPerCycle = maxJobs
	cfg.MaxConcurrent = maxJobs
	// liquidity-modulated budget
	cfg.DailyBudgetPct = min(max(p.DailyBudgetPctBase*rebalanceBudgetLiquidityFactor(calib.LiquidityClass), 10.0), 100.0)
	// size-modulated exploration
	cfg.SovereignExplorationSlotPct = min(max(int(math.Round(float64(p.ExplorationSlotPctBase)*rebalanceExplorationSizeFactor(calib.NodeClass))), 0), 50)
	// amount mechanics by node class (profile-independent)
	floors := rebalanceAmountFloorsForClass(calib.NodeClass)
	cfg.MinAmountSat = floors.MinAmountSat
	cfg.MinExecuteSat = floors.MinExecuteSat
	cfg.MinProbeSat = floors.MinProbeSat
	cfg.MppMinShardSat = floors.MppMinShardSat
	return cfg
}

// rebalanceProfileManagedEqual reports whether two configs agree on every
// profile-managed knob (used to tell whether a config still matches a profile).
func rebalanceProfileManagedEqual(a, b RebalanceConfig) bool {
	return a.ROIMin == b.ROIMin &&
		a.EconRatio == b.EconRatio &&
		a.SovereignGainV3ColdStartPct == b.SovereignGainV3ColdStartPct &&
		a.SovereignMinExpectedProfitSat == b.SovereignMinExpectedProfitSat &&
		a.SovereignMaxJobsPerCycle == b.SovereignMaxJobsPerCycle &&
		a.MaxConcurrent == b.MaxConcurrent &&
		a.DailyBudgetPct == b.DailyBudgetPct &&
		a.SovereignExplorationSlotPct == b.SovereignExplorationSlotPct &&
		a.MinAmountSat == b.MinAmountSat &&
		a.MinExecuteSat == b.MinExecuteSat &&
		a.MinProbeSat == b.MinProbeSat &&
		a.MppMinShardSat == b.MppMinShardSat
}

// detectRebalanceProfile returns the named profile whose composed knobs match cfg
// at the given calibration, or "custom" when none match (operator deviated).
func detectRebalanceProfile(cfg RebalanceConfig, calib RebalanceNodeCalibration) string {
	for _, name := range []string{rebalanceProfileConservative, rebalanceProfileBalanced, rebalanceProfileAggressive} {
		if rebalanceProfileManagedEqual(cfg, applyRebalanceProfile(cfg, name, calib)) {
			return name
		}
	}
	return rebalanceProfileCustom
}

func normalizeRebalanceProfile(name string) string {
	switch name {
	case rebalanceProfileConservative, rebalanceProfileBalanced, rebalanceProfileAggressive, rebalanceProfileCustom:
		return name
	default:
		return rebalanceProfileCustom
	}
}

// effectiveRebalanceConfig returns the config the autopilot actually runs on: for
// a named profile, the profile composed with the live node calibration; for
// "custom" (or empty/unknown), the stored config unchanged — custom is frozen and
// never re-derived, so an operator's manual tuning is respected scan to scan.
func effectiveRebalanceConfig(cfg RebalanceConfig, calib RebalanceNodeCalibration) RebalanceConfig {
	if _, ok := rebalanceProfileBases[cfg.Profile]; !ok {
		return cfg
	}
	return applyRebalanceProfile(cfg, cfg.Profile, calib)
}

// reconcileRebalanceProfileState enforces the profile/autopilot state machine on
// a config about to be persisted: autopilot OFF ⇒ custom; a named profile whose
// managed knobs no longer match the profile-at-calibration (operator hand-edited
// a knob) ⇒ custom. A named profile with matching knobs is left intact.
func reconcileRebalanceProfileState(cfg RebalanceConfig, calib RebalanceNodeCalibration) RebalanceConfig {
	if !cfg.AutoEnabled {
		cfg.Profile = rebalanceProfileCustom
		return cfg
	}
	if _, named := rebalanceProfileBases[cfg.Profile]; named {
		if !rebalanceProfileManagedEqual(cfg, applyRebalanceProfile(cfg, cfg.Profile, calib)) {
			cfg.Profile = rebalanceProfileCustom
		}
	}
	return cfg
}

// nodeCalibration computes the live node calibration from the cached channel list.
// Returns an unknown/balanced default when LND is unavailable.
func (s *RebalanceService) nodeCalibration(ctx context.Context) RebalanceNodeCalibration {
	def := RebalanceNodeCalibration{NodeClass: "unknown", LiquidityClass: "balanced"}
	if s.lnd == nil {
		return def
	}
	channels, err := s.listChannelsCached(ctx)
	if err != nil {
		return def
	}
	return classifyRebalanceNode(channels)
}

// ApplyProfile is the selector action. A named profile turns the autopilot ON
// (sovereign_live) and applies that posture composed with the node calibration;
// "custom" freezes the current effective values so they stop re-deriving. The
// autopilot is only toggled on by a named profile — never off here.
func (s *RebalanceService) ApplyProfile(ctx context.Context, profileName string) (RebalanceConfig, error) {
	// Auto-snapshot the current config before switching, so the operator always
	// has a one-click undo even if they forgot to save manually.
	if _, err := s.saveConfigSnapshot(ctx, "auto"); err != nil && s.logger != nil {
		s.logger.Printf("rebalance auto-snapshot before profile apply failed: %v", err)
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		cfg = defaultRebalanceConfig()
	}
	calib := s.nodeCalibration(ctx)
	switch normalizeRebalanceProfile(profileName) {
	case rebalanceProfileCustom:
		cfg = effectiveRebalanceConfig(cfg, calib)
		cfg.Profile = rebalanceProfileCustom
	default:
		name := normalizeRebalanceProfile(profileName)
		cfg.AutoEnabled = true
		cfg.SchedulerMode = rebalanceSchedulerModeSovereignLive
		cfg = applyRebalanceProfile(cfg, name, calib)
		cfg.Profile = name
	}
	return s.UpdateConfig(ctx, cfg)
}

const rebalanceConfigSnapshotLimit = 20

// RebalanceConfigSnapshot is the list/metadata view of a saved config snapshot
// (the serialized config blob is loaded only on restore).
type RebalanceConfigSnapshot struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // manual | auto
	Profile   string `json:"profile"`
	CreatedAt string `json:"created_at"`
}

func rebalanceSnapshotName(kind, profile string, t time.Time) string {
	ts := t.In(time.Local).Format("2006-01-02 15:04:05")
	if kind == "auto" {
		p := profile
		if p == "" {
			p = "config"
		}
		return fmt.Sprintf("Auto · %s · %s", p, ts)
	}
	return fmt.Sprintf("Snapshot · %s", ts)
}

// saveConfigSnapshot serializes the current config under an auto-generated unique
// name. kind is "manual" or "auto" (auto = taken before a profile switch). Prunes
// to the most recent rebalanceConfigSnapshotLimit.
func (s *RebalanceService) saveConfigSnapshot(ctx context.Context, kind string) (RebalanceConfigSnapshot, error) {
	if s.db == nil {
		return RebalanceConfigSnapshot{}, errors.New("db unavailable")
	}
	if kind != "auto" {
		kind = "manual"
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		cfg = defaultRebalanceConfig()
	}
	blob, err := json.Marshal(cfg)
	if err != nil {
		return RebalanceConfigSnapshot{}, err
	}
	name := rebalanceSnapshotName(kind, cfg.Profile, time.Now())
	var snap RebalanceConfigSnapshot
	var created time.Time
	if err := s.db.QueryRow(ctx, `
insert into rebalance_config_snapshots (name, kind, profile, config_json)
values ($1,$2,$3,$4)
returning id, name, kind, profile, created_at`, name, kind, cfg.Profile, string(blob)).Scan(&snap.ID, &snap.Name, &snap.Kind, &snap.Profile, &created); err != nil {
		return RebalanceConfigSnapshot{}, err
	}
	snap.CreatedAt = created.UTC().Format(time.RFC3339)
	s.pruneConfigSnapshots(ctx)
	return snap, nil
}

func (s *RebalanceService) pruneConfigSnapshots(ctx context.Context) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(ctx, `
delete from rebalance_config_snapshots
where id not in (
  select id from rebalance_config_snapshots order by created_at desc limit $1
)`, rebalanceConfigSnapshotLimit)
}

func (s *RebalanceService) listConfigSnapshots(ctx context.Context) ([]RebalanceConfigSnapshot, error) {
	out := []RebalanceConfigSnapshot{}
	if s.db == nil {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `select id, name, kind, profile, created_at from rebalance_config_snapshots order by created_at desc`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var snap RebalanceConfigSnapshot
		var created time.Time
		if err := rows.Scan(&snap.ID, &snap.Name, &snap.Kind, &snap.Profile, &created); err != nil {
			return out, err
		}
		snap.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, snap)
	}
	return out, rows.Err()
}

// restoreConfigSnapshot deserializes a snapshot and applies it via UpdateConfig,
// which re-runs normalization and the profile/autopilot state reconciliation.
func (s *RebalanceService) restoreConfigSnapshot(ctx context.Context, id int64) (RebalanceConfig, error) {
	if s.db == nil {
		return RebalanceConfig{}, errors.New("db unavailable")
	}
	var blob string
	if err := s.db.QueryRow(ctx, `select config_json from rebalance_config_snapshots where id=$1`, id).Scan(&blob); err != nil {
		return RebalanceConfig{}, err
	}
	var cfg RebalanceConfig
	if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
		return RebalanceConfig{}, err
	}
	return s.UpdateConfig(ctx, cfg)
}

func (s *RebalanceService) deleteConfigSnapshot(ctx context.Context, id int64) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	_, err := s.db.Exec(ctx, `delete from rebalance_config_snapshots where id=$1`, id)
	return err
}

type RebalanceMissionControlState struct {
	LastMCResetAt               string `json:"last_mc_reset_at,omitempty"`
	LastMCResetReason           string `json:"last_mc_reset_reason,omitempty"`
	MCResetCount                int64  `json:"mc_reset_count"`
	MCResetCooldownSec          int64  `json:"mc_reset_cooldown_sec"`
	MCResetCooldownRemainingSec int64  `json:"mc_reset_cooldown_remaining_sec,omitempty"`
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

type RebalanceSovereignDecision struct {
	ChannelID                uint64  `json:"channel_id"`
	ChannelPoint             string  `json:"channel_point"`
	PeerAlias                string  `json:"peer_alias"`
	Selected                 bool    `json:"selected"`
	Reason                   string  `json:"reason"`
	Score                    int64   `json:"score"`
	AmountSat                int64   `json:"amount_sat"`
	ExpectedGainSat          int64   `json:"expected_gain_sat"`
	EstimatedCostSat         int64   `json:"estimated_cost_sat"`
	ExpectedProfitSat        int64   `json:"expected_profit_sat"`
	ExpectedROI              float64 `json:"expected_roi"`
	ExpectedROIValid         bool    `json:"expected_roi_valid"`
	BudgetCostSat            int64   `json:"budget_cost_sat"`
	HistoricalAttempts       int     `json:"historical_attempts"`
	HistoricalSuccesses      int     `json:"historical_successes"`
	HistoricalSuccessRate    float64 `json:"historical_success_rate"`
	RecentStructuralFailures int     `json:"recent_structural_failures"`
	RecentRebalanceSentSat   int64   `json:"recent_rebalance_sent_sat,omitempty"`
	RecentRebalanceTargetSat int64   `json:"recent_rebalance_target_sat,omitempty"`
	TargetClass              string  `json:"target_class,omitempty"`
	AttributionWindowHours   int     `json:"attribution_window_hours,omitempty"`
	SlowSellerWindowHours    int     `json:"slow_seller_window_hours,omitempty"`
	RecentForward24hSat      int64   `json:"recent_forward_24h_sat,omitempty"`
	RecentForwardFee24hSat   int64   `json:"recent_forward_fee_24h_sat,omitempty"`
	RecentRealizedNet24hSat  int64   `json:"recent_realized_net_24h_sat,omitempty"`
	RecentForwardedAfterSat  int64   `json:"recent_forwarded_after_sat,omitempty"`
	RecentForwardFeeAfterSat int64   `json:"recent_forward_fee_after_sat,omitempty"`
	RecentForwardSlowSat     int64   `json:"recent_forward_slow_sat,omitempty"`
	RecentForwardFeeSlowSat  int64   `json:"recent_forward_fee_slow_sat,omitempty"`
	RecentRealizedNetSlowSat int64   `json:"recent_realized_net_slow_sat,omitempty"`
	RecentRealizedSentSat    int64   `json:"recent_realized_sent_sat,omitempty"`
	RecentRealizedForwardSat int64   `json:"recent_realized_forward_sat,omitempty"`
	RecentRealizedFeeSat     int64   `json:"recent_realized_fee_sat,omitempty"`
	RecentRealizedNetSat     int64   `json:"recent_realized_net_sat,omitempty"`
	// Score multiplier breakdown — surfaced for UI debugging so operators can
	// reason about why a given target won/lost the ranking without re-running
	// the scorer mentally.
	SuccessMultiplier           float64 `json:"success_multiplier,omitempty"`
	ROIMultiplier               float64 `json:"roi_multiplier,omitempty"`
	BudgetEfficiencyMultiplier  float64 `json:"budget_efficiency_multiplier,omitempty"`
	UnsoldLiquidityMultiplier   float64 `json:"unsold_liquidity_multiplier,omitempty"`
	RealizedEconomicsMultiplier float64 `json:"realized_economics_multiplier,omitempty"`
	ExplorationSlot             bool    `json:"exploration_slot,omitempty"`
}

type RebalanceSovereignHistory struct {
	ID                 int64                        `json:"id,omitempty"`
	ScanAt             string                       `json:"scan_at"`
	Mode               string                       `json:"mode"`
	Status             string                       `json:"status"`
	Candidates         int                          `json:"candidates"`
	Selected           int                          `json:"selected"`
	ExpectedProfitSat  int64                        `json:"expected_profit_sat"`
	BudgetRemainingSat int64                        `json:"budget_remaining_sat"`
	SkipReasons        map[string]int               `json:"skip_reasons,omitempty"`
	SelectedDecisions  []RebalanceSovereignDecision `json:"selected_decisions,omitempty"`
	Decisions          []RebalanceSovereignDecision `json:"decisions,omitempty"`
	Detail             string                       `json:"detail,omitempty"`
}

type RebalanceReasonStat struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// RebalanceFastPathFailReason agrupa motivos de falha do delegated fast-path
// numa categoria normalizada (timeout, no_route, insufficient_balance, etc.)
// e o respectivo count na janela de 24h.
type RebalanceFastPathFailReason struct {
	Category string `json:"category"`
	Count    int64  `json:"count"`
}

type RebalanceTargetStat struct {
	ChannelID       uint64 `json:"channel_id"`
	PeerAlias       string `json:"peer_alias,omitempty"`
	FailedSources   int64  `json:"failed_sources"`
	FailureAttempts int64  `json:"failure_attempts"`
	LastFailureAt   string `json:"last_failure_at,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type RebalanceChannel struct {
	ChannelID              uint64   `json:"channel_id"`
	ChannelPoint           string   `json:"channel_point"`
	PeerAlias              string   `json:"peer_alias"`
	RemotePubkey           string   `json:"remote_pubkey"`
	Active                 bool     `json:"active"`
	Private                bool     `json:"private"`
	CapacitySat            int64    `json:"capacity_sat"`
	LocalBalanceSat        int64    `json:"local_balance_sat"`
	RemoteBalanceSat       int64    `json:"remote_balance_sat"`
	LocalPct               float64  `json:"local_pct"`
	RemotePct              float64  `json:"remote_pct"`
	OutgoingFeePpm         int64    `json:"outgoing_fee_ppm"`
	OutgoingBaseMsat       int64    `json:"outgoing_base_msat"`
	PeerFeeRatePpm         int64    `json:"peer_fee_rate_ppm"`
	PeerBaseMsat           int64    `json:"peer_base_msat"`
	SpreadPpm              int64    `json:"spread_ppm"`
	TargetOutboundPct      float64  `json:"target_outbound_pct"`
	TargetAmountSat        int64    `json:"target_amount_sat"`
	AutoEnabled            bool     `json:"auto_enabled"`
	ManualRestartEnabled   bool     `json:"manual_restart_enabled"`
	AutoTargetManaged      bool     `json:"auto_target_managed"`
	AutomationMode         string   `json:"automation_mode,omitempty"`
	FixedFeePPM            *int64   `json:"fixed_fee_ppm,omitempty"`
	ReviewAt               string   `json:"review_at,omitempty"`
	AutomationNote         string   `json:"automation_note,omitempty"`
	ParkedAt               string   `json:"parked_at,omitempty"`
	UseDefaultEconRatio    bool     `json:"use_default_econ_ratio"`
	EconRatioOverride      *float64 `json:"econ_ratio_override,omitempty"`
	AutoBypassCostGate     bool     `json:"auto_bypass_cost_gate"`
	EligibleAsTarget       bool     `json:"eligible_as_target"`
	EligibleAsManualTarget bool     `json:"eligible_as_manual_target"`
	EligibleAsSource       bool     `json:"eligible_as_source"`
	ProtectedLiquiditySat  int64    `json:"protected_liquidity_sat"`
	// EffectiveProtectedSat é a porção de paid_liquidity_sat que está
	// efetivamente travada agora pela Política C v2 (linear em payback_progress).
	// Diferente de ProtectedLiquiditySat (lifetime aportado), esse valor reflete
	// o lock real do momento — é o que a UI deveria mostrar como "Protegido".
	EffectiveProtectedSat int64   `json:"effective_protected_sat"`
	PaybackProgress       float64 `json:"payback_progress"`
	TimeToPaybackHours    float64 `json:"time_to_payback_hours"`
	TimeToPaybackValid    bool    `json:"time_to_payback_valid"`
	MaxSourceSat          int64   `json:"max_source_sat"`
	Revenue7dSat          int64   `json:"revenue_7d_sat"`
	DrainRateSatPerHour   int64   `json:"drain_rate_sat_per_hour"`
	PendingOutgoingHtlcs  int     `json:"pending_outgoing_htlcs"`
	RebalanceCost7dSat    int64   `json:"rebalance_cost_7d_sat"`
	RebalanceCost7dPpm    int64   `json:"rebalance_cost_7d_ppm"`
	RebalanceAmount7dSat  int64   `json:"rebalance_amount_7d_sat"`
	ROIEstimate           float64 `json:"roi_estimate"`
	ROIEstimateValid      bool    `json:"roi_estimate_valid"`
	ExcludedAsSource      bool    `json:"excluded_as_source"`
	SourceQuarantined     bool    `json:"source_quarantined,omitempty"`
	SourceQuarantineUntil string  `json:"source_quarantine_until,omitempty"`
	SourceOpportunityCost int64   `json:"source_opportunity_cost_sat,omitempty"`
}

type RebalancePairStat struct {
	SourceChannelID      uint64   `json:"source_channel_id"`
	SourceChannelPoint   string   `json:"source_channel_point,omitempty"`
	SourcePeerAlias      string   `json:"source_peer_alias,omitempty"`
	TargetChannelID      uint64   `json:"target_channel_id"`
	TargetChannelPoint   string   `json:"target_channel_point,omitempty"`
	TargetPeerAlias      string   `json:"target_peer_alias,omitempty"`
	LastSuccessAt        string   `json:"last_success_at,omitempty"`
	LastFailAt           string   `json:"last_fail_at,omitempty"`
	LastFailReason       string   `json:"last_fail_reason,omitempty"`
	SuccessCount         int      `json:"success_count"`
	FailCount            int      `json:"fail_count"`
	PermanentFailScore   float64  `json:"permanent_fail_score"`
	SuccessAmountSat     int64    `json:"success_amount_sat"`
	SuccessFeePpm        int64    `json:"success_fee_ppm"`
	LastSuccessRouteHops []string `json:"last_success_route_hops,omitempty"`
}

type RebalanceJob struct {
	ID                            int64   `json:"id"`
	CreatedAt                     string  `json:"created_at"`
	CompletedAt                   string  `json:"completed_at,omitempty"`
	Source                        string  `json:"source"`
	Status                        string  `json:"status"`
	TriggerReason                 string  `json:"trigger_reason,omitempty"`
	Reason                        string  `json:"reason,omitempty"`
	TargetChannelID               uint64  `json:"target_channel_id"`
	TargetChannelPoint            string  `json:"target_channel_point"`
	TargetOutboundPct             float64 `json:"target_outbound_pct"`
	TargetAmountSat               int64   `json:"target_amount_sat"`
	TargetPeerAlias               string  `json:"target_peer_alias,omitempty"`
	SovereignExpectedGainSat      int64   `json:"sovereign_expected_gain_sat,omitempty"`
	SovereignEstimatedCostSat     int64   `json:"sovereign_estimated_cost_sat,omitempty"`
	SovereignExpectedProfitSat    int64   `json:"sovereign_expected_profit_sat,omitempty"`
	SovereignBudgetCostSat        int64   `json:"sovereign_budget_cost_sat,omitempty"`
	SovereignScore                int64   `json:"sovereign_score,omitempty"`
	ActualSentSat                 int64   `json:"actual_sent_sat,omitempty"`
	ActualRebalanceFeeSat         int64   `json:"actual_rebalance_fee_sat,omitempty"`
	Forward1hCount                int64   `json:"forward_1h_count,omitempty"`
	Forward1hAmountSat            int64   `json:"forward_1h_amount_sat,omitempty"`
	Forward1hFeeSat               int64   `json:"forward_1h_fee_sat,omitempty"`
	AttributedForward1hAmountSat  int64   `json:"attributed_forward_1h_amount_sat,omitempty"`
	AttributedForward1hFeeSat     int64   `json:"attributed_forward_1h_fee_sat,omitempty"`
	RealizedNet1hSat              int64   `json:"realized_net_1h_sat,omitempty"`
	Forward6hCount                int64   `json:"forward_6h_count,omitempty"`
	Forward6hAmountSat            int64   `json:"forward_6h_amount_sat,omitempty"`
	Forward6hFeeSat               int64   `json:"forward_6h_fee_sat,omitempty"`
	AttributedForward6hAmountSat  int64   `json:"attributed_forward_6h_amount_sat,omitempty"`
	AttributedForward6hFeeSat     int64   `json:"attributed_forward_6h_fee_sat,omitempty"`
	RealizedNet6hSat              int64   `json:"realized_net_6h_sat,omitempty"`
	Forward24hCount               int64   `json:"forward_24h_count,omitempty"`
	Forward24hAmountSat           int64   `json:"forward_24h_amount_sat,omitempty"`
	Forward24hFeeSat              int64   `json:"forward_24h_fee_sat,omitempty"`
	AttributedForward24hAmountSat int64   `json:"attributed_forward_24h_amount_sat,omitempty"`
	AttributedForward24hFeeSat    int64   `json:"attributed_forward_24h_fee_sat,omitempty"`
	RealizedNet24hSat             int64   `json:"realized_net_24h_sat,omitempty"`
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
	AutomationMode       string
	FixedFeePPM          *int64
	ReviewAt             string
	AutomationNote       string
	ParkedAt             string
	UseDefaultEconRatio  bool
	EconRatioOverride    float64
	EconRatioOverrideSet bool
	AutoBypassCostGate   bool
	// AutoTargetManaged: quando true (default), o AutoTarget pode ajustar o
	// target_outbound_pct deste canal. Opt-out per-channel. A coluna
	// auto_target_managed tem default true no schema; upserts das outras
	// automações não a tocam.
	AutoTargetManaged bool
}

type channelLedger struct {
	ChannelID             uint64
	PaidLiquiditySat      int64
	FreshPaidLiquiditySat int64
	PaidCostSat           int64
	PaidRevenueSat        int64
	LastRebalanceAt       time.Time
	LastForwardAt         time.Time
	LastUnlockAt          time.Time
}

type freshPaidLiquidityLot struct {
	AmountSat  int64
	OccurredAt time.Time
}

type freshPaidLiquidityTracker struct {
	ttl  time.Duration
	lots map[uint64][]freshPaidLiquidityLot
}

type pairStat struct {
	SourceChannelID      uint64
	TargetChannelID      uint64
	LastSuccessAt        time.Time
	LastFailAt           time.Time
	LastFailReason       string
	SuccessCount         int
	FailCount            int
	PermanentFailScore   float64
	PermanentFailUpdated time.Time
	SuccessAmountSat     int64
	SuccessFeePpm        int64
	LastSuccessRouteHops []string // Wave 4.1: hop pubkeys of the last successful route
}

type rebalanceTargetPairStats struct {
	TargetChannelID            uint64
	Attempts                   int
	Successes                  int
	Failures                   int
	RecentStructuralFailures   int
	PermanentFailScore         float64
	LastSuccessAt              time.Time
	LastFailAt                 time.Time
	RecentStatsLoaded          bool
	RecentAttempts             int
	RecentSuccesses            int
	RecentFailures             int
	RecentAllSourcesFailed     int
	RecentLastSuccessAt        time.Time
	RecentLastFailAt           time.Time
	RecentSentJobs             int
	RecentSentSat              int64
	RecentRebalanceFeeSat      int64
	RecentForward24hAmountSat  int64
	RecentForward24hFeeSat     int64
	RecentRealizedNet24hSat    int64
	RecentForwardAmountSat     int64
	RecentForwardFeeSat        int64
	RecentRealizedNetSat       int64
	RecentForwardSlowAmountSat int64
	RecentForwardSlowFeeSat    int64
	RecentRealizedNetSlowSat   int64
}

type recentCooldownStat struct {
	ChannelID       uint64
	Attempts        int
	Failures        int
	Successes       int
	DistinctSources int
	LastAttemptAt   time.Time
	LastFailureAt   time.Time
	LastSuccessAt   time.Time
}

type sovereignTargetStructuralCooldownStat struct {
	TargetChannelID     uint64
	Failures            int
	LastFailureAttempts int
	LastFailureAt       time.Time
	LastSuccessAt       time.Time
}

type sovereignUnsoldLiquidityStat struct {
	CompletedAt      time.Time
	TargetAmountSat  int64
	SentSat          int64
	FeePaidSat       int64
	ForwardAmountSat int64
	ForwardFeeSat    int64
}

type rebalanceJobEconomics struct {
	ExpectedGainSat   int64
	EstimatedCostSat  int64
	ExpectedProfitSat int64
	BudgetCostSat     int64
	Score             int64
}

type recentTargetCooldownWindows struct {
	RecentSince         time.Time
	NoAttemptSince      time.Time
	FailedSince         time.Time
	DistinctSourceSince time.Time
}

type recentTargetCooldownSet struct {
	Recent         map[uint64]recentCooldownStat
	NoAttempt      map[uint64]recentCooldownStat
	Failed         map[uint64]recentCooldownStat
	DistinctSource map[uint64]recentCooldownStat
}

type rebalanceCost7dStat struct {
	FeeSat    int64
	AmountSat int64
	FeePpm    int64
}

type rebalanceAttemptTelemetry24h struct {
	Attempts                 int64
	FailedAttempts           int64
	SuccessAttempts          int64
	SuccessAmountSat         int64
	SuccessAvgAmountSat      int64
	AttemptSuccessRate       float64
	AttemptsPerSuccess       float64
	SuccessSatsPerAttempt    float64
	SuccessBelowMinAttempts  int64
	SuccessBelowMinAmountSat int64
	SuccessBelowMinRate      float64
}

type rebalanceAutopilotEconomics7d struct {
	RebalanceAmountSat int64
	RebalanceCostSat   int64
	RebalanceCostPpm   int64
	// "Velocidade" — forward atribuído na janela attribution_window
	ForwardAmountSat int64
	ForwardFeeSat    int64
	ForwardFeePpm    int64
	RealizedNetSat   int64
	SellThrough      float64
	// "Realizado" — forward atribuído na janela slow_seller_window (inclui
	// slow movers). Mesmo denominador (rebalance 7d), janela maior.
	ForwardAmountSlowSat int64
	ForwardFeeSlowSat    int64
	ForwardFeeSlowPpm    int64
	RealizedNetSlowSat   int64
	SellThroughSlow      float64
	// Janelas usadas (vindas da config/UI), pra label dos indicadores.
	AttributionWindowHours int
	SlowSellerWindowHours  int
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

type BaselineMetricsPeriod struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
}

type BaselineMetricsAggregate struct {
	JobsTotal               int64    `json:"jobs_total"`
	JobsSucceeded           int64    `json:"jobs_succeeded"`
	JobsPartial             int64    `json:"jobs_partial"`
	JobsFailed              int64    `json:"jobs_failed"`
	JobsCancelled           int64    `json:"jobs_cancelled"`
	AttemptsTotal           int64    `json:"attempts_total"`
	AttemptsSucceeded       int64    `json:"attempts_succeeded"`
	SuccessRate             float64  `json:"success_rate"`
	PartialRate             float64  `json:"partial_rate"`
	FailedRate              float64  `json:"failed_rate"`
	AvgAttemptsPerJob       float64  `json:"avg_attempts_per_job"`
	AvgSatsPerSuccessfulJob float64  `json:"avg_sats_per_successful_job"`
	AvgFeePpmPaid           float64  `json:"avg_fee_ppm_paid"`
	FeePaidSatTotal         int64    `json:"fee_paid_sat_total"`
	AmountSucceededSatTotal int64    `json:"amount_succeeded_sat_total"`
	TimeToPaybackP50Hours   *float64 `json:"time_to_payback_p50_hours,omitempty"`
}

type BaselineMetricsDaily struct {
	Day                     string   `json:"day"`
	JobsTotal               int64    `json:"jobs_total"`
	JobsSucceeded           int64    `json:"jobs_succeeded"`
	JobsPartial             int64    `json:"jobs_partial"`
	JobsFailed              int64    `json:"jobs_failed"`
	JobsCancelled           int64    `json:"jobs_cancelled"`
	AttemptsTotal           int64    `json:"attempts_total"`
	AttemptsSucceeded       int64    `json:"attempts_succeeded"`
	SuccessRate             float64  `json:"success_rate"`
	PartialRate             float64  `json:"partial_rate"`
	FailedRate              float64  `json:"failed_rate"`
	AvgAttemptsPerJob       float64  `json:"avg_attempts_per_job"`
	AvgSatsPerSuccessfulJob float64  `json:"avg_sats_per_successful_job"`
	AvgFeePpmPaid           float64  `json:"avg_fee_ppm_paid"`
	FeePaidSatTotal         int64    `json:"fee_paid_sat_total"`
	AmountSucceededSatTotal int64    `json:"amount_succeeded_sat_total"`
	TimeToPaybackP50Hours   *float64 `json:"time_to_payback_p50_hours,omitempty"`
}

type BaselineMetrics struct {
	Period    BaselineMetricsPeriod    `json:"period"`
	Aggregate BaselineMetricsAggregate `json:"aggregate"`
	Daily     []BaselineMetricsDaily   `json:"daily"`
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

	mu                              sync.Mutex
	started                         bool
	stop                            chan struct{}
	wake                            chan struct{}
	subs                            map[chan RebalanceEvent]struct{}
	cfg                             RebalanceConfig
	cfgLoaded                       bool
	mcHalfLifeApplied               int64
	lastScan                        time.Time
	lastScanStatus                  string
	lastScanDetail                  string
	lastScanCandidates              int
	lastScanRemainingBudgetSat      int64
	lastScanReasons                 map[string]int
	lastScanTopScoreSat             int64
	lastScanProfitSkipped           int
	lastScanQueued                  int
	lastScanSkipped                 []RebalanceSkipDetail
	lastSovereignDecisionAt         time.Time
	lastSovereignMode               string
	lastSovereignCandidates         int
	lastSovereignSelected           int
	lastSovereignExpectedProfitSat  int64
	lastSovereignBudgetRemainingSat int64
	lastSovereignDecisions          []RebalanceSovereignDecision
	lastManualRestartAt             time.Time
	lastManualRestartQueued         int
	lastManualRestartReasons        map[string]int
	criticalMissCount               int
	sem                             chan struct{}
	semInflight                     int
	semDesiredCap                   int
	semPendingResize                bool
	cooldownProbeSem                chan struct{}
	channelLocks                    map[uint64]bool
	jobCancel                       map[int64]context.CancelFunc
	manualRestart                   map[int64]manualRestartInfo
	manualRestartCancel             map[uint64]*manualRestartHandle
	lastAutoByTarget                map[uint64]time.Time
	lastMCResetAt                   time.Time
	lastMCResetReason               string
	mcResetCount                    int64
	drainRateCache                  map[uint64]int64
	drainRateCacheAt                time.Time

	// Cache da baseline trailing 30d dos health signals (effectiveness,
	// fast-path hit, payback rebalanced). As agregações de 30d sobre
	// rebalance_jobs/attempts/notifications são pesadas e a baseline anda
	// devagar, então é cacheada com refresh em background (nunca bloqueia o
	// overview): se estiver stale, dispara recompute assíncrono e serve o
	// valor cacheado (vazio na 1ª chamada, popula no próximo refresh).
	healthSignals30dCache      rebalanceHealthSignals30d
	healthSignals30dRefreshing bool

	// Cache curto de ListChannels para evitar saturação de gRPC quando
	// múltiplos jobs (cooldown-probe burst, auto-restart watch, auto-scan)
	// chamam ListChannels concorrentemente. Sem isso, 5+ chamadas simultâneas
	// pesadas (~99 canais com PeerAliasLookup) hangam o LND e geram cascata
	// de "lnd unavailable". TTL curto (5s) permite estado quase-fresh.
	chCacheMu      sync.Mutex
	chCacheData    []lndclient.ChannelInfo
	chCacheFetchAt time.Time

	// R5 — Exploration burnout (in-memory): tracks recent exploration
	// attempts per target channel so chronically failing targets stop
	// burning exploration slots cycle after cycle. State is volatile; on
	// process restart the system starts fresh, which is acceptable (the
	// worst case is a known-bad target being tried once more).
	explorationStatsMu sync.Mutex
	explorationStats   map[uint64]*explorationStat
	explorationJobs    map[int64]uint64 // jobID → channelID (only set for exploration-marked jobs)
}

// explorationStat tracks exploration outcomes for a single target channel in a
// rolling 24h window. When AttemptsWindow >= burnoutMinAttempts and all of
// them are failures, the channel enters burnout for burnoutDuration. A
// success clears the failure window and any active burnout.
type explorationStat struct {
	AttemptsWindow []time.Time
	FailuresWindow []time.Time
	BurnedUntil    time.Time
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
		cooldownProbeSem:    make(chan struct{}, cooldownProbeWorkerSlots),
		explorationStats:    map[uint64]*explorationStat{},
		explorationJobs:     map[int64]uint64{},
	}
}

func defaultRebalanceConfig() RebalanceConfig {
	return RebalanceConfig{
		AutoEnabled:                            false,
		Profile:                                rebalanceProfileBalanced,
		SchedulerMode:                          rebalanceSchedulerModeSovereignLive,
		SovereignCandidateScope:                rebalanceSovereignScopeAutoAndManualRestart,
		SovereignMaxJobsPerCycle:               4,
		SovereignMinExpectedProfitSat:          10,
		SovereignLowSuccessMinRate:             sovereignLowSuccessRate,
		SovereignLowSuccessMinProfitCostRatio:  sovereignLowSuccessProfitCostRatio,
		SovereignBudgetEfficiencyMinRatio:      sovereignBudgetEfficiencyProfitCostRatio,
		SovereignRouteDeadSourceShare:          sovereignRouteDeadSourceShare,
		SovereignRiskScoreFloor:                sovereignRiskScoreFloor,
		SovereignGainV3ColdStartPct:            0.85,
		FastPathMaxTimeoutSec:                  fastPathMaxTimeoutSecDefault,
		SovereignTopBucketPct:                  sovereignTopBucketPctDefault,
		SovereignAttributionWindowHours:        sovereignAttributionWindowDefaultHours,
		SovereignSlowSellerWindowHours:         sovereignSlowSellerWindowDefaultHours,
		SovereignTargetSourceQuarantineHours:   sovereignTargetSourceQuarantineDefaultHours,
		SovereignStructuralCooldownRepeatHours: sovereignTargetStructuralCooldownRepeatDefaultHours,
		SovereignExplorationSlotPct:            15,
		SovereignSourceOpportunityCostEnabled:  true,
		SovereignSlowSellerEnabled:             true,
		ScanIntervalSec:                        900,
		DeadbandPct:                            3,
		SourceMinLocalPct:                      15,
		EconRatio:                              0.7,
		EconRatioMaxPpm:                        0,
		FeeLimitPpm:                            0,
		LostProfit:                             false,
		FailTolerancePpm:                       500,
		ROIMin:                                 1.0,
		DailyBudgetPct:                         50,
		BudgetMode:                             rebalanceBudgetModeHybridRevenue,
		BudgetUnlimited:                        false,
		BudgetAutoOnly:                         true,
		ManualReserveEnabled:                   false,
		ManualReserveMode:                      rebalanceManualReserveModeFixedSat,
		ManualReserveValue:                     0,
		MaxConcurrent:                          4,
		MinAmountSat:                           50000,
		MaxAmountSat:                           0,
		MinSplitEnabled:                        true,
		MinProbeSat:                            5000,
		MinExecuteSat:                          10000,
		MppEnabled:                             true,
		MppMaxShards:                           6,
		MppParallelism:                         3,
		MppMinShardSat:                         rebalanceDefaultMppMinShardSat,
		MppRoundTimeoutSec:                     35,
		MppAutoOnly:                            true,
		FeeLadderSteps:                         1,
		AmountProbeSteps:                       8,
		AmountProbeAdaptive:                    true,
		AttemptTimeoutSec:                      60,
		RebalanceTimeoutSec:                    600,
		ManualRestartWatch:                     false,
		ManualRestartIgnoreEconomicGates:       false,
		CooldownProbeEnabled:                   false,
		MissionControlHalfLifeSec:              0,
		PaybackModeFlags:                       paybackModePayback | paybackModeTime | paybackModeCritical,
		FreshPaidLiquidityLockEnabled:          true,
		FreshPaidLiquidityLockHours:            freshPaidLiquidityLockDefaultHours,
		UnlockDays:                             7,
		CriticalReleasePct:                     20,
		CriticalMinSources:                     2,
		CriticalMinAvailableSats:               0,
		CriticalCycles:                         3,
		RebalanceCostFloorPpm:                  150,
		SourceMinPaybackProgress:               0.95,
		MissionControlReinforce:                false,
		GainModelVersion:                       3,
		VelocityWeight:                         0.7,
		AutofeeSettlingWindowSec:               7200,
		AutofeeSettlingMultiplier:              0.5,
		DelegatedFastPathEnabled:               true,
		DelegatedFastPathStrictPayback:         true,
		AutoTargetEnabled:                      false,
		AutoTargetMaxPct:                       50,
		AutoTargetMinPct:                       10,
		AutoTargetStepPct:                      5,
		AutoTargetEvalIntervalHours:            6,
		AutoTargetMaxUpsPerCycle:               3,
		AutoTargetMaxLocalSat:                  5_000_000,
		AutoTargetMinDrainRateSatPerHr:         5000,
		AutoTargetMinRevenue7dSat:              500,
		AutoTargetUpSuccessThreshold:           0.5,
		AutoTargetDownSuccessThreshold:         0.25,
		AutoTargetDrainFirstMultiplier:         3.0,
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

	// Cleanup AGRESSIVO de jobs órfãos no startup. Após restart/crash, qualquer
	// job em status='running' OU 'queued' no DB é por definição órfão — não há
	// goroutine viva no processo novo executando-os. Marcar TODOS sem filtro
	// de idade (diferente do cleanupStaleJobs periódico que respeita 1h pra
	// não pegar jobs em fila legitimamente esperando).
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	s.cleanupOrphanedJobsAtStartup(startupCtx)
	startupCancel()

	go s.runAutoLoop()
	go s.runManualRestartWatchLoop()
	go s.runPairStatsCleanupLoop()
	go s.runStaleJobsCleanupLoop()
}

// runStaleJobsCleanupLoop chama cleanupStaleJobs periodicamente. Garante
// que jobs órfãos sejam limpos mesmo sem operador abrindo a UI da fila —
// importante após restarts/crashes onde goroutines morrem sem chamar
// finishJob, deixando rows com status='running' indefinidamente.
func (s *RebalanceService) runStaleJobsCleanupLoop() {
	const interval = 2 * time.Minute
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.cleanupStaleJobs(ctx)
			cancel()
			timer.Reset(interval)
		case <-s.stop:
			return
		}
	}
}

// runPairStatsCleanupLoop periodically deletes stale pair_stats rows. Wave 4.3.
// A row is considered stale when it has only ever failed (last_success_at IS
// NULL) and the last failure is older than pairStatsStaleAfter. These rows
// otherwise sit in the table and color the source/target sort + recent-failure
// cooldown filter long after the underlying network state has changed.
func (s *RebalanceService) runPairStatsCleanupLoop() {
	// Run once at startup (after a brief delay to avoid Start() contention),
	// then on the interval.
	startupDelay := time.NewTimer(5 * time.Minute)
	select {
	case <-startupDelay.C:
		s.cleanupStalePairStats()
	case <-s.stop:
		if !startupDelay.Stop() {
			<-startupDelay.C
		}
		return
	}
	for {
		timer := time.NewTimer(pairStatsCleanupInterval)
		select {
		case <-timer.C:
			s.cleanupStalePairStats()
		case <-s.stop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (s *RebalanceService) cleanupStalePairStats() {
	if s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cutoff := time.Now().Add(-pairStatsStaleAfter)
	tag, err := s.db.Exec(ctx, `
delete from rebalance_pair_stats
where last_success_at is null
  and last_fail_at is not null
  and last_fail_at < $1`, cutoff)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance pair_stats cleanup failed: %v", err)
		}
		return
	}
	if s.logger != nil && tag.RowsAffected() > 0 {
		s.logger.Printf("rebalance pair_stats cleanup: removed %d stale rows (cutoff=%s)", tag.RowsAffected(), cutoff.Format(time.RFC3339))
	}
	// Retention for AutoTarget decision history (keep the audit table lean).
	atCutoff := time.Now().Add(-autoTargetHistoryRetention)
	if atTag, atErr := s.db.Exec(ctx, `delete from rebalance_auto_target_history where decided_at < $1`, atCutoff); atErr != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance auto_target_history cleanup failed: %v", atErr)
		}
	} else if s.logger != nil && atTag.RowsAffected() > 0 {
		s.logger.Printf("rebalance auto_target_history cleanup: removed %d rows older than %s", atTag.RowsAffected(), atCutoff.Format(time.RFC3339))
	}
}

func (s *RebalanceService) ResolveChannel(ctx context.Context, channelID uint64, channelPoint string) (uint64, string, error) {
	if s.lnd == nil {
		return 0, "", errors.New("lnd unavailable")
	}
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return 0, "", err
	}
	trimmed := strings.TrimSpace(channelPoint)
	if trimmed != "" {
		for _, ch := range channels {
			if strings.EqualFold(ch.ChannelPoint, trimmed) {
				return ch.ChannelID, ch.ChannelPoint, nil
			}
		}
	}
	if channelID != 0 {
		for _, ch := range channels {
			if ch.ChannelID == channelID {
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
	subs := make([]chan RebalanceEvent, 0, len(s.subs))
	for ch := range s.subs {
		subs = append(subs, ch)
	}
	s.mu.Unlock()
	for _, ch := range subs {
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
	updated = reconcileRebalanceProfileState(updated, s.nodeCalibration(ctx))
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

func (s *RebalanceService) missionControlStateLocked(now time.Time) RebalanceMissionControlState {
	state := RebalanceMissionControlState{
		LastMCResetReason:  s.lastMCResetReason,
		MCResetCount:       s.mcResetCount,
		MCResetCooldownSec: int64(mcResetCooldown / time.Second),
	}
	if !s.lastMCResetAt.IsZero() {
		state.LastMCResetAt = s.lastMCResetAt.UTC().Format(time.RFC3339)
		if now.IsZero() {
			now = time.Now()
		}
		if remaining := mcResetCooldown - now.Sub(s.lastMCResetAt); remaining > 0 {
			state.MCResetCooldownRemainingSec = int64(remaining.Round(time.Second) / time.Second)
			if state.MCResetCooldownRemainingSec < 1 {
				state.MCResetCooldownRemainingSec = 1
			}
		}
	}
	return state
}

func (s *RebalanceService) missionControlState(now time.Time) RebalanceMissionControlState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.missionControlStateLocked(now)
}

// ResetMissionControl forces an operator-requested LND Mission Control reset
// and records the reset telemetry exposed in the overview.
func (s *RebalanceService) ResetMissionControl(ctx context.Context, trigger string) (RebalanceMissionControlState, error) {
	return s.resetMissionControl(ctx, trigger, true)
}

// tryResetMissionControl invokes LND ResetMissionControl with a cross-job
// cooldown so a burst of concurrent failures triggers at most one reset per
// mcResetCooldown window. Returns true when the reset was performed, false
// when skipped (cooldown not yet elapsed or LND unavailable).
func (s *RebalanceService) tryResetMissionControl(ctx context.Context, trigger string) bool {
	_, err := s.resetMissionControl(ctx, trigger, false)
	return err == nil
}

func (s *RebalanceService) resetMissionControl(ctx context.Context, trigger string, force bool) (RebalanceMissionControlState, error) {
	if s.lnd == nil {
		return s.missionControlState(time.Now()), errors.New("lnd unavailable")
	}
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		trigger = "manual"
	}
	now := time.Now()
	s.mu.Lock()
	if !force && !s.lastMCResetAt.IsZero() && now.Sub(s.lastMCResetAt) < mcResetCooldown {
		state := s.missionControlStateLocked(now)
		s.mu.Unlock()
		return state, errMissionControlResetCooldown
	}
	previousAt := s.lastMCResetAt
	previousReason := s.lastMCResetReason
	s.lastMCResetAt = now
	s.lastMCResetReason = trigger
	s.mu.Unlock()
	if err := s.lnd.ResetMissionControl(ctx); err != nil {
		s.mu.Lock()
		if s.lastMCResetAt.Equal(now) && s.lastMCResetReason == trigger {
			s.lastMCResetAt = previousAt
			s.lastMCResetReason = previousReason
		}
		state := s.missionControlStateLocked(time.Now())
		s.mu.Unlock()
		if s.logger != nil {
			s.logger.Printf("rebalance %s: mission control reset failed: %v", trigger, err)
		}
		return state, err
	}
	s.mu.Lock()
	s.mcResetCount++
	state := s.missionControlStateLocked(time.Now())
	s.mu.Unlock()
	if s.logger != nil {
		s.logger.Printf("rebalance %s: mission control reset triggered", trigger)
	}
	return state, nil
}

// resetSemaphore is called on Start() and on UpdateConfig(). It records the
// desired capacity (from cfg.MaxConcurrent) and rebuilds the semaphore channel
// only when there are no in-flight jobs. When a resize lands while jobs are
// running, the new size is staged in semDesiredCap and applied by the last
// releaseSem() of the in-flight set. This prevents leaking slots into a fresh
// channel when defer-release of running jobs hits a replaced semaphore.
//
// Wave 2.3: previously this rebuilt s.sem unconditionally, which could either
// duplicate capacity (running jobs holding the old channel + a fresh empty new
// channel) or starve the new channel of a phantom slot when the old job
// released onto it.
func (s *RebalanceService) resetSemaphore() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.cfg
	if !s.cfgLoaded {
		cfg = defaultRebalanceConfig()
	}
	desired := cfg.MaxConcurrent
	if desired <= 0 {
		desired = 1
	}
	s.semDesiredCap = desired
	if s.sem == nil {
		// First-time initialization: create immediately.
		s.sem = make(chan struct{}, desired)
		s.semInflight = 0
		s.semPendingResize = false
		return
	}
	if s.semInflight == 0 {
		s.sem = make(chan struct{}, desired)
		s.semPendingResize = false
		return
	}
	// Jobs are in flight on the existing channel; defer the resize.
	s.semPendingResize = true
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

func isManualRestartJob(source string, reason string, manualAutoRestart bool) bool {
	return source == "manual" && (manualAutoRestart || reason == "auto-restart")
}

func shouldEnforceManualRestartCooldown(source string, reason string) bool {
	return isManualRestartJob(source, reason, false)
}

func manualRestartWatchEligibility(snapshot RebalanceChannel, cfg RebalanceConfig) (bool, string) {
	// Modo convicção: o operador marcou o canal como manual restart porque
	// sabe que vale a pena enchê-lo, mesmo sem retorno econômico imediato.
	// Ignora cost gate (filtro 1.4, embutido em EligibleAsTarget) e ROI
	// guardrail — usa só a elegibilidade base (active + deficit + outgoing >
	// peer). Fee cap, cooldown, budget e execute_min seguem aplicados no
	// runManualRestartWatch / startJob.
	if cfg.ManualRestartIgnoreEconomicGates {
		if !snapshot.EligibleAsManualTarget {
			return false, "target_not_eligible"
		}
		return true, ""
	}
	if !snapshot.EligibleAsTarget {
		return false, "target_not_eligible"
	}
	if cfg.ROIMin > 0 && snapshot.ROIEstimateValid && snapshot.ROIEstimate < cfg.ROIMin {
		return false, "roi_guardrail"
	}
	return true, ""
}

func manualRestartStartErrorReason(err error) string {
	switch {
	case err == nil:
		return ""
	case err.Error() == "channel busy":
		return "channel_busy"
	case errors.Is(err, errManualRestartCooldown):
		return "target_cooldown"
	case errors.Is(err, errManualBudgetExhausted):
		return "budget_too_low"
	case errors.Is(err, errManualBudgetInsufficient):
		return "budget_too_low"
	default:
		return "start_error"
	}
}

func autoStartErrorReason(err error) string {
	switch {
	case err == nil:
		return ""
	case err.Error() == "channel busy":
		return "channel_busy"
	case err.Error() == "target already within range":
		return "target_already_balanced"
	case err.Error() == "target channel not found":
		return "target_not_found"
	default:
		return "start_error"
	}
}

func defaultRecentTargetCooldownWindows(now time.Time) recentTargetCooldownWindows {
	return recentTargetCooldownWindows{
		RecentSince:         now.Add(-recentCooldownWindow),
		NoAttemptSince:      now.Add(-targetNoAttemptCooldownWindow),
		FailedSince:         now.Add(-targetFailedCooldownWindow),
		DistinctSourceSince: now.Add(-targetDistinctSourceCooldownWindow),
	}
}

func (s *RebalanceService) loadRecentTargetCooldownSet(ctx context.Context, windows recentTargetCooldownWindows) recentTargetCooldownSet {
	return recentTargetCooldownSet{
		Recent:         s.loadRecentTargetCooldowns(ctx, windows.RecentSince),
		NoAttempt:      s.loadRecentTargetNoAttemptCooldowns(ctx, windows.NoAttemptSince),
		Failed:         s.loadRecentTargetFailedCooldowns(ctx, windows.FailedSince),
		DistinctSource: s.loadRecentTargetDistinctSourceCooldowns(ctx, windows.DistinctSourceSince),
	}
}

func normalizeChannelSetting(setting channelSetting) channelSetting {
	setting.AutomationMode = normalizeChannelAutomationMode(setting.AutomationMode)
	if setting.AutomationMode == "" {
		setting.AutomationMode = channelAutomationModeNormal
	}
	if isChannelAutomationParked(setting.AutomationMode) {
		setting.AutoEnabled = false
		setting.ManualRestartEnabled = false
	}
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
	cfg.Profile = normalizeRebalanceProfile(cfg.Profile)
	cfg.SchedulerMode = normalizeRebalanceSchedulerMode(cfg.SchedulerMode)
	cfg.SovereignCandidateScope = normalizeRebalanceSovereignScope(cfg.SovereignCandidateScope)
	if cfg.SovereignMaxJobsPerCycle <= 0 {
		cfg.SovereignMaxJobsPerCycle = def.SovereignMaxJobsPerCycle
	}
	if cfg.SovereignMinExpectedProfitSat < 0 {
		cfg.SovereignMinExpectedProfitSat = 0
	}
	cfg.SovereignLowSuccessMinRate = normalizeRatioConfig(cfg.SovereignLowSuccessMinRate, def.SovereignLowSuccessMinRate, 1)
	cfg.SovereignLowSuccessMinProfitCostRatio = normalizeRatioConfig(cfg.SovereignLowSuccessMinProfitCostRatio, def.SovereignLowSuccessMinProfitCostRatio, 0)
	cfg.SovereignBudgetEfficiencyMinRatio = normalizeRatioConfig(cfg.SovereignBudgetEfficiencyMinRatio, def.SovereignBudgetEfficiencyMinRatio, 0)
	cfg.SovereignRouteDeadSourceShare = normalizeRatioConfig(cfg.SovereignRouteDeadSourceShare, def.SovereignRouteDeadSourceShare, 1)
	cfg.SovereignRiskScoreFloor = normalizeRatioConfig(cfg.SovereignRiskScoreFloor, def.SovereignRiskScoreFloor, 0.2)
	if cfg.SovereignGainV3ColdStartPct < sovereignGainV3ColdStartPctMin || cfg.SovereignGainV3ColdStartPct > sovereignGainV3ColdStartPctMax {
		cfg.SovereignGainV3ColdStartPct = def.SovereignGainV3ColdStartPct
	}
	if cfg.FastPathMaxTimeoutSec < fastPathMaxTimeoutSecMin || cfg.FastPathMaxTimeoutSec > fastPathMaxTimeoutSecMax {
		cfg.FastPathMaxTimeoutSec = def.FastPathMaxTimeoutSec
	}
	if cfg.SovereignTopBucketPct < sovereignTopBucketPctMin || cfg.SovereignTopBucketPct > sovereignTopBucketPctMax {
		cfg.SovereignTopBucketPct = def.SovereignTopBucketPct
	}
	if cfg.SovereignAttributionWindowHours <= 0 {
		cfg.SovereignAttributionWindowHours = def.SovereignAttributionWindowHours
	}
	if cfg.SovereignAttributionWindowHours < 24 {
		cfg.SovereignAttributionWindowHours = 24
	}
	if cfg.SovereignAttributionWindowHours > sovereignWindowMaxHours {
		cfg.SovereignAttributionWindowHours = sovereignWindowMaxHours
	}
	if cfg.SovereignSlowSellerWindowHours <= 0 {
		cfg.SovereignSlowSellerWindowHours = def.SovereignSlowSellerWindowHours
	}
	if cfg.SovereignSlowSellerWindowHours < cfg.SovereignAttributionWindowHours {
		cfg.SovereignSlowSellerWindowHours = cfg.SovereignAttributionWindowHours
	}
	if cfg.SovereignSlowSellerWindowHours > sovereignWindowMaxHours {
		cfg.SovereignSlowSellerWindowHours = sovereignWindowMaxHours
	}
	if cfg.SovereignTargetSourceQuarantineHours < 0 {
		cfg.SovereignTargetSourceQuarantineHours = def.SovereignTargetSourceQuarantineHours
	}
	if cfg.SovereignTargetSourceQuarantineHours > sovereignWindowMaxHours {
		cfg.SovereignTargetSourceQuarantineHours = sovereignWindowMaxHours
	}
	if cfg.SovereignStructuralCooldownRepeatHours <= 0 {
		cfg.SovereignStructuralCooldownRepeatHours = def.SovereignStructuralCooldownRepeatHours
	}
	if cfg.SovereignStructuralCooldownRepeatHours > sovereignTargetStructuralCooldownRepeatMaxHours {
		cfg.SovereignStructuralCooldownRepeatHours = sovereignTargetStructuralCooldownRepeatMaxHours
	}
	if cfg.SovereignExplorationSlotPct < 0 {
		cfg.SovereignExplorationSlotPct = 0
	}
	if cfg.SovereignExplorationSlotPct > sovereignExplorationSlotPctMax {
		cfg.SovereignExplorationSlotPct = sovereignExplorationSlotPctMax
	}
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
		cfg.MppMinShardSat = defaultMppMinShardSatForConfig(cfg)
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
	if cfg.RebalanceCostFloorPpm < 0 {
		cfg.RebalanceCostFloorPpm = 0
	}
	if cfg.SourceMinPaybackProgress < 0 {
		cfg.SourceMinPaybackProgress = 0
	}
	if cfg.FreshPaidLiquidityLockHours <= 0 {
		cfg.FreshPaidLiquidityLockHours = def.FreshPaidLiquidityLockHours
	}
	if cfg.GainModelVersion <= 0 {
		cfg.GainModelVersion = def.GainModelVersion
	}
	if cfg.GainModelVersion > 3 {
		cfg.GainModelVersion = 3
	}
	if math.IsNaN(cfg.VelocityWeight) || math.IsInf(cfg.VelocityWeight, 0) {
		cfg.VelocityWeight = def.VelocityWeight
	}
	if cfg.VelocityWeight < 0 {
		cfg.VelocityWeight = 0
	}
	if cfg.VelocityWeight > 1 {
		cfg.VelocityWeight = 1
	}
	if cfg.AutofeeSettlingWindowSec < 0 {
		cfg.AutofeeSettlingWindowSec = 0
	}
	if math.IsNaN(cfg.AutofeeSettlingMultiplier) || math.IsInf(cfg.AutofeeSettlingMultiplier, 0) {
		cfg.AutofeeSettlingMultiplier = def.AutofeeSettlingMultiplier
	}
	if cfg.AutofeeSettlingMultiplier < 0 {
		cfg.AutofeeSettlingMultiplier = 0
	}
	if cfg.AutofeeSettlingMultiplier > 1 {
		cfg.AutofeeSettlingMultiplier = 1
	}
	// AutoTarget clamps. Keep the band sane and preserve hysteresis (down < up).
	if cfg.AutoTargetMaxPct < 10 || cfg.AutoTargetMaxPct > 90 {
		cfg.AutoTargetMaxPct = def.AutoTargetMaxPct
	}
	if cfg.AutoTargetMinPct < 1 {
		cfg.AutoTargetMinPct = def.AutoTargetMinPct
	}
	if cfg.AutoTargetMinPct >= cfg.AutoTargetMaxPct {
		cfg.AutoTargetMinPct = cfg.AutoTargetMaxPct / 2
		if cfg.AutoTargetMinPct < 1 {
			cfg.AutoTargetMinPct = 1
		}
	}
	if cfg.AutoTargetStepPct < 1 || cfg.AutoTargetStepPct > 25 {
		cfg.AutoTargetStepPct = def.AutoTargetStepPct
	}
	if cfg.AutoTargetEvalIntervalHours <= 0 {
		cfg.AutoTargetEvalIntervalHours = def.AutoTargetEvalIntervalHours
	}
	if cfg.AutoTargetEvalIntervalHours > 168 {
		cfg.AutoTargetEvalIntervalHours = 168
	}
	if cfg.AutoTargetMaxUpsPerCycle <= 0 {
		cfg.AutoTargetMaxUpsPerCycle = def.AutoTargetMaxUpsPerCycle
	}
	if cfg.AutoTargetMaxUpsPerCycle > 50 {
		cfg.AutoTargetMaxUpsPerCycle = 50
	}
	if cfg.AutoTargetMaxLocalSat <= 0 {
		cfg.AutoTargetMaxLocalSat = def.AutoTargetMaxLocalSat
	}
	if cfg.AutoTargetMinDrainRateSatPerHr < 0 {
		cfg.AutoTargetMinDrainRateSatPerHr = def.AutoTargetMinDrainRateSatPerHr
	}
	if cfg.AutoTargetMinRevenue7dSat < 0 {
		cfg.AutoTargetMinRevenue7dSat = 0
	}
	cfg.AutoTargetUpSuccessThreshold = normalizeRatioConfig(cfg.AutoTargetUpSuccessThreshold, def.AutoTargetUpSuccessThreshold, 1)
	cfg.AutoTargetDownSuccessThreshold = normalizeRatioConfig(cfg.AutoTargetDownSuccessThreshold, def.AutoTargetDownSuccessThreshold, 1)
	if cfg.AutoTargetDownSuccessThreshold >= cfg.AutoTargetUpSuccessThreshold {
		cfg.AutoTargetDownSuccessThreshold = cfg.AutoTargetUpSuccessThreshold / 2
	}
	if math.IsNaN(cfg.AutoTargetDrainFirstMultiplier) || math.IsInf(cfg.AutoTargetDrainFirstMultiplier, 0) || cfg.AutoTargetDrainFirstMultiplier <= 0 {
		cfg.AutoTargetDrainFirstMultiplier = def.AutoTargetDrainFirstMultiplier
	}
	if cfg.AutoTargetDrainFirstMultiplier > 20 {
		cfg.AutoTargetDrainFirstMultiplier = 20
	}
	return cfg
}

func normalizeRebalanceSchedulerMode(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case rebalanceSchedulerModeSovereignShadow:
		return rebalanceSchedulerModeSovereignShadow
	case rebalanceSchedulerModeSovereignLive:
		return rebalanceSchedulerModeSovereignLive
	default:
		return rebalanceSchedulerModeRulesAuto
	}
}

func normalizeRebalanceSovereignScope(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case rebalanceSovereignScopeAutoOnly:
		return rebalanceSovereignScopeAutoOnly
	default:
		return rebalanceSovereignScopeAutoAndManualRestart
	}
}

func normalizeRatioConfig(value float64, fallback float64, max float64) float64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	if max > 0 && value > max {
		return max
	}
	return value
}

func isSovereignSchedulerMode(mode string) bool {
	mode = normalizeRebalanceSchedulerMode(mode)
	return mode == rebalanceSchedulerModeSovereignShadow || mode == rebalanceSchedulerModeSovereignLive
}

func freshPaidLiquidityLockDuration(cfg RebalanceConfig) time.Duration {
	hours := cfg.FreshPaidLiquidityLockHours
	if hours <= 0 {
		hours = freshPaidLiquidityLockDefaultHours
	}
	return time.Duration(hours) * time.Hour
}

func sovereignAttributionWindowHoursForConfig(cfg RebalanceConfig) int {
	hours := cfg.SovereignAttributionWindowHours
	if hours <= 0 {
		hours = sovereignAttributionWindowDefaultHours
	}
	if hours < 24 {
		hours = 24
	}
	if hours > sovereignWindowMaxHours {
		hours = sovereignWindowMaxHours
	}
	return hours
}

func sovereignSlowSellerWindowHoursForConfig(cfg RebalanceConfig) int {
	attribution := sovereignAttributionWindowHoursForConfig(cfg)
	hours := cfg.SovereignSlowSellerWindowHours
	if hours <= 0 {
		hours = sovereignSlowSellerWindowDefaultHours
	}
	if hours < attribution {
		hours = attribution
	}
	if hours > sovereignWindowMaxHours {
		hours = sovereignWindowMaxHours
	}
	return hours
}

func sovereignTargetSourceQuarantineDuration(cfg RebalanceConfig) time.Duration {
	hours := cfg.SovereignTargetSourceQuarantineHours
	if hours < 0 {
		hours = sovereignTargetSourceQuarantineDefaultHours
	}
	if hours > sovereignWindowMaxHours {
		hours = sovereignWindowMaxHours
	}
	return time.Duration(hours) * time.Hour
}

func defaultMppMinShardSatForConfig(cfg RebalanceConfig) int64 {
	if minExecuteSat := effectiveMinExecuteSat(cfg); minExecuteSat > 0 {
		return minExecuteSat
	}
	return rebalanceDefaultMppMinShardSat
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

func isTargetCooldownProbeJob(jobSource string, jobReason string) bool {
	return strings.TrimSpace(strings.ToLower(jobSource)) == "auto" &&
		strings.TrimSpace(strings.ToLower(jobReason)) == targetCooldownProbeReason
}

func shouldUseRecentFailureCache(jobSource string, jobReason string) bool {
	jobReason = strings.TrimSpace(strings.ToLower(jobReason))
	if jobReason == targetCooldownProbeReason {
		return false
	}
	jobSource = strings.TrimSpace(strings.ToLower(jobSource))
	if jobSource == "auto" {
		return true
	}
	return jobSource == "manual" && jobReason == "auto-restart"
}

func normalizedPairFailReason(reason string) string {
	reason = strings.TrimSpace(strings.ToLower(reason))
	for strings.HasPrefix(reason, "mpp shard:") {
		reason = strings.TrimSpace(strings.TrimPrefix(reason, "mpp shard:"))
	}
	return reason
}

func isStructuralRebalanceFailure(reason string) bool {
	reason = normalizedPairFailReason(reason)
	switch {
	case strings.Contains(reason, "unable to find a path"):
		return true
	case strings.Contains(reason, "no route"):
		return true
	case strings.Contains(reason, "no matching outgoing channel"):
		return true
	case strings.Contains(reason, "probe returned no amount"):
		return true
	case strings.Contains(reason, "mpp structural failure"):
		return true
	case strings.Contains(reason, "attempt timeout"):
		return true
	case strings.Contains(reason, "deadlineexceeded") || strings.Contains(reason, "deadline exceeded"):
		return true
	default:
		return false
	}
}

func isTemporaryPairFailure(reason string) bool {
	reason = normalizedPairFailReason(reason)
	return strings.Contains(reason, "temporary_channel_failure") || strings.Contains(reason, "temporary channel failure")
}

func shouldBlockPairForCurrentJobFailure(reason string) bool {
	return isStructuralRebalanceFailure(reason) || isTemporaryPairFailure(reason)
}

func hasRebalanceFallbackCandidate(sources []RebalanceChannel, sourceAvailable map[uint64]int64, pairStats map[uint64]pairStat, currentJobBlockedPairs map[uint64]struct{}, useRecentFailureCache bool, minExecuteSat int64, now time.Time) bool {
	if now.IsZero() {
		now = time.Now()
	}
	for _, source := range sources {
		available := sourceAvailable[source.ChannelID]
		if available <= 0 {
			continue
		}
		if minExecuteSat > 0 && available < minExecuteSat {
			continue
		}
		if useRecentFailureCache {
			if stat, ok := pairStats[source.ChannelID]; ok {
				if shouldSkipPairForRecentFailure(stat, now) {
					continue
				}
			}
			if _, ok := currentJobBlockedPairs[source.ChannelID]; ok {
				continue
			}
		}
		return true
	}
	return false
}

func shouldAbortMppStructuralFallback(succeededShards int, attemptedShards int, structuralFailureShards int, attemptedSources int, structuralFailureSources int) bool {
	if succeededShards > 0 || attemptedShards < mppStructuralAbortMinAttempts {
		return false
	}
	if structuralFailureSources < mppStructuralAbortMinDistinctSources || attemptedSources <= 0 {
		return false
	}
	shardRatio := float64(structuralFailureShards) / float64(attemptedShards)
	if shardRatio < mppStructuralAbortRatio {
		return false
	}
	sourceRatio := float64(structuralFailureSources) / float64(attemptedSources)
	return sourceRatio >= mppStructuralAbortRatio
}

func filterExecutableSources(sources []RebalanceChannel, sourceAvailable map[uint64]int64, minExecuteSat int64) []RebalanceChannel {
	if len(sources) == 0 {
		return sources
	}
	filtered := make([]RebalanceChannel, 0, len(sources))
	for _, source := range sources {
		available := sourceAvailable[source.ChannelID]
		if available <= 0 {
			continue
		}
		if minExecuteSat > 0 && available < minExecuteSat {
			continue
		}
		filtered = append(filtered, source)
	}
	return filtered
}

func delegatedFastPathSourceIDs(sources []RebalanceChannel, sourceAvailable map[uint64]int64, amountSat int64, minExecuteSat int64, strictPayback bool) []uint64 {
	sourceIDs := make([]uint64, 0, len(sources))
	for _, source := range sources {
		available := sourceAvailable[source.ChannelID]
		if available <= 0 {
			continue
		}
		if minExecuteSat > 0 && available < minExecuteSat {
			continue
		}
		if strictPayback && amountSat > 0 && available < amountSat {
			continue
		}
		sourceIDs = append(sourceIDs, source.ChannelID)
	}
	return sourceIDs
}

func preferredFastPathSourceCount(totalSources int, maxParts uint32) int {
	if totalSources <= 0 {
		return 0
	}
	count := int(maxParts) + 2
	if count < fastPathPreferredMinSources {
		count = fastPathPreferredMinSources
	}
	if count > fastPathPreferredMaxSources {
		count = fastPathPreferredMaxSources
	}
	if count > totalSources {
		count = totalSources
	}
	return count
}

func preferredFastPathTimeout(totalTimeoutSec int32) int32 {
	if totalTimeoutSec <= 0 {
		return 0
	}
	if totalTimeoutSec < fastPathPreferredTimeoutSec {
		return totalTimeoutSec
	}
	return fastPathPreferredTimeoutSec
}

func pairStatHasRecentSuccess(stat pairStat, now time.Time) bool {
	if stat.LastSuccessAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if now.Sub(stat.LastSuccessAt) > pairSuccessTTL {
		return false
	}
	return stat.LastFailAt.IsZero() || stat.LastSuccessAt.After(stat.LastFailAt)
}

func hasPreferredFastPathRouteProof(pairStats map[uint64]pairStat, sourceIDs []uint64, now time.Time) bool {
	if len(pairStats) == 0 || len(sourceIDs) == 0 {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	for _, sourceID := range sourceIDs {
		stat, ok := pairStats[sourceID]
		if !ok {
			continue
		}
		if pairStatHasRecentSuccess(stat, now) && !shouldSkipPairForRecentFailure(stat, now) {
			return true
		}
	}
	return false
}

func preferredDelegatedFastPathSourceIDs(sources []RebalanceChannel, sourceAvailable map[uint64]int64, pairStats map[uint64]pairStat, amountSat int64, minExecuteSat int64, strictPayback bool, maxSources int, now time.Time) []uint64 {
	if maxSources <= 0 || len(sources) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	eligible := make([]RebalanceChannel, 0, len(sources))
	for _, source := range sources {
		available := sourceAvailable[source.ChannelID]
		if available <= 0 {
			continue
		}
		if minExecuteSat > 0 && available < minExecuteSat {
			continue
		}
		if strictPayback && amountSat > 0 && available < amountSat {
			continue
		}
		eligible = append(eligible, source)
	}
	if len(eligible) == 0 {
		return nil
	}
	sourceRank := func(ch RebalanceChannel) (bool, bool, float64, int64, int64, int, time.Duration) {
		stat, ok := pairStats[ch.ChannelID]
		if !ok {
			return false, false, 0, 0, 0, 0, 0
		}
		hasRecentSuccess := pairStatHasRecentSuccess(stat, now)
		hasRecentFail := shouldSkipPairForRecentFailure(stat, now)
		permanentFailScore := decayedPermanentFailScore(stat.PermanentFailScore, stat.PermanentFailUpdated, now)
		successAge := time.Duration(0)
		if !stat.LastSuccessAt.IsZero() {
			successAge = now.Sub(stat.LastSuccessAt)
		}
		return hasRecentSuccess, hasRecentFail, permanentFailScore, stat.SuccessFeePpm, stat.SuccessAmountSat, stat.SuccessCount, successAge
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left := eligible[i]
		right := eligible[j]
		leftSuccess, leftFail, leftFailScore, leftFee, leftAmount, leftSuccessCount, leftAge := sourceRank(left)
		rightSuccess, rightFail, rightFailScore, rightFee, rightAmount, rightSuccessCount, rightAge := sourceRank(right)
		if leftFail != rightFail {
			return !leftFail
		}
		if leftSuccess != rightSuccess {
			return leftSuccess
		}
		if left.SourceOpportunityCost != right.SourceOpportunityCost {
			return left.SourceOpportunityCost < right.SourceOpportunityCost
		}
		leftAvailable := sourceAvailable[left.ChannelID]
		rightAvailable := sourceAvailable[right.ChannelID]
		if leftAvailable != rightAvailable {
			return leftAvailable > rightAvailable
		}
		if math.Abs(left.LocalPct-right.LocalPct) > 0.5 {
			return left.LocalPct > right.LocalPct
		}
		if left.DrainRateSatPerHour != right.DrainRateSatPerHour {
			return left.DrainRateSatPerHour > right.DrainRateSatPerHour
		}
		if left.PendingOutgoingHtlcs != right.PendingOutgoingHtlcs {
			return left.PendingOutgoingHtlcs < right.PendingOutgoingHtlcs
		}
		if math.Abs(leftFailScore-rightFailScore) > 0.25 {
			return leftFailScore < rightFailScore
		}
		if leftSuccess && rightSuccess {
			if leftFee != rightFee {
				return leftFee < rightFee
			}
			if leftSuccessCount != rightSuccessCount {
				return leftSuccessCount > rightSuccessCount
			}
			if leftAge != rightAge {
				return leftAge < rightAge
			}
			if leftAmount != rightAmount {
				return leftAmount > rightAmount
			}
		}
		if left.OutgoingFeePpm != right.OutgoingFeePpm {
			return left.OutgoingFeePpm < right.OutgoingFeePpm
		}
		return left.ChannelID < right.ChannelID
	})
	if len(eligible) > maxSources {
		eligible = eligible[:maxSources]
	}
	out := make([]uint64, 0, len(eligible))
	for _, source := range eligible {
		out = append(out, source.ChannelID)
	}
	return out
}

func shouldRunTargetCooldownProbe(lastAutoAt time.Time, now time.Time) bool {
	return shouldRunTargetCooldownProbeAfter(lastAutoAt, now, targetCooldownProbeInterval)
}

func shouldRunTargetCooldownProbeAfter(lastAutoAt time.Time, now time.Time, interval time.Duration) bool {
	if now.IsZero() {
		now = time.Now()
	}
	if interval <= 0 {
		interval = targetCooldownProbeInterval
	}
	if lastAutoAt.IsZero() {
		return true
	}
	return now.Sub(lastAutoAt) >= interval
}

func targetCooldownProbeIntervalForStats(attemptStat recentCooldownStat, noAttemptStat recentCooldownStat, failedStat recentCooldownStat, distinctSourceStat recentCooldownStat) time.Duration {
	if attemptStat.Failures >= targetCooldownMinAttempts ||
		noAttemptStat.Failures >= targetNoAttemptCooldownMinFailures ||
		failedStat.Failures >= targetFailedCooldownMinFailures ||
		distinctSourceStat.DistinctSources >= targetDistinctSourceMinFailures {
		return targetCooldownProbeBackoffInterval
	}
	return targetCooldownProbeInterval
}

func rebalanceCooldownProbeAmount(targetAmountSat int64, cfg RebalanceConfig) int64 {
	if targetAmountSat <= 0 {
		return 0
	}
	amount := effectiveStartAmountSat(cfg)
	if minExecute := effectiveMinExecuteSat(cfg); minExecute > amount {
		amount = minExecute
	}
	if amount <= 0 {
		amount = targetAmountSat
	}
	if amount > targetAmountSat {
		amount = targetAmountSat
	}
	return amount
}

func pairFailureBaseTTL(reason string) (time.Duration, bool) {
	reason = normalizedPairFailReason(reason)
	switch {
	case strings.Contains(reason, "no matching outgoing channel"):
		return 45 * time.Minute, true
	case strings.Contains(reason, "insufficient local balance"):
		return 45 * time.Minute, true
	case strings.Contains(reason, "unable to find a path"):
		return 20 * time.Minute, true
	case strings.Contains(reason, "no route"):
		return 20 * time.Minute, true
	case strings.Contains(reason, "probe returned no amount"):
		return 15 * time.Minute, true
	case strings.Contains(reason, "mpp structural failure"):
		return 15 * time.Minute, true
	case strings.Contains(reason, "route fee exceeds limit"):
		return 15 * time.Minute, true
	case strings.Contains(reason, "temporary_channel_failure") || strings.Contains(reason, "temporary channel failure"):
		return 5 * time.Minute, true
	case strings.Contains(reason, "timeout") || strings.Contains(reason, "deadlineexceeded") || strings.Contains(reason, "deadline exceeded"):
		return 5 * time.Minute, true
	default:
		return pairFailTTL, false
	}
}

func pairFailureTTL(reason string, failCount int) time.Duration {
	base, known := pairFailureBaseTTL(reason)
	if !known || failCount <= 1 {
		return base
	}
	ttl := base
	for i := 1; i < failCount; i++ {
		if ttl >= pairFailTTLMax/2 {
			return pairFailTTLMax
		}
		ttl *= 2
	}
	if ttl > pairFailTTLMax {
		return pairFailTTLMax
	}
	return ttl
}

func permanentFailScoreIncrement(reason string) float64 {
	if isStructuralRebalanceFailure(reason) {
		return 1
	}
	if isTemporaryPairFailure(reason) {
		return 0.25
	}
	return 0
}

func decayedPermanentFailScore(score float64, updatedAt time.Time, now time.Time) float64 {
	if score <= 0 {
		return 0
	}
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0
	}
	if score > permanentFailScoreMax {
		score = permanentFailScoreMax
	}
	if updatedAt.IsZero() {
		return score
	}
	if now.IsZero() {
		now = time.Now()
	}
	elapsed := now.Sub(updatedAt)
	if elapsed <= 0 {
		return score
	}
	decayed := score * math.Pow(0.5, float64(elapsed)/float64(permanentFailScoreHalfLife))
	if decayed < 0.01 {
		return 0
	}
	return decayed
}

func nextPermanentFailScore(current float64, updatedAt time.Time, now time.Time, increment float64) float64 {
	if increment <= 0 {
		return decayedPermanentFailScore(current, updatedAt, now)
	}
	next := decayedPermanentFailScore(current, updatedAt, now) + increment
	if next > permanentFailScoreMax {
		return permanentFailScoreMax
	}
	return next
}

func permanentFailScoreTTL(score float64) time.Duration {
	if score < permanentFailScoreSkipThreshold {
		return 0
	}
	steps := int(math.Ceil(score - permanentFailScoreSkipThreshold + 1))
	if steps < 1 {
		steps = 1
	}
	ttl := time.Duration(steps) * permanentFailScoreTTLStep
	if ttl > permanentFailScoreTTLMax {
		return permanentFailScoreTTLMax
	}
	return ttl
}

func pairFailureTTLForStat(stat pairStat, now time.Time) time.Duration {
	ttl := pairFailureTTL(stat.LastFailReason, stat.FailCount)
	permanentTTL := permanentFailScoreTTL(decayedPermanentFailScore(stat.PermanentFailScore, stat.PermanentFailUpdated, now))
	if permanentTTL > ttl {
		return permanentTTL
	}
	return ttl
}

func shouldSkipPairForRecentFailure(stat pairStat, now time.Time) bool {
	if stat.LastFailAt.IsZero() {
		return false
	}
	if !stat.LastSuccessAt.IsZero() && !stat.LastSuccessAt.Before(stat.LastFailAt) {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	ttl := pairFailureTTLForStat(stat, now)
	if ttl <= 0 {
		return false
	}
	return now.Sub(stat.LastFailAt) <= ttl
}

func shouldCooldownRecentFailures(stat recentCooldownStat, minAttempts int, maxSuccesses int, now time.Time) bool {
	lastFailureAt := stat.LastFailureAt
	if lastFailureAt.IsZero() {
		lastFailureAt = stat.LastAttemptAt
	}
	if lastFailureAt.IsZero() {
		return false
	}
	if !stat.LastSuccessAt.IsZero() && !stat.LastSuccessAt.Before(lastFailureAt) {
		return false
	}
	if stat.Successes > maxSuccesses && (stat.LastSuccessAt.IsZero() || stat.LastFailureAt.IsZero()) {
		return false
	}
	if stat.Attempts < minAttempts || stat.Failures < minAttempts {
		return false
	}
	return now.Sub(lastFailureAt) <= recentCooldownTTL
}

func shouldCooldownDistinctSourceFailures(stat recentCooldownStat, minSources int, maxSuccesses int, now time.Time) bool {
	if stat.DistinctSources < minSources {
		return false
	}
	if stat.Successes > maxSuccesses && (stat.LastSuccessAt.IsZero() || stat.LastFailureAt.IsZero()) {
		return false
	}
	lastFailureAt := stat.LastFailureAt
	if lastFailureAt.IsZero() {
		lastFailureAt = stat.LastAttemptAt
	}
	if lastFailureAt.IsZero() {
		return false
	}
	if !stat.LastSuccessAt.IsZero() && !stat.LastSuccessAt.Before(lastFailureAt) {
		return false
	}
	return now.Sub(lastFailureAt) <= recentCooldownTTL
}

func shouldCooldownTargetRecentFailures(attemptStat recentCooldownStat, noAttemptStat recentCooldownStat, failedStat recentCooldownStat, distinctSourceStat recentCooldownStat, now time.Time) bool {
	if shouldCooldownRecentFailures(attemptStat, targetCooldownMinAttempts, targetCooldownMaxSuccess, now) {
		return true
	}
	if shouldCooldownRecentFailures(noAttemptStat, targetNoAttemptCooldownMinFailures, targetNoAttemptCooldownMaxSuccesses, now) {
		return true
	}
	if shouldCooldownRecentFailures(failedStat, targetFailedCooldownMinFailures, targetFailedCooldownMaxSuccesses, now) {
		return true
	}
	return shouldCooldownDistinctSourceFailures(distinctSourceStat, targetDistinctSourceMinFailures, targetDistinctSourceMaxSuccesses, now)
}

// mppMaxShardSourceSharePct caps the fraction of an MPP plan's shards that
// can come from a single source. With maxShards=6 and 40% cap, the ceiling
// is ceil(2.4)=3 — so at least 2 distinct sources are needed to fill the
// plan when other viable sources exist. Falls back gracefully when only one
// source has capacity (the cap is relaxed as last resort).
const mppMaxShardSourceSharePct = 40

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

	// R3: cap per-source share to avoid the pathological case where a single
	// large source receives every shard. The cap is the ceiling so that even
	// small maxShards values (2, 3) still force diversity.
	maxShardsPerSource := (maxShards*mppMaxShardSourceSharePct + 99) / 100
	if maxShardsPerSource < 1 {
		maxShardsPerSource = 1
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
	shardsPerSource := make(map[int]int, len(sources))

	selectSource := func(desired int64, requireDesired bool, preferUnused bool, respectCap bool) int {
		bestIdx := -1
		bestCap := int64(0)
		for i, cap := range capacityLeft {
			if respectCap && shardsPerSource[i] >= maxShardsPerSource {
				continue
			}
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

	// pickSource probes the candidate space in priority order, first
	// respecting the per-source cap and only relaxing it as a last resort.
	// The respectCap=true pass tries the four (preferUnused × requireDesired)
	// combinations; if all return -1, the same four are retried with
	// respectCap=false so the plan still builds when only one source is
	// viable (e.g. tiny network, recovering from cascading failures).
	pickSource := func(desired int64) int {
		for _, respectCap := range []bool{true, false} {
			for _, preferUnused := range []bool{true, false} {
				for _, requireDesired := range []bool{true, false} {
					if idx := selectSource(desired, requireDesired, preferUnused, respectCap); idx >= 0 {
						return idx
					}
				}
			}
		}
		return -1
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

		chosenIdx := pickSource(desired)
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
		shardsPerSource[chosenIdx]++
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

func passesAutoTargetCostGate(setting channelSetting, expectedCostPpm int64, effectiveSpreadPpm int64) bool {
	return setting.AutoBypassCostGate || expectedCostPpm <= 0 || effectiveSpreadPpm > expectedCostPpm
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
  channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, auto_bypass_cost_gate, updated_at
)
values ($1,$2,$3,false,false,true,false,now())
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
	if err != nil {
		return
	}
	// AutoEnabled is the master switch — when off, no automated rebalance
	// path may create jobs. ManualRestartWatch is a sub-toggle that further
	// gates the manual-restart loop on top of the master.
	if !cfg.AutoEnabled || !cfg.ManualRestartWatch {
		return
	}
	if normalizeRebalanceSchedulerMode(cfg.SchedulerMode) == rebalanceSchedulerModeSovereignLive {
		s.recordManualRestartWatch(time.Now(), 0, map[string]int{"sovereign_live": 1})
		return
	}
	if s.lnd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	scanAt := time.Now()
	queuedCount := 0
	restartReasons := map[string]int{}
	noteManualSkip := func(reason string) {
		if reason != "" {
			restartReasons[reason]++
		}
	}
	defer func() {
		s.recordManualRestartWatch(scanAt, queuedCount, restartReasons)
	}()

	settings, _ := s.loadChannelSettings(ctx)
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx, cfg)
	_ = s.applyForwardDeltas(ctx, ledger)
	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)
	drainRateByChannel := s.fetchChannelDrainRate24h(ctx)
	targetCooldowns := s.loadRecentTargetCooldownSet(ctx, defaultRecentTargetCooldownWindows(scanAt))

	channels, err := s.listChannelsCached(ctx)
	if err != nil {
		noteManualSkip("start_error")
		return
	}
	s.reconcileNewChannelDefaults(ctx, channels, settings, exclusions)

	for _, ch := range channels {
		setting := settings[ch.ChannelID]
		if !setting.ManualRestartEnabled {
			continue
		}
		if shouldCooldownTargetRecentFailures(targetCooldowns.Recent[ch.ChannelID], targetCooldowns.NoAttempt[ch.ChannelID], targetCooldowns.Failed[ch.ChannelID], targetCooldowns.DistinctSource[ch.ChannelID], scanAt) {
			noteManualSkip("target_cooldown")
			continue
		}
		if s.isChannelBusy(ch.ChannelID) {
			noteManualSkip("channel_busy")
			continue
		}
		snapshot := s.buildChannelSnapshot(ctx, cfg, false, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], drainRateByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		if ok, reason := manualRestartWatchEligibility(snapshot, cfg); !ok {
			noteManualSkip(reason)
			continue
		}
		deficit := computeDeficitAmount(ch, snapshot.TargetOutboundPct)
		if deficit <= 0 {
			noteManualSkip("target_already_balanced")
			continue
		}
		minExecuteSat := effectiveMinExecuteSat(cfg)
		if minExecuteSat > 0 && deficit < minExecuteSat {
			noteManualSkip("below_execute_min")
			continue
		}
		_, err := s.startJob(ch.ChannelID, "manual", "auto-restart", 0, true)
		if err != nil {
			noteManualSkip(manualRestartStartErrorReason(err))
			continue
		}
		queuedCount++
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

	channels, err := s.listChannelsCached(ctx)
	if err != nil {
		return
	}
	// Phase 2: the effective config is the active profile composed with the live
	// node calibration. "custom" passes through unchanged (frozen — never
	// re-derived). Everything downstream runs on the effective config.
	cfg = effectiveRebalanceConfig(cfg, classifyRebalanceNode(channels))

	if err := s.ensureDailyBudget(ctx, cfg); err != nil && s.logger != nil {
		s.logger.Printf("rebalance budget ensure failed: %v", err)
	}

	settings, _ := s.loadChannelSettings(ctx)
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx, cfg)

	_ = s.applyForwardDeltas(ctx, ledger)

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
		limitedSkipped := limitRebalanceSkipDetails(skippedDetails, scanSkipDetailLimit)
		s.mu.Lock()
		s.lastScan = scanAt
		s.lastScanStatus = scanStatus
		s.lastScanDetail = scanDetail
		s.lastScanCandidates = scanCandidates
		s.lastScanRemainingBudgetSat = scanRemainingBudget
		s.lastScanReasons = copyReasonCounts(scanReasons)
		s.lastScanTopScoreSat = topScore
		s.lastScanProfitSkipped = profitSkipped
		s.lastScanQueued = queuedCount
		s.lastScanSkipped = limitedSkipped
		s.mu.Unlock()
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer persistCancel()
		if err := s.persistRebalanceScanSkips(persistCtx, scanAt, limitedSkipped); err != nil && s.logger != nil {
			s.logger.Printf("rebalance scan skips persist failed: %v", err)
		}
	}()

	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)
	drainRateByChannel := s.fetchChannelDrainRate24h(ctx)
	autofeeAdjustments := s.fetchRecentAutofeeAdjustments(ctx, scanAt, time.Duration(cfg.AutofeeSettlingWindowSec)*time.Second)
	lastAutoByTarget := s.loadLastAutoEnqueueTimes(ctx)
	targetCooldowns := s.loadRecentTargetCooldownSet(ctx, defaultRecentTargetCooldownWindows(scanAt))

	// Wave 2.2: merge in-memory lastAutoByTarget and read criticalActive under
	// a single mutex session so other goroutines cannot mutate them between
	// the two reads.
	s.mu.Lock()
	for channelID, last := range s.lastAutoByTarget {
		if existing, ok := lastAutoByTarget[channelID]; !ok || last.After(existing) {
			lastAutoByTarget[channelID] = last
		}
	}
	criticalActive := cfg.CriticalCycles > 0 && s.criticalMissCount >= cfg.CriticalCycles
	s.mu.Unlock()

	snapshots := make([]RebalanceChannel, 0, len(channels))
	for _, ch := range channels {
		setting := settings[ch.ChannelID]
		snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], drainRateByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		snapshots = append(snapshots, snapshot)
	}
	schedulerMode := normalizeRebalanceSchedulerMode(cfg.SchedulerMode)
	if isSovereignSchedulerMode(schedulerMode) {
		sovereignTargetIDs := make([]uint64, 0, len(snapshots))
		includeManualRestartTargets := cfg.SovereignCandidateScope == rebalanceSovereignScopeAutoAndManualRestart
		for _, snapshot := range snapshots {
			setting := settings[snapshot.ChannelID]
			if !snapshot.EligibleAsTarget {
				continue
			}
			if setting.AutoEnabled || (includeManualRestartTargets && setting.ManualRestartEnabled) {
				sovereignTargetIDs = append(sovereignTargetIDs, snapshot.ChannelID)
			}
		}
		sovereignPairStats := s.loadPairStatsSummaryForTargets(ctx, sovereignTargetIDs, scanAt)
		sovereignRecentStats := s.loadRecentSovereignTargetStats(ctx, cfg, sovereignTargetIDs, scanAt)
		mergeRecentSovereignTargetStats(sovereignPairStats, sovereignRecentStats)
		sovereignStructuralCooldowns := s.loadSovereignTargetStructuralCooldowns(ctx, sovereignTargetIDs, cfg, scanAt)
		sovereignUnsoldLiquidity := s.loadSovereignUnsoldPaidLiquidityStats(ctx, cfg, sovereignTargetIDs, scanAt)
		sovereignPlan := buildAndOrderRebalanceCandidates(rebalanceAutoScanCandidateInput{
			Channels:                      snapshots,
			Settings:                      settings,
			Cfg:                           cfg,
			ScanAt:                        scanAt,
			LastAutoByTarget:              lastAutoByTarget,
			IncludeManualRestartTargets:   includeManualRestartTargets,
			DisableCooldownProbe:          true,
			SovereignRanking:              true,
			PairStatsByTarget:             sovereignPairStats,
			SovereignStructuralCooldowns:  sovereignStructuralCooldowns,
			SovereignUnsoldLiquidity:      sovereignUnsoldLiquidity,
			TargetCooldowns:               targetCooldowns.Recent,
			TargetNoAttemptCooldowns:      targetCooldowns.NoAttempt,
			TargetFailedCooldowns:         targetCooldowns.Failed,
			TargetDistinctSourceCooldowns: targetCooldowns.DistinctSource,
			AutofeeRecentAdjustments:      autofeeAdjustments,
		})
		// AutoTarget runs together with the round's candidates (opt-in): it can
		// raise the target of the strong sellers among them (supply-limited by
		// construction) and lower channels that stopped selling. Reuses the
		// snapshots, pair stats and structural cooldowns already loaded above.
		if cfg.AutoTargetEnabled {
			s.evaluateAutoTarget(ctx, cfg, scanAt, snapshots, settings, sovereignPlan.Candidates, sovereignPairStats, sovereignStructuralCooldowns)
		}
		sovereignResult := s.executeSovereignAutopilot(ctx, cfg, settings, sovereignPlan, scanAt, schedulerMode == rebalanceSchedulerModeSovereignLive)
		s.recordSovereignAutopilot(ctx, scanAt, schedulerMode, sovereignResult)
		if schedulerMode == rebalanceSchedulerModeSovereignLive {
			scanStatus = sovereignResult.Status
			scanDetail = sovereignResult.Detail
			scanCandidates = sovereignResult.Candidates
			scanRemainingBudget = sovereignResult.BudgetRemainingSat
			scanReasons = copyReasonCounts(sovereignResult.SkipReasons)
			topScore = sovereignPlan.TopScore
			queuedCount = sovereignResult.Selected
			return
		}
	}
	candidatePlan := buildAndOrderRebalanceCandidates(rebalanceAutoScanCandidateInput{
		Channels:                      snapshots,
		Settings:                      settings,
		Cfg:                           cfg,
		ScanAt:                        scanAt,
		LastAutoByTarget:              lastAutoByTarget,
		TargetCooldowns:               targetCooldowns.Recent,
		TargetNoAttemptCooldowns:      targetCooldowns.NoAttempt,
		TargetFailedCooldowns:         targetCooldowns.Failed,
		TargetDistinctSourceCooldowns: targetCooldowns.DistinctSource,
		AutofeeRecentAdjustments:      autofeeAdjustments,
	})
	candidates := candidatePlan.Candidates
	eligibleSources := candidatePlan.EligibleSources
	totalAvailable := candidatePlan.TotalAvailable
	roiSkipped := candidatePlan.ROISkipped
	belowExecuteMinSkipped := candidatePlan.BelowExecuteMinSkipped
	targetCooldownSkipped := candidatePlan.TargetCooldownSkipped
	profitSkipped = candidatePlan.ProfitSkipped
	topScore = candidatePlan.TopScore
	skippedDetails = append(skippedDetails, candidatePlan.SkippedDetails...)

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
		if targetCooldownSkipped > 0 {
			scanReasons["target_cooldown"] = targetCooldownSkipped
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

	budget, spentAuto, _, spentTotal := s.getDailyBudget(ctx)
	manualReserveSat := computeManualReserveSat(cfg, budget)
	remaining := computeRemainingForAuto(budget, spentAuto, spentTotal, manualReserveSat, cfg.BudgetAutoOnly)
	budgetEnforced := shouldEnforceAutoBudget(cfg)
	if budgetEnforced && remaining == 0 {
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
	addSkippedDetail := func(target rebalanceTarget, reason string) {
		if reason == "" {
			return
		}
		targetAmount := target.OriginalAmountSat
		if targetAmount <= 0 {
			targetAmount = target.Channel.TargetAmountSat
		}
		skippedDetails = append(skippedDetails, RebalanceSkipDetail{
			ChannelID:         target.Channel.ChannelID,
			ChannelPoint:      target.Channel.ChannelPoint,
			PeerAlias:         target.Channel.PeerAlias,
			TargetOutboundPct: target.Channel.TargetOutboundPct,
			TargetAmountSat:   targetAmount,
			ExpectedGainSat:   target.ExpectedGainSat,
			EstimatedCostSat:  target.EstimatedCostSat,
			ExpectedROI:       target.ExpectedROI,
			ExpectedROIValid:  target.ExpectedROIValid,
			Reason:            reason,
		})
	}
	priorityWorkActiveAtScan := s.hasActivePriorityRebalanceWork(ctx, 0)
	queuedPriorityCount := 0
	queuedCooldownProbeCount := 0
	for i := 0; i < candidatePlan.RecentSkipped; i++ {
		noteSkip("recently_attempted")
	}

	for _, target := range candidates {
		if budgetEnforced && remaining <= 0 {
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
		reason := ""
		if target.CooldownProbe {
			amountOverride = target.ProbeAmountSat
			targetAmount = target.ProbeAmountSat
			estimatedCost = estimateMaxCost(targetAmount, targetPolicy, targetCfg)
			reason = targetCooldownProbeReason
			noteSkip("target_cooldown_probe")
		}
		if budgetEnforced && estimatedCost > remaining {
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
		if target.CooldownProbe {
			switch {
			case priorityWorkActiveAtScan || queuedPriorityCount > 0:
				noteSkip(targetCooldownProbeDeferredReason)
				addSkippedDetail(target, targetCooldownProbeDeferredReason)
				continue
			case queuedCooldownProbeCount >= cooldownProbeWorkerSlots || !s.cooldownProbeSlotAvailable():
				noteSkip(targetCooldownProbeBusyReason)
				addSkippedDetail(target, targetCooldownProbeBusyReason)
				continue
			}
		}
		_, err = s.startJob(target.Channel.ChannelID, "auto", reason, amountOverride, false)
		if err == nil {
			s.mu.Lock()
			s.lastAutoByTarget[target.Channel.ChannelID] = scanAt
			s.mu.Unlock()
			if budgetEnforced {
				remaining -= estimatedCost
			}
			if target.CooldownProbe {
				queuedCooldownProbeCount++
			} else {
				queuedPriorityCount++
			}
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
		scanReasons = copyReasonCounts(skipReasons)
	} else if remaining > 0 || !budgetEnforced {
		budgetBlocked := skipReasons["budget_too_low"] + skipReasons["budget_below_min"]
		if budgetEnforced && budgetBlocked == len(candidates) {
			scanStatus = "budget_insufficient"
		} else {
			scanStatus = "no_queue"
		}
		scanCandidates = len(candidates)
		scanRemainingBudget = remaining
		scanReasons = copyReasonCounts(skipReasons)
		scanDetail = buildScanDetail(skipReasons, remaining, len(candidates), queuedCount)
	}

	if s.logger != nil {
		s.logger.Printf("rebalance scan: candidates=%d queued=%d profit_skipped=%d roi_skipped=%d top_score=%d sats", len(candidates), queuedCount, profitSkipped, roiSkipped, topScore)
	}
}

func limitRebalanceSkipDetails(details []RebalanceSkipDetail, limit int) []RebalanceSkipDetail {
	if len(details) == 0 || limit <= 0 {
		return []RebalanceSkipDetail{}
	}
	if len(details) > limit {
		details = details[:limit]
	}
	out := make([]RebalanceSkipDetail, len(details))
	copy(out, details)
	return out
}

func copyReasonCounts(reasons map[string]int) map[string]int {
	if len(reasons) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(reasons))
	for key, value := range reasons {
		out[key] = value
	}
	return out
}

type sovereignAutopilotResult struct {
	Candidates         int
	Selected           int
	ExpectedProfitSat  int64
	BudgetRemainingSat int64
	Decisions          []RebalanceSovereignDecision
	SkipReasons        map[string]int
	Status             string
	Detail             string
}

func (s *RebalanceService) executeSovereignAutopilot(ctx context.Context, cfg RebalanceConfig, settings map[uint64]channelSetting, plan rebalanceAutoScanCandidatePlan, scanAt time.Time, live bool) sovereignAutopilotResult {
	result := sovereignAutopilotResult{
		Candidates:  len(plan.Candidates),
		Decisions:   []RebalanceSovereignDecision{},
		SkipReasons: copyReasonCounts(plan.SkipReasons),
		Status:      "sovereign_shadow",
	}
	if live {
		result.Status = "sovereign_live"
	}
	noteSkip := func(reason string) {
		if reason != "" {
			result.SkipReasons[reason]++
		}
	}

	budget, _, _, spentTotal := s.getDailyBudget(ctx)
	remaining := computeRemainingTotalBudget(budget, spentTotal)
	budgetEnforced := !cfg.BudgetUnlimited
	result.BudgetRemainingSat = remaining
	if budgetEnforced && remaining == 0 {
		result.Status = "budget_exhausted"
		noteSkip("budget_too_low")
	}

	maxJobs := cfg.SovereignMaxJobsPerCycle
	if maxJobs <= 0 {
		maxJobs = defaultRebalanceConfig().SovereignMaxJobsPerCycle
	}
	if maxJobs > cfg.MaxConcurrent && cfg.MaxConcurrent > 0 {
		maxJobs = cfg.MaxConcurrent
	}
	appendDecision := func(decision RebalanceSovereignDecision) {
		if decision.Selected || len(result.Decisions) < scanSkipDetailLimit {
			result.Decisions = append(result.Decisions, decision)
		}
	}

	// Epsilon-greedy exploration: when enabled, reserve a fraction of slots
	// for randomly chosen low-score candidates. They bypass the score sort
	// but still go through every gate (cooldown, structural, ROI, etc).
	// Targets in exploration burnout (R5) are excluded from receiving the
	// ExplorationSlot mark so we stop burning cycles on dead-end channels.
	candidates := plan.Candidates
	if cfg.SovereignExplorationSlotPct > 0 && maxJobs > 1 {
		burnoutFn := func(channelID uint64) bool {
			return s.isInExplorationBurnout(channelID, scanAt)
		}
		// Targets that already reached the structural cooldown threshold must
		// not receive the exploration mark — otherwise the M3 scarcity bypass
		// lets them skip target_structural_cooldown indefinitely.
		structuralFn := func(target rebalanceTarget) bool {
			return shouldSkipSovereignTargetStructuralCooldown(target.StructuralCooldown, cfg, scanAt)
		}
		candidates = injectSovereignExplorationSlots(candidates, maxJobs, cfg.SovereignExplorationSlotPct, burnoutFn, structuralFn)
	}

	for _, target := range candidates {
		targetCfg := effectiveConfigForTarget(cfg, settings[target.Channel.ChannelID])
		targetPolicy := lndclient.ChannelPolicySnapshot{
			FeeRatePpm:  target.Channel.OutgoingFeePpm,
			BaseFeeMsat: target.Channel.OutgoingBaseMsat,
		}
		targetAmount := target.Channel.TargetAmountSat
		estimatedCost := target.EstimatedCostSat
		if estimatedCost <= 0 {
			estimatedCost = estimateMaxCost(targetAmount, targetPolicy, targetCfg)
		}
		budgetCost := target.BudgetCostSat
		if budgetCost <= 0 {
			budgetCost = estimateMaxCost(targetAmount, targetPolicy, targetCfg)
		}
		if budgetCost <= 0 {
			budgetCost = estimatedCost
		}
		historicalSuccessRate, historicalAttempts := sovereignHistoricalSuccessRate(target.PairStats)
		expectedProfit := target.ExpectedGainSat - estimatedCost
		decision := RebalanceSovereignDecision{
			ChannelID:                   target.Channel.ChannelID,
			ChannelPoint:                target.Channel.ChannelPoint,
			PeerAlias:                   target.Channel.PeerAlias,
			Score:                       target.Score,
			AmountSat:                   targetAmount,
			ExpectedGainSat:             target.ExpectedGainSat,
			EstimatedCostSat:            estimatedCost,
			ExpectedProfitSat:           expectedProfit,
			ExpectedROI:                 target.ExpectedROI,
			ExpectedROIValid:            target.ExpectedROIValid,
			BudgetCostSat:               budgetCost,
			HistoricalAttempts:          historicalAttempts,
			HistoricalSuccesses:         target.PairStats.Successes,
			HistoricalSuccessRate:       historicalSuccessRate,
			RecentStructuralFailures:    target.PairStats.RecentStructuralFailures,
			RecentRebalanceSentSat:      target.UnsoldLiquidity.SentSat,
			RecentRebalanceTargetSat:    target.UnsoldLiquidity.TargetAmountSat,
			TargetClass:                 target.TargetClass,
			AttributionWindowHours:      sovereignAttributionWindowHoursForConfig(cfg),
			SlowSellerWindowHours:       sovereignSlowSellerWindowHoursForConfig(cfg),
			RecentForward24hSat:         target.PairStats.RecentForward24hAmountSat,
			RecentForwardFee24hSat:      target.PairStats.RecentForward24hFeeSat,
			RecentRealizedNet24hSat:     target.PairStats.RecentRealizedNet24hSat,
			RecentForwardedAfterSat:     target.UnsoldLiquidity.ForwardAmountSat,
			RecentForwardFeeAfterSat:    target.UnsoldLiquidity.ForwardFeeSat,
			RecentForwardSlowSat:        target.PairStats.RecentForwardSlowAmountSat,
			RecentForwardFeeSlowSat:     target.PairStats.RecentForwardSlowFeeSat,
			RecentRealizedNetSlowSat:    target.PairStats.RecentRealizedNetSlowSat,
			RecentRealizedSentSat:       target.PairStats.RecentSentSat,
			RecentRealizedForwardSat:    target.PairStats.RecentForwardAmountSat,
			RecentRealizedFeeSat:        target.PairStats.RecentForwardFeeSat,
			RecentRealizedNetSat:        target.PairStats.RecentRealizedNetSat,
			SuccessMultiplier:           target.SuccessMultiplier,
			ROIMultiplier:               target.ROIMultiplier,
			BudgetEfficiencyMultiplier:  target.BudgetEfficiencyMultiplier,
			UnsoldLiquidityMultiplier:   target.UnsoldLiquidityMultiplier,
			RealizedEconomicsMultiplier: target.RealizedEconomicsMultiplier,
			ExplorationSlot:             target.ExplorationSlot,
		}
		if target.StructuralCooldown.LastFailureAttempts > decision.RecentStructuralFailures {
			decision.RecentStructuralFailures = target.StructuralCooldown.LastFailureAttempts
		}

		// ExplorationSlot bypasses every empirical-history gate (including the
		// budget-efficiency hard skip) so that a chronically deprioritized
		// channel can be retried in spite of past failures or weak observed
		// profit/cost ratios. Only hard operational gates (cycle limit, profit
		// floor, channel busy, budget refit math) still apply. CooldownProbe
		// entries are a distinct mechanism and remain non-sovereign regardless.
		switch {
		case target.CooldownProbe:
			decision.Reason = "cooldown_probe_not_sovereign"
			noteSkip(decision.Reason)
		case !target.ExplorationSlot && shouldSkipSovereignTargetStructuralCooldown(target.StructuralCooldown, cfg, scanAt):
			decision.Reason = sovereignTargetStructuralCooldownReason
			noteSkip(decision.Reason)
		case !target.ExplorationSlot && shouldSkipSovereignUnsoldPaidLiquidity(target.UnsoldLiquidity, cfg, scanAt):
			decision.Reason = sovereignUnsoldPaidLiquidityReason
			noteSkip(decision.Reason)
		case !target.ExplorationSlot && shouldSkipSovereignRouteDeadOpportunity(target.PairStats, expectedProfit, budgetCost, plan.EligibleSources, cfg):
			decision.Reason = sovereignRouteDeadOpportunityReason
			noteSkip(decision.Reason)
		case !target.ExplorationSlot && shouldSkipSovereignLowSuccessOpportunity(target.PairStats, expectedProfit, estimatedCost, budgetCost, cfg, scanAt):
			decision.Reason = sovereignLowSuccessOpportunityReason
			noteSkip(decision.Reason)
		case maxJobs > 0 && result.Selected >= maxJobs:
			decision.Reason = "cycle_limit"
			noteSkip(decision.Reason)
		// 2026-05-29: exploration_slot bypass para expected_profit_below_min,
		// guardado por profit >= 0 pra não autorizar jobs em loss garantido.
		// Sem isso, o knob sovereign_exploration_slot_pct era placebo — os
		// candidatos low-score que ele queria privilegiar batiam neste gate
		// antes da lógica de exploration ter efeito visível. Resultado: pool
		// efetivo travado nos top-score targets, que se queimam em
		// target_structural_cooldown e somem do scan por 6h.
		case !(target.ExplorationSlot && expectedProfit >= 0) && expectedProfit < cfg.SovereignMinExpectedProfitSat:
			decision.Reason = "expected_profit_below_min"
			noteSkip(decision.Reason)
		case !target.ExplorationSlot && shouldHardSkipSovereignBudgetEfficiencyOpportunity(target.PairStats, expectedProfit, budgetCost, cfg):
			decision.Reason = sovereignBudgetEfficiencyOpportunityReason
			noteSkip(decision.Reason)
		case s.isChannelBusy(target.Channel.ChannelID):
			decision.Reason = "channel_busy"
			noteSkip(decision.Reason)
		default:
			amountOverride := int64(0)
			if budgetEnforced && budgetCost > remaining {
				maxFeeMsat, err := calcFeeLimitMsat(targetAmount*1000, targetPolicy, nil, targetCfg)
				if err != nil || maxFeeMsat <= 0 {
					decision.Reason = "fee_cap_zero"
					noteSkip(decision.Reason)
					appendDecision(decision)
					continue
				}
				maxFeePpm := feeMsatToPpm(maxFeeMsat, targetAmount)
				fitAmount := int64(0)
				if maxFeePpm > 0 {
					fitAmount = (remaining * 1_000_000) / maxFeePpm
				}
				if fitAmount > targetAmount {
					fitAmount = targetAmount
				}
				minExecuteSat := effectiveMinExecuteSat(targetCfg)
				if fitAmount <= 0 || (minExecuteSat > 0 && fitAmount < minExecuteSat) {
					decision.Reason = "budget_below_min"
					noteSkip(decision.Reason)
					appendDecision(decision)
					continue
				}
				amountOverride = fitAmount
				targetAmount = fitAmount
				budgetCost = estimateMaxCost(targetAmount, targetPolicy, targetCfg)
				if budgetCost > remaining {
					decision.Reason = "budget_too_low"
					noteSkip(decision.Reason)
					appendDecision(decision)
					continue
				}
				decision.AmountSat = targetAmount
				resizedEstimatedCost := estimateSovereignTargetCost(targetAmount, target.Channel.RebalanceCost7dPpm, target.Channel.RebalanceAmount7dSat, budgetCost, targetCfg)
				decision.EstimatedCostSat = resizedEstimatedCost
				decision.BudgetCostSat = budgetCost
				decision.ExpectedGainSat = estimateTargetGainForConfig(targetCfg, target.Channel, targetAmount)
				decision.ExpectedProfitSat = decision.ExpectedGainSat - decision.EstimatedCostSat
				if !(target.ExplorationSlot && decision.ExpectedProfitSat >= 0) && decision.ExpectedProfitSat < cfg.SovereignMinExpectedProfitSat {
					decision.Reason = "expected_profit_below_min"
					noteSkip(decision.Reason)
					appendDecision(decision)
					continue
				}
				// Refit reapplies the empirical-history gates because the
				// resized amount changes expected_profit and budget_cost. Keep
				// the same ExplorationSlot bypass policy used by the outer
				// switch so an exploration candidate is not silently re-gated
				// after a budget-driven resize.
				if !target.ExplorationSlot && shouldHardSkipSovereignBudgetEfficiencyOpportunity(target.PairStats, decision.ExpectedProfitSat, decision.BudgetCostSat, cfg) {
					decision.Reason = sovereignBudgetEfficiencyOpportunityReason
					noteSkip(decision.Reason)
					appendDecision(decision)
					continue
				}
				if !target.ExplorationSlot && shouldSkipSovereignRouteDeadOpportunity(target.PairStats, decision.ExpectedProfitSat, decision.BudgetCostSat, plan.EligibleSources, cfg) {
					decision.Reason = sovereignRouteDeadOpportunityReason
					noteSkip(decision.Reason)
					appendDecision(decision)
					continue
				}
				if !target.ExplorationSlot && shouldSkipSovereignLowSuccessOpportunity(target.PairStats, decision.ExpectedProfitSat, decision.EstimatedCostSat, decision.BudgetCostSat, cfg, scanAt) {
					decision.Reason = sovereignLowSuccessOpportunityReason
					noteSkip(decision.Reason)
					appendDecision(decision)
					continue
				}
			}
			if live {
				economics := rebalanceJobEconomics{
					ExpectedGainSat:   decision.ExpectedGainSat,
					EstimatedCostSat:  decision.EstimatedCostSat,
					ExpectedProfitSat: decision.ExpectedProfitSat,
					BudgetCostSat:     decision.BudgetCostSat,
					Score:             decision.Score,
				}
				jobID, err := s.startJobWithEconomics(target.Channel.ChannelID, "auto", rebalanceSovereignReason, amountOverride, false, false, economics)
				if err != nil {
					decision.Reason = autoStartErrorReason(err)
					noteSkip(decision.Reason)
					appendDecision(decision)
					continue
				}
				// R5: mark exploration-slot jobs so finishJob can record the
				// outcome to update burnout state.
				if target.ExplorationSlot {
					s.markExplorationJob(jobID, target.Channel.ChannelID)
				}
				s.mu.Lock()
				s.lastAutoByTarget[target.Channel.ChannelID] = scanAt
				s.mu.Unlock()
			}
			decision.Selected = true
			if live {
				decision.Reason = "queued"
			} else {
				decision.Reason = "would_queue"
			}
			result.Selected++
			result.ExpectedProfitSat += decision.ExpectedProfitSat
			if budgetEnforced {
				remaining -= budgetCost
				if remaining < 0 {
					remaining = 0
				}
				result.BudgetRemainingSat = remaining
			}
		}
		appendDecision(decision)
	}

	if result.Candidates == 0 {
		result.Status = "no_candidates"
	} else if result.Status != "budget_exhausted" {
		if live && result.Selected > 0 {
			result.Status = "queued"
		} else if live {
			result.Status = "no_queue"
		}
	}
	result.Detail = buildScanDetail(result.SkipReasons, result.BudgetRemainingSat, result.Candidates, result.Selected)
	return result
}

func (s *RebalanceService) recordSovereignAutopilot(ctx context.Context, scanAt time.Time, mode string, result sovereignAutopilotResult) {
	s.mu.Lock()
	s.lastSovereignDecisionAt = scanAt
	s.lastSovereignMode = normalizeRebalanceSchedulerMode(mode)
	s.lastSovereignCandidates = result.Candidates
	s.lastSovereignSelected = result.Selected
	s.lastSovereignExpectedProfitSat = result.ExpectedProfitSat
	s.lastSovereignBudgetRemainingSat = result.BudgetRemainingSat
	s.lastSovereignDecisions = append([]RebalanceSovereignDecision(nil), result.Decisions...)
	s.mu.Unlock()
	if err := s.persistSovereignAutopilotHistory(ctx, scanAt, mode, result); err != nil && s.logger != nil {
		s.logger.Printf("rebalance sovereign history persist failed: %v", err)
	}
}

func (s *RebalanceService) recordManualRestartWatch(scanAt time.Time, queued int, reasons map[string]int) {
	s.mu.Lock()
	s.lastManualRestartAt = scanAt
	s.lastManualRestartQueued = queued
	s.lastManualRestartReasons = copyReasonCounts(reasons)
	s.mu.Unlock()
}

func (s *RebalanceService) persistRebalanceScanSkips(ctx context.Context, scanAt time.Time, details []RebalanceSkipDetail) error {
	if s.db == nil || scanAt.IsZero() || len(details) == 0 {
		return nil
	}
	_, _ = s.db.Exec(ctx, `delete from rebalance_scan_skips where scan_at < now() - interval '14 days'`)
	batch := &pgx.Batch{}
	for _, detail := range details {
		batch.Queue(`
insert into rebalance_scan_skips (
  scan_at, channel_id, channel_point, peer_alias, target_outbound_pct, target_amount_sat,
  expected_gain_sat, estimated_cost_sat, expected_roi, expected_roi_valid, reason
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
`, scanAt.UTC(), int64(detail.ChannelID), detail.ChannelPoint, detail.PeerAlias, detail.TargetOutboundPct, detail.TargetAmountSat, detail.ExpectedGainSat, detail.EstimatedCostSat, detail.ExpectedROI, detail.ExpectedROIValid, detail.Reason)
	}
	br := s.db.SendBatch(ctx, batch)
	defer br.Close()
	for range details {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *RebalanceService) persistSovereignAutopilotHistory(ctx context.Context, scanAt time.Time, mode string, result sovereignAutopilotResult) error {
	if s.db == nil || scanAt.IsZero() {
		return nil
	}
	skipReasonsRaw, err := json.Marshal(result.SkipReasons)
	if err != nil {
		return err
	}
	decisionsRaw, err := json.Marshal(result.Decisions)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(ctx, `delete from rebalance_sovereign_history where scan_at < now() - interval '48 hours'`)
	_, err = s.db.Exec(ctx, `
insert into rebalance_sovereign_history (
  scan_at, mode, status, candidates, selected, expected_profit_sat,
  budget_remaining_sat, skip_reasons, decisions, detail
) values ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10)
`, scanAt.UTC(), normalizeRebalanceSchedulerMode(mode), result.Status, result.Candidates, result.Selected, result.ExpectedProfitSat, result.BudgetRemainingSat, string(skipReasonsRaw), string(decisionsRaw), result.Detail)
	return err
}

func (s *RebalanceService) loadRebalanceScanSkips(ctx context.Context, scanAt time.Time, limit int) []RebalanceSkipDetail {
	if s.db == nil || scanAt.IsZero() || limit <= 0 {
		return []RebalanceSkipDetail{}
	}
	rows, err := s.db.Query(ctx, `
select channel_id, channel_point, peer_alias, target_outbound_pct, target_amount_sat,
  expected_gain_sat, estimated_cost_sat, expected_roi, expected_roi_valid, reason
from rebalance_scan_skips
where scan_at=$1
order by id
limit $2
`, scanAt.UTC(), limit)
	if err != nil {
		return []RebalanceSkipDetail{}
	}
	defer rows.Close()
	out := []RebalanceSkipDetail{}
	for rows.Next() {
		var detail RebalanceSkipDetail
		var channelID int64
		if err := rows.Scan(
			&channelID,
			&detail.ChannelPoint,
			&detail.PeerAlias,
			&detail.TargetOutboundPct,
			&detail.TargetAmountSat,
			&detail.ExpectedGainSat,
			&detail.EstimatedCostSat,
			&detail.ExpectedROI,
			&detail.ExpectedROIValid,
			&detail.Reason,
		); err != nil {
			return out
		}
		if channelID > 0 {
			detail.ChannelID = uint64(channelID)
		}
		out = append(out, detail)
	}
	return out
}

func (s *RebalanceService) SovereignHistory(ctx context.Context, limit int, includeDecisions bool) ([]RebalanceSovereignHistory, error) {
	if limit <= 0 {
		limit = 288
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.loadSovereignAutopilotHistory(ctx, time.Now().Add(-24*time.Hour), limit, includeDecisions), nil
}

func (s *RebalanceService) loadSovereignAutopilotHistory(ctx context.Context, since time.Time, limit int, includeDecisions bool) []RebalanceSovereignHistory {
	if s.db == nil || limit <= 0 {
		return []RebalanceSovereignHistory{}
	}
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour)
	}
	rows, err := s.db.Query(ctx, `
select id, scan_at, mode, status, candidates, selected, expected_profit_sat,
  budget_remaining_sat, skip_reasons::text, decisions::text, detail
from rebalance_sovereign_history
where scan_at >= $1
order by scan_at desc
limit $2
`, since.UTC(), limit)
	if err != nil {
		return []RebalanceSovereignHistory{}
	}
	defer rows.Close()
	out := []RebalanceSovereignHistory{}
	for rows.Next() {
		var entry RebalanceSovereignHistory
		var scanAt time.Time
		var skipReasonsRaw string
		var decisionsRaw string
		if err := rows.Scan(
			&entry.ID,
			&scanAt,
			&entry.Mode,
			&entry.Status,
			&entry.Candidates,
			&entry.Selected,
			&entry.ExpectedProfitSat,
			&entry.BudgetRemainingSat,
			&skipReasonsRaw,
			&decisionsRaw,
			&entry.Detail,
		); err != nil {
			return out
		}
		entry.ScanAt = scanAt.UTC().Format(time.RFC3339)
		if err := json.Unmarshal([]byte(skipReasonsRaw), &entry.SkipReasons); err != nil || entry.SkipReasons == nil {
			entry.SkipReasons = map[string]int{}
		}
		var decisions []RebalanceSovereignDecision
		if err := json.Unmarshal([]byte(decisionsRaw), &decisions); err == nil {
			selected := make([]RebalanceSovereignDecision, 0, entry.Selected)
			for _, decision := range decisions {
				if decision.Selected {
					selected = append(selected, decision)
				}
			}
			entry.SelectedDecisions = selected
			if includeDecisions {
				entry.Decisions = decisions
			}
		}
		out = append(out, entry)
	}
	return out
}

type rebalanceTarget struct {
	Channel            RebalanceChannel
	ExpectedGainSat    int64
	EstimatedCostSat   int64
	BudgetCostSat      int64
	ExpectedROI        float64
	ExpectedROIValid   bool
	Score              int64
	LastAutoAt         time.Time
	PairStats          rebalanceTargetPairStats
	StructuralCooldown sovereignTargetStructuralCooldownStat
	CooldownProbe      bool
	ProbeAmountSat     int64
	OriginalAmountSat  int64
	// Wave 6.1b: set to true when score was dampened because AutoFee
	// adjusted this channel inside the settling window. AutofeeAdjustedAt
	// holds the timestamp of the most recent autofee adjustment.
	AutofeeDampened   bool
	AutofeeAdjustedAt time.Time
	TargetClass       string
	// Risk-adjusted score multiplier breakdown — populated by
	// applySovereignRiskAdjustedScores and surfaced on the decision struct.
	SuccessMultiplier           float64
	ROIMultiplier               float64
	BudgetEfficiencyMultiplier  float64
	UnsoldLiquidityMultiplier   float64
	RealizedEconomicsMultiplier float64
	UnsoldLiquidity             sovereignUnsoldLiquidityStat
	// ExplorationSlot marks targets promoted via the epsilon-greedy
	// exploration mechanism. These bypassed the score sort to give a
	// historically-deprioritized candidate a chance to be tried.
	ExplorationSlot bool
}

type rebalanceAutoScanCandidateInput struct {
	Channels                      []RebalanceChannel
	Settings                      map[uint64]channelSetting
	Cfg                           RebalanceConfig
	ScanAt                        time.Time
	LastAutoByTarget              map[uint64]time.Time
	IncludeManualRestartTargets   bool
	DisableCooldownProbe          bool
	SovereignRanking              bool
	PairStatsByTarget             map[uint64]rebalanceTargetPairStats
	SovereignStructuralCooldowns  map[uint64]sovereignTargetStructuralCooldownStat
	SovereignUnsoldLiquidity      map[uint64]sovereignUnsoldLiquidityStat
	TargetCooldowns               map[uint64]recentCooldownStat
	TargetNoAttemptCooldowns      map[uint64]recentCooldownStat
	TargetFailedCooldowns         map[uint64]recentCooldownStat
	TargetDistinctSourceCooldowns map[uint64]recentCooldownStat
	// Wave 6.1b: most recent AutoFee outbound-fee adjustment per channel.
	// Targets ajustados dentro de cfg.AutofeeSettlingWindowSec têm o score
	// multiplicado por cfg.AutofeeSettlingMultiplier (despriorização, não skip).
	AutofeeRecentAdjustments map[uint64]time.Time
}

type rebalanceAutoScanCandidatePlan struct {
	Candidates             []rebalanceTarget
	SkippedDetails         []RebalanceSkipDetail
	SkipReasons            map[string]int
	EligibleSources        int
	TotalAvailable         int64
	ProfitSkipped          int
	ROISkipped             int
	BelowExecuteMinSkipped int
	TargetCooldownSkipped  int
	RecentSkipped          int
	// Wave 6.1b: count of candidates whose score was dampened because
	// AutoFee adjusted them inside the settling window. Não é skip.
	AutofeeDampened int
	TopScore        int64
	TopScoreSet     bool
}

func buildAndOrderRebalanceCandidates(input rebalanceAutoScanCandidateInput) rebalanceAutoScanCandidatePlan {
	plan := rebalanceAutoScanCandidatePlan{
		Candidates:     []rebalanceTarget{},
		SkippedDetails: []RebalanceSkipDetail{},
		SkipReasons:    map[string]int{},
	}
	noteSkip := func(reason string) {
		if reason != "" {
			plan.SkipReasons[reason]++
		}
	}

	for _, snapshot := range input.Channels {
		if snapshot.EligibleAsSource {
			plan.EligibleSources++
			plan.TotalAvailable += snapshot.MaxSourceSat
		}

		setting := input.Settings[snapshot.ChannelID]
		inScope := setting.AutoEnabled || (input.IncludeManualRestartTargets && setting.ManualRestartEnabled)
		if !inScope || !snapshot.EligibleAsTarget {
			continue
		}

		targetCfg := effectiveConfigForTarget(input.Cfg, setting)
		targetAmount := snapshot.TargetAmountSat
		minExecuteSat := effectiveMinExecuteSat(targetCfg)
		targetAttemptCooldown := input.TargetCooldowns[snapshot.ChannelID]
		targetNoAttemptCooldown := input.TargetNoAttemptCooldowns[snapshot.ChannelID]
		targetFailedCooldown := input.TargetFailedCooldowns[snapshot.ChannelID]
		targetDistinctSourceCooldown := input.TargetDistinctSourceCooldowns[snapshot.ChannelID]
		if shouldCooldownTargetRecentFailures(
			targetAttemptCooldown,
			targetNoAttemptCooldown,
			targetFailedCooldown,
			targetDistinctSourceCooldown,
			input.ScanAt,
		) {
			if !input.DisableCooldownProbe && input.Cfg.CooldownProbeEnabled {
				probeInterval := targetCooldownProbeIntervalForStats(targetAttemptCooldown, targetNoAttemptCooldown, targetFailedCooldown, targetDistinctSourceCooldown)
				if shouldRunTargetCooldownProbeAfter(input.LastAutoByTarget[snapshot.ChannelID], input.ScanAt, probeInterval) {
					probeAmount := rebalanceCooldownProbeAmount(targetAmount, targetCfg)
					if probeAmount > 0 && (!targetCfg.MinSplitEnabled || minExecuteSat <= 0 || probeAmount >= minExecuteSat) {
						probeSnapshot := snapshot
						probeSnapshot.TargetAmountSat = probeAmount
						plan.Candidates = append(plan.Candidates, rebalanceTarget{
							Channel:           probeSnapshot,
							ExpectedGainSat:   0,
							EstimatedCostSat:  0,
							ExpectedROI:       0,
							ExpectedROIValid:  false,
							Score:             -1,
							LastAutoAt:        input.LastAutoByTarget[snapshot.ChannelID],
							CooldownProbe:     true,
							ProbeAmountSat:    probeAmount,
							OriginalAmountSat: targetAmount,
						})
						plan.TargetCooldownSkipped++
						noteSkip("target_cooldown")
						continue
					}
				} else if probeInterval > targetCooldownProbeInterval {
					plan.TargetCooldownSkipped++
					noteSkip(targetCooldownProbeBackoffReason)
					plan.SkippedDetails = append(plan.SkippedDetails, RebalanceSkipDetail{
						ChannelID:         snapshot.ChannelID,
						ChannelPoint:      snapshot.ChannelPoint,
						PeerAlias:         snapshot.PeerAlias,
						TargetOutboundPct: snapshot.TargetOutboundPct,
						TargetAmountSat:   snapshot.TargetAmountSat,
						Reason:            targetCooldownProbeBackoffReason,
					})
					continue
				}
			}
			plan.TargetCooldownSkipped++
			noteSkip("target_cooldown")
			plan.SkippedDetails = append(plan.SkippedDetails, RebalanceSkipDetail{
				ChannelID:         snapshot.ChannelID,
				ChannelPoint:      snapshot.ChannelPoint,
				PeerAlias:         snapshot.PeerAlias,
				TargetOutboundPct: snapshot.TargetOutboundPct,
				TargetAmountSat:   snapshot.TargetAmountSat,
				Reason:            "target_cooldown",
			})
			continue
		}
		if targetCfg.MinSplitEnabled && minExecuteSat > 0 && targetAmount < minExecuteSat {
			plan.BelowExecuteMinSkipped++
			noteSkip("below_execute_min")
			plan.SkippedDetails = append(plan.SkippedDetails, RebalanceSkipDetail{
				ChannelID:         snapshot.ChannelID,
				ChannelPoint:      snapshot.ChannelPoint,
				PeerAlias:         snapshot.PeerAlias,
				TargetOutboundPct: snapshot.TargetOutboundPct,
				TargetAmountSat:   targetAmount,
				Reason:            "below_execute_min",
			})
			continue
		}
		targetPolicy := lndclient.ChannelPolicySnapshot{
			FeeRatePpm:  snapshot.OutgoingFeePpm,
			BaseFeeMsat: snapshot.OutgoingBaseMsat,
		}
		budgetCost := estimateMaxCost(targetAmount, targetPolicy, targetCfg)
		estimatedCost := int64(0)
		if input.SovereignRanking {
			estimatedCost = estimateSovereignTargetCost(targetAmount, snapshot.RebalanceCost7dPpm, snapshot.RebalanceAmount7dSat, budgetCost, targetCfg)
		} else {
			estimatedCost = estimateHistoricalCost(targetAmount, snapshot.RebalanceCost7dPpm)
		}
		expectedGain := estimateTargetGainForConfig(targetCfg, snapshot, targetAmount)
		expectedROI, roiValid := estimateTargetROI(expectedGain, estimatedCost, targetAmount, snapshot.OutgoingFeePpm, snapshot.PeerFeeRatePpm)
		if input.Cfg.ROIMin > 0 && roiValid && expectedROI < input.Cfg.ROIMin {
			plan.ROISkipped++
			noteSkip("roi_guardrail")
			plan.SkippedDetails = append(plan.SkippedDetails, RebalanceSkipDetail{
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
		// profit_guardrail enforces an implicit ROI >= 1 floor (gain >= cost).
		// When the user has explicitly lowered ROIMin below 1 they opted into
		// loss-tolerant operation, so this hardcoded gate would contradict
		// their configured floor — defer to roi_guardrail above. We still fire
		// when ROIMin is unset (<=0) or >= 1 so default behavior is preserved.
		lossTolerantROI := input.Cfg.ROIMin > 0 && input.Cfg.ROIMin < 1
		if !lossTolerantROI && expectedGain > 0 && estimatedCost > 0 && expectedGain < estimatedCost {
			plan.ProfitSkipped++
			noteSkip("profit_guardrail")
			plan.SkippedDetails = append(plan.SkippedDetails, RebalanceSkipDetail{
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
		pairStats := input.PairStatsByTarget[snapshot.ChannelID]
		if targetDistinctSourceCooldown.DistinctSources > pairStats.RecentStructuralFailures {
			pairStats.RecentStructuralFailures = targetDistinctSourceCooldown.DistinctSources
		}
		// EV-weighted scoring is gated by an opt-in flag: with cumulative
		// pair stats it filters too aggressively because long-term observed
		// rates under-represent current routing success. When enabled the
		// score is gain × P(success) − cost × P(failure); otherwise the
		// legacy gain − cost score is used.
		var score int64
		if input.Cfg.SovereignEVWeightedScoring {
			score = evWeightedEconomicScore(expectedGain, estimatedCost, pairStats)
		} else {
			score = expectedGain - estimatedCost
		}
		plan.Candidates = append(plan.Candidates, rebalanceTarget{
			Channel:            snapshot,
			ExpectedGainSat:    expectedGain,
			EstimatedCostSat:   estimatedCost,
			BudgetCostSat:      budgetCost,
			ExpectedROI:        expectedROI,
			ExpectedROIValid:   roiValid,
			Score:              score,
			LastAutoAt:         input.LastAutoByTarget[snapshot.ChannelID],
			PairStats:          pairStats,
			StructuralCooldown: input.SovereignStructuralCooldowns[snapshot.ChannelID],
			UnsoldLiquidity:    input.SovereignUnsoldLiquidity[snapshot.ChannelID],
			TargetClass:        classifySovereignTarget(pairStats, input.Cfg),
		})
		if !plan.TopScoreSet || score > plan.TopScore {
			plan.TopScore = score
			plan.TopScoreSet = true
		}
	}

	applyMultiObjectiveScores(plan.Candidates, input.Cfg, input.ScanAt)
	if input.SovereignRanking {
		applySovereignRiskAdjustedScores(plan.Candidates, input.Cfg, input.ScanAt)
		for _, candidate := range plan.Candidates {
			if candidate.UnsoldLiquidityMultiplier > 0 && candidate.UnsoldLiquidityMultiplier < 1 && !shouldSkipSovereignUnsoldPaidLiquidity(candidate.UnsoldLiquidity, input.Cfg, input.ScanAt) {
				noteSkip(sovereignUnsoldPaidLiquidityPenaltyReason)
			}
		}
	}
	plan.AutofeeDampened = applyAutofeeSettlingPenalty(plan.Candidates, input.AutofeeRecentAdjustments, input.Cfg, input.ScanAt)
	if plan.AutofeeDampened > 0 {
		noteSkip("autofee_settling_target") // observability counter; candidate still queued
	}
	plan.TopScore = 0
	plan.TopScoreSet = false
	for _, candidate := range plan.Candidates {
		if candidate.CooldownProbe {
			continue
		}
		if !plan.TopScoreSet || candidate.Score > plan.TopScore {
			plan.TopScore = candidate.Score
			plan.TopScoreSet = true
		}
	}
	sortRebalanceTargets(plan.Candidates, plan.TopScore, plan.TopScoreSet, sovereignTopBucketPctForConfig(input.Cfg))
	plan.Candidates, plan.RecentSkipped = filterRecentRebalanceTargets(plan.Candidates, input.Cfg, input.ScanAt)
	for i := 0; i < plan.RecentSkipped; i++ {
		noteSkip("recently_attempted")
	}
	return plan
}

func sortRebalanceTargets(candidates []rebalanceTarget, topScore int64, topScoreSet bool, topBucketPct int) {
	// Wave 1.1 + 2026-05-29: score-first ordering com top-bucket configurável
	// (sovereign_top_bucket_pct, default 30%). Candidatos com score dentro de
	// top × (1 - pct/100) entram em fairness rotation (LastAutoAt oldest first);
	// abaixo do bucket, score puro vence. Bucket maior = mais diversidade pois
	// mais candidatos competem por rotação ao invés de só os scores de topo.
	if topBucketPct < sovereignTopBucketPctMin || topBucketPct > sovereignTopBucketPctMax {
		topBucketPct = sovereignTopBucketPctDefault
	}
	threshold := int64(100 - topBucketPct)
	inTopBucket := func(score int64) bool {
		if !topScoreSet {
			return false
		}
		if topScore <= 0 {
			return score == topScore
		}
		return score*100 >= topScore*threshold
	}
	sort.Slice(candidates, func(i, j int) bool {
		a := candidates[i]
		b := candidates[j]
		if a.CooldownProbe != b.CooldownProbe {
			return !a.CooldownProbe
		}
		aBucket := inTopBucket(a.Score)
		bBucket := inTopBucket(b.Score)
		if aBucket != bBucket {
			return aBucket
		}
		if !aBucket && a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.LastAutoAt.IsZero() != b.LastAutoAt.IsZero() {
			return a.LastAutoAt.IsZero()
		}
		if !a.LastAutoAt.IsZero() && !b.LastAutoAt.IsZero() && !a.LastAutoAt.Equal(b.LastAutoAt) {
			return a.LastAutoAt.Before(b.LastAutoAt)
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
}

func filterRecentRebalanceTargets(candidates []rebalanceTarget, cfg RebalanceConfig, scanAt time.Time) ([]rebalanceTarget, int) {
	cooldown := time.Duration(cfg.ScanIntervalSec) * time.Second
	if cooldown <= 0 {
		cooldown = autoTargetCooldownMin
	} else if cooldown < autoTargetCooldownMin {
		cooldown = autoTargetCooldownMin
	}
	recentSkipped := 0
	if len(candidates) <= 1 {
		return candidates, recentSkipped
	}
	filtered := make([]rebalanceTarget, 0, len(candidates))
	for _, target := range candidates {
		if !target.LastAutoAt.IsZero() && scanAt.Sub(target.LastAutoAt) < cooldown {
			recentSkipped++
			continue
		}
		filtered = append(filtered, target)
	}
	if len(filtered) > 0 {
		return filtered, recentSkipped
	}
	return candidates, 0
}

func (s *RebalanceService) startJob(targetChannelID uint64, source string, reason string, amountOverride int64, manualAutoRestart bool) (int64, error) {
	return s.startJobWithEconomics(targetChannelID, source, reason, amountOverride, manualAutoRestart, false, rebalanceJobEconomics{})
}

// startOperatorJob queues a rebalance explicitly triggered by the operator via
// the "Manual Rebal In" button. The operator is acting deliberately, so the
// pre-flight budget and manual-restart cooldown gates are bypassed — the job is
// simply queued and executed. The channel-busy guard and the per-route fee
// limit (applied later in runJob) still hold. Only this button path sets
// operatorInitiated; the manual-restart watch, scheduled restarts, and the
// auto/rules scan are unaffected.
func (s *RebalanceService) startOperatorJob(targetChannelID uint64, amountOverride int64, manualAutoRestart bool) (int64, error) {
	return s.startJobWithEconomics(targetChannelID, "manual", "", amountOverride, manualAutoRestart, true, rebalanceJobEconomics{})
}

func (s *RebalanceService) startJobWithEconomics(targetChannelID uint64, source string, reason string, amountOverride int64, manualAutoRestart bool, operatorInitiated bool, economics rebalanceJobEconomics) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := s.loadConfig(ctx)
	if err != nil {
		cfg = defaultRebalanceConfig()
	}

	channels, err := s.listChannelsCached(ctx)
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
	if isChannelAutomationParked(setting.AutomationMode) {
		return 0, errChannelAutomationParked
	}
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
	if !operatorInitiated && shouldEnforceManualRestartCooldown(source, reason) {
		if lastRestartAt, ok := s.lastManualAutoRestartAt(ctx, targetChannelID); ok {
			if time.Since(lastRestartAt) < manualRestartInterval(cfg) {
				return 0, errManualRestartCooldown
			}
		}
	}
	if !operatorInitiated && shouldEnforceManualRestartBudget(cfg, source, reason, manualAutoRestart) {
		budget, _, spentManual, spentTotal := s.getDailyBudget(ctx)
		if err := checkManualBudgetAllowance(cfg, setting, target, amount, budget, spentManual, spentTotal); err != nil {
			return 0, err
		}
	}

	jobID, err := s.insertJob(ctx, &target, source, reason, targetPct, amount, economics)
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

type rebalanceJobRunner struct {
	service         *RebalanceService
	jobID           int64
	targetChannelID uint64
	amount          int64
	targetPct       float64
	jobSource       string
	jobReason       string
}

type rebalanceJobRunState struct {
	ready                  bool
	ctx                    context.Context
	cancel                 context.CancelFunc
	cancelRegistered       bool
	workerAcquired         bool
	cooldownProbeAcquired  bool
	targetLocked           bool
	cfg                    RebalanceConfig
	feeCfg                 RebalanceConfig
	amount                 int64
	minExecuteSat          int64
	minProbeSat            int64
	startAmountSat         int64
	useRecentFailureCache  bool
	cooldownProbeJob       bool
	shadowRecorded         bool
	floorBlockedSources    map[uint64]struct{}
	pairStats              map[uint64]pairStat
	targetSnapshot         RebalanceChannel
	sources                []RebalanceChannel
	sourceFloorPct         float64
	sourceBaseCap          map[uint64]int64
	sourceAvailable        map[uint64]int64
	currentJobBlockedPairs map[uint64]struct{}
}

type rebalanceMppShardTask struct {
	ShardIndex int
	Source     RebalanceChannel
	AmountSat  int64
}

type rebalanceMppShardAttemptResult struct {
	ShardIndex      int
	Source          RebalanceChannel
	AmountRequested int64
	AmountSent      int64
	FeeLimitPpm     int64
	FeePaidSat      int64
	RouteMaxSat     int64
	RouteHops       []string
	PaymentHash     string
	FailReason      string
	FailureInfo     *attemptFailureInfo
	Attempted       bool
	Succeeded       bool
	TimedOut        bool
	Fatal           bool
}

type rebalanceMppPrepassContext struct {
	runner                     *rebalanceJobRunner
	state                      *rebalanceJobRunState
	targetPolicy               lndclient.ChannelPolicySnapshot
	selfPubkey                 string
	remaining                  *int64
	attemptedAny               *bool
	attemptIndex               *int
	attemptTimeoutSec          int
	refreshSourceAvailability  func(bool)
	shouldSkipCurrentJobSource func(uint64) bool
	snapshotIgnoredRoutes      func() ([]*lnrpc.EdgeLocator, []*lnrpc.NodePair)
	noteRouteFailureFromShard  func(*lnrpc.Route, uint32)
	finishOnTimeout            func()
	recordPairSuccess          func(context.Context, uint64, uint64, int64, int64, int64, []string)
	recordPairFailure          func(context.Context, uint64, uint64, string)
	noteAutoStructuralFailure  func(string, bool)
	applySuccess               func(int64, int64, *int64, *int64) bool
}

func (s *RebalanceService) runJob(jobID int64, targetChannelID uint64, amount int64, targetPct float64, jobSource string, jobReason string) {
	runner := rebalanceJobRunner{
		service:         s,
		jobID:           jobID,
		targetChannelID: targetChannelID,
		amount:          amount,
		targetPct:       targetPct,
		jobSource:       jobSource,
		jobReason:       jobReason,
	}
	runner.run()
}

func (r *rebalanceJobRunner) contextWithTimeout(cfg RebalanceConfig) (context.Context, context.CancelFunc) {
	timeoutSec := cfg.RebalanceTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	return context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
}

func (r *rebalanceJobRunner) registerCancel(cancel context.CancelFunc) {
	r.service.mu.Lock()
	r.service.jobCancel[r.jobID] = cancel
	r.service.mu.Unlock()
}

func (r *rebalanceJobRunner) unregisterCancel() {
	r.service.mu.Lock()
	delete(r.service.jobCancel, r.jobID)
	r.service.mu.Unlock()
}

func (r *rebalanceJobRunner) finalizeMppShadow(shadowRecorded bool, floorBlockedSources map[uint64]struct{}) {
	if !shadowRecorded {
		return
	}
	shadowCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.service.updateMppShadowFloorBlockedSources(shadowCtx, r.jobID, int64(len(floorBlockedSources))); err != nil && r.service.logger != nil {
		r.service.logger.Printf("rebalance mpp shadow floor telemetry update failed: job=%d err=%v", r.jobID, err)
	}
	if err := r.service.finalizeMppShadowPlan(shadowCtx, r.jobID); err != nil && r.service.logger != nil {
		r.service.logger.Printf("rebalance mpp shadow finalize failed: job=%d err=%v", r.jobID, err)
	}
}

func (r *rebalanceJobRunner) finalize(st *rebalanceJobRunState) {
	if st == nil {
		return
	}
	if st.targetLocked {
		r.service.unlockChannel(r.targetChannelID)
		st.targetLocked = false
	}
	if st.workerAcquired {
		r.service.releaseSem()
		st.workerAcquired = false
	}
	if st.cooldownProbeAcquired {
		r.service.releaseCooldownProbeSem()
		st.cooldownProbeAcquired = false
	}
	r.finalizeMppShadow(st.shadowRecorded, st.floorBlockedSources)
	if st.cancelRegistered {
		r.unregisterCancel()
		st.cancelRegistered = false
	}
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
}

func (r *rebalanceJobRunner) run() {
	st := &rebalanceJobRunState{}
	r.prepare(st)
	if !st.ready {
		r.finalize(st)
		return
	}
	defer r.finalize(st)
	if r.runDelegatedFastPath(st) {
		return
	}
	r.runLegacyLoop(st)
}

// runDelegatedFastPath é o caminho rápido inspirado no LNDG: passa todas as
// sources elegíveis ao LND nativo numa única chamada SendPaymentV2 com MPP,
// deixando que pathfinder + Mission Control nativos escolham. Em sucesso,
// finaliza o job e retorna true (legacy loop é skipado). Em falha, retorna
// false e o caller cai no loop tradicional.
//
// Bypassa pair-cache (LND nativo decide qual source/rota usar). Mantém o
// mesmo fee_limit do loop legado. Persiste attempts agregadas com
// payment_hash. Em sucesso, chama recordPairSuccess para a source da rota
// vencedora — preserva aprendizado por par + reinforce de MC se habilitado.
func (r *rebalanceJobRunner) runDelegatedFastPath(st *rebalanceJobRunState) bool {
	if st == nil || !st.ready {
		return false
	}
	cfg := st.cfg
	if !cfg.DelegatedFastPathEnabled {
		return false
	}
	// cooldown-probe é excluído do fast-path. Avaliação em 2026-05-09 mostrou
	// que probes via fast-path têm hit rate baixo (5 %) e cada falha consome
	// 10 min de gRPC esperando o LND nativo esgotar a busca contra targets
	// estruturalmente mortos. Em ~7h de janela, isso virou 27 timeouts contra
	// 3 sucessos — custo-benefício ruim. Probes voltam ao loop legado: itera
	// 2 sources e falha rápido se rota não existe. Peers em cooldown voltam
	// naturalmente via auto-scan / auto-restart quando permanentFailScoreTTL
	// (cap 1h) expira.
	if st.cooldownProbeJob {
		return false
	}
	s := r.service
	if s.lnd == nil || s.db == nil {
		return false
	}
	ctx := st.ctx
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	targetSnapshot := st.targetSnapshot
	if strings.TrimSpace(targetSnapshot.RemotePubkey) == "" {
		return false
	}

	// Coletar sources com capacidade suficiente. Em modo strict-payback, uma
	// source só entra se puder cobrir o valor inteiro dentro do MaxSourceSat já
	// descontado pela proteção de payback; o LND nativo não aceita cap por
	// source em SendPaymentV2.
	sourceIDs := delegatedFastPathSourceIDs(st.sources, st.sourceAvailable, st.amount, st.minExecuteSat, cfg.DelegatedFastPathStrictPayback)
	if len(sourceIDs) == 0 {
		return false
	}

	// Fee limit — mesmo cálculo do loop legado.
	feeCfg := st.feeCfg
	targetPolicy := lndclient.ChannelPolicySnapshot{
		FeeRatePpm:  targetSnapshot.OutgoingFeePpm,
		BaseFeeMsat: targetSnapshot.OutgoingBaseMsat,
	}
	maxFeeMsat, feeErr := calcFeeLimitMsat(st.amount*1000, targetPolicy, nil, feeCfg)
	maxFeePpm := feeMsatToPpm(maxFeeMsat, st.amount)
	if feeErr != nil || maxFeeMsat <= 0 || maxFeePpm <= 0 {
		return false
	}

	selfPub, selfErr := s.lnd.SelfPubkey(ctx)
	if selfErr != nil || strings.TrimSpace(selfPub) == "" {
		return false
	}
	expirySec := int64(feeCfg.RebalanceTimeoutSec)
	if expirySec <= 0 {
		expirySec = 600
	}
	// Cap do fast-path: o LND nativo, quando não acha rota, fica explorando
	// exaustivamente até estourar timeout. Com volume alto, falhas concorrentes
	// saturam gRPC (ListChannels também pendura). Telemetria R6 (2026-05-28)
	// mostrou 60% dos fast-path failures sendo timeout — default baixou de 180
	// → 90s pra cortar pathfinding morto pela metade. Em sucesso, ainda dá pra
	// MPP exploration média; em falha, sobra mais tempo do orçamento total do
	// job para o legacy loop fazer RapidFire. Configurável via UI (range
	// 30-300s) para operadores que precisem ajustar.
	timeoutSec := int32(fastPathMaxTimeoutSecForConfig(feeCfg))
	if rebalTo := int32(feeCfg.RebalanceTimeoutSec); rebalTo > 0 && rebalTo < timeoutSec {
		timeoutSec = rebalTo
	}
	maxParts := uint32(1)
	if feeCfg.MppEnabled && feeCfg.MppMaxShards > 1 {
		maxParts = uint32(feeCfg.MppMaxShards)
	}

	s.markFastPathAttempted(r.jobID)
	broadTimeoutSec := timeoutSec

	preferredCount := preferredFastPathSourceCount(len(sourceIDs), maxParts)
	preferredIDs := []uint64(nil)
	now := time.Now()
	if preferredCount > 0 && preferredCount < len(sourceIDs) && hasPreferredFastPathRouteProof(st.pairStats, sourceIDs, now) {
		preferredIDs = preferredDelegatedFastPathSourceIDs(st.sources, st.sourceAvailable, st.pairStats, st.amount, st.minExecuteSat, cfg.DelegatedFastPathStrictPayback, preferredCount, now)
		if len(preferredIDs) >= len(sourceIDs) {
			preferredIDs = nil
		}
	}
	preferredTimeoutSec := preferredFastPathTimeout(timeoutSec)
	if len(preferredIDs) > 0 && preferredTimeoutSec > 0 {
		preferredInvoice, preferredInvErr := s.lnd.CreateInvoice(ctx, st.amount, "rebalance-fast-path-preferred", expirySec, nil)
		if preferredInvErr == nil && strings.TrimSpace(preferredInvoice.PaymentRequest) != "" {
			if s.logger != nil {
				s.logger.Printf("rebalance fast-path preferred: job=%d target=%d amount=%d sources=%d fee_cap_ppm=%d timeout_sec=%d", r.jobID, r.targetChannelID, st.amount, len(preferredIDs), maxFeePpm, preferredTimeoutSec)
			}
			preferredCtx, preferredCancel := context.WithTimeout(ctx, time.Duration(preferredTimeoutSec+10)*time.Second)
			preferredStartedAt := time.Now()
			preferredPayment, preferredSendErr := s.lnd.SendPaymentMultiSource(
				preferredCtx,
				preferredInvoice.PaymentRequest,
				preferredIDs,
				targetSnapshot.RemotePubkey,
				maxFeeMsat,
				preferredTimeoutSec,
				maxParts,
			)
			preferredCancel()
			if preferredSendErr == nil && preferredPayment != nil && preferredPayment.Status == lnrpc.Payment_SUCCEEDED {
				feePaidSat := preferredPayment.FeeSat
				if feePaidSat == 0 && preferredPayment.FeeMsat > 0 {
					feePaidSat = preferredPayment.FeeMsat / 1000
				}
				sourceUsed := uint64(0)
				routeHops := []string{}
				for _, htlc := range preferredPayment.Htlcs {
					if htlc.Status != lnrpc.HTLCAttempt_SUCCEEDED || htlc.Route == nil || len(htlc.Route.Hops) == 0 {
						continue
					}
					sourceUsed = htlc.Route.Hops[0].ChanId
					for _, hop := range htlc.Route.Hops {
						if hop.PubKey != "" {
							routeHops = append(routeHops, hop.PubKey)
						}
					}
					break
				}
				if sourceUsed == 0 && len(preferredIDs) == 1 {
					sourceUsed = preferredIDs[0]
				}
				_ = s.insertAttemptWithStart(ctx, r.jobID, 0, sourceUsed, st.amount, maxFeePpm, feePaidSat, "succeeded", preferredInvoice.PaymentHash, "", nil, preferredStartedAt)
				if sourceUsed != 0 {
					s.recordPairSuccess(ctx, sourceUsed, r.targetChannelID, st.amount, maxFeePpm, feePaidSat, routeHops)
					if cfg.MissionControlReinforce && len(routeHops) > 0 {
						go s.reinforceMissionControl(selfPub, append([]string(nil), routeHops...), st.amount)
					}
				}
				if s.logger != nil {
					s.logger.Printf("rebalance fast-path preferred succeeded: job=%d source=%d fee_paid=%d hops=%d", r.jobID, sourceUsed, feePaidSat, len(routeHops))
				}
				s.finishJob(r.jobID, "succeeded", "delegated-fast-path")
				return true
			}
			failReason := "fast-path preferred: failed"
			if preferredSendErr != nil {
				failReason = "fast-path preferred: " + preferredSendErr.Error()
			} else if preferredPayment != nil && preferredPayment.FailureReason != lnrpc.PaymentFailureReason_FAILURE_REASON_NONE {
				failReason = "fast-path preferred: " + strings.ToLower(strings.TrimPrefix(preferredPayment.FailureReason.String(), "FAILURE_REASON_"))
			}
			_ = s.insertAttempt(ctx, r.jobID, 0, 0, st.amount, maxFeePpm, 0, "failed", preferredInvoice.PaymentHash, failReason, nil)
			if s.logger != nil && preferredSendErr != nil {
				s.logger.Printf("rebalance fast-path preferred failed: job=%d err=%v", r.jobID, preferredSendErr)
			}
		}
	}

	invoice, invErr := s.lnd.CreateInvoice(ctx, st.amount, "rebalance-fast-path", expirySec, nil)
	if invErr != nil || strings.TrimSpace(invoice.PaymentRequest) == "" {
		return false
	}

	if s.logger != nil {
		s.logger.Printf("rebalance fast-path broad: job=%d target=%d amount=%d sources=%d fee_cap_ppm=%d timeout_sec=%d", r.jobID, r.targetChannelID, st.amount, len(sourceIDs), maxFeePpm, broadTimeoutSec)
	}

	// Broad-pass sub-context. The preferred pass is only a short route-proven
	// shortcut; the broad pass keeps the full fast-path budget so it is not
	// penalized by a preferred miss.
	fastPathCtx, fastPathCancel := context.WithTimeout(ctx, time.Duration(broadTimeoutSec+10)*time.Second)
	defer fastPathCancel()

	broadStartedAt := time.Now()
	payment, sendErr := s.lnd.SendPaymentMultiSource(
		fastPathCtx,
		invoice.PaymentRequest,
		sourceIDs,
		targetSnapshot.RemotePubkey,
		maxFeeMsat,
		broadTimeoutSec,
		maxParts,
	)
	if sendErr != nil || payment == nil || payment.Status != lnrpc.Payment_SUCCEEDED {
		// Falha — fall-through para legacy loop. Não envenenamos pair-cache aqui:
		// "LND nativo não achou rota agora" é diferente de "esta source falhou".
		// O legacy loop, se rodar em seguida, registra falhas per-source com
		// granularidade adequada.
		failReason := "fast-path broad: failed"
		if sendErr != nil {
			failReason = "fast-path broad: " + sendErr.Error()
		} else if payment != nil && payment.FailureReason != lnrpc.PaymentFailureReason_FAILURE_REASON_NONE {
			failReason = "fast-path broad: " + strings.ToLower(strings.TrimPrefix(payment.FailureReason.String(), "FAILURE_REASON_"))
		}
		// Persiste a tentativa do fast-path no histórico (attempt_index=0,
		// source_channel_id=0 → UI mostra como "Fast-path multi-source").
		// Permite ao operador ver que o fast-path foi tentado primeiro antes
		// das tentativas per-source do legacy loop.
		_ = s.insertAttempt(ctx, r.jobID, 0, 0, st.amount, maxFeePpm, 0, "failed", "", failReason, nil)
		if s.logger != nil && sendErr != nil {
			s.logger.Printf("rebalance fast-path broad failed: job=%d err=%v (falling back to legacy loop)", r.jobID, sendErr)
		}
		return false
	}

	feePaidSat := payment.FeeSat
	if feePaidSat == 0 && payment.FeeMsat > 0 {
		feePaidSat = payment.FeeMsat / 1000
	}
	sourceUsed := uint64(0)
	routeHops := []string{}
	for _, htlc := range payment.Htlcs {
		if htlc.Status != lnrpc.HTLCAttempt_SUCCEEDED || htlc.Route == nil || len(htlc.Route.Hops) == 0 {
			continue
		}
		sourceUsed = htlc.Route.Hops[0].ChanId
		for _, hop := range htlc.Route.Hops {
			if hop.PubKey != "" {
				routeHops = append(routeHops, hop.PubKey)
			}
		}
		break
	}
	if sourceUsed == 0 && len(sourceIDs) == 1 {
		sourceUsed = sourceIDs[0]
	}

	// attempt_index=0 indica que esse foi o caminho do fast-path (multi-source).
	// Legacy attempts começam em 1 e incrementam — fica visualmente separado.
	_ = s.insertAttemptWithStart(ctx, r.jobID, 0, sourceUsed, st.amount, maxFeePpm, feePaidSat, "succeeded", invoice.PaymentHash, "", nil, broadStartedAt)
	if sourceUsed != 0 {
		s.recordPairSuccess(ctx, sourceUsed, r.targetChannelID, st.amount, maxFeePpm, feePaidSat, routeHops)
		if cfg.MissionControlReinforce && len(routeHops) > 0 {
			go s.reinforceMissionControl(selfPub, append([]string(nil), routeHops...), st.amount)
		}
	}
	if s.logger != nil {
		s.logger.Printf("rebalance fast-path broad succeeded: job=%d source=%d fee_paid=%d hops=%d", r.jobID, sourceUsed, feePaidSat, len(routeHops))
	}
	s.finishJob(r.jobID, "succeeded", "delegated-fast-path")
	return true
}

func (r *rebalanceJobRunner) prepare(st *rebalanceJobRunState) {
	s := r.service
	jobID := r.jobID
	targetChannelID := r.targetChannelID
	amount := r.amount
	targetPct := r.targetPct
	jobSource := r.jobSource
	jobReason := r.jobReason

	// loadConfig com context.Background() (sem timeout) era bug crítico: se DB
	// pool saturasse durante burst de jobs, TODOS os goroutines ficavam presos
	// aqui INDEFINIDAMENTE — antes mesmo de acquireSem ou markJobRunning.
	// Resultado: jobs em status='queued' coletados pelo cleanupStaleJobs após
	// 600s, marcados como reason='timeout', completed_at em batch idêntico.
	// Wrap com 5s pra falhar rápido em caso de pool exausto.
	loadCfgCtx, loadCfgCancel := context.WithTimeout(context.Background(), 5*time.Second)
	cfg, _ := s.loadConfig(loadCfgCtx)
	loadCfgCancel()
	minExecuteSat := effectiveMinExecuteSat(cfg)
	minProbeSat := effectiveMinProbeSat(cfg)
	useRecentFailureCache := shouldUseRecentFailureCache(jobSource, jobReason)
	cooldownProbeJob := isTargetCooldownProbeJob(jobSource, jobReason)
	st.cooldownProbeJob = cooldownProbeJob
	st.floorBlockedSources = map[uint64]struct{}{}
	if cooldownProbeJob && !cfg.CooldownProbeEnabled {
		s.finishJob(jobID, "skipped", cooldownProbeDisabledJobReason)
		return
	}

	// CTX SEPARATION (fix 2026-05-09):
	//
	// Antes, o ctx do job (RebalanceTimeoutSec=600s) era criado ANTES do
	// acquireSem. Com fila (max_concurrent saturado), o ctx começava a contar
	// na espera, chegando ao prepare/legacy loop com pouco tempo restante e
	// "morrendo" em timeout antes de tentar nada. Combinado com o
	// cleanupStaleJobs (a cada 2min), produzia clusters massivos de
	// status='failed', reason='timeout' visíveis na UI.
	//
	// Agora: acquireSem espera por TEMPO INDETERMINADO até pegar slot.
	// Cada job entra na fila e respeita seu turno — ninguém é cortado por
	// causa de queue wait. SOMENTE após pegar o slot, ctx de execução é
	// criado fresco com 600s completos. Goroutines presas em acquireSem
	// são liberadas no shutdown (s.stop fecha).
	if cooldownProbeJob {
		priorityCtx, priorityCancel := context.WithTimeout(context.Background(), 2*time.Second)
		priorityBusy := s.hasActivePriorityRebalanceWork(priorityCtx, jobID)
		priorityCancel()
		if priorityBusy {
			s.finishJob(jobID, "skipped", cooldownProbeDeferredJobReason)
			return
		}
		if !s.acquireCooldownProbeSem(context.Background()) {
			s.finishJob(jobID, "skipped", cooldownProbeBusyJobReason)
			return
		}
		st.cooldownProbeAcquired = true
	} else {
		if !s.acquireSem(context.Background()) {
			s.finishJob(jobID, "skipped", "no worker available")
			return
		}
		st.workerAcquired = true
	}

	// Ctx de execução criado SÓ agora — cada job tem seus 600s completos
	// para rodar prepare + fast-path + legacy loop.
	ctx, cancel := r.contextWithTimeout(cfg)
	st.ctx = ctx
	st.cancel = cancel
	r.registerCancel(cancel)
	st.cancelRegistered = true

	if !s.tryLockChannel(targetChannelID) {
		s.finishJob(jobID, "skipped", "channel busy")
		return
	}
	st.targetLocked = true

	s.markJobRunning(jobID)

	if minExecuteSat > 0 && amount < minExecuteSat {
		s.finishJob(jobID, "skipped", "amount below minimum")
		return
	}
	if cfg.MaxAmountSat > 0 && amount > cfg.MaxAmountSat {
		amount = cfg.MaxAmountSat
	}

	settings, _ := s.loadChannelSettings(ctx)
	targetSetting := normalizeChannelSetting(settings[targetChannelID])
	if isChannelAutomationParked(targetSetting.AutomationMode) {
		s.finishJob(jobID, "skipped", "channel parked")
		return
	}
	feeCfg := effectiveConfigForTarget(cfg, targetSetting)
	minExecuteSat = effectiveMinExecuteSat(feeCfg)
	minProbeSat = effectiveMinProbeSat(feeCfg)
	startAmountSat := effectiveStartAmountSat(feeCfg)
	if cooldownProbeJob {
		probeAmount := rebalanceCooldownProbeAmount(amount, feeCfg)
		if probeAmount > 0 && probeAmount < amount {
			amount = probeAmount
		}
	}
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx, cfg)
	_ = s.applyForwardDeltas(ctx, ledger)

	channels, err := s.listChannelsCached(ctx)
	if err != nil {
		s.finishJob(jobID, "failed", "lnd unavailable")
		return
	}
	s.reconcileNewChannelDefaults(ctx, channels, settings, exclusions)

	// Wave 2.4: abort the job if revenue/cost fetch fails. Previously these
	// errors were silently dropped and the snapshot used zero values for both,
	// corrupting ROI estimation and target eligibility (zero spread/cost makes
	// any target appear free to rebalance).
	revenueByChannel, revErr := s.fetchChannelRevenue7d(ctx)
	costByChannel, costErr := s.fetchChannelRebalanceCost7d(ctx)
	if revErr != nil || costErr != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance job=%d: db unavailable for revenue/cost fetch (revErr=%v costErr=%v)", jobID, revErr, costErr)
		}
		s.finishJob(jobID, "failed", "db unavailable")
		return
	}
	drainRateByChannel := s.fetchChannelDrainRate24h(ctx)

	targetFound := false
	channelSnapshots := []RebalanceChannel{}
	for _, ch := range channels {
		setting := settings[ch.ChannelID]
		snapshot := s.buildChannelSnapshot(ctx, cfg, false, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], drainRateByChannel[ch.ChannelID], exclusions[ch.ChannelID])
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
		s.finishJob(jobID, "skipped", "target already balanced")
		return
	}
	if minExecuteSat > 0 && amount < minExecuteSat {
		s.finishJob(jobID, "skipped", "amount below minimum")
		return
	}

	// Wave 1.5: pre-load pair stats so that wave 1.2 (fee floor) and 1.3
	// (adaptive start amount) can use them before the loop starts. The map
	// is shared with the recordPairSuccess/Failure closures defined below.
	pairStats := s.loadPairStatsForTarget(ctx, targetChannelID)

	// Wave 1.3: bump the start amount toward the most recent proven success
	// across all sources. Capped at the job amount so we never over-shoot.
	if startAmountSat < amount {
		bestAdaptive := int64(0)
		now := time.Now()
		for _, stat := range pairStats {
			if stat.SuccessAmountSat <= 0 || stat.LastSuccessAt.IsZero() {
				continue
			}
			if now.Sub(stat.LastSuccessAt) > pairSuccessTTL {
				continue
			}
			if !stat.LastFailAt.IsZero() && stat.LastFailAt.After(stat.LastSuccessAt) {
				continue
			}
			candidate := stat.SuccessAmountSat * 12 / 10
			if candidate > bestAdaptive {
				bestAdaptive = candidate
			}
		}
		if bestAdaptive > amount {
			bestAdaptive = amount
		}
		if bestAdaptive > startAmountSat {
			startAmountSat = bestAdaptive
		}
	}

	targetSnapshot := RebalanceChannel{}
	for _, snap := range channelSnapshots {
		if snap.ChannelID == targetChannelID {
			targetSnapshot = snap
			break
		}
	}

	if !targetSnapshot.EligibleAsTarget {
		s.finishJob(jobID, "skipped", "target not eligible")
		return
	}
	if strings.TrimSpace(targetSnapshot.RemotePubkey) == "" {
		s.finishJob(jobID, "skipped", "target peer unavailable")
		return
	}

	sources := filterSources(channelSnapshots, targetChannelID)
	if useRecentFailureCache {
		sourceCooldowns := s.loadRecentSourceCooldowns(ctx, time.Now().Add(-recentCooldownWindow))
		if len(sourceCooldowns) > 0 {
			filteredSources := make([]RebalanceChannel, 0, len(sources))
			now := time.Now()
			for _, source := range sources {
				if shouldCooldownRecentFailures(sourceCooldowns[source.ChannelID], sourceCooldownMinAttempts, sourceCooldownMaxSuccess, now) {
					continue
				}
				filteredSources = append(filteredSources, source)
			}
			sources = filteredSources
		}
	}
	if shouldRunMppShadow(feeCfg, jobSource) && !cooldownProbeJob {
		shadowPlan := buildMppShadowPlan(amount, sources, feeCfg)
		if err := s.insertMppShadowPlan(ctx, jobID, targetChannelID, jobSource, feeCfg, amount, shadowPlan); err != nil {
			if s.logger != nil {
				s.logger.Printf("rebalance mpp shadow insert failed: job=%d err=%v", jobID, err)
			}
		} else {
			st.shadowRecorded = true
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
		s.finishJob(jobID, "skipped", "no eligible sources")
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

	st.cfg = cfg
	st.feeCfg = feeCfg
	st.amount = amount
	st.minExecuteSat = minExecuteSat
	st.minProbeSat = minProbeSat
	st.startAmountSat = startAmountSat
	st.useRecentFailureCache = useRecentFailureCache
	st.pairStats = pairStats
	st.targetSnapshot = targetSnapshot
	st.sources = sources
	st.sourceFloorPct = sourceFloorPct
	st.sourceBaseCap = sourceBaseCap
	st.sourceAvailable = sourceAvailable
	st.currentJobBlockedPairs = map[uint64]struct{}{}
	st.ready = true
	return
}

func (r *rebalanceJobRunner) runLegacyLoop(st *rebalanceJobRunState) {
	s := r.service
	jobID := r.jobID
	targetChannelID := r.targetChannelID
	jobSource := r.jobSource
	jobReason := r.jobReason
	ctx := st.ctx
	cfg := st.cfg
	feeCfg := st.feeCfg
	amount := st.amount
	minExecuteSat := st.minExecuteSat
	minProbeSat := st.minProbeSat
	startAmountSat := st.startAmountSat
	useRecentFailureCache := st.useRecentFailureCache
	cooldownProbeJob := st.cooldownProbeJob
	pairStats := st.pairStats
	targetSnapshot := st.targetSnapshot
	sources := st.sources
	sourceFloorPct := st.sourceFloorPct
	sourceBaseCap := st.sourceBaseCap
	sourceAvailable := st.sourceAvailable
	floorBlockedSources := st.floorBlockedSources

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
		channelsNow, err := s.listChannelsCached(refreshCtx)
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

	currentJobBlockedPairs := st.currentJobBlockedPairs
	// selfPubkey is fetched below before the source loop runs; declared here so
	// the recordPairSuccess closure can capture it for Wave 4.4 reinforcement.
	var selfPubkey string
	shouldSkipCurrentJobSource := func(sourceChannelID uint64) bool {
		_, ok := currentJobBlockedPairs[sourceChannelID]
		return ok
	}
	blockCurrentJobPair := func(sourceChannelID uint64, reason string) {
		if !useRecentFailureCache || sourceChannelID == 0 {
			return
		}
		if shouldBlockPairForCurrentJobFailure(reason) {
			currentJobBlockedPairs[sourceChannelID] = struct{}{}
		}
	}
	recordPairSuccess := func(ctx context.Context, sourceChannelID uint64, targetChannelID uint64, amountSat int64, feePpm int64, feePaidSat int64, routeHops []string) {
		s.recordPairSuccess(ctx, sourceChannelID, targetChannelID, amountSat, feePpm, feePaidSat, routeHops)
		delete(currentJobBlockedPairs, sourceChannelID)
		now := time.Now()
		stat := pairStats[sourceChannelID]
		stat.SourceChannelID = sourceChannelID
		stat.TargetChannelID = targetChannelID
		stat.LastSuccessAt = now
		stat.LastFailAt = time.Time{}
		stat.LastFailReason = ""
		stat.FailCount = 0
		stat.PermanentFailScore = 0
		stat.PermanentFailUpdated = now
		stat.SuccessAmountSat = amountSat
		stat.SuccessFeePpm = feePpm
		if len(routeHops) > 0 {
			stat.LastSuccessRouteHops = append([]string(nil), routeHops...)
		}
		pairStats[sourceChannelID] = stat
		// Wave 4.4: opt-in MC reinforcement after every recorded success.
		// Run async so the post-payment accounting path is not delayed by an
		// extra RPC.
		if cfg.MissionControlReinforce && len(routeHops) > 0 {
			hops := append([]string(nil), routeHops...)
			amount := amountSat
			self := selfPubkey
			go s.reinforceMissionControl(self, hops, amount)
		}
	}
	recordPairFailure := func(ctx context.Context, sourceChannelID uint64, targetChannelID uint64, reason string) {
		s.recordPairFailure(ctx, sourceChannelID, targetChannelID, reason)
		blockCurrentJobPair(sourceChannelID, reason)
		now := time.Now()
		stat := pairStats[sourceChannelID]
		stat.SourceChannelID = sourceChannelID
		stat.TargetChannelID = targetChannelID
		stat.LastFailAt = now
		stat.LastFailReason = reason
		stat.FailCount++
		if increment := permanentFailScoreIncrement(reason); increment > 0 {
			stat.PermanentFailScore = nextPermanentFailScore(stat.PermanentFailScore, stat.PermanentFailUpdated, now, increment)
			stat.PermanentFailUpdated = now
		}
		pairStats[sourceChannelID] = stat
	}

	rankNow := time.Now()
	sort.Slice(sources, func(i, j int) bool {
		rank := func(ch RebalanceChannel) (bool, bool, float64, int64, int64, int64, time.Duration) {
			sourceCostPpm := ch.OutgoingFeePpm
			if sourceCostPpm < 0 {
				sourceCostPpm = 0
			}
			stat, ok := pairStats[ch.ChannelID]
			if !ok {
				return false, false, 0, sourceCostPpm, 0, 0, 0
			}
			hasRecentSuccess := false
			if !stat.LastSuccessAt.IsZero() && rankNow.Sub(stat.LastSuccessAt) <= pairSuccessTTL {
				if stat.LastFailAt.IsZero() || stat.LastSuccessAt.After(stat.LastFailAt) {
					hasRecentSuccess = true
				}
			}
			hasRecentFail := false
			if shouldSkipPairForRecentFailure(stat, rankNow) {
				hasRecentFail = true
			}
			permanentFailScore := decayedPermanentFailScore(stat.PermanentFailScore, stat.PermanentFailUpdated, rankNow)
			age := time.Duration(0)
			if !stat.LastSuccessAt.IsZero() {
				age = rankNow.Sub(stat.LastSuccessAt)
			}
			return hasRecentSuccess, hasRecentFail, permanentFailScore, sourceCostPpm, stat.SuccessFeePpm, stat.SuccessAmountSat, age
		}

		iSuccess, iFail, iPermanentFailScore, iSourceCost, iFee, iAmt, iAge := rank(sources[i])
		jSuccess, jFail, jPermanentFailScore, jSourceCost, jFee, jAmt, jAge := rank(sources[j])

		if iSuccess != jSuccess {
			return iSuccess
		}
		if iFail != jFail {
			return !iFail
		}
		if math.Abs(iPermanentFailScore-jPermanentFailScore) > 0.25 {
			return iPermanentFailScore < jPermanentFailScore
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
		if sources[i].SourceOpportunityCost != sources[j].SourceOpportunityCost {
			return sources[i].SourceOpportunityCost < sources[j].SourceOpportunityCost
		}
		if iSourceCost != jSourceCost {
			return iSourceCost < jSourceCost
		}
		// Wave 3.4: prefer sources with fewer outgoing HTLCs locked.
		if sources[i].PendingOutgoingHtlcs != sources[j].PendingOutgoingHtlcs {
			return sources[i].PendingOutgoingHtlcs < sources[j].PendingOutgoingHtlcs
		}
		return sources[i].MaxSourceSat > sources[j].MaxSourceSat
	})
	refreshSourceAvailability(true)
	if cooldownProbeJob {
		sources = filterExecutableSources(sources, sourceAvailable, minExecuteSat)
		if len(sources) > targetCooldownProbeMaxSources {
			sources = sources[:targetCooldownProbeMaxSources]
		}
		if len(sources) == 0 {
			s.finishJob(jobID, "skipped", "no executable sources")
			return
		}
	}

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

	pubkey, selfErr := s.lnd.SelfPubkey(ctx)
	if selfErr != nil || strings.TrimSpace(pubkey) == "" {
		s.finishJob(jobID, "failed", "local pubkey unavailable")
		return
	}
	selfPubkey = pubkey

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
		if err != nil {
			continue
		}
		maxFeePpm := feeMsatToPpm(maxFeeMsat, stat.SuccessAmountSat)
		// Wave 1.2: lift the warm cap to at least 1.05× the historical success
		// fee so a temporary spread shrink does not reject a pair that already
		// proved viable.
		floorPpm := stat.SuccessFeePpm + (stat.SuccessFeePpm / 20)
		if floorPpm > maxFeePpm {
			maxFeePpm = floorPpm
			maxFeeMsat = ppmToFeeLimitMsat(stat.SuccessAmountSat, floorPpm)
		}
		if maxFeePpm <= 0 || stat.SuccessFeePpm > maxFeePpm {
			continue
		}
		if minExecuteSat > 0 && stat.SuccessAmountSat < minExecuteSat {
			continue
		}
		if stat.LastSuccessAt.After(warmAt) {
			warmAt = stat.LastSuccessAt
			warmSourceID = source.ChannelID
			warmAmount = stat.SuccessAmountSat
			warmFeePpm = maxFeePpm
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
	// Structural-failure backoff pacing only — automatic Mission Control reset
	// was removed in 0.4.4 because it was canibalizing the delegated fast-path
	// learning (every 6 structural failures wiped MC, defeating the route
	// memory the fast-path depends on). Operators still have the manual reset
	// button (POST /api/rebalance/mission-control/reset). LND's natural decay
	// (mc_half_life_sec) handles aging penalties on its own.
	autoStructuralBackoff := time.Duration(0)
	autoStructuralBase := 1 * time.Second
	autoStructuralMax := 10 * time.Second
	ignoredEdgeSet := map[string]struct{}{}
	ignoredEdges := make([]*lnrpc.EdgeLocator, 0)
	ignoredPairSet := map[string]struct{}{}
	ignoredPairs := make([]*lnrpc.NodePair, 0)
	maxIgnoredEntries := 500
	ignoredMu := sync.Mutex{}

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

	// Automatic Mission Control reset was removed in 0.4.4 — it canibalized
	// the delegated fast-path learning (every 6 structural failures wiped MC,
	// defeating the route memory the fast-path depends on). The manual reset
	// endpoint (POST /api/rebalance/mission-control/reset) remains as escape
	// hatch. Auto and manual-restart jobs still need the backoff sleep below
	// to pace structural retries within a single job; the reason filter limits
	// it to those paths.
	enableStructuralBackoff := jobSource == "auto" || isManualRestartJob(jobSource, jobReason, false)

	noteAutoStructuralFailure := func(reason string, backoff bool) {
		if !enableStructuralBackoff || !isStructuralRebalanceFailure(reason) {
			return
		}
		if !backoff {
			return
		}
		if autoStructuralBackoff <= 0 {
			autoStructuralBackoff = autoStructuralBase
		} else {
			autoStructuralBackoff *= 2
			if autoStructuralBackoff > autoStructuralMax {
				autoStructuralBackoff = autoStructuralMax
			}
		}
		_ = sleepWithContext(autoStructuralBackoff)
	}

	resetAutoStructuralFailure := func() {
		if enableStructuralBackoff {
			autoStructuralBackoff = 0
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
	snapshotIgnoredRoutes := func() ([]*lnrpc.EdgeLocator, []*lnrpc.NodePair) {
		ignoredMu.Lock()
		defer ignoredMu.Unlock()
		edgeSnapshot := append([]*lnrpc.EdgeLocator(nil), ignoredEdges...)
		pairSnapshot := append([]*lnrpc.NodePair(nil), ignoredPairs...)
		return edgeSnapshot, pairSnapshot
	}
	noteRouteFailureFromShard := func(route *lnrpc.Route, failureIndex uint32) {
		ignoredMu.Lock()
		defer ignoredMu.Unlock()
		noteRouteFailure(route, failureIndex)
	}

	reconcileTimeoutPayment := func(paymentHash string, feeLimitPpm int64, source RebalanceChannel, fallbackAmount int64) (bool, int64) {
		if s.lnd == nil || strings.TrimSpace(paymentHash) == "" {
			return false, 0
		}
		// Race observada em prod (2026-05-28): attempt_timeout_sec dispara
		// segundos depois do LND ter SETTLED o pagamento. LookupPayment no
		// instante do timeout pode retornar IN_FLIGHT/UNKNOWN/INITIATED porque
		// o LND ainda está finalizando bookkeeping interno. Sem retry,
		// marcávamos o attempt como falho, populávamos pair_failure_cache
		// indevidamente, e perdíamos crédito no ledger/budget. Poll curto
		// (até reconcileMaxRetries × reconcileRetryDelay) resolve o gap.
		const reconcileMaxRetries = 3
		const reconcileRetryDelay = 2 * time.Second
		var pay *lnrpc.Payment
		for attempt := 0; attempt <= reconcileMaxRetries; attempt++ {
			lookupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			p, err := s.lnd.LookupPayment(lookupCtx, paymentHash, 2*time.Hour)
			cancel()
			if err == nil && p != nil {
				switch p.Status {
				case lnrpc.Payment_SUCCEEDED:
					pay = p
				case lnrpc.Payment_FAILED:
					return false, 0
				}
			}
			if pay != nil {
				break
			}
			if attempt < reconcileMaxRetries {
				time.Sleep(reconcileRetryDelay)
			}
		}
		if pay == nil {
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
		_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountSent, feeLimitPpm, feePaidSat, "succeeded", paymentHash, "", nil)
		// Hops not available here: this path reconciles a previously-timed-out
		// payment via LookupPayment, which doesn't return the route.
		recordPairSuccess(ctx, source.ChannelID, targetChannelID, amountSent, feeLimitPpm, feePaidSat, nil)
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
		if shouldSkipCurrentJobSource(source.ChannelID) {
			return false, false, 0, false, nil, 0
		}
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

		// Wave 4.1: fast path — try the cached winning hops via BuildRoute
		// before invoking QueryRoutes. Skips pathfinding when the previous
		// success route is still viable. Failures here fall through silently
		// to the regular QueryRoutes flow (no pair failure recorded — the
		// cache may simply be stale).
		if stat, ok := pairStats[source.ChannelID]; ok && len(stat.LastSuccessRouteHops) > 0 && s.lnd != nil {
			if cachedRoute, buildErr := s.lnd.BuildRoute(attemptCtx, amountTry, source.ChannelID, stat.LastSuccessRouteHops); buildErr == nil && cachedRoute != nil {
				cachedFeeMsat := cachedRoute.TotalFeesMsat
				if cachedFeeMsat == 0 && cachedRoute.TotalFees > 0 {
					cachedFeeMsat = cachedRoute.TotalFees * 1000
				}
				if feeLimitMsat <= 0 || cachedFeeMsat <= feeLimitMsat {
					_, paymentHash, paymentAddr, invErr := s.createRebalanceInvoice(attemptCtx, amountTry, jobID, source.ChannelID, targetChannelID)
					if invErr == nil {
						applyMppRecord(cachedRoute, paymentAddr, amountTry)
						if _, sendErr := s.lnd.SendToRoute(attemptCtx, paymentHash, cachedRoute); sendErr == nil {
							cancelAttempt()
							resetAutoStructuralFailure()
							feePaidSat := msatToSatCeil(cachedFeeMsat)
							attemptIndex++
							_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, feePaidSat, "succeeded", paymentHash, "", nil)
							recordPairSuccess(ctx, source.ChannelID, targetChannelID, amountTry, feeLimitPpm, feePaidSat, extractHopPubkeys(cachedRoute))
							_ = s.applyRebalanceLedger(ctx, targetChannelID, amountTry, feePaidSat)
							_ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
							cachedRouteMax := s.maxAmountOnRouteSat(attemptCtx, cachedRoute, selfPubkey)
							if s.logger != nil {
								s.logger.Printf("rebalance job=%d: cached route fast-path succeeded source=%d amount=%d hops=%d", jobID, source.ChannelID, amountTry, len(stat.LastSuccessRouteHops))
							}
							return true, false, cachedRouteMax, false, cachedRoute, amountTry
						}
						// SendToRoute failed; fall through to QueryRoutes.
					}
				}
			}
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
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", "attempt timeout", nil)
				recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
				noteAutoStructuralFailure("attempt timeout", false)
				return false, false, 0, true, nil, 0
			}
			if isStructuralRebalanceFailure(err.Error()) {
				noteAutoStructuralFailure(err.Error(), true)
			} else {
				resetAutoStructuralFailure()
			}
			if logRouteFailure {
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", err.Error(), nil)
				recordPairFailure(ctx, source.ChannelID, targetChannelID, err.Error())
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
					_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, routeAmount, routeFeeLimitPpm, 0, "failed", "", "attempt timeout", nil)
					recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
					return false, false, 0, true, nil, 0
				}
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, routeAmount, routeFeeLimitPpm, 0, "failed", "", err.Error(), nil)
				recordPairFailure(ctx, source.ChannelID, targetChannelID, err.Error())
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
				resetAutoStructuralFailure()
				feePaidSat := msatToSatCeil(routeFeeMsat)
				if feePaidSat == 0 && probeFeeMsat > 0 {
					feePaidSat = msatToSatCeil(probeFeeMsat)
				}
				attemptIndex++
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, routeAmount, routeFeeLimitPpm, feePaidSat, "succeeded", paymentHash, "", nil)
				recordPairSuccess(ctx, source.ChannelID, targetChannelID, routeAmount, routeFeeLimitPpm, feePaidSat, extractHopPubkeys(activeRoute))
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
							resetAutoStructuralFailure()
							feePaidSat := msatToSatCeil(updatedRoute.TotalFeesMsat)
							if feePaidSat == 0 && probeFeeMsat > 0 {
								feePaidSat = msatToSatCeil(probeFeeMsat)
							}
							attemptIndex++
							_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, routeAmount, routeFeeLimitPpm, feePaidSat, "succeeded", paymentHash, "", nil)
							recordPairSuccess(ctx, source.ChannelID, targetChannelID, routeAmount, routeFeeLimitPpm, feePaidSat, extractHopPubkeys(updatedRoute))
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
										_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, maxAmount, retryFeePpm, 0, "failed", retryHash, "route fee exceeds limit", nil)
										recordPairFailure(ctx, source.ChannelID, targetChannelID, "route fee exceeds limit")
										continue
									}
									applyMppRecord(rebuilt, retryAddr, maxAmount)
									probeRouteMax := s.maxAmountOnRouteSat(attemptCtx, rebuilt, selfPubkey)
									_, retrySendErr := s.lnd.SendToRoute(attemptCtx, retryHash, rebuilt)
									if retrySendErr == nil {
										cancelAttempt()
										resetAutoStructuralFailure()
										feePaidSat := msatToSatCeil(rebuilt.TotalFeesMsat)
										if feePaidSat == 0 && rebuilt.TotalFeesMsat > 0 {
											feePaidSat = msatToSatCeil(rebuilt.TotalFeesMsat)
										}
										attemptIndex++
										_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, maxAmount, retryFeePpm, feePaidSat, "succeeded", retryHash, "", nil)
										recordPairSuccess(ctx, source.ChannelID, targetChannelID, maxAmount, retryFeePpm, feePaidSat, extractHopPubkeys(rebuilt))
										_ = s.applyRebalanceLedger(ctx, targetChannelID, maxAmount, feePaidSat)
										_ = s.addBudgetSpend(ctx, feePaidSat, jobSource)
										return true, false, probeRouteMax, false, rebuilt, maxAmount
									}
									attemptIndex++
									_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, maxAmount, retryFeePpm, 0, "failed", retryHash, retrySendErr.Error(), parseRouteFailure(retrySendErr, rebuilt))
									recordPairFailure(ctx, source.ChannelID, targetChannelID, retrySendErr.Error())
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
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, timeoutAmount, timeoutFeePpm, 0, "failed", timeoutHash, "attempt timeout", nil)
			recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
			noteAutoStructuralFailure("attempt timeout", false)
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
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, failAmount, failFeePpm, 0, "failed", lastPaymentHash, failReason, parseRouteFailure(lastErr, nil))
			recordPairFailure(ctx, source.ChannelID, targetChannelID, failReason)
		}
		noteAutoStructuralFailure(failReason, false)
		return false, false, 0, false, nil, 0
	}

	attemptPaymentWithRoute := func(source RebalanceChannel, baseRoute *lnrpc.Route, amountTry int64, feeLimitMsat int64, logRouteFailure bool) (bool, bool, int64, bool, *lnrpc.Route, int64) {
		attemptedAny = true
		if shouldSkipCurrentJobSource(source.ChannelID) {
			return false, false, 0, false, nil, 0
		}
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
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", err.Error(), nil)
				recordPairFailure(ctx, source.ChannelID, targetChannelID, err.Error())
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
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", "route fee exceeds limit", nil)
				recordPairFailure(ctx, source.ChannelID, targetChannelID, "route fee exceeds limit")
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
				_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", "attempt timeout", nil)
				recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
				return false, false, 0, true, nil, 0
			}
			attemptIndex++
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", "", err.Error(), nil)
			recordPairFailure(ctx, source.ChannelID, targetChannelID, err.Error())
			return false, false, 0, false, nil, 0
		}

		applyMppRecord(route, paymentAddr, amountTry)
		_, err = s.lnd.SendToRoute(attemptCtx, paymentHash, route)
		if err == nil {
			cancelAttempt()
			resetAutoStructuralFailure()
			feePaidSat := msatToSatCeil(routeFeeMsat)
			attemptIndex++
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, feePaidSat, "succeeded", paymentHash, "", nil)
			recordPairSuccess(ctx, source.ChannelID, targetChannelID, amountTry, feeLimitPpm, feePaidSat, extractHopPubkeys(route))
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
							resetAutoStructuralFailure()
							feePaidSat := msatToSatCeil(updatedRoute.TotalFeesMsat)
							attemptIndex++
							_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, feePaidSat, "succeeded", paymentHash, "", nil)
							recordPairSuccess(ctx, source.ChannelID, targetChannelID, amountTry, feeLimitPpm, feePaidSat, extractHopPubkeys(updatedRoute))
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
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", paymentHash, "attempt timeout", nil)
			recordPairFailure(ctx, source.ChannelID, targetChannelID, "attempt timeout")
			noteAutoStructuralFailure("attempt timeout", false)
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
			_ = s.insertAttempt(ctx, jobID, attemptIndex, source.ChannelID, amountTry, feeLimitPpm, 0, "failed", paymentHash, failReason, parseRouteFailure(lastErr, route))
			recordPairFailure(ctx, source.ChannelID, targetChannelID, failReason)
		}
		noteAutoStructuralFailure(failReason, false)
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
				if shouldSkipCurrentJobSource(source.ChannelID) {
					return false, false
				}
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
					if shouldSkipCurrentJobSource(source.ChannelID) {
						return false, false
					}
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
				if shouldSkipCurrentJobSource(source.ChannelID) {
					return false, false
				}
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
					if shouldSkipCurrentJobSource(source.ChannelID) {
						return false, false
					}
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
				if shouldSkipCurrentJobSource(source.ChannelID) {
					return false, false
				}
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
					if shouldSkipCurrentJobSource(source.ChannelID) {
						return false, false
					}
				}
			}
		}

		return false, false
	}

	mppPrepass := rebalanceMppPrepassContext{
		runner:                     r,
		state:                      st,
		targetPolicy:               targetPolicy,
		selfPubkey:                 selfPubkey,
		remaining:                  &remaining,
		attemptedAny:               &attemptedAny,
		attemptIndex:               &attemptIndex,
		attemptTimeoutSec:          attemptTimeoutSec,
		refreshSourceAvailability:  refreshSourceAvailability,
		shouldSkipCurrentJobSource: shouldSkipCurrentJobSource,
		snapshotIgnoredRoutes:      snapshotIgnoredRoutes,
		noteRouteFailureFromShard:  noteRouteFailureFromShard,
		finishOnTimeout:            finishOnTimeout,
		recordPairSuccess:          recordPairSuccess,
		recordPairFailure:          recordPairFailure,
		noteAutoStructuralFailure:  noteAutoStructuralFailure,
		applySuccess:               applySuccess,
	}

	if finished, fatal := mppPrepass.run(); fatal {
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
					if shouldSkipPairForRecentFailure(stat, time.Now()) {
						skippedByCache++
						continue
					}
				}
				if shouldSkipCurrentJobSource(source.ChannelID) {
					skippedByCache++
					continue
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
					if shouldSkipCurrentJobSource(source.ChannelID) {
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
					if shouldSkipCurrentJobSource(source.ChannelID) {
						break
					}
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
		s.finishJob(jobID, "skipped", "all sources skipped (recent failures)")
		return
	}
	if !attemptedAny {
		s.finishJob(jobID, "skipped", "no executable sources")
		return
	}
	s.finishJob(jobID, "failed", "all sources failed")
}

func (m *rebalanceMppPrepassContext) run() (bool, bool) {
	r := m.runner
	st := m.state
	s := r.service
	ctx := st.ctx
	feeCfg := st.feeCfg
	if st.cooldownProbeJob {
		return false, false
	}
	if !shouldRunMppExecute(feeCfg, r.jobSource) {
		return false, false
	}
	if *m.remaining <= 0 {
		return true, false
	}
	m.refreshSourceAvailability(false)

	allowedSources := make([]RebalanceChannel, 0, len(st.sources))
	sourceByID := make(map[uint64]RebalanceChannel, len(st.sources))
	for _, source := range st.sources {
		availableCap := st.sourceAvailable[source.ChannelID]
		if availableCap <= 0 {
			continue
		}
		if st.useRecentFailureCache {
			if stat, ok := st.pairStats[source.ChannelID]; ok {
				if shouldSkipPairForRecentFailure(stat, time.Now()) {
					continue
				}
			}
			if m.shouldSkipCurrentJobSource(source.ChannelID) {
				continue
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

	plan := buildMppShadowPlan(*m.remaining, allowedSources, feeCfg)
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

	tasks := make([]rebalanceMppShardTask, 0, plan.PlannedShards)
	planSourceRemaining := make(map[uint64]int64, len(allowedSources))
	for _, source := range allowedSources {
		planSourceRemaining[source.ChannelID] = source.MaxSourceSat
	}
	remainingForPlan := *m.remaining
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
		if st.minExecuteSat > 0 && amountTry < st.minExecuteSat {
			continue
		}
		tasks = append(tasks, rebalanceMppShardTask{
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
	resultsCh := make(chan rebalanceMppShardAttemptResult, len(tasks))
	var wg sync.WaitGroup
	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := m.attemptShard(roundCtx, task.Source, task.AmountSat)
			res.ShardIndex = task.ShardIndex
			resultsCh <- res
		}()
	}
	wg.Wait()
	close(resultsCh)

	results := make([]rebalanceMppShardAttemptResult, 0, len(tasks))
	for res := range resultsCh {
		results = append(results, res)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].ShardIndex < results[j].ShardIndex
	})

	attemptedShards := 0
	succeededShards := 0
	structuralFailureShards := 0
	attemptedSourceIDs := make(map[uint64]struct{}, len(allowedSources))
	structuralFailureSourceIDs := make(map[uint64]struct{}, len(allowedSources))
	sourceSuccessRemaining := make(map[uint64]int64, len(allowedSources))
	for _, source := range allowedSources {
		sourceSuccessRemaining[source.ChannelID] = source.MaxSourceSat
	}

	for _, res := range results {
		if res.Fatal {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				m.finishOnTimeout()
			} else {
				s.finishJob(r.jobID, "cancelled", "cancelled")
			}
			return false, true
		}
		if !res.Attempted {
			continue
		}
		*m.attemptedAny = true
		attemptedShards++
		if res.Source.ChannelID != 0 {
			attemptedSourceIDs[res.Source.ChannelID] = struct{}{}
		}

		*m.attemptIndex = *m.attemptIndex + 1
		attemptIndex := *m.attemptIndex
		attemptAmount := res.AmountRequested
		if res.AmountSent > 0 {
			attemptAmount = res.AmountSent
		}
		if res.Succeeded {
			_ = s.insertAttempt(ctx, r.jobID, attemptIndex, res.Source.ChannelID, attemptAmount, res.FeeLimitPpm, res.FeePaidSat, "succeeded", res.PaymentHash, "", nil)
			m.recordPairSuccess(ctx, res.Source.ChannelID, r.targetChannelID, attemptAmount, res.FeeLimitPpm, res.FeePaidSat, res.RouteHops)
			_ = s.applyRebalanceLedger(ctx, r.targetChannelID, attemptAmount, res.FeePaidSat)
			_ = s.addBudgetSpend(ctx, res.FeePaidSat, r.jobSource)
			succeededShards++

			routeCap := int64(0)
			sourceCap := sourceSuccessRemaining[res.Source.ChannelID]
			if m.applySuccess(attemptAmount, res.RouteMaxSat, &routeCap, &sourceCap) {
				sourceSuccessRemaining[res.Source.ChannelID] = sourceCap
				st.sourceAvailable[res.Source.ChannelID] = sourceCap
				if s.logger != nil {
					s.logger.Printf("rebalance mpp execute: job=%d completed via parallel prepass shards=%d/%d", r.jobID, succeededShards, attemptedShards)
				}
				return true, false
			}
			sourceSuccessRemaining[res.Source.ChannelID] = sourceCap
			st.sourceAvailable[res.Source.ChannelID] = sourceCap
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
		if isStructuralRebalanceFailure(failReason) {
			structuralFailureShards++
			if res.Source.ChannelID != 0 {
				structuralFailureSourceIDs[res.Source.ChannelID] = struct{}{}
			}
		}
		shardFailReason := formatMppShardFailReason(failReason)
		_ = s.insertAttempt(ctx, r.jobID, attemptIndex, res.Source.ChannelID, attemptAmount, res.FeeLimitPpm, 0, "failed", res.PaymentHash, shardFailReason, res.FailureInfo)
		m.recordPairFailure(ctx, res.Source.ChannelID, r.targetChannelID, shardFailReason)
		m.noteAutoStructuralFailure(shardFailReason, false)
	}

	if shouldAbortMppStructuralFallback(succeededShards, attemptedShards, structuralFailureShards, len(attemptedSourceIDs), len(structuralFailureSourceIDs)) {
		structuralRatio := float64(structuralFailureShards) / float64(attemptedShards)
		sourceStructuralRatio := float64(len(structuralFailureSourceIDs)) / float64(len(attemptedSourceIDs))
		if hasRebalanceFallbackCandidate(st.sources, st.sourceAvailable, st.pairStats, st.currentJobBlockedPairs, st.useRecentFailureCache, st.minExecuteSat, time.Now()) {
			if s.logger != nil {
				s.logger.Printf(
					"rebalance mpp execute: job=%d structural threshold reached; falling back to legacy structural_failures=%d attempted_shards=%d structural_sources=%d attempted_sources=%d shard_ratio=%.2f source_ratio=%.2f",
					r.jobID,
					structuralFailureShards,
					attemptedShards,
					len(structuralFailureSourceIDs),
					len(attemptedSourceIDs),
					structuralRatio,
					sourceStructuralRatio,
				)
			}
			return false, false
		}
		m.noteAutoStructuralFailure("mpp structural failure", false)
		s.finishJob(r.jobID, "failed", "mpp structural failure")
		if s.logger != nil {
			s.logger.Printf(
				"rebalance mpp execute: job=%d aborting fallback structural_failures=%d attempted_shards=%d structural_sources=%d attempted_sources=%d shard_ratio=%.2f source_ratio=%.2f",
				r.jobID,
				structuralFailureShards,
				attemptedShards,
				len(structuralFailureSourceIDs),
				len(attemptedSourceIDs),
				structuralRatio,
				sourceStructuralRatio,
			)
		}
		return true, false
	}

	if s.logger != nil {
		s.logger.Printf(
			"rebalance mpp execute: job=%d parallel prepass attempted_shards=%d succeeded_shards=%d remaining=%d (fallback legacy)",
			r.jobID,
			attemptedShards,
			succeededShards,
			*m.remaining,
		)
	}
	return false, false
}

func (m *rebalanceMppPrepassContext) attemptShard(roundCtx context.Context, source RebalanceChannel, amountTry int64) rebalanceMppShardAttemptResult {
	r := m.runner
	st := m.state
	s := r.service
	ctx := st.ctx
	feeCfg := st.feeCfg
	result := rebalanceMppShardAttemptResult{
		Source:          source,
		AmountRequested: amountTry,
		AmountSent:      amountTry,
		Attempted:       true,
	}
	if amountTry <= 0 {
		result.Attempted = false
		return result
	}
	if feeCfg.MinSplitEnabled && st.minExecuteSat > 0 && amountTry < st.minExecuteSat {
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
	feeLimitMsat, feeErr := calcFeeLimitMsat(amountTry*1000, m.targetPolicy, &sourcePolicy, feeCfg)
	if feeErr != nil || feeLimitMsat <= 0 {
		result.FailReason = "fee cap zero"
		return result
	}
	result.FeeLimitPpm = feeMsatToPpm(feeLimitMsat, amountTry)

	attemptCtx := roundCtx
	cancelAttempt := func() {}
	if m.attemptTimeoutSec > 0 {
		attemptCtx, cancelAttempt = context.WithTimeout(roundCtx, time.Duration(m.attemptTimeoutSec)*time.Second)
	}
	defer cancelAttempt()

	ignoredEdgeSnapshot, ignoredPairSnapshot := m.snapshotIgnoredRoutes()
	routes, err := s.lnd.QueryRoutes(attemptCtx, m.selfPubkey, amountTry, source.ChannelID, st.targetSnapshot.RemotePubkey, feeLimitMsat, 3, ignoredEdgeSnapshot, ignoredPairSnapshot)
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
		maxAmount, probeErr := s.probeRoute(attemptCtx, route, amountTry, st.minProbeSat, feeCfg.AmountProbeSteps, m.targetPolicy, sourcePolicy, feeCfg)
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
	if feeCfg.MinSplitEnabled && st.minExecuteSat > 0 && routeAmount < st.minExecuteSat {
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
		feeLimitMsat, feeErr = calcFeeLimitMsat(routeAmount*1000, m.targetPolicy, &sourcePolicy, feeCfg)
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

	_, paymentHash, paymentAddr, invErr := s.createRebalanceInvoice(attemptCtx, routeAmount, r.jobID, source.ChannelID, r.targetChannelID)
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
	result.RouteMaxSat = s.maxAmountOnRouteSat(attemptCtx, route, m.selfPubkey)

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
		var routeFailure lndclient.RouteFailureError
		if errors.As(sendErr, &routeFailure) && routeFailure.Failure != nil && routeFailure.Code == lnrpc.Failure_TEMPORARY_CHANNEL_FAILURE {
			m.noteRouteFailureFromShard(route, routeFailure.FailureSourceIndex)
		}
		result.FailureInfo = parseRouteFailure(sendErr, route)
		result.FailReason = sendErr.Error()
		return result
	}

	result.Succeeded = true
	result.AmountSent = routeAmount
	result.FeePaidSat = msatToSatCeil(routeFeeMsat)
	result.RouteHops = extractHopPubkeys(route)
	return result
}

func formatMppShardFailReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		trimmed = "failed"
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "mpp shard:") {
		return trimmed
	}
	return "mpp shard: " + trimmed
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

// extractHopPubkeys returns the pubkey path of a route as a slice of hex
// strings, suitable for `BuildRoute` reuse on the next attempt (Wave 4.1).
// Empty pubkeys are skipped so a malformed route doesn't poison the cache.
func extractHopPubkeys(route *lnrpc.Route) []string {
	if route == nil {
		return nil
	}
	hops := make([]string, 0, len(route.Hops))
	for _, h := range route.Hops {
		if h == nil {
			continue
		}
		pk := strings.TrimSpace(h.PubKey)
		if pk == "" {
			continue
		}
		hops = append(hops, pk)
	}
	return hops
}

// reinforceMissionControl pushes positive history entries into LND's mission
// control for each pair along a successful rebalance route (Wave 4.4). Only
// fires when cfg.MissionControlReinforce is true. selfPubkey anchors the path
// because routeHops is the pubkey list AFTER the origin (LND convention). All
// RPC errors are logged but not returned — reinforcement is best-effort and
// must never block a successful job's accounting.
func (s *RebalanceService) reinforceMissionControl(selfPubkey string, routeHops []string, amountSat int64) {
	if s.lnd == nil || len(routeHops) == 0 || amountSat <= 0 {
		return
	}
	updates := make([]lndclient.MissionControlPairUpdate, 0, len(routeHops))
	now := time.Now()
	prevPubkey := strings.TrimSpace(selfPubkey)
	for _, hop := range routeHops {
		nextPubkey := strings.TrimSpace(hop)
		if prevPubkey == "" || nextPubkey == "" {
			prevPubkey = nextPubkey
			continue
		}
		updates = append(updates, lndclient.MissionControlPairUpdate{
			NodeFromPubkey: prevPubkey,
			NodeToPubkey:   nextPubkey,
			SuccessTime:    now,
			SuccessAmtSat:  amountSat,
		})
		prevPubkey = nextPubkey
	}
	if len(updates) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.lnd.ImportMissionControl(ctx, updates); err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance MC reinforce failed (pairs=%d): %v", len(updates), err)
		}
		return
	}
	if s.logger != nil {
		s.logger.Printf("rebalance MC reinforce: %d pairs updated for amount=%d sats", len(updates), amountSat)
	}
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

func (s *RebalanceService) recordPairSuccess(ctx context.Context, sourceID uint64, targetID uint64, amountSat int64, feePpm int64, feePaidSat int64, routeHops []string) {
	if s.db == nil || sourceID == 0 || targetID == 0 {
		return
	}
	now := time.Now().UTC()
	// Wave 4.1: serialize hops as JSON for the next job to attempt BuildRoute
	// before falling back to QueryRoutes. nil hops → store SQL null.
	var hopsParam any
	if len(routeHops) > 0 {
		if data, err := json.Marshal(routeHops); err == nil {
			hopsParam = string(data)
		}
	}
	_, _ = s.db.Exec(ctx, `
insert into rebalance_pair_stats (
  source_channel_id, target_channel_id, last_success_at, success_amount_sat, success_fee_ppm, success_fee_paid_sat, success_count, last_success_route_hops
) values ($1,$2,$3,$4,$5,$6,1,$7)
 on conflict (source_channel_id, target_channel_id) do update set
  last_success_at = excluded.last_success_at,
  last_fail_at = null,
  last_fail_reason = null,
  fail_count = 0,
  permanent_fail_score = 0,
  permanent_fail_updated_at = excluded.last_success_at,
  success_amount_sat = excluded.success_amount_sat,
  success_fee_ppm = excluded.success_fee_ppm,
  success_fee_paid_sat = excluded.success_fee_paid_sat,
  success_count = rebalance_pair_stats.success_count + 1,
  last_success_route_hops = coalesce(excluded.last_success_route_hops, rebalance_pair_stats.last_success_route_hops)
`, int64(sourceID), int64(targetID), now, amountSat, feePpm, feePaidSat, hopsParam)
}

func (s *RebalanceService) recordPairFailure(ctx context.Context, sourceID uint64, targetID uint64, reason string) {
	if s.db == nil || sourceID == 0 || targetID == 0 {
		return
	}
	now := time.Now().UTC()
	permanentIncrement := permanentFailScoreIncrement(reason)
	_, _ = s.db.Exec(ctx, `
insert into rebalance_pair_stats (
  source_channel_id, target_channel_id, last_fail_at, last_fail_reason, fail_count, permanent_fail_score, permanent_fail_updated_at
) values ($1,$2,$3,$4,1,$5,case when $5::double precision > 0 then $3::timestamptz else null end)
 on conflict (source_channel_id, target_channel_id) do update set
  last_fail_at = excluded.last_fail_at,
  last_fail_reason = excluded.last_fail_reason,
  fail_count = rebalance_pair_stats.fail_count + 1,
  permanent_fail_score = case
    when $5::double precision <= 0 then rebalance_pair_stats.permanent_fail_score
    else least($7::double precision,
      (greatest(0, coalesce(rebalance_pair_stats.permanent_fail_score, 0)) *
        case
          when rebalance_pair_stats.permanent_fail_updated_at is null then 1
          else power(0.5, greatest(0, extract(epoch from ($3::timestamptz - rebalance_pair_stats.permanent_fail_updated_at))) / $6::double precision)
        end
      ) + $5::double precision)
  end,
  permanent_fail_updated_at = case
    when $5::double precision > 0 then excluded.permanent_fail_updated_at
    else rebalance_pair_stats.permanent_fail_updated_at
  end
`, int64(sourceID), int64(targetID), now, nullableString(reason), permanentIncrement, permanentFailScoreHalfLife.Seconds(), permanentFailScoreMax)
}

func (s *RebalanceService) loadPairStatsForTarget(ctx context.Context, targetID uint64) map[uint64]pairStat {
	stats := map[uint64]pairStat{}
	if s.db == nil || targetID == 0 {
		return stats
	}
	rows, err := s.db.Query(ctx, `
select source_channel_id, target_channel_id, last_success_at, last_fail_at, last_fail_reason, success_count, fail_count, success_amount_sat, success_fee_ppm, last_success_route_hops, permanent_fail_score, permanent_fail_updated_at
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
		var lastFailReason pgtype.Text
		var successCount int
		var failCount int
		var successAmount int64
		var successFee int64
		var routeHopsRaw []byte
		var permanentFailScore float64
		var permanentFailUpdated pgtype.Timestamptz
		if err := rows.Scan(&sourceID, &targetIDRow, &lastSuccess, &lastFail, &lastFailReason, &successCount, &failCount, &successAmount, &successFee, &routeHopsRaw, &permanentFailScore, &permanentFailUpdated); err != nil {
			return stats
		}
		stat := pairStat{
			SourceChannelID:    uint64(sourceID),
			TargetChannelID:    uint64(targetIDRow),
			SuccessCount:       successCount,
			FailCount:          failCount,
			PermanentFailScore: permanentFailScore,
			SuccessAmountSat:   successAmount,
			SuccessFeePpm:      successFee,
		}
		if lastSuccess.Valid {
			stat.LastSuccessAt = lastSuccess.Time
		}
		if lastFail.Valid {
			stat.LastFailAt = lastFail.Time
		}
		if lastFailReason.Valid {
			stat.LastFailReason = lastFailReason.String
		}
		if permanentFailUpdated.Valid {
			stat.PermanentFailUpdated = permanentFailUpdated.Time
		}
		if len(routeHopsRaw) > 0 {
			var hops []string
			if err := json.Unmarshal(routeHopsRaw, &hops); err == nil && len(hops) > 0 {
				stat.LastSuccessRouteHops = hops
			}
		}
		stats[uint64(sourceID)] = stat
	}
	return stats
}

func (s *RebalanceService) loadPairStatsSummaryForTargets(ctx context.Context, targetIDs []uint64, now time.Time) map[uint64]rebalanceTargetPairStats {
	summaries := map[uint64]rebalanceTargetPairStats{}
	if s.db == nil || len(targetIDs) == 0 {
		return summaries
	}
	ids := make([]int64, 0, len(targetIDs))
	seen := map[uint64]struct{}{}
	for _, id := range targetIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	if len(ids) == 0 {
		return summaries
	}
	if now.IsZero() {
		now = time.Now()
	}
	rows, err := s.db.Query(ctx, `
select target_channel_id, last_success_at, last_fail_at, last_fail_reason, success_count, fail_count, permanent_fail_score, permanent_fail_updated_at
from rebalance_pair_stats
where target_channel_id = any($1::bigint[])
`, ids)
	if err != nil {
		return summaries
	}
	defer rows.Close()
	for rows.Next() {
		var targetID int64
		var lastSuccess pgtype.Timestamptz
		var lastFail pgtype.Timestamptz
		var lastFailReason pgtype.Text
		var successCount int
		var failCount int
		var permanentFailScore float64
		var permanentFailUpdated pgtype.Timestamptz
		if err := rows.Scan(&targetID, &lastSuccess, &lastFail, &lastFailReason, &successCount, &failCount, &permanentFailScore, &permanentFailUpdated); err != nil {
			return summaries
		}
		if targetID <= 0 {
			continue
		}
		key := uint64(targetID)
		stat := summaries[key]
		stat.TargetChannelID = key
		stat.Successes += successCount
		stat.Failures += failCount
		stat.Attempts += successCount + failCount
		if lastSuccess.Valid && (stat.LastSuccessAt.IsZero() || lastSuccess.Time.After(stat.LastSuccessAt)) {
			stat.LastSuccessAt = lastSuccess.Time
		}
		if lastFail.Valid {
			if stat.LastFailAt.IsZero() || lastFail.Time.After(stat.LastFailAt) {
				stat.LastFailAt = lastFail.Time
			}
			if lastFailReason.Valid && isStructuralRebalanceFailure(lastFailReason.String) && now.Sub(lastFail.Time) <= targetFailedCooldownWindow {
				stat.RecentStructuralFailures++
			}
		}
		stat.PermanentFailScore += decayedPermanentFailScore(permanentFailScore, permanentFailUpdated.Time, now)
		summaries[key] = stat
	}
	return summaries
}

func (s *RebalanceService) loadRecentSovereignTargetStats(ctx context.Context, cfg RebalanceConfig, targetIDs []uint64, now time.Time) map[uint64]rebalanceTargetPairStats {
	summaries := map[uint64]rebalanceTargetPairStats{}
	if len(targetIDs) == 0 {
		return summaries
	}
	ids := make([]int64, 0, len(targetIDs))
	seen := map[uint64]struct{}{}
	for _, id := range targetIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
		summaries[id] = rebalanceTargetPairStats{
			TargetChannelID:   id,
			RecentStatsLoaded: true,
		}
	}
	if s.db == nil || len(ids) == 0 {
		return summaries
	}
	if now.IsZero() {
		now = time.Now()
	}
	attributionHours := sovereignAttributionWindowHoursForConfig(cfg)
	slowHours := sovereignSlowSellerWindowHoursForConfig(cfg)
	recentWindow := sovereignRecentTargetStatsWindow
	slowWindow := time.Duration(slowHours) * time.Hour
	if slowWindow > recentWindow {
		recentWindow = slowWindow
	}
	since := now.Add(-recentWindow)
	rows, err := s.db.Query(ctx, `
with recent_jobs as (
  select id, target_channel_id, status, reason, completed_at
  from rebalance_jobs
  where target_channel_id = any($1::bigint[])
    and completed_at is not null
    and completed_at >= $2
    and status in ('succeeded','partial','failed')
),
attempt_totals as (
  select
    r.id,
    coalesce(sum(a.amount_sat) filter (where a.status='succeeded'), 0) as sent_sat,
    coalesce(sum(a.fee_paid_sat) filter (where a.status='succeeded'), 0) as fee_paid_sat
  from recent_jobs r
  left join rebalance_attempts a on a.job_id = r.id
  group by r.id
),
forward_totals as (
  select
    r.id,
    coalesce(sum(n.amount_sat) filter (where n.occurred_at < r.completed_at + interval '24 hours'), 0) as forward_24h_amount_sat,
    coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end) filter (where n.occurred_at < r.completed_at + interval '24 hours'), 0)::bigint as forward_24h_fee_msat,
    coalesce(sum(n.amount_sat) filter (where n.occurred_at < r.completed_at + ($3::int * interval '1 hour')), 0) as forward_window_amount_sat,
    coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end) filter (where n.occurred_at < r.completed_at + ($3::int * interval '1 hour')), 0)::bigint as forward_window_fee_msat,
    coalesce(sum(n.amount_sat), 0) as forward_slow_amount_sat,
    coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end), 0)::bigint as forward_slow_fee_msat
  from recent_jobs r
  left join notifications n on n.type='forward'
    and n.channel_id = r.target_channel_id
    and n.occurred_at >= r.completed_at
    and n.occurred_at < r.completed_at + ($4::int * interval '1 hour')
  group by r.id
),
job_raw as (
  select
    r.target_channel_id,
    r.status,
    r.reason,
    r.completed_at,
    coalesce(a.sent_sat, 0) as sent_sat,
    coalesce(a.fee_paid_sat, 0) as fee_paid_sat,
    coalesce(f.forward_24h_amount_sat, 0) as forward_24h_amount_sat,
    coalesce(f.forward_24h_fee_msat, 0) as forward_24h_fee_msat,
    coalesce(f.forward_window_amount_sat, 0) as forward_window_amount_sat,
    coalesce(f.forward_window_fee_msat, 0) as forward_window_fee_msat,
    coalesce(f.forward_slow_amount_sat, 0) as forward_slow_amount_sat,
    coalesce(f.forward_slow_fee_msat, 0) as forward_slow_fee_msat
  from recent_jobs r
  left join attempt_totals a on a.id = r.id
  left join forward_totals f on f.id = r.id
),
job_economics as (
  select
    target_channel_id,
    status,
    reason,
    completed_at,
    sent_sat,
    fee_paid_sat,
    case
      when sent_sat <= 0 or forward_24h_amount_sat <= 0 then 0
      when forward_24h_amount_sat > sent_sat then sent_sat
      else forward_24h_amount_sat
    end as attributed_forward_24h_sat,
    case
      when sent_sat <= 0 or forward_24h_amount_sat <= 0 or forward_24h_fee_msat <= 0 then 0
      when forward_24h_amount_sat > sent_sat then (forward_24h_fee_msat * sent_sat) / forward_24h_amount_sat
      else forward_24h_fee_msat
    end as attributed_forward_24h_fee_msat,
    case
      when sent_sat <= 0 or forward_window_amount_sat <= 0 then 0
      when forward_window_amount_sat > sent_sat then sent_sat
      else forward_window_amount_sat
    end as attributed_forward_window_sat,
    case
      when sent_sat <= 0 or forward_window_amount_sat <= 0 or forward_window_fee_msat <= 0 then 0
      when forward_window_amount_sat > sent_sat then (forward_window_fee_msat * sent_sat) / forward_window_amount_sat
      else forward_window_fee_msat
    end as attributed_forward_window_fee_msat,
    case
      when sent_sat <= 0 or forward_slow_amount_sat <= 0 then 0
      when forward_slow_amount_sat > sent_sat then sent_sat
      else forward_slow_amount_sat
    end as attributed_forward_slow_sat,
    case
      when sent_sat <= 0 or forward_slow_amount_sat <= 0 or forward_slow_fee_msat <= 0 then 0
      when forward_slow_amount_sat > sent_sat then (forward_slow_fee_msat * sent_sat) / forward_slow_amount_sat
      else forward_slow_fee_msat
    end as attributed_forward_slow_fee_msat
  from job_raw
)
select
  target_channel_id,
  count(*) filter (where status in ('succeeded','partial','failed')) as jobs,
  count(*) filter (where status in ('succeeded','partial')) as success_jobs,
  count(*) filter (where status='failed') as failed_jobs,
  count(*) filter (where status='failed' and reason='all sources failed') as all_sources_failed_jobs,
  max(completed_at) filter (where status in ('succeeded','partial')) as last_success_at,
  max(completed_at) filter (where status='failed') as last_fail_at,
  count(*) filter (where sent_sat > 0) as sent_jobs,
  coalesce(sum(sent_sat), 0) as sent_sat,
  coalesce(sum(fee_paid_sat), 0) as fee_paid_sat,
  coalesce(sum(attributed_forward_24h_sat), 0) as attributed_forward_24h_sat,
  coalesce(sum(attributed_forward_24h_fee_msat), 0)::bigint as attributed_forward_24h_fee_msat,
  coalesce(sum(attributed_forward_window_sat), 0) as attributed_forward_window_sat,
  coalesce(sum(attributed_forward_window_fee_msat), 0)::bigint as attributed_forward_window_fee_msat,
  coalesce(sum(attributed_forward_slow_sat), 0) as attributed_forward_slow_sat,
  coalesce(sum(attributed_forward_slow_fee_msat), 0)::bigint as attributed_forward_slow_fee_msat
from job_economics
group by target_channel_id
`, ids, since, attributionHours, slowHours)
	if err != nil {
		return summaries
	}
	defer rows.Close()
	for rows.Next() {
		var targetID int64
		var attempts int64
		var successes int64
		var failures int64
		var allSourcesFailed int64
		var lastSuccess pgtype.Timestamptz
		var lastFail pgtype.Timestamptz
		var sentJobs int64
		var sentSat int64
		var feePaidSat int64
		var forward24hSat int64
		var forward24hFeeMsat int64
		var forwardWindowSat int64
		var forwardWindowFeeMsat int64
		var forwardSlowSat int64
		var forwardSlowFeeMsat int64
		if err := rows.Scan(
			&targetID,
			&attempts,
			&successes,
			&failures,
			&allSourcesFailed,
			&lastSuccess,
			&lastFail,
			&sentJobs,
			&sentSat,
			&feePaidSat,
			&forward24hSat,
			&forward24hFeeMsat,
			&forwardWindowSat,
			&forwardWindowFeeMsat,
			&forwardSlowSat,
			&forwardSlowFeeMsat,
		); err != nil {
			return summaries
		}
		if targetID <= 0 {
			continue
		}
		key := uint64(targetID)
		stat := summaries[key]
		stat.TargetChannelID = key
		stat.RecentStatsLoaded = true
		stat.RecentAttempts = int(attempts)
		stat.RecentSuccesses = int(successes)
		stat.RecentFailures = int(failures)
		stat.RecentAllSourcesFailed = int(allSourcesFailed)
		stat.RecentSentJobs = int(sentJobs)
		stat.RecentSentSat = sentSat
		stat.RecentRebalanceFeeSat = feePaidSat
		stat.RecentForward24hAmountSat = forward24hSat
		forward24hFeeSat := forward24hFeeMsat / 1000
		stat.RecentForward24hFeeSat = forward24hFeeSat
		stat.RecentRealizedNet24hSat = forward24hFeeSat - feePaidSat
		stat.RecentForwardAmountSat = forwardWindowSat
		forwardWindowFeeSat := forwardWindowFeeMsat / 1000
		stat.RecentForwardFeeSat = forwardWindowFeeSat
		stat.RecentRealizedNetSat = forwardWindowFeeSat - feePaidSat
		stat.RecentForwardSlowAmountSat = forwardSlowSat
		forwardSlowFeeSat := forwardSlowFeeMsat / 1000
		stat.RecentForwardSlowFeeSat = forwardSlowFeeSat
		stat.RecentRealizedNetSlowSat = forwardSlowFeeSat - feePaidSat
		if lastSuccess.Valid {
			stat.RecentLastSuccessAt = lastSuccess.Time
		}
		if lastFail.Valid {
			stat.RecentLastFailAt = lastFail.Time
		}
		summaries[key] = stat
	}
	return summaries
}

func mergeRecentSovereignTargetStats(base map[uint64]rebalanceTargetPairStats, recent map[uint64]rebalanceTargetPairStats) {
	for targetID, recentStat := range recent {
		stat := base[targetID]
		stat.TargetChannelID = targetID
		stat.RecentStatsLoaded = recentStat.RecentStatsLoaded
		stat.RecentAttempts = recentStat.RecentAttempts
		stat.RecentSuccesses = recentStat.RecentSuccesses
		stat.RecentFailures = recentStat.RecentFailures
		stat.RecentAllSourcesFailed = recentStat.RecentAllSourcesFailed
		stat.RecentLastSuccessAt = recentStat.RecentLastSuccessAt
		stat.RecentLastFailAt = recentStat.RecentLastFailAt
		stat.RecentSentJobs = recentStat.RecentSentJobs
		stat.RecentSentSat = recentStat.RecentSentSat
		stat.RecentRebalanceFeeSat = recentStat.RecentRebalanceFeeSat
		stat.RecentForward24hAmountSat = recentStat.RecentForward24hAmountSat
		stat.RecentForward24hFeeSat = recentStat.RecentForward24hFeeSat
		stat.RecentRealizedNet24hSat = recentStat.RecentRealizedNet24hSat
		stat.RecentForwardAmountSat = recentStat.RecentForwardAmountSat
		stat.RecentForwardFeeSat = recentStat.RecentForwardFeeSat
		stat.RecentRealizedNetSat = recentStat.RecentRealizedNetSat
		stat.RecentForwardSlowAmountSat = recentStat.RecentForwardSlowAmountSat
		stat.RecentForwardSlowFeeSat = recentStat.RecentForwardSlowFeeSat
		stat.RecentRealizedNetSlowSat = recentStat.RecentRealizedNetSlowSat
		base[targetID] = stat
	}
}

func (s *RebalanceService) PairStats(ctx context.Context, targetID uint64) ([]RebalancePairStat, error) {
	if targetID == 0 {
		return []RebalancePairStat{}, nil
	}
	stats := s.loadPairStatsForTarget(ctx, targetID)
	if len(stats) == 0 {
		return []RebalancePairStat{}, nil
	}

	aliases := map[uint64]string{}
	points := map[uint64]string{}
	if s.lnd != nil {
		if channels, err := s.lnd.ListChannels(ctx); err == nil {
			for _, ch := range channels {
				aliases[ch.ChannelID] = ch.PeerAlias
				points[ch.ChannelID] = ch.ChannelPoint
			}
		}
	}

	now := time.Now()
	result := make([]RebalancePairStat, 0, len(stats))
	for _, stat := range stats {
		item := RebalancePairStat{
			SourceChannelID:      stat.SourceChannelID,
			SourceChannelPoint:   points[stat.SourceChannelID],
			SourcePeerAlias:      aliases[stat.SourceChannelID],
			TargetChannelID:      stat.TargetChannelID,
			TargetChannelPoint:   points[stat.TargetChannelID],
			TargetPeerAlias:      aliases[stat.TargetChannelID],
			LastFailReason:       stat.LastFailReason,
			SuccessCount:         stat.SuccessCount,
			FailCount:            stat.FailCount,
			PermanentFailScore:   decayedPermanentFailScore(stat.PermanentFailScore, stat.PermanentFailUpdated, now),
			SuccessAmountSat:     stat.SuccessAmountSat,
			SuccessFeePpm:        stat.SuccessFeePpm,
			LastSuccessRouteHops: append([]string(nil), stat.LastSuccessRouteHops...),
		}
		if !stat.LastSuccessAt.IsZero() {
			item.LastSuccessAt = stat.LastSuccessAt.UTC().Format(time.RFC3339)
		}
		if !stat.LastFailAt.IsZero() {
			item.LastFailAt = stat.LastFailAt.UTC().Format(time.RFC3339)
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		left := latestPairStatEventTime(result[i])
		right := latestPairStatEventTime(result[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		if result[i].PermanentFailScore != result[j].PermanentFailScore {
			return result[i].PermanentFailScore > result[j].PermanentFailScore
		}
		return result[i].SourceChannelID < result[j].SourceChannelID
	})
	return result, nil
}

func latestPairStatEventTime(stat RebalancePairStat) time.Time {
	latest := time.Time{}
	if stat.LastSuccessAt != "" {
		if parsed, err := time.Parse(time.RFC3339, stat.LastSuccessAt); err == nil {
			latest = parsed
		}
	}
	if stat.LastFailAt != "" {
		if parsed, err := time.Parse(time.RFC3339, stat.LastFailAt); err == nil && (latest.IsZero() || parsed.After(latest)) {
			latest = parsed
		}
	}
	return latest
}

func (s *RebalanceService) loadRecentSourceCooldowns(ctx context.Context, since time.Time) map[uint64]recentCooldownStat {
	stats := map[uint64]recentCooldownStat{}
	if s.db == nil {
		return stats
	}
	rows, err := s.db.Query(ctx, `
with events as (
  select
    source_channel_id,
    status,
    coalesce(finished_at, started_at) as occurred_at
  from rebalance_attempts
  where coalesce(finished_at, started_at) >= $1
),
last_success as (
  select source_channel_id, max(occurred_at) as last_success_at
  from events
  where status='succeeded'
  group by source_channel_id
),
pressure as (
  select e.*, ls.last_success_at
  from events e
  left join last_success ls using (source_channel_id)
  where ls.last_success_at is null or e.occurred_at > ls.last_success_at
)
select source_channel_id,
  count(*) as attempts,
  coalesce(sum(case when status='succeeded' then 1 else 0 end), 0) as successes,
  coalesce(sum(case when status<>'succeeded' then 1 else 0 end), 0) as failures,
  max(occurred_at) as last_attempt_at,
  max(case when status<>'succeeded' then occurred_at else null end) as last_failure_at,
  max(last_success_at) as last_success_at
from pressure
group by source_channel_id
`, since)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var attempts int
		var successes int
		var failures int
		var lastAttempt pgtype.Timestamptz
		var lastFailure pgtype.Timestamptz
		var lastSuccess pgtype.Timestamptz
		if err := rows.Scan(&channelID, &attempts, &successes, &failures, &lastAttempt, &lastFailure, &lastSuccess); err != nil {
			return stats
		}
		stat := recentCooldownStat{
			ChannelID: uint64(channelID),
			Attempts:  attempts,
			Successes: successes,
			Failures:  failures,
		}
		if lastAttempt.Valid {
			stat.LastAttemptAt = lastAttempt.Time
		}
		if lastFailure.Valid {
			stat.LastFailureAt = lastFailure.Time
		}
		if lastSuccess.Valid {
			stat.LastSuccessAt = lastSuccess.Time
		}
		stats[uint64(channelID)] = stat
	}
	return stats
}

func (s *RebalanceService) loadRecentTargetCooldowns(ctx context.Context, since time.Time) map[uint64]recentCooldownStat {
	stats := map[uint64]recentCooldownStat{}
	if s.db == nil {
		return stats
	}
	rows, err := s.db.Query(ctx, `
with events as (
  select
    j.target_channel_id,
    a.status,
    coalesce(a.finished_at, a.started_at) as occurred_at
  from rebalance_attempts a
  join rebalance_jobs j on j.id = a.job_id
  where coalesce(a.finished_at, a.started_at) >= $1
),
last_success as (
  select target_channel_id, max(occurred_at) as last_success_at
  from events
  where status='succeeded'
  group by target_channel_id
),
pressure as (
  select e.*, ls.last_success_at
  from events e
  left join last_success ls using (target_channel_id)
  where ls.last_success_at is null or e.occurred_at > ls.last_success_at
)
select target_channel_id,
  count(*) as attempts,
  coalesce(sum(case when status='succeeded' then 1 else 0 end), 0) as successes,
  coalesce(sum(case when status<>'succeeded' then 1 else 0 end), 0) as failures,
  max(occurred_at) as last_attempt_at,
  max(case when status<>'succeeded' then occurred_at else null end) as last_failure_at,
  max(last_success_at) as last_success_at
from pressure
group by target_channel_id
`, since)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var attempts int
		var successes int
		var failures int
		var lastAttempt pgtype.Timestamptz
		var lastFailure pgtype.Timestamptz
		var lastSuccess pgtype.Timestamptz
		if err := rows.Scan(&channelID, &attempts, &successes, &failures, &lastAttempt, &lastFailure, &lastSuccess); err != nil {
			return stats
		}
		stat := recentCooldownStat{
			ChannelID: uint64(channelID),
			Attempts:  attempts,
			Successes: successes,
			Failures:  failures,
		}
		if lastAttempt.Valid {
			stat.LastAttemptAt = lastAttempt.Time
		}
		if lastFailure.Valid {
			stat.LastFailureAt = lastFailure.Time
		}
		if lastSuccess.Valid {
			stat.LastSuccessAt = lastSuccess.Time
		}
		stats[uint64(channelID)] = stat
	}
	return stats
}

func (s *RebalanceService) loadRecentTargetNoAttemptCooldowns(ctx context.Context, since time.Time) map[uint64]recentCooldownStat {
	stats := map[uint64]recentCooldownStat{}
	if s.db == nil {
		return stats
	}
	rows, err := s.db.Query(ctx, `
with events as (
  select
    j.target_channel_id,
    j.status,
    j.completed_at as occurred_at,
    (j.status='skipped' and j.reason in ('all sources skipped (recent failures)', 'no executable sources') and not exists (
      select 1 from rebalance_attempts a where a.job_id = j.id
    )) as no_attempt_failure
  from rebalance_jobs j
  where j.completed_at >= $1
    and j.status in ('succeeded','partial','failed','skipped')
),
last_success as (
  select target_channel_id, max(occurred_at) as last_success_at
  from events
  where status in ('succeeded','partial')
  group by target_channel_id
),
pressure as (
  select e.*, ls.last_success_at
  from events e
  left join last_success ls using (target_channel_id)
  where ls.last_success_at is null or e.occurred_at > ls.last_success_at
)
select target_channel_id,
  coalesce(sum(case when no_attempt_failure then 1 else 0 end), 0) as no_attempt_failures,
  coalesce(sum(case when status in ('succeeded','partial') then 1 else 0 end), 0) as successes,
  max(case when no_attempt_failure then occurred_at else null end) as last_no_attempt_failure_at,
  max(last_success_at) as last_success_at
from pressure
group by target_channel_id
`, since)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var failures int
		var successes int
		var lastAttempt pgtype.Timestamptz
		var lastSuccess pgtype.Timestamptz
		if err := rows.Scan(&channelID, &failures, &successes, &lastAttempt, &lastSuccess); err != nil {
			return stats
		}
		if failures <= 0 {
			continue
		}
		stat := recentCooldownStat{
			ChannelID: uint64(channelID),
			Attempts:  failures,
			Successes: successes,
			Failures:  failures,
		}
		if lastAttempt.Valid {
			stat.LastAttemptAt = lastAttempt.Time
			stat.LastFailureAt = lastAttempt.Time
		}
		if lastSuccess.Valid {
			stat.LastSuccessAt = lastSuccess.Time
		}
		stats[uint64(channelID)] = stat
	}
	return stats
}

func (s *RebalanceService) loadRecentTargetFailedCooldowns(ctx context.Context, since time.Time) map[uint64]recentCooldownStat {
	stats := map[uint64]recentCooldownStat{}
	if s.db == nil {
		return stats
	}
	rows, err := s.db.Query(ctx, `
with events as (
  select
    j.target_channel_id,
    j.status,
    j.reason,
    j.completed_at as occurred_at
  from rebalance_jobs j
  where j.completed_at >= $1
    and j.status in ('succeeded','partial','failed')
),
last_success as (
  select target_channel_id, max(occurred_at) as last_success_at
  from events
  where status in ('succeeded','partial')
  group by target_channel_id
),
pressure as (
  select e.*, ls.last_success_at
  from events e
  left join last_success ls using (target_channel_id)
  where ls.last_success_at is null or e.occurred_at > ls.last_success_at
)
select target_channel_id,
  coalesce(sum(case when status='failed' and reason='all sources failed' then 1 else 0 end), 0) as failures,
  coalesce(sum(case when status in ('succeeded','partial') then 1 else 0 end), 0) as successes,
  max(case when status='failed' and reason='all sources failed' then occurred_at else null end) as last_failure_at,
  max(last_success_at) as last_success_at
from pressure
group by target_channel_id
`, since)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var failures int
		var successes int
		var lastAttempt pgtype.Timestamptz
		var lastSuccess pgtype.Timestamptz
		if err := rows.Scan(&channelID, &failures, &successes, &lastAttempt, &lastSuccess); err != nil {
			return stats
		}
		if failures <= 0 {
			continue
		}
		stat := recentCooldownStat{
			ChannelID: uint64(channelID),
			Attempts:  failures,
			Successes: successes,
			Failures:  failures,
		}
		if lastAttempt.Valid {
			stat.LastAttemptAt = lastAttempt.Time
			stat.LastFailureAt = lastAttempt.Time
		}
		if lastSuccess.Valid {
			stat.LastSuccessAt = lastSuccess.Time
		}
		stats[uint64(channelID)] = stat
	}
	return stats
}

func (s *RebalanceService) loadRecentTargetDistinctSourceCooldowns(ctx context.Context, since time.Time) map[uint64]recentCooldownStat {
	stats := map[uint64]recentCooldownStat{}
	if s.db == nil {
		return stats
	}
	rows, err := s.db.Query(ctx, `
with events as (
  select
    j.target_channel_id,
    a.source_channel_id,
    a.status,
    a.fail_reason,
    coalesce(a.finished_at, a.started_at) as occurred_at
  from rebalance_attempts a
  join rebalance_jobs j on j.id = a.job_id
  where coalesce(a.finished_at, a.started_at) >= $1
),
last_success as (
  select target_channel_id, max(occurred_at) as last_success_at
  from events
  where status='succeeded'
  group by target_channel_id
),
pressure as (
  select e.*, ls.last_success_at
  from events e
  left join last_success ls using (target_channel_id)
  where ls.last_success_at is null or e.occurred_at > ls.last_success_at
),
structural_failures as (
  select *
  from pressure
  where status<>'succeeded'
    and (
      lower(coalesce(fail_reason, '')) like '%unable to find a path%'
      or lower(coalesce(fail_reason, '')) like '%no matching outgoing channel%'
      or lower(coalesce(fail_reason, '')) like '%probe returned no amount%'
      or lower(coalesce(fail_reason, '')) like '%attempt timeout%'
      or lower(coalesce(fail_reason, '')) like '%deadlineexceeded%'
      or lower(coalesce(fail_reason, '')) like '%deadline exceeded%'
    )
)
select target_channel_id,
  count(*) as failures,
  count(distinct source_channel_id) as distinct_sources,
  max(occurred_at) as last_failure_at,
  max(last_success_at) as last_success_at
from structural_failures
group by target_channel_id
`, since)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var failures int
		var distinctSources int
		var lastFailure pgtype.Timestamptz
		var lastSuccess pgtype.Timestamptz
		if err := rows.Scan(&channelID, &failures, &distinctSources, &lastFailure, &lastSuccess); err != nil {
			return stats
		}
		if failures <= 0 || distinctSources <= 0 {
			continue
		}
		stat := recentCooldownStat{
			ChannelID:       uint64(channelID),
			Attempts:        failures,
			Failures:        failures,
			DistinctSources: distinctSources,
			LastAttemptAt:   time.Time{},
			LastFailureAt:   time.Time{},
			LastSuccessAt:   time.Time{},
		}
		if lastFailure.Valid {
			stat.LastAttemptAt = lastFailure.Time
			stat.LastFailureAt = lastFailure.Time
		}
		if lastSuccess.Valid {
			stat.LastSuccessAt = lastSuccess.Time
		}
		stats[uint64(channelID)] = stat
	}
	return stats
}

func (s *RebalanceService) loadSovereignTargetStructuralCooldowns(ctx context.Context, targetIDs []uint64, cfg RebalanceConfig, now time.Time) map[uint64]sovereignTargetStructuralCooldownStat {
	stats := map[uint64]sovereignTargetStructuralCooldownStat{}
	if s.db == nil || len(targetIDs) == 0 {
		return stats
	}
	ids := make([]int64, 0, len(targetIDs))
	seen := map[uint64]struct{}{}
	for _, id := range targetIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	if len(ids) == 0 {
		return stats
	}
	if now.IsZero() {
		now = time.Now()
	}
	since := now.Add(-sovereignTargetStructuralCooldownLookback)
	rows, err := s.db.Query(ctx, `
with scoped_targets as (
  select unnest($1::bigint[]) as target_channel_id
),
jobs as (
  select
    j.id,
    j.target_channel_id,
    j.status,
    j.reason,
    j.completed_at,
    coalesce((select count(*) from rebalance_attempts a where a.job_id=j.id), 0) as attempt_count
  from rebalance_jobs j
  join scoped_targets st on st.target_channel_id = j.target_channel_id
  where j.completed_at >= $2
    and j.completed_at is not null
    and j.status in ('succeeded','partial','failed')
),
last_success as (
  select target_channel_id, max(completed_at) as last_success_at
  from jobs
  where status in ('succeeded','partial')
  group by target_channel_id
),
failure_jobs as (
  select j.*, ls.last_success_at
  from jobs j
  left join last_success ls using (target_channel_id)
  where j.status='failed'
    and j.reason='all sources failed'
    and j.attempt_count >= $3
    and (ls.last_success_at is null or j.completed_at > ls.last_success_at)
)
select target_channel_id,
  count(*) as failures,
  max(attempt_count) as last_failure_attempts,
  max(completed_at) as last_failure_at,
  max(last_success_at) as last_success_at
from failure_jobs
group by target_channel_id
`, ids, since, sovereignTargetStructuralCooldownMinAttempts)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var failures int
		var lastFailureAttempts int
		var lastFailure pgtype.Timestamptz
		var lastSuccess pgtype.Timestamptz
		if err := rows.Scan(&channelID, &failures, &lastFailureAttempts, &lastFailure, &lastSuccess); err != nil {
			return stats
		}
		if channelID <= 0 || failures <= 0 {
			continue
		}
		stat := sovereignTargetStructuralCooldownStat{
			TargetChannelID:     uint64(channelID),
			Failures:            failures,
			LastFailureAttempts: lastFailureAttempts,
		}
		if lastFailure.Valid {
			stat.LastFailureAt = lastFailure.Time
		}
		if lastSuccess.Valid {
			stat.LastSuccessAt = lastSuccess.Time
		}
		if shouldSkipSovereignTargetStructuralCooldown(stat, cfg, now) {
			stats[uint64(channelID)] = stat
		}
	}
	return stats
}

func (s *RebalanceService) loadSovereignUnsoldPaidLiquidityStats(ctx context.Context, cfg RebalanceConfig, targetIDs []uint64, now time.Time) map[uint64]sovereignUnsoldLiquidityStat {
	stats := map[uint64]sovereignUnsoldLiquidityStat{}
	if s.db == nil || len(targetIDs) == 0 {
		return stats
	}
	ids := make([]int64, 0, len(targetIDs))
	seen := map[uint64]struct{}{}
	for _, id := range targetIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, int64(id))
	}
	if len(ids) == 0 {
		return stats
	}
	if now.IsZero() {
		now = time.Now()
	}
	lookback := time.Duration(sovereignSlowSellerWindowHoursForConfig(cfg)) * time.Hour
	if lookback < sovereignUnsoldPaidLiquidityLookback {
		lookback = sovereignUnsoldPaidLiquidityLookback
	}
	since := now.Add(-lookback)
	rows, err := s.db.Query(ctx, `
with job_stats as (
  select
    j.id,
    j.target_channel_id,
    j.completed_at,
    j.target_amount_sat,
    coalesce(sum(a.amount_sat) filter (where a.status='succeeded'), 0) as sent_sat,
    coalesce(sum(a.fee_paid_sat) filter (where a.status='succeeded'), 0) as fee_paid_sat
  from rebalance_jobs j
  left join rebalance_attempts a on a.job_id = j.id
  where j.target_channel_id = any($1::bigint[])
    and j.trigger_reason = $2
    and j.status in ('succeeded','partial')
    and j.completed_at is not null
    and j.completed_at >= $3
    and j.completed_at <= $4
  group by j.id, j.target_channel_id, j.completed_at, j.target_amount_sat
),
latest as (
  select distinct on (target_channel_id)
    id, target_channel_id, completed_at, target_amount_sat, sent_sat, fee_paid_sat
  from job_stats
  where sent_sat > 0
    and target_amount_sat > 0
    and sent_sat * 100 >= target_amount_sat * $5
  order by target_channel_id, completed_at desc, id desc
)
select
  l.target_channel_id,
  l.completed_at,
  l.target_amount_sat,
  l.sent_sat,
  l.fee_paid_sat,
  coalesce(sum(n.amount_sat), 0) as forward_amount_sat,
  coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end), 0) as forward_fee_msat
from latest l
left join notifications n on n.type='forward'
  and n.channel_id = l.target_channel_id
  and n.occurred_at >= l.completed_at
  and n.occurred_at <= $4
group by l.target_channel_id, l.completed_at, l.target_amount_sat, l.sent_sat, l.fee_paid_sat
`, ids, rebalanceSovereignReason, since, now, sovereignUnsoldPaidLiquidityMinFillPct)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var completedAt time.Time
		var targetAmountSat int64
		var sentSat int64
		var feePaidSat int64
		var forwardAmountSat int64
		var forwardFeeMsat int64
		if err := rows.Scan(&channelID, &completedAt, &targetAmountSat, &sentSat, &feePaidSat, &forwardAmountSat, &forwardFeeMsat); err != nil {
			return stats
		}
		if channelID == 0 || sentSat <= 0 {
			continue
		}
		stat := sovereignUnsoldLiquidityStat{
			CompletedAt:      completedAt,
			TargetAmountSat:  targetAmountSat,
			SentSat:          sentSat,
			FeePaidSat:       feePaidSat,
			ForwardAmountSat: forwardAmountSat,
			ForwardFeeSat:    forwardFeeMsat / 1000,
		}
		if hasSovereignUnsoldPaidLiquidity(stat, cfg, now) {
			stats[uint64(channelID)] = stat
		}
	}
	return stats
}

func (s *RebalanceService) acquireSem(ctx context.Context) bool {
	s.mu.Lock()
	sem := s.sem
	stop := s.stop
	s.mu.Unlock()
	if sem == nil {
		return true
	}
	// Bloqueia indefinidamente esperando slot. Saída em 3 casos:
	// 1. Slot adquirido (case sem <- struct{}{})
	// 2. ctx cancelado pelo caller (raramente — caller usa context.Background())
	// 3. Service shutting down (stop closed) → libera goroutines presas
	select {
	case sem <- struct{}{}:
		s.mu.Lock()
		s.semInflight++
		s.mu.Unlock()
		return true
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	}
}

func (s *RebalanceService) cooldownProbeSlotAvailable() bool {
	s.mu.Lock()
	sem := s.cooldownProbeSem
	s.mu.Unlock()
	if sem == nil {
		return true
	}
	return len(sem) < cap(sem)
}

func (s *RebalanceService) acquireCooldownProbeSem(ctx context.Context) bool {
	s.mu.Lock()
	sem := s.cooldownProbeSem
	stop := s.stop
	s.mu.Unlock()
	if sem == nil {
		return true
	}
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	default:
		return false
	}
}

func (s *RebalanceService) releaseCooldownProbeSem() {
	s.mu.Lock()
	sem := s.cooldownProbeSem
	s.mu.Unlock()
	if sem == nil {
		return
	}
	select {
	case <-sem:
	default:
	}
}

func (s *RebalanceService) hasActivePriorityRebalanceWork(ctx context.Context, currentJobID int64) bool {
	if s.db == nil {
		return false
	}
	var count int
	err := s.db.QueryRow(ctx, `
select count(*)
from rebalance_jobs
where status in ('queued','running')
  and id <> $1
  and not (source='auto' and coalesce(trigger_reason, reason, '')=$2)
`, currentJobID, targetCooldownProbeReason).Scan(&count)
	return err == nil && count > 0
}

// releaseSem releases the slot on the same channel the job acquired (s.sem may
// be replaced by resetSemaphore between acquire and release). When the last
// in-flight job releases and a resize was deferred by resetSemaphore, the new
// capacity is applied here.
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
	s.mu.Lock()
	if s.semInflight > 0 {
		s.semInflight--
	}
	if s.semInflight == 0 && s.semPendingResize {
		desired := s.semDesiredCap
		if desired <= 0 {
			desired = 1
		}
		s.sem = make(chan struct{}, desired)
		s.semPendingResize = false
	}
	s.mu.Unlock()
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
  and coalesce(trigger_reason, reason, '')='auto-restart'
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

	// R5: if this job was created in an exploration slot, record the outcome
	// against the target channel so the burnout window updates. Partials and
	// full successes both count as "successful" exploration — they moved
	// liquidity, which is the whole point.
	if channelID, ok := s.takeExplorationJob(jobID); ok {
		succeeded := status == "succeeded" || status == "partial"
		s.recordExplorationOutcome(channelID, succeeded, completedAt)
	}

	go func() {
		snapCtx, snapCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer snapCancel()
		s.snapshotMetricsDay(snapCtx, completedAt)
	}()

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
	// status=="skipped" → não re-agenda. Pair cache TTL ainda está ativo (15min-6h);
	// o runManualRestartWatchLoop (15min) cobre o re-test natural sem criar phantom.
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
	// Master switch (AutoEnabled) gates this scheduled restart too — without
	// this check, a job that completed before the operator disabled rebalance
	// would still spawn a follow-up after the cooldown timer.
	if !cfg.AutoEnabled || !cfg.ManualRestartWatch {
		return
	}
	settings, _ := s.loadChannelSettings(restartCtx)
	exclusions, _ := s.loadExclusions(restartCtx)
	ledger, _ := s.loadLedger(restartCtx, cfg)
	revenueByChannel, _ := s.fetchChannelRevenue7d(restartCtx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(restartCtx)
	drainRateByChannel := s.fetchChannelDrainRate24h(restartCtx)
	targetCooldowns := s.loadRecentTargetCooldownSet(restartCtx, defaultRecentTargetCooldownWindows(time.Now()))

	channels, err := s.listChannelsCached(restartCtx)
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
	if shouldCooldownTargetRecentFailures(targetCooldowns.Recent[target.ChannelID], targetCooldowns.NoAttempt[target.ChannelID], targetCooldowns.Failed[target.ChannelID], targetCooldowns.DistinctSource[target.ChannelID], time.Now()) {
		return
	}
	snapshot := s.buildChannelSnapshot(restartCtx, cfg, false, target, setting, ledger[target.ChannelID], revenueByChannel[target.ChannelID], costByChannel[target.ChannelID], drainRateByChannel[target.ChannelID], exclusions[target.ChannelID])
	if ok, _ := manualRestartWatchEligibility(snapshot, cfg); !ok {
		return
	}
	deficit := computeDeficitAmount(target, snapshot.TargetOutboundPct)
	if deficit <= 0 {
		return
	}
	minExecuteSat := effectiveMinExecuteSat(cfg)
	if minExecuteSat > 0 && deficit < minExecuteSat {
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

func (s *RebalanceService) buildChannelSnapshot(ctx context.Context, cfg RebalanceConfig, criticalActive bool, ch lndclient.ChannelInfo, setting channelSetting, ledger *channelLedger, revenue7dSat int64, cost7d rebalanceCost7dStat, drainRateSatPerHour int64, excluded bool) RebalanceChannel {
	setting = normalizeChannelSetting(setting)
	parked := isChannelAutomationParked(setting.AutomationMode)
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

	// Wave 3.4: count outgoing HTLCs locked on this channel — used as a
	// tiebreaker so the source sort prefers channels with less concurrent
	// activity (lower collision risk during a rebalance attempt).
	pendingOutgoing := 0
	for _, htlc := range ch.PendingHtlcs {
		if !htlc.Incoming {
			pendingOutgoing++
		}
	}

	protected := int64(0)
	freshProtected := int64(0)
	paidCost := int64(0)
	paidRevenue := int64(0)
	lastRebalance := time.Time{}
	if ledger != nil {
		protected = ledger.PaidLiquiditySat
		freshProtected = ledger.FreshPaidLiquiditySat
		paidCost = ledger.PaidCostSat
		paidRevenue = ledger.PaidRevenueSat
		lastRebalance = ledger.LastRebalanceAt
	}

	paybackProgress := 0.0
	if paidCost > 0 {
		paybackProgress = float64(paidRevenue) / float64(paidCost)
	}
	timeToPaybackHours, timeToPaybackValid := estimateTimeToPaybackHours(paidRevenue, paidCost, revenue7dSat)

	// Wave 1.4: per-channel econ ratio (defaults to global cfg.EconRatio).
	effectiveEconRatio := cfg.EconRatio
	if !setting.UseDefaultEconRatio && setting.EconRatioOverrideSet {
		effectiveEconRatio = setting.EconRatioOverride
	}
	if effectiveEconRatio <= 0 {
		effectiveEconRatio = cfg.EconRatio
	}
	expectedCostPpm := cost7d.FeePpm
	if expectedCostPpm <= 0 {
		expectedCostPpm = cfg.RebalanceCostFloorPpm
	}
	effectiveSpreadPpm := int64(0)
	if spread > 0 && effectiveEconRatio > 0 {
		effectiveSpreadPpm = int64(float64(spread) * effectiveEconRatio)
	}

	eligibleTarget := false
	deficitPct := target - localPct
	// Hotfix: eligibleManualTarget mirrors the gates that runJob actually
	// enforces for the target channel (see runJob override around the
	// channelSnapshot loop). User-triggered manual runs bypass the wave 1.4
	// economic filter and the ROI guardrail — those are auto/auto-restart
	// only — so the UI should not disable the "Manual Rebal In" button just
	// because EligibleAsTarget is false.
	eligibleManualTarget := ch.Active && !parked && deficitPct > cfg.DeadbandPct && outgoingFee > peerFeeRate
	if eligibleManualTarget {
		// Wave 1.4: require effective spread to clear the expected rebalance
		// cost (historical 7d ppm, or cfg.RebalanceCostFloorPpm fallback).
		// auto_bypass_cost_gate is a per-channel operator override for cases
		// where explicit strategy should win over this conservative gate. It
		// does not bypass ROI/profit guardrails later in the auto scan.
		if passesAutoTargetCostGate(setting, expectedCostPpm, effectiveSpreadPpm) {
			eligibleTarget = true
		}
	}

	sourceFloorPct := cfg.SourceMinLocalPct
	if sourceFloorPct <= 0 || sourceFloorPct > 100 {
		sourceFloorPct = rebalanceDefaultTargetOutboundPct
	}
	sourceOpportunityCost := int64(0)
	if cfg.SovereignSourceOpportunityCostEnabled && (setting.AutoEnabled || setting.ManualRestartEnabled) {
		sourceOpportunityCost = revenue7dSat
		targetSourceFloorPct := target + sovereignSourceOpportunityTargetBufferPct
		if targetSourceFloorPct > 100 {
			targetSourceFloorPct = 100
		}
		if targetSourceFloorPct > sourceFloorPct {
			sourceFloorPct = targetSourceFloorPct
		}
	}
	maxSource := int64(float64(ch.LocalBalanceSat) - (float64(ch.CapacitySat) * (sourceFloorPct / 100)))
	if maxSource < 0 {
		maxSource = 0
	}
	eligibleSource := maxSource > 0 && localPct >= sourceFloorPct
	// Source protection (unified Policy C, 2026-05-04 refactor): combines the
	// previous binary "block channels with low payback" gate and the
	// "effectiveProtected" lock into a single proportional formula keyed on
	// paid_liquidity_sat × unrecouped_fraction.
	//
	// Rationale: the old binary gate blocked forward-driven channels (e.g.
	// Boltz with small paid_liquidity but big local from forwards) even though
	// most of their local balance was never rebalanced in. The proportional
	// formula caps source contribution to "free" liquidity only.
	//
	// SourceMinPaybackProgress is reinterpreted as the threshold of payback
	// progress at which liquidity is considered fully recouped (default 0.95).
	// At payback >= threshold, effectiveProtected = 0. At payback = 0,
	// effectiveProtected = paid_liquidity_sat (full lock). Linear in between.
	effectiveProtected := computeEffectiveProtected(protected, paidCost, paybackProgress, cfg, lastRebalance, criticalActive)
	effectiveProtected = applyFreshPaidLiquidityLock(effectiveProtected, freshProtected, protected, cfg)
	maxSource -= effectiveProtected
	if maxSource < 0 {
		maxSource = 0
	}
	sourceQuarantined := false
	sourceQuarantineUntil := ""
	if protected > 0 {
		quarantine := sovereignTargetSourceQuarantineDuration(cfg)
		if quarantine > 0 && !lastRebalance.IsZero() {
			now := time.Now()
			until := lastRebalance.Add(quarantine)
			paybackThreshold := cfg.SourceMinPaybackProgress
			if paybackThreshold <= 0 {
				paybackThreshold = defaultRebalanceConfig().SourceMinPaybackProgress
			}
			if now.Before(until) && (freshProtected > 0 || paybackProgress < paybackThreshold) {
				sourceQuarantined = true
				sourceQuarantineUntil = until.UTC().Format(time.RFC3339)
				maxSource = 0
			}
		}
	}
	if maxSource <= 0 || !ch.Active || parked {
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
		ChannelID:              ch.ChannelID,
		ChannelPoint:           ch.ChannelPoint,
		PeerAlias:              ch.PeerAlias,
		RemotePubkey:           ch.RemotePubkey,
		Active:                 ch.Active,
		Private:                ch.Private,
		CapacitySat:            ch.CapacitySat,
		LocalBalanceSat:        ch.LocalBalanceSat,
		RemoteBalanceSat:       ch.RemoteBalanceSat,
		LocalPct:               localPct,
		RemotePct:              remotePct,
		OutgoingFeePpm:         outgoingFee,
		OutgoingBaseMsat:       outgoingBaseMsat,
		PeerFeeRatePpm:         peerFeeRate,
		PeerBaseMsat:           peerBaseMsat,
		SpreadPpm:              spread,
		TargetOutboundPct:      target,
		TargetAmountSat:        targetAmount,
		AutoEnabled:            setting.AutoEnabled,
		ManualRestartEnabled:   setting.ManualRestartEnabled,
		AutoTargetManaged:      setting.AutoTargetManaged,
		AutomationMode:         setting.AutomationMode,
		FixedFeePPM:            setting.FixedFeePPM,
		ReviewAt:               setting.ReviewAt,
		AutomationNote:         setting.AutomationNote,
		ParkedAt:               setting.ParkedAt,
		UseDefaultEconRatio:    setting.UseDefaultEconRatio,
		EconRatioOverride:      econRatioOverride,
		AutoBypassCostGate:     setting.AutoBypassCostGate,
		EligibleAsTarget:       eligibleTarget,
		EligibleAsManualTarget: eligibleManualTarget,
		EligibleAsSource:       eligibleSource && !excluded,
		ProtectedLiquiditySat:  protected,
		EffectiveProtectedSat:  effectiveProtected,
		PaybackProgress:        paybackProgress,
		TimeToPaybackHours:     timeToPaybackHours,
		TimeToPaybackValid:     timeToPaybackValid,
		MaxSourceSat:           maxSource,
		Revenue7dSat:           revenue7dSat,
		DrainRateSatPerHour:    drainRateSatPerHour,
		PendingOutgoingHtlcs:   pendingOutgoing,
		RebalanceCost7dSat:     cost7d.FeeSat,
		RebalanceCost7dPpm:     cost7d.FeePpm,
		RebalanceAmount7dSat:   cost7d.AmountSat,
		ROIEstimate:            roiEstimate,
		ROIEstimateValid:       roiEstimateValid,
		ExcludedAsSource:       excluded,
		SourceQuarantined:      sourceQuarantined,
		SourceQuarantineUntil:  sourceQuarantineUntil,
		SourceOpportunityCost:  sourceOpportunityCost,
	}
}

// computeEffectiveProtected returns the portion of paid_liquidity_sat that
// should be reserved (excluded from MaxSourceSat) to prevent the channel from
// being used as a rebalance source while it still has unrecouped cost.
//
// The protection scales linearly with payback progress against
// SourceMinPaybackProgress (default 0.95):
//   - paid_cost <= rebalancePaybackMinCostSat → 0 (fresh channel, no history)
//   - paybackProgress >= threshold → 0 (fully recouped)
//   - else → paid_liquidity_sat × (1 - paybackProgress / threshold)
//
// PaybackMode flags layer on top:
//   - paybackModePayback (default on): only releases when payback ≥ threshold
//     (already encoded in the proportional formula).
//   - paybackModeTime: time-based unlock after UnlockDays of inactivity.
//     Forces protection to 0 once the deadline passes, regardless of payback.
//   - paybackModeCritical: emergency partial release of CriticalReleasePct%
//     when the node is in critical mode.
func computeEffectiveProtected(paidLiquiditySat int64, paidCost int64, paybackProgress float64, cfg RebalanceConfig, lastRebalance time.Time, criticalActive bool) int64 {
	if paidLiquiditySat <= 0 {
		return 0
	}
	// Fresh channel: paid_cost too small to take seriously.
	if paidCost <= rebalancePaybackMinCostSat {
		return 0
	}

	threshold := cfg.SourceMinPaybackProgress
	if threshold <= 0 {
		threshold = 1.0
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) {
		threshold = 1.0
	}

	protected := paidLiquiditySat
	// Proportional protection based on payback progress.
	if (cfg.PaybackModeFlags & paybackModePayback) != 0 {
		if paybackProgress >= threshold {
			protected = 0
		} else if paybackProgress > 0 {
			unrecouped := 1.0 - (paybackProgress / threshold)
			if unrecouped < 0 {
				unrecouped = 0
			}
			if unrecouped > 1 {
				unrecouped = 1
			}
			protected = int64(math.Round(float64(paidLiquiditySat) * unrecouped))
		}
		// paybackProgress <= 0 → keep full protected
	}

	// Time-based unlock: after UnlockDays without a fresh rebalance, release
	// regardless of payback. Operator-tunable knob to "give up" on stuck
	// channels.
	if (cfg.PaybackModeFlags&paybackModeTime) != 0 && !lastRebalance.IsZero() && cfg.UnlockDays > 0 {
		if time.Since(lastRebalance) >= time.Duration(cfg.UnlockDays)*24*time.Hour {
			return 0
		}
	}

	// Critical mode: emergency release of a fraction of the still-protected
	// amount to free liquidity for critical channels.
	if criticalActive && (cfg.PaybackModeFlags&paybackModeCritical) != 0 && protected > 0 {
		release := int64(math.Round(float64(protected) * (cfg.CriticalReleasePct / 100)))
		protected -= release
		if protected < 0 {
			protected = 0
		}
	}

	if protected < 0 {
		protected = 0
	}
	if protected > paidLiquiditySat {
		protected = paidLiquiditySat
	}
	return protected
}

func applyFreshPaidLiquidityLock(effectiveProtected int64, freshPaidLiquiditySat int64, paidLiquiditySat int64, cfg RebalanceConfig) int64 {
	if !cfg.FreshPaidLiquidityLockEnabled || freshPaidLiquiditySat <= 0 {
		return effectiveProtected
	}
	if paidLiquiditySat > 0 && freshPaidLiquiditySat > paidLiquiditySat {
		freshPaidLiquiditySat = paidLiquiditySat
	}
	if freshPaidLiquiditySat > effectiveProtected {
		effectiveProtected = freshPaidLiquiditySat
	}
	if paidLiquiditySat > 0 && effectiveProtected > paidLiquiditySat {
		effectiveProtected = paidLiquiditySat
	}
	if effectiveProtected < 0 {
		return 0
	}
	return effectiveProtected
}

func estimateTimeToPaybackHours(paidRevenueSat int64, paidCostSat int64, revenue7dSat int64) (float64, bool) {
	if paidCostSat <= 0 {
		return 0, false
	}
	remainingSat := paidCostSat - paidRevenueSat
	if remainingSat <= 0 {
		return 0, true
	}
	if revenue7dSat <= 0 {
		return 0, false
	}
	revenuePerHour := float64(revenue7dSat) / (7 * 24)
	if revenuePerHour <= 0 {
		return 0, false
	}
	return float64(remainingSat) / revenuePerHour, true
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

func satToPpm(feeSat int64, amountSat int64) int64 {
	if amountSat <= 0 || feeSat <= 0 {
		return 0
	}
	return int64(math.Round(float64(feeSat) * 1_000_000 / float64(amountSat)))
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

func estimateSovereignTargetCost(amountSat int64, historicalFeePpm int64, historicalAmountSat int64, budgetCostSat int64, cfg RebalanceConfig) int64 {
	historicalCost := estimateHistoricalCost(amountSat, historicalFeePpm)
	floorCost := estimateHistoricalCost(amountSat, cfg.RebalanceCostFloorPpm)
	estimatedCost := historicalCost
	if floorCost > estimatedCost {
		estimatedCost = floorCost
	}
	if !hasReliableSovereignCostHistory(amountSat, historicalAmountSat) {
		conservativeCost := (budgetCostSat * sovereignCostConservativeBudgetPct) / 100
		if conservativeCost > estimatedCost {
			estimatedCost = conservativeCost
		}
	}
	if estimatedCost <= 0 {
		estimatedCost = budgetCostSat
	}
	return estimatedCost
}

func hasReliableSovereignCostHistory(amountSat int64, historicalAmountSat int64) bool {
	if amountSat <= 0 || historicalAmountSat <= 0 {
		return false
	}
	return historicalAmountSat*100 >= amountSat*sovereignCostReliableHistoryPct
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

func estimateTargetGainForConfig(cfg RebalanceConfig, snapshot RebalanceChannel, amountSat int64) int64 {
	if cfg.GainModelVersion >= 3 {
		coldStart := cfg.SovereignGainV3ColdStartPct
		if coldStart < sovereignGainV3ColdStartPctMin || coldStart > sovereignGainV3ColdStartPctMax {
			coldStart = sovereignGainV3ColdStartPct
		}
		return estimateTargetGainV3(amountSat, snapshot.OutgoingFeePpm, snapshot.PeerFeeRatePpm, snapshot.Revenue7dSat, snapshot.LocalBalanceSat, snapshot.CapacitySat, snapshot.DrainRateSatPerHour, coldStart)
	}
	if cfg.GainModelVersion >= 2 {
		return estimateTargetGainV2(amountSat, snapshot.OutgoingFeePpm, snapshot.PeerFeeRatePpm)
	}
	return estimateTargetGain(amountSat, snapshot.Revenue7dSat, snapshot.LocalBalanceSat, snapshot.CapacitySat)
}

func spreadEffectiveness(outgoingFeePpm int64, peerFeeRatePpm int64) float64 {
	if outgoingFeePpm <= 0 {
		return 0
	}
	effectiveness := 1 - (float64(peerFeeRatePpm) / float64(outgoingFeePpm))
	if effectiveness < 0 {
		return 0
	}
	if effectiveness > 1 {
		return 1
	}
	return effectiveness
}

func estimateTargetGainV2(amountSat int64, outgoingFeePpm int64, peerFeeRatePpm int64) int64 {
	if amountSat <= 0 || outgoingFeePpm <= 0 {
		return 0
	}
	effectiveness := spreadEffectiveness(outgoingFeePpm, peerFeeRatePpm)
	if effectiveness <= 0 {
		return 0
	}
	gain := (float64(amountSat) * float64(outgoingFeePpm) / 1_000_000.0) * effectiveness
	if gain <= 0 {
		return 0
	}
	return int64(math.Round(gain))
}

// estimateTargetGainV3 combines v1 (demand from historical revenue) and v2
// (spread × amount) to fix two failure modes of the earlier models:
//   - v1 returns zero for brand-new channels (no Revenue7d), blocking the
//     autopilot from ever exploring them.
//   - v2 assumes the full rebalanced amount will be forwarded, so an idle
//     high-fee channel ranks the same as an active one with similar spread.
//
// v3 uses the strongest available demand signal (historical revenue or the
// drain-rate projected over a 7-day horizon) and caps it by the theoretical
// max (amount × spread). When no demand signal is available the channel is
// treated as a cold-start: half of the theoretical gain is returned and the
// ROI guardrails + roiValid=false multiplier (0.9) handle the rest.
// evWeightedSuccessProbability returns the success probability used by v3
// scoring. With few attempts it shrinks toward a cold-start prior so the
// autopilot can explore brand-new pairs; once enough samples accumulate it
// trusts the observed rate.
func evWeightedSuccessProbability(stats rebalanceTargetPairStats) float64 {
	const coldStartPrior = 0.5
	const fullConfidenceAt = float64(sovereignLowSuccessMinAttempts)
	attempts := stats.Attempts
	successes := stats.Successes
	if stats.RecentStatsLoaded {
		attempts = stats.RecentAttempts
		successes = stats.RecentSuccesses
	}
	if attempts <= 0 {
		return coldStartPrior
	}
	if successes < 0 {
		successes = 0
	}
	if successes > attempts {
		successes = attempts
	}
	rate := float64(successes) / float64(attempts)
	confidence := math.Min(1.0, float64(attempts)/fullConfidenceAt)
	return rate*confidence + coldStartPrior*(1-confidence)
}

// evWeightedEconomicScore returns expected value of a rebalance attempt:
// gain × P(success) − cost × P(failure). The output is a signed sat count
// where negative scores represent attempts that lose money on average.
func evWeightedEconomicScore(expectedGainSat int64, estimatedCostSat int64, stats rebalanceTargetPairStats) int64 {
	p := evWeightedSuccessProbability(stats)
	ev := float64(expectedGainSat)*p - float64(estimatedCostSat)*(1-p)
	return int64(math.Round(ev))
}

// gainV3ObservedVolumeSat estimates how much forward volume we have actually
// observed for a channel, used as the confidence basis for the demand blend in
// estimateTargetGainV3. It takes the strongest of two signals, mirroring the
// demand calculation: volume implied by 7-day fee revenue (revenue ÷ current
// out-fee rate) and volume implied by the 24h drain rate extrapolated to the
// same 7-day horizon. Returns 0 when neither signal is present.
func gainV3ObservedVolumeSat(revenue7dSat int64, outgoingFeePpm int64, drainRateSatPerHour int64) float64 {
	fromRevenue := 0.0
	if revenue7dSat > 0 && outgoingFeePpm > 0 {
		fromRevenue = float64(revenue7dSat) * 1_000_000.0 / float64(outgoingFeePpm)
	}
	fromDrain := 0.0
	if drainRateSatPerHour > 0 {
		const horizonHours = 24.0 * 7.0
		fromDrain = float64(drainRateSatPerHour) * horizonHours
	}
	return math.Max(fromRevenue, fromDrain)
}

func estimateTargetGainV3(amountSat int64, outgoingFeePpm int64, peerFeeRatePpm int64, revenue7dSat int64, localBalanceSat int64, capacitySat int64, drainRateSatPerHour int64, coldStartPct float64) int64 {
	if amountSat <= 0 || outgoingFeePpm <= 0 {
		return 0
	}
	effectiveness := spreadEffectiveness(outgoingFeePpm, peerFeeRatePpm)
	if effectiveness <= 0 {
		return 0
	}
	theoretical := (float64(amountSat) * float64(outgoingFeePpm) / 1_000_000.0) * effectiveness
	if theoretical <= 0 {
		return 0
	}

	historical := 0.0
	if revenue7dSat > 0 {
		denom := localBalanceSat
		if denom <= 0 {
			denom = capacitySat
		}
		if amountSat > denom {
			denom = amountSat
		}
		if denom > 0 {
			historical = float64(revenue7dSat) * (float64(amountSat) / float64(denom))
		}
	}

	projected := 0.0
	if drainRateSatPerHour > 0 {
		const horizonHours = 24.0 * 7.0
		volume := float64(drainRateSatPerHour) * horizonHours
		if volume > float64(amountSat) {
			volume = float64(amountSat)
		}
		projected = volume * float64(outgoingFeePpm) / 1_000_000.0 * effectiveness
	}

	pct := coldStartPct
	if pct < sovereignGainV3ColdStartPctMin || pct > sovereignGainV3ColdStartPctMax {
		pct = sovereignGainV3ColdStartPct
	}
	coldStartGain := theoretical * pct

	demand := math.Max(historical, projected)
	var gain float64
	if demand <= 0 {
		// Cold-start: no observed demand yet. Keep coldStartPct of theoretical so
		// brand-new channels survive the profit_guardrail (gain < cost) check at
		// Funnel A. Default 0.75 (configurable via sovereign_gain_v3_cold_start_pct,
		// range 0.50–0.95). The roiValid=false multiplier still trims the
		// downstream score.
		gain = coldStartGain
	} else {
		// Confidence-blended demand. A thin sample — observed forward volume far
		// below the requested amount — must not score the channel BELOW a
		// brand-new one. That "cold-start cliff" let a single small forward drop
		// the estimate under the cold-start prior, so a channel with one sale
		// scored worse than one with none, and the autopilot could never adopt it
		// (roi_guardrail filters it at candidate-build time, before exploration
		// can pick it up).
		//
		// confidence = observed volume / requested amount: at conf=1 (we have
		// already seen at least `amount` of flow) the empirical demand stands on
		// its own; at conf→0 we fall back to the cold-start prior. Mirrors the
		// evWeightedSuccessProbability shrinkage. max(demand, …) guarantees a
		// channel already out-earning the prior is never penalized;
		// min(theoretical, …) keeps the estimate physically bounded. Lower
		// sovereign_gain_v3_cold_start_pct to dial the lift down.
		observed := gainV3ObservedVolumeSat(revenue7dSat, outgoingFeePpm, drainRateSatPerHour)
		conf := 0.0
		if amountSat > 0 {
			conf = math.Min(1.0, observed/float64(amountSat))
		}
		blended := demand*conf + coldStartGain*(1.0-conf)
		gain = math.Min(theoretical, math.Max(demand, blended))
	}
	if gain <= 0 {
		return 0
	}
	return int64(math.Round(gain))
}

func applyMultiObjectiveScores(candidates []rebalanceTarget, cfg RebalanceConfig, scanAt time.Time) {
	// v3 already folds demand (revenue + drain rate) into the base gain, so
	// applying the velocity multiplier again would double-count it.
	if cfg.GainModelVersion < 2 || cfg.GainModelVersion >= 3 || len(candidates) == 0 {
		return
	}
	maxDrainRate := int64(0)
	for _, candidate := range candidates {
		if candidate.CooldownProbe {
			continue
		}
		if candidate.Channel.DrainRateSatPerHour > maxDrainRate {
			maxDrainRate = candidate.Channel.DrainRateSatPerHour
		}
	}
	for i := range candidates {
		if candidates[i].CooldownProbe {
			continue
		}
		candidates[i].Score = multiObjectiveRebalanceScore(candidates[i].Score, candidates[i].Channel.DrainRateSatPerHour, maxDrainRate, candidates[i].LastAutoAt, scanAt, cfg)
	}
}

func multiObjectiveRebalanceScore(economicScore int64, drainRateSatPerHour int64, maxDrainRateSatPerHour int64, lastAutoAt time.Time, scanAt time.Time, cfg RebalanceConfig) int64 {
	if cfg.GainModelVersion < 2 || economicScore == 0 {
		return economicScore
	}
	velocityWeight := cfg.VelocityWeight
	if math.IsNaN(velocityWeight) || math.IsInf(velocityWeight, 0) {
		velocityWeight = defaultRebalanceConfig().VelocityWeight
	}
	if velocityWeight < 0 {
		velocityWeight = 0
	}
	if velocityWeight > 1 {
		velocityWeight = 1
	}
	velocityMultiplier := 0.0
	if maxDrainRateSatPerHour > 0 && drainRateSatPerHour > 0 {
		velocityMultiplier = float64(drainRateSatPerHour) / float64(maxDrainRateSatPerHour)
		if velocityMultiplier > 1 {
			velocityMultiplier = 1
		}
	}
	ageBoost := rebalanceTargetAgeBoost(lastAutoAt, scanAt, cfg.ScanIntervalSec)
	multiplier := (velocityWeight * velocityMultiplier) + ((1 - velocityWeight) * ageBoost)
	if multiplier <= 0 {
		return 0
	}
	return int64(math.Round(float64(economicScore) * multiplier))
}

func rebalanceTargetAgeBoost(lastAutoAt time.Time, scanAt time.Time, scanIntervalSec int) float64 {
	if scanAt.IsZero() {
		scanAt = time.Now()
	}
	if lastAutoAt.IsZero() {
		return 1.5
	}
	cooldown := time.Duration(scanIntervalSec) * time.Second
	if cooldown < autoTargetCooldownMin {
		cooldown = autoTargetCooldownMin
	}
	age := scanAt.Sub(lastAutoAt)
	if age <= cooldown {
		return 1
	}
	excess := age - cooldown
	boost := 1 + (float64(excess) / float64(24*time.Hour) * 0.5)
	if boost > 1.5 {
		return 1.5
	}
	if boost < 1 {
		return 1
	}
	return boost
}

func applySovereignRiskAdjustedScores(candidates []rebalanceTarget, cfg RebalanceConfig, scanAt time.Time) {
	for i := range candidates {
		if candidates[i].CooldownProbe || candidates[i].Score <= 0 {
			continue
		}
		successMul := sovereignSuccessScoreMultiplier(candidates[i].PairStats, cfg)
		roiMul := sovereignROIScoreMultiplier(candidates[i].ExpectedROI, candidates[i].ExpectedROIValid, cfg)
		budgetMul := sovereignBudgetEfficiencyScoreMultiplier(candidates[i])
		unsoldMul := sovereignUnsoldPaidLiquidityScoreMultiplier(candidates[i].UnsoldLiquidity, cfg, scanAt)
		realizedMul := sovereignRealizedEconomicsScoreMultiplier(candidates[i].PairStats)
		classMul := sovereignTargetClassScoreMultiplier(candidates[i].TargetClass, cfg)
		candidates[i].SuccessMultiplier = successMul
		candidates[i].ROIMultiplier = roiMul
		candidates[i].BudgetEfficiencyMultiplier = budgetMul
		candidates[i].UnsoldLiquidityMultiplier = unsoldMul
		candidates[i].RealizedEconomicsMultiplier = realizedMul
		multiplier := successMul * roiMul * budgetMul * unsoldMul * realizedMul * classMul
		if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier <= 0 {
			continue
		}
		score := int64(math.Round(float64(candidates[i].Score) * multiplier))
		if score <= 0 {
			score = 1
		}
		candidates[i].Score = score
	}
}

func classifySovereignTarget(stats rebalanceTargetPairStats, cfg RebalanceConfig) string {
	if !stats.RecentStatsLoaded || stats.RecentAttempts == 0 {
		return sovereignTargetClassExploration
	}
	if cfg.SovereignSlowSellerEnabled && stats.RecentSentSat > 0 && stats.RecentRealizedNetSlowSat > 0 {
		if stats.RecentRealizedNet24hSat < stats.RecentRealizedNetSlowSat || stats.RecentForwardSlowAmountSat > stats.RecentForward24hAmountSat {
			return sovereignTargetClassSlowHighMargin
		}
	}
	if stats.RecentSentSat > 0 && stats.RecentRealizedNet24hSat >= 0 && stats.RecentForward24hAmountSat*100 >= stats.RecentSentSat*sovereignUnsoldPaidLiquidityMinForwardPct {
		return sovereignTargetClassFastSeller
	}
	if stats.RecentSentSat > 0 && stats.RecentForwardSlowAmountSat == 0 && stats.RecentFailures >= sovereignRecentEmpiricalDeadMinJobs {
		return sovereignTargetClassColdOrDead
	}
	if stats.RecentSuccesses == 0 && stats.RecentFailures >= sovereignRecentEmpiricalDeadMinJobs {
		return sovereignTargetClassColdOrDead
	}
	return sovereignTargetClassExploration
}

func sovereignTargetClassScoreMultiplier(class string, cfg RebalanceConfig) float64 {
	switch class {
	case sovereignTargetClassSlowHighMargin:
		if cfg.SovereignSlowSellerEnabled {
			return 1.20
		}
	case sovereignTargetClassFastSeller:
		return 1.10
	case sovereignTargetClassColdOrDead:
		return 0.65
	}
	return 1
}

func shouldSkipSovereignLowSuccessOpportunity(stats rebalanceTargetPairStats, expectedProfitSat int64, estimatedCostSat int64, budgetCostSat int64, cfg RebalanceConfig, now time.Time) bool {
	if shouldSkipSovereignRecentEmpiricalCooldown(stats, now) {
		return true
	}
	rate, attempts, ok := sovereignLowSuccessGateStats(stats)
	useEmpiricalDeadBan := !stats.RecentStatsLoaded
	if !ok && shouldUseSovereignLifetimeLowSuccessBackstop(stats, cfg) {
		rate, attempts = sovereignHistoricalSuccessRate(stats)
		ok = true
		useEmpiricalDeadBan = true
	}
	if !ok || rate >= sovereignLowSuccessRateForConfig(cfg) {
		return false
	}
	if useEmpiricalDeadBan {
		// Lifetime pair stats remain a fallback for tests and older runtimes,
		// and a backstop when the recent sample is still too small and there is
		// no recent route/demand signal showing that the target recovered.
		if stats.Successes == 0 && attempts >= sovereignLowSuccessDeadZeroAttempts {
			return true
		}
		if rate < sovereignLowSuccessDeadRate && attempts >= sovereignLowSuccessDeadAttempts {
			return true
		}
	}
	costBasis := estimatedCostSat
	if costBasis <= 0 {
		costBasis = budgetCostSat
	}
	profitCostRatio, ok := sovereignProfitCostRatio(expectedProfitSat, costBasis)
	if !ok {
		return true
	}
	return profitCostRatio < sovereignLowSuccessRequiredProfitCostRatio(rate, cfg)
}

func shouldUseSovereignLifetimeLowSuccessBackstop(stats rebalanceTargetPairStats, cfg RebalanceConfig) bool {
	if !stats.RecentStatsLoaded || stats.RecentAttempts >= sovereignRecentLowSuccessMinJobs {
		return false
	}
	if sovereignHasRecentPositiveOpportunitySignal(stats) {
		return false
	}
	rate, attempts := sovereignHistoricalSuccessRate(stats)
	return attempts >= sovereignLowSuccessMinAttempts && rate < sovereignLowSuccessRateForConfig(cfg)
}

func sovereignHasRecentPositiveOpportunitySignal(stats rebalanceTargetPairStats) bool {
	if stats.RecentSuccesses > 0 {
		return true
	}
	if stats.RecentForward24hAmountSat > 0 && stats.RecentRealizedNet24hSat > 0 {
		return true
	}
	if stats.RecentForwardAmountSat > 0 && stats.RecentRealizedNetSat > 0 {
		return true
	}
	return stats.RecentForwardSlowAmountSat > 0 && stats.RecentRealizedNetSlowSat > 0
}

func sovereignLowSuccessGateStats(stats rebalanceTargetPairStats) (float64, int, bool) {
	if stats.RecentStatsLoaded {
		if stats.RecentAttempts < sovereignRecentLowSuccessMinJobs {
			return 0, stats.RecentAttempts, false
		}
		return successRate(stats.RecentSuccesses, stats.RecentAttempts), stats.RecentAttempts, true
	}
	rate, attempts := sovereignHistoricalSuccessRate(stats)
	if attempts < sovereignLowSuccessMinAttempts {
		return rate, attempts, false
	}
	return rate, attempts, true
}

func shouldSkipSovereignRecentEmpiricalCooldown(stats rebalanceTargetPairStats, now time.Time) bool {
	if !stats.RecentStatsLoaded {
		return false
	}
	if stats.RecentAttempts < sovereignRecentEmpiricalDeadMinJobs ||
		stats.RecentFailures < sovereignRecentEmpiricalDeadMinJobs ||
		stats.RecentSuccesses > 0 ||
		stats.RecentLastFailAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	elapsed := now.Sub(stats.RecentLastFailAt)
	if elapsed < 0 {
		return true
	}
	duration := sovereignRecentEmpiricalCooldownDuration(stats.RecentFailures, stats.RecentAllSourcesFailed)
	return elapsed < duration
}

func sovereignRecentEmpiricalCooldownDuration(failures int, allSourcesFailed int) time.Duration {
	if failures >= sovereignRecentEmpiricalDeadMinJobs*2 || allSourcesFailed >= 2 {
		return sovereignRecentEmpiricalDeadRepeat
	}
	return sovereignRecentEmpiricalDeadFirst
}

func shouldSkipSovereignBudgetEfficiencyOpportunity(stats rebalanceTargetPairStats, expectedProfitSat int64, budgetCostSat int64, cfg RebalanceConfig) bool {
	profitCostRatio, ok := sovereignProfitCostRatio(expectedProfitSat, budgetCostSat)
	if !ok {
		return true
	}
	minRatio := sovereignBudgetEfficiencyMinRatioForConfig(cfg)
	if profitCostRatio >= minRatio {
		return false
	}
	rate, attempts := sovereignHistoricalSuccessRate(stats)
	if attempts >= sovereignLowSuccessMinAttempts &&
		rate >= sovereignBudgetEfficiencyHighSuccessRate &&
		profitCostRatio >= sovereignBudgetEfficiencyHighSuccessRatioForConfig(cfg) {
		return false
	}
	return true
}

func shouldHardSkipSovereignBudgetEfficiencyOpportunity(stats rebalanceTargetPairStats, expectedProfitSat int64, budgetCostSat int64, cfg RebalanceConfig) bool {
	if cfg.BudgetUnlimited {
		return false
	}
	return shouldSkipSovereignBudgetEfficiencyOpportunity(stats, expectedProfitSat, budgetCostSat, cfg)
}

func shouldSkipSovereignRouteDeadOpportunity(stats rebalanceTargetPairStats, expectedProfitSat int64, budgetCostSat int64, eligibleSources int, cfg RebalanceConfig) bool {
	routeDeadShare := sovereignRouteDeadShare(stats, eligibleSources, cfg)
	if routeDeadShare < sovereignRouteDeadSourceShareForConfig(cfg) {
		return false
	}
	if routeDeadShare >= sovereignRouteDeadSevereSourceShare {
		return true
	}
	profitCostRatio, ok := sovereignProfitCostRatio(expectedProfitSat, budgetCostSat)
	if !ok {
		return true
	}
	return profitCostRatio < sovereignRouteDeadRequiredProfitCostRatio(routeDeadShare)
}

func shouldSkipSovereignTargetStructuralCooldown(stat sovereignTargetStructuralCooldownStat, cfg RebalanceConfig, now time.Time) bool {
	if stat.Failures <= 0 || stat.LastFailureAt.IsZero() {
		return false
	}
	if !stat.LastSuccessAt.IsZero() && !stat.LastSuccessAt.Before(stat.LastFailureAt) {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	elapsed := now.Sub(stat.LastFailureAt)
	if elapsed < 0 {
		return true
	}
	return elapsed < sovereignTargetStructuralCooldownDuration(stat.Failures, cfg)
}

func sovereignTargetStructuralCooldownDuration(failures int, cfg RebalanceConfig) time.Duration {
	if failures >= 2 {
		hours := cfg.SovereignStructuralCooldownRepeatHours
		if hours <= 0 {
			hours = sovereignTargetStructuralCooldownRepeatDefaultHours
		}
		return time.Duration(hours) * time.Hour
	}
	return sovereignTargetStructuralCooldownFirst
}

// isInExplorationBurnout reports whether the target channel is currently
// in exploration burnout (locked out of receiving the ExplorationSlot mark
// because recent exploration attempts kept failing).
//
// Thread-safe; takes the exploration mutex.
func (s *RebalanceService) isInExplorationBurnout(channelID uint64, now time.Time) bool {
	if s == nil {
		return false
	}
	s.explorationStatsMu.Lock()
	defer s.explorationStatsMu.Unlock()
	stat := s.explorationStats[channelID]
	if stat == nil || stat.BurnedUntil.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	return now.Before(stat.BurnedUntil)
}

// recordExplorationOutcome updates the in-memory exploration window for a
// target channel after one of its exploration-marked jobs finished. When
// `succeeded` is true, the failure window is cleared and any active burnout
// is lifted. Otherwise the failure is recorded and burnout is re-evaluated
// against the threshold.
//
// Thread-safe.
func (s *RebalanceService) recordExplorationOutcome(channelID uint64, succeeded bool, now time.Time) {
	if s == nil || channelID == 0 {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.explorationStatsMu.Lock()
	defer s.explorationStatsMu.Unlock()
	if s.explorationStats == nil {
		s.explorationStats = make(map[uint64]*explorationStat)
	}
	stat := s.explorationStats[channelID]
	if stat == nil {
		stat = &explorationStat{}
		s.explorationStats[channelID] = stat
	}
	cutoff := now.Add(-sovereignExplorationBurnoutWindow)
	stat.AttemptsWindow = trimTimeWindow(stat.AttemptsWindow, cutoff)
	stat.FailuresWindow = trimTimeWindow(stat.FailuresWindow, cutoff)
	stat.AttemptsWindow = append(stat.AttemptsWindow, now)
	if succeeded {
		// Any success in the window clears the failure streak and lifts the
		// active burnout — the channel has demonstrated viability.
		stat.FailuresWindow = nil
		stat.BurnedUntil = time.Time{}
		return
	}
	stat.FailuresWindow = append(stat.FailuresWindow, now)
	if len(stat.FailuresWindow) >= sovereignExplorationBurnoutMinAttempts &&
		len(stat.FailuresWindow) == len(stat.AttemptsWindow) {
		stat.BurnedUntil = now.Add(sovereignExplorationBurnoutDuration)
		if s.logger != nil {
			s.logger.Printf("rebalance exploration burnout: channel=%d failures=%d window=%dh duration=%dh",
				channelID, len(stat.FailuresWindow),
				int(sovereignExplorationBurnoutWindow/time.Hour),
				int(sovereignExplorationBurnoutDuration/time.Hour))
		}
	}
}

// markExplorationJob remembers that a job was created in the exploration
// slot so that when it finishes we can call recordExplorationOutcome with
// the right channelID.
func (s *RebalanceService) markExplorationJob(jobID int64, channelID uint64) {
	if s == nil || jobID == 0 || channelID == 0 {
		return
	}
	s.explorationStatsMu.Lock()
	defer s.explorationStatsMu.Unlock()
	if s.explorationJobs == nil {
		s.explorationJobs = make(map[int64]uint64)
	}
	s.explorationJobs[jobID] = channelID
}

// takeExplorationJob looks up and removes the jobID from the exploration
// tracking map. Returns the channelID and whether the job was exploration.
func (s *RebalanceService) takeExplorationJob(jobID int64) (uint64, bool) {
	if s == nil || jobID == 0 {
		return 0, false
	}
	s.explorationStatsMu.Lock()
	defer s.explorationStatsMu.Unlock()
	channelID, ok := s.explorationJobs[jobID]
	if ok {
		delete(s.explorationJobs, jobID)
	}
	return channelID, ok
}

// trimTimeWindow drops timestamps older than cutoff from a sorted-ish slice.
// Used by exploration burnout to keep the moving window bounded.
func trimTimeWindow(window []time.Time, cutoff time.Time) []time.Time {
	if len(window) == 0 {
		return window
	}
	keep := window[:0]
	for _, t := range window {
		if !t.Before(cutoff) {
			keep = append(keep, t)
		}
	}
	return keep
}

// injectSovereignExplorationSlots applies an epsilon-greedy mark-and-reorder
// pass to a score-sorted candidate list. Each cycle a fraction (`pct`) of
// the available slots is dedicated to randomly chosen low-score candidates;
// they are tagged with ExplorationSlot=true so the sovereign loop can grant
// them gate bypass on empirical-history filters (structural_cooldown,
// low_success, route_dead, paid_liquidity_unsold, budget_efficiency_hard).
//
// Scarcity rule: when the non-probe candidate count is at most twice maxJobs,
// all candidates are marked as ExplorationSlot=true. The 2× multiplier
// captures the practical regime where there are "many" candidates on paper
// but most are blocked by empirical-history gates (structural_cooldown,
// route_dead, low_success) — without the bypass, only random tail picks
// would explore, missing high-score blocked candidates at the head. The
// position order is preserved so the score-based ranking still drives which
// jobs run within the maxJobs cap.
//
// Burnout filter: candidates for which `burnoutFn(channelID) == true` are
// kept in the candidate list (so the score ranking is unaffected) but never
// receive the ExplorationSlot mark. A burned-out target only goes through
// if it passes all gates on its own merit — exactly the behavior we want
// for chronically failing exploration targets.
//
// CooldownProbe entries (score=-1) always keep the head positions and are
// never marked. The non-explored tail retains its score order to act as a
// deterministic fallback when head candidates are gated out.
func injectSovereignExplorationSlots(candidates []rebalanceTarget, maxJobs int, pct int, burnoutFn func(uint64) bool, structuralFn func(rebalanceTarget) bool) []rebalanceTarget {
	if pct <= 0 || maxJobs <= 1 || len(candidates) <= 1 {
		return candidates
	}
	if pct > sovereignExplorationSlotPctMax {
		pct = sovereignExplorationSlotPctMax
	}
	// Compute exploration slot count. Round up so pct>0 always yields >=1.
	slots := (maxJobs * pct) / 100
	if slots == 0 {
		slots = 1
	}
	// Separate probes (score=-1, CooldownProbe) from real candidates.
	probeCount := 0
	for _, c := range candidates {
		if c.CooldownProbe {
			probeCount++
			continue
		}
		break
	}
	nonProbeCount := len(candidates) - probeCount
	if nonProbeCount <= 1 {
		// Need at least 2 non-probes so one can stay "top" and one can explore.
		return candidates
	}
	// Default burnoutFn to "never burned" when caller passes nil (e.g. unit
	// tests that exercise the pure ranking logic).
	if burnoutFn == nil {
		burnoutFn = func(uint64) bool { return false }
	}
	if structuralFn == nil {
		structuralFn = func(rebalanceTarget) bool { return false }
	}
	// excludeFromExploration: targets that must NOT receive the exploration
	// mark. Two cases, both meaning "let the hard guard act on its own merit":
	//   - R5 burnout: chronically-failing exploration target.
	//   - Structural cooldown threshold reached (>= 20 fails / 24h): without
	//     this, the M3 scarcity bypass below would mark a structurally-dead
	//     target as exploration and let it skip target_structural_cooldown
	//     forever (observed in prod 2026-05-31 with flashsats: 28 failures,
	//     never cooled down because the small candidate pool triggered M3 and
	//     the occasional partial kept resetting R5). Excluding it here makes
	//     the structural cooldown bite normally.
	excludeFromExploration := func(c rebalanceTarget) bool {
		return burnoutFn(c.Channel.ChannelID) || structuralFn(c)
	}
	// Scarcity bypass: when there are at most 2× maxJobs real candidates, the
	// batch is small enough that empirical-history gates would veto too many
	// top-ranked picks (kappa/CLB-style: high score, blocked by historical
	// failures). Mark every non-probe as exploration so the ranking — not the
	// gates — drives which jobs run within the maxJobs cap. Burned-out and
	// structurally-dead targets stay in the list but skip the mark — they only
	// proceed on own merit, ending the cycle-burn pattern.
	if nonProbeCount <= maxJobs*2 {
		out := make([]rebalanceTarget, 0, len(candidates))
		out = append(out, candidates[:probeCount]...)
		for _, c := range candidates[probeCount:] {
			if !excludeFromExploration(c) {
				c.ExplorationSlot = true
			}
			out = append(out, c)
		}
		return out
	}
	// Cap slots so at least one non-probe remains as deterministic top pick.
	if slots >= nonProbeCount {
		slots = nonProbeCount - 1
	}
	if slots <= 0 {
		return candidates
	}
	// Effective max is bounded by both maxJobs and candidate count so the
	// "top" group does not extend past the list when candidates are scarce.
	effectiveMax := maxJobs
	if effectiveMax > probeCount+nonProbeCount {
		effectiveMax = probeCount + nonProbeCount
	}
	keepTop := effectiveMax - slots
	if keepTop < probeCount+1 {
		keepTop = probeCount + 1
	}
	if keepTop >= len(candidates) {
		return candidates
	}
	probes := candidates[:probeCount]
	top := candidates[probeCount:keepTop]
	pool := candidates[keepTop:]
	actualSlots := slots
	if actualSlots > len(pool) {
		actualSlots = len(pool)
	}
	if actualSlots == 0 {
		return candidates
	}
	// Randomly choose actualSlots indexes from pool to mark as exploration.
	// Burned-out and structurally-dead targets are skipped here — they may end
	// up in the tail instead, still ranked by score, but without exploration
	// bypass (so target_structural_cooldown still bites them).
	idx := make([]int, 0, len(pool))
	for i, candidate := range pool {
		if excludeFromExploration(candidate) {
			continue
		}
		idx = append(idx, i)
	}
	rand.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	if actualSlots > len(idx) {
		actualSlots = len(idx)
	}
	exploredIdx := make(map[int]struct{}, actualSlots)
	explored := make([]rebalanceTarget, 0, actualSlots)
	for i := 0; i < actualSlots; i++ {
		entry := pool[idx[i]]
		entry.ExplorationSlot = true
		explored = append(explored, entry)
		exploredIdx[idx[i]] = struct{}{}
	}
	// Tail retains original score order so gated-head fallback is deterministic.
	tail := make([]rebalanceTarget, 0, len(pool)-actualSlots)
	for i, t := range pool {
		if _, ok := exploredIdx[i]; ok {
			continue
		}
		tail = append(tail, t)
	}
	out := make([]rebalanceTarget, 0, len(candidates))
	out = append(out, probes...)
	out = append(out, top...)
	out = append(out, explored...)
	out = append(out, tail...)
	return out
}

func shouldSkipSovereignUnsoldPaidLiquidity(stat sovereignUnsoldLiquidityStat, cfg RebalanceConfig, now time.Time) bool {
	if !hasSovereignUnsoldPaidLiquidity(stat, cfg, now) {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(stat.CompletedAt)
	if age < sovereignUnsoldPaidLiquidityHardAge {
		return true
	}
	attributionWindow := time.Duration(sovereignAttributionWindowHoursForConfig(cfg)) * time.Hour
	if age < attributionWindow {
		return false
	}
	return isSevereSovereignUnsoldPaidLiquidity(stat)
}

func hasSovereignUnsoldPaidLiquidity(stat sovereignUnsoldLiquidityStat, cfg RebalanceConfig, now time.Time) bool {
	if stat.CompletedAt.IsZero() || stat.SentSat <= 0 {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(stat.CompletedAt)
	lookback := time.Duration(sovereignSlowSellerWindowHoursForConfig(cfg)) * time.Hour
	if lookback < sovereignUnsoldPaidLiquidityLookback {
		lookback = sovereignUnsoldPaidLiquidityLookback
	}
	if age < sovereignUnsoldPaidLiquidityMinAge || age > lookback {
		return false
	}
	if stat.ForwardAmountSat*100 >= stat.SentSat*sovereignUnsoldPaidLiquidityMinForwardPct {
		return false
	}
	if stat.FeePaidSat > 0 && stat.ForwardFeeSat*100 >= stat.FeePaidSat*sovereignUnsoldPaidLiquidityMinFeePaybackPct {
		return false
	}
	return true
}

func isSevereSovereignUnsoldPaidLiquidity(stat sovereignUnsoldLiquidityStat) bool {
	fillPct := sovereignUnsoldLiquidityFillPct(stat)
	forwardPct := sovereignUnsoldLiquidityForwardPct(stat)
	paybackPct := sovereignUnsoldLiquidityPaybackPct(stat)
	return fillPct >= sovereignUnsoldPaidLiquiditySevereFillPct &&
		forwardPct < sovereignUnsoldPaidLiquiditySevereForwardPct &&
		paybackPct < sovereignUnsoldPaidLiquiditySeverePaybackPct
}

func sovereignUnsoldPaidLiquidityScoreMultiplier(stat sovereignUnsoldLiquidityStat, cfg RebalanceConfig, now time.Time) float64 {
	if !hasSovereignUnsoldPaidLiquidity(stat, cfg, now) {
		return 1
	}
	if shouldSkipSovereignUnsoldPaidLiquidity(stat, cfg, now) {
		return 0.1
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(stat.CompletedAt)
	if age < sovereignUnsoldPaidLiquidityHardAge {
		age = sovereignUnsoldPaidLiquidityHardAge
	}
	recovery := 0.0
	forwardPct := sovereignUnsoldLiquidityForwardPct(stat)
	if sovereignUnsoldPaidLiquidityMinForwardPct > 0 {
		recovery = math.Max(recovery, float64(forwardPct)/float64(sovereignUnsoldPaidLiquidityMinForwardPct))
	}
	paybackPct := sovereignUnsoldLiquidityPaybackPct(stat)
	if sovereignUnsoldPaidLiquidityMinFeePaybackPct > 0 {
		recovery = math.Max(recovery, float64(paybackPct)/float64(sovereignUnsoldPaidLiquidityMinFeePaybackPct))
	}
	if recovery > 1 {
		recovery = 1
	}
	lookback := time.Duration(sovereignSlowSellerWindowHoursForConfig(cfg)) * time.Hour
	if lookback < sovereignUnsoldPaidLiquidityLookback {
		lookback = sovereignUnsoldPaidLiquidityLookback
	}
	ageSpan := lookback - sovereignUnsoldPaidLiquidityHardAge
	ageProgress := 0.0
	if ageSpan > 0 {
		ageProgress = float64(age-sovereignUnsoldPaidLiquidityHardAge) / float64(ageSpan)
	}
	if ageProgress < 0 {
		ageProgress = 0
	}
	if ageProgress > 1 {
		ageProgress = 1
	}
	multiplier := 0.35 + (0.30 * recovery) + (0.20 * ageProgress)
	if sovereignUnsoldLiquidityFillPct(stat) < 50 {
		multiplier += 0.10
	}
	if multiplier < 0.25 {
		return 0.25
	}
	if multiplier > 0.85 {
		return 0.85
	}
	return multiplier
}

func sovereignUnsoldLiquidityFillPct(stat sovereignUnsoldLiquidityStat) int64 {
	if stat.TargetAmountSat <= 0 || stat.SentSat <= 0 {
		return 0
	}
	return clampInt64((stat.SentSat*100)/stat.TargetAmountSat, 0, 100)
}

func sovereignUnsoldLiquidityForwardPct(stat sovereignUnsoldLiquidityStat) int64 {
	if stat.SentSat <= 0 || stat.ForwardAmountSat <= 0 {
		return 0
	}
	return clampInt64((stat.ForwardAmountSat*100)/stat.SentSat, 0, 100)
}

func sovereignUnsoldLiquidityPaybackPct(stat sovereignUnsoldLiquidityStat) int64 {
	if stat.FeePaidSat <= 0 || stat.ForwardFeeSat <= 0 {
		return 0
	}
	return clampInt64((stat.ForwardFeeSat*100)/stat.FeePaidSat, 0, 100)
}

func clampInt64(value int64, min int64, max int64) int64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func sovereignProfitCostRatio(expectedProfitSat int64, budgetCostSat int64) (float64, bool) {
	if expectedProfitSat <= 0 {
		return 0, true
	}
	if budgetCostSat <= 0 {
		return 0, false
	}
	ratio := float64(expectedProfitSat) / float64(budgetCostSat)
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return 0, false
	}
	return ratio, true
}

func sovereignLowSuccessRateForConfig(cfg RebalanceConfig) float64 {
	return normalizeRatioConfig(cfg.SovereignLowSuccessMinRate, sovereignLowSuccessRate, 1)
}

func sovereignLowSuccessProfitCostRatioForConfig(cfg RebalanceConfig) float64 {
	return normalizeRatioConfig(cfg.SovereignLowSuccessMinProfitCostRatio, sovereignLowSuccessProfitCostRatio, 0)
}

func sovereignBudgetEfficiencyMinRatioForConfig(cfg RebalanceConfig) float64 {
	return normalizeRatioConfig(cfg.SovereignBudgetEfficiencyMinRatio, sovereignBudgetEfficiencyProfitCostRatio, 0)
}

func sovereignBudgetEfficiencyHighSuccessRatioForConfig(cfg RebalanceConfig) float64 {
	return sovereignBudgetEfficiencyMinRatioForConfig(cfg) / 2
}

func sovereignRouteDeadSourceShareForConfig(cfg RebalanceConfig) float64 {
	return normalizeRatioConfig(cfg.SovereignRouteDeadSourceShare, sovereignRouteDeadSourceShare, 1)
}

func sovereignRiskScoreFloorForConfig(cfg RebalanceConfig) float64 {
	return normalizeRatioConfig(cfg.SovereignRiskScoreFloor, sovereignRiskScoreFloor, 0.2)
}

func fastPathMaxTimeoutSecForConfig(cfg RebalanceConfig) int {
	v := cfg.FastPathMaxTimeoutSec
	if v < fastPathMaxTimeoutSecMin || v > fastPathMaxTimeoutSecMax {
		return fastPathMaxTimeoutSecDefault
	}
	return v
}

func sovereignTopBucketPctForConfig(cfg RebalanceConfig) int {
	v := cfg.SovereignTopBucketPct
	if v < sovereignTopBucketPctMin || v > sovereignTopBucketPctMax {
		return sovereignTopBucketPctDefault
	}
	return v
}

func sovereignLowSuccessRequiredProfitCostRatio(successRate float64, cfg RebalanceConfig) float64 {
	base := sovereignLowSuccessProfitCostRatioForConfig(cfg)
	switch {
	case successRate < sovereignLowSuccessVeryWeakRate:
		return math.Max(base, sovereignLowSuccessVeryWeakProfitCostRatio)
	case successRate < sovereignLowSuccessWeakRate:
		return math.Max(base, sovereignLowSuccessWeakProfitCostRatio)
	default:
		return base
	}
}

func sovereignRouteDeadShare(stats rebalanceTargetPairStats, eligibleSources int, cfg RebalanceConfig) float64 {
	failures := stats.RecentStructuralFailures
	if failures <= 0 {
		return 0
	}
	if eligibleSources <= 0 {
		if failures >= sovereignRouteDeadFallbackSevereFailSources {
			return sovereignRouteDeadSevereSourceShare
		}
		if failures >= sovereignRouteDeadFallbackMinFailedSources {
			return sovereignRouteDeadSourceShareForConfig(cfg)
		}
		return 0
	}
	share := float64(failures) / float64(eligibleSources)
	if math.IsNaN(share) || math.IsInf(share, 0) || share < 0 {
		return 0
	}
	if share > 1 {
		return 1
	}
	return share
}

func sovereignRouteDeadRequiredProfitCostRatio(routeDeadShare float64) float64 {
	switch {
	case routeDeadShare >= sovereignRouteDeadHighSourceShare:
		return sovereignRouteDeadHighProfitCostRatio
	case routeDeadShare >= sovereignRouteDeadMediumSourceShare:
		return sovereignRouteDeadMediumProfitCostRatio
	default:
		return sovereignRouteDeadProfitCostRatio
	}
}

func sovereignHistoricalSuccessRate(stats rebalanceTargetPairStats) (float64, int) {
	attempts := stats.Attempts
	if attempts <= 0 {
		attempts = stats.Successes + stats.Failures
	}
	if attempts <= 0 {
		return 0, 0
	}
	successes := stats.Successes
	if successes < 0 {
		successes = 0
	}
	if successes > attempts {
		successes = attempts
	}
	return float64(successes) / float64(attempts), attempts
}

func successRate(successes int, attempts int) float64 {
	if attempts <= 0 {
		return 0
	}
	if successes < 0 {
		successes = 0
	}
	if successes > attempts {
		successes = attempts
	}
	return float64(successes) / float64(attempts)
}

func sovereignSuccessRateForRisk(stats rebalanceTargetPairStats) (float64, int) {
	if stats.RecentStatsLoaded {
		return successRate(stats.RecentSuccesses, stats.RecentAttempts), stats.RecentAttempts
	}
	return sovereignHistoricalSuccessRate(stats)
}

func sovereignSuccessScoreMultiplier(stats rebalanceTargetPairStats, cfg RebalanceConfig) float64 {
	successRate, attempts := sovereignSuccessRateForRisk(stats)
	floor := sovereignRiskScoreFloorForConfig(cfg)
	multiplier := 0.9
	if attempts > 0 {
		confidence := float64(attempts) / float64(sovereignLowSuccessMinAttempts)
		if confidence > 1 {
			confidence = 1
		}
		observed := floor
		if successRate > 0 {
			observed += (1 - floor) * math.Pow(successRate, 0.85)
		}
		multiplier = ((1 - confidence) * 1.0) + (confidence * observed)
		if attempts >= sovereignLowSuccessMinAttempts && successRate > 0.5 {
			multiplier += math.Min(0.15, (successRate-0.5)*0.3)
		}
	}
	if stats.RecentStructuralFailures > 0 {
		// R1: curva contínua 1/(1+0.05·N) no lugar do cliff antigo (×0.05 ≥25,
		// ×0.12 ≥10, pressure linear 1-3). O guard duro continua em
		// target_structural_cooldown (skip), que não toca a partir desta função.
		// Aqui é apenas o multiplicador soft do ranking — permite que canais
		// com falhas isoladas voltem a competir após o cooldown expirar, em vez
		// de ficarem zerados por horas. Floor mínimo aplicado no clamp final.
		pressure := 1.0 / (1.0 + 0.05*float64(stats.RecentStructuralFailures))
		multiplier *= pressure
	}
	if stats.PermanentFailScore >= permanentFailScoreSkipThreshold {
		multiplier *= 0.20
	} else if stats.PermanentFailScore > 0 {
		pressure := 1 - (stats.PermanentFailScore / (permanentFailScoreSkipThreshold * 2))
		if pressure < 0.35 {
			pressure = 0.35
		}
		multiplier *= pressure
	}
	if multiplier < floor {
		return floor
	}
	if multiplier > 1.2 {
		return 1.2
	}
	return multiplier
}

func sovereignROIScoreMultiplier(expectedROI float64, roiValid bool, cfg RebalanceConfig) float64 {
	// Cold-start: when ROI cannot be evaluated (no rebalance history yet)
	// apply only a mild penalty so brand-new pairs can still be probed by the
	// autopilot. The hard ROIMin gate already filters truly bad candidates.
	if !roiValid || expectedROI <= 0 || math.IsNaN(expectedROI) || math.IsInf(expectedROI, 0) {
		return 0.9
	}
	baseline := cfg.ROIMin
	if baseline <= 0 || math.IsNaN(baseline) || math.IsInf(baseline, 0) {
		baseline = 1.0
	}
	ratio := expectedROI / baseline
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return 0.9
	}
	multiplier := math.Sqrt(ratio)
	if multiplier < 0.5 {
		return 0.5
	}
	if multiplier > 2 {
		return 2
	}
	return multiplier
}

func sovereignBudgetEfficiencyScoreMultiplier(candidate rebalanceTarget) float64 {
	profit := candidate.ExpectedGainSat - candidate.EstimatedCostSat
	if profit <= 0 {
		return 0.5
	}
	budgetCost := candidate.BudgetCostSat
	if budgetCost <= 0 {
		budgetCost = candidate.EstimatedCostSat
	}
	if budgetCost <= 0 {
		return 1
	}
	efficiency := float64(profit) / float64(budgetCost)
	if efficiency <= 0 || math.IsNaN(efficiency) || math.IsInf(efficiency, 0) {
		return 0.5
	}
	multiplier := math.Sqrt(efficiency)
	if multiplier < 0.35 {
		return 0.35
	}
	if multiplier > 1.75 {
		return 1.75
	}
	return multiplier
}

func sovereignRealizedEconomicsScoreMultiplier(stats rebalanceTargetPairStats) float64 {
	if stats.RecentSentJobs <= 0 || stats.RecentSentSat <= 0 || stats.RecentRebalanceFeeSat <= 0 {
		return 1
	}
	sellThrough := ratio64(stats.RecentForwardAmountSat, stats.RecentSentSat)
	feePayback := ratio64(stats.RecentForwardFeeSat, stats.RecentRebalanceFeeSat)
	observed := 0.9
	switch {
	case feePayback >= 1.25 && sellThrough >= 0.50:
		observed = 1.35
	case feePayback >= 1.00 && sellThrough >= 0.25:
		observed = 1.20
	case feePayback >= 0.75 && sellThrough >= 0.50:
		observed = 1.05
	case sellThrough < 0.10 && feePayback < 0.25:
		observed = 0.35
	case feePayback < 0.50:
		observed = 0.55
	case sellThrough < 0.25:
		observed = 0.70
	}
	if stats.RecentRealizedNetSat > 0 && observed < 1.45 {
		observed += math.Min(0.15, feePayback-1)
	}
	if observed < 0.30 {
		observed = 0.30
	}
	if observed > 1.50 {
		observed = 1.50
	}
	confidence := float64(stats.RecentSentJobs) / float64(sovereignRealizedEconomicsConfidenceJobs)
	if confidence > 1 {
		confidence = 1
	}
	if confidence < 0 {
		confidence = 0
	}
	multiplier := 1 + (observed-1)*confidence
	if multiplier < 0.30 {
		return 0.30
	}
	if multiplier > 1.50 {
		return 1.50
	}
	return multiplier
}

func ratio64(numerator int64, denominator int64) float64 {
	if numerator <= 0 || denominator <= 0 {
		return 0
	}
	ratio := float64(numerator) / float64(denominator)
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
		return 0
	}
	return ratio
}

// applyAutofeeSettlingPenalty implements Wave 6.1b: targets que tiveram a fee
// de saída ajustada pelo AutoFee dentro do window têm o score multiplicado por
// cfg.AutofeeSettlingMultiplier. Não é skip — é despriorização no sort.
// Retorna a contagem de candidatos afetados (probes não contam).
func applyAutofeeSettlingPenalty(candidates []rebalanceTarget, adjustments map[uint64]time.Time, cfg RebalanceConfig, scanAt time.Time) int {
	if cfg.AutofeeSettlingWindowSec <= 0 || cfg.AutofeeSettlingMultiplier >= 1 || len(adjustments) == 0 || len(candidates) == 0 {
		return 0
	}
	multiplier := cfg.AutofeeSettlingMultiplier
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier < 0 {
		return 0
	}
	if scanAt.IsZero() {
		scanAt = time.Now()
	}
	window := time.Duration(cfg.AutofeeSettlingWindowSec) * time.Second
	dampened := 0
	for i := range candidates {
		if candidates[i].CooldownProbe {
			continue
		}
		adjustedAt, ok := adjustments[candidates[i].Channel.ChannelID]
		if !ok || adjustedAt.IsZero() {
			continue
		}
		if adjustedAt.After(scanAt) {
			// Future timestamp (clock skew) — treat as just-adjusted.
		} else if scanAt.Sub(adjustedAt) > window {
			continue
		}
		candidates[i].Score = int64(math.Round(float64(candidates[i].Score) * multiplier))
		candidates[i].AutofeeDampened = true
		candidates[i].AutofeeAdjustedAt = adjustedAt
		dampened++
	}
	return dampened
}

func buildScanDetail(reasons map[string]int, remaining int64, candidates int, queued int) string {
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
		{key: sovereignTargetStructuralCooldownReason, label: "target structural cooldown"},
		{key: sovereignUnsoldPaidLiquidityReason, label: "paid liquidity unsold cooldown"},
		{key: sovereignUnsoldPaidLiquidityPenaltyReason, label: "paid liquidity unsold penalty"},
		{key: sovereignRouteDeadOpportunityReason, label: "route dead opportunity below floor"},
		{key: sovereignLowSuccessOpportunityReason, label: "low success opportunity below floor"},
		{key: sovereignBudgetEfficiencyOpportunityReason, label: "budget efficiency below floor"},
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
	if queued > 0 {
		base = fmt.Sprintf("Queued %d job(s).", queued)
		if candidates > 0 {
			base = fmt.Sprintf("Queued %d job(s). Candidates after guardrails: %d.", queued, candidates)
		}
	} else if candidates > 0 {
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
    scheduler_mode text not null default 'rules_auto',
    rebalance_profile text not null default 'custom',
    sovereign_candidate_scope text not null default 'auto_and_manual_restart',
    sovereign_max_jobs_per_cycle integer not null default 2,
    sovereign_min_expected_profit_sat bigint not null default 0,
    sovereign_low_success_min_rate double precision not null default 0.02,
    sovereign_low_success_min_profit_cost_ratio double precision not null default 1.2,
    sovereign_budget_efficiency_min_ratio double precision not null default 0.5,
    sovereign_route_dead_source_share double precision not null default 0.2,
    sovereign_risk_score_floor double precision not null default 0.02,
    sovereign_gain_v3_cold_start_pct double precision not null default 0.75,
    fast_path_max_timeout_sec integer not null default 90,
    sovereign_top_bucket_pct integer not null default 30,
    manual_restart_ignore_economic_gates boolean not null default false,
    sovereign_attribution_window_hours integer not null default 72,
    sovereign_slow_seller_window_hours integer not null default 168,
    sovereign_target_source_quarantine_hours integer not null default 6,
    sovereign_structural_cooldown_repeat_hours integer not null default 6,
    sovereign_exploration_slot_pct integer not null default 0,
    sovereign_source_opportunity_cost_enabled boolean not null default true,
    sovereign_slow_seller_enabled boolean not null default true,
    scan_interval_sec integer not null default 900,
    deadband_pct double precision not null default 5,
    source_min_local_pct double precision not null default 35,
    econ_ratio double precision not null default 0.6,
    econ_ratio_max_ppm bigint not null default 0,
    fee_limit_ppm bigint not null default 0,
    lost_profit boolean not null default false,
    fail_tolerance_ppm bigint not null default 500,
    roi_min double precision not null default 1.1,
    daily_budget_pct double precision not null default 25,
    budget_mode text not null default 'hybrid_revenue',
    budget_unlimited boolean not null default false,
    budget_auto_only boolean not null default true,
    manual_reserve_enabled boolean not null default false,
    manual_reserve_mode text not null default 'fixed_sat',
    manual_reserve_value double precision not null default 0,
    max_concurrent integer not null default 2,
    min_amount_sat bigint not null default 50000,
    max_amount_sat bigint not null default 0,
    min_split_enabled boolean not null default true,
    min_probe_sat bigint not null default 5000,
    min_execute_sat bigint not null default 10000,
    mpp_enabled boolean not null default true,
    mpp_max_shards integer not null default 6,
    mpp_parallelism integer not null default 3,
    mpp_min_shard_sat bigint not null default 10000,
    mpp_round_timeout_sec integer not null default 35,
    mpp_auto_only boolean not null default true,
    fee_ladder_steps integer not null default 1,
    amount_probe_steps integer not null default 6,
    amount_probe_adaptive boolean not null default true,
    attempt_timeout_sec integer not null default 45,
    rebalance_timeout_sec integer not null default 600,
    manual_restart_watch boolean not null default false,
    cooldown_probe_enabled boolean not null default false,
    mc_half_life_sec bigint not null default 0,
    payback_mode_flags integer not null default 7,
    fresh_paid_liquidity_lock_enabled boolean not null default true,
    fresh_paid_liquidity_lock_hours integer not null default 6,
    unlock_days integer not null default 7,
    critical_release_pct double precision not null default 20,
    critical_min_sources integer not null default 2,
    critical_min_available_sats bigint not null default 0,
    critical_cycles integer not null default 3,
    gain_model_version integer not null default 1,
    velocity_weight double precision not null default 0.7,
    autofee_settling_window_sec bigint not null default 7200,
    autofee_settling_multiplier double precision not null default 0.5,
    delegated_fast_path_enabled boolean not null default true,
    delegated_fast_path_strict_payback boolean not null default true,
    updated_at timestamptz not null default now()
  );

  alter table rebalance_config
    add column if not exists scheduler_mode text not null default 'rules_auto';
  alter table rebalance_config
    add column if not exists rebalance_profile text not null default 'custom';
  alter table rebalance_config
    add column if not exists sovereign_candidate_scope text not null default 'auto_and_manual_restart';
  alter table rebalance_config
    add column if not exists sovereign_max_jobs_per_cycle integer not null default 2;
  alter table rebalance_config
    add column if not exists sovereign_min_expected_profit_sat bigint not null default 0;
  alter table rebalance_config
    add column if not exists sovereign_low_success_min_rate double precision not null default 0.02;
  alter table rebalance_config
    add column if not exists sovereign_low_success_min_profit_cost_ratio double precision not null default 1.2;
  alter table rebalance_config
    add column if not exists sovereign_budget_efficiency_min_ratio double precision not null default 0.5;
  alter table rebalance_config
    add column if not exists sovereign_route_dead_source_share double precision not null default 0.2;
  alter table rebalance_config
    add column if not exists sovereign_risk_score_floor double precision not null default 0.02;
  alter table rebalance_config
    add column if not exists sovereign_gain_v3_cold_start_pct double precision not null default 0.75;
  alter table rebalance_config
    add column if not exists fast_path_max_timeout_sec integer not null default 90;
  alter table rebalance_config
    add column if not exists sovereign_top_bucket_pct integer not null default 30;
  alter table rebalance_config
    add column if not exists manual_restart_ignore_economic_gates boolean not null default false;
  alter table rebalance_config
    add column if not exists sovereign_attribution_window_hours integer not null default 72;
  alter table rebalance_config
    add column if not exists sovereign_slow_seller_window_hours integer not null default 168;
  alter table rebalance_config
    add column if not exists sovereign_target_source_quarantine_hours integer not null default 6;
  alter table rebalance_config
    add column if not exists sovereign_structural_cooldown_repeat_hours integer not null default 6;
  alter table rebalance_config
    add column if not exists sovereign_exploration_slot_pct integer not null default 0;
  alter table rebalance_config
    add column if not exists sovereign_source_opportunity_cost_enabled boolean not null default true;
  alter table rebalance_config
    add column if not exists sovereign_slow_seller_enabled boolean not null default true;
  alter table rebalance_config
    add column if not exists source_min_local_pct double precision not null default 35;
  alter table rebalance_config
    add column if not exists econ_ratio_max_ppm bigint not null default 0;
  alter table rebalance_config
    add column if not exists fee_limit_ppm bigint not null default 0;
  alter table rebalance_config
    add column if not exists lost_profit boolean not null default false;
  alter table rebalance_config
    add column if not exists fail_tolerance_ppm bigint not null default 500;
  alter table rebalance_config
    add column if not exists amount_probe_steps integer not null default 6;
  alter table rebalance_config
    add column if not exists budget_mode text not null default 'hybrid_revenue';
  alter table rebalance_config
    add column if not exists budget_unlimited boolean not null default false;
  alter table rebalance_config
    add column if not exists budget_auto_only boolean not null default true;
  alter table rebalance_config
    add column if not exists manual_reserve_enabled boolean not null default false;
  alter table rebalance_config
    add column if not exists manual_reserve_mode text not null default 'fixed_sat';
  alter table rebalance_config
    add column if not exists manual_reserve_value double precision not null default 0;
  alter table rebalance_config
    add column if not exists min_split_enabled boolean not null default true;
  alter table rebalance_config
    add column if not exists min_probe_sat bigint not null default 5000;
  alter table rebalance_config
    add column if not exists min_execute_sat bigint not null default 10000;
  alter table rebalance_config
    add column if not exists mpp_enabled boolean not null default true;
  alter table rebalance_config
    add column if not exists mpp_max_shards integer not null default 6;
  alter table rebalance_config
    add column if not exists mpp_parallelism integer not null default 3;
  alter table rebalance_config
    add column if not exists mpp_min_shard_sat bigint not null default 10000;
  alter table rebalance_config
    add column if not exists mpp_round_timeout_sec integer not null default 35;
  alter table rebalance_config
    alter column mpp_max_shards set default 6;
  alter table rebalance_config
    alter column mpp_parallelism set default 3;
  alter table rebalance_config
    alter column mpp_min_shard_sat set default 10000;
  alter table rebalance_config
    alter column mpp_round_timeout_sec set default 35;
  alter table rebalance_config
    add column if not exists mpp_auto_only boolean not null default true;
  alter table rebalance_config
    add column if not exists amount_probe_adaptive boolean not null default true;
  alter table rebalance_config
    add column if not exists attempt_timeout_sec integer not null default 45;
  alter table rebalance_config
    add column if not exists rebalance_timeout_sec integer not null default 600;
 alter table rebalance_config
    add column if not exists manual_restart_watch boolean not null default false;
  alter table rebalance_config
    add column if not exists cooldown_probe_enabled boolean not null default false;
  alter table rebalance_config
    add column if not exists mc_half_life_sec bigint not null default 0;
  alter table rebalance_config
    add column if not exists fresh_paid_liquidity_lock_enabled boolean not null default true;
  alter table rebalance_config
    add column if not exists fresh_paid_liquidity_lock_hours integer not null default 6;
  alter table rebalance_config
    add column if not exists new_channel_exclusion_seeded boolean not null default false;
  alter table rebalance_config
    add column if not exists rebalance_cost_floor_ppm bigint not null default 250;
  alter table rebalance_config
    add column if not exists source_min_payback_progress double precision not null default 0.95;
  alter table rebalance_config
    add column if not exists mission_control_reinforce boolean not null default false;
  alter table rebalance_config
    add column if not exists gain_model_version integer not null default 1;
  alter table rebalance_config
    add column if not exists velocity_weight double precision not null default 0.7;
  alter table rebalance_config
    add column if not exists autofee_settling_window_sec bigint not null default 7200;
  alter table rebalance_config
    add column if not exists autofee_settling_multiplier double precision not null default 0.5;
  alter table rebalance_config
    add column if not exists delegated_fast_path_enabled boolean not null default true;
  alter table rebalance_config
    add column if not exists delegated_fast_path_strict_payback boolean not null default true;

  alter table rebalance_config
    alter column scheduler_mode set default 'rules_auto';
  alter table rebalance_config
    alter column sovereign_candidate_scope set default 'auto_and_manual_restart';
  alter table rebalance_config
    alter column sovereign_max_jobs_per_cycle set default 2;
  alter table rebalance_config
    alter column sovereign_min_expected_profit_sat set default 0;
  alter table rebalance_config
    alter column sovereign_low_success_min_rate set default 0.02;
  alter table rebalance_config
    alter column sovereign_low_success_min_profit_cost_ratio set default 1.2;
  alter table rebalance_config
    alter column sovereign_budget_efficiency_min_ratio set default 0.5;
  alter table rebalance_config
    alter column sovereign_route_dead_source_share set default 0.2;
  alter table rebalance_config
    alter column sovereign_risk_score_floor set default 0.02;
  alter table rebalance_config
    alter column sovereign_gain_v3_cold_start_pct set default 0.75;
  alter table rebalance_config
    alter column fast_path_max_timeout_sec set default 90;
  alter table rebalance_config
    alter column sovereign_top_bucket_pct set default 30;
  alter table rebalance_config
    alter column manual_restart_ignore_economic_gates set default false;
  alter table rebalance_config
    alter column sovereign_attribution_window_hours set default 72;
  alter table rebalance_config
    alter column sovereign_slow_seller_window_hours set default 168;
  alter table rebalance_config
    alter column sovereign_target_source_quarantine_hours set default 6;
  alter table rebalance_config
    alter column sovereign_structural_cooldown_repeat_hours set default 6;
  alter table rebalance_config
    alter column sovereign_exploration_slot_pct set default 0;
  alter table rebalance_config
    alter column sovereign_source_opportunity_cost_enabled set default true;
  alter table rebalance_config
    alter column sovereign_slow_seller_enabled set default true;
  alter table rebalance_config
    alter column scan_interval_sec set default 900;
  alter table rebalance_config
    alter column deadband_pct set default 5;
  alter table rebalance_config
    alter column source_min_local_pct set default 35;
  alter table rebalance_config
    alter column fail_tolerance_ppm set default 500;
  alter table rebalance_config
    alter column daily_budget_pct set default 25;
  alter table rebalance_config
    alter column budget_mode set default 'hybrid_revenue';
  alter table rebalance_config
    alter column budget_unlimited set default false;
  alter table rebalance_config
    alter column budget_auto_only set default true;
  alter table rebalance_config
    alter column min_amount_sat set default 50000;
  alter table rebalance_config
    alter column min_split_enabled set default true;
  alter table rebalance_config
    alter column min_probe_sat set default 5000;
  alter table rebalance_config
    alter column min_execute_sat set default 10000;
  alter table rebalance_config
    alter column mpp_enabled set default true;
  alter table rebalance_config
    alter column mpp_auto_only set default true;
  alter table rebalance_config
    alter column fee_ladder_steps set default 1;
  alter table rebalance_config
    alter column amount_probe_steps set default 6;
  alter table rebalance_config
    alter column attempt_timeout_sec set default 45;
  alter table rebalance_config
    alter column unlock_days set default 7;
  alter table rebalance_config
    alter column gain_model_version set default 1;
  alter table rebalance_config
    alter column velocity_weight set default 0.7;
  alter table rebalance_config
    alter column autofee_settling_window_sec set default 7200;
  alter table rebalance_config
    alter column autofee_settling_multiplier set default 0.5;
  alter table rebalance_config
    alter column delegated_fast_path_enabled set default true;
  alter table rebalance_config
    alter column delegated_fast_path_strict_payback set default true;
  alter table rebalance_config
    alter column fresh_paid_liquidity_lock_enabled set default true;
  alter table rebalance_config
    alter column fresh_paid_liquidity_lock_hours set default 6;
  alter table rebalance_config
    alter column cooldown_probe_enabled set default false;
  alter table rebalance_config
    add column if not exists auto_target_enabled boolean not null default false;
  alter table rebalance_config
    add column if not exists auto_target_max_pct integer not null default 50;
  alter table rebalance_config
    add column if not exists auto_target_min_pct integer not null default 10;
  alter table rebalance_config
    add column if not exists auto_target_step_pct integer not null default 5;
  alter table rebalance_config
    add column if not exists auto_target_eval_interval_hours integer not null default 6;
  alter table rebalance_config
    add column if not exists auto_target_max_ups_per_cycle integer not null default 3;
  alter table rebalance_config
    add column if not exists auto_target_max_local_sat bigint not null default 5000000;
  alter table rebalance_config
    add column if not exists auto_target_min_drain_rate_sat_per_hr bigint not null default 5000;
  alter table rebalance_config
    add column if not exists auto_target_min_revenue_7d_sat bigint not null default 500;
  alter table rebalance_config
    add column if not exists auto_target_up_success_threshold double precision not null default 0.5;
  alter table rebalance_config
    add column if not exists auto_target_down_success_threshold double precision not null default 0.25;
  alter table rebalance_config
    add column if not exists auto_target_drain_first_multiplier double precision not null default 3.0;

  alter table if exists rebalance_channel_settings
    add column if not exists manual_restart_enabled boolean not null default false;
  alter table if exists rebalance_channel_settings
    add column if not exists use_default_econ_ratio boolean not null default true;
  alter table if exists rebalance_channel_settings
    add column if not exists econ_ratio_override double precision;
  alter table if exists rebalance_channel_settings
    add column if not exists auto_bypass_cost_gate boolean not null default false;
  alter table if exists rebalance_channel_settings
    add column if not exists auto_target_managed boolean not null default true;

create table if not exists rebalance_channel_settings (
  channel_id bigint primary key,
  channel_point text not null,
  target_outbound_pct double precision not null default 50,
  auto_enabled boolean not null default false,
  manual_restart_enabled boolean not null default false,
  use_default_econ_ratio boolean not null default true,
  econ_ratio_override double precision,
  auto_bypass_cost_gate boolean not null default false,
  auto_target_managed boolean not null default true,
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
  trigger_reason text,
  reason text,
  target_channel_id bigint not null,
  target_channel_point text not null,
  target_outbound_pct double precision not null,
  target_amount_sat bigint not null,
  config_snapshot jsonb,
  fast_path_attempted boolean not null default false,
  sovereign_expected_gain_sat bigint not null default 0,
  sovereign_estimated_cost_sat bigint not null default 0,
  sovereign_expected_profit_sat bigint not null default 0,
  sovereign_budget_cost_sat bigint not null default 0,
  sovereign_score bigint not null default 0
);

alter table if exists rebalance_jobs
  add column if not exists trigger_reason text;
alter table if exists rebalance_jobs
  add column if not exists fast_path_attempted boolean not null default false;
alter table if exists rebalance_jobs
  add column if not exists sovereign_expected_gain_sat bigint not null default 0;
alter table if exists rebalance_jobs
  add column if not exists sovereign_estimated_cost_sat bigint not null default 0;
alter table if exists rebalance_jobs
  add column if not exists sovereign_expected_profit_sat bigint not null default 0;
alter table if exists rebalance_jobs
  add column if not exists sovereign_budget_cost_sat bigint not null default 0;
alter table if exists rebalance_jobs
  add column if not exists sovereign_score bigint not null default 0;
-- Backfill: jobs com reason='delegated-fast-path' são por definição sucessos via fast-path,
-- então marcamos retroativamente para que o telemetry não subconte (idempotente).
update rebalance_jobs set fast_path_attempted=true where reason='delegated-fast-path' and not fast_path_attempted;
update rebalance_jobs
  set trigger_reason=reason
  where trigger_reason is null
    and reason is not null
    and status in ('queued','running');

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
  fail_source_index integer,
  fail_hop_pubkey text,
  started_at timestamptz not null default now(),
  finished_at timestamptz
);

alter table if exists rebalance_attempts
  add column if not exists fail_source_index integer;
alter table if exists rebalance_attempts
  add column if not exists fail_hop_pubkey text;

create table if not exists rebalance_metrics_daily (
  day date primary key,
  jobs_total integer not null default 0,
  jobs_succeeded integer not null default 0,
  jobs_partial integer not null default 0,
  jobs_failed integer not null default 0,
  jobs_cancelled integer not null default 0,
  attempts_total bigint not null default 0,
  attempts_succeeded bigint not null default 0,
  success_rate double precision not null default 0,
  partial_rate double precision not null default 0,
  failed_rate double precision not null default 0,
  avg_attempts_per_job double precision not null default 0,
  avg_sats_per_successful_job double precision not null default 0,
  avg_fee_ppm_paid double precision not null default 0,
  fee_paid_sat_total bigint not null default 0,
  amount_succeeded_sat_total bigint not null default 0,
  time_to_payback_p50_hours double precision,
  updated_at timestamptz not null default now()
);

create index if not exists rebalance_metrics_daily_day_idx on rebalance_metrics_daily (day desc);

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
  permanent_fail_score double precision not null default 0,
  permanent_fail_updated_at timestamptz,
  primary key (source_channel_id, target_channel_id)
);

create table if not exists rebalance_scan_skips (
  id bigserial primary key,
  scan_at timestamptz not null,
  channel_id bigint not null default 0,
  channel_point text not null default '',
  peer_alias text not null default '',
  target_outbound_pct double precision not null default 0,
  target_amount_sat bigint not null default 0,
  expected_gain_sat bigint not null default 0,
  estimated_cost_sat bigint not null default 0,
  expected_roi double precision not null default 0,
  expected_roi_valid boolean not null default false,
  reason text not null,
  created_at timestamptz not null default now()
);

create table if not exists rebalance_sovereign_history (
  id bigserial primary key,
  scan_at timestamptz not null,
  mode text not null,
  status text not null,
  candidates integer not null default 0,
  selected integer not null default 0,
  expected_profit_sat bigint not null default 0,
  budget_remaining_sat bigint not null default 0,
  skip_reasons jsonb not null default '{}'::jsonb,
  decisions jsonb not null default '[]'::jsonb,
  detail text not null default '',
  created_at timestamptz not null default now()
);

create table if not exists rebalance_auto_target_history (
  id bigserial primary key,
  channel_id bigint not null,
  channel_point text,
  decided_at timestamptz not null default now(),
  prev_target_pct integer,
  new_target_pct integer,
  direction text,
  applied boolean not null default false,
  trigger_signals jsonb not null default '{}'::jsonb,
  measurement_window_hours integer,
  created_at timestamptz not null default now()
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
create index if not exists rebalance_scan_skips_scan_idx on rebalance_scan_skips (scan_at desc);
create index if not exists rebalance_scan_skips_reason_idx on rebalance_scan_skips (reason, scan_at desc);
create index if not exists rebalance_sovereign_history_scan_idx on rebalance_sovereign_history (scan_at desc);
create index if not exists rebalance_auto_target_history_ch_idx on rebalance_auto_target_history (channel_id, decided_at desc);
create index if not exists notifications_channel_id_idx on notifications (channel_id);
create index if not exists notifications_channel_occurred_idx on notifications (channel_id, occurred_at desc);

alter table if exists rebalance_pair_stats
  add column if not exists last_success_route_hops jsonb;
alter table if exists rebalance_pair_stats
  add column if not exists permanent_fail_score double precision not null default 0;
alter table if exists rebalance_pair_stats
  add column if not exists permanent_fail_updated_at timestamptz;

create table if not exists rebalance_config_snapshots (
  id bigserial primary key,
  name text not null unique,
  kind text not null default 'manual',
  profile text not null default '',
  config_json jsonb not null,
  created_at timestamptz not null default now()
);
`)
	if err != nil {
		return err
	}
	// Seed the singleton config row from defaultRebalanceConfig() — the Go
	// starter profile asserted by TestDefaultRebalanceConfigStarterProfile — when
	// it does not exist yet, so a fresh node reflects the balanced defaults we
	// validated in production. The SQL column defaults had drifted from Go over
	// time (e.g. scheduler_mode, gain_model_version, exploration, budget), so the
	// old `insert (id) values (1)` seeded a stale config. Using upsertConfig with
	// an existence guard makes Go the single source of truth and never touches an
	// already-seeded row, preserving every operator's tuning.
	var configExists bool
	if err = s.db.QueryRow(ctx, `select exists(select 1 from rebalance_config where id=$1)`, rebalanceConfigID).Scan(&configExists); err != nil {
		return err
	}
	if err := ensureChannelAutomationSchema(ctx, s.db); err != nil {
		return err
	}
	if !configExists {
		return s.upsertConfig(ctx, normalizeRebalanceConfig(defaultRebalanceConfig()))
	}
	return nil
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
  select auto_enabled, scheduler_mode, sovereign_candidate_scope, sovereign_max_jobs_per_cycle, sovereign_min_expected_profit_sat, sovereign_low_success_min_rate, sovereign_low_success_min_profit_cost_ratio, sovereign_budget_efficiency_min_ratio, sovereign_route_dead_source_share, sovereign_risk_score_floor, scan_interval_sec, deadband_pct, source_min_local_pct, econ_ratio, econ_ratio_max_ppm, fee_limit_ppm, lost_profit, fail_tolerance_ppm, roi_min, daily_budget_pct, budget_mode, budget_unlimited, budget_auto_only, manual_reserve_enabled, manual_reserve_mode, manual_reserve_value,
    max_concurrent, min_amount_sat, max_amount_sat, min_split_enabled, min_probe_sat, min_execute_sat, mpp_enabled, mpp_max_shards, mpp_parallelism, mpp_min_shard_sat, mpp_round_timeout_sec, mpp_auto_only,
    fee_ladder_steps, amount_probe_steps, amount_probe_adaptive, attempt_timeout_sec, rebalance_timeout_sec, manual_restart_watch, cooldown_probe_enabled, mc_half_life_sec, payback_mode_flags, fresh_paid_liquidity_lock_enabled, fresh_paid_liquidity_lock_hours,
    unlock_days, critical_release_pct, critical_min_sources, critical_min_available_sats, critical_cycles, rebalance_cost_floor_ppm, source_min_payback_progress, mission_control_reinforce, gain_model_version, velocity_weight, autofee_settling_window_sec, autofee_settling_multiplier, delegated_fast_path_enabled, delegated_fast_path_strict_payback,
    sovereign_attribution_window_hours, sovereign_slow_seller_window_hours, sovereign_target_source_quarantine_hours, sovereign_structural_cooldown_repeat_hours, sovereign_exploration_slot_pct, sovereign_source_opportunity_cost_enabled, sovereign_slow_seller_enabled, sovereign_gain_v3_cold_start_pct, fast_path_max_timeout_sec, sovereign_top_bucket_pct, manual_restart_ignore_economic_gates, rebalance_profile,
    auto_target_enabled, auto_target_max_pct, auto_target_min_pct, auto_target_step_pct, auto_target_eval_interval_hours, auto_target_max_ups_per_cycle, auto_target_max_local_sat, auto_target_min_drain_rate_sat_per_hr, auto_target_min_revenue_7d_sat, auto_target_up_success_threshold, auto_target_down_success_threshold, auto_target_drain_first_multiplier
  from rebalance_config where id=$1`, rebalanceConfigID)

	cfg := defaultRebalanceConfig()
	err := row.Scan(
		&cfg.AutoEnabled,
		&cfg.SchedulerMode,
		&cfg.SovereignCandidateScope,
		&cfg.SovereignMaxJobsPerCycle,
		&cfg.SovereignMinExpectedProfitSat,
		&cfg.SovereignLowSuccessMinRate,
		&cfg.SovereignLowSuccessMinProfitCostRatio,
		&cfg.SovereignBudgetEfficiencyMinRatio,
		&cfg.SovereignRouteDeadSourceShare,
		&cfg.SovereignRiskScoreFloor,
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
		&cfg.BudgetUnlimited,
		&cfg.BudgetAutoOnly,
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
		&cfg.CooldownProbeEnabled,
		&cfg.MissionControlHalfLifeSec,
		&cfg.PaybackModeFlags,
		&cfg.FreshPaidLiquidityLockEnabled,
		&cfg.FreshPaidLiquidityLockHours,
		&cfg.UnlockDays,
		&cfg.CriticalReleasePct,
		&cfg.CriticalMinSources,
		&cfg.CriticalMinAvailableSats,
		&cfg.CriticalCycles,
		&cfg.RebalanceCostFloorPpm,
		&cfg.SourceMinPaybackProgress,
		&cfg.MissionControlReinforce,
		&cfg.GainModelVersion,
		&cfg.VelocityWeight,
		&cfg.AutofeeSettlingWindowSec,
		&cfg.AutofeeSettlingMultiplier,
		&cfg.DelegatedFastPathEnabled,
		&cfg.DelegatedFastPathStrictPayback,
		&cfg.SovereignAttributionWindowHours,
		&cfg.SovereignSlowSellerWindowHours,
		&cfg.SovereignTargetSourceQuarantineHours,
		&cfg.SovereignStructuralCooldownRepeatHours,
		&cfg.SovereignExplorationSlotPct,
		&cfg.SovereignSourceOpportunityCostEnabled,
		&cfg.SovereignSlowSellerEnabled,
		&cfg.SovereignGainV3ColdStartPct,
		&cfg.FastPathMaxTimeoutSec,
		&cfg.SovereignTopBucketPct,
		&cfg.ManualRestartIgnoreEconomicGates,
		&cfg.Profile,
		&cfg.AutoTargetEnabled,
		&cfg.AutoTargetMaxPct,
		&cfg.AutoTargetMinPct,
		&cfg.AutoTargetStepPct,
		&cfg.AutoTargetEvalIntervalHours,
		&cfg.AutoTargetMaxUpsPerCycle,
		&cfg.AutoTargetMaxLocalSat,
		&cfg.AutoTargetMinDrainRateSatPerHr,
		&cfg.AutoTargetMinRevenue7dSat,
		&cfg.AutoTargetUpSuccessThreshold,
		&cfg.AutoTargetDownSuccessThreshold,
		&cfg.AutoTargetDrainFirstMultiplier,
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
    id, auto_enabled, scheduler_mode, sovereign_candidate_scope, sovereign_max_jobs_per_cycle, sovereign_min_expected_profit_sat, sovereign_low_success_min_rate, sovereign_low_success_min_profit_cost_ratio, sovereign_budget_efficiency_min_ratio, sovereign_route_dead_source_share, sovereign_risk_score_floor, scan_interval_sec, deadband_pct, source_min_local_pct, econ_ratio, econ_ratio_max_ppm, fee_limit_ppm, lost_profit, fail_tolerance_ppm, roi_min, daily_budget_pct, budget_mode, budget_unlimited, budget_auto_only, manual_reserve_enabled, manual_reserve_mode, manual_reserve_value,
    max_concurrent, min_amount_sat, max_amount_sat, min_split_enabled, min_probe_sat, min_execute_sat, mpp_enabled, mpp_max_shards, mpp_parallelism, mpp_min_shard_sat, mpp_round_timeout_sec, mpp_auto_only,
    fee_ladder_steps, amount_probe_steps, amount_probe_adaptive, attempt_timeout_sec, rebalance_timeout_sec, manual_restart_watch, cooldown_probe_enabled, mc_half_life_sec, payback_mode_flags, fresh_paid_liquidity_lock_enabled, fresh_paid_liquidity_lock_hours,
    unlock_days, critical_release_pct, critical_min_sources, critical_min_available_sats, critical_cycles, rebalance_cost_floor_ppm, source_min_payback_progress, mission_control_reinforce, gain_model_version, velocity_weight, autofee_settling_window_sec, autofee_settling_multiplier, delegated_fast_path_enabled, delegated_fast_path_strict_payback,
    sovereign_attribution_window_hours, sovereign_slow_seller_window_hours, sovereign_target_source_quarantine_hours, sovereign_structural_cooldown_repeat_hours, sovereign_exploration_slot_pct, sovereign_source_opportunity_cost_enabled, sovereign_slow_seller_enabled, sovereign_gain_v3_cold_start_pct, fast_path_max_timeout_sec, sovereign_top_bucket_pct, manual_restart_ignore_economic_gates, rebalance_profile,
    auto_target_enabled, auto_target_max_pct, auto_target_min_pct, auto_target_step_pct, auto_target_eval_interval_hours, auto_target_max_ups_per_cycle, auto_target_max_local_sat, auto_target_min_drain_rate_sat_per_hr, auto_target_min_revenue_7d_sat, auto_target_up_success_threshold, auto_target_down_success_threshold, auto_target_drain_first_multiplier,
    updated_at
  ) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43,$44,$45,$46,$47,$48,$49,$50,$51,$52,$53,$54,$55,$56,$57,$58,$59,$60,$61,$62,$63,$64,$65,$66,$67,$68,$69,$70,$71,$72,$73,$74,$75,$76,$77,$78,$79,$80,$81,$82,$83,$84,$85,$86,$87,$88,now())
   on conflict (id) do update set
    auto_enabled = excluded.auto_enabled,
    scheduler_mode = excluded.scheduler_mode,
    sovereign_candidate_scope = excluded.sovereign_candidate_scope,
    sovereign_max_jobs_per_cycle = excluded.sovereign_max_jobs_per_cycle,
    sovereign_min_expected_profit_sat = excluded.sovereign_min_expected_profit_sat,
    sovereign_low_success_min_rate = excluded.sovereign_low_success_min_rate,
    sovereign_low_success_min_profit_cost_ratio = excluded.sovereign_low_success_min_profit_cost_ratio,
    sovereign_budget_efficiency_min_ratio = excluded.sovereign_budget_efficiency_min_ratio,
    sovereign_route_dead_source_share = excluded.sovereign_route_dead_source_share,
    sovereign_risk_score_floor = excluded.sovereign_risk_score_floor,
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
    budget_unlimited = excluded.budget_unlimited,
    budget_auto_only = excluded.budget_auto_only,
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
    cooldown_probe_enabled = excluded.cooldown_probe_enabled,
    mc_half_life_sec = excluded.mc_half_life_sec,
    payback_mode_flags = excluded.payback_mode_flags,
    fresh_paid_liquidity_lock_enabled = excluded.fresh_paid_liquidity_lock_enabled,
    fresh_paid_liquidity_lock_hours = excluded.fresh_paid_liquidity_lock_hours,
    unlock_days = excluded.unlock_days,
    critical_release_pct = excluded.critical_release_pct,
    critical_min_sources = excluded.critical_min_sources,
    critical_min_available_sats = excluded.critical_min_available_sats,
    critical_cycles = excluded.critical_cycles,
    rebalance_cost_floor_ppm = excluded.rebalance_cost_floor_ppm,
    source_min_payback_progress = excluded.source_min_payback_progress,
    mission_control_reinforce = excluded.mission_control_reinforce,
    gain_model_version = excluded.gain_model_version,
    velocity_weight = excluded.velocity_weight,
    autofee_settling_window_sec = excluded.autofee_settling_window_sec,
    autofee_settling_multiplier = excluded.autofee_settling_multiplier,
    delegated_fast_path_enabled = excluded.delegated_fast_path_enabled,
    delegated_fast_path_strict_payback = excluded.delegated_fast_path_strict_payback,
    sovereign_attribution_window_hours = excluded.sovereign_attribution_window_hours,
    sovereign_slow_seller_window_hours = excluded.sovereign_slow_seller_window_hours,
    sovereign_target_source_quarantine_hours = excluded.sovereign_target_source_quarantine_hours,
    sovereign_structural_cooldown_repeat_hours = excluded.sovereign_structural_cooldown_repeat_hours,
    sovereign_exploration_slot_pct = excluded.sovereign_exploration_slot_pct,
    sovereign_source_opportunity_cost_enabled = excluded.sovereign_source_opportunity_cost_enabled,
    sovereign_slow_seller_enabled = excluded.sovereign_slow_seller_enabled,
    sovereign_gain_v3_cold_start_pct = excluded.sovereign_gain_v3_cold_start_pct,
    fast_path_max_timeout_sec = excluded.fast_path_max_timeout_sec,
    sovereign_top_bucket_pct = excluded.sovereign_top_bucket_pct,
    manual_restart_ignore_economic_gates = excluded.manual_restart_ignore_economic_gates,
    rebalance_profile = excluded.rebalance_profile,
    auto_target_enabled = excluded.auto_target_enabled,
    auto_target_max_pct = excluded.auto_target_max_pct,
    auto_target_min_pct = excluded.auto_target_min_pct,
    auto_target_step_pct = excluded.auto_target_step_pct,
    auto_target_eval_interval_hours = excluded.auto_target_eval_interval_hours,
    auto_target_max_ups_per_cycle = excluded.auto_target_max_ups_per_cycle,
    auto_target_max_local_sat = excluded.auto_target_max_local_sat,
    auto_target_min_drain_rate_sat_per_hr = excluded.auto_target_min_drain_rate_sat_per_hr,
    auto_target_min_revenue_7d_sat = excluded.auto_target_min_revenue_7d_sat,
    auto_target_up_success_threshold = excluded.auto_target_up_success_threshold,
    auto_target_down_success_threshold = excluded.auto_target_down_success_threshold,
    auto_target_drain_first_multiplier = excluded.auto_target_drain_first_multiplier,
    updated_at = now()
  `, rebalanceConfigID, cfg.AutoEnabled, cfg.SchedulerMode, cfg.SovereignCandidateScope, cfg.SovereignMaxJobsPerCycle, cfg.SovereignMinExpectedProfitSat, cfg.SovereignLowSuccessMinRate, cfg.SovereignLowSuccessMinProfitCostRatio, cfg.SovereignBudgetEfficiencyMinRatio, cfg.SovereignRouteDeadSourceShare, cfg.SovereignRiskScoreFloor, cfg.ScanIntervalSec, cfg.DeadbandPct, cfg.SourceMinLocalPct, cfg.EconRatio, cfg.EconRatioMaxPpm, cfg.FeeLimitPpm, cfg.LostProfit, cfg.FailTolerancePpm, cfg.ROIMin, cfg.DailyBudgetPct, cfg.BudgetMode, cfg.BudgetUnlimited, cfg.BudgetAutoOnly, cfg.ManualReserveEnabled, cfg.ManualReserveMode, cfg.ManualReserveValue, cfg.MaxConcurrent,
		cfg.MinAmountSat, cfg.MaxAmountSat, cfg.MinSplitEnabled, cfg.MinProbeSat, cfg.MinExecuteSat, cfg.MppEnabled, cfg.MppMaxShards, cfg.MppParallelism, cfg.MppMinShardSat, cfg.MppRoundTimeoutSec, cfg.MppAutoOnly, cfg.FeeLadderSteps, cfg.AmountProbeSteps, cfg.AmountProbeAdaptive, cfg.AttemptTimeoutSec, cfg.RebalanceTimeoutSec, cfg.ManualRestartWatch, cfg.CooldownProbeEnabled, cfg.MissionControlHalfLifeSec, cfg.PaybackModeFlags, cfg.FreshPaidLiquidityLockEnabled, cfg.FreshPaidLiquidityLockHours, cfg.UnlockDays, cfg.CriticalReleasePct, cfg.CriticalMinSources, cfg.CriticalMinAvailableSats, cfg.CriticalCycles, cfg.RebalanceCostFloorPpm, cfg.SourceMinPaybackProgress, cfg.MissionControlReinforce, cfg.GainModelVersion, cfg.VelocityWeight, cfg.AutofeeSettlingWindowSec, cfg.AutofeeSettlingMultiplier, cfg.DelegatedFastPathEnabled, cfg.DelegatedFastPathStrictPayback, cfg.SovereignAttributionWindowHours, cfg.SovereignSlowSellerWindowHours, cfg.SovereignTargetSourceQuarantineHours, cfg.SovereignStructuralCooldownRepeatHours, cfg.SovereignExplorationSlotPct, cfg.SovereignSourceOpportunityCostEnabled, cfg.SovereignSlowSellerEnabled, cfg.SovereignGainV3ColdStartPct, cfg.FastPathMaxTimeoutSec, cfg.SovereignTopBucketPct, cfg.ManualRestartIgnoreEconomicGates, cfg.Profile,
		cfg.AutoTargetEnabled, cfg.AutoTargetMaxPct, cfg.AutoTargetMinPct, cfg.AutoTargetStepPct, cfg.AutoTargetEvalIntervalHours, cfg.AutoTargetMaxUpsPerCycle, cfg.AutoTargetMaxLocalSat, cfg.AutoTargetMinDrainRateSatPerHr, cfg.AutoTargetMinRevenue7dSat, cfg.AutoTargetUpSuccessThreshold, cfg.AutoTargetDownSuccessThreshold, cfg.AutoTargetDrainFirstMultiplier,
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *RebalanceService) loadChannelSettings(ctx context.Context) (map[uint64]channelSetting, error) {
	settings := map[uint64]channelSetting{}
	if s.db == nil {
		return settings, nil
	}
	rows, err := s.db.Query(ctx, `
select channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, econ_ratio_override, auto_bypass_cost_gate, auto_target_managed from rebalance_channel_settings
`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var setting channelSetting
		var econRatioOverride pgtype.Float8
		if err := rows.Scan(&channelID, &setting.ChannelPoint, &setting.TargetOutboundPct, &setting.AutoEnabled, &setting.ManualRestartEnabled, &setting.UseDefaultEconRatio, &econRatioOverride, &setting.AutoBypassCostGate, &setting.AutoTargetManaged); err != nil {
			return settings, err
		}
		if econRatioOverride.Valid {
			setting.EconRatioOverride = econRatioOverride.Float64
			setting.EconRatioOverrideSet = true
		}
		setting.ChannelID = uint64(channelID)
		settings[setting.ChannelID] = normalizeChannelSetting(setting)
	}
	if err := rows.Err(); err != nil {
		return settings, err
	}
	policies, err := loadChannelAutomationPolicies(ctx, s.db)
	if err != nil {
		return settings, err
	}
	for channelID, policy := range policies {
		setting := settings[channelID]
		setting.ChannelID = channelID
		if strings.TrimSpace(setting.ChannelPoint) == "" {
			setting.ChannelPoint = policy.ChannelPoint
		}
		setting.AutomationMode = policy.Mode
		setting.FixedFeePPM = policy.FixedFeePPM
		setting.AutomationNote = policy.Note
		if policy.ReviewAt != nil {
			setting.ReviewAt = policy.ReviewAt.UTC().Format(time.RFC3339)
		}
		if policy.ParkedAt != nil {
			setting.ParkedAt = policy.ParkedAt.UTC().Format(time.RFC3339)
		}
		settings[channelID] = normalizeChannelSetting(setting)
	}
	return settings, nil
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
	if err := rows.Err(); err != nil {
		return excluded, err
	}
	policies, err := loadChannelAutomationPolicies(ctx, s.db)
	if err != nil {
		return excluded, err
	}
	for channelID, policy := range policies {
		if isChannelAutomationParked(policy.Mode) {
			excluded[channelID] = true
		}
	}
	return excluded, nil
}

func newFreshPaidLiquidityTracker(ttl time.Duration) *freshPaidLiquidityTracker {
	if ttl <= 0 {
		ttl = time.Duration(freshPaidLiquidityLockDefaultHours) * time.Hour
	}
	return &freshPaidLiquidityTracker{
		ttl:  ttl,
		lots: map[uint64][]freshPaidLiquidityLot{},
	}
}

func (t *freshPaidLiquidityTracker) expire(channelID uint64, at time.Time) {
	if t == nil || t.ttl <= 0 {
		return
	}
	lots := t.lots[channelID]
	if len(lots) == 0 {
		return
	}
	cutoff := at.Add(-t.ttl)
	keepFrom := 0
	for keepFrom < len(lots) && !lots[keepFrom].OccurredAt.After(cutoff) {
		keepFrom++
	}
	if keepFrom == 0 {
		return
	}
	if keepFrom >= len(lots) {
		delete(t.lots, channelID)
		return
	}
	t.lots[channelID] = append([]freshPaidLiquidityLot(nil), lots[keepFrom:]...)
}

func (t *freshPaidLiquidityTracker) add(channelID uint64, amountSat int64, at time.Time) {
	if t == nil || channelID == 0 || amountSat <= 0 {
		return
	}
	t.expire(channelID, at)
	t.lots[channelID] = append(t.lots[channelID], freshPaidLiquidityLot{
		AmountSat:  amountSat,
		OccurredAt: at,
	})
}

func (t *freshPaidLiquidityTracker) consume(channelID uint64, amountSat int64, at time.Time) {
	if t == nil || channelID == 0 || amountSat <= 0 {
		return
	}
	t.expire(channelID, at)
	lots := t.lots[channelID]
	for amountSat > 0 && len(lots) > 0 {
		if lots[0].AmountSat > amountSat {
			lots[0].AmountSat -= amountSat
			amountSat = 0
			break
		}
		amountSat -= lots[0].AmountSat
		lots = lots[1:]
	}
	if len(lots) == 0 {
		delete(t.lots, channelID)
		return
	}
	t.lots[channelID] = lots
}

func (t *freshPaidLiquidityTracker) total(channelID uint64, at time.Time) int64 {
	if t == nil || channelID == 0 {
		return 0
	}
	t.expire(channelID, at)
	total := int64(0)
	for _, lot := range t.lots[channelID] {
		if lot.AmountSat > 0 {
			total += lot.AmountSat
		}
	}
	return total
}

func (s *RebalanceService) loadLedger(ctx context.Context, cfg RebalanceConfig) (map[uint64]*channelLedger, error) {
	ledger := map[uint64]*channelLedger{}
	if s.db == nil {
		return ledger, nil
	}
	freshTracker := newFreshPaidLiquidityTracker(freshPaidLiquidityLockDuration(cfg))
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
				freshTracker.add(channelID, amountSat, occurredAt)
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
			freshTracker.consume(channelID, amountSat, occurredAt)
		}
		if feeMsat > 0 {
			entry.PaidRevenueSat += feeMsat / 1000
		}
		entry.LastForwardAt = occurredAt
	}
	if err := rows.Err(); err != nil {
		return ledger, err
	}
	now := time.Now().UTC()
	for channelID, entry := range ledger {
		entry.FreshPaidLiquiditySat = freshTracker.total(channelID, now)
		if entry.FreshPaidLiquiditySat > entry.PaidLiquiditySat {
			entry.FreshPaidLiquiditySat = entry.PaidLiquiditySat
		}
	}
	return ledger, nil
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

// fetchChannelDrainRate24h returns sats/hour forwarded OUT through each channel
// over the last 24 hours, computed from LND's forwarding_history. Used by the
// scan/job snapshot to inform demand-driven scoring (Wave 3.1). Cached for
// drainRateCacheTTL since paginating ForwardingHistory is expensive enough
// that we don't want to redo it on every scan.
// fetchRecentAutofeeAdjustments returns the most recent AutoFee outbound-fee
// change timestamp per channel, restricted to changes within the window. Used
// by Wave 6.1b to dampen rebalance scores for targets in autofee settling.
// Returns an empty map if the window is non-positive, the table is missing,
// or the db handle is unavailable.
func (s *RebalanceService) fetchRecentAutofeeAdjustments(ctx context.Context, scanAt time.Time, window time.Duration) map[uint64]time.Time {
	out := map[uint64]time.Time{}
	if s.db == nil || window <= 0 {
		return out
	}
	if scanAt.IsZero() {
		scanAt = time.Now()
	}
	cutoff := scanAt.Add(-window)
	rows, err := s.db.Query(ctx, `
select channel_id, max(decided_at)
from autofee_outcomes
where kind = 'outbound'
  and decided_at >= $1
group by channel_id
`, cutoff.UTC())
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance autofee adjustments fetch failed: %v", err)
		}
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var channelID int64
		var decidedAt time.Time
		if err := rows.Scan(&channelID, &decidedAt); err != nil {
			continue
		}
		if channelID > 0 {
			out[uint64(channelID)] = decidedAt
		}
	}
	return out
}

func (s *RebalanceService) fetchChannelDrainRate24h(ctx context.Context) map[uint64]int64 {
	s.mu.Lock()
	if s.drainRateCache != nil && !s.drainRateCacheAt.IsZero() && time.Since(s.drainRateCacheAt) < drainRateCacheTTL {
		cached := make(map[uint64]int64, len(s.drainRateCache))
		for k, v := range s.drainRateCache {
			cached[k] = v
		}
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	result := map[uint64]int64{}
	if s.lnd == nil {
		return result
	}
	conn, release, err := s.lnd.BorrowLightning(ctx, false)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance drain rate: dial failed: %v", err)
		}
		return result
	}
	defer release()

	client := lnrpc.NewLightningClient(conn)
	end := time.Now()
	start := end.Add(-24 * time.Hour)

	amtSatByChan := map[uint64]int64{}
	var offset uint32
	for {
		resp, err := client.ForwardingHistory(ctx, &lnrpc.ForwardingHistoryRequest{
			StartTime:    uint64(start.Unix()),
			EndTime:      uint64(end.Unix()),
			IndexOffset:  offset,
			NumMaxEvents: rebalanceForwardPageSize,
		})
		if err != nil {
			if s.logger != nil {
				s.logger.Printf("rebalance drain rate: forwarding_history failed at offset=%d: %v", offset, err)
			}
			return result
		}
		if resp == nil || len(resp.ForwardingEvents) == 0 {
			break
		}
		for _, evt := range resp.ForwardingEvents {
			if evt == nil || evt.ChanIdOut == 0 {
				continue
			}
			amt := int64(evt.AmtOut)
			if amt == 0 && evt.AmtOutMsat > 0 {
				amt = int64(evt.AmtOutMsat / 1000)
			}
			if amt > 0 {
				amtSatByChan[evt.ChanIdOut] += amt
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

	for chanID, amtSat := range amtSatByChan {
		result[chanID] = amtSat / 24
	}

	s.mu.Lock()
	cached := make(map[uint64]int64, len(result))
	for k, v := range result {
		cached[k] = v
	}
	s.drainRateCache = cached
	s.drainRateCacheAt = time.Now()
	s.mu.Unlock()

	return result
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

func computeRemainingTotalBudget(totalBudgetSat int64, spentTotalSat int64) int64 {
	remainingTotal := totalBudgetSat - spentTotalSat
	if remainingTotal < 0 {
		remainingTotal = 0
	}
	return remainingTotal
}

func computeManualReserveRemaining(manualReserveSat int64, spentManualSat int64) int64 {
	manualReserveRemaining := manualReserveSat - spentManualSat
	if manualReserveRemaining < 0 {
		manualReserveRemaining = 0
	}
	return manualReserveRemaining
}

func computeRemainingForAuto(totalBudgetSat int64, spentAutoSat int64, spentTotalSat int64, manualReserveSat int64, budgetAutoOnly bool) int64 {
	autoBudgetCap := totalBudgetSat - manualReserveSat
	if autoBudgetCap < 0 {
		autoBudgetCap = 0
	}
	remainingForAuto := autoBudgetCap - spentAutoSat
	if remainingForAuto < 0 {
		remainingForAuto = 0
	}
	if !budgetAutoOnly {
		remainingTotal := computeRemainingTotalBudget(totalBudgetSat, spentTotalSat)
		if remainingTotal < remainingForAuto {
			remainingForAuto = remainingTotal
		}
	}
	return remainingForAuto
}

func shouldEnforceAutoBudget(cfg RebalanceConfig) bool {
	return !cfg.BudgetUnlimited
}

func shouldEnforceManualRestartBudget(cfg RebalanceConfig, source string, reason string, manualAutoRestart bool) bool {
	if cfg.BudgetUnlimited {
		return false
	}
	if cfg.BudgetAutoOnly {
		return false
	}
	return isManualRestartJob(source, reason, manualAutoRestart)
}

func checkManualBudgetAllowance(cfg RebalanceConfig, setting channelSetting, target lndclient.ChannelInfo, amountSat int64, totalBudgetSat int64, _ int64, spentTotalSat int64) error {
	if amountSat <= 0 {
		return nil
	}
	remainingTotal := computeRemainingTotalBudget(totalBudgetSat, spentTotalSat)
	if remainingTotal <= 0 {
		return errManualBudgetExhausted
	}
	feeCfg := effectiveConfigForTarget(cfg, setting)
	targetPolicy := lndclient.ChannelPolicySnapshot{
		FeeRatePpm:  int64Value(target.FeeRatePpm),
		BaseFeeMsat: int64Value(target.BaseFeeMsat),
	}
	// A single job fills at most one max_amount chunk — runJob caps the spend at
	// MaxAmountSat — so the budget only needs to cover that chunk, not the whole
	// deficit. Checking the full deficit wrongly blocked large-deficit channels
	// (e.g. a 1.1M top-up) when a 300k chunk would comfortably fit the remaining
	// budget. The deficit is filled across successive runs.
	chunkSat := amountSat
	if cfg.MaxAmountSat > 0 && chunkSat > cfg.MaxAmountSat {
		chunkSat = cfg.MaxAmountSat
	}
	estimatedCost := estimateMaxCost(chunkSat, targetPolicy, feeCfg)
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
	conn, release, err := s.lnd.BorrowLightning(ctx, false)
	if err != nil {
		return 0, err
	}
	defer release()

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
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
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
	var attempts int64
	var failedAttempts int64
	var successAttempts int64
	var successAmountSat int64
	var belowAttempts int64
	var belowAmountSat int64
	err := s.db.QueryRow(ctx, `
select
  count(*) as attempts,
  coalesce(sum(case when status<>'succeeded' then 1 else 0 end), 0) as failed_attempts,
  coalesce(sum(case when status='succeeded' then 1 else 0 end), 0) as success_attempts,
  coalesce(sum(case when status='succeeded' then amount_sat else 0 end), 0) as success_amount_sat,
  coalesce(sum(case when status='succeeded' and $3 > 0 and amount_sat < $3 then 1 else 0 end), 0) as success_below_min_attempts,
  coalesce(sum(case when status='succeeded' and $3 > 0 and amount_sat < $3 then amount_sat else 0 end), 0) as success_below_min_amount_sat
from rebalance_attempts
where coalesce(finished_at, started_at) >= $1
  and coalesce(finished_at, started_at) <= $2
`, start, end, minAmountSat).Scan(&attempts, &failedAttempts, &successAttempts, &successAmountSat, &belowAttempts, &belowAmountSat)
	if err != nil {
		return metrics, err
	}
	metrics.Attempts = attempts
	metrics.FailedAttempts = failedAttempts
	metrics.SuccessAttempts = successAttempts
	metrics.SuccessAmountSat = successAmountSat
	metrics.SuccessBelowMinAttempts = belowAttempts
	metrics.SuccessBelowMinAmountSat = belowAmountSat
	if attempts > 0 {
		metrics.AttemptSuccessRate = float64(successAttempts) / float64(attempts)
		metrics.SuccessSatsPerAttempt = float64(successAmountSat) / float64(attempts)
	}
	if successAttempts > 0 {
		metrics.SuccessAvgAmountSat = successAmountSat / successAttempts
		metrics.AttemptsPerSuccess = float64(attempts) / float64(successAttempts)
		metrics.SuccessBelowMinRate = float64(belowAttempts) / float64(successAttempts)
	}
	return metrics, nil
}

// fetchJobTelemetry24h returns the per-JOB success rate over the last 24h: the
// fraction of executed jobs (status succeeded/partial/failed) that moved
// liquidity (succeeded/partial = at least one settled attempt). This is the
// headline "did the rebalance work?" metric, distinct from the per-ATTEMPT
// route-hit rate in fetchAttemptTelemetry24h (one row per source/amount probe,
// so naturally low because every failed route try counts).
func (s *RebalanceService) fetchJobTelemetry24h(ctx context.Context) (jobs int64, successJobs int64, rate float64) {
	if s.db == nil {
		return 0, 0, 0
	}
	end := time.Now().In(time.Local)
	start := end.Add(-24 * time.Hour)
	if err := s.db.QueryRow(ctx, `
select
  count(*) filter (where status in ('succeeded','partial','failed')) as jobs,
  count(*) filter (where status in ('succeeded','partial')) as success_jobs
from rebalance_jobs
where completed_at >= $1 and completed_at <= $2
`, start, end).Scan(&jobs, &successJobs); err != nil {
		return 0, 0, 0
	}
	if jobs > 0 {
		rate = float64(successJobs) / float64(jobs)
	}
	return jobs, successJobs, rate
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

func (s *RebalanceService) fetchMppStructuralAbortJobs24h(ctx context.Context) int64 {
	if s.db == nil {
		return 0
	}
	var count int64
	_ = s.db.QueryRow(ctx, `
select count(*)
from rebalance_jobs
where completed_at >= now() - interval '24 hours'
  and status='failed'
  and reason='mpp structural failure'
`).Scan(&count)
	return count
}

// fastPathTelemetry24h consolida métricas observacionais do delegated fast-path
// nas últimas 24h. Calculado on-demand para o overview. Todos os contadores
// vêm de rebalance_jobs/rebalance_attempts — nenhuma migration necessária.
type fastPathTelemetry24h struct {
	Attempts      int64
	Successes     int64
	Failures      int64
	Fallthroughs  int64
	DurationP50Ms int64
	DurationP95Ms int64
	FailReasons   []RebalanceFastPathFailReason
}

// fetchFastPathTelemetry24h retorna telemetria do delegated fast-path nas
// últimas 24h. attempts conta jobs com fast_path_attempted=true; successes é
// o subconjunto que finalizou succeeded com reason 'delegated-fast-path';
// failures = attempts - successes; fallthroughs conta jobs onde o fast-path
// falhou E o legacy loop rodou (≥1 attempt com attempt_index>0). Duration
// p50/p95 vem das attempts succeeded com attempt_index=0. FailReasons agrupa
// fail_reason de attempts com attempt_index=0 status=failed em buckets
// normalizados (timeout, no_route, insufficient_balance, htlc_max_fee, other).
func (s *RebalanceService) fetchFastPathTelemetry24h(ctx context.Context) fastPathTelemetry24h {
	var out fastPathTelemetry24h
	if s.db == nil {
		return out
	}
	_ = s.db.QueryRow(ctx, `
select
  coalesce(sum(case when fast_path_attempted then 1 else 0 end), 0) as attempts,
  coalesce(sum(case when fast_path_attempted and status='succeeded' and reason='delegated-fast-path' then 1 else 0 end), 0) as successes
from rebalance_jobs
where completed_at >= now() - interval '24 hours'
`).Scan(&out.Attempts, &out.Successes)
	if out.Attempts > out.Successes {
		out.Failures = out.Attempts - out.Successes
	}

	// Fallthroughs: jobs com fast-path attempt index=0 failed AND attempts > 0
	// no legacy loop. Indica que o fast-path não economizou tempo nesses jobs.
	_ = s.db.QueryRow(ctx, `
select count(distinct a1.job_id)
from rebalance_attempts a1
join rebalance_jobs j on j.id = a1.job_id and j.fast_path_attempted = true
where a1.attempt_index = 0 and a1.status = 'failed'
  and coalesce(a1.finished_at, a1.started_at) >= now() - interval '24 hours'
  and exists (
    select 1 from rebalance_attempts a2
    where a2.job_id = a1.job_id and a2.attempt_index > 0
  )
`).Scan(&out.Fallthroughs)

	// Duration p50/p95 (ms) das fast-path attempts succeeded.
	var p50, p95 float64
	_ = s.db.QueryRow(ctx, `
select
  coalesce(percentile_cont(0.5) within group (order by extract(epoch from (finished_at - started_at)) * 1000), 0) as p50_ms,
  coalesce(percentile_cont(0.95) within group (order by extract(epoch from (finished_at - started_at)) * 1000), 0) as p95_ms
from rebalance_attempts
where attempt_index = 0 and status = 'succeeded'
  and started_at is not null and finished_at is not null
  and finished_at >= now() - interval '24 hours'
`).Scan(&p50, &p95)
	out.DurationP50Ms = int64(p50)
	out.DurationP95Ms = int64(p95)

	// Fail reasons agrupados por categoria normalizada.
	rows, err := s.db.Query(ctx, `
select coalesce(fail_reason, ''), count(*)
from rebalance_attempts
where attempt_index = 0 and status = 'failed'
  and coalesce(finished_at, started_at) >= now() - interval '24 hours'
group by coalesce(fail_reason, '')
`)
	if err == nil {
		defer rows.Close()
		counts := map[string]int64{}
		for rows.Next() {
			var reason string
			var count int64
			if err := rows.Scan(&reason, &count); err != nil {
				break
			}
			counts[categorizeFastPathFailReason(reason)] += count
		}
		out.FailReasons = make([]RebalanceFastPathFailReason, 0, len(counts))
		for cat, count := range counts {
			out.FailReasons = append(out.FailReasons, RebalanceFastPathFailReason{Category: cat, Count: count})
		}
		sort.Slice(out.FailReasons, func(i, j int) bool {
			if out.FailReasons[i].Count != out.FailReasons[j].Count {
				return out.FailReasons[i].Count > out.FailReasons[j].Count
			}
			return out.FailReasons[i].Category < out.FailReasons[j].Category
		})
	}

	return out
}

// categorizeFastPathFailReason agrupa fail_reason cru em buckets úteis pra
// operação. Detecta os modos comuns do LND nativo no fast-path: deadline
// exceeded (timeout LND), no_route (pathfinding falhou), insufficient_balance,
// htlc fee/max (rota encontrada mas custo acima do cap), invoice/payment hash
// inválido. Tudo o que não se encaixa cai em "other".
func categorizeFastPathFailReason(reason string) string {
	if reason == "" {
		return "unknown"
	}
	r := strings.ToLower(reason)
	switch {
	case strings.Contains(r, "deadline exceeded") || strings.Contains(r, "context deadline") || strings.Contains(r, "timeout"):
		return "timeout"
	case strings.Contains(r, "no_route") || strings.Contains(r, "no route") || strings.Contains(r, "unable to find a path"):
		return "no_route"
	case strings.Contains(r, "insufficient_balance") || strings.Contains(r, "insufficient balance"):
		return "insufficient_balance"
	case strings.Contains(r, "htlc_max_fee") || strings.Contains(r, "fee insufficient") || strings.Contains(r, "fee_insufficient"):
		return "fee_cap"
	case strings.Contains(r, "incorrect_payment_details") || strings.Contains(r, "invoice"):
		return "invoice_issue"
	case strings.Contains(r, "lnd unavailable") || strings.Contains(r, "rpc error"):
		return "rpc_error"
	}
	return "other"
}

func (s *RebalanceService) fetchFailureTelemetry30m(ctx context.Context) ([]RebalanceReasonStat, []RebalanceTargetStat) {
	if s.db == nil {
		return nil, nil
	}

	reasonCounts := map[string]int64{}
	rows, err := s.db.Query(ctx, `
select coalesce(fail_reason, ''), count(*)
from rebalance_attempts
where coalesce(finished_at, started_at) >= now() - interval '30 minutes'
  and status <> 'succeeded'
group by coalesce(fail_reason, '')
`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var reason string
			var count int64
			if err := rows.Scan(&reason, &count); err != nil {
				break
			}
			normalized := normalizedPairFailReason(reason)
			if normalized == "" {
				normalized = "unknown"
			}
			reasonCounts[normalized] += count
		}
	}

	reasons := make([]RebalanceReasonStat, 0, len(reasonCounts))
	for reason, count := range reasonCounts {
		reasons = append(reasons, RebalanceReasonStat{Reason: reason, Count: count})
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].Count != reasons[j].Count {
			return reasons[i].Count > reasons[j].Count
		}
		return reasons[i].Reason < reasons[j].Reason
	})
	if len(reasons) > 10 {
		reasons = reasons[:10]
	}

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

	targetRows, err := s.db.Query(ctx, `
with events as (
  select
    j.target_channel_id,
    a.source_channel_id,
    a.status,
    a.fail_reason,
    coalesce(a.finished_at, a.started_at) as occurred_at
  from rebalance_attempts a
  join rebalance_jobs j on j.id = a.job_id
  where coalesce(a.finished_at, a.started_at) >= now() - interval '30 minutes'
),
last_success as (
  select target_channel_id, max(occurred_at) as last_success_at
  from events
  where status='succeeded'
  group by target_channel_id
),
pressure as (
  select e.*
  from events e
  left join last_success ls using (target_channel_id)
  where (ls.last_success_at is null or e.occurred_at > ls.last_success_at)
    and e.status <> 'succeeded'
),
structural_failures as (
  select *
  from pressure
  where (
      lower(coalesce(fail_reason, '')) like '%unable to find a path%'
      or lower(coalesce(fail_reason, '')) like '%no matching outgoing channel%'
      or lower(coalesce(fail_reason, '')) like '%probe returned no amount%'
      or lower(coalesce(fail_reason, '')) like '%attempt timeout%'
      or lower(coalesce(fail_reason, '')) like '%deadlineexceeded%'
      or lower(coalesce(fail_reason, '')) like '%deadline exceeded%'
    )
)
select target_channel_id,
  count(distinct source_channel_id) as failed_sources,
  count(*) as failure_attempts,
  max(occurred_at) as last_failure_at
from structural_failures
group by target_channel_id
order by failed_sources desc, failure_attempts desc, last_failure_at desc
limit 10
`)
	if err != nil {
		return reasons, nil
	}
	defer targetRows.Close()
	targets := []RebalanceTargetStat{}
	for targetRows.Next() {
		var channelID int64
		var failedSources int64
		var failureAttempts int64
		var lastFailure pgtype.Timestamptz
		if err := targetRows.Scan(&channelID, &failedSources, &failureAttempts, &lastFailure); err != nil {
			return reasons, targets
		}
		stat := RebalanceTargetStat{
			ChannelID:       uint64(channelID),
			PeerAlias:       aliasMap[uint64(channelID)],
			FailedSources:   failedSources,
			FailureAttempts: failureAttempts,
			Reason:          "structural_failures",
		}
		if lastFailure.Valid {
			stat.LastFailureAt = lastFailure.Time.UTC().Format(time.RFC3339)
		}
		targets = append(targets, stat)
	}
	return reasons, targets
}

func (s *RebalanceService) insertJob(ctx context.Context, target *lndclient.ChannelInfo, source string, reason string, targetPct float64, amount int64, economics rebalanceJobEconomics) (int64, error) {
	if s.db == nil {
		return 0, errors.New("db unavailable")
	}
	var jobID int64
	err := s.db.QueryRow(ctx, `
insert into rebalance_jobs (
  source, status, trigger_reason, reason, target_channel_id, target_channel_point, target_outbound_pct, target_amount_sat, config_snapshot,
  sovereign_expected_gain_sat, sovereign_estimated_cost_sat, sovereign_expected_profit_sat, sovereign_budget_cost_sat, sovereign_score
) values ($1,'queued',$2,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
 returning id
`, source, nullableString(reason), int64(target.ChannelID), target.ChannelPoint, targetPct, amount, nil,
		economics.ExpectedGainSat, economics.EstimatedCostSat, economics.ExpectedProfitSat, economics.BudgetCostSat, economics.Score).Scan(&jobID)
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

// rebalanceHealthSignals30d holds the 30-day trailing averages of the three
// health-signal gauges that the UI can toggle to (effectiveness, fast-path hit,
// payback rebalanced). Sell-through is intentionally excluded — it keeps its
// fixed 72h/168h attribution windows. Cached because the 30d aggregations are
// heavy and the baseline drifts slowly.
type rebalanceHealthSignals30d struct {
	Effectiveness        float64
	FastPathHitRate      float64
	PaybackProgressRebal float64
	ComputedAt           time.Time
	Valid                bool
}

const rebalanceHealthSignals30dTTL = time.Hour

// healthSignals30d returns the cached 30d baseline, kicking off a non-blocking
// background recompute when the cache is empty or older than the TTL. The
// overview never blocks on the heavy 30d scan: the first call returns an empty
// (Valid=false) value and the cache populates on the next refresh.
func (s *RebalanceService) healthSignals30d() rebalanceHealthSignals30d {
	s.mu.Lock()
	cached := s.healthSignals30dCache
	stale := !cached.Valid || time.Since(cached.ComputedAt) >= rebalanceHealthSignals30dTTL
	if stale && !s.healthSignals30dRefreshing && s.db != nil {
		s.healthSignals30dRefreshing = true
		go s.refreshHealthSignals30d()
	}
	s.mu.Unlock()
	return cached
}

func (s *RebalanceService) refreshHealthSignals30d() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := s.computeHealthSignals30d(ctx)
	s.mu.Lock()
	if out.Valid {
		s.healthSignals30dCache = out
	}
	s.healthSignals30dRefreshing = false
	s.mu.Unlock()
}

// computeHealthSignals30d runs the three 30d-window aggregations. Mirrors the
// 7d/24h queries used for the live gauges (effectiveness ~13600, fast-path
// fetchFastPathTelemetry24h, payback fetchPaybackTotals7d) over a 30d window.
func (s *RebalanceService) computeHealthSignals30d(ctx context.Context) rebalanceHealthSignals30d {
	out := rebalanceHealthSignals30d{ComputedAt: time.Now()}
	if s.db == nil {
		return out
	}
	// Effectiveness 30d = jobs succeeded/partial ÷ jobs that had an attempt.
	var successCount, attemptedCount int64
	if err := s.db.QueryRow(ctx, `
with jobs_30d as (
  select id, status from rebalance_jobs where completed_at >= now() - interval '30 days'
),
jobs_with_attempts as (
  select j.status, exists (select 1 from rebalance_attempts a where a.job_id = j.id) as has_attempt
  from jobs_30d j
)
select
  coalesce(sum(case when status in ('succeeded','partial') then 1 else 0 end), 0),
  coalesce(sum(case when has_attempt then 1 else 0 end), 0)
from jobs_with_attempts
`).Scan(&successCount, &attemptedCount); err != nil {
		return out
	}
	if attemptedCount > 0 {
		out.Effectiveness = float64(successCount) / float64(attemptedCount)
	}
	// Fast-path hit 30d = delegated fast-path successes ÷ attempts.
	var fpAttempts, fpSuccesses int64
	_ = s.db.QueryRow(ctx, `
select
  coalesce(sum(case when fast_path_attempted then 1 else 0 end), 0),
  coalesce(sum(case when fast_path_attempted and status='succeeded' and reason='delegated-fast-path' then 1 else 0 end), 0)
from rebalance_jobs
where completed_at >= now() - interval '30 days'
`).Scan(&fpAttempts, &fpSuccesses)
	if fpAttempts > 0 {
		out.FastPathHitRate = float64(fpSuccesses) / float64(fpAttempts)
	}
	// Payback rebalanced 30d = forward fees on rebalanced channels ÷ rebalance cost.
	var revenueRebalancedMsat, costMsat int64
	_ = s.db.QueryRow(ctx, `
with rebalance_channels as (
  select distinct coalesce(rebal_target_chan_id, channel_id) as channel_id
  from notifications
  where type='rebalance' and status in ('SETTLED','SUCCEEDED')
    and occurred_at >= now() - interval '30 days'
    and coalesce(rebal_target_chan_id, channel_id) is not null
),
forward_fees as (
  select channel_id, case when fee_msat > 0 then fee_msat else fee_sat * 1000 end as fee_msat
  from notifications
  where type='forward' and occurred_at >= now() - interval '30 days' and channel_id is not null
)
select
  coalesce((select sum(f.fee_msat) from forward_fees f join rebalance_channels r on r.channel_id = f.channel_id), 0),
  coalesce((select sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end) from notifications where type='rebalance' and status in ('SETTLED','SUCCEEDED') and occurred_at >= now() - interval '30 days'), 0)
`).Scan(&revenueRebalancedMsat, &costMsat)
	if costMsat > 0 {
		out.PaybackProgressRebal = float64(revenueRebalancedMsat) / float64(costMsat)
	}
	out.Valid = true
	return out
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

func (s *RebalanceService) fetchSovereignAutopilotEconomics7d(ctx context.Context, cfg RebalanceConfig) (rebalanceAutopilotEconomics7d, error) {
	if s.db == nil {
		return rebalanceAutopilotEconomics7d{}, nil
	}
	var sentSat int64
	var costSat int64
	var forwardSat int64
	var forwardFeeMsat int64
	var forwardSlowSat int64
	var forwardSlowFeeMsat int64
	// Duas janelas, ambas vindas da config (UI): attribution_window mede a
	// venda "rápida" (velocidade) e slow_seller_window mede a realização
	// incluindo slow movers. O denominador (rebalance 7d) é o mesmo; muda só
	// a janela de atribuição do forward. Ver normalizeRebalanceConfig que
	// garante slow_seller >= attribution.
	attributionHours := sovereignAttributionWindowHoursForConfig(cfg)
	slowHours := sovereignSlowSellerWindowHoursForConfig(cfg)
	err := s.db.QueryRow(ctx, `
with jobs as (
  select id, target_channel_id, completed_at
  from rebalance_jobs
  where trigger_reason = $1
    and completed_at is not null
    and completed_at >= now() - interval '7 days'
),
attempt_totals as (
  select
    j.id,
    coalesce(sum(a.amount_sat) filter (where a.status='succeeded'), 0) as sent_sat,
    coalesce(sum(a.fee_paid_sat) filter (where a.status='succeeded'), 0) as fee_paid_sat
  from jobs j
  left join rebalance_attempts a on a.job_id = j.id
  group by j.id
),
forward_totals as (
  select
    j.id,
    coalesce(sum(n.amount_sat) filter (where n.occurred_at < j.completed_at + ($2::int * interval '1 hour')), 0) as forward_amount_sat,
    coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end) filter (where n.occurred_at < j.completed_at + ($2::int * interval '1 hour')), 0)::bigint as forward_fee_msat,
    coalesce(sum(n.amount_sat) filter (where n.occurred_at < j.completed_at + ($3::int * interval '1 hour')), 0) as forward_slow_amount_sat,
    coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end) filter (where n.occurred_at < j.completed_at + ($3::int * interval '1 hour')), 0)::bigint as forward_slow_fee_msat
  from jobs j
  left join notifications n on n.type='forward'
    and n.channel_id = j.target_channel_id
    and n.occurred_at >= j.completed_at
  group by j.id
),
job_raw as (
  select
    coalesce(a.sent_sat, 0) as sent_sat,
    coalesce(a.fee_paid_sat, 0) as fee_paid_sat,
    coalesce(f.forward_amount_sat, 0) as forward_amount_sat,
    coalesce(f.forward_fee_msat, 0) as forward_fee_msat,
    coalesce(f.forward_slow_amount_sat, 0) as forward_slow_amount_sat,
    coalesce(f.forward_slow_fee_msat, 0) as forward_slow_fee_msat
  from jobs j
  left join attempt_totals a on a.id = j.id
  left join forward_totals f on f.id = j.id
),
job_economics as (
  select
    sent_sat,
    fee_paid_sat,
    case
      when sent_sat <= 0 or forward_amount_sat <= 0 then 0
      when forward_amount_sat > sent_sat then sent_sat
      else forward_amount_sat
    end as attributed_forward_sat,
    case
      when sent_sat <= 0 or forward_amount_sat <= 0 or forward_fee_msat <= 0 then 0
      when forward_amount_sat > sent_sat then (forward_fee_msat * sent_sat) / forward_amount_sat
      else forward_fee_msat
    end as attributed_forward_fee_msat,
    case
      when sent_sat <= 0 or forward_slow_amount_sat <= 0 then 0
      when forward_slow_amount_sat > sent_sat then sent_sat
      else forward_slow_amount_sat
    end as attributed_forward_slow_sat,
    case
      when sent_sat <= 0 or forward_slow_amount_sat <= 0 or forward_slow_fee_msat <= 0 then 0
      when forward_slow_amount_sat > sent_sat then (forward_slow_fee_msat * sent_sat) / forward_slow_amount_sat
      else forward_slow_fee_msat
    end as attributed_forward_slow_fee_msat
  from job_raw
)
select
  coalesce(sum(sent_sat), 0) as sent_sat,
  coalesce(sum(fee_paid_sat), 0) as cost_sat,
  coalesce(sum(attributed_forward_sat), 0) as forward_sat,
  coalesce(sum(attributed_forward_fee_msat), 0)::bigint as forward_fee_msat,
  coalesce(sum(attributed_forward_slow_sat), 0) as forward_slow_sat,
  coalesce(sum(attributed_forward_slow_fee_msat), 0)::bigint as forward_slow_fee_msat
from job_economics
`, rebalanceSovereignReason, attributionHours, slowHours).Scan(&sentSat, &costSat, &forwardSat, &forwardFeeMsat, &forwardSlowSat, &forwardSlowFeeMsat)
	if err != nil {
		return rebalanceAutopilotEconomics7d{}, err
	}
	forwardFeeSat := forwardFeeMsat / 1000
	forwardSlowFeeSat := forwardSlowFeeMsat / 1000
	result := rebalanceAutopilotEconomics7d{
		RebalanceAmountSat:     sentSat,
		RebalanceCostSat:       costSat,
		RebalanceCostPpm:       satToPpm(costSat, sentSat),
		ForwardAmountSat:       forwardSat,
		ForwardFeeSat:          forwardFeeSat,
		ForwardFeePpm:          satToPpm(forwardFeeSat, forwardSat),
		RealizedNetSat:         forwardFeeSat - costSat,
		ForwardAmountSlowSat:   forwardSlowSat,
		ForwardFeeSlowSat:      forwardSlowFeeSat,
		ForwardFeeSlowPpm:      satToPpm(forwardSlowFeeSat, forwardSlowSat),
		RealizedNetSlowSat:     forwardSlowFeeSat - costSat,
		AttributionWindowHours: attributionHours,
		SlowSellerWindowHours:  slowHours,
	}
	if sentSat > 0 {
		result.SellThrough = float64(forwardSat) / float64(sentSat)
		result.SellThroughSlow = float64(forwardSlowSat) / float64(sentSat)
	}
	return result, nil
}

const (
	baselineMetricsDefaultDays = 30
	baselineMetricsMaxDays     = 365
)

func (s *RebalanceService) BaselineMetrics(ctx context.Context, days int) (BaselineMetrics, error) {
	if days <= 0 {
		days = baselineMetricsDefaultDays
	}
	if days > baselineMetricsMaxDays {
		days = baselineMetricsMaxDays
	}
	now := time.Now().UTC()
	to := now
	from := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(days - 1))
	result := BaselineMetrics{
		Period: BaselineMetricsPeriod{
			From: from.Format("2006-01-02"),
			To:   to.Format("2006-01-02"),
			Days: days,
		},
		Daily: []BaselineMetricsDaily{},
	}
	if s.db == nil {
		return result, nil
	}

	daily, err := s.queryBaselineDaily(ctx, from, to.Add(24*time.Hour))
	if err != nil {
		return result, err
	}
	result.Daily = daily

	for _, d := range daily {
		result.Aggregate.JobsTotal += d.JobsTotal
		result.Aggregate.JobsSucceeded += d.JobsSucceeded
		result.Aggregate.JobsPartial += d.JobsPartial
		result.Aggregate.JobsFailed += d.JobsFailed
		result.Aggregate.JobsCancelled += d.JobsCancelled
		result.Aggregate.AttemptsTotal += d.AttemptsTotal
		result.Aggregate.AttemptsSucceeded += d.AttemptsSucceeded
		result.Aggregate.FeePaidSatTotal += d.FeePaidSatTotal
		result.Aggregate.AmountSucceededSatTotal += d.AmountSucceededSatTotal
	}
	if result.Aggregate.JobsTotal > 0 {
		denom := float64(result.Aggregate.JobsTotal)
		result.Aggregate.SuccessRate = float64(result.Aggregate.JobsSucceeded) / denom
		result.Aggregate.PartialRate = float64(result.Aggregate.JobsPartial) / denom
		result.Aggregate.FailedRate = float64(result.Aggregate.JobsFailed) / denom
		result.Aggregate.AvgAttemptsPerJob = float64(result.Aggregate.AttemptsTotal) / denom
	}
	successfulJobs := result.Aggregate.JobsSucceeded + result.Aggregate.JobsPartial
	if successfulJobs > 0 {
		result.Aggregate.AvgSatsPerSuccessfulJob = float64(result.Aggregate.AmountSucceededSatTotal) / float64(successfulJobs)
	}
	if result.Aggregate.AmountSucceededSatTotal > 0 {
		result.Aggregate.AvgFeePpmPaid = float64(result.Aggregate.FeePaidSatTotal) * 1_000_000.0 / float64(result.Aggregate.AmountSucceededSatTotal)
	}

	if p50, ok := s.queryTimeToPaybackP50(ctx, from, to.Add(24*time.Hour)); ok {
		result.Aggregate.TimeToPaybackP50Hours = &p50
	}

	return result, nil
}

func (s *RebalanceService) queryBaselineDaily(ctx context.Context, from time.Time, to time.Time) ([]BaselineMetricsDaily, error) {
	rows, err := s.db.Query(ctx, `
with jobs_d as (
  select
    (j.completed_at at time zone 'UTC')::date as day,
    j.status,
    coalesce((select count(*) from rebalance_attempts a where a.job_id=j.id), 0) as attempts_total,
    coalesce((select count(*) from rebalance_attempts a where a.job_id=j.id and a.status='succeeded'), 0) as attempts_succeeded,
    coalesce((select sum(amount_sat) from rebalance_attempts a where a.job_id=j.id and a.status='succeeded'), 0) as amount_succeeded_sat,
    coalesce((select sum(fee_paid_sat) from rebalance_attempts a where a.job_id=j.id and a.status='succeeded'), 0) as fee_paid_sat
  from rebalance_jobs j
  where j.completed_at >= $1 and j.completed_at < $2
    and j.status in ('succeeded','partial','failed','cancelled')
)
select
  day,
  count(*) as jobs_total,
  count(*) filter (where status='succeeded') as jobs_succeeded,
  count(*) filter (where status='partial') as jobs_partial,
  count(*) filter (where status='failed') as jobs_failed,
  count(*) filter (where status='cancelled') as jobs_cancelled,
  coalesce(sum(attempts_total), 0)::bigint as attempts_total,
  coalesce(sum(attempts_succeeded), 0)::bigint as attempts_succeeded,
  coalesce(sum(amount_succeeded_sat), 0)::bigint as amount_succeeded_sat_total,
  coalesce(sum(fee_paid_sat), 0)::bigint as fee_paid_sat_total
from jobs_d
group by day
order by day
`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BaselineMetricsDaily, 0)
	for rows.Next() {
		var d BaselineMetricsDaily
		var day time.Time
		if err := rows.Scan(&day, &d.JobsTotal, &d.JobsSucceeded, &d.JobsPartial, &d.JobsFailed, &d.JobsCancelled,
			&d.AttemptsTotal, &d.AttemptsSucceeded, &d.AmountSucceededSatTotal, &d.FeePaidSatTotal); err != nil {
			return nil, err
		}
		d.Day = day.Format("2006-01-02")
		if d.JobsTotal > 0 {
			denom := float64(d.JobsTotal)
			d.SuccessRate = float64(d.JobsSucceeded) / denom
			d.PartialRate = float64(d.JobsPartial) / denom
			d.FailedRate = float64(d.JobsFailed) / denom
			d.AvgAttemptsPerJob = float64(d.AttemptsTotal) / denom
		}
		successJobs := d.JobsSucceeded + d.JobsPartial
		if successJobs > 0 {
			d.AvgSatsPerSuccessfulJob = float64(d.AmountSucceededSatTotal) / float64(successJobs)
		}
		if d.AmountSucceededSatTotal > 0 {
			d.AvgFeePpmPaid = float64(d.FeePaidSatTotal) * 1_000_000.0 / float64(d.AmountSucceededSatTotal)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *RebalanceService) queryTimeToPaybackP50(ctx context.Context, from time.Time, to time.Time) (float64, bool) {
	if s.db == nil {
		return 0, false
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var p50 pgtype.Float8
	err := s.db.QueryRow(queryCtx, `
with succ_jobs as (
  select j.id, j.target_channel_id, j.completed_at,
    coalesce((select sum(fee_paid_sat) from rebalance_attempts a where a.job_id=j.id and a.status='succeeded'), 0) as fee_paid_sat
  from rebalance_jobs j
  where j.completed_at >= $1 and j.completed_at < $2
    and j.status in ('succeeded','partial')
),
filtered_jobs as (
  select * from succ_jobs where fee_paid_sat > 0
),
fwd as (
  select s.id as job_id, s.completed_at, s.fee_paid_sat,
    n.occurred_at,
    sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end)
      over (partition by s.id order by n.occurred_at rows between unbounded preceding and current row) as cum_fee_msat
  from filtered_jobs s
  join notifications n
    on n.channel_id = s.target_channel_id
   and n.type='forward'
   and n.occurred_at >= s.completed_at
   and n.occurred_at < $2
),
job_payback as (
  select job_id,
    min(extract(epoch from (occurred_at - completed_at))/3600.0) as hours_to_payback
  from fwd
  where cum_fee_msat >= fee_paid_sat * 1000
  group by job_id
)
select percentile_cont(0.5) within group (order by hours_to_payback)
from job_payback
`, from, to).Scan(&p50)
	if err != nil || !p50.Valid {
		return 0, false
	}
	return p50.Float64, true
}

func (s *RebalanceService) snapshotMetricsDay(ctx context.Context, day time.Time) {
	if s.db == nil {
		return
	}
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)
	daily, err := s.queryBaselineDaily(ctx, dayStart, dayEnd)
	if err != nil || len(daily) == 0 {
		return
	}
	d := daily[0]
	var paybackP50 any
	if p50, ok := s.queryTimeToPaybackP50(ctx, dayStart, dayEnd); ok {
		paybackP50 = p50
	}
	_, _ = s.db.Exec(ctx, `
insert into rebalance_metrics_daily (
  day, jobs_total, jobs_succeeded, jobs_partial, jobs_failed, jobs_cancelled,
  attempts_total, attempts_succeeded, success_rate, partial_rate, failed_rate,
  avg_attempts_per_job, avg_sats_per_successful_job, avg_fee_ppm_paid,
  fee_paid_sat_total, amount_succeeded_sat_total, time_to_payback_p50_hours, updated_at
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17, now())
on conflict (day) do update set
  jobs_total=excluded.jobs_total,
  jobs_succeeded=excluded.jobs_succeeded,
  jobs_partial=excluded.jobs_partial,
  jobs_failed=excluded.jobs_failed,
  jobs_cancelled=excluded.jobs_cancelled,
  attempts_total=excluded.attempts_total,
  attempts_succeeded=excluded.attempts_succeeded,
  success_rate=excluded.success_rate,
  partial_rate=excluded.partial_rate,
  failed_rate=excluded.failed_rate,
  avg_attempts_per_job=excluded.avg_attempts_per_job,
  avg_sats_per_successful_job=excluded.avg_sats_per_successful_job,
  avg_fee_ppm_paid=excluded.avg_fee_ppm_paid,
  fee_paid_sat_total=excluded.fee_paid_sat_total,
  amount_succeeded_sat_total=excluded.amount_succeeded_sat_total,
  time_to_payback_p50_hours=excluded.time_to_payback_p50_hours,
  updated_at=now()
`,
		dayStart, d.JobsTotal, d.JobsSucceeded, d.JobsPartial, d.JobsFailed, d.JobsCancelled,
		d.AttemptsTotal, d.AttemptsSucceeded, d.SuccessRate, d.PartialRate, d.FailedRate,
		d.AvgAttemptsPerJob, d.AvgSatsPerSuccessfulJob, d.AvgFeePpmPaid,
		d.FeePaidSatTotal, d.AmountSucceededSatTotal, paybackP50,
	)
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

// listChannelsCached devolve a lista de canais via cache curto (5s) para
// evitar saturação do gRPC do LND quando múltiplos jobs disparam ListChannels
// concorrentemente (bursts de cooldown-probe + auto-restart-watch + etc).
// O fetch usa per-call timeout de 15s para falhar rápido se o LND estiver
// hung, em vez de pendurar pelo timeout do job (10 min).
//
// Single-flight implícito via mutex: callers concorrentes esperam o primeiro
// fetch completar e depois leem o cache. Erros NÃO são cached — falhas
// retentam imediatamente para detectar recovery rápido.
func (s *RebalanceService) listChannelsCached(ctx context.Context) ([]lndclient.ChannelInfo, error) {
	const cacheTTL = 5 * time.Second
	const fetchTimeout = 15 * time.Second
	if s.lnd == nil {
		return nil, errors.New("lnd unavailable")
	}
	s.chCacheMu.Lock()
	defer s.chCacheMu.Unlock()
	if !s.chCacheFetchAt.IsZero() && time.Since(s.chCacheFetchAt) < cacheTTL && s.chCacheData != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance listChannelsCached: HIT (age=%dms, channels=%d)", time.Since(s.chCacheFetchAt).Milliseconds(), len(s.chCacheData))
		}
		return s.chCacheData, nil
	}
	if s.logger != nil {
		s.logger.Printf("rebalance listChannelsCached: MISS, fetching from LND (timeout=%s)", fetchTimeout)
	}
	startedAt := time.Now()
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	data, err := s.lnd.ListChannels(fetchCtx)
	elapsed := time.Since(startedAt)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("rebalance listChannelsCached: fetch FAILED after %s err=%v", elapsed, err)
		}
		return nil, err
	}
	if s.logger != nil {
		s.logger.Printf("rebalance listChannelsCached: fetch OK in %s (channels=%d)", elapsed, len(data))
	}
	s.chCacheData = data
	s.chCacheFetchAt = time.Now()
	return data, nil
}

// markFastPathAttempted marca o job como tendo tentado o caminho delegado
// (independente do resultado). Permite contar attempts vs successes via
// SQL — combinado com reason='delegated-fast-path' nos sucessos, dá hit-rate.
func (s *RebalanceService) markFastPathAttempted(jobID int64) {
	if s.db == nil || jobID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = s.db.Exec(ctx, `update rebalance_jobs set fast_path_attempted=true where id=$1`, jobID)
}

type attemptFailureInfo struct {
	SourceIndex int32
	HopPubkey   string
}

func parseRouteFailure(err error, route *lnrpc.Route) *attemptFailureInfo {
	if err == nil {
		return nil
	}
	var routeFailure lndclient.RouteFailureError
	if !errors.As(err, &routeFailure) {
		return nil
	}
	info := &attemptFailureInfo{SourceIndex: int32(routeFailure.FailureSourceIndex)}
	if route != nil {
		hopIdx := int(routeFailure.FailureSourceIndex) - 1
		if hopIdx >= 0 && hopIdx < len(route.Hops) {
			info.HopPubkey = route.Hops[hopIdx].PubKey
		}
	}
	return info
}

func (s *RebalanceService) insertAttempt(ctx context.Context, jobID int64, idx int, sourceChannelID uint64, amount int64, feePpm int64, feePaidSat int64, status string, paymentHash string, failReason string, fail *attemptFailureInfo) error {
	return s.insertAttemptWithStart(ctx, jobID, idx, sourceChannelID, amount, feePpm, feePaidSat, status, paymentHash, failReason, fail, time.Time{})
}

// insertAttemptWithStart é como insertAttempt mas permite especificar quando o
// attempt começou. Quando startedAt é zero, usa a default `now()` do DB (que
// na prática iguala finished_at = duração 0 — bug que afetava as percentile_cont
// queries do R6). Callers que medem latência real (fast-path) capturam o
// start time antes da chamada de rede e passam aqui pra que finished_at -
// started_at reflita a duração real do attempt.
func (s *RebalanceService) insertAttemptWithStart(ctx context.Context, jobID int64, idx int, sourceChannelID uint64, amount int64, feePpm int64, feePaidSat int64, status string, paymentHash string, failReason string, fail *attemptFailureInfo, startedAt time.Time) error {
	if s.db == nil {
		return nil
	}
	var failSourceIndex any
	var failHopPubkey any
	if fail != nil {
		failSourceIndex = int32(fail.SourceIndex)
		if fail.HopPubkey != "" {
			failHopPubkey = fail.HopPubkey
		}
	}
	if startedAt.IsZero() {
		_, err := s.db.Exec(ctx, `
insert into rebalance_attempts (
  job_id, attempt_index, source_channel_id, amount_sat, fee_limit_ppm, fee_paid_sat, status, payment_hash, fail_reason, fail_source_index, fail_hop_pubkey, finished_at
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
`, jobID, idx, int64(sourceChannelID), amount, feePpm, feePaidSat, status, nullableString(paymentHash), nullableString(failReason), failSourceIndex, failHopPubkey)
		return err
	}
	_, err := s.db.Exec(ctx, `
insert into rebalance_attempts (
  job_id, attempt_index, source_channel_id, amount_sat, fee_limit_ppm, fee_paid_sat, status, payment_hash, fail_reason, fail_source_index, fail_hop_pubkey, started_at, finished_at
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now())
`, jobID, idx, int64(sourceChannelID), amount, feePpm, feePaidSat, status, nullableString(paymentHash), nullableString(failReason), failSourceIndex, failHopPubkey, startedAt)
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
	remainingTotal := computeRemainingTotalBudget(budget, spent)
	remainingForAuto := computeRemainingForAuto(budget, spentAuto, spent, manualReserveSat, cfg.BudgetAutoOnly)
	manualReserveRemaining := computeManualReserveRemaining(manualReserveSat, spentManual)
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
	sovereignEconomics7d := rebalanceAutopilotEconomics7d{}
	mppShadowTelemetry := mppShadowTelemetry24h{}
	mppStructuralAbortJobs := int64(0)
	topFailureReasons30m := []RebalanceReasonStat{}
	routeDeadTargets30m := []RebalanceTargetStat{}
	var successCount int64
	var totalCount int64
	var attemptedCount int64
	var jobsWithoutAttemptCount int64
	var attemptCount int64
	var successfulAttemptCount int64
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
),
job_stats as (
  select
    coalesce(sum(case when status in ('succeeded','partial') then 1 else 0 end), 0) as success_count,
    count(*) as total_count,
    coalesce(sum(case when has_attempt then 1 else 0 end), 0) as attempted_count,
    coalesce(sum(case when not has_attempt then 1 else 0 end), 0) as jobs_without_attempt_count
  from jobs_with_attempts
),
attempt_stats as (
  select
    count(*) as attempt_count,
    coalesce(sum(case when status='succeeded' then 1 else 0 end), 0) as successful_attempt_count
  from rebalance_attempts
  where coalesce(finished_at, started_at) >= now() - interval '7 days'
)
select
  job_stats.success_count,
  job_stats.total_count,
  job_stats.attempted_count,
  job_stats.jobs_without_attempt_count,
  attempt_stats.attempt_count,
  attempt_stats.successful_attempt_count
from job_stats, attempt_stats
`).Scan(&successCount, &totalCount, &attemptedCount, &jobsWithoutAttemptCount, &attemptCount, &successfulAttemptCount)
	if attemptedCount > 0 {
		effectiveness = float64(successCount) / float64(attemptedCount)
	}
	effectivenessExecution := 0.0
	if attemptCount > 0 {
		effectivenessExecution = float64(successfulAttemptCount) / float64(attemptCount)
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
	jobs24h, successJobs24h, jobSuccessRate24h := s.fetchJobTelemetry24h(ctx)
	if economics, err := s.fetchSovereignAutopilotEconomics7d(ctx, cfg); err == nil {
		sovereignEconomics7d = economics
	}
	fastPath := s.fetchFastPathTelemetry24h(ctx)
	fastPathHitRate24h := 0.0
	if fastPath.Attempts > 0 {
		fastPathHitRate24h = float64(fastPath.Successes) / float64(fastPath.Attempts)
	}
	if telemetry, err := s.fetchMppShadowTelemetry24h(ctx); err == nil {
		mppShadowTelemetry = telemetry
	}
	mppStructuralAbortJobs = s.fetchMppStructuralAbortJobs24h(ctx)
	topFailureReasons30m, routeDeadTargets30m = s.fetchFailureTelemetry30m(ctx)

	eligibleSources := 0
	targetsNeeding := 0
	nodeCalib := RebalanceNodeCalibration{NodeClass: "unknown", LiquidityClass: "balanced"}
	if s.lnd != nil {
		eligibleSources, targetsNeeding, nodeCalib = s.computeEligibilityCounts(ctx, cfg)
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
	lastSovereignDecisionAt := s.lastSovereignDecisionAt
	lastSovereignMode := s.lastSovereignMode
	lastSovereignCandidates := s.lastSovereignCandidates
	lastSovereignSelected := s.lastSovereignSelected
	lastSovereignExpectedProfitSat := s.lastSovereignExpectedProfitSat
	lastSovereignBudgetRemainingSat := s.lastSovereignBudgetRemainingSat
	lastSovereignDecisions := append([]RebalanceSovereignDecision(nil), s.lastSovereignDecisions...)
	lastManualRestartAt := s.lastManualRestartAt
	lastManualRestartQueued := s.lastManualRestartQueued
	lastManualRestartReasons := copyReasonCounts(s.lastManualRestartReasons)
	mcState := s.missionControlStateLocked(time.Now())
	s.mu.Unlock()
	if !lastScan.IsZero() {
		if persisted := s.loadRebalanceScanSkips(ctx, lastScan, scanSkipDetailLimit); len(persisted) > 0 {
			lastScanSkipped = persisted
		}
	}
	sovereignHistory24h := s.loadSovereignAutopilotHistory(ctx, time.Now().Add(-24*time.Hour), 288, false)
	lastSovereignDecisionAtText := ""
	if !lastSovereignDecisionAt.IsZero() {
		lastSovereignDecisionAtText = lastSovereignDecisionAt.UTC().Format(time.RFC3339)
	}

	hs30 := s.healthSignals30d()

	overview := RebalanceOverview{
		AutoEnabled:                         cfg.AutoEnabled,
		Profile:                             cfg.Profile,
		SchedulerMode:                       normalizeRebalanceSchedulerMode(cfg.SchedulerMode),
		SovereignLastDecisionAt:             lastSovereignDecisionAtText,
		SovereignLastMode:                   lastSovereignMode,
		SovereignCandidates:                 lastSovereignCandidates,
		SovereignSelected:                   lastSovereignSelected,
		SovereignExpectedProfitSat:          lastSovereignExpectedProfitSat,
		SovereignBudgetRemainingSat:         lastSovereignBudgetRemainingSat,
		SovereignDecisions:                  lastSovereignDecisions,
		SovereignHistory24h:                 sovereignHistory24h,
		DailyBudgetSat:                      budget,
		DailyBudgetBaseSat:                  baseBudget,
		DailyBudgetShortTermSat:             shortTermBudget,
		DailySpentSat:                       spent,
		DailySpentAutoSat:                   spentAuto,
		DailySpentManualSat:                 spentManual,
		RemainingTotalSat:                   remainingTotal,
		RemainingForAutoSat:                 remainingForAuto,
		BudgetUnlimited:                     cfg.BudgetUnlimited,
		BudgetAutoOnly:                      cfg.BudgetAutoOnly,
		ManualReserveEnabled:                cfg.ManualReserveEnabled,
		ManualReserveMode:                   cfg.ManualReserveMode,
		ManualReserveValue:                  cfg.ManualReserveValue,
		ManualReserveSat:                    manualReserveSat,
		ManualReserveRemainingSat:           manualReserveRemaining,
		LiveCostSat:                         liveCost,
		Effectiveness7d:                     effectiveness,
		EffectivenessExecution7d:            effectivenessExecution,
		Effectiveness30d:                    hs30.Effectiveness,
		FastPathHitRate30d:                  hs30.FastPathHitRate,
		PaybackProgressRebalanced30d:        hs30.PaybackProgressRebal,
		HealthSignals30dReady:               hs30.Valid,
		JobsWithoutAttempt7d:                jobsWithoutAttemptCount,
		JobsWithoutAttemptRate7d:            jobsWithoutAttemptRate,
		ROI7d:                               roi,
		SovereignRebalanceAmount7dSat:       sovereignEconomics7d.RebalanceAmountSat,
		SovereignRebalanceCost7dSat:         sovereignEconomics7d.RebalanceCostSat,
		SovereignRebalanceCost7dPpm:         sovereignEconomics7d.RebalanceCostPpm,
		SovereignForwardAmount7dSat:         sovereignEconomics7d.ForwardAmountSat,
		SovereignForwardFee7dSat:            sovereignEconomics7d.ForwardFeeSat,
		SovereignForwardFee7dPpm:            sovereignEconomics7d.ForwardFeePpm,
		SovereignRealizedNet7dSat:           sovereignEconomics7d.RealizedNetSat,
		SovereignSellThrough7d:              sovereignEconomics7d.SellThrough,
		SovereignForwardAmountSlow7dSat:     sovereignEconomics7d.ForwardAmountSlowSat,
		SovereignForwardFeeSlow7dSat:        sovereignEconomics7d.ForwardFeeSlowSat,
		SovereignRealizedNetSlow7dSat:       sovereignEconomics7d.RealizedNetSlowSat,
		SovereignSellThroughSlow7d:          sovereignEconomics7d.SellThroughSlow,
		SovereignSellThroughWindowHours:     sovereignEconomics7d.AttributionWindowHours,
		SovereignSellThroughSlowWindowHours: sovereignEconomics7d.SlowSellerWindowHours,
		Jobs24h:                             jobs24h,
		SuccessJobs24h:                      successJobs24h,
		JobSuccessRate24h:                   jobSuccessRate24h,
		Attempts24h:                         attemptTelemetry.Attempts,
		FailedAttempts24h:                   attemptTelemetry.FailedAttempts,
		SuccessAttempts24h:                  attemptTelemetry.SuccessAttempts,
		SuccessAmount24hSat:                 attemptTelemetry.SuccessAmountSat,
		SuccessAvgAmount24hSat:              attemptTelemetry.SuccessAvgAmountSat,
		AttemptSuccessRate24h:               attemptTelemetry.AttemptSuccessRate,
		AttemptsPerSuccessAttempt24h:        attemptTelemetry.AttemptsPerSuccess,
		SuccessSatsPerAttempt24h:            attemptTelemetry.SuccessSatsPerAttempt,
		SuccessBelowMinAttempts24h:          attemptTelemetry.SuccessBelowMinAttempts,
		SuccessBelowMinAmount24hSat:         attemptTelemetry.SuccessBelowMinAmountSat,
		SuccessBelowMinRate24h:              attemptTelemetry.SuccessBelowMinRate,
		FastPathAttempts24h:                 fastPath.Attempts,
		FastPathSuccesses24h:                fastPath.Successes,
		FastPathHitRate24h:                  fastPathHitRate24h,
		FastPathFailures24h:                 fastPath.Failures,
		FastPathFallthroughs24h:             fastPath.Fallthroughs,
		FastPathDurationP50Ms:               fastPath.DurationP50Ms,
		FastPathDurationP95Ms:               fastPath.DurationP95Ms,
		FastPathFailReasons24h:              fastPath.FailReasons,
		PaybackRevenueSat:                   paybackRevenue,
		PaybackRevenueRebalancedSat:         paybackRevenueRebalanced,
		PaybackCostSat:                      paybackCost,
		PaybackProgress:                     paybackProgress,
		PaybackProgressRebalanced:           paybackProgressRebalanced,
		LastScanStatus:                      lastScanStatus,
		LastScanDetail:                      lastScanDetail,
		LastScanCandidates:                  lastScanCandidates,
		LastScanRemainingBudgetSat:          lastScanRemainingBudgetSat,
		LastScanReasons:                     lastScanReasons,
		LastScanTopScoreSat:                 lastScanTopScore,
		LastScanProfitSkipped:               lastScanProfitSkipped,
		LastScanQueued:                      lastScanQueued,
		LastScanSkipped:                     lastScanSkipped,
		LastManualRestartQueued:             lastManualRestartQueued,
		LastManualRestartReasons:            lastManualRestartReasons,
		LastMCResetAt:                       mcState.LastMCResetAt,
		LastMCResetReason:                   mcState.LastMCResetReason,
		MCResetCount:                        mcState.MCResetCount,
		MCResetCooldownSec:                  mcState.MCResetCooldownSec,
		MCResetCooldownRemainingSec:         mcState.MCResetCooldownRemainingSec,
		EligibleSources:                     eligibleSources,
		TargetsNeeding:                      targetsNeeding,
		NodeCalibration:                     nodeCalib,
		MppShadowJobs24h:                    mppShadowTelemetry.Jobs,
		MppShadowPlanReady24h:               mppShadowTelemetry.PlanReadyJobs,
		MppShadowPlannedSat24h:              mppShadowTelemetry.PlannedSat,
		MppShadowActualSentSat24h:           mppShadowTelemetry.ActualSentSat,
		MppShadowInProgressJobs24h:          mppShadowTelemetry.InProgressJobs,
		MppShadowSuccessJobs24h:             mppShadowTelemetry.SuccessJobs,
		MppShadowFailedJobs24h:              mppShadowTelemetry.FailedJobs,
		MppShadowPartialJobs24h:             mppShadowTelemetry.PartialJobs,
		MppShadowFloorBlocked24h:            mppShadowTelemetry.FloorBlocked,
		MppShadowAvgPlannedShards24h:        mppShadowTelemetry.AvgPlannedShards,
		MppShadowAvgActualAttempts24h:       mppShadowTelemetry.AvgActualAttempts,
		MppStructuralAbortJobs24h:           mppStructuralAbortJobs,
		TopFailureReasons30m:                topFailureReasons30m,
		RouteDeadTargets30m:                 routeDeadTargets30m,
	}
	if !lastScan.IsZero() {
		overview.LastScanAt = lastScan.UTC().Format(time.RFC3339)
	}
	if !lastManualRestartAt.IsZero() {
		overview.LastManualRestartAt = lastManualRestartAt.UTC().Format(time.RFC3339)
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
	ledger, _ := s.loadLedger(ctx, cfg)
	_ = s.applyForwardDeltas(ctx, ledger)

	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)
	drainRateByChannel := s.fetchChannelDrainRate24h(ctx)
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
		snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], drainRateByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		result = append(result, snapshot)
	}
	return result, nil
}

// classifyRebalanceNode computes the node size/liquidity calibration over ACTIVE
// channels, mirroring AutoFee's calibrateNode thresholds (autofee_service.go) so
// both engines agree on what small/medium/large/xl and drained/balanced/full
// mean. Pure function for unit testing.
func classifyRebalanceNode(channels []lndclient.ChannelInfo) RebalanceNodeCalibration {
	calib := RebalanceNodeCalibration{NodeClass: "unknown", LiquidityClass: "balanced"}
	calib.TotalChannelCount = len(channels)
	for _, ch := range channels {
		if !ch.Active {
			continue
		}
		calib.ChannelCount++
		calib.TotalCapacitySat += ch.CapacitySat
		calib.LocalCapacitySat += ch.LocalBalanceSat
		calib.InboundCapacitySat += ch.RemoteBalanceSat
	}
	if calib.ChannelCount > 0 {
		calib.AvgCapacitySat = int64(math.Round(float64(calib.TotalCapacitySat) / float64(calib.ChannelCount)))
	}
	if calib.TotalCapacitySat > 0 {
		calib.LocalRatio = float64(calib.LocalCapacitySat) / float64(calib.TotalCapacitySat)
	}
	if calib.ChannelCount > 0 && calib.TotalCapacitySat > 0 {
		switch {
		case calib.TotalCapacitySat < 50_000_000 || calib.ChannelCount < 20:
			calib.NodeClass = "small"
		case calib.TotalCapacitySat < 200_000_000 || calib.ChannelCount < 60:
			calib.NodeClass = "medium"
		case calib.TotalCapacitySat < 1_500_000_000 || calib.ChannelCount < 150:
			calib.NodeClass = "large"
		default:
			calib.NodeClass = "xl"
		}
		if calib.LocalRatio < 0.25 {
			calib.LiquidityClass = "drained"
		} else if calib.LocalRatio > 0.75 {
			calib.LiquidityClass = "full"
		}
	}
	return calib
}

func (s *RebalanceService) computeEligibilityCounts(ctx context.Context, cfg RebalanceConfig) (int, int, RebalanceNodeCalibration) {
	if s.lnd == nil {
		return 0, 0, RebalanceNodeCalibration{NodeClass: "unknown", LiquidityClass: "balanced"}
	}
	settings, _ := s.loadChannelSettings(ctx)
	exclusions, _ := s.loadExclusions(ctx)
	ledger, _ := s.loadLedger(ctx, cfg)
	_ = s.applyForwardDeltas(ctx, ledger)

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return 0, 0, RebalanceNodeCalibration{NodeClass: "unknown", LiquidityClass: "balanced"}
	}
	s.reconcileNewChannelDefaults(ctx, channels, settings, exclusions)
	revenueByChannel, _ := s.fetchChannelRevenue7d(ctx)
	costByChannel, _ := s.fetchChannelRebalanceCost7d(ctx)
	drainRateByChannel := s.fetchChannelDrainRate24h(ctx)

	s.mu.Lock()
	criticalActive := cfg.CriticalCycles > 0 && s.criticalMissCount >= cfg.CriticalCycles
	s.mu.Unlock()

	eligibleSources := 0
	targetsNeeding := 0
	for _, ch := range channels {
		setting := settings[ch.ChannelID]
		snapshot := s.buildChannelSnapshot(ctx, cfg, criticalActive, ch, setting, ledger[ch.ChannelID], revenueByChannel[ch.ChannelID], costByChannel[ch.ChannelID], drainRateByChannel[ch.ChannelID], exclusions[ch.ChannelID])
		if snapshot.EligibleAsSource {
			eligibleSources++
		}
		if setting.AutoEnabled && snapshot.EligibleAsTarget && (cfg.ROIMin <= 0 || !snapshot.ROIEstimateValid || snapshot.ROIEstimate >= cfg.ROIMin) {
			targetsNeeding++
		}
	}
	return eligibleSources, targetsNeeding, classifyRebalanceNode(channels)
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
select id, created_at, completed_at, source, status, trigger_reason, reason, target_channel_id,
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
		var triggerReason pgtype.Text
		var reason pgtype.Text
		var targetChannelID int64
		if err := rows.Scan(&job.ID, &created, &completed, &job.Source, &job.Status, &triggerReason, &reason, &targetChannelID,
			&job.TargetChannelPoint, &job.TargetOutboundPct, &job.TargetAmountSat); err != nil {
			return jobs, attempts, err
		}
		job.CreatedAt = created.UTC().Format(time.RFC3339)
		if completed.Valid {
			job.CompletedAt = completed.Time.UTC().Format(time.RFC3339)
		}
		if triggerReason.Valid {
			job.TriggerReason = triggerReason.String
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

// cleanupOrphanedJobsAtStartup marca TODOS os jobs com status='running' ou
// 'queued' como órfãos, sem filtro de idade. Chamado uma vez no Start() após
// restart — nesse momento qualquer job nesses status é por definição órfão
// (não existe goroutine viva no processo novo para executá-los). Usar reason
// específica "orphaned by restart" pra distinguir de timeouts genuínos.
func (s *RebalanceService) cleanupOrphanedJobsAtStartup(ctx context.Context) {
	if s.db == nil {
		return
	}
	res, err := s.db.Exec(ctx, `
update rebalance_jobs
set status = case
  when exists (select 1 from rebalance_attempts a where a.job_id=rebalance_jobs.id and a.status='succeeded')
    then 'partial'
  else 'failed'
end,
reason = case
  when exists (select 1 from rebalance_attempts a where a.job_id=rebalance_jobs.id and a.status='succeeded')
    then 'orphaned by restart (partial)'
  else 'orphaned by restart'
end,
completed_at=now()
where status in ('running','queued')
`)
	if err == nil && s.logger != nil {
		if rows := res.RowsAffected(); rows > 0 {
			s.logger.Printf("rebalance startup: cleaned %d orphaned jobs from previous shutdown", rows)
		}
	}
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
	// Cleanup só pega jobs REALMENTE órfãos: deploys/crashes onde a goroutine
	// morreu sem chamar finishJob. Como a fila tem espera indeterminada
	// (acquireSem com context.Background()), jobs em status='queued' podem
	// legitimamente esperar muito. Cutoff de 1h é generoso — qualquer job
	// vivo nessa janela ainda está rodando ou na fila por capacity.
	// Jobs aparentemente "stuck" mais antigos que 1h são presumivelmente
	// resíduos de processo antigo (restart sem cleanup adequado).
	const stuckJobMaxSec = 3600 // 1 hour
	totalCutoffSec := timeoutSec
	if totalCutoffSec < stuckJobMaxSec {
		totalCutoffSec = stuckJobMaxSec
	}
	cutoff := time.Now().Add(-time.Duration(totalCutoffSec) * time.Second)
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
select id, created_at, completed_at, source, status, trigger_reason, reason, target_channel_id,
  target_channel_point, target_outbound_pct, target_amount_sat,
  sovereign_expected_gain_sat, sovereign_estimated_cost_sat, sovereign_expected_profit_sat,
  sovereign_budget_cost_sat, sovereign_score
from rebalance_jobs
where status in ('succeeded','failed','cancelled','partial','skipped')
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
		var triggerReason pgtype.Text
		var reason pgtype.Text
		var targetChannelID int64
		if err := rows.Scan(&job.ID, &created, &completed, &job.Source, &job.Status, &triggerReason, &reason, &targetChannelID,
			&job.TargetChannelPoint, &job.TargetOutboundPct, &job.TargetAmountSat,
			&job.SovereignExpectedGainSat, &job.SovereignEstimatedCostSat, &job.SovereignExpectedProfitSat,
			&job.SovereignBudgetCostSat, &job.SovereignScore); err != nil {
			return jobs, attempts, err
		}
		job.CreatedAt = created.UTC().Format(time.RFC3339)
		if completed.Valid {
			job.CompletedAt = completed.Time.UTC().Format(time.RFC3339)
		}
		if triggerReason.Valid {
			job.TriggerReason = triggerReason.String
		}
		if reason.Valid {
			job.Reason = reason.String
		}
		job.TargetChannelID = uint64(targetChannelID)
		job.TargetPeerAlias = aliasMap[job.TargetChannelID]
		jobs = append(jobs, job)
	}
	jobs = s.enrichRebalanceJobEconomics(ctx, jobs)

	attemptBase := `
with recent as (
  select id from rebalance_jobs
  where status in ('succeeded','failed','cancelled','partial','skipped')
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

func (s *RebalanceService) enrichRebalanceJobEconomics(ctx context.Context, jobs []RebalanceJob) []RebalanceJob {
	if s.db == nil || len(jobs) == 0 {
		return jobs
	}
	ids := make([]int64, 0, len(jobs))
	indexByID := map[int64]int{}
	for i := range jobs {
		ids = append(ids, jobs[i].ID)
		indexByID[jobs[i].ID] = i
	}

	rows, err := s.db.Query(ctx, `
select
  j.id,
  coalesce(sum(a.amount_sat) filter (where a.status='succeeded'), 0) as sent_sat,
  coalesce(sum(a.fee_paid_sat) filter (where a.status='succeeded'), 0) as fee_paid_sat
from rebalance_jobs j
left join rebalance_attempts a on a.job_id = j.id
where j.id = any($1::bigint[])
group by j.id
`, ids)
	if err == nil {
		for rows.Next() {
			var jobID int64
			var sentSat int64
			var feePaidSat int64
			if err := rows.Scan(&jobID, &sentSat, &feePaidSat); err != nil {
				break
			}
			if idx, ok := indexByID[jobID]; ok {
				jobs[idx].ActualSentSat = sentSat
				jobs[idx].ActualRebalanceFeeSat = feePaidSat
			}
		}
		rows.Close()
	}

	forwardRows, err := s.db.Query(ctx, `
select
  j.id,
  count(n.id) filter (where n.occurred_at < j.completed_at + interval '1 hour') as forward_1h_count,
  coalesce(sum(n.amount_sat) filter (where n.occurred_at < j.completed_at + interval '1 hour'), 0) as forward_1h_amount_sat,
  coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end) filter (where n.occurred_at < j.completed_at + interval '1 hour'), 0) as forward_1h_fee_msat,
  count(n.id) filter (where n.occurred_at < j.completed_at + interval '6 hours') as forward_6h_count,
  coalesce(sum(n.amount_sat) filter (where n.occurred_at < j.completed_at + interval '6 hours'), 0) as forward_6h_amount_sat,
  coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end) filter (where n.occurred_at < j.completed_at + interval '6 hours'), 0) as forward_6h_fee_msat,
  count(n.id) as forward_24h_count,
  coalesce(sum(n.amount_sat), 0) as forward_24h_amount_sat,
  coalesce(sum(case when n.fee_msat > 0 then n.fee_msat else n.fee_sat * 1000 end), 0) as forward_24h_fee_msat
from rebalance_jobs j
left join notifications n on n.type='forward'
  and j.completed_at is not null
  and n.channel_id = j.target_channel_id
  and n.occurred_at >= j.completed_at
  and n.occurred_at < j.completed_at + interval '24 hours'
where j.id = any($1::bigint[])
group by j.id
`, ids)
	if err != nil {
		return jobs
	}
	defer forwardRows.Close()
	for forwardRows.Next() {
		var jobID int64
		var fee1hMsat int64
		var fee6hMsat int64
		var fee24hMsat int64
		var forward1hCount int64
		var forward1hAmount int64
		var forward6hCount int64
		var forward6hAmount int64
		var forward24hCount int64
		var forward24hAmount int64
		if err := forwardRows.Scan(
			&jobID,
			&forward1hCount,
			&forward1hAmount,
			&fee1hMsat,
			&forward6hCount,
			&forward6hAmount,
			&fee6hMsat,
			&forward24hCount,
			&forward24hAmount,
			&fee24hMsat,
		); err != nil {
			return jobs
		}
		idx, ok := indexByID[jobID]
		if !ok {
			continue
		}
		jobs[idx].Forward1hCount = forward1hCount
		jobs[idx].Forward1hAmountSat = forward1hAmount
		jobs[idx].Forward1hFeeSat = fee1hMsat / 1000
		jobs[idx].Forward6hCount = forward6hCount
		jobs[idx].Forward6hAmountSat = forward6hAmount
		jobs[idx].Forward6hFeeSat = fee6hMsat / 1000
		jobs[idx].Forward24hCount = forward24hCount
		jobs[idx].Forward24hAmountSat = forward24hAmount
		jobs[idx].Forward24hFeeSat = fee24hMsat / 1000
		job := &jobs[idx]
		job.AttributedForward1hAmountSat, job.AttributedForward1hFeeSat, job.RealizedNet1hSat = attributedForwardEconomics(
			job.ActualSentSat,
			job.ActualRebalanceFeeSat,
			job.Forward1hAmountSat,
			job.Forward1hFeeSat,
		)
		job.AttributedForward6hAmountSat, job.AttributedForward6hFeeSat, job.RealizedNet6hSat = attributedForwardEconomics(
			job.ActualSentSat,
			job.ActualRebalanceFeeSat,
			job.Forward6hAmountSat,
			job.Forward6hFeeSat,
		)
		job.AttributedForward24hAmountSat, job.AttributedForward24hFeeSat, job.RealizedNet24hSat = attributedForwardEconomics(
			job.ActualSentSat,
			job.ActualRebalanceFeeSat,
			job.Forward24hAmountSat,
			job.Forward24hFeeSat,
		)
	}
	return jobs
}

func attributedForwardEconomics(sentSat int64, feePaidSat int64, forwardAmountSat int64, forwardFeeSat int64) (int64, int64, int64) {
	if sentSat <= 0 {
		return 0, 0, 0
	}
	if forwardAmountSat <= 0 || forwardFeeSat <= 0 {
		return 0, 0, -feePaidSat
	}
	attributedAmountSat := forwardAmountSat
	attributedFeeSat := forwardFeeSat
	if forwardAmountSat > sentSat {
		attributedAmountSat = sentSat
		attributedFeeSat = (forwardFeeSat * sentSat) / forwardAmountSat
	}
	if attributedFeeSat < 0 {
		attributedFeeSat = 0
	}
	return attributedAmountSat, attributedFeeSat, attributedFeeSat - feePaidSat
}

func (s *RebalanceService) SetChannelTarget(ctx context.Context, channelID uint64, channelPoint string, targetPct float64) error {
	return s.UpdateChannelTargetSettings(ctx, channelID, channelPoint, &targetPct, nil, nil, nil)
}

func (s *RebalanceService) UpdateChannelTargetSettings(ctx context.Context, channelID uint64, channelPoint string, targetPct *float64, useDefaultEconRatio *bool, econRatioOverride *float64, autoBypassCostGate *bool) error {
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
select channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, econ_ratio_override, auto_bypass_cost_gate
from rebalance_channel_settings
where channel_id=$1
`, int64(channelID)).Scan(&current.ChannelPoint, &current.TargetOutboundPct, &current.AutoEnabled, &current.ManualRestartEnabled, &current.UseDefaultEconRatio, &currentOverride, &current.AutoBypassCostGate)
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
	if autoBypassCostGate != nil {
		current.AutoBypassCostGate = *autoBypassCostGate
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
  channel_id, channel_point, target_outbound_pct, auto_enabled, manual_restart_enabled, use_default_econ_ratio, econ_ratio_override, auto_bypass_cost_gate, updated_at
)
values ($1,$2,$3,$4,$5,$6,$7,$8,now())
 on conflict (channel_id) do update set
  channel_point=excluded.channel_point,
  target_outbound_pct=excluded.target_outbound_pct,
  auto_enabled=excluded.auto_enabled,
  manual_restart_enabled=excluded.manual_restart_enabled,
  use_default_econ_ratio=excluded.use_default_econ_ratio,
  econ_ratio_override=excluded.econ_ratio_override,
  auto_bypass_cost_gate=excluded.auto_bypass_cost_gate,
  updated_at=now()
`, int64(channelID), current.ChannelPoint, current.TargetOutboundPct, current.AutoEnabled, current.ManualRestartEnabled, current.UseDefaultEconRatio, overrideValue, current.AutoBypassCostGate)
	return err
}

func (s *RebalanceService) SetChannelAuto(ctx context.Context, channelID uint64, channelPoint string, autoEnabled bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if autoEnabled {
		policy, ok, err := loadChannelAutomationPolicy(ctx, s.db, channelID)
		if err != nil {
			return err
		}
		if ok && isChannelAutomationParked(policy.Mode) {
			return errChannelAutomationParked
		}
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
	if enabled {
		policy, ok, err := loadChannelAutomationPolicy(ctx, s.db, channelID)
		if err != nil {
			return err
		}
		if ok && isChannelAutomationParked(policy.Mode) {
			return errChannelAutomationParked
		}
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

// SetChannelAutoTargetManaged toggles whether AutoTarget may adjust this
// channel's target_outbound_pct. Only touches auto_target_managed; other
// automation flags are preserved.
func (s *RebalanceService) SetChannelAutoTargetManaged(ctx context.Context, channelID uint64, channelPoint string, managed bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	_, err := s.db.Exec(ctx, `
  insert into rebalance_channel_settings (channel_id, channel_point, target_outbound_pct, auto_target_managed, updated_at)
  values ($1,$2,$3,$4,now())
   on conflict (channel_id) do update set
     channel_point=case when excluded.channel_point <> '' then excluded.channel_point else rebalance_channel_settings.channel_point end,
     auto_target_managed=excluded.auto_target_managed,
     updated_at=now()
  `, int64(channelID), strings.TrimSpace(channelPoint), rebalanceDefaultTargetOutboundPct, managed)
	return err
}

func (s *RebalanceService) SetSourceExcluded(ctx context.Context, channelID uint64, channelPoint string, excluded bool) error {
	if s.db == nil {
		return errors.New("db unavailable")
	}
	if !excluded {
		policy, ok, err := loadChannelAutomationPolicy(ctx, s.db, channelID)
		if err != nil {
			return err
		}
		if ok && isChannelAutomationParked(policy.Mode) {
			return errChannelAutomationParked
		}
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
