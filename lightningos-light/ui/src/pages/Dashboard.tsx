import { useEffect, useState } from 'react'
import { getBitcoinActive, getDisk, getLndStatus, getPostgres, getSystem, restartService } from '../api'

export default function Dashboard() {
  const [system, setSystem] = useState<any>(null)
  const [disk, setDisk] = useState<any[]>([])
  const [bitcoin, setBitcoin] = useState<any>(null)
  const [postgres, setPostgres] = useState<any>(null)
  const [lnd, setLnd] = useState<any>(null)
  const [status, setStatus] = useState('Loading...')

  const syncLabel = (info: any) => {
    if (!info || typeof info.verification_progress !== 'number') {
      return 'n/a'
    }
    return `${(info.verification_progress * 100).toFixed(2)}%`
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

  const overallTone = status === 'OK' ? 'ok' : status === 'Unavailable' ? 'warn' : 'muted'
  const lndInfoStale = Boolean(lnd?.info_stale && lnd?.info_known)
  const lndInfoAge = Number(lnd?.info_age_seconds || 0)
  const lndInfoStaleTooLong = lndInfoStale && lndInfoAge > 900

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
        setStatus('OK')
      } catch {
        if (!mounted) return
        setStatus('Unavailable')
      }
    }
    load()
    const timer = setInterval(load, 30000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  const restart = async (service: string) => {
    await restartService({ service })
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex items-start justify-between">
          <div>
            <p className="text-sm text-fog/60">System pulse</p>
            <div className="flex items-center gap-3">
              <h2 className="text-2xl font-semibold">Overall status</h2>
              <Badge label={status} tone={overallTone} />
            </div>
          </div>
          <div className="flex gap-2">
            <button className="btn-secondary" onClick={() => restart('lnd')}>Restart LND</button>
            <button className="btn-secondary" onClick={() => restart('lightningos-manager')}>Restart Manager</button>
          </div>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">LND</h3>
            <div className="text-right">
              <div className="flex items-center justify-end gap-2 text-xs text-fog/60">
                <span>{lnd?.version ? `v${lnd.version}` : ''}</span>
                {lndInfoStale && (
                  <span className="uppercase text-[10px] text-fog/40">cached</span>
                )}
              </div>
              {(lnd?.pubkey || lnd?.uri) && (
                <div className="mt-2 space-y-1 text-xs text-fog/60">
                  {lnd?.pubkey && (
                    <div className="flex items-center justify-end gap-2">
                      <span className="text-fog/50">Pubkey</span>
                      <span
                        className="font-mono text-fog/70 max-w-[220px] truncate"
                        title={lnd.pubkey}
                      >
                        {compactValue(lnd.pubkey)}
                      </span>
                      <button
                        className="text-fog/50 hover:text-fog"
                        onClick={() => copyToClipboard(lnd.pubkey)}
                        title="Copy pubkey"
                        aria-label="Copy pubkey"
                      >
                        <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6">
                          <rect x="9" y="9" width="11" height="11" rx="2" />
                          <rect x="4" y="4" width="11" height="11" rx="2" />
                        </svg>
                      </button>
                    </div>
                  )}
                  {lnd?.uri && (
                    <div className="flex items-center justify-end gap-2">
                      <span className="text-fog/50">URI</span>
                      <span
                        className="font-mono text-fog/70 max-w-[220px] truncate"
                        title={lnd.uri}
                      >
                        {compactValue(lnd.uri)}
                      </span>
                      <button
                        className="text-fog/50 hover:text-fog"
                        onClick={() => copyToClipboard(lnd.uri)}
                        title="Copy URI"
                        aria-label="Copy URI"
                      >
                        <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6">
                          <rect x="9" y="9" width="11" height="11" rx="2" />
                          <rect x="4" y="4" width="11" height="11" rx="2" />
                        </svg>
                      </button>
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
          {lnd ? (
            <div className="mt-4 text-sm space-y-2">
              <div className="flex justify-between">
                <span>Wallet</span>
                <Badge label={lnd.wallet_state} tone={lnd.wallet_state === 'unlocked' ? 'ok' : 'warn'} />
              </div>
              <div className="flex justify-between items-center">
                <span>Synced</span>
                <div className="flex items-center gap-2">
                  <Badge
                    label={
                      lnd.synced_to_chain
                        ? (lndInfoStale && !lndInfoStaleTooLong ? 'chain cached' : 'chain')
                        : 'chain pending'
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
                        ? (lndInfoStale && !lndInfoStaleTooLong ? 'graph cached' : 'graph')
                        : 'graph pending'
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
                <span>Channels</span>
                <div className="flex items-center gap-2">
                  <Badge label={`${lnd.channels.active} active`} tone={lnd.channels.active > 0 ? 'ok' : 'warn'} />
                  <Badge label={`${lnd.channels.inactive} inactive`} tone={lnd.channels.inactive > 0 ? 'warn' : 'muted'} />
                </div>
              </div>
              <div className="flex justify-between"><span>Balances</span><span>{lnd.balances.onchain_sat} sats on-chain / {lnd.balances.lightning_sat} sats LN</span></div>
            </div>
          ) : (
            <p className="text-fog/60 mt-4">Loading LND status...</p>
          )}
        </div>

        <div className="section-card">
          <h3 className="text-lg font-semibold">{bitcoin?.mode === 'local' ? 'Bitcoin Local' : 'Bitcoin Remote'}</h3>
          {bitcoin ? (
            <div className="mt-4 text-sm space-y-2">
              <div className="flex justify-between"><span>Host</span><span>{bitcoin.rpchost}</span></div>
              <div className="flex justify-between"><span>RPC</span><Badge label={bitcoin.rpc_ok ? 'OK' : 'Fail'} tone={bitcoin.rpc_ok ? 'ok' : 'warn'} /></div>
              <div className="flex justify-between"><span>ZMQ Raw Block</span><Badge label={bitcoin.zmq_rawblock_ok ? 'OK' : 'Fail'} tone={bitcoin.zmq_rawblock_ok ? 'ok' : 'warn'} /></div>
              <div className="flex justify-between"><span>ZMQ Raw Tx</span><Badge label={bitcoin.zmq_rawtx_ok ? 'OK' : 'Fail'} tone={bitcoin.zmq_rawtx_ok ? 'ok' : 'warn'} /></div>
              {bitcoin.rpc_ok && (
                <>
                  <div className="flex justify-between"><span>Chain</span><span>{bitcoin.chain || 'n/a'}</span></div>
                  <div className="flex justify-between"><span>Blocks</span><span>{bitcoin.blocks ?? 'n/a'}</span></div>
                  <div className="flex justify-between"><span>Sync</span><span>{syncLabel(bitcoin)}</span></div>
                </>
              )}
            </div>
          ) : (
            <p className="text-fog/60 mt-4">Loading bitcoin status...</p>
          )}
        </div>

        <div className="section-card">
          <h3 className="text-lg font-semibold">Postgres</h3>
          {postgres ? (
            <div className="mt-4 text-sm space-y-2">
              <div className="flex justify-between"><span>Service</span><Badge label={postgres.service_active ? 'Active' : 'Inactive'} tone={postgres.service_active ? 'ok' : 'warn'} /></div>
              <div className="flex justify-between"><span>Version</span><span>{postgres.version || 'n/a'}</span></div>
              <div className="flex justify-between"><span>DB size</span><span>{postgres.db_size_mb} MB</span></div>
              <div className="flex justify-between"><span>Connections</span><span>{postgres.connections}</span></div>
            </div>
          ) : (
            <p className="text-fog/60 mt-4">Loading postgres status...</p>
          )}
        </div>

        <div className="section-card">
          <h3 className="text-lg font-semibold">System</h3>
          {system ? (
            <div className="mt-4 grid grid-cols-2 gap-4 text-sm">
              <div>
                <p className="text-fog/60">CPU load</p>
                <p>{system.cpu_load_1?.toFixed?.(2)} / {system.cpu_percent?.toFixed?.(1)}%</p>
              </div>
              <div>
                <p className="text-fog/60">RAM used</p>
                <p>{system.ram_used_mb} / {system.ram_total_mb} MB</p>
              </div>
              <div>
                <p className="text-fog/60">Uptime</p>
                <p>{Math.round(system.uptime_sec / 3600)} hours</p>
              </div>
              <div>
                <p className="text-fog/60">Temp</p>
                <p>{system.temperature_c?.toFixed?.(1)} C</p>
              </div>
            </div>
          ) : (
            <p className="text-fog/60 mt-4">Loading system info...</p>
          )}
        </div>
      </div>

      <div className="section-card">
        <h3 className="text-lg font-semibold">Disks</h3>
        {disk.length ? (
          <div className="mt-4 grid gap-3">
            {disk.map((item) => (
              <div key={item.device} className="flex flex-col lg:flex-row lg:items-center lg:justify-between bg-ink/40 rounded-2xl p-4">
                <div>
                  <p className="text-sm text-fog/70">{item.device} ({item.type})</p>
                  <p className="text-xs text-fog/50">Power on hours: {item.power_on_hours}</p>
                </div>
                <div className="text-sm text-fog/80">
                  Wear: {item.wear_percent_used}% | Days left: {item.days_left_estimate}
                </div>
                <div className="text-xs text-fog/60">SMART: {item.smart_status}</div>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-fog/60 mt-4">No disk data yet.</p>
        )}
      </div>
    </section>
  )
}
