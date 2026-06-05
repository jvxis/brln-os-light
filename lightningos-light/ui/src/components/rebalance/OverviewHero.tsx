import type { TFunction } from 'i18next'
import HeroTile, { type HeroTone } from './HeroTile'
import HealthGauge from './HealthGauge'
import type { RebalanceConfig, RebalanceOverview } from './types'

type OverviewHeroProps = {
  overview: RebalanceOverview
  config?: RebalanceConfig | null
  t: TFunction
  formatSats: (value: number) => string
  formatPct: (value: number, digits?: number) => string
  formatTimestamp: (value?: string) => string
  onResetMC?: () => void
  resetMCDisabled?: boolean
  classicOpen?: boolean
  onToggleClassic?: () => void
}

function pickRoiTone(roi: number): HeroTone {
  if (roi >= 1.0) return 'ok'
  if (roi >= 0.85) return 'warn'
  return 'danger'
}

// Per-JOB success tone, aligned with the Effectiveness (7d) gauge (target 25%).
// A job counts as success when it moved liquidity (succeeded/partial). This is
// the headline rate; the per-attempt route-hit rate is shown un-toned below.
function pickSuccessTone(rate: number): HeroTone {
  if (rate >= 0.25) return 'ok'
  if (rate >= 0.15) return 'warn'
  return 'danger'
}

function pickAutopilotTone(enabled: boolean, mode?: string): HeroTone {
  if (!enabled) return 'muted'
  if (mode === 'sovereign_live') return 'ok'
  if (mode === 'sovereign_shadow') return 'warn'
  return 'info'
}

function pickBudgetTone(unlimited: boolean, spentPct: number): HeroTone {
  if (unlimited) return 'ok'
  if (spentPct < 70) return 'ok'
  if (spentPct < 90) return 'warn'
  return 'danger'
}

export default function OverviewHero({
  overview,
  config,
  t,
  formatSats,
  formatPct,
  formatTimestamp,
  onResetMC,
  resetMCDisabled,
  classicOpen,
  onToggleClassic,
}: OverviewHeroProps) {

  // Autopilot tile
  const autoEnabled = !!overview.auto_enabled
  const scheduler = overview.scheduler_mode || config?.scheduler_mode || 'rules_auto'
  const autopilotTone = pickAutopilotTone(autoEnabled, scheduler)
  const candidates = overview.sovereign_candidates ?? 0
  const selected = overview.sovereign_selected ?? 0
  const scanIntervalSec = config?.scan_interval_sec ?? 900
  const scanMin = Math.round(scanIntervalSec / 60)

  // ROI tile
  const roi = overview.roi_7d ?? 0
  const roiTone = pickRoiTone(roi)
  const roiPct = Math.min(150, roi * 100) // gauge max 150% so 1.5x = full
  const roiGaugePct = (roiPct / 150) * 100 // normalize to 0-100 for gauge
  const roiMarkerPct = (100 / 150) * 100 // marker at 1.0×

  // Success 24h — headline is the per-JOB rate (jobs that moved liquidity ÷
  // jobs executed). The per-attempt route-hit rate stays as a secondary,
  // un-toned line since one job fans out into many route probes.
  const jobs24h = overview.jobs_24h ?? 0
  const successJobs24h = overview.success_jobs_24h ?? 0
  const jobSuccessRate = overview.job_success_rate_24h ?? 0
  const successTone = pickSuccessTone(jobSuccessRate)
  const attempts24h = overview.attempts_24h ?? 0
  const successes24h = overview.success_attempts_24h ?? 0
  const attemptRate = overview.attempt_success_rate_24h ?? 0
  const fpHitRate = overview.fast_path_hit_rate_24h ?? 0

  // Budget
  const unlimited = !!overview.budget_unlimited
  const dailyBudget = overview.daily_budget_sat ?? 0
  const spent = overview.daily_spent_sat ?? 0
  const remaining = overview.remaining_total_sat ?? Math.max(0, dailyBudget - spent)
  const spentPct = dailyBudget > 0 ? (spent / dailyBudget) * 100 : 0
  const budgetTone = pickBudgetTone(unlimited, spentPct)

  // Health gauges
  const effectiveness = (overview.effectiveness_7d ?? 0) * 100
  const sellThrough = (overview.sovereign_sellthrough_7d ?? 0) * 100
  const sellThroughSlow = (overview.sovereign_sellthrough_slow_7d ?? 0) * 100
  const sellThroughWindowH = overview.sovereign_sellthrough_window_hours ?? 72
  const sellThroughSlowWindowH = overview.sovereign_sellthrough_slow_window_hours ?? 168
  const paybackRebal = (overview.payback_progress_rebalanced ?? 0) * 100

  // Node calibration (Phase 1) — shown in the Autopilot card like the Fee Center.
  const calib = overview.node_calibration
  const hasCalib = !!calib && (calib.channel_count ?? 0) > 0 && calib.node_class !== 'unknown'

  const detailLabels = {
    showDetailsLabel: t('rebalanceCenter.heroes.showDetails'),
    hideDetailsLabel: t('rebalanceCenter.heroes.hideDetails'),
  }

  return (
    <div className="space-y-4">
      {/* HERO TILES */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {/* Autopilot */}
        <HeroTile
          {...detailLabels}
          label={t('rebalanceCenter.heroes.autopilot')}
          value={autoEnabled ? t('common.enabled') : t('common.disabled')}
          tone={autopilotTone}
          badge={t(`rebalanceCenter.settings.schedulerModeOptions.${scheduler}`)}
          context={
            <>
              <p>{t('rebalanceCenter.heroes.scanInterval', { value: scanMin })}</p>
              <p>{t('rebalanceCenter.heroes.lastScan', {
                value: overview.last_scan_at ? formatTimestamp(overview.last_scan_at) : '—'
              })}</p>
              {candidates > 0 && (
                <p>{t('rebalanceCenter.heroes.candidatesSelected', { c: candidates, s: selected })}</p>
              )}
              {hasCalib && (
                <p className="text-fog/55">{t('rebalanceCenter.heroes.nodeCalib', {
                  node: t(`rebalanceCenter.heroes.nodeClass.${calib!.node_class}`, calib!.node_class),
                  liquidity: t(`rebalanceCenter.heroes.liquidityClass.${calib!.liquidity_class}`, calib!.liquidity_class)
                })}</p>
              )}
            </>
          }
          details={
            <>
              {hasCalib && (
                <>
                  <p>{t('rebalanceCenter.heroes.calibChannels', { count: calib!.channel_count })}</p>
                  <p>{t('rebalanceCenter.heroes.calibCapacity', {
                    cap: formatSats(calib!.total_capacity_sat),
                    local: formatSats(calib!.local_capacity_sat),
                    ratio: formatPct((calib!.local_ratio ?? 0) * 100)
                  })}</p>
                  <p>{t('rebalanceCenter.heroes.calibInbound', { value: formatSats(calib!.inbound_capacity_sat) })}</p>
                </>
              )}
              <p>{t('rebalanceCenter.heroes.lastDecision', {
                value: overview.sovereign_last_decision_at ? formatTimestamp(overview.sovereign_last_decision_at) : '—'
              })}</p>
              {(overview.sovereign_expected_profit_sat ?? 0) > 0 && (
                <p>{t('rebalanceCenter.heroes.expectedProfit', {
                  value: formatSats(overview.sovereign_expected_profit_sat ?? 0)
                })}</p>
              )}
              {overview.last_scan_status && (
                <p>{t('rebalanceCenter.heroes.scanStatus', { status: overview.last_scan_status })}</p>
              )}
              {(overview.mc_reset_count ?? 0) > 0 && (
                <p>{t('rebalanceCenter.heroes.mcResets', { count: overview.mc_reset_count })}</p>
              )}
            </>
          }
        />

        {/* ROI 7D */}
        <HeroTile
          {...detailLabels}
          markerLabel={t('rebalanceCenter.heroes.roiBreakeven')}
          label={t('rebalanceCenter.heroes.roi7d')}
          value={`${roi.toFixed(2)}×`}
          tone={roiTone}
          badge={roi >= 1.0 ? t('rebalanceCenter.heroes.profitable') : t('rebalanceCenter.heroes.belowBreakeven')}
          gaugePct={roiGaugePct}
          gaugeMarker={roiMarkerPct}
          context={
            <>
              {(overview.sovereign_rebalance_cost_7d_ppm ?? 0) > 0 && (
                <p>{t('rebalanceCenter.heroes.rebalCost7dPpm', { value: overview.sovereign_rebalance_cost_7d_ppm })}</p>
              )}
              {(overview.sovereign_forward_fee_7d_ppm ?? 0) > 0 && (
                <p>{t('rebalanceCenter.heroes.forwardFee7dPpm', { value: overview.sovereign_forward_fee_7d_ppm })}</p>
              )}
            </>
          }
          details={
            <>
              <p>{t('rebalanceCenter.heroes.rebalAmountMoved7d', {
                value: formatSats(overview.sovereign_rebalance_amount_7d_sat ?? 0)
              })}</p>
              <p>{t('rebalanceCenter.heroes.forwardAmountSold7d', {
                value: formatSats(overview.sovereign_forward_amount_7d_sat ?? 0)
              })}</p>
              {typeof overview.sovereign_realized_net_7d_sat === 'number' && (
                <p className="text-fog/45">{t('rebalanceCenter.heroes.realized7d', { value: formatSats(overview.sovereign_realized_net_7d_sat ?? 0) })}</p>
              )}
              <p>{t('rebalanceCenter.heroes.effectiveness7d', {
                value: formatPct(effectiveness, 1)
              })}</p>
              <p>{t('rebalanceCenter.heroes.paybackProgressAll', {
                value: formatPct((overview.payback_progress ?? 0) * 100, 1)
              })}</p>
            </>
          }
        />

        {/* Success 24h — headline per-JOB, route-attempt rate as un-toned subline */}
        <HeroTile
          {...detailLabels}
          markerLabel={t('rebalanceCenter.heroes.gaugeTarget', { value: 25 })}
          label={t('rebalanceCenter.heroes.success24h')}
          value={formatPct(jobSuccessRate * 100, 1)}
          tone={successTone}
          badge={t('rebalanceCenter.heroes.jobs', { value: jobs24h })}
          gaugePct={Math.min(100, jobSuccessRate * 100)}
          gaugeMarker={25}
          context={
            <>
              <p>{t('rebalanceCenter.heroes.successJobs', { s: successJobs24h, n: jobs24h })}</p>
              <p className="text-fog/45">{t('rebalanceCenter.heroes.successCount', { s: successes24h, n: attempts24h, rate: formatPct(attemptRate * 100, 1) })}</p>
              {(overview.fast_path_attempts_24h ?? 0) > 0 && (
                <p>{t('rebalanceCenter.heroes.fastPathHit', {
                  value: formatPct(fpHitRate * 100, 1),
                  count: overview.fast_path_successes_24h
                })}</p>
              )}
              <p className="text-fog/50">{t('rebalanceCenter.heroes.liveCost', { value: formatSats(overview.live_cost_sat ?? 0) })}</p>
            </>
          }
          details={
            <>
              {(overview.fast_path_attempts_24h ?? 0) > 0 && (
                <>
                  <p>{t('rebalanceCenter.heroes.fpAttempts', {
                    value: overview.fast_path_attempts_24h,
                    s: overview.fast_path_successes_24h,
                    f: overview.fast_path_failures_24h
                  })}</p>
                  <p>{t('rebalanceCenter.heroes.fpFallthroughs', { value: overview.fast_path_fallthroughs_24h ?? 0 })}</p>
                  {(overview.fast_path_duration_p50_ms ?? 0) > 0 && (
                    <p>{t('rebalanceCenter.heroes.fpDuration', {
                      p50: Math.round((overview.fast_path_duration_p50_ms ?? 0) / 1000),
                      p95: Math.round((overview.fast_path_duration_p95_ms ?? 0) / 1000)
                    })}</p>
                  )}
                </>
              )}
              {(overview.success_amount_24h_sat ?? 0) > 0 && (
                <p>{t('rebalanceCenter.heroes.successAmount', { value: formatSats(overview.success_amount_24h_sat ?? 0) })}</p>
              )}
              {(overview.success_below_min_attempts_24h ?? 0) > 0 && (
                <p>{t('rebalanceCenter.heroes.belowMin', {
                  count: overview.success_below_min_attempts_24h,
                  rate: formatPct((overview.success_below_min_rate_24h ?? 0) * 100, 1)
                })}</p>
              )}
            </>
          }
        />

        {/* Daily Budget */}
        <HeroTile
          {...detailLabels}
          label={t('rebalanceCenter.heroes.dailyBudget')}
          value={unlimited ? t('rebalanceCenter.heroes.unlimited') : formatSats(dailyBudget)}
          tone={budgetTone}
          badge={config?.budget_mode || 'hybrid_revenue'}
          gaugePct={unlimited ? 0 : Math.min(100, spentPct)}
          context={
            <>
              <p>{t('rebalanceCenter.heroes.spent', { value: formatSats(spent) })}</p>
              {!unlimited && (
                <p className={remaining < dailyBudget * 0.1 ? 'text-rose-300' : 'text-fog/65'}>
                  {t('rebalanceCenter.heroes.remaining', { value: formatSats(remaining) })}
                </p>
              )}
              {unlimited && (
                <p className="text-emerald-300/80">{t('rebalanceCenter.heroes.capNotEnforced')}</p>
              )}
            </>
          }
          details={
            <>
              {(overview.daily_budget_base_sat ?? 0) > 0 && (
                <p>{t('rebalanceCenter.overview.dailyBudgetBase', { value: formatSats(overview.daily_budget_base_sat ?? 0) })}</p>
              )}
              {(overview.daily_budget_short_term_sat ?? 0) > 0 && (
                <p>{t('rebalanceCenter.overview.dailyBudgetShortTerm', { value: formatSats(overview.daily_budget_short_term_sat ?? 0) })}</p>
              )}
              <p>{t('rebalanceCenter.heroes.spentAuto', { value: formatSats(overview.daily_spent_auto_sat) })}</p>
              <p>{t('rebalanceCenter.heroes.spentManual', { value: formatSats(overview.daily_spent_manual_sat) })}</p>
              {(overview.manual_reserve_sat ?? 0) > 0 && (
                <p>{t('rebalanceCenter.heroes.manualReserve', {
                  value: formatSats(overview.manual_reserve_sat ?? 0),
                  remaining: formatSats(overview.manual_reserve_remaining_sat ?? 0)
                })}</p>
              )}
            </>
          }
        />
      </div>

      {/* HEALTH GAUGES */}
      <article className="rounded-3xl border border-white/10 bg-white/[0.02] p-4 shadow-panel">
        <p className="mb-3 text-[11px] uppercase tracking-[0.24em] text-fog/45">
          {t('rebalanceCenter.heroes.healthSignals')}
        </p>
        <div className="grid items-start gap-x-6 gap-y-3 sm:grid-cols-2 lg:grid-cols-4">
          <HealthGauge
            label={t('rebalanceCenter.heroes.gaugeEffectiveness')}
            value={effectiveness}
            target={25}
            targetLabel={t('rebalanceCenter.heroes.gaugeTarget', { value: 25 })}
            valueLabel={formatPct(effectiveness, 1)}
            tone={effectiveness >= 25 ? 'ok' : effectiveness >= 15 ? 'warn' : 'danger'}
            hint={t('rebalanceCenter.heroes.hintEffectiveness')}
          />
          <HealthGauge
            label={t('rebalanceCenter.heroes.gaugeFastPath')}
            value={fpHitRate * 100}
            target={20}
            targetLabel={t('rebalanceCenter.heroes.gaugeTarget', { value: 20 })}
            valueLabel={formatPct(fpHitRate * 100, 1)}
            tone={fpHitRate * 100 >= 20 ? 'ok' : fpHitRate * 100 >= 10 ? 'warn' : 'danger'}
            hint={t('rebalanceCenter.heroes.hintFastPath')}
          />
          <div className="space-y-3">
            <HealthGauge
              label={t('rebalanceCenter.heroes.gaugeSellThroughFast', { hours: sellThroughWindowH })}
              value={sellThrough}
              target={70}
              targetLabel={t('rebalanceCenter.heroes.gaugeTarget', { value: 70 })}
              valueLabel={formatPct(sellThrough, 1)}
              tone={sellThrough >= 70 ? 'ok' : sellThrough >= 50 ? 'warn' : 'danger'}
              hint={t('rebalanceCenter.heroes.hintSellThroughFast')}
            />
            <HealthGauge
              label={t('rebalanceCenter.heroes.gaugeSellThroughSlow', { hours: sellThroughSlowWindowH })}
              value={sellThroughSlow}
              target={70}
              targetLabel={t('rebalanceCenter.heroes.gaugeTarget', { value: 70 })}
              valueLabel={formatPct(sellThroughSlow, 1)}
              tone={sellThroughSlow >= 70 ? 'ok' : sellThroughSlow >= 50 ? 'warn' : 'danger'}
              hint={t('rebalanceCenter.heroes.hintSellThroughSlow')}
            />
          </div>
          <HealthGauge
            label={t('rebalanceCenter.heroes.gaugePaybackRebal')}
            value={paybackRebal}
            target={100}
            targetLabel={t('rebalanceCenter.heroes.gaugeTarget', { value: 100 })}
            valueLabel={formatPct(paybackRebal, 1)}
            tone={paybackRebal >= 80 ? 'ok' : paybackRebal >= 50 ? 'warn' : 'danger'}
            hint={t('rebalanceCenter.heroes.hintPaybackRebal')}
          />
        </div>
      </article>

      {/* OPS STRIP */}
      <article className="rounded-3xl border border-white/10 bg-white/[0.02] p-3 shadow-panel">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-xs text-fog/70">
          <span>
            <span className="text-fog/45">{t('rebalanceCenter.heroes.opsLastScan')}</span>{' '}
            <span className="text-fog">{overview.last_scan_at ? formatTimestamp(overview.last_scan_at) : '—'}</span>
          </span>
          <span>
            <span className="text-fog/45">{t('rebalanceCenter.heroes.opsEligibleSources')}</span>{' '}
            <span className="text-fog">{overview.eligible_sources ?? 0}</span>
          </span>
          <span>
            <span className="text-fog/45">{t('rebalanceCenter.heroes.opsTargetsNeeding')}</span>{' '}
            <span className="text-fog">{overview.targets_needing ?? 0}</span>
          </span>
          <span>
            <span className="text-fog/45">{t('rebalanceCenter.heroes.opsMcResets')}</span>{' '}
            <span className="text-fog">{overview.mc_reset_count ?? 0}</span>
          </span>
          {(overview.top_failure_reasons_30m?.length ?? 0) > 0 && (
            <span>
              <span className="text-fog/45">{t('rebalanceCenter.heroes.opsFailures30m')}</span>{' '}
              <span className="text-fog">
                {(overview.top_failure_reasons_30m ?? []).reduce((acc, r) => acc + (r.count ?? 0), 0)}
              </span>
            </span>
          )}
          {onResetMC && (
            <button
              type="button"
              onClick={onResetMC}
              disabled={resetMCDisabled}
              className="ml-auto rounded-full border border-white/15 px-3 py-1 text-[11px] text-fog/80 hover:bg-white/5 disabled:opacity-40"
            >
              {t('rebalanceCenter.heroes.opsResetMC')}
            </button>
          )}
          {onToggleClassic && (
            <button
              type="button"
              onClick={onToggleClassic}
              className="text-[11px] uppercase tracking-wide text-fog/55 underline-offset-2 hover:underline"
            >
              {classicOpen ? t('rebalanceCenter.heroes.hideClassic') : t('rebalanceCenter.heroes.showClassic')}
            </button>
          )}
        </div>
      </article>
    </div>
  )
}
