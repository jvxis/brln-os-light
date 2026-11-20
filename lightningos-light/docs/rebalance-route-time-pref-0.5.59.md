# Rebalance route probe: time preference ladder and probe feedback (0.5.59)

## Symptom after 0.5.58

0.5.58 (`docs/rebalance-route-mc-fallback-0.5.58.md`) repeated the route
query without mission control when the weighted search answered "unable to
find a path". On a production node the no-path share of rebalance attempts
dropped from about 70% to 4% overnight, but the landed share did not move:
83% of attempts now ended in "probe returned no amount". The LND journal
showed about 2,400 `TemporaryChannelFailure` HTLCs in under ten hours, 92% of
them at intermediate hops, and eight third-party channels advertising 0–1 ppm
accounted for a third of them. The worst one was probed 277 times.

Two things were wrong:

1. A search with mission control off is fee-optimal and blind. On mainnet the
   cheapest route on paper runs through the 0–1 ppm channel every other node
   has already drained. Mission control knew those edges had just failed; the
   query ignored that knowledge.
2. `probeRoute` (the fake-invoice `SendToRoute` liquidity check) receives the
   index of the hop that refused the HTLC and dropped it. The job's
   ignored-edge list, which already exists and is fed by real payments and
   MPP shards, never learned from probes, so the next source of the same job
   asked the pathfinder for the same dry edge again, about nine times per job.

## What changed

### Query ladder keeps mission control on

`queryRoutesWithClient` (`internal/lndclient/rebalance.go`) no longer turns
mission control off. It climbs a ladder of LND's per-request `time_pref`:

| rung | `use_mission_control` | `time_pref` | risk term vs lnd.conf |
|------|-----------------------|-------------|-----------------------|
| 1    | true                  | 0           | × 1                   |
| 2    | true                  | −1          | × 0.053               |

`time_pref` is the knob LND added in lnd#6024 for exactly this trade-off; at
−1 the a-priori risk term (`routerrpc.attemptcost`, `attemptcostppm`,
`apriori.hopprob`) is divided by about 19 while mission control still drops
hops with a recent failure (probability zero) before any cost is computed,
and `routerrpc.minrtprob` still prunes improbable paths. The second rung is
therefore "the cheapest route mission control does not know to be dry". The
next rung is only tried when the previous one found nothing at all, never
once a route has been collected, and never for transport or context errors.

Measured on a production node with the manager's own fee caps (24 target ×
source pairs, 100k lot):

| mode                          | pairs with a route |
|-------------------------------|--------------------|
| mission control on, tp 0      | 4 / 24             |
| mission control on, tp −1     | 18 / 24            |
| mission control off (0.5.58)  | 21 / 24            |

The three extra pairs of the 0.5.58 mode were all routes mission control had
just seen fail (one of them the 1 ppm drained channel above).

When a route only came from rung 2 the client logs
`lndclient: route found at time_pref -1 after no path at lnd.conf weights`.

### Probe feeds the job's ignored-edge list

`probeRouteWithFailure` (`internal/server/rebalance_service.go`) wraps the
existing probe and, when the probe ends without a usable amount, hands the
probed route and the failing hop index to the same `noteRouteFailure` hook
the real `SendToRoute` path uses. All three probe call sites (attempt loop,
retry after a penultimate-hop failure, MPP shard) are wired. The job logs
`rebalance job=N: probe refused at hop H chan=C; edge ignored for this job`.

No new configuration, no new threshold: the ignored list keeps its existing
500-entry cap and job scope, and the time preference values are LND's own
range endpoints.

## Out of scope

- `time_pref` on the fast path (`SendPaymentMultiSource`, currently 0). It is
  the path that lands most lots today; change it only after measuring the
  ladder.
- Any change to `routerrpc.*` in the nodes' `lnd.conf`.

## Validation after deploy (24 h)

- Journal: count of `route found at time_pref -1` lines (how often lnd.conf
  weights alone would have said no-route) and of `probe refused at hop`
  lines.
- LND journal: `TemporaryChannelFailure` per hour, baseline about 250/h.
- `/api/rebalance/history`: attempt success share (baseline 0.6% on the
  larger node, 0 on the smaller one) and lots landed on the low-stock sellers
  that had zero landings in the 24 h before.
- Attempt and job durations unchanged (the second rung adds one QueryRoutes
  call, about 0.1 s).
