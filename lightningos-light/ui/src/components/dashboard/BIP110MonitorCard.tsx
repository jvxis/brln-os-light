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
    <details className="section-card group">
      <summary className="cursor-pointer list-none [&::-webkit-details-marker]:hidden">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <span className="text-xs uppercase tracking-[0.24em] text-sky-300/80">{t('bip110.kicker')}</span>
            <h3 className="font-semibold">{t('bip110.title')}</h3>
            <StatusBadge label={t('bip110.informational')} tone="info" />
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge
              label={status ? t(`bip110.risk.${status.risk_level}`) : t('common.unavailable')}
              tone={tone}
            />
            <StatusBadge
              label={t(`bip110.comparison.${comparisonStatus}`)}
              tone={comparisonTone(comparisonStatus)}
            />
            <span className="ml-1 text-sm text-fog/55 transition-transform group-open:rotate-180" aria-hidden="true">⌄</span>
          </div>
        </div>

        <div className="mt-3 grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-[1.25fr_1fr_1fr_1.35fr_auto] lg:items-center">
          {status ? (
            <>
              <div className="min-w-0 truncate">
                <span className="text-fog/50">{t('bip110.phaseLabel')}:</span>{' '}
                <span className="font-medium text-fog/90">{t(`bip110.phase.${status.phase}`)}</span>
              </div>
              <div className="min-w-0 truncate">
                <span className="text-fog/50">{t('bip110.signalingRate')}:</span>{' '}
                <span className="font-medium text-fog/90">{formatPercent(locale, signalPct, 2)}% / {formatPercent(locale, status.threshold_pct, 0)}%</span>
              </div>
              <div className="min-w-0 truncate">
                <span className="text-fog/50">{t('bip110.mandatoryIn')}:</span>{' '}
                <span className="font-medium text-fog/90">{formatSats(locale, status.blocks_to_mandatory)}</span>
              </div>
              <div className="min-w-0 truncate">
                <span className="text-fog/50">{t('bip110.lastComparison')}:</span>{' '}
                <span className="font-medium text-fog/90">{formatTimestamp(locale, status.checked_at)}</span>
              </div>
              <span className="text-right text-xs font-medium text-sky-300">{t('bip110.moreDetails')}</span>
            </>
          ) : (
            <p className="text-fog/60 sm:col-span-2 lg:col-span-5">{loading ? t('bip110.loading') : t('bip110.unavailable')}</p>
          )}
        </div>
      </summary>

      {status && (
        <div className="mt-5 space-y-5 border-t border-white/10 pt-5">
          <p className="text-sm text-fog/65">{t('bip110.subtitle')}</p>
          <HorizontalBarGauge
            label={t('bip110.thresholdProgress')}
            value={signalPct}
            max={status.threshold_pct || 55}
            valueLabel={`${formatPercent(locale, signalPct, 2)}% / ${formatPercent(locale, status.threshold_pct, 0)}%`}
            detail={t('bip110.thresholdDetail', { value: formatSats(locale, status.threshold_count) })}
            tone={tone}
          />

          <div className="grid gap-4 lg:grid-cols-2">
            <SourcePanel label={t('bip110.internalSource')} source={status.internal} locale={locale} />
            <SourcePanel label={t('bip110.publicSource')} source={status.public} locale={locale} />
          </div>

          <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
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
          </div>
        </div>
      )}
    </details>
  )
}
