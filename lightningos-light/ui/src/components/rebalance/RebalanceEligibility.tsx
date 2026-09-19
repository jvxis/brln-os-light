import { useTranslation } from 'react-i18next'
import { rebalanceEligibility } from './eligibility'
import type { RebalanceChannel, RebalanceConfig } from './types'

export default function RebalanceEligibility({ channel, config }: { channel?: RebalanceChannel; config?: RebalanceConfig | null }) {
  const { t } = useTranslation()
  if (!channel || !config) return <span className="text-xs text-fog/50">{t('rebalanceEligibility.unavailable')}</span>
  const state = rebalanceEligibility(channel, config)
  const badge = 'rounded-full border border-white/15 px-2 py-0.5 text-[11px]'
  return (
    <details className="mt-2 max-w-full text-xs text-fog/70" onClick={(event) => event.stopPropagation()}>
      <summary className="cursor-pointer rounded focus-visible:outline focus-visible:outline-2 focus-visible:outline-sky-300" aria-label={t('rebalanceEligibility.details')}>
        <span className="inline-flex max-w-full flex-wrap items-center gap-1 align-middle">
          <span className={badge}>{t(`rebalanceEligibility.states.${state.automation}`)}</span>
          {state.costBlocked && <span className={`${badge} border-amber-300/40 text-amber-200`}>{t('rebalanceEligibility.costBlocked')}</span>}
          {state.sourceExcluded && <span className={`${badge} text-rose-200`}>{t('rebalanceEligibility.sourceExcluded')}</span>}
          {!state.costBlocked && channel.economic_blocked_reason && <span className={`${badge} text-amber-200`}>{t('rebalanceEligibility.economicWarning')}</span>}
        </span>
      </summary>
      <div className="mt-2 max-w-md space-y-2 rounded-xl border border-white/10 bg-ink/80 p-3 text-xs leading-relaxed">
        <p>{t(`rebalanceEligibility.hints.${state.automation}`)}</p>
        <p>{t(state.costBlocked ? 'rebalanceEligibility.costHint' : 'rebalanceEligibility.noRoutePromise', { budget: channel.fee_budget_ppm, cost: channel.rebalance_cost_7d_ppm > 0 ? channel.rebalance_cost_7d_ppm : config.rebalance_cost_floor_ppm })}</p>
        {(channel.auto_bypass_cost_gate || state.conviction) && <p>{t(state.conviction ? 'rebalanceEligibility.convictionHint' : 'rebalanceEligibility.bypassHint')}</p>}
        {channel.economic_blocked_reason && <p>{t(`rebalanceEligibility.reasons.${channel.economic_blocked_reason}`, { defaultValue: t('rebalanceEligibility.economicWarningHint') })}</p>}
        {state.sourceExcluded && <p>{t('rebalanceEligibility.sourceHint')}</p>}
        <p className="text-fog/50">{t('rebalanceEligibility.snapshot')}</p>
      </div>
    </details>
  )
}
