# 0.5.47: first installation from the modern release catalog

## Final result and accepted scope

**Passed:** complete first installation on a clean Ubuntu 24.04 amd64 clone,
using the candidate bootstrap with license acceptance and the default target.
It selected the actual immutable Latest `0.5.46-Beta`, compiled/installed the
application, and passed browser, HTTPS, initial admin setup/login and real
Manager-to-broker checks. The installed LND binary is `0.21.4-beta`; its service
correctly awaits the first Bitcoin RPC/wallet wizard configuration.

The maintainer explicitly limited acceptance to new `install.sh` installations,
excluding existing-node and native ARM integration scenarios. The UI updater is
unchanged. Earlier plans and blocked attempts below are retained as history;
the complete-installation evidence and remaining configuration limits are at
the end of this document.

## Scope and preserved behavior

`lo_bootstrap.sh` now resolves GitHub Latest in `jvxis/brln-os-light-updates`.
Both discovery and tag metadata must identify a published immutable release,
not a draft/prerelease. JSON is parsed with Python 3 (now an explicit bootstrap
prerequisite); invalid metadata or network failure never selects the old catalog.

The source checkout remains `jvxis/brln-os-light`. Before invoking the installer,
its commit must match the immutable catalog's tag (annotated or lightweight),
and its source version must match the selected version. A missing/mismatched
tag or version stops before installer execution. Explicit development refs and
`LIGHTNINGOS_INSTALL_SOURCE=checkout` remain deliberate verification bypasses,
not the documented default. Custom catalogs/source repositories are rejected in
the default verified path.

The native, existing-amd64 and existing-arm64 entry points share this selection.
No installation body, UI updater, broker, systemd service, release publication
destination or old-client bridge logic changed. The original release catalog
must still end at 0.5.40. Do not rerun these installers to upgrade an installed LOS.

README corrections cover both languages: prerequisites, current version pins,
license acceptance in piped commands, new versus existing LOS installations,
development bypass limitations and removal of broad gRPC firewall workarounds.

## Baseline and automated validation — 2026-10-17

Baseline: `2d381fdc` (`agent/0.5.47-release`), clean before this work.

- Windows amd64: focused installer/bootstrap/catalog Go tests passed before edits.
- Behavioral regression against that baseline selected **0.5.40-Beta**, not the
  newer fixture release, for all three installer selections. The HTTP fixture
  recorded calls to the legacy catalog. The same test passes after the change.
- `scripts/tests/test_install_bootstrap.py` executes actual Bash/Git/bootstrap
  and the actual installer re-entry helper against isolated local Git fixtures.
  Fixture installers only record dispatch; they do not install software or
  operate services. Git Bash's Windows PATH/path conversion is handled explicitly
  by the test harness; this is not evidence of Linux system integration.
- Cases: three entry points and repeat execution; annotated/lightweight tags;
  malformed JSON, invalid tag, mutable/draft/prerelease metadata; network failure
  and retry without fallback; absent catalog tag; commit/version divergence;
  missing Python prerequisite; rejection of a branch masquerading as a tag;
  dirty checkout and unrelated/occupied targets;
  invalid installer, conflicting refs, custom catalog/source rejection and
  explicit development ref compatibility. Arguments and the re-entry guard are
  verified, as is exclusion of repository-only `.github` content.
- `go test -p 1 ./...` and `go vet -p 1 ./...` passed on Windows amd64 with
  `GOMAXPROCS=2 GOMEMLIMIT=1GiB`. Backend production code was not changed.
- Release-draft catalog/bridge regression tests passed (3 tests).
- Bash syntax validation passed. Diff whitespace check uses `cr-at-eol` for the
  pre-existing CR line-ending format in the Portuguese README.
- A broad Python discovery attempt cannot import the existing Linux-only
  `test_go_parent_recovery.py` on Windows (`pwd` is unavailable). That unchanged
  privileged Linux suite is not reported as passed or silently skipped.
- Live read-only catalog check: Latest is immutable `0.5.46-Beta`; both public
  repositories' tag peels resolve to `3a3b03d110a9fa37426cff92c451ebc8bfaeeb94`.
  This check did not run an installer or update a node.

Reproduce the isolated behavioral suite on Linux:

```bash
python3 -m unittest discover -s scripts/tests -p test_install_bootstrap.py -v
python3 -m unittest discover -s scripts/tests -p test_release_draft.py -v
bash -n lo_bootstrap.sh
```

For the expected-failure baseline regression, set
`BOOTSTRAP_BASELINE_REF=2d381fdc` and run the
`test_latest_dispatch_all_installers_and_repeat` test only. On Windows set
`BOOTSTRAP_TEST_BASH=C:/Program Files/Git/bin/bash.exe` and use `python`.

## Original validation plan

The initial plan kept the PR draft until the following paths were validated or
the maintainer accepted a narrower scope. That scope was subsequently narrowed
to first installation; see the final result and maintainer decision below:

- Fresh Ubuntu amd64 `install.sh` with this bootstrap selecting the actual
  published release: installed UI/Manager/broker versions and service health.
- First LOS install on a disposable existing native LND node, with service,
  configuration, permissions and data preservation checked.
- Native arm64 `install_existing_pi.sh`; dispatching its fixture on amd64 is not
  arm64 runtime validation.
- Actual old-client UI upgrade bridge/non-regression, separately from installer
  selection. A new helper against an old checkout is not this test.
- Privileged interrupted/retry/rollback and existing-state installation cases.

No production node, funded LND service or persistent Bitcoin data is a fixture.
No full Linux installation, real UI upgrade or ARM runtime result is claimed.

## Authorized Linux attempt — blocked before guest boot

On 2026-10-17 the maintainer authorized reuse and subsequent shutdown of
`los-disposable-0542-logs` (UUID `3619cb5d-2889-448e-94cd-1dbf8a4a181e`).
It was powered off, configured with 2 vCPU / 4 GiB RAM. Headless startup failed
in VirtualBox 7.2.14 before Ubuntu booted: `Unhandled error 1455` while allocating
the guest RAM (`pgmR3PhysInitAndLinkRamRange`, `VERR_UNRESOLVED_ERROR`).

Read-only Windows counters showed approximately 68.5 GiB committed against a
71.9 GiB commit limit (95%), leaving less than the requested guest RAM plus
overhead. This is a host memory-allocation blocker, not a bootstrap test failure.
No guest test or installer ran. The VM remains powered off; disks and logs are
preserved. Host paging/virtualization/network settings and other VMs were not
changed. The Linux gates above remain pending and the PR remains draft.

## Linux bootstrap retry — 2026-10-17

After the owner restarted the Windows Remote Desktop service to release excess
resources, the same authorized VM started successfully without changing its
2 vCPU / 4 GiB sizing or the host configuration. SSH used the previously trusted
host key and the recorded lab access helper; the guest MAC matched the VM.

Baseline: Ubuntu 24.04.3 amd64, kernel 7.0.0-38-generic, Python 3.12.3,
Git 2.43.0, approximately 3 GiB available memory and 84 GiB free disk. LND,
Bitcoin, Manager and broker socket were inactive. Docker was active with no
running containers. This is a reused lab guest, not a pristine-install fixture.

The committed bootstrap/test files from `31ddb9b5` were exported with Git archive
and executed natively in Linux:

- `python3 -m unittest discover -s scripts/tests -p test_install_bootstrap.py -v`:
  **16 passed**, including all three installer selections, installer re-entry,
  repeat execution, invalid/mismatched releases, missing prerequisites,
  network-failure/retry behavior and preservation of unrelated checkout state.
- `python3 -m unittest discover -s scripts/tests -p test_release_draft.py -v`:
  **3 passed**, preserving the legacy-catalog bridge publication boundary.
- `bash -n lo_bootstrap.sh`: **passed**.

A separate attempt used the unmodified candidate bootstrap and actual public
GitHub catalog (no network/Git fixtures). It deliberately set
`ACCEPT_MIT_LICENSE=0` to verify release resolution and checkout before allowing
any installer changes. GitHub returned HTTP 403 with
`x-ratelimit-remaining: 0` (60/60 unauthenticated requests used; reset advertised
for 2026-10-17 12:54:59 UTC). The bootstrap exited nonzero at catalog discovery,
without a legacy fallback, source clone or installer execution. Consequently,
live release attestation/checkout and full installation are **not** validated
by this attempt. No authentication bypass was added to the test.

Logs remain under `/var/lib/los0547-bootstrap-validation` in the disposable VM.
The guest was shut down after testing, as authorized, with disks and logs
preserved. No production/test node, host network or paging configuration was
changed. The PR remains draft with the installation/upgrade/ARM gates above
explicitly pending; these Linux bootstrap tests do not replace those gates.

## Public catalog retry after rate-limit reset — 2026-10-17

At 12:59 UTC the public catalog again returned HTTP 200. The same disposable VM
was started and its baseline rechecked. Ubuntu boot was slow (a guest clock
watchdog delay was observed), but SSH recovered without a reset or host changes.

The unmodified candidate bootstrap then completed the real public-network path:

- Selected `0.5.46-Beta` from the updates catalog, not the legacy `0.5.40` bridge.
- Verified immutable release metadata and the catalog tag.
- Cloned the official source repository and verified both its tag/HEAD and
  version against commit `3a3b03d110a9fa37426cff92c451ebc8bfaeeb94`.
- Invoked the actual released installer, which intentionally exited at its
  initial license gate because this bounded test set `ACCEPT_MIT_LICENSE=0`.
- Repeated the real-network selection with `install_existing.sh`,
  `install_existing_pi.sh`, and `install.sh` again. All reached that same gate,
  with no old-bootstrap recursion, a clean checkout and `.github` excluded.

This closes the public catalog/checkout/dispatch validation gap. It does **not**
claim a complete installation, native ARM execution, existing-node migration or
old-client UI upgrade. The critical installation gates remain pending and the PR
stays draft. The original HTTP 403 log was preserved separately from the new
`public-catalog-after-reset.log` and `public-install*.sh.log` evidence in
`/var/lib/los0547-bootstrap-validation`.

Postcheck matched the baseline: LND, Bitcoin, Manager and broker socket inactive;
Docker active with no running containers. The VM was shut down after testing;
disks and logs were preserved.

## Maintainer scope decision

The maintainer explicitly narrowed acceptance to a complete first LOS
installation through the default `install.sh` bootstrap. Native arm64 and
`install_existing.sh` integration scenarios are excluded from this PR's
acceptance scope; their automated selection/re-entry coverage remains recorded
above. The UI updater implementation is unchanged and its separate old-client
upgrade matrix is not part of this new-install bootstrap validation.

The earlier reused VM contained inactive LOS binaries and configuration from
previous tests. It therefore cannot establish first-install behavior. A new full
clone of the powered-off protected template was created for that purpose:
`los-disposable-0547-bootstrap-fresh`, UUID
`64455cd6-4749-4384-bfbe-f70e81e10988`, 2 vCPU / 4 GiB, Ubuntu 24.04 amd64,
kernel 6.14.0-37-generic. Baseline confirmed no LOS configuration/binaries,
no LND unit or wallet database, and no active Bitcoin or Docker service.

The template lacked curl. The first attempt correctly rejected the missing
prerequisite before cloning or installing application components; evidence was
preserved as `missing-curl.log`. The README prerequisite command then installed
`ca-certificates curl git python3`, and the same candidate bootstrap was retried
with `ACCEPT_MIT_LICENSE=1` and its default `/opt/brln-os-light` target. The
bootstrap SHA-256 matched the host checkout:
`a98f9468ca1f836bbe0d1fa264e8bac21470fc454341eb62572a54d09e6e1f5d`.

### Test harness correction

The first new clone's collector used systemd `UMask=0077` to protect its raw log,
but accidentally passed that setting to the installer. Its Manager directory
was consequently created as root-owned `0700`; that run is not accepted as the
documented default Ubuntu installation scenario. The VM and its evidence were
preserved and it was powered off. No application-source change or manual
permission repair was used to turn that run into a passing result.

The acceptance run instead uses another fresh full clone,
`los-disposable-0547-bootstrap-default`, UUID
`58dc436e-9329-4c63-91ac-a4201804d915`, same Ubuntu baseline and sizing. The
collector explicitly invokes the unchanged candidate bootstrap in a subshell
with the normal `umask 022`, while keeping its raw log file at `0600`. Baseline
again confirmed no LOS/LND/Bitcoin installation, wallet or running Docker.
README prerequisites are installed before invoking the bootstrap.

## Complete first-installation result — 2026-10-17

Acceptance VM: `los-disposable-0547-bootstrap-default`, UUID
`58dc436e-9329-4c63-91ac-a4201804d915`. Baseline and sizes are recorded above.
The candidate bootstrap was run with `ACCEPT_MIT_LICENSE=1`, normal `umask 022`,
the default target and no ref/catalog/source override. Its source matched the
committed bootstrap SHA-256 recorded above. The real public release selected
was `0.5.46-Beta`, commit `3a3b03d110a9fa37426cff92c451ebc8bfaeeb94`.

The unchanged released installer completed with **exit 0**, recorded at
2026-10-17 13:20:46 UTC. The Ubuntu unattended-upgrade apt lock was handled by
the installer itself. No application permission/configuration repair, package
verification bypass, prebuilt replacement binary or manual service restart was
used to make this acceptance run pass.

Observed installation and functional checks:

- PostgreSQL 18 provisioned and active; Tor, i2pd, Go and Node prerequisites
  installed through the normal installer path.
- LND `0.21.4-beta` authenticated with six pinned signers and archive SHA-256,
  then installed. Its service is enabled and inactive by design until the
  operator configures Bitcoin RPC and a wallet in the wizard. No wallet was
  created, seed generated, or Bitcoin/App Store container installed.
- Manager and broker/mesh built from the attested source; installed broker and
  mesh bytes match their freshly compiled `dist` binaries.
- Manager runs as `lightningos`, with `NRestarts=0`; the broker socket is active
  and listening. `runuser -u lightningos --
  /opt/lightningos/manager/lightningos-manager broker-self-test` passed against
  the actual installed privileged socket.
- Installed UI `version.txt` is `0.5.46-Beta` and agrees with the source. Runtime
  Manager/UI directories are root-owned `0755`, with the raw test log `0600`.
  Repository-only `.github` content is excluded.
- Verified HTTPS loaded the UI and both referenced JS/CSS assets. Headless Edge
  rendered the actual first-admin setup screen with two password inputs, the
  expected version, and zero JavaScript page errors. Browser test requests used
  Node HTTPS with the exact guest CA obtained over verified SSH; no Windows
  trust-store change or certificate-validation bypass was used. Screenshot:
  `D:/Cache/Temp/los0547-first-install-setup.png` (local test artifact).
- The admin setup endpoint accepted a locally issued one-time token and an
  ephemeral password held only in test-process memory. Authenticated App Store
  and wizard GETs returned HTTP 200; the wizard correctly reported no wallet.
  Logout, password login and logout again succeeded with session cookies/CSRF
  handled by the client. Tokens, password and cookies were not printed or added
  to the repository.

The initial API smoke request omitted `Origin` and was rejected with HTTP 403,
as required by the installed same-origin middleware. After adding the correct
origin to the test client, the full smoke passed; the rejected request did not
configure an admin password. Both smoke logs were preserved separately. The
first browser launch also found a missing bundled Chromium executable on the
Windows test host; using its installed Edge channel completed the browser run.
Neither issue required an application change.

Installer warnings were checked rather than treated as successful checks:

- The immediate listener check raced Manager startup. Subsequent HTTPS checks
  and the browser succeeded on 8443 without restarting Manager; `NRestarts=0`.
- UFW remains inactive in this isolated template, exactly as documented in
  both READMEs: installation does not enable it automatically. This run does
  not claim protected network exposure. Configure the documented LAN/VPN
  firewall before operating a real node.
- The health API returns HTTP 200 with overall `ERR` specifically for missing
  LND TLS and unreachable Bitcoin RPC before the wizard. These two issues were
  read back explicitly; a synchronized/configured Lightning node is not claimed.

Guest evidence: `/var/lib/los0547-bootstrap-validation/full-install.log`,
`full-install.exit`, `functional-smoke.log` and
`functional-smoke-with-origin.log`. The owner-requested first-install bootstrap
acceptance gate is complete; existing-node, native ARM and old-client upgrade
integration tests are not claimed. The lab VMs were shut down afterwards with
disks/logs preserved; the protected template, LOS-TEST2 and production nodes
were not changed.
