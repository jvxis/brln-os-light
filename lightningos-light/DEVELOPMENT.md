# Development (local)

## Prerequisites
- Go 1.24+ to compile the 0.5.40 bridge; installers/upgrader prepare Go 1.26.8
- Node.js 20+

## Quick start
1) Build the UI
```bash
cd ui
npm install
npm run build
```

2) Generate a local TLS cert
```bash
mkdir -p configs/tls
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -subj "/CN=localhost" \
  -keyout configs/tls/server.key \
  -out configs/tls/server.crt
```

3) Run the manager
```bash
go build -o bin/lightningos-manager ./cmd/lightningos-manager
./bin/lightningos-manager --config ./configs/config.yaml
```

By default, the manager binds to `0.0.0.0:8443` so you can access it from another machine on the same LAN. Use your server's LAN IP, for example: `https://192.168.1.10:8443`.

## Reports: conservative historical net repair

The scheduled reconciliation and ordinary `reports-backfill` still collect from
LND. Do not use a full backfill solely to update an old net formula: LND may no
longer retain all of the underlying events.

To compare stored nets with current formulas using only persisted LOS data:

```bash
./bin/lightningos-manager reports-backfill --config /etc/lightningos/config.yaml \
  --from 2026-08-01 --to 2026-08-31 --derive-only --dry-run
```

Review and retain the comparison before applying. Removing `--dry-run` applies
the repair in one transaction, updating only the six net fields and `updated_at`.
Source metrics, balances and provenance are untouched; absent days are skipped.
Repeated application is idempotent. The existing range limit/`--max-days` and
`REPORTS_RUN_TIMEOUT_SEC` apply. Dry-run uses a read-only snapshot, performs no
schema setup, and never initializes an LND client.

This corrects arithmetic from available components, not missing historical
events. It does not reconstruct channel sales/keysend data never captured.
Report/mark writers serialize the short persistence phase (not LND collection);
daily row locks protect repairs from concurrent component updates.

Optional PostgreSQL integration tests require a LOCAL disposable database:

```bash
REPORTS_TEST_PG_DSN='postgres://postgres@127.0.0.1:55496/postgres?sslmode=disable' \
  go test ./internal/reports -run 'TestDerived|TestReportCalendar' -v
```

These tests create and remove uniquely named schemas. Never use an operational
node database. They cover historical reads, dry-run/apply, source preservation,
marks added/edited/moved/removed, rollback on failure, concurrent daily writes,
live cached classifications and calendar dates. Without this variable they skip
the database tests; formula/timezone unit tests still run.

## UI version label
The sidebar version label is read from `ui/public/version.txt`.

## App Store development
- App handlers live in `internal/server/apps_<app>.go` and are registered in `internal/server/apps_registry.go`.
- Validate app registry:
```bash
go test ./internal/server -run TestValidateAppRegistry
```

## Rebuild only (manager/broker/UI)
Use this only for development or recovery on an already installed node. A
`git pull`, checkout, or manager-only rebuild is not a complete LightningOS
upgrade. Prefer the UI or release upgrade procedure for normal upgrades.

Run the commands below from the `lightningos-light/` application directory.

Rebuild manager:
```bash
sudo env GOTOOLCHAIN=local /opt/lightningos/toolchains/go1.26.8.linux-amd64/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
```

Rebuild and reinstall the privileged broker before restarting a manually
rebuilt manager:
```bash
sudo env GOTOOLCHAIN=local /opt/lightningos/toolchains/go1.26.8.linux-amd64/bin/go build -o dist/lightningos-privileged ./cmd/lightningos-privileged
sudo install -d -o root -g root -m 0755 /usr/local/libexec /etc/tmpfiles.d
sudo install -d -o root -g root -m 0750 /var/log/lightningos-privileged /run/lock/lightningos
sudo install -o root -g root -m 0644 templates/lightningos-privileged.tmpfiles.conf /etc/tmpfiles.d/lightningos-privileged.conf
sudo systemd-tmpfiles --create /etc/tmpfiles.d/lightningos-privileged.conf
sudo install -o root -g root -m 0755 dist/lightningos-privileged /usr/local/libexec/lightningos-privileged
sudo install -o root -g root -m 0644 templates/systemd/lightningos-privileged.socket /etc/systemd/system/lightningos-privileged.socket
sudo install -o root -g root -m 0644 templates/systemd/lightningos-privileged@.service /etc/systemd/system/lightningos-privileged@.service
sudo systemctl daemon-reload
sudo systemctl enable --now lightningos-privileged.socket
sudo systemctl is-active lightningos-privileged.socket
sudo test -S /run/lightningos-privileged/broker.sock
sudo systemctl restart lightningos-manager
```

For arm64, use the corresponding `linux-arm64` toolchain directory. On an
older installation that has not prepared this toolchain, use its existing Go
for the bridge build or the normal UI upgrade; see
[the Go transition](../docs/GO_UPGRADE_0.5.40.md).

These commands assume that the LightningOS system users and groups already
exist; they are not a replacement for an initial installer or full upgrade.

Rebuild UI:
```bash
cd ui && sudo npm install && sudo npm run build
cd ..
sudo rm -rf /opt/lightningos/ui/*
sudo cp -a ui/dist/. /opt/lightningos/ui/
```
