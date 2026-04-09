# Product Backlog

## Status

This file tracks open product and Autofee proposals that are not implemented yet.

Implemented items should not remain here.

At the time of writing:

- `Channel Ranking` is already implemented

## Priorities

Current backlog priority order:

1. `Autofee` signal hierarchy and stability redesign
2. `Autofee` dynamic liquidity state
3. channel `parking mode`
4. ranking-driven per-channel automation policy
5. rebalance budget redesign with manual reserve

## 1. Autofee Signal Hierarchy And Stability Redesign

### Goal

Make `Autofee` more efficient, less upward-biased, and significantly more stable by:

- using the full signal set already available in the product
- distinguishing channel role from liquidity urgency
- distinguishing real rebalance execution from theoretical rebalance need
- protecting assisted-revenue channels from unnecessary fee increases
- reducing fee churn to avoid unnecessary graph updates

The intended outcome is:

- fewer cases where fees keep drifting upward without strong evidence
- better handling of `sink` channels with weak economics
- better preservation of low-fee channels that are useful for assisted routing
- lower risk of frequent fee changes that could harm graph reputation

### Problem

Today the system already sees a rich set of signals:

- `out_ppm_7d`
- `rebal_ppm_7d`
- `out_ppm_30d`
- `rebal_ppm_30d`
- `forward_in/out` counts and volumes
- assisted revenue
- `Channel Ranking` state, score, trend, profitability
- rebalance attempts and outcomes
- budget exhaustion and ROI guardrails
- HTLC policy and liquidity failures
- Amboss/native seeds
- node and channel liquidity normalization via `outnorm`

But the runtime hierarchy is still too permissive toward upward protection, especially for:

- mature empty `sink` channels with poor economics
- channels with little or no real recent rebalance execution
- channels that are valuable mainly for assisted revenue at very low outbound fees
- channels where seed or stale protection remains stronger than local evidence

This creates the current operator perception:

- Autofee often feels like it "always wants to go up"
- channels can stay far above `out_ppm` or `rebal_ppm`
- some useful low-fee channels need to be removed from Autofee manually

### Proposed Direction

Redesign the decision flow into four explicit layers:

1. `anchor`
   - derive the economic reference price
2. `channel role`
   - define what the channel is good for
3. `execution reality`
   - distinguish channels that can actually be rebalanced from those only theoretically needing rebalance
4. `stability guards`
   - suppress unnecessary fee churn

The key design change is:

- local economics and channel role should decide first
- scarcity and seed should only amplify later

### Stage 1. Assisted Routing Role

#### Goal

Prevent useful low-outbound channels from being pushed upward just because they are not marked as `super-source`.

#### New Runtime States

Add internal role tags:

- `assist-channel`
- `assist-preserve`

#### Candidate Criteria

Initial candidate conditions should combine:

- meaningful `forward_in_count_7d` and/or `forward_in_amount_7d`
- meaningful `assisted_forward_fee_7d_sat`
- low or modest `forward_out`
- low or moderate `rebalance_dependence_score`
- `profit_fee_7d_sat` not strongly negative

Optional profile-sensitive thresholds can be applied later, but first iteration should use conservative hard thresholds.

#### Behavior

For `assist-channel`:

- outbound fee should not rise easily
- `surge`, `sink-floor`, and `peg` should be weakened
- cooldown for upward changes should be stricter
- hold current fee unless there is a hard upward signal

#### Hard Upward Signals Allowed

Only permit easier increases when at least one is true:

- `htlc-liquidity-hot`
- real `rebal-recent`
- strong `surge-confirmed`
- strong and recent outbound growth beyond assisted role

#### Expected Gains

- better preservation of channels intentionally kept at low or zero outbound
- less need to remove assisted channels from Autofee manually
- better support for routing strategies based on inbound attractiveness

### Stage 2. Ranking-Aware Gates

#### Goal

Use `Channel Ranking` as a primary decision gate, not only as a late correction.

#### Proposed Policy Mapping

- `expand`
  - upward freedom can remain relatively normal
- `maintain`
  - keep target close to anchor
- `monitor`
  - upward moves require stronger evidence
- `close`
  - default behavior becomes decompression or conservative hold, not protection

#### Additional Gates

Reduce or disable upward pressure when:

- `state = close`
- `state = monitor` and `trend = worsening`
- `profit_fee_7d_sat <= 0`
- `profit_fee_30d_sat <= 0`
- `score` and `score_30d` are weak

Allow stronger upward freedom only if a hard signal is present.

#### Expected Gains

- fewer channels defended by Autofee when ranking already says they are weak
- cleaner alignment between `Channel Ranking` and `Autofee`
- less manual conflict between modules

### Stage 3. Rebalance Reality Gates

#### Goal

Stop defending high fees for channels that are not actually receiving viable rebalance execution.

#### New Runtime Signals

Add explicit runtime tags and gates such as:

- `rebal-budget-exhausted`
- `rebal-roi-blocked`
- `rebal-no-attempt-recent`
- `rebal-disabled-channel`

These should be derived from:

- rebalance overview budget state
- skipped candidates by reason
- recent per-channel job and attempt history
- channel-level `auto_enabled`
- channel-level `manual_restart_enabled`

#### Behavior

For mature empty `sink` channels with local history and no real execution:

- do not raise fees just because they remain empty
- reduce force of `rebal-sink`, `peg`, `surge`, and `no-down-neg-margin`
- anchor downward toward a safe band around:
  - `max(out_ppm_7d, rebal_ref_soft)`

This stage should extend the existing `empty-sink-*` logic instead of replacing it.

#### Expected Gains

- better decisions when rebalance is blocked by budget or ROI
- less divergence between fee policy and actual capital deployment
- fewer channels stuck high simply because they look scarce on paper

### Stage 4. Stability And Anti-Churn Guards

#### Goal

Reduce unnecessary fee churn and graph-level noise.

#### Proposed Guards

Add runtime controls such as:

- larger deadband before publishing a change
- minimum ppm delta required to publish
- per-channel maximum changes per 24h
- maximum consecutive upward changes
- strict upward cooldown by default
- only explicit hard signals may bypass upward cooldown

#### Explicit Rule

Simple forward activity should not bypass `cooldown_up`.

Forwards should be treated as:

- evidence that the current fee is acceptable
- not as automatic license to raise again

#### Expected Gains

- fewer gossip updates
- more stable channel pricing
- reduced chance of fee thrash harming graph reputation

### Economic Anchor Model

The new hierarchy should treat the economic anchor as:

- `max(out_ppm_7d, rebal_ref)`

Where:

- `rebal_ref` comes from true recent rebalance cost when available
- `30d` references are used only for channels protected by `slow-cycle-30d`
- Amboss/native seed remains fallback, not dominant anchor, when mature local evidence exists

### Suggested Runtime Order

Refactor the runtime so `evaluateChannel` behaves conceptually like this:

1. compute local market anchor
2. derive channel role
3. derive ranking-aware policy
4. derive rebalance execution policy
5. apply upward/downward pressure only after the above
6. apply anti-churn stability guards
7. compute inbound discount from the final outbound result

### Suggested Technical Shape

The implementation should introduce or refactor toward helpers such as:

- `deriveChannelRole(...)`
- `deriveAssistChannel(...)`
- `deriveRankingPolicy(...)`
- `deriveRebalanceExecutionPolicy(...)`
- `deriveEconomicAnchor(...)`
- `applyAutofeeStabilityGuards(...)`

Relevant existing files:

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/autofee_service_test.go`
- `lightningos-light/internal/server/channel_ranking_service.go`
- `lightningos-light/internal/server/rebalance_service.go`
- `lightningos-light/internal/server/rebalance_handlers.go`

### Observability

Add explicit runtime tags so behavior remains auditable in Results and Telegram.

New tags should include at least:

- `assist-channel`
- `assist-preserve`
- `rank-expand`
- `rank-maintain`
- `rank-monitor`
- `rank-close`
- `rebal-budget-exhausted`
- `rebal-roi-blocked`
- `rebal-no-attempt-recent`
- `rebal-disabled-channel`
- `stability-hold`
- `stability-delta-min`
- `stability-max-changes`

### Testing Plan

Add tests for at least:

1. assisted-revenue channel with low outbound fee does not rise without hard signal
2. `close` channel with weak economics cannot receive normal upward `surge`
3. mature empty `sink` with no attempts and blocked rebalance is anchored down
4. `expand` channel with true liquidity pressure can still rise
5. seed cannot dominate mature local signals
6. upward cooldown is strict unless explicit bypass signal exists
7. anti-churn guard suppresses small and repeated changes

### Rollout Plan

Implement in this order:

1. `assist-channel`
2. ranking-aware gates
3. rebalance reality gates
4. anti-churn guards

This order is important because:

- assisted routing protection fixes a real operator pain point immediately
- ranking-aware gates improve decision quality with data already available
- rebalance-reality gates reduce false scarcity
- anti-churn should come last, after the main decision hierarchy is improved

### Non-Goals For First Iteration

Do not attempt all of the following in the same rollout:

- complete UI rework of Autofee controls
- fully automatic channel closure
- replacing `market_refill`
- removing all existing protection logic at once

The first goal is not a full rewrite. It is a hierarchy correction.

### Execution Breakdown

The implementation should be split into small, reviewable increments.

#### Milestone A. Assisted Channel Detection

##### Scope

Add explicit runtime classification for channels that are economically useful mainly because of assisted routing.

##### Backend Tasks

1. Add helper:
   - `deriveAssistChannel(...)`
2. Use these signals in the helper:
   - `forward_in_count_7d`
   - `forward_in_amount_sat_7d`
   - `forward_out_count_7d`
   - `forward_out_amount_sat_7d`
   - `assisted_forward_fee_7d_sat`
   - `rebalance_dependence_score`
   - `profit_fee_7d_sat`
3. Add runtime tags:
   - `assist-channel`
   - `assist-preserve`
4. Reduce upward pressure for these channels in `balanced` mode unless hard signals exist.

##### Likely Files

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/autofee_service_test.go`

##### Acceptance Criteria

- low-outbound assisted channels no longer rise by default
- channels with weak assisted contribution are unaffected
- results output clearly shows the new tags

#### Milestone B. Ranking-Aware Gates

##### Scope

Make `Channel Ranking` state and trend explicitly influence upward and downward permissions.

##### Backend Tasks

1. Add helper:
   - `deriveRankingPolicy(...)`
2. Use:
   - `state`
   - `score`
   - `score_30d`
   - `trend_direction`
   - `profit_fee_7d_sat`
   - `profit_fee_30d_sat`
3. Add runtime tags:
   - `rank-expand`
   - `rank-maintain`
   - `rank-monitor`
   - `rank-close`
4. Block or weaken `surge` and protection logic for:
   - `close`
   - `monitor + worsening`
   - negative `7d` and `30d`

##### Likely Files

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/autofee_service_test.go`

##### Acceptance Criteria

- `close` channels do not receive normal upward freedom
- `expand` channels still behave close to current baseline
- ranking policy is visible in tags and tests

#### Milestone C. Rebalance Reality Gates

##### Scope

Differentiate channels that need rebalance in theory from channels that are actually getting viable rebalance execution.

##### Backend Tasks

1. Add helper:
   - `deriveRebalanceExecutionPolicy(...)`
2. Feed it with:
   - recent `rebal-recent`
   - recent `rebal-attempt`
   - channel `auto_enabled`
   - channel `manual_restart_enabled`
   - rebalance budget status
   - skipped reasons such as `roi_guardrail` and `budget_too_low`
3. Add tags:
   - `rebal-budget-exhausted`
   - `rebal-roi-blocked`
   - `rebal-no-attempt-recent`
   - `rebal-disabled-channel`
4. Extend the existing `empty-sink-*` family so mature empty sinks can converge downward when rebalance is not happening in reality.

##### Likely Files

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/rebalance_service.go`
- `lightningos-light/internal/server/rebalance_handlers.go`
- `lightningos-light/internal/server/autofee_service_test.go`

##### Acceptance Criteria

- empty sinks with no real execution no longer stay artificially high
- channels still receiving real rebalance pressure preserve current behavior
- budget exhaustion becomes visible in Autofee results

#### Milestone D. Stability And Anti-Churn

##### Scope

Reduce unnecessary fee updates and protect graph stability.

##### Backend Tasks

1. Add helper:
   - `applyAutofeeStabilityGuards(...)`
2. Add controls for:
   - minimum ppm delta to publish
   - per-channel max changes per 24h
   - max consecutive upward changes
   - strict upward cooldown
3. Keep bypass narrow:
   - `htlc-liquidity-hot`
   - real `rebal-recent`
4. Add tags:
   - `stability-hold`
   - `stability-delta-min`
   - `stability-max-changes`

##### Likely Files

- `lightningos-light/internal/server/autofee_service.go`
- `lightningos-light/internal/server/autofee_service_test.go`

##### Acceptance Criteria

- forward activity alone no longer causes repeated fee increases
- small oscillations do not generate fee changes
- change frequency per channel is measurably reduced

### Suggested Delivery Order

Use this delivery order, with tests after every milestone:

1. Milestone A
2. Milestone B
3. Milestone C
4. Milestone D

This keeps early changes targeted and lowers regression risk.

### Suggested Review Gates

After each milestone, validate on a test or production-like node using:

- latest Autofee rounds
- ranking snapshots
- rebalance overview
- live reports

Look specifically for:

- fewer unjustified upward moves
- fewer channels far above `out_ppm` and `rebal_ppm`
- preservation of strong assisted-routing channels
- lower fee churn over repeated rounds

### Rollout Safety Notes

Each milestone should be:

- behind conservative gating in code
- covered by dedicated tests
- observable through explicit tags in Results and Telegram

Do not combine multiple milestone behaviors into a single opaque patch.

## 1. Autofee Dynamic Liquidity State

### Goal

Improve Autofee decisions by separating:

- channel behavior role: `sink`, `router`, `source`
- liquidity urgency state: `offer-ready`, `low`, `drained`, `extreme-drained`

The goal is to make Autofee react more coherently to:

- current node local liquidity
- node size
- selected Autofee profile
- per-channel local liquidity ratio

### Problem

Today Autofee mixes two different ideas:

- historical channel behavior
- current liquidity urgency

Current logic already uses:

- node-level `liquidity_class`
- channel-level `out_ratio`
- `sink/router/source`
- `low-out`
- `extreme-drain`

But these concerns are not cleanly separated.

This can cause:

- protection becoming too early for some channels
- weak distinction between low liquidity and true drain
- less coherent behavior when the node still has acceptable local liquidity

### Current Baseline

Relevant current behavior:

- node `liquidity_class`
  - `drained` if `local_ratio < 25%`
  - `balanced` if `25% <= local_ratio <= 75%`
  - `full` if `local_ratio > 75%`
- channel `sink`
  - requires `out_ratio < 15%` plus behavior bias
- `low-out`
  - effective threshold currently near `9.5%` for the observed node/profile
- `extreme-drain`
  - active in `moderate` around `4%`
- `extreme-drain-turbo`
  - active around `1%`

### Proposed Direction

Keep:

- `class_label = sink/router/source`

Add:

- `liquidity_state = offer-ready/low/drained/extreme-drained`

Example combinations:

- `sink + offer-ready`
- `sink + drained`
- `router + low`

### Suggested Profile Bases

Example starting point:

- `conservative`
  - `offer_ready_min = 22%`
  - `low_out_max = 10%`
  - `drained_out_max = 6%`
  - `extreme_drained_out_max = 1.5%`
- `moderate`
  - `offer_ready_min = 20%`
  - `low_out_max = 9%`
  - `drained_out_max = 5%`
  - `extreme_drained_out_max = 1%`
- `aggressive`
  - `offer_ready_min = 18%`
  - `low_out_max = 8%`
  - `drained_out_max = 4%`
  - `extreme_drained_out_max = 1%`

These values are backlog proposals, not approved defaults.

### Dynamic Adjustments

Thresholds should also depend on:

- node size: `small`, `medium`, `large`, `xl`
- node `local_ratio`

Example rule:

- if node `local_ratio < 15%`
  - protect earlier
- if node `15% to 25%`
  - mildly protect earlier
- if node `25% to 40%`
  - neutral
- if node `> 40%`
  - allow more aggressive liquidity offering

### Expected Gains

Expected benefits:

- better decisions for channels in the `5% to 20%` range
- clearer distinction between normal low liquidity and real emergency drain
- more coherent offering behavior when node local liquidity is still healthy

Expected limits:

- does not solve `peg`, `floor-lock`, `stepcap`, or dead peers by itself
- should be treated as a quality-of-decision improvement, not a complete reset

### Suggested Implementation Steps

1. Add effective liquidity threshold computation helper.
2. Add `liquidity_state` to Autofee runtime and result payloads.
3. Keep `class_label` unchanged.
4. Route `low-out` and `extreme-drain` logic through the new thresholds.
5. Expose effective thresholds in the calibration section of results/API.
6. Add tests by profile, node size, and node liquidity ratio.

## 2. Channel Parking Mode

### Goal

Create a controlled path for channels that are:

- stagnant for a long time
- poor in net profitability
- highly dependent on rebalances
- poor candidates for normal Autofee optimization

Instead of immediately closing them, allow a temporary parked state.

### Proposed Behavior

When a channel is parked:

- use fixed fee per channel
- remove channel from Autofee
- remove channel from rebalance
- disable inbound discount automation for that channel
- keep review metadata and review date

### Why

This avoids wasting Autofee cycles on channels that:

- stay `same-ppm`
- do not react to normal fee changes
- absorb operator time without clear return

It also creates a safer middle state before cooperative close.

### Expected Gains

- cleaner Autofee signal on active channels
- less noise from dead peers
- better operator workflow before closing channels

### Suggested Implementation Steps

1. Add channel automation mode:
   - `normal`
   - `parked`
   - `close_candidate`
2. Store fixed-fee override per parked channel.
3. Exclude parked channels from Autofee and rebalance.
4. Show parked state in UI and ranking.
5. Add review date and operator note fields.

## 3. Ranking-Driven Per-Channel Automation Policy

### Goal

Use `Channel Ranking` output to drive actionable automation hints or semi-automatic policies.

This should not blindly execute closures, but it should help move from analysis to action.

### Proposed Direction

Use ranking states to suggest or trigger:

- `maintain`
  - stay in normal Autofee and rebalance
- `monitor`
  - keep normal automation, but highlight weak economics
- `close`
  - suggest `parking mode` or manual review
- `expand`
  - preserve or boost active strategy

### First Safe Scope

Initial scope should be recommendation-oriented:

- suggest parking candidates
- suggest fixed fee ranges
- suggest rebalance exclusion
- suggest close review

Avoid fully automatic closure in first iteration.

### Expected Gains

- faster operator decisions
- stronger connection between ranking and action
- less manual cross-checking between ranking and Autofee behavior

### Suggested Implementation Steps

1. Add recommendation layer on top of ranking state.
2. Surface recommended action in UI.
3. Add optional one-click actions:
   - park
   - remove from rebalance
   - remove from Autofee
4. Keep irreversible actions manual.

## 5. Rebalance Budget Redesign With Manual Reserve

### Goal

Replace the current automatic rebalance budget model, which is based only on a percentage of the last 24 hours of routing revenue, with a model that:

- controls total daily rebalance spend at node level
- explicitly accounts for both `auto` and `manual` rebalance cost
- optionally protects a separate reserve for manual restart operations
- becomes more stable and less pro-cyclical than a pure `24h revenue pct` rule

### Problem

Today the automatic scanner computes:

- `daily_budget_sat = forward_revenue_24h * daily_budget_pct`

and the scan gate is effectively checked against:

- `remaining = budget - spent_auto`

This creates three issues:

- total node spend can drift higher than intended when manual restarts consume a lot of fee budget
- a strong or weak single day can distort the next day too much
- the operator cannot explicitly protect room for manual interventions

### Proposed Model

The budget should become a total daily node budget:

- `daily_budget_total_sat`

The runtime should also support an optional manual reserve:

- `manual_reserve_enabled`
- `manual_reserve_mode`
  - `fixed_sat`
  - `pct`
- `manual_reserve_value`

The automatic scanner should then consume:

- `remaining_for_auto = daily_budget_total_sat - spent_total - manual_reserve_remaining`

Where:

- `spent_total = spent_auto + spent_manual`
- `manual_reserve_remaining` is the still-protected portion of the reserve
- if manual reserve is disabled, this value is `0`

### Budget Formula

The current `24h` formula should not remain the only anchor.

The recommended budget source should be hybrid:

- `avg_revenue_7d` as the main anchor
- `revenue_24h` as a short-term adjustment

Suggested first formula:

- `base_budget_sat = avg_revenue_7d * daily_budget_pct`
- `short_term_budget_sat = revenue_24h * daily_budget_pct`
- `daily_budget_total_sat = round(0.70 * base_budget_sat + 0.30 * short_term_budget_sat)`

Optional later additions:

- absolute minimum floor
- absolute maximum cap
- small carry-over from prior day

### Behavior Rules

#### Automatic Scan

The automatic scan should:

- use `spent_total`, not only `spent_auto`
- respect the protected manual reserve
- stop queueing jobs when only the reserved manual portion remains

#### Manual Restart

Manual restart should:

- still consume total daily spend
- be allowed to use the reserved portion
- optionally warn the user when the action would exceed the total daily budget

First iteration should prefer:

- warnings and visibility
- not hard-blocking manual actions unless explicitly configured later

### Data Model Changes

#### Backend Config

Extend `RebalanceConfig` with fields such as:

- `budget_mode`
  - `revenue_24h_pct`
  - `hybrid_revenue`
- `manual_reserve_enabled`
- `manual_reserve_mode`
- `manual_reserve_value`
- optional:
  - `daily_budget_min_sat`
  - `daily_budget_max_sat`

#### Budget Tracking

The existing `rebalance_budget_daily` table already tracks:

- `budget_sat`
- `spent_auto_sat`
- `spent_manual_sat`
- `spent_sat`

This should be extended conceptually with computed outputs in API/overview:

- `budget_total_sat`
- `manual_reserved_sat`
- `manual_reserved_remaining_sat`
- `remaining_for_auto_sat`
- `remaining_total_sat`

First iteration may keep storage unchanged and compute these values at runtime.

### Backend Scope

Files likely involved:

- `lightningos-light/internal/server/rebalance_service.go`
- `lightningos-light/internal/server/rebalance_handlers.go`

Implementation tasks:

1. Add new config fields and schema migration.
2. Refactor budget calculation away from pure `24h` revenue.
3. Make `auto` scan consume against total spend.
4. Add manual reserve computation.
5. Surface reserve and remaining budget clearly in `RebalanceOverview`.
6. Ensure manual job creation records spend consistently in the same total budget model.

### UI Scope

Primary screen:

- `Rebalance Center`

Files likely involved:

- `lightningos-light/ui/src/pages/RebalanceCenter.tsx`
- `lightningos-light/ui/src/i18n/pt-BR.json`
- `lightningos-light/ui/src/i18n/en.json`

#### Config UI Changes

Add settings for:

- budget mode
- daily budget percent
- manual reserve enabled
- manual reserve type
- manual reserve value
- optional min/max total budget

These should live close to the existing rebalance automation and budget settings.

#### Overview UI Changes

Expose clearly:

- total daily budget
- spent auto
- spent manual
- total spent
- reserved for manual
- remaining for auto
- remaining total

The overview should make it visually obvious when:

- `auto` is paused because only manual reserve is left
- manual actions are already consuming most of the day budget

#### Manual Restart UX

When the user triggers manual restart:

- show current total budget state
- show whether reserve is being consumed
- show a warning if the run would push total spend above budget

First iteration should prefer:

- clear warning text
- not complex modal logic

### Acceptance Criteria

The implementation is successful when:

1. automatic rebalance no longer ignores large manual spend
2. the operator can reserve daily budget for manual restart
3. budget behavior is less volatile than pure `24h revenue pct`
4. the overview explains budget state clearly without needing logs
5. existing manual flows continue working without hidden hard blocks

### Rollout Plan

1. backend config and hybrid total-budget calculation
2. update scanner to consume against total spend and reserve
3. API/overview exposure
4. Rebalance Center settings and overview UI
5. manual restart warning UX
6. observe behavior for several days before adding any hard manual block

## Non-Goals For Now

Do not add all of this to the UI at once.

Prefer:

- backend logic first
- API visibility
- result validation
- then selective UI exposure

## Notes

This file is intended to remain the single backlog document for these pending proposals.
