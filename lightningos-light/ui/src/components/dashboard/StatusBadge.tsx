import type { ReactNode } from 'react'
import type { Tone } from './types'

type StatusBadgeProps = {
  label: ReactNode
  tone?: Tone
  size?: 'sm' | 'md'
}

const toneClasses: Record<Tone, string> = {
  ok: 'status-badge--ok',
  warn: 'status-badge--warn',
  danger: 'status-badge--danger',
  muted: 'status-badge--muted',
  info: 'status-badge--info',
}

export default function StatusBadge({ label, tone = 'muted', size = 'sm' }: StatusBadgeProps) {
  const sizeClass = size === 'md' ? 'px-3 py-1 text-xs' : 'px-2 py-0.5 text-[11px]'
  return (
    <span className={`status-badge inline-flex max-w-full items-center justify-center rounded-full border text-center uppercase leading-tight tracking-wide [overflow-wrap:anywhere] ${sizeClass} ${toneClasses[tone]}`}>
      {tone !== 'muted' ? <span className="status-badge__indicator" aria-hidden="true" /> : null}
      {label}
    </span>
  )
}
