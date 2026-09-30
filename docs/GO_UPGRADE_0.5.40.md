# Go preparation for the 0.5.40 bridge release

The internal LOS upgrade prepares the target release's Go compiler before
building manager, broker and mesh. Existing installations do not need to run
`install.sh` or `install_existing.sh` to receive this prerequisite. The three
first-install entry points share the same preparation code and version policy.

## Release policy and build behavior

`lightningos-light/scripts/go-toolchain.conf` pins Go 1.26.8 and the official
Linux amd64/arm64 archive checksums, checked against
[Go's published downloads](https://go.dev/dl/) on 2026-09-28. This supported
release line provides the compiler needed for the planned dependency fixes.
The policy is parsed as data; shell expressions, unknown/duplicate keys and
incomplete records are rejected. A target `go.mod` requiring a newer compiler
than its pinned policy is rejected before downloading or changing the app.

The updater authenticates the release tag/commit/version before loading the
preparation code from its root-controlled worktree. Broker authorization of the
embedded upgrade helper still uses its fixed SHA-256 digest. Preparation runs
before the legacy identity migration and application builds. `--verify-only`
exits before preparation and does not install Go.

Archives come from `https://go.dev/dl/`, pass pinned SHA-256 verification before
extraction, and are staged on the toolchain store's filesystem. Version,
architecture, ownership, permissions and symlinks are checked before an atomic
rename publishes the directory. A file lock serializes simultaneous preparation.
An invalid existing managed tree is rejected rather than executed or replaced.

The selected compiler lives at
`/opt/lightningos/toolchains/go1.26.8.linux-amd64/bin/go` (or `linux-arm64`).
The preparation function sets `GO_BIN`, `GOROOT` and PATH for the calling
installer/updater. `GOTOOLCHAIN=local`, `GOENV=off` and `GOWORK=off` keep builds
on that compiler instead of silently downloading another toolchain or reading
per-user Go/workspace defaults. Explicit process-level proxy configuration still
applies. The directory is available for subsequent builds; global PATH and
`/usr/local/go` are not modified.

## Failure and rollback

Download, checksum, archive, architecture and permission failures abort before
application publication. Staging directories are removed on failure. Previously
prepared versions and system Go installations are preserved.

If compilation or a later application healthcheck fails, the existing
application rollback remains responsible for manager/UI/broker state. There is
no global compiler switch to undo: a successfully prepared compiler can remain
cached for retry, and existing binaries run independently of the installed Go
compiler. This change does not migrate PostgreSQL, update LND or change module
dependencies.

## First transition and mandatory bridge

Keep `go.mod` at Go 1.24 and `toolchain go1.24.12` in 0.5.40. Raising either as
part of the bridge would make reaching it depend on implicit toolchain download
in the old updater. Fresh installations of 0.5.40 use the pinned Go 1.26.8;
upgrades into 0.5.40 are still compiled by the already installed helper. The new
preparation runs when that installed 0.5.40 later upgrades to a newer release.

The intended release rule is: installations below 0.5.40 must first receive
0.5.40; installations at or above it can receive later releases. This PR delivers
the Go preparation, not retroactive enforcement in old clients. Old binaries
query GitHub releases directly and cannot acquire a new target-selection rule
just from release notes or metadata they do not read. Do not publish the Go
requirement increase in 0.5.41 until discovery/enforcement for those clients and
the full old-version -> 0.5.40 -> 0.5.41 path have been validated.

## Validation

The original [Dependabot review plan](DEPENDABOT_REVIEW_PLAN_2026-09-28.md)
records the initial analysis and the 0.5.40/0.5.41 delivery split. Its dated
checklists are historical; the execution results and current exceptions below
take precedence when assessing this PR.

Run `go test ./...` and `go vet ./...` from `lightningos-light/`. On Linux,
`TestGoToolchainPreparationFixtures` executes the real preparation functions
with local fake archives and a substituted downloader. The same fixture is
runnable without Go, root or access to node data:

```bash
bash lightningos-light/scripts/tests/go-toolchain-test.sh
```

It covers strict policy parsing, numeric version comparison, target module
requirements, download failures, checksum rejection, wrong architecture,
symlinks, unsafe permissions, concurrent installs, reuse, invalid caches and
staging cleanup. It writes only to its temporary directory. Windows Go tests
skip this fixture because it requires Linux ownership, permissions and flock.

Release validation must additionally exercise the root entry point, amd64 and
arm64 installers, and actual UI upgrade/retry/rollback on disposable nodes. A
shell fixture or successful cross-compilation alone does not establish that
end-to-end upgrade behavior.

Validation performed for this PR on 2026-09-28:

- `go test ./...` and `go vet ./...` passed on Windows/amd64 with Go 1.26.0;
  Linux-specific tests remain subject to their normal platform skips.
- All three commands cross-compiled for Linux/amd64 and Linux/arm64.
- `npm ci` and `npm run build` passed with Node 24.13.1; existing Browserslist
  freshness and bundle-size warnings remain.
- The standalone shell fixtures passed on Ubuntu 24.04/amd64 (LOS-TEST2),
  unprivileged and confined to temporary directories.
- The real official Go 1.26.8/amd64 archive was downloaded, checked, prepared
  and reused in a temporary directory; that compiler built and ran a small
  standard-library smoke program. No installed compiler or application was
  replaced. Manager, broker socket and LND remained active.

Those initial checks did not exercise privileged installation or the installed
old-updater transition. Subsequent disposable VirtualBox validation on
2026-09-30 exercised real systemd installation/recovery, the installed 0.5.39
Manager's authenticated API upgrade/failure/retry, and a following upgrade
requiring Go 1.26. See the [integration report](baselines/go-upgrade-0.5.40-virtualbox-2026-09-30.md)
for exact baselines, transport fixtures, harness corrections and remaining
release gates. A second pristine Ubuntu/amd64 clone passed its first complete
`install.sh` invocation and independent permission/authentication/broker/database
checks without manual repair. Browser login/dashboard checks passed on the disposable node and
LOS-TEST2; confirmation/cancel/error rendering passed with explicitly scoped
browser fixtures. Native arm64 execution was explicitly waived by the owner on
2026-09-30 for this delivery; it is not a passed test. Initialized-wallet
convergence and a successful upgrade observed in the browser remain pending.
The owner explicitly requested opening PR #211 for review on 2026-09-30. This
changes the draft status only; the remaining validation/release gates are not
waived and must be addressed before merge/release.

### Existing-node installer parity review — 2026-09-30

Compared both installer changes against `agent/0.5.40-release`. All added lines
in `install_existing.sh` and `install_existing_pi.sh` are identical. Their Go
library-loading block, `install_go`, `ensure_go` and Manager cutover orchestration
match. Both use the shared preparer and preserve the system Go. The preparer
selects `linux-arm64` and its separate pinned checksum for `aarch64`/`arm64`.
Pi-specific architecture checks, GoTTY artifact and PostgreSQL SSD handling remain
intact. No installer code change was needed for this review.

On the disposable Ubuntu/amd64 VM with Go 1.26.8, `bash -n` passed for both
installers and the shared preparer. The 23 selected top-level regression tests
passed, including both installers' first-install/existing-state/failure cutover
fixtures, shared Go preparation and authenticated artifact handling:

```bash
go test -p 2 ./internal/server -run 'Test(Installer|Installers|ExistingInstaller|ExistingInstallers|InstallAndUpgrade|ManagedInstaller|GoToolchainPreparation|BridgeRelease|AppUpgradePreparesGo)' -count=1 -v
```

Installer/preparer hashes matched the reviewed checkout; the policy content
matched after Windows CRLF normalization. These checks establish parity for the
changes in this PR and exercise Linux fixtures; they do not claim native ARM
installation or upgrade execution. No further RPi4 access was made for this review.
