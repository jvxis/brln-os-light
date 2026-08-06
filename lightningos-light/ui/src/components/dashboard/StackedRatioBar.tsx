import type { Tone } from './types'

type Segment = {
  label: string
  value: number
  tone?: Tone
}

type StackedRatioBarProps = {
  segments: Segment[]
  compact?: boolean
  instrumentLegend?: boolean
}

const segmentClasses: Record<Tone, string> = {
  ok: 'bg-emerald-400',
  warn: 'bg-amber-400',
  danger: 'bg-rose-400',
  muted: 'bg-slate-400',
  info: 'bg-sky-400',
}

export default function StackedRatioBar({ segments, compact = false, instrumentLegend = false }: StackedRatioBarProps) {
  const safeSegments = segments.filter((segment) => segment.value > 0)
  const total = safeSegments.reduce((sum, segment) => sum + segment.value, 0)

  return (
    <div className="stacked-ratio space-y-2">
      <div className="stacked-ratio__track flex h-2 overflow-hidden rounded-full bg-white/8">
        {safeSegments.length > 0 ? safeSegments.map((segment) => (
          <div
            key={segment.label}
            className={`stacked-ratio__segment ${segmentClasses[segment.tone ?? 'muted']} h-full`}
            style={{ width: `${total > 0 ? (segment.value / total) * 100 : 0}%` }}
            title={`${segment.label}: ${segment.value}`}
          />
        )) : (
          <div className="h-full w-full bg-white/8" />
        )}
      </div>
      {instrumentLegend && (
        <div className="stacked-ratio__instrument-legend" aria-label={segments.map((segment) => `${segment.label}: ${segment.value}`).join(', ')}>
          {segments.map((segment) => (
            <span key={segment.label} className="stacked-ratio__instrument-item">
              <span className={`h-1.5 w-1.5 rounded-full ${segmentClasses[segment.tone ?? 'muted']}`} />
              <span>{segment.label}: {segment.value}</span>
            </span>
          ))}
        </div>
      )}
      {!compact && (
        <div className="flex flex-wrap gap-2 text-xs text-fog/55">
          {segments.map((segment) => (
            <span key={segment.label} className="inline-flex items-center gap-2 rounded-full border border-white/10 bg-white/5 px-2 py-1">
              <span className={`h-2 w-2 rounded-full ${segmentClasses[segment.tone ?? 'muted']}`} />
              <span>{segment.label}</span>
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
