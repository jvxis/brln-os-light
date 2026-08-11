# Privilege hardening persistent handoff

This document is the entry point for continuing pull request
[#33](https://github.com/jvxis/brln-os-light/pull/33) in another session. It is
an operational index, not a replacement for the accepted implementation plan
in `docs/32_PRIVILEGE_HARDENING_PLAN.md`.

Last reconciled: 2026-08-11.

## Authoritative objective

Complete the entire LightningOS `0.5.3` privilege-hardening plan on branch
`agent/0.5.3-privilege-hardening`. A single application migration, including
the current Electrs slice, is only a subgoal. The work is complete only when
all completion criteria in the plan are proven and Draft PR #33 is ready for
review. Do not redefine completion around the latest migrated app.

The scope includes:

1. the complete App Store and native-app privileged-operation migration;
2. package, firewall, storage, installer, and upgrade supply-chain hardening,
   including issue
   [#34](https://github.com/jvxis/brln-os-light/issues/34);
3. removal of wildcard sudo, direct Docker access, and Docker group membership
   only after the complete compatibility and lifecycle matrix passes;
4. manager and broker systemd confinement;
5. manager and per-app LND credential separation;
6. fresh-install and supported-upgrade acceptance on Ubuntu 24.04 and 26.04;
7. rollback, failure-injection, security, operator, and architecture evidence.

## Repository checkpoint

- Branch: `agent/0.5.3-privilege-hardening`.
- Remote Draft PR: `#33`, targeting `main`.
- Reconciled HEAD: `293250301044bed90169e1302cae0009dda905f6`.
- Reconciled HEAD subject:
  `0.5.3-Beta: harden LNbits image and LND credential`.
- The worktree was clean when this handoff was created.
- Every new implementation or evidence commit must start with
  `0.5.3-Beta`.
- `main` may continue receiving `0.5.2` work. Do not merge or rebase it into
  this branch merely because it advanced; first inspect whether a concrete
  dependency or conflict requires it.
- Keep PR #33 in draft until the full plan, not only Phase 2, passes.

At the beginning of a continuation session, verify these values rather than
assuming they remain current:

```text
git status -sb
git branch --show-current
git log -1 --oneline
gh pr view 33 --json state,isDraft,headRefName,baseRefName,url
```

## Canonical documents and evidence

Read these before making a new migration decision:

- `docs/32_PRIVILEGE_HARDENING_PLAN.md`: full accepted scope, current narrative,
  phase exits, test-node protocol, and final completion criteria;
- `docs/33_PRIVILEGE_HARDENING_PHASE0_INVENTORY.md`: privileged-call inventory
  and replacement ownership;
- `docs/34_PRIVILEGED_BROKER_PROTOCOL.md`: typed protocol and operation contract;
- `docs/35_PRIVILEGE_HARDENING_PHASE2_APP_INVENTORY.md`: 20-app inventory and
  migration families;
- `docs/baselines/privilege-hardening-*.json`: secret-free runtime gates.

The plan is the authority when this handoff becomes stale. Update both the
plan and this checkpoint after a material slice is accepted.

## Accepted progress

### Phase 0

Completed. The complete privileged-call inventory, owner/replacement matrix,
safety rails, security-documentation correction, and secret-free T0 node
baseline are committed.

### Phase 1

Completed. The typed deny-by-default broker foundation, versioned protocol,
audit, mutation locking, manager client modes, installer self-test, fixed
service operations, fixed-file operation, shadow soak, reboot behavior,
rollback, and clean Ubuntu 24.04 install gate passed. The integration node
remains compatible with `shadow`; final `enforce` cutover is intentionally
deferred.

### Phase 2 shared foundation

Accepted slices include typed Docker package/runtime readiness, asynchronous
closed-catalog image preparation and status, fixed Compose lifecycle,
inspection and removal primitives, persistent root-owned execution snapshots,
and fixed external-firewall admission where already migrated.

Application state at this checkpoint:

| Application or family | Accepted boundary | Still open |
| --- | --- | --- |
| CPU Miner | Image, Docker readiness, install, config apply, inspect, lifecycle, and uninstall passed without manager Docker access | Cross-version final matrix only |
| RoboSats | Closed images, install, root-owned snapshot, lifecycle, inspect, firewall, and data-preserving uninstall passed | Cross-version final matrix only |
| Bitcoin Core | Official-source verified image, attestation, storage enrollment, secret config operations, lifecycle, consumer network, and native-consumer path passed | Existing-node RPC credential compatibility, operational CLI/log paths, mainnet P2P firewall, and final matrix |
| BTCPay Server/NBXplorer | Official fixed security release, image refresh, root-owned secret snapshot, dedicated LND macaroon, remote/native Bitcoin gates, lifecycle, data preservation, and real functional gate passed | Final matrix and any remaining shared host dependencies found by inventory |
| LNDg | Exact-source image build and attestation plus dedicated 13-permission LND credential and narrow read-only mounts | Broker-owned Compose/secret snapshot, lifecycle, status, uninstall, firewall/host dependencies, and complete functional gate |
| LNbits | Official stable digest plus dedicated nine-permission LND credential and narrow read-only mount design | Broker-owned Compose/secret snapshot, lifecycle, status, uninstall, REST-listener/firewall policy, and data-preserving functional gate |
| Electrs | Existing manager-side implementation and Full Node manager gate only; no Electrs broker migration is committed yet | Entire closed image/manifest/secret/lifecycle/inspect/remove contract and functional gate |
| Remaining Docker apps | Inventory only unless the plan states otherwise | Migrate each complete contract and lifecycle matrix |
| Native apps and in-process features | Inventory plus isolated shared primitives only | Migrate privileged systemd, files, users, binaries, storage, firewall, and credential paths as applicable |

No phase is complete merely because the applications listed as accepted above
work individually. Phase 2 exits only when the complete supported App Store
works while the manager has no Docker group/socket access.

## Current immediate slice: Electrs

The immediate implementation slice at this checkpoint is Electrs. It must not
be mistaken for the final PR objective.

Closed upstream research already completed:

- upstream: `romanz/electrs`;
- release: `v0.11.1`;
- annotated tag object: `8e9bf28431b00286552248bd438ba5c2d4efaada`;
- resolved source commit: `35216c6d30148be8e6763d913d437330f431fc03`;
- GitHub reports the tag signature as verified;
- source archive:
  `https://codeload.github.com/romanz/electrs/tar.gz/refs/tags/v0.11.1`;
- source archive SHA-256:
  `d51db4ffe2eac77deb62b6cf51745c3c9ef3965ca1bd72d3fd5c69f64540e33f`;
- source archive size observed: `1380752` bytes;
- upstream publishes no production-supported official container image or
  release binary; its Dockerfile is explicitly demonstrative;
- selected build base:
  `debian:trixie-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258`.

These values must be independently revalidated if upstream state or the base
image is intentionally updated. Mutable transitive Debian/Rust dependencies
remain in the issue #34 supply-chain track and must not be represented as fully
solved by the source checksum alone.

Required Electrs end state:

1. Add an `appmanifest` catalog entry fixing the image, project, Compose and
   env filenames, services, networks, mounts, localhost-only ports, named
   index volume, stop bound, image variant, source identity, and remove-volume
   policy.
2. Have the broker build the LightningOS Electrs image only from the exact
   verified archive, run a fixed `electrs --version` compatibility probe, and
   bind the local image ID to a root-owned attestation.
3. Keep the manager-written declaration secret-free except for one private
   manager-side RPC credential input; validate every byte of the declaration
   and create a root-owned persistent execution snapshot. The container must
   see only its dedicated `user:password` cookie, read-only, and must never
   receive `bitcoin.conf`, LND material, the Docker socket, or broad host
   mounts.
4. Route install/start/stop/status/uninstall exclusively through typed broker
   operations. Compose execution must use only the broker snapshot. Electrs
   removal deliberately removes its reproducible named index volume; this
   behavior must come from the closed catalog, not a caller flag.
5. Before every start, the broker must independently authenticate to the fixed
   local Bitcoin RPC endpoint and prove: expected chain, `pruned=false`, initial
   block download false, chain fully synchronized, and `txindex` present and
   synchronized. Authentication failure must be distinct from zero-percent
   synchronization and must fail closed before Compose.
6. Support both App Store Bitcoin Core (`bitcoind` on the fixed consumer
   network) and a recognized external native/systemd Bitcoin Core through the
   fixed host gateway. Arbitrary remote Bitcoin endpoints are not an Electrs
   install option.
7. Add negative tests for unknown variants, manifest/env/cookie tampering,
   secret modes and symlinks, arbitrary hosts/ports/networks, bad credentials,
   pruned/IBD/unsynchronized/no-txindex nodes, unattested images, caller volume
   flags, and manager Docker fallback.
8. Run a functional gate only against an isolated synchronized, unpruned
   `txindex=1` node. Isolated regtest is acceptable only when it genuinely
   satisfies that same contract. Do not claim LOS TESTE2 as the positive
   Electrs gate while its local Bitcoin is incomplete or RPC authentication is
   unresolved.
9. Preserve evidence, remove all temporary files/containers/networks/VM state,
   commit with the required prefix, push, and update Draft PR #33.

At this checkpoint, no Electrs code has been modified. The working design was
being reconciled against `internal/appmanifest/catalog.go`,
`internal/privileged/apps.go`, the LNDg build-attestation pattern, and
`internal/server/apps_electrs.go`.

## Test-node and network constraints

The integration node record is outside Git at
`D:\Users\jaime\.secrets\infos_los-test2.txt`. Never copy its contents into a
commit, test output, evidence JSON, PR comment, shell transcript, or this file.

Operational rules from the owner:

- LOS TESTE2 is preserved integration infrastructure, not a disposable host.
- Its reserved address must be taken only from the external record. Do not scan
  the LAN, probe address ranges, renew DHCP, reset adapters, restart networking,
  or change VirtualBox bridge/NAT settings to locate it.
- Bitcoin and LND data, wallets, channels, app data, databases, network policy,
  and original running/stopped choices must be preserved.
- Bitcoin must be restarted as little as possible and never merely for a
  hardening experiment; dependent services may restart when Bitcoin restarts.
- The local Bitcoin is the App Store Docker application using the external HDD.
  Read-only inspection on 2026-08-11 showed it continuing to synchronize at
  approximately 99.86%, with no container restart or OOM. The UI's `0%` was an
  RPC-authentication reporting failure, not blockchain loss or a stopped
  indexer.
- The active config predates the broker migration and contains `rpcauth`
  without recoverable `rpcuser`/`rpcpassword`. Existing manager credential
  discovery cannot authenticate, so the hardening branch must preserve the
  file and report this as an authentication/configuration state rather than
  `0%`. Do not silently rotate credentials or restart Bitcoin.
- Existing nodes may instead use an external native/systemd Bitcoin Core. The
  fixed native-consumer gateway path is part of the supported contract.
- Electrs and Mempool require a synchronized, unpruned Full Node with
  `txindex=1`. Never weaken this gate.
- BTCPay may use the already validated remote Full Node through Tor and its
  dedicated LND credential. Preserve that source selection.

For disposable installation or destructive gates, the owner permits a local
VirtualBox clone of `brln-os-basica` when needed. Prefer an existing disposable
clone and localhost-only access. Verify the exact VM and resolved paths before
any destructive cleanup. Power it off and remove only deliberately created
temporary state after evidence capture.

## Product priorities and migration sequencing

Observed adoption informs order but does not remove any app from the PR scope:

1. high use: LNDg, LNbits, and native BRLN products such as BRLN Loop and Magma;
2. foundational: official Bitcoin Core and BTCPay/NBXplorer with current
   official security releases;
3. lower use: Fedimint, Electrs, and Mempool.

Electrs and Mempool are intentionally late because their positive gate needs a
fully synchronized unpruned `txindex=1` Bitcoin. Fedimint is also lower
priority. If the immediate Electrs slice is completed before the higher-use
apps' lifecycle work, return afterward to the open LNDg/LNbits/native-app
contracts rather than treating Phase 2 as complete.

## Global security invariants

- The manager must never regain a generic privileged shell, arbitrary sudo
  arguments, direct Docker socket access, or caller-selected privileged paths.
- The broker accepts typed IDs and enums; images, commands, units, paths,
  mounts, ports, networks, timeouts, remove semantics, owners, and modes come
  from root-owned code/catalog data.
- Secrets never enter command-line arguments, audit records, API errors, test
  fixtures based on production values, PR comments, or evidence files.
- Secret-bearing inputs are private regular files, symlink-resistant, bounded,
  atomically snapshotted, and mounted read-only with the narrowest possible
  scope.
- Failures are fail-closed and preserve existing services/data. Unsupported
  existing layouts stop before mutation and provide actionable diagnostics.
- Official upstream artifacts and current security releases are preferred.
  Provenance claims must match what was actually verified; checksums alone are
  not signatures.
- Temporary worktree files, downloaded archives, test credentials, containers,
  networks, volumes, transient units, and disposable VM changes are accounted
  for and cleaned before each accepted evidence commit.

## Work after Phase 2

Do not stop at App Store migration. Continue the numbered Phase 3, Phase 4,
and Phase 5 items in the plan, then run the complete required validation and
completion audit. In particular:

- finish issue #34 installer/upgrade authenticity gates. The contributor has
  already been thanked publicly in
  [the issue](https://github.com/jvxis/brln-os-light/issues/34#issuecomment-5246727716);
  do not post a duplicate acknowledgement merely when resuming work;
- eliminate all remaining direct `apt`, `dpkg`, UFW, `systemd-run`, Docker, and
  privileged shell call sites from the manager;
- execute supported fresh-install and upgrade matrices on Ubuntu 24.04 and
  26.04;
- remove wildcard sudo and Docker group access only in the final controlled
  cutover with a tested rollback bundle;
- enable and validate manager/broker systemd confinement;
- finish manager and per-app LND credential minimization;
- update the security/operator documentation and PR body so they describe the
  final, not intermediate, trust boundary.

Only the seven completion criteria in
`docs/32_PRIVILEGE_HARDENING_PLAN.md` authorize declaring the full PR work
complete.
