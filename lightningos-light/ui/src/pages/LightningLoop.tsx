import { FormEvent, ReactNode, useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  APIError,
  getLnChannels,
  getLoopQuote,
  getLoopStatus,
  getLoopSwaps,
  getMempoolFees,
  reauthAuth,
  startLoopSwap,
  type LoopQuote,
  type LoopQuotePayload,
  type LoopStatus,
  type LoopSwap
} from '../api'
import { getLocale } from '../i18n'

type Direction = 'out' | 'in'

type LoopChannel = {
  channel_point: string
  channel_id: number
  channel_id_str?: string
  remote_pubkey: string
  peer_alias: string
  active: boolean
  private: boolean
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
}

type LoopInDestination = {
  pubkey: string
  alias: string
  channelIDs: string[]
  localBalanceSat: number
  remoteBalanceSat: number
}

type FeePulse = {
  fastest: number
  hour: number
}

const channelID = (channel: LoopChannel) => String(channel.channel_id_str || channel.channel_id || '').trim()
const isPendingState = (state: string) => !['SUCCESS', 'FAILED'].includes(String(state || '').toUpperCase())
const loopStateKeys: Record<string, string> = {
  INITIATED: 'lightningLoop.stateInitiated',
  HTLC_PUBLISHED: 'lightningLoop.stateHtlcPublished',
  PREIMAGE_REVEALED: 'lightningLoop.statePreimageRevealed',
  INVOICE_SETTLED: 'lightningLoop.stateInvoiceSettled',
  SUCCESS: 'lightningLoop.stateSuccess',
  FAILED: 'lightningLoop.stateFailed'
}

export default function LightningLoop() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const sats = useCallback((value?: number) => `${Math.max(0, value || 0).toLocaleString(locale)} sat`, [locale])
  const compactSats = useCallback((value?: number) => `${Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(Math.max(0, value || 0))} sat`, [locale])
  const swapTime = useCallback((value: number) => {
    if (!value) return '—'
    const milliseconds = value > 10_000_000_000_000 ? Math.floor(value / 1_000_000) : value * 1000
    const date = new Date(milliseconds)
    return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString(locale)
  }, [locale])

  const [status, setStatus] = useState<LoopStatus | null>(null)
  const [swaps, setSwaps] = useState<LoopSwap[]>([])
  const [nodeChannels, setNodeChannels] = useState<LoopChannel[]>([])
  const [feePulse, setFeePulse] = useState<FeePulse | null>(null)
  const [loading, setLoading] = useState(true)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [direction, setDirection] = useState<Direction>('out')
  const [amountSat, setAmountSat] = useState('')
  const [confTarget, setConfTarget] = useState('9')
  const [routingPPM, setRoutingPPM] = useState('2500')
  const [selectedChannelIDs, setSelectedChannelIDs] = useState<Set<string>>(new Set())
  const [channelSearch, setChannelSearch] = useState('')
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
      const [historyResult, channelsResult, feesResult] = await Promise.allSettled([
        nextStatus.running ? getLoopSwaps(100) : Promise.resolve({ swaps: [] }),
        getLnChannels(),
        getMempoolFees()
      ])
      if (historyResult.status === 'fulfilled') setSwaps(historyResult.value.swaps || [])
      if (channelsResult.status === 'fulfilled') {
        const payload = channelsResult.value as any
        setNodeChannels(Array.isArray(payload?.channels) ? payload.channels : [])
      }
      if (feesResult.status === 'fulfilled') {
        const payload = feesResult.value as any
        setFeePulse({ fastest: Number(payload?.fastestFee || 0), hour: Number(payload?.hourFee || 0) })
      }
      if (!silent) setMessage('')
    } catch (err: any) {
      if (!silent) setMessage(err?.message || t('lightningLoop.loadFailed'))
    } finally {
      if (!silent) setLoading(false)
    }
  }, [t])

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(true), 15_000)
    return () => window.clearInterval(timer)
  }, [load])

  useEffect(() => {
    setConfTarget(direction === 'out' ? '9' : '6')
    setChannelSearch('')
    setQuote(null)
    setRiskAccepted(false)
  }, [direction])

  const eligibleChannels = useMemo(() => nodeChannels
    .filter((channel) => channel.active && channelID(channel))
    .sort((a, b) => b.local_balance_sat - a.local_balance_sat), [nodeChannels])

  const visibleChannels = useMemo(() => {
    const query = channelSearch.trim().toLowerCase()
    if (!query) return eligibleChannels
    return eligibleChannels.filter((channel) =>
      channel.peer_alias?.toLowerCase().includes(query) ||
      channel.remote_pubkey?.toLowerCase().includes(query) ||
      channelID(channel).includes(query))
  }, [channelSearch, eligibleChannels])

  const loopInDestinations = useMemo(() => {
    const destinations = new Map<string, LoopInDestination>()
    for (const channel of nodeChannels) {
      const pubkey = String(channel.remote_pubkey || '').trim()
      if (!channel.active || !pubkey) continue
      const existing = destinations.get(pubkey)
      if (existing) {
        existing.localBalanceSat += Math.max(0, channel.local_balance_sat || 0)
        existing.remoteBalanceSat += Math.max(0, channel.remote_balance_sat || 0)
        const id = channelID(channel)
        if (id) existing.channelIDs.push(id)
        if (!existing.alias && channel.peer_alias) existing.alias = channel.peer_alias
        continue
      }
      const id = channelID(channel)
      destinations.set(pubkey, {
        pubkey,
        alias: channel.peer_alias || '',
        channelIDs: id ? [id] : [],
        localBalanceSat: Math.max(0, channel.local_balance_sat || 0),
        remoteBalanceSat: Math.max(0, channel.remote_balance_sat || 0)
      })
    }
    return Array.from(destinations.values()).sort((a, b) => b.remoteBalanceSat - a.remoteBalanceSat)
  }, [nodeChannels])

  const visibleLoopInDestinations = useMemo(() => {
    const query = channelSearch.trim().toLowerCase()
    if (!query) return loopInDestinations
    return loopInDestinations.filter((destinationOption) =>
      destinationOption.alias.toLowerCase().includes(query) ||
      destinationOption.pubkey.toLowerCase().includes(query) ||
      destinationOption.channelIDs.some((id) => id.includes(query)))
  }, [channelSearch, loopInDestinations])

  const selectedLoopInDestination = useMemo(() => loopInDestinations.find((destinationOption) => destinationOption.pubkey === lastHop.trim()), [lastHop, loopInDestinations])

  const selectedChannels = useMemo(() => eligibleChannels.filter((channel) => selectedChannelIDs.has(channelID(channel))), [eligibleChannels, selectedChannelIDs])
  const pendingSwaps = useMemo(() => swaps.filter((swap) => isPendingState(swap.state)), [swaps])
  const selectedLocal = useMemo(() => selectedChannels.reduce((sum, channel) => sum + channel.local_balance_sat, 0), [selectedChannels])
  const amount = Number(amountSat || 0)
  const min = direction === 'out' ? status?.terms?.loop_out_min_sat : status?.terms?.loop_in_min_sat
  const max = direction === 'out' ? status?.terms?.loop_out_max_sat : status?.terms?.loop_in_max_sat
  const amountIsValid = amount > 0 && (!min || amount >= min) && (!max || amount <= max)
  const selectedLoopInDestinationIsInsufficient = Boolean(selectedLoopInDestination && amount > 0 && selectedLoopInDestination.remoteBalanceSat < amount)
  const liquidityIsValid = direction === 'in'
    ? !selectedLoopInDestinationIsInsufficient
    : selectedChannelIDs.size > 0 && selectedLocal >= amount

  const requestPayload = useMemo<LoopQuotePayload>(() => ({
    direction,
    amount_sat: Number(amountSat),
    conf_target: Number(confTarget),
    routing_fee_limit_ppm: Number(routingPPM),
    outgoing_channel_ids: direction === 'out' ? Array.from(selectedChannelIDs) : undefined,
    last_hop_pubkey: direction === 'in' ? lastHop.trim() : undefined,
    fast: direction === 'out' ? fast : false
  }), [amountSat, confTarget, direction, fast, lastHop, routingPPM, selectedChannelIDs])

  const clearQuote = () => {
    setQuote(null)
    setRiskAccepted(false)
  }

  const toggleChannel = (id: string) => {
    setSelectedChannelIDs((current) => {
      const next = new Set(current)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
    clearQuote()
  }

  const autoSelectChannels = () => {
    const next = new Set<string>()
    let available = 0
    for (const channel of eligibleChannels) {
      if (channel.local_balance_sat <= 0) continue
      next.add(channelID(channel))
      available += channel.local_balance_sat
      if (amount > 0 && available >= amount) break
    }
    setSelectedChannelIDs(next)
    clearQuote()
  }

  const requestQuote = async (event: FormEvent) => {
    event.preventDefault()
    if (!amountIsValid || !liquidityIsValid) return
    setBusy(true)
    setMessage('')
    clearQuote()
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
    setBusy(true)
    setMessage('')
    try {
      await startLoopSwap({
        ...requestPayload,
        destination_address: direction === 'out' ? destination.trim() : undefined,
        outgoing_channel_ids: direction === 'out' ? Array.from(selectedChannelIDs) : undefined,
        approved_swap_fee_sat: quote.swap_fee_sat,
        approved_onchain_fee_sat: quote.onchain_fee_sat,
        approved_routing_fee_limit_sat: quote.routing_fee_limit_sat || 0,
        max_miner_fee_sat: Number(maxMinerFee),
        confirm_password: confirmPassword
      })
      setMessage(t('lightningLoop.swapStarted'))
      clearQuote()
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

  if (loading) return <LoadingState />

  if (!status) {
    return (
      <div className="space-y-6">
        <LoopHero direction={direction} status={null} onDirection={setDirection} t={t} compact />
        <div className="section-card mx-auto max-w-3xl text-center">
          <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl border border-rose-400/25 bg-rose-400/10 text-rose-200"><InfoIcon /></div>
          <h3 className="text-xl font-semibold">{t('lightningLoop.statusUnavailableTitle')}</h3>
          <p className="mx-auto mt-2 max-w-xl text-sm leading-6 text-fog/65">{t('lightningLoop.statusUnavailableBody')}</p>
          {message && <p className="mx-auto mt-3 max-w-xl rounded-xl border border-white/10 bg-ink/35 px-3 py-2 font-mono text-xs text-rose-200/80">{message}</p>}
          <button className="btn-primary mt-5" type="button" onClick={() => void load()}>{t('lightningLoop.tryAgain')}</button>
        </div>
      </div>
    )
  }

  if (!status.installed) {
    return (
      <div className="space-y-6">
        <LoopHero direction={direction} status={status} onDirection={setDirection} t={t} compact />
        <div className="section-card mx-auto max-w-3xl text-center">
          <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl border border-brass/30 bg-brass/10 text-brass"><LoopMark /></div>
          <h3 className="text-xl font-semibold">{t('lightningLoop.optionalTitle')}</h3>
          <p className="mx-auto mt-2 max-w-xl text-sm leading-6 text-fog/65">{t('lightningLoop.optionalBody')}</p>
          <a className="btn-primary mt-5 inline-flex" href="#apps">{t('lightningLoop.openStore')}</a>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6 pb-10">
      <LoopHero direction={direction} status={status} onDirection={(next) => { setDirection(next); setMessage('') }} t={t} />

      {message && (
        <div className="flex items-start gap-3 rounded-2xl border border-brass/25 bg-brass/10 px-4 py-3 text-sm text-fog shadow-panel">
          <span className="mt-0.5 text-brass"><InfoIcon /></span><span>{message}</span>
        </div>
      )}

      {!status.running ? (
        <div className="section-card mx-auto max-w-3xl text-center">
          <div className="mx-auto mb-4 h-2 w-2 rounded-full bg-amber-300 shadow-[0_0_18px_rgba(252,211,77,.8)]" />
          <h3 className="text-xl font-semibold">{t('lightningLoop.stoppedTitle')}</h3>
          <p className="mx-auto mt-2 max-w-xl text-sm leading-6 text-fog/65">{t('lightningLoop.stoppedBody')}</p>
          <a className="btn-secondary mt-5 inline-flex" href="#apps">{t('lightningLoop.openStore')}</a>
        </div>
      ) : (
        <>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <Metric icon={<PulseIcon />} label={t('lightningLoop.pending')} value={String(status.pending_count)} hint={status.pending_count ? t('lightningLoop.processing') : t('lightningLoop.noPending')} accent="glow" />
            <Metric icon={<LightningIcon />} label="Loop Out" value={`${compactSats(status.terms?.loop_out_min_sat)} — ${compactSats(status.terms?.loop_out_max_sat)}`} hint={t('lightningLoop.inboundResult')} accent="brass" />
            <Metric icon={<BitcoinIcon />} label="Loop In" value={`${compactSats(status.terms?.loop_in_min_sat)} — ${compactSats(status.terms?.loop_in_max_sat)}`} hint={t('lightningLoop.outboundResult')} accent="glow" />
            <Metric icon={<FeeIcon />} label={t('lightningLoop.networkFees')} value={feePulse?.fastest ? `${feePulse.fastest} sat/vB` : '—'} hint={feePulse?.hour ? t('lightningLoop.hourFee', { value: feePulse.hour }) : t('common.unavailable')} accent="brass" />
          </div>

          {pendingSwaps.length > 0 && (
            <div className="space-y-3">
              {pendingSwaps.map((swap) => <SwapProgressCard key={swap.id} swap={swap} sats={sats} swapTime={swapTime} t={t} />)}
            </div>
          )}

          <form className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.55fr)_minmax(340px,.8fr)]" onSubmit={requestQuote}>
            <div className="section-card space-y-7">
              <SectionHeading number="01" title={t('lightningLoop.chooseDirection')} description={direction === 'out' ? t('lightningLoop.outHelp') : t('lightningLoop.inHelp')} />
              <div className="grid gap-3 sm:grid-cols-2">
                <DirectionCard active={direction === 'out'} title="Loop Out" flow="Lightning → Bitcoin" description={t('lightningLoop.inboundResult')} icon={<LightningIcon />} onClick={() => setDirection('out')} />
                <DirectionCard active={direction === 'in'} title="Loop In" flow="Bitcoin → Lightning" description={t('lightningLoop.outboundResult')} icon={<BitcoinIcon />} onClick={() => setDirection('in')} />
              </div>

              <div className="border-t border-white/10 pt-7">
                <SectionHeading number="02" title={t('lightningLoop.setAmount')} description={t('lightningLoop.rangeHint', { min: sats(min), max: sats(max) })} />
                <div className="mt-4 max-w-xl">
                  <label className="text-xs font-medium uppercase tracking-[0.16em] text-fog/55" htmlFor="loop-amount">{t('lightningLoop.amount')}</label>
                  <div className="relative mt-2">
                    <input id="loop-amount" className="input-field pr-20 text-lg font-semibold tabular-nums" type="number" min={min || 1} max={max || undefined} required value={amountSat} onChange={(event) => { setAmountSat(event.target.value); clearQuote() }} placeholder={min ? String(min) : '0'} />
                    <span className="pointer-events-none absolute right-4 top-1/2 -translate-y-1/2 text-xs font-semibold uppercase tracking-widest text-fog/45">sats</span>
                  </div>
                  {amount > 0 && !amountIsValid && <p className="mt-2 text-xs text-amber-200">{t('lightningLoop.amountOutsideRange')}</p>}
                </div>
              </div>

              {direction === 'out' && (
                <div className="border-t border-white/10 pt-7">
                  <SectionHeading number="03" title={t('lightningLoop.selectChannels')} description={t('lightningLoop.channelHelp')} />
                  <div className="mt-4 rounded-2xl border border-white/10 bg-ink/35 p-3 sm:p-4">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <p className="text-xs uppercase tracking-[0.14em] text-fog/45">{t('lightningLoop.selectedLiquidity')}</p>
                        <p className={`mt-1 text-lg font-semibold tabular-nums ${amount > 0 && selectedLocal < amount ? 'text-amber-200' : 'text-fog'}`}>{sats(selectedLocal)} <span className="text-sm font-normal text-fog/40">/ {amount > 0 ? sats(amount) : '—'}</span></p>
                      </div>
                      <div className="flex gap-2">
                        <button className="btn-secondary px-3 py-2 text-xs" type="button" onClick={autoSelectChannels}>{t('lightningLoop.autoSelect')}</button>
                        {selectedChannelIDs.size > 0 && <button className="rounded-xl px-3 py-2 text-xs text-fog/55 hover:bg-white/5 hover:text-fog" type="button" onClick={() => { setSelectedChannelIDs(new Set()); clearQuote() }}>{t('lightningLoop.clear')}</button>}
                      </div>
                    </div>
                    <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-white/5">
                      <div className={`h-full rounded-full transition-all ${selectedLocal >= amount && amount > 0 ? 'bg-emerald-400' : 'bg-brass'}`} style={{ width: `${amount > 0 ? Math.min(100, (selectedLocal / amount) * 100) : 0}%` }} />
                    </div>
                  </div>

                  {eligibleChannels.length > 4 && <input className="input-field mt-3 py-2.5 text-sm" value={channelSearch} onChange={(event) => setChannelSearch(event.target.value)} placeholder={t('lightningLoop.searchChannels')} />}
                  <div className="mt-3 grid max-h-[390px] gap-2 overflow-y-auto pr-1 lg:grid-cols-2">
                    {visibleChannels.map((channel) => {
                      const id = channelID(channel)
                      const selected = selectedChannelIDs.has(id)
                      const capacity = Math.max(channel.capacity_sat, channel.local_balance_sat + channel.remote_balance_sat, 1)
                      const localPercent = Math.min(100, Math.max(0, (channel.local_balance_sat / capacity) * 100))
                      return (
                        <button key={channel.channel_point || id} type="button" onClick={() => toggleChannel(id)} className={`group rounded-2xl border p-4 text-left transition ${selected ? 'border-brass/55 bg-brass/10 shadow-[0_0_0_1px_rgba(245,158,11,.08)]' : 'border-white/10 bg-white/[0.025] hover:border-white/20 hover:bg-white/[0.045]'}`}>
                          <div className="flex items-start justify-between gap-3">
                            <div className="min-w-0"><p className="truncate text-sm font-semibold">{channel.peer_alias || shorten(channel.remote_pubkey)}</p><p className="mt-1 truncate font-mono text-[10px] text-fog/38">{id}</p></div>
                            <span className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border transition ${selected ? 'border-brass bg-brass text-ink' : 'border-white/20 text-transparent group-hover:border-white/40'}`}><CheckIcon /></span>
                          </div>
                          <div className="mt-4 flex h-1.5 overflow-hidden rounded-full bg-glow/30"><span className="bg-brass" style={{ width: `${localPercent}%` }} /></div>
                          <div className="mt-2 flex justify-between text-[11px]"><span className="text-brass">{t('lightningLoop.local')} {compactSats(channel.local_balance_sat)}</span><span className="text-glow">{t('lightningLoop.remote')} {compactSats(channel.remote_balance_sat)}</span></div>
                        </button>
                      )
                    })}
                    {visibleChannels.length === 0 && <div className="rounded-2xl border border-dashed border-white/10 p-6 text-center text-sm text-fog/50 lg:col-span-2">{t('lightningLoop.noEligibleChannels')}</div>}
                  </div>
                  {amount > 0 && selectedChannelIDs.size > 0 && selectedLocal < amount && <p className="mt-3 text-xs text-amber-200">{t('lightningLoop.insufficientLiquidity', { missing: sats(amount - selectedLocal) })}</p>}
                </div>
              )}

              {direction === 'in' && (
                <div className="border-t border-white/10 pt-7">
                  <SectionHeading number="03" title={t('lightningLoop.selectDestination')} description={t('lightningLoop.destinationChannelHelp')} />
                  <button type="button" onClick={() => { setLastHop(''); clearQuote() }} className={`mt-4 flex w-full items-center justify-between gap-4 rounded-2xl border p-4 text-left transition ${!lastHop.trim() ? 'border-glow/50 bg-glow/10 shadow-[inset_0_0_0_1px_rgba(34,211,238,.05)]' : 'border-white/10 bg-white/[0.025] hover:border-white/20 hover:bg-white/[0.045]'}`}>
                    <span className="flex min-w-0 items-center gap-3">
                      <span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-full border ${!lastHop.trim() ? 'border-glow/30 bg-glow/15 text-glow' : 'border-white/10 bg-white/5 text-fog/45'}`}><RouteIcon /></span>
                      <span className="min-w-0"><strong className="block text-sm">{t('lightningLoop.automaticDestination')}</strong><span className="mt-1 block text-xs leading-5 text-fog/50">{t('lightningLoop.automaticDestinationHint')}</span></span>
                    </span>
                    <span className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border ${!lastHop.trim() ? 'border-glow bg-glow text-ink' : 'border-white/20 text-transparent'}`}><CheckIcon /></span>
                  </button>

                  {loopInDestinations.length > 4 && <input className="input-field mt-3 py-2.5 text-sm" value={channelSearch} onChange={(event) => setChannelSearch(event.target.value)} placeholder={t('lightningLoop.searchDestinations')} />}
                  <div className="mt-3 grid max-h-[390px] gap-2 overflow-y-auto pr-1 lg:grid-cols-2">
                    {visibleLoopInDestinations.map((destinationOption) => {
                      const selected = lastHop.trim() === destinationOption.pubkey
                      const insufficient = amount > 0 && destinationOption.remoteBalanceSat < amount
                      const total = Math.max(1, destinationOption.localBalanceSat + destinationOption.remoteBalanceSat)
                      const remotePercent = Math.min(100, Math.max(0, destinationOption.remoteBalanceSat / total * 100))
                      return (
                        <button key={destinationOption.pubkey} type="button" disabled={insufficient} onClick={() => { setLastHop(destinationOption.pubkey); clearQuote() }} className={`group rounded-2xl border p-4 text-left transition ${selected ? 'border-glow/55 bg-glow/10 shadow-[0_0_0_1px_rgba(34,211,238,.06)]' : insufficient ? 'cursor-not-allowed border-white/[0.06] bg-white/[0.015] opacity-45' : 'border-white/10 bg-white/[0.025] hover:border-white/20 hover:bg-white/[0.045]'}`}>
                          <div className="flex items-start justify-between gap-3">
                            <div className="min-w-0"><p className="truncate text-sm font-semibold">{destinationOption.alias || shorten(destinationOption.pubkey)}</p><p className="mt-1 truncate font-mono text-[10px] text-fog/38">{shorten(destinationOption.pubkey)}</p></div>
                            <span className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border transition ${selected ? 'border-glow bg-glow text-ink' : 'border-white/20 text-transparent group-hover:border-white/40'}`}><CheckIcon /></span>
                          </div>
                          <div className="mt-4 h-1.5 overflow-hidden rounded-full bg-white/5"><div className="h-full rounded-full bg-glow" style={{ width: `${remotePercent}%` }} /></div>
                          <div className="mt-2 flex items-start justify-between gap-2 text-[11px]"><span className="text-glow">{t('lightningLoop.inboundAvailable')} {compactSats(destinationOption.remoteBalanceSat)}</span><span className="text-right text-fog/38">{t('lightningLoop.channelCount', { count: destinationOption.channelIDs.length })}</span></div>
                          {insufficient && <p className="mt-2 text-[10px] text-amber-200/80">{t('lightningLoop.insufficientInboundDestination')}</p>}
                        </button>
                      )
                    })}
                    {visibleLoopInDestinations.length === 0 && <div className="rounded-2xl border border-dashed border-white/10 p-6 text-center text-sm text-fog/50 lg:col-span-2">{t('lightningLoop.noLoopInDestinations')}</div>}
                  </div>
                  {selectedLoopInDestinationIsInsufficient && <p className="mt-3 text-xs text-amber-200">{t('lightningLoop.selectDestinationWithLiquidity')}</p>}
                </div>
              )}

              <details className="group border-t border-white/10 pt-6">
                <summary className="flex cursor-pointer list-none items-center justify-between text-sm font-semibold">
                  <span className="flex items-center gap-2"><SettingsIcon /> {t('lightningLoop.advanced')}</span>
                  <span className="text-fog/40 transition group-open:rotate-180">⌄</span>
                </summary>
                <div className="mt-5 grid gap-4 md:grid-cols-2">
                  <Field label={t('lightningLoop.confTarget')}><input className="input-field" type="number" min="1" max="2016" required value={confTarget} onChange={(event) => { setConfTarget(event.target.value); clearQuote() }} /></Field>
                  {direction === 'out' && <Field label={t('lightningLoop.routingPPM')}><input className="input-field" type="number" min="0" max="100000" value={routingPPM} onChange={(event) => { setRoutingPPM(event.target.value); clearQuote() }} /></Field>}
                  {direction === 'out' && <Field className="md:col-span-2" label={t('lightningLoop.destination')} hint={t('lightningLoop.destinationHint')}><input className="input-field font-mono text-xs" value={destination} onChange={(event) => setDestination(event.target.value)} /></Field>}
                  {direction === 'in' && <Field className="md:col-span-2" label={t('lightningLoop.lastHop')} hint={t('lightningLoop.lastHopHint')}><input className="input-field font-mono text-xs" value={lastHop} onChange={(event) => { setLastHop(event.target.value); clearQuote() }} /></Field>}
                  {direction === 'out' && <label className="flex items-start gap-3 rounded-2xl border border-white/10 bg-white/[0.025] p-4 text-sm md:col-span-2"><input className="mt-1 accent-amber-400" type="checkbox" checked={fast} onChange={(event) => { setFast(event.target.checked); clearQuote() }} /><span><strong className="block text-fog">{t('lightningLoop.fastMode')}</strong><span className="mt-1 block text-xs leading-5 text-fog/55">{t('lightningLoop.fast')}</span></span></label>}
                </div>
              </details>
            </div>

            <aside className="section-card xl:sticky xl:top-5">
              <div className="flex items-center justify-between gap-3">
                <div><p className="text-[10px] font-semibold uppercase tracking-[0.2em] text-brass">{t('lightningLoop.review')}</p><h3 className="mt-1 text-xl font-semibold">{t('lightningLoop.executionPlan')}</h3></div>
                <span className="flex h-10 w-10 items-center justify-center rounded-2xl border border-white/10 bg-white/5 text-brass"><ShieldIcon /></span>
              </div>

              <div className="mt-6 rounded-2xl border border-white/10 bg-ink/35 p-4">
                <FlowSummary direction={direction} />
                <div className={`mt-5 grid gap-3 border-t border-white/10 pt-4 text-sm ${direction === 'in' ? 'grid-cols-3' : 'grid-cols-2'}`}>
                  <div><p className="text-[10px] uppercase tracking-wider text-fog/40">{t('lightningLoop.amount')}</p><p className="mt-1 font-semibold tabular-nums">{amount > 0 ? sats(amount) : '—'}</p></div>
                  <div><p className="text-[10px] uppercase tracking-wider text-fog/40">{direction === 'out' ? t('lightningLoop.channelsSelected') : t('lightningLoop.loopInDestination')}</p><p className="mt-1 truncate font-semibold">{direction === 'out' ? selectedChannelIDs.size : selectedLoopInDestination?.alias || (lastHop.trim() ? shorten(lastHop.trim()) : t('lightningLoop.automaticShort'))}</p></div>
                  {direction === 'in' && <div><p className="text-[10px] uppercase tracking-wider text-fog/40">{t('lightningLoop.confirmations')}</p><p className="mt-1 font-semibold">{confTarget}</p></div>}
                </div>
              </div>

              {!quote ? (
                <div className="mt-5">
                  <div className="rounded-2xl border border-dashed border-white/10 px-4 py-6 text-center">
                    <div className="mx-auto flex h-10 w-10 items-center justify-center rounded-full bg-white/5 text-fog/35"><FeeIcon /></div>
                    <p className="mt-3 text-sm font-medium">{t('lightningLoop.waitingQuote')}</p>
                    <p className="mt-1 text-xs leading-5 text-fog/45">{t('lightningLoop.waitingQuoteBody')}</p>
                  </div>
                  <button className="btn-primary mt-4 w-full justify-center" type="submit" disabled={busy || !amountIsValid || !liquidityIsValid}>{busy ? t('common.loading') : t('lightningLoop.getQuote')}</button>
                  {!liquidityIsValid && direction === 'out' && amount > 0 && <p className="mt-2 text-center text-[11px] text-amber-200/80">{t('lightningLoop.selectEnoughFirst')}</p>}
                  {!liquidityIsValid && direction === 'in' && amount > 0 && <p className="mt-2 text-center text-[11px] text-amber-200/80">{t('lightningLoop.selectDestinationWithLiquidity')}</p>}
                </div>
              ) : (
                <QuotePanel quote={quote} direction={direction} maxMinerFee={maxMinerFee} setMaxMinerFee={setMaxMinerFee} riskAccepted={riskAccepted} setRiskAccepted={setRiskAccepted} busy={busy} execute={execute} sats={sats} t={t} />
              )}

              <div className="mt-5 flex items-center gap-2 border-t border-white/10 pt-4 text-[11px] leading-4 text-fog/45"><ShieldIcon /><span>{t('lightningLoop.manualGuard')}</span></div>
            </aside>
          </form>

          <div className="section-card overflow-hidden p-0">
            <div className="flex flex-wrap items-end justify-between gap-3 border-b border-white/10 px-5 py-5 sm:px-6">
              <div><p className="text-[10px] uppercase tracking-[0.2em] text-brass">{t('lightningLoop.activity')}</p><h3 className="mt-1 text-lg font-semibold">{t('lightningLoop.history')}</h3></div>
              <p className="text-xs text-fog/40">{t('lightningLoop.swapCount', { count: swaps.length })}</p>
            </div>
            {swaps.length === 0 ? (
              <div className="px-6 py-12 text-center"><div className="mx-auto flex h-12 w-12 items-center justify-center rounded-2xl border border-white/10 bg-white/5 text-fog/25"><LoopMark /></div><p className="mt-3 text-sm text-fog/50">{t('lightningLoop.noSwaps')}</p></div>
            ) : (
              <div className="divide-y divide-white/5">
                {swaps.map((swap) => {
                  const normalizedState = String(swap.state || '').toUpperCase()
                  const normalizedFailure = String(swap.failure_reason || '').toUpperCase()
                  const pending = isPendingState(normalizedState)
                  const success = normalizedState === 'SUCCESS'
                  const stateLabel = loopStateKeys[normalizedState] ? t(loopStateKeys[normalizedState]) : normalizedState.split('_').join(' ')
                  const failureReason = ['FAILURE_REASON_NONE', 'NONE'].includes(normalizedFailure) ? '' : swap.failure_reason
                  const out = String(swap.type).toUpperCase().includes('OUT')
                  const totalCost = swap.cost_server_sat + swap.cost_onchain_sat + swap.cost_offchain_sat
                  const costPPM = swap.amount_sat > 0 ? Math.round((totalCost * 1_000_000) / swap.amount_sat) : 0
                  return (
                    <div className="grid gap-4 px-5 py-4 transition hover:bg-white/[0.025] sm:grid-cols-[minmax(220px,1.2fr)_1fr_1fr_auto] sm:items-center sm:px-6" key={swap.id}>
                      <div className="flex items-center gap-3"><span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border ${out ? 'border-brass/25 bg-brass/10 text-brass' : 'border-glow/25 bg-glow/10 text-glow'}`}>{out ? <LightningIcon /> : <BitcoinIcon />}</span><div><p className="text-sm font-semibold">{out ? 'Loop Out' : 'Loop In'}</p><p className="mt-0.5 text-xs text-fog/40">{swapTime(swap.initiation_time)}</p></div></div>
                      <div><p className="text-[10px] uppercase tracking-wider text-fog/35">{t('lightningLoop.amount')}</p><p className="mt-1 text-sm font-semibold tabular-nums">{sats(swap.amount_sat)}</p></div>
                      <div><p className="text-[10px] uppercase tracking-wider text-fog/35">{t('lightningLoop.cost')}</p><p className="mt-1 text-sm tabular-nums">{sats(totalCost)}</p><p className="mt-0.5 text-[11px] tabular-nums text-fog/40">{costPPM.toLocaleString(locale)} ppm</p></div>
                      <div className="sm:text-right"><span className={`inline-flex rounded-full border px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider ${pending ? 'border-amber-400/25 bg-amber-400/10 text-amber-200' : success ? 'border-emerald-400/25 bg-emerald-400/10 text-emerald-200' : 'border-rose-400/25 bg-rose-400/10 text-rose-200'}`}>{stateLabel}</span>{pending && <p className="mt-1 max-w-xs text-xs text-fog/45">{t('lightningLoop.pendingSwapHint')}</p>}{failureReason && <p className="mt-1 max-w-xs text-xs text-rose-200/70">{failureReason}</p>}</div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </>
      )}

      {reauthOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/85 p-4 backdrop-blur-sm">
          <form className="section-card w-full max-w-md" onSubmit={submitPassword}>
            <span className="flex h-12 w-12 items-center justify-center rounded-2xl border border-brass/25 bg-brass/10 text-brass"><ShieldIcon /></span>
            <h3 className="mt-4 text-xl font-semibold">{t('lightningLoop.reauthTitle')}</h3>
            <p className="mt-2 text-sm leading-6 text-fog/65">{t('lightningLoop.reauthBody')}</p>
            <input className="input-field mt-5" type="password" autoFocus required value={password} onChange={(event) => setPassword(event.target.value)} />
            <div className="mt-5 flex justify-end gap-2"><button className="btn-secondary" type="button" onClick={() => { setReauthOpen(false); setPassword('') }}>{t('common.cancel')}</button><button className="btn-primary" disabled={busy} type="submit">{t('lightningLoop.execute')}</button></div>
          </form>
        </div>
      )}
    </div>
  )
}

function LoopHero({ direction, status, onDirection, t, compact = false }: { direction: Direction; status: LoopStatus | null; onDirection: (direction: Direction) => void; t: any; compact?: boolean }) {
  return (
    <section className="loop-hero rounded-3xl border border-white/10 p-5 shadow-panel sm:p-7">
      <div className="relative z-10 grid items-center gap-7 lg:grid-cols-[1fr_1.05fr]">
        <div>
          <div className="flex flex-wrap items-center gap-2"><span className="text-[10px] font-semibold uppercase tracking-[0.24em] text-brass">Lightning Labs · {status?.network || 'mainnet'}</span>{status?.installed && <span className={`rounded-full border px-2.5 py-1 text-[9px] font-semibold uppercase tracking-wider ${status.running ? 'border-emerald-400/25 bg-emerald-400/10 text-emerald-200' : 'border-amber-400/25 bg-amber-400/10 text-amber-200'}`}>{status.running ? t('common.running') : t('common.stopped')} {status.version ? `· ${status.version}` : ''}</span>}</div>
          <h2 className="mt-3 text-3xl font-semibold tracking-tight sm:text-4xl">{t('lightningLoop.heroTitle')}</h2>
          <p className="mt-3 max-w-xl text-sm leading-6 text-fog/65 sm:text-base">{t('lightningLoop.heroBody')}</p>
        </div>
        {!compact && (
          <div className="rounded-3xl border border-white/10 bg-ink/40 p-4 backdrop-blur sm:p-5">
            <FlowSummary direction={direction} large />
            <div className="mt-4 grid grid-cols-2 gap-2">
              <button className={`rounded-xl border px-3 py-2.5 text-xs font-semibold transition ${direction === 'out' ? 'border-brass/40 bg-brass/15 text-brass' : 'border-white/10 bg-white/[0.025] text-fog/50 hover:text-fog'}`} type="button" onClick={() => onDirection('out')}>Loop Out</button>
              <button className={`rounded-xl border px-3 py-2.5 text-xs font-semibold transition ${direction === 'in' ? 'border-glow/40 bg-glow/15 text-glow' : 'border-white/10 bg-white/[0.025] text-fog/50 hover:text-fog'}`} type="button" onClick={() => onDirection('in')}>Loop In</button>
            </div>
          </div>
        )}
      </div>
    </section>
  )
}

function FlowSummary({ direction, large = false }: { direction: Direction; large?: boolean }) {
  return (
    <div className={`grid grid-cols-[auto_1fr_auto_1fr_auto] items-center ${large ? 'gap-3' : 'gap-2'}`}>
      <FlowNode label="Lightning" tone="brass"><LightningIcon /></FlowNode>
      <FlowLine reverse={direction === 'in'} tone="brass" />
      <div className={`${large ? 'h-12 w-12' : 'h-10 w-10'} flex items-center justify-center rounded-full border border-white/10 bg-white/[0.035] text-fog/70`}><LoopMark /></div>
      <FlowLine reverse={direction === 'in'} tone="glow" />
      <FlowNode label="Bitcoin" tone="glow"><BitcoinIcon /></FlowNode>
    </div>
  )
}

function FlowNode({ label, tone, children }: { label: string; tone: 'brass' | 'glow'; children: ReactNode }) {
  return <div className="text-center"><div className={`mx-auto flex h-10 w-10 items-center justify-center rounded-xl border ${tone === 'brass' ? 'border-brass/30 bg-brass/10 text-brass' : 'border-glow/30 bg-glow/10 text-glow'}`}>{children}</div><p className="mt-1.5 text-[9px] font-semibold uppercase tracking-wider text-fog/45">{label}</p></div>
}

function FlowLine({ reverse, tone }: { reverse: boolean; tone: 'brass' | 'glow' }) {
  return <div className={`relative h-px ${tone === 'brass' ? 'bg-gradient-to-r from-brass/15 to-brass/80 text-brass' : 'bg-gradient-to-r from-glow/15 to-glow/80 text-glow'} ${reverse ? 'rotate-180' : ''}`}><svg aria-hidden="true" className="absolute -right-0.5 top-1/2 h-2.5 w-2.5 -translate-y-1/2" viewBox="0 0 10 10" fill="none"><path d="m3 2 3 3-3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg></div>
}

function Metric({ icon, label, value, hint, accent }: { icon: ReactNode; label: string; value: string; hint: string; accent: 'brass' | 'glow' }) {
  return <div className="section-card flex items-start gap-3 p-4"><span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border ${accent === 'brass' ? 'border-brass/20 bg-brass/10 text-brass' : 'border-glow/20 bg-glow/10 text-glow'}`}>{icon}</span><div className="min-w-0"><p className="text-[10px] uppercase tracking-[0.15em] text-fog/40">{label}</p><p className="mt-1 truncate text-sm font-semibold tabular-nums">{value}</p><p className="mt-1 truncate text-[10px] text-fog/38">{hint}</p></div></div>
}

function DirectionCard({ active, title, flow, description, icon, onClick }: { active: boolean; title: string; flow: string; description: string; icon: ReactNode; onClick: () => void }) {
  return <button type="button" onClick={onClick} className={`rounded-2xl border p-4 text-left transition ${active ? 'border-brass/50 bg-gradient-to-br from-brass/15 to-transparent shadow-[inset_0_0_0_1px_rgba(245,158,11,.05)]' : 'border-white/10 bg-white/[0.025] hover:border-white/20 hover:bg-white/[0.04]'}`}><div className="flex items-start justify-between gap-3"><span className={`flex h-10 w-10 items-center justify-center rounded-xl ${active ? 'bg-brass text-ink' : 'bg-white/5 text-fog/50'}`}>{icon}</span><span className={`mt-1 flex h-5 w-5 items-center justify-center rounded-full border ${active ? 'border-brass bg-brass text-ink' : 'border-white/20 text-transparent'}`}><CheckIcon /></span></div><p className="mt-4 font-semibold">{title}</p><p className="mt-1 text-xs font-medium text-brass">{flow}</p><p className="mt-2 text-xs leading-5 text-fog/50">{description}</p></button>
}

function SectionHeading({ number, title, description }: { number: string; title: string; description: string }) {
  return <div className="flex gap-3"><span className="mt-0.5 text-[10px] font-semibold tracking-widest text-brass">{number}</span><div><h3 className="text-lg font-semibold">{title}</h3><p className="mt-1 text-sm leading-5 text-fog/55">{description}</p></div></div>
}

function Field({ label, hint, className = '', children }: { label: string; hint?: string; className?: string; children: ReactNode }) {
  return <label className={`block ${className}`}><span className="text-xs font-medium text-fog/65">{label}</span><div className="mt-2">{children}</div>{hint && <span className="mt-1.5 block text-[11px] leading-4 text-fog/40">{hint}</span>}</label>
}

function QuotePanel({ quote, direction, maxMinerFee, setMaxMinerFee, riskAccepted, setRiskAccepted, busy, execute, sats, t }: { quote: LoopQuote; direction: Direction; maxMinerFee: string; setMaxMinerFee: (value: string) => void; riskAccepted: boolean; setRiskAccepted: (value: boolean) => void; busy: boolean; execute: () => void; sats: (value?: number) => string; t: any }) {
  const service = Math.max(0, quote.swap_fee_sat || 0)
  const chain = Math.max(0, quote.onchain_fee_sat || 0)
  const routing = (quote.routing_fee_limit_sat || 0) + (quote.prepay_routing_limit_sat || 0)
  const estimatedRouting = Math.max(0, quote.estimated_routing_fee_sat || 0)
  const chartTotal = Math.max(1, service + chain + estimatedRouting)
  const estimatedAllIn = quote.routing_estimate_available ? Math.max(0, quote.estimated_all_in_fee_sat || service + chain + estimatedRouting) : service + chain
  const servicePPM = quote.amount_sat > 0 ? Math.round(service * 1_000_000 / quote.amount_sat) : 0
  const chainPPM = quote.amount_sat > 0 ? Math.round(chain * 1_000_000 / quote.amount_sat) : 0
  const estimatedRoutingPPM = quote.amount_sat > 0 ? Math.round(estimatedRouting * 1_000_000 / quote.amount_sat) : 0
  const estimatedAllInPPM = quote.amount_sat > 0 ? Math.round(estimatedAllIn * 1_000_000 / quote.amount_sat) : 0
  const routingBudgetPPM = quote.amount_sat > 0 ? Math.round(routing * 1_000_000 / quote.amount_sat) : 0
  const reservedAllIn = service + chain + routing
  const reservedAllInPPM = quote.amount_sat > 0 ? Math.round(reservedAllIn * 1_000_000 / quote.amount_sat) : 0
  const routingOverBudget = Math.max(0, estimatedRouting - routing)
  const estimateHint = quote.routing_estimate_source === 'history'
    ? t('lightningLoop.routingHistoryHint', { count: quote.routing_estimate_samples || 1 })
    : quote.routing_estimate_source === 'invoice_routes'
      ? t('lightningLoop.routingInvoiceHint', { count: quote.routing_estimate_samples || 2 })
      : t('lightningLoop.routingEstimateHint')
  return (
    <div className="mt-5 space-y-4">
      <div className="rounded-2xl border border-brass/25 bg-brass/[0.07] p-4">
        <div className="flex items-start justify-between gap-3"><div><p className="text-[10px] uppercase tracking-wider text-fog/45">{t(quote.routing_estimate_available ? 'lightningLoop.estimatedAllIn' : 'lightningLoop.totalEstimateUnavailable')}</p>{quote.routing_estimate_available ? <><p className="mt-1 text-2xl font-semibold tabular-nums">{sats(estimatedAllIn)}</p><p className="mt-0.5 text-[11px] tabular-nums text-fog/45">{estimatedAllInPPM.toLocaleString()} ppm</p></> : <p className="mt-1 text-2xl font-semibold text-fog/35">—</p>}</div><span className={`rounded-full border px-2 py-1 text-[9px] font-semibold uppercase tracking-wider ${quote.routing_estimate_available ? 'border-emerald-400/25 bg-emerald-400/10 text-emerald-200' : 'border-amber-400/25 bg-amber-400/10 text-amber-200'}`}>{t(quote.routing_estimate_available ? 'lightningLoop.liveQuote' : 'lightningLoop.partialQuote')}</span></div>
        <div className="mt-4 flex h-2 overflow-hidden rounded-full bg-white/5"><span className="bg-brass" style={{ width: `${(service / chartTotal) * 100}%` }} /><span className="bg-glow" style={{ width: `${(chain / chartTotal) * 100}%` }} />{quote.routing_estimate_available && <span className="bg-emerald-400" style={{ width: `${(estimatedRouting / chartTotal) * 100}%` }} />}</div>
        <div className="mt-3 space-y-2 text-xs"><div className="flex justify-between"><span className="flex items-center gap-2 text-fog/55"><i className="h-1.5 w-1.5 rounded-full bg-brass" />{t('lightningLoop.serviceFee')}</span><strong className="text-right"><span className="block">{sats(service)}</span><span className="block text-[10px] font-normal text-fog/40">{servicePPM.toLocaleString()} ppm</span></strong></div><div className="flex justify-between"><span className="flex items-center gap-2 text-fog/55"><i className="h-1.5 w-1.5 rounded-full bg-glow" />{t('lightningLoop.onchainEstimate')}</span><strong className="text-right"><span className="block">{sats(chain)}</span><span className="block text-[10px] font-normal text-fog/40">{chainPPM.toLocaleString()} ppm</span></strong></div>{direction === 'out' && quote.routing_estimate_available && <div className="flex justify-between"><span className="flex items-center gap-2 text-fog/55"><i className="h-1.5 w-1.5 rounded-full bg-emerald-400" />{t('lightningLoop.routingEstimate')}</span><strong className="text-right"><span className="block">{sats(estimatedRouting)}</span><span className="block text-[10px] font-normal text-fog/40">{estimatedRoutingPPM.toLocaleString()} ppm</span></strong></div>}{direction === 'out' && <div className="flex justify-between border-t border-white/10 pt-2"><span className="text-fog/55">{t('lightningLoop.routingBudget')}</span><strong className="text-right"><span className="block">{sats(routing)}</span><span className="block text-[10px] font-normal text-fog/40">{routingBudgetPPM.toLocaleString()} ppm</span></strong></div>}</div>
        {direction === 'out' && quote.routing_estimate_available && <p className="mt-3 text-[10px] leading-4 text-fog/40">{estimateHint}</p>}
        {direction === 'out' && quote.routing_estimate_available && routingOverBudget > 0 && <p className="mt-3 rounded-xl border border-amber-400/25 bg-amber-400/[0.07] p-3 text-[10px] leading-4 text-amber-100/80">{t('lightningLoop.routingOverBudget', { value: sats(routingOverBudget) })}</p>}
        {direction === 'out' && !quote.routing_estimate_available && <div className="mt-3 rounded-xl border border-amber-400/20 bg-amber-400/[0.06] p-3"><p className="text-[10px] leading-4 text-amber-100/70">{t('lightningLoop.routingEstimateUnavailable')}</p><p className="mt-1.5 text-[11px] font-semibold tabular-nums text-amber-100">{t('lightningLoop.withRoutingReserve', { value: sats(reservedAllIn), ppm: reservedAllInPPM.toLocaleString() })}</p></div>}
        <p className="mt-3 text-[10px] text-fog/40">{t('lightningLoop.expires')}: {new Date(quote.expires_at).toLocaleTimeString()}</p>
      </div>
      <Field label={t('lightningLoop.maxMiner')} hint={t('lightningLoop.maxMinerWarning')}><input className="input-field" type="number" min={quote.onchain_fee_sat} required value={maxMinerFee} onChange={(event) => setMaxMinerFee(event.target.value)} /></Field>
      <label className="flex items-start gap-3 rounded-2xl border border-white/10 bg-white/[0.025] p-3 text-xs leading-5 text-fog/65"><input className="mt-1 accent-amber-400" type="checkbox" checked={riskAccepted} onChange={(event) => setRiskAccepted(event.target.checked)} /><span>{t('lightningLoop.confirmRisk')}</span></label>
      <button className="btn-primary w-full justify-center" type="button" disabled={busy || !riskAccepted || Number(maxMinerFee) < quote.onchain_fee_sat} onClick={() => execute()}>{busy ? t('common.loading') : t('lightningLoop.execute')}</button>
    </div>
  )
}

function SwapProgressCard({ swap, sats, swapTime, t }: { swap: LoopSwap; sats: (value?: number) => string; swapTime: (value: number) => string; t: any }) {
  const state = String(swap.state || '').toUpperCase()
  const out = String(swap.type || '').toUpperCase().includes('OUT')
  const stages = out ? [
    { label: t('lightningLoop.outProgressAccepted'), detail: t('lightningLoop.outProgressAcceptedDetail') },
    { label: t('lightningLoop.outProgressHtlc'), detail: t('lightningLoop.outProgressHtlcDetail') },
    { label: t('lightningLoop.outProgressSweep'), detail: t('lightningLoop.outProgressSweepDetail') },
    { label: t('lightningLoop.progressComplete'), detail: t('lightningLoop.outProgressCompleteDetail') }
  ] : [
    { label: t('lightningLoop.inProgressPreparing'), detail: t('lightningLoop.inProgressPreparingDetail') },
    { label: t('lightningLoop.inProgressHtlc'), detail: t('lightningLoop.inProgressHtlcDetail') },
    { label: t('lightningLoop.inProgressPayment'), detail: t('lightningLoop.inProgressPaymentDetail') },
    { label: t('lightningLoop.progressComplete'), detail: t('lightningLoop.inProgressCompleteDetail') }
  ]
  const stageIndex = out
    ? ({ INITIATED: 1, PREIMAGE_REVEALED: 2, SUCCESS: 3 }[state] ?? 0)
    : ({ INITIATED: 0, HTLC_PUBLISHED: 1, INVOICE_SETTLED: 2, SUCCESS: 3 }[state] ?? 0)
  const startedMs = swap.initiation_time > 10_000_000_000_000 ? Math.floor(swap.initiation_time / 1_000_000) : swap.initiation_time * 1000
  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - startedMs) / 60_000))
  const elapsed = elapsedMinutes < 60
    ? t('lightningLoop.elapsedMinutes', { count: elapsedMinutes })
    : t('lightningLoop.elapsedHours', { hours: Math.floor(elapsedMinutes / 60), minutes: elapsedMinutes % 60 })
  const progress = (stageIndex / (stages.length - 1)) * 100

  return (
    <section className="overflow-hidden rounded-3xl border border-amber-400/25 bg-gradient-to-br from-amber-400/[0.09] via-ink/55 to-glow/[0.05] shadow-panel">
      <div className="flex flex-wrap items-start justify-between gap-4 border-b border-white/10 px-5 py-4 sm:px-6">
        <div className="flex items-start gap-3">
          <span className="relative mt-0.5 flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl border border-amber-400/30 bg-amber-400/10 text-amber-200">
            <span className="absolute inset-0 animate-ping rounded-2xl border border-amber-300/20" />
            {out ? <LightningIcon /> : <BitcoinIcon />}
          </span>
          <div><p className="text-[10px] font-semibold uppercase tracking-[0.2em] text-amber-200/70">{t('lightningLoop.activeOperation')}</p><h3 className="mt-1 text-lg font-semibold">{out ? 'Loop Out' : 'Loop In'} · {sats(swap.amount_sat)}</h3><p className="mt-1 text-xs text-fog/45">{t('lightningLoop.startedAt', { value: swapTime(swap.initiation_time) })}</p></div>
        </div>
        <div className="text-left sm:text-right"><span className="inline-flex items-center gap-2 rounded-full border border-amber-400/25 bg-amber-400/10 px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wider text-amber-200"><i className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-300" />{t('lightningLoop.progressStage', { current: stageIndex + 1, total: stages.length })}</span><p className="mt-1.5 text-xs tabular-nums text-fog/45">{elapsed}</p></div>
      </div>
      <div className="px-5 py-5 sm:px-6">
        <div className="relative">
          <div className="absolute left-[6%] right-[6%] top-3 h-0.5 bg-white/10"><div className="h-full bg-gradient-to-r from-emerald-400 to-amber-300 transition-all duration-700" style={{ width: `${progress}%` }} /></div>
          <div className="relative grid grid-cols-4 gap-2">
            {stages.map((stage, index) => {
              const done = index < stageIndex
              const current = index === stageIndex
              return <div className="min-w-0 text-center" key={stage.label}><span className={`mx-auto flex h-6 w-6 items-center justify-center rounded-full border text-[10px] font-bold ${done ? 'border-emerald-400 bg-emerald-400 text-ink' : current ? 'border-amber-300 bg-amber-300 text-ink shadow-[0_0_18px_rgba(252,211,77,.45)]' : 'border-white/15 bg-ink text-fog/30'}`}>{done ? '✓' : index + 1}</span><p className={`mt-2 truncate text-[10px] font-medium sm:text-xs ${current ? 'text-amber-100' : done ? 'text-emerald-200/70' : 'text-fog/30'}`}>{stage.label}</p></div>
            })}
          </div>
        </div>
        <div className="mt-5 flex items-start gap-3 rounded-2xl border border-white/10 bg-ink/45 p-4"><span className="mt-1 h-2 w-2 shrink-0 animate-pulse rounded-full bg-amber-300" /><div><p className="text-sm font-semibold text-amber-100">{stages[stageIndex].label}</p><p className="mt-1 text-xs leading-5 text-fog/60">{stages[stageIndex].detail}</p><p className="mt-2 text-[10px] uppercase tracking-wider text-fog/35">{t('lightningLoop.lastLoopEvent', { value: swapTime(swap.last_update_time) })} · {t('lightningLoop.autoRefresh')}</p></div></div>
      </div>
    </section>
  )
}

function LoadingState() {
  return <div className="space-y-5"><div className="loop-hero h-52 animate-pulse rounded-3xl border border-white/10" /><div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">{[0, 1, 2, 3].map((item) => <div className="section-card h-24 animate-pulse" key={item} />)}</div><div className="section-card h-96 animate-pulse" /></div>
}

const shorten = (value: string) => value ? `${value.slice(0, 8)}…${value.slice(-6)}` : '—'

function LightningIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="m13.2 2-8 11h6l-.4 9 8-12h-6.1l.5-8Z" fill="currentColor" /></svg> }
function BitcoinIcon() { return <span aria-hidden="true" className="block text-[20px] font-semibold leading-none">₿</span> }
function LoopMark() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="M5 8h13m0 0-3-3m3 3-3 3M19 16H6m0 0 3 3m-3-3 3-3" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg> }
function RouteIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><circle cx="6" cy="17" r="2" stroke="currentColor" strokeWidth="1.7" /><circle cx="18" cy="7" r="2" stroke="currentColor" strokeWidth="1.7" /><path d="M8 17h2.5c4.5 0 2-10 5.5-10" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeDasharray="2.2 2.2" /></svg> }
function PulseIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="M3 12h4l2-6 4 12 2-6h6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg> }
function FeeIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="M4 17.5V14m5 3.5V10m5 7.5V7m5 10.5V3.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg> }
function InfoIcon() { return <svg aria-hidden="true" className="h-4 w-4" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="1.8" /><path d="M12 10v6m0-9h.01" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg> }
function CheckIcon() { return <svg aria-hidden="true" className="h-3 w-3" viewBox="0 0 16 16" fill="none"><path d="m3 8.5 3 3 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg> }
function SettingsIcon() { return <svg aria-hidden="true" className="h-4 w-4 text-fog/45" viewBox="0 0 24 24" fill="none"><path d="M4 7h10m4 0h2M4 17h2m4 0h10M14 4v6M6 14v6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg> }
function ShieldIcon() { return <svg aria-hidden="true" className="h-5 w-5 shrink-0" viewBox="0 0 24 24" fill="none"><path d="M12 3 5 6v5c0 4.5 2.8 8.1 7 10 4.2-1.9 7-5.5 7-10V6l-7-3Z" stroke="currentColor" strokeWidth="1.7" strokeLinejoin="round" /><path d="m9 12 2 2 4-4" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg> }
