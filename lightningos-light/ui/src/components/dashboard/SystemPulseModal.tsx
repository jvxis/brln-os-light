import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import StatusBadge from './StatusBadge'
import { formatTimestamp, toneFromHealthStatus } from './formatters'
import type { SystemCheckGroup, SystemCheckItem, SystemCheckResponse, SystemCheckTone, Tone } from './types'

type SystemPulseModalProps = {
  open: boolean
  check: SystemCheckResponse | null
  loading: boolean
  error: string | null
  onClose: () => void
  onRefresh: () => void
}

const toneLabelKey = (tone?: SystemCheckTone) => {
  switch (tone) {
    case 'ok':
      return 'systemCheck.tones.ok'
    case 'warn':
      return 'systemCheck.tones.warn'
    case 'danger':
      return 'systemCheck.tones.danger'
    case 'info':
      return 'systemCheck.tones.info'
    default:
      return 'systemCheck.tones.muted'
  }
}

const countItemsByTone = (group: SystemCheckGroup, tone: SystemCheckTone) =>
  Array.isArray(group.items) ? group.items.filter((item) => item.status === tone).length : 0

const groupLevelLogGroups = new Set(['lnd', 'bitcoin', 'postgres', 'tor'])

type LogPanel = {
  key: string
  source?: string
  lines: string[]
  itemIds: string[]
  groupLevel: boolean
}

const itemLogKey = (item: SystemCheckItem) => {
  if (!Array.isArray(item.log_tail) || item.log_tail.length === 0) return ''
  return item.log_source ? `source:${item.log_source}` : `tail:${item.log_tail.join('\n')}`
}

const groupLogPanels = (group: SystemCheckGroup): LogPanel[] => {
  const panels = new Map<string, Omit<LogPanel, 'groupLevel'>>()
  const items = Array.isArray(group.items) ? group.items : []
  for (const item of items) {
    const key = itemLogKey(item)
    if (!key || !Array.isArray(item.log_tail) || item.log_tail.length === 0) continue
    const existing = panels.get(key)
    if (existing) {
      existing.itemIds.push(item.id)
      continue
    }
    panels.set(key, {
      key,
      source: item.log_source,
      lines: item.log_tail,
      itemIds: [item.id],
    })
  }

  return Array.from(panels.values()).map((panel) => ({
    ...panel,
    groupLevel: groupLevelLogGroups.has(group.id) || panel.itemIds.length > 1,
  }))
}

function LogDetails({ title, lines, className = 'mt-3' }: { title: string; lines: string[]; className?: string }) {
  return (
    <details className={`${className} rounded-2xl border border-white/10 bg-ink/50 p-3`} open>
      <summary className="cursor-pointer text-xs text-fog/60">
        {title}
      </summary>
      <div className="mt-2 max-h-40 overflow-y-auto whitespace-pre-wrap break-words text-[11px] font-mono leading-relaxed text-fog/65">
        {lines.join('\n')}
      </div>
    </details>
  )
}

export default function SystemPulseModal({
  open,
  check,
  loading,
  error,
  onClose,
  onRefresh,
}: SystemPulseModalProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)

  useEffect(() => {
    if (!open) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [open, onClose])

  if (!open) return null

  const groups = Array.isArray(check?.groups) ? check.groups : []
  const overallTone = toneFromHealthStatus(check?.status)
  const checkedAt = check?.checked_at ? formatTimestamp(locale, check.checked_at) : t('common.na')

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
        aria-labelledby="system-pulse-title"
        className="relative z-10 flex max-h-[86vh] w-full max-w-5xl flex-col overflow-hidden rounded-3xl border border-white/10 bg-slate/95 shadow-panel"
      >
        <div className="flex flex-col gap-4 border-b border-white/10 p-6 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <p className="text-xs uppercase tracking-[0.3em] text-fog/50">{t('systemCheck.kicker')}</p>
            <div className="mt-3 flex flex-wrap items-center gap-3">
              <h2 id="system-pulse-title" className="text-2xl font-semibold">{t('systemCheck.title')}</h2>
              <StatusBadge label={check?.status || t('dashboard.loadingStatus')} tone={overallTone} size="md" />
            </div>
            <p className="mt-2 text-sm text-fog/60">{t('systemCheck.checkedAt', { value: checkedAt })}</p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              className={`btn-secondary text-sm ${loading ? 'opacity-60 pointer-events-none' : ''}`}
              onClick={onRefresh}
              disabled={loading}
            >
              {loading ? t('systemCheck.refreshing') : t('common.refresh')}
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
          {error && (
            <div className="mb-4 rounded-2xl border border-rose-400/30 bg-rose-500/15 px-4 py-3 text-sm text-rose-200">
              {error}
            </div>
          )}

          {loading && groups.length === 0 ? (
            <div className="rounded-2xl border border-white/10 bg-white/5 px-4 py-5 text-sm text-fog/65">
              {t('systemCheck.loading')}
            </div>
          ) : null}

          {groups.length > 0 ? (
            <div className="grid gap-4 lg:grid-cols-2">
              {groups.map((group) => {
                const logPanels = groupLogPanels(group)
                const groupLevelLogKeys = new Set(logPanels.filter((panel) => panel.groupLevel).map((panel) => panel.key))
                return (
                  <article key={group.id} className="rounded-2xl border border-white/10 bg-white/5 p-4">
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <h3 className="text-lg font-semibold">{group.label}</h3>
                        <p className="mt-1 text-sm text-fog/55">
                          {group.status === 'danger'
                            ? t('systemCheck.summaryFailing', { count: countItemsByTone(group, 'danger') })
                            : group.status === 'warn'
                              ? t('systemCheck.summaryWarning', { count: countItemsByTone(group, 'warn') })
                              : group.status === 'ok'
                                ? t('systemCheck.summaryOk')
                                : t('systemCheck.summaryMuted')}
                        </p>
                      </div>
                      <StatusBadge
                        label={t(toneLabelKey(group.status))}
                        tone={group.status as Tone}
                      />
                    </div>

                    <div className="mt-4 divide-y divide-white/10">
                      {group.items.map((item) => {
                        const logKey = itemLogKey(item)
                        const showInlineLog = logKey !== '' && !groupLevelLogKeys.has(logKey)
                        return (
                          <div key={item.id} className="py-3 first:pt-0 last:pb-0">
                            <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                              <div className="min-w-0">
                                <p className="text-sm font-medium text-fog/85">{item.label}</p>
                                {item.detail ? <p className="mt-1 break-words text-xs text-fog/50">{item.detail}</p> : null}
                              </div>
                              {item.status !== 'muted' ? (
                                <StatusBadge
                                  label={t(toneLabelKey(item.status))}
                                  tone={item.status as Tone}
                                />
                              ) : null}
                            </div>

                            {item.diagnostic ? (
                              <p className="mt-3 rounded-2xl border border-amber-400/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-100">
                                {item.diagnostic}
                              </p>
                            ) : null}

                            {showInlineLog && Array.isArray(item.log_tail) && item.log_tail.length > 0 ? (
                              <LogDetails
                                title={t('systemCheck.recentLogs', { source: item.log_source || t('common.unknown') })}
                                lines={item.log_tail}
                              />
                            ) : null}
                          </div>
                        )
                      })}
                    </div>

                    {logPanels.filter((panel) => panel.groupLevel).map((panel) => (
                      <LogDetails
                        key={panel.key}
                        title={t('systemCheck.recentLogs', { source: panel.source || t('common.unknown') })}
                        lines={panel.lines}
                        className="mt-4"
                      />
                    ))}
                  </article>
                )
              })}
            </div>
          ) : !loading ? (
            <div className="rounded-2xl border border-white/10 bg-white/5 px-4 py-5 text-sm text-fog/65">
              {t('systemCheck.empty')}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}
