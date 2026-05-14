
import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getRebalanceChannels,
  getRebalanceConfig,
  getRebalanceHistory,
  getRebalanceOverview,
  getRebalancePairStats,
  getRebalanceQueue,
  resetRebalanceMissionControl,
  runRebalance,
  updateRebalanceChannelAuto,
  updateRebalanceChannelManualRestart,
  updateRebalanceChannelTarget,
  updateRebalanceConfig,
  updateRebalanceExclude
} from '../api'
import { MetricDisclosure } from '../components/rebalance/MetricDisclosure'
import { PairStatsPanel } from '../components/rebalance/PairStatsPanel'
import { SettingsSubcard } from '../components/rebalance/SettingsSubcard'
import type {
  RebalanceAttempt,
  RebalanceChannel,
  RebalanceConfig,
  RebalanceJob,
  RebalanceOverview,
  RebalancePairStat,
  RebalanceScanSkip
} from '../components/rebalance/types'
import { getLocale } from '../i18n'

const REBALANCE_DEFAULT_MIN_PROBE_SAT = 5000
const REBALANCE_DEFAULT_MIN_EXECUTE_SAT = 10000
const REBALANCE_DEFAULT_BUDGET_MODE = 'hybrid_revenue'
const REBALANCE_DEFAULT_GAIN_MODEL_VERSION = 1
const REBALANCE_DEFAULT_VELOCITY_WEIGHT = 0.7
const REBALANCE_DEFAULT_AUTOFEE_SETTLING_WINDOW_SEC = 7200
const REBALANCE_DEFAULT_AUTOFEE_SETTLING_MULTIPLIER = 0.5
const REBALANCE_DEFAULT_FRESH_LOCK_HOURS = 6
const REBALANCE_DEFAULT_SCHEDULER_MODE = 'rules_auto'
const REBALANCE_DEFAULT_SOVEREIGN_SCOPE = 'auto_and_manual_restart'
const REBALANCE_DEFAULT_SOVEREIGN_MAX_JOBS = 2
const REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_RATE = 0.02
const REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_PROFIT_COST_RATIO = 1.2
const REBALANCE_DEFAULT_SOVEREIGN_BUDGET_EFFICIENCY_MIN_RATIO = 0.5
const REBALANCE_DEFAULT_SOVEREIGN_ROUTE_DEAD_SOURCE_SHARE = 0.2
const REBALANCE_DEFAULT_SOVEREIGN_RISK_SCORE_FLOOR = 0.02
const MSPR_DEFAULT_MAX_SHARDS = 6
const MSPR_MAX_SHARDS_LIMIT = 20
const MSPR_DEFAULT_PARALLELISM = 3
const MSPR_DEFAULT_MIN_SHARD_SAT = 10000
const MSPR_DEFAULT_ROUND_TIMEOUT_SEC = 35

const PAYBACK_MODE_PAYBACK = 1
const PAYBACK_MODE_TIME = 2
const PAYBACK_MODE_CRITICAL = 4
const REBALANCE_ROUTE_KEY = 'rebalance-center'
const LIGHTNING_OPS_ROUTE_KEY = 'lightning-ops'
const CHANNEL_HASH_PARAM = 'channel_point'

const readHashChannelPoint = (routeKey: string) => {
  if (typeof window === 'undefined') return ''
  const rawHash = window.location.hash.startsWith('#')
    ? window.location.hash.slice(1)
    : window.location.hash
  if (!rawHash) return ''
  const queryIndex = rawHash.indexOf('?')
  if (queryIndex < 0) return ''
  if (rawHash.slice(0, queryIndex) !== routeKey) return ''
  const params = new URLSearchParams(rawHash.slice(queryIndex + 1))
  return (params.get(CHANNEL_HASH_PARAM) || '').trim()
}

const buildHashWithChannelPoint = (routeKey: string, channelPoint: string) =>
  `#${routeKey}?${CHANNEL_HASH_PARAM}=${encodeURIComponent(channelPoint)}`

const desktopChannelRowID = (channelPoint: string) =>
  `rebalance-channel-desktop-${channelPoint.replace(/[^a-zA-Z0-9_-]/g, '_')}`

const mobileChannelCardID = (channelPoint: string) =>
  `rebalance-channel-mobile-${channelPoint.replace(/[^a-zA-Z0-9_-]/g, '_')}`

export default function RebalanceCenter() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const formatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const pctFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }), [locale])
  const econRatioFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }), [locale])
  const roiFormatter = useMemo(
    () => new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }),
    [locale]
  )
  const dateTimeFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'medium' }),
    [locale]
  )

  const [config, setConfig] = useState<RebalanceConfig | null>(null)
  const [overview, setOverview] = useState<RebalanceOverview | null>(null)
  const [channels, setChannels] = useState<RebalanceChannel[]>([])
  const [queueJobs, setQueueJobs] = useState<RebalanceJob[]>([])
  const [queueAttempts, setQueueAttempts] = useState<RebalanceAttempt[]>([])
  const [historyJobs, setHistoryJobs] = useState<RebalanceJob[]>([])
  const [historyAttempts, setHistoryAttempts] = useState<RebalanceAttempt[]>([])
  const [historyExpanded, setHistoryExpanded] = useState<Record<number, boolean>>({})
  const [serverConfig, setServerConfig] = useState<RebalanceConfig | null>(null)
  const [configDirty, setConfigDirty] = useState(false)
  const [historyFilter, setHistoryFilter] = useState<'all' | 'succeeded' | 'partial' | 'failed' | 'skipped'>('all')
  const [initialLoading, setInitialLoading] = useState(true)
  const [isRefreshing, setIsRefreshing] = useState(false)
  const [status, setStatus] = useState('')
  const [saving, setSaving] = useState(false)
  const [autopilotSaving, setAutopilotSaving] = useState(false)
  const [autoOpen, setAutoOpen] = useState(false)
  const [advancedControlsOpen, setAdvancedControlsOpen] = useState(false)
  const [editTargets, setEditTargets] = useState<Record<string, string>>({})
  const [editEconRatios, setEditEconRatios] = useState<Record<string, string>>({})
  const [editUseDefaultEconRatio, setEditUseDefaultEconRatio] = useState<Record<string, boolean>>({})
  const [editAutoBypassCostGate, setEditAutoBypassCostGate] = useState<Record<string, boolean>>({})
  const [manualRestart, setManualRestart] = useState<Record<string, boolean>>({})
  const [channelSort, setChannelSort] = useState<'economic' | 'emptiest'>('economic')
  const [channelSortDir, setChannelSortDir] = useState<'asc' | 'desc'>('desc')
  const [channelSearch, setChannelSearch] = useState('')
  const [channelMinCapacity, setChannelMinCapacity] = useState('')
  const [channelShowPrivate, setChannelShowPrivate] = useState(false)
  const [skipDetailsOpen, setSkipDetailsOpen] = useState(false)
  const [scanDetailsOpen, setScanDetailsOpen] = useState(false)
  const [metrics24hOpen, setMetrics24hOpen] = useState(false)
  const [mppMetricsOpen, setMppMetricsOpen] = useState(false)
  const [failureTelemetryOpen, setFailureTelemetryOpen] = useState(false)
  const [autopilotDecisionsOpen, setAutopilotDecisionsOpen] = useState(false)
  const [autopilotConfigOpen, setAutopilotConfigOpen] = useState(false)
  const [pairStatsOpen, setPairStatsOpen] = useState<Record<string, boolean>>({})
  const [pairStatsByChannel, setPairStatsByChannel] = useState<Record<string, RebalancePairStat[]>>({})
  const [pairStatsLoading, setPairStatsLoading] = useState<Record<string, boolean>>({})
  const [pairStatsError, setPairStatsError] = useState<Record<string, boolean>>({})
  const [mcResetBusy, setMcResetBusy] = useState(false)
  const [scanDetailsReason, setScanDetailsReason] = useState('all')
  const [scanDetailsShowAll, setScanDetailsShowAll] = useState(false)
  const [focusedChannelPoint, setFocusedChannelPoint] = useState('')
  const configRef = useRef<RebalanceConfig | null>(null)
  const autoOpenRef = useRef(false)
  const pendingScrollChannelRef = useRef('')
  const focusClearTimerRef = useRef<number | null>(null)
  const hasLoadedOnceRef = useRef(false)
  const loadInFlightRef = useRef(false)
  const queuedSilentRefreshRef = useRef(false)
  const channelStateVersionRef = useRef(0)

  const formatSats = (value: number) => `${formatter.format(Math.round(value))} sats`
  const formatPct = (value: number) => `${pctFormatter.format(value)}%`
  const formatEconRatio = (value: number) => econRatioFormatter.format(Math.round(value * 100) / 100)
  const formatRoi = (value: number) => (value > 0 && value < 0.01 ? '<0.01' : roiFormatter.format(value))
  const effectiveExecuteMinSat = (cfg?: RebalanceConfig | null) => {
    if (!cfg) return 0
    if (cfg.min_split_enabled && cfg.min_execute_sat > 0) return cfg.min_execute_sat
    return cfg.min_amount_sat > 0 ? cfg.min_amount_sat : 0
  }
  const formatTimestamp = (value?: string) => {
    if (!value) return '-'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return dateTimeFormatter.format(date)
  }
  const normalizeLoadedConfig = (raw: RebalanceConfig): RebalanceConfig => ({
    ...raw,
    scheduler_mode: raw.scheduler_mode || REBALANCE_DEFAULT_SCHEDULER_MODE,
    sovereign_candidate_scope: raw.sovereign_candidate_scope || REBALANCE_DEFAULT_SOVEREIGN_SCOPE,
    sovereign_max_jobs_per_cycle: raw.sovereign_max_jobs_per_cycle || REBALANCE_DEFAULT_SOVEREIGN_MAX_JOBS,
    sovereign_min_expected_profit_sat: raw.sovereign_min_expected_profit_sat || 0,
    sovereign_low_success_min_rate: typeof raw.sovereign_low_success_min_rate === 'number' ? raw.sovereign_low_success_min_rate : REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_RATE,
    sovereign_low_success_min_profit_cost_ratio: typeof raw.sovereign_low_success_min_profit_cost_ratio === 'number' ? raw.sovereign_low_success_min_profit_cost_ratio : REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_PROFIT_COST_RATIO,
    sovereign_budget_efficiency_min_ratio: typeof raw.sovereign_budget_efficiency_min_ratio === 'number' ? raw.sovereign_budget_efficiency_min_ratio : REBALANCE_DEFAULT_SOVEREIGN_BUDGET_EFFICIENCY_MIN_RATIO,
    sovereign_route_dead_source_share: typeof raw.sovereign_route_dead_source_share === 'number' ? raw.sovereign_route_dead_source_share : REBALANCE_DEFAULT_SOVEREIGN_ROUTE_DEAD_SOURCE_SHARE,
    sovereign_risk_score_floor: typeof raw.sovereign_risk_score_floor === 'number' ? raw.sovereign_risk_score_floor : REBALANCE_DEFAULT_SOVEREIGN_RISK_SCORE_FLOOR,
    budget_mode: raw.budget_mode || REBALANCE_DEFAULT_BUDGET_MODE,
    budget_unlimited: raw.budget_unlimited ?? false,
    budget_auto_only: raw.budget_auto_only ?? false,
    manual_reserve_enabled: raw.manual_reserve_enabled ?? false,
    manual_reserve_mode: raw.manual_reserve_mode || 'fixed_sat',
    manual_reserve_value: raw.manual_reserve_value ?? 0,
    amount_probe_steps: raw.amount_probe_steps || 4,
    amount_probe_adaptive: raw.amount_probe_adaptive ?? true,
    attempt_timeout_sec: raw.attempt_timeout_sec || 20,
    rebalance_timeout_sec: raw.rebalance_timeout_sec || 600,
    manual_restart_watch: raw.manual_restart_watch ?? false,
    cooldown_probe_enabled: raw.cooldown_probe_enabled ?? false,
    mc_half_life_sec: raw.mc_half_life_sec || 0,
    fresh_paid_liquidity_lock_enabled: raw.fresh_paid_liquidity_lock_enabled ?? true,
    fresh_paid_liquidity_lock_hours: raw.fresh_paid_liquidity_lock_hours || REBALANCE_DEFAULT_FRESH_LOCK_HOURS,
    min_split_enabled: raw.min_split_enabled ?? false,
    min_probe_sat: raw.min_probe_sat || 0,
    min_execute_sat: raw.min_execute_sat || 0,
    mpp_enabled: raw.mpp_enabled ?? false,
    mpp_max_shards: raw.mpp_max_shards || MSPR_DEFAULT_MAX_SHARDS,
    mpp_parallelism: raw.mpp_parallelism || MSPR_DEFAULT_PARALLELISM,
    mpp_min_shard_sat: raw.mpp_min_shard_sat || MSPR_DEFAULT_MIN_SHARD_SAT,
    mpp_round_timeout_sec: raw.mpp_round_timeout_sec || MSPR_DEFAULT_ROUND_TIMEOUT_SEC,
    mpp_auto_only: raw.mpp_auto_only ?? false,
    gain_model_version: raw.gain_model_version || REBALANCE_DEFAULT_GAIN_MODEL_VERSION,
    velocity_weight: typeof raw.velocity_weight === 'number' ? raw.velocity_weight : REBALANCE_DEFAULT_VELOCITY_WEIGHT,
    autofee_settling_window_sec: typeof raw.autofee_settling_window_sec === 'number' ? raw.autofee_settling_window_sec : REBALANCE_DEFAULT_AUTOFEE_SETTLING_WINDOW_SEC,
    autofee_settling_multiplier: typeof raw.autofee_settling_multiplier === 'number' ? raw.autofee_settling_multiplier : REBALANCE_DEFAULT_AUTOFEE_SETTLING_MULTIPLIER,
    delegated_fast_path_enabled: raw.delegated_fast_path_enabled ?? true,
    delegated_fast_path_strict_payback: raw.delegated_fast_path_strict_payback ?? true
  })
  const formatTimeToPayback = (channel: RebalanceChannel) => {
    if (!channel.time_to_payback_valid) return t('rebalanceCenter.channels.timeToPaybackNA')
    const hours = Math.max(0, channel.time_to_payback_hours || 0)
    if (hours <= 0) return t('rebalanceCenter.channels.timeToPaybackPaid')
    if (hours < 24) return t('rebalanceCenter.channels.timeToPaybackHours', { value: pctFormatter.format(hours) })
    return t('rebalanceCenter.channels.timeToPaybackDays', { value: pctFormatter.format(hours / 24) })
  }
  const configSignature = (cfg?: RebalanceConfig | null) => {
    if (!cfg) return ''
    return JSON.stringify({
      auto_enabled: cfg.auto_enabled,
      scheduler_mode: cfg.scheduler_mode,
      sovereign_candidate_scope: cfg.sovereign_candidate_scope,
      sovereign_max_jobs_per_cycle: cfg.sovereign_max_jobs_per_cycle,
      sovereign_min_expected_profit_sat: cfg.sovereign_min_expected_profit_sat,
      sovereign_low_success_min_rate: cfg.sovereign_low_success_min_rate,
      sovereign_low_success_min_profit_cost_ratio: cfg.sovereign_low_success_min_profit_cost_ratio,
      sovereign_budget_efficiency_min_ratio: cfg.sovereign_budget_efficiency_min_ratio,
      sovereign_route_dead_source_share: cfg.sovereign_route_dead_source_share,
      sovereign_risk_score_floor: cfg.sovereign_risk_score_floor,
      scan_interval_sec: cfg.scan_interval_sec,
      deadband_pct: cfg.deadband_pct,
      source_min_local_pct: cfg.source_min_local_pct,
      econ_ratio: cfg.econ_ratio,
      econ_ratio_max_ppm: cfg.econ_ratio_max_ppm,
      fee_limit_ppm: cfg.fee_limit_ppm,
      lost_profit: cfg.lost_profit,
      fail_tolerance_ppm: cfg.fail_tolerance_ppm,
      roi_min: cfg.roi_min,
      daily_budget_pct: cfg.daily_budget_pct,
      budget_mode: cfg.budget_mode,
      budget_unlimited: cfg.budget_unlimited,
      budget_auto_only: cfg.budget_auto_only,
      manual_reserve_enabled: cfg.manual_reserve_enabled,
      manual_reserve_mode: cfg.manual_reserve_mode,
      manual_reserve_value: cfg.manual_reserve_value,
      max_concurrent: cfg.max_concurrent,
      min_amount_sat: cfg.min_amount_sat,
      max_amount_sat: cfg.max_amount_sat,
      min_split_enabled: cfg.min_split_enabled,
      min_probe_sat: cfg.min_probe_sat,
      min_execute_sat: cfg.min_execute_sat,
      mpp_enabled: cfg.mpp_enabled,
      mpp_max_shards: cfg.mpp_max_shards,
      mpp_parallelism: cfg.mpp_parallelism,
      mpp_min_shard_sat: cfg.mpp_min_shard_sat,
      mpp_round_timeout_sec: cfg.mpp_round_timeout_sec,
      mpp_auto_only: cfg.mpp_auto_only,
      fee_ladder_steps: cfg.fee_ladder_steps,
      amount_probe_steps: cfg.amount_probe_steps,
      amount_probe_adaptive: cfg.amount_probe_adaptive,
      attempt_timeout_sec: cfg.attempt_timeout_sec,
      rebalance_timeout_sec: cfg.rebalance_timeout_sec,
      manual_restart_watch: cfg.manual_restart_watch,
      cooldown_probe_enabled: cfg.cooldown_probe_enabled,
      mc_half_life_sec: cfg.mc_half_life_sec,
      payback_mode_flags: cfg.payback_mode_flags,
      fresh_paid_liquidity_lock_enabled: cfg.fresh_paid_liquidity_lock_enabled,
      fresh_paid_liquidity_lock_hours: cfg.fresh_paid_liquidity_lock_hours,
      unlock_days: cfg.unlock_days,
      critical_release_pct: cfg.critical_release_pct,
      critical_min_sources: cfg.critical_min_sources,
      critical_min_available_sats: cfg.critical_min_available_sats,
      critical_cycles: cfg.critical_cycles,
      rebalance_cost_floor_ppm: cfg.rebalance_cost_floor_ppm,
      source_min_payback_progress: cfg.source_min_payback_progress,
      mission_control_reinforce: cfg.mission_control_reinforce,
      gain_model_version: cfg.gain_model_version,
      velocity_weight: cfg.velocity_weight,
      autofee_settling_window_sec: cfg.autofee_settling_window_sec,
      autofee_settling_multiplier: cfg.autofee_settling_multiplier,
      delegated_fast_path_enabled: cfg.delegated_fast_path_enabled,
      delegated_fast_path_strict_payback: cfg.delegated_fast_path_strict_payback
    })
  }
  const autopilotConfigSignature = (cfg?: RebalanceConfig | null) => {
    if (!cfg) return ''
    return JSON.stringify({
      scheduler_mode: cfg.scheduler_mode || REBALANCE_DEFAULT_SCHEDULER_MODE,
      sovereign_candidate_scope: cfg.sovereign_candidate_scope || REBALANCE_DEFAULT_SOVEREIGN_SCOPE,
      sovereign_max_jobs_per_cycle: cfg.sovereign_max_jobs_per_cycle || REBALANCE_DEFAULT_SOVEREIGN_MAX_JOBS,
      sovereign_min_expected_profit_sat: cfg.sovereign_min_expected_profit_sat || 0,
      sovereign_low_success_min_rate: cfg.sovereign_low_success_min_rate ?? REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_RATE,
      sovereign_low_success_min_profit_cost_ratio: cfg.sovereign_low_success_min_profit_cost_ratio ?? REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_PROFIT_COST_RATIO,
      sovereign_budget_efficiency_min_ratio: cfg.sovereign_budget_efficiency_min_ratio ?? REBALANCE_DEFAULT_SOVEREIGN_BUDGET_EFFICIENCY_MIN_RATIO,
      sovereign_route_dead_source_share: cfg.sovereign_route_dead_source_share ?? REBALANCE_DEFAULT_SOVEREIGN_ROUTE_DEAD_SOURCE_SHARE,
      sovereign_risk_score_floor: cfg.sovereign_risk_score_floor ?? REBALANCE_DEFAULT_SOVEREIGN_RISK_SCORE_FLOOR
    })
  }
  const estimateHistoricalCost = (amountSat: number, feePpm: number) => {
    if (amountSat <= 0 || feePpm <= 0) return 0
    return Math.ceil((amountSat * feePpm) / 1_000_000)
  }
  const estimateTargetGain = (amountSat: number, revenue7d: number, localBalance: number, capacity: number) => {
    if (amountSat <= 0 || revenue7d <= 0) return 0
    let denom = localBalance > 0 ? localBalance : capacity
    if (denom <= 0) return 0
    if (amountSat > denom) denom = amountSat
    return Math.round(revenue7d * (amountSat / denom))
  }
  const estimateTargetGainV2 = (amountSat: number, outgoingFeePpm: number, peerFeeRatePpm: number) => {
    if (amountSat <= 0 || outgoingFeePpm <= 0) return 0
    const spreadEffectiveness = Math.max(0, Math.min(1, 1 - (peerFeeRatePpm / outgoingFeePpm)))
    return Math.round((amountSat * outgoingFeePpm / 1_000_000) * spreadEffectiveness)
  }
  const estimateTargetGainV3 = (amountSat: number, outgoingFeePpm: number, peerFeeRatePpm: number, revenue7d: number, localBalance: number, capacity: number, drainRateSatPerHour: number) => {
    if (amountSat <= 0 || outgoingFeePpm <= 0) return 0
    const spreadEffectiveness = Math.max(0, Math.min(1, 1 - (peerFeeRatePpm / outgoingFeePpm)))
    if (spreadEffectiveness <= 0) return 0
    const theoretical = (amountSat * outgoingFeePpm / 1_000_000) * spreadEffectiveness
    if (theoretical <= 0) return 0
    let historical = 0
    if (revenue7d > 0) {
      let denom = localBalance > 0 ? localBalance : capacity
      if (amountSat > denom) denom = amountSat
      if (denom > 0) historical = revenue7d * (amountSat / denom)
    }
    let projected = 0
    if (drainRateSatPerHour > 0) {
      const horizonHours = 168
      const volume = Math.min(amountSat, drainRateSatPerHour * horizonHours)
      projected = volume * outgoingFeePpm / 1_000_000 * spreadEffectiveness
    }
    const demand = Math.max(historical, projected)
    const gain = demand > 0 ? Math.min(demand, theoretical) : theoretical * 0.5
    return Math.round(gain)
  }
  const computeVelocityScore = (economicScore: number, drainRateSatPerHour: number, maxDrainRateSatPerHour: number) => {
    // v3 already folds drain rate into the base gain, so applying the
    // velocity multiplier again would double-count demand.
    if (!config || config.gain_model_version < 2 || config.gain_model_version >= 3 || economicScore === 0) return economicScore
    const velocityWeight = Math.max(0, Math.min(1, config.velocity_weight ?? REBALANCE_DEFAULT_VELOCITY_WEIGHT))
    const velocityMultiplier = maxDrainRateSatPerHour > 0 && drainRateSatPerHour > 0
      ? Math.min(1, drainRateSatPerHour / maxDrainRateSatPerHour)
      : 0
    const ageBoost = 1.5
    return Math.round(economicScore * ((velocityWeight * velocityMultiplier) + ((1 - velocityWeight) * ageBoost)))
  }
  const computeChannelScore = (ch: RebalanceChannel, maxDrainRateSatPerHour = 0) => {
    const gainModel = config?.gain_model_version ?? 1
    let expectedGain: number
    if (gainModel >= 3) {
      expectedGain = estimateTargetGainV3(ch.target_amount_sat, ch.outgoing_fee_ppm, ch.peer_fee_rate_ppm, ch.revenue_7d_sat, ch.local_balance_sat, ch.capacity_sat, ch.drain_rate_sat_per_hour || 0)
    } else if (gainModel >= 2) {
      expectedGain = estimateTargetGainV2(ch.target_amount_sat, ch.outgoing_fee_ppm, ch.peer_fee_rate_ppm)
    } else {
      expectedGain = estimateTargetGain(ch.target_amount_sat, ch.revenue_7d_sat, ch.local_balance_sat, ch.capacity_sat)
    }
    let estimatedCost = estimateHistoricalCost(ch.target_amount_sat, ch.rebalance_cost_7d_ppm)
    if (ch.target_amount_sat <= 0) {
      expectedGain = Math.max(0, ch.revenue_7d_sat || 0)
      estimatedCost = Math.max(0, ch.rebalance_cost_7d_sat || 0)
    }
    const economicScore = expectedGain - estimatedCost
    const expectedRoiValid = expectedGain > 0 && estimatedCost > 0
    return {
      score: computeVelocityScore(economicScore, ch.drain_rate_sat_per_hour || 0, maxDrainRateSatPerHour),
      expectedRoi: expectedRoiValid ? expectedGain / estimatedCost : 0,
      expectedRoiValid,
      expectedGain,
      estimatedCost
    }
  }
  const filteredWorkbenchChannels = useMemo(() => {
    let list = channels.filter((ch) => ch.active)
    if (!channelShowPrivate) {
      list = list.filter((ch) => !ch.private)
    }
    if (channelSearch.trim()) {
      const query = channelSearch.trim().toLowerCase()
      list = list.filter((ch) => (
        ch.peer_alias?.toLowerCase().includes(query) ||
        ch.remote_pubkey?.toLowerCase().includes(query) ||
        ch.channel_point?.toLowerCase().includes(query)
      ))
    }
    const minCap = Number(channelMinCapacity || 0)
    if (minCap > 0) {
      list = list.filter((ch) => ch.capacity_sat >= minCap)
    }
    return list
  }, [channelMinCapacity, channelSearch, channelShowPrivate, channels])
  const workbenchMaxDrainRateSatPerHour = useMemo(
    () => filteredWorkbenchChannels.reduce((max, ch) => Math.max(max, ch.drain_rate_sat_per_hour || 0), 0),
    [filteredWorkbenchChannels]
  )

  const sortedChannels = useMemo(() => {
    const active = [...filteredWorkbenchChannels]
    const direction = channelSortDir === 'desc' ? -1 : 1
    if (channelSort === 'emptiest' || !config) {
      return active.sort((a, b) => (a.local_pct - b.local_pct) * direction)
    }
    return active.sort((a, b) => {
      const scoreA = computeChannelScore(a, workbenchMaxDrainRateSatPerHour)
      const scoreB = computeChannelScore(b, workbenchMaxDrainRateSatPerHour)
      if (scoreA.score !== scoreB.score) {
        return (scoreB.score - scoreA.score) * direction
      }
      if (scoreA.expectedRoi !== scoreB.expectedRoi) {
        return (scoreB.expectedRoi - scoreA.expectedRoi) * direction
      }
      if (a.target_amount_sat !== b.target_amount_sat) {
        return (b.target_amount_sat - a.target_amount_sat) * direction
      }
      return (a.local_pct - b.local_pct) * direction
    })
  }, [filteredWorkbenchChannels, config, channelSort, channelSortDir, workbenchMaxDrainRateSatPerHour])
  const buildAttemptTotals = (attempts: RebalanceAttempt[]) => {
    const totals = new Map<number, { amount: number; fee: number }>()
    attempts.forEach((attempt) => {
      if (attempt.status !== 'succeeded') return
      const current = totals.get(attempt.job_id) ?? { amount: 0, fee: 0 }
      current.amount += attempt.amount_sat || 0
      current.fee += attempt.fee_paid_sat || 0
      totals.set(attempt.job_id, current)
    })
    return totals
  }
  const historyTotals = useMemo(() => buildAttemptTotals(historyAttempts), [historyAttempts])
  const historyAttemptsByJob = useMemo(() => {
    const map = new Map<number, RebalanceAttempt[]>()
    historyAttempts.forEach((attempt) => {
      const list = map.get(attempt.job_id)
      if (list) {
        list.push(attempt)
      } else {
        map.set(attempt.job_id, [attempt])
      }
    })
    map.forEach((list) => list.sort((a, b) => a.attempt_index - b.attempt_index))
    return map
  }, [historyAttempts])
  const filteredHistory = useMemo(() => {
    if (historyFilter === 'all') return historyJobs
    if (historyFilter === 'failed') {
      return historyJobs.filter((job) => job.status === 'failed' || job.status === 'cancelled')
    }
    return historyJobs.filter((job) => job.status === historyFilter)
  }, [historyFilter, historyJobs])

  useEffect(() => {
    setConfigDirty(configSignature(config) !== configSignature(serverConfig))
  }, [config, serverConfig])
  useEffect(() => {
    configRef.current = config
  }, [config])
  useEffect(() => {
    autoOpenRef.current = autoOpen
  }, [autoOpen])
  useEffect(() => {
    if (typeof window === 'undefined') return
    const stored = window.localStorage.getItem('rebalance_center_channel_sort')
    if (stored === 'economic' || stored === 'emptiest') {
      setChannelSort(stored)
    }
  }, [])
  useEffect(() => {
    if (typeof window === 'undefined') return
    const stored = window.localStorage.getItem('rebalance_center_channel_sort_dir')
    if (stored === 'asc' || stored === 'desc') {
      setChannelSortDir(stored)
    }
  }, [])
  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('rebalance_center_channel_sort', channelSort)
  }, [channelSort])
  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('rebalance_center_channel_sort_dir', channelSortDir)
  }, [channelSortDir])
  useEffect(() => {
    pendingScrollChannelRef.current = readHashChannelPoint(REBALANCE_ROUTE_KEY)
    return () => {
      if (focusClearTimerRef.current !== null) {
        window.clearTimeout(focusClearTimerRef.current)
      }
    }
  }, [])
  useEffect(() => {
    if (typeof window === 'undefined') return
    const targetChannelPoint = pendingScrollChannelRef.current
    if (!targetChannelPoint) return
    const targetExists = sortedChannels.some((channel) => channel.channel_point === targetChannelPoint)
    if (!targetExists) return
    const prefersDesktop = window.matchMedia('(min-width: 768px)').matches
    const preferredID = prefersDesktop
      ? desktopChannelRowID(targetChannelPoint)
      : mobileChannelCardID(targetChannelPoint)
    const fallbackID = prefersDesktop
      ? mobileChannelCardID(targetChannelPoint)
      : desktopChannelRowID(targetChannelPoint)
    const targetElement = document.getElementById(preferredID) || document.getElementById(fallbackID)
    if (!targetElement) return
    targetElement.scrollIntoView({ behavior: 'smooth', block: 'center' })
    setFocusedChannelPoint(targetChannelPoint)
    pendingScrollChannelRef.current = ''
    window.history.replaceState(null, '', `#${REBALANCE_ROUTE_KEY}`)
    if (focusClearTimerRef.current !== null) {
      window.clearTimeout(focusClearTimerRef.current)
    }
    focusClearTimerRef.current = window.setTimeout(() => {
      setFocusedChannelPoint((current) => (current === targetChannelPoint ? '' : current))
      focusClearTimerRef.current = null
    }, 3200)
  }, [sortedChannels])
  const parseRemaining = (reason?: string) => {
    if (!reason) return null
    const match = reason.match(/remaining\s+(\d+)/i)
    if (!match) return null
    return Number(match[1])
  }
  const statusClass = (status: string) => {
    switch (status) {
      case 'succeeded':
        return 'text-emerald-200'
      case 'partial':
        return 'text-amber-200'
      case 'failed':
        return 'text-rose-200'
      case 'skipped':
        return 'text-sky-300'
      default:
        return 'text-fog/60'
    }
  }
  const parseLocaleDecimal = (raw: string) => Number(raw.replace(/\s/g, '').replace(',', '.'))
  const channelKey = (channel: Pick<RebalanceChannel, 'channel_id' | 'channel_point'>) =>
    channel.channel_point || String(channel.channel_id)
  const channelUseDefaultEconRatio = (channel: RebalanceChannel) =>
    editUseDefaultEconRatio[channelKey(channel)] ?? channel.use_default_econ_ratio
  const channelAutoBypassCostGate = (channel: RebalanceChannel) =>
    editAutoBypassCostGate[channelKey(channel)] ?? Boolean(channel.auto_bypass_cost_gate)
  const channelDraftEligibleAsTarget = (channel: RebalanceChannel) =>
    channel.eligible_as_target || (channelAutoBypassCostGate(channel) && channel.eligible_as_manual_target)
  const channelEconRatioInput = (channel: RebalanceChannel) => {
    if (channelUseDefaultEconRatio(channel)) {
      return formatEconRatio(config?.econ_ratio ?? 0)
    }
    const edited = editEconRatios[channelKey(channel)]
    if (edited !== undefined) return edited
    const fallback =
      channel.econ_ratio_override && channel.econ_ratio_override > 0
        ? channel.econ_ratio_override
        : config?.econ_ratio ?? 0
    return formatEconRatio(fallback)
  }
  const isSameChannel = (
    left: Pick<RebalanceChannel, 'channel_id' | 'channel_point'>,
    right: Pick<RebalanceChannel, 'channel_id' | 'channel_point'>
  ) => {
    if (left.channel_point && right.channel_point) {
      return left.channel_point === right.channel_point
    }
    return left.channel_id === right.channel_id
  }
  const bumpChannelStateVersion = () => {
    channelStateVersionRef.current += 1
    return channelStateVersionRef.current
  }

  const loadAll = async (options: { silent?: boolean } = {}) => {
    const silent = options.silent ?? false
    if (loadInFlightRef.current) {
      queuedSilentRefreshRef.current = true
      return
    }
    loadInFlightRef.current = true
    const channelStateVersionAtStart = channelStateVersionRef.current
    const isFirstLoad = !hasLoadedOnceRef.current
    try {
      if (isFirstLoad) {
        setInitialLoading(true)
      } else if (!silent) {
        setIsRefreshing(true)
      }
        const [cfg, ovw, ch, queue, hist] = await Promise.all([
          getRebalanceConfig(),
          getRebalanceOverview(),
          getRebalanceChannels(),
          getRebalanceQueue(),
          getRebalanceHistory()
        ])
        const normalizedConfig = normalizeLoadedConfig(cfg as RebalanceConfig)
        setServerConfig(normalizedConfig)
        const currentSig = configSignature(configRef.current)
        const nextSig = configSignature(normalizedConfig)
        if (!autoOpenRef.current || currentSig === '' || currentSig === nextSig) {
          setConfig(normalizedConfig)
        }
      setOverview(ovw as RebalanceOverview)
      const channelList = Array.isArray((ch as any)?.channels) ? (ch as any).channels : []
      if (channelStateVersionAtStart === channelStateVersionRef.current) {
        setChannels(channelList)
        const restartState: Record<string, boolean> = {}
        channelList.forEach((channel: RebalanceChannel) => {
          restartState[channel.channel_point] = channel.manual_restart_enabled
        })
        setManualRestart(restartState)
      }
      setQueueJobs(Array.isArray((queue as any)?.jobs) ? (queue as any).jobs : [])
      setQueueAttempts(Array.isArray((queue as any)?.attempts) ? (queue as any).attempts : [])
      setHistoryJobs(Array.isArray((hist as any)?.jobs) ? (hist as any).jobs : [])
      setHistoryAttempts(Array.isArray((hist as any)?.attempts) ? (hist as any).attempts : [])
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.loadFailed'))
    } finally {
      if (isFirstLoad) {
        hasLoadedOnceRef.current = true
        setInitialLoading(false)
      } else if (!silent) {
        setIsRefreshing(false)
      }
      loadInFlightRef.current = false
      if (queuedSilentRefreshRef.current) {
        queuedSilentRefreshRef.current = false
        void loadAll({ silent: true })
      }
    }
  }

  useEffect(() => {
    void loadAll()
  }, [])

  useEffect(() => {
    const timer = window.setInterval(() => {
      void loadAll({ silent: true })
    }, 30000)
    return () => window.clearInterval(timer)
  }, [])

  const handleSaveConfig = async () => {
    if (!config) return
    setSaving(true)
    setStatus('')
    try {
        const safeMppMaxShards = Math.max(1, Math.min(MSPR_MAX_SHARDS_LIMIT, Number(config.mpp_max_shards) || MSPR_DEFAULT_MAX_SHARDS))
        const safeMppParallelism = Math.max(1, Math.min(safeMppMaxShards, Number(config.mpp_parallelism) || MSPR_DEFAULT_PARALLELISM))
        const safeMppRoundTimeoutSec = Math.max(5, Number(config.mpp_round_timeout_sec) || MSPR_DEFAULT_ROUND_TIMEOUT_SEC)
        const saved = (await updateRebalanceConfig({
          auto_enabled: config.auto_enabled,
          scheduler_mode: config.scheduler_mode,
          sovereign_candidate_scope: config.sovereign_candidate_scope,
          sovereign_max_jobs_per_cycle: Math.max(1, Number(config.sovereign_max_jobs_per_cycle) || REBALANCE_DEFAULT_SOVEREIGN_MAX_JOBS),
          sovereign_min_expected_profit_sat: Math.max(0, Number(config.sovereign_min_expected_profit_sat) || 0),
          sovereign_low_success_min_rate: Math.max(0, Math.min(1, Number(config.sovereign_low_success_min_rate) || REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_RATE)),
          sovereign_low_success_min_profit_cost_ratio: Math.max(0, Number(config.sovereign_low_success_min_profit_cost_ratio) || REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_PROFIT_COST_RATIO),
          sovereign_budget_efficiency_min_ratio: Math.max(0, Number(config.sovereign_budget_efficiency_min_ratio) || REBALANCE_DEFAULT_SOVEREIGN_BUDGET_EFFICIENCY_MIN_RATIO),
          sovereign_route_dead_source_share: Math.max(0.01, Math.min(1, Number(config.sovereign_route_dead_source_share) || REBALANCE_DEFAULT_SOVEREIGN_ROUTE_DEAD_SOURCE_SHARE)),
          sovereign_risk_score_floor: Math.max(0.001, Math.min(0.2, Number(config.sovereign_risk_score_floor) || REBALANCE_DEFAULT_SOVEREIGN_RISK_SCORE_FLOOR)),
          scan_interval_sec: config.scan_interval_sec,
          deadband_pct: config.deadband_pct,
          source_min_local_pct: config.source_min_local_pct,
          econ_ratio: config.econ_ratio,
          econ_ratio_max_ppm: config.econ_ratio_max_ppm,
          fee_limit_ppm: config.fee_limit_ppm,
          lost_profit: config.lost_profit,
          fail_tolerance_ppm: config.fail_tolerance_ppm,
          roi_min: config.roi_min,
          daily_budget_pct: config.daily_budget_pct,
          budget_mode: config.budget_mode,
          budget_unlimited: config.budget_unlimited,
          budget_auto_only: config.budget_auto_only,
          manual_reserve_enabled: config.manual_reserve_enabled,
          manual_reserve_mode: config.manual_reserve_mode,
          manual_reserve_value: config.manual_reserve_value,
          max_concurrent: config.max_concurrent,
          min_amount_sat: config.min_amount_sat,
          max_amount_sat: config.max_amount_sat,
          min_split_enabled: config.min_split_enabled,
          min_probe_sat: config.min_probe_sat,
          min_execute_sat: config.min_execute_sat,
          mpp_enabled: config.mpp_enabled,
          mpp_max_shards: safeMppMaxShards,
          mpp_parallelism: safeMppParallelism,
          mpp_min_shard_sat: config.mpp_min_shard_sat,
          mpp_round_timeout_sec: safeMppRoundTimeoutSec,
          mpp_auto_only: config.mpp_auto_only,
          fee_ladder_steps: config.fee_ladder_steps,
          amount_probe_steps: config.amount_probe_steps,
          amount_probe_adaptive: config.amount_probe_adaptive,
          attempt_timeout_sec: config.attempt_timeout_sec,
          rebalance_timeout_sec: config.rebalance_timeout_sec,
          manual_restart_watch: config.manual_restart_watch,
          cooldown_probe_enabled: config.cooldown_probe_enabled,
          mc_half_life_sec: config.mc_half_life_sec,
          payback_mode_flags: config.payback_mode_flags,
          fresh_paid_liquidity_lock_enabled: config.fresh_paid_liquidity_lock_enabled,
          fresh_paid_liquidity_lock_hours: Math.max(1, Number(config.fresh_paid_liquidity_lock_hours) || REBALANCE_DEFAULT_FRESH_LOCK_HOURS),
          unlock_days: config.unlock_days,
          critical_release_pct: config.critical_release_pct,
          critical_min_sources: config.critical_min_sources,
          critical_min_available_sats: config.critical_min_available_sats,
        critical_cycles: config.critical_cycles,
        rebalance_cost_floor_ppm: config.rebalance_cost_floor_ppm,
        source_min_payback_progress: config.source_min_payback_progress,
        mission_control_reinforce: config.mission_control_reinforce,
        gain_model_version: config.gain_model_version,
        velocity_weight: config.velocity_weight,
        autofee_settling_window_sec: config.autofee_settling_window_sec,
        autofee_settling_multiplier: config.autofee_settling_multiplier,
        delegated_fast_path_enabled: config.delegated_fast_path_enabled,
        delegated_fast_path_strict_payback: config.delegated_fast_path_strict_payback
      })) as RebalanceConfig
      const normalizedSaved = normalizeLoadedConfig(saved)
      setServerConfig(normalizedSaved)
      setConfig(normalizedSaved)
      setStatus(t('rebalanceCenter.settingsSaved'))
      void loadAll({ silent: true })
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.settingsSaveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const handleSaveAutopilotConfig = async () => {
    if (!config) return
    setAutopilotSaving(true)
    setStatus('')
    try {
      const saved = (await updateRebalanceConfig({
        scheduler_mode: config.scheduler_mode,
        sovereign_candidate_scope: config.sovereign_candidate_scope,
        sovereign_max_jobs_per_cycle: Math.max(1, Number(config.sovereign_max_jobs_per_cycle) || REBALANCE_DEFAULT_SOVEREIGN_MAX_JOBS),
        sovereign_min_expected_profit_sat: Math.max(0, Number(config.sovereign_min_expected_profit_sat) || 0),
        sovereign_low_success_min_rate: Math.max(0, Math.min(1, Number(config.sovereign_low_success_min_rate) || REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_RATE)),
        sovereign_low_success_min_profit_cost_ratio: Math.max(0, Number(config.sovereign_low_success_min_profit_cost_ratio) || REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_PROFIT_COST_RATIO),
        sovereign_budget_efficiency_min_ratio: Math.max(0, Number(config.sovereign_budget_efficiency_min_ratio) || REBALANCE_DEFAULT_SOVEREIGN_BUDGET_EFFICIENCY_MIN_RATIO),
        sovereign_route_dead_source_share: Math.max(0.01, Math.min(1, Number(config.sovereign_route_dead_source_share) || REBALANCE_DEFAULT_SOVEREIGN_ROUTE_DEAD_SOURCE_SHARE)),
        sovereign_risk_score_floor: Math.max(0.001, Math.min(0.2, Number(config.sovereign_risk_score_floor) || REBALANCE_DEFAULT_SOVEREIGN_RISK_SCORE_FLOOR))
      })) as RebalanceConfig
      const normalizedSaved = normalizeLoadedConfig(saved)
      setServerConfig(normalizedSaved)
      setConfig((current) => {
        if (!current) return normalizedSaved
        return {
          ...current,
          scheduler_mode: normalizedSaved.scheduler_mode,
          sovereign_candidate_scope: normalizedSaved.sovereign_candidate_scope,
          sovereign_max_jobs_per_cycle: normalizedSaved.sovereign_max_jobs_per_cycle,
          sovereign_min_expected_profit_sat: normalizedSaved.sovereign_min_expected_profit_sat,
          sovereign_low_success_min_rate: normalizedSaved.sovereign_low_success_min_rate,
          sovereign_low_success_min_profit_cost_ratio: normalizedSaved.sovereign_low_success_min_profit_cost_ratio,
          sovereign_budget_efficiency_min_ratio: normalizedSaved.sovereign_budget_efficiency_min_ratio,
          sovereign_route_dead_source_share: normalizedSaved.sovereign_route_dead_source_share,
          sovereign_risk_score_floor: normalizedSaved.sovereign_risk_score_floor
        }
      })
      setStatus(t('rebalanceCenter.autopilot.saved'))
      void loadAll({ silent: true })
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.autopilot.saveFailed'))
    } finally {
      setAutopilotSaving(false)
    }
  }

  const handleResetMissionControl = async () => {
    if (!window.confirm(t('rebalanceCenter.overview.mcResetConfirm'))) {
      return
    }
    setMcResetBusy(true)
    setStatus(t('rebalanceCenter.overview.mcResetRunning'))
    try {
      await resetRebalanceMissionControl()
      const ovw = await getRebalanceOverview()
      setOverview(ovw as RebalanceOverview)
      setStatus(t('rebalanceCenter.overview.mcResetDone'))
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.overview.mcResetFailed'))
    } finally {
      setMcResetBusy(false)
    }
  }

  const handleToggleChannelAuto = async (channel: RebalanceChannel, enabled: boolean) => {
    bumpChannelStateVersion()
    const previous = channel.auto_enabled
    const previousManual = manualRestart[channel.channel_point] === true
    setChannels((prev) =>
      prev.map((ch) =>
        isSameChannel(ch, channel)
          ? { ...ch, auto_enabled: enabled, manual_restart_enabled: enabled ? false : ch.manual_restart_enabled }
          : ch
      )
    )
    if (enabled) {
      setManualRestart((prev) => ({ ...prev, [channel.channel_point]: false }))
    }
    try {
      await updateRebalanceChannelAuto({
        channel_point: channel.channel_point,
        auto_enabled: enabled
      })
      void loadAll({ silent: true })
    } catch (err) {
      setChannels((prev) =>
        prev.map((ch) =>
          isSameChannel(ch, channel)
            ? { ...ch, auto_enabled: previous, manual_restart_enabled: previousManual }
            : ch
        )
      )
      setManualRestart((prev) => ({ ...prev, [channel.channel_point]: previousManual }))
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleBulkAuto = async (enabled: boolean) => {
    if (sortedChannels.length === 0) return
    setStatus('')
    bumpChannelStateVersion()
    try {
      await Promise.all(
        sortedChannels.map((channel) =>
          updateRebalanceChannelAuto({
            channel_point: channel.channel_point,
            auto_enabled: enabled
          })
        )
      )
      void loadAll({ silent: true })
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleBulkExclude = async (excluded: boolean) => {
    if (sortedChannels.length === 0) return
    setStatus('')
    bumpChannelStateVersion()
    try {
      await Promise.all(
        sortedChannels.map((channel) =>
          updateRebalanceExclude({
            channel_point: channel.channel_point,
            excluded
          })
        )
      )
      void loadAll({ silent: true })
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleExcludeSource = async (channel: RebalanceChannel, excluded: boolean) => {
    bumpChannelStateVersion()
    const previous = channel.excluded_as_source
    setChannels((prev) => prev.map((ch) => (isSameChannel(ch, channel) ? { ...ch, excluded_as_source: excluded } : ch)))
    try {
      await updateRebalanceExclude({ channel_point: channel.channel_point, excluded })
      void loadAll({ silent: true })
    } catch (err) {
      setChannels((prev) => prev.map((ch) => (isSameChannel(ch, channel) ? { ...ch, excluded_as_source: previous } : ch)))
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleSaveChannelSettings = async (channel: RebalanceChannel) => {
    bumpChannelStateVersion()
    const key = channelKey(channel)
    const nextTargetValue = editTargets[key]
    const parsedTarget = nextTargetValue !== undefined ? Number(nextTargetValue) : channel.target_outbound_pct
    if (!Number.isFinite(parsedTarget) || parsedTarget <= 0 || parsedTarget >= 100) {
      setStatus(t('rebalanceCenter.invalidTarget'))
      return
    }
    const useDefault = channelUseDefaultEconRatio(channel)
    const autoBypassCostGate = channelAutoBypassCostGate(channel)
    const parsedEcon = parseLocaleDecimal(channelEconRatioInput(channel))
    if (!useDefault && (!Number.isFinite(parsedEcon) || parsedEcon < 0.01 || parsedEcon > 0.99)) {
      setStatus(t('rebalanceCenter.invalidEconRatio'))
      return
    }
    try {
      await updateRebalanceChannelTarget({
        channel_point: channel.channel_point,
        target_outbound_pct: parsedTarget,
        use_default_econ_ratio: useDefault,
        econ_ratio_override: useDefault ? undefined : parsedEcon,
        auto_bypass_cost_gate: autoBypassCostGate
      })
      setEditTargets((prev) => ({ ...prev, [key]: String(parsedTarget) }))
      setEditUseDefaultEconRatio((prev) => ({ ...prev, [key]: useDefault }))
      setEditEconRatios((prev) => ({
        ...prev,
        [key]: useDefault ? formatEconRatio(config?.econ_ratio ?? 0) : formatEconRatio(parsedEcon)
      }))
      setEditAutoBypassCostGate((prev) => ({ ...prev, [key]: autoBypassCostGate }))
      void loadAll({ silent: true })
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleUseDefaultEconRatioToggle = (channel: RebalanceChannel, checked: boolean) => {
    const key = channelKey(channel)
    setEditUseDefaultEconRatio((prev) => ({ ...prev, [key]: checked }))
    if (!checked) {
      const current = channelEconRatioInput(channel)
      if (!current || current.trim() === '') {
        setEditEconRatios((prev) => ({ ...prev, [key]: formatEconRatio(config?.econ_ratio ?? 0) }))
      }
    }
  }

  const handleAutoBypassCostGateToggle = async (channel: RebalanceChannel, checked: boolean) => {
    const key = channelKey(channel)
    const previous = channelAutoBypassCostGate(channel)
    setEditAutoBypassCostGate((prev) => ({ ...prev, [key]: checked }))
    setChannels((prev) =>
      prev.map((ch) => (isSameChannel(ch, channel) ? { ...ch, auto_bypass_cost_gate: checked } : ch))
    )
    try {
      await updateRebalanceChannelTarget({
        channel_point: channel.channel_point,
        auto_bypass_cost_gate: checked
      })
      void loadAll({ silent: true })
    } catch (err) {
      setEditAutoBypassCostGate((prev) => ({ ...prev, [key]: previous }))
      setChannels((prev) =>
        prev.map((ch) => (isSameChannel(ch, channel) ? { ...ch, auto_bypass_cost_gate: previous } : ch))
      )
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleRunRebalance = async (channel: RebalanceChannel) => {
    const nextValue = editTargets[channelKey(channel)]
    const parsed = nextValue ? Number(nextValue) : channel.target_outbound_pct
    const autoRestart = manualRestart[channel.channel_point] === true
    try {
      await runRebalance({
        channel_point: channel.channel_point,
        target_outbound_pct: parsed,
        auto_restart: autoRestart
      })
      void loadAll({ silent: true })
    } catch (err) {
      const message = err instanceof Error ? err.message : ''
      if (message.includes('manual budget exhausted')) {
        setStatus(t('rebalanceCenter.runFailedBudgetExhausted'))
      } else if (message.includes('manual budget insufficient')) {
        setStatus(t('rebalanceCenter.runFailedBudgetInsufficient'))
      } else if (message.includes('manual restart cooldown active')) {
        setStatus(t('rebalanceCenter.runFailedCooldown'))
      } else {
        setStatus(message || t('rebalanceCenter.runFailed'))
      }
    }
  }

  const handleTogglePairStats = async (channel: RebalanceChannel) => {
    const key = channelKey(channel)
    const willOpen = !pairStatsOpen[key]
    setPairStatsOpen((prev) => ({ ...prev, [key]: willOpen }))
    if (!willOpen || pairStatsLoading[key]) {
      return
    }
    setPairStatsLoading((prev) => ({ ...prev, [key]: true }))
    setPairStatsError((prev) => ({ ...prev, [key]: false }))
    try {
      const response = await getRebalancePairStats(channel.channel_point)
      const pairs = Array.isArray((response as any)?.pairs) ? (response as any).pairs : []
      setPairStatsByChannel((prev) => ({ ...prev, [key]: pairs }))
    } catch {
      setPairStatsError((prev) => ({ ...prev, [key]: true }))
    } finally {
      setPairStatsLoading((prev) => ({ ...prev, [key]: false }))
    }
  }

  const handleManualRestartToggle = async (channel: RebalanceChannel, enabled: boolean) => {
    bumpChannelStateVersion()
    const key = channel.channel_point
    const previous = manualRestart[key] === true
    const previousAuto = channel.auto_enabled
    setManualRestart((prev) => ({ ...prev, [key]: enabled }))
    setChannels((prev) =>
      prev.map((ch) =>
        isSameChannel(ch, channel)
          ? { ...ch, manual_restart_enabled: enabled, auto_enabled: enabled ? false : ch.auto_enabled }
          : ch
      )
    )
    try {
      await updateRebalanceChannelManualRestart({
        channel_point: channel.channel_point,
        enabled
      })
      void loadAll({ silent: true })
    } catch (err) {
      setManualRestart((prev) => ({ ...prev, [key]: previous }))
      setChannels((prev) =>
        prev.map((ch) =>
          isSameChannel(ch, channel)
            ? { ...ch, manual_restart_enabled: previous, auto_enabled: previousAuto }
            : ch
        )
      )
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }
  const profitSkipDetails = useMemo(() => {
    if (!overview?.last_scan_skipped) return []
    return overview.last_scan_skipped.filter((item) => item.reason === 'profit_guardrail')
  }, [overview])
  const diagnosticSkipGroups = useMemo(() => {
    const groups: Record<string, RebalanceScanSkip[]> = {}
    if (!overview?.last_scan_skipped) return groups
    overview.last_scan_skipped.forEach((item) => {
      if (item.reason === 'profit_guardrail') return
      if (!groups[item.reason]) {
        groups[item.reason] = []
      }
      groups[item.reason].push(item)
    })
    return groups
  }, [overview])
  const diagnosticReasons = useMemo(() => {
    return Object.entries(diagnosticSkipGroups).map(([reason, items]) => ({
      reason,
      count: items.length
    }))
  }, [diagnosticSkipGroups])
  const diagnosticSkipTotal = useMemo(() => {
    return diagnosticReasons.reduce((sum, entry) => sum + entry.count, 0)
  }, [diagnosticReasons])
  const diagnosticSkipDetails = useMemo(() => {
    if (scanDetailsReason === 'all') {
      return Object.values(diagnosticSkipGroups).flat()
    }
    return diagnosticSkipGroups[scanDetailsReason] ?? []
  }, [diagnosticSkipGroups, scanDetailsReason])
  const autopilotDecisions = overview?.sovereign_decisions ?? []
  const autopilotSelectedDecisions = autopilotDecisions.filter((decision) => decision.selected)
  const autopilotSkippedDecisions = autopilotDecisions.filter((decision) => !decision.selected)
  const autopilotHistory24h = overview?.sovereign_history_24h ?? []
  const autopilotHistoryTotals = useMemo(() => {
    return autopilotHistory24h.reduce(
      (acc, entry) => ({
        scans: acc.scans + 1,
        selected: acc.selected + (entry.selected ?? 0),
        expectedProfit: acc.expectedProfit + (entry.expected_profit_sat ?? 0)
      }),
      { scans: 0, selected: 0, expectedProfit: 0 }
    )
  }, [autopilotHistory24h])
  const formatSkipReason = (reason: string) => {
    switch (reason) {
      case 'roi_guardrail':
        return t('rebalanceCenter.overview.skipReasonRoi')
      case 'profit_guardrail':
        return t('rebalanceCenter.overview.skipReasonProfit')
      case 'channel_busy':
        return t('rebalanceCenter.overview.skipReasonBusy')
      case 'recently_attempted':
        return t('rebalanceCenter.overview.skipReasonRecent')
      case 'target_cooldown':
        return t('rebalanceCenter.overview.scanReasonTargetCooldown')
      case 'target_cooldown_probe_backoff':
        return t('rebalanceCenter.overview.scanReasonTargetCooldownProbeBackoff')
      case 'target_cooldown_probe_deferred':
        return t('rebalanceCenter.overview.scanReasonTargetCooldownProbeDeferred')
      case 'target_cooldown_probe_busy':
        return t('rebalanceCenter.overview.scanReasonTargetCooldownProbeBusy')
      case 'below_execute_min':
        return t('rebalanceCenter.overview.scanReasonBudgetMin')
      case 'expected_profit_below_min':
        return t('rebalanceCenter.overview.scanReasonExpectedProfit')
      case 'cycle_limit':
        return t('rebalanceCenter.overview.scanReasonCycleLimit')
      case 'cooldown_probe_not_sovereign':
        return t('rebalanceCenter.overview.scanReasonCooldownProbeNotSovereign')
      case 'target_structural_cooldown':
        return t('rebalanceCenter.overview.scanReasonTargetStructuralCooldown')
      case 'low_success_opportunity_below_floor':
        return t('rebalanceCenter.overview.scanReasonLowSuccessOpportunity')
      case 'budget_efficiency_below_floor':
        return t('rebalanceCenter.overview.scanReasonBudgetEfficiency')
      case 'route_dead_opportunity_below_floor':
        return t('rebalanceCenter.overview.scanReasonRouteDeadOpportunity')
      case 'would_queue':
        return t('rebalanceCenter.overview.scanReasonWouldQueue')
      case 'queued':
        return t('rebalanceCenter.overview.scanDetailQueued')
      default:
        return reason
    }
  }
  const formatScanReason = (reason: string) => {
    switch (reason) {
      case 'channel_busy':
        return t('rebalanceCenter.overview.scanReasonBusy')
      case 'target_already_balanced':
        return t('rebalanceCenter.overview.scanReasonBalanced')
      case 'recently_attempted':
        return t('rebalanceCenter.overview.scanReasonRecent')
      case 'target_cooldown':
        return t('rebalanceCenter.overview.scanReasonTargetCooldown')
      case 'target_cooldown_probe_backoff':
        return t('rebalanceCenter.overview.scanReasonTargetCooldownProbeBackoff')
      case 'target_cooldown_probe_deferred':
        return t('rebalanceCenter.overview.scanReasonTargetCooldownProbeDeferred')
      case 'target_cooldown_probe_busy':
        return t('rebalanceCenter.overview.scanReasonTargetCooldownProbeBusy')
      case 'target_not_eligible':
        return t('rebalanceCenter.overview.scanReasonTargetNotEligible')
      case 'roi_guardrail':
        return t('rebalanceCenter.overview.scanReasonRoi')
      case 'profit_guardrail':
        return t('rebalanceCenter.overview.scanReasonProfit')
      case 'fee_cap_zero':
        return t('rebalanceCenter.overview.scanReasonFeeCap')
      case 'below_execute_min':
        return t('rebalanceCenter.overview.scanReasonBudgetMin')
      case 'budget_below_min':
        return t('rebalanceCenter.overview.scanReasonBudgetMin')
      case 'budget_too_low':
        return t('rebalanceCenter.overview.scanReasonBudgetLow')
      case 'expected_profit_below_min':
        return t('rebalanceCenter.overview.scanReasonExpectedProfit')
      case 'cycle_limit':
        return t('rebalanceCenter.overview.scanReasonCycleLimit')
      case 'cooldown_probe_not_sovereign':
        return t('rebalanceCenter.overview.scanReasonCooldownProbeNotSovereign')
      case 'target_structural_cooldown':
        return t('rebalanceCenter.overview.scanReasonTargetStructuralCooldown')
      case 'low_success_opportunity_below_floor':
        return t('rebalanceCenter.overview.scanReasonLowSuccessOpportunity')
      case 'budget_efficiency_below_floor':
        return t('rebalanceCenter.overview.scanReasonBudgetEfficiency')
      case 'route_dead_opportunity_below_floor':
        return t('rebalanceCenter.overview.scanReasonRouteDeadOpportunity')
      case 'sovereign_live':
        return t('rebalanceCenter.overview.scanReasonSovereignLive')
      case 'target_not_found':
        return t('rebalanceCenter.overview.scanReasonTargetNotFound')
      case 'start_error':
        return t('rebalanceCenter.overview.scanReasonStartError')
      default:
        return reason
    }
  }
  const formatReasonSummary = (reasons?: Record<string, number>, limit = 3) => {
    const entries = Object.entries(reasons ?? {})
      .filter(([, count]) => count > 0)
      .sort((a, b) => b[1] - a[1])
      .slice(0, limit)
    return entries.map(([reason, count]) => `${formatScanReason(reason)}: ${formatter.format(count)}`).join(', ')
  }
  const scanDetailText = useMemo(() => {
    if (!overview) return ''
    const reasons = overview.last_scan_reasons ?? {}
    const entries = Object.entries(reasons).filter(([, count]) => count > 0)
    if (entries.length > 0) {
      const ordered = ['channel_busy', 'target_already_balanced', 'target_not_eligible', 'recently_attempted', 'target_cooldown', 'target_structural_cooldown', 'target_cooldown_probe_backoff', 'target_cooldown_probe_deferred', 'target_cooldown_probe_busy', 'roi_guardrail', 'profit_guardrail', 'expected_profit_below_min', 'cycle_limit', 'cooldown_probe_not_sovereign', 'route_dead_opportunity_below_floor', 'low_success_opportunity_below_floor', 'budget_efficiency_below_floor', 'fee_cap_zero', 'below_execute_min', 'budget_below_min', 'budget_too_low', 'target_not_found', 'start_error']
      entries.sort((a, b) => {
        const ai = ordered.indexOf(a[0])
        const bi = ordered.indexOf(b[0])
        const ap = ai === -1 ? Number.MAX_SAFE_INTEGER : ai
        const bp = bi === -1 ? Number.MAX_SAFE_INTEGER : bi
        if (ap !== bp) return ap - bp
        return a[0].localeCompare(b[0])
      })
      const reasonsText = entries.map(([key, count]) => `${formatScanReason(key)}: ${count}`).join(', ')
      const parts = [
        overview.last_scan_status === 'queued'
          ? t('rebalanceCenter.overview.scanDetailQueued')
          : t('rebalanceCenter.overview.scanDetailNoJobs')
      ]
      if ((overview.last_scan_candidates ?? 0) > 0) {
        parts.push(t('rebalanceCenter.overview.scanDetailCandidates', { count: overview.last_scan_candidates }))
      }
      if ((overview.last_scan_remaining_budget_sat ?? 0) > 0) {
        parts.push(t('rebalanceCenter.overview.scanDetailRemaining', { value: formatSats(overview.last_scan_remaining_budget_sat ?? 0) }))
      }
      parts.push(t('rebalanceCenter.overview.scanDetailReasons', { value: reasonsText }))
      return parts.join(' ')
    }
    if (overview.last_scan_detail) {
      return t('rebalanceCenter.overview.scanDetail', { value: overview.last_scan_detail })
    }
    return ''
  }, [overview, t])
  const manualRestartDetailText = useMemo(() => {
    if (!overview) return ''
    const reasons = overview.last_manual_restart_reasons ?? {}
    const entries = Object.entries(reasons).filter(([, count]) => count > 0)
    if (entries.length === 0) return ''
    const ordered = ['channel_busy', 'target_already_balanced', 'target_not_eligible', 'target_cooldown', 'target_cooldown_probe_backoff', 'target_cooldown_probe_deferred', 'target_cooldown_probe_busy', 'roi_guardrail', 'below_execute_min', 'budget_below_min', 'budget_too_low', 'target_not_found', 'start_error']
    entries.sort((a, b) => {
      const ai = ordered.indexOf(a[0])
      const bi = ordered.indexOf(b[0])
      const ap = ai === -1 ? Number.MAX_SAFE_INTEGER : ai
      const bp = bi === -1 ? Number.MAX_SAFE_INTEGER : bi
      if (ap !== bp) return ap - bp
      return a[0].localeCompare(b[0])
    })
    const reasonsText = entries.map(([key, count]) => `${formatScanReason(key)}: ${count}`).join(', ')
    return t('rebalanceCenter.overview.manualRestartReasons', { value: reasonsText })
  }, [overview, t])

  const togglePaybackFlag = (flag: number) => {
    if (!config) return
    const next = config.payback_mode_flags ^ flag
    setConfig({ ...config, payback_mode_flags: next })
  }
  const remainingTotalSat = overview?.remaining_total_sat ?? 0
  const remainingForAutoSat = overview?.remaining_for_auto_sat ?? 0
  const manualReserveRemainingSat = overview?.manual_reserve_remaining_sat ?? 0
  const budgetUnlimited = overview?.budget_unlimited ?? config?.budget_unlimited ?? false
  const budgetAutoOnly = overview?.budget_auto_only ?? config?.budget_auto_only ?? false
  const manualRestartBudgetEnforced = !budgetUnlimited && !budgetAutoOnly
  const autoBudgetCapSat = Math.max(0, (overview?.daily_budget_sat ?? 0) - (overview?.manual_reserve_sat ?? 0))
  const manualReserveEncroached =
    manualRestartBudgetEnforced &&
    manualReserveRemainingSat > 0 &&
    remainingTotalSat > 0 &&
    manualReserveRemainingSat > remainingTotalSat
  const autoBudgetBlockedCount =
    (overview?.last_scan_reasons?.budget_below_min ?? 0) +
    (overview?.last_scan_reasons?.budget_too_low ?? 0)
  const autoBudgetTight =
    Boolean(overview?.auto_enabled) &&
    !budgetUnlimited &&
    remainingForAutoSat > 0 &&
    autoBudgetBlockedCount > 0
  const autoBudgetPaused =
    Boolean(overview?.auto_enabled) &&
    !budgetUnlimited &&
    remainingForAutoSat <= 0
  const manualRestartBudgetPaused =
    manualRestartBudgetEnforced &&
    remainingTotalSat <= 0
  const budgetPauseMessageKey =
    autoBudgetPaused && manualRestartBudgetPaused
      ? 'bothExhausted'
      : autoBudgetPaused
      ? budgetAutoOnly
        ? 'autoOnlyExhausted'
        : 'autoExhausted'
      : manualRestartBudgetPaused
      ? 'manualExhausted'
      : ''
  const autoBudgetInsufficient =
    Boolean(overview?.auto_enabled) &&
    !budgetUnlimited &&
    !autoBudgetPaused &&
    overview?.last_scan_status === 'budget_insufficient'
  const activeSchedulerMode = overview?.scheduler_mode || config?.scheduler_mode || REBALANCE_DEFAULT_SCHEDULER_MODE
  const autopilotModeActive = activeSchedulerMode === 'sovereign_shadow' || activeSchedulerMode === 'sovereign_live'
  const autopilotConfigDirty = autopilotConfigSignature(config) !== autopilotConfigSignature(serverConfig)
  const renderPairStatsPanel = (channel: RebalanceChannel) => {
    const key = channelKey(channel)
    return (
      <PairStatsPanel
        pairs={pairStatsByChannel[key] ?? []}
        loading={pairStatsLoading[key] === true}
        failed={pairStatsError[key] === true}
        t={t}
        formatTimestamp={formatTimestamp}
        formatRoi={formatRoi}
        formatSats={formatSats}
      />
    )
  }

  return (
    <section className="space-y-6" aria-busy={initialLoading || isRefreshing}>
      <div className="section-card flex flex-wrap items-center justify-between gap-4">
        <div>
          <h2 className="text-2xl font-semibold">{t('rebalanceCenter.title')}</h2>
          <p className="text-fog/60">{t('rebalanceCenter.subtitle')}</p>
        </div>
        {autopilotModeActive && (
          <div className={`rounded-lg border px-3 py-2 text-right ${activeSchedulerMode === 'sovereign_live' ? 'border-emerald-400/30 bg-emerald-400/10' : 'border-cyan-400/25 bg-cyan-400/10'}`}>
            <p className={`text-xs uppercase tracking-wide ${activeSchedulerMode === 'sovereign_live' ? 'text-emerald-200' : 'text-cyan-200'}`}>
              {t('rebalanceCenter.autopilot.headerStatus', {
                mode: t(`rebalanceCenter.settings.schedulerModeOptions.${activeSchedulerMode}`)
              })}
            </p>
            <p className="text-xs text-fog/55">
              {t('rebalanceCenter.autopilot.headerSummary', {
                candidates: formatter.format(overview?.sovereign_candidates ?? 0),
                selected: formatter.format(overview?.sovereign_selected ?? 0)
              })}
            </p>
          </div>
        )}
      </div>

      {status && <p className="text-sm text-brass">{status}</p>}
      {initialLoading && <p className="text-sm text-fog/60">{t('rebalanceCenter.loading')}</p>}

      {overview && (
        <div className="grid gap-4 lg:grid-cols-4">
            <div className="section-card space-y-2">
              <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.liveCost')}</p>
              <p className="text-lg font-semibold text-fog">{formatSats(overview.live_cost_sat)}</p>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.last24h')}</p>
              <p className={budgetUnlimited ? 'text-xs text-amber-200' : 'text-xs text-fog/50'}>
                {t('rebalanceCenter.overview.eligibleCounts', {
                  sources: overview.eligible_sources ?? 0,
                  targets: overview.targets_needing ?? 0
                })}
              </p>
              <p className="text-xs text-fog/50">
                {t('rebalanceCenter.overview.lastScan', {
                  value: overview.auto_enabled
                    ? formatTimestamp(overview.last_scan_at)
                    : t('rebalanceCenter.overview.lastScanDisabled')
                })}
              </p>
              {overview.auto_enabled && overview.last_scan_status && (
                <p className="text-xs text-fog/50">
                  {t(`rebalanceCenter.overview.scanStatus.${overview.last_scan_status}`)}
                </p>
              )}
              {overview.auto_enabled && scanDetailText && (
                <div className="flex flex-wrap items-center gap-2 text-xs text-amber-200">
                  <span>{scanDetailText}</span>
                  {diagnosticSkipTotal > 0 && (
                    <button
                      type="button"
                      className="text-[11px] underline underline-offset-2 text-fog/70"
                      onClick={() => {
                        setScanDetailsOpen((prev) => !prev)
                        setScanDetailsReason('all')
                        setScanDetailsShowAll(false)
                      }}
                    >
                      {t('rebalanceCenter.overview.skipDetails')}
                    </button>
                  )}
                </div>
              )}
              {overview.auto_enabled && (overview.last_scan_queued ?? 0) > 0 && (
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.lastQueued', { count: overview.last_scan_queued })}
                </p>
              )}
              {config?.manual_restart_watch && (overview.last_manual_restart_queued ?? 0) > 0 && (
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.lastManualRestartQueued', { count: overview.last_manual_restart_queued })}
                </p>
              )}
              {config?.manual_restart_watch && manualRestartDetailText && (
                <p className="text-xs text-fog/50">{manualRestartDetailText}</p>
              )}
              {overview.auto_enabled && typeof overview.last_scan_top_score_sat === 'number' && overview.last_scan_top_score_sat > 0 && (
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.topScore', { value: formatSats(overview.last_scan_top_score_sat) })}
                </p>
              )}
              {overview.auto_enabled && (overview.last_scan_profit_skipped ?? 0) > 0 && (
                <div className="flex flex-wrap items-center gap-2 text-xs text-amber-200">
                  <span>{t('rebalanceCenter.overview.profitSkipped', { count: overview.last_scan_profit_skipped })}</span>
                  {profitSkipDetails.length > 0 && (
                    <button
                      type="button"
                      className="text-[11px] underline underline-offset-2 text-fog/70"
                      onClick={() => setSkipDetailsOpen((prev) => !prev)}
                    >
                      {t('rebalanceCenter.overview.skipDetails')}
                    </button>
                  )}
                </div>
              )}
              {scanDetailsOpen && diagnosticSkipTotal > 0 && (
                <div className="mt-2 space-y-2 text-[11px] text-fog/70">
                  {diagnosticReasons.length > 1 && (
                    <div className="flex flex-wrap items-center gap-2">
                      <button
                        type="button"
                        className={`rounded-full border px-2 py-0.5 text-[10px] ${scanDetailsReason === 'all' ? 'border-emerald-400/70 text-emerald-200' : 'border-white/10 text-fog/60'}`}
                        onClick={() => {
                          setScanDetailsReason('all')
                          setScanDetailsShowAll(false)
                        }}
                      >
                        {t('common.all')} ({diagnosticSkipTotal})
                      </button>
                      {diagnosticReasons.map((entry) => (
                        <button
                          key={entry.reason}
                          type="button"
                          className={`rounded-full border px-2 py-0.5 text-[10px] ${scanDetailsReason === entry.reason ? 'border-emerald-400/70 text-emerald-200' : 'border-white/10 text-fog/60'}`}
                          onClick={() => {
                            setScanDetailsReason(entry.reason)
                            setScanDetailsShowAll(false)
                          }}
                        >
                          {formatSkipReason(entry.reason)} ({entry.count})
                        </button>
                      ))}
                    </div>
                  )}
                  <div className="space-y-2">
                    {(scanDetailsShowAll ? diagnosticSkipDetails : diagnosticSkipDetails.slice(0, 12)).map((item) => (
                      <div key={`${item.channel_point || item.channel_id}-${item.reason}`} className="rounded-lg border border-white/10 bg-white/5 p-2">
                        <div className="text-fog/80">
                          {item.peer_alias || item.channel_point}
                        </div>
                        <div>
                          {formatSkipReason(item.reason)}
                          {' · '}
                          {t('rebalanceCenter.overview.skipCalc', {
                            gain: formatSats(item.expected_gain_sat),
                            cost: formatSats(item.estimated_cost_sat),
                            roi: item.expected_roi_valid ? formatRoi(item.expected_roi) : 'n/a'
                          })}
                        </div>
                        <div className="text-fog/60">
                          {t('rebalanceCenter.overview.skipTarget', {
                            target: formatPct(item.target_outbound_pct),
                            amount: formatSats(item.target_amount_sat)
                          })}
                        </div>
                      </div>
                    ))}
                  </div>
                  {!scanDetailsShowAll && diagnosticSkipDetails.length > 12 && (
                    <button
                      type="button"
                      className="text-[11px] underline underline-offset-2 text-fog/70"
                      onClick={() => setScanDetailsShowAll(true)}
                    >
                      {t('rebalanceCenter.overview.showAllDetails', { count: diagnosticSkipDetails.length - 12 })}
                    </button>
                  )}
                </div>
              )}
              {skipDetailsOpen && profitSkipDetails.length > 0 && (
                <div className="mt-2 space-y-2 text-[11px] text-fog/70">
                  {profitSkipDetails.map((item) => (
                    <div key={item.channel_point || item.channel_id} className="rounded-lg border border-white/10 bg-white/5 p-2">
                      <div className="text-fog/80">
                        {item.peer_alias || item.channel_point}
                      </div>
                      <div>
                        {formatSkipReason(item.reason)}
                        {' · '}
                        {t('rebalanceCenter.overview.skipCalc', {
                          gain: formatSats(item.expected_gain_sat),
                          cost: formatSats(item.estimated_cost_sat),
                          roi: item.expected_roi_valid ? formatRoi(item.expected_roi) : 'n/a'
                        })}
                      </div>
                      <div className="text-fog/60">
                        {t('rebalanceCenter.overview.skipTarget', {
                          target: formatPct(item.target_outbound_pct),
                          amount: formatSats(item.target_amount_sat)
                        })}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          <div className="section-card space-y-3">
            <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.effectiveness')}</p>
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              <div className="rounded-xl border border-white/10 bg-white/5 p-2.5 space-y-1">
                <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.effectivenessOperational')}</p>
                <p className="text-sm font-semibold text-fog">{formatPct((overview.effectiveness_7d || 0) * 100)}</p>
              </div>
              <div className="rounded-xl border border-white/10 bg-white/5 p-2.5 space-y-1">
                <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.effectivenessExecution')}</p>
                <p className="text-sm font-semibold text-fog">{formatPct(((overview.effectiveness_execution_7d ?? overview.effectiveness_7d) || 0) * 100)}</p>
              </div>
            </div>
            <p className="text-xs text-fog/50">
              {t('rebalanceCenter.overview.jobsWithoutAttempt7d', {
                value: formatter.format(overview.jobs_without_attempt_7d ?? 0)
              })}
            </p>
            <p className="text-xs text-fog/50">
              {t('rebalanceCenter.overview.jobsWithoutAttemptRate7d', {
                value: formatPct((overview.jobs_without_attempt_rate_7d ?? 0) * 100)
              })}
            </p>
            <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.roi', { value: overview.roi_7d.toFixed(2) })}</p>
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              <div className="rounded-xl border border-white/10 bg-white/5 p-2.5 space-y-1">
                <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.paybackGroupRebalanced')}</p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.paybackRevenueRebalanced', { value: formatSats(overview.payback_revenue_rebalanced_sat || 0) })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.paybackProgressRebalanced', { value: formatPct((overview.payback_progress_rebalanced || 0) * 100) })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.paybackCost', { value: formatSats(overview.payback_cost_sat || 0) })}
                </p>
              </div>
              <div className="rounded-xl border border-white/10 bg-white/5 p-2.5 space-y-1">
                <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.paybackGroupAll')}</p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.paybackRevenueAll', { value: formatSats(overview.payback_revenue_sat || 0) })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.paybackProgress', { value: formatPct((overview.payback_progress || 0) * 100) })}
                </p>
              </div>
            </div>
          </div>
            <div className="section-card space-y-2">
              <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.dailyBudget')}</p>
              <p className="text-lg font-semibold text-fog">{formatSats(overview.daily_budget_sat)}</p>
              <p className="text-xs text-fog/50">
                {budgetUnlimited ? (
                  <>
                    {t('rebalanceCenter.overview.budgetMode', {
                      mode: t(`rebalanceCenter.settings.budgetModeOptions.${config?.budget_mode || REBALANCE_DEFAULT_BUDGET_MODE}`)
                    })}
                    {' · '}
                    <span className="font-semibold text-amber-200">{t('rebalanceCenter.overview.budgetModeUnlimitedBadge')}</span>
                  </>
                ) : budgetAutoOnly
                  ? t('rebalanceCenter.overview.budgetModeAutoOnly', {
                      mode: t(`rebalanceCenter.settings.budgetModeOptions.${config?.budget_mode || REBALANCE_DEFAULT_BUDGET_MODE}`)
                    })
                  : overview.manual_reserve_enabled
                  ? t('rebalanceCenter.overview.budgetModeWithReserve', {
                      mode: t(`rebalanceCenter.settings.budgetModeOptions.${config?.budget_mode || REBALANCE_DEFAULT_BUDGET_MODE}`)
                    })
                  : t('rebalanceCenter.overview.budgetMode', {
                      mode: t(`rebalanceCenter.settings.budgetModeOptions.${config?.budget_mode || REBALANCE_DEFAULT_BUDGET_MODE}`)
                    })}
              </p>
              {typeof overview.daily_budget_base_sat === 'number' && overview.daily_budget_base_sat > 0 && (
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.dailyBudgetBase', { value: formatSats(overview.daily_budget_base_sat) })}
                </p>
              )}
              {typeof overview.daily_budget_short_term_sat === 'number' && (
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.dailyBudgetShortTerm', { value: formatSats(overview.daily_budget_short_term_sat) })}
                </p>
              )}
              <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.dailySpentAuto', { value: formatSats(overview.daily_spent_auto_sat) })}</p>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.dailySpentManual', { value: formatSats(overview.daily_spent_manual_sat) })}</p>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.remainingTotal', { value: formatSats(remainingTotalSat) })}</p>
              {overview.manual_reserve_enabled && (
                <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.autoBudgetCap', { value: formatSats(autoBudgetCapSat) })}</p>
              )}
              <p className={budgetUnlimited ? 'text-xs text-amber-200' : 'text-xs text-fog/50'}>
                {budgetUnlimited
                  ? t('rebalanceCenter.overview.autoBudgetUnlimited', { value: formatSats(remainingForAutoSat) })
                  : t('rebalanceCenter.overview.autoUsableBudget', { value: formatSats(remainingForAutoSat) })}
              </p>
              {manualRestartBudgetEnforced ? (
                <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.manualRestartUsableBudget', { value: formatSats(remainingTotalSat) })}</p>
              ) : (
                <p className={budgetUnlimited ? 'text-xs text-amber-200' : 'text-xs text-emerald-200'}>
                  {budgetUnlimited
                    ? t('rebalanceCenter.overview.manualRestartBudgetUnlimited', { value: formatSats(remainingTotalSat) })
                    : t('rebalanceCenter.overview.manualRestartIgnoresBudget')}
                </p>
              )}
              {overview.manual_reserve_enabled && (
                <>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.manualReserve', {
                      value: formatSats(overview.manual_reserve_sat ?? 0),
                      mode: t(`rebalanceCenter.settings.manualReserveModeOptions.${overview.manual_reserve_mode || 'fixed_sat'}`)
                    })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.manualReserveRemaining', {
                      value: formatSats(manualReserveRemainingSat)
                    })}
                  </p>
                  {manualReserveRemainingSat > 0 && (
                    <p className="text-xs text-fog/40">
                      {budgetUnlimited
                        ? t('rebalanceCenter.overview.manualReserveUnlimited')
                        : budgetAutoOnly
                        ? t('rebalanceCenter.overview.manualReserveAutoOnly')
                        : t('rebalanceCenter.overview.manualReserveNotExtra')}
                    </p>
                  )}
                </>
              )}
              {manualReserveEncroached && (
                <p className="text-xs text-amber-200">{t('rebalanceCenter.overview.manualReserveEncroached')}</p>
              )}
              {autoBudgetTight && (
                <p className="text-xs text-amber-200">
                  {t('rebalanceCenter.overview.autoBudgetTight', {
                    count: autoBudgetBlockedCount,
                    budget: formatSats(remainingForAutoSat)
                  })}
                </p>
              )}
              {budgetPauseMessageKey && (
                <p className="text-xs text-amber-200">
                  {t(`rebalanceCenter.overview.budgetPaused.${budgetPauseMessageKey}`)}
                </p>
              )}
              {autoBudgetInsufficient && (
                <p className="text-xs text-amber-200">
                  {t('rebalanceCenter.overview.budgetPaused.autoInsufficient')}
                </p>
              )}
              <MetricDisclosure
                title={t('rebalanceCenter.overview.metrics24h')}
                summary={t('rebalanceCenter.overview.metrics24hSummary', {
                  attempts: formatter.format(overview.attempts_24h ?? 0),
                  successes: formatter.format(overview.success_attempts_24h ?? 0),
                  rate: formatPct((overview.attempt_success_rate_24h ?? 0) * 100)
                })}
                open={metrics24hOpen}
                onToggle={() => setMetrics24hOpen((value) => !value)}
              >
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.attempts24h', {
                    value: formatter.format(overview.attempts_24h ?? 0)
                  })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.failedAttempts24h', {
                    value: formatter.format(overview.failed_attempts_24h ?? 0)
                  })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.attemptSuccessRate24h', {
                    value: formatPct((overview.attempt_success_rate_24h ?? 0) * 100)
                  })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.attemptsPerSuccess24h', {
                    value: overview.attempts_per_success_attempt_24h
                      ? formatter.format(Math.round(overview.attempts_per_success_attempt_24h * 10) / 10)
                      : '0'
                  })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.successSatsPerAttempt24h', {
                    value: formatSats(overview.success_sats_per_attempt_24h ?? 0)
                  })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.successAttempts24h', {
                    value: formatter.format(overview.success_attempts_24h ?? 0)
                  })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.successAmount24h', {
                    value: formatSats(overview.success_amount_24h_sat ?? 0)
                  })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.successAvgAmount24h', {
                    value: formatSats(overview.success_avg_amount_24h_sat ?? 0)
                  })}
                </p>
                {effectiveExecuteMinSat(config) > 0 && (
                  <>
                    <p className="text-xs text-fog/50">
                      {t('rebalanceCenter.overview.belowMinAttempts24h', {
                        value: formatter.format(overview.success_below_min_attempts_24h ?? 0),
                        min: formatSats(effectiveExecuteMinSat(config))
                      })}
                    </p>
                    <p className="text-xs text-fog/50">
                      {t('rebalanceCenter.overview.belowMinAmount24h', {
                        value: formatSats(overview.success_below_min_amount_24h_sat ?? 0)
                      })}
                    </p>
                    <p className="text-xs text-fog/50">
                      {t('rebalanceCenter.overview.belowMinRate24h', {
                        value: formatPct((overview.success_below_min_rate_24h ?? 0) * 100)
                      })}
                    </p>
                  </>
                )}
                {(overview.fast_path_attempts_24h ?? 0) > 0 && (
                  <p className="text-xs text-mint/80">
                    {t('rebalanceCenter.overview.fastPath24h', {
                      attempts: formatter.format(overview.fast_path_attempts_24h ?? 0),
                      successes: formatter.format(overview.fast_path_successes_24h ?? 0),
                      rate: formatPct((overview.fast_path_hit_rate_24h ?? 0) * 100)
                    })}
                  </p>
                )}
              </MetricDisclosure>
            </div>
          <div className="section-card space-y-2">
            <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.autoMode')}</p>
            <p className={`text-lg font-semibold ${overview.auto_enabled ? 'text-emerald-200' : 'text-fog'}`}>
              {overview.auto_enabled ? t('common.enabled') : t('common.disabled')}
            </p>
            <p className="text-xs text-fog/50">
              {t('rebalanceCenter.overview.schedulerMode', {
                mode: t(`rebalanceCenter.settings.schedulerModeOptions.${overview.scheduler_mode || config?.scheduler_mode || REBALANCE_DEFAULT_SCHEDULER_MODE}`)
              })}
            </p>
            {(overview.scheduler_mode === 'sovereign_shadow' || overview.scheduler_mode === 'sovereign_live') && (
              <>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.autopilotLastDecision', {
                    value: overview.sovereign_last_decision_at
                      ? formatTimestamp(overview.sovereign_last_decision_at)
                      : t('rebalanceCenter.overview.autopilotNoDecision')
                  })}
                </p>
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.autopilotSummary', {
                    candidates: formatter.format(overview.sovereign_candidates ?? 0),
                    selected: formatter.format(overview.sovereign_selected ?? 0),
                    profit: formatSats(overview.sovereign_expected_profit_sat ?? 0)
                  })}
                </p>
              </>
            )}
            <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.scanInterval', { value: config?.scan_interval_sec || '-' })}</p>
            <div className="mt-2 border-t border-white/10 pt-2">
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="min-w-0 space-y-1">
                  <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.missionControl')}</p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mcLastReset', {
                      value: overview.last_mc_reset_at
                        ? formatTimestamp(overview.last_mc_reset_at)
                        : t('rebalanceCenter.overview.mcNeverReset')
                    })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mcResetCount', {
                      value: formatter.format(overview.mc_reset_count ?? 0)
                    })}
                    {overview.last_mc_reset_reason
                      ? ` · ${t('rebalanceCenter.overview.mcResetReason', { reason: overview.last_mc_reset_reason })}`
                      : ''}
                  </p>
                  {(overview.mc_reset_cooldown_remaining_sec ?? 0) > 0 && (
                    <p className="text-xs text-amber-200">
                      {t('rebalanceCenter.overview.mcCooldownRemaining', {
                        value: formatter.format(overview.mc_reset_cooldown_remaining_sec ?? 0)
                      })}
                    </p>
                  )}
                </div>
                <button
                  type="button"
                  className="btn-secondary text-xs px-3 py-1"
                  title={t('rebalanceCenter.overview.mcResetHint')}
                  disabled={mcResetBusy}
                  onClick={handleResetMissionControl}
                >
                  {mcResetBusy
                    ? t('rebalanceCenter.overview.mcResetRunningShort')
                    : t('rebalanceCenter.overview.mcResetAction')}
                </button>
              </div>
            </div>
            <div className="mt-2 grid grid-cols-1 gap-2 sm:grid-cols-2">
              <div>
                <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.splitMode')}</p>
                <p className={`text-sm font-semibold ${config?.min_split_enabled ? 'text-emerald-200' : 'text-fog'}`}>
                  {config?.min_split_enabled ? t('common.enabled') : t('common.disabled')}
                </p>
              </div>
              <div>
                <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.mppMode')}</p>
                <p className={`text-sm font-semibold ${config?.mpp_enabled ? 'text-emerald-200' : 'text-fog'}`}>
                  {config?.mpp_enabled ? t('common.enabled') : t('common.disabled')}
                </p>
                <p className="text-[11px] text-fog/60">
                  {config?.mpp_auto_only
                    ? t('rebalanceCenter.overview.mppScopeAutoOnly')
                    : t('rebalanceCenter.overview.mppScopeAllJobs')}
                </p>
              </div>
            </div>
            {config?.mpp_enabled && (
              <MetricDisclosure
                title={t('rebalanceCenter.overview.mppMetrics24h')}
                summary={t('rebalanceCenter.overview.mppMetricsSummary', {
                  success: formatter.format(overview.mpp_shadow_success_jobs_24h ?? 0),
                  partial: formatter.format(overview.mpp_shadow_partial_jobs_24h ?? 0),
                  aborts: formatter.format(overview.mpp_structural_abort_jobs_24h ?? 0)
                })}
                open={mppMetricsOpen}
                onToggle={() => setMppMetricsOpen((value) => !value)}
              >
                <div className="grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-2">
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppJobs24h', { value: formatter.format(overview.mpp_shadow_jobs_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppPlanReady24h', { value: formatter.format(overview.mpp_shadow_plan_ready_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppInProgressJobs24h', { value: formatter.format(overview.mpp_shadow_in_progress_jobs_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppSuccessJobs24h', { value: formatter.format(overview.mpp_shadow_success_jobs_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppFailedJobs24h', { value: formatter.format(overview.mpp_shadow_failed_jobs_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppPartialJobs24h', { value: formatter.format(overview.mpp_shadow_partial_jobs_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppStructuralAbortJobs24h', { value: formatter.format(overview.mpp_structural_abort_jobs_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppFloorBlockedSources24h', { value: formatter.format(overview.mpp_shadow_floor_blocked_sources_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppPlannedSat24h', { value: formatSats(overview.mpp_shadow_planned_sat_24h ?? 0) })}
                  </p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.overview.mppActualSentSat24h', { value: formatSats(overview.mpp_shadow_actual_sent_sat_24h ?? 0) })}
                  </p>
                </div>
              </MetricDisclosure>
            )}
            {((overview.top_failure_reasons_30m?.length ?? 0) > 0 || (overview.route_dead_targets_30m?.length ?? 0) > 0) && (
              <MetricDisclosure
                title={t('rebalanceCenter.overview.failureTelemetry30m')}
                summary={t('rebalanceCenter.overview.failureTelemetrySummary', {
                  reasons: formatter.format(overview.top_failure_reasons_30m?.length ?? 0),
                  targets: formatter.format(overview.route_dead_targets_30m?.length ?? 0)
                })}
                open={failureTelemetryOpen}
                onToggle={() => setFailureTelemetryOpen((value) => !value)}
              >
                {(overview.top_failure_reasons_30m?.length ?? 0) > 0 && (
                  <div className="space-y-1">
                    {overview.top_failure_reasons_30m?.slice(0, 3).map((item) => (
                      <p key={item.reason} className="truncate text-xs text-fog/50">
                        {t('rebalanceCenter.overview.failureReasonRow', {
                          reason: item.reason,
                          value: formatter.format(item.count)
                        })}
                      </p>
                    ))}
                  </div>
                )}
                {(overview.route_dead_targets_30m?.length ?? 0) > 0 && (
                  <div className="space-y-1">
                    {overview.route_dead_targets_30m?.slice(0, 3).map((item) => (
                      <p key={item.channel_id} className="truncate text-xs text-fog/50">
                        {t('rebalanceCenter.overview.routeDeadTargetRow', {
                          target: item.peer_alias || String(item.channel_id),
                          sources: formatter.format(item.failed_sources),
                          attempts: formatter.format(item.failure_attempts)
                        })}
                      </p>
                    ))}
                  </div>
                )}
              </MetricDisclosure>
            )}
          </div>
        </div>
      )}

      {config && (
        <div className="section-card space-y-3">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <button
              type="button"
              className="flex min-w-0 flex-1 items-start justify-between gap-3 text-left"
              aria-expanded={autopilotConfigOpen}
              onClick={() => setAutopilotConfigOpen((value) => !value)}
            >
              <span className="min-w-0">
                <span className="block text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.settings.sovereignTitle')}</span>
                <span className="block text-sm text-fog">{t('rebalanceCenter.settings.sovereignSubtitle')}</span>
                <span className="mt-1 block truncate text-xs text-fog/45">
                  {t('rebalanceCenter.settings.sovereignConfigSummary', {
                    mode: t(`rebalanceCenter.settings.schedulerModeOptions.${config.scheduler_mode}`),
                    scope: t(`rebalanceCenter.settings.sovereignCandidateScopeOptions.${config.sovereign_candidate_scope}`),
                    jobs: formatter.format(config.sovereign_max_jobs_per_cycle || REBALANCE_DEFAULT_SOVEREIGN_MAX_JOBS)
                  })}
                </span>
              </span>
              <span className="grid h-6 w-6 shrink-0 place-items-center rounded border border-white/10 text-fog/60">
                {autopilotConfigOpen ? '-' : '+'}
              </span>
            </button>
            <button
              type="button"
              className="btn-primary px-3 py-1 text-xs"
              disabled={autopilotSaving || !autopilotConfigDirty}
              onClick={handleSaveAutopilotConfig}
            >
              {autopilotSaving ? t('rebalanceCenter.autopilot.saving') : t('rebalanceCenter.autopilot.save')}
            </button>
          </div>
          {autopilotConfigOpen && (
            <div className="grid gap-3 border-t border-white/10 pt-3 md:grid-cols-3 xl:grid-cols-5">
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.schedulerMode')}>
                  {t('rebalanceCenter.settings.schedulerMode')}
                </label>
                <select
                  className="input-field"
                  value={config.scheduler_mode}
                  onChange={(e) => setConfig({ ...config, scheduler_mode: e.target.value })}
                >
                  <option value="rules_auto">{t('rebalanceCenter.settings.schedulerModeOptions.rules_auto')}</option>
                  <option value="sovereign_shadow">{t('rebalanceCenter.settings.schedulerModeOptions.sovereign_shadow')}</option>
                  <option value="sovereign_live">{t('rebalanceCenter.settings.schedulerModeOptions.sovereign_live')}</option>
                </select>
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sovereignCandidateScope')}>
                  {t('rebalanceCenter.settings.sovereignCandidateScope')}
                </label>
                <select
                  className="input-field"
                  value={config.sovereign_candidate_scope}
                  disabled={config.scheduler_mode === 'rules_auto'}
                  onChange={(e) => setConfig({ ...config, sovereign_candidate_scope: e.target.value })}
                >
                  <option value="auto_and_manual_restart">{t('rebalanceCenter.settings.sovereignCandidateScopeOptions.auto_and_manual_restart')}</option>
                  <option value="auto_only">{t('rebalanceCenter.settings.sovereignCandidateScopeOptions.auto_only')}</option>
                </select>
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sovereignMaxJobsPerCycle')}>
                  {t('rebalanceCenter.settings.sovereignMaxJobsPerCycle')}
                </label>
                <input
                  className="input-field"
                  type="number"
                  min={1}
                  disabled={config.scheduler_mode === 'rules_auto'}
                  value={config.sovereign_max_jobs_per_cycle}
                  onChange={(e) => setConfig({ ...config, sovereign_max_jobs_per_cycle: Number(e.target.value) })}
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sovereignMinExpectedProfit')}>
                  {t('rebalanceCenter.settings.sovereignMinExpectedProfit')}
                </label>
                <input
                  className="input-field"
                  type="number"
                  min={0}
                  disabled={config.scheduler_mode === 'rules_auto'}
                  value={config.sovereign_min_expected_profit_sat}
                  onChange={(e) => setConfig({ ...config, sovereign_min_expected_profit_sat: Number(e.target.value) })}
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sovereignLowSuccessMinRate')}>
                  {t('rebalanceCenter.settings.sovereignLowSuccessMinRate')}
                </label>
                <input
                  className="input-field"
                  type="number"
                  min={0.1}
                  max={100}
                  step={0.1}
                  disabled={config.scheduler_mode === 'rules_auto'}
                  value={Number(((config.sovereign_low_success_min_rate ?? REBALANCE_DEFAULT_SOVEREIGN_LOW_SUCCESS_MIN_RATE) * 100).toFixed(3))}
                  onChange={(e) => setConfig({ ...config, sovereign_low_success_min_rate: Number(e.target.value) / 100 })}
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sovereignLowSuccessMinProfitCostRatio')}>
                  {t('rebalanceCenter.settings.sovereignLowSuccessMinProfitCostRatio')}
                </label>
                <input
                  className="input-field"
                  type="number"
                  min={0.1}
                  step={0.05}
                  disabled={config.scheduler_mode === 'rules_auto'}
                  value={config.sovereign_low_success_min_profit_cost_ratio}
                  onChange={(e) => setConfig({ ...config, sovereign_low_success_min_profit_cost_ratio: Number(e.target.value) })}
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sovereignBudgetEfficiencyMinRatio')}>
                  {t('rebalanceCenter.settings.sovereignBudgetEfficiencyMinRatio')}
                </label>
                <input
                  className="input-field"
                  type="number"
                  min={0.05}
                  step={0.05}
                  disabled={config.scheduler_mode === 'rules_auto'}
                  value={config.sovereign_budget_efficiency_min_ratio}
                  onChange={(e) => setConfig({ ...config, sovereign_budget_efficiency_min_ratio: Number(e.target.value) })}
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sovereignRouteDeadSourceShare')}>
                  {t('rebalanceCenter.settings.sovereignRouteDeadSourceShare')}
                </label>
                <input
                  className="input-field"
                  type="number"
                  min={1}
                  max={100}
                  step={1}
                  disabled={config.scheduler_mode === 'rules_auto'}
                  value={Number(((config.sovereign_route_dead_source_share ?? REBALANCE_DEFAULT_SOVEREIGN_ROUTE_DEAD_SOURCE_SHARE) * 100).toFixed(2))}
                  onChange={(e) => setConfig({ ...config, sovereign_route_dead_source_share: Number(e.target.value) / 100 })}
                />
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sovereignRiskScoreFloor')}>
                  {t('rebalanceCenter.settings.sovereignRiskScoreFloor')}
                </label>
                <input
                  className="input-field"
                  type="number"
                  min={0.1}
                  max={20}
                  step={0.1}
                  disabled={config.scheduler_mode === 'rules_auto'}
                  value={Number(((config.sovereign_risk_score_floor ?? REBALANCE_DEFAULT_SOVEREIGN_RISK_SCORE_FLOOR) * 100).toFixed(3))}
                  onChange={(e) => setConfig({ ...config, sovereign_risk_score_floor: Number(e.target.value) / 100 })}
                />
              </div>
            </div>
          )}
        </div>
      )}

      {overview && (overview.scheduler_mode === 'sovereign_shadow' || overview.scheduler_mode === 'sovereign_live') && (
        <div className="section-card">
          <MetricDisclosure
            title={t('rebalanceCenter.autopilot.title')}
            summary={t('rebalanceCenter.autopilot.summary', {
              mode: t(`rebalanceCenter.settings.schedulerModeOptions.${overview.scheduler_mode}`),
              candidates: formatter.format(overview.sovereign_candidates ?? 0),
              selected: formatter.format(overview.sovereign_selected ?? 0),
              profit: formatSats(overview.sovereign_expected_profit_sat ?? 0)
            })}
            open={autopilotDecisionsOpen}
            onToggle={() => setAutopilotDecisionsOpen((value) => !value)}
          >
            <div className="space-y-4">
              <div className="space-y-2">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.autopilot.history24h')}</p>
                  <p className="text-xs text-fog/50">
                    {t('rebalanceCenter.autopilot.historySummary', {
                      scans: formatter.format(autopilotHistoryTotals.scans),
                      selected: formatter.format(autopilotHistoryTotals.selected),
                      profit: formatSats(autopilotHistoryTotals.expectedProfit)
                    })}
                  </p>
                </div>
                {autopilotHistory24h.length > 0 ? (
                  <div className="grid gap-2">
                    {autopilotHistory24h.slice(0, 8).map((entry) => {
                      const reasonSummary = formatReasonSummary(entry.skip_reasons)
                      return (
                        <div key={entry.id ?? entry.scan_at} className="rounded-lg border border-white/10 bg-white/5 p-2 text-xs text-fog/70">
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="font-semibold text-fog">{formatTimestamp(entry.scan_at)}</span>
                            <span className="text-fog/50">{t(`rebalanceCenter.settings.schedulerModeOptions.${entry.mode}`)}</span>
                          </div>
                          <div className="mt-1 grid gap-1 sm:grid-cols-3">
                            <span>
                              {t('rebalanceCenter.autopilot.historySelected', {
                                selected: formatter.format(entry.selected ?? 0),
                                candidates: formatter.format(entry.candidates ?? 0)
                              })}
                            </span>
                            <span>{t('rebalanceCenter.autopilot.profit', { value: formatSats(entry.expected_profit_sat ?? 0) })}</span>
                            <span>{t('rebalanceCenter.autopilot.historyBudget', { value: formatSats(entry.budget_remaining_sat ?? 0) })}</span>
                          </div>
                          {reasonSummary && (
                            <p className="mt-1 text-fog/50">{t('rebalanceCenter.autopilot.historyReasons', { value: reasonSummary })}</p>
                          )}
                        </div>
                      )
                    })}
                  </div>
                ) : (
                  <p className="text-sm text-fog/60">{t('rebalanceCenter.autopilot.historyEmpty')}</p>
                )}
              </div>
              {autopilotSelectedDecisions.length > 0 && (
                <div className="space-y-2">
                  <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.autopilot.selected')}</p>
                  <div className="grid gap-2">
                    {autopilotSelectedDecisions.map((decision) => (
                      <div key={`selected-${decision.channel_id}`} className="rounded-lg border border-emerald-400/20 bg-emerald-400/5 p-2 text-xs text-fog/70">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span className="font-semibold text-fog">{decision.peer_alias || decision.channel_point}</span>
                          <span className="text-emerald-200">{formatSkipReason(decision.reason)}</span>
                        </div>
                        <div className="mt-1 grid gap-1 sm:grid-cols-4">
                          <span>{t('rebalanceCenter.autopilot.amount', { value: formatSats(decision.amount_sat) })}</span>
                          <span>{t('rebalanceCenter.autopilot.profit', { value: formatSats(decision.expected_profit_sat) })}</span>
                          <span>{t('rebalanceCenter.autopilot.cost', { value: formatSats(decision.estimated_cost_sat) })}</span>
                          <span>{t('rebalanceCenter.autopilot.roi', { value: decision.expected_roi_valid ? formatRoi(decision.expected_roi) : 'n/a' })}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              {autopilotSkippedDecisions.length > 0 && (
                <div className="space-y-2">
                  <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.autopilot.skipped')}</p>
                  <div className="grid gap-2">
                    {autopilotSkippedDecisions.slice(0, 12).map((decision) => (
                      <div key={`skipped-${decision.channel_id}-${decision.reason}`} className="rounded-lg border border-white/10 bg-white/5 p-2 text-xs text-fog/70">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span className="font-semibold text-fog">{decision.peer_alias || decision.channel_point}</span>
                          <span className="text-amber-200">{formatSkipReason(decision.reason)}</span>
                        </div>
                        <div className="mt-1 grid gap-1 sm:grid-cols-4">
                          <span>{t('rebalanceCenter.autopilot.amount', { value: formatSats(decision.amount_sat) })}</span>
                          <span>{t('rebalanceCenter.autopilot.profit', { value: formatSats(decision.expected_profit_sat) })}</span>
                          <span>{t('rebalanceCenter.autopilot.cost', { value: formatSats(decision.estimated_cost_sat) })}</span>
                          <span>{t('rebalanceCenter.autopilot.roi', { value: decision.expected_roi_valid ? formatRoi(decision.expected_roi) : 'n/a' })}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              {autopilotDecisions.length === 0 && (
                <p className="text-sm text-fog/60">{t('rebalanceCenter.autopilot.empty')}</p>
              )}
            </div>
          </MetricDisclosure>
        </div>
      )}

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('rebalanceCenter.settings.title')}</h3>
            <p className="text-fog/60">{t('rebalanceCenter.settings.subtitle')}</p>
            <p className="text-xs text-fog/50">{t('rebalanceCenter.settings.autoOnlyNote')}</p>
          </div>
          <div className="flex items-center gap-2">
            {autoOpen && (
              <button className="btn-secondary text-xs px-3 py-1" onClick={() => setAdvancedControlsOpen((prev) => !prev)}>
                {advancedControlsOpen
                  ? t('rebalanceCenter.settings.hideAdvancedControls')
                  : t('rebalanceCenter.settings.showAdvancedControls')}
              </button>
            )}
            <button className="btn-secondary text-xs px-3 py-1" onClick={() => setAutoOpen((prev) => !prev)}>
              {autoOpen ? t('common.hide') : t('rebalanceCenter.settings.show')}
            </button>
          </div>
        </div>
        {config && autoOpen && (
          <div className="space-y-4">
            <div className="grid gap-4 xl:grid-cols-2">
              <SettingsSubcard title={t('rebalanceCenter.settings.groups.operation')}>
                <div className="grid gap-3 md:grid-cols-2">
                  <label
                    className="flex items-center gap-2 text-sm text-fog/70"
                    title={t('rebalanceCenter.settingsHints.autoEnabled')}
                  >
                    <input
                      type="checkbox"
                      checked={config.auto_enabled}
                      onChange={(e) => setConfig({ ...config, auto_enabled: e.target.checked })}
                    />
                    {t('rebalanceCenter.settings.autoEnabled')}
                  </label>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.scanInterval')}>
                      {t('rebalanceCenter.settings.scanInterval')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={30}
                      value={config.scan_interval_sec}
                      onChange={(e) => setConfig({ ...config, scan_interval_sec: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.maxConcurrent')}>
                      {t('rebalanceCenter.settings.maxConcurrent')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      value={config.max_concurrent}
                      onChange={(e) => setConfig({ ...config, max_concurrent: Number(e.target.value) })}
                    />
                  </div>
                  <label className="flex items-center gap-2 text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.manualRestartWatch')}>
                    <input
                      type="checkbox"
                      checked={config.manual_restart_watch}
                      onChange={(e) => setConfig({ ...config, manual_restart_watch: e.target.checked })}
                    />
                    {t('rebalanceCenter.settings.manualRestartWatch')}
                  </label>
                </div>
              </SettingsSubcard>

              <SettingsSubcard title={t('rebalanceCenter.settings.groups.budget')}>
                <div className="space-y-3">
                  <label
                    className="flex items-center gap-2 text-sm text-fog/70"
                    title={t('rebalanceCenter.settingsHints.budgetUnlimited')}
                  >
                    <input
                      type="checkbox"
                      checked={config.budget_unlimited}
                      onChange={(e) => setConfig({ ...config, budget_unlimited: e.target.checked })}
                    />
                    {t('rebalanceCenter.settings.budgetUnlimited')}
                  </label>
                  <div className="grid gap-3 md:grid-cols-2">
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.dailyBudgetPct')}>
                      {t('rebalanceCenter.settings.dailyBudgetPct')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={0.1}
                      disabled={config.budget_unlimited}
                      value={config.daily_budget_pct}
                      onChange={(e) => setConfig({ ...config, daily_budget_pct: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.budgetMode')}>
                      {t('rebalanceCenter.settings.budgetMode')}
                    </label>
                    <select
                      className="input-field"
                      value={config.budget_mode}
                      disabled={config.budget_unlimited}
                      onChange={(e) => setConfig({ ...config, budget_mode: e.target.value })}
                    >
                      <option value="hybrid_revenue">{t('rebalanceCenter.settings.budgetModeOptions.hybrid_revenue')}</option>
                      <option value="revenue_24h_pct">{t('rebalanceCenter.settings.budgetModeOptions.revenue_24h_pct')}</option>
                    </select>
                  </div>
                  <label
                    className="flex items-center gap-2 text-sm text-fog/70"
                    title={t('rebalanceCenter.settingsHints.budgetAutoOnly')}
                  >
                    <input
                      type="checkbox"
                      checked={config.budget_auto_only}
                      disabled={config.budget_unlimited}
                      onChange={(e) => setConfig({ ...config, budget_auto_only: e.target.checked })}
                    />
                    {t('rebalanceCenter.settings.budgetAutoOnly')}
                  </label>
                  <label
                    className="flex items-center gap-2 text-sm text-fog/70"
                    title={t('rebalanceCenter.settingsHints.manualReserveEnabled')}
                  >
                    <input
                      type="checkbox"
                      checked={config.manual_reserve_enabled}
                      disabled={config.budget_unlimited}
                      onChange={(e) => setConfig({ ...config, manual_reserve_enabled: e.target.checked })}
                    />
                    {t('rebalanceCenter.settings.manualReserveEnabled')}
                  </label>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.manualReserveMode')}>
                      {t('rebalanceCenter.settings.manualReserveMode')}
                    </label>
                    <select
                      className="input-field"
                      value={config.manual_reserve_mode}
                      disabled={config.budget_unlimited || !config.manual_reserve_enabled}
                      onChange={(e) => setConfig({ ...config, manual_reserve_mode: e.target.value })}
                    >
                      <option value="fixed_sat">{t('rebalanceCenter.settings.manualReserveModeOptions.fixed_sat')}</option>
                      <option value="pct">{t('rebalanceCenter.settings.manualReserveModeOptions.pct')}</option>
                    </select>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.manualReserveValue')}>
                      {t('rebalanceCenter.settings.manualReserveValue')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={config.manual_reserve_mode === 'pct' ? 0.1 : 1}
                      disabled={config.budget_unlimited || !config.manual_reserve_enabled}
                      value={config.manual_reserve_value}
                      onChange={(e) => setConfig({ ...config, manual_reserve_value: Number(e.target.value) })}
                    />
                  </div>
                  </div>
                </div>
              </SettingsSubcard>

              <SettingsSubcard title={t('rebalanceCenter.settings.groups.targets')}>
                <div className="grid gap-3 md:grid-cols-2">
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.deadband')}>
                      {t('rebalanceCenter.settings.deadband')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={0.1}
                      value={config.deadband_pct}
                      onChange={(e) => setConfig({ ...config, deadband_pct: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sourceMinLocal')}>
                      {t('rebalanceCenter.settings.sourceMinLocal')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={0.1}
                      value={config.source_min_local_pct}
                      onChange={(e) => setConfig({ ...config, source_min_local_pct: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.minAmount')}>
                      {t('rebalanceCenter.settings.minAmount')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      value={config.min_amount_sat}
                      onChange={(e) => setConfig({ ...config, min_amount_sat: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.maxAmount')}>
                      {t('rebalanceCenter.settings.maxAmount')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      value={config.max_amount_sat}
                      onChange={(e) => setConfig({ ...config, max_amount_sat: Number(e.target.value) })}
                    />
                  </div>
                </div>
              </SettingsSubcard>

              <SettingsSubcard title={t('rebalanceCenter.settings.groups.economics')}>
                <div className="grid gap-3 md:grid-cols-2">
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.econRatio')}>
                      {t('rebalanceCenter.settings.econRatio')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={0.05}
                      value={config.econ_ratio}
                      onChange={(e) => setConfig({ ...config, econ_ratio: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.econRatioMaxPpm')}>
                      {t('rebalanceCenter.settings.econRatioMaxPpm')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      value={config.econ_ratio_max_ppm}
                      onChange={(e) => setConfig({ ...config, econ_ratio_max_ppm: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.feeLimitPpm')}>
                      {t('rebalanceCenter.settings.feeLimitPpm')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      value={config.fee_limit_ppm}
                      onChange={(e) => setConfig({ ...config, fee_limit_ppm: Number(e.target.value) })}
                    />
                  </div>
                  <label className="flex items-center gap-2 text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.lostProfit')}>
                    <input
                      type="checkbox"
                      checked={config.lost_profit}
                      onChange={(e) => setConfig({ ...config, lost_profit: e.target.checked })}
                    />
                    {t('rebalanceCenter.settings.lostProfit')}
                  </label>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.roiMin')}>
                      {t('rebalanceCenter.settings.roiMin')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={0.1}
                      value={config.roi_min}
                      onChange={(e) => setConfig({ ...config, roi_min: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.rebalanceCostFloorPpm')}>
                      {t('rebalanceCenter.settings.rebalanceCostFloorPpm')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={1}
                      placeholder="250"
                      value={config.rebalance_cost_floor_ppm}
                      onChange={(e) => setConfig({ ...config, rebalance_cost_floor_ppm: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.gainModelVersion')}>
                      {t('rebalanceCenter.settings.gainModelVersion')}
                    </label>
                    <select
                      className="input-field"
                      value={config.gain_model_version}
                      onChange={(e) => setConfig({ ...config, gain_model_version: Number(e.target.value) })}
                    >
                      <option value={1}>{t('rebalanceCenter.settings.gainModelOptions.v1')}</option>
                      <option value={2}>{t('rebalanceCenter.settings.gainModelOptions.v2')}</option>
                      <option value={3}>{t('rebalanceCenter.settings.gainModelOptions.v3')}</option>
                    </select>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.velocityWeight')}>
                      {t('rebalanceCenter.settings.velocityWeight')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      max={1}
                      step={0.05}
                      value={config.velocity_weight}
                      onChange={(e) => setConfig({ ...config, velocity_weight: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.autofeeSettlingWindowSec')}>
                      {t('rebalanceCenter.settings.autofeeSettlingWindowSec')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={60}
                      placeholder="7200"
                      value={config.autofee_settling_window_sec}
                      onChange={(e) => setConfig({ ...config, autofee_settling_window_sec: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.autofeeSettlingMultiplier')}>
                      {t('rebalanceCenter.settings.autofeeSettlingMultiplier')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      max={1}
                      step={0.05}
                      placeholder="0.5"
                      value={config.autofee_settling_multiplier}
                      onChange={(e) => setConfig({ ...config, autofee_settling_multiplier: Number(e.target.value) })}
                    />
                  </div>
                </div>
              </SettingsSubcard>

              <SettingsSubcard title={t('rebalanceCenter.settings.groups.execution')} className="xl:col-span-2">
                <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                  <label
                    className="md:col-span-2 xl:col-span-4 flex min-h-[42px] items-start gap-2 rounded-lg border border-mint/40 bg-mint/5 px-3 py-2 text-sm text-fog/80"
                    title={t('rebalanceCenter.settingsHints.delegatedFastPath')}
                  >
                    <input
                      type="checkbox"
                      className="mt-1"
                      checked={config.delegated_fast_path_enabled}
                      onChange={(e) => setConfig({ ...config, delegated_fast_path_enabled: e.target.checked })}
                    />
                    <span>{t('rebalanceCenter.settings.delegatedFastPath')}</span>
                  </label>
                  <div className="space-y-2">
                    <label className="flex min-h-[42px] items-start gap-2 rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-sm text-fog/80" title={t('rebalanceCenter.settingsHints.mppEnabled')}>
                      <input
                        type="checkbox"
                        className="mt-1"
                        checked={config.mpp_enabled}
                        onChange={(e) => setConfig({ ...config, mpp_enabled: e.target.checked })}
                      />
                      <span>{t('rebalanceCenter.settings.mppEnabled')}</span>
                    </label>
                    <label className="flex min-h-[42px] items-start gap-2 rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-sm text-fog/80" title={t('rebalanceCenter.settingsHints.mppAutoOnly')}>
                      <input
                        type="checkbox"
                        className="mt-1"
                        checked={config.mpp_auto_only}
                        disabled={!config.mpp_enabled}
                        onChange={(e) => setConfig({ ...config, mpp_auto_only: e.target.checked })}
                      />
                      <span>{t('rebalanceCenter.settings.mppAutoOnly')}</span>
                    </label>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.mppMaxShards')}>
                      {t('rebalanceCenter.settings.mppMaxShards')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      max={MSPR_MAX_SHARDS_LIMIT}
                      value={config.mpp_max_shards}
                      disabled={!config.mpp_enabled}
                      onChange={(e) => {
                        const nextMaxShards = Math.max(1, Math.min(MSPR_MAX_SHARDS_LIMIT, Number(e.target.value) || 1))
                        const nextParallelism = Math.max(1, Math.min(config.mpp_parallelism || 1, nextMaxShards))
                        setConfig({ ...config, mpp_max_shards: nextMaxShards, mpp_parallelism: nextParallelism })
                      }}
                    />
                    <p className="text-[11px] text-fog/50">{t('rebalanceCenter.settings.mppMaxShardsRecommended', { value: MSPR_DEFAULT_MAX_SHARDS })}</p>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.mppParallelism')}>
                      {t('rebalanceCenter.settings.mppParallelism')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      max={Math.max(1, Math.min(MSPR_MAX_SHARDS_LIMIT, config.mpp_max_shards || 1))}
                      value={config.mpp_parallelism}
                      disabled={!config.mpp_enabled}
                      onChange={(e) => {
                        const maxAllowed = Math.max(1, Math.min(MSPR_MAX_SHARDS_LIMIT, config.mpp_max_shards || 1))
                        const nextParallelism = Math.max(1, Math.min(maxAllowed, Number(e.target.value) || 1))
                        setConfig({ ...config, mpp_parallelism: nextParallelism })
                      }}
                    />
                    <p className="text-[11px] text-fog/50">{t('rebalanceCenter.settings.mppParallelismRecommended', { value: MSPR_DEFAULT_PARALLELISM })}</p>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.mppMinShardSat')}>
                      {t('rebalanceCenter.settings.mppMinShardSat')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      value={config.mpp_min_shard_sat}
                      disabled={!config.mpp_enabled}
                      onChange={(e) => setConfig({ ...config, mpp_min_shard_sat: Number(e.target.value) })}
                    />
                    <p className="text-[11px] text-fog/50">{t('rebalanceCenter.settings.mppMinShardSatRecommended', { value: formatSats(MSPR_DEFAULT_MIN_SHARD_SAT) })}</p>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.mppRoundTimeout')}>
                      {t('rebalanceCenter.settings.mppRoundTimeout')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={5}
                      value={config.mpp_round_timeout_sec}
                      disabled={!config.mpp_enabled}
                      onChange={(e) => setConfig({ ...config, mpp_round_timeout_sec: Number(e.target.value) })}
                    />
                    <p className="text-[11px] text-fog/50">{t('rebalanceCenter.settings.mppRoundTimeoutRecommended', { value: MSPR_DEFAULT_ROUND_TIMEOUT_SEC })}</p>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.feeSteps')}>
                      {t('rebalanceCenter.settings.feeSteps')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      value={config.fee_ladder_steps}
                      onChange={(e) => setConfig({ ...config, fee_ladder_steps: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.amountProbeSteps')}>
                      {t('rebalanceCenter.settings.amountProbeSteps')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      value={config.amount_probe_steps}
                      onChange={(e) => setConfig({ ...config, amount_probe_steps: Number(e.target.value) })}
                    />
                  </div>
                  <label className="flex min-h-[42px] items-start gap-2 rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-sm text-fog/80" title={t('rebalanceCenter.settingsHints.amountProbeAdaptive')}>
                    <input
                      type="checkbox"
                      className="mt-1"
                      checked={config.amount_probe_adaptive}
                      onChange={(e) => setConfig({ ...config, amount_probe_adaptive: e.target.checked })}
                    />
                    <span>{t('rebalanceCenter.settings.amountProbeAdaptive')}</span>
                  </label>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.failTolerancePpm')}>
                      {t('rebalanceCenter.settings.failTolerancePpm')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      value={config.fail_tolerance_ppm}
                      onChange={(e) => setConfig({ ...config, fail_tolerance_ppm: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.attemptTimeout')}>
                      {t('rebalanceCenter.settings.attemptTimeout')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={5}
                      value={config.attempt_timeout_sec}
                      onChange={(e) => setConfig({ ...config, attempt_timeout_sec: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.rebalanceTimeout')}>
                      {t('rebalanceCenter.settings.rebalanceTimeout')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={60}
                      value={config.rebalance_timeout_sec}
                      onChange={(e) => setConfig({ ...config, rebalance_timeout_sec: Number(e.target.value) })}
                    />
                  </div>
                </div>
              </SettingsSubcard>

              <SettingsSubcard title={t('rebalanceCenter.settings.groups.payback')} className="xl:col-span-2">
                <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                  <label
                    className="flex min-h-[42px] items-start gap-2 rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-sm text-fog/80"
                    title={t('rebalanceCenter.settingsHints.delegatedFastPathStrictPayback')}
                  >
                    <input
                      type="checkbox"
                      className="mt-1"
                      checked={config.delegated_fast_path_strict_payback}
                      onChange={(e) => setConfig({ ...config, delegated_fast_path_strict_payback: e.target.checked })}
                    />
                    <span>{t('rebalanceCenter.settings.delegatedFastPathStrictPayback')}</span>
                  </label>
                  <label
                    className="flex min-h-[42px] items-start gap-2 rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-sm text-fog/80"
                    title={t('rebalanceCenter.settingsHints.freshPaidLiquidityLock')}
                  >
                    <input
                      type="checkbox"
                      className="mt-1"
                      checked={config.fresh_paid_liquidity_lock_enabled}
                      onChange={(e) => setConfig({ ...config, fresh_paid_liquidity_lock_enabled: e.target.checked })}
                    />
                    <span>{t('rebalanceCenter.settings.freshPaidLiquidityLock')}</span>
                  </label>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.freshPaidLiquidityLockHours')}>
                      {t('rebalanceCenter.settings.freshPaidLiquidityLockHours')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      step={1}
                      disabled={!config.fresh_paid_liquidity_lock_enabled}
                      value={config.fresh_paid_liquidity_lock_hours}
                      onChange={(e) => setConfig({ ...config, fresh_paid_liquidity_lock_hours: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <p className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.paybackPolicy')}>
                      {t('rebalanceCenter.settings.paybackPolicy')}
                    </p>
                    <label
                      className="flex items-center gap-2 text-sm text-fog/70"
                      title={t('rebalanceCenter.settingsHints.paybackMode')}
                    >
                      <input
                        type="checkbox"
                        checked={(config.payback_mode_flags & PAYBACK_MODE_PAYBACK) !== 0}
                        onChange={() => togglePaybackFlag(PAYBACK_MODE_PAYBACK)}
                      />
                      {t('rebalanceCenter.settings.paybackMode')}
                    </label>
                    <label
                      className="flex items-center gap-2 text-sm text-fog/70"
                      title={t('rebalanceCenter.settingsHints.timeMode')}
                    >
                      <input
                        type="checkbox"
                        checked={(config.payback_mode_flags & PAYBACK_MODE_TIME) !== 0}
                        onChange={() => togglePaybackFlag(PAYBACK_MODE_TIME)}
                      />
                      {t('rebalanceCenter.settings.timeMode')}
                    </label>
                    <label
                      className="flex items-center gap-2 text-sm text-fog/70"
                      title={t('rebalanceCenter.settingsHints.criticalMode')}
                    >
                      <input
                        type="checkbox"
                        checked={(config.payback_mode_flags & PAYBACK_MODE_CRITICAL) !== 0}
                        onChange={() => togglePaybackFlag(PAYBACK_MODE_CRITICAL)}
                      />
                      {t('rebalanceCenter.settings.criticalMode')}
                    </label>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.unlockDays')}>
                      {t('rebalanceCenter.settings.unlockDays')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      value={config.unlock_days}
                      onChange={(e) => setConfig({ ...config, unlock_days: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.criticalRelease')}>
                      {t('rebalanceCenter.settings.criticalRelease')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={1}
                      value={config.critical_release_pct}
                      onChange={(e) => setConfig({ ...config, critical_release_pct: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.criticalMinSources')}>
                      {t('rebalanceCenter.settings.criticalMinSources')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      step={1}
                      placeholder="2"
                      value={config.critical_min_sources}
                      onChange={(e) => setConfig({ ...config, critical_min_sources: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.criticalMinAvailable')}>
                      {t('rebalanceCenter.settings.criticalMinAvailable')}
                    </label>
                    <div className="flex items-center gap-2">
                      <input
                        className="input-field flex-1"
                        type="number"
                        min={0}
                        step={1}
                        placeholder="0"
                        value={config.critical_min_available_sats}
                        onChange={(e) => setConfig({ ...config, critical_min_available_sats: Number(e.target.value) })}
                      />
                      <span className="text-xs text-fog/60">sats</span>
                    </div>
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.criticalCycles')}>
                      {t('rebalanceCenter.settings.criticalCycles')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={1}
                      value={config.critical_cycles}
                      onChange={(e) => setConfig({ ...config, critical_cycles: Number(e.target.value) })}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sourceMinPaybackProgress')}>
                      {t('rebalanceCenter.settings.sourceMinPaybackProgress')}
                    </label>
                    <input
                      className="input-field"
                      type="number"
                      min={0}
                      max={5}
                      step={0.05}
                      placeholder="0.95"
                      value={config.source_min_payback_progress}
                      onChange={(e) => setConfig({ ...config, source_min_payback_progress: Number(e.target.value) })}
                    />
                  </div>
                </div>
              </SettingsSubcard>
            </div>

            {advancedControlsOpen && (
              <div className="grid gap-4 xl:grid-cols-2">
                <SettingsSubcard
                  title={t('rebalanceCenter.settings.splitMinTitle')}
                  subtitle={t('rebalanceCenter.settings.splitMinSubtitle')}
                >
                  <div className="grid gap-3 md:grid-cols-3">
                    <div className="space-y-2">
                      <label className="flex items-center gap-2 text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.minSplitEnabled')}>
                        <input
                          type="checkbox"
                          checked={config.min_split_enabled}
                          onChange={(e) => setConfig({ ...config, min_split_enabled: e.target.checked })}
                        />
                        {t('rebalanceCenter.settings.minSplitEnabled')}
                      </label>
                      <p className="text-[11px] text-fog/50">{t('rebalanceCenter.settings.splitMinRecommended')}</p>
                    </div>
                    <div className="space-y-2">
                      <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.minProbeAmount')}>
                        {t('rebalanceCenter.settings.minProbeAmount')}
                      </label>
                      <input
                        className="input-field"
                        type="number"
                        min={0}
                        value={config.min_probe_sat}
                        disabled={!config.min_split_enabled}
                        onChange={(e) => setConfig({ ...config, min_probe_sat: Number(e.target.value) })}
                      />
                      <p className="text-[11px] text-fog/50">{t('rebalanceCenter.settings.minProbeRecommended', { value: formatSats(REBALANCE_DEFAULT_MIN_PROBE_SAT) })}</p>
                    </div>
                    <div className="space-y-2">
                      <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.minExecuteAmount')}>
                        {t('rebalanceCenter.settings.minExecuteAmount')}
                      </label>
                      <input
                        className="input-field"
                        type="number"
                        min={0}
                        value={config.min_execute_sat}
                        disabled={!config.min_split_enabled}
                        onChange={(e) => setConfig({ ...config, min_execute_sat: Number(e.target.value) })}
                      />
                      <p className="text-[11px] text-fog/50">
                        {t('rebalanceCenter.settings.minExecuteRecommended', { value: formatSats(REBALANCE_DEFAULT_MIN_EXECUTE_SAT) })}
                      </p>
                    </div>
                  </div>
                </SettingsSubcard>

                <SettingsSubcard
                  title={t('rebalanceCenter.settings.advancedRoutingTitle')}
                  subtitle={t('rebalanceCenter.settings.advancedRoutingSubtitle')}
                >
                  <div className="grid gap-3 md:grid-cols-2">
                    <label
                      className="md:col-span-2 flex min-h-[42px] items-start gap-2 rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-sm text-fog/80"
                      title={t('rebalanceCenter.settingsHints.cooldownProbeEnabled')}
                    >
                      <input
                        type="checkbox"
                        className="mt-1"
                        checked={config.cooldown_probe_enabled}
                        onChange={(e) => setConfig({ ...config, cooldown_probe_enabled: e.target.checked })}
                      />
                      <span>{t('rebalanceCenter.settings.cooldownProbeEnabled')}</span>
                    </label>
                    <div className="space-y-2">
                      <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.mcHalfLife')}>
                        {t('rebalanceCenter.settings.mcHalfLife')}
                      </label>
                      <input
                        className="input-field"
                        type="number"
                        min={0}
                        value={config.mc_half_life_sec}
                        onChange={(e) => setConfig({ ...config, mc_half_life_sec: Number(e.target.value) })}
                      />
                    </div>
                    <label className="flex min-h-[42px] items-start gap-2 rounded-lg border border-white/10 bg-white/5 px-3 py-2 text-sm text-fog/80" title={t('rebalanceCenter.settingsHints.missionControlReinforce')}>
                      <input
                        type="checkbox"
                        className="mt-1"
                        checked={config.mission_control_reinforce}
                        onChange={(e) => setConfig({ ...config, mission_control_reinforce: e.target.checked })}
                      />
                      <span>{t('rebalanceCenter.settings.missionControlReinforce')}</span>
                    </label>
                  </div>
                </SettingsSubcard>
              </div>
            )}
          </div>
        )}
        {autoOpen && (
          <div className="flex flex-wrap items-center gap-3">
            <button className="btn-primary" onClick={handleSaveConfig} disabled={saving}>
              {saving ? t('rebalanceCenter.saving') : t('rebalanceCenter.save')}
            </button>
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('rebalanceCenter.channels.title')}</h3>
          <div className="flex flex-wrap items-center gap-4 text-xs text-fog/60">
            <span>{t('rebalanceCenter.channels.count', { count: channels.length })}</span>
            <span>{t('rebalanceCenter.channels.filteredCount', { count: sortedChannels.length })}</span>
          </div>
        </div>
        <div className="grid gap-3 lg:grid-cols-[minmax(0,1.4fr)_minmax(0,0.9fr)_minmax(0,0.9fr)_auto_auto]">
          <input
            className="input-field"
            placeholder={t('rebalanceCenter.channels.searchPlaceholder')}
            value={channelSearch}
            onChange={(e) => setChannelSearch(e.target.value)}
          />
          <input
            className="input-field"
            placeholder={t('rebalanceCenter.channels.minCapacity')}
            type="number"
            min={0}
            value={channelMinCapacity}
            onChange={(e) => setChannelMinCapacity(e.target.value)}
          />
          <select className="input-field" value={channelSort} onChange={(e) => setChannelSort(e.target.value as 'economic' | 'emptiest')}>
            <option value="economic">{t('rebalanceCenter.channels.sortEconomic')}</option>
            <option value="emptiest">{t('rebalanceCenter.channels.sortEmptiest')}</option>
          </select>
          <button
            type="button"
            className="btn-secondary px-4"
            onClick={() => setChannelSortDir((current) => (current === 'desc' ? 'asc' : 'desc'))}
          >
            {channelSortDir === 'desc' ? t('rebalanceCenter.channels.sortDesc') : t('rebalanceCenter.channels.sortAsc')}
          </button>
          <label className="flex items-center gap-2 text-[11px] text-fog/70 sm:text-xs">
            <input
              type="checkbox"
              checked={channelShowPrivate}
              onChange={(e) => setChannelShowPrivate(e.target.checked)}
            />
            {t('rebalanceCenter.channels.showPrivate')}
          </label>
        </div>
        <div className="space-y-3 md:hidden">
          <div className="flex flex-wrap items-center gap-4 text-xs text-fog/60">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={sortedChannels.length > 0 && sortedChannels.every((ch) => ch.auto_enabled)}
                onChange={(e) => handleBulkAuto(e.target.checked)}
              />
              {t('rebalanceCenter.channels.bulkAuto')}
            </label>
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={sortedChannels.length > 0 && sortedChannels.every((ch) => ch.excluded_as_source)}
                onChange={(e) => handleBulkExclude(e.target.checked)}
              />
              {t('rebalanceCenter.channels.bulkExclude')}
            </label>
          </div>
          {sortedChannels.map((ch) => {
            const scoreMeta = config ? computeChannelScore(ch, workbenchMaxDrainRateSatPerHour) : null
            const expectedRoiValid = scoreMeta ? scoreMeta.expectedRoiValid : true
            const expectedRoi = scoreMeta ? scoreMeta.expectedRoi : 0
            const meetsRoi =
              !config || config.roi_min <= 0 || !expectedRoiValid || expectedRoi >= config.roi_min
            const passesProfit = !scoreMeta || !(scoreMeta.expectedGain > 0 && scoreMeta.estimatedCost > 0 && scoreMeta.expectedGain < scoreMeta.estimatedCost)
            const draftEligibleAsTarget = channelDraftEligibleAsTarget(ch)
            const isAutoTarget = draftEligibleAsTarget && ch.auto_enabled && meetsRoi && passesProfit
            const manualRestartSelected = manualRestart[ch.channel_point] === true
            const manualRestartBudgetLow =
              manualRestartBudgetEnforced &&
              manualRestartSelected &&
              Boolean(scoreMeta) &&
              (scoreMeta?.estimatedCost ?? 0) > 0 &&
              (scoreMeta?.estimatedCost ?? 0) > remainingTotalSat
            const highlight = isAutoTarget
              ? 'bg-rose-500/10'
              : draftEligibleAsTarget
                ? 'bg-amber-500/10'
                : ch.eligible_as_source
                  ? 'bg-emerald-500/10'
                  : ''
            const isFocused = focusedChannelPoint === ch.channel_point
            const lightningOpsLink = ch.channel_point
              ? buildHashWithChannelPoint(LIGHTNING_OPS_ROUTE_KEY, ch.channel_point)
              : `#${LIGHTNING_OPS_ROUTE_KEY}`

            return (
              <article
                key={`mobile-${ch.channel_point || String(ch.channel_id)}`}
                id={mobileChannelCardID(ch.channel_point)}
                className={`rounded-2xl border border-white/10 bg-ink/50 p-3 ${highlight} ${isFocused ? 'ring-1 ring-sky-300/70 bg-sky-500/10' : ''}`}
              >
                <a
                  className="text-sm font-semibold text-fog hover:text-white hover:underline underline-offset-2"
                  href={lightningOpsLink}
                  title={t('rebalanceCenter.channels.openInLightningOps')}
                >
                  {ch.peer_alias || ch.remote_pubkey}
                </a>
                <div className="text-[11px] text-fog/50 break-all">{ch.channel_point}</div>

                <div className="mt-3 grid grid-cols-2 gap-3 text-xs">
                  <div>
                    <div className="text-fog/50">{t('rebalanceCenter.channels.balance')}</div>
                    <div>{formatPct(ch.local_pct)} / {formatPct(ch.remote_pct)}</div>
                    <div className="text-fog/50">{formatSats(ch.local_balance_sat)} | {formatSats(ch.remote_balance_sat)}</div>
                  </div>
                  <div>
                    <div className="text-fog/50">{t('rebalanceCenter.channels.fees')}</div>
                    <div>{t('rebalanceCenter.channels.feeOut', { value: ch.outgoing_fee_ppm })}</div>
                    <div>{t('rebalanceCenter.channels.feePeer', { value: ch.peer_fee_rate_ppm })}</div>
                    <div className="text-fog/50">{t('rebalanceCenter.channels.spread', { value: ch.spread_ppm })}</div>
                  </div>
                </div>

                <div className="mt-3 space-y-2">
                  <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-2">
                    <input
                      className="input-field h-9 w-full min-w-0"
                      type="number"
                      min={1}
                      max={99}
                      step={0.1}
                      title={t('rebalanceCenter.channelsHints.targetOutbound')}
                      value={editTargets[channelKey(ch)] ?? String(Math.round(ch.target_outbound_pct * 10) / 10)}
                      onChange={(e) => setEditTargets((prev) => ({ ...prev, [channelKey(ch)]: e.target.value }))}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          e.preventDefault()
                          handleSaveChannelSettings(ch)
                        }
                      }}
                    />
                    <span className="text-xs text-fog/60">%</span>
                    <input
                      className="input-field h-9 w-full min-w-0"
                      type="text"
                      inputMode="decimal"
                      title={t('rebalanceCenter.channelsHints.econRatio')}
                      value={channelEconRatioInput(ch)}
                      disabled={channelUseDefaultEconRatio(ch)}
                      onChange={(e) => setEditEconRatios((prev) => ({ ...prev, [channelKey(ch)]: e.target.value }))}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          e.preventDefault()
                          handleSaveChannelSettings(ch)
                        }
                      }}
                    />
                  </div>
                  <label className="flex items-center gap-2 text-xs text-fog/60" title={t('rebalanceCenter.channelsHints.useDefaultEconRatio')}>
                    <input
                      type="checkbox"
                      checked={channelUseDefaultEconRatio(ch)}
                      onChange={(e) => handleUseDefaultEconRatioToggle(ch, e.target.checked)}
                    />
                    {t('rebalanceCenter.channels.useDefaultEconRatio')}
                  </label>
                  <label className="flex items-center gap-2 text-xs text-amber-100/80" title={t('rebalanceCenter.channelsHints.autoBypassCostGate')}>
                    <input
                      type="checkbox"
                      checked={channelAutoBypassCostGate(ch)}
                      onChange={(e) => void handleAutoBypassCostGateToggle(ch, e.target.checked)}
                    />
                    {t('rebalanceCenter.channels.autoBypassCostGate')}
                  </label>
                  <div className="text-xs text-fog/50">
                    {t('rebalanceCenter.channels.amount', { value: formatSats(ch.target_amount_sat) })}
                  </div>
                </div>

                <div className="mt-3 grid grid-cols-2 gap-3 text-xs">
                  <div>
                    <div className="text-fog/50">{t('rebalanceCenter.channels.protected')}</div>
                    <div>{formatSats(ch.effective_protected_sat ?? 0)}</div>
                    <div className="text-fog/50">{t('rebalanceCenter.channels.payback', { value: (ch.payback_progress * 100).toFixed(0) })}</div>
                    <div className="text-fog/50" title={t('rebalanceCenter.channelsHints.timeToPayback')}>
                      {t('rebalanceCenter.channels.timeToPayback')}: {formatTimeToPayback(ch)}
                    </div>
                  </div>
                  <div>
                    <div className="text-fog/50">
                      {scoreMeta && expectedRoiValid
                        ? t('rebalanceCenter.channels.roiEstimate', { value: formatRoi(expectedRoi) })
                        : t('rebalanceCenter.channels.roiEstimateNA')}
                    </div>
                  </div>
                </div>

                <div className="mt-3 flex flex-wrap items-center gap-2">
                  <button
                    className="btn-secondary text-xs px-3 py-1"
                    onClick={() => handleSaveChannelSettings(ch)}
                    title={t('rebalanceCenter.channelsHints.save')}
                  >
                    {t('rebalanceCenter.channels.save')}
                  </button>
                  <button
                    className="btn-primary text-xs px-3 py-1"
                    onClick={() => handleRunRebalance(ch)}
                    disabled={!ch.eligible_as_manual_target}
                    title={t('rebalanceCenter.channelsHints.rebalanceIn')}
                  >
                    {t('rebalanceCenter.channels.rebalanceIn')}
                  </button>
                  <label className="flex items-center gap-1 text-[11px] text-fog/60" title={t('rebalanceCenter.channelsHints.rebalanceRestart')}>
                    <span>⟳</span>
                    <input
                      type="checkbox"
                      checked={manualRestartSelected}
                      onChange={(e) => handleManualRestartToggle(ch, e.target.checked)}
                    />
                  </label>
                  <button
                    className="btn-secondary text-xs px-3 py-1"
                    onClick={() => handleTogglePairStats(ch)}
                    title={t('rebalanceCenter.channelsHints.pairStats')}
                  >
                    {pairStatsOpen[channelKey(ch)] ? t('common.hide') : t('rebalanceCenter.channels.pairStats')}
                  </button>
                  {manualRestartBudgetLow && scoreMeta && (
                    <div className="basis-full text-[11px] text-amber-200">
                      {t('rebalanceCenter.channels.manualRestartBudgetWarning', {
                        cost: formatSats(scoreMeta.estimatedCost),
                        budget: formatSats(remainingTotalSat)
                      })}
                    </div>
                  )}
                </div>

                <div className="mt-2 flex flex-wrap items-center gap-3 text-xs">
                  <label className="flex items-center gap-2" title={t('rebalanceCenter.channelsHints.auto')}>
                    <input
                      type="checkbox"
                      checked={ch.auto_enabled}
                      onChange={(e) => handleToggleChannelAuto(ch, e.target.checked)}
                    />
                    {t('rebalanceCenter.channels.auto')}
                  </label>
                  <label className="flex items-center gap-2" title={t('rebalanceCenter.channelsHints.excludeSource')}>
                    <input
                      type="checkbox"
                      checked={ch.excluded_as_source}
                      onChange={(e) => handleExcludeSource(ch, e.target.checked)}
                    />
                    {t('rebalanceCenter.channels.excludeSource')}
                  </label>
                </div>
                {pairStatsOpen[channelKey(ch)] && renderPairStatsPanel(ch)}
              </article>
            )
          })}
        </div>
        <div className="hidden max-h-[520px] overflow-x-auto overflow-y-auto pr-1 md:block">
            <table className="w-full text-sm text-fog/70">
            <thead>
              <tr className="text-left">
                <th className="pb-2">{t('rebalanceCenter.channels.channel')}</th>
                <th className="pb-2 pl-4">{t('rebalanceCenter.channels.balance')}</th>
                <th className="pb-2 pl-4">{t('rebalanceCenter.channels.fees')}</th>
                <th className="pb-2 pl-4">
                  <div className="grid grid-cols-[3.5rem_auto_4.5rem_auto] items-end gap-2">
                    <span className="col-span-2">{t('rebalanceCenter.channels.target')}</span>
                    <span className="whitespace-nowrap">{t('rebalanceCenter.channels.econRatio')}</span>
                  </div>
                </th>
                <th className="pb-2 text-center">{t('rebalanceCenter.channels.protected')}</th>
                <th className="pb-2 pl-4 text-center" title={t('rebalanceCenter.channelsHints.timeToPayback')}>
                  {t('rebalanceCenter.channels.timeToPayback')}
                </th>
                <th className="pb-2">
                  <div className="flex flex-col gap-2">
                    <span>{t('rebalanceCenter.channels.actions')}</span>
                    <label className="flex items-center gap-2 text-xs text-fog/70">
                      <input
                        type="checkbox"
                        checked={sortedChannels.length > 0 && sortedChannels.every((ch) => ch.auto_enabled)}
                        onChange={(e) => handleBulkAuto(e.target.checked)}
                      />
                      {t('rebalanceCenter.channels.bulkAuto')}
                    </label>
                    <label className="flex items-center gap-2 text-xs text-fog/70">
                      <input
                        type="checkbox"
                        checked={sortedChannels.length > 0 && sortedChannels.every((ch) => ch.excluded_as_source)}
                        onChange={(e) => handleBulkExclude(e.target.checked)}
                      />
                      {t('rebalanceCenter.channels.bulkExclude')}
                    </label>
                  </div>
                </th>
              </tr>
            </thead>
            <tbody>
              {sortedChannels.map((ch) => {
                const scoreMeta = config ? computeChannelScore(ch, workbenchMaxDrainRateSatPerHour) : null
                const expectedRoiValid = scoreMeta ? scoreMeta.expectedRoiValid : true
                const expectedRoi = scoreMeta ? scoreMeta.expectedRoi : 0
                const meetsRoi =
                  !config || config.roi_min <= 0 || !expectedRoiValid || expectedRoi >= config.roi_min
                const passesProfit = !scoreMeta || !(scoreMeta.expectedGain > 0 && scoreMeta.estimatedCost > 0 && scoreMeta.expectedGain < scoreMeta.estimatedCost)
                const draftEligibleAsTarget = channelDraftEligibleAsTarget(ch)
                const isAutoTarget = draftEligibleAsTarget && ch.auto_enabled && meetsRoi && passesProfit
                const manualRestartSelected = manualRestart[ch.channel_point] === true
                const manualRestartBudgetLow =
                  manualRestartBudgetEnforced &&
                  manualRestartSelected &&
                  Boolean(scoreMeta) &&
                  (scoreMeta?.estimatedCost ?? 0) > 0 &&
                  (scoreMeta?.estimatedCost ?? 0) > remainingTotalSat
                const scoreTitle =
                  scoreMeta
                    ? t('rebalanceCenter.channels.scoreHint', {
                        score: formatSats(scoreMeta.score),
                        gain: formatSats(scoreMeta.expectedGain),
                        cost: formatSats(scoreMeta.estimatedCost),
                        roi: scoreMeta.expectedRoiValid ? formatRoi(scoreMeta.expectedRoi) : t('rebalanceCenter.channels.scoreRoiNA')
                      })
                    : undefined
                const highlight = isAutoTarget
                  ? 'bg-rose-500/10'
                  : draftEligibleAsTarget
                    ? 'bg-amber-500/10'
                    : ch.eligible_as_source
                      ? 'bg-emerald-500/10'
                      : ''
                const isFocused = focusedChannelPoint === ch.channel_point
                const lightningOpsLink = ch.channel_point
                  ? buildHashWithChannelPoint(LIGHTNING_OPS_ROUTE_KEY, ch.channel_point)
                  : `#${LIGHTNING_OPS_ROUTE_KEY}`
                return (
                  <Fragment key={ch.channel_point || String(ch.channel_id)}>
                  <tr
                    id={desktopChannelRowID(ch.channel_point)}
                    className={`border-t border-white/5 group ${highlight} ${isFocused ? 'bg-sky-500/20' : ''}`}
                  >
                    <td className="py-3" title={scoreTitle}>
                      <a
                        className="text-fog hover:text-white hover:underline underline-offset-2"
                        href={lightningOpsLink}
                        title={t('rebalanceCenter.channels.openInLightningOps')}
                      >
                        {ch.peer_alias || ch.remote_pubkey}
                      </a>
                      <div className="text-xs text-fog/50">{ch.channel_point}</div>
                      {scoreMeta && (
                        <div className="text-xs text-fog/40 opacity-0 transition group-hover:opacity-100">
                          {t('rebalanceCenter.channels.scoreLabel')}: {formatSats(scoreMeta.score)}
                        </div>
                      )}
                    </td>
                  <td className="py-3 pl-4">
                    <div>{formatPct(ch.local_pct)} / {formatPct(ch.remote_pct)}</div>
                    <div className="text-xs text-fog/50">{formatSats(ch.local_balance_sat)} | {formatSats(ch.remote_balance_sat)}</div>
                  </td>
                  <td className="py-3 pl-4">
                    <div>
                      {t('rebalanceCenter.channels.feeOut', { value: ch.outgoing_fee_ppm })} ·{' '}
                      {t('rebalanceCenter.channels.feePeer', { value: ch.peer_fee_rate_ppm })}
                    </div>
                    <div className="text-xs text-fog/50">{t('rebalanceCenter.channels.spread', { value: ch.spread_ppm })}</div>
                  </td>
                  <td className="py-3 pl-4">
                    <div className="flex items-center gap-2">
                      <input
                        className="input-field w-14"
                        type="number"
                        min={1}
                        max={99}
                        step={0.1}
                        title={t('rebalanceCenter.channelsHints.targetOutbound')}
                        value={editTargets[channelKey(ch)] ?? String(Math.round(ch.target_outbound_pct * 10) / 10)}
                        onChange={(e) => setEditTargets((prev) => ({ ...prev, [channelKey(ch)]: e.target.value }))}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            e.preventDefault()
                            handleSaveChannelSettings(ch)
                          }
                        }}
                      />
                      <span className="text-xs text-fog/60">%</span>
                      <input
                        className="input-field h-8 w-10 px-1 py-1 text-xs"
                        type="text"
                        inputMode="decimal"
                        title={t('rebalanceCenter.channelsHints.econRatio')}
                        value={channelEconRatioInput(ch)}
                        disabled={channelUseDefaultEconRatio(ch)}
                        onChange={(e) => setEditEconRatios((prev) => ({ ...prev, [channelKey(ch)]: e.target.value }))}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            e.preventDefault()
                            handleSaveChannelSettings(ch)
                          }
                        }}
                      />
                      <label className="flex items-center gap-1 whitespace-nowrap text-[11px]" title={t('rebalanceCenter.channelsHints.useDefaultEconRatio')}>
                        <input
                          type="checkbox"
                          checked={channelUseDefaultEconRatio(ch)}
                          onChange={(e) => handleUseDefaultEconRatioToggle(ch, e.target.checked)}
                        />
                        {t('rebalanceCenter.channels.useDefaultEconRatio')}
                      </label>
                    </div>
                    <label className="mt-1 flex items-center gap-1 whitespace-nowrap text-[11px] text-amber-100/80" title={t('rebalanceCenter.channelsHints.autoBypassCostGate')}>
                      <input
                        type="checkbox"
                        checked={channelAutoBypassCostGate(ch)}
                        onChange={(e) => void handleAutoBypassCostGateToggle(ch, e.target.checked)}
                      />
                      {t('rebalanceCenter.channels.autoBypassCostGate')}
                    </label>
                    <div className="text-xs text-fog/50">
                      {t('rebalanceCenter.channels.amount', { value: formatSats(ch.target_amount_sat) })}
                    </div>
                  </td>
                  <td className="py-3 text-center">
                    <div>{formatSats(ch.effective_protected_sat ?? 0)}</div>
                    <div className="text-xs text-fog/50">{t('rebalanceCenter.channels.payback', { value: (ch.payback_progress * 100).toFixed(0) })}</div>
                  </td>
                  <td className="py-3 pl-4 text-center text-xs text-fog/60" title={t('rebalanceCenter.channelsHints.timeToPayback')}>
                    {formatTimeToPayback(ch)}
                  </td>
                  <td className="py-3 space-y-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <button
                        className="btn-secondary text-xs px-3 py-1"
                        onClick={() => handleSaveChannelSettings(ch)}
                        title={t('rebalanceCenter.channelsHints.save')}
                      >
                        {t('rebalanceCenter.channels.save')}
                      </button>
                      <button
                        className="btn-primary text-xs px-3 py-1"
                        onClick={() => handleRunRebalance(ch)}
                        disabled={!ch.eligible_as_manual_target}
                        title={t('rebalanceCenter.channelsHints.rebalanceIn')}
                      >
                        {t('rebalanceCenter.channels.rebalanceIn')}
                      </button>
                      <div
                        className="flex flex-col items-center gap-1 text-[10px] text-fog/60"
                        title={t('rebalanceCenter.channelsHints.rebalanceRestart')}
                      >
                        <span className="text-sm">⟳</span>
                        <input
                          type="checkbox"
                          checked={manualRestartSelected}
                          onChange={(e) => handleManualRestartToggle(ch, e.target.checked)}
                        />
                      </div>
                      <button
                        className="btn-secondary text-xs px-3 py-1"
                        onClick={() => handleTogglePairStats(ch)}
                        title={t('rebalanceCenter.channelsHints.pairStats')}
                      >
                        {pairStatsOpen[channelKey(ch)] ? t('common.hide') : t('rebalanceCenter.channels.pairStats')}
                      </button>
                    </div>
                    {manualRestartBudgetLow && scoreMeta && (
                      <div className="text-xs text-amber-200">
                        {t('rebalanceCenter.channels.manualRestartBudgetWarning', {
                          cost: formatSats(scoreMeta.estimatedCost),
                          budget: formatSats(remainingTotalSat)
                        })}
                      </div>
                    )}
                    <div className="flex flex-wrap items-center gap-2 text-xs">
                      <label className="flex items-center gap-2" title={t('rebalanceCenter.channelsHints.auto')}>
                        <input
                          type="checkbox"
                        checked={ch.auto_enabled}
                        onChange={(e) => handleToggleChannelAuto(ch, e.target.checked)}
                      />
                        {t('rebalanceCenter.channels.auto')}
                      </label>
                      <label className="flex items-center gap-2" title={t('rebalanceCenter.channelsHints.excludeSource')}>
                        <input
                          type="checkbox"
                          checked={ch.excluded_as_source}
                          onChange={(e) => handleExcludeSource(ch, e.target.checked)}
                        />
                        {t('rebalanceCenter.channels.excludeSource')}
                      </label>
                    </div>
                    <div className="text-xs text-fog/50">
                      {scoreMeta && expectedRoiValid
                        ? t('rebalanceCenter.channels.roiEstimate', { value: formatRoi(expectedRoi) })
                        : t('rebalanceCenter.channels.roiEstimateNA')}
                    </div>
                    </td>
                  </tr>
                  {pairStatsOpen[channelKey(ch)] && (
                    <tr className="border-t border-white/5 bg-white/[0.02]">
                      <td colSpan={7} className="px-3 pb-3">
                        {renderPairStatsPanel(ch)}
                      </td>
                    </tr>
                  )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('rebalanceCenter.queue.title')}</h3>
          {queueJobs.length === 0 && <p className="text-sm text-fog/60">{t('rebalanceCenter.queue.empty')}</p>}
          <div className="max-h-80 space-y-3 overflow-y-auto pr-1">
            {queueJobs.map((job) => (
              <div key={job.id} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm text-fog">#{job.id} {job.source}</p>
                    <p className="text-xs text-fog/70">{t('rebalanceCenter.queue.targetLabel', { value: job.target_peer_alias || job.target_channel_point })}</p>
                    <p className="text-xs text-fog/50">{job.target_channel_point}</p>
                  </div>
                  <span className={`text-xs uppercase tracking-wide ${statusClass(job.status)}`}>{job.status}</span>
                </div>
                <div className="mt-2 text-xs text-fog/50">
                  {t('rebalanceCenter.queue.target', { value: formatPct(job.target_outbound_pct) })}
                </div>
                {job.status === 'partial' && parseRemaining(job.reason) !== null && (
                  <div className="mt-1 text-xs text-amber-200">
                    {t('rebalanceCenter.queue.remaining', { value: formatSats(parseRemaining(job.reason) || 0) })}
                  </div>
                )}
                {job.reason && job.status !== 'partial' && (
                  <div className="mt-1 text-xs text-amber-200">{job.reason}</div>
                )}
                {queueAttempts.filter((attempt) => attempt.job_id === job.id).map((attempt) => (
                  <div key={attempt.id} className="mt-2 text-xs text-fog/60">
                    {attempt.attempt_index === 0
                      ? t('rebalanceCenter.queue.fastPathAttempt', {
                          amount: formatSats(attempt.amount_sat),
                          fee: attempt.fee_limit_ppm
                        })
                      : t('rebalanceCenter.queue.attempt', {
                          index: attempt.attempt_index,
                          amount: formatSats(attempt.amount_sat),
                          fee: attempt.fee_limit_ppm
                        })}
                    {attempt.source_peer_alias && (
                      <span className="text-fog/50">
                        {' '}
                        {t('rebalanceCenter.queue.sourceInline', { value: attempt.source_peer_alias })}
                      </span>
                    )}
                    {(() => {
                      if (attempt.status === 'succeeded') {
                        return (
                          <div className="mt-1 text-xs text-emerald-200">
                            {t('rebalanceCenter.queue.routeFound')}
                          </div>
                        )
                      }
                      const reason = attempt.fail_reason?.toLowerCase() || ''
                      if (reason.includes('no route')) {
                        return (
                          <div className="mt-1 text-xs text-amber-200">
                            {t('rebalanceCenter.queue.routeNotFound')}
                          </div>
                        )
                      }
                      if (reason.includes('route failed') || reason.includes('fee exceeds limit')) {
                        return (
                          <div className="mt-1 text-xs text-amber-200">
                            {t('rebalanceCenter.queue.routeFailed')}
                          </div>
                        )
                      }
                      return null
                    })()}
                    {attempt.fail_reason && (
                      <div className="mt-1 text-xs text-amber-200">
                        {t('rebalanceCenter.queue.reason', { value: attempt.fail_reason })}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            ))}
          </div>
        </div>

        <div className="section-card space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold">{t('rebalanceCenter.history.title')}</h3>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.history.last24h')}</p>
            </div>
            <div className="flex flex-wrap items-center gap-2 text-xs">
              {(['all', 'succeeded', 'partial', 'failed', 'skipped'] as const).map((filter) => (
                <button
                  key={filter}
                  className={`rounded-full border px-3 py-1 ${
                    historyFilter === filter ? 'border-mint text-mint' : 'border-white/10 text-fog/60'
                  }`}
                  onClick={() => setHistoryFilter(filter)}
                >
                  {t(`rebalanceCenter.history.filters.${filter}`)}
                </button>
              ))}
            </div>
          </div>
          {filteredHistory.length === 0 && <p className="text-sm text-fog/60">{t('rebalanceCenter.history.empty')}</p>}
          <div className="max-h-80 space-y-3 overflow-y-auto pr-1">
            {filteredHistory.map((job) => (
              <div key={job.id} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm text-fog">
                      #{job.id}{' '}
                      {job.source === 'auto'
                        ? t('rebalanceCenter.history.source.auto')
                        : job.source === 'manual'
                          ? t('rebalanceCenter.history.source.manual')
                          : job.source}{' '}
                      <span className="text-xs text-fog/50">
                         · {formatTimestamp(job.completed_at || job.created_at)}
                      </span>
                    </p>
                    <p className="text-xs text-fog/70">{t('rebalanceCenter.history.targetLabel', { value: job.target_peer_alias || job.target_channel_point })}</p>
                    <p className="text-xs text-fog/50">{job.target_channel_point}</p>
                  </div>
                  <div className="flex flex-col items-end gap-2">
                    <span className={`text-xs uppercase tracking-wide ${statusClass(job.status)}`}>{job.status}</span>
                    <button
                      className="text-xs text-fog/60 underline decoration-white/30 underline-offset-4 hover:text-fog"
                      onClick={() =>
                        setHistoryExpanded((prev) => ({
                          ...prev,
                          [job.id]: !prev[job.id]
                        }))
                      }
                    >
                      {historyExpanded[job.id] ? t('common.hide') : t('rebalanceCenter.history.details')}
                    </button>
                  </div>
                </div>
                <div className="mt-2 text-xs text-fog/50">
                  {t('rebalanceCenter.history.target', { value: formatPct(job.target_outbound_pct) })}
                </div>
                {job.status === 'succeeded' && (
                  <div className="mt-1 text-xs text-fog/60">
                    {t('rebalanceCenter.history.amountFee', {
                      amount: formatSats(historyTotals.get(job.id)?.amount ?? 0),
                      fee: formatSats(historyTotals.get(job.id)?.fee ?? 0)
                    })}
                  </div>
                )}
                {job.status === 'partial' && parseRemaining(job.reason) !== null && (
                  <div className="mt-1 text-xs text-amber-200">
                    {t('rebalanceCenter.history.remaining', { value: formatSats(parseRemaining(job.reason) || 0) })}
                  </div>
                )}
                {job.reason && job.status !== 'partial' && (
                  <div className="mt-1 text-xs text-amber-200">{job.reason}</div>
                )}
                {historyExpanded[job.id] && (
                  <div className="mt-3 border-t border-white/10 pt-3">
                    {(historyAttemptsByJob.get(job.id) || []).map((attempt) => (
                      <div key={attempt.id} className="mt-2 text-xs text-fog/60">
                        {attempt.attempt_index === 0
                          ? t('rebalanceCenter.queue.fastPathAttempt', {
                              amount: formatSats(attempt.amount_sat),
                              fee: attempt.fee_limit_ppm
                            })
                          : t('rebalanceCenter.queue.attempt', {
                              index: attempt.attempt_index,
                              amount: formatSats(attempt.amount_sat),
                              fee: attempt.fee_limit_ppm
                            })}
                        {attempt.source_peer_alias && (
                          <span className="text-fog/50">
                            {' '}
                            {t('rebalanceCenter.queue.sourceInline', { value: attempt.source_peer_alias })}
                          </span>
                        )}
                        {(() => {
                          if (attempt.status === 'succeeded') {
                            return (
                              <div className="mt-1 text-xs text-emerald-200">
                                {t('rebalanceCenter.queue.routeFound')}
                              </div>
                            )
                          }
                          const reason = attempt.fail_reason?.toLowerCase() || ''
                          if (reason.includes('no route')) {
                            return (
                              <div className="mt-1 text-xs text-amber-200">
                                {t('rebalanceCenter.queue.routeNotFound')}
                              </div>
                            )
                          }
                          if (reason.includes('route failed') || reason.includes('fee exceeds limit')) {
                            return (
                              <div className="mt-1 text-xs text-amber-200">
                                {t('rebalanceCenter.queue.routeFailed')}
                              </div>
                            )
                          }
                          return null
                        })()}
                        {attempt.fail_reason && (
                          <div className="mt-1 text-xs text-amber-200">
                            {t('rebalanceCenter.queue.reason', { value: attempt.fail_reason })}
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  )
}
