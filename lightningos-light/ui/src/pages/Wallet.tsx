import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import QrScanner from 'qr-scanner'
import QRCode from 'qrcode'
import { APIError, createInvoice, decodeInvoice, getLnChannels, getMempoolFees, getSpendingGuard, getWalletActivity, getWalletAddress, getWalletPaymentDetail, getWalletSummary, markWalletActivity, payInvoice, payInvoiceMPP, payInvoiceValidatedRoute, previewOnchainSend, previewWalletPayment, reauthAuth, sendOnchain, updateSpendingGuard, type SpendingGuardStatus } from '../api'
import SensitiveActionModal from '../components/SensitiveActionModal'
import StatusBadge from '../components/dashboard/StatusBadge'
import { getLocale } from '../i18n'

const emptySummary = {
  balances: {
    onchain_sat: 0,
    lightning_sat: 0,
    onchain_confirmed_sat: 0,
    onchain_unconfirmed_sat: 0,
    lightning_local_sat: 0,
    lightning_unsettled_local_sat: 0,
    lightning_remote_sat: 0,
    lightning_unsettled_remote_sat: 0,
    lightning_pending_open_local_sat: 0,
    lightning_pending_open_remote_sat: 0,
    lightning_closing_pending_sat: 0,
    lightning_closing_pending_count: 0,
    lightning_coop_closing_sat: 0,
    lightning_coop_closing_count: 0,
    lightning_force_closing_sat: 0,
    lightning_force_closing_count: 0,
    lightning_force_closing_min_blocks_til_maturity: 0,
    lightning_force_closing_max_blocks_til_maturity: 0,
    lightning_waiting_close_sat: 0,
    lightning_waiting_close_count: 0
  },
  activity: [],
  updated_at: ''
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
  destination_classification?: string
  requires_password_confirmation?: boolean
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

type WalletRouteHop = {
  pubkey?: string
  alias?: string
  channel_id?: number
  channel_capacity_sat?: number
  amt_to_forward_sat?: number
  amt_to_forward_msat?: number
  fee_sat?: number
  fee_msat?: number
  expiry?: number
}

type WalletRouteProbe = {
  status?: string
  likely_liquid?: boolean
  failure_code?: string
  failure_source_index?: number
  failure_hop_index?: number
  message?: string
}

type WalletRouteSummary = {
  route_key?: string
  route_token?: string
  total_amt_sat?: number
  total_amt_msat?: number
  total_fees_sat?: number
  total_fees_msat?: number
  total_time_lock?: number
  hop_count?: number
  hops?: WalletRouteHop[]
  probe?: WalletRouteProbe
}

type WalletPaymentProbe = {
  success?: boolean
  fee_sat?: number
  fee_msat?: number
  time_lock_delay?: number
  failure_reason?: string
}

type WalletMPPPlan = {
  available?: boolean
  total_amt_sat?: number
  total_amt_msat?: number
  validated_amt_sat?: number
  validated_amt_msat?: number
  total_fees_sat?: number
  total_fees_msat?: number
  suggested_max_fee_sat?: number
  max_shard_sat?: number
  max_shard_msat?: number
  part_count?: number
  max_parts?: number
  message?: string
  routes?: WalletRouteSummary[]
}

type WalletPaymentRecommendation = {
  type?: string
  reason?: string
  target_channel_id?: number
  target_channel_id_string?: string
  target_channel_selected?: boolean
  target_channel_point?: string
  target_alias?: string
  target_pubkey?: string
  target_local_balance_sat?: number
  estimated_payment_fee_sat?: number
  estimated_payment_fee_msat?: number
  hop_count?: number
  candidate_route_count?: number
  probed_route_count?: number
  probe_status?: string
  probe_failure_code?: string
  message?: string
}

type WalletPaymentPreview = {
  payment_request?: string
  amount_sat?: number
  amount_msat?: number
  memo?: string
  destination?: string
  suggested_max_fee_sat?: number
  suggested_max_fee_msat?: number
  effective_max_fee_sat?: number
  effective_max_fee_msat?: number
  liquidity_validated?: boolean
  validated_route_count?: number
  probe?: WalletPaymentProbe
  routes?: WalletRouteSummary[]
  mpp_plan?: WalletMPPPlan
  recommendation?: WalletPaymentRecommendation
}

type WalletPaymentDetail = {
  payment_hash?: string
  payment_request?: string
  status?: string
  value_sat?: number
  value_msat?: number
  fee_sat?: number
  fee_msat?: number
  created_at?: string
  route?: WalletRouteSummary
}

const walletActivityPageSize = 100

const mempoolTxUrl = (txid?: string) => {
  const trimmed = String(txid || '').trim()
  if (!trimmed) return ''
  return `https://mempool.space/tx/${encodeURIComponent(trimmed)}`
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
  const [spendingGuard, setSpendingGuard] = useState<SpendingGuardStatus | null>(null)
  const [spendingGuardError, setSpendingGuardError] = useState('')
  const [spendingGuardEnabled, setSpendingGuardEnabled] = useState(false)
  const [spendingGuardMaxPayment, setSpendingGuardMaxPayment] = useState('')
  const [spendingGuardRolling, setSpendingGuardRolling] = useState('')
  const [spendingGuardDirty, setSpendingGuardDirty] = useState(false)
  const [spendingGuardSaving, setSpendingGuardSaving] = useState(false)
  const [spendingGuardNotice, setSpendingGuardNotice] = useState('')
  const [spendingGuardReauthOpen, setSpendingGuardReauthOpen] = useState(false)
  const [spendingGuardPassword, setSpendingGuardPassword] = useState('')
  const [spendingGuardExpanded, setSpendingGuardExpanded] = useState(false)
  const spendingGuardDirtyRef = useRef(false)
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
  const [sendConfirmOpen, setSendConfirmOpen] = useState(false)
  const [sendConfirmPassword, setSendConfirmPassword] = useState('')
  const [sendConfirmStatus, setSendConfirmStatus] = useState('')
  const [sendConfirmRunning, setSendConfirmRunning] = useState(false)
  const [paymentReauthAction, setPaymentReauthAction] = useState<'pay' | 'validated' | 'mpp' | null>(null)
  const [paymentReauthPassword, setPaymentReauthPassword] = useState('')
  const [paymentReauthError, setPaymentReauthError] = useState('')
  const [paymentReauthBusy, setPaymentReauthBusy] = useState(false)
  const [amount, setAmount] = useState('')
  const [memo, setMemo] = useState('')
  const [invoiceExpiry, setInvoiceExpiry] = useState('3600')
  const [invoice, setInvoice] = useState('')
  const [invoiceQr, setInvoiceQr] = useState<string | null>(null)
  const [invoiceCopied, setInvoiceCopied] = useState(false)
  const [invoiceNotice, setInvoiceNotice] = useState('')
  const [invoiceBlinded, setInvoiceBlinded] = useState(false)
  const [invoiceIncomingChannelPoint, setInvoiceIncomingChannelPoint] = useState('')
  const [paymentRequest, setPaymentRequest] = useState('')
  const [payAmount, setPayAmount] = useState('')
  const [decode, setDecode] = useState<any>(null)
  const [decodeError, setDecodeError] = useState('')
  const [decodeLoading, setDecodeLoading] = useState(false)
  const [status, setStatus] = useState('')
  const [channels, setChannels] = useState<any[]>([])
  const [channelsError, setChannelsError] = useState('')
  const [channelsLoading, setChannelsLoading] = useState(true)
  const [outgoingChannelPoints, setOutgoingChannelPoints] = useState<string[]>([])
  const [outgoingChannelsExpanded, setOutgoingChannelsExpanded] = useState(false)
  const [payMaxFeeSat, setPayMaxFeeSat] = useState('')
  const [payMaxFeeTouched, setPayMaxFeeTouched] = useState(false)
  const [paymentPreview, setPaymentPreview] = useState<WalletPaymentPreview | null>(null)
  const [paymentPreviewLoading, setPaymentPreviewLoading] = useState(false)
  const [paymentPreviewError, setPaymentPreviewError] = useState('')
  const [activityRange, setActivityRange] = useState<WalletActivityRange>('7d')
  const [activityItems, setActivityItems] = useState<WalletActivityItem[]>([])
  // What the operator classified as revenue or cost of running the node. The
  // technical shape of a payment does not reveal its purpose - an invoice paid
  // might be a coffee or the purchase of a channel - so an unmarked payment
  // stays out of the report entirely.
  const [activityMarks, setActivityMarks] = useState<Record<string, string>>({})
  // Rows the report already counts on its own - a Magma sale invoice, a keysend.
  // The server decides which, because the rule belongs next to the code that
  // counts them, and classifying one by hand would add the same sats twice.
  const [activityAutoCounted, setActivityAutoCounted] = useState<Record<string, string>>({})
  const [markBusy, setMarkBusy] = useState('')
  const [activityError, setActivityError] = useState('')
  const [activityLoading, setActivityLoading] = useState(true)
  const [activityLoadingMore, setActivityLoadingMore] = useState(false)
  const [activityHasMore, setActivityHasMore] = useState(false)
  const [selectedActivity, setSelectedActivity] = useState<WalletActivityItem | null>(null)
  const [selectedPaymentDetail, setSelectedPaymentDetail] = useState<WalletPaymentDetail | null>(null)
  const [selectedPaymentDetailLoading, setSelectedPaymentDetailLoading] = useState(false)
  const [selectedPaymentDetailError, setSelectedPaymentDetailError] = useState('')
  const [mobileQrAvailable, setMobileQrAvailable] = useState<boolean | null>(null)
  const [mobileQrScannerOpen, setMobileQrScannerOpen] = useState(false)
  const [mobileQrScannerStatus, setMobileQrScannerStatus] = useState('')
  const scannerVideoRef = useRef<HTMLVideoElement | null>(null)
  const scannerRef = useRef<QrScanner | null>(null)

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

  const normalizeScannedPaymentValue = (value: string) => {
    const trimmed = String(value || '').trim()
    if (!trimmed) return ''

    try {
      const parsed = new URL(trimmed)
      const lightning = parsed.searchParams.get('lightning') || parsed.searchParams.get('invoice')
      if (lightning) {
        return stripLightningPrefix(lightning)
      }
    } catch {
      // Ignore non-URL payloads and fall back to the raw scan result.
    }

    return stripLightningPrefix(trimmed)
  }

  const stopMobileQrScanner = () => {
    if (!scannerRef.current) return
    scannerRef.current.destroy()
    scannerRef.current = null
  }

  const getQrScannerErrorMessage = (err: unknown) => {
    const message = err instanceof Error ? err.message : String(err || '')
    const normalized = message.toLowerCase()
    if (
      normalized.includes('permission') ||
      normalized.includes('denied') ||
      normalized.includes('notallowed') ||
      normalized.includes('not allowed')
    ) {
      return t('wallet.qrScannerPermissionDenied')
    }
    if (
      normalized.includes('camera') ||
      normalized.includes('device') ||
      normalized.includes('notfound') ||
      normalized.includes('not found')
    ) {
      return t('wallet.qrScannerNoCamera')
    }
    return t('wallet.qrScannerStartFailed')
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

  const applySpendingGuardStatus = (data: SpendingGuardStatus, preserveDraft = false) => {
    setSpendingGuard(data)
    if (!preserveDraft) {
      setSpendingGuardEnabled(Boolean(data.enabled))
      setSpendingGuardMaxPayment(data.max_payment_sat > 0 ? String(data.max_payment_sat) : '')
      setSpendingGuardRolling(data.rolling_24h_limit_sat > 0 ? String(data.rolling_24h_limit_sat) : '')
      setSpendingGuardDirty(false)
      spendingGuardDirtyRef.current = false
    }
  }

  const loadSpendingGuard = async (preserveDraft = false) => {
    try {
      const data = await getSpendingGuard()
      applySpendingGuardStatus(data, preserveDraft)
      setSpendingGuardError('')
    } catch (err: any) {
      setSpendingGuardError(err?.message || t('wallet.spendingGuardUnavailable'))
    }
  }

  useEffect(() => {
    void loadSpendingGuard(false)
    const timer = setInterval(() => void loadSpendingGuard(spendingGuardDirtyRef.current), 30000)
    return () => clearInterval(timer)
  }, [t])

  useEffect(() => {
    spendingGuardDirtyRef.current = spendingGuardDirty
  }, [spendingGuardDirty])

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
      const nextMarks = (res?.marks && typeof res.marks === 'object') ? res.marks as Record<string, string> : {}
      setActivityMarks((prev) => append ? { ...prev, ...nextMarks } : nextMarks)
      const nextAuto = (res?.auto_counted && typeof res.auto_counted === 'object')
        ? res.auto_counted as Record<string, string>
        : {}
      setActivityAutoCounted((prev) => append ? { ...prev, ...nextAuto } : nextAuto)
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
    let mounted = true
    QrScanner.hasCamera()
      .then((available) => {
        if (!mounted) return
        setMobileQrAvailable(available)
      })
      .catch(() => {
        if (!mounted) return
        setMobileQrAvailable(Boolean(globalThis.navigator?.mediaDevices?.getUserMedia))
      })
    return () => {
      mounted = false
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

  useEffect(() => {
    setPaymentPreview(null)
    setPaymentPreviewError('')
    setPayMaxFeeSat('')
    setPayMaxFeeTouched(false)
  }, [paymentRequest, payAmount, outgoingChannelPoints])

  useEffect(() => {
    if (!mobileQrScannerOpen) {
      stopMobileQrScanner()
      return
    }

    const video = scannerVideoRef.current
    if (!video) return

    let active = true
    setMobileQrScannerStatus(t('wallet.qrScannerStarting'))

    const scanner = new QrScanner(
      video,
      (result) => {
        if (!active) return
        const scannedValue = normalizeScannedPaymentValue(result?.data || '')
        if (!scannedValue) return

        stopMobileQrScanner()
        setPaymentRequest(scannedValue)
        setDecodeError('')
        setPaymentPreview(null)
        setPaymentPreviewError('')
        setStatus('')
        setMobileQrScannerStatus('')
        setMobileQrScannerOpen(false)
      },
      {
        preferredCamera: 'environment',
        maxScansPerSecond: 8,
        returnDetailedScanResult: true,
        onDecodeError: () => {
          // Ignore transient decode misses while scanning frames.
        }
      }
    )

    scannerRef.current = scanner

    scanner.start()
      .then(() => {
        if (!active) return
        setMobileQrScannerStatus(t('wallet.qrScannerReady'))
      })
      .catch((err) => {
        if (!active) return
        stopMobileQrScanner()
        setMobileQrScannerStatus(getQrScannerErrorMessage(err))
      })

    return () => {
      active = false
      stopMobileQrScanner()
    }
  }, [mobileQrScannerOpen, t])

  const cleanedPaymentRequest = stripLightningPrefix(paymentRequest)
  const isLnAddress = isLightningAddressInput(cleanedPaymentRequest)
  const payAmountSat = Number(payAmount || 0)
  const onchainBalance = summary?.balances?.onchain_sat ?? 0
  const onchainConfirmedBalance = Number(summary?.balances?.onchain_confirmed_sat ?? onchainBalance)
  const onchainUnconfirmedBalance = Number(summary?.balances?.onchain_unconfirmed_sat ?? 0)
  const lightningBalance = summary?.balances?.lightning_sat ?? 0
  const lightningLocalBalance = Number(summary?.balances?.lightning_local_sat ?? lightningBalance)
  const lightningUnsettledLocalBalance = Number(summary?.balances?.lightning_unsettled_local_sat ?? 0)
  const lightningRemoteBalance = Number(summary?.balances?.lightning_remote_sat ?? 0)
  const lightningUnsettledRemoteBalance = Number(summary?.balances?.lightning_unsettled_remote_sat ?? 0)
  const lightningPendingOpenLocalBalance = Number(summary?.balances?.lightning_pending_open_local_sat ?? 0)
  const lightningPendingOpenRemoteBalance = Number(summary?.balances?.lightning_pending_open_remote_sat ?? 0)
  const lightningClosingPendingBalance = Number(summary?.balances?.lightning_closing_pending_sat ?? 0)
  const lightningForceClosingBalance = Number(summary?.balances?.lightning_force_closing_sat ?? 0)
  const lightningForceClosingCount = Number(summary?.balances?.lightning_force_closing_count ?? 0)
  const lightningForceClosingMinBlocks = Number(summary?.balances?.lightning_force_closing_min_blocks_til_maturity ?? 0)
  const lightningForceClosingMaxBlocks = Number(summary?.balances?.lightning_force_closing_max_blocks_til_maturity ?? 0)
  const lightningOtherClosingBalance = Math.max(0, lightningClosingPendingBalance - lightningForceClosingBalance)
  const lightningTotalBalance = lightningLocalBalance + lightningUnsettledLocalBalance
  const lightningAccountingTotalBalance = lightningTotalBalance + lightningPendingOpenLocalBalance + lightningClosingPendingBalance
  const balanceUpdatedAt = summary?.updated_at ? new Date(summary.updated_at) : null
  const balanceUpdatedLabel = balanceUpdatedAt && !Number.isNaN(balanceUpdatedAt.getTime())
    ? balanceUpdatedAt.toLocaleString(locale, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit'
    })
    : ''
  const activity = activityItems
  const summaryTone = summaryError && summaryError.toLowerCase().includes('timeout')
    ? 'text-brass'
    : 'text-ember'

  useEffect(() => {
    const paymentHash = String(selectedActivity?.payment_hash || '').trim()
    const isOutgoingLightningPayment = String(selectedActivity?.network || '').toLowerCase() === 'lightning'
      && String(selectedActivity?.direction || '').toLowerCase() === 'out'
      && String(selectedActivity?.type || '').toLowerCase() === 'payment'

    if (!paymentHash || !isOutgoingLightningPayment) {
      setSelectedPaymentDetail(null)
      setSelectedPaymentDetailError('')
      setSelectedPaymentDetailLoading(false)
      return
    }

    let cancelled = false
    setSelectedPaymentDetailLoading(true)
    setSelectedPaymentDetail(null)
    setSelectedPaymentDetailError('')
    const loadDetail = async () => {
      try {
        const res = await getWalletPaymentDetail(paymentHash)
        if (cancelled) return
        setSelectedPaymentDetail(res)
      } catch (err: any) {
        if (cancelled) return
        setSelectedPaymentDetail(null)
        setSelectedPaymentDetailError(err?.message || t('wallet.paymentDetailUnavailable'))
      } finally {
        if (cancelled) return
        setSelectedPaymentDetailLoading(false)
      }
    }

    void loadDetail()
    return () => {
      cancelled = true
    }
  }, [selectedActivity, t])

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
  const activeChannels = channels
    .filter((ch) => ch && ch.active && ch.channel_point)
    .sort((a, b) => Number(b.local_balance_sat || 0) - Number(a.local_balance_sat || 0))
  const availableChannels = activeChannels
    .filter((ch) => amountForFilter <= 0 || Number(ch.local_balance_sat || 0) >= amountForFilter)

  const formatChannelLabel = (ch: any) => {
    const alias = String(ch.peer_alias || '').trim()
    const pubkey = String(ch.remote_pubkey || '').trim()
    const peerLabel = alias || (pubkey ? `${pubkey.slice(0, 10)}...` : t('wallet.unknownPeer'))
    const point = String(ch.channel_point || '').trim()
    const shortPoint = point && point.length > 16 ? `${point.slice(0, 8)}...${point.slice(-4)}` : point
    const localBalance = Number(ch.local_balance_sat || 0)
    return `${peerLabel} | ${shortPoint} | ${formatSats(localBalance)} sats`
  }

  const formatRouteHopLabel = (hop: WalletRouteHop) => {
    const alias = String(hop?.alias || '').trim()
    const pubkey = String(hop?.pubkey || '').trim()
    if (alias) return alias
    if (!pubkey) return t('wallet.unknownPeer')
    if (pubkey.length <= 16) return pubkey
    return `${pubkey.slice(0, 12)}...`
  }

  const routeFinalForwardSat = (route?: WalletRouteSummary) => {
    const hops = route?.hops || []
    const finalHop = hops.length > 0 ? hops[hops.length - 1] : undefined
    return Number(finalHop?.amt_to_forward_sat || route?.total_amt_sat || 0)
  }

  const routeProbeLabel = (probe?: WalletRouteProbe) => {
    if (!probe) return t('wallet.paymentPreviewLiquidityUnknown')
    if (probe.likely_liquid || probe.status === 'likely_liquid') return t('wallet.paymentPreviewLiquidityLikely')
    if (probe.status === 'timeout') return t('wallet.paymentPreviewLiquidityTimeout')
    if (probe.status === 'failed' && Number(probe.failure_hop_index || 0) > 0) {
      return t('wallet.paymentPreviewLiquidityFailedAtHop', { index: Number(probe.failure_hop_index || 0) })
    }
    if (probe.status === 'failed') return t('wallet.paymentPreviewLiquidityFailed')
    return t('wallet.paymentPreviewLiquidityUnknown')
  }

  const routeProbeHint = (probe?: WalletRouteProbe) => {
    if (!probe || probe.likely_liquid || probe.status === 'likely_liquid') return ''
    const code = String(probe.failure_code || '').trim()
    switch (code) {
      case 'TEMPORARY_CHANNEL_FAILURE':
        return t('wallet.paymentPreviewProbeTemporaryChannelFailure')
      case 'FEE_INSUFFICIENT':
        return t('wallet.paymentPreviewProbeFeeInsufficient')
      case 'CHANNEL_DISABLED':
        return t('wallet.paymentPreviewProbeChannelDisabled')
      case 'AMOUNT_BELOW_MINIMUM':
        return t('wallet.paymentPreviewProbeAmountBelowMinimum')
      case 'UNKNOWN_NEXT_PEER':
        return t('wallet.paymentPreviewProbeUnknownNextPeer')
      case 'TEMPORARY_NODE_FAILURE':
        return t('wallet.paymentPreviewProbeTemporaryNodeFailure')
      case 'MPP_TIMEOUT':
        return t('wallet.paymentPreviewProbeMppTimeout')
      default:
        return code ? t('wallet.paymentPreviewProbeGenericFailure') : ''
    }
  }

  const routeProbeClassName = (probe?: WalletRouteProbe) => {
    if (probe?.likely_liquid || probe?.status === 'likely_liquid') {
      return 'border-emerald-400/40 bg-emerald-500/15 text-emerald-200'
    }
    if (probe?.status === 'timeout') {
      return 'border-brass/40 bg-brass/10 text-brass'
    }
    if (probe?.status === 'failed') {
      return 'border-ember/40 bg-ember/10 text-ember'
    }
    return 'border-white/10 bg-white/[0.03] text-fog/60'
  }

  const paymentErrorMessage = (err: any, fallback: string) => {
	if (err instanceof APIError && err.code === 'spending_guard_limit_exceeded') {
	  return t('wallet.spendingGuardBlocked')
	}
    const raw = String(err?.message || fallback || '').trim()
    if (!raw) return fallback
    const lower = raw.toLowerCase()
    if (lower.includes('<!doctype') || lower.includes('<html') || lower.includes('<body')) {
      return fallback
    }
    return raw.length > 420 ? `${raw.slice(0, 420)}...` : raw
  }

  const bestValidatedPreviewRoute = () => {
    const routes = paymentPreview?.routes || []
    const validated = routes.filter((route) => route?.probe?.likely_liquid || route?.probe?.status === 'likely_liquid')
    if (validated.length === 0) return null
    return validated.reduce((best, route) => {
      const routeFee = Number(route?.total_fees_msat || 0)
      const bestFee = Number(best?.total_fees_msat || 0)
      if (bestFee <= 0) return route
      if (routeFee <= 0) return best
      return routeFee < bestFee ? route : best
    }, validated[0])
  }

  const paymentPreviewHasLikelyLiquidRoute = Boolean(
    paymentPreview?.liquidity_validated ||
    paymentPreview?.routes?.some((route) => route?.probe?.likely_liquid || route?.probe?.status === 'likely_liquid')
  )
  const paymentPreviewHasMPPPlan = Boolean(paymentPreview?.mpp_plan?.available)
  const paymentPreviewHasValidatedPayment = paymentPreviewHasLikelyLiquidRoute || paymentPreviewHasMPPPlan
  const payInvoiceBlockedByPreview = Boolean(paymentPreview && !paymentPreviewHasLikelyLiquidRoute)
  const paymentBlockedByPreview = Boolean(paymentPreview && !paymentPreviewHasValidatedPayment)

  const outgoingChannelsSummary = () => {
    if (outgoingChannelPoints.length === 0) return t('wallet.automaticLnd')
    if (outgoingChannelPoints.length === 1) {
      const selected = activeChannels.find((ch) => String(ch.channel_point || '').trim() === outgoingChannelPoints[0])
      return selected ? formatChannelLabel(selected) : t('wallet.outgoingChannelsSelected', { count: 1 })
    }
    return t('wallet.outgoingChannelsSelected', { count: outgoingChannelPoints.length })
  }

  useEffect(() => {
    const activePoints = new Set(activeChannels.map((ch) => String(ch.channel_point || '').trim()).filter(Boolean))
    setOutgoingChannelPoints((current) => {
      if (current.length === 0) return current
      const next = current.filter((point) => activePoints.has(point))
      return next.length === current.length ? current : next
    })
  }, [activeChannels])

  useEffect(() => {
    if (!invoiceIncomingChannelPoint) return
    const exists = activeChannels.some((ch) => ch.channel_point === invoiceIncomingChannelPoint)
    if (!exists) {
      setInvoiceIncomingChannelPoint('')
    }
  }, [activeChannels, invoiceIncomingChannelPoint])

  const toggleOutgoingChannelPoint = (channelPoint: string) => {
    const point = String(channelPoint || '').trim()
    if (!point) return
    setOutgoingChannelPoints((current) => (
      current.includes(point)
        ? current.filter((value) => value !== point)
        : [...current, point]
    ))
  }

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
    setSendConfirmOpen(false)
    setSendConfirmPassword('')
    setSendConfirmStatus('')
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

  const executeOnchainSend = async () => {
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
      setSendConfirmOpen(false)
      setSendConfirmPassword('')
      setSendConfirmStatus('')
    } catch (err: any) {
      if (err instanceof APIError && err.code === 'wallet_send_external_reauth_required') {
        setSendConfirmOpen(true)
        setSendConfirmStatus(t('wallet.sendPasswordRequired'))
        return
      }
      setSendTxid('')
      setSendStatus(err?.message || t('wallet.onchainSendFailed'))
    } finally {
      setSendRunning(false)
    }
  }

  const handleSendOnchain = async () => {
    const target = sendAddress.trim()
    const amountSat = Number(sendAmount || 0)
    if (!target) {
      setSendStatus(t('wallet.destinationRequired'))
      return
    }
    if (!sendSweepAll && amountSat <= 0) {
      setSendStatus(t('wallet.amountMustBePositive'))
      return
    }
    if (sendPreview?.requires_password_confirmation) {
      setSendConfirmOpen(true)
      setSendConfirmStatus('')
      return
    }
    await executeOnchainSend()
  }

  const handleConfirmOnchainSend = async () => {
    if (!sendConfirmPassword) {
      setSendConfirmStatus(t('wallet.sendPasswordEntryRequired'))
      return
    }
    setSendConfirmRunning(true)
    setSendConfirmStatus('')
    try {
      await reauthAuth({
        password: sendConfirmPassword,
        scope: 'wallet_send_external'
      })
      await executeOnchainSend()
    } catch (err: any) {
      setSendConfirmStatus(err?.message || t('wallet.sendPasswordConfirmFailed'))
    } finally {
      setSendConfirmRunning(false)
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
        expiry_seconds: expirySeconds,
        blinded: invoiceBlinded || undefined,
        blinded_incoming_channel_point: invoiceBlinded ? invoiceIncomingChannelPoint || undefined : undefined
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

  const handlePreviewPayment = async () => {
    if (!cleanedPaymentRequest) {
      setPaymentPreview(null)
      setPaymentPreviewError(t('wallet.paymentRequestRequired'))
      return
    }
    if (!isLnAddress && decode?.is_blinded) {
      setPaymentPreview(null)
      setPaymentPreviewError(t('wallet.paymentPreviewBlindedUnavailable'))
      return
    }
    if (isLnAddress && payAmountSat <= 0) {
      setPaymentPreview(null)
      setPaymentPreviewError(t('wallet.amountPositiveForLightningAddress'))
      return
    }
    const maxFeeSatValue = Number(payMaxFeeSat || 0)
    if (maxFeeSatValue < 0) {
      setPaymentPreview(null)
      setPaymentPreviewError(t('wallet.maxFeePositive'))
      return
    }

    setPaymentPreviewLoading(true)
    setPaymentPreviewError('')
    try {
      const res = await previewWalletPayment({
        payment_request: cleanedPaymentRequest,
        channel_point: outgoingChannelPoints.length === 1 ? outgoingChannelPoints[0] : undefined,
        channel_points: outgoingChannelPoints.length > 0 ? outgoingChannelPoints : undefined,
        amount_sat: isLnAddress ? payAmountSat : undefined,
        max_fee_sat: maxFeeSatValue > 0 ? maxFeeSatValue : undefined
      })
      setPaymentPreview(res)
      const suggested = Number(res?.suggested_max_fee_sat || 0)
      if (suggested > 0) {
        setPayMaxFeeSat(String(suggested))
        setPayMaxFeeTouched(false)
      }
    } catch (err: any) {
      setPaymentPreview(null)
      setPaymentPreviewError(err?.message || t('wallet.paymentPreviewUnavailable'))
    } finally {
      setPaymentPreviewLoading(false)
    }
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
    const maxFeeSatValue = Number(payMaxFeeSat || 0)
    if (maxFeeSatValue < 0) {
      setStatus(t('wallet.maxFeePositive'))
      return
    }
    if (payInvoiceBlockedByPreview) {
      setStatus(t('wallet.paymentPreviewNoValidatedRoutePayBlocked'))
      return
    }
    setStatus(t('wallet.payingInvoice'))
    try {
      await payInvoice({
        payment_request: cleanedPaymentRequest,
        channel_point: outgoingChannelPoints.length === 1 ? outgoingChannelPoints[0] : undefined,
        channel_points: outgoingChannelPoints.length > 0 ? outgoingChannelPoints : undefined,
        amount_sat: isLnAddress ? payAmountSat : undefined,
        max_fee_sat: maxFeeSatValue > 0 ? maxFeeSatValue : undefined
      })
      setStatus(t('wallet.paymentSent'))
      setPaymentRequest('')
      setPayAmount('')
      setDecode(null)
      setDecodeError('')
      setOutgoingChannelPoints([])
      setOutgoingChannelsExpanded(false)
      setPayMaxFeeSat('')
      setPayMaxFeeTouched(false)
      setPaymentPreview(null)
      setPaymentPreviewError('')
      void getWalletSummary()
        .then((data) => {
          setSummary(data || emptySummary)
          setSummaryWarning(data?.warning || '')
          setSummaryError('')
        })
        .catch(() => {})
      void getLnChannels()
        .then((res: any) => {
          setChannels(Array.isArray(res?.channels) ? res.channels : [])
          setChannelsError('')
        })
        .catch(() => {})
      void loadActivity({
        offset: 0,
        limit: Math.max(activityItems.length, walletActivityPageSize),
        silent: true
      })
    } catch (err: any) {
      if (err instanceof APIError && err.code === 'lightning_funds_reauth_required') {
        setPaymentReauthAction('pay')
        setPaymentReauthError('')
        return
      }
      setStatus(paymentErrorMessage(err, t('wallet.paymentFailed')))
    }
  }

  const handlePayValidatedRoute = async () => {
    if (!cleanedPaymentRequest) {
      setStatus(t('wallet.paymentRequestRequired'))
      return
    }
    if (isLnAddress && payAmountSat <= 0) {
      setStatus(t('wallet.amountPositiveForLightningAddress'))
      return
    }
    if (!paymentPreviewHasLikelyLiquidRoute) {
      setStatus(t('wallet.paymentPreviewNoValidatedRoutePayBlocked'))
      return
    }
    const maxFeeSatValue = Number(payMaxFeeSat || 0)
    if (maxFeeSatValue < 0) {
      setStatus(t('wallet.maxFeePositive'))
      return
    }
    const selectedRoute = bestValidatedPreviewRoute()
    if (!selectedRoute?.route_token) {
      setStatus(t('wallet.paymentPreviewRouteTokenMissing'))
      return
    }
    setStatus(t('wallet.payingValidatedRoute'))
    try {
      await payInvoiceValidatedRoute({
        payment_request: cleanedPaymentRequest,
        channel_point: outgoingChannelPoints.length === 1 ? outgoingChannelPoints[0] : undefined,
        channel_points: outgoingChannelPoints.length > 0 ? outgoingChannelPoints : undefined,
        route_token: selectedRoute.route_token,
        amount_sat: isLnAddress ? payAmountSat : undefined,
        max_fee_sat: maxFeeSatValue > 0 ? maxFeeSatValue : undefined
      })
      setStatus(t('wallet.paymentSent'))
      setPaymentRequest('')
      setPayAmount('')
      setDecode(null)
      setDecodeError('')
      setOutgoingChannelPoints([])
      setOutgoingChannelsExpanded(false)
      setPayMaxFeeSat('')
      setPayMaxFeeTouched(false)
      setPaymentPreview(null)
      setPaymentPreviewError('')
      void getWalletSummary()
        .then((data) => {
          setSummary(data || emptySummary)
          setSummaryWarning(data?.warning || '')
          setSummaryError('')
        })
        .catch(() => {})
      void getLnChannels()
        .then((res: any) => {
          setChannels(Array.isArray(res?.channels) ? res.channels : [])
          setChannelsError('')
        })
        .catch(() => {})
      void loadActivity({
        offset: 0,
        limit: Math.max(activityItems.length, walletActivityPageSize),
        silent: true
      })
    } catch (err: any) {
      if (err instanceof APIError && err.code === 'lightning_funds_reauth_required') {
        setPaymentReauthAction('validated')
        setPaymentReauthError('')
        return
      }
      setStatus(paymentErrorMessage(err, t('wallet.paymentFailed')))
    }
  }

  const handlePayMPP = async () => {
    if (!cleanedPaymentRequest) {
      setStatus(t('wallet.paymentRequestRequired'))
      return
    }
    if (isLnAddress && payAmountSat <= 0) {
      setStatus(t('wallet.amountPositiveForLightningAddress'))
      return
    }
    const plan = paymentPreview?.mpp_plan
    if (!plan?.available) {
      setStatus(t('wallet.paymentPreviewNoValidatedRoutePayBlocked'))
      return
    }
    const maxFeeSatValue = Number(payMaxFeeSat || 0)
    if (maxFeeSatValue < 0) {
      setStatus(t('wallet.maxFeePositive'))
      return
    }
    setStatus(t('wallet.payingMPPPlan'))
    try {
      await payInvoiceMPP({
        payment_request: cleanedPaymentRequest,
        channel_point: outgoingChannelPoints.length === 1 ? outgoingChannelPoints[0] : undefined,
        channel_points: outgoingChannelPoints.length > 0 ? outgoingChannelPoints : undefined,
        amount_sat: isLnAddress ? payAmountSat : undefined,
        max_fee_sat: maxFeeSatValue > 0 ? maxFeeSatValue : undefined,
        max_parts: Number(plan.max_parts || 0) > 0 ? Number(plan.max_parts || 0) : undefined,
        max_shard_sat: Number(plan.max_shard_sat || 0) > 0 ? Number(plan.max_shard_sat || 0) : undefined
      })
      setStatus(t('wallet.paymentSent'))
      setPaymentRequest('')
      setPayAmount('')
      setDecode(null)
      setDecodeError('')
      setOutgoingChannelPoints([])
      setOutgoingChannelsExpanded(false)
      setPayMaxFeeSat('')
      setPayMaxFeeTouched(false)
      setPaymentPreview(null)
      setPaymentPreviewError('')
      void getWalletSummary()
        .then((data) => {
          setSummary(data || emptySummary)
          setSummaryWarning(data?.warning || '')
          setSummaryError('')
        })
        .catch(() => {})
      void getLnChannels()
        .then((res: any) => {
          setChannels(Array.isArray(res?.channels) ? res.channels : [])
          setChannelsError('')
        })
        .catch(() => {})
      void loadActivity({
        offset: 0,
        limit: Math.max(activityItems.length, walletActivityPageSize),
        silent: true
      })
    } catch (err: any) {
      if (err instanceof APIError && err.code === 'lightning_funds_reauth_required') {
        setPaymentReauthAction('mpp')
        setPaymentReauthError('')
        return
      }
      setStatus(paymentErrorMessage(err, t('wallet.paymentFailed')))
    }
  }

  // Only Lightning payments can be classified for now: on-chain transfers and
  // keysend were deliberately left for a later phase, so the marking surface
  // stays small while the reporting side settles.
  const activityIsMarkable = (item: WalletActivityItem) => {
    const hash = String(item?.payment_hash || '').trim()
    if (!hash) return false
    const type = String(item?.type || '').toLowerCase()
    if (!(type.includes('lightning') || type.includes('invoice') || type.includes('payment'))) return false
    // Keysend is classified automatically - received is revenue, sent is cost -
    // precisely because it is unilateral and needs no human judgement. Marking
    // one would add the same sats a second time, on top of the automatic count.
    if (item?.keysend) return false
    // A payment that failed moved no money: there is nothing to classify as
    // revenue or cost, and offering the button invited recording a cost the
    // node never paid. Only settled payments reach the report at all.
    const status = String(item?.status || '').trim().toUpperCase()
    return status === 'SETTLED' || status === 'SUCCEEDED'
  }

  const toggleActivityMark = async (item: WalletActivityItem, classification: string) => {
    const hash = String(item?.payment_hash || '').trim()
    if (!hash) return
    // Clicking the active classification clears it, which is how a mistake is
    // undone without a second control.
    const next = activityMarks[hash] === classification ? '' : classification
    setMarkBusy(hash)
    try {
      await markWalletActivity({
        payment_hash: hash,
        classification: next,
        amount_sat: Math.abs(Number(item.amount_sat || 0)),
        occurred_at: item.timestamp ? new Date(item.timestamp).toISOString() : undefined,
        direction: activityDirection(item)
      })
      setActivityMarks((prev) => {
        const copy = { ...prev }
        if (next) copy[hash] = next
        else delete copy[hash]
        return copy
      })
    } catch (err: any) {
      setActivityError(err?.message || t('wallet.markFailed'))
    } finally {
      setMarkBusy('')
    }
  }

  const handlePaymentReauth = async () => {
    if (!paymentReauthPassword.trim()) {
      setPaymentReauthError(t('wallet.paymentPasswordRequired'))
      return
    }
    const action = paymentReauthAction
    if (!action) return
    setPaymentReauthBusy(true)
    setPaymentReauthError('')
    try {
      await reauthAuth({ password: paymentReauthPassword, scope: 'lightning_funds' })
      setPaymentReauthAction(null)
      setPaymentReauthPassword('')
      if (action === 'validated') await handlePayValidatedRoute()
      else if (action === 'mpp') await handlePayMPP()
      else await handlePay()
    } catch (err: any) {
      setPaymentReauthError(err?.message || t('wallet.paymentPasswordFailed'))
    } finally {
      setPaymentReauthBusy(false)
    }
  }

  const spendingGuardPayload = (confirmPassword?: string) => {
    const maxPayment = spendingGuardMaxPayment.trim() ? Number(spendingGuardMaxPayment) : 0
    const rolling = spendingGuardRolling.trim() ? Number(spendingGuardRolling) : 0
    if (!Number.isSafeInteger(maxPayment) || maxPayment < 0 || !Number.isSafeInteger(rolling) || rolling < 0) {
      throw new Error(t('wallet.spendingGuardInvalidLimits'))
    }
    if (spendingGuardEnabled && maxPayment === 0 && rolling === 0) {
      throw new Error(t('wallet.spendingGuardLimitRequired'))
    }
    return {
      enabled: spendingGuardEnabled,
      max_payment_sat: maxPayment,
      rolling_24h_limit_sat: rolling,
      confirm_password: confirmPassword || undefined
    }
  }

  const saveSpendingGuard = async (confirmPassword?: string) => {
	if (spendingGuardReauthOpen && !String(confirmPassword || '').trim()) {
	  setSpendingGuardNotice(t('wallet.paymentPasswordRequired'))
	  return
	}
    setSpendingGuardSaving(true)
    setSpendingGuardNotice('')
    try {
      const updated = await updateSpendingGuard(spendingGuardPayload(confirmPassword))
      applySpendingGuardStatus(updated)
      setSpendingGuardNotice(t('wallet.spendingGuardSaved'))
      setSpendingGuardReauthOpen(false)
      setSpendingGuardPassword('')
    } catch (err: any) {
      if (err instanceof APIError && err.code === 'lightning_funds_reauth_required') {
        setSpendingGuardReauthOpen(true)
        return
      }
      setSpendingGuardNotice(err?.message || t('wallet.spendingGuardSaveFailed'))
    } finally {
      setSpendingGuardSaving(false)
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

  const openMobileQrScanner = () => {
    setMobileQrScannerStatus('')
    setMobileQrScannerOpen(true)
  }

  const closeMobileQrScanner = () => {
    stopMobileQrScanner()
    setMobileQrScannerStatus('')
    setMobileQrScannerOpen(false)
  }

  const selectedActivityTxUrl = selectedActivity && activityNetwork(selectedActivity) === 'onchain'
    ? mempoolTxUrl(selectedActivity.txid)
    : ''
  const spendingGuardConsumed = Number(spendingGuard?.used_sat || 0) + Number(spendingGuard?.reserved_sat || 0)
  const spendingGuardLimit = Number(spendingGuard?.rolling_24h_limit_sat || 0)
  const spendingGuardProgress = spendingGuardLimit > 0 ? Math.min(100, (spendingGuardConsumed / spendingGuardLimit) * 100) : 0

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
                        {sendPreview.requires_password_confirmation && (
                          <p className="sm:col-span-2 text-brass">
                            {t('wallet.sendPreviewPasswordConfirmation', {
                              classification: sendPreview.destination_classification || t('wallet.destinationExternal')
                            })}
                          </p>
                        )}
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
            <div className="mt-3 flex flex-wrap items-end justify-between gap-x-6 gap-y-3 rounded-xl border border-white/8 bg-white/[0.03] p-3">
              <div>
                <p className="text-[11px] uppercase tracking-wide text-fog/45">{t('wallet.lightningLocalSettled')}</p>
                <p className="mt-1 text-xl">{formatSats(lightningLocalBalance)} sats</p>
              </div>
              <div className="sm:text-right">
                <p className="text-[10px] uppercase tracking-wide text-fog/35">{t('wallet.lightningRemoteInbound')}</p>
                <p className="mt-1 text-sm text-fog/65">{formatSats(lightningRemoteBalance)} sats</p>
              </div>
            </div>
            {lightningUnsettledLocalBalance > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {t('wallet.lightningUnsettledLocal', { amount: formatSats(lightningUnsettledLocalBalance) })}
              </p>
            )}
            {lightningUnsettledRemoteBalance > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {t('wallet.lightningUnsettledRemote', { amount: formatSats(lightningUnsettledRemoteBalance) })}
              </p>
            )}
            {lightningPendingOpenLocalBalance > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {t('wallet.lightningPendingOpenLocal', { amount: formatSats(lightningPendingOpenLocalBalance) })}
              </p>
            )}
            {lightningPendingOpenRemoteBalance > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {t('wallet.lightningPendingOpenRemote', { amount: formatSats(lightningPendingOpenRemoteBalance) })}
              </p>
            )}
            {lightningForceClosingBalance > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {t('wallet.lightningForceClosing', { amount: formatSats(lightningForceClosingBalance) })}
              </p>
            )}
            {lightningForceClosingBalance > 0 && lightningForceClosingMaxBlocks > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {lightningForceClosingMinBlocks > 0 && lightningForceClosingMinBlocks !== lightningForceClosingMaxBlocks
                  ? t('wallet.lightningForceClosingBlocksRange', {
                    channelCount: formatSats(lightningForceClosingCount),
                    min: formatSats(lightningForceClosingMinBlocks),
                    max: formatSats(lightningForceClosingMaxBlocks)
                  })
                  : t('wallet.lightningForceClosingBlocks', {
                    channelCount: formatSats(lightningForceClosingCount),
                    blockCount: formatSats(lightningForceClosingMaxBlocks)
                  })}
              </p>
            )}
            {lightningOtherClosingBalance > 0 && (
              <p className="mt-1 text-xs text-fog/50">
                {t('wallet.lightningClosingPending', { amount: formatSats(lightningOtherClosingBalance) })}
              </p>
            )}
            {(lightningUnsettledLocalBalance > 0 || lightningPendingOpenLocalBalance > 0 || lightningClosingPendingBalance > 0) && (
              <p className="mt-1 text-xs text-fog/50">
                {(lightningPendingOpenLocalBalance > 0 || lightningClosingPendingBalance > 0)
                  ? t('wallet.lightningAccountingTotal', { amount: formatSats(lightningAccountingTotalBalance) })
                  : t('wallet.lightningLocalTotal', { amount: formatSats(lightningTotalBalance) })}
              </p>
            )}
            {balanceUpdatedLabel && (
              <p className="mt-2 text-[11px] text-fog/40">
                {t('wallet.balanceUpdatedAt', { time: balanceUpdatedLabel })}
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
        {status && <p className="mt-4 whitespace-pre-wrap break-words text-sm text-brass">{status}</p>}
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
          <div className="space-y-2 rounded-2xl border border-white/10 bg-ink/40 p-3">
            <label className="flex items-center gap-2 text-xs text-fog/60">
              <input
                type="checkbox"
                checked={invoiceBlinded}
                onChange={(e) => setInvoiceBlinded(e.target.checked)}
              />
              <span>{t('wallet.invoiceBlinded')}</span>
            </label>
            <p className="text-xs text-fog/50">{t('wallet.invoiceBlindedHint')}</p>
            {invoiceBlinded && (
              <div className="space-y-2">
                <label className="text-xs text-fog/60">{t('wallet.blindedIncomingChannel')}</label>
                <select
                  className="input-field"
                  value={invoiceIncomingChannelPoint}
                  onChange={(e) => setInvoiceIncomingChannelPoint(e.target.value)}
                >
                  <option value="">{t('wallet.blindedIncomingAutomatic')}</option>
                  {activeChannels.map((ch) => (
                    <option key={ch.channel_point} value={ch.channel_point}>
                      {formatChannelLabel(ch)}
                    </option>
                  ))}
                </select>
                <p className="text-xs text-fog/50">{t('wallet.blindedIncomingChannelHint')}</p>
                {channelsError && <p className="text-xs text-fog/50">{channelsError}</p>}
              </div>
            )}
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

        <div className={`section-card space-y-4 ${paymentPreviewLoading ? 'route-preview-busy' : ''}`}>
          <h3 className="text-lg font-semibold">{t('wallet.payInvoice')}</h3>
          <div className="relative">
            <textarea
              className="input-field min-h-[140px] pr-20 sm:pr-4"
              placeholder={t('wallet.paymentRequestPlaceholder')}
              value={paymentRequest}
              onChange={(e) => setPaymentRequest(e.target.value)}
            />
            {mobileQrAvailable !== false && (
              <button
                type="button"
                className="absolute right-3 top-3 inline-flex items-center gap-1 rounded-lg border border-white/10 bg-ink/90 px-2 py-1 text-[11px] text-fog/70 transition hover:border-brass/40 hover:text-fog sm:hidden"
                onClick={openMobileQrScanner}
                aria-label={t('wallet.qrScannerAction')}
                title={t('wallet.qrScannerAction')}
              >
                <svg viewBox="0 0 20 20" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
                  <path d="M3 7V4.5A1.5 1.5 0 0 1 4.5 3H7" />
                  <path d="M13 3h2.5A1.5 1.5 0 0 1 17 4.5V7" />
                  <path d="M17 13v2.5A1.5 1.5 0 0 1 15.5 17H13" />
                  <path d="M7 17H4.5A1.5 1.5 0 0 1 3 15.5V13" />
                  <path d="M7 7h2v2H7zM11 7h2v2h-2zM7 11h2v2H7zM11 11h2v2h-2z" />
                </svg>
                <span>QR</span>
              </button>
            )}
          </div>
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
            <div className="space-y-2 rounded-2xl border border-white/10 bg-ink/30 p-3">
              <button
                type="button"
                className={`flex w-full items-center justify-between gap-3 rounded-xl border px-3 py-2 text-left text-sm transition ${
                  outgoingChannelPoints.length === 0 && !outgoingChannelsExpanded
                    ? 'border-brass/40 bg-brass/10 text-fog'
                    : 'border-white/10 text-fog/80 hover:border-white/20 hover:text-fog'
                }`}
                onClick={() => setOutgoingChannelsExpanded((current) => !current)}
              >
                <span className="min-w-0 break-all">{outgoingChannelsSummary()}</span>
                <span className={`shrink-0 text-xs text-fog/55 transition ${outgoingChannelsExpanded ? 'rotate-180' : ''}`}>▼</span>
              </button>
              {outgoingChannelsExpanded && (
                <div className="space-y-2">
                  <button
                    type="button"
                    className={`w-full rounded-xl border px-3 py-2 text-left text-sm transition ${
                      outgoingChannelPoints.length === 0
                        ? 'border-brass/40 bg-brass/10 text-fog'
                        : 'border-white/10 text-fog/70 hover:border-white/20 hover:text-fog'
                    }`}
                    onClick={() => {
                      setOutgoingChannelPoints([])
                      setOutgoingChannelsExpanded(false)
                    }}
                  >
                    {t('wallet.automaticLnd')}
                  </button>
                  <div className="max-h-48 space-y-2 overflow-auto pr-1">
                    {activeChannels.map((ch) => {
                      const point = String(ch.channel_point || '').trim()
                      const checked = outgoingChannelPoints.includes(point)
                      return (
                        <label
                          key={point}
                          className={`flex cursor-pointer items-start gap-3 rounded-xl border px-3 py-2 text-sm transition ${
                            checked
                              ? 'border-brass/40 bg-brass/10 text-fog'
                              : 'border-white/10 bg-white/[0.02] text-fog/80 hover:border-white/20 hover:text-fog'
                          }`}
                        >
                          <input
                            type="checkbox"
                            className="mt-0.5 h-4 w-4 rounded border-white/20 bg-ink/50 text-brass focus:ring-brass"
                            checked={checked}
                            onChange={() => toggleOutgoingChannelPoint(point)}
                          />
                          <span className="break-all">{formatChannelLabel(ch)}</span>
                        </label>
                      )
                    })}
                  </div>
                </div>
              )}
            </div>
            <p className="text-xs text-fog/50">
              {t('wallet.outgoingChannelHint')}
            </p>
            {!channelsLoading && amountForFilter > 0 && availableChannels.length === 0 && activeChannels.length > 0 && (
              <p className="text-xs text-brass">{t('wallet.noChannelsForAmount')}</p>
            )}
            {channelsError && <p className="text-xs text-fog/50">{channelsError}</p>}
          </div>
          <div className="space-y-2">
            <label className="text-xs text-fog/60">{t('wallet.maxFeeSat')}</label>
            <input
              className="input-field"
              placeholder={t('wallet.maxFeeSatPlaceholder')}
              type="number"
              min={0}
              value={payMaxFeeSat}
              onChange={(e) => {
                setPayMaxFeeSat(e.target.value)
                setPayMaxFeeTouched(true)
              }}
            />
            {paymentPreview?.suggested_max_fee_sat ? (
              <p className="text-xs text-fog/50">
                {t('wallet.maxFeeSuggested', { amount: formatSats(Number(paymentPreview.suggested_max_fee_sat || 0)) })}
              </p>
            ) : (
              <p className="text-xs text-fog/50">{t('wallet.maxFeeHint')}</p>
            )}
          </div>
          <div className="flex flex-wrap gap-2">
            <button
              className={`btn-secondary text-xs px-3 py-1.5 disabled:opacity-50 ${paymentPreviewLoading ? 'cursor-wait' : 'disabled:cursor-not-allowed'}`}
              onClick={handlePreviewPayment}
              disabled={paymentPreviewLoading || (!isLnAddress && Boolean(decode?.is_blinded))}
            >
              {paymentPreviewLoading ? t('wallet.paymentPreviewLoading') : t('wallet.paymentPreviewAction')}
            </button>
            <button
              className="btn-primary disabled:cursor-not-allowed disabled:opacity-50"
              onClick={handlePay}
              disabled={payInvoiceBlockedByPreview}
            >
              {t('wallet.payInvoice')}
            </button>
            {paymentPreviewHasLikelyLiquidRoute && (
              <button
                className="btn-secondary text-xs px-3 py-1.5 disabled:cursor-not-allowed disabled:opacity-50"
                onClick={handlePayValidatedRoute}
                disabled={paymentPreviewLoading}
              >
                {t('wallet.payValidatedRoute')}
              </button>
            )}
            {paymentPreviewHasMPPPlan && (
              <button
                className="btn-secondary text-xs px-3 py-1.5 disabled:cursor-not-allowed disabled:opacity-50"
                onClick={handlePayMPP}
                disabled={paymentPreviewLoading}
              >
                {t('wallet.payMPPPlan')}
              </button>
            )}
          </div>
          {paymentBlockedByPreview && (
            <p className="text-xs text-brass">{t('wallet.paymentPreviewNoValidatedRoutePayBlocked')}</p>
          )}
          {!isLnAddress && decode?.is_blinded && (
            <p className="text-xs text-brass">{t('wallet.paymentPreviewBlindedUnavailable')}</p>
          )}
          {paymentPreviewError && (
            <p className="text-xs text-ember">{paymentPreviewError}</p>
          )}
          {paymentPreview && (
            <div className="space-y-3 rounded-2xl border border-white/10 bg-ink/40 p-4">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h4 className="text-sm font-semibold">{t('wallet.paymentPreviewTitle')}</h4>
                {paymentPreview.probe?.success ? (
                  <span className="text-xs text-fog/60">
                    {t('wallet.paymentPreviewProbeFee', { amount: formatSats(Number(paymentPreview.probe?.fee_sat || 0)) })}
                  </span>
                ) : paymentPreview.probe?.failure_reason ? (
                  <span className="text-xs text-brass">{paymentPreview.probe.failure_reason}</span>
                ) : null}
              </div>
              <p className="text-xs leading-relaxed text-fog/55">{t('wallet.paymentPreviewLiquidityNote')}</p>
              {paymentPreviewHasLikelyLiquidRoute ? (
                <p className="text-xs text-emerald-200">{t('wallet.paymentPreviewValidatedRouteReady')}</p>
              ) : paymentPreviewHasMPPPlan ? (
                <p className="text-xs text-emerald-200">{t('wallet.paymentPreviewMPPReady')}</p>
              ) : (
                <p className="text-xs text-brass">{t('wallet.paymentPreviewNoValidatedRoute')}</p>
              )}
              {paymentPreview.mpp_plan && (
                <div className={`rounded-2xl border p-3 ${paymentPreview.mpp_plan.available ? 'border-emerald-400/25 bg-emerald-500/10' : 'border-brass/25 bg-brass/10'}`}>
                  <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                    <span className="font-medium text-fog">{t('wallet.paymentPreviewMPPTitle')}</span>
                    <span className={paymentPreview.mpp_plan.available ? 'text-emerald-200' : 'text-brass'}>
                      {paymentPreview.mpp_plan.available ? t('wallet.paymentPreviewMPPValidated') : t('wallet.paymentPreviewMPPPartial')}
                    </span>
                  </div>
                  <div className="mt-2 grid gap-2 text-xs text-fog/60 sm:grid-cols-2">
                    <span>{t('wallet.paymentPreviewMPPParts', { count: Number(paymentPreview.mpp_plan.part_count || 0), max: Number(paymentPreview.mpp_plan.max_parts || 0) })}</span>
                    <span>{t('wallet.paymentPreviewMPPShard', { amount: formatSats(Number(paymentPreview.mpp_plan.max_shard_sat || 0)) })}</span>
                    <span>{t('wallet.paymentPreviewMPPFee', { amount: formatSats(Number(paymentPreview.mpp_plan.total_fees_sat || 0)) })}</span>
                    <span>{t('wallet.paymentPreviewMPPCoverage', { amount: formatSats(Number(paymentPreview.mpp_plan.validated_amt_sat || 0)), total: formatSats(Number(paymentPreview.mpp_plan.total_amt_sat || 0)) })}</span>
                  </div>
                  {Array.isArray(paymentPreview.mpp_plan.routes) && paymentPreview.mpp_plan.routes.length > 0 && (
                    <div className="mt-3 space-y-2">
                      {paymentPreview.mpp_plan.routes.map((route, routeIndex) => (
                        <div key={`mpp-route-${routeIndex}`} className="rounded-xl border border-white/10 bg-ink/50 px-3 py-2 text-xs">
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <span className="font-medium text-fog">
                              {t('wallet.paymentPreviewMPPPart', { index: routeIndex + 1 })}
                            </span>
                            <span className="text-fog/60">
                              {t('wallet.paymentPreviewMPPPartMeta', {
                                amount: formatSats(routeFinalForwardSat(route)),
                                hops: Number(route?.hop_count || 0),
                                fee: formatSats(Number(route?.total_fees_sat || 0))
                              })}
                            </span>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
              {paymentPreview.recommendation && (() => {
                const recommendation = paymentPreview.recommendation
                const isRebalanceTarget = recommendation.type === 'rebalance_target'
                const isAutomaticValidated = recommendation.type === 'automatic_lnd_validated_route'
                const hasValidatedAutomaticRoute = isRebalanceTarget || isAutomaticValidated
                const channelLabel = recommendation.target_alias ||
                  recommendation.target_channel_point ||
                  recommendation.target_pubkey ||
                  t('wallet.unknownPeer')
                return (
                  <div className={`rounded-2xl border p-3 ${hasValidatedAutomaticRoute ? 'border-sky-400/25 bg-sky-500/10' : 'border-brass/25 bg-brass/10'}`}>
                    <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                      <span className="font-medium text-fog">
                        {hasValidatedAutomaticRoute
                          ? t('wallet.paymentPreviewRebalanceRecommendationTitle')
                          : t('wallet.paymentPreviewAutomaticDiagnosticTitle')}
                      </span>
                      {isRebalanceTarget && (
                        <a className="btn-secondary px-3 py-1 text-[11px]" href="#rebalance-center">
                          {t('wallet.paymentPreviewRebalanceOpen')}
                        </a>
                      )}
                    </div>
                    <p className="mt-2 text-xs leading-relaxed text-fog/65">
                      {isRebalanceTarget
                        ? t('wallet.paymentPreviewRebalanceRecommendation', { channel: channelLabel })
                        : isAutomaticValidated
                          ? t('wallet.paymentPreviewAutomaticSelectedRecommendation', { channel: channelLabel })
                          : t('wallet.paymentPreviewAutomaticNoValidatedRoute')}
                    </p>
                    {hasValidatedAutomaticRoute ? (
                      <div className="mt-3 grid gap-2 text-xs text-fog/60 sm:grid-cols-2">
                        <span>
                          {t('wallet.paymentPreviewRebalanceTarget', {
                            channel: recommendation.target_channel_id_string ||
                              recommendation.target_channel_point ||
                              recommendation.target_channel_id ||
                              '-'
                          })}
                        </span>
                        <span>
                          {t('wallet.paymentPreviewRebalanceFee', {
                            amount: formatSats(Number(recommendation.estimated_payment_fee_sat || 0)),
                            hops: Number(recommendation.hop_count || 0)
                          })}
                        </span>
                        {typeof recommendation.target_local_balance_sat === 'number' && (
                          <span>
                            {t('wallet.paymentPreviewRebalanceLocalBalance', {
                              amount: formatSats(Number(recommendation.target_local_balance_sat || 0))
                            })}
                          </span>
                        )}
                        <span className="break-all">
                          {recommendation.target_channel_point || recommendation.target_pubkey || ''}
                        </span>
                      </div>
                    ) : (
                      <div className="mt-3 grid gap-2 text-xs text-fog/60 sm:grid-cols-2">
                        <span>
                          {t('wallet.paymentPreviewAutomaticProbeStats', {
                            routes: Number(recommendation.candidate_route_count || 0),
                            probes: Number(recommendation.probed_route_count || 0)
                          })}
                        </span>
                        {(recommendation.probe_failure_code || recommendation.probe_status) && (
                          <span>
                            {t('wallet.paymentPreviewAutomaticProbeCode', {
                              code: recommendation.probe_failure_code || recommendation.probe_status || '-'
                            })}
                          </span>
                        )}
                      </div>
                    )}
                  </div>
                )
              })()}
              {Array.isArray(paymentPreview.routes) && paymentPreview.routes.length > 0 ? (
                <div className="space-y-3">
                  {paymentPreview.routes.map((route, routeIndex) => (
                    <div key={`preview-route-${routeIndex}`} className="rounded-2xl border border-white/10 bg-ink/50 p-3">
                      <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="font-medium text-fog">{t('wallet.paymentPreviewRoute', { index: routeIndex + 1 })}</span>
                          <span className={`rounded-full border px-2 py-0.5 ${routeProbeClassName(route?.probe)}`}>
                            {routeProbeLabel(route?.probe)}
                          </span>
                        </div>
                        <span className="text-fog/60">
                          {t('wallet.paymentPreviewRouteMeta', {
                            hops: Number(route?.hop_count || 0),
                            fee: formatSats(Number(route?.total_fees_sat || 0))
                          })}
                        </span>
                      </div>
                      {route?.probe?.failure_code && (
                        <div className="mt-2 text-xs text-fog/55">
                          {t('wallet.paymentPreviewLiquidityCode', { code: route.probe.failure_code })}
                        </div>
                      )}
                      {routeProbeHint(route?.probe) && (
                        <div className="mt-1 text-xs text-fog/55">
                          {routeProbeHint(route?.probe)}
                        </div>
                      )}
                      <div className="mt-3 space-y-2">
                        {(route.hops || []).map((hop, hopIndex) => (
                          <div key={`route-hop-${routeIndex}-${hopIndex}`} className="rounded-xl border border-white/8 bg-white/[0.02] px-3 py-2 text-xs">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                              <span className="font-medium text-fog">
                                {t('wallet.paymentPreviewHop', { index: hopIndex + 1, peer: formatRouteHopLabel(hop) })}
                              </span>
                              {typeof hop?.fee_sat === 'number' && hop.fee_sat > 0 && (
                                <span className="text-fog/60">
                                  {t('wallet.paymentPreviewHopFee', { amount: formatSats(Number(hop.fee_sat || 0)) })}
                                </span>
                              )}
                            </div>
                            <div className="mt-1 break-all text-fog/55">
                              {hop.pubkey || ''}
                              {hop.channel_id ? ` | ${t('wallet.paymentPreviewChannel', { id: hop.channel_id })}` : ''}
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-fog/60">{t('wallet.paymentPreviewNoRoutes')}</p>
              )}
            </div>
          )}
        </div>
      </div>

      <div className="section-card overflow-hidden p-0">
        <button
          type="button"
          className="flex w-full flex-col gap-3 px-5 py-4 text-left transition hover:bg-white/[0.025] sm:flex-row sm:items-center sm:justify-between"
          onClick={() => setSpendingGuardExpanded((current) => !current)}
          aria-expanded={spendingGuardExpanded}
        >
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <h3 className="text-base font-semibold">{t('wallet.spendingGuardTitle')}</h3>
            <StatusBadge
              tone={spendingGuard?.enabled ? 'ok' : 'muted'}
              label={spendingGuard?.enabled ? t('wallet.spendingGuardActive') : t('wallet.spendingGuardDisabled')}
            />
            <span className="hidden text-fog/25 sm:inline" aria-hidden="true">|</span>
            <span className="min-w-0 text-xs text-fog/60">
              {spendingGuard?.enabled
                ? t('wallet.spendingGuardSummaryActive', {
                  perPayment: spendingGuard.max_payment_sat > 0 ? `${formatSats(spendingGuard.max_payment_sat)} sats` : t('wallet.spendingGuardUnlimited'),
                  consumed: `${formatSats(spendingGuardConsumed)} sats`,
                  limit: spendingGuardLimit > 0 ? `${formatSats(spendingGuardLimit)} sats` : t('wallet.spendingGuardUnlimited'),
                  remaining: spendingGuardLimit > 0 ? `${formatSats(spendingGuard.remaining_sat || 0)} sats` : t('wallet.spendingGuardUnlimited')
                })
                : t('wallet.spendingGuardSummaryDisabled')}
            </span>
          </div>
          <span className="flex shrink-0 items-center gap-2 text-xs font-medium text-fog/60">
            {spendingGuardExpanded ? t('wallet.spendingGuardHideSettings') : t('wallet.spendingGuardShowSettings')}
            <span className={`transition-transform ${spendingGuardExpanded ? 'rotate-180' : ''}`} aria-hidden="true">▼</span>
          </span>
        </button>

        {spendingGuardExpanded && (
          <div className="space-y-4 border-t border-white/10 px-5 py-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <p className="max-w-3xl text-sm text-fog/60">{t('wallet.spendingGuardDescription')}</p>
              <label className="flex items-center gap-2 rounded-xl border border-white/10 bg-ink/50 px-3 py-2 text-sm">
                <input
                  type="checkbox"
                  checked={spendingGuardEnabled}
                  onChange={(event) => {
                    setSpendingGuardEnabled(event.target.checked)
                    setSpendingGuardDirty(true)
                    setSpendingGuardNotice('')
                  }}
                />
                {t('wallet.spendingGuardEnable')}
              </label>
            </div>

            {spendingGuard?.enabled && spendingGuardLimit > 0 && (
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4">
                <div className="flex flex-wrap justify-between gap-2 text-sm">
                  <span>{t('wallet.spendingGuardRollingUsage')}</span>
                  <span className="font-mono">{formatSats(spendingGuardConsumed)} / {formatSats(spendingGuardLimit)} sats</span>
                </div>
                <div className="mt-3 h-2 overflow-hidden rounded-full bg-white/10">
                  <div className="h-full rounded-full bg-glow transition-all" style={{ width: `${spendingGuardProgress}%` }} />
                </div>
                <div className="mt-2 flex flex-wrap justify-between gap-2 text-xs text-fog/55">
                  <span>{t('wallet.spendingGuardUsed', { amount: formatSats(Number(spendingGuard.used_sat || 0)) })}</span>
                  <span>{t('wallet.spendingGuardReserved', { amount: formatSats(Number(spendingGuard.reserved_sat || 0)) })}</span>
                  <span>{t('wallet.spendingGuardRemaining', { amount: formatSats(Number(spendingGuard.remaining_sat || 0)) })}</span>
                </div>
              </div>
            )}

            <div className="grid gap-4 md:grid-cols-2">
              <label className="space-y-2 text-sm">
                <span className="text-fog/70">{t('wallet.spendingGuardPerPayment')}</span>
                <input
                  className="input-field"
                  type="number"
                  min={0}
                  step={1}
                  value={spendingGuardMaxPayment}
                  onChange={(event) => {
                    setSpendingGuardMaxPayment(event.target.value)
                    setSpendingGuardDirty(true)
                    setSpendingGuardNotice('')
                  }}
                  placeholder={t('wallet.spendingGuardUnlimited')}
                />
                <span className="block text-xs text-fog/50">{t('wallet.spendingGuardPerPaymentHint')}</span>
              </label>
              <label className="space-y-2 text-sm">
                <span className="text-fog/70">{t('wallet.spendingGuardRollingLimit')}</span>
                <input
                  className="input-field"
                  type="number"
                  min={0}
                  step={1}
                  value={spendingGuardRolling}
                  onChange={(event) => {
                    setSpendingGuardRolling(event.target.value)
                    setSpendingGuardDirty(true)
                    setSpendingGuardNotice('')
                  }}
                  placeholder={t('wallet.spendingGuardUnlimited')}
                />
                <span className="block text-xs text-fog/50">{t('wallet.spendingGuardRollingHint')}</span>
              </label>
            </div>

            <div className="rounded-2xl border border-brass/25 bg-brass/5 p-4 text-xs leading-relaxed text-fog/65">
              <p>{t('wallet.spendingGuardScope')}</p>
              <p className="mt-2 text-brass">{t('wallet.spendingGuardExclusions')}</p>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <button className="btn-primary" type="button" disabled={spendingGuardSaving || !spendingGuardDirty} onClick={() => void saveSpendingGuard()}>
                {spendingGuardSaving ? t('common.saving') : t('wallet.spendingGuardSave')}
              </button>
              {spendingGuardNotice && <p className="text-sm text-brass">{spendingGuardNotice}</p>}
              {spendingGuardError && <p className="text-sm text-ember">{spendingGuardError}</p>}
            </div>
          </div>
        )}
      </div>

      {mobileQrScannerOpen && (
        <div className="fixed inset-0 z-50 bg-ink/95 px-4 py-5 sm:hidden">
          <div className="mx-auto flex h-full w-full max-w-md flex-col gap-4">
            <div className="flex items-start justify-between gap-3">
              <div>
                <h4 className="text-base font-semibold text-fog">{t('wallet.qrScannerTitle')}</h4>
                <p className="mt-1 text-xs leading-relaxed text-fog/60">{t('wallet.qrScannerHint')}</p>
              </div>
              <button
                type="button"
                className="rounded-lg border border-white/10 px-3 py-1.5 text-xs text-fog/70 transition hover:border-white/20 hover:text-fog"
                onClick={closeMobileQrScanner}
              >
                {t('common.close')}
              </button>
            </div>
            <div className="relative overflow-hidden rounded-3xl border border-white/10 bg-black">
              <video
                ref={scannerVideoRef}
                className="aspect-square w-full object-cover"
                muted
                playsInline
              />
              <div className="pointer-events-none absolute inset-[14%] rounded-[28px] border border-brass/70 shadow-[0_0_0_9999px_rgba(5,10,18,0.42)]" />
            </div>
            <p className={`text-xs ${mobileQrScannerStatus === t('wallet.qrScannerReady') ? 'text-emerald-200' : 'text-fog/65'}`}>
              {mobileQrScannerStatus || t('wallet.qrScannerStarting')}
            </p>
          </div>
        </div>
      )}

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
            const markHash = String(item.payment_hash || '').trim()
            const mark = activityMarks[markHash]
            const autoCounted = Boolean(activityAutoCounted[markHash])
            // The row itself is a button, so the classification controls cannot
            // live inside it - a button inside a button is invalid and would
            // swallow the click that opens the detail. They sit beside it, and
            // the border moves to the wrapper so the row still reads as one line.
            const rowBody = clickable ? (
              <button
                type="button"
                onClick={() => setSelectedActivity(item)}
                className="grid w-full items-center gap-3 rounded-2xl text-left transition hover:bg-white/5 focus:outline-none focus:ring-2 focus:ring-white/20 sm:grid-cols-[160px_1fr_auto_auto]"
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
              <div className="grid w-full items-center gap-3 sm:grid-cols-[160px_1fr_auto_auto]">
                <span className="text-xs text-fog/50">{formatTimestamp(item.timestamp)}</span>
                <div className="min-w-0">
                  <span className="text-fog/70">{typeLabel}</span>
                  <span className="text-fog/50"> - {statusLabel}{memoLabel}{channelLabel}</span>
                </div>
                <span className={`text-xs font-mono ${arrowTone}`}>{arrow}</span>
                <span className="text-right">{formatSats(Number(item.amount_sat || 0))} sats</span>
              </div>
            )
            return (
              <div key={itemKey} className="flex items-center gap-2 border-b border-white/10 pb-2">
                <div className="min-w-0 flex-1">{rowBody}</div>
                {/* The direction decides the only classification that can make
                    sense: money that arrived is revenue or nothing, money that
                    left is cost or nothing. Offering both turned an obvious
                    choice into a way to record something impossible. */}
                {/* A row the report counts on its own offers no button - marking it
                    would double the sats. A mark made before that was true still
                    shows, so it can be cleared rather than stranded. */}
                {activityIsMarkable(item) && (!autoCounted || mark) && (() => {
                  const kind = direction === 'in' ? 'revenue' : direction === 'out' ? 'cost' : ''
                  if (!kind) return null
                  const active = mark === kind
                  return (
                    <button
                      type="button"
                      disabled={markBusy === markHash}
                      title={t('wallet.markHint')}
                      onClick={() => void toggleActivityMark(item, kind)}
                      className={`shrink-0 rounded-full border px-2 py-0.5 text-[10px] transition ${
                        active
                          ? kind === 'revenue'
                            ? 'border-emerald-300/70 bg-emerald-500/20 text-emerald-100'
                            : 'border-rose-300/70 bg-rose-500/20 text-rose-100'
                          : 'border-white/10 text-fog/40 hover:border-white/25 hover:text-fog/70'
                      }`}
                    >
                      {kind === 'revenue' ? t('wallet.markRevenue') : t('wallet.markCost')}
                    </button>
                  )
                })()}
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

      {sendConfirmOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div
            className="absolute inset-0 bg-black/60 backdrop-blur-sm"
            onClick={() => {
              if (sendConfirmRunning || sendRunning) return
              setSendConfirmOpen(false)
            }}
            aria-hidden="true"
          />
          <div className="relative z-10 w-full max-w-md rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel">
            <div className="flex items-start justify-between gap-3">
              <div>
                <h4 className="text-lg font-semibold">{t('wallet.sendPasswordModalTitle')}</h4>
                <p className="mt-1 text-sm text-fog/60">{t('wallet.sendPasswordModalBody')}</p>
              </div>
              <button
                className="btn-secondary"
                type="button"
                onClick={() => setSendConfirmOpen(false)}
                disabled={sendConfirmRunning || sendRunning}
              >
                {t('common.close')}
              </button>
            </div>

            <div className="mt-5 space-y-3">
              <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 text-sm text-fog/75">
                <p>{t('wallet.destinationAddress')}</p>
                <p className="mt-2 break-all font-mono text-xs text-fog/80">{sendAddress.trim()}</p>
              </div>
              <div className="space-y-2">
                <label className="text-xs uppercase tracking-[0.2em] text-fog/55">{t('wallet.passwordConfirmationLabel')}</label>
                <input
                  className="input-field"
                  type="password"
                  value={sendConfirmPassword}
                  onChange={(event) => setSendConfirmPassword(event.target.value)}
                  placeholder={t('wallet.passwordConfirmationPlaceholder')}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') {
                      void handleConfirmOnchainSend()
                    }
                  }}
                />
              </div>
              {sendConfirmStatus && (
                <p className="text-sm text-brass">{sendConfirmStatus}</p>
              )}
              <button
                className="btn-primary w-full justify-center"
                type="button"
                onClick={handleConfirmOnchainSend}
                disabled={sendConfirmRunning || sendRunning}
              >
                {sendConfirmRunning || sendRunning ? t('wallet.sendPasswordConfirmRunning') : t('wallet.sendPasswordConfirmAction')}
              </button>
            </div>
          </div>
        </div>
      )}

      <SensitiveActionModal
        open={paymentReauthAction !== null}
        title={t('wallet.paymentPasswordTitle')}
        description={t('wallet.paymentPasswordBody')}
        password={paymentReauthPassword}
        busy={paymentReauthBusy}
        error={paymentReauthError}
        confirmLabel={t('wallet.paymentPasswordAction')}
        onPasswordChange={setPaymentReauthPassword}
        onConfirm={handlePaymentReauth}
        onClose={() => {
          if (paymentReauthBusy) return
          setPaymentReauthAction(null)
          setPaymentReauthPassword('')
          setPaymentReauthError('')
        }}
      />

      <SensitiveActionModal
        open={spendingGuardReauthOpen}
        title={t('wallet.spendingGuardReauthTitle')}
        description={t('wallet.spendingGuardReauthBody')}
        password={spendingGuardPassword}
        busy={spendingGuardSaving}
        error={spendingGuardNotice}
        confirmLabel={t('wallet.spendingGuardReauthAction')}
        onPasswordChange={setSpendingGuardPassword}
        onConfirm={() => void saveSpendingGuard(spendingGuardPassword)}
        onClose={() => {
          if (spendingGuardSaving) return
          setSpendingGuardReauthOpen(false)
          setSpendingGuardPassword('')
          setSpendingGuardNotice('')
        }}
      />

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
            className="relative z-10 flex max-h-[calc(100vh-2rem)] w-full max-w-2xl flex-col overflow-hidden rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
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

            <div className="mt-5 grid min-h-0 gap-3 overflow-y-auto pr-2 sm:grid-cols-2">
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
                  {selectedActivityTxUrl ? (
                    <a
                      className="mt-1 block break-all font-mono text-xs text-emerald-200 underline-offset-2 hover:text-emerald-100 hover:underline"
                      href={selectedActivityTxUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      title={t('wallet.activityDetailOpenTx')}
                    >
                      {selectedActivity.txid}
                    </a>
                  ) : (
                    <div className="mt-1 break-all font-mono text-xs text-fog/80">{selectedActivity.txid}</div>
                  )}
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
                  <div className="mt-1 max-h-48 space-y-2 overflow-y-auto overscroll-contain pr-2 [scrollbar-gutter:stable]">
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
              {selectedPaymentDetailLoading && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailRoute')}</div>
                  <div className="mt-1 text-sm text-fog/60">{t('wallet.paymentDetailLoading')}</div>
                </div>
              )}
              {selectedPaymentDetailError && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailRoute')}</div>
                  <div className="mt-1 text-sm text-ember">{selectedPaymentDetailError}</div>
                </div>
              )}
              {selectedPaymentDetail?.route && !selectedPaymentDetailLoading && !selectedPaymentDetailError && (
                <div className="rounded-2xl border border-white/10 bg-ink/50 p-4 sm:col-span-2">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="text-xs uppercase tracking-wide text-fog/50">{t('wallet.activityDetailRoute')}</div>
                    <div className="text-xs text-fog/60">
                      {t('wallet.activityDetailRouteMeta', {
                        hops: Number(selectedPaymentDetail.route?.hop_count || 0),
                        fee: formatSats(Number(selectedPaymentDetail.route?.total_fees_sat || selectedPaymentDetail.fee_sat || 0))
                      })}
                    </div>
                  </div>
                  <div className="mt-3 space-y-2">
                    {(selectedPaymentDetail.route.hops || []).map((hop, hopIndex) => (
                      <div key={`detail-hop-${hopIndex}`} className="rounded-xl border border-white/8 bg-white/[0.02] px-3 py-2 text-xs">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <span className="font-medium text-fog">
                            {t('wallet.paymentPreviewHop', { index: hopIndex + 1, peer: formatRouteHopLabel(hop) })}
                          </span>
                          {typeof hop?.fee_sat === 'number' && hop.fee_sat > 0 && (
                            <span className="text-fog/60">
                              {t('wallet.paymentPreviewHopFee', { amount: formatSats(Number(hop.fee_sat || 0)) })}
                            </span>
                          )}
                        </div>
                        <div className="mt-1 break-all text-fog/55">
                          {hop.pubkey || ''}
                          {hop.channel_id ? ` | ${t('wallet.paymentPreviewChannel', { id: hop.channel_id })}` : ''}
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
