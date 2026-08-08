import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { connectPeer, getChatInbox, getChatMessages, getLnPeers, markChatRead, sendChatMessage } from '../api'
import { getLocale } from '../i18n'
import { announceChatRead, parseChatTimestamp, readLocalChatReadMap, saveLocalChatRead } from '../utils/chatRead'

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
  last_read_at?: string
  identity_source?: string
  sender_verified?: boolean
}

type ChatMessage = {
  timestamp: string
  peer_pubkey: string
  peer_alias?: string
  direction: 'in' | 'out'
  message: string
  status: string
  payment_hash?: string
  amount_sat?: number
  identity_source?: string
  sender_verified?: boolean
}

type ChatInboxItem = {
  peer_pubkey: string
  peer_alias?: string
  last_inbound_at: string
  last_message_at?: string
  last_message?: string
  last_message_direction?: 'in' | 'out'
  last_read_at?: string
  identity_source?: string
  sender_verified?: boolean
}

type ParsedInboxItem = {
  peer_pubkey: string
  peer_alias: string
  last_inbound_at: number
  last_message_at: number
  last_message: string
  last_message_direction?: 'in' | 'out'
  last_read_at: number
  identity_source?: string
  sender_verified?: boolean
}

const messageLimit = 500
const defaultAmountSat = 1

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

const parsePositiveSatAmount = (value: string) => {
  const trimmed = value.trim()
  if (!/^\d+$/.test(trimmed)) return null
  const parsed = Number(trimmed)
  if (!Number.isSafeInteger(parsed) || parsed <= 0) return null
  return parsed
}

const formatSats = (value: number | undefined) => {
  if (!value || value <= 0) return ''
  return `${value.toLocaleString()} sat`
}

const chatSendErrorMessage = (err: any, t: (key: string, options?: any) => string) => {
  const code = typeof err?.code === 'string' ? err.code : ''
  switch (code) {
	case 'spending_guard_limit_exceeded':
	  return t('chat.errorSpendingGuard')
    case 'chat_keysend_incorrect_payment_details':
      return t('chat.errorIncorrectPaymentDetails')
    case 'chat_keysend_route_failed':
      return t('chat.errorRouteFailed')
    case 'chat_keysend_insufficient_balance':
      return t('chat.errorInsufficientBalance')
    case 'chat_keysend_timeout':
      return t('chat.errorTimeout')
    default:
      break
  }
  const message = typeof err?.message === 'string' ? err.message : ''
  const lower = message.toLowerCase()
  if (lower.includes('incorrect payment details')) return t('chat.errorIncorrectPaymentDetails')
  if (lower.includes('no route') || lower.includes('unable to find a path')) return t('chat.errorRouteFailed')
  if (lower.includes('insufficient') || lower.includes('balance')) return t('chat.errorInsufficientBalance')
  if (lower.includes('timeout') || lower.includes('deadline exceeded')) return t('chat.errorTimeout')
  return message || t('chat.sendFailed')
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
  const [lastReadMap, setLastReadMap] = useState<Record<string, number>>(readLocalChatReadMap)
  const [messageStatus, setMessageStatus] = useState('')
  const [draft, setDraft] = useState('')
  const [amountSatInput, setAmountSatInput] = useState(String(defaultAmountSat))
  const [sending, setSending] = useState(false)
  const [loadingMessages, setLoadingMessages] = useState(false)
  const readAtRef = useRef<Record<string, number>>(readLocalChatReadMap())
  const markReadInFlightRef = useRef(new Set<string>())
  const messagesScrollRef = useRef<HTMLDivElement | null>(null)
  const stickToBottomRef = useRef(true)

  const updateReadState = (peerPubkey: string, readAt: number, serverReadAt = 0) => {
    if (!peerPubkey || !readAt) return
    const nextReadAt = Math.max(readAtRef.current[peerPubkey] || 0, readAt)
    if (nextReadAt === (readAtRef.current[peerPubkey] || 0)) return
    readAtRef.current[peerPubkey] = nextReadAt
    setLastReadMap((current) => {
      if ((current[peerPubkey] || 0) >= readAt) return current
      return { ...current, [peerPubkey]: readAt }
    })
    setInboxItems((current) => current.map((item) => item.peer_pubkey === peerPubkey
      ? { ...item, last_read_at: new Date(Math.max(parseChatTimestamp(item.last_read_at), serverReadAt || readAt)).toISOString() }
      : item))
    saveLocalChatRead(peerPubkey, readAt)
    announceChatRead()
  }

  const persistRead = async (peerPubkey: string, latestInboundAt: number, forceServer = false) => {
    if (!peerPubkey || !latestInboundAt) return
    if (!forceServer && (readAtRef.current[peerPubkey] || 0) >= latestInboundAt) return
    if (markReadInFlightRef.current.has(peerPubkey)) return
    markReadInFlightRef.current.add(peerPubkey)
    updateReadState(peerPubkey, latestInboundAt)
    try {
      const response = await markChatRead(peerPubkey)
      const serverReadAt = parseChatTimestamp(response?.last_read_at) || latestInboundAt
      updateReadState(peerPubkey, Math.max(latestInboundAt, serverReadAt), serverReadAt)
    } catch {
      // Keep the current browser usable; the next inbox poll retries node persistence.
    } finally {
      markReadInFlightRef.current.delete(peerPubkey)
    }
  }

  const loadPeers = async (showStatus = true) => {
    if (showStatus) setPeerStatus(t('chat.loadingPeers'))
    try {
      const res = await getLnPeers()
      const items = Array.isArray(res?.peers) ? res.peers : []
      setPeers((current) => JSON.stringify(current) === JSON.stringify(items) ? current : items)
      if (showStatus) setPeerStatus('')
      return items as Peer[]
    } catch (err: any) {
      if (showStatus) setPeerStatus(err?.message || t('chat.loadPeersFailed'))
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
    const timer = window.setInterval(() => { void loadPeers(false) }, 20000)
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
        const items = (Array.isArray(res?.items) ? res.items : []) as ChatInboxItem[]
        setInboxItems((current) => JSON.stringify(current) === JSON.stringify(items) ? current : items)

        const localReadMap = readLocalChatReadMap()
        const mergedReadMap = { ...readAtRef.current }
        for (const item of items) {
          const peerPubkey = item.peer_pubkey?.trim()
          if (!peerPubkey) continue
          const serverReadAt = parseChatTimestamp(item.last_read_at)
          const localReadAt = localReadMap[peerPubkey] || 0
          const lastInboundAt = parseChatTimestamp(item.last_inbound_at)
          mergedReadMap[peerPubkey] = Math.max(mergedReadMap[peerPubkey] || 0, serverReadAt, localReadAt)
          if (localReadAt >= lastInboundAt && serverReadAt < lastInboundAt) {
            void persistRead(peerPubkey, lastInboundAt, true)
          }
        }
        readAtRef.current = mergedReadMap
        setLastReadMap((current) => JSON.stringify(current) === JSON.stringify(mergedReadMap) ? current : mergedReadMap)
      } catch {
        // Preserve the current list during a transient polling failure.
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
    setMessages([])
    setMessageStatus('')

    let mounted = true
    const load = async (showLoading: boolean) => {
      if (showLoading) setLoadingMessages(true)
      try {
        const res = await getChatMessages(selectedPeer.pub_key)
        if (!mounted) return
        const items = (Array.isArray(res?.items) ? res.items : []) as ChatMessage[]
        setMessages((current) => JSON.stringify(current) === JSON.stringify(items) ? current : items)
        setMessageStatus('')
        let latestInboundAt = 0
        for (const item of items) {
          if (item.direction === 'in') {
            latestInboundAt = Math.max(latestInboundAt, parseChatTimestamp(item.timestamp))
          }
        }
        if (latestInboundAt) void persistRead(selectedPeer.pub_key, latestInboundAt)
      } catch (err: any) {
        if (!mounted) return
        if (showLoading) setMessageStatus(err?.message || t('chat.loadMessagesFailed'))
      } finally {
        if (mounted && showLoading) setLoadingMessages(false)
      }
    }
    void load(true)
    const timer = window.setInterval(() => { void load(false) }, 12000)
    return () => {
      mounted = false
      window.clearInterval(timer)
    }
  }, [selectedPeer?.pub_key, t])

  useEffect(() => {
    const container = messagesScrollRef.current
    if (!container || !stickToBottomRef.current) return
    const frame = window.requestAnimationFrame(() => {
      container.scrollTo({ top: container.scrollHeight, behavior: 'smooth' })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [messages, selectedPeer?.pub_key])

  const onlinePeerSet = useMemo(() => new Set(peers.map((peer) => peer.pub_key)), [peers])

  const parsedInboxItems = useMemo(() => {
    return inboxItems
      .map((item) => {
        const peerPubkey = item.peer_pubkey.trim()
        const lastInboundAt = new Date(item.last_inbound_at).getTime()
        const rawLastMessageAt = item.last_message_at ? new Date(item.last_message_at).getTime() : lastInboundAt
        const lastReadAt = parseChatTimestamp(item.last_read_at)
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
          last_read_at: lastReadAt,
          identity_source: item.identity_source,
          sender_verified: item.sender_verified,
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
      setSelectedPeer((current) => {
        const next: ChatPeer = {
          ...onlineMatch,
          last_inbound_at: inboxMatch?.last_inbound_at,
          last_message_at: inboxMatch?.last_message_at,
          last_message: inboxMatch?.last_message,
          last_message_direction: inboxMatch?.last_message_direction,
          last_read_at: inboxMatch?.last_read_at ? new Date(inboxMatch.last_read_at).toISOString() : undefined,
          identity_source: inboxMatch?.identity_source,
          sender_verified: inboxMatch?.sender_verified,
        }
        return current && JSON.stringify(current) === JSON.stringify(next) ? current : next
      })
      return
    }
    if (inboxMatch) {
      setSelectedPeer((current) => {
        if (!current) return current
        const next: ChatPeer = {
          ...current,
          alias: current.alias || inboxMatch.peer_alias,
          last_inbound_at: inboxMatch.last_inbound_at,
          last_message_at: inboxMatch.last_message_at,
          last_message: inboxMatch.last_message,
          last_message_direction: inboxMatch.last_message_direction,
          last_read_at: inboxMatch.last_read_at ? new Date(inboxMatch.last_read_at).toISOString() : undefined,
          identity_source: inboxMatch.identity_source,
          sender_verified: inboxMatch.sender_verified,
        }
        return JSON.stringify(current) === JSON.stringify(next) ? current : next
      })
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
        last_read_at: inbox?.last_read_at ? new Date(inbox.last_read_at).toISOString() : undefined,
        identity_source: inbox?.identity_source,
        sender_verified: inbox?.sender_verified,
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
        last_read_at: item.last_read_at ? new Date(item.last_read_at).toISOString() : undefined,
        identity_source: item.identity_source,
        sender_verified: item.sender_verified,
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
  const amountSat = parsePositiveSatAmount(amountSatInput)
  const canSend = Boolean(selectedPeer && selectedOnline && draft.trim() && amountSat && !overLimit && !sending)

  const handleSelectPeer = (peer: ChatPeer) => {
    stickToBottomRef.current = true
    setSelectedPeer(peer)
    if (peer.last_inbound_at) void persistRead(peer.pub_key, peer.last_inbound_at)
  }

  const handleMobileBack = () => {
    setSelectedPeer(null)
    setMessages([])
    setMessageStatus('')
    stickToBottomRef.current = true
  }

  const handleSend = async () => {
    if (!selectedPeer || !canSend || !amountSat) return
    const trimmed = draft.trim()
    setSending(true)
    setMessageStatus(t('chat.sending'))
    try {
      const res = await sendChatMessage({ peer_pubkey: selectedPeer.pub_key, message: trimmed, amount_sat: amountSat })
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
          sender_verified: true,
          identity_source: 'local',
        }
        const rest = prev.filter((item) => item.peer_pubkey !== selectedPeer.pub_key)
        return [nextItem, ...rest]
      })
      setMessageStatus('')
    } catch (err: any) {
      setMessageStatus(chatSendErrorMessage(err, t))
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
          handleSelectPeer({
            ...match,
            last_inbound_at: inbox?.last_inbound_at,
            last_message_at: inbox?.last_message_at,
            last_message: inbox?.last_message,
            last_message_direction: inbox?.last_message_direction,
            last_read_at: inbox?.last_read_at ? new Date(inbox.last_read_at).toISOString() : undefined,
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
    <section className="space-y-3 sm:space-y-6">
      <div className="section-card hidden sm:block">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-2xl font-semibold">{t('chat.title')}</h2>
            <p className="text-fog/60">{t('chat.subtitle')}</p>
          </div>
          <button className="btn-secondary text-xs px-3 py-2" onClick={() => { void loadPeers(true) }}>
            {t('common.refresh')}
          </button>
        </div>
        {peerStatus && <p className="mt-3 text-sm text-brass">{peerStatus}</p>}
      </div>

      <div className="grid items-start gap-4 lg:grid-cols-[minmax(320px,350px)_minmax(0,1fr)] xl:grid-cols-[minmax(340px,370px)_minmax(0,1fr)]">
        <div className={`section-card min-h-0 flex-col gap-4 h-[calc(100dvh-8rem)] sm:h-[calc(100dvh-15rem)] sm:min-h-[520px] lg:flex lg:h-[720px] ${selectedPeer ? 'hidden' : 'flex'}`}>
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
                      onClick={() => handleSelectPeer(peer)}
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
                            {t('chat.newMessage')}
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
                        onClick={() => handleSelectPeer(peer)}
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
                            {unreadPeers.has(peer.pub_key) ? t('chat.newMessage') : t('chat.peerOffline')}
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

        <div className={`section-card min-w-0 min-h-0 flex-col gap-3 h-[calc(100dvh-8rem)] sm:h-[calc(100dvh-15rem)] sm:min-h-[520px] lg:flex lg:h-[720px] lg:gap-4 ${selectedPeer ? 'flex' : 'hidden'}`}>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex min-w-0 items-start gap-3">
              {selectedPeer && (
                <button
                  type="button"
                  className="btn-secondary shrink-0 px-3 py-2 text-xs lg:hidden"
                  onClick={handleMobileBack}
                  aria-label={t('chat.backToChats')}
                >
                  &larr;
                </button>
              )}
              <div className="min-w-0">
                <h3 className="break-all text-lg font-semibold sm:break-normal">
                  {selectedPeer
                    ? t('chat.chatWith', { peer: selectedPeer.alias || selectedPeer.pub_key })
                    : t('chat.selectPeer')}
                </h3>
                <p className="text-xs text-fog/60">
                  {selectedPeer
                    ? (selectedOnline ? t('chat.peerOnline') : t('chat.peerOffline'))
                    : t('chat.choosePeer')}
                </p>
                {selectedPeer?.last_message_direction === 'in' && selectedPeer.identity_source && selectedPeer.identity_source !== 'signed_sender' && (
                  <p className="mt-1 text-[11px] text-fog/50">
                    {selectedPeer.identity_source === 'incoming_channel'
                      ? t('chat.senderFromIncomingChannel')
                      : t('chat.senderUnverified')}
                  </p>
                )}
              </div>
            </div>
            {selectedPeer && (
              <span className="hidden max-w-[42%] text-xs text-fog/60 break-all sm:block">{selectedPeer.pub_key}</span>
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

          <div
            ref={messagesScrollRef}
            className="flex-1 min-h-0 overflow-y-auto space-y-3 pr-1 sm:pr-2"
            onScroll={(event) => {
              const element = event.currentTarget
              stickToBottomRef.current = element.scrollHeight - element.scrollTop - element.clientHeight < 96
            }}
          >
            {loadingMessages && !messages.length && <p className="text-sm text-fog/60">{t('chat.loadingMessages')}</p>}
            {!loadingMessages && !messages.length && (
              <p className="text-sm text-fog/60">{t('chat.noMessages')}</p>
            )}
            {messages.map((msg, idx) => (
              <div key={`${msg.payment_hash || idx}`} className={`flex ${msg.direction === 'out' ? 'justify-end' : 'justify-start'}`}>
                <div
                  className={`max-w-[90%] rounded-2xl border px-4 py-3 text-sm sm:max-w-[75%] ${
                    msg.direction === 'out'
                      ? 'border-glow/30 bg-glow/20 text-fog'
                      : 'border-white/10 bg-white/10 text-fog'
                  }`}
                >
                  <div className="whitespace-pre-wrap break-words">{msg.message}</div>
                  <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-[11px] text-fog/50">
                    <span>{formatTimestamp(msg.timestamp, locale)}</span>
                    <span className="flex flex-wrap items-center justify-end gap-2">
                      {formatSats(msg.amount_sat) && <span>{formatSats(msg.amount_sat)}</span>}
                      {msg.direction === 'in' && msg.identity_source && msg.identity_source !== 'signed_sender' && (
                        <span>{msg.identity_source === 'incoming_channel' ? t('chat.senderFromChannelShort') : t('chat.senderUnverifiedShort')}</span>
                      )}
                      {msg.direction === 'out' && <span>{msg.status}</span>}
                    </span>
                  </div>
                </div>
              </div>
            ))}
          </div>

          {messageStatus && <p className="text-sm text-brass">{messageStatus}</p>}

          <div className="space-y-3">
            <textarea
              className="input-field min-h-[72px] sm:min-h-[96px]"
              placeholder={selectedPeer ? t('chat.writeMessage') : t('chat.selectPeerToChat')}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              disabled={!selectedPeer || !selectedOnline}
            />
            <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-fog/60">
              <span>{draft.trim().length}/{messageLimit}</span>
              <div className="flex w-full items-center justify-end gap-2 sm:w-auto sm:flex-wrap">
                <label className="flex min-w-0 flex-1 items-center gap-2 sm:flex-none">
                  <span>{t('chat.amountSat')}</span>
                  <input
                    className="input-field !w-full min-w-0 px-3 py-2 text-sm sm:!w-28"
                    type="number"
                    min="1"
                    step="1"
                    inputMode="numeric"
                    value={amountSatInput}
                    onChange={(e) => setAmountSatInput(e.target.value)}
                    disabled={!selectedPeer || !selectedOnline || sending}
                    aria-label={t('chat.amountSat')}
                  />
                </label>
                <button className="btn-primary shrink-0" onClick={handleSend} disabled={!canSend}>
                  {sending ? t('chat.sending') : t('chat.sendAmount', { amount: amountSat || defaultAmountSat })}
                </button>
              </div>
            </div>
            {overLimit && (
              <p className="text-xs text-ember">{t('chat.messageTooLong', { count: messageLimit })}</p>
            )}
            {amountSatInput.trim() && !amountSat && (
              <p className="text-xs text-ember">{t('chat.invalidAmount')}</p>
            )}
          </div>
        </div>
      </div>
    </section>
  )
}
