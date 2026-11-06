import type { InsightAction, InsightItem } from '../../api'

const paramsHash = (route: string, params: Record<string, string | undefined>) => {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value) query.set(key, value)
  })
  const suffix = query.toString()
  return suffix ? `#${route}?${suffix}` : `#${route}`
}

// Every action points at the screen that already does the work, with the
// channel preselected where that screen reads it from the hash. Phase 3: the
// parameters the rule computed travel along (amount, last hop, suggested cap)
// so the destination form opens filled in; the confirmation still lives there.
export const insightActionHash = (item: InsightItem, action: InsightAction) => {
  const point = action.params?.channel_point || item.channel_point
  const pubkey = action.params?.peer_pubkey || item.peer_pubkey
  switch (action.tool) {
    case 'rebalance_target':
    case 'use_as_source':
      return paramsHash('rebalance-center', { channel_point: point })
    case 'econ_override':
      return paramsHash('rebalance-center', { channel_point: point, econ_ratio: action.params?.ratio })
    case 'autofee_review':
      return paramsHash('fee-center', { channel_point: point })
    case 'open_channel':
      return item.kind === 'abrir_canal'
        ? paramsHash('new-channels', { pubkey })
        : paramsHash('lightning-ops', { channel_point: point, peer_pubkey: pubkey, amount_sat: action.params?.amount_sat, section: 'open-channel-section' })
    case 'close_channel':
      return paramsHash('lightning-ops', { channel_point: point, section: 'close-channel-section' })
    case 'loop_in':
      return paramsHash('lightning-loop', { direction: 'in', amount_sat: action.params?.amount_sat, last_hop: action.params?.last_hop_pubkey })
    default:
      return pubkey ? paramsHash('graph-explorer', { pubkey }) : paramsHash('channel-ranking', { channel_point: point })
  }
}

// readHashParams returns the query of the current hash when it belongs to the
// given route, so a destination screen can prefill its form from a link.
export const readHashParams = (route: string): URLSearchParams => {
  if (typeof window === 'undefined') return new URLSearchParams()
  const raw = window.location.hash.startsWith('#') ? window.location.hash.slice(1) : window.location.hash
  const queryIndex = raw.indexOf('?')
  if (queryIndex < 0 || raw.slice(0, queryIndex) !== route) return new URLSearchParams()
  return new URLSearchParams(raw.slice(queryIndex + 1))
}

export const insightChannelHash = (item: InsightItem) =>
  item.channel_point ? paramsHash('channel-ranking', { channel_point: item.channel_point }) : paramsHash('graph-explorer', { pubkey: item.peer_pubkey })

export const insightSourceHash = (source: string) => {
  switch (source) {
    case 'ranking':
      return '#channel-ranking'
    case 'rebalance':
      return '#rebalance-center'
    case 'interlock':
      return '#automation-interlock'
    case 'autofee':
      return '#fee-center'
    case 'open_candidates':
      return '#new-channels'
    default:
      return '#insights'
  }
}
