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
- `channel_id` can be used instead of `channel_point`; accepted formats are integer channel id or `blockxtransactionxoutput`.
- `limit` defaults to 25 and is capped at 100 for list sections.

POST /api/lnops/channel/notes
Body:
{
  "channel_point": "txid:index",
  "note": "Operator notes for this channel"
}
- Stores an operator note for the channel detail modal.

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

## App Store

GET /api/apps
- Returns app list with status.

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

POST /api/apps/{id}/reset-admin
GET /api/apps/{id}/admin-password
- Only supported for LNDg today.

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
