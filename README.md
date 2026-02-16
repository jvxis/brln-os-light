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
- Telegram notifications: SCB backups, financial summaries, on-demand `/scb` and `/balances`
- App Store: LNDg, Peerswap (psweb), Elements, Bitcoin Core
- Bitcoin Local management (status + config) and logs viewer


## Release notes
### 0.2.3 Beta
- Telegram notifications refactor: general rules card, SCB backup toggle, scheduled financial summaries, and on-demand `/scb` + `/balances` commands (auto-registered in the bot menu).
- SCB backups include peer alias context in the Telegram caption.
- Lightning Ops UI improvements: channel card refinements and channel balance bar.
- Autofee HTLC Insights steps and calibration by node size/liquidity, plus scheduler/manual-run and ceiling fee/seed fixes.
- Rebalance Center score fixes and improvements.
- LND config upgrade fixes and PostgreSQL install fix.
- Localization updates (UI Portuguese refinements).

### 0.2.2 Beta
- New HTLC Manager with hysteresis and minute-based runs, plus multiple improvements.
- Rebalance Center enhancements: manual restart flows, pre-probe routing, watchdogs, ROI logic improvements, and details view.
- Integrated Autofee: mirror brln-autofee, per-channel enablement, 0-fee support, rebalance cost fallback, results search, last-run persistence, tag trend, step-cap per channel, relax mode, and dry-run filtering.
- Channel Auto Heal update and Tor peers checker.
- Wallet activity cleanup fix (remove balances from wallet history).
- LND upgrade fixes and health check follow-bitcoin toggle.

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

Telegram notifications:
- Configure in the UI: Notifications -> Telegram.
- SCB backup on channel open/close (toggle).
- Scheduled financial summary (hourly to 12-hour intervals).
- On-demand commands: `/scb` (backup) and `/balances` (summary).
- Bot token comes from @BotFather and chat id from @userinfobot.
- Direct chat only; leaving both fields empty disables Telegram.

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

## Rebalance Center
Rebalance Center is an inbound (local/outbound) liquidity optimizer for LND. It can run manual rebalances per channel or fully automated scans that enqueue rebalances based on ROI and budget constraints. A rebalance only proceeds when **outgoing fee > peer fee** so you never pay more than the peer charge without a positive spread. Costs are tracked from notifications (fee msat) and aggregated into live cost + daily auto/manual spending.

Key behavior:
- Manual rebalances ignore the daily budget and can be started per channel.
- Auto rebalances respect the daily budget and only target channels explicitly marked as `Auto`.
- Source channels are selected from those with enough local liquidity and not excluded; a channel filled by rebalance becomes **protected** and cannot be used as a source until payback rules release it.
- Targets are chosen when outbound liquidity deficit exceeds the deadband and fee spread is positive; ROI estimate uses last 7 days of routing revenue vs estimated rebalance cost.
- Auto targets are ranked by **economic score** = (expected gain − estimated cost), so higher-margin channels are prioritized.
- A **profit guardrail** prevents auto enqueues when expected gain is lower than estimated cost (when both are known). If ROI is indeterminate (cost = 0 with positive spread), auto is still allowed.
- Source selection is weighted by pair history: recent successful pairs with lower fees are prioritized, while recent failures are de‑prioritized.
- The overview shows **Last scan** in local time and a scan status (e.g., no sources, no candidates, budget exhausted) plus economic telemetry (top score, profit guardrail skips) and optional skip details.
- Manual rebalances can optionally **auto-restart** (per-channel toggle) with a 60s cooldown until the target is reached.
- Route **pre-probing** runs before sending, searching for the largest feasible amount on the route.

Channel Workbench:
- Set per-channel target outbound percentage.
- Toggle `Auto` to allow auto mode to rebalance that channel.
- Toggle the restart icon to auto-restart manual rebalances for that channel.
- Toggle `Exclude source` to block a channel from ever being used as a source.
- Sort toggle: **Economic** (score-based) or **Emptiest** (lowest local % first).

Color coding (channel rows):
- Green background = eligible source (can fund rebalances).
- Red background = eligible target (auto-enabled and needs outbound).
- Amber background = potential target (needs outbound but not auto-enabled).

Configuration parameters:
- Auto-only settings: `Enable auto rebalance`, `Scan interval (sec)`, `Daily budget (% of revenue)`.
- `Enable auto rebalance`: turns auto scanning on/off.
- `Scan interval (sec)`: how often auto scan runs.
- `Daily budget (% of revenue)`: percent of the last 24h routing revenue allocated to auto rebalances.
- `Deadband (%)`: minimum outbound deficit before a channel becomes a target.
- `Minimum local for source (%)`: minimum local liquidity required for a channel to be a source.
- `Economic ratio`: fraction of the target channel outbound fee (base+ppm) used as the maximum fee cap.
- `Econ ratio max (ppm)`: optional cap for the fee limit when using economic ratio (0 = no cap).
- `Fee limit (ppm)`: overrides economic ratio with a fixed max fee ppm (0 = disabled).
- `Subtract source fees`: reduces the fee budget by estimated source fees (more conservative).
- `ROI minimum`: minimum estimated ROI (7d revenue / estimated cost) to enqueue auto jobs.
- `Max concurrent`: maximum number of rebalances running at the same time.
- `Minimum (sats)`: smallest rebalance amount for standard attempts (probing may go below to capture a valid route).
- `Maximum (sats)`: upper bound for rebalance size (0 = unlimited).
- `Fee ladder steps`: number of fee caps to try from low to high before giving up.
- `Amount probe steps`: number of amount probes from large to small when a last-hop temporary failure occurs.
- `Fail tolerance (ppm)`: probing stops when the delta between amounts is below this threshold.
- `Adaptive amount probing`: caps the next attempt based on the last successful amount.
- `Attempt timeout (sec)`: maximum time per attempt before moving to the next fee/amount.
- `Rebalance timeout (sec)`: maximum runtime per rebalance job (auto or manual).
- `Mission control half-life (sec)`: decay time for mission control failures (lower = forget faster, 0 = LND default).
- `Payback policy`: three modes can be enabled together.
- `Release by payback`: unlocks protected liquidity once routing revenue repays the rebalance cost.
- `Release by time`: unlocks after `Unlock days` since the last rebalance.
- `Critical mode`: unlocks a fraction when sources are scarce for repeated scans.
- `Unlock days`: number of days before time-based unlock.
- `Critical release (%)`: percent of protected liquidity released per critical cycle.
- `Critical cycles`: consecutive scans with low sources before critical release triggers.
- `Critical min sources`: minimum eligible source channels required to avoid critical mode.
- `Critical min available sats`: minimum total source liquidity required to avoid critical mode.

## Lightning Ops: Autofee
Autofee automatically adjusts **outbound fees** to maximize **profit first** and **movement second**. It uses your local routing and rebalance history (Postgres notifications) plus optional Amboss metrics to seed prices, then applies guardrails, cooldowns, and caps so updates are safe and explainable.

UI parameters:
- `Enable autofee`: global on/off.
- `Profile`: Conservative / Moderate / Aggressive (sets internal thresholds).
- `Lookback window (days)`: 5 to 21 days for stats.
- `Run interval (hours)`: minimum 1 hour.
- `Cooldown up / down (hours)`: minimum time between fee increases / decreases.
- `Min fee (ppm)` and `Max fee (ppm)`: hard clamps (min can be `0`).
- `Rebalance cost mode`: `Per-channel`, `Global`, or `Blend` (controls the cost anchor used in floors/margins).
- `Amboss fee reference`: optional seed source; requires API token.
- `Inbound passive rebalance`: uses inbound discount for sink channels.
- `Discovery mode`: faster lowering for idle/high-outbound channels.
- `Explorer mode`: temporary exploration cycles; may skip cooldown on down moves.
- `Revenue floor`: keeps a minimum floor for high-performing channels.
- `Circuit breaker`: reduces steps if demand drops after recent increases.
- `Extreme drain`: accelerates fee increases when a channel is chronically drained.
- `Super source` + base fee: raises base fee when a channel is classified as super source.
- `HTLC signal integration`: enables failed-HTLC feedback from HTLC Manager.
- `HTLC mode`: `observe_only` (telemetry/tags only), `policy_only` (policy-side effects only), `full` (policy + liquidity effects, default).

HTLC signal behavior:
- Signal window is aligned to Autofee cadence: `max(run_interval, 60m)`.
- Minimum sample/fail thresholds are scaled by the active HTLC window and calibrated by node size + liquidity class.
- Summary line includes: `htlc_liq_hot`, `htlc_policy_hot`, `htlc_low_sample`, `htlc_window`.
- Per-channel line includes counters when present: `htlc<window>m a=<attempts> p=<policy_fails> l=<liquidity_fails>`.

Automatic calibration:
- Each run computes a node classification and liquidity status to auto-scale thresholds.
- Node size classes (based on total capacity and channel count):
  - `small`: < 50M sats or < 20 channels
  - `medium`: < 200M sats or < 60 channels
  - `large`: < 1.5B sats or < 150 channels
  - `extra large`: everything above
- Liquidity classes (based on local ratio):
  - `drained`: local ratio < 25%
  - `balanced`: 25% to 75%
  - `full`: local ratio > 75%
- The calibration line in Autofee Results shows these classes plus calibrated `revfloor` thresholds.

Autofee Results lines:
- Header: run type and timestamp.
- Summary: counts for up/down/flat and skip reasons.
- Seed: Amboss and fallback usage.
- Calibration: node size, liquidity, and calibrated thresholds.
- Per-channel lines: decision, target, floors, margins, and tags.
- Results filters: you can show the last N runs and optionally filter by a local time range.

Tag glossary (Autofee Results):
- `sink`, `source`, `router`, `unknown`: class labels.
- `discovery`: channel in discovery mode.
- `discovery-hard`: discovery harddrop triggered (no baseline + idle).
- `explorer`: explorer mode active.
- `cooldown-skip`: cooldown skipped on a down move due to explorer.
- `surge+X%`: surge bump applied.
- `top-rev`: top-revenue-share bump applied.
- `neg-margin`: negative-margin protection bump.
- `negm+X%`: additional negative-margin uplift.
- `revfloor`: revenue floor applied.
- `outrate-floor`: outrate floor applied.
- `peg`: peg to observed outrate.
- `peg-grace`: peg applied inside grace window.
- `peg-demand`: peg applied due to strong demand vs seed.
- `circuit-breaker`: circuit breaker reduced the step.
- `extreme-drain`: extreme-drain step boost path used.
- `extreme-drain-turbo`: extra extreme-drain turbo uplift.
- `cooldown`: cooldown blocked an update.
- `cooldown-profit`: cooldown held a profitable down move.
- `hold-small`: change below minimum delta/percent.
- `same-ppm`: target equals current ppm.
- `no-down-low`: down move blocked while deeply drained.
- `no-down-neg-margin`: down move blocked while margin is negative.
- `sink-floor`: extra floor margin applied to sink channels.
- `trend-up`, `trend-down`, `trend-flat`: next-move directional hint.
- `stepcap`: step cap applied.
- `stepcap-lock`: step cap lock applied.
- `floor-lock`: floor lock applied.
- `global-neg-lock`: global negative-margin lock applied.
- `lock-skip-no-chan-rebal`: lock skipped because there is no channel rebalance history.
- `lock-skip-sink-profit`: lock skipped in sink profitability path.
- `profit-protect-lock`: profit-protection lock applied.
- `profit-protect-relax`: profit-protection relaxed.
- `super-source`: channel classified as super source.
- `super-source-like`: router-like super-source classification.
- `inb-<n>`: inbound discount applied.
- `htlc-policy-hot`: high policy-failure HTLC signal.
- `htlc-liquidity-hot`: high liquidity-failure HTLC signal.
- `htlc-sample-low`: HTLC sample too small for hot classification.
- `htlc-neutral-lock`: both HTLC hot signals present and neutral lock path used.
- `htlc-liq+X%`: liquidity-hot bump applied.
- `htlc-policy+X%`: policy-hot bump applied.
- `htlc-liq-nodown`: down move blocked by liquidity-hot signal.
- `htlc-policy-nodown`: down move blocked by policy-hot signal.
- `htlc-neutral-nodown`: down move blocked by combined HTLC lock.
- `htlc-step-boost`: HTLC hot-signal step-cap boost.
- `seed:amboss`: Amboss seed used.
- `seed:amboss-missing`: Amboss token missing.
- `seed:amboss-empty`: Amboss returned no data.
- `seed:amboss-error`: Amboss fetch error.
- `seed:med`: Amboss median blended in.
- `seed:vol-<n>%`: volatility penalty applied.
- `seed:ratio<factor>`: out/in ratio adjustment applied.
- `seed:outrate`: seed from recent outrate.
- `seed:mem`: seed from memory.
- `seed:default`: default seed fallback.
- `seed:guard`: seed jump guard applied.
- `seed:p95cap`: seed capped at Amboss p95.
- `seed:absmax`: absolute seed cap applied.

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
