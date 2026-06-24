import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { getLocale } from '../../i18n'
import { calculateApyPctFromNet, formatApyPercent } from '../../utils/apy'
import { formatSats, formatSignedSats, metricOffchainCost, metricTotalCost, metricNetWithKeysend } from './formatters'
import MetricTile from './MetricTile'
import type { LiveResponse, ReportRangeResponse, SummaryResponse } from './types'

type OperationsOverviewProps = {
  live: LiveResponse | null
  range: ReportRangeResponse | null
  summary: SummaryResponse | null
}

type ChartPoint = {
  label: string
  revenue: number
  cost: number
  net: number
}

const tooltipStyle = {
  background: '#0f172a',
  borderRadius: 12,
  border: '1px solid rgba(255,255,255,0.1)',
  color: '#f8fafc',
}

export default function OperationsOverview({ live, range, summary }: OperationsOverviewProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const periodSeries = Array.isArray(range?.series) ? range.series : []
  const periodDays = summary?.days ?? periodSeries.length
  const periodNet = summary?.totals
    ? metricNetWithKeysend(summary.totals)
    : periodSeries.reduce((sum, item) => sum + metricNetWithKeysend(item), 0)
  const periodApy = calculateApyPctFromNet(periodNet, periodDays, periodSeries)

  const chartData = useMemo<ChartPoint[]>(
    () => (Array.isArray(range?.series) ? [...range.series] : [])
      .sort((a, b) => a.date.localeCompare(b.date))
      .map((item) => ({
        label: new Date(`${item.date}T00:00:00`).toLocaleDateString(locale, { day: '2-digit', month: 'short' }),
        revenue: item.forward_fee_revenue_sats ?? 0,
        cost: metricTotalCost(item),
        net: metricNetWithKeysend(item),
      })),
    [locale, range?.series]
  )

  const yTickFormatter = (value: number) => new Intl.NumberFormat(locale, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value)

  return (
    <article className="section-card">
      <div className="flex flex-col gap-2 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <p className="text-xs uppercase tracking-[0.24em] text-fog/45">{t('dashboard.operationsKicker')}</p>
          <h3 className="mt-2 text-xl font-semibold">{t('dashboard.operationsTitle')}</h3>
          <p className="mt-2 text-sm text-fog/60">{t('dashboard.operationsSubtitle')}</p>
        </div>
        <div className="rounded-full border border-white/10 bg-white/5 px-4 py-2 text-sm text-fog/70">
          {summary?.days ? t('dashboard.monthTrendHint', { count: summary.days }) : t('dashboard.todayWindow')}
        </div>
      </div>

      <div className="mt-6 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <MetricTile
          label={t('reports.revenue')}
          value={`${formatSats(locale, live?.forward_fee_revenue_sats)} sats`}
          sublabel={t('dashboard.todayWindow')}
          tone="info"
        />
        <MetricTile
          label={t('reports.cost')}
          value={`${formatSats(locale, metricOffchainCost(live))} sats`}
          sublabel={t('dashboard.offchainCostLabel')}
          detail={`${t('dashboard.onchainCostLabel')}: ${formatSats(locale, live?.onchain_fee_cost_sats ?? 0)} sats`}
          tone="warn"
        />
        <MetricTile
          label={t('reports.netWithKeysend')}
          value={`${formatSignedSats(locale, metricNetWithKeysend(live))} sats`}
          sublabel={t('dashboard.todayWindow')}
          tone={metricNetWithKeysend(live) >= 0 ? 'ok' : 'danger'}
        />
        <MetricTile
          label={t('reports.routedVolume')}
          value={`${formatSats(locale, live?.routed_volume_sats)} sats`}
          sublabel={t('reports.forwardCount')}
          detail={formatSats(locale, live?.forward_count)}
          tone="info"
        />
        <MetricTile
          label={t('reports.rebalances')}
          value={formatSats(locale, live?.rebalance_count)}
          sublabel={t('reports.rebalanceVolume')}
          detail={`${formatSats(locale, live?.rebalance_volume_sats ?? 0)} sats`}
          tone="warn"
        />
        <MetricTile
          label={t('dashboard.paymentsLabel')}
          value={formatSats(locale, live?.payment_count)}
          sublabel={t('dashboard.monthAverageLabel')}
          detail={`${formatSats(locale, summary?.averages.payment_count ?? 0)} / ${t('dashboard.dayLabel')}`}
          tone="muted"
        />
      </div>

      <div className="mt-6 rounded-3xl border border-white/10 bg-white/5 p-4">
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h4 className="text-base font-semibold">{t('dashboard.monthTrendTitle')}</h4>
            <p className="text-sm text-fog/60">{t('dashboard.monthTrendChartHint')}</p>
          </div>
          <div className="grid grid-cols-3 gap-2 text-xs text-fog/60">
            <span className="rounded-full border border-emerald-400/20 bg-emerald-500/10 px-3 py-1 text-center text-emerald-100">{t('reports.net')}</span>
            <span className="rounded-full border border-sky-400/20 bg-sky-500/10 px-3 py-1 text-center text-sky-100">{t('reports.revenue')}</span>
            <span className="rounded-full border border-amber-400/20 bg-amber-500/10 px-3 py-1 text-center text-amber-100">{t('reports.cost')}</span>
          </div>
        </div>

        <div className="mt-4 h-72">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={chartData} margin={{ top: 12, right: 8, left: -16, bottom: 0 }}>
              <defs>
                <linearGradient id="operations-net" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#34d399" stopOpacity={0.4} />
                  <stop offset="100%" stopColor="#34d399" stopOpacity={0.04} />
                </linearGradient>
                <linearGradient id="operations-revenue" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#38bdf8" stopOpacity={0.28} />
                  <stop offset="100%" stopColor="#38bdf8" stopOpacity={0.02} />
                </linearGradient>
                <linearGradient id="operations-cost" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#f59e0b" stopOpacity={0.22} />
                  <stop offset="100%" stopColor="#f59e0b" stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
              <XAxis dataKey="label" tick={{ fill: 'rgba(229,231,235,0.6)', fontSize: 12 }} axisLine={false} tickLine={false} />
              <YAxis tickFormatter={yTickFormatter} tick={{ fill: 'rgba(229,231,235,0.6)', fontSize: 12 }} axisLine={false} tickLine={false} width={64} />
              <Tooltip
                contentStyle={tooltipStyle}
                formatter={(value: number) => `${formatSats(locale, value)} sats`}
                labelFormatter={(label) => label}
              />
              <Area type="monotone" dataKey="net" stroke="#34d399" fill="url(#operations-net)" strokeWidth={2.2} isAnimationActive={false} />
              <Area type="monotone" dataKey="revenue" stroke="#38bdf8" fill="url(#operations-revenue)" strokeWidth={1.8} isAnimationActive={false} />
              <Area type="monotone" dataKey="cost" stroke="#f59e0b" fill="url(#operations-cost)" strokeWidth={1.8} isAnimationActive={false} />
            </AreaChart>
          </ResponsiveContainer>
        </div>

        <div className="mt-4 grid gap-3 sm:grid-cols-3">
          <div className="rounded-2xl border border-white/10 bg-ink/35 px-4 py-3">
            <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.monthNetTitle')}</p>
            <div className="mt-2 flex flex-wrap items-baseline justify-between gap-2">
              <p className="text-lg font-semibold">{formatSignedSats(locale, metricNetWithKeysend(summary?.totals))} sats</p>
              {periodApy ? (
                <span className={`rounded-full border px-2.5 py-1 text-xs font-semibold ${
                  periodApy.apyPct < 0
                    ? 'border-rose-400/25 bg-rose-500/10 text-rose-200'
                    : 'border-emerald-400/25 bg-emerald-500/10 text-emerald-200'
                }`}>
                  {t('reports.apy')} {formatApyPercent(locale, periodApy.apyPct)}
                </span>
              ) : null}
            </div>
          </div>
          <div className="rounded-2xl border border-white/10 bg-ink/35 px-4 py-3">
            <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.monthRevenueTitle')}</p>
            <p className="mt-2 text-lg font-semibold">{formatSats(locale, summary?.totals.forward_fee_revenue_sats)} sats</p>
          </div>
          <div className="rounded-2xl border border-white/10 bg-ink/35 px-4 py-3">
            <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.monthCostTitle')}</p>
            <p className="mt-2 text-lg font-semibold">{formatSats(locale, metricTotalCost(summary?.totals))} sats</p>
          </div>
        </div>
      </div>
    </article>
  )
}
