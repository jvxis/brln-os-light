# BRLN Hardwallet: experimental validation

PR: https://github.com/jvxis/brln-os-light/pull/260, targeting `agent/0.5.45-release`.
Companion firmware/coordinator: https://github.com/jvxis/brln-hardwallet, branch
`brln/tdisplay-los`. This is not a release or approval to use real funds.

## Physical T-Display and USB startup (2026-10-14 evening)

Companion commit `f60d68b46975d6890070b1f55d0c5b58f94fa4c0`,
[CI 38656838951](https://github.com/jvxis/brln-hardwallet/actions/runs/38656838951),
builds the revised BRLN branding and fixes two failures reproduced on a real
original T-Display: Chromium UART framing errors during ESP32 boot and a ping
sent before storage/RNG startup completed. The client recovers only bounded
boot framing errors before the first protocol command, waits for the actual
startup notice after reset, closes failed ports and invalidates sessions after
reboot. The 86 web tests and Svelte checks passed. Firmware native policy,
cryptography and packaging checks, ESP32 build and both OCI architectures passed.

The owner confirmed the original 1.14-inch/two-button board with 16 MB flash.
The installer now accepts original ESP32 with 4 or 16 MB, retaining the fixed
4 MB layout and binary headers; other capacities/chip families remain rejected.
CLI erase/write/readback of the earlier `01f5796` build passed on the explicitly
authorized disposable board. The owner physically formatted storage and checked
the firmware hash. Real Chrome pairing with the candidate app, live LOS backend
and that board succeeded after camera comparison of the displayed pairing code;
BRLN capabilities were verified. No wallet/seed was created and no funds used.

The BRLN firmware excludes the inherited mascot in boot and animation frames.
The display mark uses equal gaps around the bolt and larger main/status text.
App and legacy installer favicons use the BRLN mark. Legacy upstream firmware
is explicitly distinguished from the BRLN installer served by LOS.

Current immutable catalog digest:
`sha256:16f414010fbe5d93ca9e9dc69b870380d26edbca94e74a9f64efcd6f7f462fbb`.
Anonymous OCI index/layers and all four packaged firmware hashes were verified.
The new on-screen ESP image hash (distinct from the whole-file SHA-256) is
`cbfa386c46b8e5e9de2dec64878e40506582f480c1316ee80ef58ee84c68d695`.
New firmware approval remains a physical button action; no automated test may
bypass it. Local seed entry, recovery and transaction signing remain open gates.
Historical sections below describe earlier builds, not the current catalog pin.

Final runtime validation: full LOS Go tests/vet and Linux amd64 builds passed.
Eleven privileged fixtures and broker self-test passed on LOS-TEST2. Manager and
Hardwallet were updated after backup at
`/var/backups/lightningos-hardwallet-pr260/before-usb-branding-f60d68b`.
Same-host LAN Store Open passed in real Chrome without JS errors. All six
firmware resources denied anonymous access; four authenticated binaries matched
CI byte-for-byte, and logout revoked access. Public Electrum status/fees,
invalid-Origin rejection, private address bindings and preserved proxy identity
passed. LND remained PID 3891 and unrelated app containers were unchanged.

The deployed app (no static-asset override) paired with the real disposable
board; camera comparison confirmed its code and BRLN capabilities were accepted.
The integrated installer then identified 16 MB and completed all four writes,
readback verification and reset through real Chrome Web Serial. Only the USB
identity mapped to the authorized COM5 was selected. Initial native chooser
automation attempts failed before selection; positioning the dedicated test
Chrome window on-screen fixed the harness. No device-selection guard was relaxed.
A bounded camera capture of the next boot confirmed the centered BRLN logo,
larger subtitle and matching on-screen firmware hash. The board is now waiting
for the owner's physical approval of that hash. No seed was created, no wallet
recovery/signing was claimed, and the final firmware was not paired past that
approval screen. No production VPS change or VM reboot was performed.


## UI wording correction (2026-10-14)

Removed the incorrect references to a "club Tailscale/tailnet" from the app,
Store description and current API/development documentation. The Store access
copy now only explains same-address LOS access, trusted HTTPS, login and USB.
The app describes the public Electrum endpoint without referring to a club VPN.
Earlier sections below retain historical validation context, not current copy.

Companion commit `01f579604bdf0c93ceaefa3fddcde445548ef522`, CI
[38610174376](https://github.com/jvxis/brln-hardwallet/actions/runs/38610174376),
passed and published amd64/arm64 images. Current catalog digest:
`sha256:82620e09493040c41033fa93e7a2827f1c52c5c217a47a58f4ffffad3dd0d946`.
Anonymous OCI index/layer digests and bundled firmware hashes were verified.
LOS Go tests/vet and UI build passed, followed by focused registry/catalog tests
for the new image pin. This changes copy and image packaging, not network or
authentication behavior.

After eleven Linux root fixtures passed, LOS-TEST2 received backed-up binaries,
UI and the new pinned app image. Actual Portuguese Store and coordinator copy
were inspected in Chrome; neither contains Tailscale/tailnet text. Open retains
the LAN host/session and the browser reports no JS errors. Six firmware assets
still reject anonymous requests; four authenticated binaries match the new CI
artifact and logout revokes access. LND stayed on PID 3891, chain/graph synced;
Bitcoin remained remote and unrelated app containers stayed running. No physical
USB operation was performed. Rollback binaries/app snapshot are in
`/var/backups/lightningos-hardwallet-pr260/before-copy-01f5796`, prior UI in
`/opt/lightningos/ui-before-copy-01f5796`.

## LAN and optional VPN access (2026-10-14)

The current implementation supersedes the tailnet-only listener and cross-host
launcher described in the historical sections below. Tailscale is optional.
Broker discovery uses active private IPv4 host addresses (RFC1918 and CGNAT),
excluding loopback/down interfaces and Docker `docker0`, `br-*` and `veth*`.
Public, wildcard and IPv6 listeners are not created. The Store uses the relative
`/#hardwallet-open` launcher and preserves the current Manager host/session.
HTTPS, LOS authentication and per-origin broadcast checks remain mandatory.

The app's separate CA-signed leaf includes all bind addresses and Manager SANs.
Ensure/start migrates old `bind_ip` settings to `bind_ips`, preserving source and
proxy identity. Source/address changes stop the old containers before publishing
new settings; failed stops preserve old settings. Start recreates only this app's
two containers to reopen atomically replaced config/certificate mounts. Address
discovery runs on install/start, not continuously; a DHCP/VPN address change
requires applying/starting the app again. Manager HTTPS must already be trusted
for the address used to open LOS.

Baseline focused tests passed. A LAN-only compose regression failed with the
old Tailscale requirement, then passed. Full Windows Go tests/vet and UI build
passed. Eleven native Ubuntu amd64 root fixture tests passed, including discovery
with no Tailscale interface, legacy migration, repeated ensure, separate TLS keys,
certificate coverage/expiry and unchanged token/source. Multi-address manifests
reject public/wildcard, duplicate, injected and ambiguous legacy/new settings.

LOS-TEST2 deployment backed up binaries, UI and the original app snapshot. The
authenticated start request was made over LAN and returned 200. Runtime bindings
matched the discovered LAN, Tailscale and existing WireGuard addresses; no Docker
bridge/public/wildcard binding and no host port for the backend. The proxy token
and club source were preserved. No WireGuard/Tailscale settings were changed.
Only Manager and the two Hardwallet containers restarted; LND retained PID 3891.

Real Chrome popup tests passed from both LAN and Tailscale with the same-host
session, trusted HTTPS, secure Web Serial context, no JS errors, normal login for
an expired session and mocked stopped-app recovery. The initial LAN flow passed
but the harness failed during teardown of an in-flight mock route; the harness
now avoids that fetch, waits for route cleanup and sanitizes exception output.
Manager sessions were invalidated before the successful full rerun.

The browser installer passed over LAN: bundled firmware verification, required
acknowledgement, blocked existing-wallet update, simulated USB cancellation and
port-open failure cleanup, corrupt download rejection and unsupported-browser
handling. No external app requests or horizontal overflow. No real USB opened
or firmware written. Six firmware resources rejected anonymous LAN requests;
four authenticated binaries matched CI commit `03e9cc3`, and logout revoked
access. LAN status/fees/mainnet, anonymous 401, invalid-Origin 403 and logout
checks passed; the same protected coordinator checks also passed via Tailscale.

Rollback binaries and app snapshot:
`/var/backups/lightningos-hardwallet-pr260/before-lan`.
Prior UI: `/opt/lightningos/ui-before-lan`. Physical firmware flashing/recovery,
native arm64 and long-running certificate lifecycle gates remain outstanding.

## Historical: Open button from LAN (2026-10-14)

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
