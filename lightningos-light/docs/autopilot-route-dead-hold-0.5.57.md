# Autopilot — route-dead hold, discovery backoff visibility, chan-heal backoff (0.5.57)

Three small changes from the daily audits of 11–14 Nov 2026. None adds a config
field.

## AutoTarget: an earned UP is held while the target cannot be refilled

`decideAutoTargetAdjustment` already skipped UP for a "dead route", but only
when the historical success rate was exactly zero. Targets with ~1% success
over tens of thousands of attempts, or sitting in the autopilot's structural
cooldown (every source failed on recent jobs), still had their target raised on
sell-through alone. The higher target only inflated the deficit that feeds the
score and the size of the first lot once a route reappeared.

`autoTargetRouteDead` now also returns true when:

- the target is in `target_structural_cooldown` at scan time
  (`shouldSkipSovereignTargetStructuralCooldown`, same predicate the autopilot
  uses), or
- `attempts ≥ 20` and `success_rate < 2%`.

The small-sample rule (`≥ 8` attempts, zero success) is unchanged, and so is
DOWN: a dead route never blocks `fill_and_hold`. When the channel would have
earned an UP (`sells_above_node` / `drain_first`) but the route is dead, the
decision is `noop` with reason `route_dead_hold`. It is written to the
AutoTarget history once per evaluation interval per channel (applied = false)
so the audit shows why the target stopped rising; a hold without an earned UP
stays unpersisted as before. `trigger_signals` gains `in_structural_cooldown`.

## Discovery ladder: backoff is counted in the scan

The 0.5.55 no-route backoff only stored `discovery_backoff_until` on the
in-memory decision, so a scan where the ladder was paused looked identical to
one where no ladder existed. The scan now counts `discovery_backoff` in
`skip_reasons` whenever the ladder skipped a target because of the backoff. The
Rebalance Center labels it in EN and pt-BR.

## chan-heal: per-peer reconnect backoff

The healer retried every inactive, disconnected peer on every tick (5 min, up
to 5 peers per tick). Two peers that were never reachable took 47 attempts a
day each. After `chanHealReconnectBackoffAfter` (6) consecutive failures the
peer waits one tick interval, doubling per further failure, capped at
`chanHealReconnectBackoffMax` (6 h). A successful reconnect, or the peer
showing up as connected, clears the state. The state lives in the healer
process only; a manager restart starts the count again, which is intended.

## Validation

- `go test ./internal/server` (full package) and the new tests
  `TestAutoTargetRouteDeadHoldsEarnedUp`, `TestNoteAutoTargetRouteDeadHoldOncePerInterval`,
  `TestDiscoveryBackoffReasonHasALabel`, `TestChanHealReconnectBackoffUntil`,
  `TestChanStatusHealerBacksOffPeerThatNeverReconnects`.
- After deploy: AutoTarget history should show `route_dead_hold` rows for
  targets in structural cooldown instead of `sells_above_node` UPs; scan
  history should show `discovery_backoff` on the ladder's paused days; the
  journal should show "next attempt after" for peers that keep failing, with
  far fewer `chan-heal: peer ... still unreachable` lines.
