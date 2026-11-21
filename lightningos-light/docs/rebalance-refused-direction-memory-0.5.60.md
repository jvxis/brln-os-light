# Rebalance: refused directions are remembered across jobs (0.5.60)

## What was happening

Since 0.5.59 a rebalance job stops asking the pathfinder for a third-party
channel direction once a probe is refused there at the minimum probe amount.
That list belonged to the job and died with it.

Every source gets one attempt per job. A job therefore explores one layer of
routes, records one refusal per source, and ends. The next job for the same
target, a couple of hours later, started from the same cheapest routes and was
refused at the same channels again. On a production node most of the
directions refused in a job had already been refused in the previous job for
that target, and some were refused in every job of the day.

LND's mission control does record those probe failures. It does not keep the
direction out for long: the fee-first rung of the route query only needs a
route above the minimum route probability, and the penalty decays with the
mission control half-life, so the direction is eligible again within minutes.

The existing pair-failure cache does not cover this either. It remembers a
source→target pair, not the third-party channel that refused.

## What changed

The probe's verdict now lives in the rebalance service, per channel direction,
for the whole node. A dry third-party direction is dry for every target.

- **What is remembered.** Only the probe's liquidity verdict: a temporary
  channel failure at the minimum probe amount, on a third-party hop. This
  node's own source and target channels are never remembered; they stay with
  the pair and target cooldowns.
- **For how long.** No new constant. A first refusal is held for the half-life
  this node's LND applies to a routing failure (read from LND; the rebalance
  mission control setting is the fallback). When the direction is retested
  after that and refuses again, it is held for as long as it has already been
  seen dry. Retests of a persistently dry direction thin out on their own.
- **When the history ends.** When a probe gets through the direction, when a
  payment lands over a route that uses it (fast path, cached route), or when
  it has gone unrefused for the horizon the pair statistics already use to age
  a permanent failure.
- **How a job uses it.** A job starts with the directions still remembered,
  most recent first, using at most half of its ignore list so the job keeps
  room for what it learns itself.

Process memory only. No table, no migration, no setting. A manager restart
clears it.

The delegated fast path is unchanged: it hands pathfinding to LND and takes no
ignore list.

## What to look for in the journal

```
rebalance job=N: starts with K directions refused in earlier jobs
rebalance job=N: probe refused by node at position P on outgoing chan=C; direction ignored for this job and remembered for the next ones
```

A refusal on this node's own source or target channel keeps the 0.5.59
wording (`direction ignored for this job`).

## What to measure after deploying

- Probe refusals per job should fall, and the refusals that remain should be
  on directions not seen in the previous job for that target.
- Watch the share of attempts ending in `unable to find a path`. With more
  directions ignored the search can run out earlier, and an exhausted search
  is recorded as a structural pair failure, which feeds the pair and target
  cooldowns.
- Lots landed per day on targets that were getting none is the real test.

## Limits

- A direction that refills while it is held is not retried until the hold
  ends. The job still routes around it, within the same fee limit.
- The memory is not shared with the fast path and does not survive a restart.
