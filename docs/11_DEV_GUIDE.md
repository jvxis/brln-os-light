# Developer Guide

## Stack
- Backend: Go
- Frontend: React + Tailwind
- API: JSON over HTTPS
- Integrations: systemd, LND gRPC, Postgres, Bitcoin RPC, docker compose

## Build and run (local)
- Build manager:
  go build -o dist/lightningos-manager ./cmd/lightningos-manager
- Build UI:
  cd ui && npm install && npm run build
- Install artifacts (typical):
  sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
  sudo cp -a ui/dist/. /opt/lightningos/ui/
  sudo systemctl restart lightningos-manager

## Tests
- Go tests:
  go test ./internal/reports/...
  go test ./internal/server/...

## Reports CLI
- Daily run for a specific date:
  lightningos-manager reports-run --date YYYY-MM-DD
  lightningos-manager reports-reconcile
- Backfill:
  lightningos-manager reports-backfill --from YYYY-MM-DD --to YYYY-MM-DD

## Config conventions
- /etc/lightningos/config.yaml for runtime config
- /etc/lightningos/secrets.env for secrets and DSNs
- /data/lnd/lnd.conf is edited by the UI

## Adding a new API endpoint
- Add handler in internal/server/handlers.go
- Register route in internal/server/routes.go
- Update ui/src/api.ts
- Update docs/03_API_SPEC.md

## Adding an App Store app
- Follow docs/10_APP_STORE_SPEC.md
- Add handler file, register in apps_registry.go
- Add icon to ui/src/assets/apps
- Update AppStore page for icon and routing

## Notes
- Do not persist wallet seeds.
- Keep LND gRPC on localhost.
- Ensure installer and reports jobs stay idempotent.
