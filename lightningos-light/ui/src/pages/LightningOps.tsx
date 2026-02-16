import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { boostPeers, closeChannel, connectPeer, disconnectPeer, getAmbossHealth, getAutofeeChannels, getAutofeeConfig, getAutofeeResults, getAutofeeStatus, getBitcoinLocalStatus, getLnChanHeal, getLnChannelFees, getLnChannels, getLnHtlcManager, getLnHtlcManagerFailed, getLnHtlcManagerLogs, getLnPeers, getLnTorPeerChecker, getLnTorPeerCheckerLogs, getMempoolFees, openChannel, runAutofee, signLnMessage, updateAmbossHealth, updateAutofeeChannels, updateAutofeeConfig, updateChannelFees, updateLnChanHeal, updateLnChannelStatus, updateLnHtlcManager, updateLnTorPeerChecker } from '../api'

type Channel = {
  channel_point: string
  channel_id: number
  remote_pubkey: string
  peer_alias: string
  active: boolean
  chan_status_flags?: string
  local_disabled?: boolean
  private: boolean
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
  base_fee_msat?: number
  fee_rate_ppm?: number
  inbound_fee_rate_ppm?: number
  peer_fee_rate_ppm?: number
  peer_base_msat?: number
  class_label?: string
}

type PendingChannel = {
  channel_point: string
  remote_pubkey: string
  peer_alias?: string
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
  status: string
  closing_txid?: string
  blocks_til_maturity?: number
  limbo_balance?: number
  confirmations_until_active?: number
  private?: boolean
}

type Peer = {
  pub_key: string
  alias: string
  address: string
  inbound: boolean
  bytes_sent: number
  bytes_recv: number
  sat_sent: number
  sat_recv: number
  ping_time: number
  sync_type: string
  last_error: string
  last_error_time?: number
}

type AmbossHealthStatus = {
  enabled: boolean
  status: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_attempt_at?: string
  interval_sec?: number
  consecutive_failures?: number
}

type ChanHealStatus = {
  enabled: boolean
  status: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_attempt_at?: string
  interval_sec?: number
  last_updated?: number
}

type HtlcManagerStatus = {
  enabled: boolean
  status: string
  interval_minutes?: number
  interval_hours?: number
  min_htlc_sat: number
  max_local_pct: number
  last_attempt_at?: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_changed_count?: number
}

type HtlcManagerLogEntry = {
  ts: string
  alias: string
  channel_id: number
  channel_point: string
  old_min_msat: number
  new_min_msat: number
  old_max_msat: number
  new_max_msat: number
  result: string
}

type HtlcManagerFailedEntry = {
  ts: string
  incoming_channel_id?: string
  incoming_alias?: string
  outgoing_channel_id?: string
  outgoing_alias?: string
  incoming_amt_msat?: number
  outgoing_amt_msat?: number
  potential_fee_msat?: number
  failure_code?: string
  failure_detail?: string
  failure_reason?: string
  event?: string
}

type TorPeerCheckerStatus = {
  enabled: boolean
  status: string
  interval_hours: number
  last_attempt_at?: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_checked_count?: number
  last_hybrid_on_tor_count?: number
  last_attempted_count?: number
  last_switched_count?: number
}

type TorPeerCheckerLogEntry = {
  ts: string
  alias: string
  pub_key?: string
  from_address?: string
  to_address?: string
  result: string
  detail?: string
}

type BitcoinLocalCadenceBucket = {
  count: number
}

type BitcoinLocalStatus = {
  block_cadence_window_sec?: number
  block_cadence?: BitcoinLocalCadenceBucket[]
}

  type AutofeeConfig = {
    enabled: boolean
    profile: string
    lookback_days: number
    run_interval_sec: number
    cooldown_up_sec: number
    cooldown_down_sec: number
    rebal_cost_mode?: string
    amboss_enabled: boolean
    amboss_token_set: boolean
    inbound_passive_enabled: boolean
  discovery_enabled: boolean
  explorer_enabled: boolean
  super_source_enabled: boolean
  super_source_base_fee_msat: number
  revfloor_enabled: boolean
  circuit_breaker_enabled: boolean
  extreme_drain_enabled: boolean
  htlc_signal_enabled: boolean
  htlc_mode: string
  min_ppm: number
  max_ppm: number
}

type AutofeeStatus = {
  running: boolean
  last_run_at?: string
  next_run_at?: string
  last_error?: string
}

type AutofeeChannelSetting = {
  channel_id: number
  enabled: boolean
}

type AutofeeResultItem = {
  kind: string
  category?: string
  reason?: string
  dry_run?: boolean
  timestamp?: string
  up?: number
  down?: number
  flat?: number
  cooldown?: number
  small?: number
  same?: number
  disabled?: number
  inactive?: number
  inbound_disc?: number
  super_source?: number
  htlc_liq_hot?: number
  htlc_policy_hot?: number
  htlc_sample_low?: number
  htlc_window_min?: number
  htlc_min_attempts?: number
  htlc_min_policy_fails?: number
  htlc_min_liquidity_fails?: number
  htlc_node_factor?: number
  htlc_liquidity_factor?: number
  htlc_threshold_factor?: number
  amboss?: number
  missing?: number
  err?: number
  empty?: number
  outrate?: number
  mem?: number
  default?: number
  cooldown_ignored?: boolean
  node_class?: string
  liquidity_class?: string
  channel_count?: number
  total_capacity_sat?: number
  avg_capacity_sat?: number
  local_capacity_sat?: number
  local_ratio?: number
  revfloor_baseline?: number
  revfloor_min_abs?: number
  alias?: string
  channel_id?: number
  channel_point?: string
  local_ppm?: number
  new_ppm?: number
  target?: number
  out_ratio?: number
  out_ppm7d?: number
  rebal_ppm7d?: number
  seed?: number
  floor?: number
  floor_src?: string
  margin?: number
  rev_share?: number
  tags?: string[]
  inbound_discount?: number
  class_label?: string
  skip_reason?: string
  error?: string
  delta?: number
  delta_pct?: number
  prediction_code?: string
  prediction_cooldown_hours?: number
  htlc_attempts?: number
  htlc_policy_fails?: number
  htlc_liquidity_fails?: number
  htlc_window_min_channel?: number
}

export default function LightningOps() {
  const { t } = useTranslation()
  const [channels, setChannels] = useState<Channel[]>([])
  const [activeCount, setActiveCount] = useState(0)
  const [inactiveCount, setInactiveCount] = useState(0)
  const [pendingOpenCount, setPendingOpenCount] = useState(0)
  const [pendingCloseCount, setPendingCloseCount] = useState(0)
  const [pendingChannels, setPendingChannels] = useState<PendingChannel[]>([])
  const [status, setStatus] = useState('')
  const [filter, setFilter] = useState<'all' | 'active' | 'inactive'>('all')
  const [search, setSearch] = useState('')
  const [minCapacity, setMinCapacity] = useState('')
  const [sortBy, setSortBy] = useState<'capacity' | 'local' | 'remote' | 'alias'>('local')
  const [sortDir, setSortDir] = useState<'desc' | 'asc'>('asc')
  const [showPrivate, setShowPrivate] = useState(true)

  const [peerAddress, setPeerAddress] = useState('')
  const [peerTemporary, setPeerTemporary] = useState(false)
  const [peerStatus, setPeerStatus] = useState('')
  const [boostStatus, setBoostStatus] = useState('')
  const [boostRunning, setBoostRunning] = useState(false)
  const [peers, setPeers] = useState<Peer[]>([])
  const [peerListStatus, setPeerListStatus] = useState('')
  const [peerActionStatus, setPeerActionStatus] = useState('')

  const [amboss, setAmboss] = useState<AmbossHealthStatus | null>(null)
  const [ambossStatus, setAmbossStatus] = useState('')
  const [ambossBusy, setAmbossBusy] = useState(false)
  const [chanHeal, setChanHeal] = useState<ChanHealStatus | null>(null)
  const [chanHealStatus, setChanHealStatus] = useState('')
  const [chanHealBusy, setChanHealBusy] = useState(false)
  const [chanHealInterval, setChanHealInterval] = useState('300')
  const [htlcManager, setHtlcManager] = useState<HtlcManagerStatus | null>(null)
  const [htlcManagerStatus, setHtlcManagerStatus] = useState('')
  const [htlcManagerBusy, setHtlcManagerBusy] = useState(false)
  const [htlcManagerIntervalMinutes, setHtlcManagerIntervalMinutes] = useState('240')
  const [htlcManagerMinSat, setHtlcManagerMinSat] = useState('1')
  const [htlcManagerMaxPct, setHtlcManagerMaxPct] = useState('0')
  const [htlcManagerLogs, setHtlcManagerLogs] = useState<HtlcManagerLogEntry[]>([])
  const [htlcManagerLogsOpen, setHtlcManagerLogsOpen] = useState(false)
  const [htlcManagerFailed, setHtlcManagerFailed] = useState<HtlcManagerFailedEntry[]>([])
  const [htlcManagerFailedOpen, setHtlcManagerFailedOpen] = useState(false)
  const [torPeerChecker, setTorPeerChecker] = useState<TorPeerCheckerStatus | null>(null)
  const [torPeerCheckerStatus, setTorPeerCheckerStatus] = useState('')
  const [torPeerCheckerBusy, setTorPeerCheckerBusy] = useState(false)
  const [torPeerCheckerIntervalHours, setTorPeerCheckerIntervalHours] = useState('2')
  const [torPeerCheckerLogs, setTorPeerCheckerLogs] = useState<TorPeerCheckerLogEntry[]>([])
  const [torPeerCheckerLogsOpen, setTorPeerCheckerLogsOpen] = useState(false)
  const [signMessage, setSignMessage] = useState('')
  const [signSignature, setSignSignature] = useState('')
  const [signStatus, setSignStatus] = useState('')
  const [signBusy, setSignBusy] = useState(false)
  const [signCopied, setSignCopied] = useState(false)
  const [bitcoinLocal, setBitcoinLocal] = useState<BitcoinLocalStatus | null>(null)

  const [autofeeConfig, setAutofeeConfig] = useState<AutofeeConfig | null>(null)
  const [autofeeStatus, setAutofeeStatus] = useState<AutofeeStatus | null>(null)
  const [autofeeSettings, setAutofeeSettings] = useState<Record<number, boolean>>({})
  const [autofeeBusy, setAutofeeBusy] = useState(false)
  const [autofeeMessage, setAutofeeMessage] = useState('')
  const [autofeeEnabled, setAutofeeEnabled] = useState(false)
  const [autofeeProfile, setAutofeeProfile] = useState('moderate')
  const [autofeeLookback, setAutofeeLookback] = useState('7')
  const [autofeeIntervalHours, setAutofeeIntervalHours] = useState('4')
  const [autofeeCooldownUp, setAutofeeCooldownUp] = useState('3')
  const [autofeeCooldownDown, setAutofeeCooldownDown] = useState('4')
  const [autofeeRebalMode, setAutofeeRebalMode] = useState('blend')
  const [autofeeMinPpm, setAutofeeMinPpm] = useState('10')
  const [autofeeMaxPpm, setAutofeeMaxPpm] = useState('2000')
  const [autofeeAmbossEnabled, setAutofeeAmbossEnabled] = useState(false)
  const [autofeeAmbossToken, setAutofeeAmbossToken] = useState('')
  const [autofeeInboundPassive, setAutofeeInboundPassive] = useState(false)
  const [autofeeDiscovery, setAutofeeDiscovery] = useState(true)
  const [autofeeExplorer, setAutofeeExplorer] = useState(true)
  const [autofeeSuperSource, setAutofeeSuperSource] = useState(false)
  const [autofeeSuperSourceBaseFee, setAutofeeSuperSourceBaseFee] = useState('1000')
  const [autofeeRevfloor, setAutofeeRevfloor] = useState(true)
  const [autofeeCircuitBreaker, setAutofeeCircuitBreaker] = useState(true)
  const [autofeeExtremeDrain, setAutofeeExtremeDrain] = useState(true)
  const [autofeeHtlcSignalEnabled, setAutofeeHtlcSignalEnabled] = useState(true)
  const [autofeeHtlcMode, setAutofeeHtlcMode] = useState('full')
  const [autofeeOpen, setAutofeeOpen] = useState(false)
  const [autofeeResultsOpen, setAutofeeResultsOpen] = useState(false)
  const [autofeeResults, setAutofeeResults] = useState<string[]>([])
  const [autofeeResultItems, setAutofeeResultItems] = useState<AutofeeResultItem[]>([])
  const [autofeeResultsStatus, setAutofeeResultsStatus] = useState('')
  const [autofeeResultsRuns, setAutofeeResultsRuns] = useState('4')
  const [autofeeResultsFrom, setAutofeeResultsFrom] = useState('')
  const [autofeeResultsTo, setAutofeeResultsTo] = useState('')

  const [chanStatusBusy, setChanStatusBusy] = useState<string | null>(null)
  const [chanStatusMessage, setChanStatusMessage] = useState('')

  const [openPeer, setOpenPeer] = useState('')
  const [openAmount, setOpenAmount] = useState('')
  const [openCloseAddress, setOpenCloseAddress] = useState('')
  const [openFeeRate, setOpenFeeRate] = useState('')
  const [openFeeHint, setOpenFeeHint] = useState<{ fastest?: number; hour?: number } | null>(null)
  const [openFeeStatus, setOpenFeeStatus] = useState('')
  const [openPrivate, setOpenPrivate] = useState(false)
  const [openStatus, setOpenStatus] = useState('')
  const [openChannelPoint, setOpenChannelPoint] = useState('')

  const [closePoint, setClosePoint] = useState('')
  const [closeForce, setCloseForce] = useState(false)
  const [closeFeeRate, setCloseFeeRate] = useState('')
  const [closeFeeHint, setCloseFeeHint] = useState<{ fastest?: number; hour?: number } | null>(null)
  const [closeFeeStatus, setCloseFeeStatus] = useState('')
  const [closeStatus, setCloseStatus] = useState('')

  const [feeScopeAll, setFeeScopeAll] = useState(true)
  const [feeChannelPoint, setFeeChannelPoint] = useState('')
  const [baseFeeMsat, setBaseFeeMsat] = useState('')
  const [feeRatePpm, setFeeRatePpm] = useState('')
  const [timeLockDelta, setTimeLockDelta] = useState('')
  const [inboundEnabled, setInboundEnabled] = useState(false)
  const [inboundBaseMsat, setInboundBaseMsat] = useState('')
  const [inboundFeeRatePpm, setInboundFeeRatePpm] = useState('')
  const [feeLoadStatus, setFeeLoadStatus] = useState('')
  const [feeLoading, setFeeLoading] = useState(false)
  const [inlineFeeChannelPoint, setInlineFeeChannelPoint] = useState('')
  const [inlineFeeRatePpm, setInlineFeeRatePpm] = useState('')
  const [inlineBaseFeeMsat, setInlineBaseFeeMsat] = useState('')
  const [inlineInboundFeeRatePpm, setInlineInboundFeeRatePpm] = useState('')
  const [inlineInboundBaseMsat, setInlineInboundBaseMsat] = useState('0')
  const [inlineTimeLockDelta, setInlineTimeLockDelta] = useState('0')
  const [inlineFeeStatus, setInlineFeeStatus] = useState('')
  const [inlineFeeLoading, setInlineFeeLoading] = useState(false)
  const [inlineFeeSaving, setInlineFeeSaving] = useState(false)
  const chanHealLastAttemptRef = useRef('')
  const chanHealIntervalDirtyRef = useRef(false)
  const htlcManagerFormDirtyRef = useRef(false)
  const torPeerCheckerIntervalDirtyRef = useRef(false)
  const [feeStatus, setFeeStatus] = useState('')

  const formatPing = (value: number) => {
    if (!value || value <= 0) return t('common.na')
    const ms = value / 1000
    if (ms < 1000) return t('lightningOps.pingMs', { value: ms.toFixed(1) })
    return t('lightningOps.pingSeconds', { value: (ms / 1000).toFixed(1) })
  }

  const formatAge = (timestamp?: number) => {
    if (!timestamp) return ''
    const ageMs = Date.now() - timestamp * 1000
    if (ageMs <= 0) return t('common.justNow')
    const seconds = Math.floor(ageMs / 1000)
    if (seconds < 60) return t('lightningOps.ageSeconds', { count: seconds })
    const minutes = Math.floor(seconds / 60)
    if (minutes < 60) return t('lightningOps.ageMinutes', { count: minutes })
    const hours = Math.floor(minutes / 60)
    if (hours < 24) return t('lightningOps.ageHours', { count: hours })
    const days = Math.floor(hours / 24)
    return t('lightningOps.ageDays', { count: days })
  }

  const formatAmbossTime = (value?: string) => {
    if (!value) return t('common.na')
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return t('common.unknownTime')
    return date.toLocaleString()
  }

  const formatSatFromMsat = (value?: number) => {
    if (!value || value <= 0) return '-'
    return Math.round(value / 1000).toLocaleString()
  }

  const formatFeeMsat = (value?: number) => {
    if (typeof value !== 'number' || value < 0) return '-'
    return value.toLocaleString()
  }

  const autofeeProfileDefaults: Record<string, { interval: number; cooldownUp: number; cooldownDown: number }> = {
    conservative: { interval: 8, cooldownUp: 6, cooldownDown: 8 },
    moderate: { interval: 4, cooldownUp: 3, cooldownDown: 4 },
    aggressive: { interval: 2, cooldownUp: 1, cooldownDown: 2 }
  }

  const autofeeAllChecked = useMemo(() => {
    if (!channels.length) return true
    return channels.every((ch) => (autofeeSettings[ch.channel_id] ?? true))
  }, [channels, autofeeSettings])

  const formattedAutofeeResults = useMemo(() => {
    return autofeeResults.map((line) => {
      if (!line.startsWith('\u26a1')) {
        return line
      }
      const idx = line.lastIndexOf('|')
      if (idx <= 0) {
        return line
      }
      const raw = line.slice(idx + 1).trim()
      const parsed = new Date(raw)
      if (Number.isNaN(parsed.getTime())) {
        return line
      }
      return `${line.slice(0, idx + 1)} ${parsed.toLocaleString()}`
    })
  }, [autofeeResults])

  const visibleHtlcManagerFailed = useMemo(() => {
    return htlcManagerFailed.filter((entry) => {
      if ((entry.event || '').toLowerCase() !== 'forward_fail') {
        return true
      }
      const detail = (entry.failure_detail || '').trim()
      const reason = (entry.failure_reason || '').trim()
      return detail !== '' || reason !== ''
    })
  }, [htlcManagerFailed])

  const formatAutofeeReasonLabel = (reason?: string) => {
    const normalized = (reason || '').toLowerCase()
    if (normalized === 'manual') return t('lightningOps.autofeeResultsReasonManual')
    if (normalized === 'scheduled') return t('lightningOps.autofeeResultsReasonScheduled')
    if (reason) return reason.toUpperCase()
    return t('lightningOps.autofeeResultsReasonUnknown')
  }

  const formatSatsCompact = (value?: number) => {
    const sats = Number(value || 0)
    if (!sats) return `0 ${t('lightningOps.autofeeResultsSats')}`
    if (sats >= 100_000_000) {
      return `${(sats / 100_000_000).toFixed(2)} BTC`
    }
    if (sats >= 1_000_000) {
      return `${(sats / 1_000_000).toFixed(1)}M ${t('lightningOps.autofeeResultsSats')}`
    }
    if (sats >= 1_000) {
      return `${(sats / 1_000).toFixed(1)}k ${t('lightningOps.autofeeResultsSats')}`
    }
    return `${sats} ${t('lightningOps.autofeeResultsSats')}`
  }

  const formatAutofeeNodeClass = (value?: string) => {
    const normalized = (value || '').toLowerCase()
    switch (normalized) {
      case 'small':
        return t('lightningOps.autofeeResultsNodeSmall')
      case 'medium':
        return t('lightningOps.autofeeResultsNodeMedium')
      case 'large':
        return t('lightningOps.autofeeResultsNodeLarge')
      case 'xl':
        return t('lightningOps.autofeeResultsNodeXL')
      default:
        return t('common.unknown')
    }
  }

  const formatAutofeeLiquidityClass = (value?: string) => {
    const normalized = (value || '').toLowerCase()
    switch (normalized) {
      case 'drained':
        return t('lightningOps.autofeeResultsLiquidityDrained')
      case 'full':
        return t('lightningOps.autofeeResultsLiquidityFull')
      case 'balanced':
        return t('lightningOps.autofeeResultsLiquidityBalanced')
      default:
        return t('common.unknown')
    }
  }

  const formatAutofeeCalib = (item: AutofeeResultItem) => {
    const channels = item.channel_count ?? 0
    const cap = formatSatsCompact(item.total_capacity_sat)
    const avg = formatSatsCompact(item.avg_capacity_sat)
    const local = formatSatsCompact(item.local_capacity_sat)
    const ratio = typeof item.local_ratio === 'number' ? Math.round(item.local_ratio * 100) : 0
    const nodeClass = formatAutofeeNodeClass(item.node_class)
    const liqClass = formatAutofeeLiquidityClass(item.liquidity_class)
    const revfloorThr = item.revfloor_baseline ?? 0
    const revfloorMin = item.revfloor_min_abs ?? 0
    let line = t('lightningOps.autofeeResultsCalib', {
      node: nodeClass,
      channels,
      cap,
      avg,
      local,
      ratio,
      liq: liqClass,
      revfloorThr,
      revfloorMin
    })
    if (
      typeof item.htlc_node_factor === 'number' &&
      typeof item.htlc_liquidity_factor === 'number' &&
      typeof item.htlc_threshold_factor === 'number'
    ) {
      line += ` | htlc_k node=${item.htlc_node_factor.toFixed(2)} liq=${item.htlc_liquidity_factor.toFixed(2)} total=${item.htlc_threshold_factor.toFixed(2)}`
    }
    return line
  }

  const formatAutofeeHeader = (item: AutofeeResultItem) => {
    const reasonLabel = formatAutofeeReasonLabel(item.reason)
    const dryLabel = item.dry_run ? t('lightningOps.autofeeResultsDryRunTag') : ''
    const ts = item.timestamp ? new Date(item.timestamp) : null
    const timeLabel = ts && !Number.isNaN(ts.getTime()) ? ts.toLocaleString() : t('common.na')
    return t('lightningOps.autofeeResultsHeader', { reason: reasonLabel, dry: dryLabel, time: timeLabel })
  }

  const formatAutofeeSummary = (item: AutofeeResultItem) => {
    const parts = [
      `${t('lightningOps.autofeeResultsUp')} ${item.up ?? 0}`,
      `${t('lightningOps.autofeeResultsDown')} ${item.down ?? 0}`,
      `${t('lightningOps.autofeeResultsFlat')} ${item.flat ?? 0}`,
      `${t('lightningOps.autofeeResultsCooldown')} ${item.cooldown ?? 0}`,
      `${t('lightningOps.autofeeResultsSmall')} ${item.small ?? 0}`,
      `${t('lightningOps.autofeeResultsSame')} ${item.same ?? 0}`,
      `${t('lightningOps.autofeeResultsDisabled')} ${item.disabled ?? 0}`,
      `${t('lightningOps.autofeeResultsInactive')} ${item.inactive ?? 0}`,
      `${t('lightningOps.autofeeResultsInboundDisc')} ${item.inbound_disc ?? 0}`,
      `${t('lightningOps.autofeeResultsSuperSource')} ${item.super_source ?? 0}`,
      `htlc_liq_hot ${item.htlc_liq_hot ?? 0}`,
      `htlc_policy_hot ${item.htlc_policy_hot ?? 0}`,
      `htlc_low_sample ${item.htlc_sample_low ?? 0}`,
      `htlc_window ${(item.htlc_window_min ?? 0)}m`
    ]
    if (
      typeof item.htlc_min_attempts === 'number' &&
      typeof item.htlc_min_policy_fails === 'number' &&
      typeof item.htlc_min_liquidity_fails === 'number'
    ) {
      parts.push(
        `htlc_min a>=${item.htlc_min_attempts} p>=${item.htlc_min_policy_fails} l>=${item.htlc_min_liquidity_fails}`
      )
    }
    return `📊 ${parts.join(' | ')}`
  }

  const formatAutofeeSeed = (item: AutofeeResultItem) => {
    const parts = [
      `${t('lightningOps.autofeeResultsSeedAmboss')}=${item.amboss ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedMissing')}=${item.missing ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedErr')}=${item.err ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedEmpty')}=${item.empty ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedOutrate')}=${item.outrate ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedMem')}=${item.mem ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedDefault')}=${item.default ?? 0}`
    ]
    let line = `🌱 ${t('lightningOps.autofeeResultsSeedLabel')} ${parts.join(' ')}`
    if (item.cooldown_ignored) {
      line += ` | ${t('lightningOps.autofeeResultsCooldownIgnored')}=1`
    }
    return line
  }

  const formatAutofeeTags = (tags: string[] = [], inboundDiscount?: number, classLabel?: string) => {
    const output: string[] = []
    const seen = new Set<string>()
    const add = (tag: string) => {
      if (!tag) return
      if (seen.has(tag)) return
      seen.add(tag)
      output.push(tag)
    }

    switch ((classLabel || '').toLowerCase().trim()) {
      case 'sink':
        add('🏷️sink')
        break
      case 'source':
        add('🏷️source')
        break
      case 'router':
        add('🏷️router')
        break
      case 'unknown':
        add('🏷️unknown')
        break
      default:
        break
    }

    tags.forEach((tag) => {
      if (!tag) return
      if (tag === 'discovery') {
        add('🧭discovery')
      } else if (tag === 'discovery-hard') {
        add('🧨harddrop')
      } else if (tag === 'explorer') {
        add('🧭explorer')
      } else if (tag.startsWith('surge')) {
        add(`📈${tag}`)
      } else if (tag === 'top-rev') {
        add('💎top-rev')
      } else if (tag === 'neg-margin') {
        add('⚠️neg-margin')
      } else if (tag === 'htlc-policy-hot') {
        add('🧾policy-hot')
      } else if (tag === 'htlc-liquidity-hot') {
        add('💧liq-hot')
      } else if (tag === 'htlc-sample-low') {
        add('📉htlc-low-sample')
      } else if (tag === 'htlc-neutral-lock') {
        add('🧯htlc-neutral')
      } else if (tag.startsWith('htlc-liq+')) {
        add('💧' + tag)
      } else if (tag.startsWith('htlc-policy+')) {
        add('🧾' + tag)
      } else if (tag === 'htlc-liq-nodown') {
        add('🛑liq-nodown')
      } else if (tag === 'htlc-policy-nodown') {
        add('🛑policy-nodown')
      } else if (tag === 'htlc-neutral-nodown') {
        add('🧯neutral-nodown')
      } else if (tag === 'htlc-step-boost') {
        add('⚡htlc-step')
      } else if (tag.startsWith('negm+')) {
        add(`💹${tag}`)
      } else if (tag === 'outrate-floor') {
        add('📊outrate-floor')
      } else if (tag === 'circuit-breaker') {
        add('🧯cb')
      } else if (tag === 'extreme-drain') {
        add('⚡extreme')
      } else if (tag === 'extreme-drain-turbo') {
        add('⚡turbo')
      } else if (tag === 'revfloor') {
        add('🧱revfloor')
      } else if (tag === 'peg') {
        add('📌peg')
      } else if (tag === 'peg-grace') {
        add('📌peg-grace')
      } else if (tag === 'peg-demand') {
        add('📌peg-demand')
      } else if (tag === 'cooldown') {
        add('⏳cooldown')
      } else if (tag === 'cooldown-profit') {
        add('⏳profit-hold')
      } else if (tag === 'cooldown-skip') {
        add('🧭skip-cooldown')
      } else if (tag === 'hold-small') {
        add('🧊hold-small')
      } else if (tag === 'same-ppm') {
        add('🟰same-ppm')
      } else if (tag === 'no-down-low') {
        add('🚫down-low')
      } else if (tag === 'no-down-neg-margin') {
        add('🚫down-neg')
      } else if (tag === 'super-source') {
        add('🔥super-source')
      } else if (tag === 'super-source-like') {
        add('🔥super-source-like')
      } else if (tag === 'sink-floor') {
        add('🧱sink-floor')
      } else if (tag === 'trend-up') {
        add('📈trend-up')
      } else if (tag === 'trend-down') {
        add('📉trend-down')
      } else if (tag === 'trend-flat') {
        add('➡️trend-flat')
      } else if (tag.startsWith('seed:amboss')) {
        add(`🌐${tag.replace('seed:', 'seed-')}`)
      } else if (tag.startsWith('seed:med')) {
        add('📐seed-med')
      } else if (tag.startsWith('seed:vol')) {
        add(`📉${tag.replace('seed:', 'seed-')}`)
      } else if (tag.startsWith('seed:ratio')) {
        add(`🔁${tag.replace('seed:', 'seed-')}`)
      } else if (tag.startsWith('seed:outrate')) {
        add('📊seed-outrate')
      } else if (tag.startsWith('seed:mem')) {
        add('💾seed-mem')
      } else if (tag.startsWith('seed:default')) {
        add('⚙️seed-default')
      } else if (tag.startsWith('seed:guard')) {
        add('🛡️seed-guard')
      } else if (tag.startsWith('seed:p95cap')) {
        add('🧢seed-p95')
      } else if (tag.startsWith('seed:absmax')) {
        add('🧱seed-cap')
      } else {
        add(tag)
      }
    })

    if (inboundDiscount && inboundDiscount > 0) {
      add(`↘️inb-${inboundDiscount}`)
    }
    return output.join(' ')
  }

  const formatChannelClassLabel = (label?: string) => {
    const normalized = (label || '').toLowerCase().trim()
    switch (normalized) {
      case 'sink':
        return '🏷️ sink'
      case 'source':
        return '🏷️ source'
      case 'router':
        return '🏷️ router'
      default:
        return ''
    }
  }

  const formatAutofeePrediction = (item: AutofeeResultItem) => {
    const code = (item.prediction_code || '').trim()
    if (!code) return ''
    const hours = typeof item.prediction_cooldown_hours === 'number' ? item.prediction_cooldown_hours : 0
    switch (code) {
      case 'hold_or_up':
        return t('lightningOps.autofeeResultsPredictionHoldOrUp')
      case 'reduce':
        return t('lightningOps.autofeeResultsPredictionReduce')
      case 'discovery_fast':
        return t('lightningOps.autofeeResultsPredictionDiscoveryFast')
      case 'idle_reduce':
        return t('lightningOps.autofeeResultsPredictionIdleReduce')
      case 'bias_up':
        if (hours > 0) {
          return t('lightningOps.autofeeResultsPredictionBiasUpCooldown', { hours })
        }
        return t('lightningOps.autofeeResultsPredictionBiasUp')
      case 'bias_down':
        return t('lightningOps.autofeeResultsPredictionBiasDown')
      case 'stable':
        return t('lightningOps.autofeeResultsPredictionStable')
      default:
        return ''
    }
  }

  const formatAutofeeChannelLine = (item: AutofeeResultItem) => {
    const alias = (item.alias || '').trim() || (item.channel_id ? `chan-${item.channel_id}` : t('common.unknown'))
    const localPpm = item.local_ppm ?? 0
    const newPpm = item.new_ppm ?? localPpm
    const delta = item.delta ?? (newPpm - localPpm)
    const deltaPct = item.delta_pct ?? (localPpm > 0 && newPpm !== localPpm ? Math.abs(delta) / localPpm * 100 : 0)
    const deltaStr = localPpm > 0 && newPpm !== localPpm ? ` (${delta >= 0 ? '+' : ''}${delta}, ${deltaPct.toFixed(1)}%)` : ''

    let dir = '➡️'
    if (newPpm > localPpm) {
      dir = '🔺'
    } else if (newPpm < localPpm) {
      dir = '🔻'
    }

    const tags = item.tags ?? []
    const isCooldown = tags.includes('cooldown')
    const isHoldSmall = tags.includes('hold-small')
    const isSame = tags.includes('same-ppm')

    let prefix = '🫤'
    if (item.category === 'changed') {
      prefix = `✅${dir}`
    } else if (item.category === 'skipped') {
      if (isCooldown) {
        prefix = '⏭️⏳'
      } else if (isHoldSmall) {
        prefix = '⏭️🧊'
      } else {
        prefix = '⏭️'
      }
    } else if (item.category === 'error') {
      prefix = '❌'
    } else if (isSame) {
      prefix = '🫤⏸️'
    }

    let action = ''
    if (item.category === 'error') {
      action = t('lightningOps.autofeeResultsActionError', { error: item.error || item.skip_reason || t('common.unknown') })
    } else if (item.category === 'changed') {
      action = item.dry_run
        ? t('lightningOps.autofeeResultsActionDrySet', { from: localPpm, to: newPpm })
        : t('lightningOps.autofeeResultsActionSet', { from: localPpm, to: newPpm })
    } else {
      action = t('lightningOps.autofeeResultsActionKeep', { value: localPpm })
    }

    const outRatio = typeof item.out_ratio === 'number' ? item.out_ratio : 0
    const outPpm7d = item.out_ppm7d ?? 0
    const rebalPpm7d = item.rebal_ppm7d ?? 0
    const seed = item.seed ?? 0
    const floor = item.floor ?? 0
    const floorSrc = item.floor_src ? `(${item.floor_src})` : ''
    const margin = item.margin ?? 0
    const revShare = typeof item.rev_share === 'number' ? item.rev_share : 0
    const tagLine = formatAutofeeTags(tags, item.inbound_discount, item.class_label) || '-'
    const htlcAttempts = item.htlc_attempts ?? 0
    const htlcPolicyFails = item.htlc_policy_fails ?? 0
    const htlcLiquidityFails = item.htlc_liquidity_fails ?? 0
    const htlcWindow = item.htlc_window_min_channel ?? item.htlc_window_min ?? 0

    const prediction = formatAutofeePrediction(item)
    let baseLine = `${prefix} ${alias}: ${action}${deltaStr} | ${t('lightningOps.autofeeResultsLabelTarget')} ${item.target ?? 0} | ${t('lightningOps.autofeeResultsLabelOutRatio')} ${outRatio.toFixed(2)} | ${t('lightningOps.autofeeResultsLabelOutPpm7d')}≈${outPpm7d} | ${t('lightningOps.autofeeResultsLabelRebalPpm7d')}≈${rebalPpm7d} | ${t('lightningOps.autofeeResultsLabelSeed')}≈${seed} | ${t('lightningOps.autofeeResultsLabelFloor')}≥${floor}${floorSrc} | ${t('lightningOps.autofeeResultsLabelMargin')}≈${margin} | ${t('lightningOps.autofeeResultsLabelRevShare')}≈${revShare.toFixed(2)} | ${tagLine}`
    if (htlcAttempts > 0) {
      baseLine += ` | htlc${Math.max(1, htlcWindow)}m a=${htlcAttempts} p=${htlcPolicyFails} l=${htlcLiquidityFails}`
    }
    return prediction ? `${baseLine} | ${prediction}` : baseLine
  }

  const formatAutofeeSectionLine = (category?: string) => {
    switch ((category || '').toLowerCase()) {
      case 'changed':
        return `✅ ${t('lightningOps.autofeeResultsSectionChanged')}`
      case 'kept':
        return `🟰 ${t('lightningOps.autofeeResultsSectionNoChange')}`
      case 'skipped':
        return `🟰 ${t('lightningOps.autofeeResultsSectionNoChange')}`
      default:
        return ''
    }
  }

  const formatAutofeeExplorerLine = (item: AutofeeResultItem) => {
    const alias = (item.alias || '').trim() || (item.channel_id ? `chan-${item.channel_id}` : t('common.unknown'))
    return `🧭 ${alias} ${t('lightningOps.autofeeResultsExplorerOn')}`
  }

  const buildAutofeeResultsQuery = () => {
    const runsValue = Math.max(1, Number(autofeeResultsRuns || 4))
    const payload: { runs: number; from?: string; to?: string } = { runs: runsValue }
    if (autofeeResultsFrom) {
      payload.from = autofeeResultsFrom
    }
    if (autofeeResultsTo) {
      payload.to = autofeeResultsTo
    }
    return payload
  }

  const localizedAutofeeResults = useMemo(() => {
    if (!autofeeResultItems.length) {
      return formattedAutofeeResults
    }
    const lines: string[] = []
    const max = Math.max(autofeeResults.length, autofeeResultItems.length)
    for (let i = 0; i < max; i += 1) {
      const item = autofeeResultItems[i]
      let line = ''
      if (item && item.kind) {
        switch (item.kind) {
          case 'header':
            line = formatAutofeeHeader(item)
            break
          case 'summary':
            line = formatAutofeeSummary(item)
            break
          case 'seed':
            line = formatAutofeeSeed(item)
            break
          case 'calib':
            line = formatAutofeeCalib(item)
            break
          case 'section':
            line = formatAutofeeSectionLine(item.category)
            break
          case 'explorer':
            line = formatAutofeeExplorerLine(item)
            break
          case 'channel':
            line = formatAutofeeChannelLine(item)
            break
          default:
            line = ''
            break
        }
      }
      if (!line && formattedAutofeeResults[i]) {
        line = formattedAutofeeResults[i]
      }
      if (line) {
        lines.push(line)
      }
    }
    return lines.length ? lines : formattedAutofeeResults
  }, [autofeeResultItems, autofeeResults.length, formattedAutofeeResults, t])

  const blockCadenceAvg = useMemo(() => {
    const buckets = bitcoinLocal?.block_cadence || []
    if (!buckets.length) return 0
    const windowSec = bitcoinLocal?.block_cadence_window_sec || 600
    const cadenceHours = (buckets.length * windowSec) / 3600
    if (cadenceHours <= 0) return 0
    const total = buckets.reduce((sum, bucket) => sum + (bucket?.count || 0), 0)
    return cadenceHours > 0 ? total / cadenceHours : 0
  }, [bitcoinLocal])

  const estimateMaturitySeconds = (blocks?: number) => {
    if (typeof blocks !== 'number') return null
    const secondsPerBlock = blockCadenceAvg > 0 ? 3600 / blockCadenceAvg : 600
    return Math.max(0, Math.round(blocks * secondsPerBlock))
  }

  const formatMaturityDuration = (totalSeconds?: number | null) => {
    if (totalSeconds === null || totalSeconds === undefined) return ''
    const seconds = Math.max(0, Math.floor(totalSeconds))
    const days = Math.floor(seconds / 86400)
    const hours = Math.floor((seconds % 86400) / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    const remSeconds = seconds % 60
    return `${days}d ${hours}h ${minutes}m ${remSeconds}s`
  }

  const isLocalChanDisabled = (flags?: string) => {
    if (!flags) return false
    const normalized = flags.toLowerCase()
    const tokens = normalized.split(/[|,;\s]+/).filter(Boolean)
    const scan = tokens.length ? tokens : [normalized]
    return scan.some((token) => {
      if (token.includes('localchandisabled') || token.includes('local_chan_disabled')) return true
      if (!token.includes('disabled') || token.includes('remote')) return false
      return token.includes('local') || token.includes('chanstatusdisabled') || token === 'disabled'
    })
  }

  const ambossTone = (): 'ok' | 'warn' | 'muted' => {
    if (!amboss?.enabled) return 'muted'
    if (amboss?.status === 'ok') return 'ok'
    if (amboss?.status === 'checking') return 'muted'
    return 'warn'
  }

  const chanHealTone = (): 'ok' | 'warn' | 'muted' => {
    if (!chanHeal?.enabled) return 'muted'
    if (chanHeal?.status === 'ok') return 'ok'
    if (chanHeal?.status === 'checking') return 'muted'
    return 'warn'
  }

  const htlcManagerTone = (): 'ok' | 'warn' | 'muted' => {
    if (!htlcManager?.enabled) return 'muted'
    if (htlcManager?.status === 'ok') return 'ok'
    if (htlcManager?.status === 'checking') return 'muted'
    return 'warn'
  }

  const torPeerCheckerTone = (): 'ok' | 'warn' | 'muted' => {
    if (!torPeerChecker?.enabled) return 'muted'
    if (torPeerChecker?.status === 'ok') return 'ok'
    if (torPeerChecker?.status === 'checking') return 'muted'
    return 'warn'
  }

  const badgeClass = (tone: 'ok' | 'warn' | 'muted') => {
    if (tone === 'ok') {
      return 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
    }
    if (tone === 'warn') {
      return 'bg-amber-500/15 text-amber-200 border border-amber-400/30'
    }
    return 'bg-white/10 text-fog/60 border border-white/10'
  }

  const ambossBadgeLabel = () => {
    if (!amboss?.enabled) return t('common.disabled')
    if (amboss?.status === 'ok') return t('common.ok')
    if (amboss?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const chanHealBadgeLabel = () => {
    if (!chanHeal?.enabled) return t('common.disabled')
    if (chanHeal?.status === 'ok') return t('common.ok')
    if (chanHeal?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const htlcManagerBadgeLabel = () => {
    if (!htlcManager?.enabled) return t('common.disabled')
    if (htlcManager?.status === 'ok') return t('common.ok')
    if (htlcManager?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const torPeerCheckerBadgeLabel = () => {
    if (!torPeerChecker?.enabled) return t('common.disabled')
    if (torPeerChecker?.status === 'ok') return t('common.ok')
    if (torPeerChecker?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const ambossURL = (pubkey: string) => `https://amboss.space/node/${pubkey}`

  const applyChannelsPayload = (res: any) => {
    const list = Array.isArray(res?.channels) ? res.channels : []
    setChannels(list)
    setActiveCount(res?.active_count ?? 0)
    setInactiveCount(res?.inactive_count ?? 0)
    setPendingOpenCount(res?.pending_open_count ?? 0)
    setPendingCloseCount(res?.pending_close_count ?? 0)
    setPendingChannels(Array.isArray(res?.pending_channels) ? res.pending_channels : [])
  }

  const load = async () => {
    setStatus(t('lightningOps.loadingChannels'))
    setPeerListStatus(t('lightningOps.loadingPeers'))
    setAmbossStatus(t('lightningOps.ambossHealthLoading'))
    setChanHealStatus(t('lightningOps.chanHealLoading'))
    setHtlcManagerStatus(t('lightningOps.htlcManagerLoading'))
    setTorPeerCheckerStatus(t('lightningOps.torPeerLoading'))
    setAutofeeMessage(t('lightningOps.autofeeLoading'))
    const [channelsResult, peersResult, ambossResult, chanHealResult, htlcManagerResult, htlcManagerLogsResult, htlcManagerFailedResult, torPeerCheckerResult, torPeerCheckerLogsResult, bitcoinLocalResult, autofeeConfigResult, autofeeStatusResult, autofeeChannelsResult, autofeeResultsResult] = await Promise.allSettled([
      getLnChannels(),
      getLnPeers(),
      getAmbossHealth(),
      getLnChanHeal(),
      getLnHtlcManager(),
      getLnHtlcManagerLogs(),
      getLnHtlcManagerFailed(),
      getLnTorPeerChecker(),
      getLnTorPeerCheckerLogs(),
      getBitcoinLocalStatus(),
      getAutofeeConfig(),
      getAutofeeStatus(),
      getAutofeeChannels(),
      getAutofeeResults(buildAutofeeResultsQuery())
    ])
    if (channelsResult.status === 'fulfilled') {
      const res = channelsResult.value
      applyChannelsPayload(res)
      setStatus('')
    } else {
      const message = (channelsResult.reason as any)?.message || t('lightningOps.loadChannelsFailed')
      setStatus(message)
    }
    if (peersResult.status === 'fulfilled') {
      const res = peersResult.value
      setPeers(Array.isArray(res?.peers) ? res.peers : [])
      setPeerListStatus('')
    } else {
      const message = (peersResult.reason as any)?.message || t('lightningOps.loadPeersFailed')
      setPeerListStatus(message)
    }
    if (ambossResult.status === 'fulfilled') {
      setAmboss(ambossResult.value as AmbossHealthStatus)
      setAmbossStatus('')
    } else {
      const message = (ambossResult.reason as any)?.message || t('lightningOps.ambossHealthStatusUnavailable')
      setAmbossStatus(message)
    }
    if (chanHealResult.status === 'fulfilled') {
      const payload = chanHealResult.value as ChanHealStatus
      setChanHeal(payload)
      chanHealLastAttemptRef.current = payload?.last_attempt_at || ''
      if (payload?.interval_sec) {
        setChanHealInterval(String(payload.interval_sec))
        chanHealIntervalDirtyRef.current = false
      }
      setChanHealStatus('')
    } else {
      const message = (chanHealResult.reason as any)?.message || t('lightningOps.chanHealStatusUnavailable')
      setChanHealStatus(message)
    }
    if (htlcManagerResult.status === 'fulfilled') {
      const payload = htlcManagerResult.value as HtlcManagerStatus
      setHtlcManager(payload)
      const intervalMinutes = payload?.interval_minutes ?? ((payload?.interval_hours ?? 0) * 60)
      if (intervalMinutes > 0) {
        setHtlcManagerIntervalMinutes(String(intervalMinutes))
      }
      if (payload?.min_htlc_sat) {
        setHtlcManagerMinSat(String(payload.min_htlc_sat))
      }
      setHtlcManagerMaxPct(String(payload?.max_local_pct ?? 0))
      htlcManagerFormDirtyRef.current = false
      setHtlcManagerStatus('')
    } else {
      const message = (htlcManagerResult.reason as any)?.message || t('lightningOps.htlcManagerStatusUnavailable')
      setHtlcManagerStatus(message)
    }
    if (htlcManagerLogsResult.status === 'fulfilled') {
      const entries = (htlcManagerLogsResult.value as any)?.entries
      setHtlcManagerLogs(Array.isArray(entries) ? entries : [])
    } else if (htlcManagerResult.status === 'fulfilled') {
      setHtlcManagerLogs([])
    }
    if (htlcManagerFailedResult.status === 'fulfilled') {
      const entries = (htlcManagerFailedResult.value as any)?.entries
      setHtlcManagerFailed(Array.isArray(entries) ? entries : [])
    } else if (htlcManagerResult.status === 'fulfilled') {
      setHtlcManagerFailed([])
    }
    if (torPeerCheckerResult.status === 'fulfilled') {
      const payload = torPeerCheckerResult.value as TorPeerCheckerStatus
      setTorPeerChecker(payload)
      setTorPeerCheckerIntervalHours(String(payload?.interval_hours ?? 2))
      torPeerCheckerIntervalDirtyRef.current = false
      setTorPeerCheckerStatus('')
    } else {
      const message = (torPeerCheckerResult.reason as any)?.message || t('lightningOps.torPeerStatusUnavailable')
      setTorPeerCheckerStatus(message)
    }
    if (torPeerCheckerLogsResult.status === 'fulfilled') {
      const entries = (torPeerCheckerLogsResult.value as any)?.entries
      setTorPeerCheckerLogs(Array.isArray(entries) ? entries : [])
    } else if (torPeerCheckerResult.status === 'fulfilled') {
      setTorPeerCheckerLogs([])
    }
    if (bitcoinLocalResult.status === 'fulfilled') {
      setBitcoinLocal(bitcoinLocalResult.value as BitcoinLocalStatus)
    }
    if (autofeeConfigResult.status === 'fulfilled') {
      const cfg = autofeeConfigResult.value as AutofeeConfig
      setAutofeeConfig(cfg)
      setAutofeeEnabled(cfg.enabled)
      setAutofeeProfile(cfg.profile || 'moderate')
      setAutofeeLookback(String(cfg.lookback_days ?? 7))
      setAutofeeIntervalHours(String(Math.max(1, Math.round((cfg.run_interval_sec || 14400) / 3600))))
      setAutofeeCooldownUp(String(Math.max(1, Math.round((cfg.cooldown_up_sec || 10800) / 3600))))
      setAutofeeCooldownDown(String(Math.max(2, Math.round((cfg.cooldown_down_sec || 14400) / 3600))))
      setAutofeeRebalMode(cfg.rebal_cost_mode || 'blend')
      setAutofeeMinPpm(String(cfg.min_ppm ?? 10))
      setAutofeeMaxPpm(String(cfg.max_ppm ?? 2000))
      setAutofeeAmbossEnabled(Boolean(cfg.amboss_enabled))
      setAutofeeInboundPassive(Boolean(cfg.inbound_passive_enabled))
      setAutofeeDiscovery(Boolean(cfg.discovery_enabled))
      setAutofeeExplorer(Boolean(cfg.explorer_enabled))
      setAutofeeSuperSource(Boolean(cfg.super_source_enabled))
      setAutofeeSuperSourceBaseFee(String(cfg.super_source_base_fee_msat ?? 1000))
      setAutofeeRevfloor(cfg.revfloor_enabled !== false)
      setAutofeeCircuitBreaker(cfg.circuit_breaker_enabled !== false)
      setAutofeeExtremeDrain(cfg.extreme_drain_enabled !== false)
      setAutofeeHtlcSignalEnabled(cfg.htlc_signal_enabled !== false)
      {
        const mode = (cfg.htlc_mode || 'full').toLowerCase().trim()
        const normalizedMode = mode === 'observe_only' || mode === 'policy_only' || mode === 'full' ? mode : 'full'
        setAutofeeHtlcMode(normalizedMode)
      }
      setAutofeeMessage('')
    } else {
      const message = (autofeeConfigResult.reason as any)?.message || t('lightningOps.autofeeConfigUnavailable')
      setAutofeeMessage(message)
    }
    if (autofeeStatusResult.status === 'fulfilled') {
      setAutofeeStatus(autofeeStatusResult.value as AutofeeStatus)
    }
    if (autofeeChannelsResult.status === 'fulfilled') {
      const settingsPayload = (autofeeChannelsResult.value as any)?.settings as AutofeeChannelSetting[] | undefined
      const map: Record<number, boolean> = {}
      if (Array.isArray(settingsPayload)) {
        settingsPayload.forEach((entry) => {
          if (typeof entry?.channel_id === 'number') {
            map[entry.channel_id] = Boolean(entry.enabled)
          }
        })
      }
      setAutofeeSettings(map)
    }
    if (autofeeResultsResult.status === 'fulfilled') {
      const payload = autofeeResultsResult.value as any
      const lines = payload?.lines
      const items = payload?.items
      setAutofeeResults(Array.isArray(lines) ? lines : [])
      setAutofeeResultItems(Array.isArray(items) ? items : [])
      setAutofeeResultsStatus('')
    } else {
      const message = (autofeeResultsResult.reason as any)?.message || t('lightningOps.autofeeResultsUnavailable')
      setAutofeeResultsStatus(message)
    }
  }

  useEffect(() => {
    load()
  }, [])

  useEffect(() => {
    let mounted = true
    const refreshChannels = () => {
      getLnChannels()
        .then((res) => {
          if (!mounted) return
          applyChannelsPayload(res)
          setStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setStatus(err?.message || t('lightningOps.loadChannelsFailed'))
        })
    }
    const fetchAmboss = () => {
      getAmbossHealth()
        .then((data) => {
          if (!mounted) return
          setAmboss(data as AmbossHealthStatus)
          setAmbossStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setAmbossStatus(err?.message || t('lightningOps.ambossHealthStatusUnavailable'))
        })
    }
    const fetchChanHeal = () => {
      getLnChanHeal()
        .then((data) => {
          if (!mounted) return
          const payload = data as ChanHealStatus
          const prevAttemptAt = chanHealLastAttemptRef.current
          const nextAttemptAt = payload?.last_attempt_at || ''
          setChanHeal(payload)
          if (!chanHealIntervalDirtyRef.current && payload?.interval_sec) {
            setChanHealInterval(String(payload.interval_sec))
          }
          chanHealLastAttemptRef.current = nextAttemptAt
          if (nextAttemptAt && nextAttemptAt !== prevAttemptAt) {
            refreshChannels()
          }
          setChanHealStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setChanHealStatus(err?.message || t('lightningOps.chanHealStatusUnavailable'))
        })
    }
    const fetchHtlcManager = () => {
      getLnHtlcManager()
        .then((data) => {
          if (!mounted) return
          const payload = data as HtlcManagerStatus
          setHtlcManager(payload)
          const intervalMinutes = payload?.interval_minutes ?? ((payload?.interval_hours ?? 0) * 60)
          if (!htlcManagerFormDirtyRef.current && intervalMinutes > 0) {
            setHtlcManagerIntervalMinutes(String(intervalMinutes))
          }
          if (!htlcManagerFormDirtyRef.current && payload?.min_htlc_sat) {
            setHtlcManagerMinSat(String(payload.min_htlc_sat))
          }
          if (!htlcManagerFormDirtyRef.current) {
            setHtlcManagerMaxPct(String(payload?.max_local_pct ?? 0))
          }
          setHtlcManagerStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setHtlcManagerStatus(err?.message || t('lightningOps.htlcManagerStatusUnavailable'))
        })
      getLnHtlcManagerLogs()
        .then((data: any) => {
          if (!mounted) return
          const entries = data?.entries
          setHtlcManagerLogs(Array.isArray(entries) ? entries : [])
        })
        .catch(() => {
          if (!mounted) return
          setHtlcManagerLogs([])
        })
      getLnHtlcManagerFailed()
        .then((data: any) => {
          if (!mounted) return
          const entries = data?.entries
          setHtlcManagerFailed(Array.isArray(entries) ? entries : [])
        })
        .catch(() => {
          if (!mounted) return
          setHtlcManagerFailed([])
        })
    }
    const fetchTorPeerChecker = () => {
      getLnTorPeerChecker()
        .then((data) => {
          if (!mounted) return
          const payload = data as TorPeerCheckerStatus
          setTorPeerChecker(payload)
          if (!torPeerCheckerIntervalDirtyRef.current) {
            setTorPeerCheckerIntervalHours(String(payload?.interval_hours ?? 2))
          }
          setTorPeerCheckerStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setTorPeerCheckerStatus(err?.message || t('lightningOps.torPeerStatusUnavailable'))
        })
      getLnTorPeerCheckerLogs()
        .then((data: any) => {
          if (!mounted) return
          const entries = data?.entries
          setTorPeerCheckerLogs(Array.isArray(entries) ? entries : [])
        })
        .catch(() => {
          if (!mounted) return
          setTorPeerCheckerLogs([])
        })
    }
    const timer = window.setInterval(() => {
      fetchAmboss()
      fetchChanHeal()
      fetchHtlcManager()
      fetchTorPeerChecker()
    }, 30000)
    return () => {
      mounted = false
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
        setOpenFeeHint({ fastest, hour })
        setOpenFeeRate((prev) => (prev ? prev : fastest > 0 ? String(fastest) : prev))
        setCloseFeeHint({ fastest, hour })
        setCloseFeeRate((prev) => (prev ? prev : fastest > 0 ? String(fastest) : prev))
        setOpenFeeStatus('')
        setCloseFeeStatus('')
      })
      .catch(() => {
        if (!mounted) return
        setOpenFeeStatus(t('lightningOps.feeSuggestionsUnavailable'))
        setCloseFeeStatus(t('lightningOps.feeSuggestionsUnavailable'))
      })
    return () => {
      mounted = false
    }
  }, [])

  useEffect(() => {
    if (feeScopeAll) {
      setFeeLoadStatus('')
      setFeeLoading(false)
      return
    }
    if (!feeChannelPoint) {
      setFeeLoadStatus('')
      setFeeLoading(false)
      return
    }

    let mounted = true
    setFeeLoading(true)
    setFeeLoadStatus(t('lightningOps.loadingFees'))
    getLnChannelFees(feeChannelPoint)
      .then((res) => {
        if (!mounted) return
        setBaseFeeMsat(String(res?.base_fee_msat ?? ''))
        setFeeRatePpm(String(res?.fee_rate_ppm ?? ''))
        setTimeLockDelta(String(res?.time_lock_delta ?? ''))
        setInboundBaseMsat(String(res?.inbound_base_msat ?? ''))
        setInboundFeeRatePpm(String(res?.inbound_fee_rate_ppm ?? ''))
        const inboundBase = Number(res?.inbound_base_msat || 0)
        const inboundRate = Number(res?.inbound_fee_rate_ppm || 0)
        setInboundEnabled(inboundBase !== 0 || inboundRate !== 0)
        setFeeLoadStatus(t('lightningOps.feesLoaded'))
      })
      .catch((err: any) => {
        if (!mounted) return
        setFeeLoadStatus(err?.message || t('lightningOps.loadFeesFailed'))
      })
      .finally(() => {
        if (!mounted) return
        setFeeLoading(false)
      })

    return () => {
      mounted = false
    }
  }, [feeChannelPoint, feeScopeAll])

  const filteredChannels = useMemo(() => {
    let list = channels
    if (filter === 'active') {
      list = list.filter((ch) => ch.active)
    }
    if (filter === 'inactive') {
      list = list.filter((ch) => !ch.active)
    }
    if (!showPrivate) {
      list = list.filter((ch) => !ch.private)
    }
    if (search.trim()) {
      const query = search.trim().toLowerCase()
      list = list.filter((ch) => {
        return (
          ch.peer_alias?.toLowerCase().includes(query) ||
          ch.remote_pubkey?.toLowerCase().includes(query) ||
          ch.channel_point?.toLowerCase().includes(query)
        )
      })
    }
    const minCap = Number(minCapacity || 0)
    if (minCap > 0) {
      list = list.filter((ch) => ch.capacity_sat >= minCap)
    }
    const sorted = [...list]
    const direction = sortDir === 'desc' ? -1 : 1
    sorted.sort((a, b) => {
      if (sortBy === 'alias') {
        const aVal = (a.peer_alias || a.remote_pubkey || '').toLowerCase()
        const bVal = (b.peer_alias || b.remote_pubkey || '').toLowerCase()
        return aVal.localeCompare(bVal) * direction
      }
      const aVal = sortBy === 'capacity'
        ? a.capacity_sat
        : sortBy === 'local'
          ? a.local_balance_sat
          : a.remote_balance_sat
      const bVal = sortBy === 'capacity'
        ? b.capacity_sat
        : sortBy === 'local'
          ? b.local_balance_sat
          : b.remote_balance_sat
      return (aVal - bVal) * direction
    })
    return sorted
  }, [channels, filter, minCapacity, search, showPrivate, sortBy, sortDir])

  const pendingOpen = useMemo(() => pendingChannels.filter((ch) => ch.status === 'opening'), [pendingChannels])
  const pendingClose = useMemo(() => pendingChannels.filter((ch) => ch.status !== 'opening'), [pendingChannels])

  const pendingStatusLabel = (status: string) => {
    switch (status) {
      case 'opening':
        return t('lightningOps.statusOpening')
      case 'closing':
        return t('lightningOps.statusClosing')
      case 'force_closing':
        return t('lightningOps.statusForceClosing')
      case 'waiting_close':
        return t('lightningOps.statusWaitingClose')
      default:
        return status
    }
  }

  const handleConnectPeer = async () => {
    setPeerStatus(t('lightningOps.connectingPeer'))
    try {
      await connectPeer({ address: peerAddress, perm: !peerTemporary })
      setPeerStatus(t('lightningOps.peerConnected'))
      setPeerAddress('')
      setPeerTemporary(false)
      load()
    } catch (err: any) {
      setPeerStatus(err?.message || t('lightningOps.peerConnectFailed'))
    }
  }

  const handleDisconnect = async (pubkey: string) => {
    const confirmed = window.confirm(t('lightningOps.disconnectConfirm'))
    if (!confirmed) return
    setPeerActionStatus(t('lightningOps.disconnectingPeer'))
    try {
      await disconnectPeer({ pubkey })
      setPeerActionStatus(t('lightningOps.peerDisconnected'))
      load()
    } catch (err: any) {
      setPeerActionStatus(err?.message || t('lightningOps.disconnectFailed'))
    }
  }

  const handleBoostPeers = async () => {
    setBoostRunning(true)
    setBoostStatus(t('lightningOps.boostingPeers'))
    try {
      const res = await boostPeers({ limit: 25 })
      const connected = res?.connected ?? 0
      const skipped = res?.skipped ?? 0
      const failed = res?.failed ?? 0
      setBoostStatus(t('lightningOps.boostComplete', { connected, skipped, failed }))
      load()
    } catch (err: any) {
      setBoostStatus(err?.message || t('lightningOps.boostFailed'))
    } finally {
      setBoostRunning(false)
    }
  }

  const handleToggleAmboss = async () => {
    if (ambossBusy) return
    const nextEnabled = !amboss?.enabled
    setAmbossBusy(true)
    setAmbossStatus(t('lightningOps.ambossHealthSaving'))
    try {
      const res = await updateAmbossHealth({ enabled: nextEnabled })
      setAmboss(res as AmbossHealthStatus)
      setAmbossStatus(nextEnabled ? t('lightningOps.ambossHealthEnabled') : t('lightningOps.ambossHealthDisabled'))
    } catch (err: any) {
      setAmbossStatus(err?.message || t('lightningOps.ambossHealthSaveFailed'))
    } finally {
      setAmbossBusy(false)
    }
  }

  const handleAutofeeSave = async () => {
    if (autofeeBusy) return
    setAutofeeBusy(true)
    setAutofeeMessage(t('lightningOps.autofeeSaving'))
    try {
      const lookbackDays = Math.max(5, Math.min(21, Number(autofeeLookback || 7)))
      const intervalSec = Math.max(1, Number(autofeeIntervalHours || 4)) * 3600
      const cooldownUpSec = Math.max(1, Number(autofeeCooldownUp || 3)) * 3600
      const cooldownDownSec = Math.max(2, Number(autofeeCooldownDown || 4)) * 3600
      const minPpmRaw = Math.max(0, Number(autofeeMinPpm || 0))
      let maxPpmRaw = Math.max(0, Number(autofeeMaxPpm || 2000))
      if (maxPpmRaw < minPpmRaw) {
        maxPpmRaw = minPpmRaw
      }
      const superSourceBaseFee = Math.max(0, Number(autofeeSuperSourceBaseFee || 1000))
      const payload: any = {
        enabled: autofeeEnabled,
        profile: autofeeProfile,
        lookback_days: lookbackDays,
        run_interval_sec: intervalSec,
        cooldown_up_sec: cooldownUpSec,
        cooldown_down_sec: cooldownDownSec,
        rebal_cost_mode: autofeeRebalMode,
        min_ppm: minPpmRaw,
        max_ppm: maxPpmRaw,
        amboss_enabled: autofeeAmbossEnabled,
        inbound_passive_enabled: autofeeInboundPassive,
        discovery_enabled: autofeeDiscovery,
        explorer_enabled: autofeeExplorer,
        super_source_enabled: autofeeSuperSource,
        super_source_base_fee_msat: superSourceBaseFee,
        revfloor_enabled: autofeeRevfloor,
        circuit_breaker_enabled: autofeeCircuitBreaker,
        extreme_drain_enabled: autofeeExtremeDrain,
        htlc_signal_enabled: autofeeHtlcSignalEnabled,
        htlc_mode: autofeeHtlcMode
      }
      if (autofeeAmbossToken.trim()) {
        payload.amboss_token = autofeeAmbossToken.trim()
      }
      const res = await updateAutofeeConfig(payload)
      setAutofeeConfig(res as AutofeeConfig)
      setAutofeeMessage(t('lightningOps.autofeeSaved'))
      setAutofeeAmbossToken('')
      const status = await getAutofeeStatus()
      setAutofeeStatus(status as AutofeeStatus)
    } catch (err: any) {
      setAutofeeMessage(err?.message || t('lightningOps.autofeeSaveFailed'))
    } finally {
      setAutofeeBusy(false)
    }
  }

  const handleAutofeeRun = async (dryRun: boolean) => {
    if (autofeeBusy) return
    setAutofeeBusy(true)
    setAutofeeMessage(dryRun ? t('lightningOps.autofeeDryRunning') : t('lightningOps.autofeeRunning'))
    try {
      await runAutofee({ dry_run: dryRun })
      setAutofeeMessage(dryRun ? t('lightningOps.autofeeDryRunDone') : t('lightningOps.autofeeRunDone'))
      const status = await getAutofeeStatus()
      setAutofeeStatus(status as AutofeeStatus)
      const results = await getAutofeeResults(buildAutofeeResultsQuery())
      const payload = results as any
      setAutofeeResults(Array.isArray(payload?.lines) ? payload.lines : [])
      setAutofeeResultItems(Array.isArray(payload?.items) ? payload.items : [])
      setAutofeeResultsStatus('')
    } catch (err: any) {
      setAutofeeMessage(err?.message || t('lightningOps.autofeeRunFailed'))
    } finally {
      setAutofeeBusy(false)
    }
  }

  const handleAutofeeResultsRefresh = async () => {
    setAutofeeResultsStatus(t('lightningOps.autofeeResultsLoading'))
    try {
      const results = await getAutofeeResults(buildAutofeeResultsQuery())
      const payload = results as any
      setAutofeeResults(Array.isArray(payload?.lines) ? payload.lines : [])
      setAutofeeResultItems(Array.isArray(payload?.items) ? payload.items : [])
      setAutofeeResultsStatus('')
    } catch (err: any) {
      setAutofeeResultsStatus(err?.message || t('lightningOps.autofeeResultsUnavailable'))
    }
  }

  const handleAutofeeChannelToggle = async (channel: Channel, enabled: boolean) => {
    try {
      await updateAutofeeChannels({ channel_id: channel.channel_id, channel_point: channel.channel_point, enabled })
      setAutofeeSettings((prev) => ({ ...prev, [channel.channel_id]: enabled }))
    } catch (err: any) {
      setAutofeeMessage(err?.message || t('lightningOps.autofeeChannelUpdateFailed'))
    }
  }

  const handleAutofeeBulk = async (enabled: boolean) => {
    if (autofeeBusy) return
    setAutofeeBusy(true)
    setAutofeeMessage(enabled ? t('lightningOps.autofeeIncludingAll') : t('lightningOps.autofeeExcludingAll'))
    try {
      await updateAutofeeChannels({ apply_all: true, enabled })
      const map: Record<number, boolean> = {}
      channels.forEach((ch) => {
        map[ch.channel_id] = enabled
      })
      setAutofeeSettings(map)
      setAutofeeMessage(t('lightningOps.autofeeBulkDone'))
    } catch (err: any) {
      setAutofeeMessage(err?.message || t('lightningOps.autofeeBulkFailed'))
    } finally {
      setAutofeeBusy(false)
    }
  }

  const handleToggleChanHeal = async () => {
    if (chanHealBusy) return
    const nextEnabled = !chanHeal?.enabled
    setChanHealBusy(true)
    setChanHealStatus(t('lightningOps.chanHealSaving'))
    const interval = Number(chanHealInterval || 0)
    const payload: { enabled?: boolean; interval_sec?: number } = { enabled: nextEnabled }
    if (interval > 0) {
      payload.interval_sec = interval
    }
    try {
      const res = await updateLnChanHeal(payload)
      setChanHeal(res as ChanHealStatus)
      setChanHealStatus(nextEnabled ? t('lightningOps.chanHealEnabled') : t('lightningOps.chanHealDisabled'))
    } catch (err: any) {
      setChanHealStatus(err?.message || t('lightningOps.chanHealSaveFailed'))
    } finally {
      setChanHealBusy(false)
    }
  }

  const handleSaveChanHealInterval = async () => {
    if (chanHealBusy) return
    const interval = Number(chanHealInterval || 0)
    if (!interval || interval <= 0) {
      setChanHealStatus(t('lightningOps.chanHealIntervalInvalid'))
      return
    }
    setChanHealBusy(true)
    setChanHealStatus(t('lightningOps.chanHealSaving'))
    try {
      const res = await updateLnChanHeal({ interval_sec: interval })
      setChanHeal(res as ChanHealStatus)
      chanHealIntervalDirtyRef.current = false
      setChanHealStatus(t('lightningOps.chanHealSaved'))
    } catch (err: any) {
      setChanHealStatus(err?.message || t('lightningOps.chanHealSaveFailed'))
    } finally {
      setChanHealBusy(false)
    }
  }

  const handleToggleHtlcManager = async () => {
    if (htlcManagerBusy) return
    const nextEnabled = !htlcManager?.enabled
    setHtlcManagerBusy(true)
    setHtlcManagerStatus(t('lightningOps.htlcManagerSaving'))
    try {
      const res = await updateLnHtlcManager({ enabled: nextEnabled })
      setHtlcManager(res as HtlcManagerStatus)
      setHtlcManagerStatus(nextEnabled ? t('lightningOps.htlcManagerEnabled') : t('lightningOps.htlcManagerDisabled'))
    } catch (err: any) {
      setHtlcManagerStatus(err?.message || t('lightningOps.htlcManagerSaveFailed'))
    } finally {
      setHtlcManagerBusy(false)
    }
  }

  const handleSaveHtlcManager = async () => {
    if (htlcManagerBusy) return
    const intervalMinutes = Number(htlcManagerIntervalMinutes || 0)
    const minSat = Number(htlcManagerMinSat || 0)
    const maxPct = Number(htlcManagerMaxPct || 0)
    if (!intervalMinutes || intervalMinutes < 1 || intervalMinutes > 2880) {
      setHtlcManagerStatus(t('lightningOps.htlcManagerIntervalInvalid'))
      return
    }
    if (!minSat || minSat < 1) {
      setHtlcManagerStatus(t('lightningOps.htlcManagerMinInvalid'))
      return
    }
    if (maxPct < 0) {
      setHtlcManagerStatus(t('lightningOps.htlcManagerMaxPctInvalid'))
      return
    }
    setHtlcManagerBusy(true)
    setHtlcManagerStatus(t('lightningOps.htlcManagerSaving'))
    try {
      const res = await updateLnHtlcManager({
        interval_minutes: intervalMinutes,
        min_htlc_sat: minSat,
        max_local_pct: maxPct
      })
      setHtlcManager(res as HtlcManagerStatus)
      htlcManagerFormDirtyRef.current = false
      setHtlcManagerStatus(t('lightningOps.htlcManagerSaved'))
    } catch (err: any) {
      setHtlcManagerStatus(err?.message || t('lightningOps.htlcManagerSaveFailed'))
    } finally {
      setHtlcManagerBusy(false)
    }
  }

  const handleRunHtlcManagerNow = async () => {
    if (htlcManagerBusy) return
    setHtlcManagerBusy(true)
    setHtlcManagerStatus(t('lightningOps.htlcManagerRunning'))
    try {
      const res = await updateLnHtlcManager({ run_now: true })
      setHtlcManager(res as HtlcManagerStatus)
      const [logsRes, failedRes] = await Promise.all([
        getLnHtlcManagerLogs(),
        getLnHtlcManagerFailed()
      ])
      const logEntries = (logsRes as any)?.entries
      const failedEntries = (failedRes as any)?.entries
      setHtlcManagerLogs(Array.isArray(logEntries) ? logEntries : [])
      setHtlcManagerFailed(Array.isArray(failedEntries) ? failedEntries : [])
      setHtlcManagerStatus(t('lightningOps.htlcManagerRunDone'))
    } catch (err: any) {
      setHtlcManagerStatus(err?.message || t('lightningOps.htlcManagerRunFailed'))
    } finally {
      setHtlcManagerBusy(false)
    }
  }

  const handleToggleTorPeerChecker = async () => {
    if (torPeerCheckerBusy) return
    const nextEnabled = !torPeerChecker?.enabled
    setTorPeerCheckerBusy(true)
    setTorPeerCheckerStatus(t('lightningOps.torPeerSaving'))
    try {
      const res = await updateLnTorPeerChecker({ enabled: nextEnabled })
      setTorPeerChecker(res as TorPeerCheckerStatus)
      setTorPeerCheckerStatus(nextEnabled ? t('lightningOps.torPeerEnabled') : t('lightningOps.torPeerDisabled'))
    } catch (err: any) {
      setTorPeerCheckerStatus(err?.message || t('lightningOps.torPeerSaveFailed'))
    } finally {
      setTorPeerCheckerBusy(false)
    }
  }

  const handleSaveTorPeerChecker = async () => {
    if (torPeerCheckerBusy) return
    const intervalHours = Number(torPeerCheckerIntervalHours || 0)
    if (!intervalHours || intervalHours < 2 || intervalHours > 168) {
      setTorPeerCheckerStatus(t('lightningOps.torPeerIntervalInvalid'))
      return
    }
    setTorPeerCheckerBusy(true)
    setTorPeerCheckerStatus(t('lightningOps.torPeerSaving'))
    try {
      const res = await updateLnTorPeerChecker({ interval_hours: intervalHours })
      setTorPeerChecker(res as TorPeerCheckerStatus)
      torPeerCheckerIntervalDirtyRef.current = false
      setTorPeerCheckerStatus(t('lightningOps.torPeerSaved'))
    } catch (err: any) {
      setTorPeerCheckerStatus(err?.message || t('lightningOps.torPeerSaveFailed'))
    } finally {
      setTorPeerCheckerBusy(false)
    }
  }

  const handleRunTorPeerCheckerNow = async () => {
    if (torPeerCheckerBusy) return
    setTorPeerCheckerBusy(true)
    setTorPeerCheckerStatus(t('lightningOps.torPeerRunning'))
    try {
      const res = await updateLnTorPeerChecker({ run_now: true })
      setTorPeerChecker(res as TorPeerCheckerStatus)
      const logsRes = await getLnTorPeerCheckerLogs()
      const entries = (logsRes as any)?.entries
      setTorPeerCheckerLogs(Array.isArray(entries) ? entries : [])
      setTorPeerCheckerStatus(t('lightningOps.torPeerRunDone'))
    } catch (err: any) {
      setTorPeerCheckerStatus(err?.message || t('lightningOps.torPeerRunFailed'))
    } finally {
      setTorPeerCheckerBusy(false)
    }
  }

  const handleSignMessage = async () => {
    if (signBusy) return
    const message = signMessage.trim()
    if (!message) {
      setSignStatus(t('lightningOps.signMessageRequired'))
      return
    }
    setSignBusy(true)
    setSignStatus(t('lightningOps.signMessageSigning'))
    setSignSignature('')
    setSignCopied(false)
    try {
      const res = await signLnMessage({ message })
      const signature = String(res?.signature || '').trim()
      if (!signature) {
        setSignStatus(t('lightningOps.signMessageFailed'))
        return
      }
      setSignSignature(signature)
      setSignStatus(t('lightningOps.signMessageReady'))
    } catch (err: any) {
      setSignStatus(err?.message || t('lightningOps.signMessageFailed'))
    } finally {
      setSignBusy(false)
    }
  }

  const handleCopySignature = async () => {
    if (!signSignature) return
    try {
      await navigator.clipboard.writeText(signSignature)
      setSignCopied(true)
    } catch {
      setSignStatus(t('common.copyFailedManual'))
    }
  }

  const handleToggleChanStatus = async (channel: Channel) => {
    if (!channel.channel_point || chanStatusBusy) return
    if (!channel.active) return
    const enable = channel.local_disabled ?? isLocalChanDisabled(channel.chan_status_flags)
    setChanStatusBusy(channel.channel_point)
    setChanStatusMessage(enable ? t('lightningOps.channelEnabling') : t('lightningOps.channelDisabling'))
    try {
      await updateLnChannelStatus({ channel_point: channel.channel_point, enabled: enable })
      setChanStatusMessage(enable ? t('lightningOps.channelEnabled') : t('lightningOps.channelDisabled'))
      load()
    } catch (err: any) {
      setChanStatusMessage(err?.message || t('lightningOps.channelStatusFailed'))
    } finally {
      setChanStatusBusy(null)
    }
  }

  const handleOpenChannel = async () => {
    setOpenStatus(t('lightningOps.openingChannel'))
    setOpenChannelPoint('')
    const localFunding = Number(openAmount || 0)
    const feeRate = Number(openFeeRate || 0)
    if (!openPeer.trim()) {
      setOpenStatus(t('lightningOps.peerAddressRequired'))
      return
    }
    if (localFunding < 20000) {
      setOpenStatus(t('lightningOps.minimumChannelSize'))
      return
    }
    try {
      const res = await openChannel({
        peer_address: openPeer.trim(),
        local_funding_sat: localFunding,
        close_address: openCloseAddress.trim() || undefined,
        sat_per_vbyte: feeRate > 0 ? feeRate : undefined,
        private: openPrivate
      })
      setOpenStatus(t('lightningOps.channelOpeningSubmitted'))
      setOpenChannelPoint(res?.channel_point || '')
      setOpenAmount('')
      setOpenCloseAddress('')
      load()
    } catch (err: any) {
      setOpenStatus(err?.message || t('lightningOps.channelOpenFailed'))
    }
  }

  const mempoolLink = (channelPoint: string) => {
    const parts = channelPoint.split(':')
    if (parts.length !== 2) return ''
    return `https://mempool.space/pt/tx/${parts[0]}#vout=${parts[1]}`
  }

  const mempoolTxLink = (txid?: string) => {
    if (!txid) return ''
    return `https://mempool.space/tx/${txid}`
  }

  const handleCloseChannel = async () => {
    setCloseStatus(t('lightningOps.closingChannel'))
    if (!closePoint) {
      setCloseStatus(t('lightningOps.selectChannelToClose'))
      return
    }
    try {
      const feeRate = Number(closeFeeRate || 0)
      await closeChannel({ channel_point: closePoint, force: closeForce, sat_per_vbyte: feeRate > 0 ? feeRate : undefined })
      setCloseStatus(t('lightningOps.closeInitiated'))
      load()
    } catch (err: any) {
      setCloseStatus(err?.message || t('lightningOps.closeFailed'))
    }
  }

  const runFeeUpdate = async (payload: {
    applyAll: boolean
    channelPoint?: string
    baseFeeMsat: number
    feeRatePpm: number
    timeLockDelta: number
    inboundEnabled: boolean
    inboundBaseMsat: number
    inboundFeeRatePpm: number
    setStatus: (value: string) => void
  }) => {
    payload.setStatus(t('lightningOps.updatingFees'))
    if (!payload.applyAll && !payload.channelPoint) {
      payload.setStatus(t('lightningOps.selectChannelOrAll'))
      return false
    }
    const hasOutbound = payload.baseFeeMsat !== 0 || payload.feeRatePpm !== 0 || payload.timeLockDelta !== 0
    const hasInbound = payload.inboundEnabled && (payload.inboundBaseMsat !== 0 || payload.inboundFeeRatePpm !== 0)
    if (!hasOutbound && !hasInbound) {
      payload.setStatus(t('lightningOps.setAtLeastOneFee'))
      return false
    }
    try {
      const res = await updateChannelFees({
        apply_all: payload.applyAll,
        channel_point: payload.applyAll ? undefined : payload.channelPoint,
        base_fee_msat: payload.baseFeeMsat,
        fee_rate_ppm: payload.feeRatePpm,
        time_lock_delta: payload.timeLockDelta,
        inbound_enabled: payload.inboundEnabled,
        inbound_base_msat: payload.inboundBaseMsat,
        inbound_fee_rate_ppm: payload.inboundFeeRatePpm
      })
      payload.setStatus(res?.warning || t('lightningOps.feesUpdated'))
      load()
      return true
    } catch (err: any) {
      payload.setStatus(err?.message || t('lightningOps.feeUpdateFailed'))
      return false
    }
  }

  const startInlineFeeEdit = async (ch: Channel) => {
    setInlineFeeChannelPoint(ch.channel_point)
    setInlineFeeRatePpm(String(ch.fee_rate_ppm ?? 0))
    setInlineBaseFeeMsat(String(ch.base_fee_msat ?? 0))
    setInlineInboundFeeRatePpm(String(ch.inbound_fee_rate_ppm ?? 0))
    setInlineInboundBaseMsat('0')
    setInlineTimeLockDelta('0')
    setInlineFeeStatus('')
    setInlineFeeLoading(true)
    try {
      const res = await getLnChannelFees(ch.channel_point)
      setInlineFeeRatePpm(String(res?.fee_rate_ppm ?? ch.fee_rate_ppm ?? 0))
      setInlineBaseFeeMsat(String(res?.base_fee_msat ?? ch.base_fee_msat ?? 0))
      setInlineInboundFeeRatePpm(String(res?.inbound_fee_rate_ppm ?? ch.inbound_fee_rate_ppm ?? 0))
      setInlineInboundBaseMsat(String(res?.inbound_base_msat ?? 0))
      setInlineTimeLockDelta(String(res?.time_lock_delta ?? 0))
    } catch (err: any) {
      setInlineFeeStatus(err?.message || t('lightningOps.loadFeesFailed'))
    } finally {
      setInlineFeeLoading(false)
    }
  }

  const cancelInlineFeeEdit = () => {
    setInlineFeeChannelPoint('')
    setInlineFeeStatus('')
    setInlineFeeLoading(false)
    setInlineFeeSaving(false)
  }

  const handleInlineFeeSave = async () => {
    if (!inlineFeeChannelPoint || inlineFeeLoading || inlineFeeSaving) return
    const outRate = Number(inlineFeeRatePpm)
    const outBase = Number(inlineBaseFeeMsat)
    const inRate = Number(inlineInboundFeeRatePpm)
    const inBase = Number(inlineInboundBaseMsat || 0)
    const delta = Number(inlineTimeLockDelta || 0)

    if (!Number.isFinite(outRate) || !Number.isFinite(outBase) || !Number.isFinite(inRate) || !Number.isFinite(inBase) || !Number.isFinite(delta)) {
      setInlineFeeStatus(t('lightningOps.setAtLeastOneFee'))
      return
    }
    if (outRate < 0 || outBase < 0 || delta < 0 || inBase < 0) {
      setInlineFeeStatus(t('lightningOps.setAtLeastOneFee'))
      return
    }
    if (inRate > 0) {
      setInlineFeeStatus(t('lightningOps.inboundMustBeNegative'))
      return
    }

    setInlineFeeSaving(true)
    const ok = await runFeeUpdate({
      applyAll: false,
      channelPoint: inlineFeeChannelPoint,
      baseFeeMsat: outBase,
      feeRatePpm: outRate,
      timeLockDelta: delta,
      inboundEnabled: true,
      inboundBaseMsat: inBase,
      inboundFeeRatePpm: inRate,
      setStatus: setInlineFeeStatus
    })
    setInlineFeeSaving(false)
    if (ok) {
      setInlineFeeChannelPoint('')
    }
  }

  const handleUpdateFees = async () => {
    const base = Number(baseFeeMsat || 0)
    const ppm = Number(feeRatePpm || 0)
    const delta = Number(timeLockDelta || 0)
    const inboundBase = Number(inboundBaseMsat || 0)
    const inboundRate = Number(inboundFeeRatePpm || 0)
    await runFeeUpdate({
      applyAll: feeScopeAll,
      channelPoint: feeScopeAll ? undefined : feeChannelPoint,
      baseFeeMsat: base,
      feeRatePpm: ppm,
      timeLockDelta: delta,
      inboundEnabled: inboundEnabled,
      inboundBaseMsat: inboundBase,
      inboundFeeRatePpm: inboundRate,
      setStatus: setFeeStatus
    })
  }

  const channelOptions = useMemo(() => {
    return channels.map((ch) => ({
      value: ch.channel_point,
      label: `${ch.peer_alias || ch.remote_pubkey.slice(0, 12)} - ${ch.channel_point}`
    }))
  }, [channels])

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4">
          <div>
            <h2 className="text-2xl font-semibold">{t('lightningOps.title')}</h2>
            <p className="text-fog/60">{t('lightningOps.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <div className="rounded-full border border-white/10 bg-ink/60 px-3 py-1.5 text-[11px] text-fog/70 sm:text-xs sm:px-4 sm:py-2">
              {t('lightningOps.active')}: <span className="text-fog">{activeCount}</span>
            </div>
            <div className="rounded-full border border-white/10 bg-ink/60 px-3 py-1.5 text-[11px] text-fog/70 sm:text-xs sm:px-4 sm:py-2">
              {t('lightningOps.inactive')}: <span className="text-fog">{inactiveCount}</span>
            </div>
            <div className="rounded-full border border-glow/30 bg-glow/10 px-3 py-1.5 text-[11px] text-glow sm:text-xs sm:px-4 sm:py-2">
              {t('lightningOps.opening')}: <span className="text-fog">{pendingOpenCount}</span>
            </div>
            <div className="rounded-full border border-ember/30 bg-ember/10 px-3 py-1.5 text-[11px] text-ember sm:text-xs sm:px-4 sm:py-2">
              {t('lightningOps.closing')}: <span className="text-fog">{pendingCloseCount}</span>
            </div>
            <button className="btn-secondary text-[11px] px-3 py-2 sm:text-xs" onClick={load}>
              {t('common.refresh')}
            </button>
          </div>
        </div>
        {status && <p className="mt-4 text-sm text-brass">{status}</p>}
        {chanStatusMessage && <p className="mt-2 text-sm text-brass">{chanStatusMessage}</p>}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.autofeeTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.autofeeSubtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className={`rounded-full px-3 py-1 text-xs ${autofeeEnabled ? 'bg-glow/20 text-glow' : 'bg-ember/20 text-ember'}`}>
              {autofeeEnabled ? t('common.enabled') : t('common.disabled')}
            </span>
            {autofeeStatus?.running && (
              <span className="rounded-full px-3 py-1 text-xs bg-brass/20 text-brass">{t('lightningOps.autofeeRunning')}</span>
            )}
            <button
              className="btn-secondary text-xs px-3 py-2"
              onClick={() => setAutofeeOpen((open) => !open)}
            >
              {autofeeOpen ? t('common.hide') : t('lightningOps.autofeeConfigure')}
            </button>
          </div>
        </div>

        {autofeeOpen && (
          <>
            <div className="grid gap-4 lg:grid-cols-3">
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeEnabled} onChange={(e) => setAutofeeEnabled(e.target.checked)} />
                {t('lightningOps.autofeeEnabled')}
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeProfile')}
                <select
                  className="input-field mt-2"
                  value={autofeeProfile}
                  onChange={(e) => {
                    const value = e.target.value
                    setAutofeeProfile(value)
                    const defaults = autofeeProfileDefaults[value]
                    if (defaults) {
                      setAutofeeIntervalHours(String(defaults.interval))
                      setAutofeeCooldownUp(String(defaults.cooldownUp))
                      setAutofeeCooldownDown(String(defaults.cooldownDown))
                    }
                  }}
                >
                  <option value="conservative">{t('lightningOps.autofeeProfileConservative')}</option>
                  <option value="moderate">{t('lightningOps.autofeeProfileModerate')}</option>
                  <option value="aggressive">{t('lightningOps.autofeeProfileAggressive')}</option>
                </select>
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeLookback')}
                <input className="input-field mt-2" type="number" min={5} max={21} value={autofeeLookback} onChange={(e) => setAutofeeLookback(e.target.value)} />
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeInterval')}
                <input className="input-field mt-2" type="number" min={1} max={24} value={autofeeIntervalHours} onChange={(e) => setAutofeeIntervalHours(e.target.value)} />
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeCooldownUp')}
                <input className="input-field mt-2" type="number" min={1} max={12} value={autofeeCooldownUp} onChange={(e) => setAutofeeCooldownUp(e.target.value)} />
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeCooldownDown')}
                <input className="input-field mt-2" type="number" min={2} max={24} value={autofeeCooldownDown} onChange={(e) => setAutofeeCooldownDown(e.target.value)} />
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeRebalCostMode')}
                <select className="input-field mt-2" value={autofeeRebalMode} onChange={(e) => setAutofeeRebalMode(e.target.value)}>
                  <option value="blend">{t('lightningOps.autofeeRebalCostModeBlend')}</option>
                  <option value="channel">{t('lightningOps.autofeeRebalCostModeChannel')}</option>
                  <option value="global">{t('lightningOps.autofeeRebalCostModeGlobal')}</option>
                </select>
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeMinPpm')}
                <input className="input-field mt-2" type="number" min={0} value={autofeeMinPpm} onChange={(e) => setAutofeeMinPpm(e.target.value)} />
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeMaxPpm')}
                <input className="input-field mt-2" type="number" min={1} value={autofeeMaxPpm} onChange={(e) => setAutofeeMaxPpm(e.target.value)} />
              </label>
            </div>

            <div className="grid gap-4 lg:grid-cols-3">
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeInboundPassive} onChange={(e) => setAutofeeInboundPassive(e.target.checked)} />
                {t('lightningOps.autofeeInboundPassive')}
              </label>
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeDiscovery} onChange={(e) => setAutofeeDiscovery(e.target.checked)} />
                {t('lightningOps.autofeeDiscovery')}
              </label>
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeExplorer} onChange={(e) => setAutofeeExplorer(e.target.checked)} />
                {t('lightningOps.autofeeExplorer')}
              </label>
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeRevfloor} onChange={(e) => setAutofeeRevfloor(e.target.checked)} />
                {t('lightningOps.autofeeRevfloor')}
              </label>
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeCircuitBreaker} onChange={(e) => setAutofeeCircuitBreaker(e.target.checked)} />
                {t('lightningOps.autofeeCircuitBreaker')}
              </label>
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeExtremeDrain} onChange={(e) => setAutofeeExtremeDrain(e.target.checked)} />
                {t('lightningOps.autofeeExtremeDrain')}
              </label>
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeHtlcSignalEnabled} onChange={(e) => setAutofeeHtlcSignalEnabled(e.target.checked)} />
                {t('lightningOps.autofeeHtlcSignalEnabled')}
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeHtlcMode')}
                <select className="input-field mt-2" value={autofeeHtlcMode} onChange={(e) => setAutofeeHtlcMode(e.target.value)} disabled={!autofeeHtlcSignalEnabled}>
                  <option value="observe_only">{t('lightningOps.autofeeHtlcModeObserveOnly')}</option>
                  <option value="policy_only">{t('lightningOps.autofeeHtlcModePolicyOnly')}</option>
                  <option value="full">{t('lightningOps.autofeeHtlcModeFull')}</option>
                </select>
              </label>
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeSuperSource} onChange={(e) => setAutofeeSuperSource(e.target.checked)} />
                {t('lightningOps.autofeeSuperSource')}
              </label>
              {autofeeSuperSource && (
                <label className="text-sm text-fog/70">
                  {t('lightningOps.autofeeSuperSourceBaseFee')}
                  <input className="input-field mt-2" type="number" min={0} value={autofeeSuperSourceBaseFee} onChange={(e) => setAutofeeSuperSourceBaseFee(e.target.value)} />
                </label>
              )}
              <label className="flex items-center gap-2 text-sm text-fog/70">
                <input type="checkbox" checked={autofeeAmbossEnabled} onChange={(e) => setAutofeeAmbossEnabled(e.target.checked)} />
                {t('lightningOps.autofeeAmboss')}
              </label>
              {autofeeAmbossEnabled && (
                <label className="text-sm text-fog/70 lg:col-span-2">
                  {t('lightningOps.autofeeAmbossToken')}
                  <input className="input-field mt-2" type="password" value={autofeeAmbossToken} onChange={(e) => setAutofeeAmbossToken(e.target.value)} placeholder={autofeeConfig?.amboss_token_set ? t('lightningOps.autofeeAmbossTokenSet') : ''} />
                </label>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-3">
              <button className="btn-primary" onClick={handleAutofeeSave} disabled={autofeeBusy}>{t('common.save')}</button>
              <button className="btn-secondary" onClick={() => handleAutofeeRun(true)} disabled={autofeeBusy}>{t('lightningOps.autofeeDryRun')}</button>
              <button className="btn-secondary" onClick={() => handleAutofeeRun(false)} disabled={autofeeBusy}>{t('lightningOps.autofeeRunNow')}</button>
            </div>

            {autofeeMessage && <p className="text-sm text-brass">{autofeeMessage}</p>}
            <div className="text-xs text-fog/60">
              {autofeeStatus?.last_run_at && (
                <div>{t('lightningOps.autofeeLastRun')}: <span className="text-fog">{new Date(autofeeStatus.last_run_at).toLocaleString()}</span></div>
              )}
              {autofeeEnabled && autofeeStatus?.next_run_at && (
                <div>{t('lightningOps.autofeeNextRun')}: <span className="text-fog">{new Date(autofeeStatus.next_run_at).toLocaleString()}</span></div>
              )}
              {autofeeStatus?.last_error && (
                <div>{t('lightningOps.autofeeLastError')}: <span className="text-ember">{autofeeStatus.last_error}</span></div>
              )}
            </div>
          </>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.autofeeResultsTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.autofeeResultsSubtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <button
              className="btn-secondary text-xs px-3 py-2"
              onClick={() => setAutofeeResultsOpen((open) => !open)}
            >
              {autofeeResultsOpen ? t('common.hide') : t('lightningOps.autofeeResultsShow')}
            </button>
            <button className="btn-secondary text-xs px-3 py-2" onClick={handleAutofeeResultsRefresh}>
              {t('lightningOps.autofeeResultsRefresh')}
            </button>
          </div>
        </div>

        {autofeeResultsOpen && (
          <>
            <div className="grid gap-3 lg:grid-cols-3">
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeResultsRuns')}
                <input
                  className="input-field mt-2"
                  type="number"
                  min={1}
                  max={50}
                  value={autofeeResultsRuns}
                  onChange={(e) => setAutofeeResultsRuns(e.target.value)}
                />
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeResultsFrom')}
                <input
                  className="input-field mt-2"
                  type="datetime-local"
                  value={autofeeResultsFrom}
                  onChange={(e) => setAutofeeResultsFrom(e.target.value)}
                />
              </label>
              <label className="text-sm text-fog/70">
                {t('lightningOps.autofeeResultsTo')}
                <input
                  className="input-field mt-2"
                  type="datetime-local"
                  value={autofeeResultsTo}
                  onChange={(e) => setAutofeeResultsTo(e.target.value)}
                />
              </label>
            </div>
            {autofeeResultsStatus && <p className="text-sm text-brass">{autofeeResultsStatus}</p>}
            <div className="bg-ink/70 border border-white/10 rounded-2xl p-4 text-xs font-mono whitespace-pre-wrap max-h-[420px] overflow-y-auto">
              {localizedAutofeeResults.length ? localizedAutofeeResults.join('\n') : t('lightningOps.autofeeResultsEmpty')}
            </div>
          </>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('lightningOps.channels')}</h3>
          <div className="flex flex-wrap gap-2 text-xs">
            <button className={filter === 'all' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('all')}>{t('common.all')}</button>
            <button className={filter === 'active' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('active')}>{t('common.active')}</button>
            <button className={filter === 'inactive' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('inactive')}>{t('common.inactive')}</button>
          </div>
        </div>

        {(pendingOpen.length > 0 || pendingClose.length > 0) && (
          <div className="rounded-2xl border border-brass/30 bg-brass/10 p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h4 className="text-sm font-semibold text-brass">{t('lightningOps.pendingChannels')}</h4>
              <p className="text-xs text-brass">
                {t('lightningOps.opening')}: <span className="text-glow">{pendingOpen.length}</span> | {t('lightningOps.closing')}{' '}
                <span className="text-ember">{pendingClose.length}</span>
              </p>
            </div>
            <div className="mt-3 grid gap-3 lg:grid-cols-2">
              <div className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex items-center justify-between gap-2">
                  <h5 className="text-xs font-semibold text-glow uppercase tracking-wide">{t('lightningOps.opening')}</h5>
                  <span className="rounded-full px-2 py-1 text-[11px] bg-glow/20 text-glow">{pendingOpen.length}</span>
                </div>
                {pendingOpen.length ? (
                  <div className="mt-3 space-y-3">
                    {pendingOpen.map((ch) => {
                      const pointLink = mempoolLink(ch.channel_point)
                      return (
                        <div key={ch.channel_point} className="rounded-xl border border-white/10 bg-ink/70 p-3">
                          <div className="flex flex-wrap items-center justify-between gap-3">
                            <div>
                              {ch.remote_pubkey ? (
                                <a
                                  className="text-xs text-fog/70 hover:text-fog break-all"
                                  href={ambossURL(ch.remote_pubkey)}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {ch.peer_alias || ch.remote_pubkey}
                                </a>
                              ) : (
                                <p className="text-xs text-fog/70">{ch.peer_alias || t('lightningOps.unknownPeer')}</p>
                              )}
                              {pointLink ? (
                                <a
                                  className="mt-1 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                                  href={pointLink}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {t('lightningOps.pointLabel', { point: ch.channel_point })}
                                </a>
                              ) : (
                                <p className="mt-1 text-[11px] text-fog/50 break-all">
                                  {t('lightningOps.pointLabel', { point: ch.channel_point })}
                                </p>
                              )}
                            </div>
                            <span className="rounded-full px-2 py-1 text-[11px] bg-glow/20 text-glow">
                              {pendingStatusLabel(ch.status)}
                            </span>
                          </div>
                          <div className="mt-2 grid gap-2 lg:grid-cols-2 text-[11px] text-fog/60">
                            <div>{t('lightningOps.capacityLabel', { value: ch.capacity_sat })}</div>
                            {typeof ch.confirmations_until_active === 'number' && (
                              <div>{t('lightningOps.confirmationsLabel', { count: ch.confirmations_until_active })}</div>
                            )}
                          </div>
                          {ch.private !== undefined && (
                            <p className="mt-2 text-[11px] text-fog/50">
                              {ch.private ? t('lightningOps.privateChannel') : t('lightningOps.publicChannel')}
                            </p>
                          )}
                        </div>
                      )
                    })}
                  </div>
                ) : (
                  <p className="mt-3 text-xs text-fog/60">{t('lightningOps.noChannelsOpening')}</p>
                )}
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex items-center justify-between gap-2">
                  <h5 className="text-xs font-semibold text-ember uppercase tracking-wide">{t('lightningOps.closing')}</h5>
                  <span className="rounded-full px-2 py-1 text-[11px] bg-ember/20 text-ember">{pendingClose.length}</span>
                </div>
                {pendingClose.length ? (
                  <div className="mt-3 space-y-3">
                    {pendingClose.map((ch) => {
                      const pointLink = mempoolLink(ch.channel_point)
                      const maturitySeconds = estimateMaturitySeconds(ch.blocks_til_maturity)
                      const closingTxLink = mempoolTxLink(ch.closing_txid)
                      return (
                        <div key={ch.channel_point} className="rounded-xl border border-white/10 bg-ink/70 p-3">
                          <div className="flex flex-wrap items-center justify-between gap-3">
                            <div>
                              {ch.remote_pubkey ? (
                                <a
                                  className="text-xs text-fog/70 hover:text-fog break-all"
                                  href={ambossURL(ch.remote_pubkey)}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {ch.peer_alias || ch.remote_pubkey}
                                </a>
                              ) : (
                                <p className="text-xs text-fog/70">{ch.peer_alias || t('lightningOps.unknownPeer')}</p>
                              )}
                              {pointLink ? (
                                <a
                                  className="mt-1 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                                  href={pointLink}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {t('lightningOps.pointLabel', { point: ch.channel_point })}
                                </a>
                              ) : (
                                <p className="mt-1 text-[11px] text-fog/50 break-all">
                                  {t('lightningOps.pointLabel', { point: ch.channel_point })}
                                </p>
                              )}
                            </div>
                            <span className="rounded-full px-2 py-1 text-[11px] bg-ember/20 text-ember">
                              {pendingStatusLabel(ch.status)}
                            </span>
                          </div>
                          <div className="mt-2 grid gap-2 lg:grid-cols-2 text-[11px] text-fog/60">
                            <div>{t('lightningOps.capacityLabel', { value: ch.capacity_sat })}</div>
                            {typeof ch.blocks_til_maturity === 'number' && (
                              <div className="space-y-1">
                                <div>{t('lightningOps.blocksToMaturity', { count: ch.blocks_til_maturity })}</div>
                                {maturitySeconds !== null && (
                                  <div className="text-fog/50">
                                    {t('lightningOps.maturityEta', { time: formatMaturityDuration(maturitySeconds) })}
                                  </div>
                                )}
                              </div>
                            )}
                          </div>
                          {ch.closing_txid && (
                            closingTxLink ? (
                              <a
                                className="mt-2 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                                href={closingTxLink}
                                target="_blank"
                                rel="noopener noreferrer"
                              >
                                {t('lightningOps.closingTx', { txid: ch.closing_txid })}
                              </a>
                            ) : (
                              <p className="mt-2 text-[11px] text-fog/50 break-all">{t('lightningOps.closingTx', { txid: ch.closing_txid })}</p>
                            )
                          )}
                        </div>
                      )
                    })}
                  </div>
                ) : (
                  <p className="mt-3 text-xs text-fog/60">{t('lightningOps.noChannelsClosing')}</p>
                )}
              </div>
            </div>
          </div>
        )}

        <div className="grid gap-3 lg:grid-cols-4">
            <input
              className="input-field"
              placeholder={t('lightningOps.searchPlaceholder')}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <input
              className="input-field"
              placeholder={t('lightningOps.minCapacity')}
              type="number"
              min={0}
              value={minCapacity}
              onChange={(e) => setMinCapacity(e.target.value)}
            />
            <select className="input-field" value={sortBy} onChange={(e) => setSortBy(e.target.value as any)}>
            <option value="capacity">{t('lightningOps.sortByCapacity')}</option>
            <option value="local">{t('lightningOps.sortByLocal')}</option>
            <option value="remote">{t('lightningOps.sortByRemote')}</option>
            <option value="alias">{t('lightningOps.sortByPeer')}</option>
            </select>
            <div className="flex flex-wrap items-center gap-2">
              <button className="btn-secondary text-xs px-3 py-2" onClick={() => setSortDir(sortDir === 'desc' ? 'asc' : 'desc')}>
              {sortDir === 'desc' ? t('lightningOps.sortDesc') : t('lightningOps.sortAsc')}
              </button>
              <label className="flex items-center gap-2 text-[11px] text-fog/70 sm:text-xs">
                <input type="checkbox" checked={showPrivate} onChange={(e) => setShowPrivate(e.target.checked)} />
              {t('lightningOps.showPrivate')}
              </label>
              <label className="flex items-center gap-2 text-[11px] text-fog/70 sm:text-xs">
                <input
                  type="checkbox"
                  checked={autofeeAllChecked}
                  onChange={(e) => handleAutofeeBulk(e.target.checked)}
                  disabled={autofeeBusy}
                />
                {t('lightningOps.autofeeAll')}
              </label>
            </div>
          </div>
        {filteredChannels.length ? (
          <div className="max-h-[520px] overflow-y-auto pr-2">
            <div className="grid gap-3">
              {filteredChannels.map((ch) => {
                const localDisabled = ch.local_disabled ?? isLocalChanDisabled(ch.chan_status_flags)
                const statusBusy = chanStatusBusy === ch.channel_point
                const showToggle = ch.active
                const autofeeChecked = autofeeSettings[ch.channel_id] ?? true
                const classLabel = formatChannelClassLabel(ch.class_label)
                const isInlineFeeEditing = inlineFeeChannelPoint === ch.channel_point
                const inlineBusy = inlineFeeLoading || inlineFeeSaving
                const visualTotal = (ch.local_balance_sat + ch.remote_balance_sat) > 0
                  ? (ch.local_balance_sat + ch.remote_balance_sat)
                  : (ch.capacity_sat > 0 ? ch.capacity_sat : 0)
                const localPctRaw = visualTotal > 0 ? (ch.local_balance_sat / visualTotal) * 100 : 0
                const remotePctRaw = visualTotal > 0 ? (ch.remote_balance_sat / visualTotal) * 100 : 0
                const localPct = Math.max(0, Math.min(100, localPctRaw))
                const remotePct = Math.max(0, Math.min(100, remotePctRaw))
                const localPctLabel = `${localPct.toFixed(0)}%`
                const remotePctLabel = `${remotePct.toFixed(0)}%`
                const cardClass = localDisabled && ch.active
                  ? 'rounded-2xl border border-ember/40 bg-ember/10 p-4'
                  : 'rounded-2xl border border-white/10 bg-ink/60 p-4'
                return (
                  <div key={ch.channel_point} className={cardClass}>
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        {ch.remote_pubkey ? (
                          <a
                            className="text-sm text-fog/60 hover:text-fog"
                            href={ambossURL(ch.remote_pubkey)}
                            target="_blank"
                            rel="noopener noreferrer"
                          >
                            {ch.peer_alias || ch.remote_pubkey}
                          </a>
                        ) : (
                          <p className="text-sm text-fog/60">{ch.peer_alias || t('lightningOps.unknownPeer')}</p>
                        )}
                        <p className="text-xs text-fog/50 break-all">
                          {t('lightningOps.pointCapacity', { point: ch.channel_point, capacity: ch.capacity_sat })}
                        </p>
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        <span className={`rounded-full px-3 py-1 text-xs ${ch.active ? 'bg-glow/20 text-glow' : 'bg-ember/20 text-ember'}`}>
                          {ch.active ? t('common.active') : t('common.inactive')}
                        </span>
                        {showToggle && (
                          <button
                            className={`btn-secondary text-xs px-3 py-1 ${statusBusy ? 'opacity-60 pointer-events-none' : ''}`}
                            type="button"
                            onClick={() => handleToggleChanStatus(ch)}
                            disabled={statusBusy}
                          >
                            {localDisabled ? t('lightningOps.enableChannel') : t('lightningOps.disableChannel')}
                          </button>
                        )}
                        <label className="flex items-center gap-2 text-[11px] text-fog/70">
                          <input
                            type="checkbox"
                            checked={autofeeChecked}
                            onChange={(e) => handleAutofeeChannelToggle(ch, e.target.checked)}
                          />
                          {t('lightningOps.autofeeLabel')}
                        </label>
                      </div>
                    </div>
                    <div className="mt-3 space-y-2">
                      <div className="flex items-center justify-between gap-3 text-xs text-fog/70">
                        <span>{t('lightningOps.localLabel', { value: ch.local_balance_sat })}</span>
                        <span>{t('lightningOps.remoteLabel', { value: ch.remote_balance_sat })}</span>
                      </div>
                      <div className="flex items-center gap-2 text-[11px]">
                        <span className="w-12 text-right text-glow">{localPctLabel}</span>
                        <div className="relative h-2 flex-1 overflow-hidden rounded-full bg-white/10">
                          <div
                            className="absolute inset-y-0 left-0 bg-glow/70"
                            style={{ width: `${localPct}%` }}
                          />
                          <div
                            className="absolute inset-y-0 right-0 bg-white/35"
                            style={{ width: `${remotePct}%` }}
                          />
                        </div>
                        <span className="w-12 text-left text-fog/70">{remotePctLabel}</span>
                      </div>
                    </div>
                    <div className="mt-3 grid gap-3 lg:grid-cols-6 text-xs text-fog/70">
                      <div>
                        {t('lightningOps.outRate')}:{' '}
                        <button
                          type="button"
                          className="text-fog hover:text-white hover:underline underline-offset-2"
                          onClick={() => { void startInlineFeeEdit(ch) }}
                        >
                          {typeof ch.fee_rate_ppm === 'number' ? `${ch.fee_rate_ppm} ppm` : '-'}
                        </button>
                      </div>
                      <div>
                        {t('lightningOps.outBase')}:{' '}
                        <button
                          type="button"
                          className="text-fog hover:text-white hover:underline underline-offset-2"
                          onClick={() => { void startInlineFeeEdit(ch) }}
                        >
                          {typeof ch.base_fee_msat === 'number' ? `${ch.base_fee_msat} msats` : '-'}
                        </button>
                      </div>
                      <div>
                        {t('lightningOps.inRate')}:{' '}
                        <button
                          type="button"
                          className="text-fog hover:text-white hover:underline underline-offset-2"
                          onClick={() => { void startInlineFeeEdit(ch) }}
                        >
                          {typeof ch.inbound_fee_rate_ppm === 'number' ? `${ch.inbound_fee_rate_ppm} ppm` : '-'}
                        </button>
                      </div>
                      <div>
                        {t('lightningOps.peerRate')}:{' '}
                        <span className="text-fog">
                          {typeof ch.peer_fee_rate_ppm === 'number' ? `${ch.peer_fee_rate_ppm} ppm` : '-'}
                        </span>
                      </div>
                      <div>
                        {t('lightningOps.peerBase')}:{' '}
                        <span className="text-fog">
                          {typeof ch.peer_base_msat === 'number' ? `${ch.peer_base_msat} msats` : '-'}
                        </span>
                      </div>
                      <div className="text-right">
                        {classLabel && (
                          <span className="text-fog/60">{classLabel}</span>
                        )}
                      </div>
                    </div>
                    {isInlineFeeEditing && (
                      <div className="mt-3 rounded-xl border border-white/10 bg-ink/70 p-3">
                        <div className="grid gap-2 lg:grid-cols-3">
                          <input
                            className="input-field"
                            type="number"
                            min={0}
                            placeholder={t('lightningOps.feeRatePpm')}
                            value={inlineFeeRatePpm}
                            onChange={(e) => setInlineFeeRatePpm(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                void handleInlineFeeSave()
                              }
                              if (e.key === 'Escape') {
                                e.preventDefault()
                                cancelInlineFeeEdit()
                              }
                            }}
                          />
                          <input
                            className="input-field"
                            type="number"
                            min={0}
                            placeholder={t('lightningOps.baseFeeMsats')}
                            value={inlineBaseFeeMsat}
                            onChange={(e) => setInlineBaseFeeMsat(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                void handleInlineFeeSave()
                              }
                              if (e.key === 'Escape') {
                                e.preventDefault()
                                cancelInlineFeeEdit()
                              }
                            }}
                          />
                          <input
                            className="input-field"
                            type="number"
                            max={0}
                            placeholder={t('lightningOps.inboundFeeRate')}
                            value={inlineInboundFeeRatePpm}
                            onChange={(e) => setInlineInboundFeeRatePpm(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                void handleInlineFeeSave()
                              }
                              if (e.key === 'Escape') {
                                e.preventDefault()
                                cancelInlineFeeEdit()
                              }
                            }}
                          />
                        </div>
                        <p className="mt-2 text-[11px] text-fog/60">{t('lightningOps.inlineFeeEditHint')}</p>
                        {inlineFeeLoading && <p className="mt-2 text-xs text-fog/60">{t('lightningOps.loadingFees')}</p>}
                        {inlineFeeStatus && <p className="mt-2 text-xs text-brass">{inlineFeeStatus}</p>}
                        <div className="mt-3 flex flex-wrap gap-2">
                          <button
                            className="btn-secondary text-xs px-3 py-2"
                            type="button"
                            onClick={() => { void handleInlineFeeSave() }}
                            disabled={inlineBusy}
                          >
                            {inlineBusy ? t('lightningOps.updatingFees') : t('lightningOps.updateFees')}
                          </button>
                          <button
                            className="btn-secondary text-xs px-3 py-2"
                            type="button"
                            onClick={cancelInlineFeeEdit}
                            disabled={inlineBusy}
                          >
                            {t('common.cancel')}
                          </button>
                        </div>
                      </div>
                    )}
                    <div className="mt-2 text-xs text-fog/50">
                      {ch.private ? t('lightningOps.privateChannel') : t('lightningOps.publicChannel')}
                    </div>
                    {localDisabled && ch.active && (
                      <div className="mt-2 text-xs text-amber-200">{t('lightningOps.localDisabled')}</div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        ) : (
          <p className="text-sm text-fog/60">{t('lightningOps.noChannelsFound')}</p>
        )}
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('lightningOps.addPeer')}</h3>
          <input
            className="input-field"
            placeholder={t('lightningOps.peerAddressPlaceholder')}
            value={peerAddress}
            onChange={(e) => setPeerAddress(e.target.value)}
          />
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input
              type="checkbox"
              checked={peerTemporary}
              onChange={(e) => setPeerTemporary(e.target.checked)}
            />
            {t('lightningOps.temporaryPeer')}
          </label>
          <div className="flex flex-wrap gap-3">
            <button className="btn-primary" onClick={handleConnectPeer}>{t('lightningOps.connectPeer')}</button>
            <button
              className="btn-secondary disabled:opacity-60 disabled:cursor-not-allowed"
              onClick={handleBoostPeers}
              disabled={boostRunning}
              title={t('lightningOps.boostHint')}
            >
              {boostRunning ? t('lightningOps.boosting') : t('lightningOps.boostPeers')}
            </button>
          </div>
          {peerStatus && <p className="text-sm text-brass">{peerStatus}</p>}
          {boostStatus && <p className="text-sm text-brass">{boostStatus}</p>}
        </div>

        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('lightningOps.openChannel')}</h3>
          <input
            className="input-field"
            placeholder={t('lightningOps.peerAddressPlaceholder')}
            value={openPeer}
            onChange={(e) => setOpenPeer(e.target.value)}
          />
          <div className="grid gap-4 lg:grid-cols-2">
            <input
              className="input-field"
              placeholder={t('lightningOps.fundingAmount')}
              type="number"
              min={20000}
              value={openAmount}
              onChange={(e) => setOpenAmount(e.target.value)}
            />
            <input
              className="input-field"
              placeholder={t('lightningOps.closeAddressOptional')}
              type="text"
              value={openCloseAddress}
              onChange={(e) => setOpenCloseAddress(e.target.value)}
            />
          </div>
          <label className="text-sm text-fog/70">
            {t('lightningOps.feeRate')}
            <span className="ml-2 text-xs text-fog/50">
              {t('lightningOps.feeHint', { fastest: openFeeHint?.fastest ?? '-', hour: openFeeHint?.hour ?? '-' })}
            </span>
          </label>
          <div className="flex flex-wrap items-center gap-3">
            <input
              className="input-field flex-1 min-w-[140px]"
              placeholder={t('common.auto')}
              type="number"
              min={1}
              value={openFeeRate}
              onChange={(e) => setOpenFeeRate(e.target.value)}
            />
            <button
              className="btn-secondary text-xs px-3 py-2"
              type="button"
              onClick={() => {
                if (openFeeHint?.fastest) {
                  setOpenFeeRate(String(openFeeHint.fastest))
                }
              }}
              disabled={!openFeeHint?.fastest}
            >
              {t('lightningOps.useFastest')}
            </button>
            {openFeeStatus && <p className="text-xs text-fog/50">{openFeeStatus}</p>}
          </div>
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input type="checkbox" checked={openPrivate} onChange={(e) => setOpenPrivate(e.target.checked)} />
            {t('lightningOps.privateChannel')}
          </label>
          <button className="btn-primary" onClick={handleOpenChannel}>{t('lightningOps.openChannel')}</button>
          <p className="text-xs text-fog/50">{t('lightningOps.minimumFundingNote')}</p>
          {openStatus && (
            <div className="text-sm text-brass break-words">
              <p>{openStatus}</p>
              {openChannelPoint && mempoolLink(openChannelPoint) && (
                <a
                  className="mt-1 block text-emerald-200 hover:text-emerald-100 break-all"
                  href={mempoolLink(openChannelPoint)}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {t('lightningOps.fundingTx', { point: openChannelPoint })}
                </a>
              )}
            </div>
          )}
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('lightningOps.closeChannel')}</h3>
          <select className="input-field" value={closePoint} onChange={(e) => setClosePoint(e.target.value)}>
            <option value="">{t('lightningOps.selectChannel')}</option>
            {channelOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
          <label className="text-sm text-fog/70">
            {t('lightningOps.feeRate')}
            <span className="ml-2 text-xs text-fog/50">
              {t('lightningOps.feeHint', { fastest: closeFeeHint?.fastest ?? '-', hour: closeFeeHint?.hour ?? '-' })}
            </span>
          </label>
          <div className="flex flex-wrap items-center gap-3">
            <input
              className="input-field flex-1 min-w-[140px]"
              placeholder={t('common.auto')}
              type="number"
              min={1}
              value={closeFeeRate}
              onChange={(e) => setCloseFeeRate(e.target.value)}
            />
            <button
              className="btn-secondary text-xs px-3 py-2"
              type="button"
              onClick={() => {
                if (closeFeeHint?.fastest) {
                  setCloseFeeRate(String(closeFeeHint.fastest))
                }
              }}
              disabled={!closeFeeHint?.fastest}
            >
              {t('lightningOps.useFastest')}
            </button>
            {closeFeeStatus && <p className="text-xs text-fog/50">{closeFeeStatus}</p>}
          </div>
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input type="checkbox" checked={closeForce} onChange={(e) => setCloseForce(e.target.checked)} />
            {t('lightningOps.forceClose')}
          </label>
          <button className="btn-secondary" onClick={handleCloseChannel}>{t('lightningOps.closeChannel')}</button>
          {closeStatus && <p className="text-sm text-brass">{closeStatus}</p>}
        </div>

        <div className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('lightningOps.updateFees')}</h3>
          <div className="flex flex-wrap gap-3 text-sm">
            <button
              className={feeScopeAll ? 'btn-primary' : 'btn-secondary'}
              onClick={() => setFeeScopeAll(true)}
            >
              {t('lightningOps.applyToAll')}
            </button>
            <button
              className={!feeScopeAll ? 'btn-primary' : 'btn-secondary'}
              onClick={() => setFeeScopeAll(false)}
            >
              {t('lightningOps.applyToOne')}
            </button>
          </div>
          {!feeScopeAll && (
            <select className="input-field" value={feeChannelPoint} onChange={(e) => setFeeChannelPoint(e.target.value)}>
              <option value="">{t('lightningOps.selectChannel')}</option>
              {channelOptions.map((opt) => (
                <option key={opt.value} value={opt.value}>{opt.label}</option>
              ))}
            </select>
          )}
          {feeLoadStatus && (
            <p className="text-xs text-fog/60">{feeLoadStatus}</p>
          )}
          <div className="grid gap-4 lg:grid-cols-3">
            <input
              className="input-field"
              placeholder={t('lightningOps.feeRatePpm')}
              type="number"
              min={0}
              value={feeRatePpm}
              onChange={(e) => setFeeRatePpm(e.target.value)}
            />
            <input
              className="input-field"
              placeholder={t('lightningOps.baseFeeMsats')}
              type="number"
              min={0}
              value={baseFeeMsat}
              onChange={(e) => setBaseFeeMsat(e.target.value)}
            />
            <input
              className="input-field"
              placeholder={t('lightningOps.timeLockDelta')}
              type="number"
              min={0}
              value={timeLockDelta}
              onChange={(e) => setTimeLockDelta(e.target.value)}
            />
          </div>
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input
              type="checkbox"
              checked={inboundEnabled}
              onChange={(e) => setInboundEnabled(e.target.checked)}
            />
            {t('lightningOps.includeInboundFees')}
          </label>
          {inboundEnabled && (
            <div className="grid gap-4 lg:grid-cols-2">
              <input
                className="input-field"
                placeholder={t('lightningOps.inboundFeeRate')}
                type="number"
                value={inboundFeeRatePpm}
                onChange={(e) => setInboundFeeRatePpm(e.target.value)}
              />
              <input
                className="input-field"
                placeholder={t('lightningOps.inboundBaseFee')}
                type="number"
                value={inboundBaseMsat}
                onChange={(e) => setInboundBaseMsat(e.target.value)}
              />
            </div>
          )}
          <p className="text-xs text-fog/50">{t('lightningOps.inboundFeesNote')}</p>
          <button className="btn-secondary" onClick={handleUpdateFees}>{t('lightningOps.updateFees')}</button>
          {feeStatus && <p className="text-sm text-brass">{feeStatus}</p>}
        </div>
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.htlcManagerTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.htlcManagerSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(htlcManagerTone())}`}>
              {htlcManagerBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${htlcManagerBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleHtlcManager}
              type="button"
              disabled={htlcManagerBusy}
              aria-label={t('lightningOps.toggleHtlcManager')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${htlcManager?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!htlcManager?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${htlcManager?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <label className="text-sm text-fog/70">
            {t('lightningOps.htlcManagerMinSat')}
            <input
              className="input-field mt-2"
              type="number"
              min={1}
              value={htlcManagerMinSat}
              onChange={(e) => {
                setHtlcManagerMinSat(e.target.value)
                htlcManagerFormDirtyRef.current = true
              }}
            />
          </label>
          <label className="text-sm text-fog/70">
            {t('lightningOps.htlcManagerMaxPct')}
            <input
              className="input-field mt-2"
              type="number"
              min={0}
              value={htlcManagerMaxPct}
              onChange={(e) => {
                setHtlcManagerMaxPct(e.target.value)
                htlcManagerFormDirtyRef.current = true
              }}
            />
          </label>
          <label className="text-sm text-fog/70">
            {t('lightningOps.htlcManagerInterval')}
            <input
              className="input-field mt-2"
              type="number"
              min={1}
              max={2880}
              value={htlcManagerIntervalMinutes}
              onChange={(e) => {
                setHtlcManagerIntervalMinutes(e.target.value)
                htlcManagerFormDirtyRef.current = true
              }}
            />
          </label>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={handleSaveHtlcManager} disabled={htlcManagerBusy}>
            {t('common.save')}
          </button>
          <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={handleRunHtlcManagerNow} disabled={htlcManagerBusy}>
            {t('lightningOps.htlcManagerRunNow')}
          </button>
          <button
            className="btn-secondary text-xs px-3 py-2"
            type="button"
            onClick={() => setHtlcManagerLogsOpen((open) => !open)}
          >
            {htlcManagerLogsOpen ? t('common.hide') : t('lightningOps.htlcManagerLogsShow', { count: htlcManagerLogs.length })}
          </button>
          <button
            className="btn-secondary text-xs px-3 py-2"
            type="button"
            onClick={() => setHtlcManagerFailedOpen((open) => !open)}
          >
            {htlcManagerFailedOpen ? t('common.hide') : t('lightningOps.htlcManagerFailedShow', { count: visibleHtlcManagerFailed.length })}
          </button>
        </div>
        {htlcManagerStatus && <p className="text-sm text-brass">{htlcManagerStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-3">
          <div>
            {t('lightningOps.htlcManagerLastRun')}: <span className="text-fog">{formatAmbossTime(htlcManager?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.htlcManagerLastAttempt')}: <span className="text-fog">{formatAmbossTime(htlcManager?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.htlcManagerLastChanged')}: <span className="text-fog">{htlcManager?.last_changed_count ?? 0}</span>
          </div>
        </div>
        {htlcManager?.last_error && (
          <p className="text-xs text-amber-200">
            {t('lightningOps.htlcManagerLastError')}: {htlcManager.last_error}
          </p>
        )}
        {htlcManagerLogsOpen && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 max-h-[260px] overflow-y-auto">
            {htlcManagerLogs.length ? (
              <div className="space-y-2 text-xs text-fog/70">
                {htlcManagerLogs.map((entry, idx) => (
                  <p key={`${entry.ts}-${entry.channel_point}-${idx}`}>
                    {t('lightningOps.htlcManagerLogLine', {
                      ts: formatAmbossTime(entry.ts),
                      alias: entry.alias || entry.channel_point,
                      oldMinSat: Math.round((entry.old_min_msat || 0) / 1000),
                      newMinSat: Math.round((entry.new_min_msat || 0) / 1000),
                      oldMaxSat: Math.round((entry.old_max_msat || 0) / 1000),
                      newMaxSat: Math.round((entry.new_max_msat || 0) / 1000)
                    })}
                  </p>
                ))}
              </div>
            ) : (
              <p className="text-xs text-fog/50">{t('lightningOps.htlcManagerLogsEmpty')}</p>
            )}
          </div>
        )}
        {htlcManagerFailedOpen && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3">
            {visibleHtlcManagerFailed.length ? (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[940px] text-xs text-fog/80">
                  <thead>
                    <tr className="text-left text-fog/60">
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedTs')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedChanIn')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedChanOut')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedInAmt')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedOutAmt')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedPotentialFee')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedCode')}</th>
                      <th className="py-2">{t('lightningOps.htlcManagerFailedDetail')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleHtlcManagerFailed.map((entry, idx) => (
                      <tr key={`${entry.ts}-${entry.incoming_channel_id}-${entry.outgoing_channel_id}-${idx}`} className="border-t border-white/5">
                        <td className="py-2 pr-3 whitespace-nowrap">{formatAmbossTime(entry.ts)}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">
                          <div className="text-fog">{entry.incoming_alias || '-'}</div>
                          <div className="text-[11px] text-fog/50">{entry.incoming_channel_id || '-'}</div>
                        </td>
                        <td className="py-2 pr-3 whitespace-nowrap">
                          <div className="text-fog">{entry.outgoing_alias || '-'}</div>
                          <div className="text-[11px] text-fog/50">{entry.outgoing_channel_id || '-'}</div>
                        </td>
                        <td className="py-2 pr-3 whitespace-nowrap">{formatSatFromMsat(entry.incoming_amt_msat)}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">{formatSatFromMsat(entry.outgoing_amt_msat)}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">{formatFeeMsat(entry.potential_fee_msat)}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">
                          {(entry.failure_code || '-')}
                          {entry.event ? <span className="ml-1 text-fog/50">({entry.event})</span> : null}
                        </td>
                        <td className="py-2">
                          {entry.failure_detail || entry.failure_reason || '-'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="text-xs text-fog/50">{t('lightningOps.htlcManagerFailedEmpty')}</p>
            )}
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.ambossHealthTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.ambossHealthSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(ambossTone())}`}>
              {ambossBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${ambossBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleAmboss}
              type="button"
              disabled={ambossBusy}
              aria-label={t('lightningOps.toggleAmbossHealth')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${amboss?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!amboss?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${amboss?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        {ambossStatus && <p className="text-sm text-brass">{ambossStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-3">
          <div>
            {t('lightningOps.ambossHealthLastPing')}: <span className="text-fog">{formatAmbossTime(amboss?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.ambossHealthLastAttempt')}: <span className="text-fog">{formatAmbossTime(amboss?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.ambossHealthInterval')}: <span className="text-fog">{amboss?.interval_sec ? `${amboss.interval_sec}s` : '-'}</span>
          </div>
        </div>
      {amboss?.last_error && (
        <p className="text-xs text-amber-200">
          {t('lightningOps.ambossHealthLastError')}: {amboss.last_error}
        </p>
      )}
    </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.chanHealTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.chanHealSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(chanHealTone())}`}>
              {chanHealBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${chanHealBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleChanHeal}
              type="button"
              disabled={chanHealBusy}
              aria-label={t('lightningOps.toggleChanHeal')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${chanHeal?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!chanHeal?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${chanHeal?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <label className="text-sm text-fog/70">
            {t('lightningOps.chanHealInterval')}
          </label>
          <div className="flex items-center gap-2">
            <input
              className="input-field w-32"
              type="number"
              min={30}
              value={chanHealInterval}
              onChange={(e) => {
                setChanHealInterval(e.target.value)
                chanHealIntervalDirtyRef.current = true
              }}
            />
            <button
              className="btn-secondary text-xs px-3 py-2"
              type="button"
              onClick={handleSaveChanHealInterval}
              disabled={chanHealBusy}
            >
              {t('common.save')}
            </button>
          </div>
        </div>
        {chanHealStatus && <p className="text-sm text-brass">{chanHealStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-3">
          <div>
            {t('lightningOps.chanHealLastRun')}: <span className="text-fog">{formatAmbossTime(chanHeal?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.chanHealLastAttempt')}: <span className="text-fog">{formatAmbossTime(chanHeal?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.chanHealInterval')}: <span className="text-fog">{chanHeal?.interval_sec ? `${chanHeal.interval_sec}s` : '-'}</span>
          </div>
        </div>
        {chanHeal?.last_ok_at && typeof chanHeal?.last_updated === 'number' && (
          <p className="text-xs text-fog/60">
            {t('lightningOps.chanHealLastUpdated', { count: chanHeal.last_updated })}
          </p>
        )}
        {chanHeal?.last_error && (
          <p className="text-xs text-amber-200">
            {t('lightningOps.chanHealLastError')}: {chanHeal.last_error}
          </p>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.torPeerTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.torPeerSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(torPeerCheckerTone())}`}>
              {torPeerCheckerBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${torPeerCheckerBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleTorPeerChecker}
              type="button"
              disabled={torPeerCheckerBusy}
              aria-label={t('lightningOps.toggleTorPeerChecker')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${torPeerChecker?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!torPeerChecker?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${torPeerChecker?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <label className="text-sm text-fog/70">
            {t('lightningOps.torPeerInterval')}
          </label>
          <div className="flex items-center gap-2">
            <input
              className="input-field w-32"
              type="number"
              min={2}
              max={168}
              value={torPeerCheckerIntervalHours}
              onChange={(e) => {
                setTorPeerCheckerIntervalHours(e.target.value)
                torPeerCheckerIntervalDirtyRef.current = true
              }}
            />
            <button
              className="btn-secondary text-xs px-3 py-2"
              type="button"
              onClick={handleSaveTorPeerChecker}
              disabled={torPeerCheckerBusy}
            >
              {t('common.save')}
            </button>
          </div>
          <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={handleRunTorPeerCheckerNow} disabled={torPeerCheckerBusy}>
            {t('lightningOps.torPeerRunNow')}
          </button>
          <button
            className="btn-secondary text-xs px-3 py-2"
            type="button"
            onClick={() => setTorPeerCheckerLogsOpen((open) => !open)}
          >
            {torPeerCheckerLogsOpen ? t('common.hide') : t('lightningOps.torPeerLogsShow', { count: torPeerCheckerLogs.length })}
          </button>
        </div>
        {torPeerCheckerStatus && <p className="text-sm text-brass">{torPeerCheckerStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-4">
          <div>
            {t('lightningOps.torPeerLastRun')}: <span className="text-fog">{formatAmbossTime(torPeerChecker?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.torPeerLastAttempt')}: <span className="text-fog">{formatAmbossTime(torPeerChecker?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.torPeerLastChecked')}: <span className="text-fog">{torPeerChecker?.last_checked_count ?? 0}</span>
          </div>
          <div>
            {t('lightningOps.torPeerLastSwitched')}: <span className="text-fog">{torPeerChecker?.last_switched_count ?? 0}</span>
          </div>
        </div>
        {torPeerChecker?.last_error && (
          <p className="text-xs text-amber-200">
            {t('lightningOps.torPeerLastError')}: {torPeerChecker.last_error}
          </p>
        )}
        {torPeerCheckerLogsOpen && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 max-h-[260px] overflow-y-auto">
            {torPeerCheckerLogs.length ? (
              <div className="space-y-2 text-xs text-fog/70">
                {torPeerCheckerLogs.map((entry, idx) => (
                  <p key={`${entry.ts}-${entry.pub_key || entry.alias}-${idx}`}>
                    {t('lightningOps.torPeerLogLine', {
                      ts: formatAmbossTime(entry.ts),
                      alias: entry.alias || entry.pub_key || '-',
                      result: entry.result || '-',
                      detail: entry.detail || '-',
                      from: entry.from_address || '-',
                      to: entry.to_address || '-'
                    })}
                  </p>
                ))}
              </div>
            ) : (
              <p className="text-xs text-fog/50">{t('lightningOps.torPeerLogsEmpty')}</p>
            )}
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <div>
          <h3 className="text-lg font-semibold">{t('lightningOps.signMessageTitle')}</h3>
          <p className="text-sm text-fog/60">{t('lightningOps.signMessageSubtitle')}</p>
        </div>
        <textarea
          className="input-field min-h-[120px]"
          placeholder={t('lightningOps.signMessagePlaceholder')}
          value={signMessage}
          onChange={(e) => {
            const value = e.target.value
            setSignMessage(value)
            if (signSignature) setSignSignature('')
            if (signCopied) setSignCopied(false)
            if (signStatus) setSignStatus('')
          }}
        />
        <div className="flex flex-wrap items-center gap-3">
          <button
            className="btn-primary disabled:opacity-60 disabled:cursor-not-allowed"
            onClick={handleSignMessage}
            disabled={signBusy || !signMessage.trim()}
            type="button"
          >
            {signBusy ? t('lightningOps.signMessageSigning') : t('lightningOps.signMessageAction')}
          </button>
          {signStatus && <p className="text-sm text-brass">{signStatus}</p>}
        </div>
        {signSignature && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3">
            <div className="flex items-center justify-between text-xs text-fog/60">
              <span>{t('lightningOps.signatureLabel')}</span>
              <button
                className="text-fog/50 hover:text-fog"
                onClick={handleCopySignature}
                title={t('lightningOps.copySignature')}
                aria-label={t('lightningOps.copySignature')}
                type="button"
              >
                <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6">
                  <rect x="9" y="9" width="11" height="11" rx="2" />
                  <rect x="4" y="4" width="11" height="11" rx="2" />
                </svg>
              </button>
            </div>
            <p className="mt-2 text-xs font-mono break-all">{signSignature}</p>
            {signCopied && <p className="mt-2 text-xs text-fog/60">{t('common.copied')}</p>}
          </div>
        )}
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('lightningOps.peers')}</h3>
          <span className="text-xs text-fog/60">{t('lightningOps.connectedPeers', { count: peers.length })}</span>
        </div>
        {peerActionStatus && <p className="text-sm text-brass">{peerActionStatus}</p>}
        {peerListStatus && <p className="text-sm text-brass">{peerListStatus}</p>}
        {peers.length ? (
          <div className="max-h-[520px] overflow-y-auto pr-2">
            <div className="grid gap-3">
              {peers.map((peer) => (
                <div key={peer.pub_key} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      {peer.pub_key ? (
                        <a
                          className="text-sm text-fog/60 hover:text-fog"
                          href={ambossURL(peer.pub_key)}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {peer.alias || peer.pub_key}
                        </a>
                      ) : (
                        <p className="text-sm text-fog/60">{peer.alias || t('lightningOps.unknownPeer')}</p>
                      )}
                      <p className="text-xs text-fog/50">{peer.address || t('lightningOps.addressUnknown')}</p>
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="rounded-full px-3 py-1 text-xs bg-white/10 text-fog/70">
                        {peer.inbound ? t('lightningOps.inbound') : t('lightningOps.outbound')}
                      </span>
                      <button className="btn-secondary text-xs px-3 py-1.5" onClick={() => handleDisconnect(peer.pub_key)}>
                        {t('lightningOps.disconnect')}
                      </button>
                    </div>
                  </div>
                  {peer.alias && (
                    <p className="mt-2 text-xs text-fog/50">{t('lightningOps.pubkeyLabel', { pubkey: peer.pub_key })}</p>
                  )}
                  <div className="mt-3 grid gap-3 lg:grid-cols-3 text-xs text-fog/70">
                    <div>{t('lightningOps.satSent', { value: peer.sat_sent })}</div>
                    <div>{t('lightningOps.satRecv', { value: peer.sat_recv })}</div>
                    <div>{t('lightningOps.pingLabel', { value: formatPing(peer.ping_time) })}</div>
                  </div>
                  <div className="mt-2 grid gap-3 lg:grid-cols-2 text-xs text-fog/60">
                    <div>{t('lightningOps.bytesSent', { value: peer.bytes_sent })}</div>
                    <div>{t('lightningOps.bytesRecv', { value: peer.bytes_recv })}</div>
                  </div>
                  {peer.sync_type && (
                    <p className="mt-2 text-xs text-fog/50">{t('lightningOps.syncLabel', { value: peer.sync_type })}</p>
                  )}
                  {peer.last_error && (
                    <p className="mt-2 text-xs text-ember">
                      {t('lightningOps.lastError', {
                        age: peer.last_error_time ? ` (${formatAge(peer.last_error_time)})` : '',
                        error: peer.last_error
                      })}
                    </p>
                  )}
                </div>
              ))}
            </div>
          </div>
        ) : (
          <p className="text-sm text-fog/60">{t('lightningOps.noConnectedPeers')}</p>
        )}
      </div>
    </section>
  )
}
