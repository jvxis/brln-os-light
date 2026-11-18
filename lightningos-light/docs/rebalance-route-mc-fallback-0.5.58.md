# Rebalance route probe: fallback without mission control (0.5.58)

## Symptom

For several days nearly every rebalance attempt on two production nodes
failed at the route probe with `unable to find a path to destination`, for
every source channel and for amounts from the full lot down to the execution
minimum. The only successful jobs were the ones that went through the
delegated fast path (`SendPaymentV2`). Graph sync, peer policies on the
target edges and LND itself were all healthy.

## Cause

`lndclient.queryRoutes` always asked `QueryRoutes` with
`UseMissionControl: true`. LND then minimises *fee + risk*, where the risk
term is `routerrpc.attemptcost` plus `routerrpc.attemptcostppm` scaled by
how unlikely the route is (unknown hops count with
`routerrpc.apriori.hopprob`). When the cheapest *weighted* route exceeds the
fee limit, LND answers "unable to find a path" and does not look for a
cheaper, less probable one.

On a node whose `lnd.conf` carries a heavy risk term (the production nodes
had `attemptcost`/`attemptcostppm` 10 to 20 times the repository template)
and whose mission control is refreshed by hundreds of attempts a day, every
fee-capped probe turned into a false no-route. Reproduced over LND's REST
`QueryRoutes` with the manager's exact fee limits: with mission control, no
route for four of six targets from any source and 2–3× the fee on the other
two; without it, a route well inside the limit for almost all of them.

The same weighting explains why the few lots that did land were expensive:
only the "probable" routes passed the filter.

## Change

`queryRoutesWithClient` (split out of `queryRoutes` so it can be tested with
a fake client) keeps the weighted search as the first query. When that
search returns nothing at all — the pathfinder's own no-path error or an
empty answer, before any route or sibling candidate has been collected — the
same request is repeated once with `UseMissionControl: false`.

- The fee-optimal route is only a candidate. `SendToRoute` is the real test
  and its outcome still feeds LND's mission control and the service's own
  pair statistics and per-job ignored edges.
- No retry once a route has been collected, and none for transport,
  deadline or argument errors.
- The fallback sits inside the single query, so the amount ladder in
  `rebalance_route_amount.go` only halves the lot when even the fee-optimal
  search finds nothing.
- No new configuration or threshold.

## Node-side follow-up (operator decision)

The repository template (`templates/lnd.conf`) ships
`routerrpc.minrtprob=0.001`, `attemptcost=10`, `attemptcostppm=100`. Nodes
installed before the template settled, or edited by hand, may carry much
heavier values; aligning them (requires an LND restart) makes mission
control an adviser instead of a veto. The Rebalance Center's mission
control reset clears recorded failures but not the a-priori penalty of
unknown hops, so on its own it gives only temporary relief.

## Validation

- `go test ./internal/lndclient/ -run QueryRoutes` — fallback happens on
  no-path and on an empty answer, not after a collected route, not on
  transport errors.
- Post-deploy: share of attempts failing with `unable to find a path` and
  the job success rate in `/api/rebalance/history`; both nodes had been at
  ≥95 % attempt failure for several days before the change.
