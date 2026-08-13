# Privilege hardening Phase 0 inventory

## Status and scope

Captured on 2026-08-10 from branch
`agent/0.5.3-privilege-hardening`, starting at commit `650c6c5`.

This inventory covers every production Go call expression that crosses the
current manager privilege boundary, every current privileged shell vector, the
installer and upgrader paths that create that boundary, and the secret-free T0
baseline of the preserved integration node. Generated protobufs, tests,
documentation examples, container-internal entrypoints, and root-run bootstrap
work that cannot be reached by the manager are not counted as manager runtime
calls. Root-run provisioning scripts are inventoried separately.

No runtime privilege, service, firewall, application, package, ownership, or
node configuration was changed while producing this document.

Phase 1 adds one exact broker invocation to the installer-generated sudoers
alias: `/usr/local/libexec/lightningos-privileged ""`. The explicit empty
argument list is not a wildcard and is guarded by a regression test. All 35
legacy wildcard constructors remain frozen at the Phase 0 ceiling until their
own operation families are migrated and removed.

The first fixed-file replacement is `files.enable_login`. Its request contains
no destination or content fields; the broker owns the fixed manager config
path, YAML transformation, owner/group/mode preservation, atomic rename, and
durability checks. The legacy `sudo tee` and runtime compatibility sudoers
paths remain only for `disabled` and `shadow` until this operation completes
its node validation and selective `enforce` review.

## Static inventory totals

The reviewed manager boundary contains:

- 92 `RunCommandWithSudo` call expressions, including the single generic call
  made by `runSystemd` and the power helper;
- 70 call sites of the generic `runSystemd` helper;
- 2 `WriteFileWithSudo` calls;
- 4 privileged service restart calls and 1 host power call;
- 36 source-level privileged `/bin/sh -c` vectors, including the command vector
  assembled by `loopClientMaterialSyncRunArgs`;
- 35 wildcard sudo rule constructors across installers, the application
  upgrader, and the login compatibility path;
- 7 Docker-group creation, membership, or service-grant sites.

`RunCommandWithSudo` and `runSystemd` are intentionally counted separately.
At runtime, each `runSystemd` call passes through the one generic sudo call in
`systemd_run.go`; the numbers describe static replacement work, not distinct
root processes.

Legend for the call-site table:

- `S`: `RunCommandWithSudo` calls.
- `D`: `runSystemd` calls.
- `W/R/P`: privileged file writes, service restarts, and host power calls.
- `C`: privileged shell command vectors in the source file; this is a subset
  of `S` or `D`, not an additional call count.

## Manager call-site replacement matrix

| Owning feature and source | S | D | W/R/P | C | Proposed broker operations | Required negative/regression strategy |
| --- | ---: | ---: | --- | ---: | --- | --- |
| System boundary — `internal/system/system.go` | 1 | 0 | 0/0/0 | 0 | Remove generic sudo; `host.power` with `reboot|poweroff` enum | Reject executable names, extra arguments, shell syntax, and unknown actions |
| SMART diagnostics — `internal/system/smart.go` | 1 | 0 | 0/0/0 | 0 | `storage.smart.read` for enumerated block devices | Reject arbitrary paths, options, non-block devices, traversal, and symlinks |
| General handlers — `internal/server/handlers.go` | 4 | 0 | 0/3/1 | 0 | `service.action`, `host.power`, `apps.logs`, `apps.network.inspect`, `storage.lnd.permissions.repair` | Fixed unit/action/app allowlists; reauth remains; preserve restart and log behavior |
| Generic transient unit — `internal/server/systemd_run.go` | 1 | 0 | 0/0/0 | 0 | Delete after all typed operations migrate | Any new arbitrary executable or argument must fail the Phase 0 ceiling test |
| Docker/App Store core — `internal/server/apps_docker.go` | 16 | 0 | 0/0/0 | 2 | `packages.install_feature`, `apps.lifecycle`, `apps.inspect`, root-owned Compose support | Fixed package sets and app manifests; reject socket mounts, host modes, devices, capabilities, arbitrary images and Compose paths |
| LightningOS upgrade — `internal/server/app_upgrade.go` | 2 | 1 | 0/0/0 | 1 | `upgrade.lightningos`, `files.install_helper`, `service.status` | Version/source/checksum validation, serialization, timeouts, failure rollback, no sudoers widening |
| LND upgrade — `internal/server/lnd_upgrade.go` | 0 | 0 | 0/0/0 | 0 | `upgrade.lnd.start`, `service.status` | Migrated on 2026-08-13: fixed source/architecture, five-signature manifest quorum, pre-extraction SHA-256, digest-pinned helper, typed launch and verify-only gate; full restart/rollback matrix remains |
| Tor upgrade — `internal/server/tor_upgrade.go` | 0 | 0 | 0/0/0 | 0 | `packages.tor.refresh`, `upgrade.tor.start` | Migrated on 2026-08-13: fixed package refresh, digest-pinned helper, exact official key fingerprint plus signed `InRelease`, fixed units, typed launch and verify-only health gate; real upgrade/rollback matrix remains |
| Enable-login compatibility — `internal/server/auth_enable.go` | 0 | 1 | 2/1/0 | 1 | `files.enable_login`, `service.action` | Only the configured manager YAML may change atomically; never create runtime sudoers rules |
| System integrations — `internal/server/system_integrations.go` | 0 | 1 | 0/0/0 | 0 | `files.reconcile_integrations` | Fixed embedded assets/destinations, owner/mode checks, idempotency and rollback |
| Terminal credentials — `internal/server/terminal_status.go` | 0 | 2 | 0/0/0 | 0 | `terminal.password.install`, `service.action` | Fixed staging/destination paths, no credential in argv/logs, invalid user and symlink tests |
| Firewall status — `internal/server/firewall_status.go` | 0 | 0 | 0/0/0 | 0 | `manager.firewall.status` | Migrated on 2026-08-13; broker validates fixed root-owned config/UFW paths and returns typed read-only status |
| Local Bitcoin status/config — `internal/server/bitcoin_local.go` | 5 | 0 | 0/0/0 | 0 | `apps.inspect`, manifest-defined Bitcoin config read/sync | Known container/app only; reject arbitrary container IDs, paths, and shell commands |
| Bitcoin Core app — `internal/server/apps_bitcoincore.go` | 5 | 1 | 0/0/0 | 1 | `apps.lifecycle(bitcoincore)`, `storage.prepare`, `files.seed_app_config` | Storage identity, canonical path, symlink and lifecycle tests; preserve data on rollback |
| Bark Wallet app — `internal/server/apps_bark_wallet.go` | 2 | 0 | 0/0/0 | 0 | `firewall.apply_app_policy(bark-wallet)` | Fixed port/protocol; reject caller-supplied UFW arguments |
| BTCPay app — `internal/server/apps_btcpay.go` | 2 | 0 | 0/0/0 | 0 | `firewall.apply_app_policy(btcpay)` plus manifest lifecycle | Fixed port and database/container identities; full start/stop persistence regression |
| CPU miner app — `internal/server/apps_cpuminer.go` | 1 | 0 | 0/0/0 | 0 | `apps.compatibility_probe(cpuminer)` | Known image and fixed command only; reject arbitrary image/arguments |
| CPU miner telemetry — `internal/server/apps_cpuminer_status.go` | 2 | 0 | 0/0/0 | 0 | `apps.metrics.read(cpuminer)` | Known container and fixed counters; reject path/container injection |
| Elements app — `internal/server/apps_elements.go` | 0 | 18 | 0/0/0 | 11 | `apps.lifecycle(elements)`, `files.install_app`, `storage.prepare`, `service.action` | Canonical roots, no recursive arbitrary delete/chown, atomic files, unit allowlist, lifecycle/rollback matrix |
| Fedimint apps — `internal/server/apps_fedimint.go` | 10 | 0 | 0/0/0 | 0 | Manifest lifecycle, `firewall.apply_app_policy`, `apps.network.inspect`, `service.action(lnd)` | Fixed bridges/ports/images; interceptor/LND restart regression; reject arbitrary image removal |
| LNbits app — `internal/server/apps_lnbits.go` | 7 | 0 | 0/0/0 | 0 | Manifest lifecycle, `firewall.apply_app_policy`, `apps.network.inspect`, `service.action(lnd)` | Fixed network/ports; LND compatibility and original running/stopped state restoration |
| LNDg app — `internal/server/apps_lndg.go` | 13 | 0 | 0/0/0 | 0 | Manifest lifecycle, `apps.exec_migration`, `firewall.apply_app_policy`, `service.action(lnd)` | Fixed migration command/container, no general shell, fixed bridge/port, lifecycle persistence |
| Loop app — `internal/server/apps_loop.go` | 0 | 16 | 0/0/0 | 6 | `apps.lifecycle(loop)`, `files.install_app`, `storage.prepare`, `service.action` | Canonical roots and IDs, credential sync without argv/log disclosure, no arbitrary delete or unit |
| Mempool app — `internal/server/apps_mempool.go` | 2 | 0 | 0/0/0 | 0 | `firewall.apply_app_policy(mempool)` | Fixed frontend port/protocol and idempotent reconciliation |
| Peerswap app — `internal/server/apps_peerswap.go` | 2 | 23 | 0/0/0 | 12 | `apps.lifecycle(peerswap)`, `files.install_app`, `storage.prepare`, `firewall.apply_app_policy`, `service.action` | Canonical roots, atomic config, fixed units/ports, no arbitrary shell/delete, lifecycle and source-switch rollback |
| Peerswap Elements source — `internal/server/apps_peerswap_elements_source.go` | 0 | 2 | 0/0/0 | 0 | `service.action(peerswap|psweb)` | Exact two-unit allowlist and source-change regression |
| Public Pool app — `internal/server/apps_publicpool.go` | 5 | 0 | 0/0/0 | 0 | `firewall.apply_app_policy(publicpool)`, `apps.network.inspect` | Fixed UI/Stratum ports and bridge; reject interface/port injection |
| RoboSats app — `internal/server/apps_robosats.go` | 4 | 0 | 0/0/0 | 0 | Manifest image/lifecycle operations and `firewall.apply_app_policy` | Known image/source and fixed port; lifecycle/rollback tests |
| App storage permissions — `internal/server/apps_storage_permissions.go` | 0 | 1 | 0/0/0 | 0 | `storage.repair_app` | Approved app roots and UID/GID pairs only; traversal, symlink and root-path tests |
| Taproot Assets app — `internal/server/apps_tapd.go` | 1 | 0 | 0/0/0 | 0 | `apps.exec_migration(tapd)` | Fixed container/command; reject arbitrary exec arguments and preserve asset state |
| Elements mainchain switch — `internal/server/elements_mainchain.go` | 0 | 1 | 0/0/0 | 0 | `service.action(elements)` | Exact unit/action and mainchain-switch regression |
| Elements status — `internal/server/elements_status.go` | 0 | 1 | 0/0/0 | 0 | `apps.status(elements)` | Fixed CLI and config path; no arbitrary executable or arguments |

The row totals are exactly `S=92`, `D=70`, `W=2`, `R=4`, and `P=1`.
The shell column totals 36 because the Loop credential-sync argument builder is
also a privileged shell vector even though its `runSystemd` call receives a
prebuilt argument slice.

## Provisioning and root-owned mutation inventory

The following paths run as root or are invoked through the current unrestricted
boundary. They must become installer-only behavior or typed broker operations:

| Owner/source | Current root work | Replacement/test disposition |
| --- | --- | --- |
| `install.sh`, `install_existing.sh`, `install_existing_pi.sh` | Users/groups, packages, repositories, binaries, systemd, sudoers, UFW, ownership/modes, TLS, Postgres and application roots | Keep root-run bootstrap transactional; install broker first; validate generated units/sudoers; remove legacy grants only at T4 |
| `internal/server/assets/upgrade-app.sh` | Replaces manager/UI/assets and recreates wildcard sudoers | `upgrade.lightningos`; signed/checksummed release, rollback bundle, never recreate wildcard rules after cutover |
| `internal/server/assets/upgrade-lnd.sh`, `scripts/upgrade-lnd.sh` | Replaces LND binaries, stops/starts LND, restores backups | `upgrade.lnd`; fixed destinations, checksums, timeouts, backup and health rollback |
| `internal/server/assets/check-tor-update.sh` | Configures repository and installs/upgrades Tor packages | Broker-digest-pinned `upgrade.tor.start`; fixed HTTPS repository, exact key fingerprint, authenticated `InRelease`, fixed package set and package lock |
| `internal/server/assets/lightningos-manager-firewall.sh` | Writes/reconciles UFW policy | `firewall.manager_access`; validated CIDR/interface and fixed ports |
| `internal/server/assets/setup-manager-tls-mdns.sh` | Writes TLS, mDNS, system and firewall files | Typed TLS/config/integration operations with atomic owner/mode enforcement |
| `internal/server/assets/reconcile-system-integrations.sh` | Installs system integration assets and units | `files.reconcile_integrations`; fixed embedded asset manifest |
| `internal/server/assets/lightningos-terminal-password.sh` | Installs operator credential and changes ownership/mode | `terminal.password.install`; fixed paths and secret-free audit |
| `scripts/fix-lnd-perms.sh` | Recursive LND ownership/mode repair | `storage.lnd.permissions.repair`; fixed roots, no symlink traversal |
| `scripts/install.sh`, `scripts/install-release-bootstrap.sh` | Root bootstrap/delegation to supported installers | Verify source/ref/signature and installation-type preflight before mutations |
| `scripts/capture-privilege-baseline.sh` | Read-only metadata/status collection | Remains a Phase 0 safety tool; no file contents, addresses, credentials or node identity are emitted |

The current wildcard constructors are: eight in `install.sh`, nine each in
`install_existing.sh` and `install_existing_pi.sh`, eight in
`internal/server/assets/upgrade-app.sh`, and one in `auth_enable.go`. The
wildcard command families are `smartctl`, `apt-get`, `apt`, `dpkg`, `docker`,
`docker-compose`, `systemd-run`, and `ufw`.

## Phase 0 safety rails

`internal/server/privilege_boundary_test.go` now:

- caps every reviewed privileged call family per production source file while
  permitting removals;
- rejects privileged calls added in an unreviewed file;
- rejects new or changed wildcard sudo constructors;
- rejects new or changed Docker group grants and any Docker socket reference in
  manager/runtime sources;
- rejects direct literal Docker commands through `exec.Command` or
  `system.RunCommand`;
- caps the reviewed privileged `/bin/sh -c` vectors, including helper-built
  argument lists.

Updating a ceiling is not a routine test fix. It requires updating this
inventory with an owner, typed replacement, and negative-test strategy.

## T0 integration-node baseline

The complete secret-free capture is stored at
`docs/baselines/privilege-hardening-t0-2026-08-10.json` with SHA-256
`4c7f5c0d8dea869201627abf47b9b7d2e909ff9755ec0105ed8c348618edb0c9`.

Capture summary:

- LightningOS `0.5.2-Beta`, deployed commit
  `3cbad807c2e69fa8ccea4913ca0f886dd465b9d5`, `install.sh` layout;
- Ubuntu 24.04.4 LTS, amd64, kernel 7.0.0-28-generic;
- manager health returned HTTP 200; manager, LND, terminal, reports timer,
  Docker, Postgres and Tor were active;
- the manager belongs to `docker`, `lightningos`, `lnd`, and
  `systemd-journal`, and its effective sudo list contains every wildcard family
  named above;
- Docker had 24 containers: 4 running and 20 stopped, plus 20 images, 13
  networks and 8 named volumes; original running/stopped choices were not
  changed;
- LND was synced to chain and graph at height 961865 with 1 active channel,
  100000 sat capacity, 0 sat local balance, 98998 sat remote balance, and 0 sat
  confirmed/unconfirmed wallet balance;
- the local Docker Bitcoin Core reported 960336 blocks against 961864 headers,
  `initial_block_download=true`, while LND was already at 961865. This is a
  baseline state to preserve/observe, not a hardening-induced regression;
- `ufw.service` was active but `ufw status` reported `inactive` with zero active
  rules; Tailscale was not installed. This is an existing exposure condition
  and no firewall change was made during T0;
- owner, group, mode, type, size, and presence were captured for the managed
  manager, UI, TLS, sudoers, systemd, helper, LND, App Store and Docker-socket
  paths without reading their contents.

The active-unit list intentionally excludes transient scopes, device/mount
identifiers, disk UUID units, addresses, and host-specific node identity.
Image tags that lexically resemble IPv4 addresses are redacted conservatively.

## Phase 0 exit assessment

Every current manager privilege call now has an owning feature, proposed typed
replacement, and test strategy. Current wildcard sudo, Docker group access,
generic privileged shells, and the test-node T0 state are covered by persistent
safety rails. Phase 1 may begin with the broker protocol while the integration
node remains unchanged on `0.5.2` until the shadow-broker checkpoint.

Post-baseline migration note (2026-08-12): both PeerSwap rows above have now
reached zero direct privileged manager calls. Their fixed binaries, source
policy, legacy-state migration, dedicated identity/LND credential, units,
lifecycle/removal and firewall are owned by closed broker/catalog operations.
The original counts remain in this document because they are the Phase 0
baseline; their per-file privilege-budget allowances were removed from the
permanent source gate.

Post-baseline migration note (2026-08-12): the Tapd Compose, Docker inspection,
raw `tapcli`, and HTLC-interceptor discovery rows have also reached zero direct
privileged manager calls. A root-only catalog snapshot and five typed broker
operations now own those boundaries. The Phase 0 counts remain historical;
the Tapd allowance was removed from the permanent privilege-budget test.

Post-baseline migration note (2026-08-12): Public Pool now has zero direct
privileged manager calls. Five typed broker operations own its exact
digest-pinned runtime, fixed Bitcoin consumer wiring, status, lifecycle,
data-preserving removal, and fixed-port firewall policy. The historical Phase
0 count remains above; its permanent privilege-budget allowance is now zero.
