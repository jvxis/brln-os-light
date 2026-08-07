import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { type AuthState, type TLSAccessInfo, getTLSAccessInfo, loginAuth, recoverAuth, setupAuth } from '../api'
import brlnOsLogo from '../assets/brln-os.png'

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
  const [appVersion, setAppVersion] = useState('')
  const [featurePage, setFeaturePage] = useState(0)
  const [tlsAccess, setTLSAccess] = useState<TLSAccessInfo | null>(null)
  const [trustOpen, setTrustOpen] = useState(false)

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

  useEffect(() => {
    let mounted = true
    fetch('/version.txt', { cache: 'no-store' })
      .then((response) => response.ok ? response.text() : '')
      .then((version) => {
        if (mounted) setAppVersion(version.trim())
      })
      .catch(() => undefined)
    return () => {
      mounted = false
    }
  }, [])

  useEffect(() => {
    let mounted = true
    getTLSAccessInfo()
      .then((info) => {
        if (mounted && info?.available) setTLSAccess(info)
      })
      .catch(() => undefined)
    return () => {
      mounted = false
    }
  }, [])

  useEffect(() => {
    if (!trustOpen) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setTrustOpen(false)
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [trustOpen])

  useEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    const interval = window.setInterval(() => {
      setFeaturePage((current) => (current + 1) % 3)
    }, 6500)
    return () => window.clearInterval(interval)
  }, [])

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
    if (busy) return
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
    if (busy) return
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
    if (busy) return
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

  const featurePages = [
    [
      ['auth.featureLightningTitle', 'auth.featureLightningBody'],
      ['auth.featureAutomationTitle', 'auth.featureAutomationBody'],
      ['auth.featureWalletTitle', 'auth.featureWalletBody']
    ],
    [
      ['auth.featureNetworkTitle', 'auth.featureNetworkBody'],
      ['auth.featureReportsTitle', 'auth.featureReportsBody'],
      ['auth.featureAppsTitle', 'auth.featureAppsBody']
    ],
    [
      ['auth.featureRebalanceTitle', 'auth.featureRebalanceBody'],
      ['auth.featureOnchainTitle', 'auth.featureOnchainBody'],
      ['auth.featureOperationsTitle', 'auth.featureOperationsBody']
    ]
  ]

  return (
    <div className="auth-screen">
      <div className="auth-aurora auth-aurora--primary" aria-hidden="true" />
      <div className="auth-aurora auth-aurora--secondary" aria-hidden="true" />
      <div className="auth-grid" aria-hidden="true" />

      <div className="auth-stage">
        <main className="auth-shell">
          <section className="auth-hero">
            <div className="auth-hero__content">
              <div className="auth-hero__header">
                <p className="auth-kicker">
                  <span className="auth-kicker__dot" aria-hidden="true" />
                  {t('auth.kicker')}
                </p>
                <span className="auth-mainnet-pill">{t('auth.mainnet')}</span>
              </div>

              <div className="auth-hero__copy">
                <h1>{t('auth.heroTitle')}</h1>
              </div>

              <div className="auth-core">
                <div className="auth-core__halo" aria-hidden="true" />
                <div className="auth-core__orbit auth-core__orbit--outer" aria-hidden="true">
                  <span />
                </div>
                <div className="auth-core__orbit auth-core__orbit--inner" aria-hidden="true">
                  <span />
                </div>
                <div className="auth-core__logo">
                  <img src={brlnOsLogo} alt="BRLN Lightning OS" />
                </div>
                <div className="auth-core__signal" aria-hidden="true">
                  <i /><i /><i /><i /><i />
                </div>
              </div>
            </div>

            <div className="auth-feature-showcase">
              <div className="auth-feature-grid">
                {featurePages[featurePage].map(([titleKey, bodyKey], index) => (
                  <div className="auth-feature" key={`${featurePage}-${titleKey}`}>
                    <span className="auth-feature__icon" aria-hidden="true">
                      {String((featurePage * 3) + index + 1).padStart(2, '0')}
                    </span>
                    <div>
                      <p className="auth-feature__title">{t(titleKey)}</p>
                      <p>{t(bodyKey)}</p>
                    </div>
                  </div>
                ))}
              </div>
              <div className="auth-feature-pages" role="group" aria-label={t('auth.featurePages')}>
                {featurePages.map((_, index) => (
                  <button
                    type="button"
                    key={index}
                    className={index === featurePage ? 'auth-feature-page auth-feature-page--active' : 'auth-feature-page'}
                    aria-label={t('auth.featurePage', { page: index + 1 })}
                    aria-pressed={index === featurePage}
                    onClick={() => setFeaturePage(index)}
                  />
                ))}
              </div>
            </div>
          </section>

          <section className="auth-panel">
            <div className="auth-panel__beam" aria-hidden="true" />
            <div className="auth-panel__top">
              {!state.setup_required && (
                <div className="auth-tabs" role="tablist" aria-label={t('auth.accessMode')}>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={mode === 'login'}
                    className={mode === 'login' ? 'auth-tab auth-tab--active' : 'auth-tab'}
                    onClick={() => setMode('login')}
                  >
                    {t('auth.loginTab')}
                  </button>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={mode === 'recovery'}
                    className={mode === 'recovery' ? 'auth-tab auth-tab--active' : 'auth-tab'}
                    onClick={() => setMode('recovery')}
                  >
                    {t('auth.recoveryTab')}
                  </button>
                </div>
              )}
              <div className="auth-panel__security-actions">
                <span className="auth-secure-pill">
                  <span aria-hidden="true" />
                  {t('auth.secureSession')}
                </span>
                {tlsAccess && (
                  <button type="button" className="auth-trust-button" onClick={() => setTrustOpen(true)}>
                    {t('auth.trustDeviceAction')}
                  </button>
                )}
              </div>
            </div>

            <div className="auth-panel__heading">
              <p className="auth-panel__eyebrow">LightningOS Control Center</p>
              <h2>{title}</h2>
              <p>{subtitle}</p>
            </div>

            {error && (
              <div className="auth-error" role="alert" aria-live="polite">
                <span aria-hidden="true">!</span>
                {error}
              </div>
            )}

            {mode === 'setup' && (
              <form className="auth-form" onSubmit={(event) => { event.preventDefault(); void handleSetup() }}>
                <div className="auth-instruction">
                  <p>{state.setup_token_issued ? t('auth.setupTokenIssued') : t('auth.setupTokenMissing')}</p>
                  <div>
                    <code>{t('auth.setupCommand')}</code>
                    <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={copySetupCommand}>
                      {setupCommandCopied ? t('common.copied') : t('common.copy')}
                    </button>
                  </div>
                </div>
                <label className="auth-field">
                  <span>{t('auth.setupTokenLabel')}</span>
                  <input className="input-field" value={setupToken} onChange={(event) => setSetupToken(event.target.value)} placeholder={t('auth.setupTokenPlaceholder')} autoComplete="one-time-code" />
                </label>
                <label className="auth-field">
                  <span>{t('auth.passwordLabel')}</span>
                  <input className="input-field" type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t('auth.passwordPlaceholder')} autoComplete="new-password" />
                </label>
                <label className="auth-field">
                  <span>{t('auth.confirmPasswordLabel')}</span>
                  <input className="input-field" type="password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} placeholder={t('auth.confirmPasswordPlaceholder')} autoComplete="new-password" />
                </label>
                <p className="auth-form__hint">{t('auth.passwordHint')}</p>
                <button className="auth-submit" type="submit" disabled={busy}>
                  {busy && <span className="auth-spinner" aria-hidden="true" />}
                  {busy ? t('auth.settingUp') : t('auth.setupAction')}
                </button>
              </form>
            )}

            {mode === 'login' && (
              <form className="auth-form auth-form--login" onSubmit={(event) => { event.preventDefault(); void handleLogin() }}>
                <label className="auth-field">
                  <span>{t('auth.passwordLabel')}</span>
                  <input className="input-field" type="password" value={loginPassword} onChange={(event) => setLoginPassword(event.target.value)} placeholder={t('auth.passwordPlaceholder')} autoComplete="current-password" autoFocus />
                </label>
                <button className="auth-submit" type="submit" disabled={busy}>
                  {busy && <span className="auth-spinner" aria-hidden="true" />}
                  {busy ? t('auth.signingIn') : t('auth.loginAction')}
                  {!busy && <span className="auth-submit__arrow" aria-hidden="true">→</span>}
                </button>
              </form>
            )}

            {mode === 'recovery' && (
              <form className="auth-form" onSubmit={(event) => { event.preventDefault(); void handleRecovery() }}>
                <div className="auth-instruction">
                  <p>{state.recovery_token_issued ? t('auth.recoveryTokenIssued') : t('auth.recoveryTokenMissing')}</p>
                  <code>{t('auth.recoveryCommand')}</code>
                </div>
                <label className="auth-field">
                  <span>{t('auth.recoveryTokenLabel')}</span>
                  <input className="input-field" value={recoveryToken} onChange={(event) => setRecoveryToken(event.target.value)} placeholder={t('auth.recoveryTokenPlaceholder')} autoComplete="one-time-code" />
                </label>
                <label className="auth-field">
                  <span>{t('auth.passwordLabel')}</span>
                  <input className="input-field" type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder={t('auth.passwordPlaceholder')} autoComplete="new-password" />
                </label>
                <label className="auth-field">
                  <span>{t('auth.confirmPasswordLabel')}</span>
                  <input className="input-field" type="password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} placeholder={t('auth.confirmPasswordPlaceholder')} autoComplete="new-password" />
                </label>
                <button className="auth-submit" type="submit" disabled={busy}>
                  {busy && <span className="auth-spinner" aria-hidden="true" />}
                  {busy ? t('auth.resettingPassword') : t('auth.recoveryAction')}
                </button>
              </form>
            )}

            <div className="auth-trust-line" aria-hidden="true">
              <span /><span /><span /><span /><span />
            </div>
          </section>
        </main>

        <footer className="auth-footer">
          <span>{appVersion ? `LightningOS ${appVersion}` : 'LightningOS'}</span>
          <a href="https://br-ln.com" target="_blank" rel="noreferrer">
            {t('auth.poweredBy')} <strong>BR<span aria-hidden="true">⚡</span>LN</strong>
            <span aria-hidden="true">↗</span>
          </a>
        </footer>
      </div>

      {trustOpen && tlsAccess && (
        <div className="auth-trust-modal" role="dialog" aria-modal="true" aria-labelledby="auth-trust-title" onMouseDown={(event) => {
          if (event.target === event.currentTarget) setTrustOpen(false)
        }}>
          <div className="auth-trust-dialog">
            <button type="button" className="auth-trust-dialog__close" aria-label={t('common.close')} onClick={() => setTrustOpen(false)}>×</button>
            <p className="auth-panel__eyebrow">{t('auth.trustDeviceKicker')}</p>
            <h2 id="auth-trust-title">{t('auth.trustDeviceTitle')}</h2>
            <p className="auth-trust-dialog__body">{t('auth.trustDeviceBody')}</p>

            {tlsAccess.preferred_url && (
              <div className="auth-trust-address">
                <span>{t('auth.preferredLocalAddress')}</span>
                <a href={tlsAccess.preferred_url}>{tlsAccess.preferred_url}</a>
              </div>
            )}

            <div className="auth-trust-fingerprint">
              <span>{t('auth.caFingerprint')}</span>
              <code>{tlsAccess.ca_fingerprint_sha256 || '--'}</code>
            </div>

            <div className="auth-trust-actions">
              {tlsAccess.windows_installer_url && (
                <a className="btn-primary" href={tlsAccess.windows_installer_url} download>
                  {t('auth.windowsTrustInstaller')}
                </a>
              )}
              {tlsAccess.ca_download_url && (
                <a className="btn-secondary" href={tlsAccess.ca_download_url} download>
                  {t('auth.downloadNodeCA')}
                </a>
              )}
            </div>

            <ol className="auth-trust-steps">
              <li>{t('auth.trustWindowsStep1')}</li>
              <li>{t('auth.trustWindowsStep2')}</li>
              <li>{t('auth.trustWindowsStep3')}</li>
            </ol>
            <p className="auth-trust-dialog__note">{t('auth.trustDeviceNote')}</p>
          </div>
        </div>
      )}
    </div>
  )
}
