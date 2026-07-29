import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createWalletSeed,
  getAppStorageTargets,
  initWallet,
  postBitcoinRemote,
  unlockWallet,
  getBitcoin,
  getBitcoinLocalStatus,
  installApp,
  setBitcoinSource,
  type StorageTarget
} from '../api'

type BitcoinPath = 'club' | 'local' | null

export default function Wizard() {
  const { t } = useTranslation()
  const [step, setStep] = useState(1)
  const [btcPath, setBtcPath] = useState<BitcoinPath>(null)
  const [rpcUser, setRpcUser] = useState('')
  const [rpcPass, setRpcPass] = useState('')
  const [bitcoinHost, setBitcoinHost] = useState('')
  const [zmqBlock, setZmqBlock] = useState('')
  const [zmqTx, setZmqTx] = useState('')
  const [localStatus, setLocalStatus] = useState<any>(null)
  const [localInstalling, setLocalInstalling] = useState(false)
  const [localCommitted, setLocalCommitted] = useState(false)
  const [localUseStorageMount, setLocalUseStorageMount] = useState(false)
  const [localStorageTargets, setLocalStorageTargets] = useState<StorageTarget[]>([])
  const [localSelectedMount, setLocalSelectedMount] = useState('')
  const [localStorageLoading, setLocalStorageLoading] = useState(false)
  const [localStorageError, setLocalStorageError] = useState('')
  const [walletMode, setWalletMode] = useState<'create' | 'import'>('create')
  const [walletPassword, setWalletPassword] = useState('')
  const [walletPasswordConfirm, setWalletPasswordConfirm] = useState('')
  const [seedWords, setSeedWords] = useState<string[]>([])
  const [seedInput, setSeedInput] = useState('')
  const [ackSeed, setAckSeed] = useState(false)
  const [unlockPass, setUnlockPass] = useState('')
  const [status, setStatus] = useState('')
  const [statusTone, setStatusTone] = useState<'neutral' | 'success' | 'warn' | 'error'>('neutral')
  const localPollRef = useRef<number | null>(null)

  useEffect(() => {
    getBitcoin().then((data: any) => {
      setBitcoinHost(data.rpchost)
      setZmqBlock(data.zmq_rawblock)
      setZmqTx(data.zmq_rawtx)
    }).catch(() => null)
  }, [])

  useEffect(() => {
    if (step !== 1 || btcPath !== 'local') {
      if (localPollRef.current !== null) {
        window.clearInterval(localPollRef.current)
        localPollRef.current = null
      }
      return
    }
    let active = true
    const tick = async () => {
      try {
        const data: any = await getBitcoinLocalStatus()
        if (active) setLocalStatus(data)
      } catch {
        /* transient while the container is starting */
      }
    }
    tick()
    localPollRef.current = window.setInterval(tick, 4000)
    return () => {
      active = false
      if (localPollRef.current !== null) {
        window.clearInterval(localPollRef.current)
        localPollRef.current = null
      }
    }
  }, [step, btcPath])

  const next = () => setStep((prev) => Math.min(prev + 1, 4))

  const syncLabel = (info: any) => {
    const progress = info?.verification_progress ?? info?.verificationprogress
    if (typeof progress !== 'number') {
      return t('common.na')
    }
    return `${(progress * 100).toFixed(2)}%`
  }

  const formatBitcoinInfo = (info: any, rpcOk?: boolean) => {
    const ok = rpcOk ?? info?.rpc_ok ?? false
    if (!ok) {
      return t('wizard.rpcValidationFailed')
    }
    if (!info) {
      return t('wizard.rpcOk')
    }
    if (typeof info.blocks === 'number' && info.chain) {
      return t('wizard.rpcOkWithInfo', { chain: info.chain, blocks: info.blocks, sync: syncLabel(info) })
    }
    return t('wizard.rpcOk')
  }

  const loadLocalStorageTargets = async () => {
    setLocalStorageLoading(true)
    setLocalStorageError('')
    try {
      const data: { targets?: StorageTarget[] } = await getAppStorageTargets('bitcoincore')
      const targets = data?.targets || []
      setLocalStorageTargets(targets)
      setLocalSelectedMount((current) => {
        if (current && targets.some((target) => target.eligible && target.mount === current)) return current
        return targets.find((target) => target.eligible)?.mount || ''
      })
    } catch (err) {
      setLocalStorageTargets([])
      setLocalSelectedMount('')
      setLocalStorageError(err instanceof Error ? err.message : t('appStore.storageLoadFailed'))
    } finally {
      setLocalStorageLoading(false)
    }
  }

  useEffect(() => {
    if (step === 1 && btcPath === 'local' && localUseStorageMount) {
      void loadLocalStorageTargets()
    }
  }, [step, btcPath, localUseStorageMount])

  const handleBitcoin = async () => {
    setStatus(t('wizard.testingConnection'))
    setStatusTone('warn')
    try {
      const res: any = await postBitcoinRemote({ rpcuser: rpcUser, rpcpass: rpcPass })
      setStatus(formatBitcoinInfo(res?.info, true))
      setStatusTone('success')
      setRpcPass('')
      getBitcoin()
        .then((updated: any) => {
          setBitcoinHost(updated.rpchost)
          setZmqBlock(updated.zmq_rawblock)
          setZmqTx(updated.zmq_rawtx)
        })
        .catch(() => null)
      next()
    } catch (err: any) {
      setStatus(err?.message || t('wizard.rpcValidationFailedRetry'))
      setStatusTone('error')
    }
  }

  const handlePickLocal = () => {
    setBtcPath('local')
    setLocalCommitted(false)
    setStatus('')
    setStatusTone('neutral')
  }

  const selectLocalBitcoinSource = async () => {
    await setBitcoinSource({ source: 'local', allow_unsynced: true })
    setLocalCommitted(true)
    setStatus(t('wizard.localCommitted'))
    setStatusTone('success')
  }

  const handleInstallLocal = async () => {
    setStatus(t('wizard.localPreparing'))
    setStatusTone('warn')
    setLocalInstalling(true)
    try {
      const payload = localUseStorageMount ? { storage_mount: localSelectedMount } : {}
      await installApp('bitcoincore', payload)
      await selectLocalBitcoinSource()
      const updated: any = await getBitcoinLocalStatus().catch(() => null)
      if (updated) setLocalStatus(updated)
    } catch (err: any) {
      setStatus(err?.message || t('wizard.localFailed'))
      setStatusTone('error')
    } finally {
      setLocalInstalling(false)
    }
  }

  const handleUseInstalledLocal = async () => {
    setStatus(t('wizard.localPreparing'))
    setStatusTone('warn')
    try {
      await selectLocalBitcoinSource()
    } catch (err: any) {
      setStatus(err?.message || t('wizard.localFailed'))
      setStatusTone('error')
    }
  }

  const handleContinueLocal = () => {
    if (!localCommitted) return
    setStatus('')
    setStatusTone('neutral')
    next()
  }

  const handleGenerateSeed = async () => {
    setStatus(t('wizard.generatingSeed'))
    setStatusTone('warn')
    try {
      const res = await createWalletSeed()
      setSeedWords(res.seed_words || [])
      setStatus(t('wizard.seedGenerated'))
      setStatusTone('success')
    } catch (err: any) {
      setStatus(err?.message || t('wizard.seedGenerationFailed'))
      setStatusTone('error')
    }
  }

  const handleInitWallet = async () => {
    if (!walletPassword || walletPassword !== walletPasswordConfirm) {
      setStatus(t('wizard.passwordMismatch'))
      setStatusTone('error')
      return
    }

    const words = walletMode === 'create' ? seedWords : seedInput.trim().split(/\s+/)
    if (words.length < 12) {
      setStatus(t('wizard.seedInvalid'))
      setStatusTone('error')
      return
    }
    if (walletMode === 'create' && !ackSeed) {
      setStatus(t('wizard.confirmSeed'))
      setStatusTone('error')
      return
    }

    setStatus(t('wizard.initializingWallet'))
    setStatusTone('warn')
    try {
      await initWallet({ wallet_password: walletPassword, seed_words: words })
      setStatus(t('wizard.walletInitialized'))
      setStatusTone('success')
      setStep(4)
    } catch (err: any) {
      const message = err?.message || t('wizard.walletInitFailed')
      if (message.toLowerCase().includes('wallet already exists')) {
        setStatus(t('wizard.walletExists'))
        setStatusTone('warn')
        setStep(3)
        return
      }
      setStatus(message)
      setStatusTone('error')
    }
  }

  const handleUnlock = async () => {
    if (!unlockPass) {
      setStatus(t('wizard.enterWalletPassword'))
      setStatusTone('error')
      return
    }
    setStatus(t('wizard.unlocking'))
    setStatusTone('warn')
    try {
      await unlockWallet({ wallet_password: unlockPass })
      setStatus(t('wizard.unlocked'))
      setStatusTone('success')
      next()
    } catch (err: any) {
      setStatus(err?.message || t('wizard.unlockFailed'))
      setStatusTone('error')
    }
  }

  const eligibleLocalStorageTargets = localStorageTargets.filter((target) => target.eligible)
  const selectedLocalStorageTarget = eligibleLocalStorageTargets.find((target) => target.mount === localSelectedMount)
  const formatStorageGB = (value?: number) => {
    if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return '-'
    return value >= 100 ? value.toFixed(0) : value.toFixed(1)
  }
  const storageTargetLabel = (target: StorageTarget) => t('appStore.storageTargetOption', {
    mount: target.mount,
    free: formatStorageGB(target.free_gb),
    fstype: target.fstype || target.source || 'fs'
  })

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('wizard.title')}</h2>
        <p className="text-fog/60 mt-2">{t('wizard.subtitle')}</p>
        {status && (
          <p className={`mt-4 text-sm ${
            statusTone === 'success'
              ? 'text-glow'
              : statusTone === 'error'
                ? 'text-ember'
                : statusTone === 'warn'
                  ? 'text-brass'
                  : 'text-fog/60'
          }`}>{status}</p>
        )}
      </div>

      {step === 1 && btcPath === null && (
        <div className="space-y-4">
          <div>
            <h3 className="text-lg font-semibold">{t('wizard.step1ChooseTitle')}</h3>
            <p className="text-sm text-fog/70 mt-1">{t('wizard.step1ChooseSubtitle')}</p>
          </div>
          <div className="grid gap-4 lg:grid-cols-2">
            <button
              type="button"
              className="section-card group relative overflow-hidden text-left border-glow/40 bg-gradient-to-br from-glow/15 via-white/5 to-brass/10 transition hover:-translate-y-0.5 hover:border-glow/80 hover:shadow-[0_18px_40px_rgba(20,184,166,0.14)] focus:outline-none focus:ring-2 focus:ring-glow/50"
              onClick={() => { setBtcPath('club'); setStatus(''); setStatusTone('neutral') }}
            >
              <div className="pointer-events-none absolute right-0 top-0 h-28 w-28 rounded-bl-full bg-glow/10 blur-sm transition group-hover:bg-glow/15" />
              <div className="relative space-y-4">
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="rounded-full border border-glow/40 bg-glow/15 px-3 py-1 text-[11px] font-semibold uppercase tracking-[0.16em] text-glow">
                      {t('wizard.pathClubBadge')}
                    </span>
                    <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-[11px] uppercase tracking-[0.14em] text-fog/65">
                      {t('wizard.pathClubSpeed')}
                    </span>
                  </div>
                  <h4 className="mt-4 text-2xl font-semibold leading-tight">{t('wizard.pathClubTitle')}</h4>
                  <p className="mt-2 text-base font-semibold text-brass">{t('wizard.pathClubStart')}</p>
                  <p className="mt-2 text-sm text-fog/70">{t('wizard.pathClubDesc')}</p>
                </div>
                <ul className="space-y-2 text-sm text-fog/70">
                  <li>{t('wizard.pathClubBenefitImmediate')}</li>
                  <li>{t('wizard.pathClubBenefitParallel')}</li>
                  <li>{t('wizard.pathClubBenefitRpc')}</li>
                  <li>{t('wizard.pathClubBenefitSupport')}</li>
                </ul>
                <div className="inline-flex rounded-full border border-glow/30 bg-glow/10 px-4 py-2 text-sm font-semibold text-glow transition group-hover:border-glow/50 group-hover:bg-glow/15">
                  {t('wizard.pathClubAction')}
                </div>
              </div>
            </button>
            <button
              type="button"
              className="section-card group text-left border-white/10 bg-white/[0.03] transition hover:-translate-y-0.5 hover:border-brass/70 hover:bg-white/[0.06] focus:outline-none focus:ring-2 focus:ring-brass/40"
              onClick={handlePickLocal}
            >
              <div className="space-y-4">
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="rounded-full border border-white/10 bg-white/5 px-3 py-1 text-[11px] font-semibold uppercase tracking-[0.16em] text-fog/65">
                      {t('wizard.pathLocalBadge')}
                    </span>
                    <span className="rounded-full border border-brass/30 bg-brass/10 px-3 py-1 text-[11px] uppercase tracking-[0.14em] text-brass">
                      {t('wizard.pathLocalSpeed')}
                    </span>
                  </div>
                  <h4 className="mt-4 text-2xl font-semibold leading-tight">{t('wizard.pathLocalTitle')}</h4>
                  <p className="mt-2 text-base font-semibold text-fog/75">{t('wizard.pathLocalStart')}</p>
                  <p className="mt-2 text-sm text-fog/70">{t('wizard.pathLocalDesc')}</p>
                </div>
                <ul className="space-y-2 text-sm text-fog/70">
                  <li>{t('wizard.pathLocalBenefitSovereign')}</li>
                  <li>{t('wizard.pathLocalBenefitDelayed')}</li>
                  <li>{t('wizard.pathLocalBenefitStorage')}</li>
                </ul>
                <div className="inline-flex rounded-full border border-white/10 bg-white/5 px-4 py-2 text-sm font-semibold text-fog/70 transition group-hover:border-brass/40 group-hover:text-brass">
                  {t('wizard.pathLocalAction')}
                </div>
              </div>
            </button>
          </div>
        </div>
      )}

      {step === 1 && btcPath === 'club' && (
        <div className="section-card space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">{t('wizard.step1Title')}</h3>
            <button className="text-xs text-fog/60 underline underline-offset-4" onClick={() => setBtcPath(null)}>
              {t('wizard.back')}
            </button>
          </div>
          <div className="grid gap-4 lg:grid-cols-3 text-sm text-fog/60">
            <div>
              <p className="uppercase text-xs">{t('wizard.rpcHost')}</p>
              <p>{bitcoinHost || 'bitcoin.br-ln.com:8085'}</p>
            </div>
            <div>
              <p className="uppercase text-xs">{t('wizard.zmqRawBlock')}</p>
              <p>{zmqBlock || 'tcp://bitcoin.br-ln.com:28332'}</p>
            </div>
            <div>
              <p className="uppercase text-xs">{t('wizard.zmqRawTx')}</p>
              <p>{zmqTx || 'tcp://bitcoin.br-ln.com:28333'}</p>
            </div>
          </div>
          <p className="text-sm text-fog/70">
            {t('wizard.credentialsHelp')}{' '}
            <a
              className="text-glow underline underline-offset-4"
              href="https://services.br-ln.com"
              target="_blank"
              rel="noreferrer"
            >
              {t('wizard.credentialsHelpLink')}
            </a>
          </p>
          <div className="grid gap-4 lg:grid-cols-2">
            <input className="input-field" placeholder={t('wizard.rpcUser')} value={rpcUser} onChange={(e) => setRpcUser(e.target.value)} />
            <input className="input-field" placeholder={t('wizard.rpcPassword')} type="password" value={rpcPass} onChange={(e) => setRpcPass(e.target.value)} />
          </div>
          <button className="btn-primary" onClick={handleBitcoin}>{t('wizard.testAndSave')}</button>
        </div>
      )}

      {step === 1 && btcPath === 'local' && (
        <div className="section-card space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">{t('wizard.step1LocalTitle')}</h3>
            <button className="text-xs text-fog/60 underline underline-offset-4" onClick={() => { setBtcPath(null); setLocalStatus(null); setLocalCommitted(false) }}>
              {t('wizard.back')}
            </button>
          </div>
          <p className="text-sm text-fog/70">{t('wizard.step1LocalHelp')}</p>
          {localInstalling && <p className="text-sm text-brass">{t('wizard.localInstalling')}</p>}
          {localStatus && (
            <div className="grid gap-3 lg:grid-cols-3 text-sm">
              <div>
                <p className="uppercase text-xs text-fog/50">{t('wizard.localContainer')}</p>
                <p>{localStatus.status === 'running' ? t('wizard.localRunning') : localStatus.installed ? t('wizard.localInstalled') : t('wizard.localAbsent')}</p>
              </div>
              <div>
                <p className="uppercase text-xs text-fog/50">{t('wizard.localBlocks')}</p>
                <p>{typeof localStatus.blocks === 'number' ? `${localStatus.blocks}` : '-'}{typeof localStatus.headers === 'number' ? ` / ${localStatus.headers}` : ''}</p>
              </div>
              <div>
                <p className="uppercase text-xs text-fog/50">{t('wizard.localSync')}</p>
                <p>{typeof localStatus.verification_progress === 'number' ? `${(localStatus.verification_progress * 100).toFixed(2)}%` : '-'}</p>
              </div>
            </div>
          )}
          {!localStatus?.installed && (
            <div className="space-y-4 rounded-lg border border-white/10 bg-black/20 p-4">
              <label className="flex items-start gap-3 text-sm text-fog/80">
                <input
                  className="mt-1"
                  type="checkbox"
                  checked={localUseStorageMount}
                  onChange={(event) => {
                    setLocalUseStorageMount(event.target.checked)
                    if (!event.target.checked) setLocalSelectedMount('')
                  }}
                />
                <span>{t('appStore.bitcoinCoreUseStorageMount')}</span>
              </label>

              {localUseStorageMount ? (
                <div className="space-y-2">
                  <div className="flex items-center justify-between gap-3">
                    <label className="text-xs uppercase tracking-wide text-fog/50" htmlFor="wizard-bitcoin-storage-mount">
                      {t('appStore.storageVolumeLabel')}
                    </label>
                    <button className="text-xs text-fog/60 hover:text-fog" type="button" onClick={loadLocalStorageTargets}>
                      {t('common.refresh')}
                    </button>
                  </div>
                  {localStorageLoading ? (
                    <p className="text-sm text-fog/60">{t('appStore.storageLoadingVolumes')}</p>
                  ) : (
                    <select
                      id="wizard-bitcoin-storage-mount"
                      className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm text-fog outline-none focus:border-brass disabled:opacity-60"
                      value={localSelectedMount}
                      disabled={eligibleLocalStorageTargets.length === 0}
                      onChange={(event) => setLocalSelectedMount(event.target.value)}
                    >
                      {eligibleLocalStorageTargets.map((target) => (
                        <option key={target.mount} value={target.mount}>
                          {storageTargetLabel(target)}
                        </option>
                      ))}
                    </select>
                  )}
                  {localStorageError && <p className="text-xs text-rose-300">{localStorageError}</p>}
                  {!localStorageLoading && !localStorageError && eligibleLocalStorageTargets.length === 0 && (
                    <p className="text-xs text-amber-300/80">{t('appStore.storageNoEligibleVolumes')}</p>
                  )}
                  {selectedLocalStorageTarget && (
                    <p className="break-all rounded-md border border-white/10 bg-black/20 px-3 py-2 font-mono text-xs text-fog/70">
                      {t('appStore.storageSelectedPath', { path: selectedLocalStorageTarget.suggested_path })}
                    </p>
                  )}
                </div>
              ) : (
                <p className="break-all rounded-md border border-white/10 bg-black/20 px-3 py-2 font-mono text-xs text-fog/70">
                  {t('appStore.storageDefaultPath', { path: '/data/bitcoin' })}
                </p>
              )}

              <button
                className="btn-primary"
                type="button"
                disabled={localInstalling || (localUseStorageMount && !localSelectedMount)}
                onClick={handleInstallLocal}
              >
                {t('wizard.installLocalNow')}
              </button>
            </div>
          )}
          {localStatus?.installed && !localCommitted && (
            <button className="btn-primary" type="button" onClick={handleUseInstalledLocal}>
              {t('wizard.useInstalledLocal')}
            </button>
          )}
          {localStatus?.installed && <p className="text-xs text-fog/60">{t('wizard.localSyncNote')}</p>}
          <button
            className="btn-primary"
            onClick={handleContinueLocal}
            disabled={!localStatus?.installed || !localCommitted}
          >
            {t('wizard.continueToWallet')}
          </button>
        </div>
      )}

      {step === 2 && (
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('wizard.step2Title')}</h3>
          <div className="flex gap-3">
            <button className={walletMode === 'create' ? 'btn-primary' : 'btn-secondary'} onClick={() => setWalletMode('create')}>{t('wizard.createNew')}</button>
            <button className={walletMode === 'import' ? 'btn-primary' : 'btn-secondary'} onClick={() => setWalletMode('import')}>{t('wizard.importExisting')}</button>
          </div>
          <div className="grid gap-4 lg:grid-cols-2">
            <input className="input-field" placeholder={t('wizard.walletPassword')} type="password" value={walletPassword} onChange={(e) => setWalletPassword(e.target.value)} />
            <input className="input-field" placeholder={t('wizard.confirmPassword')} type="password" value={walletPasswordConfirm} onChange={(e) => setWalletPasswordConfirm(e.target.value)} />
          </div>
          <p className="text-xs text-fog/60">{t('wizard.passwordHint')}</p>

          {walletMode === 'create' ? (
            <div className="space-y-3">
              <button className="btn-secondary" onClick={handleGenerateSeed}>{t('wizard.generateSeed')}</button>
              {seedWords.length > 0 && (
                <div className="bg-ink/50 rounded-2xl p-4 text-sm">
                  <p className="text-brass text-xs uppercase">{t('wizard.seedWordsTitle')}</p>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {seedWords.map((word: string, idx: number) => (
                      <span key={word + idx} className="px-2 py-1 rounded-xl bg-white/5 border border-white/10">{word}</span>
                    ))}
                  </div>
                  <label className="mt-4 flex items-center gap-2 text-xs text-fog/70">
                    <input type="checkbox" checked={ackSeed} onChange={(e) => setAckSeed(e.target.checked)} />
                    {t('wizard.ackSeed')}
                  </label>
                </div>
              )}
              <button className="btn-primary" onClick={handleInitWallet}>{t('wizard.initializeWallet')}</button>
            </div>
          ) : (
            <div className="space-y-3">
              <textarea className="input-field min-h-[120px]" placeholder={t('wizard.pasteSeedWords')} value={seedInput} onChange={(e) => setSeedInput(e.target.value)} />
              <button className="btn-primary" onClick={handleInitWallet}>{t('wizard.importAndInitialize')}</button>
            </div>
          )}
        </div>
      )}

      {step === 3 && (
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('wizard.step3Title')}</h3>
          <input className="input-field" placeholder={t('wizard.walletPassword')} type="password" value={unlockPass} onChange={(e) => setUnlockPass(e.target.value)} />
          <button className="btn-primary" onClick={handleUnlock}>{t('wizard.unlock')}</button>
        </div>
      )}

      {step === 4 && (
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('wizard.step4Title')}</h3>
          <p className="text-fog/60">{t('wizard.finishMessage')}</p>
          <a className="btn-primary inline-flex" href="#dashboard">{t('wizard.goToDashboard')}</a>
        </div>
      )}
    </section>
  )
}
