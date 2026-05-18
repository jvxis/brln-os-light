import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { bumpUtxoFee } from '../api'

type Props = {
  outpoint: string
  defaultSatPerVbyte?: number
  onClose: () => void
  onSuccess: () => void
}

export default function UtxoBumpDialog({ outpoint, defaultSatPerVbyte, onClose, onSuccess }: Props) {
  const { t } = useTranslation()
  const [satPerVbyte, setSatPerVbyte] = useState(String(defaultSatPerVbyte ?? 10))
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [needsPassword, setNeedsPassword] = useState(false)
  const [confirmPassword, setConfirmPassword] = useState('')

  const submit = async () => {
    setError('')
    const rate = Number(satPerVbyte)
    if (!Number.isFinite(rate) || rate <= 0) {
      setError(t('onchainHub.bump.errorInvalidFeeRate'))
      return
    }
    setSubmitting(true)
    try {
      await bumpUtxoFee({
        outpoint,
        sat_per_vbyte: rate,
        confirm_password: confirmPassword ? confirmPassword : undefined
      })
      onSuccess()
    } catch (err: any) {
      const code = err?.code || err?.body?.code
      if (
        err?.status === 428 ||
        code === 'utxo_bump_reauth_required' ||
        err?.body?.requires_password_confirmation
      ) {
        setNeedsPassword(true)
        setError(t('onchainHub.bump.errorReauthRequired'))
      } else if (code === 'auth_invalid_credentials') {
        setError(t('onchainHub.bump.errorInvalidPassword'))
      } else {
        setError(err?.message || t('onchainHub.bump.errorGeneric'))
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
      <button
        type="button"
        className="absolute inset-0 bg-black/75 backdrop-blur-sm"
        onClick={() => (!submitting ? onClose() : undefined)}
        aria-label={t('common.close')}
      />
      <div className="relative z-10 w-full max-w-md rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel">
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-xs uppercase tracking-[0.3em] text-fog/50">{t('onchainHub.utxoMgr.kicker')}</p>
            <h2 className="mt-3 text-xl font-semibold">{t('onchainHub.bump.title')}</h2>
            <p className="mt-2 text-xs text-fog/65 break-all">{outpoint}</p>
          </div>
          <button
            type="button"
            className="inline-flex h-10 w-10 items-center justify-center rounded-full border border-white/10 bg-ink/50 text-fog/70 hover:text-white"
            onClick={() => (!submitting ? onClose() : undefined)}
          >
            ✕
          </button>
        </div>

        <div className="mt-5 space-y-3">
          <p className="text-xs text-fog/60">
            {t('onchainHub.bump.description')}
          </p>
          <div>
            <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.bump.newFeeRate')}</label>
            <input
              className="input-field mt-1"
              inputMode="decimal"
              value={satPerVbyte}
              onChange={(e) => setSatPerVbyte(e.target.value.replace(/[^\d.]/g, ''))}
            />
          </div>
          {needsPassword && (
            <div>
              <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.utxoMgr.confirmPassword')}</label>
              <input
                type="password"
                className="input-field mt-1"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                autoFocus
              />
            </div>
          )}
          {error && <p className="text-xs text-ember">{error}</p>}
        </div>

        <div className="mt-6 flex flex-wrap items-center justify-end gap-2">
          <button
            type="button"
            className="btn-secondary text-xs px-3 py-2"
            onClick={() => (!submitting ? onClose() : undefined)}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            className="btn-primary text-xs px-3 py-2"
            onClick={submit}
            disabled={submitting}
          >
            {submitting ? t('onchainHub.bump.submitting') : t('onchainHub.utxoMgr.actionBumpFee')}
          </button>
        </div>
      </div>
    </div>
  )
}
