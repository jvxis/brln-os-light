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
The remaining apps, Ubuntu 26 execution, and final Docker-group removal remain
open; Phase 2 is not complete. Evidence is stored in
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

Secret-bearing `bitcoin.conf` handling now uses three separate typed broker
operations: `app.bitcoincore.config.ensure`, `.read`, and `.write`. The caller
supplies only the enrolled `data_dir` and, for mutations, bounded config
content; it cannot select the destination filename. The broker verifies the
root-only enrollment metadata and storage marker, rejects symlinks, reads only
the fixed config, and commits updates atomically as `root:101` mode `0640`.
The manager's root-container read/write ladder and manager-side temporary
secret file have been removed. Existing `101:101` configs are tightened
atomically without changing their contents, and the old manager-owned seed is
used only to preserve credentials before being removed after broker success.

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
release is explicitly cataloged as `btcpayserver/btcpayserver:2.4.1`.
BTCPay's `server` variant nevertheless performs a real pull on every requested
install/start and status checks the pull unit before accepting a cached image.
The version constant must be reviewed whenever upstream publishes a stable
release; the root broker never discovers or accepts an arbitrary tag at
runtime. NBXplorer 2.6.8, PostgreSQL 16, and Tor 0.4.9.5 remain fixed and
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

The full BTCPay+LND gate, full-node Electrs/Mempool gates, the remaining
dependent products, operational Bitcoin CLI/log paths, and the mainnet P2P
firewall contract remain open. Electrs and Mempool gates must use a synchronized
unpruned node with `txindex=1`; regtest is acceptable only as an isolated test
chain satisfying that same full-index contract.

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

1. Replace arbitrary package arguments with fixed dependency sets.
2. Replace direct UFW access with the manager-access policy operation.
3. Replace ownership and permission shell commands with typed storage actions.
4. Move LND, Tor, and LightningOS upgrades to verified broker operations.
5. Verify LND archives against an authenticated official signed manifest and
   an explicitly trusted release-signing key before extraction.
6. Verify Go toolchain archives against the expected official checksum and pin
   each supported GoTTY version, architecture, artifact name, and checksum.
7. Replace direct NodeSource and i2pd `curl | bash` execution with explicit APT
   repository definitions and dedicated `signed-by` keyrings; any unavoidable
   downloaded helper must be authenticated before execution.
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
