import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getReportsReconciliation,
  startReportsReconciliation,
  type ReportsReconciliationStatus
} from '../api'

const reportsReconciledEvent = 'reports:reconciled'

export default function ReportsReconciliationNotice() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<ReportsReconciliationStatus | null>(null)
  const [error, setError] = useState('')
  const [starting, setStarting] = useState(false)
  const [pollKey, setPollKey] = useState(0)
  const wasRunning = useRef(false)
  const hadMissing = useRef(false)

  useEffect(() => {
    let active = true
    let timer = 0
    const load = async () => {
      try {
        const next = await getReportsReconciliation()
        if (!active) return
        setStatus(next)
        setError('')
        if ((wasRunning.current || hadMissing.current) && !next.running && next.missing_count === 0) {
          window.dispatchEvent(new Event(reportsReconciledEvent))
        }
        wasRunning.current = next.running
        hadMissing.current = next.missing_count > 0
        timer = window.setTimeout(load, next.running ? 2000 : 30000)
      } catch {
        if (!active) return
        timer = window.setTimeout(load, 30000)
      }
    }
    void load()
    return () => {
      active = false
      window.clearTimeout(timer)
    }
  }, [pollKey])

  const reconcile = async () => {
    setStarting(true)
    setError('')
    try {
      const next = await startReportsReconciliation()
      setStatus(next)
      wasRunning.current = next.running
      hadMissing.current = next.missing_count > 0
      setPollKey((value) => value + 1)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('reports.reconciliation.failed'))
    } finally {
      setStarting(false)
    }
  }

  if (!status || (status.missing_count === 0 && !status.running)) {
    return null
  }

  return (
    <div className="section-card mb-6 border border-amber-400/40 bg-amber-400/5">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="space-y-1">
          <h3 className="font-semibold text-amber-200">{t('reports.reconciliation.title')}</h3>
          <p className="text-sm text-fog/70">
            {status.running
              ? t('reports.reconciliation.running', { completed: status.completed, total: status.total })
              : t('reports.reconciliation.missing', { count: status.missing_count })}
          </p>
          {!status.running && status.missing_dates.length > 0 && (
            <p className="text-xs text-fog/60">{status.missing_dates.join(', ')}</p>
          )}
          <p className="text-xs text-fog/50">{t('reports.reconciliation.safety')}</p>
          {status.last_error && <p className="text-sm text-rose-300">{status.last_error}</p>}
          {error && <p className="text-sm text-rose-300">{error}</p>}
        </div>
        <button
          type="button"
          className="btn-primary"
          disabled={status.running || starting}
          onClick={reconcile}
        >
          {status.running || starting
            ? t('reports.reconciliation.reconciling')
            : t('reports.reconciliation.action')}
        </button>
      </div>
      {status.running && status.total > 0 && (
        <div className="mt-4 h-2 overflow-hidden rounded-full bg-white/10">
          <div
            className="h-full bg-amber-300 transition-all"
            style={{ width: `${Math.max(4, (status.completed / status.total) * 100)}%` }}
          />
        </div>
      )}
    </div>
  )
}
