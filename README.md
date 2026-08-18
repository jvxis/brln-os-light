# LightningOS

> [!IMPORTANT]
> LightningOS is a local node-control plane and is **not designed for direct public Internet exposure**. Run it only on a trusted LAN or through a private VPN such as Tailscale, behind a host or network firewall. Never forward port `8443` or App Store ports from the public Internet. The installers restrict `8443` to the detected LAN and `tailscale0` only when UFW is already installed and active; they do not enable UFW automatically. Always verify `sudo ufw status` or provide an equivalent external firewall before using the node.

[Join the LightningOS Signal group](https://signal.group/#CjQKIEMiPq5Dy_s5RlfF4fZBhT7_2mqlWHlzEbcQUS20bOGHEhCWL0uFC3ebHZ3W3pAs8Hox)

<img width="1920" height="1080" alt="BRLN-logo" src="https://github.com/user-attachments/assets/7394bf7b-2515-461a-8b80-7488531c7f40" />

[Clique aqui](https://github.com/jvxis/brln-os-light/blob/main/docs/README-PT-BR.md) para ver a versão em PT-BR

[Click here](https://github.com/jvxis/brln-os-light/blob/main/docs/16_INSTALL_RASPBERRYPI5_DEBIAN12.md) Raspberry Pi 5 + Debian 12

LightningOS is a Full Lightning Node Daemon Installer, Lightning node manager with a guided wizard, dashboard, and wallet. The manager serves the UI and API over HTTPS on `0.0.0.0:8443` by default for LAN access (set `server.host: "127.0.0.1"` for local-only) and integrates with systemd, Postgres, smartctl, Tor/i2pd, and LND gRPC.
<img width="1494" height="1045" alt="image" src="https://github.com/user-attachments/assets/8fb801c0-4946-48d8-8c24-c36a53d193b3" />
<img width="1491" height="903" alt="image" src="https://github.com/user-attachments/assets/cfda34d5-bccc-4b18-9970-bad494ae77b3" />
<img width="1576" height="1337" alt="image" src="https://github.com/user-attachments/assets/019cfff2-f354-4c2b-a595-2a15bb228864" />
<img width="1779" height="4334" alt="image" src="https://github.com/user-attachments/assets/a8a2a74a-bcc9-4d26-bfbb-32d5d4e4a792" />
<img width="1280" height="660" alt="image" src="https://github.com/user-attachments/assets/84489b07-8397-4195-b0d4-7e332618666d" />
<img width="1779" height="1106" alt="image" src="https://github.com/user-attachments/assets/99f950bb-7ce0-46bf-9af0-fe12188a54cf" />


## Highlights
- Mainnet only (remote Bitcoin default)
- No Docker in the core stack
- LND managed via systemd, gRPC on localhost
- Seed phrase is never persisted or logged
- Wizard for Bitcoin RPC credentials, native seed/wallet setup, and first admin enrollment
- Redesigned dashboard with node pulse, core health, automation risk, recent activity, and revenue panels
- Lightning Ops suite: peers/channels, Network Atlas, Graph Explorer, New Channels, Rebalance Center, Autofee, Channel Ranking, Node Retirement, HTLC signals, and Channel Auto Heal
- Wallet with QR invoice scanning, blinded invoice support, route preview/probing, validated-route payments, and payment detail views
- Keysend Chat: 1 sat per message + routing fees, unread indicators, 30-day retention
- Real-time notifications (on-chain, Lightning, channels, forwards, rebalances)
- Telegram notifications: SCB backups, financial summaries, on-demand `/scb` and `/balances`
- Daily routing reports (timer + backfill + live API + movement live API)
- On-chain Hub with coin control, UTXO labels/groups/locks, fee bumping, transaction details, and Wallet Flow provenance graph
- App Store with 19 registered apps/services, dependency checks, persistent data, native integrations, and on-demand Docker installation
- Dedicated Taproot Assets interface for asset discovery, universe sync, mint/reissue, receive, send preview, send, and redeem
- Bitcoin Local, Elements, LND, Tor, disk, database maintenance, audit, terminal, shortcut, and log management

## Feature overview
- **Secure setup and administration:** guided first-run enrollment, local setup/recovery tokens, optional login enablement for legacy installs, secure HTTP-only sessions, CSRF protection, fresh reauthentication for external on-chain sends, and an audit trail for sensitive actions.
- **Dashboard and system operations:** node pulse, Bitcoin/LND/system health, liquidity, recent activity, revenue, automation risk, market/fee panels, safer service actions, journal context, Postgres maintenance, disk health, and application/LND/Tor upgrade workflows.
- **Wallet and on-chain operations:** invoices, QR scanning, blinded invoices, payment decoding, route preview/probing, automatic or validated-route payment, MPP, payment details, address generation, external-send preview, coin control, metadata, UTXO groups, lock/unlock, and fee bumping.
- **Network intelligence:** Network Atlas, cached native Graph Explorer, node search and policy history, closed-channel enrichment, peer recommendations, New Channels scoring from 30-day local evidence, and per-channel ranking with economic and operational signals.
- **Liquidity automation:** manual and automatic rebalancing, Sovereign shadow/live scheduler, fast paths, pre-probing, MPP/MSPR, budgets, ROI/payback guardrails, source protection, Mission Control controls, adaptive AutoTarget v2, and auditable decision/outcome history.
- **Fee automation:** explainable per-channel Autofee with native graph and optional Amboss seeds, dynamic liquidity states, HTLC pressure, profitability floors, node-size calibration, Balanced/Market Refill modes, outcome measurement, and detailed tags/history.
- **Channel lifecycle and safety:** peer/channel controls, batch opens, close previews and recovery, channel Parked mode, Auto Heal, Tor peer checks, HTLC Manager, failed-payment cleanup, watchtower management, Balanced Open, Node Retirement, and optional Succession dead-man switch.
- **Observability and communication:** live notifications, Telegram SCB backups and summaries, routing reports and backfills, movement/live APIs, Keysend chat, audit events, service logs, and a protected optional web terminal.
- **Bitcoin and Liquid ecosystem:** remote or local Bitcoin Core, Elements with local/remote mainchain selection, Peerswap, Wallet Flow provenance fallbacks, Taproot Assets, and applications for wallets, mining, federation, analytics, payments, and self-hosted infrastructure.

## Repository layout
- `cmd/lightningos-manager`: Go backend (API + static UI)
- `ui`: React + Tailwind UI
- `templates`: systemd units and config templates
- `install.sh`: idempotent installer (wrapper in `scripts/install.sh`)
- `install_existing.sh`: installer for existing nodes (x86_64/amd64)
- `install_existing_pi.sh`: installer for existing nodes on Raspberry Pi 4 (arm64)
- `configs/config.yaml`: local dev config

## Install (Ubuntu Server)
The installer provisions everything needed on a clean Ubuntu box:
- Postgres, smartmontools, curl, jq, ca-certificates, openssl, build tools
- Tor (ControlPort enabled) + i2pd enabled by default
- Go 1.24.12 and the latest Node.js major (falls back to Node.js 20.x if resolution fails)
- LND binaries (default `v0.21.1-beta`)
- LightningOS Manager binary (compiled locally)
- UI build (compiled locally)
- systemd services and config templates
- node-local CA, LAN TLS certificate, and automatic `.local` discovery

The familiar installer entry point automatically switches to the latest published LightningOS release before installation:
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

For development or an offline install of the exact local checkout, use `sudo LIGHTNINGOS_INSTALL_SOURCE=checkout ./install.sh`.

When UFW is already installed and active, the installer automatically detects which local IPv4 network may access the manager (for example, `192.168.1.0/24`). It removes the old public `8443/tcp` rule, allows LAN-only mDNS discovery on `5353/udp`, and also allows access through `tailscale0` when Tailscale is available. The installer deliberately does not activate UFW on an existing host because doing so could block SSH or unrelated node services; an inactive or missing UFW therefore requires an equivalent LAN/VPN firewall configured by the operator.

### Install via curl (bootstrap)
This resolves the latest published LightningOS release, checks out its exact tag, then runs `lightningos-light/install.sh`.
```bash
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo ACCEPT_MIT_LICENSE=1 bash
```

Optional overrides:
```bash
# Use a different clone location
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_DIR=/opt/brln-os-light bash

# Pin a branch/tag instead of the latest published release
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_REF=main bash

# Install on an existing x86_64/amd64 node
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_INSTALLER=install_existing.sh bash

# Install on an existing Raspberry Pi 4 (arm64) node
curl -fsSL https://raw.githubusercontent.com/jvxis/brln-os-light/main/lo_bootstrap.sh | sudo BRLN_INSTALLER=install_existing_pi.sh bash

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

Run the installer that matches your environment; each script automatically selects the latest published LightningOS release:
```bash
cd lightningos-light

# Existing node on x86_64/amd64
sudo ./install_existing.sh

# Existing node on Raspberry Pi 4 (arm64)
sudo ./install_existing_pi.sh
```

Access the UI from another machine on the same LAN. The installer prints both addresses:

- Preferred: `https://<SERVER_HOSTNAME>.local:8443`
- IP fallback: `https://<SERVER_LAN_IP>:8443`

LightningOS creates a private CA unique to the node and a certificate valid for
both addresses. On the first device access, use **Trust this device** on the
login screen to download the Windows trust installer or the public CA for
another operating system. The CA private key never leaves the node.

The normal app-upgrade flow migrates recognized legacy LightningOS
self-signed certificates automatically and keeps a timestamped backup. A
custom certificate is never replaced; `.local` discovery is only announced
when the active certificate actually covers that hostname.

### Upgrading existing LightningOS installations to 0.5.8

> [!IMPORTANT]
> The official `0.5.2-Beta -> 0.5.8-Beta` path starts from **App Upgrade in the
> LightningOS UI** and completes in two server-side stages. The legacy updater
> first installs the exact 0.5.8 Manager/UI; the new Manager then authenticates
> the release tag and full commit, installs the restricted privileged broker,
> and removes the reviewed legacy privilege grant. Keep the host powered and do
> not start a second upgrade while this transition is running. Closing the
> browser does not stop the server-side operation. Bitcoin Core and LND are not
> restarted by this privilege transition.

Nodes already on `0.5.7-Beta` use the normal single in-app upgrade flow to
`0.5.8-Beta`. Do not rerun `install.sh` or `install_existing.sh` merely to
upgrade either of these supported versions. The `0.5.6-Beta` release has a
release-specific updater bootstrap defect and requires an operator-assisted
bootstrap instead of repeated UI attempts.

## First secure access
- Login protection is enabled by default on new installs.
- At the end of `install.sh`, `install_existing.sh`, and `install_existing_pi.sh`, the installer prints the UI URL and an admin setup token in the console when no admin password is configured yet.
- On the first access, or after upgrading an older install that still has no admin password, the UI opens the admin password setup screen before entering the wizard or dashboard.
- If you need another setup token later, generate it locally on the node:

```bash
sudo /opt/lightningos/manager/lightningos-manager auth setup-token new
```

- If you forget the admin password, generate a local recovery token:

```bash
sudo /opt/lightningos/manager/lightningos-manager auth recovery new
```

- Recovery changes only the UI/API admin password. It does not reset the LND wallet password.
- Scheduled services such as Autofee, Rebalance, reports, succession, and other backend timers keep running without browser login.
- Manual on-chain sends to an external address require a fresh password confirmation. Internal automations and succession flows are not blocked by this extra confirmation.

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
LightningOS includes a real-time notifications system that tracks:
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
- UI includes a general rules card for operational defaults.
- SCB backup on channel open/close (toggle).
- Scheduled financial summary (hourly to 12-hour intervals).
- On-demand commands: `/scb` (backup) and `/balances` (summary).
- `/scb` and `/balances` are auto-registered in the Telegram bot menu.
- SCB backup messages include peer alias context in the caption.
- Bot token comes from @BotFather and chat id from @userinfobot.
- Direct chat only; leaving both fields empty disables Telegram.

Environment keys:
- `NOTIFICATIONS_TG_BOT_TOKEN`
- `NOTIFICATIONS_TG_CHAT_ID`

## Reports
Daily routing reports are stored in Postgres (same DB/user as notifications) and reconciled idempotently through the last complete local day.

Schedule:
- `lightningos-reports.timer` runs reconciliation at minute `05` of every hour. Missed or transiently failed days are retried automatically.
- The UI detects missing complete days at startup and offers a **Reconcile reports** action with progress. This does not restart LND or Bitcoin.
- Automatic/manual reconciliation: `/opt/lightningos/manager/lightningos-manager reports-reconcile`.
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
- `GET /api/reports/movement/live` (live movement/rebalance window for the current day)
- `GET /api/reports/live` (today 00:00 local → now, cached ~60s)

## Lightning Ops (feature map)
- Network Atlas: live network-map view with configurable presentation and node/channel context.
- Channel management: searchable peers/channels, connect/disconnect and boost flows, batch-open assistance, policy/status updates, detailed economics/activity/failure tabs, watchtowers, signing, and SCB restore.
- Graph Explorer: native network graph snapshot, node search, general/channel/closed/fee tabs, local-peer reconciliation, and fee-policy history.
- New Channels: peer candidates built from observed routes, local pain, graph quality, demand, relief, and confidence.
- Channel Ranking: per-channel score, recommended state, 7d vs 30d comparison, dynamic liquidity state, Parked mode, top source/sink signals, actionable recommendations, and deep links into other operations.
- Rebalance Center: manual/automatic and Sovereign shadow/live rebalances with score-based targeting, adaptive AutoTarget v2, watchdogs, pre-probing, split probe/execute floors, MSPR, ROI/payback guardrails, source-effectiveness weighting, and optional manual auto-restart.
- Autofee: per-channel fee automation with cost anchors, native corrected graph seeding, optional Amboss seeding, dynamic liquidity state, HTLC signals, refresh telemetry, calibration by node size/liquidity, scheduler/manual runs, outcomes, and detailed history.
- Channel opening/closing: single and batch open previews, pending-open fee bump, cooperative/force-close paths, peer-funded warnings, Close Manager recovery, and Balanced Open sessions.
- Node Retirement: guided safe node decommission workflow with session timeline, cooperative close controls, exception handling, and on-chain reconciliation.
- HTLC Manager: hysteresis-based HTLC telemetry used by Autofee and liquidity decisions.
- Channel Auto Heal + Tor peers checker: operational guardrails for peer/channel reliability.
- Health checks: optional follow-bitcoin checks for LND/node health workflows.

## Graph Explorer
Graph Explorer is the native graph-inspection layer. It builds and caches a graph snapshot from LND, then lets the operator inspect peers without leaving LightningOS.

What it provides:
- Search by alias, pubkey, address, and graph metadata.
- General node view with copyable pubkey/address data and graph source context.
- Channels tab with public channel capacity, policy, direction, and peer data.
- Closed tab with close classification, close source, local-channel enrichment, and recovered close context when available.
- Fees tab with outbound/inbound policy summaries, distribution charts, average fee history, and policy ceiling indicators.
- Recompute action for rebuilding the cached snapshot after major graph or node changes.

Operational use:
- Use it before opening channels to inspect candidate peers and their public-channel footprint.
- Use closed-channel enrichment to understand historical close behavior.
- Use fee summaries to compare a peer's advertised pricing against your own Autofee and routing strategy.

## New Channels
New Channels is a recommendation module for opening channels. It combines your local routing evidence with the cached graph snapshot to rank peers that may relieve routing pain or improve revenue paths.

Candidate inputs:
- Observed successful routes and route volume over 30 days.
- Failed route attempts and expensive path evidence.
- Shared adjacency with strong peers or problem peers.
- Graph channel count, total capacity, and best advertised outbound fees.
- Demand score, relief score, graph-quality score, confidence, and human-readable reasons.

How to use it:
- Start with high-confidence candidates that also show route demand or relief signals.
- Cross-check the candidate in Graph Explorer before committing capital.
- Use the recommendation as an input to channel opening, not as an automatic open trigger.

## Channel Ranking
Channel Ranking is the analysis layer for open channels. It is designed to answer four practical questions quickly:
- Is this channel producing net value?
- Is this peer worth more capital?
- Is this channel becoming expensive to maintain?
- Should I expand, maintain, monitor, or prepare a close?

Besides direct routing economics, the score also considers `assisted revenue` from forwards. This gives partial credit to the incoming channel, because some channels are strategically valuable as entry paths even when their direct outbound net result is weak.

Where it lives:
- Main page: `Channel Ranking`
- Lightweight indicator: each channel card in `Lightning Ops > Channels` shows only the short badge and score
- Deep links: recommendations can open the relevant area in `Lightning Ops`, `Autofee`, `Rebalance Center`, `HTLC Manager`, or `Close Manager`

What the score means:
- The score is a `0-100` operating score used for ranking, not a blind automation trigger.
- Higher score means the channel is showing healthier economics and lower operational friction.
- Lower score means the channel is showing weaker net return, worse channel health, or higher maintenance burden.
- The score is best used comparatively across your own channels, not as a universal grade across all nodes.

Quick reading of score bands:
- `70-100`: usually healthy and competitive inside your node
- `45-69`: usually acceptable, but worth checking the detail before adding capital
- `25-44`: usually a channel to monitor closely
- `0-24`: usually a weak channel economically or operationally, often close-worthy if the condition persists

How the score is calculated:
- Profitability: forwarding fees minus rebalance costs
- Assisted revenue: a weighted credit from forward entry traffic, used to avoid undervaluing channels that help other channels earn routing fees
- Capital efficiency: how much net result the channel generates relative to its capacity
- Utilization: how much forwarding volume the channel carries and whether liquidity is balanced enough to be useful
- Maintenance burden: how expensive rebalancing is relative to the routing income it supports
- Operational health: channel activity, pending HTLC pressure, peer stability over 30 days, and HTLC failure pressure
- Confidence: whether the channel already has enough recent routing/rebalance data to judge it with more confidence

Additional advanced signals shown in the module:
- `Peer stability 30d`: derived from repeated peer connectivity samples, recent errors, and ping quality
- `HTLC failures 30d`: aggregated failed HTLC pressure for the channel, split into policy, liquidity, and forward failures
- `Rebalance dependence`: how much the channel seems to rely on rebalances to stay useful
- `Feedback`: recent score and net-result change versus historical snapshots, to help validate whether the current recommendation is helping

What a high score usually means:
- Positive net routing result
- Good utilization relative to channel size
- Rebalance costs under control
- Stable peer/channel behavior
- Lower HTLC failure pressure

What a low score usually means:
- Weak or negative net result
- Capital tied up with little throughput
- Rebalance cost eating the economics
- Unstable peer behavior or low peer stability
- Elevated HTLC failure pressure
- Little direct or assisted contribution to the node's routing result

Recommended states:
- `Expand`: strong economics, good usage, healthy peer, and signs that more capacity may pay off
- `Maintain`: healthy enough to keep current policy and observe normally
- `Monitor`: something is inefficient or unstable, but there is not enough evidence yet for immediate close
- `Close`: persistent weakness, risk, or opportunity cost is high enough that preparing an exit is reasonable

Channel automation mode:
- A channel can be placed in `Parked` mode from Channel Ranking, with an optional fixed outbound fee, review date, and operator note.
- Parking suspends Autofee and automatic/manual-restart rebalance participation, excludes the channel as a rebalance source, and prevents new rebalance jobs for it.
- Returning the channel to `Normal` restores the automation settings captured when it was parked.

How to read the page:
- Ranking list: compare channels by score or sort by net result, capital efficiency, rebalance cost, peer stability, HTLC failures, rebalance dependence, or operational risk
- Detail panel: inspect the selected channel with:
  - metrics
  - `7D / 30D Economics`
  - trend and score history
  - operational signals
  - reasons behind the state
  - actionable recommendations
  - other channels from the same peer

How to use it in practice:
- Start by sorting by `Net result 30d` or `Operational risk`
- Open the worst and best channels to compare why they differ
- Use `Monitor` channels to review Autofee, rebalance policy, HTLC pressure, and peer stability before deciding to close
- Use `Expand` channels as candidates for additional capital or liquidity support
- Use `Close` channels to prepare an orderly cooperative close rather than reacting only when the channel becomes a problem

Important note:
- `Score` ranks channels
- `State` expresses the operational recommendation
- `Recommendations` suggest the next review path

These three are related, but not identical. A medium-score channel can still be classified as `Monitor` or `Close` if the risk and maintenance signals are bad enough.

## Rebalance Center
Rebalance Center is an inbound (local/outbound) liquidity optimizer for LND. It moves local liquidity toward channels where that liquidity can likely be sold with an economic advantage: low outbound/local balance, `outgoing_fee_ppm > peer_fee_ppm`, available source liquidity, and route cost below the expected gain. It can run manual rebalances per channel or fully automated scans that enqueue rebalances based on cost gate, ROI, profit guardrail, cooldowns, and budget constraints. Costs are tracked from notifications (fee msat) and aggregated into live cost + daily auto/manual spending.

Operator course:
- EN: `docs/24_REBALANCE_CENTER_COURSE_EN.md`
- PT-BR: `docs/23_REBALANCE_CENTER_COURSE_PT_BR.md`

Key behavior:
- Manual Rebal In uses more permissive manual target eligibility and can be used for testing, one-off correction, or forcing a specific channel.
- Manual rebalances ignore the daily budget and can be started per channel.
- Auto rebalances respect the daily budget and only target channels explicitly marked as `Auto`.
- Source channels are selected from those with enough local liquidity and not excluded; a channel filled by rebalance becomes **protected** and cannot be used as a source until payback rules release it.
- Targets are chosen when outbound liquidity deficit exceeds the deadband and fee spread is positive; ROI estimate uses last 7 days of routing revenue vs estimated rebalance cost.
- Auto targets also pass the cost gate, ROI minimum, profit guardrail, target/source cooldowns, channel busy checks, execute floor, and remaining budget.
- Auto targets are ranked by **economic score** = (expected gain − estimated cost), so higher-margin channels are prioritized.
- A **profit guardrail** prevents auto enqueues when expected gain is lower than estimated cost (when both are known). If ROI is indeterminate (cost = 0 with positive spread), auto is still allowed.
- `Bypass cost gate` is per-channel. It skips only the expected-cost gate; ROI and profit guardrails still protect the job afterward.
- Source selection now also considers source effectiveness and temporary route-dead cooldowns, so repeatedly failing targets/sources are cooled down before wasting more attempts.
- Source selection is weighted by pair history: recent successful pairs with lower fees are prioritized, while recent failures are de‑prioritized.
- The overview shows **Live cost 24h**, eligible sources/targets, **Last scan** in local time, scan status/reasons, effectiveness/attempts, ROI/payback progress, route failure pressure, MSPR 24h, and optional skip details.
- Baseline metrics are available at `/api/rebalance/metrics/baseline?days=1`; use 24h for a quick comparison and 7d for a stronger decision.
- Manual Restart Watch respects `EligibleAsTarget`; it retries manual auto-restart jobs after interval/cooldown and only ignores the cost gate when `Bypass cost gate` is enabled for that channel.
- Route **pre-probing** runs before sending, searching for the largest feasible amount on the route.
- **Sovereign scheduler:** the Auto Pilot can run in shadow mode for decision review or live mode for execution. Candidate scope, jobs per cycle, minimum expected profit, slow-seller handling, route-risk quarantine, source opportunity cost, exploration share, and EV-weighted scoring are configurable and recorded in Sovereign history.
- **AutoTarget v2:** for channels opted into management, the autopilot adjusts `target_outbound_pct` gradually within configured bounds. Decisions combine sell-through, drain rate, local balance, 7-day revenue, rebalance success, node-calibrated thresholds, and per-cycle up/down limits; every change or hold is written to AutoTarget history.
- The configured `Maximum (sats)` also caps Sovereign scan economics and the amount a selected job can spend, so candidate scoring and execution use the same ceiling.

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
- `Budget mode` / `Budget auto only`: hybrid budget mode can protect auto runs while leaving manual restart behavior clearer.
- `Manual reserve`: optional fixed or percentage reserve that protects part of the budget for manual restart workflows.
- `Deadband (%)`: minimum outbound deficit before a channel becomes a target.
- `Minimum local for source (%)`: minimum local liquidity required for a channel to be a source.
- `Economic ratio`: fraction of the target channel outbound fee (base+ppm) used as the maximum fee cap.
- `Econ ratio max (ppm)`: optional cap for the fee limit when using economic ratio (0 = no cap).
- `Fee limit (ppm)`: overrides economic ratio with a fixed max fee ppm (0 = disabled).
- `Subtract source fees`: reduces the fee budget by estimated source fees (more conservative).
- `ROI minimum`: minimum estimated ROI (7d revenue / estimated cost) to enqueue auto jobs.
- `Rebalance cost floor (ppm)`: minimum expected route cost when the channel has no usable cost history.
- `Gain model version`: v1 uses historical revenue; v2 uses effective spread plus velocity.
- `Velocity weight`: controls how strongly v2 prioritizes channels with real drain rate over fairness/age.
- `Source min payback progress`: protects channels that bought liquidity by rebalance until enough revenue has paid the cost back.
- `Max concurrent`: maximum number of rebalances running at the same time.
- `Minimum (sats)`: legacy start anchor for attempts; with split disabled, it is also the effective probe/execute floor.
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

Split min controls (`Split min (probe/execute)`):
- Purpose: decouple the economic start anchor (`Minimum (sats)`) from strict probe/execute floors.
- `Split min (probe/execute)`: enables separate floor controls for probing and execution.
- `Min probe amount (sats)` (`min_probe_sat`, default `5000`): minimum amount allowed during route probing when split is enabled.
- `Min execute amount (sats)` (`min_execute_sat`, default `10000`): minimum amount allowed to be actually sent when split is enabled.
Key interactions:
- Attempts still start anchored by `Minimum (sats)` (legacy-compatible behavior).
- If split is enabled, probing can go down to `min_probe_sat` and execution is blocked below `min_execute_sat`.
- With split enabled, auto candidates below execute floor are skipped early.
Practical recommendation:
- Keep `Min execute amount (sats)` equal to `Min probe amount (sats)` unless you explicitly want to allow probing lower than execution.
- Use split when you want broader route discovery without opening execution below your chosen floor.

MSPR (`MSPR (Multi-Source Parallel)`):
- Purpose: increase first-pass success chance by trying shards across multiple source channels in parallel before legacy sequential fallback.
- `Enable MSPR` (`mpp_enabled`, default `true`): enables MSPR prepass execution.
- `MSPR for auto jobs only` (`mpp_auto_only`, default `true`): when enabled, only auto jobs use MSPR; manual jobs stay legacy.
- `Max shards` (`mpp_max_shards`, default `6`, range `1..20`): max number of shards planned for the MSPR round.
- `Parallel workers` (`mpp_parallelism`, default `3`, range `1..max_shards`): max concurrent shard attempts in the round.
- `Min shard amount (sats)` (`mpp_min_shard_sat`, default `10000`): minimum shard size planned by MSPR.
- `Round timeout (sec)` (`mpp_round_timeout_sec`, default `35`): max duration for one MSPR round before fallback to legacy attempts.
Execution model:
- MSPR runs one parallel prepass (using shard plan + workers).
- Successful shards are executed and accounted immediately.
- After the prepass, the job continues in the same legacy queue/attempt flow for remaining amount.
- Failed shard attempts appear in history with `mpp shard:` reason prefix.
Practical recommendation:
- Start with the shipped defaults: `max_shards=6`, `parallel_workers=3`, `min_shard=10000`, `round_timeout=35`, `auto only` enabled.
- If you see frequent `mpp_structural_abort`, reduce parallelism or shard count.
- If you see too many large first shards, increase shards (up to `20`) and keep workers lower or equal to shards.
- If your node is resource-constrained, reduce parallel workers first (not shard count).

When to use each mode:
- Legacy-focused (most conservative): split off, MSPR off.
- Better route discovery with controlled floor: split on, set `min_probe=min_execute` (for example `1000`), keep `Minimum (sats)` as economic target (for example `30000`).
- Higher first-pass hit rate in busy graphs: split on + MSPR on, tune shards/workers, monitor 24h MSPR metrics and adjust gradually.

## Lightning Ops: Autofee
Autofee adjusts **outbound fees** per channel with one goal hierarchy:
1. Preserve positive unit economics (profitability).
2. Keep channels moving (avoid liquidity lock).
3. Keep fee updates stable and explainable.

It uses local routing/rebalance history (Postgres notifications), native Graph Explorer seed data based on corrected market averages, optional Amboss seed data, HTLC failure signals, node-size/liquidity calibration, and explainable guardrails.

UI parameters:
- `Enable autofee`: global on/off.
- `Node operation mode`: `Balanced` or `Market refill`.
- `Balanced`: standard mode. Keeps the normal Autofee pipeline, respects rebalance-derived signals, and preserves the latest balanced fee policy snapshot when switching away.
- `Market refill`: node-wide operating mode. Disables automatic rebalance and manual restart watch, restores the previous fee policy when returning to `Balanced`, and uses a dedicated outbound/inbound policy to attract natural refill instead of buying liquidity with rebalance.
- `Profile`: Conservative / Moderate / Aggressive (baseline behavior).
- `Lookback window (days)`: 5 to 21 days (main stats window).
- `Run interval (hours)`: minimum 1 hour.
- `Cooldown up / down (hours)`: minimum time between fee increases / decreases.
- `Min fee (ppm)` and `Max fee (ppm)`: hard clamps.
- `Rebalance cost mode`: `Per-channel`, `Global`, or `Blend`.
- `Amboss fee reference`: optional external seed source; native graph seed remains the first local market reference when available.
- `Inbound passive rebalance`, `Discovery mode`, `Explorer mode`, `Revenue floor`, `Circuit breaker`, `Extreme drain`, `Super source`.
- `HTLC signal integration` and `HTLC mode` (`observe_only`, `policy_only`, `full`).

Movement settings (drawer in the Autofee card):
- Use `0` or leave the field empty to keep the selected profile default.
- `Cooldown up (hours)`: minimum wait before another upward fee move. Higher = slower raises; lower = faster reactions.
- `Cooldown down (hours)`: minimum wait before another downward fee move. Higher = slower drops; lower = faster fee reductions.
- `Step cap override (%)`: maximum fee change allowed per run. Higher = more movement per round; lower = smoother behavior.
- `Discovery down cap override (%)`: extra down-step cap used in discovery-like scenarios. Higher = faster unlock/down moves.
- `Stall relax gap trigger (%)`: minimum gap between current fee and target before stall-relax softens the floor. Lower = relaxes earlier; higher = protects floors longer.
- `Inbound discount max ratio (%)`: maximum inbound discount as a share of the applied outbound fee. Higher = more aggressive inbound pricing for sink-like channels.
- `Inbound discount reach out ratio (%)`: maximum effective out-ratio still eligible for passive inbound discount. Higher = broader reach.
- `Inbound discount min retained spread (%)`: minimum retained spread above the cost anchor when applying inbound discount. Higher = stronger profit protection.
- `Low-flow floor factor override (%)`: multiplier applied to the floor when outbound flow is low. Higher = keeps fees higher; lower = allows lower floors.
- `Global lock soften min out ratio (%)`: minimum out ratio needed before the global negative-margin lock can soften. Lower = more channels become eligible.
- `Global lock soften max drop to peg (%)`: deepest allowed drop toward peg when the global lock is softened. Lower = allows deeper cuts.
- `HTLC min attempts 60m override`: minimum HTLC attempts required before the channel is classified by HTLC behavior. Lower = more HTLC-driven reactions.
- `HTLC policy fail rate override (%)`: policy-failure threshold for HTLC signals. Lower = easier to trigger `policy-hot`.
- `HTLC liquidity fail rate override (%)`: liquidity-failure threshold for HTLC signals. Lower = easier to trigger `liquidity-hot`.

Profile defaults now ship with stronger movement baselines:
- `Conservative`: slower and more protective.
- `Moderate`: faster down moves, softer global lock, higher step cap, and broader movement defaults.
- `Aggressive`: shortest reaction windows, highest movement caps, and the most permissive market-refill behavior.
- The UI reads profile defaults from the backend (`profile_defaults`), so labels and autofill stay aligned with server-side behavior.

Decision pipeline (per channel):
1. Build references:
- `out_ppm7d` from main lookback.
- `rebal_ppm7d` from selected rebalance cost mode.
- When no usable reference exists, `floor_src=min` means the floor came only from `min_ppm`.
- Seed (`native` corrected Graph Explorer average -> `Amboss` weighted-corrected mean -> memory/outrate/default fallbacks).
2. Classify channel behavior (`sink`, `source`, `router`, `unknown`) and liquidity state.
3. Compute a raw target using seed, out ratio, trend/margin rules, HTLC pressure, and profitability heuristics.
4. Apply mode-specific controls (discovery/explorer/stagnation/profit-protect/global locks).
5. Build floor stack (`rebal`, `rebal-sink`, `outrate`, `peg`, `revfloor`, `stagnation`, `no-signal`).
6. Apply step caps and cooldown, then decide `apply` vs `keep`.

Dynamic liquidity state is persisted with Autofee outcomes and surfaced in Fee Center and Channel Ranking, so operator reviews and later outcome measurements use the state that existed when the fee decision was made.

Flow diagram:
- `docs/AUTOFEE_FLOW_DIAGRAM_EN.md`

Balanced mode additions:
- Dynamic upward cooldown by effective `outnorm`: very drained channels can raise faster without fully bypassing cooldown.
- `Drained explorer`: a dedicated exploratory mode for very empty and idle channels, using small upward steps instead of leaving them stuck at `0` or micro-fees forever.
- Seed guardrails: when strong local `7d` outrate/rebalance signals exist, seed influence is capped by profile so external references do not dominate recent local market data; on mature channels without local signal, abrupt seed shocks must repeat before becoming the new anchor.
- Seed refresh is liquidity-aware: manual/idle refresh keeps the raw seed as `reference_ppm`, but nudges the `target_ppm` above seed when the channel is effectively drained and below seed when it is effectively full.
- Low-liquidity seed/no-signal down moves are held: Autofee can still lower toward seed when a channel has usable local liquidity, but avoids entering rescue or cutting fees blindly on drained channels without local out/rebal/HTLC evidence.
- `Rescue`: temporary floor-relax state for structurally weak channels (`close` / `worsening`) that are stuck above local signal due to `peg`, `floor-lock`, or global negative-margin protection.

Market refill mode:
- Uses the `Balanced` target as the main reference, then applies a controlled mode premium.
- Ignores rebalance-derived floors/pressure as primary drivers.
- Keeps outbound intentionally higher and derives inbound discount from the resulting outbound fee.
- Uses optional Amboss skew (`outgoing / incoming`) only as a refinement for how close inbound discount should stay to outbound.

Recent behavior improvements:
- New inbound bootstrap ramps fees in controlled steps for fresh inbound channels (`new-inbound`, `bootstrap`).
- Stalled floor unlock now supports adaptive relaxation (`floor-relax-stall`) to avoid long lock-in at high floors.
- Very small floor-driven increases are held unless signal quality is strong (for example surge/new-inbound), reducing churn.
- Forecast uses effective applied fee (not only raw candidate), improving coherence in `keep` lines.
- Mode switching now snapshots and restores real LND fee policies (`outbound ppm`, `outbound base`, `inbound ppm`, `inbound base`, `time_lock_delta`) when leaving/returning to `Market refill`.

Data windows and fallback rules:
- Main run window: configurable `lookback` (5-21d).
- Extra windows always computed:
- `1d`: short-term movement/stagnation checks.
- `7d`: canonical `out_ppm7d` reference.
- `21d`: fallback only when recent data is missing and quality thresholds are met.
- 21d outrate fallback requires:
- at least `5` forwards and
- outbound amount >= `max(50k sats, 0.5% of channel capacity)`.
- 21d rebal fallback requires rebalanced amount >= `max(30k sats, 0.3% of channel capacity)`.
- If no valid out/rebal signal is available and channel is idle, Autofee avoids blind fee increases (`no-signal-noup`).
- If the only down driver is seed/no-signal and effective local liquidity is low, Autofee holds the current fee instead of forcing rescue or a seed-driven reduction (`seed-liq-down-hold`).

HTLC signal behavior:
- Signal window follows cadence: `max(run_interval, 60m)`.
- Sample/failure thresholds are auto-scaled by node size + node liquidity class.
- Summary line shows: `htlc_liq_hot`, `htlc_policy_hot`, `htlc_forward_hot`, `htlc_low_sample`, `reversal_blocked`, `reversal_confirmed`, `downcap_general`, `downcap_low_sample`, `floor_relax`, `htlc_window`.
- Per-channel line may show: `htlc<window>m a=<attempts> p=<policy_fails> l=<liquidity_fails> f=<forward_fails> u=<unclassified>`.

Automatic calibration:
- Node size class (`small`, `medium`, `large`, `xl`) from total capacity + channel count.
- Node liquidity class (`drained`, `balanced`, `full`) from local ratio.
- Calib line prints: `low_out x<factor> t<...> p<...>`.
- This adjusts low-out thresholds dynamically (for example, less aggressive in balanced nodes, stronger protection when drained).

Autofee Results lines:
- Header: run type + timestamp + operation mode.
- Summary: up/down/flat + skip counters.
- Seed line: native/Amboss/fallback usage.
- Calibration line: node class, liquidity class, low_out factors, revfloor thresholds, HTLC global factors.
- Per-channel line: `set/keep`, `target`, `out_ratio`, `out_ppm7d`, `rebal_ppm7d`, `seed`, `floor`, `margin`, `rev_share`, inbound discount change, tags, HTLC counters, forecast.

Tag glossary (Autofee Results):
- Full reference: `docs/AUTOFEE_TAG_GLOSSARY_EN.md` (EN) and `docs/AUTOFEE_TAG_GLOSSARIO_PT_BR.md` (PT-BR).
- Channel role and trend:
- `sink`, `source`, `router`, `unknown`, `trend-up`, `trend-down`, `trend-flat`.
- Movement controls:
- `stepcap`, `stepcap-lock`, `floor-lock`, `floor-relax-stall`, `reversal-guard`, `reversal-confirmed`, `downcap-general`, `htlc-low-sample-downcap`, `hold-small`, `same-ppm`, `cooldown`, `cooldown-profit`, `cooldown-skip`, `rebal-recent`, `rebal-attempt`, `rebal-recent-noup`.
- Profit and margin controls:
- `neg-margin`, `negm+X%`, `no-down-low`, `no-down-neg-margin`, `global-neg-lock`, `lock-skip-no-chan-rebal`, `lock-skip-sink-profit`, `profit-protect-lock`, `profit-protect-relax`.
- Outrate/floor controls:
- `outrate-floor`, `peg`, `peg-grace`, `peg-demand`, `revfloor`, `sink-floor`, `min`.
- Adaptive controls:
- `circuit-breaker`, `extreme-drain`, `extreme-drain-unlock`, `extreme-drain-turbo`.
- Stagnation and anti-lock controls:
- `stagnation`, `stagnation-rN`, `stagnation-cap-<ppm>`, `normalize-out`, `normalize-rebal`, `stagnation-floor`, `stagnation-floor-relax`, `stagnation-neg-override`, `stagnation-pressure`, `peg-paused-stagnation`.
- Low-out/no-signal controls:
- `low-out-slow-up`, `low-out-noflow-cap`, `no-signal-noup`, `no-signal-floor-relax`, `seed-liq-down-hold`.
- Discovery/explorer:
- `discovery`, `discovery-hard`, `explorer`, `drained-explorer*`, `surge*`.
- HTLC signals:
- `htlc-policy-hot`, `htlc-liquidity-hot`, `htlc-forward-hot`, `htlc-sample-low`, `htlc-neutral-lock`, `htlc-liq+X%`, `htlc-policy+X%`, `htlc-liq-nodown`, `htlc-policy-nodown`, `htlc-neutral-nodown`, `htlc-step-boost`.
- Super-source and inbound:
- `super-source`, `super-source-like`, `new-inbound`, `bootstrap`, `market-refill*`, `inb-<n>`.
- Rescue / targeted floor release:
- `rescue`, `rescue-enter`, `rescue-exit`, `rescue-expired`, `rescue-floor-relax`, `rescue-global-relax`, `rescue-peg-paused`, `rescue-lowliq-block`, `rescue-lowliq-exit`.
- Seed and fallback provenance:
- `seed:native`, `seed:native-corrected`, `seed:amboss`, `seed:amboss-missing`, `seed:amboss-empty`, `seed:amboss-error`, `seed:med`, `seed:vol-<n>%`, `seed:ratio<factor>`, `seed:outrate`, `seed:mem`, `seed:default`, `seed:guard`, `seed:shock-*`, `seed:p95cap`, `seed:absmax`, `seed:outcap`, `seed:rebalcap`, `seed:rebalfloor`, `liq-low`, `liq-high`, `out-fallback-21d`, `rebal-fallback-21d`.

Reading examples:
- Example A (healthy profitable sink):
```text
keep 844 ppm | target 844 | out_ratio 0.21 | out_ppm7d~625 | rebal_ppm7d~513 | floor>=657(peg) | margin~61 | ... outrate-floor peg peg-demand ...
```
Meaning: channel is moving and profitable, floor remains anchored to market/rebalance references, no forced change.

- Example B (high local ratio, idle, no quality signal):
```text
keep 1500 ppm | target 1500 | out_ratio 0.24 | out_ppm7d~0 | rebal_ppm7d~0 | ... low-out-slow-up no-signal-noup no-signal-floor-relax ...
```
Meaning: Autofee detected missing reliable signal and avoided blind upward repricing.

- Example C (stagnation pressure on high local ratio):
```text
keep 1461 ppm | target 1139 | out_ratio 0.35 | ... stagnation normalize-out stagnation-r5 stagnation-cap-1139 stagnation-floor peg-paused-stagnation ...
```
Meaning: stagnation logic is actively trying to normalize down while preventing conflicting peg pressure.

## Node Retirement
Node Retirement is a guided workflow to safely decommission an LND node, close channels in an orderly way, and provide an auditable recovery trail.

Goals:
- Stop new operational activity before channel closure.
- Drain in-flight HTLC pressure before cooperative closes.
- Close what is possible cooperatively, then handle exceptions explicitly.
- Track final on-chain reconciliation and (for succession) transfer recovery status.

Core model:
- The flow is session-based (`manual` or `succession` source).
- Only one active retirement session can run at a time.
- Every step writes events and state to Postgres so progress survives UI refresh/restart.
- A mandatory disclaimer gate exists for manual sessions.

State machine (high level):
- `created`: session accepted.
- `snapshot_taken`: baseline balances/channels captured.
- `quiescing`: best-effort stop of rebalance/autofee + forwarding disable.
- `draining_htlcs`: waits until pending HTLC count reaches zero.
- `ready_for_coop_confirmation`: manual confirmation gate before cooperative close.
- `closing_coop`: cooperative close attempts for eligible channels.
- `awaiting_user_decision`: channels that need operator decision (`wait` vs `force_close`).
- `force_closing`: applies force-close where explicitly approved.
- `monitoring_onchain`: waits for all tracked channels to finish close lifecycle.
- terminal states: `completed`, `dry_run_completed`, `failed`, `canceled`.

Cooperative close fee policy:
- Node Retirement currently calls LND cooperative close with `sat_per_vbyte=0` (LND dynamic estimator/default confirmation target).
- This keeps retirement behavior consistent with LND fee estimation and avoids external fee dependency during decommission.

UI components:
- Disclaimer + session creation panel:
- choose `Dry-run mode (simulate only)` or live run.
- Retirement steps board:
- badge per step (`Completed`, `In progress`, `Pending`).
- Sessions list:
- shows source, run mode, state, timestamps, and last error.
- Initial Snapshot panel:
- baseline at session start (open/pending channels, HTLC count, on-chain and Lightning balances).
- Reconciliation panel:
- final summary when finished (balances/channels) and transfer result when applicable.
- Channel timeline (initial vs current):
- per-channel comparison from captured initial state to latest state (active flags, local/remote balances, pending HTLCs, close mode/txid, decision, errors).
- Session events:
- ordered runtime event trail for diagnostics/audit.
- Cooperative close confirmation modal:
- explicit no-return confirmation gate for manual sessions.
- Channel exception actions:
- per-channel `Wait` / `Force close` decisions for offline/stuck cases.
- Transfer audit (succession-triggered sessions):
- destination, attempts, status badge, txid with explorer link, confirmations, fee policy, timestamps, errors.

Dry-run behavior:
- Simulates the full orchestration path without submitting real cooperative/force closes.
- Skips the manual cooperative-close confirmation gate and advances automatically to the simulated close stage.
- Produces snapshot, channel timeline updates, session events, and final reconciliation as `dry_run_completed`.
- Intended to validate policy + operator understanding before live retirement.

### Succession Mode (dead-man switch)
Succession Mode automates retirement trigger when proof-of-life is not confirmed in time.

Defaults and prerequisites:
- Disabled by default.
- Can only be armed when Telegram `Activity mirror` is enabled in Notifications.
- Uses the same retirement engine with `source=succession`.

Configuration in UI:
- `Enable succession mode`: arms scheduler.
- `Succession dry-run`: when enabled, scheduler-triggered retirement sessions are created as dry-run.
- `External on-chain destination address`: sweep destination for recovered funds.
- `Liveness check interval (days)`: delay after a valid confirmation before reminder window starts.
- `Daily reminder grace window (days)`: deadline window after reminders begin.
- `Min confirmations before auto-transfer`: waits for UTXOs with at least this depth before succession sweep.
- `Auto-transfer fee rate (sat/vbyte)`: if `0`, LND estimates dynamically.
- `Pre-approve FC for offline peers` and `Pre-approve FC for stuck HTLC channels`: exception policy for unattended flows.

Proof-of-life inputs:
- UI button: `I'm alive (UI)`.
- Telegram command/message: `/alive` or `estou vivo`.
- Either path resets `last_alive_at`, `next_check_at`, and `deadline_at`.

Reminder and trigger cycle:
- Scheduler checks succession status every minute.
- Before `next_check_at`: state remains armed.
- Between `next_check_at` and `deadline_at`: sends one Telegram reminder per day.
- After `deadline_at`: triggers Node Retirement automatically (live or dry-run according to succession config).

Simulation controls:
- `Simulate alive`: records liveness confirmation immediately.
- `Simulate not alive`: now triggers an immediate succession retirement session in dry-run for validation.

Operational notes:
- If another retirement session is already active, succession enters waiting mode and retries later.
- Completion status is mirrored in succession state (`retirement_completed` / `dry_run_completed`) and can notify Telegram.
- For live succession runs, auto-transfer monitoring tracks submission and confirmation of the sweep transaction.

## Web terminal (optional)
LightningOS can expose a protected web terminal using GoTTY.

The installer provisions the terminal disabled. The `losop`
account has a locked Linux password, belongs to no privileged supplementary
groups, and receives only the dedicated terminal environment.
Enable or disable it from the LightningOS Terminal page. Fresh administrator
reauthentication is required to enable keyboard input, switch back to view-only
mode, or disable the service. Interactive commands still run as the restricted
`losop` account inside the hardened systemd sandbox. The typed privileged
broker applies each transition and restarts only the terminal service.
You can review the runtime settings in `/etc/lightningos/terminal.env`:
- `TERMINAL_ENABLED=0` (managed by the Terminal page)
- `TERMINAL_CREDENTIAL=user:pass`
- `TERMINAL_ALLOW_WRITE=0` (view-only) or `1` (explicit interactive opt-in)
- `TERMINAL_PORT=7681` (optional)
- `TERMINAL_WS_ORIGIN=^https://.*:8443$` (optional, default allows all origins)
Credential rotation requires fresh LightningOS reauthentication and returns the
new GoTTY password once. It never changes or unlocks the Linux password.

## Taproot Assets (experimental)

Installing **Taproot Assets (tapd)** adds a dedicated page backed by the official daemon and the local LND connection. The current integration is mainnet, alpha, and on-chain only.

- View daemon status and friendly asset balances.
- Discover assets from a universe catalog or sync a universe host manually.
- Use the BRLN one-click sync shortcut and include known/synced assets in the receive picker.
- Mint a new asset, reissue into an existing group, and choose a mempool-based sat/vbyte fee rate.
- Generate a receive address with QR code, decode addresses, preview amount/estimated on-chain cost, send, and redeem.
- LightningOS suppresses Taproot Assets anchor transactions from generic on-chain-send notifications to avoid misleading alerts.
- Standalone `tapd` and Fedimint Lightning Gateway are mutually exclusive because both require LND's HTLC interceptor. Stop/uninstall one before installing the other.

Lightning transfers of Taproot Assets are not enabled in this standalone mode; they depend on the separate community edge-node work.

## Wallet Flow / provenance graph (optional)

The Wallet Flow tab inside On-chain Hub renders a sankey graph of every transaction the wallet has touched. It needs to decode arbitrary txids — non-wallet ancestors and external counterparties — so it picks the first available source from this chain:

1. **Local Bitcoin Core** — preferred source. Auto-enabled when the same `fullIndexAppAvailability` check used by the Electrs/Mempool store apps reports OK (local Bitcoin Core + non-pruned + `txindex=1` synced). Bitcoin Core from the App Store already seeds `txindex=1`. When Core is reachable but txindex isn't ready, the Wallet Flow tab surfaces a one-time banner suggesting `txindex=1` and the chain falls through.
2. **Local electrs** — fallback for installs that already run electrs (e.g. Sparrow) or that keep Bitcoin Core pruned. `ELECTRUM_RPC_ADDR` (default `127.0.0.1:50001`). Address forms: `host:port` (plain TCP, default), `host:port:t` (TCP explicit), `host:port:s` (TLS, standard cert verification).
3. **Public Electrum servers** — mainnet-only, default-on with two BRLN-operated servers (`electrum.pagcoin.org:50002:s` and `electrum.br-ln.com:50001:t`). Override with `PROVENANCE_PUBLIC_ELECTRUM=host:port[:s|:t][,host:port[:s|:t]]` or disable entirely with `PROVENANCE_PUBLIC_ELECTRUM=disabled`.

> **Privacy warning**: when the chain reaches a public Electrum step, the txids your wallet asks about become visible to the operator. The Wallet Flow tab renders the source badge in amber for that reason. Set `PROVENANCE_PUBLIC_ELECTRUM=disabled` in `/etc/lightningos/secrets.env` if your threat model requires it.

Optional pins in `/etc/lightningos/secrets.env`:
- `PROVENANCE_PRIMARY=chain|bitcoind|electrs` — choose the provenance source policy. Default `chain` keeps `bitcoind -> electrs -> public`; `bitcoind` and `electrs` force a single local source with no fallback.
- `ELECTRUM_RPC_ADDR=host:port[:s|:t]` — point at a non-default electrs (e.g. a remote one over Tailscale).
- `PROVENANCE_PUBLIC_ELECTRUM=disabled` — opt out of the public fallback.
- `PROVENANCE_PUBLIC_ELECTRUM=host:port[:s|:t][,...]` — replace the default public list.
- `PROVENANCE_NETWORK=mainnet|testnet|signet|regtest` — gates public servers. Auto-detected from `/data/lnd/data/chain/bitcoin/<network>/wallet.db` if unset; public servers are skipped on non-mainnet.

The active backend is reported by `GET /api/onchain/provenance/health` in the `backend` field and rendered as a badge next to the Wallet Flow tab title. Source-chain telemetry is exposed at `GET /api/onchain/provenance/metrics` with per-source-class hits, errors, fallthroughs, and recent p95 latency since process start.

Daily report rows also include provenance freshness fields when the report is generated for the previous local day: `provenance_last_sync_at`, `provenance_last_sync_age_hours`, `provenance_health_alert`, and `provenance_last_error`. The alert flag is raised when provenance has never synced, the last sync is older than 24 hours, or the last sync recorded an error.

## Security notes
- The seed phrase is never stored. It is displayed once in the wizard.
- RPC credentials are stored only in `/etc/lightningos/secrets.env` (root:lightningos, `chmod 660`).
- Release `0.5.2` still gives the manager a root-equivalent host boundary: the
  service belongs to the `docker` group and its passwordless sudo policy allows
  unrestricted arguments for package, Docker, `systemd-run`, and UFW commands.
  Login, TLS, CSRF, and LAN/VPN firewalling do not contain a compromised manager
  process. The replacement broker and privilege-removal work is tracked in
  [`docs/32_PRIVILEGE_HARDENING_PLAN.md`](docs/32_PRIVILEGE_HARDENING_PLAN.md).
- API/UI bind to `0.0.0.0` by default for LAN access. If you want localhost-only, set `server.host: "127.0.0.1"` in `/etc/lightningos/config.yaml`.
- Set `UTXO_LOCK_REQUIRES_REAUTH=true` to require the same wallet-send reauth scope before UTXO lock/unlock actions. This is optional because lock/unlock are reversible, but useful on shared admin sessions.
- Sensitive API actions are recorded in Postgres `audit_events` and can be reviewed in the UI under `Audit`.
- Audit events are pruned after `AUDIT_EVENTS_RETENTION_DAYS` days by default (`365`). Set `AUDIT_EVENTS_RETENTION_DAYS=0` or `forever` in `/etc/lightningos/secrets.env` to keep them indefinitely.
- Public Electrum fallback improves Wallet Flow coverage but reveals queried txids to the selected server; disable it when that does not fit your privacy model.

## Troubleshooting
If `https://<SERVER_LAN_IP>:8443` is not reachable:
```bash
systemctl status lightningos-manager --no-pager
journalctl -u lightningos-manager -n 200 --no-pager
ss -ltn | grep :8443
```

### App Store catalog

The backend registry currently exposes 19 apps/services. App files are managed by LightningOS, persistent data is kept separately, and Docker is installed only when an app needs it.

| App | Purpose and current integration |
| --- | --- |
| **Bitcoin Core** | Local mainnet node in Docker. Defaults to `/data/bitcoin`, supports a pre-mounted custom storage target at install time, and seeds `txindex=1` for full-index consumers. |
| **Bark Wallet** | Beta self-custodial Ark, Lightning, and on-chain wallet served over local HTTPS with LightningOS-managed login. Uses Second's public mainnet Ark operator, does not use local LND, and preserves wallet data on uninstall. |
| **Electrs** | Electrum indexer for local full-index Bitcoin Core; exposes TCP `50001`, local metrics on `127.0.0.1:4224`, and indexing/sync progress in the store. |
| **Mempool** | Self-hosted mempool.space stack on port `8999`; requires local Bitcoin Core and Electrs installed, running, and ready. |
| **Fedimint Guardian** | `fedimintd` for solo or multi-guardian federations over Iroh, using the active Bitcoin backend. |
| **Fedimint Lightning Gateway** | Independent `gatewayd lnd` gateway that connects local LND to Fedimint federations over Iroh. It cannot run together with standalone Taproot Assets because both require the LND HTLC interceptor. |
| **LNDg** | Advanced LND analytics and automation in Docker, available on port `8889`, with managed admin credentials and local LND integration. |
| **LNbits** | Lightning accounts/wallet and extension platform funded by local LND. |
| **BTCPay Server** | Self-hosted Bitcoin and Lightning payment processor integrated with the local node stack. |
| **Elements** | Native Liquid Elements service with RPC on `127.0.0.1:7041`, selectable local/remote Bitcoin mainchain, local-node detection, and an optional pre-mounted custom data directory. |
| **Peerswap** | Native `peerswapd` plus `psweb` on port `1984`; uses either local Elements or a tested remote Elements RPC source, with a node-specific wallet in remote mode. |
| **RoboSats Gateway** | Self-hosted RoboSats client for P2P Bitcoin trading over Tor, pinned to a tested release and exposed through the LightningOS HTTPS proxy. |
| **Public Pool** | Self-hosted solo-mining pool backend and web UI with local or remote Bitcoin RPC support. |
| **CPU Lottery Miner** | Optional solo CPU miner against the local Public Pool. Thread count is adjustable from the UI and any block reward goes directly to the LND wallet address. |
| **Buy DePix** | Integrated PIX-to-DePix checkout with quote/order creation, status tracking, and a dedicated UI page. |
| **FSwap** | Pays Brazilian boletos and bills with sats from the local Lightning node through the dedicated Pay Boleto flow. |
| **Taproot Assets (tapd)** | Official standalone `tapd` connected to local LND for on-chain asset discovery, universe sync, mint/reissue, receive, send preview/send, and redeem. Experimental mainnet alpha; Lightning asset transfers require the separate community edge-node work. |
| **Lightning Loop** | Official Lightning Labs swap client with LightningOS-managed installation and local LND integration. |
| **Loop Out BR⚡LN** | Native outbound-liquidity workflow that splits a target into controlled Lightning Address payments, preserves a configurable local-balance floor, and keeps job, payment, and event history in LightningOS. |

Fedimint details are documented in the [Fedimint configuration guide](docs/28_FEDIMINT_CONFIGURATION_EN.md) ([PT-BR](docs/27_FEDIMINT_CONFIGURATION_PT_BR.md)).

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

## Changelog
Release-by-release notes are tracked in GitHub Releases:
- https://github.com/jvxis/brln-os-light/releases

## Development
See `DEVELOPMENT.md` for local dev setup and build instructions.

## License
Licensed under the MIT License. See `LICENSE` for the canonical text and `LICENSE.pt-BR.md` for an informational PT-BR translation.

## Systemd
Templates are in `templates/systemd/`.

## Rebuild only (manager/broker/UI)
Use this only for development or recovery on an already installed node. A
`git pull`, a checkout, or rebuilding only the manager does **not** perform a
LightningOS upgrade. Normal upgrades must use the UI or the release upgrade
procedure so that the manager, privileged broker, systemd units, UI, and system
integrations remain on the same version.

Run the commands below from the `lightningos-light/` application directory.

Rebuild manager:
```bash
sudo /usr/local/go/bin/go build -o dist/lightningos-manager ./cmd/lightningos-manager
sudo install -m 0755 dist/lightningos-manager /opt/lightningos/manager/lightningos-manager
```

Rebuild and reinstall the privileged broker:
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

Install and verify the broker before restarting a manually rebuilt manager.
These commands assume that the LightningOS system users and groups already
exist; they are not a replacement for an initial installer or a full upgrade.

Rebuild UI:
```bash
cd ui && sudo npm install && sudo npm run build
cd ..
sudo rm -rf /opt/lightningos/ui/*
sudo cp -a ui/dist/. /opt/lightningos/ui/
```


