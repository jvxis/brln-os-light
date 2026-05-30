import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getAutofeeChannels,
  getAutofeeConfig,
  getAutofeeResults,
  getAutofeeStatus,
  getLnChannels,
  refreshAutofeeReferences,
  runAutofee,
  updateAutofeeConfig,
} from '../api'
import HorizontalBarGauge from '../components/dashboard/HorizontalBarGauge'
import MetricTile from '../components/dashboard/MetricTile'
import StackedRatioBar from '../components/dashboard/StackedRatioBar'
import StatusBadge from '../components/dashboard/StatusBadge'
import type { Tone } from '../components/dashboard/types'
import { getLocale } from '../i18n'

type AutofeeProfileDefaults = {
  run_interval_sec?: number
  cooldown_up_sec?: number
  cooldown_down_sec?: number
  step_cap?: number
  discovery_step_cap_down?: number
  stall_floor_relax_gap_frac?: number
  inbound_discount_max_ratio?: number
  inbound_discount_reach_out_ratio?: number
  inbound_discount_min_retained_spread_frac?: number
  outrate_floor_factor_low?: number
  soften_min_out_ratio?: number
  soften_max_drop_to_peg_frac?: number
  htlc_min_attempts_60m?: number
  htlc_policy_fail_rate?: number
  htlc_liquidity_fail_rate?: number
}

type AutofeeConfig = {
  enabled: boolean
  operation_mode?: string
  profile?: string
  lookback_days?: number
  run_interval_sec?: number
  cooldown_up_sec?: number
  cooldown_down_sec?: number
  step_cap_override?: number
  discovery_step_cap_down_override?: number
  stall_floor_relax_gap_frac_override?: number
  inbound_discount_max_ratio_override?: number
  inbound_discount_reach_out_ratio_override?: number
  inbound_discount_min_retained_spread_frac_override?: number
  outrate_floor_factor_low_override?: number
  soften_min_out_ratio_override?: number
  soften_max_drop_to_peg_frac_override?: number
  htlc_min_attempts_60m_override?: number
  htlc_policy_fail_rate_override?: number
  htlc_liquidity_fail_rate_override?: number
  rebal_cost_mode?: string
  min_ppm?: number
  max_ppm?: number
  native_seed_enabled?: boolean
  amboss_enabled?: boolean
  amboss_token_set?: boolean
  inbound_passive_enabled?: boolean
  discovery_enabled?: boolean
  explorer_enabled?: boolean
  idle_refresh_enabled?: boolean
  super_source_enabled?: boolean
  super_source_base_fee_msat?: number
  revfloor_enabled?: boolean
  circuit_breaker_enabled?: boolean
  extreme_drain_enabled?: boolean
  htlc_signal_enabled?: boolean
  htlc_mode?: string
  profile_defaults?: Record<string, AutofeeProfileDefaults>
}

type AutofeeStatus = {
  running?: boolean
  last_run_at?: string
  next_run_at?: string
  last_error?: string
}

type AutofeeChannelSetting = {
  channel_id?: number | string
  channel_id_str?: string
  channel_point?: string
  enabled?: boolean
}

type AutofeeResultItem = {
  kind?: string
  category?: string
  reason?: string
  operation_mode?: string
  dry_run?: boolean
  timestamp?: string
  up?: number
  down?: number
  flat?: number
  cooldown?: number
  small?: number
  same?: number
  disabled?: number
  inactive?: number
  inbound_disc?: number
  super_source?: number
  updated_count?: number
  same_count?: number
  skipped_count?: number
  error_count?: number
  native?: number
  native_insufficient?: number
  native_err?: number
  amboss?: number
  missing?: number
  err?: number
  empty?: number
  outrate?: number
  mem?: number
  default?: number
  node_class?: string
  liquidity_class?: string
  channel_count?: number
  total_capacity_sat?: number
  local_capacity_sat?: number
  local_ratio?: number
  alias?: string
  channel_id?: number | string
  channel_point?: string
  local_ppm?: number
  new_ppm?: number
  target?: number
  target_raw?: number
  target_final?: number
  out_ratio?: number
  out_ppm7d?: number
  rebal_ppm7d?: number
  seed?: number
  floor?: number
  floor_src?: string
  floor_base_ppm?: number
  floor_base_src?: string
  rebal_cost_mode?: string
  margin?: number
  rev_share?: number
  tags?: string[]
  inbound_discount?: number
  prev_inbound_discount?: number
  class_label?: string
  skip_reason?: string
  error?: string
  delta?: number
  delta_pct?: number
  prediction_code?: string
  new_inbound?: boolean
  current_inbound_discount?: number
  target_inbound_discount?: number
  inbound_source?: string
  reference_ppm?: number
  refresh_source?: string
}

type AutofeeRunGroup = {
  header?: AutofeeResultItem
  summary?: AutofeeResultItem
  seed?: AutofeeResultItem
  calib?: AutofeeResultItem
  channels: AutofeeResultItem[]
  lines: string[]
}

type Channel = {
  channel_point: string
  channel_id: number
  channel_id_str?: string
  remote_pubkey?: string
  peer_alias?: string
  active?: boolean
  capacity_sat?: number
  local_balance_sat?: number
  remote_balance_sat?: number
  base_fee_msat?: number
  fee_rate_ppm?: number
  inbound_fee_rate_ppm?: number
  peer_fee_rate_ppm?: number
  peer_base_msat?: number
  out_ppm7d?: number
  rebal_ppm7d?: number
  forward_fee_7d_sat?: number
  rebal_fee_7d_sat?: number
  profit_fee_7d_sat?: number
}

type PolicyRow = {
  channel: Channel
  enabled: boolean
  last?: AutofeeResultItem
}

const LIGHTNING_OPS_ROUTE_KEY = 'lightning-ops'
const REBALANCE_ROUTE_KEY = 'rebalance-center'
const CHANNEL_HASH_PARAM = 'channel_point'

const buildHashWithChannelPoint = (routeKey: string, channelPoint: string) =>
  `#${routeKey}?${CHANNEL_HASH_PARAM}=${encodeURIComponent(channelPoint)}`

const channelImpactDomID = (channelPoint: string) =>
  `fee-center-impact-${channelPoint.replace(/[^a-zA-Z0-9_-]/g, '_')}`

const channelPolicyDomID = (channelPoint: string) =>
  `fee-center-policy-${channelPoint.replace(/[^a-zA-Z0-9_-]/g, '_')}`

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

const normalizeChannelID = (channelID?: number | string) => {
  if (typeof channelID === 'string') {
    const trimmed = channelID.trim()
    return trimmed || ''
  }
  if (typeof channelID === 'number' && Number.isFinite(channelID)) {
    return String(Math.trunc(channelID))
  }
  return ''
}

const channelKey = (channelPoint?: string, channelID?: number | string) => {
  const point = (channelPoint || '').trim()
  if (point) return point
  const id = normalizeChannelID(channelID)
  return id ? `id:${id}` : ''
}

const pctOverrideToInput = (value?: number, fractionDigits = 0) => {
  const numeric = Number(value || 0)
  if (!Number.isFinite(numeric) || numeric <= 0) return ''
  const scaled = numeric * 100
  return fractionDigits > 0
    ? String(Math.round(scaled * 10 ** fractionDigits) / 10 ** fractionDigits)
    : String(Math.round(scaled))
}

const numberOrZero = (value?: number) => {
  const numeric = Number(value || 0)
  return Number.isFinite(numeric) ? numeric : 0
}

const median = (values: number[]) => {
  const sorted = values.filter(Number.isFinite).sort((a, b) => a - b)
  if (!sorted.length) return 0
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[middle] : Math.round((sorted[middle - 1] + sorted[middle]) / 2)
}

const clamp = (value: number, min = 0, max = 100) => Math.max(min, Math.min(max, value))

const groupAutofeeRuns = (items: AutofeeResultItem[], lines: string[] = []) => {
  const groups: AutofeeRunGroup[] = []
  let current: AutofeeRunGroup | null = null

  items.forEach((raw, index) => {
    const item = raw && typeof raw === 'object' ? raw : {}
    const line = lines[index] || ''
    if (item.kind === 'header') {
      current = { header: item, channels: [], lines: line ? [line] : [] }
      groups.push(current)
      return
    }
    if (!current) {
      current = { channels: [], lines: [] }
      groups.push(current)
    }
    if (line) current.lines.push(line)
    if (item.kind === 'summary' || item.kind === 'refresh_summary') current.summary = item
    if (item.kind === 'seed') current.seed = item
    if (item.kind === 'calib') current.calib = item
    if (item.kind === 'channel') current.channels.push(item)
  })

  return groups
}

const topTags = (items: AutofeeResultItem[], limit = 6) => {
  const counts = new Map<string, number>()
  items.forEach((item) => {
    ;(item.tags || []).forEach((tag) => {
      const key = String(tag || '').trim()
      if (!key) return
      counts.set(key, (counts.get(key) || 0) + 1)
    })
  })
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, limit)
}

export default function FeeCenter() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const focusClearTimerRef = useRef<number | null>(null)
  const pendingFocusRef = useRef('')

  const [config, setConfig] = useState<AutofeeConfig | null>(null)
  const [status, setStatus] = useState<AutofeeStatus | null>(null)
  const [channels, setChannels] = useState<Channel[]>([])
  const [channelSettings, setChannelSettings] = useState<Record<string, boolean>>({})
  const [resultItems, setResultItems] = useState<AutofeeResultItem[]>([])
  const [resultLines, setResultLines] = useState<string[]>([])
  const [pageStatus, setPageStatus] = useState('')
  const [busy, setBusy] = useState(false)
  const [focusedChannelPoint, setFocusedChannelPoint] = useState('')
  const [resultsRuns, setResultsRuns] = useState('4')
  const [resultsFrom, setResultsFrom] = useState('')
  const [resultsTo, setResultsTo] = useState('')
  const [logOpen, setLogOpen] = useState(false)
  const [advancedOpen, setAdvancedOpen] = useState(false)

  const [enabled, setEnabled] = useState(false)
  const [operationMode, setOperationMode] = useState('balanced')
  const [profile, setProfile] = useState('moderate')
  const [lookback, setLookback] = useState('7')
  const [intervalHours, setIntervalHours] = useState('4')
  const [cooldownUp, setCooldownUp] = useState('3')
  const [cooldownDown, setCooldownDown] = useState('4')
  const [stepCapOverride, setStepCapOverride] = useState('')
  const [discoveryStepCapDownOverride, setDiscoveryStepCapDownOverride] = useState('')
  const [stallFloorRelaxGapFracOverride, setStallFloorRelaxGapFracOverride] = useState('')
  const [inboundDiscountMaxRatioOverride, setInboundDiscountMaxRatioOverride] = useState('')
  const [inboundDiscountReachOutRatioOverride, setInboundDiscountReachOutRatioOverride] = useState('')
  const [inboundDiscountMinRetainedSpreadFracOverride, setInboundDiscountMinRetainedSpreadFracOverride] = useState('')
  const [outrateFloorFactorLowOverride, setOutrateFloorFactorLowOverride] = useState('')
  const [softenMinOutRatioOverride, setSoftenMinOutRatioOverride] = useState('')
  const [softenMaxDropToPegFracOverride, setSoftenMaxDropToPegFracOverride] = useState('')
  const [htlcMinAttemptsOverride, setHtlcMinAttemptsOverride] = useState('')
  const [htlcPolicyFailRateOverride, setHtlcPolicyFailRateOverride] = useState('')
  const [htlcLiquidityFailRateOverride, setHtlcLiquidityFailRateOverride] = useState('')
  const [rebalMode, setRebalMode] = useState('blend')
  const [minPpm, setMinPpm] = useState('10')
  const [maxPpm, setMaxPpm] = useState('2000')
  const [nativeSeedEnabled, setNativeSeedEnabled] = useState(false)
  const [ambossEnabled, setAmbossEnabled] = useState(false)
  const [ambossToken, setAmbossToken] = useState('')
  const [refreshIncludeInbound, setRefreshIncludeInbound] = useState(true)
  const [inboundPassive, setInboundPassive] = useState(false)
  const [discovery, setDiscovery] = useState(true)
  const [explorer, setExplorer] = useState(true)
  const [idleRefresh, setIdleRefresh] = useState(false)
  const [superSource, setSuperSource] = useState(false)
  const [superSourceBaseFee, setSuperSourceBaseFee] = useState('1000')
  const [revfloor, setRevfloor] = useState(true)
  const [circuitBreaker, setCircuitBreaker] = useState(true)
  const [extremeDrain, setExtremeDrain] = useState(true)
  const [htlcSignalEnabled, setHtlcSignalEnabled] = useState(true)
  const [htlcMode, setHtlcMode] = useState('full')

  const applyConfig = (cfg: AutofeeConfig) => {
    setConfig(cfg)
    setEnabled(Boolean(cfg.enabled))
    setOperationMode((cfg.operation_mode || 'balanced').trim() || 'balanced')
    setProfile(cfg.profile || 'moderate')
    setLookback(String(cfg.lookback_days ?? 7))
    setIntervalHours(String(Math.max(1, Math.round((cfg.run_interval_sec || 14400) / 3600))))
    setCooldownUp(String(Math.max(1, Math.round((cfg.cooldown_up_sec || 10800) / 3600))))
    setCooldownDown(String(Math.max(1, Math.round((cfg.cooldown_down_sec || 14400) / 3600))))
    setStepCapOverride(pctOverrideToInput(cfg.step_cap_override, 1))
    setDiscoveryStepCapDownOverride(pctOverrideToInput(cfg.discovery_step_cap_down_override, 1))
    setStallFloorRelaxGapFracOverride(pctOverrideToInput(cfg.stall_floor_relax_gap_frac_override))
    setInboundDiscountMaxRatioOverride(pctOverrideToInput(cfg.inbound_discount_max_ratio_override))
    setInboundDiscountReachOutRatioOverride(pctOverrideToInput(cfg.inbound_discount_reach_out_ratio_override))
    setInboundDiscountMinRetainedSpreadFracOverride(pctOverrideToInput(cfg.inbound_discount_min_retained_spread_frac_override))
    setOutrateFloorFactorLowOverride(pctOverrideToInput(cfg.outrate_floor_factor_low_override))
    setSoftenMinOutRatioOverride(pctOverrideToInput(cfg.soften_min_out_ratio_override))
    setSoftenMaxDropToPegFracOverride(pctOverrideToInput(cfg.soften_max_drop_to_peg_frac_override))
    setHtlcMinAttemptsOverride((cfg.htlc_min_attempts_60m_override ?? 0) > 0 ? String(cfg.htlc_min_attempts_60m_override) : '')
    setHtlcPolicyFailRateOverride(pctOverrideToInput(cfg.htlc_policy_fail_rate_override, 1))
    setHtlcLiquidityFailRateOverride(pctOverrideToInput(cfg.htlc_liquidity_fail_rate_override, 1))
    setRebalMode(cfg.rebal_cost_mode || 'blend')
    setMinPpm(String(cfg.min_ppm ?? 10))
    setMaxPpm(String(cfg.max_ppm ?? 2000))
    setNativeSeedEnabled(Boolean(cfg.native_seed_enabled))
    setAmbossEnabled(Boolean(cfg.amboss_enabled))
    setInboundPassive(Boolean(cfg.inbound_passive_enabled))
    setDiscovery(Boolean(cfg.discovery_enabled))
    setExplorer(Boolean(cfg.explorer_enabled))
    setIdleRefresh(Boolean(cfg.idle_refresh_enabled))
    setSuperSource(Boolean(cfg.super_source_enabled))
    setSuperSourceBaseFee(String(cfg.super_source_base_fee_msat ?? 1000))
    setRevfloor(cfg.revfloor_enabled !== false)
    setCircuitBreaker(cfg.circuit_breaker_enabled !== false)
    setExtremeDrain(cfg.extreme_drain_enabled !== false)
    setHtlcSignalEnabled(cfg.htlc_signal_enabled !== false)
    {
      const normalized = (cfg.htlc_mode || 'full').toLowerCase().trim()
      setHtlcMode(normalized === 'observe_only' || normalized === 'policy_only' || normalized === 'full' ? normalized : 'full')
    }
  }

  const load = async () => {
    setPageStatus(t('feeCenter.loading'))
    const [configRes, statusRes, resultsRes, channelsRes, settingsRes] = await Promise.allSettled([
      getAutofeeConfig(),
      getAutofeeStatus(),
      getAutofeeResults({ runs: 4 }),
      getLnChannels(),
      getAutofeeChannels(),
    ])

    if (configRes.status === 'fulfilled') {
      applyConfig(configRes.value as AutofeeConfig)
    }
    if (statusRes.status === 'fulfilled') {
      setStatus(statusRes.value as AutofeeStatus)
    }
    if (resultsRes.status === 'fulfilled') {
      const payload = resultsRes.value as any
      setResultItems(Array.isArray(payload?.items) ? payload.items : [])
      setResultLines(Array.isArray(payload?.lines) ? payload.lines : [])
    }
    if (channelsRes.status === 'fulfilled') {
      const payload = channelsRes.value as any
      setChannels(Array.isArray(payload?.channels) ? payload.channels : [])
    }
    if (settingsRes.status === 'fulfilled') {
      const settingsPayload = (settingsRes.value as any)?.settings as AutofeeChannelSetting[] | undefined
      const map: Record<string, boolean> = {}
      if (Array.isArray(settingsPayload)) {
        settingsPayload.forEach((item) => {
          const key = channelKey(item.channel_point, item.channel_id_str || item.channel_id)
          if (key) map[key] = item.enabled !== false
        })
      }
      setChannelSettings(map)
    }

    const firstError = [configRes, statusRes, resultsRes, channelsRes, settingsRes].find((res) => res.status === 'rejected')
    if (firstError?.status === 'rejected') {
      setPageStatus((firstError.reason as any)?.message || t('feeCenter.partialLoadFailed'))
    } else {
      setPageStatus('')
    }
  }

  useEffect(() => {
    pendingFocusRef.current = readHashChannelPoint('fee-center')
    void load()
    return () => {
      if (focusClearTimerRef.current !== null) {
        window.clearTimeout(focusClearTimerRef.current)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const runs = useMemo(() => groupAutofeeRuns(resultItems, resultLines), [resultItems, resultLines])
  const latestRun = runs[0]
  const latestSummary = latestRun?.summary
  const latestSeed = latestRun?.seed
  const latestChanged = useMemo(
    () => (latestRun?.channels || []).filter((item) => item.category === 'changed'),
    [latestRun]
  )
  const latestChangedByKey = useMemo(() => {
    const map: Record<string, AutofeeResultItem> = {}
    latestChanged.forEach((item) => {
      const key = channelKey(item.channel_point, item.channel_id)
      if (key) map[key] = item
    })
    return map
  }, [latestChanged])

  const feeUp = useMemo(
    () => latestChanged
      .filter((item) => numberOrZero(item.new_ppm) > numberOrZero(item.local_ppm))
      .sort((a, b) => Math.abs(numberOrZero(b.delta)) - Math.abs(numberOrZero(a.delta))),
    [latestChanged]
  )
  const feeDown = useMemo(
    () => latestChanged
      .filter((item) => numberOrZero(item.new_ppm) < numberOrZero(item.local_ppm))
      .sort((a, b) => Math.abs(numberOrZero(b.delta)) - Math.abs(numberOrZero(a.delta))),
    [latestChanged]
  )
  const specialChanges = useMemo(
    () => (latestRun?.channels || [])
      .filter((item) => item.new_inbound || numberOrZero(item.inbound_discount) !== numberOrZero(item.prev_inbound_discount) || item.category === 'error' || item.refresh_source)
      .slice(0, 12),
    [latestRun]
  )

  const policyRows = useMemo<PolicyRow[]>(() => {
    return channels.map((channel) => {
      const key = channelKey(channel.channel_point, channel.channel_id)
      return {
        channel,
        enabled: key ? (channelSettings[key] ?? true) : true,
        last: key ? latestChangedByKey[key] : undefined,
      }
    })
  }, [channels, channelSettings, latestChangedByKey])

  useEffect(() => {
    const targetPoint = pendingFocusRef.current
    if (!targetPoint || !policyRows.length) return
    const target = document.getElementById(channelImpactDomID(targetPoint)) || document.getElementById(channelPolicyDomID(targetPoint))
    if (!target) return
    target.scrollIntoView({ behavior: 'smooth', block: 'center' })
    setFocusedChannelPoint(targetPoint)
    pendingFocusRef.current = ''
    window.history.replaceState(null, '', '#fee-center')
    if (focusClearTimerRef.current !== null) {
      window.clearTimeout(focusClearTimerRef.current)
    }
    focusClearTimerRef.current = window.setTimeout(() => {
      setFocusedChannelPoint((current) => (current === targetPoint ? '' : current))
      focusClearTimerRef.current = null
    }, 3200)
  }, [policyRows])

  const feePpms = useMemo(
    () => channels.map((channel) => numberOrZero(channel.fee_rate_ppm)).filter((value) => value > 0),
    [channels]
  )
  const avgPolicyPpm = feePpms.length
    ? Math.round(feePpms.reduce((sum, value) => sum + value, 0) / feePpms.length)
    : 0
  const medianPolicyPpm = median(feePpms)
  const maxPolicySeen = feePpms.length ? Math.max(...feePpms) : 0
  const activePolicyCount = policyRows.filter((row) => row.enabled).length
  const inactivePolicyCount = Math.max(0, policyRows.length - activePolicyCount)
  const targetAligned = latestChanged.filter((item) => Math.abs(numberOrZero(item.target_final ?? item.target) - numberOrZero(item.new_ppm)) <= 5).length
  const pressureTags = topTags(latestRun?.channels || [])
  const runTrend = runs
    .map((run) => ({ value: numberOrZero(run.summary?.up) + numberOrZero(run.summary?.down) }))
    .reverse()

  const runTone: Tone = status?.last_error ? 'danger' : status?.running ? 'ok' : enabled ? 'info' : 'muted'
  const changedTotal = numberOrZero(latestSummary?.up) + numberOrZero(latestSummary?.down)
  const seedTotal = numberOrZero(latestSeed?.native)
    + numberOrZero(latestSeed?.amboss)
    + numberOrZero(latestSeed?.outrate)
    + numberOrZero(latestSeed?.mem)
    + numberOrZero(latestSeed?.default)
    + numberOrZero(latestSeed?.missing)
    + numberOrZero(latestSeed?.empty)
    + numberOrZero(latestSeed?.err)
    + numberOrZero(latestSeed?.native_err)
    + numberOrZero(latestSeed?.native_insufficient)
  const seedKnown = numberOrZero(latestSeed?.native) + numberOrZero(latestSeed?.amboss) + numberOrZero(latestSeed?.outrate) + numberOrZero(latestSeed?.mem)
  const seedCoveragePct = seedTotal > 0 ? Math.round((seedKnown / seedTotal) * 100) : 0
  const economics = useMemo(() => {
    const forward = channels.reduce((sum, channel) => sum + numberOrZero(channel.forward_fee_7d_sat), 0)
    const rebal = channels.reduce((sum, channel) => sum + numberOrZero(channel.rebal_fee_7d_sat), 0)
    const profit = channels.reduce((sum, channel) => sum + numberOrZero(channel.profit_fee_7d_sat), 0)
    return { forward, rebal, profit }
  }, [channels])

  const formatInt = (value?: number) => Math.round(numberOrZero(value)).toLocaleString(locale)
  const formatPpm = (value?: number) => `${formatInt(value)} ppm`
  const formatSats = (value?: number) => `${formatInt(value)} sats`
  const formatDate = (value?: string) => {
    if (!value) return t('common.na')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return t('common.na')
    return parsed.toLocaleString(locale)
  }
  const formatMode = (value?: string) => value === 'market_refill'
    ? t('lightningOps.autofeeOperationModeMarketRefill')
    : t('lightningOps.autofeeOperationModeBalanced')
  const formatReason = (value?: string) => {
    const normalized = String(value || '').toLowerCase().trim()
    if (normalized === 'manual') return t('lightningOps.autofeeResultsReasonManual')
    if (normalized === 'scheduled') return t('lightningOps.autofeeResultsReasonScheduled')
    if (normalized === 'refresh') return t('lightningOps.autofeeResultsReasonRefresh')
    return t('lightningOps.autofeeResultsReasonUnknown')
  }

  const activeDefaults = config?.profile_defaults?.[profile]
  const defaultText = (value?: number, percent = true) => {
    if (value === undefined || value === null) return '-'
    return percent ? `${Math.round(value * 1000) / 10}%` : String(value)
  }

  const parsePercentOverride = (raw: string, min: number, max: number) => {
    const value = Number(raw)
    if (!Number.isFinite(value) || value <= 0) return 0
    return Math.max(min, Math.min(max, value)) / 100
  }

  const handleSave = async () => {
    if (!config) {
      setPageStatus(t('lightningOps.autofeeConfigUnavailable'))
      return
    }
    setBusy(true)
    setPageStatus(t('lightningOps.autofeeSaving'))
    try {
      const lookbackDays = Math.max(5, Math.min(21, Number(lookback || 7)))
      const intervalSec = Math.max(1, Number(intervalHours || 4)) * 3600
      const cooldownUpSec = Math.max(1, Number(cooldownUp || 3)) * 3600
      const cooldownDownSec = Math.max(1, Number(cooldownDown || 4)) * 3600
      const htlcMinAttemptsRaw = Number(htlcMinAttemptsOverride || 0)
      const htlcMinAttempts = Number.isFinite(htlcMinAttemptsRaw) && htlcMinAttemptsRaw > 0
        ? Math.max(1, Math.min(100, Math.round(htlcMinAttemptsRaw)))
        : 0
      const minPpmValue = Math.max(0, Number(minPpm || 0))
      let maxPpmValue = Math.max(0, Number(maxPpm || 2000))
      if (maxPpmValue < minPpmValue) maxPpmValue = minPpmValue
      const payload: any = {
        enabled,
        operation_mode: operationMode,
        profile,
        lookback_days: lookbackDays,
        run_interval_sec: intervalSec,
        cooldown_up_sec: cooldownUpSec,
        cooldown_down_sec: cooldownDownSec,
        step_cap_override: parsePercentOverride(stepCapOverride, 1, 30),
        discovery_step_cap_down_override: parsePercentOverride(discoveryStepCapDownOverride, 1, 40),
        stall_floor_relax_gap_frac_override: parsePercentOverride(stallFloorRelaxGapFracOverride, 1, 80),
        inbound_discount_max_ratio_override: parsePercentOverride(inboundDiscountMaxRatioOverride, 50, 100),
        inbound_discount_reach_out_ratio_override: parsePercentOverride(inboundDiscountReachOutRatioOverride, 5, 50),
        inbound_discount_min_retained_spread_frac_override: parsePercentOverride(inboundDiscountMinRetainedSpreadFracOverride, 1, 50),
        outrate_floor_factor_low_override: parsePercentOverride(outrateFloorFactorLowOverride, 50, 100),
        soften_min_out_ratio_override: parsePercentOverride(softenMinOutRatioOverride, 5, 95),
        soften_max_drop_to_peg_frac_override: parsePercentOverride(softenMaxDropToPegFracOverride, 50, 100),
        htlc_min_attempts_60m_override: htlcMinAttempts,
        htlc_policy_fail_rate_override: parsePercentOverride(htlcPolicyFailRateOverride, 5, 90),
        htlc_liquidity_fail_rate_override: parsePercentOverride(htlcLiquidityFailRateOverride, 5, 90),
        rebal_cost_mode: rebalMode,
        min_ppm: minPpmValue,
        max_ppm: maxPpmValue,
        native_seed_enabled: nativeSeedEnabled,
        amboss_enabled: ambossEnabled,
        inbound_passive_enabled: inboundPassive,
        discovery_enabled: discovery,
        explorer_enabled: explorer,
        idle_refresh_enabled: idleRefresh,
        super_source_enabled: superSource,
        super_source_base_fee_msat: Math.max(0, Number(superSourceBaseFee || 1000)),
        revfloor_enabled: revfloor,
        circuit_breaker_enabled: circuitBreaker,
        extreme_drain_enabled: extremeDrain,
        htlc_signal_enabled: htlcSignalEnabled,
        htlc_mode: htlcMode,
      }
      if (ambossToken.trim()) {
        payload.amboss_token = ambossToken.trim()
      }
      const nextConfig = await updateAutofeeConfig(payload) as AutofeeConfig
      applyConfig(nextConfig)
      setAmbossToken('')
      const nextStatus = await getAutofeeStatus()
      setStatus(nextStatus as AutofeeStatus)
      setPageStatus(t('lightningOps.autofeeSaved'))
    } catch (err: any) {
      setPageStatus(err?.message || t('lightningOps.autofeeSaveFailed'))
    } finally {
      setBusy(false)
    }
  }

  const refreshResults = async (runsInput = resultsRuns) => {
    const params: { runs?: number; from?: string; to?: string } = {}
    const parsedRuns = Math.max(1, Math.min(50, Number(runsInput || 4)))
    params.runs = parsedRuns
    if (resultsFrom) params.from = new Date(resultsFrom).toISOString()
    if (resultsTo) params.to = new Date(resultsTo).toISOString()
    const payload = await getAutofeeResults(params) as any
    setResultLines(Array.isArray(payload?.lines) ? payload.lines : [])
    setResultItems(Array.isArray(payload?.items) ? payload.items : [])
  }

  const handleRun = async (dryRun: boolean) => {
    setBusy(true)
    setPageStatus(dryRun ? t('lightningOps.autofeeDryRunning') : t('lightningOps.autofeeRunning'))
    try {
      await runAutofee({ dry_run: dryRun })
      setPageStatus(dryRun ? t('lightningOps.autofeeDryRunDone') : t('lightningOps.autofeeRunDone'))
      const [nextStatus] = await Promise.all([getAutofeeStatus(), refreshResults('4')])
      setStatus(nextStatus as AutofeeStatus)
    } catch (err: any) {
      setPageStatus(err?.message || t('lightningOps.autofeeRunFailed'))
    } finally {
      setBusy(false)
    }
  }

  const handleRefresh = async (dryRun: boolean) => {
    setBusy(true)
    setPageStatus(dryRun ? t('lightningOps.autofeeDryRefreshing') : t('lightningOps.autofeeRefreshing'))
    try {
      const result = await refreshAutofeeReferences({ dry_run: dryRun, include_inbound: refreshIncludeInbound }) as any
      setPageStatus(dryRun
        ? t('lightningOps.autofeeDryRefreshDone', {
          updated: Number(result?.updated || 0),
          same: Number(result?.same || 0),
          skipped: Number(result?.skipped || 0),
          errors: Number(result?.errors || 0),
        })
        : t('lightningOps.autofeeRefreshDone', {
          updated: Number(result?.updated || 0),
          same: Number(result?.same || 0),
          skipped: Number(result?.skipped || 0),
          errors: Number(result?.errors || 0),
        }))
      const [nextStatus] = await Promise.all([getAutofeeStatus(), refreshResults('4')])
      setStatus(nextStatus as AutofeeStatus)
    } catch (err: any) {
      setPageStatus(err?.message || (dryRun ? t('lightningOps.autofeeDryRefreshFailed') : t('lightningOps.autofeeRefreshFailed')))
    } finally {
      setBusy(false)
    }
  }

  const renderToggle = (label: string, checked: boolean, onChange: (next: boolean) => void, disabled = false) => (
    <label className="flex min-w-0 items-center gap-2 text-sm text-fog/70">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />
      <span className="min-w-0 [overflow-wrap:anywhere]">{label}</span>
    </label>
  )

  const renderImpactLane = (title: string, items: AutofeeResultItem[], tone: Tone) => (
    <div className="rounded-3xl border border-white/10 bg-ink/50 p-4">
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-fog">{title}</h3>
        <StatusBadge label={items.length} tone={tone} />
      </div>
      <div className="mt-4 space-y-3">
        {items.length ? items.slice(0, 8).map((item, index) => {
          const point = item.channel_point || ''
          const local = numberOrZero(item.local_ppm)
          const next = numberOrZero(item.new_ppm)
          const maxScale = Math.max(local, next, numberOrZero(item.target_final ?? item.target), numberOrZero(item.floor), numberOrZero(item.seed), 1)
          return (
            <article
              key={`${point || item.alias || 'impact'}-${index}`}
              id={point ? channelImpactDomID(point) : undefined}
              className={`rounded-2xl border border-white/10 bg-white/[0.03] p-3 ${focusedChannelPoint === point ? 'ring-1 ring-sky-300/70 bg-sky-500/10' : ''}`}
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="text-sm font-medium text-fog [overflow-wrap:anywhere]">{item.alias || item.channel_id || t('common.unknown')}</p>
                  <p className="mt-1 text-xs text-fog/55">
                    {formatPpm(local)} -&gt; <span className={next >= local ? 'text-emerald-200' : 'text-amber-200'}>{formatPpm(next)}</span>
                    {item.delta ? ` (${item.delta > 0 ? '+' : ''}${formatPpm(item.delta)})` : ''}
                  </p>
                </div>
                {point && (
                  <div className="flex shrink-0 items-center gap-2 text-xs">
                    <a className="text-sky-200 hover:text-sky-100" href={buildHashWithChannelPoint(LIGHTNING_OPS_ROUTE_KEY, point)}>
                      {t('feeCenter.links.lightningOps')}
                    </a>
                    <a className="text-sky-200 hover:text-sky-100" href={buildHashWithChannelPoint(REBALANCE_ROUTE_KEY, point)}>
                      {t('feeCenter.links.rebalance')}
                    </a>
                  </div>
                )}
              </div>
              <div className="mt-3 space-y-2">
                <div className="relative h-2 rounded-full bg-white/10">
                  <div
                    className={`absolute left-0 top-0 h-2 rounded-full ${tone === 'ok' ? 'bg-emerald-300' : tone === 'warn' ? 'bg-amber-300' : 'bg-sky-300'}`}
                    style={{ width: `${clamp((next / maxScale) * 100)}%` }}
                  />
                  {item.floor ? <span className="absolute top-[-3px] h-4 w-px bg-rose-300" style={{ left: `${clamp((numberOrZero(item.floor) / maxScale) * 100)}%` }} /> : null}
                  {item.target_final || item.target ? <span className="absolute top-[-3px] h-4 w-px bg-sky-200" style={{ left: `${clamp((numberOrZero(item.target_final ?? item.target) / maxScale) * 100)}%` }} /> : null}
                </div>
                <div className="grid gap-1 text-[11px] text-fog/60 sm:grid-cols-3">
                  <span>{t('feeCenter.policy.target')}: {formatPpm(item.target_final ?? item.target)}</span>
                  <span>{t('feeCenter.policy.floor')}: {formatPpm(item.floor)}</span>
                  <span>{t('feeCenter.policy.seed')}: {formatPpm(item.seed)}</span>
                </div>
                {item.tags?.length ? (
                  <div className="flex flex-wrap gap-1.5">
                    {item.tags.slice(0, 5).map((tag) => (
                      <span key={tag} className="rounded-full border border-white/10 bg-white/5 px-2 py-0.5 text-[10px] text-fog/60">{tag}</span>
                    ))}
                  </div>
                ) : null}
              </div>
            </article>
          )
        }) : (
          <p className="text-sm text-fog/60">{t('feeCenter.impact.emptyLane')}</p>
        )}
      </div>
    </div>
  )

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <h2 className="text-2xl font-semibold">{t('feeCenter.title')}</h2>
            <p className="text-fog/60">{t('feeCenter.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge label={enabled ? t('common.enabled') : t('common.disabled')} tone={enabled ? 'ok' : 'muted'} size="md" />
            <StatusBadge label={status?.running ? t('feeCenter.running') : t('feeCenter.idle')} tone={status?.running ? 'ok' : 'muted'} size="md" />
            <button className="btn-secondary text-xs px-3 py-2" onClick={load} disabled={busy}>
              {t('common.refresh')}
            </button>
          </div>
        </div>
        {pageStatus && <p className="mt-4 text-sm text-brass">{pageStatus}</p>}
      </div>

      <div className="grid gap-4 xl:grid-cols-4">
        <MetricTile
          label={t('feeCenter.tiles.automation')}
          value={status?.running ? t('feeCenter.running') : enabled ? t('feeCenter.enabledIdle') : t('common.disabled')}
          sublabel={`${formatMode(operationMode)} / ${profile}`}
          detail={`${t('lightningOps.autofeeLastRun')}: ${formatDate(status?.last_run_at)}`}
          tone={runTone}
          badgeLabel={status?.last_error ? t('feeCenter.error') : t('common.ok')}
        />
        <MetricTile
          label={t('feeCenter.tiles.lastRunImpact')}
          value={formatInt(changedTotal)}
          sublabel={`${t('feeCenter.impact.up')}: ${formatInt(latestSummary?.up)} | ${t('feeCenter.impact.down')}: ${formatInt(latestSummary?.down)}`}
          detail={`${t('feeCenter.impact.cooldown')}: ${formatInt(latestSummary?.cooldown)} | ${t('feeCenter.impact.errors')}: ${formatInt(latestSummary?.error_count)}`}
          tone={changedTotal > 0 ? 'info' : 'muted'}
          trend={runTrend}
        />
        <MetricTile
          label={t('feeCenter.tiles.policyRange')}
          value={formatPpm(avgPolicyPpm)}
          sublabel={`${t('feeCenter.policy.median')}: ${formatPpm(medianPolicyPpm)}`}
          detail={`${t('feeCenter.policy.maxSeen')}: ${formatPpm(maxPolicySeen)} | ${t('lightningOps.autofeeMinPpm')}: ${formatPpm(Number(minPpm || 0))}`}
          tone="info"
        >
          <HorizontalBarGauge
            label={t('feeCenter.policy.averageWithinMax')}
            value={avgPolicyPpm}
            max={Math.max(1, Number(maxPpm || 2000))}
            valueLabel={`${Math.round((avgPolicyPpm / Math.max(1, Number(maxPpm || 2000))) * 100)}%`}
            tone="info"
          />
        </MetricTile>
        <MetricTile
          label={t('feeCenter.tiles.seedCoverage')}
          value={`${seedCoveragePct}%`}
          sublabel={`${t('feeCenter.seed.known')}: ${formatInt(seedKnown)} / ${formatInt(seedTotal)}`}
          detail={`${t('lightningOps.autofeeResultsSeedMissing')}: ${formatInt(latestSeed?.missing)} | ${t('lightningOps.autofeeResultsSeedDefault')}: ${formatInt(latestSeed?.default)}`}
          tone={seedCoveragePct >= 75 ? 'ok' : seedCoveragePct >= 40 ? 'warn' : 'muted'}
        >
          <StackedRatioBar
            compact
            segments={[
              { label: t('feeCenter.seed.native'), value: numberOrZero(latestSeed?.native), tone: 'ok' },
              { label: t('feeCenter.seed.amboss'), value: numberOrZero(latestSeed?.amboss), tone: 'info' },
              { label: t('feeCenter.seed.local'), value: numberOrZero(latestSeed?.outrate) + numberOrZero(latestSeed?.mem), tone: 'warn' },
              { label: t('feeCenter.seed.fallback'), value: numberOrZero(latestSeed?.default) + numberOrZero(latestSeed?.missing), tone: 'muted' },
            ]}
          />
        </MetricTile>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <MetricTile
          label={t('feeCenter.tiles.policyCoverage')}
          value={`${formatInt(activePolicyCount)} / ${formatInt(policyRows.length)}`}
          sublabel={`${t('common.enabled')}: ${formatInt(activePolicyCount)} | ${t('common.disabled')}: ${formatInt(inactivePolicyCount)}`}
          tone={inactivePolicyCount > 0 ? 'warn' : 'ok'}
        >
          <StackedRatioBar
            compact
            segments={[
              { label: t('common.enabled'), value: activePolicyCount, tone: 'ok' },
              { label: t('common.disabled'), value: inactivePolicyCount, tone: 'muted' },
            ]}
          />
        </MetricTile>
        <MetricTile
          label={t('feeCenter.tiles.feePressure')}
          value={formatInt(pressureTags.reduce((sum, [, count]) => sum + count, 0))}
          sublabel={pressureTags.length ? pressureTags.map(([tag, count]) => `${tag}: ${count}`).join(' | ') : t('feeCenter.pressure.none')}
          tone={pressureTags.length ? 'warn' : 'muted'}
        />
        <MetricTile
          label={t('feeCenter.tiles.economics7d')}
          value={formatSats(economics.profit)}
          sublabel={`${t('feeCenter.economics.forward')}: ${formatSats(economics.forward)}`}
          detail={`${t('feeCenter.economics.rebalance')}: ${formatSats(economics.rebal)} | ${t('feeCenter.policy.targetAligned')}: ${formatInt(targetAligned)}`}
          tone={economics.profit >= 0 ? 'ok' : 'danger'}
        />
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('feeCenter.impact.title')}</h3>
            <p className="text-sm text-fog/60">
              {latestRun?.header
                ? `${formatReason(latestRun.header.reason)} | ${formatMode(latestRun.header.operation_mode)} | ${formatDate(latestRun.header.timestamp)}${latestRun.header.dry_run ? ` | ${t('lightningOps.autofeeResultsDryRunTag').trim()}` : ''}`
                : t('feeCenter.impact.empty')}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge label={`${t('feeCenter.impact.up')}: ${feeUp.length}`} tone="ok" />
            <StatusBadge label={`${t('feeCenter.impact.down')}: ${feeDown.length}`} tone="warn" />
            <StatusBadge label={`${t('feeCenter.impact.special')}: ${specialChanges.length}`} tone="info" />
          </div>
        </div>
        <div className="grid gap-4 xl:grid-cols-3">
          {renderImpactLane(t('feeCenter.impact.upLane'), feeUp, 'ok')}
          {renderImpactLane(t('feeCenter.impact.downLane'), feeDown, 'warn')}
          {renderImpactLane(t('feeCenter.impact.specialLane'), specialChanges, 'info')}
        </div>
      </div>

      <div className="grid gap-6 xl:grid-cols-[1.1fr_0.9fr]">
        <div className="section-card space-y-4">
          <div>
            <h3 className="text-lg font-semibold">{t('feeCenter.controls.title')}</h3>
            <p className="text-sm text-fog/60">{t('feeCenter.controls.subtitle')}</p>
          </div>
          <div className="grid gap-4 lg:grid-cols-3">
            {renderToggle(t('lightningOps.autofeeEnabled'), enabled, setEnabled, !config || busy)}
            <label className="text-sm text-fog/70">
              {t('lightningOps.autofeeOperationMode')}
              <select className="input-field mt-2" value={operationMode} onChange={(event) => setOperationMode(event.target.value)}>
                <option value="balanced">{t('lightningOps.autofeeOperationModeBalanced')}</option>
                <option value="market_refill">{t('lightningOps.autofeeOperationModeMarketRefill')}</option>
              </select>
            </label>
            <label className="text-sm text-fog/70">
              {t('lightningOps.autofeeProfile')}
              <select
                className="input-field mt-2"
                value={profile}
                onChange={(event) => {
                  const value = event.target.value
                  setProfile(value)
                  const defaults = config?.profile_defaults?.[value]
                  if (defaults) {
                    setIntervalHours(String(Math.max(1, Math.round((defaults.run_interval_sec || 14400) / 3600))))
                    setCooldownUp(String(Math.max(1, Math.round((defaults.cooldown_up_sec || 10800) / 3600))))
                    setCooldownDown(String(Math.max(1, Math.round((defaults.cooldown_down_sec || 14400) / 3600))))
                  }
                }}
              >
                <option value="conservative">{t('lightningOps.autofeeProfileConservative')}</option>
                <option value="moderate">{t('lightningOps.autofeeProfileModerate')}</option>
                <option value="aggressive">{t('lightningOps.autofeeProfileAggressive')}</option>
              </select>
            </label>
            <label className="text-sm text-fog/70">
              {t('lightningOps.autofeeLookback')}
              <input className="input-field mt-2" type="number" min={5} max={21} value={lookback} onChange={(event) => setLookback(event.target.value)} />
            </label>
            <label className="text-sm text-fog/70">
              {t('lightningOps.autofeeInterval')}
              <input className="input-field mt-2" type="number" min={1} max={24} value={intervalHours} onChange={(event) => setIntervalHours(event.target.value)} />
            </label>
            <label className="text-sm text-fog/70">
              {t('lightningOps.autofeeRebalCostMode')}
              <select className="input-field mt-2" value={rebalMode} onChange={(event) => setRebalMode(event.target.value)}>
                <option value="blend">{t('lightningOps.autofeeRebalCostModeBlend')}</option>
                <option value="channel">{t('lightningOps.autofeeRebalCostModeChannel')}</option>
                <option value="global">{t('lightningOps.autofeeRebalCostModeGlobal')}</option>
              </select>
            </label>
            <label className="text-sm text-fog/70">
              {t('lightningOps.autofeeMinPpm')}
              <input className="input-field mt-2" type="number" min={0} value={minPpm} onChange={(event) => setMinPpm(event.target.value)} />
            </label>
            <label className="text-sm text-fog/70">
              {t('lightningOps.autofeeMaxPpm')}
              <input className="input-field mt-2" type="number" min={1} value={maxPpm} onChange={(event) => setMaxPpm(event.target.value)} />
            </label>
            <label className="text-sm text-fog/70">
              {t('lightningOps.autofeeSuperSourceBaseFee')}
              <input className="input-field mt-2" type="number" min={0} value={superSourceBaseFee} onChange={(event) => setSuperSourceBaseFee(event.target.value)} disabled={!superSource} />
            </label>
          </div>

          <div className="grid gap-4 lg:grid-cols-3">
            <div className="rounded-3xl border border-white/10 bg-black/10 p-4 space-y-3">
              <p className="text-sm font-medium text-fog">{t('lightningOps.autofeeControlsModesTitle')}</p>
              {renderToggle(t('lightningOps.autofeeInboundPassive'), inboundPassive, setInboundPassive)}
              {renderToggle(t('lightningOps.autofeeDiscovery'), discovery, setDiscovery)}
              {renderToggle(t('lightningOps.autofeeExplorer'), explorer, setExplorer)}
              {renderToggle(t('lightningOps.autofeeIdleRefresh'), idleRefresh, setIdleRefresh)}
              {renderToggle(t('lightningOps.autofeeSuperSource'), superSource, setSuperSource)}
            </div>
            <div className="rounded-3xl border border-white/10 bg-black/10 p-4 space-y-3">
              <p className="text-sm font-medium text-fog">{t('lightningOps.autofeeControlsProtectionTitle')}</p>
              {renderToggle(t('lightningOps.autofeeRevfloor'), revfloor, setRevfloor)}
              {renderToggle(t('lightningOps.autofeeCircuitBreaker'), circuitBreaker, setCircuitBreaker)}
              {renderToggle(t('lightningOps.autofeeExtremeDrain'), extremeDrain, setExtremeDrain)}
            </div>
            <div className="rounded-3xl border border-white/10 bg-black/10 p-4 space-y-3">
              <p className="text-sm font-medium text-fog">{t('lightningOps.autofeeControlsSignalsTitle')}</p>
              {renderToggle(t('lightningOps.autofeeHtlcSignalEnabled'), htlcSignalEnabled, setHtlcSignalEnabled)}
              <label className="block text-sm text-fog/70">
                {t('lightningOps.autofeeHtlcMode')}
                <select className="input-field mt-2" value={htlcMode} onChange={(event) => setHtlcMode(event.target.value)} disabled={!htlcSignalEnabled}>
                  <option value="observe_only">{t('lightningOps.autofeeHtlcModeObserveOnly')}</option>
                  <option value="policy_only">{t('lightningOps.autofeeHtlcModePolicyOnly')}</option>
                  <option value="full">{t('lightningOps.autofeeHtlcModeFull')}</option>
                </select>
              </label>
            </div>
          </div>

          <div className="rounded-3xl border border-white/10 bg-black/10 p-4">
            <button
              className="flex w-full items-center justify-between gap-3 text-left"
              type="button"
              onClick={() => setAdvancedOpen((open) => !open)}
            >
              <span>
                <span className="block text-sm font-medium text-fog">{t('lightningOps.autofeeMovementSettingsTitle')}</span>
                <span className="mt-1 block text-xs text-fog/60">{t('lightningOps.autofeeMovementSettingsSubtitle')}</span>
              </span>
              <span className="btn-secondary text-xs px-3 py-2">{advancedOpen ? t('common.hide') : t('lightningOps.autofeeMovementSettingsButton')}</span>
            </button>
            {advancedOpen && (
              <div className="mt-4 grid gap-4 lg:grid-cols-3">
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeCooldownUp')}<input className="input-field mt-2" type="number" min={1} max={12} value={cooldownUp} onChange={(event) => setCooldownUp(event.target.value)} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: activeDefaults ? `${Math.max(1, Math.round((activeDefaults.cooldown_up_sec || 10800) / 3600))}h` : '-' })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeCooldownDown')}<input className="input-field mt-2" type="number" min={1} max={24} value={cooldownDown} onChange={(event) => setCooldownDown(event.target.value)} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: activeDefaults ? `${Math.max(1, Math.round((activeDefaults.cooldown_down_sec || 14400) / 3600))}h` : '-' })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeStepCapOverride')}<input className="input-field mt-2" type="number" min={0} max={30} step="0.1" value={stepCapOverride} onChange={(event) => setStepCapOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.step_cap) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeDiscoveryStepCapDownOverride')}<input className="input-field mt-2" type="number" min={0} max={40} step="0.1" value={discoveryStepCapDownOverride} onChange={(event) => setDiscoveryStepCapDownOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.discovery_step_cap_down) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeStallFloorRelaxGapFracOverride')}<input className="input-field mt-2" type="number" min={0} max={80} step="1" value={stallFloorRelaxGapFracOverride} onChange={(event) => setStallFloorRelaxGapFracOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.stall_floor_relax_gap_frac) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeInboundDiscountMaxRatioOverride')}<input className="input-field mt-2" type="number" min={0} max={100} step="1" value={inboundDiscountMaxRatioOverride} onChange={(event) => setInboundDiscountMaxRatioOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.inbound_discount_max_ratio) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeInboundDiscountReachOutRatioOverride')}<input className="input-field mt-2" type="number" min={0} max={50} step="1" value={inboundDiscountReachOutRatioOverride} onChange={(event) => setInboundDiscountReachOutRatioOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.inbound_discount_reach_out_ratio) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeInboundDiscountMinRetainedSpreadFracOverride')}<input className="input-field mt-2" type="number" min={0} max={50} step="1" value={inboundDiscountMinRetainedSpreadFracOverride} onChange={(event) => setInboundDiscountMinRetainedSpreadFracOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.inbound_discount_min_retained_spread_frac) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeOutrateFloorFactorLowOverride')}<input className="input-field mt-2" type="number" min={0} max={100} step="1" value={outrateFloorFactorLowOverride} onChange={(event) => setOutrateFloorFactorLowOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.outrate_floor_factor_low) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeSoftenMinOutRatioOverride')}<input className="input-field mt-2" type="number" min={0} max={95} step="1" value={softenMinOutRatioOverride} onChange={(event) => setSoftenMinOutRatioOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.soften_min_out_ratio) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeSoftenMaxDropToPegFracOverride')}<input className="input-field mt-2" type="number" min={0} max={100} step="1" value={softenMaxDropToPegFracOverride} onChange={(event) => setSoftenMaxDropToPegFracOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.soften_max_drop_to_peg_frac) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeHtlcMinAttemptsOverride')}<input className="input-field mt-2" type="number" min={0} max={100} step="1" value={htlcMinAttemptsOverride} onChange={(event) => setHtlcMinAttemptsOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: activeDefaults?.htlc_min_attempts_60m ?? '-' })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeHtlcPolicyFailRateOverride')}<input className="input-field mt-2" type="number" min={0} max={90} step="0.1" value={htlcPolicyFailRateOverride} onChange={(event) => setHtlcPolicyFailRateOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.htlc_policy_fail_rate) })}</span></label>
                <label className="text-sm text-fog/70">{t('lightningOps.autofeeHtlcLiquidityFailRateOverride')}<input className="input-field mt-2" type="number" min={0} max={90} step="0.1" value={htlcLiquidityFailRateOverride} onChange={(event) => setHtlcLiquidityFailRateOverride(event.target.value)} placeholder={t('lightningOps.autofeeMovementSettingsAuto')} /><span className="mt-1 block text-[11px] text-fog/55">{t('lightningOps.autofeeMovementDefaultLabel', { value: defaultText(activeDefaults?.htlc_liquidity_fail_rate) })}</span></label>
              </div>
            )}
          </div>
        </div>

        <div className="section-card space-y-4">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.autofeeSeedSourcesTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.autofeeSeedSourcesSubtitle')}</p>
          </div>
          <div className="grid gap-4">
            <div className="rounded-3xl border border-white/10 bg-black/10 p-4">
              {renderToggle(t('lightningOps.autofeeNativeSeed'), nativeSeedEnabled, setNativeSeedEnabled)}
              <p className="mt-3 text-xs leading-5 text-fog/55">{t('lightningOps.autofeeNativeSeedHint')}</p>
            </div>
            <div className="rounded-3xl border border-white/10 bg-black/10 p-4">
              {renderToggle(t('lightningOps.autofeeAmboss'), ambossEnabled, setAmbossEnabled)}
              <p className="mt-3 text-xs leading-5 text-fog/55">{t('lightningOps.autofeeAmbossHint')}</p>
              {ambossEnabled && (
                <label className="mt-4 block text-sm text-fog/70">
                  {t('lightningOps.autofeeAmbossToken')}
                  <input className="input-field mt-2" type="password" value={ambossToken} onChange={(event) => setAmbossToken(event.target.value)} placeholder={config?.amboss_token_set ? t('lightningOps.autofeeAmbossTokenSet') : ''} />
                </label>
              )}
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <button className="btn-primary" onClick={handleSave} disabled={busy || !config}>{t('common.save')}</button>
            <button className="btn-secondary" onClick={() => handleRefresh(true)} disabled={busy}>{t('lightningOps.autofeeDryRefresh')}</button>
            <button className="btn-secondary" onClick={() => handleRefresh(false)} disabled={busy}>{t('lightningOps.autofeeRefresh')}</button>
            <button className="btn-secondary" onClick={() => handleRun(true)} disabled={busy}>{t('lightningOps.autofeeDryRun')}</button>
            <button className="btn-secondary" onClick={() => handleRun(false)} disabled={busy}>{t('lightningOps.autofeeRunNow')}</button>
          </div>
          {renderToggle(t('lightningOps.autofeeRefreshIncludeInbound'), refreshIncludeInbound, setRefreshIncludeInbound)}
          <div className="text-xs text-fog/60">
            <div>{t('lightningOps.autofeeLastRun')}: <span className="text-fog">{formatDate(status?.last_run_at)}</span></div>
            <div>{t('lightningOps.autofeeNextRun')}: <span className="text-fog">{formatDate(status?.next_run_at)}</span></div>
            {status?.last_error && <div>{t('lightningOps.autofeeLastError')}: <span className="text-ember">{status.last_error}</span></div>}
          </div>
        </div>
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('feeCenter.policies.title')}</h3>
            <p className="text-sm text-fog/60">{t('feeCenter.policies.subtitle')}</p>
          </div>
          <StatusBadge label={t('feeCenter.policies.count', { count: policyRows.length })} tone="info" />
        </div>
        <div className="max-h-[560px] overflow-x-auto overflow-y-auto pr-1">
          <table className="w-full min-w-[1040px] text-sm text-fog/75">
            <thead className="sticky top-0 bg-ink/95 backdrop-blur">
              <tr className="text-left">
                <th className="pb-3 pr-4">{t('feeCenter.policies.peer')}</th>
                <th className="pb-3 pr-4">{t('feeCenter.policies.policy')}</th>
                <th className="pb-3 pr-4">{t('feeCenter.policies.lastMove')}</th>
                <th className="pb-3 pr-4">{t('feeCenter.policies.signals')}</th>
                <th className="pb-3">{t('feeCenter.policies.links')}</th>
              </tr>
            </thead>
            <tbody>
              {policyRows.map((row) => {
                const point = row.channel.channel_point
                const last = row.last
                return (
                  <tr key={point || String(row.channel.channel_id)} id={point ? channelPolicyDomID(point) : undefined} className={`border-t border-white/5 align-top ${focusedChannelPoint === point ? 'bg-sky-500/20' : ''}`}>
                    <td className="py-3 pr-4">
                      <div className="text-fog">{row.channel.peer_alias || row.channel.remote_pubkey || t('common.unknown')}</div>
                      <div className="mt-1 text-xs text-fog/50 break-all">{point}</div>
                      <div className="mt-1 flex flex-wrap gap-2">
                        <StatusBadge label={row.enabled ? t('common.enabled') : t('common.disabled')} tone={row.enabled ? 'ok' : 'muted'} />
                        <StatusBadge label={row.channel.active ? t('common.active') : t('common.inactive')} tone={row.channel.active ? 'ok' : 'warn'} />
                      </div>
                    </td>
                    <td className="py-3 pr-4 text-xs">
                      <div>{t('lightningOps.outRate')}: <span className="text-fog">{formatPpm(row.channel.fee_rate_ppm)}</span></div>
                      <div className="mt-1">{t('lightningOps.inRate')}: <span className="text-fog">{formatPpm(row.channel.inbound_fee_rate_ppm)}</span></div>
                      <div className="mt-1">{t('lightningOps.peerRate')}: <span className="text-fog">{formatPpm(row.channel.peer_fee_rate_ppm)}</span></div>
                    </td>
                    <td className="py-3 pr-4 text-xs">
                      {last ? (
                        <>
                          <div><span className="text-fog">{formatPpm(last.local_ppm)}</span> -&gt; <span className="text-fog">{formatPpm(last.new_ppm)}</span></div>
                          <div className="mt-1 text-fog/55">{t('feeCenter.policy.target')}: {formatPpm(last.target_final ?? last.target)}</div>
                          <div className="mt-1 text-fog/55">{t('feeCenter.policy.floor')}: {formatPpm(last.floor)}</div>
                        </>
                      ) : (
                        <span className="text-fog/50">{t('feeCenter.policies.noMove')}</span>
                      )}
                    </td>
                    <td className="py-3 pr-4 text-xs">
                      <div>{t('lightningOps.outPpm7d')}: <span className="text-fog">{formatPpm(row.channel.out_ppm7d)}</span></div>
                      <div className="mt-1">{t('lightningOps.rebalPpm7d')}: <span className="text-fog">{formatPpm(row.channel.rebal_ppm7d)}</span></div>
                      <div className={`mt-1 ${numberOrZero(row.channel.profit_fee_7d_sat) >= 0 ? 'text-emerald-200' : 'text-rose-200'}`}>{t('lightningOps.profit7d')}: {formatSats(row.channel.profit_fee_7d_sat)}</div>
                      {last?.tags?.length ? <div className="mt-2 text-fog/50">{last.tags.slice(0, 4).join(' | ')}</div> : null}
                    </td>
                    <td className="py-3 text-xs">
                      {point ? (
                        <div className="flex flex-wrap gap-2">
                          <a className="text-sky-200 hover:text-sky-100" href={buildHashWithChannelPoint(LIGHTNING_OPS_ROUTE_KEY, point)}>{t('feeCenter.links.lightningOps')}</a>
                          <a className="text-sky-200 hover:text-sky-100" href={buildHashWithChannelPoint(REBALANCE_ROUTE_KEY, point)}>{t('feeCenter.links.rebalance')}</a>
                        </div>
                      ) : '-'}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('feeCenter.runLog.title')}</h3>
            <p className="text-sm text-fog/60">{t('feeCenter.runLog.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <button className="btn-secondary text-xs px-3 py-2" onClick={() => setLogOpen((open) => !open)}>{logOpen ? t('common.hide') : t('lightningOps.autofeeResultsShow')}</button>
            <button className="btn-secondary text-xs px-3 py-2" onClick={() => { void refreshResults() }}>{t('lightningOps.autofeeResultsRefresh')}</button>
          </div>
        </div>
        {logOpen && (
          <>
            <div className="grid gap-3 lg:grid-cols-3">
              <label className="text-sm text-fog/70">{t('lightningOps.autofeeResultsRuns')}<input className="input-field mt-2" type="number" min={1} max={50} value={resultsRuns} onChange={(event) => setResultsRuns(event.target.value)} /></label>
              <label className="text-sm text-fog/70">{t('lightningOps.autofeeResultsFrom')}<input className="input-field mt-2" type="datetime-local" value={resultsFrom} onChange={(event) => setResultsFrom(event.target.value)} /></label>
              <label className="text-sm text-fog/70">{t('lightningOps.autofeeResultsTo')}<input className="input-field mt-2" type="datetime-local" value={resultsTo} onChange={(event) => setResultsTo(event.target.value)} /></label>
            </div>
            <div className="bg-ink/70 border border-white/10 rounded-2xl p-4 text-xs font-mono whitespace-pre-wrap max-h-[420px] overflow-y-auto">
              {resultLines.length ? resultLines.join('\n') : t('lightningOps.autofeeResultsEmpty')}
            </div>
          </>
        )}
      </div>
    </section>
  )
}
