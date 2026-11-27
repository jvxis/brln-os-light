# Manual rebalance: fixed source, one-job exclusion exception, absolute fee cap (0.5.60)

Slice of issue #430. Applies only to jobs the operator starts from the
"Rebal In" advanced options. The autopilot, auto-restart, guaranteed slots and
probe jobs do not read any of this.

## What the operator can now choose for one job

| Option | Payload field | Behaviour |
|---|---|---|
| Fixed source channel | `source_channel_point` (or `source_channel_id`) | The job uses that channel as the only source, on the delegated fast path and on the legacy loop. It is never swapped for another channel. |
| One-job exclusion exception | `allow_excluded_source` | Lets the fixed source be a channel that is excluded as a source. The persistent exclusion is not changed. |
| Absolute fee cap | `max_fee_msat` | The lower of this and the ppm limit applies to the total the job settles, across every lot. |

None of the three is persisted: they travel in memory with the job, like the
amount and ppm overrides that already existed. They cannot be combined with
auto-restart.

## Guards that still hold

The exception lifts the persistent source exclusion and nothing else. A fixed
source is refused, with a reason code, when:

| Code | Meaning |
|---|---|
| `source_not_found` | the channel is not open |
| `source_is_target` | it is the channel being refilled |
| `source_inactive` | the channel is inactive |
| `source_parked` | the channel is in parked mode |
| `source_quarantined` | its liquidity was just bought and is in quarantine |
| `source_no_free_liquidity` | nothing above the source floor and the paid-liquidity protection |
| `source_excluded` | excluded as a source and the exception was not requested |

The check runs twice with the same function: in the preview/run request
(HTTP 409 `fixed source not usable: <code>`, nothing queued, saved target not
touched) and again when the job starts, against the job's own snapshots (job
ends `failed` with the same text). The amount the source can give is still
its `max_source_sat`.

Manual jobs already did not read the pair-failure cooldowns, so a fixed
source is attempted even if the pair failed recently. The attempt's outcome
is recorded like any other.

## Absolute fee cap

- The cap is a budget owned by the job. Every fee limit the job computes goes
  through `calcFeeLimitMsat`, which clamps the policy limit to what is left
  of the budget. Each settled lot is charged against it.
- Paid fees are accounted in whole sats, some truncated from msat, so a lot
  is charged rounded up to the next sat boundary. The job can end slightly
  under the cap, never over it.
- When the budget is spent the remaining attempts are skipped and the job
  ends `partial` with `fee cap reached, remaining N sats`.
- The MPP pre-pass sends shards concurrently; with an absolute cap set the
  job skips it and uses the sequential loop, which charges lot by lot. The
  delegated fast path is a single LND payment whose fee limit is already a
  total across parts.
- The UI takes the cap in sats with up to three decimals and sends millisats.

## Preview

`POST /api/rebalance/run/preview` now also returns:

- `max_fee_msat`, `fee_cap_absolute`: the job's total fee ceiling and whether
  the absolute cap is the binding one;
- `source`: for a fixed source, whether it is usable, the blocked reason,
  whether it is excluded, whether the exception is in use, and how much it
  can give.

It does not query third-party HTLC policies and does not promise a route.

## Out of scope (rest of #430)

MPP controls per job, pre-verification of third-party policies, and full
per-hop telemetry.

## Validation

- Unit tests: `rebalance_operator_source_test.go` (reason codes, no
  substitution, cap shared across lots, clamp through `calcFeeLimitMsat`,
  preview, payload validation).
- Lab regtest: see the PR description.
