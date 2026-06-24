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
  if (normalized === 'unreachable') {
    return { label: t('lightningOps.chanHealUnreachableBadge'), tone: 'warn' as const }
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
  const preferExtra = normalizeStatus(status) === 'unreachable' && extra
  const primaryMeta = lastError
    ? `${t('dashboard.lastErrorLabel')}: ${lastError}`
    : preferExtra
      ? extra
    : lastOkAt
      ? `${t('dashboard.lastRunLabel')}: ${formatTimeAgo(locale, lastOkAt)}`
      : lastAttemptAt
        ? `${t('dashboard.lastAttemptLabel')}: ${formatTimeAgo(locale, lastAttemptAt)}`
        : extra || t('common.na')
  const secondaryMeta = extra && primaryMeta !== extra && (lastError || lastOkAt || lastAttemptAt) ? extra : ''

  return (
    <div className="min-w-0 overflow-hidden rounded-2xl border border-white/10 bg-white/5 px-4 py-3">
      <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2">
        <div className="min-w-0 flex-1 basis-40">
          <p className="text-sm font-medium leading-snug text-fog/85 [overflow-wrap:anywhere]">{label}</p>
          <p className="mt-1 text-xs leading-snug text-fog/50 [overflow-wrap:anywhere]">{primaryMeta}</p>
        </div>
        <div className="max-w-full shrink-0">
          <StatusBadge label={badge.label} tone={badge.tone} />
        </div>
      </div>
      {secondaryMeta ? <p className="mt-2 text-xs leading-snug text-fog/50 [overflow-wrap:anywhere]">{secondaryMeta}</p> : null}
    </div>
  )
}

const automationGridClass = 'mt-6 grid gap-4 [grid-template-columns:repeat(auto-fit,minmax(min(100%,16rem),1fr))]'
const panelClass = 'min-w-0 rounded-3xl border border-white/10 bg-white/5 p-4'
const metricGridClass = 'grid gap-3 [grid-template-columns:repeat(auto-fit,minmax(min(100%,7.5rem),1fr))]'
const metricTileClass = 'min-w-0 overflow-hidden rounded-2xl border border-white/10 bg-ink/30 px-3 py-3'
const metricLabelClass = 'text-xs uppercase leading-snug tracking-[0.04em] text-fog/45 [overflow-wrap:anywhere]'
const splitRowClass = 'flex flex-wrap items-center justify-between gap-x-3 gap-y-1'

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

  const chanHealPeerLabel = (detail?: { alias?: string; pubkey_short?: string; pubkey?: string } | null) => {
    if (!detail) return ''
    return detail.alias || detail.pubkey_short || (detail.pubkey ? `${detail.pubkey.slice(0, 12)}...` : '')
  }

  const buildChanHealExtra = () => {
    const failed = chanHeal?.last_reconnect_failed ?? 0
    const connected = chanHeal?.last_reconnected ?? 0
    const details = chanHeal?.last_reconnect_details || []
    if (failed > 0) {
      const firstIssue = details.find((detail) => {
        const status = normalizeStatus(detail.status)
        return status !== 'connected' && status !== 'already_connected'
      })
      return t('lightningOps.chanHealReconnectDashboardUnreachable', {
        count: failed,
        peer: chanHealPeerLabel(firstIssue) || t('lightningOps.chanHealReconnectUnknownPeer'),
      })
    }
    const inactiveAfterConnect = details.find((detail) => normalizeStatus(detail.status) === 'connected_channel_inactive')
    if (inactiveAfterConnect) {
      return t('lightningOps.chanHealReconnectDashboardChannelInactive', {
        peer: chanHealPeerLabel(inactiveAfterConnect) || t('lightningOps.chanHealReconnectUnknownPeer'),
      })
    }
    if (connected > 0) {
      return t('lightningOps.chanHealReconnectDashboardConnected', { count: connected })
    }
    if (typeof chanHeal?.last_updated === 'number') {
      return t('lightningOps.chanHealLastUpdated', { count: chanHeal.last_updated })
    }
    return buildAutomationExtra(chanHeal)
  }

  return (
    <article className="section-card min-w-0">
      <div className="flex flex-col gap-2 lg:flex-row lg:items-end lg:justify-between">
        <div className="min-w-0">
          <p className="text-xs uppercase tracking-[0.24em] text-fog/45 [overflow-wrap:anywhere]">{t('dashboard.automationKicker')}</p>
          <h3 className="mt-2 text-xl font-semibold [overflow-wrap:anywhere]">{t('dashboard.automationTitle')}</h3>
          <p className="mt-2 text-sm text-fog/60 [overflow-wrap:anywhere]">{t('dashboard.automationSubtitle')}</p>
        </div>
      </div>

      <div className={automationGridClass}>
        <div className={panelClass}>
          <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2">
            <div className="min-w-0 flex-1 basis-44">
              <h4 className="text-base font-semibold leading-snug [overflow-wrap:anywhere]">{t('dashboard.rebalanceBudgetTitle')}</h4>
              <p className="mt-1 text-sm leading-snug text-fog/60 [overflow-wrap:anywhere]">{t('dashboard.rebalanceBudgetHint')}</p>
            </div>
            <div className="max-w-full shrink-0">
              <StatusBadge label={rebalance?.auto_enabled ? t('common.enabled') : t('common.disabled')} tone={rebalance?.auto_enabled ? 'ok' : 'warn'} />
            </div>
          </div>

          <div className="mt-4 space-y-4">
            <HorizontalBarGauge
              label={t('dashboard.budgetUsageLabel')}
              value={dailySpent}
              max={Math.max(1, dailyBudget)}
              valueLabel={`${formatSats(locale, dailySpent)} / ${formatSats(locale, dailyBudget)} sats`}
              tone={budgetUsage >= 90 ? 'danger' : budgetUsage >= 70 ? 'warn' : 'ok'}
            />

            <div className={metricGridClass}>
              <div className={metricTileClass}>
                <p className={metricLabelClass}>{t('dashboard.roi7dLabel')}</p>
                <p className="mt-2 text-lg font-semibold [overflow-wrap:anywhere]">{roi7d}</p>
              </div>
              <div className={metricTileClass}>
                <p className={metricLabelClass}>{t('dashboard.effectiveness7dLabel')}</p>
                <p className="mt-2 text-lg font-semibold [overflow-wrap:anywhere]">{effectiveness7d}</p>
              </div>
              <div className={metricTileClass}>
                <p className={metricLabelClass}>{t('dashboard.success24hLabel')}</p>
                <p className="mt-2 text-lg font-semibold [overflow-wrap:anywhere]">{formatSats(locale, rebalance?.success_attempts_24h)}</p>
              </div>
              <div className={metricTileClass}>
                <p className={metricLabelClass}>{t('dashboard.liveCostLabel')}</p>
                <p className="mt-2 text-lg font-semibold [overflow-wrap:anywhere]">{formatSats(locale, rebalance?.live_cost_sat)} sats</p>
              </div>
            </div>

            <div className="min-w-0 overflow-hidden rounded-2xl border border-white/10 bg-ink/30 px-4 py-3 text-sm">
              <div className={splitRowClass}>
                <span className="min-w-0 text-fog/60 [overflow-wrap:anywhere]">{t('dashboard.remainingBudgetLabel')}</span>
                <span className="min-w-0 text-right [overflow-wrap:anywhere]">{formatSats(locale, rebalance?.remaining_total_sat ?? 0)} sats</span>
              </div>
              <div className={`mt-2 ${splitRowClass}`}>
                <span className="min-w-0 text-fog/60 [overflow-wrap:anywhere]">{t('dashboard.autofeeStatusLabel')}</span>
                <div className="max-w-full shrink-0">
                  <StatusBadge label={autofee?.running ? t('dashboard.runningLabel') : t('dashboard.idleLabel')} tone={autofee?.running ? 'ok' : 'muted'} />
                </div>
              </div>
              <div className="mt-2 text-xs leading-snug text-fog/50 [overflow-wrap:anywhere]">
                {autofee?.last_error
                  ? `${t('dashboard.lastErrorLabel')}: ${autofee.last_error}`
                  : `${t('dashboard.nextRunLabel')}: ${formatTimeAgo(locale, autofee?.next_run_at)}`}
              </div>
            </div>
          </div>
        </div>

        <div className={panelClass}>
          <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2">
            <div className="min-w-0 flex-1">
              <h4 className="text-base font-semibold leading-snug [overflow-wrap:anywhere]">{t('dashboard.automationsTitle')}</h4>
              <p className="mt-1 text-sm leading-snug text-fog/60 [overflow-wrap:anywhere]">{t('dashboard.automationsHint')}</p>
            </div>
          </div>

          <div className="mt-4 grid min-w-0 gap-3">
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
              extra={buildChanHealExtra()}
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

        <div className={panelClass}>
          <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-2">
            <div className="min-w-0 flex-1 basis-44">
              <h4 className="text-base font-semibold leading-snug [overflow-wrap:anywhere]">{t('dashboard.riskRecoveryTitle')}</h4>
              <p className="mt-1 text-sm leading-snug text-fog/60 [overflow-wrap:anywhere]">{t('dashboard.riskRecoveryHint')}</p>
            </div>
            <div className="max-w-full shrink-0">
              <StatusBadge
                label={actionRequiredCount > 0 ? t('dashboard.actionRequiredBadge', { count: actionRequiredCount }) : t('common.ok')}
                tone={actionRequiredCount > 0 ? 'danger' : 'ok'}
              />
            </div>
          </div>

          <div className="mt-4 grid min-w-0 gap-3">
            <div className={metricGridClass}>
              <div className={metricTileClass}>
                <p className={metricLabelClass}>{t('dashboard.activeClosingsLabel')}</p>
                <p className="mt-2 text-lg font-semibold [overflow-wrap:anywhere]">{formatSats(locale, closeManager?.active_count)}</p>
              </div>
              <div className={metricTileClass}>
                <p className={metricLabelClass}>{t('dashboard.blockedHtlcLabel')}</p>
                <p className="mt-2 text-lg font-semibold [overflow-wrap:anywhere]">{formatSats(locale, closeManager?.htlc_blocked_count)}</p>
              </div>
            </div>

            <div className="min-w-0 overflow-hidden rounded-2xl border border-white/10 bg-ink/30 px-4 py-3">
              <div className={splitRowClass}>
                <span className="min-w-0 text-sm text-fog/70 [overflow-wrap:anywhere]">{t('dashboard.nodeRetirementLabel')}</span>
                <div className="max-w-full shrink-0">
                  <StatusBadge
                    label={nodeRetirement?.active ? nodeRetirement.active_state || t('dashboard.activeLabel') : t('common.inactive')}
                    tone={nodeRetirement?.active ? 'warn' : 'muted'}
                  />
                </div>
              </div>
              <p className="mt-2 text-xs leading-snug text-fog/50 [overflow-wrap:anywhere]">
                {successionConfig?.enabled
                  ? `${t('dashboard.successionDeadlineLabel')}: ${formatTimeAgo(locale, successionConfig.deadline_at)}`
                  : t('dashboard.successionDisabledHint')}
              </p>
            </div>

            <div className="min-w-0 overflow-hidden rounded-2xl border border-white/10 bg-ink/30 px-4 py-3">
              <div className={splitRowClass}>
                <span className="min-w-0 text-sm text-fog/70 [overflow-wrap:anywhere]">{t('dashboard.successionLabel')}</span>
                <div className="max-w-full shrink-0">
                  <StatusBadge
                    label={successionConfig?.status || (successionConfig?.enabled ? t('dashboard.armedLabel') : t('common.disabled'))}
                    tone={successionConfig?.enabled ? 'warn' : 'muted'}
                  />
                </div>
              </div>
              <div className="mt-2 grid gap-2 text-xs leading-snug text-fog/50 [grid-template-columns:repeat(auto-fit,minmax(min(100%,7rem),1fr))]">
                <span className="min-w-0 [overflow-wrap:anywhere]">{t('dashboard.lastAliveLabel')}: {formatTimeAgo(locale, successionConfig?.last_alive_at)}</span>
                <span className="min-w-0 [overflow-wrap:anywhere]">{t('dashboard.nextCheckLabel')}: {formatTimeAgo(locale, successionConfig?.next_check_at)}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </article>
  )
}
