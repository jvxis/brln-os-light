import { useEffect, useState } from 'react'
import { createInvoice, decodeInvoice, getLnChannels, getMempoolFees, getWalletAddress, getWalletSummary, payInvoice, sendOnchain } from '../api'

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
  const [decode, setDecode] = useState<any>(null)
  const [decodeError, setDecodeError] = useState('')
  const [decodeLoading, setDecodeLoading] = useState(false)
  const [status, setStatus] = useState('')
  const [channels, setChannels] = useState<any[]>([])
  const [channelsError, setChannelsError] = useState('')
  const [channelsLoading, setChannelsLoading] = useState(true)
  const [outgoingChannelPoint, setOutgoingChannelPoint] = useState('')

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
        setSendFeeStatus('Fee suggestions unavailable.')
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
        setChannelsError(err?.message || 'Channels unavailable.')
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
    let label = 'Activity'
    if (type === 'invoice') label = 'Invoice'
    else if (type === 'payment') label = 'Payment'
    else if (network === 'onchain') label = direction === 'out' ? 'On-chain send' : 'On-chain deposit'
    else if (type) label = type.charAt(0).toUpperCase() + type.slice(1)
    if (network === 'lightning') return `⚡ ${label}`
    return label
  }

  const orderedActivity = [...activity].sort((a: any, b: any) => {
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
    const peerLabel = alias || (pubkey ? `${pubkey.slice(0, 10)}...` : 'Unknown peer')
    const point = String(ch.channel_point || '').trim()
    const shortPoint = point && point.length > 16 ? `${point.slice(0, 8)}...${point.slice(-4)}` : point
    const localBalance = Number(ch.local_balance_sat || 0)
    return `${peerLabel} | ${shortPoint} | ${localBalance} sats`
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

  const handleToggleSend = () => {
    setSendOpen((prev) => !prev)
    setSendStatus('')
  }

  const handleSendOnchain = async () => {
    const target = sendAddress.trim()
    const amountSat = Number(sendAmount || 0)
    const feeRate = Number(sendFeeRate || 0)
    if (!target) {
      setSendStatus('Destination address required.')
      return
    }
    if (!sendSweepAll && amountSat <= 0) {
      setSendStatus('Amount must be positive.')
      return
    }
    setSendRunning(true)
    setSendStatus('Sending on-chain...')
    try {
      const payload = {
        address: target,
        sat_per_vbyte: feeRate > 0 ? feeRate : undefined,
        ...(sendSweepAll ? { sweep_all: true } : { amount_sat: amountSat })
      }
      const res = await sendOnchain(payload)
      const txid = res?.txid ? ` Txid: ${res.txid}` : ''
      setSendStatus(`On-chain send broadcast.${txid}`)
      setSendAddress('')
      setSendAmount('')
      setSendSweepAll(false)
    } catch (err: any) {
      setSendStatus(err?.message || 'On-chain send failed.')
    } finally {
      setSendRunning(false)
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
      await payInvoice({ payment_request: paymentRequest, channel_point: outgoingChannelPoint || undefined })
      setStatus('Payment sent.')
    } catch (err: any) {
      setStatus(err?.message || 'Payment failed.')
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
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-4 space-y-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <p className="text-fog/60">On-chain</p>
                <p className="text-xl">{onchainBalance} sats</p>
              </div>
              <div className="flex flex-wrap gap-2">
                <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleAddFunds}>
                  Add funds
                </button>
                <button className="btn-secondary text-xs px-3 py-1.5" onClick={handleToggleSend}>
                  {sendOpen ? 'Hide send' : 'Send funds'}
                </button>
              </div>
            </div>
            {showAddress && (
              <div className="rounded-2xl border border-white/10 bg-ink/70 p-3">
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
            {sendOpen && (
              <div className="rounded-2xl border border-white/10 bg-ink/80 p-3 space-y-3">
                <div className="flex items-center justify-between text-xs text-fog/60">
                  <span>Send on-chain</span>
                  <button className="text-fog/50 hover:text-fog" onClick={handleToggleSend}>
                    Close
                  </button>
                </div>
                <input
                  className="input-field"
                  placeholder="Destination address"
                  value={sendAddress}
                  onChange={(e) => setSendAddress(e.target.value)}
                />
                <div className="grid gap-3 lg:grid-cols-2 lg:items-start">
                  <div className="space-y-2 lg:max-w-[360px]">
                    <label className="text-xs text-fog/60">Amount (sats)</label>
                    <input
                      className="input-field"
                      placeholder="Amount (sats)"
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
                      Sweep all
                    </label>
                    {sendSweepAll && (
                      <p className="text-xs text-brass">
                        Warning: This will send all available on-chain funds (minus fees) to the destination address.
                      </p>
                    )}
                  </div>
                  <div className="space-y-2 lg:max-w-[360px]">
                    <label className="text-xs text-fog/60">
                      Fee rate (sat/vB)
                      <span className="ml-2 text-fog/50">
                        Fastest: {sendFeeHint?.fastest ?? '-'} | 1h: {sendFeeHint?.hour ?? '-'}
                      </span>
                    </label>
                    <div className="flex items-center gap-2">
                      <input
                        className="input-field flex-1 min-w-[120px]"
                        placeholder="Auto"
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
                        Use fastest
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
                  {sendRunning ? 'Sending...' : 'Send on-chain'}
                </button>
                {sendStatus && <p className="text-xs text-brass break-words">{sendStatus}</p>}
              </div>
            )}
          </div>
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-4">
            <p className="text-fog/60">Lightning</p>
            <p className="text-xl">{lightningBalance} sats</p>
            <p className="mt-2 text-xs text-fog/50">Use the cards below to receive or send over Lightning.</p>
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
          <div className="space-y-2">
            <label className="text-xs text-fog/60">Outgoing channel (optional)</label>
            <select
              className="input-field"
              value={outgoingChannelPoint}
              onChange={(e) => setOutgoingChannelPoint(e.target.value)}
            >
              <option value="">Automatic (LND)</option>
              {availableChannels.map((ch) => (
                <option key={ch.channel_point} value={ch.channel_point}>
                  {formatChannelLabel(ch)}
                </option>
              ))}
            </select>
            <p className="text-xs text-fog/50">
              Selecting an outgoing channel forces LND to use it. If it fails, remove the selection and try again.
            </p>
            {!channelsLoading && amountForFilter > 0 && availableChannels.length === 0 && (
              <p className="text-xs text-brass">No channels with enough local balance for this amount.</p>
            )}
            {channelsError && <p className="text-xs text-fog/50">{channelsError}</p>}
          </div>
          <button className="btn-primary" onClick={handlePay}>Pay invoice</button>
        </div>
      </div>

      <div className="section-card">
        <h3 className="text-lg font-semibold">Recent activity</h3>
        <div className="mt-4 max-h-[360px] overflow-y-auto pr-2">
          <div className="space-y-2 text-sm">
          {summaryError ? (
            <p className="text-fog/60">Activity unavailable until LND is reachable.</p>
          ) : orderedActivity.length ? orderedActivity.map((item: any, idx: number) => {
            const typeLabel = formatActivityType(item)
            const statusLabel = String(item.status || 'unknown').replace(/_/g, ' ').toUpperCase()
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
                <span className="text-right">{item.amount_sat} sats</span>
              </div>
            )
          }) : (
            <p className="text-fog/60">No recent activity yet.</p>
          )}
          </div>
        </div>
      </div>
    </section>
  )
}
