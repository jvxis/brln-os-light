# Forward policy snapshot (0.5.58)

## Why

LND's forwarding history reports one number per forward: the total fee it
earned, which is `max(0, outbound fee + inbound fee)`. Since 0.5.5x the
AutoFee engine sets negative inbound fees (balanced inbound discounts) on
selected channels. From that moment the fee stored for a forward no longer
says what the outgoing channel's policy asked for: a forward that paid zero
may have crossed an exit priced at hundreds of ppm, with the discount of the
entry channel eating the whole fee.

Every consumer of realized prices reads that stored fee: the realized sale
price that bounds the drained retest and the exploration lots in the
Rebalance Center, the interlock floors, the 7/21 day forward stats of
AutoFee, the ranking, the reports. None of them can tell a discounted forward
from an ordinary fee change, and the policy in force at the time is not
recorded anywhere, so the distinction cannot be rebuilt later.

The 14/11 and 15/11 audits on the production node showed the effect: flow
attracted by a discounted sink left at zero or near-zero fee through sellers
that the autopilot refills with paid rebalances, the realized price of those
sellers dropped well below their advertised fee, and the retest bought them
back at close to the advertised price.

## What changes

A side table, written by the forwards poller next to the row it already
stores, and nothing else.

### Table `forward_policy_snapshot`

| Column | Meaning |
|--------|---------|
| `event_key` | primary key, the same key as the forward row in `notifications` |
| `occurred_at`, `chan_id_in`, `chan_id_out`, `fee_msat` | copied from the forward for self-contained queries |
| `fee_policy_out_msat` | what the outgoing channel's current policy charges for the forwarded amount (`base + amount × rate / 1e6`) |
| `inbound_fee_msat` | the incoming channel's inbound policy applied to the incoming amount; negative is a discount; null when the incoming channel is not one of ours |
| `inbound_discount_msat` | `max(0, fee_policy_out_msat − fee_msat)`: the fee actually given away |
| `captured_at` | when the snapshot was taken |

### Writer

`computeForwardPolicySnapshot` (pure) and `storeForwardPolicySnapshot` in
`forward_policy_snapshot.go`. The forwards poller in `notifications.go`
already loads every channel's policy (`ListChannels`) at the start of each
poll; the snapshot uses that map. A forward whose outgoing channel is not in
the map, or has no policy, gets no snapshot. The write is a separate
`insert … on conflict do nothing` after the existing upsert; a failure is
logged (at most once per 10 minutes) and never touches the forward itself.

The schema is created by its own statement after the notifications schema,
so a failure there only disables the snapshot.

### What is deliberately not touched

- `notifications` table, its upsert statement and every reader of it
  (reports, Telegram, AutoFee, ranking, Insights).
- No decision reads the new columns. AutoFee, the Rebalance Center and
  Insights behave exactly as in 0.5.57.
- No backfill: the policy of past forwards is unknown. Data starts at
  deploy.

### Forward History card

`GET /api/lnops/forwards` items carry `fee_policy_out_msat`,
`inbound_fee_msat` and `inbound_discount_msat` when a snapshot exists
(absent otherwise, so older rows are distinguishable from a zero discount).
`totals` and `filtered` carry `inbound_discount_sat`. The card shows a
"Discount (sats)" column (dash for forwards recorded before the snapshot)
and a caption with the discount of the current slice when it is above zero.
The lookups are primary-key subselects per returned row; the base query and
its where clause are unchanged.

### Known limit

The policy is read at poll time, not at the exact forward time. The poller
runs every few minutes while AutoFee changes fees every few hours, so only
the forwards between a fee change and the next poll can carry a stale
policy. The error is bounded by one poll interval and is noted here so the
follow-up does not mistake it for discount.

## Follow-up (not in this release)

Once a week of snapshots exists on both production nodes: use
`inbound_discount_msat` to clean the realized sale price that feeds the
drained retest, the exploration lots and the interlock floors, and decide
whether the discount should also be deducted from the estimated gain of a
rebalance target. Scope to be set from the data, together with the first
week of the inbound discount exit cap (0.5.57).

## Validation

- `go test ./internal/server -run 'ForwardPolicy|PolicyFeeMsat|ForwardHistory'`
  green; full `go test ./internal/server` green; `tsc --noEmit` clean.
- Unit tests: no discount, partial discount, discount that zeroes the fee,
  missing outgoing policy, unknown incoming channel, and the fee formula
  against LND's (base + amount × rate / 1e6, negative rates allowed).
- Post-deploy: `select count(*), sum(inbound_discount_msat)/1000 from
  forward_policy_snapshot where occurred_at > now() - interval '1 day'`
  grows with the forwards of the day; the Forward History card shows the
  Discount column filled for new forwards and a dash for older ones; the
  journal has no `forward policy snapshot` error lines.
