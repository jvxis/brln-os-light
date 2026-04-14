# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository layout

The repo root holds docs and the installer bootstrap (`lo_bootstrap.sh`). All Go/TS source lives under `lightningos-light/`, so `cd lightningos-light` before running build/test commands.

- `lightningos-light/cmd/lightningos-manager/` — Go CLI entrypoint (multi-mode: server, `auth`, `reports-run`, `reports-backfill`).
- `lightningos-light/internal/server/` — HTTP handlers, route registration, and most feature services. This is where nearly all backend work happens.
- `lightningos-light/internal/lndclient/` — LND gRPC wrappers (keysend, rebalance, graph, on-chain preview, open/close previews, etc.).
- `lightningos-light/internal/reports/` — daily metrics service, scanners, and Postgres store (msat precision).
- `lightningos-light/internal/config/` — YAML config loader.
- `lightningos-light/internal/system/` — host/system/SMART helpers.
- `lightningos-light/lnrpc/` — vendored generated LND protobuf/gRPC stubs.
- `lightningos-light/ui/` — Vite + React 18 + Tailwind SPA (TypeScript). Pages in `ui/src/pages`, API wrapper in `ui/src/api.ts`.
- `lightningos-light/templates/` — systemd unit templates, `lnd.conf`, `secrets.env`, `lightningos.config.yaml` consumed by installers.
- `lightningos-light/install*.sh` — large idempotent bash installers for fresh install and existing Pi migration.
- `docs/` — extensive product/architecture/feature-plan docs, numbered `00_*` to `22_*`. When touching a feature area, skim the matching numbered plan first (e.g. `17_CLOSE_RECOVERY_MANAGER_PLAN.md`, `21_AUTOFEE_NATIVE_SEED_PLAN.md`).

## Commands

Run from `lightningos-light/` unless noted.

**Backend:**
- Build manager: `go build -o dist/lightningos-manager ./cmd/lightningos-manager`
- Run manager: `./dist/lightningos-manager --config ./configs/config.yaml` (binds `0.0.0.0:8443`, needs `configs/tls/server.{crt,key}`)
- Generate dev TLS cert: `openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes -subj "/CN=localhost" -keyout configs/tls/server.key -out configs/tls/server.crt`
- All Go tests: `go test ./...`
- Package tests: `go test ./internal/server/...` or `go test ./internal/reports/...`
- Single test: `go test ./internal/server -run TestValidateAppRegistry`
- Reports daily run: `lightningos-manager reports-run --date YYYY-MM-DD`
- Reports backfill: `lightningos-manager reports-backfill --from YYYY-MM-DD --to YYYY-MM-DD`
- Auth CLI: `lightningos-manager auth status | setup-token new | recovery new`

**UI** (from `lightningos-light/ui/`):
- Install: `npm install`
- Dev server: `npm run dev`
- Production build: `npm run build` (runs `tsc -b && vite build`)
- Sidebar version label is read from `ui/public/version.txt`.

**In-place rebuild on a deployed host** (when iterating on a real node, not a clean checkout):
```
sudo /usr/local/go/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
sudo systemctl restart lightningos-manager
# UI:
cd ui && sudo npm install && sudo npm run build && cd ..
sudo rm -rf /opt/lightningos/ui/* && sudo cp -a ui/dist/. /opt/lightningos/ui/
```

## Architecture

**One Go binary, many responsibilities.** `lightningos-manager` is both the HTTPS server (port 8443, serves the SPA and REST API under `/api`) and a CLI for auth/reports subcommands. `main.go` dispatches on `os.Args[1]` to `runAuth`, `runReports`, `runReportsBackfill`, or `runServer`.

**Server composition.** `internal/server.Server` (see `server.go`) is a large struct that owns every feature subsystem: LND client, Postgres pool, notifier, chat, Amboss health, chan-status healer, reports, auth, rebalance, HTLC manager, failed-payments cleaner, tor peer checker, depix, autofee, shortcuts, channel ranking, graph explorer, balanced-open, close manager, node retirement, etc. Each subsystem typically has four files in `internal/server/`: `<feature>_service.go` (core logic), `<feature>_init.go` (lazy initialization + background goroutines), `<feature>_handlers.go` (HTTP handlers), plus `<feature>_service_test.go`. Routes are all registered centrally in `routes.go`; auth middleware (when enabled) wraps every route except the auth endpoints.

**Data dependencies.**
- LND gRPC on `127.0.0.1:10009` via `internal/lndclient`.
- Postgres stores notifications + `reports_daily` (msat precision, UPSERT on day). DSN resolved via `ResolveNotificationsDSN` from `/etc/lightningos/secrets.env`.
- Bitcoin RPC/ZMQ, systemd, and docker compose are called out-of-process.
- Reports `Service` runs daily via a systemd timer (`lightningos-reports.timer`) hitting `reports-run`; the server also exposes a "live" reports endpoint with ~60 s TTL cache.

**App Store.** Optional apps live in `internal/server/apps_*.go` and are registered in `apps_registry.go`. App files go under `/var/lib/lightningos/apps`, data under `/var/lib/lightningos/apps-data`. Validate with `go test ./internal/server -run TestValidateAppRegistry`. Adding an app: new `apps_<name>.go` handler, register in `apps_registry.go`, add icon under `ui/src/assets/apps`, update the AppStore page. Spec: `docs/10_APP_STORE_SPEC.md`.

**Adding a new API endpoint.** Handler in `internal/server/handlers.go` (or a feature-specific `*_handlers.go`), register in `routes.go`, extend `ui/src/api.ts`, update `docs/03_API_SPEC.md`.

**Runtime config locations** (on deployed hosts):
- `/etc/lightningos/config.yaml` — manager config (loaded by `config.Load`)
- `/etc/lightningos/secrets.env` — DSNs and secrets
- `/data/lnd/lnd.conf` — LND config, editable from the UI
- `/opt/lightningos/{manager,ui}` — installed binary and SPA bundle

**Timezones and reports.** `REPORTS_TIMEZONE` env var overrides the location used for daily rollups; `REPORTS_RUN_TIMEOUT_SEC` overrides the per-run context timeout (default 2 min). Backfill range is capped by `reports.CustomRangeDaysLimit()`.

## Conventions and constraints

- Keep LND gRPC on localhost; do not expose it.
- Do not persist wallet seeds.
- Installer scripts and the reports job must stay idempotent — they are re-run on upgrades.
- LAN or VPN access only by default; there is no multi-user auth model.
- Go module path is `lightningos-light` (declared in `go.mod`); internal imports use `lightningos-light/internal/...`.
- Go 1.23 / toolchain 1.24.12, Node 20+.
