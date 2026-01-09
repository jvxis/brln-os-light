# Development (local)

## Prerequisites
- Go 1.22+
- Node.js 18+

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

## Rebuild only (server)
Use this when you only want to recompile without running the full installer.

Rebuild manager:
```bash
sudo /usr/local/go/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
sudo systemctl restart lightningos-manager
```

Rebuild UI:
```bash
cd ui && sudo npm install && sudo npm run build
cd ..
sudo rm -rf /opt/lightningos/ui/*
sudo cp -a ui/dist/. /opt/lightningos/ui/
```
