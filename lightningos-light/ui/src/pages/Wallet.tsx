import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createInvoice, decodeInvoice, getLnChannels, getMempoolFees, getWalletAddress, getWalletSummary, payInvoice, sendOnchain } from '../api'
import { getLocale } from '../i18n'

const emptySummary = {
  balances: {
    onchain_sat: 0,
    lightning_sat: 0,
    onchain_confirmed_sat: 0,
    onchain_unconfirmed_sat: 0,
    lightning_local_sat: 0,
    lightning_unsettled_local_sat: 0
  },
  activity: []
}

export default function Wallet() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const satFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 })
  const satDecimalFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 3 })
  const [summary, setSummary] = useState<any>(emptySummary)
  const [summaryError, setSummaryError] = useState('')
  const [summaryWarning, setSummaryWarning] = useState('')
  const [summaryLoading, setSummaryLoading] = useState(true)
  const [address, setAddress] = useState('')
  const [addressStatus, setAddressStatus] = useState('')
  const [addressLoading, setAddressLoading] = useState(false)
  const [showAddress, setShowAddress] = useState(false)
  const [copied, setCopied] = useState(false)
  const [sendOpen, setSendOpen] = useState(false)
  const [sendAddress, setSendAddress] = useState('')
  const [sendAmount, setSendAmount] = useState('')
  const [sendSweepAll, setSendSweepAll] = useState(false)
  const [sendFeeRate, setSendFeeRate] = useState('')
  const [sendFeeHint, setSendFeeHint] = useState<{ fastest?: number; hour?: number } | null>(null)
  const [sendFeeStatus, setSendFeeStatus] = useState('')
  const [sendStatus, setSendStatus] = useState('')
  const [sendRunning, setSendRunning] = useState(false)
  const [amount, setAmount] = useState('')
  const [memo, setMemo] = useState('')
  const [invoice, setInvoice] = useState('')
  const [invoiceCopied, setInvoiceCopied] = useState(false)
  const [invoiceNotice, setInvoiceNotice] = useState('')
  const [paymentRequest, setPaymentRequest] = useState('')
  const [payAmount, setPayAmount] = useState('')
  const [decode, setDecode] = useState<any>(null)
  const [decodeError, setDecodeError] = useState('')
  const [decodeLoading, setDecodeLoading] = useState(false)
  const [status, setStatus] = useState('')
  const [channels, setChannels] = useState<any[]>([])
  const [channelsError, setChannelsError] = useState('')
  const [channelsLoading, setChannelsLoading] = useState(true)
  const [outgoingChannelPoint, setOutgoingChannelPoint] = useState('')

  const normalizePaymentInput = (value: string) => (value ? value.replace(/\s+/g, '') : '')

  const stripLightningPrefix = (value: string) => {
    const cleaned = normalizePaymentInput(value)
    if (cleaned.toLowerCase().startsWith('lightning:')) {
      return cleaned.slice('lightning:'.length)
    }
    return cleaned
  }

  const isLightningAddressInput = (value: string) => {
    const cleaned = stripLightningPrefix(value)
    const parts = cleaned.split('@')
    return parts.length === 2 && parts[0] && parts[1]
  }

  useEffect(() => {
    let mounted = true
    const load = async () => {
      setSummaryError('')
      setSummaryWarning('')
      try {
        const data = await getWalletSummary()
        if (!mounted) return
        setSummary(data || emptySummary)
        setSummaryWarning(data?.warning || '')
      } catch (err: any) {
        if (!mounted) return
        const message = err?.message || t('wallet.summaryUnavailable')
        setSummaryError(message)
      } finally {
        if (!mounted) return
        setSummaryLoading(false)
      }
    }
    load()
    const timer = setInterval(load, 30000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    let mounted = true
    getMempoolFees()
      .then((res: any) => {
        if (!mounted) return
        const fastest = Number(res?.fastestFee || 0)
        const hour = Number(res?.hourFee || 0)
        setSendFeeHint({ fastest, hour })
        setSendFeeRate((prev) => (prev ? prev : fastest > 0 ? String(fastest) : prev))
        setSendFeeStatus('')
      })
      .catch(() => {
        if (!mounted) return
        setSendFeeStatus(t('wallet.feeSuggestionsUnavailable'))
      })
    return () => {
      mounted = false
    }
  }, [])

  useEffect(() => {
    let mounted = true
    const load = async (initial: boolean) => {
      if (initial) {
        setChannelsLoading(true)
      }
      setChannelsError('')
      try {
        const res: any = await getLnChannels()
        if (!mounted) return
        setChannels(Array.isArray(res?.channels) ? res.channels : [])
      } catch (err: any) {
        if (!mounted) return
        setChannelsError(err?.message || t('wallet.channelsUnavailable'))
      } finally {
        if (!mounted) return
        setChannelsLoading(false)
      }
    }
    load(true)
    const timer = setInterval(() => load(false), 30000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    const cleaned = stripLightningPrefix(paymentRequest)
    if (!cleaned) {
      setDecode(null)
      setDecodeError('')
      setDecodeLoading(false)
      return
    }
    if (isLightningAddressInput(cleaned)) {
      setDecode(null)
      setDecodeError('')
      setDecodeLoading(false)
      return
    }

    setDecodeLoading(true)
    const timer = setTimeout(async () => {
      try {
        const res = await decodeInvoice({ payment_request: cleaned })
        setDecode(res)
        setDecodeError('')
      } catch (err: any) {
        setDecode(null)
        setDecodeError(err?.message || t('wallet.invalidInvoice'))
      } finally {
        setDecodeLoading(false)
      }
    }, 400)

    return () => clearTimeout(timer)
  }, [paymentRequest])

  const cleanedPaymentRequest = stripLightningPrefix(paymentRequest)
  const isLnAddress = isLightningAddressInput(cleanedPaymentRequest)
  const payAmountSat = Number(payAmount || 0)
  const onchainBalance = summary?.balances?.onchain_sat ?? 0
  const onchainConfirmedBalance = Number(summary?.balances?.onchain_confirmed_sat ?? onchainBalance)
  const onchainUnconfirmedBalance = Number(summary?.balances?.onchain_unconfirmed_sat ?? 0)
  const lightningBalance = summary?.balances?.lightning_sat ?? 0
  const lightningLocalBalance = Number(summary?.balances?.lightning_local_sat ?? lightningBalance)
  const lightningUnsettledLocalBalance = Number(summary?.balances?.lightning_unsettled_local_sat ?? 0)
  const lightningTotalBalance = lightningLocalBalance + lightningUnsettledLocalBalance
  const activity = summary?.activity ?? []
  const summaryTone = summaryError && summaryError.toLowerCase().includes('timeout')
    ? 'text-brass'
    : 'text-ember'

  const isRebalanceActivity = (item: any) => {
    const type = String(item?.type || '').toLowerCase()
    if (type === 'rebalance') return true
    const memo = typeof item?.memo === 'string' ? item.memo.trim().toLowerCase() : ''
    return memo.startsWith('rebalance:') || memo.startsWith('rebalance attempt')
  }

  const formatSats = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return satFormatter.format(value)
  }

  const formatSatsDecimal = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return satDecimalFormatter.format(value)
  }

  const formatTimestamp = (value: any) => {
    if (!value) return t('common.unknownTime')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return t('common.unknownTime')
    return parsed.toLocaleString(locale, {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    })
  }

  const activityDirection = (item: any) => {
    const direct = String(item?.direction || '').toLowerCase()
    if (direct === 'in' || direct === 'out') return direct
    const type = String(item?.type || '').toLowerCase()
    if (type === 'invoice' || type === 'onchain_in') return 'in'
    if (type === 'payment' || type === 'onchain_out') return 'out'
    return ''
  }

  const activityNetwork = (item: any) => {
    const network = String(item?.network || '').toLowerCase()
    if (network === 'lightning' || network === 'onchain') return network
    const type = String(item?.type || '').toLowerCase()
    if (type === 'invoice' || type === 'payment') return 'lightning'
    if (type.startsWith('onchain')) return 'onchain'
    return ''
  }

  const formatActivityType = (item: any) => {
    const type = String(item?.type || '').toLowerCase()
    const network = activityNetwork(item)
    const direction = activityDirection(item)
    const keysend = Boolean(item?.keysend)
    let label = t('wallet.activity')
    if (keysend) label = t('wallet.keysend')
    else if (type === 'invoice') label = t('wallet.invoice')
    else if (type === 'payment') label = t('wallet.payment')
    else if (network === 'onchain') label = direction === 'out' ? t('wallet.onchainSend') : t('wallet.onchainDeposit')
    else if (type) label = type.charAt(0).toUpperCase() + type.slice(1)
    if (network === 'lightning') return `⚡ ${label}`
    return label
  }

  const orderedActivity = [...activity]
    .filter((item: any) => !isRebalanceActivity(item))
    .sort((a: any, b: any) => {
      const timeA = new Date(a?.timestamp || 0).getTime()
      const timeB = new Date(b?.timestamp || 0).getTime()
      return timeB - timeA
    })

  const trimMemo = (value: string, max = 30) => {
    const trimmed = value.trim()
    if (trimmed.length <= max) return trimmed
    return `${trimmed.slice(0, max - 3)}...`
  }

  const decodedAmountSat = () => {
    if (isLnAddress) {
      return payAmountSat > 0 ? payAmountSat : 0
    }
    if (!decode) return 0
    const amountSat = Number(decode.amount_sat || 0)
    const amountMsat = Number(decode.amount_msat || 0)
    if (amountSat > 0) return amountSat
    if (amountMsat > 0) return Math.ceil(amountMsat / 1000)
    return 0
  }

  const amountForFilter = decodedAmountSat()
  const availableChannels = channels
    .filter((ch) => ch && ch.active && ch.channel_point)
    .filter((ch) => amountForFilter <= 0 || Number(ch.local_balance_sat || 0) >= amountForFilter)
    .sort((a, b) => Number(b.local_balance_sat || 0) - Number(a.local_balance_sat || 0))

  const formatChannelLabel = (ch: any) => {
    const alias = String(ch.peer_alias || '').trim()
    const pubkey = String(ch.remote_pubkey || '').trim()
    const peerLabel = alias || (pubkey ? `${pubkey.slice(0, 10)}...` : t('wallet.unknownPeer'))
    const point = String(ch.channel_point || '').trim()
    const shortPoint = point && point.length > 16 ? `${point.slice(0, 8)}...${point.slice(-4)}` : point
    const localBalance = Number(ch.local_balance_sat || 0)
    return `${peerLabel} | ${shortPoint} | ${formatSats(localBalance)} sats`
  }

  useEffect(() => {
    if (!outgoingChannelPoint) return
    const exists = availableChannels.some((ch) => ch.channel_point === outgoingChannelPoint)
    if (!exists) {
      setOutgoingChannelPoint('')
    }
  }, [availableChannels, outgoingChannelPoint])

  const handleAddFunds = async () => {
    setShowAddress(true)
    setAddress('')
    setAddressStatus('')
    setCopied(false)
    setAddressLoading(true)
    try {
      const res = await getWalletAddress()
      setAddress(res?.address || '')
      if (!res?.address) {
        setAddressStatus(t('wallet.addressUnavailable'))
      }
    } catch (err: any) {
      setAddressStatus(err?.message || t('wallet.addressFetchFailed'))
    } finally {
      setAddressLoading(false)
    }
  }

  const handleCopy = async () => {
    if (!address) return
    try {
      await navigator.clipboard.writeText(address)
      setCopied(true)
    } catch {
      setAddressStatus(t('common.copyFailedManual'))
    }
  }

  const handleToggleSend = () => {
    setSendOpen((prev) => !prev)
    setSendStatus('')
  }

  const handleSendOnchain = async () => {
    const target = sendAddress.trim()
    const amountSat = Number(sendAmount || 0)
    const feeRate = Number(sendFeeRate || 0)
    if (!target) {
      setSendStatus(t('wallet.destinationRequired'))
      return
    }
    if (!sendSweepAll && amountSat <= 0) {
      setSendStatus(t('wallet.amountMustBePositive'))
      return
    }
    setSendRunning(true)
    setSendStatus(t('wallet.sendingOnchain'))
    try {
      const payload = {
        address: target,
        sat_per_vbyte: feeRate > 0 ? feeRate : undefined,
        ...(sendSweepAll ? { sweep_all: true } : { amount_sat: amountSat })
      }
      const res = await sendOnchain(payload)
      const txid = res?.txid ? ` Txid: ${res.txid}` : ''
      setSendStatus(t('wallet.onchainBroadcast', { txid }))
      setSendAddress('')
      setSendAmount('')
      setSendSweepAll(false)
    } catch (err: any) {
      setSendStatus(err?.message || t('wallet.onchainSendFailed'))
    } finally {
      setSendRunning(false)
    }
  }

  const handleInvoice = async () => {
    setStatus(t('wallet.creatingInvoice'))
    setInvoiceNotice('')
    setInvoiceCopied(false)
    try {
      const res = await createInvoice({ amount_sat: Number(amount), memo })
      setInvoice(res.payment_request)
      setStatus(t('wallet.invoiceReady'))
    } catch {
      setStatus(t('wallet.invoiceFailed'))
    }
  }

  const handleCopyInvoice = async () => {
    if (!invoice) return
    try {
      await navigator.clipboard.writeText(invoice)
      setInvoiceCopied(true)
    } catch {
      setInvoiceNotice(t('common.copyFailedManual'))
    }
  }

  const handleClearInvoice = () => {
    setInvoice('')
    setInvoiceCopied(false)
    setInvoiceNotice('')
  }

  const handlePay = async () => {
    if (!cleanedPaymentRequest) {
      setStatus(t('wallet.paymentRequestRequired'))
      return
    }
    if (isLnAddress && payAmountSat <= 0) {
      setStatus(t('wallet.amountPositiveForLightningAddress'))
      return
    }
    setStatus(t('wallet.payingInvoice'))
    try {
      await payInvoice({
        payment_request: cleanedPaymentRequest,
        channel_point: outgoingChannelPoint || undefined,
        amount_sat: isLnAddress ? payAmountSat : undefined
      })
      setStatus(t('wallet.paymentSent'))
    } catch (err: any) {
      setStatus(err?.message || t('wallet.paymentFailed'))
    }
  }

  const decodedAmount = () => {
    if (!decode) return ''
    const amountSat = Number(decode.amount_sat || 0)
    const amountMsat = Number(decode.amount_msat || 0)
    if (amountSat > 0) return `${formatSats(amountSat)} sats`
    if (amountMsat > 0) return `${formatSatsDecimal(amountMsat / 1000)} sats`
    return t('wallet.amountless')
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('wallet.title')}</h2>
        <p className="text-fog/60">{t('wallet.subtitle')}</p>
        <div className="mt-4 grid gap-4 lg:grid-cols-2 text-sm">
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-4 space-y-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <p className="text-fog/60">{t('wallet.onchain')}</p>
                <p className="text-xl">{formatSats(onchainConfirmedBalance)} sats</p>
                {onchainUnconfirmedBalance > 0 && (
                  <p className="mt-1 text-xs text-fog/50">
                    {t('wallet.onchainUnconfirmed', { amount: formatSats(onchainUnconfirmedBalance) })}
                  </p>
                )}
              </div>
              <div className="flex flex-wrap gap-2">
                <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleAddFunds}>
                  {t('wallet.addFunds')}
                </button>
                <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleToggleSend}>
                  {sendOpen ? t('wallet.hideSend') : t('wallet.sendFunds')}
                </button>
              </div>
            </div>
            {showAddress && (
              <div className="rounded-2xl border border-white/10 bg-ink/70 p-3">
                <div className="flex items-center justify-between text-xs text-fog/60">
                  <span>{t('wallet.onchainDepositAddress')}</span>
                  <button className="text-fog/50 hover:text-fog" onClick={() => setShowAddress(false)}>
                    {t('common.close')}
                  </button>
                </div>
                {addressLoading && (
                  <p className="mt-2 text-xs text-fog/60">{t('wallet.generatingAddress')}</p>
                )}
                {!addressLoading && address && (
                  <>
                    <p className="mt-2 text-xs font-mono break-all">{address}</p>
                    <div className="mt-2 flex items-center gap-2">
                      <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleCopy}>
                        {copied ? t('common.copied') : t('wallet.copyAddress')}
                      </button>
                    </div>
                  </>
                )}
                {!addressLoading && !address && addressStatus && (
                  <p className="mt-2 text-xs text-ember">{addressStatus}</p>
                )}
              </div>
            )}
            {sendOpen && (
              <div className="rounded-2xl border border-white/10 bg-ink/80 p-3 space-y-3">
                <div className="flex items-center justify-between text-xs text-fog/60">
                  <span>{t('wallet.sendOnchain')}</span>
                  <button className="text-fog/50 hover:text-fog" onClick={handleToggleSend}>
                    {t('common.close')}
                  </button>
                </div>
                <input
                  className="input-field"
                  placeholder={t('wallet.destinationAddress')}
                  value={sendAddress}
                  onChange={(e) => setSendAddress(e.target.value)}
                />
                <div className="grid gap-3 lg:grid-cols-2 lg:items-start">
                  <div className="space-y-2 lg:max-w-[360px]">
                    <label className="text-xs text-fog/60">{t('wallet.amountSats')}</label>
                    <input
                      className="input-field"
                      placeholder={t('wallet.amountSats')}
                      type="number"
                      min={1}
                      value={sendAmount}
                      onChange={(e) => setSendAmount(e.target.value)}
                      disabled={sendSweepAll}
                    />
                    <label className="flex items-center gap-2 text-xs text-fog/60">
                      <input
                        type="checkbox"
                        checked={sendSweepAll}
                        onChange={(e) => {
                          const checked = e.target.checked
                          setSendSweepAll(checked)
                          if (checked) {
                            setSendAmount('')
                          }
                        }}
                      />
                      {t('wallet.sweepAll')}
                    </label>
                    {sendSweepAll && (
                      <p className="text-xs text-brass">
                        {t('wallet.sweepAllWarning')}
                      </p>
                    )}
                  </div>
                  <div className="space-y-2 lg:max-w-[360px]">
                    <label className="text-xs text-fog/60">
                      {t('wallet.feeRate')}
                      <span className="ml-2 text-fog/50">
                        {t('wallet.feeHint', { fastest: sendFeeHint?.fastest ?? '-', hour: sendFeeHint?.hour ?? '-' })}
                      </span>
                    </label>
                    <div className="flex items-center gap-2">
                      <input
                        className="input-field flex-1 min-w-[120px]"
                        placeholder={t('common.auto')}
                        type="number"
                        min={1}
                        value={sendFeeRate}
                        onChange={(e) => setSendFeeRate(e.target.value)}
                      />
                      <button
                        className="btn-secondary text-xs px-3 py-2"
                        type="button"
                        onClick={() => {
                          if (sendFeeHint?.fastest) {
                            setSendFeeRate(String(sendFeeHint.fastest))
                          }
                        }}
                        disabled={!sendFeeHint?.fastest}
                      >
                        {t('wallet.useFastest')}
                      </button>
                    </div>
                    {sendFeeStatus && <p className="text-xs text-fog/50">{sendFeeStatus}</p>}
                  </div>
                </div>
                <button
                  className="btn-primary disabled:opacity-60 disabled:cursor-not-allowed"
                  onClick={handleSendOnchain}
                  disabled={sendRunning}
                >
                  {sendRunning ? t('wallet.sending') : t('wallet.sendOnchain')}
                </button>
                {sendStatus && <p className="text-xs text-brass break-words">{sendStatus}</p>}
              </div>
            )}
          </div>
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-4">
            <p className="text-fog/60">{t('wallet.lightning')}</p>
            <p className="text-xl">{formatSats(lightningLocalBalance)} sats</p>
            {lightningUnsettledLocalBalance > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {t('wallet.lightningUnsettled', { amount: formatSats(lightningUnsettledLocalBalance) })}
              </p>
            )}
            {lightningUnsettledLocalBalance > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {t('wallet.lightningTotal', { amount: formatSats(lightningTotalBalance) })}
              </p>
            )}
            <p className="mt-2 text-xs text-fog/50">{t('wallet.lightningHint')}</p>
          </div>
        </div>
        {summaryLoading && !summaryError && (
          <p className="mt-4 text-sm text-fog/60">{t('wallet.fetchingBalances')}</p>
        )}
        {summaryWarning && !summaryError && (
          <p className="mt-4 text-sm text-brass">{summaryWarning}</p>
        )}
        {summaryError && (
          <p className={`mt-4 text-sm ${summaryTone}`}>{t('wallet.statusLabel', { status: summaryError })}</p>
        )}
        {status && <p className="mt-4 text-sm text-brass">{status}</p>}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('wallet.createInvoice')}</h3>
          <input className="input-field" placeholder={t('wallet.amountSats')} value={amount} onChange={(e) => setAmount(e.target.value)} />
          <input className="input-field" placeholder={t('wallet.memo')} value={memo} onChange={(e) => setMemo(e.target.value)} />
          <button className="btn-primary" onClick={handleInvoice}>{t('wallet.generateInvoice')}</button>
          {invoice && (
            <div className="rounded-2xl border border-white/10 bg-ink/60 p-3">
              <div className="flex items-center justify-between text-xs text-fog/60">
                <span>{t('wallet.invoiceLightning')}</span>
                <button className="text-fog/50 hover:text-fog" onClick={handleClearInvoice}>
                  {t('common.close')}
                </button>
              </div>
              <p className="mt-2 text-xs font-mono break-all">{invoice}</p>
              <div className="mt-2 flex items-center gap-2">
                <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleCopyInvoice}>
                  {invoiceCopied ? t('common.copied') : t('wallet.copyInvoice')}
                </button>
              </div>
              {invoiceNotice && (
                <p className="mt-2 text-xs text-ember">{invoiceNotice}</p>
              )}
            </div>
          )}
        </div>

        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('wallet.payInvoice')}</h3>
          <textarea className="input-field min-h-[140px]" placeholder={t('wallet.paymentRequestPlaceholder')} value={paymentRequest} onChange={(e) => setPaymentRequest(e.target.value)} />
          {isLnAddress && (
            <div className="space-y-2">
              <label className="text-xs text-fog/60">{t('wallet.amountSats')}</label>
              <input
                className="input-field"
                placeholder={t('wallet.amountSats')}
                type="number"
                min={1}
                value={payAmount}
                onChange={(e) => setPayAmount(e.target.value)}
              />
              <p className="text-xs text-fog/50">{t('wallet.lightningAddressDetected')}</p>
            </div>
          )}
          {decodeLoading && (
            <p className="text-xs text-fog/60">{t('wallet.decodingInvoice')}</p>
          )}
          {!decodeLoading && decodeError && (
            <p className="text-xs text-ember">{decodeError}</p>
          )}
          {!decodeLoading && !decodeError && decode && (
            <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 text-xs">
              <div className="flex items-center justify-between">
                <span className="text-fog/60">{t('wallet.amount')}</span>
                <span>{decodedAmount()}</span>
              </div>
              <div className="mt-2 flex items-center justify-between">
                <span className="text-fog/60">{t('wallet.memo')}</span>
                <span className="max-w-[220px] truncate text-right">{decode.memo || t('wallet.noMemo')}</span>
              </div>
            </div>
          )}
          <div className="space-y-2">
            <label className="text-xs text-fog/60">{t('wallet.outgoingChannel')}</label>
            <select
              className="input-field"
              value={outgoingChannelPoint}
              onChange={(e) => setOutgoingChannelPoint(e.target.value)}
            >
              <option value="">{t('wallet.automaticLnd')}</option>
              {availableChannels.map((ch) => (
                <option key={ch.channel_point} value={ch.channel_point}>
                  {formatChannelLabel(ch)}
                </option>
              ))}
            </select>
            <p className="text-xs text-fog/50">
              {t('wallet.outgoingChannelHint')}
            </p>
            {!channelsLoading && amountForFilter > 0 && availableChannels.length === 0 && (
              <p className="text-xs text-brass">{t('wallet.noChannelsForAmount')}</p>
            )}
            {channelsError && <p className="text-xs text-fog/50">{channelsError}</p>}
          </div>
          <button className="btn-primary" onClick={handlePay}>{t('wallet.payInvoice')}</button>
        </div>
      </div>

      <div className="section-card">
        <h3 className="text-lg font-semibold">{t('wallet.recentActivity')}</h3>
        <div className="mt-4 max-h-[360px] overflow-y-auto pr-2">
          <div className="space-y-2 text-sm">
          {summaryError ? (
            <p className="text-fog/60">{t('wallet.activityUnavailable')}</p>
          ) : orderedActivity.length ? orderedActivity.map((item: any, idx: number) => {
            const typeLabel = formatActivityType(item)
            const statusLabel = String(item.status || t('common.unknown')).replace(/_/g, ' ').toUpperCase()
            const direction = activityDirection(item)
            const arrow = direction === 'in' ? '<-' : direction === 'out' ? '->' : '.'
            const arrowTone = direction === 'in' ? 'text-glow' : direction === 'out' ? 'text-ember' : 'text-fog/50'
            const memo = typeof item.memo === 'string' ? item.memo.trim() : ''
            const memoLabel = String(item?.type || '').toLowerCase() === 'invoice' && memo
              ? ` - ${trimMemo(memo, 30)}`
              : ''
            return (
              <div key={`${item.type}-${idx}`} className="grid items-center gap-3 border-b border-white/10 pb-2 sm:grid-cols-[160px_1fr_auto_auto]">
                <span className="text-xs text-fog/50">{formatTimestamp(item.timestamp)}</span>
                <div className="min-w-0">
                  <span className="text-fog/70">{typeLabel}</span>
                  <span className="text-fog/50"> - {statusLabel}{memoLabel}</span>
                </div>
                <span className={`text-xs font-mono ${arrowTone}`}>{arrow}</span>
                <span className="text-right">{formatSats(Number(item.amount_sat || 0))} sats</span>
              </div>
            )
          }) : (
            <p className="text-fog/60">{t('wallet.noRecentActivity')}</p>
          )}
          </div>
        </div>
      </div>
    </section>
  )
}
