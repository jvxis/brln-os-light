# AGENTS.md

Guidance for coding agents working in this repository.

## Project Shape

LightningOS Light is a Bitcoin mainnet-only Lightning node management platform. It installs and manages native LND with a Go HTTPS manager, React SPA, automation services, reports, notifications, and an optional Docker-based App Store.

The active application code lives under `lightningos-light/`. The repository root also contains product docs, install docs, analysis notes, and planning documents.

Canonical references:
- `lightningos-light/internal/server/routes.go`: complete backend route registration.
- `lightningos-light/ui/src/api.ts`: frontend fetch layer and useful request shape hints.
- `docs/03_API_SPEC.md`: API spec, but verify against `routes.go` because the code has more routes.
- `CLAUDE.md`, `lightningos-light/DEVELOPMENT.md`, `docs/00_PROJECT_OVERVIEW.md`, and `docs/02_TECH_ARCHITECTURE.md`: project and dev context.

## Commands

Run backend commands from `lightningos-light/`.

```bash
# Backend build
go build -o bin/lightningos-manager ./cmd/lightningos-manager

# Backend tests
go test ./...
go test ./internal/server/...

# Validate App Store registry
go test ./internal/server -run TestValidateAppRegistry

# Run locally after UI build and TLS setup
./bin/lightningos-manager --config ./configs/config.yaml
```

Run frontend commands from `lightningos-light/ui/`.

```bash
npm install
npm run build
npm run dev
```

Local TLS cert setup from `lightningos-light/`:

```bash
mkdir -p configs/tls
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -subj "/CN=localhost" \
  -keyout configs/tls/server.key \
  -out configs/tls/server.crt
```

## Architecture Notes

- Go entry point: `lightningos-light/cmd/lightningos-manager/main.go`.
- Backend packages:
  - `internal/config`: `config.yaml` and `secrets.env` loading.
  - `internal/lndclient`: LND gRPC wrapper.
  - `internal/server`: HTTP API, business logic, automations, App Store.
  - `internal/reports`: daily and live report generation.
  - `lnrpc/`: generated LND protobuf/gRPC stubs; do not edit manually.
- Frontend:
  - `ui/src/App.tsx`: top-level routing/layout.
  - `ui/src/api.ts`: all backend fetch calls.
  - `ui/src/pages/`: feature pages.
  - `ui/src/components/`: shared components.
- Most major server features follow:
  - `<feature>_service.go`: business logic/state.
  - `<feature>_handlers.go`: HTTP handlers.
  - `<feature>_init.go`: lazy initialization.

## API Change Checklist

When adding or changing an API endpoint:

1. Add or update the handler in the relevant `*_handlers.go` file, or `handlers.go` for legacy/general handlers.
2. Register the route in `lightningos-light/internal/server/routes.go`.
3. Add or update the typed frontend fetch helper in `lightningos-light/ui/src/api.ts`.
4. Update `docs/03_API_SPEC.md` when the public contract changes.
5. Add focused tests when behavior, validation, permissions, persistence, or service orchestration changes.

## Security Rules

- Never persist, log, or return wallet seed words.
- Never expose `/etc/lightningos/secrets.env`, RPC credentials, macaroons, private keys, wallet passwords, Telegram tokens, or setup/recovery tokens in API responses or logs.
- LND gRPC is expected to stay localhost-only.
- Login-protected installs use secure HTTP-only cookies and CSRF tokens for mutating API calls.
- Public unauthenticated API paths are limited in `internal/server/auth.go`; verify there before relaxing auth.
- `POST /api/wallet/send` requires fresh reauthentication for external on-chain sends.
- `GET /api/onchain/utxos?limit=N` intentionally enriches the full wallet UTXO set before slicing; `limit` is only a response-size cap.

## App Store Notes

To add an App Store app:

1. Add `internal/server/apps_<id>.go` implementing the app handler interface.
2. Register it in `internal/server/apps_registry.go`.
3. Add UI icon/handling in `ui/src/pages/AppStore.tsx`.
4. Run `go test ./internal/server -run TestValidateAppRegistry`.

Docker app helpers live in `apps_docker.go`. Native service helpers use `systemd_run.go`.

## API Endpoint Map

Base URL in local/default docs: `https://127.0.0.1:8443`.

This endpoint inventory was verified from `lightningos-light/internal/server/routes.go` on 2026-06-24. It lists all registered `/api` routes at endpoint level. Use handlers and `ui/src/api.ts` for exact query parameters and request/response payload shapes.

### Auth

```text
GET    /api/auth/state
POST   /api/auth/setup
POST   /api/auth/login
POST   /api/auth/logout
POST   /api/auth/recovery
POST   /api/auth/enable-login
POST   /api/auth/change-password
POST   /api/auth/reauth
```

### Health, System, Postgres, Actions, Logs

```text
GET    /api/health
GET    /api/system-check
GET    /api/system
GET    /api/disk
GET    /api/postgres
GET    /api/postgres/maintenance
POST   /api/postgres/maintenance/cleanup
POST   /api/postgres/maintenance/vacuum
POST   /api/postgres/maintenance/compact-graph-history
POST   /api/actions/restart
POST   /api/actions/system
GET    /api/audit/events
GET    /api/logs
```

### Bitcoin, Elements, Mempool, LND, Upgrades

```text
GET    /api/bitcoin
GET    /api/bitcoin/active
GET    /api/bitcoin/bip110
GET    /api/bitcoin/source
POST   /api/bitcoin/source
GET    /api/bitcoin/market
GET    /api/mempool/fees
GET    /api/bitcoin-local/status
GET    /api/bitcoin-local/config
POST   /api/bitcoin-local/config
GET    /api/elements/status
GET    /api/elements/mainchain
POST   /api/elements/mainchain
GET    /api/lnd/status
GET    /api/lnd/config
POST   /api/lnd/config
POST   /api/lnd/config/raw
GET    /api/lnd/upgrade/status
POST   /api/lnd/upgrade
GET    /api/app/upgrade/status
POST   /api/app/upgrade/start
```

### Wizard

```text
GET    /api/wizard/status
POST   /api/wizard/bitcoin-remote
POST   /api/wizard/lnd/create-wallet
POST   /api/wizard/lnd/init-wallet
POST   /api/wizard/lnd/unlock
```

### Wallet

```text
GET    /api/wallet/summary
GET    /api/wallet/activity
GET    /api/wallet/payments/{paymentHash}
POST   /api/wallet/address
POST   /api/wallet/invoice
POST   /api/wallet/decode
POST   /api/wallet/pay/preview
POST   /api/wallet/pay/validated-route
POST   /api/wallet/pay/mpp
POST   /api/wallet/pay
POST   /api/wallet/send/preview
POST   /api/wallet/send
```

### On-chain and Provenance

```text
GET    /api/onchain/utxos
GET    /api/onchain/transactions
POST   /api/onchain/utxos/metadata
POST   /api/onchain/utxos/lock
POST   /api/onchain/utxos/unlock
POST   /api/onchain/utxos/bump
GET    /api/onchain/utxos/groups
POST   /api/onchain/utxos/groups
POST   /api/onchain/utxos/groups/{id}/assign
DELETE /api/onchain/utxos/groups/{id}
GET    /api/onchain/provenance
GET    /api/onchain/provenance/status
GET    /api/onchain/provenance/health
GET    /api/onchain/provenance/metrics
POST   /api/onchain/provenance/rebuild
```

### Lightning Ops: Core

```text
GET    /api/lnops/channels
GET    /api/lnops/channel-db-impact
GET    /api/lnops/peers
GET    /api/lnops/closed-channels
GET    /api/lnops/watchtower
POST   /api/lnops/watchtower/add
POST   /api/lnops/watchtower/remove
POST   /api/lnops/sign-message
POST   /api/lnops/peer
POST   /api/lnops/peer/disconnect
POST   /api/lnops/peers/boost
GET    /api/lnops/channel/peer-recommendations
GET    /api/lnops/channel/fees
POST   /api/lnops/channel/open-preview
POST   /api/lnops/channel/open
POST   /api/lnops/channel/open-batch-preview
POST   /api/lnops/channel/open-batch
POST   /api/lnops/channel/pending-open/bump-fee
POST   /api/lnops/channel/close
POST   /api/lnops/channel/fees
POST   /api/lnops/channel/status
POST   /api/lnops/channel/scb/restore
```

### Lightning Ops: Network Map and Graph Explorer

```text
GET    /api/lnops/network-map
GET    /api/lnops/network-map/config
POST   /api/lnops/network-map/config
GET    /api/lnops/graph-explorer/status
GET    /api/lnops/graph-explorer/search
GET    /api/lnops/graph-explorer/nodes/{pubkey}/general
GET    /api/lnops/graph-explorer/nodes/{pubkey}/channels
GET    /api/lnops/graph-explorer/nodes/{pubkey}/closed
GET    /api/lnops/graph-explorer/nodes/{pubkey}/fees
GET    /api/lnops/graph-explorer/storage
POST   /api/lnops/graph-explorer/storage
POST   /api/lnops/graph-explorer/storage/cleanup
POST   /api/lnops/graph-explorer/recompute
```

### Lightning Ops: Autofee and Ranking

```text
GET    /api/lnops/autofee/config
POST   /api/lnops/autofee/config
GET    /api/lnops/autofee/channels
POST   /api/lnops/autofee/channels
POST   /api/lnops/autofee/run
POST   /api/lnops/autofee/refresh
GET    /api/lnops/autofee/status
GET    /api/lnops/autofee/results
GET    /api/lnops/autofee/outcomes
POST   /api/lnops/autofee/outcomes/measure
GET    /api/lnops/channel-ranking
POST   /api/lnops/channel-ranking/recompute
GET    /api/lnops/channel-ranking/open-candidates
POST   /api/lnops/channel-ranking/open-candidates/recompute
GET    /api/lnops/channel-ranking/{channel_point}
```

### Lightning Ops: Close Manager

```text
GET    /api/lnops/close-manager/status
GET    /api/lnops/close-manager/sessions
GET    /api/lnops/close-manager/sessions/{id}
GET    /api/lnops/close-manager/sessions/{id}/events
POST   /api/lnops/close-manager/sessions/{id}/recover
POST   /api/lnops/close-manager/sessions/{id}/force-close
POST   /api/lnops/close-manager/sessions/{id}/bump-fee
```

### Lightning Ops: Automation Health

```text
GET    /api/lnops/channel/auto-heal
POST   /api/lnops/channel/auto-heal
GET    /api/lnops/channel/htlc-manager
POST   /api/lnops/channel/htlc-manager
GET    /api/lnops/channel/htlc-manager/logs
GET    /api/lnops/channel/htlc-manager/failed
GET    /api/lnops/payments/clean-failed
POST   /api/lnops/payments/clean-failed
GET    /api/lnops/channel/tor-peers
POST   /api/lnops/channel/tor-peers
GET    /api/lnops/channel/tor-peers/logs
```

### Lightning Ops: Balanced Open

```text
GET    /api/lnops/balanced-open/status
GET    /api/lnops/balanced-open/sessions
POST   /api/lnops/balanced-open/sessions
GET    /api/lnops/balanced-open/sessions/{id}
GET    /api/lnops/balanced-open/sessions/{id}/events
POST   /api/lnops/balanced-open/sessions/{id}/propose
POST   /api/lnops/balanced-open/sessions/{id}/accept
POST   /api/lnops/balanced-open/sessions/{id}/execute
POST   /api/lnops/balanced-open/sessions/{id}/retry-broadcast
POST   /api/lnops/balanced-open/sessions/{id}/recover
POST   /api/lnops/balanced-open/sessions/{id}/cancel
```

### Lightning Ops: Node Retirement and Succession

```text
GET    /api/lnops/node-retirement/status
GET    /api/lnops/node-retirement/sessions
POST   /api/lnops/node-retirement/sessions
GET    /api/lnops/node-retirement/sessions/{id}
GET    /api/lnops/node-retirement/sessions/{id}/events
GET    /api/lnops/node-retirement/sessions/{id}/channels
POST   /api/lnops/node-retirement/sessions/{id}/confirm-coop
POST   /api/lnops/node-retirement/sessions/{id}/decision
GET    /api/lnops/node-retirement/sessions/{id}/transfer
GET    /api/lnops/succession/status
GET    /api/lnops/succession/config
POST   /api/lnops/succession/config
POST   /api/lnops/succession/alive
POST   /api/lnops/succession/simulate
```

### Rebalance

```text
GET    /api/rebalance/config
POST   /api/rebalance/config
GET    /api/rebalance/config/snapshots
POST   /api/rebalance/config/snapshots
POST   /api/rebalance/config/snapshots/{id}/restore
DELETE /api/rebalance/config/snapshots/{id}
POST   /api/rebalance/profile
GET    /api/rebalance/overview
GET    /api/rebalance/channels
GET    /api/rebalance/pair-stats
GET    /api/rebalance/queue
GET    /api/rebalance/history
GET    /api/rebalance/sovereign-history
POST   /api/rebalance/run
POST   /api/rebalance/stop
POST   /api/rebalance/mission-control/reset
POST   /api/rebalance/channel/target
POST   /api/rebalance/channel/auto
POST   /api/rebalance/channel/manual-restart
POST   /api/rebalance/channel/exclude
GET    /api/rebalance/metrics/baseline
GET    /api/rebalance/stream
```

### Reports

```text
GET    /api/reports/range
GET    /api/reports/custom
GET    /api/reports/summary
GET    /api/reports/summary/custom
GET    /api/reports/live
GET    /api/reports/movement/live
GET    /api/reports/config
POST   /api/reports/config
```

### Apps

```text
GET    /api/apps
GET    /api/apps/storage-targets
GET    /api/apps/peerswap/elements-source
POST   /api/apps/peerswap/elements-source/test
POST   /api/apps/peerswap/elements-source
POST   /api/apps/{id}/install
POST   /api/apps/{id}/uninstall
POST   /api/apps/{id}/start
POST   /api/apps/{id}/stop
POST   /api/apps/{id}/reset-admin
GET    /api/apps/{id}/admin-password
GET    /api/apps/electrs/status
```

### Notifications

```text
GET    /api/notifications
GET    /api/notifications/stream
GET    /api/notifications/telegram
POST   /api/notifications/telegram
GET    /api/notifications/backup/telegram
POST   /api/notifications/backup/telegram
POST   /api/notifications/backup/telegram/test
```

### Chat

```text
GET    /api/chat/inbox
GET    /api/chat/messages
POST   /api/chat/send
```

### Amboss, DePix, Boleto, Shortcuts, Terminal Status

```text
GET    /api/amboss/health
POST   /api/amboss/health
GET    /api/depix/config
POST   /api/depix/orders
GET    /api/depix/orders
GET    /api/depix/orders/{id}
GET    /api/boleto/config
POST   /api/boleto/activate
GET    /api/boleto/activate/status/{paymentHash}
POST   /api/boleto/quote
GET    /api/boleto/status/{paymentHash}
GET    /api/shortcuts
POST   /api/shortcuts
DELETE /api/shortcuts/{id}
GET    /api/terminal/status
```

### Non-API Routes

```text
ANY    /terminal
ANY    /terminal/ws
ANY    /terminal/*
GET    /*
```

`GET /*` serves the React SPA/static files after API and terminal routes.
