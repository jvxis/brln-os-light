import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { previewLNURLWithdraw, redeemLNURLWithdraw, type LNURLWithdrawPreview, type LNURLWithdrawResult } from '../api'

export default function LNURLWithdraw() {
  const { t } = useTranslation()
  const [lnurl, setLNURL] = useState('')
  const [preview, setPreview] = useState<LNURLWithdrawPreview | null>(null)
  const [amount, setAmount] = useState('')
  const [result, setResult] = useState<LNURLWithdrawResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [attempted, setAttempted] = useState(false)
  const errorMessage = (err: unknown) => {
    const code = err instanceof Error ? err.message : ''
    const key = `wallet.lnurlWithdraw.errors.${code}`
    return t(key, { defaultValue: t('wallet.lnurlWithdraw.failed') })
  }
  const inspect = async () => {
    setBusy(true)
    setError('')
    setResult(null)
    try {
      const next = await previewLNURLWithdraw(lnurl.trim())
      setPreview(next)
      setAmount(String(next.max_sat))
      setLNURL('') // The voucher is a bearer credential; do not retain it in the form.
      setAttempted(false)
    } catch (err) { setError(errorMessage(err)) }
    finally { setBusy(false) }
  }
  const redeem = async () => {
    if (!preview) return
    setBusy(true)
    setError('')
    setAttempted(true)
    try { setResult(await redeemLNURLWithdraw(preview.token, Number(amount))) }
    catch (err) { setError(errorMessage(err)) }
    finally { setBusy(false) }
  }
  const amountValid = /^\d+$/.test(amount) && Number.isSafeInteger(Number(amount)) && !!preview && Number(amount) >= preview.min_sat && Number(amount) <= preview.max_sat
  return (
    <div className="space-y-3 border-t border-white/10 pt-4">
      <h4 className="font-semibold">{t('wallet.lnurlWithdraw.title')}</h4>
      <p className="text-xs text-fog/60">{t('wallet.lnurlWithdraw.hint')}</p>
      {!preview ? <>
        <label className="block text-xs text-fog/70" htmlFor="lnurl-withdraw-input">{t('wallet.lnurlWithdraw.voucher')}</label>
        <textarea id="lnurl-withdraw-input" className="input-field min-h-[80px]" value={lnurl} disabled={busy} autoComplete="off" spellCheck={false} onChange={e => setLNURL(e.target.value)} placeholder="LNURL1…" />
        <button className="btn-secondary" disabled={busy || !lnurl.trim()} onClick={inspect}>{t(busy ? 'wallet.lnurlWithdraw.busy' : 'wallet.lnurlWithdraw.inspect')}</button>
      </> : <div className="space-y-3 rounded-2xl border border-white/10 bg-ink/60 p-3">
        <dl className="space-y-1 break-words text-sm">
          <div><dt className="text-xs text-fog/60">{t('wallet.lnurlWithdraw.service')}</dt><dd>{preview.service}</dd></div>
          {preview.callback_service !== preview.service && <div><dt className="text-xs text-fog/60">{t('wallet.lnurlWithdraw.callbackService')}</dt><dd>{preview.callback_service}</dd></div>}
          {preview.description && <div><dt className="text-xs text-fog/60">{t('wallet.memo')}</dt><dd>{preview.description}</dd></div>}
          <div><dt className="text-xs text-fog/60">{t('wallet.lnurlWithdraw.range')}</dt><dd>{preview.min_sat}–{preview.max_sat} sats</dd></div>
        </dl>
        <label className="block text-xs text-fog/70" htmlFor="lnurl-withdraw-amount">{t('wallet.amountSats')}</label>
        <input id="lnurl-withdraw-amount" className="input-field" type="number" step="1" min={preview.min_sat} max={preview.max_sat} value={amount} disabled={busy || attempted} onChange={e => setAmount(e.target.value)} />
        <p className="text-xs text-fog/60">{t('wallet.lnurlWithdraw.confirmHint')}</p>
        {result && <p role="status" className="text-sm text-brass">{t(`wallet.lnurlWithdraw.${result.status}`)}</p>}
        {result && <p className="break-all font-mono text-xs text-fog/60">{t('wallet.lnurlWithdraw.paymentHash')}: {result.payment_hash}</p>}
        {attempted && <p className="text-xs text-fog/60">{t('wallet.lnurlWithdraw.restartHint')}</p>}
        <div className="flex flex-wrap gap-2">
          {result?.status !== 'accepted' && <button className="btn-primary" disabled={busy || !amountValid} onClick={redeem}>{t(busy ? 'wallet.lnurlWithdraw.busy' : attempted ? 'wallet.lnurlWithdraw.retry' : 'wallet.lnurlWithdraw.confirm')}</button>}
          <button className="btn-secondary" disabled={busy} onClick={() => { setPreview(null); setResult(null); setError(''); setAttempted(false) }}>{t('common.close')}</button>
        </div>
      </div>}
      {error && <p role="alert" className="text-sm text-ember">{error}</p>}
    </div>
  )
}
