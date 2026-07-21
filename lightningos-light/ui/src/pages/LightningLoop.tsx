import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  APIError,
  getLoopQuote,
  getLoopStatus,
  getLoopSwaps,
  reauthAuth,
  startLoopSwap,
  type LoopQuote,
  type LoopQuotePayload,
  type LoopStatus,
  type LoopSwap
} from '../api'

type Direction = 'out' | 'in'

const sats = (value?: number) => `${Math.max(0, value || 0).toLocaleString()} sat`

const swapTime = (value: number) => {
  if (!value) return '—'
  const milliseconds = value > 10_000_000_000_000 ? Math.floor(value / 1_000_000) : value * 1000
  const date = new Date(milliseconds)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

export default function LightningLoop() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<LoopStatus | null>(null)
  const [swaps, setSwaps] = useState<LoopSwap[]>([])
  const [loading, setLoading] = useState(true)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [direction, setDirection] = useState<Direction>('out')
  const [amountSat, setAmountSat] = useState('')
  const [confTarget, setConfTarget] = useState('9')
  const [routingPPM, setRoutingPPM] = useState('2500')
  const [channels, setChannels] = useState('')
  const [lastHop, setLastHop] = useState('')
  const [destination, setDestination] = useState('')
  const [fast, setFast] = useState(false)
  const [quote, setQuote] = useState<LoopQuote | null>(null)
  const [maxMinerFee, setMaxMinerFee] = useState('')
  const [riskAccepted, setRiskAccepted] = useState(false)
  const [reauthOpen, setReauthOpen] = useState(false)
  const [password, setPassword] = useState('')

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const nextStatus = await getLoopStatus()
      setStatus(nextStatus)
      if (nextStatus.running) {
        const history = await getLoopSwaps(100)
        setSwaps(history.swaps || [])
      } else {
        setSwaps([])
      }
      if (!silent) setMessage('')
    } catch (err: any) {
      if (!silent) setMessage(err?.message || t('lightningLoop.loadFailed'))
    } finally {
      if (!silent) setLoading(false)
    }
  }, [t])

  useEffect(() => {
    load()
    const timer = window.setInterval(() => load(true), 15_000)
    return () => window.clearInterval(timer)
  }, [load])

  useEffect(() => {
    setConfTarget(direction === 'out' ? '9' : '6')
    setQuote(null)
    setRiskAccepted(false)
  }, [direction])

  const requestPayload = useMemo<LoopQuotePayload>(() => ({
    direction,
    amount_sat: Number(amountSat),
    conf_target: Number(confTarget),
    routing_fee_limit_ppm: Number(routingPPM),
    last_hop_pubkey: direction === 'in' ? lastHop.trim() : undefined,
    fast: direction === 'out' ? fast : false
  }), [amountSat, confTarget, direction, fast, lastHop, routingPPM])

  const requestQuote = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setMessage('')
    setQuote(null)
    setRiskAccepted(false)
    try {
      const result = await getLoopQuote(requestPayload)
      setQuote(result)
      setMaxMinerFee(String(result.recommended_max_miner_fee_sat))
    } catch (err: any) {
      setMessage(err?.message || t('lightningLoop.quoteFailed'))
    } finally {
      setBusy(false)
    }
  }

  const execute = async (confirmPassword?: string) => {
    if (!quote) return
    const channelIDs = channels.split(',').map((item) => item.trim()).filter(Boolean)
    setBusy(true)
    setMessage('')
    try {
      await startLoopSwap({
        ...requestPayload,
        destination_address: direction === 'out' ? destination.trim() : undefined,
        outgoing_channel_ids: direction === 'out' ? channelIDs : undefined,
        approved_swap_fee_sat: quote.swap_fee_sat,
        approved_onchain_fee_sat: quote.onchain_fee_sat,
        approved_routing_fee_limit_sat: quote.routing_fee_limit_sat || 0,
        max_miner_fee_sat: Number(maxMinerFee),
        confirm_password: confirmPassword
      })
      setMessage(t('lightningLoop.swapStarted'))
      setQuote(null)
      setRiskAccepted(false)
      setReauthOpen(false)
      setPassword('')
      await load(true)
    } catch (err: any) {
      if (err instanceof APIError && err.code === 'loop_swap_reauth_required') {
        setReauthOpen(true)
      } else {
        if (err instanceof APIError && err.code === 'loop_quote_changed') setQuote(null)
        setMessage(err?.message || t('lightningLoop.swapFailed'))
      }
    } finally {
      setBusy(false)
    }
  }

  const submitPassword = async (event: FormEvent) => {
    event.preventDefault()
    if (!password.trim()) return
    setBusy(true)
    try {
      await reauthAuth({ password, scope: 'loop_swap' })
      await execute()
    } catch (err: any) {
      setMessage(err?.message || t('lightningLoop.reauthFailed'))
      setBusy(false)
    }
  }

  const min = direction === 'out' ? status?.terms?.loop_out_min_sat : status?.terms?.loop_in_min_sat
  const max = direction === 'out' ? status?.terms?.loop_out_max_sat : status?.terms?.loop_in_max_sat

  if (loading) return <div className="card p-6 text-fog/70">{t('common.loading')}</div>

  if (!status?.installed) {
    return (
      <div className="space-y-5">
        <div>
          <p className="text-xs uppercase tracking-[0.2em] text-brass">Lightning Labs</p>
          <h2 className="text-2xl font-semibold">Lightning Loop</h2>
        </div>
        <div className="card p-6 space-y-3">
          <h3 className="text-lg font-semibold">{t('lightningLoop.optionalTitle')}</h3>
          <p className="text-sm text-fog/70">{t('lightningLoop.optionalBody')}</p>
          <a className="btn-primary inline-flex" href="#apps">{t('lightningLoop.openStore')}</a>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-xs uppercase tracking-[0.2em] text-brass">Lightning Labs • {status.network || 'mainnet'}</p>
          <h2 className="text-2xl font-semibold">Lightning Loop</h2>
          <p className="mt-1 text-sm text-fog/70">{t('lightningLoop.subtitle')}</p>
        </div>
        <div className={`rounded-full border px-3 py-1 text-xs ${status.running ? 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200' : 'border-amber-400/30 bg-amber-500/15 text-amber-200'}`}>
          {status.running ? t('common.running') : t('common.stopped')} {status.version ? `• ${status.version}` : ''}
        </div>
      </div>

      {message && <div className="card p-4 text-sm text-brass">{message}</div>}

      {!status.running ? (
        <div className="card p-6 space-y-3">
          <h3 className="font-semibold">{t('lightningLoop.stoppedTitle')}</h3>
          <p className="text-sm text-fog/70">{t('lightningLoop.stoppedBody')}</p>
          <a className="btn-secondary inline-flex" href="#apps">{t('lightningLoop.openStore')}</a>
        </div>
      ) : (
        <>
          <div className="grid gap-4 md:grid-cols-3">
            <div className="card p-4"><p className="text-xs text-fog/60">{t('lightningLoop.pending')}</p><p className="mt-1 text-2xl font-semibold">{status.pending_count}</p></div>
            <div className="card p-4"><p className="text-xs text-fog/60">Loop Out</p><p className="mt-1 text-sm">{sats(status.terms?.loop_out_min_sat)} – {sats(status.terms?.loop_out_max_sat)}</p></div>
            <div className="card p-4"><p className="text-xs text-fog/60">Loop In</p><p className="mt-1 text-sm">{sats(status.terms?.loop_in_min_sat)} – {sats(status.terms?.loop_in_max_sat)}</p></div>
          </div>

          <div className="card p-5 space-y-5">
            <div>
              <h3 className="text-lg font-semibold">{t('lightningLoop.newSwap')}</h3>
              <p className="text-sm text-fog/65">{direction === 'out' ? t('lightningLoop.outHelp') : t('lightningLoop.inHelp')}</p>
            </div>
            <form className="space-y-4" onSubmit={requestQuote}>
              <div className="grid gap-4 md:grid-cols-2">
                <label className="space-y-1 text-sm"><span>{t('lightningLoop.direction')}</span><select className="input w-full" value={direction} onChange={(e) => setDirection(e.target.value as Direction)}><option value="out">Loop Out — Lightning → on-chain</option><option value="in">Loop In — on-chain → Lightning</option></select></label>
                <label className="space-y-1 text-sm"><span>{t('lightningLoop.amount')}</span><input className="input w-full" type="number" min={min || 1} max={max || undefined} required value={amountSat} onChange={(e) => { setAmountSat(e.target.value); setQuote(null) }} /></label>
                <label className="space-y-1 text-sm"><span>{t('lightningLoop.confTarget')}</span><input className="input w-full" type="number" min="1" max="2016" required value={confTarget} onChange={(e) => { setConfTarget(e.target.value); setQuote(null) }} /></label>
                {direction === 'out' && <label className="space-y-1 text-sm"><span>{t('lightningLoop.routingPPM')}</span><input className="input w-full" type="number" min="0" max="100000" value={routingPPM} onChange={(e) => { setRoutingPPM(e.target.value); setQuote(null) }} /></label>}
                {direction === 'out' && <label className="space-y-1 text-sm md:col-span-2"><span>{t('lightningLoop.channels')}</span><input className="input w-full" required value={channels} onChange={(e) => setChannels(e.target.value)} placeholder="123456789012345678, 223456789012345678" /></label>}
                {direction === 'out' && <label className="space-y-1 text-sm md:col-span-2"><span>{t('lightningLoop.destination')}</span><input className="input w-full" value={destination} onChange={(e) => setDestination(e.target.value)} placeholder={t('lightningLoop.destinationHint')} /></label>}
                {direction === 'in' && <label className="space-y-1 text-sm md:col-span-2"><span>{t('lightningLoop.lastHop')}</span><input className="input w-full" value={lastHop} onChange={(e) => { setLastHop(e.target.value); setQuote(null) }} placeholder={t('lightningLoop.lastHopHint')} /></label>}
              </div>
              {direction === 'out' && <label className="flex items-start gap-2 text-sm text-fog/75"><input className="mt-1" type="checkbox" checked={fast} onChange={(e) => { setFast(e.target.checked); setQuote(null) }} /><span>{t('lightningLoop.fast')}</span></label>}
              <button className="btn-primary" type="submit" disabled={busy}>{busy ? t('common.loading') : t('lightningLoop.getQuote')}</button>
            </form>

            {quote && (
              <div className="rounded-xl border border-brass/30 bg-brass/5 p-4 space-y-4">
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <div><p className="text-xs text-fog/60">{t('lightningLoop.serviceFee')}</p><p className="font-semibold">{sats(quote.swap_fee_sat)}</p></div>
                  <div><p className="text-xs text-fog/60">{t('lightningLoop.onchainEstimate')}</p><p className="font-semibold">{sats(quote.onchain_fee_sat)}</p></div>
                  <div><p className="text-xs text-fog/60">{t('lightningLoop.estimatedTotal')}</p><p className="font-semibold">{sats(quote.estimated_fee_sat)}</p></div>
                  <div><p className="text-xs text-fog/60">{t('lightningLoop.expires')}</p><p className="font-semibold">{new Date(quote.expires_at).toLocaleTimeString()}</p></div>
                </div>
                {direction === 'out' && <p className="text-xs text-fog/65">{t('lightningLoop.routingLimit', { value: sats((quote.routing_fee_limit_sat || 0) + (quote.prepay_routing_limit_sat || 0)) })}</p>}
                <label className="block space-y-1 text-sm"><span>{t('lightningLoop.maxMiner')}</span><input className="input w-full md:w-64" type="number" min={quote.onchain_fee_sat} required value={maxMinerFee} onChange={(e) => setMaxMinerFee(e.target.value)} /><p className="text-xs text-amber-200/80">{t('lightningLoop.maxMinerWarning')}</p></label>
                <label className="flex items-start gap-2 text-sm"><input className="mt-1" type="checkbox" checked={riskAccepted} onChange={(e) => setRiskAccepted(e.target.checked)} /><span>{t('lightningLoop.confirmRisk')}</span></label>
                <button className="btn-primary" type="button" disabled={busy || !riskAccepted || Number(maxMinerFee) < quote.onchain_fee_sat} onClick={() => execute()}>{t('lightningLoop.execute')}</button>
              </div>
            )}
          </div>

          <div className="card overflow-hidden">
            <div className="border-b border-white/10 p-4"><h3 className="font-semibold">{t('lightningLoop.history')}</h3></div>
            {swaps.length === 0 ? <p className="p-5 text-sm text-fog/60">{t('lightningLoop.noSwaps')}</p> : (
              <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead className="text-xs text-fog/55"><tr><th className="p-3">{t('lightningLoop.type')}</th><th className="p-3">{t('lightningLoop.amount')}</th><th className="p-3">{t('lightningLoop.state')}</th><th className="p-3">{t('lightningLoop.cost')}</th><th className="p-3">{t('lightningLoop.started')}</th></tr></thead><tbody>{swaps.map((swap) => <tr className="border-t border-white/5" key={swap.id}><td className="p-3">{swap.type}</td><td className="p-3">{sats(swap.amount_sat)}</td><td className="p-3"><span className={isPendingState(swap.state) ? 'text-amber-200' : swap.state === 'SUCCESS' ? 'text-emerald-200' : 'text-rose-200'}>{swap.state}</span>{swap.failure_reason && <p className="text-xs text-rose-200/70">{swap.failure_reason}</p>}</td><td className="p-3">{sats(swap.cost_server_sat + swap.cost_onchain_sat + swap.cost_offchain_sat)}</td><td className="p-3 text-fog/70">{swapTime(swap.initiation_time)}</td></tr>)}</tbody></table></div>
            )}
          </div>
        </>
      )}

      {reauthOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
          <form className="card w-full max-w-md space-y-4 p-6" onSubmit={submitPassword}>
            <h3 className="text-lg font-semibold">{t('lightningLoop.reauthTitle')}</h3>
            <p className="text-sm text-fog/70">{t('lightningLoop.reauthBody')}</p>
            <input className="input w-full" type="password" autoFocus required value={password} onChange={(e) => setPassword(e.target.value)} />
            <div className="flex justify-end gap-2"><button className="btn-secondary" type="button" onClick={() => { setReauthOpen(false); setPassword('') }}>{t('common.cancel')}</button><button className="btn-primary" disabled={busy} type="submit">{t('lightningLoop.execute')}</button></div>
          </form>
        </div>
      )}
    </div>
  )
}

const isPendingState = (state: string) => !['SUCCESS', 'FAILED'].includes(String(state || '').toUpperCase())
