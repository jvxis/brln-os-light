export type RebalanceConfig = {
  auto_enabled: boolean
  scan_interval_sec: number
  deadband_pct: number
  source_min_local_pct: number
  econ_ratio: number
  econ_ratio_max_ppm: number
  fee_limit_ppm: number
  lost_profit: boolean
  fail_tolerance_ppm: number
  roi_min: number
  daily_budget_pct: number
  budget_mode: string
  budget_unlimited: boolean
  budget_auto_only: boolean
  manual_reserve_enabled: boolean
  manual_reserve_mode: string
  manual_reserve_value: number
  max_concurrent: number
  min_amount_sat: number
  max_amount_sat: number
  min_split_enabled: boolean
  min_probe_sat: number
  min_execute_sat: number
  mpp_enabled: boolean
  mpp_max_shards: number
  mpp_parallelism: number
  mpp_min_shard_sat: number
  mpp_round_timeout_sec: number
  mpp_auto_only: boolean
  fee_ladder_steps: number
  amount_probe_steps: number
  amount_probe_adaptive: boolean
  attempt_timeout_sec: number
  rebalance_timeout_sec: number
  manual_restart_watch: boolean
  mc_half_life_sec: number
  payback_mode_flags: number
  unlock_days: number
  critical_release_pct: number
  critical_min_sources: number
  critical_min_available_sats: number
  critical_cycles: number
  rebalance_cost_floor_ppm: number
  source_min_payback_progress: number
  mission_control_reinforce: boolean
  gain_model_version: number
  velocity_weight: number
  autofee_settling_window_sec: number
  autofee_settling_multiplier: number
}

export type RebalanceOverview = {
  auto_enabled: boolean
  last_scan_at?: string
  last_scan_status?: string
  last_scan_detail?: string
  last_scan_candidates?: number
  last_scan_remaining_budget_sat?: number
  last_scan_reasons?: Record<string, number>
  last_scan_top_score_sat?: number
  last_scan_profit_skipped?: number
  last_scan_queued?: number
  last_scan_skipped?: RebalanceScanSkip[]
  last_manual_restart_at?: string
  last_manual_restart_queued?: number
  last_manual_restart_reasons?: Record<string, number>
  last_mc_reset_at?: string
  last_mc_reset_reason?: string
  mc_reset_count?: number
  mc_reset_cooldown_sec?: number
  mc_reset_cooldown_remaining_sec?: number
  eligible_sources?: number
  targets_needing?: number
  daily_budget_sat: number
  daily_budget_base_sat?: number
  daily_budget_short_term_sat?: number
  daily_spent_sat: number
  daily_spent_auto_sat: number
  daily_spent_manual_sat: number
  remaining_total_sat?: number
  remaining_for_auto_sat?: number
  budget_unlimited?: boolean
  budget_auto_only?: boolean
  manual_reserve_enabled?: boolean
  manual_reserve_mode?: string
  manual_reserve_value?: number
  manual_reserve_sat?: number
  manual_reserve_remaining_sat?: number
  live_cost_sat: number
  effectiveness_7d: number
  effectiveness_execution_7d?: number
  jobs_without_attempt_7d?: number
  jobs_without_attempt_rate_7d?: number
  roi_7d: number
  attempts_24h?: number
  failed_attempts_24h?: number
  attempt_success_rate_24h?: number
  attempts_per_success_attempt_24h?: number
  success_sats_per_attempt_24h?: number
  success_attempts_24h?: number
  success_amount_24h_sat?: number
  success_avg_amount_24h_sat?: number
  success_below_min_attempts_24h?: number
  success_below_min_amount_24h_sat?: number
  success_below_min_rate_24h?: number
  payback_revenue_sat: number
  payback_revenue_rebalanced_sat: number
  payback_cost_sat: number
  payback_progress: number
  payback_progress_rebalanced: number
  mpp_shadow_jobs_24h?: number
  mpp_shadow_plan_ready_24h?: number
  mpp_shadow_planned_sat_24h?: number
  mpp_shadow_actual_sent_sat_24h?: number
  mpp_shadow_in_progress_jobs_24h?: number
  mpp_shadow_success_jobs_24h?: number
  mpp_shadow_failed_jobs_24h?: number
  mpp_shadow_partial_jobs_24h?: number
  mpp_shadow_floor_blocked_sources_24h?: number
  mpp_shadow_avg_planned_shards_24h?: number
  mpp_shadow_avg_actual_attempts_24h?: number
  mpp_structural_abort_jobs_24h?: number
  top_failure_reasons_30m?: RebalanceReasonStat[]
  route_dead_targets_30m?: RebalanceTargetStat[]
}

export type RebalanceReasonStat = {
  reason: string
  count: number
}

export type RebalanceTargetStat = {
  channel_id: number
  peer_alias?: string
  failed_sources: number
  failure_attempts: number
  last_failure_at?: string
  reason?: string
}

export type RebalanceScanSkip = {
  channel_id: number
  channel_point: string
  peer_alias: string
  target_outbound_pct: number
  target_amount_sat: number
  expected_gain_sat: number
  estimated_cost_sat: number
  expected_roi: number
  expected_roi_valid: boolean
  reason: string
}

export type RebalancePairStat = {
  source_channel_id: number
  source_channel_point?: string
  source_peer_alias?: string
  target_channel_id: number
  target_channel_point?: string
  target_peer_alias?: string
  last_success_at?: string
  last_fail_at?: string
  last_fail_reason?: string
  fail_count: number
  permanent_fail_score: number
  success_amount_sat: number
  success_fee_ppm: number
  last_success_route_hops?: string[]
}

export type RebalanceChannel = {
  channel_id: number
  channel_point: string
  peer_alias: string
  remote_pubkey: string
  active: boolean
  private: boolean
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
  local_pct: number
  remote_pct: number
  outgoing_fee_ppm: number
  outgoing_base_msat: number
  peer_fee_rate_ppm: number
  peer_base_msat: number
  spread_ppm: number
  target_outbound_pct: number
  target_amount_sat: number
  auto_enabled: boolean
  manual_restart_enabled: boolean
  use_default_econ_ratio: boolean
  econ_ratio_override?: number
  auto_bypass_cost_gate?: boolean
  eligible_as_target: boolean
  eligible_as_manual_target: boolean
  eligible_as_source: boolean
  protected_liquidity_sat: number
  payback_progress: number
  time_to_payback_hours?: number
  time_to_payback_valid?: boolean
  max_source_sat: number
  revenue_7d_sat: number
  drain_rate_sat_per_hour?: number
  rebalance_cost_7d_sat: number
  rebalance_cost_7d_ppm: number
  rebalance_amount_7d_sat: number
  roi_estimate: number
  roi_estimate_valid?: boolean
  excluded_as_source: boolean
}

export type RebalanceJob = {
  id: number
  created_at: string
  completed_at?: string
  source: string
  status: string
  reason?: string
  target_channel_id: number
  target_channel_point: string
  target_peer_alias?: string
  target_outbound_pct: number
  target_amount_sat: number
}

export type RebalanceAttempt = {
  id: number
  job_id: number
  attempt_index: number
  source_channel_id: number
  source_peer_alias?: string
  amount_sat: number
  fee_limit_ppm: number
  fee_paid_sat: number
  status: string
  payment_hash?: string
  fail_reason?: string
  started_at?: string
  finished_at?: string
}
