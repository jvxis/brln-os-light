import type { ChannelRankingItem } from './types'

const paramsHash = (route: string, params: Record<string, string | undefined>) => {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value) query.set(key, value)
  })
  const suffix = query.toString()
  return suffix ? `#${route}?${suffix}` : `#${route}`
}

export const channelRankingHash = (channelPoint: string) =>
  paramsHash('channel-ranking', { channel_point: channelPoint })

export const graphExplorerHash = (pubkey: string) =>
  paramsHash('graph-explorer', { pubkey })

export const lightningOpsHash = (channelPoint: string, section?: string) =>
  paramsHash('lightning-ops', { channel_point: channelPoint, section })

export const moduleHash = (item: ChannelRankingItem, targetModule?: string) => {
  switch (String(targetModule || '').trim()) {
    case 'rebalance':
      return paramsHash('rebalance-center', { channel_point: item.channel_point })
    case 'rebalance-sources':
      return paramsHash('rebalance-center', {})
    case 'autofee':
      return paramsHash('fee-center', { channel_point: item.channel_point })
    case 'close-manager':
      return lightningOpsHash(item.channel_point, 'close-channel-section')
    case 'htlc-manager':
      return lightningOpsHash(item.channel_point, 'htlc-manager-section')
    default:
      return lightningOpsHash(item.channel_point)
  }
}

export const readChannelPointFromHash = () => {
  if (typeof window === 'undefined') return ''
  const raw = window.location.hash.startsWith('#') ? window.location.hash.slice(1) : window.location.hash
  const [route, query = ''] = raw.split('?', 2)
  if (route !== 'channel-ranking') return ''
  return (new URLSearchParams(query).get('channel_point') || '').trim()
}
