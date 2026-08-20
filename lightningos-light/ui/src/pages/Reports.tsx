import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis
} from 'recharts'
import {
  getReportsConfig,
  getReportsCustom,
  getReportsLive,
  getReportsMovementLive,
  getReportsRange,
  getReportsSummary,
  getReportsSummaryCustom,
  updateReportsConfig
} from '../api'
import { getLocale } from '../i18n'
import { calculateDisplayApyPct, calculateShortWindowApyPct, formatApyPercent, totalBalanceFromPoint } from '../utils/apy'

type ReportSeriesItem = {
  date: string
  forward_fee_revenue_sats: number
  rebalance_fee_cost_sats: number
  payment_fee_cost_sats?: number
  onchain_fee_cost_sats?: number
  onchain_coop_close_cost_sats?: number
  onchain_local_force_cost_sats?: number
  onchain_remote_force_cost_sats?: number
  offchain_fee_cost_sats?: number
  keysend_received_sats?: number
  sales_revenue_sats?: number
  sales_count?: number
  net_total_sats?: number
  keysend_received_count?: number
  total_fee_cost_sats?: number
  total_fee_cost_with_onchain_sats?: number
  net_routing_profit_sats: number
  net_with_keysend_sats?: number
  keysend_sent_sats?: number
  keysend_sent_count?: number
  total_revenue_sats?: number
  total_cost_sats?: number
  marked_revenue_sats?: number
  marked_cost_sats?: number
  forward_count: number
  rebalance_count: number
  rebalance_volume_sats?: number
  payment_count?: number
  routed_volume_sats: number
  onchain_balance_sats?: number | null
  lightning_balance_sats?: number | null
  total_balance_sats?: number | null
  provenance_last_sync_at?: string
  provenance_last_sync_age_hours?: number
  provenance_health_alert?: boolean
  provenance_last_error?: string
}

type ReportMetrics = {
  forward_fee_revenue_sats: number
  rebalance_fee_cost_sats: number
  payment_fee_cost_sats?: number
  onchain_fee_cost_sats?: number
  onchain_coop_close_cost_sats?: number
  onchain_local_force_cost_sats?: number
  onchain_remote_force_cost_sats?: number
  offchain_fee_cost_sats?: number
  keysend_received_sats?: number
  sales_revenue_sats?: number
  sales_count?: number
  net_total_sats?: number
  keysend_received_count?: number
  total_fee_cost_sats?: number
  total_fee_cost_with_onchain_sats?: number
  net_routing_profit_sats: number
  net_with_keysend_sats?: number
  keysend_sent_sats?: number
  keysend_sent_count?: number
  total_revenue_sats?: number
  total_cost_sats?: number
  marked_revenue_sats?: number
  marked_cost_sats?: number
  forward_count: number
  rebalance_count: number
  rebalance_volume_sats?: number
  payment_count?: number
  routed_volume_sats: number
  onchain_balance_sats?: number | null
  lightning_balance_sats?: number | null
  total_balance_sats?: number | null
}

type SeriesResponse = {
  range: string
  timezone: string
  series: ReportSeriesItem[]
}

type SummaryResponse = {
  range: string
  timezone: string
  days: number
  totals: ReportMetrics
  averages: ReportMetrics
  movement_target_sats?: number
  movement_pct?: number
}

type LiveResponse = ReportMetrics & {
  start: string
  end: string
  timezone: string
}

type MovementLiveResponse = {
  date: string
  start: string
  end: string
  timezone: string
  outbound_target_sats: number
  routed_volume_sats: number
  movement_pct: number
}

type ReportsConfig = {
  live_timeout_sec?: number | null
  live_lookback_hours?: number | null
  run_timeout_sec?: number | null
}

type BaseRangeKey = 'd-1' | 'date' | 'month' | '3m' | '6m' | '12m' | 'all'
type QuickRangeKey = 'prev-month' | 'current-month' | 'prev-year' | 'current-year'
type RangeKey = BaseRangeKey | QuickRangeKey
type ChartGranularity = 'day' | 'week' | 'month'

type DateWindow = {
  from: string
  to: string
}

type ChartDataPoint = {
  date: string
  startDate: string
  endDate: string
  label: string
  net: number
  netRouting: number
  keysend: number
  sales: number
  markedRevenue: number
  markedCost: number
  keysendSent: number
  totalRevenue: number
  revenue: number
  rebalanceCost: number
  paymentCost: number
  forwardVolume: number
  rebalanceVolume: number
  offchainCost: number
  onchainCost: number
  onchainCoopCloseCost: number
  onchainLocalForceCost: number
  onchainRemoteForceCost: number
  costWithOnchain: number
  onchain: number | null
  lightning: number | null
  total: number | null
}

type BalanceChangeDataPoint = {
  label: string
  onchain: number | null
  lightning: number | null
  total: number | null
  onchainChange: number | null
  lightningChange: number | null
  totalChange: number | null
}

const primaryRangeOptions: RangeKey[] = ['d-1', 'date', 'month', '3m', '6m', '12m', 'all']
const calendarRangeOptions: RangeKey[] = ['prev-month', 'current-month', 'prev-year', 'current-year']
const chartGranularityOptions: ChartGranularity[] = ['day', 'week', 'month']

const COLORS = {
  net: '#34d399',
  keysend: '#84cc16',
  netNegative: '#f87171',
  revenue: '#38bdf8',
  costRebalance: '#f59e0b',
  costPayment: '#fbbf24',
  cost: '#f59e0b',
  forwardVolume: '#38bdf8',
  rebalanceVolume: '#f59e0b',
  onchain: '#22c55e',
  onchainCoop: '#22c55e',
  onchainLocalForce: '#f97316',
  onchainRemoteForce: '#ef4444',
  lightning: '#fb7185',
  total: '#eab308'
}

const tooltipContentStyle = {
  background: '#0f172a',
  borderRadius: 12,
  border: '1px solid rgba(255,255,255,0.1)',
  color: '#f8fafc'
}

const tooltipLabelStyle = {
  color: '#f8fafc',
  fontWeight: 600
}

const tooltipItemStyle = {
  color: '#f8fafc'
}

const buildYAxisTicks = (maxValue: number, segments: number) => {
  if (!Number.isFinite(maxValue) || maxValue <= 0 || segments < 2) {
    return [0]
  }
  const step = maxValue / segments
  const ticks: number[] = []
  for (let idx = 0; idx <= segments; idx += 1) {
    ticks.push(step * idx)
  }
  return ticks
}

export default function Reports() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [range, setRange] = useState<RangeKey>('month')
  const [customDate, setCustomDate] = useState(() => {
    const value = new Date()
    value.setDate(value.getDate() - 1)
    const year = value.getFullYear()
    const month = String(value.getMonth() + 1).padStart(2, '0')
    const day = String(value.getDate()).padStart(2, '0')
    return `${year}-${month}-${day}`
  })
  const [customMonth, setCustomMonth] = useState('')
  const [series, setSeries] = useState<ReportSeriesItem[]>([])
  const [summary, setSummary] = useState<SummaryResponse | null>(null)
  const [live, setLive] = useState<LiveResponse | null>(null)
  // Inclusion toggles for the non-routing lines. Default on, so the page opens
  // showing everything the node actually earned and spent.
  const [includeKeysend, setIncludeKeysend] = useState(true)
  const [includeSales, setIncludeSales] = useState(true)
  // The payment cost used to be optional because it was subtracted from the
  // routing figure, and excluding it was the only way to see routing alone.
  // The formula no longer mixes them, so the choice is gone from the cards - but
  // the charts still read this flag, and pinning it to the default keeps every
  // series exactly as it renders today.
  const includePaymentCost = true
  const [movementLive, setMovementLive] = useState<MovementLiveResponse | null>(null)
  const [movementLoading, setMovementLoading] = useState(true)
  const [movementError, setMovementError] = useState('')
  const [seriesLoading, setSeriesLoading] = useState(true)
  const [seriesError, setSeriesError] = useState('')
  const [liveLoading, setLiveLoading] = useState(true)
  const [liveError, setLiveError] = useState('')
  const [configLoading, setConfigLoading] = useState(true)
  const [configSaving, setConfigSaving] = useState(false)
  const [configStatus, setConfigStatus] = useState('')
  const [liveTimeout, setLiveTimeout] = useState('')
  const [liveLookback, setLiveLookback] = useState('')
  const [runTimeout, setRunTimeout] = useState('')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [cumulativeOpen, setCumulativeOpen] = useState(false)
  const [chartGranularity, setChartGranularity] = useState<ChartGranularity>('day')
  const [includeOnchainCostInCharts, setIncludeOnchainCostInCharts] = useState(false)
  const [includeOnchainBreakdownInCharts, setIncludeOnchainBreakdownInCharts] = useState(false)
  const [useDualScaleInCumulativeChart, setUseDualScaleInCumulativeChart] = useState(false)
  const [reportsRefreshKey, setReportsRefreshKey] = useState(0)

  const formatter = useMemo(() => new Intl.NumberFormat(locale, { maximumFractionDigits: 3 }), [locale])
  const compactFormatter = useMemo(() => new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 2 }), [locale])

  const formatSats = (value: number) => `${formatter.format(value)} sats`
  const formatCompact = (value: number) => compactFormatter.format(value)
  const formatInputDate = (value: Date) => {
    const year = value.getFullYear()
    const month = String(value.getMonth() + 1).padStart(2, '0')
    const day = String(value.getDate()).padStart(2, '0')
    return `${year}-${month}-${day}`
  }
  const formatInputMonth = (value: Date) => {
    const year = value.getFullYear()
    const month = String(value.getMonth() + 1).padStart(2, '0')
    return `${year}-${month}`
  }
  const parseInputDate = (value: string) => {
    const parsed = new Date(`${value}T00:00:00`)
    if (Number.isNaN(parsed.getTime())) {
      return null
    }
    return parsed
  }
  const parseInputMonth = (value: string) => {
    const parsed = new Date(`${value}-01T00:00:00`)
    if (Number.isNaN(parsed.getTime())) {
      return null
    }
    return parsed
  }
  const shiftDays = (value: Date, days: number) => {
    const next = new Date(value)
    next.setDate(next.getDate() + days)
    return next
  }
  const startOfWeek = (value: Date) => {
    const next = new Date(value)
    const weekDay = next.getDay()
    const offset = weekDay === 0 ? -6 : 1 - weekDay
    next.setDate(next.getDate() + offset)
    return new Date(next.getFullYear(), next.getMonth(), next.getDate())
  }
  const startOfMonth = (value: Date) => new Date(value.getFullYear(), value.getMonth(), 1)
  const endOfMonth = (value: Date) => new Date(value.getFullYear(), value.getMonth() + 1, 0)
  const startOfYear = (value: Date) => new Date(value.getFullYear(), 0, 1)
  const endOfYear = (value: Date) => new Date(value.getFullYear(), 11, 31)
  const clampEndDate = (start: Date, candidate: Date) => {
    if (candidate.getTime() < start.getTime()) {
      return start
    }
    return candidate
  }

  const quickRanges = useMemo<Record<QuickRangeKey, { label: string } & DateWindow>>(() => {
    const today = new Date()
    const yesterday = shiftDays(today, -1)
    const currentMonthStart = startOfMonth(today)
    const previousMonthStart = startOfMonth(new Date(today.getFullYear(), today.getMonth() - 1, 1))
    const previousMonthEnd = endOfMonth(previousMonthStart)
    const currentYearStart = startOfYear(today)
    const previousYearStart = startOfYear(new Date(today.getFullYear() - 1, 0, 1))
    const previousYearEnd = endOfYear(previousYearStart)
    const monthLabel = new Intl.DateTimeFormat(locale, { month: 'long', year: 'numeric' })
    const currentMonthEnd = clampEndDate(currentMonthStart, yesterday)
    const currentYearEnd = clampEndDate(currentYearStart, yesterday)

    return {
      'prev-month': {
        label: monthLabel.format(previousMonthStart),
        from: formatInputDate(previousMonthStart),
        to: formatInputDate(previousMonthEnd)
      },
      'current-month': {
        label: monthLabel.format(currentMonthStart),
        from: formatInputDate(currentMonthStart),
        to: formatInputDate(currentMonthEnd)
      },
      'prev-year': {
        label: String(previousYearStart.getFullYear()),
        from: formatInputDate(previousYearStart),
        to: formatInputDate(previousYearEnd)
      },
      'current-year': {
        label: String(currentYearStart.getFullYear()),
        from: formatInputDate(currentYearStart),
        to: formatInputDate(currentYearEnd)
      }
    }
  }, [locale])

  const customRangeWindow = useMemo<DateWindow | null>(() => {
    if (range === 'date') {
      return { from: customDate, to: customDate }
    }
    if (range === 'month' && customMonth) {
      const parsed = parseInputMonth(customMonth) ?? new Date()
      const start = startOfMonth(parsed)
      const end = endOfMonth(start)
      const yesterday = shiftDays(new Date(), -1)
      const cappedEnd = end.getTime() > yesterday.getTime() ? clampEndDate(start, yesterday) : end
      return { from: formatInputDate(start), to: formatInputDate(cappedEnd) }
    }
    if (range === 'prev-month' || range === 'current-month' || range === 'prev-year' || range === 'current-year') {
      const selected = quickRanges[range]
      return { from: selected.from, to: selected.to }
    }
    return null
  }, [customDate, customMonth, quickRanges, range])

  const rangeLabel = (value: RangeKey) => {
    if (value === 'prev-month' || value === 'current-month' || value === 'prev-year' || value === 'current-year') {
      return quickRanges[value].label
    }
    return t(`reports.range.${value}`)
  }
  const isTodaySelection = (dateValue: string, serverToday?: string) => {
    if (serverToday && serverToday.trim()) {
      return dateValue === serverToday
    }
    return dateValue === formatInputDate(new Date())
  }
  const parseChartNumber = (value: number | string) => {
    if (typeof value === 'number') return value
    const raw = String(value).trim()
    if (!raw) return 0
    const inParens = raw.startsWith('(') && raw.endsWith(')')
    const cleaned = raw
      .replace(/^\(/, '')
      .replace(/\)$/, '')
      .replace(/\u2212/g, '-')
      .replace(/,/g, '')
      .replace(/\s/g, '')
      .replace(/[^\d.-]/g, '')
    const parsed = Number(cleaned)
    if (Number.isNaN(parsed)) return 0
    return inParens ? -parsed : parsed
  }

  const formatDateLabel = (value: string, includeYear = false) => {
    const parsed = parseInputDate(value)
    if (!parsed) {
      return value
    }
    return parsed.toLocaleDateString(locale, {
      month: 'short',
      day: 'numeric',
      ...(includeYear ? { year: 'numeric' } : {})
    })
  }

  const formatMonthLabel = (value: string) => {
    const parsed = parseInputDate(value)
    if (!parsed) {
      return value
    }
    return parsed.toLocaleDateString(locale, { month: 'short', year: 'numeric' })
  }

  const formatRangeLabel = (startValue: string, endValue: string, granularity: ChartGranularity, includeYear = false) => {
    if (granularity === 'month') {
      return formatMonthLabel(startValue)
    }
    if (granularity === 'week') {
      const start = formatDateLabel(startValue, includeYear)
      const end = formatDateLabel(endValue, includeYear)
      return start === end ? start : `${start} - ${end}`
    }
    return formatDateLabel(startValue, includeYear)
  }

  const formatDateLong = (value: string) => {
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) {
      return value
    }
    return parsed.toLocaleString(locale, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit'
    })
  }

  const formatTimeOnly = (value: string) => {
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) {
      return value
    }
    return parsed.toLocaleTimeString(locale, {
      hour: '2-digit',
      minute: '2-digit'
    })
  }

  const formatLiveWindow = (start: string, end: string) => {
    const startDate = new Date(start)
    const endDate = new Date(end)
    if (Number.isNaN(startDate.getTime()) || Number.isNaN(endDate.getTime())) {
      return t('reports.liveRange')
    }
    const sameDay = startDate.toDateString() === endDate.toDateString()
    if (sameDay) {
      return `${formatTimeOnly(start)} - ${formatTimeOnly(end)}`
    }
    const startLabel = formatDateLong(start)
    const endLabel = formatDateLong(end)
    return `${startLabel} - ${endLabel}`
  }

  useEffect(() => {
    if (!includeOnchainCostInCharts) {
      if (includeOnchainBreakdownInCharts) {
        setIncludeOnchainBreakdownInCharts(false)
      }
      if (useDualScaleInCumulativeChart) {
        setUseDualScaleInCumulativeChart(false)
      }
    }
  }, [includeOnchainBreakdownInCharts, includeOnchainCostInCharts, useDualScaleInCumulativeChart])

  useEffect(() => {
    const selectedToday = range === 'date' && isTodaySelection(customDate, movementLive?.date)
    if (selectedToday) {
      setSeriesError('')
      if (!live) {
        setSeriesLoading(true)
        setSeries([])
        setSummary(null)
      }
      return
    }

    let active = true
    setSeriesLoading(true)
    setSeriesError('')

    const rangeRequest = customRangeWindow
      ? getReportsCustom(customRangeWindow.from, customRangeWindow.to)
      : getReportsRange(range as BaseRangeKey)
    const summaryRequest = customRangeWindow
      ? getReportsSummaryCustom(customRangeWindow.from, customRangeWindow.to)
      : getReportsSummary(range as BaseRangeKey)

    Promise.all([rangeRequest, summaryRequest])
      .then(([rangeResp, summaryResp]) => {
        if (!active) return
        const typedRange = rangeResp as SeriesResponse
        const typedSummary = summaryResp as SummaryResponse
        setSeries(Array.isArray(typedRange.series) ? typedRange.series : [])
        setSummary(typedSummary)
      })
      .catch((err) => {
        if (!active) return
        setSeriesError(err instanceof Error ? err.message : t('reports.unavailable'))
        setSeries([])
        setSummary(null)
      })
      .finally(() => {
        if (!active) return
        setSeriesLoading(false)
      })

    return () => {
      active = false
    }
  }, [customRangeWindow, movementLive?.date, range, reportsRefreshKey, t])

  useEffect(() => {
    const refresh = () => setReportsRefreshKey((value) => value + 1)
    window.addEventListener('reports:reconciled', refresh)
    return () => window.removeEventListener('reports:reconciled', refresh)
  }, [])

  useEffect(() => {
    const selectedToday = range === 'date' && isTodaySelection(customDate, movementLive?.date)
    if (!selectedToday || !live) return

    const liveMetrics: ReportMetrics = {
      forward_fee_revenue_sats: live.forward_fee_revenue_sats,
      rebalance_fee_cost_sats: live.rebalance_fee_cost_sats,
      payment_fee_cost_sats: live.payment_fee_cost_sats ?? 0,
      onchain_fee_cost_sats: live.onchain_fee_cost_sats ?? 0,
      onchain_coop_close_cost_sats: live.onchain_coop_close_cost_sats ?? 0,
      onchain_local_force_cost_sats: live.onchain_local_force_cost_sats ?? 0,
      onchain_remote_force_cost_sats: live.onchain_remote_force_cost_sats ?? 0,
      offchain_fee_cost_sats: live.offchain_fee_cost_sats ?? (live.total_fee_cost_sats ?? ((live.rebalance_fee_cost_sats ?? 0) + (live.payment_fee_cost_sats ?? 0))),
      keysend_received_sats: live.keysend_received_sats ?? 0,
      keysend_received_count: live.keysend_received_count ?? 0,
      total_fee_cost_sats: live.total_fee_cost_sats ?? ((live.rebalance_fee_cost_sats ?? 0) + (live.payment_fee_cost_sats ?? 0)),
      total_fee_cost_with_onchain_sats: live.total_fee_cost_with_onchain_sats ?? ((live.total_fee_cost_sats ?? ((live.rebalance_fee_cost_sats ?? 0) + (live.payment_fee_cost_sats ?? 0))) + (live.onchain_fee_cost_sats ?? 0)),
      net_routing_profit_sats: live.net_routing_profit_sats,
      net_with_keysend_sats: live.net_with_keysend_sats ?? ((live.net_routing_profit_sats ?? 0) + (live.keysend_received_sats ?? 0)),
      forward_count: live.forward_count,
      rebalance_count: live.rebalance_count,
      rebalance_volume_sats: live.rebalance_volume_sats ?? 0,
      payment_count: live.payment_count ?? 0,
      routed_volume_sats: live.routed_volume_sats,
      onchain_balance_sats: live.onchain_balance_sats ?? null,
      lightning_balance_sats: live.lightning_balance_sats ?? null,
      total_balance_sats: live.total_balance_sats ?? null
    }

    setSeries([{
      date: customDate,
      forward_fee_revenue_sats: live.forward_fee_revenue_sats,
      rebalance_fee_cost_sats: live.rebalance_fee_cost_sats,
      payment_fee_cost_sats: live.payment_fee_cost_sats ?? 0,
      onchain_fee_cost_sats: live.onchain_fee_cost_sats ?? 0,
      onchain_coop_close_cost_sats: live.onchain_coop_close_cost_sats ?? 0,
      onchain_local_force_cost_sats: live.onchain_local_force_cost_sats ?? 0,
      onchain_remote_force_cost_sats: live.onchain_remote_force_cost_sats ?? 0,
      offchain_fee_cost_sats: live.offchain_fee_cost_sats ?? (live.total_fee_cost_sats ?? ((live.rebalance_fee_cost_sats ?? 0) + (live.payment_fee_cost_sats ?? 0))),
      keysend_received_sats: live.keysend_received_sats ?? 0,
      keysend_received_count: live.keysend_received_count ?? 0,
      total_fee_cost_sats: live.total_fee_cost_sats ?? ((live.rebalance_fee_cost_sats ?? 0) + (live.payment_fee_cost_sats ?? 0)),
      total_fee_cost_with_onchain_sats: live.total_fee_cost_with_onchain_sats ?? ((live.total_fee_cost_sats ?? ((live.rebalance_fee_cost_sats ?? 0) + (live.payment_fee_cost_sats ?? 0))) + (live.onchain_fee_cost_sats ?? 0)),
      net_routing_profit_sats: live.net_routing_profit_sats,
      net_with_keysend_sats: live.net_with_keysend_sats ?? ((live.net_routing_profit_sats ?? 0) + (live.keysend_received_sats ?? 0)),
      forward_count: live.forward_count,
      rebalance_count: live.rebalance_count,
      rebalance_volume_sats: live.rebalance_volume_sats ?? 0,
      payment_count: live.payment_count ?? 0,
      routed_volume_sats: live.routed_volume_sats,
      onchain_balance_sats: live.onchain_balance_sats ?? null,
      lightning_balance_sats: live.lightning_balance_sats ?? null,
      total_balance_sats: live.total_balance_sats ?? null
    }])
    setSummary({
      range: 'custom',
      timezone: live.timezone,
      days: 1,
      totals: liveMetrics,
      averages: liveMetrics,
      movement_target_sats: movementLive?.outbound_target_sats ?? 0,
      movement_pct: movementLive?.movement_pct ?? 0
    })
    setSeriesError('')
    setSeriesLoading(false)
  }, [customDate, live, movementLive, range])

  useEffect(() => {
    let active = true
    const loadLive = () => {
      setLiveLoading(true)
      setLiveError('')
      getReportsLive()
        .then((data) => {
          if (!active) return
          setLive(data as LiveResponse)
        })
        .catch((err) => {
          if (!active) return
          setLiveError(err instanceof Error ? err.message : t('reports.liveUnavailable'))
        })
        .finally(() => {
          if (!active) return
          setLiveLoading(false)
        })
    }

    loadLive()
    const timer = window.setInterval(loadLive, 60000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    let active = true
    const loadMovement = () => {
      setMovementLoading(true)
      setMovementError('')
      getReportsMovementLive()
        .then((data) => {
          if (!active) return
          setMovementLive(data as MovementLiveResponse)
        })
        .catch((err) => {
          if (!active) return
          setMovementError(err instanceof Error ? err.message : t('reports.dailyMovementUnavailable'))
        })
        .finally(() => {
          if (!active) return
          setMovementLoading(false)
        })
    }
    loadMovement()
    const timer = window.setInterval(loadMovement, 60000)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [t])

  useEffect(() => {
    let active = true
    setConfigLoading(true)
    getReportsConfig()
      .then((data) => {
        if (!active) return
        const cfg = data as ReportsConfig
        setLiveTimeout(cfg.live_timeout_sec ? String(cfg.live_timeout_sec) : '')
        setLiveLookback(cfg.live_lookback_hours ? String(cfg.live_lookback_hours) : '')
        setRunTimeout(cfg.run_timeout_sec ? String(cfg.run_timeout_sec) : '')
      })
      .catch(() => {
        if (!active) return
      })
      .finally(() => {
        if (!active) return
        setConfigLoading(false)
      })

    return () => {
      active = false
    }
  }, [])

  const handleSaveConfig = async () => {
    setConfigSaving(true)
    setConfigStatus('')
    const payload: ReportsConfig = {
      live_timeout_sec: liveTimeout ? Number(liveTimeout) : null,
      live_lookback_hours: liveLookback ? Number(liveLookback) : null,
      run_timeout_sec: runTimeout ? Number(runTimeout) : null
    }
    try {
      await updateReportsConfig(payload)
      setConfigStatus(t('reports.settings.saved'))
    } catch (err) {
      setConfigStatus(err instanceof Error ? err.message : t('reports.settings.saveFailed'))
    } finally {
      setConfigSaving(false)
    }
  }

  const rawChartData = useMemo(() => {
    const mapped = series.map((item) => ({
      date: item.date,
      // Recomposed below from the parts, so the inclusion toggles change the
      // series the same way they change the totals. Reading the stored net would
      // make the chart disagree with the numbers right above it.
      net: item.net_routing_profit_sats,
      netRouting: item.net_routing_profit_sats,
      keysend: item.keysend_received_sats ?? 0,
      sales: item.sales_revenue_sats ?? 0,
      markedRevenue: item.marked_revenue_sats ?? 0,
      markedCost: item.marked_cost_sats ?? 0,
      keysendSent: item.keysend_sent_sats ?? 0,
      totalRevenue: item.total_revenue_sats
        ?? ((item.forward_fee_revenue_sats ?? 0) + (item.keysend_received_sats ?? 0)
            + (item.sales_revenue_sats ?? 0) + (item.marked_revenue_sats ?? 0)),
      revenue: item.forward_fee_revenue_sats,
      rebalanceCost: item.rebalance_fee_cost_sats ?? 0,
      paymentCost: item.payment_fee_cost_sats ?? 0,
      forwardVolume: item.routed_volume_sats ?? 0,
      rebalanceVolume: item.rebalance_volume_sats ?? 0,
      offchainCost: item.offchain_fee_cost_sats ?? item.total_fee_cost_sats ?? ((item.rebalance_fee_cost_sats ?? 0) + (item.payment_fee_cost_sats ?? 0)),
      onchainCost: item.onchain_fee_cost_sats ?? 0,
      onchainCoopCloseCost: item.onchain_coop_close_cost_sats ?? 0,
      onchainLocalForceCost: item.onchain_local_force_cost_sats ?? 0,
      onchainRemoteForceCost: item.onchain_remote_force_cost_sats ?? 0,
      costWithOnchain: item.total_fee_cost_with_onchain_sats ?? ((item.offchain_fee_cost_sats ?? item.total_fee_cost_sats ?? ((item.rebalance_fee_cost_sats ?? 0) + (item.payment_fee_cost_sats ?? 0))) + (item.onchain_fee_cost_sats ?? 0)),
      onchain: item.onchain_balance_sats ?? null,
      lightning: item.lightning_balance_sats ?? null,
      total: item.total_balance_sats ?? null
    }))
    return mapped.sort((a, b) => a.date.localeCompare(b.date))
  }, [series])

  const showYearInChartLabels = range === 'all'

  // One definition of "net" for the whole page: routing, plus the non-routing
  // income that is switched on, plus the payment cost added back when it is
  // switched off (routing net already has it subtracted).
  // Spelled out so the chart never claims to show something it does not: the
  // title used to say "routing + keysend" no matter which toggles were on.
  // The net column is now revenue minus cost in one bar, so the separate
  // keysend slice it used to carry is gone.
  // The line is every revenue minus every cost, so listing the parts would be a
  // sentence. It names what it is, and only mentions what a toggle is leaving
  // out - the exception is what the reader needs to know.
  const netChartTitle = (() => {
    const excluded: string[] = []
    if (!includeKeysend) excluded.push(t('reports.netPartKeysend'))
    if (!includeSales) excluded.push(t('reports.netPartSales'))
    const base = t('reports.netTotalLabel')
    return excluded.length > 0 ? `${base} · ${t('reports.netExcluding')} ${excluded.join(', ')}` : base
  })()

  // The net line is every revenue minus every cost, the same definition the
  // cards use. It used to be routing plus whatever the toggles let through,
  // which made the chart and the cards answer different questions.
  const composeNet = (item: {
    revenue: number; keysend: number; sales: number; markedRevenue: number
    rebalanceCost: number; paymentCost: number; keysendSent: number; markedCost: number
    onchainCost: number
  }) => {
    const revenue = item.revenue
      + (includeKeysend ? item.keysend : 0)
      + (includeSales ? item.sales : 0)
      + item.markedRevenue
    const cost = item.rebalanceCost + item.paymentCost + item.keysendSent + item.markedCost
      + (includeOnchainCostInCharts ? item.onchainCost : 0)
    return revenue - cost
  }

  const chartData = useMemo<ChartDataPoint[]>(() => {
    if (rawChartData.length === 0) {
      return []
    }
    if (chartGranularity === 'day') {
      return rawChartData.map((item) => ({
        ...item,
        net: composeNet(item),
        startDate: item.date,
        endDate: item.date,
        label: formatRangeLabel(item.date, item.date, 'day', showYearInChartLabels)
      }))
    }

    const grouped = new Map<string, ChartDataPoint>()
    for (const item of rawChartData) {
      const currentDate = parseInputDate(item.date)
      if (!currentDate) {
        continue
      }

      const bucketDate = chartGranularity === 'week' ? startOfWeek(currentDate) : startOfMonth(currentDate)
      const bucketKey = formatInputDate(bucketDate)
      const current = grouped.get(bucketKey)

      if (!current) {
        grouped.set(bucketKey, {
          ...item,
          date: bucketKey,
          startDate: item.date,
          endDate: item.date,
          label: ''
        })
        continue
      }

      current.netRouting += item.netRouting
      current.keysend += item.keysend
      current.sales += item.sales
      current.revenue += item.revenue
      current.rebalanceCost += item.rebalanceCost
      current.paymentCost += item.paymentCost
      current.forwardVolume += item.forwardVolume
      current.rebalanceVolume += item.rebalanceVolume
      current.offchainCost += item.offchainCost
      current.onchainCost += item.onchainCost
      current.onchainCoopCloseCost += item.onchainCoopCloseCost
      current.onchainLocalForceCost += item.onchainLocalForceCost
      current.onchainRemoteForceCost += item.onchainRemoteForceCost
      current.costWithOnchain += item.costWithOnchain
      current.endDate = item.date
      if (item.onchain !== null) current.onchain = item.onchain
      if (item.lightning !== null) current.lightning = item.lightning
      if (item.total !== null) current.total = item.total
    }

    return Array.from(grouped.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, item]) => ({
        ...item,
        // Recomposed after the parts are summed, not accumulated: summing a net
        // that was already composed per day would apply the toggles twice.
        net: composeNet(item),
        date: key,
        label: formatRangeLabel(item.startDate, item.endDate, chartGranularity, showYearInChartLabels)
      }))
  }, [chartGranularity, locale, rawChartData, showYearInChartLabels, includeKeysend, includeSales, includePaymentCost, includeOnchainCostInCharts])

  const cumulativeCostData = useMemo(() => {
    let cumulativeRevenue = 0
    let cumulativeOffchain = 0
    let cumulativeOnchain = 0
    let cumulativeOnchainCoop = 0
    let cumulativeOnchainLocalForce = 0
    let cumulativeOnchainRemoteForce = 0
    return chartData.map((item) => {
      cumulativeRevenue += item.revenue
      cumulativeOffchain += item.offchainCost
      cumulativeOnchain += item.onchainCost
      cumulativeOnchainCoop += item.onchainCoopCloseCost
      cumulativeOnchainLocalForce += item.onchainLocalForceCost
      cumulativeOnchainRemoteForce += item.onchainRemoteForceCost
      return {
        label: item.label,
        cumulativeRevenue,
        cumulativeOffchain,
        cumulativeOnchain,
        cumulativeOnchainCoop,
        cumulativeOnchainLocalForce,
        cumulativeOnchainRemoteForce
      }
    })
  }, [chartData])
  const dualScaleEnabled = includeOnchainCostInCharts && useDualScaleInCumulativeChart

  const cumulativeSingleAxisTicks = useMemo(() => {
    if (cumulativeCostData.length === 0) {
      return [0]
    }
    let maxValue = 0
    for (const item of cumulativeCostData) {
      maxValue = Math.max(maxValue, item.cumulativeRevenue, item.cumulativeOffchain)
      if (includeOnchainCostInCharts) {
        maxValue = Math.max(maxValue, item.cumulativeOnchain)
        if (includeOnchainBreakdownInCharts) {
          maxValue = Math.max(
            maxValue,
            item.cumulativeOnchainCoop,
            item.cumulativeOnchainLocalForce,
            item.cumulativeOnchainRemoteForce
          )
        }
      }
    }
    return buildYAxisTicks(maxValue, 22)
  }, [cumulativeCostData, includeOnchainBreakdownInCharts, includeOnchainCostInCharts])

  const cumulativeRightAxisTicks = useMemo(() => {
    if (cumulativeCostData.length === 0) {
      return [0]
    }
    let maxValue = 0
    for (const item of cumulativeCostData) {
      maxValue = Math.max(maxValue, item.cumulativeRevenue, item.cumulativeOffchain)
    }
    return buildYAxisTicks(maxValue, 22)
  }, [cumulativeCostData])

  const cumulativeLeftAxisTicks = useMemo(() => {
    if (cumulativeCostData.length === 0 || !includeOnchainCostInCharts) {
      return [0]
    }
    let maxValue = 0
    for (const item of cumulativeCostData) {
      maxValue = Math.max(maxValue, item.cumulativeOnchain)
      if (includeOnchainBreakdownInCharts) {
        maxValue = Math.max(
          maxValue,
          item.cumulativeOnchainCoop,
          item.cumulativeOnchainLocalForce,
          item.cumulativeOnchainRemoteForce
        )
      }
    }
    return buildYAxisTicks(maxValue, 22)
  }, [cumulativeCostData, includeOnchainBreakdownInCharts, includeOnchainCostInCharts])

  const liveChartData = useMemo(() => {
    if (!live) return []
    const revenueValue = parseChartNumber(live.forward_fee_revenue_sats)
    const rebalanceCostValue = parseChartNumber(live.rebalance_fee_cost_sats ?? 0)
    // Zeroed rather than filtered so the bar simply disappears from the cost
    // column while the other series keep their positions.
    const paymentCostValue = includePaymentCost
      ? parseChartNumber(live.payment_fee_cost_sats ?? 0)
      : 0
    const keysendValue = includeKeysend ? parseChartNumber(live.keysend_received_sats ?? 0) : 0
    const salesValue = includeSales ? parseChartNumber(live.sales_revenue_sats ?? 0) : 0
    const markedRevenueValue = parseChartNumber(live.marked_revenue_sats ?? 0)
    const keysendSentValue = parseChartNumber(live.keysend_sent_sats ?? 0)
    const markedCostValue = parseChartNumber(live.marked_cost_sats ?? 0)
    const onchainCostValue = includeOnchainCostInCharts ? parseChartNumber(live.onchain_fee_cost_sats ?? 0) : 0
    // One consolidated revenue bar against the full cost stack, matching the
    // cards. The net column is revenue minus cost, not routing plus extras.
    const totalRevenueValue = revenueValue + keysendValue + salesValue + markedRevenueValue
    const totalCostValue = rebalanceCostValue + paymentCostValue + keysendSentValue + markedCostValue + onchainCostValue
    const netValue = totalRevenueValue - totalCostValue
    const empty = {
      totalRevenue: 0, rebalanceCost: 0, paymentCost: 0, keysendSent: 0, markedCost: 0,
      onchainCost: 0, netRouting: 0, netRoutingColor: undefined as string | undefined
    }
    return [
      { name: t('reports.revenue'), ...empty, totalRevenue: totalRevenueValue },
      {
        name: t('reports.cost'),
        ...empty,
        rebalanceCost: rebalanceCostValue,
        paymentCost: paymentCostValue,
        keysendSent: keysendSentValue,
        markedCost: markedCostValue,
        onchainCost: onchainCostValue
      },
      {
        name: t('reports.net'),
        ...empty,
        netRouting: netValue,
        netRoutingColor: netValue < 0 ? COLORS.netNegative : COLORS.net
      }
    ]
  }, [live, t, includeKeysend, includeSales, includePaymentCost, includeOnchainCostInCharts])

  const balancePoints = useMemo(
    () => chartData
      .filter((item) => item.onchain !== null || item.lightning !== null || item.total !== null)
      .map((item) => {
        const onchain = item.onchain
        const lightning = item.lightning
        const total = item.total ?? (onchain !== null && lightning !== null ? onchain + lightning : null)
        return { ...item, onchain, lightning, total }
      }),
    [chartData]
  )
  const balanceChangeData = useMemo<BalanceChangeDataPoint[]>(
    () => balancePoints.map((item, index) => {
      const previous = index > 0 ? balancePoints[index - 1] : null
      return {
        label: item.label,
        onchain: item.onchain,
        lightning: item.lightning,
        total: item.total,
        onchainChange: previous && item.onchain !== null && previous.onchain !== null ? item.onchain - previous.onchain : null,
        lightningChange: previous && item.lightning !== null && previous.lightning !== null ? item.lightning - previous.lightning : null,
        totalChange: previous && item.total !== null && previous.total !== null ? item.total - previous.total : null
      }
    }),
    [balancePoints]
  )
  const latestBalance = balancePoints.length > 0 ? balancePoints[balancePoints.length - 1] : null
  const firstBalance = balancePoints[0] ?? null
  const balancePeriodChange = {
    onchain: latestBalance?.onchain !== null && latestBalance?.onchain !== undefined && firstBalance?.onchain !== null && firstBalance?.onchain !== undefined
      ? latestBalance.onchain - firstBalance.onchain
      : null,
    lightning: latestBalance?.lightning !== null && latestBalance?.lightning !== undefined && firstBalance?.lightning !== null && firstBalance?.lightning !== undefined
      ? latestBalance.lightning - firstBalance.lightning
      : null,
    total: latestBalance?.total !== null && latestBalance?.total !== undefined && firstBalance?.total !== null && firstBalance?.total !== undefined
      ? latestBalance.total - firstBalance.total
      : null
  }
  const hasBalances = balancePoints.length > 0
  const movementPct = movementLive?.movement_pct ?? 0
  const hasMovementTarget = (movementLive?.outbound_target_sats ?? 0) > 0
  const movementProgress = Math.max(0, Math.min(100, movementPct))
  const movementTone = !hasMovementTarget ? 'bg-slate-500' : movementPct >= 75 ? 'bg-emerald-500' : movementPct >= 50 ? 'bg-amber-400' : 'bg-rose-500'
  const summaryMovementPct = summary?.movement_pct ?? 0
  const summaryMovementTarget = summary?.movement_target_sats ?? 0
  const summaryMovementTone = summaryMovementPct >= 75 ? 'bg-emerald-500' : summaryMovementPct >= 50 ? 'bg-amber-400' : 'bg-rose-500'
  const liveRebalanceCost = live?.rebalance_fee_cost_sats ?? 0
  const livePaymentCost = live?.payment_fee_cost_sats ?? 0
  const liveOffchainCost = live?.offchain_fee_cost_sats ?? live?.total_fee_cost_sats ?? (liveRebalanceCost + livePaymentCost)
  const liveOnchainCost = live?.onchain_fee_cost_sats ?? 0
  const liveTotalCostWithOnchain = live?.total_fee_cost_with_onchain_sats ?? (liveOffchainCost + liveOnchainCost)
  const liveKeysendReceived = live?.keysend_received_sats ?? 0
  const liveNetWithKeysend = live?.net_with_keysend_sats ?? ((live?.net_routing_profit_sats ?? 0) + liveKeysendReceived)
  const liveSalesRevenue = live?.sales_revenue_sats ?? 0
  const liveKeysendSent = live?.keysend_sent_sats ?? 0
  const liveMarkedRevenue = live?.marked_revenue_sats ?? 0
  const liveMarkedCost = live?.marked_cost_sats ?? 0
  const liveForwardRevenue = live?.forward_fee_revenue_sats ?? 0
  // Served by the API so the definition of revenue and cost lives in one place.
  const liveTotalRevenue = live?.total_revenue_sats
    ?? (liveForwardRevenue + liveKeysendReceived + liveSalesRevenue)
  const liveTotalCost = live?.total_cost_sats
    ?? (liveRebalanceCost + livePaymentCost + liveKeysendSent + liveOnchainCost)
  // Routing asks one question: is the node profitable at forwarding? Only
  // rebalances belong against it.
  const liveRoutingNet = liveForwardRevenue - liveRebalanceCost
  const liveNetTotal = live?.net_total_sats ?? (liveTotalRevenue - liveTotalCost)
  // Two APYs over two different bases: routing earns on the liquidity sitting in
  // channels, the node as a whole earns on everything it holds.
  const liveOutboundBase = live?.lightning_balance_sats ?? null
  const liveTotalBase = totalBalanceFromPoint(live ?? null)
  // The live window is intraday, so it is annualised linearly - see
  // calculateShortWindowApyPct for why compounding it is not an option.
  const liveWindowDays = 1
  const liveRoutingApy = calculateShortWindowApyPct(liveRoutingNet, liveWindowDays, liveOutboundBase)
  const liveTotalApy = calculateShortWindowApyPct(liveNetTotal, liveWindowDays, liveTotalBase)
  // The toggle drives the historical totals too, so its visibility cannot depend
  // on the live window alone: on a day with no sale the control would vanish
  // while still silently including past sales in the range below.
  const hasSalesData =
    liveSalesRevenue > 0 ||
    (live?.sales_count ?? 0) > 0 ||
    (summary?.totals.sales_revenue_sats ?? 0) > 0 ||
    (summary?.totals.sales_count ?? 0) > 0
  // The three toggles let the operator read the report as pure routing, or with
  // the non-routing income and the payment cost folded in. All on by default:
  // the honest view is the complete one.
  const liveOtherRevenue =
    (includeKeysend ? liveKeysendReceived : 0) + (includeSales ? liveSalesRevenue : 0)
  // Routing net already has the payment cost subtracted, so excluding it means
  // adding it back rather than skipping a subtraction.
  const liveNetWithExtras =
    (live?.net_routing_profit_sats ?? 0) +
    liveOtherRevenue +
    (includePaymentCost ? 0 : livePaymentCost)
  // Totals and averages share the live card's structure, so the same period reads
  // the same way whether it is the window on the left or the range on the right.
  // Only lines with a value are listed.
  const summarySection = (m: any, apy: number | null) => {
    const forwards = m?.forward_fee_revenue_sats ?? 0
    const keysendIn = m?.keysend_received_sats ?? 0
    const sales = m?.sales_revenue_sats ?? 0
    const markedIn = m?.marked_revenue_sats ?? 0
    const rebalances = m?.rebalance_fee_cost_sats ?? 0
    const payments = m?.payment_fee_cost_sats ?? 0
    const keysendOut = m?.keysend_sent_sats ?? 0
    const markedOut = m?.marked_cost_sats ?? 0
    const onchain = m?.onchain_fee_cost_sats ?? 0
    const revenue = m?.total_revenue_sats ?? (forwards + keysendIn + sales + markedIn)
    const cost = m?.total_cost_sats ?? (rebalances + payments + keysendOut + markedOut + onchain)
    const routing = forwards - rebalances
    const net = m?.net_total_sats ?? (revenue - cost)
    return {
      revenueLines: [
        { key: 'forwards', label: t('reports.forwards'), value: forwards },
        { key: 'keysend', label: t('reports.keysendReceived'), value: keysendIn },
        { key: 'sales', label: t('reports.salesRevenue'), value: sales },
        { key: 'marked', label: t('reports.markedRevenue'), value: markedIn }
      ].filter((line) => line.value !== 0),
      costLines: [
        { key: 'rebalances', label: t('reports.rebalances'), value: rebalances },
        { key: 'payments', label: t('reports.payments'), value: payments },
        { key: 'keysendSent', label: t('reports.keysendSent'), value: keysendOut },
        { key: 'marked', label: t('reports.markedCost'), value: markedOut },
        { key: 'onchain', label: t('reports.onchainCost'), value: onchain }
      ].filter((line) => line.value !== 0),
      revenue,
      cost,
      routing,
      net,
      apy
    }
  }

  const summaryTotalsOffchainCost = summary?.totals.offchain_fee_cost_sats ?? summary?.totals.total_fee_cost_sats ?? ((summary?.totals.rebalance_fee_cost_sats ?? 0) + (summary?.totals.payment_fee_cost_sats ?? 0))
  const summaryTotalsOnchainCost = summary?.totals.onchain_fee_cost_sats ?? 0
  const summaryTotalsCostWithOnchain = summary?.totals.total_fee_cost_with_onchain_sats ?? (summaryTotalsOffchainCost + summaryTotalsOnchainCost)
  // Built from the parts rather than from the stored net so the inclusion
  // toggles apply to history the same way they apply to the live window.
  const summaryTotalsKeysend = summary?.totals.keysend_received_sats ?? 0
  const summaryTotalsSales = summary?.totals.sales_revenue_sats ?? 0
  const summaryTotalsPaymentCost = summary?.totals.payment_fee_cost_sats ?? 0
  const summaryTotalsOtherRevenue =
    (includeKeysend ? summaryTotalsKeysend : 0) + (includeSales ? summaryTotalsSales : 0)
  const summaryTotalsNetWithKeysend =
    (summary?.totals.net_routing_profit_sats ?? 0) +
    summaryTotalsOtherRevenue +
    (includePaymentCost ? 0 : summaryTotalsPaymentCost)
  const summaryTotalsNetWithOnchain = summaryTotalsNetWithKeysend - summaryTotalsOnchainCost
  const summaryDays = summary?.days ?? series.length
  const summaryRevenue = summary?.totals.forward_fee_revenue_sats ?? 0
  const summaryCapitalBase = totalBalanceFromPoint(live)
  const summaryUsesRevenueBaseApy = range === 'prev-year' || range === 'current-year'
  const summaryApy = calculateDisplayApyPct(summaryTotalsNetWithKeysend, summaryRevenue, summaryDays, summaryCapitalBase, summaryUsesRevenueBaseApy)
  const summaryOnchainApy = calculateDisplayApyPct(summaryTotalsNetWithOnchain, summaryRevenue, summaryDays, summaryCapitalBase, summaryUsesRevenueBaseApy)
  const summaryAveragesOffchainCost = summary?.averages.offchain_fee_cost_sats ?? summary?.averages.total_fee_cost_sats ?? ((summary?.averages.rebalance_fee_cost_sats ?? 0) + (summary?.averages.payment_fee_cost_sats ?? 0))
  const summaryAveragesOnchainCost = summary?.averages.onchain_fee_cost_sats ?? 0
  const summaryAveragesCostWithOnchain = summary?.averages.total_fee_cost_with_onchain_sats ?? (summaryAveragesOffchainCost + summaryAveragesOnchainCost)
  const summaryAveragesOtherRevenue =
    (includeKeysend ? summary?.averages.keysend_received_sats ?? 0 : 0) +
    (includeSales ? summary?.averages.sales_revenue_sats ?? 0 : 0)
  const summaryAveragesNetWithKeysend =
    (summary?.averages.net_routing_profit_sats ?? 0) +
    summaryAveragesOtherRevenue +
    (includePaymentCost ? 0 : summary?.averages.payment_fee_cost_sats ?? 0)
  const summaryAveragesNetWithOnchain = summaryAveragesNetWithKeysend - summaryAveragesOnchainCost
  const currentMonthInput = formatInputMonth(new Date())
  const currentMonthNumber = currentMonthInput.slice(5)
  const selectedMonthParts = useMemo(() => {
    const parsed = parseInputMonth(customMonth) ?? new Date()
    return {
      year: parsed.getFullYear(),
      month: String(parsed.getMonth() + 1).padStart(2, '0')
    }
  }, [customMonth])
  const monthOptions = useMemo(
    () => Array.from({ length: 12 }, (_, index) => {
      const month = String(index + 1).padStart(2, '0')
      const label = new Intl.DateTimeFormat(locale, { month: 'long' }).format(new Date(2026, index, 1))
      return { month, label }
    }),
    [locale]
  )
  const yearOptions = useMemo(() => {
    const currentYear = new Date().getFullYear()
    const years: number[] = []
    for (let year = currentYear; year >= 2009; year -= 1) {
      years.push(year)
    }
    if (!years.includes(selectedMonthParts.year)) {
      years.push(selectedMonthParts.year)
      years.sort((a, b) => b - a)
    }
    return years
  }, [selectedMonthParts.year])
  const updateCustomMonth = (month: string, year = selectedMonthParts.year) => {
    if (!month) {
      setCustomMonth('')
      return
    }
    const nextMonth = `${year}-${month}`
    setCustomMonth(nextMonth > currentMonthInput ? currentMonthInput : nextMonth)
  }
  const formatSignedSats = (value: number | null) => {
    if (value === null) return t('common.na')
    return value > 0 ? `+${formatSats(value)}` : formatSats(value)
  }
  const balanceChangeTone = (value: number | null) => {
    if (value === null) return 'text-fog/50'
    if (value > 0) return 'text-emerald-300'
    if (value < 0) return 'text-rose-300'
    return 'text-fog/60'
  }
  const renderChartStatus = (hasData: boolean, emptyMessage = t('reports.noData')) => {
    if (seriesLoading && !hasData) {
      return <p className="text-sm text-fog/60">{t('reports.loadingRange')}</p>
    }
    if (seriesError) {
      return <p className="text-sm text-brass">{seriesError}</p>
    }
    if (!hasData) {
      return <p className="text-sm text-fog/60">{emptyMessage}</p>
    }
    return null
  }
  const renderGranularityToggle = () => (
    <div className="inline-flex items-center gap-1 rounded-full bg-white/10 p-1">
      {chartGranularityOptions.map((option) => (
        <button
          key={option}
          type="button"
          className={`rounded-full px-2.5 py-1 text-xs ${chartGranularity === option ? 'bg-white/20 text-fog' : 'text-fog/70 hover:text-fog'}`}
          onClick={() => setChartGranularity(option)}
        >
          {t(`reports.granularity.${option}`)}
        </button>
      ))}
    </div>
  )

  return (
    <section className="space-y-6">
      <div className="section-card flex flex-wrap items-center justify-between gap-4">
        <div>
          <h2 className="text-2xl font-semibold">{t('reports.title')}</h2>
          <p className="text-fog/60">{t('reports.subtitle')}</p>
        </div>
        <span className="text-xs uppercase tracking-wide text-fog/60">{t('reports.updatedDaily')}</span>
      </div>

      <div className="grid gap-6 lg:grid-cols-5">
        <div className="section-card lg:col-span-3 flex flex-col gap-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">{t('reports.liveResults')}</h3>
            <span className="rounded-full bg-white/10 px-3 py-1 text-xs font-semibold uppercase tracking-wide text-fog">
              {live ? `${t('reports.liveWindow')}: ${formatLiveWindow(live.start, live.end)}` : t('reports.liveRange')}
            </span>
          </div>
          {liveLoading && !live && <p className="text-sm text-fog/60">{t('reports.loadingLive')}</p>}
          {liveError && <p className="text-sm text-brass">{liveError}</p>}
          {!liveLoading && !liveError && live && (
            <div className="flex flex-1 flex-col gap-4">
              {/* Three sections instead of four tiles: what came in, what it cost,
                  and what was left. The old layout put forwards alone under
                  "Revenue" and the rest under "Other revenue", which made the
                  two questions - how much did the node earn, and how much did it
                  keep - both hard to answer at a glance. */}
              <div className="grid gap-3 lg:grid-cols-3">
                <div className="rounded-2xl bg-white/5 p-4">
                  <p className="text-xs uppercase tracking-wide" style={{ color: COLORS.net }}>
                    {t('reports.revenue')}
                  </p>
                  {/* Only lines with a value are shown. A column of zeroes reads
                      as noise and buries the one number that moved. */}
                  <div className="mt-2 space-y-1 text-sm text-fog/70">
                    {[
                      { key: 'forwards', label: t('reports.forwards'), value: liveForwardRevenue },
                      { key: 'keysend', label: t('reports.keysendReceived'), value: liveKeysendReceived },
                      {
                        key: 'sales',
                        label: `${t('reports.salesRevenue')} (${t('reports.salesCount', { count: live.sales_count ?? 0 })})`,
                        value: liveSalesRevenue
                      },
                      { key: 'marked', label: t('reports.markedRevenue'), value: liveMarkedRevenue }
                    ].filter((line) => line.value !== 0).map((line) => (
                      <div key={line.key} className="flex justify-between gap-3">
                        <span>{line.label}</span>
                        <span className="text-fog">{formatSats(line.value)}</span>
                      </div>
                    ))}
                  </div>
                  <div className="mt-2 flex justify-between gap-3 border-t border-white/10 pt-2 text-sm">
                    <span className="text-fog/60">{t('reports.total')}</span>
                    <span className="font-semibold" style={{ color: COLORS.net }}>
                      {formatSats(liveTotalRevenue)}
                    </span>
                  </div>
                </div>

                <div className="rounded-2xl bg-white/5 p-4">
                  <p className="text-xs uppercase tracking-wide" style={{ color: COLORS.netNegative }}>
                    {t('reports.cost')}
                  </p>
                  <div className="mt-2 space-y-1 text-sm text-fog/70">
                    {[
                      { key: 'rebalances', label: t('reports.rebalances'), value: liveRebalanceCost },
                      { key: 'payments', label: t('reports.payments'), value: livePaymentCost },
                      { key: 'keysendSent', label: t('reports.keysendSent'), value: liveKeysendSent },
                      { key: 'marked', label: t('reports.markedCost'), value: liveMarkedCost },
                      { key: 'onchain', label: t('reports.onchainCost'), value: liveOnchainCost }
                    ].filter((line) => line.value !== 0).map((line) => (
                      <div key={line.key} className="flex justify-between gap-3">
                        <span>{line.label}</span>
                        <span className="text-fog">{formatSats(line.value)}</span>
                      </div>
                    ))}
                  </div>
                  <div className="mt-2 flex justify-between gap-3 border-t border-white/10 pt-2 text-sm">
                    <span className="text-fog/60">{t('reports.total')}</span>
                    <span className="font-semibold" style={{ color: COLORS.netNegative }}>
                      {formatSats(liveTotalCost)}
                    </span>
                  </div>
                </div>

                {/* Colour follows the sign across the whole line, so a losing
                    period reads as one at a glance rather than needing the
                    number to be parsed first. */}
                <div className="rounded-2xl bg-white/5 p-4">
                  <p className="text-xs uppercase tracking-wide text-fog/60">{t('reports.net')}</p>
                  <div className="mt-2 space-y-3 text-sm">
                    <div style={{ color: liveRoutingNet >= 0 ? COLORS.net : COLORS.netNegative }}>
                      <div className="flex justify-between gap-3">
                        <span>{t('reports.routingNet')}</span>
                        <span className="font-semibold">{formatSats(liveRoutingNet)}</span>
                      </div>
                      <p className="truncate text-xs opacity-80" title={t('reports.apyOverOutbound')}>
                        {t('reports.apy')} {formatApyPercent(locale, liveRoutingApy)}
                      </p>
                    </div>
                    <div style={{ color: liveNetTotal >= 0 ? COLORS.net : COLORS.netNegative }}>
                      <div className="flex justify-between gap-3">
                        <span>{t('reports.total')}</span>
                        <span className="font-semibold">{formatSats(liveNetTotal)}</span>
                      </div>
                      <p className="truncate text-xs opacity-80" title={t('reports.apyOverTotal')}>
                        {t('reports.apy')} {formatApyPercent(locale, liveTotalApy)}
                      </p>
                    </div>
                  </div>
                </div>
              </div>
              <div className="grid gap-3 sm:grid-cols-4 text-sm text-fog/70">
                <div className="flex items-center justify-between rounded-2xl bg-white/5 px-4 py-3">
                  <span>{t('reports.forwardCount')}</span>
                  <span className="text-fog">{formatter.format(live.forward_count)}</span>
                </div>
                <div className="flex items-center justify-between rounded-2xl bg-white/5 px-4 py-3">
                  <span>{t('reports.rebalanceCount')}</span>
                  <span className="text-fog">{formatter.format(live.rebalance_count)}</span>
                </div>
                <div className="flex items-center justify-between rounded-2xl bg-white/5 px-4 py-3">
                  <span>{t('reports.paymentCount')}</span>
                  <span className="text-fog">{formatter.format(live.payment_count ?? 0)}</span>
                </div>
                <div className="flex items-center justify-between rounded-2xl bg-white/5 px-4 py-3">
                  <span>{t('reports.keysendCount')}</span>
                  <span className="text-fog">{formatter.format(live.keysend_received_count ?? 0)}</span>
                </div>
              </div>
              <div className="min-h-[220px] flex-1">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={liveChartData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                    <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                    <XAxis dataKey="name" tick={{ fill: '#cbd5f5', fontSize: 12 }} axisLine={false} tickLine={false} />
                    <YAxis tick={{ fill: '#cbd5f5', fontSize: 11 }} axisLine={false} tickLine={false} tickFormatter={formatCompact} />
                    <Tooltip
                      shared={false}
                      cursor={{ fill: 'rgba(255,255,255,0.06)' }}
                      contentStyle={tooltipContentStyle}
                      labelStyle={tooltipLabelStyle}
                      itemStyle={tooltipItemStyle}
                      formatter={(value) => formatSats(Number(value))}
                    />
                    <Legend verticalAlign="top" height={24} formatter={(value) => <span className="text-xs text-fog/60">{value}</span>} />
                    <Bar dataKey="totalRevenue" stackId="live" name={t('reports.revenue')} fill={COLORS.revenue} radius={[8, 8, 8, 8]} />
                    <Bar dataKey="rebalanceCost" stackId="live" name={t('reports.rebalances')} fill={COLORS.costRebalance} radius={[8, 8, 8, 8]} />
                    <Bar dataKey="paymentCost" stackId="live" name={t('reports.payments')} fill={COLORS.costPayment} radius={[8, 8, 8, 8]} />
                    <Bar dataKey="keysendSent" stackId="live" name={t('reports.keysendSent')} fill={COLORS.keysend} radius={[8, 8, 8, 8]} />
                    <Bar dataKey="markedCost" stackId="live" name={t('reports.markedCost')} fill={COLORS.netNegative} radius={[8, 8, 8, 8]} />
                    {includeOnchainCostInCharts && (
                      <Bar dataKey="onchainCost" stackId="live" name={t('reports.onchainCost')} fill={COLORS.onchain} radius={[8, 8, 8, 8]} />
                    )}
                    {/* Carries revenue minus cost, so it is the node total - the
                        key name is historical and the label must not repeat it. */}
                    <Bar dataKey="netRouting" stackId="live" name={t('reports.netTotalLabel')} fill={COLORS.net} radius={[8, 8, 8, 8]}>
                      {liveChartData.map((entry) => (
                        <Cell key={entry.name} fill={entry.netRoutingColor ?? COLORS.net} />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>
              <p className="text-xs text-fog/50">{t('reports.lastUpdated', { time: formatDateLong(live.end) })}</p>
            </div>
          )}
        </div>

        <div className="section-card lg:col-span-2 space-y-4">
          <h3 className="text-lg font-semibold">{t('reports.historicalRange')}</h3>
          <div className="flex flex-wrap gap-2">
            {primaryRangeOptions.map((key) => (
              <button
                key={key}
                type="button"
                className={range === key ? 'btn-primary' : 'btn-secondary'}
                onClick={() => setRange(key)}
              >
                {rangeLabel(key)}
              </button>
            ))}
          </div>
          <div className="flex flex-wrap gap-2">
            {calendarRangeOptions.map((key) => (
              <button
                key={key}
                type="button"
                className={range === key ? 'btn-primary' : 'btn-secondary'}
                onClick={() => setRange(key)}
              >
                {rangeLabel(key)}
              </button>
            ))}
          </div>
          {range === 'date' && (
            <label className="block text-sm text-fog/70">
              {t('reports.selectDate')}
              <input
                className="input-field mt-2"
                type="date"
                value={customDate}
                onChange={(e) => setCustomDate(e.target.value)}
              />
            </label>
          )}
          {range === 'month' && (
            <label className="block text-sm text-fog/70">
              {t('reports.selectMonth')}
              <div className="mt-2 grid gap-2 sm:grid-cols-[auto_minmax(0,1fr)_8rem]">
                <button
                  type="button"
                  className={customMonth ? 'btn-secondary' : 'btn-primary'}
                  onClick={() => setCustomMonth('')}
                >
                  {t('reports.last30Days')}
                </button>
                <select
                  className="input-field"
                  value={customMonth ? selectedMonthParts.month : ''}
                  onChange={(e) => updateCustomMonth(e.target.value)}
                >
                  <option value="" disabled>{t('reports.selectMonthPlaceholder')}</option>
                  {monthOptions.map((option) => (
                    <option
                      key={option.month}
                      value={option.month}
                      disabled={selectedMonthParts.year === new Date().getFullYear() && option.month > currentMonthNumber}
                    >
                      {option.label}
                    </option>
                  ))}
                </select>
                <select
                  className="input-field"
                  value={selectedMonthParts.year}
                  onChange={(e) => updateCustomMonth(selectedMonthParts.month, Number(e.target.value))}
                >
                  {yearOptions.map((year) => (
                    <option key={year} value={year}>{year}</option>
                  ))}
                </select>
              </div>
            </label>
          )}
          {seriesLoading && series.length === 0 && <p className="text-sm text-fog/60">{t('reports.loadingRange')}</p>}
          {seriesError && <p className="text-sm text-brass">{seriesError}</p>}
          {!seriesLoading && !seriesError && summary && (
            <div className="space-y-3 text-sm text-fog/70">
              {[
                { key: 'totals', title: t('reports.totals'), data: summarySection(summary.totals, summaryApy) },
                { key: 'averages', title: t('reports.averagesPerDay'), data: summarySection(summary.averages, null) }
              ].map(({ key, title, data }) => (
                <div key={key} className="rounded-2xl bg-white/5 px-4 py-3">
                  <p className="text-xs uppercase tracking-wide text-fog/50">{title}</p>

                  <p className="mt-2 text-xs uppercase tracking-wide" style={{ color: COLORS.net }}>
                    {t('reports.revenue')}
                  </p>
                  {data.revenueLines.map((line) => (
                    <p key={line.key} className="flex justify-between gap-3 text-fog/70">
                      <span>{line.label}</span>
                      <span className="text-fog">{formatSats(line.value)}</span>
                    </p>
                  ))}
                  <p className="flex justify-between gap-3 border-t border-white/10 pt-1">
                    <span className="text-fog/60">{t('reports.total')}</span>
                    <span className="font-semibold" style={{ color: COLORS.net }}>{formatSats(data.revenue)}</span>
                  </p>

                  <p className="mt-3 text-xs uppercase tracking-wide" style={{ color: COLORS.netNegative }}>
                    {t('reports.cost')}
                  </p>
                  {data.costLines.map((line) => (
                    <p key={line.key} className="flex justify-between gap-3 text-fog/70">
                      <span>{line.label}</span>
                      <span className="text-fog">{formatSats(line.value)}</span>
                    </p>
                  ))}
                  <p className="flex justify-between gap-3 border-t border-white/10 pt-1">
                    <span className="text-fog/60">{t('reports.total')}</span>
                    <span className="font-semibold" style={{ color: COLORS.netNegative }}>{formatSats(data.cost)}</span>
                  </p>

                  <p className="mt-3 text-xs uppercase tracking-wide text-fog/50">{t('reports.net')}</p>
                  <p
                    className="flex justify-between gap-3"
                    style={{ color: data.routing >= 0 ? COLORS.net : COLORS.netNegative }}
                  >
                    <span>{t('reports.routingNet')}</span>
                    <span className="font-semibold">{formatSats(data.routing)}</span>
                  </p>
                  <p
                    className="flex justify-between gap-3"
                    style={{ color: data.net >= 0 ? COLORS.net : COLORS.netNegative }}
                  >
                    <span>
                      {t('reports.total')}
                      {data.apy !== null ? (
                        <span className="ml-2 text-xs opacity-80">
                          {t('reports.apy')} {formatApyPercent(locale, data.apy)}
                        </span>
                      ) : null}
                    </span>
                    <span className="font-semibold">{formatSats(data.net)}</span>
                  </p>
                </div>
              ))}
              <div className="rounded-2xl bg-white/5 px-4 py-3">
                <p className="text-xs uppercase tracking-wide text-fog/50">{t('reports.activity')}</p>
                <p className="text-fog">{t('reports.forwards')} {formatter.format(summary.totals.forward_count)}</p>
                <p className="text-fog">{t('reports.rebalances')} {formatter.format(summary.totals.rebalance_count)}</p>
                <p className="text-fog">{t('reports.payments')} {formatter.format(summary.totals.payment_count ?? 0)}</p>
                <p className="text-fog">{t('reports.keysendCount')} {formatter.format(summary.totals.keysend_received_count ?? 0)}</p>
                <p className="text-fog">{t('reports.routedVolume')} {formatSats(summary.totals.routed_volume_sats)}</p>
                {summaryMovementTarget > 0 ? (
                  <p className="mt-1 flex items-center gap-2 text-fog">
                    <span className={`inline-block h-2.5 w-2.5 rounded-full ${summaryMovementTone}`} />
                    <span>
                      {t('reports.movementProgress', {
                        routed: formatSats(summary.totals.routed_volume_sats),
                        target: formatSats(summaryMovementTarget),
                        percent: formatter.format(summaryMovementPct)
                      })}
                    </span>
                  </p>
                ) : (
                  <p className="mt-1 text-fog/70">{t('reports.movementProgressUnavailable')}</p>
                )}
              </div>
              <p className="text-xs text-fog/50">{t('reports.basedOnDays', { count: summary.days })}</p>
            </div>
          )}
        </div>
      </div>

      <div className="section-card space-y-4">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('reports.dailyMovementGoal')}</h3>
          <span className="text-xs text-fog/60">{t('reports.liveRange')}</span>
        </div>
        {movementLoading && !movementLive && <p className="text-sm text-fog/60">{t('reports.loadingMovement')}</p>}
        {movementError && <p className="text-sm text-brass">{movementError}</p>}
        {!movementLoading && !movementError && movementLive && (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-fog/70">
              <span>{t('reports.routedVolume')} {formatSats(movementLive.routed_volume_sats)}</span>
              <span>{t('reports.outboundTarget')} {formatSats(movementLive.outbound_target_sats)}</span>
              <span>{t('reports.progressPct', { value: formatter.format(movementPct) })}</span>
            </div>
            <div className="h-3 w-full rounded-full bg-white/10 overflow-hidden">
              <div className={`h-full ${movementTone}`} style={{ width: `${movementProgress}%` }} />
            </div>
            {!hasMovementTarget && <p className="text-xs text-fog/50">{t('reports.movementProgressUnavailable')}</p>}
            <p className="text-xs text-fog/50">{t('reports.lastUpdated', { time: formatDateLong(movementLive.end) })}</p>
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('reports.settings.title')}</h3>
            <p className="text-fog/60">{t('reports.settings.subtitle')}</p>
          </div>
          <button
            className="btn-secondary text-xs px-3 py-2"
            type="button"
            aria-expanded={settingsOpen}
            onClick={() => setSettingsOpen((open) => !open)}
          >
            {settingsOpen ? t('common.hide') : t('reports.settings.configure')}
          </button>
        </div>
        {settingsOpen && (
          <>
            {configLoading && <p className="text-sm text-fog/60">{t('reports.settings.loading')}</p>}
            {!configLoading && (
              <div className="grid gap-4 lg:grid-cols-3">
                <div className="space-y-2">
                  <label className="text-sm text-fog/70">{t('reports.settings.liveTimeout')}</label>
                  <input
                    className="input-field"
                    type="number"
                    min={0}
                    placeholder="60"
                    value={liveTimeout}
                    onChange={(e) => setLiveTimeout(e.target.value)}
                  />
                  <p className="text-xs text-fog/50">{t('reports.settings.liveTimeoutHint')}</p>
                </div>
                <div className="space-y-2">
                  <label className="text-sm text-fog/70">{t('reports.settings.liveLookback')}</label>
                  <input
                    className="input-field"
                    type="number"
                    min={0}
                    placeholder="24"
                    value={liveLookback}
                    onChange={(e) => setLiveLookback(e.target.value)}
                  />
                  <p className="text-xs text-fog/50">{t('reports.settings.liveLookbackHint')}</p>
                </div>
                <div className="space-y-2">
                  <label className="text-sm text-fog/70">{t('reports.settings.runTimeout')}</label>
                  <input
                    className="input-field"
                    type="number"
                    min={0}
                    placeholder="300"
                    value={runTimeout}
                    onChange={(e) => setRunTimeout(e.target.value)}
                  />
                  <p className="text-xs text-fog/50">{t('reports.settings.runTimeoutHint')}</p>
                </div>
              </div>
            )}
            <div className="flex flex-wrap items-center gap-3">
              <button className="btn-primary" onClick={handleSaveConfig} disabled={configSaving}>
                {configSaving ? t('reports.settings.saving') : t('reports.settings.save')}
              </button>
              {configStatus && <span className="text-sm text-fog/60">{configStatus}</span>}
            </div>
          </>
        )}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <div className="flex items-center justify-between gap-3">
            <h3 className="text-lg font-semibold">{netChartTitle}</h3>
            {renderGranularityToggle()}
          </div>
          {renderChartStatus(chartData.length > 0) ?? (
            <div className="h-64">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                  <defs>
                    <linearGradient id="netGradient" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor={COLORS.net} stopOpacity={0.5} />
                      <stop offset="95%" stopColor={COLORS.net} stopOpacity={0.05} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                  <XAxis dataKey="label" tick={{ fill: '#cbd5f5', fontSize: 11 }} axisLine={false} tickLine={false} />
                  <YAxis tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatCompact} axisLine={false} tickLine={false} />
                  <Legend verticalAlign="top" height={24} formatter={(value) => <span className="text-xs text-fog/60">{value}</span>} />
                  {/* The cost stack has five series and most days carry only one
                      or two. Listing the empty ones turns the tooltip into a
                      column of zeroes that hides the number being hovered. */}
                  <Tooltip
                    cursor={{ stroke: 'rgba(255,255,255,0.1)', strokeWidth: 1 }}
                    contentStyle={tooltipContentStyle}
                    labelStyle={tooltipLabelStyle}
                    itemStyle={tooltipItemStyle}
                    formatter={(value, name) => (Number(value) === 0 ? [] : [formatSats(Number(value)), name])}
                    labelFormatter={(value) => String(value)}
                  />
                  <Area type="monotone" dataKey="net" name={t('reports.net')} stroke={COLORS.net} fill="url(#netGradient)" strokeWidth={2} />
                  <Line type="monotone" dataKey="keysend" name={t('reports.keysendReceived')} stroke={COLORS.keysend} strokeWidth={2} dot={false} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}
        </div>

        <div className="section-card space-y-4">
          <div className="flex items-center justify-between gap-3">
            <h3 className="text-lg font-semibold">{t('reports.revenueVsCost')}</h3>
            {renderGranularityToggle()}
          </div>
          <label className="inline-flex items-center gap-2 text-sm text-fog/70">
            <input
              type="checkbox"
              className="h-4 w-4 accent-emerald-500"
              checked={includeOnchainCostInCharts}
              onChange={(e) => setIncludeOnchainCostInCharts(e.target.checked)}
            />
            <span>{t('reports.includeOnchainCost')}</span>
          </label>
          {renderChartStatus(chartData.length > 0) ?? (
            <div className="h-64">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={chartData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                  <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                  <XAxis dataKey="label" tick={{ fill: '#cbd5f5', fontSize: 11 }} axisLine={false} tickLine={false} />
                  <YAxis tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatCompact} axisLine={false} tickLine={false} />
                  <Legend verticalAlign="top" height={24} formatter={(value) => <span className="text-xs text-fog/60">{value}</span>} />
                  {/* The cost stack has five series and most days carry only one
                      or two. Listing the empty ones turns the tooltip into a
                      column of zeroes that hides the number being hovered. */}
                  <Tooltip
                    cursor={{ stroke: 'rgba(255,255,255,0.1)', strokeWidth: 1 }}
                    contentStyle={tooltipContentStyle}
                    labelStyle={tooltipLabelStyle}
                    itemStyle={tooltipItemStyle}
                    formatter={(value, name) => (Number(value) === 0 ? [] : [formatSats(Number(value)), name])}
                    labelFormatter={(value) => String(value)}
                  />
                  {/* Revenue is one consolidated bar - forwards, keysend, sales
                      and marked payments - against the full cost stack, so the
                      two columns are comparable at a glance. */}
                  <Bar dataKey="totalRevenue" stackId="revenue" name={t('reports.revenue')} fill={COLORS.revenue} fillOpacity={0.72} radius={[6, 6, 0, 0]} />
                  <Bar dataKey="rebalanceCost" stackId="cost" name={t('reports.rebalances')} fill={COLORS.costRebalance} fillOpacity={0.7} />
                  <Bar dataKey="paymentCost" stackId="cost" name={t('reports.payments')} fill={COLORS.costPayment} fillOpacity={0.7} />
                  <Bar dataKey="keysendSent" stackId="cost" name={t('reports.keysendSent')} fill={COLORS.keysend} fillOpacity={0.7} />
                  <Bar dataKey="markedCost" stackId="cost" name={t('reports.markedCost')} fill={COLORS.netNegative} fillOpacity={0.7} radius={includeOnchainCostInCharts ? [0, 0, 0, 0] : [6, 6, 0, 0]} />
                  {includeOnchainCostInCharts && (
                    <Bar dataKey="onchainCost" stackId="cost" name={t('reports.onchainCost')} fill={COLORS.onchain} fillOpacity={0.7} radius={[6, 6, 0, 0]} />
                  )}
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}
        </div>
      </div>

      <div className="section-card space-y-4">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('reports.forwardRebalanceVolume')}</h3>
          {renderGranularityToggle()}
        </div>
        {renderChartStatus(chartData.length > 0) ?? (
          <div className="h-64">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={chartData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                <defs>
                  <linearGradient id="forwardVolumeGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={COLORS.forwardVolume} stopOpacity={0.45} />
                    <stop offset="95%" stopColor={COLORS.forwardVolume} stopOpacity={0.04} />
                  </linearGradient>
                  <linearGradient id="rebalanceVolumeGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={COLORS.rebalanceVolume} stopOpacity={0.4} />
                    <stop offset="95%" stopColor={COLORS.rebalanceVolume} stopOpacity={0.04} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                <XAxis dataKey="label" tick={{ fill: '#cbd5f5', fontSize: 11 }} axisLine={false} tickLine={false} />
                <YAxis tick={{ fill: '#cbd5f5', fontSize: 11 }} tickFormatter={formatCompact} axisLine={false} tickLine={false} />
                <Legend verticalAlign="top" height={24} formatter={(value) => <span className="text-xs text-fog/60">{value}</span>} />
                <Tooltip
                  cursor={{ stroke: 'rgba(255,255,255,0.1)', strokeWidth: 1 }}
                  contentStyle={tooltipContentStyle}
                  labelStyle={tooltipLabelStyle}
                  itemStyle={tooltipItemStyle}
                  formatter={(value) => formatSats(Number(value))}
                  labelFormatter={(value) => String(value)}
                />
                <Area type="monotone" dataKey="forwardVolume" name={t('reports.forwardVolume')} stroke={COLORS.forwardVolume} fill="url(#forwardVolumeGradient)" strokeWidth={2} dot={false} />
                <Area type="monotone" dataKey="rebalanceVolume" name={t('reports.rebalanceVolume')} stroke={COLORS.rebalanceVolume} fill="url(#rebalanceVolumeGradient)" strokeWidth={2} dot={false} />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('reports.balances')}</h3>
          <div className="flex items-center gap-3">
            <span className="text-xs text-fog/60">{t('reports.balancesSubtitle')}</span>
            {renderGranularityToggle()}
          </div>
        </div>
        {renderChartStatus(hasBalances, t('reports.balanceHistoryUnavailable')) ?? (
          <div className="space-y-4">
            {latestBalance && (
              <div className="grid gap-3 md:grid-cols-3">
                {[
                  { key: 'total', label: t('reports.total'), value: latestBalance.total, change: balancePeriodChange.total, color: COLORS.total },
                  { key: 'lightning', label: t('reports.lightning'), value: latestBalance.lightning, change: balancePeriodChange.lightning, color: COLORS.lightning },
                  { key: 'onchain', label: t('reports.onchain'), value: latestBalance.onchain, change: balancePeriodChange.onchain, color: COLORS.onchain }
                ].map((item) => (
                  <div key={item.key} className="rounded-2xl bg-white/5 px-4 py-3">
                    <p className="text-xs uppercase tracking-wide text-fog/50">{item.label}</p>
                    <p className="mt-1 text-lg font-semibold text-fog">
                      {item.value === null ? t('common.na') : formatSats(item.value)}
                    </p>
                    <p className={`mt-1 text-xs ${balanceChangeTone(item.change)}`}>
                      {t('reports.periodChange')} {formatSignedSats(item.change)}
                    </p>
                    <span className="mt-3 block h-1 rounded-full" style={{ backgroundColor: item.color }} />
                  </div>
                ))}
              </div>
            )}
            <div className="h-72">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={balanceChangeData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                  <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                  <XAxis dataKey="label" tick={{ fill: '#cbd5f5', fontSize: 11 }} axisLine={false} tickLine={false} />
                  <YAxis
                    tick={{ fill: '#cbd5f5', fontSize: 11 }}
                    tickFormatter={formatCompact}
                    axisLine={false}
                    tickLine={false}
                    tickCount={9}
                  />
                  <ReferenceLine y={0} stroke="rgba(255,255,255,0.18)" />
                  <Legend verticalAlign="top" height={24} formatter={(value) => <span className="text-xs text-fog/60">{value}</span>} />
                  <Tooltip
                    cursor={{ fill: 'rgba(255,255,255,0.06)' }}
                    contentStyle={tooltipContentStyle}
                    labelStyle={tooltipLabelStyle}
                    itemStyle={tooltipItemStyle}
                    formatter={(value) => formatSignedSats(Number(value))}
                    labelFormatter={(value) => String(value)}
                  />
                  <Bar dataKey="onchainChange" name={t('reports.onchainChange')} fill={COLORS.onchain} fillOpacity={0.72} radius={[5, 5, 0, 0]} />
                  <Bar dataKey="lightningChange" name={t('reports.lightningChange')} fill={COLORS.lightning} fillOpacity={0.72} radius={[5, 5, 0, 0]} />
                  <Bar dataKey="totalChange" name={t('reports.totalChange')} fill={COLORS.total} fillOpacity={0.72} radius={[5, 5, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('reports.cumulativeRevenueCosts')}</h3>
          <div className="flex flex-wrap items-center gap-3">
            {cumulativeOpen && (
              <>
                <label className="inline-flex items-center gap-2 text-sm text-fog/70">
                  <input
                    type="checkbox"
                    className="h-4 w-4 accent-emerald-500"
                    checked={includeOnchainCostInCharts}
                    onChange={(e) => setIncludeOnchainCostInCharts(e.target.checked)}
                  />
                  <span>{t('reports.includeOnchainCost')}</span>
                </label>
                <label className={`inline-flex items-center gap-2 text-sm ${includeOnchainCostInCharts ? 'text-fog/70' : 'text-fog/40'}`}>
                  <input
                    type="checkbox"
                    className="h-4 w-4 accent-emerald-500"
                    checked={includeOnchainBreakdownInCharts}
                    disabled={!includeOnchainCostInCharts}
                    onChange={(e) => setIncludeOnchainBreakdownInCharts(e.target.checked)}
                  />
                  <span>{t('reports.includeOnchainBreakdown')}</span>
                </label>
                <label className={`inline-flex items-center gap-2 text-sm ${includeOnchainCostInCharts ? 'text-fog/70' : 'text-fog/40'}`}>
                  <input
                    type="checkbox"
                    className="h-4 w-4 accent-emerald-500"
                    checked={useDualScaleInCumulativeChart}
                    disabled={!includeOnchainCostInCharts}
                    onChange={(e) => setUseDualScaleInCumulativeChart(e.target.checked)}
                  />
                  <span>{t('reports.dualScale')}</span>
                </label>
                {renderGranularityToggle()}
              </>
            )}
            <button
              className="btn-secondary text-xs px-3 py-2"
              type="button"
              aria-expanded={cumulativeOpen}
              onClick={() => setCumulativeOpen((open) => !open)}
            >
              {cumulativeOpen ? t('reports.hideChart') : t('reports.showChart')}
            </button>
          </div>
        </div>
        {cumulativeOpen && (
          renderChartStatus(cumulativeCostData.length > 0) ?? (
          <div className="h-[60rem]">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={cumulativeCostData} margin={{ top: 10, right: 10, left: 0, bottom: 10 }}>
                <defs>
                  <linearGradient id="cumulativeRevenueGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={COLORS.revenue} stopOpacity={0.35} />
                    <stop offset="95%" stopColor={COLORS.revenue} stopOpacity={0.04} />
                  </linearGradient>
                  <linearGradient id="cumulativeOffchainGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={COLORS.cost} stopOpacity={0.35} />
                    <stop offset="95%" stopColor={COLORS.cost} stopOpacity={0.04} />
                  </linearGradient>
                  <linearGradient id="cumulativeOnchainGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={COLORS.onchain} stopOpacity={0.35} />
                    <stop offset="95%" stopColor={COLORS.onchain} stopOpacity={0.04} />
                  </linearGradient>
                  <linearGradient id="cumulativeOnchainCoopGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={COLORS.onchainCoop} stopOpacity={0.3} />
                    <stop offset="95%" stopColor={COLORS.onchainCoop} stopOpacity={0.03} />
                  </linearGradient>
                  <linearGradient id="cumulativeOnchainLocalForceGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={COLORS.onchainLocalForce} stopOpacity={0.3} />
                    <stop offset="95%" stopColor={COLORS.onchainLocalForce} stopOpacity={0.03} />
                  </linearGradient>
                  <linearGradient id="cumulativeOnchainRemoteForceGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor={COLORS.onchainRemoteForce} stopOpacity={0.3} />
                    <stop offset="95%" stopColor={COLORS.onchainRemoteForce} stopOpacity={0.03} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="rgba(255,255,255,0.08)" vertical={false} />
                <XAxis dataKey="label" tick={{ fill: '#cbd5f5', fontSize: 11 }} axisLine={false} tickLine={false} />
                {dualScaleEnabled ? (
                  <>
                    <YAxis
                      yAxisId="left"
                      orientation="left"
                      tick={{ fill: '#cbd5f5', fontSize: 11 }}
                      tickFormatter={formatCompact}
                      axisLine={false}
                      tickLine={false}
                      ticks={cumulativeLeftAxisTicks}
                    />
                    <YAxis
                      yAxisId="right"
                      orientation="right"
                      tick={{ fill: '#cbd5f5', fontSize: 11 }}
                      tickFormatter={formatCompact}
                      axisLine={false}
                      tickLine={false}
                      ticks={cumulativeRightAxisTicks}
                    />
                  </>
                ) : (
                  <YAxis
                    yAxisId="single"
                    tick={{ fill: '#cbd5f5', fontSize: 11 }}
                    tickFormatter={formatCompact}
                    axisLine={false}
                    tickLine={false}
                    ticks={cumulativeSingleAxisTicks}
                  />
                )}
                <Legend verticalAlign="top" height={24} formatter={(value) => <span className="text-xs text-fog/60">{value}</span>} />
                <Tooltip
                  cursor={{ stroke: 'rgba(255,255,255,0.1)', strokeWidth: 1 }}
                  contentStyle={tooltipContentStyle}
                  labelStyle={tooltipLabelStyle}
                  itemStyle={tooltipItemStyle}
                  formatter={(value) => formatSats(Number(value))}
                  labelFormatter={(value) => String(value)}
                />
                <Area
                  type="monotone"
                  dataKey="cumulativeRevenue"
                  name={t('reports.revenue')}
                  yAxisId={dualScaleEnabled ? 'right' : 'single'}
                  stroke={COLORS.revenue}
                  fill="url(#cumulativeRevenueGradient)"
                  strokeWidth={2}
                  dot={false}
                />
                <Area
                  type="monotone"
                  dataKey="cumulativeOffchain"
                  name={t('reports.offchainCost')}
                  yAxisId={dualScaleEnabled ? 'right' : 'single'}
                  stroke={COLORS.cost}
                  fill="url(#cumulativeOffchainGradient)"
                  strokeWidth={2}
                  dot={false}
                />
                {includeOnchainCostInCharts && (
                  <Area
                    type="monotone"
                    dataKey="cumulativeOnchain"
                    name={t('reports.onchainCost')}
                    yAxisId={dualScaleEnabled ? 'left' : 'single'}
                    stroke={COLORS.onchain}
                    fill="url(#cumulativeOnchainGradient)"
                    strokeWidth={2}
                    dot={false}
                  />
                )}
                {includeOnchainCostInCharts && includeOnchainBreakdownInCharts && (
                  <Area
                    type="monotone"
                    dataKey="cumulativeOnchainCoop"
                    name={t('reports.cooperativeCloseCost')}
                    yAxisId={dualScaleEnabled ? 'left' : 'single'}
                    stroke={COLORS.onchainCoop}
                    fill="url(#cumulativeOnchainCoopGradient)"
                    strokeWidth={2}
                    dot={false}
                  />
                )}
                {includeOnchainCostInCharts && includeOnchainBreakdownInCharts && (
                  <Area
                    type="monotone"
                    dataKey="cumulativeOnchainLocalForce"
                    name={t('reports.localForceCloseCost')}
                    yAxisId={dualScaleEnabled ? 'left' : 'single'}
                    stroke={COLORS.onchainLocalForce}
                    fill="url(#cumulativeOnchainLocalForceGradient)"
                    strokeWidth={2}
                    dot={false}
                  />
                )}
                {includeOnchainCostInCharts && includeOnchainBreakdownInCharts && (
                  <Area
                    type="monotone"
                    dataKey="cumulativeOnchainRemoteForce"
                    name={t('reports.remoteForceCloseCost')}
                    yAxisId={dualScaleEnabled ? 'left' : 'single'}
                    stroke={COLORS.onchainRemoteForce}
                    fill="url(#cumulativeOnchainRemoteForceGradient)"
                    strokeWidth={2}
                    dot={false}
                  />
                )}
              </AreaChart>
            </ResponsiveContainer>
          </div>
        ))}
      </div>
    </section>
  )
}
