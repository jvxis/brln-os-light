import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getChannelRanking, getChannelRankings, recomputeChannelRankings } from '../api'
import { getLocale } from '../i18n'

type ChannelRankingReason = {
  code: string
}

type ChannelRankingRecommendation = {
  code: string
  target_module?: string
}

type ChannelRankingItem = {
  channel_point: string
  channel_id: number
  peer_pubkey?: string
  peer_alias?: string
  active: boolean
  private: boolean
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
  local_balance_pct: number
  remote_balance_pct: number
  inactive_duration_sec?: number
  pending_htlc_count?: number
  class_label?: string
  forward_fee_7d_sat: number
  forward_amt_7d_sat: number
  assisted_forward_fee_7d_sat: number
  assisted_forward_amt_7d_sat: number
  out_ppm_7d: number
  forward_fee_30d_sat: number
  forward_amt_30d_sat: number
  assisted_forward_fee_30d_sat: number
  assisted_forward_amt_30d_sat: number
  out_ppm_30d: number
  rebal_fee_7d_sat: number
  rebal_amt_7d_sat: number
  rebal_ppm_7d: number
  rebal_fee_30d_sat: number
  rebal_amt_30d_sat: number
  rebal_ppm_30d: number
  profit_fee_7d_sat: number
  profit_fee_30d_sat: number
  peer_stability_score_30d: number
  peer_sample_count_30d?: number
  htlc_failures_30d?: number
  htlc_policy_fails_30d?: number
  htlc_liquidity_fails_30d?: number
  htlc_forward_fails_30d?: number
  rebalance_dependence_score: number
  score: number
  score_7d: number
  score_30d: number
  trend_direction?: 'improving' | 'stable' | 'worsening'
  trend_delta?: number
  state: 'expand' | 'maintain' | 'monitor' | 'close'
  reasons?: ChannelRankingReason[]
  recommendations?: ChannelRankingRecommendation[]
  computed_at?: string
}

type ChannelRankingPayload = {
  available?: boolean
  last_sync_at?: string
  state_counts?: Record<string, number>
  items?: ChannelRankingItem[]
}

type ChannelRankingHistoryPoint = {
  computed_at: string
  score: number
  score_7d: number
  score_30d: number
  trend_direction?: 'improving' | 'stable' | 'worsening'
  trend_delta?: number
  state: 'expand' | 'maintain' | 'monitor' | 'close'
  profit_fee_7d_sat: number
  profit_fee_30d_sat: number
}

type ChannelRankingPeerComparison = {
  channel_point: string
  channel_id: number
  peer_alias?: string
  score: number
  score_30d: number
  trend_direction?: 'improving' | 'stable' | 'worsening'
  trend_delta?: number
  state: 'expand' | 'maintain' | 'monitor' | 'close'
  capacity_sat: number
  profit_fee_7d_sat: number
  profit_fee_30d_sat: number
}

type ChannelRankingDetailPayload = {
  item?: ChannelRankingItem
  history?: ChannelRankingHistoryPoint[]
  peer_channels?: ChannelRankingPeerComparison[]
  feedback?: ChannelRankingFeedback
}

type ChannelRankingFeedback = {
  direction?: 'improving' | 'stable' | 'worsening'
  score_delta?: number
  net_delta_sat?: number
  baseline_at?: string
  window_hours?: number
}

const CHANNEL_RANKING_ROUTE_KEY = 'channel-ranking'
const LIGHTNING_OPS_ROUTE_KEY = 'lightning-ops'
const REBALANCE_ROUTE_KEY = 'rebalance-center'
const CHANNEL_HASH_PARAM = 'channel_point'
const SECTION_HASH_PARAM = 'section'

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

const buildLightningOpsHash = (channelPoint: string, section?: string) => {
  const params = new URLSearchParams()
  if (channelPoint) {
    params.set(CHANNEL_HASH_PARAM, channelPoint)
  }
  if (section) {
    params.set(SECTION_HASH_PARAM, section)
  }
  const suffix = params.toString()
  return suffix ? `#${LIGHTNING_OPS_ROUTE_KEY}?${suffix}` : `#${LIGHTNING_OPS_ROUTE_KEY}`
}

const rankingRowID = (channelPoint: string) =>
  `channel-ranking-${channelPoint.replace(/[^a-zA-Z0-9_-]/g, '_')}`

const clamp = (value: number, min: number, max: number) => Math.max(min, Math.min(max, value))

export default function ChannelRanking() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const pctFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }), [locale])
  const dateTimeFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  )

  const [items, setItems] = useState<ChannelRankingItem[]>([])
  const [stateCounts, setStateCounts] = useState<Record<string, number>>({})
  const [available, setAvailable] = useState(true)
  const [lastSyncAt, setLastSyncAt] = useState('')
  const [status, setStatus] = useState('')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [filter, setFilter] = useState<'all' | 'expand' | 'maintain' | 'monitor' | 'close'>('all')
  const [search, setSearch] = useState('')
  const [activityFilter, setActivityFilter] = useState<'all' | 'active' | 'inactive'>('all')
  const [visibilityFilter, setVisibilityFilter] = useState<'all' | 'public' | 'private'>('all')
  const [recommendationFilter, setRecommendationFilter] = useState('all')
  const [sortBy, setSortBy] = useState<'score' | 'net_7d' | 'net_30d' | 'capital_efficiency' | 'rebalance_cost' | 'risk' | 'peer_stability' | 'htlc_failures' | 'rebalance_dependence'>('score')
  const [selectedChannelPoint, setSelectedChannelPoint] = useState('')
  const [focusedChannelPoint, setFocusedChannelPoint] = useState('')
  const [detailItem, setDetailItem] = useState<ChannelRankingItem | null>(null)
  const [detailHistory, setDetailHistory] = useState<ChannelRankingHistoryPoint[]>([])
  const [detailPeerChannels, setDetailPeerChannels] = useState<ChannelRankingPeerComparison[]>([])
  const [detailFeedback, setDetailFeedback] = useState<ChannelRankingFeedback | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const pendingScrollChannelRef = useRef('')
  const focusClearTimerRef = useRef<number | null>(null)

  const stateLabel = (value?: string) =>
    t(`channelRanking.states.${String(value || '').trim() || 'monitor'}` as any, {
      defaultValue: value || t('common.unknown')
    })

  const stateBadgeClass = (value?: string) => {
    switch (String(value || '').trim()) {
      case 'expand':
        return 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
      case 'maintain':
        return 'bg-sky-500/15 text-sky-100 border border-sky-300/30'
      case 'close':
        return 'bg-rose-500/15 text-rose-100 border border-rose-400/30'
      default:
        return 'bg-amber-500/15 text-amber-100 border border-amber-300/30'
    }
  }

  const trendLabel = (value?: string) =>
    t(`channelRanking.trends.${String(value || '').trim() || 'stable'}` as any, {
      defaultValue: t('channelRanking.trends.stable')
    })

  const trendBadgeClass = (value?: string) => {
    switch (String(value || '').trim()) {
      case 'improving':
        return 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200'
      case 'worsening':
        return 'border-rose-400/30 bg-rose-500/15 text-rose-100'
      default:
        return 'border-white/10 bg-white/[0.03] text-fog/70'
    }
  }

  const formatSats = (value?: number) => `${numberFormatter.format(Math.round(Number(value || 0)))} sats`
  const formatPct = (value?: number) => `${pctFormatter.format(clamp(Number(value || 0), 0, 100))}%`
  const formatTimestamp = (value?: string) => {
    if (!value) return t('common.na')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return value
    return dateTimeFormatter.format(parsed)
  }

  const formatDuration = (value?: number) => {
    const totalSec = Math.max(0, Math.floor(Number(value || 0)))
    if (totalSec <= 0) return t('common.na')
    if (totalSec >= 86400) return t('channelRanking.durationDays', { count: Math.floor(totalSec / 86400) })
    if (totalSec >= 3600) return t('channelRanking.durationHours', { count: Math.floor(totalSec / 3600) })
    if (totalSec >= 60) return t('channelRanking.durationMinutes', { count: Math.floor(totalSec / 60) })
    return t('channelRanking.durationSeconds', { count: totalSec })
  }

  const reasonLabel = (code?: string) =>
    t(`channelRanking.reasons.${String(code || '').trim()}` as any, { defaultValue: code || t('common.unknown') })

  const recommendationLabel = (code?: string) =>
    t(`channelRanking.recommendations.${String(code || '').trim()}` as any, {
      defaultValue: code || t('common.unknown')
    })

  const targetModuleLabel = (value?: string) =>
    t(`channelRanking.targetModules.${String(value || '').trim()}` as any, {
      defaultValue: value || t('common.na')
    })

  const buildModuleLink = (item: ChannelRankingItem, targetModule?: string) => {
    switch (String(targetModule || '').trim()) {
      case 'rebalance':
        return buildHashWithChannelPoint(REBALANCE_ROUTE_KEY, item.channel_point)
      case 'autofee':
        return buildLightningOpsHash(item.channel_point, 'autofee')
      case 'close-manager':
        return buildLightningOpsHash(item.channel_point, 'close_recovery')
      case 'htlc-manager':
        return buildLightningOpsHash(item.channel_point, 'htlc_manager')
      default:
        return buildLightningOpsHash(item.channel_point)
    }
  }

  const load = async (options: { recompute?: boolean } = {}) => {
    const recompute = options.recompute === true
    if (recompute) {
      setRefreshing(true)
    } else {
      setLoading(true)
      setStatus(t('channelRanking.loading'))
    }
    try {
      if (recompute) {
        await recomputeChannelRankings()
      }
      const payload = await getChannelRankings({ limit: 500 }) as ChannelRankingPayload
      const nextItems = Array.isArray(payload?.items) ? payload.items : []
      setItems(nextItems)
      setStateCounts(payload?.state_counts || {})
      setAvailable(payload?.available !== false)
      setLastSyncAt(String(payload?.last_sync_at || ''))
      setStatus('')
      setSelectedChannelPoint((current) => {
        if (pendingScrollChannelRef.current) return pendingScrollChannelRef.current
        if (current && nextItems.some((item) => item.channel_point === current)) return current
        return nextItems[0]?.channel_point || ''
      })
    } catch (err: any) {
      setStatus(err?.message || t('channelRanking.unavailable'))
      setItems([])
      setStateCounts({})
      setAvailable(false)
      setDetailItem(null)
      setDetailHistory([])
      setDetailPeerChannels([])
      setDetailFeedback(null)
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => {
    pendingScrollChannelRef.current = readHashChannelPoint(CHANNEL_RANKING_ROUTE_KEY)
    const handleHashChange = () => {
      const next = readHashChannelPoint(CHANNEL_RANKING_ROUTE_KEY)
      pendingScrollChannelRef.current = next
      if (next) {
        setSelectedChannelPoint(next)
      }
    }
    window.addEventListener('hashchange', handleHashChange)
    void load()
    return () => {
      window.removeEventListener('hashchange', handleHashChange)
      if (focusClearTimerRef.current !== null) {
        window.clearTimeout(focusClearTimerRef.current)
      }
    }
  }, [])

  const filteredItems = useMemo(() => {
    const query = search.trim().toLowerCase()
    return items.filter((item) => {
      if (filter !== 'all' && item.state !== filter) return false
      if (activityFilter === 'active' && !item.active) return false
      if (activityFilter === 'inactive' && item.active) return false
      if (visibilityFilter === 'public' && item.private) return false
      if (visibilityFilter === 'private' && !item.private) return false
      if (recommendationFilter !== 'all') {
        const matchesRecommendation = (item.recommendations || []).some((rec) => rec.code === recommendationFilter)
        if (!matchesRecommendation) return false
      }
      if (!query) return true
      return (
        String(item.peer_alias || '').toLowerCase().includes(query) ||
        String(item.peer_pubkey || '').toLowerCase().includes(query) ||
        String(item.channel_point || '').toLowerCase().includes(query)
      )
    })
  }, [activityFilter, filter, items, recommendationFilter, search, visibilityFilter])

  const sortedItems = useMemo(() => {
    const list = [...filteredItems]
    const riskScore = (item: ChannelRankingItem) => {
      let total = 0
      if (!item.active) total += 30
      if ((item.pending_htlc_count || 0) > 0) total += Math.min(20, (item.pending_htlc_count || 0) * 4)
      if (item.profit_fee_7d_sat < 0) total += 25
      if (item.rebal_fee_7d_sat > item.forward_fee_7d_sat) total += 15
      if (item.local_balance_pct < 10 || item.local_balance_pct > 90) total += 10
      if (item.trend_direction === 'worsening') total += 10
      return total
    }
    list.sort((left, right) => {
      switch (sortBy) {
        case 'net_7d':
          return right.profit_fee_7d_sat - left.profit_fee_7d_sat || right.score - left.score
        case 'net_30d':
          return right.profit_fee_30d_sat - left.profit_fee_30d_sat || right.score - left.score
        case 'capital_efficiency': {
          const leftEfficiency = left.capacity_sat > 0 ? left.profit_fee_7d_sat / left.capacity_sat : 0
          const rightEfficiency = right.capacity_sat > 0 ? right.profit_fee_7d_sat / right.capacity_sat : 0
          return rightEfficiency - leftEfficiency || right.score - left.score
        }
        case 'rebalance_cost':
          return right.rebal_fee_7d_sat - left.rebal_fee_7d_sat || right.score - left.score
        case 'peer_stability':
          return right.peer_stability_score_30d - left.peer_stability_score_30d || right.score - left.score
        case 'htlc_failures':
          return (right.htlc_failures_30d || 0) - (left.htlc_failures_30d || 0) || right.score - left.score
        case 'rebalance_dependence':
          return right.rebalance_dependence_score - left.rebalance_dependence_score || right.score - left.score
        case 'risk':
          return riskScore(right) - riskScore(left) || right.score - left.score
        default:
          return right.score - left.score || right.profit_fee_7d_sat - left.profit_fee_7d_sat
      }
    })
    return list
  }, [filteredItems, sortBy])

  const recommendationOptions = useMemo(() => {
    const seen = new Set<string>()
    const options: string[] = []
    items.forEach((item) => {
      ;(item.recommendations || []).forEach((rec) => {
        const code = String(rec.code || '').trim()
        if (!code || seen.has(code)) return
        seen.add(code)
        options.push(code)
      })
    })
    return options
  }, [items])

  const selectedItem = useMemo(() => {
    if (selectedChannelPoint) {
      const selected = sortedItems.find((item) => item.channel_point === selectedChannelPoint) || items.find((item) => item.channel_point === selectedChannelPoint)
      if (selected) return selected
    }
    return sortedItems[0] || items[0] || null
  }, [items, selectedChannelPoint, sortedItems])

  const selectedDetail = detailItem && detailItem.channel_point === selectedChannelPoint ? detailItem : selectedItem

  const displayedDetailHistory = useMemo(() => {
    if (!Array.isArray(detailHistory) || detailHistory.length === 0) return []
    const compressed: ChannelRankingHistoryPoint[] = []
    for (const point of detailHistory) {
      const previous = compressed[compressed.length - 1]
      if (previous && previous.score === point.score) {
        continue
      }
      compressed.push(point)
    }
    return compressed
  }, [detailHistory])

  const stateCapacity = useMemo(() => {
    return items.reduce<Record<string, number>>((acc, item) => {
      acc[item.state] = (acc[item.state] || 0) + Number(item.capacity_sat || 0)
      return acc
    }, {})
  }, [items])

  const trendCounts = useMemo(() => {
    return items.reduce<Record<string, number>>((acc, item) => {
      const key = item.trend_direction || 'stable'
      acc[key] = (acc[key] || 0) + 1
      return acc
    }, {})
  }, [items])

  useEffect(() => {
    if (!selectedItem) return
    if (selectedChannelPoint === selectedItem.channel_point) return
    setSelectedChannelPoint(selectedItem.channel_point)
  }, [selectedChannelPoint, selectedItem])

  useEffect(() => {
    if (!selectedChannelPoint) {
      setDetailItem(null)
      setDetailHistory([])
      setDetailPeerChannels([])
      setDetailFeedback(null)
      return
    }
    let active = true
    setDetailLoading(true)
    getChannelRanking(selectedChannelPoint)
      .then((payload: any) => {
        if (!active) return
        const detail = payload as ChannelRankingDetailPayload
      setDetailItem((detail?.item as ChannelRankingItem) || null)
      setDetailHistory(Array.isArray(detail?.history) ? detail.history : [])
      setDetailPeerChannels(Array.isArray(detail?.peer_channels) ? detail.peer_channels : [])
      setDetailFeedback((detail?.feedback as ChannelRankingFeedback) || null)
      })
      .catch(() => {
        if (!active) return
        const fallback = items.find((item) => item.channel_point === selectedChannelPoint) || null
        setDetailItem(fallback)
        setDetailHistory([])
        setDetailPeerChannels([])
        setDetailFeedback(null)
      })
      .finally(() => {
        if (!active) return
        setDetailLoading(false)
      })
    return () => {
      active = false
    }
  }, [items, selectedChannelPoint])

  useEffect(() => {
    const targetChannelPoint = pendingScrollChannelRef.current || selectedChannelPoint
    if (!targetChannelPoint) return
    const targetExists = items.some((item) => item.channel_point === targetChannelPoint)
    if (!targetExists) return
    const targetElement = document.getElementById(rankingRowID(targetChannelPoint))
    if (!targetElement) return
    targetElement.scrollIntoView({ behavior: 'smooth', block: 'center' })
    setFocusedChannelPoint(targetChannelPoint)
    pendingScrollChannelRef.current = ''
    if (focusClearTimerRef.current !== null) {
      window.clearTimeout(focusClearTimerRef.current)
    }
    focusClearTimerRef.current = window.setTimeout(() => {
      setFocusedChannelPoint((current) => (current === targetChannelPoint ? '' : current))
      focusClearTimerRef.current = null
    }, 3200)
  }, [items, selectedChannelPoint])

  const summaryCount = (state: 'expand' | 'maintain' | 'monitor' | 'close') =>
    Number(stateCounts[state] || items.filter((item) => item.state === state).length)

  const filters: Array<{ key: 'all' | 'expand' | 'maintain' | 'monitor' | 'close'; count: number }> = [
    { key: 'all', count: items.length },
    { key: 'expand', count: summaryCount('expand') },
    { key: 'maintain', count: summaryCount('maintain') },
    { key: 'monitor', count: summaryCount('monitor') },
    { key: 'close', count: summaryCount('close') }
  ]

  return (
    <div className="space-y-6">
      <section className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold">{t('channelRanking.title')}</h2>
          <p className="text-sm text-fog/70">{t('channelRanking.subtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <div className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/70">
            {t('channelRanking.lastSync', { value: lastSyncAt ? formatTimestamp(lastSyncAt) : t('common.unavailable') })}
          </div>
          <button
            type="button"
            className={`btn-secondary ${refreshing ? 'opacity-60 pointer-events-none' : ''}`}
            onClick={() => void load({ recompute: true })}
            disabled={refreshing}
          >
            {refreshing ? t('channelRanking.refreshing') : t('common.refresh')}
          </button>
        </div>
      </section>

      {status && (
        <div className="rounded-2xl border border-rose-400/35 bg-rose-500/10 px-4 py-3 text-sm text-rose-100">
          {status}
        </div>
      )}

      {!available && !status && (
        <div className="rounded-2xl border border-amber-400/35 bg-amber-500/10 px-4 py-3 text-sm text-amber-100">
          {t('channelRanking.unavailable')}
        </div>
      )}

      <section className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
        <div className="flex flex-wrap items-center gap-2">
          {filters.map((entry) => {
            const active = filter === entry.key
            return (
              <button
                key={entry.key}
                type="button"
                className={active ? 'btn-primary' : 'btn-secondary'}
                onClick={() => setFilter(entry.key)}
              >
                {entry.key === 'all'
                  ? t('common.all')
                  : stateLabel(entry.key)}
                : {numberFormatter.format(entry.count)}
              </button>
            )
          })}
        </div>
        <div className="mt-4 grid gap-3 lg:grid-cols-[minmax(0,1.2fr)_repeat(4,minmax(0,1fr))]">
          <label className="text-sm text-fog/70">
            {t('channelRanking.searchLabel')}
            <input
              className="input-field mt-2"
              type="text"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t('channelRanking.searchPlaceholder')}
            />
          </label>
          <label className="text-sm text-fog/70">
            {t('channelRanking.activityFilterLabel')}
            <select className="input-field mt-2" value={activityFilter} onChange={(event) => setActivityFilter(event.target.value as any)}>
              <option value="all">{t('common.all')}</option>
              <option value="active">{t('common.active')}</option>
              <option value="inactive">{t('common.inactive')}</option>
            </select>
          </label>
          <label className="text-sm text-fog/70">
            {t('channelRanking.visibilityFilterLabel')}
            <select className="input-field mt-2" value={visibilityFilter} onChange={(event) => setVisibilityFilter(event.target.value as any)}>
              <option value="all">{t('common.all')}</option>
              <option value="public">{t('channelRanking.publicOnly')}</option>
              <option value="private">{t('channelRanking.privateOnly')}</option>
            </select>
          </label>
          <label className="text-sm text-fog/70">
            {t('channelRanking.recommendationFilterLabel')}
            <select className="input-field mt-2" value={recommendationFilter} onChange={(event) => setRecommendationFilter(event.target.value)}>
              <option value="all">{t('common.all')}</option>
              {recommendationOptions.map((code) => (
                <option key={code} value={code}>
                  {recommendationLabel(code)}
                </option>
              ))}
            </select>
          </label>
          <label className="text-sm text-fog/70">
            {t('channelRanking.sortLabel')}
            <select className="input-field mt-2" value={sortBy} onChange={(event) => setSortBy(event.target.value as any)}>
              <option value="score">{t('channelRanking.sorts.score')}</option>
              <option value="net_7d">{t('channelRanking.sorts.net7d')}</option>
              <option value="net_30d">{t('channelRanking.sorts.net30d')}</option>
              <option value="capital_efficiency">{t('channelRanking.sorts.capitalEfficiency')}</option>
              <option value="rebalance_cost">{t('channelRanking.sorts.rebalanceCost')}</option>
              <option value="peer_stability">{t('channelRanking.sorts.peerStability')}</option>
              <option value="htlc_failures">{t('channelRanking.sorts.htlcFailures')}</option>
              <option value="rebalance_dependence">{t('channelRanking.sorts.rebalanceDependence')}</option>
              <option value="risk">{t('channelRanking.sorts.risk')}</option>
            </select>
          </label>
        </div>
      </section>

      <section className="grid gap-3 lg:grid-cols-3">
        <div className="rounded-3xl border border-white/10 bg-ink/60 p-4">
          <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.summaryCapitalTitle')}</div>
          <div className="mt-2 space-y-1 text-sm text-fog/80">
            <div>{t('channelRanking.summaryCapitalExpand', { value: formatSats(stateCapacity.expand) })}</div>
            <div>{t('channelRanking.summaryCapitalMaintain', { value: formatSats(stateCapacity.maintain) })}</div>
            <div>{t('channelRanking.summaryCapitalMonitor', { value: formatSats(stateCapacity.monitor) })}</div>
            <div>{t('channelRanking.summaryCapitalClose', { value: formatSats(stateCapacity.close) })}</div>
          </div>
        </div>
        <div className="rounded-3xl border border-white/10 bg-ink/60 p-4">
          <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.summaryTrendTitle')}</div>
          <div className="mt-2 space-y-1 text-sm text-fog/80">
            <div>{t('channelRanking.summaryTrendImproving', { count: trendCounts.improving || 0 })}</div>
            <div>{t('channelRanking.summaryTrendStable', { count: trendCounts.stable || 0 })}</div>
            <div>{t('channelRanking.summaryTrendWorsening', { count: trendCounts.worsening || 0 })}</div>
          </div>
        </div>
        <div className="rounded-3xl border border-white/10 bg-ink/60 p-4">
          <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.summaryOpportunityTitle')}</div>
          <div className="mt-2 space-y-1 text-sm text-fog/80">
            {sortedItems.slice(0, 3).map((item) => (
              <div key={item.channel_point} className="truncate">
                {(item.peer_alias || item.channel_point)} · {t('channelRanking.scoreShort', { value: numberFormatter.format(item.score) })}
              </div>
            ))}
            {sortedItems.length === 0 && <div>{t('common.na')}</div>}
          </div>
        </div>
      </section>

      <div className="space-y-6">
        <section className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
          <div className="mb-4 flex items-center justify-between gap-3">
            <h3 className="text-lg font-semibold">{t('channelRanking.listTitle')}</h3>
            <div className="text-xs text-fog/60">{t('channelRanking.listCount', { count: sortedItems.length })}</div>
          </div>
          {loading ? (
            <p className="text-sm text-fog/70">{t('channelRanking.loading')}</p>
          ) : sortedItems.length === 0 ? (
            <p className="text-sm text-fog/70">{t('channelRanking.empty')}</p>
          ) : (
            <div className="lg:h-[46rem] lg:min-h-[24rem] lg:max-h-[80vh] lg:resize-y lg:overflow-y-auto lg:pr-2">
              <div className="space-y-3">
                {sortedItems.map((item) => {
                  const isSelected = selectedItem?.channel_point === item.channel_point
                  const isFocused = focusedChannelPoint === item.channel_point
                  return (
                    <button
                      key={item.channel_point}
                      id={rankingRowID(item.channel_point)}
                      type="button"
                      onClick={() => {
                        setSelectedChannelPoint(item.channel_point)
                        window.history.replaceState(null, '', buildHashWithChannelPoint(CHANNEL_RANKING_ROUTE_KEY, item.channel_point))
                      }}
                      className={`w-full rounded-2xl border p-4 text-left transition ${
                        isSelected
                          ? 'border-sky-300/60 bg-sky-500/10'
                          : 'border-white/10 bg-white/[0.03] hover:border-white/20 hover:bg-white/[0.05]'
                      } ${isFocused ? 'ring-1 ring-sky-300/70' : ''}`}
                    >
                      <div className="flex flex-wrap items-start justify-between gap-3">
                        <div className="min-w-0 space-y-1">
                          <div className="truncate text-sm font-medium text-fog">{item.peer_alias || item.peer_pubkey || item.channel_point}</div>
                          <div className="break-all text-[11px] text-fog/55">{item.channel_point}</div>
                        </div>
                        <div className="flex flex-wrap items-center gap-2">
                          <span className={`rounded-full px-2.5 py-1 text-[11px] ${stateBadgeClass(item.state)}`}>
                            {stateLabel(item.state)}
                          </span>
                          <span className={`rounded-full border px-2.5 py-1 text-[11px] ${trendBadgeClass(item.trend_direction)}`}>
                            {trendLabel(item.trend_direction)}
                          </span>
                          <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-fog/75">
                            {t('channelRanking.scoreShort', { value: numberFormatter.format(item.score) })}
                          </span>
                        </div>
                      </div>
                      <div className="mt-3 grid gap-2 text-xs text-fog/70 sm:grid-cols-2 xl:grid-cols-6">
                        <div>{t('channelRanking.netFees7d', { value: formatSats(item.profit_fee_7d_sat) })}</div>
                        <div>{t('channelRanking.netFees30d', { value: formatSats(item.profit_fee_30d_sat) })}</div>
                        <div>{t('channelRanking.capacity', { value: formatSats(item.capacity_sat) })}</div>
                        <div>{t('channelRanking.localBalancePct', { value: formatPct(item.local_balance_pct) })}</div>
                        <div>{t('channelRanking.peerStabilityScore30d', { value: numberFormatter.format(item.peer_stability_score_30d || 0) })}</div>
                        <div>{t('channelRanking.htlcFailures30dLabel', { value: numberFormatter.format(item.htlc_failures_30d || 0) })}</div>
                        <div>{t('channelRanking.trendDelta', { value: numberFormatter.format(item.trend_delta || 0) })}</div>
                      </div>
                    </button>
                  )
                })}
              </div>
            </div>
          )}
        </section>

        <aside className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
          <div className="mb-4 flex items-center justify-between gap-3">
            <h3 className="text-lg font-semibold">{t('channelRanking.detailTitle')}</h3>
            {selectedDetail && (
              <a
                href={buildHashWithChannelPoint(LIGHTNING_OPS_ROUTE_KEY, selectedDetail.channel_point)}
                className="text-xs text-sky-200 hover:text-sky-100"
              >
                {t('channelRanking.openInLightningOps')}
              </a>
            )}
          </div>

          {!selectedDetail ? (
            <p className="text-sm text-fog/70">{t('channelRanking.selectChannel')}</p>
          ) : (
            <div className="space-y-5 lg:max-h-[46rem] lg:overflow-y-auto lg:pr-2">
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={`rounded-full px-3 py-1 text-xs ${stateBadgeClass(selectedDetail.state)}`}>
                    {stateLabel(selectedDetail.state)}
                  </span>
                  <span className={`rounded-full border px-3 py-1 text-xs ${trendBadgeClass(selectedDetail.trend_direction)}`}>
                    {trendLabel(selectedDetail.trend_direction)}
                  </span>
                  <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/75">
                    {t('channelRanking.score', { value: numberFormatter.format(selectedDetail.score) })}
                  </span>
                  <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/75">
                    {t('channelRanking.score7dVs30d', {
                      score7d: numberFormatter.format(selectedDetail.score_7d || selectedDetail.score),
                      score30d: numberFormatter.format(selectedDetail.score_30d || 0)
                    })}
                  </span>
                  {selectedDetail.class_label && (
                    <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/75">
                      {selectedDetail.class_label}
                    </span>
                  )}
                </div>
                <div className="text-base font-medium text-fog">
                  {selectedDetail.peer_alias || selectedDetail.peer_pubkey || selectedDetail.channel_point}
                </div>
                <div className="break-all text-xs text-fog/55">{selectedDetail.channel_point}</div>
              </div>

              <div className="grid gap-3 text-sm sm:grid-cols-2">
                <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                  <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.metricsTitle')}</div>
                  <div className="mt-2 space-y-1 text-fog/80">
                    <div>{t('channelRanking.capacity', { value: formatSats(selectedDetail.capacity_sat) })}</div>
                    <div>{t('channelRanking.localBalance', { value: formatSats(selectedDetail.local_balance_sat) })}</div>
                    <div>{t('channelRanking.remoteBalance', { value: formatSats(selectedDetail.remote_balance_sat) })}</div>
                    <div>{t('channelRanking.localBalancePct', { value: formatPct(selectedDetail.local_balance_pct) })}</div>
                    <div>{t('channelRanking.remoteBalancePct', { value: formatPct(selectedDetail.remote_balance_pct) })}</div>
                    <div>{t('channelRanking.pendingHtlcCount', { value: numberFormatter.format(selectedDetail.pending_htlc_count || 0) })}</div>
                    <div>{t('channelRanking.inactiveDuration', { value: formatDuration(selectedDetail.inactive_duration_sec) })}</div>
                    <div>{t('channelRanking.peerStabilityScore30d', { value: numberFormatter.format(selectedDetail.peer_stability_score_30d || 0) })}</div>
                  </div>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                  <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.economicsTitle')}</div>
                  <div className="mt-2 grid gap-3 lg:grid-cols-2">
                    <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                      <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.window7dTitle')}</div>
                      <div className="mt-2 space-y-1 text-fog/80">
                        <div>{t('channelRanking.forwardFees7d', { value: formatSats(selectedDetail.forward_fee_7d_sat) })}</div>
                        <div>{t('channelRanking.forwardAmount7d', { value: formatSats(selectedDetail.forward_amt_7d_sat) })}</div>
                        <div>{t('channelRanking.assistedForwardFees7d', { value: formatSats(selectedDetail.assisted_forward_fee_7d_sat) })}</div>
                        <div>{t('channelRanking.assistedForwardAmount7d', { value: formatSats(selectedDetail.assisted_forward_amt_7d_sat) })}</div>
                        <div>{t('channelRanking.outPpm7d', { value: numberFormatter.format(selectedDetail.out_ppm_7d || 0) })}</div>
                        <div>{t('channelRanking.rebalanceFees7d', { value: formatSats(selectedDetail.rebal_fee_7d_sat) })}</div>
                        <div>{t('channelRanking.rebalanceAmount7d', { value: formatSats(selectedDetail.rebal_amt_7d_sat) })}</div>
                        <div>{t('channelRanking.rebalancePpm7d', { value: numberFormatter.format(selectedDetail.rebal_ppm_7d || 0) })}</div>
                        <div>{t('channelRanking.netFees7d', { value: formatSats(selectedDetail.profit_fee_7d_sat) })}</div>
                      </div>
                    </div>
                    <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                      <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.window30dTitle')}</div>
                      <div className="mt-2 space-y-1 text-fog/80">
                        <div>{t('channelRanking.forwardFees30d', { value: formatSats(selectedDetail.forward_fee_30d_sat) })}</div>
                        <div>{t('channelRanking.forwardAmount30d', { value: formatSats(selectedDetail.forward_amt_30d_sat) })}</div>
                        <div>{t('channelRanking.assistedForwardFees30d', { value: formatSats(selectedDetail.assisted_forward_fee_30d_sat) })}</div>
                        <div>{t('channelRanking.assistedForwardAmount30d', { value: formatSats(selectedDetail.assisted_forward_amt_30d_sat) })}</div>
                        <div>{t('channelRanking.outPpm30d', { value: numberFormatter.format(selectedDetail.out_ppm_30d || 0) })}</div>
                        <div>{t('channelRanking.rebalanceFees30d', { value: formatSats(selectedDetail.rebal_fee_30d_sat) })}</div>
                        <div>{t('channelRanking.rebalanceAmount30d', { value: formatSats(selectedDetail.rebal_amt_30d_sat) })}</div>
                        <div>{t('channelRanking.rebalancePpm30d', { value: numberFormatter.format(selectedDetail.rebal_ppm_30d || 0) })}</div>
                        <div>{t('channelRanking.netFees30d', { value: formatSats(selectedDetail.profit_fee_30d_sat) })}</div>
                      </div>
                    </div>
                    <div className="lg:col-span-2 text-fog/80">
                      <div>{t('channelRanking.computedAt', { value: formatTimestamp(selectedDetail.computed_at) })}</div>
                    </div>
                  </div>
                </div>
              </div>

              <div className="grid gap-3 lg:grid-cols-2">
                <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                  <div className="mb-2 text-sm font-medium text-fog">{t('channelRanking.operationalSignalsTitle')}</div>
                  <div className="space-y-1 text-sm text-fog/80">
                    <div>{t('channelRanking.peerStabilityScore30d', { value: numberFormatter.format(selectedDetail.peer_stability_score_30d || 0) })}</div>
                    <div>{t('channelRanking.peerSampleCount30d', { value: numberFormatter.format(selectedDetail.peer_sample_count_30d || 0) })}</div>
                    <div>{t('channelRanking.htlcFailures30dLabel', { value: numberFormatter.format(selectedDetail.htlc_failures_30d || 0) })}</div>
                    <div>{t('channelRanking.htlcPolicyFails30d', { value: numberFormatter.format(selectedDetail.htlc_policy_fails_30d || 0) })}</div>
                    <div>{t('channelRanking.htlcLiquidityFails30d', { value: numberFormatter.format(selectedDetail.htlc_liquidity_fails_30d || 0) })}</div>
                    <div>{t('channelRanking.htlcForwardFails30d', { value: numberFormatter.format(selectedDetail.htlc_forward_fails_30d || 0) })}</div>
                    <div>{t('channelRanking.rebalanceDependenceScore', { value: numberFormatter.format(selectedDetail.rebalance_dependence_score || 0) })}</div>
                  </div>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                  <div className="mb-2 text-sm font-medium text-fog">{t('channelRanking.trendSectionTitle')}</div>
                  <div className="space-y-1 text-sm text-fog/80">
                    <div>{t('channelRanking.score7dLabel', { value: numberFormatter.format(selectedDetail.score_7d || selectedDetail.score) })}</div>
                    <div>{t('channelRanking.score30dLabel', { value: numberFormatter.format(selectedDetail.score_30d || 0) })}</div>
                    <div>{t('channelRanking.trendDirectionLabel', { value: trendLabel(selectedDetail.trend_direction) })}</div>
                    <div>{t('channelRanking.trendDelta', { value: numberFormatter.format(selectedDetail.trend_delta || 0) })}</div>
                  </div>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                  <div className="mb-2 text-sm font-medium text-fog">{t('channelRanking.feedbackTitle')}</div>
                  {detailFeedback ? (
                    <div className="space-y-1 text-sm text-fog/80">
                      <div>{t('channelRanking.feedbackDirectionLabel', { value: trendLabel(detailFeedback.direction) })}</div>
                      <div>{t('channelRanking.feedbackScoreDeltaLabel', { value: numberFormatter.format(detailFeedback.score_delta || 0) })}</div>
                      <div>{t('channelRanking.feedbackNetDeltaLabel', { value: formatSats(detailFeedback.net_delta_sat) })}</div>
                      <div>{t('channelRanking.feedbackWindowLabel', { value: numberFormatter.format(detailFeedback.window_hours || 0) })}</div>
                      {detailFeedback.baseline_at && (
                        <div>{t('channelRanking.feedbackBaselineAtLabel', { value: formatTimestamp(detailFeedback.baseline_at) })}</div>
                      )}
                    </div>
                  ) : (
                    <div className="text-sm text-fog/60">{t('channelRanking.feedbackEmpty')}</div>
                  )}
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                  <div className="mb-2 text-sm font-medium text-fog">{t('channelRanking.historyTitle')}</div>
                  <div className="space-y-2 max-h-[17rem] overflow-y-auto pr-1">
                    {detailLoading ? (
                      <div className="text-sm text-fog/60">{t('channelRanking.loading')}</div>
                    ) : displayedDetailHistory.length > 0 ? (
                      displayedDetailHistory.slice(0, 8).map((point) => (
                        <div key={`${selectedDetail.channel_point}-${point.computed_at}`} className="rounded-xl border border-white/10 px-3 py-2 text-xs text-fog/75">
                          <div className="flex items-center justify-between gap-3">
                            <span>{formatTimestamp(point.computed_at)}</span>
                            <span className={`rounded-full border px-2 py-0.5 ${trendBadgeClass(point.trend_direction)}`}>
                              {trendLabel(point.trend_direction)}
                            </span>
                          </div>
                          <div className="mt-1 flex flex-wrap gap-3">
                            <span>{t('channelRanking.scoreShort', { value: numberFormatter.format(point.score) })}</span>
                            <span>{t('channelRanking.netFees7d', { value: formatSats(point.profit_fee_7d_sat) })}</span>
                          </div>
                        </div>
                      ))
                    ) : (
                      <div className="text-sm text-fog/60">{t('channelRanking.historyEmpty')}</div>
                    )}
                  </div>
                </div>
              </div>

              <div className="space-y-3">
                <div>
                  <div className="mb-2 text-sm font-medium text-fog">{t('channelRanking.reasonsTitle')}</div>
                  <div className="flex flex-wrap gap-2">
                    {(selectedDetail.reasons || []).length > 0 ? (
                      selectedDetail.reasons!.map((reason) => (
                        <span
                          key={reason.code}
                          className="rounded-full border border-white/10 bg-white/[0.03] px-2.5 py-1 text-xs text-fog/75"
                        >
                          {reasonLabel(reason.code)}
                        </span>
                      ))
                    ) : (
                      <span className="text-sm text-fog/60">{t('common.na')}</span>
                    )}
                  </div>
                </div>

                <div>
                  <div className="mb-2 text-sm font-medium text-fog">{t('channelRanking.recommendationsTitle')}</div>
                  <div className="space-y-2">
                    {(selectedDetail.recommendations || []).length > 0 ? (
                      selectedDetail.recommendations!.map((item) => (
                        <a
                          key={`${item.code}-${item.target_module || ''}`}
                          href={buildModuleLink(selectedDetail, item.target_module)}
                          className="flex items-start justify-between gap-3 rounded-2xl border border-white/10 bg-white/[0.03] px-3 py-2 text-sm text-fog/80 transition hover:border-white/20 hover:bg-white/[0.05]"
                        >
                          <span>{recommendationLabel(item.code)}</span>
                          <span className="shrink-0 text-[11px] text-sky-200">{targetModuleLabel(item.target_module)}</span>
                        </a>
                      ))
                    ) : (
                      <span className="text-sm text-fog/60">{t('common.na')}</span>
                    )}
                  </div>
                </div>

                <div>
                  <div className="mb-2 text-sm font-medium text-fog">{t('channelRanking.peerComparisonTitle')}</div>
                  <div className="space-y-2">
                    {detailLoading ? (
                      <div className="text-sm text-fog/60">{t('channelRanking.loading')}</div>
                    ) : detailPeerChannels.length > 1 ? (
                      detailPeerChannels
                        .filter((item) => item.channel_point !== selectedDetail.channel_point)
                        .map((item) => (
                          <button
                            key={item.channel_point}
                            type="button"
                            onClick={() => {
                              setSelectedChannelPoint(item.channel_point)
                              window.history.replaceState(null, '', buildHashWithChannelPoint(CHANNEL_RANKING_ROUTE_KEY, item.channel_point))
                            }}
                            className="flex w-full items-start justify-between gap-3 rounded-2xl border border-white/10 bg-white/[0.03] px-3 py-2 text-left text-sm text-fog/80 transition hover:border-white/20 hover:bg-white/[0.05]"
                          >
                            <div className="min-w-0">
                              <div className="truncate">{item.peer_alias || item.channel_point}</div>
                              <div className="mt-1 text-xs text-fog/60">
                                {t('channelRanking.peerComparisonSummary', {
                                  score: numberFormatter.format(item.score),
                                  net7d: formatSats(item.profit_fee_7d_sat),
                                  capacity: formatSats(item.capacity_sat)
                                })}
                              </div>
                            </div>
                            <div className="flex flex-wrap items-center justify-end gap-2">
                              <span className={`rounded-full px-2 py-0.5 text-[11px] ${stateBadgeClass(item.state)}`}>
                                {stateLabel(item.state)}
                              </span>
                              <span className={`rounded-full border px-2 py-0.5 text-[11px] ${trendBadgeClass(item.trend_direction)}`}>
                                {trendLabel(item.trend_direction)}
                              </span>
                            </div>
                          </button>
                        ))
                    ) : (
                      <div className="text-sm text-fog/60">{t('channelRanking.peerComparisonEmpty')}</div>
                    )}
                  </div>
                </div>
              </div>
            </div>
          )}
        </aside>
      </div>
    </div>
  )
}
