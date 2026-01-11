const base = ''

async function request(path: string, options?: RequestInit) {
  const res = await fetch(`${base}${path}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options?.headers || {})
    }
  })
  if (!res.ok) {
    const text = await res.text()
    if (text) {
      try {
        const payload = JSON.parse(text)
        if (payload && typeof payload.error === 'string') {
          throw new Error(payload.error)
        }
      } catch {
        // fall through to raw text
      }
      throw new Error(text)
    }
    throw new Error('Request failed')
  }
  if (res.status === 204) return null
  return res.json()
}

export const getHealth = () => request('/api/health')
export const getSystem = () => request('/api/system')
export const getDisk = () => request('/api/disk')
export const getPostgres = () => request('/api/postgres')
export const getBitcoin = () => request('/api/bitcoin')
export const getBitcoinActive = () => request('/api/bitcoin/active')
export const getBitcoinSource = () => request('/api/bitcoin/source')
export const setBitcoinSource = (payload: { source: 'local' | 'remote' }) =>
  request('/api/bitcoin/source', { method: 'POST', body: JSON.stringify(payload) })
export const getBitcoinLocalStatus = () => request('/api/bitcoin-local/status')
export const getBitcoinLocalConfig = () => request('/api/bitcoin-local/config')
export const updateBitcoinLocalConfig = (payload: {
  mode: 'full' | 'pruned'
  prune_size_gb?: number
  apply_now?: boolean
}) => request('/api/bitcoin-local/config', { method: 'POST', body: JSON.stringify(payload) })
export const getLndStatus = () => request('/api/lnd/status')
export const getLndConfig = () => request('/api/lnd/config')

export const postBitcoinRemote = (payload: { rpcuser: string; rpcpass: string }) =>
  request('/api/wizard/bitcoin-remote', { method: 'POST', body: JSON.stringify(payload) })

export const createWalletSeed = (payload?: { seed_passphrase?: string; wallet_password?: string }) =>
  request('/api/wizard/lnd/create-wallet', { method: 'POST', body: JSON.stringify(payload ?? {}) })

export const initWallet = (payload: { wallet_password: string; seed_words: string[] }) =>
  request('/api/wizard/lnd/init-wallet', { method: 'POST', body: JSON.stringify(payload) })

export const unlockWallet = (payload: { wallet_password: string }) =>
  request('/api/wizard/lnd/unlock', { method: 'POST', body: JSON.stringify(payload) })

export const restartService = (payload: { service: string }) =>
  request('/api/actions/restart', { method: 'POST', body: JSON.stringify(payload) })

export const getLogs = (service: string, lines: number) =>
  request(`/api/logs?service=${service}&lines=${lines}`)

export const updateLndConfig = (payload: {
  alias: string
  color: string
  min_channel_size_sat: number
  max_channel_size_sat: number
  apply_now: boolean
}) => request('/api/lnd/config', { method: 'POST', body: JSON.stringify(payload) })

export const updateLndRawConfig = (payload: { raw_user_conf: string; apply_now: boolean }) =>
  request('/api/lnd/config/raw', { method: 'POST', body: JSON.stringify(payload) })

export const getWalletSummary = () => request('/api/wallet/summary')
export const getWalletAddress = () => request('/api/wallet/address', { method: 'POST' })
export const createInvoice = (payload: { amount_sat: number; memo: string }) =>
  request('/api/wallet/invoice', { method: 'POST', body: JSON.stringify(payload) })
export const decodeInvoice = (payload: { payment_request: string }) =>
  request('/api/wallet/decode', { method: 'POST', body: JSON.stringify(payload) })
export const payInvoice = (payload: { payment_request: string }) =>
  request('/api/wallet/pay', { method: 'POST', body: JSON.stringify(payload) })

export const getLnChannels = () => request('/api/lnops/channels')
export const getLnPeers = () => request('/api/lnops/peers')
export const getLnChannelFees = (channelPoint: string) =>
  request(`/api/lnops/channel/fees?channel_point=${encodeURIComponent(channelPoint)}`)
export const connectPeer = (payload: { address?: string; pubkey?: string; host?: string; perm?: boolean }) =>
  request('/api/lnops/peer', { method: 'POST', body: JSON.stringify(payload) })
export const disconnectPeer = (payload: { pubkey: string }) =>
  request('/api/lnops/peer/disconnect', { method: 'POST', body: JSON.stringify(payload) })
export const boostPeers = (payload?: { limit?: number }) =>
  request('/api/lnops/peers/boost', { method: 'POST', body: JSON.stringify(payload ?? {}) })
export const openChannel = (payload: {
  pubkey: string
  local_funding_sat: number
  push_sat?: number
  private?: boolean
}) => request('/api/lnops/channel/open', { method: 'POST', body: JSON.stringify(payload) })
export const closeChannel = (payload: { channel_point: string; force?: boolean }) =>
  request('/api/lnops/channel/close', { method: 'POST', body: JSON.stringify(payload) })
export const updateChannelFees = (payload: {
  channel_point?: string
  apply_all?: boolean
  base_fee_msat?: number
  fee_rate_ppm?: number
  time_lock_delta?: number
  inbound_enabled?: boolean
  inbound_base_msat?: number
  inbound_fee_rate_ppm?: number
}) => request('/api/lnops/channel/fees', { method: 'POST', body: JSON.stringify(payload) })

export const getApps = () => request('/api/apps')
export const installApp = (id: string) => request(`/api/apps/${id}/install`, { method: 'POST' })
export const uninstallApp = (id: string) => request(`/api/apps/${id}/uninstall`, { method: 'POST' })
export const startApp = (id: string) => request(`/api/apps/${id}/start`, { method: 'POST' })
export const stopApp = (id: string) => request(`/api/apps/${id}/stop`, { method: 'POST' })
