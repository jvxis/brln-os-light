import { useEffect, useMemo, useState } from 'react'
import { getElementsMainchain, getElementsStatus, setElementsMainchain } from '../api'

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

export default function Elements() {
  const [status, setStatus] = useState<ElementsStatus | null>(null)
  const [message, setMessage] = useState('')
  const [mainchain, setMainchain] = useState<ElementsMainchain | null>(null)
  const [mainchainMessage, setMainchainMessage] = useState('')
  const [mainchainBusy, setMainchainBusy] = useState(false)

  const loadStatus = () => {
    getElementsStatus()
      .then((data: ElementsStatus) => {
        setStatus(data)
        setMessage('')
      })
      .catch((err) => {
        setMessage(err instanceof Error ? err.message : 'Failed to load Elements status.')
      })
    getElementsMainchain()
      .then((data: ElementsMainchain) => {
        setMainchain(data)
        setMainchainMessage('')
      })
      .catch((err) => {
        setMainchainMessage(err instanceof Error ? err.message : 'Failed to load Elements mainchain.')
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
  const mainchainSource = mainchain?.source || status?.mainchain_source || 'remote'
  const mainchainSourceLabel = mainchainSource === 'local' ? 'Local' : 'Remote'
  const mainchainHost = mainchain?.rpchost || status?.mainchain_rpchost || ''
  const mainchainPort = mainchain?.rpcport || status?.mainchain_rpcport || 0
  const mainchainRPC = mainchainHost ? `${mainchainHost}${mainchainPort ? `:${mainchainPort}` : ''}` : '-'
  const localReady = Boolean(mainchain?.local_ready)
  const canToggleMainchain = mainchainSource === 'local' || localReady

  const handleToggleMainchain = async () => {
    if (!mainchain || mainchainBusy || !canToggleMainchain) return
    const next = mainchain.source === 'remote' ? 'local' : 'remote'
    setMainchainBusy(true)
    setMainchainMessage(`Switching to ${next} Bitcoin...`)
    try {
      await setElementsMainchain({ source: next })
      setMainchainMessage(`Elements is now using ${next} Bitcoin. Restarting service...`)
      const updated = await getElementsMainchain()
      setMainchain(updated)
    } catch (err) {
      setMainchainMessage(err instanceof Error ? err.message : 'Failed to switch mainchain source.')
    } finally {
      setMainchainBusy(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">Elements</h2>
            <p className="text-fog/60">Liquid mainnet sync and node health.</p>
            <p className="text-xs text-fog/50 mt-2">CLI: use the losop user to manage Elements.</p>
          </div>
          <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${statusClass}`}>
            {status?.status?.replace('_', ' ') || 'unknown'}
          </span>
        </div>
        {message && <p className="text-sm text-brass">{message}</p>}
      </div>

      {!installed && (
        <div className="section-card space-y-3">
          <h3 className="text-lg font-semibold">Elements not installed</h3>
          <p className="text-fog/60">Install Elements in the App Store to enable local monitoring.</p>
          <a className="btn-primary inline-flex items-center" href="#apps">Open App Store</a>
        </div>
      )}

      {installed && (
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="section-card space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-semibold">Sync</h3>
              <span className="text-xs text-fog/60">{syncing ? 'Syncing' : 'Status'}</span>
            </div>

            <div className="space-y-2">
              <div className="flex items-center justify-between text-sm">
                <span className="text-fog/60">{syncing ? 'Downloading blocks' : 'Verification progress'}</span>
                <span className="font-semibold text-fog">{progress}%</span>
              </div>
              <div className="h-3 rounded-full bg-white/10 overflow-hidden">
                <div className="h-full bg-glow transition-all" style={{ width: `${progress}%` }} />
              </div>
            </div>

            <div className="grid gap-3 text-sm text-fog/70">
              <div className="flex items-center justify-between">
                <span>Blocks</span>
                <span className="text-fog">{status?.blocks?.toLocaleString() || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>Headers</span>
                <span className="text-fog">{status?.headers?.toLocaleString() || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>Disk usage</span>
                <span className="text-fog">{formatGB(status?.size_on_disk)}</span>
              </div>
            </div>
          </div>

          <div className="section-card space-y-4">
            <h3 className="text-lg font-semibold">Node status</h3>
            <div className="grid gap-3 text-sm text-fog/70">
              <div className="flex items-center justify-between">
                <span>RPC status</span>
                <span className={rpcReady ? 'text-emerald-200' : 'text-fog'}>{rpcReady ? 'OK' : 'Offline'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>Network</span>
                <span className="text-fog">{status?.chain || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>Peers</span>
                <span className="text-fog">{status?.peers ?? '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>Version</span>
                <span className="text-fog">{status?.subversion || status?.version || '-'}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>Mainchain source</span>
                <span className="text-fog">{mainchainSourceLabel}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>Mainchain RPC</span>
                <span className="text-fog">{mainchainRPC}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>Data dir</span>
                <span className="text-fog">{status?.data_dir || '-'}</span>
              </div>
            </div>
            <div className="glow-divider" />
            <div className="flex items-center justify-between gap-4">
              <div>
                <p className="text-xs text-fog/60">
                  {syncing ? 'Syncing Liquid blocks. This may take hours or days.' : 'Node is ready for Liquid services.'}
                </p>
                {!localReady && mainchainSource === 'remote' && (
                  <p className="text-xs text-fog/50 mt-2">Local Bitcoin must be fully synced to switch.</p>
                )}
              </div>
              <button
                className={`relative flex h-9 w-32 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${mainchainBusy || !canToggleMainchain ? 'opacity-70' : 'hover:border-white/30'}`}
                onClick={handleToggleMainchain}
                type="button"
                disabled={mainchainBusy || !canToggleMainchain}
                aria-label="Toggle mainchain source"
              >
                <span
                  className={`absolute top-1 h-7 w-14 rounded-full bg-glow shadow transition-all ${mainchainSource === 'local' ? 'left-[68px]' : 'left-[6px]'}`}
                />
                <span className={`relative z-10 flex-1 text-center text-xs ${mainchainSource === 'remote' ? 'text-ink' : 'text-fog/60'}`}>Remote</span>
                <span className={`relative z-10 flex-1 text-center text-xs ${mainchainSource === 'local' ? 'text-ink' : 'text-fog/60'}`}>Local</span>
              </button>
            </div>
            {mainchainMessage && <p className="text-sm text-brass">{mainchainMessage}</p>}
          </div>
        </div>
      )}
    </section>
  )
}
