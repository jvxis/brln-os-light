import type { TFunction } from 'i18next'

import type { RebalancePairStat } from './types'

type PairStatsPanelProps = {
  pairs: RebalancePairStat[]
  loading: boolean
  failed: boolean
  t: TFunction
  formatTimestamp: (value?: string) => string
  formatRoi: (value: number) => string
  formatSats: (value: number) => string
}

export function PairStatsPanel({
  pairs,
  loading,
  failed,
  t,
  formatTimestamp,
  formatRoi,
  formatSats
}: PairStatsPanelProps) {
  return (
    <div className="mt-3 border-t border-white/10 pt-3 text-xs text-fog/60">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <span className="font-semibold text-fog/80" title={t('rebalanceCenter.channelsHints.pairStats')}>
          {t('rebalanceCenter.channels.pairStatsTitle')}
        </span>
        <span>{pairs.length}</span>
      </div>
      {loading && <div>{t('rebalanceCenter.channels.pairStatsLoading')}</div>}
      {failed && <div className="text-rose-200">{t('rebalanceCenter.channels.pairStatsError')}</div>}
      {!loading && !failed && pairs.length === 0 && (
        <div>{t('rebalanceCenter.channels.pairStatsEmpty')}</div>
      )}
      {!loading && !failed && pairs.length > 0 && (
        <div className="space-y-2">
          {pairs.map((pair) => (
            <div
              key={`${pair.source_channel_id}-${pair.target_channel_id}`}
              className="grid gap-2 border-t border-white/5 pt-2 md:grid-cols-[minmax(0,1.3fr)_minmax(0,0.9fr)_minmax(0,0.9fr)_minmax(0,0.8fr)_minmax(0,1fr)]"
            >
              <div className="min-w-0">
                <div className="truncate text-fog/80">{pair.source_peer_alias || pair.source_channel_id}</div>
                <div className="truncate text-[11px] text-fog/40">{pair.source_channel_point || pair.source_channel_id}</div>
              </div>
              <div>
                <div className="text-fog/40">{t('rebalanceCenter.channels.pairStatsLastSuccess')}</div>
                <div>{formatTimestamp(pair.last_success_at)}</div>
              </div>
              <div>
                <div className="text-fog/40">{t('rebalanceCenter.channels.pairStatsLastFail')}</div>
                <div>{formatTimestamp(pair.last_fail_at)}</div>
                {pair.last_fail_reason && <div className="truncate text-[11px] text-fog/40">{pair.last_fail_reason}</div>}
              </div>
              <div>
                <div className="text-fog/40">{t('rebalanceCenter.channels.pairStatsFailScore')}</div>
                <div>{formatRoi(pair.permanent_fail_score || 0)} / {pair.fail_count || 0}</div>
              </div>
              <div>
                <div className="text-fog/40">{t('rebalanceCenter.channels.pairStatsSuccess')}</div>
                <div>{formatSats(pair.success_amount_sat || 0)}</div>
                <div className="text-[11px] text-fog/40">{pair.success_fee_ppm || 0} ppm</div>
                {pair.last_success_route_hops && pair.last_success_route_hops.length > 0 && (
                  <div className="truncate text-[11px] text-fog/40" title={pair.last_success_route_hops.join(' -> ')}>
                    {t('rebalanceCenter.channels.pairStatsRoute')}: {pair.last_success_route_hops.length}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
