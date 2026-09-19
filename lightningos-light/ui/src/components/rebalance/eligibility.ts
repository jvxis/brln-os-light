import type { RebalanceChannel, RebalanceConfig } from './types'

export type RebalanceEligibilityData = {
  channels: Record<string, RebalanceChannel>
  config: RebalanceConfig | null
}

export function rebalanceEligibility(channel: RebalanceChannel, config: RebalanceConfig) {
  const parked = channel.automation_mode === 'parked'
  const sovereign = config.scheduler_mode === 'sovereign_live' || config.scheduler_mode === 'sovereign_shadow'
  const conviction = !sovereign && config.auto_enabled && config.manual_restart_watch &&
    channel.manual_restart_enabled && config.manual_restart_ignore_economic_gates
  const automation = parked ? 'parked'
    : !config.auto_enabled ? 'globalOff'
      : config.scheduler_mode === 'sovereign_shadow' ? 'shadow'
        : channel.auto_enabled ? 'auto'
          : channel.manual_restart_enabled
            ? sovereign
              ? config.sovereign_candidate_scope === 'auto_and_manual_restart' ? 'restart' : 'scopeExcluded'
              : config.manual_restart_watch ? 'restart' : 'restartOff'
            : channel.guaranteed_rebalance_enabled ? 'guaranteed' : 'channelOff'
  // These two backend flags differ only by the automatic historical-cost
  // gate. Do not infer economic failure from general target ineligibility.
  const costBlocked = channel.eligible_as_manual_target && !channel.eligible_as_target &&
    !channel.auto_bypass_cost_gate && !conviction
  return { automation, parked, conviction, costBlocked, sourceExcluded: channel.excluded_as_source }
}
