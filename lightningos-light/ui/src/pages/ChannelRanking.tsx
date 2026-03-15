import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getChannelRankings, recomputeChannelRankings } from '../api'
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
  out_ppm_7d: number
  rebal_fee_7d_sat: number
  rebal_amt_7d_sat: number
  rebal_ppm_7d: number
  profit_fee_7d_sat: number
  score: number
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

const CHANNEL_RANKING_ROUTE_KEY = 'channel-ranking'
const LIGHTNING_OPS_ROUTE_KEY = 'lightning-ops'
const REBALANCE_ROUTE_KEY = 'rebalance-center'
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
  const [selectedChannelPoint, setSelectedChannelPoint] = useState('')
  const [focusedChannelPoint, setFocusedChannelPoint] = useState('')
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

  const formatSats = (value?: number) => `${numberFormatter.format(Math.max(0, Math.round(Number(value || 0))))} sats`
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
      default:
        return buildHashWithChannelPoint(LIGHTNING_OPS_ROUTE_KEY, item.channel_point)
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
    if (filter === 'all') return items
    return items.filter((item) => item.state === filter)
  }, [filter, items])

  const selectedItem = useMemo(() => {
    if (selectedChannelPoint) {
      const selected = items.find((item) => item.channel_point === selectedChannelPoint)
      if (selected) return selected
    }
    return filteredItems[0] || items[0] || null
  }, [filteredItems, items, selectedChannelPoint])

  useEffect(() => {
    if (!selectedItem) return
    if (selectedChannelPoint === selectedItem.channel_point) return
    setSelectedChannelPoint(selectedItem.channel_point)
  }, [selectedChannelPoint, selectedItem])

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
      </section>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.2fr)_minmax(320px,0.8fr)]">
        <section className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
          <div className="mb-4 flex items-center justify-between gap-3">
            <h3 className="text-lg font-semibold">{t('channelRanking.listTitle')}</h3>
            <div className="text-xs text-fog/60">{t('channelRanking.listCount', { count: filteredItems.length })}</div>
          </div>
          {loading ? (
            <p className="text-sm text-fog/70">{t('channelRanking.loading')}</p>
          ) : filteredItems.length === 0 ? (
            <p className="text-sm text-fog/70">{t('channelRanking.empty')}</p>
          ) : (
            <div className="space-y-3">
              {filteredItems.map((item) => {
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
                        <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-fog/75">
                          {t('channelRanking.scoreShort', { value: numberFormatter.format(item.score) })}
                        </span>
                      </div>
                    </div>
                    <div className="mt-3 grid gap-2 text-xs text-fog/70 sm:grid-cols-2 lg:grid-cols-4">
                      <div>{t('channelRanking.netFees7d', { value: formatSats(item.profit_fee_7d_sat) })}</div>
                      <div>{t('channelRanking.capacity', { value: formatSats(item.capacity_sat) })}</div>
                      <div>{t('channelRanking.localBalancePct', { value: formatPct(item.local_balance_pct) })}</div>
                      <div>{t('channelRanking.pendingHtlcCount', { value: numberFormatter.format(item.pending_htlc_count || 0) })}</div>
                    </div>
                  </button>
                )
              })}
            </div>
          )}
        </section>

        <aside className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
          <div className="mb-4 flex items-center justify-between gap-3">
            <h3 className="text-lg font-semibold">{t('channelRanking.detailTitle')}</h3>
            {selectedItem && (
              <a
                href={buildHashWithChannelPoint(LIGHTNING_OPS_ROUTE_KEY, selectedItem.channel_point)}
                className="text-xs text-sky-200 hover:text-sky-100"
              >
                {t('channelRanking.openInLightningOps')}
              </a>
            )}
          </div>

          {!selectedItem ? (
            <p className="text-sm text-fog/70">{t('channelRanking.selectChannel')}</p>
          ) : (
            <div className="space-y-5">
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={`rounded-full px-3 py-1 text-xs ${stateBadgeClass(selectedItem.state)}`}>
                    {stateLabel(selectedItem.state)}
                  </span>
                  <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/75">
                    {t('channelRanking.score', { value: numberFormatter.format(selectedItem.score) })}
                  </span>
                  {selectedItem.class_label && (
                    <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/75">
                      {selectedItem.class_label}
                    </span>
                  )}
                </div>
                <div className="text-base font-medium text-fog">
                  {selectedItem.peer_alias || selectedItem.peer_pubkey || selectedItem.channel_point}
                </div>
                <div className="break-all text-xs text-fog/55">{selectedItem.channel_point}</div>
              </div>

              <div className="grid gap-3 text-sm sm:grid-cols-2">
                <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                  <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.metricsTitle')}</div>
                  <div className="mt-2 space-y-1 text-fog/80">
                    <div>{t('channelRanking.capacity', { value: formatSats(selectedItem.capacity_sat) })}</div>
                    <div>{t('channelRanking.localBalance', { value: formatSats(selectedItem.local_balance_sat) })}</div>
                    <div>{t('channelRanking.remoteBalance', { value: formatSats(selectedItem.remote_balance_sat) })}</div>
                    <div>{t('channelRanking.localBalancePct', { value: formatPct(selectedItem.local_balance_pct) })}</div>
                    <div>{t('channelRanking.remoteBalancePct', { value: formatPct(selectedItem.remote_balance_pct) })}</div>
                    <div>{t('channelRanking.pendingHtlcCount', { value: numberFormatter.format(selectedItem.pending_htlc_count || 0) })}</div>
                    <div>{t('channelRanking.inactiveDuration', { value: formatDuration(selectedItem.inactive_duration_sec) })}</div>
                  </div>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/[0.03] p-3">
                  <div className="text-[11px] uppercase tracking-wide text-fog/45">{t('channelRanking.economicsTitle')}</div>
                  <div className="mt-2 space-y-1 text-fog/80">
                    <div>{t('channelRanking.forwardFees7d', { value: formatSats(selectedItem.forward_fee_7d_sat) })}</div>
                    <div>{t('channelRanking.forwardAmount7d', { value: formatSats(selectedItem.forward_amt_7d_sat) })}</div>
                    <div>{t('channelRanking.outPpm7d', { value: numberFormatter.format(selectedItem.out_ppm_7d || 0) })}</div>
                    <div>{t('channelRanking.rebalanceFees7d', { value: formatSats(selectedItem.rebal_fee_7d_sat) })}</div>
                    <div>{t('channelRanking.rebalanceAmount7d', { value: formatSats(selectedItem.rebal_amt_7d_sat) })}</div>
                    <div>{t('channelRanking.rebalancePpm7d', { value: numberFormatter.format(selectedItem.rebal_ppm_7d || 0) })}</div>
                    <div>{t('channelRanking.netFees7d', { value: formatSats(selectedItem.profit_fee_7d_sat) })}</div>
                    <div>{t('channelRanking.computedAt', { value: formatTimestamp(selectedItem.computed_at) })}</div>
                  </div>
                </div>
              </div>

              <div className="space-y-3">
                <div>
                  <div className="mb-2 text-sm font-medium text-fog">{t('channelRanking.reasonsTitle')}</div>
                  <div className="flex flex-wrap gap-2">
                    {(selectedItem.reasons || []).length > 0 ? (
                      selectedItem.reasons!.map((reason) => (
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
                    {(selectedItem.recommendations || []).length > 0 ? (
                      selectedItem.recommendations!.map((item) => (
                        <a
                          key={`${item.code}-${item.target_module || ''}`}
                          href={buildModuleLink(selectedItem, item.target_module)}
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
              </div>
            </div>
          )}
        </aside>
      </div>
    </div>
  )
}
