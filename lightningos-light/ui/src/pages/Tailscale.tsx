import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getApps, getTailscaleAuthURL, getTailscaleStatus, tailscaleLogout } from '../api'

type TailscaleNode = {
  ID: string
  HostName: string
  DNSName: string
  OS: string
  TailscaleIPs: string[]
  Online: boolean
  Active: boolean
  ExitNode?: boolean
}

type TailscaleStatusData = {
  Version?: string
  BackendState?: string
  AuthURL?: string
  TailscaleIPs?: string[]
  Self?: TailscaleNode
  Peer?: Record<string, TailscaleNode>
}

type AppInfo = {
  id: string
  installed: boolean
  status: string
}

const statusStyles: Record<string, string> = {
  running: 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30',
  stopped: 'bg-amber-500/15 text-amber-200 border border-amber-400/30',
  unknown: 'bg-rose-500/15 text-rose-200 border border-rose-400/30',
  not_installed: 'bg-white/10 text-fog/60 border border-white/10'
}

export default function Tailscale() {
  const { t } = useTranslation()
  const [tsStatus, setTsStatus] = useState<TailscaleStatusData | null>(null)
  const [authURL, setAuthURL] = useState('')
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null)
  const [message, setMessage] = useState('')
  const [loggingOut, setLoggingOut] = useState(false)

  const loadInfo = () => {
    getApps()
      .then((data: AppInfo[]) => {
        const app = (data || []).find((a) => a.id === 'tailscale')
        setAppInfo(app || null)
        if (app?.installed) {
          getTailscaleStatus()
            .then((s: TailscaleStatusData) => {
              setTsStatus(s)
              if (s?.BackendState === 'NeedsLogin' || s?.AuthURL) {
                getTailscaleAuthURL()
                  .then((r: { url: string }) => setAuthURL(r?.url || ''))
                  .catch(() => setAuthURL(''))
              } else {
                setAuthURL('')
              }
            })
            .catch(() => setTsStatus(null))
        }
      })
      .catch((err: unknown) => {
        setMessage(err instanceof Error ? err.message : t('tailscale.loadFailed'))
      })
  }

  useEffect(() => {
    loadInfo()
    const timer = setInterval(loadInfo, 15000)
    return () => clearInterval(timer)
  }, [])

  const handleLogout = async () => {
    setLoggingOut(true)
    setMessage('')
    try {
      await tailscaleLogout()
      setMessage(t('tailscale.loggedOut'))
      loadInfo()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : t('tailscale.logoutFailed'))
    } finally {
      setLoggingOut(false)
    }
  }

  const installed = Boolean(appInfo?.installed)
  const serviceStatus = appInfo?.status || 'not_installed'
  const statusStyle = statusStyles[serviceStatus] || statusStyles.unknown
  const statusLabel = () => {
    switch (serviceStatus) {
      case 'running': return t('common.running')
      case 'stopped': return t('common.stopped')
      case 'not_installed': return t('common.notInstalled')
      default: return t('common.unknown')
    }
  }

  const backendState = tsStatus?.BackendState || ''
  const needsLogin = backendState === 'NeedsLogin' || Boolean(authURL)
  const isConnected = backendState === 'Running'
  const selfIPs = tsStatus?.TailscaleIPs || tsStatus?.Self?.TailscaleIPs || []
  const peers = tsStatus?.Peer ? Object.values(tsStatus.Peer) : []
  const onlinePeers = peers.filter((p) => p.Online)

  return (
    <section className="space-y-6">
      <div className="section-card space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">{t('tailscale.title')}</h2>
            <p className="text-fog/60">{t('tailscale.subtitle')}</p>
          </div>
          <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${statusStyle}`}>
            {statusLabel()}
          </span>
        </div>
        {message && <p className="text-sm text-brass">{message}</p>}
      </div>

      {!installed && (
        <div className="section-card space-y-3">
          <h3 className="text-lg font-semibold">{t('tailscale.notInstalledTitle')}</h3>
          <p className="text-fog/60">{t('tailscale.notInstalledBody')}</p>
          <a className="btn-primary inline-flex items-center" href="#apps">{t('tailscale.openAppStore')}</a>
        </div>
      )}

      {installed && needsLogin && (
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('tailscale.loginRequired')}</h3>
          <p className="text-fog/60">{t('tailscale.loginBody')}</p>
          {authURL ? (
            <a
              className="btn-primary inline-flex items-center gap-2"
              href={authURL}
              target="_blank"
              rel="noreferrer"
            >
              {t('tailscale.openAuthURL')}
            </a>
          ) : (
            <p className="text-sm text-fog/50">{t('tailscale.fetchingAuthURL')}</p>
          )}
        </div>
      )}

      {installed && !needsLogin && (
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="section-card space-y-4">
            <h3 className="text-lg font-semibold">{t('tailscale.thisNode')}</h3>
            <div className="grid gap-3 text-sm text-fog/70">
              <div className="flex items-center justify-between">
                <span>{t('tailscale.backendState')}</span>
                <span className={isConnected ? 'text-emerald-200' : 'text-fog'}>
                  {backendState || t('common.unknown')}
                </span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('tailscale.hostname')}</span>
                <span className="text-fog">{tsStatus?.Self?.HostName || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('tailscale.tailscaleIP')}</span>
                <span className="text-fog font-mono">{selfIPs[0] || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('tailscale.os')}</span>
                <span className="text-fog">{tsStatus?.Self?.OS || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('tailscale.version')}</span>
                <span className="text-fog">{tsStatus?.Version || '-'}</span>
              </div>
            </div>
            <div className="glow-divider" />
            <button
              className="btn-secondary"
              onClick={handleLogout}
              disabled={loggingOut}
            >
              {loggingOut ? t('tailscale.loggingOut') : t('tailscale.logout')}
            </button>
          </div>

          <div className="section-card space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-semibold">{t('tailscale.peers')}</h3>
              <span className="text-xs text-fog/60">
                {t('tailscale.onlinePeers', { count: onlinePeers.length, total: peers.length })}
              </span>
            </div>
            {peers.length === 0 && (
              <p className="text-sm text-fog/50">{t('tailscale.noPeers')}</p>
            )}
            <div className="space-y-3">
              {peers.map((peer) => (
                <div key={peer.ID} className="flex items-center justify-between text-sm">
                  <div>
                    <p className="text-fog">{peer.HostName}</p>
                    <p className="text-xs text-fog/50 font-mono">{peer.TailscaleIPs?.[0] || '-'}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="text-xs text-fog/50">{peer.OS}</span>
                    <span className={`h-2 w-2 rounded-full ${peer.Online ? 'bg-emerald-400' : 'bg-white/20'}`} />
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
