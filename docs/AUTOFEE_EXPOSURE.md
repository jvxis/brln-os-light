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
the existing LND wrapper returns its transport error and does not convert
`FailedUpdates` into an error. This delivery exposes that distinction in
diagnostics while preserving the wrapper's existing return/retry behavior.
Changing that behavior needs a separate focused correction and regression review.

## Safety contracts

| Contract | Implementation / regression evidence |
| --- | --- |
| Observation cannot change fees or jobs | Separate worker and tables; no decision reads; RPC on/off equivalence test |
| New inbound costs, base fees and econ_ratio remain authoritative | Existing AutoFee/interlock golden and safety tests unchanged |
| Outbound seed/exploration, overrides, off channels and contracts remain intact | No evaluator edits; existing idle-discovery/micro-step and contract tests |
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
