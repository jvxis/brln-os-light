import { clamp } from './formatters'
import type { Tone } from './types'

type HorizontalBarGaugeProps = {
  label: string
  value: number
  max?: number
  valueLabel?: string
  detail?: string
  tone?: Tone
}

const fillClasses: Record<Tone, string> = {
  ok: 'from-emerald-400 to-emerald-300',
  warn: 'from-amber-400 to-yellow-300',
  danger: 'from-rose-400 to-rose-300',
  muted: 'from-slate-400 to-slate-300',
  info: 'from-sky-400 to-cyan-300',
}

export default function HorizontalBarGauge({
  label,
  value,
  max = 100,
  valueLabel,
  detail,
  tone = 'info',
}: HorizontalBarGaugeProps) {
  const percent = max > 0 ? clamp((value / max) * 100) : 0

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-3 text-sm">
        <span className="text-fog/70">{label}</span>
        <span className="font-medium text-fog">{valueLabel ?? `${Math.round(percent)}%`}</span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-white/8">
        <div
          className={`h-full rounded-full bg-gradient-to-r ${fillClasses[tone]}`}
          style={{ width: `${percent}%` }}
        />
      </div>
      {detail ? <p className="text-xs text-fog/50">{detail}</p> : null}
    </div>
  )
}
