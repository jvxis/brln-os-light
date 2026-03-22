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
      let errorMsg = text
      try {
        const payload = JSON.parse(text)
        if (payload && typeof payload.message === 'string') {
          errorMsg = payload.message
        } else if (payload && typeof payload.error === 'string') {
          errorMsg = payload.error
        }
      } catch {
        // not JSON, use raw text
      }
      throw new Error(errorMsg)
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
export const getAppUpgradeStatus = (force?: boolean) =>
  request(`/api/app/upgrade/status${buildQuery({ force: force ? 1 : undefined })}`)
export const startAppUpgrade = (payload?: { target_version?: string }) =>
  request('/api/app/upgrade/start', { method: 'POST', body: JSON.stringify(payload ?? {}) })
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
export const previewOnchainSend = (payload: { address: string; amount_sat?: number; sat_per_vbyte: number; sweep_all?: boolean }) =>
  request('/api/wallet/send/preview', { method: 'POST', body: JSON.stringify(payload) })
export const sendOnchain = (payload: { address: string; amount_sat?: number; sat_per_vbyte?: number; sweep_all?: boolean }) =>
  request('/api/wallet/send', { method: 'POST', body: JSON.stringify(payload) })
export const createInvoice = (payload: { amount_sat: number; memo: string; expiry_seconds?: number }) =>
  request('/api/wallet/invoice', { method: 'POST', body: JSON.stringify(payload) })
export const decodeInvoice = (payload: { payment_request: string }) =>
  request('/api/wallet/decode', { method: 'POST', body: JSON.stringify(payload) })
export const payInvoice = (payload: { payment_request: string; channel_point?: string; amount_sat?: number }) =>
  request('/api/wallet/pay', { method: 'POST', body: JSON.stringify(payload) })

export const getLnChannels = () => request('/api/lnops/channels')
export const getLnPeers = () => request('/api/lnops/peers')
export const getLnChannelPeerRecommendations = (channelPoint: string, limit = 5) =>
  request(`/api/lnops/channel/peer-recommendations?channel_point=${encodeURIComponent(channelPoint)}&limit=${encodeURIComponent(String(limit))}`)
export const getNetworkAtlasMap = () => request('/api/lnops/network-map')
export const getNetworkAtlasConfig = () => request('/api/lnops/network-map/config')
export const updateNetworkAtlasConfig = (payload: { label?: string; lat?: string | null; lon?: string | null }) =>
  request('/api/lnops/network-map/config', { method: 'POST', body: JSON.stringify(payload) })
export const getLnClosedChannels = () => request('/api/lnops/closed-channels')
export const getChannelRankings = (params?: { limit?: number; state?: string }) =>
  request(`/api/lnops/channel-ranking${buildQuery(params)}`)
export const getChannelRanking = (channelPoint: string) =>
  request(`/api/lnops/channel-ranking/${encodeURIComponent(channelPoint)}`)
export const recomputeChannelRankings = () =>
  request('/api/lnops/channel-ranking/recompute', { method: 'POST' })
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
}) => request('/api/lnops/node-retirement/sessions', { method: 'POST', body: JSON.stringify(payload) })
export const getNodeRetirementSession = (sessionID: string) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}`)
export const getNodeRetirementSessionEvents = (sessionID: string, limit = 100) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/events?limit=${encodeURIComponent(String(limit))}`)
export const getNodeRetirementSessionChannels = (sessionID: string) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/channels`)
export const getNodeRetirementSessionTransfer = (sessionID: string) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/transfer`)
export const confirmNodeRetirementCoopClose = (sessionID: string) =>
  request(`/api/lnops/node-retirement/sessions/${encodeURIComponent(sessionID)}/confirm-coop`, { method: 'POST' })
export const decideNodeRetirementChannel = (sessionID: string, payload: { channel_point: string; decision: 'wait' | 'force_close' }) =>
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
}) => request('/api/lnops/succession/config', { method: 'POST', body: JSON.stringify(payload) })
export const successionAlive = (payload: { source?: string }) =>
  request('/api/lnops/succession/alive', { method: 'POST', body: JSON.stringify(payload) })
export const successionSimulate = (payload: { action: 'alive' | 'not_alive'; source?: string }) =>
  request('/api/lnops/succession/simulate', { method: 'POST', body: JSON.stringify(payload) })
export const getAutofeeConfig = () => request('/api/lnops/autofee/config')
export const updateAutofeeConfig = (payload: {
  enabled?: boolean
  profile?: string
  lookback_days?: number
  run_interval_sec?: number
  cooldown_up_sec?: number
  cooldown_down_sec?: number
  step_cap_override?: number
  discovery_step_cap_down_override?: number
  stall_floor_relax_gap_frac_override?: number
  inbound_discount_max_ratio_override?: number
  outrate_floor_factor_low_override?: number
  soften_min_out_ratio_override?: number
  soften_max_drop_to_peg_frac_override?: number
  htlc_min_attempts_60m_override?: number
  htlc_policy_fail_rate_override?: number
  htlc_liquidity_fail_rate_override?: number
  rebal_cost_mode?: string
  amboss_enabled?: boolean
  amboss_token?: string
  inbound_passive_enabled?: boolean
  discovery_enabled?: boolean
  explorer_enabled?: boolean
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
export const openChannel = (payload: {
  peer_address: string
  local_funding_sat: number
  push_sat?: number
  close_address?: string
  sat_per_vbyte?: number
  private?: boolean
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

export const sendChatMessage = (payload: { peer_pubkey: string; message: string }) =>
  request('/api/chat/send', { method: 'POST', body: JSON.stringify(payload) })

export const getNotifications = (limit = 200) =>
  request(`/api/notifications?limit=${limit}`)

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
export const updateRebalanceConfig = (payload: {
  auto_enabled?: boolean
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
  mc_half_life_sec?: number
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
export const runRebalance = (payload: {
  channel_id: number
  channel_point?: string
  target_outbound_pct?: number
  auto_restart?: boolean
}) =>
  request('/api/rebalance/run', { method: 'POST', body: JSON.stringify(payload) })
export const stopRebalance = (payload: { job_id: number }) =>
  request('/api/rebalance/stop', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelTarget = (payload: {
  channel_id: number
  channel_point: string
  target_outbound_pct?: number
  use_default_econ_ratio?: boolean
  econ_ratio_override?: number
}) =>
  request('/api/rebalance/channel/target', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelAuto = (payload: { channel_id: number; channel_point?: string; auto_enabled: boolean }) =>
  request('/api/rebalance/channel/auto', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceChannelManualRestart = (payload: { channel_id: number; channel_point?: string; enabled: boolean }) =>
  request('/api/rebalance/channel/manual-restart', { method: 'POST', body: JSON.stringify(payload) })
export const updateRebalanceExclude = (payload: { channel_id: number; channel_point: string; excluded: boolean }) =>
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

export const getApps = () => request('/api/apps')
export const getAppAdminPassword = (id: string) => request(`/api/apps/${id}/admin-password`)
export const installApp = (id: string) => request(`/api/apps/${id}/install`, { method: 'POST' })
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
