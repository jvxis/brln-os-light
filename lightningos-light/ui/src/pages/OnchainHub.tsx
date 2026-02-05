import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getMempoolFees, getOnchainTransactions, getOnchainUtxos, getWalletSummary } from '../api'
import { getLocale } from '../i18n'
import clsx from '../utils/clsx'

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

type OnchainUtxo = {
  outpoint: string
  txid: string
  vout: number
  address: string
  address_type: string
  amount_sat: number
  confirmations: number
  pk_script?: string
}

type OnchainTx = {
  txid: string
  direction: 'in' | 'out'
  amount_sat: number
  fee_sat: number
  confirmations: number
  block_height: number
  timestamp: string
  label?: string
  addresses?: string[]
}

type MempoolFeeHint = {
  fastest?: number
  hour?: number
}

const explorerBase = 'https://mempool.space'

export default function OnchainHub() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const satFormatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 })
  const [summary, setSummary] = useState<any>(emptySummary)
  const [summaryError, setSummaryError] = useState('')
  const [summaryLoading, setSummaryLoading] = useState(true)
  const [utxos, setUtxos] = useState<OnchainUtxo[]>([])
  const [utxoError, setUtxoError] = useState('')
  const [utxoLoading, setUtxoLoading] = useState(true)
  const [txs, setTxs] = useState<OnchainTx[]>([])
  const [txError, setTxError] = useState('')
  const [txLoading, setTxLoading] = useState(true)
  const [fees, setFees] = useState<MempoolFeeHint | null>(null)
  const [feesStatus, setFeesStatus] = useState('')
  const [activePane, setActivePane] = useState<'utxos' | 'txs'>('txs')
  const mountedRef = useRef(true)

  const [utxoQuery, setUtxoQuery] = useState('')
  const [utxoStatus, setUtxoStatus] = useState<'all' | 'confirmed' | 'unconfirmed'>('all')
  const [utxoMinAmount, setUtxoMinAmount] = useState('')
  const [utxoMaxAmount, setUtxoMaxAmount] = useState('')
  const [utxoSortBy, setUtxoSortBy] = useState<'amount' | 'confirmations' | 'address'>('amount')
  const [utxoSortDir, setUtxoSortDir] = useState<'desc' | 'asc'>('desc')

  const [txQuery, setTxQuery] = useState('')
  const [txDirection, setTxDirection] = useState<'all' | 'in' | 'out'>('all')
  const [txStatus, setTxStatus] = useState<'all' | 'confirmed' | 'pending'>('all')
  const [txSortBy, setTxSortBy] = useState<'time' | 'amount' | 'confirmations' | 'fee'>('time')
  const [txSortDir, setTxSortDir] = useState<'desc' | 'asc'>('desc')

  const formatSats = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return satFormatter.format(value)
  }

  const formatTimestamp = (value?: string) => {
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

  const toNumber = (value: string) => {
    const cleaned = value.replace(/[^\d]/g, '')
    if (!cleaned) return null
    const parsed = Number(cleaned)
    return Number.isFinite(parsed) ? parsed : null
  }

  const matchesQuery = (value: string, query: string) => value.toLowerCase().includes(query)

  const copyToClipboard = async (value: string) => {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      // ignore
    }
  }

  const loadSummary = async () => {
    setSummaryError('')
    try {
      const data = await getWalletSummary()
      if (!mountedRef.current) return
      setSummary(data || emptySummary)
    } catch (err: any) {
      if (!mountedRef.current) return
      setSummaryError(err?.message || t('wallet.summaryUnavailable'))
    } finally {
      if (!mountedRef.current) return
      setSummaryLoading(false)
    }
  }

  const loadUtxos = async () => {
    setUtxoError('')
    try {
      const res: any = await getOnchainUtxos({ limit: 800 })
      if (!mountedRef.current) return
      setUtxos(Array.isArray(res?.items) ? res.items : [])
    } catch (err: any) {
      if (!mountedRef.current) return
      setUtxoError(err?.message || t('onchainHub.utxosUnavailable'))
    } finally {
      if (!mountedRef.current) return
      setUtxoLoading(false)
    }
  }

  const loadTxs = async () => {
    setTxError('')
    try {
      const res: any = await getOnchainTransactions()
      if (!mountedRef.current) return
      setTxs(Array.isArray(res?.items) ? res.items : [])
    } catch (err: any) {
      if (!mountedRef.current) return
      setTxError(err?.message || t('onchainHub.transactionsUnavailable'))
    } finally {
      if (!mountedRef.current) return
      setTxLoading(false)
    }
  }

  useEffect(() => {
    mountedRef.current = true
    loadSummary()
    loadUtxos()
    loadTxs()
    const timer = window.setInterval(() => {
      loadSummary()
      loadUtxos()
      loadTxs()
    }, 30000)
    return () => {
      mountedRef.current = false
      window.clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    let mounted = true
    getMempoolFees()
      .then((res: any) => {
        if (!mounted) return
        const fastest = Number(res?.fastestFee || 0)
        const hour = Number(res?.hourFee || 0)
        setFees({ fastest, hour })
        setFeesStatus('')
      })
      .catch(() => {
        if (!mounted) return
        setFeesStatus(t('wallet.feeSuggestionsUnavailable'))
      })
    return () => {
      mounted = false
    }
  }, [])

  const confirmedBalance = Number(summary?.balances?.onchain_confirmed_sat ?? summary?.balances?.onchain_sat ?? 0)
  const unconfirmedBalance = Number(summary?.balances?.onchain_unconfirmed_sat ?? 0)
  const totalBalance = confirmedBalance + unconfirmedBalance

  const utxoTotal = useMemo(() => utxos.reduce((sum, utxo) => sum + Number(utxo.amount_sat || 0), 0), [utxos])
  const utxoAvg = utxos.length ? Math.round(utxoTotal / utxos.length) : 0
  const latestTx = useMemo(() => {
    if (!txs.length) return null
    return [...txs].sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())[0]
  }, [txs])

  const utxoFiltered = useMemo(() => {
    let list = [...utxos]
    if (utxoStatus === 'confirmed') {
      list = list.filter((item) => item.confirmations > 0)
    } else if (utxoStatus === 'unconfirmed') {
      list = list.filter((item) => item.confirmations <= 0)
    }
    if (utxoQuery.trim()) {
      const query = utxoQuery.trim().toLowerCase()
      list = list.filter((item) =>
        matchesQuery(item.address || '', query) ||
        matchesQuery(item.outpoint || '', query) ||
        matchesQuery(item.txid || '', query)
      )
    }
    const minAmount = toNumber(utxoMinAmount)
    const maxAmount = toNumber(utxoMaxAmount)
    if (minAmount !== null) {
      list = list.filter((item) => Number(item.amount_sat || 0) >= minAmount)
    }
    if (maxAmount !== null) {
      list = list.filter((item) => Number(item.amount_sat || 0) <= maxAmount)
    }
    const dir = utxoSortDir === 'desc' ? -1 : 1
    list.sort((a, b) => {
      if (utxoSortBy === 'confirmations') {
        return (a.confirmations - b.confirmations) * dir
      }
      if (utxoSortBy === 'address') {
        return a.address.localeCompare(b.address) * dir
      }
      return (a.amount_sat - b.amount_sat) * dir
    })
    return list
  }, [utxos, utxoStatus, utxoQuery, utxoMinAmount, utxoMaxAmount, utxoSortBy, utxoSortDir])

  const txFiltered = useMemo(() => {
    let list = [...txs]
    if (txDirection !== 'all') {
      list = list.filter((item) => item.direction === txDirection)
    }
    if (txStatus === 'confirmed') {
      list = list.filter((item) => item.confirmations > 0)
    } else if (txStatus === 'pending') {
      list = list.filter((item) => item.confirmations <= 0)
    }
    if (txQuery.trim()) {
      const query = txQuery.trim().toLowerCase()
      list = list.filter((item) => {
        const addressMatch = item.addresses?.some((addr) => matchesQuery(addr, query))
        return matchesQuery(item.txid || '', query) || Boolean(addressMatch)
      })
    }
    const dir = txSortDir === 'desc' ? -1 : 1
    list.sort((a, b) => {
      if (txSortBy === 'amount') {
        return (a.amount_sat - b.amount_sat) * dir
      }
      if (txSortBy === 'fee') {
        return (a.fee_sat - b.fee_sat) * dir
      }
      if (txSortBy === 'confirmations') {
        return (a.confirmations - b.confirmations) * dir
      }
      return (new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()) * dir
    })
    return list
  }, [txs, txDirection, txStatus, txQuery, txSortBy, txSortDir])

  const utxoPaneVisible = activePane === 'utxos'
  const txPaneVisible = activePane === 'txs'
  return (
    <section className="space-y-6">
      <div className="section-card onchain-hero">
        <div className="relative z-10 flex flex-wrap items-start justify-between gap-6">
          <div>
            <p className="text-xs uppercase tracking-[0.3em] text-fog/50">{t('onchainHub.kicker')}</p>
            <h2 className="mt-3 text-3xl font-semibold">{t('onchainHub.title')}</h2>
            <p className="mt-2 max-w-2xl text-sm text-fog/60">{t('onchainHub.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <div className="onchain-pill">
              <span className="text-xs text-fog/60">{t('onchainHub.feeFast')}</span>
              <span className="text-sm font-semibold">{fees?.fastest ? `${fees.fastest} sat/vB` : '-'}</span>
            </div>
            <div className="onchain-pill">
              <span className="text-xs text-fog/60">{t('onchainHub.feeHour')}</span>
              <span className="text-sm font-semibold">{fees?.hour ? `${fees.hour} sat/vB` : '-'}</span>
            </div>
            <button
              type="button"
              className="btn-secondary text-xs px-3 py-2"
              onClick={() => {
                loadSummary()
                loadUtxos()
                loadTxs()
              }}
            >
              {t('common.refresh')}
            </button>
          </div>
        </div>
        {feesStatus && <p className="relative z-10 mt-3 text-xs text-brass">{feesStatus}</p>}
        <div className="relative z-10 mt-6 grid gap-4 lg:grid-cols-5">
          <div className="onchain-kpi">
            <p>{t('onchainHub.confirmed')}</p>
            <h3>{formatSats(confirmedBalance)} sats</h3>
          </div>
          <div className="onchain-kpi">
            <p>{t('onchainHub.unconfirmed')}</p>
            <h3>{formatSats(unconfirmedBalance)} sats</h3>
          </div>
          <div className="onchain-kpi">
            <p>{t('onchainHub.total')}</p>
            <h3>{formatSats(totalBalance)} sats</h3>
          </div>
          <div className="onchain-kpi">
            <p>{t('onchainHub.utxoCount')}</p>
            <h3>{utxos.length.toLocaleString(locale)}</h3>
          </div>
          <div className="onchain-kpi">
            <p>{t('onchainHub.avgUtxo')}</p>
            <h3>{formatSats(utxoAvg)} sats</h3>
          </div>
        </div>
        {summaryError && (
          <p className="relative z-10 mt-4 text-sm text-ember">{summaryError}</p>
        )}
        {summaryLoading && !summaryError && (
          <p className="relative z-10 mt-4 text-sm text-fog/60">{t('onchainHub.loadingSummary')}</p>
        )}
      </div>

      <div className="flex gap-2 lg:hidden">
        <button
          className={clsx('flex-1 btn-secondary text-xs px-3 py-2', utxoPaneVisible && 'bg-white/10')}
          onClick={() => setActivePane('utxos')}
        >
          {t('onchainHub.utxos')}
        </button>
        <button
          className={clsx('flex-1 btn-secondary text-xs px-3 py-2', txPaneVisible && 'bg-white/10')}
          onClick={() => setActivePane('txs')}
        >
          {t('onchainHub.transactions')}
        </button>
      </div>

      <div className="grid gap-6 lg:grid-cols-[1.1fr_1.9fr]">
        <div className={clsx('section-card space-y-4', !utxoPaneVisible && 'hidden lg:block')}>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 className="text-xl font-semibold">{t('onchainHub.utxos')}</h3>
              <p className="text-xs text-fog/60">{t('onchainHub.utxosSubtitle')}</p>
            </div>
            <span className="text-xs text-fog/50">{utxoFiltered.length} {t('onchainHub.items')}</span>
          </div>

          <div className="grid gap-3">
            <input
              className="input-field"
              placeholder={t('onchainHub.searchUtxo')}
              value={utxoQuery}
              onChange={(e) => setUtxoQuery(e.target.value)}
            />
            <div className="flex flex-wrap gap-2">
              <button className={utxoStatus === 'all' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setUtxoStatus('all')}>
                {t('common.all')}
              </button>
              <button className={utxoStatus === 'confirmed' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setUtxoStatus('confirmed')}>
                {t('onchainHub.confirmed')}
              </button>
              <button className={utxoStatus === 'unconfirmed' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setUtxoStatus('unconfirmed')}>
                {t('onchainHub.unconfirmed')}
              </button>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <input
                className="input-field"
                placeholder={t('onchainHub.minAmount')}
                value={utxoMinAmount}
                onChange={(e) => setUtxoMinAmount(e.target.value)}
              />
              <input
                className="input-field"
                placeholder={t('onchainHub.maxAmount')}
                value={utxoMaxAmount}
                onChange={(e) => setUtxoMaxAmount(e.target.value)}
              />
            </div>
            <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
              <select className="input-field" value={utxoSortBy} onChange={(e) => setUtxoSortBy(e.target.value as any)}>
                <option value="amount">{t('onchainHub.sortAmount')}</option>
                <option value="confirmations">{t('onchainHub.sortConfirmations')}</option>
                <option value="address">{t('onchainHub.sortAddress')}</option>
              </select>
              <button className="btn-secondary text-xs px-3 py-2" onClick={() => setUtxoSortDir(utxoSortDir === 'desc' ? 'asc' : 'desc')}>
                {utxoSortDir === 'desc' ? t('onchainHub.sortDesc') : t('onchainHub.sortAsc')}
              </button>
            </div>
            <button
              className="text-xs text-fog/60 hover:text-white transition"
              onClick={() => {
                setUtxoQuery('')
                setUtxoStatus('all')
                setUtxoMinAmount('')
                setUtxoMaxAmount('')
                setUtxoSortBy('amount')
                setUtxoSortDir('desc')
              }}
            >
              {t('onchainHub.resetFilters')}
            </button>
          </div>

          <div className="onchain-table">
            <div className="onchain-table-head">
              <span>{t('onchainHub.amount')}</span>
              <span>{t('onchainHub.confirmations')}</span>
              <span>{t('onchainHub.address')}</span>
              <span>{t('onchainHub.outpoint')}</span>
            </div>
            {utxoLoading && <p className="text-sm text-fog/60">{t('onchainHub.loadingUtxos')}</p>}
            {utxoError && <p className="text-sm text-ember">{utxoError}</p>}
            {!utxoLoading && !utxoError && utxoFiltered.length === 0 && (
              <p className="text-sm text-fog/60">{t('onchainHub.emptyUtxos')}</p>
            )}
            <div className="onchain-table-body">
              {utxoFiltered.map((item) => (
                <div key={`${item.txid}-${item.vout}`} className={clsx('onchain-row', 'py-3')}>
                  <div className="onchain-cell" data-label={t('onchainHub.amount')}>
                    <p className="text-sm text-fog">{formatSats(item.amount_sat)} sats</p>
                    <p className="text-xs text-fog/50">{item.address_type || '-'}</p>
                  </div>
                  <div className="onchain-cell" data-label={t('onchainHub.confirmations')}>
                    <span className={clsx('onchain-badge', item.confirmations > 0 ? 'onchain-badge--ok' : 'onchain-badge--warn')}>
                      {item.confirmations > 0 ? item.confirmations.toLocaleString(locale) : t('onchainHub.pending')}
                    </span>
                  </div>
                  <div className="onchain-cell text-xs text-fog/70 break-all" data-label={t('onchainHub.address')}>
                    {item.address || '-'}
                  </div>
                  <div className="onchain-cell text-xs text-fog/70 break-all" data-label={t('onchainHub.outpoint')}>
                    {item.txid ? (
                      <a
                        className="onchain-link"
                        href={`${explorerBase}/tx/${item.txid}`}
                        target="_blank"
                        rel="noreferrer"
                        title={t('onchainHub.viewExternal')}
                      >
                        {item.outpoint || item.txid}
                      </a>
                    ) : (
                      <button
                        type="button"
                        className="onchain-link"
                        onClick={() => copyToClipboard(item.outpoint)}
                      >
                        {item.outpoint || '-'}
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>

        <div className={clsx('section-card space-y-4', !txPaneVisible && 'hidden lg:block')}>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 className="text-xl font-semibold">{t('onchainHub.transactions')}</h3>
              <p className="text-xs text-fog/60">{t('onchainHub.transactionsSubtitle')}</p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <div className="onchain-pill">
                <span className="text-xs text-fog/60">{t('onchainHub.lastActivity')}</span>
                <span className="text-sm">{latestTx ? formatTimestamp(latestTx.timestamp) : '-'}</span>
              </div>
              <span className="text-xs text-fog/50">{txFiltered.length} {t('onchainHub.items')}</span>
            </div>
          </div>

          <div className="grid gap-3">
            <input
              className="input-field"
              placeholder={t('onchainHub.searchTx')}
              value={txQuery}
              onChange={(e) => setTxQuery(e.target.value)}
            />
            <div className="flex flex-wrap gap-2">
              <button className={txDirection === 'all' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setTxDirection('all')}>
                {t('common.all')}
              </button>
              <button className={txDirection === 'in' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setTxDirection('in')}>
                {t('onchainHub.inbound')}
              </button>
              <button className={txDirection === 'out' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setTxDirection('out')}>
                {t('onchainHub.outbound')}
              </button>
            </div>
            <div className="flex flex-wrap gap-2">
              <button className={txStatus === 'all' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setTxStatus('all')}>
                {t('common.all')}
              </button>
              <button className={txStatus === 'confirmed' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setTxStatus('confirmed')}>
                {t('onchainHub.confirmed')}
              </button>
              <button className={txStatus === 'pending' ? 'btn-primary text-xs px-3 py-2' : 'btn-secondary text-xs px-3 py-2'} onClick={() => setTxStatus('pending')}>
                {t('onchainHub.pending')}
              </button>
            </div>
            <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
              <select className="input-field" value={txSortBy} onChange={(e) => setTxSortBy(e.target.value as any)}>
                <option value="time">{t('onchainHub.sortTime')}</option>
                <option value="amount">{t('onchainHub.sortAmount')}</option>
                <option value="fee">{t('onchainHub.sortFee')}</option>
                <option value="confirmations">{t('onchainHub.sortConfirmations')}</option>
              </select>
              <button className="btn-secondary text-xs px-3 py-2" onClick={() => setTxSortDir(txSortDir === 'desc' ? 'asc' : 'desc')}>
                {txSortDir === 'desc' ? t('onchainHub.sortDesc') : t('onchainHub.sortAsc')}
              </button>
            </div>
            <button
              className="text-xs text-fog/60 hover:text-white transition"
              onClick={() => {
                setTxQuery('')
                setTxDirection('all')
                setTxStatus('all')
                setTxSortBy('time')
                setTxSortDir('desc')
              }}
            >
              {t('onchainHub.resetFilters')}
            </button>
          </div>

          <div className="onchain-table">
            <div className="onchain-table-head onchain-table-head--tx">
              <span>{t('onchainHub.type')}</span>
              <span>{t('onchainHub.amount')}</span>
              <span>{t('onchainHub.fee')}</span>
              <span>{t('onchainHub.confirmations')}</span>
              <span>{t('onchainHub.address')}</span>
              <span>{t('onchainHub.txid')}</span>
            </div>
            {txLoading && <p className="text-sm text-fog/60">{t('onchainHub.loadingTransactions')}</p>}
            {txError && <p className="text-sm text-ember">{txError}</p>}
            {!txLoading && !txError && txFiltered.length === 0 && (
              <p className="text-sm text-fog/60">{t('onchainHub.emptyTransactions')}</p>
            )}
            <div className="onchain-table-body">
              {txFiltered.map((item) => (
                <div key={item.txid} className={clsx('onchain-row onchain-row--tx', 'py-3')}>
                  <div className="onchain-cell" data-label={t('onchainHub.type')}>
                    <div className="onchain-cell-inline">
                      <span className={clsx('onchain-dot', item.direction === 'in' ? 'onchain-dot--in' : 'onchain-dot--out')} />
                      <div>
                        <p className="text-sm text-fog">{item.direction === 'in' ? t('onchainHub.inbound') : t('onchainHub.outbound')}</p>
                        <p className="text-xs text-fog/50">{formatTimestamp(item.timestamp)}</p>
                      </div>
                    </div>
                  </div>
                  <div className="onchain-cell text-sm text-fog" data-label={t('onchainHub.amount')}>
                    {formatSats(item.amount_sat)} sats
                  </div>
                  <div className="onchain-cell text-xs text-fog/60" data-label={t('onchainHub.fee')}>
                    {item.fee_sat ? `${formatSats(item.fee_sat)} sats` : '-'}
                  </div>
                  <div className="onchain-cell" data-label={t('onchainHub.confirmations')}>
                    <span className={clsx('onchain-badge', item.confirmations > 0 ? 'onchain-badge--ok' : 'onchain-badge--warn')}>
                      {item.confirmations > 0 ? item.confirmations.toLocaleString(locale) : t('onchainHub.pending')}
                    </span>
                  </div>
                  <div className="onchain-cell onchain-addresses text-xs text-fog/70" data-label={t('onchainHub.address')}>
                    {item.addresses && item.addresses.length ? (
                      <>
                        {item.addresses.slice(0, 3).map((addr) => (
                          <span key={addr}>{addr}</span>
                        ))}
                        {item.addresses.length > 3 && (
                          <span className="text-fog/50">+{item.addresses.length - 3} {t('onchainHub.more')}</span>
                        )}
                      </>
                    ) : (
                      <span>-</span>
                    )}
                  </div>
                  <div className="onchain-cell text-xs text-fog/70 break-all" data-label={t('onchainHub.txid')}>
                    <a
                      className="onchain-link"
                      href={`${explorerBase}/tx/${item.txid}`}
                      target="_blank"
                      rel="noreferrer"
                      title={t('onchainHub.viewExternal')}
                    >
                      {item.txid}
                    </a>
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}
