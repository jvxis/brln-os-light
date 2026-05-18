import { useCallback, useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { getAuditEvents } from '../api'
import { getLocale } from '../i18n'

type AuditEvent = {
  id: number
  ts: string
  session_id?: string
  action: string
  target?: string
  metadata?: unknown
  ip?: string
}

type AuditEventsResponse = {
  items?: AuditEvent[]
  limit?: number
  retention_days?: number
}

type AuditFilters = {
  action: string
  sessionID: string
  target: string
  limit: number
}

const defaultFilters: AuditFilters = {
  action: '',
  sessionID: '',
  target: '',
  limit: 100
}

const limitOptions = [100, 200, 500]

const trimMiddle = (value: string, max = 36) => {
  if (value.length <= max) return value
  const head = Math.ceil((max - 3) / 2)
  const tail = Math.floor((max - 3) / 2)
  return `${value.slice(0, head)}...${value.slice(value.length - tail)}`
}

const formatMetadata = (value: unknown) => {
  if (value === undefined || value === null || value === '') return '{}'
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

export default function AuditLog() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [filters, setFilters] = useState<AuditFilters>(defaultFilters)
  const [draft, setDraft] = useState<AuditFilters>(defaultFilters)
  const [items, setItems] = useState<AuditEvent[]>([])
  const [retentionDays, setRetentionDays] = useState<number | null>(null)
  const [status, setStatus] = useState('')
  const [loading, setLoading] = useState(false)

  const formatTimestamp = useCallback((value: string) => {
    if (!value) return t('common.unknownTime')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return t('common.unknownTime')
    return parsed.toLocaleString(locale, {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false
    })
  }, [locale, t])

  const retentionLabel = useMemo(() => {
    if (retentionDays === null) return ''
    if (retentionDays <= 0) return t('auditLog.retentionForever')
    return t('auditLog.retention', { days: retentionDays })
  }, [retentionDays, t])

  const load = useCallback(async () => {
    setLoading(true)
    setStatus(t('auditLog.loading'))
    try {
      const res = await getAuditEvents({
        limit: filters.limit,
        action: filters.action || undefined,
        session_id: filters.sessionID || undefined,
        target: filters.target || undefined
      }) as AuditEventsResponse
      setItems(Array.isArray(res?.items) ? res.items : [])
      setRetentionDays(typeof res?.retention_days === 'number' ? res.retention_days : null)
      setStatus('')
    } catch (err: any) {
      setStatus(err?.message || t('auditLog.unavailable'))
    } finally {
      setLoading(false)
    }
  }, [filters, t])

  useEffect(() => {
    void load()
  }, [load])

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault()
    setFilters({
      action: draft.action.trim(),
      sessionID: draft.sessionID.trim(),
      target: draft.target.trim(),
      limit: draft.limit
    })
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">{t('auditLog.title')}</h2>
            <p className="text-fog/60">{t('auditLog.subtitle')}</p>
          </div>
          {retentionLabel && (
            <span className="rounded-full border border-white/10 bg-ink/70 px-3 py-1 text-xs text-fog/70">
              {retentionLabel}
            </span>
          )}
        </div>
      </div>

      <form className="section-card space-y-4" onSubmit={handleSubmit}>
        <div className="flex flex-wrap items-end gap-3">
          <label className="space-y-1">
            <span className="block text-xs text-fog/60">{t('auditLog.action')}</span>
            <input
              className="input-field w-[220px]"
              placeholder={t('auditLog.actionPlaceholder')}
              value={draft.action}
              onChange={(event) => setDraft((prev) => ({ ...prev, action: event.target.value }))}
            />
          </label>
          <label className="space-y-1">
            <span className="block text-xs text-fog/60">{t('auditLog.session')}</span>
            <input
              className="input-field w-[240px]"
              placeholder={t('auditLog.sessionPlaceholder')}
              value={draft.sessionID}
              onChange={(event) => setDraft((prev) => ({ ...prev, sessionID: event.target.value }))}
            />
          </label>
          <label className="space-y-1">
            <span className="block text-xs text-fog/60">{t('auditLog.target')}</span>
            <input
              className="input-field w-[260px]"
              placeholder={t('auditLog.targetPlaceholder')}
              value={draft.target}
              onChange={(event) => setDraft((prev) => ({ ...prev, target: event.target.value }))}
            />
          </label>
          <label className="space-y-1">
            <span className="block text-xs text-fog/60">{t('auditLog.limit')}</span>
            <select
              className="input-field w-[120px]"
              value={draft.limit}
              onChange={(event) => setDraft((prev) => ({ ...prev, limit: Number(event.target.value) }))}
            >
              {limitOptions.map((value) => (
                <option key={value} value={value}>{value}</option>
              ))}
            </select>
          </label>
          <button className="btn-primary" type="submit" disabled={loading}>
            {t('auditLog.applyFilters')}
          </button>
          <button className="btn-secondary" type="button" onClick={() => void load()} disabled={loading}>
            {t('common.refresh')}
          </button>
        </div>
      </form>

      <div className="section-card">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('auditLog.recentEvents')}</h3>
          <span className="text-xs text-fog/50">{t('auditLog.resultCount', { count: items.length })}</span>
        </div>
        {status && <p className="mt-4 text-sm text-brass">{status}</p>}
        {!status && items.length === 0 && (
          <p className="mt-4 text-sm text-fog/60">{t('auditLog.empty')}</p>
        )}
        {!status && items.length > 0 && (
          <div className="mt-4 overflow-x-auto">
            <table className="min-w-full text-left text-sm">
              <thead className="border-b border-white/10 text-xs uppercase text-fog/50">
                <tr>
                  <th className="px-3 py-2 font-medium">{t('auditLog.time')}</th>
                  <th className="px-3 py-2 font-medium">{t('auditLog.action')}</th>
                  <th className="px-3 py-2 font-medium">{t('auditLog.target')}</th>
                  <th className="px-3 py-2 font-medium">{t('auditLog.session')}</th>
                  <th className="px-3 py-2 font-medium">{t('auditLog.ip')}</th>
                  <th className="px-3 py-2 font-medium">{t('auditLog.metadata')}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={item.id} className="border-b border-white/10 align-top">
                    <td className="whitespace-nowrap px-3 py-3 text-xs text-fog/60">{formatTimestamp(item.ts)}</td>
                    <td className="px-3 py-3 font-mono text-xs text-emerald-100">{item.action || t('common.unknown')}</td>
                    <td className="px-3 py-3 font-mono text-xs text-fog" title={item.target || ''}>
                      {item.target ? trimMiddle(item.target) : t('common.na')}
                    </td>
                    <td className="px-3 py-3 font-mono text-xs text-fog/70" title={item.session_id || ''}>
                      {item.session_id ? trimMiddle(item.session_id) : t('common.na')}
                    </td>
                    <td className="whitespace-nowrap px-3 py-3 font-mono text-xs text-fog/70">
                      {item.ip || t('common.na')}
                    </td>
                    <td className="min-w-[260px] max-w-[520px] px-3 py-3">
                      <pre className="max-h-32 overflow-auto whitespace-pre-wrap break-all rounded-xl border border-white/10 bg-ink/60 p-3 text-xs text-fog/70">{formatMetadata(item.metadata)}</pre>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  )
}
