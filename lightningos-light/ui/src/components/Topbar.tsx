import { useEffect, useState } from 'react'
import { getHealth } from '../api'

const statusColors: Record<string, string> = {
  OK: 'bg-glow/20 text-glow border-glow/40',
  WARN: 'bg-brass/20 text-brass border-brass/40',
  ERR: 'bg-ember/20 text-ember border-ember/40'
}

export default function Topbar() {
  const [status, setStatus] = useState('...')
  const [issues, setIssues] = useState<Array<{ component?: string; level?: string; message?: string }>>([])

  useEffect(() => {
    let mounted = true
    const load = async () => {
      try {
        const data = await getHealth()
        if (!mounted) return
        setStatus(data.status)
        setIssues(Array.isArray(data.issues) ? data.issues : [])
      } catch {
        if (!mounted) return
        setStatus('ERR')
        setIssues([{ component: 'system', level: 'ERR', message: 'Health check failed' }])
      }
    }

    load()
    const timer = setInterval(load, 30000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  return (
    <header className="px-6 lg:px-12 pt-8">
      <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4">
        <div>
          <p className="text-sm uppercase tracking-[0.3em] text-fog/50">Status overview</p>
          <h1 className="text-3xl lg:text-4xl font-semibold">LightningOS Control Center</h1>
        </div>
        <div className="flex items-center gap-4">
          <div className={`px-4 py-2 rounded-full border text-sm ${statusColors[status] || 'bg-white/10 border-white/20'}`}>
            {status}
          </div>
        <div className="text-xs text-fog/60 max-w-xs">
          {issues.length
            ? issues
              .map((issue) => {
                const label = issue.component ? issue.component.toUpperCase() : 'SYSTEM'
                const message = issue.message || 'Issue detected'
                return `${label}: ${message}`
              })
              .join(' • ')
            : status === '...'
              ? 'Checking system status...'
              : status === 'OK'
                ? 'All systems green'
                : 'Status unavailable'}
        </div>
        </div>
      </div>
      <div className="glow-divider mt-6" />
    </header>
  )
}
