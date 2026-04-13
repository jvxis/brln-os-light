import type { ReactNode } from 'react'
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
  children,
}: MetricTileProps) {
  return (
    <article className={`rounded-3xl border border-white/10 bg-gradient-to-br ${accentClasses[tone]} to-transparent p-4 shadow-panel`}>
      <div className="flex items-start justify-between gap-3">
        <p className="text-xs uppercase tracking-[0.24em] text-fog/45">{label}</p>
        {badgeLabel ? <StatusBadge label={badgeLabel} tone={tone} /> : null}
      </div>
      <div className="mt-4">
        <div className="text-2xl font-semibold leading-none text-fog">{value}</div>
        {sublabel ? <p className="mt-2 text-sm text-fog/68">{sublabel}</p> : null}
        {detail ? <p className="mt-1 text-xs text-fog/50">{detail}</p> : null}
      </div>
      {trend && trend.length > 1 ? <div className="mt-3"><MiniSparkline data={trend} tone={tone} /></div> : null}
      {children ? <div className="mt-3">{children}</div> : null}
    </article>
  )
}
