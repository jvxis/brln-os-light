import { useEffect, useMemo, useState } from 'react'
import { getNotifications } from '../api'

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

export default function Notifications() {
  const [items, setItems] = useState<Notification[]>([])
  const [status, setStatus] = useState('Loading notifications...')
  const [streamState, setStreamState] = useState<'idle' | 'waiting' | 'error'>('idle')
  const [filter, setFilter] = useState<'all' | 'onchain' | 'lightning' | 'channel' | 'forward' | 'rebalance'>('all')

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
    const stream = new EventSource('/api/notifications/stream')
    const markWaiting = () => setStreamState('waiting')
    stream.onopen = markWaiting
    stream.addEventListener('ready', markWaiting)
    stream.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data)
        if (!payload || !payload.id) return
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
      setStreamState('error')
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
        <h3 className="text-lg font-semibold">Recent activity</h3>
        {status && <p className="mt-4 text-sm text-fog/60">{status}</p>}
        {!status && streamState === 'error' && (
          <p className="mt-2 text-sm text-brass">Live updates unavailable.</p>
        )}
        {!status && streamState === 'waiting' && filtered.length === 0 && (
          <p className="mt-2 text-sm text-fog/60">Waiting for events...</p>
        )}
        {!status && !filtered.length && (
          <p className="mt-4 text-sm text-fog/60">No notifications yet.</p>
        )}
        <div className="mt-4 space-y-2 text-sm">
          {filtered.map((item) => {
            const arrow = arrowForDirection(item.direction)
            const title = `${labelForType(item.type)} ${labelForAction(item.action)}`
            const statusLabel = normalizeStatus(item.status)
            const peer = item.peer_alias || (item.peer_pubkey ? item.peer_pubkey.slice(0, 16) : '')
            const detail = [
              peer ? `Peer ${peer}` : '',
              item.channel_point ? `Channel ${item.channel_point.slice(0, 16)}...` : '',
              item.txid ? `Tx ${item.txid.slice(0, 16)}...` : '',
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
    </section>
  )
}
