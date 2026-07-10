import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getLogs, getTorUpgradeStatus, startTorUpgrade } from '../../api'
import { getLocale } from '../../i18n'
import StatusBadge from './StatusBadge'
import type { Tone } from './types'

type TorUpgradeStatus = {
  version?: string
  installed_package_version?: string
  candidate_version?: string
  candidate_package_version?: string
  repository_official?: boolean
  service_unit?: string
  service_status?: 'healthy' | 'degraded' | 'inactive' | 'unavailable'
  service_active?: boolean
  socks_ready?: boolean
  control_ready?: boolean
  update_available?: boolean
  can_update?: boolean
  running?: boolean
  checked_at?: string
  error?: string
}

const formatVersion = (value?: string) => (value ? `v${value.replace(/^v/i, '')}` : '-')

export default function TorUpgradeCard() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [status, setStatus] = useState<TorUpgradeStatus | null>(null)
  const [checking, setChecking] = useState(false)
  const [message, setMessage] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [locked, setLocked] = useState(false)
  const [complete, setComplete] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [logs, setLogs] = useState<string[]>([])
  const [logsStatus, setLogsStatus] = useState('')
  const [logSince, setLogSince] = useState('')
  const [started, setStarted] = useState(false)

  const loadStatus = async (force = false, silent = false) => {
    if (!silent) {
      setChecking(true)
      setMessage(t('torUpgrade.checking'))
    }
    try {
      const next = await getTorUpgradeStatus(force) as TorUpgradeStatus
      setStatus(next)
      if (!silent) setMessage('')
      return next
    } catch (err) {
      const detail = err instanceof Error ? err.message : t('torUpgrade.statusFailed')
      if (!silent) setMessage(detail)
      return null
    } finally {
      if (!silent) setChecking(false)
    }
  }

  useEffect(() => {
    let mounted = true
    const load = async () => {
      try {
        const next = await getTorUpgradeStatus() as TorUpgradeStatus
        if (mounted) setStatus(next)
      } catch {
        // The card keeps the last known status when a background refresh fails.
      }
    }
    void load()
    const timer = window.setInterval(() => void load(), 60000)
    return () => {
      mounted = false
      window.clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    if (!modalOpen) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previousOverflow
    }
  }, [modalOpen])

  useEffect(() => {
    if (!modalOpen) return
    let mounted = true

    const refresh = async () => {
      try {
        const res = await getLogs('tor-upgrade', 200, logSince || undefined)
        if (!mounted) return
        const nextLogs: string[] = Array.isArray(res?.lines) ? res.lines : []
        setLogs(nextLogs)
        setLogsStatus('')

        const completed = nextLogs.some((line) => line.includes('Tor update complete.'))
        const errorLine = [...nextLogs].reverse().find((line) =>
          line.includes('[ERROR]')
          || line.includes('Failed with result')
          || line.includes('Main process exited')
        )
        if (completed) {
          setComplete(true)
          setLocked(false)
          setError(null)
        } else if (errorLine) {
          setError(errorLine)
          setLocked(false)
        }
      } catch (err) {
        if (mounted) {
          setLogsStatus(err instanceof Error ? err.message : t('torUpgrade.logFetchFailed'))
        }
      }

      const next = await loadStatus(false, true)
      if (!mounted || !next) return
      if (next.running) {
        setLocked(true)
      } else if (started && next.repository_official && !next.update_available) {
        setComplete(true)
        setLocked(false)
      }
    }

    void refresh()
    const timer = window.setInterval(() => void refresh(), 2500)
    return () => {
      mounted = false
      window.clearInterval(timer)
    }
  }, [logSince, modalOpen, started, t])

  const tone: Tone = status?.service_status === 'healthy'
    ? 'ok'
    : status?.service_status === 'inactive'
      ? 'danger'
      : status?.service_status === 'degraded'
        ? 'warn'
        : 'muted'

  const statusLabel = t(`torUpgrade.statuses.${status?.service_status || 'unavailable'}`)
  const checkedAt = status?.checked_at
    ? new Intl.DateTimeFormat(locale, { dateStyle: 'short', timeStyle: 'short' }).format(new Date(status.checked_at))
    : t('common.na')
  const canOpen = Boolean(status?.can_update || status?.running) && !checking
  const canConfirm = Boolean(status?.can_update) && !status?.running && !complete && !started && !locked && !checking
  const candidate = formatVersion(status?.candidate_version)

  const statusMessage = useMemo(() => {
    if (!status) return t('torUpgrade.statusPending')
    if (status?.running) return t('torUpgrade.inProgress')
    if (!status?.repository_official) return t('torUpgrade.repositoryMissing')
    if (status?.update_available) return t('torUpgrade.updateAvailable')
    return t('torUpgrade.upToDate')
  }, [status, t])

  const openModal = () => {
    setModalOpen(true)
    setBusy(false)
    setLocked(Boolean(status?.running))
    setComplete(false)
    setError(null)
    setLogs([])
    setLogsStatus('')
    setStarted(Boolean(status?.running))
    setLogSince(status?.running ? '' : new Date(Date.now() - 15000).toISOString())
  }

  const closeModal = () => {
    if (busy || (locked && !error && !complete)) return
    setModalOpen(false)
  }

  const startUpgrade = async () => {
    if (!canConfirm || busy) return
    setBusy(true)
    setError(null)
    setComplete(false)
    setStarted(true)
    setLocked(true)
    setLogSince(new Date(Date.now() - 15000).toISOString())
    setMessage(t('torUpgrade.starting'))
    try {
      await startTorUpgrade()
      setMessage(t('torUpgrade.started'))
    } catch (err) {
      const detail = err instanceof Error ? err.message : t('torUpgrade.startFailed')
      setError(detail)
      setMessage(detail)
      setLocked(false)
    } finally {
      setBusy(false)
      void loadStatus(false, true)
    }
  }

  return (
    <>
      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <div className="flex flex-wrap items-center gap-3">
              <h3 className="text-lg font-semibold">{t('torUpgrade.title')}</h3>
              <StatusBadge label={statusLabel} tone={tone} />
            </div>
            <p className="mt-1 text-fog/60">{t('torUpgrade.subtitle')}</p>
          </div>
          <button className="btn-secondary" onClick={() => void loadStatus(true)} disabled={checking} type="button">
            {checking ? t('torUpgrade.checking') : t('common.refresh')}
          </button>
        </div>

        <div className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('torUpgrade.current')}</span>
            <span className="font-mono text-fog">{formatVersion(status?.version)}</span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('torUpgrade.latest')}</span>
            <span className="font-mono text-fog">{candidate}</span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('torUpgrade.repository')}</span>
            <span className={status?.repository_official ? 'text-emerald-200' : 'text-amber-200'}>
              {!status ? t('common.na') : status.repository_official ? t('torUpgrade.official') : t('torUpgrade.distribution')}
            </span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('torUpgrade.checkedAt')}</span>
            <span className="text-fog/90">{checkedAt}</span>
          </div>
        </div>

        {status?.error && <p className="text-sm text-rose-200">{t('torUpgrade.statusError', { error: status.error })}</p>}
        {!status?.error && <p className="text-sm text-fog/70">{statusMessage}</p>}
        {message && <p className="text-sm text-brass">{message}</p>}

        <div className="flex flex-wrap items-center gap-3">
          <button className="btn-primary" onClick={openModal} disabled={!canOpen} type="button">
            {status?.running ? t('torUpgrade.viewLogs') : t('torUpgrade.upgrade')}
          </button>
          <span className="text-xs text-fog/50">
            {t('torUpgrade.ports', {
              socks: status?.socks_ready ? t('common.ok') : t('common.fail'),
              control: status?.control_ready ? t('common.ok') : t('common.fail'),
            })}
          </span>
        </div>

        <p className="text-xs text-fog/50">{t('torUpgrade.warning')}</p>
      </div>

      {modalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={closeModal} aria-hidden="true" />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="tor-upgrade-title"
            className="relative z-10 w-full max-w-3xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <h4 id="tor-upgrade-title" className="text-lg font-semibold">{t('torUpgrade.confirmTitle')}</h4>
            <p className="mt-2 text-sm text-fog/70">
              {status?.repository_official
                ? t('torUpgrade.confirmBody', { current: formatVersion(status?.version), latest: candidate })
                : t('torUpgrade.confirmBodyWithRepository', { current: formatVersion(status?.version) })}
            </p>
            <p className="mt-3 text-xs text-rose-200">{t('torUpgrade.confirmWarning')}</p>
            {message && <p className="mt-3 text-sm text-brass">{message}</p>}
            {error && <p className="mt-2 text-sm text-rose-200">{error}</p>}
            {complete && !error && <p className="mt-2 text-sm text-emerald-200">{t('torUpgrade.completed')}</p>}

            <div className="mt-4">
              <div className="flex items-center justify-between">
                <span className="text-sm text-fog/70">{t('torUpgrade.logsTitle')}</span>
                <span className="text-xs text-fog/50">{status?.running ? t('torUpgrade.inProgress') : t('torUpgrade.logsHint')}</span>
              </div>
              {logsStatus && <p className="mt-2 text-xs text-brass">{logsStatus}</p>}
              <div className="mt-2 max-h-[320px] overflow-y-auto rounded-2xl border border-white/10 bg-ink/70 p-3 text-xs font-mono whitespace-pre-wrap">
                {logs.length ? logs.join('\n') : t('torUpgrade.noLogs')}
              </div>
            </div>

            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${(busy || (locked && !error && !complete)) ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={closeModal}
                type="button"
                disabled={busy || (locked && !error && !complete)}
              >
                {complete || error ? t('common.close') : t('common.cancel')}
              </button>
              {canConfirm && (
                <button
                  className={`btn-secondary border-amber-400/30 text-amber-200 ${busy ? 'opacity-60 pointer-events-none' : ''}`}
                  onClick={startUpgrade}
                  type="button"
                  disabled={busy}
                >
                  {busy ? t('torUpgrade.upgrading') : t('torUpgrade.confirmUpgrade')}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </>
  )
}
