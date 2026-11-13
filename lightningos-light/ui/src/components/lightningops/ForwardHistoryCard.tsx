import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Bar, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import {
  getLnForwardHistory,
  type ForwardHistoryFlowEntry,
  type ForwardHistoryItem,
  type ForwardHistoryRange,
  type ForwardHistoryResponse,
  type ForwardHistorySort,
} from '../../api'
import { getLocale } from '../../i18n'

// Forward History — collapsible card on Lightning Ops. Lists every forward the
// node routed in a window, lets the operator slice it by incoming/outgoing
// channel, sort the list and always shows the filtered slice against the
// whole window (volume, fees, count, average ppm).

const RANGES: ForwardHistoryRange[] = ['24h', '7d', '30d', '90d']
const PAGE_SIZE = 100
const AUTO_REFRESH_MS = 60_000

const VOLUME_COLOR = '#38bdf8'
const FEE_COLOR = '#34d399'

const tooltipStyle = {
  background: '#0f172a',
  borderRadius: 12,
  border: '1px solid rgba(255,255,255,0.1)',
  color: '#f8fafc',
}

type Filters = {
  range: ForwardHistoryRange
  chanIn: string
  chanOut: string
  sort: ForwardHistorySort
  order: 'asc' | 'desc'
}

const DEFAULT_FILTERS: Filters = { range: '7d', chanIn: '', chanOut: '', sort: 'time', order: 'desc' }

const browserTimeZone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined
  } catch {
    return undefined
  }
}

const share = (part: number, total: number) => (total > 0 ? Math.min(100, Math.max(0, (part / total) * 100)) : 0)

export default function ForwardHistoryCard() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [open, setOpen] = useState(false)
  const [filters, setFilters] = useState<Filters>(DEFAULT_FILTERS)
  const [data, setData] = useState<ForwardHistoryResponse | null>(null)
  const [items, setItems] = useState<ForwardHistoryItem[]>([])
  const [loading, setLoading] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState('')
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null)
  const requestSeq = useRef(0)
  const tz = useMemo(browserTimeZone, [])

  const formatSats = useCallback((value: number) => Math.round(value || 0).toLocaleString(locale), [locale])
  const formatCompact = useCallback(
    (value: number) => new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(value || 0),
    [locale],
  )
  const formatPct = useCallback((value: number) => `${value.toLocaleString(locale, { maximumFractionDigits: value >= 10 ? 0 : 1 })}%`, [locale])
  const formatTime = useCallback(
    (iso: string) => {
      const date = new Date(iso)
      if (Number.isNaN(date.getTime())) return iso
      return date.toLocaleString(locale, { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
    },
    [locale],
  )

  const load = useCallback(
    async (next: Filters, offset: number) => {
      const seq = ++requestSeq.current
      if (offset === 0) setLoading(true)
      else setLoadingMore(true)
      try {
        const res = await getLnForwardHistory({
          range: next.range,
          chan_in: next.chanIn || undefined,
          chan_out: next.chanOut || undefined,
          sort: next.sort,
          order: next.order,
          limit: PAGE_SIZE,
          offset,
          tz,
        })
        if (seq !== requestSeq.current) return
        setData(res)
        setItems((prev) => (offset === 0 ? res.items : [...prev, ...res.items]))
        setError('')
        setUpdatedAt(new Date())
      } catch (err: any) {
        if (seq !== requestSeq.current) return
        setError(err?.message || t('lightningOps.forwardHistoryLoadFailed'))
      } finally {
        if (seq === requestSeq.current) {
          setLoading(false)
          setLoadingMore(false)
        }
      }
    },
    [t, tz],
  )

  useEffect(() => {
    if (!open) return
    void load(filters, 0)
  }, [open, filters, load])

  // Keep the first page fresh while the card is open; once the operator has
  // paged further we leave the list alone so it does not jump under them.
  useEffect(() => {
    if (!open) return
    const timer = setInterval(() => {
      if (items.length <= PAGE_SIZE) void load(filters, 0)
    }, AUTO_REFRESH_MS)
    return () => clearInterval(timer)
  }, [open, filters, items.length, load])

  const update = (patch: Partial<Filters>) => setFilters((prev) => ({ ...prev, ...patch }))
  const toggleSort = (sort: ForwardHistorySort) =>
    setFilters((prev) => (prev.sort === sort ? { ...prev, order: prev.order === 'desc' ? 'asc' : 'desc' } : { ...prev, sort, order: 'desc' }))
  const filtered = filters.chanIn !== '' || filters.chanOut !== ''

  const channelOptions = useMemo(() => {
    const list = data?.channels || []
    return [...list].sort((a, b) => a.peer_alias.localeCompare(b.peer_alias, locale, { sensitivity: 'base' }))
  }, [data, locale])
  const aliasOf = useCallback(
    (id: string) => channelOptions.find((channel) => channel.channel_id_str === id)?.peer_alias || id,
    [channelOptions],
  )

  const chart = useMemo(() => {
    const unit = data?.bucket_unit || 'day'
    return (data?.buckets || []).map((bucket) => {
      const date = new Date(bucket.start)
      const label =
        unit === 'hour'
          ? date.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' })
          : date.toLocaleDateString(locale, { day: '2-digit', month: '2-digit' })
      return { label, amount: bucket.amount_sat, fee: bucket.fee_sat, count: bucket.count }
    })
  }, [data, locale])

  const totals = data?.totals
  const subset = data?.filtered

  const summary = subset
    ? t('lightningOps.forwardHistorySummary', { forwards: subset.count.toLocaleString(locale), amount: formatSats(subset.amount_sat), fee: formatSats(subset.fee_sat) })
    : null

  return (
    <div id="forward-history-section" className="section-card order-3 space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <h3 className="text-lg font-semibold">{t('lightningOps.forwardHistoryTitle')}</h3>
          <p className="text-xs text-fog/55">{open && summary ? summary : t('lightningOps.forwardHistorySubtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {open && updatedAt && (
            <span className="text-xs text-fog/50">{t('lightningOps.forwardHistoryUpdated', { time: updatedAt.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' }) })}</span>
          )}
          <button type="button" className="btn-secondary text-xs px-3 py-2" onClick={() => setOpen((value) => !value)} aria-expanded={open} aria-controls="forward-history-body">
            {open ? t('common.hide') : t('common.open')}
          </button>
        </div>
      </div>

      {open && (
        <div id="forward-history-body" className="space-y-4">
          <div className="flex flex-wrap items-end gap-3">
            <div className="flex rounded-xl border border-white/10 bg-ink/50 p-1" role="group" aria-label={t('lightningOps.forwardHistoryRangeLabel')}>
              {RANGES.map((range) => (
                <button
                  key={range}
                  type="button"
                  className={`rounded-lg px-3 py-1.5 text-xs font-medium transition ${filters.range === range ? 'bg-white/10 text-fog' : 'text-fog/60 hover:text-fog'}`}
                  onClick={() => update({ range })}
                  aria-pressed={filters.range === range}
                >
                  {range}
                </button>
              ))}
            </div>
            <label className="flex min-w-[200px] flex-1 flex-col gap-1 text-xs text-fog/60">
              {t('lightningOps.forwardHistoryInChannel')}
              <select className="input-field" value={filters.chanIn} onChange={(event) => update({ chanIn: event.target.value })}>
                <option value="">{t('lightningOps.forwardHistoryAnyChannel')}</option>
                {channelOptions
                  .filter((channel) => channel.in_count > 0 || channel.channel_id_str === filters.chanIn)
                  .map((channel) => (
                    <option key={`in-${channel.channel_id_str}`} value={channel.channel_id_str}>
                      {channel.peer_alias}
                      {channel.open ? '' : ` (${t('lightningOps.forwardHistoryClosedChannel')})`} · {channel.in_count.toLocaleString(locale)}
                    </option>
                  ))}
              </select>
            </label>
            <button
              type="button"
              className="btn-secondary px-3 py-2 text-sm"
              title={t('lightningOps.forwardHistorySwap')}
              aria-label={t('lightningOps.forwardHistorySwap')}
              disabled={!filtered}
              onClick={() => update({ chanIn: filters.chanOut, chanOut: filters.chanIn })}
            >
              ⇄
            </button>
            <label className="flex min-w-[200px] flex-1 flex-col gap-1 text-xs text-fog/60">
              {t('lightningOps.forwardHistoryOutChannel')}
              <select className="input-field" value={filters.chanOut} onChange={(event) => update({ chanOut: event.target.value })}>
                <option value="">{t('lightningOps.forwardHistoryAnyChannel')}</option>
                {channelOptions
                  .filter((channel) => channel.out_count > 0 || channel.channel_id_str === filters.chanOut)
                  .map((channel) => (
                    <option key={`out-${channel.channel_id_str}`} value={channel.channel_id_str}>
                      {channel.peer_alias}
                      {channel.open ? '' : ` (${t('lightningOps.forwardHistoryClosedChannel')})`} · {channel.out_count.toLocaleString(locale)}
                    </option>
                  ))}
              </select>
            </label>
            {filtered && (
              <button type="button" className="btn-secondary px-3 py-2 text-xs" onClick={() => update({ chanIn: '', chanOut: '' })}>
                {t('lightningOps.forwardHistoryClearFilters')}
              </button>
            )}
          </div>

          {filtered && (
            <div className="flex flex-wrap items-center gap-2 text-xs">
              {filters.chanIn && (
                <span className="inline-flex items-center gap-1 rounded-full border border-sky-300/40 bg-sky-500/10 px-3 py-1 text-sky-200">
                  {t('lightningOps.forwardHistoryFilterIn', { alias: aliasOf(filters.chanIn) })}
                  <button type="button" className="ml-1 text-sky-200/70 hover:text-sky-100" onClick={() => update({ chanIn: '' })} aria-label={t('lightningOps.forwardHistoryClearFilters')}>×</button>
                </span>
              )}
              {filters.chanOut && (
                <span className="inline-flex items-center gap-1 rounded-full border border-emerald-300/40 bg-emerald-500/10 px-3 py-1 text-emerald-200">
                  {t('lightningOps.forwardHistoryFilterOut', { alias: aliasOf(filters.chanOut) })}
                  <button type="button" className="ml-1 text-emerald-200/70 hover:text-emerald-100" onClick={() => update({ chanOut: '' })} aria-label={t('lightningOps.forwardHistoryClearFilters')}>×</button>
                </span>
              )}
            </div>
          )}

          {error && <p role="alert" className="text-sm text-ember">{error}</p>}
          {loading && !data && <p className="text-sm text-fog/60">{t('common.loading')}</p>}

          {data && totals && subset && (
            <>
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                <KpiTile
                  label={t('lightningOps.forwardHistoryVolume')}
                  value={`${formatSats(subset.amount_sat)} sats`}
                  total={filtered ? `${formatSats(totals.amount_sat)} sats` : null}
                  pct={share(subset.amount_sat, totals.amount_sat)}
                  color={VOLUME_COLOR}
                  filtered={filtered}
                  formatPct={formatPct}
                />
                <KpiTile
                  label={t('lightningOps.forwardHistoryFees')}
                  value={`${formatSats(subset.fee_sat)} sats`}
                  total={filtered ? `${formatSats(totals.fee_sat)} sats` : null}
                  pct={share(subset.fee_sat, totals.fee_sat)}
                  color={FEE_COLOR}
                  filtered={filtered}
                  formatPct={formatPct}
                />
                <KpiTile
                  label={t('lightningOps.forwardHistoryCount')}
                  value={subset.count.toLocaleString(locale)}
                  total={filtered ? totals.count.toLocaleString(locale) : null}
                  pct={share(subset.count, totals.count)}
                  color="#a78bfa"
                  filtered={filtered}
                  formatPct={formatPct}
                />
                <KpiTile
                  label={t('lightningOps.forwardHistoryAvgPpm')}
                  value={`${subset.avg_ppm.toLocaleString(locale)} ppm`}
                  total={filtered ? `${totals.avg_ppm.toLocaleString(locale)} ppm` : null}
                  pct={totals.avg_ppm > 0 ? Math.min(100, (subset.avg_ppm / totals.avg_ppm) * 100) : 0}
                  color="#f59e0b"
                  filtered={filtered}
                  formatPct={formatPct}
                  ratio
                />
              </div>

              <div className="rounded-2xl border border-white/10 bg-ink/40 p-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="text-sm font-medium">{t('lightningOps.forwardHistoryTimeline')}</p>
                  <p className="flex items-center gap-3 text-xs text-fog/55">
                    <span className="inline-flex items-center gap-1"><span className="inline-block h-2 w-3 rounded-sm" style={{ background: VOLUME_COLOR }} />{t('lightningOps.forwardHistoryVolume')}</span>
                    <span className="inline-flex items-center gap-1"><span className="inline-block h-0.5 w-3" style={{ background: FEE_COLOR }} />{t('lightningOps.forwardHistoryFees')}</span>
                  </p>
                </div>
                <div className="mt-3 h-56 w-full">
                  <ResponsiveContainer width="100%" height="100%">
                    <ComposedChart data={chart} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
                      <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                      <XAxis dataKey="label" tick={{ fill: 'rgba(229,231,235,0.6)', fontSize: 11 }} axisLine={false} tickLine={false} interval="preserveStartEnd" minTickGap={24} />
                      <YAxis yAxisId="amount" tickFormatter={formatCompact} tick={{ fill: 'rgba(229,231,235,0.6)', fontSize: 11 }} axisLine={false} tickLine={false} width={56} />
                      <YAxis yAxisId="fee" orientation="right" tickFormatter={formatCompact} tick={{ fill: 'rgba(52,211,153,0.8)', fontSize: 11 }} axisLine={false} tickLine={false} width={48} />
                      <Tooltip
                        contentStyle={tooltipStyle}
                        formatter={(value: number, name: string) => {
                          if (name === 'count') return [value.toLocaleString(locale), t('lightningOps.forwardHistoryCount')]
                          const label = name === 'fee' ? t('lightningOps.forwardHistoryFees') : t('lightningOps.forwardHistoryVolume')
                          return [`${formatSats(value)} sats`, label]
                        }}
                      />
                      <Bar yAxisId="amount" dataKey="amount" fill={VOLUME_COLOR} fillOpacity={0.55} radius={[4, 4, 0, 0]} isAnimationActive={false} />
                      <Line yAxisId="fee" type="monotone" dataKey="fee" stroke={FEE_COLOR} strokeWidth={2} dot={false} isAnimationActive={false} />
                      <Line yAxisId="amount" dataKey="count" stroke="transparent" dot={false} activeDot={false} isAnimationActive={false} legendType="none" />
                    </ComposedChart>
                  </ResponsiveContainer>
                </div>
              </div>

              <div className="grid gap-3 lg:grid-cols-2">
                <FlowPanel
                  title={t('lightningOps.forwardHistoryTopIn')}
                  hint={t('lightningOps.forwardHistoryFlowHint')}
                  entries={data.flow_in}
                  total={subset.amount_sat}
                  color={VOLUME_COLOR}
                  active={filters.chanIn}
                  onPick={(id) => update({ chanIn: filters.chanIn === id ? '' : id })}
                  formatSats={formatSats}
                  formatCompact={formatCompact}
                  formatPct={formatPct}
                  locale={locale}
                  feeLabel={t('lightningOps.forwardHistoryColFee')}
                />
                <FlowPanel
                  title={t('lightningOps.forwardHistoryTopOut')}
                  hint={t('lightningOps.forwardHistoryFlowHint')}
                  entries={data.flow_out}
                  total={subset.amount_sat}
                  color={FEE_COLOR}
                  active={filters.chanOut}
                  onPick={(id) => update({ chanOut: filters.chanOut === id ? '' : id })}
                  formatSats={formatSats}
                  formatCompact={formatCompact}
                  formatPct={formatPct}
                  locale={locale}
                  feeLabel={t('lightningOps.forwardHistoryColFee')}
                />
              </div>

              <div className="rounded-2xl border border-white/10 bg-ink/40">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-white/10 px-4 py-3 text-xs text-fog/60">
                  <span>{t('lightningOps.forwardHistoryShowing', { shown: items.length.toLocaleString(locale), total: subset.count.toLocaleString(locale) })}</span>
                  <span>{t('lightningOps.forwardHistorySortHint')}</span>
                </div>
                {items.length === 0 ? (
                  <p className="px-4 py-6 text-center text-sm text-fog/60">{filtered ? t('lightningOps.forwardHistoryEmptyFiltered') : t('lightningOps.forwardHistoryEmpty')}</p>
                ) : (
                  <div className="max-h-[480px] overflow-auto">
                    <table className="w-full min-w-[640px] text-sm">
                      <thead className="sticky top-0 bg-ink/95 text-xs uppercase tracking-wide text-fog/50 backdrop-blur">
                        <tr>
                          <SortableHeader label={t('lightningOps.forwardHistoryColTime')} active={filters.sort === 'time'} order={filters.order} onClick={() => toggleSort('time')} align="left" />
                          <th className="px-4 py-2 text-left font-medium">{t('lightningOps.forwardHistoryColRoute')}</th>
                          <SortableHeader label={t('lightningOps.forwardHistoryColAmount')} active={filters.sort === 'amount'} order={filters.order} onClick={() => toggleSort('amount')} align="right" />
                          <SortableHeader label={t('lightningOps.forwardHistoryColFee')} active={filters.sort === 'fee'} order={filters.order} onClick={() => toggleSort('fee')} align="right" />
                          <SortableHeader label="ppm" active={filters.sort === 'ppm'} order={filters.order} onClick={() => toggleSort('ppm')} align="right" />
                        </tr>
                      </thead>
                      <tbody>
                        {items.map((item) => (
                          <tr key={item.id} className="border-t border-white/5 transition hover:bg-white/[0.03]">
                            <td className="whitespace-nowrap px-4 py-2 font-mono text-xs text-fog/70">{formatTime(item.occurred_at)}</td>
                            <td className="px-4 py-2">
                              <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                                <button
                                  type="button"
                                  className={`max-w-[220px] truncate rounded-md px-1.5 py-0.5 text-left text-xs transition ${filters.chanIn === item.chan_id_in_str ? 'bg-sky-500/20 text-sky-100' : 'bg-white/5 text-fog/80 hover:bg-sky-500/15'}`}
                                  title={item.peer_pubkey_in || item.chan_id_in_str}
                                  onClick={() => update({ chanIn: filters.chanIn === item.chan_id_in_str ? '' : item.chan_id_in_str })}
                                >
                                  {item.peer_alias_in || '—'}
                                </button>
                                <span className="text-fog/40" aria-hidden="true">→</span>
                                <button
                                  type="button"
                                  className={`max-w-[220px] truncate rounded-md px-1.5 py-0.5 text-left text-xs transition ${filters.chanOut === item.chan_id_out_str ? 'bg-emerald-500/20 text-emerald-100' : 'bg-white/5 text-fog/80 hover:bg-emerald-500/15'}`}
                                  title={item.peer_pubkey_out || item.chan_id_out_str}
                                  onClick={() => update({ chanOut: filters.chanOut === item.chan_id_out_str ? '' : item.chan_id_out_str })}
                                >
                                  {item.peer_alias_out || '—'}
                                </button>
                              </div>
                            </td>
                            <td className="whitespace-nowrap px-4 py-2 text-right font-mono text-xs">{formatSats(item.amount_sat)}</td>
                            <td className="whitespace-nowrap px-4 py-2 text-right font-mono text-xs text-emerald-200">{item.fee_msat % 1000 === 0 ? formatSats(item.fee_sat) : (item.fee_msat / 1000).toLocaleString(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}</td>
                            <td className="whitespace-nowrap px-4 py-2 text-right font-mono text-xs text-fog/70">{item.ppm.toLocaleString(locale)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                {data.has_more && items.length > 0 && (
                  <div className="border-t border-white/10 px-4 py-3 text-center">
                    <button type="button" className="btn-secondary px-4 py-2 text-xs" disabled={loadingMore} onClick={() => void load(filters, items.length)}>
                      {loadingMore ? t('common.loading') : t('lightningOps.forwardHistoryLoadMore')}
                    </button>
                  </div>
                )}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}

type KpiTileProps = {
  label: string
  value: string
  total: string | null
  pct: number
  color: string
  filtered: boolean
  ratio?: boolean
  formatPct: (value: number) => string
}

function KpiTile({ label, value, total, pct, color, filtered, ratio, formatPct }: KpiTileProps) {
  const { t } = useTranslation()
  return (
    <div className="rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
      <p className="text-xs uppercase tracking-wide text-fog/45">{label}</p>
      <p className="mt-1 text-lg font-semibold tabular-nums">{value}</p>
      {filtered && total && (
        <p className="text-xs text-fog/55">
          {t('lightningOps.forwardHistoryOfTotal', { total })}
          {!ratio && <span className="ml-1 text-fog/80">· {formatPct(pct)}</span>}
        </p>
      )}
      <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-white/10">
        <div className="h-full rounded-full transition-all" style={{ width: `${filtered ? pct : 100}%`, background: color }} />
      </div>
    </div>
  )
}

type FlowPanelProps = {
  title: string
  hint: string
  entries: ForwardHistoryFlowEntry[]
  total: number
  color: string
  active: string
  onPick: (id: string) => void
  formatSats: (value: number) => string
  formatCompact: (value: number) => string
  formatPct: (value: number) => string
  locale: string
  feeLabel: string
}

function FlowPanel({ title, hint, entries, total, color, active, onPick, formatSats, formatCompact, formatPct, locale, feeLabel }: FlowPanelProps) {
  const max = entries.reduce((acc, entry) => Math.max(acc, entry.amount_sat), 0)
  return (
    <div className="rounded-2xl border border-white/10 bg-ink/40 p-4">
      <div className="flex items-center justify-between gap-2">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-xs text-fog/45">{hint}</p>
      </div>
      {entries.length === 0 ? (
        <p className="mt-3 text-xs text-fog/50">—</p>
      ) : (
        <ul className="mt-3 space-y-2">
          {entries.map((entry) => {
            const isActive = active === entry.channel_id_str
            return (
              <li key={entry.channel_id_str}>
                <button
                  type="button"
                  className={`w-full rounded-xl px-2 py-1.5 text-left transition ${isActive ? 'bg-white/10' : 'hover:bg-white/5'}`}
                  onClick={() => onPick(entry.channel_id_str)}
                  aria-pressed={isActive}
                  title={`${formatSats(entry.amount_sat)} sats · ${entry.count.toLocaleString(locale)} fwd · ${feeLabel} ${formatSats(entry.fee_sat)} sats`}
                >
                  <div className="flex items-center justify-between gap-3 text-xs">
                    <span className="min-w-0 truncate text-fog/85">{entry.peer_alias}</span>
                    <span className="shrink-0 font-mono text-fog/70">
                      {formatCompact(entry.amount_sat)} <span className="text-fog/40">·</span> <span style={{ color }}>{formatCompact(entry.fee_sat)}</span> <span className="text-fog/40">·</span> {formatPct(share(entry.amount_sat, total))}
                    </span>
                  </div>
                  <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-white/10">
                    <div className="h-full rounded-full" style={{ width: `${max > 0 ? (entry.amount_sat / max) * 100 : 0}%`, background: color, opacity: isActive ? 1 : 0.65 }} />
                  </div>
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

type SortableHeaderProps = {
  label: string
  active: boolean
  order: 'asc' | 'desc'
  onClick: () => void
  align: 'left' | 'right'
}

function SortableHeader({ label, active, order, onClick, align }: SortableHeaderProps) {
  return (
    <th className={`px-4 py-2 font-medium ${align === 'right' ? 'text-right' : 'text-left'}`} aria-sort={active ? (order === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button type="button" className={`inline-flex items-center gap-1 uppercase tracking-wide transition hover:text-fog ${active ? 'text-fog' : ''}`} onClick={onClick}>
        {label}
        <span className={`text-[10px] ${active ? 'opacity-100' : 'opacity-30'}`} aria-hidden="true">{active && order === 'asc' ? '▲' : '▼'}</span>
      </button>
    </th>
  )
}

