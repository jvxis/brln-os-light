import { useEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { getNotifications, getTelegramNotifications, testTelegramBackup, updateTelegramNotifications } from '../api'
import { getLocale } from '../i18n'

type Notification = {
  id: number
  occurred_at: string
  type: string
  action: string
  direction: string
  status: string
  amount_sat: number
  fee_sat: number
  fee_msat?: number
  peer_pubkey?: string
  peer_alias?: string
  channel_id?: number
  channel_point?: string
  txid?: string
  payment_hash?: string
  memo?: string
}

type TelegramNotificationConfig = {
  chat_id?: string
  bot_token_set?: boolean
  scb_backup_enabled?: boolean
  summary_enabled?: boolean
  summary_interval_min?: number
  system_summary_enabled?: boolean
  system_summary_interval_min?: number
}

const arrowForDirection = (value: string) => {
  if (value === 'in') return { label: '<-', tone: 'text-glow' }
  if (value === 'out') return { label: '->', tone: 'text-ember' }
  return { label: '.', tone: 'text-fog/50' }
}

const trimMemo = (value: string, max = 48) => {
  if (value.length <= max) return value
  return `${value.slice(0, max)}...`
}

const feeMsatTotal = (feeSat: number, feeMsat?: number) => {
  if (feeMsat && feeMsat > 0) {
    return feeMsat
  }
  return Math.max(0, feeSat) * 1000
}

const formatFeeDisplay = (locale: string, feeSat: number, feeMsat?: number) => {
  const msat = feeMsatTotal(feeSat, feeMsat)
  if (msat <= 0) return ''
  const sats = msat / 1000
  const formatter = new Intl.NumberFormat(locale, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
  return `${formatter.format(sats)} sats`
}

const formatFeeRate = (amount: number, feeSat: number, feeMsat?: number) => {
  if (!amount || amount <= 0) return ''
  const msat = feeMsatTotal(feeSat, feeMsat)
  if (msat <= 0) return ''
  const feeSatTotal = msat / 1000
  const ratio = feeSatTotal / amount
  const percentRaw = ratio * 100
  const percent = percentRaw.toFixed(3).replace(/\.?0+$/, '')
  const ppm = Math.round(ratio * 1_000_000)
  return `${percent}% ${ppm}ppm`
}

const mempoolLinkFromChannelPoint = (channelPoint?: string) => {
  if (!channelPoint) return ''
  const parts = channelPoint.split(':')
  if (parts.length !== 2) return ''
  return `https://mempool.space/pt/tx/${parts[0]}#vout=${parts[1]}`
}

const mempoolTxLink = (txid?: string) => {
  if (!txid) return ''
  return `https://mempool.space/tx/${txid}`
}

export default function Notifications() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const limitOptions = [200, 500, 1000]

  const formatTimestamp = (value: string) => {
    if (!value) return t('common.unknownTime')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return t('common.unknownTime')
    return parsed.toLocaleString(locale, {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    })
  }

  const labelForType = (value: string) => {
    switch (value) {
      case 'onchain':
        return t('notifications.type.onchain')
      case 'lightning':
        return t('notifications.type.lightning')
      case 'channel':
        return t('notifications.type.channel')
      case 'forward':
        return t('notifications.type.forward')
      case 'rebalance':
        return t('notifications.type.rebalance')
      case 'keysend':
        return t('notifications.type.keysend')
      default:
        if (!value) return ''
        return value.charAt(0).toUpperCase() + value.slice(1)
    }
  }

  const labelForAction = (value: string) => {
    switch (value) {
      case 'receive':
        return t('notifications.action.received')
      case 'send':
        return t('notifications.action.sent')
      case 'open':
        return t('notifications.action.opened')
      case 'close':
        return t('notifications.action.closed')
      case 'opening':
        return t('notifications.action.opening')
      case 'closing':
        return t('notifications.action.closing')
      case 'forwarded':
        return t('notifications.action.forwarded')
      case 'rebalanced':
        return t('notifications.action.rebalanced')
      default:
        return value
    }
  }

  const normalizeStatus = (value: string) => {
    if (!value) return t('common.unknown').toUpperCase()
    return value.replace(/_/g, ' ').toUpperCase()
  }

  const [items, setItems] = useState<Notification[]>([])
  const [status, setStatus] = useState(t('notifications.loading'))
  const [streamState, setStreamState] = useState<'idle' | 'waiting' | 'reconnecting' | 'error'>('idle')
  const streamErrors = useRef(0)
  const [filter, setFilter] = useState<'all' | 'onchain' | 'lightning' | 'keysend' | 'channel' | 'forward' | 'rebalance'>('all')
  const [limit, setLimit] = useState(200)
  const limitRef = useRef(limit)
  const [telegramConfig, setTelegramConfig] = useState<TelegramNotificationConfig | null>(null)
  const [telegramToken, setTelegramToken] = useState('')
  const [telegramChatId, setTelegramChatId] = useState('')
  const [telegramScbEnabled, setTelegramScbEnabled] = useState(true)
  const [telegramSummaryEnabled, setTelegramSummaryEnabled] = useState(false)
  const [telegramSummaryInterval, setTelegramSummaryInterval] = useState('')
  const [telegramSystemEnabled, setTelegramSystemEnabled] = useState(false)
  const [telegramSystemInterval, setTelegramSystemInterval] = useState('')
  const [telegramStatus, setTelegramStatus] = useState('')
  const [telegramSaving, setTelegramSaving] = useState(false)
  const [telegramTesting, setTelegramTesting] = useState(false)
  const [telegramOpen, setTelegramOpen] = useState(false)

  useEffect(() => {
    limitRef.current = limit
  }, [limit])

  useEffect(() => {
    let mounted = true
    const load = async () => {
      setStatus(t('notifications.loading'))
      try {
        const res = await getNotifications(limit)
        if (!mounted) return
        setItems(Array.isArray(res?.items) ? res.items : [])
        setStatus('')
      } catch (err: any) {
        if (!mounted) return
        setStatus(err?.message || t('notifications.unavailable'))
      }
    }
    load()
    return () => {
      mounted = false
    }
  }, [limit])

  useEffect(() => {
    let mounted = true
    getTelegramNotifications()
      .then((data: TelegramNotificationConfig) => {
        if (!mounted) return
        setTelegramConfig(data)
        setTelegramChatId(data?.chat_id || '')
        setTelegramToken('')
        setTelegramScbEnabled(Boolean(data?.scb_backup_enabled))
        setTelegramSummaryEnabled(Boolean(data?.summary_enabled))
        setTelegramSummaryInterval(data?.summary_interval_min ? String(data.summary_interval_min) : '')
        setTelegramSystemEnabled(Boolean(data?.system_summary_enabled))
        setTelegramSystemInterval(data?.system_summary_interval_min ? String(data.system_summary_interval_min) : '')
      })
      .catch(() => null)
    return () => {
      mounted = false
    }
  }, [])

  useEffect(() => {
    const stream = new EventSource('/api/notifications/stream')
    const markWaiting = () => {
      streamErrors.current = 0
      setStreamState('waiting')
    }
    stream.onopen = markWaiting
    stream.addEventListener('ready', markWaiting)
    stream.addEventListener('heartbeat', () => {
      setStreamState((prev) => (prev === 'idle' ? prev : 'waiting'))
    })
    stream.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data)
        if (!payload || !payload.id) return
        streamErrors.current = 0
        setStreamState('idle')
        setItems((prev) => {
          const next = [payload, ...prev.filter((item) => item.id !== payload.id)]
          next.sort((a, b) => new Date(b.occurred_at).getTime() - new Date(a.occurred_at).getTime())
          return next.slice(0, limitRef.current)
        })
      } catch {
        // ignore malformed payloads
      }
    }
    stream.onerror = () => {
      streamErrors.current += 1
      if (streamErrors.current >= 5) {
        setStreamState('error')
      } else {
        setStreamState('reconnecting')
      }
    }
    return () => {
      stream.close()
    }
  }, [])

  const rebalanceHashes = useMemo(() => {
    return new Set(items.filter((item) => item.type === 'rebalance' && item.payment_hash).map((item) => item.payment_hash))
  }, [items])

  const filtered = useMemo(() => {
    const base = filter === 'all'
      ? items
      : items.filter((item) => item.type === filter)
    return base.filter((item) => {
      if (item.type === 'rebalance') return true
      if (!item.payment_hash) return true
      return !rebalanceHashes.has(item.payment_hash)
    })
  }, [filter, items, rebalanceHashes])

  const telegramEnabled = Boolean(telegramConfig?.bot_token_set && telegramConfig?.chat_id)

  const handleTelegramKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      handleSaveTelegram()
    }
  }

  const triggerTelegramTest = async (startingMessage?: string, force?: boolean) => {
    if (telegramTesting) return
    if (!force && !telegramEnabled) {
      setTelegramStatus(t('notifications.telegram.configureFirst'))
      return
    }
    if (startingMessage) {
      setTelegramStatus(startingMessage)
    } else {
      setTelegramStatus(t('notifications.telegram.sendingTest'))
    }
    setTelegramTesting(true)
    try {
      await testTelegramBackup()
      setTelegramStatus(t('notifications.telegram.testSent'))
    } catch (err: any) {
      setTelegramStatus(err?.message || t('notifications.telegram.testFailed'))
    } finally {
      setTelegramTesting(false)
    }
  }

  const handleSaveTelegram = async (overrides?: {
    scbBackupEnabled?: boolean
    summaryEnabled?: boolean
    summaryInterval?: string
    systemSummaryEnabled?: boolean
    systemSummaryInterval?: string
  }) => {
    if (telegramSaving) return
    setTelegramSaving(true)
    setTelegramStatus(t('common.saving'))
    try {
      const nextScbEnabled = overrides?.scbBackupEnabled ?? telegramScbEnabled
      const nextSummaryEnabled = overrides?.summaryEnabled ?? telegramSummaryEnabled
      const nextSummaryInterval = overrides?.summaryInterval ?? telegramSummaryInterval
      const nextSystemEnabled = overrides?.systemSummaryEnabled ?? telegramSystemEnabled
      const nextSystemInterval = overrides?.systemSummaryInterval ?? telegramSystemInterval
      const prevIntervalValue = Number(telegramConfig?.summary_interval_min || 0)
      const effectiveSummaryInterval = nextSummaryInterval || (prevIntervalValue ? String(prevIntervalValue) : '720')
      const prevSystemIntervalValue = Number(telegramConfig?.system_summary_interval_min || 0)
      const effectiveSystemInterval = nextSystemInterval || (prevSystemIntervalValue ? String(prevSystemIntervalValue) : '720')
      const summaryIntervalValue = Number(effectiveSummaryInterval || 0)
      const systemIntervalValue = Number(effectiveSystemInterval || 0)
      if (nextSummaryEnabled) {
        if (!summaryIntervalValue || summaryIntervalValue < 60 || summaryIntervalValue > 720) {
          setTelegramStatus(t('notifications.telegram.summaryIntervalInvalid'))
          setTelegramSaving(false)
          return
        }
      }
      if (nextSystemEnabled) {
        if (!systemIntervalValue || systemIntervalValue < 60 || systemIntervalValue > 720) {
          setTelegramStatus(t('notifications.telegram.systemIntervalInvalid'))
          setTelegramSaving(false)
          return
        }
      }
      const trimmedToken = telegramToken.trim()
      const trimmedChatId = telegramChatId.trim()
      const existingChatId = String(telegramConfig?.chat_id || '').trim()
      const hadTelegram = Boolean(telegramConfig?.bot_token_set && existingChatId)
      const chatChanged = trimmedChatId !== '' && trimmedChatId !== existingChatId
      const tokenProvided = trimmedToken !== ''
      const clearTelegram = trimmedToken === '' && trimmedChatId === '' && hadTelegram
      const payload: {
        bot_token?: string
        chat_id?: string
        scb_backup_enabled?: boolean
        summary_enabled?: boolean
        summary_interval_min?: number
        system_summary_enabled?: boolean
        system_summary_interval_min?: number
      } = {
        scb_backup_enabled: nextScbEnabled,
        summary_enabled: nextSummaryEnabled,
        summary_interval_min: summaryIntervalValue || undefined,
        system_summary_enabled: nextSystemEnabled,
        system_summary_interval_min: systemIntervalValue || undefined
      }
      if (clearTelegram) {
        payload.bot_token = ''
        payload.chat_id = ''
      } else {
        if (tokenProvided) {
          payload.bot_token = trimmedToken
        }
        if (chatChanged) {
          payload.chat_id = trimmedChatId
        }
      }
      await updateTelegramNotifications(payload)
      const data: TelegramNotificationConfig = await getTelegramNotifications()
      setTelegramConfig(data)
      setTelegramChatId(data?.chat_id || '')
      setTelegramToken('')
      setTelegramScbEnabled(Boolean(data?.scb_backup_enabled))
      setTelegramSummaryEnabled(Boolean(data?.summary_enabled))
      setTelegramSummaryInterval(data?.summary_interval_min ? String(data.summary_interval_min) : '')
      setTelegramSystemEnabled(Boolean(data?.system_summary_enabled))
      setTelegramSystemInterval(data?.system_summary_interval_min ? String(data.system_summary_interval_min) : '')
      if (!data?.bot_token_set && !data?.chat_id) {
        setTelegramStatus(t('notifications.telegram.disabled'))
      } else {
        const nextEnabled = Boolean(data?.bot_token_set && data?.chat_id)
        const credentialsUpdated = clearTelegram || tokenProvided || chatChanged
        if (nextEnabled && credentialsUpdated) {
          await triggerTelegramTest(t('notifications.telegram.savedSendingTest'), true)
        } else {
          const prevScbEnabled = Boolean(telegramConfig?.scb_backup_enabled)
          const prevSummaryEnabled = Boolean(telegramConfig?.summary_enabled)
          const prevSystemEnabled = Boolean(telegramConfig?.system_summary_enabled)
          const prevIntervalValue = Number(telegramConfig?.summary_interval_min || 0)
          const prevSystemIntervalValue = Number(telegramConfig?.system_summary_interval_min || 0)
          const scbChanged = prevScbEnabled !== nextScbEnabled
          const summaryChanged = prevSummaryEnabled !== nextSummaryEnabled
          const systemChanged = prevSystemEnabled !== nextSystemEnabled
          const intervalChanged = summaryIntervalValue > 0 && prevIntervalValue !== summaryIntervalValue
          const systemIntervalChanged = systemIntervalValue > 0 && prevSystemIntervalValue !== systemIntervalValue
          if ((intervalChanged || systemIntervalChanged) && !scbChanged && !summaryChanged && !systemChanged) {
            setTelegramStatus(t('notifications.telegram.frequencySaved'))
          } else {
            setTelegramStatus(t('notifications.telegram.rulesSaved'))
          }
        }
      }
    } catch (err: any) {
      setTelegramStatus(err?.message || t('notifications.telegram.saveFailed'))
    } finally {
      setTelegramSaving(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">{t('notifications.title')}</h2>
            <p className="text-fog/60">{t('notifications.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <button className={filter === 'all' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('all')}>{t('common.all')}</button>
            <button className={filter === 'onchain' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('onchain')}>{t('notifications.filter.onchain')}</button>
            <button className={filter === 'lightning' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('lightning')}>{t('notifications.filter.lightning')}</button>
            <button className={filter === 'keysend' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('keysend')}>{t('notifications.filter.keysend')}</button>
            <button className={filter === 'channel' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('channel')}>{t('notifications.filter.channels')}</button>
            <button className={filter === 'forward' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('forward')}>{t('notifications.filter.forwards')}</button>
            <button className={filter === 'rebalance' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('rebalance')}>{t('notifications.filter.rebalance')}</button>
            <select className="btn-secondary text-xs" value={limit} onChange={(e) => setLimit(Number(e.target.value))}>
              {limitOptions.map((value) => (
                <option key={value} value={value}>{t('notifications.linesOption', { count: value })}</option>
              ))}
            </select>
          </div>
        </div>
      </div>

      <div className="section-card">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('notifications.telegram.title')}</h3>
            <p className="text-fog/60">{t('notifications.telegram.subtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-xs ${telegramEnabled ? 'text-glow' : 'text-fog/60'}`}>
              {telegramEnabled ? t('common.enabled') : t('common.disabled')}
            </span>
            <button className="btn-secondary" type="button" onClick={() => setTelegramOpen((prev) => !prev)}>
              {telegramOpen ? t('common.hide') : t('notifications.telegram.configure')}
            </button>
          </div>
        </div>
        {telegramOpen && (
          <div className="mt-4 space-y-4">
            <div className="rounded-2xl border border-white/10 bg-ink/70 p-4 space-y-4">
              <div>
                <h4 className="text-base font-semibold">{t('notifications.telegram.configTitle')}</h4>
                <p className="text-xs text-fog/60">{t('notifications.telegram.configSubtitle')}</p>
              </div>
              <div className="grid gap-4 lg:grid-cols-2">
                <div className="space-y-2">
                  <label className="text-sm text-fog/70">{t('notifications.telegram.botToken')}</label>
                  <input
                    className="input-field"
                    type="password"
                    placeholder={telegramConfig?.bot_token_set ? t('notifications.telegram.tokenSaved') : '123456:ABC...'}
                    value={telegramToken}
                    onChange={(e) => setTelegramToken(e.target.value)}
                    onKeyDown={handleTelegramKeyDown}
                  />
                  <p className="text-xs text-fog/50">{t('notifications.telegram.botTokenHint')}</p>
                </div>
                <div className="space-y-2">
                  <label className="text-sm text-fog/70">{t('notifications.telegram.chatId')}</label>
                  <input
                    className="input-field"
                    placeholder="123456789"
                    value={telegramChatId}
                    onChange={(e) => setTelegramChatId(e.target.value)}
                    onKeyDown={handleTelegramKeyDown}
                  />
                  <p className="text-xs text-fog/50">{t('notifications.telegram.chatIdHint')}</p>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-3">
                <button className="btn-primary" onClick={() => void handleSaveTelegram()} disabled={telegramSaving}>
                  {telegramSaving ? t('common.saving') : t('notifications.telegram.save')}
                </button>
                <button
                  className="btn-secondary"
                  onClick={() => triggerTelegramTest()}
                  disabled={telegramTesting || !telegramEnabled}
                >
                  {telegramTesting ? t('notifications.telegram.sendingTest') : t('notifications.telegram.sendTest')}
                </button>
                {telegramStatus && <span className="text-sm text-brass">{telegramStatus}</span>}
              </div>
              <p className="text-xs text-fog/50">{t('notifications.telegram.directChatOnly')}</p>
            </div>
            <div className="rounded-2xl border border-white/10 bg-ink/70 p-4 space-y-4">
              <div>
                <h4 className="text-base font-semibold">{t('notifications.telegram.notificationsTitle')}</h4>
                <p className="text-xs text-fog/60">{t('notifications.telegram.notificationsSubtitle')}</p>
              </div>
              <label className="flex items-start gap-3 text-sm text-fog">
                <input
                  type="checkbox"
                  checked={telegramScbEnabled}
                  onChange={(e) => {
                    const checked = e.target.checked
                    setTelegramScbEnabled(checked)
                    void handleSaveTelegram({ scbBackupEnabled: checked })
                  }}
                />
                <span>
                  <span className="font-semibold">{t('notifications.telegram.scbBackupLabel')}</span>
                  <span className="block text-xs text-fog/60">{t('notifications.telegram.scbBackupHint')}</span>
                </span>
              </label>
              <div className="grid gap-3 sm:items-start sm:grid-cols-[minmax(220px,320px)_96px_minmax(320px,1fr)]">
                <label className="flex items-start gap-3 text-sm text-fog">
                  <input
                    type="checkbox"
                    checked={telegramSummaryEnabled}
                    onChange={(e) => {
                      const checked = e.target.checked
                      setTelegramSummaryEnabled(checked)
                      void handleSaveTelegram({
                        summaryEnabled: checked,
                        summaryInterval: telegramSummaryInterval
                      })
                    }}
                  />
                  <span>
                    <span className="font-semibold">{t('notifications.telegram.summaryLabel')}</span>
                    <span className="block text-xs text-fog/60">{t('notifications.telegram.summaryHint')}</span>
                  </span>
                </label>
                <input
                  className="input-field w-[96px] sm:mt-0.5"
                  type="number"
                  min={60}
                  max={720}
                  placeholder="120"
                  value={telegramSummaryInterval}
                  onChange={(e) => setTelegramSummaryInterval(e.target.value)}
                  onKeyDown={handleTelegramKeyDown}
                />
                <div className="text-xs text-fog/50 sm:mt-1 sm:whitespace-nowrap sm:min-w-[320px]">
                  <span className="text-fog/60">{t('notifications.telegram.summaryInterval')}</span>
                  <span className="ml-2">{t('notifications.telegram.summaryIntervalHint')}</span>
                </div>
              </div>
              <div className="grid gap-3 sm:items-start sm:grid-cols-[minmax(220px,320px)_96px_minmax(320px,1fr)]">
                <label className="flex items-start gap-3 text-sm text-fog">
                  <input
                    type="checkbox"
                    checked={telegramSystemEnabled}
                    onChange={(e) => {
                      const checked = e.target.checked
                      setTelegramSystemEnabled(checked)
                      void handleSaveTelegram({
                        systemSummaryEnabled: checked,
                        systemSummaryInterval: telegramSystemInterval
                      })
                    }}
                  />
                  <span>
                    <span className="font-semibold">{t('notifications.telegram.systemLabel')}</span>
                    <span className="block text-xs text-fog/60">{t('notifications.telegram.systemHint')}</span>
                  </span>
                </label>
                <input
                  className="input-field w-[96px] sm:mt-0.5"
                  type="number"
                  min={60}
                  max={720}
                  placeholder="120"
                  value={telegramSystemInterval}
                  onChange={(e) => setTelegramSystemInterval(e.target.value)}
                  onKeyDown={handleTelegramKeyDown}
                />
                <div className="text-xs text-fog/50 sm:mt-1 sm:whitespace-nowrap sm:min-w-[320px]">
                  <span className="text-fog/60">{t('notifications.telegram.systemInterval')}</span>
                  <span className="ml-2">{t('notifications.telegram.systemIntervalHint')}</span>
                </div>
              </div>
              <p className="text-xs text-fog/50">{t('notifications.telegram.commandsHint')}</p>
            </div>
          </div>
        )}
      </div>

      <div className="section-card">
        <h3 className="text-lg font-semibold">{t('notifications.recentActivity')}</h3>
        {status && <p className="mt-4 text-sm text-fog/60">{status}</p>}
        {!status && streamState === 'reconnecting' && (
          <p className="mt-2 text-sm text-brass">{t('notifications.reconnecting')}</p>
        )}
        {!status && streamState === 'error' && (
          <p className="mt-2 text-sm text-brass">{t('notifications.liveUpdatesUnavailable')}</p>
        )}
        {!status && streamState === 'waiting' && filtered.length === 0 && (
          <p className="mt-2 text-sm text-fog/60">{t('notifications.waitingForEvents')}</p>
        )}
        {!status && !filtered.length && (
          <p className="mt-4 text-sm text-fog/60">{t('notifications.noNotifications')}</p>
        )}
        {filtered.length > 0 && (
          <div className="mt-4 max-h-[520px] overflow-y-auto pr-2">
            <div className="space-y-2 text-sm">
              {filtered.map((item) => {
                const arrow = arrowForDirection(item.direction)
                const title = `${labelForType(item.type)} ${labelForAction(item.action)}`
                const statusLabel = normalizeStatus(item.status)
                const peer = item.peer_alias || (item.peer_pubkey ? item.peer_pubkey.slice(0, 16) : '')
                const peerLabel = peer
                  ? item.type === 'rebalance'
                    ? t('notifications.routeLabel', { peer })
                    : item.type === 'keysend'
                      ? item.direction === 'in'
                        ? t('notifications.peerFrom', { peer })
                        : item.direction === 'out'
                          ? t('notifications.peerTo', { peer })
                          : t('notifications.peerLabel', { peer })
                      : t('notifications.peerLabel', { peer })
                  : ''
                const feeRate = formatFeeRate(item.amount_sat, item.fee_sat, item.fee_msat)
                let feeDetail = ''
                if (feeRate) {
                  if (item.type === 'forward') {
                    feeDetail = t('notifications.feeEarned', {
                      fee: formatFeeDisplay(locale, item.fee_sat, item.fee_msat),
                      rate: feeRate
                    })
                  } else if (item.type === 'rebalance') {
                    feeDetail = t('notifications.feeDetail', {
                      fee: formatFeeDisplay(locale, item.fee_sat, item.fee_msat),
                      rate: feeRate
                    })
                  }
                }
                const memo = typeof item.memo === 'string' ? item.memo.trim() : ''
                const memoLabel = memo && (item.type === 'lightning' || item.type === 'keysend')
                  ? t('notifications.memoLabel', { memo: trimMemo(memo) })
                  : ''
                const detailParts: Array<string | JSX.Element> = [
                  peerLabel,
                  memoLabel,
                ].filter(Boolean)
                if (item.channel_point) {
                  if (item.type === 'channel') {
                    const link = mempoolLinkFromChannelPoint(item.channel_point)
                    detailParts.push(
                      <a
                        key={`${item.id}-channel`}
                        className="text-emerald-200 hover:text-emerald-100"
                        href={link}
                        target="_blank"
                        rel="noopener noreferrer"
                      >
                        {t('notifications.channelLabel', { value: item.channel_point.slice(0, 16) })}
                      </a>
                    )
                  } else {
                    detailParts.push(t('notifications.channelLabel', { value: item.channel_point.slice(0, 16) }))
                  }
                }
                if (item.txid) {
                  if (item.type === 'channel' && !item.channel_point) {
                    const link = mempoolTxLink(item.txid)
                    detailParts.push(
                      <a
                        key={`${item.id}-tx`}
                        className="text-emerald-200 hover:text-emerald-100"
                        href={link}
                        target="_blank"
                        rel="noopener noreferrer"
                      >
                        {t('notifications.txLabel', { value: item.txid.slice(0, 16) })}
                      </a>
                    )
                  } else if (item.type === 'onchain') {
                    const link = mempoolTxLink(item.txid)
                    detailParts.push(
                      <a
                        key={`${item.id}-onchain-tx`}
                        className="text-emerald-200 hover:text-emerald-100"
                        href={link}
                        target="_blank"
                        rel="noopener noreferrer"
                      >
                        {t('notifications.txLabel', { value: item.txid.slice(0, 16) })}
                      </a>
                    )
                  } else {
                    detailParts.push(t('notifications.txLabel', { value: item.txid.slice(0, 16) }))
                  }
                }
                if (feeDetail) {
                  detailParts.push(feeDetail)
                }
                if (item.type === 'rebalance' && item.memo) {
                  detailParts.push(item.memo)
                }
                return (
                  <div key={item.id} className="grid items-center gap-3 border-b border-white/10 pb-3 sm:grid-cols-[160px_1fr_auto_auto]">
                    <span className="text-xs text-fog/50">{formatTimestamp(item.occurred_at)}</span>
                    <div className="min-w-0">
                      <div className="text-sm text-fog">{title}</div>
                      <div className="text-xs text-fog/50">
                        {statusLabel}
                        {detailParts.length > 0 && (
                          <>
                            {' - '}
                            {detailParts.map((part, idx) => (
                              <span key={`${item.id}-detail-${idx}`}>
                                {idx > 0 ? ' - ' : ''}
                                {part}
                              </span>
                            ))}
                          </>
                        )}
                      </div>
                    </div>
                    <span className={`text-xs font-mono ${arrow.tone}`}>{arrow.label}</span>
                    <div className="text-right">
                      <div>{item.amount_sat} sats</div>
                      {formatFeeDisplay(locale, item.fee_sat, item.fee_msat) && (
                        <div
                          className={`text-xs ${
                            item.type === 'forward'
                              ? 'text-emerald-200'
                              : item.type === 'rebalance'
                                ? 'text-ember'
                                : 'text-fog/50'
                          }`}
                        >
                          {t('notifications.feeLabel', { fee: formatFeeDisplay(locale, item.fee_sat, item.fee_msat) })}
                        </div>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          </div>
        )}
      </div>
    </section>
  )
}
