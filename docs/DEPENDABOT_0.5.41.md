# Dependabot maintenance for 0.5.41

Date: 2026-09-29. Status: implementation and local regression checks completed;
release validation remains open. This is not approval to publish or deploy.

## Branches and prerequisite

`agent/0.5.41-release` starts at the Go preparation candidate from
[PR #211](https://github.com/jvxis/brln-os-light/pull/211), commit `fb658932`,
plus the `0.5.41-Beta` version change. `agent/0.5.41-dependabot` contains the
dependency changes for review against that release branch. The original working
directory and its uncommitted files were preserved.

The prerequisite is still a draft: synchronize the final 0.5.40 release before
releasing 0.5.41. Existing installations must receive the 0.5.40 updater first;
new installations use its shared Go preparer. The legacy release-discovery gate
and the old-updater transition remain release blockers. This change does not
merge PR #211 or publish a GitHub release.

## Selected versions

| Component | Before | After |
| --- | --- | --- |
| Go requirement / preferred toolchain | 1.24 / 1.24.12 | 1.26.0 / 1.26.8 |
| chi | 5.0.10 | 5.3.0 |
| pgx | 5.5.5 | 5.9.2 |
| gRPC | 1.70.0 | 1.83.2 |
| x/crypto | 0.30.0 | 0.56.0 |
| x/net | 0.32.0 | 0.58.0 |
| Vite | 5.4.21 | 6.4.3 |
| esbuild | 0.21.5 | 0.25.12 |
| Browserslist | 4.28.1 | 4.29.3 |
| baseline-browser-mapping | 2.10.0 | 2.11.26 |
| postcss-selector-parser | 6.1.2 | 6.1.4 |

Related Go modules are resolved in `go.mod`/`go.sum`; the existing d3-color and
lodash overrides remain. React, Tailwind and their major versions are unchanged.
The Linux preparer already pins Go 1.26.8 and official amd64/arm64 checksums.

The candidate versions were checked against GitHub advisories, the Go module
proxy, npm metadata and the Go vulnerability database. gRPC 1.83.2 includes the
fix for its 1.83 line; 1.83.1 would not cover every advisory. The first scan also
identified newer chi and x/crypto advisories, so their final versions are above
the earlier planning candidates. x/crypto 0.56.0 requires Go 1.26.0.

## Regression found and fixed

Raising the Go directive to 1.26 activates strict colon validation in `net/url`.
Existing tests then failed for `http://127.0.0.1:8332:8332`: normalization returned
`http:8332`, and PeerSwap configuration became `http://http:8332`.

The parser now repairs only an identical valid repeated port in the authority
before normal URL validation. It preserves IPv6, path/query content and ordinary
URLs. No global GODEBUG compatibility switch disables the new URL validation.
The existing failures and new path/IPv6 cases were reproduced before the fix;
the focused suite passed afterward. Tests also preserve different ports and
out-of-range ports rather than treating them as this known legacy format.

## Validation performed

Platform: Windows/amd64, Node 24.13.1, Microsoft Edge headless via Playwright.
Initial backend baseline used installed Go 1.26.0 with the old module directive;
scanner, database comparison and final backend checks used Go 1.26.8.

| Check | Baseline | Final result |
| --- | --- | --- |
| `go test ./...` | 3,025 test/subtest pass events, 45 skips, no failures | 3,057 pass events, 25 skips, no failures; disposable database tests enabled |
| PostgreSQL integration comparison | 33 test/subtest pass events, no skips, Go 1.26.8 with old dependencies | Passed in the full final suite with new dependencies |
| `go vet ./...` | Not separately captured before changes | Passed |
| `go mod verify` | Not separately captured before changes | Passed |
| Linux amd64/arm64 `go build ./cmd/...` | Not separately captured before changes | Passed; cross-compilation only |
| `npm ci`, `npm run build` | Passed | Passed; existing large-bundle warning remains |
| `node --test tests/*.test.mjs` | 8 passed | 8 passed |
| `npm audit --json` (including development dependencies) | 5 vulnerable package entries, representing multiple advisories | Zero vulnerabilities |
| Browser production-build smoke | Passed against Vite 5 build | Passed against Vite 6 build |
| Vite development dependency fixture | Not separately captured before changes | HMR without reload, HTTPS API proxy, Recharts, ReactFlow zoom, QR worker encode/decode and i18n passed |

The PostgreSQL 16.15 fixture came from EDB's Windows binary distribution, ran
only on 127.0.0.1:55441 in a separate temporary data directory and used dedicated
test databases. Covered reports repair/history/marks/concurrency, AutoFee
exposure persistence and database-failure recovery, mesh correlation/replay/
idempotence/restart, and OP_RETURN persistence with a mocked wallet. No node
database or mainnet wallet was used. Baseline and final database suites ran
sequentially. The fixture is stopped after validation.

The browser test exercises the real built SPA with simulated API responses:
login rejection and success, notification display, Keysend filtering, English
and Portuguese, fonts and a mobile viewport. It checks uncaught page errors.
This does not exercise real authentication or real node API integration.

Run the retained browser check after a build and starting a localhost preview:

```powershell
# Install Playwright separately from the application's dependency graph.
npm.cmd install --prefix <temporary-tools-directory> playwright
$env:NODE_PATH = '<temporary-tools-directory>/node_modules'
$env:LOS_BROWSER_CHANNEL = 'msedge'
$env:LOS_UI_TEST_URL = 'http://127.0.0.1:5178'
node tests/browser-smoke.cjs
```

Use `npm run preview -- --host 127.0.0.1 --port 5178 --strictPort` in the UI
directory. The default browser channel is Edge; choose an installed Playwright
channel on other platforms. Test credentials and responses are synthetic.

`tests/dev-dependencies-smoke.cjs` exercises an isolated development fixture,
including the d3/ReactFlow/Recharts combination and the QR worker. It uses the
project's Vite configuration, overriding only the API target to a local
disposable HTTPS server with the same proxy options. Run from the UI directory
with the same `NODE_PATH` and browser settings, plus:

```powershell
# Use a new temporary directory; these keys belong only to the test fixture.
openssl req -x509 -newkey rsa:2048 -sha256 -days 1 -nodes -subj '/CN=localhost' -keyout <temporary-directory>/fixture.key -out <temporary-directory>/fixture.crt
$env:LOS_TLS_FIXTURE_DIR = '<temporary-directory>'
node tests/dev-dependencies-smoke.cjs
```

The runner binds localhost port 5179, creates two temporary `.qa-dependency.*`
files in the UI directory and removes them in `finally`; it refuses to overwrite
existing files. Its fixture servers and browser are also closed in `finally`.

## Security scan and remaining advisory

`govulncheck` v1.8.0, database timestamp 2026-09-28T16:43:40Z, source/symbol
analysis with Go 1.26.8 and `GOOS=linux`, was run for amd64 and arm64. The amd64
baseline reported reachable symbols for GO-2026-5004, GO-2026-5970,
GO-2026-6061 and GO-2026-6348. Final scans reported no vulnerable packages or
reachable symbols, but retained one **module-level** advisory:

- [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932): unmaintained
  `golang.org/x/crypto/openpgp`, no patched version. x/crypto is needed by LOS;
  openpgp is not imported by the application command graph. This advisory was
  not dismissed or hidden. Therefore the result is not described as a clean
  module-level scan.

The 36 open Dependabot alerts have patched selected versions in the candidate
graph. Their GitHub state remains open until integration into the default branch
and GitHub's rescan. No alerts were manually dismissed.

## Required before release

- Complete PR #211's disposable Linux installation and upgrade validation:
  first `install.sh`, first `install_existing.sh`, native arm64
  `install_existing_pi.sh`, old updater to 0.5.40 to 0.5.41, failure/retry/rollback
  and the enforced 0.5.40 discovery gate.
- Synchronize all final 0.5.40 fixes into the release branch.
- Run native Linux backend checks, permissions/systemd/broker tests and native
  arm64 validation. The 25 skipped Windows tests are not passed tests (including
  the intentionally bridge-only test and the opt-in regtest fixture).
- Validate the SPA served by the Go manager, real TLS/macaroon LND streams and
  reconnection, and the full affected UI workflows in the disposable lab.
- Observe automation stability and verify application rollback. No operational
  node was used as a dependency-update regression fixture.
- Confirm every tracked alert closes after an authorized merge and rescan.

Keep the dependency PR in draft until these critical gaps are resolved.

## Sources

- [Dependabot alerts](https://github.com/jvxis/brln-os-light/security/dependabot)
- [gRPC 1.83.2](https://github.com/grpc/grpc-go/releases/tag/v1.83.2)
- [pgx 5.9.2](https://github.com/jackc/pgx/releases/tag/v5.9.2)
- [Vite 6.4.3](https://github.com/vitejs/vite/releases/tag/v6.4.3)
- [Go downloads](https://go.dev/dl/)
- [PostgreSQL binary distributions](https://www.enterprisedb.com/download-postgresql-binaries)

## Per-alert disposition

All rows: patched version selected; pending default-branch merge and rescan.

| Alert | Package | Advisory | Severity | Selected |
| --- | --- | --- | --- | --- |
| [#1](https://github.com/jvxis/brln-os-light/security/dependabot/1) | golang.org/x/crypto | GHSA-v778-237x-gjrc | critical | 0.56.0 |
| [#2](https://github.com/jvxis/brln-os-light/security/dependabot/2) | golang.org/x/net | GHSA-qxp5-gwg8-xv66 | medium | 0.58.0 |
| [#3](https://github.com/jvxis/brln-os-light/security/dependabot/3) | golang.org/x/crypto | GHSA-hcg3-q754-cr77 | high | 0.56.0 |
| [#4](https://github.com/jvxis/brln-os-light/security/dependabot/4) | golang.org/x/net | GHSA-vvgc-356p-c3xw | medium | 0.58.0 |
| [#5](https://github.com/jvxis/brln-os-light/security/dependabot/5) | github.com/go-chi/chi/v5 | GHSA-vrw8-fxc6-2r93 | medium | 5.3.0 |
| [#6](https://github.com/jvxis/brln-os-light/security/dependabot/6) | golang.org/x/crypto | GHSA-j5w8-q4qc-rx2x | medium | 0.56.0 |
| [#7](https://github.com/jvxis/brln-os-light/security/dependabot/7) | golang.org/x/crypto | GHSA-f6x5-jh6r-wrfv | medium | 0.56.0 |
| [#8](https://github.com/jvxis/brln-os-light/security/dependabot/8) | google.golang.org/grpc | GHSA-p77j-4mvh-x3m3 | critical | 1.83.2 |
| [#9](https://github.com/jvxis/brln-os-light/security/dependabot/9) | github.com/jackc/pgx/v5 | GHSA-9jj7-4m8r-rfcm | critical | 5.9.2 |
| [#10](https://github.com/jvxis/brln-os-light/security/dependabot/10) | github.com/jackc/pgx/v5 | GHSA-j88v-2chj-qfwx | low | 5.9.2 |
| [#11](https://github.com/jvxis/brln-os-light/security/dependabot/11) | github.com/jackc/pgx/v5 | GHSA-xgrm-4fwx-7qm8 | critical | 5.9.2 |
| [#12](https://github.com/jvxis/brln-os-light/security/dependabot/12) | golang.org/x/net | GHSA-5cv4-jp36-h3mw | medium | 0.58.0 |
| [#13](https://github.com/jvxis/brln-os-light/security/dependabot/13) | golang.org/x/crypto | GHSA-q4h4-gmj2-qvw2 | high | 0.56.0 |
| [#14](https://github.com/jvxis/brln-os-light/security/dependabot/14) | golang.org/x/crypto | GHSA-45gg-vh54-h5m9 | medium | 0.56.0 |
| [#15](https://github.com/jvxis/brln-os-light/security/dependabot/15) | golang.org/x/crypto | GHSA-78mq-xcr3-xm33 | medium | 0.56.0 |
| [#16](https://github.com/jvxis/brln-os-light/security/dependabot/16) | golang.org/x/crypto | GHSA-qpw4-5x99-6vjp | medium | 0.56.0 |
| [#17](https://github.com/jvxis/brln-os-light/security/dependabot/17) | golang.org/x/crypto | GHSA-vgwf-h737-ff37 | critical | 0.56.0 |
| [#18](https://github.com/jvxis/brln-os-light/security/dependabot/18) | golang.org/x/crypto | GHSA-w879-237q-wc7r | high | 0.56.0 |
| [#19](https://github.com/jvxis/brln-os-light/security/dependabot/19) | golang.org/x/crypto | GHSA-89gr-r52h-f8rx | critical | 0.56.0 |
| [#20](https://github.com/jvxis/brln-os-light/security/dependabot/20) | golang.org/x/crypto | GHSA-rm3j-f69w-wqmq | critical | 0.56.0 |
| [#21](https://github.com/jvxis/brln-os-light/security/dependabot/21) | golang.org/x/crypto | GHSA-5cgq-3rg8-m6cv | critical | 0.56.0 |
| [#22](https://github.com/jvxis/brln-os-light/security/dependabot/22) | golang.org/x/crypto | GHSA-x527-x647-q7gg | critical | 0.56.0 |
| [#23](https://github.com/jvxis/brln-os-light/security/dependabot/23) | golang.org/x/crypto | GHSA-jppx-rxg9-jmrx | critical | 0.56.0 |
| [#24](https://github.com/jvxis/brln-os-light/security/dependabot/24) | golang.org/x/crypto | GHSA-f5wc-c3c7-36mc | critical | 0.56.0 |
| [#25](https://github.com/jvxis/brln-os-light/security/dependabot/25) | golang.org/x/crypto | GHSA-9m57-25v3-79x9 | medium | 0.56.0 |
| [#26](https://github.com/jvxis/brln-os-light/security/dependabot/26) | google.golang.org/grpc | GHSA-hrxh-6v49-42gf | high | 1.83.2 |
| [#27](https://github.com/jvxis/brln-os-light/security/dependabot/27) | google.golang.org/grpc | GHSA-vp52-pcj8-j9qc | high | 1.83.2 |
| [#28](https://github.com/jvxis/brln-os-light/security/dependabot/28) | google.golang.org/grpc | GHSA-qc2q-p7wx-3px3 | medium | 1.83.2 |
| [#29](https://github.com/jvxis/brln-os-light/security/dependabot/29) | google.golang.org/grpc | GHSA-2v4p-qf9q-27wj | high | 1.83.2 |
| [#30](https://github.com/jvxis/brln-os-light/security/dependabot/30) | esbuild | GHSA-67mh-4wv8-2f99 | medium | 0.25.12 |
| [#31](https://github.com/jvxis/brln-os-light/security/dependabot/31) | vite | GHSA-4w7w-66w2-5vf9 | medium | 6.4.3 |
| [#32](https://github.com/jvxis/brln-os-light/security/dependabot/32) | vite | GHSA-fx2h-pf6j-xcff | high | 6.4.3 |
| [#33](https://github.com/jvxis/brln-os-light/security/dependabot/33) | vite | GHSA-v6wh-96g9-6wx3 | medium | 6.4.3 |
| [#34](https://github.com/jvxis/brln-os-light/security/dependabot/34) | postcss-selector-parser | GHSA-w9m9-85wc-3x92 | low | 6.1.4 |
| [#35](https://github.com/jvxis/brln-os-light/security/dependabot/35) | browserslist | GHSA-73wf-gq98-2v4g | high | 4.29.3 |
| [#37](https://github.com/jvxis/brln-os-light/security/dependabot/37) | baseline-browser-mapping | GHSA-w5vr-8v7q-w6rv | medium | 2.11.26 |
