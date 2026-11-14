import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getInsights, recomputeInsights, setInsightStatus, type InsightItem, type InsightsPayload } from '../api'
import MetricTile from '../components/dashboard/MetricTile'
import { getLocale } from '../i18n'
import InsightCard from './insights/InsightCard'
import OutcomePanel from './insights/OutcomePanel'
import RuleGlossary from './insights/RuleGlossary'
import SignalMap from './insights/SignalMap'

const KINDS: InsightItem['kind'][] = ['repor_vendedor', 'mais_capacidade', 'parar_compra', 'capital_parado', 'abrir_canal', 'preco_fora', 'desconto_entrada', 'bloqueio_esperado']
const CONFIDENCES: InsightItem['confidence'][] = ['high', 'medium', 'low']
const TEACH_KEY = 'los-insights-teach'
const RULES = ['R1', 'R2', 'R3', 'R4', 'R5', 'R6', 'R7', 'R8']

const readTeach = () => {
  try {
    return localStorage.getItem(TEACH_KEY) !== '0'
  } catch {
    return true
  }
}

export default function Insights() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }), [locale])
  const relativeFormatter = useMemo(() => new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }), [locale])

  const [payload, setPayload] = useState<InsightsPayload | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [status, setStatus] = useState('')
  const [teach, setTeach] = useState(readTeach)
  const [kindFilter, setKindFilter] = useState<InsightItem['kind'] | 'all'>('all')
  const [confFilter, setConfFilter] = useState<InsightItem['confidence'] | 'all'>('all')
  const [search, setSearch] = useState('')
  const [showHidden, setShowHidden] = useState(false)
  const [glossary, setGlossary] = useState<{ open: boolean; rule?: string }>({ open: false })
  const [busyId, setBusyId] = useState('')
  const [actionError, setActionError] = useState<{ id: string; message: string } | null>(null)

  const formatNumber = useCallback((value: number) => numberFormatter.format(Math.round(value * 10) / 10), [numberFormatter])
  const formatAge = useCallback((seconds: number) => {
    // Bare durations only: the i18n "age" key adds "ago" / "há" around them.
    if (seconds < 90) return `${Math.max(0, Math.round(seconds))} s`
    if (seconds < 5400) return `${Math.round(seconds / 60)} min`
    if (seconds < 172800) return `${Math.round(seconds / 3600)} h`
    return `${Math.round(seconds / 86400)} d`
  }, [])
  const formatRelative = useCallback((iso: string) => {
    const date = new Date(iso)
    if (Number.isNaN(date.getTime())) return iso
    const diffSec = (date.getTime() - Date.now()) / 1000
    const abs = Math.abs(diffSec)
    if (abs < 3600) return relativeFormatter.format(Math.round(diffSec / 60), 'minute')
    if (abs < 86400) return relativeFormatter.format(Math.round(diffSec / 3600), 'hour')
    return relativeFormatter.format(Math.round(diffSec / 86400), 'day')
  }, [relativeFormatter])

  const load = useCallback(async (recompute = false) => {
    if (recompute) setRefreshing(true)
    setStatus('')
    try {
      const data = recompute ? await recomputeInsights() : await getInsights()
      setPayload(data)
    } catch (err: any) {
      setStatus(err?.message || t('insights.unavailable'))
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [t])

  useEffect(() => {
    void load()
  }, [load])

  const toggleTeach = () => {
    setTeach((value) => {
      const next = !value
      try {
        localStorage.setItem(TEACH_KEY, next ? '1' : '0')
      } catch {
        // per-viewer convenience only
      }
      return next
    })
  }

  const onStatus = async (item: InsightItem, next: 'open' | 'dismissed' | 'done', reason?: string) => {
    setBusyId(item.id)
    setActionError((current) => (current?.id === item.id ? null : current))
    try {
      await setInsightStatus(item.id, next, reason)
      setPayload((current) => {
        if (!current) return current
        const items = current.items.map((entry) => (entry.id === item.id ? { ...entry, status: next, dismiss_reason: reason } : entry))
        return { ...current, items }
      })
    } catch (err: any) {
      setActionError({ id: item.id, message: err?.message || t('insights.unavailable') })
    } finally {
      setBusyId('')
    }
  }

  const items = payload?.items ?? []
  const counts = useMemo(() => {
    const out: Record<string, number> = {}
    items.filter((item) => item.status === 'open').forEach((item) => {
      out[item.kind] = (out[item.kind] || 0) + 1
    })
    return out
  }, [items])
  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return items.filter((item) => {
      if (!showHidden && item.status !== 'open') return false
      if (kindFilter !== 'all' && item.kind !== kindFilter) return false
      if (confFilter !== 'all' && item.confidence !== confFilter) return false
      if (needle) {
        const hay = `${item.peer_alias || ''} ${item.channel_point || ''} ${item.peer_pubkey || ''}`.toLowerCase()
        if (!hay.includes(needle)) return false
      }
      return true
    })
  }, [items, showHidden, kindFilter, confFilter, search])
  const summary = payload?.summary
  const node = payload?.node
  const openCount = items.filter((item) => item.status === 'open').length
  const warming = Boolean(node && !node.ready)
  const warmingPct = node ? Math.min(100, Math.round((node.observed_days / Math.max(1, node.required_days)) * 100)) : 0

  return (
    <section className="space-y-6">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <h2 className="text-2xl font-semibold text-fog">{t('insights.title')}</h2>
          <p className="mt-1 max-w-3xl text-sm text-fog/70">{t('insights.subtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button type="button" className={`rounded-full border px-3 py-1 text-xs ${teach ? 'border-sky-400/40 bg-sky-500/15 text-fog' : 'border-white/10 bg-white/5 text-fog/70'}`} title={t('insights.teachHint')} onClick={toggleTeach}>
            {teach ? t('insights.teachOn') : t('insights.teachOff')}
          </button>
          {node && node.node_class !== 'unknown' && (
            <span
              className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/70"
              title={t('insights.node.hint', {
                idle: formatNumber(node.idle_min_capacity_sat / 1e6),
                idlePct: node.idle_local_pct,
                open: formatNumber(node.open_min_volume_sat / 1e6),
                stop: node.stop_buying_min_fee_sat,
              })}
            >
              {t('insights.node.pill', {
                size: t(`insights.node.size.${node.node_class}`),
                liquidity: t(`insights.node.liquidity.${node.liquidity_class}`),
                count: Math.min(node.observed_days, 30),
              })}
            </span>
          )}
          {payload && <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs text-fog/70">{t('insights.computedAt', { value: formatRelative(payload.computed_at) })}</span>}
          <button type="button" className="btn-secondary px-3 py-1 text-xs" disabled={refreshing} onClick={() => void load(true)}>
            {refreshing ? t('insights.recomputing') : t('insights.recompute')}
          </button>
          <button type="button" className="btn-primary px-3 py-1 text-xs" onClick={() => setGlossary({ open: true })}>{t('insights.how')}</button>
        </div>
      </header>

      {status && <div className="rounded-2xl border border-amber-400/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">{status}</div>}

      {loading ? (
        <p className="text-sm text-fog/70">{t('insights.loading')}</p>
      ) : payload && warming && node ? (
        <>
          <div className="rounded-3xl border border-sky-400/30 bg-sky-500/10 p-6">
            <p className="text-base font-semibold text-fog">{t('insights.warming.title')}</p>
            <p className="mt-1 max-w-3xl text-sm text-fog/70">{t('insights.warming.body', { observed: node.observed_days, required: node.required_days })}</p>
            <div className="mt-4 h-2 w-full max-w-xl overflow-hidden rounded-full bg-white/10">
              <div className="h-full rounded-full bg-sky-400 transition-all" style={{ width: `${warmingPct}%` }} />
            </div>
            <p className="mt-2 text-xs text-fog/60">{t('insights.warming.progress', { observed: node.observed_days, required: node.required_days })}</p>
            {teach && <p className="mt-4 max-w-3xl text-xs text-fog/60">{t('insights.warming.teach')}</p>}
          </div>
          <SignalMap sources={payload.sources} teach={teach} formatAge={formatAge} />
        </>
      ) : payload ? (
        <>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <MetricTile label={t('insights.summary.valueAtStake')} value={`+${formatNumber(summary?.value_sat_day || 0)} sat/d`} tone={summary && summary.value_sat_day > 0 ? 'ok' : 'muted'} sublabel={teach ? t('insights.summary.valueHint') : undefined} />
            <MetricTile label={t('insights.summary.open')} value={summary?.open ?? 0} tone="info" badgeLabel={summary ? t('insights.summary.openHigh', { count: summary.open_high }) : undefined} sublabel={teach ? t('insights.summary.openHint') : undefined} />
            <MetricTile
              label={t('insights.summary.followed')}
              value={summary?.followed_30d ?? 0}
              tone={summary && summary.measured > 0 && summary.hits * 2 >= summary.measured ? 'ok' : 'muted'}
              badgeLabel={summary && summary.measured > 0 ? t('insights.summary.accuracy', { value: Math.round((summary.hits / summary.measured) * 100) }) : summary && summary.pending > 0 ? t('insights.summary.pendingBadge', { count: summary.pending }) : undefined}
              sublabel={teach ? t('insights.summary.followedHint') : undefined}
            />
            <MetricTile label={t('insights.summary.idle')} value={`${formatNumber((summary?.idle_capital_sat || 0) / 1e6)}M sat`} tone={summary && summary.idle_capital_sat > 0 ? 'warn' : 'muted'} sublabel={teach ? t('insights.summary.idleHint') : undefined} />
          </div>

          <SignalMap sources={payload.sources} teach={teach} formatAge={formatAge} />

          <div className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
            <div className="flex flex-wrap items-center gap-2">
              <button type="button" className={`rounded-full border px-3 py-1 text-xs ${kindFilter === 'all' ? 'border-sky-400/40 bg-sky-500/15 text-fog' : 'border-white/10 bg-white/5 text-fog/70'}`} onClick={() => setKindFilter('all')}>
                {t('insights.filters.all')} · {openCount}
              </button>
              {KINDS.map((kind) => (
                <button key={kind} type="button" className={`rounded-full border px-3 py-1 text-xs ${kindFilter === kind ? 'border-sky-400/40 bg-sky-500/15 text-fog' : 'border-white/10 bg-white/5 text-fog/70'}`} onClick={() => setKindFilter(kind)}>
                  {t(`insights.kind.${kind}.label`)}{counts[kind] ? ` · ${counts[kind]}` : ''}
                </button>
              ))}
              <span className="flex-1" />
              <span className="text-[11px] uppercase tracking-wide text-fog/50">{t('insights.filters.confidence')}</span>
              {CONFIDENCES.map((level) => (
                <button key={level} type="button" className={`rounded-full border px-3 py-1 text-xs ${confFilter === level ? 'border-sky-400/40 bg-sky-500/15 text-fog' : 'border-white/10 bg-white/5 text-fog/70'}`} onClick={() => setConfFilter(confFilter === level ? 'all' : level)}>
                  {t(`insights.confidence.${level}`)}
                </button>
              ))}
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-3">
              <input className="input-field max-w-xs text-xs" placeholder={t('insights.filters.search')} value={search} onChange={(event) => setSearch(event.target.value)} />
              <label className="flex items-center gap-2 text-xs text-fog/70">
                <input type="checkbox" checked={showHidden} onChange={(event) => setShowHidden(event.target.checked)} />
                {t('insights.filters.showHidden')}
              </label>
            </div>
          </div>

          {visible.length === 0 ? (
            <div className="rounded-3xl border border-dashed border-white/15 bg-ink/40 p-6 text-center">
              <p className="text-base font-semibold text-fog">{t('insights.empty.title')}</p>
              <p className="mt-1 text-sm text-fog/70">{t('insights.empty.body')}</p>
              <p className="mt-4 text-[11px] uppercase tracking-[0.2em] text-fog/50">{t('insights.empty.watching')}</p>
              <ul className="mt-2 flex flex-wrap justify-center gap-2 text-xs">
                {RULES.map((rule) => (
                  <li key={rule}>
                    <button type="button" className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-fog/75 hover:text-fog" onClick={() => setGlossary({ open: true, rule })}>
                      ✓ {rule} · {t(`insights.glossary.rules.${rule}.name`)}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ) : (
            <div className="grid min-w-0 gap-3">
              {visible.map((item) => (
                <InsightCard key={item.id} item={item} teach={teach} busy={busyId === item.id} actionError={actionError?.id === item.id ? actionError.message : undefined} formatNumber={formatNumber} formatRelative={formatRelative} onStatus={(entry, next, reason) => void onStatus(entry, next, reason)} onRule={(rule) => setGlossary({ open: true, rule })} />
              ))}
            </div>
          )}

          <OutcomePanel scorecard={payload.scorecard ?? []} teach={teach} formatNumber={formatNumber} formatRelative={formatRelative} onRule={(rule) => setGlossary({ open: true, rule })} />
        </>
      ) : null}

      <RuleGlossary open={glossary.open} focusRule={glossary.rule} onClose={() => setGlossary({ open: false })} />
    </section>
  )
}
