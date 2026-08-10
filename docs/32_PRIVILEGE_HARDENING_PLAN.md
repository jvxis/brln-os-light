# LightningOS privilege hardening plan

## Status

Accepted for implementation. No runtime privilege changes are made by this
document.

Date: 2026-08-10.

Tracking issue: [#32](https://github.com/jvxis/brln-os-light/issues/32).

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

1. Implement the broker protocol, authorization, validation, structured logs,
   locking, and timeouts.
2. Add a client package used by the manager.
3. Migrate low-risk service and fixed-file operations first.
4. Keep the old path available only behind an explicit temporary compatibility
   flag.

Exit criterion: negative tests prove that arbitrary executables, shell syntax,
paths, units, and arguments are rejected.

### Phase 2 — App Store and Docker cutover

1. Convert every catalog app to a validated broker manifest.
2. Migrate Docker installation and all app lifecycle operations.
3. Run install/start/stop/restart/uninstall tests for every app on disposable
   Ubuntu 24 and Ubuntu 26 nodes.
4. Remove direct Docker calls and the manager's Docker socket access.

Exit criterion: the complete supported App Store works while the manager is not
in the `docker` group.

### Phase 3 — Packages, firewall, storage, and upgrades

1. Replace arbitrary package arguments with fixed dependency sets.
2. Replace direct UFW access with the manager-access policy operation.
3. Replace ownership and permission shell commands with typed storage actions.
4. Move LND, Tor, and LightningOS upgrades to verified broker operations.

Exit criterion: no manager code path invokes `apt`, `dpkg`, `ufw`,
`systemd-run`, or a privileged shell directly.

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
