import React, { useState } from 'react'
import { createRoot } from 'react-dom/client'
import '../src/styles/main.css'
import '../src/i18n'
import ChannelRebalanceControls from '../src/components/rebalance/ChannelRebalanceControls'
import RebalanceEligibility from '../src/components/rebalance/RebalanceEligibility'
import ChannelRankingTable from '../src/pages/channel-ranking/ChannelRankingTable'
import ChannelCapitalPlanPanel from '../src/pages/channel-ranking/ChannelCapitalPlanPanel'
import type { RebalanceChannel, RebalanceConfig } from '../src/components/rebalance/types'
import type { ChannelCapitalPlanItem, ChannelRankingFormatters } from '../src/pages/channel-ranking/types'

const config = { auto_enabled: true, scheduler_mode: 'rules_auto', manual_restart_watch: true, manual_restart_ignore_economic_gates: false, rebalance_cost_floor_ppm: 150 } as RebalanceConfig
const initialChannel = { channel_point: 'fixture:0', peer_alias: 'Example channel', active: true, auto_enabled: true, manual_restart_enabled: false, excluded_as_source: true, eligible_as_manual_target: true, eligible_as_target: false, fee_budget_ppm: 100, rebalance_cost_7d_ppm: 250 } as RebalanceChannel
const item = {
  channel: { channel_point: 'fixture:0', peer_alias: 'Example channel', active: true, private: false, capacity_sat: 2000000, local_balance_pct: 15, score: 75, state: 'maintain', profit_fee_7d_sat: 100, profit_fee_30d_sat: 300, forward_in_count_7d: 5, forward_in_amount_sat_7d: 20000, forward_out_count_7d: 2, forward_out_amount_sat_7d: 10000 },
  action: 'refill', eligible: true, automation_ready: false, observation_days: 30, observation_required_days: 30
} as ChannelCapitalPlanItem
const number = new Intl.NumberFormat('en-US')
const format = { number, sats: (n = 0) => `${number.format(n)} sats`, pct: (n = 0) => `${n}%`, flow: (count = 0, amount = 0) => `${count} / ${amount} sats` } as ChannelRankingFormatters

function Fixture() {
  const [channel, setChannel] = useState(initialChannel)
  const rebalance = { config, channels: { [channel.channel_point]: channel } }
  return <main className="mx-auto max-w-6xl space-y-6 p-4 text-fog">
    <h1>Rebalance UI fixture</h1>
    <section data-testid="controls" className="max-w-md rounded-2xl border border-white/10 p-3">
      <ChannelRebalanceControls channel={channel} config={config} bypass={Boolean(channel.auto_bypass_cost_gate)}
        onAuto={(enabled) => setChannel({ ...channel, auto_enabled: enabled, manual_restart_enabled: enabled ? false : channel.manual_restart_enabled })}
        onRestart={(enabled) => setChannel({ ...channel, manual_restart_enabled: enabled, auto_enabled: enabled ? false : channel.auto_enabled })}
        onBypass={(enabled) => setChannel({ ...channel, auto_bypass_cost_gate: enabled })}
        onGuaranteed={(enabled) => setChannel({ ...channel, guaranteed_rebalance_enabled: enabled })}
        onExclude={(enabled) => setChannel({ ...channel, excluded_as_source: enabled })}
        onAutoTarget={() => {}} />
    </section>
    <section data-testid="unknown"><RebalanceEligibility /></section>
    <ChannelCapitalPlanPanel items={[item]} rebalance={rebalance} summary={{ total_channels: 1, action_counts: { refill: 1 }, productive_capital_sat: 2000000, parked_capital_sat: 0, protected_capital_sat: 0, recoverable_local_sat: 0 }} magmaStateKnown loading={false} format={format} onSelect={() => {}} />
    <ChannelRankingTable items={[item]} rebalance={rebalance} stateCounts={{ maintain: 1 }} selectedChannelPoint="" loading={false} format={format} onSelect={() => {}} />
  </main>
}

createRoot(document.getElementById('root')!).render(<Fixture />)
