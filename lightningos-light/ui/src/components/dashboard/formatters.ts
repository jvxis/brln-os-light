import type { ReportMetrics, Tone } from './types'

export const clamp = (value: number, min = 0, max = 100) => Math.min(max, Math.max(min, value))

export const compactValue = (value: string, head = 10, tail = 10) => {
  if (!value) return ''
  if (value.length <= head + tail + 3) return value
  return `${value.slice(0, head)}...${value.slice(-tail)}`
}

export const formatSats = (locale: string, value?: number | null) => {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value)
}

export const formatSignedSats = (locale: string, value?: number | null) => {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  const sign = value > 0 ? '+' : ''
  return `${sign}${formatSats(locale, value)}`
}

export const formatPercent = (locale: string, value?: number | null, maxFractionDigits = 1) => {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  return new Intl.NumberFormat(locale, { maximumFractionDigits: maxFractionDigits }).format(value)
}

export const formatGB = (locale: string, value?: number | null, digits = 1) => {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  return `${new Intl.NumberFormat(locale, { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(value)} GB`
}

export const formatMB = (locale: string, value?: number | null) => {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value)} MB`
}

export const formatTemp = (locale: string, value?: number | null) => {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(value)} C`
}

export const formatTimestamp = (locale: string, value?: string | number | null) => {
  if (value === undefined || value === null || value === '') return '-'
  const date = typeof value === 'number' ? new Date(value * 1000) : new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString(locale, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

export const formatTimeAgo = (locale: string, value?: string | number | null) => {
  if (value === undefined || value === null || value === '') return '-'
  const date = typeof value === 'number' ? new Date(value * 1000) : new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  const diffMs = date.getTime() - Date.now()
  const diffMinutes = Math.round(diffMs / 60000)
  const rtf = new Intl.RelativeTimeFormat(locale.startsWith('pt') ? 'pt-BR' : 'en-US', { numeric: 'auto' })
  if (Math.abs(diffMinutes) < 60) return rtf.format(diffMinutes, 'minute')
  const diffHours = Math.round(diffMinutes / 60)
  if (Math.abs(diffHours) < 48) return rtf.format(diffHours, 'hour')
  const diffDays = Math.round(diffHours / 24)
  return rtf.format(diffDays, 'day')
}

export const toneFromHealthStatus = (value?: string | null): Tone => {
  const normalized = String(value || '').trim().toUpperCase()
  if (normalized === 'OK') return 'ok'
  if (normalized === 'WARN') return 'warn'
  if (normalized === 'ERR') return 'danger'
  return 'muted'
}

export const toneFromStatusText = (value?: string | null): Tone => {
  const normalized = String(value || '').trim().toLowerCase()
  if (!normalized) return 'muted'
  if (['ok', 'running', 'active', 'enabled', 'ready', 'synced', 'unlocked', 'passed', 'confirmed'].includes(normalized)) {
    return 'ok'
  }
  if (['warn', 'warning', 'checking', 'pending', 'stopped', 'degraded', 'external'].includes(normalized)) {
    return 'warn'
  }
  if (['err', 'error', 'failed', 'inactive', 'disabled', 'unknown', 'unavailable'].includes(normalized)) {
    return 'danger'
  }
  return 'info'
}

export const metricOffchainCost = (metrics?: ReportMetrics | null) => {
  if (!metrics) return 0
  return metrics.offchain_fee_cost_sats
    ?? metrics.total_fee_cost_sats
    ?? ((metrics.rebalance_fee_cost_sats ?? 0) + (metrics.payment_fee_cost_sats ?? 0))
}

export const metricTotalCost = (metrics?: ReportMetrics | null) => {
  if (!metrics) return 0
  return metrics.total_fee_cost_with_onchain_sats
    ?? (metricOffchainCost(metrics) + (metrics.onchain_fee_cost_sats ?? 0))
}

export const metricNetWithKeysend = (metrics?: ReportMetrics | null) => {
  if (!metrics) return 0
  return metrics.net_with_keysend_sats
    ?? ((metrics.net_routing_profit_sats ?? 0) + (metrics.keysend_received_sats ?? 0))
}
