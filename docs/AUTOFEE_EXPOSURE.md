# AutoFee exposure measurement — issue #184 / 0.5.34-Beta

## Scope and interpretation

This delivery establishes an independent observation ledger and bounded read
API for the measurement part of #184. It preserves the legacy 24-hour outcome
semantics and all pricing/rebalance behavior. Observed policy windows are
sampled evidence, not exact reconstruction of when every payment accepted a
fee. There is no historical backfill, optimizer input, new seed, economic-floor
strategy, inventory experiment, production parameter change or deployment.

Three concepts remain separate:

1. The evaluator's calculated decision is available in existing AutoFee results.
2. Manager policy RPC attempts record the submitted values after the last cap,
   their source, timestamps and acknowledgement/partial-failure status.
3. Independent local FeeReport/ListChannels samples show fees, availability and
   balances actually observed. They cannot prove remote gossip propagation.

Do not describe RPC acknowledgement as successful read-back. In particular,
the initial 0.5.34 wrapper returned only its transport error. Starting with
0.5.35, any `FailedUpdates` returns a typed `PolicyUpdateError`; an empty
response without a transport error is also rejected. Transport errors retain
their identity. Successful acknowledgements still do not prove read-back or
gossip propagation. Partial global rejection does not imply rollback of the
other channels. AutoFee does not immediately retry an application rejection;
ordinary later automation rounds are unchanged.
Live AutoFee up/down/applied summary counters now advance only after the
successful RPC result; dry-run counters still describe simulated decisions.
Rejections use the existing caller error paths and do not create a successful
AutoFee outcome. This is not a transactional rollback of evaluator state or
attempt cooldowns, nor a redesign of those existing persistence semantics.

Application records now include optional `failure_reasons`, bounded counts of
`unknown`, `pending`, `not_found`, `internal`, and `invalid_parameter`. Raw LND
`update_error` strings are never stored or exposed by this diagnostic. Old
records lack these reasons; absence is not proof of successful application.

## Safety contracts

| Contract | Implementation / regression evidence |
| --- | --- |
| Observation cannot change fees or jobs | Separate worker and tables; no decision reads; RPC on/off equivalence test |
| New inbound costs, base fees and econ_ratio remain authoritative | Existing AutoFee/interlock golden and safety tests unchanged |
| Outbound seed/exploration, overrides, off channels and contracts remain intact | Output-only cost evidence; existing idle-discovery/micro-step and contract tests |
| Sovereign budgets, FIFO and unsold inventory guard remain intact | No selection/execution/attribution edits; existing regressions |
| Zero-fee forwards are movement; assisted revenue is not added twice | Real PostgreSQL boundary/zero/assisted fixtures |
| Unknown policy or observation gaps do not imply free/empty channels | Nullable policies; unknown confidence; no policy_activity attribution |
| An intention or RPC response is not an applied/propagated fee | Separate attempts and read-only snapshots; failed-updates RPC fixture |
| No duplicated exposure attribution | Per-channel half-open adjacent intervals; boundary and pagination tests |
| Restart/replay/database failure is safe | Immutable idempotent inserts, batch transaction, session boundary, cancellation tests |
| Legacy outcomes stay legible and keep their semantics | Actual schema migration and legacy measureOne integration test |
| Private audit evidence remains private | Only synthetic fixtures; no node dumps or credentials in repository |

## Storage and load

`autofee_policy_observations` stores timestamped snapshots per channel.
`autofee_policy_applications` stores submitted policy RPC observations. Both are
append-only except for bounded 30-day retention cleanup. Repeated inserts do
nothing. Interval projections and notification aggregation use a repeatable-read
transaction rather than persisting possibly incomplete notification totals.

The observer runs independently of AutoFee enablement, once at startup and
every five minutes. It performs two RPCs, with no per-channel graph queries,
alias fetches, or mutation of the active engine's state/caches. Failed sampling,
database writes or queue overflow increment observation loss. Restart changes
the session identifier. Database/observation errors cannot fail policy RPCs or
the existing decision loop. Retention removes at most 5,000 rows per table per
tick; a large pre-existing backlog can temporarily retain more than 30 days.

Each API request is restricted to one channel, 24 hours, at most 200 intervals,
2,000 application records and five seconds. Notification queries are batched
over disjoint intervals. Acquisition windows and telemetry loss are visible.
An empty response is lack of coverage, not evidence of zero routing demand.

## Validation and rollout boundaries

### 0.5.35 margin-input evidence

New evaluated channel results include optional `cost_evidence`, persisted in
the existing JSON log payload and returned by `/api/lnops/autofee/results`.
It describes the actual **margin input**, independently of `floor_base_src`
and `floor_base_ppm`, which can subsequently be softened or replaced:

- `source` is the selected engine source; `kind` separates channel rebalance,
  historical channel rebalance, global/blended references, outgoing references,
  market references and the configured minimum. These are not FIFO costs.
- `reference_ppm` is the selected value before the minimum clamp;
  `effective_ppm` is the input after it (or the newer rebalance anchor if that
  supersedes it). `min_adjusted` exposes the difference. Source `seed` describes
  the engine fallback and does not by itself prove a fresh market sample.
- `margin_actionable` records the existing gate's decision, **not** proof that
  the reference represents paid channel cost. `forward_count` is the evaluated
  count, with the same lookback semantics as the decision.
- `negative_margin_guard` means the `no-down-neg-margin` tag occurred during
  evaluation. Later gates may override the target; it is neither exclusive
  causal attribution nor a simulated alternative price.

No decision reads this object. Prices, discounts, seed selection, rebalance
selection, budgets and inventory attribution are unchanged. This is provenance
measurement, **not** the native-seed comparison, a new optimizer or a full
counterfactual simulation. Legacy results and early skipped decisions may lack
it; missing evidence is unknown, never zero acquisition cost. No schema
migration or backfill is required.

After rollout, inspect ordinary automation attempts for failure counts and
reason codes, then group new cost evidence by source, liquidity, actual policy
exposure and movement. Compare complete multi-day windows and matured inventory
cohorts; do not sum assisted revenue into routing profit or interpret flat fees
as proof of absent demand. Local tests are not production performance evidence.

Run the ordinary Go tests and UI build. PostgreSQL integration tests use only
an explicitly named disposable database:

```text
LOS_AUTOFEE_EXPOSURE_TEST_DSN=postgres://.../los_autofee_exposure_test_<name>
go test ./internal/server -run TestAutofeeExposure -count=1
```

The tests refuse database names without that prefix. They use the actual
notification/AutoFee schemas, clear synthetic fixtures only in that database,
and verify migration replay, duplicate observation/application replay,
exclusive boundaries, pagination, zero-fee traffic, assisted movement,
rebalance flags, manual changes, restart, cancellation and legacy outcomes.
RPC fixtures verify no policy changes from sampling and unchanged update
requests/results with observation enabled or disabled.

No production validation is implied by local tests. Before deploying this
instrumentation, review storage growth, API latency and sampler overhead in
the authorized lab/rollout. Set `LIGHTNINGOS_AUTOFEE_EXPOSURE_ENABLED=0` and
restart Manager to stop collection; the additive tables may stay for rollback.
Disabling collection neither restores fees nor initiates any transfers.

Remaining #184 work is explicit: precise application/propagation boundaries
cannot be recovered from these samples; external/manual changes between
samples may remain unobserved. Historical confidence and exact application
tracking need further evidence. Native seed comparison (1B), economic
coherence and stock-sale experiments remain separate conditional deliveries.
