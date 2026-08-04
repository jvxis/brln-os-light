const base = ''

let csrfToken = ''
let lndStatusInFlight: Promise<any> | null = null
let lndStatusCached: any = null
let lndStatusCachedAt = 0
const lndStatusClientCacheMs = 1500

export class APIError extends Error {
  status: number
  code: string

  constructor(message: string, status: number, code = '') {
    super(message)
    this.name = 'APIError'
    this.status = status
    this.code = code
  }
}

export type AuthState = {
  enabled: boolean
  password_configured: boolean
  setup_required: boolean
  authenticated: boolean
  csrf_token?: string
  session_expires_at?: string
  setup_token_issued?: boolean
  recovery_token_issued?: boolean
}

export type MenuPreferences = {
  version: number
  favorites: string[]
  hidden: string[]
}

export type MenuPreferencesRecord = MenuPreferences & {
  exists: boolean
  updated_at?: string
}

const setCSRFToken = (value?: string) => {
  csrfToken = typeof value === 'string' ? value.trim() : ''
}

async function request(path: string, options?: RequestInit) {
  const method = String(options?.method || 'GET').toUpperCase()
  const headers = new Headers({
    'Content-Type': 'application/json',
    ...(options?.headers || {})
  })
  if (csrfToken && !['GET', 'HEAD', 'OPTIONS'].includes(method)) {
    headers.set('X-CSRF-Token', csrfToken)
  }

  const res = await fetch(`${base}${path}`, {
    ...options,
    credentials: 'same-origin',
    headers
  })
  if (!res.ok) {
    const text = await res.text()
    let errorMsg = text || 'Request failed'
    let errorCode = ''
    if (text) {
      try {
        const payload = JSON.parse(text)
        if (payload && typeof payload.message === 'string') {
          errorMsg = payload.message
        } else if (payload && typeof payload.error === 'string') {
          errorMsg = payload.error
        }
        if (payload && typeof payload.code === 'string') {
          errorCode = payload.code
        }
      } catch {
        // not JSON, use raw text
      }
    }
    if (res.status === 401 && !path.startsWith('/api/auth/')) {
      setCSRFToken('')
      window.dispatchEvent(new CustomEvent('auth:required'))
    }
    throw new APIError(errorMsg, res.status, errorCode)
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
export const getSystemCheck = () => request('/api/system-check')
export const getAuthState = async () => {
  const data = await request('/api/auth/state') as AuthState
  if (data?.authenticated && data?.csrf_token) {
    setCSRFToken(data.csrf_token)
  } else {
    setCSRFToken('')
  }
  return data
}
export const setupAuth = async (payload: { setup_token: string; password: string; confirm_password: string }) => {
  const data = await request('/api/auth/setup', { method: 'POST', body: JSON.stringify(payload) }) as AuthState
  setCSRFToken(data?.csrf_token)
  return data
}
export const loginAuth = async (payload: { password: string }) => {
  const data = await request('/api/auth/login', { method: 'POST', body: JSON.stringify(payload) }) as AuthState
  setCSRFToken(data?.csrf_token)
  return data
}
export const logoutAuth = async () => {
  const data = await request('/api/auth/logout', { method: 'POST', body: JSON.stringify({}) })
  setCSRFToken('')
  return data
}
export const recoverAuth = async (payload: { recovery_token: string; password: string; confirm_password: string }) => {
  const data = await request('/api/auth/recovery', { method: 'POST', body: JSON.stringify(payload) }) as AuthState
  setCSRFToken(data?.csrf_token)
  return data
}
export const enableLoginAuth = () =>
  request('/api/auth/enable-login', { method: 'POST', body: JSON.stringify({}) })
export const changePasswordAuth = async (payload: { current_password: string; password: string; confirm_password: string }) => {
  const data = await request('/api/auth/change-password', { method: 'POST', body: JSON.stringify(payload) }) as AuthState
  setCSRFToken(data?.csrf_token)
  return data
}
export const reauthAuth = (payload: { password: string; scope: string }) =>
  request('/api/auth/reauth', { method: 'POST', body: JSON.stringify(payload) })
export const getMenuPreferences = () =>
  request('/api/ui/preferences/menu') as Promise<MenuPreferencesRecord>
export const updateMenuPreferences = (payload: MenuPreferences) =>
  request('/api/ui/preferences/menu', { method: 'PUT', body: JSON.stringify(payload) }) as Promise<MenuPreferencesRecord>
export const getAmbossHealth = () => request('/api/amboss/health')
export const updateAmbossHealth = (payload: { enabled: boolean }) =>
  request('/api/amboss/health', { method: 'POST', body: JSON.stringify(payload) })
export const getSystem = () => request('/api/system')
export const getDisk = () => request('/api/disk')
export const getPostgres = () => request('/api/postgres')
export const getPostgresMaintenance = () => request('/api/postgres/maintenance')
export const runPostgresCleanup = () =>
  request('/api/postgres/maintenance/cleanup', { method: 'POST', body: JSON.stringify({}) })
export const runPostgresVacuum = () =>
  request('/api/postgres/maintenance/vacuum', { method: 'POST', body: JSON.stringify({}) })
export const runPostgresGraphHistoryCompact = () =>
  request('/api/postgres/maintenance/compact-graph-history', { method: 'POST', body: JSON.stringify({}) })
export const getBitcoin = () => request('/api/bitcoin')
export const getBitcoinActive = () => request('/api/bitcoin/active')
export const getBIP110Monitor = () => request('/api/bitcoin/bip110')
export const getBitcoinSource = () => request('/api/bitcoin/source')
export const setBitcoinSource = (payload: { source: 'local' | 'remote'; allow_unsynced?: boolean }) =>
  request('/api/bitcoin/source', { method: 'POST', body: JSON.stringify(payload) })
export const getBitcoinMarket = () => request('/api/bitcoin/market')
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
export type PeerswapElementsSourcePayload = {
  mode: 'local' | 'remote'
  url?: string
  user?: string
  password?: string
  wallet?: string
}
export const getPeerswapElementsSource = () => request('/api/apps/peerswap/elements-source')
export const testPeerswapElementsSource = (payload?: PeerswapElementsSourcePayload, signal?: AbortSignal) =>
  request('/api/apps/peerswap/elements-source/test', {
    method: 'POST',
    body: payload ? JSON.stringify(payload) : undefined,
    signal
  })
export const setPeerswapElementsSource = (payload: PeerswapElementsSourcePayload) =>
  request('/api/apps/peerswap/elements-source', { method: 'POST', body: JSON.stringify(payload) })
export const getLndStatus = (force?: boolean) => {
  if (force) {
    return request(`/api/lnd/status${buildQuery({ force: 1 })}`)
  }
  const now = Date.now()
  if (lndStatusCached !== null && now - lndStatusCachedAt < lndStatusClientCacheMs) {
    return Promise.resolve(lndStatusCached)
  }
  if (lndStatusInFlight) return lndStatusInFlight
  lndStatusInFlight = request('/api/lnd/status')
    .then((value) => {
      lndStatusCached = value
      lndStatusCachedAt = Date.now()
      return value
    })
    .finally(() => {
      lndStatusInFlight = null
    })
  return lndStatusInFlight
}
export const getLndConfig = () => request('/api/lnd/config')
export const getLndUpgradeStatus = (force?: boolean) =>
  request(`/api/lnd/upgrade/status${buildQuery({ force: force ? 1 : undefined })}`)
export const startLndUpgrade = (payload: { target_version: string; download_url?: string }) =>
  request('/api/lnd/upgrade', { method: 'POST', body: JSON.stringify(payload) })
export const getAppUpgradeStatus = (force?: boolean) =>
  request(`/api/app/upgrade/status${buildQuery({ force: force ? 1 : undefined })}`)
export const startAppUpgrade = (payload?: { target_version?: string }) =>
  request('/api/app/upgrade/start', { method: 'POST', body: JSON.stringify(payload ?? {}) })
export const getTorUpgradeStatus = (force?: boolean) =>
  request(`/api/tor/upgrade/status${buildQuery({ force: force ? 1 : undefined })}`)
export const startTorUpgrade = () =>
  request('/api/tor/upgrade/start', { method: 'POST', body: JSON.stringify({}) })
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

export const getLogs = (service: string, lines: number, since?: string) =>
  request(`/api/logs${buildQuery({ service, lines, since })}`)

export const updateLndConfig = (payload: {
  alias?: string
  color?: string
  min_channel_size_sat?: number
  max_channel_size_sat?: number
  network_mode?: 'private' | 'hybrid'
  graph_sync_peers?: number
  disconnect_unresponsive_peers?: boolean
  apply_now: boolean
}) => request('/api/lnd/config', { method: 'POST', body: JSON.stringify(payload) })

export const updateLndRawConfig = (payload: { raw_user_conf: string; apply_now: boolean }) =>
  request('/api/lnd/config/raw', { method: 'POST', body: JSON.stringify(payload) })

export const getMempoolFees = () => request('/api/mempool/fees')

export const getWalletSummary = () => request('/api/wallet/summary')
export const getWalletActivity = (range: '7d' | '1m' | '1a', limit = 100, offset = 0) =>
  request(`/api/wallet/activity?range=${encodeURIComponent(range)}&limit=${encodeURIComponent(String(limit))}&offset=${encodeURIComponent(String(offset))}`)
export const getWalletPaymentDetail = (paymentHash: string) =>
  request(`/api/wallet/payments/${encodeURIComponent(paymentHash)}`)
export const getWalletAddress = () => request('/api/wallet/address', { method: 'POST' })
export const previewOnchainSend = (payload: { address: string; amount_sat?: number; sat_per_vbyte: number; sweep_all?: boolean; outpoints?: string[] }) =>
  request('/api/wallet/send/preview', { method: 'POST', body: JSON.stringify(payload) })
export const sendOnchain = (payload: { address: string; amount_sat?: number; sat_per_vbyte?: number; sweep_all?: boolean; outpoints?: string[]; label?: string; confirm_password?: string }) =>
  request('/api/wallet/send', { method: 'POST', body: JSON.stringify(payload) })
export const createInvoice = (payload: {
  amount_sat: number
  memo: string
  expiry_seconds?: number
  blinded?: boolean
  blinded_incoming_channel_point?: string
}) =>
  request('/api/wallet/invoice', { method: 'POST', body: JSON.stringify(payload) })
export const decodeInvoice = (payload: { payment_request: string }) =>
  request('/api/wallet/decode', { method: 'POST', body: JSON.stringify(payload) })
export const previewWalletPayment = (payload: { payment_request: string; channel_point?: string; channel_points?: string[]; amount_sat?: number; max_fee_sat?: number }) =>
  request('/api/wallet/pay/preview', { method: 'POST', body: JSON.stringify(payload) })
export const payInvoiceValidatedRoute = (payload: { payment_request: string; channel_point?: string; channel_points?: string[]; route_token?: string; amount_sat?: number; max_fee_sat?: number }) =>
  request('/api/wallet/pay/validated-route', { method: 'POST', body: JSON.stringify(payload) })
export const payInvoiceMPP = (payload: { payment_request: string; channel_point?: string; channel_points?: string[]; amount_sat?: number; max_fee_sat?: number; max_parts?: number; max_shard_sat?: number }) =>
  request('/api/wallet/pay/mpp', { method: 'POST', body: JSON.stringify(payload) })
export const payInvoice = (payload: { payment_request: string; channel_point?: string; channel_points?: string[]; amount_sat?: number; max_fee_sat?: number }) =>
  request('/api/wallet/pay', { method: 'POST', body: JSON.stringify(payload) })

export const getLnChannels = () => request('/api/lnops/channels')
export const getLnChannelDBImpact = () => request('/api/lnops/channel-db-impact')
export const getLnPeers = () => request('/api/lnops/peers')
export const getLnChannelDetail = (channelPoint: string, limit = 30) =>
  request(`/api/lnops/channel/detail?channel_point=${encodeURIComponent(channelPoint)}&limit=${encodeURIComponent(String(limit))}`)
export const saveLnChannelNote = (payload: { channel_point: string; note: string; remote_pubkey?: string; peer_alias?: string; channel_id?: number; short_channel_id?: string }) =>
  request('/api/lnops/channel/notes', { method: 'POST', body: JSON.stringify(payload) })
export const updateLnChannelAutomation = (payload: {
  channel_id?: number
  channel_point: string
  automation_mode: 'normal' | 'parked' | 'close_candidate'
  fixed_fee_ppm?: number | null
  review_at?: string
  automation_note?: string
  restore_previous?: boolean
}) =>
  request('/api/lnops/channel/automation', { method: 'POST', body: JSON.stringify(payload) })
export const saveLnPeerNote = (payload: { remote_pubkey: string; note: string }) =>
  request('/api/lnops/peer/notes', { method: 'POST', body: JSON.stringify(payload) })
export const getLnChannelPeerRecommendations = (channelPoint: string, limit = 5) =>
  request(`/api/lnops/channel/peer-recommendations?channel_point=${encodeURIComponent(channelPoint)}&limit=${encodeURIComponent(String(limit))}`)
export const getNetworkAtlasMap = () => request('/api/lnops/network-map')
export const getNetworkAtlasConfig = () => request('/api/lnops/network-map/config')
export const updateNetworkAtlasConfig = (payload: { label?: string; lat?: string | null; lon?: string | null }) =>
  request('/api/lnops/network-map/config', { method: 'POST', body: JSON.stringify(payload) })
export const getGraphExplorerStatus = () => request('/api/lnops/graph-explorer/status')
export const getGraphExplorerStorage = () => request('/api/lnops/graph-explorer/storage')
export const updateGraphExplorerStorage = (payload: { history_retention_days?: number; history_max_bytes?: number; history_max_gb?: number }) =>
  request('/api/lnops/graph-explorer/storage', { method: 'POST', body: JSON.stringify(payload) })
export const cleanupGraphExplorerStorage = () =>
  request('/api/lnops/graph-explorer/storage/cleanup', { method: 'POST', body: JSON.stringify({}) })
export const searchGraphExplorerNodes = (params?: { q?: string; limit?: number }) =>
  request(`/api/lnops/graph-explorer/search${buildQuery(params)}`)
export const getGraphExplorerNodeGeneral = (pubkey: string) =>
  request(`/api/lnops/graph-explorer/nodes/${encodeURIComponent(pubkey)}/general`)
export const getGraphExplorerNodeChannels = (pubkey: string, params?: { limit?: number }) =>
  request(`/api/lnops/graph-explorer/nodes/${encodeURIComponent(pubkey)}/channels${buildQuery(params)}`)
export const getGraphExplorerNodeClosed = (pubkey: string, params?: { range?: string; limit?: number }) =>
  request(`/api/lnops/graph-explorer/nodes/${encodeURIComponent(pubkey)}/closed${buildQuery(params)}`)
export const getGraphExplorerNodeFees = (pubkey: string, params?: { range?: string }) =>
  request(`/api/lnops/graph-explorer/nodes/${encodeURIComponent(pubkey)}/fees${buildQuery(params)}`)
export const recomputeGraphExplorer = () =>
  request('/api/lnops/graph-explorer/recompute', { method: 'POST' })
export const getLnClosedChannels = () => request('/api/lnops/closed-channels')
export const getChannelRankings = (params?: { limit?: number; state?: string }) =>
  request(`/api/lnops/channel-ranking${buildQuery(params)}`)
export const getChannelRanking = (channelPoint: string) =>
  request(`/api/lnops/channel-ranking/${encodeURIComponent(channelPoint)}`)
export const recomputeChannelRankings = () =>
  request('/api/lnops/channel-ranking/recompute', { method: 'POST' })
export const getChannelOpenCandidates = (params?: { limit?: number }) =>
  request(`/api/lnops/channel-ranking/open-candidates${buildQuery(params)}`)
export const recomputeChannelOpenCandidates = () =>
  request('/api/lnops/channel-ranking/open-candidates/recompute', { method: 'POST' })
export const getCloseManagerStatus = () => request('/api/lnops/close-manager/status')
export const getCloseManagerSessions = (limit = 100) =>
  request(`/api/lnops/close-manager/sessions?limit=${encodeURIComponent(String(limit))}`)
export const getCloseManagerSession = (id: number) =>
  request(`/api/lnops/close-manager/sessions/${encodeURIComponent(String(id))}`)
export const getCloseManagerSessionEvents = (id: number, limit = 100) =>
  request(`/api/lnops/close-manager/sessions/${encodeURIComponent(String(id))}/events?limit=${encodeURIComponent(String(limit))}`)
export const recoverCloseManagerSession = (id: number) =>
  request(`/api/lnops/close-manager/sessions/${encodeURIComponent(String(id))}/recover`, { method: 'POST' })
export const forceCloseManagerSession = (id: number) =>
  request(`/api/lnops/close-manager/sessions/${encodeURIComponent(String(id))}/force-close`, { method: 'POST' })
export const bumpFeeCloseManagerSession = (id: number, payload: { preset?: 'economic' | 'normal' | 'urgent'; sat_per_vbyte?: number }) =>
  request(`/api/lnops/close-manager/sessions/${encodeURIComponent(String(id))}/bump-fee`, { method: 'POST', body: JSON.stringify(payload) })
export const getLnWatchtowers = () => request('/api/lnops/watchtower')
export const addLnWatchtower = (payload: { address: string }) =>
  request('/api/lnops/watchtower/add', { method: 'POST', body: JSON.stringify(payload) })
export const removeLnWatchtower = (payload: { pubkey: string; address?: string }) =>
  request('/api/lnops/watchtower/remove', { method: 'POST', body: JSON.stringify(payload) })
export const signLnMessage = (payload: { message: string }) =>
  request('/api/lnops/sign-message', { method: 'POST', body: JSON.stringify(payload) })
export type LnMacaroonPermission = {
  entity: string
  action: string
}
export type LnMacaroonPreset = {
  id: string
  label: string
  permissions: LnMacaroonPermission[]
}
export type LnMacaroonOptions = {
  presets: LnMacaroonPreset[]
  permissions: LnMacaroonPermission[]
}
export type BakeLnMacaroonPayload = {
  preset?: string
  permissions?: LnMacaroonPermission[]
  confirm_password?: string
}
export type LnMacaroonBakeResult = {
  file_name: string
  root_key_id: number
  macaroon_hex: string
  macaroon_base64: string
  permissions: string[]
}
export const getLnMacaroonOptions = () =>
  request('/api/lnops/macaroon/options') as Promise<LnMacaroonOptions>
export const bakeLnMacaroon = (payload: BakeLnMacaroonPayload) =>
  request('/api/lnops/macaroon/bake', { method: 'POST', body: JSON.stringify(payload) }) as Promise<LnMacaroonBakeResult>
export const getLnChanHeal = () => request('/api/lnops/channel/auto-heal')
export const updateLnChanHeal = (payload: { enabled?: boolean; interval_sec?: number }) =>
  request('/api/lnops/channel/auto-heal', { method: 'POST', body: JSON.stringify(payload) })
export const getLnHtlcManager = () => request('/api/lnops/channel/htlc-manager')
export const updateLnHtlcManager = (payload: {
  enabled?: boolean
  interval_minutes?: number
  interval_hours?: number
  min_htlc_sat?: number
  max_local_pct?: number
  run_now?: boolean
}) => request('/api/lnops/channel/htlc-manager', { method: 'POST', body: JSON.stringify(payload) })
export const getLnHtlcManagerLogs = (limit = 100) =>
  request(`/api/lnops/channel/htlc-manager/logs?limit=${encodeURIComponent(String(limit))}`)
export const getLnHtlcManagerFailed = (limit = 100) =>
  request(`/api/lnops/channel/htlc-manager/failed?limit=${encodeURIComponent(String(limit))}`)
export const getLnFailedPaymentsCleaner = () => request('/api/lnops/payments/clean-failed')
export const updateLnFailedPaymentsCleaner = (payload: {
  enabled?: boolean
  interval_hours?: number
  run_now?: boolean
}) => request('/api/lnops/payments/clean-failed', { method: 'POST', body: JSON.stringify(payload) })
export const getLnTorPeerChecker = () => request('/api/lnops/channel/tor-peers')
export const updateLnTorPeerChecker = (payload: {
  enabled?: boolean
  interval_hours?: number
  run_now?: boolean
}) => request('/api/lnops/channel/tor-peers', { method: 'POST', body: JSON.stringify(payload) })
export const getLnTorPeerCheckerLogs = (limit = 100) =>
  request(`/api/lnops/channel/tor-peers/logs?limit=${encodeURIComponent(String(limit))}`)
export const restoreLnScb = (payload: { multi_chan_backup: string; confirm_phrase: string }) =>
  request('/api/lnops/channel/scb/restore', { method: 'POST', body: JSON.stringify(payload) })
export const getNodeRetirementStatus = () => request('/api/lnops/node-retirement/status')
export const getNodeRetirementSessions = (limit = 50) =>
  request(`/api/lnops/node-retirement/sessions?limit=${encodeURIComponent(String(limit))}`)
export const createNodeRetirementSession = (payload: {
  source?: string
  dry_run?: boolean
  disclaimer_accepted?: boolean
  config?: Record<string, any>
  confirm_password?: string
}) => request('/api/lnops/node-retirement/sessions', { method: 'POST', body: JSON.stringify(payload) })
export const getNodeRetirementSession = (sessionID: string) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}`)
export const getNodeRetirementSessionEvents = (sessionID: string, limit = 100) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/events?limit=${encodeURIComponent(String(limit))}`)
export const getNodeRetirementSessionChannels = (sessionID: string) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/channels`)
export const getNodeRetirementSessionTransfer = (sessionID: string) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/transfer`)
export const confirmNodeRetirementCoopClose = (sessionID: string, payload?: { confirm_password?: string }) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/confirm-coop`, { method: 'POST', body: payload ? JSON.stringify(payload) : undefined })
export const decideNodeRetirementChannel = (sessionID: string, payload: { channel_point: string; decision: 'wait' | 'force_close'; confirm_password?: string }) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/decision`, { method: 'POST', body: JSON.stringify(payload) })
export const getSuccessionStatus = () => request('/api/lnops/succession/status')
export const getSuccessionConfig = () => request('/api/lnops/succession/config')
export const updateSuccessionConfig = (payload: {
  enabled?: boolean
  dry_run?: boolean
  destination_address?: string
  preapprove_fc_offline?: boolean
  preapprove_fc_stuck_htlc?: boolean
  stuck_htlc_threshold_sec?: number
  sweep_min_confs?: number
  sweep_sat_per_vbyte?: number
  check_period_days?: number
  reminder_period_days?: number
  confirm_password?: string
}) => request('/api/lnops/succession/config', { method: 'POST', body: JSON.stringify(payload) })
export const successionAlive = (payload: { source?: string }) =>
  request('/api/lnops/succession/alive', { method: 'POST', body: JSON.stringify(payload) })
export const successionSimulate = (payload: { action: 'alive' | 'not_alive'; source?: string }) =>
  request('/api/lnops/succession/simulate', { method: 'POST', body: JSON.stringify(payload) })
export const getAutofeeConfig = () => request('/api/lnops/autofee/config')
export const updateAutofeeConfig = (payload: {
  enabled?: boolean
  operation_mode?: string
  profile?: string
  lookback_days?: number
  run_interval_sec?: number
  cooldown_up_sec?: number
  cooldown_down_sec?: number
  step_cap_override?: number
  discovery_step_cap_down_override?: number
  stall_floor_relax_gap_frac_override?: number
  inbound_discount_max_ratio_override?: number
  inbound_discount_reach_out_ratio_override?: number
  inbound_discount_min_retained_spread_frac_override?: number
  outrate_floor_factor_low_override?: number
  soften_min_out_ratio_override?: number
  soften_max_drop_to_peg_frac_override?: number
  htlc_min_attempts_60m_override?: number
  htlc_policy_fail_rate_override?: number
  htlc_liquidity_fail_rate_override?: number
  rebal_cost_mode?: string
  native_seed_enabled?: boolean
  amboss_enabled?: boolean
  amboss_token?: string
  inbound_passive_enabled?: boolean
  discovery_enabled?: boolean
  explorer_enabled?: boolean
  idle_refresh_enabled?: boolean
  super_source_enabled?: boolean
  super_source_base_fee_msat?: number
  revfloor_enabled?: boolean
  circuit_breaker_enabled?: boolean
  extreme_drain_enabled?: boolean
  htlc_signal_enabled?: boolean
  htlc_mode?: string
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
export const refreshAutofeeReferences = (payload?: {
  dry_run?: boolean
  include_inbound?: boolean
  channel_point?: string
  channel_id?: number
  channel_id_str?: string
}) =>
  request('/api/lnops/autofee/refresh', { method: 'POST', body: JSON.stringify(payload ?? {}) })
export const getAutofeeStatus = () => request('/api/lnops/autofee/status')
export const getAutofeeResults = (params: number | {
  lines?: number
  runs?: number
  from?: string
  to?: string
} = 50) => {
  if (typeof params === 'number') {
    return request(`/api/lnops/autofee/results?lines=${params}`)
  }
  return request(`/api/lnops/autofee/results${buildQuery(params)}`)
}
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
export const previewOpenChannel = (payload: {
  local_funding_sat: number
  push_sat?: number
  sat_per_vbyte: number
}) => request('/api/lnops/channel/open-preview', { method: 'POST', body: JSON.stringify(payload) })
export const previewBatchOpenChannels = (payload: {
  channels: Array<{
    local_funding_sat: number
  }>
  sat_per_vbyte: number
}) => request('/api/lnops/channel/open-batch-preview', { method: 'POST', body: JSON.stringify(payload) })
export const openChannel = (payload: {
  peer_address: string
  local_funding_sat: number
  push_sat?: number
  close_address?: string
  sat_per_vbyte?: number
  private?: boolean
  outpoints?: string[]
}) => request('/api/lnops/channel/open', { method: 'POST', body: JSON.stringify(payload) })
export const openBatchChannels = (payload: {
  channels: Array<{
    peer_address?: string
    pubkey?: string
    host?: string
    local_funding_sat: number
    close_address?: string
    private?: boolean
  }>
  sat_per_vbyte?: number
}) => request('/api/lnops/channel/open-batch', { method: 'POST', body: JSON.stringify(payload) })
export const bumpPendingOpenChannel = (payload: { channel_point: string; preset?: 'economic' | 'normal' | 'urgent'; sat_per_vbyte?: number }) =>
  request('/api/lnops/channel/pending-open/bump-fee', { method: 'POST', body: JSON.stringify(payload) })
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
export const getBalancedOpenStatus = () => request('/api/lnops/balanced-open/status')
export const getBalancedOpenSessions = (params?: {
  limit?: number
  state?: string
  role?: string
  peer_pubkey?: string
}) => request(`/api/lnops/balanced-open/sessions${buildQuery(params)}`)
export const getBalancedOpenSession = (sessionId: string) =>
  request(`/api/lnops/balanced-open/sessions/${encodeURIComponent(sessionId)}`)
export const getBalancedOpenSessionEvents = (sessionId: string, params?: { limit?: number }) =>
  request(`/api/lnops/balanced-open/sessions/${encodeURIComponent(sessionId)}/events${buildQuery(params)}`)
export const createBalancedOpenSession = (payload: {
  peer_address: string
  capacity_sat: number
  fee_rate_sat_vb?: number
  private?: boolean
  close_address?: string
  role?: 'initiator' | 'accepter'
  metadata?: Record<string, unknown>
}) => request('/api/lnops/balanced-open/sessions', { method: 'POST', body: JSON.stringify(payload) })
export const proposeBalancedOpenSession = (sessionId: string) =>
  request(`/api/lnops/balanced-open/sessions/${encodeURIComponent(sessionId)}/propose`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
export const acceptBalancedOpenSession = (sessionId: string) =>
  request(`/api/lnops/balanced-open/sessions/${encodeURIComponent(sessionId)}/accept`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
export const executeBalancedOpenSession = (sessionId: string) =>
  request(`/api/lnops/balanced-open/sessions/${encodeURIComponent(sessionId)}/execute`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
export const retryBalancedOpenSessionBroadcast = (sessionId: string) =>
  request(`/api/lnops/balanced-open/sessions/${encodeURIComponent(sessionId)}/retry-broadcast`, {
    method: 'POST',
    body: JSON.stringify({}),
  })
export const recoverBalancedOpenSession = (sessionId: string, payload?: { sat_per_vbyte?: number }) =>
  request(`/api/lnops/balanced-open/sessions/${encodeURIComponent(sessionId)}/recover`, {
    method: 'POST',
    body: JSON.stringify(payload ?? {}),
  })
export const cancelBalancedOpenSession = (sessionId: string, payload?: { reason?: string }) =>
  request(`/api/lnops/balanced-open/sessions/${encodeURIComponent(sessionId)}/cancel`, {
    method: 'POST',
    body: JSON.stringify(payload ?? {}),
  })

export const getChatMessages = (peerPubkey: string, limit = 200) =>
  request(`/api/chat/messages?peer_pubkey=${encodeURIComponent(peerPubkey)}&limit=${limit}`)

export const getChatInbox = () =>
  request('/api/chat/inbox')

export const sendChatMessage = (payload: { peer_pubkey: string; message: string; amount_sat?: number }) =>
  request('/api/chat/send', { method: 'POST', body: JSON.stringify(payload) })

export const getNotifications = (limit = 200) =>
  request(`/api/notifications?limit=${limit}`)

export const getAuditEvents = (params?: {
  limit?: number
  action?: string
  session_id?: string
  target?: string
}) => request(`/api/audit/events${buildQuery(params)}`)

export const getTelegramNotifications = () =>
  request('/api/notifications/telegram')

export const updateTelegramNotifications = (payload: {
  bot_token?: string
  chat_id?: string
  scb_backup_enabled?: boolean
  activity_mirror_enabled?: boolean
  autofee_summary_enabled?: boolean
  summary_enabled?: boolean
  summary_interval_min?: number
  system_summary_enabled?: boolean
  system_summary_interval_min?: number
}) => request('/api/notifications/telegram', { method: 'POST', body: JSON.stringify(payload) })

export const getTelegramBackupConfig = () =>
  request('/api/notifications/backup/telegram')

export const updateTelegramBackupConfig = (payload: { bot_token?: string; chat_id?: string }) =>
  request('/api/notifications/backup/telegram', { method: 'POST', body: JSON.stringify(payload) })

export const testTelegramBackup = () =>
  request('/api/notifications/backup/telegram/test', { method: 'POST' })

export const getTerminalStatus = () => request('/api/terminal/status')

export const rotateTerminalCredential = (payload: { confirm_password?: string } = {}) =>
  request('/api/terminal/credential/rotate', { method: 'POST', body: JSON.stringify(payload) })

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

export const upsertUtxoMetadata = (payload: {
  outpoint: string
  label?: string
  tag?: string
  color?: string
  group_id?: string
}) =>
  request('/api/onchain/utxos/metadata', {
    method: 'POST',
    body: JSON.stringify(payload)
  })

export const lockUtxos = (payload: { outpoints: string[]; expiry_sec?: number; confirm_password?: string }) =>
  request('/api/onchain/utxos/lock', { method: 'POST', body: JSON.stringify(payload) })

export const bumpUtxoFee = (payload: { outpoint: string; sat_per_vbyte?: number; target_conf?: number; budget_sat?: number; confirm_password?: string }) =>
  request('/api/onchain/utxos/bump', { method: 'POST', body: JSON.stringify(payload) })

export const getProvenanceGraph = (params?: {
  mode?: 'live' | 'ours' | 'all' | 'lineage'
  limit?: number
  since?: number
  root?: string
  hops?: number
  include_external?: boolean
  max_external?: number
}) => {
  const q = buildQuery(params)
  return request(`/api/onchain/provenance${q}`)
}
export const getProvenanceStatus = () => request('/api/onchain/provenance/status')
export const getProvenanceHealth = () => request('/api/onchain/provenance/health')
export const rebuildProvenance = (full = false) =>
  request(`/api/onchain/provenance/rebuild${full ? '?full=true' : ''}`, { method: 'POST' })

export const unlockUtxos = (payload: { outpoints: string[]; confirm_password?: string }) =>
  request('/api/onchain/utxos/unlock', { method: 'POST', body: JSON.stringify(payload) })

export const listUtxoGroups = () => request('/api/onchain/utxos/groups')

export const upsertUtxoGroup = (payload: {
  id?: string
  name?: string
  color?: string
  outpoints?: string[]
}) => request('/api/onchain/utxos/groups', { method: 'POST', body: JSON.stringify(payload) })

export const assignUtxoGroup = (groupId: string, payload: { outpoints: string[]; detach?: boolean }) =>
  request(`/api/onchain/utxos/groups/${encodeURIComponent(groupId)}/assign`, {
    method: 'POST',
    body: JSON.stringify(payload)
  })

export const deleteUtxoGroup = (groupId: string) =>
  request(`/api/onchain/utxos/groups/${encodeURIComponent(groupId)}`, { method: 'DELETE' })

export const getReportsRange = (range: string) =>
  request(`/api/reports/range?range=${encodeURIComponent(range)}`)
export const getReportsCustom = (from: string, to: string) =>
  request(`/api/reports/custom?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`)
export const getReportsSummary = (range: string) =>
  request(`/api/reports/summary?range=${encodeURIComponent(range)}`)
export const getReportsSummaryCustom = (from: string, to: string) =>
  request(`/api/reports/summary/custom?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`)
export const getReportsLive = () => request('/api/reports/live')
export const getReportsMovementLive = () => request('/api/reports/movement/live')
export const getReportsConfig = () => request('/api/reports/config')
export const updateReportsConfig = (payload: {
  live_timeout_sec?: number | null
  live_lookback_hours?: number | null
  run_timeout_sec?: number | null
}) => request('/api/reports/config', { method: 'POST', body: JSON.stringify(payload) })

export const getRebalanceConfig = () => request('/api/rebalance/config')
export type AutomationIntentConfig = {
  mode: 'off' | 'shadow' | 'enforce'
  refill_target_ttl_sec: number
  protect_fee_floor_ttl_sec: number
  refill_score_multiplier: number
  min_confidence: number
  history_retention_days: number
}

export type AutomationIntent = {
  id: number
  channel_id: number
  channel_id_str?: string
  channel_point?: string
  producer: 'autofee' | 'rebalance'
  consumer: 'autofee' | 'rebalance'
  kind: 'refill_target' | 'protect_fee_floor'
  confidence: number
  reason_code: string
  evidence?: Record<string, unknown>
  score_multiplier?: number
  fee_floor_ppm?: number
  producer_profile?: string
  producer_node_class?: string
  producer_liquidity_class?: string
  active: boolean
  first_seen_at: string
  last_seen_at: string
  expires_at: string
}

export type AutomationIntentEvent = {
  id: number
  intent_id?: number
  channel_id: number
  channel_id_str?: string
  producer: 'autofee' | 'rebalance'
  consumer: 'autofee' | 'rebalance'
  kind: 'refill_target' | 'protect_fee_floor'
  event_type: 'published' | 'applied' | 'resolved' | string
  metadata?: Record<string, unknown>
  occurred_at: string
}

export const getAutomationIntentConfig = () =>
  request('/api/lnops/automation-intents/config') as Promise<AutomationIntentConfig>
export const updateAutomationIntentConfig = (payload: Partial<Pick<AutomationIntentConfig, 'mode' | 'refill_score_multiplier' | 'min_confidence'>>) =>
  request('/api/lnops/automation-intents/config', { method: 'POST', body: JSON.stringify(payload) }) as Promise<AutomationIntentConfig>
export const getAutomationIntents = (active = true) =>
  request(`/api/lnops/automation-intents?active=${active}`) as Promise<{ items: AutomationIntent[] }>
export const getAutomationIntentHistory = (limit = 200) =>
  request(`/api/lnops/automation-intents/history?limit=${Math.max(1, Math.min(1000, Math.round(limit)))}`) as Promise<{ items: AutomationIntentEvent[] }>
export const applyRebalanceProfile = (profile: string) =>
  request('/api/rebalance/profile', { method: 'POST', body: JSON.stringify({ profile }) })
export const getRebalanceConfigSnapshots = () => request('/api/rebalance/config/snapshots')
export const saveRebalanceConfigSnapshot = () =>
  request('/api/rebalance/config/snapshots', { method: 'POST', body: JSON.stringify({}) })
export const restoreRebalanceConfigSnapshot = (id: number) =>
  request(`/api/rebalance/config/snapshots/${id}/restore`, { method: 'POST', body: JSON.stringify({}) })
export const deleteRebalanceConfigSnapshot = (id: number) =>
  request(`/api/rebalance/config/snapshots/${id}`, { method: 'DELETE' })
export const updateRebalanceConfig = (payload: {
  auto_enabled?: boolean
  scheduler_mode?: string
  sovereign_candidate_scope?: string
  sovereign_max_jobs_per_cycle?: number
  sovereign_min_expected_profit_sat?: number
  sovereign_low_success_min_rate?: number
  sovereign_low_success_min_profit_cost_ratio?: number
  sovereign_budget_efficiency_min_ratio?: number
  sovereign_route_dead_source_share?: number
  sovereign_risk_score_floor?: number
  sovereign_gain_v3_cold_start_pct?: number
  fast_path_max_timeout_sec?: number
  sovereign_top_bucket_pct?: number
  sovereign_attribution_window_hours?: number
  sovereign_slow_seller_window_hours?: number
  sovereign_target_source_quarantine_hours?: number
  sovereign_structural_cooldown_repeat_hours?: number
  sovereign_exploration_slot_pct?: number
  sovereign_ev_weighted_scoring?: boolean
  sovereign_source_opportunity_cost_enabled?: boolean
  sovereign_slow_seller_enabled?: boolean
  scan_interval_sec?: number
  deadband_pct?: number
  source_min_local_pct?: number
  econ_ratio?: number
  econ_ratio_max_ppm?: number
  fee_limit_ppm?: number
  lost_profit?: boolean
  fail_tolerance_ppm?: number
  roi_min?: number
  daily_budget_pct?: number
  budget_mode?: string
  budget_unlimited?: boolean
  budget_auto_only?: boolean
  manual_reserve_enabled?: boolean
  manual_reserve_mode?: string
  manual_reserve_value?: number
  max_concurrent?: number
  min_amount_sat?: number
  max_amount_sat?: number
  min_split_enabled?: boolean
  min_probe_sat?: number
  min_execute_sat?: number
  mpp_enabled?: boolean
  mpp_max_shards?: number
  mpp_parallelism?: number
  mpp_min_shard_sat?: number
  mpp_round_timeout_sec?: number
  mpp_auto_only?: boolean
  fee_ladder_steps?: number
  amount_probe_steps?: number
  amount_probe_adaptive?: boolean
  attempt_timeout_sec?: number
  rebalance_timeout_sec?: number
  manual_restart_watch?: boolean
  manual_restart_ignore_economic_gates?: boolean
  cooldown_probe_enabled?: boolean
  mc_half_life_sec?: number
  payback_mode_flags?: number
  fresh_paid_liquidity_lock_enabled?: boolean
  fresh_paid_liquidity_lock_hours?: number
  unlock_days?: number
  critical_release_pct?: number
  critical_min_sources?: number
  critical_min_available_sats?: number
  critical_cycles?: number
  rebalance_cost_floor_ppm?: number
  source_min_payback_progress?: number
  mission_control_reinforce?: boolean
  gain_model_version?: number
  velocity_weight?: number
  autofee_settling_window_sec?: number
  autofee_settling_multiplier?: number
  delegated_fast_path_enabled?: boolean
  delegated_fast_path_strict_payback?: boolean
  auto_target_enabled?: boolean
  auto_target_max_pct?: number
  auto_target_min_pct?: number
  auto_target_step_pct?: number
  auto_target_eval_interval_hours?: number
  auto_target_max_ups_per_cycle?: number
  auto_target_max_local_sat?: number
  auto_target_min_drain_rate_sat_per_hr?: number
  auto_target_min_revenue_7d_sat?: number
  auto_target_up_success_threshold?: number
  auto_target_down_success_threshold?: number
  auto_target_drain_first_multiplier?: number
  auto_target_up_sellthrough_factor?: number
  auto_target_down_sellthrough_factor?: number
  auto_target_max_downs_per_cycle?: number
}) => request('/api/rebalance/config', { method: 'POST', body: JSON.stringify(payload) })
export const getRebalanceOverview = () => request('/api/rebalance/overview')
export const getRebalanceChannels = () => request('/api/rebalance/channels')
export const getRebalancePairStats = (targetChannelPoint: string) =>
  request(`/api/rebalance/pair-stats?target_channel_point=${encodeURIComponent(targetChannelPoint)}`)
export const getRebalanceQueue = () => request('/api/rebalance/queue')
export const getRebalanceHistory = (limit = 0) =>
  limit > 0 ? request(`/api/rebalance/history?limit=${limit}`) : request('/api/rebalance/history')
export const getRebalanceSovereignHistory = (limit = 0, includeDecisions = false) => {
  const params = new URLSearchParams()
  if (limit > 0) params.set('limit', String(limit))
  if (includeDecisions) params.set('include_decisions', '1')
  const query = params.toString()
  return request(query ? `/api/rebalance/sovereign-history?${query}` : '/api/rebalance/sovereign-history')
}
export const getRebalanceAutoTargetHistory = (opts: { channelId?: number; limit?: number; since?: string } = {}) => {
  const params = new URLSearchParams()
  if (opts.channelId) params.set('channel_id', String(opts.channelId))
  if (opts.limit && opts.limit > 0) params.set('limit', String(opts.limit))
  if (opts.since) params.set('since', opts.since)
  const query = params.toString()
  return request(query ? `/api/rebalance/auto-target/history?${query}` : '/api/rebalance/auto-target/history')
}
export const resetRebalanceMissionControl = () =>
  request('/api/rebalance/mission-control/reset', { method: 'POST', body: JSON.stringify({}) })
export type RebalanceRunPayload = {
  channel_id?: number
  channel_point: string
  target_outbound_pct?: number
  auto_restart?: boolean
  amount_sat?: number
  fee_limit_ppm?: number
}

export type RebalanceRunPreview = {
  channel_id: number
  channel_point: string
  peer_alias?: string
  target_outbound_pct: number
  deficit_sat: number
  effective_amount_sat: number
  default_amount_sat: number
  effective_fee_limit_ppm: number
  default_fee_limit_ppm: number
  outgoing_fee_ppm: number
  max_fee_sat: number
  config_max_amount_sat: number
  uses_default_amount: boolean
  uses_default_fee: boolean
  amount_overrides_config: boolean
  amount_clamped: boolean
  fee_exceeds_outgoing: boolean
}

export const previewRebalance = (payload: RebalanceRunPayload) =>
  request('/api/rebalance/run/preview', { method: 'POST', body: JSON.stringify(payload) }) as Promise<RebalanceRunPreview>

export const runRebalance = (payload: RebalanceRunPayload) =>
  request('/api/rebalance/run', { method: 'POST', body: JSON.stringify(payload) })
export const stopRebalance = (payload: { job_id: number }) =>
  request('/api/rebalance/stop', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelTarget = (payload: {
  channel_id?: number
  channel_point: string
  target_outbound_pct?: number
  use_default_econ_ratio?: boolean
  econ_ratio_override?: number
  auto_bypass_cost_gate?: boolean
}) =>
  request('/api/rebalance/channel/target', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelAuto = (payload: { channel_id?: number; channel_point: string; auto_enabled: boolean }) =>
  request('/api/rebalance/channel/auto', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelAutoTarget = (payload: { channel_id?: number; channel_point: string; managed: boolean }) =>
  request('/api/rebalance/channel/auto-target', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelGuaranteed = (payload: { channel_id_str: string; channel_point: string; enabled: boolean }) =>
  request('/api/rebalance/channel/guaranteed', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelManualRestart = (payload: { channel_id?: number; channel_point: string; enabled: boolean }) =>
  request('/api/rebalance/channel/manual-restart', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceExclude = (payload: { channel_id?: number; channel_point: string; excluded: boolean }) =>
  request('/api/rebalance/channel/exclude', { method: 'POST', body: JSON.stringify(payload) })

export const getDepixConfig = (params?: { user_key?: string; timezone?: string }) =>
  request(`/api/depix/config${buildQuery(params)}`)
export const createDepixOrder = (payload: {
  user_key: string
  timezone: string
  liquid_address: string
  amount_brl: string
}) => request('/api/depix/orders', { method: 'POST', body: JSON.stringify(payload) })
export const getDepixOrders = (params: { user_key: string; limit?: number }) =>
  request(`/api/depix/orders${buildQuery(params)}`)
export const getDepixOrder = (id: number, params: { user_key: string; refresh?: boolean }) =>
  request(`/api/depix/orders/${encodeURIComponent(String(id))}${buildQuery(params)}`)

export const getShortcuts = () => request('/api/shortcuts')
export const createShortcut = (payload: { name: string; url: string; emoji: string }) =>
  request('/api/shortcuts', { method: 'POST', body: JSON.stringify(payload) })
export const deleteShortcut = (id: number) =>
  request(`/api/shortcuts/${encodeURIComponent(String(id))}`, { method: 'DELETE' })

export type StorageTarget = {
  mount: string
  source?: string
  fstype?: string
  total_gb?: number
  used_gb?: number
  free_gb?: number
  used_percent?: number
  eligible: boolean
  reason?: string
  suggested_path: string
}

export const getApps = () => request('/api/apps')
export const getAppStorageTargets = (app: string) =>
  request(`/api/apps/storage-targets${buildQuery({ app })}`)
export const getElectrsStatus = () => request('/api/apps/electrs/status')
export type CpuMinerStatus = {
  installed: boolean
  running: boolean
  address: string
  worker: string
  pool_mode: string
  pool_label: string
  threads: number
  max_threads: number
  host_cpu_count: number
  hashrate_hs: number
  shares_accepted: number
  shares_rejected: number
  best_difficulty: number
  pool_hashrate_hs: number
  cpu_percent: number
}
export const getCpuMinerStatus = (): Promise<CpuMinerStatus> => request('/api/apps/cpuminer/status')
export const setCpuMinerThreads = (threads: number) =>
  request('/api/apps/cpuminer/threads', { method: 'POST', body: JSON.stringify({ threads }) })
export const setCpuMinerConfig = (payload: {
  pool_mode: string
  address?: string
  worker?: string
  use_node_address?: boolean
}) => request('/api/apps/cpuminer/config', { method: 'POST', body: JSON.stringify(payload) })

// Taproot Assets (tapd) — standalone, on-chain only. All calls forward the raw
// tapcli JSON output, so return types are intentionally loose.
export const getTapdInfo = () => request('/api/apps/tapd/info')
export const getTapdAssets = () => request('/api/apps/tapd/assets')
export const newTapdAddress = (payload: { asset_id?: string; group_key?: string; amount: number }) =>
  request('/api/apps/tapd/address', { method: 'POST', body: JSON.stringify(payload) })
export const tapdUniverseSync = (payload: { universe_host: string; group_key?: string; asset_id?: string }) =>
  request('/api/apps/tapd/universe-sync', { method: 'POST', body: JSON.stringify(payload) })
export const getTapdDiscover = (host: string) => request(`/api/apps/tapd/discover${buildQuery({ host })}`)
export const tapdMint = (payload: { name: string; supply: number; decimal_display?: number; grouped?: boolean; group_key?: string; meta?: string }) =>
  request('/api/apps/tapd/mint', { method: 'POST', body: JSON.stringify(payload) })
export const tapdMintFinalize = (payload?: { fee_rate?: number }) =>
  request('/api/apps/tapd/mint-finalize', { method: 'POST', body: JSON.stringify(payload || {}) })
export const tapdDecodeAddr = (payload: { addr: string }) =>
  request('/api/apps/tapd/decode-addr', { method: 'POST', body: JSON.stringify(payload) })
export const tapdSend = (payload: { addr: string; fee_rate?: number }) =>
  request('/api/apps/tapd/send', { method: 'POST', body: JSON.stringify(payload) })

export type LoopTerms = {
  loop_out_min_sat: number
  loop_out_max_sat: number
  loop_in_min_sat: number
  loop_in_max_sat: number
}

export type LoopStatus = {
  installed: boolean
  running: boolean
  version?: string
  network?: string
  pending_count: number
  terms?: LoopTerms
  autoloop_enabled: boolean
}

export type LoopQuotePayload = {
  direction: 'out' | 'in'
  amount_sat: number
  conf_target?: number
  last_hop_pubkey?: string
  fast?: boolean
  routing_fee_limit_ppm?: number
  outgoing_channel_ids?: string[]
}

export type LoopQuote = {
  direction: 'out' | 'in'
  amount_sat: number
  conf_target: number
  swap_fee_sat: number
  onchain_fee_sat: number
  prepay_amount_sat?: number
  routing_fee_limit_sat?: number
  prepay_routing_limit_sat?: number
  estimated_fee_sat: number
  routing_estimate_available: boolean
  routing_estimate_source?: 'graph' | 'invoice_routes' | 'history'
  routing_estimate_samples?: number
  estimated_routing_fee_sat?: number
  estimated_all_in_fee_sat?: number
  recommended_max_miner_fee_sat: number
  cltv_delta: number
  expires_at: string
}

export type LoopSwap = {
  id: string
  type: string
  state: string
  failure_reason?: string
  amount_sat: number
  initiation_time: number
  last_update_time: number
  cost_server_sat: number
  cost_onchain_sat: number
  cost_offchain_sat: number
  outgoing_channel_ids?: string[]
  label?: string
}

export type LoopSwapPayload = LoopQuotePayload & {
  destination_address?: string
  outgoing_channel_ids?: string[]
  approved_swap_fee_sat: number
  approved_onchain_fee_sat: number
  approved_routing_fee_limit_sat: number
  max_miner_fee_sat: number
  confirm_password?: string
}

export const getLoopStatus = (): Promise<LoopStatus> => request('/api/apps/loop/status')
export const getLoopSwaps = (limit = 100): Promise<{ swaps: LoopSwap[] }> =>
  request(`/api/apps/loop/swaps${buildQuery({ limit })}`)
export const getLoopQuote = (payload: LoopQuotePayload): Promise<LoopQuote> =>
  request('/api/apps/loop/quote', { method: 'POST', body: JSON.stringify(payload) })
export const startLoopSwap = (payload: LoopSwapPayload) =>
  request('/api/apps/loop/swap', { method: 'POST', body: JSON.stringify(payload) })

export type LoopOutBRLNRequest = {
  lightning_address: string
  total_sat: number
  tranche_sat: number
  interval_seconds: number
  timeout_seconds: number
  max_fee_ppm: number
  min_local_percent: number
  comment?: string
  selected_channel_ids?: string[]
  suppress_failed_telegram?: boolean
  strike_return_enabled?: boolean
  confirm_password?: string
}

export type LoopOutBRLNChannelPreview = {
  channel_id: string
  channel_point: string
  peer_alias: string
  remote_pubkey: string
  capacity_sat: number
  local_balance_sat: number
  local_percent: number
  reserve_target_sat: number
  drainable_sat: number
  eligible_first: boolean
  reason?: string
}

export type LoopOutBRLNPreview = {
  lightning_address: string
  total_sat: number
  tranche_sat: number
  last_tranche_sat: number
  estimated_parts: number
  max_fee_total_sat: number
  total_drainable_sat: number
  can_start: boolean
  warnings?: string[]
  channels: LoopOutBRLNChannelPreview[]
}

export type LoopOutBRLNAddressValidation = {
  lightning_address: string
  min_sendable_msat: number
  max_sendable_msat: number
  comment_allowed: number
}

export type LoopOutBRLNJob = {
  id: number
  lightning_address: string
  total_sat: number
  tranche_sat: number
  interval_seconds: number
  timeout_seconds: number
  max_fee_ppm: number
  min_local_percent: number
  comment?: string
  selected_channel_ids?: string[]
  suppress_failed_telegram: boolean
  strike_return_enabled: boolean
  status: string
  sent_sat: number
  fee_sat: number
  attempt_count: number
  retry_round: number
  last_error?: string
  next_attempt_at: string
  created_at: string
  updated_at: string
  started_at?: string
  completed_at?: string
}

export type LoopOutBRLNPayment = {
  id: number
  job_id: number
  sequence_no: number
  retry_round: number
  attempt_no: number
  amount_sat: number
  payment_hash?: string
  status: string
  fee_sat: number
  fee_msat: number
  channel_id?: string
  channel_point?: string
  channel_alias?: string
  failure_reason?: string
  created_at: string
  updated_at: string
  completed_at?: string
}

export type LoopOutBRLNEvent = {
  id: number
  job_id: number
  kind: string
  level: string
  message: string
  metadata?: Record<string, unknown>
  created_at: string
}

export type LoopOutBRLNStatus = {
  installed: boolean
  enabled: boolean
  active_job?: LoopOutBRLNJob
}

export type LoopOutBRLNJobDetail = {
  job: LoopOutBRLNJob
  payments: LoopOutBRLNPayment[]
  events: LoopOutBRLNEvent[]
  strike_return?: LoopOutBRLNStrikeReturn
}

export type LoopOutBRLNStrikeReturn = {
  id: number
  job_id: number
  automatic: boolean
  status: string
  amount_sat: number
  btc_address?: string
  quote_id?: string
  payment_id?: string
  txid?: string
  fee_sat: number
  estimated_delivery_minutes: number
  last_error?: string
  next_check_at: string
  created_at: string
  updated_at: string
  completed_at?: string
}

export type LoopOutBRLNStrikeStatus = {
  configured: boolean
}

export const getLoopOutBRLNStatus = (): Promise<LoopOutBRLNStatus> => request('/api/apps/loopout-brln/status')
export const getLoopOutBRLNStrikeStatus = (): Promise<LoopOutBRLNStrikeStatus> =>
  request('/api/apps/loopout-brln/strike/status')
export const connectLoopOutBRLNStrike = (apiKey: string, confirmPassword?: string): Promise<LoopOutBRLNStrikeStatus> =>
  request('/api/apps/loopout-brln/strike/connect', {
    method: 'POST',
    body: JSON.stringify({ api_key: apiKey, confirm_password: confirmPassword || '' })
  })
export const disconnectLoopOutBRLNStrike = (confirmPassword?: string): Promise<LoopOutBRLNStrikeStatus> =>
  request('/api/apps/loopout-brln/strike/disconnect', {
    method: 'POST',
    body: JSON.stringify({ confirm_password: confirmPassword || '' })
  })
export const validateLoopOutBRLNAddress = (lightningAddress: string, signal?: AbortSignal): Promise<LoopOutBRLNAddressValidation> =>
  request('/api/apps/loopout-brln/lightning-address/validate', {
    method: 'POST',
    body: JSON.stringify({ lightning_address: lightningAddress }),
    signal
  })
export const previewLoopOutBRLN = (payload: LoopOutBRLNRequest): Promise<LoopOutBRLNPreview> =>
  request('/api/apps/loopout-brln/preview', { method: 'POST', body: JSON.stringify(payload) })
export const getLoopOutBRLNJobs = (limit = 50): Promise<{ jobs: LoopOutBRLNJob[] }> =>
  request(`/api/apps/loopout-brln/jobs${buildQuery({ limit })}`)
export const getLoopOutBRLNJob = (id: number): Promise<LoopOutBRLNJobDetail> =>
  request(`/api/apps/loopout-brln/jobs/${id}`)
export const createLoopOutBRLNJob = (payload: LoopOutBRLNRequest): Promise<LoopOutBRLNJob> =>
  request('/api/apps/loopout-brln/jobs', { method: 'POST', body: JSON.stringify(payload) })
export const pauseLoopOutBRLNJob = (id: number): Promise<LoopOutBRLNJob> =>
  request(`/api/apps/loopout-brln/jobs/${id}/pause`, { method: 'POST', body: JSON.stringify({}) })
export const resumeLoopOutBRLNJob = (id: number, confirmPassword?: string): Promise<LoopOutBRLNJob> =>
  request(`/api/apps/loopout-brln/jobs/${id}/resume`, { method: 'POST', body: JSON.stringify({ confirm_password: confirmPassword || '' }) })
export const cancelLoopOutBRLNJob = (id: number): Promise<LoopOutBRLNJob> =>
  request(`/api/apps/loopout-brln/jobs/${id}/cancel`, { method: 'POST', body: JSON.stringify({}) })
export const requestLoopOutBRLNStrikeReturn = (id: number, confirmPassword?: string): Promise<LoopOutBRLNStrikeReturn> =>
  request(`/api/apps/loopout-brln/jobs/${id}/strike-return`, {
    method: 'POST',
    body: JSON.stringify({ confirm_password: confirmPassword || '' })
  })
export type MagmaOrder = {
  id: string
  status: string
  buyer_pubkey: string
  buyer_alias?: string
  offer_id: string
  size_sat: number
  revenue_sat: number
  buyer_pays_sat: number
  amboss_fee_ppm: number
  price_fixed_sat: number
  price_variable_sat: number
  price_ppm: number
  fee_rate_cap_ppm: number
  base_fee_cap_sat: number
  commitment_blocks: number
  blocks_until_can_be_closed?: number
  closed_blocks_before_min?: number
  fee_above_cap_seconds?: number
  payment_status?: string
  payment_hash?: string
  channel_scid?: string
  channel_point?: string
  created_at?: string
  updated_at?: string
  is_automated: boolean
  chat_enabled: boolean
  cancellation_reason?: string
  seller_close_side?: string
  buyer_close_side?: string
  local_state?: string
  funding_txid?: string
  last_error?: string
}

export type MagmaSettings = {
  installed: boolean
  enabled: boolean
  mode: string
  poll_interval_sec: number
  notify_telegram: boolean
}

export type MagmaTokenState = {
  configured: boolean
  expires_at?: string
  days_to_expiry?: number
  expired: boolean
  expiring_soon: boolean
}

export type MagmaMarketSummary = {
  enabled: boolean
  has_active_offers: boolean
  pending_seller_orders: number
  pending_buyer_orders: number
}

export type MagmaCapacity = {
  confirmed_sat: number
  committed_sat: number
  committed_orders: number
  available_sat: number
}

export type MagmaPolicy = {
  min_revenue_sat: number
  min_price_ppm: number
  min_price_ppm_per_day: number
  min_fee_rate_cap_ppm: number
  min_channel_size_sat: number
  max_channel_size_sat: number
  max_commitment_days: number
  max_sat_per_vbyte: number
  max_onchain_cost_pct: number
  min_onchain_reserve_sat: number
  max_concurrent_opens: number
  max_daily_orders: number
  max_daily_size_sat: number
  auto_reject_declined: boolean
}

export const updateMagmaPolicy = (payload: Partial<MagmaPolicy>): Promise<MagmaPolicy> =>
  request('/api/apps/magma-sales/policy', { method: 'POST', body: JSON.stringify(payload) })

export type MagmaPnL = {
  sales_count: number
  revenue_sat: number
  onchain_cost_sat: number
  onchain_cost_resolved: number
  net_sat: number
  pending_revenue_sat: number
  pending_count: number
  onchain_cost_already_in_reports: boolean
}

export type MagmaBackfillReport = {
  invoices_found: number
  matched_orders: number
  already_stamped: number
  unmatched_invoices: number
  revenue_sat: number
  oldest_settled_at?: string
  newest_settled_at?: string
  applied: boolean
  stamped: number
  reports_rerun_from?: string
  reports_rerun_to?: string
  notes?: string[]
}

export const previewMagmaBackfill = (): Promise<MagmaBackfillReport> =>
  request('/api/apps/magma-sales/backfill')
export const applyMagmaBackfill = (): Promise<MagmaBackfillReport> =>
  request('/api/apps/magma-sales/backfill', { method: 'POST', body: JSON.stringify({}) })

export type MagmaOfferCondition = {
  condition: string
  operator: string
  value: string
}

export type MagmaOffer = {
  id?: string
  status?: string
  side?: string
  total_size_sat: number
  min_size_sat: number
  max_size_sat: number
  fee_rate_ppm: number
  base_fee_sat: number
  fee_rate_cap_ppm: number
  base_fee_cap_sat: number
  min_block_length: number
  conditions?: MagmaOfferCondition[]
  seller_score?: string
  fixed_fee_mode: 'manual' | 'automatic'
  onchain_priority?: string
  onchain_multiplier?: number
}

export type MagmaOfferConflict = {
  blocking: boolean
  message: string
}

export type MagmaOffersView = {
  offers: MagmaOffer[]
  conflicts?: Record<string, MagmaOfferConflict[]>
  condition_options: string[]
  operator_options: string[]
  mode_warning?: string
}

export const getMagmaOffers = (): Promise<MagmaOffersView> =>
  request('/api/apps/magma-sales/offers')
export const saveMagmaOffer = (offer: MagmaOffer): Promise<MagmaOffersView> =>
  request('/api/apps/magma-sales/offers', { method: 'POST', body: JSON.stringify(offer) })
export const toggleMagmaOffer = (id: string): Promise<MagmaOffersView> =>
  request(`/api/apps/magma-sales/offers/${encodeURIComponent(id)}/toggle`, {
    method: 'POST',
    body: JSON.stringify({})
  })

export type MagmaChannelCommitment = {
  order_id: string
  channel_point: string
  channel_scid?: string
  buyer_alias?: string
  buyer_pubkey?: string
  size_sat: number
  revenue_sat: number
  fee_rate_cap_ppm: number
  base_fee_cap_sat: number
  blocks_remaining?: number
  commitment_blocks: number
  magma_status: string
}

// Always answers, with an empty list when the app is absent: the channel list
// must render whether or not Magma is installed.
export const getMagmaCommitments = (): Promise<{ commitments: MagmaChannelCommitment[] }> =>
  request('/api/apps/magma-sales/commitments')

export type MagmaOverview = {
  settings: MagmaSettings
  token: MagmaTokenState
  market?: MagmaMarketSummary
  orders: MagmaOrder[]
  action_needed?: MagmaOrder[]
  capacity?: MagmaCapacity
  token_warning?: string
  policy?: MagmaPolicy
  policy_summary?: string
  policy_warnings?: string[]
  pnl?: MagmaPnL
  last_sync_at?: string
  last_sync_error?: string
}

export type MagmaOrderEvent = {
  id: number
  order_id: string
  kind: string
  level: string
  message: string
  created_at: string
}

export type MagmaOpenPreview = {
  order_id: string
  size_sat: number
  revenue_sat: number
  sat_per_vbyte: number
  fastest_sat_per_vb?: number
  half_hour_sat_per_vb?: number
  hour_sat_per_vb?: number
  estimated_fee_sat: number
  total_debit_sat: number
  spendable_sat: number
  enough_funds: boolean
  fee_share_of_revenue_pct: number
  net_revenue_sat: number
  can_open: boolean
  warnings?: string[]
  blockers?: string[]
}

export const getMagmaOpenPreview = (id: string, satPerVbyte?: number): Promise<MagmaOpenPreview> =>
  request(`/api/apps/magma-sales/orders/${encodeURIComponent(id)}/open-preview${buildQuery({ sat_per_vbyte: satPerVbyte })}`)
export const acceptMagmaOrder = (id: string): Promise<MagmaOrder> =>
  request(`/api/apps/magma-sales/orders/${encodeURIComponent(id)}/accept`, { method: 'POST', body: JSON.stringify({}) })
export const rejectMagmaOrder = (id: string): Promise<MagmaOrder> =>
  request(`/api/apps/magma-sales/orders/${encodeURIComponent(id)}/reject`, { method: 'POST', body: JSON.stringify({}) })
export const openMagmaChannel = (id: string, satPerVbyte: number): Promise<MagmaOrder> =>
  request(`/api/apps/magma-sales/orders/${encodeURIComponent(id)}/open`, {
    method: 'POST',
    body: JSON.stringify({ sat_per_vbyte: satPerVbyte })
  })

export const getMagmaOverview = (): Promise<MagmaOverview> => request('/api/apps/magma-sales/overview')
export const refreshMagma = (): Promise<MagmaOverview> =>
  request('/api/apps/magma-sales/refresh', { method: 'POST', body: JSON.stringify({}) })
export const getMagmaEvents = (limit = 60): Promise<{ events: MagmaOrderEvent[] }> =>
  request(`/api/apps/magma-sales/events${buildQuery({ limit })}`)
export const getMagmaOrderEvents = (id: string): Promise<{ events: MagmaOrderEvent[] }> =>
  request(`/api/apps/magma-sales/orders/${encodeURIComponent(id)}/events`)
export const updateMagmaSettings = (payload: {
  mode?: 'monitor' | 'assisted' | 'auto'
  enabled?: boolean
  poll_interval_sec?: number
  notify_telegram?: boolean
}): Promise<MagmaSettings> =>
  request('/api/apps/magma-sales/settings', { method: 'POST', body: JSON.stringify(payload) })

export const getAppAdminPassword = (id: string) => request(`/api/apps/${id}/admin-password`)
export const installApp = (id: string, payload?: { data_dir?: string; storage_mount?: string; elements_mode?: 'local' | 'remote'; elements_rpc_url?: string; elements_rpc_user?: string; elements_rpc_password?: string }) =>
  request(`/api/apps/${id}/install`, { method: 'POST', body: JSON.stringify(payload ?? {}) })
export const uninstallApp = (id: string) => request(`/api/apps/${id}/uninstall`, { method: 'POST' })
export const startApp = (id: string) => request(`/api/apps/${id}/start`, { method: 'POST' })
export const stopApp = (id: string) => request(`/api/apps/${id}/stop`, { method: 'POST' })
export const resetAppAdmin = (id: string) => request(`/api/apps/${id}/reset-admin`, { method: 'POST' })

// ── Boleto (Pay bills with Lightning) ────────────────────────────────
export const getBoletoConfig = () => request('/api/boleto/config')
export const activateBoleto = () =>
  request('/api/boleto/activate', { method: 'POST', body: JSON.stringify({}) })
export const getActivationStatus = (paymentHash: string) =>
  request(`/api/boleto/activate/status/${encodeURIComponent(paymentHash)}`)
export const createBoletoQuote = (barcode: string) =>
  request('/api/boleto/quote', { method: 'POST', body: JSON.stringify({ barcode }) })
export const getBoletoStatus = (paymentHash: string) =>
  request(`/api/boleto/status/${encodeURIComponent(paymentHash)}`)
