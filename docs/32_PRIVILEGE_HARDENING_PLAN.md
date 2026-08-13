# LightningOS privilege hardening plan

## Status

Accepted for implementation. No runtime privilege changes are made by this
document.

Date: 2026-08-10.

Tracking issues:

- privilege-boundary hardening: [#32](https://github.com/jvxis/brln-os-light/issues/32);
- installer and upgrade supply-chain hardening:
  [#34](https://github.com/jvxis/brln-os-light/issues/34).

Target release: `0.5.3`.

Implementation branch: `agent/0.5.3-privilege-hardening`.

Persistent continuation checkpoint:
`docs/36_PRIVILEGE_HARDENING_HANDOFF.md`. The checkpoint is an operational
index for resuming work in another session; this plan remains the authority for
scope and completion.

All privilege-hardening implementation commits must remain on this branch until
the complete cutover and regression matrix is ready for review. Release `0.5.2`
continues on `main` without partial broker or privilege-removal changes.

## Objective

Keep the LightningOS Manager unprivileged even when it manages LND, host
services, upgrades, the firewall, and the Docker-based App Store. A compromise
of the HTTPS manager must not provide a general-purpose path to root or to the
Docker socket.

Security takes priority over retaining every legacy `install_existing.sh`
layout. Existing nodes must nevertheless fail safely: an unsupported layout is
reported before permissions are removed or services are changed.

## Current risk

The installers grant the manager passwordless commands with unrestricted
arguments for `apt`, `apt-get`, `dpkg`, `docker`, `docker-compose`,
`systemd-run`, and `ufw`. The manager service is also a member of the `docker`
group. These are root-equivalent capabilities and substantially increase the
impact of a future manager or dependency compromise.

Authentication, TLS, CSRF protection, sensitive-action reauthentication, and
LAN/Tailscale firewall rules reduce exposure. They do not contain a process
that has already been compromised.

## Target architecture

### Unprivileged manager

`lightningos-manager` will:

- have no membership in the `docker` group;
- have no direct access to the Docker socket;
- have no wildcard sudo rules;
- never construct a privileged shell command;
- request privileged operations through a versioned, typed interface;
- retain only the files and LND credentials required for manager functions.

### Privileged broker

A root-owned helper, provisionally
`/usr/local/libexec/lightningos-privileged`, will expose a deliberately small
set of operations. It may initially be invoked through one exact sudoers entry;
the preferred final interface is a root systemd service on a protected Unix
socket with peer credential checks.

The broker must:

- accept an explicit operation name, not an executable name;
- validate every enum, identifier, CIDR, port, version, and path;
- use fixed executable paths and argument arrays without `/bin/sh -c`;
- reject symlinks and paths outside owned roots;
- use atomic writes with explicit owner and mode;
- emit structured audit events without secrets;
- apply timeouts and serialize package and upgrade operations;
- default to denial for unknown operations or versions.

Initial operation families:

| Family | Examples | Required constraints |
|---|---|---|
| Services | restart LND, manager, Postgres; reboot; poweroff | Fixed unit/action allowlist; sensitive reauthentication remains in the manager |
| Files | install config, certificate, service, and app-owned files | Fixed destination roots, owner, group, mode, and content type |
| Firewall | apply manager LAN/Tailscale policy | Valid CIDR/interface; fixed ports; no arbitrary UFW arguments |
| Packages | install a declared feature dependency set | Feature ID maps to a root-owned package allowlist; no caller-supplied package names |
| Apps | install/start/stop/uninstall a catalog app | Known app ID and root-owned manifest; fixed mounts, ports, images, and data roots |
| Upgrades | upgrade LND, Tor, or LightningOS | Allowed source, channel, version format, checksum/signature, and destination |
| Storage | prepare an approved app or blockchain target | Canonical path checks; no traversal, symlink, or arbitrary ownership changes |

### App Store isolation

The broker, rather than the manager, will own Docker access. App definitions
used by the broker must not be supplied or modified by an API request. Each app
manifest will declare:

- allowed image and version source;
- exact host paths and whether each mount is read-only;
- allowed ports, networks, capabilities, and restart policy;
- required LND/Bitcoin/Elements credentials;
- install, health, start, stop, and uninstall behavior.

The catalog audit must reject privileged containers, host PID/network modes,
the Docker socket, broad host mounts, and unexpected devices unless an explicit
security exception is documented and tested.

## Delivery phases

### Phase 0 — Inventory and safety rails

Status: completed on 2026-08-10. The reviewed call-site/replacement matrix,
safety-rail design, and T0 findings are recorded in
`docs/33_PRIVILEGE_HARDENING_PHASE0_INVENTORY.md`; the secret-free machine
capture is stored in `docs/baselines/privilege-hardening-t0-2026-08-10.json`.

1. Inventory every `RunCommandWithSudo`, `runSystemd`, Docker, package,
   firewall, ownership, service, and root-file write call site.
2. Map each call to a proposed broker operation and owning feature.
3. Add tests that fail if new wildcard sudo rules, direct Docker access, or
   caller-controlled privileged shell commands are introduced.
4. Correct security documentation so the current root-equivalent boundary is
   explicit.

Exit criterion: every current privileged call has an owner, replacement, and
test strategy.

### Phase 1 — Broker foundation

Status: implementation and Phase 1 acceptance completed on 2026-08-10.
Protocol version 1, the
deny-by-default broker, root-only audit and lock files, manager client modes,
installer self-test, and the first `service.restart` family are implemented.
The integration node began from `0.5.2-Beta`; the helper was installed and
exercised in `shadow` on 2026-08-10 with manager, LND, and Postgres remaining
active. The secret-free result is stored in
`docs/baselines/privilege-hardening-phase1-shadow-2026-08-10.json`. The phase
now includes the first fixed-file operation, `files.enable_login`, which has no
caller-controlled path or content and atomically targets only the installed
manager config. Its shadow validation passed on 2026-08-10: the dry-run was
accepted, caller-controlled path/content fields were rejected, the config hash
was unchanged, and no mutation lock was created. The secret-free evidence is
stored in
`docs/baselines/privilege-hardening-phase1-files-shadow-2026-08-10.json`.
The soak and explicit review passed without moving the integration node from
`shadow`. The review found and fixed runtime-directory recreation after reboot
and auditable manager self-restarts. A real reboot plus service status,
PostgreSQL restart, manager restart, negative requests, health checks, and
rollback passed on the disposable clone. Evidence is stored in
`docs/baselines/privilege-hardening-phase1-soak-reboot-service-2026-08-10.json`.
The integration node remains in `shadow`; its `enforce` cutover is deliberately
deferred while later operation families are migrated. The protocol and
rollback contract are recorded in `docs/34_PRIVILEGED_BROKER_PROTOCOL.md`.

A clean Ubuntu 24.04 VirtualBox clone of `brln-os-basica` then passed the full
new-install gate on 2026-08-10. The exact branch checkout installed
successfully, the manager started in `enforce`, and the real
`POST /api/auth/enable-login` path performed one audited fixed-file mutation.
The config owner/group/mode and root-only mutation lock were verified, then the
rollback restored the original config hash and `disabled` mode. The disposable
VM was powered off after validation. Secret-free evidence is stored in
`docs/baselines/privilege-hardening-phase1-fresh-install-enforce-2026-08-10.json`.

1. Implement the broker protocol, authorization, validation, structured logs,
   locking, and timeouts.
2. Add a client package used by the manager.
3. Migrate low-risk service and fixed-file operations first.
4. Keep the old path available only behind an explicit temporary compatibility
   flag.

Exit criterion: negative tests prove that arbitrary executables, shell syntax,
paths, units, and arguments are rejected.

### Phase 2 — App Store and Docker cutover

Phase 2 has started with the complete 20-app privilege inventory in
`docs/35_PRIVILEGE_HARDENING_PHASE2_APP_INVENTORY.md` and the first typed
manifest slice. CPU Miner `start` and `stop` passed the disposable Ubuntu 24.04
`enforce` gate after the gate exposed and repaired a client/Compose timeout
mismatch. The app was removed after the test, the broker mode returned to
`disabled`, and the integration node was not changed. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-cpuminer-enforce-2026-08-10.json`.
The next typed slice adds CPU Miner status and CPU metrics inspection through
the broker. In `enforce`, the manager receives only `running`/`stopped` and a
raw CPU percentage; hashrate/shares remain on the unprivileged localhost miner
API. The disposable Ubuntu 24.04 gate passed with the manager's Docker
supplementary group temporarily removed: dedicated status, App Store status,
typed start/stop, negative requests, and manifest tampering behaved as
designed. The exact config and Docker group were restored, the app was removed,
and Docker was left inactive. CPU Miner thread and pool/address/worker updates
then passed a separate `enforce` gate without the Docker GID in the manager;
the broker rejected an invalid retained pool mode. That gate exposed Compose's
ten-second grace period during config-driven recreation, so the exact catalog
manifest now fixes `stop_grace_period: 2s`. CPU Miner uninstall has also moved
to the typed broker and passed an `enforce` gate with both the service user's
Docker membership and the running manager process's Docker GID removed. The
manager's direct Docker access was denied, while typed start and uninstall
succeeded and left no app files or Compose containers. Lifecycle start now
fails closed before Compose if the selected allowlisted image is not already
local, preventing an implicit pull from crossing the synchronous broker
deadline. CPU Miner install now uses typed Docker-runtime readiness, asynchronous
image preparation/status, fixed compatibility probes, and the brokered
first-container creation when Docker is already installed. Its Ubuntu 24.04
gate completed a 90-second pull plus install/start/stop/uninstall with the live
manager lacking the Docker GID; all transient pull units were collected.
At this historical CPU Miner checkpoint, the remaining apps, Ubuntu 26
execution, and final Docker-group removal were still open. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-cpuminer-inspect-enforce-2026-08-10.json`
and
`docs/baselines/privilege-hardening-phase2-cpuminer-config-enforce-2026-08-10.json`,
plus
`docs/baselines/privilege-hardening-phase2-cpuminer-remove-enforce-2026-08-10.json`
and
`docs/baselines/privilege-hardening-phase2-cpuminer-image-install-enforce-2026-08-10.json`.

The next shared slice moved CPU Miner's remaining Docker package installation
behind `packages.feature.ensure/status`. The manager can select only the
closed `docker_runtime` feature; the broker maps it to `docker.io` and
`docker-compose-v2` on Ubuntu 24.04/26.04, executes fixed asynchronous
index/install units with a package lock and 15-minute bounds, and reports a
small staged state machine. Local contract tests and a harmless Ubuntu 24.04
transient-unit check passed. A clean `brln-os-basica` clone then passed the full
`enforce` API gate starting without Docker: both packages installed, the units
were collected, CPU Miner reached `running`, stop/start/uninstall returned 200,
and no app files or containers remained. The manager user and live process had
no Docker GID, direct Docker access failed, and an injected package list was
rejected. `LOS-TEST2` was untouched and the successful gate clone was powered
off. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-docker-package-install-enforce-2026-08-10.json`.

The package catalog now also admits the fixed `mdns` feature, mapped only to
`avahi-daemon` and `libnss-mdns` with its own index/install units. The same
Ubuntu 24.04/26.04 OS gate, fixed dpkg query, package lock, async state machine,
runtime ceiling, and strict caller-field rejection apply. Ubuntu 24.04 passed a
real service-user dry-run/status gate without invoking apt or changing package,
service, audit, lock, broker, firewall, or network state. Evidence is in
`docs/baselines/privilege-hardening-phase3-mdns-package-2026-08-13.json`.
This closes the package prerequisite for decomposing the system-integration
reconciler. The reconciler cut is now complete: the broker accepts only four
compiled asset IDs whose bytes must match compiled SHA-256 values and whose
destinations/modes are fixed. Separate typed stages apply the exact
TLS/mDNS/firewall helpers and idempotent LND restart policy, enroll an existing
Bitcoin Store data directory through `app.bitcoincore.storage.ensure` without
restarting Bitcoin, restart only an already-active terminal when its helper
changed, and write the v5 completion marker last. Marker readiness is itself a
read-only broker operation that validates root ownership, mode, content and
all four installed asset hashes. The old staged root shell
reconciler and its manager `runSystemd` call were deleted. Ubuntu 24.04 passed
the Linux/root filesystem test and real service-user broker dry-runs; altered
asset bytes and a caller-supplied path failed closed, while package, lock,
firewall and Manager/LND/Postgres activation state remained unchanged. Real
TLS/firewall mutation is reserved for the final disposable fresh-install
matrix. Evidence is in
`docs/baselines/privilege-hardening-phase3-system-integrations-2026-08-13.json`.

The first shared Compose catalog now admits RoboSats for typed status,
start, and stop. The broker byte-validates the manager-owned catalog files and
TLS key pair, then atomically maintains a persistent root-only execution tree
under `/var/lib/lightningos-privileged/apps/robosats`. RoboSats data moved from
a manager-writable host bind to a Docker named volume. A disposable Ubuntu
24.04 gate passed two stop/start cycles, broker status, and HTTPS 200 while the
live manager lacked the Docker GID and direct Docker access returned failure.
The gate rejected an earlier temporary-snapshot design before the persistent
model was adopted. At that gate, install/image preparation, UFW, and uninstall
remained on the reviewed legacy path. `LOS-TEST2` was not mutated. Evidence is
stored in
`docs/baselines/privilege-hardening-phase2-robosats-lifecycle-enforce-2026-08-10.json`.

RoboSats install and image preparation have now joined that typed boundary.
The manager selects only the closed `client`, `tor`, and `proxy` variants; the
broker maps them to three pinned images and fixed asynchronous transient units.
The install path reuses typed Docker package/runtime readiness, writes only the
manager-owned catalog assets, and delegates first-container creation to the
typed lifecycle. The Ubuntu 24.04 `enforce` gate began with those images and
containers removed, completed install in 15.6 seconds, returned HTTPS 200, and
passed stop/start/final-stop while the live manager lacked the Docker GID. An
extra caller-supplied image field was rejected and all pull units were
collected. The pre-existing named volume and firewall rule were retained, so
RoboSats UFW and uninstall remain open. The gate VM was powered off, its prior
roots remain as recoverable backups, and `LOS-TEST2` was untouched. Evidence is
stored in
`docs/baselines/privilege-hardening-phase2-robosats-image-install-enforce-2026-08-10.json`.

RoboSats external firewall access has also moved to the broker. The new closed
`app.firewall.ensure` request contains only `app_id`; the catalog fixes TCP port
`12596`, and the broker fixes `/usr/sbin/ufw status` plus the exact `allow`
command. The disposable Ubuntu 24.04 gate passed the inactive no-op and an
active-UFW run protected by a temporary SSH rule. Typed start created the IPv4
and IPv6 `12596/tcp` rules, external HTTPS returned 200, and a caller-supplied
`port: 22` was rejected with a sanitized response. The app was stopped, both
temporary rule sets were removed, UFW returned to inactive, and the VM was
powered off. The legacy global UFW sudo wildcard remains for other unmigrated
apps; RoboSats itself now has only uninstall open. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-robosats-firewall-enforce-2026-08-10.json`.

RoboSats uninstall now completes the app's current typed contract. The existing
`app.compose.remove` operation admits RoboSats through the closed catalog,
validates the exact Compose/Caddy/TLS assets, executes fixed
`down --remove-orphans --timeout 2`, and deliberately omits `--volumes` to
preserve the legacy data-retention behavior. The broker removes its root-owned
execution tree only after Compose succeeds; the manager then removes its own
catalog files. The Ubuntu 24.04 `enforce` gate returned HTTP 200 in 622 ms,
removed three stopped containers and the project network, removed both roots,
and preserved all five named volumes. A marker in the data volume survived and
was cleaned after verification. A caller-supplied `volumes: true` field was
rejected with a sanitized response. The gate VM was powered off and `LOS-TEST2`
was untouched. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-robosats-remove-enforce-2026-08-10.json`.

Bitcoin Core migration has started with its independently testable runtime and
image boundary. The first gate pinned `bitcoin/bitcoin:31.1`, but the
subsequent provenance review confirmed that Docker Hub describes that image as
unofficial. That initial gate remains as historical evidence but is
superseded before any Bitcoin lifecycle or persistent data use.

The closed `bitcoincore/node` variant now maps only to a local
`lightningos/bitcoin-core:31.1` image. The broker downloads the official 31.1
Linux archive, `SHA256SUMS`, signature bundle, and seven fingerprint-pinned
Guix builder keys; it requires at least three distinct valid signatures,
checks the exact architecture hash, and builds on a Debian base pinned by
platform digest with network disabled for Docker build steps. Readiness also
requires a root-only attestation whose Docker image ID matches the local
image. An unattested same-tag image and every mismatch fail closed, and the
manager has no registry fallback outside broker `enforce` mode.

The disposable Ubuntu 24.04 gate observed seven valid signatures, produced a
root-owned `0600` attestation, reported Bitcoin Core and `bitcoin-cli` 31.1.0,
and ran bitcoind as UID 101. Isolated `regtest` smoke tests passed RPC, both ZMQ
publishers, and a real P2P handshake between two nodes on an internal network.
All temporary containers, networks, and files were removed; the verified image
and attestation were preserved, the unofficial image was removed, no App Store
root or blockchain data was created, the gate VM was powered off, and
LOS-TEST2 was untouched. Storage identity/configuration, typed Compose
lifecycle, mainnet firewall, and dependent-app contracts remain open. The
superseded pin-only gate is recorded in
`docs/baselines/privilege-hardening-phase2-bitcoincore-image-enforce-2026-08-10.json`;
the official-provenance gate is recorded in
`docs/baselines/privilege-hardening-phase2-bitcoincore-official-image-enforce-2026-08-10.json`.

Bitcoin Core storage enrollment now uses the typed
`app.bitcoincore.storage.ensure` operation. The broker accepts only one
canonical data path, rejects system paths, symlinks, root-filesystem fallback
for custom storage, insufficient space, and target changes after enrollment,
then generates and retains the identity in its root-only tree. The manager no
longer supplies the identity or invokes Docker root containers to write/remove
the storage marker. The disposable Ubuntu 24.04 gate passed dry-run, real and
idempotent enrollment, ownership/mode checks, negative requests, audit privacy,
and complete cleanup without starting Bitcoin or creating blockchain data.
Evidence is stored in
`docs/baselines/privilege-hardening-phase2-bitcoincore-storage-enforce-2026-08-10.json`.

Secret-bearing `bitcoin.conf` handling now uses four separate typed broker
operations: `app.bitcoincore.config.ensure`, `.read`, `.write`, and the
read-only `app.bitcoincore.credentials.read`. The caller
supplies only the enrolled `data_dir` and, for mutations, bounded config
content; it cannot select the destination filename. The broker verifies the
root-only enrollment metadata and storage marker, rejects symlinks, reads only
the fixed config, and commits updates atomically as `root:101` mode `0640`.
The manager's root-container read/write ladder and manager-side temporary
secret file have been removed. Existing `101:101` configs are tightened
atomically without changing their contents, and the old manager-owned seed is
used only to preserve credentials before being removed after broker success.

Fresh App Store installs no longer generate `rpcuser`/`rpcpassword` in
`bitcoin.conf`. The manager supplies a credential-free standard template and
the broker generates a Bitcoin Core-compatible `rpcauth`, stores only its hash
in the config, and retains the recoverable credential as `root:root 0600` in
the broker-owned Bitcoin tree. The typed credential read verifies that this
record and the active `rpcauth` match before LND or a managed app can consume
it. The operation is local-only and its result is absent from HTTP, audit, and
logs. Existing configs remain byte-for-byte preserved and are never silently
rotated or restarted by bootstrap. The later explicit Electrs maintenance
migration closes the legacy `rpcauth` case without replacing that credential.

The disposable Ubuntu 24.04 functional gate used the official-source verified
Bitcoin Core 31.1 image and the config produced by this broker path. Correct
credentials authenticated to an isolated `regtest` JSON-RPC endpoint and an
incorrect password was rejected. The first fixture failed closed because
Bitcoin Core 31 requires regtest-specific RPC settings inside `[regtest]`; the
fixture was corrected without changing the production mainnet template. Linux
ownership/idempotency/legacy tests, the full Go suite, vet, both builds, and the
UI build passed. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-bitcoincore-rpcauth-2026-08-11.json`.

The disposable Ubuntu 24.04 gate passed dry-run without creation, config
ensure/read/write round trips, legacy-owner migration, target/field/content
negative cases, symlink resistance, and audit privacy. The gate exposed and
repaired an initially over-strict pre-rename legacy-owner check. All temporary
storage and remote gate artifacts were removed, the official image and
attestation were preserved, and no Bitcoin process or blockchain data was
created. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-bitcoincore-config-enforce-2026-08-10.json`.

Bitcoin Core's typed Compose lifecycle is now complete for status, start,
stop, restart, and uninstall. The broker derives the data mount only from its
root-owned storage enrollment, validates the fixed config and official-image
attestation before start/restart, and writes the execution Compose plus storage
guard as root-owned `0600` files. The manager-owned Compose is an inert install
record and never reaches Docker. `restart` is a closed lifecycle action allowed
only for Bitcoin Core; the Bitcoin config endpoint and BTCPay, Electrs,
Fedimint, Mempool, Public Pool, and Bitcoin-source wiring now use it instead of
direct manager Docker calls. Bitcoin lifecycle, status, and removal fail closed
outside broker `enforce`, and the remaining legacy privileged shell fallback
for custom storage validation has been removed.

The disposable Ubuntu 24.04 gate first passed Linux ownership and negative
tests, then ran the real typed sequence against the previously verified
official image: storage enrollment and config ensure on a 12 GiB-declared
`tmpfs`, rejected argument injection, start, running inspection, restart,
running inspection, stop, stopped inspection, and remove. The isolated config
selected `regtest`; the process ran as UID 101 and `bitcoin-cli` confirmed the
regtest chain. No mainnet process or persistent blockchain data was used. The
broker preserved the official image and attestation while removing the
container and execution assets. All temporary mount, storage metadata,
checkout, and binary paths were removed, the manager remained active, the VM
was powered off, and `LOS-TEST2` was untouched. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-bitcoincore-lifecycle-enforce-2026-08-10.json`.
Bitcoin consumer access now has a stable, restart-minimizing contract. New
installs create `bitcoincore_default` on the fixed private subnet
`172.31.253.0/24`, and the default `bitcoin.conf` allows localhost plus that
subnet before bitcoind first starts. Every current or future local Docker
consumer must join this one external network; app-specific networks are not
added to `rpcallowip`. Reading local RPC configuration is now side-effect free,
and the BTCPay, Electrs, Fedimint, Mempool, and Public Pool wiring paths no
longer rewrite `bitcoin.conf` or restart Bitcoin Core. Existing nodes receive
the fixed allow entry once when the Bitcoin app itself is installed/started;
an already-running daemon is restarted once only if that migration changed the
file, while a stopped daemon reads it on its normal start.

The disposable Ubuntu 24.04 gate passed both fresh and legacy-network cases.
The fresh network used `172.31.253.0/24`; a pre-existing Compose-labeled
dynamic network on `172.18.0.0/16` remained usable while its old allow entry
coexisted with the new baseline. In both cases an isolated Docker consumer
resolved `bitcoind`, authenticated over the shared network, and confirmed
`regtest` with `getblockchaininfo`; the Bitcoin container ID and start time did
not change. No mainnet or persistent blockchain data was used. Evidence is in
`docs/baselines/privilege-hardening-phase2-bitcoincore-consumer-network-enforce-2026-08-11.json`.

The shared network is now also a closed broker operation for nodes whose local
Bitcoin Core is a native/systemd service rather than the App Store container.
The caller cannot choose its name, subnet, gateway, labels, bridge, or firewall
arguments. The broker creates or validates only `bitcoincore_default` at
`172.31.253.0/24` with gateway `172.31.253.1`; an existing network with a
different driver, scope, labels, or IPAM is rejected without replacement. If
UFW is active, only the compiled Bitcoin RPC, P2P, and ZMQ ports are allowed on
the bridge derived from the validated Docker network ID. The manager never
writes, starts, stops, restarts, or removes the external Bitcoin service.

Native Bitcoin has a distinct RPC exposure contract because Docker-to-host
traffic is source-NATed to a host address before Bitcoin Core applies
`rpcallowip`. The native service therefore binds RPC only to localhost and the
private gateway (`rpcbind=127.0.0.1` plus `rpcbind=172.31.253.1`) and uses
`rpcallowip=0.0.0.0/0`; this is safe only as a complete contract with no RPC
bind on a LAN/WAN interface and the broker-managed bridge-only UFW rule. The
App Store Bitcoin container keeps the narrower `rpcallowip=172.31.253.0/24`
contract because its consumers connect container-to-container without that
host source NAT. Existing native configurations are detected read-only and
must be reconciled explicitly by the operator; LightningOS does not silently
rewrite or restart them.

BTCPay, Electrs, Fedimint, Mempool, and Public Pool now resolve native Bitcoin
to the fixed gateway and join the shared network. Electrs and Mempool no longer
require the Bitcoin App Store marker: their full-index gate checks the running
local node over RPC for synchronization, pruning, and a synced `txindex`.
Remote Bitcoin modes remain separate and do not create the local consumer
network.

An Ubuntu 24.04 disposable gate copied `bitcoind` and `bitcoin-cli` from the
previously verified official LightningOS Bitcoin Core 31.1 image, ran the
daemon as an unprivileged native systemd service in `regtest`, and reached it
twice from disposable containers on the fixed network. The service start
timestamp and config hash remained unchanged across the accepted consumer
window, the manager stayed active, and no production/mainnet data was used.
NAT-PMP, UPnP, and discovery were explicitly disabled in the accepted fixture.
The same finding is enforced for App Store Bitcoin: fresh configs disable
NAT-PMP and UPnP before first start, and the idempotent existing-node baseline
sets both to zero only when reconciliation is required.
The first discarded fixture briefly attempted a NAT-PMP mapping before this
safety omission was detected; the service was stopped immediately, and the
accepted rerun verified that no mapping was added. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-bitcoin-native-consumer-enforce-2026-08-11.json`.

The first dependent-product gate now covers BTCPay's Bitcoin-facing component.
NBXplorer 2.6.8 reached `Ready` against the official Bitcoin Core 31.1 binary
running as an unprivileged native systemd service. Both authenticated RPC and
the P2P handshake crossed only `bitcoincore_default`; NBXplorer and PostgreSQL
published no host ports, the Bitcoin config hash remained unchanged, and the
manager stayed active. The disposable node has no initialized LND certificate
or macaroon, so the gate deliberately did not fabricate Lightning credentials
or claim that the full BTCPay UI/LND surface was exercised.

This gate closed two restart-prevention gaps. The standard App Store
`bitcoin.conf` now includes `whitelist=172.31.253.0/24` before first start, and
the existing-node App Store baseline adds it idempotently alongside the fixed
RPC allowlist. This prevents NBXplorer's stable private peer from requiring a
later Bitcoin config edit. BTCPay's PostgreSQL database repair also retries the
brief first-initialization interval in which `pg_isready` succeeds immediately
before PostgreSQL shuts down its temporary bootstrap server. The accepted gate
then passed from a clean state. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-btcpay-native-bitcoin-gate-2026-08-11.json`.

BTCPay image preparation has now entered the typed broker boundary before its
secret-bearing Compose lifecycle. The closed catalog admits only `server`,
`nbxplorer`, `postgres`, and `tor`; each maps to one fixed catalog image and
transient unit. The official registry no longer exposes the legacy
`btcpayserver/btcpayserver:latest` reference, so the current upstream stable
release is explicitly cataloged as `btcpayserver/btcpayserver:2.4.2`.
BTCPay's `server` variant nevertheless performs a real pull on every requested
install/start and status checks the pull unit before accepting a cached image.
The version constant must be reviewed whenever upstream publishes a stable
release; the root broker never discovers or accepts an arbitrary tag at
runtime. NBXplorer 2.6.10, PostgreSQL 16, and Tor 0.4.9.5 remain fixed and
cache-authoritative, with Tor omitted for clearnet/local Bitcoin sources.

The disposable Ubuntu gate rejected an injected variant, passed dry-run,
downloaded official BTCPay 2.4.1 at digest
`sha256:f841a37ae8888bf1aca1e9391352dec5f622fc739b13b4913eebdb30b1c275e2`,
and then proved refresh-on-request by scheduling a second pull while that same
image was already cached. The second pull reported the image up to date. All
three fixed dependencies returned `ready`; no container or Compose project was
created, the manager remained active, temporary source/broker assets were
removed, and the VM was shut down gracefully. Bitcoin source resolution and
LND were not invoked, so the working remote Full Node + local LND contract was
preserved unchanged. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-btcpay-images-enforce-2026-08-11.json`.

On 7 August 2026, upstream released BTCPay 2.4.2 for a critical vulnerability
under active exploitation that allowed an unauthenticated attacker to obtain
LND `.macaroon` credentials from every earlier BTCPay version. Upstream
confirmed stolen funds and required both BTCPay 2.4.2 and LND 0.21.1, while
recommending NBXplorer 2.6.10 to integrators. The catalog was advanced
immediately to BTCPay 2.4.2 and NBXplorer 2.6.10 and now has an explicit test
against regression below that security floor. LightningOS already installs LND
0.21.1-beta and gives BTCPay a dedicated macaroon without `offchain:write` or
`onchain:write`; no admin macaroon is referenced or mounted. This reduces the
authority of a leaked BTCPay credential but does not replace the mandatory
upstream update.

The follow-up disposable Ubuntu gate passed the full Go test/vet/build suite,
then used the updated broker to resolve and prepare only the fixed images. It
accepted dry-run, returned both variants `ready`, and verified official amd64
digests `sha256:66a7e88964d44302a0312bf601c9ef0e4c3af71696e2e4edac6daa2742854e79`
for BTCPay 2.4.2 and
`sha256:7d2ab1f6ce38e301cba98a4f85bfbfcf2912d5034bd88af056f9d60e5511f911`
for NBXplorer 2.6.10. No container was created, the manager remained active,
and the audit contained operation metadata only. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-btcpay-security-release-2026-08-11.json`.

BTCPay's secret-bearing inputs first entered a closed broker snapshot gate
before the legacy Compose lifecycle. The typed `app.compose.snapshot` operation
accepts only BTCPay and byte-validates the shared Compose catalog, exact
PostgreSQL initialization, and an exact allowlist of environment keys with
coherent local, App Store, clearnet-remote, or Onion-remote Bitcoin wiring. It
also requires the copied LND TLS certificate to match `/data/lnd/tls.cert` and
requires the dedicated BTCPay credential to be a private regular file whose
bytes differ from the native `admin.macaroon`. Symlinks, an admin hardlink,
unexpected snapshot assets, broad Linux secret permissions, and Compose/env
injection are rejected before persistence.

In enforce mode the broker persists the validated execution snapshot under
`/var/lib/lightningos-privileged/apps/btcpay`, with root-owned `0700`
directories and `0600` files. The dedicated credential is exposed to the
container only as `/etc/lnd/btcpay.auth`; neither a `.macaroon` filename nor
`admin.macaroon` appears in the execution manifest. This does not create a new
credential or expand its permissions: the manager still bakes the existing
BTCPay-specific set (`address` read/write, `info` read, `invoices` read/write,
and `onchain` read), without `offchain:write` or `onchain:write`. The Ubuntu
root gate passed the complete Go test/vet/build suite and all positive and
negative snapshot cases without starting a container or touching Bitcoin/LND.
Evidence is stored in
`docs/baselines/privilege-hardening-phase2-btcpay-secret-snapshot-2026-08-11.json`.

BTCPay's Compose execution has now completed that cutover. The shared manifest
catalog admits BTCPay start/stop/status/remove but rejects restart and every
caller-supplied path, image, service, or argument. Docker package/runtime
preparation and all four fixed BTCPay image variants fail closed outside broker
`enforce`; the manager file contains no legacy Compose, image pull, or Compose
status call. Start verifies the exact required images, uses only the
root-owned snapshot, starts `btcpay-db`, checks for the NBXplorer database, and
creates it with the fixed owner/template/encoding/locale arguments only when
missing, before starting the complete stack. Database command output is never
returned through broker errors. Stop uses the fixed 30-second catalog timeout,
status reads the catalog primary service, and remove runs a fixed Compose down
before deleting only the privileged snapshot; persistent BTCPay application
data, database, wallet state, and the dedicated source macaroon remain intact
for reinstall.

The Ubuntu 24.04 amd64 gate passed all Go tests, vet, builds, and the root-mode
BTCPay lifecycle tests. Those tests proved the database-before-stack order,
existing/missing/transient database paths, exact image set with and without
Tor, snapshot-only Compose paths, generic failures, stop behavior, and
snapshot-only removal. The command runner was deliberately recorded rather
than connected to Docker, so no container or Bitcoin/LND service was touched;
the full functional BTCPay+LND gate remains separate. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-btcpay-lifecycle-enforce-2026-08-11.json`.

The full BTCPay+LND integration gate has now passed on LOS TESTE2. The node's
local App Store Bitcoin Core was intentionally not used because it was not
synchronized; the existing BTCPay/NBXplorer wiring continued to use the
remote synchronized Full Node through Tor. The gate upgraded the stopped
installation from BTCPay 2.4.0 and NBXplorer 2.6.8 to the fixed official
BTCPay 2.4.2 and NBXplorer 2.6.10 images, reached NBXplorer `Ready`, and
returned `synchronized=true` without changing the selected Bitcoin source.
The dedicated LND macaroon was byte-distinct from `admin.macaroon` and exposed
only address read/write, info read, invoice read/write, and on-chain read. It
authenticated `getinfo`, created and immediately cancelled a zero-amount test
invoice, rejected payment-history access, and performed no payment or fund
movement.

The real upgrade exposed two recovery defects that the command-recording gate
could not reveal. First, the original 15-second transport deadline expired
while Compose was legitimately recreating PostgreSQL. Real lifecycle and
remove mutations now receive a fixed two-minute ceiling in both client and
broker, while dry-run and all other operations retain their short configured
deadline. Second, that interrupted Compose recreate left a temporary generated
container name, while the repair path addressed the literal `btcpay-db`
container name. PostgreSQL readiness, catalog lookup, and idempotent database
creation now run through the same validated Compose project and the fixed
`btcpay-db` service, so recovery does not trust a runtime-generated container
name. Both failures remained fail-closed and were retained in the root-only
audit alongside the successful retries.

The final-code gate then passed start/stop/start/stop through the authenticated
manager API. It preserved 68 public BTCPay tables, one existing store, and the
NBXplorer index across the cycle; the final binary reached `Ready` at block
962046. The root-owned secret snapshot remained `0700`/`0600`, the LND mount
remained read-only, and the official amd64 image IDs were
`sha256:66a7e88964d44302a0312bf601c9ef0e4c3af71696e2e4edac6daa2742854e79`
for BTCPay and
`sha256:7d2ab1f6ce38e301cba98a4f85bfbfcf2912d5034bd88af056f9d60e5511f911`
for NBXplorer. Bitcoin Core and LND were neither replaced nor restarted,
manager configuration and sudoers hashes remained unchanged, the temporary
`enforce` override was removed, and the node was returned to its original
BTCPay-stopped state with the corrected binaries in `shadow`. The audit stayed
root-owned `0600`, used only the declared metadata fields, and contained no
credential terms. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-btcpay-functional-lifecycle-2026-08-11.json`.

The subsequent App Store order was driven by observed use: LNDg first,
LNbits second, then the native BRLN Loop and Magma paths. Fedimint, Electrs,
and Mempool are lower priority; Electrs and Mempool remain last because their
functional gate requires a synchronized, unpruned Full Node with `txindex=1`.

LNDg's image boundary has entered the broker. Upstream publishes neither a
container image nor release assets, so LightningOS builds its own image from
the exact `v1.11.0` commit. That release ref resolves to the cataloged
GitHub-verified commit, and the broker downloads only its fixed codeload
archive, checks the closed SHA-256 before extraction, uses a digest-pinned
Python base image, pins the three LightningOS-added Python packages, and
attests the resulting Docker image ID together with release, commit, source
digest, and base image. In `enforce`, install/start wait for that attested image
and skip the manager-owned build. The compatibility build used in `shadow`
also stopped cloning `master` and now checks the same source digest.

The isolated Ubuntu 24.04 gate built the image with Docker 29.1.3, imported
Django, gRPC, pandas, PostgreSQL, supervisor, and whitenoise successfully, and
bound image ID
`sha256:374025a562a4dbba3b79ac9177cee0c92364ecbf76347ed4c9b3be0c033c8c56`
to the attestation contract. The existing provisional VirtualBox VM was
reached only through a localhost NAT forward; no LAN discovery occurred, no
application container was started, and Bitcoin/LND were untouched.

LNDg cannot use a read-only macaroon without losing core features. Its release
invokes analytics plus peer, channel, fee, invoice, payment/rebalance,
on-chain, signing, and watchtower mutations. The next slice will therefore
bake a dedicated LNDg credential with only the required LND permission
entities and expose it through a root-owned snapshot. The current whole
`/data/lnd` mount, including `admin.macaroon`, is explicitly not an acceptable
final state. Because upstream provides no signed release manifest, this slice
does not claim an upstream cryptographic release signature; transitive Python
hash locking remains part of issue #34. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-lndg-image-gate-2026-08-11.json`.

The LNDg credential surface is now separated from the native LND directory.
An inventory of every non-generated Python RPC call in LNDg `v1.11.0`, checked
against the official LND `v0.21.1-beta` permission maps, produced a fixed
13-permission macaroon: address write; info read; invoice read/write; message
write; off-chain read/write; on-chain read/write; peer read/write; and signer
read/generate. The file must be regular and private, and byte equality with
`admin.macaroon` is rejected. Macaroon administration and `info:write` are not
granted. This credential remains capable of moving funds by design because
LNDg exposes payments, rebalances, channel operations, and on-chain actions;
dedication limits unrelated authority but cannot make this app read-only.

Compose no longer mounts all of `/data/lnd`. It mounts only the private
`tls.cert` and `lndg.macaroon` directory plus the exact `channel.db` file in
read-only mode. Upstream uses that database path only to display its file size;
no graph directory or native macaroon is exposed. The initializer receives
explicit certificate, macaroon, and database paths instead of deriving
`admin.macaroon` from an LND root.

The fixed permission set was baked on LOS TESTE2 and authenticated safe reads
for node info, channels, invoices, wallet balance, peers, and pending channels,
plus a test message signature. The macaroon lived only at one validated `/tmp`
path and was removed by a trap. No invoice, payment, channel mutation,
on-chain mutation, config edit, LND restart, or fund movement occurred. The
node address came directly from its secret record and no network scan was
performed. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-lndg-dedicated-credential-2026-08-11.json`.

The complete LNDg runtime boundary has now passed on LOS TESTE2. The manager no
longer owns any Docker, UFW, LND-restart, database-exec, lifecycle, inspection,
removal, or admin-reset command for this app. It writes only a closed
declaration; the broker validates it byte-for-byte, copies secrets and LND
material into a root-owned execution snapshot, selects both images from the
catalog, and runs fixed typed operations. The LNDg image runs as UID/GID 1000
with all capabilities dropped and `no-new-privileges`; its dedicated
PostgreSQL 16.14 Trixie container uses the Docker Official Image index digest,
has its own persistent volume and private network, and does not publish 5432.

The live gate also corrected a legacy behavior: although the old Compose had a
PostgreSQL container, an entrypoint indentation defect left LNDg using SQLite.
The new entrypoint exports SQLite only when PostgreSQL has no Django schema,
migrates PostgreSQL, imports the fixture once, records a private marker, and
keeps the original SQLite file for rollback. The gate found 31 legacy tables
and compared all 27 relevant common tables after migration: 190 rows on each
side, zero count differences, and no tables exclusive to either database.
Nodes whose native LND uses PostgreSQL are also supported: because they have no
`channel.db`, the broker mounts a private empty read-only placeholder used only
by LNDg's file-size display.

Install/start, inspect, authenticated admin login, stop, and start passed with
both containers running at the end. The LND credential remained dedicated;
the execution snapshot was root-owned `0700` with `0600` environment and a
group-readable `0640` macaroon for the non-root container. LND and Bitcoin
retained their original activation/container timestamps. The manager/broker
binaries were updated in `shadow`, all gate uploads/backups and the mistakenly
tested Bookworm image were removed, and no network scan or adapter operation
occurred. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-lndg-functional-lifecycle-2026-08-12.json`.

LNbits is the next high-adoption application to enter the boundary. The
mutable `lnbits/lnbits:latest` selector has been replaced by the official
stable `v1.5.6` image and its Docker Hub multi-architecture manifest digest.
The broker catalog alone selects that image and schedules its pull. Existing
installations keep their database and extensions, but their generated Compose
is advanced to the closed image on the next install/start request.

The LNbits LND surface no longer mounts `/data/lnd`. The complete `v1.5.6`
inventory includes both `LndRestWallet` and the built-in `LndRestNode` manager.
Preserving wallets, invoices, payments, node information, peers, channels,
fee policy, and on-chain balance/open/close operations requires a dedicated
nine-permission macaroon: info read plus invoice, off-chain, on-chain, and peer
read/write. Only that credential and `tls.cert` enter the app-private
read-only mount. Existing `.env` files that reference `admin.macaroon` are
migrated to the dedicated path, and alternate encrypted/admin/invoice
macaroon selectors are scrubbed because upstream gives an encrypted selector
precedence. The credential must be a private regular file and byte equality
with the native admin macaroon is rejected. LNbits can still move funds and
manage channels by design, but it has no address, message-signing, signer, or
macaroon-administration authority.

The fixed official image digest was pulled on LOS TESTE2 and imported in an
ephemeral container with no network, a read-only root filesystem, and only
temporary data filesystems. The exact image reference was removed afterward.
A temporary nine-permission macaroon authenticated safe node, peer, channel,
on-chain balance, payment-history, and invoice reads over the existing local
gRPC listener, while address creation, message signing, and macaroon
administration were denied; the file was removed by a trap. No LNbits service was installed, no
invoice or payment was created, no funds moved, and neither LND configuration
nor LND/Bitcoin service state changed. The node address came only from its
fixed secret record and no network discovery was performed. Evidence is in
`docs/baselines/privilege-hardening-phase2-lnbits-image-credential-gate-2026-08-11.json`.

LNbits lifecycle, status, uninstall, REST-listener policy, and firewall policy
now use the typed broker exclusively. The manager declaration is reduced to a
closed Compose document plus one private environment file. The broker validates
both, rejects unexpected app/LND assets, and creates a persistent root-owned
execution snapshot. The official digest-pinned image runs as the closed
host-independent UID/GID `65532:65532` with a
read-only root filesystem, all capabilities dropped, `no-new-privileges`, no
host networking or Docker socket, one writable data mount, and only the
dedicated macaroon and `tls.cert` as individual read-only mounts.

The existing-node gate exposed an additional upstream precedence rule: when
LNbits Admin UI has persisted funding-source settings in `system_settings`, the
database overrides the corrected `.env`. A legacy node therefore still tried
the removed native `admin.macaroon` and fell back to `VoidWallet`. Before every
start, the broker now runs a fixed parameterized SQLite migration in the same
official image with no network, non-root UID, read-only root filesystem, and
only the data directory mounted. It updates only the LndRest wallet class,
endpoint, certificate, and dedicated macaroon fields and empties alternate LND
credential selectors; no user, wallet, payment, extension, or unrelated
setting is inserted, deleted, or rewritten.

For a clean installation, the broker now materializes the reviewed container
and `lnbits_default` network in the stopped state before applying the internal
LND REST firewall rule. Only after the real Compose subnet is known and the
fixed rule succeeds does the broker start LNbits. This closes the fresh-node
ordering gap without adding a caller-controlled network or firewall input.

LOS TESTE2 provided a real stopped legacy upgrade with no app-private LND
directory. A dedicated nine-permission macaroon was created under a separate
root key, authenticated `GetInfo`, and denied address creation. The old SQLite
database started and finished with one database, 13 application tables, and
273 rows; integrity passed and no table or row was lost. The hardened runtime
returned HTTP 200 using `LndRestWallet` without `VoidWallet` fallback, and
dry-run, start, inspect-running, firewall, stop, and inspect-stopped all passed.
The final gate also confirmed that numeric UID/GID `65532` did not collide with
a host account or group, the stopped create-before-firewall sequence passed,
and the persistent data was owned by the closed container UID.
The node returned to its original stopped choice. LND and Bitcoin retained
their exact activation/container timestamps, the manager remained healthy in
`shadow`, and all gate backups, uploads, log captures, and render helpers were
removed. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-lnbits-functional-lifecycle-2026-08-12.json`.

The high-adoption native BRLN application slice, beginning with BRLN Loop and
Magma, is accepted. LNbits remains pending only in the final cross-version
matrix and final rollout/rollback acceptance shared by all applications.

BRLN Loop Out and Magma do not have an OS-privileged lifecycle to move into the
broker. Both are in-process services of the non-root manager, persist their
install/enabled state in the manager PostgreSQL database, publish no host port,
and contain no sudo, systemd, Docker, UFW, root-file, or host-listener path. A
new source-level boundary test scans every production `loopout_brln_*` and
`magma_*` file plus both App Store wrappers and rejects those operation classes
if they are introduced later.

Their LND authority remains explicitly inventoried in this technical plan, but
the App Store does not present the third-party elevated-access disclaimer for
these first-party native features. BRLN Loop can pay invoices and create a
return address; it retains sensitive reauthentication and the central spending
guard. Magma can create invoices, connect peers, and fund channels; manual
channel opens retain fresh funds reauthentication, and the tested installation
remained in monitor mode. The authenticated LOS TESTE2 gate preserved both
apps as installed/running with no active Loop job or transient Magma order and
moved no funds or channels. LND and Bitcoin retained their timestamps. The node
still had the manager's legacy Docker-group membership because the shared Phase
2 `enforce` cutover had not yet run; neither native app used it. The later
shared cutover implements and tests final removal.
Evidence is stored in
`docs/baselines/privilege-hardening-phase2-native-brln-boundary-2026-08-12.json`.

Lightning Labs' Lightning Loop app is a separate product from the first-party
BRLN Loop Out feature above. Its elevated-LND disclaimer remains because it is
a third-party daemon with a dedicated credential capable of swaps and fund
movement. Its native systemd lifecycle is now inside a closed broker contract:
status, verified release/config/unit preparation, start/stop, removal,
permission repair, and manager-readable API material synchronization have
distinct typed operations. The manager no longer contains a `sudo`,
`systemd-run`, privileged shell, user/group mutation, root-file install, or
caller-selected privileged path for this app, and its legacy privilege budget
has been removed.

The catalog fixes Loop `v0.33.3-beta`, the official GitHub release URL,
architecture-specific SHA-256 values, service account, paths, ports,
configuration, and hardened unit. Archive extraction accepts only the two
bounded regular binaries. Requests cannot select a URL, archive, checksum,
path, unit, user, owner, command, or lifecycle action outside `start`/`stop`;
symlinked trees and oversized credential material fail closed. The dedicated
LND macaroon is preserved across idempotent repair, and only broker-created
`0640` client copies make the Loop API certificate and macaroon readable to
the manager.

LOS TESTE2 supplied the existing-node gate. Loop was already installed at the
cataloged version with persistent swap state, but intentionally inactive and
disabled. The broker was exercised directly as `lightningos` while the global
manager configuration remained in `shadow`. Typed status, real idempotent
permission/config/unit preparation, client-material synchronization, and a
dry-run start all passed. No real start, stop, uninstall, or swap operation was
performed; the service finished inactive/disabled. Internal before/after hash
comparisons proved the swap database, dedicated LND macaroon, Loop API
macaroon, and TLS private key unchanged. Broker audit contained no certificate
or macaroon fields. Bitcoin and LND retained their exact timestamps, and all
remote gate files were removed. Clean install, reboot persistence, and
Ubuntu 24.04/26.04 remain in the shared final matrix. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-lightning-loop-boundary-2026-08-12.json`.

The Elements half of the coupled Elements/PeerSwap domain is accepted. The
manager now has zero direct privileged call sites for Elements. A closed broker
contract owns storage enrollment, the dedicated `lightningos-elements`
identity, official release download and fixed SHA-256 verification, bounded
archive extraction, config merge/read, hardened unit, typed status/RPC,
lifecycle, and removal. The official `elements-23.3.3` amd64 and arm64 digests
are fixed in the shared catalog; callers cannot select a release URL, checksum,
binary, service identity, unit, RPC method, or unenrolled config path.

Existing configs are merged inside the broker only after the operator-selected
storage target passes canonical mounted-filesystem validation. The default
path remains supported, while a distinct/external volume remains a first-class
installation choice and is persisted as the active enrolled target. The broker
forces loopback-only RPC values, rejects any additional public `rpcbind` or
`rpcallowip`, preserves
unknown operator options, and permits later config reads only for the
root-enrolled target. The service no longer runs as human operator `losop`; its
unit uses `ProtectSystem=strict`, `ProtectHome=true`, private devices, no new
privileges, and write access only to the enrolled data tree.

LOS TESTE2 supplied the state-preserving gate with Elements installed but
inactive/disabled on its existing external mounted storage. Real ensure
migrated ownership and the unit without starting the daemon. Config and RPC
credential hashes were unchanged, an unenrolled config read failed closed,
dry-run start/removal passed, and no legacy `losop` ownership remained in the
app or data tree. Bitcoin and LND retained their exact timestamps. Evidence is
stored in
`docs/baselines/privilege-hardening-phase2-elements-boundary-2026-08-12.json`.

The PeerSwap half of the coupled domain is also accepted. The manager now has
zero direct privileged call sites in its PeerSwap lifecycle and Elements-source
files. Six closed broker operations own status, the root-only source policy,
idempotent runtime preparation, lifecycle, and removal; the existing catalog
firewall operation admits only port 1984. No request accepts a path, unit,
binary, release URL, checksum, user, group, port, shell fragment, or arbitrary
lifecycle action.

The legacy package directory remains `version_5_0/amd64` for installer and
upgrade compatibility, while the broker verifies that its actual content is
upstream PeerSwap `v6.0.0` at commit
`25a153e5b70cb35830dc6354d0fac6994e0fd610` plus the official PSWeb upstream
`main` commit `09983da398f253f8c14213e9f5c61b80cc879b67`, packaged under the
`v6.0.0.1` label, using three fixed SHA-256 hashes. Both services run as
`lightningos-peerswap`, without the
human operator or the `lnd` group, and can write only their dedicated runtime.
The source policy is `root:root 0600`; neither the manager nor the app can read
it directly. Existing configuration and PSWeb preferences are merged while
forcing loopback LND, the dedicated credential path, and the selected Elements
RPC values.

Local store-managed Elements and remote/external Elements remain independent
first-class modes. Local mode consumes the already enrolled default or
distinct/external Elements volume; remote mode has no systemd dependency on
the local Elements service. LOS TESTE2 supplied the real remote-mode upgrade:
eight legacy files were copied byte-for-byte to the new runtime and the legacy
tree was retained for rollback, while the two PeerSwap services and local
Elements stayed inactive/disabled. A dedicated nine-permission LND macaroon
authenticated required reads and was denied macaroon administration. The
remote full Liquid node answered `liquidv1`, while Bitcoin, LND, manager, and
their timestamps remained unchanged. No service lifecycle or network-policy
mutation was performed and all gate files were removed. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-peerswap-boundary-2026-08-12.json`.

The Tapd slice is accepted without starting the preserved installation. The
manager now has zero direct Docker, Compose, sudo, or raw `tapcli` execution in
the Tapd contract. Five closed broker operations own status, root-only runtime
preparation, lifecycle, data-preserving removal, and eight typed CLI actions.
The official Lightning Labs stable `v0.8.0` image is fixed by its multi-arch
manifest digest; neither the manager nor a request can select an image,
container, mount, command, flag, port, path, user, or raw CLI argument. The
runtime is read-only, drops every capability, enables no-new-privileges, binds
Tapd RPC/REST to loopback, and mounts only its data plus exact config, LND TLS
certificate, and dedicated macaroon. The LND tree, admin macaroon, Docker
socket, and published ports are absent.

The upstream `v0.8.0` verifier contains a case-only mismatch for one signer.
The release gate did not lower its five-signature quorum: a case-only local
correction validated the signed manifest and the exact `tapd`/`tapcli`
checksums against five developers. The production probe then ran both binaries
from the immutable digest with no network, no capabilities, a read-only
filesystem, and an unprivileged UID, requiring their exact `v0.8.0` outputs.
No release candidate was substituted for the stable release.

LOS TESTE2 retained its existing Tapd data and PostgreSQL database while the
broker created the root-only execution snapshot and a dedicated nine-permission
LND credential. That credential authenticated `getinfo` and was denied
macaroon administration. Image readiness, status, and start/stop dry-runs
passed; Tapd remained stopped. Bitcoin, LND, and manager activation timestamps
and the Tapd data metadata hash remained unchanged. Universe discovery now
selects only the server-approved `universe.lightning.finance` REST endpoint,
pins its validated public DNS results, and rejects alternate, private, local,
reserved, proxy, and redirect targets; arbitrary universe hosts remain confined
to the typed `tapcli` sync operation. Asset send and mint finalization require
fresh reauthentication. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-tapd-boundary-2026-08-12.json`.

The Public Pool slice is accepted without starting its preserved containers.
Five closed broker operations now own its root-only declaration, status,
lifecycle, data-preserving removal, and fixed `3333`/`8081` firewall policy.
The two previously mutable image choices are fixed to reviewed publisher
commit tags and multi-architecture manifest digests, with OCI source/revision
correlation and exact no-network runnable probes. Both containers run as
UID/GID 65532 with read-only roots, all capabilities dropped, and
no-new-privileges. The backend can write only its existing database bind; the
UI copies Caddy and static assets into bounded tmpfs and receives only a
root-owned read-only Caddyfile.

The Bitcoin contract retains App Store local, existing/native local, and
remote modes. Local modes join only the fixed `bitcoincore_default` consumer
network and use fixed mainnet RPC/raw-block ZMQ endpoints; no request can
select a network, bridge, port, image, mount, user, or command. LOS TESTE2 kept
both Public Pool containers stopped. Its three data files were
content-identical before and after the required non-root ownership migration,
and Bitcoin, LND, and manager activation timestamps did not change. Image
probes used ephemeral no-network containers; UFW remained inactive and
unmodified. Evidence is in
`docs/baselines/privilege-hardening-phase2-publicpool-boundary-2026-08-12.json`.

The Bark Wallet slice is accepted with a real state-preserving lifecycle on
LOS TESTE2. Seven closed broker operations now own status, the exact runtime
snapshot, lifecycle, data-preserving removal, fixed-port firewall admission,
and password read/reset. The manager no longer creates or changes Bark's
Compose declaration, TLS key, UI password, session secret, file ownership, or
UFW rule. Uninstall preserves both wallet and authentication state.

The official Bark Web `v0.7.2` web/API images, Bark `0.6.1` daemon image, and
Caddy `2.10.2` proxy are pinned by multi-architecture manifest digest. The
published daemon image contains a legacy regtest wrapper, so the catalog never
executes it: the broker verifies the release checksum for the architecture,
requires the exact `barkd 0.6.1` version output, and starts only
`/usr/local/bin/barkd`. All four containers run non-root with read-only root
filesystems, all capabilities dropped, `no-new-privileges`, and no Docker
socket or host networking. Only barkd receives the writable wallet bind;
auth, TLS, and proxy configuration are exact read-only files.

The v0.7.2 authentication boundary forces UI auth, secure HttpOnly/SameSite
cookies over HTTPS, and CSRF on state changes. Direct barkd mnemonic access
returned 404, unauthenticated reveal returned 401, and a broker-delivered
password created a valid session. The final policy decision preserves that
official backup flow but adds LightningOS fresh reauthentication: opening Bark
requires the `bark_seed_reveal` scope and the proxy forwards the exact mnemonic
reveal route only while that authorization is younger than three minutes. The
proxy authenticates the manager with the fixed root-owned LightningOS CA and
SNI `localhost`; insecure TLS verification is forbidden. The manager returns
only 204 authorization state and never receives the mnemonic, Bark session,
token, or UI password. When UFW is active, the broker derives the exact Bark
bridge from Docker's validated network ID and admits manager port 8443 only on
that bridge. Login-disabled nodes retain Bark's own authenticated session and
CSRF boundary. Caddy 2.10.2 accepted the generated configuration on
the disposable Ubuntu 26.04 VM using the official release checksum. Evidence is
in
`docs/baselines/privilege-hardening-phase2-bark-wallet-reauth-2026-08-13.json`.

The four existing data files were byte-identical across broker enrollment;
the subsequent functional start performed expected database writes. The
legacy TLS certificate/private key were copied byte-for-byte, and the original
stopped choice was restored. Bitcoin, LND, manager, firewall, and network
state did not change. Evidence is in
`docs/baselines/privilege-hardening-phase2-bark-wallet-boundary-2026-08-12.json`.

The Mempool slice is accepted. Its official v3.3.1 frontend/backend images and
the official MariaDB 10.11.18 LTS image are pinned by multi-architecture
manifest digest. The manager has no direct Docker, Compose, UFW, filesystem
removal, or sudo call in this app. The broker validates the exact declaration,
private runtime environment, image variants, fixed networks, port, identities,
volumes, lifecycle and removal policy before it creates a root-only snapshot.
All three services run non-root with read-only roots, all capabilities dropped,
`no-new-privileges`, and bounded tmpfs; only the named MariaDB and backend cache
volumes are persistent and writable.

The independent start gate reuses the Electrs Full Node checks, requiring an
authenticated, synchronized, unpruned Bitcoin node with synchronized
`txindex=1`, plus the running catalog Electrs container on
`electrs_default`. App Store Bitcoin and existing native/systemd Bitcoin remain
supported through the fixed consumer boundary. A disposable positive gate
used 101-block regtest with real Electrs, then passed Mempool API readiness,
inspect, stop/start and volume-removing uninstall. A separate fixture upgraded
a MariaDB 10.5.21 volume to 10.11.18 with `MARIADB_AUTO_UPGRADE=1` and preserved
its test row. LOS TESTE2 was inspected read-only only: Mempool and Electrs stayed
absent, and Bitcoin, LND and manager timestamps were unchanged. Evidence is in
`docs/baselines/privilege-hardening-phase2-mempool-functional-2026-08-13.json`.

The Fedimint Guardian and Gateway slice is accepted as the final
lower-priority Docker family. Both official stable v0.11.1 images are pinned by
multi-architecture manifest digest and correlated to upstream commit
`2620789610a2c65c1068de973ebb5657d08d549d`. The manager has no direct Docker,
Compose, UFW, systemctl, sudo, TLS deletion, or filesystem ownership operation
in the Fedimint handler. The broker validates exact private runtime documents,
immutable images, mounts, ports, networks, identities, lifecycle, removal,
firewall, logs, and root-owned execution snapshots.

Both containers run as UID/GID 1000 with read-only root filesystems, all
capabilities dropped, `no-new-privileges`, and bounded tmpfs. Existing Guardian
and Gateway databases are retained and converted to the fixed runtime owner
only after the legacy container is stopped. The former Gateway boundary that
mounted all of `/data/lnd`, used `admin.macaroon`, deleted TLS material, and
restarted LND from manager code has been removed. The new snapshot exposes
only a copied certificate and a dedicated macaroon with the nine permissions
actually exercised by upstream v0.11.1: `info:read`, invoice, offchain,
onchain, and peer read/write. No macaroon administration, signer, or message
permission is granted.

App Store Bitcoin, existing native/systemd Bitcoin, and remote Bitcoin remain
supported. Existing nodes already provisioned for Docker-to-LND RPC do not
restart LND; a new Gateway install performs the one-time broker-controlled LND
listener/certificate reconciliation only when the fixed host access is absent.
Guardian public TCP/UDP and Gateway UI/Iroh plus internal LND RPC rules are
fixed by the catalog. Fedimint log reads now use a bounded read-only broker
operation rather than manager-side Compose.

The disposable Ubuntu gate resolved the two official digests, returned
`fedimintd 0.11.1` and `fedimint-gateway-server 0.11.1`, and proved the real
Compose containers use UID 1000, read-only roots, `cap_drop=ALL`, and
`no-new-privileges`. All containers, project networks, images, and the gate
binary were removed; the VM was powered off and its bridge restored. LOS
TESTE2 was then inspected read-only: Guardian remained running, Gateway
remained stopped, their data remained present, and Bitcoin, LND, and manager
timestamps were unchanged. Evidence is in
`docs/baselines/privilege-hardening-phase2-fedimint-functional-2026-08-13.json`.

The shared Docker cutover implementation is now complete. The manager's final
legacy Compose/Docker fallbacks were removed, Bitcoin logs and BTCPay LND host
access moved behind typed broker operations, and fresh configuration defaults
to `enforce`. Fresh/existing installers and the in-place upgrade no longer
grant Docker/Compose sudo or Docker group membership. The upgrade stages a
root-only rollback bundle and preserves existing config ownership, mode, ACLs,
supplementary groups other than Docker, sudoers state, and the prior Docker
membership.

On the disposable Ubuntu 24.04 clone, a simulated legacy Docker-group manager
passed forward cutover, broker self-test, manager restart, rollback, and a
second forward cutover. Both account and live-process Docker GIDs were removed;
the rollback restored the old boundary; Bitcoin and LND activation timestamps
did not change. The gate caught and repaired ACL loss before acceptance. The
Linux privileged package passed except for two pre-existing Loop fixtures that
require a fixed app user absent from this clone; all tests added or changed by
this slice passed. Evidence is in
`docs/baselines/privilege-hardening-phase2-shared-cutover-2026-08-13.json`.

Phase 2 remains open only for its supported cross-version acceptance matrix:
complete per-app lifecycle/reboot coverage on Ubuntu 24.04 and 26.04. The
manager/Docker code boundary and controlled rollback path are no longer open
implementation items.

The Ubuntu 26.04 fresh-install platform gate passed on 2026-08-13. An official
release upgrade of a disposable `brln-os-basica` clone reached Ubuntu 26.04
LTS with kernel 7.0.0-29; package repair, reboot, Manager health, PostgreSQL,
Tor, i2pd, broker runtime recreation, the manager-service-user broker
self-test, and the complete Linux Go suite passed. The gate found two installer
regressions before acceptance: Go VCS stamping rejected the normal case where
`sudo` builds an operator-owned checkout, and automatic setup-token output was
captured by the install log. Installer and application-upgrade builds now use
`-buildvcs=false`; setup tokens are generated only with an attached interactive
terminal and are written directly to `/dev/tty`. Non-interactive installs
leave token issuance to a later interactive command. Evidence is in
`docs/baselines/privilege-hardening-ubuntu26-fresh-install-2026-08-13.json`.
This is the Ubuntu 26 platform gate, not the still-required complete per-app
lifecycle/reboot matrix.

Electrs now has a closed source-built image from the verified upstream
`v0.11.1` archive, fixed manifest and networking, one private dedicated RPC
cookie, root-owned execution snapshot, typed lifecycle/inspect/remove, and
fail-closed Full Node checks for authentication, synchronization, pruning, and
`txindex`. Its Ubuntu 24.04 functional gate used an isolated 101-block regtest
Full Node with synchronized `txindex=1`; Electrum protocol, metrics,
stop/start, and volume-removing uninstall passed. The retained LOS TESTE2 node
was inspected only in read-only mode and was not used to weaken or satisfy the
positive gate. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-electrs-functional-2026-08-11.json`.

The same slice closes managed-node Bitcoin Local telemetry in broker
`enforce`: the broker now validates the root-owned Bitcoin execution state and
uses the runtime cookie through fixed in-container `bitcoin-cli` queries,
returning only typed status fields. Existing canonical App Store layouts are
enrolled idempotently without restarting Bitcoin, Docker, networking, or
dependents. In `shadow`, the existing container/log compatibility reader stays
authoritative: it reuses a preserved LightningOS Bitcoin Local credential from
`lnd.conf` when one exists and retains bounded `debug.log` progress when RPC is
busy. Missing telemetry is shown as unknown rather than `0.00%`.
Read-only LOS TESTE2 validation confirmed the preserved node was running,
unpruned, and approximately 99.87% synchronized; the former UI value was an
authentication/reporting failure, not blockchain loss. Evidence is stored in
`docs/baselines/privilege-hardening-phase2-bitcoincore-status-2026-08-11.json`.

The one-time legacy Electrs credential migration is now accepted. The typed
`app.bitcoincore.electrs-credentials.ensure` operation generates a fixed
`electrs` credential into root-only `0600` state, preserves every existing RPC
credential, and atomically inserts only its additive `rpcauth` line before
network sections. App Store install/start requires explicit API/UI operator
confirmation. If authentication proves that activation is pending, the manager
performs exactly one closed Bitcoin lifecycle restart, waits for the credential
to work, and reruns the strict Full Node/`txindex` gate. An already-active
credential causes no restart. External native/systemd Bitcoin remains
read-only and is never reconfigured or restarted by this migration.

The Ubuntu 24.04 disposable gate used the authenticated official Bitcoin Core
31.1 image on regtest. The original credential worked before and after the one
restart, the dedicated credential became active after it, wrong passwords were
rejected, and a second ensure was ready and idempotent. Local full Go tests,
vet/build, UI build and diff checks passed; Ubuntu vet/build and the root
functional gate passed. The test container was removed, the checkout restored,
the official image and root-only attestation retained, and the VM powered off.
LOS TESTE2 was not touched. Evidence is in
`docs/baselines/privilege-hardening-phase2-electrs-rpcauth-migration-2026-08-13.json`.

The remaining dependent products, operational Bitcoin CLI/log paths, and the
mainnet P2P firewall contract remain open. The completed Mempool gate preserves
the permanent synchronized/unpruned/`txindex=1` contract; regtest was used only
as the isolated positive test chain satisfying that same full-index contract.

1. Convert every catalog app to a validated broker manifest.
2. Migrate Docker installation and all app lifecycle operations.
3. Run install/start/stop/restart/uninstall tests for every app on disposable
   Ubuntu 24 and Ubuntu 26 nodes.
4. Remove direct Docker calls and the manager's Docker socket access.

Exit criterion: the complete supported App Store works while the manager is not
in the `docker` group.

### Phase 3 — Packages, firewall, storage, and upgrades

Issue [#34](https://github.com/jvxis/brln-os-light/issues/34) is accepted into
the required `0.5.3` work plan as a dedicated supply-chain track. It complements
the privilege boundary: code downloaded by a root installer or upgrade path
must be authenticated before it is extracted, installed, or executed. This
track applies consistently to fresh installs, existing-node installs, and
upgrades; it is not deferred to a later release.

Phase 3 started with the Manager firewall status boundary on 2026-08-13. The
Manager no longer executes privileged `ufw status`; it requests the read-only
`manager.firewall.status` operation. The broker accepts no caller-controlled
arguments, validates the fixed root-owned configuration and UFW executable,
and returns only typed status fields. The Ubuntu 26.04 gate ran as the real
`lightningos` service user and left both the UFW status hash and rules-file
timestamp unchanged. Evidence is in
`docs/baselines/privilege-hardening-phase3-manager-firewall-status-2026-08-13.json`.
The LND upgrade launch moved next to the serialized `upgrade.lnd.start`
operation. The broker accepts only a bounded version and the exact
digest-pinned embedded helper, chooses a fixed architecture-specific official
archive, installs the helper atomically at its fixed root-owned path, and
launches only one of two fixed units. The manager's LND upgrade direct sudo,
`systemd-run`, and privileged-shell budgets are now zero, and its direct helper
sudoers authorization was removed from fresh, existing-node, Raspberry Pi, and
in-place upgrade paths.

Before extraction, the helper authenticates the official LND manifest with a
quorum of at least five signatures. Fourteen full primary-key fingerprints are
compiled as trust anchors; matching-tag ASCII key material is accepted only
after exact fingerprint validation, and a signing subkey counts only when GPG
links it to that pinned primary key. The selected archive SHA-256 and safe
archive paths are then checked before extraction. A `verify-only` gate on the
Ubuntu 26.04 disposable clone authenticated `v0.21.1-beta` with six pinned
signers and left the `lnd`/`lncli` hashes and stopped LND service state
unchanged. A modified helper was rejected before execution. Evidence is in
`docs/baselines/privilege-hardening-phase3-lnd-upgrade-2026-08-13.json`.

The Tor path now uses the serialized `packages.tor.refresh` and
`upgrade.tor.start` operations. The former exposes no package or argument and
runs one fixed APT refresh command. The latter accepts only the exact
digest-pinned embedded helper and a boolean `verify_only`, installs it
atomically at a fixed root-controlled path, and launches one of two fixed
units. The helper requires HTTPS/TLS 1.2, validates the exact official Tor
repository primary-key fingerprint, exports only that key, and authenticates
the downloaded `InRelease` with `gpgv` before installing a keyring, source, or
package. The manager's Tor direct sudo, `systemd-run`, and privileged-shell
budgets are now zero.

The Ubuntu 24.04 `noble` verify-only gate ran as the real `lightningos`
service user. It authenticated the official repository metadata and pinned
key, rejected a modified helper and a caller-selected URL, and preserved the
Tor package, APT source/keyring/metadata, Tor PID, service state, and restart
count. Evidence is in
`docs/baselines/privilege-hardening-phase3-tor-upgrade-2026-08-13.json`.

The LightningOS self-upgrade launch now uses the serialized
`upgrade.lightningos.start` operation. The manager no longer installs or
executes the helper, invokes `sudo`/`systemd-run`, or falls back to a privileged
status call. The broker accepts only the digest-pinned embedded helper, a
normalized version, its matching tag, a full lowercase commit, and
`verify_only`; it chooses a fixed helper path and fixed verify/upgrade units.
The repository is no longer an argument. Before build, the helper requires the
fixed repository tag to resolve to the expected full commit, requires the
source `version.txt` to match, builds the worktree from the full commit rather
than the mutable tag, and uses `npm ci` with the committed lockfile. Direct
helper sudoers grants were removed from every installer and in-place upgrade
variant.

The Ubuntu 24.04 service-user gate bound the historical `0.5.1-Beta` release
to commit `97240c30673039a119ae4740c57dc12dc68d0cae`, rejected an injected
repository and modified helper, and failed a wrong commit before build. The
manager/UI/config/sudoers/source cache/log/temp state and Manager/LND/Bitcoin
services were unchanged. Evidence is in
`docs/baselines/privilege-hardening-phase3-lightningos-upgrade-2026-08-13.json`.
That historical release and commit are unsigned, so the current gate provides
Git object integrity and tag-to-commit consistency, not an independent
publisher-authenticity proof. A signed release manifest or equivalent trusted
attestation, plus the real upgrade/rollback matrix, remains required.

The App Store shared-root repair is now the closed, serialized
`storage.apps.ensure` operation. It accepts no fields; the broker resolves the
fixed `lightningos` identity and reconciles only `/var/lib/lightningos`,
`apps`, and `apps-data` at mode `0750`. Linux traversal is descriptor-relative
with `O_NOFOLLOW`, so caller paths, uid/gid values, symlinks, commands, and
executables cannot reach the root operation. The manager's former privileged
`systemd-run install -d` path and permanent call budget are zero. The Ubuntu
24.04 service-user gate passed dry-run, injected-path and symlink rejection,
and temporary-tree owner/mode mutation without changing the real App Store
tree or Manager/LND/Bitcoin states. Evidence is in
`docs/baselines/privilege-hardening-phase3-app-storage-2026-08-13.json`.

SMART diagnostics now use the read-only `storage.smart.read` operation. The
manager has no direct `sudo smartctl` call. A requested bounded `/dev` basename
must independently match the broker's fixed `lsblk` inventory as a whole disk;
the broker then permits only fixed root-owned `smartctl -a <device>` and bounds
the unaudited raw response before the manager reduces it to existing health
fields. Flags, traversal, non-disk devices, symlinked executables, empty failed
reads, and oversized responses fail closed. The Ubuntu 24.04 service-user gate
passed without changing Manager/LND/Bitcoin states. Evidence is in
`docs/baselines/privilege-hardening-phase3-smart-read-2026-08-13.json`. The
now-unused smartctl wildcard grants were subsequently removed by the
coordinated Phase 4 socket cutover.

The asynchronous post-wallet LND metadata repair now uses the serialized
`storage.lnd.permissions.repair` operation and `handlers.go` has zero direct
sudo calls. The request is empty. The broker fixes the `lnd` identity, the
five permitted directories, `lnd.conf`, `tls.cert`, and the bounded mainnet
`*.macaroon` set; it opens by descriptor with `O_NOFOLLOW`, performs no
recursive walk, reads no contents, creates no paths, and excludes channel,
wallet, graph and backup databases. Ubuntu 24.04 passed real-tree dry-run plus
temporary-tree owner/mode, byte-preservation, unmanaged-file and symlink gates
without changing `/data/lnd` metadata or Manager/LND/Bitcoin states. Evidence
is in
`docs/baselines/privilege-hardening-phase3-lnd-permissions-2026-08-13.json`.
The unused direct helper sudoers grant was subsequently removed by the
coordinated Phase 4 socket cutover.

General service restarts and host reboot/poweroff now fail closed through the
typed broker boundary. The former direct `systemctl`, `sudo`, and transient
`systemd-run` compatibility chain was removed from `internal/system`; the
general handler restart/power budgets and system-boundary sudo budget are zero.
`service.restart` retains its fixed unit allowlist, while the new `host.power`
accepts only `reboot|poweroff` and schedules a fixed two-second transient action
so the completion audit and HTTP response precede the host state change.
Disabled/shadow calls perform no mutation. The disposable Ubuntu 24.04 gate and
secret-free evidence are recorded in
`docs/baselines/privilege-hardening-phase3-service-power-2026-08-13.json`.
The legacy installer sudoers entries were subsequently removed by the
coordinated Phase 4 socket cutover.

The one-time login-protection activation flow is now broker-only. The manager
calls the existing fixed `files.enable_login` operation and schedules the
manager restart through `service.restart`; outside `enforce` it returns a
service-unavailable error without changing disk or in-memory configuration.
The manager-side YAML rewrite, `sudo tee`, runtime
`/etc/sudoers.d/lightningos-auth-enable` creation, `visudo`, privileged shell,
and generic `systemd-run` fallback were deleted, reducing the file's permanent
privilege budget to zero. The existing real Ubuntu 24.04 activation and manager
restart gates cover the positive path; new disabled/shadow tests prove the
cutover fails closed. Evidence is in
`docs/baselines/privilege-hardening-phase3-auth-enable-2026-08-13.json`. The
Phase 4 cutover now removes a stale runtime sudoers file only after its expected
contents and root ownership are validated.

Runtime terminal credential rotation is now the closed
`terminal.credential.rotate` operation. The manager no longer stages a password
file or invokes a privileged helper/transient unit. The password is carried in
the broker's stdin JSON and then only through stdin to the fixed root-owned
`/usr/sbin/chpasswd`; it never enters argv, error details, or structured audit
fields. The requested operator must match the independently read `User=` of
the fixed `lightningos-terminal.service`, and a root operator is forbidden.
The subsequent terminal restart uses the existing typed service operation.
This removed the last two manager `runSystemd` calls and allowed deletion of
the generic wrapper itself. Ubuntu 24.04 accepted the real service-user dry-run,
rejected an injected command, preserved `/etc/shadow` and Manager/terminal
activation state, and proved the synthetic password fragment absent from the
gate audit. A real password/login test remains reserved for the final
disposable fresh-install matrix. Evidence is in
`docs/baselines/privilege-hardening-phase3-terminal-credential-2026-08-13.json`.

Policy application/reconciliation, installer artifact verification, the real
Tor and LightningOS upgrade/rollback matrices, release publisher attestation,
and the remaining package/storage paths are still open.

The now-unused manager-side `RunCommandWithSudo` and `WriteFileWithSudo`
implementations were removed after all runtime consumers reached zero. A
permanent AST guard now rejects reintroduction of either helper or the deleted
generic `runSystemd` wrapper anywhere under `internal/server` or
`internal/system`. The legacy installer-generated sudoers grants were then
removed by the transactional Phase 4 socket cutover.

Issue #34's Go and GoTTY installer slice is accepted. `install.sh`,
`install_existing.sh`, and `install_existing_pi.sh` now source one fixed local
verification library and pin the exact version, Linux architecture, artifact
name, HTTPS source, and SHA-256 for Go 1.24.12 and GoTTY 1.8.0. The hashes come
from the official Go download metadata and the upstream GoTTY release asset
digests. Downloaded bytes are authenticated before archive listing or
extraction, and the existing Go toolchain is not removed until verification
and tar validation succeed. Ubuntu 24.04 downloaded and authenticated all four
amd64/arm64 artifacts, rejected modified bytes, and preserved installed
binary hashes and service activation state. Evidence is in
`docs/baselines/privilege-hardening-phase3-installer-go-gotty-2026-08-13.json`.

Issue #34's NodeSource/i2pd repository slice is also accepted. All installer
variants now pin Node.js major 24 and replace the NodeSource remote setup
helper with an explicit `nodistro` deb822 source and a dedicated `signed-by`
keyring. The fresh installer likewise replaces the i2pd remote shell helper
with the Ubuntu-suite deb822 repository. Repository keys are downloaded only
over HTTPS/TLS 1.2 with HTTPS-only redirects, authenticated by both the exact
whole-file SHA-256 and pinned primary OpenPGP fingerprint, required to contain
one primary key, and dearmored before root-owned installation. Whole-file
authentication closes the parser behavior where a valid armored key followed
by trailing junk would otherwise retain a valid fingerprint.

The isolated Ubuntu 24.04 gate authenticated both real repositories and found
Node.js 24.19.0 plus i2pd 2.61.0 without writing system APT state. Wrong hashes,
wrong fingerprints, HTTP, a symlink destination, and trailing junk all failed
closed. The VM's newer existing Node.js, installed packages, binary hashes,
service state, and system repository files remained unchanged. Evidence is in
`docs/baselines/privilege-hardening-phase3-installer-apt-repositories-2026-08-13.json`.
The complete cross-version install matrices remain open.

Issue #34's LND installer release gate is now accepted. The obsolete
`scripts/upgrade-lnd.sh`, which accepted a caller URL and extracted an
unauthenticated archive, was removed. Fresh, existing-amd64, and existing-arm64
installers all install the single broker-embedded helper from
`internal/server/assets/upgrade-lnd.sh`. The fresh installer fixes LND at
`0.21.1-beta`, exposes no version or URL override, and selects the helper's
closed `--install-new` mode. Existing binaries cannot be overwritten by that
mode; an unparseable existing version fails closed and a newer version is not
downgraded.

The canonical helper requires at least five valid signatures from pinned LND
primary fingerprints, takes the archive checksum only from that authenticated
manifest, rejects unsafe archive paths, and validates the binary version before
staging. New binaries use root-owned same-filesystem staging and no-clobber
commits, with failure cleanup limited to files whose bytes match this run. A
real Ubuntu 24.04 container gate authenticated six signatures for official LND
0.21.1-beta, installed both binaries, and left no staging files. The VM host's
LND/Bitcoin/manager/PostgreSQL state and binary hashes were unchanged; the
container and temporary image were removed. Evidence is in
`docs/baselines/privilege-hardening-phase3-installer-lnd-release-2026-08-13.json`.

Issue #34's installer repository-authentication slice is complete. The shared
verifier now authenticates the complete clearsigned `InRelease` envelope and
its `gpgv` signature with the exact downloaded-key SHA-256 and pinned primary
fingerprint before installing a system keyring. The strict envelope gate was
added after a negative test proved that `gpgv` alone accepts trailing bytes.
NodeSource and i2pd gained this pre-install metadata gate; PGDG moved from HTTP
to HTTPS deb822 sources with the official full fingerprint; Tor moved to the
same closed flow and no longer falls back silently to `jammy`.

The Ubuntu 24.04 gate authenticated NodeSource `nodistro` plus i2pd, PGDG, and
Tor metadata for both Ubuntu 24.04 `noble` and Ubuntu 26.04 `resolute`. A
trailing byte, a valid signature from the wrong repository key, and an HTTP
metadata URL all failed before keyring installation. Isolated APT state found
valid PGDG and Tor candidates while real system keyrings, sources, packages,
and service activation state stayed unchanged. Evidence is in
`docs/baselines/privilege-hardening-phase3-installer-repository-auth-2026-08-13.json`.
The remaining issue #34 work is the complete fresh-install, upgrade, and
negative matrix on both supported Ubuntu releases.

1. Replace arbitrary package arguments with fixed dependency sets.
2. Replace direct UFW access with the manager-access policy operation.
3. Replace ownership and permission shell commands with typed storage actions.
4. Move LND, Tor, and LightningOS upgrades to verified broker operations. The
   three launches are brokered; Tor/LightningOS real upgrade and rollback
   matrices plus LightningOS publisher attestation remain open.
5. Verify LND archives against an authenticated official signed manifest and
   explicitly trusted release-signing keys before extraction. Complete for the
   brokered upgrade and every installer path.
6. Verify Go toolchain archives against the expected official checksum and pin
   each supported GoTTY version, architecture, artifact name, and checksum.
7. Replace direct NodeSource and i2pd `curl | bash` execution with explicit APT
   repository definitions and dedicated `signed-by` keyrings; any unavoidable
   downloaded helper must be authenticated before execution. Complete for all
   three installer variants, including whole-file key authentication.
8. Add negative tests proving that altered artifacts, checksums, manifests,
   signatures, keys, architectures, and repository helpers fail closed before
   privileged execution or installation.

Exit criterion: no manager code path invokes `apt`, `dpkg`, `ufw`,
`systemd-run`, or a privileged shell directly, and no root install or upgrade
path executes or installs a downloaded artifact before its required
authenticity/integrity verification succeeds.

### Phase 4 — Privilege removal and systemd confinement

1. Remove wildcard sudoers entries and `SupplementaryGroups=docker`.
2. Remove any remaining generic `systemd-run` authorization.
3. Tighten the manager service with `NoNewPrivileges`, a strict filesystem
   view, private devices, kernel/control-group protections, restricted address
   families, and an audited syscall policy.
4. Give the broker a separate, narrowly scoped service policy and writable
   paths.

Accepted slice (2026-08-13): the manager no longer executes the broker through
`sudo`. It connects to a root-owned, systemd-activated AF_UNIX socket whose peer
UID is checked against the canonical `lightningos` account before any request is
decoded. Fresh installers install and enable the socket units and no longer
generate manager sudoers policy. The in-place upgrade creates a schema-versioned
root-only rollback bundle before replacing either binary, validates recognized
legacy sudoers files, self-tests the new transport as the service user, removes
the manager and stale login-enable sudoers files, removes Docker membership, and
rolls back automatically on a failed restart or health gate. Unrecognized
manager identities and sudoers layouts fail closed before cutover.

The manager template and upgrade drop-in now apply `NoNewPrivileges`, strict
filesystem protection, private devices, empty capability sets, kernel and
control-group protections, restricted address families, and an audited syscall
deny policy. The broker has a separate socket-activated root service policy. A
disposable Ubuntu 24.04 gate passed forward operation, unauthorized-peer and
direct-sudo rejection, manager health, boundary assertions, and full rollback.
The corresponding Ubuntu 26.04 gate passed the same matrix with an exact
byte-for-byte rollback of all captured boundary artifacts. That gate first
failed closed on a legitimate historical sudoers variant; recognition was
limited to the known paired LightningOS LND/application upgrade helpers. It also
found and fixed restoration of the previously captured rollback helper before
acceptance. Docker was not restarted on either VM and the pre-existing LND
activation cycles were not caused or changed intentionally. Evidence is in
`docs/baselines/privilege-hardening-phase4-socket-cutover-2026-08-13.json`.

The Phase 4 operating-system cutover gates are complete. Phase 4 remains open
only for the final supported-upgrade and application regression matrices.

Exit criterion: a test running as the manager user cannot access the Docker
socket, launch a root process, modify root-owned configuration, alter the
firewall, or install a package except through an allowed broker operation.

### Phase 5 — LND credential separation

1. Inventory the exact LND RPC permissions needed by the manager and each app.
2. Replace the manager's admin macaroon where practical with a constrained
   manager macaroon.
3. Issue app-specific macaroons and mount them read-only.
4. Keep explicit warnings for apps that demonstrably require elevated LND
   access.

Inventory checkpoint (2026-08-13): a source scan matched 70 authenticated
manager RPC method names to 75 service/method rows from the 163-method
`ListPermissions` map returned read-only by LND `0.21.2-beta.rc1` on LOS TESTE2.
The manager currently remains the only application process configured with
`admin.macaroon`. Its observed RPC surface requires 17 of the 19 permission
pairs exposed by that LND build; `info:write` and `macaroon:write` were not used.
`GenSeed`, `InitWallet`, and `UnlockWallet` use the intentionally
unauthenticated WalletUnlocker connection.

BTCPay, Fedimint Gateway, LNbits, LNDg, Lightning Loop, PeerSwap, and Tapd each
have a named dedicated macaroon, an explicit permission set, and an admin-byte
equality rejection gate. No App Store runtime mounts the native admin macaroon.
BRLN Loop Out and Magma remain native manager features, receive no separate LND
credential or data-directory mount, and require no third-party disclaimer. The
exact inventory is recorded in
`docs/baselines/privilege-hardening-phase5-lnd-credentials-2026-08-13.json`.

The transactional manager migration is now implemented with closed broker
operations, a unique revocable root key, the fixed 17-permission set, atomic
configuration updates, and root-only transaction state. The credential lives
at `/var/lib/lightningos-credentials/lnd/manager.macaroon` as
`root:lightningos:0640`; its root-owned ancestor prevents the manager from
replacing the boundary. After commit, native `admin.macaroon` is
`lnd:lnd:0600`. Upgrade rollback schema v3 records metadata and existence only,
never credential bytes, and restores the previous path while revoking the
dedicated root key. An unavailable or locked LND returns `pending` without
filesystem/config mutation.

The real existing-wallet ensure/rollback gate passed on LOS TESTE2. The root
key count increased only for the migration and returned to baseline on
rollback; native admin bytes/metadata, manager/LND PIDs, and HTTPS health were
preserved without restarting Bitcoin, LND, or manager. Evidence is in
`docs/baselines/privilege-hardening-phase5-manager-credential-2026-08-13.json`.

The fresh-wallet and committed-reboot gate then passed on a disposable Ubuntu
24.04 VM. The first-unlock hook now retries bounded transient RPC/config races,
and the broker's `ProtectSystem=full` policy grants its transaction writer only
the exact `/etc/lightningos` exception needed for the atomic configuration
switch. Wallet creation and initialization returned HTTP 200; the credential
converged automatically without a manual ensure, survived a real reboot, and
remained idempotent. Post-reboot broker rollback revoked and removed the
dedicated credential, restored the native admin path/metadata, preserved the
manager, LND and isolated Bitcoin processes, and kept HTTPS health. Phase 5
remains open only for the final supported Ubuntu 24.04/26.04 matrix. Evidence
is in
`docs/baselines/privilege-hardening-phase5-first-unlock-reboot-2026-08-13.json`.

Exit criterion: compromising one app does not automatically grant another
app's permissions, host administration, or unrestricted LND administration.

## Migration and compatibility policy

The migration must be transactional:

1. Detect installation type, service user, LND unit, data roots, Docker state,
   and required app manifests.
2. Install and validate the broker without changing current privileges.
3. Exercise a non-destructive broker self-test.
4. Switch manager call sites and verify health.
5. Remove old sudoers rules and Docker group membership last.
6. Preserve a root-run rollback command that restores the previous service and
   sudoers files without touching LND or app data.

Supported acceptance targets are new `install.sh` nodes on Ubuntu 24 and Ubuntu
26 plus in-place upgrades from a declared LightningOS release baseline.
Generic support for arbitrary `install_existing.sh` layouts is not an acceptance
requirement. Recognized existing layouts may receive a guided migration;
unrecognized layouts must stop before the cutover with actionable diagnostics.

## Required validation

- Unit and fuzz tests for broker argument, path, CIDR, version, and manifest
  validation.
- Tests for command injection, traversal, symlinks, malicious app IDs, unknown
  units, arbitrary packages, mounts, images, and firewall rules.
- Disposable-VM tests on Ubuntu 24 and Ubuntu 26.
- Full App Store lifecycle matrix, including reboot persistence and stopped-app
  persistence.
- LND restart, upgrade, maintenance mode, wallet, channel, rebalance, report,
  notification, and terminal regression tests.
- Failure injection during package operations, app install, upgrade, service
  restart, and privilege cutover.
- Verification that no seed, macaroon, password, RPC credential, or token enters
  broker arguments or logs.

## Test-node preservation protocol

The existing LightningOS test node is the integration target, not a disposable
fixture. It remains on the stable `0.5.2` line until the Phase 1 broker can run
in shadow mode without removing any existing privilege. Merely creating this
branch is not a reason to deploy `0.5.3` to that node.

The following state must be preserved throughout hardening work:

- Bitcoin Core and LND blockchain, graph, wallet, and channel data;
- the configured local/remote Bitcoin source and storage targets;
- installed app data, configuration, and the operator's running/stopped choice;
- TLS, local CA, hostname discovery, LAN/Tailscale access, and firewall policy;
- Postgres databases, reports, notifications, audit history, and UI settings.

Before the first runtime hardening deployment, capture a secret-free baseline:

- LightningOS version, commit, install type, Ubuntu version, architecture, and
  active systemd units;
- `lightningos` user/group membership and effective sudo command list;
- manager, LND, terminal, Docker, Postgres, UFW, and Tailscale service state;
- Docker containers, images, networks, volumes, and App Store running/stopped
  state;
- LND `GetInfo`, wallet/channel totals, and Bitcoin synchronization height;
- owner, group, mode, and path metadata for managed files without reading secret
  contents.

Immediately before privilege cutover, create a root-only, timestamped rollback
bundle containing the previous manager binary, systemd units, sudoers files,
group membership, broker files, and non-secret configuration. LND, Bitcoin,
Postgres, and app data directories are never copied, deleted, reset, reindexed,
or ownership-rewritten as part of this rollback bundle.

Test checkpoints:

1. **T0 — baseline:** current manager and apps pass health checks unchanged.
2. **T1 — shadow broker:** broker is installed and self-tested; all legacy
   privileges remain available and the manager behavior is unchanged.
3. **T2 — selective migration:** one operation family at a time uses the broker;
   the previous path remains available for immediate rollback.
4. **T3 — App Store cutover:** every installed and uninstalled catalog app passes
   lifecycle tests before direct Docker access is removed.
5. **T4 — privilege removal:** wildcard sudo and Docker group access are removed
   only after broker and rollback verification in the same maintenance window.
6. **T5 — confinement:** stronger systemd restrictions are applied individually,
   with health and regression checks after each restriction.

Operations that can interrupt LND, Bitcoin, Docker, the network, or the host
require an announced test window. Bitcoin must never be deliberately stopped for
hardening tests. LND and the manager may be restarted when the checkpoint calls
for it. Apps may be stopped only when required for their own lifecycle test and
must finish in their original running/stopped state.

The local test network is outside the mutation scope. Agents must use the
router-reserved address recorded outside Git and must not scan the LAN, renew
DHCP, reset host/guest adapters, or change VirtualBox bridge/NAT settings while
locating a test VM. Read-only neighbor-table inspection is also avoided once a
reserved address is available.

Destructive and failure-injection tests run first on a fresh disposable VM. The
preserved integration node receives only a phase that has already passed that
VM gate. No credential, IP address, token, macaroon, password, or private node
identifier is stored in this plan, Git history, PR comments, or test output.

## Completion criteria

This issue is complete only when:

1. the manager has no wildcard sudo permission;
2. the manager is not in the Docker group and cannot access the Docker socket;
3. all privileged operations use the validated broker;
4. the hardened systemd policy is enabled by default;
5. the supported install and upgrade matrix passes;
6. unsupported legacy layouts fail before destructive or lockout-prone changes;
7. the security model and operator documentation describe the remaining trust
   boundaries accurately.
