import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { moduleHash } from './links'
import type { ChannelCapitalPlanItem, ChannelCapitalPlanSummary, ChannelRankingFormatters } from './types'

type Props = {
  items: ChannelCapitalPlanItem[]
  summary: ChannelCapitalPlanSummary
  magmaStateKnown: boolean
  loading: boolean
  format: ChannelRankingFormatters
  onSelect: (item: ChannelCapitalPlanItem) => void
}

const actionOrder: ChannelCapitalPlanItem['action'][] = [
  'rotate',
  'refill',
  'reprice',
  'expand',
  'maintain',
  'observe',
  'protected',
  'parked'
]

const actionClass = (action: ChannelCapitalPlanItem['action']) => {
  switch (action) {
    case 'rotate':
      return 'border-rose-400/35 bg-rose-500/10 text-rose-100'
    case 'refill':
      return 'border-cyan-400/35 bg-cyan-500/10 text-cyan-100'
    case 'reprice':
      return 'border-violet-400/35 bg-violet-500/10 text-violet-100'
    case 'expand':
      return 'border-emerald-400/35 bg-emerald-500/10 text-emerald-100'
    case 'protected':
      return 'border-sky-400/35 bg-sky-500/10 text-sky-100'
    case 'parked':
      return 'border-amber-400/35 bg-amber-500/10 text-amber-100'
    default:
      return 'border-white/10 bg-white/[0.03] text-fog/80'
  }
}

export default function ChannelCapitalPlanPanel({ items, summary, magmaStateKnown, loading, format, onSelect }: Props) {
  const { t } = useTranslation()
  const groups = useMemo(
    () => actionOrder
      .map((action) => ({ action, items: items.filter((item) => item.action === action) }))
      .filter((group) => group.items.length > 0),
    [items]
  )

  if (loading) {
    return <p className="text-sm text-fog/70">{t('channelRanking.loading')}</p>
  }

  return (
    <div className="space-y-5">
      {!magmaStateKnown && (
        <div className="rounded-2xl border border-amber-400/35 bg-amber-500/10 px-4 py-3 text-sm text-amber-100">
          {t('channelRanking.plan.magmaUnavailable')}
        </div>
      )}

      <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <div className="rounded-3xl border border-emerald-400/20 bg-emerald-500/[0.07] p-4">
          <div className="text-[11px] uppercase tracking-wide text-emerald-200/70">{t('channelRanking.plan.productiveCapital')}</div>
          <div className="mt-2 text-xl font-semibold text-emerald-100">{format.sats(summary.productive_capital_sat)}</div>
        </div>
        <div className="rounded-3xl border border-cyan-400/20 bg-cyan-500/[0.07] p-4">
          <div className="text-[11px] uppercase tracking-wide text-cyan-200/70">{t('channelRanking.plan.growthActions')}</div>
          <div className="mt-2 text-xl font-semibold text-cyan-100">
            {format.number.format((summary.action_counts.refill || 0) + (summary.action_counts.expand || 0))}
          </div>
        </div>
        <div className="rounded-3xl border border-sky-400/20 bg-sky-500/[0.07] p-4">
          <div className="text-[11px] uppercase tracking-wide text-sky-200/70">{t('channelRanking.plan.protectedCapital')}</div>
          <div className="mt-2 text-xl font-semibold text-sky-100">{format.sats(summary.protected_capital_sat)}</div>
          <div className="mt-1 text-xs text-sky-100/60">{t('channelRanking.plan.parkedCapital', { value: format.sats(summary.parked_capital_sat) })}</div>
        </div>
        <div className="rounded-3xl border border-rose-400/20 bg-rose-500/[0.07] p-4">
          <div className="text-[11px] uppercase tracking-wide text-rose-200/70">{t('channelRanking.plan.recoverableCapital')}</div>
          <div className="mt-2 text-xl font-semibold text-rose-100">{format.sats(summary.recoverable_local_sat)}</div>
          <div className="mt-1 text-xs text-rose-100/60">{t('channelRanking.plan.afterCoopClose')}</div>
        </div>
      </section>

      {groups.length === 0 ? (
        <div className="rounded-3xl border border-white/10 bg-ink/60 p-5 text-sm text-fog/70">
          {t('channelRanking.empty')}
        </div>
      ) : groups.map((group) => (
        <section key={group.action} className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
          <div className="mb-4 flex flex-wrap items-end justify-between gap-2">
            <div>
              <h3 className="text-lg font-semibold">{t(`channelRanking.plan.actions.${group.action}` as any)}</h3>
              <p className="mt-1 text-xs text-fog/60">{t(`channelRanking.plan.actionHints.${group.action}` as any)}</p>
            </div>
            <span className="text-xs text-fog/55">{t('channelRanking.listCount', { count: group.items.length })}</span>
          </div>
          <div className="grid gap-3 xl:grid-cols-2">
            {group.items.map((planItem) => {
              const channel = planItem.channel
              return (
                <article key={channel.channel_point} className={`rounded-2xl border p-4 ${actionClass(group.action)}`}>
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                      <button type="button" className="max-w-full truncate text-left font-medium hover:underline" onClick={() => onSelect(planItem)}>
                        {channel.peer_alias || channel.peer_pubkey || channel.channel_point}
                      </button>
                      <div className="mt-1 flex flex-wrap gap-2 text-[11px] opacity-75">
                        <span>{t('channelRanking.plan.rankingBadge', { value: t(`channelRanking.states.${channel.state}` as any) })}</span>
                        <span>{t('channelRanking.scoreShort', { value: format.number.format(channel.score) })}</span>
                        <span>{t('channelRanking.localBalancePct', { value: format.pct(channel.local_balance_pct) })}</span>
                      </div>
                    </div>
                    <span className="rounded-full border border-current/20 px-2.5 py-1 text-[11px]">
                      {t(`channelRanking.plan.actions.${planItem.action}` as any)}
                    </span>
                  </div>

                  <div className="mt-3 grid gap-2 text-xs opacity-80 sm:grid-cols-3">
                    <div>{t('channelRanking.netFees7d', { value: format.sats(channel.profit_fee_7d_sat) })}</div>
                    <div>{t('channelRanking.netFees30d', { value: format.sats(channel.profit_fee_30d_sat) })}</div>
                    <div>{t('channelRanking.plan.observation', { current: planItem.observation_days, required: planItem.observation_required_days })}</div>
                  </div>

                  {planItem.magma_commitment && (
                    <div className="mt-3 rounded-xl border border-sky-300/20 bg-sky-950/20 px-3 py-2 text-xs text-sky-100/85">
                      {t('channelRanking.plan.magmaCommitment', {
                        buyer: planItem.magma_commitment.buyer_alias || planItem.magma_commitment.buyer_pubkey || t('common.unknown'),
                        blocks: planItem.magma_commitment.blocks_remaining ?? t('common.na')
                      })}
                    </div>
                  )}

                  {(planItem.blockers || []).length > 0 && (
                    <div className="mt-3 flex flex-wrap gap-2">
                      {planItem.blockers!.map((blocker) => (
                        <span key={blocker} className="rounded-full border border-current/20 px-2 py-0.5 text-[11px] opacity-80">
                          {t(`channelRanking.plan.blockers.${blocker}` as any, { defaultValue: blocker })}
                        </span>
                      ))}
                    </div>
                  )}

                  <div className="mt-4 flex flex-wrap items-center gap-2">
                    <button type="button" className="btn-secondary" onClick={() => onSelect(planItem)}>
                      {t('channelRanking.plan.inspect')}
                    </button>
                    {planItem.eligible && planItem.primary_action && (
                      <a className="btn-primary" href={moduleHash(channel, planItem.primary_action.target_module)}>
                        {t(`channelRanking.plan.primaryActions.${planItem.primary_action.code}` as any)}
                      </a>
                    )}
                  </div>
                </article>
              )
            })}
          </div>
        </section>
      ))}
    </div>
  )
}
