# Rebalance Center Course

[Leia em Português](23_REBALANCE_CENTER_COURSE_PT_BR.md)

Rebalance Center exists to move local liquidity to channels where that liquidity is likely to be sold with an economic advantage. In practical terms, it selects channels with low outbound liquidity and an outbound fee higher than the peer fee, finds sources with excess local liquidity, tries routes, measures cost, learns good and bad pairs, and protects channels that have not yet paid back their previous rebalance.

## 1. Mental Model

A channel becomes a **target** when it needs to receive local liquidity. The basic calculation is:

```text
target_outbound_pct - local_pct = deficit
```

If the deficit exceeds `deadband_pct`, the channel is active, and `outgoing_fee_ppm > peer_fee_ppm`, it can be a manual target. To become an automatic target, it also needs to pass economic filters: cost gate, ROI, profit guardrail, cooldowns, and budget.

A channel becomes a **source** when it has local liquidity above `source_min_local_pct`, is not excluded as a source, and is not protected by payback. The system tries not to use a channel as a source while that channel is still in the red from a previous rebalance.

## 2. Operating Modes

**Manual Rebal In:** the operator chooses the channel and starts the job. It is used to test, fix a specific channel, or force a one-off action. It uses manual eligibility, which is more permissive than auto eligibility.

**Auto Rebalance:** the periodic scanner chooses candidates and enqueues jobs automatically. This is the most economic and conservative mode.

**Manual Restart Watch:** monitors channels marked per channel. When a manual auto-restart job fails or remains partial, it tries again after the interval/cooldown. It now respects `EligibleAsTarget`, so it only ignores the cost gate when the channel has **Bypass cost gate** enabled.

## 3. What To Check First

At the top of Rebalance Center, always check:

- **Live cost 24h:** how much the system spent.
- **Eligible sources / Targets:** if both are low, the system has little room to act.
- **Last scan status:** shows whether auto found candidates, enqueued jobs, or was blocked.
- **Last scan reasons:** the most important diagnostic field.
- **Effectiveness 7d / Attempts:** measures whether the system is trying well or wasting attempts.
- **ROI 7d / Payback progress:** indicates whether rebalances are paying for themselves.
- **Failures 30m:** shows whether there is a no-route/timeout storm.
- **MSPR 24h:** shows whether multi-source mode is helping or generating aborts.
- **Time-to-payback per channel:** shows whether rebalance cost comes back as revenue.

For an objective comparison, use `/api/rebalance/metrics/baseline?days=1` after 24 hours. For a reliable decision, use 7 days.

## 4. How To Read A Channel

In the channel row, read these fields in this order:

1. `local_pct / remote_pct`: if local is too low, the channel may need local inbound.
2. `outgoing ppm - peer ppm = spread`: positive spread is the economic base.
3. `effective spread`: spread multiplied by the econ ratio.
4. `target_outbound_pct`: how much local liquidity you want to keep.
5. `local liquidity to add`: required amount.
6. `payback`: whether previous rebalances are still unpaid.
7. `ROI est.` and `time-to-payback`: economic quality.
8. `Auto`, `Manual restart`, `Exclude source`, `Bypass cost gate`.

A good target channel usually has real demand, high outbound fee, low peer fee, positive spread, low local liquidity, revenue history, and reasonable payback.

## 5. Operating Parameters

Start conservatively:

- `auto_enabled`: only enable it after sources and targets have been reviewed.
- `scan_interval_sec`: default 900s. Use 900-1800s. Lower values tend to create noise.
- `max_concurrent`: default 2. Use 1 for diagnostics, 2 for normal operation, and more than 2 only if the node is large and stable.
- `daily_budget_pct`: default 25%. If the system spends too much, reduce it. If `budget_too_low` appears, increase it or reduce limits/amounts.
- `budget_mode`: prefer hybrid, because it mixes 7d average and 24h revenue.
- `budget_auto_only`: enabled means the budget limits auto, but does not pin manual restart.
- `manual reserve`: useful when auto consumes budget before channels marked for restart can use it.

## 6. Target Parameters

- `target_outbound_pct`: per channel. Do not set 50% everywhere by habit. Expensive channels with demand can sit at 30-50%; uncertain channels are usually better at 20-30%.
- `deadband_pct`: default 5. Raising it to 8-12 reduces small rebalances. Lowering it to 3-5 makes the system more active.
- `min_execute_sat`: default 10k. Raise it if there are too many small jobs with little impact. Lower it only if you want micro-corrections.
- `max_amount_sat`: 0 means unlimited. Use a limit on channels where you do not want overfunding.

## 7. Economic Parameters

- `econ_ratio`: default 0.6. Defines how much of the economic advantage you are willing to spend on fees. Raising it lets more routes pass and increases cost; lowering it makes selection stricter.
- `econ_ratio_override` per channel: use it for strategic channels without weakening the global setting.
- `bypass cost gate`: use only per channel. It skips the `effective spread > expected cost` filter, but ROI/profit still protect the job afterward.
- `roi_min`: default 1.1. If there are many `roi_guardrail` skips, the system is saying the target still does not pay enough.
- `rebalance_cost_floor_ppm`: default 250 ppm. This is the minimum expected cost when there is no history.
- `gain_model_version`: v1 uses historical revenue; v2 uses effective spread and velocity. Enable v2 when you want to prioritize real demand.
- `velocity_weight`: default 0.7. Higher prioritizes channels with drain rate; lower gives more weight back to fairness/age.

## 8. Execution Parameters

- `fee_limit_ppm`: fixed global override. Use carefully; normally prefer `econ_ratio`.
- `fee_ladder_steps`: default 1. More steps explore fees but increase attempts.
- `amount_probe_steps`: default 6. More steps refine amount but can increase runtime.
- `attempt_timeout_sec`: default 45.
- `rebalance_timeout_sec`: default 600.
- `fail_tolerance_ppm`: default 500. Helps tolerate small fee variations.
- `amount_probe_adaptive`: keep it enabled.

## 9. MSPR / Multi-Source

MSPR splits the rebalance into shards across multiple sources.

Current defaults:

- `mpp_enabled`: enabled.
- `mpp_auto_only`: enabled.
- `mpp_max_shards`: 6.
- `mpp_parallelism`: 3.
- `mpp_min_shard_sat`: follows the execution minimum when unset.
- `mpp_round_timeout_sec`: 35.

If there are many `mpp_structural_abort` events, reduce parallelism or shards. If partial success is frequent and collisions are low, MSPR is helping.

## 10. Payback And Source Protection

The system protects liquidity bought by rebalance until it pays for itself.

- `source_min_payback_progress`: default 0.95. Strong protection. It avoids using as source channels that have not recovered their cost yet.
- `payback mode`: releases when revenue pays cost.
- `time mode`: releases after `unlock_days`, default 7.
- `critical mode`: releases partially when sources are scarce.
- `critical_release_pct`: default 20%.

Do not disable payback globally just to create a source. That usually worsens PnL.

## 11. Mission Control

Use **Reset MC** when there are many no-route/route-dead failures in a short time. Do not use it for every isolated failure.

- `mc_half_life_sec`: 0 uses the LND default. During a bad-route storm, 600s can help.
- `mission_control_reinforce`: when enabled, reinforces successful routes in LND. This is advanced; enable it only if the node is already stable.

## 12. How To Diagnose Skip Reasons

- `target_not_eligible`: did not pass automatic target checks. Inspect spread, deadband, cost gate, and bypass.
- `roi_guardrail`: estimated ROI is below the minimum. Usually do not change it; wait for revenue or raise the channel fee.
- `profit_guardrail`: expected gain is lower than cost. Do not force it unless there is a clear strategy.
- `budget_too_low`: remaining budget does not cover estimated cost.
- `below_execute_min`: target amount is too small; usually healthy to ignore.
- `target_cooldown`: too many recent failures; wait or inspect pair stats.
- `channel_busy`: there is already a job on the channel.
- `autofee_settling_target`: AutoFee changed recently; score was temporarily reduced.

## 13. Node Runner Routine

Daily:

1. Check live cost, ROI, effectiveness, and failures 30m.
2. Open channels with low target and high spread.
3. Check whether skips are economic or operational.
4. Adjust at most one parameter group at a time.
5. Compare again after 24h.

Weekly:

1. Compare the 7d baseline.
2. Review channels with poor payback.
3. Review excluded sources.
4. Check pair drill-down for channels that always fail.
5. Promote or revert `gain_model_version=2` based on results.

## 14. Recommended Calibration Strategy

Start with:

- Auto enabled only on selected channels.
- `scan_interval_sec=900`.
- `max_concurrent=1` or `2`.
- `daily_budget_pct=10-25`.
- `deadband_pct=5-8`.
- `source_min_local_pct=30-40`.
- `roi_min=1.1`.
- `source_min_payback_progress=0.95`.
- MSPR auto-only enabled.

Then calibrate like this:

1. If there are no jobs: check `target_not_eligible`, `no_sources`, `budget_too_low`.
2. If there are jobs but little execution: check MC, pair stats, fee cap, and route failures.
3. If jobs execute but do not pay back: raise ROI minimum, reduce econ ratio, and review targets.
4. If jobs pay well but are slow: enable v2, adjust velocity weight, and review target pct.
5. If spending is too high: reduce budget, econ ratio, target pct, or max amount.

Rule of thumb: adjust **per-channel targets first**, then **sources**, then **economics**, then **execution**. Changing everything together makes it impossible to know what improved or worsened.
