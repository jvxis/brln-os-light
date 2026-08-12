# Phase 2 App Store privilege inventory

Captured from `internal/server/apps_registry.go`, the app handlers, and
`internal/server/apps_docker.go` on 2026-08-10. This document is secret-free.

## Catalog shape

The registry exposes 20 app IDs through the common `Info`, `Install`,
`Uninstall`, `Start`, and `Stop` interface.

| Family | App IDs | Current lifecycle boundary | Additional privileged dependencies |
| --- | --- | --- | --- |
| Docker Compose | `bitcoincore`, `bark-wallet`, `electrs`, `mempool`, `fedimint-guardian`, `fedimint-gateway`, `lndg`, `lnbits`, `btcpay`, `robosats`, `publicpool`, `cpuminer`, `tapd` | Generic `runCompose(root, composePath, args...)` with caller-selected paths and argument slices | Docker/Compose installation, image probes and pulls, container inspect/stats/exec/update, Docker network inspection, UFW, LND compatibility changes, and storage repair, depending on the app |
| Native systemd (migrated) | `loop`, `elements`, `peerswap` | Closed typed broker operations; no direct privileged manager call | Final Ubuntu 24.04/26.04 clean-install, reboot, and shared cutover matrix |
| In-process feature toggle | `depixbuy`, `fswap`, `loopout-brln`, `magma-sales` | Manager service state/configuration | Persistent database or fixed environment state; no container lifecycle |

The 13 Compose apps do not share one safe free-form command shape. Observed
fixed behavior includes whole-project `up -d`, `stop`, and `down`; destructive
`down --volumes`; service-specific start/stop; build; `up --no-start`; Bitcoin
dependency restarts; and container/network inspection. These must become
separate catalog capabilities rather than an argument allowlist.

## First broker manifest

The first migration slice is deliberately limited to `cpuminer` `start` and
`stop` through protocol operation `app.compose.lifecycle`.

- The request contains only `app_id` and the `start` or `stop` enum.
- The manager and broker share the exact catalog Compose document from
  `internal/appmanifest`; the broker rejects any byte change.
- The broker rejects missing, extra, or duplicated environment keys, unknown
  images, non-catalog pool targets, invalid payout/worker values, invalid thread
  counts, non-regular files, and symlinks.
- After validation, Compose receives root-owned temporary snapshots. It never
  receives the manager-writable manifest or environment paths.
- Executable choices are fixed to `/usr/bin/docker compose` and the compatibility
  fallback `/usr/bin/docker-compose`; callers cannot select either path or add
  arguments.
- `shadow` performs validation only and preserves the legacy lifecycle path.
  `enforce` executes the broker action and fails closed.

The second CPU Miner slice adds the read-only `app.compose.inspect` operation.
In `enforce`, both App Store status and the dedicated CPU Miner status endpoint
receive only `running`/`stopped` and raw CPU percentage from the broker. The
broker uses validated snapshots, fixed status/container queries, and a strictly
validated container ID for the fixed stats command. Hashrate/share counters
already come from the miner's localhost TCP API and require no privilege.

CPU Miner config and thread updates now write only their existing manager-owned
catalog files and reuse the typed start operation for Compose apply. In
`enforce`, they make no direct Docker call from the manager. Their disposable
gate passed without the manager Docker GID, including worker sanitization and
fail-closed rejection of an invalid retained pool mode. The gate also fixed the
catalog service grace period at two seconds so config-driven recreation stays
inside the broker deadline.

CPU Miner install, uninstall, image probes/preparation, and first-container
creation have also moved behind typed broker operations. Its migrated lifecycle
is complete for the current app contract. The legacy implementation is retained
only for `disabled` and `shadow` compatibility while Phase 2 continues.

## Shared Compose catalog and RoboSats

The first generalized Compose slice moves RoboSats `status`, `start`, and
`stop` onto the same closed catalog boundary as CPU Miner.

- `ComposeManifestForApp` is the only admission point for lifecycle and inspect
  requests; unknown app IDs still fail before command execution.
- The manager and broker share the exact RoboSats Compose and Caddy documents,
  three pinned images, fixed project/service names, port, and stop timeout.
- The broker rejects changed Compose/Caddy content, non-regular files,
  symlinks, malformed certificates, and certificate/private-key mismatches.
- RoboSats data uses a Docker named volume. No manager-writable host data path
  reaches a root Docker bind mount.
- Compose, Caddy, certificate, and private-key execution copies live below
  `/var/lib/lightningos-privileged/apps/robosats`. Every component is checked
  against symlinks; directories are root-only and file replacement is atomic.
- Existing manager-owned catalog files are refreshed before typed start/stop,
  allowing an installed legacy manifest to converge without Docker access.
- All three images must already exist locally before typed start. Install now
  uses the closed Docker package/runtime feature, prepares the three catalog
  images asynchronously through fixed transient units, and creates the first
  containers through typed lifecycle start.
- The manager never supplies an image name, unit name, executable, or pull
  argument. The broker maps only the `client`, `tor`, and `proxy` variants to
  the three pinned images and fixed unit names.
- RoboSats external firewall access now uses a typed `app.firewall.ensure`
  operation. Typed uninstall now completes its current app contract.

The disposable Ubuntu 24.04 gate deliberately caught and rejected an initial
temporary `/proc/<pid>/fd` design: Docker container creation can outlive the
broker process, and restart policies require bind sources to remain present.
The persistent root-owned snapshot plus named-volume design then passed two
start/stop cycles, broker status, and HTTPS checks with direct manager Docker
access denied.

A follow-up gate removed the three images and preserved the previous app roots
as recoverable backups before calling the install API. Typed Docker readiness,
three asynchronous image pulls, manager-owned catalog asset generation, and
brokered first-container creation completed in `enforce`. A request containing
a caller-supplied image field was rejected, all transient units were collected,
and stop/start plus HTTPS passed without the manager Docker GID. The pre-existing
firewall rule and named volume were deliberately retained, so this slice does
not claim firewall migration or clean-data provisioning.

The firewall follow-up adds a catalog-only external TCP port lookup. The
request carries only `app_id`; the broker fixes `/usr/sbin/ufw`, `status`,
`allow`, TCP, and port `12596`. An inactive or unavailable UFW is a compatible
no-op, while active UFW receives the exact catalog rule. On the disposable gate,
inactive start succeeded without a rule; after adding a temporary SSH guard and
activating UFW, typed start created only the expected IPv4/IPv6 `12596/tcp`
rules and HTTPS returned 200. A caller-supplied `port: 22` was rejected. The
test rules were removed and UFW returned to its original inactive state.

The legacy global sudoers wildcard for UFW remains because other App Store apps
still depend on it. This slice removes direct UFW execution from the RoboSats
`enforce` path; global privilege removal is intentionally deferred until every
consumer is migrated.

RoboSats uninstall now reuses `app.compose.remove` with the same strict asset
validation and persistent root-owned execution snapshot. The broker executes a
fixed `down --remove-orphans --timeout 2`, never admits `--volumes`, and removes
its execution snapshot only after Compose succeeds. The manager then removes
only its own catalog directory. A failed Compose call preserves both trees for
retry. The disposable gate removed three stopped containers and the project
network, removed both catalog/execution roots, and left all five named volumes
intact. A marker in `robosats_robosats-data` survived uninstall and was removed
after the proof; a caller-supplied `volumes: true` field was rejected.

This slice still does not authorize global removal of Docker access from the
manager; other App Store handlers retain explicit Phase 2 work.

## Bitcoin Core image preparation

Bitcoin Core starts with a narrower catalog slice because its lifecycle also
owns a configurable blockchain data path, a storage-identity guard, RPC
credentials, configuration mutation, a public P2P port, and a Docker network
consumed by Electrs, Mempool, BTCPay, Fedimint, and Public Pool. Image
preparation can be separated safely from those contracts.

- The former `bitcoin/bitcoin:latest` reference is not merely pinned: the
  third-party registry image is removed from the execution contract. Docker
  Hub labels it unofficial, so the earlier `bitcoin/bitcoin:31.1` gate is
  retained only as superseded historical evidence.
- The manager selects only `bitcoincore` plus the `node` variant. The broker
  maps those values to local image `lightningos/bitcoin-core:31.1` and the
  fixed `lightningos-bitcoincore-image-node` transient unit; the local tag is
  never pulled from a registry.
- The broker downloads the official architecture-specific archive and signed
  checksum bundle from `bitcoincore.org`, pins seven Guix builder primary-key
  fingerprints, requires at least three distinct valid signatures, checks the
  exact archive SHA-256, and builds from a platform-specific Debian base
  digest. Docker build steps receive `--network=none`.
- Image readiness requires a root-owned attestation containing the release,
  archive hash, base digest, valid-signature count, and exact Docker image ID.
  An unattested same-tag image, malformed/symlinked attestation, insufficient
  signatures, or any metadata/image mismatch is never reported ready.
- Docker package/runtime readiness now uses the existing typed broker path
  before Bitcoin Core installation. In `enforce`, image preparation fails
  closed and cannot fall back to direct Docker execution or a registry pull.
- The disposable Ubuntu 24.04 provenance gate observed seven valid signatures,
  produced a root-owned `0600` attestation, matched the attested Docker image
  ID, reported daemon and CLI 31.1.0, and ran bitcoind as UID 101. Ephemeral
  `regtest` tests passed RPC, both ZMQ publishers, and a P2P handshake between
  two nodes on an internal Docker network. Temporary containers/networks were
  removed, the verified image was retained, the unofficial image was removed,
  and no App Store root or persistent blockchain data was created.

Bitcoin Core storage enrollment is now typed separately as
`app.bitcoincore.storage.ensure`. The only caller field is a canonical
`data_dir`; custom targets must resolve to a mounted non-root filesystem with
at least 10 GiB free, and every existing path component must be a real
directory rather than a symlink. The broker creates the data directory as
UID/GID 101, generates the identity itself, persists the target and identity
under its root-only app tree, and writes only the matching marker into the
selected storage. A second target, root-filesystem fallback, symlink, partial
metadata, or caller-supplied identity fails closed before target mutation.

The Ubuntu 24.04 gate passed mutation-free dry-run, real and repeated
enrollment on an isolated `nodev,nosuid,noexec` tmpfs, identity/mode checks,
all negative requests, sanitized responses, and parameter-free audit events.
Cleanup removed the tmpfs, marker, and storage metadata while preserving the
official image attestation; Bitcoin never started and LOS-TEST2 was untouched.
Secret-bearing `bitcoin.conf` read/write is the next slice. Lifecycle admission
follows only after config and fixed Compose mounts are independently validated.

## Accepted native Elements boundary

Elements has moved out of the generic native-systemd bucket. The broker now
owns the operator-selected default or distinct/external root-enrolled mounted
storage target, the dedicated `lightningos-elements` identity, digest-pinned
official release binaries,
bounded extraction, safe existing-config merge, post-enrollment config reads,
the hardened fixed unit, two allowlisted read-only RPC status calls, lifecycle,
and removal. The manager-side Elements files have zero direct privileged calls
and their Phase 0 budget entries were removed. LOS TESTE2 proved the real
existing-node migration while inactive/disabled, with config/credential hashes
and Bitcoin/LND timestamps unchanged. Evidence is in
`docs/baselines/privilege-hardening-phase2-elements-boundary-2026-08-12.json`.

## Accepted native PeerSwap boundary

PeerSwap has also moved out of the generic native-systemd bucket. Its manager
files contain no direct privileged calls. The broker fixes the upstream
v6.0.0 binary plus official PSWeb `main` commit `09983da398f253f8c14213e9f5c61b80cc879b67`
(packaged as v6.0.0.1) and hashes while retaining the legacy packaging
folder, migrates regular legacy state into a dedicated runtime, creates the
`lightningos-peerswap` identity, installs a dedicated nine-permission LND
credential, validates and merges both config formats, owns the two hardened
units, and exposes typed status/source/lifecycle/removal plus fixed firewall.

The root-only source contract preserves both local store-managed Elements with
its enrolled default or distinct external volume and a remote/external
Elements RPC. LOS TESTE2 proved the latter without starting PeerSwap or local
Elements: the old state matched its new runtime copy, remote Liquid RPC and
dedicated LND reads succeeded, macaroon administration failed, source policy
became `root:root 0600`, and Bitcoin/LND timestamps were unchanged. Evidence:
`docs/baselines/privilege-hardening-phase2-peerswap-boundary-2026-08-12.json`.

## Migration order

1. Prove CPU Miner start/stop and negative validation on a disposable node.
2. Prove the fixed CPU Miner status/inspection capability on a disposable node.
   Completed on Ubuntu 24.04 with the manager process verified without the
   Docker GID; see the inspection enforce baseline.
3. Add typed install, uninstall, image probe, and first-container creation
   capabilities to complete CPU Miner. Completed.
4. Generalize the catalog schema for simple whole-project Compose apps.
   The shared schema plus RoboSats status/start/stop, image preparation, install,
   first-container creation, firewall, and uninstall are complete.
5. Add service-specific and dependency capabilities for complex Compose apps.
   Bitcoin Core image preparation is complete; its storage/configuration
   boundary is next.
6. Separate Docker package installation, image management, networking,
   firewall, storage, and LND compatibility operations.
7. Run the complete per-app lifecycle and reboot-state matrix on Ubuntu 24 and
   Ubuntu 26.
8. Remove direct Docker calls and manager Docker-group membership only after
   every supported app passes.

## Acceptance gates for the migrated slices

- strict protocol rejection of unknown apps, actions, fields, paths, and args;
- manifest/environment tampering and symlink rejection before command execution;
- mutation lock and paired audit events for real lifecycle calls;
- no command execution or mutation lock in `dry_run`;
- fixed executable and arguments operating only on validated snapshots;
- status and lifecycle success in `enforce` while the manager process has no
  Docker supplementary group;
- fail-closed inspection of a tampered manifest and strict rejection of an
  unknown app and caller-supplied argument array;
- thread and config apply without the Docker GID, bounded config recreation,
  worker sanitization, and fail-closed rejection of an invalid retained pool;
- full Go tests, vet, registry validation, and privilege-boundary budgets;
- disposable-VM start/stop/status with final config, group, app, and Docker
  service state restored.
- RoboSats execution files persist under a root-only tree, data uses a named
  volume, no manager-writable path reaches Docker, all three fixed images are
  local, and repeated stop/start plus HTTPS survive without the Docker GID.
- RoboSats install prepares only the three closed image variants through fixed
  transient units, creates the first containers through the typed lifecycle,
  rejects caller-supplied image fields, and leaves no transient unit loaded.
- RoboSats active/inactive firewall behavior passes through a fixed broker
  command, port injection is rejected, and the gate restores its original UFW
  state after proving external HTTPS access.
- RoboSats uninstall removes containers, network, and both execution/catalog
  roots without `--volumes`; named data survives, injected volume deletion is
  rejected, and a failed broker removal preserves retry state.
- Bitcoin Core image preparation accepts only the catalog `node` variant,
  resolves an official release, signed checksums, fingerprint-pinned builders,
  architecture hash, base digest, local image, and fixed transient unit inside
  the broker. It requires a matching root-only attestation, fails closed in
  `enforce`, rejects caller-supplied image fields, and does not create an App
  Store root or blockchain data directory.
