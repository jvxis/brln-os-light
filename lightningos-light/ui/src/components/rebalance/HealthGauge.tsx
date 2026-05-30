import type { HeroTone } from './HeroTile'

const fillClasses: Record<HeroTone, string> = {
  ok: 'from-emerald-400 to-emerald-300',
  warn: 'from-amber-400 to-yellow-300',
  danger: 'from-rose-400 to-rose-300',
  muted: 'from-slate-400 to-slate-300',
  info: 'from-sky-400 to-cyan-300',
}

type HealthGaugeProps = {
  label: string
  value: number
  target?: number
  max?: number
  valueLabel?: string
  tone?: HeroTone
  hint?: string
}

export default function HealthGauge({
  label,
  value,
  target,
  max = 100,
  valueLabel,
  tone = 'info',
  hint,
}: HealthGaugeProps) {
  const safeMax = max > 0 ? max : 100
  const pct = Math.max(0, Math.min(100, (value / safeMax) * 100))
  const markerPct = typeof target === 'number' ? Math.max(0, Math.min(100, (target / safeMax) * 100)) : null
  const valueText = valueLabel ?? `${pct.toFixed(0)}%`

  return (
    <div className="min-w-0 space-y-1" title={hint}>
      <div className="flex items-baseline justify-between gap-2 text-[11px]">
        <span className="uppercase tracking-wide text-fog/55">{label}</span>
        <span className="font-medium text-fog">{valueText}</span>
      </div>
      <div className="relative h-2 overflow-hidden rounded-full bg-white/8">
        <div className={`h-full rounded-full bg-gradient-to-r ${fillClasses[tone]}`} style={{ width: `${pct}%` }} />
        {markerPct !== null && (
          <div
            className="absolute top-[-2px] h-3 w-[2px] rounded bg-fog/60"
            style={{ left: `${markerPct}%` }}
            aria-label={`target ${target}`}
          />
        )}
      </div>
      {typeof target === 'number' && (
        <p className="text-[10px] text-fog/45">alvo {target}{max === 100 ? '%' : ''}</p>
      )}
    </div>
  )
}
