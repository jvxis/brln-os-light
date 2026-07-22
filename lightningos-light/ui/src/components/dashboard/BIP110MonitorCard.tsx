import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import { formatPercent, formatSats, formatTimestamp } from './formatters'
import HorizontalBarGauge from './HorizontalBarGauge'
import StatusBadge from './StatusBadge'
import type { BIP110MonitorStatus, BIP110SourceStatus, Tone } from './types'

type BIP110MonitorCardProps = {
  status: BIP110MonitorStatus | null
  loading: boolean
}

const riskTone = (risk?: string): Tone => {
  if (risk === 'high') return 'danger'
  if (risk === 'elevated' || risk === 'watch') return 'warn'
  if (risk === 'normal') return 'ok'
  return 'muted'
}

const comparisonTone = (value?: string): Tone => {
  if (value === 'matched') return 'ok'
  if (value === 'signal_mismatch') return 'danger'
  if (value === 'tip_mismatch') return 'warn'
  return 'muted'
}

type SourcePanelProps = {
  label: string
  source: BIP110SourceStatus
  locale: string
}

function SourcePanel({ label, source, locale }: SourcePanelProps) {
  const { t } = useTranslation()
  return (
    <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="font-medium text-fog/90">{label}</p>
        <StatusBadge
          label={source.available ? t('bip110.available') : t('common.unavailable')}
          tone={source.available ? 'ok' : 'warn'}
        />
      </div>
      {source.available ? (
        <div className="mt-4 grid gap-2 text-sm sm:grid-cols-2">
          <div className="flex items-center justify-between gap-3">
            <span className="text-fog/55">{t('bip110.tip')}</span>
            <span>{formatSats(locale, source.tip)}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="text-fog/55">{t('bip110.period')}</span>
            <span>{formatSats(locale, source.period_num)}</span>
          </div>
          <div className="flex items-center justify-between gap-3 sm:col-span-2">
            <span className="text-fog/55">{t('bip110.signalingBlocks')}</span>
            <span>{formatSats(locale, source.signaling_count)} / {formatSats(locale, source.total_blocks)}</span>
          </div>
          <div className="flex items-center justify-between gap-3 sm:col-span-2">
            <span className="text-fog/55">{t('bip110.signalingRate')}</span>
            <span>{formatPercent(locale, source.pct, 2)}%</span>
          </div>
        </div>
      ) : (
        <p className="mt-3 text-xs text-amber-200/80 [overflow-wrap:anywhere]">{source.error || t('bip110.sourceUnavailable')}</p>
      )}
    </div>
  )
}

export default function BIP110MonitorCard({ status, loading }: BIP110MonitorCardProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const displaySource = status?.internal?.available ? status.internal : status?.public
  const signalPct = displaySource?.pct ?? 0
  const tone = riskTone(status?.risk_level)
  const comparisonStatus = status?.comparison?.status || 'unavailable'

  return (
    <article className="section-card">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="max-w-3xl">
          <div className="flex flex-wrap items-center gap-2">
            <p className="text-xs uppercase tracking-[0.28em] text-sky-300/80">{t('bip110.kicker')}</p>
            <StatusBadge label={t('bip110.informational')} tone="info" />
          </div>
          <h3 className="mt-3 text-xl font-semibold">{t('bip110.title')}</h3>
          <p className="mt-2 text-sm text-fog/65">{t('bip110.subtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <StatusBadge
            label={status ? t(`bip110.risk.${status.risk_level}`) : t('common.unavailable')}
            tone={tone}
            size="md"
          />
          <StatusBadge
            label={t(`bip110.comparison.${comparisonStatus}`)}
            tone={comparisonTone(comparisonStatus)}
            size="md"
          />
        </div>
      </div>

      {status ? (
        <div className="mt-6 space-y-5">
          <HorizontalBarGauge
            label={t('bip110.thresholdProgress')}
            value={signalPct}
            max={status.threshold_pct || 55}
            valueLabel={`${formatPercent(locale, signalPct, 2)}% / ${formatPercent(locale, status.threshold_pct, 0)}%`}
            detail={t('bip110.thresholdDetail', { value: formatSats(locale, status.threshold_count) })}
            tone={tone}
          />

          <div className="grid gap-3 lg:grid-cols-3">
            <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
              <p className="text-sm text-fog/55">{t('bip110.phaseLabel')}</p>
              <p className="mt-2 font-semibold">{t(`bip110.phase.${status.phase}`)}</p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
              <p className="text-sm text-fog/55">{t('bip110.blocksToMandatory')}</p>
              <p className="mt-2 font-semibold">{formatSats(locale, status.blocks_to_mandatory)}</p>
              <p className="mt-1 text-xs text-fog/45">#{formatSats(locale, status.mandatory_start_height)}</p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
              <p className="text-sm text-fog/55">{t('bip110.lastComparison')}</p>
              <p className="mt-2 font-semibold">{formatTimestamp(locale, status.checked_at)}</p>
            </div>
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <SourcePanel label={t('bip110.internalSource')} source={status.internal} locale={locale} />
            <SourcePanel label={t('bip110.publicSource')} source={status.public} locale={locale} />
          </div>

          <details className="rounded-2xl border border-white/10 bg-white/5 p-4">
            <summary className="cursor-pointer text-sm text-fog/75">{t('bip110.moreDetails')}</summary>
            <div className="mt-4 grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
              <div><span className="text-fog/50">{t('bip110.backend')}:</span> <span className="[overflow-wrap:anywhere]">{status.internal.subversion || status.internal.source}</span></div>
              <div><span className="text-fog/50">{t('bip110.lockIn')}:</span> #{formatSats(locale, status.lock_in_height)}</div>
              <div><span className="text-fog/50">{t('bip110.activation')}:</span> #{formatSats(locale, status.activation_height)}</div>
              <div><span className="text-fog/50">{t('bip110.tipDelta')}:</span> {formatSats(locale, status.comparison.tip_delta)}</div>
              <div><span className="text-fog/50">{t('bip110.signalDelta')}:</span> {formatSats(locale, status.comparison.signaling_count_delta)}</div>
              <div><span className="text-fog/50">{t('bip110.publicUpdated')}:</span> {formatTimestamp(locale, status.public.updated_at)}</div>
            </div>
            <p className="mt-4 text-xs text-fog/50">{t('bip110.disclaimer')}</p>
            <a className="mt-3 inline-flex text-xs font-medium text-sky-300 hover:text-sky-200" href="https://bip110monitor.com/" target="_blank" rel="noreferrer">
              {t('bip110.openPublicMonitor')}
            </a>
          </details>
        </div>
      ) : (
        <p className="mt-5 text-sm text-fog/60">{loading ? t('bip110.loading') : t('bip110.unavailable')}</p>
      )}
    </article>
  )
}
