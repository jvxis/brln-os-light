# Privilege hardening persistent handoff

This document is the entry point for continuing pull request
[#33](https://github.com/jvxis/brln-os-light/pull/33) in another session. It is
an operational index, not a replacement for the accepted implementation plan
in `docs/32_PRIVILEGE_HARDENING_PLAN.md`.

Last reconciled: 2026-08-13.

## Authoritative objective

Complete the entire LightningOS `0.5.3` privilege-hardening plan on branch
`agent/0.5.3-privilege-hardening`. Any single application migration is only a
subgoal. The work is complete only when
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
- Published checkpoint entering the Public Pool slice: `23f2e92`.
- The accepted PeerSwap implementation checkpoint is `a3cfb2b`, subject
  `0.5.3-Beta: harden PeerSwap runtime`.
- The accepted Tapd implementation checkpoint is the commit carrying this
  handoff, subject `0.5.3-Beta: harden Tapd runtime`.
- The accepted Public Pool implementation checkpoint is the commit carrying
  this handoff, subject `0.5.3-Beta: harden Public Pool runtime`.
- `origin/main` was then integrated through `c0c488a` because it had a concrete
  conflict in PeerSwap and supplied the owner's updated PSWeb binary in
  `cd0ea3e`, plus the intervening 0.5.2 fee/Magma fixes. The hardening catalog
  now pins that binary to official upstream commit `09983da` and its tested
  SHA-256; this was not a routine merge merely because `main` advanced.
- Tapd gate uploads and remote files were removed, local cache directories were
  sent to the recycle bin, and the worktree contains no temporary artifacts.
- Every new implementation or evidence commit must start with
  `0.5.3-Beta`.
- The shared Docker cutover checkpoint is `3d12bf53`. A subsequent disposable
  Ubuntu 26.04 fresh-install gate found and fixed sudo/Go VCS ownership failure
  and setup-token leakage into captured installer logs. The exact gate and
  remaining scope are recorded in
  `docs/baselines/privilege-hardening-ubuntu26-fresh-install-2026-08-13.json`;
  the full application matrix and Phases 3-5 remain open.
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

### Phase 3

Started on 2026-08-13. Manager firewall status is now the read-only typed
`manager.firewall.status` broker operation; the Manager's direct privileged
UFW call budget is zero. The Ubuntu 26.04 service-user gate changed neither
the UFW status hash nor the rules timestamp. Evidence:
`docs/baselines/privilege-hardening-phase3-manager-firewall-status-2026-08-13.json`.
The LND upgrade is now the typed, serialized `upgrade.lnd.start` broker
operation. It has no caller-selected URL/path/command, accepts only the exact
broker-digest-pinned embedded helper, authenticates the official manifest with
at least five compiled LND primary-key fingerprints, verifies the selected
archive hash before extraction, and supports a non-mutating `verify-only`
gate. Ubuntu 26.04 authenticated `v0.21.1-beta` with six signers; LND/lncli
hashes and the stopped service state were unchanged, and a tampered helper was
rejected. The manager direct privilege budget and direct helper sudoers entry
for this path are zero. Evidence:
`docs/baselines/privilege-hardening-phase3-lnd-upgrade-2026-08-13.json`.

Tor metadata refresh and upgrade launch are now the typed, serialized
`packages.tor.refresh` and `upgrade.tor.start` operations. The manager has no
direct privileged call on this path. The broker permits no caller-selected
package, URL, path, command, or unit; it accepts only the digest-pinned embedded
helper and chooses fixed verify/upgrade units. Before any repository state is
installed, the helper checks the exact official Tor primary-key fingerprint
and verifies the downloaded `InRelease` signature with that exported key.
Ubuntu 24.04 `noble` passed the service-user verify-only gate without changing
the Tor package, APT source/keyring/metadata, Tor PID, state, or restart count;
a tampered helper and injected URL failed closed. Evidence:
`docs/baselines/privilege-hardening-phase3-tor-upgrade-2026-08-13.json`.

LightningOS self-upgrade launch is now the typed, serialized
`upgrade.lightningos.start` operation. The manager direct sudo, `systemd-run`,
privileged-shell, and helper-install budgets are zero, and the direct helper
sudoers authorization was removed from all installer/upgrade variants. The
broker digest-pins the embedded helper, fixes its path and units, and permits
only version, a matching tag, a full commit, and `verify_only`; the repository
is fixed and no longer request-selectable. Before build, the helper binds the
tag to the expected full commit, validates the source version, checks out that
commit rather than the tag, and uses `npm ci`. Ubuntu 24.04 passed the
service-user verify-only gate without changing manager/UI/config/sudoers,
source cache, log/temp state, or Manager/LND/Bitcoin services. An injected
repository, modified helper, and wrong commit failed closed. Evidence:
`docs/baselines/privilege-hardening-phase3-lightningos-upgrade-2026-08-13.json`.

Do not describe this as independent publisher authentication yet: the tested
historical release/tag/commit are unsigned. Add a signed release manifest or
equivalent trusted publisher attestation before closing issue #34's
self-upgrade supply-chain requirement.

Shared App Store storage repair is now the empty, serialized
`storage.apps.ensure` operation. The broker fixes the `lightningos` identity,
the three shared paths and mode `0750`, and walks by directory descriptor with
`O_NOFOLLOW`; the manager's direct `systemd-run install -d` budget is zero.
The Ubuntu 24.04 service-user gate rejected an injected path and symlink,
changed only a temporary test tree, and preserved the real storage metadata
and Manager/LND/Bitcoin states. Evidence:
`docs/baselines/privilege-hardening-phase3-app-storage-2026-08-13.json`.

SMART diagnostics now use read-only `storage.smart.read`; the manager's direct
sudo budget is zero. The broker requires a bounded device to match its own
fixed `lsblk` whole-disk inventory and runs only root-owned fixed `smartctl -a`
argv with bounded output. Ubuntu 24.04 passed the service-user read plus flags,
traversal, non-disk and symlink negative gates without service changes.
Evidence: `docs/baselines/privilege-hardening-phase3-smart-read-2026-08-13.json`.
The now-unused wildcard smartctl grants are deliberately still listed until
the coordinated Phase 4 sudoers cutover.

Post-wallet LND metadata repair now uses empty serialized
`storage.lnd.permissions.repair`; `handlers.go` has zero direct sudo calls.
The broker fixes the `lnd` identity and a non-recursive descriptor-relative
allowlist of directories, `lnd.conf`, `tls.cert` and mainnet macaroons. It
excludes channel/wallet/graph/backup state and rejects symlinks. Ubuntu 24.04
preserved the real `/data/lnd` metadata hash and all core service states while
the temporary-tree gate proved exact metadata repair and byte preservation.
Evidence:
`docs/baselines/privilege-hardening-phase3-lnd-permissions-2026-08-13.json`.
Remove the now-unused direct helper grant only in the coordinated sudoers
cutover.

Service restarts and host power are now enforce-only broker operations. The
manager no longer falls back to direct `systemctl`, `sudo`, or transient
`systemd-run`; `handlers.go` has zero restart/power compatibility calls and
`internal/system/system.go` has zero sudo calls. `host.power` accepts only
`reboot|poweroff`, schedules a fixed delayed command, and validates strict
typed completion state. Disabled/shadow requests fail closed. Ubuntu 24.04
validated dry-run rejection/preservation and a real disposable-VM poweroff,
then restored the original broker and VM network state. Evidence:
`docs/baselines/privilege-hardening-phase3-service-power-2026-08-13.json`.
The now-unused service/power sudoers entries remain until the coordinated
cutover.

The typed package catalog now includes `mdns`, fixed to
`avahi-daemon` plus `libnss-mdns` and isolated index/install units. Ubuntu
24.04 passed service-user dry-run, read-only status, injected-package rejection,
and package/service/lock preservation without apt, firewall, or network
mutation. Evidence:
`docs/baselines/privilege-hardening-phase3-mdns-package-2026-08-13.json`. Use
this feature as the package prerequisite for the system-integration cut; do not
put apt back inside the reconciler.

Login-protection activation is now broker-only: fixed `files.enable_login`
followed by delayed `service.restart`. `auth_enable.go` no longer writes YAML
directly, invokes sudo/systemd-run/visudo, creates a runtime sudoers file, or
contains a privileged shell. Disabled/shadow requests return 503 and preserve
the in-memory setting; the previously accepted real Ubuntu 24.04 activation
and restart gates cover enforce mode. Evidence:
`docs/baselines/privilege-hardening-phase3-auth-enable-2026-08-13.json`. During
Phase 4, detect and remove any stale
`/etc/sudoers.d/lightningos-auth-enable` only after validating it as the exact
legacy root-owned regular file.

Firewall policy reconciliation, real Tor/LightningOS upgrade and rollback
matrices, LightningOS publisher attestation, remaining helper/storage
operations, wildcard sudoers removal, installer authenticity, and the rest of
issue #34 remain open.

Application state at this checkpoint:

| Application or family | Accepted boundary | Still open |
| --- | --- | --- |
| CPU Miner | Image, Docker readiness, install, config apply, inspect, lifecycle, and uninstall passed without manager Docker access | Cross-version final matrix only |
| RoboSats | Closed images, install, root-owned snapshot, lifecycle, inspect, firewall, and data-preserving uninstall passed | Cross-version final matrix only |
| Bitcoin Core | Official-source verified image, attestation, storage enrollment, secret config operations, lifecycle, consumer network, native-consumer path, cookie-backed typed local status, and brokered bounded logs passed | Dedicated Electrs credential migration for legacy `rpcauth` nodes, mainnet P2P firewall, and final matrix |
| BTCPay Server/NBXplorer | Official fixed security release, image refresh, root-owned secret snapshot, dedicated LND macaroon, brokered LND host-access reconciliation, remote/native Bitcoin gates, lifecycle, data preservation, and real functional gate passed | Final cross-version matrix |
| LNDg | Exact-source non-root image, official digest-pinned private PostgreSQL, dedicated 13-permission LND credential, root-owned snapshot, typed lifecycle/inspect/remove/admin reset, firewall/host policy, SQLite-to-PostgreSQL migration, data preservation, and real functional gate passed | Cross-version final matrix only |
| LNbits | Official stable digest, dedicated nine-permission LND credential, root-owned Compose/environment/credential snapshot, non-root read-only runtime, typed lifecycle/status/uninstall/REST/firewall, and data-preserving legacy SQLite/Admin UI migration accepted on LOS TESTE2 | Final Ubuntu 24.04/26.04 matrix and shared rollout/rollback acceptance only |
| BRLN Loop Out and Magma | Native non-root/PostgreSQL lifecycle confirmed free of OS-privileged execution; permanent source gate added; authenticated LOS TESTE2 state-preserving gate passed; no third-party disclaimer applies | Final cross-version matrix only |
| Lightning Loop | Closed native broker contract for fixed official release, user/directories, config/unit, dedicated LND material, lifecycle/status/removal and client-material repair; existing stopped installation preserved through real LOS TESTE2 gate | Clean install/reboot on final Ubuntu 24.04/26.04 matrix and shared enforce/rollback cutover |
| Elements | Closed native broker for official digest-pinned release, dedicated identity, enrolled storage, safe config merge/read, bounded RPC status, hardened unit and lifecycle/removal; preserved inactive LOS TESTE2 install migrated without dependency restarts | Clean install/reboot and shared final matrix |
| PeerSwap | Fixed upstream v6.0.0 and official PSWeb commit `09983da` (v6.0.0.1 package) hashes in the compatible legacy asset folder, dedicated identity and nine-permission LND credential, root-only local/remote Elements policy, state-preserving migration, hardened units, typed status/lifecycle/removal/firewall; remote-mode LOS TESTE2 gate passed without starting services | Clean install/reboot on final Ubuntu 24.04/26.04 matrix and shared enforce/rollback cutover |
| Tapd | Official stable v0.8.0 manifest digest, five-signature release gate, exact closed Compose snapshot, dedicated nine-permission LND credential, typed lifecycle/status/remove/CLI, SSRF-safe universe discovery, and sensitive-action reauthentication; stopped LOS TESTE2 installation migrated without dependency restarts | Clean install, real functional lifecycle on a disposable node, reboot persistence, final Ubuntu 24.04/26.04 matrix, and shared enforce/rollback cutover |
| Public Pool | Immutable backend/UI manifest digests correlated to fixed source revisions, exact root-only non-root/read-only runtime, typed Bitcoin modes/status/lifecycle/remove/firewall, CPU Miner broker dependency, and state-preserving stopped LOS TESTE2 gate | Clean install, real mining/API/UI lifecycle on a disposable node, reboot persistence, final Ubuntu 24.04/26.04 matrix, and shared enforce/rollback cutover |
| Bark Wallet | Official digest-pinned Bark Web v0.7.2/Bark 0.6.1/Caddy images, architecture-specific daemon binary attestation, exact root-only snapshot, non-root read-only runtime, typed status/lifecycle/remove/firewall/password operations, protected seed routes, and real stopped-to-running-to-stopped LOS TESTE2 gate | Decide whether mnemonic reveal needs fresh password reauthentication before final security sign-off; clean install/reboot, final Ubuntu 24.04/26.04 matrix, and shared enforce/rollback cutover |
| Electrs | Verified-source image, fixed non-root manifest, private dedicated cookie, root-owned snapshot, typed lifecycle/inspect/remove, independent Full Node gate, and isolated functional gate passed | Cross-version final matrix and one-time dedicated credential migration on legacy `rpcauth` nodes |
| Mempool | Closed official images, Full Node/Electrs admission, root-owned snapshot, lifecycle, inspection, removal, firewall and legacy MariaDB upgrade passed | Final cross-version matrix |
| Fedimint Guardian/Gateway | Closed official images, exact private runtimes, dedicated Gateway LND credential, lifecycle, logs, firewall and data-preserving migration passed | Final cross-version matrix |
| Native apps and in-process features | Closed broker boundary where OS privilege is required; otherwise non-root manager/PostgreSQL with permanent source gates | Final cross-version application regression matrix |

The 20-app code boundary and shared Docker cutover are implemented. A permanent
source test rejects manager-side Docker/Compose execution and Docker socket
access; fresh installs default to `enforce`; upgrade rollback restores the
previous sudoers, config, service/drop-in and Docker membership without touching
node or app data. The Ubuntu 24.04 disposable gate passed forward, rollback and
forward again while preserving config ACLs and Bitcoin/LND timestamps. Evidence:
`docs/baselines/privilege-hardening-phase2-shared-cutover-2026-08-13.json`.

Phase 2 is still not accepted until the complete supported App Store lifecycle
and reboot matrix passes on both Ubuntu 24.04 and 26.04.

## Accepted slice: Electrs and Bitcoin Local status

The Electrs implementation slice is accepted. It must not be mistaken for the
final PR objective.

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

Accepted Electrs end state:

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
8. The functional gate ran only against an isolated synchronized, unpruned
   `txindex=1` node. Isolated regtest is acceptable only when it genuinely
   satisfies that same contract. Do not claim LOS TESTE2 as the positive
   Electrs gate while its local Bitcoin is incomplete or RPC authentication is
   unresolved.
9. Evidence was preserved and all temporary files, containers, networks,
   volumes, transient units, source trees, and disposable VM settings were
   removed or restored before publication.

The Ubuntu 24.04 gate built image ID
`sha256:db39852159a1324aefca44fc4dd8f0a3d968a8a6891ab1565e0bfa6dee827141`
and passed with an isolated 101-block regtest chain, `pruned=false`, and a
fully synchronized transaction index. It exercised Electrum protocol,
Prometheus metrics, stop/start, data preservation across stop, and index-volume
removal on uninstall. Failed build dependencies and incomplete wallet/txindex
fixtures remained fail-closed and were corrected before acceptance.

Bitcoin Local status now has a separate closed read-only broker operation. It
uses the container's runtime cookie through fixed `bitcoin-cli` calls, never
returns credentials, and is authoritative in `enforce`. In `shadow`, the
compatibility reader remains authoritative so a slow indexing RPC cannot hide
the bounded `debug.log` synchronization progress; it also reuses the stored
LightningOS Bitcoin Local block from `lnd.conf` when that block still contains
credentials. Existing canonical App Store nodes receive idempotent storage
enrollment without any Bitcoin, Docker, network, or dependent restart.
Read-only LOS TESTE2 validation observed mainnet at blocks 961493/headers
962058, verification progress 0.9986531584779004, unpruned, with about 806 GiB
on disk. Its active remote LND block contains credentials, but the preserved
local block has empty user/password fields; the current `rpcauth` hash cannot
recover them. The final shadow gate nevertheless returned running at block
961534 and progress 0.998754 from the bounded log fallback. The node was not
used as the Electrs positive gate. Its legacy `rpcauth` still needs a one-time
dedicated Electrs credential migration once it is synchronized; this status
fix deliberately does not rotate credentials or restart Bitcoin.

The App Store Bitcoin bootstrap now closes the credential gap for future
installs. The manager sends a credential-free standard config template; the
broker generates `lightningos` credentials and writes only `rpcauth` to
`bitcoin.conf`, while retaining the recoverable tuple in its root-only `0600`
state. `app.bitcoincore.credentials.read` recomputes the HMAC and verifies the
matching active config before returning the tuple locally for LND or a managed
consumer. No HTTP route exposes it, and audits/logs contain neither username
nor password. Existing configs, including LOS TESTE2, are preserved without
rotation or Bitcoin restart and remain queued for explicit one-time migration
when consumer credentials are needed.

The final disposable functional gate authenticated successfully against the
official-source verified Bitcoin Core 31.1 image using the broker-generated
config and rejected a wrong password. The full Linux suite, vet, manager and
broker builds, registry validation, and UI build passed. LOS TESTE2 was updated
only with the tested manager/broker binaries; Bitcoin retained the same
container ID and `StartedAt`, and the UI/API still reported real progress via
the bounded log fallback while RPC was busy. All uploads/backups were removed,
and the VM was cleaned, powered off, and returned to its original bridge.

Evidence is stored in
`docs/baselines/privilege-hardening-phase2-electrs-functional-2026-08-11.json`
and
`docs/baselines/privilege-hardening-phase2-bitcoincore-status-2026-08-11.json`.

The high-adoption native BRLN slice is accepted. BRLN Loop Out and Magma remain
inside the non-root manager and need no broker operation: their lifecycle is
PostgreSQL-only and a permanent source gate rejects OS-privileged imports,
Docker/systemd/sudo wrappers, host file mutation, and host listeners. They are
first-party native features, so the third-party App Store disclaimer does not
apply. The authenticated LOS TESTE2 gate preserved both as installed/running,
with Loop idle and Magma in monitor mode, and preserved LND/Bitcoin timestamps. The integration node's
legacy manager Docker-group membership remains an explicit shared-cutover item.
Mempool and Fedimint remain lower priority.

The Lightning Labs Loop systemd slice is accepted separately from BRLN Loop
Out. `apps_loop.go` now has zero direct privileged call sites; a closed broker
catalog owns the pinned `v0.33.3-beta` release/checksums, fixed service account,
paths, loopback ports, configuration, hardened unit, lifecycle, removal, ACLs,
and manager-readable API credential copies. The third-party elevated-LND
notice remains appropriate for Lightning Loop. LOS TESTE2 had an existing
persistent installation that was inactive/disabled. Real status, idempotent
ensure/permission repair, client-material repair, and start dry-run passed
without changing that choice or the hashes of the swap database, dedicated LND
macaroon, Loop API macaroon, or TLS private key. Global mode remained `shadow`;
the broker was called directly as `lightningos`. Bitcoin/LND timestamps were
preserved and gate files were removed. Evidence:
`docs/baselines/privilege-hardening-phase2-lightning-loop-boundary-2026-08-12.json`.
The Elements provider half of the coupled native domain is accepted.
`apps_elements.go`, `elements_status.go`, and `elements_mainchain.go` now have
zero direct privileged calls. The broker fixes official `23.3.3` Linux release
digests, extracts only `elementsd`/`elements-cli`, creates the dedicated
`lightningos-elements` identity, enrolls the operator-selected default or
distinct/external mounted storage target, merges existing config while forcing
loopback-only RPC, confines the systemd unit,
and exposes only two read-only status RPC methods. Config reads fail unless the
target is already root-enrolled. On LOS TESTE2 the real ensure retained
inactive/disabled state, config and RPC credential hashes, and Bitcoin/LND
timestamps while removing `losop` ownership from both trees. Evidence:
`docs/baselines/privilege-hardening-phase2-elements-boundary-2026-08-12.json`.
The PeerSwap consumer half is accepted as well. The broker pins the actual
v6.0.0 and official PSWeb commit `09983da` (v6.0.0.1 package) hashes while
retaining the `version_5_0/amd64` packaging path,
creates `lightningos-peerswap`, migrates regular legacy state without deleting
the rollback tree, forces a dedicated nine-permission LND credential, and owns
the root-only Elements source, validated configs, hardened units,
lifecycle/removal and port-1984 firewall contract. Local mode preserves the
enrolled store Elements volume; remote mode is independent of the disabled
local service. LOS TESTE2 proved the remote path, byte-preserved state,
root-only policy promotion, allowed LND reads, denied macaroon administration,
and unchanged Bitcoin/LND/manager timestamps while PeerSwap stayed
inactive/disabled. Evidence:
`docs/baselines/privilege-hardening-phase2-peerswap-boundary-2026-08-12.json`.

The Tapd slice is accepted. Five typed broker operations now own its root-only
declaration, lifecycle, removal, status, and eight CLI actions. The stable
official `v0.8.0` image is pinned by manifest digest; a case-only correction to
the upstream verifier proved its manifest and binaries against five developer
signatures without weakening quorum. The production runtime probe requires the
exact `tapd` and `tapcli` version outputs in a no-network, read-only,
capability-free container. Tapd receives a dedicated nine-permission LND
macaroon rather than admin access. LOS TESTE2 retained Tapd stopped and
preserved its data hash and Bitcoin/LND/manager activation timestamps. Evidence:
`docs/baselines/privilege-hardening-phase2-tapd-boundary-2026-08-12.json`.

The Public Pool slice is accepted. Its five typed operations own the exact
root-only runtime, lifecycle, status, data-preserving removal and fixed-port
firewall boundary. Both digest-pinned image probes passed without network and
without starting the preserved containers. LOS TESTE2's three database files
remained content-identical across the UID/GID 65532 migration; Bitcoin, LND,
manager and Public Pool lifecycle state remained unchanged. Evidence:
`docs/baselines/privilege-hardening-phase2-publicpool-boundary-2026-08-12.json`.

The Bark Wallet slice is accepted. Seven typed operations own its exact
runtime, lifecycle, status, data-preserving removal, fixed port, and UI
password read/reset. The official v0.7.2 web/API, Bark 0.6.1 daemon, and Caddy
images are manifest-digest pinned. Because the daemon image's published
entrypoint is a legacy regtest wrapper, the broker bypasses it and requires
the official architecture checksum plus exact version output from
`/usr/local/bin/barkd` before lifecycle execution. All services are non-root,
read-only, capability-free, and `no-new-privileges`; only barkd can write the
wallet bind. LOS TESTE2 passed HTTPS, login, direct mnemonic denial,
unauthenticated reveal denial, start/stop, and state/TLS preservation checks,
then returned to stopped without Bitcoin, LND, manager, firewall, or network
changes. Upstream mnemonic reveal has session+CSRF protection but no fresh
password reauthentication; preserve the beta warning and resolve that policy
before final sign-off. Evidence:
`docs/baselines/privilege-hardening-phase2-bark-wallet-boundary-2026-08-12.json`.

The Mempool slice is accepted. Official v3.3.1 frontend/backend and MariaDB
10.11.18 images are digest-pinned; the broker owns the closed declaration,
private snapshot, image preparation/probes, lifecycle, status, volume-removing
uninstall and fixed port 8999 firewall rule. All services are non-root,
read-only, capability-free and `no-new-privileges`, with writable state limited
to bounded tmpfs and the named database/cache volumes. The broker independently
requires a synchronized unpruned `txindex=1` Bitcoin Full Node and running
catalog Electrs before start. A disposable 101-block regtest/Electrs gate passed
API readiness, stop/start and uninstall, and a MariaDB 10.5.21 fixture upgraded
to 10.11.18 without losing its row. LOS TESTE2 remained read-only, with Mempool
and Electrs absent and Bitcoin/LND/manager timestamps preserved. Evidence:
`docs/baselines/privilege-hardening-phase2-mempool-functional-2026-08-13.json`.

Both Fedimint apps are accepted. The shared cutover subsequently removed the
manager's Docker-group membership and direct Docker/Compose path on the
disposable gate. The remaining Phase 2 work is the Ubuntu 24.04/26.04 complete
lifecycle and reboot acceptance matrix, not another Docker app migration.

The system-integration boundary is accepted. The generic staged root
reconciler was removed and the manager's permanent `runSystemd` budget for
this feature is now zero. The new flow first requires the closed `mdns`
package feature, installs four broker-catalogued embedded helpers only when
their SHA-256 values match, applies fixed TLS/mDNS/firewall and LND policy,
enrolls any existing Bitcoin Store storage through its existing typed
operation without restarting Bitcoin, restarts only an already-active
terminal when needed, writes the v5 marker last, validates marker readiness
plus every installed asset through a read-only broker operation, and schedules
a manager restart only if its certificate actually changed. A disposable
Ubuntu 24.04 clone passed the Linux/root filesystem test and service-user dry-run gate;
tampered bytes and an injected path failed closed, package/lock/firewall and
Manager/LND/Postgres activation state stayed unchanged, all artifacts were
removed, and the original bridge was restored. Real TLS/firewall application
remains for the final disposable fresh-install matrix. Evidence:
`docs/baselines/privilege-hardening-phase3-system-integrations-2026-08-13.json`.

The runtime terminal credential boundary is accepted. Rotation now uses the
typed `terminal.credential.rotate` operation: the password crosses only the
broker stdin protocol and fixed `chpasswd` stdin, never argv, staging files,
errors, or audit fields. The broker independently resolves the fixed terminal
service's `User=`, requires the request to match it, rejects root, validates
the root-owned executable, and leaves restart to `service.restart`. The last
two manager `runSystemd` calls and the generic wrapper were deleted. Ubuntu
24.04 passed the service-user dry-run, command-injection, `/etc/shadow`, audit,
and service-activation preservation gate; real rotation/login remains for the
final disposable fresh-install matrix. Evidence:
`docs/baselines/privilege-hardening-phase3-terminal-credential-2026-08-13.json`.

## Test-node and network constraints

Access records are outside Git:

- integration node LOS TESTE2:
  `D:\Users\jaime\.secrets\infos_los-test2.txt`;
- general BRLN VPS inventory/access:
  `D:\Users\jaime\.secrets\infos_vps_brln.txt`.

Never copy their contents into a commit, test output, evidence JSON, PR
comment, shell transcript, or this file. Use only the record required for the
specific authorized target; do not treat the general VPS inventory as
permission to mutate another host.

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
- keep the completed brokered LND upgrade gate intact: five-signature minimum,
  exact pinned primary-key/subkey linkage, pre-extraction archive hash, fixed
  source/architecture, and zero direct manager privilege calls. Apply the same
  gate to every installer variant;
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
