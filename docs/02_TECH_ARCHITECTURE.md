# Technical Architecture (v0.2)

## Components
1) lightningos-manager (Go)
- HTTPS server on port 8443.
- Serves the React SPA and a REST API.
- Talks to systemd, LND gRPC, Bitcoin RPC and ZMQ, and Postgres.
- Manages optional native/Docker apps and report jobs.

2) UI (React + Tailwind)
- Single page app served by the manager.
- Uses JSON endpoints under /api.

3) LND
- Native binary with systemd unit.
- Data in /data/lnd, config in /data/lnd/lnd.conf.
- gRPC on 127.0.0.1:10009.

4) Postgres
- LND backend DB.
- LightningOS tables for notifications and reports.

5) Reports service
- Runs via systemd timer at 00:00 local time.
- Computes D-1 metrics from LND data.
- Writes to reports_daily (UPSERT).
- Live reports are computed on demand with a short TTL cache.

6) App Store (native and Docker-based apps)
- Optional apps managed by the manager with docker compose.
- App files in /var/lib/lightningos/apps.
- App data in /var/lib/lightningos/apps-data.

7) Terminal (GoTTY)
- Optional web terminal proxy on its own port.

## Data flow
- UI -> Manager API -> LND gRPC
- UI -> Manager API -> Bitcoin RPC and ZMQ checks
- Manager -> Postgres for notifications and reports
- Manager -> systemd for service restarts
- Manager -> docker compose for app lifecycle

## Storage layout
- /etc/lightningos/config.yaml (manager config)
- /etc/lightningos/secrets.env (secrets and DSNs)
- /data/lnd (LND data)
- /opt/lightningos/manager (binary)
- /opt/lightningos/ui (SPA build)
- /var/lib/lightningos/apps (app files)
- /var/lib/lightningos/apps-data (app data)

## Network defaults
- UI/API: https://127.0.0.1:8443
- LND gRPC: 127.0.0.1:10009
- Bitcoin remote RPC and ZMQ configured in config.yaml
- App ports are defined per app (for example LNDg on 8889)
- Terminal port default: 7681

## Caching and timeouts
- LND status cached with short TTL to reduce load.
- Reports live endpoint caches metrics for about 60 seconds.
- API calls use context timeouts to avoid blocking.

## Database tables
- notifications_* (notifications history and config)
- reports_daily (per day metrics, msat precision)
- audit_events (session-scoped audit trail for sensitive wallet operations)

## Scheduler
- lightningos-reports.timer triggers lightningos-reports.service daily.
