# Inbound discount exit cap (0.5.57)

## What happened

A sink priced at ~3.500 ppm earned the balanced inbound discount (sink, drained,
selling well, margin over its cost anchor) and the budget formula let it reach
−1.869 ppm. LND charges a forward `max(0, outbound fee + inbound fee)`, so every
channel of the node priced below 1.869 ppm became a free exit for flow that
entered through that sink. In one day 8,6M sats came in through it and left,
at zero fee, through three top sellers priced at 1.300–1.700 ppm that the
autopilot refills with paid rebalances. The sellers had to be bought back
(one of them the very same day, at ~970 ppm), one lot bought the day before at
1.512 ppm left at zero, and the discounted sink filled to 99% with liquidity it
sells slowly. The day closed negative.

Nothing in the existing design looked at the exits. The budget
(`computeInboundDiscountBudget`) is bounded by the discounted channel's own fee,
cost anchor, max ratio and retained spread. When the channel leaves the
reach-out band the discount walks through two grace rounds and a 25% decay per
round, so a large discount outlives the need for several runs.

## What changes

### Exit cap

`inboundDiscountExitCap` (pure) and `inboundDiscountExitCapFor` (engine) bound
the discount by where the inbound flow of the channel actually went:

1. Exits = `chan_id_out` of forwards with `chan_id_in` = the channel, grouped
   with their inbound volume, on the engine's existing 7 and 21 day windows
   (`fetchInboundExitStats`, same `notifications` table as the forward stats).
2. Only **paid** exits bound: channels with paid rebalance cost in the same
   window (`rebalStats.ByChannel`). Sources and channels that refill by
   themselves never bound — draining them cheaply into a seller is the mix
   working.
3. Only **relevant** exits bound: share of the inbound volume ≥
   `InboundDiscountExitShareMin`, a profile field (conservative 5%, moderate
   10%, aggressive 15%) with a Fee Center override
   (`inbound_discount_exit_share_min_override`). An override of 100% switches
   the cap off; no extra switch was added.
4. Cap = the lowest advertised fee among those exits × (1 −
   `InboundDiscountMinRetainedSpreadFrac`), the same retained spread the
   budget already uses.
5. Both windows are evaluated and the lower cap wins. No inbound history means
   no cap: the discount behaves exactly as before.

The cap applies to a fresh discount (`computeBalancedInboundDiscount` result)
and to a discount kept during grace/decay. Tag `inbound-exit-cap`; the result
item carries `inbound_exit_cap_ppm` and `inbound_exit_cap_window`; the log
line shows `cap≤N(exit:7d|21d)`. The Fee Center preview
(`selectAutofeeRefreshInboundDiscount`) applies the same cap and marks the
source `…+exit-cap`.

With the 14/11 shape the cap is 1.327 × 0,88 ≈ 1.167 ppm: the flow into the
sink keeps coming, but the forward through the 1.327 ppm seller pays ~160 ppm
instead of nothing, and the sellers priced at 1.600–1.700 keep a margin too.

### Cut when the channel is at its target

`inboundDiscountFullCut`: when the channel's local ratio is at or above the
outbound target the operator set for it in the Rebalance Center
(`autofeeRebalanceRuntimeSnapshot.TargetOutboundPct`), a discount that is no
longer earned is dropped at once (tag `inbound-cut-full`) instead of walking
through grace and decay. A channel without a target keeps the existing
lifecycle. No threshold was invented: the operator's own target says when the
channel is full.

### Numbers

Every number comes from the profile (`InboundDiscountExitShareMin`,
`InboundDiscountMinRetainedSpreadFrac`), the node's own history (exits, paid
refills) and current policies (exit fees, rebalance target). The profile
values 5/10/15% are a first setting to be calibrated from outcomes after a
week on both production nodes.

## What does not change

Budget formula, max ratio, reach-out band, grace and decay lengths, the cost
anchor swap for never-refilled sellers (`autofeeInboundCostAnchor`), the
market-refill mode. Insights R8 still shows the uncapped budget: it has no
exit history in its input (follow-up if the number misleads).

## Validation

- `go test ./internal/server -run 'Inbound|Autofee|Insight'` green;
  `tsc --noEmit` clean.
- New tests: `TestInboundDiscountExitCapBoundsByCheapestRelevantPaidExit`,
  `TestInboundDiscountExitCapIgnoresSourcesAndTinyExits`,
  `TestInboundDiscountExitCapProfilesAndOff`,
  `TestInboundDiscountExitCapForPicksLowerWindow`,
  `TestAdvanceBalancedInboundDiscountLifecycleCapAndFullCut`,
  `TestInboundDiscountFullCutUsesTheChannelTarget`,
  `TestSelectAutofeeRefreshInboundDiscountHonoursExitCap`.
- Post-deploy: a discounted sink whose inbound flow leaves through paid
  sellers shows `inbound-exit-cap` and `cap≤N(exit:…)` in its line, with the
  discount at or below the cheapest relevant seller's fee minus the spread;
  forwards with zero total fee out of paid sellers stop appearing in
  `/api/lnops/forwards`; a sink at or above its rebalance target loses the
  discount in one round (`inbound-cut-full`).
