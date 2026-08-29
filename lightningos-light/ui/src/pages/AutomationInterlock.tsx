import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getAutofeeConfig,
  getAutofeeStatus,
  getAutomationIntentConfig,
  getAutomationIntentHistory,
  getAutomationIntents,
  getLnChannels,
  getRebalanceConfig,
  getRebalanceOverview,
  updateAutomationIntentConfig,
  updateRebalanceConfig
} from '../api'
import type { AutomationIntent, AutomationIntentConfig, AutomationIntentEvent } from '../api'
import type { RebalanceOverview, RebalanceSovereignDecision } from '../components/rebalance/types'
import { getLocale } from '../i18n'

type AutomationMode = AutomationIntentConfig['mode']

type AutofeeSummary = {
  enabled?: boolean
  operation_mode?: string
  profile?: string
}

type AutofeeStatus = {
  running?: boolean
  last_run_at?: string
  last_error?: string
}

type RebalanceInterlockConfig = {
  autofee_settling_window_sec?: number
  autofee_settling_multiplier?: number
}

type ChannelSummary = {
  channel_point: string
  channel_id?: number
  channel_id_str?: string
  peer_alias?: string
  remote_pubkey?: string
}

type IconProps = { className?: string }

const SignalIcon = ({ className = 'h-5 w-5' }: IconProps) => (
  <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
    <circle cx="12" cy="12" r="2" />
    <path d="M8.5 8.5a5 5 0 0 0 0 7M15.5 8.5a5 5 0 0 1 0 7M5.5 5.5a9.2 9.2 0 0 0 0 13M18.5 5.5a9.2 9.2 0 0 1 0 13" />
  </svg>
)

const ScoreIcon = ({ className = 'h-5 w-5' }: IconProps) => (
  <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
    <path d="M4 18V9M10 18V5M16 18v-7M22 18H2" />
    <path d="m15 7 3-3 3 3M18 4v7" />
  </svg>
)

const QueueIcon = ({ className = 'h-5 w-5' }: IconProps) => (
  <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
    <path d="M5 5h14v14H5zM8 9h8M8 12h8M8 15h5" />
  </svg>
)

const ShieldIcon = ({ className = 'h-5 w-5' }: IconProps) => (
  <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
    <path d="M12 3 5 6v5c0 4.6 2.8 8.2 7 10 4.2-1.8 7-5.4 7-10V6l-7-3Z" />
    <path d="m9 12 2 2 4-4" />
  </svg>
)

const Arrow = () => (
  <div className="hidden items-center text-fog/25 md:flex" aria-hidden="true">
    <div className="h-px w-8 bg-current" />
    <div className="-ml-1 h-2 w-2 rotate-45 border-r border-t border-current" />
  </div>
)

const modeTone = (mode: AutomationMode) => {
  if (mode === 'enforce') return 'border-emerald-400/35 bg-emerald-400/10 text-emerald-100'
  if (mode === 'shadow') return 'border-cyan-400/35 bg-cyan-400/10 text-cyan-100'
  return 'border-white/10 bg-white/5 text-fog/60'
}

const kindTone = (kind: AutomationIntent['kind']) =>
  kind === 'refill_target'
    ? 'border-cyan-400/30 bg-cyan-400/10 text-cyan-100'
    : 'border-amber-400/30 bg-amber-400/10 text-amber-100'

const intentKey = (intent: Pick<AutomationIntent, 'channel_point' | 'channel_id_str' | 'channel_id'>) =>
  String(intent.channel_point || intent.channel_id_str || intent.channel_id || '').trim()

const decisionKey = (decision: RebalanceSovereignDecision) =>
  String(decision.channel_point || decision.channel_id || '').trim()

const shortChannelPoint = (value?: string) => {
  const text = String(value || '').trim()
  if (text.length <= 28) return text
  return `${text.slice(0, 14)}…${text.slice(-8)}`
}

const metadataNumber = (metadata: Record<string, unknown> | undefined, key: string) => {
  const value = Number(metadata?.[key])
  return Number.isFinite(value) ? value : undefined
}

const metadataBoolean = (metadata: Record<string, unknown> | undefined, key: string) =>
  typeof metadata?.[key] === 'boolean' ? metadata[key] as boolean : undefined

const metadataString = (metadata: Record<string, unknown> | undefined, key: string) =>
  typeof metadata?.[key] === 'string' ? metadata[key] as string : undefined

function ImpactStage({ icon, value, label, hint, tone }: { icon: ReactNode; value: number | string; label: string; hint: string; tone: string }) {
  return (
    <div className={`min-w-0 flex-1 rounded-2xl border p-4 ${tone}`}>
      <div className="flex items-center gap-3">
        <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl border border-current/20 bg-black/15">{icon}</div>
        <div>
          <div className="text-2xl font-semibold leading-none">{value}</div>
          <div className="mt-1 text-xs font-medium">{label}</div>
        </div>
      </div>
      <p className="mt-3 text-[11px] leading-relaxed opacity-70">{hint}</p>
    </div>
  )
}

function InfoTooltip({ title, text }: { title: string; text: string }) {
  return (
    <span className="group relative inline-flex">
      <button
        type="button"
        className="grid h-5 w-5 place-items-center rounded-full border border-cyan-300/20 bg-cyan-300/[0.07] text-[11px] font-semibold text-cyan-100/75 transition hover:border-cyan-300/40 hover:text-cyan-50 focus:outline-none focus:ring-2 focus:ring-cyan-300/30"
        aria-label={`${title}. ${text}`}
      >
        i
      </button>
      <span
        role="tooltip"
        className="pointer-events-none absolute left-0 top-full z-40 mt-2 w-72 max-w-[75vw] rounded-xl border border-cyan-300/20 bg-[#0b1322] p-3 text-left opacity-0 shadow-2xl shadow-black/50 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100"
      >
        <span className="block text-xs font-semibold text-cyan-100">{title}</span>
        <span className="mt-1.5 block text-[11px] leading-relaxed text-fog/70">{text}</span>
      </span>
    </span>
  )
}

export default function AutomationInterlock() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }), [locale])
  const integerFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }), [locale])
  const dateFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  )

  const [config, setConfig] = useState<AutomationIntentConfig | null>(null)
  const [allIntents, setAllIntents] = useState<AutomationIntent[]>([])
  const [history, setHistory] = useState<AutomationIntentEvent[]>([])
  const [channels, setChannels] = useState<ChannelSummary[]>([])
  const [autofee, setAutofee] = useState<AutofeeSummary | null>(null)
  const [autofeeStatus, setAutofeeStatus] = useState<AutofeeStatus | null>(null)
  const [rebalance, setRebalance] = useState<RebalanceOverview | null>(null)
  const [rebalanceConfig, setRebalanceConfig] = useState<RebalanceInterlockConfig | null>(null)
  const [multiplier, setMultiplier] = useState('1.2')
  const [minConfidence, setMinConfidence] = useState('0.7')
  const [settlingMultiplier, setSettlingMultiplier] = useState('0.75')
  const [settlingWindowHours, setSettlingWindowHours] = useState('2')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')

  const applyConfig = (next: AutomationIntentConfig) => {
    setConfig(next)
    setMultiplier(String(next.refill_score_multiplier))
    setMinConfidence(String(next.min_confidence))
  }

  const applyRebalanceConfig = (next: RebalanceInterlockConfig) => {
    setRebalanceConfig(next)
    setSettlingMultiplier(String(next.autofee_settling_multiplier ?? 0.75))
    setSettlingWindowHours(String(Number(next.autofee_settling_window_sec ?? 7200) / 3600))
  }

  const load = async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    const results = await Promise.allSettled([
      getAutomationIntentConfig(),
      getAutomationIntents(false),
      getAutomationIntentHistory(200),
      getLnChannels(),
      getAutofeeConfig(),
      getAutofeeStatus(),
      getRebalanceConfig(),
      getRebalanceOverview()
    ])
    const [configResult, intentsResult, historyResult, channelsResult, autofeeResult, autofeeStatusResult, rebalanceConfigResult, rebalanceResult] = results
    if (configResult.status === 'fulfilled') {
      applyConfig(configResult.value)
      setMessage('')
    } else {
      setMessage(configResult.reason instanceof Error ? configResult.reason.message : t('automationInterlock.loadFailed'))
    }
    if (intentsResult.status === 'fulfilled') setAllIntents(intentsResult.value.items || [])
    if (historyResult.status === 'fulfilled') setHistory(historyResult.value.items || [])
    if (channelsResult.status === 'fulfilled') {
      const payload = channelsResult.value as { channels?: ChannelSummary[] }
      setChannels(Array.isArray(payload?.channels) ? payload.channels : [])
    }
    if (autofeeResult.status === 'fulfilled') setAutofee(autofeeResult.value as AutofeeSummary)
    if (autofeeStatusResult.status === 'fulfilled') setAutofeeStatus(autofeeStatusResult.value as AutofeeStatus)
    if (rebalanceConfigResult.status === 'fulfilled') applyRebalanceConfig(rebalanceConfigResult.value as RebalanceInterlockConfig)
    if (rebalanceResult.status === 'fulfilled') setRebalance(rebalanceResult.value as RebalanceOverview)
    setLoading(false)
    setRefreshing(false)
  }

  useEffect(() => {
    void load()
  }, [])

  const saveMode = async (mode: AutomationMode) => {
    if (!config || saving || mode === config.mode) return
    setSaving(true)
    setMessage('')
    try {
      applyConfig(await updateAutomationIntentConfig({ mode }))
      setMessage(t('automationInterlock.saved'))
    } catch (err) {
      setMessage(err instanceof Error ? err.message : t('automationInterlock.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const saveAdvanced = async () => {
    const nextMultiplier = Number(multiplier)
    const nextConfidence = Number(minConfidence)
    const nextSettlingMultiplier = Number(settlingMultiplier)
    const nextSettlingWindowHours = Number(settlingWindowHours)
    if (!Number.isFinite(nextMultiplier) || nextMultiplier < 1 || nextMultiplier > 1.5 ||
        !Number.isFinite(nextConfidence) || nextConfidence < 0.5 || nextConfidence > 1 ||
        !Number.isFinite(nextSettlingMultiplier) || nextSettlingMultiplier <= 0 || nextSettlingMultiplier > 1 ||
        !Number.isFinite(nextSettlingWindowHours) || nextSettlingWindowHours < 0) {
      setMessage(t('automationInterlock.invalidSettings'))
      return
    }
    if (!rebalanceConfig) {
      setMessage(t('automationInterlock.rebalanceConfigUnavailable'))
      return
    }
    setSaving(true)
    setMessage('')
    try {
      const [nextIntentConfig, nextRebalanceConfig] = await Promise.all([
        updateAutomationIntentConfig({
          refill_score_multiplier: nextMultiplier,
          min_confidence: nextConfidence
        }),
        updateRebalanceConfig({
          autofee_settling_multiplier: nextSettlingMultiplier,
          autofee_settling_window_sec: Math.round(nextSettlingWindowHours * 3600)
        })
      ])
      applyConfig(nextIntentConfig)
      applyRebalanceConfig(nextRebalanceConfig as RebalanceInterlockConfig)
      setMessage(t('automationInterlock.settingsSaved'))
    } catch (err) {
      await load(true)
      setMessage(err instanceof Error ? err.message : t('automationInterlock.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const intents = useMemo(() => {
    const now = Date.now()
    return allIntents.filter((item) => item.active && new Date(item.expires_at).getTime() > now)
  }, [allIntents])
  const refillIntents = intents.filter((item) => item.kind === 'refill_target')
  const floorIntents = intents.filter((item) => item.kind === 'protect_fee_floor')
  const averageConfidence = intents.length
    ? intents.reduce((sum, item) => sum + Number(item.confidence || 0), 0) / intents.length
    : 0

  const channelByPoint = useMemo(
    () => new Map(channels.map((channel) => [String(channel.channel_point || '').trim(), channel])),
    [channels]
  )
  const channelByID = useMemo(() => {
    const result = new Map<string, ChannelSummary>()
    channels.forEach((channel) => {
      const exact = String(channel.channel_id_str || '').trim()
      if (exact) result.set(exact, channel)
      const numeric = String(channel.channel_id || '').trim()
      if (numeric) result.set(numeric, channel)
    })
    return result
  }, [channels])
  const intentByID = useMemo(() => new Map(allIntents.map((intent) => [intent.id, intent])), [allIntents])
  const decisions = rebalance?.sovereign_decisions || []
  const decisionByKey = useMemo(() => {
    const result = new Map<string, RebalanceSovereignDecision>()
    decisions.forEach((decision) => result.set(decisionKey(decision), decision))
    return result
  }, [decisions])
  const influencedDecisions = decisions.filter((decision) => Boolean(decision.intent_kind))
  const simulatedDecisions = influencedDecisions.filter((decision) => decision.intent_shadow)
  const selectedInfluenced = influencedDecisions.filter((decision) => decision.intent_applied && decision.selected)
  const liveScoreDelta = influencedDecisions.reduce(
    (sum, decision) => sum + Number(decision.intent_score_after || 0) - Number(decision.intent_score_before || 0),
    0
  )
  const liveScanAt = rebalance?.last_scan_at || rebalance?.sovereign_last_decision_at
  const hasLiveDecisionSnapshot = Boolean(liveScanAt && rebalance?.sovereign_decisions)
  const scoreEffectEvents = history.filter((event) =>
    event.event_type === 'applied' &&
    event.consumer === 'rebalance' &&
    metadataNumber(event.metadata, 'score_before') != null &&
    metadataNumber(event.metadata, 'score_after') != null
  )
  const latestEffectAt = scoreEffectEvents[0]?.occurred_at
  const persistedEffectEvents = latestEffectAt
    ? scoreEffectEvents.filter((event) => event.occurred_at === latestEffectAt)
    : []
  const hasPersistedEffectSnapshot = !hasLiveDecisionSnapshot && persistedEffectEvents.length > 0
  const hasImpactSnapshot = hasLiveDecisionSnapshot || hasPersistedEffectSnapshot
  const persistedSelectionKnown = persistedEffectEvents.length > 0 &&
    persistedEffectEvents.every((event) => metadataBoolean(event.metadata, 'selected') != null)
  const persistedSelected = persistedEffectEvents.filter((event) =>
    metadataBoolean(event.metadata, 'selected') === true && metadataString(event.metadata, 'mode') === 'enforce'
  ).length
  const persistedScoreDelta = persistedEffectEvents.reduce((sum, event) =>
    sum + Number(metadataNumber(event.metadata, 'score_after') || 0) - Number(metadataNumber(event.metadata, 'score_before') || 0), 0)
  const impactInfluenced = hasLiveDecisionSnapshot ? influencedDecisions.length : persistedEffectEvents.length
  const impactSelected: number | undefined = hasLiveDecisionSnapshot
    ? selectedInfluenced.length
    : persistedSelectionKnown ? persistedSelected : undefined
  const totalScoreDelta = hasLiveDecisionSnapshot ? liveScoreDelta : persistedScoreDelta
  const impactSimulated = hasLiveDecisionSnapshot
    ? simulatedDecisions.length === influencedDecisions.length && influencedDecisions.length > 0
    : persistedEffectEvents.length > 0 && persistedEffectEvents.every((event) => metadataBoolean(event.metadata, 'shadow') === true)
  const latestPersistedScan = rebalance?.sovereign_history_24h?.[0]
  const impactDate = hasLiveDecisionSnapshot
    ? liveScanAt
    : hasPersistedEffectSnapshot ? latestEffectAt : latestPersistedScan?.scan_at
  const impactStatus = hasLiveDecisionSnapshot
    ? rebalance?.last_scan_status || rebalance?.sovereign_last_mode || t('common.unknown')
    : hasPersistedEffectSnapshot ? t('automationInterlock.persistedImpact') : t('automationInterlock.snapshotUnavailable')
  const floorEffectEvents = history.filter((event) =>
    event.event_type === 'applied' &&
    event.kind === 'protect_fee_floor' &&
    event.consumer === 'autofee' &&
    metadataNumber(event.metadata, 'ppm_before') != null &&
    metadataNumber(event.metadata, 'ppm_after') != null
  )
  const latestFloorEffectAt = floorEffectEvents[0]?.occurred_at
  const latestFloorRunID = metadataString(floorEffectEvents[0]?.metadata, 'run_id')
  const latestFloorEffectEvents = latestFloorRunID
    ? floorEffectEvents.filter((event) => metadataString(event.metadata, 'run_id') === latestFloorRunID)
    : latestFloorEffectAt
      ? floorEffectEvents.filter((event) => event.occurred_at === latestFloorEffectAt)
      : []
  const hasFloorImpactSnapshot = latestFloorEffectEvents.length > 0
  const floorImpactSimulated = hasFloorImpactSnapshot && latestFloorEffectEvents.every((event) =>
    metadataBoolean(event.metadata, 'shadow') === true || metadataBoolean(event.metadata, 'dry_run') === true
  )
  const floorTargetChanges = latestFloorEffectEvents.filter((event) =>
    metadataNumber(event.metadata, 'ppm_before') !== metadataNumber(event.metadata, 'ppm_after')
  ).length
  const effectByIntentID = new Map<number, AutomationIntentEvent>()
  scoreEffectEvents.forEach((event) => {
    if (event.intent_id && !effectByIntentID.has(event.intent_id)) effectByIntentID.set(event.intent_id, event)
  })
  const appliedByIntentID = new Map<number, AutomationIntentEvent>()
  history.forEach((event) => {
    if (event.event_type === 'applied' && event.intent_id && !appliedByIntentID.has(event.intent_id)) {
      appliedByIntentID.set(event.intent_id, event)
    }
  })

  const formatDate = (value?: string) => {
    if (!value) return t('common.na')
    const parsed = new Date(value)
    return Number.isNaN(parsed.getTime()) ? value : dateFormatter.format(parsed)
  }
  const formatPercent = (value?: number) => `${numberFormatter.format(Number(value || 0) * 100)}%`
  const formatDuration = (seconds?: number) => {
    const hours = Math.round(Number(seconds || 0) / 3600)
    return t('automationInterlock.hours', { count: hours })
  }
  const aliasForIntent = (intent: AutomationIntent) => {
    const channel = channelByPoint.get(String(intent.channel_point || '').trim()) ||
      channelByID.get(String(intent.channel_id_str || intent.channel_id || '').trim())
    return channel?.peer_alias || channel?.remote_pubkey || t('automationInterlock.unknownChannel')
  }
  const aliasForEvent = (event: AutomationIntentEvent) => {
    const intent = event.intent_id ? intentByID.get(event.intent_id) : undefined
    if (intent) return aliasForIntent(intent)
    const channel = channelByID.get(String(event.channel_id_str || event.channel_id || '').trim())
    return channel?.peer_alias || channel?.remote_pubkey || t('automationInterlock.unknownChannel')
  }
  const decisionForIntent = (intent: AutomationIntent) =>
    decisionByKey.get(String(intent.channel_point || '').trim()) || decisionByKey.get(intentKey(intent))
  const reasonLabel = (reason?: string) => reason
    ? t(`automationInterlock.blockReasons.${reason}`, { defaultValue: reason.replace(/_/g, ' ') })
    : t('automationInterlock.awaitingEvaluation')

  const parsedMultiplier = Number(multiplier)
  const parsedConfidence = Number(minConfidence)
  const parsedSettlingMultiplier = Number(settlingMultiplier)
  const parsedSettlingWindowHours = Number(settlingWindowHours)
  const calibrationPreviewReady = [parsedMultiplier, parsedConfidence, parsedSettlingMultiplier, parsedSettlingWindowHours]
    .every(Number.isFinite)
  const admittedMultiplier = calibrationPreviewReady
    ? 1 + ((parsedMultiplier - 1) * parsedConfidence)
    : 0
  const settlingEnabled = calibrationPreviewReady && parsedSettlingWindowHours > 0 && parsedSettlingMultiplier < 1
  const combinedMultiplier = settlingEnabled ? admittedMultiplier * parsedSettlingMultiplier : admittedMultiplier
  const combinedTone = combinedMultiplier < 1
    ? 'border-amber-300/25 bg-amber-300/[0.07] text-amber-100'
    : 'border-emerald-300/25 bg-emerald-300/[0.07] text-emerald-100'

  if (loading && !config) {
    return <p className="text-sm text-fog/70">{t('automationInterlock.loading')}</p>
  }

  return (
    <section className="space-y-6">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-2xl font-semibold">{t('automationInterlock.pageTitle')}</h2>
            {config && (
              <span className={`rounded-full border px-2.5 py-1 text-xs ${modeTone(config.mode)}`}>
                {t(`automationInterlock.modes.${config.mode}`)}
              </span>
            )}
          </div>
          <p className="max-w-3xl text-sm text-fog/70">{t('automationInterlock.pageSubtitle')}</p>
        </div>
        <button type="button" className="btn-secondary" disabled={refreshing} onClick={() => void load(true)}>
          {refreshing ? t('automationInterlock.refreshing') : t('common.refresh')}
        </button>
      </header>

      {message && (
        <div className="rounded-2xl border border-amber-400/30 bg-amber-400/10 px-4 py-3 text-sm text-amber-100">
          {message}
        </div>
      )}

      <section className="overflow-hidden rounded-3xl border border-cyan-300/15 bg-gradient-to-br from-cyan-400/[0.08] via-ink/80 to-amber-400/[0.05] p-5 sm:p-6">
        <div>
          <div className="flex items-center gap-2 text-cyan-100">
            <SignalIcon />
            <h3 className="text-lg font-semibold">{t('automationInterlock.directionalImpact')}</h3>
          </div>
          <p className="mt-1 max-w-4xl text-sm text-fog/60">{t('automationInterlock.directionalImpactHint')}</p>
        </div>

        <div className="mt-5 grid gap-5 2xl:grid-cols-2">
          <article className="rounded-2xl border border-cyan-400/20 bg-cyan-400/[0.035] p-4 sm:p-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2 text-cyan-100">
                  <SignalIcon className="h-4 w-4" />
                  <h4 className="font-semibold">{t('automationInterlock.autofeeToRebalanceImpact')}</h4>
                </div>
                <p className="mt-1 text-xs text-fog/55">
                  {t('automationInterlock.lastScanMeta', {
                    date: formatDate(impactDate),
                    status: impactStatus
                  })}
                </p>
              </div>
              <span className={`rounded-full border px-3 py-1 text-xs ${hasImpactSnapshot && Number(impactSelected || 0) > 0 ? 'border-emerald-400/30 bg-emerald-400/10 text-emerald-100' : 'border-amber-400/30 bg-amber-400/10 text-amber-100'}`}>
                {!hasImpactSnapshot
                  ? t('automationInterlock.awaitingScan')
                  : Number(impactSelected || 0) > 0
                    ? t('automationInterlock.rebalanceJobsSelected', { count: impactSelected })
                    : t('automationInterlock.noRebalanceJobSelected')}
              </span>
            </div>

            <div className="mt-4 flex flex-col gap-3 md:flex-row md:items-stretch">
              <ImpactStage
                icon={<SignalIcon />}
                value={refillIntents.length}
                label={t('automationInterlock.refillSignalsAvailable')}
                hint={t('automationInterlock.refillSignalsAvailableHint')}
                tone="border-cyan-400/25 bg-cyan-400/[0.07] text-cyan-100"
              />
              <Arrow />
              <ImpactStage
                icon={<ScoreIcon />}
                value={hasImpactSnapshot ? impactInfluenced : '—'}
                label={impactSimulated ? t('automationInterlock.scoresSimulated') : t('automationInterlock.scoresChanged')}
                hint={hasImpactSnapshot
                  ? t('automationInterlock.scoresChangedHint', { delta: integerFormatter.format(totalScoreDelta) })
                  : t('automationInterlock.awaitingScanHint')}
                tone="border-violet-400/25 bg-violet-400/[0.07] text-violet-100"
              />
              <Arrow />
              <ImpactStage
                icon={<QueueIcon />}
                value={hasImpactSnapshot && impactSelected != null ? impactSelected : '—'}
                label={t('automationInterlock.rebalanceJobsAfterGates')}
                hint={t('automationInterlock.selectedAfterGatesHint')}
                tone="border-emerald-400/25 bg-emerald-400/[0.07] text-emerald-100"
              />
            </div>

            <div className="mt-4 flex items-start gap-3 rounded-2xl border border-white/10 bg-black/20 p-4">
              <ShieldIcon className="mt-0.5 h-5 w-5 shrink-0 text-cyan-200" />
              <div>
                <div className="text-sm font-medium text-fog">
                  {hasImpactSnapshot
                    ? t('automationInterlock.rebalanceImpactSummary', {
                        active: refillIntents.length,
                        influenced: impactInfluenced,
                        selected: impactSelected == null ? '—' : impactSelected
                      })
                    : t('automationInterlock.rebalanceImpactUnavailableSummary', { active: refillIntents.length })}
                </div>
                <p className="mt-1 text-xs leading-relaxed text-fog/55">
                  {!hasImpactSnapshot
                    ? t('automationInterlock.impactUnavailableHint')
                    : impactSelected == null
                      ? t('automationInterlock.persistedSelectionUnknown')
                      : impactSelected === 0
                        ? t('automationInterlock.guardrailsHeld')
                        : t('automationInterlock.guardrailsSelected', { count: impactSelected })}
                </p>
              </div>
            </div>
          </article>

          <article className="rounded-2xl border border-amber-400/20 bg-amber-400/[0.035] p-4 sm:p-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div className="flex items-center gap-2 text-amber-100">
                  <ShieldIcon className="h-4 w-4" />
                  <h4 className="font-semibold">{t('automationInterlock.rebalanceToAutofeeImpact')}</h4>
                </div>
                <p className="mt-1 text-xs text-fog/55">
                  {t('automationInterlock.lastFloorDecisionMeta', { date: formatDate(latestFloorEffectAt) })}
                </p>
              </div>
              <span className={`rounded-full border px-3 py-1 text-xs ${hasFloorImpactSnapshot ? 'border-violet-400/30 bg-violet-400/10 text-violet-100' : 'border-amber-400/30 bg-amber-400/10 text-amber-100'}`}>
                {hasFloorImpactSnapshot
                  ? floorImpactSimulated
                    ? t('automationInterlock.autofeeDecisionsSimulated', { count: latestFloorEffectEvents.length })
                    : t('automationInterlock.autofeeDecisionsInfluenced', { count: latestFloorEffectEvents.length })
                  : t('automationInterlock.awaitingAutofeeDecision')}
              </span>
            </div>

            <div className="mt-4 flex flex-col gap-3 md:flex-row md:items-stretch">
              <ImpactStage
                icon={<ShieldIcon />}
                value={floorIntents.length}
                label={t('automationInterlock.activeFeeFloors')}
                hint={t('automationInterlock.activeFeeFloorsHint')}
                tone="border-amber-400/25 bg-amber-400/[0.07] text-amber-100"
              />
              <Arrow />
              <ImpactStage
                icon={<ScoreIcon />}
                value={hasFloorImpactSnapshot ? latestFloorEffectEvents.length : '—'}
                label={floorImpactSimulated ? t('automationInterlock.autofeeDecisionsSimulatedLabel') : t('automationInterlock.autofeeDecisionsInfluencedLabel')}
                hint={t('automationInterlock.autofeeDecisionsInfluencedHint')}
                tone="border-violet-400/25 bg-violet-400/[0.07] text-violet-100"
              />
              <Arrow />
              <ImpactStage
                icon={<QueueIcon />}
                value={hasFloorImpactSnapshot ? floorTargetChanges : '—'}
                label={floorImpactSimulated ? t('automationInterlock.feeTargetsSimulated') : t('automationInterlock.feeTargetsAdjusted')}
                hint={t('automationInterlock.feeTargetsAdjustedHint')}
                tone="border-emerald-400/25 bg-emerald-400/[0.07] text-emerald-100"
              />
            </div>

            <div className="mt-4 flex items-start gap-3 rounded-2xl border border-white/10 bg-black/20 p-4">
              <ShieldIcon className="mt-0.5 h-5 w-5 shrink-0 text-amber-200" />
              <div>
                <div className="text-sm font-medium text-fog">
                  {hasFloorImpactSnapshot
                    ? t('automationInterlock.autofeeImpactSummary', {
                        active: floorIntents.length,
                        influenced: latestFloorEffectEvents.length,
                        adjusted: floorTargetChanges
                      })
                    : t('automationInterlock.autofeeImpactUnavailableSummary', { active: floorIntents.length })}
                </div>
                <p className="mt-1 text-xs leading-relaxed text-fog/55">
                  {hasFloorImpactSnapshot
                    ? floorImpactSimulated
                      ? t('automationInterlock.autofeeImpactShadowHint')
                      : t('automationInterlock.autofeeImpactDecisionHint')
                    : t('automationInterlock.autofeeImpactUnavailableHint')}
                </p>
              </div>
            </div>
          </article>
        </div>
      </section>

      {config && (
        <section className="section-card space-y-4">
          <div>
            <h3 className="text-base font-semibold text-fog">{t('automationInterlock.operatingMode')}</h3>
            <p className="mt-1 text-sm text-fog/60">{t('automationInterlock.operatingModeHint')}</p>
          </div>
          <div className="grid gap-3 md:grid-cols-3">
            {(['off', 'shadow', 'enforce'] as const).map((mode) => (
              <button
                key={mode}
                type="button"
                disabled={saving}
                onClick={() => void saveMode(mode)}
                className={`rounded-2xl border p-4 text-left transition disabled:opacity-50 ${config.mode === mode ? modeTone(mode) : 'border-white/10 bg-black/15 text-fog/65 hover:border-white/20 hover:bg-white/5'}`}
              >
                <span className="text-sm font-semibold">{t(`automationInterlock.modes.${mode}`)}</span>
                <span className="mt-1 block text-xs opacity-75">{t(`automationInterlock.modeHints.${mode}`)}</span>
              </button>
            ))}
          </div>
        </section>
      )}

      <div className="grid gap-4 xl:grid-cols-2">
        <section className="section-card relative overflow-hidden space-y-4">
          <div className="absolute -right-7 -top-7 h-28 w-28 rounded-full bg-cyan-400/5 blur-2xl" />
          <div className="relative flex items-start justify-between gap-3">
            <div>
              <h3 className="font-semibold text-fog">{t('automationInterlock.autofeeToRebalance')}</h3>
              <p className="mt-1 text-xs text-fog/55">{t('automationInterlock.autofeeToRebalanceHint')}</p>
            </div>
            <a href="#fee-center" className="text-xs text-cyan-200 hover:text-cyan-100">{t('nav.feeCenter')}</a>
          </div>
          <div className="relative grid grid-cols-2 gap-3 text-sm">
            <div><span className="text-fog/45">{t('automationInterlock.profile')}</span><div>{autofee?.profile || t('common.unknown')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.status')}</span><div>{autofee?.enabled ? t('common.enabled') : t('common.inactive')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.operationMode')}</span><div>{autofee?.operation_mode || t('common.unknown')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.lastRun')}</span><div>{formatDate(autofeeStatus?.last_run_at)}</div></div>
          </div>
          <div className="relative flex items-center gap-2 rounded-xl border border-cyan-400/15 bg-cyan-400/5 px-3 py-2 text-xs text-cyan-100/75">
            <SignalIcon className="h-4 w-4" />
            {t('automationInterlock.producingSignals', { count: refillIntents.length })}
          </div>
        </section>

        <section className="section-card relative overflow-hidden space-y-4">
          <div className="absolute -right-7 -top-7 h-28 w-28 rounded-full bg-amber-400/5 blur-2xl" />
          <div className="relative flex items-start justify-between gap-3">
            <div>
              <h3 className="font-semibold text-fog">{t('automationInterlock.rebalanceToAutofee')}</h3>
              <p className="mt-1 text-xs text-fog/55">{t('automationInterlock.rebalanceToAutofeeHint')}</p>
            </div>
            <a href="#rebalance-center" className="text-xs text-cyan-200 hover:text-cyan-100">{t('nav.rebalanceCenter')}</a>
          </div>
          <div className="relative grid grid-cols-2 gap-3 text-sm">
            <div><span className="text-fog/45">{t('automationInterlock.profile')}</span><div>{rebalance?.profile || t('common.unknown')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.status')}</span><div>{rebalance?.auto_enabled ? t('common.enabled') : t('common.inactive')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.scheduler')}</span><div>{rebalance?.scheduler_mode || t('common.unknown')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.nodeClass')}</span><div>{rebalance?.node_calibration?.node_class || t('common.unknown')}</div></div>
          </div>
          <div className="relative flex items-center gap-2 rounded-xl border border-amber-400/15 bg-amber-400/5 px-3 py-2 text-xs text-amber-100/75">
            <ShieldIcon className="h-4 w-4" />
            {t('automationInterlock.producingFloors', { count: floorIntents.length })}
          </div>
        </section>
      </div>

      <section className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="font-semibold text-fog">{t('automationInterlock.activeTitle')}</h3>
            <p className="mt-1 text-xs text-fog/55">{t('automationInterlock.provenanceHint')}</p>
          </div>
          <div className="flex items-center gap-2">
            <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-xs text-fog/60">
              {t('automationInterlock.activeCount', { count: intents.length })}
            </span>
            <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-xs text-fog/60">
              {t('automationInterlock.averageConfidenceValue', { value: formatPercent(averageConfidence) })}
            </span>
          </div>
        </div>
        {intents.length === 0 ? (
          <p className="text-sm text-fog/60">{t('automationInterlock.noActive')}</p>
        ) : (
          <div className="max-h-[38rem] overflow-y-scroll overscroll-contain pr-2">
            <div className="grid gap-3 xl:grid-cols-2">
              {intents.map((intent) => {
                const alias = aliasForIntent(intent)
                const decision = decisionForIntent(intent)
                const persistedEffect = effectByIntentID.get(intent.id)
                const appliedEffect = appliedByIntentID.get(intent.id)
                const floorEffect = intent.kind === 'protect_fee_floor' ? appliedEffect : undefined
                const outRatio = Math.max(0, Math.min(1, Number(intent.evidence?.out_ratio || 0)))
                const localPpm = Number(intent.evidence?.local_ppm)
                const targetPpm = Number(intent.evidence?.target_ppm)
                const scoreChanged = Boolean(decision?.intent_kind || persistedEffect)
                const scoreBefore = decision?.intent_score_before ?? metadataNumber(persistedEffect?.metadata, 'score_before') ?? 0
                const scoreAfter = decision?.intent_score_after ?? metadataNumber(persistedEffect?.metadata, 'score_after') ?? 0
                const effectReason = decision?.reason || metadataString(persistedEffect?.metadata, 'reason')
                const floorBefore = metadataNumber(appliedEffect?.metadata, 'ppm_before')
                const floorAfter = metadataNumber(appliedEffect?.metadata, 'ppm_after')
                const intentInfluenced = scoreChanged || Boolean(floorEffect)
                const stateExplanation = intent.kind === 'refill_target'
                  ? scoreChanged
                    ? t('automationInterlock.intentHelp.refillInfluenced', {
                        before: integerFormatter.format(scoreBefore),
                        after: integerFormatter.format(scoreAfter),
                        reason: effectReason ? reasonLabel(effectReason) : t('automationInterlock.intentHelp.stillSubjectToGates')
                      })
                    : !hasImpactSnapshot
                      ? t('automationInterlock.intentHelp.waitingRebalanceScan')
                      : t('automationInterlock.intentHelp.refillNotChanged', {
                          reason: effectReason ? reasonLabel(effectReason) : t('automationInterlock.intentHelp.noMeasurableDelta')
                        })
                  : floorEffect
                    ? t('automationInterlock.intentHelp.floorInfluenced', {
                        before: floorBefore == null ? t('common.na') : integerFormatter.format(floorBefore),
                        after: floorAfter == null ? t('common.na') : integerFormatter.format(floorAfter)
                      })
                    : t('automationInterlock.intentHelp.waitingAutofeeDecision')
                const intentExplanation = [
                  t(`automationInterlock.intentHelp.kinds.${intent.kind}`),
                  stateExplanation,
                  t(`automationInterlock.intentHelp.modes.${config?.mode || 'off'}`)
                ].join(' ')
                return (
                  <article key={intent.id} className="rounded-2xl border border-white/10 bg-black/15 p-4 transition hover:border-cyan-300/20 hover:bg-white/[0.025]">
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className={`rounded-full border px-2 py-0.5 text-[10px] ${kindTone(intent.kind)}`}>
                            {t(`automationInterlock.kinds.${intent.kind}`)}
                          </span>
                          <span className={`rounded-full border px-2 py-0.5 text-[10px] ${intentInfluenced ? 'border-violet-400/25 bg-violet-400/10 text-violet-100' : 'border-white/10 bg-white/5 text-fog/50'}`}>
                            {intentInfluenced ? t('automationInterlock.influencedDecision') : t('automationInterlock.signalOnly')}
                          </span>
                          <InfoTooltip
                            title={t('automationInterlock.intentHelp.title', { alias })}
                            text={intentExplanation}
                          />
                        </div>
                        <a
                          href={intent.channel_point ? `#rebalance-center?channel_point=${encodeURIComponent(intent.channel_point)}` : '#rebalance-center'}
                          className="mt-2 block truncate text-sm font-semibold text-fog hover:text-cyan-100"
                          title={alias}
                        >
                          {alias}
                        </a>
                        <div className="mt-0.5 text-[10px] text-fog/35" title={intent.channel_point || intent.channel_id_str}>
                          {shortChannelPoint(intent.channel_point) || intent.channel_id_str || t('common.unknown')}
                        </div>
                      </div>
                      <div className="text-right text-[11px] text-fog/50">
                        <div>{t('automationInterlock.confidence', { value: formatPercent(intent.confidence) })}</div>
                        <div>{t('automationInterlock.expires', { value: formatDate(intent.expires_at) })}</div>
                      </div>
                    </div>

                    {intent.kind === 'refill_target' && (
                      <div className="mt-3 rounded-xl border border-white/5 bg-white/[0.025] p-3">
                        <div className="flex items-center justify-between text-[11px] text-fog/55">
                          <span>{t('automationInterlock.outboundLiquidity')}</span>
                          <span className="font-medium text-fog">{formatPercent(outRatio)}</span>
                        </div>
                        <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-white/5">
                          <div className="h-full rounded-full bg-gradient-to-r from-rose-400 to-amber-300" style={{ width: `${Math.max(2, outRatio * 100)}%` }} />
                        </div>
                        <div className="mt-2 flex flex-wrap justify-between gap-2 text-[10px] text-fog/45">
                          <span>{t('automationInterlock.liquidityState', { value: String(intent.evidence?.liquidity_state || t('common.unknown')) })}</span>
                          {Number.isFinite(localPpm) && Number.isFinite(targetPpm) && (
                            <span>{t('automationInterlock.feeMovement', { before: localPpm, after: targetPpm })}</span>
                          )}
                        </div>
                      </div>
                    )}

                    <div className="mt-3 grid gap-2 text-[11px] text-fog/55 sm:grid-cols-2">
                      <div>{t('automationInterlock.producerProfile', { value: intent.producer_profile || t('common.unknown') })}</div>
                      <div>{t('automationInterlock.calibration', { node: intent.producer_node_class || t('common.unknown'), liquidity: intent.producer_liquidity_class || t('common.unknown') })}</div>
                    </div>

                    <div className={`mt-3 rounded-xl border px-3 py-2 text-xs ${intentInfluenced ? 'border-violet-400/20 bg-violet-400/[0.07] text-violet-100' : 'border-white/5 bg-white/[0.02] text-fog/55'}`}>
                      {scoreChanged ? (
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span>{t('automationInterlock.scoreEffect')}</span>
                          <span className="font-semibold">
                            {integerFormatter.format(scoreBefore)} → {integerFormatter.format(scoreAfter)} sats
                          </span>
                        </div>
                      ) : floorEffect ? (
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span>{metadataBoolean(floorEffect.metadata, 'shadow') || metadataBoolean(floorEffect.metadata, 'dry_run') ? t('automationInterlock.feeTargetSimulatedEffect') : t('automationInterlock.feeTargetEffect')}</span>
                          <span className="font-semibold">
                            {floorBefore == null ? t('common.na') : integerFormatter.format(floorBefore)} → {floorAfter == null ? t('common.na') : integerFormatter.format(floorAfter)} ppm
                          </span>
                        </div>
                      ) : (
                        <span>{reasonLabel(effectReason)}</span>
                      )}
                      {scoreChanged && effectReason && (
                        <div className="mt-1 text-[10px] opacity-60">{reasonLabel(effectReason)}</div>
                      )}
                    </div>
                  </article>
                )
              })}
            </div>
          </div>
        )}
      </section>

      <section className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="font-semibold text-fog">{t('automationInterlock.historyTitle')}</h3>
            <p className="mt-1 text-xs text-fog/55">{t('automationInterlock.historyHint')}</p>
          </div>
          <span className="rounded-full border border-white/10 bg-white/5 px-2.5 py-1 text-xs text-fog/60">
            {t('automationInterlock.eventsCount', { count: history.length })}
          </span>
        </div>
        {history.length === 0 ? (
          <p className="text-sm text-fog/60">{t('automationInterlock.noHistory')}</p>
        ) : (
          <div className="h-[28rem] overflow-y-scroll overscroll-contain pr-2">
            <div className="relative space-y-1 before:absolute before:bottom-3 before:left-[0.45rem] before:top-3 before:w-px before:bg-white/10">
              {history.map((event) => {
                const before = metadataNumber(event.metadata, 'score_before') ?? metadataNumber(event.metadata, 'ppm_before')
                const after = metadataNumber(event.metadata, 'score_after') ?? metadataNumber(event.metadata, 'ppm_after')
                const eventLabel = event.event_type === 'applied'
                  ? t(`automationInterlock.appliedEvents.${event.kind}`, { defaultValue: t('automationInterlock.events.applied') })
                  : t(`automationInterlock.events.${event.event_type}`, { defaultValue: event.event_type })
                return (
                  <div key={event.id} className="relative grid gap-2 py-3 pl-7 text-xs sm:grid-cols-[10rem_1fr_auto] sm:items-center">
                    <span className={`absolute left-0 top-[1.1rem] h-4 w-4 rounded-full border-4 border-ink ${event.event_type === 'applied' ? 'bg-violet-300' : event.event_type === 'resolved' ? 'bg-fog/40' : 'bg-cyan-300'}`} />
                    <span className="text-fog/40">{formatDate(event.occurred_at)}</span>
                    <div className="min-w-0">
                      <div className="truncate text-fog/75">
                        <span className="font-medium text-fog">{aliasForEvent(event)}</span>
                        {' · '}{eventLabel}
                        {' · '}{t(`automationInterlock.kinds.${event.kind}`)}
                      </div>
                      <div className="mt-0.5 text-[10px] text-fog/35">
                        {event.channel_id_str || String(event.channel_id || '')}
                      </div>
                    </div>
                    <div className="text-right text-fog/40">
                      {before != null && after != null
                        ? <span className="text-violet-200">{integerFormatter.format(before)} → {integerFormatter.format(after)}</span>
                        : <span>{event.producer} → {event.consumer}</span>}
                    </div>
                  </div>
                )
              })}
            </div>
          </div>
        )}
      </section>

      {config && (
        <details className="section-card group">
          <summary className="cursor-pointer list-none font-semibold text-fog">{t('automationInterlock.advanced')}</summary>
          <p className="mt-2 text-xs text-fog/55">{t('automationInterlock.advancedHint')}</p>
          {calibrationPreviewReady && (
            <div className={`mt-4 rounded-2xl border p-4 ${combinedTone}`}>
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <div className="text-xs font-semibold uppercase tracking-[0.14em] opacity-65">{t('automationInterlock.combinedEffect')}</div>
                  <div className="mt-1 text-sm font-medium">
                    {t('automationInterlock.combinedFormula', {
                      intent: numberFormatter.format(admittedMultiplier),
                      settling: numberFormatter.format(settlingEnabled ? parsedSettlingMultiplier : 1),
                      combined: numberFormatter.format(combinedMultiplier)
                    })}
                  </div>
                </div>
                <span className="rounded-full border border-current/20 bg-black/10 px-3 py-1 text-xs font-semibold">
                  {combinedMultiplier < 1
                    ? t('automationInterlock.netDemotion')
                    : t('automationInterlock.netPriority')}
                </span>
              </div>
              <p className="mt-2 text-[11px] leading-relaxed opacity-70">
                {settlingEnabled
                  ? t('automationInterlock.combinedEffectHint', { hours: numberFormatter.format(parsedSettlingWindowHours) })
                  : t('automationInterlock.settlingDisabledHint')}
              </p>
            </div>
          )}

          <div className="mt-4 grid gap-4 xl:grid-cols-2">
            <section className="rounded-2xl border border-cyan-300/15 bg-cyan-300/[0.035] p-4">
              <h4 className="text-sm font-semibold text-cyan-100">{t('automationInterlock.intentAdmission')}</h4>
              <p className="mt-1 text-[11px] leading-relaxed text-fog/50">{t('automationInterlock.intentAdmissionHint')}</p>
              <div className="mt-4 grid gap-4 sm:grid-cols-2">
                <label className="space-y-1 text-xs text-fog/60">
                  <span>{t('automationInterlock.refillMultiplier')}</span>
                  <code className="block text-[10px] text-fog/35">refill_score_multiplier</code>
                  <input className="input-field w-full" type="number" min="1" max="1.5" step="0.05" value={multiplier} disabled={saving} onChange={(event) => setMultiplier(event.target.value)} />
                </label>
                <label className="space-y-1 text-xs text-fog/60">
                  <span>{t('automationInterlock.minConfidence')}</span>
                  <code className="block text-[10px] text-fog/35">min_confidence</code>
                  <input className="input-field w-full" type="number" min="0.5" max="1" step="0.05" value={minConfidence} disabled={saving} onChange={(event) => setMinConfidence(event.target.value)} />
                </label>
              </div>
            </section>

            <section className="rounded-2xl border border-amber-300/15 bg-amber-300/[0.035] p-4">
              <h4 className="text-sm font-semibold text-amber-100">{t('automationInterlock.settlingGuardrail')}</h4>
              <p className="mt-1 text-[11px] leading-relaxed text-fog/50">{t('automationInterlock.settlingGuardrailHint')}</p>
              {!rebalanceConfig && <p className="mt-2 text-[11px] text-amber-200">{t('automationInterlock.rebalanceConfigUnavailable')}</p>}
              <div className="mt-4 grid gap-4 sm:grid-cols-2">
                <label className="space-y-1 text-xs text-fog/60">
                  <span>{t('automationInterlock.settlingMultiplier')}</span>
                  <code className="block text-[10px] text-fog/35">autofee_settling_multiplier</code>
                  <input className="input-field w-full" type="number" min="0.05" max="1" step="0.05" value={settlingMultiplier} disabled={saving || !rebalanceConfig} onChange={(event) => setSettlingMultiplier(event.target.value)} />
                </label>
                <label className="space-y-1 text-xs text-fog/60">
                  <span>{t('automationInterlock.settlingWindow')}</span>
                  <code className="block text-[10px] text-fog/35">autofee_settling_window_sec</code>
                  <input className="input-field w-full" type="number" min="0" step="0.5" value={settlingWindowHours} disabled={saving || !rebalanceConfig} onChange={(event) => setSettlingWindowHours(event.target.value)} />
                </label>
              </div>
            </section>
          </div>

          <section className="mt-4 rounded-2xl border border-white/[0.07] bg-black/10 p-4">
            <h4 className="text-sm font-semibold text-fog">{t('automationInterlock.operationalWindows')}</h4>
            <p className="mt-1 text-[11px] text-fog/45">{t('automationInterlock.operationalWindowsHint')}</p>
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              <div className="rounded-xl border border-white/[0.06] bg-white/[0.025] px-3 py-2 text-xs text-fog/60">
                <span>{t('automationInterlock.refillTtl')}</span>
                <div className="mt-1 text-sm font-medium text-fog">{formatDuration(config.refill_target_ttl_sec)}</div>
              </div>
              <div className="rounded-xl border border-white/[0.06] bg-white/[0.025] px-3 py-2 text-xs text-fog/60">
                <span>{t('automationInterlock.floorTtl')}</span>
                <div className="mt-1 text-sm font-medium text-fog">{formatDuration(config.protect_fee_floor_ttl_sec)}</div>
              </div>
            </div>
          </section>

          <div className="mt-4 flex flex-wrap items-center gap-3">
            <button type="button" className="btn-primary" disabled={saving || !rebalanceConfig} onClick={() => void saveAdvanced()}>{t('common.save')}</button>
            <span className="text-[11px] text-fog/45">{t('automationInterlock.saveCalibrationHint')}</span>
          </div>
        </details>
      )}
    </section>
  )
}
