import { FormEvent, ReactNode, useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  APIError,
  cancelLoopOutBRLNJob,
  createLoopOutBRLNJob,
  getLnChannels,
  getLoopOutBRLNJob,
  getLoopOutBRLNJobs,
  getLoopOutBRLNStatus,
  pauseLoopOutBRLNJob,
  previewLoopOutBRLN,
  resumeLoopOutBRLNJob,
  type LoopOutBRLNJob,
  type LoopOutBRLNJobDetail,
  type LoopOutBRLNPreview,
  type LoopOutBRLNRequest
} from '../api'
import loopOutBRLNIcon from '../assets/apps/loopout-brln.png'
import { getLocale } from '../i18n'

type SourceChannel = {
  channel_id?: number
  channel_id_str?: string
  channel_point?: string
  peer_alias?: string
  remote_pubkey?: string
  active?: boolean
  local_disabled?: boolean
  capacity_sat?: number
  local_balance_sat?: number
  local_chan_reserve_sat?: number
}

type ReauthAction = { kind: 'create' } | { kind: 'resume'; jobID: number }

const terminalStatuses = new Set(['completed', 'cancelled', 'failed'])
const activeStatuses = new Set(['running', 'waiting_liquidity', 'pause_requested', 'cancel_requested'])
const channelID = (channel: SourceChannel) => String(channel.channel_id_str || channel.channel_id || '').trim()
const compactSats = (value: number) => {
  const amount = Math.max(0, Number(value) || 0)
  if (amount >= 1_000_000_000) return `${(amount / 1_000_000_000).toFixed(amount >= 10_000_000_000 ? 0 : 1)}B`
  if (amount >= 1_000_000) return `${(amount / 1_000_000).toFixed(amount >= 10_000_000 ? 0 : 1)}M`
  if (amount >= 1_000) return `${(amount / 1_000).toFixed(amount >= 10_000 ? 0 : 1)}k`
  return String(amount)
}

export default function LoopOutBRLN() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const sats = useCallback((value?: number) => `${Math.max(0, Number(value) || 0).toLocaleString(locale)} sat`, [locale])
  const dateTime = useCallback((value?: string) => value ? new Date(value).toLocaleString(locale) : '—', [locale])
  const [status, setStatus] = useState<Awaited<ReturnType<typeof getLoopOutBRLNStatus>> | null>(null)
  const [jobs, setJobs] = useState<LoopOutBRLNJob[]>([])
  const [detail, setDetail] = useState<LoopOutBRLNJobDetail | null>(null)
  const [channels, setChannels] = useState<SourceChannel[]>([])
  const [selectedJobID, setSelectedJobID] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [message, setMessage] = useState('')
  const [tab, setTab] = useState<'new' | 'history'>('new')
  const [preview, setPreview] = useState<LoopOutBRLNPreview | null>(null)
  const [sourceMode, setSourceMode] = useState<'auto' | 'manual'>('auto')
  const [selectedChannels, setSelectedChannels] = useState<Set<string>>(new Set())
  const [address, setAddress] = useState('')
  const [totalSat, setTotalSat] = useState('')
  const [trancheSat, setTrancheSat] = useState('100000')
  const [intervalSeconds, setIntervalSeconds] = useState('15')
  const [timeoutSeconds, setTimeoutSeconds] = useState('120')
  const [maxFeePPM, setMaxFeePPM] = useState('2500')
  const [minLocalPercent, setMinLocalPercent] = useState('60')
  const [comment, setComment] = useState('')
  const [channelSearch, setChannelSearch] = useState('')
  const [reauthAction, setReauthAction] = useState<ReauthAction | null>(null)
  const [password, setPassword] = useState('')

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    try {
      const [nextStatus, history, channelResult] = await Promise.all([
        getLoopOutBRLNStatus(),
        getLoopOutBRLNJobs(50),
        getLnChannels().catch(() => ({ channels: [] }))
      ])
      setStatus(nextStatus)
      setJobs(history.jobs || [])
      const raw = channelResult as any
      setChannels(Array.isArray(raw?.channels) ? raw.channels : [])
      const focusID = selectedJobID || nextStatus.active_job?.id || history.jobs?.[0]?.id
      if (focusID) {
        const nextDetail = await getLoopOutBRLNJob(focusID).catch(() => null)
        if (nextDetail) {
          setDetail(nextDetail)
          setSelectedJobID(focusID)
        }
      } else {
        setDetail(null)
      }
      if (!silent) setMessage('')
    } catch (err: any) {
      if (!silent) setMessage(err?.message || t('loopOutBrln.loadFailed'))
    } finally {
      if (!silent) setLoading(false)
    }
  }, [selectedJobID, t])

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(true), 3_000)
    return () => window.clearInterval(timer)
  }, [load])

  const requestPayload = useMemo<LoopOutBRLNRequest>(() => ({
    lightning_address: address.trim(),
    total_sat: Number(totalSat),
    tranche_sat: Number(trancheSat),
    interval_seconds: Number(intervalSeconds),
    timeout_seconds: Number(timeoutSeconds),
    max_fee_ppm: Number(maxFeePPM),
    min_local_percent: Number(minLocalPercent),
    comment: comment.trim(),
    selected_channel_ids: sourceMode === 'manual' ? Array.from(selectedChannels) : undefined
  }), [address, comment, intervalSeconds, maxFeePPM, minLocalPercent, selectedChannels, sourceMode, timeoutSeconds, totalSat, trancheSat])

  const formValid = requestPayload.lightning_address.includes('@') && requestPayload.total_sat > 0 &&
    requestPayload.tranche_sat > 0 && requestPayload.tranche_sat <= requestPayload.total_sat &&
    Number.isSafeInteger(requestPayload.total_sat) && Number.isSafeInteger(requestPayload.tranche_sat) &&
    requestPayload.max_fee_ppm >= 1 && requestPayload.max_fee_ppm <= 1_000_000 &&
    requestPayload.min_local_percent >= 0 && requestPayload.min_local_percent < 100 &&
    (sourceMode === 'auto' || selectedChannels.size > 0)

  const visibleChannels = useMemo(() => {
    const query = channelSearch.trim().toLowerCase()
    return channels
      .filter((channel) => channelID(channel) && channel.capacity_sat)
      .filter((channel) => !query || channel.peer_alias?.toLowerCase().includes(query) || channel.remote_pubkey?.toLowerCase().includes(query) || channelID(channel).includes(query))
      .sort((a, b) => Number(b.local_balance_sat || 0) - Number(a.local_balance_sat || 0))
  }, [channelSearch, channels])

  const previewByChannel = useMemo(() => new Map((preview?.channels || []).map((channel) => [channel.channel_id, channel])), [preview])
  const activeJob = status?.active_job
  const displayedJob = detail?.job || activeJob
  const progress = displayedJob ? Math.min(100, Math.max(0, displayedJob.sent_sat * 100 / Math.max(1, displayedJob.total_sat))) : 0
  const effectivePPM = displayedJob?.sent_sat ? Math.round(displayedJob.fee_sat * 1_000_000 / displayedJob.sent_sat) : 0
  const draftParts = requestPayload.total_sat > 0 && requestPayload.tranche_sat > 0
    ? Math.ceil(requestPayload.total_sat / requestPayload.tranche_sat)
    : 0
  const draftLast = draftParts > 0
    ? requestPayload.total_sat - Math.max(0, draftParts - 1) * requestPayload.tranche_sat
    : 0
  const sourceMetrics = useMemo(() => {
    const floor = Math.max(0, Math.min(99.99, Number(minLocalPercent) || 0))
    const tranche = Math.max(0, Number(trancheSat) || 0)
    const fee = Math.ceil(tranche * Math.max(0, Number(maxFeePPM) || 0) / 1_000_000)
    let local = 0
    let drainable = 0
    let eligible = 0
    let considered = 0
    channels.forEach((channel) => {
      const id = channelID(channel)
      if (!id || (sourceMode === 'manual' && !selectedChannels.has(id))) return
      considered += 1
      const capacity = Math.max(0, Number(channel.capacity_sat) || 0)
      const balance = Math.max(0, Number(channel.local_balance_sat) || 0)
      const reserve = Math.max(Math.ceil(capacity * floor / 100), Number(channel.local_chan_reserve_sat) || 0)
      const safe = Math.max(0, balance - reserve)
      local += balance
      if (channel.active && !channel.local_disabled) {
        drainable += safe
        if (safe >= tranche + fee && tranche > 0) eligible += 1
      }
    })
    return { local, drainable, eligible, considered }
  }, [channels, maxFeePPM, minLocalPercent, selectedChannels, sourceMode, trancheSat])

  const clearPreview = () => setPreview(null)
  const toggleChannel = (id: string) => {
    setSelectedChannels((current) => {
      const next = new Set(current)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
    clearPreview()
  }

  const requestPreview = async (event?: FormEvent) => {
    event?.preventDefault()
    if (!formValid) return
    setBusy('preview')
    setMessage('')
    setPreview(null)
    try {
      setPreview(await previewLoopOutBRLN(requestPayload))
    } catch (err: any) {
      setMessage(err?.message || t('loopOutBrln.previewFailed'))
    } finally {
      setBusy('')
    }
  }

  const createJob = async (confirmPassword?: string) => {
    if (!preview?.can_start) return
    setBusy('create')
    setMessage('')
    try {
      const created = await createLoopOutBRLNJob({ ...requestPayload, confirm_password: confirmPassword })
      setReauthAction(null)
      setPassword('')
      setSelectedJobID(created.id)
      setPreview(null)
      setTab('history')
      setMessage(t('loopOutBrln.started'))
      await load(true)
    } catch (err: any) {
      if (err instanceof APIError && err.code === 'loopout_brln_reauth_required') {
        setReauthAction({ kind: 'create' })
      } else {
        setMessage(err?.message || t('loopOutBrln.startFailed'))
      }
    } finally {
      setBusy('')
    }
  }

  const jobAction = async (action: 'pause' | 'resume' | 'cancel', jobID: number, confirmPassword?: string) => {
    setBusy(action)
    setMessage('')
    try {
      if (action === 'pause') await pauseLoopOutBRLNJob(jobID)
      if (action === 'cancel') await cancelLoopOutBRLNJob(jobID)
      if (action === 'resume') await resumeLoopOutBRLNJob(jobID, confirmPassword)
      setReauthAction(null)
      setPassword('')
      await load(true)
    } catch (err: any) {
      if (action === 'resume' && err instanceof APIError && err.code === 'loopout_brln_reauth_required') {
        setReauthAction({ kind: 'resume', jobID })
      } else {
        setMessage(err?.message || t('loopOutBrln.actionFailed'))
      }
    } finally {
      setBusy('')
    }
  }

  const submitPassword = async (event: FormEvent) => {
    event.preventDefault()
    if (!password.trim() || !reauthAction) return
    if (reauthAction.kind === 'create') await createJob(password)
    else await jobAction('resume', reauthAction.jobID, password)
  }

  const selectJob = async (id: number) => {
    setSelectedJobID(id)
    setBusy('detail')
    try {
      setDetail(await getLoopOutBRLNJob(id))
    } catch (err: any) {
      setMessage(err?.message || t('loopOutBrln.loadFailed'))
    } finally {
      setBusy('')
    }
  }

  if (loading) return <div className="section-card text-sm text-fog/60">{t('common.loading')}</div>

  if (!status?.installed || !status.enabled) {
    return (
      <div className="space-y-6">
        <Hero />
        <div className="section-card mx-auto max-w-3xl text-center">
          <img src={loopOutBRLNIcon} alt="" className="mx-auto h-20 w-20 rounded-3xl object-cover shadow-[0_0_40px_rgba(245,158,11,.18)]" />
          <h2 className="mt-5 text-2xl font-semibold">{status?.installed ? t('loopOutBrln.stoppedTitle') : t('loopOutBrln.installTitle')}</h2>
          <p className="mx-auto mt-2 max-w-xl text-sm leading-6 text-fog/65">{status?.installed ? t('loopOutBrln.stoppedBody') : t('loopOutBrln.installBody')}</p>
          <a href="#apps" className="btn-primary mt-6 inline-flex">{t('loopOutBrln.openStore')}</a>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6 pb-12">
      <Hero />
      {message && <div className="rounded-2xl border border-brass/25 bg-brass/10 px-4 py-3 text-sm text-fog">{message}</div>}

      {activeJob && <ActiveJobCard job={activeJob} sats={sats} progress={activeJob.sent_sat * 100 / Math.max(1, activeJob.total_sat)} busy={busy} t={t} onAction={jobAction} />}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <OverviewMetric icon={<LiquidityIcon />} label={t('loopOutBrln.availableLocal')} value={sats(sourceMetrics.local)} hint={t('loopOutBrln.availableLocalHint')} tone="glow" />
        <OverviewMetric icon={<ShieldIcon />} label={t('loopOutBrln.safeToMove')} value={sats(sourceMetrics.drainable)} hint={t('loopOutBrln.safeToMoveHint', { floor: requestPayload.min_local_percent || 0 })} tone="brass" />
        <OverviewMetric icon={<ChannelIcon />} label={t('loopOutBrln.eligibleSources')} value={`${sourceMetrics.eligible} / ${sourceMetrics.considered}`} hint={sourceMode === 'auto' ? t('loopOutBrln.automaticSelection') : t('loopOutBrln.manualSelection')} tone="glow" />
        <OverviewMetric icon={<LayersIcon />} label={t('loopOutBrln.draftPlan')} value={draftParts ? t('loopOutBrln.paymentCount', { count: draftParts }) : '—'} hint={requestPayload.total_sat > 0 ? sats(requestPayload.total_sat) : t('loopOutBrln.awaitingAmount')} tone="brass" />
      </div>

      <div className="inline-flex w-fit rounded-2xl border border-white/10 bg-black/15 p-1 shadow-panel">
        <button className={`flex items-center gap-2 rounded-xl px-4 py-2 text-sm font-medium transition ${tab === 'new' ? 'bg-glow text-ink shadow-[0_8px_24px_rgba(34,211,238,.12)]' : 'text-fog/55 hover:bg-white/5 hover:text-fog'}`} type="button" onClick={() => setTab('new')}><TargetIcon />{t('loopOutBrln.newLoop')}</button>
        <button className={`flex items-center gap-2 rounded-xl px-4 py-2 text-sm font-medium transition ${tab === 'history' ? 'bg-glow text-ink shadow-[0_8px_24px_rgba(34,211,238,.12)]' : 'text-fog/55 hover:bg-white/5 hover:text-fog'}`} type="button" onClick={() => setTab('history')}><HistoryIcon />{t('loopOutBrln.history')}<span className={`rounded-full px-1.5 py-0.5 text-[10px] ${tab === 'history' ? 'bg-ink/15' : 'bg-white/5'}`}>{jobs.length}</span></button>
      </div>

      {tab === 'new' && (
        <form className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(340px,.72fr)]" onSubmit={requestPreview}>
          <div className="section-card space-y-8">
            <div>
              <p className="text-[10px] font-semibold uppercase tracking-[.24em] text-brass">{t('loopOutBrln.destinationEyebrow')}</p>
              <h2 className="mt-2 text-2xl font-semibold">{t('loopOutBrln.configureTitle')}</h2>
              <p className="mt-2 max-w-2xl text-sm leading-6 text-fog/55">{t('loopOutBrln.configureBody')}</p>
            </div>

            <section>
              <SectionHeading number="01" icon={<WalletIcon />} title={t('loopOutBrln.destinationStep')} description={t('loopOutBrln.lightningAddressHint')} />
              <div className="relative mt-4">
                <span className="pointer-events-none absolute left-4 top-1/2 -translate-y-1/2 text-fog/35"><AtIcon /></span>
                <input className="input-field pl-12 pr-12 text-base font-medium" value={address} onChange={(e) => { setAddress(e.target.value); clearPreview() }} placeholder="name@example.com" autoComplete="off" spellCheck={false} />
                <span className={`pointer-events-none absolute right-4 top-1/2 h-2.5 w-2.5 -translate-y-1/2 rounded-full ${address.includes('@') ? 'bg-emerald-400 shadow-[0_0_12px_rgba(52,211,153,.55)]' : 'bg-white/15'}`} />
              </div>
            </section>

            <section className="border-t border-white/10 pt-7">
              <SectionHeading number="02" icon={<LayersIcon />} title={t('loopOutBrln.amountStep')} description={t('loopOutBrln.trancheHint')} />
              <div className="mt-4 grid gap-4 sm:grid-cols-2">
                <Field label={t('loopOutBrln.totalAmount')}><AmountInput value={totalSat} onChange={(value) => { setTotalSat(value); clearPreview() }} /></Field>
                <Field label={t('loopOutBrln.trancheAmount')}><AmountInput value={trancheSat} onChange={(value) => { setTrancheSat(value); clearPreview() }} /></Field>
              </div>
              <div className="mt-4 grid gap-3 rounded-2xl border border-white/10 bg-ink/30 p-4 sm:grid-cols-[1fr_auto] sm:items-center">
                <div className="flex items-center gap-3"><span className="flex h-9 w-9 items-center justify-center rounded-xl border border-glow/20 bg-glow/10 text-glow"><SplitIcon /></span><div><p className="text-sm font-medium">{draftParts ? t('loopOutBrln.paymentCount', { count: draftParts }) : t('loopOutBrln.awaitingAmount')}</p><p className="mt-1 text-xs text-fog/45">{draftParts ? t('loopOutBrln.lastDraftPayment', { amount: sats(draftLast) }) : t('loopOutBrln.planUpdatesLive')}</p></div></div>
                <div className="flex flex-wrap gap-1.5">{[50_000, 100_000, 250_000].map((value) => <button key={value} type="button" onClick={() => { setTrancheSat(String(value)); clearPreview() }} className={`rounded-lg border px-2.5 py-1.5 text-[10px] font-medium transition ${Number(trancheSat) === value ? 'border-brass/50 bg-brass/12 text-brass' : 'border-white/10 text-fog/45 hover:border-white/25 hover:text-fog'}`}>{compactSats(value)}</button>)}</div>
              </div>
            </section>

            <section className="border-t border-white/10 pt-7">
              <SectionHeading number="03" icon={<ShieldIcon />} title={t('loopOutBrln.safetyStep')} description={t('loopOutBrln.safetyStepHint')} />
              <div className="mt-4 grid gap-5 sm:grid-cols-2">
                <PresetField label={t('loopOutBrln.minLocal')} suffix="%" value={minLocalPercent} values={[40, 50, 60, 70]} onChange={(value) => { setMinLocalPercent(value); clearPreview() }} hint={t('loopOutBrln.minLocalHint')} />
                <PresetField label={t('loopOutBrln.maxFee')} suffix="PPM" value={maxFeePPM} values={[500, 1000, 2500, 5000]} onChange={(value) => { setMaxFeePPM(value); clearPreview() }} />
              </div>
            </section>

            <section className="border-t border-white/10 pt-7">
              <SectionHeading number="04" icon={<ChannelIcon />} title={t('loopOutBrln.sources')} description={t('loopOutBrln.sourcesHint')} />
              <div className="mt-4 grid gap-3 sm:grid-cols-2">
                <ModeCard active={sourceMode === 'auto'} icon={<AutoIcon />} title={t('loopOutBrln.automatic')} badge={t('loopOutBrln.recommended')} description={t('loopOutBrln.autoModeBody')} onClick={() => { setSourceMode('auto'); clearPreview() }} />
                <ModeCard active={sourceMode === 'manual'} icon={<TuneIcon />} title={t('loopOutBrln.manual')} badge={selectedChannels.size ? t('loopOutBrln.selectedCount', { count: selectedChannels.size }) : undefined} description={t('loopOutBrln.manualModeBody')} onClick={() => { setSourceMode('manual'); clearPreview() }} />
              </div>
              {sourceMode === 'manual' && (
                <div className="mt-4 space-y-3 rounded-2xl border border-white/10 bg-ink/25 p-3 sm:p-4">
                  <div className="flex flex-wrap items-center justify-between gap-3"><div><p className="text-sm font-medium">{t('loopOutBrln.chooseSources')}</p><p className="mt-1 text-xs text-fog/45">{t('loopOutBrln.selectedLiquidity', { amount: sats(sourceMetrics.local) })}</p></div>{selectedChannels.size > 0 && <button className="text-xs text-fog/45 hover:text-fog" type="button" onClick={() => { setSelectedChannels(new Set()); clearPreview() }}>{t('loopOutBrln.clearSelection')}</button>}</div>
                  <input className="input-field py-2.5 text-sm" value={channelSearch} onChange={(e) => setChannelSearch(e.target.value)} placeholder={t('loopOutBrln.searchChannels')} />
                  <div className="grid max-h-[390px] gap-2 overflow-y-auto pr-1 lg:grid-cols-2">
                    {visibleChannels.map((channel) => {
                      const id = channelID(channel)
                      const localPct = Number(channel.local_balance_sat || 0) * 100 / Math.max(1, Number(channel.capacity_sat || 0))
                      const selected = selectedChannels.has(id)
                      const projected = previewByChannel.get(id)
                      return <button key={id} type="button" onClick={() => toggleChannel(id)} className={`group rounded-2xl border p-4 text-left transition ${selected ? 'border-brass/55 bg-brass/10 shadow-[0_0_0_1px_rgba(245,158,11,.08)]' : 'border-white/10 bg-white/[0.025] hover:border-white/20 hover:bg-white/[0.045]'}`}>
                        <div className="flex items-start justify-between gap-3"><div className="min-w-0"><p className="truncate text-sm font-semibold">{channel.peer_alias || id}</p><p className="mt-1 truncate font-mono text-[10px] text-fog/35">{id}</p></div><span className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full border transition ${selected ? 'border-brass bg-brass text-ink' : 'border-white/20 text-transparent group-hover:border-white/40'}`}><CheckIcon /></span></div>
                        <div className="mt-4 h-1.5 overflow-hidden rounded-full bg-white/10"><div className="h-full rounded-full bg-gradient-to-r from-glow/70 to-brass" style={{ width: `${Math.min(100, localPct)}%` }} /></div>
                        <div className="mt-2 flex justify-between text-[11px]"><span className="text-glow">{t('loopOutBrln.local')} {compactSats(Number(channel.local_balance_sat) || 0)}</span><span className="text-fog/45">{localPct.toFixed(1)}%</span></div>
                        {projected && <p className="mt-2 text-[10px] text-fog/40">{t('loopOutBrln.drainable')}: {sats(projected.drainable_sat)}</p>}
                      </button>
                    })}
                    {visibleChannels.length === 0 && <div className="rounded-2xl border border-dashed border-white/10 p-6 text-center text-sm text-fog/45 lg:col-span-2">{t('loopOutBrln.noSourceChannels')}</div>}
                  </div>
                </div>
              )}
            </section>

            <details className="group border-t border-white/10 pt-6">
              <summary className="flex cursor-pointer list-none items-center justify-between text-sm font-semibold"><span className="flex items-center gap-2"><TuneIcon />{t('loopOutBrln.advanced')}</span><span className="text-fog/40 transition group-open:rotate-180">⌄</span></summary>
              <div className="mt-5 grid gap-4 sm:grid-cols-2">
                <Field label={t('loopOutBrln.interval')}><input className="input-field" type="number" min="0" max="86400" value={intervalSeconds} onChange={(e) => { setIntervalSeconds(e.target.value); clearPreview() }} /></Field>
                <Field label={t('loopOutBrln.timeout')}><input className="input-field" type="number" min="30" max="600" value={timeoutSeconds} onChange={(e) => { setTimeoutSeconds(e.target.value); clearPreview() }} /></Field>
                <Field className="sm:col-span-2" label={t('loopOutBrln.comment')} hint={t('loopOutBrln.commentHint')}><input className="input-field" maxLength={512} value={comment} onChange={(e) => { setComment(e.target.value); clearPreview() }} /></Field>
              </div>
            </details>
          </div>

          <ReviewPanel preview={preview} request={requestPayload} sats={sats} t={t} busy={busy} formValid={formValid} hasActiveJob={Boolean(activeJob)} sourceMode={sourceMode} selectedCount={selectedChannels.size} draftParts={draftParts} draftLast={draftLast} onStart={() => void createJob()} />
        </form>
      )}

      {tab === 'history' && (
        <div className="grid gap-6 xl:grid-cols-[340px_1fr]">
          <div className="section-card space-y-3 self-start">
            <div><p className="text-xs uppercase tracking-[.2em] text-brass">{t('loopOutBrln.runs')}</p><h2 className="mt-2 text-xl font-semibold">{t('loopOutBrln.history')}</h2></div>
            {jobs.length === 0 && <p className="text-sm text-fog/55">{t('loopOutBrln.noHistory')}</p>}
            {jobs.map((job) => <button key={job.id} type="button" onClick={() => void selectJob(job.id)} className={`w-full rounded-2xl border p-4 text-left ${selectedJobID === job.id ? 'border-brass/50 bg-brass/10' : 'border-white/10 bg-black/10 hover:border-white/20'}`}>
              <div className="flex items-center justify-between gap-2"><span className="truncate text-sm font-medium">{job.lightning_address}</span><StatusBadge status={job.status} t={t} /></div>
              <div className="mt-3 flex justify-between text-xs text-fog/55"><span>{sats(job.sent_sat)} / {sats(job.total_sat)}</span><span>{dateTime(job.created_at)}</span></div>
              <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-white/5"><div className="h-full rounded-full bg-brass" style={{ width: `${Math.min(100, job.sent_sat * 100 / Math.max(1, job.total_sat))}%` }} /></div>
            </button>)}
          </div>
          <JobDetail detail={detail} busy={busy} sats={sats} dateTime={dateTime} progress={progress} effectivePPM={effectivePPM} t={t} onAction={jobAction} />
        </div>
      )}

      {reauthAction && <ReauthModal password={password} busy={busy} t={t} onPassword={setPassword} onSubmit={submitPassword} onClose={() => { setReauthAction(null); setPassword('') }} />}
    </div>
  )
}

function Hero() {
  const { t } = useTranslation()
  return <div className="relative overflow-hidden rounded-[2rem] border border-brass/20 bg-[radial-gradient(circle_at_12%_0%,rgba(245,158,11,.2),transparent_34%),radial-gradient(circle_at_92%_20%,rgba(34,211,238,.1),transparent_30%),linear-gradient(135deg,rgba(13,17,23,.99),rgba(21,25,32,.96))] p-6 shadow-panel sm:p-8">
    <div className="absolute -right-20 -top-24 h-64 w-64 rounded-full border border-brass/10" /><div className="absolute -right-6 -top-10 h-40 w-40 rounded-full border border-glow/10" />
    <div className="relative grid items-center gap-8 lg:grid-cols-[1fr_360px]">
      <div className="flex flex-col gap-5 sm:flex-row sm:items-center">
        <img src={loopOutBRLNIcon} alt="Loop Out BRLN" className="h-20 w-20 rounded-3xl object-cover shadow-[0_0_42px_rgba(245,158,11,.25)] ring-1 ring-brass/20" />
        <div><p className="text-[10px] font-semibold uppercase tracking-[.3em] text-brass">{t('loopOutBrln.eyebrow')}</p><h1 className="mt-2 text-3xl font-semibold tracking-tight sm:text-4xl">Loop Out BR⚡LN</h1><p className="mt-2 max-w-2xl text-sm leading-6 text-fog/60">{t('loopOutBrln.subtitle')}</p><div className="mt-4 flex flex-wrap gap-2"><HeroPill icon={<ShieldIcon />} text={t('loopOutBrln.heroGuard')} /><HeroPill icon={<SplitIcon />} text={t('loopOutBrln.heroBatches')} /><HeroPill icon={<HistoryIcon />} text={t('loopOutBrln.heroHistory')} /></div></div>
      </div>
      <div className="hidden items-center justify-end gap-3 lg:flex">
        <div className="rounded-2xl border border-white/10 bg-white/[0.035] px-4 py-3 text-right"><p className="text-[9px] uppercase tracking-[.2em] text-fog/35">{t('loopOutBrln.yourNode')}</p><p className="mt-1 text-sm font-semibold">LND</p></div>
        <div className="relative h-px w-24 bg-gradient-to-r from-glow/60 via-brass to-brass/20"><span className="absolute -top-2.5 left-1/2 -translate-x-1/2 rounded-full border border-brass/20 bg-ink px-2 py-0.5 text-[9px] text-brass">⚡</span><span className="absolute -right-1 -top-1 h-2 w-2 rotate-45 border-r border-t border-brass" /></div>
        <div className="rounded-2xl border border-brass/20 bg-brass/[0.07] px-4 py-3"><p className="text-[9px] uppercase tracking-[.2em] text-fog/35">{t('loopOutBrln.externalWallet')}</p><p className="mt-1 text-sm font-semibold">name@wallet</p></div>
      </div>
    </div>
  </div>
}

function HeroPill({ icon, text }: { icon: ReactNode; text: string }) {
  return <span className="inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-white/[0.035] px-2.5 py-1 text-[10px] text-fog/50"><span className="text-brass">{icon}</span>{text}</span>
}

function Field({ label, hint, className = '', children }: { label: string; hint?: string; className?: string; children: ReactNode }) {
  return <label className={`block ${className}`}><span className="mb-2 block text-[10px] font-semibold uppercase tracking-[0.16em] text-fog/50">{label}</span>{children}{hint && <span className="mt-2 block text-[11px] leading-5 text-fog/40">{hint}</span>}</label>
}

function SectionHeading({ number, icon, title, description }: { number: string; icon: ReactNode; title: string; description: string }) {
  return <div className="flex items-start gap-3"><span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl border border-brass/20 bg-brass/10 text-brass">{icon}</span><div><div className="flex items-center gap-2"><span className="text-[9px] font-semibold uppercase tracking-[.2em] text-brass/70">{number}</span><h3 className="text-base font-semibold">{title}</h3></div><p className="mt-1 text-xs leading-5 text-fog/45">{description}</p></div></div>
}

function AmountInput({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <div className="relative"><input className="input-field pr-20 text-lg font-semibold tabular-nums" type="number" min="1" value={value} onChange={(event) => onChange(event.target.value)} placeholder="0" /><span className="pointer-events-none absolute right-4 top-1/2 -translate-y-1/2 text-[10px] font-semibold uppercase tracking-[.16em] text-fog/35">sats</span></div>
}

function PresetField({ label, suffix, value, values, hint, onChange }: { label: string; suffix: string; value: string; values: number[]; hint?: string; onChange: (value: string) => void }) {
  return <div><Field label={label} hint={hint}><div className="relative"><input className="input-field pr-16 text-base font-semibold tabular-nums" type="number" min={suffix === '%' ? 0 : 1} max={suffix === '%' ? 99.99 : 1_000_000} step={suffix === '%' ? .1 : 1} value={value} onChange={(event) => onChange(event.target.value)} /><span className="pointer-events-none absolute right-4 top-1/2 -translate-y-1/2 text-[10px] font-semibold uppercase tracking-wider text-fog/35">{suffix}</span></div></Field><div className="mt-2 flex flex-wrap gap-1.5">{values.map((preset) => <button key={preset} type="button" onClick={() => onChange(String(preset))} className={`rounded-lg border px-2.5 py-1.5 text-[10px] font-medium transition ${Number(value) === preset ? 'border-brass/45 bg-brass/10 text-brass' : 'border-white/10 text-fog/40 hover:border-white/25 hover:text-fog'}`}>{preset.toLocaleString()} {suffix}</button>)}</div></div>
}

function ModeCard({ active, icon, title, badge, description, onClick }: { active: boolean; icon: ReactNode; title: string; badge?: string; description: string; onClick: () => void }) {
  return <button type="button" onClick={onClick} className={`group flex min-h-28 items-start gap-3 rounded-2xl border p-4 text-left transition ${active ? 'border-brass/50 bg-brass/[0.08] shadow-[inset_0_0_0_1px_rgba(245,158,11,.05)]' : 'border-white/10 bg-white/[0.025] hover:border-white/20 hover:bg-white/[0.045]'}`}><span className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl border ${active ? 'border-brass/25 bg-brass/15 text-brass' : 'border-white/10 bg-white/5 text-fog/40'}`}>{icon}</span><span className="min-w-0 flex-1"><span className="flex items-center justify-between gap-2"><strong className="text-sm">{title}</strong>{badge && <span className={`rounded-full px-2 py-1 text-[9px] uppercase tracking-wider ${active ? 'bg-brass/15 text-brass' : 'bg-white/5 text-fog/35'}`}>{badge}</span>}</span><span className="mt-1.5 block text-xs leading-5 text-fog/45">{description}</span></span><span className={`mt-1 flex h-5 w-5 shrink-0 items-center justify-center rounded-full border ${active ? 'border-brass bg-brass text-ink' : 'border-white/20 text-transparent group-hover:border-white/40'}`}><CheckIcon /></span></button>
}

function OverviewMetric({ icon, label, value, hint, tone }: { icon: ReactNode; label: string; value: string; hint: string; tone: 'brass' | 'glow' }) {
  return <div className={`rounded-3xl border border-white/10 bg-gradient-to-br ${tone === 'brass' ? 'from-brass/[0.11]' : 'from-glow/[0.1]'} to-transparent p-4 shadow-panel`}><div className="flex items-start gap-3"><span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border ${tone === 'brass' ? 'border-brass/20 bg-brass/10 text-brass' : 'border-glow/20 bg-glow/10 text-glow'}`}>{icon}</span><div className="min-w-0"><p className="text-[9px] font-semibold uppercase tracking-[.18em] text-fog/40">{label}</p><p className="mt-1 truncate text-lg font-semibold tabular-nums">{value}</p><p className="mt-1 min-h-8 text-[10px] leading-4 text-fog/35">{hint}</p></div></div></div>
}

function StatusBadge({ status, t }: { status: string; t: any }) {
  const color = status === 'completed' ? 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200' : status === 'failed' || status === 'cancelled' ? 'border-rose-400/30 bg-rose-500/15 text-rose-200' : status === 'paused' ? 'border-sky-400/30 bg-sky-500/15 text-sky-200' : status === 'waiting_liquidity' ? 'border-amber-400/30 bg-amber-500/15 text-amber-200' : 'border-brass/30 bg-brass/10 text-brass'
  return <span className={`shrink-0 rounded-full border px-2.5 py-1 text-[10px] uppercase tracking-wide ${color}`}>{t(`loopOutBrln.status.${status}`, { defaultValue: status.replace(/_/g, ' ') })}</span>
}

function ActiveJobCard({ job, sats, progress, busy, t, onAction }: { job: LoopOutBRLNJob; sats: (n?: number) => string; progress: number; busy: string; t: any; onAction: (action: 'pause' | 'resume' | 'cancel', id: number) => Promise<void> }) {
  return <div className="section-card overflow-hidden">
    <div className="flex flex-col gap-5 lg:flex-row lg:items-center lg:justify-between">
      <div className="flex items-center gap-4"><div className="relative flex h-20 w-20 items-center justify-center rounded-full" style={{ background: `conic-gradient(rgb(217 165 75) ${Math.min(100, progress)}%, rgba(255,255,255,.07) 0)` }}><div className="flex h-16 w-16 items-center justify-center rounded-full bg-ink text-sm font-semibold">{Math.floor(progress)}%</div></div><div><div className="flex flex-wrap items-center gap-2"><h2 className="text-xl font-semibold">{t('loopOutBrln.activeLoop')}</h2><StatusBadge status={job.status} t={t} /></div><p className="mt-1 text-sm text-fog/60">{job.lightning_address}</p><p className="mt-2 text-xs text-fog/45">{sats(job.sent_sat)} / {sats(job.total_sat)}</p></div></div>
      <div className="flex flex-wrap gap-2">{activeStatuses.has(job.status) && !['pause_requested', 'cancel_requested'].includes(job.status) && <button className="btn-secondary" disabled={Boolean(busy)} onClick={() => void onAction('pause', job.id)}>{t('common.pause')}</button>}{job.status === 'paused' && <button className="btn-primary" disabled={Boolean(busy)} onClick={() => void onAction('resume', job.id)}>{t('common.resume')}</button>}{!terminalStatuses.has(job.status) && job.status !== 'cancel_requested' && <button className="btn-secondary text-rose-200" disabled={Boolean(busy)} onClick={() => void onAction('cancel', job.id)}>{t('common.cancel')}</button>}</div>
    </div>
    {job.last_error && <p className="mt-4 rounded-xl border border-amber-400/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-100/80">{job.last_error}</p>}
  </div>
}

function ReviewPanel({ preview, request, sats, t, busy, formValid, hasActiveJob, sourceMode, selectedCount, draftParts, draftLast, onStart }: { preview: LoopOutBRLNPreview | null; request: LoopOutBRLNRequest; sats: (n?: number) => string; t: any; busy: string; formValid: boolean; hasActiveJob: boolean; sourceMode: 'auto' | 'manual'; selectedCount: number; draftParts: number; draftLast: number; onStart: () => void }) {
  const destinationReady = request.lightning_address.includes('@')
  const amountReady = request.total_sat > 0 && request.tranche_sat > 0 && request.tranche_sat <= request.total_sat
  const sourcesReady = sourceMode === 'auto' || selectedCount > 0
  const requiredLiquidity = preview ? preview.total_sat + preview.max_fee_total_sat : 0
  const coverage = preview && requiredLiquidity > 0 ? Math.min(100, preview.total_drainable_sat * 100 / requiredLiquidity) : 0
  return <aside className="self-start overflow-hidden rounded-3xl border border-white/10 bg-slate/80 shadow-panel xl:sticky xl:top-5">
    <div className="border-b border-white/10 bg-gradient-to-br from-brass/[0.09] to-transparent p-5 sm:p-6">
      <div className="flex items-start justify-between gap-3"><div><p className="text-[10px] font-semibold uppercase tracking-[.22em] text-brass">{t('loopOutBrln.reviewEyebrow')}</p><h2 className="mt-1.5 text-xl font-semibold">{preview ? t('loopOutBrln.reviewTitle') : t('loopOutBrln.livePlan')}</h2></div><span className={`flex h-10 w-10 items-center justify-center rounded-2xl border ${preview?.can_start ? 'border-emerald-400/25 bg-emerald-400/10 text-emerald-300' : 'border-brass/20 bg-brass/10 text-brass'}`}>{preview?.can_start ? <CheckIcon /> : <RouteIcon />}</span></div>

      <div className="mt-6 rounded-2xl border border-white/10 bg-ink/45 p-4">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0"><span className="flex h-9 w-9 items-center justify-center rounded-xl border border-glow/20 bg-glow/10 text-glow"><NodeIcon /></span><p className="mt-2 text-[9px] uppercase tracking-wider text-fog/35">{sourceMode === 'auto' ? t('loopOutBrln.autoSourceShort') : t('loopOutBrln.manualSourceShort', { count: selectedCount })}</p></div>
          <div className="min-w-16 flex-1"><div className="relative h-px bg-gradient-to-r from-glow/50 via-brass to-brass/30"><span className="absolute -top-2.5 left-1/2 -translate-x-1/2 rounded-full border border-brass/20 bg-ink px-2 py-0.5 text-[9px] text-brass">⚡ {draftParts || '—'}×</span><span className="absolute -right-1 -top-1 h-2 w-2 rotate-45 border-r border-t border-brass" /></div></div>
          <div className="min-w-0 text-right"><span className="ml-auto flex h-9 w-9 items-center justify-center rounded-xl border border-brass/20 bg-brass/10 text-brass"><WalletIcon /></span><p className="mt-2 max-w-32 truncate text-[9px] text-fog/45">{request.lightning_address || t('loopOutBrln.destination')}</p></div>
        </div>
        <div className="mt-4 grid grid-cols-2 gap-3 border-t border-white/10 pt-4"><div><p className="text-[9px] uppercase tracking-wider text-fog/35">{t('loopOutBrln.total')}</p><p className="mt-1 text-base font-semibold tabular-nums">{request.total_sat > 0 ? sats(request.total_sat) : '—'}</p></div><div className="text-right"><p className="text-[9px] uppercase tracking-wider text-fog/35">{t('loopOutBrln.lastPayment')}</p><p className="mt-1 text-base font-semibold tabular-nums">{draftLast > 0 ? sats(draftLast) : '—'}</p></div></div>
      </div>
    </div>

    <div className="space-y-5 p-5 sm:p-6">
      {!preview ? <>
        <div className="space-y-2">
          <PlanCheck ready={destinationReady} label={t('loopOutBrln.checkDestination')} detail={destinationReady ? request.lightning_address : t('loopOutBrln.checkDestinationMissing')} />
          <PlanCheck ready={amountReady} label={t('loopOutBrln.checkAmounts')} detail={amountReady ? t('loopOutBrln.paymentCount', { count: draftParts }) : t('loopOutBrln.checkAmountsMissing')} />
          <PlanCheck ready={request.max_fee_ppm >= 1 && request.min_local_percent >= 0} label={t('loopOutBrln.checkGuards')} detail={`${request.min_local_percent || 0}% · ${(request.max_fee_ppm || 0).toLocaleString()} PPM`} />
          <PlanCheck ready={sourcesReady} label={t('loopOutBrln.checkSources')} detail={sourceMode === 'auto' ? t('loopOutBrln.automaticSelection') : t('loopOutBrln.selectedCount', { count: selectedCount })} />
        </div>
        <div className="rounded-2xl border border-dashed border-white/10 px-4 py-4 text-center"><p className="text-sm font-medium">{t('loopOutBrln.reviewEmptyTitle')}</p><p className="mt-1 text-xs leading-5 text-fog/40">{t('loopOutBrln.reviewEmptyBody')}</p></div>
        <button className="btn-primary w-full justify-center py-3" type="submit" disabled={!formValid || Boolean(busy) || hasActiveJob}>{busy === 'preview' ? t('loopOutBrln.validating') : t('loopOutBrln.review')}</button>
        {hasActiveJob && <p className="text-center text-xs text-amber-200/75">{t('loopOutBrln.oneActiveOnly')}</p>}
      </> : <>
        <div className="flex items-center justify-between"><span className={`inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-[10px] font-semibold uppercase tracking-wider ${preview.can_start ? 'border-emerald-400/25 bg-emerald-400/10 text-emerald-200' : 'border-amber-400/25 bg-amber-400/10 text-amber-200'}`}><i className={`h-1.5 w-1.5 rounded-full ${preview.can_start ? 'bg-emerald-300' : 'bg-amber-300'}`} />{preview.can_start ? t('loopOutBrln.ready') : t('loopOutBrln.needsAttention')}</span><span className="text-[10px] text-fog/35">{t('loopOutBrln.validatedNow')}</span></div>
        <div className="grid grid-cols-2 gap-3"><Metric label={t('loopOutBrln.payments')} value={String(preview.estimated_parts)} /><Metric label={t('loopOutBrln.maxFees')} value={sats(preview.max_fee_total_sat)} /><Metric label={t('loopOutBrln.lastPayment')} value={sats(preview.last_tranche_sat)} /><Metric label={t('loopOutBrln.drainable')} value={sats(preview.total_drainable_sat)} /></div>
        <div className="rounded-2xl border border-white/10 bg-black/15 p-4"><div className="flex justify-between text-xs"><span className="text-fog/50">{t('loopOutBrln.liquidityCoverage')}</span><strong className={coverage >= 100 ? 'text-emerald-300' : 'text-amber-200'}>{coverage.toFixed(0)}%</strong></div><div className="mt-3 h-2 overflow-hidden rounded-full bg-white/5"><div className={`h-full rounded-full transition-all ${coverage >= 100 ? 'bg-gradient-to-r from-emerald-400 to-glow' : 'bg-gradient-to-r from-amber-500 to-amber-300'}`} style={{ width: `${coverage}%` }} /></div><div className="mt-3 flex justify-between text-[10px] text-fog/35"><span>{t('loopOutBrln.required')}: {sats(requiredLiquidity)}</span><span>{t('loopOutBrln.safeToMove')}: {sats(preview.total_drainable_sat)}</span></div></div>
        <div className="rounded-2xl border border-white/10 bg-black/15 p-4"><div className="flex justify-between text-xs text-fog/50"><span>{t('loopOutBrln.feeBudget')}</span><strong className="text-fog">{request.max_fee_ppm.toLocaleString()} PPM</strong></div><div className="mt-2 flex justify-between text-xs text-fog/50"><span>{t('loopOutBrln.liquidityFloor')}</span><strong className="text-fog">{request.min_local_percent}%</strong></div><div className="mt-2 flex justify-between text-xs text-fog/50"><span>{t('loopOutBrln.interval')}</span><strong className="text-fog">{request.interval_seconds}s</strong></div></div>
        {(preview.warnings || []).map((warning) => <p key={warning} className="rounded-xl border border-amber-400/20 bg-amber-500/10 px-3 py-2 text-xs leading-5 text-amber-100/80">{t(`loopOutBrln.warnings.${warning}`, { defaultValue: warning })}</p>)}
        <p className="flex items-start gap-2 text-[11px] leading-5 text-fog/40"><ShieldIcon /><span>{t('loopOutBrln.approvalNotice')}</span></p>
        <button className="btn-primary w-full justify-center py-3" type="button" disabled={!preview.can_start || Boolean(busy) || hasActiveJob} onClick={onStart}>{busy === 'create' ? t('loopOutBrln.starting') : t('loopOutBrln.start')}</button>
      </>}
    </div>
  </aside>
}

function PlanCheck({ ready, label, detail }: { ready: boolean; label: string; detail: string }) {
  return <div className="flex items-center gap-3 rounded-xl border border-white/[0.07] bg-white/[0.02] px-3 py-2.5"><span className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full border ${ready ? 'border-emerald-400/30 bg-emerald-400/10 text-emerald-300' : 'border-white/10 bg-white/5 text-fog/25'}`}>{ready ? <CheckIcon /> : <span className="h-1.5 w-1.5 rounded-full bg-current" />}</span><div className="min-w-0"><p className="text-xs font-medium">{label}</p><p className="mt-0.5 truncate text-[10px] text-fog/35">{detail}</p></div></div>
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="rounded-2xl border border-white/10 bg-black/15 p-3"><p className="text-[10px] uppercase tracking-wide text-fog/40">{label}</p><p className="mt-1 break-all text-sm font-semibold">{value}</p></div> }

function JobDetail({ detail, busy, sats, dateTime, progress, effectivePPM, t, onAction }: { detail: LoopOutBRLNJobDetail | null; busy: string; sats: (n?: number) => string; dateTime: (v?: string) => string; progress: number; effectivePPM: number; t: any; onAction: (action: 'pause' | 'resume' | 'cancel', id: number) => Promise<void> }) {
  if (!detail) return <div className="section-card text-center text-sm text-fog/55">{busy === 'detail' ? t('common.loading') : t('loopOutBrln.selectHistory')}</div>
  const { job, payments, events } = detail
  return <div className="space-y-6">
    <div className="section-card space-y-5"><div className="flex flex-wrap items-start justify-between gap-3"><div><div className="flex flex-wrap items-center gap-2"><h2 className="text-2xl font-semibold">{job.lightning_address}</h2><StatusBadge status={job.status} t={t} /></div><p className="mt-1 text-xs text-fog/45">#{job.id} · {dateTime(job.created_at)}</p></div><div className="flex gap-2">{activeStatuses.has(job.status) && !['pause_requested', 'cancel_requested'].includes(job.status) && <button className="btn-secondary" disabled={Boolean(busy)} onClick={() => void onAction('pause', job.id)}>{t('common.pause')}</button>}{job.status === 'paused' && <button className="btn-primary" disabled={Boolean(busy)} onClick={() => void onAction('resume', job.id)}>{t('common.resume')}</button>}{!terminalStatuses.has(job.status) && job.status !== 'cancel_requested' && <button className="btn-secondary text-rose-200" disabled={Boolean(busy)} onClick={() => void onAction('cancel', job.id)}>{t('common.cancel')}</button>}</div></div>
      <div><div className="mb-2 flex justify-between text-xs text-fog/55"><span>{sats(job.sent_sat)} / {sats(job.total_sat)}</span><span>{progress.toFixed(1)}%</span></div><div className="h-2 overflow-hidden rounded-full bg-white/5"><div className="h-full rounded-full bg-gradient-to-r from-brass to-amber-300" style={{ width: `${progress}%` }} /></div></div>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4"><Metric label={t('loopOutBrln.sent')} value={sats(job.sent_sat)} /><Metric label={t('loopOutBrln.remaining')} value={sats(Math.max(0, job.total_sat - job.sent_sat))} /><Metric label={t('loopOutBrln.fees')} value={sats(job.fee_sat)} /><Metric label={t('loopOutBrln.effectivePPM')} value={effectivePPM.toLocaleString()} /></div>
      {job.last_error && <p className="rounded-xl border border-amber-400/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-100/80">{job.last_error}</p>}
    </div>
    <div className="section-card overflow-hidden"><h3 className="mb-4 text-lg font-semibold">{t('loopOutBrln.payments')}</h3><div className="overflow-x-auto"><table className="w-full min-w-[720px] text-left text-xs"><thead className="text-fog/40"><tr><th className="pb-3">#</th><th className="pb-3">{t('loopOutBrln.amount')}</th><th className="pb-3">{t('loopOutBrln.channel')}</th><th className="pb-3">{t('loopOutBrln.fees')}</th><th className="pb-3">{t('common.status')}</th><th className="pb-3">{t('loopOutBrln.time')}</th></tr></thead><tbody>{payments.map((payment) => <tr key={payment.id} className="border-t border-white/5"><td className="py-3">{payment.sequence_no}.{payment.attempt_no}</td><td className="py-3 font-medium">{sats(payment.amount_sat)}</td><td className="py-3"><div>{payment.channel_alias || payment.channel_id || '—'}</div><div className="mt-1 font-mono text-[10px] text-fog/35">{payment.channel_id}</div></td><td className="py-3">{sats(payment.fee_sat)}</td><td className="py-3"><StatusBadge status={payment.status} t={t} />{payment.failure_reason && <p className="mt-1 max-w-xs text-[10px] text-rose-200/70">{payment.failure_reason}</p>}</td><td className="py-3 text-fog/50">{dateTime(payment.created_at)}</td></tr>)}{payments.length === 0 && <tr><td className="py-8 text-center text-fog/45" colSpan={6}>{t('loopOutBrln.noPayments')}</td></tr>}</tbody></table></div></div>
    <div className="section-card"><h3 className="mb-4 text-lg font-semibold">{t('loopOutBrln.timeline')}</h3><div className="space-y-4">{events.map((event, index) => <div key={event.id} className="flex gap-3"><div className="flex flex-col items-center"><span className={`mt-1 h-2.5 w-2.5 rounded-full ${event.level === 'success' ? 'bg-emerald-400' : event.level === 'error' ? 'bg-rose-400' : event.level === 'warning' ? 'bg-amber-300' : 'bg-sky-300'}`} />{index < events.length - 1 && <span className="mt-1 h-full w-px bg-white/10" />}</div><div className="pb-3"><p className="text-sm">{t(`loopOutBrln.events.${event.kind}`, { defaultValue: event.message })}</p><p className="mt-1 text-xs text-fog/40">{dateTime(event.created_at)}</p></div></div>)}</div></div>
  </div>
}

function ReauthModal({ password, busy, t, onPassword, onSubmit, onClose }: { password: string; busy: string; t: any; onPassword: (v: string) => void; onSubmit: (e: FormEvent) => Promise<void>; onClose: () => void }) {
  return <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 px-4 backdrop-blur-sm"><form className="w-full max-w-md rounded-3xl border border-white/10 bg-ink p-6 shadow-2xl" onSubmit={onSubmit}><span className="flex h-11 w-11 items-center justify-center rounded-2xl border border-brass/20 bg-brass/10 text-brass"><ShieldIcon /></span><p className="mt-5 text-xs uppercase tracking-[.2em] text-brass">{t('loopOutBrln.security')}</p><h3 className="mt-2 text-2xl font-semibold">{t('loopOutBrln.confirmTitle')}</h3><p className="mt-2 text-sm leading-6 text-fog/60">{t('loopOutBrln.confirmBody')}</p><input className="input-field mt-5" type="password" autoFocus value={password} onChange={(e) => onPassword(e.target.value)} placeholder={t('auth.password')} /><div className="mt-6 flex justify-end gap-3"><button className="btn-secondary" type="button" onClick={onClose}>{t('common.cancel')}</button><button className="btn-primary" type="submit" disabled={!password.trim() || Boolean(busy)}>{t('loopOutBrln.confirm')}</button></div></form></div>
}

function CheckIcon() { return <svg aria-hidden="true" className="h-3 w-3" viewBox="0 0 16 16" fill="none"><path d="m3 8.5 3 3 7-7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg> }
function ShieldIcon() { return <svg aria-hidden="true" className="h-5 w-5 shrink-0" viewBox="0 0 24 24" fill="none"><path d="M12 3 5 6v5c0 4.5 2.8 8.1 7 10 4.2-1.9 7-5.5 7-10V6l-7-3Z" stroke="currentColor" strokeWidth="1.7" strokeLinejoin="round" /><path d="m9 12 2 2 4-4" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg> }
function WalletIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="M4 7.5h15a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-12a2 2 0 0 1 2-2h12" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /><path d="M16 12h5v4h-5a2 2 0 1 1 0-4Z" stroke="currentColor" strokeWidth="1.7" /></svg> }
function AtIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="8.5" stroke="currentColor" strokeWidth="1.6" /><path d="M15.5 15.5c-1.2 0-1.8-.7-1.8-1.8V9.5m0 4.2a3 3 0 1 1 0-4.2m0 4.2c.7.7 2.3.9 3.1-.2" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg> }
function LayersIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="m12 4 8 4-8 4-8-4 8-4Z" stroke="currentColor" strokeWidth="1.7" strokeLinejoin="round" /><path d="m4 12 8 4 8-4M4 16l8 4 8-4" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg> }
function SplitIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="M5 5h4a3 3 0 0 1 3 3v8a3 3 0 0 0 3 3h4M12 12a3 3 0 0 1 3-3h4" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /><path d="m17 6 3 3-3 3m0 4 3 3-3 3" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg> }
function ChannelIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><circle cx="5" cy="12" r="2.5" stroke="currentColor" strokeWidth="1.7" /><circle cx="19" cy="7" r="2.5" stroke="currentColor" strokeWidth="1.7" /><circle cx="19" cy="17" r="2.5" stroke="currentColor" strokeWidth="1.7" /><path d="m7.5 11 9-3m-9 5 9 3" stroke="currentColor" strokeWidth="1.7" /></svg> }
function AutoIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="M4 12a8 8 0 0 1 13.7-5.6L20 9m0 0V4m0 5h-5M20 12a8 8 0 0 1-13.7 5.6L4 15m0 0v5m0-5h5" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg> }
function TuneIcon() { return <svg aria-hidden="true" className="h-4 w-4" viewBox="0 0 24 24" fill="none"><path d="M4 7h10m4 0h2M4 17h2m4 0h10M14 4v6M6 14v6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg> }
function TargetIcon() { return <svg aria-hidden="true" className="h-4 w-4" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="8" stroke="currentColor" strokeWidth="1.7" /><circle cx="12" cy="12" r="3" stroke="currentColor" strokeWidth="1.7" /><path d="M12 2v3m10 7h-3M12 22v-3M2 12h3" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /></svg> }
function HistoryIcon() { return <svg aria-hidden="true" className="h-4 w-4" viewBox="0 0 24 24" fill="none"><path d="M4 12a8 8 0 1 0 2.3-5.7L4 8.5M4 4v4.5h4.5" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /><path d="M12 8v4l3 2" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /></svg> }
function LiquidityIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><path d="M12 3c3.5 4.2 6 7 6 10.4a6 6 0 1 1-12 0C6 10 8.5 7.2 12 3Z" stroke="currentColor" strokeWidth="1.7" /><path d="M9.5 15.5c.8 1 1.7 1.5 3 1.5" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /></svg> }
function RouteIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><circle cx="6" cy="17" r="2" stroke="currentColor" strokeWidth="1.7" /><circle cx="18" cy="7" r="2" stroke="currentColor" strokeWidth="1.7" /><path d="M8 17h2.5c4.5 0 2-10 5.5-10" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeDasharray="2.2 2.2" /></svg> }
function NodeIcon() { return <svg aria-hidden="true" className="h-5 w-5" viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="3" stroke="currentColor" strokeWidth="1.7" /><circle cx="12" cy="3.5" r="1.5" fill="currentColor" /><circle cx="4.5" cy="17" r="1.5" fill="currentColor" /><circle cx="19.5" cy="17" r="1.5" fill="currentColor" /><path d="M12 9V5m-2.5 9-3.6 2m8.6-2 3.6 2" stroke="currentColor" strokeWidth="1.5" /></svg> }
