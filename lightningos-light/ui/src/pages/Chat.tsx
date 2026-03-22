import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { connectPeer, getChatInbox, getChatMessages, getLnPeers, sendChatMessage } from '../api'
import { getLocale } from '../i18n'

type Peer = {
  pub_key: string
  alias: string
  address: string
  inbound: boolean
}

type ChatPeer = Peer & {
  last_inbound_at?: number
  last_message_at?: number
  last_message?: string
  last_message_direction?: 'in' | 'out'
}

type ChatMessage = {
  timestamp: string
  peer_pubkey: string
  peer_alias?: string
  direction: 'in' | 'out'
  message: string
  status: string
  payment_hash?: string
}

type ChatInboxItem = {
  peer_pubkey: string
  peer_alias?: string
  last_inbound_at: string
  last_message_at?: string
  last_message?: string
  last_message_direction?: 'in' | 'out'
}

type ParsedInboxItem = {
  peer_pubkey: string
  peer_alias: string
  last_inbound_at: number
  last_message_at: number
  last_message: string
  last_message_direction?: 'in' | 'out'
}

const messageLimit = 500
const lastReadKey = 'chat:lastRead'

const formatTimestamp = (value: string | number | undefined, locale: string) => {
  if (!value) return ''
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return ''
  return parsed.toLocaleString(locale, {
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false
  })
}

export default function Chat() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [peers, setPeers] = useState<Peer[]>([])
  const [peerStatus, setPeerStatus] = useState('')
  const [selectedPeer, setSelectedPeer] = useState<ChatPeer | null>(null)
  const [peerQuery, setPeerQuery] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [inboxItems, setInboxItems] = useState<ChatInboxItem[]>([])
  const [connectAddress, setConnectAddress] = useState('')
  const [connectTemporary, setConnectTemporary] = useState(false)
  const [connectStatus, setConnectStatus] = useState('')
  const [connectingPeer, setConnectingPeer] = useState(false)
  const [showConnectForm, setShowConnectForm] = useState(false)
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
    setPeerStatus(t('chat.loadingPeers'))
    try {
      const res = await getLnPeers()
      const items = Array.isArray(res?.peers) ? res.peers : []
      setPeers(items)
      setPeerStatus('')
      return items as Peer[]
    } catch (err: any) {
      setPeerStatus(err?.message || t('chat.loadPeersFailed'))
      return [] as Peer[]
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
        setMessageStatus(err?.message || t('chat.loadMessagesFailed'))
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
  }, [selectedPeer?.pub_key, t])

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

  const onlinePeerSet = useMemo(() => new Set(peers.map((peer) => peer.pub_key)), [peers])

  const parsedInboxItems = useMemo(() => {
    return inboxItems
      .map((item) => {
        const peerPubkey = item.peer_pubkey.trim()
        const lastInboundAt = new Date(item.last_inbound_at).getTime()
        const rawLastMessageAt = item.last_message_at ? new Date(item.last_message_at).getTime() : lastInboundAt
        if (!peerPubkey || Number.isNaN(lastInboundAt) || !lastInboundAt) {
          return null
        }
        return {
          peer_pubkey: peerPubkey,
          peer_alias: (item.peer_alias || '').trim(),
          last_inbound_at: lastInboundAt,
          last_message_at: !Number.isNaN(rawLastMessageAt) && rawLastMessageAt ? rawLastMessageAt : lastInboundAt,
          last_message: (item.last_message || '').trim(),
          last_message_direction: item.last_message_direction,
        } as ParsedInboxItem
      })
      .filter((item): item is ParsedInboxItem => item !== null)
  }, [inboxItems])

  const inboxByPeer = useMemo(() => {
    const map = new Map<string, (typeof parsedInboxItems)[number]>()
    for (const item of parsedInboxItems) {
      map.set(item.peer_pubkey, item)
    }
    return map
  }, [parsedInboxItems])

  useEffect(() => {
    if (!selectedPeer) return
    const onlineMatch = peers.find((peer) => peer.pub_key === selectedPeer.pub_key)
    const inboxMatch = inboxByPeer.get(selectedPeer.pub_key)
    if (onlineMatch) {
      setSelectedPeer({
        ...onlineMatch,
        last_inbound_at: inboxMatch?.last_inbound_at,
        last_message_at: inboxMatch?.last_message_at,
        last_message: inboxMatch?.last_message,
        last_message_direction: inboxMatch?.last_message_direction,
      })
      return
    }
    if (inboxMatch) {
      setSelectedPeer((current) => current ? {
        ...current,
        alias: current.alias || inboxMatch.peer_alias,
        last_inbound_at: inboxMatch.last_inbound_at,
        last_message_at: inboxMatch.last_message_at,
        last_message: inboxMatch.last_message,
        last_message_direction: inboxMatch.last_message_direction,
      } : current)
    }
  }, [inboxByPeer, peers, selectedPeer?.pub_key])

  const selectedOnline = useMemo(() => {
    if (!selectedPeer) return false
    return onlinePeerSet.has(selectedPeer.pub_key)
  }, [onlinePeerSet, selectedPeer])

  const unreadPeers = useMemo(() => {
    const unread = new Set<string>()
    for (const item of parsedInboxItems) {
      const lastRead = lastReadMap[item.peer_pubkey] || 0
      if (item.last_inbound_at > lastRead) {
        unread.add(item.peer_pubkey)
      }
    }
    return unread
  }, [parsedInboxItems, lastReadMap])

  const unreadCount = unreadPeers.size

  const sortedPeers = useMemo(() => {
    const list = peers.map((peer) => {
      const inbox = inboxByPeer.get(peer.pub_key)
      return {
        ...peer,
        last_inbound_at: inbox?.last_inbound_at,
        last_message_at: inbox?.last_message_at,
        last_message: inbox?.last_message,
        last_message_direction: inbox?.last_message_direction,
      } as ChatPeer
    })
    list.sort((a, b) => {
      const aUnread = unreadPeers.has(a.pub_key)
      const bUnread = unreadPeers.has(b.pub_key)
      if (aUnread !== bUnread) {
        return aUnread ? -1 : 1
      }
      if ((a.last_message_at || 0) !== (b.last_message_at || 0)) {
        return (b.last_message_at || 0) - (a.last_message_at || 0)
      }
      const aVal = (a.alias || a.pub_key).toLowerCase()
      const bVal = (b.alias || b.pub_key).toLowerCase()
      return aVal.localeCompare(bVal)
    })
    return list
  }, [inboxByPeer, peers, unreadPeers])

  const recentPeers = useMemo(() => {
    return parsedInboxItems
      .filter((item) => !onlinePeerSet.has(item.peer_pubkey))
      .map((item) => ({
        pub_key: item.peer_pubkey,
        alias: item.peer_alias,
        address: '',
        inbound: false,
        last_inbound_at: item.last_inbound_at,
        last_message_at: item.last_message_at,
        last_message: item.last_message,
        last_message_direction: item.last_message_direction,
      } satisfies ChatPeer))
      .sort((a, b) => {
        const aUnread = unreadPeers.has(a.pub_key)
        const bUnread = unreadPeers.has(b.pub_key)
        if (aUnread !== bUnread) {
          return aUnread ? -1 : 1
        }
        if ((a.last_message_at || 0) !== (b.last_message_at || 0)) {
          return (b.last_message_at || 0) - (a.last_message_at || 0)
        }
        const aVal = (a.alias || a.pub_key).toLowerCase()
        const bVal = (b.alias || b.pub_key).toLowerCase()
        return aVal.localeCompare(bVal)
      })
  }, [onlinePeerSet, parsedInboxItems, unreadPeers])

  const filteredOnlinePeers = useMemo(() => {
    const query = peerQuery.trim().toLowerCase()
    if (!query) return sortedPeers
    return sortedPeers.filter((peer) => {
      const alias = (peer.alias || '').toLowerCase()
      const key = peer.pub_key.toLowerCase()
      const preview = (peer.last_message || '').toLowerCase()
      return alias.includes(query) || key.includes(query) || preview.includes(query)
    })
  }, [peerQuery, sortedPeers])

  const filteredRecentPeers = useMemo(() => {
    const query = peerQuery.trim().toLowerCase()
    if (!query) return recentPeers
    return recentPeers.filter((peer) => {
      const alias = (peer.alias || '').toLowerCase()
      const key = peer.pub_key.toLowerCase()
      const preview = (peer.last_message || '').toLowerCase()
      return alias.includes(query) || key.includes(query) || preview.includes(query)
    })
  }, [peerQuery, recentPeers])

  const overLimit = draft.trim().length > messageLimit
  const canSend = Boolean(selectedPeer && selectedOnline && draft.trim() && !overLimit && !sending)

  const handleSend = async () => {
    if (!selectedPeer || !canSend) return
    const trimmed = draft.trim()
    setSending(true)
    setMessageStatus(t('chat.sending'))
    try {
      const res = await sendChatMessage({ peer_pubkey: selectedPeer.pub_key, message: trimmed })
      setDraft('')
      setMessages((prev) => [...prev, res])
      setInboxItems((prev) => {
        const existing = prev.find((item) => item.peer_pubkey === selectedPeer.pub_key)
        const nextItem: ChatInboxItem = {
          peer_pubkey: selectedPeer.pub_key,
          peer_alias: selectedPeer.alias,
          last_inbound_at: existing?.last_inbound_at || res.timestamp,
          last_message_at: res.timestamp,
          last_message: trimmed,
          last_message_direction: 'out',
        }
        const rest = prev.filter((item) => item.peer_pubkey !== selectedPeer.pub_key)
        return [nextItem, ...rest]
      })
      setMessageStatus('')
    } catch (err: any) {
      setMessageStatus(err?.message || t('chat.sendFailed'))
    } finally {
      setSending(false)
    }
  }

  const handleConnectPeer = async () => {
    const address = connectAddress.trim()
    if (!address || connectingPeer) return
    const pubkey = address.includes('@') ? address.split('@')[0].trim().toLowerCase() : ''

    setConnectingPeer(true)
    setConnectStatus(t('lightningOps.connectingPeer'))
    try {
      await connectPeer({ address, perm: !connectTemporary })
      const nextPeers = await loadPeers()
      if (pubkey) {
        const match = nextPeers.find((peer) => peer.pub_key.toLowerCase() === pubkey)
        const inbox = inboxByPeer.get(pubkey)
        if (match) {
          setSelectedPeer({
            ...match,
            last_inbound_at: inbox?.last_inbound_at,
            last_message_at: inbox?.last_message_at,
            last_message: inbox?.last_message,
            last_message_direction: inbox?.last_message_direction,
          })
        }
      }
      setConnectAddress('')
      setConnectTemporary(false)
      setShowConnectForm(false)
      setConnectStatus(t('lightningOps.peerConnected'))
    } catch (err: any) {
      setShowConnectForm(true)
      setConnectStatus(err?.message || t('lightningOps.peerConnectFailed'))
    } finally {
      setConnectingPeer(false)
    }
  }

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">{t('chat.title')}</h2>
            <p className="text-fog/60">{t('chat.subtitle')}</p>
          </div>
          <button className="btn-secondary text-xs px-3 py-2" onClick={loadPeers}>
            {t('common.refresh')}
          </button>
        </div>
        {peerStatus && <p className="mt-3 text-sm text-brass">{peerStatus}</p>}
      </div>

      <div className="grid items-start gap-4 lg:grid-cols-[minmax(320px,350px)_minmax(0,1fr)] xl:grid-cols-[minmax(340px,370px)_minmax(0,1fr)]">
        <div className="section-card flex min-h-0 flex-col gap-4 lg:h-[720px]">
          <div className="space-y-3">
            <button
              type="button"
              onClick={() => setShowConnectForm((current) => !current)}
              className="flex w-full items-center justify-between rounded-2xl border border-white/10 bg-ink/40 px-4 py-3 text-left transition hover:border-white/20"
            >
              <span className="text-sm font-semibold uppercase tracking-wide text-fog/80">
                {t('lightningOps.addPeer')}
              </span>
              <span className="text-[11px] uppercase tracking-wide text-fog/50">
                {showConnectForm ? t('common.hide') : t('common.open')}
              </span>
            </button>
            {showConnectForm && (
              <div className="rounded-2xl border border-white/10 bg-ink/60 p-4 space-y-3">
                <input
                  className="input-field text-sm"
                  placeholder={t('lightningOps.peerAddressPlaceholder')}
                  value={connectAddress}
                  onChange={(e) => setConnectAddress(e.target.value)}
                />
                <label className="flex items-center gap-2 text-xs text-fog/70">
                  <input
                    type="checkbox"
                    checked={connectTemporary}
                    onChange={(e) => setConnectTemporary(e.target.checked)}
                  />
                  {t('lightningOps.temporaryPeer')}
                </label>
                <button
                  className="btn-secondary w-full disabled:cursor-not-allowed disabled:opacity-60"
                  type="button"
                  onClick={handleConnectPeer}
                  disabled={!connectAddress.trim() || connectingPeer}
                >
                  {connectingPeer ? t('lightningOps.connectingPeer') : t('lightningOps.connectPeer')}
                </button>
              </div>
            )}
            {connectStatus && <p className="text-xs text-brass">{connectStatus}</p>}
          </div>

          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold">{t('chat.title')}</h3>
            <span className="text-xs text-fog/60">{peers.length}</span>
          </div>
          {(sortedPeers.length || recentPeers.length) ? (
            <>
              <input
                className="input-field text-sm"
                placeholder={t('chat.searchPeers')}
                value={peerQuery}
                onChange={(e) => setPeerQuery(e.target.value)}
              />
              <div className="flex-1 min-h-0 space-y-4 overflow-y-auto pr-2">
                <div className="space-y-2">
                  <div className="flex items-center justify-between text-[11px] uppercase tracking-wide text-fog/50">
                    <span>{t('chat.onlinePeers')}</span>
                    <span>{peers.length}</span>
                  </div>
                  {filteredOnlinePeers.map((peer) => (
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
                      <div className="mt-2 text-[11px] text-fog/55 break-words">
                        {peer.last_message || peer.pub_key}
                      </div>
                      <div className="mt-2 flex items-center justify-between gap-2 text-[10px] text-fog/45">
                        <span className="truncate">{peer.pub_key}</span>
                        <span className="shrink-0">{formatTimestamp(peer.last_message_at, locale)}</span>
                      </div>
                    </button>
                  ))}
                  {!sortedPeers.length && (
                    <p className="text-sm text-fog/60">{t('chat.noOnlinePeers')}</p>
                  )}
                </div>

                {recentPeers.length > 0 && (
                  <div className="space-y-2">
                    <div className="flex items-center justify-between text-[11px] uppercase tracking-wide text-fog/50">
                      <span>{t('chat.recentMessages')}</span>
                      <span>{recentPeers.length}</span>
                    </div>
                    {filteredRecentPeers.map((peer) => (
                      <button
                        key={peer.pub_key}
                        type="button"
                        onClick={() => setSelectedPeer(peer)}
                        className={`w-full text-left rounded-2xl border px-4 py-3 transition ${
                          selectedPeer?.pub_key === peer.pub_key
                            ? 'border-glow/40 bg-glow/10'
                            : unreadPeers.has(peer.pub_key)
                              ? 'border-brass/40 bg-brass/10'
                              : 'border-white/10 bg-ink/40 hover:border-white/30'
                        }`}
                      >
                        <div className="flex items-center justify-between gap-2 text-sm text-fog break-all">
                          <span>{peer.alias || peer.pub_key}</span>
                          <span className="rounded-full bg-white/10 px-2 py-0.5 text-[10px] uppercase tracking-wide text-fog/70">
                            {unreadPeers.has(peer.pub_key) ? 'New' : t('chat.peerOffline')}
                          </span>
                        </div>
                        <div className="mt-2 text-[11px] text-fog/55 break-words">
                          {peer.last_message || peer.pub_key}
                        </div>
                        <div className="mt-2 flex items-center justify-between gap-2 text-[10px] text-fog/45">
                          <span className="truncate">{peer.pub_key}</span>
                          <span className="shrink-0">{formatTimestamp(peer.last_message_at, locale)}</span>
                        </div>
                      </button>
                    ))}
                  </div>
                )}

                {!filteredOnlinePeers.length && !filteredRecentPeers.length && (
                  <p className="text-sm text-fog/60">{t('chat.noPeersMatch')}</p>
                )}
              </div>
            </>
          ) : (
            <p className="text-sm text-fog/60">{t('chat.noOnlinePeers')}</p>
          )}
        </div>

        <div className="section-card min-w-0 flex min-h-0 flex-col gap-4 lg:h-[720px]">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h3 className="text-lg font-semibold">
                {selectedPeer
                  ? t('chat.chatWith', { peer: selectedPeer.alias || selectedPeer.pub_key })
                  : t('chat.selectPeer')}
              </h3>
              <p className="text-xs text-fog/60">
                {selectedPeer
                  ? (selectedOnline ? t('chat.peerOnline') : t('chat.peerOffline'))
                  : t('chat.choosePeer')}
              </p>
            </div>
            {selectedPeer && (
              <span className="text-xs text-fog/60 break-all">{selectedPeer.pub_key}</span>
            )}
          </div>

          {unreadCount > 0 && (
            <div className="rounded-2xl border border-brass/30 bg-brass/10 px-4 py-2 text-xs text-brass">
              {unreadCount === 1
                ? t('chat.unreadSingle')
                : t('chat.unreadMultiple', { count: unreadCount })}{' '}
              {t('chat.unreadHint')}
            </div>
          )}

          <p className="text-xs text-fog/60">{t('chat.keysendCost')}</p>

          <div className="flex-1 min-h-0 overflow-y-auto space-y-3 pr-2">
            {loadingMessages && <p className="text-sm text-fog/60">{t('chat.loadingMessages')}</p>}
            {!loadingMessages && !messages.length && (
              <p className="text-sm text-fog/60">{t('chat.noMessages')}</p>
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
                    <span>{formatTimestamp(msg.timestamp, locale)}</span>
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
              placeholder={selectedPeer ? t('chat.writeMessage') : t('chat.selectPeerToChat')}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              disabled={!selectedPeer || !selectedOnline}
            />
            <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-fog/60">
              <span>{draft.trim().length}/{messageLimit}</span>
              <button className="btn-primary" onClick={handleSend} disabled={!canSend}>
                {sending ? t('chat.sending') : t('chat.sendOneSat')}
              </button>
            </div>
            {overLimit && (
              <p className="text-xs text-ember">{t('chat.messageTooLong', { count: messageLimit })}</p>
            )}
          </div>
        </div>
      </div>
    </section>
  )
}
