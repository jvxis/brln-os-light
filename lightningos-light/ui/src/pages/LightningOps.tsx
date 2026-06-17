import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { acceptBalancedOpenSession, addLnWatchtower, boostPeers, bumpFeeCloseManagerSession, bumpPendingOpenChannel, cancelBalancedOpenSession, closeChannel, connectPeer, createBalancedOpenSession, disconnectPeer, executeBalancedOpenSession, forceCloseManagerSession, getAmbossHealth, getAutofeeChannels, getAutofeeConfig, getAutofeeResults, getAutofeeStatus, getBalancedOpenSessionEvents, getBalancedOpenSessions, getBalancedOpenStatus, getBitcoinLocalStatus, getChannelRankings, getCloseManagerSessions, getCloseManagerStatus, getLnChanHeal, getLnChannelFees, getLnChannelPeerRecommendations, getLnChannels, getLnClosedChannels, getLnFailedPaymentsCleaner, getLnHtlcManager, getLnHtlcManagerFailed, getLnHtlcManagerLogs, getLnPeers, getLnTorPeerChecker, getLnTorPeerCheckerLogs, getLnWatchtowers, getMempoolFees, openBatchChannels, openChannel, previewBatchOpenChannels, previewOpenChannel, proposeBalancedOpenSession, recoverBalancedOpenSession, recoverCloseManagerSession, refreshAutofeeReferences, removeLnWatchtower, restoreLnScb, retryBalancedOpenSessionBroadcast, runAutofee, signLnMessage, updateAmbossHealth, updateAutofeeChannels, updateAutofeeConfig, updateChannelFees, updateLnChanHeal, updateLnChannelStatus, updateLnFailedPaymentsCleaner, updateLnHtlcManager, updateLnTorPeerChecker } from '../api'
import { getLocale } from '../i18n'

type Channel = {
  channel_point: string
  channel_id: number
  channel_id_str?: string
  remote_pubkey: string
  peer_alias: string
  initiator?: boolean
  active: boolean
  inactive_since_unix?: number
  inactive_duration_sec?: number
  chan_status_flags?: string
  local_disabled?: boolean
  private: boolean
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
  unsettled_balance_sat?: number
  pending_htlc_count?: number
  pending_htlcs?: ChannelPendingHtlc[]
  base_fee_msat?: number
  fee_rate_ppm?: number
  inbound_base_msat?: number
  inbound_fee_rate_ppm?: number
  peer_fee_rate_ppm?: number
  peer_base_msat?: number
  class_label?: string
  out_ppm7d?: number
  rebal_ppm7d?: number
  forward_fee_7d_sat?: number
  rebal_fee_7d_sat?: number
  profit_fee_7d_sat?: number
  movement_7d?: ChannelMovement7d
}

type ChannelMovement7d = {
  forward_count: number
  forward_amount_sat: number
  forward_in_count: number
  forward_in_amount_sat: number
  forward_out_count: number
  forward_out_amount_sat: number
  rebalance_count: number
  rebalance_amount_sat: number
  rebalance_in_count: number
  rebalance_in_amount_sat: number
  rebalance_out_count: number
  rebalance_out_amount_sat: number
  lightning_out_count: number
  lightning_out_amount_sat: number
  lightning_in_count: number
  lightning_in_amount_sat: number
}

type ChannelPendingHtlc = {
  incoming: boolean
  peer_alias?: string
  amount_sat: number
  expiration_height: number
  htlc_index?: number
  forwarding_channel_id?: number
  locked_in?: boolean
}

type PendingChannel = {
  channel_point: string
  remote_pubkey: string
  peer_alias?: string
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
  status: string
  closing_txid?: string
  blocks_til_maturity?: number
  limbo_balance?: number
  confirmations_until_active?: number
  confirmation_height?: number
  opening_since_unix?: number
  opening_duration_sec?: number
  funding_fee_rate_sat_vb?: number
  funding_bump_checked?: boolean
  funding_bump_eligible?: boolean
  funding_bump_outpoint?: string
  funding_bump_amount_sat?: number
  funding_bump_reason?: string
  private?: boolean
  waiting_close_recovery?: {
    attempts?: number
    last_attempt_at?: string
    last_result?: string
    last_error?: string
    last_recovered_txid?: string
    suggest_force_close?: boolean
  }
}

type MempoolFeeHint = {
  fastest?: number
  halfHour?: number
  hour?: number
  economy?: number
  minimum?: number
}

type CloseRecoverySession = {
  id: number
  channel_point: string
  channel_id: number
  peer_pubkey?: string
  peer_alias?: string
  source: string
  source_ref?: string
  state: string
  action_required?: string
  action_recommended?: string
  decision?: string
  risk_level?: string
  close_mode?: string
  close_height?: number
  close_txid?: string
  sweep_txid?: string
  limbo_balance_sat?: number
  pending_htlc_count?: number
  pending_htlc_first_seen_at?: string
  pending_htlc_age_sec?: number
  blocks_til_maturity?: number
  maturity_eta_at?: string
  sweep_pending_count?: number
  sweep_broadcast_attempts?: number
  sweep_requested_fee_rate_sat_vb?: number
  sweep_fee_rate_sat_vb?: number
  mempool_target_sat_vb?: number
  last_error?: string
  waiting_close_attempts?: number
  waiting_close_last_attempt_at?: string
  waiting_close_last_result?: string
  waiting_close_last_error?: string
  waiting_close_last_recovered_txid?: string
  waiting_close_suggest_force_close?: boolean
  close_tx_external_seen?: boolean
  close_tx_external_confirmed?: boolean
  close_tx_external_block_time?: string
  sweep_tx_external_seen?: boolean
  sweep_tx_external_confirmed?: boolean
  sweep_tx_external_block_time?: string
  last_progress_at?: string
  updated_at?: string
  closed_at?: string
}

type CloseRecoveryStatus = {
  available: boolean
  last_sync_at?: string
  active_count: number
  action_required_count: number
  waiting_close_count: number
  htlc_blocked_count: number
  node_retirement_count: number
  state_counts?: Record<string, number>
}

type ChannelRankingItem = {
  channel_point: string
  score: number
  state: 'expand' | 'maintain' | 'monitor' | 'close'
}

type Peer = {
  pub_key: string
  alias: string
  address: string
  inbound: boolean
  bytes_sent: number
  bytes_recv: number
  sat_sent: number
  sat_recv: number
  ping_time: number
  sync_type: string
  last_error: string
  last_error_time?: number
}

type PeerRecommendation = {
  pub_key: string
  alias?: string
  host?: string
  peer_address?: string
  has_clearnet?: boolean
  channel_count: number
  total_capacity_sat: number
  shared_channel_count?: number
  shared_capacity_sat?: number
  largest_capacity_sat: number
  inbound_base_msat?: number
  inbound_fee_rate_ppm?: number
  outbound_base_msat?: number
  outbound_fee_rate_ppm?: number
}

type PeerRecommendationResponse = {
  recommendations?: PeerRecommendation[]
  recommendations_count?: number
  selection_tier?: string
}

type ClosedChannelResolution = {
  resolution_type: number
  sweep_txid?: string
}

type ClosedChannel = {
  channel_point?: string
  chan_id: number
  closed_at?: string
  closing_tx_hash?: string
  remote_pubkey?: string
  peer_alias?: string
  capacity_sat: number
  settled_balance_sat: number
  time_locked_balance_sat: number
  close_type: number
  close_type_label?: string
  open_initiator: number
  open_initiator_label?: string
  close_initiator: number
  close_initiator_label?: string
  close_height: number
  resolutions?: ClosedChannelResolution[]
}

type Watchtower = {
  pubkey: string
  addresses: string[]
  active_session_candidate: boolean
  num_sessions: number
}

type BatchOpenItem = {
  id: number
  pubkey: string
  host?: string
  local_funding_sat: number
  private: boolean
  close_address?: string
}

type OpenChannelPreview = {
  local_funding_sat: number
  push_sat: number
  fee_sat: number
  total_debit_sat: number
  spendable_sat: number
  spendable_remaining_sat: number
  selected_input_count: number
  selected_input_sat: number
  estimated_vbytes: number
  sat_per_vbyte: number
  enough_funds: boolean
  exact: boolean
  has_change: boolean
  reference_only: boolean
  message_code?: string
  message?: string
}

type BatchOpenPreview = {
  channel_count: number
  total_funding_sat: number
  fee_sat: number
  total_debit_sat: number
  spendable_sat: number
  spendable_remaining_sat: number
  selected_input_count: number
  selected_input_sat: number
  estimated_vbytes: number
  sat_per_vbyte: number
  enough_funds: boolean
  exact: boolean
  has_change: boolean
  reference_only: boolean
  message_code?: string
  message?: string
}

type BalancedOpenStatusPayload = {
  enabled: boolean
  available: boolean
  error?: string
  wallet?: {
    total_sat?: number
    confirmed_sat?: number
    unconfirmed_sat?: number
    locked_sat?: number
    reserved_anchor_sat?: number
    estimated_spendable_sat?: number
  }
  wallet_error?: string
}

type BalancedOpenSession = {
  session_id: string
  role: 'initiator' | 'accepter'
  peer_pubkey: string
  peer_host?: string
  capacity_sat: number
  fee_rate_sat_vb: number
  private: boolean
  close_address?: string
  state: string
  state_updated_at: string
  last_error?: string
  created_at: string
  metadata?: Record<string, unknown>
}

type BalancedOpenEvent = {
  id: number
  session_id: string
  event_type: string
  detail?: Record<string, unknown>
  created_at: string
}

type AmbossHealthStatus = {
  enabled: boolean
  status: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_attempt_at?: string
  interval_sec?: number
  consecutive_failures?: number
}

type ChanHealStatus = {
  enabled: boolean
  status: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_attempt_at?: string
  interval_sec?: number
  last_updated?: number
}

type HtlcManagerStatus = {
  enabled: boolean
  status: string
  interval_minutes?: number
  interval_hours?: number
  min_htlc_sat: number
  max_local_pct: number
  last_attempt_at?: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_changed_count?: number
}

type HtlcManagerLogEntry = {
  ts: string
  alias: string
  channel_id: number
  channel_point: string
  old_min_msat: number
  new_min_msat: number
  old_max_msat: number
  new_max_msat: number
  result: string
}

type HtlcManagerFailedEntry = {
  ts: string
  incoming_channel_id?: string
  incoming_alias?: string
  outgoing_channel_id?: string
  outgoing_alias?: string
  incoming_amt_msat?: number
  outgoing_amt_msat?: number
  potential_fee_msat?: number
  failure_code?: string
  failure_detail?: string
  failure_reason?: string
  event?: string
}

type FailedPaymentsCleanerStatus = {
  enabled: boolean
  status: string
  interval_hours: number
  last_attempt_at?: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_deleted_count?: number
}

type TorPeerCheckerStatus = {
  enabled: boolean
  status: string
  interval_hours: number
  last_attempt_at?: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_checked_count?: number
  last_hybrid_on_tor_count?: number
  last_attempted_count?: number
  last_switched_count?: number
}

type TorPeerCheckerLogEntry = {
  ts: string
  alias: string
  pub_key?: string
  from_address?: string
  to_address?: string
  result: string
  detail?: string
}

type BitcoinLocalCadenceBucket = {
  count: number
}

type BitcoinLocalStatus = {
  block_cadence_window_sec?: number
  block_cadence?: BitcoinLocalCadenceBucket[]
}

type AutofeeProfileDefaults = {
  run_interval_sec: number
  cooldown_up_sec: number
  cooldown_down_sec: number
  step_cap: number
  discovery_step_cap_down: number
  stall_floor_relax_gap_frac: number
  inbound_discount_max_ratio: number
  inbound_discount_reach_out_ratio: number
  inbound_discount_min_retained_spread_frac: number
  outrate_floor_factor_low: number
  soften_min_out_ratio: number
  soften_max_drop_to_peg_frac: number
  htlc_min_attempts_60m: number
  htlc_policy_fail_rate: number
  htlc_liquidity_fail_rate: number
}

type AutofeeConfig = {
    enabled: boolean
    operation_mode: string
    profile: string
    lookback_days: number
    run_interval_sec: number
    cooldown_up_sec: number
    cooldown_down_sec: number
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
    native_seed_enabled: boolean
    amboss_enabled: boolean
    amboss_token_set: boolean
    inbound_passive_enabled: boolean
  discovery_enabled: boolean
  explorer_enabled: boolean
  idle_refresh_enabled: boolean
  super_source_enabled: boolean
  super_source_base_fee_msat: number
  revfloor_enabled: boolean
  circuit_breaker_enabled: boolean
  extreme_drain_enabled: boolean
  htlc_signal_enabled: boolean
  htlc_mode: string
  min_ppm: number
  max_ppm: number
  profile_defaults?: Record<string, AutofeeProfileDefaults>
}

type AutofeeStatus = {
  running: boolean
  last_run_at?: string
  next_run_at?: string
  last_error?: string
}

type AutofeeChannelSetting = {
  channel_id?: number | string
  channel_id_str?: string
  channel_point?: string
  enabled: boolean
}

type AutofeeResultItem = {
  kind: string
  category?: string
  reason?: string
  operation_mode?: string
  dry_run?: boolean
  timestamp?: string
  up?: number
  down?: number
  flat?: number
  cooldown?: number
  small?: number
  same?: number
  disabled?: number
  inactive?: number
  inbound_disc?: number
  super_source?: number
  htlc_liq_hot?: number
  htlc_policy_hot?: number
  htlc_forward_hot?: number
  htlc_sample_low?: number
  reversal_blocked?: number
  reversal_confirmed?: number
  downcap_general?: number
  downcap_low_sample?: number
  floor_relax_applied?: number
  stall_alert?: number
  htlc_attempts_total?: number
  htlc_link_fails_total?: number
  htlc_forward_fails_total?: number
  htlc_other_fails_total?: number
  htlc_classified_total?: number
  htlc_unclassified_total?: number
  htlc_top_reasons?: string[]
  htlc_window_min?: number
  htlc_min_attempts?: number
  htlc_min_policy_fails?: number
  htlc_min_liquidity_fails?: number
  htlc_min_forward_fails?: number
  htlc_policy_fail_rate_min?: number
  htlc_liquidity_fail_rate_min?: number
  htlc_forward_fail_rate_min?: number
  htlc_global_count_factor?: number
  htlc_global_rate_factor?: number
  htlc_node_factor?: number
  htlc_liquidity_factor?: number
  htlc_threshold_factor?: number
  low_out_thresh?: number
  low_out_protect_thresh?: number
  low_out_factor?: number
  amboss?: number
  missing?: number
  err?: number
  empty?: number
  outrate?: number
  mem?: number
  default?: number
  cooldown_ignored?: boolean
  updated_count?: number
  same_count?: number
  skipped_count?: number
  error_count?: number
  rebal_markup_pct?: number
  node_class?: string
  liquidity_class?: string
  channel_count?: number
  total_capacity_sat?: number
  avg_capacity_sat?: number
  local_capacity_sat?: number
  local_ratio?: number
  revfloor_baseline?: number
  revfloor_min_abs?: number
  alias?: string
  channel_id?: number | string
  channel_point?: string
  local_ppm?: number
  new_ppm?: number
  target?: number
  target_raw?: number
  target_final?: number
  out_ratio?: number
  out_ratio_effective?: number
  out_ppm7d?: number
  rebal_ppm7d?: number
  seed?: number
  floor?: number
  floor_src?: string
  floor_base_ppm?: number
  floor_base_src?: string
  rebal_cost_mode?: string
  margin?: number
  rev_share?: number
  tags?: string[]
  inbound_discount?: number
  prev_inbound_discount?: number
  class_label?: string
  skip_reason?: string
  error?: string
  delta?: number
  delta_pct?: number
  prediction_code?: string
  prediction_cooldown_hours?: number
  new_inbound?: boolean
  current_inbound_discount?: number
  target_inbound_discount?: number
  inbound_source?: string
  inbound_updated?: number
  include_inbound?: boolean
  channel_age_hours?: number
  htlc_attempts?: number
  htlc_forward_fails?: number
  htlc_policy_fails?: number
  htlc_liquidity_fails?: number
  htlc_unclassified_fails?: number
  htlc_window_min_channel?: number
  stalled_rounds?: number
  hours_since_last_change?: number
  target_gap_ppm?: number
  target_gap_pct?: number
  reference_ppm?: number
  refresh_source?: string
}

type AutofeeChannelRound = {
  run_key: string
  timestamp?: string
  reason?: string
  category?: string
  local_ppm?: number
  new_ppm?: number
  target?: number
  floor?: number
  floor_src?: string
  floor_base_ppm?: number
  floor_base_src?: string
  rebal_cost_mode?: string
  seed?: number
  tags?: string[]
  prediction_code?: string
  prediction_cooldown_hours?: number
  skip_reason?: string
  error?: string
  reference_ppm?: number
  refresh_source?: string
}

const normalizeAutofeeChannelID = (channelID?: number | string) => {
  if (typeof channelID === 'string') {
    const trimmed = channelID.trim()
    return trimmed
  }
  if (typeof channelID === 'number' && Number.isFinite(channelID)) {
    return String(Math.trunc(channelID))
  }
  return ''
}

const formatAutofeeFloorSource = (floorSrc?: string, floorBaseSrc?: string, floorBasePpm?: number) => {
  let source = (floorSrc || '').trim()
  const baseSource = (floorBaseSrc || '').trim()
  const basePpm = typeof floorBasePpm === 'number' && Number.isFinite(floorBasePpm)
    ? Math.round(floorBasePpm)
    : 0
  if (!source && baseSource) source = baseSource
  if (!source) return ''
  if (baseSource && basePpm > 0) {
    if (baseSource === source) {
      source = `${source}≈${basePpm}`
    } else if (source === 'rebal' && baseSource.startsWith('rebal-')) {
      source = `${baseSource}≈${basePpm}`
    } else if (source === 'outrate' && baseSource.startsWith('outrate-')) {
      source = `${baseSource}≈${basePpm}`
    } else {
      source = `${source}; base ${baseSource}≈${basePpm}`
    }
  }
  return `(${source})`
}

const autofeeChannelKey = (channelPoint?: string, channelID?: number | string) => {
  const point = (channelPoint || '').trim()
  if (point) return point
  const id = normalizeAutofeeChannelID(channelID)
  return id ? `id:${id}` : ''
}

const collectAutofeeChannelRounds = (items: AutofeeResultItem[], maxPerChannel = 2): Record<string, AutofeeChannelRound[]> => {
  const roundsByChannel: Record<string, AutofeeChannelRound[]> = {}
  const seenRunsByChannel: Record<string, Set<string>> = {}
  let currentRun: { key: string; timestamp?: string; reason?: string; dryRun: boolean } | null = null

  items.forEach((item, index) => {
    if (!item || typeof item !== 'object') return
    if (item.kind === 'header') {
      currentRun = {
        key: `run-${index}`,
        timestamp: item.timestamp,
        reason: item.reason,
        dryRun: Boolean(item.dry_run)
      }
      return
    }
    if (item.kind !== 'channel') return

    const channelKey = autofeeChannelKey(item.channel_point, item.channel_id)
    if (!channelKey) return
    if (currentRun?.dryRun || item.dry_run) return

    const runKey = currentRun?.key || `legacy-${index}`
    if (!seenRunsByChannel[channelKey]) {
      seenRunsByChannel[channelKey] = new Set<string>()
    }
    if (seenRunsByChannel[channelKey].has(runKey)) return
    seenRunsByChannel[channelKey].add(runKey)

    if (!roundsByChannel[channelKey]) {
      roundsByChannel[channelKey] = []
    }
    if (roundsByChannel[channelKey].length >= maxPerChannel) return

    roundsByChannel[channelKey].push({
      run_key: runKey,
      timestamp: currentRun?.timestamp,
      reason: currentRun?.reason,
      category: item.category,
      local_ppm: item.local_ppm,
      new_ppm: item.new_ppm,
      target: item.target,
      floor: item.floor,
      floor_src: formatAutofeeFloorSource(item.floor_src, item.floor_base_src, item.floor_base_ppm).replace(/^\(|\)$/g, '') || item.floor_src,
      floor_base_ppm: item.floor_base_ppm,
      floor_base_src: item.floor_base_src,
      rebal_cost_mode: item.rebal_cost_mode,
      seed: item.seed,
      tags: Array.isArray(item.tags) ? [...item.tags] : [],
      prediction_code: item.prediction_code,
      prediction_cooldown_hours: item.prediction_cooldown_hours,
      skip_reason: item.skip_reason,
      error: item.error,
      reference_ppm: item.reference_ppm,
      refresh_source: item.refresh_source
    })
  })

  return roundsByChannel
}

const autofeeHistoryLimitingTagSet = new Set([
  'cooldown',
  'cooldown-profit',
  'hold-small',
  'small-delta',
  'same-ppm',
  'no-down-low',
  'no-down-neg-margin',
  'circuit-breaker',
  'peg',
  'peg-grace',
  'peg-demand',
  'sink-floor',
  'htlc-liq-nodown',
  'htlc-policy-nodown',
  'htlc-neutral-nodown',
  'super-source',
  'super-source-like'
])

const autofeeHistoryRelaxTagSet = new Set([
  'discovery',
  'discovery-hard',
  'explorer',
  'idle-refresh',
  'cooldown-skip',
  'floor-relax-stall',
  'stall-alert',
  'extreme-drain',
  'extreme-drain-turbo',
  'htlc-step-boost',
  'trend-down',
  'trend-flat'
])

const formatAutofeeHistoryTag = (tag: string) => {
  if (!tag) return ''
  if (tag === 'cooldown') return '⏳ cooldown'
  if (tag === 'cooldown-profit') return '⏳ profit-hold'
  if (tag === 'hold-small') return '🧊 hold-small'
  if (tag === 'small-delta') return '🧊 small-delta'
  if (tag === 'same-ppm') return '🟰 same-ppm'
  if (tag === 'no-down-low') return '🚫 down-low'
  if (tag === 'no-down-neg-margin') return '🚫 down-neg'
  if (tag === 'circuit-breaker') return '🧯 circuit-breaker'
  if (tag === 'peg') return '📌 peg'
  if (tag === 'peg-grace') return '📌 peg-grace'
  if (tag === 'peg-demand') return '📌 peg-demand'
  if (tag === 'sink-floor') return '🧱 sink-floor'
  if (tag === 'htlc-liq-nodown') return '🚫 liq-nodown'
  if (tag === 'htlc-policy-nodown') return '🚫 policy-nodown'
  if (tag === 'htlc-neutral-nodown') return '🚫 neutral-nodown'
  if (tag === 'super-source') return '🔥 super-source'
  if (tag === 'super-source-like') return '🔥 super-source-like'
  if (tag === 'discovery') return '🧭 discovery'
  if (tag === 'discovery-hard') return '🧨 harddrop'
  if (tag === 'explorer') return '🧭 explorer'
  if (tag === 'idle-refresh') return 'idle-refresh'
  if (tag.startsWith('idle-refresh:')) return tag
  if (tag === 'cooldown-skip') return '🧭 skip-cooldown'
  if (tag === 'floor-relax-stall') return '🧯 floor-relax'
  if (tag === 'reversal-fasttrack') return '↩️ reversal-fasttrack'
  if (tag === 'stall-alert') return '🚨 stall-alert'
  if (tag === 'extreme-drain') return '⚡ extreme'
  if (tag === 'extreme-drain-turbo') return '⚡ turbo'
  if (tag === 'htlc-step-boost') return '⚡ htlc-step'
  if (tag === 'trend-down') return '📉 trend-down'
  if (tag === 'trend-flat') return '➡️ trend-flat'
  if (tag.startsWith('htlc-liq+')) return `💧 ${tag}`
  if (tag.startsWith('htlc-policy+')) return `🧾 ${tag}`
  if (tag.startsWith('surge')) return `📈 ${tag}`
  if (tag.startsWith('negm+')) return `💹 ${tag}`
  return tag
}

const LIGHTNING_OPS_ROUTE_KEY = 'lightning-ops'
const CHANNEL_RANKING_ROUTE_KEY = 'channel-ranking'
const REBALANCE_ROUTE_KEY = 'rebalance-center'
const GRAPH_EXPLORER_ROUTE_KEY = 'graph-explorer'
type ChannelsViewMode = 'full' | 'condensed'
const CHANNELS_VIEW_MODE_STORAGE_KEY = 'lightningOps.channelsViewMode'
const CHANNEL_HASH_PARAM = 'channel_point'
const PEER_HASH_PARAM = 'peer_pubkey'
const SECTION_HASH_PARAM = 'section'
const PEERS_SECTION_ID = 'peers-section'
const CLOSE_RECOVERY_SECTION_ID = 'close-recovery-section'
const AUTOFEE_SECTION_ID = 'autofee-section'
const LIGHTNING_TOOLS_SECTION_ID = 'lightning-tools-section'
const ADD_PEER_TOOL_SECTION_ID = 'add-peer-tool-section'
const HTLC_MANAGER_SECTION_ID = 'htlc-manager-section'
const OPEN_CHANNEL_SECTION_ID = 'open-channel-section'
const CLOSE_CHANNEL_SECTION_ID = 'close-channel-section'
const UPDATE_FEES_SECTION_ID = 'update-fees-section'
const BATCH_OPEN_SECTION_ID = 'batch-open-section'
const BALANCED_OPEN_SECTION_ID = 'balanced-open-section'
const WATCHTOWER_SECTION_ID = 'watchtower-section'
const AMBOSS_HEALTH_SECTION_ID = 'amboss-health-section'
const CHAN_HEAL_SECTION_ID = 'chan-heal-section'
const TOR_PEER_SECTION_ID = 'tor-peer-section'
const FAILED_PAYMENTS_CLEANER_SECTION_ID = 'failed-payments-cleaner-section'
const SIGN_MESSAGE_SECTION_ID = 'sign-message-section'
const SCB_RECOVERY_CONFIRM_PHRASE = 'I UNDERSTAND FORCE CLOSE'
const BALANCED_OPEN_FUNDING_VBYTES = 190
const BALANCED_OPEN_REQUIRED_REMAINING_SAT = 10000
const COOP_CLOSE_ESTIMATED_ONE_OUTPUT_VBYTES = 140
const COOP_CLOSE_ESTIMATED_TWO_OUTPUT_VBYTES = 170
const CLOSE_PREVIEW_DUST_LIMIT_SAT = 546
const PENDING_OPEN_STUCK_THRESHOLD_SEC = 60 * 60
const PENDING_OPEN_BUMP_REFERENCE_VBYTES = 110

const readChannelsViewMode = (): ChannelsViewMode => {
  if (typeof window === 'undefined') return 'full'
  try {
    return window.localStorage.getItem(CHANNELS_VIEW_MODE_STORAGE_KEY) === 'condensed'
      ? 'condensed'
      : 'full'
  } catch {
    return 'full'
  }
}

const readHashChannelPoint = (routeKey: string) => {
  if (typeof window === 'undefined') return ''
  const rawHash = window.location.hash.startsWith('#')
    ? window.location.hash.slice(1)
    : window.location.hash
  if (!rawHash) return ''
  const queryIndex = rawHash.indexOf('?')
  if (queryIndex < 0) return ''
  if (rawHash.slice(0, queryIndex) !== routeKey) return ''
  const params = new URLSearchParams(rawHash.slice(queryIndex + 1))
  return (params.get(CHANNEL_HASH_PARAM) || '').trim()
}

const readHashPeerPubKey = (routeKey: string) => {
  if (typeof window === 'undefined') return ''
  const rawHash = window.location.hash.startsWith('#')
    ? window.location.hash.slice(1)
    : window.location.hash
  if (!rawHash) return ''
  const queryIndex = rawHash.indexOf('?')
  if (queryIndex < 0) return ''
  if (rawHash.slice(0, queryIndex) !== routeKey) return ''
  const params = new URLSearchParams(rawHash.slice(queryIndex + 1))
  return (params.get(PEER_HASH_PARAM) || '').trim()
}

const readHashSection = (routeKey: string) => {
  if (typeof window === 'undefined') return ''
  const rawHash = window.location.hash.startsWith('#')
    ? window.location.hash.slice(1)
    : window.location.hash
  if (!rawHash) return ''
  const queryIndex = rawHash.indexOf('?')
  if (queryIndex < 0) return ''
  if (rawHash.slice(0, queryIndex) !== routeKey) return ''
  const params = new URLSearchParams(rawHash.slice(queryIndex + 1))
  return (params.get(SECTION_HASH_PARAM) || '').trim()
}

const buildHashWithChannelPoint = (routeKey: string, channelPoint: string) =>
  `#${routeKey}?${CHANNEL_HASH_PARAM}=${encodeURIComponent(channelPoint)}`

const buildGraphExplorerHash = (pubkey: string) =>
  `#${GRAPH_EXPLORER_ROUTE_KEY}?pubkey=${encodeURIComponent(pubkey)}`

const channelCardID = (channelPoint: string) =>
  `lightning-channel-${channelPoint.replace(/[^a-zA-Z0-9_-]/g, '_')}`

const peerCardID = (pubkey: string) =>
  `lightning-peer-${pubkey.replace(/[^a-zA-Z0-9_-]/g, '_')}`

const arrayBufferToBase64 = (buffer: ArrayBuffer) => {
  const bytes = new Uint8Array(buffer)
  if (!bytes.length) return ''
  const chunkSize = 0x8000
  let binary = ''
  for (let i = 0; i < bytes.length; i += chunkSize) {
    const chunk = bytes.subarray(i, i + chunkSize)
    binary += String.fromCharCode(...chunk)
  }
  return btoa(binary)
}

const isFCRiskChannel = (ch: Channel) =>
  !ch.active && Number(ch.unsettled_balance_sat || 0) > 0

export default function LightningOps() {
  const { t, i18n } = useTranslation()
  const locale = getLocale(i18n.language)
  const [channels, setChannels] = useState<Channel[]>([])
  const [activeCount, setActiveCount] = useState(0)
  const [inactiveCount, setInactiveCount] = useState(0)
  const [pendingOpenCount, setPendingOpenCount] = useState(0)
  const [pendingCloseCount, setPendingCloseCount] = useState(0)
  const [pendingChannels, setPendingChannels] = useState<PendingChannel[]>([])
  const [channelBlockHeight, setChannelBlockHeight] = useState(0)
  const [status, setStatus] = useState('')
  const [filter, setFilter] = useState<'all' | 'active' | 'inactive'>('all')
  const [profitFilter, setProfitFilter] = useState<'all' | 'profitable' | 'neutral' | 'deficit'>('all')
  const [fcRiskOnly, setFcRiskOnly] = useState(false)
  const [search, setSearch] = useState('')
  const [minCapacity, setMinCapacity] = useState('')
  const [rankingFilter, setRankingFilter] = useState<'all' | 'expand' | 'maintain' | 'monitor' | 'close'>('all')
  const [movementFilter, setMovementFilter] = useState<'all' | 'low' | 'active' | 'none'>('all')
  const [sortBy, setSortBy] = useState<'capacity' | 'local' | 'remote' | 'alias'>('local')
  const [sortDir, setSortDir] = useState<'desc' | 'asc'>('asc')
  const [showPrivate, setShowPrivate] = useState(true)
  const [focusedChannelPoint, setFocusedChannelPoint] = useState('')
  const [focusedPeerPubKey, setFocusedPeerPubKey] = useState('')
  const [pendingHtlcOpenChannelPoint, setPendingHtlcOpenChannelPoint] = useState('')
  const [movementOpenChannelPoint, setMovementOpenChannelPoint] = useState('')
  const [peerRecommendationOpenChannelPoint, setPeerRecommendationOpenChannelPoint] = useState('')
  const [peerRecommendationsByChannel, setPeerRecommendationsByChannel] = useState<Record<string, PeerRecommendation[]>>({})
  const [peerRecommendationTierByChannel, setPeerRecommendationTierByChannel] = useState<Record<string, string>>({})
  const [peerRecommendationLoadingByChannel, setPeerRecommendationLoadingByChannel] = useState<Record<string, boolean>>({})
  const [peerRecommendationErrorByChannel, setPeerRecommendationErrorByChannel] = useState<Record<string, string>>({})
  const [peerRecommendationCopiedKey, setPeerRecommendationCopiedKey] = useState('')
  const [copiedChannelIDKey, setCopiedChannelIDKey] = useState('')

  const [peerAddress, setPeerAddress] = useState('')
  const [peerTemporary, setPeerTemporary] = useState(false)
  const [peerStatus, setPeerStatus] = useState('')
  const [boostStatus, setBoostStatus] = useState('')
  const [boostRunning, setBoostRunning] = useState(false)
  const [peers, setPeers] = useState<Peer[]>([])
  const [closedChannels, setClosedChannels] = useState<ClosedChannel[]>([])
  const [closedChannelStatus, setClosedChannelStatus] = useState('')
  const [closeRecoveryStatusData, setCloseRecoveryStatusData] = useState<CloseRecoveryStatus | null>(null)
  const [closeRecoverySessions, setCloseRecoverySessions] = useState<CloseRecoverySession[]>([])
  const [closeRecoveryStatus, setCloseRecoveryStatus] = useState('')
  const [closeRecoveryBusyByID, setCloseRecoveryBusyByID] = useState<Record<number, boolean>>({})
  const [closeRecoveryActionStatusByID, setCloseRecoveryActionStatusByID] = useState<Record<number, string>>({})
  const [channelRankingMap, setChannelRankingMap] = useState<Record<string, ChannelRankingItem>>({})
  const [channelsSubview, setChannelsSubview] = useState<'channels' | 'close_recovery'>('channels')
  const [channelsViewMode, setChannelsViewMode] = useState<ChannelsViewMode>(() => readChannelsViewMode())
  const [viewportSize, setViewportSize] = useState(() => ({
    width: typeof window === 'undefined' ? 0 : window.innerWidth,
    height: typeof window === 'undefined' ? 0 : window.innerHeight,
  }))
  const [lightningToolsOpen, setLightningToolsOpen] = useState(false)
  const [peersOpen, setPeersOpen] = useState(false)
  const [closedChannelsOpen, setClosedChannelsOpen] = useState(false)
  const [scbRecoveryOpen, setScbRecoveryOpen] = useState(false)
  const [closedChannelSearch, setClosedChannelSearch] = useState('')
  const [closedChannelFilter, setClosedChannelFilter] = useState<'all' | 'cooperative' | 'force' | 'breach' | 'other'>('all')
  const [peerListStatus, setPeerListStatus] = useState('')
  const [peerActionStatus, setPeerActionStatus] = useState('')
  const [watchtowers, setWatchtowers] = useState<Watchtower[]>([])
  const [watchtowerAddress, setWatchtowerAddress] = useState('')
  const [watchtowerStatus, setWatchtowerStatus] = useState('')
  const [watchtowerBusy, setWatchtowerBusy] = useState(false)

  const [amboss, setAmboss] = useState<AmbossHealthStatus | null>(null)
  const [ambossStatus, setAmbossStatus] = useState('')
  const [ambossBusy, setAmbossBusy] = useState(false)
  const [chanHeal, setChanHeal] = useState<ChanHealStatus | null>(null)
  const [chanHealStatus, setChanHealStatus] = useState('')
  const [chanHealBusy, setChanHealBusy] = useState(false)
  const [chanHealInterval, setChanHealInterval] = useState('300')
  const [htlcManager, setHtlcManager] = useState<HtlcManagerStatus | null>(null)
  const [htlcManagerStatus, setHtlcManagerStatus] = useState('')
  const [htlcManagerBusy, setHtlcManagerBusy] = useState(false)
  const [htlcManagerIntervalMinutes, setHtlcManagerIntervalMinutes] = useState('240')
  const [htlcManagerMinSat, setHtlcManagerMinSat] = useState('1')
  const [htlcManagerMaxPct, setHtlcManagerMaxPct] = useState('0')
  const [htlcManagerLogs, setHtlcManagerLogs] = useState<HtlcManagerLogEntry[]>([])
  const [htlcManagerLogsOpen, setHtlcManagerLogsOpen] = useState(false)
  const [htlcManagerFailed, setHtlcManagerFailed] = useState<HtlcManagerFailedEntry[]>([])
  const [htlcManagerFailedOpen, setHtlcManagerFailedOpen] = useState(false)
  const [failedPaymentsCleaner, setFailedPaymentsCleaner] = useState<FailedPaymentsCleanerStatus | null>(null)
  const [failedPaymentsCleanerStatus, setFailedPaymentsCleanerStatus] = useState('')
  const [failedPaymentsCleanerBusy, setFailedPaymentsCleanerBusy] = useState(false)
  const [failedPaymentsCleanerIntervalHours, setFailedPaymentsCleanerIntervalHours] = useState('24')
  const [torPeerChecker, setTorPeerChecker] = useState<TorPeerCheckerStatus | null>(null)
  const [torPeerCheckerStatus, setTorPeerCheckerStatus] = useState('')
  const [torPeerCheckerBusy, setTorPeerCheckerBusy] = useState(false)
  const [torPeerCheckerIntervalHours, setTorPeerCheckerIntervalHours] = useState('2')
  const [torPeerCheckerLogs, setTorPeerCheckerLogs] = useState<TorPeerCheckerLogEntry[]>([])
  const [torPeerCheckerLogsOpen, setTorPeerCheckerLogsOpen] = useState(false)
  const [signMessage, setSignMessage] = useState('')
  const [signSignature, setSignSignature] = useState('')
  const [signStatus, setSignStatus] = useState('')
  const [signBusy, setSignBusy] = useState(false)
  const [signCopied, setSignCopied] = useState(false)
  const [scbRestoreData, setScbRestoreData] = useState('')
  const [scbRestoreFileName, setScbRestoreFileName] = useState('')
  const [scbRestoreConfirm, setScbRestoreConfirm] = useState(false)
  const [scbRestorePhrase, setScbRestorePhrase] = useState('')
  const [scbRestoreBusy, setScbRestoreBusy] = useState(false)
  const [scbRestoreStatus, setScbRestoreStatus] = useState('')
  const [scbRestoreResult, setScbRestoreResult] = useState<number | null>(null)
  const [bitcoinLocal, setBitcoinLocal] = useState<BitcoinLocalStatus | null>(null)

  const [autofeeConfig, setAutofeeConfig] = useState<AutofeeConfig | null>(null)
  const [autofeeStatus, setAutofeeStatus] = useState<AutofeeStatus | null>(null)
  const [autofeeSettings, setAutofeeSettings] = useState<Record<string, boolean>>({})
  const [autofeeBusy, setAutofeeBusy] = useState(false)
  const [autofeeMessage, setAutofeeMessage] = useState('')
  const [autofeeEnabled, setAutofeeEnabled] = useState(false)
  const [autofeeOperationMode, setAutofeeOperationMode] = useState('balanced')
  const [autofeeProfile, setAutofeeProfile] = useState('moderate')
  const [autofeeLookback, setAutofeeLookback] = useState('7')
  const [autofeeIntervalHours, setAutofeeIntervalHours] = useState('4')
  const [autofeeCooldownUp, setAutofeeCooldownUp] = useState('3')
  const [autofeeCooldownDown, setAutofeeCooldownDown] = useState('4')
  const [autofeeMovementOpen, setAutofeeMovementOpen] = useState(false)
  const [autofeeStepCapOverride, setAutofeeStepCapOverride] = useState('')
  const [autofeeDiscoveryStepCapDownOverride, setAutofeeDiscoveryStepCapDownOverride] = useState('')
  const [autofeeStallFloorRelaxGapFracOverride, setAutofeeStallFloorRelaxGapFracOverride] = useState('')
  const [autofeeInboundDiscountMaxRatioOverride, setAutofeeInboundDiscountMaxRatioOverride] = useState('')
  const [autofeeInboundDiscountReachOutRatioOverride, setAutofeeInboundDiscountReachOutRatioOverride] = useState('')
  const [autofeeInboundDiscountMinRetainedSpreadFracOverride, setAutofeeInboundDiscountMinRetainedSpreadFracOverride] = useState('')
  const [autofeeOutrateFloorFactorLowOverride, setAutofeeOutrateFloorFactorLowOverride] = useState('')
  const [autofeeSoftenMinOutRatioOverride, setAutofeeSoftenMinOutRatioOverride] = useState('')
  const [autofeeSoftenMaxDropToPegFracOverride, setAutofeeSoftenMaxDropToPegFracOverride] = useState('')
  const [autofeeHtlcMinAttemptsOverride, setAutofeeHtlcMinAttemptsOverride] = useState('')
  const [autofeeHtlcPolicyFailRateOverride, setAutofeeHtlcPolicyFailRateOverride] = useState('')
  const [autofeeHtlcLiquidityFailRateOverride, setAutofeeHtlcLiquidityFailRateOverride] = useState('')
  const [autofeeRebalMode, setAutofeeRebalMode] = useState('blend')
  const [autofeeMinPpm, setAutofeeMinPpm] = useState('10')
  const [autofeeMaxPpm, setAutofeeMaxPpm] = useState('2000')
  const [autofeeNativeSeedEnabled, setAutofeeNativeSeedEnabled] = useState(false)
  const [autofeeAmbossEnabled, setAutofeeAmbossEnabled] = useState(false)
  const [autofeeAmbossToken, setAutofeeAmbossToken] = useState('')
  const [autofeeRefreshIncludeInbound, setAutofeeRefreshIncludeInbound] = useState(true)
  const [autofeeInboundPassive, setAutofeeInboundPassive] = useState(false)
  const [autofeeDiscovery, setAutofeeDiscovery] = useState(true)
  const [autofeeExplorer, setAutofeeExplorer] = useState(true)
  const [autofeeIdleRefresh, setAutofeeIdleRefresh] = useState(false)
  const [autofeeSuperSource, setAutofeeSuperSource] = useState(false)
  const [autofeeSuperSourceBaseFee, setAutofeeSuperSourceBaseFee] = useState('1000')
  const [autofeeRevfloor, setAutofeeRevfloor] = useState(true)
  const [autofeeCircuitBreaker, setAutofeeCircuitBreaker] = useState(true)
  const [autofeeExtremeDrain, setAutofeeExtremeDrain] = useState(true)
  const [autofeeHtlcSignalEnabled, setAutofeeHtlcSignalEnabled] = useState(true)
  const [autofeeHtlcMode, setAutofeeHtlcMode] = useState('full')
  const [autofeeOpen, setAutofeeOpen] = useState(false)
  const [autofeeResultsOpen, setAutofeeResultsOpen] = useState(false)
  const [autofeeResults, setAutofeeResults] = useState<string[]>([])
  const [autofeeResultItems, setAutofeeResultItems] = useState<AutofeeResultItem[]>([])
  const [autofeeResultsStatus, setAutofeeResultsStatus] = useState('')
  const [autofeeResultsRuns, setAutofeeResultsRuns] = useState('4')
  const [autofeeResultsFrom, setAutofeeResultsFrom] = useState('')
  const [autofeeResultsTo, setAutofeeResultsTo] = useState('')
  const [autofeeHistoryOpenChannelKey, setAutofeeHistoryOpenChannelKey] = useState<string | null>(null)
  const [autofeeHistoryByChannel, setAutofeeHistoryByChannel] = useState<Record<string, AutofeeChannelRound[]>>({})
  const [autofeeHistoryLoadingByChannel, setAutofeeHistoryLoadingByChannel] = useState<Record<string, boolean>>({})
  const [autofeeHistoryErrorByChannel, setAutofeeHistoryErrorByChannel] = useState<Record<string, string>>({})
  const [autofeeRefreshBusyByPoint, setAutofeeRefreshBusyByPoint] = useState<Record<string, boolean>>({})
  const [autofeeRefreshFlashByPoint, setAutofeeRefreshFlashByPoint] = useState<Record<string, 'success' | 'same' | 'error'>>({})

  const [chanStatusBusy, setChanStatusBusy] = useState<string | null>(null)
  const [chanStatusMessage, setChanStatusMessage] = useState('')

  const [openPeer, setOpenPeer] = useState('')
  const [openAmount, setOpenAmount] = useState('')
  const [openPushSat, setOpenPushSat] = useState('')
  const [openCloseAddress, setOpenCloseAddress] = useState('')
  const [openFeeMode, setOpenFeeMode] = useState<'auto' | 'manual'>('auto')
  const [openFeeRate, setOpenFeeRate] = useState('')
  const [openFeeHint, setOpenFeeHint] = useState<MempoolFeeHint | null>(null)
  const [openFeeStatus, setOpenFeeStatus] = useState('')
  const [openPreview, setOpenPreview] = useState<OpenChannelPreview | null>(null)
  const [openPreviewLoading, setOpenPreviewLoading] = useState(false)
  const [openPreviewStatus, setOpenPreviewStatus] = useState('')
  const [openPrivate, setOpenPrivate] = useState(false)
  const [openStatus, setOpenStatus] = useState('')
  const [openChannelPoint, setOpenChannelPoint] = useState('')
  const [batchPeer, setBatchPeer] = useState('')
  const [batchAmount, setBatchAmount] = useState('')
  const [batchCloseAddress, setBatchCloseAddress] = useState('')
  const [batchPrivate, setBatchPrivate] = useState(false)
  const [batchItems, setBatchItems] = useState<BatchOpenItem[]>([])
  const [batchFeeMode, setBatchFeeMode] = useState<'auto' | 'manual'>('auto')
  const [batchFeeRate, setBatchFeeRate] = useState('')
  const [batchFeeStatus, setBatchFeeStatus] = useState('')
  const [batchPreview, setBatchPreview] = useState<BatchOpenPreview | null>(null)
  const [batchPreviewLoading, setBatchPreviewLoading] = useState(false)
  const [batchPreviewStatus, setBatchPreviewStatus] = useState('')
  const [batchStatus, setBatchStatus] = useState('')
  const [batchBusy, setBatchBusy] = useState(false)
  const [batchChannelPoints, setBatchChannelPoints] = useState<string[]>([])
  const [balancedOpenInfo, setBalancedOpenInfo] = useState<BalancedOpenStatusPayload | null>(null)
  const [balancedOpenSessions, setBalancedOpenSessions] = useState<BalancedOpenSession[]>([])
  const [balancedPeer, setBalancedPeer] = useState('')
  const [balancedCapacity, setBalancedCapacity] = useState('')
  const [balancedFeeRate, setBalancedFeeRate] = useState('')
  const [balancedFeeStatus, setBalancedFeeStatus] = useState('')
  const [balancedCloseAddress, setBalancedCloseAddress] = useState('')
  const [balancedPrivate, setBalancedPrivate] = useState(false)
  const [balancedOpenStatus, setBalancedOpenStatus] = useState('')
  const [balancedOpenBusy, setBalancedOpenBusy] = useState(false)
  const [balancedOpenRefreshBusy, setBalancedOpenRefreshBusy] = useState(false)
  const [balancedOpenActionBusyID, setBalancedOpenActionBusyID] = useState('')
  const [balancedOpenDetailsSessionID, setBalancedOpenDetailsSessionID] = useState('')
  const [balancedOpenEventsBySession, setBalancedOpenEventsBySession] = useState<Record<string, BalancedOpenEvent[]>>({})
  const [balancedOpenEventsLoadingSessionID, setBalancedOpenEventsLoadingSessionID] = useState('')
  const [balancedOpenEventsErrorBySession, setBalancedOpenEventsErrorBySession] = useState<Record<string, string>>({})

  const [closePoint, setClosePoint] = useState('')
  const [closeForce, setCloseForce] = useState(false)
  const [closeFeeMode, setCloseFeeMode] = useState<'auto' | 'manual'>('auto')
  const [closeFeeRate, setCloseFeeRate] = useState('')
  const [closeFeeHint, setCloseFeeHint] = useState<MempoolFeeHint | null>(null)
  const [closeFeeStatus, setCloseFeeStatus] = useState('')
  const [closeStatus, setCloseStatus] = useState('')
  const [closingTxHints, setClosingTxHints] = useState<Record<string, string>>({})
  const [pendingForceBusyByPoint, setPendingForceBusyByPoint] = useState<Record<string, boolean>>({})
  const [pendingForceStatusByPoint, setPendingForceStatusByPoint] = useState<Record<string, string>>({})
  const [pendingOpenBumpBusyByPoint, setPendingOpenBumpBusyByPoint] = useState<Record<string, boolean>>({})
  const [pendingOpenBumpStatusByPoint, setPendingOpenBumpStatusByPoint] = useState<Record<string, string>>({})
  const closeCardRef = useRef<HTMLDivElement | null>(null)
  const closeSelectRef = useRef<HTMLSelectElement | null>(null)
  const openPeerInputRef = useRef<HTMLInputElement | null>(null)
  const batchPeerInputRef = useRef<HTMLInputElement | null>(null)

  const [feeScopeAll, setFeeScopeAll] = useState(true)
  const [feeChannelPoint, setFeeChannelPoint] = useState('')
  const [baseFeeMsat, setBaseFeeMsat] = useState('')
  const [feeRatePpm, setFeeRatePpm] = useState('')
  const [timeLockDelta, setTimeLockDelta] = useState('')
  const [inboundEnabled, setInboundEnabled] = useState(false)
  const [inboundBaseMsat, setInboundBaseMsat] = useState('')
  const [inboundFeeRatePpm, setInboundFeeRatePpm] = useState('')
  const [feeLoadStatus, setFeeLoadStatus] = useState('')
  const [feeLoading, setFeeLoading] = useState(false)
  const [inlineFeeChannelPoint, setInlineFeeChannelPoint] = useState('')
  const [inlineFeeRatePpm, setInlineFeeRatePpm] = useState('')
  const [inlineBaseFeeMsat, setInlineBaseFeeMsat] = useState('')
  const [inlineInboundFeeRatePpm, setInlineInboundFeeRatePpm] = useState('')
  const [inlineInboundBaseMsat, setInlineInboundBaseMsat] = useState('0')
  const [inlineTimeLockDelta, setInlineTimeLockDelta] = useState('0')
  const [inlineFeeStatus, setInlineFeeStatus] = useState('')
  const [inlineFeeLoading, setInlineFeeLoading] = useState(false)
  const [inlineFeeSaving, setInlineFeeSaving] = useState(false)
  const [condensedFeeDrafts, setCondensedFeeDrafts] = useState<Record<string, string>>({})
  const [condensedFeeBusyByPoint, setCondensedFeeBusyByPoint] = useState<Record<string, boolean>>({})
  const [condensedFeeFlashByPoint, setCondensedFeeFlashByPoint] = useState<Record<string, 'success' | 'error'>>({})
  const chanHealLastAttemptRef = useRef('')
  const chanHealIntervalDirtyRef = useRef(false)
  const htlcManagerFormDirtyRef = useRef(false)
  const failedPaymentsCleanerIntervalDirtyRef = useRef(false)
  const torPeerCheckerIntervalDirtyRef = useRef(false)
  const batchItemIdRef = useRef(1)
  const pendingScrollChannelRef = useRef('')
  const pendingScrollPeerRef = useRef('')
  const pendingScrollSectionRef = useRef('')
  const focusClearTimerRef = useRef<number | null>(null)
  const peerFocusClearTimerRef = useRef<number | null>(null)
  const condensedFeeFlashTimersRef = useRef<Record<string, number>>({})
  const autofeeRefreshFlashTimersRef = useRef<Record<string, number>>({})
  const [feeStatus, setFeeStatus] = useState('')

  const formatPing = (value: number) => {
    if (!value || value <= 0) return t('common.na')
    const ms = value / 1000
    if (ms < 1000) return t('lightningOps.pingMs', { value: ms.toFixed(1) })
    return t('lightningOps.pingSeconds', { value: (ms / 1000).toFixed(1) })
  }

  const formatAge = (timestamp?: number) => {
    if (!timestamp) return ''
    const ageMs = Date.now() - timestamp * 1000
    if (ageMs <= 0) return t('common.justNow')
    const seconds = Math.floor(ageMs / 1000)
    if (seconds < 60) return t('lightningOps.ageSeconds', { count: seconds })
    const minutes = Math.floor(seconds / 60)
    if (minutes < 60) return t('lightningOps.ageMinutes', { count: minutes })
    const hours = Math.floor(minutes / 60)
    if (hours < 24) return t('lightningOps.ageHours', { count: hours })
    const days = Math.floor(hours / 24)
    return t('lightningOps.ageDays', { count: days })
  }

  const formatAmbossTime = (value?: string) => {
    if (!value) return t('common.na')
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return t('common.unknownTime')
    return date.toLocaleString()
  }

  const formatCloseRecoveryTime = (value?: string) => {
    if (!value) return t('common.unknownTime')
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return t('common.unknownTime')
    return date.toLocaleString()
  }

  const formatCloseRecoveryAge = (seconds?: number) => {
    const total = Math.max(0, Math.trunc(Number(seconds || 0)))
    if (!total) return t('common.justNow')
    if (total < 60) return t('lightningOps.ageSeconds', { count: total })
    const minutes = Math.floor(total / 60)
    if (minutes < 60) return t('lightningOps.ageMinutes', { count: minutes })
    const hours = Math.floor(minutes / 60)
    if (hours < 24) return t('lightningOps.ageHours', { count: hours })
    const days = Math.floor(hours / 24)
    return t('lightningOps.ageDays', { count: days })
  }

  const closeRecoveryStateLabel = (value: string) => {
    switch (value) {
      case 'coop_requested':
        return t('lightningOps.closeRecoveryStateCoopRequested')
      case 'coop_blocked_by_htlcs':
        return t('lightningOps.closeRecoveryStateHtlcBlocked')
      case 'waiting_close_no_txid':
        return t('lightningOps.closeRecoveryStateWaitingClose')
      case 'closing_tx_seen_unconfirmed':
        return t('lightningOps.closeRecoveryStateClosingSeen')
      case 'force_close_requested':
        return t('lightningOps.closeRecoveryStateForceRequested')
      case 'force_close_active':
        return t('lightningOps.closeRecoveryStateForceActive')
      case 'outputs_timelocked':
        return t('lightningOps.closeRecoveryStateOutputsTimelocked')
      case 'sweep_pending':
        return t('lightningOps.closeRecoveryStateSweepPending')
      case 'sweep_stuck':
        return t('lightningOps.closeRecoveryStateSweepStuck')
      case 'funds_recovered':
        return t('lightningOps.closeRecoveryStateFundsRecovered')
      case 'closed_terminal':
        return t('lightningOps.closeRecoveryStateClosed')
      case 'failed_manual_attention':
        return t('lightningOps.closeRecoveryStateManualAttention')
      default:
        return value || t('common.unknown')
    }
  }

  const closeRecoveryBadgeClass = (value?: string) => {
    switch (value) {
      case 'warn':
        return 'bg-amber-500/20 text-amber-100'
      case 'error':
      case 'err':
        return 'bg-rose-500/20 text-rose-100'
      default:
        return 'bg-emerald-500/20 text-emerald-100'
    }
  }

  const closeRecoveryActionLabel = (value?: string) => {
    switch (value) {
      case 'wait':
        return t('lightningOps.closeRecoveryActionWait')
      case 'recover_or_monitor':
        return t('lightningOps.closeRecoveryActionRecover')
      case 'force_close':
        return t('lightningOps.closeRecoveryActionForceClose')
      case 'wait_maturity':
        return t('lightningOps.closeRecoveryActionWaitMaturity')
      case 'review_sweep':
        return t('lightningOps.closeRecoveryActionReviewSweep')
      case 'monitor':
        return t('lightningOps.closeRecoveryActionMonitor')
      default:
        return t('common.na')
    }
  }

  const closeRecoveryModeLabel = (value?: string) => {
    switch (String(value || '').trim()) {
      case 'force':
        return t('lightningOps.closeRecoveryModeForce')
      case 'coop':
        return t('lightningOps.closeRecoveryModeCoop')
      default:
        return ''
    }
  }

  const closeRecoveryModeBadgeClass = (value?: string) => {
    switch (String(value || '').trim()) {
      case 'force':
        return 'bg-rose-500/20 text-rose-100'
      case 'coop':
        return 'bg-sky-500/20 text-sky-100'
      default:
        return 'bg-white/5 text-fog/60'
    }
  }

  const channelRankingStateLabel = (value?: string) => {
    switch (String(value || '').trim()) {
      case 'expand':
        return t('channelRanking.states.expand')
      case 'maintain':
        return t('channelRanking.states.maintain')
      case 'close':
        return t('channelRanking.states.close')
      default:
        return t('channelRanking.states.monitor')
    }
  }

  const channelRankingBadgeClass = (value?: string) => {
    switch (String(value || '').trim()) {
      case 'expand':
        return 'border-emerald-400/30 bg-emerald-500/15 text-emerald-200'
      case 'maintain':
        return 'border-sky-300/30 bg-sky-500/15 text-sky-100'
      case 'close':
        return 'border-rose-400/30 bg-rose-500/15 text-rose-100'
      default:
        return 'border-amber-300/30 bg-amber-500/15 text-amber-100'
    }
  }

  const formatSatFromMsat = (value?: number) => {
    if (!value || value <= 0) return '-'
    return Math.round(value / 1000).toLocaleString()
  }

  const formatFeeMsat = (value?: number) => {
    if (typeof value !== 'number' || value < 0) return '-'
    return value.toLocaleString()
  }

  const formatSatsValue = (value?: number) => {
    const sats = Math.max(0, Math.trunc(Number(value || 0)))
    return `${sats.toLocaleString()} ${t('lightningOps.autofeeResultsSats')}`
  }

  const normalizeClosedChannelType = (value?: string) => {
    switch (String(value || '').trim().toUpperCase()) {
      case 'COOPERATIVE_CLOSE':
        return t('lightningOps.closedChannelTypeCooperative')
      case 'LOCAL_FORCE_CLOSE':
        return t('lightningOps.closedChannelTypeLocalForce')
      case 'REMOTE_FORCE_CLOSE':
        return t('lightningOps.closedChannelTypeRemoteForce')
      case 'BREACH_CLOSE':
        return t('lightningOps.closedChannelTypeBreach')
      case 'FUNDING_CANCELED':
        return t('lightningOps.closedChannelTypeFundingCanceled')
      case 'ABANDONED':
        return t('lightningOps.closedChannelTypeAbandoned')
      default:
        return value || t('common.unknown')
    }
  }

  const normalizeInitiatorLabel = (value?: string) => {
    switch (String(value || '').trim().toUpperCase()) {
      case 'INITIATOR_LOCAL':
        return t('lightningOps.closedChannelInitiatorLocal')
      case 'INITIATOR_REMOTE':
        return t('lightningOps.closedChannelInitiatorRemote')
      case 'INITIATOR_BOTH':
        return t('lightningOps.closedChannelInitiatorBoth')
      default:
        return t('lightningOps.closedChannelInitiatorUnknown')
    }
  }

  const closedChannelTypeCategory = (item: ClosedChannel) => {
    const label = String(item.close_type_label || '').trim().toUpperCase()
    if (label === 'COOPERATIVE_CLOSE') return 'cooperative'
    if (label.includes('FORCE_CLOSE')) return 'force'
    if (label === 'BREACH_CLOSE') return 'breach'
    return 'other'
  }

  const closedChannelBadgeClass = (item: ClosedChannel) => {
    switch (closedChannelTypeCategory(item)) {
      case 'cooperative':
        return 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
      case 'force':
        return 'bg-amber-500/15 text-amber-200 border border-amber-400/30'
      case 'breach':
        return 'bg-fuchsia-500/25 text-fuchsia-100 border border-fuchsia-300/70'
      default:
        return 'bg-white/10 text-fog/70 border border-white/10'
    }
  }

  const formatCloseHeight = (value?: number) => {
    const height = Number(value || 0)
    if (height <= 0) return t('common.na')
    const currentHeight = Number(channelBlockHeight || 0)
    if (currentHeight > 0 && currentHeight >= height) {
      return t('lightningOps.closedChannelHeightWithAge', {
        height: height.toLocaleString(),
        blocks: (currentHeight - height).toLocaleString(),
      })
    }
    return t('lightningOps.closedChannelHeightOnly', { height: height.toLocaleString() })
  }

  const formatClosedAt = (value?: string) => {
    const raw = String(value || '').trim()
    if (!raw) return ''
    const parsed = new Date(raw)
    if (Number.isNaN(parsed.getTime())) return ''
    return parsed.toLocaleString(locale, {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    })
  }

  const balancedCapacitySat = Math.trunc(Number(balancedCapacity || 0))
  const balancedFeeRateEstimateSatVb = Math.max(
    1,
    Math.trunc(Number(balancedFeeRate || 0)) || Math.trunc(Number(openFeeHint?.fastest || 0)) || 1,
  )
  const balancedTransitSatEstimate = balancedCapacitySat > 0
    ? Math.floor((balancedCapacitySat + (BALANCED_OPEN_FUNDING_VBYTES * balancedFeeRateEstimateSatVb)) / 2)
    : 0
  const balancedPreflightSpendingSat = balancedCapacitySat > 0
    ? balancedCapacitySat + balancedTransitSatEstimate
    : 0
  const balancedRequiredSpendableSat = balancedCapacitySat > 0
    ? balancedPreflightSpendingSat + BALANCED_OPEN_REQUIRED_REMAINING_SAT
    : 0
  const balancedWalletConfirmedSat = Math.max(0, Math.trunc(Number(balancedOpenInfo?.wallet?.confirmed_sat || 0)))
  const balancedWalletSpendableSat = Math.max(0, Math.trunc(Number(balancedOpenInfo?.wallet?.estimated_spendable_sat || 0)))
  const balancedWalletLockedSat = Math.max(0, Math.trunc(Number(balancedOpenInfo?.wallet?.locked_sat || 0)))
  const balancedWalletReservedAnchorSat = Math.max(0, Math.trunc(Number(balancedOpenInfo?.wallet?.reserved_anchor_sat || 0)))
  const balancedRequiredConfirmedSat = balancedRequiredSpendableSat > 0
    ? balancedRequiredSpendableSat + balancedWalletLockedSat + balancedWalletReservedAnchorSat
    : 0
  const balancedSpendableEnough = balancedRequiredSpendableSat > 0 && balancedWalletSpendableSat >= balancedRequiredSpendableSat
  const balancedConfirmedEnough = balancedRequiredConfirmedSat > 0 && balancedWalletConfirmedSat >= balancedRequiredConfirmedSat

  const activeAutofeeProfileDefaults = autofeeConfig?.profile_defaults?.[autofeeProfile]
  const pctText = (ratio: number, decimals = 0) => `${(ratio * 100).toFixed(decimals)}%`
  const withHint = (label: string, hint: string) => (
    <span className="inline-flex items-center gap-1">
      <span>{label}</span>
      <span
        className="inline-flex h-4 w-4 items-center justify-center rounded-full border border-white/20 text-[10px] text-fog/60 cursor-help"
        title={hint}
      >
        i
      </span>
    </span>
  )

  const autofeeAllChecked = useMemo(() => {
    if (!channels.length) return true
    return channels.every((ch) => {
      const key = autofeeChannelKey(ch.channel_point, ch.channel_id)
      return key ? (autofeeSettings[key] ?? true) : true
    })
  }, [channels, autofeeSettings])

  const formattedAutofeeResults = useMemo(() => {
    return autofeeResults.map((line) => {
      if (!line.startsWith('\u26a1')) {
        return line
      }
      const idx = line.lastIndexOf('|')
      if (idx <= 0) {
        return line
      }
      const raw = line.slice(idx + 1).trim()
      const parsed = new Date(raw)
      if (Number.isNaN(parsed.getTime())) {
        return line
      }
      return `${line.slice(0, idx + 1)} ${parsed.toLocaleString()}`
    })
  }, [autofeeResults])

  const visibleHtlcManagerFailed = useMemo(() => {
    return htlcManagerFailed.filter((entry) => {
      if ((entry.event || '').toLowerCase() !== 'forward_fail') {
        return true
      }
      const detail = (entry.failure_detail || '').trim()
      const reason = (entry.failure_reason || '').trim()
      return detail !== '' || reason !== ''
    })
  }, [htlcManagerFailed])

  const formatAutofeeReasonLabel = (reason?: string) => {
    const normalized = (reason || '').toLowerCase()
    if (normalized === 'manual') return t('lightningOps.autofeeResultsReasonManual')
    if (normalized === 'scheduled') return t('lightningOps.autofeeResultsReasonScheduled')
    if (normalized === 'refresh') return t('lightningOps.autofeeResultsReasonRefresh')
    if (reason) return reason.toUpperCase()
    return t('lightningOps.autofeeResultsReasonUnknown')
  }

  const formatAutofeeHistoryReason = (reason?: string) => {
    const normalized = (reason || '').toLowerCase().trim()
    if (normalized === 'manual') return t('lightningOps.autofeeHistoryManual')
    if (normalized === 'scheduled') return t('lightningOps.autofeeHistoryAutomatic')
    if (normalized === 'refresh') return t('lightningOps.autofeeHistoryRefresh')
    return t('lightningOps.autofeeHistoryUnknown')
  }

  const formatAutofeeHistoryTime = (raw?: string) => {
    if (!raw) return t('common.na')
    const parsed = new Date(raw)
    if (Number.isNaN(parsed.getTime())) return t('common.na')
    return parsed.toLocaleString()
  }

  const formatAutofeeHistoryOutcome = (round: AutofeeChannelRound) => {
    if (round.error) {
      return t('lightningOps.autofeeHistoryOutcomeError')
    }
    const local = Math.round(Number(round.local_ppm || 0))
    const next = Math.round(Number(round.new_ppm ?? local))
    const category = (round.category || '').toLowerCase().trim()
    const skipReason = (round.skip_reason || '').toLowerCase().trim()

    // For modern rounds, trust category/skip_reason first so "calculated up"
    // does not look like an applied change when it was skipped.
    if (category) {
      if (category === 'changed') {
        if (next > local) {
          return t('lightningOps.autofeeHistoryOutcomeUp', { from: local, to: next })
        }
        if (next < local) {
          return t('lightningOps.autofeeHistoryOutcomeDown', { from: local, to: next })
        }
      }
      if (skipReason === 'cooldown') {
        return t('lightningOps.autofeeHistoryOutcomeCooldown', { value: local })
      }
      return t('lightningOps.autofeeHistoryOutcomeKeep', { value: local })
    }

    // Backward compatibility for legacy rounds without category.
    if (next > local) {
      return t('lightningOps.autofeeHistoryOutcomeUp', { from: local, to: next })
    }
    if (next < local) {
      return t('lightningOps.autofeeHistoryOutcomeDown', { from: local, to: next })
    }
    if (skipReason === 'cooldown') {
      return t('lightningOps.autofeeHistoryOutcomeCooldown', { value: local })
    }
    return t('lightningOps.autofeeHistoryOutcomeKeep', { value: next })
  }

  const formatAutofeeHistorySignals = (round: AutofeeChannelRound) => {
    if ((round.reason || '').toLowerCase().trim() === 'refresh') {
      const reference = Math.round(Number(round.reference_ppm || 0))
      const source = (round.refresh_source || '').trim() || '-'
      return `🔄 ${t('lightningOps.autofeeRefreshReference')} ${reference} | ${t('lightningOps.autofeeRefreshSource')} ${source}`
    }
    const target = Math.round(Number(round.target || 0))
    const floor = Math.round(Number(round.floor || 0))
    const seed = Math.round(Number(round.seed || 0))
    const floorSrc = round.floor_src ? ` (${round.floor_src})` : ''
    return `🎯 ${t('lightningOps.autofeeHistoryTarget')} ${target} | 🧱 ${t('lightningOps.autofeeHistoryFloor')} ${floor}${floorSrc} | 🌱 ${t('lightningOps.autofeeHistorySeed')} ${seed}`
  }

  const splitAutofeeHistoryTags = (tags: string[] = []) => {
    const limiting: string[] = []
    const relaxing: string[] = []
    const seenLimiting = new Set<string>()
    const seenRelaxing = new Set<string>()

    tags.forEach((rawTag) => {
      const tag = (rawTag || '').trim()
      if (!tag) return
      const formatted = formatAutofeeHistoryTag(tag)
      if (autofeeHistoryLimitingTagSet.has(tag) || tag.startsWith('htlc-liq+') || tag.startsWith('htlc-policy+') || tag.startsWith('surge') || tag.startsWith('negm+')) {
        if (!seenLimiting.has(formatted)) {
          seenLimiting.add(formatted)
          limiting.push(formatted)
        }
        return
      }
      if (autofeeHistoryRelaxTagSet.has(tag) || tag.startsWith('idle-refresh:')) {
        if (!seenRelaxing.has(formatted)) {
          seenRelaxing.add(formatted)
          relaxing.push(formatted)
        }
      }
    })

    return { limiting, relaxing }
  }

  const formatAutofeeHistoryPrediction = (round: AutofeeChannelRound) => {
    if (!round.prediction_code) return ''
    return formatAutofeePrediction({
      kind: 'channel',
      prediction_code: round.prediction_code,
      prediction_cooldown_hours: round.prediction_cooldown_hours
    })
  }

  const formatSatsCompact = (value?: number) => {
    const sats = Number(value || 0)
    if (!sats) return `0 ${t('lightningOps.autofeeResultsSats')}`
    if (sats >= 100_000_000) {
      return `${(sats / 100_000_000).toFixed(2)} BTC`
    }
    if (sats >= 1_000_000) {
      return `${(sats / 1_000_000).toFixed(1)}M ${t('lightningOps.autofeeResultsSats')}`
    }
    if (sats >= 1_000) {
      return `${(sats / 1_000).toFixed(1)}k ${t('lightningOps.autofeeResultsSats')}`
    }
    return `${sats} ${t('lightningOps.autofeeResultsSats')}`
  }

  const formatAutofeeNodeClass = (value?: string) => {
    const normalized = (value || '').toLowerCase()
    switch (normalized) {
      case 'small':
        return t('lightningOps.autofeeResultsNodeSmall')
      case 'medium':
        return t('lightningOps.autofeeResultsNodeMedium')
      case 'large':
        return t('lightningOps.autofeeResultsNodeLarge')
      case 'xl':
        return t('lightningOps.autofeeResultsNodeXL')
      default:
        return t('common.unknown')
    }
  }

  const formatAutofeeLiquidityClass = (value?: string) => {
    const normalized = (value || '').toLowerCase()
    switch (normalized) {
      case 'drained':
        return t('lightningOps.autofeeResultsLiquidityDrained')
      case 'full':
        return t('lightningOps.autofeeResultsLiquidityFull')
      case 'balanced':
        return t('lightningOps.autofeeResultsLiquidityBalanced')
      default:
        return t('common.unknown')
    }
  }

  const formatAutofeeCalib = (item: AutofeeResultItem) => {
    const channels = item.channel_count ?? 0
    const cap = formatSatsCompact(item.total_capacity_sat)
    const avg = formatSatsCompact(item.avg_capacity_sat)
    const local = formatSatsCompact(item.local_capacity_sat)
    const ratio = typeof item.local_ratio === 'number' ? Math.round(item.local_ratio * 100) : 0
    const nodeClass = formatAutofeeNodeClass(item.node_class)
    const liqClass = formatAutofeeLiquidityClass(item.liquidity_class)
    const revfloorThr = item.revfloor_baseline ?? 0
    const revfloorMin = item.revfloor_min_abs ?? 0
    let line = t('lightningOps.autofeeResultsCalib', {
      node: nodeClass,
      channels,
      cap,
      avg,
      local,
      ratio,
      liq: liqClass,
      revfloorThr,
      revfloorMin
    })
    if (
      typeof item.low_out_factor === 'number' &&
      typeof item.low_out_thresh === 'number' &&
      typeof item.low_out_protect_thresh === 'number'
    ) {
      line += ` | low_out x${item.low_out_factor.toFixed(2)} t<${(item.low_out_thresh * 100).toFixed(1)}% p<${(item.low_out_protect_thresh * 100).toFixed(1)}%`
    }
    if (
      typeof item.htlc_node_factor === 'number' &&
      typeof item.htlc_liquidity_factor === 'number' &&
      typeof item.htlc_threshold_factor === 'number'
    ) {
      line += ` | htlc_k node=${item.htlc_node_factor.toFixed(2)} liq=${item.htlc_liquidity_factor.toFixed(2)} total=${item.htlc_threshold_factor.toFixed(2)}`
    }
    if (
      typeof item.htlc_policy_fail_rate_min === 'number' &&
      typeof item.htlc_liquidity_fail_rate_min === 'number' &&
      typeof item.htlc_forward_fail_rate_min === 'number'
    ) {
      line += ` | htlc_rate p>=${(item.htlc_policy_fail_rate_min * 100).toFixed(1)}% l>=${(item.htlc_liquidity_fail_rate_min * 100).toFixed(1)}% f>=${(item.htlc_forward_fail_rate_min * 100).toFixed(1)}%`
    }
    if (
      typeof item.htlc_global_count_factor === 'number' &&
      typeof item.htlc_global_rate_factor === 'number'
    ) {
      line += ` | htlc_global c×${item.htlc_global_count_factor.toFixed(2)} r×${item.htlc_global_rate_factor.toFixed(2)}`
    }
    return line
  }

  const formatAutofeeHeader = (item: AutofeeResultItem) => {
    const reasonLabel = formatAutofeeReasonLabel(item.reason)
    const dryLabel = item.dry_run ? t('lightningOps.autofeeResultsDryRunTag') : ''
    const rawMode = String(item.operation_mode || '').trim().toLowerCase()
    const modeLabel = rawMode === 'market_refill'
      ? t('lightningOps.autofeeOperationModeMarketRefill')
      : t('lightningOps.autofeeOperationModeBalanced')
    const ts = item.timestamp ? new Date(item.timestamp) : null
    const timeLabel = ts && !Number.isNaN(ts.getTime()) ? ts.toLocaleString() : t('common.na')
    if ((item.reason || '').toLowerCase().trim() === 'refresh') {
      return `🔄 ${item.dry_run ? 'DRY REFRESH' : 'REFRESH'} [${modeLabel}] | ${timeLabel}`
    }
    return t('lightningOps.autofeeResultsHeader', { reason: reasonLabel, dry: dryLabel, time: timeLabel, mode: modeLabel })
  }

  const formatAutofeeSummary = (item: AutofeeResultItem) => {
    const parts = [
      `${t('lightningOps.autofeeResultsUp')} ${item.up ?? 0}`,
      `${t('lightningOps.autofeeResultsDown')} ${item.down ?? 0}`,
      `${t('lightningOps.autofeeResultsFlat')} ${item.flat ?? 0}`,
      `${t('lightningOps.autofeeResultsCooldown')} ${item.cooldown ?? 0}`,
      `${t('lightningOps.autofeeResultsSmall')} ${item.small ?? 0}`,
      `${t('lightningOps.autofeeResultsSame')} ${item.same ?? 0}`,
      `${t('lightningOps.autofeeResultsDisabled')} ${item.disabled ?? 0}`,
      `${t('lightningOps.autofeeResultsInactive')} ${item.inactive ?? 0}`,
      `${t('lightningOps.autofeeResultsInboundDisc')} ${item.inbound_disc ?? 0}`,
      `${t('lightningOps.autofeeResultsSuperSource')} ${item.super_source ?? 0}`,
      `htlc_liq_hot ${item.htlc_liq_hot ?? 0}`,
      `htlc_policy_hot ${item.htlc_policy_hot ?? 0}`,
      `htlc_forward_hot ${item.htlc_forward_hot ?? 0}`,
      `htlc_low_sample ${item.htlc_sample_low ?? 0}`,
      `reversal_blocked ${item.reversal_blocked ?? 0}`,
      `reversal_confirmed ${item.reversal_confirmed ?? 0}`,
      `downcap_general ${item.downcap_general ?? 0}`,
      `downcap_low_sample ${item.downcap_low_sample ?? 0}`,
      `floor_relax ${item.floor_relax_applied ?? 0}`,
      `stall_alert ${item.stall_alert ?? 0}`,
      `htlc_window ${(item.htlc_window_min ?? 0)}m`
    ]
    if (
      typeof item.htlc_min_attempts === 'number' &&
      typeof item.htlc_min_policy_fails === 'number' &&
      typeof item.htlc_min_liquidity_fails === 'number' &&
      typeof item.htlc_min_forward_fails === 'number'
    ) {
      parts.push(
        `htlc_min a>=${item.htlc_min_attempts} p>=${item.htlc_min_policy_fails} l>=${item.htlc_min_liquidity_fails} f>=${item.htlc_min_forward_fails}`
      )
    }
    if (
      typeof item.htlc_policy_fail_rate_min === 'number' &&
      typeof item.htlc_liquidity_fail_rate_min === 'number' &&
      typeof item.htlc_forward_fail_rate_min === 'number'
    ) {
      parts.push(
        `htlc_rate p>=${(item.htlc_policy_fail_rate_min * 100).toFixed(1)}% l>=${(item.htlc_liquidity_fail_rate_min * 100).toFixed(1)}% f>=${(item.htlc_forward_fail_rate_min * 100).toFixed(1)}%`
      )
    }
    if (
      typeof item.htlc_global_count_factor === 'number' &&
      typeof item.htlc_global_rate_factor === 'number'
    ) {
      parts.push(`htlc_global c×${item.htlc_global_count_factor.toFixed(2)} r×${item.htlc_global_rate_factor.toFixed(2)}`)
    }
    if (
      typeof item.htlc_classified_total === 'number' &&
      typeof item.htlc_attempts_total === 'number'
    ) {
      parts.push(`htlc_classified ${item.htlc_classified_total}/${item.htlc_attempts_total}`)
    }
    if (typeof item.htlc_forward_fails_total === 'number') {
      parts.push(`htlc_forward ${item.htlc_forward_fails_total}`)
    }
    if (
      typeof item.htlc_link_fails_total === 'number' &&
      typeof item.htlc_forward_fails_total === 'number' &&
      typeof item.htlc_other_fails_total === 'number'
    ) {
      parts.push(`htlc_events link=${item.htlc_link_fails_total} forward=${item.htlc_forward_fails_total} other=${item.htlc_other_fails_total}`)
    }
    if (typeof item.htlc_unclassified_total === 'number') {
      parts.push(`htlc_unclassified ${item.htlc_unclassified_total}`)
    }
    if (item.htlc_top_reasons?.length) {
      parts.push(`htlc_unclassified_top ${item.htlc_top_reasons.join(' | ')}`)
    }
    return `📊 ${parts.join(' | ')}`
  }

  const formatAutofeeRefreshSummary = (item: AutofeeResultItem) => {
    const updated = item.updated_count ?? 0
    const inboundUpdated = item.inbound_disc ?? item.inbound_updated ?? 0
    const same = item.same_count ?? 0
    const skipped = item.skipped_count ?? 0
    const errors = item.error_count ?? 0
    const markup = typeof item.rebal_markup_pct === 'number' ? item.rebal_markup_pct.toFixed(0) : '0'
    const inboundPart = item.include_inbound ? ` | inbound ${inboundUpdated}` : ''
    return `🔄 ${item.dry_run ? 'DRY REFRESH SUMMARY' : 'REFRESH SUMMARY'} | updated ${updated} | same ${same} | skipped ${skipped} | errors ${errors}${inboundPart} | rebal_markup +${markup}%`
  }

  const formatAutofeeRefreshSource = (value?: string) => {
    const normalized = (value || '').trim().toLowerCase()
    switch (normalized) {
      case 'outppm7d':
        return 'outppm7d'
      case 'outppm21d':
        return 'outppm21d'
      case 'rebalppm7d':
        return 'rebalppm7d'
      case 'rebalppm21d':
        return 'rebalppm21d'
      case 'rebalppm7d+10%':
        return 'rebalppm7d+10%'
      case 'rebalppm21d+10%':
        return 'rebalppm21d+10%'
      case 'seed:amboss':
        return 'seed:amboss'
      case 'seed:native':
        return 'seed:native'
      default:
        return value || '-'
    }
  }

  const formatAutofeeSeed = (item: AutofeeResultItem) => {
    const parts = [
      `${t('lightningOps.autofeeResultsSeedAmboss')}=${item.amboss ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedMissing')}=${item.missing ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedErr')}=${item.err ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedEmpty')}=${item.empty ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedOutrate')}=${item.outrate ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedMem')}=${item.mem ?? 0}`,
      `${t('lightningOps.autofeeResultsSeedDefault')}=${item.default ?? 0}`
    ]
    let line = `🌱 ${t('lightningOps.autofeeResultsSeedLabel')} ${parts.join(' ')}`
    if (item.cooldown_ignored) {
      line += ` | ${t('lightningOps.autofeeResultsCooldownIgnored')}=1`
    }
    return line
  }

  const formatAutofeeHtlcDiag = (item: AutofeeResultItem) => {
    const parts: string[] = []
    if (
      typeof item.htlc_classified_total === 'number' &&
      typeof item.htlc_attempts_total === 'number'
    ) {
      parts.push(`htlc_classified ${item.htlc_classified_total}/${item.htlc_attempts_total}`)
    }
    if (typeof item.htlc_forward_fails_total === 'number') {
      parts.push(`htlc_forward ${item.htlc_forward_fails_total}`)
    }
    if (
      typeof item.htlc_link_fails_total === 'number' &&
      typeof item.htlc_forward_fails_total === 'number' &&
      typeof item.htlc_other_fails_total === 'number'
    ) {
      parts.push(`htlc_events link=${item.htlc_link_fails_total} forward=${item.htlc_forward_fails_total} other=${item.htlc_other_fails_total}`)
    }
    if (typeof item.htlc_unclassified_total === 'number') {
      parts.push(`htlc_unclassified ${item.htlc_unclassified_total}`)
    }
    if (item.htlc_top_reasons?.length) {
      parts.push(`htlc_unclassified_top ${item.htlc_top_reasons.join(' | ')}`)
    }
    if (!parts.length) {
      return ''
    }
    return `🧪 ${parts.join(' | ')}`
  }

  const formatAutofeeTags = (tags: string[] = [], inboundDiscount?: number, classLabel?: string) => {
    const output: string[] = []
    const seen = new Set<string>()
    const add = (tag: string) => {
      if (!tag) return
      if (seen.has(tag)) return
      seen.add(tag)
      output.push(tag)
    }

    switch ((classLabel || '').toLowerCase().trim()) {
      case 'sink':
        add('🏷️sink')
        break
      case 'source':
        add('🏷️source')
        break
      case 'router':
        add('🏷️router')
        break
      case 'unknown':
        add('🏷️unknown')
        break
      default:
        break
    }

    tags.forEach((tag) => {
      if (!tag) return
      if (tag === 'discovery') {
        add('🧭discovery')
      } else if (tag === 'discovery-hard') {
        add('🧨harddrop')
      } else if (tag === 'explorer') {
        add('🧭explorer')
      } else if (tag.startsWith('surge')) {
        add(`📈${tag}`)
      } else if (tag === 'top-rev') {
        add('💎top-rev')
      } else if (tag === 'neg-margin') {
        add('⚠️neg-margin')
      } else if (tag === 'rebal-recent') {
        add('🔁rebal-recent')
      } else if (tag === 'rebal-attempt') {
        add('🔁rebal-attempt')
      } else if (tag === 'rebal-recent-noup') {
        add('🛑rebal-noup')
      } else if (tag === 'new-inbound') {
        add('🆕NEW-inbound')
      } else if (tag === 'bootstrap') {
        add('🌱bootstrap')
      } else if (tag === 'htlc-policy-hot') {
        add('🧾policy-hot')
      } else if (tag === 'htlc-liquidity-hot') {
        add('💧liq-hot')
      } else if (tag === 'htlc-forward-hot') {
        add('forward-hot')
      } else if (tag === 'htlc-sample-low') {
        add('📉htlc-low-sample')
      } else if (tag === 'htlc-neutral-lock') {
        add('🧯htlc-neutral')
      } else if (tag.startsWith('htlc-liq+')) {
        add('💧' + tag)
      } else if (tag.startsWith('htlc-policy+')) {
        add('🧾' + tag)
      } else if (tag === 'htlc-liq-nodown') {
        add('🛑liq-nodown')
      } else if (tag === 'htlc-policy-nodown') {
        add('🛑policy-nodown')
      } else if (tag === 'htlc-neutral-nodown') {
        add('🧯neutral-nodown')
      } else if (tag === 'htlc-step-boost') {
        add('⚡htlc-step')
      } else if (tag.startsWith('negm+')) {
        add(`💹${tag}`)
      } else if (tag === 'outrate-floor') {
        add('📊outrate-floor')
      } else if (tag === 'circuit-breaker') {
        add('🧯cb')
      } else if (tag === 'extreme-drain') {
        add('⚡extreme')
      } else if (tag === 'extreme-drain-unlock') {
        add('⚡extreme-unlock')
      } else if (tag === 'extreme-drain-turbo') {
        add('⚡turbo')
      } else if (tag === 'revfloor') {
        add('🧱revfloor')
      } else if (tag === 'peg') {
        add('📌peg')
      } else if (tag === 'peg-grace') {
        add('📌peg-grace')
      } else if (tag === 'peg-demand') {
        add('📌peg-demand')
      } else if (tag === 'cooldown') {
        add('⏳cooldown')
      } else if (tag === 'cooldown-profit') {
        add('⏳profit-hold')
      } else if (tag === 'cooldown-skip') {
        add('🧭skip-cooldown')
      } else if (tag === 'hold-small') {
        add('🧊hold-small')
      } else if (tag === 'small-delta') {
        add('🧊small-delta')
      } else if (tag === 'same-ppm') {
        add('🟰same-ppm')
      } else if (tag === 'no-down-low') {
        add('🚫down-low')
      } else if (tag === 'no-down-neg-margin') {
        add('🚫down-neg')
      } else if (tag === 'super-source') {
        add('🔥super-source')
      } else if (tag === 'super-source-like') {
        add('🔥super-source-like')
      } else if (tag === 'sink-floor') {
        add('🧱sink-floor')
      } else if (tag === 'floor-relax-stall') {
        add('🧯floor-relax')
      } else if (tag === 'reversal-fasttrack') {
        add('↩️reversal-fasttrack')
      } else if (tag === 'stall-alert') {
        add('🚨stall-alert')
      } else if (tag === 'trend-up') {
        add('📈trend-up')
      } else if (tag === 'trend-down') {
        add('📉trend-down')
      } else if (tag === 'trend-flat') {
        add('➡️trend-flat')
      } else if (tag.startsWith('seed:amboss')) {
        add(`🌐${tag.replace('seed:', 'seed-')}`)
      } else if (tag.startsWith('seed:med')) {
        add('📐seed-med')
      } else if (tag.startsWith('seed:vol')) {
        add(`📉${tag.replace('seed:', 'seed-')}`)
      } else if (tag.startsWith('seed:ratio')) {
        add(`🔁${tag.replace('seed:', 'seed-')}`)
      } else if (tag.startsWith('seed:outrate')) {
        add('📊seed-outrate')
      } else if (tag.startsWith('seed:mem')) {
        add('💾seed-mem')
      } else if (tag.startsWith('seed:default')) {
        add('⚙️seed-default')
      } else if (tag.startsWith('seed:guard')) {
        add('🛡️seed-guard')
      } else if (tag.startsWith('seed:p95cap')) {
        add('🧢seed-p95')
      } else if (tag.startsWith('seed:absmax')) {
        add('🧱seed-cap')
      } else {
        add(tag)
      }
    })

    if (inboundDiscount && inboundDiscount > 0) {
      add(`↘️inb-${inboundDiscount}`)
    }
    return output.join(' ')
  }

  const formatChannelClassLabel = (label?: string) => {
    const normalized = (label || '').toLowerCase().trim()
    switch (normalized) {
      case 'sink':
        return '🏷️ sink'
      case 'source':
        return '🏷️ source'
      case 'router':
        return '🏷️ router'
      default:
        return ''
    }
  }

  const formatPpmValue = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    return `${Math.round(value)} ppm`
  }

  const formatSatSigned = (value?: number) => {
    if (typeof value !== 'number' || Number.isNaN(value)) return '-'
    const rounded = Math.round(value)
    const prefix = rounded > 0 ? '+' : ''
    return `${prefix}${rounded} sats`
  }

  const formatMovementAmount = (value?: number) => {
    const sats = Math.max(0, Math.round(Number(value || 0)))
    return `${sats.toLocaleString(locale)} sats`
  }

  const hasChannelMovementActivity = (movement?: ChannelMovement7d) => {
    if (!movement) return false
    return (
      Number(movement.forward_in_count || 0) > 0 ||
      Number(movement.forward_in_amount_sat || 0) > 0 ||
      Number(movement.forward_out_count || 0) > 0 ||
      Number(movement.forward_out_amount_sat || 0) > 0 ||
      Number(movement.rebalance_in_count || 0) > 0 ||
      Number(movement.rebalance_in_amount_sat || 0) > 0 ||
      Number(movement.rebalance_out_count || 0) > 0 ||
      Number(movement.rebalance_out_amount_sat || 0) > 0 ||
      Number(movement.lightning_out_count || 0) > 0 ||
      Number(movement.lightning_out_amount_sat || 0) > 0 ||
      Number(movement.lightning_in_count || 0) > 0 ||
      Number(movement.lightning_in_amount_sat || 0) > 0
    )
  }

  const isLowMovementChannel = (channel: Channel) => {
    const movement = channel.movement_7d
    if (!movement) return false
    const trafficCount = Math.max(0, Number(movement.forward_count || 0)) +
      Math.max(0, Number(movement.lightning_in_count || 0)) +
      Math.max(0, Number(movement.lightning_out_count || 0))
    const trafficAmount = Math.max(0, Number(movement.forward_amount_sat || 0)) +
      Math.max(0, Number(movement.lightning_in_amount_sat || 0)) +
      Math.max(0, Number(movement.lightning_out_amount_sat || 0))
    const amountThreshold = Math.max(100000, Math.round(Math.max(0, Number(channel.capacity_sat || 0)) * 0.05))
    return trafficCount === 0 || (trafficCount <= 2 && trafficAmount <= amountThreshold)
  }

  const recommendationPeerAddress = (item?: PeerRecommendation) => {
    const explicit = String(item?.peer_address || '').trim()
    if (explicit) return explicit
    const pubkey = String(item?.pub_key || '').trim()
    const host = String(item?.host || '').trim()
    if (pubkey && host) return `${pubkey}@${host}`
    return ''
  }

  const recommendationCopyKey = (channelPoint: string, pubkey: string) =>
    `${channelPoint}:${pubkey}`

  const peerRecommendationTierLabel = (value?: string) => {
    switch (String(value || '').trim()) {
      case 'fallback_balanced':
        return t('lightningOps.peerRecommendationsTierBalanced')
      case 'fallback_relaxed':
        return t('lightningOps.peerRecommendationsTierRelaxed')
      case 'fallback_loose':
        return t('lightningOps.peerRecommendationsTierLoose')
      case 'fallback_exhaustive':
        return t('lightningOps.peerRecommendationsTierExhaustive')
      default:
        return t('lightningOps.peerRecommendationsTierStrict')
    }
  }

  const peerRecommendationTierHint = (value?: string) => {
    switch (String(value || '').trim()) {
      case 'fallback_balanced':
        return t('lightningOps.peerRecommendationsHintBalanced')
      case 'fallback_relaxed':
        return t('lightningOps.peerRecommendationsHintRelaxed')
      case 'fallback_loose':
        return t('lightningOps.peerRecommendationsHintLoose')
      case 'fallback_exhaustive':
        return t('lightningOps.peerRecommendationsHintExhaustive')
      default:
        return t('lightningOps.peerRecommendationsHintStrict')
    }
  }

  const profitBadge = (profitSat?: number) => {
    if (typeof profitSat !== 'number' || Number.isNaN(profitSat)) {
      return { label: t('lightningOps.profitUnknown'), className: 'bg-white/10 text-fog/70' }
    }
    if (profitSat > 0) {
      return { label: t('lightningOps.profitPositive'), className: 'bg-glow/20 text-glow' }
    }
    if (profitSat < 0) {
      return { label: t('lightningOps.profitNegative'), className: 'bg-ember/20 text-ember' }
    }
    return { label: t('lightningOps.profitNeutral'), className: 'bg-sky/20 text-sky-200' }
  }

  const formatAutofeePrediction = (item: AutofeeResultItem) => {
    const code = (item.prediction_code || '').trim()
    if (!code) return ''
    const hours = typeof item.prediction_cooldown_hours === 'number' ? item.prediction_cooldown_hours : 0
    switch (code) {
      case 'hold_or_up':
        return t('lightningOps.autofeeResultsPredictionHoldOrUp')
      case 'reduce':
        return t('lightningOps.autofeeResultsPredictionReduce')
      case 'discovery_fast':
        return t('lightningOps.autofeeResultsPredictionDiscoveryFast')
      case 'idle_reduce':
        return t('lightningOps.autofeeResultsPredictionIdleReduce')
      case 'bias_up':
        if (hours > 0) {
          return t('lightningOps.autofeeResultsPredictionBiasUpCooldown', { hours })
        }
        return t('lightningOps.autofeeResultsPredictionBiasUp')
      case 'bias_down':
        return t('lightningOps.autofeeResultsPredictionBiasDown')
      case 'stable':
        return t('lightningOps.autofeeResultsPredictionStable')
      default:
        return ''
    }
  }

  const formatAutofeeChannelLine = (item: AutofeeResultItem) => {
    if ((item.reason || '').toLowerCase().trim() === 'refresh') {
      const alias = (item.alias || '').trim() || (item.channel_id ? `chan-${item.channel_id}` : t('common.unknown'))
      const localPpm = item.local_ppm ?? 0
      const newPpm = item.new_ppm ?? localPpm
      const delta = item.delta ?? (newPpm - localPpm)
      const deltaPct = item.delta_pct ?? (localPpm > 0 && newPpm !== localPpm ? Math.abs(delta) / localPpm * 100 : 0)
      const deltaStr = localPpm > 0 && newPpm !== localPpm ? ` (${delta >= 0 ? '+' : ''}${delta}, ${deltaPct.toFixed(1)}%)` : ''
      const source = formatAutofeeRefreshSource(item.refresh_source)
      const reference = item.reference_ppm ?? 0
      const currentInbound = item.current_inbound_discount ?? item.prev_inbound_discount ?? 0
      const targetInbound = item.target_inbound_discount ?? item.inbound_discount ?? currentInbound

      let prefix = '🔄🫤'
      if (item.category === 'changed') {
        prefix = newPpm > localPpm ? '🔄✅🔺' : newPpm < localPpm ? '🔄✅🔻' : '🔄✅➡️'
      } else if (item.category === 'skipped') {
        prefix = '🔄⏭️'
      } else if (item.category === 'error') {
        prefix = '🔄❌'
      }

      if (item.category === 'error') {
        return `${prefix} ${alias}: ${item.error || item.skip_reason || t('common.unknown')}`
      }

      const action = item.category === 'changed'
        ? `${item.dry_run ? 'DRY set' : 'set'} ${localPpm}→${newPpm} ppm${deltaStr}`
        : `keep ${localPpm} ppm`
      let line = `${prefix} ${alias}: ${action}`
      if (reference > 0) {
        line += ` | ${t('lightningOps.autofeeRefreshReference')} ≈${reference}`
      }
      line += ` | ${t('lightningOps.autofeeRefreshSource')} ${source}`
      if (currentInbound !== targetInbound) {
        line += ` | ↘️ inb ${currentInbound}→${targetInbound}`
        if (item.inbound_source) {
          line += ` (${item.inbound_source})`
        }
      } else if (targetInbound > 0) {
        line += ` | ↘️ inb ${targetInbound}`
      }
      if (item.category === 'skipped' && item.skip_reason) {
        line += ` | ${item.skip_reason}`
      }
      return line
    }

    const alias = (item.alias || '').trim() || (item.channel_id ? `chan-${item.channel_id}` : t('common.unknown'))
    const localPpm = item.local_ppm ?? 0
    const newPpm = item.new_ppm ?? localPpm
    const delta = item.delta ?? (newPpm - localPpm)
    const deltaPct = item.delta_pct ?? (localPpm > 0 && newPpm !== localPpm ? Math.abs(delta) / localPpm * 100 : 0)
    const deltaStr = localPpm > 0 && newPpm !== localPpm ? ` (${delta >= 0 ? '+' : ''}${delta}, ${deltaPct.toFixed(1)}%)` : ''

    let dir = '➡️'
    if (newPpm > localPpm) {
      dir = '🔺'
    } else if (newPpm < localPpm) {
      dir = '🔻'
    }

    const tags = item.tags ?? []
    const isCooldown = tags.includes('cooldown')
    const isHoldSmall = tags.includes('hold-small')
    const isSame = tags.includes('same-ppm')

    let prefix = '🫤'
    if (item.category === 'changed') {
      prefix = `✅${dir}`
    } else if (item.category === 'skipped') {
      if (isCooldown) {
        prefix = '⏭️⏳'
      } else if (isHoldSmall) {
        prefix = '⏭️🧊'
      } else {
        prefix = '⏭️'
      }
    } else if (item.category === 'error') {
      prefix = '❌'
    } else if (isSame) {
      prefix = '🫤⏸️'
    }

    let action = ''
    if (item.category === 'error') {
      action = t('lightningOps.autofeeResultsActionError', { error: item.error || item.skip_reason || t('common.unknown') })
    } else if (item.category === 'changed') {
      action = item.dry_run
        ? t('lightningOps.autofeeResultsActionDrySet', { from: localPpm, to: newPpm })
        : t('lightningOps.autofeeResultsActionSet', { from: localPpm, to: newPpm })
    } else {
      action = t('lightningOps.autofeeResultsActionKeep', { value: localPpm })
    }

    const outRatio = typeof item.out_ratio === 'number' ? item.out_ratio : 0
    const outPpm7d = item.out_ppm7d ?? 0
    const rebalPpm7d = item.rebal_ppm7d ?? 0
    const seed = item.seed ?? 0
    const floor = item.floor ?? 0
    const floorSrc = formatAutofeeFloorSource(item.floor_src, item.floor_base_src, item.floor_base_ppm)
    const margin = item.margin ?? 0
    const revShare = typeof item.rev_share === 'number' ? item.rev_share : 0
    const tagLine = formatAutofeeTags(tags, item.inbound_discount, item.class_label) || '-'
    const htlcAttempts = item.htlc_attempts ?? 0
    const htlcForwardFails = item.htlc_forward_fails ?? 0
    const htlcPolicyFails = item.htlc_policy_fails ?? 0
    const htlcLiquidityFails = item.htlc_liquidity_fails ?? 0
    const htlcUnclassifiedFails = item.htlc_unclassified_fails ?? 0
    const htlcWindow = item.htlc_window_min_channel ?? item.htlc_window_min ?? 0

    const prediction = formatAutofeePrediction(item)
    const targetRaw = item.target_raw ?? item.target ?? 0
    const targetFinal = item.target_final ?? item.new_ppm ?? targetRaw
    const targetLabel = targetFinal !== targetRaw ? `${targetRaw}→${targetFinal}` : `${targetRaw}`
    let baseLine = `${prefix} ${alias}: ${action}${deltaStr} | ${t('lightningOps.autofeeResultsLabelTarget')} ${targetLabel} | ${t('lightningOps.autofeeResultsLabelOutRatio')} ${outRatio.toFixed(2)} | ${t('lightningOps.autofeeResultsLabelOutPpm7d')}≈${outPpm7d} | ${t('lightningOps.autofeeResultsLabelRebalPpm7d')}≈${rebalPpm7d} | ${t('lightningOps.autofeeResultsLabelSeed')}≈${seed} | ${t('lightningOps.autofeeResultsLabelFloor')}≥${floor}${floorSrc} | ${t('lightningOps.autofeeResultsLabelMargin')}≈${margin} | ${t('lightningOps.autofeeResultsLabelRevShare')}≈${revShare.toFixed(2)} | ${tagLine}`
    if (item.new_inbound) {
      const age = typeof item.channel_age_hours === 'number' ? item.channel_age_hours : 0
      baseLine += ` | NEW inbound ${age.toFixed(1)}h`
    }
    if (newPpm === localPpm && ((item.stalled_rounds ?? 0) > 0 || (item.target_gap_ppm ?? 0) !== 0)) {
      const stalledRounds = item.stalled_rounds ?? 0
      const hoursSinceLastChange = typeof item.hours_since_last_change === 'number' ? item.hours_since_last_change : 0
      const targetGapPpm = item.target_gap_ppm ?? 0
      const targetGapPct = typeof item.target_gap_pct === 'number' ? item.target_gap_pct : 0
      const signedGapPpm = targetGapPpm >= 0 ? `+${targetGapPpm}` : `${targetGapPpm}`
      baseLine += ` | stall r=${stalledRounds} h=${hoursSinceLastChange.toFixed(1)} gap=${signedGapPpm}(${targetGapPct.toFixed(1)}%)`
    }
    if (htlcAttempts > 0) {
      baseLine += ` | htlc${Math.max(1, htlcWindow)}m a=${htlcAttempts} p=${htlcPolicyFails} l=${htlcLiquidityFails} f=${htlcForwardFails} u=${htlcUnclassifiedFails}`
    }
    return prediction ? `${baseLine} | ${prediction}` : baseLine
  }

  const formatAutofeeSectionLine = (category?: string, reason?: string) => {
    const isRefresh = (reason || '').toLowerCase().trim() === 'refresh'
    switch ((category || '').toLowerCase()) {
      case 'changed':
        return `${isRefresh ? '🔄✅' : '✅'} ${t('lightningOps.autofeeResultsSectionChanged')}`
      case 'kept':
        return `${isRefresh ? '🔄🟰' : '🟰'} ${t('lightningOps.autofeeResultsSectionNoChange')}`
      case 'skipped':
        return `${isRefresh ? '🔄⏭️' : '🟰'} ${t('lightningOps.autofeeResultsSectionNoChange')}`
      case 'error':
        return `${isRefresh ? '🔄❌' : '❌'} ${t('lightningOps.autofeeHistoryOutcomeError')}`
      default:
        return ''
    }
  }

  const formatAutofeeExplorerLine = (item: AutofeeResultItem) => {
    const alias = (item.alias || '').trim() || (item.channel_id ? `chan-${item.channel_id}` : t('common.unknown'))
    return `🧭 ${alias} ${t('lightningOps.autofeeResultsExplorerOn')}`
  }

  const buildAutofeeResultsQuery = () => {
    const runsValue = Math.max(1, Number(autofeeResultsRuns || 4))
    const payload: { runs: number; from?: string; to?: string } = { runs: runsValue }
    if (autofeeResultsFrom) {
      payload.from = autofeeResultsFrom
    }
    if (autofeeResultsTo) {
      payload.to = autofeeResultsTo
    }
    return payload
  }

  const recentAutofeeRoundsByChannel = useMemo(() => {
    return collectAutofeeChannelRounds(autofeeResultItems, 2)
  }, [autofeeResultItems])

  const handleAutofeeHistoryToggle = async (channelKey: string, enabled: boolean) => {
    if (!enabled) return
    if (!channelKey) return
    const isOpen = autofeeHistoryOpenChannelKey === channelKey
    setAutofeeHistoryOpenChannelKey(isOpen ? null : channelKey)
    if (isOpen) return

    const cached = autofeeHistoryByChannel[channelKey]
    if (Array.isArray(cached) && cached.length > 0) return
    if (autofeeHistoryLoadingByChannel[channelKey]) return

    const fromCurrent = recentAutofeeRoundsByChannel[channelKey] || []
    if (fromCurrent.length >= 2) {
      setAutofeeHistoryByChannel((prev) => ({ ...prev, [channelKey]: fromCurrent.slice(0, 2) }))
      setAutofeeHistoryErrorByChannel((prev) => ({ ...prev, [channelKey]: '' }))
      return
    }

    setAutofeeHistoryByChannel((prev) => ({ ...prev, [channelKey]: fromCurrent.slice(0, 2) }))
    setAutofeeHistoryErrorByChannel((prev) => ({ ...prev, [channelKey]: '' }))
    setAutofeeHistoryLoadingByChannel((prev) => ({ ...prev, [channelKey]: true }))
    try {
      const payload = await getAutofeeResults({ runs: 20 }) as any
      const items = Array.isArray(payload?.items) ? (payload.items as AutofeeResultItem[]) : []
      const fromExpanded = collectAutofeeChannelRounds(items, 2)
      setAutofeeHistoryByChannel((prev) => ({ ...prev, [channelKey]: fromExpanded[channelKey] || [] }))
      setAutofeeHistoryErrorByChannel((prev) => ({ ...prev, [channelKey]: '' }))
    } catch (err: any) {
      setAutofeeHistoryErrorByChannel((prev) => ({ ...prev, [channelKey]: err?.message || t('lightningOps.autofeeResultsUnavailable') }))
    } finally {
      setAutofeeHistoryLoadingByChannel((prev) => ({ ...prev, [channelKey]: false }))
    }
  }

  const localizedAutofeeResults = useMemo(() => {
    if (!autofeeResultItems.length) {
      return formattedAutofeeResults
    }
    const lines: string[] = []
    const max = Math.max(autofeeResults.length, autofeeResultItems.length)
    for (let i = 0; i < max; i += 1) {
      const item = autofeeResultItems[i]
      let line = ''
      if (item && item.kind) {
        switch (item.kind) {
          case 'header':
            line = formatAutofeeHeader(item)
            break
          case 'summary':
            line = formatAutofeeSummary(item)
            break
          case 'refresh_summary':
            line = formatAutofeeRefreshSummary(item)
            break
          case 'seed':
            line = formatAutofeeSeed(item)
            break
          case 'htlc_diag':
            line = formatAutofeeHtlcDiag(item)
            break
          case 'calib':
            line = formatAutofeeCalib(item)
            break
          case 'section':
            line = formatAutofeeSectionLine(item.category, item.reason)
            break
          case 'explorer':
            line = formatAutofeeExplorerLine(item)
            break
          case 'channel':
            line = formatAutofeeChannelLine(item)
            break
          default:
            line = ''
            break
        }
      }
      if (!line && formattedAutofeeResults[i]) {
        line = formattedAutofeeResults[i]
      }
      if (line) {
        lines.push(line)
      }
    }
    return lines.length ? lines : formattedAutofeeResults
  }, [autofeeResultItems, autofeeResults.length, formattedAutofeeResults, t])

  useEffect(() => {
    setAutofeeHistoryByChannel({})
    setAutofeeHistoryLoadingByChannel({})
    setAutofeeHistoryErrorByChannel({})
  }, [autofeeResultItems])

  useEffect(() => {
    if (typeof window === 'undefined') return
    try {
      window.localStorage.setItem(CHANNELS_VIEW_MODE_STORAGE_KEY, channelsViewMode)
    } catch {
      // Keep the UI usable when browser storage is unavailable.
    }
  }, [channelsViewMode])

  useEffect(() => {
    if (typeof window === 'undefined') return
    return () => {
      Object.values(condensedFeeFlashTimersRef.current).forEach((timer) => {
        window.clearTimeout(timer)
      })
      condensedFeeFlashTimersRef.current = {}
      Object.values(autofeeRefreshFlashTimersRef.current).forEach((timer) => {
        window.clearTimeout(timer)
      })
      autofeeRefreshFlashTimersRef.current = {}
    }
  }, [])

  const blockCadenceAvg = useMemo(() => {
    const buckets = bitcoinLocal?.block_cadence || []
    if (!buckets.length) return 0
    const windowSec = bitcoinLocal?.block_cadence_window_sec || 600
    const cadenceHours = (buckets.length * windowSec) / 3600
    if (cadenceHours <= 0) return 0
    const total = buckets.reduce((sum, bucket) => sum + (bucket?.count || 0), 0)
    return cadenceHours > 0 ? total / cadenceHours : 0
  }, [bitcoinLocal])

  const estimateMaturitySeconds = (blocks?: number) => {
    if (typeof blocks !== 'number') return null
    const secondsPerBlock = blockCadenceAvg > 0 ? 3600 / blockCadenceAvg : 600
    return Math.max(0, Math.round(blocks * secondsPerBlock))
  }

  const formatMaturityDuration = (totalSeconds?: number | null) => {
    if (totalSeconds === null || totalSeconds === undefined) return ''
    const seconds = Math.max(0, Math.floor(totalSeconds))
    const days = Math.floor(seconds / 86400)
    const hours = Math.floor((seconds % 86400) / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    const remSeconds = seconds % 60
    return `${days}d ${hours}h ${minutes}m ${remSeconds}s`
  }

  const formatChannelDowntime = (totalSeconds?: number | null) => {
    if (totalSeconds === null || totalSeconds === undefined) return ''
    const seconds = Math.max(0, Math.floor(totalSeconds))
    const days = Math.floor(seconds / 86400)
    const hours = Math.floor((seconds % 86400) / 3600)
    const minutes = Math.floor((seconds % 3600) / 60)
    if (days > 0) return `${days}d ${hours}h`
    if (hours > 0) return `${hours}h ${minutes}m`
    if (minutes > 0) return `${minutes}m`
    return `${seconds}s`
  }

  const isLocalChanDisabled = (flags?: string) => {
    if (!flags) return false
    const normalized = flags.toLowerCase()
    const tokens = normalized.split(/[|,;\s]+/).filter(Boolean)
    const scan = tokens.length ? tokens : [normalized]
    return scan.some((token) => {
      if (token.includes('localchandisabled') || token.includes('local_chan_disabled')) return true
      if (!token.includes('disabled') || token.includes('remote')) return false
      return token.includes('local') || token.includes('chanstatusdisabled') || token === 'disabled'
    })
  }

  const ambossTone = (): 'ok' | 'warn' | 'muted' => {
    if (!amboss?.enabled) return 'muted'
    if (amboss?.status === 'ok') return 'ok'
    if (amboss?.status === 'checking') return 'muted'
    return 'warn'
  }

  const chanHealTone = (): 'ok' | 'warn' | 'muted' => {
    if (!chanHeal?.enabled) return 'muted'
    if (chanHeal?.status === 'ok') return 'ok'
    if (chanHeal?.status === 'checking') return 'muted'
    return 'warn'
  }

  const htlcManagerTone = (): 'ok' | 'warn' | 'muted' => {
    if (!htlcManager?.enabled) return 'muted'
    if (htlcManager?.status === 'ok') return 'ok'
    if (htlcManager?.status === 'checking') return 'muted'
    return 'warn'
  }

  const failedPaymentsCleanerTone = (): 'ok' | 'warn' | 'muted' => {
    if (!failedPaymentsCleaner?.enabled) return 'muted'
    if (failedPaymentsCleaner?.status === 'ok') return 'ok'
    if (failedPaymentsCleaner?.status === 'checking') return 'muted'
    return 'warn'
  }

  const torPeerCheckerTone = (): 'ok' | 'warn' | 'muted' => {
    if (!torPeerChecker?.enabled) return 'muted'
    if (torPeerChecker?.status === 'ok') return 'ok'
    if (torPeerChecker?.status === 'checking') return 'muted'
    return 'warn'
  }

  const badgeClass = (tone: 'ok' | 'warn' | 'muted') => {
    if (tone === 'ok') {
      return 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30'
    }
    if (tone === 'warn') {
      return 'bg-amber-500/15 text-amber-200 border border-amber-400/30'
    }
    return 'bg-white/10 text-fog/60 border border-white/10'
  }

  const ambossBadgeLabel = () => {
    if (!amboss?.enabled) return t('common.disabled')
    if (amboss?.status === 'ok') return t('common.ok')
    if (amboss?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const chanHealBadgeLabel = () => {
    if (!chanHeal?.enabled) return t('common.disabled')
    if (chanHeal?.status === 'ok') return t('common.ok')
    if (chanHeal?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const htlcManagerBadgeLabel = () => {
    if (!htlcManager?.enabled) return t('common.disabled')
    if (htlcManager?.status === 'ok') return t('common.ok')
    if (htlcManager?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const failedPaymentsCleanerBadgeLabel = () => {
    if (!failedPaymentsCleaner?.enabled) return t('common.disabled')
    if (failedPaymentsCleaner?.status === 'ok') return t('common.ok')
    if (failedPaymentsCleaner?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const torPeerCheckerBadgeLabel = () => {
    if (!torPeerChecker?.enabled) return t('common.disabled')
    if (torPeerChecker?.status === 'ok') return t('common.ok')
    if (torPeerChecker?.status === 'checking') return t('common.check')
    return t('common.check')
  }

  const ambossURL = (pubkey: string) => `https://amboss.space/node/${pubkey}`
  const peerProfileLinkGroup = (pubkey?: string, className = 'mt-1 flex flex-wrap items-center gap-2') => {
    const normalized = String(pubkey || '').trim()
    if (!normalized) return null
    return (
      <div className={className}>
        <a
          className="text-[11px] text-sky-200 hover:text-sky-100"
          href={buildGraphExplorerHash(normalized)}
        >
          {t('nav.graphExplorer')}
        </a>
        <a
          className="text-[11px] text-emerald-100/80 hover:text-emerald-100"
          href={ambossURL(normalized)}
          target="_blank"
          rel="noopener noreferrer"
        >
          Amboss
        </a>
      </div>
    )
  }

  const applyChannelsPayload = (res: any) => {
    const list = Array.isArray(res?.channels) ? res.channels : []
    const pending = Array.isArray(res?.pending_channels) ? res.pending_channels : []
    setChannels(list)
    setChannelBlockHeight(Number(res?.current_block_height || 0))
    setActiveCount(res?.active_count ?? 0)
    setInactiveCount(res?.inactive_count ?? 0)
    setPendingOpenCount(res?.pending_open_count ?? 0)
    setPendingCloseCount(res?.pending_close_count ?? 0)
    setPendingChannels(pending)
    setClosingTxHints((prev) => {
      const next = { ...prev }
      pending.forEach((item: any) => {
        const channelPoint = String(item?.channel_point || '').trim()
        const closingTxid = String(item?.closing_txid || '').trim()
        if (channelPoint && closingTxid) {
          next[channelPoint] = closingTxid
        }
      })
      return next
    })
  }

  const refreshBalancedOpen = async (opts?: { quiet?: boolean }) => {
    const quiet = Boolean(opts?.quiet)
    if (!quiet) {
      setBalancedOpenRefreshBusy(true)
    }
    try {
      const status = await getBalancedOpenStatus() as BalancedOpenStatusPayload
      setBalancedOpenInfo(status)
      if (!status?.enabled || !status?.available) {
        setBalancedOpenSessions([])
        setBalancedOpenDetailsSessionID('')
        if (!quiet) {
          setBalancedOpenStatus(status?.error || t('lightningOps.balancedOpenUnavailable'))
        }
        return
      }

      const payload = await getBalancedOpenSessions({ limit: 40 }) as any
      const sessions = Array.isArray(payload?.items) ? payload.items as BalancedOpenSession[] : []
      setBalancedOpenSessions(sessions)
      if (balancedOpenDetailsSessionID) {
        const stillExists = sessions.some((item) => item.session_id === balancedOpenDetailsSessionID)
        if (!stillExists) {
          setBalancedOpenDetailsSessionID('')
        }
      }
      if (!quiet) {
        setBalancedOpenStatus('')
      }
    } catch (err: any) {
      if (!quiet) {
        setBalancedOpenStatus(err?.message || t('lightningOps.balancedOpenLoadFailed'))
      }
    } finally {
      if (!quiet) {
        setBalancedOpenRefreshBusy(false)
      }
    }
  }

  const refreshCloseRecovery = async (opts?: { quiet?: boolean }) => {
    const quiet = Boolean(opts?.quiet)
    if (!quiet) {
      setCloseRecoveryStatus(t('lightningOps.closeRecoveryLoading'))
    }
    try {
      const [statusRes, sessionsRes] = await Promise.all([
        getCloseManagerStatus(),
        getCloseManagerSessions(80)
      ])
      setCloseRecoveryStatusData(statusRes as CloseRecoveryStatus)
      setCloseRecoverySessions(Array.isArray((sessionsRes as any)?.items) ? (sessionsRes as any).items : [])
      if (!quiet) {
        setCloseRecoveryStatus('')
      }
    } catch (err: any) {
      setCloseRecoveryStatusData(null)
      setCloseRecoverySessions([])
      if (!quiet) {
        setCloseRecoveryStatus(err?.message || t('lightningOps.closeRecoveryLoadFailed'))
      }
    }
  }

  const refreshChannelRankings = async () => {
    try {
      const res = await getChannelRankings({ limit: 500 }) as any
      const nextItems = Array.isArray(res?.items) ? res.items as ChannelRankingItem[] : []
      setChannelRankingMap(Object.fromEntries(nextItems.map((item) => [item.channel_point, item])))
    } catch {
      setChannelRankingMap({})
    }
  }

  const load = async () => {
    setStatus(t('lightningOps.loadingChannels'))
    setPeerListStatus(t('lightningOps.loadingPeers'))
    setClosedChannelStatus(t('lightningOps.loadingClosedChannels'))
    setCloseRecoveryStatus(t('lightningOps.closeRecoveryLoading'))
    setWatchtowerStatus(t('lightningOps.watchtowerLoading'))
    setAmbossStatus(t('lightningOps.ambossHealthLoading'))
    setChanHealStatus(t('lightningOps.chanHealLoading'))
    setHtlcManagerStatus(t('lightningOps.htlcManagerLoading'))
    setFailedPaymentsCleanerStatus(t('lightningOps.failedPaymentsCleanerLoading'))
    setTorPeerCheckerStatus(t('lightningOps.torPeerLoading'))
    setAutofeeMessage(t('lightningOps.autofeeLoading'))
    const channelsRequest = getLnChannels()
    channelsRequest
      .then((res) => {
        applyChannelsPayload(res)
        setStatus('')
      })
      .catch((err: any) => {
        setStatus(err?.message || t('lightningOps.loadChannelsFailed'))
      })
    void refreshChannelRankings()
    void refreshCloseRecovery()

    const [peersResult, closedChannelsResult, watchtowerResult, ambossResult, chanHealResult, htlcManagerResult, htlcManagerLogsResult, htlcManagerFailedResult, failedPaymentsCleanerResult, torPeerCheckerResult, torPeerCheckerLogsResult, bitcoinLocalResult, autofeeConfigResult, autofeeStatusResult, autofeeChannelsResult, autofeeResultsResult, balancedOpenStatusResult, balancedOpenSessionsResult] = await Promise.allSettled([
      getLnPeers(),
      getLnClosedChannels(),
      getLnWatchtowers(),
      getAmbossHealth(),
      getLnChanHeal(),
      getLnHtlcManager(),
      getLnHtlcManagerLogs(),
      getLnHtlcManagerFailed(),
      getLnFailedPaymentsCleaner(),
      getLnTorPeerChecker(),
      getLnTorPeerCheckerLogs(),
      getBitcoinLocalStatus(),
      getAutofeeConfig(),
      getAutofeeStatus(),
      getAutofeeChannels(),
      getAutofeeResults(buildAutofeeResultsQuery()),
      getBalancedOpenStatus(),
      getBalancedOpenSessions({ limit: 40 })
    ])
    if (peersResult.status === 'fulfilled') {
      const res = peersResult.value
      setPeers(Array.isArray(res?.peers) ? res.peers : [])
      setPeerListStatus('')
    } else {
      const message = (peersResult.reason as any)?.message || t('lightningOps.loadPeersFailed')
      setPeerListStatus(message)
    }
    if (closedChannelsResult.status === 'fulfilled') {
      const res = closedChannelsResult.value as any
      setClosedChannels(Array.isArray(res?.channels) ? res.channels : [])
      setClosedChannelStatus('')
    } else {
      const message = (closedChannelsResult.reason as any)?.message || t('lightningOps.loadClosedChannelsFailed')
      setClosedChannelStatus(message)
    }
    if (watchtowerResult.status === 'fulfilled') {
      const res = watchtowerResult.value as any
      setWatchtowers(Array.isArray(res?.towers) ? res.towers : [])
      setWatchtowerStatus('')
    } else {
      const message = (watchtowerResult.reason as any)?.message || t('lightningOps.watchtowerLoadFailed')
      setWatchtowerStatus(message)
    }
    if (ambossResult.status === 'fulfilled') {
      setAmboss(ambossResult.value as AmbossHealthStatus)
      setAmbossStatus('')
    } else {
      const message = (ambossResult.reason as any)?.message || t('lightningOps.ambossHealthStatusUnavailable')
      setAmbossStatus(message)
    }
    if (chanHealResult.status === 'fulfilled') {
      const payload = chanHealResult.value as ChanHealStatus
      setChanHeal(payload)
      chanHealLastAttemptRef.current = payload?.last_attempt_at || ''
      if (payload?.interval_sec) {
        setChanHealInterval(String(payload.interval_sec))
        chanHealIntervalDirtyRef.current = false
      }
      setChanHealStatus('')
    } else {
      const message = (chanHealResult.reason as any)?.message || t('lightningOps.chanHealStatusUnavailable')
      setChanHealStatus(message)
    }
    if (htlcManagerResult.status === 'fulfilled') {
      const payload = htlcManagerResult.value as HtlcManagerStatus
      setHtlcManager(payload)
      const intervalMinutes = payload?.interval_minutes ?? ((payload?.interval_hours ?? 0) * 60)
      if (intervalMinutes > 0) {
        setHtlcManagerIntervalMinutes(String(intervalMinutes))
      }
      if (payload?.min_htlc_sat) {
        setHtlcManagerMinSat(String(payload.min_htlc_sat))
      }
      setHtlcManagerMaxPct(String(payload?.max_local_pct ?? 0))
      htlcManagerFormDirtyRef.current = false
      setHtlcManagerStatus('')
    } else {
      const message = (htlcManagerResult.reason as any)?.message || t('lightningOps.htlcManagerStatusUnavailable')
      setHtlcManagerStatus(message)
    }
    if (htlcManagerLogsResult.status === 'fulfilled') {
      const entries = (htlcManagerLogsResult.value as any)?.entries
      setHtlcManagerLogs(Array.isArray(entries) ? entries : [])
    } else if (htlcManagerResult.status === 'fulfilled') {
      setHtlcManagerLogs([])
    }
    if (htlcManagerFailedResult.status === 'fulfilled') {
      const entries = (htlcManagerFailedResult.value as any)?.entries
      setHtlcManagerFailed(Array.isArray(entries) ? entries : [])
    } else if (htlcManagerResult.status === 'fulfilled') {
      setHtlcManagerFailed([])
    }
    if (failedPaymentsCleanerResult.status === 'fulfilled') {
      const payload = failedPaymentsCleanerResult.value as FailedPaymentsCleanerStatus
      setFailedPaymentsCleaner(payload)
      setFailedPaymentsCleanerIntervalHours(String(payload?.interval_hours ?? 24))
      failedPaymentsCleanerIntervalDirtyRef.current = false
      setFailedPaymentsCleanerStatus('')
    } else {
      const message = (failedPaymentsCleanerResult.reason as any)?.message || t('lightningOps.failedPaymentsCleanerStatusUnavailable')
      setFailedPaymentsCleanerStatus(message)
    }
    if (torPeerCheckerResult.status === 'fulfilled') {
      const payload = torPeerCheckerResult.value as TorPeerCheckerStatus
      setTorPeerChecker(payload)
      setTorPeerCheckerIntervalHours(String(payload?.interval_hours ?? 2))
      torPeerCheckerIntervalDirtyRef.current = false
      setTorPeerCheckerStatus('')
    } else {
      const message = (torPeerCheckerResult.reason as any)?.message || t('lightningOps.torPeerStatusUnavailable')
      setTorPeerCheckerStatus(message)
    }
    if (torPeerCheckerLogsResult.status === 'fulfilled') {
      const entries = (torPeerCheckerLogsResult.value as any)?.entries
      setTorPeerCheckerLogs(Array.isArray(entries) ? entries : [])
    } else if (torPeerCheckerResult.status === 'fulfilled') {
      setTorPeerCheckerLogs([])
    }
    if (bitcoinLocalResult.status === 'fulfilled') {
      setBitcoinLocal(bitcoinLocalResult.value as BitcoinLocalStatus)
    }
    if (autofeeConfigResult.status === 'fulfilled') {
      const cfg = autofeeConfigResult.value as AutofeeConfig
      setAutofeeConfig(cfg)
      setAutofeeEnabled(cfg.enabled)
      setAutofeeOperationMode((cfg.operation_mode || 'balanced').trim() || 'balanced')
      setAutofeeProfile(cfg.profile || 'moderate')
      setAutofeeLookback(String(cfg.lookback_days ?? 7))
      setAutofeeIntervalHours(String(Math.max(1, Math.round((cfg.run_interval_sec || 14400) / 3600))))
      setAutofeeCooldownUp(String(Math.max(1, Math.round((cfg.cooldown_up_sec || 10800) / 3600))))
      setAutofeeCooldownDown(String(Math.max(1, Math.round((cfg.cooldown_down_sec || 14400) / 3600))))
      setAutofeeStepCapOverride((cfg.step_cap_override ?? 0) > 0 ? String(Math.round((cfg.step_cap_override ?? 0) * 1000) / 10) : '')
      setAutofeeDiscoveryStepCapDownOverride((cfg.discovery_step_cap_down_override ?? 0) > 0 ? String(Math.round((cfg.discovery_step_cap_down_override ?? 0) * 1000) / 10) : '')
      setAutofeeStallFloorRelaxGapFracOverride((cfg.stall_floor_relax_gap_frac_override ?? 0) > 0 ? String(Math.round((cfg.stall_floor_relax_gap_frac_override ?? 0) * 100)) : '')
      setAutofeeInboundDiscountMaxRatioOverride((cfg.inbound_discount_max_ratio_override ?? 0) > 0 ? String(Math.round((cfg.inbound_discount_max_ratio_override ?? 0) * 100)) : '')
      setAutofeeInboundDiscountReachOutRatioOverride((cfg.inbound_discount_reach_out_ratio_override ?? 0) > 0 ? String(Math.round((cfg.inbound_discount_reach_out_ratio_override ?? 0) * 100)) : '')
      setAutofeeInboundDiscountMinRetainedSpreadFracOverride((cfg.inbound_discount_min_retained_spread_frac_override ?? 0) > 0 ? String(Math.round((cfg.inbound_discount_min_retained_spread_frac_override ?? 0) * 100)) : '')
      setAutofeeOutrateFloorFactorLowOverride((cfg.outrate_floor_factor_low_override ?? 0) > 0 ? String(Math.round((cfg.outrate_floor_factor_low_override ?? 0) * 100)) : '')
      setAutofeeSoftenMinOutRatioOverride((cfg.soften_min_out_ratio_override ?? 0) > 0 ? String(Math.round((cfg.soften_min_out_ratio_override ?? 0) * 100)) : '')
      setAutofeeSoftenMaxDropToPegFracOverride((cfg.soften_max_drop_to_peg_frac_override ?? 0) > 0 ? String(Math.round((cfg.soften_max_drop_to_peg_frac_override ?? 0) * 100)) : '')
      setAutofeeHtlcMinAttemptsOverride((cfg.htlc_min_attempts_60m_override ?? 0) > 0 ? String(cfg.htlc_min_attempts_60m_override) : '')
      setAutofeeHtlcPolicyFailRateOverride((cfg.htlc_policy_fail_rate_override ?? 0) > 0 ? String(Math.round((cfg.htlc_policy_fail_rate_override ?? 0) * 1000) / 10) : '')
      setAutofeeHtlcLiquidityFailRateOverride((cfg.htlc_liquidity_fail_rate_override ?? 0) > 0 ? String(Math.round((cfg.htlc_liquidity_fail_rate_override ?? 0) * 1000) / 10) : '')
      setAutofeeRebalMode(cfg.rebal_cost_mode || 'blend')
      setAutofeeMinPpm(String(cfg.min_ppm ?? 10))
      setAutofeeMaxPpm(String(cfg.max_ppm ?? 2000))
      setAutofeeNativeSeedEnabled(Boolean(cfg.native_seed_enabled))
      setAutofeeAmbossEnabled(Boolean(cfg.amboss_enabled))
      setAutofeeInboundPassive(Boolean(cfg.inbound_passive_enabled))
      setAutofeeDiscovery(Boolean(cfg.discovery_enabled))
      setAutofeeExplorer(Boolean(cfg.explorer_enabled))
      setAutofeeIdleRefresh(Boolean(cfg.idle_refresh_enabled))
      setAutofeeSuperSource(Boolean(cfg.super_source_enabled))
      setAutofeeSuperSourceBaseFee(String(cfg.super_source_base_fee_msat ?? 1000))
      setAutofeeRevfloor(cfg.revfloor_enabled !== false)
      setAutofeeCircuitBreaker(cfg.circuit_breaker_enabled !== false)
      setAutofeeExtremeDrain(cfg.extreme_drain_enabled !== false)
      setAutofeeHtlcSignalEnabled(cfg.htlc_signal_enabled !== false)
      {
        const mode = (cfg.htlc_mode || 'full').toLowerCase().trim()
        const normalizedMode = mode === 'observe_only' || mode === 'policy_only' || mode === 'full' ? mode : 'full'
        setAutofeeHtlcMode(normalizedMode)
      }
      setAutofeeMessage('')
    } else {
      const message = (autofeeConfigResult.reason as any)?.message || t('lightningOps.autofeeConfigUnavailable')
      setAutofeeMessage(message)
    }
    if (autofeeStatusResult.status === 'fulfilled') {
      setAutofeeStatus(autofeeStatusResult.value as AutofeeStatus)
    }
    if (autofeeChannelsResult.status === 'fulfilled') {
      const settingsPayload = (autofeeChannelsResult.value as any)?.settings as AutofeeChannelSetting[] | undefined
      const map: Record<string, boolean> = {}
      const keyByChannelID: Record<string, string> = {}
      try {
        const channelsRes = await channelsRequest as any
        const channelsPayload = channelsRes?.channels
        if (Array.isArray(channelsPayload)) {
          channelsPayload.forEach((raw: any) => {
            const key = autofeeChannelKey(raw?.channel_point, raw?.channel_id)
            const id = normalizeAutofeeChannelID(raw?.channel_id)
            if (key && id) keyByChannelID[id] = key
          })
        }
      } catch {
        // Leave channel-id mapping empty when the channels payload is unavailable.
      }
      if (Array.isArray(settingsPayload)) {
        settingsPayload.forEach((entry) => {
          const pointKey = autofeeChannelKey(entry?.channel_point, entry?.channel_id)
          if (pointKey) {
            map[pointKey] = Boolean(entry.enabled)
            return
          }
          const id = normalizeAutofeeChannelID(entry?.channel_id_str || entry?.channel_id)
          if (id && keyByChannelID[id]) {
            map[keyByChannelID[id]] = Boolean(entry.enabled)
          }
        })
      }
      setAutofeeSettings(map)
    }
    if (autofeeResultsResult.status === 'fulfilled') {
      const payload = autofeeResultsResult.value as any
      const lines = payload?.lines
      const items = payload?.items
      setAutofeeResults(Array.isArray(lines) ? lines : [])
      setAutofeeResultItems(Array.isArray(items) ? items : [])
      setAutofeeResultsStatus('')
    } else {
      const message = (autofeeResultsResult.reason as any)?.message || t('lightningOps.autofeeResultsUnavailable')
      setAutofeeResultsStatus(message)
    }
    if (balancedOpenStatusResult.status === 'fulfilled') {
      const payload = balancedOpenStatusResult.value as BalancedOpenStatusPayload
      setBalancedOpenInfo(payload)
      if (!payload?.enabled || !payload?.available) {
        setBalancedOpenSessions([])
        setBalancedOpenDetailsSessionID('')
        setBalancedOpenStatus(payload?.error || t('lightningOps.balancedOpenUnavailable'))
      } else if (balancedOpenSessionsResult.status === 'fulfilled') {
        const itemsPayload = balancedOpenSessionsResult.value as any
        const sessions = Array.isArray(itemsPayload?.items) ? itemsPayload.items as BalancedOpenSession[] : []
        setBalancedOpenSessions(sessions)
        if (balancedOpenDetailsSessionID) {
          const stillExists = sessions.some((item) => item.session_id === balancedOpenDetailsSessionID)
          if (!stillExists) {
            setBalancedOpenDetailsSessionID('')
          }
        }
        setBalancedOpenStatus('')
      } else {
        const message = (balancedOpenSessionsResult as PromiseRejectedResult)?.reason?.message || t('lightningOps.balancedOpenLoadFailed')
        setBalancedOpenStatus(message)
      }
    } else {
      const message = (balancedOpenStatusResult.reason as any)?.message || t('lightningOps.balancedOpenUnavailable')
      setBalancedOpenInfo({ enabled: true, available: false, error: message })
      setBalancedOpenSessions([])
      setBalancedOpenDetailsSessionID('')
      setBalancedOpenStatus(message)
    }
  }

  useEffect(() => {
    load()
  }, [])

  useEffect(() => {
    let mounted = true
    const refreshChannels = () => {
      getLnChannels()
        .then((res) => {
          if (!mounted) return
          applyChannelsPayload(res)
          setStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setStatus(err?.message || t('lightningOps.loadChannelsFailed'))
        })
    }
    const refreshClosedChannels = () => {
      getLnClosedChannels()
        .then((res: any) => {
          if (!mounted) return
          setClosedChannels(Array.isArray(res?.channels) ? res.channels : [])
          setClosedChannelStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setClosedChannelStatus(err?.message || t('lightningOps.loadClosedChannelsFailed'))
        })
    }
    const pollChannelRankings = () => {
      getChannelRankings({ limit: 500 })
        .then((res: any) => {
          if (!mounted) return
          const nextItems = Array.isArray(res?.items) ? res.items as ChannelRankingItem[] : []
          setChannelRankingMap(Object.fromEntries(nextItems.map((item) => [item.channel_point, item])))
        })
        .catch(() => {
          if (!mounted) return
          setChannelRankingMap({})
        })
    }
    const fetchWatchtowers = () => {
      getLnWatchtowers()
        .then((res: any) => {
          if (!mounted) return
          setWatchtowers(Array.isArray(res?.towers) ? res.towers : [])
          setWatchtowerStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setWatchtowerStatus(err?.message || t('lightningOps.watchtowerLoadFailed'))
        })
    }
    const fetchAmboss = () => {
      getAmbossHealth()
        .then((data) => {
          if (!mounted) return
          setAmboss(data as AmbossHealthStatus)
          setAmbossStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setAmbossStatus(err?.message || t('lightningOps.ambossHealthStatusUnavailable'))
        })
    }
    const fetchChanHeal = () => {
      getLnChanHeal()
        .then((data) => {
          if (!mounted) return
          const payload = data as ChanHealStatus
          const prevAttemptAt = chanHealLastAttemptRef.current
          const nextAttemptAt = payload?.last_attempt_at || ''
          setChanHeal(payload)
          if (!chanHealIntervalDirtyRef.current && payload?.interval_sec) {
            setChanHealInterval(String(payload.interval_sec))
          }
          chanHealLastAttemptRef.current = nextAttemptAt
          if (nextAttemptAt && nextAttemptAt !== prevAttemptAt) {
            refreshChannels()
          }
          setChanHealStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setChanHealStatus(err?.message || t('lightningOps.chanHealStatusUnavailable'))
        })
    }
    const fetchHtlcManager = () => {
      getLnHtlcManager()
        .then((data) => {
          if (!mounted) return
          const payload = data as HtlcManagerStatus
          setHtlcManager(payload)
          const intervalMinutes = payload?.interval_minutes ?? ((payload?.interval_hours ?? 0) * 60)
          if (!htlcManagerFormDirtyRef.current && intervalMinutes > 0) {
            setHtlcManagerIntervalMinutes(String(intervalMinutes))
          }
          if (!htlcManagerFormDirtyRef.current && payload?.min_htlc_sat) {
            setHtlcManagerMinSat(String(payload.min_htlc_sat))
          }
          if (!htlcManagerFormDirtyRef.current) {
            setHtlcManagerMaxPct(String(payload?.max_local_pct ?? 0))
          }
          setHtlcManagerStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setHtlcManagerStatus(err?.message || t('lightningOps.htlcManagerStatusUnavailable'))
        })
      getLnHtlcManagerLogs()
        .then((data: any) => {
          if (!mounted) return
          const entries = data?.entries
          setHtlcManagerLogs(Array.isArray(entries) ? entries : [])
        })
        .catch(() => {
          if (!mounted) return
          setHtlcManagerLogs([])
        })
      getLnHtlcManagerFailed()
        .then((data: any) => {
          if (!mounted) return
          const entries = data?.entries
          setHtlcManagerFailed(Array.isArray(entries) ? entries : [])
        })
        .catch(() => {
          if (!mounted) return
          setHtlcManagerFailed([])
        })
    }
    const fetchFailedPaymentsCleaner = () => {
      getLnFailedPaymentsCleaner()
        .then((data) => {
          if (!mounted) return
          const payload = data as FailedPaymentsCleanerStatus
          setFailedPaymentsCleaner(payload)
          if (!failedPaymentsCleanerIntervalDirtyRef.current) {
            setFailedPaymentsCleanerIntervalHours(String(payload?.interval_hours ?? 24))
          }
          setFailedPaymentsCleanerStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setFailedPaymentsCleanerStatus(err?.message || t('lightningOps.failedPaymentsCleanerStatusUnavailable'))
        })
    }
    const fetchTorPeerChecker = () => {
      getLnTorPeerChecker()
        .then((data) => {
          if (!mounted) return
          const payload = data as TorPeerCheckerStatus
          setTorPeerChecker(payload)
          if (!torPeerCheckerIntervalDirtyRef.current) {
            setTorPeerCheckerIntervalHours(String(payload?.interval_hours ?? 2))
          }
          setTorPeerCheckerStatus('')
        })
        .catch((err: any) => {
          if (!mounted) return
          setTorPeerCheckerStatus(err?.message || t('lightningOps.torPeerStatusUnavailable'))
        })
      getLnTorPeerCheckerLogs()
        .then((data: any) => {
          if (!mounted) return
          const entries = data?.entries
          setTorPeerCheckerLogs(Array.isArray(entries) ? entries : [])
        })
        .catch(() => {
          if (!mounted) return
          setTorPeerCheckerLogs([])
        })
    }
    const fetchBalancedOpen = () => {
      getBalancedOpenStatus()
        .then((statusPayload: any) => {
          if (!mounted) return
          const status = statusPayload as BalancedOpenStatusPayload
          setBalancedOpenInfo(status)
          if (!status?.enabled || !status?.available) {
            setBalancedOpenSessions([])
            return
          }
          getBalancedOpenSessions({ limit: 40 })
            .then((sessionsPayload: any) => {
              if (!mounted) return
              setBalancedOpenSessions(Array.isArray(sessionsPayload?.items) ? sessionsPayload.items : [])
            })
            .catch(() => {
              if (!mounted) return
              setBalancedOpenSessions([])
            })
        })
        .catch(() => {
          if (!mounted) return
          setBalancedOpenInfo({ enabled: true, available: false })
          setBalancedOpenSessions([])
        })
    }
    const timer = window.setInterval(() => {
      fetchWatchtowers()
      fetchAmboss()
      fetchChanHeal()
      fetchHtlcManager()
      fetchFailedPaymentsCleaner()
      fetchTorPeerChecker()
      fetchBalancedOpen()
    }, 30000)
    const channelsTimer = window.setInterval(() => {
      refreshChannels()
      refreshClosedChannels()
      pollChannelRankings()
    }, 10 * 60 * 1000)
    return () => {
      mounted = false
      window.clearInterval(timer)
      window.clearInterval(channelsTimer)
    }
  }, [])

  useEffect(() => {
    let mounted = true
    getMempoolFees()
      .then((res: any) => {
        if (!mounted) return
        const fastest = Number(res?.fastestFee || 0)
        const halfHour = Number(res?.halfHourFee || 0)
        const hour = Number(res?.hourFee || 0)
        const economy = Number(res?.economyFee || 0)
        const minimum = Number(res?.minimumFee || 0)
        setOpenFeeHint({ fastest, halfHour, hour, economy, minimum })
        setOpenFeeRate((prev) => (prev ? prev : fastest > 0 ? String(fastest) : prev))
        setCloseFeeHint({ fastest, halfHour, hour, economy, minimum })
        setBatchFeeRate((prev) => (prev ? prev : fastest > 0 ? String(fastest) : prev))
        setBalancedFeeRate((prev) => (prev ? prev : fastest > 0 ? String(fastest) : prev))
        setOpenFeeStatus('')
        setCloseFeeStatus('')
        setBatchFeeStatus('')
        setBalancedFeeStatus('')
      })
      .catch(() => {
        if (!mounted) return
        setOpenFeeStatus(t('lightningOps.feeSuggestionsUnavailable'))
        setCloseFeeStatus(t('lightningOps.feeSuggestionsUnavailable'))
        setBatchFeeStatus(t('lightningOps.feeSuggestionsUnavailable'))
        setBalancedFeeStatus(t('lightningOps.feeSuggestionsUnavailable'))
      })
    return () => {
      mounted = false
    }
  }, [])

  useEffect(() => {
    const localFunding = Math.trunc(Number(openAmount || 0))
    const pushRaw = openPushSat.trim()
    const pushSat = pushRaw === '' ? 0 : Math.trunc(Number(openPushSat || 0))
    const manualRate = Math.max(0, Math.trunc(Number(openFeeRate || 0)))
    const autoRate = Math.max(0, Math.trunc(Number(openFeeHint?.fastest || openFeeHint?.hour || 0)))
    const previewRate = openFeeMode === 'manual' ? manualRate : autoRate

    if (!localFunding) {
      setOpenPreview(null)
      setOpenPreviewLoading(false)
      setOpenPreviewStatus('')
      return
    }
    if (!Number.isFinite(pushSat) || pushSat < 0) {
      setOpenPreview(null)
      setOpenPreviewLoading(false)
      setOpenPreviewStatus(t('lightningOps.pushAmountInvalid'))
      return
    }
    if (pushSat > localFunding) {
      setOpenPreview(null)
      setOpenPreviewLoading(false)
      setOpenPreviewStatus(t('lightningOps.pushAmountExceedsFunding'))
      return
    }
    if (openFeeMode === 'manual' && previewRate <= 0) {
      setOpenPreview(null)
      setOpenPreviewLoading(false)
      setOpenPreviewStatus(t('lightningOps.openPreviewFeeRequired'))
      return
    }
    if (previewRate <= 0) {
      setOpenPreview(null)
      setOpenPreviewLoading(false)
      setOpenPreviewStatus(openFeeStatus || t('lightningOps.openPreviewUnavailable'))
      return
    }

    let mounted = true
    const timer = window.setTimeout(() => {
      setOpenPreviewLoading(true)
      previewOpenChannel({
        local_funding_sat: localFunding,
        push_sat: pushSat > 0 ? pushSat : undefined,
        sat_per_vbyte: previewRate,
      })
        .then((res: any) => {
          if (!mounted) return
          const next = res as OpenChannelPreview
          setOpenPreview(next)
          setOpenPreviewStatus(openPreviewMessage(next))
        })
        .catch((err: any) => {
          if (!mounted) return
          setOpenPreview(null)
          setOpenPreviewStatus(err?.message || t('lightningOps.openPreviewUnavailable'))
        })
        .finally(() => {
          if (!mounted) return
          setOpenPreviewLoading(false)
        })
    }, 250)

    return () => {
      mounted = false
      window.clearTimeout(timer)
    }
  }, [openAmount, openPushSat, openFeeHint, openFeeMode, openFeeRate, openFeeStatus, t])

  useEffect(() => {
    const manualRate = Math.max(0, Math.trunc(Number(batchFeeRate || 0)))
    const autoRate = Math.max(0, Math.trunc(Number(openFeeHint?.fastest || openFeeHint?.hour || 0)))
    const previewRate = batchFeeMode === 'manual' ? manualRate : autoRate

    if (!batchItems.length) {
      setBatchPreview(null)
      setBatchPreviewLoading(false)
      setBatchPreviewStatus('')
      return
    }
    if (batchFeeMode === 'manual' && previewRate <= 0) {
      setBatchPreview(null)
      setBatchPreviewLoading(false)
      setBatchPreviewStatus(t('lightningOps.batchOpenPreviewFeeRequired'))
      return
    }
    if (previewRate <= 0) {
      setBatchPreview(null)
      setBatchPreviewLoading(false)
      setBatchPreviewStatus(batchFeeStatus || t('lightningOps.batchOpenPreviewUnavailable'))
      return
    }

    let mounted = true
    const timer = window.setTimeout(() => {
      setBatchPreviewLoading(true)
      previewBatchOpenChannels({
        channels: batchItems.map((item) => ({
          local_funding_sat: item.local_funding_sat,
        })),
        sat_per_vbyte: previewRate,
      })
        .then((res: any) => {
          if (!mounted) return
          const next = res as BatchOpenPreview
          setBatchPreview(next)
          setBatchPreviewStatus(batchPreviewMessage(next))
        })
        .catch((err: any) => {
          if (!mounted) return
          setBatchPreview(null)
          setBatchPreviewStatus(err?.message || t('lightningOps.batchOpenPreviewUnavailable'))
        })
        .finally(() => {
          if (!mounted) return
          setBatchPreviewLoading(false)
        })
    }, 250)

    return () => {
      mounted = false
      window.clearTimeout(timer)
    }
  }, [batchFeeMode, batchFeeRate, batchFeeStatus, batchItems, openFeeHint, t])

  useEffect(() => {
    if (feeScopeAll) {
      setFeeLoadStatus('')
      setFeeLoading(false)
      return
    }
    if (!feeChannelPoint) {
      setFeeLoadStatus('')
      setFeeLoading(false)
      return
    }

    let mounted = true
    setFeeLoading(true)
    setFeeLoadStatus(t('lightningOps.loadingFees'))
    getLnChannelFees(feeChannelPoint)
      .then((res) => {
        if (!mounted) return
        setBaseFeeMsat(String(res?.base_fee_msat ?? ''))
        setFeeRatePpm(String(res?.fee_rate_ppm ?? ''))
        setTimeLockDelta(String(res?.time_lock_delta ?? ''))
        setInboundBaseMsat(String(res?.inbound_base_msat ?? ''))
        setInboundFeeRatePpm(String(res?.inbound_fee_rate_ppm ?? ''))
        const inboundBase = Number(res?.inbound_base_msat || 0)
        const inboundRate = Number(res?.inbound_fee_rate_ppm || 0)
        setInboundEnabled(inboundBase !== 0 || inboundRate !== 0)
        setFeeLoadStatus(t('lightningOps.feesLoaded'))
      })
      .catch((err: any) => {
        if (!mounted) return
        setFeeLoadStatus(err?.message || t('lightningOps.loadFeesFailed'))
      })
      .finally(() => {
        if (!mounted) return
        setFeeLoading(false)
      })

    return () => {
      mounted = false
    }
  }, [feeChannelPoint, feeScopeAll])
  useEffect(() => {
    pendingScrollChannelRef.current = readHashChannelPoint(LIGHTNING_OPS_ROUTE_KEY)
    pendingScrollPeerRef.current = readHashPeerPubKey(LIGHTNING_OPS_ROUTE_KEY)
    pendingScrollSectionRef.current = readHashSection(LIGHTNING_OPS_ROUTE_KEY)
    return () => {
      if (focusClearTimerRef.current !== null) {
        window.clearTimeout(focusClearTimerRef.current)
      }
      if (peerFocusClearTimerRef.current !== null) {
        window.clearTimeout(peerFocusClearTimerRef.current)
      }
    }
  }, [])

  useEffect(() => {
    if (typeof window === 'undefined') return
    const targetSection = pendingScrollSectionRef.current
    if (!targetSection) return
    if (targetSection === 'close_recovery') {
      setChannelsSubview('close_recovery')
      window.setTimeout(() => {
        const target = document.getElementById(CLOSE_RECOVERY_SECTION_ID)
        if (!target) return
        target.scrollIntoView({ behavior: 'smooth', block: 'start' })
        pendingScrollSectionRef.current = ''
      }, 50)
      return
    }
    if (targetSection === 'autofee') {
      window.setTimeout(() => {
        const target = document.getElementById(AUTOFEE_SECTION_ID)
        if (!target) return
        target.scrollIntoView({ behavior: 'smooth', block: 'start' })
        pendingScrollSectionRef.current = ''
      }, 50)
      return
    }
    if (targetSection === 'htlc_manager') {
      setLightningToolsOpen(true)
      window.setTimeout(() => {
        const target = document.getElementById(HTLC_MANAGER_SECTION_ID)
        if (!target) return
        target.scrollIntoView({ behavior: 'smooth', block: 'start' })
        pendingScrollSectionRef.current = ''
      }, 80)
    }
  }, [channelsSubview, autofeeOpen, lightningToolsOpen])

  const baseFilteredChannels = useMemo(() => {
    let list = channels
    if (filter === 'active') {
      list = list.filter((ch) => ch.active)
    }
    if (filter === 'inactive') {
      list = list.filter((ch) => !ch.active)
    }
    if (!showPrivate) {
      list = list.filter((ch) => !ch.private)
    }
    if (search.trim()) {
      const query = search.trim().toLowerCase()
      list = list.filter((ch) => {
        return (
          ch.peer_alias?.toLowerCase().includes(query) ||
          ch.remote_pubkey?.toLowerCase().includes(query) ||
          ch.channel_point?.toLowerCase().includes(query)
        )
      })
    }
    const minCap = Number(minCapacity || 0)
    if (minCap > 0) {
      list = list.filter((ch) => ch.capacity_sat >= minCap)
    }
    return list
  }, [channels, filter, minCapacity, search, showPrivate])

  const profitCounts = useMemo(() => {
    const counts = { profitable: 0, neutral: 0, deficit: 0 }
    baseFilteredChannels.forEach((ch) => {
      const value = ch.profit_fee_7d_sat
      if (typeof value !== 'number' || Number.isNaN(value)) return
      if (value > 0) counts.profitable += 1
      else if (value < 0) counts.deficit += 1
      else counts.neutral += 1
    })
    return counts
  }, [baseFilteredChannels])

  const fcRiskCount = useMemo(() => channels.filter(isFCRiskChannel).length, [channels])

  const filteredChannels = useMemo(() => {
    let list = baseFilteredChannels
    if (profitFilter === 'profitable') {
      list = list.filter((ch) => typeof ch.profit_fee_7d_sat === 'number' && ch.profit_fee_7d_sat > 0)
    } else if (profitFilter === 'neutral') {
      list = list.filter((ch) => typeof ch.profit_fee_7d_sat === 'number' && ch.profit_fee_7d_sat === 0)
    } else if (profitFilter === 'deficit') {
      list = list.filter((ch) => typeof ch.profit_fee_7d_sat === 'number' && ch.profit_fee_7d_sat < 0)
    }
    if (rankingFilter !== 'all') {
      list = list.filter((ch) => String(channelRankingMap[ch.channel_point]?.state || 'monitor').trim() === rankingFilter)
    }
    if (movementFilter === 'low') {
      list = list.filter((ch) => isLowMovementChannel(ch))
    } else if (movementFilter === 'active') {
      list = list.filter((ch) => hasChannelMovementActivity(ch.movement_7d) && !isLowMovementChannel(ch))
    } else if (movementFilter === 'none') {
      list = list.filter((ch) => !hasChannelMovementActivity(ch.movement_7d))
    }
    if (fcRiskOnly) {
      list = list.filter((ch) => isFCRiskChannel(ch))
    }
    const sorted = [...list]
    const direction = sortDir === 'desc' ? -1 : 1
    sorted.sort((a, b) => {
      if (sortBy === 'alias') {
        const aVal = (a.peer_alias || a.remote_pubkey || '').toLowerCase()
        const bVal = (b.peer_alias || b.remote_pubkey || '').toLowerCase()
        return aVal.localeCompare(bVal) * direction
      }
      const aVal = sortBy === 'capacity'
        ? a.capacity_sat
        : sortBy === 'local'
          ? a.local_balance_sat
          : a.remote_balance_sat
      const bVal = sortBy === 'capacity'
        ? b.capacity_sat
        : sortBy === 'local'
          ? b.local_balance_sat
          : b.remote_balance_sat
      return (aVal - bVal) * direction
    })
    return sorted
  }, [baseFilteredChannels, channelRankingMap, fcRiskOnly, movementFilter, profitFilter, rankingFilter, sortBy, sortDir])

  useEffect(() => {
    if (typeof window === 'undefined') return undefined
    const syncViewportSize = () => {
      setViewportSize({ width: window.innerWidth, height: window.innerHeight })
    }
    syncViewportSize()
    window.addEventListener('resize', syncViewportSize)
    return () => window.removeEventListener('resize', syncViewportSize)
  }, [])

  useEffect(() => {
    if (typeof window === 'undefined') return
    const targetChannelPoint = pendingScrollChannelRef.current
    if (!targetChannelPoint) return
    const targetExists = filteredChannels.some((channel) => channel.channel_point === targetChannelPoint)
    if (!targetExists) return
    const targetElement = document.getElementById(channelCardID(targetChannelPoint))
    if (!targetElement) return
    targetElement.scrollIntoView({ behavior: 'smooth', block: 'center' })
    setFocusedChannelPoint(targetChannelPoint)
    pendingScrollChannelRef.current = ''
    window.history.replaceState(null, '', `#${LIGHTNING_OPS_ROUTE_KEY}`)
    if (focusClearTimerRef.current !== null) {
      window.clearTimeout(focusClearTimerRef.current)
    }
    focusClearTimerRef.current = window.setTimeout(() => {
      setFocusedChannelPoint((current) => (current === targetChannelPoint ? '' : current))
      focusClearTimerRef.current = null
    }, 3200)
  }, [filteredChannels])

  useEffect(() => {
    if (typeof window === 'undefined') return
    const targetPeerPubKey = pendingScrollPeerRef.current
    if (!targetPeerPubKey) return
    const normalizedTarget = targetPeerPubKey.toLowerCase()
    const targetPeer = peers.find((peer) => String(peer.pub_key || '').trim().toLowerCase() === normalizedTarget)
    if (!targetPeer) return
    if (!peersOpen) {
      setPeersOpen(true)
      return
    }
    const targetElement = document.getElementById(peerCardID(targetPeer.pub_key))
    if (!targetElement) return
    targetElement.scrollIntoView({ behavior: 'smooth', block: 'center' })
    setFocusedPeerPubKey(targetPeer.pub_key)
    pendingScrollPeerRef.current = ''
    window.history.replaceState(null, '', `#${LIGHTNING_OPS_ROUTE_KEY}`)
    if (peerFocusClearTimerRef.current !== null) {
      window.clearTimeout(peerFocusClearTimerRef.current)
    }
    peerFocusClearTimerRef.current = window.setTimeout(() => {
      setFocusedPeerPubKey((current) => (current === targetPeer.pub_key ? '' : current))
      peerFocusClearTimerRef.current = null
    }, 3200)
  }, [peers, peersOpen])

  const sortClosedChannelsList = (items: ClosedChannel[]) => (
    [...items].sort((a, b) => {
      const heightDelta = Number(b.close_height || 0) - Number(a.close_height || 0)
      if (heightDelta !== 0) return heightDelta
      return Number(b.chan_id || 0) - Number(a.chan_id || 0)
    })
  )
  const pendingOpen = useMemo(() => pendingChannels.filter((ch) => ch.status === 'opening'), [pendingChannels])
  const pendingClose = useMemo(() => pendingChannels.filter((ch) => ch.status !== 'opening'), [pendingChannels])
  const closedChannelsSorted = useMemo(() => sortClosedChannelsList(closedChannels), [closedChannels])
  const closeRecoveryActiveSessions = useMemo(
    () => closeRecoverySessions.filter((item) => item.state !== 'closed_terminal' && item.state !== 'funds_recovered'),
    [closeRecoverySessions]
  )
  const closeRecoveryRecentSessions = useMemo(() => {
    const terminal = closeRecoverySessions.filter((item) => item.state === 'closed_terminal' || item.state === 'funds_recovered')
    if (terminal.length === 0) return []

    const normalize = (value?: string) => String(value || '').trim().toLowerCase()
    const byChanID = new Map<number, CloseRecoverySession>()
    const byChannelPoint = new Map<string, CloseRecoverySession>()
    const byCloseTxid = new Map<string, CloseRecoverySession>()

    for (const item of terminal) {
      const chanID = Math.trunc(Number(item.channel_id || 0))
      if (chanID > 0 && !byChanID.has(chanID)) {
        byChanID.set(chanID, item)
      }
      const point = normalize(item.channel_point)
      if (point && !byChannelPoint.has(point)) {
        byChannelPoint.set(point, item)
      }
      const closeTxid = normalize(item.close_txid)
      if (closeTxid && !byCloseTxid.has(closeTxid)) {
        byCloseTxid.set(closeTxid, item)
      }
    }

    const selected: CloseRecoverySession[] = []
    const seenIDs = new Set<number>()
    for (const item of closedChannelsSorted) {
      const chanID = Math.trunc(Number(item.chan_id || 0))
      const point = normalize(item.channel_point)
      const closeTxid = normalize(item.closing_tx_hash)
      const matched = (chanID > 0 ? byChanID.get(chanID) : undefined)
        || (point ? byChannelPoint.get(point) : undefined)
        || (closeTxid ? byCloseTxid.get(closeTxid) : undefined)
      if (!matched || seenIDs.has(matched.id)) continue
      seenIDs.add(matched.id)
      selected.push(matched)
      if (selected.length >= 3) break
    }

    return selected
  }, [closeRecoverySessions, closedChannelsSorted])
  const closeRecoveryGroups = useMemo(() => {
    const groups = [
      { key: 'coop', title: t('lightningOps.closeRecoveryGroupCoop'), states: ['coop_requested'] },
      { key: 'htlc', title: t('lightningOps.closeRecoveryGroupHtlc'), states: ['coop_blocked_by_htlcs'] },
      { key: 'waiting', title: t('lightningOps.closeRecoveryGroupWaiting'), states: ['waiting_close_no_txid', 'closing_tx_seen_unconfirmed'] },
      { key: 'force', title: t('lightningOps.closeRecoveryGroupForce'), states: ['force_close_requested', 'force_close_active', 'outputs_timelocked'] },
      { key: 'sweep', title: t('lightningOps.closeRecoveryGroupSweep'), states: ['sweep_pending', 'sweep_stuck'] },
      { key: 'recent', title: t('lightningOps.closeRecoveryGroupRecent'), states: ['funds_recovered', 'closed_terminal'] }
    ]
    return groups
      .map((group) => ({
        ...group,
        items: (() => {
          if (group.key === 'recent') return closeRecoveryRecentSessions
          return closeRecoverySessions.filter((item) => group.states.includes(item.state))
        })()
      }))
      .filter((group) => group.items.length > 0)
  }, [closeRecoveryRecentSessions, closeRecoverySessions, t])
  const filteredClosedChannels = useMemo(() => {
    let list = closedChannels
    if (closedChannelFilter !== 'all') {
      list = list.filter((item) => closedChannelTypeCategory(item) === closedChannelFilter)
    }
    if (closedChannelSearch.trim()) {
      const query = closedChannelSearch.trim().toLowerCase()
      list = list.filter((item) => {
        const sweepMatch = Array.isArray(item.resolutions) && item.resolutions.some((resolution) =>
          String(resolution?.sweep_txid || '').toLowerCase().includes(query))
        return (
          String(item.peer_alias || '').toLowerCase().includes(query) ||
          String(item.remote_pubkey || '').toLowerCase().includes(query) ||
          String(item.channel_point || '').toLowerCase().includes(query) ||
          String(item.closing_tx_hash || '').toLowerCase().includes(query) ||
          sweepMatch
        )
      })
    }
    return sortClosedChannelsList(list)
  }, [closedChannelFilter, closedChannelSearch, closedChannels])
  const balancedOpenSelectedSession = useMemo(
    () => balancedOpenSessions.find((item) => item.session_id === balancedOpenDetailsSessionID) || null,
    [balancedOpenSessions, balancedOpenDetailsSessionID]
  )
  const balancedOpenSelectedEvents = useMemo(
    () => (balancedOpenDetailsSessionID ? (balancedOpenEventsBySession[balancedOpenDetailsSessionID] || []) : []),
    [balancedOpenDetailsSessionID, balancedOpenEventsBySession]
  )
  const balancedOpenSelectedEventsError = balancedOpenDetailsSessionID ? (balancedOpenEventsErrorBySession[balancedOpenDetailsSessionID] || '') : ''
  const balancedOpenSelectedEventsLoading = Boolean(balancedOpenDetailsSessionID && balancedOpenEventsLoadingSessionID === balancedOpenDetailsSessionID)
  const scbRecoveryAvailable = channels.length === 0 && pendingChannels.length === 0
  const scbRestorePhraseValid = scbRestorePhrase.trim().toUpperCase() === SCB_RECOVERY_CONFIRM_PHRASE
  const scbRestoreCanSubmit = scbRecoveryAvailable
    && !scbRestoreBusy
    && scbRestoreConfirm
    && scbRestorePhraseValid
    && scbRestoreData.trim().length > 0

  const pendingStatusLabel = (status: string) => {
    switch (status) {
      case 'opening':
        return t('lightningOps.statusOpening')
      case 'closing':
        return t('lightningOps.statusClosing')
      case 'force_closing':
        return t('lightningOps.statusForceClosing')
      case 'waiting_close':
        return t('lightningOps.statusWaitingClose')
      default:
        return status
    }
  }

  const isPendingOpenStuck = (ch: PendingChannel) => {
    if ((ch.status || '').trim().toLowerCase() !== 'opening') return false
    if (typeof ch.opening_duration_sec !== 'number' || ch.opening_duration_sec < PENDING_OPEN_STUCK_THRESHOLD_SEC) return false
    if (typeof ch.confirmations_until_active === 'number' && ch.confirmations_until_active <= 0) return false
    return channelPointTxid(ch.channel_point).length > 0
  }

  const resolvePendingOpenBumpPreview = (ch: PendingChannel, preset: 'economic' | 'normal' | 'urgent') => {
    const current = Math.max(0, Math.trunc(Number(ch.funding_fee_rate_sat_vb || 0)))
    const economicTarget = Math.max(1, Math.trunc(Number(openFeeHint?.economy || openFeeHint?.minimum || 1)))
    const normalTarget = Math.max(economicTarget + 1, Math.trunc(Number(openFeeHint?.hour || openFeeHint?.halfHour || 0)) || (economicTarget + 1))
    const urgentTarget = Math.max(normalTarget + 3, Math.trunc(Number(openFeeHint?.fastest || 0)) || (normalTarget + 3))

    let satPerVbyte = 0
    let immediate = false
    if (preset === 'economic') {
      satPerVbyte = Math.max(current + 1, economicTarget)
    } else if (preset === 'urgent') {
      satPerVbyte = Math.max(current + 5, urgentTarget)
      immediate = true
    } else {
      satPerVbyte = Math.max(current + 2, normalTarget)
    }

    return {
      satPerVbyte,
      immediate,
      estimatedFeeSat: satPerVbyte * PENDING_OPEN_BUMP_REFERENCE_VBYTES,
      referenceVbytes: PENDING_OPEN_BUMP_REFERENCE_VBYTES,
    }
  }

  const pendingOpenBumpPresetLabel = (preset: 'economic' | 'normal' | 'urgent') => {
    if (preset === 'economic') return t('lightningOps.pendingOpenBumpEconomic')
    if (preset === 'urgent') return t('lightningOps.pendingOpenBumpUrgent')
    return t('lightningOps.pendingOpenBumpNormal')
  }

  const pendingOpenBumpReasonLabel = (reason?: string) => {
    switch ((reason || '').trim()) {
      case 'no_wallet_output':
        return t('lightningOps.pendingOpenBumpUnavailableNoWalletOutput')
      case 'wallet_output_unavailable':
        return t('lightningOps.pendingOpenBumpUnavailableWalletOutput')
      case 'funding_tx_unavailable':
        return t('lightningOps.pendingOpenBumpUnavailableFundingTx')
      case 'channel_point_invalid':
      case 'diagnostic_unavailable':
      default:
        return t('lightningOps.pendingOpenBumpCheckUnavailable')
    }
  }

  const waitingCloseRecoveryResultLabel = (result?: string) => {
    const normalized = String(result || '').trim()
    switch (normalized) {
      case 'no_raw_tx_available':
        return t('lightningOps.waitingCloseResultNoRawTx')
      case 'recover_failed':
        return t('lightningOps.waitingCloseResultRecoverFailed')
      case 'rebroadcast_ok':
        return t('lightningOps.waitingCloseResultRebroadcastOk')
      case 'closing_txid_detected':
        return t('lightningOps.waitingCloseResultTxidDetected')
      case 'recovery_submitted_no_txid':
      case 'rebroadcast_submitted_no_txid':
        return t('lightningOps.waitingCloseResultRebroadcastNoTxid')
      default:
        return normalized || t('common.na')
    }
  }

  const formatWaitingCloseRecoveryTime = (value?: string) => {
    if (!value) return t('common.na')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return t('common.unknownTime')
    return parsed.toLocaleString()
  }

  const balancedOpenStateLabel = (state: string) => {
    switch (state) {
      case 'session_created':
        return t('lightningOps.balancedOpenStateSessionCreated')
      case 'proposal_sent':
        return t('lightningOps.balancedOpenStateProposalSent')
      case 'proposal_received':
        return t('lightningOps.balancedOpenStateProposalReceived')
      case 'accepted':
        return t('lightningOps.balancedOpenStateAccepted')
      case 'funding_tx_half_signed':
        return t('lightningOps.balancedOpenStateFundingHalf')
      case 'funding_tx_fully_signed':
        return t('lightningOps.balancedOpenStateFundingFull')
      case 'channel_proposed_to_lnd':
        return t('lightningOps.balancedOpenStateProposed')
      case 'funding_broadcasted':
        return t('lightningOps.balancedOpenStateBroadcasted')
      case 'pending_open_detected':
        return t('lightningOps.balancedOpenStatePending')
      case 'active':
        return t('lightningOps.balancedOpenStateActive')
      case 'failed':
        return t('lightningOps.balancedOpenStateFailed')
      case 'canceled':
        return t('lightningOps.balancedOpenStateCanceled')
      case 'recovery_required':
        return t('lightningOps.balancedOpenStateRecoveryRequired')
      case 'recovered':
        return t('lightningOps.balancedOpenStateRecovered')
      default:
        return state || t('common.unknown')
    }
  }

  const isBalancedOpenTerminalState = (state: string) => {
    return state === 'active' || state === 'failed' || state === 'canceled' || state === 'recovered'
  }

  const isBalancedOpenProposeEligible = (state: string) => {
    return state === 'session_created'
  }

  const canAcceptBalancedSession = (session: BalancedOpenSession) => {
    return session.role === 'accepter' && (session.state === 'proposal_received' || session.state === 'funding_tx_half_signed')
  }

  const canExecuteBalancedSession = (session: BalancedOpenSession) => {
    return session.role === 'initiator' && (session.state === 'accepted' || session.state === 'recovery_required')
  }

  const canProposeBalancedSession = (session: BalancedOpenSession) => {
    return session.role === 'initiator' && isBalancedOpenProposeEligible(session.state)
  }

  const balancedOpenHasSignedFundingTxHex = (session: BalancedOpenSession) => {
    const txHex = balancedOpenMetadataString(session, 'funding_tx_hex').toLowerCase()
    if (!txHex) return false
    if (txHex.length < 200 || txHex.length % 2 !== 0) return false
    return /^[0-9a-f]+$/.test(txHex)
  }

  const canRetryBalancedBroadcastSession = (session: BalancedOpenSession) => {
    if (session.role !== 'initiator') return false
    if (balancedOpenSessionExecutionMode(session) !== 'dual_funded_v1') return false
    if (
      session.state !== 'channel_proposed_to_lnd' &&
      session.state !== 'pending_open_detected' &&
      session.state !== 'recovery_required'
    ) {
      return false
    }
    if (!balancedOpenSessionChannelPoint(session)) return false
    if (!balancedOpenHasSignedFundingTxHex(session)) return false
    return true
  }

  const balancedOpenSessionExecutionMode = (session: BalancedOpenSession) => {
    const raw = session?.metadata?.execution_mode
    if (typeof raw !== 'string') return ''
    return raw.trim().toLowerCase()
  }

  const balancedOpenMetadataString = (session: BalancedOpenSession, key: string) => {
    const raw = session?.metadata?.[key]
    if (typeof raw !== 'string') return ''
    return raw.trim()
  }

  const balancedOpenMetadataNumber = (session: BalancedOpenSession, key: string) => {
    const raw = session?.metadata?.[key]
    if (typeof raw === 'number' && Number.isFinite(raw)) return raw
    if (typeof raw === 'string') {
      const parsed = Number(raw)
      if (Number.isFinite(parsed)) return parsed
    }
    return NaN
  }

  const balancedOpenMetadataBoolean = (session: BalancedOpenSession, key: string): boolean | null => {
    const raw = session?.metadata?.[key]
    if (typeof raw === 'boolean') return raw
    if (typeof raw === 'string') {
      const normalized = raw.trim().toLowerCase()
      if (normalized === 'true') return true
      if (normalized === 'false') return false
    }
    return null
  }

  const balancedOpenCanonicalPoint = (txid: string, vout: number) => {
    const id = (txid || '').trim().toLowerCase()
    if (!/^[0-9a-f]{64}$/.test(id)) return ''
    if (!Number.isInteger(vout) || vout < 0) return ''
    return `${id}:${vout}`
  }

  const balancedOpenHasOrphanFundingCandidate = (session: BalancedOpenSession) => {
    if (balancedOpenSessionExecutionMode(session) !== 'dual_funded_v1') return false

    const fundingTxid = balancedOpenMetadataString(session, 'funding_tx_id')
    const fundingVout = balancedOpenMetadataNumber(session, 'funding_tx_vout')
    const expectedPoint = balancedOpenCanonicalPoint(fundingTxid, fundingVout)
    if (!expectedPoint) return false

    const channelPoint = balancedOpenSessionChannelPoint(session)
    const parts = channelPoint.split(':')
    if (parts.length !== 2) return false
    const actualPoint = balancedOpenCanonicalPoint(parts[0], Number(parts[1]))
    if (!actualPoint) return false

    const orphanRecoveryTxid = balancedOpenMetadataString(session, 'orphan_recovery_txid')
    if (orphanRecoveryTxid) return false

    return expectedPoint !== actualPoint
  }

  const balancedOpenRecoveryKeysForRole = (role: string) => {
    if (role === 'initiator') {
      return { txidKey: 'initiator_recovery_txid', unavailableKey: 'initiator_recovery_unavailable_outpoint' }
    }
    if (role === 'accepter') {
      return { txidKey: 'accepter_recovery_txid', unavailableKey: 'accepter_recovery_unavailable_outpoint' }
    }
    return { txidKey: '', unavailableKey: '' }
  }

  const canRecoverBalancedSession = (session: BalancedOpenSession) => {
    if (balancedOpenSessionExecutionMode(session) !== 'dual_funded_v1') return false
    const orphanRecoveryTxid = balancedOpenMetadataString(session, 'orphan_recovery_txid')
    const orphanLocalSweepTxid = balancedOpenMetadataString(session, 'orphan_local_sweep_txid')
    if (orphanRecoveryTxid && !orphanLocalSweepTxid) return true
    if (balancedOpenHasOrphanFundingCandidate(session)) return true
    if (session.state === 'recovery_required' || session.state === 'canceled') return true
    if (session.state !== 'recovered') return false

    const keys = balancedOpenRecoveryKeysForRole(session.role)
    if (!keys.txidKey || !keys.unavailableKey) return false
    const hasRecoveryTxid = balancedOpenMetadataString(session, keys.txidKey) !== ''
    const unavailableOutpoint = balancedOpenMetadataString(session, keys.unavailableKey)
    return unavailableOutpoint !== '' && !hasRecoveryTxid
  }

  const balancedOpenSessionChannelPoint = (session: BalancedOpenSession) => {
    const raw = session?.metadata?.channel_point
    if (typeof raw !== 'string') return ''
    return raw.trim()
  }

  const findPendingOpenForBalancedSession = (session: BalancedOpenSession) => {
    const pointHint = balancedOpenSessionChannelPoint(session).toLowerCase()
    const peer = (session.peer_pubkey || '').trim().toLowerCase()
    const capacity = Number(session.capacity_sat || 0)
    if (!pointHint && (!peer || capacity <= 0)) return null

    for (const item of pendingChannels) {
      if ((item.status || '').trim().toLowerCase() !== 'opening') continue
      const point = (item.channel_point || '').trim().toLowerCase()
      if (pointHint && point === pointHint) return item
      if (!pointHint) {
        const remote = (item.remote_pubkey || '').trim().toLowerCase()
        if (remote === peer && Number(item.capacity_sat || 0) === capacity) return item
      }
    }
    return null
  }

  const canCancelBalancedSession = (session: BalancedOpenSession) => {
    if (isBalancedOpenTerminalState(session.state)) return false
    if (session.state === 'pending_open_detected') {
      const pending = findPendingOpenForBalancedSession(session)
      if (pending && Number(pending.confirmation_height || 0) > 0) return false
    }
    return true
  }

  const balancedOpenRoleLabel = (role: string) => {
    if (role === 'initiator') return t('lightningOps.balancedOpenHealthRoleInitiator')
    if (role === 'accepter') return t('lightningOps.balancedOpenHealthRoleAccepter')
    return role || t('common.unknown')
  }

  const balancedOpenMempoolHealth = (session: BalancedOpenSession): { tone: 'ok' | 'warn' | 'muted'; label: string } => {
    if (session.state === 'active') {
      return { tone: 'ok', label: t('lightningOps.balancedOpenHealthMempoolActive') }
    }

    const checked = balancedOpenMetadataBoolean(session, 'pending_stuck_external_mempool_checked')
    const seen = balancedOpenMetadataBoolean(session, 'pending_stuck_external_mempool_seen')
    if (checked === null) {
      return { tone: 'muted', label: t('lightningOps.balancedOpenHealthMempoolUnknown') }
    }
    if (!checked) {
      return { tone: 'muted', label: t('lightningOps.balancedOpenHealthMempoolUnknown') }
    }
    if (seen) {
      return { tone: 'ok', label: t('lightningOps.balancedOpenHealthMempoolSeen') }
    }
    return { tone: 'warn', label: t('lightningOps.balancedOpenHealthMempoolNotSeen') }
  }

  const balancedOpenLastAutoRetryLabel = (session: BalancedOpenSession) => {
    const retryUnix = balancedOpenMetadataNumber(session, 'pending_stuck_autoretry_unix')
    if (!Number.isFinite(retryUnix) || retryUnix <= 0) {
      return t('lightningOps.balancedOpenHealthAutoRetryNone')
    }
    return formatBalancedDate(new Date(retryUnix * 1000).toISOString())
  }

  const formatBalancedDate = (value?: string) => {
    if (!value) return t('common.na')
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return t('common.na')
    return parsed.toLocaleString()
  }

  const formatBalancedEventType = (eventType: string) => {
    switch ((eventType || '').trim()) {
      case 'session_created':
        return t('lightningOps.balancedOpenEventSessionCreated')
      case 'proposal_sent':
        return t('lightningOps.balancedOpenEventProposalSent')
      case 'proposal_received':
        return t('lightningOps.balancedOpenEventProposalReceived')
      case 'proposal_accepted_local':
      case 'proposal_accepted_remote':
        return t('lightningOps.balancedOpenEventAccepted')
      case 'channel_open_submitted':
        return t('lightningOps.balancedOpenEventOpenSubmitted')
      case 'channel_open_acknowledged':
        return t('lightningOps.balancedOpenEventOpenAcknowledged')
      case 'funding_broadcasted_local':
      case 'funding_broadcasted_reconcile':
        return t('lightningOps.balancedOpenEventFundingBroadcasted')
      case 'funding_broadcast_observed_after_error':
        return t('lightningOps.balancedOpenEventFundingObservedAfterError')
      case 'funding_broadcast_observed_after_retry_error':
        return t('lightningOps.balancedOpenEventFundingObservedAfterRetryError')
      case 'funding_broadcast_retry_failed':
      case 'funding_broadcast_failed_retrying':
        return t('lightningOps.balancedOpenEventFundingBroadcastRetryFailed')
      case 'proposal_preflight_failed':
        return t('lightningOps.balancedOpenEventProposalPreflightFailed')
      case 'accept_preflight_failed':
        return t('lightningOps.balancedOpenEventAcceptPreflightFailed')
      case 'funding_shim_canceled':
        return t('lightningOps.balancedOpenEventFundingShimCanceled')
      case 'funding_shim_cancel_failed':
        return t('lightningOps.balancedOpenEventFundingShimCancelFailed')
      case 'pending_open_stuck_detected':
        return t('lightningOps.balancedOpenEventPendingStuckDetected')
      case 'pending_open_stuck_autoretry_submitted':
        return t('lightningOps.balancedOpenEventPendingStuckAutoRetrySubmitted')
      case 'pending_open_stuck_autoretry_failed':
        return t('lightningOps.balancedOpenEventPendingStuckAutoRetryFailed')
      case 'pending_open_detected':
        return t('lightningOps.balancedOpenEventPendingDetected')
      case 'channel_active_detected':
        return t('lightningOps.balancedOpenEventActiveDetected')
      case 'anchor_reserve_precheck_failed':
        return t('lightningOps.balancedOpenEventAnchorPrecheckFailed')
      case 'transit_recovered':
        return t('lightningOps.balancedOpenEventTransitRecovered')
      case 'transit_outpoint_already_spent':
        return t('lightningOps.balancedOpenEventTransitAlreadySpent')
      case 'transit_outpoint_unavailable':
        return t('lightningOps.balancedOpenEventTransitUnavailable')
      case 'session_canceled':
      case 'session_canceled_remote':
        return t('lightningOps.balancedOpenEventCanceled')
      default:
        return eventType || t('common.unknown')
    }
  }

  const formatBalancedEventDetail = (detail: unknown) => {
    if (!detail || typeof detail !== 'object') return ''
    try {
      return JSON.stringify(detail, null, 2)
    } catch {
      return ''
    }
  }

  const connectedPubkeys = useMemo(() => {
    const set = new Set<string>()
    peers.forEach((peer) => {
      const key = (peer.pub_key || '').trim().toLowerCase()
      if (key) {
        set.add(key)
      }
    })
    return set
  }, [peers])

  const peerAliasMap = useMemo(() => {
    const map = new Map<string, string>()
    peers.forEach((peer) => {
      const key = (peer.pub_key || '').trim().toLowerCase()
      if (key) {
        map.set(key, peer.alias || '')
      }
    })
    return map
  }, [peers])

  const parseBatchPeerAddress = (raw: string): { pubkey: string; host?: string; error?: string } => {
    const value = raw.trim()
    if (!value) {
      return { pubkey: '', error: t('lightningOps.peerAddressRequired') }
    }
    if (!value.includes('@')) {
      return { pubkey: value }
    }
    const parts = value.split('@')
    if (parts.length !== 2 || !parts[0]?.trim() || !parts[1]?.trim()) {
      return { pubkey: '', error: t('lightningOps.batchOpenInvalidPeer') }
    }
    const host = parts[1].trim()
    if (!host.includes(':')) {
      return { pubkey: '', error: t('lightningOps.batchOpenInvalidPeer') }
    }
    return { pubkey: parts[0].trim(), host }
  }

  const parseWatchtowerAddress = (raw: string): { pubkey: string; host: string; error?: string } => {
    const value = raw.trim()
    if (!value.includes('@')) {
      return { pubkey: '', host: '', error: t('lightningOps.watchtowerInvalidAddress') }
    }
    const parts = value.split('@')
    if (parts.length !== 2 || !parts[0]?.trim() || !parts[1]?.trim()) {
      return { pubkey: '', host: '', error: t('lightningOps.watchtowerInvalidAddress') }
    }
    const host = parts[1].trim()
    if (!host.includes(':')) {
      return { pubkey: '', host: '', error: t('lightningOps.watchtowerInvalidAddress') }
    }
    return { pubkey: parts[0].trim(), host }
  }

  const handleAddWatchtower = async () => {
    if (watchtowerBusy) return
    const parsed = parseWatchtowerAddress(watchtowerAddress)
    if (parsed.error) {
      setWatchtowerStatus(parsed.error)
      return
    }

    setWatchtowerBusy(true)
    setWatchtowerStatus(t('lightningOps.watchtowerAdding'))
    try {
      await addLnWatchtower({ address: `${parsed.pubkey}@${parsed.host}` })
      setWatchtowerAddress('')
      setWatchtowerStatus(t('lightningOps.watchtowerAdded'))
      load()
    } catch (err: any) {
      setWatchtowerStatus(err?.message || t('lightningOps.watchtowerActionFailed'))
    } finally {
      setWatchtowerBusy(false)
    }
  }

  const handleRemoveWatchtower = async (pubkey: string) => {
    if (watchtowerBusy) return
    const confirmed = window.confirm(t('lightningOps.watchtowerRemoveConfirm'))
    if (!confirmed) return

    setWatchtowerBusy(true)
    setWatchtowerStatus(t('lightningOps.watchtowerRemoving'))
    try {
      await removeLnWatchtower({ pubkey })
      setWatchtowerStatus(t('lightningOps.watchtowerRemoved'))
      load()
    } catch (err: any) {
      setWatchtowerStatus(err?.message || t('lightningOps.watchtowerActionFailed'))
    } finally {
      setWatchtowerBusy(false)
    }
  }

  const handleConnectPeer = async () => {
    setPeerStatus(t('lightningOps.connectingPeer'))
    try {
      await connectPeer({ address: peerAddress, perm: !peerTemporary })
      setPeerStatus(t('lightningOps.peerConnected'))
      setPeerAddress('')
      setPeerTemporary(false)
      load()
    } catch (err: any) {
      setPeerStatus(err?.message || t('lightningOps.peerConnectFailed'))
    }
  }

  const handleDisconnect = async (pubkey: string) => {
    const confirmed = window.confirm(t('lightningOps.disconnectConfirm'))
    if (!confirmed) return
    setPeerActionStatus(t('lightningOps.disconnectingPeer'))
    try {
      await disconnectPeer({ pubkey })
      setPeerActionStatus(t('lightningOps.peerDisconnected'))
      load()
    } catch (err: any) {
      setPeerActionStatus(err?.message || t('lightningOps.disconnectFailed'))
    }
  }

  const handleBoostPeers = async () => {
    setBoostRunning(true)
    setBoostStatus(t('lightningOps.boostingPeers'))
    try {
      const res = await boostPeers({ limit: 25 })
      const connected = res?.connected ?? 0
      const skipped = res?.skipped ?? 0
      const failed = res?.failed ?? 0
      setBoostStatus(t('lightningOps.boostComplete', { connected, skipped, failed }))
      load()
    } catch (err: any) {
      setBoostStatus(err?.message || t('lightningOps.boostFailed'))
    } finally {
      setBoostRunning(false)
    }
  }

  const handleToggleAmboss = async () => {
    if (ambossBusy) return
    const nextEnabled = !amboss?.enabled
    setAmbossBusy(true)
    setAmbossStatus(t('lightningOps.ambossHealthSaving'))
    try {
      const res = await updateAmbossHealth({ enabled: nextEnabled })
      setAmboss(res as AmbossHealthStatus)
      setAmbossStatus(nextEnabled ? t('lightningOps.ambossHealthEnabled') : t('lightningOps.ambossHealthDisabled'))
    } catch (err: any) {
      setAmbossStatus(err?.message || t('lightningOps.ambossHealthSaveFailed'))
    } finally {
      setAmbossBusy(false)
    }
  }

  const handleAutofeeSave = async () => {
    if (autofeeBusy) return
    if (!autofeeConfig) {
      setAutofeeMessage(t('lightningOps.autofeeConfigUnavailable'))
      return
    }
    setAutofeeBusy(true)
    setAutofeeMessage(t('lightningOps.autofeeSaving'))
    try {
      const lookbackDays = Math.max(5, Math.min(21, Number(autofeeLookback || 7)))
      const intervalSec = Math.max(1, Number(autofeeIntervalHours || 4)) * 3600
      const cooldownUpSec = Math.max(1, Number(autofeeCooldownUp || 3)) * 3600
      const cooldownDownSec = Math.max(1, Number(autofeeCooldownDown || 4)) * 3600
      const parsePercentOverride = (raw: string, min: number, max: number) => {
        const value = Number(raw)
        if (!Number.isFinite(value) || value <= 0) return 0
        return Math.max(min, Math.min(max, value)) / 100
      }
      const stepCapOverride = parsePercentOverride(autofeeStepCapOverride, 1, 30)
      const discoveryStepCapDownOverride = parsePercentOverride(autofeeDiscoveryStepCapDownOverride, 1, 40)
      const stallFloorRelaxGapFracOverride = parsePercentOverride(autofeeStallFloorRelaxGapFracOverride, 1, 80)
      const inboundDiscountMaxRatioOverride = parsePercentOverride(autofeeInboundDiscountMaxRatioOverride, 50, 100)
      const inboundDiscountReachOutRatioOverride = parsePercentOverride(autofeeInboundDiscountReachOutRatioOverride, 5, 50)
      const inboundDiscountMinRetainedSpreadFracOverride = parsePercentOverride(autofeeInboundDiscountMinRetainedSpreadFracOverride, 1, 50)
      const outrateFloorFactorLowOverride = parsePercentOverride(autofeeOutrateFloorFactorLowOverride, 50, 100)
      const softenMinOutRatioOverride = parsePercentOverride(autofeeSoftenMinOutRatioOverride, 5, 95)
      const softenMaxDropToPegFracOverride = parsePercentOverride(autofeeSoftenMaxDropToPegFracOverride, 50, 100)
      const htlcMinAttemptsOverrideRaw = Number(autofeeHtlcMinAttemptsOverride || 0)
      const htlcMinAttemptsOverride = Number.isFinite(htlcMinAttemptsOverrideRaw) && htlcMinAttemptsOverrideRaw > 0
        ? Math.max(1, Math.min(100, Math.round(htlcMinAttemptsOverrideRaw)))
        : 0
      const htlcPolicyFailRateOverride = parsePercentOverride(autofeeHtlcPolicyFailRateOverride, 5, 90)
      const htlcLiquidityFailRateOverride = parsePercentOverride(autofeeHtlcLiquidityFailRateOverride, 5, 90)
      const minPpmRaw = Math.max(0, Number(autofeeMinPpm || 0))
      let maxPpmRaw = Math.max(0, Number(autofeeMaxPpm || 2000))
      if (maxPpmRaw < minPpmRaw) {
        maxPpmRaw = minPpmRaw
      }
      const superSourceBaseFee = Math.max(0, Number(autofeeSuperSourceBaseFee || 1000))
      const payload: any = {
        enabled: autofeeEnabled,
        operation_mode: autofeeOperationMode,
        profile: autofeeProfile,
        lookback_days: lookbackDays,
        run_interval_sec: intervalSec,
        cooldown_up_sec: cooldownUpSec,
        cooldown_down_sec: cooldownDownSec,
        step_cap_override: stepCapOverride,
        discovery_step_cap_down_override: discoveryStepCapDownOverride,
        stall_floor_relax_gap_frac_override: stallFloorRelaxGapFracOverride,
        inbound_discount_max_ratio_override: inboundDiscountMaxRatioOverride,
        inbound_discount_reach_out_ratio_override: inboundDiscountReachOutRatioOverride,
        inbound_discount_min_retained_spread_frac_override: inboundDiscountMinRetainedSpreadFracOverride,
        outrate_floor_factor_low_override: outrateFloorFactorLowOverride,
        soften_min_out_ratio_override: softenMinOutRatioOverride,
        soften_max_drop_to_peg_frac_override: softenMaxDropToPegFracOverride,
        htlc_min_attempts_60m_override: htlcMinAttemptsOverride,
        htlc_policy_fail_rate_override: htlcPolicyFailRateOverride,
        htlc_liquidity_fail_rate_override: htlcLiquidityFailRateOverride,
        rebal_cost_mode: autofeeRebalMode,
        min_ppm: minPpmRaw,
        max_ppm: maxPpmRaw,
        native_seed_enabled: autofeeNativeSeedEnabled,
        amboss_enabled: autofeeAmbossEnabled,
        inbound_passive_enabled: autofeeInboundPassive,
        discovery_enabled: autofeeDiscovery,
        explorer_enabled: autofeeExplorer,
        idle_refresh_enabled: autofeeIdleRefresh,
        super_source_enabled: autofeeSuperSource,
        super_source_base_fee_msat: superSourceBaseFee,
        revfloor_enabled: autofeeRevfloor,
        circuit_breaker_enabled: autofeeCircuitBreaker,
        extreme_drain_enabled: autofeeExtremeDrain,
        htlc_signal_enabled: autofeeHtlcSignalEnabled,
        htlc_mode: autofeeHtlcMode
      }
      if (autofeeAmbossToken.trim()) {
        payload.amboss_token = autofeeAmbossToken.trim()
      }
      const res = await updateAutofeeConfig(payload)
      const nextConfig = res as AutofeeConfig
      setAutofeeConfig(nextConfig)
      setAutofeeEnabled(Boolean(nextConfig.enabled))
      setAutofeeMessage(t('lightningOps.autofeeSaved'))
      setAutofeeAmbossToken('')
      const status = await getAutofeeStatus()
      setAutofeeStatus(status as AutofeeStatus)
    } catch (err: any) {
      setAutofeeMessage(err?.message || t('lightningOps.autofeeSaveFailed'))
    } finally {
      setAutofeeBusy(false)
    }
  }

  const handleAutofeeRun = async (dryRun: boolean) => {
    if (autofeeBusy) return
    setAutofeeBusy(true)
    setAutofeeMessage(dryRun ? t('lightningOps.autofeeDryRunning') : t('lightningOps.autofeeRunning'))
    try {
      await runAutofee({ dry_run: dryRun })
      setAutofeeMessage(dryRun ? t('lightningOps.autofeeDryRunDone') : t('lightningOps.autofeeRunDone'))
      const status = await getAutofeeStatus()
      setAutofeeStatus(status as AutofeeStatus)
      const results = await getAutofeeResults(buildAutofeeResultsQuery())
      const payload = results as any
      setAutofeeResults(Array.isArray(payload?.lines) ? payload.lines : [])
      setAutofeeResultItems(Array.isArray(payload?.items) ? payload.items : [])
      setAutofeeResultsStatus('')
    } catch (err: any) {
      setAutofeeMessage(err?.message || t('lightningOps.autofeeRunFailed'))
    } finally {
      setAutofeeBusy(false)
    }
  }

  const handleAutofeeResultsRefresh = async () => {
    setAutofeeResultsStatus(t('lightningOps.autofeeResultsLoading'))
    try {
      const results = await getAutofeeResults(buildAutofeeResultsQuery())
      const payload = results as any
      setAutofeeResults(Array.isArray(payload?.lines) ? payload.lines : [])
      setAutofeeResultItems(Array.isArray(payload?.items) ? payload.items : [])
      setAutofeeResultsStatus('')
    } catch (err: any) {
      setAutofeeResultsStatus(err?.message || t('lightningOps.autofeeResultsUnavailable'))
    }
  }

  const handleAutofeeChannelToggle = async (channel: Channel, enabled: boolean) => {
    try {
      await updateAutofeeChannels({ channel_id: channel.channel_id, channel_point: channel.channel_point, enabled })
      const key = autofeeChannelKey(channel.channel_point, channel.channel_id)
      if (!key) return
      setAutofeeSettings((prev) => ({ ...prev, [key]: enabled }))
    } catch (err: any) {
      setAutofeeMessage(err?.message || t('lightningOps.autofeeChannelUpdateFailed'))
    }
  }

  const handleAutofeeBulk = async (enabled: boolean) => {
    if (autofeeBusy) return
    setAutofeeBusy(true)
    setAutofeeMessage(enabled ? t('lightningOps.autofeeIncludingAll') : t('lightningOps.autofeeExcludingAll'))
    try {
      await updateAutofeeChannels({ apply_all: true, enabled })
      const map: Record<string, boolean> = {}
      channels.forEach((ch) => {
        const key = autofeeChannelKey(ch.channel_point, ch.channel_id)
        if (key) map[key] = enabled
      })
      setAutofeeSettings(map)
      setAutofeeMessage(t('lightningOps.autofeeBulkDone'))
    } catch (err: any) {
      setAutofeeMessage(err?.message || t('lightningOps.autofeeBulkFailed'))
    } finally {
      setAutofeeBusy(false)
    }
  }

  const handleToggleChanHeal = async () => {
    if (chanHealBusy) return
    const nextEnabled = !chanHeal?.enabled
    setChanHealBusy(true)
    setChanHealStatus(t('lightningOps.chanHealSaving'))
    const interval = Number(chanHealInterval || 0)
    const payload: { enabled?: boolean; interval_sec?: number } = { enabled: nextEnabled }
    if (interval > 0) {
      payload.interval_sec = interval
    }
    try {
      const res = await updateLnChanHeal(payload)
      setChanHeal(res as ChanHealStatus)
      setChanHealStatus(nextEnabled ? t('lightningOps.chanHealEnabled') : t('lightningOps.chanHealDisabled'))
    } catch (err: any) {
      setChanHealStatus(err?.message || t('lightningOps.chanHealSaveFailed'))
    } finally {
      setChanHealBusy(false)
    }
  }

  const handleSaveChanHealInterval = async () => {
    if (chanHealBusy) return
    const interval = Number(chanHealInterval || 0)
    if (!interval || interval <= 0) {
      setChanHealStatus(t('lightningOps.chanHealIntervalInvalid'))
      return
    }
    setChanHealBusy(true)
    setChanHealStatus(t('lightningOps.chanHealSaving'))
    try {
      const res = await updateLnChanHeal({ interval_sec: interval })
      setChanHeal(res as ChanHealStatus)
      chanHealIntervalDirtyRef.current = false
      setChanHealStatus(t('lightningOps.chanHealSaved'))
    } catch (err: any) {
      setChanHealStatus(err?.message || t('lightningOps.chanHealSaveFailed'))
    } finally {
      setChanHealBusy(false)
    }
  }

  const handleToggleHtlcManager = async () => {
    if (htlcManagerBusy) return
    const nextEnabled = !htlcManager?.enabled
    setHtlcManagerBusy(true)
    setHtlcManagerStatus(t('lightningOps.htlcManagerSaving'))
    try {
      const res = await updateLnHtlcManager({ enabled: nextEnabled })
      setHtlcManager(res as HtlcManagerStatus)
      setHtlcManagerStatus(nextEnabled ? t('lightningOps.htlcManagerEnabled') : t('lightningOps.htlcManagerDisabled'))
    } catch (err: any) {
      setHtlcManagerStatus(err?.message || t('lightningOps.htlcManagerSaveFailed'))
    } finally {
      setHtlcManagerBusy(false)
    }
  }

  const handleSaveHtlcManager = async () => {
    if (htlcManagerBusy) return
    const intervalMinutes = Number(htlcManagerIntervalMinutes || 0)
    const minSat = Number(htlcManagerMinSat || 0)
    const maxPct = Number(htlcManagerMaxPct || 0)
    if (!intervalMinutes || intervalMinutes < 1 || intervalMinutes > 2880) {
      setHtlcManagerStatus(t('lightningOps.htlcManagerIntervalInvalid'))
      return
    }
    if (!minSat || minSat < 1) {
      setHtlcManagerStatus(t('lightningOps.htlcManagerMinInvalid'))
      return
    }
    if (maxPct < 0) {
      setHtlcManagerStatus(t('lightningOps.htlcManagerMaxPctInvalid'))
      return
    }
    setHtlcManagerBusy(true)
    setHtlcManagerStatus(t('lightningOps.htlcManagerSaving'))
    try {
      const res = await updateLnHtlcManager({
        interval_minutes: intervalMinutes,
        min_htlc_sat: minSat,
        max_local_pct: maxPct
      })
      setHtlcManager(res as HtlcManagerStatus)
      htlcManagerFormDirtyRef.current = false
      setHtlcManagerStatus(t('lightningOps.htlcManagerSaved'))
    } catch (err: any) {
      setHtlcManagerStatus(err?.message || t('lightningOps.htlcManagerSaveFailed'))
    } finally {
      setHtlcManagerBusy(false)
    }
  }

  const handleRunHtlcManagerNow = async () => {
    if (htlcManagerBusy) return
    setHtlcManagerBusy(true)
    setHtlcManagerStatus(t('lightningOps.htlcManagerRunning'))
    try {
      const res = await updateLnHtlcManager({ run_now: true })
      setHtlcManager(res as HtlcManagerStatus)
      const [logsRes, failedRes] = await Promise.all([
        getLnHtlcManagerLogs(),
        getLnHtlcManagerFailed()
      ])
      const logEntries = (logsRes as any)?.entries
      const failedEntries = (failedRes as any)?.entries
      setHtlcManagerLogs(Array.isArray(logEntries) ? logEntries : [])
      setHtlcManagerFailed(Array.isArray(failedEntries) ? failedEntries : [])
      setHtlcManagerStatus(t('lightningOps.htlcManagerRunDone'))
    } catch (err: any) {
      setHtlcManagerStatus(err?.message || t('lightningOps.htlcManagerRunFailed'))
    } finally {
      setHtlcManagerBusy(false)
    }
  }

  const handleAutofeeRefresh = async (dryRun = false) => {
    if (autofeeBusy) return
    setAutofeeBusy(true)
    setAutofeeMessage(dryRun ? t('lightningOps.autofeeDryRefreshing') : t('lightningOps.autofeeRefreshing'))
    try {
      const payload = await refreshAutofeeReferences({ dry_run: dryRun, include_inbound: autofeeRefreshIncludeInbound })
      const updated = Number((payload as any)?.updated || 0)
      const inboundUpdated = Number((payload as any)?.inbound_updated || 0)
      const same = Number((payload as any)?.same || 0)
      const skipped = Number((payload as any)?.skipped || 0)
      const errors = Number((payload as any)?.errors || 0)
      const messageBase = dryRun
        ? t('lightningOps.autofeeDryRefreshDone', { updated, same, skipped, errors })
        : t('lightningOps.autofeeRefreshDone', { updated, same, skipped, errors })
      setAutofeeMessage(autofeeRefreshIncludeInbound
        ? `${messageBase} ${t('lightningOps.autofeeRefreshInboundDone', { count: inboundUpdated })}`
        : messageBase)
      if (!dryRun) {
        const channelsPayload = await getLnChannels()
        applyChannelsPayload(channelsPayload)
      }
      const results = await getAutofeeResults(buildAutofeeResultsQuery())
      const resultsPayload = results as any
      setAutofeeResults(Array.isArray(resultsPayload?.lines) ? resultsPayload.lines : [])
      setAutofeeResultItems(Array.isArray(resultsPayload?.items) ? resultsPayload.items : [])
      setAutofeeResultsStatus('')
    } catch (err: any) {
      setAutofeeMessage(err?.message || (dryRun ? t('lightningOps.autofeeDryRefreshFailed') : t('lightningOps.autofeeRefreshFailed')))
    } finally {
      setAutofeeBusy(false)
    }
  }

  const flashAutofeeRefreshButton = (channelPoint: string, tone: 'success' | 'same' | 'error') => {
    setAutofeeRefreshFlashByPoint((prev) => ({ ...prev, [channelPoint]: tone }))
    if (typeof window === 'undefined') return
    const existingTimer = autofeeRefreshFlashTimersRef.current[channelPoint]
    if (existingTimer) {
      window.clearTimeout(existingTimer)
    }
    autofeeRefreshFlashTimersRef.current[channelPoint] = window.setTimeout(() => {
      setAutofeeRefreshFlashByPoint((prev) => {
        const next = { ...prev }
        delete next[channelPoint]
        return next
      })
      delete autofeeRefreshFlashTimersRef.current[channelPoint]
    }, 2200)
  }

  const handleAutofeeChannelRefresh = async (ch: Channel) => {
    const channelPoint = String(ch.channel_point || '').trim()
    if (!channelPoint || autofeeRefreshBusyByPoint[channelPoint]) return

    const alias = ch.peer_alias || ch.remote_pubkey || t('lightningOps.unknownPeer')
    const channelID = normalizeAutofeeChannelID(ch.channel_id_str)
    setAutofeeRefreshBusyByPoint((prev) => ({ ...prev, [channelPoint]: true }))
    setAutofeeMessage(t('lightningOps.autofeeChannelRefreshing', { alias }))
    try {
      const payload = await refreshAutofeeReferences({
        dry_run: false,
        include_inbound: autofeeRefreshIncludeInbound,
        channel_point: channelPoint,
        channel_id_str: channelID || undefined
      })
      const rawItems = (payload as any)?.items
      const item = Array.isArray(rawItems) && rawItems.length ? rawItems[0] : null
      const updated = Number((payload as any)?.updated || 0)
      const inboundUpdated = Number((payload as any)?.inbound_updated || 0)
      const same = Number((payload as any)?.same || 0)
      const skipped = Number((payload as any)?.skipped || 0)
      const errors = Number((payload as any)?.errors || 0)
      const currentPpm = Number(item?.current_ppm ?? ch.fee_rate_ppm ?? 0)
      const targetPpm = Number(item?.target_ppm ?? currentPpm)
      const source = String(item?.source || '').trim() || t('common.na')
      const reason = String(item?.reason || '').trim() || t('common.na')

      if (errors > 0 || item?.error) {
        flashAutofeeRefreshButton(channelPoint, 'error')
        setAutofeeMessage(item?.error || t('lightningOps.autofeeChannelRefreshFailed', { alias }))
      } else if (updated > 0 || item?.changed) {
        flashAutofeeRefreshButton(channelPoint, 'success')
        const inboundText = inboundUpdated > 0
          ? ` ${t('lightningOps.autofeeRefreshInboundDone', { count: inboundUpdated })}`
          : ''
        setAutofeeMessage(`${t('lightningOps.autofeeChannelRefreshApplied', { alias, current: currentPpm, target: targetPpm, source })}${inboundText}`)
      } else if (same > 0 || reason === 'same') {
        flashAutofeeRefreshButton(channelPoint, 'same')
        setAutofeeMessage(t('lightningOps.autofeeChannelRefreshSame', { alias, target: targetPpm, source }))
      } else if (skipped > 0) {
        flashAutofeeRefreshButton(channelPoint, 'error')
        setAutofeeMessage(t('lightningOps.autofeeChannelRefreshSkipped', { alias, reason }))
      } else {
        flashAutofeeRefreshButton(channelPoint, 'same')
        setAutofeeMessage(t('lightningOps.autofeeChannelRefreshSame', { alias, target: targetPpm, source }))
      }

      const channelsPayload = await getLnChannels()
      applyChannelsPayload(channelsPayload)
      const results = await getAutofeeResults(buildAutofeeResultsQuery())
      const resultsPayload = results as any
      setAutofeeResults(Array.isArray(resultsPayload?.lines) ? resultsPayload.lines : [])
      setAutofeeResultItems(Array.isArray(resultsPayload?.items) ? resultsPayload.items : [])
      setAutofeeResultsStatus('')
    } catch (err: any) {
      flashAutofeeRefreshButton(channelPoint, 'error')
      setAutofeeMessage(err?.message || t('lightningOps.autofeeChannelRefreshFailed', { alias }))
    } finally {
      setAutofeeRefreshBusyByPoint((prev) => {
        const next = { ...prev }
        delete next[channelPoint]
        return next
      })
    }
  }

  const renderAutofeeChannelRefreshButton = (ch: Channel, compact = false) => {
    const channelPoint = String(ch.channel_point || '').trim()
    const busy = channelPoint ? autofeeRefreshBusyByPoint[channelPoint] === true : false
    const flash = channelPoint ? autofeeRefreshFlashByPoint[channelPoint] : undefined
    const alias = ch.peer_alias || ch.remote_pubkey || t('lightningOps.unknownPeer')
    const toneClass = busy
      ? 'border-sky-300/70 bg-sky-500/15 text-sky-100'
      : flash === 'success'
        ? 'border-emerald-300/70 bg-emerald-500/20 text-emerald-50'
        : flash === 'same'
          ? 'border-brass/70 bg-brass/15 text-brass'
          : flash === 'error'
            ? 'border-amber-300/70 bg-amber-500/20 text-amber-50'
            : 'border-white/15 bg-white/5 text-fog/60 hover:border-sky-300/70 hover:bg-sky-500/10 hover:text-sky-100'
    return (
      <button
        type="button"
        className={`inline-flex ${compact ? 'h-7 w-7 text-[13px]' : 'h-8 w-8 text-sm'} shrink-0 items-center justify-center rounded-full border font-semibold leading-none transition disabled:cursor-wait disabled:opacity-70 ${toneClass}`}
        title={t('lightningOps.autofeeChannelRefresh', { alias })}
        aria-label={t('lightningOps.autofeeChannelRefresh', { alias })}
        disabled={!channelPoint || busy}
        onClick={(event) => {
          event.stopPropagation()
          void handleAutofeeChannelRefresh(ch)
        }}
      >
        <span className={busy ? 'inline-block animate-spin' : ''} aria-hidden="true">↻</span>
      </button>
    )
  }

  const handleToggleFailedPaymentsCleaner = async () => {
    if (failedPaymentsCleanerBusy) return
    const nextEnabled = !failedPaymentsCleaner?.enabled
    setFailedPaymentsCleanerBusy(true)
    setFailedPaymentsCleanerStatus(t('lightningOps.failedPaymentsCleanerSaving'))
    try {
      const res = await updateLnFailedPaymentsCleaner({ enabled: nextEnabled })
      setFailedPaymentsCleaner(res as FailedPaymentsCleanerStatus)
      setFailedPaymentsCleanerStatus(nextEnabled ? t('lightningOps.failedPaymentsCleanerEnabled') : t('lightningOps.failedPaymentsCleanerDisabled'))
    } catch (err: any) {
      setFailedPaymentsCleanerStatus(err?.message || t('lightningOps.failedPaymentsCleanerSaveFailed'))
    } finally {
      setFailedPaymentsCleanerBusy(false)
    }
  }

  const handleSaveFailedPaymentsCleaner = async () => {
    if (failedPaymentsCleanerBusy) return
    const intervalHours = Number(failedPaymentsCleanerIntervalHours || 0)
    if (!intervalHours || intervalHours < 1 || intervalHours > 168) {
      setFailedPaymentsCleanerStatus(t('lightningOps.failedPaymentsCleanerIntervalInvalid'))
      return
    }
    setFailedPaymentsCleanerBusy(true)
    setFailedPaymentsCleanerStatus(t('lightningOps.failedPaymentsCleanerSaving'))
    try {
      const res = await updateLnFailedPaymentsCleaner({ interval_hours: intervalHours })
      setFailedPaymentsCleaner(res as FailedPaymentsCleanerStatus)
      failedPaymentsCleanerIntervalDirtyRef.current = false
      setFailedPaymentsCleanerStatus(t('lightningOps.failedPaymentsCleanerSaved'))
    } catch (err: any) {
      setFailedPaymentsCleanerStatus(err?.message || t('lightningOps.failedPaymentsCleanerSaveFailed'))
    } finally {
      setFailedPaymentsCleanerBusy(false)
    }
  }

  const handleRunFailedPaymentsCleanerNow = async () => {
    if (failedPaymentsCleanerBusy) return
    setFailedPaymentsCleanerBusy(true)
    setFailedPaymentsCleanerStatus(t('lightningOps.failedPaymentsCleanerRunning'))
    try {
      const res = await updateLnFailedPaymentsCleaner({ run_now: true })
      setFailedPaymentsCleaner(res as FailedPaymentsCleanerStatus)
      setFailedPaymentsCleanerStatus(t('lightningOps.failedPaymentsCleanerRunDone'))
    } catch (err: any) {
      setFailedPaymentsCleanerStatus(err?.message || t('lightningOps.failedPaymentsCleanerRunFailed'))
    } finally {
      setFailedPaymentsCleanerBusy(false)
    }
  }

  const handleToggleTorPeerChecker = async () => {
    if (torPeerCheckerBusy) return
    const nextEnabled = !torPeerChecker?.enabled
    setTorPeerCheckerBusy(true)
    setTorPeerCheckerStatus(t('lightningOps.torPeerSaving'))
    try {
      const res = await updateLnTorPeerChecker({ enabled: nextEnabled })
      setTorPeerChecker(res as TorPeerCheckerStatus)
      setTorPeerCheckerStatus(nextEnabled ? t('lightningOps.torPeerEnabled') : t('lightningOps.torPeerDisabled'))
    } catch (err: any) {
      setTorPeerCheckerStatus(err?.message || t('lightningOps.torPeerSaveFailed'))
    } finally {
      setTorPeerCheckerBusy(false)
    }
  }

  const handleSaveTorPeerChecker = async () => {
    if (torPeerCheckerBusy) return
    const intervalHours = Number(torPeerCheckerIntervalHours || 0)
    if (!intervalHours || intervalHours < 2 || intervalHours > 168) {
      setTorPeerCheckerStatus(t('lightningOps.torPeerIntervalInvalid'))
      return
    }
    setTorPeerCheckerBusy(true)
    setTorPeerCheckerStatus(t('lightningOps.torPeerSaving'))
    try {
      const res = await updateLnTorPeerChecker({ interval_hours: intervalHours })
      setTorPeerChecker(res as TorPeerCheckerStatus)
      torPeerCheckerIntervalDirtyRef.current = false
      setTorPeerCheckerStatus(t('lightningOps.torPeerSaved'))
    } catch (err: any) {
      setTorPeerCheckerStatus(err?.message || t('lightningOps.torPeerSaveFailed'))
    } finally {
      setTorPeerCheckerBusy(false)
    }
  }

  const handleRunTorPeerCheckerNow = async () => {
    if (torPeerCheckerBusy) return
    setTorPeerCheckerBusy(true)
    setTorPeerCheckerStatus(t('lightningOps.torPeerRunning'))
    try {
      const res = await updateLnTorPeerChecker({ run_now: true })
      setTorPeerChecker(res as TorPeerCheckerStatus)
      const logsRes = await getLnTorPeerCheckerLogs()
      const entries = (logsRes as any)?.entries
      setTorPeerCheckerLogs(Array.isArray(entries) ? entries : [])
      setTorPeerCheckerStatus(t('lightningOps.torPeerRunDone'))
    } catch (err: any) {
      setTorPeerCheckerStatus(err?.message || t('lightningOps.torPeerRunFailed'))
    } finally {
      setTorPeerCheckerBusy(false)
    }
  }

  const handleSignMessage = async () => {
    if (signBusy) return
    const message = signMessage.trim()
    if (!message) {
      setSignStatus(t('lightningOps.signMessageRequired'))
      return
    }
    setSignBusy(true)
    setSignStatus(t('lightningOps.signMessageSigning'))
    setSignSignature('')
    setSignCopied(false)
    try {
      const res = await signLnMessage({ message })
      const signature = String(res?.signature || '').trim()
      if (!signature) {
        setSignStatus(t('lightningOps.signMessageFailed'))
        return
      }
      setSignSignature(signature)
      setSignStatus(t('lightningOps.signMessageReady'))
    } catch (err: any) {
      setSignStatus(err?.message || t('lightningOps.signMessageFailed'))
    } finally {
      setSignBusy(false)
    }
  }

  const handleCopySignature = async () => {
    if (!signSignature) return
    try {
      await navigator.clipboard.writeText(signSignature)
      setSignCopied(true)
    } catch {
      setSignStatus(t('common.copyFailedManual'))
    }
  }

  const channelIDText = (channel: Pick<Channel, 'channel_id' | 'channel_id_str'>) => {
    const precise = String(channel.channel_id_str || '').trim()
    if (precise) return precise
    const fallback = Number(channel.channel_id || 0)
    if (!Number.isFinite(fallback) || fallback <= 0) return ''
    return Math.trunc(fallback).toString()
  }

  const handleCopyChannelID = async (channel: Channel) => {
    const value = channelIDText(channel)
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
      const key = channel.channel_point || value
      setCopiedChannelIDKey(key)
      window.setTimeout(() => {
        setCopiedChannelIDKey((current) => current === key ? '' : current)
      }, 1600)
    } catch {
      setStatus(t('common.copyFailedManual'))
    }
  }

  const handleScbFileSelected = async (event: any) => {
    const file = event?.target?.files?.[0]
    if (!file) return
    setScbRestoreStatus(t('lightningOps.scbRecoveryReading'))
    setScbRestoreResult(null)
    try {
      const buffer = await file.arrayBuffer()
      const encoded = arrayBufferToBase64(buffer)
      if (!encoded) {
        setScbRestoreStatus(t('lightningOps.scbRecoveryFileReadFailed'))
        return
      }
      setScbRestoreData(encoded)
      setScbRestoreFileName(file.name || '')
      setScbRestoreStatus(t('lightningOps.scbRecoveryFileLoaded', { name: file.name || 'SCB' }))
    } catch {
      setScbRestoreStatus(t('lightningOps.scbRecoveryFileReadFailed'))
    } finally {
      if (event?.target) {
        event.target.value = ''
      }
    }
  }

  const handleScbRestore = async () => {
    if (scbRestoreBusy) return
    const backup = scbRestoreData.trim()
    if (!backup) {
      setScbRestoreStatus(t('lightningOps.scbRecoveryBackupRequired'))
      return
    }
    if (!scbRestoreConfirm) {
      setScbRestoreStatus(t('lightningOps.scbRecoveryCheckRequired'))
      return
    }
    if (!scbRestorePhraseValid) {
      setScbRestoreStatus(t('lightningOps.scbRecoveryPhraseInvalid'))
      return
    }

    const confirmed = window.confirm(t('lightningOps.scbRecoveryRunConfirm'))
    if (!confirmed) return

    setScbRestoreBusy(true)
    setScbRestoreResult(null)
    setScbRestoreStatus(t('lightningOps.scbRecoveryRunning'))
    try {
      const res = await restoreLnScb({
        multi_chan_backup: backup,
        confirm_phrase: scbRestorePhrase.trim(),
      })
      const numRestored = Number(res?.num_restored ?? 0)
      const safeNumRestored = Number.isFinite(numRestored) ? numRestored : 0
      setScbRestoreResult(safeNumRestored)
      setScbRestoreStatus(t('lightningOps.scbRecoveryDone', { count: safeNumRestored }))
      await load()
    } catch (err: any) {
      setScbRestoreStatus(err?.message || t('lightningOps.scbRecoveryFailed'))
    } finally {
      setScbRestoreBusy(false)
    }
  }

  const handleToggleChanStatus = async (channel: Channel) => {
    if (!channel.channel_point || chanStatusBusy) return
    if (!channel.active) return
    const enable = channel.local_disabled ?? isLocalChanDisabled(channel.chan_status_flags)
    setChanStatusBusy(channel.channel_point)
    setChanStatusMessage(enable ? t('lightningOps.channelEnabling') : t('lightningOps.channelDisabling'))
    try {
      await updateLnChannelStatus({ channel_point: channel.channel_point, enabled: enable })
      setChanStatusMessage(enable ? t('lightningOps.channelEnabled') : t('lightningOps.channelDisabled'))
      load()
    } catch (err: any) {
      setChanStatusMessage(err?.message || t('lightningOps.channelStatusFailed'))
    } finally {
      setChanStatusBusy(null)
    }
  }

  const handleTogglePeerRecommendations = async (channel: Channel) => {
    if (!channel.channel_point) return
    const channelPoint = channel.channel_point
    setPeerRecommendationCopiedKey('')
    if (peerRecommendationOpenChannelPoint === channelPoint) {
      setPeerRecommendationOpenChannelPoint('')
      return
    }

    setPeerRecommendationOpenChannelPoint(channelPoint)
    if (peerRecommendationsByChannel[channelPoint]) return

    setPeerRecommendationLoadingByChannel((prev) => ({ ...prev, [channelPoint]: true }))
    setPeerRecommendationErrorByChannel((prev) => ({ ...prev, [channelPoint]: '' }))
    try {
      const res = await getLnChannelPeerRecommendations(channelPoint) as PeerRecommendationResponse
      setPeerRecommendationsByChannel((prev) => ({
        ...prev,
        [channelPoint]: Array.isArray(res?.recommendations) ? res.recommendations : []
      }))
      setPeerRecommendationTierByChannel((prev) => ({
        ...prev,
        [channelPoint]: String(res?.selection_tier || 'strict')
      }))
    } catch (err: any) {
      setPeerRecommendationErrorByChannel((prev) => ({
        ...prev,
        [channelPoint]: err?.message || t('lightningOps.peerRecommendationsLoadFailed')
      }))
    } finally {
      setPeerRecommendationLoadingByChannel((prev) => ({ ...prev, [channelPoint]: false }))
    }
  }

  const handleCopyPeerRecommendation = async (channelPoint: string, item: PeerRecommendation) => {
    const value = recommendationPeerAddress(item)
    if (!value) {
      setOpenStatus(t('lightningOps.peerRecommendationsAddressUnavailable'))
      return
    }
    try {
      await navigator.clipboard.writeText(value)
      const copyKey = recommendationCopyKey(channelPoint, item.pub_key)
      setPeerRecommendationCopiedKey(copyKey)
      window.setTimeout(() => {
        setPeerRecommendationCopiedKey((current) => current === copyKey ? '' : current)
      }, 1800)
    } catch {
      setOpenStatus(t('common.copyFailedManual'))
    }
  }

  const scrollToSectionAndFocus = (sectionID: string, focusTarget?: HTMLInputElement | null) => {
    if (typeof window === 'undefined') return
    setLightningToolsOpen(true)
    window.setTimeout(() => {
      const target = document.getElementById(sectionID)
      if (target) {
        target.scrollIntoView({ behavior: 'smooth', block: 'start' })
      }
      focusTarget?.focus()
    }, 50)
  }

  const handleUsePeerRecommendation = (item: PeerRecommendation) => {
    const value = recommendationPeerAddress(item)
    if (!value) {
      setOpenStatus(t('lightningOps.peerRecommendationsAddressUnavailable'))
      return
    }
    setOpenPeer(value)
    setOpenStatus(t('lightningOps.peerRecommendationsLoaded'))
    scrollToSectionAndFocus(OPEN_CHANNEL_SECTION_ID, openPeerInputRef.current)
  }

  const handleUsePeerRecommendationBatch = (item: PeerRecommendation) => {
    const value = recommendationPeerAddress(item)
    if (!value) {
      setBatchStatus(t('lightningOps.peerRecommendationsAddressUnavailable'))
      return
    }
    setBatchPeer(value)
    setBatchStatus(t('lightningOps.peerRecommendationsBatchLoaded'))
    scrollToSectionAndFocus(BATCH_OPEN_SECTION_ID, batchPeerInputRef.current)
  }

  function openPreviewMessage(preview?: OpenChannelPreview | null) {
    const code = String(preview?.message_code || '').trim()
    switch (code) {
      case 'dust_change_absorbed':
        return t('lightningOps.openPreviewDustChange')
      case 'insufficient_confirmed_balance':
      case 'insufficient_balance_for_fees':
        return t('lightningOps.openPreviewInsufficient')
      case 'no_confirmed_utxos':
        return t('lightningOps.openPreviewNoConfirmedUtxos')
      default:
        return String(preview?.message || '').trim()
    }
  }

  function batchPreviewMessage(preview?: BatchOpenPreview | null) {
    const code = String(preview?.message_code || '').trim()
    switch (code) {
      case 'dust_change_absorbed':
        return t('lightningOps.openPreviewDustChange')
      case 'insufficient_confirmed_balance':
      case 'insufficient_balance_for_fees':
        return t('lightningOps.batchOpenPreviewInsufficient')
      case 'no_confirmed_utxos':
        return t('lightningOps.batchOpenPreviewNoConfirmedUtxos')
      case 'no_channels':
        return t('lightningOps.batchOpenNoItems')
      default:
        return String(preview?.message || '').trim()
    }
  }

  const handleOpenChannel = async () => {
    setOpenStatus(t('lightningOps.openingChannel'))
    setOpenChannelPoint('')
    const localFunding = Number(openAmount || 0)
    const pushRaw = openPushSat.trim()
    const pushSat = pushRaw === '' ? 0 : Number(pushRaw)
    const feeRate = Number(openFeeRate || 0)
    if (!openPeer.trim()) {
      setOpenStatus(t('lightningOps.peerAddressRequired'))
      return
    }
    if (localFunding < 20000) {
      setOpenStatus(t('lightningOps.minimumChannelSize'))
      return
    }
    if (!Number.isFinite(pushSat) || pushSat < 0) {
      setOpenStatus(t('lightningOps.pushAmountInvalid'))
      return
    }
    if (pushSat > localFunding) {
      setOpenStatus(t('lightningOps.pushAmountExceedsFunding'))
      return
    }
    if (openFeeMode === 'manual' && feeRate <= 0) {
      setOpenStatus(t('lightningOps.openManualFeeRequired'))
      return
    }
    try {
      const res = await openChannel({
        peer_address: openPeer.trim(),
        local_funding_sat: localFunding,
        push_sat: pushSat > 0 ? pushSat : undefined,
        close_address: openCloseAddress.trim() || undefined,
        sat_per_vbyte: openFeeMode === 'manual' && feeRate > 0 ? feeRate : undefined,
        private: openPrivate
      })
      setOpenStatus(t('lightningOps.channelOpeningSubmitted'))
      setOpenChannelPoint(res?.channel_point || '')
      setOpenAmount('')
      setOpenPushSat('')
      setOpenCloseAddress('')
      load()
    } catch (err: any) {
      setOpenStatus(err?.message || t('lightningOps.channelOpenFailed'))
    }
  }

  const handleBatchAddItem = async () => {
    if (batchBusy) return
    const parsed = parseBatchPeerAddress(batchPeer)
    if (parsed.error) {
      setBatchStatus(parsed.error)
      return
    }
    const amount = Number(batchAmount || 0)
    if (amount < 20000) {
      setBatchStatus(t('lightningOps.minimumChannelSize'))
      return
    }
    const pubkey = parsed.pubkey.trim()
    const pubkeyKey = pubkey.toLowerCase()
    if (batchItems.some((item) => item.pubkey.toLowerCase() === pubkeyKey)) {
      setBatchStatus(t('lightningOps.batchOpenDuplicatePeer'))
      return
    }

    try {
      if (!connectedPubkeys.has(pubkeyKey)) {
        if (!parsed.host) {
          setBatchStatus(t('lightningOps.batchOpenHostRequired'))
          return
        }
        setBatchStatus(t('lightningOps.batchOpenConnectingPeer'))
        try {
          await connectPeer({ address: `${pubkey}@${parsed.host}`, perm: false })
        } catch (err: any) {
          const msg = String(err?.message || '').toLowerCase()
          const alreadyConnected = msg.includes('already connected') || msg.includes('already have a connection')
          if (!alreadyConnected) {
            throw err
          }
        }
      }

      const nextId = batchItemIdRef.current++
      setBatchItems((prev) => [
        ...prev,
        {
          id: nextId,
          pubkey,
          host: parsed.host,
          local_funding_sat: amount,
          private: batchPrivate,
          close_address: batchCloseAddress.trim() || undefined,
        },
      ])
      setBatchPeer('')
      setBatchAmount('')
      setBatchCloseAddress('')
      setBatchPrivate(false)
      setBatchStatus(t('lightningOps.batchOpenItemAdded'))
      load()
    } catch (err: any) {
      setBatchStatus(err?.message || t('lightningOps.peerConnectFailed'))
    }
  }

  const handleBatchRemoveItem = (id: number) => {
    setBatchItems((prev) => prev.filter((item) => item.id !== id))
  }

  const handleBatchOpenChannels = async () => {
    if (batchBusy) return
    if (!batchItems.length) {
      setBatchStatus(t('lightningOps.batchOpenNoItems'))
      return
    }
    setBatchBusy(true)
    setBatchStatus(t('lightningOps.batchOpenRunning'))
    setBatchChannelPoints([])
    try {
      const feeRate = Number(batchFeeRate || 0)
      if (batchFeeMode === 'manual' && feeRate <= 0) {
        setBatchStatus(t('lightningOps.batchOpenManualFeeRequired'))
        setBatchBusy(false)
        return
      }
      const channelsPayload = batchItems.map((item) => {
        const hasHost = Boolean(item.host?.trim())
        const payload: any = {
          pubkey: item.pubkey,
          local_funding_sat: item.local_funding_sat,
          private: item.private,
        }
        if (hasHost) {
          payload.host = item.host?.trim()
          payload.peer_address = `${item.pubkey}@${item.host?.trim()}`
        } else {
          payload.peer_address = item.pubkey
        }
        if (item.close_address?.trim()) {
          payload.close_address = item.close_address.trim()
        }
        return payload
      })
      const res: any = await openBatchChannels({
        channels: channelsPayload,
        sat_per_vbyte: batchFeeMode === 'manual' && feeRate > 0 ? feeRate : undefined,
      })
      const points = Array.isArray(res?.pending_channels)
        ? res.pending_channels
            .map((entry: any) => String(entry?.channel_point || '').trim())
            .filter((value: string) => value.length > 0)
        : []
      setBatchChannelPoints(points)
      setBatchStatus(t('lightningOps.batchOpenSubmitted', { count: Number(res?.count || points.length || batchItems.length) }))
      setBatchItems([])
      load()
    } catch (err: any) {
      setBatchStatus(err?.message || t('lightningOps.channelOpenFailed'))
    } finally {
      setBatchBusy(false)
    }
  }

  const handleBalancedOpenCreateAndPropose = async () => {
    if (balancedOpenBusy) return

    const parsed = parseBatchPeerAddress(balancedPeer)
    if (parsed.error) {
      setBalancedOpenStatus(parsed.error)
      return
    }

    const capacity = Number(balancedCapacity || 0)
    if (capacity < 20000) {
      setBalancedOpenStatus(t('lightningOps.minimumChannelSize'))
      return
    }
    if (capacity % 2 !== 0) {
      setBalancedOpenStatus(t('lightningOps.balancedOpenCapacityEven'))
      return
    }

    const feeRate = Number(balancedFeeRate || 0)
    const pubkey = parsed.pubkey.trim()
    const pubkeyKey = pubkey.toLowerCase()
    const hasHost = Boolean(parsed.host?.trim())

    if (!connectedPubkeys.has(pubkeyKey) && !hasHost) {
      setBalancedOpenStatus(t('lightningOps.batchOpenHostRequired'))
      return
    }

    setBalancedOpenBusy(true)
    setBalancedOpenStatus(t('lightningOps.balancedOpenCreating'))
    try {
      if (!connectedPubkeys.has(pubkeyKey) && hasHost) {
        setBalancedOpenStatus(t('lightningOps.batchOpenConnectingPeer'))
        try {
          await connectPeer({ address: `${pubkey}@${parsed.host}`, perm: false })
        } catch (err: any) {
          const msg = String(err?.message || '').toLowerCase()
          const alreadyConnected = msg.includes('already connected') || msg.includes('already have a connection')
          if (!alreadyConnected) {
            throw err
          }
        }
      }

      const peerAddress = hasHost ? `${pubkey}@${parsed.host}` : pubkey
      const created = await createBalancedOpenSession({
        peer_address: peerAddress,
        capacity_sat: capacity,
        fee_rate_sat_vb: feeRate > 0 ? feeRate : undefined,
        private: balancedPrivate,
        close_address: balancedCloseAddress.trim() || undefined,
        role: 'initiator',
      }) as any

      const sessionID = String(created?.session_id || '').trim()
      if (!sessionID) {
        throw new Error(t('lightningOps.balancedOpenCreateFailed'))
      }

      setBalancedOpenStatus(t('lightningOps.balancedOpenProposing'))
      await proposeBalancedOpenSession(sessionID)
      setBalancedOpenStatus(t('lightningOps.balancedOpenProposalSent'))
      setBalancedPeer('')
      setBalancedCapacity('')
      setBalancedCloseAddress('')
      setBalancedPrivate(false)
      await refreshBalancedOpen()
      load()
    } catch (err: any) {
      setBalancedOpenStatus(err?.message || t('lightningOps.balancedOpenCreateFailed'))
    } finally {
      setBalancedOpenBusy(false)
    }
  }

  const handleBalancedOpenAction = async (session: BalancedOpenSession, action: 'propose' | 'accept' | 'execute' | 'retry_broadcast' | 'recover' | 'cancel') => {
    if (balancedOpenActionBusyID) return
    const sessionID = String(session.session_id || '').trim()
    if (!sessionID) return

    setBalancedOpenActionBusyID(`${action}:${sessionID}`)
    try {
      if (action === 'propose') {
        setBalancedOpenStatus(t('lightningOps.balancedOpenProposing'))
        await proposeBalancedOpenSession(sessionID)
        setBalancedOpenStatus(t('lightningOps.balancedOpenProposalSent'))
      } else if (action === 'accept') {
        setBalancedOpenStatus(t('lightningOps.balancedOpenAccepting'))
        await acceptBalancedOpenSession(sessionID)
        setBalancedOpenStatus(t('lightningOps.balancedOpenAccepted'))
      } else if (action === 'execute') {
        setBalancedOpenStatus(t('lightningOps.balancedOpenExecuting'))
        await executeBalancedOpenSession(sessionID)
        setBalancedOpenStatus(t('lightningOps.balancedOpenExecutionSubmitted'))
      } else if (action === 'retry_broadcast') {
        setBalancedOpenStatus(t('lightningOps.balancedOpenRetryBroadcasting'))
        await retryBalancedOpenSessionBroadcast(sessionID)
        setBalancedOpenStatus(t('lightningOps.balancedOpenRetryBroadcastSubmitted'))
      } else if (action === 'recover') {
        const confirmed = window.confirm(t('lightningOps.balancedOpenRecoverConfirm'))
        if (!confirmed) return
        setBalancedOpenStatus(t('lightningOps.balancedOpenRecovering'))
        await recoverBalancedOpenSession(sessionID, {})
        setBalancedOpenStatus(t('lightningOps.balancedOpenRecovered'))
      } else if (action === 'cancel') {
        const confirmed = window.confirm(t('lightningOps.balancedOpenCancelConfirm'))
        if (!confirmed) return
        setBalancedOpenStatus(t('lightningOps.balancedOpenCanceling'))
        await cancelBalancedOpenSession(sessionID)
        setBalancedOpenStatus(t('lightningOps.balancedOpenCanceled'))
      }

      await refreshBalancedOpen()
      load()
    } catch (err: any) {
      setBalancedOpenStatus(err?.message || t('lightningOps.balancedOpenActionFailed'))
    } finally {
      setBalancedOpenActionBusyID('')
    }
  }

  const fetchBalancedOpenEvents = async (sessionID: string) => {
    const id = String(sessionID || '').trim()
    if (!id) return

    setBalancedOpenEventsLoadingSessionID(id)
    setBalancedOpenEventsErrorBySession((prev) => ({ ...prev, [id]: '' }))
    try {
      const payload = await getBalancedOpenSessionEvents(id, { limit: 120 }) as any
      const items = Array.isArray(payload?.items) ? payload.items as BalancedOpenEvent[] : []
      setBalancedOpenEventsBySession((prev) => ({ ...prev, [id]: items }))
    } catch (err: any) {
      setBalancedOpenEventsErrorBySession((prev) => ({ ...prev, [id]: err?.message || t('lightningOps.balancedOpenEventsLoadFailed') }))
    } finally {
      setBalancedOpenEventsLoadingSessionID((current) => (current === id ? '' : current))
    }
  }

  const handleBalancedOpenToggleDetails = async (sessionID: string) => {
    const id = String(sessionID || '').trim()
    if (!id) return

    if (balancedOpenDetailsSessionID === id) {
      setBalancedOpenDetailsSessionID('')
      return
    }

    setBalancedOpenDetailsSessionID(id)
    const cached = balancedOpenEventsBySession[id]
    if (Array.isArray(cached) && cached.length > 0) return
    await fetchBalancedOpenEvents(id)
  }

  const mempoolLink = (channelPoint: string) => {
    const parts = channelPoint.split(':')
    if (parts.length !== 2) return ''
    const base = locale === 'pt-BR' ? 'https://mempool.space/pt' : 'https://mempool.space'
    return `${base}/tx/${parts[0]}#vout=${parts[1]}`
  }

  const channelPointTxid = (channelPoint?: string) => {
    const parts = String(channelPoint || '').trim().split(':')
    if (parts.length !== 2) return ''
    const txid = (parts[0] || '').trim()
    return /^[0-9a-fA-F]{64}$/.test(txid) ? txid : ''
  }

  const mempoolTxLink = (txid?: string) => {
    if (!txid) return ''
    const base = locale === 'pt-BR' ? 'https://mempool.space/pt' : 'https://mempool.space'
    return `${base}/tx/${txid}`
  }

  const handleCloseChannel = async () => {
    setCloseStatus(t('lightningOps.closingChannel'))
    if (!closePoint) {
      setCloseStatus(t('lightningOps.selectChannelToClose'))
      return
    }
    try {
      const feeRate = Number(closeFeeRate || 0)
      if (!closeForce && closeFeeMode === 'manual' && feeRate <= 0) {
        setCloseStatus(t('lightningOps.closeManualFeeRequired'))
        return
      }
      const res: any = await closeChannel({
        channel_point: closePoint,
        force: closeForce,
        sat_per_vbyte: closeForce || closeFeeMode === 'auto' ? undefined : feeRate
      })
      const closingTxid = String(res?.closing_txid || '').trim()
      if (closingTxid) {
        setClosingTxHints((prev) => ({ ...prev, [closePoint]: closingTxid }))
        setCloseStatus(t('lightningOps.closingTx', { txid: closingTxid }))
      } else {
        setCloseStatus(t('lightningOps.closeInitiated'))
      }
      load()
    } catch (err: any) {
      setCloseStatus(err?.message || t('lightningOps.closeFailed'))
    }
  }

  const handleForceClosePending = async (channelPoint: string) => {
    const point = (channelPoint || '').trim()
    if (!point) return
    const confirmed = window.confirm(t('lightningOps.forceCloseCardConfirm', { point }))
    if (!confirmed) return

    setPendingForceBusyByPoint((prev) => ({ ...prev, [point]: true }))
    setPendingForceStatusByPoint((prev) => ({ ...prev, [point]: t('lightningOps.forceCloseCardRunning') }))
    try {
      const res: any = await closeChannel({
        channel_point: point,
        force: true
      })
      const closingTxid = String(res?.closing_txid || '').trim()
      if (closingTxid) {
        setClosingTxHints((prev) => ({ ...prev, [point]: closingTxid }))
        setPendingForceStatusByPoint((prev) => ({ ...prev, [point]: t('lightningOps.forceCloseCardSubmittedTx', { txid: closingTxid }) }))
      } else {
        setPendingForceStatusByPoint((prev) => ({ ...prev, [point]: t('lightningOps.forceCloseCardSubmitted') }))
      }
      setClosePoint(point)
      setCloseForce(true)
      load()
    } catch (err: any) {
      setPendingForceStatusByPoint((prev) => ({ ...prev, [point]: err?.message || t('lightningOps.forceCloseCardFailed') }))
    } finally {
      setPendingForceBusyByPoint((prev) => ({ ...prev, [point]: false }))
    }
  }

  const handlePendingOpenBumpFee = async (channelPoint: string, preset: 'economic' | 'normal' | 'urgent', preview: { satPerVbyte: number; estimatedFeeSat: number }) => {
    const point = (channelPoint || '').trim()
    if (!point || pendingOpenBumpBusyByPoint[point]) return
    const confirmed = window.confirm(t('lightningOps.pendingOpenBumpConfirm', {
      preset: pendingOpenBumpPresetLabel(preset),
      rate: preview.satPerVbyte,
      fee: Math.max(0, Math.trunc(preview.estimatedFeeSat)).toLocaleString(),
    }))
    if (!confirmed) return

    setPendingOpenBumpBusyByPoint((prev) => ({ ...prev, [point]: true }))
    setPendingOpenBumpStatusByPoint((prev) => ({ ...prev, [point]: t('lightningOps.pendingOpenBumpRunning') }))
    try {
      const res: any = await bumpPendingOpenChannel({ channel_point: point, preset })
      const satPerVbyte = Math.max(0, Math.trunc(Number(res?.sat_per_vbyte || preview.satPerVbyte)))
      const estimatedFeeSat = Math.max(0, Math.trunc(Number(res?.estimated_fee_sat || preview.estimatedFeeSat)))
      setPendingOpenBumpStatusByPoint((prev) => ({
        ...prev,
        [point]: t('lightningOps.pendingOpenBumpDone', {
          preset: pendingOpenBumpPresetLabel(preset),
          rate: satPerVbyte,
          fee: estimatedFeeSat.toLocaleString(),
        }),
      }))
      load()
    } catch (err: any) {
      setPendingOpenBumpStatusByPoint((prev) => ({ ...prev, [point]: err?.message || t('lightningOps.pendingOpenBumpFailed') }))
    } finally {
      setPendingOpenBumpBusyByPoint((prev) => ({ ...prev, [point]: false }))
    }
  }

  const runFeeUpdate = async (payload: {
    applyAll: boolean
    channelPoint?: string
    baseFeeMsat: number
    feeRatePpm: number
    timeLockDelta: number
    inboundEnabled: boolean
    inboundBaseMsat: number
    inboundFeeRatePpm: number
    setStatus: (value: string) => void
  }) => {
    payload.setStatus(t('lightningOps.updatingFees'))
    if (!payload.applyAll && !payload.channelPoint) {
      payload.setStatus(t('lightningOps.selectChannelOrAll'))
      return false
    }
    const hasOutbound = payload.baseFeeMsat !== 0 || payload.feeRatePpm !== 0 || payload.timeLockDelta !== 0
    const hasInbound = payload.inboundEnabled && (payload.inboundBaseMsat !== 0 || payload.inboundFeeRatePpm !== 0)
    if (!hasOutbound && !hasInbound) {
      payload.setStatus(t('lightningOps.setAtLeastOneFee'))
      return false
    }
    try {
      const res = await updateChannelFees({
        apply_all: payload.applyAll,
        channel_point: payload.applyAll ? undefined : payload.channelPoint,
        base_fee_msat: payload.baseFeeMsat,
        fee_rate_ppm: payload.feeRatePpm,
        time_lock_delta: payload.timeLockDelta,
        inbound_enabled: payload.inboundEnabled,
        inbound_base_msat: payload.inboundBaseMsat,
        inbound_fee_rate_ppm: payload.inboundFeeRatePpm
      })
      payload.setStatus(res?.warning || t('lightningOps.feesUpdated'))
      load()
      return true
    } catch (err: any) {
      payload.setStatus(err?.message || t('lightningOps.feeUpdateFailed'))
      return false
    }
  }

  const clearCondensedFeeDraft = (channelPoint: string) => {
    setCondensedFeeDrafts((prev) => {
      const next = { ...prev }
      delete next[channelPoint]
      return next
    })
  }

  const flashCondensedFeeField = (channelPoint: string, tone: 'success' | 'error') => {
    setCondensedFeeFlashByPoint((prev) => ({ ...prev, [channelPoint]: tone }))
    if (typeof window === 'undefined') return
    const existingTimer = condensedFeeFlashTimersRef.current[channelPoint]
    if (existingTimer) {
      window.clearTimeout(existingTimer)
    }
    condensedFeeFlashTimersRef.current[channelPoint] = window.setTimeout(() => {
      setCondensedFeeFlashByPoint((prev) => {
        const next = { ...prev }
        delete next[channelPoint]
        return next
      })
      delete condensedFeeFlashTimersRef.current[channelPoint]
    }, 1600)
  }

  const handleCondensedOutRateSave = async (ch: Channel) => {
    const channelPoint = String(ch.channel_point || '').trim()
    if (!channelPoint || condensedFeeBusyByPoint[channelPoint]) return

    const rawDraft = condensedFeeDrafts[channelPoint]
    if (rawDraft === undefined) return

    if (String(rawDraft).trim() === '') {
      clearCondensedFeeDraft(channelPoint)
      flashCondensedFeeField(channelPoint, 'error')
      return
    }
    const nextRate = Number(rawDraft)
    if (!Number.isFinite(nextRate) || nextRate < 0 || !Number.isInteger(nextRate)) {
      clearCondensedFeeDraft(channelPoint)
      flashCondensedFeeField(channelPoint, 'error')
      return
    }

    const normalizedRate = Math.trunc(nextRate)
    const currentRate = typeof ch.fee_rate_ppm === 'number' && Number.isFinite(ch.fee_rate_ppm)
      ? Math.trunc(ch.fee_rate_ppm)
      : 0
    if (normalizedRate === currentRate) {
      clearCondensedFeeDraft(channelPoint)
      return
    }

    setCondensedFeeBusyByPoint((prev) => ({ ...prev, [channelPoint]: true }))
    try {
      const policy: any = await getLnChannelFees(channelPoint)
      const readPolicyInt = (value: unknown, fallback = 0) => {
        const parsed = Math.trunc(Number(value ?? fallback))
        return Number.isFinite(parsed) ? parsed : fallback
      }
      const baseFeeMsat = Math.max(0, readPolicyInt(policy?.base_fee_msat, ch.base_fee_msat ?? 0))
      const timeLockDelta = Math.max(0, readPolicyInt(policy?.time_lock_delta, 0))
      const inboundBaseMsat = readPolicyInt(policy?.inbound_base_msat, ch.inbound_base_msat ?? 0)
      const inboundFeeRatePpm = readPolicyInt(policy?.inbound_fee_rate_ppm, ch.inbound_fee_rate_ppm ?? 0)

      await updateChannelFees({
        apply_all: false,
        channel_point: channelPoint,
        base_fee_msat: baseFeeMsat,
        fee_rate_ppm: normalizedRate,
        time_lock_delta: timeLockDelta,
        inbound_enabled: true,
        inbound_base_msat: inboundBaseMsat,
        inbound_fee_rate_ppm: inboundFeeRatePpm
      })

      setChannels((prev) => prev.map((item) => (
        item.channel_point === channelPoint
          ? { ...item, fee_rate_ppm: normalizedRate, base_fee_msat: baseFeeMsat, inbound_base_msat: inboundBaseMsat, inbound_fee_rate_ppm: inboundFeeRatePpm }
          : item
      )))
      clearCondensedFeeDraft(channelPoint)
      flashCondensedFeeField(channelPoint, 'success')
      load()
    } catch {
      clearCondensedFeeDraft(channelPoint)
      flashCondensedFeeField(channelPoint, 'error')
    } finally {
      setCondensedFeeBusyByPoint((prev) => ({ ...prev, [channelPoint]: false }))
    }
  }

  const startInlineFeeEdit = async (ch: Channel) => {
    setInlineFeeChannelPoint(ch.channel_point)
    setInlineFeeRatePpm(String(ch.fee_rate_ppm ?? 0))
    setInlineBaseFeeMsat(String(ch.base_fee_msat ?? 0))
    setInlineInboundFeeRatePpm(String(ch.inbound_fee_rate_ppm ?? 0))
    setInlineInboundBaseMsat('0')
    setInlineTimeLockDelta('0')
    setInlineFeeStatus('')
    setInlineFeeLoading(true)
    try {
      const res = await getLnChannelFees(ch.channel_point)
      setInlineFeeRatePpm(String(res?.fee_rate_ppm ?? ch.fee_rate_ppm ?? 0))
      setInlineBaseFeeMsat(String(res?.base_fee_msat ?? ch.base_fee_msat ?? 0))
      setInlineInboundFeeRatePpm(String(res?.inbound_fee_rate_ppm ?? ch.inbound_fee_rate_ppm ?? 0))
      setInlineInboundBaseMsat(String(res?.inbound_base_msat ?? 0))
      setInlineTimeLockDelta(String(res?.time_lock_delta ?? 0))
    } catch (err: any) {
      setInlineFeeStatus(err?.message || t('lightningOps.loadFeesFailed'))
    } finally {
      setInlineFeeLoading(false)
    }
  }

  const cancelInlineFeeEdit = () => {
    setInlineFeeChannelPoint('')
    setInlineFeeStatus('')
    setInlineFeeLoading(false)
    setInlineFeeSaving(false)
  }

  const handleInlineFeeSave = async () => {
    if (!inlineFeeChannelPoint || inlineFeeLoading || inlineFeeSaving) return
    const outRate = Number(inlineFeeRatePpm)
    const outBase = Number(inlineBaseFeeMsat)
    const inRate = Number(inlineInboundFeeRatePpm)
    const inBase = Number(inlineInboundBaseMsat || 0)
    const delta = Number(inlineTimeLockDelta || 0)

    if (!Number.isFinite(outRate) || !Number.isFinite(outBase) || !Number.isFinite(inRate) || !Number.isFinite(inBase) || !Number.isFinite(delta)) {
      setInlineFeeStatus(t('lightningOps.setAtLeastOneFee'))
      return
    }
    if (outRate < 0 || outBase < 0 || delta < 0 || inBase < 0) {
      setInlineFeeStatus(t('lightningOps.setAtLeastOneFee'))
      return
    }
    if (inRate > 0) {
      setInlineFeeStatus(t('lightningOps.inboundMustBeNegative'))
      return
    }

    setInlineFeeSaving(true)
    const ok = await runFeeUpdate({
      applyAll: false,
      channelPoint: inlineFeeChannelPoint,
      baseFeeMsat: outBase,
      feeRatePpm: outRate,
      timeLockDelta: delta,
      inboundEnabled: true,
      inboundBaseMsat: inBase,
      inboundFeeRatePpm: inRate,
      setStatus: setInlineFeeStatus
    })
    setInlineFeeSaving(false)
    if (ok) {
      setInlineFeeChannelPoint('')
    }
  }

  const handleUpdateFees = async () => {
    const base = Number(baseFeeMsat || 0)
    const ppm = Number(feeRatePpm || 0)
    const delta = Number(timeLockDelta || 0)
    const inboundBase = Number(inboundBaseMsat || 0)
    const inboundRate = Number(inboundFeeRatePpm || 0)
    await runFeeUpdate({
      applyAll: feeScopeAll,
      channelPoint: feeScopeAll ? undefined : feeChannelPoint,
      baseFeeMsat: base,
      feeRatePpm: ppm,
      timeLockDelta: delta,
      inboundEnabled: inboundEnabled,
      inboundBaseMsat: inboundBase,
      inboundFeeRatePpm: inboundRate,
      setStatus: setFeeStatus
    })
  }

  const channelOptions = useMemo(() => {
    const shortenChannelPoint = (point: string) => {
      const [txid = '', vout = ''] = (point || '').split(':')
      if (txid.length <= 12) return point || ''
      const head = txid.slice(0, 6)
      const tail = txid.slice(-4)
      return vout !== '' ? `${head}…${tail}:${vout}` : `${head}…${tail}`
    }
    const formatCapacity = (sats: number) => {
      const value = Number(sats || 0)
      if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
      if (value >= 1_000) return `${(value / 1_000).toFixed(0)}k`
      return String(value)
    }
    return channels.map((ch) => {
      const alias = ch.peer_alias || ch.remote_pubkey.slice(0, 12)
      const shortPoint = shortenChannelPoint(ch.channel_point)
      const capacity = formatCapacity(ch.capacity_sat)
      return {
        value: ch.channel_point,
        label: `${alias} · ${shortPoint} · ${capacity} sats`,
      }
    })
  }, [channels])

  const selectedCloseChannel = useMemo(
    () => channels.find((ch) => ch.channel_point === closePoint) || null,
    [channels, closePoint],
  )

  const closePreview = useMemo(() => {
    if (!selectedCloseChannel || closeForce) return null

    const manualRate = Math.max(0, Math.trunc(Number(closeFeeRate || 0)))
    const autoRate = Math.max(
      0,
      Math.trunc(Number(closeFeeHint?.fastest || closeFeeHint?.hour || 0)),
    )
    const satPerVbyte = closeFeeMode === 'manual' ? manualRate : autoRate
    if (satPerVbyte <= 0) return null

    const localHasOutput = selectedCloseChannel.local_balance_sat > CLOSE_PREVIEW_DUST_LIMIT_SAT
    const remoteHasOutput = selectedCloseChannel.remote_balance_sat > CLOSE_PREVIEW_DUST_LIMIT_SAT
    const estimatedVbytes = localHasOutput && remoteHasOutput
      ? COOP_CLOSE_ESTIMATED_TWO_OUTPUT_VBYTES
      : COOP_CLOSE_ESTIMATED_ONE_OUTPUT_VBYTES
    const feeSat = estimatedVbytes * satPerVbyte
    const estimatedLocalAfterFeeSat = Math.max(0, selectedCloseChannel.local_balance_sat - feeSat)

    return {
      feeSat,
      satPerVbyte,
      estimatedVbytes,
      estimatedLocalAfterFeeSat,
      reference: closeFeeMode === 'manual' ? 'manual' : 'auto',
    }
  }, [selectedCloseChannel, closeForce, closeFeeRate, closeFeeHint, closeFeeMode])

  const handlePrepareCloseChannel = (channelPoint: string) => {
    const point = String(channelPoint || '').trim()
    if (!point) return
    setLightningToolsOpen(true)
    setClosePoint(point)
    setCloseStatus('')
    if (typeof window === 'undefined') return
    window.setTimeout(() => {
      closeCardRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      closeSelectRef.current?.focus()
    }, 80)
  }

  const handleToggleFCRiskFilter = () => {
    if (fcRiskOnly) {
      setFcRiskOnly(false)
      return
    }
    if (!fcRiskCount) return
    setFilter('inactive')
    setProfitFilter('all')
    setSearch('')
    setMinCapacity('')
    setShowPrivate(true)
    setFcRiskOnly(true)
    const firstRiskChannel = channels.find((ch) => isFCRiskChannel(ch))
    if (firstRiskChannel?.channel_point) {
      pendingScrollChannelRef.current = firstRiskChannel.channel_point
    }
  }

  const scrollToCloseRecovery = () => {
    setChannelsSubview('close_recovery')
    if (typeof window === 'undefined') return
    window.setTimeout(() => {
      const target = document.getElementById(CLOSE_RECOVERY_SECTION_ID)
      if (!target) return
      target.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }, 0)
  }

  const handleCloseRecoveryRecover = async (sessionID: number) => {
    if (closeRecoveryBusyByID[sessionID]) return
    setCloseRecoveryBusyByID((prev) => ({ ...prev, [sessionID]: true }))
    setCloseRecoveryActionStatusByID((prev) => ({ ...prev, [sessionID]: t('lightningOps.closeRecoveryRecoverRunning') }))
    try {
      const res: any = await recoverCloseManagerSession(sessionID)
      const result = String(res?.result || '').trim()
      const txid = String(res?.closing_txid || '').trim()
      setCloseRecoveryActionStatusByID((prev) => ({
        ...prev,
        [sessionID]: txid
          ? t('lightningOps.closeRecoveryRecoverDoneTx', { txid })
          : result
            ? t('lightningOps.closeRecoveryRecoverDoneResult', { value: result })
            : t('lightningOps.closeRecoveryRecoverDone')
      }))
      await refreshCloseRecovery({ quiet: true })
      const channelsPayload = await getLnChannels()
      applyChannelsPayload(channelsPayload)
    } catch (err: any) {
      setCloseRecoveryActionStatusByID((prev) => ({ ...prev, [sessionID]: err?.message || t('lightningOps.closeRecoveryRecoverFailed') }))
    } finally {
      setCloseRecoveryBusyByID((prev) => ({ ...prev, [sessionID]: false }))
    }
  }

  const handleCloseRecoveryForceClose = async (sessionID: number) => {
    if (closeRecoveryBusyByID[sessionID]) return
    const confirmed = window.confirm(t('lightningOps.closeRecoveryForceConfirm'))
    if (!confirmed) return
    setCloseRecoveryBusyByID((prev) => ({ ...prev, [sessionID]: true }))
    setCloseRecoveryActionStatusByID((prev) => ({ ...prev, [sessionID]: t('lightningOps.closeRecoveryForceRunning') }))
    try {
      const res: any = await forceCloseManagerSession(sessionID)
      const delegated = Boolean(res?.delegated)
      const txid = String(res?.closing_txid || '').trim()
      setCloseRecoveryActionStatusByID((prev) => ({
        ...prev,
        [sessionID]: delegated
          ? t('lightningOps.closeRecoveryForceDelegated')
          : txid
            ? t('lightningOps.closeRecoveryForceDoneTx', { txid })
            : t('lightningOps.closeRecoveryForceDone')
      }))
      await refreshCloseRecovery({ quiet: true })
      const channelsPayload = await getLnChannels()
      applyChannelsPayload(channelsPayload)
    } catch (err: any) {
      setCloseRecoveryActionStatusByID((prev) => ({ ...prev, [sessionID]: err?.message || t('lightningOps.closeRecoveryForceFailed') }))
    } finally {
      setCloseRecoveryBusyByID((prev) => ({ ...prev, [sessionID]: false }))
    }
  }

  const handleCloseRecoveryBumpFee = async (sessionID: number, preset: 'economic' | 'normal' | 'urgent') => {
    if (closeRecoveryBusyByID[sessionID]) return
    const presetLabelKey =
      preset === 'economic'
        ? 'lightningOps.closeRecoveryBumpEconomic'
        : preset === 'urgent'
          ? 'lightningOps.closeRecoveryBumpUrgent'
          : 'lightningOps.closeRecoveryBumpNormal'
    const confirmed = window.confirm(t('lightningOps.closeRecoveryBumpConfirm', { preset: t(presetLabelKey) }))
    if (!confirmed) return
    setCloseRecoveryBusyByID((prev) => ({ ...prev, [sessionID]: true }))
    setCloseRecoveryActionStatusByID((prev) => ({ ...prev, [sessionID]: t('lightningOps.closeRecoveryBumpRunning') }))
    try {
      const res: any = await bumpFeeCloseManagerSession(sessionID, { preset })
      const satPerVbyte = Number(res?.sat_per_vbyte || 0)
      const outpointsBumped = Number(res?.outpoints_bumped || 0)
      setCloseRecoveryActionStatusByID((prev) => ({
        ...prev,
        [sessionID]: t('lightningOps.closeRecoveryBumpDone', {
          preset: t(presetLabelKey),
          fee: satPerVbyte > 0 ? satPerVbyte.toLocaleString() : '?',
          count: outpointsBumped
        })
      }))
      await refreshCloseRecovery({ quiet: true })
      const channelsPayload = await getLnChannels()
      applyChannelsPayload(channelsPayload)
    } catch (err: any) {
      setCloseRecoveryActionStatusByID((prev) => ({ ...prev, [sessionID]: err?.message || t('lightningOps.closeRecoveryBumpFailed') }))
    } finally {
      setCloseRecoveryBusyByID((prev) => ({ ...prev, [sessionID]: false }))
    }
  }

  const renderToolGlyph = (kind: string) => {
    let paths: JSX.Element
    switch (kind) {
      case 'peer':
        paths = <><circle cx="8" cy="8" r="3" /><path d="M14 20a6 6 0 0 0-12 0" /><path d="M18 7v6" /><path d="M15 10h6" /></>
        break
      case 'open':
        paths = <><path d="M5 12h14" /><path d="M13 6l6 6-6 6" /><path d="M5 5v14" /></>
        break
      case 'close':
        paths = <><circle cx="12" cy="12" r="8" /><path d="M8 8l8 8" /><path d="M16 8l-8 8" /></>
        break
      case 'fees':
        paths = <><path d="M5 7h14" /><path d="M5 12h14" /><path d="M5 17h14" /><circle cx="9" cy="7" r="1.5" /><circle cx="15" cy="12" r="1.5" /><circle cx="11" cy="17" r="1.5" /></>
        break
      case 'batch':
        paths = <><rect x="4" y="4" width="6" height="6" rx="1" /><rect x="14" y="4" width="6" height="6" rx="1" /><rect x="4" y="14" width="6" height="6" rx="1" /><rect x="14" y="14" width="6" height="6" rx="1" /></>
        break
      case 'balance':
        paths = <><path d="M12 4v16" /><path d="M5 8h14" /><path d="M7 8l-3 5h6l-3-5z" /><path d="M17 8l-3 5h6l-3-5z" /><path d="M8 20h8" /></>
        break
      case 'tower':
        paths = <><path d="M12 3l5 18H7l5-18z" /><path d="M9 12h6" /><path d="M10 8h4" /></>
        break
      case 'pulse':
        paths = <><path d="M4 13h4l2-6 4 10 2-4h4" /><path d="M4 19h16" /></>
        break
      case 'heal':
        paths = <><path d="M12 5v14" /><path d="M5 12h14" /><circle cx="12" cy="12" r="8" /></>
        break
      case 'tor':
        paths = <><circle cx="12" cy="12" r="8" /><path d="M12 4v16" /><path d="M4 12h16" /><path d="M7 7c3 2 7 2 10 0" /><path d="M7 17c3-2 7-2 10 0" /></>
        break
      case 'clean':
        paths = <><path d="M6 19h12" /><path d="M8 15h8" /><path d="M10 5h4l1 10H9l1-10z" /></>
        break
      case 'sign':
        paths = <><path d="M5 19l4-1 9-9-3-3-9 9-1 4z" /><path d="M13 6l3 3" /></>
        break
      default:
        paths = <><circle cx="12" cy="12" r="8" /><path d="M12 8v8" /><path d="M8 12h8" /></>
    }
    return (
      <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        {paths}
      </svg>
    )
  }

  const scrollToLightningTool = (sectionID: string, focusTarget?: HTMLElement | null) => {
    setLightningToolsOpen(true)
    if (typeof window === 'undefined') return
    window.setTimeout(() => {
      const target = document.getElementById(sectionID)
      target?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      focusTarget?.focus()
    }, 80)
  }

  const renderToolShortcut = (sectionID: string, label: string, kind: string, focusTarget?: HTMLElement | null) => (
    <button
      key={sectionID}
      type="button"
      className="inline-flex h-10 items-center gap-2 rounded-full border border-white/10 bg-ink/60 px-3 text-xs text-fog/75 transition hover:border-sky-300/60 hover:text-sky-100"
      onClick={() => scrollToLightningTool(sectionID, focusTarget)}
      title={label}
      aria-label={label}
    >
      {renderToolGlyph(kind)}
      <span className="hidden md:inline">{label}</span>
    </button>
  )

  const channelsListViewportLimitPx = Math.max(240, Math.floor(viewportSize.height * (viewportSize.width >= 1280 ? 0.78 : 0.7)))
  const channelsListEstimatedHeightPx = channelsViewMode === 'condensed'
    ? 34 + (filteredChannels.length * 54)
    : Math.max(0, (filteredChannels.length * 182) - 12)
  const channelsListDefaultHeightClass = channelsListEstimatedHeightPx > channelsListViewportLimitPx
    ? 'h-[70vh] xl:h-[78vh]'
    : ''
  const channelsListSizeKey = channelsListDefaultHeightClass ? 'limited' : 'natural'

  return (
    <section className="space-y-6">
      <div className="section-card">
        <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4">
          <div>
            <h2 className="text-2xl font-semibold">{t('lightningOps.title')}</h2>
            <p className="text-fog/60">{t('lightningOps.subtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <div className="rounded-full border border-white/10 bg-ink/60 px-3 py-1.5 text-[11px] text-fog/70 sm:text-xs sm:px-4 sm:py-2">
              {t('lightningOps.active')}: <span className="text-fog">{activeCount}</span>
            </div>
            <div className="rounded-full border border-white/10 bg-ink/60 px-3 py-1.5 text-[11px] text-fog/70 sm:text-xs sm:px-4 sm:py-2">
              {t('lightningOps.inactive')}: <span className="text-fog">{inactiveCount}</span>
            </div>
            <div className="rounded-full border border-glow/30 bg-glow/10 px-3 py-1.5 text-[11px] text-glow sm:text-xs sm:px-4 sm:py-2">
              {t('lightningOps.opening')}: <span className="text-fog">{pendingOpenCount}</span>
            </div>
            <div className="rounded-full border border-ember/30 bg-ember/10 px-3 py-1.5 text-[11px] text-ember sm:text-xs sm:px-4 sm:py-2">
              {t('lightningOps.closing')}: <span className="text-fog">{pendingCloseCount}</span>
            </div>
            <button className="btn-secondary text-[11px] px-3 py-2 sm:text-xs" onClick={load}>
              {t('common.refresh')}
            </button>
          </div>
        </div>
        {status && <p className="mt-4 text-sm text-brass">{status}</p>}
        {chanStatusMessage && <p className="mt-2 text-sm text-brass">{chanStatusMessage}</p>}
      </div>

      <div className="flex flex-col gap-6">
      <div id={AUTOFEE_SECTION_ID} className="section-card order-2 space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="space-y-3">
            <h3 className="text-lg font-semibold">{t('lightningOps.channels')}</h3>
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <button
                className={channelsSubview === 'channels' ? 'btn-primary' : 'btn-secondary'}
                onClick={() => setChannelsSubview('channels')}
                type="button"
              >
                {t('lightningOps.channelsSubviewChannels')}
              </button>
              <button
                className={channelsSubview === 'close_recovery' ? 'btn-primary' : 'btn-secondary'}
                onClick={() => setChannelsSubview('close_recovery')}
                type="button"
              >
                {t('lightningOps.channelsSubviewCloseRecovery')}
                {closeRecoveryActiveSessions.length > 0 ? `: ${closeRecoveryActiveSessions.length}` : ''}
              </button>
            </div>
          </div>
          {channelsSubview === 'channels' ? (
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <button className={filter === 'all' && !fcRiskOnly ? 'btn-primary' : 'btn-secondary'} onClick={() => { setFilter('all'); setFcRiskOnly(false) }}>{t('common.all')}</button>
              <button className={filter === 'active' && !fcRiskOnly ? 'btn-primary' : 'btn-secondary'} onClick={() => { setFilter('active'); setFcRiskOnly(false) }}>{t('common.active')}</button>
              <button className={filter === 'inactive' && !fcRiskOnly ? 'btn-primary' : 'btn-secondary'} onClick={() => { setFilter('inactive'); setFcRiskOnly(false) }}>{t('common.inactive')}</button>
              <button className={profitFilter === 'profitable' && !fcRiskOnly ? 'btn-primary' : 'btn-secondary'} onClick={() => { setProfitFilter((prev) => (prev === 'profitable' ? 'all' : 'profitable')); setFcRiskOnly(false) }}>
                {t('lightningOps.profitPositive')}: {profitCounts.profitable}
              </button>
              <button className={profitFilter === 'neutral' && !fcRiskOnly ? 'btn-primary' : 'btn-secondary'} onClick={() => { setProfitFilter((prev) => (prev === 'neutral' ? 'all' : 'neutral')); setFcRiskOnly(false) }}>
                {t('lightningOps.profitNeutral')}: {profitCounts.neutral}
              </button>
              <button className={profitFilter === 'deficit' && !fcRiskOnly ? 'btn-primary' : 'btn-secondary'} onClick={() => { setProfitFilter((prev) => (prev === 'deficit' ? 'all' : 'deficit')); setFcRiskOnly(false) }}>
                {t('lightningOps.profitNegative')}: {profitCounts.deficit}
              </button>
              <button
                className={`px-3 py-2 rounded-full border text-xs transition ${
                  fcRiskOnly
                    ? 'border-rose-300/70 bg-rose-500/25 text-rose-100'
                    : 'border-rose-500/45 bg-rose-500/10 text-rose-300 hover:border-rose-300/70'
                } ${!fcRiskCount && !fcRiskOnly ? 'opacity-60 cursor-not-allowed' : ''}`}
                onClick={handleToggleFCRiskFilter}
                disabled={!fcRiskCount && !fcRiskOnly}
                title={t('lightningOps.fcRiskFilterHint')}
                aria-label={t('lightningOps.fcRiskFilterHint')}
              >
                {t('lightningOps.fcRiskBadge', { count: fcRiskCount })}
              </button>
            </div>
          ) : (
            <div className="flex flex-wrap items-center gap-2 text-xs text-fog/70">
              <span className="rounded-full border border-white/10 px-3 py-2">
                {t('lightningOps.closeRecoveryActiveCount', { count: closeRecoveryStatusData?.active_count ?? closeRecoveryActiveSessions.length })}
              </span>
              <span className="rounded-full border border-white/10 px-3 py-2">
                {t('lightningOps.closeRecoveryActionRequiredCount', { count: closeRecoveryStatusData?.action_required_count ?? 0 })}
              </span>
            </div>
          )}
        </div>

        {channelsSubview === 'channels' && (pendingOpen.length > 0 || pendingClose.length > 0) && (
          <div className="rounded-2xl border border-brass/30 bg-brass/10 p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h4 className="text-sm font-semibold text-brass">{t('lightningOps.pendingChannels')}</h4>
              <div className="flex flex-wrap items-center gap-2">
                <p className="text-xs text-brass">
                  {t('lightningOps.opening')}: <span className="text-glow">{pendingOpen.length}</span> | {t('lightningOps.closing')}{' '}
                  <span className="text-ember">{pendingClose.length}</span>
                </p>
                {closeRecoveryActiveSessions.length > 0 && (
                  <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={scrollToCloseRecovery}>
                    {t('lightningOps.closeRecoveryOpen')}
                  </button>
                )}
              </div>
            </div>
            <div className="mt-3 grid gap-3 lg:grid-cols-2">
              <div className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex items-center justify-between gap-2">
                  <h5 className="text-xs font-semibold text-glow uppercase tracking-wide">{t('lightningOps.opening')}</h5>
                  <span className="rounded-full px-2 py-1 text-[11px] bg-glow/20 text-glow">{pendingOpen.length}</span>
                </div>
                {pendingOpen.length ? (
                  <div className="mt-3 space-y-3">
                    {pendingOpen.map((ch) => {
                      const pointLink = mempoolLink(ch.channel_point)
                      const openingTxid = channelPointTxid(ch.channel_point)
                      const openingTxLink = mempoolTxLink(openingTxid)
                      const stuckOpening = isPendingOpenStuck(ch)
                      const openingObserved = typeof ch.opening_duration_sec === 'number' && ch.opening_duration_sec > 0
                        ? formatCloseRecoveryAge(ch.opening_duration_sec)
                        : ''
                      const bumpChecked = ch.funding_bump_checked === true
                      const bumpEligible = ch.funding_bump_eligible === true
                      const bumpAmountSat = Math.max(0, Number(ch.funding_bump_amount_sat || 0))
                      const currentFundingFeeRate = Math.max(0, Math.trunc(Number(ch.funding_fee_rate_sat_vb || 0)))
                      const bumpStatus = pendingOpenBumpStatusByPoint[ch.channel_point] || ''
                      const bumpBusy = pendingOpenBumpBusyByPoint[ch.channel_point] === true
                      return (
                        <div key={ch.channel_point} className="rounded-xl border border-white/10 bg-ink/70 p-3">
                          <div className="flex flex-wrap items-center justify-between gap-3">
                            <div>
                              <p className="text-xs text-fog/70 break-all">{ch.peer_alias || ch.remote_pubkey || t('lightningOps.unknownPeer')}</p>
                              {peerProfileLinkGroup(ch.remote_pubkey)}
                              {pointLink ? (
                                <a
                                  className="mt-1 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                                  href={pointLink}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {t('lightningOps.pointLabel', { point: ch.channel_point })}
                                </a>
                              ) : (
                                <p className="mt-1 text-[11px] text-fog/50 break-all">
                                  {t('lightningOps.pointLabel', { point: ch.channel_point })}
                                </p>
                              )}
                            </div>
                            <span className="rounded-full px-2 py-1 text-[11px] bg-glow/20 text-glow">
                              {pendingStatusLabel(ch.status)}
                            </span>
                          </div>
                          <div className="mt-2 grid gap-2 lg:grid-cols-2 text-[11px] text-fog/60">
                            <div>{t('lightningOps.capacityLabel', { value: ch.capacity_sat })}</div>
                            {typeof ch.confirmations_until_active === 'number' && (
                              <div>{t('lightningOps.confirmationsLabel', { count: ch.confirmations_until_active })}</div>
                            )}
                            {openingObserved && (
                              <div>{t('lightningOps.pendingOpenObservedFor', { value: openingObserved })}</div>
                            )}
                          </div>
                          {stuckOpening && (
                            <div className="mt-3 rounded-xl border border-amber-300/35 bg-amber-500/10 p-3 space-y-1">
                              <div className="flex flex-wrap items-center justify-between gap-2">
                                <p className="text-[11px] font-semibold text-amber-100">{t('lightningOps.pendingOpenStuckTitle')}</p>
                                <span className="rounded-full border border-amber-300/35 px-2 py-0.5 text-[10px] text-amber-100">
                                  {t('lightningOps.pendingOpenStuckBadge')}
                                </span>
                              </div>
                              <p className="text-[11px] text-amber-200/90">{t('lightningOps.pendingOpenStuckBody')}</p>
                              {openingObserved && (
                                <p className="text-[11px] text-fog/70">{t('lightningOps.pendingOpenObservedFor', { value: openingObserved })}</p>
                              )}
                              <p className="text-[11px] text-fog/70">{t('lightningOps.pendingOpenMonitorMempool')}</p>
                              {currentFundingFeeRate > 0 && (
                                <p className="text-[11px] text-fog/70">{t('lightningOps.pendingOpenBumpCurrentFee', { value: currentFundingFeeRate })}</p>
                              )}
                              {bumpChecked ? (
                                bumpEligible ? (
                                  <div className="rounded-lg border border-emerald-300/25 bg-emerald-500/10 p-2">
                                    <p className="text-[11px] font-medium text-emerald-100">{t('lightningOps.pendingOpenBumpEligibleTitle')}</p>
                                    <p className="mt-1 text-[11px] text-emerald-100/85">{t('lightningOps.pendingOpenBumpEligibleBody')}</p>
                                    {bumpAmountSat > 0 && (
                                      <p className="mt-1 text-[11px] text-fog/75">
                                        {t('lightningOps.pendingOpenBumpEligibleAmount', { value: formatSatsValue(bumpAmountSat) })}
                                      </p>
                                    )}
                                    <div className="mt-2 flex flex-wrap gap-2">
                                      {(['economic', 'normal', 'urgent'] as const).map((preset) => {
                                        const preview = resolvePendingOpenBumpPreview(ch, preset)
                                        return (
                                          <button
                                            key={preset}
                                            type="button"
                                            className="btn-secondary px-3 py-1.5 text-[11px]"
                                            onClick={() => { void handlePendingOpenBumpFee(ch.channel_point, preset, preview) }}
                                            disabled={bumpBusy}
                                          >
                                            {bumpBusy
                                              ? t('lightningOps.pendingOpenBumpRunning')
                                              : `${pendingOpenBumpPresetLabel(preset)} · ${preview.satPerVbyte} sat/vB · ~${Math.max(0, Math.trunc(preview.estimatedFeeSat)).toLocaleString()} sats`}
                                          </button>
                                        )
                                      })}
                                    </div>
                                  </div>
                                ) : (
                                  <p className="text-[11px] text-fog/70">{pendingOpenBumpReasonLabel(ch.funding_bump_reason)}</p>
                                )
                              ) : (
                                <p className="text-[11px] text-fog/70">{t('lightningOps.pendingOpenBumpCheckUnavailable')}</p>
                              )}
                              {bumpStatus && (
                                <p className="text-[11px] text-fog/70">{bumpStatus}</p>
                              )}
                            </div>
                          )}
                          {openingTxid && (
                            openingTxLink ? (
                              <a
                                className="mt-2 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                                href={openingTxLink}
                                target="_blank"
                                rel="noopener noreferrer"
                              >
                                {t('lightningOps.fundingTx', { point: openingTxid })}
                              </a>
                            ) : (
                              <p className="mt-2 text-[11px] text-fog/50 break-all">{t('lightningOps.fundingTx', { point: openingTxid })}</p>
                            )
                          )}
                          {ch.private !== undefined && (
                            <p className="mt-2 text-[11px] text-fog/50">
                              {ch.private ? t('lightningOps.privateChannel') : t('lightningOps.publicChannel')}
                            </p>
                          )}
                        </div>
                      )
                    })}
                  </div>
                ) : (
                  <p className="mt-3 text-xs text-fog/60">{t('lightningOps.noChannelsOpening')}</p>
                )}
              </div>
              <div className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                <div className="flex items-center justify-between gap-2">
                  <h5 className="text-xs font-semibold text-ember uppercase tracking-wide">{t('lightningOps.closing')}</h5>
                  <span className="rounded-full px-2 py-1 text-[11px] bg-ember/20 text-ember">{pendingClose.length}</span>
                </div>
                {pendingClose.length ? (
                  <div className="mt-3 space-y-3">
                    {pendingClose.map((ch) => {
                      const pointLink = mempoolLink(ch.channel_point)
                      const maturitySeconds = estimateMaturitySeconds(ch.blocks_til_maturity)
                      const closingTxid = (ch.closing_txid || closingTxHints[ch.channel_point] || '').trim()
                      const closingTxLink = mempoolTxLink(closingTxid)
                      const waitingCloseRecovery = ch.waiting_close_recovery
                      const showWaitingCloseRecovery = ch.status === 'waiting_close' && !closingTxid
                      const recoveryAttempts = Math.max(0, Number(waitingCloseRecovery?.attempts || 0))
                      const recoveryResult = waitingCloseRecoveryResultLabel(waitingCloseRecovery?.last_result)
                      const recoveryLastAttempt = formatWaitingCloseRecoveryTime(waitingCloseRecovery?.last_attempt_at)
                      const recoveryLastError = String(waitingCloseRecovery?.last_error || '').trim()
                      const recoveryTxid = String(waitingCloseRecovery?.last_recovered_txid || '').trim()
                      const suggestForceClose = Boolean(waitingCloseRecovery?.suggest_force_close)
                      const forceBusy = Boolean(pendingForceBusyByPoint[ch.channel_point])
                      const forceStatus = pendingForceStatusByPoint[ch.channel_point] || ''
                      return (
                        <div key={ch.channel_point} className="rounded-xl border border-white/10 bg-ink/70 p-3">
                          <div className="flex flex-wrap items-center justify-between gap-3">
                            <div>
                              <p className="text-xs text-fog/70 break-all">{ch.peer_alias || ch.remote_pubkey || t('lightningOps.unknownPeer')}</p>
                              {peerProfileLinkGroup(ch.remote_pubkey)}
                              {pointLink ? (
                                <a
                                  className="mt-1 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                                  href={pointLink}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {t('lightningOps.pointLabel', { point: ch.channel_point })}
                                </a>
                              ) : (
                                <p className="mt-1 text-[11px] text-fog/50 break-all">
                                  {t('lightningOps.pointLabel', { point: ch.channel_point })}
                                </p>
                              )}
                            </div>
                            <span className="rounded-full px-2 py-1 text-[11px] bg-ember/20 text-ember">
                              {pendingStatusLabel(ch.status)}
                            </span>
                          </div>
                          <div className="mt-2 grid gap-2 lg:grid-cols-2 text-[11px] text-fog/60">
                            <div>{t('lightningOps.capacityLabel', { value: ch.capacity_sat })}</div>
                            {typeof ch.blocks_til_maturity === 'number' && (
                              <div className="space-y-1">
                                <div>{t('lightningOps.blocksToMaturity', { count: ch.blocks_til_maturity })}</div>
                                {maturitySeconds !== null && (
                                  <div className="text-fog/50">
                                    {t('lightningOps.maturityEta', { time: formatMaturityDuration(maturitySeconds) })}
                                  </div>
                                )}
                              </div>
                            )}
                          </div>
                          {closingTxid && (
                            closingTxLink ? (
                              <a
                                className="mt-2 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                                href={closingTxLink}
                                target="_blank"
                                rel="noopener noreferrer"
                              >
                                {t('lightningOps.closingTx', { txid: closingTxid })}
                              </a>
                            ) : (
                              <p className="mt-2 text-[11px] text-fog/50 break-all">{t('lightningOps.closingTx', { txid: closingTxid })}</p>
                            )
                          )}
                          {showWaitingCloseRecovery && (
                            <div className="mt-3 rounded-xl border border-amber-300/35 bg-amber-500/10 p-3 space-y-1">
                              <p className="text-[11px] font-semibold text-amber-100">{t('lightningOps.waitingCloseNoTxTitle')}</p>
                              <p className="text-[11px] text-amber-200/90">{t('lightningOps.waitingCloseAutoActions')}</p>
                              <p className="text-[11px] text-fog/70">{t('lightningOps.waitingCloseAttempts', { count: recoveryAttempts })}</p>
                              <p className="text-[11px] text-fog/70">{t('lightningOps.waitingCloseLastAttempt', { time: recoveryLastAttempt })}</p>
                              <p className="text-[11px] text-fog/70">{t('lightningOps.waitingCloseLastResult', { result: recoveryResult })}</p>
                              {recoveryLastError && (
                                <p className="text-[11px] text-rose-200 break-words">{t('lightningOps.waitingCloseLastError', { error: recoveryLastError })}</p>
                              )}
                              {recoveryTxid && (
                                <p className="text-[11px] text-fog/70 break-all">{t('lightningOps.waitingCloseRecoveredTx', { txid: recoveryTxid })}</p>
                              )}
                              <p className="text-[11px] text-amber-200/90">
                                {suggestForceClose ? t('lightningOps.waitingCloseSuggestForce') : t('lightningOps.waitingCloseKeepMonitoring')}
                              </p>
                              {suggestForceClose && (
                                <button
                                  type="button"
                                  className="btn-secondary text-xs px-3 py-1 mt-1"
                                  onClick={() => handleForceClosePending(ch.channel_point)}
                                  disabled={forceBusy}
                                >
                                  {forceBusy ? t('lightningOps.forceCloseCardRunning') : t('lightningOps.forceCloseNow')}
                                </button>
                              )}
                              {forceStatus && (
                                <p className="text-[11px] text-amber-100 break-words">{forceStatus}</p>
                              )}
                            </div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                ) : (
                  <p className="mt-3 text-xs text-fog/60">{t('lightningOps.noChannelsClosing')}</p>
                )}
              </div>
            </div>
          </div>
        )}

        {channelsSubview === 'close_recovery' && (
        <div id={CLOSE_RECOVERY_SECTION_ID} className="rounded-2xl border border-white/10 bg-ink/40 p-4 space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h4 className="text-sm font-semibold">{t('lightningOps.closeRecoveryTitle')}</h4>
              <p className="text-xs text-fog/60">{t('lightningOps.closeRecoverySubtitle')}</p>
            </div>
            <div className="flex flex-wrap items-center gap-2 text-[11px] text-fog/70">
              <span className="rounded-full border border-white/10 px-3 py-1.5">
                {t('lightningOps.closeRecoveryActiveCount', { count: closeRecoveryStatusData?.active_count ?? closeRecoveryActiveSessions.length })}
              </span>
              <span className="rounded-full border border-white/10 px-3 py-1.5">
                {t('lightningOps.closeRecoveryActionRequiredCount', { count: closeRecoveryStatusData?.action_required_count ?? 0 })}
              </span>
              {closeRecoveryStatusData?.node_retirement_count ? (
                <span className="rounded-full border border-white/10 px-3 py-1.5">
                  {t('lightningOps.closeRecoveryNodeRetirementCount', { count: closeRecoveryStatusData.node_retirement_count })}
                </span>
              ) : null}
            </div>
          </div>
          {closeRecoveryStatus && <p className="text-sm text-brass">{closeRecoveryStatus}</p>}
          {!closeRecoveryStatus && closeRecoveryGroups.length === 0 && (
            <p className="text-sm text-fog/60">{t('lightningOps.closeRecoveryEmpty')}</p>
          )}
          {closeRecoveryGroups.length > 0 && (
            <div className="space-y-4">
              {closeRecoveryGroups.map((group) => (
                <div key={group.key} className="space-y-3">
                  <div className="flex items-center justify-between gap-3">
                    <h5 className="text-xs font-semibold uppercase tracking-wide text-fog/70">{group.title}</h5>
                    <span className="rounded-full bg-white/5 px-2 py-1 text-[11px] text-fog/60">{group.items.length}</span>
                  </div>
                  <div className="grid gap-3 xl:grid-cols-2">
                    {group.items.map((item) => {
                      const pointLink = mempoolLink(item.channel_point)
                      const closeTxLink = mempoolTxLink(item.close_txid)
                      const sweepTxLink = mempoolTxLink(item.sweep_txid)

                      return (
                        <div key={item.id} className="rounded-xl border border-white/10 bg-ink/70 p-3 space-y-2">
                          <div className="flex flex-wrap items-start justify-between gap-3">
                            <div>
                              <p className="text-xs text-fog/70 break-all">{item.peer_alias || item.peer_pubkey || t('lightningOps.unknownPeer')}</p>
                              {peerProfileLinkGroup(item.peer_pubkey)}
                              {pointLink ? (
                                <a
                                  className="mt-1 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                                  href={pointLink}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {t('lightningOps.pointLabel', { point: item.channel_point })}
                                </a>
                              ) : (
                                <p className="mt-1 text-[11px] text-fog/50 break-all">{t('lightningOps.pointLabel', { point: item.channel_point })}</p>
                              )}
                            </div>
                            <div className="flex flex-wrap items-center justify-end gap-2">
                              {item.source === 'node_retirement' && (
                                <span className="rounded-full bg-sky-500/20 px-2 py-1 text-[11px] text-sky-100">
                                  {t('lightningOps.closeRecoverySourceNodeRetirement')}
                                </span>
                              )}
                              {group.key === 'recent' && item.close_mode && (
                                <span className={`rounded-full px-2 py-1 text-[11px] ${closeRecoveryModeBadgeClass(item.close_mode)}`}>
                                  {closeRecoveryModeLabel(item.close_mode)}
                                </span>
                              )}
                              <span className={`rounded-full px-2 py-1 text-[11px] ${closeRecoveryBadgeClass(item.risk_level)}`}>
                                {closeRecoveryStateLabel(item.state)}
                              </span>
                            </div>
                          </div>
                          <div className="grid gap-2 text-[11px] text-fog/65 md:grid-cols-2">
                            <div>{t('lightningOps.closeRecoveryDiagnostic')}: <span className="text-fog">{closeRecoveryStateLabel(item.state)}</span></div>
                            <div>{t('lightningOps.closeRecoveryNextAction')}: <span className="text-fog">{closeRecoveryActionLabel(item.action_recommended)}</span></div>
                            {group.key === 'recent' && item.close_mode && (
                              <div>{t('lightningOps.closeRecoveryCloseType', { value: closeRecoveryModeLabel(item.close_mode) })}</div>
                            )}
                            {typeof item.pending_htlc_count === 'number' && item.pending_htlc_count > 0 && (
                              <div>{t('lightningOps.closeRecoveryPendingHtlcCount', { count: item.pending_htlc_count })}</div>
                            )}
                            {typeof item.pending_htlc_age_sec === 'number' && item.pending_htlc_age_sec > 0 && (
                              <div>{t('lightningOps.closeRecoveryPendingHtlcAge', { value: formatCloseRecoveryAge(item.pending_htlc_age_sec) })}</div>
                            )}
                            {item.close_txid && (
                              closeTxLink ? (
                                <a
                                  className="md:col-span-2 text-emerald-200 hover:text-emerald-100 break-all"
                                  href={closeTxLink}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {t('lightningOps.closingTx', { txid: item.close_txid })}
                                </a>
                              ) : (
                                <div className="md:col-span-2">{t('lightningOps.closingTx', { txid: item.close_txid })}</div>
                              )
                            )}
                            {item.close_tx_external_confirmed && item.close_tx_external_block_time && (
                              <div>{t('lightningOps.closeRecoveryCloseTxConfirmedAt', { value: formatCloseRecoveryTime(item.close_tx_external_block_time) })}</div>
                            )}
                            {item.close_tx_external_seen && !item.close_tx_external_confirmed && (
                              <div>{t('lightningOps.closeRecoveryCloseTxSeen')}</div>
                            )}
                            {typeof item.limbo_balance_sat === 'number' && item.limbo_balance_sat > 0 && (
                              <div>{t('lightningOps.closeRecoveryRecoverableValue', { value: formatSatsValue(item.limbo_balance_sat) })}</div>
                            )}
                            {typeof item.blocks_til_maturity === 'number' && item.blocks_til_maturity > 0 && (
                              <div>{t('lightningOps.blocksToMaturity', { count: item.blocks_til_maturity })}</div>
                            )}
                            {typeof item.blocks_til_maturity === 'number' && item.blocks_til_maturity > 0 && (
                              <div>{t('lightningOps.closeRecoveryMaturityEta', { value: formatMaturityDuration(estimateMaturitySeconds(item.blocks_til_maturity)) })}</div>
                            )}
                            {(!item.blocks_til_maturity || item.blocks_til_maturity <= 0) && item.maturity_eta_at && (
                              <div>{t('lightningOps.closeRecoveryMaturityEta', { value: formatCloseRecoveryTime(item.maturity_eta_at) })}</div>
                            )}
                            {item.state === 'outputs_timelocked' && (!item.blocks_til_maturity || item.blocks_til_maturity <= 0) && !item.maturity_eta_at && (
                              <div>{t('lightningOps.closeRecoveryMaturityEtaUnavailable')}</div>
                            )}
                            {typeof item.sweep_pending_count === 'number' && item.sweep_pending_count > 0 && (
                              <div>{t('lightningOps.closeRecoverySweepCount', { count: item.sweep_pending_count })}</div>
                            )}
                            {typeof item.sweep_broadcast_attempts === 'number' && item.sweep_broadcast_attempts > 0 && (
                              <div>{t('lightningOps.closeRecoverySweepAttempts', { count: item.sweep_broadcast_attempts })}</div>
                            )}
                            {typeof item.sweep_fee_rate_sat_vb === 'number' && item.sweep_fee_rate_sat_vb > 0 && (
                              <div>{t('lightningOps.closeRecoverySweepFeeRate', { current: item.sweep_fee_rate_sat_vb.toLocaleString(), target: Math.max(0, Number(item.mempool_target_sat_vb || 0)).toLocaleString() })}</div>
                            )}
                            {!item.sweep_fee_rate_sat_vb && typeof item.sweep_requested_fee_rate_sat_vb === 'number' && item.sweep_requested_fee_rate_sat_vb > 0 && (
                              <div>{t('lightningOps.closeRecoverySweepRequestedFeeRate', { value: item.sweep_requested_fee_rate_sat_vb.toLocaleString() })}</div>
                            )}
                            {item.sweep_txid && (
                              sweepTxLink ? (
                                <a
                                  className="md:col-span-2 text-emerald-200 hover:text-emerald-100 break-all"
                                  href={sweepTxLink}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                >
                                  {t('lightningOps.closeRecoverySweepTx', { txid: item.sweep_txid })}
                                </a>
                              ) : (
                                <div className="md:col-span-2">{t('lightningOps.closeRecoverySweepTx', { txid: item.sweep_txid })}</div>
                              )
                            )}
                            {item.sweep_tx_external_confirmed && item.sweep_tx_external_block_time && (
                              <div>{t('lightningOps.closeRecoverySweepTxConfirmedAt', { value: formatCloseRecoveryTime(item.sweep_tx_external_block_time) })}</div>
                            )}
                            {item.sweep_tx_external_seen && !item.sweep_tx_external_confirmed && (
                              <div>{t('lightningOps.closeRecoverySweepTxSeen')}</div>
                            )}
                            {item.waiting_close_last_attempt_at && (
                              <div>{t('lightningOps.closeRecoveryLastAttempt', { value: formatCloseRecoveryTime(item.waiting_close_last_attempt_at) })}</div>
                            )}
                          </div>
                          {item.last_error && (
                            <p className="text-[11px] text-rose-200 break-words">
                              {t('lightningOps.closeRecoveryReason')}: {item.last_error}
                            </p>
                          )}
                          <div className="flex flex-wrap items-center gap-2">
                            {(item.state === 'waiting_close_no_txid' || item.action_recommended === 'recover_or_monitor') && (
                              <button
                                type="button"
                                className="btn-secondary text-xs px-3 py-1.5"
                                onClick={() => handleCloseRecoveryRecover(item.id)}
                                disabled={closeRecoveryBusyByID[item.id] === true}
                              >
                                {closeRecoveryBusyByID[item.id] ? t('lightningOps.closeRecoveryRecoverRunning') : t('lightningOps.closeRecoveryRecoverAction')}
                              </button>
                            )}
                            {(item.action_required === 'force_close' || item.action_recommended === 'force_close') && (
                              <button
                                type="button"
                                className="btn-secondary text-xs px-3 py-1.5"
                                onClick={() => handleCloseRecoveryForceClose(item.id)}
                                disabled={closeRecoveryBusyByID[item.id] === true}
                              >
                                {closeRecoveryBusyByID[item.id] ? t('lightningOps.closeRecoveryForceRunning') : t('lightningOps.closeRecoveryForceAction')}
                              </button>
                            )}
                            {typeof item.sweep_pending_count === 'number' && item.sweep_pending_count > 0 && (item.state === 'sweep_pending' || item.state === 'sweep_stuck') && (
                              <>
                                <button
                                  type="button"
                                  className="btn-secondary text-xs px-3 py-1.5"
                                  onClick={() => handleCloseRecoveryBumpFee(item.id, 'economic')}
                                  disabled={closeRecoveryBusyByID[item.id] === true}
                                >
                                  {closeRecoveryBusyByID[item.id] ? t('lightningOps.closeRecoveryBumpRunning') : t('lightningOps.closeRecoveryBumpEconomic')}
                                </button>
                                <button
                                  type="button"
                                  className="btn-secondary text-xs px-3 py-1.5"
                                  onClick={() => handleCloseRecoveryBumpFee(item.id, 'normal')}
                                  disabled={closeRecoveryBusyByID[item.id] === true}
                                >
                                  {closeRecoveryBusyByID[item.id] ? t('lightningOps.closeRecoveryBumpRunning') : t('lightningOps.closeRecoveryBumpNormal')}
                                </button>
                                <button
                                  type="button"
                                  className="btn-secondary text-xs px-3 py-1.5"
                                  onClick={() => handleCloseRecoveryBumpFee(item.id, 'urgent')}
                                  disabled={closeRecoveryBusyByID[item.id] === true}
                                >
                                  {closeRecoveryBusyByID[item.id] ? t('lightningOps.closeRecoveryBumpRunning') : t('lightningOps.closeRecoveryBumpUrgent')}
                                </button>
                              </>
                            )}
                          </div>
                          {closeRecoveryActionStatusByID[item.id] && (
                            <p className="text-[11px] text-brass break-words">{closeRecoveryActionStatusByID[item.id]}</p>
                          )}
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
        )}

        {channelsSubview === 'channels' && (
        <>
        <div className="grid gap-3 lg:grid-cols-6">
            <input
              className="input-field"
              placeholder={t('lightningOps.searchPlaceholder')}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <input
              className="input-field"
              placeholder={t('lightningOps.minCapacity')}
              type="number"
              min={0}
              value={minCapacity}
              onChange={(e) => setMinCapacity(e.target.value)}
            />
            <select className="input-field" value={sortBy} onChange={(e) => setSortBy(e.target.value as any)}>
            <option value="capacity">{t('lightningOps.sortByCapacity')}</option>
            <option value="local">{t('lightningOps.sortByLocal')}</option>
            <option value="remote">{t('lightningOps.sortByRemote')}</option>
            <option value="alias">{t('lightningOps.sortByPeer')}</option>
            </select>
            <select className="input-field" value={rankingFilter} onChange={(e) => setRankingFilter(e.target.value as any)}>
            <option value="all">{t('lightningOps.rankingFilterAll')}</option>
            <option value="expand">{t('lightningOps.rankingFilterExpand')}</option>
            <option value="maintain">{t('lightningOps.rankingFilterMaintain')}</option>
            <option value="monitor">{t('lightningOps.rankingFilterMonitor')}</option>
            <option value="close">{t('lightningOps.rankingFilterClose')}</option>
            </select>
            <select className="input-field" value={movementFilter} onChange={(e) => setMovementFilter(e.target.value as any)}>
            <option value="all">{t('lightningOps.movementFilterAll')}</option>
            <option value="low">{t('lightningOps.movementFilterLow')}</option>
            <option value="active">{t('lightningOps.movementFilterActive')}</option>
            <option value="none">{t('lightningOps.movementFilterNone')}</option>
            </select>
            <div className="flex flex-wrap items-center gap-2">
              <button className="btn-secondary text-xs px-3 py-2" onClick={() => setSortDir(sortDir === 'desc' ? 'asc' : 'desc')}>
              {sortDir === 'desc' ? t('lightningOps.sortDesc') : t('lightningOps.sortAsc')}
              </button>
              <button
                type="button"
                className={`inline-flex h-9 w-9 items-center justify-center rounded-full border transition ${
                  channelsViewMode === 'condensed'
                    ? 'border-sky-300/70 bg-sky-500/15 text-sky-100'
                    : 'border-white/15 bg-white/5 text-fog/70 hover:border-white/30 hover:text-fog'
                }`}
                onClick={() => setChannelsViewMode((mode) => (mode === 'condensed' ? 'full' : 'condensed'))}
                title={channelsViewMode === 'condensed' ? t('lightningOps.channelsViewFull') : t('lightningOps.channelsViewCondensed')}
                aria-label={channelsViewMode === 'condensed' ? t('lightningOps.channelsViewFull') : t('lightningOps.channelsViewCondensed')}
              >
                <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M4 6h16" />
                  <path d="M4 12h16" />
                  <path d="M4 18h16" />
                  <path d="M8 4v16" />
                  <path d="M16 4v16" />
                </svg>
              </button>
              <label className="flex items-center gap-2 text-[11px] text-fog/70 sm:text-xs">
                <input type="checkbox" checked={showPrivate} onChange={(e) => setShowPrivate(e.target.checked)} />
              {t('lightningOps.showPrivate')}
              </label>
              <label className="flex items-center gap-2 text-[11px] text-fog/70 sm:text-xs">
                <input
                  type="checkbox"
                  checked={autofeeAllChecked}
                  onChange={(e) => handleAutofeeBulk(e.target.checked)}
                  disabled={autofeeBusy}
                />
                {t('lightningOps.autofeeAll')}
              </label>
            </div>
          </div>
        {filteredChannels.length ? (
          channelsViewMode === 'condensed' ? (
            <div
              key={`channels-condensed-${channelsListSizeKey}`}
              className={`resize-y overflow-auto rounded-xl border border-white/10 bg-ink/50 ${channelsListDefaultHeightClass}`}
            >
              <div className="min-w-[1180px]">
                <table className="w-full text-left text-[11px]">
                <thead className="sticky top-0 z-10 bg-ink/95 text-[10px] uppercase tracking-wide text-fog/55 backdrop-blur">
                  <tr>
                    <th className="px-3 py-2">{t('lightningOps.condensedChannel')}</th>
                    <th className="px-3 py-2 text-center">{t('lightningOps.condensedLiquidity')}</th>
                    <th className="px-3 py-2">{t('lightningOps.pendingHtlcsTitle')}</th>
                    <th className="px-3 py-2 text-center">{t('lightningOps.economic7d')}</th>
                    <th className="border-l border-white/5 px-4 py-2 text-center">{t('lightningOps.outRate')}</th>
                    <th className="px-4 py-2 text-center">{t('lightningOps.outBase')}</th>
                    <th className="px-4 py-2 text-center">{t('lightningOps.inRate')}</th>
                    <th className="px-4 py-2 text-center">{t('lightningOps.inBase')}</th>
                    <th className="px-4 py-2 text-center">{t('lightningOps.condensedPeerFee')}</th>
                    <th className="px-3 py-2 text-center">{t('lightningOps.condensedRebalance')}</th>
                    <th className="px-3 py-2 text-center">{t('lightningOps.autofeeLabel')}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredChannels.map((ch) => {
                    const localDisabled = ch.local_disabled ?? isLocalChanDisabled(ch.chan_status_flags)
                    const autofeeHistoryKey = autofeeChannelKey(ch.channel_point, ch.channel_id)
                    const autofeeChecked = autofeeHistoryKey ? (autofeeSettings[autofeeHistoryKey] ?? true) : true
                    const visualTotal = (ch.local_balance_sat + ch.remote_balance_sat) > 0
                      ? (ch.local_balance_sat + ch.remote_balance_sat)
                      : (ch.capacity_sat > 0 ? ch.capacity_sat : 0)
                    const localPctRaw = visualTotal > 0 ? (ch.local_balance_sat / visualTotal) * 100 : 0
                    const remotePctRaw = visualTotal > 0 ? (ch.remote_balance_sat / visualTotal) * 100 : 0
                    const localPct = Math.max(0, Math.min(100, localPctRaw))
                    const remotePct = Math.max(0, Math.min(100, remotePctRaw))
                    const unsettledBalanceSat = typeof ch.unsettled_balance_sat === 'number' && Number.isFinite(ch.unsettled_balance_sat)
                      ? Math.max(0, Math.round(ch.unsettled_balance_sat))
                      : 0
                    const pendingHtlcCount = typeof ch.pending_htlc_count === 'number' && Number.isFinite(ch.pending_htlc_count)
                      ? Math.max(0, Math.round(ch.pending_htlc_count))
                      : 0
                    const pendingHtlcs = Array.isArray(ch.pending_htlcs) ? ch.pending_htlcs : []
                    const hasPendingHtlcs = pendingHtlcs.length > 0
                    const pendingHtlcOpen = pendingHtlcOpenChannelPoint === ch.channel_point
                    const isFCRisk = !ch.active && unsettledBalanceSat > 0
                    const marginPpm7d = typeof ch.out_ppm7d === 'number' && typeof ch.rebal_ppm7d === 'number'
                      ? ch.out_ppm7d - ch.rebal_ppm7d
                      : undefined
                    const rebalanceLink = ch.channel_point
                      ? buildHashWithChannelPoint(REBALANCE_ROUTE_KEY, ch.channel_point)
                      : `#${REBALANCE_ROUTE_KEY}`
                    const channelIDValue = channelIDText(ch)
                    const channelIDCopied = copiedChannelIDKey === (ch.channel_point || channelIDValue)
                    const feeDraft = condensedFeeDrafts[ch.channel_point]
                    const condensedFeeValue = feeDraft ?? (typeof ch.fee_rate_ppm === 'number' ? String(ch.fee_rate_ppm) : '')
                    const condensedFeeBusy = condensedFeeBusyByPoint[ch.channel_point] === true
                    const condensedFeeFlash = condensedFeeFlashByPoint[ch.channel_point]
                    const condensedFeeInputClass = condensedFeeFlash === 'success'
                      ? 'border-emerald-300/70 bg-emerald-500/20 text-emerald-50'
                      : condensedFeeFlash === 'error'
                        ? 'border-amber-300/70 bg-amber-500/20 text-amber-50'
                        : 'border-white/10 bg-ink/80 text-fog'
                    const rowTone = isFCRisk
                      ? 'border-rose-400/25 bg-rose-500/10'
                      : localDisabled && ch.active
                        ? 'border-ember/25 bg-ember/10'
                        : 'border-white/5'

                    return (
                      <Fragment key={ch.channel_point}>
                        <tr id={channelCardID(ch.channel_point)} className={`border-t align-top ${rowTone}`}>
                          <td className="px-3 py-2 align-middle">
                            <div className="max-w-[220px]">
                              <div className="flex items-center gap-1.5">
                                <span className={`h-2 w-2 shrink-0 rounded-full ${ch.active ? 'bg-glow' : isFCRisk ? 'bg-rose-300' : 'bg-ember'}`} />
                                <span className="truncate text-xs text-fog" title={ch.peer_alias || ch.remote_pubkey || t('lightningOps.unknownPeer')}>
                                  {ch.peer_alias || ch.remote_pubkey || t('lightningOps.unknownPeer')}
                                </span>
                                {channelIDValue && (
                                  <button
                                    type="button"
                                    className={`inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full border transition ${
                                      channelIDCopied
                                        ? 'border-glow/60 bg-glow/15 text-glow'
                                        : 'border-white/10 text-fog/35 hover:border-white/25 hover:text-fog/75'
                                    }`}
                                    title={channelIDCopied ? t('common.copied') : t('lightningOps.copyChannelId')}
                                    aria-label={channelIDCopied ? t('common.copied') : t('lightningOps.copyChannelId')}
                                    onClick={() => { void handleCopyChannelID(ch) }}
                                  >
                                    <svg viewBox="0 0 24 24" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="1.8">
                                      {channelIDCopied ? (
                                        <path d="M5 12.5l4 4L19 7" />
                                      ) : (
                                        <>
                                          <path d="M4 7h16" />
                                          <path d="M4 12h16" />
                                          <path d="M4 17h16" />
                                          <path d="M8 4L6 20" />
                                          <path d="M18 4l-2 16" />
                                        </>
                                      )}
                                    </svg>
                                  </button>
                                )}
                              </div>
                              {peerProfileLinkGroup(ch.remote_pubkey, 'mt-1 flex flex-wrap items-center gap-2')}
                            </div>
                          </td>
                          <td className="px-3 py-2 align-middle">
                            <div className="mx-auto w-[230px]">
                              <div className="flex items-center justify-between gap-3 text-[11px] text-fog">
                                <span>{formatSatsValue(ch.local_balance_sat)}</span>
                                <span>{formatSatsValue(ch.remote_balance_sat)}</span>
                              </div>
                              <div className="mt-1 flex items-center gap-2">
                                <span className="w-9 text-right text-[10px] text-glow">{localPct.toFixed(0)}%</span>
                                <div className="relative h-1.5 flex-1 overflow-hidden rounded-full bg-white/10">
                                  <div className="absolute inset-y-0 left-0 bg-glow/70" style={{ width: `${localPct}%` }} />
                                  <div className="absolute inset-y-0 right-0 bg-white/35" style={{ width: `${remotePct}%` }} />
                                </div>
                                <span className="w-9 text-left text-[10px] text-fog/55">{remotePct.toFixed(0)}%</span>
                              </div>
                              <div className="mt-1 text-center text-[10px] text-fog/50">{formatSatsValue(ch.capacity_sat)}</div>
                            </div>
                          </td>
                          <td className="px-3 py-2 whitespace-nowrap">
                            <button
                              type="button"
                              className={`text-left ${hasPendingHtlcs ? 'hover:text-white hover:underline underline-offset-2' : 'cursor-default'} ${unsettledBalanceSat > 0 || pendingHtlcCount > 0 ? 'text-brass' : 'text-fog/65'}`}
                              onClick={() => {
                                if (!hasPendingHtlcs) return
                                setPendingHtlcOpenChannelPoint((current) => current === ch.channel_point ? '' : ch.channel_point)
                              }}
                              disabled={!hasPendingHtlcs}
                              aria-expanded={pendingHtlcOpen}
                            >
                              <div>{formatSatsValue(unsettledBalanceSat)}</div>
                              <div className="text-[10px]">{pendingHtlcCount} HTLC</div>
                            </button>
                          </td>
                          <td className="px-3 py-2 text-center">
                            <div className="mx-auto flex min-w-[250px] max-w-[285px] items-center justify-center gap-2">
                              <div className="grid flex-1 grid-cols-2 gap-x-4 gap-y-1 text-left text-[10px] text-fog/65">
                                <span>{t('lightningOps.outPpm7d')}: <span className="text-fog">{formatPpmValue(ch.out_ppm7d)}</span></span>
                                <span>{t('lightningOps.rebalPpm7d')}: <span className="text-fog">{formatPpmValue(ch.rebal_ppm7d)}</span></span>
                                <span>
                                  {t('lightningOps.marginPpm7d')}:{' '}
                                  <span className={typeof marginPpm7d === 'number' && marginPpm7d < 0 ? 'text-ember' : 'text-fog'}>
                                    {typeof marginPpm7d === 'number' ? `${marginPpm7d >= 0 ? '+' : ''}${Math.round(marginPpm7d)}` : '-'}
                                  </span>
                                </span>
                                <span>
                                  {t('lightningOps.profit7d')}:{' '}
                                  <span className={typeof ch.profit_fee_7d_sat === 'number' && ch.profit_fee_7d_sat < 0 ? 'text-ember' : 'text-fog'}>
                                    {formatSatSigned(ch.profit_fee_7d_sat)}
                                  </span>
                                </span>
                              </div>
                              {renderAutofeeChannelRefreshButton(ch, true)}
                            </div>
                          </td>
                          <td className="border-l border-white/5 px-4 py-2 text-center">
                            <input
                              className={`mx-auto h-8 w-24 rounded-md border px-2 text-center text-xs outline-none transition-colors duration-300 focus:border-sky-300/70 disabled:opacity-60 ${condensedFeeInputClass}`}
                              type="number"
                              min={0}
                              step={1}
                              value={condensedFeeValue}
                              disabled={condensedFeeBusy}
                              title={t('lightningOps.condensedOutRateEdit')}
                              aria-label={t('lightningOps.condensedOutRateEdit')}
                              onChange={(e) => {
                                const value = e.target.value
                                setCondensedFeeDrafts((prev) => ({ ...prev, [ch.channel_point]: value }))
                              }}
                              onBlur={() => { void handleCondensedOutRateSave(ch) }}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter') {
                                  e.preventDefault()
                                  e.currentTarget.blur()
                                }
                                if (e.key === 'Escape') {
                                  e.preventDefault()
                                  setCondensedFeeDrafts((prev) => {
                                    const next = { ...prev }
                                    delete next[ch.channel_point]
                                    return next
                                  })
                                }
                              }}
                            />
                          </td>
                          <td className="px-4 py-2 text-center whitespace-nowrap text-fog/75">{typeof ch.base_fee_msat === 'number' ? ch.base_fee_msat.toLocaleString(locale) : '-'}</td>
                          <td className="px-4 py-2 text-center whitespace-nowrap text-fog/75">{typeof ch.inbound_fee_rate_ppm === 'number' ? `${ch.inbound_fee_rate_ppm} ppm` : '-'}</td>
                          <td className="px-4 py-2 text-center whitespace-nowrap text-fog/75">{typeof ch.inbound_base_msat === 'number' ? ch.inbound_base_msat.toLocaleString(locale) : '-'}</td>
                          <td className="px-4 py-2 text-center whitespace-nowrap text-fog/75">
                            <div>{typeof ch.peer_fee_rate_ppm === 'number' ? `${ch.peer_fee_rate_ppm} ppm` : '-'}</div>
                            <div className="text-[10px] text-fog/45">{typeof ch.peer_base_msat === 'number' ? `${ch.peer_base_msat.toLocaleString(locale)} msat` : '-'}</div>
                          </td>
                          <td className="px-3 py-2 text-center">
                            <a
                              href={rebalanceLink}
                              className="inline-flex h-8 w-8 items-center justify-center rounded-full border border-white/15 text-fog/70 transition hover:border-sky-300/70 hover:text-sky-100"
                              title={t('lightningOps.openInRebalanceCenter')}
                              aria-label={t('lightningOps.openInRebalanceCenter')}
                            >
                              <svg viewBox="0 0 24 24" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.8">
                                <path d="M12 4v14" />
                                <path d="M6 8h12" />
                                <path d="M8 8l-3 5h6l-3-5z" />
                                <path d="M16 8l-3 5h6l-3-5z" />
                                <path d="M8 20h8" />
                              </svg>
                            </a>
                          </td>
                          <td className="px-3 py-2 text-center">
                            <input
                              type="checkbox"
                              checked={autofeeChecked}
                              title={t('lightningOps.autofeeLabel')}
                              aria-label={t('lightningOps.autofeeLabel')}
                              onChange={(e) => {
                                handleAutofeeChannelToggle(ch, e.target.checked)
                              }}
                            />
                          </td>
                        </tr>
                        {pendingHtlcOpen && hasPendingHtlcs && (
                          <tr className={isFCRisk ? 'border-t border-rose-400/25 bg-rose-500/10' : 'border-t border-white/5 bg-ink/70'}>
                            <td colSpan={11} className="px-3 py-2">
                              <div className="overflow-x-auto rounded-lg border border-white/10 bg-black/10 p-2">
                                <table className="w-full min-w-[560px] text-[11px]">
                                  <thead>
                                    <tr className={isFCRisk ? 'text-rose-200/80' : 'text-fog/60'}>
                                      <th className="py-1 pr-3 text-left">{t('lightningOps.pendingHtlcDirection')}</th>
                                      <th className="py-1 pr-3 text-left">{t('lightningOps.pendingHtlcPeer')}</th>
                                      <th className="py-1 pr-3 text-left">{t('lightningOps.pendingHtlcAmount')}</th>
                                      <th className="py-1 text-left">{t('lightningOps.pendingHtlcExpiry')}</th>
                                    </tr>
                                  </thead>
                                  <tbody>
                                    {pendingHtlcs.map((htlc, idx) => {
                                      const expirationHeight = Number(htlc?.expiration_height || 0)
                                      const amountSat = Math.max(0, Math.round(Number(htlc?.amount_sat || 0)))
                                      const blocksToExpiry = channelBlockHeight > 0 && expirationHeight > 0
                                        ? Math.max(0, expirationHeight - channelBlockHeight)
                                        : null
                                      const expiryEta = blocksToExpiry === null ? '' : formatMaturityDuration(estimateMaturitySeconds(blocksToExpiry))
                                      const peerAlias = typeof htlc?.peer_alias === 'string' ? htlc.peer_alias.trim() : ''
                                      const forwardingChannelId = typeof htlc?.forwarding_channel_id === 'number' && Number.isFinite(htlc.forwarding_channel_id)
                                        ? Math.max(0, Math.round(htlc.forwarding_channel_id))
                                        : 0
                                      const peerLabel = peerAlias || (forwardingChannelId > 0 ? `chan ${forwardingChannelId}` : t('lightningOps.pendingHtlcPeerUnknown'))
                                      const rowKey = `${htlc?.htlc_index ?? 'idx'}-${expirationHeight}-${idx}`
                                      return (
                                        <tr key={rowKey} className={`align-top ${isFCRisk ? 'text-rose-100' : 'text-fog'}`}>
                                          <td className="py-1 pr-3 whitespace-nowrap">
                                            {htlc?.incoming ? t('lightningOps.pendingHtlcIncoming') : t('lightningOps.pendingHtlcOutgoing')}
                                          </td>
                                          <td className="py-1 pr-3 whitespace-nowrap">{peerLabel}</td>
                                          <td className="py-1 pr-3 whitespace-nowrap">{amountSat} sats</td>
                                          <td className="py-1">
                                            <div>{t('lightningOps.pendingHtlcExpiryHeight', { value: expirationHeight })}</div>
                                            {blocksToExpiry !== null && (
                                              <div className={isFCRisk ? 'text-rose-200/80' : 'text-fog/60'}>
                                                {t('lightningOps.pendingHtlcExpiryBlocks', { count: blocksToExpiry })} | {t('lightningOps.pendingHtlcExpiryEta', { time: expiryEta })}
                                              </div>
                                            )}
                                          </td>
                                        </tr>
                                      )
                                    })}
                                  </tbody>
                                </table>
                              </div>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    )
                  })}
                </tbody>
                </table>
              </div>
            </div>
          ) : (
            <div
              key={`channels-full-${channelsListSizeKey}`}
              className={`resize-y overflow-auto pr-2 ${channelsListDefaultHeightClass}`}
            >
              <div className="grid gap-3">
                {filteredChannels.map((ch) => {
                const localDisabled = ch.local_disabled ?? isLocalChanDisabled(ch.chan_status_flags)
                const statusBusy = chanStatusBusy === ch.channel_point
                const showToggle = ch.active
                const autofeeHistoryKey = autofeeChannelKey(ch.channel_point, ch.channel_id)
                const autofeeChecked = autofeeHistoryKey ? (autofeeSettings[autofeeHistoryKey] ?? true) : true
                const autofeeHistoryOpen = autofeeHistoryOpenChannelKey === autofeeHistoryKey
                const autofeeHistoryLoading = Boolean(autofeeHistoryLoadingByChannel[autofeeHistoryKey])
                const autofeeHistoryError = autofeeHistoryErrorByChannel[autofeeHistoryKey] || ''
                const autofeeHistoryRounds = autofeeHistoryByChannel[autofeeHistoryKey] || recentAutofeeRoundsByChannel[autofeeHistoryKey] || []
                const classLabel = formatChannelClassLabel(ch.class_label)
                const isInlineFeeEditing = inlineFeeChannelPoint === ch.channel_point
                const inlineBusy = inlineFeeLoading || inlineFeeSaving
                const visualTotal = (ch.local_balance_sat + ch.remote_balance_sat) > 0
                  ? (ch.local_balance_sat + ch.remote_balance_sat)
                  : (ch.capacity_sat > 0 ? ch.capacity_sat : 0)
                const localPctRaw = visualTotal > 0 ? (ch.local_balance_sat / visualTotal) * 100 : 0
                const remotePctRaw = visualTotal > 0 ? (ch.remote_balance_sat / visualTotal) * 100 : 0
                const localPct = Math.max(0, Math.min(100, localPctRaw))
                const remotePct = Math.max(0, Math.min(100, remotePctRaw))
                const localPctLabel = `${localPct.toFixed(0)}%`
                const remotePctLabel = `${remotePct.toFixed(0)}%`
                const unsettledBalanceSat = typeof ch.unsettled_balance_sat === 'number' && Number.isFinite(ch.unsettled_balance_sat)
                  ? Math.max(0, Math.round(ch.unsettled_balance_sat))
                  : 0
                const pendingHtlcCount = typeof ch.pending_htlc_count === 'number' && Number.isFinite(ch.pending_htlc_count)
                  ? Math.max(0, Math.round(ch.pending_htlc_count))
                  : 0
                const pendingHtlcs = Array.isArray(ch.pending_htlcs) ? ch.pending_htlcs : []
                const hasPendingHtlcs = pendingHtlcs.length > 0
                const pendingHtlcOpen = pendingHtlcOpenChannelPoint === ch.channel_point
                const movement7d = ch.movement_7d
                const movementOpen = movementOpenChannelPoint === ch.channel_point
                const hasMovementActivity = hasChannelMovementActivity(movement7d)
                const lowMovementChannel = isLowMovementChannel(ch)
                const peerRecommendationsOpen = peerRecommendationOpenChannelPoint === ch.channel_point
                const peerRecommendations = peerRecommendationsByChannel[ch.channel_point] || []
                const peerRecommendationTier = peerRecommendationTierByChannel[ch.channel_point] || 'strict'
                const peerRecommendationsLoading = Boolean(peerRecommendationLoadingByChannel[ch.channel_point])
                const peerRecommendationsError = peerRecommendationErrorByChannel[ch.channel_point] || ''
                const isFCRisk = !ch.active && unsettledBalanceSat > 0
                const marginPpm7d = typeof ch.out_ppm7d === 'number' && typeof ch.rebal_ppm7d === 'number'
                  ? ch.out_ppm7d - ch.rebal_ppm7d
                  : undefined
                const profitMeta = profitBadge(ch.profit_fee_7d_sat)
                const opener = ch.initiator ? t('lightningOps.openerLocal') : t('lightningOps.openerRemote')
                const inactiveSinceUnix = typeof ch.inactive_since_unix === 'number' && Number.isFinite(ch.inactive_since_unix)
                  ? Math.max(0, Math.floor(ch.inactive_since_unix))
                  : 0
                const inactiveDurationSec = !ch.active && inactiveSinceUnix > 0
                  ? Math.max(0, Math.floor((Date.now() / 1000) - inactiveSinceUnix))
                  : 0
                const inactiveForLabel = !ch.active && inactiveSinceUnix > 0
                  ? t('lightningOps.inactiveFor', { time: formatChannelDowntime(inactiveDurationSec) })
                  : ''
                const isFocused = focusedChannelPoint === ch.channel_point
                const rankingItem = channelRankingMap[ch.channel_point]
                const rankingLink = ch.channel_point
                  ? buildHashWithChannelPoint(CHANNEL_RANKING_ROUTE_KEY, ch.channel_point)
                  : `#${CHANNEL_RANKING_ROUTE_KEY}`
                const rebalanceLink = ch.channel_point
                  ? buildHashWithChannelPoint(REBALANCE_ROUTE_KEY, ch.channel_point)
                  : `#${REBALANCE_ROUTE_KEY}`
                const channelIDValue = channelIDText(ch)
                const channelIDCopied = copiedChannelIDKey === (ch.channel_point || channelIDValue)
                const cardClassBase = isFCRisk
                  ? 'rounded-2xl border border-rose-400/45 bg-rose-500/10 p-5 min-h-[170px]'
                  : localDisabled && ch.active
                    ? 'rounded-2xl border border-ember/40 bg-ember/10 p-5 min-h-[170px]'
                    : 'rounded-2xl border border-white/10 bg-ink/60 p-5 min-h-[170px]'
                const cardClass = `${cardClassBase} ${isFocused ? 'ring-1 ring-sky-300/70 bg-sky-500/10' : ''}`
                return (
                  <div key={ch.channel_point} id={channelCardID(ch.channel_point)} className={cardClass}>
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <div className="flex flex-wrap items-center gap-1.5">
                          <p className="text-sm text-fog/60">{ch.peer_alias || ch.remote_pubkey || t('lightningOps.unknownPeer')}</p>
                          {channelIDValue && (
                            <button
                              type="button"
                              className={`inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full border transition ${
                                channelIDCopied
                                  ? 'border-glow/60 bg-glow/15 text-glow'
                                  : 'border-white/10 text-fog/35 hover:border-white/25 hover:text-fog/75'
                              }`}
                              title={channelIDCopied ? t('common.copied') : t('lightningOps.copyChannelId')}
                              aria-label={channelIDCopied ? t('common.copied') : t('lightningOps.copyChannelId')}
                              onClick={() => { void handleCopyChannelID(ch) }}
                            >
                              <svg viewBox="0 0 24 24" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="1.8">
                                {channelIDCopied ? (
                                  <path d="M5 12.5l4 4L19 7" />
                                ) : (
                                  <>
                                    <path d="M4 7h16" />
                                    <path d="M4 12h16" />
                                    <path d="M4 17h16" />
                                    <path d="M8 4L6 20" />
                                    <path d="M18 4l-2 16" />
                                  </>
                                )}
                              </svg>
                            </button>
                          )}
                        </div>
                        {peerProfileLinkGroup(ch.remote_pubkey)}
                        <div className="mt-0.5 flex flex-wrap items-center gap-2">
                          <p className="min-w-0 break-all text-xs text-fog/50">
                            {t('lightningOps.pointCapacityWithOpener', { point: ch.channel_point, capacity: ch.capacity_sat, opener })}
                          </p>
                          {rankingItem && (
                            <a
                              href={rankingLink}
                              className={`shrink-0 rounded-full border px-2 py-0.5 text-[11px] transition hover:border-white/25 hover:text-fog ${channelRankingBadgeClass(rankingItem.state)}`}
                              title={t('lightningOps.openInChannelRanking')}
                              aria-label={t('lightningOps.openInChannelRanking')}
                            >
                              {t('lightningOps.channelRankingBadge', {
                                state: channelRankingStateLabel(rankingItem.state),
                                score: rankingItem.score,
                              })}
                            </a>
                          )}
                          <button
                            type="button"
                            className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-white/15 text-fog/60 transition hover:border-rose-300/70 hover:text-rose-100"
                            title={t('lightningOps.prepareCloseChannel')}
                            aria-label={t('lightningOps.prepareCloseChannel')}
                            onClick={() => handlePrepareCloseChannel(ch.channel_point)}
                          >
                            <svg viewBox="0 0 24 24" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.8">
                              <path d="M6 6l12 12" />
                              <path d="M18 6L6 18" />
                              <circle cx="12" cy="12" r="9" />
                            </svg>
                          </button>
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        <span className={`rounded-full px-3 py-1 text-xs ${ch.active ? 'bg-glow/20 text-glow' : isFCRisk ? 'bg-rose-500/25 text-rose-100' : 'bg-ember/20 text-ember'}`}>
                          {ch.active ? t('common.active') : t('common.inactive')}
                        </span>
                        {!ch.active && inactiveForLabel && (
                          <span className="text-[11px] text-fog/70">{inactiveForLabel}</span>
                        )}
                        {isFCRisk && (
                          <span className="rounded-full px-2 py-1 text-[11px] border border-rose-300/70 bg-rose-500/20 text-rose-100">
                            {t('lightningOps.fcRiskLabel')}
                          </span>
                        )}
                        {showToggle && (
                          <button
                            className={`btn-secondary text-xs px-3 py-1 ${statusBusy ? 'opacity-60 pointer-events-none' : ''}`}
                            type="button"
                            onClick={() => handleToggleChanStatus(ch)}
                            disabled={statusBusy}
                          >
                            {localDisabled ? t('lightningOps.enableChannel') : t('lightningOps.disableChannel')}
                          </button>
                        )}
                        <a
                          href={rebalanceLink}
                          className="inline-flex h-9 w-9 items-center justify-center rounded-full border border-white/15 text-fog/70 transition hover:border-sky-300/70 hover:text-sky-100"
                          title={t('lightningOps.openInRebalanceCenter')}
                          aria-label={t('lightningOps.openInRebalanceCenter')}
                        >
                          <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.8">
                            <path d="M12 4v14" />
                            <path d="M6 8h12" />
                            <path d="M8 8l-3 5h6l-3-5z" />
                            <path d="M16 8l-3 5h6l-3-5z" />
                            <path d="M8 20h8" />
                          </svg>
                        </a>
                        <div className="flex items-center gap-2 text-[11px] text-fog/70">
                          <input
                            type="checkbox"
                            checked={autofeeChecked}
                            onChange={(e) => {
                              const enabled = e.target.checked
                              handleAutofeeChannelToggle(ch, enabled)
                              if (!enabled && autofeeHistoryOpenChannelKey === autofeeHistoryKey) {
                                setAutofeeHistoryOpenChannelKey(null)
                              }
                            }}
                          />
                          <button
                            type="button"
                            className={`underline decoration-dotted underline-offset-2 ${autofeeChecked && autofeeHistoryKey ? 'hover:text-fog' : 'opacity-50 cursor-not-allowed'}`}
                            onClick={() => handleAutofeeHistoryToggle(autofeeHistoryKey, autofeeChecked)}
                            disabled={!autofeeChecked || !autofeeHistoryKey}
                          >
                            {t('lightningOps.autofeeLabel')}
                          </button>
                        </div>
                      </div>
                    </div>
                    {autofeeHistoryOpen && (
                      <div className="mt-2 rounded-xl border border-white/10 bg-ink/70 p-2.5 text-[11px]">
                        <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('lightningOps.autofeeHistoryTitle')}</p>
                        {autofeeHistoryLoading ? (
                          <p className="mt-1 text-fog/70">{t('lightningOps.autofeeHistoryLoading')}</p>
                        ) : autofeeHistoryError ? (
                          <p className="mt-1 text-ember">{autofeeHistoryError}</p>
                        ) : autofeeHistoryRounds.length ? (
                          <div className="mt-1 space-y-1.5">
                            {autofeeHistoryRounds.map((round) => {
                              const prediction = formatAutofeeHistoryPrediction(round)
                              const { limiting, relaxing } = splitAutofeeHistoryTags(round.tags || [])
                              return (
                                <div key={round.run_key} className="rounded-lg border border-white/10 bg-ink/60 px-2 py-1.5 text-fog/80">
                                  <div className="flex flex-wrap items-center gap-2">
                                    <span className="text-fog">{formatAutofeeHistoryTime(round.timestamp)}</span>
                                    <span className="text-fog/50">|</span>
                                    <span>{formatAutofeeHistoryReason(round.reason)}</span>
                                    <span className="text-fog/50">|</span>
                                    <span>{formatAutofeeHistoryOutcome(round)}</span>
                                  </div>
                                  <div className="mt-1 text-fog/70">{formatAutofeeHistorySignals(round)}</div>
                                  {limiting.length > 0 && (
                                    <div className="mt-1 text-ember/90">🛑 {t('lightningOps.autofeeHistoryLimitingTags')}: {limiting.join(' | ')}</div>
                                  )}
                                  {relaxing.length > 0 && (
                                    <div className="mt-1 text-glow/90">🟢 {t('lightningOps.autofeeHistoryRelaxingTags')}: {relaxing.join(' | ')}</div>
                                  )}
                                  {prediction && (
                                    <div className="mt-1 text-fog/70">{prediction}</div>
                                  )}
                                </div>
                              )
                            })}
                          </div>
                        ) : (
                          <p className="mt-1 text-fog/70">{t('lightningOps.autofeeHistoryEmpty')}</p>
                        )}
                      </div>
                    )}
                    <div className="mt-3 grid gap-2 xl:grid-cols-[1.25fr_0.3fr_1fr]">
                      <div className="space-y-2">
                        <div className="flex items-center justify-between gap-3 text-xs text-fog/70">
                          <span>{t('lightningOps.localLabel', { value: ch.local_balance_sat })}</span>
                          <span>{t('lightningOps.remoteLabel', { value: ch.remote_balance_sat })}</span>
                        </div>
                        <div className="flex items-center gap-2 text-[11px]">
                          <span className="w-12 text-right text-glow">{localPctLabel}</span>
                          <div className="relative h-1.5 flex-1 overflow-hidden rounded-full bg-white/10">
                            <div
                              className="absolute inset-y-0 left-0 bg-glow/70"
                              style={{ width: `${localPct}%` }}
                            />
                            <div
                              className="absolute inset-y-0 right-0 bg-white/35"
                              style={{ width: `${remotePct}%` }}
                            />
                          </div>
                          <span className="w-12 text-left text-fog/70">{remotePctLabel}</span>
                        </div>
                      </div>
                      <button
                        type="button"
                        className={`rounded-xl p-2.5 text-left transition ${isFCRisk ? 'border border-rose-400/45 bg-rose-500/10 hover:border-rose-300/70' : 'border border-white/10 bg-ink/70 hover:border-white/25'} ${hasPendingHtlcs ? '' : 'cursor-default'}`}
                        onClick={() => {
                          if (!hasPendingHtlcs) return
                          setPendingHtlcOpenChannelPoint((current) => current === ch.channel_point ? '' : ch.channel_point)
                        }}
                        disabled={!hasPendingHtlcs}
                        aria-expanded={pendingHtlcOpen}
                        aria-label={t('lightningOps.pendingHtlcDetailsToggle')}
                      >
                        <p className={`text-[10px] uppercase tracking-wide ${isFCRisk ? 'text-rose-200' : 'text-fog/60'}`}>{t('lightningOps.pendingHtlcsTitle')}</p>
                        <div className="mt-1 grid grid-cols-1 gap-y-0.5 text-[11px]">
                          <p className={unsettledBalanceSat > 0 ? (isFCRisk ? 'text-rose-300' : 'text-brass') : 'text-fog'}>
                            {t('lightningOps.unsettledBalanceLabel', { value: unsettledBalanceSat })}
                          </p>
                          <p className={pendingHtlcCount > 0 ? (isFCRisk ? 'text-rose-300' : 'text-brass') : 'text-fog'}>
                            {t('lightningOps.pendingHtlcCountLabel', { count: pendingHtlcCount })}
                          </p>
                          {hasPendingHtlcs && (
                            <p className={isFCRisk ? 'text-rose-200/80' : 'text-fog/60'}>
                              {pendingHtlcOpen ? t('lightningOps.pendingHtlcDetailsHide') : t('lightningOps.pendingHtlcDetailsShow')}
                            </p>
                          )}
                        </div>
                      </button>
                      <div className="rounded-xl border border-white/10 bg-ink/70 p-2.5">
                        <div className="flex items-center justify-between gap-2">
                          <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('lightningOps.economic7d')}</p>
                          {renderAutofeeChannelRefreshButton(ch)}
                        </div>
                        <div className="mt-1 grid grid-cols-1 gap-y-0.5 text-[11px] sm:grid-cols-3 sm:gap-x-3 sm:gap-y-0">
                          <p className="whitespace-nowrap">
                            <span className="text-fog/50">{t('lightningOps.outPpm7d')}:</span>{' '}
                            <span className="text-fog">{formatPpmValue(ch.out_ppm7d)}</span>
                          </p>
                          <p className="whitespace-nowrap">
                            <span className="text-fog/50">{t('lightningOps.rebalPpm7d')}:</span>{' '}
                            <span className="text-fog">{formatPpmValue(ch.rebal_ppm7d)}</span>
                          </p>
                          <p className="whitespace-nowrap">
                            <span className="text-fog/50">{t('lightningOps.marginPpm7d')}:</span>{' '}
                            <span className={typeof marginPpm7d === 'number' && marginPpm7d < 0 ? 'text-ember' : 'text-fog'}>
                              {typeof marginPpm7d === 'number' ? `${marginPpm7d >= 0 ? '+' : ''}${Math.round(marginPpm7d)} ppm` : '-'}
                            </span>
                          </p>
                        </div>
                        <div className="mt-1.5 flex items-center justify-between gap-2">
                          <p className="whitespace-nowrap text-[11px]">
                            <span className="text-fog/60">{t('lightningOps.profit7d')}:</span>{' '}
                            <span className={typeof ch.profit_fee_7d_sat === 'number' && ch.profit_fee_7d_sat < 0 ? 'text-ember' : 'text-fog'}>
                              {formatSatSigned(ch.profit_fee_7d_sat)}
                            </span>
                          </p>
                          <span className={`rounded-full px-2 py-0.5 text-[10px] ${profitMeta.className}`}>{profitMeta.label}</span>
                        </div>
                      </div>
                    </div>
                    {movementOpen && movement7d && (
                      <div className="mt-2 rounded-xl border border-white/10 bg-ink/70 p-2.5">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <p className="text-[10px] uppercase tracking-wide text-fog/60">{t('lightningOps.last7dMov')}</p>
                          {!hasMovementActivity && (
                            <span className="text-[10px] text-fog/50">{t('lightningOps.last7dMovEmpty')}</span>
                          )}
                        </div>
                        <div className="mt-2 grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
                          {[
                            {
                              key: 'forward_in',
                              label: t('lightningOps.last7dMovForwardIn'),
                              count: movement7d.forward_in_count,
                              amount: movement7d.forward_in_amount_sat,
                            },
                            {
                              key: 'forward_out',
                              label: t('lightningOps.last7dMovForwardOut'),
                              count: movement7d.forward_out_count,
                              amount: movement7d.forward_out_amount_sat,
                            },
                            {
                              key: 'rebalance_in',
                              label: t('lightningOps.last7dMovRebalanceIn'),
                              count: movement7d.rebalance_in_count,
                              amount: movement7d.rebalance_in_amount_sat,
                            },
                            {
                              key: 'rebalance_out',
                              label: t('lightningOps.last7dMovRebalanceOut'),
                              count: movement7d.rebalance_out_count,
                              amount: movement7d.rebalance_out_amount_sat,
                            },
                            {
                              key: 'lightning_out',
                              label: t('lightningOps.last7dMovLightningOut'),
                              count: movement7d.lightning_out_count,
                              amount: movement7d.lightning_out_amount_sat,
                            },
                            {
                              key: 'lightning_in',
                              label: t('lightningOps.last7dMovLightningIn'),
                              count: movement7d.lightning_in_count,
                              amount: movement7d.lightning_in_amount_sat,
                            },
                          ].map((item) => (
                            <div key={item.key} className="rounded-lg border border-white/10 bg-black/10 px-2.5 py-2">
                              <div className="flex items-center justify-between gap-2 text-[10px] uppercase tracking-wide text-fog/60">
                                <span>{item.label}</span>
                                <span>{Math.max(0, Math.round(Number(item.count || 0))).toLocaleString(locale)}x</span>
                              </div>
                              <div className="mt-1 text-sm text-fog">{formatMovementAmount(item.amount)}</div>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                    {peerRecommendationsOpen && (
                      <div className="mt-2 rounded-xl border border-emerald-400/20 bg-emerald-500/5 p-2.5">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div>
                            <p className="text-[10px] uppercase tracking-wide text-emerald-100/80">{t('lightningOps.peerRecommendationsTitle')}</p>
                            <p className="text-[11px] text-fog/60">{peerRecommendationTierHint(peerRecommendationTier)}</p>
                          </div>
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="rounded-full border border-white/10 px-2 py-0.5 text-[10px] text-fog/60">
                              {t('lightningOps.peerRecommendationsLowMovement')}
                            </span>
                            <span className="rounded-full border border-emerald-400/20 bg-emerald-500/10 px-2 py-0.5 text-[10px] text-emerald-100/80">
                              {peerRecommendationTierLabel(peerRecommendationTier)}
                            </span>
                          </div>
                        </div>
                        {peerRecommendationsLoading ? (
                          <p className="mt-2 text-sm text-fog/70">{t('lightningOps.peerRecommendationsLoading')}</p>
                        ) : peerRecommendationsError ? (
                          <p className="mt-2 text-sm text-ember">{peerRecommendationsError}</p>
                        ) : peerRecommendations.length ? (
                          <div className="mt-2 grid gap-2 xl:grid-cols-2">
                            {peerRecommendations.map((item) => {
                              const peerAddress = recommendationPeerAddress(item)
                              const canUseRecommendation = peerAddress.length > 0
                              const copyKey = recommendationCopyKey(ch.channel_point, item.pub_key)
                              const copied = peerRecommendationCopiedKey === copyKey
                              const hasClearnet = item.has_clearnet === true
                              return (
                                <div key={item.pub_key} className="rounded-lg border border-white/10 bg-ink/70 p-3">
                                  <div className="flex flex-wrap items-start justify-between gap-2">
                                    <div>
                                      <div className="flex flex-wrap items-center gap-2">
                                        <div className="text-sm text-fog">{item.alias || item.pub_key}</div>
                                        <span className={`rounded-full px-2 py-0.5 text-[10px] ${hasClearnet ? 'bg-emerald-500/15 text-emerald-100' : 'bg-amber-500/15 text-amber-100'}`}>
                                          {hasClearnet ? t('lightningOps.peerRecommendationsClearnet') : t('lightningOps.peerRecommendationsTor')}
                                        </span>
                                      </div>
                                      <div className="text-[11px] text-fog/50 break-all">{item.pub_key}</div>
                                    </div>
                                    {peerProfileLinkGroup(item.pub_key, 'flex flex-wrap items-center justify-end gap-2')}
                                  </div>
                                  <div className="mt-2 grid gap-1 text-[11px] text-fog/70 sm:grid-cols-2">
                                    <div>{t('lightningOps.peerRecommendationsCapacity', { value: Math.round(Number(item.total_capacity_sat || 0)).toLocaleString(locale) })}</div>
                                    <div>{t('lightningOps.peerRecommendationsChannels', { count: Math.max(0, Math.round(Number(item.channel_count || 0))) })}</div>
                                    <div>{t('lightningOps.peerRecommendationsInbound', { value: typeof item.inbound_fee_rate_ppm === 'number' ? item.inbound_fee_rate_ppm : 0 })}</div>
                                    <div>{t('lightningOps.peerRecommendationsOutbound', { value: typeof item.outbound_fee_rate_ppm === 'number' ? item.outbound_fee_rate_ppm : 0 })}</div>
                                  </div>
                                  {((Number(item.shared_channel_count || 0) > 0) || (Number(item.shared_capacity_sat || 0) > 0)) && (
                                    <div className="mt-1 text-[11px] text-fog/55">
                                      {t('lightningOps.peerRecommendationsShared', {
                                        count: Math.max(0, Math.round(Number(item.shared_channel_count || 0))),
                                        value: Math.round(Number(item.shared_capacity_sat || 0)).toLocaleString(locale),
                                      })}
                                    </div>
                                  )}
                                  <div className="mt-2 rounded-md bg-black/10 px-2 py-1 text-[11px] text-fog/60 break-all">
                                    {peerAddress || t('lightningOps.peerRecommendationsAddressUnavailable')}
                                  </div>
                                  <div className="mt-2 flex flex-wrap gap-2">
                                    <button
                                      type="button"
                                      className="btn-secondary px-3 py-1 text-[11px] disabled:opacity-50 disabled:cursor-not-allowed"
                                      onClick={() => { void handleCopyPeerRecommendation(ch.channel_point, item) }}
                                      disabled={!canUseRecommendation}
                                    >
                                      {copied ? t('common.copied') : t('lightningOps.peerRecommendationsCopy')}
                                    </button>
                                    <button
                                      type="button"
                                      className="btn-primary px-3 py-1 text-[11px] disabled:opacity-50 disabled:cursor-not-allowed"
                                      onClick={() => handleUsePeerRecommendation(item)}
                                      disabled={!canUseRecommendation}
                                    >
                                      {t('lightningOps.peerRecommendationsUse')}
                                    </button>
                                    <button
                                      type="button"
                                      className="btn-secondary px-3 py-1 text-[11px] disabled:opacity-50 disabled:cursor-not-allowed"
                                      onClick={() => handleUsePeerRecommendationBatch(item)}
                                      disabled={!canUseRecommendation}
                                    >
                                      {t('lightningOps.peerRecommendationsBatch')}
                                    </button>
                                  </div>
                                </div>
                              )
                            })}
                          </div>
                        ) : (
                          <p className="mt-2 text-sm text-fog/70">{t('lightningOps.peerRecommendationsEmpty')}</p>
                        )}
                      </div>
                    )}
                    {pendingHtlcOpen && hasPendingHtlcs && (
                      <div className={`mt-2 rounded-xl p-2.5 ${isFCRisk ? 'border border-rose-400/45 bg-rose-500/10' : 'border border-white/10 bg-ink/70'}`}>
                        <p className={`text-[10px] uppercase tracking-wide ${isFCRisk ? 'text-rose-200' : 'text-fog/60'}`}>{t('lightningOps.pendingHtlcDetailsTitle')}</p>
                        <div className="mt-2 overflow-x-auto">
                          <table className="w-full min-w-[560px] text-[11px]">
                            <thead>
                              <tr className={isFCRisk ? 'text-rose-200/80' : 'text-fog/60'}>
                                <th className="py-1 pr-3 text-left">{t('lightningOps.pendingHtlcDirection')}</th>
                                <th className="py-1 pr-3 text-left">{t('lightningOps.pendingHtlcPeer')}</th>
                                <th className="py-1 pr-3 text-left">{t('lightningOps.pendingHtlcAmount')}</th>
                                <th className="py-1 text-left">{t('lightningOps.pendingHtlcExpiry')}</th>
                              </tr>
                            </thead>
                            <tbody>
                              {pendingHtlcs.map((htlc, idx) => {
                                const expirationHeight = Number(htlc?.expiration_height || 0)
                                const amountSat = Math.max(0, Math.round(Number(htlc?.amount_sat || 0)))
                                const blocksToExpiry = channelBlockHeight > 0 && expirationHeight > 0
                                  ? Math.max(0, expirationHeight - channelBlockHeight)
                                  : null
                                const expiryEta = blocksToExpiry === null ? '' : formatMaturityDuration(estimateMaturitySeconds(blocksToExpiry))
                                const peerAlias = typeof htlc?.peer_alias === 'string' ? htlc.peer_alias.trim() : ''
                                const forwardingChannelId = typeof htlc?.forwarding_channel_id === 'number' && Number.isFinite(htlc.forwarding_channel_id)
                                  ? Math.max(0, Math.round(htlc.forwarding_channel_id))
                                  : 0
                                const peerLabel = peerAlias || (forwardingChannelId > 0 ? `chan ${forwardingChannelId}` : t('lightningOps.pendingHtlcPeerUnknown'))
                                const rowKey = `${htlc?.htlc_index ?? 'idx'}-${expirationHeight}-${idx}`
                                return (
                                  <tr key={rowKey} className={`align-top ${isFCRisk ? 'text-rose-100' : 'text-fog'}`}>
                                    <td className="py-1 pr-3 whitespace-nowrap">
                                      {htlc?.incoming ? t('lightningOps.pendingHtlcIncoming') : t('lightningOps.pendingHtlcOutgoing')}
                                    </td>
                                    <td className="py-1 pr-3 whitespace-nowrap">{peerLabel}</td>
                                    <td className="py-1 pr-3 whitespace-nowrap">{amountSat} sats</td>
                                    <td className="py-1">
                                      <div>{t('lightningOps.pendingHtlcExpiryHeight', { value: expirationHeight })}</div>
                                      {blocksToExpiry !== null && (
                                        <div className={isFCRisk ? 'text-rose-200/80' : 'text-fog/60'}>
                                          {t('lightningOps.pendingHtlcExpiryBlocks', { count: blocksToExpiry })} · {t('lightningOps.pendingHtlcExpiryEta', { time: expiryEta })}
                                        </div>
                                      )}
                                    </td>
                                  </tr>
                                )
                              })}
                            </tbody>
                          </table>
                        </div>
                      </div>
                    )}
                    <div className="mt-3 grid gap-3 lg:grid-cols-6 text-xs text-fog/70">
                      <div>
                        {t('lightningOps.outRate')}:{' '}
                        <button
                          type="button"
                          className="text-fog hover:text-white hover:underline underline-offset-2"
                          onClick={() => { void startInlineFeeEdit(ch) }}
                        >
                          {typeof ch.fee_rate_ppm === 'number' ? `${ch.fee_rate_ppm} ppm` : '-'}
                        </button>
                      </div>
                      <div>
                        {t('lightningOps.outBase')}:{' '}
                        <button
                          type="button"
                          className="text-fog hover:text-white hover:underline underline-offset-2"
                          onClick={() => { void startInlineFeeEdit(ch) }}
                        >
                          {typeof ch.base_fee_msat === 'number' ? `${ch.base_fee_msat} msats` : '-'}
                        </button>
                      </div>
                      <div>
                        {t('lightningOps.inRate')}:{' '}
                        <button
                          type="button"
                          className="text-fog hover:text-white hover:underline underline-offset-2"
                          onClick={() => { void startInlineFeeEdit(ch) }}
                        >
                          {typeof ch.inbound_fee_rate_ppm === 'number' ? `${ch.inbound_fee_rate_ppm} ppm` : '-'}
                        </button>
                      </div>
                      <div>
                        {t('lightningOps.peerRate')}:{' '}
                        <span className="text-fog">
                          {typeof ch.peer_fee_rate_ppm === 'number' ? `${ch.peer_fee_rate_ppm} ppm` : '-'}
                        </span>
                      </div>
                      <div>
                        {t('lightningOps.peerBase')}:{' '}
                        <span className="text-fog">
                          {typeof ch.peer_base_msat === 'number' ? `${ch.peer_base_msat} msats` : '-'}
                        </span>
                      </div>
                      <div className="text-right">
                        <div className="flex flex-wrap items-center justify-end gap-2">
                          {lowMovementChannel && (
                            <button
                              type="button"
                              className={`inline-flex items-center gap-1 rounded-full border px-2 py-1 text-[10px] transition ${
                                peerRecommendationsOpen
                                  ? 'border-emerald-300/70 bg-emerald-500/10 text-emerald-100'
                                  : 'border-white/15 bg-white/5 text-fog/75 hover:border-white/30 hover:text-fog'
                              }`}
                              onClick={() => { void handleTogglePeerRecommendations(ch) }}
                              aria-expanded={peerRecommendationsOpen}
                              aria-label={t('lightningOps.peerRecommendations')}
                            >
                              <svg viewBox="0 0 24 24" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.8">
                                <path d="M12 5a3 3 0 1 0 0 6 3 3 0 0 0 0-6Z" />
                                <path d="M5 19a7 7 0 0 1 14 0" />
                                <path d="M18 7h3" />
                                <path d="M19.5 5.5v3" />
                              </svg>
                              <span>{t('lightningOps.peerRecommendations')}</span>
                            </button>
                          )}
                          {movement7d && (
                            <button
                              type="button"
                              className={`inline-flex items-center gap-1 rounded-full border px-2 py-1 text-[10px] transition ${
                                movementOpen
                                  ? 'border-sky-300/70 bg-sky-500/10 text-sky-100'
                                  : hasMovementActivity
                                    ? 'border-white/15 bg-white/5 text-fog/75 hover:border-white/30 hover:text-fog'
                                    : 'border-white/10 bg-white/5 text-fog/55 hover:border-white/20 hover:text-fog/75'
                              }`}
                              onClick={() => {
                                setMovementOpenChannelPoint((current) => current === ch.channel_point ? '' : ch.channel_point)
                              }}
                              aria-expanded={movementOpen}
                              aria-label={t('lightningOps.last7dMov')}
                            >
                              <svg viewBox="0 0 24 24" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.8">
                                <path d="M4 16l4-4 3 3 5-6 4 4" />
                                <path d="M4 20h16" />
                              </svg>
                              <span>{t('lightningOps.last7dMov')}</span>
                            </button>
                          )}
                          {classLabel && (
                            <span className="text-fog/60">{classLabel}</span>
                          )}
                        </div>
                      </div>
                    </div>
                    {isInlineFeeEditing && (
                      <div className="mt-3 rounded-xl border border-white/10 bg-ink/70 p-3">
                        <div className="grid gap-2 lg:grid-cols-3">
                          <input
                            className="input-field"
                            type="number"
                            min={0}
                            placeholder={t('lightningOps.feeRatePpm')}
                            value={inlineFeeRatePpm}
                            onChange={(e) => setInlineFeeRatePpm(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                void handleInlineFeeSave()
                              }
                              if (e.key === 'Escape') {
                                e.preventDefault()
                                cancelInlineFeeEdit()
                              }
                            }}
                          />
                          <input
                            className="input-field"
                            type="number"
                            min={0}
                            placeholder={t('lightningOps.baseFeeMsats')}
                            value={inlineBaseFeeMsat}
                            onChange={(e) => setInlineBaseFeeMsat(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                void handleInlineFeeSave()
                              }
                              if (e.key === 'Escape') {
                                e.preventDefault()
                                cancelInlineFeeEdit()
                              }
                            }}
                          />
                          <input
                            className="input-field"
                            type="number"
                            max={0}
                            placeholder={t('lightningOps.inboundFeeRate')}
                            value={inlineInboundFeeRatePpm}
                            onChange={(e) => setInlineInboundFeeRatePpm(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                void handleInlineFeeSave()
                              }
                              if (e.key === 'Escape') {
                                e.preventDefault()
                                cancelInlineFeeEdit()
                              }
                            }}
                          />
                        </div>
                        <p className="mt-2 text-[11px] text-fog/60">{t('lightningOps.inlineFeeEditHint')}</p>
                        {inlineFeeLoading && <p className="mt-2 text-xs text-fog/60">{t('lightningOps.loadingFees')}</p>}
                        {inlineFeeStatus && <p className="mt-2 text-xs text-brass">{inlineFeeStatus}</p>}
                        <div className="mt-3 flex flex-wrap gap-2">
                          <button
                            className="btn-secondary text-xs px-3 py-2"
                            type="button"
                            onClick={() => { void handleInlineFeeSave() }}
                            disabled={inlineBusy}
                          >
                            {inlineBusy ? t('lightningOps.updatingFees') : t('lightningOps.updateFees')}
                          </button>
                          <button
                            className="btn-secondary text-xs px-3 py-2"
                            type="button"
                            onClick={cancelInlineFeeEdit}
                            disabled={inlineBusy}
                          >
                            {t('common.cancel')}
                          </button>
                        </div>
                      </div>
                    )}
                    <div className="mt-2 text-xs text-fog/50">
                      {ch.private ? t('lightningOps.privateChannel') : t('lightningOps.publicChannel')}
                    </div>
                    {localDisabled && ch.active && (
                      <div className="mt-2 text-xs text-amber-200">{t('lightningOps.localDisabled')}</div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
          )
        ) : (
          <p className="text-sm text-fog/60">{t('lightningOps.noChannelsFound')}</p>
        )}
        </>
        )}
      </div>

      <div id={LIGHTNING_TOOLS_SECTION_ID} className="order-1 space-y-4">
        <div className="section-card space-y-4">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold">{t('lightningOps.lightningToolsTitle')}</h3>
              <p className="text-sm text-fog/60">{t('lightningOps.lightningToolsSubtitle')}</p>
            </div>
            <button
              type="button"
              className="btn-secondary text-xs px-3 py-2"
              onClick={() => setLightningToolsOpen((open) => !open)}
              aria-expanded={lightningToolsOpen}
            >
              {lightningToolsOpen ? t('common.hide') : t('common.open')}
            </button>
          </div>
          <div className="flex flex-wrap gap-2">
            {renderToolShortcut(ADD_PEER_TOOL_SECTION_ID, t('lightningOps.addPeer'), 'peer')}
            {renderToolShortcut(OPEN_CHANNEL_SECTION_ID, t('lightningOps.openChannel'), 'open', openPeerInputRef.current)}
            {renderToolShortcut(CLOSE_CHANNEL_SECTION_ID, t('lightningOps.closeChannel'), 'close', closeSelectRef.current)}
            {renderToolShortcut(UPDATE_FEES_SECTION_ID, t('lightningOps.updateFees'), 'fees')}
            {renderToolShortcut(BATCH_OPEN_SECTION_ID, t('lightningOps.batchOpenTitle'), 'batch', batchPeerInputRef.current)}
            {renderToolShortcut(BALANCED_OPEN_SECTION_ID, t('lightningOps.balancedOpenTitle'), 'balance')}
            {renderToolShortcut(WATCHTOWER_SECTION_ID, t('lightningOps.watchtowerTitle'), 'tower')}
            {renderToolShortcut(HTLC_MANAGER_SECTION_ID, t('lightningOps.htlcManagerTitle'), 'pulse')}
            {renderToolShortcut(AMBOSS_HEALTH_SECTION_ID, t('lightningOps.ambossHealthTitle'), 'pulse')}
            {renderToolShortcut(CHAN_HEAL_SECTION_ID, t('lightningOps.chanHealTitle'), 'heal')}
            {renderToolShortcut(TOR_PEER_SECTION_ID, t('lightningOps.torPeerTitle'), 'tor')}
            {renderToolShortcut(FAILED_PAYMENTS_CLEANER_SECTION_ID, t('lightningOps.failedPaymentsCleanerTitle'), 'clean')}
            {renderToolShortcut(SIGN_MESSAGE_SECTION_ID, t('lightningOps.signMessageTitle'), 'sign')}
          </div>
        </div>

        {lightningToolsOpen && (
          <div className="space-y-6">
      <div className="grid gap-6 lg:grid-cols-2">
        <div id={ADD_PEER_TOOL_SECTION_ID} className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('lightningOps.addPeer')}</h3>
          <input
            className="input-field"
            placeholder={t('lightningOps.peerAddressPlaceholder')}
            value={peerAddress}
            onChange={(e) => setPeerAddress(e.target.value)}
          />
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input
              type="checkbox"
              checked={peerTemporary}
              onChange={(e) => setPeerTemporary(e.target.checked)}
            />
            {t('lightningOps.temporaryPeer')}
          </label>
          <div className="flex flex-wrap gap-3">
            <button className="btn-primary" onClick={handleConnectPeer}>{t('lightningOps.connectPeer')}</button>
            <button
              className="btn-secondary disabled:opacity-60 disabled:cursor-not-allowed"
              onClick={handleBoostPeers}
              disabled={boostRunning}
              title={t('lightningOps.boostHint')}
            >
              {boostRunning ? t('lightningOps.boosting') : t('lightningOps.boostPeers')}
            </button>
          </div>
          {peerStatus && <p className="text-sm text-brass">{peerStatus}</p>}
          {boostStatus && <p className="text-sm text-brass">{boostStatus}</p>}
        </div>

        <div id={OPEN_CHANNEL_SECTION_ID} className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('lightningOps.openChannel')}</h3>
          <input
            ref={openPeerInputRef}
            className="input-field"
            placeholder={t('lightningOps.peerAddressPlaceholder')}
            value={openPeer}
            onChange={(e) => setOpenPeer(e.target.value)}
          />
          <div className="grid gap-4 lg:grid-cols-2">
            <input
              className="input-field"
              placeholder={t('lightningOps.fundingAmount')}
              type="number"
              min={20000}
              value={openAmount}
              onChange={(e) => setOpenAmount(e.target.value)}
            />
            <input
              className="input-field"
              placeholder={t('lightningOps.closeAddressOptional')}
              type="text"
              value={openCloseAddress}
              onChange={(e) => setOpenCloseAddress(e.target.value)}
            />
          </div>
          <input
            className="input-field"
            placeholder={t('lightningOps.pushAmountOptional')}
            type="number"
            min={0}
            value={openPushSat}
            onChange={(e) => setOpenPushSat(e.target.value)}
          />
          <p className="text-xs text-fog/50">{t('lightningOps.pushAmountNote')}</p>
          <label className="text-sm text-fog/70">
            {t('lightningOps.feeRate')}
            <span className="ml-2 text-xs text-fog/50">
              {t('lightningOps.feeHint', { fastest: openFeeHint?.fastest ?? '-', hour: openFeeHint?.hour ?? '-' })}
            </span>
          </label>
          <div className="flex flex-wrap gap-3 text-sm">
            <button
              className={openFeeMode === 'auto' ? 'btn-primary' : 'btn-secondary'}
              type="button"
              onClick={() => setOpenFeeMode('auto')}
            >
              {t('lightningOps.closeFeeModeAuto')}
            </button>
            <button
              className={openFeeMode === 'manual' ? 'btn-primary' : 'btn-secondary'}
              type="button"
              onClick={() => setOpenFeeMode('manual')}
            >
              {t('lightningOps.closeFeeModeManual')}
            </button>
          </div>
          {openFeeMode === 'auto' && (
            <p className="text-xs text-fog/55">{t('lightningOps.openFeeAutoHint')}</p>
          )}
          {openFeeMode === 'manual' && (
            <>
              <div className="flex flex-wrap items-center gap-3">
                <input
                  className="input-field flex-1 min-w-[140px]"
                  placeholder={t('lightningOps.closeFeeModeManual')}
                  type="number"
                  min={1}
                  value={openFeeRate}
                  onChange={(e) => setOpenFeeRate(e.target.value)}
                />
                <button
                  className="btn-secondary text-xs px-3 py-2"
                  type="button"
                  onClick={() => {
                    if (openFeeHint?.fastest) {
                      setOpenFeeRate(String(openFeeHint.fastest))
                    }
                  }}
                  disabled={!openFeeHint?.fastest}
                >
                  {t('lightningOps.useFastest')}
                </button>
              </div>
              <p className="text-xs text-fog/55">{t('lightningOps.openFeeManualHint')}</p>
            </>
          )}
          {openFeeStatus && <p className="text-xs text-fog/50">{openFeeStatus}</p>}
          {(openPreviewLoading || openPreview || openPreviewStatus) && (
            <div className={`rounded-2xl border p-3 space-y-2 ${openPreview?.enough_funds === false ? 'border-amber-400/30 bg-amber-500/10' : 'border-white/10 bg-ink/70'}`}>
              <div className="flex items-center justify-between gap-3 text-xs">
                <span className="text-fog/60">{t('lightningOps.openPreviewTitle')}</span>
                {openPreview && (
                  <span className="text-fog/50">{t('lightningOps.closePreviewEstimated')}</span>
                )}
              </div>
              {openPreviewLoading && (
                <p className="text-xs text-fog/60">{t('lightningOps.openPreviewLoading')}</p>
              )}
              {!openPreviewLoading && openPreview && (
                <>
                  <div className="grid gap-2 text-xs text-fog/80 sm:grid-cols-2">
                    <p>{t('lightningOps.openPreviewFee', { amount: formatSatsValue(openPreview.fee_sat) })}</p>
                    <p>{t('lightningOps.openPreviewVbytes', { amount: openPreview.estimated_vbytes, fee: openPreview.sat_per_vbyte })}</p>
                    <p>{t('lightningOps.openPreviewFunding', { amount: formatSatsValue(openPreview.local_funding_sat) })}</p>
                    <p>{t('lightningOps.openPreviewPush', { amount: formatSatsValue(openPreview.push_sat) })}</p>
                    <p>{t('lightningOps.openPreviewTotalDebit', { amount: formatSatsValue(openPreview.total_debit_sat) })}</p>
                    <p>{t('lightningOps.openPreviewRemaining', { amount: formatSatsValue(openPreview.spendable_remaining_sat) })}</p>
                    {!openPreview.reference_only && (
                      <p>{t('lightningOps.openPreviewInputs', { selected: openPreview.selected_input_count, amount: formatSatsValue(openPreview.selected_input_sat) })}</p>
                    )}
                    <p>{t('lightningOps.openPreviewSpendable', { amount: formatSatsValue(openPreview.spendable_sat) })}</p>
                  </div>
                  <p className="text-xs text-fog/55">
                    {openPreview.reference_only
                      ? t('lightningOps.openPreviewReferenceFallback', { fee: openPreview.sat_per_vbyte })
                      : openFeeMode === 'manual'
                      ? t('lightningOps.openPreviewReferenceManual', { fee: openPreview.sat_per_vbyte })
                      : t('lightningOps.openPreviewReferenceAuto', { fee: openPreview.sat_per_vbyte })}
                  </p>
                  <p className={openPreview.enough_funds ? 'text-xs text-emerald-200' : 'text-xs text-amber-200'}>
                    {openPreview.enough_funds ? t('lightningOps.openPreviewEnough') : t('lightningOps.openPreviewInsufficient')}
                  </p>
                </>
              )}
              {!openPreviewLoading && openPreviewStatus && (
                <p className={`text-xs ${openPreview?.enough_funds === false ? 'text-amber-200' : 'text-fog/60'}`}>{openPreviewStatus}</p>
              )}
            </div>
          )}
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input type="checkbox" checked={openPrivate} onChange={(e) => setOpenPrivate(e.target.checked)} />
            {t('lightningOps.privateChannel')}
          </label>
          <button className="btn-primary" onClick={handleOpenChannel}>{t('lightningOps.openChannel')}</button>
          <p className="text-xs text-fog/50">{t('lightningOps.minimumFundingNote')}</p>
          {openStatus && (
            <div className="text-sm text-brass break-words">
              <p>{openStatus}</p>
              {openChannelPoint && mempoolLink(openChannelPoint) && (
                <a
                  className="mt-1 block text-emerald-200 hover:text-emerald-100 break-all"
                  href={mempoolLink(openChannelPoint)}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {t('lightningOps.fundingTx', { point: openChannelPoint })}
                </a>
              )}
            </div>
          )}
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <div id={CLOSE_CHANNEL_SECTION_ID} ref={closeCardRef} className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('lightningOps.closeChannel')}</h3>
          <select
            ref={closeSelectRef}
            className="input-field"
            value={closePoint}
            onChange={(e) => setClosePoint(e.target.value)}
          >
            <option value="">{t('lightningOps.selectChannel')}</option>
            {channelOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
          <label className="text-sm text-fog/70">
            {t('lightningOps.feeRate')}
            <span className="ml-2 text-xs text-fog/50">
              {t('lightningOps.feeHint', { fastest: closeFeeHint?.fastest ?? '-', hour: closeFeeHint?.hour ?? '-' })}
            </span>
          </label>
          {!closeForce && (
            <div className="flex flex-wrap gap-3 text-sm">
              <button
                className={closeFeeMode === 'auto' ? 'btn-primary' : 'btn-secondary'}
                type="button"
                onClick={() => setCloseFeeMode('auto')}
              >
                {t('lightningOps.closeFeeModeAuto')}
              </button>
              <button
                className={closeFeeMode === 'manual' ? 'btn-primary' : 'btn-secondary'}
                type="button"
                onClick={() => setCloseFeeMode('manual')}
              >
                {t('lightningOps.closeFeeModeManual')}
              </button>
            </div>
          )}
          {!closeForce && closeFeeMode === 'auto' && (
            <p className="text-xs text-fog/55">{t('lightningOps.closeFeeAutoHint')}</p>
          )}
          {!closeForce && closeFeeMode === 'manual' && (
            <>
              <div className="flex flex-wrap items-center gap-3">
                <input
                  className="input-field flex-1 min-w-[140px]"
                  placeholder={t('lightningOps.closeFeeModeManual')}
                  type="number"
                  min={1}
                  value={closeFeeRate}
                  onChange={(e) => setCloseFeeRate(e.target.value)}
                />
                <button
                  className="btn-secondary text-xs px-3 py-2"
                  type="button"
                  onClick={() => {
                    if (closeFeeHint?.fastest) {
                      setCloseFeeRate(String(closeFeeHint.fastest))
                    }
                  }}
                  disabled={!closeFeeHint?.fastest}
                >
                  {t('lightningOps.useFastest')}
                </button>
              </div>
              {Number(closeFeeRate || 0) > 0 && Number(closeFeeRate || 0) <= 1 && (
                <p className="text-xs text-brass">{t('lightningOps.closeLowFeeWarning')}</p>
              )}
              <p className="text-xs text-fog/55">{t('lightningOps.closeFeeManualHint')}</p>
            </>
          )}
          {!closeForce && selectedCloseChannel && closePreview && (
            <div className="rounded-2xl border border-white/10 bg-ink/70 p-3 space-y-2">
              <div className="flex items-center justify-between gap-3 text-xs">
                <span className="text-fog/60">{t('lightningOps.closePreviewTitle')}</span>
                <span className="text-fog/50">{t('lightningOps.closePreviewEstimated')}</span>
              </div>
              <div className="grid gap-2 text-xs text-fog/80 sm:grid-cols-2">
                <p>{t('lightningOps.closePreviewFee', { amount: formatSatsValue(closePreview.feeSat) })}</p>
                <p>{t('lightningOps.closePreviewVbytes', { amount: closePreview.estimatedVbytes, fee: closePreview.satPerVbyte })}</p>
                <p>{t('lightningOps.closePreviewLocalBalance', { amount: formatSatsValue(selectedCloseChannel.local_balance_sat) })}</p>
                <p>{t('lightningOps.closePreviewLocalAfterFee', { amount: formatSatsValue(closePreview.estimatedLocalAfterFeeSat) })}</p>
              </div>
              <p className="text-xs text-fog/55">
                {closePreview.reference === 'manual'
                  ? t('lightningOps.closePreviewReferenceManual', { fee: closePreview.satPerVbyte })
                  : t('lightningOps.closePreviewReferenceAuto', { fee: closePreview.satPerVbyte })}
              </p>
            </div>
          )}
          {closeFeeStatus && <p className="text-xs text-fog/50">{closeFeeStatus}</p>}
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input type="checkbox" checked={closeForce} onChange={(e) => setCloseForce(e.target.checked)} />
            {t('lightningOps.forceClose')}
          </label>
          <button className="btn-secondary" onClick={handleCloseChannel}>{t('lightningOps.closeChannel')}</button>
          {closeStatus && <p className="text-sm text-brass">{closeStatus}</p>}
        </div>

        <div id={UPDATE_FEES_SECTION_ID} className="section-card space-y-4">
          <h3 className="text-lg font-semibold">{t('lightningOps.updateFees')}</h3>
          <div className="flex flex-wrap gap-3 text-sm">
            <button
              className={feeScopeAll ? 'btn-primary' : 'btn-secondary'}
              onClick={() => setFeeScopeAll(true)}
            >
              {t('lightningOps.applyToAll')}
            </button>
            <button
              className={!feeScopeAll ? 'btn-primary' : 'btn-secondary'}
              onClick={() => setFeeScopeAll(false)}
            >
              {t('lightningOps.applyToOne')}
            </button>
          </div>
          {!feeScopeAll && (
            <select className="input-field" value={feeChannelPoint} onChange={(e) => setFeeChannelPoint(e.target.value)}>
              <option value="">{t('lightningOps.selectChannel')}</option>
              {channelOptions.map((opt) => (
                <option key={opt.value} value={opt.value}>{opt.label}</option>
              ))}
            </select>
          )}
          {feeLoadStatus && (
            <p className="text-xs text-fog/60">{feeLoadStatus}</p>
          )}
          <div className="grid gap-4 lg:grid-cols-3">
            <input
              className="input-field"
              placeholder={t('lightningOps.feeRatePpm')}
              type="number"
              min={0}
              value={feeRatePpm}
              onChange={(e) => setFeeRatePpm(e.target.value)}
            />
            <input
              className="input-field"
              placeholder={t('lightningOps.baseFeeMsats')}
              type="number"
              min={0}
              value={baseFeeMsat}
              onChange={(e) => setBaseFeeMsat(e.target.value)}
            />
            <input
              className="input-field"
              placeholder={t('lightningOps.timeLockDelta')}
              type="number"
              min={0}
              value={timeLockDelta}
              onChange={(e) => setTimeLockDelta(e.target.value)}
            />
          </div>
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input
              type="checkbox"
              checked={inboundEnabled}
              onChange={(e) => setInboundEnabled(e.target.checked)}
            />
            {t('lightningOps.includeInboundFees')}
          </label>
          {inboundEnabled && (
            <div className="grid gap-4 lg:grid-cols-2">
              <input
                className="input-field"
                placeholder={t('lightningOps.inboundFeeRate')}
                type="number"
                value={inboundFeeRatePpm}
                onChange={(e) => setInboundFeeRatePpm(e.target.value)}
              />
              <input
                className="input-field"
                placeholder={t('lightningOps.inboundBaseFee')}
                type="number"
                value={inboundBaseMsat}
                onChange={(e) => setInboundBaseMsat(e.target.value)}
              />
            </div>
          )}
          <p className="text-xs text-fog/50">{t('lightningOps.inboundFeesNote')}</p>
          <button className="btn-secondary" onClick={handleUpdateFees}>{t('lightningOps.updateFees')}</button>
          {feeStatus && <p className="text-sm text-brass">{feeStatus}</p>}
        </div>
      </div>

      <div id={BATCH_OPEN_SECTION_ID} className="section-card space-y-4">
        <div>
          <h3 className="text-lg font-semibold">{t('lightningOps.batchOpenTitle')}</h3>
          <p className="text-sm text-fog/60">{t('lightningOps.batchOpenSubtitle')}</p>
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          <input
            ref={batchPeerInputRef}
            className="input-field"
            placeholder={t('lightningOps.peerAddressPlaceholder')}
            value={batchPeer}
            onChange={(e) => setBatchPeer(e.target.value)}
          />
          <input
            className="input-field"
            placeholder={t('lightningOps.fundingAmount')}
            type="number"
            min={20000}
            value={batchAmount}
            onChange={(e) => setBatchAmount(e.target.value)}
          />
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          <input
            className="input-field"
            placeholder={t('lightningOps.closeAddressOptional')}
            type="text"
            value={batchCloseAddress}
            onChange={(e) => setBatchCloseAddress(e.target.value)}
          />
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input type="checkbox" checked={batchPrivate} onChange={(e) => setBatchPrivate(e.target.checked)} />
            {t('lightningOps.privateChannel')}
          </label>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <button className="btn-secondary" type="button" onClick={handleBatchAddItem} disabled={batchBusy}>
            + {t('lightningOps.batchOpenAdd')}
          </button>
          <span className="text-xs text-fog/60">
            {t('lightningOps.batchOpenItemsCount', { count: batchItems.length })}
          </span>
        </div>
        {batchItems.length > 0 && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 space-y-2">
            {batchItems.map((item) => {
              const alias = peerAliasMap.get(item.pubkey.toLowerCase()) || item.pubkey
              const peerRef = item.host ? `${item.pubkey}@${item.host}` : item.pubkey
              return (
                <div key={item.id} className="flex flex-wrap items-center justify-between gap-3 text-xs text-fog/80 border-b border-white/5 pb-2 last:border-b-0 last:pb-0">
                  <div className="space-y-1">
                    <p className="text-fog">{alias}</p>
                    <p className="text-fog/60 break-all">{peerRef}</p>
                    <p className="text-fog/60">
                      {t('lightningOps.batchOpenItemSummary', {
                        amount: item.local_funding_sat.toLocaleString(),
                        type: item.private ? t('lightningOps.privateChannel') : t('lightningOps.publicChannel'),
                      })}
                    </p>
                  </div>
                  <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleBatchRemoveItem(item.id)} disabled={batchBusy}>
                    {t('lightningOps.batchOpenRemove')}
                  </button>
                </div>
              )
            })}
          </div>
        )}
        <label className="text-sm text-fog/70">
          {t('lightningOps.feeRate')}
          <span className="ml-2 text-xs text-fog/50">
            {t('lightningOps.feeHint', { fastest: openFeeHint?.fastest ?? '-', hour: openFeeHint?.hour ?? '-' })}
          </span>
        </label>
        <div className="flex flex-wrap gap-3 text-sm">
          <button
            className={batchFeeMode === 'auto' ? 'btn-primary' : 'btn-secondary'}
            type="button"
            onClick={() => setBatchFeeMode('auto')}
          >
            {t('lightningOps.closeFeeModeAuto')}
          </button>
          <button
            className={batchFeeMode === 'manual' ? 'btn-primary' : 'btn-secondary'}
            type="button"
            onClick={() => setBatchFeeMode('manual')}
          >
            {t('lightningOps.closeFeeModeManual')}
          </button>
        </div>
        {batchFeeMode === 'auto' && (
          <p className="text-xs text-fog/55">{t('lightningOps.batchOpenFeeAutoHint')}</p>
        )}
        {batchFeeMode === 'manual' && (
          <>
            <div className="flex flex-wrap items-center gap-3">
              <input
                className="input-field flex-1 min-w-[140px]"
                placeholder={t('lightningOps.closeFeeModeManual')}
                type="number"
                min={1}
                value={batchFeeRate}
                onChange={(e) => setBatchFeeRate(e.target.value)}
              />
              <button
                className="btn-secondary text-xs px-3 py-2"
                type="button"
                onClick={() => {
                  if (openFeeHint?.fastest) {
                    setBatchFeeRate(String(openFeeHint.fastest))
                  }
                }}
                disabled={!openFeeHint?.fastest}
              >
                {t('lightningOps.useFastest')}
              </button>
            </div>
            <p className="text-xs text-fog/55">{t('lightningOps.batchOpenFeeManualHint')}</p>
          </>
        )}
        {batchFeeStatus && <p className="text-xs text-fog/50">{batchFeeStatus}</p>}
        {(batchPreviewLoading || batchPreview || batchPreviewStatus) && (
          <div className={`rounded-2xl border p-3 space-y-2 ${batchPreview?.enough_funds === false ? 'border-amber-400/30 bg-amber-500/10' : 'border-white/10 bg-ink/70'}`}>
            <div className="flex items-center justify-between gap-3 text-xs">
              <span className="text-fog/60">{t('lightningOps.batchOpenPreviewTitle')}</span>
              {batchPreview && (
                <span className="text-fog/50">{t('lightningOps.closePreviewEstimated')}</span>
              )}
            </div>
            {batchPreviewLoading && (
              <p className="text-xs text-fog/60">{t('lightningOps.batchOpenPreviewLoading')}</p>
            )}
            {!batchPreviewLoading && batchPreview && (
              <>
                <div className="grid gap-2 text-xs text-fog/80 sm:grid-cols-2">
                  <p>{t('lightningOps.batchOpenPreviewChannels', { count: batchPreview.channel_count })}</p>
                  <p>{t('lightningOps.batchOpenPreviewVbytes', { amount: batchPreview.estimated_vbytes, fee: batchPreview.sat_per_vbyte })}</p>
                  <p>{t('lightningOps.batchOpenPreviewFunding', { amount: formatSatsValue(batchPreview.total_funding_sat) })}</p>
                  <p>{t('lightningOps.batchOpenPreviewFee', { amount: formatSatsValue(batchPreview.fee_sat) })}</p>
                  <p>{t('lightningOps.batchOpenPreviewTotalDebit', { amount: formatSatsValue(batchPreview.total_debit_sat) })}</p>
                  <p>{t('lightningOps.batchOpenPreviewRemaining', { amount: formatSatsValue(batchPreview.spendable_remaining_sat) })}</p>
                  {!batchPreview.reference_only && (
                    <p>{t('lightningOps.batchOpenPreviewInputs', { selected: batchPreview.selected_input_count, amount: formatSatsValue(batchPreview.selected_input_sat) })}</p>
                  )}
                  <p>{t('lightningOps.batchOpenPreviewSpendable', { amount: formatSatsValue(batchPreview.spendable_sat) })}</p>
                </div>
                <p className="text-xs text-fog/55">
                  {batchPreview.reference_only
                    ? t('lightningOps.batchOpenPreviewReferenceFallback', { fee: batchPreview.sat_per_vbyte })
                    : batchFeeMode === 'manual'
                      ? t('lightningOps.batchOpenPreviewReferenceManual', { fee: batchPreview.sat_per_vbyte })
                      : t('lightningOps.batchOpenPreviewReferenceAuto', { fee: batchPreview.sat_per_vbyte })}
                </p>
                <p className={batchPreview.enough_funds ? 'text-xs text-emerald-200' : 'text-xs text-amber-200'}>
                  {batchPreview.enough_funds ? t('lightningOps.batchOpenPreviewEnough') : t('lightningOps.batchOpenPreviewInsufficient')}
                </p>
              </>
            )}
            {!batchPreviewLoading && batchPreviewStatus && (
              <p className={`text-xs ${batchPreview?.enough_funds === false ? 'text-amber-200' : 'text-fog/60'}`}>{batchPreviewStatus}</p>
            )}
          </div>
        )}
        <button className="btn-primary" onClick={handleBatchOpenChannels} disabled={batchBusy || !batchItems.length}>
          {batchBusy ? t('lightningOps.batchOpenRunning') : t('lightningOps.batchOpenAction')}
        </button>
        <p className="text-xs text-fog/50">{t('lightningOps.minimumFundingNote')}</p>
        {batchStatus && <p className="text-sm text-brass">{batchStatus}</p>}
        {batchChannelPoints.length > 0 && (
          <div className="text-xs text-fog/70 space-y-1">
            {batchChannelPoints.map((point) => (
              <a
                key={point}
                className="block text-emerald-200 hover:text-emerald-100 break-all"
                href={mempoolLink(point)}
                target="_blank"
                rel="noopener noreferrer"
              >
                {t('lightningOps.fundingTx', { point })}
              </a>
            ))}
          </div>
        )}
      </div>

      <div id={BALANCED_OPEN_SECTION_ID} className="section-card space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.balancedOpenTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.balancedOpenSubtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${balancedOpenInfo?.enabled ? 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30' : 'bg-white/10 text-fog/60 border border-white/10'}`}>
              {balancedOpenInfo?.enabled ? t('common.enabled') : t('common.disabled')}
            </span>
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${balancedOpenInfo?.available ? 'bg-emerald-500/15 text-emerald-200 border border-emerald-400/30' : 'bg-amber-500/15 text-amber-200 border border-amber-400/30'}`}>
              {balancedOpenInfo?.available ? t('common.ok') : t('common.unavailable')}
            </span>
            <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => refreshBalancedOpen()} disabled={balancedOpenRefreshBusy}>
              {balancedOpenRefreshBusy ? t('lightningOps.balancedOpenRefreshing') : t('common.refresh')}
            </button>
          </div>
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <input
            className="input-field"
            placeholder={t('lightningOps.peerAddressPlaceholder')}
            value={balancedPeer}
            onChange={(e) => setBalancedPeer(e.target.value)}
            disabled={!balancedOpenInfo?.enabled || !balancedOpenInfo?.available}
          />
          <input
            className="input-field"
            placeholder={t('lightningOps.balancedOpenCapacity')}
            type="number"
            min={20000}
            step={2}
            value={balancedCapacity}
            onChange={(e) => setBalancedCapacity(e.target.value)}
            disabled={!balancedOpenInfo?.enabled || !balancedOpenInfo?.available}
          />
        </div>
        <div className="grid gap-4 lg:grid-cols-2">
          <div className="space-y-2">
            <label className="text-sm text-fog/70">
              {t('lightningOps.feeRate')}
              <span className="ml-2 text-xs text-fog/50">
                {t('lightningOps.feeHint', { fastest: openFeeHint?.fastest ?? '-', hour: openFeeHint?.hour ?? '-' })}
              </span>
            </label>
            <div className="flex flex-wrap items-center gap-3">
              <input
                className="input-field flex-1 min-w-[140px]"
                placeholder={t('common.auto')}
                type="number"
                min={1}
                value={balancedFeeRate}
                onChange={(e) => setBalancedFeeRate(e.target.value)}
                disabled={!balancedOpenInfo?.enabled || !balancedOpenInfo?.available}
              />
              <button
                className="btn-secondary text-xs px-3 py-2"
                type="button"
                onClick={() => {
                  if (openFeeHint?.fastest) {
                    setBalancedFeeRate(String(openFeeHint.fastest))
                  }
                }}
                disabled={!openFeeHint?.fastest || !balancedOpenInfo?.enabled || !balancedOpenInfo?.available}
              >
                {t('lightningOps.useFastest')}
              </button>
            </div>
            {balancedFeeStatus && <p className="text-xs text-fog/50">{balancedFeeStatus}</p>}
          </div>
          <input
            className="input-field"
            placeholder={t('lightningOps.closeAddressOptional')}
            type="text"
            value={balancedCloseAddress}
            onChange={(e) => setBalancedCloseAddress(e.target.value)}
            disabled={!balancedOpenInfo?.enabled || !balancedOpenInfo?.available}
          />
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input type="checkbox" checked={balancedPrivate} onChange={(e) => setBalancedPrivate(e.target.checked)} disabled={!balancedOpenInfo?.enabled || !balancedOpenInfo?.available} />
            {t('lightningOps.privateChannel')}
          </label>
          <button className="btn-primary" type="button" onClick={handleBalancedOpenCreateAndPropose} disabled={balancedOpenBusy || !balancedOpenInfo?.enabled || !balancedOpenInfo?.available}>
            {balancedOpenBusy ? t('lightningOps.balancedOpenRunning') : t('lightningOps.balancedOpenCreateAndPropose')}
          </button>
        </div>
        {balancedOpenInfo?.enabled && balancedOpenInfo?.available && balancedCapacitySat > 0 && (
          <div className="rounded-xl border border-white/10 bg-black/20 p-3 text-xs space-y-1">
            <p className="text-fog/80">
              {t('lightningOps.balancedOpenFundsPreviewTitle', {
                feeRate: balancedFeeRateEstimateSatVb,
                fundingVbytes: BALANCED_OPEN_FUNDING_VBYTES,
              })}
            </p>
            <p className="text-fog/70">
              {t('lightningOps.balancedOpenFundsPreviewSpendable', {
                required: balancedRequiredSpendableSat.toLocaleString(),
                current: balancedWalletSpendableSat.toLocaleString(),
              })}
            </p>
            <p className="text-fog/70">
              {t('lightningOps.balancedOpenFundsPreviewConfirmed', {
                required: balancedRequiredConfirmedSat.toLocaleString(),
                current: balancedWalletConfirmedSat.toLocaleString(),
              })}
            </p>
            <p className={balancedSpendableEnough && balancedConfirmedEnough ? 'text-emerald-200' : 'text-amber-200'}>
              {balancedSpendableEnough && balancedConfirmedEnough
                ? t('lightningOps.balancedOpenFundsPreviewEnough')
                : t('lightningOps.balancedOpenFundsPreviewInsufficient')}
            </p>
            {!balancedSpendableEnough && (
              <p className="text-fog/60">
                {t('lightningOps.balancedOpenFundsPreviewWhy', {
                  anchor: balancedWalletReservedAnchorSat.toLocaleString(),
                  remaining: BALANCED_OPEN_REQUIRED_REMAINING_SAT.toLocaleString(),
                })}
              </p>
            )}
            {balancedOpenInfo?.wallet_error ? <p className="text-amber-200">{balancedOpenInfo.wallet_error}</p> : null}
          </div>
        )}
        <p className="text-xs text-fog/50">{t('lightningOps.balancedOpenNote')}</p>
        {balancedOpenInfo?.error && !balancedOpenInfo?.available && (
          <p className="text-xs text-amber-200">{balancedOpenInfo.error}</p>
        )}
        {balancedOpenStatus && <p className="text-sm text-brass">{balancedOpenStatus}</p>}

        {balancedOpenSessions.length > 0 ? (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 space-y-2 max-h-[320px] overflow-y-auto">
            {balancedOpenSessions.map((session) => {
              const alias = peerAliasMap.get((session.peer_pubkey || '').toLowerCase()) || session.peer_pubkey
              const oneSideSat = Math.floor(Number(session.capacity_sat || 0) / 2)
              const sessionBusy = Boolean(balancedOpenActionBusyID && balancedOpenActionBusyID.endsWith(`:${session.session_id}`))
              const detailsOpen = balancedOpenDetailsSessionID === session.session_id
              const mempoolHealth = balancedOpenMempoolHealth(session)
              const lastAutoRetry = balancedOpenLastAutoRetryLabel(session)
              const retryTone: 'ok' | 'warn' | 'muted' = lastAutoRetry === t('lightningOps.balancedOpenHealthAutoRetryNone') ? 'muted' : 'ok'
              return (
                <div key={session.session_id} className="flex flex-wrap items-start justify-between gap-3 border-b border-white/5 pb-2 last:border-b-0 last:pb-0">
                  <div className="space-y-1 text-xs text-fog/75">
                    <p className="text-fog">
                      {alias} - <span className="uppercase">{session.role}</span>
                    </p>
                    <p className="text-fog/60 break-all">{session.peer_pubkey}{session.peer_host ? `@${session.peer_host}` : ''}</p>
                    <p>
                      {t('lightningOps.balancedOpenCapacityLabel', {
                        total: Number(session.capacity_sat || 0).toLocaleString(),
                        each: oneSideSat.toLocaleString(),
                      })}
                    </p>
                    <p>{t('lightningOps.balancedOpenStateLabel', { state: balancedOpenStateLabel(session.state) })}</p>
                    <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
                      <span className={`text-[11px] px-2 py-0.5 rounded-full ${badgeClass('muted')}`}>
                        {t('lightningOps.balancedOpenHealthRole', { role: balancedOpenRoleLabel(session.role) })}
                      </span>
                      <span className={`text-[11px] px-2 py-0.5 rounded-full ${badgeClass(mempoolHealth.tone)}`}>
                        {mempoolHealth.label}
                      </span>
                      <span className={`text-[11px] px-2 py-0.5 rounded-full ${badgeClass(retryTone)}`}>
                        {t('lightningOps.balancedOpenHealthAutoRetry', { time: lastAutoRetry })}
                      </span>
                    </div>
                    <p className="text-fog/60">{t('lightningOps.balancedOpenUpdatedAt', { time: formatBalancedDate(session.state_updated_at) })}</p>
                    {session.last_error ? <p className="text-amber-200">{session.last_error}</p> : null}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {canProposeBalancedSession(session) && (
                      <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleBalancedOpenAction(session, 'propose')} disabled={sessionBusy}>
                        {t('lightningOps.balancedOpenActionPropose')}
                      </button>
                    )}
                    {canAcceptBalancedSession(session) && (
                      <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleBalancedOpenAction(session, 'accept')} disabled={sessionBusy}>
                        {t('lightningOps.balancedOpenActionAccept')}
                      </button>
                    )}
                    {canExecuteBalancedSession(session) && (
                      <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleBalancedOpenAction(session, 'execute')} disabled={sessionBusy}>
                        {t('lightningOps.balancedOpenActionExecute')}
                      </button>
                    )}
                    {canRetryBalancedBroadcastSession(session) && (
                      <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleBalancedOpenAction(session, 'retry_broadcast')} disabled={sessionBusy}>
                        {t('lightningOps.balancedOpenActionRetryBroadcast')}
                      </button>
                    )}
                    {canRecoverBalancedSession(session) && (
                      <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleBalancedOpenAction(session, 'recover')} disabled={sessionBusy}>
                        {t('lightningOps.balancedOpenActionRecover')}
                      </button>
                    )}
                    {canCancelBalancedSession(session) && (
                      <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleBalancedOpenAction(session, 'cancel')} disabled={sessionBusy}>
                        {t('lightningOps.balancedOpenActionCancel')}
                      </button>
                    )}
                    <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleBalancedOpenToggleDetails(session.session_id)} disabled={sessionBusy}>
                      {detailsOpen ? t('common.hide') : t('lightningOps.balancedOpenActionDetails')}
                    </button>
                  </div>
                </div>
              )
            })}
          </div>
        ) : (
          <p className="text-xs text-fog/50">{t('lightningOps.balancedOpenNoSessions')}</p>
        )}
        {balancedOpenDetailsSessionID && balancedOpenSelectedSession && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="text-xs text-fog/70">{t('lightningOps.balancedOpenEventsTitle')}</p>
              <button
                className="btn-secondary text-xs px-3 py-1.5"
                type="button"
                onClick={() => fetchBalancedOpenEvents(balancedOpenDetailsSessionID)}
                disabled={balancedOpenSelectedEventsLoading}
              >
                {balancedOpenSelectedEventsLoading ? t('lightningOps.balancedOpenRefreshing') : t('common.refresh')}
              </button>
            </div>
            <p className="text-xs text-fog/60 break-all">{balancedOpenSelectedSession.session_id}</p>
            {balancedOpenSelectedEventsError ? <p className="text-xs text-amber-200">{balancedOpenSelectedEventsError}</p> : null}
            {balancedOpenSelectedEventsLoading ? (
              <p className="text-xs text-fog/60">{t('lightningOps.balancedOpenEventsLoading')}</p>
            ) : balancedOpenSelectedEvents.length > 0 ? (
              <div className="space-y-2 max-h-[260px] overflow-y-auto">
                {balancedOpenSelectedEvents.map((event) => {
                  const rawDetail = formatBalancedEventDetail(event.detail)
                  return (
                    <div key={`${event.id}-${event.created_at}`} className="rounded-xl border border-white/10 bg-black/20 p-2 text-xs text-fog/75 space-y-1">
                      <p className="text-fog">{formatBalancedEventType(event.event_type)}</p>
                      <p className="text-fog/60">{formatBalancedDate(event.created_at)}</p>
                      {rawDetail ? (
                        <pre className="text-[11px] text-fog/70 whitespace-pre-wrap break-all">{rawDetail}</pre>
                      ) : null}
                    </div>
                  )
                })}
              </div>
            ) : (
              <p className="text-xs text-fog/60">{t('lightningOps.balancedOpenEventsEmpty')}</p>
            )}
          </div>
        )}
      </div>

      <div id={WATCHTOWER_SECTION_ID} className="section-card space-y-4">
        <div>
          <h3 className="text-lg font-semibold">{t('lightningOps.watchtowerTitle')}</h3>
          <p className="text-sm text-fog/60">{t('lightningOps.watchtowerSubtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <input
            className="input-field flex-1 min-w-[220px]"
            placeholder={t('lightningOps.watchtowerAddressPlaceholder')}
            value={watchtowerAddress}
            onChange={(e) => setWatchtowerAddress(e.target.value)}
          />
          <button className="btn-secondary" type="button" onClick={handleAddWatchtower} disabled={watchtowerBusy}>
            + {t('lightningOps.watchtowerAdd')}
          </button>
        </div>
        <p className="text-xs text-fog/60">{t('lightningOps.watchtowerCount', { count: watchtowers.length })}</p>
        {watchtowers.length > 0 ? (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 space-y-2">
            {watchtowers.map((tower) => (
              <div key={tower.pubkey} className="flex flex-wrap items-center justify-between gap-3 text-xs text-fog/80 border-b border-white/5 pb-2 last:border-b-0 last:pb-0">
                <div className="space-y-1">
                  <p className="text-fog break-all">{tower.pubkey}</p>
                  <p className="text-fog/60 break-all">
                    {Array.isArray(tower.addresses) && tower.addresses.length > 0 ? tower.addresses.join(' | ') : t('common.na')}
                  </p>
                  <p className="text-fog/60">
                    {t('lightningOps.watchtowerSessions', { count: Number(tower.num_sessions || 0) })} | {tower.active_session_candidate ? t('common.enabled') : t('common.disabled')}
                  </p>
                </div>
                <button className="btn-secondary text-xs px-3 py-1.5" type="button" onClick={() => handleRemoveWatchtower(tower.pubkey)} disabled={watchtowerBusy}>
                  {t('lightningOps.watchtowerRemove')}
                </button>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-xs text-fog/50">{t('lightningOps.watchtowerNoItems')}</p>
        )}
        {watchtowerStatus && <p className="text-sm text-brass">{watchtowerStatus}</p>}
      </div>

      <div id={HTLC_MANAGER_SECTION_ID} className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.htlcManagerTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.htlcManagerSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(htlcManagerTone())}`}>
              {htlcManagerBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${htlcManagerBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleHtlcManager}
              type="button"
              disabled={htlcManagerBusy}
              aria-label={t('lightningOps.toggleHtlcManager')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${htlcManager?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!htlcManager?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${htlcManager?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <label className="text-sm text-fog/70">
            {t('lightningOps.htlcManagerMinSat')}
            <input
              className="input-field mt-2"
              type="number"
              min={1}
              value={htlcManagerMinSat}
              onChange={(e) => {
                setHtlcManagerMinSat(e.target.value)
                htlcManagerFormDirtyRef.current = true
              }}
            />
          </label>
          <label className="text-sm text-fog/70">
            {t('lightningOps.htlcManagerMaxPct')}
            <input
              className="input-field mt-2"
              type="number"
              min={0}
              value={htlcManagerMaxPct}
              onChange={(e) => {
                setHtlcManagerMaxPct(e.target.value)
                htlcManagerFormDirtyRef.current = true
              }}
            />
          </label>
          <label className="text-sm text-fog/70">
            {t('lightningOps.htlcManagerInterval')}
            <input
              className="input-field mt-2"
              type="number"
              min={1}
              max={2880}
              value={htlcManagerIntervalMinutes}
              onChange={(e) => {
                setHtlcManagerIntervalMinutes(e.target.value)
                htlcManagerFormDirtyRef.current = true
              }}
            />
          </label>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={handleSaveHtlcManager} disabled={htlcManagerBusy}>
            {t('common.save')}
          </button>
          <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={handleRunHtlcManagerNow} disabled={htlcManagerBusy}>
            {t('lightningOps.htlcManagerRunNow')}
          </button>
          <button
            className="btn-secondary text-xs px-3 py-2"
            type="button"
            onClick={() => setHtlcManagerLogsOpen((open) => !open)}
          >
            {htlcManagerLogsOpen ? t('common.hide') : t('lightningOps.htlcManagerLogsShow', { count: htlcManagerLogs.length })}
          </button>
          <button
            className="btn-secondary text-xs px-3 py-2"
            type="button"
            onClick={() => setHtlcManagerFailedOpen((open) => !open)}
          >
            {htlcManagerFailedOpen ? t('common.hide') : t('lightningOps.htlcManagerFailedShow', { count: visibleHtlcManagerFailed.length })}
          </button>
        </div>
        {htlcManagerStatus && <p className="text-sm text-brass">{htlcManagerStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-3">
          <div>
            {t('lightningOps.htlcManagerLastRun')}: <span className="text-fog">{formatAmbossTime(htlcManager?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.htlcManagerLastAttempt')}: <span className="text-fog">{formatAmbossTime(htlcManager?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.htlcManagerLastChanged')}: <span className="text-fog">{htlcManager?.last_changed_count ?? 0}</span>
          </div>
        </div>
        {htlcManager?.last_error && (
          <p className="text-xs text-amber-200">
            {t('lightningOps.htlcManagerLastError')}: {htlcManager.last_error}
          </p>
        )}
        {htlcManagerLogsOpen && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 max-h-[260px] overflow-y-auto">
            {htlcManagerLogs.length ? (
              <div className="space-y-2 text-xs text-fog/70">
                {htlcManagerLogs.map((entry, idx) => (
                  <p key={`${entry.ts}-${entry.channel_point}-${idx}`}>
                    {t('lightningOps.htlcManagerLogLine', {
                      ts: formatAmbossTime(entry.ts),
                      alias: entry.alias || entry.channel_point,
                      oldMinSat: Math.round((entry.old_min_msat || 0) / 1000),
                      newMinSat: Math.round((entry.new_min_msat || 0) / 1000),
                      oldMaxSat: Math.round((entry.old_max_msat || 0) / 1000),
                      newMaxSat: Math.round((entry.new_max_msat || 0) / 1000)
                    })}
                  </p>
                ))}
              </div>
            ) : (
              <p className="text-xs text-fog/50">{t('lightningOps.htlcManagerLogsEmpty')}</p>
            )}
          </div>
        )}
        {htlcManagerFailedOpen && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3">
            {visibleHtlcManagerFailed.length ? (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[940px] text-xs text-fog/80">
                  <thead>
                    <tr className="text-left text-fog/60">
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedTs')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedChanIn')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedChanOut')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedInAmt')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedOutAmt')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedPotentialFee')}</th>
                      <th className="py-2 pr-3">{t('lightningOps.htlcManagerFailedCode')}</th>
                      <th className="py-2">{t('lightningOps.htlcManagerFailedDetail')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleHtlcManagerFailed.map((entry, idx) => (
                      <tr key={`${entry.ts}-${entry.incoming_channel_id}-${entry.outgoing_channel_id}-${idx}`} className="border-t border-white/5">
                        <td className="py-2 pr-3 whitespace-nowrap">{formatAmbossTime(entry.ts)}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">
                          <div className="text-fog">{entry.incoming_alias || '-'}</div>
                          <div className="text-[11px] text-fog/50">{entry.incoming_channel_id || '-'}</div>
                        </td>
                        <td className="py-2 pr-3 whitespace-nowrap">
                          <div className="text-fog">{entry.outgoing_alias || '-'}</div>
                          <div className="text-[11px] text-fog/50">{entry.outgoing_channel_id || '-'}</div>
                        </td>
                        <td className="py-2 pr-3 whitespace-nowrap">{formatSatFromMsat(entry.incoming_amt_msat)}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">{formatSatFromMsat(entry.outgoing_amt_msat)}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">{formatFeeMsat(entry.potential_fee_msat)}</td>
                        <td className="py-2 pr-3 whitespace-nowrap">
                          {(entry.failure_code || '-')}
                          {entry.event ? <span className="ml-1 text-fog/50">({entry.event})</span> : null}
                        </td>
                        <td className="py-2">
                          {entry.failure_detail || entry.failure_reason || '-'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="text-xs text-fog/50">{t('lightningOps.htlcManagerFailedEmpty')}</p>
            )}
          </div>
        )}
      </div>

      <div id={AMBOSS_HEALTH_SECTION_ID} className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.ambossHealthTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.ambossHealthSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(ambossTone())}`}>
              {ambossBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${ambossBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleAmboss}
              type="button"
              disabled={ambossBusy}
              aria-label={t('lightningOps.toggleAmbossHealth')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${amboss?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!amboss?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${amboss?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        {ambossStatus && <p className="text-sm text-brass">{ambossStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-3">
          <div>
            {t('lightningOps.ambossHealthLastPing')}: <span className="text-fog">{formatAmbossTime(amboss?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.ambossHealthLastAttempt')}: <span className="text-fog">{formatAmbossTime(amboss?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.ambossHealthInterval')}: <span className="text-fog">{amboss?.interval_sec ? `${amboss.interval_sec}s` : '-'}</span>
          </div>
        </div>
      {amboss?.last_error && (
        <p className="text-xs text-amber-200">
          {t('lightningOps.ambossHealthLastError')}: {amboss.last_error}
        </p>
      )}
    </div>

      <div id={CHAN_HEAL_SECTION_ID} className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.chanHealTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.chanHealSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(chanHealTone())}`}>
              {chanHealBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${chanHealBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleChanHeal}
              type="button"
              disabled={chanHealBusy}
              aria-label={t('lightningOps.toggleChanHeal')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${chanHeal?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!chanHeal?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${chanHeal?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <label className="text-sm text-fog/70">
            {t('lightningOps.chanHealInterval')}
          </label>
          <div className="flex items-center gap-2">
            <input
              className="input-field w-32"
              type="number"
              min={30}
              value={chanHealInterval}
              onChange={(e) => {
                setChanHealInterval(e.target.value)
                chanHealIntervalDirtyRef.current = true
              }}
            />
            <button
              className="btn-secondary text-xs px-3 py-2"
              type="button"
              onClick={handleSaveChanHealInterval}
              disabled={chanHealBusy}
            >
              {t('common.save')}
            </button>
          </div>
        </div>
        {chanHealStatus && <p className="text-sm text-brass">{chanHealStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-3">
          <div>
            {t('lightningOps.chanHealLastRun')}: <span className="text-fog">{formatAmbossTime(chanHeal?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.chanHealLastAttempt')}: <span className="text-fog">{formatAmbossTime(chanHeal?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.chanHealInterval')}: <span className="text-fog">{chanHeal?.interval_sec ? `${chanHeal.interval_sec}s` : '-'}</span>
          </div>
        </div>
        {chanHeal?.last_ok_at && typeof chanHeal?.last_updated === 'number' && (
          <p className="text-xs text-fog/60">
            {t('lightningOps.chanHealLastUpdated', { count: chanHeal.last_updated })}
          </p>
        )}
        {chanHeal?.last_error && (
          <p className="text-xs text-amber-200">
            {t('lightningOps.chanHealLastError')}: {chanHeal.last_error}
          </p>
        )}
      </div>

      <div id={TOR_PEER_SECTION_ID} className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.torPeerTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.torPeerSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(torPeerCheckerTone())}`}>
              {torPeerCheckerBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${torPeerCheckerBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleTorPeerChecker}
              type="button"
              disabled={torPeerCheckerBusy}
              aria-label={t('lightningOps.toggleTorPeerChecker')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${torPeerChecker?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!torPeerChecker?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${torPeerChecker?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <label className="text-sm text-fog/70">
            {t('lightningOps.torPeerInterval')}
          </label>
          <div className="flex items-center gap-2">
            <input
              className="input-field w-32"
              type="number"
              min={2}
              max={168}
              value={torPeerCheckerIntervalHours}
              onChange={(e) => {
                setTorPeerCheckerIntervalHours(e.target.value)
                torPeerCheckerIntervalDirtyRef.current = true
              }}
            />
            <button
              className="btn-secondary text-xs px-3 py-2"
              type="button"
              onClick={handleSaveTorPeerChecker}
              disabled={torPeerCheckerBusy}
            >
              {t('common.save')}
            </button>
          </div>
          <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={handleRunTorPeerCheckerNow} disabled={torPeerCheckerBusy}>
            {t('lightningOps.torPeerRunNow')}
          </button>
          <button
            className="btn-secondary text-xs px-3 py-2"
            type="button"
            onClick={() => setTorPeerCheckerLogsOpen((open) => !open)}
          >
            {torPeerCheckerLogsOpen ? t('common.hide') : t('lightningOps.torPeerLogsShow', { count: torPeerCheckerLogs.length })}
          </button>
        </div>
        {torPeerCheckerStatus && <p className="text-sm text-brass">{torPeerCheckerStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-4">
          <div>
            {t('lightningOps.torPeerLastRun')}: <span className="text-fog">{formatAmbossTime(torPeerChecker?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.torPeerLastAttempt')}: <span className="text-fog">{formatAmbossTime(torPeerChecker?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.torPeerLastChecked')}: <span className="text-fog">{torPeerChecker?.last_checked_count ?? 0}</span>
          </div>
          <div>
            {t('lightningOps.torPeerLastSwitched')}: <span className="text-fog">{torPeerChecker?.last_switched_count ?? 0}</span>
          </div>
        </div>
        {torPeerChecker?.last_error && (
          <p className="text-xs text-amber-200">
            {t('lightningOps.torPeerLastError')}: {torPeerChecker.last_error}
          </p>
        )}
        {torPeerCheckerLogsOpen && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3 max-h-[260px] overflow-y-auto">
            {torPeerCheckerLogs.length ? (
              <div className="space-y-2 text-xs text-fog/70">
                {torPeerCheckerLogs.map((entry, idx) => (
                  <p key={`${entry.ts}-${entry.pub_key || entry.alias}-${idx}`}>
                    {t('lightningOps.torPeerLogLine', {
                      ts: formatAmbossTime(entry.ts),
                      alias: entry.alias || entry.pub_key || '-',
                      result: entry.result || '-',
                      detail: entry.detail || '-',
                      from: entry.from_address || '-',
                      to: entry.to_address || '-'
                    })}
                  </p>
                ))}
              </div>
            ) : (
              <p className="text-xs text-fog/50">{t('lightningOps.torPeerLogsEmpty')}</p>
            )}
          </div>
        )}
      </div>

      <div id={FAILED_PAYMENTS_CLEANER_SECTION_ID} className="section-card space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.failedPaymentsCleanerTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.failedPaymentsCleanerSubtitle')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className={`text-[11px] uppercase tracking-wide px-2 py-0.5 rounded-full ${badgeClass(failedPaymentsCleanerTone())}`}>
              {failedPaymentsCleanerBadgeLabel()}
            </span>
            <button
              className={`relative flex h-9 w-36 items-center rounded-full border border-white/10 bg-ink/60 px-2 transition ${failedPaymentsCleanerBusy ? 'opacity-70' : 'hover:border-white/30'}`}
              onClick={handleToggleFailedPaymentsCleaner}
              type="button"
              disabled={failedPaymentsCleanerBusy}
              aria-label={t('lightningOps.toggleFailedPaymentsCleaner')}
            >
              <span
                className={`absolute top-1 h-7 w-16 rounded-full bg-glow shadow transition-all ${failedPaymentsCleaner?.enabled ? 'left-[70px]' : 'left-[6px]'}`}
              />
              <span className={`relative z-10 flex-1 text-center text-xs ${!failedPaymentsCleaner?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.disabled')}</span>
              <span className={`relative z-10 flex-1 text-center text-xs ${failedPaymentsCleaner?.enabled ? 'text-ink' : 'text-fog/60'}`}>{t('common.enabled')}</span>
            </button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <label className="text-sm text-fog/70">
            {t('lightningOps.failedPaymentsCleanerInterval')}
          </label>
          <div className="flex items-center gap-2">
            <input
              className="input-field w-32"
              type="number"
              min={1}
              max={168}
              value={failedPaymentsCleanerIntervalHours}
              onChange={(e) => {
                setFailedPaymentsCleanerIntervalHours(e.target.value)
                failedPaymentsCleanerIntervalDirtyRef.current = true
              }}
            />
            <button
              className="btn-secondary text-xs px-3 py-2"
              type="button"
              onClick={handleSaveFailedPaymentsCleaner}
              disabled={failedPaymentsCleanerBusy}
            >
              {t('common.save')}
            </button>
          </div>
          <button className="btn-secondary text-xs px-3 py-2" type="button" onClick={handleRunFailedPaymentsCleanerNow} disabled={failedPaymentsCleanerBusy}>
            {t('lightningOps.failedPaymentsCleanerRunNow')}
          </button>
        </div>
        {failedPaymentsCleanerStatus && <p className="text-sm text-brass">{failedPaymentsCleanerStatus}</p>}
        <div className="grid gap-3 text-xs text-fog/70 lg:grid-cols-4">
          <div>
            {t('lightningOps.failedPaymentsCleanerLastRun')}: <span className="text-fog">{formatAmbossTime(failedPaymentsCleaner?.last_ok_at)}</span>
          </div>
          <div>
            {t('lightningOps.failedPaymentsCleanerLastAttempt')}: <span className="text-fog">{formatAmbossTime(failedPaymentsCleaner?.last_attempt_at)}</span>
          </div>
          <div>
            {t('lightningOps.failedPaymentsCleanerLastDeleted')}: <span className="text-fog">{failedPaymentsCleaner?.last_deleted_count ?? 0}</span>
          </div>
          <div>
            {t('lightningOps.failedPaymentsCleanerInterval')}: <span className="text-fog">{failedPaymentsCleaner?.interval_hours ? `${failedPaymentsCleaner.interval_hours}h` : '-'}</span>
          </div>
        </div>
        {failedPaymentsCleaner?.last_error && (
          <p className="text-xs text-amber-200">
            {t('lightningOps.failedPaymentsCleanerLastError')}: {failedPaymentsCleaner.last_error}
          </p>
        )}
      </div>

      <div id={SIGN_MESSAGE_SECTION_ID} className="section-card space-y-4">
        <div>
          <h3 className="text-lg font-semibold">{t('lightningOps.signMessageTitle')}</h3>
          <p className="text-sm text-fog/60">{t('lightningOps.signMessageSubtitle')}</p>
        </div>
        <textarea
          className="input-field min-h-[120px]"
          placeholder={t('lightningOps.signMessagePlaceholder')}
          value={signMessage}
          onChange={(e) => {
            const value = e.target.value
            setSignMessage(value)
            if (signSignature) setSignSignature('')
            if (signCopied) setSignCopied(false)
            if (signStatus) setSignStatus('')
          }}
        />
        <div className="flex flex-wrap items-center gap-3">
          <button
            className="btn-primary disabled:opacity-60 disabled:cursor-not-allowed"
            onClick={handleSignMessage}
            disabled={signBusy || !signMessage.trim()}
            type="button"
          >
            {signBusy ? t('lightningOps.signMessageSigning') : t('lightningOps.signMessageAction')}
          </button>
          {signStatus && <p className="text-sm text-brass">{signStatus}</p>}
        </div>
        {signSignature && (
          <div className="rounded-2xl border border-white/10 bg-ink/60 p-3">
            <div className="flex items-center justify-between text-xs text-fog/60">
              <span>{t('lightningOps.signatureLabel')}</span>
              <button
                className="text-fog/50 hover:text-fog"
                onClick={handleCopySignature}
                title={t('lightningOps.copySignature')}
                aria-label={t('lightningOps.copySignature')}
                type="button"
              >
                <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.6">
                  <rect x="9" y="9" width="11" height="11" rx="2" />
                  <rect x="4" y="4" width="11" height="11" rx="2" />
                </svg>
              </button>
            </div>
            <p className="mt-2 text-xs font-mono break-all">{signSignature}</p>
            {signCopied && <p className="mt-2 text-xs text-fog/60">{t('common.copied')}</p>}
          </div>
        )}
      </div>

          </div>
        )}
      </div>

      <div id={PEERS_SECTION_ID} className="section-card order-3 space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-lg font-semibold">{t('lightningOps.peers')}</h3>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs text-fog/60">{t('lightningOps.connectedPeers', { count: peers.length })}</span>
            <button
              type="button"
              className="btn-secondary text-xs px-3 py-2"
              onClick={() => setPeersOpen((open) => !open)}
              aria-expanded={peersOpen}
            >
              {peersOpen ? t('common.hide') : t('common.open')}
            </button>
          </div>
        </div>
        {peersOpen && (
          <>
        {peerActionStatus && <p className="text-sm text-brass">{peerActionStatus}</p>}
        {peerListStatus && <p className="text-sm text-brass">{peerListStatus}</p>}
        {peers.length ? (
          <div className="max-h-[520px] overflow-y-auto pr-2">
            <div className="grid gap-3">
              {peers.map((peer) => {
                const isFocusedPeer = focusedPeerPubKey === peer.pub_key
                const peerCardClass = isFocusedPeer
                  ? 'rounded-2xl border border-sky-300/70 bg-sky-500/10 p-4 ring-1 ring-sky-300/70'
                  : 'rounded-2xl border border-white/10 bg-ink/60 p-4'
                return (
                  <div key={peer.pub_key} id={peerCardID(peer.pub_key)} className={peerCardClass}>
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <p className="text-sm text-fog/60">{peer.alias || peer.pub_key || t('lightningOps.unknownPeer')}</p>
                        {peerProfileLinkGroup(peer.pub_key)}
                        <p className="text-xs text-fog/50">{peer.address || t('lightningOps.addressUnknown')}</p>
                      </div>
                      <div className="flex items-center gap-2">
                        <span className="rounded-full px-3 py-1 text-xs bg-white/10 text-fog/70">
                          {peer.inbound ? t('lightningOps.inbound') : t('lightningOps.outbound')}
                        </span>
                        <button className="btn-secondary text-xs px-3 py-1.5" onClick={() => handleDisconnect(peer.pub_key)}>
                          {t('lightningOps.disconnect')}
                        </button>
                      </div>
                    </div>
                    {peer.alias && (
                      <p className="mt-2 text-xs text-fog/50">{t('lightningOps.pubkeyLabel', { pubkey: peer.pub_key })}</p>
                    )}
                    <div className="mt-3 grid gap-3 lg:grid-cols-3 text-xs text-fog/70">
                      <div>{t('lightningOps.satSent', { value: peer.sat_sent })}</div>
                      <div>{t('lightningOps.satRecv', { value: peer.sat_recv })}</div>
                      <div>{t('lightningOps.pingLabel', { value: formatPing(peer.ping_time) })}</div>
                    </div>
                    <div className="mt-2 grid gap-3 lg:grid-cols-2 text-xs text-fog/60">
                      <div>{t('lightningOps.bytesSent', { value: peer.bytes_sent })}</div>
                      <div>{t('lightningOps.bytesRecv', { value: peer.bytes_recv })}</div>
                    </div>
                    {peer.sync_type && (
                      <p className="mt-2 text-xs text-fog/50">{t('lightningOps.syncLabel', { value: peer.sync_type })}</p>
                    )}
                    {peer.last_error && (
                      <p className="mt-2 text-xs text-ember">
                        {t('lightningOps.lastError', {
                          age: peer.last_error_time ? ` (${formatAge(peer.last_error_time)})` : '',
                          error: peer.last_error
                        })}
                      </p>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        ) : (
          <p className="text-sm text-fog/60">{t('lightningOps.noConnectedPeers')}</p>
        )}
          </>
        )}
      </div>

      <div className="section-card order-4 space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold">{t('lightningOps.closedChannelsTitle')}</h3>
            <p className="text-sm text-fog/60">{t('lightningOps.closedChannelsSubtitle')}</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs text-fog/60">{t('lightningOps.closedChannelsCount', { count: closedChannels.length })}</span>
            <button
              type="button"
              className="btn-secondary text-xs px-3 py-2"
              onClick={() => setClosedChannelsOpen((open) => !open)}
              aria-expanded={closedChannelsOpen}
            >
              {closedChannelsOpen ? t('common.hide') : t('common.open')}
            </button>
          </div>
        </div>
        {closedChannelsOpen && (
          <>
        <div className="flex flex-col gap-3 lg:flex-row lg:items-center">
          <input
            className="input-field lg:flex-1"
            placeholder={t('lightningOps.closedChannelsSearchPlaceholder')}
            value={closedChannelSearch}
            onChange={(e) => setClosedChannelSearch(e.target.value)}
          />
          <select
            className="input-field lg:w-56"
            value={closedChannelFilter}
            onChange={(e) => setClosedChannelFilter(e.target.value as 'all' | 'cooperative' | 'force' | 'breach' | 'other')}
          >
            <option value="all">{t('common.all')}</option>
            <option value="cooperative">{t('lightningOps.closedChannelsFilterCooperative')}</option>
            <option value="force">{t('lightningOps.closedChannelsFilterForce')}</option>
            <option value="breach">{t('lightningOps.closedChannelsFilterBreach')}</option>
            <option value="other">{t('lightningOps.closedChannelsFilterOther')}</option>
          </select>
        </div>
        {closedChannelStatus && <p className="text-sm text-brass">{closedChannelStatus}</p>}
        {filteredClosedChannels.length ? (
          <>
            <div className="space-y-3 md:hidden">
              {filteredClosedChannels.map((item) => {
                const peerLabel = item.peer_alias || item.remote_pubkey || t('lightningOps.unknownPeer')
                const pointLink = mempoolLink(item.channel_point || '')
                const closingTxLink = mempoolTxLink(item.closing_tx_hash)
                const closedAtLabel = formatClosedAt(item.closed_at)
                const sweepTxs = Array.isArray(item.resolutions)
                  ? item.resolutions.map((resolution) => String(resolution?.sweep_txid || '').trim()).filter(Boolean)
                  : []
                return (
                  <div key={`${item.chan_id}-${item.channel_point || item.closing_tx_hash || peerLabel}`} className="rounded-2xl border border-white/10 bg-ink/60 p-4">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <p className="text-sm text-fog break-all">{peerLabel}</p>
                        {peerProfileLinkGroup(item.remote_pubkey)}
                        {closedAtLabel ? (
                          <p className="mt-1 text-xs text-fog/50">{closedAtLabel}</p>
                        ) : (
                          <p className="mt-1 text-xs text-fog/50">{formatCloseHeight(item.close_height)}</p>
                        )}
                        {closedAtLabel && (
                          <p className="mt-1 text-[11px] text-fog/40">{formatCloseHeight(item.close_height)}</p>
                        )}
                      </div>
                      <span className={`rounded-full px-2 py-1 text-[11px] ${closedChannelBadgeClass(item)}`}>
                        {normalizeClosedChannelType(item.close_type_label)}
                      </span>
                    </div>
                    <div className="mt-3 grid gap-2 text-xs text-fog/70">
                      <div>{t('lightningOps.capacityLabel', { value: item.capacity_sat })}</div>
                      <div>{t('lightningOps.closedChannelSettledBalance', { value: formatSatsValue(item.settled_balance_sat) })}</div>
                      <div>{t('lightningOps.closedChannelTimelockedBalance', { value: formatSatsValue(item.time_locked_balance_sat) })}</div>
                      <div>{t('lightningOps.closedChannelCloseInitiator', { value: normalizeInitiatorLabel(item.close_initiator_label) })}</div>
                      <div>{t('lightningOps.closedChannelOpenInitiator', { value: normalizeInitiatorLabel(item.open_initiator_label) })}</div>
                    </div>
                    {item.channel_point && (
                      pointLink ? (
                        <a
                          className="mt-3 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                          href={pointLink}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {t('lightningOps.pointLabel', { point: item.channel_point })}
                        </a>
                      ) : (
                        <p className="mt-3 text-[11px] text-fog/50 break-all">{t('lightningOps.pointLabel', { point: item.channel_point })}</p>
                      )
                    )}
                    {item.closing_tx_hash && (
                      closingTxLink ? (
                        <a
                          className="mt-2 block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                          href={closingTxLink}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {t('lightningOps.closingTx', { txid: item.closing_tx_hash })}
                        </a>
                      ) : (
                        <p className="mt-2 text-[11px] text-fog/50 break-all">{t('lightningOps.closingTx', { txid: item.closing_tx_hash })}</p>
                      )
                    )}
                    {sweepTxs.length > 0 && (
                      <div className="mt-3 space-y-1">
                        <p className="text-[11px] uppercase tracking-wide text-fog/50">{t('lightningOps.closedChannelResolutions')}</p>
                        {sweepTxs.map((txid) => {
                          const txLink = mempoolTxLink(txid)
                          return txLink ? (
                            <a
                              key={txid}
                              className="block text-[11px] text-emerald-200 hover:text-emerald-100 break-all"
                              href={txLink}
                              target="_blank"
                              rel="noopener noreferrer"
                            >
                              {txid}
                            </a>
                          ) : (
                            <p key={txid} className="text-[11px] text-fog/50 break-all">{txid}</p>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>

            <div className="hidden max-h-[520px] overflow-x-auto overflow-y-auto pr-1 md:block">
              <table className="w-full min-w-[1080px] text-sm text-fog/75">
                <thead className="sticky top-0 bg-ink/95 backdrop-blur">
                  <tr className="text-left">
                    <th className="pb-3 pr-4">{t('lightningOps.closedChannelPeer')}</th>
                    <th className="pb-3 pr-4">{t('lightningOps.closedChannelClosedAt')}</th>
                    <th className="pb-3 pr-4">{t('lightningOps.closedChannelBalances')}</th>
                    <th className="pb-3 pr-4">{t('lightningOps.closedChannelType')}</th>
                    <th className="pb-3 pr-4">{t('lightningOps.closedChannelInitiators')}</th>
                    <th className="pb-3">{t('lightningOps.closedChannelLinks')}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredClosedChannels.map((item) => {
                    const peerLabel = item.peer_alias || item.remote_pubkey || t('lightningOps.unknownPeer')
                    const pointLink = mempoolLink(item.channel_point || '')
                    const closingTxLink = mempoolTxLink(item.closing_tx_hash)
                    const closedAtLabel = formatClosedAt(item.closed_at)
                    const sweepTxs = Array.isArray(item.resolutions)
                      ? item.resolutions.map((resolution) => String(resolution?.sweep_txid || '').trim()).filter(Boolean)
                      : []
                    return (
                      <tr key={`${item.chan_id}-${item.channel_point || item.closing_tx_hash || peerLabel}`} className="border-t border-white/5 align-top">
                        <td className="py-3 pr-4">
                          <div className="text-fog">{peerLabel}</div>
                          {peerProfileLinkGroup(item.remote_pubkey, 'mt-1 flex flex-wrap items-center gap-2')}
                          <div className="mt-1 text-xs text-fog/50">{item.remote_pubkey || t('common.na')}</div>
                          {item.channel_point && (
                            pointLink ? (
                              <a
                                className="mt-1 block text-xs text-emerald-200 hover:text-emerald-100 break-all"
                                href={pointLink}
                                target="_blank"
                                rel="noopener noreferrer"
                              >
                                {item.channel_point}
                              </a>
                            ) : (
                              <div className="mt-1 text-xs text-fog/50 break-all">{item.channel_point}</div>
                            )
                          )}
                        </td>
                        <td className="py-3 pr-4 text-xs">
                          <div>{closedAtLabel || formatCloseHeight(item.close_height)}</div>
                          {closedAtLabel && <div className="mt-1 text-fog/50">{formatCloseHeight(item.close_height)}</div>}
                          <div className="mt-1 text-fog/50">chan #{Number(item.chan_id || 0).toLocaleString()}</div>
                          <div className="mt-1 text-fog/50">{t('lightningOps.capacityLabel', { value: item.capacity_sat })}</div>
                        </td>
                        <td className="py-3 pr-4 text-xs">
                          <div>{t('lightningOps.closedChannelSettledBalance', { value: formatSatsValue(item.settled_balance_sat) })}</div>
                          <div className="mt-1">{t('lightningOps.closedChannelTimelockedBalance', { value: formatSatsValue(item.time_locked_balance_sat) })}</div>
                        </td>
                        <td className="py-3 pr-4">
                          <span className={`inline-flex rounded-full px-2 py-1 text-[11px] ${closedChannelBadgeClass(item)}`}>
                            {normalizeClosedChannelType(item.close_type_label)}
                          </span>
                        </td>
                        <td className="py-3 pr-4 text-xs">
                          <div>{t('lightningOps.closedChannelCloseInitiator', { value: normalizeInitiatorLabel(item.close_initiator_label) })}</div>
                          <div className="mt-1 text-fog/55">{t('lightningOps.closedChannelOpenInitiator', { value: normalizeInitiatorLabel(item.open_initiator_label) })}</div>
                        </td>
                        <td className="py-3 text-xs">
                          {item.closing_tx_hash && (
                            closingTxLink ? (
                              <a
                                className="block text-emerald-200 hover:text-emerald-100 break-all"
                                href={closingTxLink}
                                target="_blank"
                                rel="noopener noreferrer"
                              >
                                {item.closing_tx_hash}
                              </a>
                            ) : (
                              <div className="break-all text-fog/50">{item.closing_tx_hash}</div>
                            )
                          )}
                          {sweepTxs.length > 0 && (
                            <div className="mt-2 space-y-1">
                              <div className="text-fog/50">{t('lightningOps.closedChannelResolutions')}</div>
                              {sweepTxs.map((txid) => {
                                const txLink = mempoolTxLink(txid)
                                return txLink ? (
                                  <a
                                    key={txid}
                                    className="block text-emerald-200 hover:text-emerald-100 break-all"
                                    href={txLink}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                  >
                                    {txid}
                                  </a>
                                ) : (
                                  <div key={txid} className="break-all text-fog/50">{txid}</div>
                                )
                              })}
                            </div>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </>
        ) : (
          <p className="text-sm text-fog/60">
            {closedChannels.length ? t('lightningOps.closedChannelsNoMatch') : t('lightningOps.closedChannelsEmpty')}
          </p>
        )}
          </>
        )}
      </div>

      {scbRecoveryAvailable && (
        <div className="section-card order-5 space-y-4 border border-ember/20">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h3 className="text-lg font-semibold">{t('lightningOps.scbRecoveryTitle')}</h3>
              <p className="text-sm text-fog/60">{t('lightningOps.scbRecoverySubtitle')}</p>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="rounded-full px-3 py-1 text-xs bg-ember/20 text-ember">{t('lightningOps.scbRecoveryWarningTag')}</span>
              <button
                type="button"
                className="btn-secondary text-xs px-3 py-2"
                onClick={() => setScbRecoveryOpen((open) => !open)}
                aria-expanded={scbRecoveryOpen}
              >
                {scbRecoveryOpen ? t('common.hide') : t('common.open')}
              </button>
            </div>
          </div>

          {scbRecoveryOpen && (
            <>
          <p className="text-xs text-ember">{t('lightningOps.scbRecoveryWarningBody')}</p>

          <div className="flex flex-wrap items-center gap-3">
            <label className="btn-secondary text-xs px-3 py-2 cursor-pointer">
              {t('lightningOps.scbRecoverySelectFile')}
              <input
                className="hidden"
                type="file"
                accept=".scb,.backup,.bin,text/plain,application/octet-stream"
                onChange={handleScbFileSelected}
              />
            </label>
            {scbRestoreFileName && <span className="text-xs text-fog/60">{scbRestoreFileName}</span>}
          </div>

          <label className="text-sm text-fog/70">
            {t('lightningOps.scbRecoveryBackupLabel')}
            <textarea
              className="input-field mt-2 h-28 font-mono text-xs"
              value={scbRestoreData}
              onChange={(e) => {
                setScbRestoreData(e.target.value)
                if (scbRestoreResult !== null) {
                  setScbRestoreResult(null)
                }
              }}
              placeholder={t('lightningOps.scbRecoveryBackupPlaceholder')}
              spellCheck={false}
            />
          </label>

          <label className="flex items-center gap-2 text-sm text-fog/70">
            <input
              type="checkbox"
              checked={scbRestoreConfirm}
              onChange={(e) => setScbRestoreConfirm(e.target.checked)}
            />
            {t('lightningOps.scbRecoveryAcknowledge')}
          </label>

          <label className="text-sm text-fog/70">
            {t('lightningOps.scbRecoveryPhraseLabel')}
            <input
              className="input-field mt-2 font-mono text-xs"
              value={scbRestorePhrase}
              onChange={(e) => setScbRestorePhrase(e.target.value)}
              placeholder={SCB_RECOVERY_CONFIRM_PHRASE}
              spellCheck={false}
            />
          </label>

          <button
            className="btn-secondary text-xs px-3 py-2 border border-ember/40 text-ember disabled:opacity-60 disabled:cursor-not-allowed"
            type="button"
            onClick={handleScbRestore}
            disabled={!scbRestoreCanSubmit}
          >
            {scbRestoreBusy ? t('lightningOps.scbRecoveryRunning') : t('lightningOps.scbRecoveryRun')}
          </button>

          {scbRestoreStatus && <p className="text-sm text-brass">{scbRestoreStatus}</p>}
          {scbRestoreResult !== null && (
            <p className="text-xs text-fog/60">{t('lightningOps.scbRecoveryResult', { count: scbRestoreResult })}</p>
          )}
            </>
          )}
        </div>
      )}
      </div>
    </section>
  )
}
