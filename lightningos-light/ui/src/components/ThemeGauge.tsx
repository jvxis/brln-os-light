import type { ReactNode } from 'react'

export type ThemeGaugeTone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

const fillClasses: Record<ThemeGaugeTone, string> = {
  ok: 'from-emerald-400 to-emerald-300',
  warn: 'from-amber-400 to-yellow-300',
  danger: 'from-rose-400 to-rose-300',
  muted: 'from-slate-400 to-slate-300',
  info: 'from-sky-400 to-cyan-300',
}

type ThemeGaugeProps = {
  value: number
  marker?: number | null
  markerLabel?: string
  tone?: ThemeGaugeTone
  className?: string
  readout?: ReactNode
}

const clampPct = (value: number) => Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0))

const polar = (cx: number, cy: number, radius: number, degrees: number) => {
  const radians = ((degrees - 90) * Math.PI) / 180
  return {
    x: cx + (radius * Math.cos(radians)),
    y: cy + (radius * Math.sin(radians)),
  }
}

const arcPath = (cx: number, cy: number, radius: number, from: number, to: number) => {
  const start = polar(cx, cy, radius, from)
  const end = polar(cx, cy, radius, to)
  const largeArc = Math.abs(to - from) > 180 ? 1 : 0
  return `M ${start.x.toFixed(2)} ${start.y.toFixed(2)} A ${radius} ${radius} 0 ${largeArc} 1 ${end.x.toFixed(2)} ${end.y.toFixed(2)}`
}

export default function ThemeGauge({
  value,
  marker,
  markerLabel,
  tone = 'info',
  className = '',
  readout,
}: ThemeGaugeProps) {
  const pct = clampPct(value)
  const markerPct = typeof marker === 'number' ? clampPct(marker) : null
  const startAngle = -135
  const endAngle = 135
  const span = endAngle - startAngle
  const valueAngle = startAngle + ((pct / 100) * span)
  const markerAngle = markerPct === null ? null : startAngle + ((markerPct / 100) * span)
  const needle = polar(100, 100, 61, valueAngle)
  const needleTail = polar(100, 100, -12, valueAngle)
  const markerOuter = markerAngle === null ? null : polar(100, 100, 82, markerAngle)
  const markerInner = markerAngle === null ? null : polar(100, 100, 55, markerAngle)

  return (
    <div className={`theme-gauge theme-gauge--${tone} ${className}`.trim()}>
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
        <svg className="theme-gauge__dial-face" viewBox="0 0 200 200" focusable="false">
          <path className="theme-gauge__dial-scale" d={arcPath(100, 100, 78, startAngle, endAngle)} />
          <path className="theme-gauge__dial-value" d={arcPath(100, 100, 78, startAngle, valueAngle)} />
          {Array.from({ length: 11 }, (_, index) => {
            const angle = startAngle + ((index / 10) * span)
            const outer = polar(100, 100, 75, angle)
            const inner = polar(100, 100, index % 2 === 0 ? 60 : 65, angle)
            return (
              <line
                className={index % 2 === 0 ? 'theme-gauge__tick theme-gauge__tick--major' : 'theme-gauge__tick'}
                key={index}
                x1={outer.x}
                y1={outer.y}
                x2={inner.x}
                y2={inner.y}
              />
            )
          })}
          {markerOuter && markerInner ? (
            <line className="theme-gauge__dial-marker" x1={markerOuter.x} y1={markerOuter.y} x2={markerInner.x} y2={markerInner.y}>
              {markerLabel ? <title>{markerLabel}</title> : null}
            </line>
          ) : null}
          <line
            className="theme-gauge__needle"
            x1={needleTail.x}
            y1={needleTail.y}
            x2={needle.x}
            y2={needle.y}
          />
          <circle className="theme-gauge__hub" cx="100" cy="100" r="5" />
          <text className="theme-gauge__scale-label" x="34" y="151">0</text>
          <text className="theme-gauge__scale-label" x="166" y="151" textAnchor="end">100</text>
        </svg>
        <div className="theme-gauge__readout">{readout ?? `${Math.round(pct)}%`}</div>
      </div>
    </div>
  )
}
