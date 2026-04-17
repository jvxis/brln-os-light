import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getAmbossHealth,
  enableLoginAuth,
  getAppUpgradeStatus,
  getAutofeeStatus,
  getBitcoinActive,
  getCloseManagerStatus,
  getDisk,
  getHealth,
  getLndStatus,
  getLnChannels,
  getLnChanHeal,
  getLnFailedPaymentsCleaner,
  getLnHtlcManager,
  getLnPeers,
  getLnTorPeerChecker,
  getLogs,
  getNodeRetirementStatus,
  getNotifications,
  getPostgres,
  getRebalanceOverview,
  getReportsLive,
  getReportsMovementLive,
  getReportsRange,
  getReportsSummary,
  getSuccessionConfig,
  getSystem,
  restartService,
  runSystemAction,
  startAppUpgrade,
  type AuthState,
} from '../../api'
import { getLocale } from '../../i18n'
import AutomationRiskGrid from './AutomationRiskGrid'
import CoreHealthGrid from './CoreHealthGrid'
import NodePulseRow from './NodePulseRow'
import OperationsOverview from './OperationsOverview'
import RecentActivityCard from './RecentActivityCard'
import StatusBadge from './StatusBadge'
import { toneFromHealthStatus } from './formatters'
import type {
  AmbossHealthStatus,
  AutofeeStatus,
  ChanHealStatus,
  CloseRecoveryStatus,
  DiskSmart,
  FailedPaymentsCleanerStatus,
  HealthPayload,
  LndChannel,
  LndPeer,
  HtlcManagerStatus,
  LndStatus,
  LiveResponse,
  MovementLiveResponse,
  NodeRetirementStatus,
  NotificationItem,
  PostgresStatus,
  RebalanceOverview,
  ReportRangeResponse,
  SuccessionConfig,
  SummaryResponse,
  SystemStats,
  TorPeerCheckerStatus,
  BitcoinStatus,
} from './types'

type AppUpgradeStatus = {
  current_version?: string
  latest_version?: string
  latest_tag?: string
  latest_channel?: string
  checked_at?: string
  update_available?: boolean
  running?: boolean
  error?: string
}

type DashboardScreenProps = {
  authState?: AuthState | null
}

type LoadState = 'loading' | 'ok' | 'unavailable'

const LND_RESTART_POLL_INTERVAL_MS = 3000
const LND_RESTART_TIMEOUT_MS = 180000

export default function DashboardScreen({ authState }: DashboardScreenProps) {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)

  const [system, setSystem] = useState<SystemStats | null>(null)
  const [disk, setDisk] = useState<DiskSmart[]>([])
  const [bitcoin, setBitcoin] = useState<BitcoinStatus | null>(null)
  const [postgres, setPostgres] = useState<PostgresStatus | null>(null)
  const [lnd, setLnd] = useState<LndStatus | null>(null)
  const [lndPeers, setLndPeers] = useState<LndPeer[]>([])
  const [lndChannels, setLndChannels] = useState<LndChannel[]>([])
  const [health, setHealth] = useState<HealthPayload | null>(null)
  const [reportsLive, setReportsLive] = useState<LiveResponse | null>(null)
  const [reportsRange, setReportsRange] = useState<ReportRangeResponse | null>(null)
  const [reportsSummary, setReportsSummary] = useState<SummaryResponse | null>(null)
  const [movementLive, setMovementLive] = useState<MovementLiveResponse | null>(null)
  const [rebalanceOverview, setRebalanceOverview] = useState<RebalanceOverview | null>(null)
  const [autofeeStatus, setAutofeeStatus] = useState<AutofeeStatus | null>(null)
  const [ambossHealth, setAmbossHealth] = useState<AmbossHealthStatus | null>(null)
  const [chanHealStatus, setChanHealStatus] = useState<ChanHealStatus | null>(null)
  const [closeManagerStatus, setCloseManagerStatus] = useState<CloseRecoveryStatus | null>(null)
  const [htlcManagerStatus, setHtlcManagerStatus] = useState<HtlcManagerStatus | null>(null)
  const [torPeerCheckerStatus, setTorPeerCheckerStatus] = useState<TorPeerCheckerStatus | null>(null)
  const [failedPaymentsCleanerStatus, setFailedPaymentsCleanerStatus] = useState<FailedPaymentsCleanerStatus | null>(null)
  const [nodeRetirementStatus, setNodeRetirementStatus] = useState<NodeRetirementStatus | null>(null)
  const [successionConfig, setSuccessionConfig] = useState<SuccessionConfig | null>(null)
  const [notifications, setNotifications] = useState<NotificationItem[]>([])
  const [coreStatus, setCoreStatus] = useState<LoadState>('loading')
  const [pulseStatus, setPulseStatus] = useState<LoadState>('loading')
  const [automationStatus, setAutomationStatus] = useState<LoadState>('loading')
  const [activityStatus, setActivityStatus] = useState<LoadState>('loading')
  const [systemAction, setSystemAction] = useState<'restart' | 'shutdown' | null>(null)
  const [systemActionBusy, setSystemActionBusy] = useState(false)
  const [systemActionError, setSystemActionError] = useState<string | null>(null)
  const [appUpgrade, setAppUpgrade] = useState<AppUpgradeStatus | null>(null)
  const [appUpgradeChecking, setAppUpgradeChecking] = useState(false)
  const [appUpgradeMessage, setAppUpgradeMessage] = useState('')
  const [appUpgradeModalOpen, setAppUpgradeModalOpen] = useState(false)
  const [appUpgradeBusy, setAppUpgradeBusy] = useState(false)
  const [appUpgradeLogs, setAppUpgradeLogs] = useState<string[]>([])
  const [appUpgradeLogsStatus, setAppUpgradeLogsStatus] = useState('')
  const [appUpgradeError, setAppUpgradeError] = useState<string | null>(null)
  const [appUpgradeComplete, setAppUpgradeComplete] = useState(false)
  const [appUpgradeLocked, setAppUpgradeLocked] = useState(false)
  const [appUpgradeStartedVersion, setAppUpgradeStartedVersion] = useState('')
  const [appUpgradeLogSince, setAppUpgradeLogSince] = useState('')
  const [enableLoginModalOpen, setEnableLoginModalOpen] = useState(false)
  const [enableLoginBusy, setEnableLoginBusy] = useState(false)
  const [enableLoginMessage, setEnableLoginMessage] = useState('')
  const [enableLoginError, setEnableLoginError] = useState<string | null>(null)
  const [lndRestartModalOpen, setLndRestartModalOpen] = useState(false)
  const [lndRestartBusy, setLndRestartBusy] = useState(false)
  const [lndRestartRequested, setLndRestartRequested] = useState(false)
  const [lndRestartLocked, setLndRestartLocked] = useState(false)
  const [lndRestartLogs, setLndRestartLogs] = useState<string[]>([])
  const [lndRestartLogsStatus, setLndRestartLogsStatus] = useState('')
  const [lndRestartError, setLndRestartError] = useState<string | null>(null)
  const [lndRestartComplete, setLndRestartComplete] = useState(false)
  const [lndRestartMessage, setLndRestartMessage] = useState('')
  const [lndRestartSince, setLndRestartSince] = useState('')
  const [lndRestartStartedAt, setLndRestartStartedAt] = useState(0)
  const [lndRestartStatusSnapshot, setLndRestartStatusSnapshot] = useState<LndStatus | null>(null)
  const lndRestartLogContainerRef = useRef<HTMLDivElement | null>(null)

  const overallTone = health?.status
    ? toneFromHealthStatus(health.status)
    : coreStatus === 'ok'
      ? 'ok'
      : coreStatus === 'unavailable'
        ? 'warn'
        : 'muted'
  const overallStatusLabel = health?.status
    || (coreStatus === 'ok'
      ? t('common.ok')
      : coreStatus === 'unavailable'
        ? t('common.unavailable')
        : t('dashboard.loadingStatus'))
  const healthIssues = Array.isArray(health?.issues) ? health.issues : []
  const topSummary = health
    ? (healthIssues.length > 0
      ? healthIssues
        .map((issue) => {
          const label = issue.component ? String(issue.component).toUpperCase() : t('dashboard.systemLabel')
          return `${label}: ${issue.message || t('dashboard.issueDetected')}`
        })
        .join(' | ')
      : t('topbar.allSystemsGreen'))
    : pulseStatus === 'unavailable'
      ? t('dashboard.partialDataHint')
      : t('dashboard.loadingStatus')
  const systemActionIsShutdown = systemAction === 'shutdown'
  const systemActionTitle = systemActionIsShutdown
    ? t('dashboard.confirmShutdownTitle')
    : t('dashboard.confirmRestartTitle')
  const systemActionBody = systemActionIsShutdown
    ? t('dashboard.confirmShutdownBody')
    : t('dashboard.confirmRestartBody')
  const systemActionButtonClass = systemActionIsShutdown
    ? 'text-rose-200 border-rose-400/30'
    : 'text-amber-200 border-amber-400/30'
  const lndRestartRpcReady = Boolean(
    lndRestartStatusSnapshot?.service_active
    && lndRestartStatusSnapshot?.wallet_state === 'unlocked'
    && lndRestartStatusSnapshot?.info_known
    && !lndRestartStatusSnapshot?.info_stale
  )
  const lndRestartCanClose = !lndRestartBusy && (!lndRestartLocked || Boolean(lndRestartError) || lndRestartComplete)

  const formatVersion = (value?: string) => {
    if (!value) return t('common.na')
    return value.startsWith('v') ? value : `v${value}`
  }

  const formatCheckedAt = (value?: string) => {
    if (!value) return ''
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return ''
    return parsed.toLocaleString(locale, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    })
  }

  useEffect(() => {
    let mounted = true
    const load = async () => {
      const [systemRes, diskRes, bitcoinRes, postgresRes, lndRes, lndPeersRes, lndChannelsRes] = await Promise.allSettled([
        getSystem(),
        getDisk(),
        getBitcoinActive(),
        getPostgres(),
        getLndStatus(),
        getLnPeers(),
        getLnChannels(),
      ])
      if (!mounted) return

      let fulfilled = 0
      if (systemRes.status === 'fulfilled') {
        setSystem((systemRes.value ?? null) as SystemStats | null)
        fulfilled += 1
      }
      if (diskRes.status === 'fulfilled') {
        setDisk(Array.isArray(diskRes.value) ? diskRes.value as DiskSmart[] : [])
        fulfilled += 1
      }
      if (bitcoinRes.status === 'fulfilled') {
        setBitcoin((bitcoinRes.value ?? null) as BitcoinStatus | null)
        fulfilled += 1
      }
      if (postgresRes.status === 'fulfilled') {
        setPostgres((postgresRes.value ?? null) as PostgresStatus | null)
        fulfilled += 1
      }
      if (lndRes.status === 'fulfilled') {
        setLnd((lndRes.value ?? null) as LndStatus | null)
        fulfilled += 1
      }
      if (lndPeersRes.status === 'fulfilled') {
        const peersPayload = lndPeersRes.value as { peers?: LndPeer[] } | null
        setLndPeers(Array.isArray(peersPayload?.peers) ? peersPayload.peers : [])
        fulfilled += 1
      }
      if (lndChannelsRes.status === 'fulfilled') {
        const channelsPayload = lndChannelsRes.value as { channels?: LndChannel[] } | null
        setLndChannels(Array.isArray(channelsPayload?.channels) ? channelsPayload.channels : [])
        fulfilled += 1
      }
      setCoreStatus(fulfilled > 0 ? 'ok' : 'unavailable')
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
    const load = async () => {
      const [healthRes, liveRes, rangeRes, summaryRes, movementRes] = await Promise.allSettled([
        getHealth(),
        getReportsLive(),
        getReportsRange('month'),
        getReportsSummary('month'),
        getReportsMovementLive(),
      ])
      if (!mounted) return

      let fulfilled = 0
      if (healthRes.status === 'fulfilled') {
        setHealth((healthRes.value ?? null) as HealthPayload | null)
        fulfilled += 1
      }
      if (liveRes.status === 'fulfilled') {
        setReportsLive((liveRes.value ?? null) as LiveResponse | null)
        fulfilled += 1
      }
      if (rangeRes.status === 'fulfilled') {
        setReportsRange((rangeRes.value ?? null) as ReportRangeResponse | null)
        fulfilled += 1
      }
      if (summaryRes.status === 'fulfilled') {
        setReportsSummary((summaryRes.value ?? null) as SummaryResponse | null)
        fulfilled += 1
      }
      if (movementRes.status === 'fulfilled') {
        setMovementLive((movementRes.value ?? null) as MovementLiveResponse | null)
        fulfilled += 1
      }
      setPulseStatus(fulfilled > 0 ? 'ok' : 'unavailable')
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
    const load = async () => {
      const [rebalanceRes, autofeeRes, ambossRes, chanHealRes, closeManagerRes, htlcRes, torRes, failedRes, retirementRes, successionRes] = await Promise.allSettled([
        getRebalanceOverview(),
        getAutofeeStatus(),
        getAmbossHealth(),
        getLnChanHeal(),
        getCloseManagerStatus(),
        getLnHtlcManager(),
        getLnTorPeerChecker(),
        getLnFailedPaymentsCleaner(),
        getNodeRetirementStatus(),
        getSuccessionConfig(),
      ])
      if (!mounted) return

      let fulfilled = 0
      if (rebalanceRes.status === 'fulfilled') {
        setRebalanceOverview((rebalanceRes.value ?? null) as RebalanceOverview | null)
        fulfilled += 1
      }
      if (autofeeRes.status === 'fulfilled') {
        setAutofeeStatus((autofeeRes.value ?? null) as AutofeeStatus | null)
        fulfilled += 1
      }
      if (ambossRes.status === 'fulfilled') {
        setAmbossHealth((ambossRes.value ?? null) as AmbossHealthStatus | null)
        fulfilled += 1
      }
      if (chanHealRes.status === 'fulfilled') {
        setChanHealStatus((chanHealRes.value ?? null) as ChanHealStatus | null)
        fulfilled += 1
      }
      if (closeManagerRes.status === 'fulfilled') {
        setCloseManagerStatus((closeManagerRes.value ?? null) as CloseRecoveryStatus | null)
        fulfilled += 1
      }
      if (htlcRes.status === 'fulfilled') {
        setHtlcManagerStatus((htlcRes.value ?? null) as HtlcManagerStatus | null)
        fulfilled += 1
      }
      if (torRes.status === 'fulfilled') {
        setTorPeerCheckerStatus((torRes.value ?? null) as TorPeerCheckerStatus | null)
        fulfilled += 1
      }
      if (failedRes.status === 'fulfilled') {
        setFailedPaymentsCleanerStatus((failedRes.value ?? null) as FailedPaymentsCleanerStatus | null)
        fulfilled += 1
      }
      if (retirementRes.status === 'fulfilled') {
        setNodeRetirementStatus((retirementRes.value ?? null) as NodeRetirementStatus | null)
        fulfilled += 1
      }
      if (successionRes.status === 'fulfilled') {
        setSuccessionConfig((successionRes.value ?? null) as SuccessionConfig | null)
        fulfilled += 1
      }
      setAutomationStatus(fulfilled > 0 ? 'ok' : 'unavailable')
    }

    load()
    const timer = setInterval(load, 60000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    let mounted = true
    const load = async () => {
      try {
        const next = await getNotifications(12) as { items?: NotificationItem[] } | NotificationItem[]
        if (!mounted) return
        const items = Array.isArray(next)
          ? next as NotificationItem[]
          : Array.isArray(next?.items)
            ? next.items
            : []
        setNotifications(items)
        setActivityStatus('ok')
      } catch {
        if (!mounted) return
        setActivityStatus('unavailable')
      }
    }

    load()
    const timer = setInterval(load, 60000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  const loadAppUpgradeStatus = async (force = false, silent = false) => {
    if (!force && appUpgradeChecking) return
    if (!silent) {
      setAppUpgradeChecking(true)
      setAppUpgradeMessage(t('appUpgrade.checking'))
    }
    try {
      const data = await getAppUpgradeStatus(force)
      setAppUpgrade(data as AppUpgradeStatus)
      if (!silent) {
        setAppUpgradeMessage('')
      }
    } catch (err) {
      if (!silent) {
        setAppUpgradeMessage(err instanceof Error ? err.message : t('appUpgrade.statusFailed'))
      }
    } finally {
      if (!silent) {
        setAppUpgradeChecking(false)
      }
    }
  }

  useEffect(() => {
    let mounted = true
    const load = async () => {
      try {
        const data = await getAppUpgradeStatus()
        if (!mounted) return
        setAppUpgrade(data as AppUpgradeStatus)
      } catch {
        if (!mounted) return
      }
    }
    load()
    const timer = setInterval(load, 60000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    if (!lndRestartModalOpen) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previousOverflow
    }
  }, [lndRestartModalOpen])

  useEffect(() => {
    if (!lndRestartModalOpen) return
    const container = lndRestartLogContainerRef.current
    if (!container) return
    container.scrollTop = container.scrollHeight
  }, [lndRestartLogs, lndRestartModalOpen])

  const closeLndRestartModal = () => {
    if (!lndRestartCanClose) return
    setLndRestartModalOpen(false)
  }

  const startLndRestartFlow = async () => {
    if (lndRestartBusy || lndRestartLocked) return

    const sinceNow = new Date(Date.now() - 15000).toISOString()
    setLndRestartModalOpen(true)
    setLndRestartBusy(true)
    setLndRestartRequested(false)
    setLndRestartLocked(true)
    setLndRestartLogs([])
    setLndRestartLogsStatus('')
    setLndRestartError(null)
    setLndRestartComplete(false)
    setLndRestartMessage(t('dashboard.lndRestartStarting'))
    setLndRestartSince(sinceNow)
    setLndRestartStartedAt(Date.now())
    setLndRestartStatusSnapshot(null)

    try {
      await restartService({ service: 'lnd' })
      setLndRestartRequested(true)
      setLndRestartMessage(t('dashboard.lndRestartWaitingService'))
    } catch (err) {
      setLndRestartMessage('')
      setLndRestartError(err instanceof Error ? err.message : t('dashboard.lndRestartFailed'))
      setLndRestartLocked(false)
    } finally {
      setLndRestartBusy(false)
    }
  }

  const restart = async (service: string) => {
    try {
      if (service === 'lnd') {
        await startLndRestartFlow()
        return
      }
      await restartService({ service })
    } catch (err) {
      console.error(`Failed to restart ${service}`, err)
    }
  }

  const openSystemAction = (action: 'restart' | 'shutdown') => {
    setSystemAction(action)
    setSystemActionError(null)
  }

  const closeSystemAction = () => {
    if (systemActionBusy) return
    setSystemAction(null)
    setSystemActionError(null)
  }

  const confirmSystemAction = async () => {
    if (!systemAction) return
    setSystemActionBusy(true)
    setSystemActionError(null)
    try {
      await runSystemAction({ action: systemAction === 'restart' ? 'reboot' : 'shutdown' })
      setSystemAction(null)
    } catch (err) {
      setSystemActionError(err instanceof Error ? err.message : t('common.fail'))
    } finally {
      setSystemActionBusy(false)
    }
  }

  const openAppUpgradeModal = () => {
    setAppUpgradeModalOpen(true)
    setAppUpgradeLogs([])
    setAppUpgradeLogsStatus('')
    setAppUpgradeError(null)
    setAppUpgradeComplete(false)
    setAppUpgradeMessage('')
    setAppUpgradeLocked(Boolean(appUpgrade?.running))
    setAppUpgradeStartedVersion('')
    setAppUpgradeLogSince(appUpgrade?.running ? '' : new Date().toISOString())
  }

  const closeAppUpgradeModal = () => {
    if (appUpgradeBusy || (appUpgradeLocked && !appUpgradeError && !appUpgradeComplete)) return
    setAppUpgradeModalOpen(false)
  }

  const startAppUpgradeFlow = async () => {
    if (!appUpgrade?.latest_version || appUpgradeBusy) return
    const sinceNow = new Date(Date.now() - 15000).toISOString()
    setAppUpgradeStartedVersion(appUpgrade.latest_version)
    setAppUpgradeLogSince(sinceNow)
    setAppUpgradeBusy(true)
    setAppUpgradeError(null)
    setAppUpgradeComplete(false)
    setAppUpgradeMessage(t('appUpgrade.starting'))
    try {
      await startAppUpgrade({ target_version: appUpgrade.latest_version })
      setAppUpgradeMessage(t('appUpgrade.started'))
      setAppUpgradeModalOpen(true)
      setAppUpgradeLocked(true)
    } catch (err) {
      const message = err instanceof Error ? err.message : t('appUpgrade.startFailed')
      setAppUpgradeMessage(message)
      setAppUpgradeError(message)
      setAppUpgradeLocked(false)
    } finally {
      setAppUpgradeBusy(false)
      loadAppUpgradeStatus(true, true)
    }
  }

  useEffect(() => {
    if (!lndRestartModalOpen || lndRestartComplete || lndRestartError || !lndRestartStartedAt) return
    let mounted = true

    const loadLogs = async () => {
      setLndRestartLogsStatus(t('dashboard.lndRestartLogsLoading'))
      try {
        const res = await getLogs('lnd', 200, lndRestartSince || undefined)
        if (!mounted) return
        const lines: string[] = Array.isArray(res?.lines) ? res.lines : []
        setLndRestartLogs(lines)
        setLndRestartLogsStatus('')
      } catch (err) {
        if (!mounted) return
        setLndRestartLogsStatus(err instanceof Error ? err.message : t('logs.fetchFailed'))
      }
    }

    const refreshStatus = async () => {
      try {
        const nextStatus = await getLndStatus() as LndStatus
        if (!mounted) return

        setLndRestartStatusSnapshot(nextStatus)
        setLnd(nextStatus)

        const timedOut = Date.now() - lndRestartStartedAt >= LND_RESTART_TIMEOUT_MS
        if (!lndRestartRequested) {
          return
        }

        const rpcReady = Boolean(
          nextStatus?.service_active
          && nextStatus?.wallet_state === 'unlocked'
          && nextStatus?.info_known
          && !nextStatus?.info_stale
        )

        if (rpcReady) {
          setLndRestartComplete(true)
          setLndRestartLocked(false)
          setLndRestartMessage(t('dashboard.lndRestartCompleted'))

          const [peersRes, channelsRes] = await Promise.allSettled([getLnPeers(), getLnChannels()])
          if (!mounted) return

          if (peersRes.status === 'fulfilled') {
            const peersPayload = peersRes.value as { peers?: LndPeer[] } | null
            setLndPeers(Array.isArray(peersPayload?.peers) ? peersPayload.peers : [])
          }
          if (channelsRes.status === 'fulfilled') {
            const channelsPayload = channelsRes.value as { channels?: LndChannel[] } | null
            setLndChannels(Array.isArray(channelsPayload?.channels) ? channelsPayload.channels : [])
          }
          return
        }

        if (timedOut) {
          setLndRestartError(t('dashboard.lndRestartTimeout'))
          setLndRestartLocked(false)
          return
        }

        if (!nextStatus?.service_active) {
          setLndRestartMessage(t('dashboard.lndRestartWaitingService'))
          return
        }

        if (nextStatus?.wallet_state === 'locked') {
          setLndRestartMessage(t('dashboard.lndRestartWaitingWallet'))
          return
        }

        setLndRestartMessage(t('dashboard.lndRestartWaitingRpc'))
      } catch (err) {
        if (!mounted) return
        if (lndRestartRequested && Date.now() - lndRestartStartedAt >= LND_RESTART_TIMEOUT_MS) {
          setLndRestartError(t('dashboard.lndRestartTimeout'))
          setLndRestartLocked(false)
          return
        }
        setLndRestartStatusSnapshot(null)
        setLndRestartMessage(t('dashboard.lndRestartWaitingService'))
        if (err instanceof Error) {
          setLndRestartLogsStatus(err.message)
        }
      }
    }

    void loadLogs()
    void refreshStatus()
    const timer = window.setInterval(() => {
      void loadLogs()
      void refreshStatus()
    }, LND_RESTART_POLL_INTERVAL_MS)

    return () => {
      mounted = false
      window.clearInterval(timer)
    }
  }, [
    lndRestartComplete,
    lndRestartError,
    lndRestartModalOpen,
    lndRestartRequested,
    lndRestartSince,
    lndRestartStartedAt,
    t,
  ])

  useEffect(() => {
    if (!appUpgradeModalOpen) return
    let mounted = true
    const loadLogs = async () => {
      setAppUpgradeLogsStatus(t('appUpgrade.loadingLogs'))
      try {
        const res = await getLogs('app-upgrade', 200, appUpgradeLogSince || undefined)
        if (!mounted) return
        const lines: string[] = Array.isArray(res?.lines) ? res.lines : []
        setAppUpgradeLogs(lines)
        const completed = lines.some((line) => line.includes('App upgrade complete'))
        const errorLine = [...lines].reverse().find((line) =>
          line.includes('[ERROR]') || line.toLowerCase().includes('failed')
        )
        if (completed) {
          setAppUpgradeComplete(true)
          setAppUpgradeLocked(false)
        }
        if (errorLine) {
          setAppUpgradeError(errorLine)
          setAppUpgradeLocked(false)
        }
        setAppUpgradeLogsStatus('')
      } catch (err) {
        if (!mounted) return
        setAppUpgradeLogsStatus(err instanceof Error ? err.message : t('appUpgrade.logFetchFailed'))
      }
    }
    const refreshStatus = async () => {
      try {
        const data = await getAppUpgradeStatus()
        if (!mounted) return
        const next = data as AppUpgradeStatus
        setAppUpgrade(next)
        if (next.running) {
          setAppUpgradeLocked(true)
        } else if (appUpgradeLocked) {
          setAppUpgradeLocked(false)
        }
        if (appUpgradeStartedVersion && !next.running && !appUpgradeError && !next.update_available) {
          setAppUpgradeComplete(true)
        }
      } catch {
        // ignore status refresh errors while modal is open
      }
    }

    loadLogs()
    refreshStatus()
    const timer = setInterval(() => {
      loadLogs()
      refreshStatus()
    }, 4000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [appUpgradeError, appUpgradeLocked, appUpgradeLogSince, appUpgradeModalOpen, appUpgradeStartedVersion, t])

  const showConfirmAppUpgrade = Boolean(appUpgrade?.update_available) && !appUpgradeComplete

  const openEnableLoginModal = () => {
    setEnableLoginModalOpen(true)
    setEnableLoginMessage('')
    setEnableLoginError(null)
  }

  const closeEnableLoginModal = () => {
    if (enableLoginBusy) return
    setEnableLoginModalOpen(false)
    setEnableLoginError(null)
  }

  const confirmEnableLogin = async () => {
    if (enableLoginBusy) return
    setEnableLoginBusy(true)
    setEnableLoginError(null)
    setEnableLoginMessage(t('auth.legacyEnableStarting'))
    try {
      await enableLoginAuth()
      setEnableLoginMessage(t('auth.legacyEnableRestarting'))
      window.setTimeout(() => {
        window.location.reload()
      }, 2500)
    } catch (err) {
      setEnableLoginError(err instanceof Error ? err.message : t('auth.legacyEnableFailed'))
      setEnableLoginMessage('')
    } finally {
      setEnableLoginBusy(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
          <div className="max-w-4xl">
            <p className="text-sm text-fog/60">{t('dashboard.systemPulse')}</p>
            <div className="mt-2 flex flex-wrap items-center gap-3">
              <h2 className="text-2xl font-semibold">{t('dashboard.nodePulseTitle')}</h2>
              <StatusBadge label={overallStatusLabel} tone={overallTone} size="md" />
            </div>
            <p className="mt-3 text-sm text-fog/65">{topSummary}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2 xl:justify-end">
            <button
              className={`btn-secondary text-xs px-3 py-2 sm:text-sm sm:px-4 ${(lndRestartBusy || lndRestartLocked) ? 'opacity-60 pointer-events-none' : ''}`}
              onClick={() => void restart('lnd')}
              type="button"
              disabled={lndRestartBusy || lndRestartLocked}
            >
              {t('dashboard.restartLnd')}
            </button>
            <button className="btn-secondary text-xs px-3 py-2 sm:text-sm sm:px-4" onClick={() => void restart('lightningos-manager')} type="button">
              {t('dashboard.restartManager')}
            </button>
            <div className="flex flex-wrap items-center gap-2 rounded-2xl border border-white/10 bg-white/5 px-2 py-1 w-full xl:w-auto">
              <span className="text-[10px] uppercase tracking-[0.2em] text-fog/50">{t('dashboard.systemActions')}</span>
              <button
                className="btn-secondary text-[11px] px-2 py-1 sm:text-xs sm:px-3 sm:py-1.5 text-amber-200 border-amber-400/30"
                onClick={() => openSystemAction('restart')}
                type="button"
              >
                {t('dashboard.safeRestart')}
              </button>
              <button
                className="btn-secondary text-[11px] px-2 py-1 sm:text-xs sm:px-3 sm:py-1.5 text-rose-200 border-rose-400/30"
                onClick={() => openSystemAction('shutdown')}
                type="button"
              >
                {t('dashboard.safeShutdown')}
              </button>
            </div>
          </div>
        </div>
      </div>

      {authState?.enabled === false && (
        <div className="section-card border-amber-400/30 bg-amber-500/10">
          <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
            <div className="max-w-3xl">
              <p className="text-xs uppercase tracking-[0.3em] text-amber-200">{t('auth.legacyEnableKicker')}</p>
              <h3 className="mt-3 text-xl font-semibold">{t('auth.legacyEnableTitle')}</h3>
              <p className="mt-3 text-sm text-fog/75">{t('auth.legacyEnableBody')}</p>
              <p className="mt-2 text-xs text-fog/55">{t('auth.legacyEnableHint')}</p>
            </div>
            <div className="flex shrink-0 items-center gap-3">
              <button className="btn-secondary text-amber-200 border-amber-400/30" type="button" onClick={openEnableLoginModal}>
                {t('auth.legacyEnableAction')}
              </button>
            </div>
          </div>
        </div>
      )}

      <NodePulseRow
        live={reportsLive}
        range={reportsRange}
        summary={reportsSummary}
        movement={movementLive}
        lnd={lnd}
      />

      <CoreHealthGrid
        lnd={lnd}
        lndPeers={lndPeers}
        lndChannels={lndChannels}
        bitcoin={bitcoin}
        postgres={postgres}
        system={system}
        disks={disk}
      />

      <OperationsOverview
        live={reportsLive}
        range={reportsRange}
        summary={reportsSummary}
      />

      <div className="grid gap-6 xl:grid-cols-2">
        <AutomationRiskGrid
          rebalance={rebalanceOverview}
          autofee={autofeeStatus}
          amboss={ambossHealth}
          chanHeal={chanHealStatus}
          closeManager={closeManagerStatus}
          htlcManager={htlcManagerStatus}
          torPeerChecker={torPeerCheckerStatus}
          failedPaymentsCleaner={failedPaymentsCleanerStatus}
          nodeRetirement={nodeRetirementStatus}
          successionConfig={successionConfig}
        />
        <RecentActivityCard notifications={notifications} />
      </div>

      <div className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h3 className="text-lg font-semibold">{t('appUpgrade.title')}</h3>
            <p className="text-fog/60">{t('appUpgrade.subtitle')}</p>
          </div>
          <button
            className="btn-secondary"
            onClick={() => loadAppUpgradeStatus(true)}
            disabled={appUpgradeChecking}
          >
            {appUpgradeChecking ? t('appUpgrade.checking') : t('common.refresh')}
          </button>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 text-sm">
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('appUpgrade.current')}</span>
            <span className="font-mono text-fog">{formatVersion(appUpgrade?.current_version)}</span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('appUpgrade.latest')}</span>
            <span className="font-mono text-fog">{formatVersion(appUpgrade?.latest_version)}</span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('appUpgrade.channel')}</span>
            <span className="text-fog/90">
              {appUpgrade?.latest_channel
                ? t(`appUpgrade.channels.${appUpgrade.latest_channel}`)
                : t('common.na')}
            </span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('appUpgrade.checkedAt')}</span>
            <span className="text-fog/90">{formatCheckedAt(appUpgrade?.checked_at) || t('common.na')}</span>
          </div>
        </div>

        {appUpgrade?.error && (
          <p className="text-sm text-rose-200">{t('appUpgrade.statusError', { error: appUpgrade.error })}</p>
        )}

        {!appUpgrade?.error && (
          <p className="text-sm text-fog/70">
            {appUpgrade?.running
              ? t('appUpgrade.inProgress')
              : appUpgrade?.update_available
                ? t('appUpgrade.updateAvailable')
                : t('appUpgrade.upToDate')}
          </p>
        )}

        {appUpgradeMessage && <p className="text-sm text-brass">{appUpgradeMessage}</p>}

        <div className="flex flex-wrap items-center gap-3">
          <button
            className="btn-primary"
            onClick={openAppUpgradeModal}
            disabled={!appUpgrade?.update_available && !appUpgrade?.running}
          >
            {appUpgrade?.running ? t('appUpgrade.viewLogs') : t('appUpgrade.upgrade')}
          </button>
        </div>

        <p className="text-xs text-fog/50">{t('appUpgrade.warning')}</p>
      </div>

      {systemAction && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={closeSystemAction} aria-hidden="true" />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="system-action-title"
            className="relative z-10 w-full max-w-md rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <h4 id="system-action-title" className="text-lg font-semibold">{systemActionTitle}</h4>
            <p className="mt-2 text-sm text-fog/70">{systemActionBody}</p>
            {systemActionError && (
              <p className="mt-3 text-sm text-rose-200">{systemActionError}</p>
            )}
            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${systemActionBusy ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={closeSystemAction}
                type="button"
                autoFocus
              >
                {t('common.cancel')}
              </button>
              <button
                className={`btn-secondary ${systemActionButtonClass} ${systemActionBusy ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={confirmSystemAction}
                type="button"
              >
                {t('common.ok')}
              </button>
            </div>
          </div>
        </div>
      )}

      {enableLoginModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={closeEnableLoginModal} aria-hidden="true" />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="enable-login-title"
            className="relative z-10 w-full max-w-xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <h4 id="enable-login-title" className="text-lg font-semibold">{t('auth.legacyEnableConfirmTitle')}</h4>
            <p className="mt-2 text-sm text-fog/70">{t('auth.legacyEnableConfirmBody')}</p>
            <p className="mt-3 text-xs text-amber-200">{t('auth.legacyEnableConfirmHint')}</p>
            {enableLoginMessage && <p className="mt-4 text-sm text-brass">{enableLoginMessage}</p>}
            {enableLoginError && <p className="mt-3 text-sm text-rose-200">{enableLoginError}</p>}
            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${enableLoginBusy ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={closeEnableLoginModal}
                type="button"
              >
                {t('common.cancel')}
              </button>
              <button
                className={`btn-secondary text-amber-200 border-amber-400/30 ${enableLoginBusy ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={confirmEnableLogin}
                type="button"
              >
                {enableLoginBusy ? t('auth.legacyEnableBusy') : t('auth.legacyEnableAction')}
              </button>
            </div>
          </div>
        </div>
      )}

      {lndRestartModalOpen && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center px-4">
          <div
            className="absolute inset-0 bg-black/70 backdrop-blur-sm"
            onClick={closeLndRestartModal}
            aria-hidden="true"
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="lnd-restart-title"
            className="relative z-10 w-full max-w-3xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h4 id="lnd-restart-title" className="text-lg font-semibold">{t('dashboard.lndRestartTitle')}</h4>
                <p className="mt-2 text-sm text-fog/70">{t('dashboard.lndRestartBody')}</p>
              </div>
              <StatusBadge
                label={lndRestartComplete ? t('common.ok') : lndRestartRpcReady ? t('common.ok') : t('dashboard.runningLabel')}
                tone={lndRestartComplete ? 'ok' : lndRestartRpcReady ? 'ok' : 'warn'}
              />
            </div>

            {lndRestartMessage && <p className="mt-4 text-sm text-brass">{lndRestartMessage}</p>}
            {lndRestartError && <p className="mt-2 text-sm text-rose-200">{lndRestartError}</p>}
            {lndRestartComplete && !lndRestartError && (
              <p className="mt-2 text-sm text-emerald-200">{t('dashboard.lndRestartCompletedHint')}</p>
            )}

            <div className="mt-4">
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm text-fog/70">{t('logs.services.lnd')}</span>
                <span className="text-xs text-fog/50">
                  {lndRestartComplete
                    ? t('dashboard.lndRestartCompleted')
                    : t('dashboard.lndRestartLogsHint')}
                </span>
              </div>
              {lndRestartLogsStatus && <p className="mt-2 text-xs text-brass">{lndRestartLogsStatus}</p>}
              <div
                ref={lndRestartLogContainerRef}
                className="mt-2 max-h-[320px] overflow-y-auto rounded-2xl border border-white/10 bg-ink/70 p-3 text-xs font-mono whitespace-pre-wrap"
              >
                {lndRestartLogs.length ? lndRestartLogs.join('\n') : t('logs.noLogs')}
              </div>
            </div>

            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${lndRestartCanClose ? '' : 'opacity-60 pointer-events-none'}`}
                onClick={closeLndRestartModal}
                type="button"
                disabled={!lndRestartCanClose}
              >
                {lndRestartComplete || lndRestartError ? t('common.close') : t('common.cancel')}
              </button>
            </div>
          </div>
        </div>
      )}

      {appUpgradeModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div
            className="absolute inset-0 bg-black/60 backdrop-blur-sm"
            onClick={closeAppUpgradeModal}
            aria-hidden="true"
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="app-upgrade-title"
            className="relative z-10 w-full max-w-3xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <h4 id="app-upgrade-title" className="text-lg font-semibold">{t('appUpgrade.confirmTitle')}</h4>
            <p className="mt-2 text-sm text-fog/70">
              {t('appUpgrade.confirmBody', { version: formatVersion(appUpgrade?.latest_version) })}
            </p>
            <p className="mt-3 text-xs text-rose-200">{t('appUpgrade.confirmWarning')}</p>
            {appUpgradeMessage && <p className="mt-3 text-sm text-brass">{appUpgradeMessage}</p>}
            {appUpgradeError && <p className="mt-2 text-sm text-rose-200">{appUpgradeError}</p>}
            {appUpgradeComplete && !appUpgradeError && (
              <p className="mt-2 text-sm text-emerald-200">{t('appUpgrade.completed')}</p>
            )}

            <div className="mt-4">
              <div className="flex items-center justify-between">
                <span className="text-sm text-fog/70">{t('appUpgrade.logsTitle')}</span>
                <span className="text-xs text-fog/50">
                  {appUpgrade?.running ? t('appUpgrade.inProgress') : t('appUpgrade.logsHint')}
                </span>
              </div>
              {appUpgradeLogsStatus && <p className="mt-2 text-xs text-brass">{appUpgradeLogsStatus}</p>}
              <div className="mt-2 max-h-[320px] overflow-y-auto rounded-2xl border border-white/10 bg-ink/70 p-3 text-xs font-mono whitespace-pre-wrap">
                {appUpgradeLogs.length ? appUpgradeLogs.join('\n') : t('appUpgrade.noLogs')}
              </div>
            </div>

            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${(appUpgradeBusy || (appUpgradeLocked && !appUpgradeError && !appUpgradeComplete)) ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={closeAppUpgradeModal}
                type="button"
                disabled={appUpgradeBusy || (appUpgradeLocked && !appUpgradeError && !appUpgradeComplete)}
              >
                {appUpgradeComplete || appUpgradeError ? t('common.close') : t('common.cancel')}
              </button>
              {showConfirmAppUpgrade && (
                <button
                  className={`btn-secondary text-amber-200 border-amber-400/30 ${appUpgradeBusy ? 'opacity-60 pointer-events-none' : ''}`}
                  onClick={startAppUpgradeFlow}
                  type="button"
                  disabled={!appUpgrade?.update_available || appUpgrade?.running}
                >
                  {appUpgradeBusy ? t('appUpgrade.upgrading') : t('appUpgrade.confirmUpgrade')}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
