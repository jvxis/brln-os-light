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
  channels?: {
    active?: number
    inactive?: number
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
  daily_budget_sat?: number
  daily_spent_sat?: number
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
