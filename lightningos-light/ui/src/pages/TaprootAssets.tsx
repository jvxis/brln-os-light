import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getApps,
  getTapdAssets,
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
  // Universe
  const [uniHost, setUniHost] = useState('')
  const [uniGroupKey, setUniGroupKey] = useState('')
  // Mint
  const [mintName, setMintName] = useState('')
  const [mintSupply, setMintSupply] = useState('')
  const [mintDecimals, setMintDecimals] = useState('')
  const [mintGrouped, setMintGrouped] = useState(true)
  const [mintMeta, setMintMeta] = useState('')
  // Send
  const [sendAddr, setSendAddr] = useState('')

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
            <div className="section-card space-y-2">
              <h3 className="text-lg font-semibold">{t('tapd.sectionBalances')}</h3>
              <pre className="text-xs text-fog/70 overflow-auto max-h-64 whitespace-pre-wrap break-all">{pretty(balances)}</pre>
            </div>
          </div>

          <div className="grid gap-6 lg:grid-cols-2">
            {/* Receive */}
            <div className="section-card space-y-4">
              <h3 className="text-lg font-semibold">{t('tapd.sectionReceive')}</h3>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.assetId')}</label>
                <input className="input-field mt-2" value={rcvAssetId} onChange={(e) => setRcvAssetId(e.target.value)} placeholder="hex asset_id" />
              </div>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.groupKey')}</label>
                <input className="input-field mt-2" value={rcvGroupKey} onChange={(e) => setRcvGroupKey(e.target.value)} placeholder="hex group_key" />
              </div>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('tapd.amount')}</label>
                <input className="input-field mt-2" value={rcvAmount} onChange={(e) => setRcvAmount(e.target.value)} inputMode="numeric" />
              </div>
              <button
                className="btn-primary"
                disabled={busy === 'address'}
                onClick={() => void run('address', () => newTapdAddress({
                  asset_id: rcvAssetId.trim() || undefined,
                  group_key: rcvGroupKey.trim() || undefined,
                  amount: Number(rcvAmount) || 0
                }))}
              >
                {t('tapd.generateAddress')}
              </button>
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
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={mintGrouped} onChange={(e) => setMintGrouped(e.target.checked)} />
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
                    grouped: mintGrouped,
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
