# Product Backlog

## Status

This file tracks open product and Autofee proposals that are not implemented yet.

Implemented items should not remain here.

At the time of writing:

- `Channel Ranking` is already implemented

## Priorities

Current backlog priority order:

1. `Autofee` dynamic liquidity state
2. channel `parking mode`
3. ranking-driven per-channel automation policy

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

## Non-Goals For Now

Do not add all of this to the UI at once.

Prefer:

- backend logic first
- API visibility
- result validation
- then selective UI exposure

## Notes

This file is intended to remain the single backlog document for these pending proposals.
