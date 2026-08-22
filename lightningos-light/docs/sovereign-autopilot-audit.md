# Sovereign Autopilot Audit Process

Use this process before enabling `sovereign_live`, after changing autopilot
parameters, and whenever movement drops after a rebalance tuning session.

The goal is to answer four questions:

1. Is the autopilot selecting targets with positive realized economics?
2. Are guardrails filtering noise or blocking the actual opportunity set?
3. Are high-movement low-fee sources being drained into targets that can sell?
4. Is the node preserving movement while capital is being shifted?

## Run The Audit

From the repo root:

```powershell
$env:BRLN_API_URL = "https://jvx-minipc01:8443"
$env:BRLN_API_PASSWORD = "<admin password>"
.\lightningos-light\scripts\sovereign-autopilot-audit.ps1
```

The script is read-only after login. It writes raw snapshots and `report.md`
under `sovereign-audit-YYYYMMDD-HHMMSS`.

Useful options:

```powershell
.\lightningos-light\scripts\sovereign-autopilot-audit.ps1 `
  -BaseUrl "https://jvx-minipc01:8443" `
  -OutDir ".\audit-sovereign-current" `
  -HistoryLimit 288 `
  -BaselineDays 14
```

Do not commit generated reports if they include peer/channel data from
production.

## Interpretation Checklist

### 1. Mode Split

Check the "Mode Split" section first. `sovereign_shadow` records decisions but
does not execute jobs, queue the guaranteed slot, run the rules-auto fallback,
or apply AutoTarget changes. It is a pure dry run; manual API-triggered jobs are
outside the scheduler-mode guarantee.

If the last 24h mixes `sovereign_live` and `sovereign_shadow`, split conclusions:

- `sovereign_live`: use execution metrics and realized economics.
- `sovereign_shadow`: use only candidate quality and skip reasons.

### 2. Funnel Health

Look at skip reasons in this order:

- `roi_guardrail` and `profit_guardrail`: target economics are below configured
  price/cost requirements before sovereign ranking can help.
- `low_success_opportunity_below_floor`: targets may look profitable but have
  too little empirical route success.
- `target_structural_cooldown` and `target_cooldown`: routing is failing across
  many sources; loosening this increases attempts, not necessarily movement.
- `paid_liquidity_unsold_cooldown`: recent paid liquidity did not sell enough.
- `expected_profit_below_min`: candidate survived early gates but profit is too
  small for the configured floor.

If `roi_guardrail` dominates, the target set is economically thin. If
`target_structural_cooldown` dominates, the target set may be route-dead. If
`low_success_opportunity_below_floor` dominates, ranking is finding targets that
are expensive to reach or historically unreliable.

### 3. Economics Gate

Use the overview and "Sovereign Live Execution" sections:

- `sovereign_realized_net_7d_sat` must be positive before live mode is trusted.
- `sovereign_sellthrough_7d` below roughly 80% means too much paid liquidity is
  sitting unsold.
- Attempt success rate below 5% means the planner is spending most work on
  failed routes. This can still be acceptable for probing, but not for live
  capital allocation.

Positive expected profit in `sovereign_history` is not enough. The live audit is
passed only when attributed forward fees exceed paid rebalance fees over the
chosen attribution window. Forward attribution is FIFO per target channel:
each forward consumes the oldest eligible paid-liquidity lot and is counted at
most once, even when multiple jobs have overlapping attribution windows.

### 4. Source Objective

The business objective is source-first:

> drain high-movement, low-fee sources into targets that sell higher and fast
> enough.

The current autopilot is mostly target-first: it ranks target channels, then the
job runner chooses sources. The source audit sections show whether the intended
sources are actually used and whether they succeed.

Red flags:

- A high-assisted-fee source appears in "Assisted Source Candidates" but never
  succeeds in "Sources Used By Sovereign Jobs".
- The same source has many `no_route` or `no_amount` failures.
- High-revenue or high-assisted sources are protected by source opportunity
  cost when the desired policy is to drain them.

If this happens, the next design should rank `(source, target)` pairs, not only
targets.

## Suggested Pass/Fail Criteria

Keep `sovereign_live` off unless all are true over at least 24h, preferably 7d:

- Sovereign realized net is positive.
- Sovereign sell-through is above 80%.
- Sovereign attempt success rate is above 5%, or the successful sats per attempt
  is high enough to justify the route churn.
- Top selected targets are not repeatedly failing with `all sources failed`.
- Top assisted sources are either being drained successfully or explicitly
  excluded from the source objective.

## Current Design Risks To Watch

- Shadow mode intentionally blocks all scheduler mutations. A manual job started
  through the API remains possible and must be separated from shadow telemetry.
- `sovereign_exploration_slot_pct` can bypass empirical-history gates. When the
  candidate set is small, the scarcity rule may mark many candidates as
  exploration, allowing route-dead targets into live execution. This is an
  intentional discovery mechanism: exploration jobs are persisted and visible
  through `exploration_slot`, while five consecutive failed exploration jobs in
  24h apply a 12h target burnout that survives Manager restarts.
- `sovereign_target_source_quarantine_hours`,
  `fresh_paid_liquidity_lock_enabled`, and `source_min_payback_progress` are the
  direct protections that stop a recently rebalanced channel from immediately
  becoming a source before the paid liquidity has had time to sell or pay back.
- `SovereignSourceOpportunityCostEnabled` is a separate source-ranking/floor
  guard: for channels that are also auto/manual targets, it keeps liquidity near
  `target_outbound_pct + 5pp` and sorts lower-revenue sources first. It helps
  preserve channels that are also good targets, but it can conflict with a
  source-first strategy if the desired behavior is to drain a low-fee assisted
  source even when it also has target value.
- ROI and profit guardrails are target-level. They do not prove that a specific
  source can reach the target.

## Better North-Star Metric

Track this per run and per 7d window:

```text
source_pressure_sat * target_spread_ppm * pair_success_probability
minus expected_route_cost_sat
minus opportunity_cost_of_source_sat
```

The unit of decision should be a `(source_channel_id, target_channel_id)` pair.
The target-only score can remain as a coarse pre-filter, but live selection
should prefer pairs that both drain the right source and have empirical route
success.
