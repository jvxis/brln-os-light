# AutoFee: idle-channel price discovery (0.5.28)

## Purpose

An Amboss/native-graph seed is a market reference, not proof that a channel can
sell liquidity at that price. Mature channels with usable local liquidity and
no recent outbound demand may explore below it. This is not a global fee cut,
an automatic channel-close policy, or a change to sovereign rebalance selection.

## Bounded reference-floor relaxation

The existing `relaxStaleNoFlowAdvisoryFloor` path now also recognizes `rescue`,
`rank`, and `revfloor` wrappers when their underlying reference is `seed`.
Rebalance-cost or current-outbound-rate provenance is not eligible for those
wrappers. Existing eligible seed and stale-outrate-memory sources remain.

If the seed/no-signal hold also pins the target to the current policy, the same
bounded experiment lowers the target, but only with Discovery enabled and a
seed/no-signal hold identified by the evaluator. An independent upward target
is not converted into a decrease.

Eligibility retains the existing maturity/bootstrap, effective-liquidity,
recent-rebalance and HTLC-pressure checks. The evaluator additionally checks
seven-day forward counts/amounts: zero-fee forwards are movement, not evidence
of no demand. No recent outbound reference, rebalance-history reference, recent
rebalance count, weak attempt signal, or recent cost may be present.

The existing step parameters are unchanged: normally at least 24 hours since
the last outgoing change, a 4% floor step bounded to 15–60 ppm; the existing
small-channel exception requires 96 hours and uses 2.5%, bounded to 15–20 ppm.
Configured minimum ppm, the raw target and subsequent safety gates can reduce
the actual step further. A just-changed or future-dated timestamp is not
mistaken for an unknown timestamp/channel age.

All downstream caps, reversals, cooldowns, economic interlock floors and fee
contracts remain in the normal decision path. Inbound-discount calculation and
Market Refill mode are not changed. Low-liquidity/new inbound bootstrap rules
remain protected. This relaxation does not promise any additional routing.

## Low-fee progress (0.5.29)

At low outgoing fees, the existing percentage down cap can permit less than
the absolute 5 ppm anti-noise minimum. For example, a 50 ppm fee can fall only
4 ppm under the normal 8% cap; rejecting every such move traps discovery at
50 ppm indefinitely. Waiting for more rounds does not resolve that conflict.

An eligible stale/no-flow experiment now accepts its already-capped small
decrease when the entire permitted down step is below the usual minimum.
Discovery must be enabled and the full reference-floor relaxation eligibility
must have passed in that same evaluation. This is not a global reduction of
the anti-noise threshold or permission to enlarge a decrease to 5 ppm.

- Normal and low-HTLC-sample percentage caps remain unchanged, including their
  existing minimum step of 1 ppm. Examples: 50 -> 46 and 10 -> 9 ppm.
- The existing 24-hour wait (96 hours for the small-channel exception),
  maturity, liquidity, no-flow and cost/HTLC checks still apply.
- Configured minimum fees, reversal confirmation, downstream cooldowns,
  economic interlock floors, settling and fee contracts remain authoritative.
- A successful fee change updates the existing state/timestamp; the exception
  cannot repeat at every scan and needs no new persistence or configuration.
- `stale-noflow-micro-step` identifies this anti-noise exception in decision
  telemetry. It does not by itself mean a policy was applied: a later guard
  can still veto or adjust the decision. Check the final result/apply status.

Ordinary tiny changes at higher fees, increases, disabled discovery and channels
that fail stale-floor eligibility retain their existing behavior. Inbound fees
and Sovereign rebalance selection are unchanged.

## Automatic idle refresh

Automatic refresh still requires seven days without outgoing, incoming or
rebalance movement, and only runs in Balanced mode when enabled.

- Seed or local-policy drift no longer bypasses the seven-day repeat interval.
- The normal evaluator keeps running during that interval; its reductions are
  not frozen by the refresh cooldown.
- Refresh does not replace a computed normal decrease with a hard-set reference,
  reverse downward intent upward, raise an idle channel with sufficient effective
  liquidity, or undercut an identified hard cost floor.
- An enforced economic intent that must raise the normal result remains able to
  bypass these discretionary refresh guards. A weaker floor is not permission
  to accelerate a decrease; shadow intents cannot bypass the interval.
- Explicit/manual refresh behavior is unchanged.

No database migration, API/configuration field, production parameter adjustment,
or change in default enablement is required.

## Validation and rollout

Regression coverage includes seed-wrapper provenance, bounded multi-round
descent below a fixed seed, repeat scans, zero-fee traffic, disabled discovery,
bootstrap/low-liquidity/cost/HTLC protections, economic-interlock precedence,
and changed-seed refresh intervals. Existing AutoFee golden scenarios remain
unchanged.

After an operator-approved release/deploy, inspect the new tags in the Results
history. Compare channel-level outgoing volume, revenue and available liquidity
over completed windows, allowing time after each price experiment. Overlapping
24-hour outcome windows are observations, not independent causal evidence that
a fee reduction caused traffic.
