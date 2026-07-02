import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getApps,
  getTapdAssets,
  getTapdDiscover,
  getTapdInfo,
  newTapdAddress,
  tapdMint,
  tapdMintFinalize,
  tapdSend,
  tapdUniverseSync
} from '../api'

type AppRow = { id: string; installed?: boolean; status?: string }

type ActionResult = { ok: boolean; data: unknown }

function pretty(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

// A held asset the user can pick by name (fills asset_id/group_key under the
// hood). Taproot Assets are addressed by their 32-byte asset_id or 33-byte
// group_key, never by name (names are not unique), so the picker maps the
// human-readable name to the real identifier.
type AssetOption = { key: string; label: string; assetId: string; groupKey: string; balance: string }

type assetGenesis = { name?: string; asset_id?: string }
type balanceEntry = { asset_genesis?: assetGenesis; group_key?: string; balance?: string }
type balancesShape = { asset_balances?: Record<string, balanceEntry> }

function parseAssetOptions(balances: unknown): AssetOption[] {
  const ab = (balances as balancesShape | null)?.asset_balances
  if (!ab || typeof ab !== 'object') return []
  const out: AssetOption[] = []
  const seen = new Set<string>()
  for (const [id, entry] of Object.entries(ab)) {
    const gen = entry?.asset_genesis || {}
    const assetId = gen.asset_id || id
    const groupKey = entry?.group_key || ''
    const key = groupKey || assetId
    if (seen.has(key)) continue
    seen.add(key)
    out.push({ key, label: gen.name || `${assetId.slice(0, 12)}…`, assetId, groupKey, balance: entry?.balance || '' })
  }
  return out
}

// One row per held asset (no dedupe by group), for the friendly balances table.
type balanceEntryFull = balanceEntry & { asset_type?: string }
type AssetRow = { name: string; balance: string; assetId: string; groupKey: string; assetType: string }

function parseAssetRows(balances: unknown): AssetRow[] {
  const ab = (balances as { asset_balances?: Record<string, balanceEntryFull> } | null)?.asset_balances
  if (!ab || typeof ab !== 'object') return []
  const rows: AssetRow[] = []
  for (const [id, entry] of Object.entries(ab)) {
    const gen = entry?.asset_genesis || {}
    const assetId = gen.asset_id || id
    rows.push({
      name: gen.name || `${assetId.slice(0, 12)}…`,
      balance: entry?.balance || '0',
      assetId,
      groupKey: entry?.group_key || '',
      assetType: entry?.asset_type || ''
    })
  }
  return rows.sort((a, b) => a.name.localeCompare(b.name))
}

function shortHex(hex: string): string {
  if (!hex) return '—'
  if (hex.length <= 16) return hex
  return `${hex.slice(0, 8)}…${hex.slice(-6)}`
}

type DiscoverAsset = { name: string; asset_id: string; group_key: string; proof_type: string; supply: string }

// Taproot Assets (tapd) — standalone, on-chain only (Camada 1). Lightning
// transfers / redeem-to-sats require the community edge node (Fase 2).
export default function TaprootAssets() {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(true)
  const [installed, setInstalled] = useState(false)
  const [running, setRunning] = useState(false)
  const [info, setInfo] = useState<unknown>(null)
  const [balances, setBalances] = useState<unknown>(null)
  const [result, setResult] = useState<ActionResult | null>(null)
  const [busy, setBusy] = useState('')

  // Receive
  const [rcvAssetId, setRcvAssetId] = useState('')
  const [rcvGroupKey, setRcvGroupKey] = useState('')
  const [rcvAmount, setRcvAmount] = useState('')
  const [rcvSelectedKey, setRcvSelectedKey] = useState('')
  const [rcvManual, setRcvManual] = useState(false)
  // Universe
  const [uniHost, setUniHost] = useState('universe.lightning.finance:10029')
  const [uniGroupKey, setUniGroupKey] = useState('')
  // Mint
  const [mintName, setMintName] = useState('')
  const [mintSupply, setMintSupply] = useState('')
  const [mintDecimals, setMintDecimals] = useState('')
  const [mintGrouped, setMintGrouped] = useState(true)
  const [mintReissueKey, setMintReissueKey] = useState('')
  const [mintMeta, setMintMeta] = useState('')
  // Send
  const [sendAddr, setSendAddr] = useState('')
  // Discover
  const [discHost, setDiscHost] = useState('universe.lightning.finance')
  const [discAssets, setDiscAssets] = useState<DiscoverAsset[]>([])
  const [discTotal, setDiscTotal] = useState(0)
  const [discBusy, setDiscBusy] = useState(false)
  const [discError, setDiscError] = useState('')

  const assetOptions = useMemo(() => parseAssetOptions(balances), [balances])
  const assetRows = useMemo(() => parseAssetRows(balances), [balances])

  const copy = useCallback((value: string) => {
    if (value) void navigator.clipboard?.writeText(value)
  }, [])

  const fetchDiscover = useCallback(async () => {
    setDiscBusy(true)
    setDiscError('')
    try {
      const res = (await getTapdDiscover(discHost.trim())) as { assets?: DiscoverAsset[]; total?: number }
      setDiscAssets(res.assets || [])
      setDiscTotal(res.total || 0)
    } catch (err) {
      setDiscError(err instanceof Error ? err.message : String(err))
      setDiscAssets([])
      setDiscTotal(0)
    } finally {
      setDiscBusy(false)
    }
  }, [discHost])

  const loadDaemon = useCallback(async () => {
    try {
      const [i, b] = await Promise.all([getTapdInfo(), getTapdAssets()])
      setInfo(i)
      setBalances(b)
    } catch {
      // daemon may be starting/syncing; leave panels empty
    }
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const apps = (await getApps()) as AppRow[]
      const app = Array.isArray(apps) ? apps.find((a) => a.id === 'tapd') : undefined
      const isInstalled = Boolean(app?.installed)
      const isRunning = app?.status === 'running'
      setInstalled(isInstalled)
      setRunning(isRunning)
      if (isRunning) await loadDaemon()
    } finally {
      setLoading(false)
    }
  }, [loadDaemon])

  useEffect(() => {
    void load()
  }, [load])

  const run = useCallback(async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key)
    setResult(null)
    try {
      const data = await fn()
      setResult({ ok: true, data })
      await loadDaemon()
    } catch (err) {
      setResult({ ok: false, data: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy('')
    }
  }, [loadDaemon])

  return (
    <div className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('tapd.title')}</h2>
        <p className="text-fog/70 mt-1">{t('tapd.subtitle')}</p>
        <p className="text-amber-300/80 text-sm mt-3">{t('tapd.experimental')}</p>
      </div>

      {loading && <div className="section-card text-fog/70">{t('tapd.loading')}</div>}

      {!loading && !installed && (
        <div className="section-card space-y-3">
          <p className="text-fog/70">{t('tapd.notInstalled')}</p>
          <a className="btn-primary inline-flex items-center" href="#apps">{t('tapd.openStore')}</a>
        </div>
      )}

      {!loading && installed && !running && (
        <div className="section-card space-y-3">
          <p className="text-fog/70">{t('tapd.notRunning')}</p>
          <a className="btn-primary inline-flex items-center" href="#apps">{t('tapd.openStore')}</a>
        </div>
      )}

      {!loading && installed && running && (
        <>
          <div className="flex justify-end">
            <button className="btn-secondary" onClick={() => void load()} disabled={Boolean(busy)}>
              {t('tapd.refresh')}
            </button>
          </div>

          <div className="grid gap-6 lg:grid-cols-2">
            <div className="section-card space-y-2">
              <h3 className="text-lg font-semibold">{t('tapd.sectionStatus')}</h3>
              <pre className="text-xs text-fog/70 overflow-auto max-h-64 whitespace-pre-wrap break-all">{pretty(info)}</pre>
            </div>
            <div className="section-card space-y-3">
              <h3 className="text-lg font-semibold">{t('tapd.sectionBalances')}</h3>
              {assetRows.length === 0 ? (
                <p className="text-sm text-fog/60">{t('tapd.noAssets')}</p>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="text-left text-xs uppercase tracking-wide text-fog/50">
                        <th className="py-1 pr-3">{t('tapd.asset')}</th>
                        <th className="py-1 pr-3 text-right">{t('tapd.colBalance')}</th>
                        <th className="py-1 pr-3">{t('tapd.assetId')}</th>
                        <th className="py-1">{t('tapd.groupKey')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {assetRows.map((row) => (
                        <tr key={row.assetId} className="border-t border-white/5">
                          <td className="py-2 pr-3 font-medium">{row.name}</td>
                          <td className="py-2 pr-3 text-right tabular-nums">{Number(row.balance).toLocaleString()}</td>
                          <td className="py-2 pr-3">
                            <button className="font-mono text-xs text-fog/70 hover:text-fog" title={`${row.assetId}\n${t('tapd.copy')}`} onClick={() => copy(row.assetId)}>{shortHex(row.assetId)}</button>
                          </td>
                          <td className="py-2">
                            <button className="font-mono text-xs text-fog/70 hover:text-fog" title={row.groupKey ? `${row.groupKey}\n${t('tapd.copy')}` : '—'} onClick={() => copy(row.groupKey)}>{shortHex(row.groupKey)}</button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </div>

          <div className="grid gap-6 lg:grid-cols-2">
            {/* Receive */}
            <div className="section-card space-y-4">
              <h3 className="text-lg font-semibold">{t('tapd.sectionReceive')}</h3>
              {assetOptions.length > 0 && !rcvManual ? (
                <div>
                  <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.asset')}</label>
                  <select className="input-field mt-2" value={rcvSelectedKey} onChange={(e) => setRcvSelectedKey(e.target.value)}>
                    <option value="">{t('tapd.selectAsset')}</option>
                    {assetOptions.map((o) => (
                      <option key={o.key} value={o.key}>{o.label}{o.balance ? ` — ${o.balance}` : ''}</option>
                    ))}
                  </select>
                </div>
              ) : (
                <>
                  <div>
                    <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.assetId')}</label>
                    <input className="input-field mt-2" value={rcvAssetId} onChange={(e) => setRcvAssetId(e.target.value)} placeholder="hex asset_id" />
                  </div>
                  <div>
                    <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.groupKey')}</label>
                    <input className="input-field mt-2" value={rcvGroupKey} onChange={(e) => setRcvGroupKey(e.target.value)} placeholder="hex group_key" />
                  </div>
                </>
              )}
              {assetOptions.length > 0 && (
                <label className="flex items-center gap-2 text-sm text-fog/70">
                  <input type="checkbox" checked={rcvManual} onChange={(e) => setRcvManual(e.target.checked)} />
                  {t('tapd.manualAsset')}
                </label>
              )}
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.amount')}</label>
                <input className="input-field mt-2" value={rcvAmount} onChange={(e) => setRcvAmount(e.target.value)} inputMode="numeric" />
              </div>
              <button
                className="btn-primary"
                disabled={busy === 'address'}
                onClick={() => void run('address', () => {
                  let assetId = rcvAssetId.trim()
                  let groupKey = rcvGroupKey.trim()
                  if (!rcvManual && rcvSelectedKey) {
                    const opt = assetOptions.find((o) => o.key === rcvSelectedKey)
                    if (opt) {
                      assetId = opt.assetId
                      groupKey = opt.groupKey
                    }
                  }
                  return newTapdAddress({
                    asset_id: groupKey ? undefined : (assetId || undefined),
                    group_key: groupKey || undefined,
                    amount: Number(rcvAmount) || 0
                  })
                })}
              >
                {t('tapd.generateAddress')}
              </button>
            </div>

            {/* Send */}
            <div className="section-card space-y-4">
              <h3 className="text-lg font-semibold">{t('tapd.sectionSend')}</h3>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.addr')}</label>
                <input className="input-field mt-2" value={sendAddr} onChange={(e) => setSendAddr(e.target.value)} placeholder="tap1..." />
              </div>
              <button
                className="btn-primary"
                disabled={busy === 'send'}
                onClick={() => void run('send', () => tapdSend({ addr: sendAddr.trim() }))}
              >
                {t('tapd.send')}
              </button>
            </div>

            {/* Mint */}
            <div className="section-card space-y-4">
              <h3 className="text-lg font-semibold">{t('tapd.sectionMint')}</h3>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.name')}</label>
                <input className="input-field mt-2" value={mintName} onChange={(e) => setMintName(e.target.value)} />
              </div>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.supply')}</label>
                <input className="input-field mt-2" value={mintSupply} onChange={(e) => setMintSupply(e.target.value)} inputMode="numeric" />
              </div>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.decimalDisplay')}</label>
                <input className="input-field mt-2" value={mintDecimals} onChange={(e) => setMintDecimals(e.target.value)} inputMode="numeric" placeholder="0" />
              </div>
              {assetOptions.some((o) => o.groupKey) && (
                <div>
                  <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.reissueGroup')}</label>
                  <select
                    className="input-field mt-2"
                    value={mintReissueKey}
                    onChange={(e) => {
                      const gk = e.target.value
                      setMintReissueKey(gk)
                      const opt = assetOptions.find((o) => o.groupKey === gk)
                      if (opt) setMintName(opt.label)
                    }}
                  >
                    <option value="">{t('tapd.reissueNew')}</option>
                    {assetOptions.filter((o) => o.groupKey).map((o) => (
                      <option key={o.key} value={o.groupKey}>{o.label}</option>
                    ))}
                  </select>
                </div>
              )}
              <label className={`flex items-center gap-2 text-sm ${mintReissueKey ? 'text-fog/30' : 'text-fog/70'}`}>
                <input type="checkbox" checked={mintGrouped} disabled={Boolean(mintReissueKey)} onChange={(e) => setMintGrouped(e.target.checked)} />
                {t('tapd.grouped')}
              </label>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.meta')}</label>
                <input className="input-field mt-2" value={mintMeta} onChange={(e) => setMintMeta(e.target.value)} placeholder='{"about":"BRLN points"}' />
              </div>
              <div className="flex gap-2">
                <button
                  className="btn-secondary"
                  disabled={busy === 'mint'}
                  onClick={() => void run('mint', () => tapdMint({
                    name: mintName.trim(),
                    supply: Number(mintSupply) || 0,
                    decimal_display: Number(mintDecimals) || 0,
                    grouped: mintReissueKey ? false : mintGrouped,
                    group_key: mintReissueKey || undefined,
                    meta: mintMeta.trim() || undefined
                  }))}
                >
                  {t('tapd.prepareMint')}
                </button>
                <button
                  className="btn-primary"
                  disabled={busy === 'mint-finalize'}
                  onClick={() => void run('mint-finalize', () => tapdMintFinalize())}
                >
                  {t('tapd.finalizeMint')}
                </button>
              </div>
            </div>

            {/* Universe sync */}
            <div className="section-card space-y-4">
              <h3 className="text-lg font-semibold">{t('tapd.sectionUniverse')}</h3>
              <p className="text-sm text-fog/60">{t('tapd.universeHint')}</p>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.universeHost')}</label>
                <input className="input-field mt-2" value={uniHost} onChange={(e) => setUniHost(e.target.value)} placeholder="universe.example.com:10029" />
              </div>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.groupKey')}</label>
                <input className="input-field mt-2" value={uniGroupKey} onChange={(e) => setUniGroupKey(e.target.value)} placeholder="hex group_key (optional)" />
              </div>
              <button
                className="btn-primary"
                disabled={busy === 'universe'}
                onClick={() => void run('universe', () => tapdUniverseSync({
                  universe_host: uniHost.trim(),
                  group_key: uniGroupKey.trim() || undefined
                }))}
              >
                {t('tapd.syncUniverse')}
              </button>
            </div>
          </div>

          {/* Discover assets from a universe REST catalog */}
          <div className="section-card space-y-3">
            <h3 className="text-lg font-semibold">{t('tapd.sectionDiscover')}</h3>
            <p className="text-sm text-fog/60">{t('tapd.discoverHint')}</p>
            <div className="flex flex-wrap items-end gap-2">
              <div className="flex-1 min-w-[240px]">
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.universeHost')}</label>
                <input className="input-field mt-2" value={discHost} onChange={(e) => setDiscHost(e.target.value)} placeholder="universe.lightning.finance" />
              </div>
              <button className="btn-primary" onClick={() => void fetchDiscover()} disabled={discBusy}>{t('tapd.fetch')}</button>
            </div>
            {discError && <p className="text-rose-300 text-sm break-all">{discError}</p>}
            {discAssets.length > 0 && (
              <>
                <p className="text-xs text-fog/50">{t('tapd.discoverCount', { shown: discAssets.length, total: discTotal })}</p>
                <div className="overflow-auto max-h-[32rem] rounded-lg border border-white/5">
                  <table className="w-full text-sm">
                    <thead className="sticky top-0 bg-ink/90 backdrop-blur">
                      <tr className="text-left text-xs uppercase tracking-wide text-fog/50">
                        <th className="py-1 pr-3">{t('tapd.asset')}</th>
                        <th className="py-1 pr-3 text-right">{t('tapd.colSupply')}</th>
                        <th className="py-1 pr-3">{t('tapd.assetId')}</th>
                        <th className="py-1 pr-3">{t('tapd.groupKey')}</th>
                        <th className="py-1"></th>
                      </tr>
                    </thead>
                    <tbody>
                      {discAssets.map((a, i) => (
                        <tr key={`${a.asset_id}-${i}`} className="border-t border-white/5">
                          <td className="py-2 pr-3 font-medium">{a.name || '—'}</td>
                          <td className="py-2 pr-3 text-right tabular-nums">{a.supply ? Number(a.supply).toLocaleString() : '—'}</td>
                          <td className="py-2 pr-3">
                            <button className="font-mono text-xs text-fog/70 hover:text-fog" title={`${a.asset_id}\n${t('tapd.copy')}`} onClick={() => copy(a.asset_id)}>{shortHex(a.asset_id)}</button>
                          </td>
                          <td className="py-2 pr-3">
                            <button className="font-mono text-xs text-fog/70 hover:text-fog" title={a.group_key ? `${a.group_key}\n${t('tapd.copy')}` : '—'} onClick={() => copy(a.group_key)}>{shortHex(a.group_key)}</button>
                          </td>
                          <td className="py-2">
                            <button
                              className="btn-secondary text-xs"
                              disabled={busy === 'universe'}
                              onClick={() => void run('universe', () => tapdUniverseSync({
                                universe_host: `${discHost.trim().replace(/:\d+$/, '')}:10029`,
                                group_key: a.group_key || undefined,
                                asset_id: a.group_key ? undefined : a.asset_id
                              }))}
                            >
                              {t('tapd.syncUniverse')}
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </div>

          {/* Redeem — Fase 2 */}
          <div className="section-card space-y-3">
            <h3 className="text-lg font-semibold">{t('tapd.sectionRedeem')}</h3>
            <p className="text-fog/60 text-sm">{t('tapd.redeemPhase2')}</p>
            <button className="btn-secondary opacity-50 cursor-not-allowed" disabled>
              {t('tapd.sectionRedeem')}
            </button>
          </div>

          {result && (
            <div className="section-card space-y-2">
              <h3 className={`text-lg font-semibold ${result.ok ? '' : 'text-rose-300'}`}>
                {result.ok ? t('tapd.result') : t('tapd.error')}
              </h3>
              <pre className="text-xs text-fog/70 overflow-auto max-h-96 whitespace-pre-wrap break-all">{pretty(result.data)}</pre>
            </div>
          )}
        </>
      )}
    </div>
  )
}
