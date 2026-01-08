# LightningOS Light

LightningOS Light is a Lightning node manager with a guided wizard, dashboard, and wallet. The manager serves the UI and API over HTTPS on `0.0.0.0:8443` by default for LAN access (set `server.host: "127.0.0.1"` for local-only) and integrates with systemd, Postgres, smartctl, and LND gRPC.

## Highlights
- Mainnet only (remote Bitcoin default)
- No Docker in the core stack
- LND managed via systemd, gRPC on localhost
- Seed phrase is never persisted or logged
- Wizard for Bitcoin RPC credentials and wallet setup

## Repository layout
- `cmd/lightningos-manager`: Go backend (API + static UI)
- `ui`: React + Tailwind UI
- `templates`: systemd units and config templates
- `install.sh`: idempotent installer (wrapper in `scripts/install.sh`)
- `configs/config.yaml`: local dev config

## Install (Ubuntu Server)
The installer provisions everything needed on a clean Ubuntu box:
- Postgres, smartmontools, curl, jq, ca-certificates, openssl, build tools
- Go 1.22.x and Node.js 18.x (if missing or too old)
- LND binaries (default `v0.20.0-beta`)
- LightningOS Manager binary (compiled locally)
- UI build (compiled locally)
- systemd services and config templates
- self-signed TLS cert

Usage:
```bash
git clone <REPO_URL>
cd brln-os-light/lightningos-light
sudo ./install.sh
```

If you already cloned and are in `brln-os-light`, use:
```bash
cd lightningos-light
sudo ./install.sh
```

Access the UI from another machine on the same LAN:
`https://<SERVER_LAN_IP>:8443`

Notes:
- You can override LND URL with `LND_URL=...` or version with `LND_VERSION=...`.
- The installer will generate a Postgres role and update `LND_PG_DSN` in `/etc/lightningos/secrets.env`.

## Troubleshooting
If `https://<SERVER_LAN_IP>:8443` is not reachable:
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 200 --no-pager
ss -ltn | grep :8443
```

## Development
See `DEVELOPMENT.md` for local dev setup and build instructions.

## Configuration paths
- `/etc/lightningos/config.yaml`
- `/etc/lightningos/secrets.env` (chmod 600)
- `/etc/lnd/lnd.conf` and `/etc/lnd/lnd.user.conf`

## Security notes
- The seed phrase is never stored. It is displayed once in the wizard.
- RPC credentials are stored only in `/etc/lightningos/secrets.env` (root:lightningos, `chmod 660`).
- API/UI bind to `0.0.0.0` by default for LAN access. If you want localhost-only, set `server.host: "127.0.0.1"` in `/etc/lightningos/config.yaml`.

## API
See `docs/03_API_SPEC.md` for endpoint definitions. The manager also provides:
- `POST /api/wallet/invoice`
- `POST /api/wallet/pay`
- `GET /api/wallet/summary`

## Systemd
Templates are in `templates/systemd/`.


