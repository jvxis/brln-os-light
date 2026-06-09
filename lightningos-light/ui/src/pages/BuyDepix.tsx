import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createDepixOrder, getDepixConfig, getDepixOrder, getDepixOrders } from '../api'
import { getLocale } from '../i18n'

type DepixConfig = {
  enabled: boolean
  timezone: string
  min_amount_cents: number
  max_amount_cents: number
  daily_limit_cents: number
  daily_used_cents: number
  daily_remaining_cents: number
  eulen_fee_cents: number
  brln_fee_bps: number
  brln_fee_percent: string
}

type DepixOrder = {
  id: number
  status: string
  deposit_id: string
  liquid_address: string
  gross_cents: number
  eulen_fee_cents: number
  brln_fee_cents: number
  net_cents: number
  qr_copy: string
  qr_image_url: string
  checkout_url: string
  timeout_seconds: number
  remaining_seconds: number
  created_at: string
  confirmed_at?: string
}

const USER_KEY_STORAGE = 'los-depix-user-key'
const TERMINAL_RECHECK_WINDOW_MS = 2 * 60 * 60 * 1000

const normalizeAmountInput = (raw: string) => raw.trim().replace(',', '.')

const parseAmountToCents = (raw: string) => {
  const value = normalizeAmountInput(raw)
  if (!value) return 0
  if (!/^\d+(\.\d{1,2})?$/.test(value)) return NaN
  const parts = value.split('.')
  const intPart = Number(parts[0] || 0)
  const fracPart = Number(((parts[1] || '') + '00').slice(0, 2))
  return intPart * 100 + fracPart
}

const ensureUserKey = () => {
  const existing = window.localStorage.getItem(USER_KEY_STORAGE)
  if (existing) return existing
  let generated = ''
  if (window.crypto && typeof window.crypto.randomUUID === 'function') {
    generated = window.crypto.randomUUID().replace(/-/g, '')
  } else {
    generated = `${Date.now()}${Math.floor(Math.random() * 1_000_000_000)}`
  }
  const key = `depix_${generated}`.slice(0, 40)
  window.localStorage.setItem(USER_KEY_STORAGE, key)
  return key
}

const statusTone = (status: string) => {
  const normalized = (status || '').toLowerCase()
  if (normalized === 'depix_sent') return 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
  if (normalized === 'pending' || normalized === 'under_review' || normalized === 'pending_pix2fa') {
    return 'bg-amber-500/15 text-amber-200 border border-amber-400/30'
  }
  if (normalized === 'expired' || normalized === 'timeout' || normalized === 'error' || normalized === 'not_found') {
    return 'bg-rose-500/15 text-rose-200 border border-rose-400/30'
  }
  return 'bg-white/10 text-fog/70 border border-white/10'
}

const isTerminalStatus = (status: string) => {
  const normalized = (status || '').toLowerCase()
  return ['depix_sent', 'expired', 'timeout', 'canceled', 'refunded', 'error', 'not_found'].includes(normalized)
}

const shouldRefreshOrder = (order: DepixOrder) => {
  const normalized = (order.status || '').toLowerCase()
  if (normalized === 'pending' || normalized === 'under_review' || normalized === 'pending_pix2fa') return true
  if (!['timeout', 'expired', 'error', 'not_found'].includes(normalized)) return false
  if (order.confirmed_at) return false
  const createdAtMs = new Date(order.created_at).getTime()
  if (!Number.isFinite(createdAtMs)) return true
  const timeoutMs = Math.max(0, (order.timeout_seconds || 0) * 1000)
  return Date.now() <= createdAtMs + timeoutMs + TERMINAL_RECHECK_WINDOW_MS
}

export default function BuyDepix() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [userKey, setUserKey] = useState('')
  const [timezone, setTimezone] = useState('America/Sao_Paulo')
  const [config, setConfig] = useState<DepixConfig | null>(null)
  const [orders, setOrders] = useState<DepixOrder[]>([])
  const [activeOrderID, setActiveOrderID] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [message, setMessage] = useState('')
  const [amountBRL, setAmountBRL] = useState('')
  const [liquidAddress, setLiquidAddress] = useState('')
  const [copying, setCopying] = useState(false)

  const brl = useMemo(
    () => new Intl.NumberFormat(locale, { style: 'currency', currency: 'BRL' }),
    [locale]
  )

  useEffect(() => {
    const key = ensureUserKey()
    setUserKey(key)
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'America/Sao_Paulo'
    setTimezone(tz)
  }, [])

  const loadConfig = async (key: string, tz: string) => {
    const payload = await getDepixConfig({ user_key: key, timezone: tz })
    setConfig(payload as DepixConfig)
  }

  const loadOrders = async (key: string) => {
    try {
      const payload: any = await getDepixOrders({ user_key: key, limit: 30 })
      const items: DepixOrder[] = Array.isArray(payload?.items) ? payload.items : []
      setOrders(items)
      if (items.length > 0 && activeOrderID === null) {
        const firstLive = items.find((item) => shouldRefreshOrder(item))
        setActiveOrderID(firstLive ? firstLive.id : null)
      }
    } catch {
      setOrders([])
    }
  }

  useEffect(() => {
    if (!userKey) return
    let active = true
    const run = async () => {
      setLoading(true)
      setMessage('')
      try {
        await loadConfig(userKey, timezone)
        await loadOrders(userKey)
      } catch (err: any) {
        if (!active) return
        setMessage(err?.message || t('depix.loadFailed'))
      } finally {
        if (!active) return
        setLoading(false)
      }
    }
    run()
    return () => {
      active = false
    }
  }, [userKey, timezone, t])

  const activeOrder = useMemo(() => {
    if (orders.length === 0) return null
    if (activeOrderID !== null) {
      const match = orders.find((item) => item.id === activeOrderID)
      if (match && shouldRefreshOrder(match)) return match
    }
    return orders.find((item) => shouldRefreshOrder(item)) || null
  }, [orders, activeOrderID])

  useEffect(() => {
    if (!userKey || !activeOrder || !shouldRefreshOrder(activeOrder)) return
    let active = true
    const timer = window.setInterval(async () => {
      try {
        const refreshed = await getDepixOrder(activeOrder.id, { user_key: userKey, refresh: true }) as DepixOrder
        if (!active) return
        setOrders((current) => current.map((item) => (item.id === refreshed.id ? refreshed : item)))
        if (shouldRefreshOrder(refreshed)) return
        await loadConfig(userKey, timezone)
      } catch {
        // keep UI calm on transient provider errors
      }
    }, 5000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [activeOrder, userKey, timezone])

  const grossCents = parseAmountToCents(amountBRL)
  const eulenFeeCents = config?.eulen_fee_cents ?? 99
  const brlnFeeCents = Number.isFinite(grossCents) && grossCents > 0 ? Math.round(grossCents * ((config?.brln_fee_bps ?? 150) / 10000)) : 0
  const netCents = Number.isFinite(grossCents) ? Math.max(0, grossCents - eulenFeeCents - brlnFeeCents) : 0

  const statusLabel = (value: string) => {
    const normalized = (value || '').toLowerCase()
    if (normalized === 'pending') return t('depix.statusPending')
    if (normalized === 'under_review' || normalized === 'pending_pix2fa') return t('depix.statusPending')
    if (normalized === 'depix_sent') return t('depix.statusSent')
    if (normalized === 'expired') return t('depix.statusExpired')
    if (normalized === 'timeout') return t('depix.statusTimeout')
    if (normalized === 'canceled') return t('depix.statusCanceled')
    if (normalized === 'refunded') return t('depix.statusRefunded')
    if (normalized === 'error') return t('depix.statusError')
    if (normalized === 'not_found') return t('depix.statusNotFound')
    return t('depix.statusUnknown')
  }

  const validate = () => {
    if (!userKey) return t('depix.userKeyMissing')
    if (!liquidAddress.trim()) return t('depix.addressRequired')
    if (!/^((lq1|ex1|tlq1|tex1)[ac-hj-np-z02-9]{20,120})$/i.test(liquidAddress.trim())) {
      return t('depix.addressInvalid')
    }
    if (!Number.isFinite(grossCents) || grossCents <= 0) return t('depix.amountInvalid')
    const min = config?.min_amount_cents ?? 10000
    const max = config?.max_amount_cents ?? 50000
    if (grossCents < min || grossCents > max) {
      return t('depix.amountRange', { min: brl.format(min / 100), max: brl.format(max / 100) })
    }
    const remaining = config?.daily_remaining_cents ?? 0
    if (grossCents > remaining) {
      return t('depix.dailyLimitExceeded', { value: brl.format(remaining / 100) })
    }
    return ''
  }

  const handleCreate = async () => {
    const error = validate()
    if (error) {
      setMessage(error)
      return
    }
    setSubmitting(true)
    setMessage('')
    try {
      const order = await createDepixOrder({
        user_key: userKey,
        timezone,
        liquid_address: liquidAddress.trim().toLowerCase(),
        amount_brl: normalizeAmountInput(amountBRL)
      }) as DepixOrder
      setOrders((current) => [order, ...current.filter((item) => item.id !== order.id)])
      setActiveOrderID(order.id)
      await loadConfig(userKey, timezone)
      setMessage(t('depix.created'))
    } catch (err: any) {
      setMessage(err?.message || t('depix.createFailed'))
    } finally {
      setSubmitting(false)
    }
  }

  const handleCopy = async () => {
    if (!activeOrder?.qr_copy) return
    setCopying(true)
    try {
      await navigator.clipboard.writeText(activeOrder.qr_copy)
      setMessage(t('common.copied'))
    } catch {
      setMessage(t('common.copyFailedManual'))
    } finally {
      setCopying(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <h2 className="text-2xl font-semibold">{t('depix.title')}</h2>
        <p className="text-fog/60">{t('depix.subtitle')}</p>
        {message && <p className="mt-3 text-sm text-brass">{message}</p>}
      </div>

      {loading && <p className="text-fog/60">{t('depix.loading')}</p>}

      {!loading && config && !config.enabled && (
        <div className="section-card space-y-3">
          <h3 className="text-lg font-semibold">{t('depix.disabledTitle')}</h3>
          <p className="text-fog/70">{t('depix.disabledBody')}</p>
          <a className="btn-primary inline-flex items-center" href="#apps">{t('depix.openAppStore')}</a>
        </div>
      )}

      {!loading && config?.enabled && (
        <>
          <div className="grid gap-6 lg:grid-cols-2">
            <div className="section-card space-y-3">
              <h3 className="text-lg font-semibold">{t('depix.feesTitle')}</h3>
              <p className="text-sm text-fog/70">{t('depix.eulenFee', { value: brl.format((config.eulen_fee_cents || 0) / 100) })}</p>
              <p className="text-sm text-fog/70">{t('depix.brlnFee', { value: `${config.brln_fee_percent}%` })}</p>
              <p className="text-sm text-fog/70">{t('depix.minMax', { min: brl.format(config.min_amount_cents / 100), max: brl.format(config.max_amount_cents / 100) })}</p>
              <p className="text-sm text-fog/70">{t('depix.dailyLimit', { value: brl.format(config.daily_limit_cents / 100) })}</p>
              <p className="text-sm text-fog/70">{t('depix.dailyUsed', { used: brl.format(config.daily_used_cents / 100) })}</p>
              <p className="text-sm text-fog/70">{t('depix.dailyRemaining', { remaining: brl.format(config.daily_remaining_cents / 100) })}</p>
            </div>

            <div className="section-card space-y-4">
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('depix.liquidAddress')}</label>
                <input
                  className="input-field mt-2"
                  value={liquidAddress}
                  onChange={(e) => setLiquidAddress(e.target.value)}
                  placeholder={t('depix.liquidAddressPlaceholder')}
                />
              </div>
              <div>
                <label className="text-xs uppercase tracking-wide text-fog/60">{t('depix.amountBrl')}</label>
                <input
                  className="input-field mt-2"
                  value={amountBRL}
                  onChange={(e) => setAmountBRL(e.target.value)}
                  placeholder={t('depix.amountPlaceholder')}
                />
              </div>

              <div className="rounded-2xl border border-white/10 bg-ink/40 p-4 text-sm space-y-1">
                <p className="text-fog/70">{t('depix.summaryGross')}: <span className="text-fog">{Number.isFinite(grossCents) ? brl.format(Math.max(0, grossCents) / 100) : '-'}</span></p>
                <p className="text-fog/70">{t('depix.summaryEulen')}: <span className="text-fog">{brl.format(eulenFeeCents / 100)}</span></p>
                <p className="text-fog/70">{t('depix.summaryBrln')}: <span className="text-fog">{brl.format(brlnFeeCents / 100)}</span></p>
                <p className="text-fog/70">{t('depix.summaryNet')}: <span className="text-fog">{brl.format(netCents / 100)}</span></p>
              </div>

              <button className="btn-primary" onClick={handleCreate} disabled={submitting}>
                {submitting ? t('depix.creatingOrder') : t('depix.createOrder')}
              </button>
            </div>
          </div>

          {activeOrder && (
            <div className="section-card space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h3 className="text-lg font-semibold">{t('depix.checkoutTitle')}</h3>
                <span className={`text-xs uppercase tracking-wide px-3 py-1 rounded-full ${statusTone(activeOrder.status)}`}>
                  {statusLabel(activeOrder.status)}
                </span>
              </div>

              <div className="grid gap-6 lg:grid-cols-[minmax(0,280px),1fr]">
                <div className="space-y-3">
                  {activeOrder.qr_image_url ? (
                    <img src={activeOrder.qr_image_url} alt="PIX QR" className="w-full rounded-2xl border border-white/10 bg-white p-2" />
                  ) : (
                    <div className="rounded-2xl border border-white/10 p-4 text-sm text-fog/60">{t('depix.qrUnavailable')}</div>
                  )}
                  {activeOrder.checkout_url && (
                    <a className="btn-secondary inline-flex items-center" href={activeOrder.checkout_url} target="_blank" rel="noreferrer">
                      {t('depix.checkoutLink')}
                    </a>
                  )}
                </div>
                <div className="space-y-3">
                  <label className="text-xs uppercase tracking-wide text-fog/60">{t('depix.pixCopyPaste')}</label>
                  <textarea className="input-field min-h-[140px]" value={activeOrder.qr_copy || ''} readOnly />
                  <button className="btn-secondary" onClick={handleCopy} disabled={copying || !activeOrder.qr_copy}>
                    {t('depix.copyQr')}
                  </button>
                  <div className="text-xs text-fog/60 space-y-1">
                    <p>{t('depix.orderId')}: {activeOrder.deposit_id}</p>
                    <p>{t('depix.createdAt')}: {new Date(activeOrder.created_at).toLocaleString(locale)}</p>
                    {activeOrder.remaining_seconds > 0 && !isTerminalStatus(activeOrder.status) && (
                      <p>{t('depix.remaining')}: {activeOrder.remaining_seconds}s</p>
                    )}
                  </div>
                </div>
              </div>
            </div>
          )}

          <div className="section-card space-y-4">
            <h3 className="text-lg font-semibold">{t('depix.historyTitle')}</h3>
            {orders.length === 0 && <p className="text-fog/60">{t('depix.historyEmpty')}</p>}
            {orders.length > 0 && (
              <div className="overflow-x-auto">
                <table className="min-w-full text-sm">
                  <thead className="text-fog/60">
                    <tr>
                      <th className="text-left py-2 pr-4">{t('depix.colDate')}</th>
                      <th className="text-left py-2 pr-4">{t('depix.colGross')}</th>
                      <th className="text-left py-2 pr-4">{t('depix.colNet')}</th>
                      <th className="text-left py-2 pr-4">{t('depix.colStatus')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {orders.map((item) => (
                      <tr key={item.id} className="border-t border-white/10 cursor-pointer hover:bg-white/5" onClick={() => setActiveOrderID(item.id)}>
                        <td className="py-2 pr-4">{new Date(item.created_at).toLocaleString(locale)}</td>
                        <td className="py-2 pr-4">{brl.format((item.gross_cents || 0) / 100)}</td>
                        <td className="py-2 pr-4">{brl.format((item.net_cents || 0) / 100)}</td>
                        <td className="py-2 pr-4">{statusLabel(item.status)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}
    </section>
  )
}
