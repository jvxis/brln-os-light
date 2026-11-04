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
// channel preselected where that screen reads it from the hash.
export const insightActionHash = (item: InsightItem, action: InsightAction) => {
  const point = action.params?.channel_point || item.channel_point
  const pubkey = action.params?.peer_pubkey || item.peer_pubkey
  switch (action.tool) {
    case 'rebalance_target':
    case 'use_as_source':
    case 'econ_override':
      return paramsHash('rebalance-center', { channel_point: point })
    case 'autofee_review':
      return paramsHash('fee-center', { channel_point: point })
    case 'open_channel':
      return item.kind === 'abrir_canal' ? paramsHash('new-channels', {}) : paramsHash('lightning-ops', { channel_point: point, section: 'open-channel-section' })
    case 'close_channel':
      return paramsHash('lightning-ops', { channel_point: point, section: 'close-channel-section' })
    case 'loop_in':
      return paramsHash('lightning-loop', {})
    default:
      return pubkey ? paramsHash('graph-explorer', { pubkey }) : paramsHash('channel-ranking', { channel_point: point })
  }
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
