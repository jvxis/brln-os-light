import { FormEvent, useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { APIError, getTerminalStatus, rotateTerminalCredential } from '../api'

type TerminalStatus = {
  enabled: boolean
  credential_configured: boolean
  allow_write?: boolean
  port?: number
  operator_user?: string
}

type RotatedCredential = {
  operator_user: string
  password: string
  restart_pending: boolean
}

export default function Terminal() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<TerminalStatus | null>(null)
  const [statusMessage, setStatusMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [reauthOpen, setReauthOpen] = useState(false)
  const [adminPassword, setAdminPassword] = useState('')
  const [rotated, setRotated] = useState<RotatedCredential | null>(null)

  const load = useCallback(async () => {
    try {
      const data = await getTerminalStatus() as TerminalStatus
      setStatus(data)
      setStatusMessage('')
    } catch (err: any) {
      setStatus(null)
      setStatusMessage(err?.message || t('terminal.statusUnavailable'))
    }
  }, [t])

  useEffect(() => {
    void load()
  }, [load])

  const copyToClipboard = async (value: string) => {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      setStatusMessage(t('terminal.copyFailed'))
    }
  }

  const rotate = async (confirmPassword?: string) => {
    setBusy(true)
    setStatusMessage('')
    try {
      const result = await rotateTerminalCredential({ confirm_password: confirmPassword }) as RotatedCredential
      setRotated(result)
      setReauthOpen(false)
      setAdminPassword('')
      setStatus((current) => current ? { ...current, credential_configured: true } : current)
    } catch (err: any) {
      if (err instanceof APIError && err.code === 'terminal_credential_reauth_required') {
        setReauthOpen(true)
      } else {
        setStatusMessage(err?.message || t('terminal.rotateFailed'))
      }
    } finally {
      setBusy(false)
    }
  }

  const submitReauth = async (event: FormEvent) => {
    event.preventDefault()
    if (!adminPassword.trim()) return
    await rotate(adminPassword)
  }

  return (
    <div className="space-y-6">
      <div className="rounded-3xl border border-white/10 bg-ink/60 p-6 shadow-panel">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div>
            <p className="text-sm uppercase tracking-[0.2em] text-fog/60">{t('terminal.kicker')}</p>
            <h2 className="text-2xl font-semibold text-white">{t('terminal.title')}</h2>
            {statusMessage && <p className="mt-2 text-sm text-brass">{statusMessage}</p>}
            {status && (
              <div className="mt-4 space-y-2 text-sm text-fog/70">
                <div className="flex items-center gap-2">
                  <span className="text-fog/50">{t('common.status')}</span>
                  <span>{status.enabled ? t('common.enabled') : t('common.disabled')}</span>
                </div>
                {!status.enabled && <p className="text-brass">{t('terminal.disabledMessage')}</p>}
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-fog/50">{t('terminal.operator')}</span>
                  <span className="font-mono text-fog/80">{status.operator_user || 'losop'}</span>
                  <span className={`rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-wider ${status.credential_configured ? 'border-emerald-400/25 bg-emerald-400/10 text-emerald-200' : 'border-amber-400/25 bg-amber-400/10 text-amber-200'}`}>
                    {status.credential_configured ? t('terminal.credentialConfigured') : t('terminal.credentialMissing')}
                  </span>
                </div>
                <p className="text-xs text-fog/50">{t('terminal.credentialPrivate')}</p>
                <p className="text-xs text-fog/50">{t('terminal.pasteHint')}</p>
              </div>
            )}
          </div>
          <div className="flex flex-wrap gap-2">
            <button className="btn-secondary" type="button" disabled={busy} onClick={() => void rotate()}>
              {busy ? t('terminal.rotating') : t('terminal.rotateCredential')}
            </button>
            <a className="btn-secondary" href="/terminal/" target="_blank" rel="noreferrer">{t('terminal.openNewTab')}</a>
          </div>
        </div>
      </div>

      <div className="overflow-hidden rounded-3xl border border-white/10 bg-ink/70 shadow-panel">
        <iframe title={t('terminal.title')} src="/terminal/" className="h-[70vh] w-full bg-black" />
      </div>

      {reauthOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/85 p-4 backdrop-blur-sm">
          <form className="section-card w-full max-w-md" onSubmit={submitReauth}>
            <h3 className="text-xl font-semibold">{t('terminal.reauthTitle')}</h3>
            <p className="mt-2 text-sm leading-6 text-fog/65">{t('terminal.reauthBody')}</p>
            <input className="input-field mt-5" type="password" autoFocus required autoComplete="current-password" value={adminPassword} onChange={(event) => setAdminPassword(event.target.value)} />
            <div className="mt-5 flex justify-end gap-2">
              <button className="btn-secondary" type="button" onClick={() => { setReauthOpen(false); setAdminPassword('') }}>{t('common.cancel')}</button>
              <button className="btn-primary" disabled={busy} type="submit">{t('terminal.rotateCredential')}</button>
            </div>
          </form>
        </div>
      )}

      {rotated && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/85 p-4 backdrop-blur-sm">
          <div className="section-card w-full max-w-lg">
            <h3 className="text-xl font-semibold">{t('terminal.rotatedTitle')}</h3>
            <p className="mt-2 text-sm leading-6 text-fog/65">{t('terminal.rotatedBody')}</p>
            <div className="mt-5 rounded-2xl border border-brass/25 bg-brass/10 p-4">
              <p className="text-xs uppercase tracking-wider text-fog/50">{t('terminal.operator')}</p>
              <p className="mt-1 font-mono text-sm text-fog">{rotated.operator_user}</p>
              <p className="mt-4 text-xs uppercase tracking-wider text-fog/50">{t('terminal.password')}</p>
              <div className="mt-1 flex items-center justify-between gap-3">
                <p className="break-all font-mono text-sm text-white">{rotated.password}</p>
                <button className="btn-secondary shrink-0" type="button" onClick={() => void copyToClipboard(rotated.password)}>{t('common.copy')}</button>
              </div>
            </div>
            {rotated.restart_pending && <p className="mt-3 text-sm text-amber-200">{t('terminal.restartPending')}</p>}
            <p className="mt-3 text-xs leading-5 text-rose-200/80">{t('terminal.oneTimeWarning')}</p>
            <div className="mt-5 flex justify-end"><button className="btn-primary" type="button" onClick={() => setRotated(null)}>{t('common.close')}</button></div>
          </div>
        </div>
      )}
    </div>
  )
}
