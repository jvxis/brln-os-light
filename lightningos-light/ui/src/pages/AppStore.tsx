import { useEffect, useState } from 'react'
import { getAppAdminPassword, getApps, installApp, resetAppAdmin, startApp, stopApp, uninstallApp } from '../api'
import lndgIcon from '../assets/apps/lndg.ico'
import bitcoincoreIcon from '../assets/apps/bitcoincore.svg'

type AppInfo = {
  id: string
  name: string
  description: string
  installed: boolean
  status: string
  port?: number
  admin_password_path?: string
}

const iconMap: Record<string, string> = {
  lndg: lndgIcon,
  bitcoincore: bitcoincoreIcon
}

const internalRoutes: Record<string, string> = {
  bitcoincore: 'bitcoin-local'
}

const statusStyles: Record<string, string> = {
  running: 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30',
  stopped: 'bg-amber-500/15 text-amber-200 border border-amber-400/30',
  unknown: 'bg-rose-500/15 text-rose-200 border border-rose-400/30',
  not_installed: 'bg-white/10 text-fog/60 border border-white/10'
}

export default function AppStore() {
  const [apps, setApps] = useState<AppInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState<Record<string, string>>({})
  const [copying, setCopying] = useState<Record<string, boolean>>({})

  const loadApps = () => {
    setLoading(true)
    getApps().then((data: AppInfo[]) => {
      setApps(data || [])
      setLoading(false)
    }).catch((err: unknown) => {
      setMessage(err instanceof Error ? err.message : 'Failed to load apps.')
      setLoading(false)
    })
  }

  useEffect(() => {
    loadApps()
  }, [])

  const handleAction = async (id: string, action: 'install' | 'start' | 'stop' | 'uninstall') => {
    setMessage('')
    setBusy((prev) => ({ ...prev, [id]: action }))
    try {
      if (action === 'install') await installApp(id)
      if (action === 'start') await startApp(id)
      if (action === 'stop') await stopApp(id)
      if (action === 'uninstall') await uninstallApp(id)
      loadApps()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Action failed.')
    } finally {
      setBusy((prev) => {
        const next = { ...prev }
        delete next[id]
        return next
      })
    }
  }

  const handleResetAdmin = async (id: string) => {
    setMessage('')
    setBusy((prev) => ({ ...prev, [id]: 'reset-admin' }))
    try {
      await resetAppAdmin(id)
      setMessage('LNDg admin password reset to the stored value.')
      loadApps()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Reset failed.')
    } finally {
      setBusy((prev) => {
        const next = { ...prev }
        delete next[id]
        return next
      })
    }
  }

  const handleCopyAdminPassword = async (id: string) => {
    setMessage('')
    setCopying((prev) => ({ ...prev, [id]: true }))
    try {
      const res = await getAppAdminPassword(id)
      const password = res?.password || ''
      if (!password) {
        setMessage('Admin password unavailable.')
        return
      }
      await navigator.clipboard.writeText(password)
      setMessage('LNDg admin password copied.')
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Copy failed.')
    } finally {
      setCopying((prev) => {
        const next = { ...prev }
        delete next[id]
        return next
      })
    }
  }

  const host = window.location.hostname

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">App Store</h2>
        <p className="text-fog/60">Install optional services on demand. Docker is installed automatically when required.</p>
        {message && <p className="text-sm text-brass mt-4">{message}</p>}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        {apps.map((app) => {
          const isBusy = Boolean(busy[app.id])
          const canResetAdmin = app.id === 'lndg' && app.status === 'running'
          const resetTitle = canResetAdmin ? 'Reset to stored admin password' : 'Start LNDg to reset password'
          const statusStyle = statusStyles[app.status] || statusStyles.unknown
          const internalRoute = internalRoutes[app.id]
          const openUrl = app.port ? `http://${host}:${app.port}` : ''
          const icon = iconMap[app.id]
          return (
            <div key={app.id} className="section-card space-y-4">
              <div className="flex items-start justify-between gap-4">
                <div className="flex items-start gap-4">
                  <div className="h-12 w-12 rounded-2xl bg-transparent flex items-center justify-center overflow-hidden">
                    {icon ? (
                      <img src={icon} alt={`${app.name} icon`} className="h-12 w-12 rounded-2xl object-cover" />
                    ) : (
                      <span className="text-xs text-fog/50">APP</span>
                    )}
                  </div>
                  <div>
                    <h3 className="text-lg font-semibold">{app.name}</h3>
                    <p className="text-sm text-fog/60">{app.description}</p>
                  </div>
                </div>
                <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${statusStyle}`}>
                  {app.status.replace('_', ' ')}
                </span>
              </div>

              <div className="text-xs text-fog/50 space-y-1">
                {app.port ? (
                  <p>Default port: {app.port}</p>
                ) : internalRoute ? (
                  <p>Default access: Bitcoin Local</p>
                ) : null}
                {app.admin_password_path && (
                  <div className="flex flex-wrap items-center gap-2">
                    <span>Admin password saved at {app.admin_password_path}</span>
                    {app.id === 'lndg' && (
                      <button
                        className="text-fog/50 hover:text-fog"
                        onClick={() => handleCopyAdminPassword(app.id)}
                        title="Copy LNDg admin password"
                        aria-label="Copy LNDg admin password"
                        disabled={Boolean(copying[app.id])}
                      >
                        <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6">
                          <rect x="9" y="9" width="11" height="11" rx="2" />
                          <rect x="4" y="4" width="11" height="11" rx="2" />
                        </svg>
                      </button>
                    )}
                  </div>
                )}
              </div>

              <div className="flex flex-wrap items-center gap-3">
                {!app.installed && (
                  <button className="btn-primary" disabled={isBusy} onClick={() => handleAction(app.id, 'install')}>
                    {isBusy ? 'Installing...' : 'Install'}
                  </button>
                )}
                {app.installed && app.status === 'running' && (
                  <>
                    {internalRoute && (
                      <a className="btn-primary" href={`#${internalRoute}`}>
                        Open
                      </a>
                    )}
                    {!internalRoute && app.port && openUrl && (
                      <a className="btn-primary" href={openUrl} target="_blank" rel="noreferrer">
                        Open
                      </a>
                    )}
                    {app.id === 'lndg' && (
                      <button
                        className="btn-secondary"
                        disabled={isBusy || !canResetAdmin}
                        title={resetTitle}
                        onClick={() => handleResetAdmin(app.id)}
                      >
                        {isBusy ? 'Resetting...' : 'Reset admin password'}
                      </button>
                    )}
                    <button className="btn-secondary" disabled={isBusy} onClick={() => handleAction(app.id, 'stop')}>
                      {isBusy ? 'Stopping...' : 'Stop'}
                    </button>
                    <button className="btn-secondary" disabled={isBusy} onClick={() => handleAction(app.id, 'uninstall')}>
                      Uninstall
                    </button>
                  </>
                )}
                {app.installed && app.status !== 'running' && (
                  <>
                    <button className="btn-primary" disabled={isBusy} onClick={() => handleAction(app.id, 'start')}>
                      {isBusy ? 'Starting...' : 'Start'}
                    </button>
                    {app.id === 'lndg' && (
                      <button
                        className="btn-secondary"
                        disabled={isBusy || !canResetAdmin}
                        title={resetTitle}
                        onClick={() => handleResetAdmin(app.id)}
                      >
                        {isBusy ? 'Resetting...' : 'Reset admin password'}
                      </button>
                    )}
                    <button className="btn-secondary" disabled={isBusy} onClick={() => handleAction(app.id, 'uninstall')}>
                      Uninstall
                    </button>
                  </>
                )}
              </div>
            </div>
          )
        })}
      </div>

      {loading && <p className="text-fog/60">Loading apps...</p>}
      {!loading && apps.length === 0 && (
        <p className="text-fog/60">No apps available yet.</p>
      )}
    </section>
  )
}
