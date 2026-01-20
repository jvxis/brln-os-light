import { useEffect, useMemo, useRef, useState } from 'react'
import { getChatInbox, getChatMessages, getLnPeers, sendChatMessage } from '../api'

type Peer = {
  pub_key: string
  alias: string
  address: string
  inbound: boolean
}

type ChatMessage = {
  timestamp: string
  peer_pubkey: string
  direction: 'in' | 'out'
  message: string
  status: string
  payment_hash?: string
}

type ChatInboxItem = {
  peer_pubkey: string
  last_inbound_at: string
}

const messageLimit = 500
const lastReadKey = 'chat:lastRead'

const formatTimestamp = (value: string) => {
  if (!value) return ''
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return ''
  return parsed.toLocaleString('en-US', {
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  })
}

export default function Chat() {
  const [peers, setPeers] = useState<Peer[]>([])
  const [peerStatus, setPeerStatus] = useState('')
  const [selectedPeer, setSelectedPeer] = useState<Peer | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [inboxItems, setInboxItems] = useState<ChatInboxItem[]>([])
  const [lastReadMap, setLastReadMap] = useState<Record<string, number>>(() => {
    try {
      const raw = localStorage.getItem(lastReadKey)
      if (!raw) return {}
      const parsed = JSON.parse(raw)
      if (parsed && typeof parsed === 'object') {
        return parsed
      }
      return {}
    } catch {
      return {}
    }
  })
  const [messageStatus, setMessageStatus] = useState('')
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [loadingMessages, setLoadingMessages] = useState(false)
  const bottomRef = useRef<HTMLDivElement | null>(null)

  const loadPeers = async () => {
    setPeerStatus('Loading peers...')
    try {
      const res = await getLnPeers()
      setPeers(Array.isArray(res?.peers) ? res.peers : [])
      setPeerStatus('')
    } catch (err: any) {
      setPeerStatus(err?.message || 'Failed to load peers.')
    }
  }

  useEffect(() => {
    let mounted = true
    const load = async () => {
      if (!mounted) return
      await loadPeers()
    }
    load()
    const timer = window.setInterval(loadPeers, 20000)
    return () => {
      mounted = false
      window.clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    let mounted = true
    const loadInbox = async () => {
      try {
        const res = await getChatInbox()
        if (!mounted) return
        setInboxItems(Array.isArray(res?.items) ? res.items : [])
      } catch {
        if (!mounted) return
        setInboxItems([])
      }
    }
    loadInbox()
    const timer = window.setInterval(loadInbox, 12000)
    return () => {
      mounted = false
      window.clearInterval(timer)
    }
  }, [])

  useEffect(() => {
    if (!selectedPeer) {
      setMessages([])
      setMessageStatus('')
      return
    }

    let mounted = true
    const load = async () => {
      setLoadingMessages(true)
      try {
        const res = await getChatMessages(selectedPeer.pub_key)
        if (!mounted) return
        setMessages(Array.isArray(res?.items) ? res.items : [])
        setMessageStatus('')
      } catch (err: any) {
        if (!mounted) return
        setMessageStatus(err?.message || 'Failed to load messages.')
      } finally {
        if (!mounted) return
        setLoadingMessages(false)
      }
    }
    load()
    const timer = window.setInterval(load, 12000)
    return () => {
      mounted = false
      window.clearInterval(timer)
    }
  }, [selectedPeer?.pub_key])

  useEffect(() => {
    if (!selectedPeer) return
    let latest = 0
    for (const msg of messages) {
      if (msg.direction !== 'in') continue
      const time = new Date(msg.timestamp).getTime()
      if (!Number.isNaN(time)) {
        latest = Math.max(latest, time)
      }
    }
    if (!latest) return
    if ((lastReadMap[selectedPeer.pub_key] || 0) >= latest) return
    const next = { ...lastReadMap, [selectedPeer.pub_key]: latest }
    setLastReadMap(next)
    try {
      localStorage.setItem(lastReadKey, JSON.stringify(next))
    } catch {
      // ignore storage errors
    }
  }, [messages, selectedPeer?.pub_key, lastReadMap])

  useEffect(() => {
    if (!bottomRef.current) return
    bottomRef.current.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  useEffect(() => {
    if (!selectedPeer) return
    const match = peers.find((peer) => peer.pub_key === selectedPeer.pub_key)
    if (match) {
      setSelectedPeer(match)
    }
  }, [peers, selectedPeer?.pub_key])

  const selectedOnline = useMemo(() => {
    if (!selectedPeer) return false
    return peers.some((peer) => peer.pub_key === selectedPeer.pub_key)
  }, [peers, selectedPeer])

  const onlinePeerSet = useMemo(() => new Set(peers.map((peer) => peer.pub_key)), [peers])

  const unreadPeers = useMemo(() => {
    const unread = new Set<string>()
    for (const item of inboxItems) {
      if (!onlinePeerSet.has(item.peer_pubkey)) {
        continue
      }
      const ts = new Date(item.last_inbound_at).getTime()
      if (!ts || Number.isNaN(ts)) continue
      const lastRead = lastReadMap[item.peer_pubkey] || 0
      if (ts > lastRead) {
        unread.add(item.peer_pubkey)
      }
    }
    return unread
  }, [inboxItems, lastReadMap, onlinePeerSet])

  const unreadCount = unreadPeers.size

  const sortedPeers = useMemo(() => {
    const list = [...peers]
    list.sort((a, b) => {
      const aVal = (a.alias || a.pub_key).toLowerCase()
      const bVal = (b.alias || b.pub_key).toLowerCase()
      return aVal.localeCompare(bVal)
    })
    return list
  }, [peers])

  const overLimit = draft.trim().length > messageLimit
  const canSend = Boolean(selectedPeer && selectedOnline && draft.trim() && !overLimit && !sending)

  const handleSend = async () => {
    if (!selectedPeer || !canSend) return
    const trimmed = draft.trim()
    setSending(true)
    setMessageStatus('Sending...')
    try {
      const res = await sendChatMessage({ peer_pubkey: selectedPeer.pub_key, message: trimmed })
      setDraft('')
      setMessages((prev) => [...prev, res])
      setMessageStatus('')
    } catch (err: any) {
      setMessageStatus(err?.message || 'Failed to send message.')
    } finally {
      setSending(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">Chat</h2>
            <p className="text-fog/60">Keysend messages to connected peers.</p>
          </div>
          <button className="btn-secondary text-xs px-3 py-2" onClick={loadPeers}>
            Refresh peers
          </button>
        </div>
        {peerStatus && <p className="mt-3 text-sm text-brass">{peerStatus}</p>}
      </div>

      <div className="grid gap-6 lg:grid-cols-[320px_1fr]">
        <div className="section-card space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">Online peers</h3>
            <span className="text-xs text-fog/60">{peers.length}</span>
          </div>
          {sortedPeers.length ? (
            <div className="space-y-2 max-h-[520px] overflow-y-auto pr-2">
              {sortedPeers.map((peer) => (
                <button
                  key={peer.pub_key}
                  type="button"
                  onClick={() => setSelectedPeer(peer)}
                  className={`w-full text-left rounded-2xl border px-4 py-3 transition ${
                    selectedPeer?.pub_key === peer.pub_key
                      ? 'border-glow/40 bg-glow/10'
                      : unreadPeers.has(peer.pub_key)
                        ? 'border-brass/40 bg-brass/10'
                        : 'border-white/10 bg-ink/60 hover:border-white/30'
                  }`}
                >
                  <div className="flex items-center justify-between gap-2 text-sm text-fog break-all">
                    <span>{peer.alias || peer.pub_key}</span>
                    {unreadPeers.has(peer.pub_key) && (
                      <span className="rounded-full bg-brass/20 px-2 py-0.5 text-[10px] uppercase tracking-wide text-brass">
                        New
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-fog/50 break-all">{peer.pub_key}</div>
                </button>
              ))}
            </div>
          ) : (
            <p className="text-sm text-fog/60">No online peers available.</p>
          )}
        </div>

        <div className="section-card flex flex-col gap-4 max-h-[720px]">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h3 className="text-lg font-semibold">
                {selectedPeer ? `Chat with ${selectedPeer.alias || selectedPeer.pub_key}` : 'Select a peer'}
              </h3>
              <p className="text-xs text-fog/60">
                {selectedPeer ? (selectedOnline ? 'Peer online' : 'Peer offline') : 'Choose an online peer to start.'}
              </p>
            </div>
            {selectedPeer && (
              <span className="text-xs text-fog/60 break-all">{selectedPeer.pub_key}</span>
            )}
          </div>

          {unreadCount > 0 && (
            <div className="rounded-2xl border border-brass/30 bg-brass/10 px-4 py-2 text-xs text-brass">
              {unreadCount === 1 ? '1 unread chat' : `${unreadCount} unread chats`} waiting. Open a highlighted peer to mark as read.
            </div>
          )}

          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 text-xs text-fog/70">
            Keysend chat costs 1 sat per message + routing fees. Messages expire after 30 days.
          </div>

          <div className="flex-1 min-h-[280px] overflow-y-auto space-y-3 pr-2">
            {loadingMessages && <p className="text-sm text-fog/60">Loading messages...</p>}
            {!loadingMessages && !messages.length && (
              <p className="text-sm text-fog/60">No messages yet.</p>
            )}
            {messages.map((msg, idx) => (
              <div key={`${msg.payment_hash || idx}`} className={`flex ${msg.direction === 'out' ? 'justify-end' : 'justify-start'}`}>
                <div
                  className={`max-w-[75%] rounded-2xl border px-4 py-3 text-sm ${
                    msg.direction === 'out'
                      ? 'border-glow/30 bg-glow/20 text-fog'
                      : 'border-white/10 bg-white/10 text-fog'
                  }`}
                >
                  <div className="whitespace-pre-wrap break-words">{msg.message}</div>
                  <div className="mt-2 flex items-center justify-between text-[11px] text-fog/50">
                    <span>{formatTimestamp(msg.timestamp)}</span>
                    {msg.direction === 'out' && <span>{msg.status}</span>}
                  </div>
                </div>
              </div>
            ))}
            <div ref={bottomRef} />
          </div>

          {messageStatus && <p className="text-sm text-brass">{messageStatus}</p>}

          <div className="space-y-3">
            <textarea
              className="input-field min-h-[96px]"
              placeholder={selectedPeer ? 'Write a message...' : 'Select an online peer to chat.'}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              disabled={!selectedPeer || !selectedOnline}
            />
            <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-fog/60">
              <span>{draft.trim().length}/{messageLimit}</span>
              <button className="btn-primary" onClick={handleSend} disabled={!canSend}>
                {sending ? 'Sending...' : 'Send 1 sat'}
              </button>
            </div>
            {overLimit && (
              <p className="text-xs text-ember">Message exceeds {messageLimit} characters.</p>
            )}
          </div>
        </div>
      </div>
    </section>
  )
}
