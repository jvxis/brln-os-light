import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ensureTorLndRest, getTorLndRest, type TorLndRestStatus } from '../../api'

// LND REST over a Tor onion service, for Lightning Address services such as
// pay.br-ln.com. The broker owns the torrc block and the reload; this only
// asks for the fixed status and the fixed "create if missing" operation.

export default function TorLndRestOnion() {
  const { t } = useTranslation()
  const [state, setState] = useState<TorLndRestStatus | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [password, setPassword] = useState('')

  const load = async () => {
    try {
      setState(await getTorLndRest())
      setError('')
    } catch (err) {
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
  return (
    <div className="space-y-3 rounded-2xl border border-white/10 bg-ink/40 px-4 py-4">
      <div>
        <h4 className="text-sm font-semibold text-fog">{t('torUpgrade.lndRest.title')}</h4>
        <p className="mt-1 text-xs text-fog/60">{t('torUpgrade.lndRest.subtitle')}</p>
      </div>
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
    </div>
  )
}
