import { useEffect, useState, useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { getBoletoConfig, activateBoleto, getActivationStatus, createBoletoQuote, getBoletoStatus, payInvoice } from '../api'
import { getLocale } from '../i18n'
import QRCode from 'qrcode'
import { copyText } from '../utils/clipboard'

type BoletoConfig = {
  enabled: boolean
  activated: boolean
  feePercent: number
  provider: string
}

type ActivationData = {
  invoice: string
  paymentHash: string
  sats: number
  expiresAt: string
  message?: string
}

type BoletoQuote = {
  invoice: string
  paymentHash: string
  amountBrl: number
  baseSats: number
  feeSats: number
  feePercent: number
  totalSats: number
  btcRate: number
  bankName: string
  bankCode: string
  dueDate?: string | null
  expiresAt: string
}

type BoletoStatus = {
  status: string
  invoicePaid: boolean
  boletoPaid: boolean
  protocolo?: string
  amountBrl?: number
  totalSats?: number
  reason?: string
}

type Step = 'input' | 'confirm' | 'paying' | 'done' | 'error'

const ACTIVATION_STORAGE_KEY = 'pay_boleto_activation_pending'

const fmt = (n: number, locale: string) =>
  new Intl.NumberFormat(locale, { style: 'currency', currency: 'BRL' }).format(n)

const fmtSats = (n: number, locale: string) =>
  new Intl.NumberFormat(locale).format(n) + ' sats'

// ─── Activation persistence (saves full object for QR on page return) ─
const savePendingActivation = (activation: ActivationData | null) => {
  try {
    if (activation) {
      window.localStorage.setItem(ACTIVATION_STORAGE_KEY, JSON.stringify(activation))
    } else {
      window.localStorage.removeItem(ACTIVATION_STORAGE_KEY)
    }
  } catch {
    // ignore storage failures
  }
}

const loadPendingActivation = (): ActivationData | null => {
  try {
    const raw = window.localStorage.getItem(ACTIVATION_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as ActivationData
    if (
      !parsed ||
      typeof parsed.invoice !== 'string' ||
      typeof parsed.paymentHash !== 'string' ||
      typeof parsed.sats !== 'number' ||
      typeof parsed.expiresAt !== 'string'
    ) {
      return null
    }
    return parsed
  } catch {
    return null
  }
}

// ─── Countdown hook ───────────────────────────────────────────────────
function useCountdown(expiresAt: string | undefined | null) {
  const [remaining, setRemaining] = useState('')
  const [expired, setExpired] = useState(false)

  useEffect(() => {
    if (!expiresAt) { setRemaining(''); setExpired(false); return }
    const tick = () => {
      const diff = new Date(expiresAt).getTime() - Date.now()
      if (diff <= 0) { setRemaining('0:00'); setExpired(true); return }
      const m = Math.floor(diff / 60000)
      const s = Math.floor((diff % 60000) / 1000)
      setRemaining(`${m}:${s.toString().padStart(2, '0')}`)
      setExpired(false)
    }
    tick()
    const id = setInterval(tick, 1000)
    return () => clearInterval(id)
  }, [expiresAt])

  return { remaining, expired }
}

// ─── Or-divider component ─────────────────────────────────────────────
function OrDivider({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-3 my-1">
      <div className="flex-1 h-px bg-white/10" />
      <span className="text-fog/40 text-xs uppercase tracking-wider">{label}</span>
      <div className="flex-1 h-px bg-white/10" />
    </div>
  )
}

// ─── Status badge component ──────────────────────────────────────────
function StatusBadge({ status, label }: { status: string; label: string }) {
  const colors: Record<string, string> = {
    completed: 'bg-emerald-500/20 text-emerald-400',
    failed: 'bg-red-500/20 text-red-400',
    expired: 'bg-red-500/20 text-red-400',
    pending: 'bg-amber-500/20 text-amber-400',
    processing: 'bg-blue-500/20 text-blue-400',
  }
  return (
    <span className={`inline-block px-2 py-0.5 rounded-full text-xs font-medium ${colors[status] || colors.pending}`}>
      {label}
    </span>
  )
}

export default function PayBoleto() {
  const { t } = useTranslation()
  const locale = getLocale()

  const [config, setConfig] = useState<BoletoConfig | null>(null)
  const [loading, setLoading] = useState(true)
  const [step, setStep] = useState<Step>('input')
  const [barcode, setBarcode] = useState('')
  const [error, setError] = useState('')
  const [quoting, setQuoting] = useState(false)
  const [quote, setQuote] = useState<BoletoQuote | null>(null)
  const [status, setStatus] = useState<BoletoStatus | null>(null)
  const [copied, setCopied] = useState(false)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const [history, setHistory] = useState<Array<{ paymentHash: string; amountBrl: number; totalSats: number; status: string; bankName: string; createdAt: string }>>([])

  // Activation state
  const [activating, setActivating] = useState(false)
  const [activation, setActivation] = useState<ActivationData | null>(null)
  const [activationCopied, setActivationCopied] = useState(false)
  const activationPollRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const [activationQr, setActivationQr] = useState<string | null>(null)
  const [payingWithNode, setPayingWithNode] = useState(false)
  const [paidWithNode, setPaidWithNode] = useState(false)

  // QR code for boleto payment invoice
  const [payingQr, setPayingQr] = useState<string | null>(null)

  // Expiration countdowns
  const activationCountdown = useCountdown(activation?.expiresAt)
  const quoteCountdown = useCountdown(step === 'paying' ? quote?.expiresAt : null)

  const clearActivationPoll = useCallback(() => {
    if (activationPollRef.current) {
      clearInterval(activationPollRef.current)
      activationPollRef.current = null
    }
  }, [])

  const startActivationPolling = useCallback((nextActivation: ActivationData) => {
    clearActivationPoll()
    const poll = async () => {
      try {
        const s: any = await getActivationStatus(nextActivation.paymentHash)
        if (s.status === 'completed') {
          clearActivationPoll()
          savePendingActivation(null)
          setActivation(null)
          setActivating(false)
          setActivationQr(null)
          const cfg = await getBoletoConfig()
          setConfig(cfg)
        } else if (s.status === 'expired') {
          clearActivationPoll()
          savePendingActivation(null)
          setActivation(null)
          setActivating(false)
          setActivationQr(null)
          setError(t('boleto.activationExpired'))
        }
      } catch {
        // continue polling
      }
    }
    poll()
    activationPollRef.current = setInterval(poll, 4000)
  }, [clearActivationPoll, t])

  // Load config on mount
  useEffect(() => {
    let canceled = false
    const run = async () => {
      try {
        const cfg = await getBoletoConfig()
        if (canceled) return
        setConfig(cfg)
        if (cfg.activated) {
          savePendingActivation(null)
          return
        }

        const pending = loadPendingActivation()
        if (!pending) return

        const expiresAt = Date.parse(pending.expiresAt)
        if (!Number.isNaN(expiresAt) && Date.now() > expiresAt) {
          savePendingActivation(null)
          return
        }

        setActivation(pending)
      } catch {
        if (!canceled) setConfig({ enabled: false, activated: false, feePercent: 6, provider: '' })
      } finally {
        if (!canceled) setLoading(false)
      }
    }
    run()
    return () => {
      canceled = true
    }
  }, [])

  // Resume polling if activation was restored after remount
  useEffect(() => {
    if (!activation?.paymentHash || config?.activated) return
    startActivationPolling(activation)
  }, [activation?.paymentHash, config?.activated, startActivationPolling])

  // Cleanup poll on unmount
  useEffect(() => {
    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
      clearActivationPoll()
    }
  }, [clearActivationPoll])

  // Generate QR code locally instead of using external service
  useEffect(() => {
    if (!activation?.invoice) {
      setActivationQr(null)
      return
    }
    QRCode.toDataURL(activation.invoice, { width: 220, margin: 1 })
      .then(setActivationQr)
      .catch(() => setActivationQr(null))
  }, [activation?.invoice])

  // Generate QR code for boleto payment invoice
  useEffect(() => {
    if (step !== 'paying' || !quote?.invoice) {
      setPayingQr(null)
      return
    }
    QRCode.toDataURL(quote.invoice, { width: 220, margin: 1 })
      .then(setPayingQr)
      .catch(() => setPayingQr(null))
  }, [step, quote?.invoice])

  // Auto-expire activation if countdown reaches zero
  useEffect(() => {
    if (activationCountdown.expired && activation) {
      clearActivationPoll()
      savePendingActivation(null)
      setActivation(null)
      setActivating(false)
      setActivationQr(null)
      setError(t('boleto.activationExpired'))
    }
  }, [activationCountdown.expired, activation, t, clearActivationPoll])

  // Auto-expire boleto quote if countdown reaches zero
  useEffect(() => {
    if (quoteCountdown.expired && step === 'paying') {
      if (pollRef.current) { clearInterval(pollRef.current); pollRef.current = null }
      setStep('error')
      setError(t('boleto.quoteExpired'))
    }
  }, [quoteCountdown.expired, step, t])

  // ─── Activation flow ────────────────────────────────────────────────

  const handleActivate = useCallback(async () => {
    setActivating(true)
    setError('')
    try {
      const data = await activateBoleto()
      setActivation(data)
      savePendingActivation(data)
      startActivationPolling(data)
    } catch (err: any) {
      setError(err.message || t('boleto.activationError'))
      setActivating(false)
    }
  }, [startActivationPolling, t])

  const handlePayWithNode = useCallback(async (invoice: string) => {
    setPayingWithNode(true)
    setError('')
    try {
      await payInvoice({ payment_request: invoice })
      setPaidWithNode(true)
      // Payment sent — polling will detect completion
    } catch (err: any) {
      setError(err.message || t('boleto.payWithNodeError'))
    } finally {
      setPayingWithNode(false)
    }
  }, [t])

  const copyActivationInvoice = useCallback(() => {
    if (!activation) return
    copyText(activation.invoice).then(() => {
      setActivationCopied(true)
      setTimeout(() => setActivationCopied(false), 2000)
    })
  }, [activation])

  const handleQuote = useCallback(async () => {
    setError('')
    const clean = barcode.replace(/[\s.-]/g, '')
    if (clean.length < 44) {
      setError(t('boleto.errorMinDigits'))
      return
    }
    setQuoting(true)
    try {
      const q = await createBoletoQuote(clean)
      setQuote(q)
      setStep('confirm')
    } catch (err: any) {
      setError(err.message || t('boleto.errorQuote'))
    } finally {
      setQuoting(false)
    }
  }, [barcode, t])

  const handleConfirm = useCallback(() => {
    if (!quote) return
    setStep('paying')
    setCopied(false)
    setError('')
    setPaidWithNode(false)

    // Save to history
    setHistory(prev => [{
      paymentHash: quote.paymentHash,
      amountBrl: quote.amountBrl,
      totalSats: quote.totalSats,
      status: 'pending',
      bankName: quote.bankName,
      createdAt: new Date().toISOString(),
    }, ...prev].slice(0, 20))

    // Start polling
    const poll = async () => {
      try {
        const s = await getBoletoStatus(quote.paymentHash)
        setStatus(s)
        if (s.status === 'completed') {
          setStep('done')
          setHistory(prev => prev.map(h =>
            h.paymentHash === quote.paymentHash ? { ...h, status: 'completed' } : h
          ))
          if (pollRef.current) clearInterval(pollRef.current)
        } else if (s.status === 'failed' || s.status === 'expired') {
          setStep('error')
          setError(s.reason || s.status)
          setHistory(prev => prev.map(h =>
            h.paymentHash === quote.paymentHash ? { ...h, status: s.status } : h
          ))
          if (pollRef.current) clearInterval(pollRef.current)
        }
      } catch {
        // continue polling
      }
    }

    poll()
    pollRef.current = setInterval(poll, 4000)
  }, [quote])

  const handleReset = useCallback(() => {
    setStep('input')
    setBarcode('')
    setQuote(null)
    setStatus(null)
    setError('')
    setCopied(false)
    setPaidWithNode(false)
    setPayingQr(null)
    if (pollRef.current) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [])

  const copyInvoice = useCallback(() => {
    if (!quote) return
    copyText(quote.invoice).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }, [quote])

  if (loading) {
    return (
      <section className="space-y-6">
        <div className="section-card animate-pulse">
          <div className="h-6 bg-white/10 rounded w-48 mb-4" />
          <div className="h-4 bg-white/10 rounded w-72" />
        </div>
      </section>
    )
  }

  if (!config?.enabled) {
    return (
      <section className="space-y-6">
        <div className="section-card">
          <h2 className="text-xl font-bold text-fog mb-2">{t('boleto.title')}</h2>
          <p className="text-fog/60">{t('boleto.disabled')}</p>
        </div>
      </section>
    )
  }

  // ─── Activation screen ───────────────────────────────────────────────

  if (!config.activated) {
    return (
      <section className="space-y-6">
        <div className="section-card">
          <h2 className="text-xl font-bold text-fog mb-1">{t('boleto.title')}</h2>
          <p className="text-fog/60 text-sm">{t('boleto.subtitle')}</p>
        </div>

        <div className="section-card space-y-4">
          {!activation ? (
            <>
              <h3 className="text-lg font-semibold text-fog">{t('boleto.activationTitle')}</h3>
              <p className="text-fog/70 text-sm">{t('boleto.activationDesc', { sats: '1,000' })}</p>
              <ul className="text-fog/60 text-sm space-y-1 list-disc list-inside">
                <li>{t('boleto.activationStep1')}</li>
                <li>{t('boleto.activationStep2')}</li>
                <li>{t('boleto.activationStep3')}</li>
              </ul>
              {error && <p className="text-red-400 text-sm">{error}</p>}
              <button
                className="btn-primary"
                onClick={handleActivate}
                disabled={activating}
              >
                {activating ? t('boleto.activating') : t('boleto.activateBtn')}
              </button>
            </>
          ) : (
            <>
              <h3 className="text-lg font-semibold text-fog">{t('boleto.activationPayTitle')}</h3>
              <p className="text-fog/70 text-sm">{t('boleto.activationPayDesc', { sats: new Intl.NumberFormat(locale).format(activation.sats) })}</p>

              {/* Pay with Node — primary action */}
              <button
                className="btn-primary w-full flex items-center justify-center gap-2"
                onClick={() => handlePayWithNode(activation.invoice)}
                disabled={payingWithNode || paidWithNode}
              >
                {paidWithNode ? (
                  <>
                    <svg className="w-4 h-4 text-ink" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={3}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
                    {t('boleto.paymentSent')}
                  </>
                ) : (
                  <>
                    <span>⚡</span>
                    {payingWithNode ? t('boleto.payingWithNode') : t('boleto.payWithNode')}
                  </>
                )}
              </button>

              <OrDivider label={t('boleto.orExternal')} />

              {/* QR code */}
              <div className="bg-white rounded-xl p-4 mx-auto w-fit">
                {activationQr ? (
                  <img src={activationQr} alt="QR Code" className="w-[220px] h-[220px]" />
                ) : (
                  <div className="w-[220px] h-[220px] flex items-center justify-center text-gray-400 text-sm">Generating QR...</div>
                )}
              </div>

              {/* Invoice copy */}
              <div>
                <label className="block text-sm text-fog/70 mb-1">{t('boleto.invoiceLabel')}</label>
                <div className="flex gap-2">
                  <input
                    type="text"
                    className="input-field font-mono text-xs"
                    value={activation.invoice}
                    readOnly
                  />
                  <button className="btn-secondary whitespace-nowrap" onClick={copyActivationInvoice}>
                    {activationCopied ? t('boleto.copied') : t('boleto.copyInvoice')}
                  </button>
                </div>
              </div>

              {error && <p className="text-red-400 text-sm">{error}</p>}

              {/* Status + countdown */}
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2 text-fog/60 text-sm">
                  <div className="w-3 h-3 rounded-full bg-amber-400 animate-pulse" />
                  {t('boleto.activationWaiting')}
                </div>
                {activationCountdown.remaining && (
                  <span className={`text-xs font-mono ${activationCountdown.expired ? 'text-red-400' : 'text-fog/40'}`}>
                    ⏱ {activationCountdown.remaining}
                  </span>
                )}
              </div>
            </>
          )}
        </div>
      </section>
    )
  }

  return (
    <section className="space-y-6">
      {/* Header */}
      <div className="section-card">
        <h2 className="text-xl font-bold text-fog mb-1">{t('boleto.title')}</h2>
        <p className="text-fog/60 text-sm">{t('boleto.subtitle')}</p>
      </div>

      {/* Step: Input barcode */}
      {step === 'input' && (
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold text-fog">{t('boleto.inputTitle')}</h3>
          <div>
            <label className="block text-sm text-fog/70 mb-1">{t('boleto.barcodeLabel')}</label>
            <textarea
              className="input-field font-mono text-sm resize-none"
              rows={3}
              placeholder={t('boleto.barcodePlaceholder')}
              value={barcode}
              onChange={e => setBarcode(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleQuote() } }}
              disabled={quoting}
            />
          </div>
          {error && <p className="text-ember text-sm">{error}</p>}
          <button
            className="btn-primary w-full"
            onClick={handleQuote}
            disabled={quoting || barcode.replace(/[\s.-]/g, '').length < 44}
          >
            {quoting ? t('boleto.quoting') : t('boleto.getQuote')}
          </button>

          <div className="border-t border-white/10 pt-3 mt-2">
            <p className="text-xs text-fog/50">
              {t('boleto.feeInfo', { percent: config.feePercent })}
            </p>
          </div>
        </div>
      )}

      {/* Step: Confirm quote */}
      {step === 'confirm' && quote && (
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold text-fog">{t('boleto.confirmTitle')}</h3>

          <div className="grid grid-cols-2 gap-3 text-sm">
            <div className="bg-ink/40 rounded-2xl p-3">
              <span className="text-fog/50 block">{t('boleto.bank')}</span>
              <span className="text-fog font-medium">{quote.bankName}</span>
            </div>
            <div className="bg-ink/40 rounded-2xl p-3">
              <span className="text-fog/50 block">{t('boleto.valueBrl')}</span>
              <span className="text-fog font-medium">{fmt(quote.amountBrl, locale)}</span>
            </div>
            <div className="bg-ink/40 rounded-2xl p-3">
              <span className="text-fog/50 block">{t('boleto.baseSats')}</span>
              <span className="text-fog font-medium">{fmtSats(quote.baseSats, locale)}</span>
            </div>
            <div className="bg-ink/40 rounded-2xl p-3">
              <span className="text-fog/50 block">{t('boleto.fee')}</span>
              <span className="text-brass font-medium">+{fmtSats(quote.feeSats, locale)} ({quote.feePercent}%)</span>
            </div>
          </div>

          <div className="bg-glow/10 border border-glow/30 rounded-2xl p-4 text-center">
            <span className="text-fog/60 text-sm block mb-1">{t('boleto.totalLabel')}</span>
            <span className="text-2xl font-bold text-glow">{fmtSats(quote.totalSats, locale)}</span>
          </div>

          <div className="text-xs text-fog/40 text-center">
            {t('boleto.rate')}: 1 BTC = {fmt(quote.btcRate, locale)}
            {quote.dueDate && (
              <> · {t('boleto.dueDate')}: {new Date(quote.dueDate).toLocaleDateString(locale)}</>
            )}
          </div>

          <div className="flex gap-3">
            <button className="btn-secondary flex-1" onClick={handleReset}>
              {t('boleto.cancel')}
            </button>
            <button className="btn-primary flex-1" onClick={handleConfirm}>
              {t('boleto.confirmPay')}
            </button>
          </div>
        </div>
      )}

      {/* Step: Paying — waiting for LN payment */}
      {step === 'paying' && quote && (
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold text-fog">{t('boleto.payingTitle')}</h3>

          {/* Amount — prominent at top */}
          <div className="text-center">
            <span className="text-2xl font-bold text-glow">{fmtSats(quote.totalSats, locale)}</span>
            <span className="text-fog/50 text-sm block">{fmt(quote.amountBrl, locale)}</span>
          </div>

          {/* Pay with Node — primary action */}
          {!status?.invoicePaid && (
            <>
              <button
                className="btn-primary w-full flex items-center justify-center gap-2"
                onClick={() => handlePayWithNode(quote.invoice)}
                disabled={payingWithNode || paidWithNode}
              >
                {paidWithNode ? (
                  <>
                    <svg className="w-4 h-4 text-ink" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={3}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
                    {t('boleto.paymentSent')}
                  </>
                ) : (
                  <>
                    <span>⚡</span>
                    {payingWithNode ? t('boleto.payingWithNode') : t('boleto.payWithNode')}
                  </>
                )}
              </button>

              <OrDivider label={t('boleto.orExternal')} />
            </>
          )}

          {/* QR code for external wallet */}
          {!status?.invoicePaid && (
            <div className="bg-white rounded-xl p-4 mx-auto w-fit">
              {payingQr ? (
                <img src={payingQr} alt="QR Code" className="w-[220px] h-[220px]" />
              ) : (
                <div className="w-[220px] h-[220px] flex items-center justify-center text-gray-400 text-sm">Generating QR...</div>
              )}
            </div>
          )}

          {/* Invoice copy */}
          {!status?.invoicePaid && (
            <div className="bg-ink/40 rounded-2xl p-4 space-y-2">
              <span className="text-fog/50 text-xs block">{t('boleto.invoiceLabel')}</span>
              <div className="font-mono text-xs text-fog/80 break-all leading-relaxed max-h-20 overflow-y-auto">
                {quote.invoice}
              </div>
              <button
                className="btn-secondary text-xs py-1 px-3"
                onClick={copyInvoice}
              >
                {copied ? t('boleto.copied') : t('boleto.copyInvoice')}
              </button>
            </div>
          )}

          {error && <p className="text-red-400 text-sm">{error}</p>}

          {/* Status indicator */}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-sm">
              {status?.invoicePaid ? (
                <>
                  <svg className="w-4 h-4 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={3}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  <span className="text-emerald-400">{t('boleto.processingBoleto')}</span>
                </>
              ) : (
                <>
                  <svg className="w-4 h-4 animate-spin text-glow" viewBox="0 0 24 24" fill="none">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                  <span className="text-fog/60">{t('boleto.waitingPayment')}</span>
                </>
              )}
            </div>
            {!status?.invoicePaid && quoteCountdown.remaining && (
              <span className={`text-xs font-mono ${quoteCountdown.expired ? 'text-red-400' : 'text-fog/40'}`}>
                ⏱ {quoteCountdown.remaining}
              </span>
            )}
          </div>

          {!status?.invoicePaid && (
            <button className="btn-secondary w-full text-sm" onClick={handleReset}>
              {t('boleto.cancel')}
            </button>
          )}
        </div>
      )}

      {/* Step: Done */}
      {step === 'done' && quote && status && (
        <div className="section-card space-y-4 text-center">
          <div className="w-16 h-16 mx-auto rounded-full bg-emerald-500/20 flex items-center justify-center">
            <svg className="w-8 h-8 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          </div>
          <h3 className="text-lg font-semibold text-emerald-400">{t('boleto.success')}</h3>
          <p className="text-fog/60 text-sm">{t('boleto.successDetail')}</p>

          <div className="grid grid-cols-2 gap-3 text-sm">
            <div className="bg-ink/40 rounded-2xl p-3">
              <span className="text-fog/50 block">{t('boleto.valueBrl')}</span>
              <span className="text-fog font-medium">{fmt(status.amountBrl || quote.amountBrl, locale)}</span>
            </div>
            <div className="bg-ink/40 rounded-2xl p-3">
              <span className="text-fog/50 block">{t('boleto.paidSats')}</span>
              <span className="text-fog font-medium">{fmtSats(status.totalSats || quote.totalSats, locale)}</span>
            </div>
          </div>

          {status.protocolo && (
            <div className="text-xs text-fog/40">
              {t('boleto.protocolo')}: <span className="font-mono">{status.protocolo}</span>
            </div>
          )}

          <button className="btn-primary w-full" onClick={handleReset}>
            {t('boleto.payAnother')}
          </button>
        </div>
      )}

      {/* Step: Error */}
      {step === 'error' && (
        <div className="section-card space-y-4 text-center">
          <div className="w-16 h-16 mx-auto rounded-full bg-ember/20 flex items-center justify-center">
            <svg className="w-8 h-8 text-ember" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </div>
          <h3 className="text-lg font-semibold text-ember">{t('boleto.errorTitle')}</h3>
          <p className="text-fog/60 text-sm">
            {error || t('boleto.errorGeneric')}
          </p>
          <button className="btn-primary w-full" onClick={handleReset}>
            {t('boleto.tryAgain')}
          </button>
        </div>
      )}

      {/* History */}
      {history.length > 0 && (
        <div className="section-card">
          <h3 className="text-lg font-semibold text-fog mb-3">{t('boleto.history')}</h3>
          <div className="space-y-3">
            {history.map(h => (
              <div key={h.paymentHash} className="bg-ink/40 rounded-2xl p-3 flex items-center justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 mb-1">
                    <span className="text-fog font-medium text-sm truncate">{h.bankName}</span>
                    <StatusBadge status={h.status} label={t(`boleto.status.${h.status}`, h.status)} />
                  </div>
                  <span className="text-fog/40 text-xs">
                    {new Date(h.createdAt).toLocaleString(locale, { dateStyle: 'short', timeStyle: 'short' })}
                  </span>
                </div>
                <div className="text-right shrink-0">
                  <span className="text-fog font-medium text-sm block">{fmt(h.amountBrl, locale)}</span>
                  <span className="text-fog/50 text-xs">{fmtSats(h.totalSats, locale)}</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  )
}
