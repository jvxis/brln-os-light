# API Spec (v0.2)

Base URL: https://127.0.0.1:8443

## Auth
- No auth in the current build. Access is expected via LAN or VPN.

## Error format
- Non-2xx responses return JSON: {"error": "message"}

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

GET /api/logs?service=lnd&lines=200
- Returns a list of log lines.

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

GET /api/lnops/channel/fees?channel_point=txid:index

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
