import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { type AuthState, loginAuth, recoverAuth, setupAuth } from '../api'
import brlnLightningOsLogo from '../assets/brln-lightning-os.svg'

type AuthScreenProps = {
  state: AuthState
  onAuthenticated: (state: AuthState) => void
}

type AuthMode = 'setup' | 'login' | 'recovery'

export default function AuthScreen({ state, onAuthenticated }: AuthScreenProps) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<AuthMode>(state.setup_required ? 'setup' : 'login')
  const [setupToken, setSetupToken] = useState('')
  const [recoveryToken, setRecoveryToken] = useState('')
  const [loginPassword, setLoginPassword] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [setupCommandCopied, setSetupCommandCopied] = useState(false)

  useEffect(() => {
    if (state.setup_required) {
      setMode('setup')
      return
    }
    setMode((current) => (current === 'setup' ? 'login' : current))
  }, [state.setup_required])

  useEffect(() => {
    setError('')
  }, [mode])

  useEffect(() => {
    setSetupCommandCopied(false)
  }, [mode])

  const copySetupCommand = async () => {
    try {
      await navigator.clipboard.writeText(t('auth.setupCommand'))
      setSetupCommandCopied(true)
      window.setTimeout(() => setSetupCommandCopied(false), 2000)
    } catch {
      setError(t('common.copyFailedManual'))
    }
  }

  const handleSetup = async () => {
    setBusy(true)
    setError('')
    try {
      const next = await setupAuth({
        setup_token: setupToken,
        password,
        confirm_password: confirmPassword
      })
      onAuthenticated(next)
    } catch (err: any) {
      setError(err?.message || t('auth.setupFailed'))
    } finally {
      setBusy(false)
    }
  }

  const handleLogin = async () => {
    setBusy(true)
    setError('')
    try {
      const next = await loginAuth({ password: loginPassword })
      onAuthenticated(next)
    } catch (err: any) {
      setError(err?.message || t('auth.loginFailed'))
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
    } catch (err: any) {
      setError(err?.message || t('auth.recoveryFailed'))
    } finally {
      setBusy(false)
    }
  }

  const title = mode === 'setup'
    ? t('auth.setupTitle')
    : mode === 'recovery'
      ? t('auth.recoveryTitle')
      : t('auth.loginTitle')

  const subtitle = mode === 'setup'
    ? t('auth.setupSubtitle')
    : mode === 'recovery'
      ? t('auth.recoverySubtitle')
      : t('auth.loginSubtitle')

  return (
    <div className="min-h-screen px-6 py-10 lg:px-12">
      <div className="mx-auto grid max-w-6xl gap-6 lg:grid-cols-[1.15fr_0.85fr]">
        <section className="section-card flex flex-col justify-between gap-6">
          <div className="grid gap-6 lg:grid-cols-[0.85fr_1.15fr] lg:items-center">
            <div className="space-y-4">
              <p className="text-sm uppercase tracking-[0.3em] text-fog/50">{t('auth.kicker')}</p>
              <h1 className="text-3xl font-semibold lg:text-4xl">{t('auth.heroTitle')}</h1>
              <p className="max-w-2xl text-fog/70">{t('auth.heroBody')}</p>
            </div>
            <div className="flex items-center justify-center rounded-[2rem] border border-white/10 bg-white/[0.03] px-6 py-8">
              <img
                src={brlnLightningOsLogo}
                alt="BRLN Lightning OS"
                className="max-h-64 w-full max-w-lg object-contain drop-shadow-[0_24px_48px_rgba(255,207,32,0.18)]"
              />
            </div>
          </div>
          <div className="grid gap-4 sm:grid-cols-3">
            <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
              <p className="text-xs uppercase tracking-[0.2em] text-fog/50">{t('auth.cardProtected')}</p>
              <p className="mt-2 text-sm text-fog/80">{t('auth.cardProtectedBody')}</p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
              <p className="text-xs uppercase tracking-[0.2em] text-fog/50">{t('auth.cardRecovery')}</p>
              <p className="mt-2 text-sm text-fog/80">{t('auth.cardRecoveryBody')}</p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
              <p className="text-xs uppercase tracking-[0.2em] text-fog/50">{t('auth.cardAutomation')}</p>
              <p className="mt-2 text-sm text-fog/80">{t('auth.cardAutomationBody')}</p>
            </div>
          </div>
        </section>

        <section className="section-card space-y-5">
          {!state.setup_required && (
            <div className="inline-flex rounded-2xl border border-white/10 bg-ink/40 p-1">
              <button
                type="button"
                className={mode === 'login' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'}
                onClick={() => setMode('login')}
              >
                {t('auth.loginTab')}
              </button>
              <button
                type="button"
                className={mode === 'recovery' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'}
                onClick={() => setMode('recovery')}
              >
                {t('auth.recoveryTab')}
              </button>
            </div>
          )}

          <div>
            <h2 className="text-2xl font-semibold">{title}</h2>
            <p className="mt-2 text-sm text-fog/65">{subtitle}</p>
          </div>

          {error && (
            <div className="rounded-2xl border border-rose-400/30 bg-rose-500/15 px-4 py-3 text-sm text-rose-200">
              {error}
            </div>
          )}

          {mode === 'setup' && (
            <div className="space-y-4">
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 text-sm text-fog/75">
                <p>{state.setup_token_issued ? t('auth.setupTokenIssued') : t('auth.setupTokenMissing')}</p>
                <div className="mt-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <p className="font-mono text-xs text-fog/65 break-all">{t('auth.setupCommand')}</p>
                  <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={copySetupCommand}>
                    {setupCommandCopied ? t('common.copied') : t('common.copy')}
                  </button>
                </div>
              </div>
              <div className="space-y-2">
                <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.setupTokenLabel')}</label>
                <input
                  className="input-field"
                  value={setupToken}
                  onChange={(event) => setSetupToken(event.target.value)}
                  placeholder={t('auth.setupTokenPlaceholder')}
                />
              </div>
              <div className="space-y-2">
                <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.passwordLabel')}</label>
                <input
                  className="input-field"
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  placeholder={t('auth.passwordPlaceholder')}
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
                />
              </div>
              <p className="text-xs text-fog/55">{t('auth.passwordHint')}</p>
              <button className="btn-primary w-full justify-center" type="button" onClick={handleSetup} disabled={busy}>
                {busy ? t('auth.settingUp') : t('auth.setupAction')}
              </button>
            </div>
          )}

          {mode === 'login' && (
            <div className="space-y-4">
              <div className="space-y-2">
                <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.passwordLabel')}</label>
                <input
                  className="input-field"
                  type="password"
                  value={loginPassword}
                  onChange={(event) => setLoginPassword(event.target.value)}
                  placeholder={t('auth.passwordPlaceholder')}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') {
                      void handleLogin()
                    }
                  }}
                />
              </div>
              <button className="btn-primary w-full justify-center" type="button" onClick={handleLogin} disabled={busy}>
                {busy ? t('auth.signingIn') : t('auth.loginAction')}
              </button>
            </div>
          )}

          {mode === 'recovery' && (
            <div className="space-y-4">
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 text-sm text-fog/75">
                <p>{state.recovery_token_issued ? t('auth.recoveryTokenIssued') : t('auth.recoveryTokenMissing')}</p>
                <p className="mt-3 font-mono text-xs text-fog/65">{t('auth.recoveryCommand')}</p>
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
                <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('auth.passwordLabel')}</label>
                <input
                  className="input-field"
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  placeholder={t('auth.passwordPlaceholder')}
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
                />
              </div>
              <button className="btn-primary w-full justify-center" type="button" onClick={handleRecovery} disabled={busy}>
                {busy ? t('auth.resettingPassword') : t('auth.recoveryAction')}
              </button>
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
