# Installing LightningOS Light on Raspberry Pi 5 — Debian 12 (Bookworm)

> **Community guide** — Adaptation of the official LightningOS Light installer for
> **Raspberry Pi 5 (8 GB RAM)** running **Debian 12 Bookworm (aarch64)** with an existing Bitcoin Core node.
>
> Original project: [https://github.com/jvxis/brln-os-light](https://github.com/jvxis/brln-os-light)
> This fork: [https://github.com/MarcosJRcwb/brln-os-light](https://github.com/MarcosJRcwb/brln-os-light)

---

[Leia em Português](INSTALL_RASPBERRYPI5_DEBIAN12_PT_BR.md)


---

## Why a separate guide?

The official `install.sh` and `install_existing_pi.sh` target **Ubuntu Server 22.04/24.04**.
Debian 12 differs in several important ways that cause silent failures if not addressed:

| Issue | Detail |
|---|---|
| Tor group name | Debian uses `debian-tor`, not `tor` |
| Go binary architecture | Installer may pull `amd64` — must force `arm64` |
| GoTTY binary architecture | Bundled binary is `amd64` — must replace |
| REST port conflict | Port 8080 may be in use — use 8082 |
| PostgreSQL install method | PGDG apt may not work cleanly — use Docker |
| journalctl in UI | Shows permission warning — cosmetic only, no impact |

---

## Prerequisites

| Item | Requirement |
|---|---|
| Hardware | Raspberry Pi 5, 8 GB RAM |
| OS | Debian 12 Bookworm, aarch64 |
| Storage | External SSD, minimum 1 TB (2 TB recommended) |
| Bitcoin Core | Already installed and **fully synced** |
| Access | SSH (PuTTY or any terminal) or local terminal |
| Internet | Stable connection throughout installation |

> ⚠️ **Do not start until Bitcoin Core is 100% synced.**
> You can verify with: `bitcoin-cli getblockchaininfo | grep verificationprogress`
> The value must be `0.9999...` or `1`.

---

## Installation overview

```
PHASE 1: Environment preparation
  1.1  Verify OS and architecture
  1.2  Confirm Bitcoin Core is running and synced
  1.3  Add ZMQ to bitcoin.conf
  1.4  Create /data symlinks
       ↓
PHASE 2: Dependencies
  2.1  Install Go 1.24 (linux-arm64 — mandatory)
  2.2  Install Node.js 20.x
  2.3  Deploy PostgreSQL via Docker
       ↓
PHASE 3: LightningOS installation
  3.1  Clone repository
  3.2  Create users and directories
  3.3  Compile manager (Go build)
  3.4  Compile UI (npm build)
  3.5  Generate TLS certificate
       ↓
PHASE 4: LND installation
  4.1  Download LND v0.20.0 (linux-arm64)
  4.2  Create systemd service
       ↓
PHASE 5: Debian-specific fixes
  5.1  Check port 8080 conflict
  5.2  Configure Tor with debian-tor group
  5.3  Replace GoTTY with arm64 build
  5.4  Set group permissions
  5.5  Configure secrets.env and config.yaml
  5.6  Create LightningOS systemd service
       ↓
PHASE 6: Validation
  6.1  Verify all services
  6.2  Access UI and run wizard
  6.3  Create wallet — write down seed phrase
```

> **One command at a time.** Verify the output before proceeding to the next step.

---

## PHASE 1 — Environment preparation

### 1.1 Verify the operating system

```bash
uname -a
```

Expected output (must contain `aarch64` and `Debian`):
```
Linux raspnode 6.12.62+rpt-rpi-2712 #1 SMP PREEMPT Debian aarch64 GNU/Linux
```

> ❌ If `aarch64` is not present: this guide does not apply to your hardware.

---

### 1.2 Verify Bitcoin Core

```bash
systemctl status bitcoin --no-pager | head -10
```

Expected: `Active: active (running)`

> ⏳ If syncing: wait for `verificationprogress` to reach `1` before continuing.

---

### 1.3 Add ZMQ to bitcoin.conf

LND requires ZMQ to receive real-time block and transaction notifications.

Check if already configured:
```bash
sudo grep zmq /etc/bitcoin/bitcoin.conf
```

If the lines below are already present, skip to step 1.4:
```
zmqpubrawblock=tcp://127.0.0.1:28332
zmqpubrawtx=tcp://127.0.0.1:28333
```

If not, add them:
```bash
sudo bash -c 'printf "\n# ZMQ for LND\nzmqpubrawblock=tcp://127.0.0.1:28332\nzmqpubrawtx=tcp://127.0.0.1:28333\n" >> /etc/bitcoin/bitcoin.conf'
```

Restart Bitcoin Core and verify:
```bash
sudo systemctl restart bitcoin
sleep 30 && systemctl status bitcoin --no-pager | grep Active
```

---

### 1.4 Create /data symlinks

LightningOS expects data under `/data/bitcoin` and `/data/lnd`.
Adapt the paths below to match your actual SSD mount point.

```bash
sudo mkdir -p /data
sudo ln -sf /mnt/ssd/bitcoin /data/bitcoin
sudo mkdir -p /mnt/ssd/lnd
sudo ln -sf /mnt/ssd/lnd /data/lnd
sudo ln -sf /etc/bitcoin/bitcoin.conf /mnt/ssd/bitcoin/bitcoin.conf
```

Verify:
```bash
ls -la /data/
```

Expected:
```
bitcoin -> /mnt/ssd/bitcoin
lnd -> /mnt/ssd/lnd
```

---

## PHASE 2 — Dependencies

### 2.1 Install Go 1.24 (linux-arm64 — CRITICAL)

> ⚠️ **Common failure point:** automatic scripts may download the `amd64` binary.
> Always install `linux-arm64` manually on Raspberry Pi.

```bash
cd /tmp && curl -LO https://go.dev/dl/go1.24.0.linux-arm64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.0.linux-arm64.tar.gz
```

Verify (must show `linux/arm64`):
```bash
/usr/local/go/bin/go version
```

Expected:
```
go version go1.24.0 linux/arm64
```

> ❌ If output shows `amd64`: delete and reinstall using the correct file above.

---

### 2.2 Install Node.js 20.x

```bash
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs
node --version
```

Expected: `v20.x.x`

---

### 2.3 Deploy PostgreSQL via Docker

Verify Docker is installed:
```bash
docker --version
```

If not installed:
```bash
sudo apt-get install -y docker.io docker-compose-plugin
sudo systemctl enable --now docker
```

Create data directory and Docker Compose file:

```bash
sudo mkdir -p /mnt/ssd/postgres/data /opt/lightningos-postgres
```

Create `/opt/lightningos-postgres/docker-compose.yml` with the content below.
**Replace all placeholder passwords with strong values of your choice and record them securely.**

```yaml
version: "3.8"
services:
  postgres:
    image: postgres:16
    container_name: lightningos-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: losadmin
      POSTGRES_PASSWORD: <STRONG_ADMIN_PASSWORD>
      POSTGRES_DB: postgres
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - /mnt/ssd/postgres/data:/var/lib/postgresql/data
```

Start PostgreSQL:
```bash
cd /opt/lightningos-postgres && sudo docker compose up -d
sleep 10 && sudo docker ps | grep postgres
```

Expected: container with status `Up`.

Create the required databases and users (replace passwords to match your compose file):
```bash
sudo docker exec -it lightningos-postgres psql -U losadmin -c "
CREATE USER lndpg WITH PASSWORD '<LND_DB_PASSWORD>';
CREATE DATABASE lnd OWNER lndpg;
CREATE USER losapp WITH PASSWORD '<APP_DB_PASSWORD>';
CREATE DATABASE lightningos OWNER losapp;
GRANT ALL PRIVILEGES ON DATABASE lnd TO lndpg;
GRANT ALL PRIVILEGES ON DATABASE lightningos TO losapp;
"
```

---

## PHASE 3 — LightningOS installation

### 3.1 Clone the repository

```bash
sudo git clone https://github.com/MarcosJRcwb/brln-os-light /opt/brln-os-light
ls /opt/brln-os-light/lightningos-light/
```

---

### 3.2 Create users and directories

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin lightningos 2>/dev/null || echo "already exists"
sudo useradd --system --no-create-home --shell /usr/sbin/nologin lnd 2>/dev/null || echo "already exists"
sudo mkdir -p /opt/lightningos/manager /opt/lightningos/ui
sudo mkdir -p /etc/lightningos/tls
sudo mkdir -p /var/lib/lightningos /var/log/lightningos
```

---

### 3.3 Compile the manager (Go backend)

> ⏳ This step can take **5 to 15 minutes**. No output is shown during compilation — this is normal.

```bash
cd /opt/brln-os-light/lightningos-light
sudo GOFLAGS="-mod=mod" /usr/local/go/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
```

Verify the binary was created (should be 20–40 MB):
```bash
ls -lh dist/lightningos-manager
```

Install:
```bash
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
```

> ❌ If you get module errors: run `sudo /usr/local/go/bin/go mod download` first.

---

### 3.4 Build the UI (frontend)

```bash
cd /opt/brln-os-light/lightningos-light/ui
sudo npm install
```

> ⏳ Wait for npm packages to install (~5 minutes).

```bash
sudo npm run build
sudo cp -a dist/. /opt/lightningos/ui/
```

Verify:
```bash
ls /opt/lightningos/ui/ | head -5
```

Expected: `index.html  assets/  ...`

---

### 3.5 Generate self-signed TLS certificate

```bash
sudo openssl req -x509 -newkey rsa:4096 \
  -keyout /etc/lightningos/tls/server.key \
  -out /etc/lightningos/tls/server.crt \
  -days 3650 -nodes \
  -subj "/CN=lightningos" \
  -addext "subjectAltName=IP:127.0.0.1,IP:0.0.0.0"
```

---

## PHASE 4 — LND installation

### 4.1 Download LND v0.20.0 (linux-arm64)

> ⚠️ Always use `linux-arm64`. The `amd64` binary returns `Exec format error` on Raspberry Pi.

```bash
cd /tmp
curl -LO https://github.com/lightningnetwork/lnd/releases/download/v0.20.0-beta/lnd-linux-arm64-v0.20.0-beta.tar.gz
tar -xzf lnd-linux-arm64-v0.20.0-beta.tar.gz
sudo install -m 0755 lnd-linux-arm64-v0.20.0-beta/lnd /usr/local/bin/lnd
sudo install -m 0755 lnd-linux-arm64-v0.20.0-beta/lncli /usr/local/bin/lncli
lnd --version
```

Expected: `lnd version 0.20.0-beta`

---

### 4.2 Configure lnd.conf

Create `/mnt/ssd/lnd/lnd.conf` with the content below.

> **Replace:**
> - `<YOUR_RPC_USER>` and `<YOUR_RPC_PASS>` with your Bitcoin Core RPC credentials
> - `<LND_DB_PASSWORD>` with the PostgreSQL password you set in Phase 2
> - If port 8080 is in use, keep `restlisten=127.0.0.1:8082` (see step 5.1)

```ini
[Application Options]
alias=MyNode-BR
color=#ff9900
debuglevel=info
restlisten=127.0.0.1:8082

# Uncomment after creating wallet:
# wallet-unlock-password-file=/data/lnd/password.txt
# wallet-unlock-allow-create=true

tlsautorefresh=true
tlsextraip=127.0.0.1
tlsdisableautofill=true

accept-keysend=true
accept-amp=true
allow-circular-route=true

[Bitcoin]
bitcoin.mainnet=1
bitcoin.node=bitcoind
bitcoin.basefee=1000
bitcoin.feerate=1
bitcoin.timelockdelta=144

[db]
db.backend=postgres
db.use-native-sql=true

[postgres]
db.postgres.dsn=postgres://lndpg:<LND_DB_PASSWORD>@127.0.0.1:5432/lnd?sslmode=disable
db.postgres.timeout=0

[Bitcoind]
bitcoind.rpchost=127.0.0.1:8332
bitcoind.rpcuser=<YOUR_RPC_USER>
bitcoind.rpcpass=<YOUR_RPC_PASS>
bitcoind.zmqpubrawblock=tcp://127.0.0.1:28332
bitcoind.zmqpubrawtx=tcp://127.0.0.1:28333
bitcoind.estimatemode=ECONOMICAL

[tor]
tor.active=true
tor.v3=true
tor.skip-proxy-for-clearnet-targets=false
tor.streamisolation=true

[rpcmiddleware]
rpcmiddleware.enable=true
```

Set permissions:
```bash
sudo chown -R lnd:lnd /mnt/ssd/lnd
sudo chmod 750 /mnt/ssd/lnd
```

---

### 4.3 Create LND systemd service

Create `/etc/systemd/system/lnd.service`:

```ini
[Unit]
Description=Lightning Network Daemon (LND)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/lnd --lnddir=/data/lnd
ExecStop=/usr/local/bin/lncli --lnddir=/data/lnd stop
Restart=on-failure
RestartSec=60
Type=notify
TimeoutStartSec=1200
TimeoutStopSec=3600
User=lnd
Group=lnd

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable lnd
sudo systemctl start lnd
sleep 30 && systemctl status lnd --no-pager | grep Active
```

Expected: `Active: active (running)`
Note: "wallet locked" in logs is normal before wallet creation.

---

## PHASE 5 — Debian-specific fixes

### 5.1 Check port 8080 conflict

```bash
ss -tlnp | grep 8080
```

- If a service appears: use `restlisten=127.0.0.1:8082` in `lnd.conf` (already set in the template above).
- If nothing appears: the default port is free and `8082` still works fine.

---

### 5.2 Configure Tor — debian-tor group

> ⚠️ **Critical Debian difference:** the Tor cookie group is `debian-tor`, not `tor`.
> Missing this step causes LND to fail connecting to Tor silently.

```bash
sudo usermod -aG debian-tor lnd
```

Verify Tor is running and configure if needed:
```bash
systemctl status tor --no-pager | head -5
```

If not installed:
```bash
sudo apt-get install -y tor
```

Add required Tor configuration if not present:
```bash
sudo bash -c 'grep -q "ControlPort 9051" /etc/tor/torrc || cat >> /etc/tor/torrc << EOF

# LightningOS LND
ControlPort 9051
CookieAuthentication 1
CookieAuthFileGroupReadable 1
EOF'
sudo systemctl restart tor
```

---

### 5.3 Replace GoTTY with arm64 build

> ⚠️ The GoTTY binary bundled by the installer is `amd64`. It returns `Exec format error` on Raspberry Pi.
> Replace it with the correct `arm64` build.

```bash
curl -L https://github.com/sorenisanerd/gotty/releases/download/v1.5.0/gotty_v1.5.0_linux_arm64.tar.gz \
  -o /tmp/gotty.tar.gz
tar -xzf /tmp/gotty.tar.gz -C /tmp
sudo install -m 0755 /tmp/gotty /usr/local/bin/gotty
gotty --version
```

Expected: `gotty version v1.5.0`

---

### 5.4 Set group permissions

```bash
sudo usermod -aG lnd lightningos
sudo usermod -aG docker lightningos
```

---

### 5.5 Configure secrets.env and config.yaml

Create `/etc/lightningos/secrets.env` — replace all placeholder values:

```bash
sudo tee /etc/lightningos/secrets.env > /dev/null << 'EOF'
LND_PG_DSN=postgres://lndpg:<LND_DB_PASSWORD>@127.0.0.1:5432/lnd?sslmode=disable
NOTIFICATIONS_PG_DSN=postgres://losapp:<APP_DB_PASSWORD>@127.0.0.1:5432/lightningos?sslmode=disable
NOTIFICATIONS_PG_ADMIN_DSN=postgres://losadmin:<ADMIN_DB_PASSWORD>@127.0.0.1:5432/postgres?sslmode=disable
BITCOIN_RPC_USER=<YOUR_RPC_USER>
BITCOIN_RPC_PASS=<YOUR_RPC_PASS>
REPORTS_TIMEZONE=America/Sao_Paulo
TERMINAL_ENABLED=1
TERMINAL_CREDENTIAL=losop:<GENERATE_STRONG_PASSWORD>
TERMINAL_OPERATOR_USER=losop
TERMINAL_OPERATOR_PASSWORD=<SAME_AS_ABOVE>
TERMINAL_PORT=7681
EOF

sudo chmod 660 /etc/lightningos/secrets.env
sudo chown root:lightningos /etc/lightningos/secrets.env
```

Create `/etc/lightningos/config.yaml`:

```bash
sudo tee /etc/lightningos/config.yaml > /dev/null << 'EOF'
server:
  host: "0.0.0.0"
  port: 8443
  tls_cert: "/etc/lightningos/tls/server.crt"
  tls_key: "/etc/lightningos/tls/server.key"

lnd:
  grpc_host: "127.0.0.1:10009"
  tls_cert_path: "/data/lnd/tls.cert"
  admin_macaroon_path: "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"

bitcoin_remote:
  rpchost: "127.0.0.1:8332"
  zmq_rawblock: "tcp://127.0.0.1:28332"
  zmq_rawtx: "tcp://127.0.0.1:28333"

postgres:
  db_name: "lnd"

ui:
  static_dir: "/opt/lightningos/ui"

features:
  enable_login: false
  enable_bitcoin_local_placeholder: true
  enable_app_store_placeholder: true
EOF
```

---

### 5.6 Create LightningOS Manager systemd service

Create `/etc/systemd/system/lightningos-manager.service`:

```ini
[Unit]
Description=LightningOS Manager
After=network-online.target lnd.service
Wants=network-online.target

[Service]
User=lightningos
Group=lightningos
SupplementaryGroups=lnd systemd-journal docker
Type=simple
EnvironmentFile=/etc/lightningos/secrets.env
ExecStart=/opt/lightningos/manager/lightningos-manager --config /etc/lightningos/config.yaml
Restart=on-failure
RestartSec=3
LimitNOFILE=65536
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/var/lib/lightningos /var/log/lightningos /etc/lightningos /data/lnd

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable lightningos-manager
sudo systemctl start lightningos-manager
```

---

## PHASE 6 — Validation and first access

### 6.1 Verify all services

```bash
for svc in bitcoin lnd lightningos-manager; do
  STATUS=$(systemctl is-active $svc 2>/dev/null || echo "not found")
  echo "$svc: $STATUS"
done
sudo docker ps --format "{{.Names}}: {{.Status}}" | grep postgres
```

Expected:
```
bitcoin: active
lnd: active
lightningos-manager: active
lightningos-postgres: Up ...
```

Test the API:
```bash
curl -sk https://127.0.0.1:8443/api/health | python3 -m json.tool
```

---

### 6.2 Access the UI

Find your IP:
```bash
hostname -I | awk '{print $1}'
```

Open in a browser on the same network:
```
https://<YOUR_IP>:8443
```

> The browser will show a self-signed certificate warning. Click "Advanced" → "Proceed anyway".
> This is expected and safe on your local network.

---

### 6.3 Complete the wizard and create the wallet

1. **Bitcoin Remote:** enter your Bitcoin Core RPC credentials
2. **Create wallet:** the system generates 24 seed words
3. **Write down your seed:** on paper only — never take a photo or save digitally
4. **Unlock:** create a strong wallet password

> 🔐 **The 24 seed words are the only way to recover your funds.**
> Store them physically in a secure location. Never photograph or digitally store them.

---

### 6.4 Configure auto-unlock (optional)

After creating the wallet:

```bash
echo "YOUR_WALLET_PASSWORD" | sudo tee /data/lnd/password.txt
sudo chmod 600 /data/lnd/password.txt
sudo chown lnd:lnd /data/lnd/password.txt
```

Uncomment in `/mnt/ssd/lnd/lnd.conf`:
```
wallet-unlock-password-file=/data/lnd/password.txt
wallet-unlock-allow-create=true
```

Restart LND:
```bash
sudo systemctl restart lnd
```

---

## 🎉 Your node is up and running!

Congratulations — you now have a fully operational Lightning node on Raspberry Pi 5 with Debian 12!

Your node is connected to the Bitcoin mainnet, LND is running, and LightningOS Light is
managing everything through a clean web interface at `https://<YOUR_IP>:8443`.

**What to do next:**
- Open channels with well-connected peers (500k+ sats recommended for routing)
- Enable **Autofee** in Lightning Ops to automate fee management
- Set up **Telegram notifications** for on-chain and Lightning events
- Explore the **App Store** to install LNDg for advanced monitoring

---

## ⚡ Enjoyed this guide? Send some sats!

This guide was written by the Brazilian Lightning Network community based on real hands-on
experience installing and running LightningOS Light on a Raspberry Pi 5 with Debian 12.

Hours of trial, error, and troubleshooting went into documenting every quirk so you
wouldn't have to find them the hard way. If it saved you time, consider sending a small
donation to the node that made it possible!

```
⚡ Node:   RaspNode-BR
   Pubkey: 025b2f2679168c49dc72b6ac93ba8b9b7f782113eef613b7bc09bcf53ea05b792c
```

> Connect via [Amboss](https://amboss.space/node/025b2f2679168c49dc72b6ac93ba8b9b7f782113eef613b7bc09bcf53ea05b792c)
> or open a channel directly — every sat helps the Brazilian Lightning community grow! 🇧🇷


---

## Troubleshooting

| Problem | Likely cause | Solution |
|---|---|---|
| `Exec format error` on LND or GoTTY | `amd64` binary on `arm64` system | Download `linux-arm64` version explicitly |
| LightningOS not accessible on port 8443 | Manager failed to start | `journalctl -u lightningos-manager -n 50 --no-pager` |
| LND cannot connect to Tor | `lnd` user not in `debian-tor` group | `sudo usermod -aG debian-tor lnd && sudo systemctl restart lnd` |
| PostgreSQL unreachable | Docker container stopped | `cd /opt/lightningos-postgres && sudo docker compose up -d` |
| `log read failed: journalctl failed` in UI | Debian permission limitation | Cosmetic warning only — no functional impact |
| Port 8080 conflict | Another service (e.g. Nominatim) | Use `restlisten=127.0.0.1:8082` in `lnd.conf` |
| Go build fails with module errors | Module cache issue | Run `sudo /usr/local/go/bin/go mod download` first |

---

## Reference — Key file paths

| What | Path |
|---|---|
| Bitcoin Core config | `/etc/bitcoin/bitcoin.conf` |
| Bitcoin data | `/mnt/ssd/bitcoin` → symlink `/data/bitcoin` |
| LND config | `/mnt/ssd/lnd/lnd.conf` |
| LND data | `/mnt/ssd/lnd` → symlink `/data/lnd` |
| LightningOS config | `/etc/lightningos/config.yaml` |
| LightningOS secrets | `/etc/lightningos/secrets.env` |
| Manager binary | `/opt/lightningos/manager/lightningos-manager` |
| UI files | `/opt/lightningos/ui/` |
| PostgreSQL data | `/mnt/ssd/postgres/data` |
| Repository | `/opt/brln-os-light/lightningos-light` |

---

## Tested service status after full installation

| Service | Status |
|---|---|
| Bitcoin Core | ✅ `systemctl status bitcoin` |
| LND | ✅ `systemctl status lnd` |
| LightningOS Manager | ✅ `systemctl status lightningos-manager` |
| PostgreSQL (Docker) | ✅ `docker ps \| grep postgres` |
| Tor | ✅ `systemctl status tor` |
| Web Terminal (GoTTY) | ✅ `systemctl status lightningos-terminal` |

---

## Useful links

- Original project: [github.com/jvxis/brln-os-light](https://github.com/jvxis/brln-os-light)
- This fork: [github.com/MarcosJRcwb/brln-os-light](https://github.com/MarcosJRcwb/brln-os-light)
- BR⚡LN Community: [services.br-ln.com](https://services.br-ln.com)
- LND documentation: [docs.lightning.engineering](https://docs.lightning.engineering)
- Check your node: [amboss.space](https://amboss.space)
- LND releases: [github.com/lightningnetwork/lnd/releases](https://github.com/lightningnetwork/lnd/releases)

---

*Community guide — March 2026 — Brazilian Lightning Network*
