import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getCpuMinerStatus, type CpuMinerStatus } from '../api'

function formatHashrate(hs: number): string {
  if (!hs || hs <= 0) return '0 H/s'
  const units = ['H/s', 'kH/s', 'MH/s', 'GH/s', 'TH/s']
  let value = hs
  let i = 0
  while (value >= 1000 && i < units.length - 1) {
    value /= 1000
    i += 1
  }
  const decimals = value >= 100 ? 0 : value >= 10 ? 1 : 2
  return `${value.toFixed(decimals)} ${units[i]}`
}

function formatDifficulty(d: number): string {
  if (!d || d <= 0) return '—'
  const units = ['', 'K', 'M', 'G', 'T', 'P']
  let value = d
  let i = 0
  while (value >= 1000 && i < units.length - 1) {
    value /= 1000
    i += 1
  }
  const decimals = value >= 100 ? 0 : value >= 10 ? 1 : 2
  return `${value.toFixed(decimals)}${units[i]}`
}

function shortenAddress(addr: string): string {
  if (!addr || addr.length <= 18) return addr
  return `${addr.slice(0, 10)}…${addr.slice(-6)}`
}

type Props = {
  running: boolean
}

export default function CpuMinerStats({ running }: Props) {
  const { t } = useTranslation()
  const [status, setStatus] = useState<CpuMinerStatus | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!running) {
      setStatus(null)
      return
    }
    let cancelled = false
    const tick = () => {
      getCpuMinerStatus()
        .then((data) => {
          if (!cancelled) setStatus(data)
        })
        .catch(() => {
          if (!cancelled) setStatus(null)
        })
    }
    tick()
    const handle = window.setInterval(tick, 3_000)
    return () => {
      cancelled = true
      window.clearInterval(handle)
    }
  }, [running])

  if (!running || !status) return null

  const threads = status.threads > 0 ? status.threads : 1
  const cpuCeiling = threads * 100
  const cpuFill = Math.min(100, Math.max(0, (status.cpu_percent / cpuCeiling) * 100))
  const warmingUp = status.hashrate_hs <= 0

  const handleCopy = async () => {
    if (!status.address) return
    try {
      await navigator.clipboard.writeText(status.address)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      // Clipboard may be unavailable; ignore silently.
    }
  }

  return (
    <div className="mt-1 rounded-2xl border border-brass/20 bg-gradient-to-br from-brass/[0.07] via-transparent to-transparent p-4 space-y-4">
      {/* Hero: live hashrate + best lottery ticket */}
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="rounded-xl bg-ink/40 border border-white/5 p-3">
          <div className="flex items-center gap-2 text-[11px] uppercase tracking-wide text-fog/50">
            <span className={`h-2 w-2 rounded-full ${warmingUp ? 'bg-amber-400' : 'bg-emerald-400 animate-pulse'}`} />
            {t('appStore.cpuMinerHashrate')}
          </div>
          <div className="mt-1 text-2xl font-semibold text-brass tabular-nums">
            {warmingUp ? t('appStore.cpuMinerConnecting') : formatHashrate(status.hashrate_hs)}
          </div>
        </div>
        <div className="rounded-xl bg-ink/40 border border-white/5 p-3">
          <div className="text-[11px] uppercase tracking-wide text-fog/50">{t('appStore.cpuMinerBestTicket')}</div>
          <div className="mt-1 text-2xl font-semibold text-fog tabular-nums">{formatDifficulty(status.best_difficulty)}</div>
          <div className="text-[11px] text-fog/40">{t('appStore.cpuMinerBestTicketHint')}</div>
        </div>
      </div>

      {/* Secondary stats */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <div className="space-y-0.5">
          <div className="text-[11px] uppercase tracking-wide text-fog/50">{t('appStore.cpuMinerShares')}</div>
          <div className="text-sm font-medium tabular-nums">
            <span className="text-emerald-300">{status.shares_accepted.toLocaleString()}</span>
            <span className="text-fog/30"> / </span>
            <span className="text-rose-300">{status.shares_rejected.toLocaleString()}</span>
          </div>
          <div className="text-[10px] text-fog/40">{t('appStore.cpuMinerSharesHint')}</div>
        </div>
        <div className="space-y-0.5">
          <div className="text-[11px] uppercase tracking-wide text-fog/50">{t('appStore.cpuMinerPoolHashrate')}</div>
          <div className="text-sm font-medium tabular-nums text-fog">
            {status.pool_hashrate_hs > 0 ? formatHashrate(status.pool_hashrate_hs) : '—'}
          </div>
        </div>
        <div className="col-span-2 space-y-1 sm:col-span-1">
          <div className="flex items-center justify-between text-[11px] uppercase tracking-wide text-fog/50">
            <span>{t('appStore.cpuMinerCpuUsage')}</span>
            <span className="tabular-nums text-fog/70">{status.cpu_percent.toFixed(0)}%</span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-white/5">
            <div
              className="h-full rounded-full bg-gradient-to-r from-brass/70 to-brass transition-all duration-500"
              style={{ width: `${cpuFill}%` }}
            />
          </div>
          <div className="text-[10px] text-fog/40">{t('appStore.cpuMinerThreads', { count: threads })}</div>
        </div>
      </div>

      {/* Reward address */}
      {status.address && (
        <div className="rounded-xl bg-ink/40 border border-white/5 p-3 space-y-1">
          <div className="flex items-center justify-between gap-2">
            <span className="text-[11px] uppercase tracking-wide text-fog/50">{t('appStore.cpuMinerAddress')}</span>
            <button
              className="flex items-center gap-1 text-[11px] text-fog/50 hover:text-fog"
              onClick={handleCopy}
              title={t('appStore.cpuMinerCopyAddress')}
              aria-label={t('appStore.cpuMinerCopyAddress')}
            >
              {copied ? (
                <span className="text-emerald-300">{t('common.copied')}</span>
              ) : (
                <svg viewBox="0 0 24 24" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.6">
                  <rect x="9" y="9" width="11" height="11" rx="2" />
                  <rect x="4" y="4" width="11" height="11" rx="2" />
                </svg>
              )}
            </button>
          </div>
          <div className="font-mono text-[12px] text-brass/90 break-all">{shortenAddress(status.address)}</div>
          <div className="text-[10px] text-fog/40">{t('appStore.cpuMinerRewardNote')}</div>
        </div>
      )}

      <p className="text-[11px] leading-snug text-amber-300/80">⚡ {t('appStore.cpuMinerLotteryWarning')}</p>
    </div>
  )
}
