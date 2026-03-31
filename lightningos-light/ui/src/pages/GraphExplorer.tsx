import { useDeferredValue, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  APIError,
  getGraphExplorerNodeChannels,
  getGraphExplorerNodeClosed,
  getGraphExplorerNodeFees,
  getGraphExplorerNodeGeneral,
  getGraphExplorerStatus,
  getLndStatus,
  recomputeGraphExplorer,
  searchGraphExplorerNodes
} from '../api'
import { getLocale } from '../i18n'
import clsx from '../utils/clsx'

type GraphExplorerTab = 'general' | 'channels' | 'closed' | 'fees'
type GraphExplorerClosedRange = '30d' | '90d' | '1y' | 'all'
type GraphExplorerFeeRange = '7d' | '30d' | '90d' | '1y' | 'all'

type GraphExplorerStatus = {
  available?: boolean
  running?: boolean
  first_native_coverage_at?: string
  last_sync_at?: string
  last_error?: string
  node_count?: number
  open_channel_count?: number
  closed_channel_count?: number
}

type GraphExplorerSearchResult = {
  pubkey: string
  alias?: string
  color?: string
  channel_count: number
  total_capacity_sat: number
  last_seen_at?: string
}

type GraphExplorerSearchResponse = {
  query?: string
  coverage_since?: string
  items?: GraphExplorerSearchResult[]
}

type GraphExplorerNodeAddress = {
  network?: string
  addr?: string
}

type GraphExplorerNodeProfile = {
  pubkey: string
  alias?: string
  color?: string
  addresses?: GraphExplorerNodeAddress[]
  address_count: number
  clearnet_address_count: number
  onion_address_count: number
  channel_count: number
  open_channel_count: number
  peer_count: number
  total_capacity_sat: number
  smallest_channel_sat: number
  largest_channel_sat: number
  average_channel_size_sat: number
  oldest_channel_block: number
  youngest_channel_block: number
  first_seen_at?: string
  last_seen_at?: string
  last_graph_update_at?: string
  last_policy_update_at?: string
}

type GraphExplorerNodeGeneral = {
  coverage_since?: string
  source?: string
  node?: GraphExplorerNodeProfile
}

type GraphExplorerPolicy = {
  fee_base_msat: number
  fee_rate_ppm: number
  inbound_base_msat: number
  inbound_fee_rate_ppm: number
  disabled: boolean
  last_update_at?: string
}

type GraphExplorerNodeChannel = {
  channel_id: number
  chan_point?: string
  peer_pubkey: string
  peer_alias?: string
  capacity_sat: number
  open_block_height: number
  target_policy: GraphExplorerPolicy
  peer_policy: GraphExplorerPolicy
  last_policy_update?: string
}

type GraphExplorerNodeChannelsResponse = {
  coverage_since?: string
  items?: GraphExplorerNodeChannel[]
}

type GraphExplorerClosedSummary = {
  total_closed_channels: number
  total_capacity_sat: number
  known_type_count: number
  unknown_type_count: number
}

type GraphExplorerClosedChannel = {
  channel_id: number
  chan_point?: string
  peer_pubkey?: string
  peer_alias?: string
  capacity_sat: number
  closed_height: number
  observed_at?: string
  close_type?: string
  close_source?: string
}

type GraphExplorerClosedResponse = {
  coverage_since?: string
  range?: string
  summary?: GraphExplorerClosedSummary
  items?: GraphExplorerClosedChannel[]
}

type GraphExplorerFeeSummary = {
  channel_count: number
  disabled_count: number
  min_ppm: number
  max_ppm: number
  avg_ppm: number
  median_ppm: number
  weighted_avg_ppm: number
  total_capacity_sat: number
  last_policy_update_at?: string
}

type GraphExplorerFeeBin = {
  label: string
  min_ppm_inclusive: number
  max_ppm_inclusive: number
  channel_count: number
  capacity_sat: number
}

type GraphExplorerFeeHistoryPoint = {
  day: string
  outbound_avg_ppm: number
  outbound_weighted_avg_ppm: number
  outbound_sample_count: number
  inbound_avg_ppm: number
  inbound_weighted_avg_ppm: number
  inbound_sample_count: number
}

type GraphExplorerFeeResponse = {
  coverage_since?: string
  range?: string
  outbound?: GraphExplorerFeeSummary
  inbound?: GraphExplorerFeeSummary
  outbound_bins?: GraphExplorerFeeBin[]
  inbound_bins?: GraphExplorerFeeBin[]
  history?: GraphExplorerFeeHistoryPoint[]
}

type LndStatusResponse = {
  block_height?: number
}

type ChannelSortKey = 'peer' | 'channel' | 'capacity' | 'openBlock' | 'targetPolicy' | 'peerPolicy' | 'lastUpdate'
type SortDirection = 'asc' | 'desc'
type FeeTrendDirection = 'up' | 'down' | 'flat'
type FeeTrendResult = {
  direction: FeeTrendDirection
  deltaPct: number
  available: boolean
}

const FEE_TREND_BASELINE_MIN_DAYS = 3
const FEE_TREND_BASELINE_WINDOW_DAYS = 7
const FEE_TREND_BASELINE_MIN_PPM = 25

const GRAPH_EXPLORER_ROUTE_KEY = 'graph-explorer'
const GRAPH_EXPLORER_PUBKEY_PARAM = 'pubkey'
const GRAPH_EXPLORER_TAB_PARAM = 'tab'

const normalizeGraphExplorerTab = (value?: string): GraphExplorerTab => {
  switch (String(value || '').trim()) {
    case 'channels':
      return 'channels'
    case 'closed':
      return 'closed'
    case 'fees':
      return 'fees'
    default:
      return 'general'
  }
}

const readGraphExplorerRouteState = () => {
  if (typeof window === 'undefined') {
    return { pubkey: '', tab: 'general' as GraphExplorerTab }
  }
  const rawHash = window.location.hash.startsWith('#')
    ? window.location.hash.slice(1)
    : window.location.hash
  if (!rawHash) {
    return { pubkey: '', tab: 'general' as GraphExplorerTab }
  }
  const queryIndex = rawHash.indexOf('?')
  const routeKey = queryIndex >= 0 ? rawHash.slice(0, queryIndex) : rawHash
  if (routeKey !== GRAPH_EXPLORER_ROUTE_KEY) {
    return { pubkey: '', tab: 'general' as GraphExplorerTab }
  }
  const params = new URLSearchParams(queryIndex >= 0 ? rawHash.slice(queryIndex + 1) : '')
  return {
    pubkey: (params.get(GRAPH_EXPLORER_PUBKEY_PARAM) || '').trim(),
    tab: normalizeGraphExplorerTab(params.get(GRAPH_EXPLORER_TAB_PARAM) || '')
  }
}

const buildGraphExplorerHash = (pubkey: string, tab: GraphExplorerTab) => {
  const trimmedPubkey = String(pubkey || '').trim()
  if (!trimmedPubkey) return `#${GRAPH_EXPLORER_ROUTE_KEY}`
  const params = new URLSearchParams()
  params.set(GRAPH_EXPLORER_PUBKEY_PARAM, trimmedPubkey)
  if (tab !== 'general') {
    params.set(GRAPH_EXPLORER_TAB_PARAM, tab)
  }
  return `#${GRAPH_EXPLORER_ROUTE_KEY}?${params.toString()}`
}

const shortPubkey = (value: string) => {
  const trimmed = String(value || '').trim()
  if (trimmed.length <= 18) return trimmed
  return `${trimmed.slice(0, 9)}...${trimmed.slice(-6)}`
}

const normalizeNodeColor = (value?: string) => {
  const trimmed = String(value || '').trim()
  return /^#[0-9a-fA-F]{6}$/.test(trimmed) ? trimmed : '#7dd3fc'
}

const tabButtonClass = (active: boolean) =>
  active
    ? 'rounded-full border border-sky-400/30 bg-sky-500/12 px-4 py-2 text-sm font-medium text-sky-100'
    : 'rounded-full border border-white/10 bg-white/[0.03] px-4 py-2 text-sm text-fog/60 transition hover:border-white/20 hover:text-fog'

const rangeButtonClass = (active: boolean) =>
  active
    ? 'rounded-full border border-sky-400/30 bg-sky-500/12 px-3 py-1.5 text-xs font-medium text-sky-100'
    : 'rounded-full border border-white/10 bg-white/[0.03] px-3 py-1.5 text-xs text-fog/60 transition hover:border-white/20 hover:text-fog'

const closeTypeTone = (value?: string) => {
  switch (String(value || '').trim().toLowerCase()) {
    case 'force_close':
    case 'force':
      return 'border-rose-400/30 bg-rose-500/12 text-rose-100'
    case 'cooperative':
    case 'mutual':
    case 'mutual_close':
      return 'border-emerald-400/30 bg-emerald-500/12 text-emerald-100'
    default:
      return 'border-white/10 bg-white/[0.04] text-fog/70'
  }
}

const feeTrendTone = (direction: FeeTrendDirection) => {
  switch (direction) {
    case 'up':
      return 'border-emerald-400/30 bg-emerald-500/10 text-emerald-100'
    case 'down':
      return 'border-rose-400/30 bg-rose-500/12 text-rose-100'
    default:
      return 'border-white/10 bg-white/[0.04] text-fog/70'
  }
}

const sortIndicator = (active: boolean, direction: SortDirection) => {
  if (!active) return '-'
  return direction === 'asc' ? '^' : 'v'
}

function PolicyCell({
  policy,
  formatInteger,
  t
}: {
  policy: GraphExplorerPolicy
  formatInteger: (value?: number) => string
  t: (key: string, options?: any) => string
}) {
  return (
    <div className="space-y-1">
      <div className="font-medium text-fog">{formatInteger(policy.fee_rate_ppm)} ppm</div>
      <div className="text-xs text-fog/65">{formatInteger(policy.fee_base_msat)} msat</div>
      <div className="text-xs text-fog/55">
        {t('graphExplorer.policyInboundLabel', { ppm: formatInteger(policy.inbound_fee_rate_ppm) })}
      </div>
      <span
        className={clsx(
          'inline-flex rounded-full border px-2 py-0.5 text-[11px] font-medium',
          policy.disabled
            ? 'border-amber-400/25 bg-amber-500/12 text-amber-100'
            : 'border-emerald-400/25 bg-emerald-500/10 text-emerald-100'
        )}
      >
        {policy.disabled ? t('common.disabled') : t('common.enabled')}
      </span>
    </div>
  )
}

function FeeDistributionPanel({
  title,
  bins,
  formatInteger,
  formatSats,
  t
}: {
  title: string
  bins: GraphExplorerFeeBin[]
  formatInteger: (value?: number) => string
  formatSats: (value?: number) => string
  t: (key: string, options?: any) => string
}) {
  const maxCount = bins.reduce((current, item) => Math.max(current, item.channel_count), 0)
  return (
    <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
      <div className="flex items-center justify-between gap-3">
        <p className="text-sm font-medium text-fog">{title}</p>
        <span className="text-xs text-fog/55">{t('graphExplorer.feeDistributionHint')}</span>
      </div>
      <div className="mt-4 space-y-3">
        {bins.map((item) => (
          <div key={item.label} className="space-y-1.5">
            <div className="flex items-center justify-between gap-3 text-xs text-fog/65">
              <span>{item.label} ppm</span>
              <span>{t('graphExplorer.feeDistributionCount', { count: formatInteger(item.channel_count) })}</span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-white/[0.06]">
              <div
                className="h-full rounded-full bg-[linear-gradient(90deg,rgba(56,189,248,0.35),rgba(14,165,233,0.9))]"
                style={{ width: `${maxCount > 0 ? (item.channel_count / maxCount) * 100 : 0}%` }}
              />
            </div>
            <div className="text-[11px] text-fog/55">{formatSats(item.capacity_sat)}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

function FeeSummaryCard({
  title,
  summary,
  formatInteger,
  formatSats,
  formatTimestamp,
  t
}: {
  title: string
  summary: GraphExplorerFeeSummary
  formatInteger: (value?: number) => string
  formatSats: (value?: number) => string
  formatTimestamp: (value?: string) => string
  t: (key: string, options?: any) => string
}) {
  return (
    <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
      <p className="text-sm font-medium text-fog">{title}</p>
      <dl className="mt-4 space-y-3">
        <div className="flex items-center justify-between gap-4">
          <dt className="text-sm text-fog/65">{t('graphExplorer.feeSummary.avg')}</dt>
          <dd className="text-sm font-medium text-fog">{formatInteger(summary.avg_ppm)} ppm</dd>
        </div>
        <div className="flex items-center justify-between gap-4">
          <dt className="text-sm text-fog/65">{t('graphExplorer.feeSummary.weightedAvg')}</dt>
          <dd className="text-sm font-medium text-fog">{formatInteger(summary.weighted_avg_ppm)} ppm</dd>
        </div>
        <div className="flex items-center justify-between gap-4">
          <dt className="text-sm text-fog/65">{t('graphExplorer.feeSummary.median')}</dt>
          <dd className="text-sm font-medium text-fog">{formatInteger(summary.median_ppm)} ppm</dd>
        </div>
        <div className="flex items-center justify-between gap-4">
          <dt className="text-sm text-fog/65">{t('graphExplorer.feeSummary.minMax')}</dt>
          <dd className="text-sm font-medium text-fog">
            {formatInteger(summary.min_ppm)} / {formatInteger(summary.max_ppm)} ppm
          </dd>
        </div>
        <div className="flex items-center justify-between gap-4">
          <dt className="text-sm text-fog/65">{t('graphExplorer.feeSummary.disabled')}</dt>
          <dd className="text-sm font-medium text-fog">
            {formatInteger(summary.disabled_count)} / {formatInteger(summary.channel_count)}
          </dd>
        </div>
        <div className="flex items-center justify-between gap-4">
          <dt className="text-sm text-fog/65">{t('graphExplorer.feeSummary.capacity')}</dt>
          <dd className="text-sm font-medium text-fog">{formatSats(summary.total_capacity_sat)}</dd>
        </div>
        <div className="flex items-center justify-between gap-4">
          <dt className="text-sm text-fog/65">{t('graphExplorer.feeSummary.lastUpdate')}</dt>
          <dd className="text-sm font-medium text-fog">{formatTimestamp(summary.last_policy_update_at)}</dd>
        </div>
      </dl>
    </div>
  )
}

function FeeComparisonCard({
  ratio,
  ratioTrend,
  outboundValue,
  inboundValue,
  outboundTrend,
  inboundTrend,
  formatInteger,
  t
}: {
  ratio: number
  ratioTrend: FeeTrendResult
  outboundValue: number
  inboundValue: number
  outboundTrend: FeeTrendResult
  inboundTrend: FeeTrendResult
  formatInteger: (value?: number) => string
  t: (key: string, options?: any) => string
}) {
  const ratioDisplay = Number.isFinite(ratio) && ratio > 0 ? `${ratio.toFixed(2)}x` : t('common.na')
  const trendLabel = (trend: FeeTrendResult) => {
    if (!trend.available) {
      return t('graphExplorer.feeComparison.insufficientHistory')
    }
    switch (trend.direction) {
      case 'up':
        return t('graphExplorer.feeComparison.rising')
      case 'down':
        return t('graphExplorer.feeComparison.falling')
      default:
        return t('graphExplorer.feeComparison.stable')
    }
  }
  const trendValue = (trend: FeeTrendResult) => {
    if (!trend.available) return ''
    const prefix = trend.direction === 'up' ? '+' : ''
    return `${prefix}${trend.deltaPct.toFixed(1)}%`
  }
  return (
    <div className="rounded-[1.35rem] border border-white/10 bg-[linear-gradient(180deg,rgba(255,255,255,0.04),rgba(255,255,255,0.02))] p-5">
      <p className="text-sm font-medium text-fog">{t('graphExplorer.feeComparison.title')}</p>
      <div className="mt-4 rounded-[1.15rem] border border-white/10 bg-black/15 px-4 py-6 text-center">
        <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.feeComparison.ratio')}</p>
        <p className="mt-3 text-4xl font-semibold text-fog">{ratioDisplay}</p>
        <div className="mt-4 flex items-center justify-center">
          <span className={clsx('inline-flex rounded-full border px-3 py-1 text-xs font-medium', feeTrendTone(ratioTrend.available ? ratioTrend.direction : 'flat'))}>
            {trendLabel(ratioTrend)} {trendValue(ratioTrend) ? ` ${trendValue(ratioTrend)}` : ''}
          </span>
        </div>
      </div>
      <div className="mt-5 space-y-3">
        <div className="flex items-center justify-between gap-3">
          <div>
            <p className="text-sm font-medium text-fog">{t('graphExplorer.feeComparison.outbound')}</p>
            <p className="text-xs text-fog/55">{formatInteger(outboundValue)} ppm</p>
          </div>
          <span className={clsx('inline-flex rounded-full border px-3 py-1 text-xs font-medium', feeTrendTone(outboundTrend.available ? outboundTrend.direction : 'flat'))}>
            {trendLabel(outboundTrend)} {trendValue(outboundTrend) ? ` ${trendValue(outboundTrend)}` : ''}
          </span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <div>
            <p className="text-sm font-medium text-fog">{t('graphExplorer.feeComparison.inbound')}</p>
            <p className="text-xs text-fog/55">{formatInteger(inboundValue)} ppm</p>
          </div>
          <span className={clsx('inline-flex rounded-full border px-3 py-1 text-xs font-medium', feeTrendTone(inboundTrend.available ? inboundTrend.direction : 'flat'))}>
            {trendLabel(inboundTrend)} {trendValue(inboundTrend) ? ` ${trendValue(inboundTrend)}` : ''}
          </span>
        </div>
      </div>
      <p className="mt-5 text-center text-xs text-fog/55">{t('graphExplorer.feeComparison.basedOnWeighted')}</p>
    </div>
  )
}

export default function GraphExplorer() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const dateTimeFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  )
  const relativeTimeFormatter = useMemo(
    () => new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }),
    [locale]
  )

  const initialRouteState = readGraphExplorerRouteState()
  const [status, setStatus] = useState<GraphExplorerStatus | null>(null)
  const [statusError, setStatusError] = useState('')
  const [currentBlockHeight, setCurrentBlockHeight] = useState(0)
  const [query, setQuery] = useState('')
  const [searchResults, setSearchResults] = useState<GraphExplorerSearchResult[]>([])
  const [searchLoading, setSearchLoading] = useState(false)
  const [searchError, setSearchError] = useState('')
  const [copiedKey, setCopiedKey] = useState('')
  const [copyError, setCopyError] = useState('')
  const [selectedPubkey, setSelectedPubkey] = useState(initialRouteState.pubkey)
  const [activeTab, setActiveTab] = useState<GraphExplorerTab>(initialRouteState.tab)
  const [channelSort, setChannelSort] = useState<{ key: ChannelSortKey; direction: SortDirection }>({
    key: 'capacity',
    direction: 'desc'
  })
  const [general, setGeneral] = useState<GraphExplorerNodeGeneral | null>(null)
  const [generalLoading, setGeneralLoading] = useState(false)
  const [generalError, setGeneralError] = useState('')
  const [channels, setChannels] = useState<GraphExplorerNodeChannelsResponse | null>(null)
  const [channelsLoading, setChannelsLoading] = useState(false)
  const [channelsError, setChannelsError] = useState('')
  const [closedRange, setClosedRange] = useState<GraphExplorerClosedRange>('90d')
  const [closed, setClosed] = useState<GraphExplorerClosedResponse | null>(null)
  const [closedLoading, setClosedLoading] = useState(false)
  const [closedError, setClosedError] = useState('')
  const [feeRange, setFeeRange] = useState<GraphExplorerFeeRange>('30d')
  const [fees, setFees] = useState<GraphExplorerFeeResponse | null>(null)
  const [feesLoading, setFeesLoading] = useState(false)
  const [feesError, setFeesError] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const deferredQuery = useDeferredValue(query)

  const formatSats = (value?: number) =>
    `${numberFormatter.format(Math.max(0, Math.round(Number(value || 0))))} sats`

  const formatInteger = (value?: number) =>
    numberFormatter.format(Math.max(0, Math.round(Number(value || 0))))

  const formatTimestamp = (value?: string) => {
    if (!value) return t('common.na')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return value
    return dateTimeFormatter.format(parsed)
  }

  const formatBlock = (value?: number) => {
    const normalized = Math.max(0, Math.round(Number(value || 0)))
    if (!normalized) return t('common.na')
    return numberFormatter.format(normalized)
  }

  const parseTimestamp = (value?: string) => {
    if (!value) return null
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? null : parsed
  }

  const estimateDateFromBlock = (blockHeight?: number) => {
    const normalizedBlock = Math.max(0, Math.round(Number(blockHeight || 0)))
    const normalizedTip = Math.max(0, Math.round(Number(currentBlockHeight || 0)))
    if (!normalizedBlock || !normalizedTip || normalizedBlock > normalizedTip) return null
    const blockDelta = normalizedTip - normalizedBlock
    return new Date(Date.now() - blockDelta * 10 * 60 * 1000)
  }

  const formatEstimatedDate = (value?: Date | null) => {
    if (!value) return t('common.na')
    return `~${dateTimeFormatter.format(value)}`
  }

  const formatApproximateAge = (value?: Date | null) => {
    if (!value) return t('common.na')
    const diffMs = Date.now() - value.getTime()
    if (!Number.isFinite(diffMs) || diffMs < 0) return t('common.na')
    const units: Array<{ unit: Intl.RelativeTimeFormatUnit; ms: number }> = [
      { unit: 'year', ms: 365 * 24 * 60 * 60 * 1000 },
      { unit: 'month', ms: 30 * 24 * 60 * 60 * 1000 },
      { unit: 'week', ms: 7 * 24 * 60 * 60 * 1000 },
      { unit: 'day', ms: 24 * 60 * 60 * 1000 },
      { unit: 'hour', ms: 60 * 60 * 1000 },
      { unit: 'minute', ms: 60 * 1000 }
    ]
    const selectedUnit = units.find((item) => diffMs >= item.ms) || units[units.length - 1]
    const amount = Math.max(1, Math.round(diffMs / selectedUnit.ms))
    return `~${relativeTimeFormatter.format(-amount, selectedUnit.unit)}`
  }

  useEffect(() => {
    if (!copiedKey && !copyError) return
    const timer = window.setTimeout(() => {
      setCopiedKey('')
      setCopyError('')
    }, 2200)
    return () => window.clearTimeout(timer)
  }, [copiedKey, copyError])

  useEffect(() => {
    let active = true

    const loadStatus = async () => {
      try {
        const [graphResult, lndResult] = await Promise.allSettled([
          getGraphExplorerStatus(),
          getLndStatus()
        ])
        if (!active) return
        if (graphResult.status === 'fulfilled') {
          setStatus(graphResult.value as GraphExplorerStatus)
          setStatusError('')
        } else {
          setStatusError((graphResult.reason as any)?.message || t('graphExplorer.statusLoadFailed'))
        }
        if (lndResult.status === 'fulfilled') {
          const lndStatus = lndResult.value as LndStatusResponse
          setCurrentBlockHeight(Math.max(0, Math.round(Number(lndStatus?.block_height || 0))))
        }
      } catch (err: any) {
        if (!active) return
        setStatusError(err?.message || t('graphExplorer.statusLoadFailed'))
      }
    }

    const handleHashChange = () => {
      const next = readGraphExplorerRouteState()
      setSelectedPubkey(next.pubkey)
      setActiveTab(next.tab)
    }

    void loadStatus()
    window.addEventListener('hashchange', handleHashChange)
    const timer = window.setInterval(() => void loadStatus(), 60000)
    return () => {
      active = false
      window.clearInterval(timer)
      window.removeEventListener('hashchange', handleHashChange)
    }
  }, [t])

  useEffect(() => {
    const normalizedQuery = deferredQuery.trim()
    if (normalizedQuery.length < 2) {
      setSearchResults([])
      setSearchError('')
      setSearchLoading(false)
      return
    }

    let active = true
    setSearchLoading(true)
    setSearchError('')

    const timer = window.setTimeout(async () => {
      try {
        const response = await searchGraphExplorerNodes({ q: normalizedQuery, limit: 10 }) as GraphExplorerSearchResponse
        if (!active) return
        setSearchResults(Array.isArray(response?.items) ? response.items : [])
      } catch (err: any) {
        if (!active) return
        setSearchResults([])
        setSearchError(err?.message || t('graphExplorer.searchFailed'))
      } finally {
        if (!active) return
        setSearchLoading(false)
      }
    }, 220)

    return () => {
      active = false
      window.clearTimeout(timer)
    }
  }, [deferredQuery, t])

  useEffect(() => {
    if (!selectedPubkey) {
      setGeneral(null)
      setGeneralError('')
      setGeneralLoading(false)
      setChannels(null)
      setChannelsError('')
      setClosed(null)
      setClosedError('')
      setFees(null)
      setFeesError('')
      return
    }

    let active = true
    setGeneralLoading(true)
    setGeneralError('')

    void getGraphExplorerNodeGeneral(selectedPubkey)
      .then((response) => {
        if (!active) return
        const payload = response as GraphExplorerNodeGeneral
        setGeneral(payload)
        const aliasOrPubkey = String(payload?.node?.alias || payload?.node?.pubkey || selectedPubkey).trim()
        setQuery((current) => (current.trim() ? current : aliasOrPubkey))
      })
      .catch((err: any) => {
        if (!active) return
        setGeneral(null)
        if (err instanceof APIError && err.status === 404) {
          setGeneralError(t('graphExplorer.nodeNotFound'))
          return
        }
        setGeneralError(err?.message || t('graphExplorer.nodeLoadFailed'))
      })
      .finally(() => {
        if (!active) return
        setGeneralLoading(false)
      })

    return () => {
      active = false
    }
  }, [selectedPubkey, t])

  useEffect(() => {
    if (!selectedPubkey || activeTab !== 'channels') return
    let active = true
    setChannelsLoading(true)
    setChannelsError('')
    void getGraphExplorerNodeChannels(selectedPubkey, { limit: 400 })
      .then((response) => {
        if (!active) return
        setChannels(response as GraphExplorerNodeChannelsResponse)
      })
      .catch((err: any) => {
        if (!active) return
        setChannels(null)
        setChannelsError(err?.message || t('graphExplorer.channelsLoadFailed'))
      })
      .finally(() => {
        if (!active) return
        setChannelsLoading(false)
      })
    return () => {
      active = false
    }
  }, [activeTab, selectedPubkey, t])

  useEffect(() => {
    if (!selectedPubkey || activeTab !== 'closed') return
    let active = true
    setClosedLoading(true)
    setClosedError('')
    void getGraphExplorerNodeClosed(selectedPubkey, { range: closedRange, limit: 300 })
      .then((response) => {
        if (!active) return
        setClosed(response as GraphExplorerClosedResponse)
      })
      .catch((err: any) => {
        if (!active) return
        setClosed(null)
        setClosedError(err?.message || t('graphExplorer.closedLoadFailed'))
      })
      .finally(() => {
        if (!active) return
        setClosedLoading(false)
      })
    return () => {
      active = false
    }
  }, [activeTab, closedRange, selectedPubkey, t])

  useEffect(() => {
    if (!selectedPubkey || activeTab !== 'fees') return
    let active = true
    setFeesLoading(true)
    setFeesError('')
    void getGraphExplorerNodeFees(selectedPubkey, { range: feeRange })
      .then((response) => {
        if (!active) return
        setFees(response as GraphExplorerFeeResponse)
      })
      .catch((err: any) => {
        if (!active) return
        setFees(null)
        setFeesError(err?.message || t('graphExplorer.feesLoadFailed'))
      })
      .finally(() => {
        if (!active) return
        setFeesLoading(false)
      })
    return () => {
      active = false
    }
  }, [activeTab, feeRange, selectedPubkey, t])

  const handleSelectNode = (item: GraphExplorerSearchResult) => {
    const pubkey = String(item?.pubkey || '').trim()
    if (!pubkey) return
    setSelectedPubkey(pubkey)
    setQuery(String(item.alias || item.pubkey || '').trim())
    window.location.hash = buildGraphExplorerHash(pubkey, activeTab)
  }

  const handleTabChange = (tab: GraphExplorerTab) => {
    setActiveTab(tab)
    window.location.hash = buildGraphExplorerHash(selectedPubkey, tab)
  }

  const handleRefresh = async () => {
    setRefreshing(true)
    setStatusError('')
    try {
      const response: any = await recomputeGraphExplorer()
      const nextStatus = response?.status as GraphExplorerStatus | undefined
      if (nextStatus) {
        setStatus(nextStatus)
      } else {
        const fallbackStatus = await getGraphExplorerStatus() as GraphExplorerStatus
        setStatus(fallbackStatus)
      }
      try {
        const lndStatus = await getLndStatus() as LndStatusResponse
        setCurrentBlockHeight(Math.max(0, Math.round(Number(lndStatus?.block_height || 0))))
      } catch {
        // keep previous height on transient failures
      }
      if (selectedPubkey) {
        const nextGeneral = await getGraphExplorerNodeGeneral(selectedPubkey) as GraphExplorerNodeGeneral
        setGeneral(nextGeneral)
        setGeneralError('')
        if (activeTab === 'channels') {
          const nextChannels = await getGraphExplorerNodeChannels(selectedPubkey, { limit: 400 }) as GraphExplorerNodeChannelsResponse
          setChannels(nextChannels)
          setChannelsError('')
        }
        if (activeTab === 'closed') {
          const nextClosed = await getGraphExplorerNodeClosed(selectedPubkey, { range: closedRange, limit: 300 }) as GraphExplorerClosedResponse
          setClosed(nextClosed)
          setClosedError('')
        }
        if (activeTab === 'fees') {
          const nextFees = await getGraphExplorerNodeFees(selectedPubkey, { range: feeRange }) as GraphExplorerFeeResponse
          setFees(nextFees)
          setFeesError('')
        }
      }
      if (query.trim().length >= 2) {
        const nextSearch = await searchGraphExplorerNodes({ q: query.trim(), limit: 10 }) as GraphExplorerSearchResponse
        setSearchResults(Array.isArray(nextSearch?.items) ? nextSearch.items : [])
      }
    } catch (err: any) {
      setStatusError(err?.message || t('graphExplorer.refreshFailed'))
    } finally {
      setRefreshing(false)
    }
  }

  const handleCopy = async (value: string, key: string) => {
    const text = String(value || '').trim()
    if (!text) return
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error('clipboard_unavailable')
      }
      await navigator.clipboard.writeText(text)
      setCopiedKey(key)
      setCopyError('')
    } catch {
      setCopiedKey('')
      setCopyError(t('common.copyFailedManual'))
    }
  }

  const handleChannelSort = (key: ChannelSortKey) => {
    setChannelSort((current) => {
      if (current.key === key) {
        return { key, direction: current.direction === 'asc' ? 'desc' : 'asc' }
      }
      return { key, direction: key === 'peer' ? 'asc' : 'desc' }
    })
  }

  const node = general?.node || null
  const selectedColor = normalizeNodeColor(node?.color)
  const coverageSince = general?.coverage_since || status?.first_native_coverage_at || ''
  const statusBadgeClass = status?.running
    ? 'border-emerald-400/30 bg-emerald-500/10 text-emerald-200'
    : 'border-white/10 bg-white/[0.04] text-fog/75'
  const selectedResultKey = String(node?.pubkey || selectedPubkey || '').trim()
  const shouldShowSearchHint = deferredQuery.trim().length < 2
  const addressList = Array.isArray(node?.addresses) ? node.addresses : []
  const channelItems = Array.isArray(channels?.items) ? channels.items : []
  const closedItems = Array.isArray(closed?.items) ? closed.items : []
  const feeHistory = Array.isArray(fees?.history) ? fees.history : []
  const outboundBins = Array.isArray(fees?.outbound_bins) ? fees.outbound_bins : []
  const inboundBins = Array.isArray(fees?.inbound_bins) ? fees.inbound_bins : []
  const estimatedOldestChannelDate = estimateDateFromBlock(node?.oldest_channel_block)
  const estimatedYoungestChannelDate = estimateDateFromBlock(node?.youngest_channel_block)
  const approximateNodeStartDate = estimatedOldestChannelDate || parseTimestamp(node?.first_seen_at)

  const metricCards = node
    ? [
        { key: 'channelCount', label: t('graphExplorer.metrics.publicChannels'), value: formatInteger(node.channel_count) },
        { key: 'peerCount', label: t('graphExplorer.metrics.peerCount'), value: formatInteger(node.peer_count) },
        { key: 'totalCapacity', label: t('graphExplorer.metrics.totalCapacity'), value: formatSats(node.total_capacity_sat) },
        { key: 'smallestChannel', label: t('graphExplorer.metrics.smallestChannel'), value: formatSats(node.smallest_channel_sat) },
        { key: 'largestChannel', label: t('graphExplorer.metrics.largestChannel'), value: formatSats(node.largest_channel_sat) },
        { key: 'averageChannel', label: t('graphExplorer.metrics.averageChannel'), value: formatSats(node.average_channel_size_sat) },
        { key: 'lastPolicyUpdate', label: t('graphExplorer.metrics.lastPolicyUpdate'), value: formatTimestamp(node.last_policy_update_at) },
        { key: 'lastGraphUpdate', label: t('graphExplorer.metrics.lastGraphUpdate'), value: formatTimestamp(node.last_graph_update_at) }
      ]
    : []

  const sortedChannelItems = useMemo(() => {
    const items = [...channelItems]
    const multiplier = channelSort.direction === 'asc' ? 1 : -1
    items.sort((left, right) => {
      const compareString = (a?: string, b?: string) =>
        String(a || '').localeCompare(String(b || ''), locale, { sensitivity: 'base' })
      const compareNumber = (a?: number, b?: number) => Number(a || 0) - Number(b || 0)
      let comparison = 0
      switch (channelSort.key) {
        case 'peer':
          comparison =
            compareString(left.peer_alias || left.peer_pubkey, right.peer_alias || right.peer_pubkey) ||
            compareString(left.peer_pubkey, right.peer_pubkey)
          break
        case 'channel':
          comparison = compareNumber(left.channel_id, right.channel_id)
          break
        case 'capacity':
          comparison = compareNumber(left.capacity_sat, right.capacity_sat)
          break
        case 'openBlock':
          comparison = compareNumber(left.open_block_height, right.open_block_height)
          break
        case 'targetPolicy':
          comparison =
            compareNumber(left.target_policy?.fee_rate_ppm, right.target_policy?.fee_rate_ppm) ||
            compareNumber(left.target_policy?.fee_base_msat, right.target_policy?.fee_base_msat)
          break
        case 'peerPolicy':
          comparison =
            compareNumber(left.peer_policy?.fee_rate_ppm, right.peer_policy?.fee_rate_ppm) ||
            compareNumber(left.peer_policy?.fee_base_msat, right.peer_policy?.fee_base_msat)
          break
        case 'lastUpdate':
          comparison = compareString(left.last_policy_update, right.last_policy_update)
          break
      }
      if (comparison === 0) {
        comparison = compareNumber(left.channel_id, right.channel_id)
      }
      return comparison * multiplier
    })
    return items
  }, [channelItems, channelSort, locale])

  const feeHistoryAscending = useMemo(
    () => [...feeHistory].sort((left, right) => String(left.day || '').localeCompare(String(right.day || ''))),
    [feeHistory]
  )

  const computeFeeTrend = (currentValue: number, baselineValue: number): FeeTrendResult => {
    const current = Number(currentValue || 0)
    const baseline = Number(baselineValue || 0)
    if (current <= 0 || baseline <= 0) {
      return { direction: 'flat', deltaPct: 0, available: false }
    }
    const deltaPct = ((current - baseline) / baseline) * 100
    if (Math.abs(deltaPct) < 0.05) {
      return { direction: 'flat', deltaPct: 0, available: true }
    }
    return {
      direction: deltaPct > 0 ? 'up' : 'down',
      deltaPct,
      available: true
    }
  }

  const currentDayUTC = new Date().toISOString().slice(0, 10)
  const buildFeeBaseline = (selector: (item: GraphExplorerFeeHistoryPoint) => number, sampleSelector: (item: GraphExplorerFeeHistoryPoint) => number) => {
    const values = feeHistoryAscending
      .filter((item) => item.day !== currentDayUTC)
      .filter((item) => Number(sampleSelector(item) || 0) > 0)
      .map((item) => Number(selector(item) || 0))
      .filter((value) => Number.isFinite(value) && value > 0)
      .slice(-FEE_TREND_BASELINE_WINDOW_DAYS)
    if (values.length < FEE_TREND_BASELINE_MIN_DAYS) {
      return null
    }
    const average = values.reduce((sum, value) => sum + value, 0) / values.length
    if (!Number.isFinite(average) || average < FEE_TREND_BASELINE_MIN_PPM) {
      return null
    }
    return average
  }

  const outboundBaseline = buildFeeBaseline(
    (item) => item.outbound_weighted_avg_ppm,
    (item) => item.outbound_sample_count
  )
  const inboundBaseline = buildFeeBaseline(
    (item) => item.inbound_weighted_avg_ppm,
    (item) => item.inbound_sample_count
  )
  const ratioBaselineSeries = feeHistoryAscending
    .filter((item) => item.day !== currentDayUTC)
    .filter((item) => Number(item.outbound_sample_count || 0) > 0 && Number(item.inbound_sample_count || 0) > 0)
    .map((item) => {
      const inbound = Number(item.inbound_weighted_avg_ppm || 0)
      const outbound = Number(item.outbound_weighted_avg_ppm || 0)
      if (inbound <= 0 || outbound <= 0) return 0
      return outbound / inbound
    })
    .filter((value) => Number.isFinite(value) && value > 0)
    .slice(-FEE_TREND_BASELINE_WINDOW_DAYS)
  const ratioBaseline = ratioBaselineSeries.length >= FEE_TREND_BASELINE_MIN_DAYS
    ? ratioBaselineSeries.reduce((sum, value) => sum + value, 0) / ratioBaselineSeries.length
    : null

  const outboundTrend = outboundBaseline === null
    ? { direction: 'flat' as FeeTrendDirection, deltaPct: 0, available: false }
    : computeFeeTrend(Number(fees?.outbound?.weighted_avg_ppm || 0), outboundBaseline)
  const inboundTrend = inboundBaseline === null
    ? { direction: 'flat' as FeeTrendDirection, deltaPct: 0, available: false }
    : computeFeeTrend(Number(fees?.inbound?.weighted_avg_ppm || 0), inboundBaseline)
  const currentOutInRatio = Number(fees?.inbound?.weighted_avg_ppm || 0) > 0
    ? Number(fees?.outbound?.weighted_avg_ppm || 0) / Number(fees?.inbound?.weighted_avg_ppm || 0)
    : 0
  const outInRatioTrend = ratioBaseline === null
    ? { direction: 'flat' as FeeTrendDirection, deltaPct: 0, available: false }
    : computeFeeTrend(currentOutInRatio, ratioBaseline)

  const renderGeneralTab = () => (
    <>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {metricCards.map((item) => (
          <div key={item.key} className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-4">
            <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{item.label}</p>
            <p className="mt-3 text-lg font-semibold text-fog">{item.value}</p>
          </div>
        ))}
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
          <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.sections.presence')}</p>
          <dl className="mt-4 space-y-4">
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.approxNodeAge')}</dt>
              <dd className="text-right">
                <div className="text-sm font-medium text-fog">{formatApproximateAge(approximateNodeStartDate)}</div>
                <div className="text-xs text-fog/55">{formatEstimatedDate(approximateNodeStartDate)}</div>
              </dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.firstSeen')}</dt>
              <dd className="text-sm font-medium text-fog">{formatTimestamp(node?.first_seen_at)}</dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.lastSeen')}</dt>
              <dd className="text-sm font-medium text-fog">{formatTimestamp(node?.last_seen_at)}</dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.oldestChannelBlock')}</dt>
              <dd className="text-right">
                <div className="text-sm font-medium text-fog">{formatBlock(node?.oldest_channel_block)}</div>
                <div className="text-xs text-fog/55">{formatEstimatedDate(estimatedOldestChannelDate)}</div>
              </dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.youngestChannelBlock')}</dt>
              <dd className="text-right">
                <div className="text-sm font-medium text-fog">{formatBlock(node?.youngest_channel_block)}</div>
                <div className="text-xs text-fog/55">{formatEstimatedDate(estimatedYoungestChannelDate)}</div>
              </dd>
            </div>
          </dl>
        </div>

        <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
          <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.sections.addressSummary')}</p>
          <dl className="mt-4 space-y-4">
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.addressCount')}</dt>
              <dd className="text-sm font-medium text-fog">{formatInteger(node?.address_count)}</dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.clearnetAddresses')}</dt>
              <dd className="text-sm font-medium text-fog">{formatInteger(node?.clearnet_address_count)}</dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.onionAddresses')}</dt>
              <dd className="text-sm font-medium text-fog">{formatInteger(node?.onion_address_count)}</dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-sm text-fog/65">{t('graphExplorer.fields.selectedPubkey')}</dt>
              <dd className="flex items-center gap-2">
                <span className="truncate text-sm font-medium text-fog">{shortPubkey(node?.pubkey || '')}</span>
                <button
                  type="button"
                  className="rounded-full border border-white/10 bg-white/[0.05] px-2.5 py-1 text-[11px] font-medium text-fog/75 transition hover:border-white/20 hover:text-fog"
                  onClick={() => void handleCopy(node?.pubkey || '', 'node-pubkey')}
                >
                  {copiedKey === 'node-pubkey' ? t('common.copied') : t('common.copy')}
                </button>
              </dd>
            </div>
          </dl>
        </div>
      </div>
    </>
  )

  const renderChannelsTab = () => (
    <div className="space-y-4">
      {channelsLoading ? (
        <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-6 text-fog/70">
          {t('graphExplorer.loadingChannels')}
        </div>
      ) : channelsError ? (
        <div className="rounded-[1.35rem] border border-amber-400/25 bg-amber-500/10 p-6 text-amber-100">
          {channelsError}
        </div>
      ) : channelItems.length === 0 ? (
        <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-6 text-fog/70">
          {t('graphExplorer.channelsEmpty')}
        </div>
      ) : (
        <>
          <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
            <p className="text-sm font-medium text-fog">{t('graphExplorer.sections.channels')}</p>
            <p className="mt-2 text-sm text-fog/60">
              {t('graphExplorer.channelCoverage', { value: formatTimestamp(channels?.coverage_since || coverageSince) })}
            </p>
          </div>
          <div className="max-h-[42rem] overflow-auto rounded-[1.35rem] border border-white/10 bg-white/[0.03]">
            <table className="min-w-full text-sm">
              <thead className="sticky top-0 z-10 border-b border-white/10 bg-[#182232] text-left text-xs uppercase tracking-[0.14em] text-fog/45">
                <tr>
                  <th className="px-4 py-3">
                    <button type="button" className="flex items-center gap-2" onClick={() => handleChannelSort('peer')}>
                      <span>{t('graphExplorer.columns.peer')}</span>
                      <span>{sortIndicator(channelSort.key === 'peer', channelSort.direction)}</span>
                    </button>
                  </th>
                  <th className="px-4 py-3">
                    <button type="button" className="flex items-center gap-2" onClick={() => handleChannelSort('channel')}>
                      <span>{t('graphExplorer.columns.channel')}</span>
                      <span>{sortIndicator(channelSort.key === 'channel', channelSort.direction)}</span>
                    </button>
                  </th>
                  <th className="px-4 py-3">
                    <button type="button" className="flex items-center gap-2" onClick={() => handleChannelSort('capacity')}>
                      <span>{t('graphExplorer.columns.capacity')}</span>
                      <span>{sortIndicator(channelSort.key === 'capacity', channelSort.direction)}</span>
                    </button>
                  </th>
                  <th className="px-4 py-3">
                    <button type="button" className="flex items-center gap-2" onClick={() => handleChannelSort('openBlock')}>
                      <span>{t('graphExplorer.columns.openBlock')}</span>
                      <span>{sortIndicator(channelSort.key === 'openBlock', channelSort.direction)}</span>
                    </button>
                  </th>
                  <th className="px-4 py-3">
                    <button type="button" className="flex items-center gap-2" onClick={() => handleChannelSort('targetPolicy')}>
                      <span>{t('graphExplorer.columns.targetPolicy')}</span>
                      <span>{sortIndicator(channelSort.key === 'targetPolicy', channelSort.direction)}</span>
                    </button>
                  </th>
                  <th className="px-4 py-3">
                    <button type="button" className="flex items-center gap-2" onClick={() => handleChannelSort('peerPolicy')}>
                      <span>{t('graphExplorer.columns.peerPolicy')}</span>
                      <span>{sortIndicator(channelSort.key === 'peerPolicy', channelSort.direction)}</span>
                    </button>
                  </th>
                  <th className="px-4 py-3">
                    <button type="button" className="flex items-center gap-2" onClick={() => handleChannelSort('lastUpdate')}>
                      <span>{t('graphExplorer.columns.lastUpdate')}</span>
                      <span>{sortIndicator(channelSort.key === 'lastUpdate', channelSort.direction)}</span>
                    </button>
                  </th>
                </tr>
              </thead>
              <tbody>
                {sortedChannelItems.map((item) => (
                  <tr key={`${item.channel_id}-${item.chan_point || 'unknown'}`} className="border-t border-white/6 align-top">
                    <td className="px-4 py-4">
                      <div className="space-y-1">
                        <div className="font-medium text-fog">{item.peer_alias || t('graphExplorer.aliasFallback')}</div>
                        <div className="font-mono text-xs text-fog/55">{shortPubkey(item.peer_pubkey)}</div>
                      </div>
                    </td>
                    <td className="px-4 py-4">
                      <div className="space-y-1">
                        <div className="font-medium text-fog">{formatInteger(item.channel_id)}</div>
                        <div className="font-mono text-xs text-fog/55">{item.chan_point || t('common.na')}</div>
                      </div>
                    </td>
                    <td className="px-4 py-4 text-fog">{formatSats(item.capacity_sat)}</td>
                    <td className="px-4 py-4 text-fog">{formatBlock(item.open_block_height)}</td>
                    <td className="px-4 py-4">
                      <PolicyCell policy={item.target_policy} formatInteger={formatInteger} t={t} />
                    </td>
                    <td className="px-4 py-4">
                      <PolicyCell policy={item.peer_policy} formatInteger={formatInteger} t={t} />
                    </td>
                    <td className="px-4 py-4 text-fog">{formatTimestamp(item.last_policy_update)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  )

  const renderClosedTab = () => (
    <div className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
        <div>
          <p className="text-sm font-medium text-fog">{t('graphExplorer.sections.closed')}</p>
          <p className="mt-2 text-sm text-fog/60">
            {t('graphExplorer.closedCoverage', { value: formatTimestamp(closed?.coverage_since || coverageSince) })}
          </p>
          <p className="mt-2 text-xs text-fog/50">{t('graphExplorer.closedTrackingHint')}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          {(['30d', '90d', '1y', 'all'] as GraphExplorerClosedRange[]).map((value) => (
            <button
              key={value}
              type="button"
              className={rangeButtonClass(closedRange === value)}
              onClick={() => setClosedRange(value)}
            >
              {value}
            </button>
          ))}
        </div>
      </div>

      {closedLoading ? (
        <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-6 text-fog/70">
          {t('graphExplorer.loadingClosed')}
        </div>
      ) : closedError ? (
        <div className="rounded-[1.35rem] border border-amber-400/25 bg-amber-500/10 p-6 text-amber-100">
          {closedError}
        </div>
      ) : (
        <>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-4">
              <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.closedSummary.total')}</p>
              <p className="mt-3 text-lg font-semibold text-fog">
                {formatInteger(closed?.summary?.total_closed_channels)}
              </p>
            </div>
            <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-4">
              <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.closedSummary.capacity')}</p>
              <p className="mt-3 text-lg font-semibold text-fog">{formatSats(closed?.summary?.total_capacity_sat)}</p>
            </div>
            <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-4">
              <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.closedSummary.knownTypes')}</p>
              <p className="mt-3 text-lg font-semibold text-fog">{formatInteger(closed?.summary?.known_type_count)}</p>
            </div>
            <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-4">
              <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.closedSummary.unknownTypes')}</p>
              <p className="mt-3 text-lg font-semibold text-fog">{formatInteger(closed?.summary?.unknown_type_count)}</p>
            </div>
          </div>
          {closedItems.length === 0 ? (
            <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-6 text-fog/70">
              {t('graphExplorer.closedEmpty')}
            </div>
          ) : (
            <div className="overflow-x-auto rounded-[1.35rem] border border-white/10 bg-white/[0.03]">
              <table className="min-w-full text-sm">
                <thead className="border-b border-white/10 bg-white/[0.025] text-left text-xs uppercase tracking-[0.14em] text-fog/45">
                  <tr>
                    <th className="px-4 py-3">{t('graphExplorer.columns.observedAt')}</th>
                    <th className="px-4 py-3">{t('graphExplorer.columns.peer')}</th>
                    <th className="px-4 py-3">{t('graphExplorer.columns.channel')}</th>
                    <th className="px-4 py-3">{t('graphExplorer.columns.capacity')}</th>
                    <th className="px-4 py-3">{t('graphExplorer.columns.closeHeight')}</th>
                    <th className="px-4 py-3">{t('graphExplorer.columns.closeType')}</th>
                    <th className="px-4 py-3">{t('graphExplorer.columns.closeSource')}</th>
                  </tr>
                </thead>
                <tbody>
                  {closedItems.map((item) => (
                    <tr key={`${item.channel_id}-${item.observed_at || item.chan_point || 'closed'}`} className="border-t border-white/6 align-top">
                      <td className="px-4 py-4 text-fog">{formatTimestamp(item.observed_at)}</td>
                      <td className="px-4 py-4">
                        <div className="space-y-1">
                          <div className="font-medium text-fog">{item.peer_alias || t('graphExplorer.aliasFallback')}</div>
                          <div className="font-mono text-xs text-fog/55">{shortPubkey(item.peer_pubkey || '')}</div>
                        </div>
                      </td>
                      <td className="px-4 py-4">
                        <div className="space-y-1">
                          <div className="font-medium text-fog">{formatInteger(item.channel_id)}</div>
                          <div className="font-mono text-xs text-fog/55">{item.chan_point || t('common.na')}</div>
                        </div>
                      </td>
                      <td className="px-4 py-4 text-fog">{formatSats(item.capacity_sat)}</td>
                      <td className="px-4 py-4 text-fog">{formatBlock(item.closed_height)}</td>
                      <td className="px-4 py-4">
                        <span className={clsx('inline-flex rounded-full border px-2.5 py-1 text-xs font-medium', closeTypeTone(item.close_type))}>
                          {String(item.close_type || 'unknown').replace(/_/g, ' ')}
                        </span>
                      </td>
                      <td className="px-4 py-4 text-fog/75">{item.close_source || t('common.unknown')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </div>
  )

  const renderFeesTab = () => (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
        <div>
          <p className="text-sm font-medium text-fog">{t('graphExplorer.sections.fees')}</p>
          <p className="mt-2 text-sm text-fog/60">
            {t('graphExplorer.feeCoverage', { value: formatTimestamp(fees?.coverage_since || coverageSince) })}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {(['7d', '30d', '90d', '1y', 'all'] as GraphExplorerFeeRange[]).map((value) => (
            <button
              key={value}
              type="button"
              className={rangeButtonClass(feeRange === value)}
              onClick={() => setFeeRange(value)}
            >
              {value}
            </button>
          ))}
        </div>
      </div>

      {feesLoading ? (
        <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-6 text-fog/70">
          {t('graphExplorer.loadingFees')}
        </div>
      ) : feesError ? (
        <div className="rounded-[1.35rem] border border-amber-400/25 bg-amber-500/10 p-6 text-amber-100">
          {feesError}
        </div>
      ) : !fees?.outbound && !fees?.inbound ? (
        <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-6 text-fog/70">
          {t('graphExplorer.feesEmpty')}
        </div>
      ) : (
        <>
          <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(18rem,0.86fr)_minmax(0,1fr)]">
            <FeeSummaryCard
              title={t('graphExplorer.feeSummary.outboundTitle')}
              summary={fees?.outbound || {
                channel_count: 0,
                disabled_count: 0,
                min_ppm: 0,
                max_ppm: 0,
                avg_ppm: 0,
                median_ppm: 0,
                weighted_avg_ppm: 0,
                total_capacity_sat: 0
              }}
              formatInteger={formatInteger}
              formatSats={formatSats}
              formatTimestamp={formatTimestamp}
              t={t}
            />
            <FeeComparisonCard
              ratio={currentOutInRatio}
              ratioTrend={outInRatioTrend}
              outboundValue={Number(fees?.outbound?.weighted_avg_ppm || 0)}
              inboundValue={Number(fees?.inbound?.weighted_avg_ppm || 0)}
              outboundTrend={outboundTrend}
              inboundTrend={inboundTrend}
              formatInteger={formatInteger}
              t={t}
            />
            <FeeSummaryCard
              title={t('graphExplorer.feeSummary.inboundTitle')}
              summary={fees?.inbound || {
                channel_count: 0,
                disabled_count: 0,
                min_ppm: 0,
                max_ppm: 0,
                avg_ppm: 0,
                median_ppm: 0,
                weighted_avg_ppm: 0,
                total_capacity_sat: 0
              }}
              formatInteger={formatInteger}
              formatSats={formatSats}
              formatTimestamp={formatTimestamp}
              t={t}
            />
          </div>

          <div className="grid gap-4 xl:grid-cols-2">
            <FeeDistributionPanel
              title={t('graphExplorer.feeDistribution.outbound')}
              bins={outboundBins}
              formatInteger={formatInteger}
              formatSats={formatSats}
              t={t}
            />
            <FeeDistributionPanel
              title={t('graphExplorer.feeDistribution.inbound')}
              bins={inboundBins}
              formatInteger={formatInteger}
              formatSats={formatSats}
              t={t}
            />
          </div>

          <div className="rounded-[1.35rem] border border-white/10 bg-white/[0.03] p-5">
            <div className="flex items-center justify-between gap-3">
              <p className="text-sm font-medium text-fog">{t('graphExplorer.feeHistory.title')}</p>
              <span className="text-xs text-fog/55">{t('graphExplorer.feeHistory.subtitle')}</span>
            </div>
            {feeHistory.length === 0 ? (
              <p className="mt-4 text-sm text-fog/60">{t('graphExplorer.feeHistoryEmpty')}</p>
            ) : (
              <div className="mt-4 overflow-x-auto">
                <table className="min-w-full text-sm">
                  <thead className="border-b border-white/10 text-left text-xs uppercase tracking-[0.14em] text-fog/45">
                    <tr>
                      <th className="px-3 py-3">{t('graphExplorer.columns.day')}</th>
                      <th className="px-3 py-3">{t('graphExplorer.columns.outbound')}</th>
                      <th className="px-3 py-3">{t('graphExplorer.columns.inbound')}</th>
                      <th className="px-3 py-3">{t('graphExplorer.columns.samples')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {feeHistory.map((item) => (
                      <tr key={item.day} className="border-t border-white/6">
                        <td className="px-3 py-3 text-fog">{item.day}</td>
                        <td className="px-3 py-3 text-fog">
                          <div>{formatInteger(item.outbound_avg_ppm)} ppm</div>
                          <div className="text-xs text-fog/55">
                            {t('graphExplorer.feeHistoryWeighted', { value: formatInteger(item.outbound_weighted_avg_ppm) })}
                          </div>
                        </td>
                        <td className="px-3 py-3 text-fog">
                          <div>{formatInteger(item.inbound_avg_ppm)} ppm</div>
                          <div className="text-xs text-fog/55">
                            {t('graphExplorer.feeHistoryWeighted', { value: formatInteger(item.inbound_weighted_avg_ppm) })}
                          </div>
                        </td>
                        <td className="px-3 py-3 text-fog">
                          {formatInteger(item.outbound_sample_count)} / {formatInteger(item.inbound_sample_count)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  )

  return (
    <div className="space-y-6">
      <section className="section-card space-y-6">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-2">
            <p className="text-xs uppercase tracking-[0.22em] text-fog/50">{t('graphExplorer.kicker')}</p>
            <div>
              <h1 className="text-3xl font-semibold">{t('graphExplorer.title')}</h1>
              <p className="mt-2 max-w-3xl text-sm text-fog/70">{t('graphExplorer.subtitle')}</p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-full border border-sky-400/25 bg-sky-500/10 px-3 py-1 text-xs font-medium text-sky-100">
              {t('graphExplorer.badges.nativeSource')}
            </span>
            <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs font-medium text-fog/75">
              {t('graphExplorer.badges.coverageSince', { value: formatTimestamp(coverageSince) })}
            </span>
            <span className={clsx('rounded-full border px-3 py-1 text-xs font-medium', statusBadgeClass)}>
              {status?.running ? t('graphExplorer.badges.streaming') : t('graphExplorer.badges.idle')}
            </span>
            <button
              type="button"
              className={clsx('btn-primary', refreshing && 'opacity-70 cursor-wait')}
              onClick={handleRefresh}
              disabled={refreshing}
            >
              {refreshing ? t('graphExplorer.refreshing') : t('graphExplorer.refresh')}
            </button>
          </div>
        </div>
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_minmax(18rem,0.8fr)]">
          <div className="rounded-[1.5rem] border border-white/10 bg-black/10 p-4">
            <label htmlFor="graph-explorer-search" className="text-sm font-medium text-fog/80">
              {t('graphExplorer.searchLabel')}
            </label>
            <div className="mt-3 flex flex-col gap-3 md:flex-row">
              <input
                id="graph-explorer-search"
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t('graphExplorer.searchPlaceholder')}
                className="min-w-0 flex-1 rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-3 text-sm text-fog outline-none transition placeholder:text-fog/35 focus:border-sky-300/40 focus:bg-white/[0.06]"
                autoComplete="off"
                spellCheck={false}
              />
              {selectedPubkey && (
                <button
                  type="button"
                  className="rounded-2xl border border-white/10 bg-white/[0.04] px-4 py-3 text-sm text-fog/80 transition hover:border-white/20 hover:text-white"
                  onClick={() => {
                    setSelectedPubkey('')
                    setActiveTab('general')
                    setGeneral(null)
                    setGeneralError('')
                    setChannels(null)
                    setClosed(null)
                    setFees(null)
                    window.location.hash = `#${GRAPH_EXPLORER_ROUTE_KEY}`
                  }}
                >
                  {t('graphExplorer.clearSelection')}
                </button>
              )}
            </div>
            <p className="mt-3 text-sm text-fog/60">
              {shouldShowSearchHint ? t('graphExplorer.searchHint') : t('graphExplorer.searchLiveHint')}
            </p>
            {searchError && (
              <p className="mt-3 text-sm text-amber-200">{searchError}</p>
            )}
            <div className="mt-4 space-y-2">
              {searchLoading && (
                <p className="text-sm text-fog/60">{t('graphExplorer.searching')}</p>
              )}
              {!searchLoading && !searchError && !shouldShowSearchHint && searchResults.length === 0 && (
                <p className="text-sm text-fog/60">{t('graphExplorer.searchEmpty')}</p>
              )}
              {searchResults.map((item) => {
                const isSelected = String(item.pubkey || '').trim() === selectedResultKey
                return (
                  <button
                    key={item.pubkey}
                    type="button"
                    onClick={() => handleSelectNode(item)}
                    className={clsx(
                      'w-full rounded-[1.35rem] border px-4 py-3 text-left transition',
                      isSelected
                        ? 'border-sky-300/35 bg-sky-500/10 shadow-[0_0_0_1px_rgba(125,211,252,0.12)]'
                        : 'border-white/10 bg-white/[0.03] hover:border-white/20 hover:bg-white/[0.05]'
                    )}
                  >
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span
                            className="h-3 w-3 rounded-full border border-white/10"
                            style={{ backgroundColor: normalizeNodeColor(item.color) }}
                          />
                          <span className="truncate font-medium text-fog">
                            {item.alias || t('graphExplorer.aliasFallback')}
                          </span>
                        </div>
                        <p className="mt-1 truncate text-xs text-fog/55">{item.pubkey}</p>
                      </div>
                      <div className="text-right text-xs text-fog/65">
                        <p>{t('graphExplorer.searchResultChannels', { count: item.channel_count })}</p>
                        <p className="mt-1">{formatSats(item.total_capacity_sat)}</p>
                      </div>
                    </div>
                  </button>
                )
              })}
            </div>
          </div>

          <div className="rounded-[1.5rem] border border-white/10 bg-black/10 p-4">
            <p className="text-sm font-medium text-fog/80">{t('graphExplorer.indexStatus')}</p>
            <div className="mt-4 grid gap-3 sm:grid-cols-3 xl:grid-cols-1">
              <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.statusCards.nodes')}</p>
                <p className="mt-2 text-2xl font-semibold">{formatInteger(status?.node_count)}</p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.statusCards.openChannels')}</p>
                <p className="mt-2 text-2xl font-semibold">{formatInteger(status?.open_channel_count)}</p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
                <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.statusCards.lastSync')}</p>
                <p className="mt-2 text-sm font-medium text-fog">{formatTimestamp(status?.last_sync_at)}</p>
              </div>
            </div>
            {statusError && (
              <p className="mt-4 text-sm text-amber-200">{statusError}</p>
            )}
            {!statusError && status?.last_error && (
              <p className="mt-4 text-sm text-amber-200">{status.last_error}</p>
            )}
            {!statusError && Number(status?.node_count || 0) === 0 && (
              <p className="mt-4 text-sm text-fog/60">{t('graphExplorer.emptyIndex')}</p>
            )}
          </div>
        </div>
      </section>

      <section className="section-card space-y-5">
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" className={tabButtonClass(activeTab === 'general')} onClick={() => handleTabChange('general')}>
            {t('graphExplorer.tabs.general')}
          </button>
          <button type="button" className={tabButtonClass(activeTab === 'channels')} onClick={() => handleTabChange('channels')}>
            {t('graphExplorer.tabs.channels')}
          </button>
          <button type="button" className={tabButtonClass(activeTab === 'closed')} onClick={() => handleTabChange('closed')}>
            {t('graphExplorer.tabs.closed')}
          </button>
          <button type="button" className={tabButtonClass(activeTab === 'fees')} onClick={() => handleTabChange('fees')}>
            {t('graphExplorer.tabs.fees')}
          </button>
        </div>

        {generalLoading && !node ? (
          <div className="rounded-[1.5rem] border border-white/10 bg-white/[0.03] p-6 text-fog/70">
            {t('graphExplorer.loadingNode')}
          </div>
        ) : generalError && !node ? (
          <div className="rounded-[1.5rem] border border-amber-400/25 bg-amber-500/10 p-6 text-amber-100">
            {generalError}
          </div>
        ) : !node ? (
          <div className="rounded-[1.5rem] border border-white/10 bg-white/[0.03] p-6">
            <h2 className="text-lg font-semibold">{t('graphExplorer.emptySelectionTitle')}</h2>
            <p className="mt-2 max-w-2xl text-sm text-fog/65">{t('graphExplorer.emptySelectionBody')}</p>
          </div>
        ) : (
          <>
            {generalError && (
              <div className="rounded-[1.35rem] border border-amber-400/25 bg-amber-500/10 p-4 text-sm text-amber-100">
                {generalError}
              </div>
            )}

            <div className="rounded-[1.75rem] border border-white/10 bg-[radial-gradient(circle_at_top_left,rgba(56,189,248,0.14),transparent_38%),linear-gradient(180deg,rgba(255,255,255,0.03),rgba(255,255,255,0.015))] p-6">
              <div className="flex flex-col gap-6 xl:flex-row xl:items-start xl:justify-between">
                <div className="flex min-w-0 gap-4">
                  <div
                    className="grid h-16 w-16 flex-none place-items-center rounded-2xl border border-white/10 text-xl font-semibold text-slate-950"
                    style={{ backgroundColor: selectedColor }}
                  >
                    {String(node.alias || node.pubkey || '?').trim().slice(0, 1).toUpperCase()}
                  </div>
                  <div className="min-w-0 space-y-2">
                    <div>
                      <h2 className="truncate text-2xl font-semibold text-fog">
                        {node.alias || t('graphExplorer.aliasFallback')}
                      </h2>
                      <div className="mt-2 flex flex-wrap items-start gap-2">
                        <p className="min-w-0 flex-1 break-all font-mono text-xs text-fog/62">{node.pubkey}</p>
                        <button
                          type="button"
                          className="rounded-full border border-white/10 bg-white/[0.05] px-2.5 py-1 text-[11px] font-medium text-fog/75 transition hover:border-white/20 hover:text-fog"
                          onClick={() => void handleCopy(node.pubkey, 'node-pubkey')}
                        >
                          {copiedKey === 'node-pubkey' ? t('common.copied') : t('common.copy')}
                        </button>
                      </div>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/75">
                        {t('graphExplorer.header.source', {
                          value: general?.source === 'native'
                            ? t('graphExplorer.badges.nativeSource')
                            : general?.source || t('common.unknown')
                        })}
                      </span>
                      <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/75">
                        {t('graphExplorer.header.coverage', { value: formatTimestamp(coverageSince) })}
                      </span>
                      <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/75">
                        {t('graphExplorer.header.lastSeen', { value: formatTimestamp(node.last_seen_at) })}
                      </span>
                    </div>
                  </div>
                </div>

                <div className="grid gap-3 sm:grid-cols-2 xl:min-w-[28rem]">
                  <div className="rounded-2xl border border-white/10 bg-black/15 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.header.publicChannels')}</p>
                    <p className="mt-2 text-2xl font-semibold">{formatInteger(node.channel_count)}</p>
                  </div>
                  <div className="rounded-2xl border border-white/10 bg-black/15 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.header.totalCapacity')}</p>
                    <p className="mt-2 text-2xl font-semibold">{formatSats(node.total_capacity_sat)}</p>
                  </div>
                  <div className="rounded-2xl border border-white/10 bg-black/15 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.header.addresses')}</p>
                    <p className="mt-2 text-2xl font-semibold">{formatInteger(node.address_count)}</p>
                  </div>
                  <div className="rounded-2xl border border-white/10 bg-black/15 p-4">
                    <p className="text-xs uppercase tracking-[0.18em] text-fog/45">{t('graphExplorer.header.lastPolicyUpdate')}</p>
                    <p className="mt-2 text-sm font-medium text-fog">{formatTimestamp(node.last_policy_update_at)}</p>
                  </div>
                </div>
              </div>

              <div className="mt-6 flex flex-wrap gap-2">
                {addressList.length === 0 ? (
                  <span className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-xs text-fog/60">
                    {t('graphExplorer.noAddresses')}
                  </span>
                ) : (
                  addressList.map((address, index) => {
                    const visibleAddress = String(address.addr || '').trim() || shortPubkey(node.pubkey)
                    const copyValue = node.pubkey && visibleAddress
                      ? `${node.pubkey}@${visibleAddress}`
                      : visibleAddress
                    const copyStateKey = `node-address-${index}`
                    return (
                    <button
                      type="button"
                      key={`${address.network || 'addr'}-${address.addr || index}`}
                      className="rounded-full border border-white/10 bg-white/[0.04] px-3 py-1 text-left text-xs text-fog/75 transition hover:border-white/20 hover:bg-white/[0.06]"
                      onClick={() => void handleCopy(copyValue, copyStateKey)}
                      title={copyValue}
                    >
                      <span>{visibleAddress}</span>
                      <span className="ml-2 text-[11px] text-sky-100/80">
                        {copiedKey === copyStateKey ? t('common.copied') : t('common.copy')}
                      </span>
                    </button>
                  )})
                )}
              </div>
              {copyError && (
                <p className="mt-3 text-sm text-amber-200">{copyError}</p>
              )}
            </div>

            {activeTab === 'general' && renderGeneralTab()}
            {activeTab === 'channels' && renderChannelsTab()}
            {activeTab === 'closed' && renderClosedTab()}
            {activeTab === 'fees' && renderFeesTab()}
          </>
        )}
      </section>
    </div>
  )
}
