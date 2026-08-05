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
    <div className="horizontal-gauge min-w-0 space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-sm">
        <span className="min-w-0 text-fog/70 [overflow-wrap:anywhere]">{label}</span>
        <span className="min-w-0 text-right font-medium text-fog [overflow-wrap:anywhere]">{valueLabel ?? `${Math.round(percent)}%`}</span>
      </div>
      <div className="horizontal-gauge__track h-2 overflow-hidden rounded-full bg-white/8">
        <div
          className={`horizontal-gauge__fill h-full rounded-full bg-gradient-to-r ${fillClasses[tone]}`}
          style={{ width: `${percent}%` }}
        />
      </div>
      {detail ? <p className="text-xs text-fog/50 [overflow-wrap:anywhere]">{detail}</p> : null}
    </div>
  )
}
