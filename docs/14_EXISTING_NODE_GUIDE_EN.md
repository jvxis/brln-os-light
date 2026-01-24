# LightningOS Light - Existing Node Guide (EN)

## Scope
- This guide is for users who already have Bitcoin Core and LND running.
- Do not use install.sh. The flow is manual after git pull and build.
- LND gRPC local only (127.0.0.1:10009).
- Mainnet only.

## Assumptions
- Data lives in /data/lnd and /data/bitcoin.
- If your Bitcoin Core data lives elsewhere (e.g. /mnt/bitcoin-data), create a bind mount or symlink to /data/bitcoin (LightningOS only reads /data/bitcoin/bitcoin.conf).
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

If the script is not executable:
```bash
chmod +x install_existing.sh
# or:
sudo bash install_existing.sh
```

## Important about /data/lnd
- LightningOS uses fixed paths for lnd.conf and wallet.db in /data/lnd.
- If LND is not in /data/lnd, the lnd.conf editor and auto-unlock will not work.
- Recommendation: use /data/lnd or create a symlink/bind to /data/lnd.

## Quick checks (read-only)
```bash
systemctl status lnd --no-pager
systemctl status bitcoind --no-pager

ls -la /data/lnd /data/bitcoin
ls -la /home/admin/.lnd /home/admin/.bitcoin

ls -l /data/lnd/tls.cert /data/lnd/data/chain/bitcoin/mainnet/admin.macaroon
ss -ltnp | rg ':10009' || ss -ltnp | grep -n ':10009'

rg -n "rpcuser|rpcpassword|zmqpubraw" /data/bitcoin/bitcoin.conf || \
  grep -nE "rpcuser|rpcpassword|zmqpubraw" /data/bitcoin/bitcoin.conf
```

## Build manager and UI
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

## Folders and permissions
```bash
sudo mkdir -p /etc/lightningos/tls /opt/lightningos/manager /opt/lightningos/ui \
  /var/lib/lightningos /var/log/lightningos

sudo chown -R root:admin /etc/lightningos
sudo chmod 750 /etc/lightningos /etc/lightningos/tls
sudo chown -R admin:admin /opt/lightningos /var/lib/lightningos /var/log/lightningos

sudo usermod -aG lnd,bitcoin admin
sudo ln -sfn /data/lnd /home/admin/.lnd
sudo ln -sfn /data/bitcoin /home/admin/.bitcoin
```

## Manager TLS
```bash
sudo openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
  -subj "/CN=$(hostname -f)" \
  -keyout /etc/lightningos/tls/server.key \
  -out /etc/lightningos/tls/server.crt

sudo chown root:admin /etc/lightningos/tls/server.key /etc/lightningos/tls/server.crt
sudo chmod 640 /etc/lightningos/tls/server.key /etc/lightningos/tls/server.crt
```

## LightningOS configuration
1) Copy the template:
```bash
sudo cp lightningos-light/templates/lightningos.config.yaml /etc/lightningos/config.yaml
sudo ${EDITOR:-nano} /etc/lightningos/config.yaml
```

2) Set these fields:
- server.host: "0.0.0.0" for LAN access, or "127.0.0.1" for local only.
- lnd.grpc_host: "127.0.0.1:10009"
- lnd.tls_cert_path: "/data/lnd/tls.cert"
- lnd.admin_macaroon_path: "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"
- bitcoin_remote.rpchost: "127.0.0.1:8332"
- bitcoin_remote.zmq_rawblock: "tcp://127.0.0.1:28332"
- bitcoin_remote.zmq_rawtx: "tcp://127.0.0.1:28333"
- postgres.db_name: "lnd" (only if LND uses Postgres; if LND uses Bolt/SQLite, this field is not used)

## Bitcoin RPC (local and remote)
- LightningOS does not read bitcoin.conf automatically.
- It uses credentials from /etc/lightningos/secrets.env (BITCOIN_RPC_USER/PASS).
- For local Bitcoin, use the same values from bitcoin.conf (rpcuser/rpcpassword).
- If you use rpcauth, you need the original username and password (the rpcauth hash alone is not enough) or create a dedicated rpcuser/rpcpassword entry.
- If you use the club remote Bitcoin, you can keep bitcoin.br-ln.com:8085 and the template ZMQ values.

Minimal bitcoin.conf example (local):
```
server=1
rpcuser=rpc_user
rpcpassword=rpc_pass
rpcallowip=127.0.0.1
zmqpubrawblock=tcp://127.0.0.1:28332
zmqpubrawtx=tcp://127.0.0.1:28333
```

Note: if you use a custom rpcport, LightningOS uses that port for local RPC when reading /data/bitcoin/bitcoin.conf.

Local RPC test (optional):
```bash
bitcoin-cli -conf=/data/bitcoin/bitcoin.conf getblockchaininfo
# or, if you do not have bitcoin-cli:
curl --user rpc_user:rpc_pass --data-binary '{"jsonrpc":"1.0","id":"curl","method":"getblockchaininfo","params":[]}' \
  -H 'content-type:text/plain;' http://127.0.0.1:8332/
```

## Secrets (credentials and DSNs)
1) Copy the template:
```bash
sudo cp lightningos-light/templates/secrets.env /etc/lightningos/secrets.env
sudo ${EDITOR:-nano} /etc/lightningos/secrets.env
sudo chown root:admin /etc/lightningos/secrets.env
sudo chmod 640 /etc/lightningos/secrets.env
```

2) Fill in:
- BITCOIN_RPC_USER and BITCOIN_RPC_PASS
- NOTIFICATIONS_PG_DSN and NOTIFICATIONS_PG_ADMIN_DSN
- LND_PG_DSN only if LND uses Postgres

Example DSNs:
```
NOTIFICATIONS_PG_DSN=postgres://losapp:PASSWORD@127.0.0.1:5432/lightningos?sslmode=disable
NOTIFICATIONS_PG_ADMIN_DSN=postgres://losadmin:PASSWORD@127.0.0.1:5432/postgres?sslmode=disable
LND_PG_DSN=postgres://lndpg:PASSWORD@127.0.0.1:5432/lnd?sslmode=disable
```

## Postgres - Track A (LND on Postgres)
1) Verify LND backend:
```bash
rg -n "db.backend|db.postgres.dsn" /data/lnd/lnd.conf || \
  grep -nE "db.backend|db.postgres.dsn" /data/lnd/lnd.conf
```

2) Ensure Postgres is active:
```bash
systemctl status postgresql --no-pager
```

3) Create roles and DB for notifications/reports (if missing):
```bash
sudo -u postgres psql -c "create role losadmin with login password 'ADMIN_PASS' createrole createdb;"
sudo -u postgres psql -c "create role losapp with login password 'APP_PASS';"
sudo -u postgres psql -c "create database lightningos owner losapp;"
```

4) Update secrets.env:
- NOTIFICATIONS_PG_DSN and NOTIFICATIONS_PG_ADMIN_DSN with your passwords
- LND_PG_DSN pointing to the LND DB

5) In config.yaml:
- postgres.db_name: "lnd"

## Postgres - Track B (LND on Bolt/SQLite)
1) Install Postgres (examples):
```bash
# Debian/Ubuntu
sudo apt-get update
sudo apt-get install -y postgresql postgresql-client

# RHEL/Fedora/CentOS
sudo dnf install -y postgresql-server postgresql
sudo postgresql-setup --initdb
sudo systemctl enable --now postgresql
```

2) Create roles and DB for notifications/reports:
```bash
sudo -u postgres psql -c "create role losadmin with login password 'ADMIN_PASS' createrole createdb;"
sudo -u postgres psql -c "create role losapp with login password 'APP_PASS';"
sudo -u postgres psql -c "create database lightningos owner losapp;"
```

3) Update secrets.env:
- NOTIFICATIONS_PG_DSN and NOTIFICATIONS_PG_ADMIN_DSN with your passwords
- Leave LND_PG_DSN empty or remove it

4) In config.yaml:
- postgres.db_name: "lnd" (keep the default; not used if LND does not use Postgres)

## Reports Update
### 1) Daily cost and profit synchronization
Command to calculate and store daily costs and profits, ensuring that financial reports display accurate and complete information.
```bash
/opt/lightningos/manager/lightningos-manager reports-backfill --from YYYY-MM-DD --to YYYY-MM-DD
```

### 2) Daily update scheduling
Configure a systemd timer to run a service daily that updates the database with the previous day's data.
```bash
sudo cp templates/systemd/lightningos-reports.timer \
  /etc/systemd/system/lightningos-reports.timer
```

```bash
sudo cp templates/systemd/lightningos-reports.service \
  /etc/systemd/system/lightningos-reports.service
```

### 3) Recommended adjustments
Edit the lightningos-reports.service file and adjust it according to your environment:
- User=admin
- Group=admin
```bash
sudo ${EDITOR:-nano} /etc/systemd/system/lightningos-reports.service
```

### 4) Enable and start
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lightningos-reports.timer
```

## Manager systemd unit
1) Copy the unit and edit User/Group:
```bash
sudo cp templates/systemd/lightningos-manager.service \
  /etc/systemd/system/lightningos-manager.service
sudo ${EDITOR:-nano} /etc/systemd/system/lightningos-manager.service
```

2) Recommended edits:
- User=admin
- Group=admin
- SupplementaryGroups=lnd bitcoin systemd-journal docker

3) Enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lightningos-manager
```

## Validation
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 200 --no-pager

curl -k https://127.0.0.1:8443/api/health
curl -k https://127.0.0.1:8443/api/postgres
curl -k https://127.0.0.1:8443/api/lnd/status
```

## Common issues
- TLS/macaroon: permission error or missing file.
- Postgres: service inactive or invalid DSN.
- Bitcoin RPC/ZMQ: wrong credentials or ports.
- LND gRPC: port not 10009 or non-local bind.
- If your Postgres service is not named "postgresql", create a systemd alias or expose that name on your distro.
