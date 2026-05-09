import type { ReactNode } from 'react'
import type { Tone } from './types'

type StatusBadgeProps = {
  label: ReactNode
  tone?: Tone
  size?: 'sm' | 'md'
}

const toneClasses: Record<Tone, string> = {
  ok: 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200',
  warn: 'border-amber-400/30 bg-amber-500/15 text-amber-200',
  danger: 'border-rose-400/30 bg-rose-500/15 text-rose-200',
  muted: 'border-white/10 bg-white/10 text-fog/60',
  info: 'border-sky-400/30 bg-sky-500/15 text-sky-200',
}

export default function StatusBadge({ label, tone = 'muted', size = 'sm' }: StatusBadgeProps) {
  const sizeClass = size === 'md' ? 'px-3 py-1 text-xs' : 'px-2 py-0.5 text-[11px]'
  return (
    <span className={`inline-flex max-w-full items-center justify-center rounded-full border text-center uppercase leading-tight tracking-wide [overflow-wrap:anywhere] ${sizeClass} ${toneClasses[tone]}`}>
      {label}
    </span>
  )
}
