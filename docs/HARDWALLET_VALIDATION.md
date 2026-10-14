# BRLN Hardwallet: experimental validation

PR: https://github.com/jvxis/brln-os-light/pull/260, targeting `agent/0.5.45-release`.
Companion firmware/coordinator: https://github.com/jvxis/brln-hardwallet, branch
`brln/tdisplay-los`. This is not a release or approval to use real funds.

## Open button from LAN (2026-10-14)

Reproduced the reported failure in Chrome by logging into the Manager via its
LAN address and clicking the actual Store Open link: it appended port 4449 to
the LAN host and the new tab failed with `ERR_CONNECTION_REFUSED`. Opening from
the Tailscale IP worked. Prior direct-navigation tests missed this workflow.

Broker status now returns the saved bind IP, and the catalog returns a validated
Manager launcher URL on that host. The Store never synthesizes a LAN app URL.
The launcher passes through normal LOS authentication, verifies the running app
and configured origin, then opens same-host HTTPS port 4449. No cookies/tokens
cross hostnames or appear in URLs; app authentication and network binding remain
unchanged. A stopped app or changed origin shows a recovery action.

The configured-address regression failed before the fix and passes afterward.
Windows amd64 `go test -p 1 ./...`, `go vet -p 1 ./...` and UI `npm run build`
passed. Nine native Ubuntu amd64 root fixture tests passed, including the new
status regression. LOS-TEST2 received backed-up Manager/broker/UI candidates;
only Manager restarted. LND retained PID 3891 and chain/graph sync; Bitcoin
remained remote, local Electrs stopped and Hardwallet running on the club source.
No container, VM or production service was restarted.

`scripts/hardwallet-open-smoke.cjs` uses Playwright and a password on stdin to
exercise actual popup navigation. With normal CA validation, both flows passed:
LAN login -> Open -> login on the app host -> coordinator, and tailnet login ->
Open -> coordinator using the existing session. Secure context/Web Serial,
authenticated status, anonymous 401 and zero JS errors were verified. A mocked
stopped-app status showed the recovery message without navigation; the real app
was not stopped. No USB connection or firmware write was performed.

The smoke script requires `LOS_MANAGER_URLS` (comma-separated HTTPS Manager
origins), installed `playwright` or `PLAYWRIGHT_MODULE`, and optionally
`CHROME_PATH`. Supply the password through stdin from private local metadata;
never place it in a command argument or environment variable. On Windows with
the LOS CA trusted by the OS, use Node's `--use-system-ca`.

Rollback: `/var/backups/lightningos-hardwallet-pr260/before-open-fix` for binaries
and `/opt/lightningos/ui-before-open-fix` for UI. Existing physical-signer,
native-arm64 and certificate lifecycle validation gaps remain; PR stays draft.

## Public club Electrum correction (2026-10-14)

The original pilot incorrectly made the owner's personal tailnet a requirement
for querying the club Electrs. The current catalog instead selects the public
`electrum.br-ln.com:50001` endpoint with explicit `tcp` transport. Members do not
join the club/owner tailnet. Their own node's authenticated HTTPS/Tailscale app
access remains a separate connection. Source selection and lack of automatic
fallback are unchanged; local Electrs remains the default.

The owner confirmed the `electrum.br-ln.com` hostname through the Nginx config.
`electrs.br-ln.com` did not resolve. Public port 50001 answered `server.features`
with the mainnet genesis and electrs/0.10.8, from both the workstation and
LOS-TEST2. The VM routed this connection over `enp0s3`, not `tailscale0`.
The supplied Nginx `location` uses an HTTP proxy to the raw Electrum port; it
does not translate HTTP/WebSocket to Electrum TCP. A raw Electrum request over
verified TLS on 443 received HTTP 400; the HTTPS GET timed out, as did TLS on
50002. The usable public endpoint is therefore TCP without TLS. Store/app copy
states this and the query privacy implications. No VPS configuration/service
was changed. Public Electrum TLS would need an actual TCP/TLS endpoint.

Regression tests reproduced both defects before the fix: the backend rejected
explicit `tcp`, and the catalog used a private tailnet IP. They pass with the
correction. Tests retain local transport, mainnet/genesis/response-ID validation,
and TLS certificate verification with no TCP fallback after an untrusted cert.
Historical runtime evidence below describes the earlier tailnet-based pilot;
it does not prescribe the current club connection.

Companion commit `03e9cc3346badb10b79c2276cffa4c4d326e390a`, CI
[38603324950](https://github.com/jvxis/brln-hardwallet/actions/runs/38603324950),
passed firmware/native/Python checks, web tests/check/build and Go tests/vet.
The catalog image digest is
`sha256:df60a615c010f69c5017a9653c8f76b4836d067ed0285c8acb973572628ff57a`.
Public OCI index/layers and all four bundled firmware hashes were verified.
On Windows, backend tests/vet, 74 web tests and Svelte check passed. The local
web build hit WebAssembly memory allocation failure; the CI Linux production
build passed. LOS full Go tests/vet and UI build passed, followed by catalog
tests for the final image pin and eight native Linux root tests.

LOS-TEST2 manager/broker/UI were backed up and updated; only the manager was
restarted. The App Store's Apply server action returned 200. Runtime container
inspection confirmed the public DNS endpoint, `tcp` transport, `club` source,
exact image and no local Electrs network attachment. Authenticated status/fees
returned 200/mainnet; anonymous routes returned 401, invalid Origin returned
403 and logout revoked access. The six firmware resources denied anonymous
access; authenticated binaries matched the new CI artifacts byte-for-byte.
Chrome showed the public-server option, completed the Apply flow and loaded
the coordinator over verified HTTPS without JS errors or horizontal overflow.
LND retained PID 3891 and reported chain/graph synced; Bitcoin remained remote,
local Electrs stopped, and the production VPS was untouched.

Rollback artifacts: manager/broker in
`/var/backups/lightningos-hardwallet-pr260/before-public-electrum-03e9cc3`, UI in
`/opt/lightningos/ui-before-public-electrum-03e9cc3`. This correction does not
resolve the physical signer, native arm64 or long-running TLS/lifecycle gates.

## Software checks (2026-10-14)

The baseline server, broker, system and catalog tests passed before development.
After the implementation, Windows amd64 checks passed:

```text
cd lightningos-light
go test -p 1 ./...
go vet -p 1 ./...
cd ui
npm run build
```

Go build concurrency was limited after an initial cross-build exhausted Windows
commit memory. The bounded build succeeded. This was not a passing result for
the failed build attempt.

Eight Hardwallet tests also passed as root on Ubuntu 24.04 amd64 using temporary
fixtures. They cover real Linux ownership and permissions, manifest tampering,
dry-run behavior, closed broker inputs, bridge firewall scope, certificate trust,
changed bind address, expiry, failed source-stop preservation and remaining
Electrs network attachments. The changed-address test failed before the fix.

The companion CI builds original ESP32 T-Display firmware and amd64/arm64 OCI
images. A Linux/Windows build is not physical signer validation; an arm64 image
build is not native arm64 runtime validation.

## LOS-TEST2 runtime evidence

### Integrated USB installer (2026-10-14 follow-up)

The companion app now includes initial firmware installation for the original
ESP32 T-Display (1.14-inch display, two buttons, 4 MB flash). Firmware is built
first in the same CI run, packaged with its source commit and SHA-256 hashes,
then included in the immutable app image. The installer and its Espressif stub
are served locally; no CDN or external installer site is required.

The browser requests the USB port on the member's computer. No new broker
operation, host USB mount or privilege is added. Firmware routes retain the
existing LOS session authentication. Local/club Electrum choice is unchanged.

Before writing, the member confirms the physical board model and that there is
no wallet to preserve, and the installer identifies an original ESP32 with 4 MB
flash. Identification only uploads a stub into RAM. A separate button authorizes
flash writes. A chip cannot establish the display model or absence of every
possible wallet format; the UI explicitly explains that limitation. The
existing-wallet update option has no installation action.

Only the bootloader, partition table, boot_app0 and application ranges are
written. Full erase, NVS/SPIFFS writes, eFuse writes, arbitrary binaries/URLs and
automatic retries are absent. Downloaded bytes undergo SHA-256 verification;
stub MD5 verifies written ranges before reset. These are integrity checks, not
an independent firmware signature or an audit of source/build provenance.

Completed on Windows amd64:

- Companion: 74 Vitest tests passed with one worker; Svelte check reported zero
  errors/warnings and production build passed. Four new Python packaging tests
  plus the two existing packaging tests passed. New tests cover wrong chip/flash,
  modified/truncated files, fixed write ranges, failure to verify, no automatic
  retry, and rejection of modified partition maps before packaging.
- Chrome against the local production build: first-install acknowledgement,
  update blocking, cancelled USB selector releasing the coordinator, corrupted
  file blocking, unsupported-browser blocking, same-origin-only requests, no JS
  errors and no horizontal overflow at 390 px. The USB selector was simulated;
  no real port was opened or written.
- Initial unconstrained local test/build attempts failed due to Windows commit
  memory pressure. Repeating tests with one worker and bounded Node memory
  passed; failed attempts are not counted as passing runs.

CI [38599494043](https://github.com/jvxis/brln-hardwallet/actions/runs/38599494043)
passed the native C++/cryptography and Python tests, compiled the ESP32 firmware,
passed web tests/check/build and Go tests/vet, and published the amd64/arm64 image.
The new catalog pin is companion commit `194a1ecf7b419e159c385be6902ec475408c5814`,
image digest `sha256:4ed6d102c06e6a29637c32ac915fac4f8084c6b573725405d1cc41379026b41b`.

The public OCI index and every downloaded layer digest were verified without
registry credentials, and both amd64/arm64 platforms were present. All four
packaged firmware files matched the CI artifact. Chrome checks also passed
against the static files extracted from that exact published image, including
loading the bundled esptool module and releasing a simulated failed serial port.

The LOS-TEST2 VM entered VirtualBox `GURU_MEDITATION` with
`VERR_NEM_GET_REGISTERS_FAILED` before this installer image was deployed. The
owner explicitly authorized a forced power-off and headless restart of only
LOS-TEST2. No disks were removed or restored. No causal conclusion about the
VM failure is established by the concurrent host memory pressure. The manager
waited for LND during boot, then services recovered normally. That authorized
VM recovery, unlike the app deployment, restarted the guest's services.

After recovery, the prior manager/broker hashes were verified before replacement
and backed up in
`/var/backups/lightningos-hardwallet-pr260/before-installer-194a1ec`.
Windows `go test -p 1 ./...` and `go vet -p 1 ./...` passed after the catalog pin
change; all eight Linux root Hardwallet tests and the broker self-test passed.
The candidate manager/broker were installed and only the manager was restarted.
The existing authenticated app install operation then returned HTTP 200 and
started the exact new image with club Electrs selected.

Actual authenticated HTTPS/browser checks on LOS-TEST2 passed:

- All six firmware resources (manifest, four binaries, checksum list) returned
  401 without a LOS session. Authenticated binaries exactly matched the CI
  artifact and manifest SHA-256. Logout revoked firmware access.
- The initial-install acknowledgement and existing-wallet update block worked.
  Simulated USB-selector cancellation and port-open failure released the
  coordinator controls. A truncated firmware download and an unsupported
  browser both blocked installation. No real USB port was opened.
- The esptool module loaded from the app, with no external requests or JS page
  errors. Desktop/mobile layouts had no horizontal overflow at 390 px.
- Authenticated Electrum status/fees returned 200 for club/mainnet; anonymous
  coordinator routes returned 401, invalid-Origin broadcast returned 403 and
  logout revoked access. No valid transaction was broadcast.
- Docker inspection confirmed the new pinned image, unchanged non-root UIDs,
  read-only root filesystems, dropped capabilities, no app host port, proxy
  bound only to the Tailscale IP, and no club-mode Electrs network attachment.
  WireGuard interfaces and the post-recovery LND PID remained unchanged during
  deployment. Bitcoin source remained remote and local Electrs remained stopped.
  Final LND status reported both chain and graph synchronization complete.

Physical USB/reset/readback/boot and subsequent signer pairing remain untested.

### Earlier app lifecycle validation

Baseline was LOS `0.5.44-Beta`, Ubuntu 24.04 amd64, Bitcoin source `remote`,
LND synced to chain and graph, and installed local Electrs stopped. No Bitcoin
or Electrs indexing was started for this test. Existing WireGuard interfaces,
LND state, chain storage and unrelated app containers were preserved.

The owner authorized Tailscale installation and completed its login. The VM
queried the club Electrs through Tailscale and verified the Bitcoin mainnet
genesis (`electrs/0.10.8`). DNS and advertised subnet route acceptance were
disabled for this lab connection.

Candidate manager, broker and UI were deployed with backups under
`/var/backups/lightningos-hardwallet-pr260`. Only the manager was restarted;
the socket-activated broker uses the candidate executable for new requests.
The broker self-test passed. The manager TLS leaf was reissued with its original
SANs plus the lab Tailscale addresses, retaining the existing CA and private key.
No production VPS service was changed or restarted.

Observed through the actual manager/broker and browser:

- App installation with club source returned HTTP 200; the coordinator and
  proxy ran with the catalog's restricted users and mounts.
- All app routes tested without a LOS session returned 401, including `/`
  and `/healthz`. Authenticated HTML, status and fee requests returned 200.
- Status identified club source and mainnet; height and fees came from Electrs.
- A broadcast request with an invalid Origin returned 403 before processing.
  No valid transaction was broadcast.
- Logout revoked app access (401).
- Chrome loaded the app with normal certificate validation, a secure context
  and Web Serial available. No JavaScript page errors or horizontal overflow
  were observed at desktop and 390-pixel viewport widths.
- Stop returned 200 and subsequent status reported stopped. Start restored
  healthy containers; the manager logged HTTP 200 after 35 seconds, but one
  test client lost the connection before receiving that response. This remains
  a transport/retry validation gap, not an unqualified passed start test.
- During a high-load attempt to select local Electrs, LOS returned 503 with
  `app_node_busy`; subsequent status retained the previous club configuration.
- A later local-source install returned 200 and reported `electrum_source=local`.
  With local Electrs stopped, status returned 503 `electrum_unavailable`; it did
  not fall back to the club server. The App Store source selector and Apply
  button then switched back to club with HTTP 200, and the browser showed club
  source and working Electrum status again.
- The actual App Store card, source selection and HTTPS link were verified in
  Chrome. A layout issue under the progress badge was fixed and visually checked.
- Uninstall with the documented confirmation returned 200; status then reported
  uninstalled. Reinstallation with club source returned 200, followed by another
  successful authentication/status/fees/Origin/logout verification. The empty
  uninstall request was correctly rejected with 409.
- Final Docker inspection confirmed users `65527:65527` and `65532:65532`,
  non-privileged/read-only containers, all capabilities dropped, no host port
  for the coordinator, and proxy port 4449 bound only to the Tailscale IPv4.
  No Hardwallet container remained attached to `electrs_default` in club mode.
  Existing app containers stayed up and the original LND process remained
  synced. Both WireGuard interfaces retained their addresses.

The first runtime installation correctly failed image verification: the
coordinator's version flag wrote to stderr. The companion fix writes stdout and
has a subprocess regression test. The catalog pins a subsequent successful CI
artifact; see `internal/appmanifest/brln_hardwallet.go` for the exact digest.

## Remaining gates

- Complete repeated lifecycle/source-switch/reinstall tests, including local
  Electrs when it is available, and verify retry behavior after slow responses.
- Exercise the remaining lifecycle buttons in the App Store browser UI; source
  selection and Apply have been exercised.
- Verify the complete physical signer workflow: initialization, buttons,
  password/passphrase, backup, recovery on another board, full output review,
  rejection, disconnect handling and signing. Use laboratory material only.
- Validate native arm64 runtime and perform independent security review.
- The app renews its TLS leaf during ensure/install/start when trust, names or
  expiry require it; this is not a background renewal service. Long-running
  certificate renewal/reload and manager tailnet SAN provisioning still need
  release-level validation. The user's browser must trust the LOS CA and use
  the same host for LOS and the app.

Keep the PR in draft until the critical gates are met. Do not interpret the
presence of a public image or a successful build as a completed safety review.
