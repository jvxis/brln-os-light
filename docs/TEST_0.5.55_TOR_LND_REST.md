# 0.5.55 — optional LND REST onion (#407)

## Scope and security

The dashboard Tor card can explicitly create and copy a REST onion. Nothing is
created by opening the dashboard. The fixed broker operation adds port 8080 to
127.0.0.1:8080, or reuses a manually configured matching service. It does not
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
  evidence that the network is merely slow. It must be resolved before readiness.

## Reproducing the privileged test

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
`install.sh`, `install_existing.sh`, native ARM installer path, and old Manager UI
upgrade deploying the changed broker unit. A root test under the correct sandbox
is not the complete authenticated UI → socket-activated broker deployment test.
No installer-matrix waiver was requested or assumed. No production node was used.
