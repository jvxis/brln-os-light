import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import { calculateNetRevenueYieldPct, formatApyPercent } from '../../utils/apy'
import { clamp, formatPercent, formatSats, formatSignedSats, metricNetWithKeysend } from './formatters'
import MetricTile from './MetricTile'
import StackedRatioBar from './StackedRatioBar'
import type { LiveResponse, LndStatus, MovementLiveResponse, ReportRangeResponse, SummaryResponse } from './types'

type NodePulseRowProps = {
  live: LiveResponse | null
  range: ReportRangeResponse | null
  summary: SummaryResponse | null
  movement: MovementLiveResponse | null
  lnd: LndStatus | null
}

export default function NodePulseRow({
  live,
  range,
  summary,
  movement,
  lnd,
}: NodePulseRowProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)

  const sparklineSource = Array.isArray(range?.series) ? [...range.series].sort((a, b) => a.date.localeCompare(b.date)) : []
  const revenueTrend = sparklineSource.map((item) => ({ value: item.forward_fee_revenue_sats ?? 0 }))
  const netTrend = sparklineSource.map((item) => ({ value: metricNetWithKeysend(item) }))
  const volumeTrend = sparklineSource.map((item) => ({ value: item.routed_volume_sats ?? 0 }))
  const periodNet = summary?.totals
    ? metricNetWithKeysend(summary.totals)
    : sparklineSource.reduce((sum, item) => sum + metricNetWithKeysend(item), 0)
  const periodRevenue = summary?.totals.forward_fee_revenue_sats
    ?? sparklineSource.reduce((sum, item) => sum + (item.forward_fee_revenue_sats ?? 0), 0)

  const totalLiquidity = (lnd?.balances?.onchain_sat ?? 0) + (lnd?.balances?.lightning_sat ?? 0)
  const onchainLiquidity = lnd?.balances?.onchain_sat ?? 0
  const lightningLiquidity = lnd?.balances?.lightning_sat ?? 0
  const movementPct = movement?.movement_pct ?? 0
  const movementProgress = clamp(movementPct)
  const movementTone = movementPct >= 75 ? 'ok' : movementPct >= 50 ? 'warn' : 'danger'
  const monthDays = summary?.days ?? sparklineSource.length
  const periodApy = calculateNetRevenueYieldPct(periodNet, periodRevenue)
  const periodTrendDetail = monthDays > 0 ? t('dashboard.monthTrendHint', { count: monthDays }) : undefined
  const netTrendDetail = periodTrendDetail
    ? (
      <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1">
        <span>{periodTrendDetail}</span>
        {periodApy !== null ? (
          <span className={periodApy < 0 ? 'text-rose-300' : 'text-emerald-300'}>
            {t('reports.apy')} {formatApyPercent(locale, periodApy)}
          </span>
        ) : null}
      </span>
    )
    : undefined

  return (
    <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
      <MetricTile
        label={t('reports.revenue')}
        value={`${formatSats(locale, live?.forward_fee_revenue_sats)} sats`}
        sublabel={t('dashboard.todayWindow')}
        detail={monthDays > 0 ? t('dashboard.monthTrendHint', { count: monthDays }) : undefined}
        tone="info"
        trend={revenueTrend}
      />

      <MetricTile
        label={t('reports.netWithKeysend')}
        value={`${formatSignedSats(locale, metricNetWithKeysend(live))} sats`}
        sublabel={t('dashboard.todayWindow')}
        detail={netTrendDetail}
        tone={metricNetWithKeysend(live) >= 0 ? 'ok' : 'danger'}
        trend={netTrend}
      />

      <MetricTile
        label={t('reports.routedVolume')}
        value={`${formatSats(locale, live?.routed_volume_sats)} sats`}
        sublabel={t('dashboard.todayWindow')}
        detail={t('dashboard.routingCountHint', {
          forwards: live?.forward_count ?? 0,
          rebalances: live?.rebalance_count ?? 0,
        })}
        tone="info"
        trend={volumeTrend}
      />

      <MetricTile
        label={t('dashboard.movementToday')}
        value={`${formatPercent(locale, movementPct)}%`}
        sublabel={
          movement?.outbound_target_sats
            ? t('dashboard.movementTargetHint', { target: formatSats(locale, movement.outbound_target_sats) })
            : t('reports.movementProgressUnavailable')
        }
        detail={movement?.routed_volume_sats ? t('dashboard.movementRoutedHint', { routed: formatSats(locale, movement.routed_volume_sats) }) : undefined}
        tone={movementTone}
      >
        <div className="h-2 overflow-hidden rounded-full bg-white/8">
          <div
            className={`h-full rounded-full ${
              movementTone === 'ok'
                ? 'bg-gradient-to-r from-emerald-400 to-emerald-300'
                : movementTone === 'warn'
                  ? 'bg-gradient-to-r from-amber-400 to-yellow-300'
                  : 'bg-gradient-to-r from-rose-400 to-rose-300'
            }`}
            style={{ width: `${movementProgress}%` }}
          />
        </div>
      </MetricTile>

      <MetricTile
        label={t('dashboard.liquiditySplit')}
        value={`${formatSats(locale, totalLiquidity)} sats`}
        sublabel={t('dashboard.balanceSummary', {
          onchain: formatSats(locale, onchainLiquidity),
          lightning: formatSats(locale, lightningLiquidity),
        })}
        tone="info"
      >
        <StackedRatioBar
          compact
          segments={[
            { label: t('dashboard.onchainShort'), value: onchainLiquidity, tone: 'ok' },
            { label: t('dashboard.lightningShort'), value: lightningLiquidity, tone: 'info' },
          ]}
        />
      </MetricTile>
    </section>
  )
}
