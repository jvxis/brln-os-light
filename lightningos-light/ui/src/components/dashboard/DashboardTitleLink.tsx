import type { ReactNode } from 'react'

type DashboardTitleLinkProps = {
  href: string
  children: ReactNode
  className?: string
  title?: string
}

export default function DashboardTitleLink({
  href,
  children,
  className = '',
  title,
}: DashboardTitleLinkProps) {
  const classes = [
    'inline-flex max-w-full items-center rounded-sm text-current underline decoration-sky-300/0 underline-offset-4 transition hover:text-sky-100 hover:decoration-sky-300/70 focus:outline-none focus:ring-2 focus:ring-sky-300/50',
    className,
  ].filter(Boolean).join(' ')

  return (
    <a className={classes} href={href} title={title}>
      {children}
    </a>
  )
}
