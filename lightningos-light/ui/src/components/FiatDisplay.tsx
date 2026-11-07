import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { getBitcoinMarket } from '../api'
import { getLocale } from '../i18n'
import type { BitcoinMarketStatus } from './dashboard/types'

const currencies = ['BRL', 'USD', 'EUR'] as const
type Currency = typeof currencies[number]
const storageKey = 'los-fiat-currency'
const refreshInterval = 5 * 60 * 1000
const isCurrency = (value: unknown): value is Currency => currencies.includes(value as Currency)

const FiatContext = createContext<{
  currency: Currency
  setCurrency: (currency: Currency) => void
  market: BitcoinMarketStatus | null
}>({ currency: 'BRL', setCurrency: () => {}, market: null })

export function FiatProvider({ children }: { children: ReactNode }) {
  const [currency, updateCurrency] = useState<Currency>(() => {
    try {
      const stored = localStorage.getItem(storageKey)
      return isCurrency(stored) ? stored : 'BRL'
    } catch { return 'BRL' }
  })
  const [market, setMarket] = useState<BitcoinMarketStatus | null>(null)
  const setCurrency = (next: Currency) => {
    if (!isCurrency(next)) return
    updateCurrency(next)
    try { localStorage.setItem(storageKey, next) } catch { /* Private/storage-restricted browsers still work. */ }
  }
  useEffect(() => {
    let mounted = true
    let pending = false
    const load = async () => {
      if (pending) return
      pending = true
      try {
        const data = await getBitcoinMarket()
        if (mounted) setMarket(data as BitcoinMarketStatus)
      } catch {
        if (mounted) setMarket(null)
      } finally { pending = false }
    }
    void load()
    const timer = setInterval(load, refreshInterval)
    return () => { mounted = false; clearInterval(timer) }
  }, [])
  return <FiatContext.Provider value={{ currency, setCurrency, market }}>{children}</FiatContext.Provider>
}

export const useBitcoinMarket = () => useContext(FiatContext).market

export function CurrencySelector() {
  const { currency, setCurrency } = useContext(FiatContext)
  const { t } = useTranslation()
  return (
    <select className="fiat-selector" value={currency} onChange={event => setCurrency(event.target.value as Currency)}
      aria-label={t('fiat.currency')} title={t('fiat.currency')}>
      {currencies.map(value => <option key={value} value={value}>{value}</option>)}
    </select>
  )
}

export function FiatAmount({ sats }: { sats: number | null | undefined }) {
  const { currency, market } = useContext(FiatContext)
  const { t, i18n } = useTranslation()
  const prices = Array.isArray(market?.prices) ? market.prices : []
  const price = prices.find(item => String(item?.currency ?? '').toUpperCase() === currency)?.value
  const updated = Date.parse(market?.updated_at ?? '')
  // Never present an unavailable/old quotation as a current estimate.
  if (typeof sats !== 'number' || !Number.isFinite(sats) || market?.stale || market?.price_error
    || !Number.isFinite(updated) || Date.now() - updated > 15 * 60 * 1000
    || typeof price !== 'number' || !Number.isFinite(price) || price <= 0) return null
  const value = sats / 100_000_000 * price
  if (!Number.isFinite(value)) return null
  return <span className="fiat-equivalent" title={t('fiat.hint')}>
    ≈ {new Intl.NumberFormat(getLocale(i18n.language), { style: 'currency', currency }).format(value)}
  </span>
}
