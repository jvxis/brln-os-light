# Inbound discount: cost anchor and Insights R8 (0.5.57)

Investigation of 14 Nov 2026: a 10M seller refilled from 1% to 47% local in a
day with no rebalance spend, through the inbound fee discount AutoFee had set
on it. Other top sellers that the rebalance cannot reach had no discount. The
discount itself is AutoFee's balanced inbound rule (`inbound_passive_enabled`,
`inbound_discount_max_ratio`, `inbound_discount_reach_out_ratio`,
`inbound_discount_min_retained_spread`) and was working as designed where the
rule's inputs allowed it. Two gaps were found.

## 1. AutoFee: the inbound budget needs a real cost anchor

The balanced discount is budgeted as `applied_ppm − cost_anchor`, bounded by
the max ratio and the retained spread, and only when the 7-day margin
(`sale_ppm_7d − 1.1 × cost`) is at least 200 ppm. The outbound engine picks the
cost anchor from the channel's rebalance cost, then its 21-day cost, then its
remembered cost, then **the channel's own 7-day sale price** (`outrate`), then
the seed. That `outrate` fallback is fine for the floor but makes the inbound
margin negative by construction: a seller that was never refilled (no route at
any price) has no rebalance cost, so its cost became its own price and the
discount could never be budgeted. That is exactly the channel that most needs
to refill passively.

`autofeeInboundCostAnchor` now replaces an `outrate` / `outrate-mem` anchor,
for the inbound budget only, with the native seed (or the remembered seed),
then the node-wide rebalance cost. Real rebalance costs and the seed anchor are
unchanged; the outbound floor, target and tags are unchanged. The decision
carries `inbound-anchor-seed` / `inbound-anchor-rebal-global` when the swap
happened. Market-refill mode already anchors on the seed and is untouched.

## 2. Insights R8: "inbound discount for a seller that cannot be refilled"

Kind `desconto_entrada`. Fires for a top-selling **sink** at or under the
profile's reach-out ratio, with at least 5 sales in 7 days and no inbound
discount in place, **only when AutoFee will not set one**:

- `inbound_passive_enabled` is off → action `autofee_review`
  (`direction=inbound_passive`, `reason=inbound_passive_off`), or
- the channel's fee is pinned by hand (`fixed_fee_ppm`) → action `set_fee`
  (`direction=inbound_discount`, `reason=fixed_fee`).

With inbound passive on and AutoFee managing the channel the rule stays
silent: a missing discount is AutoFee's decision (see gap 1). Unknown AutoFee
config also stays silent. Evidence: local %, sale price, current fee, current
inbound fee and, when a rebalance cost or fee budget exists, the discount the
engine formula would budget (`inbound_discount_suggested_ppm`). Value per day is
the channel's 7-day revenue per day, the income a free refill keeps alive.
Invalidates when a discount is in place or the channel is back above the
reach-out level.

`RebalanceChannel` gains `inbound_fee_ppm` (from the local policy) so the rule
and the API can see the current discount.

## Validation

- `TestAutofeeInboundCostAnchorReplacesOutrateFallback`,
  `TestInsightInboundDiscountOnlySpeaksWhenAutofeeWillNot`, full
  `go test ./internal/server`, `tsc --noEmit`.
- After deploy on a node with inbound passive on: the never-refilled seller
  should receive an inbound discount on the next AutoFee run (tag
  `inbound-anchor-seed` in its line) and start receiving inbound forwards; R8
  should list nothing there. On a node with inbound passive off, R8 should list
  the drained sink sellers.
