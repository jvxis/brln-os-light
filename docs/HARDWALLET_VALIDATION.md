# BRLN Hardwallet: experimental validation

PR: https://github.com/jvxis/brln-os-light/pull/260, targeting `agent/0.5.45-release`.
Companion firmware/coordinator: https://github.com/jvxis/brln-hardwallet, branch
`brln/tdisplay-los`. This is not a release or approval to use real funds.

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
