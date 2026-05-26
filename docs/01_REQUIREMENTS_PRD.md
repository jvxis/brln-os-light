# LightningOS - PRD (v0.2)

## Personas
- P1 Beginner: wants a guided setup without running Bitcoin Core locally.
- P2 Intermediate: wants a clean UI plus access to logs and ops tools.
- P3 Power user: wants reporting, automation, and optional apps.

## Goals
- Make first run safe and guided.
- Provide daily operational visibility for LND and Bitcoin.
- Keep the core system native and stable (systemd, no Docker).

## Functional requirements

### Wizard
- Step 1: connect to Bitcoin remote
  - Collect RPC user and password.
  - Validate RPC and ZMQ connectivity.
  - Store secrets in /etc/lightningos/secrets.env.
- Step 2: LND wallet
  - Create wallet (show 24 words once, never store the seed).
  - Import wallet (user inputs 24 words, do not persist).
- Step 3: unlock wallet
  - Unlock using wallet password.

### Dashboard
- Global health (OK, WARN, ERR) with clear issues.
- Cards for system, disks, Postgres, Bitcoin, LND.
- Quick actions: restart LND and manager.

### Wallet
- Balances (on-chain and lightning).
- Create invoice and pay invoice.
- Decode invoice.
- Send on-chain.
- Basic recent activity.

### Lightning Ops
- List channels and peers.
- Open and close channels.
- Update channel fees (base, ppm, timelock, inbound).
- Boost peers by mempool connectivity ranking.

### Reports
- Daily job at 00:00 local time computes D-1 metrics and stores in Postgres.
- Idempotent UPSERT by report_date.
- Stored metrics include revenue, rebalance cost, net, counts, and balances.
- Live results endpoint computes metrics from 00:00 to now on demand.
- Date ranges supported: D-1, month, 3m, 6m, 12m, all.
- Precision must use msats to preserve decimals.

### Notifications
- Store and list events in Postgres.
- Optional Telegram backup config and test.
- SSE stream endpoint for live updates.

### LND Config
- Basic settings in UI: alias, color, min and max channel size.
- Advanced raw editor with rollback on failure.
- Toggle Bitcoin source between local and remote.

### Bitcoin Local
- Manage a local Bitcoin Core node via Docker (optional app).
- Show sync status, chain info, prune config, and block cadence card.

### App Store
- List available apps.
- Install, start, stop, uninstall.
- Optional admin password helpers for specific apps.

### Terminal
- Optional GoTTY terminal proxy with credentials.

### Logs
- Tail logs for LND, manager, and Postgres.

## Non-functional requirements
- No seed phrase persistence.
- LAN or VPN access only by default.
- Idempotent operations for installer and reports job.
- Clear errors when LND or Bitcoin RPC is unavailable.
- Mobile friendly UI.

## Acceptance criteria
- Installer finishes with UI reachable and systemd services active.
- Wizard completes and LND can unlock successfully.
- Reports daily job writes reports_daily and live endpoint works.
- App Store can install and run LNDg and Bitcoin Core.
- Notifications and logs endpoints return data without breaking other features.
