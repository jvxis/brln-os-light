# Stock gate: sibling channels count as the peer's stock (0.5.60)

## The problem

A peer with several channels sells through whichever one has balance: LND
forwards an HTLC over any channel to the next hop whose policy the HTLC
satisfies. The stock gate ("sold, then refill") already measured **demand**
per peer, adding up the sales of all its channels. **Stock**, however, was
only the unsold remainder of the lots the autopilot had paid for, and the
first two unsold lots were always let through so that a drained channel
receives its first refills.

With parallel channels those two rules together bought liquidity nobody
needed. Observed shape: three channels to one peer at the same outgoing fee.
The two smaller ones held roughly four weeks of the peer's demand and kept
selling without any refill. The large one sat below its target, so the
autopilot bought lot after lot into it; the lots stayed there, because the
peer's sales kept leaving through the siblings. Over 30 days the purchases
into that channel cost several times what the channel sold.

## What changes

For each target channel the gate now also looks at its **siblings**: the other
channels to the same peer that are

- active, and
- priced at or below the target's outgoing fee.

A sibling priced above the target does not count: a payment that pays the
target's fee cannot be forwarded through it.

The siblings' local balance, plus what is left of the lots bought into the
target itself, is compared with the **same allowed stock the gate already
computes** (the peer's demand in the window, the cover days, or the share of
capacity while demand is unknown). When it reaches that limit the purchase
waits, even if it would be the first lot: the "first refills" exemption is
for a drained peer, and this peer is not drained.

Lots bought into a sibling are already inside the sibling's balance and are
counted once.

## What does not change

- No new parameter and no new threshold. The limit is the existing one; only
  what counts against it changes.
- A peer with a single channel behaves exactly as before.
- With `stock_gate_enabled` off nothing happens.
- Manual jobs and the operator's guaranteed slot do not go through the gate.
- AutoTarget and AutoFee remain per channel.
- The skip reason is still `paid_stock_gate`. A selected decision carries
  `stock_sibling_local_sat` when siblings were counted, and the Rebalance
  Center shows it next to the stock line.

## When the gate reopens

- The siblings sell down below the allowed stock.
- AutoFee moves a full sibling above the target's fee. That is correct (the
  sibling no longer serves the target's buyers), but two channels whose fees
  cross repeatedly would make the gate open and close with them.

## Known limit

A small sibling cannot carry a payment larger than its balance, which the
large channel could. The gate does not model HTLC sizes.

## Validation

Unit tests in `rebalance_stock_gate_test.go` cover: siblings holding the
allowed stock block the very first lot; a dearer or inactive sibling does not
count; fee order decides which channel sees which; own unsold lot and sibling
balance add up; a sibling's lot is counted once; the gate reopens when the
siblings run low; single-channel peers and a disabled gate are unaffected.

Lab regtest, three nodes, a second channel opened to the same peer (full),
the first one drained and set as an autopilot target:

| Scenario | Scan result for the drained channel |
|---|---|
| Sibling full, same fee | skipped, `paid_stock_gate`, nothing bought yet |
| Sibling full, priced above the target | selected and queued; lot settled |
| Sibling back at the same fee | skipped again, `paid_stock_gate` |
