import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { bakeLnMacaroon, ensureTorLndRest, getTorLndRest, type LnMacaroonBakeResult, type TorLndRestStatus } from '../../api'

// LND REST over a Tor onion service, for Lightning Address services such as
// pay.br-ln.com. The broker owns the torrc block and the reload; this only
// asks for the fixed status and the fixed "create if missing" operation.

export default function TorLndRestOnion() {
  const { t } = useTranslation()
  const [state, setState] = useState<TorLndRestStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [statusFailed, setStatusFailed] = useState(false)
  const [copied, setCopied] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [password, setPassword] = useState('')
  const [expanded, setExpanded] = useState(false)
  const [invoice, setInvoice] = useState<LnMacaroonBakeResult | null>(null)
  const [invoicePassword, setInvoicePassword] = useState('')
  const [invoiceConfirmed, setInvoiceConfirmed] = useState(false)
  const [invoiceCopied, setInvoiceCopied] = useState(false)

  const clearInvoice = () => {
    setInvoice(null)
    setInvoicePassword('')
    setInvoiceConfirmed(false)
    setInvoiceCopied(false)
  }

  const toggle = () => {
    if (busy) return
    setExpanded(value => !value)
    setPassword('')
    setConfirmed(false)
    clearInvoice()
  }

  const generateInvoice = async () => {
    if (busy || !invoiceConfirmed || !invoicePassword.trim()) return
    setBusy(true)
    setError('')
    setInvoice(null)
    setInvoiceCopied(false)
    try {
      setInvoice(await bakeLnMacaroon({ preset: 'invoice_permissions', confirm_password: invoicePassword }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('lightningOps.macaroonGenerateFailed'))
    } finally {
      setInvoicePassword('')
      setInvoiceConfirmed(false)
      setBusy(false)
    }
  }

  const copyInvoice = async () => {
    if (!invoice) return
    try {
      await navigator.clipboard.writeText(invoice.macaroon_base64)
      setInvoiceCopied(true)
    } catch {
      setError(t('common.copyFailedManual'))
    }
  }

  const load = async () => {
    try {
      setState(await getTorLndRest())
      setStatusFailed(false)
      setError('')
    } catch (err) {
      setStatusFailed(true)
      setError(err instanceof Error ? err.message : t('torUpgrade.lndRest.failed'))
    }
  }

  useEffect(() => {
    void load()
  }, [])

  const create = async () => {
    if (!confirmed || busy) return
    setBusy(true)
    setError('')
    try {
      setState(await ensureTorLndRest(password))
      setStatusFailed(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('torUpgrade.lndRest.failed'))
    } finally {
      setBusy(false)
      setPassword('')
    }
  }

  const copy = async () => {
    if (!state?.url) return
    try {
      await navigator.clipboard.writeText(state.url)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      setError(t('common.copyFailedManual'))
    }
  }

  const ready = state?.status === 'configured' && !!state.url
  const statusKey = !state || statusFailed ? 'unknown' : ready ? (state.rest_reachable ? 'configured' : 'unreachable') : state.status === 'pending' ? 'waiting' : 'absent'
  return (
    <div className="space-y-3 rounded-2xl border border-white/10 bg-ink/40 px-4 py-4">
      <button type="button" onClick={toggle} disabled={busy} aria-expanded={expanded} aria-controls="tor-lnd-rest-settings" className="flex w-full flex-wrap items-center justify-between gap-2 text-left">
        <span className="text-sm font-semibold text-fog">{t('torUpgrade.lndRest.title')}</span>
        <span className="flex items-center gap-2 text-xs text-fog/70">
          <span role="status">{t(`torUpgrade.lndRest.${statusKey}`)}</span>
          <span>{expanded ? t('common.hide') : t('torUpgrade.lndRest.configure')}</span>
        </span>
      </button>
      {expanded && <div id="tor-lnd-rest-settings" className="space-y-3">
        <p className="mt-1 text-xs text-fog/60">{t('torUpgrade.lndRest.subtitle')}</p>
      <button type="button" className="btn-secondary" disabled={busy} onClick={() => void load()}>{t('common.refresh')}</button>

      {ready ? (
        <div className="space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <code className="break-all rounded-xl border border-white/10 bg-ink/70 px-3 py-2 text-xs text-fog">{state.url}</code>
            <button className="btn-secondary" onClick={() => void copy()} type="button">
              {copied ? t('common.copied') : t('common.copy')}
            </button>
          </div>
          {!state.managed && <p className="text-xs text-fog/50">{t('torUpgrade.lndRest.manual')}</p>}
        </div>
      ) : (
        <div className="space-y-3">
          <label className="flex items-start gap-2 text-xs text-amber-200">
            <input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} disabled={busy} />
            {t('torUpgrade.lndRest.confirm')}
          </label>
          <label className="block text-xs text-fog/70">
            {t('torUpgrade.lndRest.password')}
            <input type="password" autoComplete="current-password" value={password} onChange={event => setPassword(event.target.value)} disabled={busy} className="input mt-1 w-full" />
          </label>
          <button
            className="btn-secondary"
            onClick={() => void create()}
            disabled={busy || !confirmed || !state || state.rest_reachable === false}
            type="button"
          >
            {busy ? t('torUpgrade.lndRest.creating') : t('torUpgrade.lndRest.create')}
          </button>
          {state?.status === 'pending' && <span className="text-xs text-brass">{t('torUpgrade.lndRest.pending')}</span>}
          {state?.rest_reachable === false && <span className="text-xs text-amber-200">{t('torUpgrade.lndRest.restDown')}</span>}
        </div>
      )}

      {error && <p className="text-xs text-rose-200">{error}</p>}
      {ready && !state.rest_reachable && <p className="text-xs text-amber-200">{t('torUpgrade.lndRest.restDown')}</p>}
      <p className="text-xs text-fog/60">{t('torUpgrade.lndRest.tls')}</p>
      <p className="text-xs text-amber-200/90">{t('torUpgrade.lndRest.macaroon')}</p>
      {ready && <div className="space-y-3 border-t border-white/10 pt-3">
        <h5 className="text-sm font-semibold">{t('torUpgrade.lndRest.invoiceTitle')}</h5>
        <p className="text-xs text-fog/60">{t('torUpgrade.lndRest.invoiceHelp')}</p>
        {invoice ? <div className="space-y-2">
          <label className="block text-xs text-fog/70">
            {t('lightningOps.macaroonBase64')}
            <textarea readOnly className="input-field mt-1 min-h-[84px] w-full font-mono text-xs" value={invoice.macaroon_base64} spellCheck={false} />
          </label>
          <p className="text-xs text-fog/60">{t('lightningOps.macaroonRootKey')}: {invoice.root_key_id}</p>
          <p className="text-xs text-fog/60">{invoice.permissions.join(', ')}</p>
          <p className="text-xs text-amber-200">{t('torUpgrade.lndRest.invoiceOnce')}</p>
          <div className="flex flex-wrap gap-2">
            <button type="button" className="btn-secondary" onClick={() => void copyInvoice()}>{invoiceCopied ? t('common.copied') : t('lightningOps.macaroonCopyBase64')}</button>
            <button type="button" className="btn-secondary" onClick={clearInvoice}>{t('lightningOps.macaroonClear')}</button>
          </div>
        </div> : <div className="space-y-2">
          <label className="flex items-start gap-2 text-xs text-amber-200">
            <input type="checkbox" checked={invoiceConfirmed} onChange={event => setInvoiceConfirmed(event.target.checked)} disabled={busy} />
            {t('torUpgrade.lndRest.invoiceConfirm')}
          </label>
          <label className="block text-xs text-fog/70">
            {t('lightningOps.macaroonPassword')}
            <input type="password" autoComplete="current-password" value={invoicePassword} onChange={event => setInvoicePassword(event.target.value)} disabled={busy} className="input mt-1 w-full" />
          </label>
          <button type="button" className="btn-secondary" disabled={busy || !invoiceConfirmed || !invoicePassword.trim() || !state.rest_reachable} onClick={() => void generateInvoice()}>
            {busy ? t('lightningOps.macaroonGenerating') : t('torUpgrade.lndRest.invoiceGenerate')}
          </button>
        </div>}
      </div>}
      </div>}
    </div>
  )
}
