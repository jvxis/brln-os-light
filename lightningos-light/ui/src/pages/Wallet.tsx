import { useEffect, useState } from 'react'
import { createInvoice, decodeInvoice, getWalletAddress, getWalletSummary, payInvoice } from '../api'

const emptySummary = {
  balances: {
    onchain_sat: 0,
    lightning_sat: 0
  },
  activity: []
}

export default function Wallet() {
  const [summary, setSummary] = useState<any>(emptySummary)
  const [summaryError, setSummaryError] = useState('')
  const [summaryWarning, setSummaryWarning] = useState('')
  const [summaryLoading, setSummaryLoading] = useState(true)
  const [address, setAddress] = useState('')
  const [addressStatus, setAddressStatus] = useState('')
  const [addressLoading, setAddressLoading] = useState(false)
  const [showAddress, setShowAddress] = useState(false)
  const [copied, setCopied] = useState(false)
  const [amount, setAmount] = useState('')
  const [memo, setMemo] = useState('')
  const [invoice, setInvoice] = useState('')
  const [invoiceCopied, setInvoiceCopied] = useState(false)
  const [invoiceNotice, setInvoiceNotice] = useState('')
  const [paymentRequest, setPaymentRequest] = useState('')
  const [decode, setDecode] = useState<any>(null)
  const [decodeError, setDecodeError] = useState('')
  const [decodeLoading, setDecodeLoading] = useState(false)
  const [status, setStatus] = useState('')

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
        const message = err?.message || 'Wallet summary unavailable'
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
    const trimmed = paymentRequest.trim()
    if (!trimmed) {
      setDecode(null)
      setDecodeError('')
      setDecodeLoading(false)
      return
    }

    setDecodeLoading(true)
    const timer = setTimeout(async () => {
      try {
        const res = await decodeInvoice({ payment_request: trimmed })
        setDecode(res)
        setDecodeError('')
      } catch (err: any) {
        setDecode(null)
        setDecodeError(err?.message || 'Invalid invoice')
      } finally {
        setDecodeLoading(false)
      }
    }, 400)

    return () => clearTimeout(timer)
  }, [paymentRequest])

  const onchainBalance = summary?.balances?.onchain_sat ?? 0
  const lightningBalance = summary?.balances?.lightning_sat ?? 0
  const activity = summary?.activity ?? []
  const summaryTone = summaryError && summaryError.toLowerCase().includes('timeout')
    ? 'text-brass'
    : 'text-ember'

  const formatTimestamp = (value: any) => {
    if (!value) return 'Unknown time'
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return 'Unknown time'
    return parsed.toLocaleString('en-US', {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    })
  }

  const formatActivityType = (value: string) => {
    if (!value) return 'Activity'
    if (value.toLowerCase() === 'invoice') return 'Invoice'
    if (value.toLowerCase() === 'payment') return 'Payment'
    return value
  }

  const orderedActivity = [...activity].sort((a: any, b: any) => {
    const timeA = new Date(a?.timestamp || 0).getTime()
    const timeB = new Date(b?.timestamp || 0).getTime()
    return timeB - timeA
  })

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
        setAddressStatus('Address unavailable.')
      }
    } catch (err: any) {
      setAddressStatus(err?.message || 'Address fetch failed.')
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
      setAddressStatus('Copy failed. Select and copy manually.')
    }
  }

  const handleInvoice = async () => {
    setStatus('Creating invoice...')
    setInvoiceNotice('')
    setInvoiceCopied(false)
    try {
      const res = await createInvoice({ amount_sat: Number(amount), memo })
      setInvoice(res.payment_request)
      setStatus('Invoice ready.')
    } catch {
      setStatus('Invoice failed.')
    }
  }

  const handleCopyInvoice = async () => {
    if (!invoice) return
    try {
      await navigator.clipboard.writeText(invoice)
      setInvoiceCopied(true)
    } catch {
      setInvoiceNotice('Copy failed. Select and copy manually.')
    }
  }

  const handleClearInvoice = () => {
    setInvoice('')
    setInvoiceCopied(false)
    setInvoiceNotice('')
  }

  const handlePay = async () => {
    setStatus('Paying invoice...')
    try {
      await payInvoice({ payment_request: paymentRequest })
      setStatus('Payment sent.')
    } catch {
      setStatus('Payment failed.')
    }
  }

  const decodedAmount = () => {
    if (!decode) return ''
    const amountSat = Number(decode.amount_sat || 0)
    const amountMsat = Number(decode.amount_msat || 0)
    if (amountSat > 0) return `${amountSat} sats`
    if (amountMsat > 0) return `${(amountMsat / 1000).toFixed(3)} sats`
    return 'Amountless'
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">Wallet</h2>
        <p className="text-fog/60">Manage Lightning and on-chain balances.</p>
        <div className="mt-4 grid gap-4 lg:grid-cols-2 text-sm">
          <div>
            <div className="flex items-center justify-between gap-3">
              <div>
                <p className="text-fog/60">On-chain</p>
                <p className="text-xl">{onchainBalance} sats</p>
              </div>
              <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleAddFunds}>
                Add funds
              </button>
            </div>
            {showAddress && (
              <div className="mt-3 rounded-2xl border border-white/10 bg-ink/60 p-3">
                <div className="flex items-center justify-between text-xs text-fog/60">
                  <span>On-chain deposit address (SegWit)</span>
                  <button className="text-fog/50 hover:text-fog" onClick={() => setShowAddress(false)}>
                    Close
                  </button>
                </div>
                {addressLoading && (
                  <p className="mt-2 text-xs text-fog/60">Generating address...</p>
                )}
                {!addressLoading && address && (
                  <>
                    <p className="mt-2 text-xs font-mono break-all">{address}</p>
                    <div className="mt-2 flex items-center gap-2">
                      <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleCopy}>
                        {copied ? 'Copied' : 'Copy address'}
                      </button>
                    </div>
                  </>
                )}
                {!addressLoading && !address && addressStatus && (
                  <p className="mt-2 text-xs text-ember">{addressStatus}</p>
                )}
              </div>
            )}
          </div>
          <div>
            <p className="text-fog/60">Lightning</p>
            <p className="text-xl">{lightningBalance} sats</p>
          </div>
        </div>
        {summaryLoading && !summaryError && (
          <p className="mt-4 text-sm text-fog/60">Fetching wallet balances...</p>
        )}
        {summaryWarning && !summaryError && (
          <p className="mt-4 text-sm text-brass">{summaryWarning}</p>
        )}
        {summaryError && (
          <p className={`mt-4 text-sm ${summaryTone}`}>Wallet status: {summaryError}</p>
        )}
        {status && <p className="mt-4 text-sm text-brass">{status}</p>}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">Create invoice</h3>
          <input className="input-field" placeholder="Amount (sats)" value={amount} onChange={(e) => setAmount(e.target.value)} />
          <input className="input-field" placeholder="Memo" value={memo} onChange={(e) => setMemo(e.target.value)} />
          <button className="btn-primary" onClick={handleInvoice}>Generate invoice</button>
          {invoice && (
            <div className="rounded-2xl border border-white/10 bg-ink/60 p-3">
              <div className="flex items-center justify-between text-xs text-fog/60">
                <span>Invoice (Lightning)</span>
                <button className="text-fog/50 hover:text-fog" onClick={handleClearInvoice}>
                  Close
                </button>
              </div>
              <p className="mt-2 text-xs font-mono break-all">{invoice}</p>
              <div className="mt-2 flex items-center gap-2">
                <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleCopyInvoice}>
                  {invoiceCopied ? 'Copied' : 'Copy invoice'}
                </button>
              </div>
              {invoiceNotice && (
                <p className="mt-2 text-xs text-ember">{invoiceNotice}</p>
              )}
            </div>
          )}
        </div>

        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">Pay invoice</h3>
          <textarea className="input-field min-h-[140px]" placeholder="Paste payment request" value={paymentRequest} onChange={(e) => setPaymentRequest(e.target.value)} />
          {decodeLoading && (
            <p className="text-xs text-fog/60">Decoding invoice...</p>
          )}
          {!decodeLoading && decodeError && (
            <p className="text-xs text-ember">{decodeError}</p>
          )}
          {!decodeLoading && !decodeError && decode && (
            <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 text-xs">
              <div className="flex items-center justify-between">
                <span className="text-fog/60">Amount</span>
                <span>{decodedAmount()}</span>
              </div>
              <div className="mt-2 flex items-center justify-between">
                <span className="text-fog/60">Memo</span>
                <span className="max-w-[220px] truncate text-right">{decode.memo || 'No memo'}</span>
              </div>
            </div>
          )}
          <button className="btn-primary" onClick={handlePay}>Pay invoice</button>
        </div>
      </div>

      <div className="section-card">
        <h3 className="text-lg font-semibold">Recent activity</h3>
        <div className="mt-4 space-y-2 text-sm">
          {summaryError ? (
            <p className="text-fog/60">Activity unavailable until LND is reachable.</p>
          ) : orderedActivity.length ? orderedActivity.map((item: any, idx: number) => {
            const typeLabel = formatActivityType(item.type)
            const statusLabel = String(item.status || 'unknown').replace(/_/g, ' ').toUpperCase()
            const isInbound = String(item.type || '').toLowerCase() === 'invoice'
            const arrow = isInbound ? '<-' : '->'
            const arrowTone = isInbound ? 'text-glow' : 'text-ember'
            return (
              <div key={`${item.type}-${idx}`} className="grid items-center gap-3 border-b border-white/10 pb-2 sm:grid-cols-[160px_1fr_auto_auto]">
                <span className="text-xs text-fog/50">{formatTimestamp(item.timestamp)}</span>
                <div className="min-w-0">
                  <span className="text-fog/70">{typeLabel}</span>
                  <span className="text-fog/50"> - {statusLabel}</span>
                </div>
                <span className={`text-xs font-mono ${arrowTone}`}>{arrow}</span>
                <span className="text-right">{item.amount_sat} sats</span>
              </div>
            )
          }) : (
            <p className="text-fog/60">No recent activity yet.</p>
          )}
        </div>
      </div>
    </section>
  )
}
