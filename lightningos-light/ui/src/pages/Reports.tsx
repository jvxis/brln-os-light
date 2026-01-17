import { useEffect, useMemo, useState } from 'react'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis
} from 'recharts'
import { getReportsLive, getReportsRange, getReportsSummary } from '../api'

type ReportSeriesItem = {
  date: string
  forward_fee_revenue_sats: number
  rebalance_fee_cost_sats: number
  net_routing_profit_sats: number
  forward_count: number
  rebalance_count: number
  routed_volume_sats: number
  onchain_balance_sats?: number | null
  lightning_balance_sats?: number | null
  total_balance_sats?: number | null
}

type ReportMetrics = {
  forward_fee_revenue_sats: number
  rebalance_fee_cost_sats: number
  net_routing_profit_sats: number
  forward_count: number
  rebalance_count: number
  routed_volume_sats: number
  onchain_balance_sats?: number | null
  lightning_balance_sats?: number | null
  total_balance_sats?: number | null
}

type SeriesResponse = {
  range: string
  timezone: string
  series: ReportSeriesItem[]
}

type SummaryResponse = {
  range: string
  timezone: string
  days: number
  totals: ReportMetrics
  averages: ReportMetrics
}

type LiveResponse = ReportMetrics & {
  start: string
  end: string
  timezone: string
}

type RangeKey = 'd-1' | 'month' | '3m' | '6m' | '12m' | 'all'

type RangeOption = {
  key: RangeKey
  label: string
}

const rangeOptions: RangeOption[] = [
  { key: 'd-1', label: 'D-1' },
  { key: 'month', label: 'Month' },
  { key: '3m', label: '3 months' },
  { key: '6m', label: '6 months' },
  { key: '12m', label: '12 months' },
  { key: 'all', label: 'All time' }
]

const formatter = new Intl.NumberFormat('en-US', { maximumFractionDigits: 3 })
const compactFormatter = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 2 })

const COLORS = {
  net: '#34d399',
  revenue: '#38bdf8',
  cost: '#f59e0b',
  onchain: '#22c55e',
  lightning: '#fb7185',
  total: '#eab308'
}

const formatSats = (value: number) => `${formatter.format(value)} sats`
const formatCompact = (value: number) => compactFormatter.format(value)

const formatDateLabel = (value: string) => {
  const parsed = new Date(`${value}T00:00:00`)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return parsed.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

const formatDateLong = (value: string) => {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return parsed.toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  })
}

export default function Reports() {
  const [range, setRange] = useState<RangeKey>('d-1')
  const [series, setSeries] = useState<ReportSeriesItem[]>([])
  const [summary, setSummary] = useState<SummaryResponse | null>(null)
  const [live, setLive] = useState<LiveResponse | null>(null)
  const [seriesLoading, setSeriesLoading] = useState(true)
  const [seriesError, setSeriesError] = useState('')
  const [liveLoading, setLiveLoading] = useState(true)
  const [liveError, setLiveError] = useState('')

  useEffect(() => {
    let active = true
    setSeriesLoading(true)
    setSeriesError('')

    Promise.all([getReportsRange(range), getReportsSummary(range)])
      .then(([rangeResp, summaryResp]) => {
        if (!active) return
        const typedRange = rangeResp as SeriesResponse
        const typedSummary = summaryResp as SummaryResponse
        setSeries(Array.isArray(typedRange.series) ? typedRange.series : [])
        setSummary(typedSummary)
      })
      .catch((err) => {
        if (!active) return
        setSeriesError(err instanceof Error ? err.message : 'Reports unavailable')
        setSeries([])
        setSummary(null)
      })
      .finally(() => {
        if (!active) return
        setSeriesLoading(false)
      })

    return () => {
      active = false
    }
  }, [range])

  useEffect(() => {
    let active = true
    const loadLive = () => {
      setLiveLoading(true)
      setLiveError('')
      getReportsLive()
        .then((data) => {
          if (!active) return
          setLive(data as LiveResponse)
        })
        .catch((err) => {
          if (!active) return
          setLiveError(err instanceof Error ? err.message : 'Live report unavailable')
        })
        .finally(() => {
          if (!active) return
          setLiveLoading(false)
        })
    }

    loadLive()
    const timer = window.setInterval(loadLive, 60000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [])

  const chartData = useMemo(() => {
    return series.map((item) => ({
      date: item.date,
      net: item.net_routing_profit_sats,
      revenue: item.forward_fee_revenue_sats,
      cost: item.rebalance_fee_cost_sats,
      onchain: item.onchain_balance_sats ?? null,
      lightning: item.lightning_balance_sats ?? null,
      total: item.total_balance_sats ?? null
    }))
  }, [series])

  const liveChartData = useMemo(() => {
    if (!live) return []
    return [
      { name: 'Revenue', value: live.forward_fee_revenue_sats, color: COLORS.revenue },
      { name: 'Cost', value: live.rebalance_fee_cost_sats, color: COLORS.cost },
      { name: 'Net', value: live.net_routing_profit_sats, color: COLORS.net }
    ]
  }, [live])

  const hasBalances = chartData.some((item) => item.onchain !== null || item.lightning !== null || item.total !== null)

  return (
    <section className="space-y-6">
      <div className="section-card flex flex-wrap items-center justify-between gap-4">
        <div>
          <h2 className="text-2xl font-semibold">Reports</h2>
          <p className="text-fog/60">Daily routing performance, rebalances, and balance history.</p>
        </div>
        <span className="text-xs uppercase tracking-wide text-fog/60">Updated daily at 00:00 local time</span>
      </div>

      <div className="grid gap-6 lg:grid-cols-5">
        <div className="section-card lg:col-span-3 space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">Live results</h3>
            <span className="text-xs text-fog/60">Today 00:00 ? now</span>
          </div>
          {liveLoading && !live && <p className="text-sm text-fog/60">Loading live metrics...</p>}
          {liveError && <p className="text-sm text-brass">{liveError}</p>}
          {!liveLoading && !liveError && live && (
            <>
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="rounded-2xl bg-white/5 p-4">
                  <p className="text-xs uppercase tracking-wide text-fog/60">Revenue</p>
                  <p className="text-lg font-semibold text-fog">{formatSats(live.forward_fee_revenue_sats)}</p>
                </div>
                <div className="rounded-2xl bg-white/5 p-4">
                  <p className="text-xs uppercase tracking-wide text-fog/60">Cost</p>
                  <p className="text-lg font-semibold text-fog">{formatSats(live.rebalance_fee_cost_sats)}</p>
                </div>
                <div className="rounded-2xl bg-white/5 p-4">
                  <p className="text-xs uppercase tracking-wide text-fog/60">Net</p>
                  <p className="text-lg font-semibold text-fog">{formatSats(live.net_routing_profit_sats)}</p>
                </div>
              </div>
              <div className="grid gap-3 sm:grid-cols-2 text-sm text-fog/70">
                <div className="flex items-center justify-between rounded-2xl bg-white/5 px-4 py-3">
                  <span>Forward count</span>
                  <span className="text-fog">{formatter.format(live.forward_count)}</span>
                </div>
                <div className="flex items-center justify-between rounded-2xl bg-white/5 px-4 py-3">
                  <span>Rebalance count</span>
                  <span className="text-fog">{formatter.format(live.rebalance_count)}</span>
                </div>
              </div>
              <div className="h-44">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={liveChartData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                    <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                    <XAxis dataKey="name" tick={{ fill: '#cbd5f5', fontSize: 12 }} axisLine={false} tickLine={false} />
                    <YAxis tick={{ fill: '#cbd5f5', fontSize: 11 }} axisLine={false} tickLine={false} tickFormatter={formatCompact} />
                    <Tooltip
                      cursor={{ fill: 'rgba(255,255,255,0.06)' }}
                      contentStyle={{ background: '#0f172a', borderRadius: 12, border: '1px solid rgba(255,255,255,0.1)' }}
                      formatter={(value) => formatSats(Number(value))}
                    />
                    <Bar dataKey="value" radius={[8, 8, 8, 8]}>
                      {liveChartData.map((entry) => (
                        <Cell key={entry.name} fill={entry.color} />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>
              <p className="text-xs text-fog/50">Last updated {formatDateLong(live.end)}.</p>
            </>
          )}
        </div>

        <div className="section-card lg:col-span-2 space-y-4">
          <h3 className="text-lg font-semibold">Historical range</h3>
          <div className="flex flex-wrap gap-2">
            {rangeOptions.map((option) => (
              <button
                key={option.key}
                type="button"
                className={range === option.key ? 'btn-primary' : 'btn-secondary'}
                onClick={() => setRange(option.key)}
              >
                {option.label}
              </button>
            ))}
          </div>
          {seriesLoading && series.length === 0 && <p className="text-sm text-fog/60">Loading range...</p>}
          {seriesError && <p className="text-sm text-brass">{seriesError}</p>}
          {!seriesLoading && !seriesError && summary && (
            <div className="space-y-3 text-sm text-fog/70">
              <div className="rounded-2xl bg-white/5 px-4 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/50">Totals</p>
                <p className="text-fog">Revenue {formatSats(summary.totals.forward_fee_revenue_sats)}</p>
                <p className="text-fog">Cost {formatSats(summary.totals.rebalance_fee_cost_sats)}</p>
                <p className="text-fog">Net {formatSats(summary.totals.net_routing_profit_sats)}</p>
              </div>
              <div className="rounded-2xl bg-white/5 px-4 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/50">Averages per day</p>
                <p className="text-fog">Revenue {formatSats(summary.averages.forward_fee_revenue_sats)}</p>
                <p className="text-fog">Cost {formatSats(summary.averages.rebalance_fee_cost_sats)}</p>
                <p className="text-fog">Net {formatSats(summary.averages.net_routing_profit_sats)}</p>
              </div>
              <div className="rounded-2xl bg-white/5 px-4 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/50">Activity</p>
                <p className="text-fog">Forwards {formatter.format(summary.totals.forward_count)}</p>
                <p className="text-fog">Rebalances {formatter.format(summary.totals.rebalance_count)}</p>
                <p className="text-fog">Routed volume {formatSats(summary.totals.routed_volume_sats)}</p>
              </div>
              <p className="text-xs text-fog/50">Based on {summary.days} stored day(s).</p>
            </div>
          )}
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">Net routing profit</h3>
            <span className="text-xs text-fog/60">Daily</span>
          </div>
          {chartData.length === 0 && !seriesLoading && !seriesError ? (
            <p className="text-sm text-fog/60">No data yet for this range.</p>
          ) : (
            <div className="h-64">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                  <defs>
                    <linearGradient id="netGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor={COLORS.net} stopOpacity={0.5} />
                      <stop offset="95%" stopColor={COLORS.net} stopOpacity={0.05} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                  <XAxis dataKey="date" tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatDateLabel} axisLine={false} tickLine={false} />
                  <YAxis tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatCompact} axisLine={false} tickLine={false} />
                  <Tooltip
                    cursor={{ stroke: 'rgba(255,255,255,0.1)', strokeWidth: 1 }}
                    contentStyle={{ background: '#0f172a', borderRadius: 12, border: '1px solid rgba(255,255,255,0.1)' }}
                    formatter={(value) => formatSats(Number(value))}
                    labelFormatter={formatDateLabel}
                  />
                  <Area type="monotone" dataKey="net" stroke={COLORS.net} fill="url(#netGradient)" strokeWidth={2} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}
        </div>

        <div className="section-card space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">Revenue vs cost</h3>
            <span className="text-xs text-fog/60">Daily</span>
          </div>
          {chartData.length === 0 && !seriesLoading && !seriesError ? (
            <p className="text-sm text-fog/60">No data yet for this range.</p>
          ) : (
            <div className="h-64">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={chartData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                  <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                  <XAxis dataKey="date" tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatDateLabel} axisLine={false} tickLine={false} />
                  <YAxis tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatCompact} axisLine={false} tickLine={false} />
                  <Legend verticalAlign="top" height={24} formatter={(value) => <span className="text-xs text-fog/60">{value}</span>} />
                  <Tooltip
                    cursor={{ stroke: 'rgba(255,255,255,0.1)', strokeWidth: 1 }}
                    contentStyle={{ background: '#0f172a', borderRadius: 12, border: '1px solid rgba(255,255,255,0.1)' }}
                    formatter={(value) => formatSats(Number(value))}
                    labelFormatter={formatDateLabel}
                  />
                  <Line type="monotone" dataKey="revenue" name="Revenue" stroke={COLORS.revenue} strokeWidth={2} dot={false} />
                  <Line type="monotone" dataKey="cost" name="Cost" stroke={COLORS.cost} strokeWidth={2} dot={false} />
                </LineChart>
              </ResponsiveContainer>
            </div>
          )}
        </div>
      </div>

      <div className="section-card space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-lg font-semibold">Balances</h3>
          <span className="text-xs text-fog/60">On-chain vs lightning vs total</span>
        </div>
        {!hasBalances && !seriesLoading && !seriesError ? (
          <p className="text-sm text-fog/60">Balance history not available yet.</p>
        ) : (
          <div className="h-72">
            <ResponsiveContainer width="100%" height="100%">
              <LineChart data={chartData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                <XAxis dataKey="date" tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatDateLabel} axisLine={false} tickLine={false} />
                <YAxis tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatCompact} axisLine={false} tickLine={false} />
                <Legend verticalAlign="top" height={24} formatter={(value) => <span className="text-xs text-fog/60">{value}</span>} />
                <Tooltip
                  cursor={{ stroke: 'rgba(255,255,255,0.1)', strokeWidth: 1 }}
                  contentStyle={{ background: '#0f172a', borderRadius: 12, border: '1px solid rgba(255,255,255,0.1)' }}
                  formatter={(value) => formatSats(Number(value))}
                  labelFormatter={formatDateLabel}
                />
                <Line type="monotone" dataKey="onchain" name="On-chain" stroke={COLORS.onchain} strokeWidth={2} dot={false} />
                <Line type="monotone" dataKey="lightning" name="Lightning" stroke={COLORS.lightning} strokeWidth={2} dot={false} />
                <Line type="monotone" dataKey="total" name="Total" stroke={COLORS.total} strokeWidth={2} dot={false} />
              </LineChart>
            </ResponsiveContainer>
          </div>
        )}
      </div>
    </section>
  )
}
