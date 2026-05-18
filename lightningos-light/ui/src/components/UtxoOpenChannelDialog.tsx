import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { openChannel, previewOpenChannel } from '../api'
import { computePrivacyWarnings, type UtxoLike } from '../utils/utxoPrivacy'

type Props = {
  selection: UtxoLike[]
  defaultSatPerVbyte?: number
  onClose: () => void
  onSuccess: (channelPoint: string) => void
}

export default function UtxoOpenChannelDialog({
  selection,
  defaultSatPerVbyte,
  onClose,
  onSuccess
}: Props) {
  const { t } = useTranslation()
  const totalSat = useMemo(() => selection.reduce((a, u) => a + (u.amount_sat || 0), 0), [selection])
  const [peerAddress, setPeerAddress] = useState('')
  const [localFundingSat, setLocalFundingSat] = useState(String(Math.max(totalSat - 5000, 0)))
  const [pushSat, setPushSat] = useState('0')
  const [satPerVbyte, setSatPerVbyte] = useState(String(defaultSatPerVbyte ?? 5))
  const [isPrivate, setIsPrivate] = useState(false)
  const [closeAddress, setCloseAddress] = useState('')
  const [preview, setPreview] = useState<any>(null)
  const [previewing, setPreviewing] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const outpoints = useMemo(() => selection.map((u) => u.outpoint), [selection])
  const warnings = useMemo(() => computePrivacyWarnings(selection), [selection])

  const runPreview = async () => {
    setPreviewError('')
    setPreview(null)
    const local = Number(localFundingSat)
    const push = Number(pushSat)
    const rate = Number(satPerVbyte)
    if (!Number.isFinite(local) || local <= 0) {
      setPreviewError(t('onchainHub.openChannel.errorInvalidLocalFunding'))
      return
    }
    if (!Number.isFinite(rate) || rate <= 0) {
      setPreviewError(t('onchainHub.openChannel.errorInvalidFeeRate'))
      return
    }
    setPreviewing(true)
    try {
      const res: any = await previewOpenChannel({
        local_funding_sat: local,
        push_sat: push || 0,
        sat_per_vbyte: rate
      })
      setPreview(res)
    } catch (err: any) {
      setPreviewError(err?.message || t('onchainHub.openChannel.errorPreviewFailed'))
    } finally {
      setPreviewing(false)
    }
  }

  const submit = async () => {
    setError('')
    const peer = peerAddress.trim()
    if (!peer) {
      setError(t('onchainHub.openChannel.errorPeerRequired'))
      return
    }
    const local = Number(localFundingSat)
    if (!Number.isFinite(local) || local <= 0) {
      setError(t('onchainHub.openChannel.errorInvalidLocalFunding'))
      return
    }
    const push = Number(pushSat) || 0
    if (push > local) {
      setError(t('onchainHub.openChannel.errorPushExceedsLocal'))
      return
    }
    const rate = Number(satPerVbyte)
    if (!Number.isFinite(rate) || rate <= 0) {
      setError(t('onchainHub.openChannel.errorInvalidFeeRate'))
      return
    }
    setSubmitting(true)
    try {
      const res: any = await openChannel({
        peer_address: peer,
        local_funding_sat: local,
        push_sat: push,
        sat_per_vbyte: rate,
        private: isPrivate,
        close_address: closeAddress.trim() || undefined,
        outpoints
      })
      onSuccess(res?.channel_point || 'ok')
    } catch (err: any) {
      setError(err?.message || t('onchainHub.openChannel.errorOpenFailed'))
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
      <div className="relative z-10 w-full max-w-xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel">
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-xs uppercase tracking-[0.3em] text-fog/50">{t('onchainHub.utxoMgr.kicker')}</p>
            <h2 className="mt-3 text-2xl font-semibold">{t('onchainHub.openChannel.title')}</h2>
            <p className="mt-2 text-sm text-fog/65">
              {t('onchainHub.openChannel.summary', { count: selection.length, sats: totalSat.toLocaleString() })}
            </p>
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
            <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.openChannel.peerLabel')}</label>
            <input
              className="input-field mt-1"
              placeholder={t('onchainHub.openChannel.peerPlaceholder')}
              value={peerAddress}
              onChange={(e) => setPeerAddress(e.target.value)}
            />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.openChannel.localFunding')}</label>
              <input
                className="input-field mt-1"
                inputMode="numeric"
                value={localFundingSat}
                onChange={(e) => setLocalFundingSat(e.target.value.replace(/[^\d]/g, ''))}
              />
            </div>
            <div>
              <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.openChannel.pushSats')}</label>
              <input
                className="input-field mt-1"
                inputMode="numeric"
                value={pushSat}
                onChange={(e) => setPushSat(e.target.value.replace(/[^\d]/g, ''))}
              />
            </div>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.utxoMgr.feeRateLabel')}</label>
              <input
                className="input-field mt-1"
                inputMode="decimal"
                value={satPerVbyte}
                onChange={(e) => setSatPerVbyte(e.target.value.replace(/[^\d.]/g, ''))}
              />
            </div>
            <div>
              <label className="text-xs uppercase tracking-wide text-fog/50">{t('onchainHub.openChannel.closeAddress')}</label>
              <input
                className="input-field mt-1"
                placeholder={t('onchainHub.spend.placeholderExternalAddress')}
                value={closeAddress}
                onChange={(e) => setCloseAddress(e.target.value)}
              />
            </div>
          </div>
          <label className="flex items-center gap-2 text-xs text-fog/70">
            <input
              type="checkbox"
              checked={isPrivate}
              onChange={(e) => setIsPrivate(e.target.checked)}
            />
            {t('onchainHub.openChannel.privateChannel')}
          </label>

          {warnings.length > 0 && (
            <div className="rounded-2xl border border-brass/40 bg-brass/10 p-3 text-xs space-y-1">
              <p className="text-brass font-semibold">{t('onchainHub.privacy.notesHeading')}</p>
              {warnings.map((w, i) => (
                <p key={i} className="text-brass">{t(w.key, w.params)}</p>
              ))}
            </div>
          )}

          {preview && (
            <div className="rounded-2xl border border-white/10 bg-ink/40 p-3 text-xs space-y-1">
              <p>{t('onchainHub.openChannel.previewFundingFee')}: <span className="text-fog">{t('onchainHub.utxoMgr.satsValue', { value: Number(preview.fee_sat || 0).toLocaleString() })}</span></p>
              {preview.message && (
                <p className={preview.enough_funds === false ? 'text-ember' : 'text-fog/70'}>{preview.message}</p>
              )}
            </div>
          )}
          {previewError && <p className="text-xs text-ember">{previewError}</p>}
          {error && <p className="text-xs text-ember">{error}</p>}
        </div>

        <div className="mt-6 flex flex-wrap items-center justify-end gap-2">
          <button
            type="button"
            className="btn-secondary text-xs px-3 py-2"
            onClick={runPreview}
            disabled={previewing || submitting}
          >
            {previewing ? t('onchainHub.utxoMgr.previewing') : t('onchainHub.utxoMgr.preview')}
          </button>
          <button
            type="button"
            className="btn-primary text-xs px-3 py-2"
            onClick={submit}
            disabled={submitting}
          >
            {submitting ? t('onchainHub.openChannel.submitting') : t('onchainHub.openChannel.cta')}
          </button>
        </div>
      </div>
    </div>
  )
}
