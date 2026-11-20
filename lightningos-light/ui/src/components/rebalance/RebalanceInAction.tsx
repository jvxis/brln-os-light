import { FormEvent, useEffect, useId, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { previewRebalance } from '../../api'
import type { RebalanceRunPreview } from '../../api'

export type RebalanceInOverrides = {
  amountSat?: number
  feeLimitPpm?: number
  sourceChannelPoint?: string
  allowExcludedSource?: boolean
  maxFeeMsat?: number
}

export type RebalanceSourceOption = {
  channelPoint: string
  channelId: string
  peerAlias: string
  excluded: boolean
  maxSourceSat: number
}

type RebalanceInActionProps = {
  channelPoint: string
  peerAlias: string
  targetOutboundPct: number
  disabled?: boolean
  disabledReason?: string
  sourceOptions?: RebalanceSourceOption[]
  onRun: (overrides?: RebalanceInOverrides) => Promise<boolean>
}

const parseOptionalPositiveInteger = (raw: string) => {
  if (!raw.trim()) return undefined
  const value = Number(raw)
  if (!Number.isSafeInteger(value) || value <= 0) return null
  return value
}

// The absolute fee cap is typed in sats with up to three decimals and sent in
// millisats, so nothing is rounded on the way.
const parseOptionalFeeMsat = (raw: string) => {
  const text = raw.trim().replace(',', '.')
  if (!text) return undefined
  if (!/^\d+(\.\d{1,3})?$/.test(text)) return null
  const msat = Math.round(Number(text) * 1000)
  if (!Number.isSafeInteger(msat) || msat <= 0) return null
  return msat
}

export function RebalanceInAction({
  channelPoint,
  peerAlias,
  targetOutboundPct,
  disabled = false,
  disabledReason,
  sourceOptions,
  onRun
}: RebalanceInActionProps) {
  const { t } = useTranslation()
  const formatter = useMemo(() => new Intl.NumberFormat(), [])
  const [open, setOpen] = useState(false)
  const [amount, setAmount] = useState('')
  const [feeLimit, setFeeLimit] = useState('')
  const [source, setSource] = useState('')
  const [allowExcluded, setAllowExcluded] = useState(false)
  const [maxFee, setMaxFee] = useState('')
  const [preview, setPreview] = useState<RebalanceRunPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const [running, setRunning] = useState(false)
  const reasonID = useId()
  const reason = disabledReason || t('rebalanceCenter.manualDisabled.unavailable')

  const parsedAmount = parseOptionalPositiveInteger(amount)
  const parsedFeeLimit = parseOptionalPositiveInteger(feeLimit)
  const parsedMaxFee = parseOptionalFeeMsat(maxFee)
  const inputsValid = parsedAmount !== null && parsedFeeLimit !== null && parsedMaxFee !== null && (parsedFeeLimit === undefined || parsedFeeLimit <= 10_000)
  const sources = useMemo(
    () => (sourceOptions ?? [])
      .filter((option) => option.channelPoint !== channelPoint)
      .sort((a, b) => Number(a.excluded) - Number(b.excluded) || b.maxSourceSat - a.maxSourceSat),
    [channelPoint, sourceOptions]
  )
  const selectedSource = sources.find((option) => option.channelPoint === source)
  const sourcePoint = selectedSource?.channelPoint
  const exceptionRequested = selectedSource?.excluded === true && allowExcluded
  const hasOverrides = parsedAmount !== undefined || parsedFeeLimit !== undefined || sourcePoint !== undefined || parsedMaxFee !== undefined
  const sourceBlocked = preview?.source !== undefined && !preview.source.eligible

  useEffect(() => {
    if (!open) return
    if (!inputsValid) {
      setPreview(null)
      setPreviewError(t('rebalanceCenter.channels.rebalanceCustomInvalid'))
      setPreviewLoading(false)
      return
    }
    let cancelled = false
    const timer = window.setTimeout(() => {
      setPreviewLoading(true)
      setPreviewError('')
      void previewRebalance({
        channel_point: channelPoint,
        target_outbound_pct: targetOutboundPct,
        amount_sat: parsedAmount,
        fee_limit_ppm: parsedFeeLimit,
        source_channel_point: sourcePoint,
        allow_excluded_source: exceptionRequested || undefined,
        max_fee_msat: parsedMaxFee ?? undefined
      })
        .then((next) => {
          if (!cancelled) setPreview(next)
        })
        .catch((err) => {
          if (!cancelled) {
            setPreview(null)
            setPreviewError(err instanceof Error ? err.message : t('rebalanceCenter.channels.rebalancePreviewUnavailable'))
          }
        })
        .finally(() => {
          if (!cancelled) setPreviewLoading(false)
        })
    }, 250)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [channelPoint, exceptionRequested, inputsValid, open, parsedAmount, parsedFeeLimit, parsedMaxFee, sourcePoint, t, targetOutboundPct])

  const openCustom = () => {
    setAmount('')
    setFeeLimit('')
    setSource('')
    setAllowExcluded(false)
    setMaxFee('')
    setPreview(null)
    setPreviewError('')
    setOpen(true)
  }

  const handleDefaultRun = async () => {
    if (disabled || running) return
    setRunning(true)
    try {
      await onRun()
    } finally {
      setRunning(false)
    }
  }

  const handleCustomRun = async (event: FormEvent) => {
    event.preventDefault()
    if (!inputsValid || !preview || previewLoading || running || sourceBlocked) return
    setRunning(true)
    try {
      const ok = await onRun({
        amountSat: parsedAmount,
        feeLimitPpm: parsedFeeLimit,
        sourceChannelPoint: sourcePoint,
        allowExcludedSource: exceptionRequested || undefined,
        maxFeeMsat: parsedMaxFee ?? undefined
      })
      if (ok) setOpen(false)
    } finally {
      setRunning(false)
    }
  }

  const dialog = open && typeof document !== 'undefined'
    ? createPortal(
        <div
          className="fixed inset-0 z-50 flex items-end justify-center bg-black/65 px-3 py-4 backdrop-blur-sm sm:items-center"
          onMouseDown={() => {
            if (!running) setOpen(false)
          }}
        >
          <form
            className="w-full max-w-md rounded-2xl border border-white/10 bg-ink p-5 shadow-2xl"
            onSubmit={handleCustomRun}
            onMouseDown={(event) => event.stopPropagation()}
          >
            <div className="flex items-start justify-between gap-4">
              <div>
                <div className="text-[10px] font-semibold uppercase tracking-[0.18em] text-brass">
                  {t('rebalanceCenter.channels.rebalanceCustomEyebrow')}
                </div>
                <h3 className="mt-1 text-lg font-semibold text-fog">
                  {t('rebalanceCenter.channels.rebalanceCustomTitle', { alias: peerAlias })}
                </h3>
              </div>
              <button
                className="rounded-lg px-2 py-1 text-fog/50 hover:bg-white/5 hover:text-fog"
                type="button"
                onClick={() => setOpen(false)}
                disabled={running}
                aria-label={t('common.close')}
              >
                ×
              </button>
            </div>

            <div className="mt-4 rounded-xl border border-white/10 bg-white/[0.03] p-3 text-xs text-fog/70">
              {previewLoading && !preview
                ? t('rebalanceCenter.channels.rebalancePreviewLoading')
                : preview
                  ? t('rebalanceCenter.channels.rebalanceDefaultSummary', {
                      amount: formatter.format(preview.default_amount_sat),
                      fee: formatter.format(preview.default_fee_limit_ppm)
                    })
                  : t('rebalanceCenter.channels.rebalancePreviewUnavailable')}
            </div>

            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              <label className="block">
                <span className="mb-2 block text-[10px] font-semibold uppercase tracking-[0.14em] text-fog/50">
                  {t('rebalanceCenter.channels.rebalanceAmountOverride')}
                </span>
                <input
                  className="input-field"
                  type="number"
                  min={1}
                  step={1}
                  inputMode="numeric"
                  value={amount}
                  placeholder={preview ? String(preview.default_amount_sat) : ''}
                  onChange={(event) => setAmount(event.target.value)}
                  autoFocus
                />
                <span className="mt-1 block text-[10px] text-fog/40">
                  {t('rebalanceCenter.channels.rebalanceBlankUsesDefault')}
                </span>
              </label>
              <label className="block">
                <span className="mb-2 block text-[10px] font-semibold uppercase tracking-[0.14em] text-fog/50">
                  {t('rebalanceCenter.channels.rebalanceFeeOverride')}
                </span>
                <input
                  className="input-field"
                  type="number"
                  min={1}
                  max={10_000}
                  step={1}
                  inputMode="numeric"
                  value={feeLimit}
                  placeholder={preview ? String(preview.default_fee_limit_ppm) : ''}
                  onChange={(event) => setFeeLimit(event.target.value)}
                />
                <span className="mt-1 block text-[10px] text-fog/40">
                  {t('rebalanceCenter.channels.rebalanceBlankUsesDefault')}
                </span>
              </label>
            </div>

            <div className="mt-4 grid gap-4 sm:grid-cols-2">
              {sources.length > 0 && (
                <label className="block sm:col-span-2">
                  <span className="mb-2 block text-[10px] font-semibold uppercase tracking-[0.14em] text-fog/50">
                    {t('rebalanceCenter.channels.rebalanceSource')}
                  </span>
                  <select
                    className="input-field"
                    value={source}
                    onChange={(event) => {
                      setSource(event.target.value)
                      setAllowExcluded(false)
                    }}
                  >
                    <option value="">{t('rebalanceCenter.channels.rebalanceSourceAuto')}</option>
                    {sources.map((option) => (
                      <option key={option.channelPoint} value={option.channelPoint}>
                        {t(option.excluded ? 'rebalanceCenter.channels.rebalanceSourceOptionExcluded' : 'rebalanceCenter.channels.rebalanceSourceOption', {
                          alias: option.peerAlias || option.channelId,
                          id: option.channelId,
                          amount: formatter.format(option.maxSourceSat)
                        })}
                      </option>
                    ))}
                  </select>
                  <span className="mt-1 block text-[10px] text-fog/40">
                    {t('rebalanceCenter.channels.rebalanceSourceHelp')}
                  </span>
                </label>
              )}
              {selectedSource?.excluded && (
                <label className="flex items-start gap-2 text-xs text-amber-100/90 sm:col-span-2">
                  <input
                    className="mt-0.5"
                    type="checkbox"
                    checked={allowExcluded}
                    onChange={(event) => setAllowExcluded(event.target.checked)}
                  />
                  <span>{t('rebalanceCenter.channels.rebalanceAllowExcluded')}</span>
                </label>
              )}
              <label className="block sm:col-span-2">
                <span className="mb-2 block text-[10px] font-semibold uppercase tracking-[0.14em] text-fog/50">
                  {t('rebalanceCenter.channels.rebalanceMaxFeeAbsolute')}
                </span>
                <input
                  className="input-field"
                  type="text"
                  inputMode="decimal"
                  value={maxFee}
                  onChange={(event) => setMaxFee(event.target.value)}
                />
                <span className="mt-1 block text-[10px] text-fog/40">
                  {t('rebalanceCenter.channels.rebalanceMaxFeeAbsoluteHelp')}
                </span>
              </label>
            </div>

            {preview && (
              <div className="mt-4 space-y-2 rounded-xl border border-cyan-300/10 bg-cyan-300/[0.04] p-3 text-xs text-fog/70">
                <div className="flex items-center justify-between gap-4">
                  <span>{t('rebalanceCenter.channels.rebalanceEffective')}</span>
                  <span className="font-medium text-fog">
                    {formatter.format(preview.effective_amount_sat)} sats · {formatter.format(preview.effective_fee_limit_ppm)} ppm
                  </span>
                </div>
                <div className="flex items-center justify-between gap-4">
                  <span>{t('rebalanceCenter.channels.rebalanceMaxCost')}</span>
                  <span className="font-medium text-fog">
                    {preview.fee_cap_absolute
                      ? `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 3 }).format(preview.max_fee_msat / 1000)} sats`
                      : `${formatter.format(preview.max_fee_sat)} sats`}
                  </span>
                </div>
                {preview.fee_cap_absolute && (
                  <div className="text-fog/60">{t('rebalanceCenter.channels.rebalanceMaxFeeAbsoluteBinds')}</div>
                )}
                {preview.source && preview.source.eligible && (
                  <div className={preview.source.exclusion_exception ? 'text-amber-200' : 'text-fog/70'}>
                    {t(preview.source.exclusion_exception ? 'rebalanceCenter.channels.rebalanceSourceException' : 'rebalanceCenter.channels.rebalanceSourceUsable', {
                      alias: preview.source.peer_alias || selectedSource?.channelId || '',
                      amount: formatter.format(preview.source.max_source_sat)
                    })}
                  </div>
                )}
                {preview.source && !preview.source.eligible && (
                  <div className="text-rose-300">
                    {t('rebalanceCenter.channels.rebalanceSourceBlocked', {
                      alias: preview.source.peer_alias || selectedSource?.channelId || '',
                      reason: t(`rebalanceCenter.channels.fixedSourceReason.${preview.source.blocked_reason || 'source_not_found'}`)
                    })}
                  </div>
                )}
                <p className="text-fog/60">{t('rebalanceCenter.manualFeeBudgetHelp')}</p>
                {preview.amount_overrides_config && (
                  <div className="text-amber-200">{t('rebalanceCenter.channels.rebalanceAmountWarning')}</div>
                )}
                {preview.amount_clamped && (
                  <div className="text-amber-200">{t('rebalanceCenter.channels.rebalanceAmountClamped')}</div>
                )}
                {preview.fee_exceeds_outgoing && (
                  <div className="text-amber-200">{t('rebalanceCenter.channels.rebalanceFeeWarning')}</div>
                )}
              </div>
            )}

            {hasOverrides && (
              <div className="mt-3 text-[11px] text-amber-100/80">
                {t('rebalanceCenter.channels.rebalanceOneJobOnly')}
              </div>
            )}
            {previewError && <div className="mt-3 text-xs text-rose-300">{previewError}</div>}

            <div className="mt-5 flex justify-end gap-3">
              <button className="btn-secondary" type="button" onClick={() => setOpen(false)} disabled={running}>
                {t('common.cancel')}
              </button>
              <button className="btn-primary" type="submit" disabled={!inputsValid || !preview || previewLoading || running || sourceBlocked}>
                {running ? t('common.loading') : t('rebalanceCenter.channels.rebalanceStartCustom')}
              </button>
            </div>
          </form>
        </div>,
        document.body
      )
    : null

  return (
    <>
      <span className="inline-flex flex-col gap-1">
      <span className="inline-flex items-center gap-1" title={disabled ? reason : undefined}>
        <button
          className="btn-primary px-3 py-1 text-xs"
          type="button"
          onClick={() => void handleDefaultRun()}
          disabled={disabled || running}
          title={disabled ? reason : t('rebalanceCenter.channelsHints.rebalanceIn')}
          aria-describedby={disabled ? reasonID : undefined}
        >
          {t('rebalanceCenter.channels.rebalanceIn')}
        </button>
        <button
          className="inline-flex h-7 w-7 items-center justify-center rounded-lg bg-transparent text-fog/40 transition hover:bg-white/5 hover:text-cyan-200 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-cyan-300/60 disabled:cursor-not-allowed disabled:opacity-30"
          type="button"
          onClick={openCustom}
          disabled={running}
          title={t('rebalanceCenter.channels.rebalanceOptions')}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-label={t('rebalanceCenter.channels.rebalanceOptions')}
        >
          <svg aria-hidden="true" viewBox="0 0 20 20" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.5">
            <path d="M3 5h7m3 0h4M3 10h2m3 0h9M3 15h8m3 0h3" strokeLinecap="round" />
            <circle cx="11.5" cy="5" r="1.5" />
            <circle cx="6.5" cy="10" r="1.5" />
            <circle cx="12.5" cy="15" r="1.5" />
          </svg>
        </button>
      </span>
      {disabled && <span id={reasonID} className="max-w-64 text-[11px] text-amber-200">{reason} {t('rebalanceCenter.manualDisabled.review')}</span>}
      </span>
      {dialog}
    </>
  )
}
