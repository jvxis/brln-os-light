import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getBitcoinLocalStatus, getBitcoinSource, getLndConfig, getLndStatus, getLndUpgradeStatus, getLogs, setBitcoinSource, startLndUpgrade, updateLndConfig, updateLndRawConfig } from '../api'

type BitcoinLocalStatus = {
  installed?: boolean
  status?: string
  rpc_ok?: boolean
  blocks?: number
  headers?: number
  verification_progress?: number
  initial_block_download?: boolean
}

const LND_ALIAS_MAX_BYTES = 32
type LndNetworkMode = 'private' | 'hybrid' | 'custom'

type LndConfigProps = {
  externalBitcoinDetected?: boolean
}

export default function LndConfig({ externalBitcoinDetected = false }: LndConfigProps) {
  const { t } = useTranslation()
  const [config, setConfig] = useState<any>(null)
  const [alias, setAlias] = useState('')
  const [color, setColor] = useState('#ff9900')
  const [colorInput, setColorInput] = useState('#ff9900')
  const [minChan, setMinChan] = useState('')
  const [maxChan, setMaxChan] = useState('')
  const [networkMode, setNetworkMode] = useState<LndNetworkMode>('custom')
  const [graphSyncPeers, setGraphSyncPeers] = useState('5')
  const [disconnectUnresponsivePeers, setDisconnectUnresponsivePeers] = useState(true)
  const [syncedToGraph, setSyncedToGraph] = useState<boolean | null>(null)
  const [networkBusy, setNetworkBusy] = useState(false)
  const [raw, setRaw] = useState('')
  const [advanced, setAdvanced] = useState(false)
  const [status, setStatus] = useState('')
  const [bitcoinSource, setBitcoinSourceState] = useState<'remote' | 'local'>('remote')
  const [bitcoinLocalStatus, setBitcoinLocalStatus] = useState<BitcoinLocalStatus | null>(null)
  const [sourceBusy, setSourceBusy] = useState(false)
  const [upgrade, setUpgrade] = useState<any>(null)
  const [upgradeMessage, setUpgradeMessage] = useState('')
  const [upgradeChecking, setUpgradeChecking] = useState(false)
  const [upgradeModalOpen, setUpgradeModalOpen] = useState(false)
  const [upgradeBusy, setUpgradeBusy] = useState(false)
  const [upgradeLogs, setUpgradeLogs] = useState<string[]>([])
  const [upgradeLogsStatus, setUpgradeLogsStatus] = useState('')
  const [upgradeError, setUpgradeError] = useState<string | null>(null)
  const [upgradeComplete, setUpgradeComplete] = useState(false)
  const [upgradeLocked, setUpgradeLocked] = useState(false)
  const [upgradeRcConfirm, setUpgradeRcConfirm] = useState(false)
  const [upgradeStartedVersion, setUpgradeStartedVersion] = useState('')
  const [upgradeLogSince, setUpgradeLogSince] = useState('')
  const aliasByteLength = useMemo(() => new TextEncoder().encode(alias).length, [alias])
  const aliasTooLong = aliasByteLength > LND_ALIAS_MAX_BYTES

  const findLastMatchIndex = (lines: string[], pattern: string) => {
    for (let i = lines.length - 1; i >= 0; i -= 1) {
      if (lines[i].includes(pattern)) {
        return i
      }
    }
    return -1
  }

  const findLastPredicateIndex = (lines: string[], predicate: (line: string) => boolean) => {
    for (let i = lines.length - 1; i >= 0; i -= 1) {
      if (predicate(lines[i])) {
        return i
      }
    }
    return -1
  }

  const loadLocalStatus = () => {
    getBitcoinLocalStatus()
      .then((data: BitcoinLocalStatus) => {
        setBitcoinLocalStatus(data)
      })
      .catch(() => {
        setBitcoinLocalStatus(null)
      })
  }

  useEffect(() => {
    getLndConfig().then((data: any) => {
      setConfig(data)
      setAlias(data.current.alias || '')
      const nextColor = data.current.color || '#ff9900'
      setColor(nextColor)
      setColorInput(nextColor)
      const minVal = Number(data.current.min_channel_size_sat || 0)
      const maxVal = Number(data.current.max_channel_size_sat || 0)
      setMinChan(minVal > 0 ? minVal.toString() : '')
      setMaxChan(maxVal > 0 ? maxVal.toString() : '')
      const nextMode = data.current.network_mode
      setNetworkMode(nextMode === 'private' || nextMode === 'hybrid' ? nextMode : 'custom')
      const nextGraphSyncPeers = Number(data.current.graph_sync_peers || 0)
      setGraphSyncPeers(nextGraphSyncPeers > 0 ? nextGraphSyncPeers.toString() : '5')
      setDisconnectUnresponsivePeers(data.current.disconnect_unresponsive_peers !== false)
      setRaw(data.raw_user_conf || '')
    }).catch(() => null)
    getBitcoinSource().then((data: any) => {
      if (data?.source === 'local' || data?.source === 'remote') {
        setBitcoinSourceState(data.source)
      }
    }).catch(() => null)
    loadUpgradeStatus()
  }, [])

  useEffect(() => {
    let mounted = true
    const loadGraphStatus = () => {
      getLndStatus()
        .then((data: any) => {
          if (!mounted) return
          setSyncedToGraph(
            data?.info_known !== false && typeof data?.synced_to_graph === 'boolean'
              ? data.synced_to_graph
              : null
          )
        })
        .catch(() => {
          if (mounted) setSyncedToGraph(null)
        })
    }
    loadGraphStatus()
    const timer = setInterval(loadGraphStatus, 15000)
    return () => {
      mounted = false
      clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    loadLocalStatus()
    const timer = setInterval(loadLocalStatus, 10000)
    return () => clearInterval(timer)
  }, [])

  useEffect(() => {
    if (!upgradeModalOpen) return
    let mounted = true
    const loadLogs = async () => {
      setUpgradeLogsStatus(t('lndUpgrade.loadingLogs'))
      try {
        if (!upgradeStartedVersion && !upgrade?.running && !upgradeLocked) {
          setUpgradeLogs([])
          setUpgradeLogsStatus('')
          return
        }
        const res = await getLogs('lnd-upgrade', 200, upgradeLogSince || undefined)
        if (!mounted) return
        const lines: string[] = Array.isArray(res?.lines) ? res.lines : []
        const startMarker = '==> Starting LND upgrade to v'
        const systemdStartMarker = 'Started lightningos-lnd-upgrade.service'
        const expectedStartMarker = upgradeStartedVersion
          ? `${startMarker}${upgradeStartedVersion}`
          : ''

        let runLines = lines
        let hasRunSegment = false
        if (expectedStartMarker) {
          const expectedScriptIndex = findLastMatchIndex(lines, expectedStartMarker)
          const expectedSystemdIndex = findLastPredicateIndex(
            lines,
            (line: string) =>
              line.includes(systemdStartMarker) &&
              line.includes(`--version ${upgradeStartedVersion}`)
          )
          const expectedIndex = Math.max(expectedScriptIndex, expectedSystemdIndex)
          if (expectedIndex !== -1) {
            runLines = lines.slice(expectedIndex)
            hasRunSegment = true
          }
        } else if (upgrade?.running || upgradeLocked) {
          const genericScriptIndex = findLastMatchIndex(lines, startMarker)
          const genericSystemdIndex = findLastMatchIndex(lines, systemdStartMarker)
          const genericIndex = Math.max(genericScriptIndex, genericSystemdIndex)
          if (genericIndex !== -1) {
            runLines = lines.slice(genericIndex)
            hasRunSegment = true
          }
        }

        setUpgradeLogs(expectedStartMarker && !hasRunSegment ? [] : runLines)

        if (hasRunSegment) {
          const completed = runLines.some((line: string) => line.includes('Upgrade complete.'))
          const errorLine = [...runLines].reverse().find((line: string) =>
            line.includes('[ERROR]') || line.includes('Upgrade failed')
          )
          if (completed) {
            setUpgradeComplete(true)
            setUpgradeLocked(false)
          }
          if (errorLine) {
            setUpgradeError(errorLine)
            setUpgradeLocked(false)
          }
        }
        setUpgradeLogsStatus('')
      } catch (err) {
        if (!mounted) return
        setUpgradeLogsStatus(err instanceof Error ? err.message : t('lndUpgrade.logFetchFailed'))
      }
    }
    const refreshStatus = async () => {
      try {
        const data = await getLndUpgradeStatus()
        if (mounted) {
          setUpgrade(data)
          if (data && data.running) {
            setUpgradeLocked(true)
          } else if (upgradeLocked) {
            setUpgradeLocked(false)
          }
          if (upgradeStartedVersion && !data?.running && !upgradeError && !data?.update_available) {
            setUpgradeComplete(true)
          }
        }
      } catch {
        // ignore status refresh errors during modal
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
  }, [upgradeModalOpen, t, upgrade?.running, upgradeLocked, upgradeStartedVersion, upgradeLogSince, upgradeError])

  const isHexColor = (value: string) => /^#[0-9a-fA-F]{6}$/.test(value.trim())

  const formatVersion = (value?: string) => {
    if (!value) return t('common.na')
    const trimmed = value.startsWith('v') ? value.slice(1) : value
    return `v${trimmed}`
  }

  const isRcVersion = (value?: string) => {
    if (!value) return false
    return /(^|[.-])rc[0-9]+/i.test(value)
  }

  const formatCheckedAt = (value?: string) => {
    if (!value) return ''
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return ''
    return parsed.toLocaleString()
  }

  const localReady = useMemo(() => {
    if (bitcoinLocalStatus?.rpc_ok !== true) return false
    if (bitcoinLocalStatus?.initial_block_download === true) return false
    const progress = bitcoinLocalStatus?.verification_progress
    const headers = bitcoinLocalStatus?.headers
    const blocks = bitcoinLocalStatus?.blocks
    if (typeof progress === 'number' && progress < 0.9999) return false
    if (typeof headers === 'number' && headers > 0) {
      if (typeof blocks !== 'number' || blocks < headers) return false
    } else if (typeof progress !== 'number') {
      return false
    }
    return true
  }, [bitcoinLocalStatus])

  const localToggleBlocked = bitcoinSource === 'remote' && !localReady
  const toggleDisabled = sourceBusy || localToggleBlocked || externalBitcoinDetected

  const loadUpgradeStatus = async (force = false, silent = false) => {
    if (!force && upgradeChecking) return
    if (!silent) {
      setUpgradeChecking(true)
      setUpgradeMessage(t('lndUpgrade.checking'))
    }
    try {
      const data = await getLndUpgradeStatus(force)
      setUpgrade(data)
      if (!silent) {
        setUpgradeMessage('')
      }
    } catch (err) {
      if (!silent) {
        setUpgradeMessage(err instanceof Error ? err.message : t('lndUpgrade.statusFailed'))
      }
    } finally {
      if (!silent) {
        setUpgradeChecking(false)
      }
    }
  }

  const openUpgradeModal = () => {
    const nowSince = new Date().toISOString()
    setUpgradeModalOpen(true)
    setUpgradeLogs([])
    setUpgradeLogsStatus('')
    setUpgradeMessage('')
    setUpgradeError(null)
    setUpgradeComplete(false)
    setUpgradeLocked(Boolean(upgrade?.running))
    setUpgradeRcConfirm(false)
    setUpgradeStartedVersion('')
    setUpgradeLogSince(nowSince)
  }

  const closeUpgradeModal = () => {
    if (upgradeBusy || (upgradeLocked && !upgradeError && !upgradeComplete)) return
    setUpgradeModalOpen(false)
    setUpgradeRcConfirm(false)
  }

  const showConfirmUpgrade = Boolean(upgrade?.update_available) && !upgradeComplete

  const startUpgrade = async () => {
    if (externalBitcoinDetected || !upgrade?.latest_version || upgradeBusy) return
    if (isRcVersion(upgrade.latest_version) && !upgradeRcConfirm) {
      setUpgradeRcConfirm(true)
      return
    }
    // Small safety window avoids missing early log lines emitted in the same second.
    const sinceNow = new Date(Date.now() - 15000).toISOString()
    setUpgradeStartedVersion(upgrade.latest_version)
    setUpgradeLogSince(sinceNow)
    setUpgradeBusy(true)
    setUpgradeError(null)
    setUpgradeComplete(false)
    setUpgradeMessage(t('lndUpgrade.starting'))
    try {
      await startLndUpgrade({
        target_version: upgrade.latest_version,
        download_url: upgrade.latest_url
      })
      setUpgradeMessage(t('lndUpgrade.started'))
      setUpgradeModalOpen(true)
      setUpgradeLocked(true)
    } catch (err) {
      const message = err instanceof Error ? err.message : t('lndUpgrade.startFailed')
      setUpgradeMessage(message)
      setUpgradeError(message)
      setUpgradeLocked(false)
    } finally {
      setUpgradeBusy(false)
      loadUpgradeStatus(true, true)
    }
  }

  const handleSave = async () => {
    if (aliasTooLong) {
      setStatus(t('lndConfig.aliasTooLong', { count: aliasByteLength, max: LND_ALIAS_MAX_BYTES }))
      return
    }
    if (!isHexColor(color)) {
      setStatus(t('lndConfig.colorInvalid'))
      return
    }
    setStatus(t('common.saving'))
    try {
      await updateLndConfig({
        alias,
        color,
        min_channel_size_sat: Number(minChan || 0),
        max_channel_size_sat: Number(maxChan || 0),
        apply_now: true
      })
      setStatus(t('lndConfig.savedApplied'))
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('lndConfig.saveFailed'))
    }
  }

  const handleSaveRaw = async () => {
    setStatus(t('lndConfig.savingAdvanced'))
    try {
      const result = await updateLndRawConfig({ raw_user_conf: raw, apply_now: true })
      if (result?.warning) {
        setStatus(t('lndConfig.advancedAppliedWarning', { warning: result.warning }))
      } else {
        setStatus(t('lndConfig.advancedApplied'))
      }
    } catch (err) {
      if (err instanceof Error && err.message) {
        setStatus(err.message)
      } else {
        setStatus(t('lndConfig.advancedFailed'))
      }
    }
  }

  const handleSaveNetwork = async () => {
    if (networkMode === 'custom') {
      setStatus(t('lndConfig.networkChoosePreset'))
      return
    }
    const peers = Number(graphSyncPeers)
    if (!Number.isInteger(peers) || peers < 1 || peers > 100) {
      setStatus(t('lndConfig.graphSyncPeersInvalid'))
      return
    }
    setNetworkBusy(true)
    setStatus(t('common.saving'))
    try {
      await updateLndConfig({
        network_mode: networkMode,
        graph_sync_peers: peers,
        disconnect_unresponsive_peers: disconnectUnresponsivePeers,
        apply_now: true
      })
      const refreshed = await getLndConfig().catch(() => null)
      if (refreshed?.raw_user_conf) {
        setRaw(refreshed.raw_user_conf)
        setConfig(refreshed)
      }
      setStatus(t('lndConfig.networkSaved'))
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('lndConfig.networkSaveFailed'))
    } finally {
      setNetworkBusy(false)
    }
  }

  const handleToggleSource = async () => {
    if (externalBitcoinDetected || sourceBusy || localToggleBlocked) return
    const next = bitcoinSource === 'remote' ? 'local' : 'remote'
    const targetLabel = next === 'local' ? t('common.local') : t('common.remote')
    setSourceBusy(true)
    setStatus(t('lndConfig.switchingBitcoin', { target: targetLabel }))
    try {
      await setBitcoinSource({ source: next })
      setBitcoinSourceState(next)
      setStatus(t('lndConfig.bitcoinSourceSet', { target: targetLabel }))
    } catch (err) {
      setStatus(err instanceof Error ? err.message : t('lndConfig.switchFailed'))
    } finally {
      setSourceBusy(false)
    }
  }

  return (
    <section className="flex flex-col gap-6">
      <div className="section-card">
        <div className="flex items-start justify-between gap-6">
          <div>
            <h2 className="text-2xl font-semibold">{t('lndConfig.title')}</h2>
            <p className="text-fog/60">{t('lndConfig.subtitle')}</p>
            <p className="text-fog/50 text-sm">{t('lndConfig.advancedHint')}</p>
          </div>
          <div className="flex flex-col items-end gap-2">
            <span className="text-xs text-fog/60">{t('lndConfig.bitcoinSource')}</span>
            {externalBitcoinDetected ? (
              <span className="rounded-full border border-cyan-400/30 bg-cyan-500/10 px-3 py-2 text-xs text-cyan-200">
                {t('lndConfig.externalBitcoinSource')}
              </span>
            ) : (
              <button
                className={`relative flex h-9 w-32 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${toggleDisabled ? 'opacity-70 cursor-not-allowed' : 'hover:border-white/30'}`}
                onClick={handleToggleSource}
                type="button"
                disabled={toggleDisabled}
                aria-label={t('lndConfig.toggleBitcoinSource')}
                title={localToggleBlocked ? t('lndConfig.localBitcoinRequired') : undefined}
              >
                <span
                  className={`absolute top-1 h-7 w-14 rounded-full bg-glow shadow transition-all ${bitcoinSource === 'local' ? 'left-[68px]' : 'left-[6px]'}`}
                />
                <span className={`relative z-10 flex-1 text-center text-xs ${bitcoinSource === 'remote' ? 'text-ink' : 'text-fog/60'}`}>{t('common.remote')}</span>
                <span className={`relative z-10 flex-1 text-center text-xs ${bitcoinSource === 'local' ? 'text-ink' : 'text-fog/60'}`}>{t('common.local')}</span>
              </button>
            )}
            {!externalBitcoinDetected && localToggleBlocked && (
              <span className="text-xs text-brass">{t('lndConfig.localBitcoinRequired')}</span>
            )}
          </div>
        </div>
        {externalBitcoinDetected && (
          <p className="mt-4 rounded-xl border border-cyan-400/20 bg-cyan-500/5 px-4 py-3 text-sm text-cyan-100/80">
            {t('lndConfig.existingNodeEditingNotice')}
          </p>
        )}
        {status && <p className="text-sm text-brass mt-4">{status}</p>}
      </div>

      <div className="section-card order-3 space-y-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h3 className="text-lg font-semibold">{t('lndConfig.networkPrivacy')}</h3>
            <p className="text-sm text-fog/60">{t('lndConfig.networkPrivacyHint')}</p>
          </div>
          <span className={`rounded-full border px-3 py-1 text-xs ${
            syncedToGraph === true
              ? 'border-glow/40 bg-glow/10 text-glow'
              : syncedToGraph === false
                ? 'border-brass/40 bg-brass/10 text-brass'
                : 'border-white/10 bg-white/5 text-fog/60'
          }`}>
            {syncedToGraph === true
              ? t('lndConfig.graphSynced')
              : syncedToGraph === false
                ? t('lndConfig.graphSyncing')
                : t('lndConfig.graphUnknown')}
          </span>
        </div>

        <div className="grid gap-4 lg:grid-cols-2" role="radiogroup" aria-label={t('lndConfig.networkPrivacy')}>
          <button
            type="button"
            role="radio"
            aria-checked={networkMode === 'private'}
            onClick={() => setNetworkMode('private')}
            className={`rounded-2xl border p-5 text-left transition ${
              networkMode === 'private'
                ? 'border-glow/60 bg-glow/10 shadow-[0_0_24px_rgba(45,212,191,0.08)]'
                : 'border-white/10 bg-ink/40 hover:border-white/25'
            }`}
          >
            <div className="flex items-center justify-between gap-3">
              <span className="font-semibold">{t('lndConfig.privateMode')}</span>
              <span className="rounded-full border border-glow/30 bg-glow/10 px-2 py-1 text-[10px] uppercase tracking-wide text-glow">
                {t('lndConfig.maximumPrivacy')}
              </span>
            </div>
            <p className="mt-3 text-sm text-fog/70">{t('lndConfig.privateModeDescription')}</p>
            <p className="mt-3 text-xs text-fog/50">{t('lndConfig.privateModeTechnical')}</p>
          </button>

          <button
            type="button"
            role="radio"
            aria-checked={networkMode === 'hybrid'}
            onClick={() => setNetworkMode('hybrid')}
            className={`rounded-2xl border p-5 text-left transition ${
              networkMode === 'hybrid'
                ? 'border-brass/60 bg-brass/10 shadow-[0_0_24px_rgba(245,158,11,0.08)]'
                : 'border-white/10 bg-ink/40 hover:border-white/25'
            }`}
          >
            <div className="flex items-center justify-between gap-3">
              <span className="font-semibold">{t('lndConfig.hybridMode')}</span>
              <span className="rounded-full border border-brass/30 bg-brass/10 px-2 py-1 text-[10px] uppercase tracking-wide text-brass">
                {t('lndConfig.fasterInitialSync')}
              </span>
            </div>
            <p className="mt-3 text-sm text-fog/70">{t('lndConfig.hybridModeDescription')}</p>
            <p className="mt-3 text-xs text-brass">{t('lndConfig.hybridModePrivacyWarning')}</p>
          </button>
        </div>

        {networkMode === 'custom' && (
          <div className="rounded-xl border border-brass/30 bg-brass/10 px-4 py-3 text-sm text-brass">
            {t('lndConfig.customNetworkDetected')}
          </div>
        )}
        {networkMode === 'hybrid' && syncedToGraph === true && (
          <div className="rounded-xl border border-glow/30 bg-glow/10 px-4 py-3 text-sm text-glow">
            {t('lndConfig.graphSyncedPrivateSuggestion')}
          </div>
        )}

        <div className="grid gap-4 lg:grid-cols-2">
          <div className="space-y-2">
            <label className="text-sm text-fog/70" htmlFor="graph-sync-peers">{t('lndConfig.graphSyncPeers')}</label>
            <input
              id="graph-sync-peers"
              className="input-field"
              type="number"
              min={1}
              max={100}
              value={graphSyncPeers}
              onChange={(event) => setGraphSyncPeers(event.target.value)}
            />
            <p className="text-xs text-fog/50">{t('lndConfig.graphSyncPeersHint')}</p>
          </div>
          <label className="flex cursor-pointer items-start gap-3 rounded-2xl border border-white/10 bg-ink/40 p-4">
            <input
              className="mt-1 h-4 w-4 accent-teal-400"
              type="checkbox"
              checked={disconnectUnresponsivePeers}
              onChange={(event) => setDisconnectUnresponsivePeers(event.target.checked)}
            />
            <span>
              <span className="block text-sm font-medium">{t('lndConfig.disconnectUnresponsivePeers')}</span>
              <span className="mt-1 block text-xs text-fog/50">{t('lndConfig.disconnectUnresponsivePeersHint')}</span>
            </span>
          </label>
        </div>

        <div className="flex flex-wrap items-center gap-3">
          <button
            className="btn-primary"
            type="button"
            onClick={handleSaveNetwork}
            disabled={networkBusy || networkMode === 'custom'}
          >
            {networkBusy ? t('common.saving') : t('lndConfig.saveNetworkRestart')}
          </button>
          <p className="text-xs text-fog/50">{t('lndConfig.networkRestartHint')}</p>
        </div>
      </div>

      <div className="section-card order-1 space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-lg font-semibold">{t('lndConfig.basicSettings')}</h3>
          <button className="btn-secondary" onClick={() => setAdvanced((v) => !v)}>
            {advanced ? t('lndConfig.hideAdvanced') : t('lndConfig.showAdvanced')}
          </button>
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          <div className="space-y-2">
            <label className="text-sm text-fog/70">{t('lndConfig.alias')}</label>
            <input
              className="input-field"
              placeholder={t('lndConfig.nodeName')}
              value={alias}
              maxLength={LND_ALIAS_MAX_BYTES}
              aria-invalid={aliasTooLong}
              onChange={(e) => setAlias(e.target.value)}
            />
            <div className="flex items-center justify-between gap-3 text-xs">
              <p className="text-fog/50">{t('lndConfig.aliasHint')}</p>
              <span className={aliasTooLong ? 'text-rose-300' : 'text-fog/50'}>
                {t('lndConfig.aliasByteCount', { count: aliasByteLength, max: LND_ALIAS_MAX_BYTES })}
              </span>
            </div>
          </div>
          <div className="space-y-2">
            <label className="text-sm text-fog/70">{t('lndConfig.nodeColor')}</label>
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
              <input
                className="h-12 w-16 rounded-xl border border-white/10 bg-ink/60 p-1"
                type="color"
                value={color}
                onChange={(e) => {
                  setColor(e.target.value)
                  setColorInput(e.target.value)
                }}
              />
              <input
                className="input-field flex-1"
                placeholder={t('lndConfig.colorPlaceholder')}
                value={colorInput}
                onChange={(e) => {
                  const next = e.target.value
                  setColorInput(next)
                  if (isHexColor(next)) {
                    setColor(next)
                  }
                }}
              />
            </div>
            <p className="text-xs text-fog/50">{t('lndConfig.colorHint')}</p>
          </div>
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          <div className="space-y-2">
            <label className="text-sm text-fog/70">{t('lndConfig.minChannelSize')}</label>
            <input
              className="input-field"
              placeholder={t('lndConfig.minChannelPlaceholder')}
              type="number"
              min={0}
              value={minChan}
              onChange={(e) => setMinChan(e.target.value)}
            />
            <p className="text-xs text-fog/50">{t('lndConfig.minChannelHint')}</p>
          </div>
          <div className="space-y-2">
            <label className="text-sm text-fog/70">{t('lndConfig.maxChannelSize')}</label>
            <input
              className="input-field"
              placeholder={t('common.optional')}
              type="number"
              min={0}
              value={maxChan}
              onChange={(e) => setMaxChan(e.target.value)}
            />
            <p className="text-xs text-fog/50">{t('lndConfig.maxChannelHint')}</p>
          </div>
        </div>
        <button className="btn-primary" onClick={handleSave}>{t('lndConfig.saveRestart')}</button>
        <p className="text-xs text-fog/50">{t('lndConfig.restartHint')}</p>
      </div>

      {advanced && (
        <div className="section-card order-2 space-y-4">
          <h3 className="text-lg font-semibold">{t('lndConfig.advancedEditor')}</h3>
          <textarea className="input-field min-h-[180px]" value={raw} onChange={(e) => setRaw(e.target.value)} />
          <button className="btn-secondary" onClick={handleSaveRaw}>{t('lndConfig.applyAdvanced')}</button>
        </div>
      )}

      <div className="section-card order-4 space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <h3 className="text-lg font-semibold">{t('lndUpgrade.title')}</h3>
            <p className="text-fog/60">{t('lndUpgrade.subtitle')}</p>
          </div>
          <button
            className="btn-secondary"
            onClick={() => loadUpgradeStatus(true)}
            disabled={upgradeChecking}
          >
            {upgradeChecking ? t('lndUpgrade.checking') : t('common.refresh')}
          </button>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 text-sm">
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('lndUpgrade.current')}</span>
            <span className="font-mono text-fog">{formatVersion(upgrade?.current_version)}</span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('lndUpgrade.latest')}</span>
            <span className="font-mono text-fog">{formatVersion(upgrade?.latest_version)}</span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('lndUpgrade.channel')}</span>
            <span className="text-fog/90">
              {upgrade?.latest_channel
                ? t(`lndUpgrade.channels.${upgrade.latest_channel}`)
                : t('common.na')}
            </span>
          </div>
          <div className="flex items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3">
            <span className="text-fog/70">{t('lndUpgrade.checkedAt')}</span>
            <span className="text-fog/90">{formatCheckedAt(upgrade?.checked_at) || t('common.na')}</span>
          </div>
        </div>

        {upgrade?.error && (
          <p className="text-sm text-rose-200">{t('lndUpgrade.statusError', { error: upgrade.error })}</p>
        )}

        {!upgrade?.error && (
          <p className="text-sm text-fog/70">
            {upgrade?.running
              ? t('lndUpgrade.inProgress')
              : upgrade?.update_available
                ? t('lndUpgrade.updateAvailable')
                : t('lndUpgrade.upToDate')}
          </p>
        )}

        {upgradeMessage && <p className="text-sm text-brass">{upgradeMessage}</p>}

        <div className="flex flex-wrap items-center gap-3">
          <button
            className="btn-primary"
            onClick={openUpgradeModal}
            disabled={externalBitcoinDetected || !upgrade?.update_available || upgrade?.running}
          >
            {upgrade?.running ? t('lndUpgrade.upgrading') : t('lndUpgrade.upgrade')}
          </button>
          {upgrade?.running && (
            <button className="btn-secondary" onClick={openUpgradeModal}>
              {t('lndUpgrade.viewLogs')}
            </button>
          )}
        </div>

        <p className="text-xs text-fog/50">{t('lndUpgrade.warning')}</p>
        {externalBitcoinDetected && (
          <p className="text-xs text-cyan-200">{t('lndConfig.externalUpgradeProtected')}</p>
        )}
      </div>

      {!config && <p className="order-5 text-fog/60">{t('lndConfig.loadingConfig')}</p>}

      {upgradeModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center px-4">
          <div
            className="absolute inset-0 bg-black/60 backdrop-blur-sm"
            onClick={closeUpgradeModal}
            aria-hidden="true"
          />
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="lnd-upgrade-title"
            className="relative z-10 w-full max-w-3xl rounded-3xl border border-white/10 bg-slate/95 p-6 shadow-panel"
          >
            <h4 id="lnd-upgrade-title" className="text-lg font-semibold">{t('lndUpgrade.confirmTitle')}</h4>
            <p className="mt-2 text-sm text-fog/70">
              {t('lndUpgrade.confirmBody', { version: formatVersion(upgrade?.latest_version) })}
            </p>
            <p className="mt-3 text-xs text-rose-200">{t('lndUpgrade.confirmWarning')}</p>
            {isRcVersion(upgrade?.latest_version) && (
              <>
                <p className="mt-3 text-xs text-rose-200">
                  {t('lndUpgrade.rcWarning', { version: formatVersion(upgrade?.latest_version) })}
                </p>
                {!upgradeRcConfirm && (
                  <p className="mt-2 text-xs text-amber-200">{t('lndUpgrade.rcConfirmHint')}</p>
                )}
                {upgradeRcConfirm && (
                  <p className="mt-2 text-xs text-amber-200">{t('lndUpgrade.rcConfirmReady')}</p>
                )}
              </>
            )}
            {upgradeMessage && <p className="mt-3 text-sm text-brass">{upgradeMessage}</p>}
            {upgradeError && <p className="mt-2 text-sm text-rose-200">{upgradeError}</p>}
            {upgradeComplete && !upgradeError && (
              <p className="mt-2 text-sm text-emerald-200">{t('lndUpgrade.completed')}</p>
            )}

            <div className="mt-4">
              <div className="flex items-center justify-between">
                <span className="text-sm text-fog/70">{t('lndUpgrade.logsTitle')}</span>
                <span className="text-xs text-fog/50">
                  {upgrade?.running ? t('lndUpgrade.inProgress') : t('lndUpgrade.logsHint')}
                </span>
              </div>
              {upgradeLogsStatus && <p className="mt-2 text-xs text-brass">{upgradeLogsStatus}</p>}
              <div className="mt-2 max-h-[320px] overflow-y-auto rounded-2xl border border-white/10 bg-ink/70 p-3 text-xs font-mono whitespace-pre-wrap">
                {upgradeLogs.length ? upgradeLogs.join('\n') : t('lndUpgrade.noLogs')}
              </div>
            </div>

            <div className="mt-5 flex items-center justify-end gap-3">
              <button
                className={`btn-secondary ${(upgradeBusy || (upgradeLocked && !upgradeError && !upgradeComplete)) ? 'opacity-60 pointer-events-none' : ''}`}
                onClick={closeUpgradeModal}
                type="button"
                disabled={upgradeBusy || (upgradeLocked && !upgradeError && !upgradeComplete)}
              >
                {upgradeComplete || upgradeError ? t('common.close') : t('common.cancel')}
              </button>
              {showConfirmUpgrade && (
                <button
                  className={`btn-secondary text-amber-200 border-amber-400/30 ${upgradeBusy ? 'opacity-60 pointer-events-none' : ''}`}
                  onClick={startUpgrade}
                  type="button"
                  disabled={!upgrade?.update_available || upgrade?.running}
                >
                  {upgradeBusy
                    ? t('lndUpgrade.upgrading')
                    : (isRcVersion(upgrade?.latest_version) && upgradeRcConfirm
                      ? t('lndUpgrade.rcConfirmUpgrade')
                      : t('lndUpgrade.confirmUpgrade'))}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
