# 0.5.47: first installation from the modern release catalog

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

## Required gates before readiness

Keep the PR **draft** until these real installation paths are validated, or the
maintainer explicitly accepts and documents a narrower validation scope:

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
