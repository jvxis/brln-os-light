import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { APIError, changePasswordAuth, recoverAuth, type AuthState } from '../api'

type AccountSecurityModalProps = {
  open: boolean
  state: AuthState
  onClose: () => void
  onAuthenticated: (state: AuthState) => void
  onRefreshState?: () => Promise<AuthState | void>
}

type AccountSecurityMode = 'change' | 'recovery'

export default function AccountSecurityModal({
  open,
  state,
  onClose,
  onAuthenticated,
  onRefreshState
}: AccountSecurityModalProps) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<AccountSecurityMode>('change')
  const [currentPassword, setCurrentPassword] = useState('')
  const [recoveryToken, setRecoveryToken] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) {
      setMode('change')
      setCurrentPassword('')
      setRecoveryToken('')
      setPassword('')
      setConfirmPassword('')
      setBusy(false)
      setRefreshing(false)
      setError('')
      return
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) {
        onClose()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [open, busy, onClose])

  useEffect(() => {
    setError('')
  }, [mode])

  if (!open) {
    return null
  }

  const resolveErrorMessage = (err: unknown, fallbackKey: string) => {
    if (err instanceof APIError) {
      if (err.code === 'auth_invalid_credentials' && mode === 'change') {
        return t('auth.currentPasswordInvalid')
      }
      if (err.code === 'auth_required') {
        return t('auth.sessionExpired')
      }
      return err.message || t(fallbackKey)
    }
    if (err instanceof Error && err.message.trim()) {
      return err.message
    }
    return t(fallbackKey)
  }

  const handleChangePassword = async () => {
    setBusy(true)
    setError('')
    try {
      const next = await changePasswordAuth({
        current_password: currentPassword,
        password,
        confirm_password: confirmPassword
      })
      onAuthenticated(next)
      onClose()
    } catch (err) {
      setError(resolveErrorMessage(err, 'auth.changePasswordFailed'))
    } finally {
      setBusy(false)
    }
  }

  const handleRecovery = async () => {
    setBusy(true)
    setError('')
    try {
      const next = await recoverAuth({
        recovery_token: recoveryToken,
        password,
        confirm_password: confirmPassword
      })
      onAuthenticated(next)
      onClose()
    } catch (err) {
      setError(resolveErrorMessage(err, 'auth.recoveryFailed'))
    } finally {
      setBusy(false)
    }
  }

  const handleRefreshState = async () => {
    if (!onRefreshState) {
      return
    }
    setRefreshing(true)
    setError('')
    try {
      await onRefreshState()
    } catch (err) {
      setError(resolveErrorMessage(err, 'auth.stateRefreshFailed'))
    } finally {
      setRefreshing(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
      <button
        type="button"
        className="absolute inset-0 bg-black/75 backdrop-blur-sm"
        onClick={() => {
          if (!busy) {
            onClose()
          }
        }}
        aria-label={t('common.close')}
      />
      <div className="relative z-10 w-full max-w-xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel">
        <div className="flex items-start justify-between gap-4">
          <div>
            <p className="text-xs uppercase tracking-[0.3em] text-fog/50">{t('auth.accountSecurityKicker')}</p>
            <h2 className="mt-3 text-2xl font-semibold">
              {mode === 'change' ? t('auth.changePasswordTitle') : t('auth.recoveryTitle')}
            </h2>
            <p className="mt-2 text-sm text-fog/65">
              {mode === 'change' ? t('auth.changePasswordSubtitle') : t('auth.recoverySubtitle')}
            </p>
          </div>
          <button
            type="button"
            className="inline-flex h-10 w-10 items-center justify-center rounded-full border border-white/10 bg-ink/50 text-fog/70 transition hover:border-white/30 hover:text-white"
            onClick={() => {
              if (!busy) {
                onClose()
              }
            }}
            aria-label={t('common.close')}
          >
            <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8">
              <path d="M6 6l12 12M18 6l-12 12" />
            </svg>
          </button>
        </div>

        {error && (
          <div className="mt-5 rounded-2xl border border-rose-400/30 bg-rose-500/15 px-4 py-3 text-sm text-rose-200">
            {error}
          </div>
        )}

        {mode === 'change' && (
          <div className="mt-6 space-y-4">
            <div className="rounded-2xl border border-emerald-400/30 bg-emerald-500/15 px-4 py-3 text-sm text-emerald-100">
              {t('auth.changePasswordNote')}
            </div>
            <div className="space-y-2">
              <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.currentPasswordLabel')}</label>
              <input
                className="input-field"
                type="password"
                value={currentPassword}
                onChange={(event) => setCurrentPassword(event.target.value)}
                placeholder={t('auth.currentPasswordPlaceholder')}
              />
            </div>
            <div className="space-y-2">
              <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.newPasswordLabel')}</label>
              <input
                className="input-field"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder={t('auth.newPasswordPlaceholder')}
              />
            </div>
            <div className="space-y-2">
              <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.confirmPasswordLabel')}</label>
              <input
                className="input-field"
                type="password"
                value={confirmPassword}
                onChange={(event) => setConfirmPassword(event.target.value)}
                placeholder={t('auth.confirmPasswordPlaceholder')}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    void handleChangePassword()
                  }
                }}
              />
            </div>
            <p className="text-xs text-fog/55">{t('auth.passwordHint')}</p>
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <button
                type="button"
                className="text-sm text-fog/65 transition hover:text-white"
                onClick={() => setMode('recovery')}
              >
                {t('auth.forgotPasswordAction')}
              </button>
              <div className="flex gap-3">
                <button className="btn-secondary" type="button" onClick={onClose} disabled={busy}>
                  {t('common.cancel')}
                </button>
                <button className="btn-primary" type="button" onClick={handleChangePassword} disabled={busy}>
                  {busy ? t('auth.changingPassword') : t('auth.changePasswordAction')}
                </button>
              </div>
            </div>
          </div>
        )}

        {mode === 'recovery' && (
          <div className="mt-6 space-y-4">
            <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 text-sm text-fog/75">
              <p>{state.recovery_token_issued ? t('auth.recoveryTokenIssued') : t('auth.recoveryTokenMissing')}</p>
              <p className="mt-3 font-mono text-xs text-fog/65">{t('auth.recoveryCommand')}</p>
              <div className="mt-4 flex justify-end">
                <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={handleRefreshState} disabled={refreshing}>
                  {refreshing ? t('auth.refreshingState') : t('common.refresh')}
                </button>
              </div>
            </div>
            <div className="space-y-2">
              <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.recoveryTokenLabel')}</label>
              <input
                className="input-field"
                value={recoveryToken}
                onChange={(event) => setRecoveryToken(event.target.value)}
                placeholder={t('auth.recoveryTokenPlaceholder')}
              />
            </div>
            <div className="space-y-2">
              <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.newPasswordLabel')}</label>
              <input
                className="input-field"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder={t('auth.newPasswordPlaceholder')}
              />
            </div>
            <div className="space-y-2">
              <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.confirmPasswordLabel')}</label>
              <input
                className="input-field"
                type="password"
                value={confirmPassword}
                onChange={(event) => setConfirmPassword(event.target.value)}
                placeholder={t('auth.confirmPasswordPlaceholder')}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    void handleRecovery()
                  }
                }}
              />
            </div>
            <p className="text-xs text-fog/55">{t('auth.passwordHint')}</p>
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <button
                type="button"
                className="text-sm text-fog/65 transition hover:text-white"
                onClick={() => setMode('change')}
              >
                {t('auth.backToChangePassword')}
              </button>
              <div className="flex gap-3">
                <button className="btn-secondary" type="button" onClick={onClose} disabled={busy}>
                  {t('common.cancel')}
                </button>
                <button className="btn-primary" type="button" onClick={handleRecovery} disabled={busy}>
                  {busy ? t('auth.resettingPassword') : t('auth.recoveryAction')}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
