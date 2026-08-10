# Phase 2 App Store privilege inventory

Captured from `internal/server/apps_registry.go`, the app handlers, and
`internal/server/apps_docker.go` on 2026-08-10. This document is secret-free.

## Catalog shape

The registry exposes 20 app IDs through the common `Info`, `Install`,
`Uninstall`, `Start`, and `Stop` interface.

| Family | App IDs | Current lifecycle boundary | Additional privileged dependencies |
| --- | --- | --- | --- |
| Docker Compose | `bitcoincore`, `bark-wallet`, `electrs`, `mempool`, `fedimint-guardian`, `fedimint-gateway`, `lndg`, `lnbits`, `btcpay`, `robosats`, `publicpool`, `cpuminer`, `tapd` | Generic `runCompose(root, composePath, args...)` with caller-selected paths and argument slices | Docker/Compose installation, image probes and pulls, container inspect/stats/exec/update, Docker network inspection, UFW, LND compatibility changes, and storage repair, depending on the app |
| Native systemd | `elements`, `peerswap`, `loop` | Generic privileged systemd and shell helpers | Units, binaries, config/data trees, users/groups, ownership, firewall, and credential material |
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
  operation. Uninstall remains its final open capability.

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

This slice still does not authorize global removal of Docker access from the
manager; other App Store handlers retain explicit Phase 2 work.

## Migration order

1. Prove CPU Miner start/stop and negative validation on a disposable node.
2. Prove the fixed CPU Miner status/inspection capability on a disposable node.
   Completed on Ubuntu 24.04 with the manager process verified without the
   Docker GID; see the inspection enforce baseline.
3. Add typed install, uninstall, image probe, and first-container creation
   capabilities to complete CPU Miner. Completed.
4. Generalize the catalog schema for simple whole-project Compose apps.
   The shared schema plus RoboSats status/start/stop, image preparation, install,
   first-container creation, and firewall are complete; uninstall remains open.
5. Add service-specific and dependency capabilities for complex Compose apps.
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
