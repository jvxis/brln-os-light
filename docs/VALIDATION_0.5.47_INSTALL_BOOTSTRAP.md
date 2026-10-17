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
