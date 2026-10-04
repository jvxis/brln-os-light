# 0.5.41 integrated LOS-TEST2 validation

Date: 2026-10-04. AMD64 release-readiness validation; no release published.

## Candidate and scope

Local integration branch: `agent/los-test2-0.5.41-integration`.
Tested source: `54c88fb5a744d7aceb48f56bf8c0c954a18e149e`.
Includes PRs #214, #221, #223, #224, #225 and #226, merged locally without
conflicts. No remote merge, source tag or release was created by this test.

LOS-TEST2: Ubuntu 24.04, amd64, original `install.sh` installation, currently
using remote Bitcoin. Baseline: official 0.5.40, source `eee45ff3`.
Electrs and Mempool were already stopped and remained stopped.

Native binaries were built and tested in a separate disposable Ubuntu 24.04
amd64 VM with Go 1.26.8. The UI was built on Windows. The exact candidate
Manager, privileged broker, Mesh binary and built UI were deployed together.
This was a **manual deployment, not a UI-upgrade transition test**. Previous
binaries, UI, build stamp and existing LNbits files/data have root-only backups
on the test node. Rollback was prepared but not executed.

## Automated checks

- Windows amd64: `go test -p 2 ./... -count=1`, `go vet -p 2 ./...`,
  `go mod verify`, `npm.cmd ci`, `npm.cmd run build`,
  `node --test tests/*.test.mjs` (8 tests), and `git diff --check` passed.
- Native Ubuntu amd64: `go test -p 2 ./... -count=1`, `go vet -p 2 ./...`,
  `go mod verify`, and builds of Manager, broker and Mesh passed. Opt-in
  database/regtest skips are not counted as passes.
- `TestRelease0540PostgresCompatibility` passed against a separate PostgreSQL
  18 fixture, not the node's database. The fixture was stopped afterwards.
- Existing large frontend bundle warning remains.
- A new full npm audit on this date reports five high-severity package entries
  involving braces/chokidar/micromatch/fast-glob/tailwindcss and
  GHSA-vfj7-8cjw-p6xm. `npm audit --omit=dev` reports zero vulnerabilities.
  This supersedes the earlier date-specific zero-full-audit result in #214;
  no forced dependency update or advisory dismissal was performed.

## Real node integration

- Manager and broker self-test passed; authenticated health is OK.
- LND remained unlocked and synced to chain/graph; remote Bitcoin RPC stayed
  available. LND PID, certificate, private key and configuration hashes were
  unchanged. No wallet sends, channel changes or rebalances were triggered.
- Go Manager serves the actual 0.5.41 SPA. Headless Edge authenticated using
  verified node TLS, opened Notifications, Rebalance Center, Lightning Ops and
  App Store without uncaught JavaScript errors.
- Expanded Lightning Tools and verified the rendered 15-minute recovery policy;
  `/api/lnops/channel/auto-heal` returns `recovery_wait_sec=900`.
  The previously unreachable peer remained a pre-existing condition. Recovery
  actions were not deliberately triggered on live channels.
- `/api/rebalance/overview` exposes the mature/to-date sell-through fields.
  This node currently returns zero for those samples; arithmetic, discovery
  minimum and parallel-peer guards were covered by automated tests, not a
  funds-moving end-to-end rebalance.
- App Store displays LNbits 1.6.2 and Community 0.1.57 as available versions.

## LNbits

- Before deployment, installing on 0.5.40 already returned `app_node_busy`.
  Early candidate attempts also timed out. Broker image preparation continued
  independently and completed with the exact reviewed 1.6.2 digest. Once the
  image was available, App Store API start, stop and start again all passed.
  Do not call the earlier timeout fixed: it remains a pre-existing operational
  observation. Docker image enumeration itself exceeded a 10-second diagnostic
  deadline; these observations alone do not establish a new release regression.
- Verified running image digest, non-root UID/GID 65532, read-only rootfs and
  host networking; `/api/v1/health` returned HTTP 200 after readiness.
- Executed the image's real `LndRestWallet.status()` and a read-only getinfo
  request with its dedicated macaroon and verified TLS. Both passed, including
  after stop/start. LND remained synced and was not restarted.
- Compared existing SQLite contents with the pre-test backup: one account and
  one wallet preserved exactly; zero existing payment/extension rows remained
  zero. This is not evidence for preservation of a populated payment history.
- The old persisted App Store version was 1.5.6. Starting 1.6.1 on the baseline
  did not complete; do not describe this node test as a running 1.6.1-to-1.6.2
  transition. The separate earlier FakeWallet fixture covers that image pair.
- LNbits stopped after testing, data retained.

## Community

- The old 0.1.54 instance was stopped successfully through the App Store API.
- The owner subsequently started Community for chat login during testing.
  Independently verified web and signer containers running at the exact pinned
  0.1.57 images, and proxy HTTPS returning 200 with certificate verification.
- Community and signer are intentionally left running for the owner. Signing,
  login-expiry and pairing flows were not simulated on the owner's identity.

## Additional disposable Linux release gates

Both guests: Ubuntu 24.04, native amd64, 4 GiB RAM, no initialized LND wallet
or channels. These are separate from LOS-TEST2. Existing OS prerequisites
(PostgreSQL 18, Node and Go) were present. Original fixture files were moved
to root-only backup directories, not deleted.

### Official old updater, browser, rollback and retry

Guest: `los-disposable-0540-upgrade`.

- Installed the actual official 0.5.40 source, peeled commit
  `eee45ff316382f7f1948b8a0c0dd988718bfa539`, with its own helper.
- A loopback HTTPS release fixture exposed the exact aggregate commit as
  0.5.41 in the modern catalog, with its corresponding tag/commit attestation.
  The legacy catalog continued to expose only official 0.5.40. SSH and TLS
  verification remained enabled; no public tag or catalog was changed.
- Used the actual 0.5.40 browser UI to authenticate, refresh the offered
  release and confirm the upgrade. Its real Manager/broker/updater fetched,
  verified, built and installed this aggregate, including Go 1.26.8.
- Browser-only substitution: `wizard/status.wallet_exists=true`, to enter
  the dashboard on an unfunded guest. Authentication, CSRF, release selection,
  upgrade start, build, logs, restart and completion were not mocked. LND RPC
  behavior is covered separately by the initialized LOS-TEST2 tests above.
- Upgrade completed with the expected candidate build stamp; Manager and
  broker self-test passed. System Go 1.24.12, LND configuration and PostgreSQL
  activation timestamp were preserved.
- Executed `lightningos-rollback-privilege-cutover`: Manager, broker, UI version,
  config and secrets matched the recorded pre-upgrade SHA256 values. The
  official 0.5.40 stamp was restored. Repeated rollback also passed.
- Repeated the complete upgrade from the restored official 0.5.40 through
  Chromium. Exactly one successful upgrade-start request was recorded. Manager
  restart invalidated the in-memory login session (expected existing behavior).
  After normal reauthentication, the pending-upgrade marker reopened the modal,
  it displayed completion, and reload served 0.5.41 without JS runtime errors.
  This validates resumption after login, **not uninterrupted session continuity**.
- The first browser harness incorrectly treated the expected 401 during Manager
  restart as an upgrade failure. The helper had completed successfully. The
  corrected harness checks the actual login/resumption behavior described above;
  no application code was changed to make that test pass.
- Negative source test: the official old helper with `--verify-only` rejected
  a mismatched tag/commit. Installed hashes and Manager activation timestamp
  remained unchanged.
- Temporary catalog service stopped, hosts file restored, temporary catalog CA
  removed from trust and Manager restarted to discard cached test trust.
  Verified public-catalog discovery and broker health afterward. Browser drivers
  stopped; logs and rollback evidence retained under `/root/los-0541-gates`.

### First install and existing-node installation

Guest: `los-disposable-0540-fresh`; exact aggregate checkout used with
`LIGHTNINGOS_INSTALL_SOURCE=checkout ACCEPT_MIT_LICENSE=1` and normal umask 022.
The checkout override is needed because 0.5.41 is not publicly released; this
does not test a public first-install bootstrap selecting unpublished source.

- `bash install.sh`: no LOS/LND application files at start. Installed native
  LND 0.21.4-beta, Manager, broker and UI. Manager and broker permissions passed;
  native binary reports Go 1.26.8. Auth setup and seven authenticated API routes
  returned 200 over verified TLS. Both notification PostgreSQL roles authenticated.
  LND startup remained deferred to wallet initialization, as expected.
- Retained that native LND, its configuration and database service, removed LOS
  from active paths into a recoverable fixture backup, then ran
  `bash install_existing.sh`. LND configuration and binaries matched recorded
  SHA256 values, configuration mode/owner were preserved, and PostgreSQL was not
  restarted. New LOS Manager/broker and auth/API checks passed.
- Two initial fixture errors were diagnosed and preserved in logs: an absent
  default `/data/bitcoin` directory, and `systemd-run` starting the root executor
  without HOME (Go could not resolve its module cache). Supplied an empty Bitcoin
  directory and normal root HOME/UID, then retried successfully. Installer source
  is unchanged from 0.5.40; these failures are not represented as product fixes.
- Ran the full `install_existing.sh` again, declining database reprovisioning.
  Config and secrets hashes stayed identical, LND files stayed identical and
  PostgreSQL activation timestamp was unchanged. Broker and authenticated API
  checks passed again. Evidence: `/root/los-0541-installs`.

The final 0.5.40 commit is an ancestor of this aggregate. None of the three
installer scripts or the embedded upgrade helper changed between that release
and this aggregate; the shared Go pin metadata did. Earlier bridge-specific
installation/legacy layout tests remain complementary evidence, not a claim
that every old layout was recreated in this run.

## Security advisory disposition

The five high npm entries are one newly reviewed root advisory propagated
through the build-tool graph:
[GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm),
braces stack exhaustion on deeply nested attacker-supplied patterns. At review
on 2026-10-04 there is no patched version. The affected braces/chokidar/
micromatch/fast-glob/Tailwind chain is development-only; `npm audit --omit=dev`
reports zero. Its versions and the trusted static Tailwind content globs are
unchanged from 0.5.40. No runtime request path to this dependency was identified.

Disposition: retain as an explicit build-environment advisory, not an identified
runtime release blocker. Do not feed untrusted glob patterns into the build;
track an upstream patch. No forced Tailwind major migration or advisory dismissal.
The earlier dated full-audit-zero statement must not be reused as current status.
The prior module-only Go openpgp advisory remains as documented in
`DEPENDABOT_0.5.41.md`; this run is not a new govulncheck scan.

## Remaining limitations and publication checks

- Native ARM execution was explicitly waived by the owner for this release
  validation. **Not run, not passed.** Do not claim native ARM validation.
- No mainnet funds were moved and no real channel was deliberately disrupted.
  Automated tests cover recovery/rebalance rules; live checks cover integration,
  configuration and data shape, not an end-to-end paid rebalance.
- LNbits populated payment/extension history and PostgreSQL backup/export were
  not exercised here. Existing account/wallet data and SQLite integrity/lifecycle
  were checked; separate old/new image smoke evidence is in PR #226.
- Full clean-OS prerequisite installation, every legacy layout, power loss at
  every cutover step and long-duration automation soak were not repeated here.
- No new blocking AMD64 regression was found in the exercised paths. This is
  bounded evidence, not a guarantee that every installation is failure-free.
- Merge must preserve this tested source tree (or rerun checks for differences).
  Refresh PR #214/#226 validation status with this evidence before release.
  After default-branch integration, verify the GitHub advisory rescan; alerts were
  not manually dismissed.
- Publish 0.5.41 **only** in `jvxis/brln-os-light-updates`, using the reviewed exact
  tag/commit and immutable release. Public checks on this date: legacy latest is
  immutable 0.5.40-Beta; modern catalog has no releases yet. Validate both public
  catalogs again after publication; local fixtures do not prove unpublished state.

Production nodes were not touched. Disposable VMs were not reset, deleted or
powered off; temporary catalog/browser drivers were stopped and evidence retained.

## Final node state

Manager reports 0.5.41-Beta from the candidate commit, health OK, broker
self-test passed, LND unlocked/synced, remote Bitcoin RPC OK. No app upgrade
job remains running; the public catalog still offers only 0.5.40.

BTCPay was restored via the App Store API after its temporary stop; server and
database containers are running and its HTTP endpoint returns the expected 302
login redirect. Community web/signer are running at 0.1.57. LNbits is stopped
after its successful 1.6.2 test, with data preserved. Electrs and Mempool remain
stopped. LND PID and TLS/config hashes still match the baseline.
