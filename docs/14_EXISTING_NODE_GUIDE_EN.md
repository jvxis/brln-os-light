# LightningOS - Existing Node Guide (EN)

## VERY IMPORTANT DISCLAIMER
**LightningOS WAS NOT DESIGNED FOR EXISTING NODE INSTALLATIONS.**  
It was built around nodes configured via the **BRLN BOLT** and **MINIBOLT** tutorials, but there are many particularities that cannot be fully mapped in a free-form setup.  
Therefore, **if you do not have at least intermediate Linux and command-line knowledge, we do not recommend this installation**.  
Manual adjustments and adaptations may be required and are not fully covered in this guide.

## Scope
- This guide is for users who already have Bitcoin Core and LND running.
- Do not use install.sh on an existing node.
- Prefer install_existing.sh (primary flow). The manual configuration below is fallback/legacy.
- LND gRPC local only (127.0.0.1:10009).
- Mainnet only.

## Assumptions
- Data lives in /data/lnd and /data/bitcoin.
- If your Bitcoin Core data lives elsewhere (e.g. /mnt/bitcoin-data), prefer exposing local credentials in the `[Bitcoind]` block of `/data/lnd/lnd.conf`. As a fallback, keep a readable `bitcoin.conf` in a detectable path: `/data/bitcoin/bitcoin.conf`, `/etc/bitcoin/bitcoin.conf`, `/var/lib/bitcoind/bitcoin.conf`, `/home/bitcoin/.bitcoin/bitcoin.conf`, `/home/admin/.bitcoin/bitcoin.conf`, or `/root/.bitcoin/bitcoin.conf`.
- If you already use /home/admin/.lnd and /home/admin/.bitcoin, the guided installer can create /data/lnd and /data/bitcoin pointing to those paths.
- admin user has symlinks /home/admin/.lnd -> /data/lnd and /home/admin/.bitcoin -> /data/bitcoin.
- Alternative: dedicated lnd and bitcoin users with data in /data, and admin in lnd and bitcoin groups.

## Clone repository
```bash
git clone https://github.com/jvxis/brln-os-light
cd brln-os-light/lightningos-light
```

## Guided install (optional)
If you already have LND and Bitcoin Core running, you can use the guided installer:
```bash
sudo ./install_existing.sh
```
It asks about Go/npm (required for build), Postgres, terminal, and basic setup.
If you opt into Postgres, the script creates the LightningOS roles/DB and fills secrets.env automatically.
If LND already uses Postgres, the script does not install a new Postgres server by default: it tries the existing local service and, when local admin access is unavailable, asks for `NOTIFICATIONS_PG_ADMIN_DSN` so it can create only the `lightningos` database on the existing server.
When it finds `db.postgres.dsn` in `lnd.conf`, the script also fills `LND_PG_DSN` if `secrets.env` still has the placeholder value.
The script also writes the systemd units with these users:
- lightningos-manager: uses the `lightningos` user/group.
- lightningos-reports: uses the same user/group (`lightningos`).
- lightningos-terminal: runs as `lightningos`.
For SupplementaryGroups, it only adds groups that exist on the host.
The script may rebuild and reinstall manager/UI from your local checkout (prompts default to `y`), so confirm branch/tag before running to avoid reverting to an older app version.

Quick checklist (post-install):
- Check manager status/logs:
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 50 --no-pager
```
- Get your IP and open the UI:
```bash
hostname -I | awk '{print $1}'
```
Open: `https://YOUR_SERVER_IP:8443`
- Confirm `lightningos` group membership:
```bash
id lightningos
```
- Validate sudoers:
```bash
sudo visudo -cf /etc/sudoers.d/lightningos
```
- If you use UFW, confirm port 8443 is allowed:
```bash
sudo ufw status
```
If you use remote Bitcoin/LND, some health checks may show "ERR" until remote access is configured.

Quick check (passwordless sudo for App Store):
```bash
sudo -u lightningos sudo -n docker compose version || sudo -u lightningos sudo -n docker-compose version
```
If you get `sudo: a password is required`, passwordless sudo for `lightningos` is not correctly applied.

UFW note for LNDg (App Store):
If LNDg cannot reach LND gRPC and UFW is enabled, Docker-to-host traffic may be blocked.
Follow these steps to allow the bridge used by the LNDg network:
```bash
sudo docker exec -it lndg-lndg-1 getent hosts host.docker.internal
sudo docker exec -it lndg-lndg-1 bash -lc 'timeout 3 bash -lc "</dev/tcp/host.docker.internal/10009" && echo OK || echo FAIL'
sudo docker network inspect lndg_default --format '{{.Id}}'
# bridge name is br-<first 12 chars of the id>
sudo ufw allow in on br-<id> to any port 10009 proto tcp
```
If it still fails:
```bash
sudo iptables -I INPUT -i br-<id> -p tcp --dport 10009 -j ACCEPT
```

If the script is not executable:
```bash
chmod +x install_existing.sh
# or:
sudo bash install_existing.sh
```


## Manual configuration (legacy fallback)
Use this section only if `install_existing.sh` cannot be executed on the host.

Recommended minimal flow:
1) fix `lightningos` sudoers
2) ensure Docker + Compose works without password prompt for the manager
3) build/install manager and UI
4) restart services and validate

### 1) Sudoers (required for App Store and upgrades)
```bash
sudo tee /etc/sudoers.d/lightningos >/dev/null <<'EOF'
Defaults:lightningos !requiretty
Cmnd_Alias LIGHTNINGOS_SYSTEM = /usr/bin/systemctl restart lnd, /usr/bin/systemctl restart lightningos-manager, /usr/bin/systemctl restart postgresql, /usr/bin/systemctl is-active lightningos-lnd-upgrade, /usr/bin/systemctl is-active lightningos-app-upgrade, /usr/bin/systemctl reboot, /usr/bin/systemctl poweroff, /usr/local/sbin/lightningos-fix-lnd-perms, /usr/local/sbin/lightningos-upgrade-lnd, /usr/local/sbin/lightningos-upgrade-app, /usr/sbin/smartctl *
Cmnd_Alias LIGHTNINGOS_APPS = /usr/bin/apt-get *, /usr/bin/apt *, /usr/bin/dpkg *, /usr/bin/docker *, /usr/bin/docker-compose *, /usr/bin/systemd-run *, /usr/sbin/ufw *
lightningos ALL=NOPASSWD: LIGHTNINGOS_SYSTEM, LIGHTNINGOS_APPS
EOF
sudo chmod 440 /etc/sudoers.d/lightningos
sudo visudo -cf /etc/sudoers.d/lightningos
```

Required test:
```bash
sudo -u lightningos sudo -n docker compose version || sudo -u lightningos sudo -n docker-compose version
```

### 2) Docker + Compose
```bash
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-plugin || sudo apt-get install -y docker.io docker-compose
sudo systemctl enable --now docker
sudo systemctl is-active docker
```

### 3) Build and install (manager/UI)
```bash
cd lightningos-light

go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager

cd ui
npm install
npm run build
cd ..

sudo rm -rf /opt/lightningos/ui/*
sudo cp -a ui/dist/. /opt/lightningos/ui/
```

### 4) Restart and validation
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lightningos-manager
sudo systemctl restart lightningos-manager

systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 100 --no-pager
curl -k https://127.0.0.1:8443/api/health
```

### 5) Path requirements
- `/data/lnd` is still the expected path for features like `lnd.conf` editing and auto-unlock.
- For local Bitcoin, ensure `bitcoind.rpcuser` and `bitcoind.rpcpass` exist in the `[Bitcoind]` block of `/data/lnd/lnd.conf`, or keep a readable `bitcoin.conf` in a detectable source, including `/home/admin/.bitcoin/bitcoin.conf`.

## Quick troubleshooting
- `sudo: a password is required` with `sudo -n`: `lightningos` sudoers is invalid or incomplete.
- `docker-compose failed ... sudo failed`: usually the same sudoers problem, or Docker is inactive.
- App version reverted after `install_existing.sh`: local checkout was on an older branch/tag and manager/UI were rebuilt with default `y` prompts.
- App shows Bitcoin as `remote`/`ERR` even with local bitcoind: ensure `/data/lnd/lnd.conf` has an active `[Bitcoind]` section with local RPC settings (non-commented lines), for example:
```ini
[Bitcoind]
bitcoind.rpchost=127.0.0.1:8332
bitcoind.rpcuser=YOUR_RPC_USER
bitcoind.rpcpass=YOUR_RPC_PASS
```
If this block is missing, auto-detection may fall back to `remote`. After updating, restart `lightningos-manager` and `lnd`.
