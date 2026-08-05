import type { CSSProperties } from 'react'

export type ThemeGaugeTone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

const fillClasses: Record<ThemeGaugeTone, string> = {
  ok: 'from-emerald-400 to-emerald-300',
  warn: 'from-amber-400 to-yellow-300',
  danger: 'from-rose-400 to-rose-300',
  muted: 'from-slate-400 to-slate-300',
  info: 'from-sky-400 to-cyan-300',
}

type GaugeStyle = CSSProperties & {
  '--instrument-pct': number
  '--instrument-angle': string
  '--instrument-marker-angle': string
}

type ThemeGaugeProps = {
  value: number
  marker?: number | null
  markerLabel?: string
  tone?: ThemeGaugeTone
  className?: string
}

const clampPct = (value: number) => Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0))

export default function ThemeGauge({
  value,
  marker,
  markerLabel,
  tone = 'info',
  className = '',
}: ThemeGaugeProps) {
  const pct = clampPct(value)
  const markerPct = typeof marker === 'number' ? clampPct(marker) : null
  const style: GaugeStyle = {
    '--instrument-pct': pct,
    '--instrument-angle': `${-135 + (pct * 2.7)}deg`,
    '--instrument-marker-angle': `${-135 + ((markerPct ?? 0) * 2.7)}deg`,
  }

  return (
    <div className={`theme-gauge theme-gauge--${tone} ${className}`.trim()} style={style}>
      <div className="theme-gauge__bar" aria-hidden="true">
        <div className={`theme-gauge__fill bg-gradient-to-r ${fillClasses[tone]}`} style={{ width: `${pct}%` }} />
        {markerPct !== null ? (
          <span
            className="theme-gauge__marker"
            style={{ left: `${markerPct}%` }}
            title={markerLabel}
          />
        ) : null}
      </div>

      <div className="theme-gauge__dial" aria-hidden="true">
        <span className="theme-gauge__dial-progress" />
        <span className="theme-gauge__dial-ticks" />
        {markerPct !== null ? <span className="theme-gauge__dial-marker" /> : null}
        <span className="theme-gauge__needle" />
        <span className="theme-gauge__hub" />
      </div>
    </div>
  )
}
