import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { enableLoginAuth, getAppUpgradeStatus, getBitcoinActive, getDisk, getLndStatus, getLogs, getPostgres, getSystem, restartService, runSystemAction, startAppUpgrade, type AuthState } from '../api'
import { getLocale } from '../i18n'

type AppUpgradeStatus = {
  current_version?: string
  latest_version?: string
  latest_tag?: string
  latest_channel?: string
  checked_at?: string
  update_available?: boolean
  running?: boolean
  error?: string
}

type DashboardProps = {
  authState?: AuthState | null
}

export default function Dashboard({ authState }: DashboardProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const gbFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 })
  const dbGBFormatter = new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  const percentFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 })
  const tempFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 })
  const satFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 })
  const [system, setSystem] = useState<any>(null)
  const [disk, setDisk] = useState<any[]>([])
  const [bitcoin, setBitcoin] = useState<any>(null)
  const [postgres, setPostgres] = useState<any>(null)
  const [lnd, setLnd] = useState<any>(null)
  const [status, setStatus] = useState<'loading' | 'ok' | 'unavailable'>('loading')
  const [systemAction, setSystemAction] = useState<'restart' | 'shutdown' | null>(null)
  const [systemActionBusy, setSystemActionBusy] = useState(false)
  const [systemActionError, setSystemActionError] = useState<string | null>(null)
  const [appUpgrade, setAppUpgrade] = useState<AppUpgradeStatus | null>(null)
  const [appUpgradeChecking, setAppUpgradeChecking] = useState(false)
  const [appUpgradeMessage, setAppUpgradeMessage] = useState('')
  const [appUpgradeModalOpen, setAppUpgradeModalOpen] = useState(false)
  const [appUpgradeBusy, setAppUpgradeBusy] = useState(false)
  const [appUpgradeLogs, setAppUpgradeLogs] = useState<string[]>([])
  const [appUpgradeLogsStatus, setAppUpgradeLogsStatus] = useState('')
  const [appUpgradeError, setAppUpgradeError] = useState<string | null>(null)
  const [appUpgradeComplete, setAppUpgradeComplete] = useState(false)
  const [appUpgradeLocked, setAppUpgradeLocked] = useState(false)
  const [appUpgradeStartedVersion, setAppUpgradeStartedVersion] = useState('')
  const [appUpgradeLogSince, setAppUpgradeLogSince] = useState('')
  const [enableLoginModalOpen, setEnableLoginModalOpen] = useState(false)
  const [enableLoginBusy, setEnableLoginBusy] = useState(false)
  const [enableLoginMessage, setEnableLoginMessage] = useState('')
  const [enableLoginError, setEnableLoginError] = useState<string | null>(null)

  const wearWarnThreshold = 75
  const tempWarnThreshold = 70

  const syncLabel = (info: any) => {
    if (!info || typeof info.verification_progress !== 'number') {
      return t('common.na')
    }
    return `${(info.verification_progress * 100).toFixed(2)}%`
  }

  const formatGB = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return `${gbFormatter.format(value)} GB`
  }

  const formatDBSizeGB = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return t('common.na')
    return `${dbGBFormatter.format(value)} GB`
  }

  const formatPercent = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return percentFormatter.format(value)
  }

  const formatTemp = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return `${tempFormatter.format(value)} C`
  }

  const formatSats = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return satFormatter.format(value)
  }

  const compactValue = (value: string, head = 10, tail = 10) => {
    if (!value) return ''
    if (value.length <= head + tail + 3) return value
    return `${value.slice(0, head)}...${value.slice(-tail)}`
  }

  const copyToClipboard = async (value: string) => {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      // ignore copy failures
    }
  }

  const badgeClass = (tone: 'ok' | 'warn' | 'muted') => {
    if (tone === 'ok') {
      return 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
    }
    if (tone === 'warn') {
      return 'bg-amber-500/15 text-amber-200 border border-amber-400/30'
    }
    return 'bg-white/10 text-fog/60 border border-white/10'
  }

  const Badge = ({ label, tone }: { label: string; tone: 'ok' | 'warn' | 'muted' }) => (
    <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(tone)}`}>
      {label}
    </span>
  )

  const overallTone = status === 'ok' ? 'ok' : status === 'unavailable' ? 'warn' : 'muted'
  const statusLabel = status === 'ok'
    ? t('common.ok')
    : status === 'unavailable'
      ? t('common.unavailable')
      : t('dashboard.loadingStatus')
  const lndInfoStale = Boolean(lnd?.info_stale && lnd?.info_known)
  const lndInfoAge = Number(lnd?.info_age_seconds || 0)
  const lndInfoStaleTooLong = lndInfoStale && lndInfoAge > 900
  const lndUris = (() => {
    const raw: string[] = []
    if (Array.isArray(lnd?.uris)) {
      for (const item of lnd.uris) {
        if (typeof item === 'string') {
          raw.push(item)
        }
      }
    }
    if (typeof lnd?.uri === 'string') {
      raw.push(lnd.uri)
    }
    const seen = new Set<string>()
    return raw
      .map((item) => item.trim())
      .filter((item) => {
        if (!item || seen.has(item)) return false
        seen.add(item)
        return true
      })
  })()
  const postgresDatabases = Array.isArray(postgres?.databases) ? postgres.databases : []
  const systemActionIsShutdown = systemAction === 'shutdown'
  const systemActionTitle = systemActionIsShutdown
    ? t('dashboard.confirmShutdownTitle')
    : t('dashboard.confirmRestartTitle')
  const systemActionBody = systemActionIsShutdown
    ? t('dashboard.confirmShutdownBody')
    : t('dashboard.confirmRestartBody')
  const systemActionButtonClass = systemActionIsShutdown
    ? 'text-rose-200 border-rose-400/30'
    : 'text-amber-200 border-amber-400/30'

  const formatVersion = (value?: string) => {
    if (!value) return t('common.na')
    return value.startsWith('v') ? value : `v${value}`
  }

  const formatCheckedAt = (value?: string) => {
    if (!value) return ''
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return ''
    return parsed.toLocaleString()
  }

  useEffect(() => {
    let mounted = true
    const load = async () => {
      try {
        const [sys, disks, btc, pg, lndStatus] = await Promise.all([
          getSystem(),
          getDisk(),
          getBitcoinActive(),
          getPostgres(),
          getLndStatus()
        ])
        if (!mounted) return
        setSystem(sys)
        setDisk(Array.isArray(disks) ? disks : [])
        setBitcoin(btc)
        setPostgres(pg)
        setLnd(lndStatus)
        setStatus('ok')
      } catch {
        if (!mounted) return
        setStatus('unavailable')
      }
    }
    load()
    const timer = setInterval(load, 30000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  const loadAppUpgradeStatus = async (force = false, silent = false) => {
    if (!force && appUpgradeChecking) return
    if (!silent) {
      setAppUpgradeChecking(true)
      setAppUpgradeMessage(t('appUpgrade.checking'))
    }
    try {
      const data = await getAppUpgradeStatus(force)
      setAppUpgrade(data as AppUpgradeStatus)
      if (!silent) {
        setAppUpgradeMessage('')
      }
    } catch (err) {
      if (!silent) {
        setAppUpgradeMessage(err instanceof Error ? err.message : t('appUpgrade.statusFailed'))
      }
    } finally {
      if (!silent) {
        setAppUpgradeChecking(false)
      }
    }
  }

  useEffect(() => {
    let mounted = true
    const load = async () => {
      try {
        const data = await getAppUpgradeStatus()
        if (!mounted) return
        setAppUpgrade(data as AppUpgradeStatus)
      } catch {
        if (!mounted) return
      }
    }
    load()
    const timer = setInterval(load, 60000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  const restart = async (service: string) => {
    await restartService({ service })
  }

  const openSystemAction = (action: 'restart' | 'shutdown') => {
    setSystemAction(action)
    setSystemActionError(null)
  }

  const closeSystemAction = () => {
    if (systemActionBusy) return
    setSystemAction(null)
    setSystemActionError(null)
  }

  const confirmSystemAction = async () => {
    if (!systemAction) return
    setSystemActionBusy(true)
    setSystemActionError(null)
    try {
      await runSystemAction({ action: systemAction === 'restart' ? 'reboot' : 'shutdown' })
      setSystemAction(null)
    } catch (err) {
      setSystemActionError(err instanceof Error ? err.message : t('common.fail'))
    } finally {
      setSystemActionBusy(false)
    }
  }

  const openAppUpgradeModal = () => {
    setAppUpgradeModalOpen(true)
    setAppUpgradeLogs([])
    setAppUpgradeLogsStatus('')
    setAppUpgradeError(null)
    setAppUpgradeComplete(false)
    setAppUpgradeMessage('')
    setAppUpgradeLocked(Boolean(appUpgrade?.running))
    setAppUpgradeStartedVersion('')
    setAppUpgradeLogSince(appUpgrade?.running ? '' : new Date().toISOString())
  }

  const closeAppUpgradeModal = () => {
    if (appUpgradeBusy || (appUpgradeLocked && !appUpgradeError && !appUpgradeComplete)) return
    setAppUpgradeModalOpen(false)
  }

  const startAppUpgradeFlow = async () => {
    if (!appUpgrade?.latest_version || appUpgradeBusy) return
    const sinceNow = new Date(Date.now() - 15000).toISOString()
    setAppUpgradeStartedVersion(appUpgrade.latest_version)
    setAppUpgradeLogSince(sinceNow)
    setAppUpgradeBusy(true)
    setAppUpgradeError(null)
    setAppUpgradeComplete(false)
    setAppUpgradeMessage(t('appUpgrade.starting'))
    try {
      await startAppUpgrade({ target_version: appUpgrade.latest_version })
      setAppUpgradeMessage(t('appUpgrade.started'))
      setAppUpgradeModalOpen(true)
      setAppUpgradeLocked(true)
    } catch (err) {
      const message = err instanceof Error ? err.message : t('appUpgrade.startFailed')
      setAppUpgradeMessage(message)
      setAppUpgradeError(message)
      setAppUpgradeLocked(false)
    } finally {
      setAppUpgradeBusy(false)
      loadAppUpgradeStatus(true, true)
    }
  }

  useEffect(() => {
    if (!appUpgradeModalOpen) return
    let mounted = true
    const loadLogs = async () => {
      setAppUpgradeLogsStatus(t('appUpgrade.loadingLogs'))
      try {
        const res = await getLogs('app-upgrade', 200, appUpgradeLogSince || undefined)
        if (!mounted) return
        const lines: string[] = Array.isArray(res?.lines) ? res.lines : []
        setAppUpgradeLogs(lines)
        const completed = lines.some((line) => line.includes('App upgrade complete'))
        const errorLine = [...lines].reverse().find((line) =>
          line.includes('[ERROR]') || line.toLowerCase().includes('failed')
        )
        if (completed) {
          setAppUpgradeComplete(true)
          setAppUpgradeLocked(false)
        }
        if (errorLine) {
          setAppUpgradeError(errorLine)
          setAppUpgradeLocked(false)
        }
        setAppUpgradeLogsStatus('')
      } catch (err) {
        if (!mounted) return
        setAppUpgradeLogsStatus(err instanceof Error ? err.message : t('appUpgrade.logFetchFailed'))
      }
    }
    const refreshStatus = async () => {
      try {
        const data = await getAppUpgradeStatus()
        if (!mounted) return
        const next = data as AppUpgradeStatus
        setAppUpgrade(next)
        if (next.running) {
          setAppUpgradeLocked(true)
        } else if (appUpgradeLocked) {
          setAppUpgradeLocked(false)
        }
        if (appUpgradeStartedVersion && !next.running && !appUpgradeError && !next.update_available) {
          setAppUpgradeComplete(true)
        }
      } catch {
        // ignore status refresh errors while modal is open
      }
    }

    loadLogs()
    refreshStatus()
    const timer = setInterval(() => {
      loadLogs()
      refreshStatus()
    }, 4000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [appUpgradeModalOpen, t, appUpgradeLogSince, appUpgradeLocked, appUpgradeStartedVersion, appUpgradeError])

  const showConfirmAppUpgrade = Boolean(appUpgrade?.update_available) && !appUpgradeComplete

  const openEnableLoginModal = () => {
    setEnableLoginModalOpen(true)
    setEnableLoginMessage('')
    setEnableLoginError(null)
  }

  const closeEnableLoginModal = () => {
    if (enableLoginBusy) return
    setEnableLoginModalOpen(false)
    setEnableLoginError(null)
  }

  const confirmEnableLogin = async () => {
    if (enableLoginBusy) return
    setEnableLoginBusy(true)
    setEnableLoginError(null)
    setEnableLoginMessage(t('auth.legacyEnableStarting'))
    try {
      await enableLoginAuth()
      setEnableLoginMessage(t('auth.legacyEnableRestarting'))
      window.setTimeout(() => {
        window.location.reload()
      }, 2500)
    } catch (err) {
      setEnableLoginError(err instanceof Error ? err.message : t('auth.legacyEnableFailed'))
      setEnableLoginMessage('')
    } finally {
      setEnableLoginBusy(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <p className="text-sm text-fog/60">{t('dashboard.systemPulse')}</p>
            <div className="flex items-center gap-3">
              <h2 className="text-2xl font-semibold">{t('dashboard.overallStatus')}</h2>
              <Badge label={statusLabel} tone={overallTone} />
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2 sm:justify-end">
            <button className="btn-secondary text-xs px-3 py-2 sm:text-sm sm:px-4" onClick={() => restart('lnd')} type="button">
              {t('dashboard.restartLnd')}
            </button>
            <button className="btn-secondary text-xs px-3 py-2 sm:text-sm sm:px-4" onClick={() => restart('lightningos-manager')} type="button">
              {t('dashboard.restartManager')}
            </button>
            <div className="flex flex-wrap items-center gap-2 rounded-2xl border border-white/10 bg-white/5 px-2 py-1 w-full sm:w-auto">
              <span className="text-[10px] uppercase tracking-[0.2em] text-fog/50">{t('dashboard.systemActions')}</span>
              <button
                className="btn-secondary text-[11px] px-2 py-1 sm:text-xs sm:px-3 sm:py-1.5 text-amber-200 border-amber-400/30"
                onClick={() => openSystemAction('restart')}
                type="button"
              >
                {t('dashboard.safeRestart')}
              </button>
              <button
                className="btn-secondary text-[11px] px-2 py-1 sm:text-xs sm:px-3 sm:py-1.5 text-rose-200 border-rose-400/30"
                onClick={() => openSystemAction('shutdown')}
                type="button"
              >
                {t('dashboard.safeShutdown')}
              </button>
            </div>
          </div>
        </div>
      </div>

      {authState?.enabled === false && (
        <div className="section-card border-amber-400/30 bg-amber-500/10">
          <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
            <div className="max-w-3xl">
              <p className="text-xs uppercase tracking-[0.3em] text-amber-200">{t('auth.legacyEnableKicker')}</p>
              <h3 className="mt-3 text-xl font-semibold">{t('auth.legacyEnableTitle')}</h3>
              <p className="mt-3 text-sm text-fog/75">{t('auth.legacyEnableBody')}</p>
              <p className="mt-2 text-xs text-fog/55">{t('auth.legacyEnableHint')}</p>
            </div>
            <div className="flex shrink-0 items-center gap-3">
              <button className="btn-secondary text-amber-200 border-amber-400/30" type="button" onClick={openEnableLoginModal}>
                {t('auth.legacyEnableAction')}
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">{t('dashboard.lnd')}</h3>
            <div className="text-right">
              <div className="flex items-center justify-end gap-2 text-xs text-fog/60">
                <span>{lnd?.version ? `v${lnd.version}` : ''}</span>
                {lndInfoStale && (
                  <span className="uppercase text-[10px] text-fog/40">{t('dashboard.cached')}</span>
                )}
              </div>
              {(lnd?.pubkey || lndUris.length > 0) && (
                <div className="mt-2 space-y-1 text-xs text-fog/60">
                  {lnd?.pubkey && (
                    <div className="flex items-center justify-end gap-2">
                      <span className="text-fog/50">{t('dashboard.pubkey')}</span>
                      <span
                        className="font-mono text-fog/70 max-w-[220px] truncate"
                        title={lnd.pubkey}
                      >
                        {compactValue(lnd.pubkey)}
                      </span>
                      <button
                        className="text-fog/50 hover:text-fog"
                        onClick={() => copyToClipboard(lnd.pubkey)}
                        title={t('dashboard.copyPubkey')}
                        aria-label={t('dashboard.copyPubkey')}
                      >
                        <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6">
                          <rect x="9" y="9" width="11" height="11" rx="2" />
                          <rect x="4" y="4" width="11" height="11" rx="2" />
                        </svg>
                      </button>
                    </div>
                  )}
                  {lndUris.map((uri, idx) => (
                    <div key={uri} className="flex items-center justify-end gap-2">
                      <span className="text-fog/50">{idx === 0 ? t('dashboard.uri') : ''}</span>
                      <span
                        className="font-mono text-fog/70 max-w-[220px] truncate"
                        title={uri}
                      >
                        {compactValue(uri)}
                      </span>
                      <button
                        className="text-fog/50 hover:text-fog"
                        onClick={() => copyToClipboard(uri)}
                        title={t('dashboard.copyUri')}
                        aria-label={t('dashboard.copyUri')}
                      >
                        <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6">
                          <rect x="9" y="9" width="11" height="11" rx="2" />
                          <rect x="4" y="4" width="11" height="11" rx="2" />
                        </svg>
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
          {lnd ? (
            <div className="mt-4 text-sm space-y-2">
              <div className="flex justify-between">
                <span>{t('dashboard.wallet')}</span>
                <Badge label={lnd.wallet_state} tone={lnd.wallet_state === 'unlocked' ? 'ok' : 'warn'} />
              </div>
              <div className="flex justify-between items-center">
                <span>{t('dashboard.synced')}</span>
                <div className="flex items-center gap-2">
                  <Badge
                    label={
                      lnd.synced_to_chain
                        ? (lndInfoStale && !lndInfoStaleTooLong ? t('dashboard.chainCached') : t('dashboard.chain'))
                        : t('dashboard.chainPending')
                    }
                    tone={
                      lnd.synced_to_chain
                        ? (lndInfoStale && !lndInfoStaleTooLong ? 'muted' : 'ok')
                        : 'warn'
                    }
                  />
                  <Badge
                    label={
                      lnd.synced_to_graph
                        ? (lndInfoStale && !lndInfoStaleTooLong ? t('dashboard.graphCached') : t('dashboard.graph'))
                        : t('dashboard.graphPending')
                    }
                    tone={
                      lnd.synced_to_graph
                        ? (lndInfoStale && !lndInfoStaleTooLong ? 'muted' : 'ok')
                        : 'warn'
                    }
                  />
                </div>
              </div>
              <div className="flex justify-between items-center">
                <span>{t('dashboard.channels')}</span>
                <div className="flex items-center gap-2">
                  <Badge label={t('dashboard.activeCount', { count: lnd.channels.active })} tone={lnd.channels.active > 0 ? 'ok' : 'warn'} />
                  <Badge label={t('dashboard.inactiveCount', { count: lnd.channels.inactive })} tone={lnd.channels.inactive > 0 ? 'warn' : 'muted'} />
                </div>
              </div>
              {lnd.db_backend === 'bolt' && (
                <div className="flex justify-between">
                  <span>{t('dashboard.channelDbSize')}</span>
                  <span>{formatDBSizeGB(lnd?.channel_db_size_gb)}</span>
                </div>
              )}
              <div className="flex justify-between">
                <span>{t('dashboard.balances')}</span>
                <span>{t('dashboard.balanceSummary', {
                  onchain: formatSats(lnd?.balances?.onchain_sat),
                  lightning: formatSats(lnd?.balances?.lightning_sat)
                })}</span>
              </div>
            </div>
          ) : (
            <p className="text-fog/60 mt-4">{t('dashboard.loadingLndStatus')}</p>
          )}
        </div>

        <div className="section-card">
          <h3 className="text-lg font-semibold">
            {bitcoin?.mode === 'local' ? t('dashboard.bitcoinLocal') : t('dashboard.bitcoinRemote')}
          </h3>
          {bitcoin ? (
            <div className="mt-4 text-sm space-y-2">
              <div className="flex justify-between"><span>{t('dashboard.host')}</span><span>{bitcoin.rpchost}</span></div>
              <div className="flex justify-between"><span>{t('dashboard.rpc')}</span><Badge label={bitcoin.rpc_ok ? t('common.ok') : t('common.fail')} tone={bitcoin.rpc_ok ? 'ok' : 'warn'} /></div>
              <div className="flex justify-between"><span>{t('dashboard.zmqRawBlock')}</span><Badge label={bitcoin.zmq_rawblock_ok ? t('common.ok') : t('common.fail')} tone={bitcoin.zmq_rawblock_ok ? 'ok' : 'warn'} /></div>
              <div className="flex justify-between"><span>{t('dashboard.zmqRawTx')}</span><Badge label={bitcoin.zmq_rawtx_ok ? t('common.ok') : t('common.fail')} tone={bitcoin.zmq_rawtx_ok ? 'ok' : 'warn'} /></div>
              {bitcoin.rpc_ok && (
                <>
                  <div className="flex justify-between"><span>{t('dashboard.chain')}</span><span>{bitcoin.chain || t('common.na')}</span></div>
                  <div className="flex justify-between"><span>{t('dashboard.version')}</span><span>{bitcoin.subversion || (typeof bitcoin.version === 'number' ? bitcoin.version : t('common.na'))}</span></div>
                  <div className="flex justify-between"><span>{t('dashboard.blocks')}</span><span>{bitcoin.blocks ?? t('common.na')}</span></div>
                  <div className="flex justify-between"><span>{t('dashboard.sync')}</span><span>{syncLabel(bitcoin)}</span></div>
                </>
              )}
            </div>
          ) : (
            <p className="text-fog/60 mt-4">{t('dashboard.loadingBitcoinStatus')}</p>
          )}
        </div>

        <div className="section-card">
          <h3 className="text-lg font-semibold">{t('dashboard.postgres')}</h3>
          {postgres ? (
            <div className="mt-4 text-sm space-y-2">
              <div className="flex justify-between"><span>{t('dashboard.service')}</span><Badge label={postgres.service_active ? t('common.active') : t('common.inactive')} tone={postgres.service_active ? 'ok' : 'warn'} /></div>
              <div className="flex justify-between"><span>{t('dashboard.version')}</span><span>{postgres.version || t('common.na')}</span></div>
              {postgresDatabases.length ? (
                <div className="mt-3 space-y-3">
                  {postgresDatabases.map((db: any) => {
                    const sourceLabel = db?.source === 'lnd' ? 'LND' : db?.source === 'lightningos' ? 'LIGHTNINGOS' : ''
                    const sizeLabel = db?.available ? `${db?.size_mb ?? 0} MB` : t('common.na')
                    const connLabel = db?.available ? db?.connections ?? 0 : t('common.na')
                    return (
                      <div key={`${db?.source || 'db'}-${db?.name || 'unknown'}`} className="rounded-2xl border border-white/10 bg-ink/40 px-3 py-2 space-y-1">
                        <div className="flex items-center justify-between">
                          <span className="text-sm font-medium text-fog/80">{db?.name || t('common.na')}</span>
                          {sourceLabel && <Badge label={sourceLabel} tone="muted" />}
                        </div>
                        <div className="flex justify-between text-xs text-fog/70"><span>{t('dashboard.dbSize')}</span><span>{sizeLabel}</span></div>
                        <div className="flex justify-between text-xs text-fog/70"><span>{t('dashboard.connections')}</span><span>{connLabel}</span></div>
                      </div>
                    )
                  })}
                </div>
              ) : (
                <>
                  <div className="flex justify-between"><span>{t('dashboard.dbSize')}</span><span>{postgres.db_size_mb} MB</span></div>
                  <div className="flex justify-between"><span>{t('dashboard.connections')}</span><span>{postgres.connections}</span></div>
                </>
              )}
            </div>
          ) : (
            <p className="text-fog/60 mt-4">{t('dashboard.loadingPostgresStatus')}</p>
          )}
        </div>

        <div className="section-card">
          <h3 className="text-lg font-semibold">{t('dashboard.system')}</h3>
          {system ? (
            <div className="mt-4 grid grid-cols-2 gap-4 text-sm">
              <div>
                <p className="text-fog/60">{t('dashboard.cpuLoad')}</p>
                <p>{system.cpu_load_1?.toFixed?.(2)} / {system.cpu_percent?.toFixed?.(1)}%</p>
              </div>
              <div>
                <p className="text-fog/60">{t('dashboard.ramUsed')}</p>
                <p>{system.ram_used_mb} / {system.ram_total_mb} MB</p>
              </div>
              <div>
                <p className="text-fog/60">{t('dashboard.uptime')}</p>
                <p>{t('dashboard.uptimeHours', { count: Math.round(system.uptime_sec / 3600) })}</p>
              </div>
              <div>
                <p className="text-fog/60">{t('dashboard.temp')}</p>
                <p>{system.temperature_c?.toFixed?.(1)} C</p>
              </div>
            </div>
          ) : (
            <p className="text-fog/60 mt-4">{t('dashboard.loadingSystemInfo')}</p>
          )}
        </div>
      </div>

      <div className="section-card">
        <h3 className="text-lg font-semibold">{t('dashboard.disks')}</h3>
        {disk.length ? (
          <div className="mt-4 grid gap-3">
            {disk.map((item) => {
              const totalLabel = formatGB(item.total_gb)
              const usedLabel = formatGB(item.used_gb)
              const percentLabel = formatPercent(item.used_percent)
              const tempLabel = formatTemp(item.temperature_c)
              const wearWarn = typeof item.wear_percent_used === 'number' && item.wear_percent_used >= wearWarnThreshold
              const tempWarn = typeof item.temperature_c === 'number' && item.temperature_c >= tempWarnThreshold
              const partitions = Array.isArray(item.partitions) ? item.partitions : []
              return (
              <div key={item.device} className="flex flex-col lg:flex-row lg:items-center lg:justify-between bg-ink/40 rounded-2xl p-4">
                <div>
                  <p className="text-sm text-fog/70">{item.device} ({item.type})</p>
                  <p className="text-xs text-fog/50">{t('dashboard.powerOnHours', { count: item.power_on_hours })}</p>
                  <p className="text-xs text-fog/50">
                    {t('dashboard.diskUsageSummary', { total: totalLabel, used: usedLabel, percent: percentLabel })}
                  </p>
                  {partitions.length > 0 && (
                    <div className="mt-2 space-y-1 text-[11px] text-fog/50">
                      {partitions.map((part: any) => {
                        const partTotal = formatGB(part.total_gb)
                        const partUsed = formatGB(part.used_gb)
                        const partPercent = formatPercent(part.used_percent)
                        return (
                          <div key={part.device} className="flex flex-wrap items-center gap-2">
                            <span className="font-mono text-fog/70">{part.device}</span>
                            {part.mount && <span className="text-fog/50">{part.mount}</span>}
                            <span>{t('disks.size')}: {partTotal}</span>
                            <span>{t('disks.used')}: {t('disks.usedValue', { used: partUsed, percent: partPercent })}</span>
                          </div>
                        )
                      })}
                    </div>
                  )}
                </div>
                <div className="text-sm text-fog/80 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span>{t('dashboard.wearDaysLeft', { wear: item.wear_percent_used, days: item.days_left_estimate })}</span>
                    {wearWarn && <Badge label={t('disks.wearWarn')} tone="warn" />}
                  </div>
                  <div className="flex flex-wrap items-center gap-2 text-xs text-fog/60">
                    <span>{t('disks.temp')}: {tempLabel}</span>
                    {tempWarn && <Badge label={t('disks.tempWarn')} tone="warn" />}
                  </div>
                </div>
                <div className="text-xs text-fog/60">{t('dashboard.smartLabel', { status: item.smart_status })}</div>
              </div>
            )})}
          </div>
        ) : (
          <p className="text-fog/60 mt-4">{t('dashboard.noDiskData')}</p>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h3 className="text-lg font-semibold">{t('appUpgrade.title')}</h3>
            <p className="text-fog/60">{t('appUpgrade.subtitle')}</p>
          </div>
          <button
            className="btn-secondary"
            onClick={() => loadAppUpgradeStatus(true)}
            disabled={appUpgradeChecking}
          >
            {appUpgradeChecking ? t('appUpgrade.checking') : t('common.refresh')}
          </button>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 text-sm">
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('appUpgrade.current')}</span>
            <span className="font-mono text-fog">{formatVersion(appUpgrade?.current_version)}</span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('appUpgrade.latest')}</span>
            <span className="font-mono text-fog">{formatVersion(appUpgrade?.latest_version)}</span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('appUpgrade.channel')}</span>
            <span className="text-fog/90">
              {appUpgrade?.latest_channel
                ? t(`appUpgrade.channels.${appUpgrade.latest_channel}`)
                : t('common.na')}
            </span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('appUpgrade.checkedAt')}</span>
            <span className="text-fog/90">{formatCheckedAt(appUpgrade?.checked_at) || t('common.na')}</span>
          </div>
        </div>

        {appUpgrade?.error && (
          <p className="text-sm text-rose-200">{t('appUpgrade.statusError', { error: appUpgrade.error })}</p>
        )}

        {!appUpgrade?.error && (
          <p className="text-sm text-fog/70">
            {appUpgrade?.running
              ? t('appUpgrade.inProgress')
              : appUpgrade?.update_available
                ? t('appUpgrade.updateAvailable')
                : t('appUpgrade.upToDate')}
          </p>
        )}

        {appUpgradeMessage && <p className="text-sm text-brass">{appUpgradeMessage}</p>}

        <div className="flex flex-wrap items-center gap-3">
          <button
            className="btn-primary"
            onClick={openAppUpgradeModal}
            disabled={!appUpgrade?.update_available && !appUpgrade?.running}
          >
            {appUpgrade?.running ? t('appUpgrade.viewLogs') : t('appUpgrade.upgrade')}
          </button>
        </div>

        <p className="text-xs text-fog/50">{t('appUpgrade.warning')}</p>
      </div>

      {systemAction && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={closeSystemAction} aria-hidden="true" />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="system-action-title"
            className="relative z-10 w-full max-w-md rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <h4 id="system-action-title" className="text-lg font-semibold">{systemActionTitle}</h4>
            <p className="mt-2 text-sm text-fog/70">{systemActionBody}</p>
            {systemActionError && (
              <p className="mt-3 text-sm text-rose-200">{systemActionError}</p>
            )}
            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${systemActionBusy ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={closeSystemAction}
                type="button"
                autoFocus
              >
                {t('common.cancel')}
              </button>
              <button
                className={`btn-secondary ${systemActionButtonClass} ${systemActionBusy ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={confirmSystemAction}
                type="button"
              >
                {t('common.ok')}
              </button>
            </div>
          </div>
        </div>
      )}

      {enableLoginModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={closeEnableLoginModal} aria-hidden="true" />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="enable-login-title"
            className="relative z-10 w-full max-w-xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <h4 id="enable-login-title" className="text-lg font-semibold">{t('auth.legacyEnableConfirmTitle')}</h4>
            <p className="mt-2 text-sm text-fog/70">{t('auth.legacyEnableConfirmBody')}</p>
            <p className="mt-3 text-xs text-amber-200">{t('auth.legacyEnableConfirmHint')}</p>
            {enableLoginMessage && <p className="mt-4 text-sm text-brass">{enableLoginMessage}</p>}
            {enableLoginError && <p className="mt-3 text-sm text-rose-200">{enableLoginError}</p>}
            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${enableLoginBusy ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={closeEnableLoginModal}
                type="button"
              >
                {t('common.cancel')}
              </button>
              <button
                className={`btn-secondary text-amber-200 border-amber-400/30 ${enableLoginBusy ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={confirmEnableLogin}
                type="button"
              >
                {enableLoginBusy ? t('auth.legacyEnableBusy') : t('auth.legacyEnableAction')}
              </button>
            </div>
          </div>
        </div>
      )}

      {appUpgradeModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div
            className="absolute inset-0 bg-black/60 backdrop-blur-sm"
            onClick={closeAppUpgradeModal}
            aria-hidden="true"
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="app-upgrade-title"
            className="relative z-10 w-full max-w-3xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <h4 id="app-upgrade-title" className="text-lg font-semibold">{t('appUpgrade.confirmTitle')}</h4>
            <p className="mt-2 text-sm text-fog/70">
              {t('appUpgrade.confirmBody', { version: formatVersion(appUpgrade?.latest_version) })}
            </p>
            <p className="mt-3 text-xs text-rose-200">{t('appUpgrade.confirmWarning')}</p>
            {appUpgradeMessage && <p className="mt-3 text-sm text-brass">{appUpgradeMessage}</p>}
            {appUpgradeError && <p className="mt-2 text-sm text-rose-200">{appUpgradeError}</p>}
            {appUpgradeComplete && !appUpgradeError && (
              <p className="mt-2 text-sm text-emerald-200">{t('appUpgrade.completed')}</p>
            )}

            <div className="mt-4">
              <div className="flex items-center justify-between">
                <span className="text-sm text-fog/70">{t('appUpgrade.logsTitle')}</span>
                <span className="text-xs text-fog/50">
                  {appUpgrade?.running ? t('appUpgrade.inProgress') : t('appUpgrade.logsHint')}
                </span>
              </div>
              {appUpgradeLogsStatus && <p className="mt-2 text-xs text-brass">{appUpgradeLogsStatus}</p>}
              <div className="mt-2 max-h-[320px] overflow-y-auto rounded-2xl border border-white/10 bg-ink/70 p-3 text-xs font-mono whitespace-pre-wrap">
                {appUpgradeLogs.length ? appUpgradeLogs.join('\n') : t('appUpgrade.noLogs')}
              </div>
            </div>

            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${(appUpgradeBusy || (appUpgradeLocked && !appUpgradeError && !appUpgradeComplete)) ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={closeAppUpgradeModal}
                type="button"
                disabled={appUpgradeBusy || (appUpgradeLocked && !appUpgradeError && !appUpgradeComplete)}
              >
                {appUpgradeComplete || appUpgradeError ? t('common.close') : t('common.cancel')}
              </button>
              {showConfirmAppUpgrade && (
                <button
                  className={`btn-secondary text-amber-200 border-amber-400/30 ${appUpgradeBusy ? 'opacity-60 pointer-events-none' : ''}`}
                  onClick={startAppUpgradeFlow}
                  type="button"
                  disabled={!appUpgrade?.update_available || appUpgrade?.running}
                >
                  {appUpgradeBusy ? t('appUpgrade.upgrading') : t('appUpgrade.confirmUpgrade')}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
