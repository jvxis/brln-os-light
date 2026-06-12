import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import { getPostgresMaintenance, runPostgresCleanup, runPostgresGraphHistoryCompact, runPostgresVacuum } from '../../api'
import { formatGB, formatMB, formatSats, formatTimestamp } from './formatters'
import StatusBadge from './StatusBadge'

type DbTableStat = {
  name: string
  total_bytes?: number
  row_estimate?: number
  dead_tuples?: number
  last_autovacuum?: string | null
  has_retention?: boolean
  retention_label?: string
}

type DbMaintenanceOverview = {
  database?: string
  total_bytes?: number
  tables?: DbTableStat[]
  graph_history_compaction?: DbGraphHistoryCompactionStatus
}

type DbActionResult = { table: string; rows_removed?: number; error?: string }
type DbActionResponse = { results?: DbActionResult[] }
type DbActionKind = 'cleanup' | 'vacuum' | 'compact'

type DbGraphHistoryCompactionStatus = {
  table?: string
  available?: boolean
  running?: boolean
  reason?: string
  physical_bytes?: number
  estimated_live_bytes?: number
  history_max_bytes?: number
  reclaimable_bytes?: number
  reclaimable_ratio?: number
  cleanup_available?: boolean
  estimated_live_exceeds_cap?: boolean
  effective_retention_days?: number
  min_reclaimable_bytes?: number
  min_reclaimable_ratio?: number
  lock_timeout_seconds?: number
  started_at?: string
  completed_at?: string
  last_error?: string
  physical_bytes_before?: number
  physical_bytes_after?: number
  last_reclaimed_bytes?: number
}

type DbMaintenanceModalProps = {
  open: boolean
  onClose: () => void
}

export default function DbMaintenanceModal({ open, onClose }: DbMaintenanceModalProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)

  const [overview, setOverview] = useState<DbMaintenanceOverview | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [action, setAction] = useState<DbActionKind | null>(null)
  const [results, setResults] = useState<DbActionResult[] | null>(null)
  const [resultKind, setResultKind] = useState<DbActionKind | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const data = (await getPostgresMaintenance()) as DbMaintenanceOverview
      setOverview(data)
    } catch (err: any) {
      setError(err?.message || t('dbMaintenance.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    if (!open) return
    setResults(null)
    setResultKind(null)
    void load()
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [open, load, onClose])

  useEffect(() => {
    if (!open || !overview?.graph_history_compaction?.running) return
    const timer = window.setInterval(() => void load(), 5000)
    return () => window.clearInterval(timer)
  }, [load, open, overview?.graph_history_compaction?.running])

  if (!open) return null

  const fmtSize = (bytes?: number) => {
    const mb = Number(bytes || 0) / (1024 * 1024)
    return mb >= 1024 ? formatGB(locale, mb / 1024) : formatMB(locale, mb)
  }

  const runAction = async (kind: DbActionKind) => {
    if (kind === 'compact' && !window.confirm(t('dbMaintenance.compact.confirm'))) return
    setAction(kind)
    setResults(null)
    setError('')
    try {
      if (kind === 'compact') {
        const compactStatus = await runPostgresGraphHistoryCompact() as DbGraphHistoryCompactionStatus
        setOverview((current) => current ? { ...current, graph_history_compaction: compactStatus } : current)
        setResultKind(kind)
        await load()
        return
      }
      const resp = (kind === 'cleanup' ? await runPostgresCleanup() : await runPostgresVacuum()) as DbActionResponse
      setResults(Array.isArray(resp?.results) ? resp.results : [])
      setResultKind(kind)
      await load()
    } catch (err: any) {
      setError(err?.message || t('dbMaintenance.actionFailed'))
    } finally {
      setAction(null)
    }
  }

  const tables = Array.isArray(overview?.tables) ? overview!.tables : []
  const compaction = overview?.graph_history_compaction
  const operationBusy = action !== null
  const busy = operationBusy || Boolean(compaction?.running)
  const totalRemoved = (results || []).reduce((sum, r) => sum + Number(r.rows_removed || 0), 0)
  const actionErrors = (results || []).filter((r) => r.error)
  const formatRatio = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(value * 100)}%`
  }
  const compactReason = String(compaction?.reason || '').trim()
  const compactReasonKey = compactReason ? `dbMaintenance.compact.reasons.${compactReason}` : ''
  const compactTone = compaction?.running ? 'warn' : compaction?.available ? 'ok' : compaction?.last_error ? 'danger' : 'muted'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center px-4 py-6">
      <button
        type="button"
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={onClose}
        aria-label={t('common.close')}
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="db-maintenance-title"
        className="relative z-10 flex max-h-[86vh] w-full max-w-4xl flex-col overflow-hidden rounded-3xl border border-white/10 bg-slate/95 shadow-panel"
      >
        <div className="flex flex-col gap-4 border-b border-white/10 p-6 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <p className="text-xs uppercase tracking-[0.3em] text-fog/50">{t('dbMaintenance.kicker')}</p>
            <div className="mt-3 flex flex-wrap items-center gap-3">
              <h2 id="db-maintenance-title" className="text-2xl font-semibold">{t('dbMaintenance.title')}</h2>
              <StatusBadge label={overview?.database || t('common.na')} tone="muted" size="md" />
            </div>
            <p className="mt-2 text-sm text-fog/60">
              {t('dbMaintenance.totalSize', { value: fmtSize(overview?.total_bytes) })}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              className={`btn-secondary text-sm ${loading ? 'opacity-60 pointer-events-none' : ''}`}
              onClick={() => void load()}
              disabled={loading || busy}
            >
              {loading ? t('dbMaintenance.loading') : t('common.refresh')}
            </button>
            <button
              type="button"
              className="inline-flex h-10 w-10 items-center justify-center rounded-full border border-white/10 bg-ink/50 text-fog/70 transition hover:border-white/30 hover:text-white"
              onClick={onClose}
              aria-label={t('common.close')}
            >
              <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8">
                <path d="M6 6l12 12M18 6l-12 12" />
              </svg>
            </button>
          </div>
        </div>

        <div className="overflow-y-auto p-6">
          <p className="text-sm text-fog/60">{t('dbMaintenance.description')}</p>

          <div className="mt-4 flex flex-wrap gap-3">
            <button
              type="button"
              className={`btn-primary text-sm ${busy ? 'opacity-60 pointer-events-none' : ''}`}
              onClick={() => void runAction('cleanup')}
              disabled={busy}
            >
              {action === 'cleanup' ? t('dbMaintenance.cleanupRunning') : t('dbMaintenance.runCleanup')}
            </button>
            <button
              type="button"
              className={`btn-secondary text-sm ${busy ? 'opacity-60 pointer-events-none' : ''}`}
              onClick={() => void runAction('vacuum')}
              disabled={busy}
            >
              {action === 'vacuum' ? t('dbMaintenance.vacuumRunning') : t('dbMaintenance.runVacuum')}
            </button>
          </div>

          {error && (
            <div className="mt-4 rounded-2xl border border-rose-400/30 bg-rose-500/15 px-4 py-3 text-sm text-rose-200">
              {error}
            </div>
          )}

          {results && !error && (
            <div className="mt-4 rounded-2xl border border-emerald-400/25 bg-emerald-500/10 px-4 py-3 text-sm text-emerald-100">
              {resultKind === 'cleanup'
                ? t('dbMaintenance.cleanupDone', { rows: formatSats(locale, totalRemoved) })
                : resultKind === 'vacuum'
                  ? t('dbMaintenance.vacuumDone')
                  : t('dbMaintenance.compact.started')}
              {actionErrors.length > 0 ? (
                <p className="mt-1 text-rose-200">{t('dbMaintenance.actionErrors', { count: actionErrors.length })}</p>
              ) : null}
            </div>
          )}

          <div className="mt-5 rounded-2xl border border-amber-400/20 bg-amber-500/[0.07] p-4">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <p className="text-sm font-semibold text-fog">{t('dbMaintenance.compact.title')}</p>
                  <StatusBadge
                    label={compaction?.running
                      ? t('dbMaintenance.compact.statusRunning')
                      : compaction?.available
                        ? t('dbMaintenance.compact.statusReady')
                        : t('dbMaintenance.compact.statusBlocked')}
                    tone={compactTone}
                  />
                </div>
                <p className="mt-2 max-w-2xl text-sm text-fog/60">{t('dbMaintenance.compact.description')}</p>
              </div>
              <button
                type="button"
                className={`rounded-full border px-4 py-2 text-sm font-medium transition ${
                  compaction?.available && !busy
                    ? 'border-amber-400/35 bg-amber-500/15 text-amber-100 hover:border-amber-300/55'
                    : 'border-white/10 bg-white/[0.04] text-fog/45'
                }`}
                onClick={() => void runAction('compact')}
                disabled={!compaction?.available || busy}
              >
                {action === 'compact' || compaction?.running ? t('dbMaintenance.compact.running') : t('dbMaintenance.compact.run')}
              </button>
            </div>

            <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <div className="rounded-xl border border-white/10 bg-black/10 p-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dbMaintenance.compact.physical')}</p>
                <p className="mt-1 text-sm font-semibold text-fog">{fmtSize(compaction?.physical_bytes)}</p>
              </div>
              <div className="rounded-xl border border-white/10 bg-black/10 p-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dbMaintenance.compact.live')}</p>
                <p className="mt-1 text-sm font-semibold text-fog">{fmtSize(compaction?.estimated_live_bytes)}</p>
              </div>
              <div className="rounded-xl border border-white/10 bg-black/10 p-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dbMaintenance.compact.reclaimable')}</p>
                <p className="mt-1 text-sm font-semibold text-fog">
                  {fmtSize(compaction?.reclaimable_bytes)} · {formatRatio(compaction?.reclaimable_ratio)}
                </p>
              </div>
              <div className="rounded-xl border border-white/10 bg-black/10 p-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dbMaintenance.compact.cap')}</p>
                <p className="mt-1 text-sm font-semibold text-fog">{fmtSize(compaction?.history_max_bytes)}</p>
              </div>
            </div>

            {compactReason && !compaction?.available && !compaction?.running ? (
              <p className="mt-3 text-sm text-amber-100">
                {t(compactReasonKey, { defaultValue: compactReason })}
              </p>
            ) : null}
            {compaction?.estimated_live_exceeds_cap ? (
              <p className="mt-3 text-sm text-amber-100">{t('dbMaintenance.compact.liveExceedsCap')}</p>
            ) : null}
            {compaction?.running && compaction.started_at ? (
              <p className="mt-3 text-sm text-amber-100">
                {t('dbMaintenance.compact.startedAt', { value: formatTimestamp(locale, compaction.started_at) })}
              </p>
            ) : null}
            {!compaction?.running && compaction?.completed_at ? (
              <p className={compaction.last_error ? 'mt-3 text-sm text-rose-200' : 'mt-3 text-sm text-emerald-100'}>
                {compaction.last_error
                  ? t('dbMaintenance.compact.failed', { value: compaction.last_error })
                  : t('dbMaintenance.compact.done', { value: fmtSize(compaction.last_reclaimed_bytes) })}
              </p>
            ) : null}
            <p className="mt-3 text-xs text-fog/45">
              {t('dbMaintenance.compact.lockHint', { seconds: compaction?.lock_timeout_seconds || 30 })}
            </p>
          </div>

          <div className="mt-5 overflow-hidden rounded-2xl border border-white/10">
            <div className="grid grid-cols-[1.6fr_0.8fr_0.8fr_0.9fr] gap-2 border-b border-white/10 bg-white/[0.03] px-4 py-2 text-xs uppercase tracking-wide text-fog/50">
              <span>{t('dbMaintenance.colTable')}</span>
              <span className="text-right">{t('dbMaintenance.colSize')}</span>
              <span className="text-right">{t('dbMaintenance.colDead')}</span>
              <span className="text-right">{t('dbMaintenance.colRetention')}</span>
            </div>
            {loading && tables.length === 0 ? (
              <p className="px-4 py-5 text-sm text-fog/60">{t('dbMaintenance.loading')}</p>
            ) : tables.length === 0 ? (
              <p className="px-4 py-5 text-sm text-fog/60">{t('dbMaintenance.empty')}</p>
            ) : (
              tables.map((tbl) => (
                <div
                  key={tbl.name}
                  className="grid grid-cols-[1.6fr_0.8fr_0.8fr_0.9fr] items-center gap-2 border-b border-white/5 px-4 py-2.5 text-sm last:border-b-0"
                >
                  <span className="truncate font-medium text-fog/85" title={tbl.name}>{tbl.name}</span>
                  <span className="text-right text-fog/75">{fmtSize(tbl.total_bytes)}</span>
                  <span className="text-right text-fog/60">{formatSats(locale, Number(tbl.dead_tuples || 0))}</span>
                  <span className="flex justify-end">
                    {tbl.has_retention ? (
                      <StatusBadge label={tbl.retention_label || ''} tone="ok" />
                    ) : (
                      <span className="text-fog/40">{t('dbMaintenance.noRetention')}</span>
                    )}
                  </span>
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
