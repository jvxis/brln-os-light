# 0.5.55 — optional LND REST onion (#407)

## Scope and security

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

Keep the PR draft until installation/upgrade integration is complete: fresh
`install.sh`, `install_existing.sh`, and old Manager UI
upgrade deploying the changed broker unit. A root test under the correct sandbox
is not a complete installer/old-updater test. Authenticated API/socket-activated
broker deployment was additionally exercised on LOS-TEST2; it does not replace
the installer matrix. The owner explicitly waived ARM for this PR on 2026-11-12;
ARM is not recorded as tested. The amd64 fresh/existing/old-updater gates remain
mandatory. Authorized fixtures are `los-disposable-0555-lndg`,
`los-disposable-0555-tor-fresh` and `los-disposable-0555-tor-existing`, one running
at a time, with normal shutdown and retained disks/evidence at completion.
No funds-bearing node was used for payment, credential-revocation or failure
injection fixtures; those tests remained isolated on disposable regtest.
