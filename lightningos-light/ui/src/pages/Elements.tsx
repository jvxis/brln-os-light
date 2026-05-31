import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getElementsMainchain, getElementsStatus, getPeerswapElementsSource, setElementsMainchain, setPeerswapElementsSource, testPeerswapElementsSource } from '../api'
import { getLocale } from '../i18n'

type ElementsStatus = {
  installed: boolean
  status: string
  data_dir: string
  mainchain_source?: string
  mainchain_rpchost?: string
  mainchain_rpcport?: number
  rpc_ok?: boolean
  peers?: number
  chain?: string
  blocks?: number
  headers?: number
  verification_progress?: number
  initial_block_download?: boolean
  version?: number
  subversion?: string
  size_on_disk?: number
}

type ElementsMainchain = {
  source: 'remote' | 'local'
  rpchost?: string
  rpcport?: number
  local_ready?: boolean
  local_status?: string
}

type PeerswapElementsSource = {
  configured: boolean
  mode: 'remote' | 'local'
  url?: string
  user?: string
  wallet?: string
  local_ready?: boolean
  local_status?: string
  installed?: boolean
  running?: boolean
}

const statusStyles: Record<string, string> = {
  running: 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30',
  stopped: 'bg-amber-500/15 text-amber-200 border border-amber-400/30',
  unknown: 'bg-rose-500/15 text-rose-200 border border-rose-400/30',
  not_installed: 'bg-white/10 text-fog/60 border border-white/10'
}

const formatGB = (value?: number) => {
  if (!value || value <= 0) return '-'
  const gb = value / (1024 * 1024 * 1024)
  return `${gb.toFixed(1)} GB`
}

const formatPercent = (value?: number) => {
  if (value === undefined || value === null) return '0.00'
  return Math.min(100, value * 100).toFixed(2)
}

const peerswapDefaultRemoteUrl = 'http://elements.br-ln.com:8086'

export default function Elements() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [status, setStatus] = useState<ElementsStatus | null>(null)
  const [message, setMessage] = useState('')
  const [mainchain, setMainchain] = useState<ElementsMainchain | null>(null)
  const [mainchainMessage, setMainchainMessage] = useState('')
  const [mainchainBusy, setMainchainBusy] = useState(false)
  const [peerswapSource, setPeerswapSource] = useState<PeerswapElementsSource | null>(null)
  const [peerswapSourceMessage, setPeerswapSourceMessage] = useState('')
  const [peerswapSourceBusy, setPeerswapSourceBusy] = useState(false)
  const [peerswapRemoteOpen, setPeerswapRemoteOpen] = useState(false)
  const [peerswapRemoteUrl, setPeerswapRemoteUrl] = useState(peerswapDefaultRemoteUrl)
  const [peerswapRemoteUser, setPeerswapRemoteUser] = useState('')
  const [peerswapRemotePassword, setPeerswapRemotePassword] = useState('')

  const loadStatus = () => {
    getElementsStatus()
      .then((data: ElementsStatus) => {
        setStatus(data)
        setMessage('')
      })
      .catch((err) => {
        setMessage(err instanceof Error ? err.message : t('elements.loadStatusFailed'))
      })
    getElementsMainchain()
      .then((data: ElementsMainchain) => {
        setMainchain(data)
        setMainchainMessage('')
      })
      .catch((err) => {
        setMainchainMessage(err instanceof Error ? err.message : t('elements.loadMainchainFailed'))
      })
    getPeerswapElementsSource()
      .then((data: PeerswapElementsSource) => {
        setPeerswapSource(data)
        setPeerswapSourceMessage('')
      })
      .catch((err) => {
        setPeerswapSourceMessage(err instanceof Error ? err.message : t('elements.peerswapSourceLoadFailed'))
      })
  }

  useEffect(() => {
    loadStatus()
    const timer = setInterval(loadStatus, 6000)
    return () => clearInterval(timer)
  }, [])

  const progressValue = useMemo(() => {
    const raw = status?.verification_progress ?? 0
    return Math.max(0, Math.min(100, raw * 100))
  }, [status?.verification_progress])

  const progress = useMemo(() => formatPercent(status?.verification_progress), [status?.verification_progress])
  const syncing = Boolean(status?.initial_block_download)
  const installed = Boolean(status?.installed)
  const rpcReady = Boolean(status?.status === 'running' && status?.rpc_ok)
  const statusClass = statusStyles[status?.status || 'unknown'] || statusStyles.unknown
  const statusLabel = (value?: string) => {
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
  const mainchainSource = mainchain?.source || status?.mainchain_source || 'remote'
  const mainchainSourceLabel = mainchainSource === 'local' ? t('common.local') : t('common.remote')
  const mainchainHost = mainchain?.rpchost || status?.mainchain_rpchost || ''
  const mainchainPort = mainchain?.rpcport || status?.mainchain_rpcport || 0
  const mainchainRPC = mainchainHost ? `${mainchainHost}${mainchainPort ? `:${mainchainPort}` : ''}` : '-'
  const localReady = Boolean(mainchain?.local_ready)
  const canToggleMainchain = mainchainSource === 'local' || localReady
  const peerswapSourceMode = peerswapSource?.mode || 'local'
  const peerswapSourceLabel = peerswapSourceMode === 'remote' ? t('common.remote') : t('common.local')
  const peerswapSourceDetail = peerswapSourceMode === 'remote'
    ? `${peerswapSource?.url || '-'}${peerswapSource?.user ? ` · ${peerswapSource.user}` : ''}`
    : t('elements.peerswapLocalSource')
  const peerswapLocalReady = Boolean(peerswapSource?.local_ready)
  const peerswapCanUseLocal = peerswapSourceMode === 'local' || peerswapLocalReady

  const handleToggleMainchain = async () => {
    if (!mainchain || mainchainBusy || !canToggleMainchain) return
    const next = mainchain.source === 'remote' ? 'local' : 'remote'
    const targetLabel = next === 'local' ? t('common.local') : t('common.remote')
    setMainchainBusy(true)
    setMainchainMessage(t('elements.switchingToBitcoin', { target: targetLabel }))
    try {
      await setElementsMainchain({ source: next })
      setMainchainMessage(t('elements.switchedBitcoin', { target: targetLabel }))
      const updated = await getElementsMainchain()
      setMainchain(updated)
    } catch (err) {
      setMainchainMessage(err instanceof Error ? err.message : t('elements.switchFailed'))
    } finally {
      setMainchainBusy(false)
    }
  }

  const openPeerswapRemoteConfig = () => {
    setPeerswapRemoteUrl(peerswapSource?.mode === 'remote' && peerswapSource.url ? peerswapSource.url : peerswapDefaultRemoteUrl)
    setPeerswapRemoteUser(peerswapSource?.mode === 'remote' && peerswapSource.user ? peerswapSource.user : '')
    setPeerswapRemotePassword('')
    setPeerswapSourceMessage('')
    setPeerswapRemoteOpen(true)
  }

  const peerswapRemotePayload = () => ({
    mode: 'remote' as const,
    url: peerswapRemoteUrl.trim(),
    user: peerswapRemoteUser.trim(),
    password: peerswapRemotePassword
  })

  const reloadPeerswapSource = async () => {
    const updated = await getPeerswapElementsSource()
    setPeerswapSource(updated)
  }

  const handlePeerswapTestCurrent = async () => {
    setPeerswapSourceBusy(true)
    setPeerswapSourceMessage(t('elements.peerswapTesting'))
    try {
      const res = await testPeerswapElementsSource()
      const chain = res?.chain ? ` (${res.chain})` : ''
      setPeerswapSourceMessage(`${t('elements.peerswapTestOk')}${chain}`)
    } catch (err) {
      setPeerswapSourceMessage(err instanceof Error ? err.message : t('elements.peerswapTestFailed'))
    } finally {
      setPeerswapSourceBusy(false)
    }
  }

  const handlePeerswapUseLocal = async () => {
    setPeerswapSourceBusy(true)
    setPeerswapSourceMessage(t('elements.peerswapSwitchingLocal'))
    try {
      await setPeerswapElementsSource({ mode: 'local' })
      await reloadPeerswapSource()
      setPeerswapSourceMessage(t('elements.peerswapSwitchedLocal'))
    } catch (err) {
      setPeerswapSourceMessage(err instanceof Error ? err.message : t('elements.peerswapSwitchFailed'))
    } finally {
      setPeerswapSourceBusy(false)
    }
  }

  const handlePeerswapSaveRemote = async () => {
    setPeerswapSourceBusy(true)
    setPeerswapSourceMessage(t('elements.peerswapSwitchingRemote'))
    try {
      await setPeerswapElementsSource(peerswapRemotePayload())
      setPeerswapRemoteOpen(false)
      await reloadPeerswapSource()
      setPeerswapSourceMessage(t('elements.peerswapSwitchedRemote'))
    } catch (err) {
      setPeerswapSourceMessage(err instanceof Error ? err.message : t('elements.peerswapSwitchFailed'))
    } finally {
      setPeerswapSourceBusy(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">{t('elements.title')}</h2>
            <p className="text-fog/60">{t('elements.subtitle')}</p>
            <p className="text-xs text-fog/50 mt-2">{t('elements.cliHint')}</p>
          </div>
          <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${statusClass}`}>
            {statusLabel(status?.status)}
          </span>
        </div>
        {message && <p className="text-sm text-brass">{message}</p>}
      </div>

      {!installed && (
        <div className="section-card space-y-3">
          <h3 className="text-lg font-semibold">{t('elements.notInstalledTitle')}</h3>
          <p className="text-fog/60">{t('elements.notInstalledBody')}</p>
          <a className="btn-primary inline-flex items-center" href="#apps">{t('elements.openAppStore')}</a>
        </div>
      )}

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('elements.peerswapSourceTitle')}</h3>
            <p className="text-sm text-fog/60">{t('elements.peerswapSourceBody')}</p>
          </div>
          <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-xs uppercase tracking-wide text-fog/70">
            {peerswapSourceLabel}
          </span>
        </div>

        <div className="grid gap-3 text-sm text-fog/70 md:grid-cols-2">
          <div className="flex items-center justify-between gap-4">
            <span>{t('elements.peerswapActiveSource')}</span>
            <span className="break-all text-right text-fog">{peerswapSourceDetail}</span>
          </div>
          <div className="flex items-center justify-between gap-4">
            <span>{t('elements.peerswapLocalStatus')}</span>
            <span className="text-fog">{statusLabel(peerswapSource?.local_status)}</span>
          </div>
          <div className="flex items-center justify-between gap-4">
            <span>{t('elements.peerswapInstalled')}</span>
            <span className="text-fog">{peerswapSource?.installed ? t('common.yes') : t('common.no')}</span>
          </div>
          <div className="flex items-center justify-between gap-4">
            <span>{t('elements.peerswapService')}</span>
            <span className="text-fog">{peerswapSource?.running ? t('common.running') : t('common.stopped')}</span>
          </div>
        </div>

        {peerswapSourceMode === 'remote' && peerswapLocalReady && (
          <p className="text-xs text-amber-300/80">{t('elements.peerswapRemoteWithLocalReady')}</p>
        )}
        {peerswapSourceMode === 'local' && !peerswapLocalReady && (
          <p className="text-xs text-amber-300/80">{t('elements.peerswapLocalMissing')}</p>
        )}

        <div className="flex flex-wrap items-center gap-3">
          <button className="btn-secondary" type="button" disabled={peerswapSourceBusy} onClick={handlePeerswapTestCurrent}>
            {peerswapSourceBusy ? t('elements.peerswapTesting') : t('elements.peerswapTestCurrent')}
          </button>
          <button className="btn-secondary" type="button" disabled={peerswapSourceBusy || !peerswapCanUseLocal} onClick={handlePeerswapUseLocal}>
            {t('elements.peerswapUseLocal')}
          </button>
          <button className="btn-primary" type="button" disabled={peerswapSourceBusy} onClick={openPeerswapRemoteConfig}>
            {t('elements.peerswapConfigureRemote')}
          </button>
        </div>
        {peerswapSourceMessage && <p className="text-sm text-brass">{peerswapSourceMessage}</p>}
      </div>

      {installed && (
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="section-card space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-semibold">{t('elements.sync')}</h3>
              <span className="text-xs text-fog/60">{syncing ? t('elements.syncing') : t('common.status')}</span>
            </div>

            <div className="space-y-2">
              <div className="flex items-center justify-between text-sm">
                <span className="text-fog/60">{syncing ? t('elements.downloadingBlocks') : t('elements.verificationProgress')}</span>
                <span className="font-semibold text-fog">{progress}%</span>
              </div>
              <div className="h-3 rounded-full bg-white/10 overflow-hidden">
                <div className="h-full bg-glow transition-all" style={{ width: `${progress}%` }} />
              </div>
            </div>

            <div className="grid gap-3 text-sm text-fog/70">
              <div className="flex items-center justify-between">
                <span>{t('elements.blocks')}</span>
                <span className="text-fog">{status?.blocks?.toLocaleString(locale) || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('elements.headers')}</span>
                <span className="text-fog">{status?.headers?.toLocaleString(locale) || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('elements.diskUsage')}</span>
                <span className="text-fog">{formatGB(status?.size_on_disk)}</span>
              </div>
            </div>
          </div>

          <div className="section-card space-y-4">
            <h3 className="text-lg font-semibold">{t('elements.nodeStatus')}</h3>
            <div className="grid gap-3 text-sm text-fog/70">
              <div className="flex items-center justify-between">
                <span>{t('elements.rpcStatus')}</span>
                <span className={rpcReady ? 'text-emerald-200' : 'text-fog'}>{rpcReady ? t('common.ok') : t('common.offline')}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('elements.network')}</span>
                <span className="text-fog">{status?.chain || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('elements.peers')}</span>
                <span className="text-fog">{status?.peers ?? '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('elements.version')}</span>
                <span className="text-fog">{status?.subversion || status?.version || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('elements.mainchainSource')}</span>
                <span className="text-fog">{mainchainSourceLabel}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('elements.mainchainRpc')}</span>
                <span className="text-fog">{mainchainRPC}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('elements.dataDir')}</span>
                <span className="text-fog">{status?.data_dir || '-'}</span>
              </div>
            </div>
            <div className="glow-divider" />
            <div className="flex items-center justify-between gap-4">
              <div>
                <p className="text-xs text-fog/60">
                  {syncing ? t('elements.syncingNote') : t('elements.readyNote')}
                </p>
                {!localReady && mainchainSource === 'remote' && (
                  <p className="text-xs text-fog/50 mt-2">{t('elements.localBitcoinRequired')}</p>
                )}
              </div>
              <button
                className={`relative flex h-9 w-32 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${mainchainBusy || !canToggleMainchain ? 'opacity-70' : 'hover:border-white/30'}`}
                onClick={handleToggleMainchain}
                type="button"
                disabled={mainchainBusy || !canToggleMainchain}
                aria-label={t('elements.toggleMainchain')}
              >
                <span
                  className={`absolute top-1 h-7 w-14 rounded-full bg-glow shadow transition-all ${mainchainSource === 'local' ? 'left-[68px]' : 'left-[6px]'}`}
                />
                <span className={`relative z-10 flex-1 text-center text-xs ${mainchainSource === 'remote' ? 'text-ink' : 'text-fog/60'}`}>{t('common.remote')}</span>
                <span className={`relative z-10 flex-1 text-center text-xs ${mainchainSource === 'local' ? 'text-ink' : 'text-fog/60'}`}>{t('common.local')}</span>
              </button>
            </div>
            {mainchainMessage && <p className="text-sm text-brass">{mainchainMessage}</p>}
          </div>
        </div>
      )}

      {peerswapRemoteOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4">
          <div className="w-full max-w-lg rounded-lg border border-white/10 bg-ink p-5 shadow-xl">
            <div className="space-y-2">
              <h3 className="text-lg font-semibold">{t('elements.peerswapRemoteTitle')}</h3>
              <p className="text-sm text-fog/60">{t('elements.peerswapRemoteBody')}</p>
            </div>

            <div className="mt-5 grid gap-4">
              <label className="grid gap-2 text-sm">
                <span className="text-xs uppercase tracking-wide text-fog/50">{t('elements.peerswapRemoteUrl')}</span>
                <input
                  className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass"
                  value={peerswapRemoteUrl}
                  onChange={(event) => setPeerswapRemoteUrl(event.target.value)}
                />
              </label>
              <div className="grid gap-4 sm:grid-cols-2">
                <label className="grid gap-2 text-sm">
                  <span className="text-xs uppercase tracking-wide text-fog/50">{t('elements.peerswapRemoteUser')}</span>
                  <input
                    className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass"
                    value={peerswapRemoteUser}
                    onChange={(event) => setPeerswapRemoteUser(event.target.value)}
                  />
                </label>
                <label className="grid gap-2 text-sm">
                  <span className="text-xs uppercase tracking-wide text-fog/50">{t('elements.peerswapRemotePassword')}</span>
                  <input
                    className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass"
                    type="password"
                    value={peerswapRemotePassword}
                    onChange={(event) => setPeerswapRemotePassword(event.target.value)}
                  />
                </label>
              </div>
              <p className="text-xs text-fog/50">{t('elements.peerswapRemoteSecurity')}</p>
            </div>

            <div className="mt-6 flex flex-wrap justify-end gap-3">
              <button className="btn-secondary" type="button" onClick={() => setPeerswapRemoteOpen(false)}>
                {t('common.cancel')}
              </button>
              <button
                className="btn-primary"
                type="button"
                disabled={peerswapSourceBusy || !peerswapRemoteUrl.trim() || !peerswapRemoteUser.trim() || !peerswapRemotePassword}
                onClick={handlePeerswapSaveRemote}
              >
                {peerswapSourceBusy ? t('elements.peerswapSaving') : t('elements.peerswapSaveRemote')}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
