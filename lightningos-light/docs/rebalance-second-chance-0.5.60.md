# Rebalance: measured sellers and a second chance for unmeasured targets (0.5.60)

## The problem

The autopilot drops a target before ranking when its estimated gain is below
its estimated cost (`roi_guardrail`, `profit_guardrail`). Lowering `roi_min`
does not help: the same comparison is repeated later as
`expected_profit_below_min`, whose minimum cannot go below zero, and the
exploration slot only skips that check when the estimate is not negative.

Two kinds of target fail that comparison by construction, on every scan:

1. **A measured seller.** The channel was refilled recently, so its cost is its
   own realized cost, which already includes the peer's last hop. The gain cuts
   the peer's fee again. A channel bought below its sale price scores under 1.
2. **A target with no cost of its own.** Its cost is assumed at the node
   reference or at the whole fee budget (`econ_ratio` of the outgoing fee),
   while the gain is at most the cold-start share of the spread. Whenever the
   peer charges more than a small part of our fee, the estimate is negative.
   The only way to replace the assumption with a measurement is to buy once,
   which the guardrail forbids. The cost-gate bypass does not cover these
   guardrails, and the exploration draw happens after them.

`peer_fee_as_cost_enabled` (0.5.51) removes the double count for every channel
at once. In production that also let channels with no sale through on the
cold-start prior, at the full lot size, and most of that stock did not sell.
This change keeps the two cases apart instead of treating them alike.

## What changes

### Measured seller: the peer's hop is counted once

A target is a measured seller when, for the lot being evaluated:

- its own realized rebalance cost in the window covers the lot
  (`hasReliableSovereignCostHistory`), and
- the volume it sold in the window covers the lot too
  (`gainV3ObservedVolumeSat`).

For such a target the gain and the cost floor are estimated as with
`peer_fee_as_cost_enabled`: the peer's fee leaves the gain and becomes a floor
on the cost. No prior is involved, both numbers are the channel's own. A
channel that did not sell, or whose cost was measured on less than the lot,
keeps the previous estimate.

A seller whose stock already covers its demand is still held: its gain is
limited by what it sold, not by the fee.

### Unmeasured target: one small lot on the exploration slot

A target that fails the entry guardrails stays in the candidate list, marked
`SecondChanceOnly`, when exploration is on, the channel has a spread to earn
and its cost is an assumption (no cost history of its own covering the lot).

In the scan loop such a target:

- is held at `roi_guardrail`, as before, unless it draws the exploration slot;
- when it draws the slot, buys **one lot at the operator's start amount**
  (`min_amount_sat`, never below the execution floor), the same size the
  exploration probe uses after an unsold batch;
- may carry a negative estimate through `expected_profit_below_min`, because
  the estimate is the thing being measured;
- is still bought under the usual fee cap, so the price paid stays below the
  outgoing fee.

Everything else stays in force: the exploration share per cycle, exploration
burnout, the wait after an unsold paid batch, the stock gate, cooldowns and
the budget. A target whose own measured cost says it loses gets no second
chance. AutoTarget does not see held targets as round candidates.

The decision carries `second_chance: true` when the lot is queued this way.

## What does not change

- No new parameter, no new threshold, no migration.
- With exploration off (`sovereign_exploration_slot_pct = 0`) no target gets a
  second chance.
- `peer_fee_as_cost_enabled` keeps its meaning; with it on, nothing here adds
  to it.
- Gain models before v3, manual jobs and the guaranteed slot are untouched.

## Limits

- A second-chance lot is a purchase that may not sell. The loss per lot is
  bounded by the start amount and the fee cap, and the unsold-batch wait keeps
  a channel from getting another one while the first has not sold.
- One small lot does not make the channel "measured" for a full lot: it earns
  further small lots only as each one sells.
- The measured-seller rule looks at the same window as the gain model; a
  seller whose last refill left the window is evaluated as before.
