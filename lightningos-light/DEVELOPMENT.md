# Development (local)

## Prerequisites
- Go 1.24+
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
sudo /usr/local/go/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
```

Rebuild and reinstall the privileged broker before restarting a manually
rebuilt manager:
```bash
sudo /usr/local/go/bin/go build -o dist/lightningos-privileged ./cmd/lightningos-privileged
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

These commands assume that the LightningOS system users and groups already
exist; they are not a replacement for an initial installer or full upgrade.

Rebuild UI:
```bash
cd ui && sudo npm install && sudo npm run build
cd ..
sudo rm -rf /opt/lightningos/ui/*
sudo cp -a ui/dist/. /opt/lightningos/ui/
```
