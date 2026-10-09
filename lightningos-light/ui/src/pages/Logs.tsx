import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getApps, queryLogs, type LogEntry, type LogQuery, type LogQueryResult } from '../api'

const baseServices = [
  ['lnd', 'lnd'], ['bitcoin', 'bitcoin'], ['autofee', 'autofee'],
  ['lndUpgrade', 'lnd-upgrade'], ['appUpgrade', 'app-upgrade'],
  ['manager', 'lightningos-manager'], ['elements', 'lightningos-elements'],
  ['peerswapd', 'lightningos-peerswapd'], ['psweb', 'lightningos-psweb'], ['postgres', 'postgresql']
]
const fedimintServices = [['fedimintGuardian', 'fedimint-guardian'], ['fedimintGateway', 'fedimint-gateway']]
const levels = ['error', 'warning', 'info', 'debug', 'unknown']
const events = ['connection', 'channel_open', 'cooperative_close', 'force_close', 'lifecycle', 'sync', 'other']
const localInput = (date: Date) => new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 19)
const initialForm = () => ({ period: false, since: localInput(new Date(Date.now() - 3600000)), until: localInput(new Date()), level: '', event: '', q: '', limit: 200 })
const lineText = (entry: LogEntry) => [entry.time, entry.message].filter(Boolean).join(' ')

export default function Logs() {
  const { t } = useTranslation()
  const [service, setService] = useState('lnd')
  const [apps, setApps] = useState<string[]>([])
  const [form, setForm] = useState(initialForm)
  const [result, setResult] = useState<LogQueryResult | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [capabilities, setCapabilities] = useState<LogQueryResult['capabilities'] | null>(null)
  const requestID = useRef(0)
  const activeRequest = useRef<AbortController | null>(null)
  const resultsRef = useRef<HTMLDivElement | null>(null)
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone
  const services = [...baseServices, ...fedimintServices.filter(([, id]) => apps.includes(id))]
  const change = (patch: Partial<ReturnType<typeof initialForm>>) => { setForm(value => ({ ...value, ...patch })); setDirty(true) }

  useEffect(() => {
    let active = true
    const loadApps = async () => {
      try {
        const data = await getApps()
        if (active) setApps(Array.isArray(data) ? data.filter(app => app.installed).map(app => app.id) : [])
      } catch { if (active) setApps([]) }
    }
    void loadApps()
    window.addEventListener('apps:changed', loadApps)
    return () => { active = false; window.removeEventListener('apps:changed', loadApps) }
  }, [])

  useEffect(() => {
    if (fedimintServices.some(([, id]) => id === service) && !apps.includes(service)) setService('lnd')
  }, [apps, service])

  const load = useCallback(async (query: LogQuery) => {
    const id = ++requestID.current
    activeRequest.current?.abort()
    const controller = new AbortController()
    activeRequest.current = controller
    setLoading(true)
    setError('')
    setResult(null)
    try {
      const response = await queryLogs(query, controller.signal)
      if (requestID.current === id) { setResult(response); setCapabilities(response.capabilities); setDirty(false) }
    } catch (err: any) {
      if (requestID.current === id) setError(err?.message || t('logs.fetchFailed'))
    } finally {
      if (requestID.current === id) setLoading(false)
    }
  }, [t])

  useEffect(() => {
    setForm(initialForm())
    setCapabilities(null)
    setDirty(false)
    void load({ service, limit: 200 })
    return () => { requestID.current++; activeRequest.current?.abort() }
  }, [service, load])

  useEffect(() => {
    if (resultsRef.current) resultsRef.current.scrollTop = resultsRef.current.scrollHeight
  }, [result])

  const submit = () => {
    const query: LogQuery = { service, limit: form.limit, level: form.level, event: service === 'lnd' ? form.event : '', q: form.q }
    if (form.period) {
      const since = new Date(form.since), until = new Date(form.until)
      if (!Number.isFinite(since.getTime()) || !Number.isFinite(until.getTime()) || since > until || until.getTime() - since.getTime() > 7 * 86400000) {
        setError(t('logs.explorer.invalidPeriod'))
        return
      }
      query.since = since.toISOString()
      query.until = until.toISOString()
    }
    void load(query)
  }

  const preset = (minutes: number) => {
    const until = new Date()
    const since = minutes ? new Date(until.getTime() - minutes * 60000) : new Date(until.getFullYear(), until.getMonth(), until.getDate())
    change({ period: true, since: localInput(since), until: localInput(until) })
  }

  const exportText = () => {
    if (!result) return
    const query = result.query
    const text = [
      'LightningOS — ' + t('logs.title'),
      t('logs.explorer.service') + ': ' + query.service,
      t('logs.explorer.source') + ': ' + result.source,
      t('logs.explorer.timezone') + ': ' + timezone + ' (' + t('logs.explorer.exportUTC') + ')',
      t('logs.explorer.period') + ': ' + (query.since && !query.since.startsWith('0001-') ? query.since : t('logs.explorer.recent')) + ' → ' + query.until,
      t('logs.explorer.level') + ': ' + (query.level ? t('logs.explorer.levels.' + query.level) : t('logs.explorer.all')),
      t('logs.explorer.event') + ': ' + (query.event ? t('logs.explorer.events.' + query.event) : t('logs.explorer.all')),
      t('logs.explorer.search') + ': ' + (query.q || '—'),
      t('logs.explorer.queriedAt') + ': ' + result.queried_at,
      t('logs.explorer.count', { count: result.entries.length, matched: result.matched, scanned: result.scanned }),
      t('logs.explorer.retention'),
      ...(result.partial ? [t('logs.explorer.reasons.' + result.reason)] : []),
      ...(result.result_limited ? [t('logs.explorer.resultLimited')] : []),
      t('logs.explorer.exportScope'), '',
      ...result.entries.map(lineText)
    ].join('\n')
    const url = URL.createObjectURL(new Blob([text + '\n'], { type: 'text/plain;charset=utf-8' }))
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = 'los-logs-' + query.service + '-' + result.queried_at.replace(/[:.]/g, '-') + '.txt'
    anchor.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  const timestamp = (value?: string) => value ? new Date(value).toLocaleString(undefined, { timeZone: timezone }) : ''
  const levelClass = (level: string) => level === 'error' ? 'text-rose-300' : level === 'warning' ? 'text-brass' : 'text-fog/60'

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('logs.title')}</h2>
        <p className="text-fog/60">{t('logs.explorer.subtitle')}</p>
      </div>
      <div className="section-card space-y-4">
        <div className="flex flex-wrap gap-2" aria-label={t('logs.explorer.service')}>
          {services.map(([label, value]) => (
            <button key={value} type="button" className={service === value ? 'btn-primary' : 'btn-secondary'} aria-pressed={service === value} onClick={() => setService(value)}>
              {t('logs.services.' + label)}
            </button>
          ))}
        </div>
        <form onSubmit={event => { event.preventDefault(); submit() }} className="space-y-3">
          <fieldset disabled={loading || !capabilities} className="space-y-3 min-w-0">
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              <label className="space-y-1 min-w-0">
                <span className="text-sm">{t('logs.explorer.period')}</span>
                <select aria-label={t('logs.explorer.period')} className="input-field w-full" value={form.period ? 'period' : 'recent'} onChange={event => change({ period: event.target.value === 'period' })}>
                  <option value="recent">{t('logs.explorer.recent')}</option>
                  <option value="period" disabled={!capabilities?.period}>{t('logs.explorer.customPeriod')}</option>
                </select>
              </label>
              <label className="space-y-1 min-w-0">
                <span className="text-sm">{t('logs.explorer.level')}</span>
                <select aria-label={t('logs.explorer.level')} className="input-field w-full" disabled={!capabilities?.filters} value={form.level} onChange={event => change({ level: event.target.value })}>
                  <option value="">{t('logs.explorer.all')}</option>
                  {levels.map(level => <option key={level} value={level}>{t('logs.explorer.levels.' + level)}</option>)}
                </select>
              </label>
              <label className="space-y-1 min-w-0">
                <span className="text-sm">{t('logs.explorer.event')}</span>
                <select aria-label={t('logs.explorer.event')} className="input-field w-full" disabled={!capabilities?.events} value={form.event} onChange={event => change({ event: event.target.value })}>
                  <option value="">{t('logs.explorer.all')}</option>
                  {events.map(value => <option key={value} value={value}>{t('logs.explorer.events.' + value)}</option>)}
                </select>
              </label>
            </div>
            {capabilities?.period && <div className="flex flex-wrap gap-2">
              {[15, 60, 1440, 0].map(minutes => <button type="button" className="btn-secondary text-sm" key={minutes} onClick={() => preset(minutes)}>{t('logs.explorer.presets.' + minutes)}</button>)}
            </div>}
            {form.period && <div className="grid gap-3 sm:grid-cols-2">
              <label className="space-y-1 min-w-0"><span className="text-sm">{t('logs.explorer.since')}</span><input type="datetime-local" step="1" required className="input-field w-full min-w-0" value={form.since} onChange={event => change({ since: event.target.value })} /></label>
              <label className="space-y-1 min-w-0"><span className="text-sm">{t('logs.explorer.until')}</span><input type="datetime-local" step="1" required className="input-field w-full min-w-0" value={form.until} onChange={event => change({ until: event.target.value })} /></label>
            </div>}
            <div className="flex flex-wrap items-end gap-3">
              <label className="space-y-1 flex-1 min-w-0"><span className="text-sm">{t('logs.explorer.search')}</span><input type="search" className="input-field w-full" disabled={!capabilities?.filters} maxLength={256} value={form.q} onChange={event => change({ q: event.target.value })} placeholder={t('logs.explorer.searchPlaceholder')} /></label>
              <label className="space-y-1"><span className="text-sm">{t('logs.explorer.limit')}</span><select aria-label={t('logs.explorer.limit')} className="input-field block" value={form.limit} onChange={event => change({ limit: Number(event.target.value) })}>{[200, 500, 1000].map(value => <option key={value} value={value}>{value}</option>)}</select></label>
              <button type="submit" className="btn-primary">{t('logs.explorer.query')}</button>
            </div>
          </fieldset>
        </form>
        <p className="text-xs text-fog/60">{t('logs.explorer.timezone')}: {timezone}. {t('logs.explorer.retention')}</p>
        {capabilities && !capabilities.period && <p className="text-sm text-brass">{t('logs.explorer.recentOnly')}</p>}
        {service === 'lnd' && <p className="text-xs text-fog/60">{t('logs.explorer.classification')}</p>}
        {dirty && result && <p role="status" className="text-sm text-brass">{t('logs.explorer.pending')}</p>}
        {loading && <p role="status">{t('logs.loading')}</p>}
        {error && <div role="alert" className="text-sm text-rose-300">{error} {!capabilities && <button type="button" className="btn-secondary ml-2" onClick={() => void load({ service, limit: 200 })}>{t('common.refresh')}</button>}</div>}
        {result && <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="text-xs text-fog/60">
              <p>{t('logs.explorer.count', { count: result.entries.length, matched: result.matched, scanned: result.scanned })}</p>
              <p>{t('logs.explorer.source')}: {result.source} · {t('logs.explorer.queriedAt')}: {timestamp(result.queried_at)}</p>
            </div>
            <button type="button" className="btn-secondary" disabled={loading || dirty || !result.entries.length} onClick={exportText}>{t('logs.explorer.export')}</button>
          </div>
          {result.partial && <p role="status" className="text-sm text-brass">{t('logs.explorer.reasons.' + result.reason)}</p>}
          {result.result_limited && <p role="status" className="text-sm text-brass">{t('logs.explorer.resultLimited')}</p>}
          <div ref={resultsRef} className="bg-ink/70 border border-white/10 rounded-2xl p-3 text-xs font-mono min-h-[320px] max-h-[65vh] overflow-y-auto" aria-label={t('logs.explorer.results')} data-testid="log-results">
            {!result.entries.length && <p>{t('logs.noLogs')}</p>}
            {result.entries.map((entry, index) => <article key={index} className="py-2 border-b border-white/5 last:border-0">
              <div className="flex flex-wrap gap-x-3 gap-y-1 mb-1">
                <time dateTime={entry.time} className="text-fog/60">{timestamp(entry.time)}</time>
                <span className={levelClass(entry.level)}>{t('logs.explorer.levels.' + entry.level)}</span>
                {capabilities?.events && <span className="text-brass">{t('logs.explorer.events.' + entry.event)}</span>}
              </div>
              <pre className="whitespace-pre-wrap break-words font-inherit" style={{ overflowWrap: 'anywhere' }}>{entry.message}</pre>
              {!!entry.context?.length && <details className="mt-2 text-fog/70">
                <summary className="cursor-pointer w-fit">{t('logs.explorer.context')}</summary>
                <p className="my-2 text-fog/50">{t('logs.explorer.contextHint')}</p>
                <pre className="whitespace-pre-wrap break-words border-l-2 border-brass/40 pl-3" style={{ overflowWrap: 'anywhere' }}>{entry.context.map(lineText).join('\n')}</pre>
              </details>}
            </article>)}
          </div>
        </>}
      </div>
    </section>
  )
}
