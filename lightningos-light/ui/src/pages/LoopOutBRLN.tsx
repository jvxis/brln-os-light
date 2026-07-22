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

      <div className="flex flex-wrap gap-2">
        <button className={tab === 'new' ? 'btn-primary' : 'btn-secondary'} type="button" onClick={() => setTab('new')}>{t('loopOutBrln.newLoop')}</button>
        <button className={tab === 'history' ? 'btn-primary' : 'btn-secondary'} type="button" onClick={() => setTab('history')}>{t('loopOutBrln.history')}</button>
      </div>

      {tab === 'new' && (
        <form className="grid gap-6 xl:grid-cols-[1.05fr_.95fr]" onSubmit={requestPreview}>
          <div className="section-card space-y-6">
            <div>
              <p className="text-xs uppercase tracking-[.22em] text-brass">{t('loopOutBrln.destinationEyebrow')}</p>
              <h2 className="mt-2 text-2xl font-semibold">{t('loopOutBrln.configureTitle')}</h2>
              <p className="mt-2 text-sm text-fog/60">{t('loopOutBrln.configureBody')}</p>
            </div>
            <Field label={t('loopOutBrln.lightningAddress')} hint={t('loopOutBrln.lightningAddressHint')}>
              <input className="input w-full" value={address} onChange={(e) => { setAddress(e.target.value); clearPreview() }} placeholder="name@example.com" />
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t('loopOutBrln.totalAmount')}><input className="input w-full" type="number" min="1" value={totalSat} onChange={(e) => { setTotalSat(e.target.value); clearPreview() }} /></Field>
              <Field label={t('loopOutBrln.trancheAmount')} hint={t('loopOutBrln.trancheHint')}><input className="input w-full" type="number" min="1" value={trancheSat} onChange={(e) => { setTrancheSat(e.target.value); clearPreview() }} /></Field>
              <Field label={t('loopOutBrln.minLocal')} hint={t('loopOutBrln.minLocalHint')}><div className="relative"><input className="input w-full pr-9" type="number" min="0" max="99.99" step="0.1" value={minLocalPercent} onChange={(e) => { setMinLocalPercent(e.target.value); clearPreview() }} /><span className="absolute right-3 top-2.5 text-sm text-fog/45">%</span></div></Field>
              <Field label={t('loopOutBrln.maxFee')}><div className="relative"><input className="input w-full pr-14" type="number" min="1" max="1000000" value={maxFeePPM} onChange={(e) => { setMaxFeePPM(e.target.value); clearPreview() }} /><span className="absolute right-3 top-2.5 text-xs text-fog/45">PPM</span></div></Field>
              <Field label={t('loopOutBrln.interval')}><input className="input w-full" type="number" min="0" max="86400" value={intervalSeconds} onChange={(e) => { setIntervalSeconds(e.target.value); clearPreview() }} /></Field>
              <Field label={t('loopOutBrln.timeout')}><input className="input w-full" type="number" min="30" max="600" value={timeoutSeconds} onChange={(e) => { setTimeoutSeconds(e.target.value); clearPreview() }} /></Field>
            </div>
            <Field label={t('loopOutBrln.comment')} hint={t('loopOutBrln.commentHint')}><input className="input w-full" maxLength={512} value={comment} onChange={(e) => { setComment(e.target.value); clearPreview() }} /></Field>

            <div className="space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div><p className="text-sm font-medium">{t('loopOutBrln.sources')}</p><p className="text-xs text-fog/50">{t('loopOutBrln.sourcesHint')}</p></div>
                <div className="flex rounded-xl border border-white/10 bg-ink/40 p-1">
                  <button type="button" className={`rounded-lg px-3 py-1.5 text-xs ${sourceMode === 'auto' ? 'bg-brass text-ink' : 'text-fog/60'}`} onClick={() => { setSourceMode('auto'); clearPreview() }}>{t('loopOutBrln.automatic')}</button>
                  <button type="button" className={`rounded-lg px-3 py-1.5 text-xs ${sourceMode === 'manual' ? 'bg-brass text-ink' : 'text-fog/60'}`} onClick={() => { setSourceMode('manual'); clearPreview() }}>{t('loopOutBrln.manual')}</button>
                </div>
              </div>
              {sourceMode === 'manual' && (
                <div className="space-y-3 rounded-2xl border border-white/10 bg-ink/25 p-3">
                  <input className="input w-full" value={channelSearch} onChange={(e) => setChannelSearch(e.target.value)} placeholder={t('loopOutBrln.searchChannels')} />
                  <div className="max-h-72 space-y-2 overflow-y-auto pr-1">
                    {visibleChannels.map((channel) => {
                      const id = channelID(channel)
                      const localPct = Number(channel.local_balance_sat || 0) * 100 / Math.max(1, Number(channel.capacity_sat || 0))
                      const selected = selectedChannels.has(id)
                      const projected = previewByChannel.get(id)
                      return <button key={id} type="button" onClick={() => toggleChannel(id)} className={`w-full rounded-xl border p-3 text-left transition ${selected ? 'border-brass/55 bg-brass/10' : 'border-white/10 bg-black/10 hover:border-white/20'}`}>
                        <div className="flex items-center justify-between gap-3"><span className="truncate text-sm font-medium">{channel.peer_alias || id}</span><span className={`h-2.5 w-2.5 rounded-full ${channel.active && !channel.local_disabled ? 'bg-emerald-400' : 'bg-rose-400'}`} /></div>
                        <div className="mt-2 flex justify-between text-xs text-fog/55"><span>{sats(channel.local_balance_sat)}</span><span>{localPct.toFixed(1)}%</span></div>
                        <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-white/5"><div className="h-full rounded-full bg-gradient-to-r from-brass/60 to-amber-300" style={{ width: `${Math.min(100, localPct)}%` }} /></div>
                        {projected && <p className="mt-2 text-[11px] text-fog/45">{t('loopOutBrln.drainable')}: {sats(projected.drainable_sat)}</p>}
                      </button>
                    })}
                  </div>
                </div>
              )}
            </div>
            <button className="btn-primary w-full justify-center" type="submit" disabled={!formValid || Boolean(busy) || Boolean(activeJob)}>{busy === 'preview' ? t('loopOutBrln.validating') : t('loopOutBrln.review')}</button>
            {activeJob && <p className="text-center text-xs text-amber-200/75">{t('loopOutBrln.oneActiveOnly')}</p>}
          </div>

          <ReviewPanel preview={preview} request={requestPayload} sats={sats} t={t} busy={busy} onStart={() => void createJob()} />
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
  return <div className="relative overflow-hidden rounded-3xl border border-brass/20 bg-[radial-gradient(circle_at_15%_10%,rgba(245,158,11,.18),transparent_34%),linear-gradient(135deg,rgba(13,17,23,.98),rgba(21,25,32,.95))] p-6 shadow-panel sm:p-8">
    <div className="absolute -right-16 -top-20 h-56 w-56 rounded-full border border-brass/10" /><div className="absolute -right-4 -top-8 h-36 w-36 rounded-full border border-brass/10" />
    <div className="relative flex flex-col gap-5 sm:flex-row sm:items-center">
      <img src={loopOutBRLNIcon} alt="Loop Out BRLN" className="h-20 w-20 rounded-3xl object-cover shadow-[0_0_38px_rgba(245,158,11,.22)]" />
      <div><p className="text-xs uppercase tracking-[.3em] text-brass">{t('loopOutBrln.eyebrow')}</p><h1 className="mt-2 text-3xl font-semibold tracking-tight sm:text-4xl">Loop Out BR⚡LN</h1><p className="mt-2 max-w-2xl text-sm leading-6 text-fog/65">{t('loopOutBrln.subtitle')}</p></div>
    </div>
  </div>
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return <label className="block"><span className="mb-1.5 block text-sm font-medium">{label}</span>{children}{hint && <span className="mt-1.5 block text-xs leading-5 text-fog/45">{hint}</span>}</label>
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

function ReviewPanel({ preview, request, sats, t, busy, onStart }: { preview: LoopOutBRLNPreview | null; request: LoopOutBRLNRequest; sats: (n?: number) => string; t: any; busy: string; onStart: () => void }) {
  if (!preview) return <div className="section-card flex min-h-[420px] flex-col items-center justify-center text-center"><div className="flex h-16 w-16 items-center justify-center rounded-2xl border border-brass/25 bg-brass/10 text-3xl text-brass">↗</div><h3 className="mt-5 text-xl font-semibold">{t('loopOutBrln.reviewEmptyTitle')}</h3><p className="mt-2 max-w-sm text-sm leading-6 text-fog/55">{t('loopOutBrln.reviewEmptyBody')}</p></div>
  return <div className="section-card space-y-5 self-start xl:sticky xl:top-5">
    <div className="flex items-start justify-between gap-3"><div><p className="text-xs uppercase tracking-[.22em] text-brass">{t('loopOutBrln.reviewEyebrow')}</p><h2 className="mt-2 text-2xl font-semibold">{t('loopOutBrln.reviewTitle')}</h2></div><span className={`rounded-full border px-3 py-1 text-xs ${preview.can_start ? 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200' : 'border-amber-400/30 bg-amber-500/15 text-amber-200'}`}>{preview.can_start ? t('loopOutBrln.ready') : t('loopOutBrln.needsAttention')}</span></div>
    <div className="grid grid-cols-2 gap-3"><Metric label={t('loopOutBrln.destination')} value={preview.lightning_address} /><Metric label={t('loopOutBrln.total')} value={sats(preview.total_sat)} /><Metric label={t('loopOutBrln.payments')} value={String(preview.estimated_parts)} /><Metric label={t('loopOutBrln.lastPayment')} value={sats(preview.last_tranche_sat)} /><Metric label={t('loopOutBrln.maxFees')} value={sats(preview.max_fee_total_sat)} /><Metric label={t('loopOutBrln.drainable')} value={sats(preview.total_drainable_sat)} /></div>
    <div className="rounded-2xl border border-white/10 bg-black/15 p-4"><div className="flex justify-between text-xs text-fog/55"><span>{t('loopOutBrln.feeBudget')}</span><span>{request.max_fee_ppm.toLocaleString()} PPM</span></div><div className="mt-2 flex justify-between text-xs text-fog/55"><span>{t('loopOutBrln.liquidityFloor')}</span><span>{request.min_local_percent}%</span></div><div className="mt-2 flex justify-between text-xs text-fog/55"><span>{t('loopOutBrln.interval')}</span><span>{request.interval_seconds}s</span></div></div>
    {(preview.warnings || []).map((warning) => <p key={warning} className="rounded-xl border border-amber-400/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-100/80">{t(`loopOutBrln.warnings.${warning}`, { defaultValue: warning })}</p>)}
    <p className="text-xs leading-5 text-fog/45">{t('loopOutBrln.approvalNotice')}</p>
    <button className="btn-primary w-full justify-center" type="button" disabled={!preview.can_start || Boolean(busy)} onClick={onStart}>{busy === 'create' ? t('loopOutBrln.starting') : t('loopOutBrln.start')}</button>
  </div>
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
  return <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 px-4"><form className="w-full max-w-md rounded-3xl border border-white/10 bg-ink p-6 shadow-2xl" onSubmit={onSubmit}><p className="text-xs uppercase tracking-[.2em] text-brass">{t('loopOutBrln.security')}</p><h3 className="mt-2 text-2xl font-semibold">{t('loopOutBrln.confirmTitle')}</h3><p className="mt-2 text-sm leading-6 text-fog/60">{t('loopOutBrln.confirmBody')}</p><input className="input mt-5 w-full" type="password" autoFocus value={password} onChange={(e) => onPassword(e.target.value)} placeholder={t('auth.password')} /><div className="mt-6 flex justify-end gap-3"><button className="btn-secondary" type="button" onClick={onClose}>{t('common.cancel')}</button><button className="btn-primary" type="submit" disabled={!password.trim() || Boolean(busy)}>{t('loopOutBrln.confirm')}</button></div></form></div>
}
