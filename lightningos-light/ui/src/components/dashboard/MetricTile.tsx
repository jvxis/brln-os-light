import type { ReactNode } from 'react'
import ThemeGauge from '../ThemeGauge'
import MiniSparkline from './MiniSparkline'
import StatusBadge from './StatusBadge'
import type { Tone } from './types'

type MetricTileProps = {
  label: string
  value: ReactNode
  sublabel?: ReactNode
  detail?: ReactNode
  tone?: Tone
  badgeLabel?: ReactNode
  trend?: Array<{ value: number }>
  gaugePct?: number
  gaugeMarker?: number
  gaugeMarkerLabel?: string
  children?: ReactNode
}

const accentClasses: Record<Tone, string> = {
  ok: 'from-emerald-500/14',
  warn: 'from-amber-500/14',
  danger: 'from-rose-500/14',
  muted: 'from-white/8',
  info: 'from-sky-500/14',
}

export default function MetricTile({
  label,
  value,
  sublabel,
  detail,
  tone = 'muted',
  badgeLabel,
  trend,
  gaugePct,
  gaugeMarker,
  gaugeMarkerLabel,
  children,
}: MetricTileProps) {
  const showGauge = typeof gaugePct === 'number'

  return (
    <article className={`metric-tile instrument-card${showGauge ? ' instrument-card--gauge' : ''} rounded-3xl border border-white/10 bg-gradient-to-br ${accentClasses[tone]} to-transparent p-4 shadow-panel`}>
      <div className="flex min-w-0 flex-wrap items-start justify-between gap-3">
        <p className="min-w-0 text-xs uppercase tracking-[0.24em] text-fog/45">{label}</p>
        {badgeLabel ? <StatusBadge label={badgeLabel} tone={tone} /> : null}
      </div>
      <div className="metric-tile__body mt-4">
        <div className="metric-tile__value text-2xl font-semibold leading-none text-fog">{value}</div>
      </div>
      <div className="metric-tile__context">
        {sublabel ? <p className="mt-2 text-sm text-fog/68">{sublabel}</p> : null}
        {detail ? <p className="mt-1 text-xs text-fog/50">{detail}</p> : null}
      </div>
      {showGauge ? <ThemeGauge value={gaugePct} marker={gaugeMarker} markerLabel={gaugeMarkerLabel} tone={tone} readout={value} /> : null}
      {trend && trend.length > 1 ? <div className="metric-tile__trend mt-3"><MiniSparkline data={trend} tone={tone} /></div> : null}
      {children ? <div className="metric-tile__children mt-3">{children}</div> : null}
    </article>
  )
}
