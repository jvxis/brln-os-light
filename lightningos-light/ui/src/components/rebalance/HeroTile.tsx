import { useState, type ReactNode } from 'react'

export type HeroTone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

const accentClasses: Record<HeroTone, string> = {
  ok: 'from-emerald-500/14',
  warn: 'from-amber-500/14',
  danger: 'from-rose-500/14',
  muted: 'from-white/8',
  info: 'from-sky-500/14',
}

const dotClasses: Record<HeroTone, string> = {
  ok: 'bg-emerald-400',
  warn: 'bg-amber-400',
  danger: 'bg-rose-400',
  muted: 'bg-fog/40',
  info: 'bg-sky-400',
}

const gaugeFill: Record<HeroTone, string> = {
  ok: 'from-emerald-400 to-emerald-300',
  warn: 'from-amber-400 to-yellow-300',
  danger: 'from-rose-400 to-rose-300',
  muted: 'from-slate-400 to-slate-300',
  info: 'from-sky-400 to-cyan-300',
}

type HeroTileProps = {
  label: string
  value: ReactNode
  tone?: HeroTone
  badge?: string
  gaugePct?: number
  gaugeMarker?: number
  context?: ReactNode
  details?: ReactNode
  defaultOpen?: boolean
}

export default function HeroTile({
  label,
  value,
  tone = 'muted',
  badge,
  gaugePct,
  gaugeMarker,
  context,
  details,
  defaultOpen = false,
}: HeroTileProps) {
  const [open, setOpen] = useState(defaultOpen)
  const showGauge = typeof gaugePct === 'number'
  const clampedPct = Math.max(0, Math.min(100, gaugePct ?? 0))
  const clampedMarker = typeof gaugeMarker === 'number' ? Math.max(0, Math.min(100, gaugeMarker)) : null
  const hasDetails = Boolean(details)

  return (
    <article className={`flex flex-col rounded-3xl border border-white/10 bg-gradient-to-br ${accentClasses[tone]} to-transparent p-4 shadow-panel`}>
      <div className="flex items-start justify-between gap-3">
        <p className="text-[11px] uppercase tracking-[0.24em] text-fog/45">{label}</p>
        {badge ? (
          <span className="inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-white/5 px-2 py-0.5 text-[10px] uppercase tracking-wide text-fog/80">
            <span className={`h-1.5 w-1.5 rounded-full ${dotClasses[tone]}`} />
            {badge}
          </span>
        ) : null}
      </div>
      <div className="mt-3 text-2xl font-semibold leading-none text-fog">{value}</div>
      {showGauge && (
        <div className="mt-3 space-y-1">
          <div className="relative h-2 overflow-hidden rounded-full bg-white/8">
            <div
              className={`h-full rounded-full bg-gradient-to-r ${gaugeFill[tone]}`}
              style={{ width: `${clampedPct}%` }}
            />
            {clampedMarker !== null && (
              <div
                className="absolute top-[-2px] h-3 w-[2px] rounded bg-fog/60"
                style={{ left: `${clampedMarker}%` }}
                title={`alvo ${clampedMarker.toFixed(0)}%`}
              />
            )}
          </div>
        </div>
      )}
      {context ? <div className="mt-3 space-y-1 text-xs text-fog/65">{context}</div> : null}
      {hasDetails && (
        <button
          type="button"
          onClick={() => setOpen((prev) => !prev)}
          className="mt-auto pt-3 self-start text-[11px] uppercase tracking-wide text-fog/55 underline-offset-2 hover:underline"
          aria-expanded={open}
        >
          {open ? '▴ Ocultar detalhes' : '▾ Detalhes'}
        </button>
      )}
      {hasDetails && open && (
        <div className="mt-3 space-y-1 border-t border-white/10 pt-3 text-xs text-fog/70">{details}</div>
      )}
    </article>
  )
}
