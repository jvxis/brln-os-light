# Rebalance probe: amounts below a hop's min_htlc (0.5.59)

## Symptom

On a production node one source channel failed every attempt of every job
with `no matching outgoing channel available for node index 1`, one attempt
per job, for days. The channel was active, its policy enabled, no pending
HTLCs, 36 ppm outbound, well stocked. The LND journal showed about 130 of
those `BuildRoute` errors per day, some at `node index 2`, i.e. coming from
other sources too.

## Cause

The fake-invoice liquidity probe (`probeRouteRecursive` in
`internal/server/rebalance_service.go`) starts at `min_probe_sat` (5,000 by
default) and, at every step, rebuilds the candidate route for the new amount
with LND's `BuildRoute`. `BuildRoute` refuses an amount that is outside the
policy of a hop (below its `min_htlc`, or above every parallel channel's
`max_htlc`) and answers `no matching outgoing channel available for node
index N`.

That error left the probe as the attempt's failure, before any HTLC was
sent. The service classifies it as a structural pair failure with the longest
base cooldown (45 min, doubling), so a source whose downstream channels
advertise `min_htlc` above the probe start never got its liquidity tested at
all.

Reproduced over REST on the affected node: `QueryRoutes` through the source
returned a route normally; `BuildRoute` with the same hops failed at 5,000,
5,500, 5,900 and 6,500 sats and succeeded at 10,000 and 100,000 sats.

## Change

When the rebuild fails with that message (`isRouteAmountOutOfRange`), the
probe no longer gives up:

- if it has already proven an amount, it returns that amount;
- otherwise it moves the probe up towards the attempt amount, exactly as the
  existing `FEE_INSUFFICIENT` branch does, and keeps going.

Any other `BuildRoute` error (transport, context) still surfaces as before.
No new configuration: `min_probe_sat` keeps its meaning, the probe simply
does not treat a policy refusal as a liquidity verdict.

## Validation after deploy

- `/api/rebalance/history`: attempts whose `fail_reason` contains
  `no matching outgoing channel` should drop to near zero (baseline about 50
  per day on the larger node, all from one source).
- LND journal: `[/routerrpc.Router/BuildRoute]: no matching outgoing channel`
  per day (baseline about 130).
- The affected source should appear in successful attempts or fail with a
  real probe verdict (`probe returned no amount`).
