import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createWalletSeed,
  initWallet,
  postBitcoinRemote,
  unlockWallet,
  getBitcoin,
  getBitcoinLocalStatus,
  installApp,
  setBitcoinSource
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

  const handlePickLocal = async () => {
    setBtcPath('local')
    setStatus(t('wizard.localPreparing'))
    setStatusTone('warn')
    try {
      const current: any = await getBitcoinLocalStatus().catch(() => null)
      if (!current?.installed) {
        setLocalInstalling(true)
        await installApp('bitcoincore')
      }
      setLocalInstalling(false)
      await setBitcoinSource({ source: 'local', allow_unsynced: true })
      const updated: any = await getBitcoinLocalStatus().catch(() => null)
      if (updated) setLocalStatus(updated)
      setStatus(t('wizard.localCommitted'))
      setStatusTone('success')
    } catch (err: any) {
      setLocalInstalling(false)
      setStatus(err?.message || t('wizard.localFailed'))
      setStatusTone('error')
    }
  }

  const handleContinueLocal = () => {
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
              className="section-card text-left space-y-4 border-glow/30 hover:border-glow/70 transition"
              onClick={() => { setBtcPath('club'); setStatus(''); setStatusTone('neutral') }}
            >
              <div>
                <p className="text-xs uppercase text-brass">{t('wizard.pathClubBadge')}</p>
                <h4 className="text-xl font-semibold mt-1">{t('wizard.pathClubTitle')}</h4>
                <p className="text-sm text-fog/70 mt-2">{t('wizard.pathClubDesc')}</p>
              </div>
              <ul className="space-y-2 text-sm text-fog/70">
                <li>{t('wizard.pathClubBenefitImmediate')}</li>
                <li>{t('wizard.pathClubBenefitParallel')}</li>
                <li>{t('wizard.pathClubBenefitRpc')}</li>
                <li>{t('wizard.pathClubBenefitSupport')}</li>
              </ul>
            </button>
            <button
              type="button"
              className="section-card text-left space-y-4 hover:border-brass/70 transition"
              onClick={handlePickLocal}
            >
              <div>
                <p className="text-xs uppercase text-brass">{t('wizard.pathLocalBadge')}</p>
                <h4 className="text-xl font-semibold mt-1">{t('wizard.pathLocalTitle')}</h4>
                <p className="text-sm text-fog/70 mt-2">{t('wizard.pathLocalDesc')}</p>
              </div>
              <ul className="space-y-2 text-sm text-fog/70">
                <li>{t('wizard.pathLocalBenefitSovereign')}</li>
                <li>{t('wizard.pathLocalBenefitDelayed')}</li>
                <li>{t('wizard.pathLocalBenefitStorage')}</li>
              </ul>
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
            <button className="text-xs text-fog/60 underline underline-offset-4" onClick={() => { setBtcPath(null); setLocalStatus(null) }}>
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
          <p className="text-xs text-fog/60">{t('wizard.localSyncNote')}</p>
          <button
            className="btn-primary"
            onClick={handleContinueLocal}
            disabled={!localStatus?.installed}
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
