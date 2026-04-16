import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getChannelOpenCandidates, recomputeChannelOpenCandidates } from '../api'
import { getLocale } from '../i18n'

type ChannelOpenCandidateReason = {
  code: string
}

type ChannelOpenCandidateItem = {
  peer_pubkey: string
  peer_alias?: string
  route_hit_count_30d: number
  route_volume_sat_30d: number
  route_cost_ppm_30d: number
  failed_attempts_30d: number
  shared_problem_peer_count: number
  shared_strong_peer_count: number
  graph_channel_count: number
  graph_total_capacity_sat: number
  best_outbound_fee_ppm: number
  demand_score: number
  relief_score: number
  graph_quality_score: number
  score: number
  confidence: number
  reasons?: ChannelOpenCandidateReason[]
}

type ChannelOpenCandidatesPayload = {
  available?: boolean
  last_sync_at?: string
  last_error?: string
  candidate_count?: number
  items?: ChannelOpenCandidateItem[]
}

const GRAPH_EXPLORER_ROUTE_KEY = 'graph-explorer'

const buildGraphExplorerHash = (pubkey: string) =>
  `#${GRAPH_EXPLORER_ROUTE_KEY}?pubkey=${encodeURIComponent(pubkey)}`

const categoryBadgeClass = (kind: 'demand' | 'relief' | 'graph') => {
  switch (kind) {
    case 'demand':
      return 'border-sky-400/30 bg-sky-500/15 text-sky-100'
    case 'relief':
      return 'border-amber-400/30 bg-amber-500/15 text-amber-100'
    default:
      return 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200'
  }
}

export default function ChannelOpenCandidates() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const dateTimeFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  )

  const [items, setItems] = useState<ChannelOpenCandidateItem[]>([])
  const [available, setAvailable] = useState(true)
  const [lastSyncAt, setLastSyncAt] = useState('')
  const [status, setStatus] = useState('')
  const [candidateCount, setCandidateCount] = useState(0)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)

  const formatSats = (value?: number) => `${numberFormatter.format(Math.round(Number(value || 0)))} sats`
  const formatTimestamp = (value?: string) => {
    if (!value) return t('common.na')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return value
    return dateTimeFormatter.format(parsed)
  }

  const reasonLabel = (code?: string) =>
    t(`channelRanking.openCandidates.reasons.${String(code || '').trim()}` as any, {
      defaultValue: code || t('common.unknown')
    })

  const load = async (options: { recompute?: boolean } = {}) => {
    const recompute = options.recompute === true
    if (recompute) {
      setRefreshing(true)
    } else {
      setLoading(true)
    }
    try {
      if (recompute) {
        await recomputeChannelOpenCandidates()
      }
      const payload = await getChannelOpenCandidates({ limit: 100 }) as ChannelOpenCandidatesPayload
      setItems(Array.isArray(payload?.items) ? payload.items : [])
      setAvailable(payload?.available !== false)
      setLastSyncAt(String(payload?.last_sync_at || ''))
      setCandidateCount(Math.max(0, Number(payload?.candidate_count || 0)))
      setStatus(String(payload?.last_error || '').trim())
    } catch (err: any) {
      setItems([])
      setAvailable(false)
      setCandidateCount(0)
      setStatus(err?.message || t('channelRanking.openCandidates.unavailable'))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  return (
    <section className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold">{t('channelRanking.openCandidates.title')}</h2>
          <p className="text-sm text-fog/70">{t('channelRanking.openCandidates.subtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <div className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/70">
            {t('channelRanking.openCandidates.lastSync', {
              value: lastSyncAt ? formatTimestamp(lastSyncAt) : t('common.unavailable')
            })}
          </div>
          <div className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/70">
            {t('channelRanking.openCandidates.count', { count: candidateCount })}
          </div>
          <button
            type="button"
            className={`btn-secondary ${refreshing ? 'opacity-60 pointer-events-none' : ''}`}
            onClick={() => void load({ recompute: true })}
            disabled={refreshing}
          >
            {refreshing
              ? t('channelRanking.openCandidates.refreshing')
              : t('channelRanking.openCandidates.runNow')}
          </button>
        </div>
      </div>

      {status && (
        <div className="rounded-2xl border border-amber-400/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-100">
          {status}
        </div>
      )}

      {!status && !available && (
        <div className="rounded-2xl border border-amber-400/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-100">
          {t('channelRanking.openCandidates.unavailable')}
        </div>
      )}

      <section className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
        {loading ? (
          <p className="text-sm text-fog/70">{t('channelRanking.openCandidates.loading')}</p>
        ) : items.length === 0 ? (
          <p className="text-sm text-fog/70">{t('channelRanking.openCandidates.empty')}</p>
        ) : (
          <div className="max-h-[72vh] overflow-auto pb-2 pr-1">
            <div className="grid min-w-[78rem] gap-3 xl:grid-cols-2">
              {items.map((item) => (
                <div key={item.peer_pubkey} className="rounded-2xl border border-white/10 bg-white/[0.03] p-4">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0 space-y-1">
                      <div className="truncate text-sm font-medium text-fog">{item.peer_alias || item.peer_pubkey}</div>
                      <div className="break-all text-[11px] text-fog/55">{item.peer_pubkey}</div>
                    </div>
                    <a
                      href={buildGraphExplorerHash(item.peer_pubkey)}
                      className="shrink-0 text-xs text-sky-200 hover:text-sky-100"
                    >
                      {t('nav.graphExplorer')}
                    </a>
                  </div>

                  <div className="mt-3 flex flex-wrap items-center gap-2">
                    <span className="rounded-full border border-emerald-400/30 bg-emerald-500/15 px-2.5 py-1 text-[11px] text-emerald-200">
                      {t('channelRanking.openCandidates.score', { value: numberFormatter.format(item.score) })}
                    </span>
                    <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-fog/75">
                      {t('channelRanking.openCandidates.confidence', { value: numberFormatter.format(item.confidence) })}
                    </span>
                    <span
                      className={`cursor-help rounded-full border px-2.5 py-1 text-[11px] ${categoryBadgeClass('demand')}`}
                      title={t('channelRanking.openCandidates.demandHint')}
                      aria-label={t('channelRanking.openCandidates.demandHint')}
                    >
                      {t('channelRanking.openCandidates.demand', { value: numberFormatter.format(item.demand_score || 0) })}
                    </span>
                    <span
                      className={`cursor-help rounded-full border px-2.5 py-1 text-[11px] ${categoryBadgeClass('relief')}`}
                      title={t('channelRanking.openCandidates.reliefHint')}
                      aria-label={t('channelRanking.openCandidates.reliefHint')}
                    >
                      {t('channelRanking.openCandidates.relief', { value: numberFormatter.format(item.relief_score || 0) })}
                    </span>
                    <span
                      className={`cursor-help rounded-full border px-2.5 py-1 text-[11px] ${categoryBadgeClass('graph')}`}
                      title={t('channelRanking.openCandidates.graphQualityHint')}
                      aria-label={t('channelRanking.openCandidates.graphQualityHint')}
                    >
                      {t('channelRanking.openCandidates.graphQuality', { value: numberFormatter.format(item.graph_quality_score || 0) })}
                    </span>
                  </div>

                  <div className="mt-3 grid gap-2 text-xs text-fog/70 md:grid-cols-3 xl:grid-cols-3 2xl:grid-cols-4">
                    <div>{t('channelRanking.openCandidates.routeHits', { count: item.route_hit_count_30d || 0 })}</div>
                    <div>{t('channelRanking.openCandidates.routeVolume', { value: formatSats(item.route_volume_sat_30d) })}</div>
                    <div>{t('channelRanking.openCandidates.routeCost', { value: numberFormatter.format(item.route_cost_ppm_30d || 0) })}</div>
                    <div>{t('channelRanking.openCandidates.failedAttempts', { count: item.failed_attempts_30d || 0 })}</div>
                    <div>{t('channelRanking.openCandidates.problemAdjacency', { count: item.shared_problem_peer_count || 0 })}</div>
                    <div>{t('channelRanking.openCandidates.strongAdjacency', { count: item.shared_strong_peer_count || 0 })}</div>
                    <div>{t('channelRanking.openCandidates.graphChannels', { count: item.graph_channel_count || 0 })}</div>
                    <div>{t('channelRanking.openCandidates.graphCapacity', { value: formatSats(item.graph_total_capacity_sat) })}</div>
                    <div>{t('channelRanking.openCandidates.bestOutbound', { value: numberFormatter.format(item.best_outbound_fee_ppm || 0) })}</div>
                  </div>

                  {!!item.reasons?.length && (
                    <div className="mt-3 flex flex-wrap gap-2">
                      {item.reasons.map((reason) => (
                        <span
                          key={`${item.peer_pubkey}-${reason.code}`}
                          className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-fog/75"
                        >
                          {reasonLabel(reason.code)}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </section>
    </section>
  )
}
