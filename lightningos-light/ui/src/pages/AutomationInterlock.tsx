import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getAutofeeConfig,
  getAutofeeStatus,
  getAutomationIntentConfig,
  getAutomationIntentHistory,
  getAutomationIntents,
  getRebalanceOverview,
  updateAutomationIntentConfig
} from '../api'
import type { AutomationIntent, AutomationIntentConfig, AutomationIntentEvent } from '../api'
import type { RebalanceOverview } from '../components/rebalance/types'
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

const modeTone = (mode: AutomationMode) => {
  if (mode === 'enforce') return 'border-emerald-400/35 bg-emerald-400/10 text-emerald-100'
  if (mode === 'shadow') return 'border-cyan-400/35 bg-cyan-400/10 text-cyan-100'
  return 'border-white/10 bg-white/5 text-fog/60'
}

const kindTone = (kind: AutomationIntent['kind']) =>
  kind === 'refill_target'
    ? 'border-cyan-400/30 bg-cyan-400/10 text-cyan-100'
    : 'border-amber-400/30 bg-amber-400/10 text-amber-100'

export default function AutomationInterlock() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }), [locale])
  const dateFormatter = useMemo(
    () => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    [locale]
  )

  const [config, setConfig] = useState<AutomationIntentConfig | null>(null)
  const [intents, setIntents] = useState<AutomationIntent[]>([])
  const [history, setHistory] = useState<AutomationIntentEvent[]>([])
  const [autofee, setAutofee] = useState<AutofeeSummary | null>(null)
  const [autofeeStatus, setAutofeeStatus] = useState<AutofeeStatus | null>(null)
  const [rebalance, setRebalance] = useState<RebalanceOverview | null>(null)
  const [multiplier, setMultiplier] = useState('1.2')
  const [minConfidence, setMinConfidence] = useState('0.7')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')

  const applyConfig = (next: AutomationIntentConfig) => {
    setConfig(next)
    setMultiplier(String(next.refill_score_multiplier))
    setMinConfidence(String(next.min_confidence))
  }

  const load = async (silent = false) => {
    if (silent) setRefreshing(true)
    else setLoading(true)
    const results = await Promise.allSettled([
      getAutomationIntentConfig(),
      getAutomationIntents(true),
      getAutomationIntentHistory(200),
      getAutofeeConfig(),
      getAutofeeStatus(),
      getRebalanceOverview()
    ])
    const [configResult, intentsResult, historyResult, autofeeResult, autofeeStatusResult, rebalanceResult] = results
    if (configResult.status === 'fulfilled') {
      applyConfig(configResult.value)
      setMessage('')
    } else {
      setMessage(configResult.reason instanceof Error ? configResult.reason.message : t('automationInterlock.loadFailed'))
    }
    if (intentsResult.status === 'fulfilled') setIntents(intentsResult.value.items || [])
    if (historyResult.status === 'fulfilled') setHistory(historyResult.value.items || [])
    if (autofeeResult.status === 'fulfilled') setAutofee(autofeeResult.value as AutofeeSummary)
    if (autofeeStatusResult.status === 'fulfilled') setAutofeeStatus(autofeeStatusResult.value as AutofeeStatus)
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
    if (!Number.isFinite(nextMultiplier) || nextMultiplier < 1 || nextMultiplier > 1.5 ||
        !Number.isFinite(nextConfidence) || nextConfidence < 0.5 || nextConfidence > 1) {
      setMessage(t('automationInterlock.invalidSettings'))
      return
    }
    setSaving(true)
    setMessage('')
    try {
      applyConfig(await updateAutomationIntentConfig({
        refill_score_multiplier: nextMultiplier,
        min_confidence: nextConfidence
      }))
      setMessage(t('automationInterlock.settingsSaved'))
    } catch (err) {
      setMessage(err instanceof Error ? err.message : t('automationInterlock.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  const refillIntents = intents.filter((item) => item.kind === 'refill_target')
  const floorIntents = intents.filter((item) => item.kind === 'protect_fee_floor')
  const averageConfidence = intents.length
    ? intents.reduce((sum, item) => sum + Number(item.confidence || 0), 0) / intents.length
    : 0

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

      {config && (
        <div className="section-card space-y-4">
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
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {[
          [t('automationInterlock.activeIntents'), intents.length],
          [t('automationInterlock.refillTargets'), refillIntents.length],
          [t('automationInterlock.feeFloors'), floorIntents.length],
          [t('automationInterlock.averageConfidence'), formatPercent(averageConfidence)]
        ].map(([label, value]) => (
          <div key={String(label)} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
            <div className="text-xs uppercase tracking-[0.16em] text-fog/45">{label}</div>
            <div className="mt-2 text-2xl font-semibold text-fog">{value}</div>
          </div>
        ))}
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <div className="section-card space-y-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="font-semibold text-fog">{t('automationInterlock.autofeeToRebalance')}</h3>
              <p className="mt-1 text-xs text-fog/55">{t('automationInterlock.autofeeToRebalanceHint')}</p>
            </div>
            <a href="#fee-center" className="text-xs text-cyan-200 hover:text-cyan-100">{t('nav.feeCenter')}</a>
          </div>
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div><span className="text-fog/45">{t('automationInterlock.profile')}</span><div>{autofee?.profile || t('common.unknown')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.status')}</span><div>{autofee?.enabled ? t('common.enabled') : t('common.inactive')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.operationMode')}</span><div>{autofee?.operation_mode || t('common.unknown')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.lastRun')}</span><div>{formatDate(autofeeStatus?.last_run_at)}</div></div>
          </div>
        </div>

        <div className="section-card space-y-4">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h3 className="font-semibold text-fog">{t('automationInterlock.rebalanceToAutofee')}</h3>
              <p className="mt-1 text-xs text-fog/55">{t('automationInterlock.rebalanceToAutofeeHint')}</p>
            </div>
            <a href="#rebalance-center" className="text-xs text-cyan-200 hover:text-cyan-100">{t('nav.rebalanceCenter')}</a>
          </div>
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div><span className="text-fog/45">{t('automationInterlock.profile')}</span><div>{rebalance?.profile || t('common.unknown')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.status')}</span><div>{rebalance?.auto_enabled ? t('common.enabled') : t('common.inactive')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.scheduler')}</span><div>{rebalance?.scheduler_mode || t('common.unknown')}</div></div>
            <div><span className="text-fog/45">{t('automationInterlock.nodeClass')}</span><div>{rebalance?.node_calibration?.node_class || t('common.unknown')}</div></div>
          </div>
        </div>
      </div>

      <div className="section-card space-y-4">
        <div>
          <h3 className="font-semibold text-fog">{t('automationInterlock.activeTitle')}</h3>
          <p className="mt-1 text-xs text-fog/55">{t('automationInterlock.provenanceHint')}</p>
        </div>
        {intents.length === 0 ? (
          <p className="text-sm text-fog/60">{t('automationInterlock.noActive')}</p>
        ) : (
          <div className="grid gap-3 xl:grid-cols-2">
            {intents.map((intent) => (
              <article key={intent.id} className="rounded-2xl border border-white/10 bg-black/15 p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <span className={`rounded-full border px-2 py-0.5 text-[11px] ${kindTone(intent.kind)}`}>
                      {t(`automationInterlock.kinds.${intent.kind}`)}
                    </span>
                    <div className="mt-2 text-sm font-medium text-fog">
                      {t('automationInterlock.channel', { value: intent.channel_point || intent.channel_id })}
                    </div>
                  </div>
                  <div className="text-right text-xs text-fog/55">
                    <div>{t('automationInterlock.confidence', { value: formatPercent(intent.confidence) })}</div>
                    <div>{t('automationInterlock.expires', { value: formatDate(intent.expires_at) })}</div>
                  </div>
                </div>
                <div className="mt-3 grid gap-2 text-xs text-fog/60 sm:grid-cols-2">
                  <div>{t('automationInterlock.flow', { producer: intent.producer, consumer: intent.consumer })}</div>
                  <div>{t('automationInterlock.reason', { value: intent.reason_code })}</div>
                  <div>{t('automationInterlock.producerProfile', { value: intent.producer_profile || t('common.unknown') })}</div>
                  <div>{t('automationInterlock.calibration', { node: intent.producer_node_class || t('common.unknown'), liquidity: intent.producer_liquidity_class || t('common.unknown') })}</div>
                  {intent.score_multiplier != null && <div>{t('automationInterlock.scoreMultiplier', { value: numberFormatter.format(intent.score_multiplier) })}</div>}
                  {intent.fee_floor_ppm != null && <div>{t('automationInterlock.feeFloor', { value: numberFormatter.format(intent.fee_floor_ppm) })}</div>}
                </div>
              </article>
            ))}
          </div>
        )}
      </div>

      {config && (
        <details className="section-card group">
          <summary className="cursor-pointer list-none font-semibold text-fog">{t('automationInterlock.advanced')}</summary>
          <p className="mt-2 text-xs text-fog/55">{t('automationInterlock.advancedHint')}</p>
          <div className="mt-4 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <label className="space-y-1 text-xs text-fog/60">
              <span>{t('automationInterlock.refillMultiplier')}</span>
              <input className="input-field w-full" type="number" min="1" max="1.5" step="0.05" value={multiplier} onChange={(event) => setMultiplier(event.target.value)} />
            </label>
            <label className="space-y-1 text-xs text-fog/60">
              <span>{t('automationInterlock.minConfidence')}</span>
              <input className="input-field w-full" type="number" min="0.5" max="1" step="0.05" value={minConfidence} onChange={(event) => setMinConfidence(event.target.value)} />
            </label>
            <div className="text-xs text-fog/60"><span>{t('automationInterlock.refillTtl')}</span><div className="mt-2 text-sm text-fog">{formatDuration(config.refill_target_ttl_sec)}</div></div>
            <div className="text-xs text-fog/60"><span>{t('automationInterlock.floorTtl')}</span><div className="mt-2 text-sm text-fog">{formatDuration(config.protect_fee_floor_ttl_sec)}</div></div>
          </div>
          <button type="button" className="btn-primary mt-4" disabled={saving} onClick={() => void saveAdvanced()}>{t('common.save')}</button>
        </details>
      )}

      <div className="section-card space-y-4">
        <div>
          <h3 className="font-semibold text-fog">{t('automationInterlock.historyTitle')}</h3>
          <p className="mt-1 text-xs text-fog/55">{t('automationInterlock.historyHint')}</p>
        </div>
        {history.length === 0 ? (
          <p className="text-sm text-fog/60">{t('automationInterlock.noHistory')}</p>
        ) : (
          <div className="max-h-[32rem] divide-y divide-white/5 overflow-y-auto">
            {history.map((event) => (
              <div key={event.id} className="grid gap-1 py-3 text-xs sm:grid-cols-[9rem_1fr_auto] sm:items-center">
                <span className="text-fog/45">{formatDate(event.occurred_at)}</span>
                <span className="text-fog/70">
                  {t('automationInterlock.historyEvent', {
                    event: t(`automationInterlock.events.${event.event_type}`, { defaultValue: event.event_type }),
                    kind: t(`automationInterlock.kinds.${event.kind}`),
                    channel: event.channel_id
                  })}
                </span>
                <span className="text-fog/45">{event.producer} → {event.consumer}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </section>
  )
}
