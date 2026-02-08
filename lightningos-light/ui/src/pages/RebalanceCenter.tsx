
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getRebalanceChannels,
  getRebalanceConfig,
  getRebalanceHistory,
  getRebalanceOverview,
  getRebalanceQueue,
  runRebalance,
  updateRebalanceChannelAuto,
  updateRebalanceChannelTarget,
  updateRebalanceConfig,
  updateRebalanceExclude
} from '../api'
import { getLocale } from '../i18n'

type RebalanceConfig = {
  auto_enabled: boolean
  scan_interval_sec: number
  deadband_pct: number
  source_min_local_pct: number
  econ_ratio: number
  roi_min: number
  daily_budget_pct: number
  max_concurrent: number
  min_amount_sat: number
  max_amount_sat: number
  fee_ladder_steps: number
  amount_probe_steps: number
  amount_probe_adaptive: boolean
  attempt_timeout_sec: number
  rebalance_timeout_sec: number
  payback_mode_flags: number
  unlock_days: number
  critical_release_pct: number
  critical_min_sources: number
  critical_min_available_sats: number
  critical_cycles: number
}

type RebalanceOverview = {
  auto_enabled: boolean
  last_scan_at?: string
  last_scan_status?: string
  last_scan_top_score_sat?: number
  last_scan_profit_skipped?: number
  eligible_sources?: number
  targets_needing?: number
  daily_budget_sat: number
  daily_spent_sat: number
  daily_spent_auto_sat: number
  daily_spent_manual_sat: number
  live_cost_sat: number
  effectiveness_7d: number
  roi_7d: number
}

type RebalanceChannel = {
  channel_id: number
  channel_point: string
  peer_alias: string
  remote_pubkey: string
  active: boolean
  private: boolean
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
  local_pct: number
  remote_pct: number
  outgoing_fee_ppm: number
  peer_fee_rate_ppm: number
  spread_ppm: number
  target_outbound_pct: number
  target_amount_sat: number
  auto_enabled: boolean
  eligible_as_target: boolean
  eligible_as_source: boolean
  protected_liquidity_sat: number
  payback_progress: number
  max_source_sat: number
  revenue_7d_sat: number
  roi_estimate: number
  roi_estimate_valid?: boolean
  excluded_as_source: boolean
}

type RebalanceJob = {
  id: number
  created_at: string
  completed_at?: string
  source: string
  status: string
  reason?: string
  target_channel_id: number
  target_channel_point: string
  target_peer_alias?: string
  target_outbound_pct: number
  target_amount_sat: number
}

type RebalanceAttempt = {
  id: number
  job_id: number
  attempt_index: number
  source_channel_id: number
  amount_sat: number
  fee_limit_ppm: number
  fee_paid_sat: number
  status: string
  payment_hash?: string
  fail_reason?: string
  started_at?: string
  finished_at?: string
}

const PAYBACK_MODE_PAYBACK = 1
const PAYBACK_MODE_TIME = 2
const PAYBACK_MODE_CRITICAL = 4

export default function RebalanceCenter() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const formatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const pctFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }), [locale])
  const roiFormatter = useMemo(
    () => new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }),
    [locale]
  )
  const dateTimeFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'medium' }),
    [locale]
  )

  const [config, setConfig] = useState<RebalanceConfig | null>(null)
  const [overview, setOverview] = useState<RebalanceOverview | null>(null)
  const [channels, setChannels] = useState<RebalanceChannel[]>([])
  const [queueJobs, setQueueJobs] = useState<RebalanceJob[]>([])
  const [queueAttempts, setQueueAttempts] = useState<RebalanceAttempt[]>([])
  const [historyJobs, setHistoryJobs] = useState<RebalanceJob[]>([])
  const [historyAttempts, setHistoryAttempts] = useState<RebalanceAttempt[]>([])
  const [serverConfig, setServerConfig] = useState<RebalanceConfig | null>(null)
  const [configDirty, setConfigDirty] = useState(false)
  const [historyFilter, setHistoryFilter] = useState<'all' | 'succeeded' | 'partial' | 'failed'>('all')
  const [loading, setLoading] = useState(true)
  const [status, setStatus] = useState('')
  const [saving, setSaving] = useState(false)
  const [autoOpen, setAutoOpen] = useState(false)
  const [editTargets, setEditTargets] = useState<Record<number, string>>({})
  const [channelSort, setChannelSort] = useState<'economic' | 'emptiest'>('economic')

  const formatSats = (value: number) => `${formatter.format(Math.round(value))} sats`
  const formatPct = (value: number) => `${pctFormatter.format(value)}%`
  const formatRoi = (value: number) => (value > 0 && value < 0.01 ? '<0.01' : roiFormatter.format(value))
  const formatTimestamp = (value?: string) => {
    if (!value) return '-'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return dateTimeFormatter.format(date)
  }
  const configSignature = (cfg?: RebalanceConfig | null) => {
    if (!cfg) return ''
    return JSON.stringify({
      auto_enabled: cfg.auto_enabled,
      scan_interval_sec: cfg.scan_interval_sec,
      deadband_pct: cfg.deadband_pct,
      source_min_local_pct: cfg.source_min_local_pct,
      econ_ratio: cfg.econ_ratio,
      roi_min: cfg.roi_min,
      daily_budget_pct: cfg.daily_budget_pct,
      max_concurrent: cfg.max_concurrent,
      min_amount_sat: cfg.min_amount_sat,
      max_amount_sat: cfg.max_amount_sat,
      fee_ladder_steps: cfg.fee_ladder_steps,
      amount_probe_steps: cfg.amount_probe_steps,
      amount_probe_adaptive: cfg.amount_probe_adaptive,
      attempt_timeout_sec: cfg.attempt_timeout_sec,
      rebalance_timeout_sec: cfg.rebalance_timeout_sec,
      payback_mode_flags: cfg.payback_mode_flags,
      unlock_days: cfg.unlock_days,
      critical_release_pct: cfg.critical_release_pct,
      critical_min_sources: cfg.critical_min_sources,
      critical_min_available_sats: cfg.critical_min_available_sats,
      critical_cycles: cfg.critical_cycles
    })
  }
  const calcMaxFeePpm = (outgoingFee: number, peerFee: number, econRatio: number) => {
    const spread = outgoingFee - peerFee
    if (spread <= 0) return 0
    const scaled = Math.round(outgoingFee * econRatio)
    if (scaled <= 0) return 0
    return Math.min(scaled, spread)
  }
  const estimateMaxCost = (amountSat: number, outgoingFee: number, econRatio: number, peerFee: number) => {
    const maxFee = calcMaxFeePpm(outgoingFee, peerFee, econRatio)
    if (maxFee <= 0 || amountSat <= 0) return 0
    return Math.floor((amountSat * maxFee) / 1_000_000)
  }
  const estimateTargetGain = (amountSat: number, revenue7d: number, localBalance: number, capacity: number) => {
    if (amountSat <= 0 || revenue7d <= 0) return 0
    let denom = localBalance > 0 ? localBalance : capacity
    if (denom <= 0) return 0
    if (amountSat > denom) denom = amountSat
    return Math.round(revenue7d * (amountSat / denom))
  }
  const computeChannelScore = (ch: RebalanceChannel, econRatio: number) => {
    const expectedGain = estimateTargetGain(ch.target_amount_sat, ch.revenue_7d_sat, ch.local_balance_sat, ch.capacity_sat)
    const estimatedCost = estimateMaxCost(ch.target_amount_sat, ch.outgoing_fee_ppm, econRatio, ch.peer_fee_rate_ppm)
    return {
      score: expectedGain - estimatedCost,
      expectedRoi: estimatedCost > 0 ? expectedGain / estimatedCost : -1,
      expectedGain,
      estimatedCost
    }
  }
  const sortedChannels = useMemo(() => {
    const active = channels.filter((ch) => ch.active)
    if (channelSort === 'emptiest' || !config) {
      return active.sort((a, b) => a.local_pct - b.local_pct)
    }
    return active.sort((a, b) => {
      const scoreA = computeChannelScore(a, config.econ_ratio)
      const scoreB = computeChannelScore(b, config.econ_ratio)
      if (scoreA.score !== scoreB.score) {
        return scoreB.score - scoreA.score
      }
      if (scoreA.expectedRoi !== scoreB.expectedRoi) {
        return scoreB.expectedRoi - scoreA.expectedRoi
      }
      if (a.target_amount_sat !== b.target_amount_sat) {
        return b.target_amount_sat - a.target_amount_sat
      }
      return a.local_pct - b.local_pct
    })
  }, [channels, config, channelSort])
  const buildAttemptTotals = (attempts: RebalanceAttempt[]) => {
    const totals = new Map<number, { amount: number; fee: number }>()
    attempts.forEach((attempt) => {
      if (attempt.status !== 'succeeded') return
      const current = totals.get(attempt.job_id) ?? { amount: 0, fee: 0 }
      current.amount += attempt.amount_sat || 0
      current.fee += attempt.fee_paid_sat || 0
      totals.set(attempt.job_id, current)
    })
    return totals
  }
  const historyTotals = useMemo(() => buildAttemptTotals(historyAttempts), [historyAttempts])
  const filteredHistory = useMemo(() => {
    if (historyFilter === 'all') return historyJobs
    if (historyFilter === 'failed') {
      return historyJobs.filter((job) => job.status === 'failed' || job.status === 'cancelled')
    }
    return historyJobs.filter((job) => job.status === historyFilter)
  }, [historyFilter, historyJobs])

  useEffect(() => {
    setConfigDirty(configSignature(config) !== configSignature(serverConfig))
  }, [config, serverConfig])
  useEffect(() => {
    if (typeof window === 'undefined') return
    const stored = window.localStorage.getItem('rebalance_center_channel_sort')
    if (stored === 'economic' || stored === 'emptiest') {
      setChannelSort(stored)
    }
  }, [])
  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('rebalance_center_channel_sort', channelSort)
  }, [channelSort])
  const parseRemaining = (reason?: string) => {
    if (!reason) return null
    const match = reason.match(/remaining\s+(\d+)/i)
    if (!match) return null
    return Number(match[1])
  }
  const statusClass = (status: string) => {
    switch (status) {
      case 'succeeded':
        return 'text-emerald-200'
      case 'partial':
        return 'text-amber-200'
      case 'failed':
        return 'text-rose-200'
      default:
        return 'text-fog/60'
    }
  }

  const loadAll = async () => {
    try {
      setLoading(true)
        const [cfg, ovw, ch, queue, hist] = await Promise.all([
          getRebalanceConfig(),
          getRebalanceOverview(),
          getRebalanceChannels(),
          getRebalanceQueue(),
          getRebalanceHistory()
        ])
        const nextConfig = cfg as RebalanceConfig
        const normalizedConfig = {
          ...nextConfig,
          amount_probe_steps: nextConfig.amount_probe_steps || 4,
          amount_probe_adaptive: nextConfig.amount_probe_adaptive ?? true,
          attempt_timeout_sec: nextConfig.attempt_timeout_sec || 20,
          rebalance_timeout_sec: nextConfig.rebalance_timeout_sec || 600
        }
        setServerConfig(normalizedConfig)
        const currentSig = configSignature(config)
        const nextSig = configSignature(normalizedConfig)
        if (!autoOpen || currentSig === '' || currentSig === nextSig) {
          setConfig(normalizedConfig)
        }
      setOverview(ovw as RebalanceOverview)
      const channelList = Array.isArray((ch as any)?.channels) ? (ch as any).channels : []
      setChannels(channelList)
      setQueueJobs(Array.isArray((queue as any)?.jobs) ? (queue as any).jobs : [])
      setQueueAttempts(Array.isArray((queue as any)?.attempts) ? (queue as any).attempts : [])
      setHistoryJobs(Array.isArray((hist as any)?.jobs) ? (hist as any).jobs : [])
      setHistoryAttempts(Array.isArray((hist as any)?.attempts) ? (hist as any).attempts : [])
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.loadFailed'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadAll()
  }, [])

  useEffect(() => {
    const timer = window.setInterval(() => {
      loadAll()
    }, 30000)
    return () => window.clearInterval(timer)
  }, [])

  const handleSaveConfig = async () => {
    if (!config) return
    setSaving(true)
    setStatus('')
    try {
        await updateRebalanceConfig({
          auto_enabled: config.auto_enabled,
          scan_interval_sec: config.scan_interval_sec,
          deadband_pct: config.deadband_pct,
          source_min_local_pct: config.source_min_local_pct,
          econ_ratio: config.econ_ratio,
          roi_min: config.roi_min,
          daily_budget_pct: config.daily_budget_pct,
          max_concurrent: config.max_concurrent,
          min_amount_sat: config.min_amount_sat,
          max_amount_sat: config.max_amount_sat,
          fee_ladder_steps: config.fee_ladder_steps,
          amount_probe_steps: config.amount_probe_steps,
          amount_probe_adaptive: config.amount_probe_adaptive,
          attempt_timeout_sec: config.attempt_timeout_sec,
          rebalance_timeout_sec: config.rebalance_timeout_sec,
          payback_mode_flags: config.payback_mode_flags,
          unlock_days: config.unlock_days,
          critical_release_pct: config.critical_release_pct,
          critical_min_sources: config.critical_min_sources,
          critical_min_available_sats: config.critical_min_available_sats,
        critical_cycles: config.critical_cycles
      })
      setStatus(t('rebalanceCenter.settingsSaved'))
      loadAll()
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.settingsSaveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const handleToggleChannelAuto = async (channel: RebalanceChannel, enabled: boolean) => {
    try {
      await updateRebalanceChannelAuto({
        channel_id: channel.channel_id,
        channel_point: channel.channel_point,
        auto_enabled: enabled
      })
      loadAll()
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleBulkAuto = async (enabled: boolean) => {
    if (sortedChannels.length === 0) return
    setStatus('')
    try {
      await Promise.all(
        sortedChannels.map((channel) =>
          updateRebalanceChannelAuto({
            channel_id: channel.channel_id,
            channel_point: channel.channel_point,
            auto_enabled: enabled
          })
        )
      )
      loadAll()
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleBulkExclude = async (excluded: boolean) => {
    if (sortedChannels.length === 0) return
    setStatus('')
    try {
      await Promise.all(
        sortedChannels.map((channel) =>
          updateRebalanceExclude({
            channel_id: channel.channel_id,
            channel_point: channel.channel_point,
            excluded
          })
        )
      )
      loadAll()
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleExcludeSource = async (channel: RebalanceChannel, excluded: boolean) => {
    try {
      await updateRebalanceExclude({ channel_id: channel.channel_id, channel_point: channel.channel_point, excluded })
      loadAll()
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleUpdateTarget = async (channel: RebalanceChannel) => {
    const nextValue = editTargets[channel.channel_id]
    const parsed = Number(nextValue)
    if (!Number.isFinite(parsed) || parsed <= 0 || parsed >= 100) {
      setStatus(t('rebalanceCenter.invalidTarget'))
      return
    }
    try {
      await updateRebalanceChannelTarget({
        channel_id: channel.channel_id,
        channel_point: channel.channel_point,
        target_outbound_pct: parsed
      })
      setEditTargets((prev) => ({ ...prev, [channel.channel_id]: String(parsed) }))
      loadAll()
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.saveFailed'))
    }
  }

  const handleRunRebalance = async (channel: RebalanceChannel) => {
    const nextValue = editTargets[channel.channel_id]
    const parsed = nextValue ? Number(nextValue) : channel.target_outbound_pct
    try {
      await runRebalance({
        channel_id: channel.channel_id,
        channel_point: channel.channel_point,
        target_outbound_pct: parsed
      })
      loadAll()
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('rebalanceCenter.runFailed'))
    }
  }

  const togglePaybackFlag = (flag: number) => {
    if (!config) return
    const next = config.payback_mode_flags ^ flag
    setConfig({ ...config, payback_mode_flags: next })
  }

  return (
    <section className="space-y-6">
      <div className="section-card flex flex-wrap items-center justify-between gap-4">
        <div>
          <h2 className="text-2xl font-semibold">{t('rebalanceCenter.title')}</h2>
          <p className="text-fog/60">{t('rebalanceCenter.subtitle')}</p>
        </div>
      </div>

      {status && <p className="text-sm text-brass">{status}</p>}
      {loading && <p className="text-sm text-fog/60">{t('rebalanceCenter.loading')}</p>}

      {overview && (
        <div className="grid gap-4 lg:grid-cols-4">
            <div className="section-card space-y-2">
              <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.liveCost')}</p>
              <p className="text-lg font-semibold text-fog">{formatSats(overview.live_cost_sat)}</p>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.last24h')}</p>
              <p className="text-xs text-fog/50">
                {t('rebalanceCenter.overview.eligibleCounts', {
                  sources: overview.eligible_sources ?? 0,
                  targets: overview.targets_needing ?? 0
                })}
              </p>
              <p className="text-xs text-fog/50">
                {t('rebalanceCenter.overview.lastScan', {
                  value: overview.auto_enabled
                    ? formatTimestamp(overview.last_scan_at)
                    : t('rebalanceCenter.overview.lastScanDisabled')
                })}
              </p>
              {overview.auto_enabled && overview.last_scan_status && (
                <p className="text-xs text-fog/50">
                  {t(`rebalanceCenter.overview.scanStatus.${overview.last_scan_status}`)}
                </p>
              )}
              {overview.auto_enabled && typeof overview.last_scan_top_score_sat === 'number' && overview.last_scan_top_score_sat > 0 && (
                <p className="text-xs text-fog/50">
                  {t('rebalanceCenter.overview.topScore', { value: formatSats(overview.last_scan_top_score_sat) })}
                </p>
              )}
              {overview.auto_enabled && (overview.last_scan_profit_skipped ?? 0) > 0 && (
                <p className="text-xs text-amber-200">
                  {t('rebalanceCenter.overview.profitSkipped', { count: overview.last_scan_profit_skipped })}
                </p>
              )}
            </div>
          <div className="section-card space-y-2">
            <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.effectiveness')}</p>
            <p className="text-lg font-semibold text-fog">{formatPct(overview.effectiveness_7d * 100)}</p>
            <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.roi', { value: overview.roi_7d.toFixed(2) })}</p>
          </div>
            <div className="section-card space-y-2">
              <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.dailyBudget')}</p>
              <p className="text-lg font-semibold text-fog">{formatSats(overview.daily_budget_sat)}</p>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.budgetLast24h')}</p>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.dailySpentAuto', { value: formatSats(overview.daily_spent_auto_sat) })}</p>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.dailySpentManual', { value: formatSats(overview.daily_spent_manual_sat) })}</p>
              {overview.last_scan_status && (overview.last_scan_status === 'budget_exhausted' || overview.last_scan_status === 'budget_insufficient') && (
                <p className="text-xs text-amber-200">
                  {t(`rebalanceCenter.overview.budgetPaused.${overview.last_scan_status}`)}
                </p>
              )}
            </div>
          <div className="section-card space-y-2">
            <p className="text-xs uppercase tracking-wide text-fog/60">{t('rebalanceCenter.overview.autoMode')}</p>
            <p className={`text-lg font-semibold ${overview.auto_enabled ? 'text-emerald-200' : 'text-fog'}`}>
              {overview.auto_enabled ? t('common.enabled') : t('common.disabled')}
            </p>
            <p className="text-xs text-fog/50">{t('rebalanceCenter.overview.scanInterval', { value: config?.scan_interval_sec || '-' })}</p>
          </div>
        </div>
      )}

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('rebalanceCenter.settings.title')}</h3>
            <p className="text-fog/60">{t('rebalanceCenter.settings.subtitle')}</p>
            <p className="text-xs text-fog/50">{t('rebalanceCenter.settings.autoOnlyNote')}</p>
          </div>
          <button className="btn-secondary text-xs px-3 py-1" onClick={() => setAutoOpen((prev) => !prev)}>
            {autoOpen ? t('common.hide') : t('rebalanceCenter.settings.show')}
          </button>
        </div>
        {config && autoOpen && (
          <div className="grid gap-4 lg:grid-cols-3">
            <label
              className="flex items-center gap-2 text-sm text-fog/70"
              title={t('rebalanceCenter.settingsHints.autoEnabled')}
            >
              <input
                type="checkbox"
                checked={config.auto_enabled}
                onChange={(e) => setConfig({ ...config, auto_enabled: e.target.checked })}
              />
              {t('rebalanceCenter.settings.autoEnabled')}
            </label>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.scanInterval')}>
                {t('rebalanceCenter.settings.scanInterval')}
              </label>
              <input
                className="input-field"
                type="number"
                min={30}
                value={config.scan_interval_sec}
                onChange={(e) => setConfig({ ...config, scan_interval_sec: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.dailyBudgetPct')}>
                {t('rebalanceCenter.settings.dailyBudgetPct')}
              </label>
              <input
                className="input-field"
                type="number"
                min={0}
                step={0.1}
                value={config.daily_budget_pct}
                onChange={(e) => setConfig({ ...config, daily_budget_pct: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.deadband')}>
                {t('rebalanceCenter.settings.deadband')}
              </label>
              <input
                className="input-field"
                type="number"
                min={0}
                step={0.1}
                value={config.deadband_pct}
                onChange={(e) => setConfig({ ...config, deadband_pct: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.sourceMinLocal')}>
                {t('rebalanceCenter.settings.sourceMinLocal')}
              </label>
              <input
                className="input-field"
                type="number"
                min={0}
                step={0.1}
                value={config.source_min_local_pct}
                onChange={(e) => setConfig({ ...config, source_min_local_pct: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.econRatio')}>
                {t('rebalanceCenter.settings.econRatio')}
              </label>
              <input
                className="input-field"
                type="number"
                min={0}
                step={0.05}
                value={config.econ_ratio}
                onChange={(e) => setConfig({ ...config, econ_ratio: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.roiMin')}>
                {t('rebalanceCenter.settings.roiMin')}
              </label>
              <input
                className="input-field"
                type="number"
                min={0}
                step={0.1}
                value={config.roi_min}
                onChange={(e) => setConfig({ ...config, roi_min: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.maxConcurrent')}>
                {t('rebalanceCenter.settings.maxConcurrent')}
              </label>
              <input
                className="input-field"
                type="number"
                min={1}
                value={config.max_concurrent}
                onChange={(e) => setConfig({ ...config, max_concurrent: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.minAmount')}>
                {t('rebalanceCenter.settings.minAmount')}
              </label>
              <input
                className="input-field"
                type="number"
                min={0}
                value={config.min_amount_sat}
                onChange={(e) => setConfig({ ...config, min_amount_sat: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.maxAmount')}>
                {t('rebalanceCenter.settings.maxAmount')}
              </label>
              <input
                className="input-field"
                type="number"
                min={0}
                value={config.max_amount_sat}
                onChange={(e) => setConfig({ ...config, max_amount_sat: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.feeSteps')}>
                {t('rebalanceCenter.settings.feeSteps')}
              </label>
              <input
                className="input-field"
                type="number"
                min={1}
                value={config.fee_ladder_steps}
                onChange={(e) => setConfig({ ...config, fee_ladder_steps: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.amountProbeSteps')}>
                {t('rebalanceCenter.settings.amountProbeSteps')}
              </label>
              <input
                className="input-field"
                type="number"
                min={1}
                value={config.amount_probe_steps}
                onChange={(e) => setConfig({ ...config, amount_probe_steps: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.attemptTimeout')}>
                {t('rebalanceCenter.settings.attemptTimeout')}
              </label>
              <input
                className="input-field"
                type="number"
                min={5}
                value={config.attempt_timeout_sec}
                onChange={(e) => setConfig({ ...config, attempt_timeout_sec: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.rebalanceTimeout')}>
                {t('rebalanceCenter.settings.rebalanceTimeout')}
              </label>
              <input
                className="input-field"
                type="number"
                min={60}
                value={config.rebalance_timeout_sec}
                onChange={(e) => setConfig({ ...config, rebalance_timeout_sec: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="flex items-center gap-2 text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.amountProbeAdaptive')}>
                <input
                  type="checkbox"
                  checked={config.amount_probe_adaptive}
                  onChange={(e) => setConfig({ ...config, amount_probe_adaptive: e.target.checked })}
                />
                {t('rebalanceCenter.settings.amountProbeAdaptive')}
              </label>
            </div>
          </div>
        )}
        {config && autoOpen && (
          <div className="grid gap-4 lg:grid-cols-3">
            <div className="space-y-2">
              <p className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.paybackPolicy')}>
                {t('rebalanceCenter.settings.paybackPolicy')}
              </p>
              <label
                className="flex items-center gap-2 text-sm text-fog/70"
                title={t('rebalanceCenter.settingsHints.paybackMode')}
              >
                <input
                  type="checkbox"
                  checked={(config.payback_mode_flags & PAYBACK_MODE_PAYBACK) !== 0}
                  onChange={() => togglePaybackFlag(PAYBACK_MODE_PAYBACK)}
                />
                {t('rebalanceCenter.settings.paybackMode')}
              </label>
              <label
                className="flex items-center gap-2 text-sm text-fog/70"
                title={t('rebalanceCenter.settingsHints.timeMode')}
              >
                <input
                  type="checkbox"
                  checked={(config.payback_mode_flags & PAYBACK_MODE_TIME) !== 0}
                  onChange={() => togglePaybackFlag(PAYBACK_MODE_TIME)}
                />
                {t('rebalanceCenter.settings.timeMode')}
              </label>
              <label
                className="flex items-center gap-2 text-sm text-fog/70"
                title={t('rebalanceCenter.settingsHints.criticalMode')}
              >
                <input
                  type="checkbox"
                  checked={(config.payback_mode_flags & PAYBACK_MODE_CRITICAL) !== 0}
                  onChange={() => togglePaybackFlag(PAYBACK_MODE_CRITICAL)}
                />
                {t('rebalanceCenter.settings.criticalMode')}
              </label>
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.unlockDays')}>
                {t('rebalanceCenter.settings.unlockDays')}
              </label>
              <input
                className="input-field"
                type="number"
                min={1}
                value={config.unlock_days}
                onChange={(e) => setConfig({ ...config, unlock_days: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.criticalRelease')}>
                {t('rebalanceCenter.settings.criticalRelease')}
              </label>
              <input
                className="input-field"
                type="number"
                min={0}
                step={1}
                value={config.critical_release_pct}
                onChange={(e) => setConfig({ ...config, critical_release_pct: Number(e.target.value) })}
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm text-fog/70" title={t('rebalanceCenter.settingsHints.criticalCycles')}>
                {t('rebalanceCenter.settings.criticalCycles')}
              </label>
              <input
                className="input-field"
                type="number"
                min={1}
                value={config.critical_cycles}
                onChange={(e) => setConfig({ ...config, critical_cycles: Number(e.target.value) })}
              />
            </div>
          </div>
        )}
        {autoOpen && (
          <div className="flex flex-wrap items-center gap-3">
            <button className="btn-primary" onClick={handleSaveConfig} disabled={saving}>
              {saving ? t('rebalanceCenter.saving') : t('rebalanceCenter.save')}
            </button>
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('rebalanceCenter.channels.title')}</h3>
          <div className="flex flex-wrap items-center gap-4 text-xs text-fog/60">
            <span>{t('rebalanceCenter.channels.count', { count: channels.length })}</span>
            <div className="flex items-center gap-2">
              <span>{t('rebalanceCenter.channels.sortLabel')}</span>
              <div className="flex items-center rounded-full border border-white/10 bg-white/5 p-1">
                <button
                  className={`px-3 py-1 text-[11px] font-semibold uppercase tracking-wide ${channelSort === 'economic' ? 'rounded-full bg-emerald-500/20 text-emerald-100' : 'text-fog/60'}`}
                  onClick={() => setChannelSort('economic')}
                >
                  {t('rebalanceCenter.channels.sortEconomic')}
                </button>
                <button
                  className={`px-3 py-1 text-[11px] font-semibold uppercase tracking-wide ${channelSort === 'emptiest' ? 'rounded-full bg-sky-500/20 text-sky-100' : 'text-fog/60'}`}
                  onClick={() => setChannelSort('emptiest')}
                >
                  {t('rebalanceCenter.channels.sortEmptiest')}
                </button>
              </div>
            </div>
          </div>
        </div>
        <div className="max-h-[520px] overflow-x-auto overflow-y-auto pr-1">
            <table className="w-full text-sm text-fog/70">
            <thead>
              <tr className="text-left">
                <th className="pb-2">{t('rebalanceCenter.channels.channel')}</th>
                <th className="pb-2 pl-6">{t('rebalanceCenter.channels.balance')}</th>
                <th className="pb-2 pl-6">{t('rebalanceCenter.channels.fees')}</th>
                <th className="pb-2 pl-6">{t('rebalanceCenter.channels.target')}</th>
                <th className="pb-2 text-center">{t('rebalanceCenter.channels.protected')}</th>
                <th className="pb-2">
                  <div className="flex flex-col gap-2">
                    <span>{t('rebalanceCenter.channels.actions')}</span>
                    <label className="flex items-center gap-2 text-xs text-fog/70">
                      <input
                        type="checkbox"
                        checked={sortedChannels.length > 0 && sortedChannels.every((ch) => ch.auto_enabled)}
                        onChange={(e) => handleBulkAuto(e.target.checked)}
                      />
                      {t('rebalanceCenter.channels.bulkAuto')}
                    </label>
                    <label className="flex items-center gap-2 text-xs text-fog/70">
                      <input
                        type="checkbox"
                        checked={sortedChannels.length > 0 && sortedChannels.every((ch) => ch.excluded_as_source)}
                        onChange={(e) => handleBulkExclude(e.target.checked)}
                      />
                      {t('rebalanceCenter.channels.bulkExclude')}
                    </label>
                  </div>
                </th>
              </tr>
            </thead>
            <tbody>
              {sortedChannels.map((ch) => {
                const meetsRoi = !config || config.roi_min <= 0 || !ch.roi_estimate_valid || ch.roi_estimate >= config.roi_min
                const isAutoTarget = ch.eligible_as_target && ch.auto_enabled && meetsRoi
                const scoreMeta = config ? computeChannelScore(ch, config.econ_ratio) : null
                const scoreTitle =
                  scoreMeta
                    ? t('rebalanceCenter.channels.scoreHint', {
                        score: formatSats(scoreMeta.score),
                        gain: formatSats(scoreMeta.expectedGain),
                        cost: formatSats(scoreMeta.estimatedCost),
                        roi: scoreMeta.estimatedCost > 0 ? formatRoi(scoreMeta.expectedRoi) : t('rebalanceCenter.channels.scoreRoiNA')
                      })
                    : undefined
                const highlight = isAutoTarget
                  ? 'bg-rose-500/10'
                  : ch.eligible_as_target
                    ? 'bg-amber-500/10'
                    : ch.eligible_as_source
                      ? 'bg-emerald-500/10'
                      : ''
                return (
                  <tr key={ch.channel_point || String(ch.channel_id)} className={`border-t border-white/5 group ${highlight}`}>
                    <td className="py-3" title={scoreTitle}>
                      <div className="text-fog">{ch.peer_alias || ch.remote_pubkey}</div>
                      <div className="text-xs text-fog/50">{ch.channel_point}</div>
                      {scoreMeta && (
                        <div className="text-xs text-fog/40 opacity-0 transition group-hover:opacity-100">
                          {t('rebalanceCenter.channels.scoreLabel')}: {formatSats(scoreMeta.score)}
                        </div>
                      )}
                    </td>
                  <td className="py-3 pl-6">
                    <div>{formatPct(ch.local_pct)} / {formatPct(ch.remote_pct)}</div>
                    <div className="text-xs text-fog/50">{formatSats(ch.local_balance_sat)} | {formatSats(ch.remote_balance_sat)}</div>
                  </td>
                  <td className="py-3 pl-6">
                    <div>
                      {t('rebalanceCenter.channels.feeOut', { value: ch.outgoing_fee_ppm })} ·{' '}
                      {t('rebalanceCenter.channels.feePeer', { value: ch.peer_fee_rate_ppm })}
                    </div>
                    <div className="text-xs text-fog/50">{t('rebalanceCenter.channels.spread', { value: ch.spread_ppm })}</div>
                  </td>
                  <td className="py-3 pl-6">
                    <div className="flex items-center gap-2">
                      <input
                        className="input-field w-24"
                        type="number"
                        min={1}
                        max={99}
                        step={0.1}
                        title={t('rebalanceCenter.channelsHints.targetOutbound')}
                        value={editTargets[ch.channel_id] ?? String(Math.round(ch.target_outbound_pct * 10) / 10)}
                        onChange={(e) => setEditTargets((prev) => ({ ...prev, [ch.channel_id]: e.target.value }))}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            e.preventDefault()
                            handleUpdateTarget(ch)
                          }
                        }}
                      />
                      <span className="text-xs text-fog/60">%</span>
                    </div>
                    <div className="text-xs text-fog/50">
                      {t('rebalanceCenter.channels.amount', { value: formatSats(ch.target_amount_sat) })}
                    </div>
                  </td>
                  <td className="py-3 text-center">
                    <div>{formatSats(ch.protected_liquidity_sat)}</div>
                    <div className="text-xs text-fog/50">{t('rebalanceCenter.channels.payback', { value: (ch.payback_progress * 100).toFixed(0) })}</div>
                  </td>
                  <td className="py-3 space-y-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <button
                        className="btn-secondary text-xs px-3 py-1"
                        onClick={() => handleUpdateTarget(ch)}
                        title={t('rebalanceCenter.channelsHints.saveTarget')}
                      >
                        {t('rebalanceCenter.channels.saveTarget')}
                      </button>
                      <button
                        className="btn-primary text-xs px-3 py-1"
                        onClick={() => handleRunRebalance(ch)}
                        disabled={!ch.eligible_as_target}
                        title={t('rebalanceCenter.channelsHints.rebalanceIn')}
                      >
                        {t('rebalanceCenter.channels.rebalanceIn')}
                      </button>
                    </div>
                    <div className="flex flex-wrap items-center gap-2 text-xs">
                      <label className="flex items-center gap-2" title={t('rebalanceCenter.channelsHints.auto')}>
                        <input
                          type="checkbox"
                        checked={ch.auto_enabled}
                        onChange={(e) => handleToggleChannelAuto(ch, e.target.checked)}
                      />
                        {t('rebalanceCenter.channels.auto')}
                      </label>
                      <label className="flex items-center gap-2" title={t('rebalanceCenter.channelsHints.excludeSource')}>
                        <input
                          type="checkbox"
                          checked={ch.excluded_as_source}
                          onChange={(e) => handleExcludeSource(ch, e.target.checked)}
                        />
                        {t('rebalanceCenter.channels.excludeSource')}
                      </label>
                    </div>
                      <div className="text-xs text-fog/50">
                        {ch.roi_estimate_valid
                          ? t('rebalanceCenter.channels.roiEstimate', { value: formatRoi(ch.roi_estimate) })
                          : t('rebalanceCenter.channels.roiEstimateNA')}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('rebalanceCenter.queue.title')}</h3>
          {queueJobs.length === 0 && <p className="text-sm text-fog/60">{t('rebalanceCenter.queue.empty')}</p>}
          <div className="max-h-80 space-y-3 overflow-y-auto pr-1">
            {queueJobs.map((job) => (
              <div key={job.id} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm text-fog">#{job.id} {job.source}</p>
                    <p className="text-xs text-fog/70">{t('rebalanceCenter.queue.targetLabel', { value: job.target_peer_alias || job.target_channel_point })}</p>
                    <p className="text-xs text-fog/50">{job.target_channel_point}</p>
                  </div>
                  <span className={`text-xs uppercase tracking-wide ${statusClass(job.status)}`}>{job.status}</span>
                </div>
                <div className="mt-2 text-xs text-fog/50">
                  {t('rebalanceCenter.queue.target', { value: formatPct(job.target_outbound_pct) })}
                </div>
                {job.status === 'partial' && parseRemaining(job.reason) !== null && (
                  <div className="mt-1 text-xs text-amber-200">
                    {t('rebalanceCenter.queue.remaining', { value: formatSats(parseRemaining(job.reason) || 0) })}
                  </div>
                )}
                {job.reason && job.status !== 'partial' && (
                  <div className="mt-1 text-xs text-amber-200">{job.reason}</div>
                )}
                {queueAttempts.filter((attempt) => attempt.job_id === job.id).map((attempt) => (
                  <div key={attempt.id} className="mt-2 text-xs text-fog/60">
                    {t('rebalanceCenter.queue.attempt', {
                      index: attempt.attempt_index,
                      amount: formatSats(attempt.amount_sat),
                      fee: attempt.fee_limit_ppm
                    })}
                  </div>
                ))}
              </div>
            ))}
          </div>
        </div>

        <div className="section-card space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold">{t('rebalanceCenter.history.title')}</h3>
              <p className="text-xs text-fog/50">{t('rebalanceCenter.history.last24h')}</p>
            </div>
            <div className="flex flex-wrap items-center gap-2 text-xs">
              {(['all', 'succeeded', 'partial', 'failed'] as const).map((filter) => (
                <button
                  key={filter}
                  className={`rounded-full border px-3 py-1 ${
                    historyFilter === filter ? 'border-mint text-mint' : 'border-white/10 text-fog/60'
                  }`}
                  onClick={() => setHistoryFilter(filter)}
                >
                  {t(`rebalanceCenter.history.filters.${filter}`)}
                </button>
              ))}
            </div>
          </div>
          {filteredHistory.length === 0 && <p className="text-sm text-fog/60">{t('rebalanceCenter.history.empty')}</p>}
          <div className="max-h-80 space-y-3 overflow-y-auto pr-1">
            {filteredHistory.map((job) => (
              <div key={job.id} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex items-center justify-between">
                  <div>
                    <p className="text-sm text-fog">#{job.id} {job.source}</p>
                    <p className="text-xs text-fog/70">{t('rebalanceCenter.history.targetLabel', { value: job.target_peer_alias || job.target_channel_point })}</p>
                    <p className="text-xs text-fog/50">{job.target_channel_point}</p>
                  </div>
                  <span className={`text-xs uppercase tracking-wide ${statusClass(job.status)}`}>{job.status}</span>
                </div>
                <div className="mt-2 text-xs text-fog/50">
                  {t('rebalanceCenter.history.target', { value: formatPct(job.target_outbound_pct) })}
                </div>
                {job.status === 'succeeded' && (
                  <div className="mt-1 text-xs text-fog/60">
                    {t('rebalanceCenter.history.amountFee', {
                      amount: formatSats(historyTotals.get(job.id)?.amount ?? 0),
                      fee: formatSats(historyTotals.get(job.id)?.fee ?? 0)
                    })}
                  </div>
                )}
                {job.status === 'partial' && parseRemaining(job.reason) !== null && (
                  <div className="mt-1 text-xs text-amber-200">
                    {t('rebalanceCenter.history.remaining', { value: formatSats(parseRemaining(job.reason) || 0) })}
                  </div>
                )}
                {job.reason && job.status !== 'partial' && (
                  <div className="mt-1 text-xs text-amber-200">{job.reason}</div>
                )}
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  )
}

