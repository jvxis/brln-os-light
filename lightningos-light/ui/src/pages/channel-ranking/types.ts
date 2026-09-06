export type ChannelRankingReason = {
  code: string
}

export type ChannelRankingRecommendation = {
  code: string
  target_module?: string
}

export type ChannelRankingFlowCounterparty = {
  channel_point?: string
  channel_id: number
  peer_alias?: string
  forward_count_7d: number
  forward_amount_sat_7d: number
}

export type ChannelRankingItem = {
  channel_point: string
  channel_id: number
  peer_pubkey?: string
  peer_alias?: string
  active: boolean
  private: boolean
  capacity_sat: number
  local_balance_sat: number
  remote_balance_sat: number
  local_balance_pct: number
  remote_balance_pct: number
  inactive_duration_sec?: number
  pending_htlc_count?: number
  class_label?: string
  forward_in_count_7d: number
  forward_in_amount_sat_7d: number
  forward_out_count_7d: number
  forward_out_amount_sat_7d: number
  forward_fee_7d_sat: number
  forward_amt_7d_sat: number
  assisted_forward_fee_7d_sat: number
  assisted_forward_amt_7d_sat: number
  out_ppm_7d: number
  forward_fee_30d_sat: number
  forward_amt_30d_sat: number
  assisted_forward_fee_30d_sat: number
  assisted_forward_amt_30d_sat: number
  out_ppm_30d: number
  rebal_fee_7d_sat: number
  rebal_amt_7d_sat: number
  rebal_ppm_7d: number
  rebal_fee_30d_sat: number
  rebal_amt_30d_sat: number
  rebal_ppm_30d: number
  profit_fee_7d_sat: number
  profit_fee_30d_sat: number
  peer_stability_score_30d: number
  peer_sample_count_30d?: number
  htlc_failures_30d?: number
  htlc_policy_fails_30d?: number
  htlc_liquidity_fails_30d?: number
  htlc_forward_fails_30d?: number
  rebalance_dependence_score: number
  score: number
  score_7d: number
  score_30d: number
  trend_direction?: 'improving' | 'stable' | 'worsening'
  trend_delta?: number
  state: 'expand' | 'maintain' | 'monitor' | 'close'
  reasons?: ChannelRankingReason[]
  recommendations?: ChannelRankingRecommendation[]
  liquidity_state?: 'offer-ready' | 'low' | 'drained' | 'extreme-drained'
  liquidity_state_at?: string
  autofee_out_ratio_effective?: number
  automation_mode?: 'normal' | 'parked' | 'close_candidate'
  fixed_fee_ppm?: number
  review_at?: string
  automation_note?: string
  parked_at?: string
  computed_at?: string
}

export type ChannelRankingHistoryPoint = {
  computed_at: string
  score: number
  score_7d: number
  score_30d: number
  trend_direction?: 'improving' | 'stable' | 'worsening'
  trend_delta?: number
  state: 'expand' | 'maintain' | 'monitor' | 'close'
  profit_fee_7d_sat: number
  profit_fee_30d_sat: number
}

export type ChannelRankingPeerComparison = {
  channel_point: string
  channel_id: number
  peer_alias?: string
  score: number
  score_30d: number
  trend_direction?: 'improving' | 'stable' | 'worsening'
  trend_delta?: number
  state: 'expand' | 'maintain' | 'monitor' | 'close'
  capacity_sat: number
  profit_fee_7d_sat: number
  profit_fee_30d_sat: number
}

export type ChannelRankingFeedback = {
  direction?: 'improving' | 'stable' | 'worsening'
  score_delta?: number
  net_delta_sat?: number
  baseline_at?: string
  window_hours?: number
}

export type ChannelRankingDetailPayload = {
  item?: ChannelRankingItem
  history?: ChannelRankingHistoryPoint[]
  peer_channels?: ChannelRankingPeerComparison[]
  top_forward_in_sources?: ChannelRankingFlowCounterparty[]
  top_forward_out_sinks?: ChannelRankingFlowCounterparty[]
  feedback?: ChannelRankingFeedback
}

export type MagmaChannelCommitment = {
  order_id: string
  channel_point: string
  buyer_alias?: string
  buyer_pubkey?: string
  size_sat: number
  revenue_sat: number
  blocks_remaining?: number
  commitment_blocks: number
  magma_status: string
}

export type ChannelCapitalPlanAction = {
  code: string
  target_module?: string
}

export type ChannelCapitalPlanItem = {
  channel: ChannelRankingItem
  action: 'refill' | 'recycle_source' | 'reprice' | 'expand' | 'maintain' | 'observe' | 'rotate' | 'parked' | 'protected'
  priority: number
  eligible: boolean
  blockers?: string[]
  observation_days: number
  observation_required_days: number
  recoverable_local_sat: number
  primary_action?: ChannelCapitalPlanAction
  magma_commitment?: MagmaChannelCommitment
  automation_ready: boolean
  automation_blockers?: string[]
  target_outbound_pct?: number
  target_amount_sat?: number
  active_refill_intent?: boolean
}

export type ChannelCapitalPlanSummary = {
  total_channels: number
  action_counts: Record<string, number>
  productive_capital_sat: number
  protected_capital_sat: number
  parked_capital_sat: number
  recoverable_local_sat: number
}

export type ChannelCapitalPlanPayload = {
  available?: boolean
  last_sync_at?: string
  state_counts?: Record<string, number>
  magma_state_known?: boolean
  summary?: ChannelCapitalPlanSummary
  items?: ChannelCapitalPlanItem[]
}

export type ChannelRankingFormatters = {
  number: Intl.NumberFormat
  percent: Intl.NumberFormat
  dateTime: Intl.DateTimeFormat
  sats: (value?: number) => string
  pct: (value?: number) => string
  ratioPct: (value?: number) => string
  timestamp: (value?: string) => string
  duration: (value?: number) => string
  flow: (count?: number, amount?: number) => string
}
