# API Spec (v0.2)

Base URL: https://127.0.0.1:8443

## Auth
- Session auth with secure HTTP-only cookie when `features.enable_login=true`.
- Public auth endpoints:
  - `GET /api/auth/state`
  - `POST /api/auth/setup`
  - `POST /api/auth/login`
  - `POST /api/auth/recovery`
- Authenticated auth endpoints:
  - `POST /api/auth/logout`
  - `POST /api/auth/reauth`
- Manual external on-chain sends (`POST /api/wallet/send`) require a fresh reauthentication step.
- Sensitive scopes accepted by `POST /api/auth/reauth` include:
  - `wallet_send_external`
  - `macaroon_export`
  - `node_retirement_control`
  - `succession_live_control`

## Error format
- Non-2xx responses return JSON: `{"error":"message","code":"optional_code"}`

## UI preferences

GET /api/ui/preferences/menu
- Returns the node-wide menu preferences used by every authenticated browser.
- `exists=false` indicates that no preference has been saved yet.

PUT /api/ui/preferences/menu
Body:
{
  "version": 1,
  "favorites": ["dashboard", "wallet", "fee-center"],
  "hidden": ["terminal", "logs"]
}
- Route keys are stored instead of translated labels.
- Hidden items are removed from favorites during normalization.
- Preferences are persisted in PostgreSQL. Browser storage remains a fallback when the service is unavailable.

## Health and system

GET /api/health
- Returns overall status and issues.

GET /api/system
- System stats (uptime, CPU, RAM, disks, temperature).

GET /api/disk
- SMART and disk health details.

GET /api/postgres
- Postgres service status and DB stats.

## Bitcoin

GET /api/bitcoin
- Remote Bitcoin RPC and ZMQ status.

GET /api/bitcoin/active
- Returns active source (remote or local) with status.

GET /api/bitcoin/bip110
- Informational BIP 110 monitor. Calculates bit-4 signaling from the active `bitcoind`, fetches `https://bip110monitor.com/api`, and compares both sources at the same sampled height when possible.
- Returns the scheduled phase and milestones, internal and public samples, comparison status, and an informational risk level.
- Never changes the Bitcoin backend, LND configuration, channel state, or service state.

GET /api/bitcoin/source
- Returns {"source":"remote"|"local"}.

POST /api/bitcoin/source
Body:
{
  "source": "remote"|"local"
}
- Updates lnd.conf for the selected source and restarts LND.

GET /api/bitcoin-local/status
- Status and chain info for local Bitcoin Core (if installed).

GET /api/bitcoin-local/config
- Current prune mode and settings.

POST /api/bitcoin-local/config
Body:
{
  "mode": "full"|"pruned",
  "prune_size_gb": 10,
  "apply_now": true
}

## Elements

GET /api/elements/status
- Status and chain info for the Elements (Liquid) node (if installed).
  - Includes mainchain source and RPC host/port.

GET /api/elements/mainchain
- Returns Elements mainchain source, RPC host/port, and local readiness.
  - local_ready: true when a local Bitcoin RPC (App Store or external) is reachable and fully synced.

POST /api/elements/mainchain
Body:
{
  "source": "remote"|"local"
}
- Updates elements.conf and restarts the Elements service.

GET /api/mempool/fees
- Recommended fee rates from mempool.space.

## LND status and config

GET /api/lnd/status
- LND state, sync, channels, balances.

GET /api/lnd/config
- Supported settings, current values, and raw lnd.conf.

POST /api/lnd/config
Body:
{
  "alias": "MyNode",
  "color": "#ff9900",
  "min_channel_size_sat": 20000,
  "max_channel_size_sat": 5000000,
  "apply_now": true
}

POST /api/lnd/config/raw
Body:
{
  "raw_user_conf": "full lnd.conf text",
  "apply_now": true
}

## Tor status and upgrade

GET /api/tor/upgrade/status
- Returns the installed Tor/runtime version, current APT candidate, package source, service/port health, update availability, transient upgrade state, and check timestamp.
- `force=1` refreshes APT metadata before returning status.

POST /api/tor/upgrade/start
- Starts a transient `lightningos-tor-upgrade` systemd unit after the authenticated UI confirmation.
- The embedded helper configures the official Tor Project repository when missing, upgrades `tor`, `tor-geoipdb`, and the repository keyring, restarts Tor, and waits for bootstrap.
- Returns `409` when an upgrade is already running or the official candidate is already installed.

## Wizard

GET /api/wizard/status
- {"wallet_exists": true|false}

POST /api/wizard/bitcoin-remote
Body:
{
  "rpcuser": "...",
  "rpcpass": "..."
}
- Validates RPC and ZMQ, stores secrets, updates lnd.conf, restarts LND.

POST /api/wizard/lnd/create-wallet
Body:
{
  "seed_passphrase": "optional"
}
- Returns seed words.

POST /api/wizard/lnd/init-wallet
Body:
{
  "wallet_password": "...",
  "seed_words": ["..."]
}

POST /api/wizard/lnd/unlock
Body:
{
  "wallet_password": "..."
}

## Actions and logs

POST /api/actions/restart
Body:
{
  "service": "lnd"|"lightningos-manager"|"postgresql"
}

GET /api/audit/events?limit=100&action=utxo.lock
- Returns recent structured audit events.
- Optional filters: `action`, `session_id`, `target`.
- `limit` defaults to 100 and is capped at 500.

GET /api/logs?service=lnd&lines=200
- Returns a list of log lines.
- `service=bitcoin` returns local Bitcoin logs from the LightningOS Docker app when installed, otherwise from a local bitcoind systemd unit.

## Wallet

GET /api/wallet/summary
- Balances and recent activity.

POST /api/wallet/address
- Returns a new on-chain address.

POST /api/wallet/invoice
Body:
{
  "amount_sat": 1000,
  "memo": "optional"
}

POST /api/wallet/decode
Body:
{
  "payment_request": "lnbc..."
}

POST /api/wallet/pay
Body:
{
  "payment_request": "lnbc..."
}

POST /api/wallet/send
Body:
{
  "address": "bc1...",
  "amount_sat": 1000,
  "sat_per_vbyte": 5
}

GET /api/onchain/utxos?limit=500
- Returns wallet UTXOs enriched with local UTXO Manager metadata and lease state.
- `limit` caps the response size only. The backend intentionally lists and enriches the full wallet UTXO set before slicing so metadata maintenance sees the complete live outpoint set.

GET /api/onchain/provenance/metrics
- Returns in-memory Wallet Flow source-chain counters since process start.
- Includes per-source-class calls, hits, errors, unavailable errors, fallthroughs, and recent p95 latency in milliseconds for `bitcoind`, `electrs`, and `public`.

## Lightning Ops

GET /api/lnops/channels
- Channel rows with pending HTLCs may include `forwarding_channel_alias` and `forwarding_channel_short_id` in each pending HTLC entry, alongside the existing numeric `forwarding_channel_id`.
- Pending opening rows include `funding_initiator` (`local`, `remote`, `both`, or `unknown`). When the public transaction lookup is attempted, they also include `funding_tx_status` (`mempool`, `confirmed`, `not_found`, or `unavailable`) and may include `funding_tx_fee_sat`, `funding_tx_vsize`, `funding_tx_effective_fee_rate_sat_vb`, and `funding_tx_rbf`.
- `funding_tx_effective_fee_rate_sat_vb` is calculated from the observed funding transaction fee and weight. It is distinct from `funding_fee_rate_sat_vb`, which is the LND funding/commitment fee reference used by the existing bump workflow.

GET /api/lnops/channel-db-impact
- Returns a channel.db maintenance report for nodes using LND's Bolt channel database.
- For Postgres-backed LND nodes, returns HTTP 200 with `available=false` and `db_backend="postgres"`.
- Uses LND `ListChannels.num_updates` to rank open channels by update count, update share, estimated channel.db footprint, updates/day, and updates per 1M sats capacity.
- `estimated_db_bytes` and `estimated_db_gb` are proportional estimates based on each channel's share of total updates and the physical `channel.db` size. LND/BoltDB do not expose exact bytes per channel over RPC.
- Response shape:
{
  "available": true,
  "db_backend": "bolt",
  "size_available": true,
  "channel_db_size_bytes": 11400000000,
  "channel_db_size_gb": 11.4,
  "total_updates": 232000000,
  "total_channels": 42,
  "top10_updates": 146000000,
  "top10_share_pct": 62.9,
  "channels": [
    {
      "channel_point": "txid:index",
      "channel_id": 1043834558060691457,
      "channel_id_str": "1043834558060691457",
      "short_channel_id": "949362x1404x1",
      "peer_alias": "Peer alias",
      "remote_pubkey": "02...",
      "capacity_sat": 5000000,
      "num_updates": 42800000,
      "share_pct": 18.45,
      "estimated_db_gb": 2.1,
      "updates_per_day": 58000,
      "updates_per_million_sat": 8560000,
      "recommendation": "critical"
    }
  ]
}

GET /api/lnops/peers

POST /api/lnops/peer
Body:
{
  "address": "pubkey@host:port",
  "perm": true
}

POST /api/lnops/peer/disconnect
Body:
{
  "pubkey": "..."
}

POST /api/lnops/peers/boost
Body:
{
  "limit": 25
}

GET /api/lnops/macaroon/options
- Returns available LND macaroon permissions and LOS presets for the custom macaroon tool.

POST /api/lnops/macaroon/bake
Body:
{
  "preset": "invoice_permissions",
  "permissions": [
    { "entity": "invoices", "action": "read" },
    { "entity": "invoices", "action": "write" }
  ],
  "confirm_password": "..."
}
- Requires an authenticated admin session, CSRF, and recent `macaroon_export` reauthentication or inline `confirm_password`.
- Returns `file_name`, `root_key_id`, selected `permissions`, `macaroon_hex`, and `macaroon_base64`.
- The generated macaroon is returned only in the immediate response and is not persisted by LOS.

GET /api/lnops/channel/fees?channel_point=txid:index

GET /api/lnops/channel/detail?channel_point=txid:index&limit=30
- Returns live channel state plus historical per-channel data for the Lightning Ops detail modal: peer status, balances, policy/settings, period economics, fee logs, routed payments, rebalances, sent/received payments, failed HTLCs, peer events, peer/channel notes, and historical coverage.
- Pending HTLC entries include `forwarding_channel_alias` and `forwarding_channel_short_id` when the forwarding channel is known; `forwarding_channel_id` remains available for compatibility.
- Returns `previous_channel_notes` when older channel notes are available for the same peer.
- `channel_id` can be used instead of `channel_point`; accepted formats are integer channel id or `blockxtransactionxoutput`.
- `limit` defaults to 25 and is capped at 100 for list sections.

POST /api/lnops/channel/notes
Body:
{
  "channel_point": "txid:index",
  "remote_pubkey": "02...",
  "peer_alias": "Peer alias",
  "channel_id": 1043834558060691457,
  "short_channel_id": "949362x1404x1",
  "note": "Operator notes for this channel"
}
- Stores an operator note for the channel detail modal.
- Metadata fields are optional but allow closed-channel notes to be shown later as previous notes for the same peer.

POST /api/lnops/channel/automation
Body:
{
  "channel_id": 1043834558060691457,
  "channel_point": "txid:index",
  "automation_mode": "parked",
  "fixed_fee_ppm": 250,
  "review_at": "2026-08-01",
  "automation_note": "Review after observation window",
  "restore_previous": false
}
- Sets the per-channel automation policy. `automation_mode` accepts `normal`, `parked`, or `close_candidate`.
- `channel_id` or `channel_point` must identify an open channel; both may be supplied.
- Parking a channel disables Autofee for that channel, disables rebalance auto and manual restart, and excludes the channel as a rebalance source.
- Unparking with `restore_previous=false` keeps Autofee and rebalance automations disabled until the operator explicitly enables them again.

### AutoFee/Rebalance automation intents

GET /api/lnops/automation-intents/config
- Returns the shared interlock mode (`off`, `shadow`, or `enforce`), intent TTLs,
  refill score multiplier, minimum confidence, and history retention.

POST /api/lnops/automation-intents/config
Body:
```json
{
  "mode": "shadow",
  "refill_score_multiplier": 1.2,
  "min_confidence": 0.7
}
```
- Updates the operator-controlled rollout mode and bounded scoring parameters.
- `shadow` records the hypothetical effect without changing AutoFee or Rebalance decisions.

GET /api/lnops/automation-intents?active=true&consumer=rebalance&kind=refill_target&limit=200
- Lists current intents and their producer profile/node calibration provenance.
- Includes `channel_id_str` so clients can preserve the full unsigned Lightning channel ID without JavaScript number rounding.
- Supported kinds in the MVP are `refill_target` and `protect_fee_floor`.

GET /api/lnops/automation-intents/history?limit=200
- Lists publish, resolve, and apply events without credentials or payment secrets.
- Events also include the exact `channel_id_str` representation.
- An `applied` event means the intent influenced a consumer decision (for example, a score or fee-floor calculation); it does not by itself mean a rebalance job was queued or completed.
- Returns `{ "ok": true, "policy": ... }`.

POST /api/lnops/peer/notes
Body:
{
  "remote_pubkey": "02...",
  "note": "Operator notes shared by every channel with this peer"
}
- Stores an operator note keyed by peer remote pubkey. It is returned as `peer_note` in channel detail responses for every open channel with that peer.

POST /api/lnops/channel/open
Body:
{
  "peer_address": "pubkey@host:port",
  "local_funding_sat": 200000,
  "private": false,
  "sat_per_vbyte": 5,
  "close_address": "optional"
}

POST /api/lnops/channel/close
Body:
{
  "channel_point": "txid:index",
  "force": false
}

POST /api/lnops/channel/fees
Body:
{
  "channel_point": "txid:index",
  "apply_all": false,
  "base_fee_msat": 1000,
  "fee_rate_ppm": 100,
  "time_lock_delta": 40,
  "inbound_enabled": false
}

## Node Retirement and Succession

GET /api/lnops/node-retirement/status
GET /api/lnops/node-retirement/sessions
GET /api/lnops/node-retirement/sessions/{id}
GET /api/lnops/node-retirement/sessions/{id}/events
GET /api/lnops/node-retirement/sessions/{id}/channels
GET /api/lnops/node-retirement/sessions/{id}/transfer

POST /api/lnops/node-retirement/sessions
Body:
{
  "source": "manual",
  "dry_run": false,
  "disclaimer_accepted": true,
  "confirm_password": "optional inline reauth"
}
- API-created sessions must use `source=manual`; `source=succession` is reserved for the internal succession scheduler.
- Live sessions (`dry_run=false`) require recent `node_retirement_control` reauthentication or inline `confirm_password`.
- Missing reauth returns HTTP 428 with `code=node_retirement_control_reauth_required`.

POST /api/lnops/node-retirement/sessions/{id}/confirm-coop
Body:
{
  "confirm_password": "optional inline reauth"
}
- Live cooperative close confirmation requires recent `node_retirement_control` reauthentication.

POST /api/lnops/node-retirement/sessions/{id}/decision
Body:
{
  "channel_point": "txid:index",
  "decision": "wait"|"force_close",
  "confirm_password": "optional inline reauth"
}
- Live `force_close` decisions require recent `node_retirement_control` reauthentication.

GET /api/lnops/succession/status
GET /api/lnops/succession/config

POST /api/lnops/succession/config
Body:
{
  "enabled": true,
  "dry_run": false,
  "destination_address": "bc1...",
  "confirm_password": "optional inline reauth"
}
- Saving a final config with `enabled=true` and `dry_run=false` requires recent `succession_live_control` reauthentication.
- Missing reauth returns HTTP 428 with `code=succession_live_control_reauth_required`.
- The automatic succession scheduler can still start its retirement session without an interactive password prompt once live succession has already been armed.

POST /api/lnops/succession/alive
POST /api/lnops/succession/simulate
- Do not require live-control reauthentication.

## App Store

GET /api/apps
- Returns app list with status.
- Public Pool may include optional `ufw_active` and `ufw_command` fields when UFW is active and the active Bitcoin source is an existing systemd/external local node.
- `bark-wallet` reports `scheme=https`, port `4004`, and the path of its generated UI login password. Its Bark daemon and API are not published on the host.

POST /api/apps/{id}/install
- Installs an app.
- For `bitcoincore` and `elements`, optional body:
{
  "data_dir": "/mnt/bitcoin-ssd/bitcoin"
}
- `data_dir` is install-time only; existing blockchain data is not migrated.
- `bitcoincore` defaults to `/data/bitcoin`; `elements` defaults to `/data/elements`.
- Custom `bitcoincore` data directories must be on an already mounted volume and have at least 10 GiB free.

POST /api/apps/{id}/start
POST /api/apps/{id}/stop
POST /api/apps/{id}/uninstall
- Uninstalling `bark-wallet` removes its containers and app definition but intentionally preserves `/var/lib/lightningos/apps-data/bark-wallet` so wallet/off-chain state is not destroyed by the generic App Store action.
- `loop` is optional. Stop and uninstall are rejected while any Loop swap is pending. Uninstall preserves `/var/lib/lightningos/apps-data/loop` for recovery and history.
- `loopout-brln` is a native internal app. Stop or uninstall pauses its active job after the current attempt and preserves PostgreSQL history.

GET /api/apps/loop/status
- Returns installed/running state, safe daemon version/network fields, server terms, and pending swap count. It never returns TLS or macaroon paths.

GET /api/apps/loop/swaps?limit=100
- Returns normalized Loop In/Out history. Channel IDs are strings to preserve uint64 precision.

POST /api/apps/loop/quote
- Body: `direction` (`out` or `in`), `amount_sat`, optional `conf_target`, `last_hop_pubkey`, `fast`, `routing_fee_limit_ppm`, and Loop Out `outgoing_channel_ids` as strings.
- Returns separate server and on-chain estimates plus a recommended miner-fee ceiling. For Loop Out, it first attempts a read-only LND route estimate for the quoted server destination and selected channels. If the destination is not present in the public graph, private route hints from previously settled Loop invoices to the same destination are reused in query-only route simulation (`routing_estimate_source=invoice_routes`). Successful swaps with the exact same channel set provide the final empirical fallback (`routing_estimate_source=history`). `routing_estimate_available=false` means no source was available; the routing fee ceiling remains available and enforced.

POST /api/apps/loop/swap
- Starts a manual swap only after obtaining a fresh quote and verifying the approved fee ceilings.
- Loop Out also requires `outgoing_channel_ids` as strings. An optional `destination_address` may be supplied; otherwise Loop uses the local LND wallet.
- Requires recent `loop_swap` reauthentication when login protection is enabled. Missing reauth returns HTTP 428 with `code=loop_swap_reauth_required`.
- Autoloop is intentionally not exposed or enabled.

GET /api/apps/loopout-brln/status
- Returns install/enable state and the current non-terminal job, if any.

POST /api/apps/loopout-brln/lightning-address/validate
- Body: `lightning_address`.
- Resolves the provider's public LNURL-pay metadata endpoint and validates its `payRequest`, HTTPS callback, and payment range without requesting an invoice or sending a payment.
- Returns the normalized address, provider minimum/maximum in msat, and comment allowance.

POST /api/apps/loopout-brln/preview
- Validates the Lightning Address and execution limits without requesting a payable invoice or sending an HTLC.
- Body fields: `lightning_address`, `total_sat`, `tranche_sat`, `interval_seconds`, `timeout_seconds`, `max_fee_ppm` (1–1,000,000), `min_local_percent`, optional `comment`, optional `selected_channel_ids` as decimal strings, and optional `suppress_failed_telegram`.
- The preview also checks the provider's per-payment minimum/maximum and comment policy for both the regular tranche and the final reduced tranche.
- Returns payment count, final tranche, maximum aggregate fee budget, safely drainable liquidity, warnings, and per-channel projections.

GET  /api/apps/loopout-brln/jobs?limit=50
POST /api/apps/loopout-brln/jobs
- Creates one background job at a time. Creation requires recent `loopout_brln` reauthentication when login protection is enabled; missing reauth returns HTTP 428 with `code=loopout_brln_reauth_required`.
- When `suppress_failed_telegram=true`, failed LND attempts belonging to the job remain in app history but are excluded from the Telegram activity mirror. Successful payment notifications are unchanged.
- A fresh LNURL-pay invoice is requested for each attempt. Its exact msat amount is verified before sending.

GET  /api/apps/loopout-brln/jobs/{id}
- Returns the job, individual payment attempts, and the app-local event timeline.

POST /api/apps/loopout-brln/jobs/{id}/pause
POST /api/apps/loopout-brln/jobs/{id}/resume
POST /api/apps/loopout-brln/jobs/{id}/cancel
- Pause and cancel take effect before a new payment begins or immediately after the current in-flight payment reaches a terminal LND state.
- Resume requires recent `loopout_brln` reauthentication.
- The app does not emit global or Telegram-specific notifications; successful payments continue to appear through the existing LND payment notification flow.

POST /api/apps/{id}/reset-admin
GET /api/apps/{id}/admin-password
- Password read is supported for LNDg, Fedimint Gateway, and Bark Wallet.
- Password reset is supported for LNDg and Bark Wallet. Resetting Bark Wallet invalidates existing UI sessions without changing wallet data.

## Notifications

GET /api/notifications?limit=200
- Returns stored notifications.

GET /api/notifications/stream
- Server Sent Events stream.

GET /api/notifications/backup/telegram
POST /api/notifications/backup/telegram
POST /api/notifications/backup/telegram/test

## Reports

GET /api/reports/range?range=d-1|month|3m|6m|12m|all
- Returns a daily series. Sat values are floats for msat precision.

GET /api/reports/custom?from=YYYY-MM-DD&to=YYYY-MM-DD
- Custom range, max 730 days.

GET /api/reports/summary?range=d-1|month|3m|6m|12m|all
- Totals and averages for the selected range.

GET /api/reports/live
- Metrics from today 00:00 local time to now.

## Rebalance channel automation

POST /api/rebalance/run/preview
- Resolves the effective amount and fee cap for an operator-triggered Manual Rebal In without creating a job.
- Body: `{"channel_point":"txid:index","target_outbound_pct":10,"amount_sat":10000,"fee_limit_ppm":1200}`.
- `amount_sat` and `fee_limit_ppm` are optional. Omitted values use the channel deficit capped by global `max_amount_sat`, and the configured fee-limit/economic-ratio calculation respectively.
- Returns the defaults, effective values, maximum fee in sats, and warnings when the one-job amount exceeds global `max_amount_sat`, is clamped to the live deficit, or the fee cap exceeds the channel outgoing fee.

POST /api/rebalance/run
- Starts an operator-triggered Manual Rebal In.
- Accepts the same optional `amount_sat` and `fee_limit_ppm` fields as the preview endpoint. Explicit values override global amount/fee mechanics for this job only and do not persist to the channel, Rebalance config, or AutoFee.
- Without overrides, behavior remains unchanged: the job uses the current deficit capped by `max_amount_sat` and derives its fee cap from `fee_limit_ppm` or outgoing policy × effective economic ratio.
- One-job overrides cannot be combined with `auto_restart`.

POST /api/rebalance/channel/guaranteed
- Adds or removes one exact LND channel from the scheduler-independent guaranteed rebalance pool.
- Body: `{"channel_id_str":"1005750773843558400","channel_point":"txid:index","enabled":true}`.
- `channel_id_str` is used to preserve uint64 precision in browser clients and is validated against `channel_point`.
- At most one eligible pool member is queued per automatic scan before the selected scheduler. The job bypasses score and strategic/history gates, but retains the automatic budget, fee cap, channel-busy, target-deficit, and route/source safety checks.

## Terminal

GET /api/terminal/status
- Returns whether the web terminal is enabled.
