import type { ReactNode } from 'react'

type MetricDisclosureProps = {
  title: string
  summary: string
  open: boolean
  onToggle: () => void
  children: ReactNode
}

export function MetricDisclosure({
  title,
  summary,
  open,
  onToggle,
  children
}: MetricDisclosureProps) {
  return (
    <div className="mt-2 border-t border-white/10 pt-2">
      <button
        type="button"
        className="flex w-full items-center justify-between gap-3 rounded-md py-1 text-left transition hover:text-cyan-100"
        aria-expanded={open}
        onClick={onToggle}
      >
        <span className="text-[10px] uppercase tracking-wide text-fog/60">{title}</span>
        <span className="flex min-w-0 items-center gap-2 text-right text-[11px] text-fog/45">
          <span className="truncate normal-case tracking-normal">{summary}</span>
          <span className="grid h-5 w-5 shrink-0 place-items-center rounded border border-white/10 text-fog/60">
            {open ? '-' : '+'}
          </span>
        </span>
      </button>
      {open && <div className="mt-2 space-y-1">{children}</div>}
    </div>
  )
}
