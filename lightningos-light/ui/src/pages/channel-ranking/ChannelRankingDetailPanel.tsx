import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { updateLnChannelAutomation } from '../../api'
import { graphExplorerHash, lightningOpsHash, moduleHash } from './links'
import type { ChannelCapitalPlanItem, ChannelRankingDetailPayload, ChannelRankingFormatters, ChannelRankingItem } from './types'

type DetailTab = 'summary' | 'economics' | 'flow' | 'automation' | 'history' | 'peer'

type Props = {
  planItem: ChannelCapitalPlanItem | null
  detail: ChannelRankingDetailPayload | null
  loading: boolean
  format: ChannelRankingFormatters
  onClose: () => void
  onSelectChannel: (channelPoint: string) => void
  onPolicyChanged: () => Promise<void>
}

const rankingClass = (state?: string) => {
  switch (state) {
    case 'expand': return 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200'
    case 'maintain': return 'border-sky-300/30 bg-sky-500/15 text-sky-100'
    case 'close': return 'border-rose-400/30 bg-rose-500/15 text-rose-100'
    default: return 'border-amber-300/30 bg-amber-500/15 text-amber-100'
  }
}

const MetricGrid = ({ children }: { children: React.ReactNode }) => (
  <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">{children}</div>
)

const Metric = ({ children }: { children: React.ReactNode }) => (
  <div className="rounded-xl border border-white/10 bg-white/[0.025] px-3 py-2 text-xs text-fog/70">{children}</div>
)

export default function ChannelRankingDetailPanel({ planItem, detail, loading, format, onClose, onSelectChannel, onPolicyChanged }: Props) {
  const { t } = useTranslation()
  const [tab, setTab] = useState<DetailTab>('summary')
  const [automationBusy, setAutomationBusy] = useState(false)
  const [automationStatus, setAutomationStatus] = useState('')
  const [fixedFee, setFixedFee] = useState('')
  const [reviewAt, setReviewAt] = useState('')
  const [note, setNote] = useState('')

  const channel = detail?.item || planItem?.channel || null
  const isParked = channel?.automation_mode === 'parked'
  const history = useMemo(() => {
    const result = []
    for (const point of detail?.history || []) {
      if (result[result.length - 1]?.score === point.score) continue
      result.push(point)
    }
    return result
  }, [detail?.history])

  useEffect(() => {
    setTab('summary')
    setAutomationStatus('')
    setFixedFee(channel?.fixed_fee_ppm !== undefined ? String(channel.fixed_fee_ppm) : '')
    setReviewAt(channel?.review_at ? String(channel.review_at).slice(0, 10) : '')
    setNote(channel?.automation_note || '')
  }, [channel?.channel_point, channel?.fixed_fee_ppm, channel?.review_at, channel?.automation_note])

  if (!channel || !planItem) return null

  const reasonLabel = (code?: string) => t(`channelRanking.reasons.${String(code || '').trim()}` as any, { defaultValue: code || t('common.unknown') })
  const recommendationLabel = (code?: string) => t(`channelRanking.recommendations.${String(code || '').trim()}` as any, { defaultValue: code || t('common.unknown') })
  const targetLabel = (target?: string) => t(`channelRanking.targetModules.${String(target || '').trim()}` as any, { defaultValue: target || t('common.na') })

  const handleAutomation = async () => {
    setAutomationStatus('')
    let parsedFee: number | undefined
    if (!isParked && fixedFee.trim()) {
      parsedFee = Number(fixedFee)
      if (!Number.isFinite(parsedFee) || parsedFee < 0) {
        setAutomationStatus(t('channelRanking.automationInvalidFixedFee'))
        return
      }
    }
    if (!isParked && !window.confirm(t('channelRanking.automationParkConfirm'))) return
    setAutomationBusy(true)
    try {
      await updateLnChannelAutomation({
        channel_id: channel.channel_id,
        channel_point: channel.channel_point,
        automation_mode: isParked ? 'normal' : 'parked',
        fixed_fee_ppm: !isParked && parsedFee !== undefined ? Math.round(parsedFee) : undefined,
        review_at: !isParked ? reviewAt.trim() : '',
        automation_note: !isParked ? note.trim() : '',
        restore_previous: isParked
      })
      setAutomationStatus(t(isParked ? 'channelRanking.automationRestoredSaved' : 'channelRanking.automationParkedSaved'))
      await onPolicyChanged()
    } catch (error: any) {
      setAutomationStatus(error?.message || t('channelRanking.automationUpdateFailed'))
    } finally {
      setAutomationBusy(false)
    }
  }

  const tabs: DetailTab[] = ['summary', 'economics', 'flow', 'automation', 'history', 'peer']

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/60 backdrop-blur-sm" role="dialog" aria-modal="true" aria-label={t('channelRanking.detailTitle')}>
      <button type="button" className="min-w-0 flex-1 cursor-default" onClick={onClose} aria-label={t('common.close')} />
      <aside className="flex h-full w-full max-w-3xl flex-col border-l border-white/10 bg-[#0d1219] shadow-2xl">
        <header className="border-b border-white/10 p-4 sm:p-5">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <div className="truncate text-lg font-semibold">{channel.peer_alias || channel.peer_pubkey || channel.channel_point}</div>
              <div className="mt-1 break-all text-[11px] text-fog/45">{channel.channel_point}</div>
              <div className="mt-3 flex flex-wrap gap-2">
                <span className={`rounded-full border px-2.5 py-1 text-[11px] ${rankingClass(channel.state)}`}>{t('channelRanking.plan.rankingBadge', { value: t(`channelRanking.states.${channel.state}` as any) })}</span>
                <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-fog/75">{t('channelRanking.scoreShort', { value: channel.score })}</span>
                <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] text-fog/75">{t('channelRanking.plan.actionBadge', { value: t(`channelRanking.plan.actions.${planItem.action}` as any) })}</span>
                {isParked && <span className="rounded-full border border-amber-300/30 bg-amber-500/10 px-2.5 py-1 text-[11px] text-amber-100">{t('channelRanking.plan.policyBadge', { value: t('channelRanking.automationModes.parked') })}</span>}
                {planItem.magma_commitment && <span className="rounded-full border border-sky-300/30 bg-sky-500/10 px-2.5 py-1 text-[11px] text-sky-100">{t('channelRanking.plan.constraintBadge', { value: 'Magma' })}</span>}
              </div>
            </div>
            <button type="button" className="btn-secondary" onClick={onClose}>{t('common.close')}</button>
          </div>
          <nav className="mt-4 flex gap-2 overflow-x-auto pb-1">
            {tabs.map((key) => <button key={key} type="button" className={tab === key ? 'btn-primary' : 'btn-secondary'} onClick={() => setTab(key)}>{t(`channelRanking.detailTabs.${key}` as any)}</button>)}
          </nav>
        </header>

        <div className="flex-1 overflow-y-auto p-4 sm:p-5">
          {loading && <div className="mb-4 text-xs text-fog/50">{t('channelRanking.loading')}</div>}

          {tab === 'summary' && (
            <div className="space-y-5">
              <section>
                <h4 className="mb-3 text-sm font-medium">{t('channelRanking.metricsTitle')}</h4>
                <MetricGrid>
                  <Metric>{t('channelRanking.capacity', { value: format.sats(channel.capacity_sat) })}</Metric>
                  <Metric>{t('channelRanking.localBalance', { value: format.sats(channel.local_balance_sat) })} · {format.pct(channel.local_balance_pct)}</Metric>
                  <Metric>{t('channelRanking.remoteBalance', { value: format.sats(channel.remote_balance_sat) })} · {format.pct(channel.remote_balance_pct)}</Metric>
                  <Metric>{t('channelRanking.peerStabilityScore30d', { value: channel.peer_stability_score_30d || 0 })}</Metric>
                  <Metric>{t('channelRanking.peerSampleCount30d', { value: channel.peer_sample_count_30d || 0 })}</Metric>
                  <Metric>{t('channelRanking.rebalanceDependenceScore', { value: channel.rebalance_dependence_score })}</Metric>
                  <Metric>{t('channelRanking.htlcFailures30dLabel', { value: channel.htlc_failures_30d || 0 })}</Metric>
                  <Metric>{t('channelRanking.pendingHtlcCount', { value: channel.pending_htlc_count || 0 })}</Metric>
                  <Metric>{t('channelRanking.inactiveDuration', { value: channel.active ? t('common.na') : format.duration(channel.inactive_duration_sec) })}</Metric>
                  <Metric>{t('channelRanking.liquidityStateLabel', { value: channel.liquidity_state ? t(`liquidityStates.${channel.liquidity_state === 'offer-ready' ? 'offerReady' : channel.liquidity_state === 'extreme-drained' ? 'extremeDrained' : channel.liquidity_state}` as any) : t('common.unknown') })}</Metric>
                  <Metric>{t('channelRanking.autofeeOutRatioEffective', { value: format.ratioPct(channel.autofee_out_ratio_effective) })}</Metric>
                  <Metric>{t('channelRanking.liquidityStateAt', { value: format.timestamp(channel.liquidity_state_at) })}</Metric>
                  {channel.class_label && <Metric>{t('channelRanking.classLabel', { value: channel.class_label })}</Metric>}
                  <Metric>{t('channelRanking.computedAt', { value: format.timestamp(channel.computed_at) })}</Metric>
                </MetricGrid>
              </section>

              {(planItem.blockers || []).length > 0 && (
                <section className="rounded-2xl border border-amber-300/25 bg-amber-500/[0.07] p-4">
                  <h4 className="text-sm font-medium text-amber-100">{t('channelRanking.plan.constraintsTitle')}</h4>
                  <div className="mt-2 flex flex-wrap gap-2">{planItem.blockers!.map((blocker) => <span key={blocker} className="rounded-full border border-amber-200/20 px-2.5 py-1 text-xs text-amber-100/80">{t(`channelRanking.plan.blockers.${blocker}` as any, { defaultValue: blocker })}</span>)}</div>
                </section>
              )}

              <section>
                <h4 className="mb-2 text-sm font-medium">{t('channelRanking.reasonsTitle')}</h4>
                <div className="flex flex-wrap gap-2">{(channel.reasons || []).length ? channel.reasons!.map((reason) => <span key={reason.code} className="rounded-full border border-white/10 bg-white/[0.03] px-2.5 py-1 text-xs text-fog/75">{reasonLabel(reason.code)}</span>) : <span className="text-sm text-fog/60">{t('common.na')}</span>}</div>
              </section>

              <section>
                <h4 className="mb-2 text-sm font-medium">{t('channelRanking.recommendationsTitle')}</h4>
                <div className="space-y-2">
                  {(channel.recommendations || []).length ? channel.recommendations!.map((recommendation) => {
                    const blockedClose = recommendation.target_module === 'close-manager' && !planItem.eligible
                    return blockedClose ? (
                      <div key={`${recommendation.code}-${recommendation.target_module}`} className="flex justify-between gap-3 rounded-2xl border border-white/10 bg-white/[0.02] px-3 py-2 text-sm text-fog/45">
                        <span>{recommendationLabel(recommendation.code)}</span><span>{t('channelRanking.plan.blocked')}</span>
                      </div>
                    ) : (
                      <a key={`${recommendation.code}-${recommendation.target_module}`} href={moduleHash(channel, recommendation.target_module)} className="flex justify-between gap-3 rounded-2xl border border-white/10 bg-white/[0.03] px-3 py-2 text-sm text-fog/80 transition hover:border-white/20">
                        <span>{recommendationLabel(recommendation.code)}</span><span className="shrink-0 text-xs text-sky-200">{targetLabel(recommendation.target_module)}</span>
                      </a>
                    )
                  }) : <span className="text-sm text-fog/60">{t('common.na')}</span>}
                </div>
              </section>
            </div>
          )}

          {tab === 'economics' && (
            <div className="grid gap-4 xl:grid-cols-2">
              {([7, 30] as const).map((days) => (
                <section key={days} className="rounded-2xl border border-white/10 bg-white/[0.025] p-4">
                  <h4 className="mb-3 font-medium">{t(days === 7 ? 'channelRanking.window7dTitle' : 'channelRanking.window30dTitle')}</h4>
                  <div className="space-y-2 text-sm text-fog/70">
                    <div>{t(`channelRanking.forwardFees${days}d` as any, { value: format.sats(days === 7 ? channel.forward_fee_7d_sat : channel.forward_fee_30d_sat) })}</div>
                    <div>{t(`channelRanking.forwardAmount${days}d` as any, { value: format.sats(days === 7 ? channel.forward_amt_7d_sat : channel.forward_amt_30d_sat) })}</div>
                    <div>{t(`channelRanking.assistedForwardFees${days}d` as any, { value: format.sats(days === 7 ? channel.assisted_forward_fee_7d_sat : channel.assisted_forward_fee_30d_sat) })}</div>
                    <div>{t(`channelRanking.assistedForwardAmount${days}d` as any, { value: format.sats(days === 7 ? channel.assisted_forward_amt_7d_sat : channel.assisted_forward_amt_30d_sat) })}</div>
                    <div>{t(`channelRanking.outPpm${days}d` as any, { value: format.number.format(days === 7 ? channel.out_ppm_7d : channel.out_ppm_30d) })}</div>
                    <div>{t(`channelRanking.rebalanceFees${days}d` as any, { value: format.sats(days === 7 ? channel.rebal_fee_7d_sat : channel.rebal_fee_30d_sat) })}</div>
                    <div>{t(`channelRanking.rebalanceAmount${days}d` as any, { value: format.sats(days === 7 ? channel.rebal_amt_7d_sat : channel.rebal_amt_30d_sat) })}</div>
                    <div>{t(`channelRanking.rebalancePpm${days}d` as any, { value: format.number.format(days === 7 ? channel.rebal_ppm_7d : channel.rebal_ppm_30d) })}</div>
                    <div className="border-t border-white/10 pt-2 font-medium text-fog">{t(`channelRanking.netFees${days}d` as any, { value: format.sats(days === 7 ? channel.profit_fee_7d_sat : channel.profit_fee_30d_sat) })}</div>
                  </div>
                </section>
              ))}
              <section className="rounded-2xl border border-white/10 bg-white/[0.025] p-4 xl:col-span-2">
                <h4 className="mb-3 font-medium">{t('channelRanking.trendSectionTitle')}</h4>
                <MetricGrid>
                  <Metric>{t('channelRanking.score7dLabel', { value: channel.score_7d })}</Metric>
                  <Metric>{t('channelRanking.score30dLabel', { value: channel.score_30d })}</Metric>
                  <Metric>{t('channelRanking.trendDirectionLabel', { value: t(`channelRanking.trends.${channel.trend_direction || 'stable'}` as any) })} · {t('channelRanking.trendDelta', { value: channel.trend_delta || 0 })}</Metric>
                </MetricGrid>
              </section>
            </div>
          )}

          {tab === 'flow' && (
            <div className="space-y-4">
              <MetricGrid>
                <Metric>{t('channelRanking.forwardIn7d', { value: format.flow(channel.forward_in_count_7d, channel.forward_in_amount_sat_7d) })}</Metric>
                <Metric>{t('channelRanking.forwardOut7d', { value: format.flow(channel.forward_out_count_7d, channel.forward_out_amount_sat_7d) })}</Metric>
                <Metric>{t('channelRanking.htlcPolicyFails30d', { value: channel.htlc_policy_fails_30d || 0 })}</Metric>
                <Metric>{t('channelRanking.htlcLiquidityFails30d', { value: channel.htlc_liquidity_fails_30d || 0 })}</Metric>
                <Metric>{t('channelRanking.htlcForwardFails30d', { value: channel.htlc_forward_fails_30d || 0 })}</Metric>
              </MetricGrid>
              <div className="grid gap-4 xl:grid-cols-2">
                {[
                  { title: t('channelRanking.topForwardInSourcesTitle'), entries: detail?.top_forward_in_sources || [], key: 'source' },
                  { title: t('channelRanking.topForwardOutSinksTitle'), entries: detail?.top_forward_out_sinks || [], key: 'sink' }
                ].map((group) => (
                  <section key={group.key} className="rounded-2xl border border-white/10 bg-white/[0.025] p-4">
                    <h4 className="mb-3 font-medium">{group.title}</h4>
                    <div className="space-y-2">{group.entries.length ? group.entries.map((entry) => (
                      <div key={`${group.key}-${entry.channel_id}-${entry.channel_point || ''}`} className="rounded-xl border border-white/10 px-3 py-2 text-sm text-fog/80">
                        <div className="truncate font-medium text-fog">{entry.peer_alias || entry.channel_point || String(entry.channel_id)}</div>
                        <div className="mt-1 text-xs text-fog/60">{t('channelRanking.counterpartyFlowSummary', { count: entry.forward_count_7d || 0, value: format.sats(entry.forward_amount_sat_7d) })}</div>
                      </div>
                    )) : <div className="text-sm text-fog/60">{t('channelRanking.topCounterpartiesEmpty')}</div>}</div>
                  </section>
                ))}
              </div>
            </div>
          )}

          {tab === 'automation' && (
            <div className="space-y-4">
              <section className={`rounded-2xl border p-4 ${isParked ? 'border-amber-300/30 bg-amber-500/[0.07]' : 'border-white/10 bg-white/[0.025]'}`}>
                <h4 className="font-medium">{t('channelRanking.automationPolicyTitle')}</h4>
                <p className="mt-1 text-sm text-fog/65">{t(isParked ? 'channelRanking.automationParkedDetail' : 'channelRanking.automationNormalDetail')}</p>
                <div className="mt-4 grid gap-3 sm:grid-cols-2">
                  <label className="text-xs text-fog/65">{t('channelRanking.automationFixedFeeLabel')}<input className="input-field mt-1" type="number" min="0" value={fixedFee} disabled={isParked || automationBusy} onChange={(event) => setFixedFee(event.target.value)} placeholder={t('channelRanking.automationFixedFeePlaceholder')} /></label>
                  <label className="text-xs text-fog/65">{t('channelRanking.automationReviewAtLabel')}<input className="input-field mt-1" type="date" value={reviewAt} disabled={isParked || automationBusy} onChange={(event) => setReviewAt(event.target.value)} /></label>
                  <label className="text-xs text-fog/65 sm:col-span-2">{t('channelRanking.automationNoteLabel')}<textarea className="input-field mt-1 min-h-24" value={note} disabled={isParked || automationBusy} onChange={(event) => setNote(event.target.value)} placeholder={t('channelRanking.automationNotePlaceholder')} /></label>
                </div>
                <p className="mt-3 text-xs text-fog/55">{isParked && channel.parked_at ? t('channelRanking.automationParkedAt', { value: format.timestamp(channel.parked_at) }) : t('channelRanking.automationSideEffects')}</p>
                <div className="mt-4 flex flex-wrap items-center gap-3">
                  <button type="button" className={isParked ? 'btn-primary' : 'btn-secondary'} disabled={automationBusy} onClick={() => void handleAutomation()}>
                    {automationBusy ? t('common.loading') : t(isParked ? 'channelRanking.automationRestoreAction' : 'channelRanking.automationParkAction')}
                  </button>
                  {automationStatus && <span className="text-xs text-fog/70">{automationStatus}</span>}
                </div>
              </section>
              {isParked && channel.review_at && <div className="rounded-2xl border border-amber-300/20 bg-amber-500/[0.05] p-4 text-sm text-amber-100">{t('channelRanking.plan.reviewDue', { value: format.timestamp(channel.review_at) })}</div>}
            </div>
          )}

          {tab === 'history' && (
            <div className="space-y-4">
              <section className="rounded-2xl border border-white/10 bg-white/[0.025] p-4">
                <h4 className="mb-3 font-medium">{t('channelRanking.feedbackTitle')}</h4>
                {detail?.feedback ? <MetricGrid>
                  <Metric>{t('channelRanking.feedbackDirectionLabel', { value: t(`channelRanking.trends.${detail.feedback.direction || 'stable'}` as any) })}</Metric>
                  <Metric>{t('channelRanking.feedbackScoreDeltaLabel', { value: detail.feedback.score_delta || 0 })}</Metric>
                  <Metric>{t('channelRanking.feedbackNetDeltaLabel', { value: format.sats(detail.feedback.net_delta_sat) })}</Metric>
                  <Metric>{t('channelRanking.feedbackWindowLabel', { value: detail.feedback.window_hours || 0 })}</Metric>
                  <Metric>{t('channelRanking.feedbackBaselineAtLabel', { value: format.timestamp(detail.feedback.baseline_at) })}</Metric>
                </MetricGrid> : <p className="text-sm text-fog/60">{t('channelRanking.feedbackEmpty')}</p>}
              </section>
              <section className="rounded-2xl border border-white/10 bg-white/[0.025] p-4">
                <h4 className="mb-3 font-medium">{t('channelRanking.historyTitle')}</h4>
                <div className="space-y-2">{history.length ? history.map((point) => <div key={point.computed_at} className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-white/10 px-3 py-2 text-xs text-fog/70"><span>{format.timestamp(point.computed_at)}</span><span>{t('channelRanking.score7dVs30d', { score7d: point.score_7d, score30d: point.score_30d })}</span><span className={`rounded-full border px-2 py-0.5 ${rankingClass(point.state)}`}>{t(`channelRanking.states.${point.state}` as any)}</span><span>{t('channelRanking.netFees7d', { value: format.sats(point.profit_fee_7d_sat) })}</span></div>) : <p className="text-sm text-fog/60">{t('channelRanking.historyEmpty')}</p>}</div>
              </section>
            </div>
          )}

          {tab === 'peer' && (
            <div className="space-y-4">
              <div className="flex flex-wrap gap-2">
                {channel.peer_pubkey && <a className="btn-secondary" href={graphExplorerHash(channel.peer_pubkey)}>{t('nav.graphExplorer')}</a>}
                <a className="btn-secondary" href={lightningOpsHash(channel.channel_point)}>{t('channelRanking.openInLightningOps')}</a>
              </div>
              <MetricGrid>
                <Metric>{t('channelRanking.channelId', { value: channel.channel_id })}</Metric>
                <Metric>{t('channelRanking.peerPubkey', { value: channel.peer_pubkey || t('common.na') })}</Metric>
              </MetricGrid>
              <section className="rounded-2xl border border-white/10 bg-white/[0.025] p-4">
                <h4 className="mb-3 font-medium">{t('channelRanking.peerComparisonTitle')}</h4>
                <div className="space-y-2">{(detail?.peer_channels || []).filter((entry) => entry.channel_point !== channel.channel_point).length ? detail!.peer_channels!.filter((entry) => entry.channel_point !== channel.channel_point).map((entry) => (
                  <button key={entry.channel_point} type="button" onClick={() => onSelectChannel(entry.channel_point)} className="flex w-full items-start justify-between gap-3 rounded-xl border border-white/10 px-3 py-2 text-left text-sm text-fog/75 transition hover:border-white/20">
                    <div className="min-w-0"><div className="truncate font-medium text-fog">{entry.peer_alias || entry.channel_point}</div><div className="mt-1 text-xs text-fog/55">{t('channelRanking.peerComparisonSummary', { score: entry.score, net7d: format.sats(entry.profit_fee_7d_sat), capacity: format.sats(entry.capacity_sat) })}</div></div>
                    <span className={`rounded-full border px-2 py-0.5 text-[11px] ${rankingClass(entry.state)}`}>{t(`channelRanking.states.${entry.state}` as any)}</span>
                  </button>
                )) : <p className="text-sm text-fog/60">{t('channelRanking.peerComparisonEmpty')}</p>}</div>
              </section>
            </div>
          )}
        </div>
      </aside>
    </div>
  )
}
