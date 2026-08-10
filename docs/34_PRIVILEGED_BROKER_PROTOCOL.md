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

In `shadow`, CPU Miner start/stop validates this operation and then uses the
legacy Compose path. In `enforce`, it executes only through the broker and
fails closed. CPU Miner install, uninstall, status, configuration updates,
image probes, and metrics remain on the reviewed legacy path until their own
typed capabilities are implemented.

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
