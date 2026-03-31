# LightningOS Light

<img width="1920" height="1080" alt="BRLN-logo" src="https://github.com/user-attachments/assets/7394bf7b-2515-461a-8b80-7488531c7f40" />

[Clique aqui](https://github.com/jvxis/brln-os-light/blob/main/docs/README-PT-BR.md) para ver a versão em PT-BR

[Click here](https://github.com/jvxis/brln-os-light/blob/main/docs/16_INSTALL_RASPBERRYPI5_DEBIAN12.md) Raspberry Pi 5 + Debian 12

LightningOS Light is a Full Lightning Node Daemon Installer, Lightning node manager with a guided wizard, dashboard, and wallet. The manager serves the UI and API over HTTPS on `0.0.0.0:8443` by default for LAN access (set `server.host: "127.0.0.1"` for local-only) and integrates with systemd, Postgres, smartctl, Tor/i2pd, and LND gRPC.
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
- Wizard for Bitcoin RPC credentials and wallet setup
- Lightning Ops suite: peers/channels, Rebalance Center, Autofee, Channel Ranking, Node Retirement, HTLC signals, and Channel Auto Heal
- Keysend Chat: 1 sat per message + routing fees, unread indicators, 30-day retention
- Real-time notifications (on-chain, Lightning, channels, forwards, rebalances)
- Telegram notifications: SCB backups, financial summaries, on-demand `/scb` and `/balances`
- Daily routing reports (timer + backfill + live API)
- App Store: LNDg, Peerswap (psweb), Elements, Bitcoin Core
- Bitcoin Local management (status + config) and logs viewer

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

Run the installer that matches your environment:
```bash
cd lightningos-light

# Existing node on x86_64/amd64
sudo ./install_existing.sh

# Existing node on Raspberry Pi 4 (arm64)
sudo ./install_existing_pi.sh
```

Access the UI from another machine on the same LAN:
`https://<SERVER_LAN_IP>:8443`

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

## Lightning Ops (feature map)
- Channel management: peer/channel controls, policy updates, and channel card/balance refinements.
- Channel Ranking: per-channel score, recommended state, 7d vs 30d comparison, actionable recommendations, and links into Autofee, Rebalance, HTLC Manager, and Close Manager.
- Rebalance Center: manual + auto rebalances with score-based targeting, watchdogs, pre-probing, ROI guardrails, and optional manual auto-restart.
- Autofee: per-channel fee automation with cost anchors, Amboss seeding, HTLC signal integration, calibration by node size/liquidity, scheduler/manual runs, and detailed run history.
- Node Retirement: guided safe node decommission workflow with session timeline, cooperative close controls, exception handling, and on-chain reconciliation.
- HTLC Manager: hysteresis-based HTLC telemetry used by Autofee and liquidity decisions.
- Channel Auto Heal + Tor peers checker: operational guardrails for peer/channel reliability.
- Health checks: optional follow-bitcoin checks for LND/node health workflows.

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
- `Min probe amount (sats)` (`min_probe_sat`, default `0`): minimum amount allowed during route probing when split is enabled. `0` falls back to `Minimum (sats)`.
- `Min execute amount (sats)` (`min_execute_sat`, default `0`): minimum amount allowed to be actually sent when split is enabled. `0` falls back to `Minimum (sats)`.
Key interactions:
- Attempts still start anchored by `Minimum (sats)` (legacy-compatible behavior).
- If split is enabled, probing can go down to `min_probe_sat` and execution is blocked below `min_execute_sat`.
- With split enabled, auto candidates below execute floor are skipped early.
Practical recommendation:
- Keep `Min execute amount (sats)` equal to `Min probe amount (sats)` unless you explicitly want to allow probing lower than execution.
- Use split when you want broader route discovery without opening execution below your chosen floor.

MSPR (`MSPR (Multi-Source Parallel)`):
- Purpose: increase first-pass success chance by trying shards across multiple source channels in parallel before legacy sequential fallback.
- `Enable MSPR` (`mpp_enabled`, default `false`): enables MSPR prepass execution.
- `MSPR for auto jobs only` (`mpp_auto_only`, default `false`): when enabled, only auto jobs use MSPR; manual jobs stay legacy.
- `Max shards` (`mpp_max_shards`, default `8`, range `1..20`): max number of shards planned for the MSPR round.
- `Parallel workers` (`mpp_parallelism`, default `6`, range `1..max_shards`): max concurrent shard attempts in the round.
- `Min shard amount (sats)` (`mpp_min_shard_sat`, default `1000`): minimum shard size planned by MSPR.
- `Round timeout (sec)` (`mpp_round_timeout_sec`, default `30`): max duration for one MSPR round before fallback to legacy attempts.
Execution model:
- MSPR runs one parallel prepass (using shard plan + workers).
- Successful shards are executed and accounted immediately.
- After the prepass, the job continues in the same legacy queue/attempt flow for remaining amount.
- Failed shard attempts appear in history with `mpp shard:` reason prefix.
Practical recommendation:
- Start with `max_shards=8`, `parallel_workers=6`, `min_shard=1000`, `round_timeout=30`.
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

It uses local routing/rebalance history (Postgres notifications), optional Amboss seed data, HTLC failure signals, node-size/liquidity calibration, and explainable guardrails.

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
- `Amboss fee reference`: optional external seed source.
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
- Seed (`Amboss` -> memory/outrate/default fallbacks).
2. Classify channel behavior (`sink`, `source`, `router`, `unknown`) and liquidity state.
3. Compute a raw target using seed, out ratio, trend/margin rules, HTLC pressure, and profitability heuristics.
4. Apply mode-specific controls (discovery/explorer/stagnation/profit-protect/global locks).
5. Build floor stack (`rebal`, `rebal-sink`, `outrate`, `peg`, `revfloor`, `stagnation`, `no-signal`).
6. Apply step caps and cooldown, then decide `apply` vs `keep`.

Flow diagram:
- `docs/AUTOFEE_FLOW_DIAGRAM_EN.md`

Balanced mode additions:
- Dynamic upward cooldown by effective `outnorm`: very drained channels can raise faster without fully bypassing cooldown.
- `Drained explorer`: a dedicated exploratory mode for very empty and idle channels, using small upward steps instead of leaving them stuck at `0` or micro-fees forever.
- Seed guardrails: when strong local `7d` outrate/rebalance signals exist, Amboss seed influence is capped by profile so external references do not dominate recent local market data.
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
- Seed line: Amboss/fallback usage.
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
- `outrate-floor`, `peg`, `peg-grace`, `peg-demand`, `revfloor`, `sink-floor`.
- Adaptive controls:
- `circuit-breaker`, `extreme-drain`, `extreme-drain-unlock`, `extreme-drain-turbo`.
- Stagnation and anti-lock controls:
- `stagnation`, `stagnation-rN`, `stagnation-cap-<ppm>`, `normalize-out`, `normalize-rebal`, `stagnation-floor`, `stagnation-floor-relax`, `stagnation-neg-override`, `stagnation-pressure`, `peg-paused-stagnation`.
- Low-out/no-signal controls:
- `low-out-slow-up`, `low-out-noflow-cap`, `no-signal-noup`, `no-signal-floor-relax`.
- Discovery/explorer:
- `discovery`, `discovery-hard`, `explorer`, `drained-explorer*`, `surge*`.
- HTLC signals:
- `htlc-policy-hot`, `htlc-liquidity-hot`, `htlc-forward-hot`, `htlc-sample-low`, `htlc-neutral-lock`, `htlc-liq+X%`, `htlc-policy+X%`, `htlc-liq-nodown`, `htlc-policy-nodown`, `htlc-neutral-nodown`, `htlc-step-boost`.
- Super-source and inbound:
- `super-source`, `super-source-like`, `new-inbound`, `bootstrap`, `market-refill*`, `inb-<n>`.
- Rescue / targeted floor release:
- `rescue`, `rescue-enter`, `rescue-exit`, `rescue-expired`, `rescue-floor-relax`, `rescue-global-relax`, `rescue-peg-paused`.
- Seed and fallback provenance:
- `seed:amboss`, `seed:amboss-missing`, `seed:amboss-empty`, `seed:amboss-error`, `seed:med`, `seed:vol-<n>%`, `seed:ratio<factor>`, `seed:outrate`, `seed:mem`, `seed:default`, `seed:guard`, `seed:p95cap`, `seed:absmax`, `seed:outcap`, `seed:rebalcap`, `seed:rebalfloor`, `out-fallback-21d`, `rebal-fallback-21d`.

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

## Changelog
Release-by-release notes are tracked in GitHub Releases:
- https://github.com/jvxis/brln-os-light/releases

## Development
See `DEVELOPMENT.md` for local dev setup and build instructions.

## License
Licensed under the MIT License. See `LICENSE` for the canonical text and `LICENSE.pt-BR.md` for an informational PT-BR translation.

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


