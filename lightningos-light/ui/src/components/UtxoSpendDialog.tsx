import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getWalletAddress, previewOnchainSend, sendOnchain } from '../api'
import { computePrivacyWarnings, type UtxoLike } from '../utils/utxoPrivacy'

export type SpendMode = 'spend' | 'consolidate'

type Props = {
  mode: SpendMode
  selection: UtxoLike[]
  defaultSatPerVbyte?: number
  onClose: () => void
  onSuccess: (txid: string) => void
}

export default function UtxoSpendDialog({
  mode,
  selection,
  defaultSatPerVbyte,
  onClose,
  onSuccess
}: Props) {
  const { t } = useTranslation()
  const [address, setAddress] = useState('')
  const [internalAddrLoading, setInternalAddrLoading] = useState(mode === 'consolidate')
  const [internalAddrError, setInternalAddrError] = useState('')
  const [amountSat, setAmountSat] = useState('')
  const [sweepAll, setSweepAll] = useState(mode === 'consolidate')
  const [satPerVbyte, setSatPerVbyte] = useState(String(defaultSatPerVbyte ?? 5))
  const [label, setLabel] = useState(mode === 'consolidate' ? 'consolidate' : '')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [preview, setPreview] = useState<any>(null)
  const [previewError, setPreviewError] = useState('')
  const [previewing, setPreviewing] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState('')
  const [needsPassword, setNeedsPassword] = useState(false)

  useEffect(() => {
    if (mode !== 'consolidate') return
    let active = true
    setInternalAddrLoading(true)
    getWalletAddress()
      .then((res: any) => {
        if (!active) return
        const next = res?.address || res?.addr || ''
        if (!next) throw new Error(t('onchainHub.spend.errorDeriveAddress'))
        setAddress(next)
        setInternalAddrError('')
      })
      .catch((err: any) => {
        if (!active) return
        setInternalAddrError(err?.message || t('onchainHub.spend.errorDeriveAddress'))
      })
      .finally(() => {
        if (active) setInternalAddrLoading(false)
      })
    return () => {
      active = false
    }
  }, [mode])

  const outpoints = useMemo(() => selection.map((u) => u.outpoint), [selection])
  const totalSat = useMemo(() => selection.reduce((a, u) => a + (u.amount_sat || 0), 0), [selection])
  const warnings = useMemo(
    () =>
      computePrivacyWarnings(selection, {
        mode,
        satPerVbyte: Number(satPerVbyte) || 0
      }),
    [selection, mode, satPerVbyte]
  )

  const formattedSelection = useMemo(() => {
    const list = selection.slice(0, 4).map((u) => u.outpoint.slice(0, 12) + '…')
    if (selection.length > 4) list.push(t('onchainHub.utxoMgr.moreItems', { count: selection.length - 4 }))
    return list.join(', ')
  }, [selection, t])

  const runPreview = async () => {
    setPreviewError('')
    setPreview(null)
    const rate = Number(satPerVbyte)
    if (!Number.isFinite(rate) || rate <= 0) {
      setPreviewError(t('onchainHub.spend.errorInvalidFeeRate'))
      return
    }
    const amount = sweepAll ? 0 : Number(amountSat)
    if (!sweepAll && (!Number.isFinite(amount) || amount <= 0)) {
      setPreviewError(t('onchainHub.spend.errorInvalidAmount'))
      return
    }
    if (!address.trim()) {
      setPreviewError(t('onchainHub.spend.errorAddressRequired'))
      return
    }
    setPreviewing(true)
    try {
      const res: any = await previewOnchainSend({
        address: address.trim(),
        amount_sat: amount,
        sweep_all: sweepAll,
        sat_per_vbyte: rate,
        outpoints
      })
      setPreview(res)
    } catch (err: any) {
      setPreviewError(err?.message || t('onchainHub.spend.errorPreviewFailed'))
    } finally {
      setPreviewing(false)
    }
  }

  const submit = async () => {
    setSubmitError('')
    const rate = Number(satPerVbyte)
    if (!Number.isFinite(rate) || rate <= 0) {
      setSubmitError(t('onchainHub.spend.errorInvalidFeeRate'))
      return
    }
    const amount = sweepAll ? 0 : Number(amountSat)
    if (!sweepAll && (!Number.isFinite(amount) || amount <= 0)) {
      setSubmitError(t('onchainHub.spend.errorInvalidAmount'))
      return
    }
    if (!address.trim()) {
      setSubmitError(t('onchainHub.spend.errorAddressRequired'))
      return
    }
    setSubmitting(true)
    try {
      const res: any = await sendOnchain({
        address: address.trim(),
        amount_sat: amount,
        sweep_all: sweepAll,
        sat_per_vbyte: rate,
        outpoints,
        label: label.trim() || undefined,
        confirm_password: confirmPassword || undefined
      })
      onSuccess(res?.txid || 'ok')
    } catch (err: any) {
      const message: string = err?.message || t('onchainHub.spend.errorSendFailed')
      const code: string | undefined = err?.code
      if (code === 'wallet_send_external_reauth_required' || message.toLowerCase().includes('password')) {
        setNeedsPassword(true)
        setSubmitError(t('onchainHub.spend.errorReauthRequired'))
      } else {
        setSubmitError(message)
      }
    } finally {
      setSubmitting(false)
    }
  }

  const title = mode === 'consolidate' ? t('onchainHub.spend.titleConsolidate') : t('onchainHub.spend.titleSpend')
  const cta = mode === 'consolidate' ? t('onchainHub.spend.ctaConsolidate') : t('onchainHub.spend.ctaSend')

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
      <button
        type="button"
        className="absolute inset-0 bg-black/75 backdrop-blur-sm"
        onClick={() => (!submitting ? onClose() : undefined)}
        aria-label={t('common.close')}
      />
      <div className="relative z-10 w-full max-w-xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel">
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-xs uppercase tracking-[0.3em] text-fog/50">{t('onchainHub.utxoMgr.kicker')}</p>
            <h2 className="mt-3 text-2xl font-semibold">{title}</h2>
            <p className="mt-2 text-sm text-fog/65">
              {t('onchainHub.spend.summary', { count: selection.length, sats: totalSat.toLocaleString() })}
            </p>
            <p className="mt-1 text-xs text-fog/50 break-all">{formattedSelection}</p>
          </div>
          <button
            type="button"
            className="inline-flex h-10 w-10 items-center justify-center rounded-full border border-white/10 bg-ink/50 text-fog/70 hover:text-white"
            onClick={() => (!submitting ? onClose() : undefined)}
            aria-label={t('common.close')}
          >
            ✕
          </button>
        </div>

        <div className="mt-5 space-y-3">
          <div>
            <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.spend.destination')}</label>
            <input
              className="input-field mt-1"
              placeholder={mode === 'consolidate' ? t('onchainHub.spend.placeholderInternalAddress') : t('onchainHub.spend.placeholderExternalAddress')}
              value={address}
              readOnly={mode === 'consolidate'}
              onChange={(e) => setAddress(e.target.value)}
            />
            {internalAddrLoading && <p className="text-xs text-fog/50 mt-1">{t('onchainHub.spend.derivingAddress')}</p>}
            {internalAddrError && <p className="text-xs text-ember mt-1">{internalAddrError}</p>}
          </div>

          {mode === 'spend' && (
            <>
              <label className="flex items-center gap-2 text-xs text-fog/70">
                <input
                  type="checkbox"
                  checked={sweepAll}
                  onChange={(e) => setSweepAll(e.target.checked)}
                />
                {t('onchainHub.spend.sweepAll')}
              </label>
              {!sweepAll && (
                <div>
                  <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.spend.amountSats')}</label>
                  <input
                    className="input-field mt-1"
                    inputMode="numeric"
                    value={amountSat}
                    onChange={(e) => setAmountSat(e.target.value.replace(/[^\d]/g, ''))}
                  />
                </div>
              )}
            </>
          )}

          <div>
            <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.utxoMgr.feeRateLabel')}</label>
            <input
              className="input-field mt-1"
              inputMode="decimal"
              value={satPerVbyte}
              onChange={(e) => setSatPerVbyte(e.target.value.replace(/[^\d.]/g, ''))}
            />
          </div>

          {mode === 'spend' && (
            <div>
              <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.spend.labelOptional')}</label>
              <input
                className="input-field mt-1"
                placeholder={t('onchainHub.spend.labelPlaceholder')}
                value={label}
                onChange={(e) => setLabel(e.target.value)}
              />
            </div>
          )}

          {needsPassword && (
            <div>
              <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.utxoMgr.confirmPassword')}</label>
              <input
                className="input-field mt-1"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
              />
            </div>
          )}

          {warnings.length > 0 && (
            <div className="rounded-2xl border border-brass/40 bg-brass/10 p-3 text-xs space-y-1">
              <p className="text-brass font-semibold">{t('onchainHub.privacy.notesHeading')}</p>
              {warnings.map((w, i) => (
                <p key={i} className={w.severity === 'warn' ? 'text-brass' : 'text-fog/70'}>
                  {t(w.key, w.params)}
                </p>
              ))}
            </div>
          )}

          {preview && (
            <div className="rounded-2xl border border-white/10 bg-ink/40 p-3 text-xs space-y-1">
              <p>
                {t('onchainHub.spend.previewFeeLabel')}: <span className="text-fog">{t('onchainHub.utxoMgr.satsValue', { value: Number(preview.fee_sat || 0).toLocaleString() })}</span>{' '}
                · {t('onchainHub.spend.previewVbytesLabel')}: {Number(preview.estimated_vbytes || 0).toLocaleString()}
              </p>
              <p>
                {t('onchainHub.spend.previewRecipientLabel')}: <span className="text-fog">{t('onchainHub.utxoMgr.satsValue', { value: Number(preview.recipient_amount_sat || 0).toLocaleString() })}</span>
                {preview.change_sat ? ` · ${t('onchainHub.spend.previewChangeLabel')}: ${t('onchainHub.utxoMgr.satsValue', { value: Number(preview.change_sat).toLocaleString() })}` : ''}
              </p>
              <p className={preview.enough_funds ? 'text-glow' : 'text-ember'}>
                {preview.message || (preview.enough_funds ? t('onchainHub.spend.previewReady') : t('onchainHub.spend.previewInsufficient'))}
              </p>
            </div>
          )}
          {previewError && <p className="text-xs text-ember">{previewError}</p>}
          {submitError && !needsPassword && <p className="text-xs text-ember">{submitError}</p>}
          {submitError && needsPassword && <p className="text-xs text-brass">{submitError}</p>}
        </div>

        <div className="mt-6 flex flex-wrap items-center justify-end gap-2">
          <button
            type="button"
            className="btn-secondary text-xs px-3 py-2"
            onClick={runPreview}
            disabled={previewing || submitting || internalAddrLoading}
          >
            {previewing ? t('onchainHub.utxoMgr.previewing') : t('onchainHub.utxoMgr.preview')}
          </button>
          <button
            type="button"
            className="btn-primary text-xs px-3 py-2"
            onClick={submit}
            disabled={submitting || internalAddrLoading}
          >
            {submitting ? t('onchainHub.utxoMgr.broadcasting') : cta}
          </button>
        </div>
      </div>
    </div>
  )
}
