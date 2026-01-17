import { useEffect, useMemo, useRef, useState } from 'react'
import { getBitcoinLocalConfig, getBitcoinLocalStatus, updateBitcoinLocalConfig } from '../api'

type BitcoinLocalStatus = {
  installed: boolean
  status: string
  data_dir: string
  rpc_ok?: boolean
  connections?: number
  chain?: string
  blocks?: number
  headers?: number
  best_block_time?: number
  block_cadence_window_sec?: number
  block_cadence?: Array<{ start_time: number; end_time: number; count: number }>
  verification_progress?: number
  initial_block_download?: boolean
  version?: number
  subversion?: string
  pruned?: boolean
  prune_height?: number
  prune_target_size?: number
  size_on_disk?: number
}

type BitcoinLocalConfig = {
  mode: 'full' | 'pruned'
  prune_size_gb?: number
  min_prune_gb: number
  data_dir: string
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

export default function BitcoinLocal() {
  const [status, setStatus] = useState<BitcoinLocalStatus | null>(null)
  const [config, setConfig] = useState<BitcoinLocalConfig | null>(null)
  const [mode, setMode] = useState<'full' | 'pruned'>('full')
  const [pruneSizeGB, setPruneSizeGB] = useState<number>(10)
  const [applyNow, setApplyNow] = useState(true)
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [blockFlash, setBlockFlash] = useState(false)
  const lastBlockRef = useRef<number | null>(null)
  const flashTimerRef = useRef<number | null>(null)
  const rpcFailureTimesRef = useRef<number[]>([])
  const rpcLastSuccessRef = useRef<number | null>(null)
  const [rpcFailCount, setRpcFailCount] = useState(0)
  const [rpcStale, setRpcStale] = useState(false)

  const mergeStatus = (prev: BitcoinLocalStatus | null, next: BitcoinLocalStatus) => {
    if (!prev) return next
    if (!next.installed || next.status !== 'running') return next
    const hasRpcPayload =
      typeof next.connections === 'number' ||
      typeof next.blocks === 'number' ||
      typeof next.headers === 'number' ||
      typeof next.verification_progress === 'number' ||
      typeof next.initial_block_download === 'boolean' ||
      typeof next.version === 'number' ||
      typeof next.pruned === 'boolean' ||
      typeof next.prune_target_size === 'number' ||
      typeof next.size_on_disk === 'number' ||
      Boolean(next.chain) ||
      Boolean(next.subversion)
    const keepRpcSnapshot = !hasRpcPayload && next.rpc_ok === false
    return {
      ...prev,
      ...next,
      rpc_ok: keepRpcSnapshot ? prev.rpc_ok : next.rpc_ok ?? prev.rpc_ok,
      connections: keepRpcSnapshot ? prev.connections : next.connections ?? prev.connections,
      chain: keepRpcSnapshot ? prev.chain : next.chain ?? prev.chain,
      blocks: keepRpcSnapshot ? prev.blocks : next.blocks ?? prev.blocks,
      headers: keepRpcSnapshot ? prev.headers : next.headers ?? prev.headers,
      verification_progress: keepRpcSnapshot ? prev.verification_progress : next.verification_progress ?? prev.verification_progress,
      initial_block_download: keepRpcSnapshot ? prev.initial_block_download : next.initial_block_download ?? prev.initial_block_download,
      version: keepRpcSnapshot ? prev.version : next.version ?? prev.version,
      subversion: keepRpcSnapshot ? prev.subversion : next.subversion ?? prev.subversion,
      pruned: keepRpcSnapshot ? prev.pruned : next.pruned ?? prev.pruned,
      prune_height: keepRpcSnapshot ? prev.prune_height : next.prune_height ?? prev.prune_height,
      prune_target_size: keepRpcSnapshot ? prev.prune_target_size : next.prune_target_size ?? prev.prune_target_size,
      size_on_disk: keepRpcSnapshot ? prev.size_on_disk : next.size_on_disk ?? prev.size_on_disk
    }
  }

  const recordRpcFailure = () => {
    const now = Date.now()
    const times = rpcFailureTimesRef.current
    if (times.length === 0 || now - times[times.length - 1] >= 60000) {
      const next = [...times, now].slice(-5)
      rpcFailureTimesRef.current = next
      setRpcFailCount(next.length)
      setRpcStale(next.length >= 5)
    } else {
      setRpcFailCount(times.length)
    }
  }

  const recordRpcSuccess = (updateTimestamp: boolean) => {
    rpcFailureTimesRef.current = []
    setRpcFailCount(0)
    setRpcStale(false)
    if (updateTimestamp) {
      rpcLastSuccessRef.current = Date.now()
    }
  }

  const loadStatus = () => {
    getBitcoinLocalStatus()
      .then((data: BitcoinLocalStatus) => {
        const hasRpcPayload =
          typeof data.connections === 'number' ||
          typeof data.blocks === 'number' ||
          typeof data.headers === 'number' ||
          typeof data.verification_progress === 'number' ||
          typeof data.initial_block_download === 'boolean' ||
          typeof data.version === 'number' ||
          typeof data.pruned === 'boolean' ||
          typeof data.prune_target_size === 'number' ||
          typeof data.size_on_disk === 'number' ||
          Boolean(data.chain) ||
          Boolean(data.subversion)
        if (data.installed && data.status === 'running') {
          if (data.rpc_ok === false && !hasRpcPayload) {
            recordRpcFailure()
          } else {
            recordRpcSuccess(hasRpcPayload)
          }
        } else {
          recordRpcSuccess(false)
        }
        setStatus((prev) => mergeStatus(prev, data))
      })
      .catch(() => {
        if (status?.status === 'running') {
          recordRpcFailure()
        }
      })
  }

  const loadConfig = () => {
    getBitcoinLocalConfig()
      .then((data: BitcoinLocalConfig) => {
        setConfig(data)
        setMode(data.mode)
        if (data.prune_size_gb) {
          setPruneSizeGB(data.prune_size_gb)
        }
      })
      .catch(() => null)
  }

  useEffect(() => {
    loadStatus()
    loadConfig()
    const timer = setInterval(loadStatus, 6000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    return () => {
      if (flashTimerRef.current) {
        window.clearTimeout(flashTimerRef.current)
      }
    }
  }, [])

  useEffect(() => {
    if (typeof status?.blocks !== 'number') return
    if (lastBlockRef.current === null) {
      lastBlockRef.current = status.blocks
      return
    }
    if (status.blocks > lastBlockRef.current) {
      lastBlockRef.current = status.blocks
      setBlockFlash(true)
      if (flashTimerRef.current) {
        window.clearTimeout(flashTimerRef.current)
      }
      flashTimerRef.current = window.setTimeout(() => {
        setBlockFlash(false)
      }, 1200)
    }
  }, [status?.blocks])

  const progressValue = useMemo(() => {
    const raw = status?.verification_progress ?? 0
    return Math.max(0, Math.min(100, raw * 100))
  }, [status?.verification_progress])

  const progress = useMemo(() => formatPercent(status?.verification_progress), [status?.verification_progress])
  const statusClass = statusStyles[status?.status || 'unknown'] || statusStyles.unknown
  const syncing = Boolean(status?.initial_block_download)
  const ready = Boolean(status?.status === 'running' && status?.rpc_ok)
  const installed = Boolean(status?.installed)
  const blockCount = 12
  const activeBlocks = syncing ? Math.max(1, Math.round((progressValue / 100) * blockCount)) : blockCount
  const sweepDuration = syncing ? Math.max(2.5, 7.5 - progressValue / 15) : 12
  const currentPeers = status?.connections ?? 0
  const rpcStatusLabel = ready
    ? 'OK'
    : status?.status !== 'running'
      ? 'Offline'
      : rpcStale
        ? `Stale (${rpcFailCount}/5)`
        : rpcFailCount > 0
          ? `Retrying (${rpcFailCount}/5)`
          : 'Connecting...'
  const rpcBadgeClass = ready
    ? 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30 px-2 py-0.5 rounded-full text-[11px] uppercase tracking-wide'
    : 'text-fog'

  const formatAge = (timestamp?: number | null) => {
    if (!timestamp) return ''
    const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
    if (seconds < 60) return `${seconds}s`
    const minutes = Math.floor(seconds / 60)
    if (minutes < 60) return `${minutes}m`
    const hours = Math.floor(minutes / 60)
    return `${hours}h`
  }

  const formatDuration = (seconds?: number | null) => {
    if (seconds === null || seconds === undefined) return '-'
    if (seconds < 60) return `${Math.floor(seconds)}s`
    const minutes = Math.floor(seconds / 60)
    if (minutes < 60) return `${minutes}m`
    const hours = Math.floor(minutes / 60)
    const remMinutes = minutes % 60
    if (hours < 24) return remMinutes ? `${hours}h ${remMinutes}m` : `${hours}h`
    const days = Math.floor(hours / 24)
    return `${days}d ${hours % 24}h`
  }

  const cadenceBuckets = status?.block_cadence || []
  const cadenceWindowSec = status?.block_cadence_window_sec || 600
  const cadenceHours = cadenceBuckets.length > 0 ? (cadenceBuckets.length * cadenceWindowSec) / 3600 : 2
  const cadenceCounts = cadenceBuckets.length > 0 ? cadenceBuckets.map((bucket) => bucket.count) : []
  const baselineCount = Math.max(1, Math.round(cadenceWindowSec / 600))
  const maxCadence = Math.max(1, baselineCount, ...cadenceCounts)
  const baselinePercent = (baselineCount / maxCadence) * 100
  const cadenceTotal = cadenceCounts.reduce((sum, count) => sum + count, 0)
  const cadenceAvg = cadenceHours > 0 ? cadenceTotal / cadenceHours : 0
  const lastBlockTime = typeof status?.best_block_time === 'number' ? status.best_block_time * 1000 : null
  const lastBlockAgeSec = lastBlockTime ? Math.max(0, (Date.now() - lastBlockTime) / 1000) : null
  const lastBlockLabel = lastBlockTime
    ? new Date(lastBlockTime).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' })
    : '-'
  const cadenceTone = lastBlockAgeSec === null
    ? 'muted'
    : lastBlockAgeSec <= 1200
      ? 'ok'
      : lastBlockAgeSec <= 3600
        ? 'warn'
        : 'stale'
  const cadenceBadgeClass = cadenceTone === 'ok'
    ? 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
    : cadenceTone === 'warn'
      ? 'bg-amber-500/15 text-amber-200 border border-amber-400/30'
      : cadenceTone === 'stale'
        ? 'bg-rose-500/15 text-rose-200 border border-rose-400/30'
        : 'bg-white/10 text-fog/60 border border-white/10'
  const cadenceLabel = cadenceTone === 'ok' ? 'OK' : cadenceTone === 'warn' ? 'Warn' : cadenceTone === 'stale' ? 'Stale' : 'Unknown'

  const handleSave = async () => {
    setMessage('')
    setSaving(true)
    try {
      const payload = {
        mode,
        prune_size_gb: mode === 'pruned' ? pruneSizeGB : undefined,
        apply_now: applyNow
      }
      await updateBitcoinLocalConfig(payload)
      setMessage('Configuration saved.')
      loadConfig()
      loadStatus()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Failed to save configuration.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">Bitcoin Local</h2>
            <p className="text-fog/60">Manage your local Bitcoin Core node and track sync in real time.</p>
          </div>
          <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${statusClass}`}>
            {status?.status?.replace('_', ' ') || 'unknown'}
          </span>
        </div>
        {message && <p className="text-sm text-brass">{message}</p>}
      </div>

      {!installed && (
        <div className="section-card space-y-3">
          <h3 className="text-lg font-semibold">Bitcoin Core not installed</h3>
          <p className="text-fog/60">Install Bitcoin Core in the App Store to enable local monitoring.</p>
          <a className="btn-primary inline-flex items-center" href="#apps">Open App Store</a>
        </div>
      )}

      {installed && (
        <>
          <div className="grid gap-6 lg:grid-cols-2">
            <div className="section-card space-y-4">
              <div className="flex items-center justify-between">
                <h3 className="text-lg font-semibold">Sync</h3>
                <span className="text-xs text-fog/60">{syncing ? 'Syncing' : 'Status'}</span>
              </div>

              <div className="chain-track" style={{ ['--sync-progress' as any]: progressValue / 100 }}>
                <div className="chain-sweep" style={{ animationDuration: `${sweepDuration}s` }} />
                <div className="absolute inset-0 flex items-center gap-2 px-4">
                  {Array.from({ length: blockCount }).map((_, i) => {
                    const isActive = i < activeBlocks
                    const isPulse = syncing && i === Math.min(activeBlocks, blockCount - 1)
                    const isFlash = !syncing && i === blockCount - 1 && blockFlash
                    return (
                    <div
                      key={`block-${i}`}
                      className={`block-cell ${isActive ? 'block-cell--active' : ''} ${isPulse ? 'block-cell--pulse' : ''} ${isFlash ? 'block-cell--flash' : ''}`}
                      style={{ animationDelay: `${i * 0.12}s` }}
                    />
                  )})}
                </div>
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
                  <span>{ready ? 'Live blocks' : 'Blocks'}</span>
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
                  <span className={rpcBadgeClass}>{rpcStatusLabel}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span>Network</span>
                  <span className="text-fog">{status?.chain || '-'}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span>Peers</span>
                  <span className="text-fog">{currentPeers || '-'}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span>Version</span>
                  <span className="text-fog">{status?.subversion || status?.version || '-'}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span>Pruned</span>
                  <span className="text-fog">{status?.pruned ? 'Yes' : 'No'}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span>Prune target</span>
                  <span className="text-fog">{formatGB(status?.prune_target_size)}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span>Data dir</span>
                  <span className="text-fog">{status?.data_dir || config?.data_dir || '-'}</span>
                </div>
              </div>
              <div className="glow-divider" />
              {rpcStale ? (
                <p className="text-xs text-brass">
                  RPC reconnecting. Showing last captured data{rpcLastSuccessRef.current ? ` (${formatAge(rpcLastSuccessRef.current)} ago)` : ''}. Retrying every 6s.
                </p>
              ) : (
                <p className="text-xs text-fog/60">
                  {syncing ? 'The node is syncing the blockchain. This may take hours or days.' : 'Node is ready for local use.'}
                </p>
              )}
            </div>
          </div>

          <div className="grid gap-6 lg:grid-cols-2">
            <div className="section-card space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-4">
                <div>
                  <h3 className="text-lg font-semibold">Storage configuration</h3>
                  <p className="text-fog/60 text-sm">Choose full node or pruned mode to reduce disk usage.</p>
                </div>
                <div className="text-xs text-fog/50">
                  Min prune: {config?.min_prune_gb?.toFixed(2) || '0.54'} GB
                </div>
              </div>

              <div className="flex flex-wrap gap-3">
                <button
                  className={`px-4 py-2 rounded-full border ${mode === 'full' ? 'bg-glow text-ink border-transparent' : 'border-white/20 text-fog'}`}
                  onClick={() => setMode('full')}
                  type="button"
                >
                  Full node
                </button>
                <button
                  className={`px-4 py-2 rounded-full border ${mode === 'pruned' ? 'bg-glow text-ink border-transparent' : 'border-white/20 text-fog'}`}
                  onClick={() => setMode('pruned')}
                  type="button"
                >
                  Pruned
                </button>
              </div>

              {mode === 'pruned' && (
                <div className="grid gap-3 lg:grid-cols-2">
                  <label className="text-sm text-fog/70">
                    Prune size (GB)
                    <input
                      className="input-field mt-2"
                      type="number"
                      min={config?.min_prune_gb || 0.54}
                      step="1"
                      value={pruneSizeGB}
                      onChange={(e) => setPruneSizeGB(Number(e.target.value))}
                    />
                  </label>
                  <div className="text-xs text-fog/50">
                    <p>Pruned mode keeps only part of the blockchain to save disk space.</p>
                    <p>Minimum value accepted by Bitcoin Core: {config?.min_prune_gb?.toFixed(2) || '0.54'} GB.</p>
                  </div>
                </div>
              )}

              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input
                  type="checkbox"
                  className="accent-teal-300"
                  checked={applyNow}
                  onChange={(e) => setApplyNow(e.target.checked)}
                />
                Apply now (restarts bitcoind)
              </label>

              <div className="flex flex-wrap items-center gap-3">
                <button className="btn-primary" onClick={handleSave} disabled={saving}>
                  {saving ? 'Saving...' : 'Save configuration'}
                </button>
                <span className="text-xs text-fog/50">
                  Prune changes require a restart to take effect.
                </span>
              </div>
            </div>

            <div className="section-card space-y-4">
              <div className="flex items-start justify-between gap-4">
                <div>
                  <h3 className="text-lg font-semibold">Block cadence</h3>
                  <p className="text-xs text-fog/60">Last {cadenceHours.toFixed(1)} hours</p>
                </div>
                <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${cadenceBadgeClass}`}>
                  {cadenceLabel}
                </span>
              </div>
              <div className="grid gap-2 text-sm text-fog/70">
                <div className="flex items-center justify-between">
                  <span>Last block seen</span>
                  <span className="text-fog">{formatDuration(lastBlockAgeSec)} ago</span>
                </div>
                <div className="flex items-center justify-between">
                  <span>Network block time</span>
                  <span className="text-fog">{lastBlockLabel}</span>
                </div>
              </div>
              <div className="block-cadence-chart">
                <div className="block-cadence-baseline" style={{ bottom: `${baselinePercent}%` }} />
                {(cadenceBuckets.length > 0 ? cadenceBuckets : Array.from({ length: 12 }).map(() => ({ start_time: 0, end_time: 0, count: 0 }))).map((bucket, idx) => {
                  const height = Math.max(8, Math.round((bucket.count / maxCadence) * 100))
                  const startLabel = bucket.start_time
                    ? new Date(bucket.start_time * 1000).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' })
                    : ''
                  const endLabel = bucket.end_time
                    ? new Date(bucket.end_time * 1000).toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' })
                    : ''
                  const title = startLabel && endLabel
                    ? `${startLabel}–${endLabel}: ${bucket.count} blocks`
                    : `${bucket.count} blocks`
                  return (
                    <div className="block-cadence-bar" key={`cadence-${idx}`} title={title}>
                      <div className="block-cadence-fill" style={{ height: `${height}%` }} />
                    </div>
                  )
                })}
              </div>
              <div className="flex items-center justify-between text-xs text-fog/50">
                <span>{cadenceTotal} blocks / {cadenceHours.toFixed(1)}h</span>
                <span>avg {cadenceAvg.toFixed(1)} blocks/h</span>
              </div>
            </div>
          </div>
        </>
      )}
    </section>
  )
}
