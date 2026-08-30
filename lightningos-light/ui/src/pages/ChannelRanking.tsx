import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getChannelCapitalPlan, getChannelRanking, recomputeChannelRankings } from '../api'
import { getLocale } from '../i18n'
import ChannelCapitalPlanPanel from './channel-ranking/ChannelCapitalPlanPanel'
import ChannelRankingDetailPanel from './channel-ranking/ChannelRankingDetailPanel'
import ChannelRankingTable from './channel-ranking/ChannelRankingTable'
import { channelRankingHash, readChannelPointFromHash } from './channel-ranking/links'
import type {
  ChannelCapitalPlanItem,
  ChannelCapitalPlanPayload,
  ChannelCapitalPlanSummary,
  ChannelRankingDetailPayload,
  ChannelRankingFormatters
} from './channel-ranking/types'

type View = 'plan' | 'channels'

const emptySummary: ChannelCapitalPlanSummary = {
  total_channels: 0,
  action_counts: {},
  productive_capital_sat: 0,
  protected_capital_sat: 0,
  parked_capital_sat: 0,
  recoverable_local_sat: 0
}

const clamp = (value: number, min: number, max: number) => Math.max(min, Math.min(max, value))

export default function ChannelRanking() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [view, setView] = useState<View>('plan')
  const [items, setItems] = useState<ChannelCapitalPlanItem[]>([])
  const [summary, setSummary] = useState<ChannelCapitalPlanSummary>(emptySummary)
  const [stateCounts, setStateCounts] = useState<Record<string, number>>({})
  const [available, setAvailable] = useState(true)
  const [magmaStateKnown, setMagmaStateKnown] = useState(true)
  const [lastSyncAt, setLastSyncAt] = useState('')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [status, setStatus] = useState('')
  const [selectedChannelPoint, setSelectedChannelPoint] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [detail, setDetail] = useState<ChannelRankingDetailPayload | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const percentFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }), [locale])
  const dateTimeFormatter = useMemo(() => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }), [locale])

  const format = useMemo<ChannelRankingFormatters>(() => ({
    number: numberFormatter,
    percent: percentFormatter,
    dateTime: dateTimeFormatter,
    sats: (value?: number) => `${numberFormatter.format(Math.round(Number(value || 0)))} sats`,
    pct: (value?: number) => `${percentFormatter.format(clamp(Number(value || 0), 0, 100))}%`,
    ratioPct: (value?: number) => value === undefined ? t('common.na') : `${percentFormatter.format(clamp(Number(value) * 100, 0, 100))}%`,
    timestamp: (value?: string) => {
      if (!value) return t('common.na')
      const parsed = new Date(value)
      return Number.isNaN(parsed.getTime()) ? value : dateTimeFormatter.format(parsed)
    },
    duration: (value?: number) => {
      const seconds = Math.max(0, Math.floor(Number(value || 0)))
      if (!seconds) return t('common.na')
      if (seconds >= 86400) return t('channelRanking.durationDays', { count: Math.floor(seconds / 86400) })
      if (seconds >= 3600) return t('channelRanking.durationHours', { count: Math.floor(seconds / 3600) })
      if (seconds >= 60) return t('channelRanking.durationMinutes', { count: Math.floor(seconds / 60) })
      return t('channelRanking.durationSeconds', { count: seconds })
    },
    flow: (count?: number, amount?: number) => `${numberFormatter.format(Math.max(0, Math.round(Number(count || 0))))}x · ${numberFormatter.format(Math.round(Number(amount || 0)))} sats`
  }), [dateTimeFormatter, numberFormatter, percentFormatter, t])

  const load = async (options: { recompute?: boolean; quiet?: boolean } = {}) => {
    if (options.recompute) setRefreshing(true)
    else if (!options.quiet) setLoading(true)
    setStatus('')
    try {
      if (options.recompute) await recomputeChannelRankings()
      const payload = await getChannelCapitalPlan() as ChannelCapitalPlanPayload
      const nextItems = Array.isArray(payload.items) ? payload.items : []
      setItems(nextItems)
      setSummary(payload.summary || { ...emptySummary, total_channels: nextItems.length })
      setStateCounts(payload.state_counts || {})
      setAvailable(payload.available !== false)
      setMagmaStateKnown(payload.magma_state_known !== false)
      setLastSyncAt(String(payload.last_sync_at || ''))
      setSelectedChannelPoint((current) => current || readChannelPointFromHash())
    } catch (error: any) {
      setStatus(error?.message || t('channelRanking.unavailable'))
      if (!options.quiet) {
        setItems([])
        setSummary(emptySummary)
        setStateCounts({})
        setAvailable(false)
      }
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => {
    const initial = readChannelPointFromHash()
    if (initial) {
      setSelectedChannelPoint(initial)
      setDrawerOpen(true)
    }
    const handleHashChange = () => {
      const point = readChannelPointFromHash()
      if (!point) return
      setSelectedChannelPoint(point)
      setDrawerOpen(true)
    }
    window.addEventListener('hashchange', handleHashChange)
    void load()
    return () => window.removeEventListener('hashchange', handleHashChange)
  }, [])

  const selectedPlanItem = useMemo(
    () => items.find((entry) => entry.channel.channel_point === selectedChannelPoint) || null,
    [items, selectedChannelPoint]
  )

  useEffect(() => {
    if (!drawerOpen || !selectedChannelPoint) {
      setDetail(null)
      return
    }
    let active = true
    setDetailLoading(true)
    getChannelRanking(selectedChannelPoint)
      .then((payload) => { if (active) setDetail(payload as ChannelRankingDetailPayload) })
      .catch(() => { if (active) setDetail(null) })
      .finally(() => { if (active) setDetailLoading(false) })
    return () => { active = false }
  }, [drawerOpen, selectedChannelPoint])

  const selectPlanItem = (item: ChannelCapitalPlanItem) => {
    setSelectedChannelPoint(item.channel.channel_point)
    setDrawerOpen(true)
    window.history.replaceState(null, '', channelRankingHash(item.channel.channel_point))
  }

  const selectChannelPoint = (channelPoint: string) => {
    const item = items.find((entry) => entry.channel.channel_point === channelPoint)
    if (item) selectPlanItem(item)
  }

  const closeDrawer = () => {
    setDrawerOpen(false)
    window.history.replaceState(null, '', '#channel-ranking')
  }

  const refreshPolicy = async () => {
    await load({ quiet: true })
    try {
      setDetail(await getChannelRanking(selectedChannelPoint) as ChannelRankingDetailPayload)
    } catch {
      setDetail(null)
    }
  }

  return (
    <div className="space-y-6">
      <section className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h2 className="text-2xl font-semibold">{t('channelRanking.title')}</h2>
          <p className="mt-1 max-w-3xl text-sm text-fog/70">{t('channelRanking.refactoredSubtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/65">
            {t('channelRanking.lastSync', { value: lastSyncAt ? format.timestamp(lastSyncAt) : t('common.unavailable') })}
          </span>
          <a className="btn-secondary" href="#new-channels">{t('nav.newChannels')}</a>
          <button type="button" className="btn-secondary" disabled={refreshing} onClick={() => void load({ recompute: true })}>
            {refreshing ? t('channelRanking.refreshing') : t('common.refresh')}
          </button>
        </div>
      </section>

      {status && <div className="rounded-2xl border border-rose-400/35 bg-rose-500/10 px-4 py-3 text-sm text-rose-100">{status}</div>}
      {!available && !status && <div className="rounded-2xl border border-amber-400/35 bg-amber-500/10 px-4 py-3 text-sm text-amber-100">{t('channelRanking.unavailable')}</div>}

      <nav className="flex w-fit gap-2 rounded-2xl border border-white/10 bg-ink/60 p-1.5">
        <button type="button" className={view === 'plan' ? 'btn-primary' : 'btn-secondary'} onClick={() => setView('plan')}>{t('channelRanking.views.plan')}</button>
        <button type="button" className={view === 'channels' ? 'btn-primary' : 'btn-secondary'} onClick={() => setView('channels')}>{t('channelRanking.views.channels')}</button>
      </nav>

      {view === 'plan' ? (
        <ChannelCapitalPlanPanel items={items} summary={summary} magmaStateKnown={magmaStateKnown} loading={loading} format={format} onSelect={selectPlanItem} />
      ) : (
        <ChannelRankingTable items={items} stateCounts={stateCounts} selectedChannelPoint={selectedChannelPoint} loading={loading} format={format} onSelect={selectPlanItem} />
      )}

      {drawerOpen && selectedPlanItem && (
        <ChannelRankingDetailPanel
          planItem={selectedPlanItem}
          detail={detail}
          loading={detailLoading}
          format={format}
          onClose={closeDrawer}
          onSelectChannel={selectChannelPoint}
          onPolicyChanged={refreshPolicy}
        />
      )}
    </div>
  )
}
