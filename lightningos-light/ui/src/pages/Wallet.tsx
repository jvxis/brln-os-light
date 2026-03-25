import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import QRCode from 'qrcode'
import { createInvoice, decodeInvoice, getLnChannels, getMempoolFees, getWalletActivity, getWalletAddress, getWalletSummary, payInvoice, previewOnchainSend, sendOnchain } from '../api'
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

type OnchainSendPreview = {
  address: string
  sweep_all: boolean
  requested_amount_sat: number
  recipient_amount_sat: number
  fee_sat: number
  change_sat: number
  total_debit_sat: number
  spendable_sat: number
  spendable_utxo_count: number
  selected_input_count: number
  selected_input_sat: number
  estimated_vbytes: number
  sat_per_vbyte: number
  enough_funds: boolean
  exact: boolean
  message?: string
}

type WalletActivityRange = '7d' | '1m' | '1a'

type WalletActivityItem = {
  type?: string
  network?: string
  direction?: string
  amount_sat?: number
  fee_sat?: number
  confirmations?: number
  block_height?: number
  addresses?: string[]
  memo?: string
  timestamp?: string
  created_at?: string
  settled_at?: string
  status?: string
  txid?: string
  keysend?: boolean
  channel_id?: number
  channel_point?: string
  channel_alias?: string
  payment_hash?: string
}

const walletActivityPageSize = 100

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
  const [addressQr, setAddressQr] = useState<string | null>(null)
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
  const [sendPreview, setSendPreview] = useState<OnchainSendPreview | null>(null)
  const [sendPreviewLoading, setSendPreviewLoading] = useState(false)
  const [sendPreviewStatus, setSendPreviewStatus] = useState('')
  const [sendStatus, setSendStatus] = useState('')
  const [sendTxid, setSendTxid] = useState('')
  const [sendRunning, setSendRunning] = useState(false)
  const [amount, setAmount] = useState('')
  const [memo, setMemo] = useState('')
  const [invoiceExpiry, setInvoiceExpiry] = useState('3600')
  const [invoice, setInvoice] = useState('')
  const [invoiceQr, setInvoiceQr] = useState<string | null>(null)
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
  const [activityRange, setActivityRange] = useState<WalletActivityRange>('7d')
  const [activityItems, setActivityItems] = useState<WalletActivityItem[]>([])
  const [activityError, setActivityError] = useState('')
  const [activityLoading, setActivityLoading] = useState(true)
  const [activityLoadingMore, setActivityLoadingMore] = useState(false)
  const [activityHasMore, setActivityHasMore] = useState(false)
  const [selectedActivity, setSelectedActivity] = useState<WalletActivityItem | null>(null)

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

  const loadActivity = async (options?: { offset?: number; limit?: number; append?: boolean; silent?: boolean }) => {
    const offset = options?.offset ?? 0
    const limit = options?.limit ?? walletActivityPageSize
    const append = Boolean(options?.append)
    const silent = Boolean(options?.silent)

    if (append) {
      setActivityLoadingMore(true)
    } else if (!silent) {
      setActivityLoading(true)
    }
    if (!silent) {
      setActivityError('')
    }

    try {
      const res: any = await getWalletActivity(activityRange, limit, offset)
      const nextItems = Array.isArray(res?.items) ? res.items : []
      setActivityItems((prev) => append ? [...prev, ...nextItems] : nextItems)
      setActivityHasMore(Boolean(res?.has_more))
    } catch (err: any) {
      if (!silent || activityItems.length === 0) {
        setActivityError(err?.message || t('wallet.activityUnavailable'))
      }
    } finally {
      if (append) {
        setActivityLoadingMore(false)
      } else {
        setActivityLoading(false)
      }
    }
  }

  useEffect(() => {
    setSelectedActivity(null)
    void loadActivity({ offset: 0, limit: walletActivityPageSize })
  }, [activityRange, t])

  useEffect(() => {
    const timer = setInterval(() => {
      const visibleCount = Math.max(activityItems.length, walletActivityPageSize)
      void loadActivity({ offset: 0, limit: visibleCount, silent: true })
    }, 30000)
    return () => clearInterval(timer)
  }, [activityRange, activityItems.length, t])

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
  const activity = activityItems
  const summaryTone = summaryError && summaryError.toLowerCase().includes('timeout')
    ? 'text-brass'
    : 'text-ember'

  const isRebalanceActivity = (item: WalletActivityItem) => {
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

  const activityDirection = (item: WalletActivityItem) => {
    const direct = String(item?.direction || '').toLowerCase()
    if (direct === 'in' || direct === 'out') return direct
    const type = String(item?.type || '').toLowerCase()
    if (type === 'invoice' || type === 'onchain_in') return 'in'
    if (type === 'payment' || type === 'onchain_out') return 'out'
    return ''
  }

  const activityNetwork = (item: WalletActivityItem) => {
    const network = String(item?.network || '').toLowerCase()
    if (network === 'lightning' || network === 'onchain') return network
    const type = String(item?.type || '').toLowerCase()
    if (type === 'invoice' || type === 'payment') return 'lightning'
    if (type.startsWith('onchain')) return 'onchain'
    return ''
  }

  const formatActivityType = (item: WalletActivityItem) => {
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

  const formatActivityStatus = (item: WalletActivityItem) =>
    String(item?.status || t('common.unknown')).replace(/_/g, ' ').toUpperCase()

  const formatActivityDirectionLabel = (item: WalletActivityItem) => {
    const direction = activityDirection(item)
    if (direction === 'in') return t('wallet.directionIn')
    if (direction === 'out') return t('wallet.directionOut')
    return t('common.unknown')
  }

  const formatActivityNetworkLabel = (item: WalletActivityItem) => {
    const network = activityNetwork(item)
    if (network === 'lightning') return t('wallet.lightning')
    if (network === 'onchain') return t('wallet.onchain')
    return t('common.unknown')
  }

  const hasActivityDetail = (item: WalletActivityItem) => {
    const network = activityNetwork(item)
    return network === 'lightning' || network === 'onchain'
  }

  const orderedActivity = [...activity]
    .filter((item) => !isRebalanceActivity(item))
    .sort((a, b) => {
      const timeA = new Date(a?.timestamp || 0).getTime()
      const timeB = new Date(b?.timestamp || 0).getTime()
      return timeB - timeA
    })

  const rangeWindowMs = activityRange === '7d'
    ? 7 * 24 * 60 * 60 * 1000
    : activityRange === '1m'
      ? 30 * 24 * 60 * 60 * 1000
      : 365 * 24 * 60 * 60 * 1000

  const filteredActivity = orderedActivity.filter((item) => {
    const parsed = new Date(item?.timestamp || 0).getTime()
    if (Number.isNaN(parsed) || parsed <= 0) return false
    return parsed >= Date.now() - rangeWindowMs
  })

  const activityRangeOptions: Array<{ value: WalletActivityRange; label: string }> = [
    { value: '7d', label: t('wallet.activityFilter7d') },
    { value: '1m', label: t('wallet.activityFilter1m') },
    { value: '1a', label: t('wallet.activityFilter1a') }
  ]

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

  useEffect(() => {
    if (!address) {
      setAddressQr(null)
      return
    }
    QRCode.toDataURL(address, { width: 220, margin: 1 })
      .then(setAddressQr)
      .catch(() => setAddressQr(null))
  }, [address])

  useEffect(() => {
    if (!invoice) {
      setInvoiceQr(null)
      return
    }
    QRCode.toDataURL(`lightning:${invoice}`, { width: 220, margin: 1 })
      .then(setInvoiceQr)
      .catch(() => setInvoiceQr(null))
  }, [invoice])

  useEffect(() => {
    if (!selectedActivity) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setSelectedActivity(null)
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [selectedActivity])

  const handleLoadMoreActivity = () => {
    if (activityLoadingMore || !activityHasMore) return
    void loadActivity({
      offset: activityItems.length,
      limit: walletActivityPageSize,
      append: true
    })
  }

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
    setSendTxid('')
    setSendPreview(null)
    setSendPreviewStatus('')
  }

  useEffect(() => {
    if (!sendOpen || sendRunning) {
      setSendPreview(null)
      setSendPreviewLoading(false)
      setSendPreviewStatus('')
      return
    }

    const target = sendAddress.trim()
    const amountSat = Number(sendAmount || 0)
    const feeRate = Number(sendFeeRate || 0)
    if (!target) {
      setSendPreview(null)
      setSendPreviewLoading(false)
      setSendPreviewStatus('')
      return
    }
    if (feeRate <= 0) {
      setSendPreview(null)
      setSendPreviewLoading(false)
      setSendPreviewStatus(t('wallet.sendPreviewFeeRequired'))
      return
    }
    if (!sendSweepAll && amountSat <= 0) {
      setSendPreview(null)
      setSendPreviewLoading(false)
      setSendPreviewStatus('')
      return
    }

    let cancelled = false
    setSendPreviewLoading(true)
    const timer = setTimeout(async () => {
      try {
        const res = await previewOnchainSend({
          address: target,
          sat_per_vbyte: feeRate,
          ...(sendSweepAll ? { sweep_all: true } : { amount_sat: amountSat })
        }) as OnchainSendPreview
        if (cancelled) return
        setSendPreview(res)
        setSendPreviewStatus(res?.message || '')
      } catch (err: any) {
        if (cancelled) return
        setSendPreview(null)
        setSendPreviewStatus(err?.message || t('wallet.sendPreviewUnavailable'))
      } finally {
        if (cancelled) return
        setSendPreviewLoading(false)
      }
    }, 350)

    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [sendOpen, sendAddress, sendAmount, sendFeeRate, sendSweepAll, sendRunning, t])

  const handleSendOnchain = async () => {
    const target = sendAddress.trim()
    const amountSat = Number(sendAmount || 0)
    const feeRate = Number(sendFeeRate || 0)
    setSendTxid('')
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
      setSendStatus(t('wallet.onchainBroadcast', { txid: '' }))
      setSendTxid(String(res?.txid || '').trim())
      setSendAddress('')
      setSendAmount('')
      setSendSweepAll(false)
      setSendPreview(null)
      setSendPreviewStatus('')
    } catch (err: any) {
      setSendTxid('')
      setSendStatus(err?.message || t('wallet.onchainSendFailed'))
    } finally {
      setSendRunning(false)
    }
  }

  const handleInvoice = async () => {
    setStatus(t('wallet.creatingInvoice'))
    setInvoiceNotice('')
    setInvoiceCopied(false)
    const parsedExpiry = Number(invoiceExpiry || 0)
    const expirySeconds = Number.isFinite(parsedExpiry) && parsedExpiry > 0
      ? Math.trunc(parsedExpiry)
      : undefined
    try {
      const res = await createInvoice({
        amount_sat: Number(amount),
        memo,
        expiry_seconds: expirySeconds
      })
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
                    {addressQr && (
                      <img
                        src={addressQr}
                        alt={t('wallet.onchainDepositAddress')}
                        className="mt-3 w-full max-w-[220px] rounded-xl border border-white/10 bg-white p-2"
                      />
                    )}
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
                {(sendPreviewLoading || sendPreview || sendPreviewStatus) && (
                  <div className={`rounded-2xl border p-3 space-y-2 ${sendPreview?.enough_funds === false || sendPreviewStatus ? 'border-ember/40 bg-ember/10' : 'border-white/10 bg-ink/70'}`}>
                    <div className="flex items-center justify-between gap-3 text-xs">
                      <span className="text-fog/60">{t('wallet.sendPreviewTitle')}</span>
                      {sendPreview && (
                        <span className={sendPreview.exact ? 'text-emerald-300' : 'text-fog/50'}>
                          {sendPreview.exact ? t('wallet.sendPreviewExact') : t('wallet.sendPreviewEstimated')}
                        </span>
                      )}
                    </div>
                    {sendPreviewLoading && (
                      <p className="text-xs text-fog/60">{t('wallet.sendPreviewCalculating')}</p>
                    )}
                    {!sendPreviewLoading && sendPreview && (
                      <div className="grid gap-2 text-xs text-fog/80 sm:grid-cols-2">
                        <p>{t('wallet.sendPreviewFee', { amount: formatSats(sendPreview.fee_sat) })}</p>
                        <p>{t('wallet.sendPreviewVbytes', { amount: formatSats(sendPreview.estimated_vbytes), fee: formatSats(sendPreview.sat_per_vbyte) })}</p>
                        <p>{t('wallet.sendPreviewRecipient', { amount: formatSats(sendPreview.recipient_amount_sat) })}</p>
                        <p>{t('wallet.sendPreviewTotalDebit', { amount: formatSats(sendPreview.total_debit_sat) })}</p>
                        <p>{t('wallet.sendPreviewChange', { amount: formatSats(sendPreview.change_sat) })}</p>
                        <p>{t('wallet.sendPreviewInputs', { selected: formatSats(sendPreview.selected_input_count), available: formatSats(sendPreview.spendable_utxo_count) })}</p>
                        <p className="sm:col-span-2">{t('wallet.sendPreviewSpendable', { amount: formatSats(sendPreview.spendable_sat), selected: formatSats(sendPreview.selected_input_sat) })}</p>
                      </div>
                    )}
                    {!sendPreviewLoading && sendPreviewStatus && (
                      <p className={`text-xs ${sendPreview?.enough_funds === false ? 'text-ember' : 'text-fog/60'}`}>{sendPreviewStatus}</p>
                    )}
                  </div>
                )}
                <button
                  className="btn-primary disabled:opacity-60 disabled:cursor-not-allowed"
                  onClick={handleSendOnchain}
                  disabled={sendRunning}
                >
                  {sendRunning ? t('wallet.sending') : t('wallet.sendOnchain')}
                </button>
                {sendStatus && (
                  <p className="text-xs text-brass break-words">
                    {sendStatus}
                    {sendTxid && (
                      <>
                        {' '}Txid:{' '}
                        <a
                          className="hover:underline underline-offset-2 break-all"
                          href={`https://mempool.space/tx/${sendTxid}`}
                          target="_blank"
                          rel="noreferrer"
                        >
                          {sendTxid}
                        </a>
                      </>
                    )}
                  </p>
                )}
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
          <div className="space-y-1">
            <label className="text-xs text-fog/60">{t('wallet.invoiceExpirySeconds')}</label>
            <input
              className="input-field"
              placeholder={t('wallet.invoiceExpirySeconds')}
              type="number"
              min={1}
              value={invoiceExpiry}
              onChange={(e) => setInvoiceExpiry(e.target.value)}
            />
          </div>
          <button className="btn-primary" onClick={handleInvoice}>{t('wallet.generateInvoice')}</button>
          {invoice && (
            <div className="rounded-2xl border border-white/10 bg-ink/60 p-3">
              <div className="flex items-center justify-between text-xs text-fog/60">
                <span>{t('wallet.invoiceLightning')}</span>
                <button className="text-fog/50 hover:text-fog" onClick={handleClearInvoice}>
                  {t('common.close')}
                </button>
              </div>
              {invoiceQr && (
                <img
                  src={invoiceQr}
                  alt={t('wallet.invoiceLightning')}
                  className="mt-3 w-full max-w-[220px] rounded-xl border border-white/10 bg-white p-2"
                />
              )}
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
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('wallet.recentActivity')}</h3>
          <div className="inline-flex rounded-2xl border border-white/10 bg-ink/40 p-1">
            {activityRangeOptions.map((option) => {
              const active = activityRange === option.value
              return (
                <button
                  key={option.value}
                  type="button"
                  onClick={() => setActivityRange(option.value)}
                  className={`rounded-xl px-3 py-1 text-xs font-semibold transition ${active ? 'bg-white/10 text-fog' : 'text-fog/60 hover:text-fog'}`}
                >
                  {option.label}
                </button>
              )
            })}
          </div>
        </div>
        <div className="mt-4 max-h-[360px] overflow-y-auto pr-2">
          <div className="space-y-2 text-sm">
          {activityLoading && !activityError ? (
            <p className="text-fog/60">{t('wallet.fetchingActivity')}</p>
          ) : activityError ? (
            <p className="text-fog/60">{t('wallet.activityUnavailable')}</p>
          ) : filteredActivity.length ? filteredActivity.map((item, idx: number) => {
            const typeLabel = formatActivityType(item)
            const statusLabel = formatActivityStatus(item)
            const direction = activityDirection(item)
            const arrow = direction === 'in' ? '<-' : direction === 'out' ? '->' : '.'
            const arrowTone = direction === 'in' ? 'text-glow' : direction === 'out' ? 'text-ember' : 'text-fog/50'
            const memo = typeof item.memo === 'string' ? item.memo.trim() : ''
            const memoLabel = String(item?.type || '').toLowerCase() === 'invoice' && memo
              ? ` - ${trimMemo(memo, 30)}`
              : ''
            const channelAlias = typeof item?.channel_alias === 'string' ? item.channel_alias.trim() : ''
            const channelLabel = channelAlias ? ` - ${t('wallet.viaChannel', { channel: channelAlias })}` : ''
            const clickable = hasActivityDetail(item)
            const itemKey = item.payment_hash || item.txid || `${item.type || 'activity'}-${item.timestamp || idx}-${idx}`
            return clickable ? (
              <button
                key={itemKey}
                type="button"
                onClick={() => setSelectedActivity(item)}
                className="grid w-full items-center gap-3 rounded-2xl border-b border-white/10 pb-2 text-left transition hover:bg-white/5 focus:outline-none focus:ring-2 focus:ring-white/20 sm:grid-cols-[160px_1fr_auto_auto]"
              >
                <span className="text-xs text-fog/50">{formatTimestamp(item.timestamp)}</span>
                <div className="min-w-0">
                  <span className="text-fog/70">{typeLabel}</span>
                  <span className="text-fog/50"> - {statusLabel}{memoLabel}{channelLabel}</span>
                </div>
                <span className={`text-xs font-mono ${arrowTone}`}>{arrow}</span>
                <span className="text-right">{formatSats(Number(item.amount_sat || 0))} sats</span>
              </button>
            ) : (
              <div key={itemKey} className="grid items-center gap-3 border-b border-white/10 pb-2 sm:grid-cols-[160px_1fr_auto_auto]">
                <span className="text-xs text-fog/50">{formatTimestamp(item.timestamp)}</span>
                <div className="min-w-0">
                  <span className="text-fog/70">{typeLabel}</span>
                  <span className="text-fog/50"> - {statusLabel}{memoLabel}{channelLabel}</span>
                </div>
                <span className={`text-xs font-mono ${arrowTone}`}>{arrow}</span>
                <span className="text-right">{formatSats(Number(item.amount_sat || 0))} sats</span>
              </div>
            )
          }) : (
            <p className="text-fog/60">{orderedActivity.length ? t('wallet.noActivityInRange') : t('wallet.noRecentActivity')}</p>
          )}
          </div>
          {!activityLoading && !activityError && filteredActivity.length > 0 && activityHasMore && (
            <div className="mt-4 flex justify-center">
              <button
                type="button"
                className={`btn-secondary ${activityLoadingMore ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={handleLoadMoreActivity}
              >
                {activityLoadingMore ? t('wallet.activityLoadingMore') : t('wallet.activityLoadMore')}
              </button>
            </div>
          )}
        </div>
      </div>

      {selectedActivity && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div
            className="absolute inset-0 bg-black/60 backdrop-blur-sm"
            onClick={() => setSelectedActivity(null)}
            aria-hidden="true"
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="wallet-activity-detail-title"
            className="relative z-10 w-full max-w-2xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <div className="flex items-start justify-between gap-3">
              <div>
                <h4 id="wallet-activity-detail-title" className="text-lg font-semibold">
                  {t('wallet.activityDetailTitle')}
                </h4>
                <p className="mt-1 text-sm text-fog/60">{formatActivityType(selectedActivity)}</p>
              </div>
              <button className="btn-secondary" type="button" onClick={() => setSelectedActivity(null)}>
                {t('common.close')}
              </button>
            </div>

            <div className="mt-5 grid gap-3 sm:grid-cols-2">
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailAmount')}</div>
                <div className="mt-1 text-base font-semibold">{formatSats(Number(selectedActivity.amount_sat || 0))} sats</div>
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailStatus')}</div>
                <div className="mt-1 text-base font-semibold">{formatActivityStatus(selectedActivity)}</div>
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailNetwork')}</div>
                <div className="mt-1">{formatActivityNetworkLabel(selectedActivity)}</div>
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailDirection')}</div>
                <div className="mt-1">{formatActivityDirectionLabel(selectedActivity)}</div>
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailOccurredAt')}</div>
                <div className="mt-1">{formatTimestamp(selectedActivity.timestamp)}</div>
              </div>
              {selectedActivity.txid && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailTxid')}</div>
                  <div className="mt-1 break-all font-mono text-xs text-fog/80">{selectedActivity.txid}</div>
                </div>
              )}
              {typeof selectedActivity.confirmations === 'number' && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailConfirmations')}</div>
                  <div className="mt-1">{selectedActivity.confirmations}</div>
                </div>
              )}
              {typeof selectedActivity.block_height === 'number' && selectedActivity.block_height > 0 && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailBlockHeight')}</div>
                  <div className="mt-1">{selectedActivity.block_height}</div>
                </div>
              )}
              {typeof selectedActivity.fee_sat === 'number' && selectedActivity.fee_sat > 0 && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailFee')}</div>
                  <div className="mt-1">{formatSats(selectedActivity.fee_sat)} sats</div>
                </div>
              )}
              {selectedActivity.created_at && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailCreatedAt')}</div>
                  <div className="mt-1">{formatTimestamp(selectedActivity.created_at)}</div>
                </div>
              )}
              {selectedActivity.settled_at && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailSettledAt')}</div>
                  <div className="mt-1">{formatTimestamp(selectedActivity.settled_at)}</div>
                </div>
              )}
              {selectedActivity.memo && String(selectedActivity.type || '').toLowerCase() === 'invoice' && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailMemo')}</div>
                  <div className="mt-1 whitespace-pre-wrap break-words">{selectedActivity.memo}</div>
                </div>
              )}
              {selectedActivity.memo && String(selectedActivity.type || '').toLowerCase() === 'onchain' && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailLabel')}</div>
                  <div className="mt-1 whitespace-pre-wrap break-words">{selectedActivity.memo}</div>
                </div>
              )}
              {Array.isArray(selectedActivity.addresses) && selectedActivity.addresses.length > 0 && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailAddresses')}</div>
                  <div className="mt-1 space-y-2">
                    {selectedActivity.addresses.map((address, idx) => (
                      <div key={`${address}-${idx}`} className="break-all font-mono text-xs text-fog/80">{address}</div>
                    ))}
                  </div>
                </div>
              )}
              {selectedActivity.channel_alias && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailChannel')}</div>
                  <div className="mt-1 break-words">{selectedActivity.channel_alias}</div>
                </div>
              )}
              {selectedActivity.channel_point && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailChannelPoint')}</div>
                  <div className="mt-1 break-all font-mono text-xs text-fog/80">{selectedActivity.channel_point}</div>
                </div>
              )}
              {selectedActivity.payment_hash && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailPaymentHash')}</div>
                  <div className="mt-1 break-all font-mono text-xs text-fog/80">{selectedActivity.payment_hash}</div>
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
