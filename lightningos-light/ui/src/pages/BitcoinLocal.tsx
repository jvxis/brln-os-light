import { useEffect, useMemo, useState } from 'react'
import { getBitcoinLocalConfig, getBitcoinLocalStatus, updateBitcoinLocalConfig } from '../api'

type BitcoinLocalStatus = {
  installed: boolean
  status: string
  data_dir: string
  rpc_ok?: boolean
  chain?: string
  blocks?: number
  headers?: number
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

  const loadStatus = () => {
    getBitcoinLocalStatus()
      .then((data: BitcoinLocalStatus) => setStatus(data))
      .catch(() => null)
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

  const progress = useMemo(() => formatPercent(status?.verification_progress), [status?.verification_progress])
  const statusClass = statusStyles[status?.status || 'unknown'] || statusStyles.unknown
  const syncing = Boolean(status?.initial_block_download)
  const ready = Boolean(status?.status === 'running' && status?.rpc_ok)
  const installed = Boolean(status?.installed)

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
      setMessage('Configuracao salva.')
      loadConfig()
      loadStatus()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Falha ao salvar configuracao.')
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
            <p className="text-fog/60">Controle o Bitcoin Core local e acompanhe a sincronizacao em tempo real.</p>
          </div>
          <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${statusClass}`}>
            {status?.status?.replace('_', ' ') || 'unknown'}
          </span>
        </div>
        {message && <p className="text-sm text-brass">{message}</p>}
      </div>

      {!installed && (
        <div className="section-card space-y-3">
          <h3 className="text-lg font-semibold">Bitcoin Core nao instalado</h3>
          <p className="text-fog/60">Instale o Bitcoin Core na App Store para habilitar o monitoramento local.</p>
          <a className="btn-primary inline-flex items-center" href="#apps">Abrir App Store</a>
        </div>
      )}

      {installed && (
        <>
          <div className="grid gap-6 lg:grid-cols-2">
            <div className="section-card space-y-4">
              <div className="flex items-center justify-between">
                <h3 className="text-lg font-semibold">Sincronizacao</h3>
                <span className="text-xs text-fog/60">{syncing ? 'Sincronizando' : 'Status'}</span>
              </div>

              <div className="chain-track">
                <div className="chain-sweep" />
                <div className="absolute inset-0 flex items-center gap-2 px-4">
                  {Array.from({ length: 10 }).map((_, i) => (
                    <div
                      key={`block-${i}`}
                      className="block-pulse h-3 w-5 rounded-md bg-white/10 border border-white/10"
                      style={{ animationDelay: `${i * 0.2}s` }}
                    />
                  ))}
                </div>
              </div>

              <div className="space-y-2">
                <div className="flex items-center justify-between text-sm">
                  <span className="text-fog/60">{syncing ? 'Baixando blocos' : 'Progresso de verificacao'}</span>
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
                  <span>Espaco em disco</span>
                  <span className="text-fog">{formatGB(status?.size_on_disk)}</span>
                </div>
              </div>
            </div>

            <div className="section-card space-y-4">
              <h3 className="text-lg font-semibold">Node status</h3>
              <div className="grid gap-3 text-sm text-fog/70">
                <div className="flex items-center justify-between">
                  <span>RPC status</span>
                  <span className="text-fog">{ready ? 'OK' : 'Unavailable'}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span>Network</span>
                  <span className="text-fog">{status?.chain || '-'}</span>
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
              <p className="text-xs text-fog/60">
                {syncing ? 'O node esta sincronizando a blockchain. Isso pode levar horas ou dias.' : 'Node pronto para uso local.'}
              </p>
            </div>
          </div>

          <div className="section-card space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-4">
              <div>
                <h3 className="text-lg font-semibold">Configuracao de armazenamento</h3>
                <p className="text-fog/60 text-sm">Escolha entre full node ou pruned para reduzir espaco em disco.</p>
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
                  <p>Pruned mode mantem apenas parte da blockchain para economizar disco.</p>
                  <p>Valor minimo aceito pelo Bitcoin Core: {config?.min_prune_gb?.toFixed(2) || '0.54'} GB.</p>
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
              Aplicar agora (reinicia o bitcoind)
            </label>

            <div className="flex flex-wrap items-center gap-3">
              <button className="btn-primary" onClick={handleSave} disabled={saving}>
                {saving ? 'Salvando...' : 'Salvar configuracao'}
              </button>
              <span className="text-xs text-fog/50">
                Mudancas de prune exigem restart para entrar em vigor.
              </span>
            </div>
          </div>
        </>
      )}
    </section>
  )
}
