# Forward History (Lightning Ops) — 0.5.55

Collapsible card on Lightning Ops, placed right above the Peers card, that lets
the operator browse every forward the node routed and measure any slice of it
against the whole window.

## Data source

The Notifier already stores one row per settled forward in `notifications`
(`type='forward'`): both channel ids, incoming/outgoing msat amounts, fee in
msat and the `"in -> out"` alias pair. The card reads that table; no new table,
poller or LND call per row is needed. Aliases of open channels come from LND
(`ListChannels`, cached for 60 s); closed channels fall back to the stored
alias pair.

## Endpoint

`GET /api/lnops/forwards`

| Param | Values | Default |
|-------|--------|---------|
| `range` | `24h`, `7d`, `30d`, `90d` | `7d` |
| `chan_in` / `chan_out` | numeric channel id (both optional, combinable) | — |
| `sort` | `time`, `amount`, `fee`, `ppm` | `time` |
| `order` | `asc`, `desc` | `desc` |
| `limit` / `offset` | page size ≤ 500 / offset ≤ 50 000 | 200 / 0 |
| `tz` | IANA zone used for the timeline buckets | UTC |

Response: `totals` (whole window) and `filtered` (current slice), each with
count, volume, fee and average ppm; `channels` (per-channel in/out aggregates
for the window, used to build the filter lists); `flow_in` / `flow_out` (top 12
channels of the slice); `buckets` (hourly for 24h, daily otherwise, filled with
empty periods); paginated `items` with `in -> out` aliases, amount, fee and ppm.

All queries hit the existing `notifications_type_occurred_id_idx`,
`notifications_chan_in_idx` and `notifications_chan_out_idx` indexes. On the
regtest lab with ~900 rows the whole response takes ~10–15 ms.

## UI

- Window selector, incoming/outgoing channel selects (with per-channel counts
  and a "closed" marker), swap button and active-filter chips.
- Four KPI tiles: routed volume, fees earned, forwards and average ppm. With a
  filter active each shows the slice, the window total and the share bar.
- Timeline (bars = volume, line = fees) in the browser's time zone.
- "Where it came in" / "Where it went out" panels for the current slice; a
  click toggles that channel as the filter. Route chips in the table do the
  same.
- Sortable table (time, amount, fee, ppm) with "Load more" paging. The first
  page refreshes every 60 s while the card is open and no extra page has been
  loaded.

Strings live under `lightningOps.forwardHistory*` (EN and pt-BR).

## Validation

- `go test ./internal/server -run 'ForwardHistory|ForwardPPM|SplitForwardPeerAlias'`
  covers option parsing, alias split, ppm, bucket filling, order-by and
  placeholder numbering.
- Deployed to the regtest lab (Alice) with 900 seeded forwards across open and
  closed channels: totals vs filtered subtotals, flow panels, hourly buckets in
  `America/Sao_Paulo`, sorting, paging and input validation checked through the
  API and the rendered card in pt-BR and EN.
