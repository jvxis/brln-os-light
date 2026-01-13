import { useEffect, useMemo, useRef, useState } from 'react'
import { getNotifications, getTelegramBackupConfig, testTelegramBackup, updateTelegramBackupConfig } from '../api'

type Notification = {
  id: number
  occurred_at: string
  type: string
  action: string
  direction: string
  status: string
  amount_sat: number
  fee_sat: number
  peer_pubkey?: string
  peer_alias?: string
  channel_id?: number
  channel_point?: string
  txid?: string
  payment_hash?: string
  memo?: string
}

type TelegramBackupConfig = {
  chat_id?: string
  bot_token_set?: boolean
}

const formatTimestamp = (value: string) => {
  if (!value) return 'Unknown time'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return 'Unknown time'
  return parsed.toLocaleString('en-US', {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  })
}

const capitalize = (value: string) => {
  if (!value) return ''
  return value.charAt(0).toUpperCase() + value.slice(1)
}

const labelForType = (value: string) => {
  switch (value) {
    case 'onchain':
      return 'On-chain'
    case 'lightning':
      return 'Lightning'
    case 'channel':
      return 'Channel'
    case 'forward':
      return 'Forward'
    case 'rebalance':
      return 'Rebalance'
    default:
      return capitalize(value)
  }
}

const labelForAction = (value: string) => {
  switch (value) {
    case 'receive':
      return 'received'
    case 'send':
      return 'sent'
    case 'open':
      return 'opened'
    case 'close':
      return 'closed'
    case 'opening':
      return 'opening'
    case 'forwarded':
      return 'forwarded'
    case 'rebalanced':
      return 'rebalanced'
    default:
      return value
  }
}

const normalizeStatus = (value: string) => {
  if (!value) return 'UNKNOWN'
  return value.replace(/_/g, ' ').toUpperCase()
}

const arrowForDirection = (value: string) => {
  if (value === 'in') return { label: '<-', tone: 'text-glow' }
  if (value === 'out') return { label: '->', tone: 'text-ember' }
  return { label: '.', tone: 'text-fog/50' }
}

const formatFeeRate = (amount: number, fee: number) => {
  if (!amount || amount <= 0) return ''
  const safeFee = fee > 0 ? fee : 0
  const ratio = safeFee / amount
  const percentRaw = ratio * 100
  const percent = percentRaw.toFixed(3).replace(/\.?0+$/, '')
  const ppm = Math.round(ratio * 1_000_000)
  return `${percent}% ${ppm}ppm`
}

export default function Notifications() {
  const [items, setItems] = useState<Notification[]>([])
  const [status, setStatus] = useState('Loading notifications...')
  const [streamState, setStreamState] = useState<'idle' | 'waiting' | 'reconnecting' | 'error'>('idle')
  const streamErrors = useRef(0)
  const [filter, setFilter] = useState<'all' | 'onchain' | 'lightning' | 'channel' | 'forward' | 'rebalance'>('all')
  const [telegramConfig, setTelegramConfig] = useState<TelegramBackupConfig | null>(null)
  const [telegramToken, setTelegramToken] = useState('')
  const [telegramChatId, setTelegramChatId] = useState('')
  const [telegramStatus, setTelegramStatus] = useState('')
  const [telegramSaving, setTelegramSaving] = useState(false)
  const [telegramTesting, setTelegramTesting] = useState(false)
  const [telegramOpen, setTelegramOpen] = useState(false)

  useEffect(() => {
    let mounted = true
    const load = async () => {
      setStatus('Loading notifications...')
      try {
        const res = await getNotifications(200)
        if (!mounted) return
        setItems(Array.isArray(res?.items) ? res.items : [])
        setStatus('')
      } catch (err: any) {
        if (!mounted) return
        setStatus(err?.message || 'Notifications unavailable')
      }
    }
    load()
    return () => {
      mounted = false
    }
  }, [])

  useEffect(() => {
    let mounted = true
    getTelegramBackupConfig()
      .then((data: TelegramBackupConfig) => {
        if (!mounted) return
        setTelegramConfig(data)
        setTelegramChatId(data?.chat_id || '')
        setTelegramToken('')
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
          return next.slice(0, 200)
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
    const base = filter === 'all' ? items : items.filter((item) => item.type === filter)
    return base.filter((item) => {
      if (item.type === 'rebalance') return true
      if (!item.payment_hash) return true
      return !rebalanceHashes.has(item.payment_hash)
    })
  }, [filter, items, rebalanceHashes])

  const telegramEnabled = Boolean(telegramConfig?.bot_token_set && telegramConfig?.chat_id)

  const triggerTelegramTest = async (startingMessage?: string, force?: boolean) => {
    if (telegramTesting) return
    if (!force && !telegramEnabled) {
      setTelegramStatus('Configure Telegram first.')
      return
    }
    if (startingMessage) {
      setTelegramStatus(startingMessage)
    } else {
      setTelegramStatus('Sending test message...')
    }
    setTelegramTesting(true)
    try {
      await testTelegramBackup()
      setTelegramStatus('Telegram test sent.')
    } catch (err: any) {
      setTelegramStatus(err?.message || 'Failed to send Telegram test.')
    } finally {
      setTelegramTesting(false)
    }
  }

  const handleSaveTelegram = async () => {
    if (telegramSaving) return
    setTelegramSaving(true)
    setTelegramStatus('Saving...')
    try {
      await updateTelegramBackupConfig({
        bot_token: telegramToken,
        chat_id: telegramChatId
      })
      const data: TelegramBackupConfig = await getTelegramBackupConfig()
      setTelegramConfig(data)
      setTelegramChatId(data?.chat_id || '')
      setTelegramToken('')
      if (!data?.bot_token_set && !data?.chat_id) {
        setTelegramStatus('Telegram backup disabled.')
      } else {
        const nextEnabled = Boolean(data?.bot_token_set && data?.chat_id)
        if (nextEnabled) {
          await triggerTelegramTest('Telegram backup saved. Sending test...', true)
        } else {
          setTelegramStatus('Telegram backup saved.')
        }
      }
    } catch (err: any) {
      setTelegramStatus(err?.message || 'Failed to save Telegram backup.')
    } finally {
      setTelegramSaving(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">Notifications</h2>
            <p className="text-fog/60">Real-time LND activity and history.</p>
          </div>
          <div className="flex flex-wrap gap-2 text-xs">
            <button className={filter === 'all' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('all')}>All</button>
            <button className={filter === 'onchain' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('onchain')}>On-chain</button>
            <button className={filter === 'lightning' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('lightning')}>Lightning</button>
            <button className={filter === 'channel' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('channel')}>Channels</button>
            <button className={filter === 'forward' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('forward')}>Forwards</button>
            <button className={filter === 'rebalance' ? 'btn-primary' : 'btn-secondary'} onClick={() => setFilter('rebalance')}>Rebalance</button>
          </div>
        </div>
      </div>

      <div className="section-card">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">Telegram SCB backup</h3>
            <p className="text-fog/60">Send a static channel backup on every channel open or close.</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-xs ${telegramEnabled ? 'text-glow' : 'text-fog/60'}`}>
              {telegramEnabled ? 'Enabled' : 'Disabled'}
            </span>
            <button className="btn-secondary" type="button" onClick={() => setTelegramOpen((prev) => !prev)}>
              {telegramOpen ? 'Hide' : 'Configure'}
            </button>
          </div>
        </div>
        {telegramOpen && (
          <div className="mt-4 space-y-4">
            <div className="grid gap-4 lg:grid-cols-2">
              <div className="space-y-2">
                <label className="text-sm text-fog/70">Bot token</label>
                <input
                  className="input-field"
                  type="password"
                  placeholder={telegramConfig?.bot_token_set ? 'Token saved' : '123456:ABC...'}
                  value={telegramToken}
                  onChange={(e) => setTelegramToken(e.target.value)}
                />
                <p className="text-xs text-fog/50">Get the bot token from @BotFather.</p>
              </div>
              <div className="space-y-2">
                <label className="text-sm text-fog/70">Chat ID</label>
                <input
                  className="input-field"
                  placeholder="123456789"
                  value={telegramChatId}
                  onChange={(e) => setTelegramChatId(e.target.value)}
                />
                <p className="text-xs text-fog/50">Find your chat id via @userinfobot. Start a chat with your bot first.</p>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <button className="btn-primary" onClick={handleSaveTelegram} disabled={telegramSaving}>
                {telegramSaving ? 'Saving...' : 'Save Telegram backup'}
              </button>
              <button
                className="btn-secondary"
                onClick={() => triggerTelegramTest()}
                disabled={telegramTesting || !telegramEnabled}
              >
                {telegramTesting ? 'Sending test...' : 'Send test'}
              </button>
              {telegramStatus && <span className="text-sm text-brass">{telegramStatus}</span>}
            </div>
            <p className="text-xs text-fog/50">Direct chat only. Leave both fields empty to disable Telegram backup.</p>
          </div>
        )}
      </div>

      <div className="section-card">
        <h3 className="text-lg font-semibold">Recent activity</h3>
        {status && <p className="mt-4 text-sm text-fog/60">{status}</p>}
        {!status && streamState === 'reconnecting' && (
          <p className="mt-2 text-sm text-brass">Reconnecting live updates...</p>
        )}
        {!status && streamState === 'error' && (
          <p className="mt-2 text-sm text-brass">Live updates unavailable.</p>
        )}
        {!status && streamState === 'waiting' && filtered.length === 0 && (
          <p className="mt-2 text-sm text-fog/60">Waiting for events...</p>
        )}
        {!status && !filtered.length && (
          <p className="mt-4 text-sm text-fog/60">No notifications yet.</p>
        )}
        {filtered.length > 0 && (
          <div className="mt-4 max-h-[520px] overflow-y-auto pr-2">
            <div className="space-y-2 text-sm">
              {filtered.map((item) => {
                const arrow = arrowForDirection(item.direction)
                const title = `${labelForType(item.type)} ${labelForAction(item.action)}`
                const statusLabel = normalizeStatus(item.status)
                const peer = item.peer_alias || (item.peer_pubkey ? item.peer_pubkey.slice(0, 16) : '')
                const feeRate = formatFeeRate(item.amount_sat, item.fee_sat)
                let feeDetail = ''
                if (feeRate) {
                  if (item.type === 'forward') {
                    feeDetail = `Earned ${item.fee_sat} sats (${feeRate})`
                  } else if (item.type === 'rebalance') {
                    feeDetail = `Fee ${item.fee_sat} sats (${feeRate})`
                  }
                }
                const detail = [
                  peer ? `Peer ${peer}` : '',
                  item.channel_point ? `Channel ${item.channel_point.slice(0, 16)}...` : '',
                  item.txid ? `Tx ${item.txid.slice(0, 16)}...` : '',
                  feeDetail,
                ].filter(Boolean).join(' - ')
                return (
                  <div key={item.id} className="grid items-center gap-3 border-b border-white/10 pb-3 sm:grid-cols-[160px_1fr_auto_auto]">
                    <span className="text-xs text-fog/50">{formatTimestamp(item.occurred_at)}</span>
                    <div className="min-w-0">
                      <div className="text-sm text-fog">{title}</div>
                      <div className="text-xs text-fog/50">{statusLabel}{detail ? ` - ${detail}` : ''}</div>
                    </div>
                    <span className={`text-xs font-mono ${arrow.tone}`}>{arrow.label}</span>
                    <div className="text-right">
                      <div>{item.amount_sat} sats</div>
                      {item.fee_sat > 0 && (
                        <div className="text-xs text-fog/50">Fee {item.fee_sat} sats</div>
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
