# Settled invoice cursor recovery

Notifications and Chat retain an authoritative checkpoint alongside their legacy
scalar cursor. It records the settlement index, add index, payment hash, latest
settlement time, and the time fence of a retired settlement sequence. This is
LOS state only: no LND configuration, SQL sequence or invoice is modified.

On connection, each consumer reads at most the latest 500 invoices (by add index)
with a 10-second timeout. An upgraded scalar cursor is anchored only to an exact
settlement-index match. A lower/equal index is not enough to reset: the candidate
must have a different hash, valid add index, and a strictly later settlement
time. Live candidates are independently confirmed using LookupInvoice. The
normal monotonic stream makes no extra per-invoice LND calls.

Recovery processes the bounded page chronologically before resubscribing. Each
event must persist successfully before its checkpoint advances. Old invoices
from the retired sequence cannot move the cursor back up. PostgreSQL writes the
checkpoint and compatibility index in one statement; file-only Chat uses atomic
checkpoint replacement. Event keys and directional payment hashes provide
idempotency on retry. Historical reconciliation always suppresses Telegram
mirroring, including recent historical entries; live events retain normal rules.

The service journal records confirmed sequence changes and missing anchors,
without invoice hashes, memos, credentials or payment data. Read/checkpoint errors
preserve the old position and trigger reconnect rather than acknowledging an
uncommitted event.

## Deliberate bounds

- This is defense in depth, not an LND database repair or migration tool.
- The initial probe does not scan the entire invoice database. If a legacy
  cursor's exact anchor is outside the last 500 invoices, historical automatic
  reset is deferred and logged. The connection time becomes a conservative
  anchor for later, independently confirmed live regressions.
- LND settlement timestamps have second precision. Ambiguous same-second
  lower-index events do not justify an automatic reset.
- A long-disconnected, already migrated node may require operator investigation
  when these bounds prevent establishing a trustworthy historical anchor.

## Verification

`go test ./internal/server -run 'TestInvoiceCursor|TestChatInvoiceCheckpoint|TestChatFileStoreInvoiceReplay'`

Tests exercise 31078 -> 1, pre-existing invoices settled after migration,
old-generation replay, checkpoint serialization/restart, persistence failure,
bounded probing, live confirmation, normal-stream call counts, historical mirror
suppression and file-backed Chat idempotency.
