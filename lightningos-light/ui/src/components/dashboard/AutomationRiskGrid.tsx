import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import { formatPercent, formatSats, formatTimeAgo } from './formatters'
import HorizontalBarGauge from './HorizontalBarGauge'
import StatusBadge from './StatusBadge'
import type {
  AmbossHealthStatus,
  AutofeeStatus,
  ChanHealStatus,
  CloseRecoveryStatus,
  FailedPaymentsCleanerStatus,
  HtlcManagerStatus,
  NodeRetirementStatus,
  RebalanceOverview,
  SuccessionConfig,
  TorPeerCheckerStatus,
} from './types'

type AutomationRiskGridProps = {
  rebalance: RebalanceOverview | null
  autofee: AutofeeStatus | null
  amboss: AmbossHealthStatus | null
  chanHeal: ChanHealStatus | null
  closeManager: CloseRecoveryStatus | null
  htlcManager: HtlcManagerStatus | null
  torPeerChecker: TorPeerCheckerStatus | null
  failedPaymentsCleaner: FailedPaymentsCleanerStatus | null
  nodeRetirement: NodeRetirementStatus | null
  successionConfig: SuccessionConfig | null
}

type AutomationRowProps = {
  label: string
  enabled?: boolean | null
  status?: string | null
  lastOkAt?: string
  lastAttemptAt?: string
  lastError?: string
  extra?: string
}

const normalizeStatus = (value?: string | null) => String(value || '').trim().toLowerCase()

function buildAutomationBadge(t: ReturnType<typeof useTranslation>['t'], enabled?: boolean | null, status?: string | null, hasError?: boolean) {
  if (enabled === false) {
    return { label: t('common.disabled'), tone: 'muted' as const }
  }
  const normalized = normalizeStatus(status)
  if (normalized === 'ok') {
    return { label: t('common.ok'), tone: 'ok' as const }
  }
  if (normalized === 'warn' || hasError) {
    return { label: t('common.fail'), tone: 'warn' as const }
  }
  if (normalized === 'checking') {
    return { label: t('common.check'), tone: 'info' as const }
  }
  if (enabled) {
    return { label: t('common.enabled'), tone: 'info' as const }
  }
  return { label: t('common.na'), tone: 'muted' as const }
}

function AutomationRow({ label, enabled, status, lastOkAt, lastAttemptAt, lastError, extra }: AutomationRowProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const badge = buildAutomationBadge(t, enabled, status, Boolean(lastError))
  const primaryMeta = lastError
    ? `${t('dashboard.lastErrorLabel')}: ${lastError}`
    : lastOkAt
      ? `${t('dashboard.lastRunLabel')}: ${formatTimeAgo(locale, lastOkAt)}`
      : lastAttemptAt
        ? `${t('dashboard.lastAttemptLabel')}: ${formatTimeAgo(locale, lastAttemptAt)}`
        : extra || t('common.na')
  const secondaryMeta = extra && (lastError || lastOkAt || lastAttemptAt) ? extra : ''

  return (
    <div className="rounded-2xl border border-white/10 bg-white/5 px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-medium text-fog/85">{label}</p>
          <p className="mt-1 text-xs text-fog/50">{primaryMeta}</p>
        </div>
        <StatusBadge label={badge.label} tone={badge.tone} />
      </div>
      {secondaryMeta ? <p className="mt-2 text-xs text-fog/50">{secondaryMeta}</p> : null}
    </div>
  )
}

export default function AutomationRiskGrid({
  rebalance,
  autofee,
  amboss,
  chanHeal,
  closeManager,
  htlcManager,
  torPeerChecker,
  failedPaymentsCleaner,
  nodeRetirement,
  successionConfig,
}: AutomationRiskGridProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const ratioFormatter = new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 })

  const dailyBudget = rebalance?.daily_budget_sat ?? 0
  const dailySpent = rebalance?.daily_spent_sat ?? 0
  const budgetUsage = dailyBudget > 0 ? (dailySpent / dailyBudget) * 100 : 0
  const actionRequiredCount = closeManager?.action_required_count ?? 0
  const roi7d = typeof rebalance?.roi_7d === 'number' ? ratioFormatter.format(rebalance.roi_7d) : '-'
  const effectiveness7dRaw = rebalance?.effectiveness_execution_7d ?? rebalance?.effectiveness_7d
  const effectiveness7d = typeof effectiveness7dRaw === 'number' ? `${formatPercent(locale, effectiveness7dRaw * 100)}%` : '-'

  const formatAutomationInterval = (value?: { interval_sec?: number; interval_minutes?: number; interval_hours?: number } | null) => {
    if (typeof value?.interval_hours === 'number' && value.interval_hours > 0) {
      return t('dashboard.automationEveryHours', { count: value.interval_hours })
    }
    if (typeof value?.interval_minutes === 'number' && value.interval_minutes > 0) {
      return t('dashboard.automationEveryMinutes', { count: value.interval_minutes })
    }
    if (typeof value?.interval_sec === 'number' && value.interval_sec > 0) {
      const seconds = value.interval_sec
      if (seconds % 3600 === 0) return t('dashboard.automationEveryHours', { count: seconds / 3600 })
      if (seconds % 60 === 0) return t('dashboard.automationEveryMinutes', { count: seconds / 60 })
      return t('dashboard.automationEverySeconds', { count: seconds })
    }
    return ''
  }

  const buildAutomationExtra = (value?: {
    enabled?: boolean
    status?: string
    interval_sec?: number
    interval_minutes?: number
    interval_hours?: number
  } | null, fallback?: string) => {
    const intervalHint = formatAutomationInterval(value)
    const normalized = normalizeStatus(value?.status)
    if (normalized === 'checking' && value?.enabled) {
      return intervalHint
        ? `${t('dashboard.awaitingFirstRunHint')} ${intervalHint}`
        : t('dashboard.awaitingFirstRunHint')
    }
    return fallback || intervalHint || undefined
  }

  return (
    <article className="section-card">
      <div className="flex flex-col gap-2 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <p className="text-xs uppercase tracking-[0.24em] text-fog/45">{t('dashboard.automationKicker')}</p>
          <h3 className="mt-2 text-xl font-semibold">{t('dashboard.automationTitle')}</h3>
          <p className="mt-2 text-sm text-fog/60">{t('dashboard.automationSubtitle')}</p>
        </div>
      </div>

      <div className="mt-6 grid gap-4 xl:grid-cols-3">
        <div className="rounded-3xl border border-white/10 bg-white/5 p-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h4 className="text-base font-semibold">{t('dashboard.rebalanceBudgetTitle')}</h4>
              <p className="mt-1 text-sm text-fog/60">{t('dashboard.rebalanceBudgetHint')}</p>
            </div>
            <StatusBadge label={rebalance?.auto_enabled ? t('common.enabled') : t('common.disabled')} tone={rebalance?.auto_enabled ? 'ok' : 'warn'} />
          </div>

          <div className="mt-4 space-y-4">
            <HorizontalBarGauge
              label={t('dashboard.budgetUsageLabel')}
              value={dailySpent}
              max={Math.max(1, dailyBudget)}
              valueLabel={`${formatSats(locale, dailySpent)} / ${formatSats(locale, dailyBudget)} sats`}
              tone={budgetUsage >= 90 ? 'danger' : budgetUsage >= 70 ? 'warn' : 'ok'}
            />

            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-2xl border border-white/10 bg-ink/30 px-3 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.roi7dLabel')}</p>
                <p className="mt-2 text-lg font-semibold">{roi7d}</p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/30 px-3 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.effectiveness7dLabel')}</p>
                <p className="mt-2 text-lg font-semibold">{effectiveness7d}</p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/30 px-3 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.success24hLabel')}</p>
                <p className="mt-2 text-lg font-semibold">{formatSats(locale, rebalance?.success_attempts_24h)}</p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/30 px-3 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.liveCostLabel')}</p>
                <p className="mt-2 text-lg font-semibold">{formatSats(locale, rebalance?.live_cost_sat)} sats</p>
              </div>
            </div>

            <div className="rounded-2xl border border-white/10 bg-ink/30 px-4 py-3 text-sm">
              <div className="flex items-center justify-between gap-3">
                <span className="text-fog/60">{t('dashboard.remainingBudgetLabel')}</span>
                <span>{formatSats(locale, rebalance?.remaining_total_sat ?? 0)} sats</span>
              </div>
              <div className="mt-2 flex items-center justify-between gap-3">
                <span className="text-fog/60">{t('dashboard.autofeeStatusLabel')}</span>
                <StatusBadge label={autofee?.running ? t('dashboard.runningLabel') : t('dashboard.idleLabel')} tone={autofee?.running ? 'ok' : 'muted'} />
              </div>
              <div className="mt-2 text-xs text-fog/50">
                {autofee?.last_error
                  ? `${t('dashboard.lastErrorLabel')}: ${autofee.last_error}`
                  : `${t('dashboard.nextRunLabel')}: ${formatTimeAgo(locale, autofee?.next_run_at)}`}
              </div>
            </div>
          </div>
        </div>

        <div className="rounded-3xl border border-white/10 bg-white/5 p-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h4 className="text-base font-semibold">{t('dashboard.automationsTitle')}</h4>
              <p className="mt-1 text-sm text-fog/60">{t('dashboard.automationsHint')}</p>
            </div>
          </div>

          <div className="mt-4 grid gap-3">
            <AutomationRow
              label={t('lightningOps.ambossHealthTitle')}
              enabled={amboss?.enabled}
              status={amboss?.status}
              lastOkAt={amboss?.last_ok_at}
              lastAttemptAt={amboss?.last_attempt_at}
              lastError={amboss?.last_error}
              extra={buildAutomationExtra(amboss)}
            />
            <AutomationRow
              label={t('lightningOps.chanHealTitle')}
              enabled={chanHeal?.enabled}
              status={chanHeal?.status}
              lastOkAt={chanHeal?.last_ok_at}
              lastAttemptAt={chanHeal?.last_attempt_at}
              lastError={chanHeal?.last_error}
              extra={buildAutomationExtra(
                chanHeal,
                typeof chanHeal?.last_updated === 'number'
                  ? t('lightningOps.chanHealLastUpdated', { count: chanHeal.last_updated })
                  : undefined,
              )}
            />
            <AutomationRow
              label={t('lightningOps.htlcManagerTitle')}
              enabled={htlcManager?.enabled}
              status={htlcManager?.status}
              lastOkAt={htlcManager?.last_ok_at}
              lastAttemptAt={htlcManager?.last_attempt_at}
              lastError={htlcManager?.last_error}
              extra={buildAutomationExtra(
                htlcManager,
                typeof htlcManager?.last_changed_count === 'number' ? t('dashboard.channelsChangedHint', { count: htlcManager.last_changed_count }) : undefined,
              )}
            />
            <AutomationRow
              label={t('lightningOps.torPeerTitle')}
              enabled={torPeerChecker?.enabled}
              status={torPeerChecker?.status}
              lastOkAt={torPeerChecker?.last_ok_at}
              lastAttemptAt={torPeerChecker?.last_attempt_at}
              lastError={torPeerChecker?.last_error}
              extra={buildAutomationExtra(
                torPeerChecker,
                typeof torPeerChecker?.last_switched_count === 'number' ? t('dashboard.peersSwitchedHint', { count: torPeerChecker.last_switched_count }) : undefined,
              )}
            />
            <AutomationRow
              label={t('lightningOps.failedPaymentsCleanerTitle')}
              enabled={failedPaymentsCleaner?.enabled}
              status={failedPaymentsCleaner?.status}
              lastOkAt={failedPaymentsCleaner?.last_ok_at}
              lastAttemptAt={failedPaymentsCleaner?.last_attempt_at}
              lastError={failedPaymentsCleaner?.last_error}
              extra={buildAutomationExtra(
                failedPaymentsCleaner,
                typeof failedPaymentsCleaner?.last_deleted_count === 'number' ? t('dashboard.deletedPaymentsHint', { count: failedPaymentsCleaner.last_deleted_count }) : undefined,
              )}
            />
          </div>
        </div>

        <div className="rounded-3xl border border-white/10 bg-white/5 p-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h4 className="text-base font-semibold">{t('dashboard.riskRecoveryTitle')}</h4>
              <p className="mt-1 text-sm text-fog/60">{t('dashboard.riskRecoveryHint')}</p>
            </div>
            <StatusBadge
              label={actionRequiredCount > 0 ? t('dashboard.actionRequiredBadge', { count: actionRequiredCount }) : t('common.ok')}
              tone={actionRequiredCount > 0 ? 'danger' : 'ok'}
            />
          </div>

          <div className="mt-4 grid gap-3">
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-2xl border border-white/10 bg-ink/30 px-3 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.activeClosingsLabel')}</p>
                <p className="mt-2 text-lg font-semibold">{formatSats(locale, closeManager?.active_count)}</p>
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/30 px-3 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/45">{t('dashboard.blockedHtlcLabel')}</p>
                <p className="mt-2 text-lg font-semibold">{formatSats(locale, closeManager?.htlc_blocked_count)}</p>
              </div>
            </div>

            <div className="rounded-2xl border border-white/10 bg-ink/30 px-4 py-3">
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm text-fog/70">{t('dashboard.nodeRetirementLabel')}</span>
                <StatusBadge
                  label={nodeRetirement?.active ? nodeRetirement.active_state || t('dashboard.activeLabel') : t('common.inactive')}
                  tone={nodeRetirement?.active ? 'warn' : 'muted'}
                />
              </div>
              <p className="mt-2 text-xs text-fog/50">
                {successionConfig?.enabled
                  ? `${t('dashboard.successionDeadlineLabel')}: ${formatTimeAgo(locale, successionConfig.deadline_at)}`
                  : t('dashboard.successionDisabledHint')}
              </p>
            </div>

            <div className="rounded-2xl border border-white/10 bg-ink/30 px-4 py-3">
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm text-fog/70">{t('dashboard.successionLabel')}</span>
                <StatusBadge
                  label={successionConfig?.status || (successionConfig?.enabled ? t('dashboard.armedLabel') : t('common.disabled'))}
                  tone={successionConfig?.enabled ? 'warn' : 'muted'}
                />
              </div>
              <div className="mt-2 grid gap-2 text-xs text-fog/50 sm:grid-cols-2">
                <span>{t('dashboard.lastAliveLabel')}: {formatTimeAgo(locale, successionConfig?.last_alive_at)}</span>
                <span>{t('dashboard.nextCheckLabel')}: {formatTimeAgo(locale, successionConfig?.next_check_at)}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </article>
  )
}
