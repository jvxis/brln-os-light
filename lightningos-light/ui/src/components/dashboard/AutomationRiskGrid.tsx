import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import { formatPercent, formatSats, formatTimeAgo, toneFromStatusText } from './formatters'
import HorizontalBarGauge from './HorizontalBarGauge'
import StatusBadge from './StatusBadge'
import type {
  AutofeeStatus,
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
  closeManager: CloseRecoveryStatus | null
  htlcManager: HtlcManagerStatus | null
  torPeerChecker: TorPeerCheckerStatus | null
  failedPaymentsCleaner: FailedPaymentsCleanerStatus | null
  nodeRetirement: NodeRetirementStatus | null
  successionConfig: SuccessionConfig | null
}

type AutomationRowProps = {
  label: string
  status?: string | boolean | null
  lastOkAt?: string
  lastError?: string
  extra?: string
}

function AutomationRow({ label, status, lastOkAt, lastError, extra }: AutomationRowProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const statusLabel = typeof status === 'boolean'
    ? (status ? t('common.enabled') : t('common.disabled'))
    : (status || t('common.na'))
  const tone = typeof status === 'boolean'
    ? (status ? 'ok' : 'warn')
    : toneFromStatusText(status)

  return (
    <div className="rounded-2xl border border-white/10 bg-white/5 px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-medium text-fog/85">{label}</p>
          <p className="mt-1 text-xs text-fog/50">
            {lastError
              ? `${t('dashboard.lastErrorLabel')}: ${lastError}`
              : lastOkAt
                ? `${t('dashboard.lastRunLabel')}: ${formatTimeAgo(locale, lastOkAt)}`
                : extra || t('common.na')}
          </p>
        </div>
        <StatusBadge label={statusLabel} tone={tone} />
      </div>
      {extra && !lastError && !lastOkAt ? <p className="mt-2 text-xs text-fog/50">{extra}</p> : null}
    </div>
  )
}

export default function AutomationRiskGrid({
  rebalance,
  autofee,
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
              label={t('lightningOps.htlcManagerTitle')}
              status={htlcManager?.status || htlcManager?.enabled}
              lastOkAt={htlcManager?.last_ok_at || htlcManager?.last_attempt_at}
              lastError={htlcManager?.last_error}
              extra={typeof htlcManager?.last_changed_count === 'number' ? t('dashboard.channelsChangedHint', { count: htlcManager.last_changed_count }) : undefined}
            />
            <AutomationRow
              label={t('lightningOps.torPeerTitle')}
              status={torPeerChecker?.status || torPeerChecker?.enabled}
              lastOkAt={torPeerChecker?.last_ok_at || torPeerChecker?.last_attempt_at}
              lastError={torPeerChecker?.last_error}
              extra={typeof torPeerChecker?.last_switched_count === 'number' ? t('dashboard.peersSwitchedHint', { count: torPeerChecker.last_switched_count }) : undefined}
            />
            <AutomationRow
              label={t('lightningOps.failedPaymentsCleanerTitle')}
              status={failedPaymentsCleaner?.status || failedPaymentsCleaner?.enabled}
              lastOkAt={failedPaymentsCleaner?.last_ok_at || failedPaymentsCleaner?.last_attempt_at}
              lastError={failedPaymentsCleaner?.last_error}
              extra={typeof failedPaymentsCleaner?.last_deleted_count === 'number' ? t('dashboard.deletedPaymentsHint', { count: failedPaymentsCleaner.last_deleted_count }) : undefined}
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
