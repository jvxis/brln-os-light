import type { ReactNode } from 'react'

type SettingsSubcardProps = {
  title: string
  subtitle?: string
  className?: string
  children: ReactNode
}

export function SettingsSubcard({
  title,
  subtitle,
  className = '',
  children
}: SettingsSubcardProps) {
  return (
    <div className={`rounded-lg border border-white/10 bg-white/5 p-3 space-y-3 ${className}`}>
      <div>
        <p className="text-xs uppercase tracking-wide text-fog/60">{title}</p>
        {subtitle && <p className="text-xs text-fog/50">{subtitle}</p>}
      </div>
      {children}
    </div>
  )
}
