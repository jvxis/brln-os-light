import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { getLnChannelDetail, saveLnChannelNote, saveLnPeerNote } from '../api'
import { getLocale } from '../i18n'

export type ChannelDetailInitialChannel = {
  channel_point: string
  channel_id?: number
  channel_id_str?: string
  remote_pubkey?: string
  peer_alias?: string
  initiator?: boolean
  active?: boolean
  inactive_since_unix?: number
  inactive_duration_sec?: number
  chan_status_flags?: string
  local_disabled?: boolean
  private?: boolean
  capacity_sat?: number
  local_balance_sat?: number
  remote_balance_sat?: number
  local_chan_reserve_sat?: number
  unsettled_balance_sat?: number
  pending_htlc_count?: number
  pending_htlcs?: ChannelPendingHtlc[]
  base_fee_msat?: number
  fee_rate_ppm?: number
  inbound_base_msat?: number
  inbound_fee_rate_ppm?: number
  peer_fee_rate_ppm?: number
  peer_base_msat?: number
  class_label?: string
  out_ppm7d?: number
  rebal_ppm7d?: number
  forward_fee_7d_sat?: number
  rebal_fee_7d_sat?: number
  profit_fee_7d_sat?: number
  movement_7d?: ChannelMovement7d
}

type ChannelMovement7d = {
  forward_count?: number
  forward_amount_sat?: number
  forward_in_count?: number
  forward_in_amount_sat?: number
  forward_out_count?: number
  forward_out_amount_sat?: number
  rebalance_count?: number
  rebalance_amount_sat?: number
  rebalance_in_count?: number
  rebalance_in_amount_sat?: number
  rebalance_out_count?: number
  rebalance_out_amount_sat?: number
  lightning_out_count?: number
  lightning_out_amount_sat?: number
  lightning_in_count?: number
  lightning_in_amount_sat?: number
}

type ChannelPendingHtlc = {
  incoming?: boolean
  peer_alias?: string
  amount_sat?: number
  expiration_height?: number
  htlc_index?: number
  forwarding_channel_id?: number
  locked_in?: boolean
}

type ChannelPeer = {
  pub_key?: string
  alias?: string
  address?: string
  inbound?: boolean
  bytes_sent?: number
  bytes_recv?: number
  sat_sent?: number
  sat_recv?: number
  ping_time?: number
  sync_type?: string
  last_error?: string
  last_error_time?: number
}

type ChannelDetailSettings = {
  autofee_enabled?: boolean
  autofee_configured?: boolean
  autofee_last_ppm?: number
  autofee_last_inbound_ppm?: number
  autofee_last_at?: string
  autofee_last_direction?: string
  autofee_class_label?: string
  autofee_stalled_rounds?: number
  rebalance_configured?: boolean
  rebalance_auto_enabled?: boolean
  manual_restart_enabled?: boolean
  target_outbound_pct?: number
  use_default_econ_ratio?: boolean
  econ_ratio_override?: number
  auto_bypass_cost_gate?: boolean
  excluded_as_source?: boolean
}

type ChannelDetailPeriod = {
  period?: string
  days?: number
  forward_count?: number
  forward_in_count?: number
  forward_out_count?: number
  forward_in_amount_sat?: number
  forward_out_amount_sat?: number
  rebalance_in_count?: number
  rebalance_out_count?: number
  rebalance_in_amount_sat?: number
  rebalance_out_amount_sat?: number
  revenue_sat?: number
  cost_sat?: number
  profit_sat?: number
  out_ppm?: number
  rebalance_ppm?: number
  apy?: number
}

type ChannelDetailFeeLog = {
  captured_at?: string
  side?: string
  old_fee_rate_ppm?: number
  new_fee_rate_ppm?: number
  old_base_msat?: number
  new_base_msat?: number
  old_inbound_fee_rate_ppm?: number
  new_inbound_fee_rate_ppm?: number
  old_disabled?: boolean
  new_disabled?: boolean
  change_pct?: number
}

type ChannelDetailForward = {
  occurred_at?: string
  payment_hash?: string
  chan_id_in?: number
  chan_id_out?: number
  chan_in_alias?: string
  chan_out_alias?: string
  amount_in_sat?: number
  amount_out_sat?: number
  fee_sat?: number
  ppm?: number
  status?: string
}

type ChannelDetailRebalance = {
  occurred_at?: string
  status?: string
  direction?: string
  source_channel_id?: number
  target_channel_id?: number
  source_alias?: string
  target_alias?: string
  source_point?: string
  target_point?: string
  amount_sat?: number
  fee_sat?: number
  ppm?: number
  payment_hash?: string
  memo?: string
}

type ChannelDetailPayment = {
  occurred_at?: string
  type?: string
  status?: string
  amount_sat?: number
  fee_sat?: number
  payment_hash?: string
  memo?: string
  channel_alias?: string
}

type ChannelDetailFailure = {
  occurred_at?: string
  source?: string
  incoming_channel_id?: string
  incoming_alias?: string
  outgoing_channel_id?: string
  outgoing_alias?: string
  amount_sat?: number
  potential_fee_sat?: number
  failure_code?: string
  failure_detail?: string
  payment_hash?: string
}

type ChannelDetailPeerEvent = {
  occurred_at?: string
  side?: string
  setting?: string
  old_value?: string
  new_value?: string
}

type ChannelPreviousNote = {
  channel_point?: string
  channel_id?: number
  short_channel_id?: string
  peer_alias?: string
  note?: string
  updated_at?: string
}

type ChannelDetailCoverage = {
  notifications_since?: string
  notifications_until?: string
  policy_since?: string
  policy_until?: string
}

type ChannelDetailResponse = {
  channel?: ChannelDetailInitialChannel
  peer?: ChannelPeer
  current_block_height?: number
  short_channel_id?: string
  open_block_height?: number
  opened_confirmations?: number
  settings?: ChannelDetailSettings
  periods?: ChannelDetailPeriod[]
  fee_logs?: ChannelDetailFeeLog[]
  routed?: ChannelDetailForward[]
  rebalances?: ChannelDetailRebalance[]
  sent?: ChannelDetailPayment[]
  received?: ChannelDetailPayment[]
  failed_htlcs?: ChannelDetailFailure[]
  peer_events?: ChannelDetailPeerEvent[]
  note?: string
  channel_note?: string
  peer_note?: string
  previous_channel_notes?: ChannelPreviousNote[]
  coverage?: ChannelDetailCoverage
  data_source_warnings?: string[]
  pending_htlcs?: ChannelPendingHtlc[]
}

type TabID = 'overview' | 'economics' | 'policy' | 'activity' | 'failures' | 'notes'

type Props = {
  open: boolean
  channelPoint: string
  initialChannel?: ChannelDetailInitialChannel | null
  onClose: () => void
}

const DETAIL_LIMIT = 40

const asNumber = (value: unknown, fallback = 0) => {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  return fallback
}

const finiteNumber = (value: unknown) => (typeof value === 'number' && Number.isFinite(value) ? value : undefined)

const shortMiddle = (value?: string, start = 12, end = 8) => {
  const text = String(value || '').trim()
  if (!text) return ''
  if (text.length <= start + end + 3) return text
  return `${text.slice(0, start)}...${text.slice(-end)}`
}

const toneClass = (tone: 'green' | 'blue' | 'amber' | 'red' | 'muted') => {
  switch (tone) {
    case 'green':
      return 'border-glow/35 bg-glow/10 text-glow'
    case 'blue':
      return 'border-sky-300/30 bg-sky-500/10 text-sky-100'
    case 'amber':
      return 'border-brass/35 bg-brass/10 text-brass'
    case 'red':
      return 'border-rose-300/35 bg-rose-500/10 text-rose-100'
    default:
      return 'border-white/10 bg-white/5 text-fog/65'
  }
}

function Icon({ name }: { name: 'close' | 'refresh' | 'save' | 'note' | 'activity' }) {
  const paths = {
    close: <><path d="M6 6l12 12" /><path d="M18 6L6 18" /></>,
    refresh: <><path d="M20 6v5h-5" /><path d="M4 18v-5h5" /><path d="M19 11a7 7 0 0 0-12-4l-3 3" /><path d="M5 13a7 7 0 0 0 12 4l3-3" /></>,
    save: <><path d="M5 5h12l2 2v12H5z" /><path d="M8 5v6h8" /><path d="M8 19v-5h8v5" /></>,
    note: <><path d="M6 4h9l3 3v13H6z" /><path d="M14 4v4h4" /><path d="M9 12h6" /><path d="M9 16h6" /></>,
    activity: <><path d="M4 13h4l3-7 4 12 2-5h3" /></>,
  }[name]
  return (
    <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {paths}
    </svg>
  )
}

function Badge({ children, tone = 'muted' }: { children: ReactNode; tone?: 'green' | 'blue' | 'amber' | 'red' | 'muted' }) {
  return (
    <span className={`inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-[11px] font-medium ${toneClass(tone)}`}>
      {children}
    </span>
  )
}

function MetricCard({ label, value, detail, tone = 'muted' }: { label: string; value: ReactNode; detail?: ReactNode; tone?: 'green' | 'blue' | 'amber' | 'red' | 'muted' }) {
  return (
    <div className={`min-h-[86px] rounded-2xl border p-3 ${toneClass(tone)}`}>
      <p className="text-[10px] font-medium uppercase tracking-[0.12em] opacity-70">{label}</p>
      <div className="mt-2 text-lg font-semibold leading-tight text-fog">{value}</div>
      {detail && <div className="mt-1 text-[11px] leading-snug opacity-75">{detail}</div>}
    </div>
  )
}

function InfoRow({ label, value, mono = false }: { label: string; value: ReactNode; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[118px_minmax(0,1fr)] gap-3 border-t border-white/5 py-2 first:border-t-0">
      <dt className="text-[11px] uppercase tracking-[0.12em] text-fog/45">{label}</dt>
      <dd className={`min-w-0 text-sm text-fog/85 ${mono ? 'break-all font-mono text-xs' : ''}`}>{value}</dd>
    </div>
  )
}

function TablePanel({ title, subtitle, empty, emptyLabel, children, heightClass = 'max-h-[260px]' }: { title: string; subtitle?: string; empty: boolean; emptyLabel: string; children: ReactNode; heightClass?: string }) {
  return (
    <div className="min-h-0 overflow-hidden rounded-2xl border border-white/10 bg-ink/45">
      <div className="flex items-start justify-between gap-3 border-b border-white/10 px-4 py-3">
        <div>
          <h4 className="text-sm font-semibold text-fog">{title}</h4>
          {subtitle && <p className="mt-0.5 text-xs text-fog/50">{subtitle}</p>}
        </div>
      </div>
      {empty ? (
        <div className="px-4 py-8 text-center text-sm text-fog/45">{emptyLabel}</div>
      ) : (
        <div className={`${heightClass} overflow-auto`}>
          {children}
        </div>
      )}
    </div>
  )
}

export default function ChannelDetailModal({ open, channelPoint, initialChannel, onClose }: Props) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const compactFormatter = useMemo(() => new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }), [locale])
  const dateFormatter = useMemo(() => new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }), [locale])

  const [detail, setDetail] = useState<ChannelDetailResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [activeTab, setActiveTab] = useState<TabID>('overview')
  const [peerNoteDraft, setPeerNoteDraft] = useState('')
  const [peerNoteSaving, setPeerNoteSaving] = useState(false)
  const [peerNoteStatus, setPeerNoteStatus] = useState('')
  const [channelNoteDraft, setChannelNoteDraft] = useState('')
  const [channelNoteSaving, setChannelNoteSaving] = useState(false)
  const [channelNoteStatus, setChannelNoteStatus] = useState('')

  const channel = detail?.channel || initialChannel || { channel_point: channelPoint }
  const settings = detail?.settings || {}
  const periods = Array.isArray(detail?.periods) ? detail?.periods || [] : []
  const feeLogs = Array.isArray(detail?.fee_logs) ? detail?.fee_logs || [] : []
  const routed = Array.isArray(detail?.routed) ? detail?.routed || [] : []
  const rebalances = Array.isArray(detail?.rebalances) ? detail?.rebalances || [] : []
  const sent = Array.isArray(detail?.sent) ? detail?.sent || [] : []
  const received = Array.isArray(detail?.received) ? detail?.received || [] : []
  const failedHtlcs = Array.isArray(detail?.failed_htlcs) ? detail?.failed_htlcs || [] : []
  const peerEvents = Array.isArray(detail?.peer_events) ? detail?.peer_events || [] : []
  const previousChannelNotes = Array.isArray(detail?.previous_channel_notes) ? detail?.previous_channel_notes || [] : []
  const pendingHtlcs = Array.isArray(detail?.pending_htlcs)
    ? detail?.pending_htlcs || []
    : Array.isArray(channel.pending_htlcs)
      ? channel.pending_htlcs || []
      : []
  const warnings = Array.isArray(detail?.data_source_warnings) ? detail?.data_source_warnings || [] : []

  const capacity = asNumber(channel.capacity_sat)
  const localBalance = asNumber(channel.local_balance_sat)
  const remoteBalance = asNumber(channel.remote_balance_sat)
  const reserveBalance = asNumber(channel.local_chan_reserve_sat)
  const unsettledBalance = asNumber(channel.unsettled_balance_sat)
  const visualTotal = Math.max(capacity, localBalance + remoteBalance + unsettledBalance, 1)
  const localPct = Math.max(0, Math.min(100, (localBalance / visualTotal) * 100))
  const remotePct = Math.max(0, Math.min(100, (remoteBalance / visualTotal) * 100))
  const unsettledPct = Math.max(0, Math.min(100, (unsettledBalance / visualTotal) * 100))
  const reservePct = Math.max(0, Math.min(100, (reserveBalance / visualTotal) * 100))
  const outboundPct = capacity > 0 ? (localBalance / capacity) * 100 : 0
  const inboundPct = capacity > 0 ? (remoteBalance / capacity) * 100 : 0
  const alias = channel.peer_alias || detail?.peer?.alias || shortMiddle(channel.remote_pubkey, 14, 6) || t('lightningOps.unknownPeer')
  const shortChannelID = detail?.short_channel_id || channel.channel_id_str || (channel.channel_id ? String(channel.channel_id) : '')
  const isActive = channel.active === true
  const isLocalDisabled = channel.local_disabled === true
  const profit7d = finiteNumber(channel.profit_fee_7d_sat)
  const feeMargin = finiteNumber(channel.out_ppm7d) !== undefined && finiteNumber(channel.rebal_ppm7d) !== undefined
    ? asNumber(channel.out_ppm7d) - asNumber(channel.rebal_ppm7d)
    : undefined

  const formatSats = (value?: number) => `${numberFormatter.format(Math.trunc(Math.max(0, asNumber(value))))} sat`
  const formatCompactSats = (value?: number) => `${compactFormatter.format(Math.trunc(Math.max(0, asNumber(value))))} sat`
  const formatSignedSats = (value?: number) => {
    if (typeof value !== 'number' || !Number.isFinite(value)) return t('common.na')
    const sign = value > 0 ? '+' : ''
    return `${sign}${numberFormatter.format(Math.trunc(value))} sat`
  }
  const formatPpm = (value?: number) => (typeof value === 'number' && Number.isFinite(value) ? `${numberFormatter.format(Math.round(value))} ppm` : t('common.na'))
  const formatMsat = (value?: number) => (typeof value === 'number' && Number.isFinite(value) ? `${numberFormatter.format(Math.round(value))} msat` : t('common.na'))
  const formatPct = (value?: number, digits = 1) => (typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(digits)}%` : t('common.na'))
  const formatTime = (value?: string) => {
    if (!value) return t('common.na')
    const date = new Date(value)
    if (!Number.isFinite(date.getTime())) return value
    return dateFormatter.format(date)
  }
  const formatBool = (value?: boolean) => value ? t('common.yes') : t('common.no')
  const periodLabel = (value?: string) => {
    switch (String(value || '').toLowerCase()) {
      case '1d':
        return t('lightningOps.channelDetailPeriod1d')
      case '7d':
        return t('lightningOps.channelDetailPeriod7d')
      case '30d':
        return t('lightningOps.channelDetailPeriod30d')
      case 'lifetime':
        return t('lightningOps.channelDetailPeriodLifetime')
      default:
        return value || t('common.na')
    }
  }
  const sideLabel = (value?: string) => {
    const normalized = String(value || '').toLowerCase()
    if (normalized === 'local') return t('common.local')
    if (normalized === 'remote' || normalized === 'peer') return t('common.remote')
    return value || t('common.na')
  }
  const rebalanceDirectionLabel = (value?: string) => {
    switch (String(value || '').toLowerCase()) {
      case 'in':
        return t('lightningOps.channelDetailRebalanceDirectionIn')
      case 'out':
        return t('lightningOps.channelDetailRebalanceDirectionOut')
      default:
        return t('lightningOps.channelDetailRebalanceDirectionRelated')
    }
  }

  const loadDetail = async () => {
    const point = String(channelPoint || initialChannel?.channel_point || '').trim()
    if (!point) return
    setLoading(true)
    setError('')
    try {
      const payload = await getLnChannelDetail(point, DETAIL_LIMIT) as ChannelDetailResponse
      setDetail(payload)
      setPeerNoteDraft(typeof payload?.peer_note === 'string' ? payload.peer_note : '')
      setChannelNoteDraft(typeof payload?.channel_note === 'string' ? payload.channel_note : typeof payload?.note === 'string' ? payload.note : '')
      setPeerNoteStatus('')
      setChannelNoteStatus('')
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : t('lightningOps.channelDetailLoadFailed'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!open) return
    setActiveTab('overview')
    setDetail(null)
    setError('')
    setPeerNoteStatus('')
    setChannelNoteStatus('')
    setPeerNoteDraft('')
    setChannelNoteDraft('')
    void loadDetail()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, channelPoint])

  useEffect(() => {
    if (!open || typeof document === 'undefined') return
    const original = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = original
    }
  }, [open])

  useEffect(() => {
    if (!open || typeof window === 'undefined') return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [open, onClose])

  if (!open) return null

  const savePeerNote = async () => {
    const remotePubkey = String(channel.remote_pubkey || detail?.peer?.pub_key || '').trim()
    if (!remotePubkey) return
    setPeerNoteSaving(true)
    setPeerNoteStatus('')
    try {
      const payload = await saveLnPeerNote({ remote_pubkey: remotePubkey, note: peerNoteDraft }) as { peer_note?: string }
      const savedNote = typeof payload?.peer_note === 'string' ? payload.peer_note : peerNoteDraft
      setDetail((current) => current ? { ...current, peer_note: savedNote } : current)
      setPeerNoteDraft(savedNote)
      setPeerNoteStatus(t('lightningOps.channelDetailPeerNoteSaved'))
    } catch (err: unknown) {
      setPeerNoteStatus(err instanceof Error ? err.message : t('lightningOps.channelDetailPeerNoteSaveFailed'))
    } finally {
      setPeerNoteSaving(false)
    }
  }

  const saveChannelNote = async () => {
    const point = String(channel.channel_point || channelPoint || '').trim()
    if (!point) return
    setChannelNoteSaving(true)
    setChannelNoteStatus('')
    try {
      const payload = await saveLnChannelNote({
        channel_point: point,
        note: channelNoteDraft,
        remote_pubkey: channel.remote_pubkey || detail?.peer?.pub_key || '',
        peer_alias: alias,
        channel_id: channel.channel_id,
        short_channel_id: shortChannelID,
      }) as { note?: string; channel_note?: string }
      const savedNote = typeof payload?.channel_note === 'string' ? payload.channel_note : typeof payload?.note === 'string' ? payload.note : channelNoteDraft
      setDetail((current) => current ? { ...current, note: savedNote, channel_note: savedNote } : current)
      setChannelNoteDraft(savedNote)
      setChannelNoteStatus(t('lightningOps.channelDetailChannelNoteSaved'))
    } catch (err: unknown) {
      setChannelNoteStatus(err instanceof Error ? err.message : t('lightningOps.channelDetailChannelNoteSaveFailed'))
    } finally {
      setChannelNoteSaving(false)
    }
  }

  const tabs: Array<{ id: TabID; label: string; count?: number }> = [
    { id: 'overview', label: t('lightningOps.channelDetailTabOverview') },
    { id: 'economics', label: t('lightningOps.channelDetailTabEconomics'), count: periods.length + feeLogs.length },
    { id: 'policy', label: t('lightningOps.channelDetailTabPolicy'), count: peerEvents.length },
    { id: 'activity', label: t('lightningOps.channelDetailTabActivity'), count: routed.length + rebalances.length + sent.length + received.length },
    { id: 'failures', label: t('lightningOps.channelDetailTabFailures'), count: failedHtlcs.length + pendingHtlcs.length },
    { id: 'notes', label: t('lightningOps.channelDetailTabNotes') },
  ]
  const emptyRowsLabel = t('lightningOps.channelDetailNoRows')

  const statusTone = isActive ? 'green' : isLocalDisabled ? 'amber' : 'red'
  const statusLabel = isActive
    ? t('lightningOps.channelDetailStatusActive')
    : isLocalDisabled
      ? t('lightningOps.channelDetailStatusLocalDisabled')
      : t('lightningOps.channelDetailStatusInactive')
  const policyRows = [
    { label: t('lightningOps.channelDetailLocalFee'), value: formatPpm(channel.fee_rate_ppm) },
    { label: t('lightningOps.channelDetailLocalBase'), value: formatMsat(channel.base_fee_msat) },
    { label: t('lightningOps.channelDetailInboundFee'), value: formatPpm(channel.inbound_fee_rate_ppm) },
    { label: t('lightningOps.channelDetailInboundBase'), value: formatMsat(channel.inbound_base_msat) },
    { label: t('lightningOps.channelDetailPeerFee'), value: formatPpm(channel.peer_fee_rate_ppm) },
    { label: t('lightningOps.channelDetailPeerBase'), value: formatMsat(channel.peer_base_msat) },
    { label: t('lightningOps.channelDetailLocalDisabled'), value: formatBool(channel.local_disabled) },
  ]
  const settingBadges = [
    { label: t('lightningOps.channelDetailAutofee'), enabled: settings.autofee_enabled, configured: settings.autofee_configured },
    { label: t('lightningOps.channelDetailRebalance'), enabled: settings.rebalance_auto_enabled || settings.manual_restart_enabled, configured: settings.rebalance_configured },
    { label: t('lightningOps.channelDetailManualRestart'), enabled: settings.manual_restart_enabled, configured: settings.rebalance_configured },
    { label: t('lightningOps.channelDetailExcludedSource'), enabled: settings.excluded_as_source, configured: settings.excluded_as_source },
    { label: t('lightningOps.channelDetailBypassCostGate'), enabled: settings.auto_bypass_cost_gate, configured: settings.auto_bypass_cost_gate },
  ]

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center px-3 py-4">
      <button type="button" className="absolute inset-0 cursor-default bg-black/70 backdrop-blur-sm" aria-label={t('common.close')} onClick={onClose} />
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="channel-detail-title"
        className="relative z-10 flex h-[calc(100vh-2rem)] max-h-[840px] w-[calc(100vw-1.5rem)] max-w-[1180px] flex-col overflow-hidden rounded-3xl border border-white/10 bg-slate/95 shadow-panel"
      >
        <div className="shrink-0 border-b border-white/10 bg-ink/70 px-5 py-4">
          <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span className={`h-3 w-3 rounded-full ${isActive ? 'bg-glow' : isLocalDisabled ? 'bg-brass' : 'bg-rose-300'}`} />
                <p className="text-xs font-semibold uppercase tracking-[0.18em] text-fog/45">{t('lightningOps.channelDetailTitle')}</p>
                <Badge tone={statusTone}>{statusLabel}</Badge>
                <Badge tone={channel.private ? 'amber' : 'blue'}>{channel.private ? t('lightningOps.channelDetailPrivate') : t('lightningOps.channelDetailPublic')}</Badge>
                <Badge tone={channel.initiator ? 'green' : 'muted'}>{channel.initiator ? t('lightningOps.openerLocal') : t('lightningOps.openerRemote')}</Badge>
              </div>
              <h3 id="channel-detail-title" className="mt-2 min-w-0 truncate text-2xl font-semibold text-fog">{alias}</h3>
              <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-fog/50">
                {shortChannelID && <span>{t('lightningOps.channelDetailShortId')}: <span className="font-mono text-fog/75">{shortChannelID}</span></span>}
                {detail?.open_block_height ? <span>{t('lightningOps.channelDetailOpenBlock')}: {numberFormatter.format(detail.open_block_height)}</span> : null}
                {detail?.opened_confirmations ? <span>{numberFormatter.format(detail.opened_confirmations)} {t('lightningOps.channelDetailConfirmations')}</span> : null}
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <button
                type="button"
                className="inline-flex h-10 items-center gap-2 rounded-full border border-white/10 bg-white/5 px-3 text-xs text-fog/75 transition hover:border-glow/40 hover:text-fog disabled:cursor-wait disabled:opacity-60"
                onClick={() => { void loadDetail() }}
                disabled={loading}
              >
                <Icon name="refresh" />
                <span>{loading ? t('lightningOps.channelDetailLoading') : t('common.refresh')}</span>
              </button>
              <button
                type="button"
                className="inline-flex h-10 w-10 items-center justify-center rounded-full border border-white/10 bg-white/5 text-fog/70 transition hover:border-rose-300/40 hover:text-rose-100"
                onClick={onClose}
                aria-label={t('common.close')}
                title={t('common.close')}
              >
                <Icon name="close" />
              </button>
            </div>
          </div>
          <div className="mt-4 grid gap-3 text-xs lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
            <div className="min-w-0 rounded-2xl border border-white/10 bg-white/5 px-3 py-2">
              <span className="text-fog/45">{t('lightningOps.channelDetailChannelPoint')}</span>
              <div className="mt-1 break-all font-mono text-fog/80">{channel.channel_point || channelPoint || t('common.na')}</div>
            </div>
            <div className="min-w-0 rounded-2xl border border-white/10 bg-white/5 px-3 py-2">
              <span className="text-fog/45">{t('lightningOps.channelDetailRemotePubkey')}</span>
              <div className="mt-1 break-all font-mono text-fog/80">{channel.remote_pubkey || detail?.peer?.pub_key || t('common.na')}</div>
            </div>
          </div>
        </div>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-hidden p-4">
          {(error || warnings.length > 0) && (
            <div className={`rounded-2xl border px-4 py-3 text-sm ${error ? 'border-rose-300/30 bg-rose-500/10 text-rose-100' : 'border-brass/35 bg-brass/10 text-brass'}`}>
              {error || `${t('lightningOps.channelDetailDataLimited')}: ${warnings.join(', ')}`}
            </div>
          )}

          <div className="grid shrink-0 gap-4 lg:grid-cols-[1.2fr_1fr]">
            <div className="rounded-2xl border border-white/10 bg-ink/45 p-4">
              <div className="flex items-center justify-between gap-3">
                <div>
                  <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailLiquidityTitle')}</h4>
                  <p className="text-xs text-fog/50">{t('lightningOps.channelDetailLiquiditySubtitle', { outbound: formatPct(outboundPct, 0), inbound: formatPct(inboundPct, 0) })}</p>
                </div>
                <Badge tone={profit7d !== undefined && profit7d > 0 ? 'green' : profit7d !== undefined && profit7d < 0 ? 'red' : 'muted'}>
                  {t('lightningOps.channelDetailProfit7d')}: {formatSignedSats(profit7d)}
                </Badge>
              </div>
              <div className="mt-4">
                <div className="relative h-7 overflow-hidden rounded-full border border-white/10 bg-black/25">
                  <div className="absolute inset-y-0 left-0 bg-glow/90" style={{ width: `${localPct}%` }} />
                  <div className="absolute inset-y-0 bg-sky-300/35" style={{ left: `${localPct}%`, width: `${remotePct}%` }} />
                  {unsettledPct > 0 && <div className="absolute inset-y-0 right-0 bg-brass/75" style={{ width: `${unsettledPct}%` }} />}
                  {reservePct > 0 && <div className="absolute inset-y-0 left-0 border-r border-white/70 bg-white/20" style={{ width: `${reservePct}%` }} />}
                </div>
                <div className="mt-3 grid grid-cols-2 gap-2 text-xs md:grid-cols-4">
                  <div><span className="text-glow">{t('lightningOps.channelDetailLocalBalance')}</span><div className="font-semibold">{formatSats(localBalance)}</div></div>
                  <div><span className="text-sky-200">{t('lightningOps.channelDetailRemoteBalance')}</span><div className="font-semibold">{formatSats(remoteBalance)}</div></div>
                  <div><span className="text-fog/50">{t('lightningOps.channelDetailReserve')}</span><div className="font-semibold">{formatSats(reserveBalance)}</div></div>
                  <div><span className="text-brass">{t('lightningOps.channelDetailUnsettled')}</span><div className="font-semibold">{formatSats(unsettledBalance)}</div></div>
                </div>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3 md:grid-cols-4 lg:grid-cols-2">
              <MetricCard label={t('lightningOps.channelDetailCapacity')} value={formatCompactSats(capacity)} detail={formatSats(capacity)} tone="blue" />
              <MetricCard label={t('lightningOps.channelDetailPendingHtlcs')} value={numberFormatter.format(Math.max(asNumber(channel.pending_htlc_count), pendingHtlcs.length))} detail={pendingHtlcs.length ? t('lightningOps.channelDetailWithDetails', { count: pendingHtlcs.length }) : t('common.na')} tone={pendingHtlcs.length ? 'amber' : 'muted'} />
              <MetricCard label={t('lightningOps.channelDetailFeeRate')} value={formatPpm(channel.fee_rate_ppm)} detail={`${t('lightningOps.channelDetailMargin7d')}: ${formatPpm(feeMargin)}`} tone={feeMargin !== undefined && feeMargin > 0 ? 'green' : 'muted'} />
              <MetricCard label={t('lightningOps.channelDetailClass')} value={settings.autofee_class_label || channel.class_label || t('common.na')} detail={settings.autofee_stalled_rounds ? t('lightningOps.channelDetailStalledRounds', { count: settings.autofee_stalled_rounds }) : t('lightningOps.channelDetailAutofee')} tone="amber" />
            </div>
          </div>

          <div className="flex min-h-0 flex-col overflow-hidden">
            <div className="shrink-0 overflow-x-auto border-b border-white/10">
              <div className="flex min-w-max gap-2 pb-2">
                {tabs.map((tab) => (
                  <button
                    key={tab.id}
                    type="button"
                    className={`inline-flex items-center gap-2 rounded-full border px-3 py-2 text-xs transition ${
                      activeTab === tab.id
                        ? 'border-glow/45 bg-glow/15 text-glow'
                        : 'border-white/10 bg-white/5 text-fog/60 hover:border-white/25 hover:text-fog'
                    }`}
                    onClick={() => setActiveTab(tab.id)}
                  >
                    {tab.label}
                    {typeof tab.count === 'number' && tab.count > 0 && (
                      <span className="rounded-full bg-white/10 px-1.5 py-0.5 text-[10px] text-fog/70">{tab.count}</span>
                    )}
                  </button>
                ))}
              </div>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto pt-4">
              {activeTab === 'overview' && (
                <div className="grid gap-4 xl:grid-cols-[1fr_1fr]">
                  <div className="rounded-2xl border border-white/10 bg-ink/45 p-4">
                    <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailCurrentPolicy')}</h4>
                    <dl className="mt-3">
                      {policyRows.map((row) => <InfoRow key={row.label} label={row.label} value={row.value} />)}
                    </dl>
                  </div>
                  <div className="rounded-2xl border border-white/10 bg-ink/45 p-4">
                    <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailChannelSettings')}</h4>
                    <div className="mt-3 flex flex-wrap gap-2">
                      {settingBadges.map((item) => (
                        <Badge key={item.label} tone={item.enabled ? 'green' : item.configured ? 'amber' : 'muted'}>
                          {item.label}: {item.enabled ? t('common.enabled') : item.configured ? t('common.disabled') : t('lightningOps.channelDetailNotConfigured')}
                        </Badge>
                      ))}
                    </div>
                    <dl className="mt-4">
                      <InfoRow label={t('lightningOps.channelDetailTargetOutbound')} value={formatPct(settings.target_outbound_pct)} />
                      <InfoRow label={t('lightningOps.channelDetailEconRatio')} value={settings.use_default_econ_ratio ? t('common.auto') : formatPct(settings.econ_ratio_override ? settings.econ_ratio_override * 100 : undefined)} />
                      <InfoRow label={t('lightningOps.channelDetailLastAutofee')} value={settings.autofee_last_at ? `${formatTime(settings.autofee_last_at)} - ${formatPpm(settings.autofee_last_ppm)}` : t('common.na')} />
                      <InfoRow label={t('lightningOps.channelDetailPeerAddress')} value={detail?.peer?.address || t('common.na')} mono />
                    </dl>
                  </div>
                  <div className="rounded-2xl border border-white/10 bg-ink/45 p-4 xl:col-span-2">
                    <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailHistoricalCoverage')}</h4>
                    <div className="mt-3 grid gap-3 md:grid-cols-2">
                      <MetricCard label={t('lightningOps.channelDetailCoverageNotifications')} value={detail?.coverage?.notifications_since ? formatTime(detail.coverage.notifications_since) : t('common.na')} detail={detail?.coverage?.notifications_until ? formatTime(detail.coverage.notifications_until) : undefined} tone="blue" />
                      <MetricCard label={t('lightningOps.channelDetailCoveragePolicy')} value={detail?.coverage?.policy_since ? formatTime(detail.coverage.policy_since) : t('common.na')} detail={detail?.coverage?.policy_until ? formatTime(detail.coverage.policy_until) : undefined} tone="amber" />
                    </div>
                  </div>
                </div>
              )}

              {activeTab === 'economics' && (
                <div className="grid gap-4">
                  <TablePanel title={t('lightningOps.channelDetailPeriodStats')} empty={periods.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[260px]">
                    <table className="min-w-[980px] w-full text-left text-xs">
                      <thead className="sticky top-0 bg-slate text-fog/60">
                        <tr>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailPeriod')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailRouted')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailForwardIn')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailForwardOut')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailRebalanceIn')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailRebalanceOut')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailRevenue')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailCost')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailProfit')}</th>
                          <th className="px-3 py-2">APY</th>
                        </tr>
                      </thead>
                      <tbody>
                        {periods.map((period, index) => (
                          <tr key={`${period.period || index}`} className="border-t border-white/5">
                            <td className="px-3 py-2 font-semibold text-fog">{periodLabel(period.period)}</td>
                            <td className="px-3 py-2">{numberFormatter.format(asNumber(period.forward_count))}</td>
                            <td className="px-3 py-2">{formatSats(period.forward_in_amount_sat)}</td>
                            <td className="px-3 py-2">{formatSats(period.forward_out_amount_sat)}</td>
                            <td className="px-3 py-2">{formatSats(period.rebalance_in_amount_sat)}</td>
                            <td className="px-3 py-2">{formatSats(period.rebalance_out_amount_sat)}</td>
                            <td className="px-3 py-2 text-glow">{formatSats(period.revenue_sat)}</td>
                            <td className="px-3 py-2 text-brass">{formatSats(period.cost_sat)}</td>
                            <td className={`px-3 py-2 ${(period.profit_sat || 0) >= 0 ? 'text-glow' : 'text-rose-200'}`}>{formatSignedSats(period.profit_sat)}</td>
                            <td className="px-3 py-2">{formatPct(period.apy)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TablePanel>
                  <TablePanel title={t('lightningOps.channelDetailFeeLogs')} empty={feeLogs.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[310px]">
                    <table className="min-w-[920px] w-full text-left text-xs">
                      <thead className="sticky top-0 bg-slate text-fog/60">
                        <tr>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailTimestamp')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailSide')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailFeeRate')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailBase')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailInboundFee')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailDisabled')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailChange')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {feeLogs.map((log, index) => (
                          <tr key={`${log.captured_at || index}-${log.side || ''}`} className="border-t border-white/5">
                            <td className="px-3 py-2 whitespace-nowrap">{formatTime(log.captured_at)}</td>
                            <td className="px-3 py-2">{sideLabel(log.side)}</td>
                            <td className="px-3 py-2">{formatPpm(log.old_fee_rate_ppm)} {'->'} {formatPpm(log.new_fee_rate_ppm)}</td>
                            <td className="px-3 py-2">{formatMsat(log.old_base_msat)} {'->'} {formatMsat(log.new_base_msat)}</td>
                            <td className="px-3 py-2">{formatPpm(log.old_inbound_fee_rate_ppm)} {'->'} {formatPpm(log.new_inbound_fee_rate_ppm)}</td>
                            <td className="px-3 py-2">{log.old_disabled === undefined ? t('common.na') : `${formatBool(log.old_disabled)} -> ${formatBool(Boolean(log.new_disabled))}`}</td>
                            <td className={`px-3 py-2 ${asNumber(log.change_pct) > 0 ? 'text-glow' : asNumber(log.change_pct) < 0 ? 'text-rose-200' : ''}`}>{formatPct(log.change_pct)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TablePanel>
                </div>
              )}

              {activeTab === 'policy' && (
                <div className="grid gap-4 xl:grid-cols-2">
                  <div className="rounded-2xl border border-white/10 bg-ink/45 p-4">
                    <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailCurrentPolicy')}</h4>
                    <dl className="mt-3">
                      {policyRows.map((row) => <InfoRow key={row.label} label={row.label} value={row.value} />)}
                    </dl>
                  </div>
                  <TablePanel title={t('lightningOps.channelDetailPeerEvents')} empty={peerEvents.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[360px]">
                    <table className="min-w-[680px] w-full text-left text-xs">
                      <thead className="sticky top-0 bg-slate text-fog/60">
                        <tr>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailTimestamp')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailSide')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailSetting')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailOld')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailNew')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {peerEvents.map((event, index) => (
                          <tr key={`${event.occurred_at || index}-${event.setting || ''}`} className="border-t border-white/5">
                            <td className="px-3 py-2 whitespace-nowrap">{formatTime(event.occurred_at)}</td>
                            <td className="px-3 py-2">{sideLabel(event.side)}</td>
                            <td className="px-3 py-2">{event.setting || t('common.na')}</td>
                            <td className="px-3 py-2">{event.old_value || t('common.na')}</td>
                            <td className="px-3 py-2">{event.new_value || t('common.na')}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TablePanel>
                </div>
              )}

              {activeTab === 'activity' && (
                <div className="grid gap-4">
                  <TablePanel title={t('lightningOps.channelDetailRoutedPayments')} empty={routed.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[250px]">
                    <table className="min-w-[960px] w-full text-left text-xs">
                      <thead className="sticky top-0 bg-slate text-fog/60">
                        <tr>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailTimestamp')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailIncoming')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailOutgoing')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailAmountIn')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailAmountOut')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailFee')}</th>
                          <th className="px-3 py-2">PPM</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailHash')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {routed.map((item, index) => (
                          <tr key={`${item.occurred_at || index}-${item.payment_hash || ''}`} className="border-t border-white/5">
                            <td className="px-3 py-2 whitespace-nowrap">{formatTime(item.occurred_at)}</td>
                            <td className="px-3 py-2">{item.chan_in_alias || item.chan_id_in || t('common.na')}</td>
                            <td className="px-3 py-2">{item.chan_out_alias || item.chan_id_out || t('common.na')}</td>
                            <td className="px-3 py-2">{formatSats(item.amount_in_sat)}</td>
                            <td className="px-3 py-2">{formatSats(item.amount_out_sat)}</td>
                            <td className="px-3 py-2 text-glow">{formatSats(item.fee_sat)}</td>
                            <td className="px-3 py-2">{formatPpm(item.ppm)}</td>
                            <td className="px-3 py-2 font-mono">{shortMiddle(item.payment_hash, 10, 6) || t('common.na')}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TablePanel>

                  <TablePanel title={t('lightningOps.channelDetailRebalances')} empty={rebalances.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[250px]">
                    <table className="min-w-[940px] w-full text-left text-xs">
                      <thead className="sticky top-0 bg-slate text-fog/60">
                        <tr>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailTimestamp')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailDirection')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailSource')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailTarget')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailAmount')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailCost')}</th>
                          <th className="px-3 py-2">PPM</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailStatus')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailHash')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {rebalances.map((item, index) => (
                          <tr key={`${item.occurred_at || index}-${item.payment_hash || ''}`} className="border-t border-white/5">
                            <td className="px-3 py-2 whitespace-nowrap">{formatTime(item.occurred_at)}</td>
                            <td className="px-3 py-2">{rebalanceDirectionLabel(item.direction)}</td>
                            <td className="px-3 py-2">{item.source_alias || (item.source_channel_id ? numberFormatter.format(item.source_channel_id) : shortMiddle(item.source_point, 10, 6)) || t('common.na')}</td>
                            <td className="px-3 py-2">{item.target_alias || (item.target_channel_id ? numberFormatter.format(item.target_channel_id) : shortMiddle(item.target_point, 10, 6)) || t('common.na')}</td>
                            <td className="px-3 py-2">{formatSats(item.amount_sat)}</td>
                            <td className="px-3 py-2 text-brass">{formatSats(item.fee_sat)}</td>
                            <td className="px-3 py-2">{formatPpm(item.ppm)}</td>
                            <td className="px-3 py-2">{item.status || t('common.na')}</td>
                            <td className="px-3 py-2 font-mono">{shortMiddle(item.payment_hash, 10, 6) || t('common.na')}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TablePanel>

                  <div className="grid gap-4 xl:grid-cols-2">
                    <TablePanel title={t('lightningOps.channelDetailSentPayments')} empty={sent.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[240px]">
                      <table className="min-w-[720px] w-full text-left text-xs">
                        <thead className="sticky top-0 bg-slate text-fog/60">
                          <tr>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailTimestamp')}</th>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailAmount')}</th>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailFee')}</th>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailStatus')}</th>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailHash')}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {sent.map((item, index) => (
                            <tr key={`${item.occurred_at || index}-${item.payment_hash || ''}`} className="border-t border-white/5">
                              <td className="px-3 py-2 whitespace-nowrap">{formatTime(item.occurred_at)}</td>
                              <td className="px-3 py-2">{formatSats(item.amount_sat)}</td>
                              <td className="px-3 py-2">{formatSats(item.fee_sat)}</td>
                              <td className="px-3 py-2">{item.status || t('common.na')}</td>
                              <td className="px-3 py-2 font-mono">{shortMiddle(item.payment_hash, 10, 6) || t('common.na')}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </TablePanel>

                    <TablePanel title={t('lightningOps.channelDetailReceivedPayments')} empty={received.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[240px]">
                      <table className="min-w-[720px] w-full text-left text-xs">
                        <thead className="sticky top-0 bg-slate text-fog/60">
                          <tr>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailTimestamp')}</th>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailAmount')}</th>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailStatus')}</th>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailMemo')}</th>
                            <th className="px-3 py-2">{t('lightningOps.channelDetailHash')}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {received.map((item, index) => (
                            <tr key={`${item.occurred_at || index}-${item.payment_hash || ''}`} className="border-t border-white/5">
                              <td className="px-3 py-2 whitespace-nowrap">{formatTime(item.occurred_at)}</td>
                              <td className="px-3 py-2">{formatSats(item.amount_sat)}</td>
                              <td className="px-3 py-2">{item.status || t('common.na')}</td>
                              <td className="px-3 py-2 max-w-[220px] truncate">{item.memo || t('common.na')}</td>
                              <td className="px-3 py-2 font-mono">{shortMiddle(item.payment_hash, 10, 6) || t('common.na')}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </TablePanel>
                  </div>
                </div>
              )}

              {activeTab === 'failures' && (
                <div className="grid gap-4">
                  <TablePanel title={t('lightningOps.channelDetailFailedHtlcs')} empty={failedHtlcs.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[340px]">
                    <table className="min-w-[980px] w-full text-left text-xs">
                      <thead className="sticky top-0 bg-slate text-fog/60">
                        <tr>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailTimestamp')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailSource')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailIncoming')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailOutgoing')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailAmount')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailPotentialFee')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailFailure')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailDetail')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {failedHtlcs.map((item, index) => (
                          <tr key={`${item.occurred_at || index}-${item.payment_hash || ''}`} className="border-t border-white/5">
                            <td className="px-3 py-2 whitespace-nowrap">{formatTime(item.occurred_at)}</td>
                            <td className="px-3 py-2">{item.source || t('common.na')}</td>
                            <td className="px-3 py-2">{item.incoming_alias || item.incoming_channel_id || t('common.na')}</td>
                            <td className="px-3 py-2">{item.outgoing_alias || item.outgoing_channel_id || t('common.na')}</td>
                            <td className="px-3 py-2">{formatSats(item.amount_sat)}</td>
                            <td className="px-3 py-2 text-brass">{formatSats(item.potential_fee_sat)}</td>
                            <td className="px-3 py-2">{item.failure_code || t('common.na')}</td>
                            <td className="px-3 py-2 max-w-[260px] truncate">{item.failure_detail || t('common.na')}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TablePanel>

                  <TablePanel title={t('lightningOps.channelDetailPendingHtlcsTable')} empty={pendingHtlcs.length === 0} emptyLabel={emptyRowsLabel} heightClass="max-h-[260px]">
                    <table className="min-w-[760px] w-full text-left text-xs">
                      <thead className="sticky top-0 bg-slate text-fog/60">
                        <tr>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailDirection')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailPeerAlias')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailAmount')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailExpiration')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailForwardingChannel')}</th>
                          <th className="px-3 py-2">{t('lightningOps.channelDetailLocked')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {pendingHtlcs.map((item, index) => (
                          <tr key={`${item.htlc_index || index}-${item.forwarding_channel_id || ''}`} className="border-t border-white/5">
                            <td className="px-3 py-2">{item.incoming ? t('lightningOps.channelDetailIncoming') : t('lightningOps.channelDetailOutgoing')}</td>
                            <td className="px-3 py-2">{item.peer_alias || t('common.na')}</td>
                            <td className="px-3 py-2">{formatSats(item.amount_sat)}</td>
                            <td className="px-3 py-2">{item.expiration_height ? numberFormatter.format(item.expiration_height) : t('common.na')}</td>
                            <td className="px-3 py-2">{item.forwarding_channel_id ? numberFormatter.format(item.forwarding_channel_id) : t('common.na')}</td>
                            <td className="px-3 py-2">{formatBool(item.locked_in)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TablePanel>
                </div>
              )}

              {activeTab === 'notes' && (
                <div className="grid gap-4 xl:grid-cols-[1fr_0.8fr]">
                  <div className="space-y-4">
                    <div className="rounded-2xl border border-white/10 bg-ink/45 p-4">
                      <div className="flex flex-wrap items-center justify-between gap-3">
                        <div>
                          <div className="flex flex-wrap items-center gap-2">
                            <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailPeerNotes')}</h4>
                            <Badge tone="blue">{t('lightningOps.channelDetailPeerNoteScope')}</Badge>
                          </div>
                          <p className="mt-1 text-xs text-fog/50">{t('lightningOps.channelDetailPeerNotesSubtitle')}</p>
                        </div>
                        <button
                          type="button"
                          className="inline-flex h-10 items-center gap-2 rounded-full border border-glow/35 bg-glow/10 px-3 text-xs text-glow transition hover:border-glow/60 disabled:cursor-wait disabled:opacity-60"
                          onClick={() => { void savePeerNote() }}
                          disabled={peerNoteSaving}
                        >
                          <Icon name="save" />
                          <span>{peerNoteSaving ? t('common.saving') : t('lightningOps.channelDetailSavePeerNote')}</span>
                        </button>
                      </div>
                      <textarea
                        className="mt-4 h-[180px] w-full resize-none rounded-2xl border border-white/10 bg-black/20 p-4 text-sm text-fog outline-none transition placeholder:text-fog/35 focus:border-glow/45"
                        value={peerNoteDraft}
                        onChange={(event) => setPeerNoteDraft(event.target.value)}
                        placeholder={t('lightningOps.channelDetailPeerNotePlaceholder')}
                        spellCheck={false}
                      />
                      {peerNoteStatus && <p className="mt-3 text-sm text-brass">{peerNoteStatus}</p>}
                    </div>
                    <div className="rounded-2xl border border-white/10 bg-ink/45 p-4">
                      <div className="flex flex-wrap items-center justify-between gap-3">
                        <div>
                          <div className="flex flex-wrap items-center gap-2">
                            <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailChannelNotes')}</h4>
                            <Badge tone="amber">{t('lightningOps.channelDetailChannelNoteScope')}</Badge>
                          </div>
                          <p className="mt-1 text-xs text-fog/50">{t('lightningOps.channelDetailChannelNotesSubtitle')}</p>
                        </div>
                        <button
                          type="button"
                          className="inline-flex h-10 items-center gap-2 rounded-full border border-glow/35 bg-glow/10 px-3 text-xs text-glow transition hover:border-glow/60 disabled:cursor-wait disabled:opacity-60"
                          onClick={() => { void saveChannelNote() }}
                          disabled={channelNoteSaving}
                        >
                          <Icon name="save" />
                          <span>{channelNoteSaving ? t('common.saving') : t('lightningOps.channelDetailSaveChannelNote')}</span>
                        </button>
                      </div>
                      <textarea
                        className="mt-4 h-[180px] w-full resize-none rounded-2xl border border-white/10 bg-black/20 p-4 text-sm text-fog outline-none transition placeholder:text-fog/35 focus:border-glow/45"
                        value={channelNoteDraft}
                        onChange={(event) => setChannelNoteDraft(event.target.value)}
                        placeholder={t('lightningOps.channelDetailChannelNotePlaceholder')}
                        spellCheck={false}
                      />
                      {channelNoteStatus && <p className="mt-3 text-sm text-brass">{channelNoteStatus}</p>}
                    </div>
                  </div>
                  <div className="flex min-h-0 flex-col gap-4 xl:h-[552px]">
                    <div className="shrink-0 rounded-2xl border border-white/10 bg-ink/45 p-4">
                      <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailQuickContext')}</h4>
                      <dl className="mt-3">
                        <InfoRow label={t('lightningOps.channelDetailAlias')} value={alias} />
                        <InfoRow label={t('lightningOps.channelDetailCapacity')} value={formatSats(capacity)} />
                        <InfoRow label={t('lightningOps.channelDetailLocalBalance')} value={formatSats(localBalance)} />
                        <InfoRow label={t('lightningOps.channelDetailRemoteBalance')} value={formatSats(remoteBalance)} />
                        <InfoRow label={t('lightningOps.channelDetailClass')} value={settings.autofee_class_label || channel.class_label || t('common.na')} />
                        <InfoRow label={t('lightningOps.channelDetailPeerAddress')} value={detail?.peer?.address || t('common.na')} mono />
                      </dl>
                    </div>
                    {previousChannelNotes.length > 0 && (
                      <div className="flex min-h-[220px] flex-1 flex-col overflow-hidden rounded-2xl border border-white/10 bg-ink/45 p-4">
                        <div className="shrink-0">
                          <div className="flex flex-wrap items-center gap-2">
                            <h4 className="text-sm font-semibold">{t('lightningOps.channelDetailPreviousChannelNotes')}</h4>
                            <Badge tone="muted">{previousChannelNotes.length}</Badge>
                          </div>
                          <p className="mt-1 text-xs text-fog/50">{t('lightningOps.channelDetailPreviousChannelNotesSubtitle')}</p>
                        </div>
                        <div className="mt-3 min-h-0 flex-1 space-y-3 overflow-y-auto pr-1">
                          {previousChannelNotes.map((item, index) => (
                            <article key={`${item.channel_point || index}-${item.updated_at || ''}`} className="rounded-2xl border border-white/10 bg-black/20 p-3">
                              <div className="flex flex-wrap items-center justify-between gap-2">
                                <span className="font-mono text-[11px] text-fog/70">{item.short_channel_id || shortMiddle(item.channel_point, 12, 6) || t('common.na')}</span>
                                <span className="text-[11px] text-fog/45">{formatTime(item.updated_at)}</span>
                              </div>
                              <p className="mt-2 whitespace-pre-wrap break-words text-sm leading-relaxed text-fog/85">{item.note || t('common.na')}</p>
                            </article>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
