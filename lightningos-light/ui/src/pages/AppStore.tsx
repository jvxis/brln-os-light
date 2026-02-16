import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getAppAdminPassword, getApps, getBitcoinLocalStatus, installApp, resetAppAdmin, startApp, stopApp, uninstallApp } from '../api'
import lndgIcon from '../assets/apps/lndg.ico'
import bitcoincoreIcon from '../assets/apps/bitcoincore.png'
import elementsIcon from '../assets/apps/elements.png'
import peerswapIcon from '../assets/apps/peerswap.png'
import robosatsIcon from '../assets/apps/robosats.svg'

type AppInfo = {
  id: string
  name: string
  description: string
  installed: boolean
  status: string
  port?: number
  admin_password_path?: string
}

type BitcoinLocalStatus = {
  source?: 'app' | 'external' | 'none'
}

const iconMap: Record<string, string> = {
  lndg: lndgIcon,
  bitcoincore: bitcoincoreIcon,
  elements: elementsIcon,
  peerswap: peerswapIcon,
  robosats: robosatsIcon
}

const internalRoutes: Record<string, string> = {
  bitcoincore: 'bitcoin-local',
  elements: 'elements'
}

const statusStyles: Record<string, string> = {
  running: 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30',
  stopped: 'bg-amber-500/15 text-amber-200 border border-amber-400/30',
  unknown: 'bg-rose-500/15 text-rose-200 border border-rose-400/30',
  not_installed: 'bg-white/10 text-fog/60 border border-white/10'
}

export default function AppStore() {
  const { t } = useTranslation()
  const [apps, setApps] = useState<AppInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState<Record<string, string>>({})
  const [copying, setCopying] = useState<Record<string, boolean>>({})
  const [hideBitcoinCore, setHideBitcoinCore] = useState(false)

  const resolveStatusLabel = (value: string) => {
    switch (value) {
      case 'running':
        return t('common.running')
      case 'stopped':
        return t('common.stopped')
      case 'not_installed':
        return t('common.notInstalled')
      case 'unknown':
        return t('common.unknown')
      default:
        return value ? value.replace('_', ' ') : t('common.unknown')
    }
  }

  const loadApps = () => {
    setLoading(true)
    getApps().then((data: AppInfo[]) => {
      setApps(data || [])
      setLoading(false)
    }).catch((err: unknown) => {
      setMessage(err instanceof Error ? err.message : t('appStore.loadFailed'))
      setLoading(false)
    })
  }

  useEffect(() => {
    loadApps()
    getBitcoinLocalStatus()
      .then((data: BitcoinLocalStatus) => {
        setHideBitcoinCore(data?.source === 'external')
      })
      .catch(() => {
        setHideBitcoinCore(false)
      })
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
      setMessage(err instanceof Error ? err.message : t('appStore.actionFailed'))
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
      setMessage(t('appStore.resetStoredPasswordMessage'))
      loadApps()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : t('appStore.resetFailed'))
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
        setMessage(t('appStore.adminPasswordUnavailable'))
        return
      }
      await navigator.clipboard.writeText(password)
      setMessage(t('appStore.adminPasswordCopied'))
    } catch (err) {
      setMessage(err instanceof Error ? err.message : t('common.copyFailed'))
    } finally {
      setCopying((prev) => {
        const next = { ...prev }
        delete next[id]
        return next
      })
    }
  }

  const host = window.location.hostname
  const visibleApps = hideBitcoinCore
    ? apps.filter((app) => app.id !== 'bitcoincore')
    : apps

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('appStore.title')}</h2>
        <p className="text-fog/60">{t('appStore.subtitle')}</p>
        {message && <p className="text-sm text-brass mt-4">{message}</p>}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        {visibleApps.map((app) => {
          const busyAction = busy[app.id]
          const isBusy = Boolean(busyAction)
          const isResetting = busyAction === 'reset-admin'
          const canResetAdmin = app.id === 'lndg' && app.status === 'running'
          const resetTitle = canResetAdmin ? t('appStore.resetStoredPassword') : t('appStore.startLndgToReset')
          const statusStyle = statusStyles[app.status] || statusStyles.unknown
          const internalRoute = internalRoutes[app.id]
          const internalRouteLabel = app.id === 'bitcoincore'
            ? t('nav.bitcoinLocal')
            : app.id === 'elements'
              ? t('nav.elements')
              : t('appStore.internal')
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
                      <span className="text-xs text-fog/50">{t('appStore.appBadge')}</span>
                    )}
                  </div>
                  <div>
                    <h3 className="text-lg font-semibold">{app.name}</h3>
                    <p className="text-sm text-fog/60">{app.description}</p>
                  </div>
                </div>
                <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${statusStyle}`}>
                  {resolveStatusLabel(app.status)}
                </span>
              </div>

              <div className="text-xs text-fog/50 space-y-1">
                {app.port ? (
                  <p>{t('appStore.defaultPort', { port: app.port })}</p>
                ) : internalRoute ? (
                  <p>{t('appStore.defaultAccess', { access: internalRouteLabel })}</p>
                ) : null}
                {app.admin_password_path && (
                  <div className="flex flex-wrap items-center gap-2">
                    <span>{t('appStore.adminPasswordSavedAt', { path: app.admin_password_path })}</span>
                    {app.id === 'lndg' && (
                      <button
                        className="text-fog/50 hover:text-fog"
                        onClick={() => handleCopyAdminPassword(app.id)}
                        title={t('appStore.copyLndgPassword')}
                        aria-label={t('appStore.copyLndgPassword')}
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
                    {isBusy ? t('appStore.installing') : t('appStore.install')}
                  </button>
                )}
                {app.installed && app.status === 'running' && (
                  <>
                    {internalRoute && (
                      <a className="btn-primary" href={`#${internalRoute}`}>
                        {t('common.open')}
                      </a>
                    )}
                    {!internalRoute && app.port && openUrl && (
                      <a className="btn-primary" href={openUrl} target="_blank" rel="noreferrer">
                        {t('common.open')}
                      </a>
                    )}
                    {app.id === 'lndg' && (
                      <button
                        className="btn-secondary"
                        disabled={isBusy || !canResetAdmin}
                        title={resetTitle}
                        onClick={() => handleResetAdmin(app.id)}
                      >
                        {isResetting ? t('appStore.resetting') : t('appStore.resetAdminPassword')}
                      </button>
                    )}
                    <button className="btn-secondary" disabled={isBusy} onClick={() => handleAction(app.id, 'stop')}>
                      {isBusy ? t('appStore.stopping') : t('common.stop')}
                    </button>
                    <button className="btn-secondary" disabled={isBusy} onClick={() => handleAction(app.id, 'uninstall')}>
                      {t('appStore.uninstall')}
                    </button>
                  </>
                )}
                {app.installed && app.status !== 'running' && (
                  <>
                    <button className="btn-primary" disabled={isBusy} onClick={() => handleAction(app.id, 'start')}>
                      {isBusy ? t('appStore.starting') : t('common.start')}
                    </button>
                    {app.id === 'lndg' && (
                      <button
                        className="btn-secondary"
                        disabled={isBusy || !canResetAdmin}
                        title={resetTitle}
                        onClick={() => handleResetAdmin(app.id)}
                      >
                        {isResetting ? t('appStore.resetting') : t('appStore.resetAdminPassword')}
                      </button>
                    )}
                    <button className="btn-secondary" disabled={isBusy} onClick={() => handleAction(app.id, 'uninstall')}>
                      {t('appStore.uninstall')}
                    </button>
                  </>
                )}
              </div>
            </div>
          )
        })}
      </div>

      {loading && <p className="text-fog/60">{t('appStore.loadingApps')}</p>}
      {!loading && apps.length === 0 && (
        <p className="text-fog/60">{t('appStore.noApps')}</p>
      )}
    </section>
  )
}
