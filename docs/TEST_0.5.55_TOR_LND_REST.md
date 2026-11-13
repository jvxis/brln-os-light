# 0.5.55 — optional LND REST onion (#407)

## Scope and security

### Collapsible setup and invoice credential (2026-11-13)

The subcard starts collapsed, showing configuration state (not an assertion of
external reachability). Expanding and refreshing perform no mutations. Separate
explicit actions create the onion and generate an invoice macaroon using the
existing `/api/lnops/macaroon/bake` invoice_permissions preset and its existing
reauthentication/CSRF protections. No broker/API permission expansion was added.
The UI sends no custom permissions or external-permission override. It displays
base64 and the root key ID; credentials/passwords are not persisted in browser
storage and are cleared when collapsed/unmounted. Clearing does not revoke a
credential and regeneration does not revoke predecessors; the UI says so.
Nothing is automatically sent to pay.br-ln.com or registered there.

Baseline browser suite passed before this UI change after installing missing
local npm dependencies. Updated real Edge browser fixture passes EN/PT, desktop
and mobile, collapse/expand, no implicit bake, confirmation/password gates,
exact preset-only request, auth error/retry, base64 copy, memory clearing,
manual onion reuse and REST unavailable. Browser test uses synthetic credentials,
not node secrets. Focused Go Macaroon/TorLNDREST tests and UI build pass.
No real credential was generated on LOS-TEST2 for this UI test: the operator
will exercise that step. Existing native macaroon backend behavior is reused.

Deployed the integrated UI on LOS-TEST2 and, at the owner's request, removed
only the exact managed REST block from torrc, with config verification and
root-only rollback backup at `/var/backups/lightningos/rest-reset-20261113`.
Tor was reloaded (not restarted); LND/Tor process identities and LND config/TLS
hashes were preserved. Onion identity files were retained, so recreation can
reuse the address. Initial reset harness stopped before any writes because
Tor's empty authorized_clients directory was not a regular file; the harness
was corrected to explicitly validate that directory rather than traverse it.
Post-reset API reports absent with local REST reachable. Manager/broker/LND
are healthy, LND chain/graph synced, two active channels; LNbits and Hardwallet
remain running. No macaroon was baked or revoked. UI rollback directory:
`/opt/lightningos/ui.rest-card-before`. PR remains draft for the previously
documented Tor 0.4.9.16 connectivity gap; this UI change does not waive it.

The dashboard Tor card can explicitly create and copy a REST onion. Nothing is
created by opening the dashboard. The broker operation adds port 8080 to a
kernel-reported listener owned by the native LND MainPID, or reuses a matching
manual service. Loopback is preferred; legacy Docker-only REST is accepted only
when its private address belongs to an active local Docker bridge. It does not
create macaroons, change LND configuration, regenerate TLS, or restart LND.
Tor reloads its configuration without a process restart.

The API requires confirmation plus the existing LND configuration reauthentication
scope when login is enabled. Authentication and CSRF middleware remain unchanged.
The broker takes no caller-selected path, port or command. Public hostname reads
are bounded and reject symlinks. Includes, quoted paths and unsupported/ambiguous
syntax require manual review rather than speculative rewriting.

The broker systemd template gains only `ReadWritePaths=-/etc/tor`, needed for
atomic torrc replacement under `ProtectSystem=full`; the Manager gains no access.
Missing Tor does not prevent broker startup. The candidate configuration is
checked by Tor before replacement. Reload failures restore the original torrc.
Atomic writes use root:debian-tor 0640: Tor must still read the file after
dropping privileges. A legacy root:root 0600 file can load at startup yet fail
on SIGHUP; systemctl's successful signal delivery does not prove reload success.
Removal was optional in the issue and is deliberately outside this first version.

The onion exposes the REST API, not an invoice-only proxy. Any valid macaroon
retains its permissions; only a dedicated invoice macaroon should be shared with
an invoice service. Existing TLS remains authoritative. The onion name may not
match that certificate, so clients need an explicit secure trust configuration.
The feature never turns off TLS validation. Configured status is not a promise
of public Tor reachability. `rest_reachable` is a local TCP-listener check.

## Validation — 2026-11-12

Baseline: existing uncommitted draft on `agent/0.5.55-lnd-rest-onion`, based on
`c91277c0`. Focused Go Tor/LND REST tests passed before changes. The Linux sandbox
review found the missing torrc write permission before publication.

- Windows amd64: `go test ./...` and `go vet ./...` passed. The final rerun used
  `GOMAXPROCS=2`, `GOMEMLIMIT=512MiB`, `-p 1` after an unrestricted Linux test
  cross-compilation exhausted Windows memory; the limited retry passed.
- UI: `npm ci --ignore-scripts`, `npm run build` passed (existing chunk-size
  warning). `node tests/tor-lnd-rest.browser.test.mjs` passed using the real
  component/API client with synthetic local HTTP responses: PT/EN, desktop/mobile,
  confirmation required, fixed POST body, copy URL, manual service display,
  REST-down gating, and refresh without mutations. No production credentials.
- Ubuntu 24.04 amd64 disposable regtest lab: native Tor 0.4.9.11 and LND 0.21.8.
  `TestTorLNDRESTLive` passed directly and under systemd with the actual broker
  template's security properties. Covered create, repeat with identical onion
  and torrc, manual-service reuse, injected reload failure/rollback, and real Tor
  rejection of an invalid staged candidate before replacing the live file.
- Process identities and activation timestamps of all three LNDs, Bitcoin and
  Tor remained unchanged. TLS cert/key hashes also remained unchanged (not logged).
  Original torrc restored; generated test onion directories retained as evidence.
- Lab baseline LND RPCs were available on regtest but `synced_to_chain=false`
  at height 138 before testing; no channel/payment or sync claim is made here.
- The optional network test (`LOS_TOR_REST_NETWORK=1`) timed out accessing the
  onion through SOCKS5 (90-second run, then a 181-second retry), despite Tor
  reporting bootstrap 100%. An independent SOCKS probe completed the greeting
  but timed out connecting to the onion, before TLS. Local REST TLS
  validated against Alice's public certificate and rejected a no-macaroon call
  as expected. This is an unresolved integration failure, not a passed test or
  evidence that the network is merely slow. See the later integration results below.

## Integrated candidate follow-up — 2026-11-12

- Assembled PRs #395–#405 and #408 in a separate
  local validation branch; #403/#396 retain their #395 dependency. No release
  or main/release-branch merge was performed. The App Store merge preserves
  both the LNDg spending warning and LNbits extension/2FA warning.
- Full candidate: Windows amd64 `go test -p 2 ./...`, `go vet -p 2 ./...`,
  `npm ci --ignore-scripts`, `npm run build` passed. PT/EN desktop/mobile Tor
  and LNbits/LNDg browser fixtures passed. One browser harness run hung and was
  stopped; bounded CDP timeouts were added in the integration branch.
- Ubuntu 24 amd64 disposable: `TestLNbitsNativeDockerMigration` passed with
  LNbits 1.7.1, native regtest LND 0.21.8, all three nodes synchronized. Covers
  clean-data install, no default extensions, real 2FA enrollment/login/recovery,
  running/stopped migration, failed stop, interrupted bake/retry, and real
  invoice/subscription/payment/tracking using only regtest coins. Native LND,
  Bitcoin and Tor identities and TLS hashes remained unchanged.
- Tor network validation under the actual broker sandbox passed in 41.68s:
  SOCKS5 → onion → REST with verified TLS, no macaroon, expected authentication
  rejection. A bounded publication window replaced six immediate retries.
  Earlier failures remain recorded; this does not guarantee Tor availability.
- LOS-TEST2 exposed a root:root 0600 torrc: SIGHUP returned success while Tor
  logged permission denied and closed its Control listener. Corrected to
  root:debian-tor 0640 and reloaded (no daemon restart). The Control listener
  returned. Added a real root-only-file regression, including a read check as
  debian-tor. The subsequent network rerun timed out after 201.64s; permission
  recovery alone is not recorded as an external-connectivity pass. A separate
  fresh-identity disposable retry also timed out (201.87s); the previous test
  keys were retained as evidence. An independent SOCKS client on LOS-TEST2 also
  timed out connecting to the disposable onion. Tor reported bootstrap 100%,
  network liveness up and six built circuits. Root cause remains unresolved;
  do not dismiss these failures solely as publication delay.
- The integrated Manager, broker, UI and broker unit were deployed to LOS-TEST2
  with retained rollback artifacts. This is a candidate deployment, **not** an
  old-Manager UI upgrade-path test. LND remains on its existing 0.21.4 runtime;
  the installer pin does not automatically upgrade a running LND.
- Authenticated API → socket-activated broker correctly detected LOS-TEST2's
  existing 172.17.0.1:8080 listener as local Docker, created the onion, and the
  onion reached LND using its verified existing TLS certificate. A no-macaroon
  request was rejected. LNbits HTTP 200 and its running container were confirmed.
  LND/Tor PIDs, LND configuration and TLS were preserved; Friendspool was untouched.

## Reproducing the privileged test

### Additional disposable network isolation (2026-11-12)

- On `los-disposable-0555-lndg`, a fresh boot reported synchronized NTP, low
  load, and successful HTTPS through Tor SOCKS (HTTP 200). Native Tor's
  authenticated Control interface confirmed the same systemd PID and loaded
  `HiddenServicePort 8080 127.0.0.1:8080`. HS_DESC events reported descriptor
  creation/uploads, while client directory requests/streams timed out.
- The broker-sandbox network run failed after 201.22s. A separate opt-in
  `TestTorLNDRESTSinglePublication` performs one ensure without preceding fault
  injection; it also timed out (385.89s). It remains a failed observation, not
  a substitute pass or a reason to weaken the regression suite.
- Independent baseline: a new ephemeral onion created directly through Tor
  Control `ADD_ONION NEW:ED25519-V3 Flags=DiscardPK`, without any LOS operation,
  also timed out in four SOCKS attempts (201s). No private key was requested,
  identity logged, or persistent configuration changed. The ephemeral service
  was removed when its controller disconnected. This demonstrates that the
  failure also occurs outside LOS on this fixture; it does not prove all Tor
  environments work or establish the underlying Tor/network cause.
- Local full regression rerun passed: create/retry, root-only torrc repair,
  rollback, operator-service reuse and invalid candidate rejection. LND,
  Bitcoin and Tor process identities and all LND TLS hashes remained unchanged.
- Windows amd64 `go test -p 2 ./...` and `go vet -p 2 ./...` passed again.

### Fresh-install harness finding (2026-11-12)

The first full `install.sh` run on a new Ubuntu 24.04.3 amd64 clone used an
incorrect harness `umask 077`. The installer built the Manager directory and
binary as root:root 0700; systemd's `lightningos` user failed with 203/EXEC.
`namei`, `stat`, a user execute check and mount options identified the exact
permission failure (not noexec). The complete `install_manager()` function is
byte-for-byte identical to released 0.5.54; this permission sensitivity predates
this PR. It is not counted as a passed fresh-install test or silently ignored.

The harness was corrected to the normal 022 mask. Only the two identified,
root-owned, non-symlink Manager paths were normalized to 0755 on that disposable,
and the broker self-test then passed. The original failed log/exit record were
retained before a complete installer retry. This repaired/repeated scenario is
distinct from a clean first-install result; a separate clean rerun is pending.

The repaired installer retry completed with exit 0 and the installed broker
self-test passed. A separate owner-authorized `los-disposable-0555-tor-fresh-clean`
clone then started a genuinely clean first installation with umask 022, Ubuntu
24.04.3 amd64, 6 GiB and 4 vCPUs. Baseline contained neither LOS nor LND. It
installed the prerequisites, PostgreSQL and the signed LND 0.21.8 archive and
reached Manager compilation. At that point SSH authentication timed out and
the guest console reported CPU soft lockups; VirtualBox also reported missed
guest heartbeats. Windows had approximately 1.3--1.5 GiB available RAM.
This is an incomplete run, not a successful installer result or a demonstrated
LOS regression. No REST-onion API mutation had run on this clone.

Only one disposable was running. The owner separately authorized normal shutdown
of LOS-TEST2 to free resources; its Manager, broker socket and LND were active
before shutdown. It must be started and checked again after validation. The
unrelated `brln-e2e-tempo-real` VM was not stopped without separate authorization.
Normal ACPI shutdown of the stalled clean clone was requested; disks and logs
remain intact. Existing-install and old-updater gates remain outstanding.

After normal shutdown failed, the owner explicitly authorized power-off of that
exact stalled clone and normal shutdown of `brln-e2e-tempo-real`. With both
non-disposable VMs off, the clone resumed at 4 GiB/2 vCPUs and the installer at
GOMAXPROCS=1/GOMEMLIMIT=1GiB. Windows available RAM recovered to about 12 GiB.
The original interrupted log was retained separately; the full install.sh retry
completed with exit 0, native-origin marker, active Manager/PostgreSQL/broker
socket and successful broker self-test. This covers first LOS provisioning on
an initially empty host plus interrupted-install recovery, not an uninterrupted
one-shot run. No installer/product code was changed to obtain this result.

After isolated regtest wallet initialization, the installed authenticated API
through the real socket-activated broker passed wrong-password/no-write,
create and identical-repeat checks. Native LND/Tor process identities, lnd.conf
and TLS cert/key were unchanged. Health and LND status APIs responded HTTP 200.
An initial harness assertion incorrectly matched commented HiddenServiceDir
examples in the distro torrc; inspection found two comments, zero active
directives and no generated service directory. It was replaced by exact before/
after byte comparison plus the absence of the managed directory, and rerun.
The fresh fixture uses Tor 0.4.9.16 from the installer repository; NTP was synced
and Tor reported bootstrap 100%. Network forwarding is checked separately.

The clean fixture's six-attempt onion network run timed out. A subsequent
instrumented rerun confirmed timeouts in SOCKS connection establishment, before
TLS (three failures observed, then deliberately stopped with normal VM shutdown
to continue the remaining installer matrix). An independent client-only Tor
instance with its own data/guards eventually bootstrapped to 100% but its bounded
probe also failed; its first attempts occurred before bootstrap completed and
are not treated as valid service tests. The native client successfully fetched
the Tor Project's public blog onion (HTTP 200, 8.13s) and clearnet HTTPS through
SOCKS (HTTP 200, 2.43s). The public control hostname came from
https://onion.torproject.org/. This narrows the observation but does not resolve
the new-service publication/connectivity failure. Local config/API success is
not being relabeled as network success. The clean clone was shut down normally,
preserving its logs, generated test service and independent-client data.

The separate Ubuntu 24.04.3 amd64 existing-node fixture completed the full
`install_existing.sh` with exit 0 (4 GiB/2 vCPUs). Before LOS installation it
had native LND 0.21.8, a zero-peer/zero-fund Bitcoin mainnet fixture and distro
Tor 0.4.9.11. Original LND/Bitcoin configuration, LND TLS cert/key and all three
native process identities were preserved; Manager, PostgreSQL and broker socket
were active and broker self-test passed. Through the installed authenticated
Manager and real broker, wrong-password/no-write, create and identical retry
passed without LND/Tor restart or LND config/TLS changes. The actual onion
network probe passed in 18 seconds: SOCKS5, verified native TLS, and an HTTP 500
response rejecting the absent macaroon. This does not erase the separate
fresh-fixture network failure with Tor 0.4.9.16. The existing fixture was shut
down normally and its evidence retained before moving to the old-updater test.

On `los-disposable-0555-lndg`, the original tagged 0.5.54 installer completed
successfully. The authenticated original Manager then offered the full candidate
through a private loopback HTTPS catalog; only that disposable trusted its test
CA/Git redirect, with no public release/tag. The original Manager/broker binaries
were hashed and unchanged before starting. The generated helper matched the
old embedded source byte-for-byte (SHA256
`5dfce2b88b9d67e7cb9d4b854111d783752513c8d4590cf88ecd052bb3348397`).
The first attempt correctly refused the harness's manually selected regtest
macaroon path before cutover: production credential configuration accepts fixed
mainnet paths, not that lab adaptation. Original installed binaries remained
unchanged. Its journal was retained. The fixture was corrected to a separate
empty mainnet chain with zero peers/funds/channels (regtest data preserved),
without changing product code, and the actual old Manager API retried.

That retry completed at 2026-11-12 23:58:31 UTC with successful systemd exit,
installed version 0.5.55-Beta, active services, successful broker self-test and
the new `ReadWritePaths=-/etc/tor` unit directive. Native LND/Tor identities and
LND config/TLS hashes matched the pre-upgrade baseline. Post-upgrade real API
wrong-password/no-write, create and identical retry also passed. The reused
fixture retained a test onion directory from earlier runs; a harness assertion
requiring its absence was corrected to exact before/after directory metadata
and content hashes, retaining the no-write invariant. Network validation and
restoration of the private catalog follow separately.

The post-upgrade fixture ran Tor 0.4.9.16. Six 30-second-circuit attempts failed
before TLS. A new ephemeral onion created directly via authenticated Tor Control
(`ADD_ONION`, without LOS/broker/torrc changes) also failed two 90-second-circuit
attempts; closing its owning Control connection removed it. In that same run,
the official Tor Project public onion returned HTTP 200 in 15.29s and ordinary
HTTPS through Tor returned HTTP 200 in 2.15s. This independently reproduces a
new-onion connectivity failure outside LOS, but does not establish its precise
network/upstream cause. Temporary catalog hosts/Git redirects/trust CA were
restored and the catalog service stopped. Failed network runs remain failures.

On resuming the fresh fixture, the retained managed onion still failed four
90-second-circuit attempts after clock synchronization and Tor bootstrap 100%.
No Tor-related AppArmor denial appeared in the boot journal. Effective settings
included PublishHidServDescriptors=1 and DisableNetwork=0. An isolated comparison
then used an APT-authenticated Ubuntu Tor 0.4.9.11 binary, extracted without
installing/downgrading any package, and a separate unprivileged process/data/onion
on SOCKS 19050. It reached the same native LND with verified TLS and missing-
macaroon rejection in 14s. Cross-client probes: the separate 0.4.9.11 client
could not reach the native 0.4.9.16 service (90s timeout), whereas the native
0.4.9.16 client reached the separate 0.4.9.11 service in 8s.

Keeping that separate process's configuration, data, onion and user, changing
only its executable to the installed upstream 0.4.9.16 binary resulted in two
90-second-circuit failures. Returning to the 0.4.9.11 binary passed again in 16s.
Both comparison executables were outside the native AppArmor profile; native
Tor/config/package were untouched. This A/B/A result reproduces a package/runtime
dependent service failure without LOS, but live network/time and package build
differences prevent claiming the exact upstream defect is proven. It is not a
recommendation to downgrade Tor, and is not a passing native 0.4.9.16 result.
All comparison processes were transient; the VM was shut down normally with
data/evidence preserved. The native production-layout fixture added to the
reused regtest VM was disabled for future boots to avoid competing with its
original enabled lab services; the original lab data were retained.

Only on an explicitly authorized disposable regtest host with native Tor, Alice
REST listening on 127.0.0.1:8080, and `/etc/los-regtest/LAB_ONLY` containing
`LOS_REGTEST_ONLY`. Never enable this test on a production/funds-bearing node.

```sh
# Build from lightningos-light; run on the matching Linux amd64 fixture.
GOOS=linux GOARCH=amd64 go test -c -o tor-rest.test ./internal/privileged
sudo env LOS_TOR_REST_LIVE=1 ./tor-rest.test -test.v -test.run TestTorLNDRESTLive -test.timeout 300s
```

The test refuses a fixture whose torrc already contains a REST onion, restores
the original file on completion, and retains test onion directories/keys locally.
The sandbox run uses a root-owned test binary outside `/home` and the Service
security properties from `templates/systemd/lightningos-privileged@.service`.
To also exercise Tor network forwarding, add `LOS_TOR_REST_NETWORK=1`; it uses
the fixture's public TLS certificate and expected loopback SAN, without turning
off certificate verification or sending a macaroon. This is not a test of an
external Lightning Address provider's TLS configuration.

## Remaining release gates

Keep the PR draft for the native Tor 0.4.9.16 service-connectivity failure above.
The amd64 installer/old-updater matrix now has real results: full install.sh on
an initially empty host with interrupted-install recovery (not an uninterrupted
one-shot pass), full first install_existing.sh preserving native state, and
actual original 0.5.54 Manager/broker/helper upgrade to the full candidate.
Installed authenticated API/broker checks passed in all three scenarios.
Network access passed on LOS-TEST2 and the distro-Tor existing fixture; failures
on the two upstream-Tor fixtures remain explicitly unpassed. Resolving that
dependency/network condition or changing release scope requires a separate
decision; no package downgrade or security-policy relaxation was shipped.
The owner explicitly waived ARM on 2026-11-12; ARM is not recorded as tested.
Only one disposable ran at a time, with disks/evidence retained after shutdown.
No funds-bearing node was used for payment, credential-revocation or failure
injection fixtures; those tests remained isolated on disposable regtest.
