# LightningOS Light

LightningOS Light is a Full Lightning Node Daemon Installer, Lightning node manager with a guided wizard, dashboard, and wallet. The manager serves the UI and API over HTTPS on `0.0.0.0:8443` by default for LAN access (set `server.host: "127.0.0.1"` for local-only) and integrates with systemd, Postgres, smartctl, Tor/i2pd, and LND gRPC.
<img width="1494" height="1045" alt="image" src="https://github.com/user-attachments/assets/8fb801c0-4946-48d8-8c24-c36a53d193b3" />
<img width="1491" height="903" alt="image" src="https://github.com/user-attachments/assets/cfda34d5-bccc-4b18-9970-bad494ae77b3" />
<img width="1576" height="1337" alt="image" src="https://github.com/user-attachments/assets/019cfff2-f354-4c2b-a595-2a15bb228864" />
<img width="1280" height="660" alt="image" src="https://github.com/user-attachments/assets/84489b07-8397-4195-b0d4-7e332618666d" />


## Highlights
- Mainnet only (remote Bitcoin default)
- No Docker in the core stack
- LND managed via systemd, gRPC on localhost
- Seed phrase is never persisted or logged
- Wizard for Bitcoin RPC credentials and wallet setup
- Lightning Ops: peers, channels, and fee updates
- Keysend Chat: 1 sat per message + routing fees, unread indicators, 30-day retention
- Real-time notifications (on-chain, Lightning, channels, forwards, rebalances)
- Optional Telegram SCB backup on channel open/close
- App Store: LNDg, Peerswap (psweb), Elements, Bitcoin Core
- Bitcoin Local management (status + config) and logs viewer

## Repository layout
- `cmd/lightningos-manager`: Go backend (API + static UI)
- `ui`: React + Tailwind UI
- `templates`: systemd units and config templates
- `install.sh`: idempotent installer (wrapper in `scripts/install.sh`)
- `configs/config.yaml`: local dev config

## Install (Ubuntu Server)
The installer provisions everything needed on a clean Ubuntu box:
- Postgres, smartmontools, curl, jq, ca-certificates, openssl, build tools
- Tor (ControlPort enabled) + i2pd enabled by default
- Go 1.22.x and Node.js 20.x (if missing or too old)
- LND binaries (default `v0.20.0-beta`)
- LightningOS Manager binary (compiled locally)
- UI build (compiled locally)
- systemd services and config templates
- self-signed TLS cert

Usage:
```bash
git clone https://github.com/jvxis/brln-os-light
cd brln-os-light/lightningos-light
sudo ./install.sh
```

If you already cloned and are in `brln-os-light`, use:
```bash
cd lightningos-light
sudo ./install.sh
```

### Install via curl (bootstrap)
This pulls the repo (or runs `git pull` if it already exists), then runs `lightningos-light/install.sh`.
```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo ACCEPT_MIT_LICENSE=1 bash
```

Optional overrides:
```bash
# Use a different clone location
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_DIR=/opt/brln-os-light bash

# Pin a branch/tag
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_BRANCH=main bash

# Use a different repo URL
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo REPO_URL=https://github.com/jvxis/brln-os-light bash
```

UFW note (App Store/LNDg):
If LNDg fails to reach LND gRPC and UFW is enabled, Docker-to-host traffic can be blocked.
Run these checks and allow the bridge interface used by the LNDg network:
```bash
sudo docker exec -it lndg-lndg-1 getent hosts host.docker.internal
sudo docker exec -it lndg-lndg-1 bash -lc 'timeout 3 bash -lc "</dev/tcp/host.docker.internal/10009" && echo OK || echo FAIL'
sudo docker network inspect lndg_default --format '{{.Id}}'
# bridge name = br-<first 12 chars of the id>
sudo ufw allow in on br-<id> to any port 10009 proto tcp
```
If it still fails, try:
```bash
sudo iptables -I INPUT -i br-<id> -p tcp --dport 10009 -j ACCEPT
```

**Attention (existing nodes):** If you already have a Lightning node with LND/Bitcoin running, do not use `install.sh`.  
Follow the Existing Node Guide instead:
- PT-BR: `docs/13_EXISTING_NODE_GUIDE_PT_BR.md`
- EN: `docs/14_EXISTING_NODE_GUIDE_EN.md`

Access the UI from another machine on the same LAN:
`https://<SERVER_LAN_IP>:8443`

Notes:
- You can override LND URL with `LND_URL=...` or version with `LND_VERSION=...`.
- The installer will generate a Postgres role and update `LND_PG_DSN` in `/etc/lightningos/secrets.env`.
- The UI version label comes from `ui/public/version.txt`.
- PostgreSQL uses the PGDG repository by default. Set `POSTGRES_VERSION=18` (or another major) to override.
- Tor uses the Tor Project repository when available. If your Ubuntu codename is unsupported, it falls back to `jammy`.

## Installer permissions (what `install.sh` enforces)
- Users:
  - `lnd` (system user, owns `/data/lnd`)
  - `lightningos` (system user, runs manager service)
- Group memberships:
  - `lightningos` in `lnd` and `systemd-journal`
  - `lnd` in `debian-tor`
- Key paths:
  - `/etc/lightningos` and `/etc/lightningos/tls`: `root:lightningos`, `chmod 750`
  - `/etc/lightningos/secrets.env`: `root:lightningos`, `chmod 660`
  - `/data/lnd`: `lnd:lnd`, `chmod 750`
  - `/data/lnd/data/chain/bitcoin/mainnet`: `lnd:lnd`, `chmod 750`
  - `/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon`: `lnd:lnd`, `chmod 640`

## Configuration paths
- `/etc/lightningos/config.yaml`
- `/etc/lightningos/secrets.env` (chmod 660)
- `/data/lnd/lnd.conf`
- `/data/lnd` (LND data dir)

## Notifications & backups
LightningOS Light includes a real-time notifications system that tracks:
- On-chain transactions (received/sent)
- Lightning invoices (settled) and payments (sent)
- Channel events (open, close, pending)
- Forwards and rebalances

Notifications are stored in a dedicated Postgres DB (see `NOTIFICATIONS_PG_DSN` in `/etc/lightningos/secrets.env`).

## Chat (Keysend)
Keysend chat is available in the UI and targets only online peers.
- Every message sends 1 sat + routing fees.
- Messages are stored locally in `/var/lib/lightningos/chat/messages.jsonl` and retained for 30 days.
- Unread peers are highlighted until their chat is opened.

Optional Telegram SCB backup:
- When configured, every channel open/close triggers `ExportAllChannelBackups` and sends the SCB to Telegram.
- Configure in the UI: Notifications -> Telegram SCB backup.
- Bot token comes from @BotFather and chat id from @userinfobot.
- Direct chat only; leaving both fields empty disables Telegram backup.

Environment keys:
- `NOTIFICATIONS_TG_BOT_TOKEN`
- `NOTIFICATIONS_TG_CHAT_ID`

## Reports
Daily routing reports are computed at midnight local time and stored in Postgres (same DB/user as notifications).

Schedule:
- `lightningos-reports.timer` runs `lightningos-reports.service` at `00:00` local time.
- Manual run: `/opt/lightningos/manager/lightningos-manager reports-run --date YYYY-MM-DD` (defaults to yesterday).
- Backfill: `/opt/lightningos/manager/lightningos-manager reports-backfill --from YYYY-MM-DD --to YYYY-MM-DD` (default max 730 days; use `--max-days N` to override).
- Optional timezone pin: set `REPORTS_TIMEZONE=America/Sao_Paulo` in `/etc/lightningos/secrets.env` to force daily, backfill, and live reports to use the same IANA timezone.

Stored table: `reports_daily`
- `report_date` (DATE, local day)
- `forward_fee_revenue_sats`
- `forward_fee_revenue_msat`
- `rebalance_fee_cost_sats`
- `rebalance_fee_cost_msat`
- `net_routing_profit_sats`
- `net_routing_profit_msat`
- `forward_count`
- `rebalance_count`
- `routed_volume_sats`
- `routed_volume_msat`
- `onchain_balance_sats`
- `lightning_balance_sats`
- `total_balance_sats`
- `created_at`, `updated_at`

API endpoints:
- `GET /api/reports/range?range=d-1|month|3m|6m|12m|all` (month = last 30 days)
- `GET /api/reports/custom?from=YYYY-MM-DD&to=YYYY-MM-DD` (max 730 days)
- `GET /api/reports/summary?range=...`
- `GET /api/reports/live` (today 00:00 local → now, cached ~60s)

## Web terminal (optional)
LightningOS Light can expose a protected web terminal using GoTTY.

The installer auto-enables the terminal and generates a credential when it is missing.
You can review or override in `/etc/lightningos/secrets.env`:
- `TERMINAL_ENABLED=1`
- `TERMINAL_CREDENTIAL=user:pass`
- `TERMINAL_ALLOW_WRITE=0` (set `1` to allow input)
- `TERMINAL_PORT=7681` (optional)
- `TERMINAL_WS_ORIGIN=^https://.*:8443$` (optional, default allows all origins)

Start (or restart) the service:
```bash
sudo systemctl enable --now lightningos-terminal
```
The Terminal page shows the current password and a copy button.

## Security notes
- The seed phrase is never stored. It is displayed once in the wizard.
- RPC credentials are stored only in `/etc/lightningos/secrets.env` (root:lightningos, `chmod 660`).
- API/UI bind to `0.0.0.0` by default for LAN access. If you want localhost-only, set `server.host: "127.0.0.1"` in `/etc/lightningos/config.yaml`.

## Troubleshooting
If `https://<SERVER_LAN_IP>:8443` is not reachable:
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 200 --no-pager
ss -ltn | grep :8443
```

### App Store (LNDg, Peerswap, Elements, Bitcoin Core)
- LNDg runs in Docker and listens on `http://<SERVER_LAN_IP>:8889`.
- Peerswap installs `peerswapd` + `psweb` (UI on `http://<SERVER_LAN_IP>:1984`) and requires Elements.
- Elements runs as a native service (Liquid Elements node, RPC on `127.0.0.1:7041`).
- Bitcoin Core runs via Docker with data in `/data/bitcoin`.

LNDg notes:
- The LNDg logs page reads `/var/log/lndg-controller.log` inside the container. If it is empty, check `docker logs lndg-lndg-1`.
- If you see `Is a directory: /var/log/lndg-controller.log`, remove `/var/lib/lightningos/apps-data/lndg/data/lndg-controller.log` on the host and restart LNDg.
- If LND is using Postgres, LNDg may log `channel.db` missing. This is expected and harmless.

## App Store architecture
- Each app implements a handler in `internal/server/apps_<app>.go`.
- Apps are registered in `internal/server/apps_registry.go`.
- App files live under `/var/lib/lightningos/apps/<app>` and persistent data under `/var/lib/lightningos/apps-data/<app>`.
- Docker is installed on-demand by apps that need it (core install stays Docker-free).
- Registry sanity checks ensure unique app IDs and ports.

### Adding a new app
1) Create `internal/server/apps_<app>.go` and implement the `appHandler` interface.
2) Register the app in `internal/server/apps_registry.go`.
3) Add a card in `ui/src/pages/AppStore.tsx` and an icon in `ui/src/assets/apps/`.

### App Store checks
Run the registry sanity tests:
```bash
go test ./internal/server -run TestValidateAppRegistry
```

## Development
See `DEVELOPMENT.md` for local dev setup and build instructions.

## Systemd
Templates are in `templates/systemd/`.

## Rebuild only (manager/UI)
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
