import type { HeroTone } from './HeroTile'
import ThemeGauge from '../ThemeGauge'

type HealthGaugeProps = {
  label: string
  value: number
  target?: number
  targetLabel?: string
  max?: number
  valueLabel?: string
  tone?: HeroTone
  hint?: string
}

export default function HealthGauge({
  label,
  value,
  target,
  targetLabel,
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
    <div className="health-gauge min-w-0 space-y-1" title={hint}>
      <div className="flex items-baseline justify-between gap-2 text-[11px]">
        <span className="uppercase tracking-wide text-fog/55">{label}</span>
        <span className="font-medium text-fog">{valueText}</span>
      </div>
      <ThemeGauge value={pct} marker={markerPct} markerLabel={targetLabel} tone={tone} />
      {typeof target === 'number' && targetLabel && (
        <p className="text-[10px] text-fog/45">{targetLabel}</p>
      )}
    </div>
  )
}
