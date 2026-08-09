export type Tone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

export type HealthIssue = {
  component?: string
  level?: string
  message?: string
}

export type HealthPayload = {
  status?: string
  issues?: HealthIssue[]
}

export type SystemCheckTone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

export type SystemCheckItem = {
  id: string
  label: string
  status: SystemCheckTone
  detail?: string
  value?: string | number | boolean
  diagnostic?: string
  log_source?: string
  log_tail?: string[]
}

export type SystemCheckGroup = {
  id: string
  label: string
  status: SystemCheckTone
  summary?: string
  items: SystemCheckItem[]
}

export type SystemCheckResponse = {
  status?: string
  checked_at?: string
  groups: SystemCheckGroup[]
}

export type SystemStats = {
  uptime_sec?: number
  cpu_load_1?: number
  cpu_percent?: number
  cpu_percent_now?: number
  cpu_percent_avg_30s?: number
  cpu_cores?: number
  ram_total_mb?: number
  ram_used_mb?: number
  temperature_c?: number
}

export type DiskPartition = {
  device: string
  mount?: string
  total_gb?: number
  used_gb?: number
  used_percent?: number
}

export type DiskSmart = {
  device: string
  type?: string
  power_on_hours?: number
  wear_percent_used?: number
  temperature_c?: number
  days_left_estimate?: number
  smart_status?: string
  total_gb?: number
  used_gb?: number
  used_percent?: number
  partitions?: DiskPartition[]
}

export type PostgresDatabase = {
  name?: string
  source?: string
  size_mb?: number
  connections?: number
  available?: boolean
}

export type PostgresStatus = {
  service_active?: boolean
  db_name?: string
  db_size_mb?: number
  connections?: number
  version?: string
  databases?: PostgresDatabase[]
}

export type LndStatus = {
  service_active?: boolean
  wallet_state?: string
  synced_to_chain?: boolean
  synced_to_graph?: boolean
  block_height?: number
  version?: string
  pubkey?: string
  uri?: string
  uris?: string[]
  info_known?: boolean
  info_stale?: boolean
  info_age_seconds?: number
  db_backend?: string
  channel_db_size_gb?: number
  graph_sync?: {
    progress_percent?: number
    known_channels?: number
    total_channels?: number
    remaining_channels?: number
    channels_per_hour?: number
    eta_seconds?: number
    approximate?: boolean
  }
  channels?: {
    active?: number
    inactive?: number
    pending?: number
  }
  peers?: {
    connected?: number
  }
  balances?: {
    onchain_sat?: number
    lightning_sat?: number
  }
}

export type LndPeer = {
  pub_key?: string
  alias?: string
  address?: string
  inbound?: boolean
  bytes_sent?: number
  bytes_recv?: number
  sat_sent?: number
  sat_recv?: number
  ping_time?: number
  sync_type?: string
  last_error?: string
  last_error_time?: number
}

export type LndChannel = {
  channel_point: string
  channel_id: number
  remote_pubkey?: string
  peer_alias?: string
  active?: boolean
  private?: boolean
  capacity_sat?: number
  local_balance_sat?: number
  remote_balance_sat?: number
  unsettled_balance_sat?: number
}

export type BitcoinCadenceBucket = {
  start_time?: number
  end_time?: number
  count: number
}

export type BitcoinStatus = {
  mode?: string
  rpchost?: string
  zmq_rawblock?: string
  zmq_rawtx?: string
  rpc_ok?: boolean
  rpc_stale?: boolean
  rpc_last_ok_age_seconds?: number
  zmq_rawblock_ok?: boolean
  zmq_rawtx_ok?: boolean
  version?: number
  subversion?: string
  chain?: string
  blocks?: number
  headers?: number
  verification_progress?: number
  initial_block_download?: boolean
  best_block_hash?: string
  installed?: boolean
  status?: string
  source?: string
  data_dir?: string
  connections?: number
  best_block_time?: number
  block_cadence_window_sec?: number
  block_cadence?: BitcoinCadenceBucket[]
  pruned?: boolean
  prune_height?: number
  prune_target_size?: number
  size_on_disk?: number
}

export type BIP110SourceStatus = {
  available: boolean
  source: string
  tip?: number
  sampled_tip?: number
  best_block_hash?: string
  chainwork?: string
  subversion?: string
  enforces_bip110?: boolean
  period_num?: number
  period_start?: number
  period_end?: number
  total_blocks?: number
  signaling_count?: number
  pct?: number
  synced?: boolean
  updated_at?: string
  error?: string
}

export type BIP110MonitorStatus = {
  informational_only: boolean
  risk_level: 'low' | 'normal' | 'watch' | 'elevated' | 'high' | 'unknown'
  phase: string
  checked_at: string
  signal_bit: number
  threshold_count: number
  threshold_pct: number
  mandatory_start_height: number
  lock_in_height: number
  activation_height: number
  forced_lock_in_height: number
  forced_activation_height: number
  blocks_to_mandatory: number
  internal: BIP110SourceStatus
  public: BIP110SourceStatus
  comparison: {
    comparable: boolean
    matches: boolean
    status: 'matched' | 'tip_mismatch' | 'signal_mismatch' | 'unavailable'
    same_period: boolean
    tip_delta?: number
    signaling_count_delta?: number
    pct_delta?: number
  }
}

export type BitcoinMarketPrice = {
  currency: string
  value?: number
  change_24h?: number
}

export type BitcoinMarketFees = {
  fastestFee?: number
  halfHourFee?: number
  hourFee?: number
  economyFee?: number
  minimumFee?: number
}

export type BitcoinMarketStatus = {
  prices?: BitcoinMarketPrice[]
  fees?: BitcoinMarketFees | null
  updated_at?: string
  partial?: boolean
  stale?: boolean
  price_error?: string
  fee_error?: string
}

export type ReportSeriesItem = {
  date: string
  forward_fee_revenue_sats: number
  rebalance_fee_cost_sats: number
  payment_fee_cost_sats?: number
  onchain_fee_cost_sats?: number
  offchain_fee_cost_sats?: number
  total_fee_cost_sats?: number
  total_fee_cost_with_onchain_sats?: number
  net_routing_profit_sats: number
  net_with_keysend_sats?: number
  keysend_received_sats?: number
  forward_count: number
  rebalance_count: number
  payment_count?: number
  rebalance_volume_sats?: number
  routed_volume_sats: number
  onchain_balance_sats?: number | null
  lightning_balance_sats?: number | null
  total_balance_sats?: number | null
  provenance_last_sync_at?: string
  provenance_last_sync_age_hours?: number
  provenance_health_alert?: boolean
  provenance_last_error?: string
}

export type ReportMetrics = {
  forward_fee_revenue_sats: number
  rebalance_fee_cost_sats: number
  payment_fee_cost_sats?: number
  onchain_fee_cost_sats?: number
  offchain_fee_cost_sats?: number
  total_fee_cost_sats?: number
  total_fee_cost_with_onchain_sats?: number
  net_routing_profit_sats: number
  net_with_keysend_sats?: number
  keysend_received_sats?: number
  forward_count: number
  rebalance_count: number
  payment_count?: number
  rebalance_volume_sats?: number
  routed_volume_sats: number
  onchain_balance_sats?: number | null
  lightning_balance_sats?: number | null
  total_balance_sats?: number | null
}

export type ReportRangeResponse = {
  range?: string
  timezone?: string
  series: ReportSeriesItem[]
}

export type SummaryResponse = {
  range?: string
  timezone?: string
  days?: number
  totals: ReportMetrics
  averages: ReportMetrics
  movement_target_sats?: number
  movement_pct?: number
}

export type LiveResponse = ReportMetrics & {
  start?: string
  end?: string
  timezone?: string
}

export type MovementLiveResponse = {
  date?: string
  start?: string
  end?: string
  timezone?: string
  outbound_target_sats?: number
  routed_volume_sats?: number
  movement_pct?: number
}

export type RebalanceOverview = {
  auto_enabled?: boolean
  last_scan_at?: string
  last_scan_status?: string
  last_manual_restart_at?: string
  last_manual_restart_queued?: number
  last_manual_restart_reasons?: Record<string, number>
  daily_budget_sat?: number
  daily_spent_sat?: number
  budget_unlimited?: boolean
  remaining_total_sat?: number
  remaining_for_auto_sat?: number
  live_cost_sat?: number
  effectiveness_7d?: number
  effectiveness_execution_7d?: number
  roi_7d?: number
  success_attempts_24h?: number
  success_amount_24h_sat?: number
  mpp_structural_abort_jobs_24h?: number
}

export type AutofeeStatus = {
  running?: boolean
  last_run_at?: string
  next_run_at?: string
  last_error?: string
}

export type AmbossHealthStatus = {
  enabled?: boolean
  status?: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_attempt_at?: string
  interval_sec?: number
  consecutive_failures?: number
}

export type ChanHealStatus = {
  enabled?: boolean
  status?: string
  last_ok_at?: string
  last_error?: string
  last_error_at?: string
  last_attempt_at?: string
  interval_sec?: number
  last_updated?: number
  last_reconnect_attempted?: number
  last_reconnected?: number
  last_reconnect_failed?: number
  last_reconnect_details?: ChanHealReconnectDetail[]
}

export type ChanHealReconnectDetail = {
  alias?: string
  pubkey?: string
  pubkey_short?: string
  channel_points?: string[]
  status?: string
  socket?: string
  sockets?: string[]
  socket_attempts?: ChanHealReconnectSocketAttempt[]
  error_summary?: string
  raw_error?: string
}

export type ChanHealReconnectSocketAttempt = {
  socket?: string
  network?: string
  status?: string
  error_summary?: string
  raw_error?: string
}

export type CloseRecoveryStatus = {
  available?: boolean
  active_count?: number
  action_required_count?: number
  waiting_close_count?: number
  htlc_blocked_count?: number
  node_retirement_count?: number
}

export type HtlcManagerStatus = {
  enabled?: boolean
  status?: string
  interval_minutes?: number
  interval_hours?: number
  last_attempt_at?: string
  last_ok_at?: string
  last_error?: string
  last_changed_count?: number
}

export type FailedPaymentsCleanerStatus = {
  enabled?: boolean
  status?: string
  interval_hours?: number
  last_attempt_at?: string
  last_ok_at?: string
  last_error?: string
  last_deleted_count?: number
}

export type TorPeerCheckerStatus = {
  enabled?: boolean
  status?: string
  interval_hours?: number
  last_attempt_at?: string
  last_ok_at?: string
  last_error?: string
  last_checked_count?: number
  last_switched_count?: number
}

export type NodeRetirementStatus = {
  available?: boolean
  active?: boolean
  active_session_id?: string
  active_state?: string
  error?: string
}

export type SuccessionConfig = {
  enabled?: boolean
  dry_run?: boolean
  destination_address?: string
  check_period_days?: number
  reminder_period_days?: number
  last_alive_at?: string
  next_check_at?: string
  deadline_at?: string
  status?: string
}

export type NotificationItem = {
  id: number
  occurred_at: string
  type: string
  action: string
  direction: string
  status: string
  amount_sat: number
  fee_sat: number
  fee_msat?: number
  peer_pubkey?: string
  peer_alias?: string
  channel_id?: number
  channel_point?: string
  channel_alias?: string
  txid?: string
  payment_hash?: string
  memo?: string
}
