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

const buildQuery = (params?: Record<string, string | number | boolean | undefined | null>) => {
  if (!params) return ''
  const query = Object.entries(params)
    .filter(([, value]) => value !== undefined && value !== null && value !== '')
    .map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`)
    .join('&')
  return query ? `?${query}` : ''
}

export const getHealth = () => request('/api/health')
export const getAmbossHealth = () => request('/api/amboss/health')
export const updateAmbossHealth = (payload: { enabled: boolean }) =>
  request('/api/amboss/health', { method: 'POST', body: JSON.stringify(payload) })
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
export const getElementsStatus = () => request('/api/elements/status')
export const getElementsMainchain = () => request('/api/elements/mainchain')
export const setElementsMainchain = (payload: { source: 'local' | 'remote' }) =>
  request('/api/elements/mainchain', { method: 'POST', body: JSON.stringify(payload) })
export const getLndStatus = () => request('/api/lnd/status')
export const getLndConfig = () => request('/api/lnd/config')
export const getLndUpgradeStatus = (force?: boolean) =>
  request(`/api/lnd/upgrade/status${buildQuery({ force: force ? 1 : undefined })}`)
export const startLndUpgrade = (payload: { target_version: string; download_url?: string }) =>
  request('/api/lnd/upgrade', { method: 'POST', body: JSON.stringify(payload) })
export const getWizardStatus = () => request('/api/wizard/status')

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

export const runSystemAction = (payload: { action: 'reboot' | 'shutdown' }) =>
  request('/api/actions/system', { method: 'POST', body: JSON.stringify(payload) })

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

export const getMempoolFees = () => request('/api/mempool/fees')

export const getWalletSummary = () => request('/api/wallet/summary')
export const getWalletAddress = () => request('/api/wallet/address', { method: 'POST' })
export const sendOnchain = (payload: { address: string; amount_sat?: number; sat_per_vbyte?: number; sweep_all?: boolean }) =>
  request('/api/wallet/send', { method: 'POST', body: JSON.stringify(payload) })
export const createInvoice = (payload: { amount_sat: number; memo: string }) =>
  request('/api/wallet/invoice', { method: 'POST', body: JSON.stringify(payload) })
export const decodeInvoice = (payload: { payment_request: string }) =>
  request('/api/wallet/decode', { method: 'POST', body: JSON.stringify(payload) })
export const payInvoice = (payload: { payment_request: string; channel_point?: string; amount_sat?: number }) =>
  request('/api/wallet/pay', { method: 'POST', body: JSON.stringify(payload) })

export const getLnChannels = () => request('/api/lnops/channels')
export const getLnPeers = () => request('/api/lnops/peers')
export const signLnMessage = (payload: { message: string }) =>
  request('/api/lnops/sign-message', { method: 'POST', body: JSON.stringify(payload) })
export const getLnChanHeal = () => request('/api/lnops/channel/auto-heal')
export const updateLnChanHeal = (payload: { enabled?: boolean; interval_sec?: number }) =>
  request('/api/lnops/channel/auto-heal', { method: 'POST', body: JSON.stringify(payload) })
export const getAutofeeConfig = () => request('/api/lnops/autofee/config')
export const updateAutofeeConfig = (payload: {
  enabled?: boolean
  profile?: string
  lookback_days?: number
  run_interval_sec?: number
  cooldown_up_sec?: number
  cooldown_down_sec?: number
  amboss_enabled?: boolean
  amboss_token?: string
  inbound_passive_enabled?: boolean
  discovery_enabled?: boolean
  explorer_enabled?: boolean
  super_source_enabled?: boolean
  super_source_base_fee_msat?: number
  min_ppm?: number
  max_ppm?: number
}) => request('/api/lnops/autofee/config', { method: 'POST', body: JSON.stringify(payload) })
export const getAutofeeChannels = () => request('/api/lnops/autofee/channels')
export const updateAutofeeChannels = (payload: {
  apply_all?: boolean
  enabled?: boolean
  channel_id?: number
  channel_point?: string
}) => request('/api/lnops/autofee/channels', { method: 'POST', body: JSON.stringify(payload) })
export const runAutofee = (payload: { dry_run: boolean }) =>
  request('/api/lnops/autofee/run', { method: 'POST', body: JSON.stringify(payload) })
export const getAutofeeStatus = () => request('/api/lnops/autofee/status')
export const getAutofeeResults = (lines = 50) => request(`/api/lnops/autofee/results?lines=${lines}`)
export const getLnChannelFees = (channelPoint: string) =>
  request(`/api/lnops/channel/fees?channel_point=${encodeURIComponent(channelPoint)}`)
export const updateLnChannelStatus = (payload: { channel_point: string; enabled: boolean }) =>
  request('/api/lnops/channel/status', { method: 'POST', body: JSON.stringify(payload) })
export const connectPeer = (payload: { address?: string; pubkey?: string; host?: string; perm?: boolean }) =>
  request('/api/lnops/peer', { method: 'POST', body: JSON.stringify(payload) })
export const disconnectPeer = (payload: { pubkey: string }) =>
  request('/api/lnops/peer/disconnect', { method: 'POST', body: JSON.stringify(payload) })
export const boostPeers = (payload?: { limit?: number }) =>
  request('/api/lnops/peers/boost', { method: 'POST', body: JSON.stringify(payload ?? {}) })
export const openChannel = (payload: {
  peer_address: string
  local_funding_sat: number
  close_address?: string
  sat_per_vbyte?: number
  private?: boolean
}) => request('/api/lnops/channel/open', { method: 'POST', body: JSON.stringify(payload) })
export const closeChannel = (payload: { channel_point: string; force?: boolean; sat_per_vbyte?: number }) =>
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

export const getChatMessages = (peerPubkey: string, limit = 200) =>
  request(`/api/chat/messages?peer_pubkey=${encodeURIComponent(peerPubkey)}&limit=${limit}`)

export const getChatInbox = () =>
  request('/api/chat/inbox')

export const sendChatMessage = (payload: { peer_pubkey: string; message: string }) =>
  request('/api/chat/send', { method: 'POST', body: JSON.stringify(payload) })

export const getNotifications = (limit = 200) =>
  request(`/api/notifications?limit=${limit}`)

export const getTelegramBackupConfig = () =>
  request('/api/notifications/backup/telegram')

export const updateTelegramBackupConfig = (payload: { bot_token?: string; chat_id?: string }) =>
  request('/api/notifications/backup/telegram', { method: 'POST', body: JSON.stringify(payload) })

export const testTelegramBackup = () =>
  request('/api/notifications/backup/telegram/test', { method: 'POST' })

export const getTerminalStatus = () => request('/api/terminal/status')

export const getOnchainUtxos = (params?: {
  min_conf?: number
  max_conf?: number
  include_unconfirmed?: boolean
  limit?: number
}) => request(`/api/onchain/utxos${buildQuery(params)}`)

export const getOnchainTransactions = (params?: {
  min_conf?: number
  max_conf?: number
  include_unconfirmed?: boolean
  limit?: number
}) => request(`/api/onchain/transactions${buildQuery(params)}`)

export const getReportsRange = (range: string) =>
  request(`/api/reports/range?range=${encodeURIComponent(range)}`)
export const getReportsCustom = (from: string, to: string) =>
  request(`/api/reports/custom?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`)
export const getReportsSummary = (range: string) =>
  request(`/api/reports/summary?range=${encodeURIComponent(range)}`)
export const getReportsLive = () => request('/api/reports/live')
export const getReportsConfig = () => request('/api/reports/config')
export const updateReportsConfig = (payload: {
  live_timeout_sec?: number | null
  live_lookback_hours?: number | null
  run_timeout_sec?: number | null
}) => request('/api/reports/config', { method: 'POST', body: JSON.stringify(payload) })

export const getRebalanceConfig = () => request('/api/rebalance/config')
export const updateRebalanceConfig = (payload: {
  auto_enabled?: boolean
  scan_interval_sec?: number
  deadband_pct?: number
  source_min_local_pct?: number
  econ_ratio?: number
  roi_min?: number
  daily_budget_pct?: number
  max_concurrent?: number
  min_amount_sat?: number
  max_amount_sat?: number
  fee_ladder_steps?: number
  amount_probe_steps?: number
  amount_probe_adaptive?: boolean
  attempt_timeout_sec?: number
  rebalance_timeout_sec?: number
  payback_mode_flags?: number
  unlock_days?: number
  critical_release_pct?: number
  critical_min_sources?: number
  critical_min_available_sats?: number
  critical_cycles?: number
}) => request('/api/rebalance/config', { method: 'POST', body: JSON.stringify(payload) })
export const getRebalanceOverview = () => request('/api/rebalance/overview')
export const getRebalanceChannels = () => request('/api/rebalance/channels')
export const getRebalanceQueue = () => request('/api/rebalance/queue')
export const getRebalanceHistory = (limit = 0) =>
  limit > 0 ? request(`/api/rebalance/history?limit=${limit}`) : request('/api/rebalance/history')
export const runRebalance = (payload: { channel_id: number; channel_point?: string; target_outbound_pct?: number }) =>
  request('/api/rebalance/run', { method: 'POST', body: JSON.stringify(payload) })
export const stopRebalance = (payload: { job_id: number }) =>
  request('/api/rebalance/stop', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelTarget = (payload: { channel_id: number; channel_point: string; target_outbound_pct: number }) =>
  request('/api/rebalance/channel/target', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelAuto = (payload: { channel_id: number; channel_point?: string; auto_enabled: boolean }) =>
  request('/api/rebalance/channel/auto', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceExclude = (payload: { channel_id: number; channel_point: string; excluded: boolean }) =>
  request('/api/rebalance/channel/exclude', { method: 'POST', body: JSON.stringify(payload) })

export const getApps = () => request('/api/apps')
export const getAppAdminPassword = (id: string) => request(`/api/apps/${id}/admin-password`)
export const installApp = (id: string) => request(`/api/apps/${id}/install`, { method: 'POST' })
export const uninstallApp = (id: string) => request(`/api/apps/${id}/uninstall`, { method: 'POST' })
export const startApp = (id: string) => request(`/api/apps/${id}/start`, { method: 'POST' })
export const stopApp = (id: string) => request(`/api/apps/${id}/stop`, { method: 'POST' })
export const resetAppAdmin = (id: string) => request(`/api/apps/${id}/reset-admin`, { method: 'POST' })
