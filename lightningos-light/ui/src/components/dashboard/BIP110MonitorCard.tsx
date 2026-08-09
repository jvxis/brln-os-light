import { useTranslation } from 'react-i18next'
import { getLocale } from '../../i18n'
import { clamp, formatPercent, formatSats, formatTimestamp } from './formatters'
import StatusBadge from './StatusBadge'
import type { BIP110MonitorStatus, BIP110SourceStatus, BitcoinStatus, Tone } from './types'

type BIP110MonitorCardProps = {
  status: BIP110MonitorStatus | null
  bitcoin: BitcoinStatus | null
  loading: boolean
}

const BIP110_AVERAGE_BLOCK_INTERVAL_MS = 10 * 60 * 1000

type MandatorySignalingEstimate = {
  estimatedAt: string
  cadenceBlocksPerHour: number | null
  cadenceHours: number | null
}

const estimateMandatorySignalingAt = (
  checkedAt: string,
  blocksToMandatory: number,
  bitcoin: BitcoinStatus | null,
): MandatorySignalingEstimate | null => {
  if (!Number.isFinite(blocksToMandatory) || blocksToMandatory <= 0) return null
  const checkedAtMs = new Date(checkedAt).getTime()
  if (!Number.isFinite(checkedAtMs)) return null

  const cadenceBuckets = Array.isArray(bitcoin?.block_cadence) ? bitcoin.block_cadence : []
  const cadenceWindowSec = bitcoin?.block_cadence_window_sec ?? 600
  const cadenceHours = cadenceWindowSec > 0 && cadenceBuckets.length > 0
    ? (cadenceWindowSec * cadenceBuckets.length) / 3600
    : 0
  const cadenceTotal = cadenceBuckets.reduce((sum, bucket) => sum + bucket.count, 0)
  const cadenceBlocksPerHour = bitcoin?.rpc_ok !== false
    && bitcoin?.rpc_stale !== true
    && bitcoin?.initial_block_download !== true
    && cadenceHours > 0
    && cadenceTotal > 0
    ? cadenceTotal / cadenceHours
    : null
  const blockIntervalMs = cadenceBlocksPerHour
    ? (60 * 60 * 1000) / cadenceBlocksPerHour
    : BIP110_AVERAGE_BLOCK_INTERVAL_MS
  const estimatedAt = new Date(checkedAtMs + blocksToMandatory * blockIntervalMs)
  if (!Number.isFinite(estimatedAt.getTime())) return null

  return {
    estimatedAt: estimatedAt.toISOString(),
    cadenceBlocksPerHour,
    cadenceHours: cadenceBlocksPerHour ? cadenceHours : null,
  }
}

const formatCompactTimestamp = (locale: string, value: string) => {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '-'
  return new Intl.DateTimeFormat(locale, {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(date)
}

const riskTone = (risk?: string): Tone => {
  if (risk === 'high') return 'danger'
  if (risk === 'elevated' || risk === 'watch') return 'warn'
  if (risk === 'low' || risk === 'normal') return 'ok'
  return 'muted'
}

const comparisonTone = (value?: string): Tone => {
  if (value === 'matched') return 'ok'
  if (value === 'signal_mismatch') return 'danger'
  if (value === 'tip_mismatch') return 'warn'
  return 'muted'
}

function Chevron() {
  return (
    <svg aria-hidden="true" className="h-4 w-4" fill="none" viewBox="0 0 24 24">
      <path d="m6 9 6 6 6-6" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" />
    </svg>
  )
}

type SourcePanelProps = {
  label: string
  source: BIP110SourceStatus
  locale: string
}

function SourcePanel({ label, source, locale }: SourcePanelProps) {
  const { t } = useTranslation()
  return (
    <div className="bip110-source">
      <div className="flex min-w-0 items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold text-fog/90">{label}</p>
          <p className="mt-0.5 truncate text-[10px] text-fog/40">{source.subversion || source.source}</p>
        </div>
        <StatusBadge
          label={source.available ? t('bip110.available') : t('common.unavailable')}
          tone={source.available ? 'ok' : 'warn'}
        />
      </div>
      {source.available ? (
        <div className="mt-4 grid grid-cols-3 gap-3">
          <div>
            <p className="bip110-detail-label">{t('bip110.tip')}</p>
            <p className="bip110-detail-value">#{formatSats(locale, source.tip)}</p>
          </div>
          <div>
            <p className="bip110-detail-label">{t('bip110.signaling')}</p>
            <p className="bip110-detail-value">{formatPercent(locale, source.pct, 2)}%</p>
          </div>
          <div>
            <p className="bip110-detail-label">{t('bip110.scoreBlocks')}</p>
            <p className="bip110-detail-value">{formatSats(locale, source.signaling_count)} / {formatSats(locale, source.total_blocks)}</p>
          </div>
        </div>
      ) : (
        <p className="mt-3 text-xs text-amber-200/80 [overflow-wrap:anywhere]">{source.error || t('bip110.sourceUnavailable')}</p>
      )}
    </div>
  )
}

type EnforcingSourcePanelProps = {
  score: BIP110MonitorStatus['fork_score']
  locale: string
}

function EnforcingSourcePanel({ score, locale }: EnforcingSourcePanelProps) {
  const { t } = useTranslation()
  const available = Boolean(score?.available)
  const gap = available
    ? Math.max(0, (score?.non_enforcing_tip ?? 0) - (score?.enforcing_tip ?? 0))
    : null

  return (
    <div className="bip110-source">
      <div className="flex min-w-0 items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold text-fog/90">{t('bip110.enforcingPublicSource')}</p>
          <p className="mt-0.5 truncate text-[10px] text-fog/40">{score?.enforcing_source || 'mempool.guide'}</p>
        </div>
        <StatusBadge
          label={available ? t('bip110.available') : t('common.unavailable')}
          tone={available ? 'ok' : 'warn'}
        />
      </div>
      {available ? (
        <div className="mt-4 grid grid-cols-3 gap-3">
          <div>
            <p className="bip110-detail-label">{t('bip110.tip')}</p>
            <p className="bip110-detail-value">#{formatSats(locale, score?.enforcing_tip)}</p>
          </div>
          <div>
            <p className="bip110-detail-label">{t('bip110.branchAdvance')}</p>
            <p className="bip110-detail-value">+{formatSats(locale, score?.enforcing_blocks)}</p>
          </div>
          <div>
            <p className="bip110-detail-label">{t('bip110.chainGap')}</p>
            <p className="bip110-detail-value">{t('bip110.blockCount', { value: formatSats(locale, gap) })}</p>
          </div>
        </div>
      ) : (
        <p className="mt-3 text-xs text-amber-200/80 [overflow-wrap:anywhere]">{score?.error || t('bip110.sourceUnavailable')}</p>
      )}
    </div>
  )
}

export default function BIP110MonitorCard({ status, bitcoin, loading }: BIP110MonitorCardProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const displaySource = status?.internal?.available ? status.internal : status?.public
  // Older manager builds omitted numeric zeroes from the response. An
  // available source with no pct field therefore means a valid 0% score, not
  // an unavailable monitor.
  const scoreAvailable = Boolean(displaySource?.available)
  const signalPct = scoreAvailable ? clamp(displaySource?.pct ?? 0) : null
  const nonSignalPct = signalPct === null ? null : clamp(100 - signalPct)
  const signalingCount = scoreAvailable ? Math.max(0, displaySource?.signaling_count ?? 0) : null
  const totalBlocks = scoreAvailable ? Math.max(0, displaySource?.total_blocks ?? 0) : null
  const nonSignalingCount = signalingCount === null || totalBlocks === null
    ? null
    : Math.max(0, totalBlocks - signalingCount)
  const forkScore = status?.fork_score
  const forkScoreAvailable = Boolean(forkScore?.available)
  const nonEnforcingAdvance = forkScoreAvailable ? Math.max(0, forkScore?.non_enforcing_blocks ?? 0) : null
  const enforcingAdvance = forkScoreAvailable ? Math.max(0, forkScore?.enforcing_blocks ?? 0) : null
  const nonEnforcingTip = forkScoreAvailable ? forkScore?.non_enforcing_tip : null
  const enforcingTip = forkScoreAvailable ? forkScore?.enforcing_tip : null
  const leadingAdvance = Math.max(nonEnforcingAdvance ?? 0, enforcingAdvance ?? 0, 1)
  const enforcingRacePct = forkScoreAvailable ? clamp(((enforcingAdvance ?? 0) / leadingAdvance) * 100) : 0
  const tone = riskTone(status?.risk_level)
  const comparisonStatus = status?.comparison?.status || 'unavailable'
  const mandatorySignalingEta = status
    ? estimateMandatorySignalingAt(status.checked_at, status.blocks_to_mandatory, bitcoin)
    : null
  const mandatorySignalingEtaBasis = mandatorySignalingEta?.cadenceBlocksPerHour
    ? t('bip110.etaBasisObserved', {
        rate: formatPercent(locale, mandatorySignalingEta.cadenceBlocksPerHour, 1),
        hours: formatPercent(locale, mandatorySignalingEta.cadenceHours, 1),
      })
    : t('bip110.etaBasisFallback')
  const currentTip = displaySource?.tip

  return (
    <details className={`section-card bip110-card bip110-card--${tone} group`}>
      <summary className="bip110-summary cursor-pointer list-none [&::-webkit-details-marker]:hidden">
        {status ? (
          <div className="bip110-scoreboard">
            <div className="bip110-scoreboard__identity">
              <span className="bip110-eyebrow">{t('bip110.kicker')}</span>
              <div className="mt-1 flex items-center gap-2">
                <h3 className="text-base font-semibold tracking-tight text-fog">{t('bip110.shortTitle')}</h3>
                <span className="bip110-info-dot" title={t('bip110.informational')}>i</span>
              </div>
              <p className="mt-1 truncate text-[10px] text-fog/40">{t(`bip110.phase.${status.phase}`)}</p>
            </div>

            <div
              aria-label={t('bip110.scoreAria', {
                split: formatSats(locale, forkScore?.split_height),
                nonEnforcing: formatSats(locale, nonEnforcingAdvance),
                nonEnforcingTip: formatSats(locale, nonEnforcingTip),
                enforcing: formatSats(locale, enforcingAdvance),
                enforcingTip: formatSats(locale, enforcingTip),
              })}
              className="bip110-scoreboard__race"
            >
              <div className="bip110-score bip110-score--non">
                <span className="bip110-score__label">{t('bip110.nonEnforcing')}</span>
                <strong className="bip110-score__value">
                  {nonEnforcingAdvance === null ? '—' : <><small>+</small>{formatSats(locale, nonEnforcingAdvance)}</>}
                </strong>
                <span className="bip110-score__blocks">
                  {nonEnforcingTip === null ? t('bip110.forkScoreUnavailable') : `#${formatSats(locale, nonEnforcingTip)}`}
                </span>
              </div>
              <span className="bip110-scoreboard__versus">×</span>
              <div className="bip110-score bip110-score--signal">
                <span className="bip110-score__label">{t('bip110.enforcing')}</span>
                <strong className="bip110-score__value">
                  {enforcingAdvance === null ? '—' : <><small>+</small>{formatSats(locale, enforcingAdvance)}</>}
                </strong>
                <span className="bip110-score__blocks">
                  {enforcingTip === null ? t('bip110.forkScoreUnavailable') : `#${formatSats(locale, enforcingTip)}`}
                </span>
              </div>
              <div
                className="bip110-race-rail"
                title={forkScoreAvailable
                  ? t('bip110.forkScoreDetail', { split: formatSats(locale, forkScore?.split_height) })
                  : t('bip110.forkScoreUnavailable')}
              >
                <span className="bip110-race-rail__signal" style={{ width: `${enforcingRacePct}%` }} />
              </div>
            </div>

            <div className="bip110-scoreboard__next">
              <div className="flex items-center justify-end gap-2">
                <StatusBadge label={t(`bip110.risk.${status.risk_level}`)} tone={tone} />
                <span
                  aria-label={t(`bip110.comparison.${comparisonStatus}`)}
                  className={`bip110-source-check bip110-source-check--${comparisonTone(comparisonStatus)}`}
                  title={t(`bip110.comparison.${comparisonStatus}`)}
                />
              </div>
              {status.blocks_to_mandatory > 0 ? (
                <>
                  <p className="mt-1.5 text-right text-sm font-semibold tabular-nums text-fog/90">
                    {t('bip110.blocksUntilWindow', { value: formatSats(locale, status.blocks_to_mandatory) })}
                  </p>
                  {mandatorySignalingEta && (
                    <p className="bip110-eta mt-0.5 truncate text-right text-[10px]" title={mandatorySignalingEtaBasis}>
                      {t('bip110.compactEta', { date: formatCompactTimestamp(locale, mandatorySignalingEta.estimatedAt) })}
                    </p>
                  )}
                </>
              ) : (
                <p className="mt-1.5 text-right text-sm font-semibold text-fog/90">{t(`bip110.phase.${status.phase}`)}</p>
              )}
            </div>

            <span className="bip110-scoreboard__chevron text-fog/45 transition-transform group-open:rotate-180">
              <Chevron />
            </span>
          </div>
        ) : (
          <div className="bip110-scoreboard bip110-scoreboard--empty">
            <div>
              <span className="bip110-eyebrow">{t('bip110.kicker')}</span>
              <h3 className="mt-1 font-semibold">{t('bip110.shortTitle')}</h3>
            </div>
            <p className="text-sm text-fog/55">{loading ? t('bip110.loading') : t('bip110.unavailable')}</p>
            <span className="bip110-scoreboard__chevron text-fog/45"><Chevron /></span>
          </div>
        )}
      </summary>

      {status && (
        <div className="bip110-detail">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="max-w-3xl">
              <p className="bip110-eyebrow">{t('bip110.detailKicker')}</p>
              <h4 className="mt-1 text-lg font-semibold text-fog">{t('bip110.detailTitle')}</h4>
              <p className="mt-1 text-sm text-fog/60">
                {t('bip110.scoreStory', {
                  nonSignalingBlocks: formatSats(locale, nonSignalingCount),
                  signalingBlocks: formatSats(locale, signalingCount),
                  signaling: signalPct === null ? '-' : formatPercent(locale, signalPct, 1),
                  threshold: formatPercent(locale, status.threshold_pct, 0),
                })}
              </p>
            </div>
            <StatusBadge label={t(`bip110.comparison.${comparisonStatus}`)} tone={comparisonTone(comparisonStatus)} />
          </div>

          <div className="bip110-milestones">
            <div className="bip110-milestone bip110-milestone--current">
              <span className="bip110-milestone__dot" />
              <p>{t('bip110.currentTip')}</p>
              <strong>#{formatSats(locale, currentTip)}</strong>
            </div>
            <div className="bip110-milestone">
              <span className="bip110-milestone__dot" />
              <p>{t('bip110.mandatoryWindow')}</p>
              <strong>#{formatSats(locale, status.mandatory_start_height)}</strong>
            </div>
            <div className="bip110-milestone">
              <span className="bip110-milestone__dot" />
              <p>{t('bip110.lockIn')}</p>
              <strong>#{formatSats(locale, status.lock_in_height)}</strong>
            </div>
            <div className="bip110-milestone">
              <span className="bip110-milestone__dot" />
              <p>{t('bip110.activation')}</p>
              <strong>#{formatSats(locale, status.activation_height)}</strong>
            </div>
          </div>

          <div className="grid gap-3 lg:grid-cols-2">
            <SourcePanel label={t('bip110.internalSource')} source={status.internal} locale={locale} />
            <EnforcingSourcePanel score={status.fork_score} locale={locale} />
          </div>

          <div className="bip110-technical">
            <div className="grid gap-x-5 gap-y-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
              <div><span>{t('bip110.backend')}</span><strong className="[overflow-wrap:anywhere]">{status.internal.subversion || status.internal.source}</strong></div>
              <div><span>{t('bip110.period')}</span><strong>{formatSats(locale, displaySource?.period_num)}</strong></div>
              <div><span>{t('bip110.lastComparison')}</span><strong>{formatTimestamp(locale, status.checked_at)}</strong></div>
              <div><span>{t('bip110.sourceDelta')}</span><strong>{formatSats(locale, status.comparison.tip_delta)} / {formatSats(locale, status.comparison.signaling_count_delta)}</strong></div>
            </div>
            <div className="mt-4 flex flex-col gap-2 border-t border-white/10 pt-3 sm:flex-row sm:items-center sm:justify-between">
              <p className="max-w-4xl text-[10px] leading-relaxed text-fog/40">{t('bip110.disclaimer')}</p>
              <a className="shrink-0 text-xs font-medium text-sky-300 hover:text-sky-200" href="https://bip110monitor.com/" target="_blank" rel="noreferrer">
                {t('bip110.openPublicMonitor')} ↗
              </a>
            </div>
          </div>
        </div>
      )}
    </details>
  )
}
