export type ApyBalancePoint = {
  onchain_balance_sats?: number | null
  lightning_balance_sats?: number | null
  total_balance_sats?: number | null
}

export type ApyResult = {
  apyPct: number
  averageBalanceSats: number
  sampleCount: number
  source: 'range-average' | 'fallback'
}

const finiteNumber = (value: unknown): value is number => (
  typeof value === 'number' && Number.isFinite(value)
)

export const totalBalanceFromPoint = (point?: ApyBalancePoint | null) => {
  if (!point) return null
  if (finiteNumber(point.total_balance_sats) && point.total_balance_sats > 0) {
    return point.total_balance_sats
  }

  const onchain = finiteNumber(point.onchain_balance_sats) ? point.onchain_balance_sats : null
  const lightning = finiteNumber(point.lightning_balance_sats) ? point.lightning_balance_sats : null
  if (onchain === null && lightning === null) return null

  const total = (onchain ?? 0) + (lightning ?? 0)
  return total > 0 ? total : null
}

export const averageTotalBalance = (points?: ApyBalancePoint[] | null) => {
  if (!Array.isArray(points) || points.length === 0) return null

  let total = 0
  let sampleCount = 0
  for (const point of points) {
    const balance = totalBalanceFromPoint(point)
    if (balance === null) continue
    total += balance
    sampleCount += 1
  }

  if (sampleCount === 0) return null
  return {
    averageBalanceSats: total / sampleCount,
    sampleCount,
  }
}

export const calculateApyPct = (netSats: number, days: number, averageBalanceSats: number) => {
  if (!finiteNumber(netSats) || !finiteNumber(days) || !finiteNumber(averageBalanceSats)) return null
  if (days <= 0 || averageBalanceSats <= 0) return null

  const periodReturn = netSats / averageBalanceSats
  const base = 1 + periodReturn
  if (base <= 0) return null

  const apyPct = (Math.pow(base, 365 / days) - 1) * 100
  return Number.isFinite(apyPct) ? apyPct : null
}

export const calculateApyPctFromNet = (
  netSats: number,
  days: number,
  points?: ApyBalancePoint[] | null,
  fallbackBalanceSats?: number | null
): ApyResult | null => {
  const average = averageTotalBalance(points)
  const balance = average && average.sampleCount >= 2
    ? average
    : finiteNumber(fallbackBalanceSats) && fallbackBalanceSats > 0
      ? { averageBalanceSats: fallbackBalanceSats, sampleCount: 0 }
      : null
  if (!balance) return null

  const apyPct = calculateApyPct(netSats, days, balance.averageBalanceSats)
  if (apyPct === null) return null

  return {
    ...balance,
    apyPct,
    source: average && average.sampleCount >= 2 ? 'range-average' : 'fallback',
  }
}

export const formatApyPercent = (locale: string, value?: number | null, maximumFractionDigits = 1) => {
  if (!finiteNumber(value)) return '-'
  const sign = value > 0 ? '+' : ''
  return `${sign}${new Intl.NumberFormat(locale, { maximumFractionDigits }).format(value)}%`
}

export const calculateRevenueApyPct = (netSats: number, revenueSats: number, days: number) => (
  calculateApyPct(netSats, days, revenueSats)
)
