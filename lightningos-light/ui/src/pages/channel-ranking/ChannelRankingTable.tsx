import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { ChannelCapitalPlanItem, ChannelRankingFormatters, ChannelRankingItem } from './types'

type RankingFilter = 'all' | ChannelRankingItem['state']
type SortKey = 'score' | 'net_7d' | 'net_30d' | 'capital_efficiency' | 'rebalance_cost' | 'risk' | 'peer_stability' | 'htlc_failures' | 'rebalance_dependence'

type Props = {
  items: ChannelCapitalPlanItem[]
  stateCounts: Record<string, number>
  selectedChannelPoint: string
  loading: boolean
  format: ChannelRankingFormatters
  onSelect: (item: ChannelCapitalPlanItem) => void
}

const rankingClass = (state: string) => {
  switch (state) {
    case 'expand': return 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200'
    case 'maintain': return 'border-sky-300/30 bg-sky-500/15 text-sky-100'
    case 'close': return 'border-rose-400/30 bg-rose-500/15 text-rose-100'
    default: return 'border-amber-300/30 bg-amber-500/15 text-amber-100'
  }
}

const actionClass = (action: string) => {
  switch (action) {
    case 'rotate': return 'text-rose-200'
    case 'refill': return 'text-cyan-200'
    case 'reprice': return 'text-violet-200'
    case 'expand': return 'text-emerald-200'
    case 'protected': return 'text-sky-200'
    case 'parked': return 'text-amber-200'
    default: return 'text-fog/70'
  }
}

export default function ChannelRankingTable({ items, stateCounts, selectedChannelPoint, loading, format, onSelect }: Props) {
  const { t } = useTranslation()
  const [filter, setFilter] = useState<RankingFilter>('all')
  const [search, setSearch] = useState('')
  const [activity, setActivity] = useState<'all' | 'active' | 'inactive'>('all')
  const [visibility, setVisibility] = useState<'all' | 'public' | 'private'>('all')
  const [recommendation, setRecommendation] = useState('all')
  const [sortBy, setSortBy] = useState<SortKey>('score')
  const [advanced, setAdvanced] = useState(false)

  const recommendationOptions = useMemo(() => {
    const options = new Set<string>()
    items.forEach(({ channel }) => (channel.recommendations || []).forEach((entry) => entry.code && options.add(entry.code)))
    return Array.from(options)
  }, [items])

  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase()
    const risk = (item: ChannelRankingItem) => {
      let value = item.active ? 0 : 30
      value += Math.min(20, Number(item.pending_htlc_count || 0) * 4)
      if (item.profit_fee_7d_sat < 0) value += 25
      if (item.rebal_fee_7d_sat > item.forward_fee_7d_sat) value += 15
      if (item.local_balance_pct < 10 || item.local_balance_pct > 90) value += 10
      if (item.trend_direction === 'worsening') value += 10
      return value
    }
    const result = items.filter(({ channel }) => {
      if (filter !== 'all' && channel.state !== filter) return false
      if (activity === 'active' && !channel.active) return false
      if (activity === 'inactive' && channel.active) return false
      if (visibility === 'public' && channel.private) return false
      if (visibility === 'private' && !channel.private) return false
      if (recommendation !== 'all' && !(channel.recommendations || []).some((entry) => entry.code === recommendation)) return false
      if (!query) return true
      return [channel.peer_alias, channel.peer_pubkey, channel.channel_point].some((value) => String(value || '').toLowerCase().includes(query))
    })
    result.sort((left, right) => {
      const a = left.channel
      const b = right.channel
      switch (sortBy) {
        case 'net_7d': return b.profit_fee_7d_sat - a.profit_fee_7d_sat || b.score - a.score
        case 'net_30d': return b.profit_fee_30d_sat - a.profit_fee_30d_sat || b.score - a.score
        case 'capital_efficiency': return (b.capacity_sat ? b.profit_fee_7d_sat / b.capacity_sat : 0) - (a.capacity_sat ? a.profit_fee_7d_sat / a.capacity_sat : 0)
        case 'rebalance_cost': return b.rebal_fee_7d_sat - a.rebal_fee_7d_sat || b.score - a.score
        case 'peer_stability': return b.peer_stability_score_30d - a.peer_stability_score_30d || b.score - a.score
        case 'htlc_failures': return Number(b.htlc_failures_30d || 0) - Number(a.htlc_failures_30d || 0) || b.score - a.score
        case 'rebalance_dependence': return b.rebalance_dependence_score - a.rebalance_dependence_score || b.score - a.score
        case 'risk': return risk(b) - risk(a) || b.score - a.score
        default: return b.score - a.score || b.profit_fee_7d_sat - a.profit_fee_7d_sat
      }
    })
    return result
  }, [activity, filter, items, recommendation, search, sortBy, visibility])

  const filters: RankingFilter[] = ['all', 'expand', 'maintain', 'monitor', 'close']

  return (
    <section className="rounded-3xl border border-white/10 bg-ink/60 p-4 sm:p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap gap-2">
          {filters.map((key) => (
            <button key={key} type="button" className={filter === key ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter(key)}>
              {key === 'all' ? t('common.all') : t(`channelRanking.states.${key}` as any)}: {format.number.format(key === 'all' ? items.length : Number(stateCounts[key] || 0))}
            </button>
          ))}
        </div>
        <div className="text-xs text-fog/55">{t('channelRanking.listCount', { count: filtered.length })}</div>
      </div>

      <div className="mt-4 grid gap-3 md:grid-cols-[minmax(0,1fr)_minmax(12rem,0.35fr)_auto]">
        <input className="input-field" value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t('channelRanking.searchPlaceholder')} />
        <select className="input-field" value={sortBy} onChange={(event) => setSortBy(event.target.value as SortKey)} aria-label={t('channelRanking.sortLabel')}>
          <option value="score">{t('channelRanking.sorts.score')}</option>
          <option value="net_7d">{t('channelRanking.sorts.net7d')}</option>
          <option value="net_30d">{t('channelRanking.sorts.net30d')}</option>
          <option value="capital_efficiency">{t('channelRanking.sorts.capitalEfficiency')}</option>
          <option value="rebalance_cost">{t('channelRanking.sorts.rebalanceCost')}</option>
          <option value="peer_stability">{t('channelRanking.sorts.peerStability')}</option>
          <option value="htlc_failures">{t('channelRanking.sorts.htlcFailures')}</option>
          <option value="rebalance_dependence">{t('channelRanking.sorts.rebalanceDependence')}</option>
          <option value="risk">{t('channelRanking.sorts.risk')}</option>
        </select>
        <button type="button" className="btn-secondary" onClick={() => setAdvanced((current) => !current)}>
          {advanced ? t('channelRanking.filters.hideAdvanced') : t('channelRanking.filters.showAdvanced')}
        </button>
      </div>

      {advanced && (
        <div className="mt-3 grid gap-3 rounded-2xl border border-white/10 bg-white/[0.025] p-3 md:grid-cols-3">
          <label className="text-xs text-fog/65">
            {t('channelRanking.activityFilterLabel')}
            <select className="input-field mt-1" value={activity} onChange={(event) => setActivity(event.target.value as any)}>
              <option value="all">{t('common.all')}</option><option value="active">{t('common.active')}</option><option value="inactive">{t('common.inactive')}</option>
            </select>
          </label>
          <label className="text-xs text-fog/65">
            {t('channelRanking.visibilityFilterLabel')}
            <select className="input-field mt-1" value={visibility} onChange={(event) => setVisibility(event.target.value as any)}>
              <option value="all">{t('common.all')}</option><option value="public">{t('channelRanking.publicOnly')}</option><option value="private">{t('channelRanking.privateOnly')}</option>
            </select>
          </label>
          <label className="text-xs text-fog/65">
            {t('channelRanking.recommendationFilterLabel')}
            <select className="input-field mt-1" value={recommendation} onChange={(event) => setRecommendation(event.target.value)}>
              <option value="all">{t('common.all')}</option>
              {recommendationOptions.map((code) => <option key={code} value={code}>{t(`channelRanking.recommendations.${code}` as any, { defaultValue: code })}</option>)}
            </select>
          </label>
        </div>
      )}

      {loading ? (
        <p className="mt-5 text-sm text-fog/70">{t('channelRanking.loading')}</p>
      ) : filtered.length === 0 ? (
        <p className="mt-5 text-sm text-fog/70">{t('channelRanking.empty')}</p>
      ) : (
        <>
          <div className="mt-4 space-y-3 md:hidden">
            {filtered.map((planItem) => {
              const channel = planItem.channel
              return (
                <button key={channel.channel_point} type="button" onClick={() => onSelect(planItem)} className={`w-full rounded-2xl border p-4 text-left ${selectedChannelPoint === channel.channel_point ? 'border-sky-300/60 bg-sky-500/10' : 'border-white/10 bg-white/[0.03]'}`}>
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0 truncate font-medium">{channel.peer_alias || channel.peer_pubkey || channel.channel_point}</div>
                    <span className={`shrink-0 rounded-full border px-2 py-0.5 text-[11px] ${rankingClass(channel.state)}`}>{t(`channelRanking.states.${channel.state}` as any)}</span>
                  </div>
                  <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-fog/65">
                    <span className={actionClass(planItem.action)}>{t(`channelRanking.plan.actions.${planItem.action}` as any)}</span>
                    <span>{t('channelRanking.scoreShort', { value: channel.score })}</span>
                    <span>{t('channelRanking.localBalancePct', { value: format.pct(channel.local_balance_pct) })}</span>
                    <span>{t('channelRanking.netFees7d', { value: format.sats(channel.profit_fee_7d_sat) })}</span>
                  </div>
                </button>
              )
            })}
          </div>

          <div className="mt-4 hidden max-h-[66vh] overflow-auto rounded-2xl border border-white/10 md:block">
            <table className="w-full min-w-[70rem] text-left text-sm">
              <thead className="sticky top-0 z-10 bg-[#10151d] text-[11px] uppercase tracking-wide text-fog/50">
                <tr>
                  <th className="p-3">{t('channelRanking.table.channel')}</th>
                  <th className="p-3">{t('channelRanking.table.decision')}</th>
                  <th className="p-3">{t('channelRanking.table.liquidity')}</th>
                  <th className="p-3">{t('channelRanking.table.economics')}</th>
                  <th className="p-3">{t('channelRanking.table.flow')}</th>
                  <th className="p-3 text-right">{t('channelRanking.table.score')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/10">
                {filtered.map((planItem) => {
                  const channel = planItem.channel
                  return (
                    <tr key={channel.channel_point} onClick={() => onSelect(planItem)} className={`cursor-pointer transition hover:bg-white/[0.04] ${selectedChannelPoint === channel.channel_point ? 'bg-sky-500/10' : ''}`}>
                      <td className="p-3">
                        <div className="max-w-[17rem] truncate font-medium text-fog">{channel.peer_alias || channel.peer_pubkey || channel.channel_point}</div>
                        <div className="mt-1 text-[11px] text-fog/45">{channel.active ? t('common.active') : t('common.inactive')} · {channel.private ? t('channelRanking.privateOnly') : t('channelRanking.publicOnly')}</div>
                      </td>
                      <td className="p-3">
                        <div className="flex flex-wrap gap-1.5">
                          <span className={`rounded-full border px-2 py-0.5 text-[11px] ${rankingClass(channel.state)}`}>{t(`channelRanking.states.${channel.state}` as any)}</span>
                          {channel.automation_mode === 'parked' && <span className="rounded-full border border-amber-300/30 bg-amber-500/10 px-2 py-0.5 text-[11px] text-amber-100">{t('channelRanking.automationModes.parked')}</span>}
                          {planItem.magma_commitment && <span className="rounded-full border border-sky-300/30 bg-sky-500/10 px-2 py-0.5 text-[11px] text-sky-100">Magma</span>}
                        </div>
                        <div className={`mt-1 text-xs ${actionClass(planItem.action)}`}>{t(`channelRanking.plan.actions.${planItem.action}` as any)}</div>
                      </td>
                      <td className="p-3 text-xs text-fog/70">
                        <div>{t('channelRanking.localBalancePct', { value: format.pct(channel.local_balance_pct) })}</div>
                        <div className="mt-1">{channel.liquidity_state ? t(`liquidityStates.${channel.liquidity_state === 'offer-ready' ? 'offerReady' : channel.liquidity_state === 'extreme-drained' ? 'extremeDrained' : channel.liquidity_state}` as any) : t('common.unknown')}</div>
                      </td>
                      <td className="p-3 text-xs text-fog/70">
                        <div>{t('channelRanking.netFees7d', { value: format.sats(channel.profit_fee_7d_sat) })}</div>
                        <div className="mt-1">{t('channelRanking.netFees30d', { value: format.sats(channel.profit_fee_30d_sat) })}</div>
                      </td>
                      <td className="p-3 text-xs text-fog/70">
                        <div>{t('channelRanking.forwardIn7dCompact', { value: format.flow(channel.forward_in_count_7d, channel.forward_in_amount_sat_7d) })}</div>
                        <div className="mt-1">{t('channelRanking.forwardOut7dCompact', { value: format.flow(channel.forward_out_count_7d, channel.forward_out_amount_sat_7d) })}</div>
                      </td>
                      <td className="p-3 text-right">
                        <div className="font-semibold text-fog">{format.number.format(channel.score)}</div>
                        <div className="mt-1 text-[11px] text-fog/50">{t(`channelRanking.trends.${channel.trend_direction || 'stable'}` as any)} {channel.trend_delta ? `(${channel.trend_delta > 0 ? '+' : ''}${channel.trend_delta})` : ''}</div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  )
}
