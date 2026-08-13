# LightningOS privileged broker protocol

## Phase 1 foundation

The first broker foundation is implemented on the
`agent/0.5.3-privilege-hardening` branch. It installs a root-owned helper at
`/usr/local/libexec/lightningos-privileged` and adds one sudoers command with
an explicitly empty argument list:

```sudoers
/usr/local/libexec/lightningos-privileged ""
```

The helper accepts no command-line arguments. One JSON request is read from
standard input and one JSON response is written to standard output. The
manager never supplies an executable path or an arbitrary argument array.

This is a migration interface. The target architecture remains a protected
Unix socket with peer credential checks after the operation catalog and node
compatibility have stabilized.

## Authorization and runtime files

- The helper must have effective UID 0.
- Sudo callers must be the installed `lightningos` account, with matching
  `SUDO_USER` and `SUDO_UID`. Direct root invocation is permitted for installer
  self-tests and recovery. Installer self-tests clear inherited `SUDO_*`
  variables so a root shell originally opened by another operator is not
  mistaken for a broker request from that operator.
- The audit log is `/var/log/lightningos-privileged/audit.jsonl`, mode `0600`,
  below a root-owned non-writable parent.
- The mutation lock is `/run/lock/lightningos/privileged.lock`, mode `0600`,
  below a root-owned non-writable parent.
- `/etc/tmpfiles.d/lightningos-privileged.conf`, mode `0644`, recreates the
  root-owned `/run/lock/lightningos` directory with mode `0750` at every boot.
  All fresh, existing-node, Raspberry Pi, and application-upgrade paths install
  and apply this rule before broker self-test.
- Both files are opened with `O_NOFOLLOW`; existing non-regular, non-root-owned,
  or broadly writable files are rejected.
- Mutating operations are serialized. Broker execution is capped at 30
  seconds; the service foundation uses 15 seconds internally and the manager
  client defaults to 5 seconds.

Audit records contain timestamp, phase, caller, request ID, typed operation,
dry-run state, completion status, duration, and a stable error code. Request
parameters and command output are not logged.

## Request envelope

The current protocol version is `1`. Messages larger than 64 KiB, unknown
fields, multiple JSON values, unknown versions, malformed request IDs, missing
parameters, and unknown operations are rejected.

```json
{
  "version": 1,
  "request_id": "9d43f4dc69b94d21a33e056293df48ce",
  "operation": "service.restart",
  "dry_run": true,
  "params": {
    "unit": "lnd",
    "no_block": true
  }
}
```

`request_id` must contain 1 to 64 ASCII letters, digits, underscores, or
hyphens. `params` must be an object. `dry_run` is accepted only by mutating
operations that explicitly implement it.

## Response envelope

Successful response:

```json
{
  "version": 1,
  "request_id": "9d43f4dc69b94d21a33e056293df48ce",
  "ok": true,
  "result": {
    "validated": true
  }
}
```

Rejected response:

```json
{
  "version": 1,
  "request_id": "9d43f4dc69b94d21a33e056293df48ce",
  "ok": false,
  "error": {
    "code": "invalid_request",
    "message": "service unit is not allowed"
  }
}
```

The client requires the response request ID to match. It does not expose the
helper's stderr or privileged command output in API responses.

## Version 1 operations

### `self_test`

Non-mutating installer and recovery check. Parameters must be `{}`. It verifies
protocol decoding, caller authorization, runtime path validation, and audit
availability without changing a service.

### `service.status`

Runs the fixed command `/usr/bin/systemctl is-active <unit>` for an allowlisted
unit. No caller-supplied options are accepted.

### `service.restart`

Runs one of these fixed argument shapes for an allowlisted unit:

```text
/usr/bin/systemctl restart <unit>
/usr/bin/systemctl restart --no-block <unit>
```

Restarting `lightningos-manager` is necessarily asynchronous because the
broker process starts inside the manager's systemd cgroup. That unit requires
`no_block: true` and uses one fixed transient command instead:

```text
/usr/bin/systemd-run --quiet --collect \
  --unit=lightningos-manager-restart-<request_id> --on-active=1s \
  /usr/bin/systemctl restart lightningos-manager
```

The validated request ID is the only derived fragment in the transient unit
name. The one-second delay lets the broker write its successful completion
audit and lets the HTTP handler return before systemd stops the manager. The
transient timer and service are collected after execution.

With `dry_run: true`, the request is validated and audited but no lock or
command execution occurs.

The initial unit allowlist covers the units already reachable through the
manager restart helpers: LND (`lnd` and `lnd@default`), manager, Postgres,
Autofee, Elements, Peerswap, PSWeb, terminal, and the fixed upgrade units.
Shell syntax, paths, arbitrary unit suffixes, options, and unknown units are
rejected before execution.

### `packages.tor.refresh`

Accepts only `{}`. In a real request, the broker serializes and runs exactly:

```text
/usr/bin/apt-get -o DPkg::Lock::Timeout=60 update
```

No package, option, repository, environment, path, or command is supplied by
the manager. A dry-run validates the operation without taking the mutation
lock or invoking APT.

### `upgrade.tor.start`

Accepts a bounded embedded helper and `verify_only`; the helper is executable
only when its SHA-256 equals the digest compiled into the broker. The broker
atomically installs it at
`/usr/local/sbin/lightningos-check-tor-update`, rejecting unsafe parents,
symlinks, non-regular files, non-root ownership, and group/world-writable
existing targets. It then launches exactly one fixed unit through
`/usr/bin/systemd-run`: `lightningos-tor-verify` for verification or
`lightningos-tor-upgrade` for the real update.

The helper downloads only the fixed Tor Project key and distribution
`InRelease` over HTTPS with TLS 1.2 or newer. It checks the exact primary-key
fingerprint `A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89`, exports only that key,
and authenticates the current `InRelease` with `gpgv` before installing the
keyring, repository source, or package. `verify_only` exits after these checks
without changing APT configuration, packages, or Tor. Unknown fields,
caller-selected URLs/units/arguments, oversized or modified helpers, and
unexpected result states fail closed.

### `upgrade.lightningos.start`

Accepts only a normalized version, matching release tag, lowercase full
40-character commit, bounded embedded helper, and `verify_only`. The repository
is fixed to `https://github.com/jvxis/brln-os-light.git`; no repository, path,
command, unit, branch, shortened revision, or build argument is caller
selectable. The helper is executable only when its SHA-256 equals the digest
compiled into the broker and is atomically installed at the fixed root-owned
`/usr/local/sbin/lightningos-upgrade-app` path. The broker launches only
`lightningos-app-verify` or `lightningos-app-upgrade` through the fixed
`/usr/bin/systemd-run` executable.

Before build or installation, the helper fetches the exact release tag from
the fixed repository, requires it to resolve to the expected full commit,
requires `ui/public/version.txt` in that Git object to match the requested
version, and creates the worktree from the full commit rather than the tag. UI
dependencies use the committed lockfile through `npm ci`. `verify_only` uses a
bounded `/tmp/lightningos-release-verify.*` directory, removes it on success or
failure, and exits before opening the upgrade log, creating application paths,
building, installing, or restarting services.

The current historical releases use unsigned tags and commits. This binding
provides Git object integrity and closes tag-mutation/argument-injection gaps,
but it is not an independent publisher signature. A signed release manifest or
equivalent trusted publisher attestation remains required before the
LightningOS self-upgrade supply-chain track is complete.

### `files.enable_login`

Enables login protection in the single fixed destination
`/etc/lightningos/config.yaml`. Parameters must be `{}`: the caller cannot
provide a path, content, owner, group, or mode. The broker:

- rejects a symlink, non-regular file, non-root owner, group/world-writable
  file, unsafe parent directory, malformed YAML, duplicate keys, and input over
  1 MiB;
- changes only `features.enable_login` to the YAML boolean `true` and preserves
  the other configuration values;
- stages the result in the same root-owned directory, preserves the original
  owner/group/mode, fsyncs the file, revalidates the original inode, atomically
  renames the result, and fsyncs the directory;
- serializes the real update with the broker mutation lock;
- in `dry_run`, performs target and YAML validation but creates no temporary
  file and acquires no mutation lock.

In `shadow`, the existing compatibility writer remains responsible for the
actual update. In `enforce`, failure of this typed operation does not fall back
to `sudo tee` or runtime sudoers creation. Non-default manager config paths are
unsupported in `enforce` and fail closed.

### Native Lightning Loop operations

Lightning Labs Loop uses six operations whose paths, service account, unit,
ports, release URL, archive names, checksums, configuration, and ownership are
fixed by the shared catalog:

- `app.loop.status` accepts `{}` and returns only installed/service state plus
  booleans indicating whether the dedicated LND macaroon and persistent swap
  state exist;
- `app.loop.ensure` accepts a bounded LND TLS certificate and an optional
  bounded newly baked dedicated macaroon. It prepares only the fixed
  `v0.33.3-beta` release and the cataloged native runtime;
- `app.loop.lifecycle` accepts only `start` or `stop`;
- `app.loop.remove` accepts `{}`, disables the fixed unit, removes only the
  application binaries/unit, and deliberately preserves swap data and
  credentials;
- `app.loop.permissions.ensure` accepts `{}` and idempotently repairs only the
  fixed service account, directories, modes, ownership, and traverse ACLs;
- `app.loop.client-material.ensure` accepts `{}` and atomically creates only
  the fixed manager-readable copies of Loop's API certificate and macaroon.

No operation accepts a path, unit, executable, URL, checksum, user, group,
mode, port, shell fragment, or arbitrary lifecycle argument. Existing
directory components and credential sources must be real directories/regular
files, never symlinks. Credential bytes remain in standard input only; audit
records contain the operation metadata but not request parameters.

### Native PeerSwap operations

PeerSwap uses six closed operations plus the shared catalog firewall contract:

- `app.peerswap.status` accepts `{}` and returns only installed/service state,
  the selected `local` or `remote` Elements mode, and whether a dedicated LND
  credential exists;
- `app.peerswap.source.read` accepts `{}` and reads only the fixed root-owned
  source policy. The manager needs the secret to construct PeerSwap's private
  config, but no HTTP response returns the password;
- `app.peerswap.source.write` accepts only a validated local or HTTP(S) remote
  Elements source. It cannot select a policy path, and real writes are atomic
  `root:root 0600` files;
- `app.peerswap.ensure` accepts the `local|remote` enum, bounded fully
  validated PeerSwap/PSWeb configuration bytes, the LND TLS certificate, and
  an optional newly baked dedicated macaroon. The release, hashes, legacy
  migration source, identities, paths, units, ports, and service sandbox are
  fixed by the catalog;
- `app.peerswap.lifecycle` accepts only `start`, `stop`, or `restart`. Local
  mode refuses to start unless the fixed Elements service is running; remote
  mode does not depend on or restart local Elements;
- `app.peerswap.remove` accepts `{}`, disables the two fixed units and removes
  only the reproducible app tree. Persistent swap state, source policy, and
  dedicated credential survive reinstall;
- `app.firewall.ensure` with catalog ID `peerswap` admits only TCP port 1984.

The legacy asset folder name `version_5_0/amd64` is an installer compatibility
contract, not provenance. Every real ensure verifies the fixed upstream
PeerSwap v6.0.0 and the official PSWeb upstream `main` commit
`09983da398f253f8c14213e9f5c61b80cc879b67`, packaged as v6.0.0.1, before
changing the runtime. Existing
`/home/losop/.peerswap` regular files are copied without links or special files
to the dedicated runtime and left untouched for rollback. Managed config
values always replace legacy admin-macaroon and broad-listener paths; unknown
operator preferences and the `bitcoinswaps` choice are preserved.

### `docker.runtime.ensure` and `docker.runtime.status`

Both operations accept only `{}`. They support a Docker runtime that is already
installed; package names, repositories, versions, paths, service units, and
arguments cannot be supplied. `docker.runtime.ensure` checks the fixed Docker
binary and Compose implementation. If the daemon is stopped, it executes only:

```text
/usr/bin/systemctl start --no-block docker
```

It returns `ready` or `starting`; it never waits inside one privileged request
for daemon startup. The non-mutating `docker.runtime.status` returns only
`ready`, `starting`, `stopped`, or `failed`. The manager polls it for at most
one minute. `ensure` supports validation-only `dry_run` and uses the mutation
lock; `status` rejects `dry_run` and takes no lock. Missing Docker or Compose
fails closed in `enforce`; installing Docker packages remains a separate
pending capability.

### `app.image.prepare`, `app.image.status`, and `app.image.probe`

These operations accept only app/variant pairs compiled into the shared
catalog. CPU Miner admits `baseline`, `fast_pinned`, and `fast_latest`;
RoboSats admits `client`, `tor`, and `proxy`; Bitcoin Core admits only `node`;
BTCPay admits `server`, `nbxplorer`, `postgres`, and `tor`; and Electrs admits
only `server`. Every variant
maps to one exact image and one fixed transient unit name. A caller cannot
submit an image, registry, digest, tag, unit, path, or Docker argument.

When a cache-authoritative image is absent, `app.image.prepare` schedules a
bounded pull and returns `preparing` without waiting for registry I/O:

```text
/usr/bin/systemd-run --quiet --collect \
  --unit=<fixed-catalog-unit> \
  --property=Type=exec --property=RuntimeMaxSec=10min \
  /usr/bin/docker pull <fixed-catalog-image>
```

The non-mutating status operation reports only `ready`, `preparing`, `absent`,
or `failed`. Cache-authoritative variants inspect the fixed image first and
then the unit. Refresh-on-request variants inspect the unit first, so an older
local image with the same release tag cannot make an active update appear
complete. The manager polls for at most ten minutes, so cancellation of an
HTTP request does not ambiguously terminate the root pull; a retry can observe
the same unit or the completed image. `--collect` removes the transient unit
after completion.

The probe operation requires the selected image to be local and executes one
fixed two-second CPU compatibility benchmark. Its response is only
`{"runnable":true|false}`. A non-runnable fast image is an expected result,
allowing selection to continue to the next catalog variant. Prepare and probe
are serialized mutating operations and support validation-only `dry_run`;
status is read-only.

BTCPay uses the official `btcpayserver/btcpayserver:2.4.1` image, the newest
stable release published when this catalog was updated. Upstream does not
publish a usable `btcpayserver/btcpayserver:latest` tag: the release number is
therefore explicit and reviewable, while the `server` variant is still marked
refresh-on-request and executes a real pull for every install/start. Updating
to a future stable release changes the single catalog release constant rather
than allowing root-time discovery of an arbitrary tag. NBXplorer 2.6.8,
PostgreSQL 16, and Tor 0.4.9.5 are fixed cache-authoritative dependencies. Tor
is requested only for the existing remote-Onion Bitcoin wiring. This image
slice does not alter local/remote Bitcoin selection, RPC credentials, or LND.

Bitcoin Core uses the same operations with the additional closed variant
`{"app_id":"bitcoincore","variant":"node"}`, but it never executes a
registry pull. The broker maps that variant to release 31.1 and one
architecture record containing the official archive name and SHA-256 plus a
Debian slim base pinned by platform digest. It downloads `SHA256SUMS`,
`SHA256SUMS.asc`, the archive from `bitcoincore.org`, and seven Guix builder
keys from the upstream signing repository. Every imported primary key must
match its catalog fingerprint. At least three distinct `VALIDSIG`
fingerprints must authenticate the checksum bundle, the catalog archive line
must be present verbatim, and the downloaded archive must match the embedded
SHA-256.

Only after those checks does the transient unit build
`lightningos/bitcoin-core:31.1` locally using the pinned base. The Dockerfile
has no package installation or downloader and build `RUN` steps use
`--network=none`. The resulting entrypoint drops bitcoind to UID/GID 101. A
successful version probe is followed by an atomic root-only attestation under
the broker's privileged app tree containing release, archive hash, base
digest, signature count, and exact Docker image ID.

`app.image.status` reports Bitcoin Core ready only when that attestation is a
bounded regular non-symlink file, every field matches the compiled catalog,
the signature count meets the threshold, and fixed Docker inspection returns
the attested image ID. A local same-tag image without the attestation is
untrusted. Bitcoin Core image preparation has no disabled/shadow legacy pull;
the manager fails closed unless the broker operates in `enforce` mode.

Electrs has no production-supported upstream image or release binary. The
broker therefore builds `lightningos/electrs:0.11.1` from the exact verified
`romanz/electrs` `v0.11.1` archive, whose resolved commit and SHA-256 are fixed
in the catalog, on a platform-pinned Debian slim base. A fixed version probe
and the exact local image ID are bound to a root-only attestation. Build
failure remains observable through a validated root-only failure marker even
after systemd collects the transient unit; only a later explicit prepare may
clear and retry it. A same-tag local image without the matching attestation is
untrusted.

### `app.bitcoincore.storage.ensure`

This mutating operation accepts only `{"data_dir":"<canonical-linux-path>"}`.
The default `/data/bitcoin` is allowed; a custom path must be outside blocked
system/data trees, contain only the closed path character set, resolve through
non-symlink directory components, live on a mounted filesystem distinct from
`/`, and expose at least 10 GiB free. The caller cannot provide an identity,
owner, mode, marker, executable, mount command, or cleanup instruction.

For a real request the broker first rejects any target that differs from
existing root-owned metadata, then creates only the validated directory as
UID/GID 101 mode `0750`. It generates a 24-byte random identity, stores the
identity and canonical target as root-owned `0600` files under its Bitcoin app
tree, and writes the matching marker as `root:101` mode `0640`. Repeated calls
preserve the identity. `dry_run` validates path, mount, and capacity without
creating metadata, directories, or markers. Audit records operation outcome
but never the path or identity.

### `app.bitcoincore.config.ensure`, `.read`, `.write`, and credentials

These operations bind secret-bearing `bitcoin.conf` handling to the storage
target previously enrolled by `app.bitcoincore.storage.ensure`. Every request
contains only the canonical `data_dir`; `ensure` and `write` additionally carry
bounded `content`. On a new App Store install, `ensure` also carries only the
boolean `generate_rpcauth`; the content must then be a credential-free
template. The destination is always the fixed `bitcoin.conf` basename.
Caller-supplied filenames, owners, modes, identities, commands, and extra fields
are rejected. Config content must be non-empty valid UTF-8, at most 8 KiB,
contain neither NUL nor carriage-return bytes, and end with a newline.

Before any access the broker verifies the root-owned enrollment metadata, the
UID/GID 101 data directory, and the matching `root:101` storage marker. It opens
the directory and files without following symlinks. Reads accept only a bounded
regular `root:101` mode `0640` config. Mutations create a random same-directory
temporary file, set `root:101` mode `0640`, fsync it, revalidate the original
inode immediately before rename, atomically replace the fixed config, and fsync
the directory. `write` requires an existing canonical config. `ensure` creates
only when absent and also tightens the legacy `101:101` owner atomically while
preserving content. Its `dry_run` validates the same boundary without creating
or replacing a file; `read` is non-mutating and rejects `dry_run`.

The manager no longer reads the config through a running/root Docker container
or writes it through a manager-side temporary secret file. An old manager-owned
seed is read only to preserve existing credentials during migration and is
removed only after broker ensure succeeds. All config operations fail closed
outside broker `enforce` mode. Audit events contain operation outcomes but no
path, config content, RPC username, or RPC password.

For a new config, the broker generates the fixed `lightningos` username, a
32-byte random password, a 16-byte random salt, and the Bitcoin Core-compatible
HMAC-SHA256 `rpcauth`. Only the `rpcauth=<user>:<salt>$<hash>` verifier is added
to `bitcoin.conf`; neither `rpcuser` nor `rpcpassword` is written there. The
recoverable tuple is stored under the broker's root-only Bitcoin tree as a
fixed `root:root` mode `0600` record. Config and credential creation are
idempotent and never rotate or rewrite an existing config.

`app.bitcoincore.credentials.read` is read-only, accepts only the enrolled
`data_dir`, revalidates the root-only record, recomputes its HMAC, and requires
the matching `rpcauth` to be active in the fixed config before returning the
username and password over the local typed broker channel. This is the narrow
compatibility bridge required by LND and managed consumers, whose upstream
configuration formats still require the clear credential. It is never exposed
by an HTTP endpoint, command argument, audit event, or log. Legacy configs
without a matching protected record fail closed here and require an explicit
one-time credential migration; they are never silently rotated or restarted.

### `app.bitcoincore.status`

This read-only operation accepts only `{}` and rejects `dry_run`. The broker
validates the enrolled Bitcoin storage identity and the root-owned execution
Compose snapshot, resolves the one fixed running `bitcoind` service, and
validates its container ID before invoking `bitcoin-cli` inside that container.
The CLI receives only the fixed container data directory and config path, so
Bitcoin Core supplies authentication from its runtime cookie. RPC usernames,
passwords, cookies, arbitrary methods, arguments, endpoints, container names,
and Docker output cannot be supplied by or returned to the manager.

The closed query set is `getblockchaininfo`, `getnetworkinfo`, and
`getblockheader` for the validated best-block hash. Mainnet is required and
all output is size-bounded and decoded into typed chain, synchronization,
pruning, storage, peer, version, and best-block-time fields. In `enforce`, a
managed App Store node uses this operation for the Bitcoin Local page and
fails closed without the legacy password path. In `shadow`, the established
container-cookie and bounded `debug.log` compatibility reader remains
authoritative so a slow indexing RPC cannot suppress synchronization progress;
the typed operation becomes authoritative at `enforce` cutover.

Existing App Store nodes are enrolled during the idempotent system-integration
reconcile only when their legacy Compose and single-line canonical data-dir
record satisfy the closed layout. Enrollment uses
`app.bitcoincore.storage.ensure`, leaves a failed migration retryable by not
writing the version marker, and never restarts Bitcoin, Docker, networking, or
dependent applications. It does not reconstruct or rotate a legacy
`rpcauth` password; telemetry uses the runtime cookie independently.

### `bitcoin.consumer-network.ensure`

This mutating operation accepts only `{}`. The caller cannot provide a Docker
network name, CIDR, gateway, label, interface, port, executable, or firewall
argument. A real request creates or validates exactly:

```text
network: bitcoincore_default
driver/scope: bridge/local
subnet: 172.31.253.0/24
gateway: 172.31.253.1
labels: com.docker.compose.project=bitcoincore
        com.docker.compose.network=default
```

An existing network is preserved only if all fields match; incompatible or
uninspectable state fails closed and is never deleted or replaced. If UFW is
active, the broker resolves the bridge solely from the validated hexadecimal
Docker network ID and applies fixed mainnet RPC, P2P, and ZMQ TCP rules
only on that bridge. Inactive or unavailable UFW is not modified. `dry_run`
validates the closed request without invoking Docker or UFW.

The operation owns only the consumer boundary. It never reads or writes an
external `bitcoin.conf` and never starts, stops, restarts, or removes an
external/systemd Bitcoin service.

### `packages.feature.ensure` and `packages.feature.status`

The first shared package capability accepts only this closed request:

```json
{"feature":"docker_runtime"}
```

The feature maps inside the broker to the fixed Ubuntu package set
`docker.io` plus `docker-compose-v2`. Package names, repositories, versions,
executables, arguments, environment variables, unit names, and lock paths are
never supplied by the manager. Unknown features and fields are rejected. The
catalog currently admits only Ubuntu 24.04 and Ubuntu 26.04; every other OS or
version fails closed before a command runs.

Preparation is a two-stage asynchronous state machine. The broker schedules a
fixed `apt-get update` unit, reports `indexing` until its root-owned transient
unit reaches `active/exited`, and then schedules a separate fixed `apt-get
install -y docker.io docker-compose-v2` unit. Both commands run through a
fixed `flock` lock under `/run/lock/lightningos`, use dpkg's bounded lock wait,
have a 15-minute runtime ceiling, and receive only the fixed noninteractive
environment. The manager polls the read-only status operation and never holds
an HTTP broker process open while apt runs.

Successful oneshot units use `RemainAfterExit=yes`, making completion
observable without shell scripts or writable marker files. The next typed
ensure call stops completed units so `--collect` removes them. A failed fixed
stage remains observable as `failed`; a later ensure request may clear and
retry only that same catalog stage. Readiness requires both catalog packages
to report `installed` through fixed `/usr/bin/dpkg-query` arguments.

CPU Miner install invokes this capability before typed Docker-runtime
readiness. In `enforce`, missing Docker packages can therefore be installed
without any direct `apt-get`, `systemd-run`, package name, or repository choice
in manager code. In `shadow`, the request is validation-only and the reviewed
legacy installer remains responsible for compatibility.

The clean Ubuntu 24.04 gate began without either catalog package and with both
the manager user and live process lacking the Docker GID. The API install
returned 200, installed the Noble `docker.io` and `docker-compose-v2` packages,
collected both transient package units, and completed image preparation plus
first-container creation. CPU Miner reported `running`; typed stop, start, and
uninstall each returned 200 and left no app files or containers. Direct Docker
access as `lightningos` failed, while a request that injected a `packages`
array was rejected as `invalid_request`. `LOS-TEST2` was not changed and the
successful disposable gate clone was powered off. Secret-free evidence is in
`docs/baselines/privilege-hardening-phase2-docker-package-install-enforce-2026-08-10.json`.

### `app.compose.lifecycle`

The first Phase 2 manifest admits only this request shape:

```json
{
  "app_id": "cpuminer",
  "action": "start"
}
```

`action` is exactly `start` or `stop` for the shared catalog; `restart` is also
accepted only for Bitcoin Core and targets its fixed `bitcoind` service.
Unknown apps, actions, fields, paths, services, images, and argument arrays are
rejected. The broker accepts only the
catalog CPU Miner Compose document and a strict seven-key environment with an
allowlisted image, fixed pool target, validated mainnet payout address, safe
worker, and bounded thread count. Symlinks and non-regular files are rejected.

For a real operation, the validated files are copied into a private,
broker-owned temporary directory before a fixed `/usr/bin/docker compose` (or
fixed `/usr/bin/docker-compose` compatibility) command runs. Docker never
receives the manager-writable source paths. Output and validation details are
not returned to the API. `dry_run` validates only, without a Docker command or
mutation lock.

Before `start`, the broker extracts the image only from the already validated
environment and requires `/usr/bin/docker image inspect <allowlisted-image>`
to succeed. A missing image fails closed before Compose, so lifecycle start
cannot turn into an implicit, potentially long-running image pull. Explicit
image preparation is handled by the typed asynchronous capability above.

The CPU Miner stop manifest fixes its graceful timeout at two seconds. Compose's
ten-second default exceeds the manager client's five-second broker deadline;
CPU Miner keeps no mutable container state, so the shorter bounded stop is safe
for this app and leaves time for audit completion. The catalog service also
sets `stop_grace_period: 2s`, applying the same bound when `compose up`
recreates a running container after a configuration change.

In `shadow`, CPU Miner start/stop validates this operation and then uses the
legacy Compose path. In `enforce`, it executes only through the broker and
fails closed. CPU Miner config and thread updates now reuse this typed start
operation after the manager updates its non-root app files. In `enforce`, their
Compose apply therefore has no direct Docker call or fallback in the manager.
CPU Miner install now uses the closed package feature above, typed runtime
readiness, image preparation/status, compatibility probes, and this lifecycle
operation for first-container creation in `enforce`.

### `app.compose.remove`

CPU Miner uninstall admits only `{"app_id":"cpuminer"}`. Unknown apps,
fields, paths, arguments, and caller-selected Compose options are rejected.
The broker validates and privately snapshots the exact catalog Compose and
environment files, then runs one fixed teardown shape:

```text
/usr/bin/docker compose <fixed snapshot/project options> \
  down --remove-orphans --timeout 2
```

The standalone `/usr/bin/docker-compose` fallback uses the same fixed tail.
Real removal is serialized by the mutation lock; `dry_run` validates without
a Docker command or lock. In `enforce`, a broker failure is returned before the
manager deletes its own app directory. Only after successful typed teardown
does the unprivileged manager remove those files. Disabled and shadow modes
retain the reviewed compatibility behavior.

The disposable Ubuntu 24.04 gate installed commit `c5ba28d` and found that
removing the service user's Docker group membership alone was insufficient:
the systemd unit still injected `SupplementaryGroups=docker`. The accepted run
therefore reset that unit property temporarily and verified both the service
user and live manager process lacked the Docker GID. Direct Docker access as
`lightningos` was denied, while HTTP API start and uninstall returned success.
The broker recorded a successful `app.compose.remove` completion and no app
files or Compose containers remained. A separate negative start using an
allowlisted but absent image failed before Compose and did not pull or start a
container. The original unit, group, config, binaries, disabled mode, and login
setting were restored; Docker was stopped and the VM was powered off.
Secret-free evidence is in
`docs/baselines/privilege-hardening-phase2-cpuminer-remove-enforce-2026-08-10.json`.

### `app.compose.inspect`

The second Phase 2 capability admits only `{"app_id":"cpuminer"}` and is
non-mutating; `dry_run`, unknown fields, paths, services, argument arrays, and
other app IDs are rejected. The broker validates and privately snapshots the
same exact catalog Compose and environment files used by lifecycle operations.
It then runs fixed Compose status and container-ID queries. A running container
ID must be a single 12-to-64-character lowercase hexadecimal value before it
can reach the fixed Docker stats command:

```text
/usr/bin/docker stats --no-stream --format {{.CPUPerc}} <validated-id>
```

The response is limited to `running` or `stopped` plus the raw Docker CPU
percentage. Docker output, container metadata, paths, and IDs never cross the
broker response. A stats failure degrades the optional CPU value to zero while
preserving the independently established running state; a status or container
lookup failure fails closed.

In `shadow`, the read-only inspection is audited and the manager still uses
the legacy Docker status/metrics path for compatibility comparison. In
`enforce`, CPU Miner App Store status and its dedicated status endpoint use the
typed inspection with no direct Docker command from the manager. Hashrate and
share counters continue to use the miner's unprivileged localhost TCP API, and
pool statistics continue to use their existing HTTP endpoints.

The disposable Ubuntu 24.04 inspection gate installed commit `32f7053`, then
temporarily reset the manager unit's supplementary groups to `lnd` and
`systemd-journal` and removed the `lightningos` user's Docker group membership.
The running manager process was verified not to contain the Docker GID. In
`enforce`, the dedicated status endpoint and App Store status still reported
the app correctly; typed stop/start produced three successful lifecycle audit
completions, and four inspections completed successfully. Unknown app and
argument-array requests were rejected, while an altered Compose document
failed closed with one expected `app_inspection_failed` audit completion.

The base clone has no initialized LND wallet, so it cannot exercise the legacy
install path that generates a payout address. The gate instead staged the exact
catalog files with a public discard address and reproduced the current install
postcondition by creating and stopping the container in `disabled` before the
broker cutover. An attempted broker `start` before that postcondition exceeded
the five-second client deadline while first-container setup was occurring; it
was rejected as outside the accepted lifecycle state rather than hidden. First
container creation remained outside that earlier checkpoint and was later
covered by the typed install/image gate. After acceptance, the temporary unit
override was removed, the
Docker group and `disabled` mode were restored, the app was uninstalled, Docker
was stopped, and the VM was powered off. Secret-free evidence is in
`docs/baselines/privilege-hardening-phase2-cpuminer-inspect-enforce-2026-08-10.json`.

The follow-up config gate exercised the thread and pool/address/worker endpoints
with the manager still lacking the Docker GID. The first config-driven recreate
reproduced the ten-second Compose grace-period mismatch and returned HTTP 500
after the client killed the broker transport. This fail-ambiguous result was
rejected. Commit `60371ea` added the catalog `stop_grace_period: 2s`; the rerun
then completed thread and config apply, kept the miner running, preserved one
thread, and returned the sanitized worker. A retained invalid `POOL_MODE` was
rejected with the expected `app_lifecycle_failed` audit completion. The gate
recorded three successful typed lifecycle completions and two successful typed
inspections, restored all temporary privilege changes, removed the app, stopped
Docker, and powered off the VM. Secret-free evidence is in
`docs/baselines/privilege-hardening-phase2-cpuminer-config-enforce-2026-08-10.json`.

The later install gate installed commit `6fad543` on the same disposable
Ubuntu 24.04 clone. Docker packages were already present, but the daemon was
inactive and the baseline image was deliberately removed after verifying that
no container referenced it. With both the service user and live manager
process lacking the Docker GID, the HTTP install path started Docker through
the typed runtime operation, evaluated all three catalog image variants, ran
two fixed compatibility probes, pulled the missing baseline image through a
transient unit, and created the first container through typed lifecycle. The
pull/install completed in 90.5 seconds across eleven typed status checks,
demonstrating that registry I/O did not remain inside one five-second broker
request. Stop, start, and uninstall then passed; all image units were collected
and no app files or containers remained. An injected image field was rejected.
The original binaries, config, login setting, Docker group, inactive daemon,
baseline image presence, and powered-off VM state were restored. `los-test2`
was not changed. Secret-free evidence is in
`docs/baselines/privilege-hardening-phase2-cpuminer-image-install-enforce-2026-08-10.json`.

The first disposable Ubuntu 24.04 gate exposed the timeout mismatch described
above: the manager returned HTTP 500 after five seconds while the original
ten-second Compose stop continued and produced a successful completion audit
at 10.567 seconds. This fail-ambiguous result was rejected for acceptance.
Commit `9e34e50` bounded the catalog stop, and commit `490d9c9` kept the Linux
module graph stable during installation. The rerun returned HTTP 200 for stop
and start, observed zero then one running container, produced paired audit
events, accepted a non-mutating dry-run, rejected an unknown app and an
argument array, uninstalled the test app, restored `disabled`, and left Docker
inactive. Secret-free evidence is in
`docs/baselines/privilege-hardening-phase2-cpuminer-enforce-2026-08-10.json`.

### Bitcoin Core Compose contract

Bitcoin Core reuses `app.compose.lifecycle`, `app.compose.inspect`, and
`app.compose.remove` with requests containing only the catalog app ID and, for
lifecycle, the closed action. Unlike CPU Miner and RoboSats, the broker does
not trust or byte-validate a manager-owned Compose input. It reads the exact
data directory and storage identity from root-owned enrollment metadata,
verifies the protected marker and fixed `bitcoin.conf`, verifies the official
release attestation and exact local Docker image ID for start/restart, and
generates its execution manifest from the closed catalog.

The execution Compose and storage guard are atomically stored as root-owned
mode `0600` under
`/var/lib/lightningos-privileged/apps/bitcoincore`. No caller-selected path,
mount, image, service, port, option, or argument reaches Docker. Status uses a
private transient copy of the generated manifest and returns only `running` or
`stopped`; it does not rewrite the persistent execution assets. The fixed
commands are `up -d`, `stop --timeout 10`,
`restart --timeout 10 bitcoind`, and
`down --remove-orphans --timeout 10`.

Removal deletes only the root-owned execution Compose and guard after Compose
teardown succeeds. It deliberately preserves the blockchain directory,
storage identity/enrollment, `bitcoin.conf`, official image attestation, and
verified local image so reinstall cannot silently select a new storage target
or downgrade provenance. The manager may remove only its own inert app record
after the typed removal succeeds. Bitcoin Core lifecycle, inspection, and
removal have no legacy fallback outside broker `enforce`; dependent apps reuse
the fixed `bitcoincore_default` network and do not mutate the allowlist or
restart Bitcoin during install-time RPC discovery.

The Ubuntu 24.04 real-operation gate used an isolated `regtest` config and a
12 GiB-declared `tmpfs`. It passed typed start, two running inspections,
restart, stop, stopped inspection, and remove; verified the daemon as UID 101
and the RPC chain as regtest; rejected an injected argument array; and left no
container, mount, execution assets, enrollment metadata, checkout, binary, or
persistent blockchain data. The official image and root-owned attestation were
preserved. Secret-free evidence is in
`docs/baselines/privilege-hardening-phase2-bitcoincore-lifecycle-enforce-2026-08-10.json`.

### Bitcoin Core consumer-network contract

The closed Compose manifest assigns `bitcoincore_default` the fixed private
subnet `172.31.253.0/24`. The default `bitcoin.conf` contains the matching
`rpcallowip` before first start. Current and future Docker consumers gain local
RPC access only by joining that external network; neither caller-provided
network names nor app-specific subnets enter the privileged protocol.

For upgrades, the manager adds the fixed subnet idempotently only from the
Bitcoin app's own install/start path. It preserves RPC credentials and legacy
allow entries. A running Bitcoin Core is restarted once only when this one-time
migration changed the file; a stopped node is not restarted after start.
Routine config reads and dependent-app wiring are read-only and cannot request
a Bitcoin restart as a side effect.

The Ubuntu 24.04 gate exercised a fresh fixed network and an existing
Compose-labeled dynamic `172.18.0.0/16` network. Both ran the official attested
image with an isolated `regtest` config. A disposable consumer joined
`bitcoincore_default`, resolved `bitcoind`, and returned `chain=regtest` from
`getblockchaininfo`; the Bitcoin container ID and start timestamp were
unchanged. The legacy network remained in place for the duration of its test,
proving that upgrade does not force immediate network recreation. Secret-free
evidence is in
`docs/baselines/privilege-hardening-phase2-bitcoincore-consumer-network-enforce-2026-08-11.json`.

### Electrs Compose and full-node contract

Electrs reuses the typed image, lifecycle, inspection, and removal operations
with the catalog-fixed `server` variant. Its non-root Compose execution has a
read-only root filesystem, drops all capabilities, enables
`no-new-privileges`, exposes Electrum and Prometheus only on localhost, and
joins either the fixed App Store Bitcoin network or the fixed native Bitcoin
gateway. It receives no Bitcoin config, Docker socket, LND material, broad
host directory, or caller-selected mount, network, port, image, or option.
Only its reproducible named index volume is removed on uninstall.

The manager declaration contains the closed runtime mode and one private
dedicated `user:password` cookie. The broker rejects symlinks, unsafe modes,
unexpected bytes, alternate hosts, manifest/env/cookie tampering, and a cookie
equal to known broad credentials, then snapshots the execution material below
the root-only privileged app tree. Docker consumes only that snapshot and the
cookie is mounted read-only at the fixed container path.

Every start independently authenticates Bitcoin and requires the expected
chain, `pruned=false`, initial block download false, blocks equal headers, and
an enabled fully synchronized transaction index. Authentication, pruning,
synchronization, and transaction-index failures are distinct fail-closed
states reached before Compose. The accepted Ubuntu 24.04 functional gate used
an isolated 101-block regtest node satisfying the same full-node and
`txindex=1` contract. Electrum protocol and metrics checks, stop/start,
data-preserving stop, data-removing uninstall, and negative tamper gates
passed; no preserved integration node was used as the positive gate. Evidence
is in
`docs/baselines/privilege-hardening-phase2-electrs-functional-2026-08-11.json`.

## Manager modes and rollback

The `privileged` configuration block supports:

- `disabled`: default; only the reviewed legacy path executes;
- `shadow`: the broker validates and audits a dry-run request, then the legacy
  path executes regardless of the shadow result;
- `enforce`: the broker executes and the legacy fallback is disabled.

Environment overrides are `LIGHTNINGOS_PRIVILEGED_MODE` and
`LIGHTNINGOS_PRIVILEGED_TIMEOUT_SECONDS`.

Node rollout must install and self-test the helper in `disabled`, then observe
`shadow` before selecting `enforce`. Rollback is a configuration-only change
back to `disabled`; installing the foundation does not remove any legacy
sudoers rule or Docker group membership.

When the manager starts in `shadow` or `enforce`, it performs `self_test`
through the real sudo transport and records the result in the manager log. A
failure is visible but does not prevent read-only manager/API availability;
migrated mutations still fail closed in `enforce`.

## First shadow rollout

The `los-test2` node entered `shadow` on 2026-08-10. Root, manager-caller, and
manager-startup self-tests passed. A `service.restart` dry-run for LND was
validated without creating the mutation lock or calling systemctl. An unknown
operation, shell syntax in a unit, and sudo command-line arguments were
rejected. Manager, LND, Postgres, and the HTTPS health endpoint remained
healthy. No broker service mutation was executed.

The first installation attempt intentionally exercised the rollback trap. It
revealed that a direct-root self-test inside an operator's sudo shell inherited
`SUDO_USER`; the broker denied that ambiguous caller before creating its audit
file. The previous manager/config/sudoers were restored automatically. Commit
`2254287` fixed the installer boundary by clearing inherited `SUDO_*` variables
only for the direct-root self-test. The subsequent rollout passed. Full
secret-free evidence is in
`docs/baselines/privilege-hardening-phase1-shadow-2026-08-10.json`.

The first fixed-file shadow checkpoint followed on 2026-08-10 using commit
`ee0cb9e`. A real manager-caller request validated `files.enable_login` in
dry-run mode. Requests attempting to add either a path or content were denied,
the manager config hash remained unchanged, and the mutation lock was not
created. Manager, LND, and Postgres remained active. Full secret-free evidence
is in
`docs/baselines/privilege-hardening-phase1-files-shadow-2026-08-10.json`.

## Fresh-install mutation gate

A clean Ubuntu 24.04 VirtualBox clone of `brln-os-basica` installed commit
`ab63762` from the hardening branch on 2026-08-10. The installer completed with
the exact broker sudo command, a passing root self-test, an active manager and
Postgres, and LND correctly enabled but deferred until wizard configuration.

The disposable node was preconditioned from the backed-up default config to
`enable_login: false` and broker mode `enforce`. Manager startup transport
self-test passed. A real `POST /api/auth/enable-login` returned HTTP 202 and
produced exactly one new successful, non-dry-run `files.enable_login` audit
completion. The manager config remained `root:lightningos:0640`, the mutation
lock was `root:root:0600`, and the manager remained healthy after its scheduled
restart. The root-only rollback restored the exact original config hash and
`disabled` mode. The VM was powered off and retained for follow-up testing.

Full secret-free evidence is in
`docs/baselines/privilege-hardening-phase1-fresh-install-enforce-2026-08-10.json`.

## Soak, reboot, and service acceptance

The integration node remained healthy in `shadow` through 2026-08-10T11:55Z.
Its config was byte-identical to the rollout backup, the last broker event was
still the fixed-file checkpoint at 11:13Z, no real broker mutation had run, and
no mutation lock existed.

The explicit Phase 1 review then rebooted the disposable Ubuntu 24.04 clone and
found that the runtime lock parent disappeared with `/run`. Commit `257b391`
added the tmpfiles rule to every install/upgrade path. An idempotent install and
real reboot recreated the directory as `root:root:0750`, after which
`service.status` passed immediately.

The same review found that a blocking self-restart killed the broker before its
completion audit. Commit `257b391` made manager restarts non-blocking and moved
their actual execution to the fixed delayed transient unit documented above.
The acceptance rerun returned HTTP 200, changed the manager activation
timestamp, restored HTTPS health, and produced exactly one new successful
`service.restart` start/completion pair. A PostgreSQL restart also returned
HTTP 200 with one successful pair, while path, shell, unknown-field, and
blocking-manager requests were rejected. The original config hash and
`disabled` mode were restored, and the disposable VM was powered off.

Full secret-free evidence is in
`docs/baselines/privilege-hardening-phase1-soak-reboot-service-2026-08-10.json`.

## Tapd typed boundary

Tapd adds five operations with no raw command or caller-selected runtime
fields:

| Operation | Caller fields | Broker-owned boundary |
| --- | --- | --- |
| `app.tapd.status` | none | Fixed Compose labels, stopped/running state, dedicated credential presence, and fixed Fedimint gateway conflict check |
| `app.tapd.ensure` | PostgreSQL password plus bounded LND TLS/macaroon bytes | Exact root-only config, official digest-pinned Compose, fixed data path, dedicated credential mounts, modes, and atomic writes |
| `app.tapd.lifecycle` | `start` or `stop` | Fixed project/snapshot, image readiness, conflict gate, Compose argv, and timeout |
| `app.tapd.remove` | none | Fixed project removal without `--volumes`; persistent Tapd data and PostgreSQL database are retained |
| `app.tapd.cli` | One strict typed Tapd request | Fixed container identity and `tapcli` argv for get-info, balance, address, universe sync, mint, finalize, send, or decode only |

The catalog fixes stable release `v0.8.0` and official manifest digest
`sha256:868e8dec4174798eaff056336eb9b3ba1bd387590c0f29201781f98702cc567d`.
The runnable probe first requires that exact image locally, then executes both
`/bin/tapd --version` and `/bin/tapcli --version` as UID/GID 65534 with
`--network none`, a read-only filesystem, all capabilities dropped, and
no-new-privileges. Exact expected output is part of the catalog. The upstream
release verifier's signer-name case mismatch is documented in the persistent
gate evidence; production does not weaken the five-signature policy.

CLI values are validated by type, size, alphabet, range, and mutual exclusion
before argv construction. Metadata must be bounded valid JSON, asset/group
identifiers have exact lowercase hex lengths, addresses are bounded mainnet
Taproot Asset strings, and universe hosts cannot contain URL syntax. The broker
resolves a single validated container ID from fixed Compose labels; the caller
never controls a Docker identifier or flag. Audit events contain operation,
request ID, dry-run, result, and duration only, never request parameters.

## Public Pool typed boundary

| Operation | Caller fields | Broker-owned boundary |
| --- | --- | --- |
| `app.publicpool.status` | none | Fixed project/service labels and legacy-install compatibility |
| `app.publicpool.ensure` | Validated Bitcoin mode, RPC endpoint/credentials and optional remote ZMQ endpoint | Exact images, paths, UID/GID, mounts, ports, networks, Compose, environment and Caddyfile |
| `app.publicpool.lifecycle` | `start` or `stop` | Exact snapshot validation, image readiness, fixed Compose argv and timeout |
| `app.publicpool.remove` | none | Fixed project removal without data deletion; legacy container identities must be singular |
| `app.publicpool.firewall` | none | UFW status plus only `3333/tcp` and `8081/tcp` when active |

The catalog fixes backend and UI images by commit tag and manifest digest. The
backend probe requires exact Node output as UID/GID 65532 with no network,
capabilities or writable root. The UI probe copies Caddy to an executable
tmpfs to strip the image binary's file capability, then requires its exact
version under the same restrictions. Runtime validation reconstructs the
entire environment, Compose, and Caddyfile byte-for-byte before lifecycle or
removal. Unknown fields, arbitrary image/path/port/network/command input,
unsafe credentials, loopback remote targets, duplicate environment keys, and
ambiguous container identities fail closed.
