import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getAppAdminPassword, getAppStorageTargets, getApps, getBitcoinLocalStatus, getBitcoinSource, getElectrsStatus, getPeerswapElementsSource, installApp, resetAppAdmin, startApp, stopApp, testPeerswapElementsSource, uninstallApp, type StorageTarget } from '../api'
import lndgIcon from '../assets/apps/lndg.ico'
import bitcoincoreIcon from '../assets/apps/bitcoincore.png'
import elementsIcon from '../assets/apps/elements.png'
import peerswapIcon from '../assets/apps/peerswap.png'
import robosatsIcon from '../assets/apps/robosats.svg'
import lnbitsIcon from '../assets/apps/lnbits.svg'
import fswapIcon from '../assets/apps/fswap.png'
import publicPoolIcon from '../assets/apps/public-pool.svg'
import electrsIcon from '../assets/apps/electrs.svg'
import mempoolIcon from '../assets/apps/mempool.svg'
import fedimintIcon from '../assets/apps/fedimint.svg'
import cpuLotteryIcon from '../assets/apps/cpu-lottery.svg'
import taprootAssetsIcon from '../assets/apps/taproot-assets.svg'
import barkWalletIcon from '../assets/apps/bark-wallet.svg'
import btcpayIcon from '../assets/apps/btcpay.svg'
import CpuMinerStats from '../components/CpuMinerStats'

type AppInfo = {
  id: string
  name: string
  description: string
  installed: boolean
  status: string
  port?: number
  scheme?: string
  external_url?: string
  admin_password_path?: string
  available?: boolean
  unavailable_reason?: string
  unavailable_message?: string
  ufw_active?: boolean
  ufw_command?: string
}

type BitcoinLocalStatus = {
  source?: 'app' | 'external' | 'none'
}

type BitcoinSourceStatus = {
  source?: 'local' | 'remote'
}

type BitcoinMode = 'remote' | 'local_app' | 'local_external' | 'local_none'
type InstallFilter = 'all' | 'installed' | 'not_installed'

type ElectrsStatus = {
  installed: boolean
  running: boolean
  rpc_port: number
  index_height: number
  tip_height: number
  indexing: boolean
  message?: string
}

type AppAction = 'install' | 'start' | 'stop' | 'uninstall'
type InstallPayload = {
  data_dir?: string
  storage_mount?: string
  elements_mode?: 'local' | 'remote'
  elements_rpc_url?: string
  elements_rpc_user?: string
  elements_rpc_password?: string
}

const iconMap: Record<string, string> = {
  lndg: lndgIcon,
  bitcoincore: bitcoincoreIcon,
  elements: elementsIcon,
  peerswap: peerswapIcon,
  robosats: robosatsIcon,
  lnbits: lnbitsIcon,
  fswap: fswapIcon,
  publicpool: publicPoolIcon,
  cpuminer: cpuLotteryIcon,
  electrs: electrsIcon,
  mempool: mempoolIcon,
  'fedimint-guardian': fedimintIcon,
  'fedimint-gateway': fedimintIcon,
  tapd: taprootAssetsIcon,
  'bark-wallet': barkWalletIcon,
  btcpay: btcpayIcon
}

const internalRoutes: Record<string, string> = {
  bitcoincore: 'bitcoin-local',
  elements: 'elements',
  fswap: 'pay-boleto',
  tapd: 'taproot-assets'
}

const statusStyles: Record<string, string> = {
  running: 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30',
  stopped: 'bg-amber-500/15 text-amber-200 border border-amber-400/30',
  unknown: 'bg-rose-500/15 text-rose-200 border border-rose-400/30',
  not_installed: 'bg-white/10 text-fog/60 border border-white/10'
}

const publicPoolUIPortFallback = 8081
const publicPoolStratumPort = 3333
const fedimintGuardianP2PPort = 8173
const fedimintGuardianAPIPort = 8174
const fedimintGatewayUIPort = 8176
const fedimintGatewayIrohPort = 8177
const APP_STORE_INSTALL_FILTER_KEY = 'app_store_install_filter'
const bitcoinCoreDefaultDataDir = '/data/bitcoin'
const elementsDefaultDataDir = '/data/elements'
const peerswapDefaultRemoteUrl = 'http://elements.br-ln.com:8086'
const hiddenStoreAppIds = new Set(['depixbuy'])

export default function AppStore() {
  const { t } = useTranslation()
  const [apps, setApps] = useState<AppInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState<Record<string, string>>({})
  const [copying, setCopying] = useState<Record<string, boolean>>({})
  const [hideBitcoinCore, setHideBitcoinCore] = useState(false)
  const [bitcoinMode, setBitcoinMode] = useState<BitcoinMode>('remote')
  const [electrsStatus, setElectrsStatus] = useState<ElectrsStatus | null>(null)
  const [bitcoinCoreInstallOpen, setBitcoinCoreInstallOpen] = useState(false)
  const [bitcoinCoreUseStorageMount, setBitcoinCoreUseStorageMount] = useState(false)
  const [bitcoinCoreStorageTargets, setBitcoinCoreStorageTargets] = useState<StorageTarget[]>([])
  const [bitcoinCoreSelectedMount, setBitcoinCoreSelectedMount] = useState('')
  const [bitcoinCoreStorageLoading, setBitcoinCoreStorageLoading] = useState(false)
  const [bitcoinCoreStorageError, setBitcoinCoreStorageError] = useState('')
  const [elementsInstallOpen, setElementsInstallOpen] = useState(false)
  const [elementsUseStorageMount, setElementsUseStorageMount] = useState(false)
  const [elementsStorageTargets, setElementsStorageTargets] = useState<StorageTarget[]>([])
  const [elementsSelectedMount, setElementsSelectedMount] = useState('')
  const [elementsStorageLoading, setElementsStorageLoading] = useState(false)
  const [elementsStorageError, setElementsStorageError] = useState('')
  const [peerswapRemoteOpen, setPeerswapRemoteOpen] = useState(false)
  const [peerswapRemoteUrl, setPeerswapRemoteUrl] = useState(peerswapDefaultRemoteUrl)
  const [peerswapRemoteUser, setPeerswapRemoteUser] = useState('')
  const [peerswapRemotePassword, setPeerswapRemotePassword] = useState('')
  const [peerswapRemoteTesting, setPeerswapRemoteTesting] = useState(false)
  const [peerswapRemoteMessage, setPeerswapRemoteMessage] = useState('')
  const [peerswapRemoteTested, setPeerswapRemoteTested] = useState(false)
  const [barkWalletInstallOpen, setBarkWalletInstallOpen] = useState(false)
  const [installFilter, setInstallFilter] = useState<InstallFilter>(() => {
    if (typeof window === 'undefined') return 'all'
    const stored = window.localStorage.getItem(APP_STORE_INSTALL_FILTER_KEY)
    if (stored === 'all' || stored === 'installed' || stored === 'not_installed') {
      return stored
    }
    return 'all'
  })

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

  const resolveUnavailableMessage = (app: AppInfo) => {
    switch (app.unavailable_reason) {
      case 'requires_local_bitcoin_source':
        return t('appStore.unavailable.requiresLocalBitcoinSource')
      case 'requires_bitcoin_store':
        return t('appStore.unavailable.requiresBitcoinStore')
      case 'requires_bitcoin_rpc':
        return t('appStore.unavailable.requiresBitcoinRpc')
      case 'requires_unpruned_bitcoin':
        return t('appStore.unavailable.requiresUnprunedBitcoin')
      case 'requires_txindex':
        return t('appStore.unavailable.requiresTxIndex')
      case 'requires_htlc_interceptor':
        return t('appStore.unavailable.requiresHtlcInterceptor')
      default:
        return app.unavailable_message || ''
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
    Promise.all([
      getBitcoinLocalStatus().catch(() => ({ source: 'none' } as BitcoinLocalStatus)),
      getBitcoinSource().catch(() => ({ source: 'remote' } as BitcoinSourceStatus))
    ])
      .then(([localStatus, sourceStatus]) => {
        setHideBitcoinCore(localStatus?.source === 'external')
        if (sourceStatus?.source !== 'local') {
          setBitcoinMode('remote')
          return
        }
        if (localStatus?.source === 'app') {
          setBitcoinMode('local_app')
          return
        }
        if (localStatus?.source === 'external') {
          setBitcoinMode('local_external')
          return
        }
        setBitcoinMode('local_none')
      })
      .catch(() => {
        setHideBitcoinCore(false)
        setBitcoinMode('remote')
      })
  }, [])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem(APP_STORE_INSTALL_FILTER_KEY, installFilter)
  }, [installFilter])

  const loadBitcoinCoreStorageTargets = () => {
    setBitcoinCoreStorageLoading(true)
    setBitcoinCoreStorageError('')
    getAppStorageTargets('bitcoincore')
      .then((data: { targets?: StorageTarget[] }) => {
        const targets = data?.targets || []
        setBitcoinCoreStorageTargets(targets)
        setBitcoinCoreSelectedMount((current) => {
          if (current && targets.some((target) => target.eligible && target.mount === current)) return current
          return targets.find((target) => target.eligible)?.mount || ''
        })
      })
      .catch((err: unknown) => {
        setBitcoinCoreStorageError(err instanceof Error ? err.message : t('appStore.storageLoadFailed'))
        setBitcoinCoreStorageTargets([])
        setBitcoinCoreSelectedMount('')
      })
      .finally(() => setBitcoinCoreStorageLoading(false))
  }

  const loadElementsStorageTargets = () => {
    setElementsStorageLoading(true)
    setElementsStorageError('')
    getAppStorageTargets('elements')
      .then((data: { targets?: StorageTarget[] }) => {
        const targets = data?.targets || []
        setElementsStorageTargets(targets)
        setElementsSelectedMount((current) => {
          if (current && targets.some((target) => target.eligible && target.mount === current)) return current
          return targets.find((target) => target.eligible)?.mount || ''
        })
      })
      .catch((err: unknown) => {
        setElementsStorageError(err instanceof Error ? err.message : t('appStore.storageLoadFailed'))
        setElementsStorageTargets([])
        setElementsSelectedMount('')
      })
      .finally(() => setElementsStorageLoading(false))
  }

  useEffect(() => {
    if (bitcoinCoreInstallOpen && bitcoinCoreUseStorageMount) {
      loadBitcoinCoreStorageTargets()
    }
  }, [bitcoinCoreInstallOpen, bitcoinCoreUseStorageMount])

  useEffect(() => {
    if (elementsInstallOpen && elementsUseStorageMount) {
      loadElementsStorageTargets()
    }
  }, [elementsInstallOpen, elementsUseStorageMount])

  const openPeerswapRemoteInstall = () => {
    setPeerswapRemoteUrl(peerswapDefaultRemoteUrl)
    setPeerswapRemoteUser('')
    setPeerswapRemotePassword('')
    setPeerswapRemoteMessage('')
    setPeerswapRemoteTested(false)
    setPeerswapRemoteOpen(true)
  }

  const peerswapRemotePayload = () => ({
    mode: 'remote' as const,
    url: peerswapRemoteUrl.trim(),
    user: peerswapRemoteUser.trim(),
    password: peerswapRemotePassword
  })

  const handlePeerswapRemoteTest = async () => {
    setPeerswapRemoteTesting(true)
    setPeerswapRemoteMessage('')
    setPeerswapRemoteTested(false)
    try {
      const res = await testPeerswapElementsSource(peerswapRemotePayload())
      const chain = res?.chain ? ` (${res.chain})` : ''
      setPeerswapRemoteMessage(`${t('appStore.peerswapRemoteTestOk')}${chain}`)
      setPeerswapRemoteTested(true)
    } catch (err) {
      setPeerswapRemoteMessage(err instanceof Error ? err.message : t('appStore.peerswapRemoteTestFailed'))
    } finally {
      setPeerswapRemoteTesting(false)
    }
  }

  const electrsApp = apps.find((app) => app.id === 'electrs')
  const electrsRunning = Boolean(electrsApp?.installed && electrsApp?.status === 'running')
  useEffect(() => {
    if (!electrsRunning) {
      setElectrsStatus(null)
      return
    }
    let cancelled = false
    const tick = () => {
      getElectrsStatus()
        .then((data: ElectrsStatus) => {
          if (!cancelled) setElectrsStatus(data)
        })
        .catch(() => {
          if (!cancelled) setElectrsStatus(null)
        })
    }
    tick()
    const handle = window.setInterval(tick, 10_000)
    return () => {
      cancelled = true
      window.clearInterval(handle)
    }
  }, [electrsRunning])

  const handleAction = async (id: string, action: AppAction, payload?: InstallPayload) => {
    if (id === 'bark-wallet' && action === 'install' && !payload) {
      setBarkWalletInstallOpen(true)
      return
    }
    if (id === 'bitcoincore' && action === 'install' && !payload) {
      setBitcoinCoreUseStorageMount(false)
      setBitcoinCoreSelectedMount('')
      setBitcoinCoreStorageError('')
      setBitcoinCoreInstallOpen(true)
      return
    }
    if (id === 'elements' && action === 'install' && !payload) {
      setElementsUseStorageMount(false)
      setElementsSelectedMount('')
      setElementsStorageError('')
      setElementsInstallOpen(true)
      return
    }
    if (id === 'peerswap' && action === 'install' && !payload) {
      const elementsApp = apps.find((app) => app.id === 'elements')
      if (elementsApp?.installed && elementsApp.status === 'running') {
        await handleAction(id, action, { elements_mode: 'local' })
        return
      }
      try {
        const source = await getPeerswapElementsSource()
        if (source?.configured && source?.mode === 'remote') {
          await handleAction(id, action, {})
          return
        }
      } catch {
        // Fall back to collecting remote credentials below.
      }
      openPeerswapRemoteInstall()
      return
    }
    setMessage('')
    setBusy((prev) => ({ ...prev, [id]: action }))
    try {
      if (action === 'install') await installApp(id, payload)
      if (action === 'start') await startApp(id)
      if (action === 'stop') await stopApp(id)
      if (action === 'uninstall') await uninstallApp(id)
      window.dispatchEvent(new CustomEvent('apps:changed', { detail: { id, action } }))
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

  const handleBitcoinCoreInstallConfirm = async () => {
    const payload = bitcoinCoreUseStorageMount ? { storage_mount: bitcoinCoreSelectedMount } : {}
    setBitcoinCoreInstallOpen(false)
    await handleAction('bitcoincore', 'install', payload)
  }

  const handleElementsInstallConfirm = async () => {
    const payload = elementsUseStorageMount ? { storage_mount: elementsSelectedMount } : { data_dir: elementsDefaultDataDir }
    setElementsInstallOpen(false)
    await handleAction('elements', 'install', payload)
  }

  const handlePeerswapRemoteInstallConfirm = async () => {
    const payload = peerswapRemotePayload()
    setPeerswapRemoteOpen(false)
    await handleAction('peerswap', 'install', {
      elements_mode: 'remote',
      elements_rpc_url: payload.url,
      elements_rpc_user: payload.user,
      elements_rpc_password: payload.password
    })
  }

  const handleBarkWalletInstallConfirm = async () => {
    setBarkWalletInstallOpen(false)
    await handleAction('bark-wallet', 'install', {})
  }

  const handleResetAdmin = async (id: string) => {
    setMessage('')
    setBusy((prev) => ({ ...prev, [id]: 'reset-admin' }))
    try {
      await resetAppAdmin(id)
      setMessage(id === 'bark-wallet' ? t('appStore.adminPasswordResetMessage') : t('appStore.resetStoredPasswordMessage'))
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
  const storeApps = apps.filter((app) => !hiddenStoreAppIds.has(app.id))
  const baseVisibleApps = hideBitcoinCore
    ? storeApps.filter((app) => app.id !== 'bitcoincore')
    : storeApps
  const visibleApps = baseVisibleApps.filter((app) => {
    if (installFilter === 'installed') return app.installed
    if (installFilter === 'not_installed') return !app.installed
    return true
  })
  const eligibleBitcoinCoreStorageTargets = bitcoinCoreStorageTargets.filter((target) => target.eligible)
  const selectedBitcoinCoreStorageTarget = eligibleBitcoinCoreStorageTargets.find((target) => target.mount === bitcoinCoreSelectedMount)
  const eligibleElementsStorageTargets = elementsStorageTargets.filter((target) => target.eligible)
  const selectedElementsStorageTarget = eligibleElementsStorageTargets.find((target) => target.mount === elementsSelectedMount)
  const formatStorageGB = (value?: number) => {
    if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return '-'
    return value >= 100 ? value.toFixed(0) : value.toFixed(1)
  }
  const storageTargetLabel = (target: StorageTarget) => t('appStore.storageTargetOption', {
    mount: target.mount,
    free: formatStorageGB(target.free_gb),
    fstype: target.fstype || target.source || 'fs'
  })

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <h2 className="text-2xl font-semibold">{t('appStore.title')}</h2>
            <p className="text-fog/60">{t('appStore.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <button
              className={installFilter === 'all' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'}
              onClick={() => setInstallFilter('all')}
              aria-pressed={installFilter === 'all'}
              type="button"
            >
              {t('appStore.filters.all')}
            </button>
            <button
              className={installFilter === 'installed' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'}
              onClick={() => setInstallFilter('installed')}
              aria-pressed={installFilter === 'installed'}
              type="button"
            >
              {t('appStore.filters.installed')}
            </button>
            <button
              className={installFilter === 'not_installed' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'}
              onClick={() => setInstallFilter('not_installed')}
              aria-pressed={installFilter === 'not_installed'}
              type="button"
            >
              {t('appStore.filters.notInstalled')}
            </button>
          </div>
        </div>
        {message && <p className="text-sm text-brass mt-4">{message}</p>}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        {visibleApps.map((app) => {
          const busyAction = busy[app.id]
          const isBusy = Boolean(busyAction)
          const isResetting = busyAction === 'reset-admin'
          const supportsAdminReset = app.id === 'lndg' || app.id === 'bark-wallet'
          const canResetAdmin = supportsAdminReset && app.status === 'running'
          const resetTitle = canResetAdmin ? t('appStore.resetStoredPassword') : t('appStore.startAppToReset')
          const statusStyle = statusStyles[app.status] || statusStyles.unknown
          const internalRoute = internalRoutes[app.id]
          const internalRouteLabel = app.id === 'bitcoincore'
            ? t('nav.bitcoinLocal')
            : app.id === 'elements'
              ? t('nav.elements')
              : app.id === 'fswap'
                ? t('nav.payBoleto')
              : app.id === 'tapd'
                ? t('nav.taprootAssets')
              : t('appStore.internal')
          const openUrl = app.external_url || (app.port ? `${app.scheme || 'http'}://${host}:${app.port}` : '')
          const publicPoolUrl = openUrl || `http://${host}:${publicPoolUIPortFallback}`
          const publicPoolStratumEndpoint = `${host}:${publicPoolStratumPort}`
          const fedimintGatewayApiUrl = `http://${host}:${fedimintGatewayUIPort}/v1`
          const icon = iconMap[app.id]
          const unavailable = app.available === false
          const unavailableMessage = unavailable ? resolveUnavailableMessage(app) : ''
          const canCopyAdminPassword = app.id === 'lndg' || app.id === 'fedimint-gateway' || app.id === 'bark-wallet'
          return (
            <div key={app.id} className="section-card space-y-4">
              <div className="flex items-start justify-between gap-4">
                <div className="flex items-start gap-4">
                  <div className="h-12 w-12 rounded-2xl bg-transparent flex items-center justify-center overflow-hidden">
                    {icon ? (
                      <img
                        src={icon}
                        alt={`${app.name} icon`}
                        className={`h-12 w-12 rounded-2xl ${app.id === 'bark-wallet' ? 'bg-white p-2 object-contain' : app.id === 'electrs' ? 'object-contain' : 'object-cover'}`}
                      />
                    ) : (
                      <span className="text-xs text-fog/50">{t('appStore.appBadge')}</span>
                    )}
                  </div>
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="text-lg font-semibold">{app.name}</h3>
                      {app.id === 'bark-wallet' && (
                        <>
                          <span className="rounded-full border border-amber-400/30 bg-amber-500/15 px-2 py-0.5 text-[10px] uppercase tracking-wide text-amber-200">{t('appStore.barkWalletBeta')}</span>
                          <span className="rounded-full border border-white/10 bg-white/5 px-2 py-0.5 text-[10px] uppercase tracking-wide text-fog/70">{t('appStore.barkWalletMainnet')}</span>
                          <span className="rounded-full border border-white/10 bg-white/5 px-2 py-0.5 text-[10px] uppercase tracking-wide text-fog/70">{t('appStore.barkWalletSecond')}</span>
                        </>
                      )}
                    </div>
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
                    {canCopyAdminPassword && (
                      <button
                        className="text-fog/50 hover:text-fog"
                        onClick={() => handleCopyAdminPassword(app.id)}
                        title={t('appStore.copyAdminPassword')}
                        aria-label={t('appStore.copyAdminPassword')}
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
                {unavailable && unavailableMessage && (
                  <p className="text-amber-300/80">{unavailableMessage}</p>
                )}
                {app.id === 'electrs' && app.installed && (
                  <>
                    <p className="font-mono text-[11px] text-fog/70">
                      {t('appStore.electrsTcpEndpoint', { endpoint: `${host}:${electrsStatus?.rpc_port || 50001}` })}
                    </p>
                    {electrsStatus && electrsStatus.running && electrsStatus.index_height > 0 && electrsStatus.tip_height > 0 && (
                      electrsStatus.indexing ? (
                        <p>
                          {t('appStore.electrsIndexing', {
                            index: electrsStatus.index_height.toLocaleString(),
                            tip: electrsStatus.tip_height.toLocaleString(),
                            percent: ((electrsStatus.index_height / electrsStatus.tip_height) * 100).toFixed(2)
                          })}
                        </p>
                      ) : (
                        <p>{t('appStore.electrsSynced', { height: electrsStatus.index_height.toLocaleString() })}</p>
                      )
                    )}
                    {electrsStatus && electrsStatus.running && electrsStatus.index_height === 0 && (
                      <p className="text-amber-300/80">{t('appStore.electrsMetricsUnavailable')}</p>
                    )}
                  </>
                )}
                {app.id === 'publicpool' && (
                  <>
                    <p>{t('appStore.publicPoolUiAccess', { url: publicPoolUrl })}</p>
                    <p>{t('appStore.publicPoolStratumEndpoint', { endpoint: publicPoolStratumEndpoint })}</p>
                    {bitcoinMode === 'local_external' && (
                      <>
                        <p>{t('appStore.publicPoolBitcoinModeLocalExternal')}</p>
                        <p className="font-mono text-[11px] text-fog/70">{t('appStore.publicPoolConfServer')}</p>
                        <p className="font-mono text-[11px] text-fog/70">{t('appStore.publicPoolConfRpcBind')}</p>
                        <p className="font-mono text-[11px] text-fog/70">{t('appStore.publicPoolConfRpcAllow')}</p>
                        {app.ufw_active && app.ufw_command && (
                          <>
                            <p className="text-amber-300/80">{t('appStore.publicPoolUfwActive')}</p>
                            <p className="break-all font-mono text-[11px] text-fog/70">{app.ufw_command}</p>
                          </>
                        )}
                      </>
                    )}
                  </>
                )}
                {app.id === 'fedimint-guardian' && (
                  <>
                    <p>{t('appStore.fedimintGuardianUiAccess', { url: openUrl })}</p>
                    <p>{t('appStore.fedimintGuardianP2P', { port: fedimintGuardianP2PPort })}</p>
                    <p>{t('appStore.fedimintGuardianIrohApi', { port: fedimintGuardianAPIPort })}</p>
                  </>
                )}
                {app.id === 'fedimint-gateway' && (
                  <>
                    <p>{t('appStore.fedimintGatewayUiAccess', { url: openUrl })}</p>
                    <p>{t('appStore.fedimintGatewayApiUrl', { url: fedimintGatewayApiUrl })}</p>
                    <p>{t('appStore.fedimintGatewayIroh', { port: fedimintGatewayIrohPort })}</p>
                    <p>{t('appStore.fedimintGatewayLndMode')}</p>
                  </>
                )}
                {app.id === 'bark-wallet' && (
                  <>
                    <p>{t('appStore.barkWalletExternalOperator')}</p>
                    <p>{t('appStore.barkWalletNoLocalLnd')}</p>
                    <p>{t('appStore.barkWalletDataPreserved')}</p>
                  </>
                )}
              </div>

              {app.id === 'cpuminer' && app.installed && (
                <CpuMinerStats running={app.status === 'running'} />
              )}

              <div className="flex flex-wrap items-center gap-3">
                {!app.installed && (
                  <button className="btn-primary" disabled={isBusy || unavailable} title={unavailable ? unavailableMessage : undefined} onClick={() => handleAction(app.id, 'install')}>
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
                    {!internalRoute && openUrl && (
                      <a className="btn-primary" href={openUrl} target="_blank" rel="noreferrer">
                        {t('common.open')}
                      </a>
                    )}
                    {supportsAdminReset && (
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
                    <button className="btn-primary" disabled={isBusy || unavailable} title={unavailable ? unavailableMessage : undefined} onClick={() => handleAction(app.id, 'start')}>
                      {isBusy ? t('appStore.starting') : t('common.start')}
                    </button>
                    {supportsAdminReset && (
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
      {!loading && apps.length > 0 && visibleApps.length === 0 && (
        <p className="text-fog/60">{t('appStore.noAppsForFilter')}</p>
      )}

      {barkWalletInstallOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
          <div className="w-full max-w-lg rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
            <div className="space-y-2">
              <div className="flex items-center gap-3">
                <img src={barkWalletIcon} alt="" className="h-10 w-10 rounded-xl bg-white p-1.5" />
                <div>
                  <h3 className="text-lg font-semibold">{t('appStore.barkWalletInstallTitle')}</h3>
                  <p className="text-xs uppercase tracking-wide text-amber-200">{t('appStore.barkWalletInstallBadge')}</p>
                </div>
              </div>
              <p className="text-sm text-fog/60">{t('appStore.barkWalletInstallBody')}</p>
            </div>

            <div className="mt-5 space-y-3 rounded-lg border border-white/10 bg-black/20 p-4 text-sm text-fog/80">
              <p>• {t('appStore.barkWalletInstallMainnet')}</p>
              <p>• {t('appStore.barkWalletInstallExternal')}</p>
              <p>• {t('appStore.barkWalletInstallNoLnd')}</p>
              <p>• {t('appStore.barkWalletInstallHotWallet')}</p>
              <p>• {t('appStore.barkWalletInstallBackup')}</p>
            </div>

            <div className="mt-6 flex flex-wrap justify-end gap-3">
              <button className="btn-secondary" type="button" onClick={() => setBarkWalletInstallOpen(false)}>
                {t('common.cancel')}
              </button>
              <button className="btn-primary" type="button" onClick={handleBarkWalletInstallConfirm}>
                {t('appStore.install')}
              </button>
            </div>
          </div>
        </div>
      )}

      {bitcoinCoreInstallOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
          <div className="w-full max-w-lg rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
            <div className="space-y-2">
              <h3 className="text-lg font-semibold">{t('appStore.bitcoinCoreInstallTitle')}</h3>
              <p className="text-sm text-fog/60">{t('appStore.bitcoinCoreInstallBody')}</p>
            </div>

            <div className="mt-5 space-y-4">
              <label className="flex items-start gap-3 text-sm text-fog/80">
                <input
                  className="mt-1"
                  type="checkbox"
                  checked={bitcoinCoreUseStorageMount}
                  onChange={(event) => {
                    setBitcoinCoreUseStorageMount(event.target.checked)
                    if (!event.target.checked) setBitcoinCoreSelectedMount('')
                  }}
                />
                <span>{t('appStore.bitcoinCoreUseStorageMount')}</span>
              </label>

              {bitcoinCoreUseStorageMount ? (
                <div className="space-y-2">
                  <div className="flex items-center justify-between gap-3">
                    <label className="text-xs uppercase tracking-wide text-fog/50" htmlFor="bitcoin-core-storage-mount">
                      {t('appStore.storageVolumeLabel')}
                    </label>
                    <button className="text-xs text-fog/60 hover:text-fog" type="button" onClick={loadBitcoinCoreStorageTargets}>
                      {t('common.refresh')}
                    </button>
                  </div>
                  {bitcoinCoreStorageLoading ? (
                    <p className="text-sm text-fog/60">{t('appStore.storageLoadingVolumes')}</p>
                  ) : (
                    <select
                      id="bitcoin-core-storage-mount"
                      className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass disabled:opacity-60"
                      value={bitcoinCoreSelectedMount}
                      disabled={eligibleBitcoinCoreStorageTargets.length === 0}
                      onChange={(event) => setBitcoinCoreSelectedMount(event.target.value)}
                    >
                      {eligibleBitcoinCoreStorageTargets.map((target) => (
                        <option key={target.mount} value={target.mount}>
                          {storageTargetLabel(target)}
                        </option>
                      ))}
                    </select>
                  )}
                  {bitcoinCoreStorageError && <p className="text-xs text-rose-300">{bitcoinCoreStorageError}</p>}
                  {!bitcoinCoreStorageLoading && !bitcoinCoreStorageError && eligibleBitcoinCoreStorageTargets.length === 0 && (
                    <p className="text-xs text-amber-300/80">{t('appStore.storageNoEligibleVolumes')}</p>
                  )}
                  {selectedBitcoinCoreStorageTarget && (
                    <p className="break-all rounded-md border border-white/10 bg-black/20 px-3 py-2 font-mono text-xs text-fog/70">
                      {t('appStore.storageSelectedPath', { path: selectedBitcoinCoreStorageTarget.suggested_path })}
                    </p>
                  )}
                  <p className="text-xs text-fog/50">{t('appStore.bitcoinCoreDataDirHint')}</p>
                </div>
              ) : (
                <p className="break-all rounded-md border border-white/10 bg-black/20 px-3 py-2 font-mono text-xs text-fog/70">
                  {t('appStore.storageDefaultPath', { path: bitcoinCoreDefaultDataDir })}
                </p>
              )}
            </div>

            <div className="mt-6 flex flex-wrap justify-end gap-3">
              <button
                className="btn-secondary"
                type="button"
                onClick={() => setBitcoinCoreInstallOpen(false)}
              >
                {t('common.cancel')}
              </button>
              <button
                className="btn-primary"
                type="button"
                disabled={bitcoinCoreUseStorageMount && !bitcoinCoreSelectedMount}
                onClick={handleBitcoinCoreInstallConfirm}
              >
                {t('appStore.install')}
              </button>
            </div>
          </div>
        </div>
      )}

      {elementsInstallOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
          <div className="w-full max-w-lg rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
            <div className="space-y-2">
              <h3 className="text-lg font-semibold">{t('appStore.elementsInstallTitle')}</h3>
              <p className="text-sm text-fog/60">{t('appStore.elementsInstallBody')}</p>
            </div>

            <div className="mt-5 space-y-4">
              <label className="flex items-start gap-3 text-sm text-fog/80">
                <input
                  className="mt-1"
                  type="checkbox"
                  checked={elementsUseStorageMount}
                  onChange={(event) => {
                    setElementsUseStorageMount(event.target.checked)
                    if (!event.target.checked) setElementsSelectedMount('')
                  }}
                />
                <span>{t('appStore.elementsUseStorageMount')}</span>
              </label>

              {elementsUseStorageMount ? (
                <div className="space-y-2">
                  <div className="flex items-center justify-between gap-3">
                    <label className="text-xs uppercase tracking-wide text-fog/50" htmlFor="elements-storage-mount">
                      {t('appStore.storageVolumeLabel')}
                    </label>
                    <button className="text-xs text-fog/60 hover:text-fog" type="button" onClick={loadElementsStorageTargets}>
                      {t('common.refresh')}
                    </button>
                  </div>
                  {elementsStorageLoading ? (
                    <p className="text-sm text-fog/60">{t('appStore.storageLoadingVolumes')}</p>
                  ) : (
                    <select
                      id="elements-storage-mount"
                      className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass disabled:opacity-60"
                      value={elementsSelectedMount}
                      disabled={eligibleElementsStorageTargets.length === 0}
                      onChange={(event) => setElementsSelectedMount(event.target.value)}
                    >
                      {eligibleElementsStorageTargets.map((target) => (
                        <option key={target.mount} value={target.mount}>
                          {storageTargetLabel(target)}
                        </option>
                      ))}
                    </select>
                  )}
                  {elementsStorageError && <p className="text-xs text-rose-300">{elementsStorageError}</p>}
                  {!elementsStorageLoading && !elementsStorageError && eligibleElementsStorageTargets.length === 0 && (
                    <p className="text-xs text-amber-300/80">{t('appStore.storageNoEligibleVolumes')}</p>
                  )}
                  {selectedElementsStorageTarget && (
                    <p className="break-all rounded-md border border-white/10 bg-black/20 px-3 py-2 font-mono text-xs text-fog/70">
                      {t('appStore.storageSelectedPath', { path: selectedElementsStorageTarget.suggested_path })}
                    </p>
                  )}
                  <p className="text-xs text-fog/50">{t('appStore.elementsDataDirHint')}</p>
                </div>
              ) : (
                <p className="break-all rounded-md border border-white/10 bg-black/20 px-3 py-2 font-mono text-xs text-fog/70">
                  {t('appStore.storageDefaultPath', { path: elementsDefaultDataDir })}
                </p>
              )}
            </div>

            <div className="mt-6 flex flex-wrap justify-end gap-3">
              <button
                className="btn-secondary"
                type="button"
                onClick={() => setElementsInstallOpen(false)}
              >
                {t('common.cancel')}
              </button>
              <button
                className="btn-primary"
                type="button"
                disabled={elementsUseStorageMount && !elementsSelectedMount}
                onClick={handleElementsInstallConfirm}
              >
                {t('appStore.install')}
              </button>
            </div>
          </div>
        </div>
      )}

      {peerswapRemoteOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
          <div className="w-full max-w-lg rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
            <div className="space-y-2">
              <h3 className="text-lg font-semibold">{t('appStore.peerswapRemoteTitle')}</h3>
              <p className="text-sm text-fog/60">{t('appStore.peerswapRemoteBody')}</p>
            </div>

            <div className="mt-5 grid gap-4">
              <label className="grid gap-2 text-sm">
                <span className="text-xs uppercase tracking-wide text-fog/50">{t('appStore.peerswapRemoteUrl')}</span>
                <input
                  className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass"
                  value={peerswapRemoteUrl}
                  onChange={(event) => {
                    setPeerswapRemoteUrl(event.target.value)
                    setPeerswapRemoteTested(false)
                  }}
                />
              </label>
              <div className="grid gap-4 sm:grid-cols-2">
                <label className="grid gap-2 text-sm">
                  <span className="text-xs uppercase tracking-wide text-fog/50">{t('appStore.peerswapRemoteUser')}</span>
                  <input
                    className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass"
                    value={peerswapRemoteUser}
                    onChange={(event) => {
                      setPeerswapRemoteUser(event.target.value)
                      setPeerswapRemoteTested(false)
                    }}
                  />
                </label>
                <label className="grid gap-2 text-sm">
                  <span className="text-xs uppercase tracking-wide text-fog/50">{t('appStore.peerswapRemotePassword')}</span>
                  <input
                    className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass"
                    type="password"
                    value={peerswapRemotePassword}
                    onChange={(event) => {
                      setPeerswapRemotePassword(event.target.value)
                      setPeerswapRemoteTested(false)
                    }}
                  />
                </label>
              </div>
              <p className="text-xs text-fog/50">{t('appStore.peerswapRemoteSecurity')}</p>
              {peerswapRemoteMessage && <p className="text-sm text-brass">{peerswapRemoteMessage}</p>}
            </div>

            <div className="mt-6 flex flex-wrap justify-end gap-3">
              <button
                className="btn-secondary"
                type="button"
                onClick={() => setPeerswapRemoteOpen(false)}
              >
                {t('common.cancel')}
              </button>
              <button
                className="btn-secondary"
                type="button"
                disabled={peerswapRemoteTesting || !peerswapRemoteUrl.trim() || !peerswapRemoteUser.trim() || !peerswapRemotePassword}
                onClick={handlePeerswapRemoteTest}
              >
                {peerswapRemoteTesting ? t('appStore.peerswapRemoteTesting') : t('appStore.peerswapRemoteTest')}
              </button>
              <button
                className="btn-primary"
                type="button"
                disabled={!peerswapRemoteUrl.trim() || !peerswapRemoteUser.trim() || !peerswapRemotePassword || peerswapRemoteTesting || !peerswapRemoteTested}
                onClick={handlePeerswapRemoteInstallConfirm}
              >
                {t('appStore.install')}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
