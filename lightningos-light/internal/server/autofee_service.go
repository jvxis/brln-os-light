
package server

import (
  "bytes"
  "context"
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
  autofeeConfigID = 1
  autofeeMinLookbackDays = 5
  autofeeMaxLookbackDays = 21
)

const superSourceBaseFeeMsatDefault = 1000
const rebalCostModeDefault = "blend"
const htlcModeDefault = "full"

const (
  htlcModeObserveOnly = "observe_only"
  htlcModePolicyOnly = "policy_only"
  htlcModeFull = "full"
)

const (
  defaultLowOutProtectThresh = 0.10
  defaultSinkMinMargin = 150
  defaultMinSoftCeiling = 100
  defaultSeedCeilingMult = 1.50
  defaultSeedFloorMult = 1.10
  defaultSeedP95Boost = 1.15
  defaultSourceSeedTargetFrac = 0.55
  defaultProfitProtectOutRatio = 0.10
  defaultProfitProtectRelaxHours = 72
  defaultProfitProtectRelaxMaxFwds = 1
  defaultProfitProtectRelaxStepFrac = 0.015
  defaultProfitProtectRelaxMinStepPpm = 15
  defaultGlobalNegLockSoften = true
  defaultSoftenMinOutRatio = 0.45
  defaultSoftenRequirePosChanMargin = true
  defaultSoftenMaxDropToPegFrac = 0.95
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

type AutofeeConfig struct {
  Enabled bool `json:"enabled"`
  Profile string `json:"profile"`
  LookbackDays int `json:"lookback_days"`
  RunIntervalSec int `json:"run_interval_sec"`
  CooldownUpSec int `json:"cooldown_up_sec"`
  CooldownDownSec int `json:"cooldown_down_sec"`
  RebalCostMode string `json:"rebal_cost_mode"`
  AmbossEnabled bool `json:"amboss_enabled"`
  AmbossTokenSet bool `json:"amboss_token_set"`
  InboundPassiveEnabled bool `json:"inbound_passive_enabled"`
  DiscoveryEnabled bool `json:"discovery_enabled"`
  ExplorerEnabled bool `json:"explorer_enabled"`
  SuperSourceEnabled bool `json:"super_source_enabled"`
  SuperSourceBaseFeeMsat int `json:"super_source_base_fee_msat"`
  RevfloorEnabled bool `json:"revfloor_enabled"`
  CircuitBreakerEnabled bool `json:"circuit_breaker_enabled"`
  ExtremeDrainEnabled bool `json:"extreme_drain_enabled"`
  HTLCSignalEnabled bool `json:"htlc_signal_enabled"`
  HTLCMode string `json:"htlc_mode"`
  MinPpm int `json:"min_ppm"`
  MaxPpm int `json:"max_ppm"`
}

type AutofeeConfigUpdate struct {
  Enabled *bool `json:"enabled,omitempty"`
  Profile *string `json:"profile,omitempty"`
  LookbackDays *int `json:"lookback_days,omitempty"`
  RunIntervalSec *int `json:"run_interval_sec,omitempty"`
  CooldownUpSec *int `json:"cooldown_up_sec,omitempty"`
  CooldownDownSec *int `json:"cooldown_down_sec,omitempty"`
  RebalCostMode *string `json:"rebal_cost_mode,omitempty"`
  AmbossEnabled *bool `json:"amboss_enabled,omitempty"`
  AmbossToken *string `json:"amboss_token,omitempty"`
  InboundPassiveEnabled *bool `json:"inbound_passive_enabled,omitempty"`
  DiscoveryEnabled *bool `json:"discovery_enabled,omitempty"`
  ExplorerEnabled *bool `json:"explorer_enabled,omitempty"`
  SuperSourceEnabled *bool `json:"super_source_enabled,omitempty"`
  SuperSourceBaseFeeMsat *int `json:"super_source_base_fee_msat,omitempty"`
  RevfloorEnabled *bool `json:"revfloor_enabled,omitempty"`
  CircuitBreakerEnabled *bool `json:"circuit_breaker_enabled,omitempty"`
  ExtremeDrainEnabled *bool `json:"extreme_drain_enabled,omitempty"`
  HTLCSignalEnabled *bool `json:"htlc_signal_enabled,omitempty"`
  HTLCMode *string `json:"htlc_mode,omitempty"`
  MinPpm *int `json:"min_ppm,omitempty"`
  MaxPpm *int `json:"max_ppm,omitempty"`
}

type AutofeeStatus struct {
  Running bool `json:"running"`
  LastRunAt string `json:"last_run_at,omitempty"`
  NextRunAt string `json:"next_run_at,omitempty"`
  LastError string `json:"last_error,omitempty"`
}

type autofeeLogItem struct {
  Kind string `json:"kind"`
  Category string `json:"category,omitempty"`
  Reason string `json:"reason,omitempty"`
  DryRun bool `json:"dry_run,omitempty"`
  Timestamp string `json:"timestamp,omitempty"`
  NodeClass string `json:"node_class,omitempty"`
  LiquidityClass string `json:"liquidity_class,omitempty"`
  ChannelCount int `json:"channel_count,omitempty"`
  TotalCapacitySat int64 `json:"total_capacity_sat,omitempty"`
  AvgCapacitySat int64 `json:"avg_capacity_sat,omitempty"`
  LocalCapacitySat int64 `json:"local_capacity_sat,omitempty"`
  LocalRatio float64 `json:"local_ratio,omitempty"`
  RevfloorBaseline int `json:"revfloor_baseline,omitempty"`
  RevfloorMinAbs int `json:"revfloor_min_abs,omitempty"`
  Up int `json:"up,omitempty"`
  Down int `json:"down,omitempty"`
  Flat int `json:"flat,omitempty"`
  Cooldown int `json:"cooldown,omitempty"`
  Small int `json:"small,omitempty"`
  Same int `json:"same,omitempty"`
  Disabled int `json:"disabled,omitempty"`
  Inactive int `json:"inactive,omitempty"`
  InboundDisc int `json:"inbound_disc,omitempty"`
  SuperSource int `json:"super_source,omitempty"`
  HTLCLiqHot int `json:"htlc_liq_hot,omitempty"`
  HTLCPolicyHot int `json:"htlc_policy_hot,omitempty"`
  HTLCSampleLow int `json:"htlc_sample_low,omitempty"`
  HTLCWindowMin int `json:"htlc_window_min,omitempty"`
  HTLCMinAttempts int `json:"htlc_min_attempts,omitempty"`
  HTLCMinPolicyFails int `json:"htlc_min_policy_fails,omitempty"`
  HTLCMinLiquidityFails int `json:"htlc_min_liquidity_fails,omitempty"`
  HTLCNodeFactor float64 `json:"htlc_node_factor,omitempty"`
  HTLCLiquidityFactor float64 `json:"htlc_liquidity_factor,omitempty"`
  HTLCThresholdFactor float64 `json:"htlc_threshold_factor,omitempty"`
  Amboss int `json:"amboss,omitempty"`
  Missing int `json:"missing,omitempty"`
  Err int `json:"err,omitempty"`
  Empty int `json:"empty,omitempty"`
  Outrate int `json:"outrate,omitempty"`
  Mem int `json:"mem,omitempty"`
  Default int `json:"default,omitempty"`
  CooldownIgnored bool `json:"cooldown_ignored,omitempty"`
  Alias string `json:"alias,omitempty"`
  ChannelID uint64 `json:"channel_id,omitempty"`
  ChannelPoint string `json:"channel_point,omitempty"`
  LocalPpm int `json:"local_ppm,omitempty"`
  NewPpm int `json:"new_ppm,omitempty"`
  Target int `json:"target,omitempty"`
  OutRatio float64 `json:"out_ratio,omitempty"`
  OutPpm7d int `json:"out_ppm7d,omitempty"`
  RebalPpm7d int `json:"rebal_ppm7d,omitempty"`
  Seed int `json:"seed,omitempty"`
  Floor int `json:"floor,omitempty"`
  FloorSrc string `json:"floor_src,omitempty"`
  Margin int `json:"margin,omitempty"`
  RevShare float64 `json:"rev_share,omitempty"`
  Tags []string `json:"tags,omitempty"`
  InboundDiscount int `json:"inbound_discount,omitempty"`
  ClassLabel string `json:"class_label,omitempty"`
  SkipReason string `json:"skip_reason,omitempty"`
  Error string `json:"error,omitempty"`
  Delta int `json:"delta,omitempty"`
  DeltaPct float64 `json:"delta_pct,omitempty"`
  PredictionCode string `json:"prediction_code,omitempty"`
  PredictionCooldownHours int `json:"prediction_cooldown_hours,omitempty"`
  HTLCAttempts int `json:"htlc_attempts,omitempty"`
  HTLCPolicyFails int `json:"htlc_policy_fails,omitempty"`
  HTLCLiquidityFails int `json:"htlc_liquidity_fails,omitempty"`
  HTLCWindowMinChannel int `json:"htlc_window_min_channel,omitempty"`
}

type autofeeProfile struct {
  Name string
  StepCap float64
  LowOutThresh float64
  LowOutProtectThresh float64
  HighOutThresh float64
  SurgeBumpMax float64
  RunIntervalSec int
  CooldownUpSec int
  CooldownDownSec int
  DiscoveryStepCapDown float64
  SeedGuardMaxJump float64
  OutratePegGraceHours int
  ProfitDownMarginMin int
  ProfitDownFwdsMin int
  ProfitDownExtraHours int
  NegMarginSurgeBump float64
  NegMarginSurgeMinFwds int
  NegMarginSurgeFwdsRatio float64
  SinkExtraFloorMargin float64
  RevfloorBaselineThresh int
  RevfloorMinAbs int
  RevfloorBaselineScale float64
  RevfloorMinAbsScale float64
  DiscHarddropDaysNoBase int
  DiscHarddropCapFrac float64
  DiscHarddropCushion int
  DiscRequireExplorer bool
  DiscAfterExplorerDays int
  OutrateFloorFactorLow float64
  ExplorerSkipCooldownDown bool
  CircuitBreakerDropRatio float64
  CircuitBreakerReduceStep float64
  CircuitBreakerGraceDays int
  ExtremeDrainStreak int
  ExtremeDrainOutMax float64
  ExtremeDrainStepCap float64
  ExtremeDrainMinStepPpm int
  ExtremeDrainTurboStreak int
  ExtremeDrainTurboOutMax float64
  ExtremeDrainTurboStepCap float64
  ExtremeDrainTurboMinStepPpm int
  SinkMinMargin int
  MinSoftCeiling int
  SeedCeilingMult float64
  SeedFloorMult float64
  SeedP95Boost float64
  SourceSeedTargetFrac float64
  ProfitProtectOutRatio float64
  ProfitProtectMarginPpm int
  ProfitProtectRelaxHours int
  ProfitProtectRelaxMaxFwds int
  ProfitProtectRelaxMarginPpm int
  ProfitProtectRelaxStepFrac float64
  ProfitProtectRelaxMinStepPpm int
  GlobalNegLockSoften bool
  SoftenMinOutRatio float64
  SoftenRequirePosChanMargin bool
  SoftenMaxDropToPegFrac float64
  HTLCMinAttempts60m int
  HTLCPolicyFailRate float64
  HTLCPolicyMinFails int
  HTLCLiquidityFailRate float64
  HTLCLiquidityMinFails int
  HTLCLiquidityHotBump float64
  HTLCLiquidityHotNoDownOutRatio float64
  HTLCPolicyHotBump float64
  HTLCPolicyHotNoDownMarginPpm int
  HTLCHotStepCapBoost float64
}

type superSourceThresholds struct {
  OutRatioMin float64
  OutAmt1dMult float64
  OutAmt7dMult float64
  MinFwds7d int
  EnterHours int
  ExitHours int
}

var autofeeProfiles = map[string]autofeeProfile{
  "conservative": {
    Name: "conservative",
    StepCap: 0.03,
    LowOutThresh: 0.08,
    LowOutProtectThresh: 0.08,
    HighOutThresh: 0.25,
    SurgeBumpMax: 0.10,
    RunIntervalSec: 8 * 3600,
    CooldownUpSec: 6 * 3600,
    CooldownDownSec: 8 * 3600,
    DiscoveryStepCapDown: 0.10,
    SeedGuardMaxJump: 0.30,
    OutratePegGraceHours: 24,
    ProfitDownMarginMin: 20,
    ProfitDownFwdsMin: 10,
    ProfitDownExtraHours: 4,
    NegMarginSurgeBump: 0.06,
    NegMarginSurgeMinFwds: 6,
    NegMarginSurgeFwdsRatio: 0.25,
    SinkExtraFloorMargin: 0.06,
    RevfloorBaselineThresh: 80,
    RevfloorMinAbs: 160,
    RevfloorBaselineScale: 1.2,
    RevfloorMinAbsScale: 1.1,
    DiscHarddropDaysNoBase: 8,
    DiscHarddropCapFrac: 0.10,
    DiscHarddropCushion: 15,
    DiscRequireExplorer: true,
    DiscAfterExplorerDays: 14,
    OutrateFloorFactorLow: 0.90,
    ExplorerSkipCooldownDown: false,
    CircuitBreakerDropRatio: 0.75,
    CircuitBreakerReduceStep: 0.08,
    CircuitBreakerGraceDays: 10,
    ExtremeDrainStreak: 32,
    ExtremeDrainOutMax: 0.03,
    ExtremeDrainStepCap: 0.10,
    ExtremeDrainMinStepPpm: 10,
    ExtremeDrainTurboStreak: 400,
    ExtremeDrainTurboOutMax: 0.01,
    ExtremeDrainTurboStepCap: 0.15,
    ExtremeDrainTurboMinStepPpm: 15,
    SinkMinMargin: 180,
    MinSoftCeiling: 150,
    SeedCeilingMult: 1.40,
    SeedFloorMult: 1.10,
    SeedP95Boost: 1.10,
    SourceSeedTargetFrac: 0.50,
    ProfitProtectOutRatio: 0.08,
    ProfitProtectMarginPpm: 0,
    ProfitProtectRelaxHours: 96,
    ProfitProtectRelaxMaxFwds: 0,
    ProfitProtectRelaxMarginPpm: -10,
    ProfitProtectRelaxStepFrac: 0.010,
    ProfitProtectRelaxMinStepPpm: 10,
    GlobalNegLockSoften: true,
    SoftenMinOutRatio: 0.55,
    SoftenRequirePosChanMargin: true,
    SoftenMaxDropToPegFrac: 0.98,
    HTLCMinAttempts60m: 20,
    HTLCPolicyFailRate: 0.20,
    HTLCPolicyMinFails: 4,
    HTLCLiquidityFailRate: 0.30,
    HTLCLiquidityMinFails: 5,
    HTLCLiquidityHotBump: 0.03,
    HTLCLiquidityHotNoDownOutRatio: 0.08,
    HTLCPolicyHotBump: 0.015,
    HTLCPolicyHotNoDownMarginPpm: 0,
    HTLCHotStepCapBoost: 0.01,
  },
  "moderate": {
    Name: "moderate",
    StepCap: 0.05,
    LowOutThresh: 0.10,
    LowOutProtectThresh: 0.10,
    HighOutThresh: 0.20,
    SurgeBumpMax: 0.20,
    RunIntervalSec: 4 * 3600,
    CooldownUpSec: 3 * 3600,
    CooldownDownSec: 4 * 3600,
    DiscoveryStepCapDown: 0.15,
    SeedGuardMaxJump: 0.50,
    OutratePegGraceHours: 16,
    ProfitDownMarginMin: 10,
    ProfitDownFwdsMin: 8,
    ProfitDownExtraHours: 2,
    NegMarginSurgeBump: 0.08,
    NegMarginSurgeMinFwds: 4,
    NegMarginSurgeFwdsRatio: 0.20,
    SinkExtraFloorMargin: 0.04,
    RevfloorBaselineThresh: 60,
    RevfloorMinAbs: 140,
    RevfloorBaselineScale: 1.0,
    RevfloorMinAbsScale: 1.0,
    DiscHarddropDaysNoBase: 6,
    DiscHarddropCapFrac: 0.20,
    DiscHarddropCushion: 10,
    DiscRequireExplorer: true,
    DiscAfterExplorerDays: 10,
    OutrateFloorFactorLow: 0.85,
    ExplorerSkipCooldownDown: true,
    CircuitBreakerDropRatio: 0.70,
    CircuitBreakerReduceStep: 0.10,
    CircuitBreakerGraceDays: 7,
    ExtremeDrainStreak: 24,
    ExtremeDrainOutMax: 0.04,
    ExtremeDrainStepCap: 0.12,
    ExtremeDrainMinStepPpm: 12,
    ExtremeDrainTurboStreak: 300,
    ExtremeDrainTurboOutMax: 0.01,
    ExtremeDrainTurboStepCap: 0.20,
    ExtremeDrainTurboMinStepPpm: 20,
    SinkMinMargin: 150,
    MinSoftCeiling: 100,
    SeedCeilingMult: 1.50,
    SeedFloorMult: 1.10,
    SeedP95Boost: 1.15,
    SourceSeedTargetFrac: 0.55,
    ProfitProtectOutRatio: 0.10,
    ProfitProtectMarginPpm: 0,
    ProfitProtectRelaxHours: 72,
    ProfitProtectRelaxMaxFwds: 1,
    ProfitProtectRelaxMarginPpm: -30,
    ProfitProtectRelaxStepFrac: 0.015,
    ProfitProtectRelaxMinStepPpm: 15,
    GlobalNegLockSoften: true,
    SoftenMinOutRatio: 0.45,
    SoftenRequirePosChanMargin: true,
    SoftenMaxDropToPegFrac: 0.95,
    HTLCMinAttempts60m: 12,
    HTLCPolicyFailRate: 0.15,
    HTLCPolicyMinFails: 3,
    HTLCLiquidityFailRate: 0.25,
    HTLCLiquidityMinFails: 4,
    HTLCLiquidityHotBump: 0.05,
    HTLCLiquidityHotNoDownOutRatio: 0.12,
    HTLCPolicyHotBump: 0.025,
    HTLCPolicyHotNoDownMarginPpm: 25,
    HTLCHotStepCapBoost: 0.02,
  },
  "aggressive": {
    Name: "aggressive",
    StepCap: 0.08,
    LowOutThresh: 0.12,
    LowOutProtectThresh: 0.12,
    HighOutThresh: 0.18,
    SurgeBumpMax: 0.30,
    RunIntervalSec: 2 * 3600,
    CooldownUpSec: 1 * 3600,
    CooldownDownSec: 2 * 3600,
    DiscoveryStepCapDown: 0.20,
    SeedGuardMaxJump: 0.70,
    OutratePegGraceHours: 8,
    ProfitDownMarginMin: 5,
    ProfitDownFwdsMin: 5,
    ProfitDownExtraHours: 1,
    NegMarginSurgeBump: 0.12,
    NegMarginSurgeMinFwds: 3,
    NegMarginSurgeFwdsRatio: 0.15,
    SinkExtraFloorMargin: 0.03,
    RevfloorBaselineThresh: 40,
    RevfloorMinAbs: 120,
    RevfloorBaselineScale: 0.8,
    RevfloorMinAbsScale: 0.9,
    DiscHarddropDaysNoBase: 3,
    DiscHarddropCapFrac: 0.25,
    DiscHarddropCushion: 5,
    DiscRequireExplorer: false,
    DiscAfterExplorerDays: 5,
    OutrateFloorFactorLow: 0.80,
    ExplorerSkipCooldownDown: true,
    CircuitBreakerDropRatio: 0.60,
    CircuitBreakerReduceStep: 0.15,
    CircuitBreakerGraceDays: 5,
    ExtremeDrainStreak: 16,
    ExtremeDrainOutMax: 0.05,
    ExtremeDrainStepCap: 0.15,
    ExtremeDrainMinStepPpm: 15,
    ExtremeDrainTurboStreak: 300,
    ExtremeDrainTurboOutMax: 0.01,
    ExtremeDrainTurboStepCap: 0.20,
    ExtremeDrainTurboMinStepPpm: 20,
    SinkMinMargin: 120,
    MinSoftCeiling: 80,
    SeedCeilingMult: 1.60,
    SeedFloorMult: 1.10,
    SeedP95Boost: 1.20,
    SourceSeedTargetFrac: 0.60,
    ProfitProtectOutRatio: 0.12,
    ProfitProtectMarginPpm: -20,
    ProfitProtectRelaxHours: 48,
    ProfitProtectRelaxMaxFwds: 2,
    ProfitProtectRelaxMarginPpm: -50,
    ProfitProtectRelaxStepFrac: 0.020,
    ProfitProtectRelaxMinStepPpm: 20,
    GlobalNegLockSoften: true,
    SoftenMinOutRatio: 0.40,
    SoftenRequirePosChanMargin: false,
    SoftenMaxDropToPegFrac: 0.90,
    HTLCMinAttempts60m: 8,
    HTLCPolicyFailRate: 0.10,
    HTLCPolicyMinFails: 2,
    HTLCLiquidityFailRate: 0.20,
    HTLCLiquidityMinFails: 3,
    HTLCLiquidityHotBump: 0.07,
    HTLCLiquidityHotNoDownOutRatio: 0.18,
    HTLCPolicyHotBump: 0.035,
    HTLCPolicyHotNoDownMarginPpm: 50,
    HTLCHotStepCapBoost: 0.03,
  },
}

const (
  outratePegHeadroom = 1.05
  outratePegSeedMult = 1.10
  outrateFloorFactor = 1.00
  outrateFloorMinFwds = 4
  outrateFloorDisableBelowFwds = 5
  outrateFloorLowFwds = 10
)

var superSourceThresholdsByProfile = map[string]superSourceThresholds{
  "conservative": {
    OutRatioMin: 0.65,
    OutAmt1dMult: 0.70,
    OutAmt7dMult: 5.0,
    MinFwds7d: 15,
    EnterHours: 6,
    ExitHours: 96,
  },
  "moderate": {
    OutRatioMin: 0.60,
    OutAmt1dMult: 0.50,
    OutAmt7dMult: 4.0,
    MinFwds7d: 10,
    EnterHours: 0,
    ExitHours: 72,
  },
  "aggressive": {
    OutRatioMin: 0.55,
    OutAmt1dMult: 0.35,
    OutAmt7dMult: 3.0,
    MinFwds7d: 7,
    EnterHours: 0,
    ExitHours: 48,
  },
}

type AutofeeService struct {
  db *pgxpool.Pool
  lnd *lndclient.Client
  notifier *Notifier
  htlcFailedProvider htlcFailedProvider
  logger loggerLike

  mu sync.Mutex
  started bool
  running bool
  stop chan struct{}
  wake chan struct{}
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
  Line string
  Payload *autofeeLogItem
}

func NewAutofeeService(db *pgxpool.Pool, lnd *lndclient.Client, notifier *Notifier, htlcProvider htlcFailedProvider, logger loggerLike) *AutofeeService {
  return &AutofeeService{
    db: db,
    lnd: lnd,
    notifier: notifier,
    htlcFailedProvider: htlcProvider,
    logger: logger,
  }
}

func (s *AutofeeService) lastRunFromLogs(ctx context.Context) (time.Time, bool) {
  var ts pgtype.Timestamptz
  err := s.db.QueryRow(ctx, `
select max(occurred_at)
from autofee_logs
where seq = 0
  and coalesce(payload->>'dry_run', 'false') <> 'true'
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
  profile text not null default 'moderate',
  lookback_days integer not null default 7,
  run_interval_sec integer not null default 14400,
  cooldown_up_sec integer not null default 10800,
  cooldown_down_sec integer not null default 14400,
  rebal_cost_mode text not null default 'blend',
  amboss_enabled boolean not null default false,
  amboss_token text,
  inbound_passive_enabled boolean not null default false,
  discovery_enabled boolean not null default true,
  explorer_enabled boolean not null default true,
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
  last_outrate_ppm integer,
  last_outrate_ts timestamptz,
  last_rebal_cost_ppm integer,
  last_rebal_cost_ts timestamptz,
  last_ts timestamptz,
  last_dir text,
  low_streak integer not null default 0,
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

alter table autofee_config add column if not exists super_source_enabled boolean not null default false;
alter table autofee_config add column if not exists super_source_base_fee_msat integer not null default 1000;
alter table autofee_config add column if not exists revfloor_enabled boolean not null default true;
alter table autofee_config add column if not exists circuit_breaker_enabled boolean not null default true;
alter table autofee_config add column if not exists extreme_drain_enabled boolean not null default true;
alter table autofee_config add column if not exists htlc_signal_enabled boolean not null default true;
alter table autofee_config add column if not exists htlc_mode text not null default 'full';
alter table autofee_config add column if not exists rebal_cost_mode text not null default 'blend';
alter table autofee_state add column if not exists ss_active boolean;
alter table autofee_state add column if not exists ss_ok_since timestamptz;
alter table autofee_state add column if not exists ss_bad_since timestamptz;
alter table autofee_state add column if not exists last_dir text;
alter table autofee_logs add column if not exists payload jsonb;
`)
  if err != nil {
    return err
  }

  _, err = s.db.Exec(ctx, `
insert into autofee_config (id)
values ($1)
on conflict (id) do nothing
`, autofeeConfigID)
  return err
}

func (s *AutofeeService) defaultConfig() AutofeeConfig {
  p := autofeeProfiles["moderate"]
  return AutofeeConfig{
    Enabled: false,
    Profile: p.Name,
    LookbackDays: 7,
    RunIntervalSec: p.RunIntervalSec,
    CooldownUpSec: p.CooldownUpSec,
    CooldownDownSec: p.CooldownDownSec,
    RebalCostMode: rebalCostModeDefault,
    AmbossEnabled: false,
    AmbossTokenSet: false,
    InboundPassiveEnabled: false,
    DiscoveryEnabled: true,
    ExplorerEnabled: true,
    SuperSourceEnabled: false,
    SuperSourceBaseFeeMsat: superSourceBaseFeeMsatDefault,
    RevfloorEnabled: true,
    CircuitBreakerEnabled: true,
    ExtremeDrainEnabled: true,
    HTLCSignalEnabled: true,
    HTLCMode: htlcModeDefault,
    MinPpm: 10,
    MaxPpm: 2000,
  }
}

func (s *AutofeeService) GetConfig(ctx context.Context) (AutofeeConfig, error) {
  cfg := s.defaultConfig()
  if s.db == nil {
    return cfg, errors.New("db unavailable")
  }

  var ambossToken pgtype.Text
  err := s.db.QueryRow(ctx, `
select enabled, profile, lookback_days, run_interval_sec, cooldown_up_sec, cooldown_down_sec,
  rebal_cost_mode, amboss_enabled, amboss_token, inbound_passive_enabled, discovery_enabled, explorer_enabled,
  super_source_enabled, super_source_base_fee_msat, revfloor_enabled, circuit_breaker_enabled, extreme_drain_enabled,
  htlc_signal_enabled, htlc_mode, min_ppm, max_ppm
from autofee_config where id=$1
`, autofeeConfigID).Scan(
    &cfg.Enabled,
    &cfg.Profile,
    &cfg.LookbackDays,
    &cfg.RunIntervalSec,
    &cfg.CooldownUpSec,
    &cfg.CooldownDownSec,
    &cfg.RebalCostMode,
    &cfg.AmbossEnabled,
    &ambossToken,
    &cfg.InboundPassiveEnabled,
    &cfg.DiscoveryEnabled,
    &cfg.ExplorerEnabled,
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
      return cfg, nil
    }
    return cfg, err
  }
  cfg.AmbossTokenSet = ambossToken.Valid && strings.TrimSpace(ambossToken.String) != ""
  if cfg.Profile == "" {
    cfg.Profile = "moderate"
  }
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
  return cfg, nil
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
  if req.RebalCostMode != nil {
    current.RebalCostMode = normalizeRebalCostMode(*req.RebalCostMode)
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

  if current.RunIntervalSec < 3600 {
    current.RunIntervalSec = 3600
  }
  if current.RunIntervalSec > 86400 {
    current.RunIntervalSec = 86400
  }
  if current.CooldownUpSec < 3600 {
    current.CooldownUpSec = 3600
  }
  if current.CooldownDownSec < 7200 {
    current.CooldownDownSec = 7200
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
  profile=$3,
  lookback_days=$4,
  run_interval_sec=$5,
  cooldown_up_sec=$6,
  cooldown_down_sec=$7,
  rebal_cost_mode=$8,
  amboss_enabled=$9,
  amboss_token=$10,
  inbound_passive_enabled=$11,
  discovery_enabled=$12,
  explorer_enabled=$13,
  super_source_enabled=$14,
  super_source_base_fee_msat=$15,
  revfloor_enabled=$16,
  circuit_breaker_enabled=$17,
  extreme_drain_enabled=$18,
  htlc_signal_enabled=$19,
  htlc_mode=$20,
  min_ppm=$21,
  max_ppm=$22,
  updated_at=now()
where id=$1
`, autofeeConfigID,
    current.Enabled,
    current.Profile,
    current.LookbackDays,
    current.RunIntervalSec,
    current.CooldownUpSec,
    current.CooldownDownSec,
    current.RebalCostMode,
    current.AmbossEnabled,
    ambossToken,
    current.InboundPassiveEnabled,
    current.DiscoveryEnabled,
    current.ExplorerEnabled,
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
    return current, err
  }
  if previousRebalMode != current.RebalCostMode {
    _, resetErr := s.db.Exec(ctx, `
update autofee_state
set last_rebal_cost_ppm = null,
  last_rebal_cost_ts = null
`)
    if resetErr != nil {
      return current, resetErr
    }
  }
  current.AmbossTokenSet = strings.TrimSpace(ambossToken) != ""
  s.nudgeScheduler()
  return current, nil
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
    Running: s.running,
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

func (s *AutofeeService) Start() {
  s.mu.Lock()
  if s.started {
    s.mu.Unlock()
    return
  }
  s.started = true
  s.stop = make(chan struct{})
  s.wake = make(chan struct{}, 1)
  s.mu.Unlock()

  go s.loop()
}

func (s *AutofeeService) Stop() {
  s.mu.Lock()
  if s.stop != nil {
    close(s.stop)
    s.stop = nil
  }
  s.mu.Unlock()
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
        header := fmt.Sprintf("⚡ Autofee %s | %s", strings.ToUpper(reason), now.Format(time.RFC3339))
        if dryRun {
          header = header + " (dry-run)"
        }
        entries := []autofeeLogEntry{
          {Line: header, Payload: &autofeeLogItem{Kind: "header", Reason: reason, DryRun: dryRun, Timestamp: now.Format(time.RFC3339)}},
          {Line: "📊 up 0 | down 0 | flat 0 | cooldown 0 | small 0 | same 0 | disabled 0 | inactive 0 | inb_disc 0 | super_source 0", Payload: &autofeeLogItem{
            Kind: "summary",
            Up: 0, Down: 0, Flat: 0, Cooldown: 0, Small: 0, Same: 0, Disabled: 0, Inactive: 0, InboundDisc: 0, SuperSource: 0,
          }},
          {Line: "🌱 seed amboss=0 missing=0 err=0 empty=0 outrate=0 mem=0 default=0", Payload: &autofeeLogItem{
            Kind: "seed",
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
  svc *AutofeeService
  cfg AutofeeConfig
  profile autofeeProfile
  superSource superSourceThresholds
  ignoreCooldown bool
  calib autofeeCalibration
  now time.Time
}

type autofeeCalibration struct {
  RevfloorBaseline int
  RevfloorMinAbs int
  NodeClass string
  LiquidityClass string
  ChannelCount int
  TotalCapacitySat int64
  AvgCapacitySat int64
  LocalCapacitySat int64
  LocalRatio float64
  HTLCNodeFactor float64
  HTLCLiquidityFactor float64
  HTLCThresholdFactor float64
  HTLCMinAttempts int
  HTLCMinPolicyFails int
  HTLCMinLiquidityFails int
  HTLCWindowMin int
}

type autofeeRunSummary struct {
  total int
  inactive int
  disabled int
  eligible int
  applied int
  applyErrors int
  changedUp int
  changedDown int
  kept int
  skippedCooldown int
  skippedSmall int
  skippedSame int
  skippedOther int
  seedAmboss int
  seedAmbossMissing int
  seedAmbossError int
  seedAmbossEmpty int
  seedOutrate int
  seedMem int
  seedDefault int
  superSource int
  inboundDiscount int
  htlcLiqHot int
  htlcPolicyHot int
  htlcSampleLow int
  htlcWindowMin int
  htlcMinAttempts int
  htlcMinPolicyFails int
  htlcMinLiquidityFails int
  htlcNodeFactor float64
  htlcLiquidityFactor float64
  htlcThresholdFactor float64
}

func (s *autofeeRunSummary) addTags(tags []string) {
  for _, tag := range tags {
    switch tag {
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
    case "htlc-liquidity-hot":
      s.htlcLiqHot++
    case "htlc-policy-hot":
      s.htlcPolicyHot++
    case "htlc-sample-low":
      s.htlcSampleLow++
    }
  }
}

func newAutofeeEngine(svc *AutofeeService, cfg AutofeeConfig) *autofeeEngine {
  p := autofeeProfiles[cfg.Profile]
  if p.Name == "" {
    p = autofeeProfiles["moderate"]
  }
  ss := superSourceThresholdsByProfile[p.Name]
  if ss.OutRatioMin == 0 {
    ss = superSourceThresholdsByProfile["moderate"]
  }
  return &autofeeEngine{
    svc: svc,
    cfg: cfg,
    profile: p,
    superSource: ss,
    now: time.Now().UTC(),
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

  e.calib.RevfloorBaseline = revfloorBaseline
  e.calib.RevfloorMinAbs = revfloorMinAbs
  e.calib.NodeClass = nodeClass
  e.calib.LiquidityClass = liquidityClass
  e.calib.ChannelCount = chanCount
  e.calib.TotalCapacitySat = totalCap
  e.calib.LocalCapacitySat = totalLocal
  e.calib.AvgCapacitySat = avgCap
  e.calib.LocalRatio = localRatio
}

type autofeeChannelState struct {
  ChannelID uint64
  LastPpm int
  LastInboundDiscount int
  LastSeed int
  LastOutrate int
  LastOutrateTs time.Time
  LastRebalCost int
  LastRebalCostTs time.Time
  LastTs time.Time
  LastDir string
  LowStreak int
  BaselineFwd7d int
  ClassLabel string
  ClassConf float64
  BiasEma float64
  FirstSeen time.Time
  SuperSourceActive bool
  SuperSourceOkSince time.Time
  SuperSourceBadSince time.Time
  ExplorerState explorerState
}

type explorerState struct {
  Active bool `json:"active"`
  StartedTs int64 `json:"started_ts"`
  Rounds int `json:"rounds"`
  FwdsAtStart int `json:"fwds_at_start"`
  LastExitTs int64 `json:"last_exit_ts"`
  Seen bool `json:"seen"`
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

  forwardStats, err := e.fetchForwardStats(ctx, e.cfg.LookbackDays)
  if err != nil {
    return err
  }
  forwardStats1d, err := e.fetchForwardStats(ctx, 1)
  if err != nil {
    return err
  }
  forwardStats7d := forwardStats
  if e.cfg.LookbackDays != 7 {
    if stats7d, err := e.fetchForwardStats(ctx, 7); err == nil {
      forwardStats7d = stats7d
    }
  }
  inboundStats, err := e.fetchInboundStats(ctx, e.cfg.LookbackDays)
  if err != nil {
    return err
  }
  rebalStats, err := e.fetchRebalanceStats(ctx, e.cfg.LookbackDays)
  if err != nil {
    return err
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
  header := fmt.Sprintf("⚡ Autofee %s | %s", strings.ToUpper(reason), e.now.UTC().Format(time.RFC3339))
  if dryRun {
    header = header + " (dry-run)"
  }
  summary := autofeeRunSummary{total: len(channels)}
  summary.htlcWindowMin = htlcMeta.WindowMin
  summary.htlcMinAttempts = htlcMeta.MinAttempts
  summary.htlcMinPolicyFails = htlcMeta.MinPolicyFails
  summary.htlcMinLiquidityFails = htlcMeta.MinLiquidityFails
  summary.htlcNodeFactor = htlcMeta.NodeFactor
  summary.htlcLiquidityFactor = htlcMeta.LiquidityFactor
  summary.htlcThresholdFactor = htlcMeta.ThresholdFactor
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
    enabled, ok := settings[ch.ChannelID]
    if ok && !enabled {
      summary.disabled++
      continue
    }

    st := state[ch.ChannelID]
    decision := e.evaluateChannel(ch, st, forwardStats, forwardStats1d, forwardStats7d, inboundStats, rebalStats, htlcSignals, totalOutFeeMsat, rebalGlobalPpm, negMarginGlobal)
    if decision == nil {
      continue
    }
    if decision.Alias == "" {
      decision.Alias = strings.TrimSpace(ch.RemotePubkey)
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
        continue
      }
      changedLines = append(changedLines, buildAutofeeChannelLogEntry(decision, "changed", dryRun, nil))
    } else {
      if decision.NewPpm == decision.LocalPpm {
        summary.kept++
      }
      cat := "kept"
      if containsTag(decision.Tags, "cooldown") || containsTag(decision.Tags, "hold-small") {
        cat = "skipped"
      }
      entry := buildAutofeeChannelLogEntry(decision, cat, dryRun, nil)
      if cat == "skipped" {
        skippedLines = append(skippedLines, entry)
      } else {
        keptLines = append(keptLines, entry)
      }
      if containsTag(decision.Tags, "explorer") {
        explorerLines = append(explorerLines, autofeeLogEntry{
          Line: fmt.Sprintf("🧭 %s explorer: ON", decision.Alias),
          Payload: &autofeeLogItem{
            Kind: "explorer",
            Category: "explorer",
            Alias: decision.Alias,
          },
        })
      }
    }
  }

  summaryText := fmt.Sprintf(
    "📊 up %d | down %d | flat %d | cooldown %d | small %d | same %d | disabled %d | inactive %d | inb_disc %d | super_source %d | htlc_liq_hot %d | htlc_policy_hot %d | htlc_low_sample %d | htlc_window %dm | htlc_min a>=%d p>=%d l>=%d",
    summary.changedUp, summary.changedDown, summary.kept,
    summary.skippedCooldown, summary.skippedSmall, summary.skippedSame,
    summary.disabled, summary.inactive, summary.inboundDiscount, summary.superSource,
    summary.htlcLiqHot, summary.htlcPolicyHot, summary.htlcSampleLow, summary.htlcWindowMin,
    summary.htlcMinAttempts, summary.htlcMinPolicyFails, summary.htlcMinLiquidityFails,
  )
  seedText := fmt.Sprintf(
    "🌱 seed amboss=%d missing=%d err=%d empty=%d outrate=%d mem=%d default=%d",
    summary.seedAmboss, summary.seedAmbossMissing, summary.seedAmbossError, summary.seedAmbossEmpty,
    summary.seedOutrate, summary.seedMem, summary.seedDefault,
  )
  if e.ignoreCooldown {
    seedText = seedText + " | cooldown_ignored=1"
  }

  entries := []autofeeLogEntry{
    {Line: header, Payload: &autofeeLogItem{Kind: "header", Reason: reason, DryRun: dryRun, Timestamp: e.now.UTC().Format(time.RFC3339)}},
    {Line: summaryText, Payload: &autofeeLogItem{
      Kind: "summary",
      Up: summary.changedUp,
      Down: summary.changedDown,
      Flat: summary.kept,
      Cooldown: summary.skippedCooldown,
      Small: summary.skippedSmall,
      Same: summary.skippedSame,
      Disabled: summary.disabled,
      Inactive: summary.inactive,
      InboundDisc: summary.inboundDiscount,
      SuperSource: summary.superSource,
      HTLCLiqHot: summary.htlcLiqHot,
      HTLCPolicyHot: summary.htlcPolicyHot,
      HTLCSampleLow: summary.htlcSampleLow,
      HTLCWindowMin: summary.htlcWindowMin,
      HTLCMinAttempts: summary.htlcMinAttempts,
      HTLCMinPolicyFails: summary.htlcMinPolicyFails,
      HTLCMinLiquidityFails: summary.htlcMinLiquidityFails,
      HTLCNodeFactor: summary.htlcNodeFactor,
      HTLCLiquidityFactor: summary.htlcLiquidityFactor,
      HTLCThresholdFactor: summary.htlcThresholdFactor,
    }},
    {Line: seedText, Payload: &autofeeLogItem{
      Kind: "seed",
      Amboss: summary.seedAmboss,
      Missing: summary.seedAmbossMissing,
      Err: summary.seedAmbossError,
      Empty: summary.seedAmbossEmpty,
      Outrate: summary.seedOutrate,
      Mem: summary.seedMem,
      Default: summary.seedDefault,
      CooldownIgnored: e.ignoreCooldown,
    }},
  }
  calibLine := fmt.Sprintf("⚙️ calib node=%s channels=%d cap=%d avg=%d local=%d (%.0f%%) revfloor_thr=%d revfloor_min=%d liq=%s | htlc_k node=%.2f liq=%.2f total=%.2f",
    e.calib.NodeClass, e.calib.ChannelCount, e.calib.TotalCapacitySat, e.calib.AvgCapacitySat,
    e.calib.LocalCapacitySat, e.calib.LocalRatio*100, e.calib.RevfloorBaseline, e.calib.RevfloorMinAbs, e.calib.LiquidityClass,
    e.calib.HTLCNodeFactor, e.calib.HTLCLiquidityFactor, e.calib.HTLCThresholdFactor,
  )
  entries = append(entries, autofeeLogEntry{
    Line: calibLine,
    Payload: &autofeeLogItem{
      Kind: "calib",
      NodeClass: e.calib.NodeClass,
      LiquidityClass: e.calib.LiquidityClass,
      ChannelCount: e.calib.ChannelCount,
      TotalCapacitySat: e.calib.TotalCapacitySat,
      AvgCapacitySat: e.calib.AvgCapacitySat,
      LocalCapacitySat: e.calib.LocalCapacitySat,
      LocalRatio: e.calib.LocalRatio,
      RevfloorBaseline: e.calib.RevfloorBaseline,
      RevfloorMinAbs: e.calib.RevfloorMinAbs,
      HTLCWindowMin: e.calib.HTLCWindowMin,
      HTLCMinAttempts: e.calib.HTLCMinAttempts,
      HTLCMinPolicyFails: e.calib.HTLCMinPolicyFails,
      HTLCMinLiquidityFails: e.calib.HTLCMinLiquidityFails,
      HTLCNodeFactor: e.calib.HTLCNodeFactor,
      HTLCLiquidityFactor: e.calib.HTLCLiquidityFactor,
      HTLCThresholdFactor: e.calib.HTLCThresholdFactor,
    },
  })

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
  return nil
}

type forwardStat struct {
  FeeMsat int64
  AmtMsat int64
  Count int64
}

type inboundStat struct {
  AmtMsat int64
  Count int64
}

type rebalStat struct {
  FeeMsat int64
  AmtMsat int64
}

type rebalStats struct {
  ByChannel map[uint64]rebalStat
  Global rebalStat
}

type htlcFailureSignal struct {
  Attempts60m int
  PolicyFails60m int
  LiquidityFails60m int
  PolicyFailRate60m float64
  LiquidityFailRate60m float64
  WindowMin int
  SampleLow bool
  PolicyHot bool
  LiquidityHot bool
}

type htlcSignalMeta struct {
  WindowMin int
  MinAttempts int
  MinPolicyFails int
  MinLiquidityFails int
  NodeFactor float64
  LiquidityFactor float64
  ThresholdFactor float64
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
  meta := htlcSignalMeta{
    WindowMin: windowMin,
    MinAttempts: minAttempts,
    MinPolicyFails: minPolicyFails,
    MinLiquidityFails: minLiquidityFails,
    NodeFactor: nodeFactor,
    LiquidityFactor: liquidityFactor,
    ThresholdFactor: thresholdFactor,
  }
  e.calib.HTLCWindowMin = meta.WindowMin
  e.calib.HTLCMinAttempts = meta.MinAttempts
  e.calib.HTLCMinPolicyFails = meta.MinPolicyFails
  e.calib.HTLCMinLiquidityFails = meta.MinLiquidityFails
  e.calib.HTLCNodeFactor = meta.NodeFactor
  e.calib.HTLCLiquidityFactor = meta.LiquidityFactor
  e.calib.HTLCThresholdFactor = meta.ThresholdFactor
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
  policyFails := map[uint64]int{}
  liquidityFails := map[uint64]int{}
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
    policy, liquidity := classifyHTLCFailure(entry)
    if policy {
      policyFails[chanID]++
    }
    if liquidity {
      liquidityFails[chanID]++
    }
  }

  for _, ch := range channels {
    if ch.ChannelID == 0 {
      continue
    }
    total := attempts[ch.ChannelID]
    if total == 0 {
      continue
    }
    polFails := policyFails[ch.ChannelID]
    liqFails := liquidityFails[ch.ChannelID]
    polRate := float64(polFails) / float64(total)
    liqRate := float64(liqFails) / float64(total)
    sampleLow := total < minAttempts
    policyHot := !sampleLow &&
      polFails >= minPolicyFails &&
      polRate >= e.profile.HTLCPolicyFailRate
    liquidityHot := !sampleLow &&
      liqFails >= minLiquidityFails &&
      liqRate >= e.profile.HTLCLiquidityFailRate
    signals[ch.ChannelID] = htlcFailureSignal{
      Attempts60m: total,
      PolicyFails60m: polFails,
      LiquidityFails60m: liqFails,
      PolicyFailRate60m: polRate,
      LiquidityFailRate60m: liqRate,
      WindowMin: windowMin,
      SampleLow: sampleLow,
      PolicyHot: policyHot,
      LiquidityHot: liquidityHot,
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
  policy := containsAnyFailureToken(blob, []string{
    "FEE INSUFFICIENT",
    "INCORRECT CLTV EXPIRY",
  })
  liquidity := containsAnyFailureToken(blob, []string{
    "TEMPORARY CHANNEL FAILURE",
    "CHANNEL DISABLED",
    "UNKNOWN NEXT PEER",
  })
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
select coalesce(rebal_target_chan_id, rebal_source_chan_id) as chan_id,
  coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0),
  coalesce(sum(amount_sat), 0)
from notifications
where type='rebalance' and occurred_at >= now() - ($1 * interval '1 day')
  and (rebal_target_chan_id is not null or rebal_source_chan_id is not null)
group by coalesce(rebal_target_chan_id, rebal_source_chan_id)
`, lookback)
  if err != nil {
    return stats, err
  }
  defer rows.Close()
  for rows.Next() {
    var chanID int64
    var feeMsat int64
    var amtSat int64
    if err := rows.Scan(&chanID, &feeMsat, &amtSat); err != nil {
      return stats, err
    }
    stats.ByChannel[uint64(chanID)] = rebalStat{FeeMsat: feeMsat, AmtMsat: amtSat * 1000}
  }
  if err := rows.Err(); err != nil {
    return stats, err
  }

  err = e.svc.db.QueryRow(ctx, `
select coalesce(sum(case when fee_msat > 0 then fee_msat else fee_sat * 1000 end), 0),
  coalesce(sum(amount_sat), 0)
from notifications
where type='rebalance' and occurred_at >= now() - ($1 * interval '1 day')
`, lookback).Scan(&stats.Global.FeeMsat, &stats.Global.AmtMsat)
  if err != nil && !errors.Is(err, pgx.ErrNoRows) {
    return stats, err
  }
  stats.Global.AmtMsat = stats.Global.AmtMsat * 1000
  return stats, nil
}

func (e *autofeeEngine) loadState(ctx context.Context) (map[uint64]*autofeeChannelState, error) {
  items := map[uint64]*autofeeChannelState{}
  rows, err := e.svc.db.Query(ctx, `
select channel_id, last_ppm, last_inbound_discount_ppm, last_seed_ppm, last_outrate_ppm, last_outrate_ts,
  last_rebal_cost_ppm, last_rebal_cost_ts, last_ts, last_dir, low_streak, baseline_fwd7d, class_label, class_conf, bias_ema,
  first_seen_ts, ss_active, ss_ok_since, ss_bad_since, explorer_state
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
    var lastOut pgtype.Int4
    var lastOutTs pgtype.Timestamptz
    var lastRebal pgtype.Int4
    var lastRebalTs pgtype.Timestamptz
    var lastTs pgtype.Timestamptz
    var lastDir pgtype.Text
    var lowStreak int
    var baseline int
    var classLabel pgtype.Text
    var classConf pgtype.Float8
    var biasEma pgtype.Float8
    var firstSeen pgtype.Timestamptz
    var ssActive pgtype.Bool
    var ssOkSince pgtype.Timestamptz
    var ssBadSince pgtype.Timestamptz
    var explorerRaw []byte
    if err := rows.Scan(&channelID, &lastPpm, &lastInb, &lastSeed, &lastOut, &lastOutTs, &lastRebal, &lastRebalTs, &lastTs, &lastDir,
      &lowStreak, &baseline, &classLabel, &classConf, &biasEma, &firstSeen, &ssActive, &ssOkSince, &ssBadSince, &explorerRaw); err != nil {
      return items, err
    }
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
    if lastTs.Valid {
      st.LastTs = lastTs.Time
    }
    if lastDir.Valid {
      st.LastDir = lastDir.String
    }
    st.LowStreak = lowStreak
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

func (e *autofeeEngine) persistState(ctx context.Context, st *autofeeChannelState) {
  if st == nil {
    return
  }
  rawExplorer, _ := json.Marshal(st.ExplorerState)
  _, _ = e.svc.db.Exec(ctx, `
insert into autofee_state (
  channel_id, last_ppm, last_inbound_discount_ppm, last_seed_ppm, last_outrate_ppm, last_outrate_ts,
  last_rebal_cost_ppm, last_rebal_cost_ts, last_ts, last_dir, low_streak, baseline_fwd7d, class_label, class_conf, bias_ema,
  first_seen_ts, ss_active, ss_ok_since, ss_bad_since, explorer_state
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
`+
`on conflict (channel_id) do update set
  last_ppm=excluded.last_ppm,
  last_inbound_discount_ppm=excluded.last_inbound_discount_ppm,
  last_seed_ppm=excluded.last_seed_ppm,
  last_outrate_ppm=excluded.last_outrate_ppm,
  last_outrate_ts=excluded.last_outrate_ts,
  last_rebal_cost_ppm=excluded.last_rebal_cost_ppm,
  last_rebal_cost_ts=excluded.last_rebal_cost_ts,
  last_ts=excluded.last_ts,
  last_dir=excluded.last_dir,
  low_streak=excluded.low_streak,
  baseline_fwd7d=excluded.baseline_fwd7d,
  class_label=excluded.class_label,
  class_conf=excluded.class_conf,
  bias_ema=excluded.bias_ema,
  first_seen_ts=excluded.first_seen_ts,
  ss_active=excluded.ss_active,
  ss_ok_since=excluded.ss_ok_since,
  ss_bad_since=excluded.ss_bad_since,
  explorer_state=excluded.explorer_state
`, int64(st.ChannelID), nullableInt(int64(st.LastPpm)), nullableInt(int64(st.LastInboundDiscount)),
    nullableInt(int64(st.LastSeed)), nullableInt(int64(st.LastOutrate)), nullableTime(st.LastOutrateTs),
    nullableInt(int64(st.LastRebalCost)), nullableTime(st.LastRebalCostTs), nullableTime(st.LastTs),
    nullableString(st.LastDir), st.LowStreak, st.BaselineFwd7d, nullableString(st.ClassLabel), nullableFloat(st.ClassConf),
    nullableFloat(st.BiasEma), nullableTime(st.FirstSeen), st.SuperSourceActive,
    nullableTime(st.SuperSourceOkSince), nullableTime(st.SuperSourceBadSince), rawExplorer,
  )
}
// ===== decisions =====

type decision struct {
  ChannelID uint64
  ChannelPoint string
  Alias string
  LocalPpm int
  NewPpm int
  Target int
  Floor int
  FloorSrc string
  Tags []string
  InboundDiscount int
  SuperSourceActive bool
  OutRatio float64
  OutPpm7d int
  RebalPpm int
  Seed int
  Margin int
  RevShare float64
  ClassLabel string
  FwdCount int
  NegMarginGlobal bool
  PredictionCode string
  PredictionCooldownHours int
  HTLCAttempts int
  HTLCPolicyFails int
  HTLCLiquidityFails int
  HTLCWindowMin int
  Apply bool
  Error error
  State *autofeeChannelState
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
    if containsTag(d.Tags, "cooldown") || containsTag(d.Tags, "hold-small") {
      category = "skipped"
    }
  }

  deltaStr := ""
  if d.LocalPpm > 0 && d.NewPpm != d.LocalPpm {
    delta := d.NewPpm - d.LocalPpm
    pct := math.Abs(float64(delta)) / float64(d.LocalPpm) * 100.0
    deltaStr = fmt.Sprintf(" (%+d, %.1f%%)", delta, pct)
  }

  floorSrc := ""
  if d.FloorSrc != "" {
    floorSrc = fmt.Sprintf("(%s)", d.FloorSrc)
  }
  tagLine := formatAutofeeTags(d)
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

  line := fmt.Sprintf("%s %s: %s%s | alvo %d | out_ratio %.2f | out_ppm7d≈%d | rebal_ppm7d≈%d | seed≈%d | floor≥%d%s | marg≈%d | rev_share≈%.2f | %s",
    prefix,
    alias,
    action,
    deltaStr,
    d.Target,
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
  if dryRun && d.HTLCAttempts > 0 {
    line = line + fmt.Sprintf(" | htlc%dm a=%d p=%d l=%d",
      maxInt(1, d.HTLCWindowMin), d.HTLCAttempts, d.HTLCPolicyFails, d.HTLCLiquidityFails)
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
    } else if containsTag(d.Tags, "hold-small") {
      skipReason = "hold-small"
    } else if containsTag(d.Tags, "same-ppm") {
      skipReason = "same-ppm"
    }
  }
  payload := &autofeeLogItem{
    Kind: "channel",
    Category: category,
    DryRun: dryRun,
    Alias: d.Alias,
    ChannelID: d.ChannelID,
    ChannelPoint: d.ChannelPoint,
    LocalPpm: d.LocalPpm,
    NewPpm: d.NewPpm,
    Target: d.Target,
    OutRatio: d.OutRatio,
    OutPpm7d: d.OutPpm7d,
    RebalPpm7d: d.RebalPpm,
    Seed: d.Seed,
    Floor: d.Floor,
    FloorSrc: d.FloorSrc,
    Margin: d.Margin,
    RevShare: d.RevShare,
    Tags: append([]string{}, d.Tags...),
    InboundDiscount: d.InboundDiscount,
    ClassLabel: d.ClassLabel,
    SkipReason: skipReason,
    Delta: delta,
    DeltaPct: deltaPct,
    PredictionCode: d.PredictionCode,
    PredictionCooldownHours: d.PredictionCooldownHours,
    HTLCAttempts: d.HTLCAttempts,
    HTLCPolicyFails: d.HTLCPolicyFails,
    HTLCLiquidityFails: d.HTLCLiquidityFails,
    HTLCWindowMinChannel: d.HTLCWindowMin,
  }
  if err != nil {
    payload.Error = err.Error()
  }
  return autofeeLogEntry{Line: line, Payload: payload}
}

// ===== evaluation =====

func (e *autofeeEngine) evaluateChannel(ch lndclient.ChannelInfo, st *autofeeChannelState, forwardStats map[uint64]forwardStat,
  forwardStats1d map[uint64]forwardStat, forwardStats7d map[uint64]forwardStat, inboundStats map[uint64]inboundStat,
  rebalStats rebalStats, htlcSignals map[uint64]htlcFailureSignal, totalOutFeeMsat int64, rebalGlobalPpm int, negMarginGlobal bool) *decision {

  localPpm := 0
  if ch.FeeRatePpm != nil {
    localPpm = int(*ch.FeeRatePpm)
  } else if st != nil && st.LastPpm > 0 {
    localPpm = st.LastPpm
  }

  if localPpm <= 0 {
    localPpm = e.cfg.MinPpm
  }

  outRatio := 0.5
  if ch.CapacitySat > 0 {
    outRatio = float64(ch.LocalBalanceSat) / float64(ch.CapacitySat)
  }

  fwd := forwardStats[ch.ChannelID]
  fwd1d := forwardStats1d[ch.ChannelID]
  fwd7d := forwardStats7d[ch.ChannelID]
  inb := inboundStats[ch.ChannelID]
  outPpm7d := ppmMsat(fwd.FeeMsat, fwd.AmtMsat)
  fwdCount := int(fwd.Count)
  fwdCount7d := int(fwd7d.Count)

  outAmtSat := fwd.AmtMsat / 1000
  inAmtSat := inb.AmtMsat / 1000
  outAmt1dSat := fwd1d.AmtMsat / 1000
  outAmt7dSat := fwd7d.AmtMsat / 1000

  totalVal := outAmtSat + inAmtSat
  biasRaw := 0.0
  if totalVal > 0 {
    biasRaw = float64(outAmtSat-inAmtSat) / float64(totalVal)
  }

  if st == nil {
    st = &autofeeChannelState{ChannelID: ch.ChannelID}
  }
  if st.FirstSeen.IsZero() {
    st.FirstSeen = e.now
  }

  biasEma := biasRaw
  if st.BiasEma != 0 {
    biasEma = (1.0-0.45)*st.BiasEma + 0.45*biasRaw
  }
  st.BiasEma = biasEma

  classLabel, classConf := classifyChannel(biasEma, outRatio, inb.Count, fwd.Count, st.ClassLabel, st.ClassConf)
  st.ClassLabel = classLabel
  st.ClassConf = classConf

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

  seed, _, seedTags := e.seedForChannel(ch.RemotePubkey, st)
  if seed <= 0 {
    seed = 200
  }
  st.LastSeed = int(seed)

  lowOutProtectThresh := e.profile.LowOutProtectThresh
  if lowOutProtectThresh <= 0 {
    lowOutProtectThresh = defaultLowOutProtectThresh
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

  target := int(seed) + 25

  if outRatio < lowOutProtectThresh {
    st.LowStreak++
  } else {
    st.LowStreak = 0
  }
  if st.LowStreak >= 1 {
    bumpAcc := math.Min(0.25, float64(st.LowStreak)*0.05)
    if target <= localPpm {
      target = int(math.Ceil(float64(localPpm) * (1.0 + bumpAcc)))
    } else {
      target = int(math.Ceil(float64(target) * (1.0 + bumpAcc)))
    }
  }

  if outRatio < e.profile.LowOutThresh {
    target = int(math.Ceil(float64(target) * 1.02))
  } else if outRatio > e.profile.HighOutThresh {
    target = int(math.Floor(float64(target) * 0.98))
    if fwdCount == 0 && outRatio > 0.60 {
      target = int(math.Floor(float64(target) * 0.985))
    }
  }

  tags := []string{}
  htlcSampleLow := false
  htlcPolicyHot := false
  htlcLiquidityHot := false
  htlcAttempts := 0
  htlcPolicyFails := 0
  htlcLiquidityFails := 0
  htlcWindowMin := 0
  if superSourceActive {
    tags = append(tags, "super-source")
    if superSourceLike {
      tags = append(tags, "super-source-like")
    }
  }
  if signal, ok := htlcSignals[ch.ChannelID]; ok && signal.Attempts60m > 0 {
    htlcSampleLow = signal.SampleLow
    htlcPolicyHot = signal.PolicyHot
    htlcLiquidityHot = signal.LiquidityHot
    htlcAttempts = signal.Attempts60m
    htlcPolicyFails = signal.PolicyFails60m
    htlcLiquidityFails = signal.LiquidityFails60m
    htlcWindowMin = signal.WindowMin
    if signal.SampleLow {
      tags = append(tags, "htlc-sample-low")
    }
    if signal.PolicyHot {
      tags = append(tags, "htlc-policy-hot")
    }
    if signal.LiquidityHot {
      tags = append(tags, "htlc-liquidity-hot")
    }
    if signal.PolicyHot && signal.LiquidityHot {
      tags = append(tags, "htlc-neutral-lock")
    }
  }
  if outRatio < 0.10 {
    lack := (0.10 - outRatio) / 0.10
    bump := math.Min(e.profile.SurgeBumpMax, 0.5*lack)
    if bump > 0 {
      target = int(math.Ceil(float64(target) * (1.0 + bump)))
      tags = append(tags, fmt.Sprintf("surge+%d%%", int(bump*100)))
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

  rebal := rebalStats.ByChannel[ch.ChannelID]
  baseCostPpm := 0
  perCost := 0
  if rebal.AmtMsat > 0 {
    perCost = ppmMsat(rebal.FeeMsat, rebal.AmtMsat)
  }

  switch normalizeRebalCostMode(e.cfg.RebalCostMode) {
  case "global":
    baseCostPpm = rebalGlobalPpm
  case "channel":
    if perCost > 0 {
      baseCostPpm = perCost
      st.LastRebalCost = perCost
      st.LastRebalCostTs = e.now
    } else if st.LastRebalCost > 0 && e.now.Sub(st.LastRebalCostTs) <= 21*24*time.Hour {
      baseCostPpm = st.LastRebalCost
    } else if outPpm7d > 0 && fwdCount >= 4 {
      baseCostPpm = outPpm7d
    } else if st.LastOutrate > 0 && !st.LastOutrateTs.IsZero() && e.now.Sub(st.LastOutrateTs) <= 21*24*time.Hour {
      baseCostPpm = st.LastOutrate
    } else if seed > 0 {
      baseCostPpm = int(seed)
    }
  default:
    baseCostPpm = rebalGlobalPpm
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
      blended := int(math.Round(weight*float64(perCost) + (1.0-weight)*float64(rebalGlobalPpm)))
      baseCostPpm = blended
      st.LastRebalCost = perCost
      st.LastRebalCostTs = e.now
    } else if st.LastRebalCost > 0 && e.now.Sub(st.LastRebalCostTs) <= 21*24*time.Hour {
      baseCostPpm = st.LastRebalCost
    }
  }
  if baseCostPpm < e.cfg.MinPpm {
    baseCostPpm = e.cfg.MinPpm
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

  capRefPpm := 0
  if perCost > 0 {
    capRefPpm = perCost
  } else if st.LastRebalCost > 0 && e.now.Sub(st.LastRebalCostTs) <= 21*24*time.Hour {
    capRefPpm = st.LastRebalCost
  } else if outPpm7d > 0 && fwdCount >= 4 {
    capRefPpm = outPpm7d
  } else if st.LastOutrate > 0 && !st.LastOutrateTs.IsZero() && e.now.Sub(st.LastOutrateTs) <= 21*24*time.Hour {
    capRefPpm = st.LastOutrate
  } else if seed > 0 {
    capRefPpm = int(seed)
  }

  discoveryHit := false
  discoveryHard := false
  explorerActive := false
  if e.cfg.ExplorerEnabled {
    explorerActive = e.evalExplorer(st, outRatio, fwdCount, ch.LocalBalanceSat, ch.CapacitySat, localPpm)
    if explorerActive {
      tags = append(tags, "explorer")
    }
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
    target = localPpm
    tags = append(tags, "no-down-low")
  }

  if superSourceActive {
    target = e.cfg.MinPpm
  }

  target = clampInt(target, e.cfg.MinPpm, e.cfg.MaxPpm)
  if marginPpm7d < 0 && target < localPpm {
    target = localPpm
    tags = append(tags, "no-down-neg-margin")
  }
  capFrac := e.profile.StepCap
  minStep := 5
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
  if discoveryHit {
    capFrac = math.Max(capFrac, e.profile.DiscoveryStepCapDown)
  }
  if discoveryHard {
    capFrac = math.Max(capFrac, e.profile.DiscHarddropCapFrac)
  }
  if explorerActive {
    capFrac = math.Max(capFrac, e.profile.DiscoveryStepCapDown)
  }

  globalNegLockApplied := false
  lockSkipTag := ""
  if negMarginGlobal {
    hasRecentRebal := perCost > 0 || (st.LastRebalCost > 0 && e.now.Sub(st.LastRebalCostTs) <= 21*24*time.Hour)
    hasRecentOutrate := (outPpm7d > 0 && fwdCount >= 4) || (st.LastOutrate > 0 && !st.LastOutrateTs.IsZero() && e.now.Sub(st.LastOutrateTs) <= 21*24*time.Hour)
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
        if allowSoften {
          if outPpm7d > 0 {
            pegFloor := int(math.Round(float64(outPpm7d) * softenMaxDropToPegFrac))
            if target < pegFloor {
              target = pegFloor
            }
          }
        } else {
          target = localPpm
          globalNegLockApplied = true
        }
      }
      capFrac = math.Max(capFrac, e.profile.StepCap+0.05)
    } else if target < localPpm && !discoveryHit {
      lockSkipTag = "lock-skip-no-chan-rebal"
    }
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

  floor := int(math.Ceil(float64(baseCostPpm) * 1.10))
  floorSrc := "rebal"
  if strings.EqualFold(classLabel, "sink") && baseCostPpm > 0 && e.profile.SinkExtraFloorMargin > 0 {
    sinkFloor := int(math.Ceil(float64(baseCostPpm) * (1.10 + e.profile.SinkExtraFloorMargin)))
    if sinkFloor > floor {
      floor = sinkFloor
      floorSrc = "rebal-sink"
      tags = append(tags, "sink-floor")
    }
  }
  if outPpm7d > 0 && !discoveryHit && !explorerActive {
    outrateFloorActive := true
    factor := outrateFloorFactor
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
  if outPpm7d > 0 && fwdCount >= 4 {
    peg := int(math.Ceil(float64(outPpm7d) * outratePegHeadroom))
    withinGrace := true
    if !st.LastTs.IsZero() && e.profile.OutratePegGraceHours > 0 {
      hoursSince := e.now.Sub(st.LastTs).Hours()
      withinGrace = hoursSince < float64(e.profile.OutratePegGraceHours)
    }
    demandPeg := seed > 0 && float64(outPpm7d) >= seed*outratePegSeedMult
    if peg > floor && (withinGrace || demandPeg) {
      floor = peg
      floorSrc = "peg"
      tags = append(tags, "peg")
      if withinGrace {
        tags = append(tags, "peg-grace")
      }
      if demandPeg {
        tags = append(tags, "peg-demand")
      }
    }
  }

  if explorerActive {
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

  if superSourceActive {
    floor = e.cfg.MinPpm
    floorSrc = "super-source"
  }

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
      finalPpm = localPpm
      profitProtectLocked = true
    }
    finalPpm = clampInt(finalPpm, e.cfg.MinPpm, e.cfg.MaxPpm)
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
  if marginPpm7d < 0 && finalPpm < localPpm {
    finalPpm = localPpm
    if !containsTag(tags, "no-down-neg-margin") {
      tags = append(tags, "no-down-neg-margin")
    }
  }

  inboundDiscount := 0
  if e.cfg.InboundPassiveEnabled && classLabel == "sink" && outRatio <= 0.10 && fwdCount >= 5 && marginPpm7d >= 200 {
    anchor := int(math.Ceil(float64(baseCostPpm) * 1.002))
    maxDiscount := int(math.Ceil(float64(finalPpm) * 0.90))
    gap := finalPpm - anchor
    if gap > 0 {
      inboundDiscount = minInt(gap, maxDiscount)
    }
  }

  apply := true
  delta := int(math.Abs(float64(finalPpm - localPpm)))
  rel := float64(delta) / float64(maxInt(1, localPpm))
  if delta < 15 && rel < 0.04 {
    apply = false
    tags = append(tags, "hold-small")
  }
  skipCooldownDown := explorerActive && finalPpm < localPpm && e.profile.ExplorerSkipCooldownDown
  if skipCooldownDown {
    tags = append(tags, "cooldown-skip")
  }
  if finalPpm != localPpm && !e.ignoreCooldown && !skipCooldownDown {
    fwdsSince := fwdCount - st.BaselineFwd7d
    cooldownHours := float64(e.cfg.CooldownDownSec) / 3600.0
    if finalPpm > localPpm {
      cooldownHours = float64(e.cfg.CooldownUpSec) / 3600.0
    }
    if !st.LastTs.IsZero() {
      hoursSince := e.now.Sub(st.LastTs).Hours()
      if hoursSince < cooldownHours && fwdsSince < 2 {
        apply = false
        if !containsTag(tags, "cooldown") {
          tags = append(tags, "cooldown")
        }
      }
    }
  }

  if finalPpm < localPpm && !e.ignoreCooldown && !skipCooldownDown && !st.LastTs.IsZero() &&
    marginPpm7d >= e.profile.ProfitDownMarginMin && fwdCount >= e.profile.ProfitDownFwdsMin {
    hoursSince := e.now.Sub(st.LastTs).Hours()
    profitCooldown := float64(e.cfg.CooldownDownSec)/3600.0 + float64(e.profile.ProfitDownExtraHours)
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

  if fwdCount > 0 {
    if st.BaselineFwd7d > 0 {
      st.BaselineFwd7d = int(math.Round(0.7*float64(st.BaselineFwd7d) + 0.3*float64(fwdCount)))
    } else {
      st.BaselineFwd7d = fwdCount
    }
  }
  st.LastPpm = finalPpm
  if inboundDiscount > 0 {
    st.LastInboundDiscount = inboundDiscount
  }
  if outPpm7d > 0 {
    st.LastOutrate = outPpm7d
    st.LastOutrateTs = e.now
  }

  if finalPpm == localPpm {
    tags = append(tags, "same-ppm")
  }
  if apply && finalPpm != localPpm {
    st.LastTs = e.now
    if finalPpm > localPpm {
      st.LastDir = "up"
    } else if finalPpm < localPpm {
      st.LastDir = "down"
    }
  }

  if explorerActive && finalPpm < localPpm {
    st.ExplorerState.Rounds++
  }

  cooldownRemaining := 0.0
  if containsTag(tags, "cooldown") && !st.LastTs.IsZero() {
    hoursSince := e.now.Sub(st.LastTs).Hours()
    cooldownHours := float64(e.cfg.CooldownDownSec) / 3600.0
    if finalPpm > localPpm {
      cooldownHours = float64(e.cfg.CooldownUpSec) / 3600.0
    }
    remaining := cooldownHours - hoursSince
    if remaining > 0 {
      cooldownRemaining = remaining
    }
  }
  predictionCode, predictionCooldownHours := buildAutofeePrediction(outRatio, marginPpm7d, target, localPpm, finalPpm, fwdCount, negMarginGlobal, discoveryHit, cooldownRemaining)

  tags = append(tags, seedTags...)
  return &decision{
    ChannelID: ch.ChannelID,
    ChannelPoint: ch.ChannelPoint,
    Alias: strings.TrimSpace(ch.PeerAlias),
    LocalPpm: localPpm,
    NewPpm: finalPpm,
    Target: target,
    Floor: floor,
    FloorSrc: floorSrc,
    Tags: tags,
    InboundDiscount: inboundDiscount,
    SuperSourceActive: superSourceActive,
    OutRatio: outRatio,
    OutPpm7d: outPpm7d,
    RebalPpm: baseCostPpm,
    Seed: int(seed),
    Margin: marginPpm7d,
    RevShare: revShare,
    ClassLabel: classLabel,
    FwdCount: fwdCount,
    NegMarginGlobal: negMarginGlobal,
    PredictionCode: predictionCode,
    PredictionCooldownHours: predictionCooldownHours,
    HTLCAttempts: htlcAttempts,
    HTLCPolicyFails: htlcPolicyFails,
    HTLCLiquidityFails: htlcLiquidityFails,
    HTLCWindowMin: htlcWindowMin,
    Apply: apply && finalPpm != localPpm,
    State: st,
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
      st.ExplorerState = explorerState{
        Active: true,
        StartedTs: nowTs,
        Rounds: 0,
        FwdsAtStart: fwdCount,
        Seen: true,
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
func (e *autofeeEngine) seedForChannel(pubkey string, st *autofeeChannelState) (float64, float64, []string) {
  tags := []string{}
  if e.cfg.AmbossEnabled {
    token, err := e.fetchAmbossToken(context.Background())
    if err != nil {
      tags = append(tags, "seed:amboss-error")
    } else if token == "" {
      tags = append(tags, "seed:amboss-missing")
    } else if pubkey != "" {
      seed, seedP95, seedTags, err := e.fetchAmbossSeed(pubkey, token)
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
        return seed, seedP95, tags
      } else {
        tags = append(tags, "seed:amboss-empty")
      }
    }
  }

  if st.LastOutrate > 0 && !st.LastOutrateTs.IsZero() && e.now.Sub(st.LastOutrateTs) <= 21*24*time.Hour {
    return float64(st.LastOutrate), 0, append(tags, "seed:outrate")
  }
  if st.LastSeed > 0 {
    return float64(st.LastSeed), 0, append(tags, "seed:mem")
  }
  return 200.0, 0, append(tags, "seed:default")
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

type ambossSeriesResp struct {
  Data struct {
    GetNodeMetrics struct {
      HistoricalSeries [][]any `json:"historical_series"`
    } `json:"getNodeMetrics"`
  } `json:"data"`
}

func (e *autofeeEngine) fetchAmbossSeed(pubkey string, token string) (float64, float64, []string, error) {
  vals, err := fetchAmbossSeries(pubkey, token, e.cfg.LookbackDays, "incoming_fee_rate_metrics", "weighted_corrected_mean")
  if err != nil {
    return 0, 0, nil, err
  }
  if len(vals) == 0 {
    return 0, 0, nil, nil
  }
  p65 := percentile(vals, 0.65)
  p95 := percentile(vals, 0.95)
  seed := p65
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
    ratio := outWcorr / incWcorr
    f := 1.0 + 0.20*(ratio-1.0)
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
  if seed > 1600 {
    seed = 1600
    tags = append(tags, "seed:absmax")
  }
  tags = append(tags, "seed:amboss")
  return seed, p95, tags, nil
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
      "from": fromDate,
      "metric": metric,
      "pubkey": pubkey,
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
  if d.InboundDiscount > 0 {
    inboundEnabled = true
    inboundRate = int64(-absInt(d.InboundDiscount))
  }

  if baseFee == 0 || timeLock == 0 {
    policy, err := e.svc.lnd.GetChannelPolicy(ctx, ch.ChannelPoint)
    if err == nil {
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

  return e.svc.lnd.UpdateChannelFees(ctx, ch.ChannelPoint, false, baseFee, feeRate, timeLock, inboundEnabled, 0, inboundRate)
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
    OccurredAt: time.Now().UTC(),
    Type: "autofee",
    Action: action,
    Direction: "neutral",
    Status: "SETTLED",
    AmountSat: 0,
    FeeSat: 0,
    FeeMsat: 0,
    ChannelID: int64(d.ChannelID),
    ChannelPoint: d.ChannelPoint,
    Memo: memo,
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
    Type: "autofee",
    Action: action,
    Direction: "neutral",
    Status: "SETTLED",
    Memo: memo,
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

func classifyChannel(biasEma float64, outRatio float64, inCount int64, outCount int64, prevLabel string, prevConf float64) (string, float64) {
  label := "unknown"
  conf := 0.0
  if (inCount+outCount) >= 4 {
    if biasEma >= 0.50 && outRatio < 0.15 {
      label = "sink"
      conf = math.Min(1.0, (biasEma-0.50)/(1.0-0.50)+0.3)
    } else if biasEma <= -0.35 && outRatio > 0.55 {
      label = "source"
      conf = math.Min(1.0, ((-biasEma)-0.35)/(1.0-0.35)+0.3)
    } else if math.Abs(biasEma) <= 0.30 && inCount > 0 && outCount > 0 {
      label = "router"
      conf = math.Min(1.0, (0.30-math.Abs(biasEma))/0.30+0.3)
    }
  }
  if label == "unknown" {
    return prevLabel, prevConf
  }
  if prevLabel == "" || prevLabel == "unknown" {
    return label, conf
  }
  if label != prevLabel {
    if conf >= prevConf+0.10 {
      return label, conf
    }
    return prevLabel, prevConf
  }
  return label, math.Min(1.0, 0.5*prevConf+0.5*conf)
}

func containsTag(tags []string, want string) bool {
  for _, tag := range tags {
    if tag == want {
      return true
    }
  }
  return false
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
    case strings.HasPrefix(t, "surge"):
      add("📈" + t)
    case t == "top-rev":
      add("💎top-rev")
    case t == "neg-margin":
      add("⚠️neg-margin")
    case t == "htlc-policy-hot":
      add("🧾policy-hot")
    case t == "htlc-liquidity-hot":
      add("💧liq-hot")
    case t == "htlc-sample-low":
      add("📉htlc-low-sample")
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
    case strings.HasPrefix(t, "negm+"):
      add("💹" + t)
    case t == "outrate-floor":
      add("📊outrate-floor")
    case t == "circuit-breaker":
      add("🧯cb")
    case t == "extreme-drain":
      add("⚡extreme")
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
    case t == "cooldown":
      add("⏳cooldown")
    case t == "cooldown-profit":
      add("⏳profit-hold")
    case t == "cooldown-skip":
      add("🧭skip-cooldown")
    case t == "hold-small":
      add("🧊hold-small")
    case t == "same-ppm":
      add("🟰same-ppm")
    case t == "no-down-low":
      add("🚫down-low")
    case t == "no-down-neg-margin":
      add("🚫down-neg")
    case t == "profit-protect-lock":
      add("🛡️profit-lock")
    case t == "profit-protect-relax":
      add("🕊️profit-relax")
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
    case strings.HasPrefix(t, "seed:p95cap"):
      add("🧢seed-p95")
    case strings.HasPrefix(t, "seed:absmax"):
      add("🧱seed-cap")
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
  if outRatio < 0.05 && (marginPpm7d < 0 || negMarginGlobal) {
    return "hold_or_up", 0
  }
  if outRatio > 0.10 && marginPpm7d > 0 {
    return "reduce", 0
  }
  if discoveryHit && newPpm < localPpm {
    return "discovery_fast", 0
  }
  if fwdCount == 0 && outRatio > 0.60 {
    return "idle_reduce", 0
  }
  if target > localPpm && newPpm >= localPpm {
    if cooldownRemainingHours > 0 {
      return "bias_up", int(math.Round(cooldownRemainingHours))
    }
    return "bias_up", 0
  }
  if target < localPpm && newPpm <= localPpm {
    return "bias_down", 0
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
