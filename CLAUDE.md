# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LightningOS Light is a Lightning Network node installer and manager. It provides a Go backend (REST API + HTTPS server) that wraps LND via gRPC, a React frontend, and an idempotent installer for Ubuntu Server. Mainnet only, no Docker in core stack, LAN/VPN access only, seed phrases never persisted.

## Repository Structure

All application code lives under `lightningos-light/`. Commands should be run from there.

```
lightningos-light/
├── cmd/lightningos-manager/main.go   # Entry point (server, reports-run, reports-backfill)
├── internal/
│   ├── config/        # YAML config loading
│   ├── lndclient/     # LND gRPC client wrapper (channels, wallet, payments, peers, etc.)
│   ├── server/        # HTTP handlers, routes, and all background services
│   │   ├── routes.go          # Chi router, 170+ endpoints
│   │   ├── handlers.go        # Core REST handlers
│   │   ├── autofee_service.go # Automatic fee adjustment engine
│   │   ├── rebalance_service.go # Channel rebalancing engine
│   │   ├── notifications.go   # Event tracking + SSE streaming
│   │   ├── htlc_manager.go    # HTLC telemetry/signals
│   │   ├── apps_registry.go   # App store registry
│   │   └── apps_*.go          # Per-app handlers
│   ├── reports/       # Daily routing reports (metrics, rebalance detection, store)
│   └── system/        # OS-level utilities
├── lnrpc/             # Generated LND protobuf bindings (do not edit)
├── ui/                # React + Tailwind + Vite frontend
│   ├── src/pages/     # 22 page components (LightningOps, RebalanceCenter, Reports, etc.)
│   ├── src/api.ts     # API client
│   └── src/i18n/      # English + Portuguese translations
├── templates/systemd/ # Systemd service units
├── configs/           # Local dev config
├── install.sh         # Production installer
└── scripts/           # Helper scripts (upgrade-lnd, fix-lnd-perms, etc.)
```

## Build & Development Commands

All commands run from `lightningos-light/`:

```bash
# Build backend
go build -o bin/lightningos-manager ./cmd/lightningos-manager

# Build frontend
cd ui && npm install && npm run build && cd ..

# Run locally (requires TLS cert + config)
./bin/lightningos-manager --config ./configs/config.yaml

# Run all Go tests
go test ./...

# Run a single test
go test ./internal/server -run TestValidateAppRegistry

# Run tests for a specific package
go test ./internal/reports/...
```

### Local TLS cert (one-time setup)

```bash
mkdir -p configs/tls
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -subj "/CN=localhost" \
  -keyout configs/tls/server.key -out configs/tls/server.crt
```

### Production rebuild (without full installer)

```bash
# Backend
sudo /usr/local/go/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
sudo systemctl restart lightningos-manager

# Frontend
cd ui && sudo npm install && sudo npm run build && cd ..
sudo rm -rf /opt/lightningos/ui/*
sudo cp -a ui/dist/. /opt/lightningos/ui/
```

## Architecture

### Backend (Go)

- **HTTP Router**: Chi (`go-chi/chi/v5`) with middleware for recovery and logging
- **LND Integration**: Direct gRPC calls via `internal/lndclient` wrapping generated protobuf bindings in `lnrpc/`
- **Database**: PostgreSQL via `pgx/v5` — stores notifications, reports, autofee results, channel downtime
- **Config**: YAML file (`/etc/lightningos/config.yaml` in prod, `configs/config.yaml` for dev)
- **Secrets**: Environment file at `/etc/lightningos/secrets.env` (Postgres DSN, Telegram tokens, etc.)
- **SPA serving**: Catch-all route serves `index.html` for client-side routing

### Background Services (all in `internal/server/`)

- **Autofee** (`autofee_service.go`): Per-channel fee adjustment using HTLC signals, channel classification, and tag-based decision logging
- **Rebalance** (`rebalance_service.go`): ROI-based channel rebalancing with profit guardrails, daily budgets, and adaptive probing
- **Notifications** (`notifications.go`): Tracks on-chain/Lightning/channel events, persists to Postgres, SSE streaming
- **Telegram** (`telegram_notifications.go`): Bot for alerts, SCB backups, financial summaries
- **HTLC Manager** (`htlc_manager.go`): Hysteresis-based failure signal tracking, feeds into Autofee
- **Channel Auto-Heal** (`chan_status_healer.go`): Monitors and remediates failing peer connections
- **Tor Peer Checker** (`tor_peer_checker.go`): Validates Tor peer reliability

### Frontend (React)

- Vite build, TypeScript, Tailwind CSS
- i18next for English + Portuguese
- `src/api.ts` is the centralized API client
- Large page components: `LightningOps.tsx` (channels/autofee), `RebalanceCenter.tsx`, `Reports.tsx`

### App Store

App handlers follow the pattern `internal/server/apps_<name>.go` and register in `apps_registry.go`. Validate with:
```bash
go test ./internal/server -run TestValidateAppRegistry
```

## Key Conventions

- The `lnrpc/` directory contains generated protobuf code — never edit manually
- Config struct is in `internal/config/config.go`; features are toggled via `features:` in YAML
- API routes are all under `/api/` — see `internal/server/routes.go` for the full list
- The server runs HTTPS with TLS 1.2+ (self-signed cert generated by installer)
- UI version label comes from `ui/public/version.txt`
- Reports run via systemd timer (`lightningos-reports.timer`) calling `lightningos-manager reports-run`

## Dependencies

- **Go 1.23+** (toolchain 1.24.12)
- **Node.js 20+** for UI build
- **PostgreSQL** for persistence
- **LND** (gRPC on localhost:10009)
