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

These operations accept only the CPU Miner app ID plus one closed image
variant: `baseline`, `fast_pinned`, or `fast_latest`. The variant maps inside
the broker catalog to one exact image and one fixed transient unit name. A
caller cannot submit an image, registry, digest, tag, unit, path, or Docker
argument.

When the image is absent, `app.image.prepare` schedules a bounded pull and
returns `preparing` without waiting for registry I/O:

```text
/usr/bin/systemd-run --quiet --collect \
  --unit=<fixed-catalog-unit> \
  --property=Type=exec --property=RuntimeMaxSec=10min \
  /usr/bin/docker pull <fixed-catalog-image>
```

The non-mutating status operation first performs a fixed image inspection,
then reports only `ready`, `preparing`, `absent`, or `failed` from the fixed
unit state. The manager polls for at most ten minutes, so cancellation of an
HTTP request does not ambiguously terminate the root pull; a retry can observe
the same unit or the completed image. `--collect` removes the transient unit
after completion.

The probe operation requires the selected image to be local and executes one
fixed two-second CPU compatibility benchmark. Its response is only
`{"runnable":true|false}`. A non-runnable fast image is an expected result,
allowing selection to continue to the next catalog variant. Prepare and probe
are serialized mutating operations and support validation-only `dry_run`;
status is read-only.

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

`action` is exactly `start` or `stop`. Unknown apps, actions, fields, paths,
services, images, and argument arrays are rejected. The broker accepts only the
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
