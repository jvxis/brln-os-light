import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { boostPeers, closeChannel, connectPeer, disconnectPeer, getAmbossHealth, getLnChanHeal, getLnChannelFees, getLnChannels, getLnPeers, getMempoolFees, openChannel, updateAmbossHealth, updateChannelFees, updateLnChanHeal, updateLnChannelStatus } from '../api'

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
  const [sortBy, setSortBy] = useState<'capacity' | 'local' | 'remote' | 'alias'>('capacity')
  const [sortDir, setSortDir] = useState<'desc' | 'asc'>('desc')
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

  const isLocalChanDisabled = (flags?: string) => {
    if (!flags) return false
    const normalized = flags.toLowerCase()
    return (normalized.includes('local') && normalized.includes('disabled')) ||
      normalized.includes('localchandisabled') ||
      normalized.includes('local_chan_disabled')
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

  const ambossURL = (pubkey: string) => `https://amboss.space/node/${pubkey}`

  const load = async () => {
    setStatus(t('lightningOps.loadingChannels'))
    setPeerListStatus(t('lightningOps.loadingPeers'))
    setAmbossStatus(t('lightningOps.ambossHealthLoading'))
    setChanHealStatus(t('lightningOps.chanHealLoading'))
    const [channelsResult, peersResult, ambossResult, chanHealResult] = await Promise.allSettled([
      getLnChannels(),
      getLnPeers(),
      getAmbossHealth(),
      getLnChanHeal()
    ])
    if (channelsResult.status === 'fulfilled') {
      const res = channelsResult.value
      const list = Array.isArray(res?.channels) ? res.channels : []
      setChannels(list)
      setActiveCount(res?.active_count ?? 0)
      setInactiveCount(res?.inactive_count ?? 0)
      setPendingOpenCount(res?.pending_open_count ?? 0)
      setPendingCloseCount(res?.pending_close_count ?? 0)
      setPendingChannels(Array.isArray(res?.pending_channels) ? res.pending_channels : [])
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
      if (payload?.interval_sec) {
        setChanHealInterval(String(payload.interval_sec))
      }
      setChanHealStatus('')
    } else {
      const message = (chanHealResult.reason as any)?.message || t('lightningOps.chanHealStatusUnavailable')
      setChanHealStatus(message)
    }
  }

  useEffect(() => {
    load()
  }, [])

  useEffect(() => {
    let mounted = true
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
          setChanHeal(data as ChanHealStatus)
          if ((data as ChanHealStatus)?.interval_sec) {
            setChanHealInterval(String((data as ChanHealStatus).interval_sec))
          }
          setChanHealStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setChanHealStatus(err?.message || t('lightningOps.chanHealStatusUnavailable'))
        })
    }
    const timer = window.setInterval(() => {
      fetchAmboss()
      fetchChanHeal()
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
      setChanHealStatus(t('lightningOps.chanHealSaved'))
    } catch (err: any) {
      setChanHealStatus(err?.message || t('lightningOps.chanHealSaveFailed'))
    } finally {
      setChanHealBusy(false)
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

  const handleUpdateFees = async () => {
    setFeeStatus(t('lightningOps.updatingFees'))
    const base = Number(baseFeeMsat || 0)
    const ppm = Number(feeRatePpm || 0)
    const delta = Number(timeLockDelta || 0)
    const inboundBase = Number(inboundBaseMsat || 0)
    const inboundRate = Number(inboundFeeRatePpm || 0)
    if (!feeScopeAll && !feeChannelPoint) {
      setFeeStatus(t('lightningOps.selectChannelOrAll'))
      return
    }
    const hasOutbound = base !== 0 || ppm !== 0 || delta !== 0
    const hasInbound = inboundEnabled && (inboundBase !== 0 || inboundRate !== 0)
    if (!hasOutbound && !hasInbound) {
      setFeeStatus(t('lightningOps.setAtLeastOneFee'))
      return
    }
    try {
      const res = await updateChannelFees({
        apply_all: feeScopeAll,
        channel_point: feeScopeAll ? undefined : feeChannelPoint,
        base_fee_msat: base,
        fee_rate_ppm: ppm,
        time_lock_delta: delta,
        inbound_enabled: inboundEnabled,
        inbound_base_msat: inboundBase,
        inbound_fee_rate_ppm: inboundRate
      })
      setFeeStatus(res?.warning || t('lightningOps.feesUpdated'))
      load()
    } catch (err: any) {
      setFeeStatus(err?.message || t('lightningOps.feeUpdateFailed'))
    }
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
                              <div>{t('lightningOps.blocksToMaturity', { count: ch.blocks_til_maturity })}</div>
                            )}
                          </div>
                          {ch.closing_txid && (
                            <p className="mt-2 text-[11px] text-fog/50 break-all">{t('lightningOps.closingTx', { txid: ch.closing_txid })}</p>
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
            </div>
          </div>
        {filteredChannels.length ? (
          <div className="max-h-[520px] overflow-y-auto pr-2">
            <div className="grid gap-3">
              {filteredChannels.map((ch) => {
                const localDisabled = ch.local_disabled ?? isLocalChanDisabled(ch.chan_status_flags)
                const statusBusy = chanStatusBusy === ch.channel_point
                const showToggle = ch.active
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
                      </div>
                    </div>
                    <div className="mt-3 grid gap-3 lg:grid-cols-5 text-xs text-fog/70">
                      <div>{t('lightningOps.localLabel', { value: ch.local_balance_sat })}</div>
                      <div>{t('lightningOps.remoteLabel', { value: ch.remote_balance_sat })}</div>
                      <div>
                        {t('lightningOps.outRate')}:{' '}
                        <span className="text-fog">
                          {typeof ch.fee_rate_ppm === 'number' ? `${ch.fee_rate_ppm} ppm` : '-'}
                        </span>
                      </div>
                      <div>
                        {t('lightningOps.outBase')}:{' '}
                        <span className="text-fog">
                          {typeof ch.base_fee_msat === 'number' ? `${ch.base_fee_msat} msats` : '-'}
                        </span>
                      </div>
                      <div>
                        {t('lightningOps.inRate')}:{' '}
                        <span className="text-fog">
                          {typeof ch.inbound_fee_rate_ppm === 'number' ? `${ch.inbound_fee_rate_ppm} ppm` : '-'}
                        </span>
                      </div>
                    </div>
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
              onChange={(e) => setChanHealInterval(e.target.value)}
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
