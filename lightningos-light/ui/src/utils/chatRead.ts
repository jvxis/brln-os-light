export const chatLastReadKey = 'chat:lastRead'
export const chatReadEvent = 'lightningos:chat-read'

export const parseChatTimestamp = (value: unknown) => {
  if (typeof value !== 'string' && typeof value !== 'number') return 0
  const parsed = new Date(value).getTime()
  return Number.isNaN(parsed) ? 0 : parsed
}

export const readLocalChatReadMap = (): Record<string, number> => {
  try {
    const raw = localStorage.getItem(chatLastReadKey)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    if (parsed && typeof parsed === 'object') {
      const normalized: Record<string, number> = {}
      for (const [peerPubkey, value] of Object.entries(parsed)) {
        const readAt = Number(value)
        if (peerPubkey && Number.isFinite(readAt) && readAt > 0) {
          normalized[peerPubkey] = readAt
        }
      }
      return normalized
    }
  } catch {
    // The backend remains the source of truth when local storage is disabled.
  }
  return {}
}

export const saveLocalChatRead = (peerPubkey: string, readAt: number) => {
  if (!peerPubkey || !readAt) return
  const current = readLocalChatReadMap()
  if ((current[peerPubkey] || 0) >= readAt) return
  try {
    localStorage.setItem(chatLastReadKey, JSON.stringify({ ...current, [peerPubkey]: readAt }))
  } catch {
    // Node-side persistence still succeeds if local storage is unavailable.
  }
}

export const effectiveChatReadAt = (
  item: { peer_pubkey?: string; last_read_at?: string },
  localReadMap: Record<string, number>,
) => Math.max(
  parseChatTimestamp(item.last_read_at),
  localReadMap[item.peer_pubkey || ''] || 0,
)

export const announceChatRead = () => {
  window.dispatchEvent(new Event(chatReadEvent))
}
