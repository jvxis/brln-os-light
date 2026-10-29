import type { RebalanceChannel } from '../components/rebalance/types'

// Explains the server's decision; does not recalculate or bypass eligibility.
export function manualRebalanceReason(channel: RebalanceChannel, deadband: number): string {
  if (channel.eligible_as_manual_target) return ''
  if (!channel.active) return 'inactive'
  if (channel.automation_mode === 'parked') return 'parked'
  if (channel.target_outbound_pct - channel.local_pct <= deadband) return 'target'
  if (!channel.peer_policy_known) return 'policy'
  return 'budget'
}
