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

## Terminal

GET /api/terminal/status
- Returns whether the web terminal is enabled.
