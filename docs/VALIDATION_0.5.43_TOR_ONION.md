# 0.5.43: LND onion recovery after Tor restart (#245)

Status: implementation for review; **draft, not ready to merge/release**. Real
unfunded LND/Tor upgrade and release-hook acceptance passed on Ubuntu amd64;
the full installation/old-updater/architecture matrix remains outstanding.

## Behavior and boundaries

The Tor upgrade broker installs a root-controlled Python 3 helper and an LND
systemd drop-in before starting APT. After a Tor restart, the helper restores
the existing v3 onion with `ADD_ONION ... Flags=Detach`, without restarting LND.
An EXIT handler also attempts recovery after a partially failed package update.
The installer installs the same helper before its package phase and invokes
recovery after configuring Tor and on exit. Both existing-node installers
install the release hook. Repository verification-only retains its read-only
behavior. No watchdog or new API endpoint is introduced.

The helper reads the running LND's explicit `--lnddir`, optional `--configfile`
and CLI overrides, then queries `lncli getinfo` for the advertised onion/port.
It uses all configured listen ports and `tor.targetipaddress`, following
[LND createNewHiddenService](https://github.com/lightningnetwork/lnd/blob/v0.21.4-beta/server.go)
and [Tor port mapping](https://github.com/lightningnetwork/lnd/blob/v0.21.4-beta/tor/cmd_onion.go).
It requires a unique advertised v3 onion and an absolute plaintext key path.
Nonstandard layouts that cannot be resolved safely produce a warning.

Control connections are loopback-only, authenticated with SAFECOOKIE, including
verification of the server challenge before authenticating the client. The key
never appears in a subprocess argument, helper output, API response or recovery
record. Only the service ID and local control address are recorded under the
root-only `/var/lib/lightningos-onion` directory. The helper suppresses external
stderr and never formats arbitrary exception/protocol content into its output.

`ExecStartPre=-+/usr/local/libexec/lightningos-lnd-onion release` runs as root
before LND starts, releases the recorded detached service, and never blocks LND
startup on error. Recovery and release share a bounded file lock. Recovery has
a shorter hard deadline than the release lock wait. A write-ahead record permits
release after interruption; a changed LND PID causes recovery to abort/release.
Failed release retains the record for retry. The helper never removes an
attached service owned by LND and does not claim an existing service on collision.

`GETINFO hs/service/desc/id/...` needs special care: Tor 0.4.9.11 returns
`551 Not found in cache` before a descriptor is available, including for an
already registered attached service. Recovery handles this bounded response;
an `ADD_ONION` collision leaves the existing service in place. A `250` descriptor
reply is a no-op. Other unknown failures are warnings, not assumed absence.

Encrypted keys are refused. The upgrade modal shows a translated warning, even
if package installation completed, explaining that an operator must schedule
any necessary LND restart. No automatic LND restart occurs.

The new broker carries the helper so installation does not depend on an old
LOS updater knowing the new file. This is an implementation property, **not
evidence that the transition through the old updater has been exercised**.

## Baseline and executed validation (2026-10-10)

Base: `origin/agent/0.5.43-release`, commit `3be5d220`.
Baseline Windows amd64: existing Tor/LND tests passed before editing:

```powershell
go test ./internal/privileged ./internal/server -run 'TestTor|Test.*Tor|Test.*LND' -count=1
```

After implementation on Windows amd64:

```powershell
go test ./...
go vet ./...
npm.cmd ci --no-audit --no-fund
npm.cmd run build
```

All passed. Vite reports the existing large-chunk warning. `npm.cmd` was used
because this host disallows execution of the npm PowerShell shim.

Disposable Linux: `los-disposable-0542-logs`, UUID
`3619cb5d-2889-448e-94cd-1dbf8a4a181e`, Ubuntu 24.04.3 amd64.
Baseline: no Bitcoin or LOS Manager; an existing transient `lnd.service` runs
a synthetic Python journal producer from the earlier log tests. That service
was preserved; it is **not a real LND acceptance fixture**. Tor 0.4.9.11 was
installed in this disposable VM. Lifecycle tests launch and terminate their
own private Tor process with networking disabled, separate data and control
port. No production host or wallet was used.

```bash
python3 -m unittest discover -s scripts -p test_lnd_onion.py -v
LOS_ONION_DISPOSABLE=1 python3 scripts/test_lnd_onion_tor.py
# Cross-built test binary, actually executed as root in the Linux VM:
/tmp/los-0543-privileged.test -test.run TestTor -test.v
# Each modified shell script was checked with bash -n.
```

19 Python tests passed: multiple/default/IPv6 target ports, absent/disabled LND,
already published onion, missing descriptor/collision, encrypted key refusal,
missing release hook, changed PID, interrupted/failed registration state,
repeated/failed release, malformed replies and spoofed SAFECOOKIE server.
Six Go Tor tests passed on Linux, including trusted file installation,
idempotence, writable-file and symlink rejection, digest and protocol guards.

The real Tor regression demonstrated:

1. An attached onion disappears across a Tor restart (original failure).
2. Recovery reuses its key/identity and retains it after the helper disconnects.
3. Repeated recovery preserves the service while its descriptor is unavailable.
4. Registration with the same key collides while the detached service exists
   (the second failure described by the issue).
5. Release, including repeated release, allows the next attached registration.

The fixture substitutes LND config/getinfo/PID and hook inspection; it uses the
real control protocol and SAFECOOKIE exchange. The test cookie location is
substituted to the private fixture directory. It does not test public Tor
reachability or real LND startup.

Chromium component smoke with mocked API responses passed for English and
Portuguese: normal completion has no warning; encrypted-key failure remains
visible as an alert alongside Tor package completion. Production UI build
passed. The smoke renders the real TorUpgradeCard, not a duplicate component.

## 2026-10-11: operational result and follow-up regressions

The integrated `504fe032` candidate was deployed to LOS-TEST2 (Ubuntu 24.04
amd64). The owner subsequently authorized a normal UI Tor upgrade from
0.4.9.11 to 0.4.9.14. Package installation and bootstrap completed, with the
same LND process, configuration/TLS/onion-key hashes and advertised onion.
The original onion accepted a TCP connection through the host's Tor proxy.
Docker states/images and Manager/Bitcoin/LND health were preserved. This was
an authorized operational upgrade on a funds-bearing node, **not** a disposable
failure-injection test or an independent external onion reachability test.

The start request nevertheless returned HTTP 500: the broker completed its
preparation in 4682 ms, after the client's short socket deadline. Follow-up
review also found that repository configuration replaced/removed the parent's
onion-recovery EXIT trap. The normal success path recovered the onion, but a
partial APT failure would bypass the fallback.

The follow-up correction:

- Gives both broker and client a 30-second minimum Tor-start preparation
  budget, leaving dry-run and explicit caller deadlines intact. The HTTP
  request allows 45 seconds for discovery plus preparation.
- On a start timeout/cancellation, checks the fixed upgrade unit with a fresh
  bounded read and never retries the mutation. A running unit is confirmed;
  otherwise HTTP 504 explains the uncertainty and asks for a status/log check.
  Explicit broker rejections are not converted into success.
- Scopes repository-key cleanup to a subshell, preserving the parent EXIT
  recovery trap, original failure status and verification-only behavior.
- Updates the broker's pinned helper digest; arbitrary helpers remain rejected.

Baseline and red/green validation on Windows amd64:

```powershell
go test ./internal/privileged ./internal/server -run 'TestTor|TestExtractTor|TestAptPolicy|TestClientTor' -count=1
$env:TEST_BASH='C:/Program Files/Git/bin/bash.exe'
python -X utf8 scripts/test_tor_upgrade_exit.py -v
go test ./internal/privileged -run TestTorStart -count=1
go test ./...
go vet ./...
```

Existing focused tests passed before changes. The new delayed-client and
preparation-budget tests failed on the original code. The new shell harness
ran the production main/configuration/recovery functions with external tools
stubbed: both partial-APT-failure cases failed because recovery was not called;
success and pre-package/verify cases already passed. All these tests pass after
the correction. Added tests cover the broker-side budget, cancelled callers,
socket timeouts, one-shot state confirmation and explicit rejection handling.
Full Go tests and vet passed; `bash -n` passed for the embedded helper.

The shell harness verifies successful recovery exactly once, recovery failure
without masking the APT exit code, temporary-key cleanup, verification-only,
repository failure and metadata failure. It performs no real package, network,
systemd or LND actions. It is **not** Linux privileged integration coverage.
The existing `test_lnd_onion.py` suite could not run on Windows (`fcntl` is
Linux/POSIX-only); it is not counted as passed in this follow-up. Its helper
code was not changed. No follow-up artifacts were deployed to LOS-TEST2 and
no second Tor upgrade or LND restart was attempted.

## 2026-10-11: unfunded real LND and delayed UI start

Source: `4230a2d5bc38ab9bb1238eb959fa5b906383665a`. Reused the disposable
`los-disposable-0542-logs` (same UUID above), Ubuntu 24.04.3 amd64, 2 vCPU,
4 GiB RAM. Preserved its old synthetic journal fixtures before replacing the
fake LND service. Official LND 0.21.4-beta was checked against its release
SHA-256 manifest. The real daemon used an explicit custom lnddir, mainnet
`nochainbackend`, `noseedbackup`, **zero funds and zero channels**. It had a
real listener, RPC and v3 onion, but no chain synchronization; this is not an
installer-parity or normal Bitcoin-backend acceptance test.

The candidate Manager, broker and UI were installed with the repository's
hardened systemd units. The Manager ran as `lightningos`, with login enabled,
a read-only macaroon and the short three-second default broker timeout.
Authentication used the real browser UI, verified TLS and CSRF handling.
SSH identity was verified through the local VM console. Temporary trust for
the lab's public HTTPS certificate was removed after each browser test.

Native Linux checks, executed from the corresponding source/package directory:

```bash
python3 -m unittest discover -s scripts -p test_lnd_onion.py -v
python3 scripts/test_tor_upgrade_exit.py -v
LOS_ONION_DISPOSABLE=1 python3 scripts/test_lnd_onion_tor.py
# Go test binaries cross-built from the exact commit and executed on Linux:
(cd internal/privileged && /home/admin/los0543-tor-privileged.test -test.run 'TestTor|TestClientTor' -test.v)
(cd internal/server && /home/admin/los0543-tor-server.test -test.run 'TestTor|TestExtractTor|TestAptPolicy' -test.v)
```

All passed: 19 Python onion tests, four shell-trap regressions, real private-Tor
protocol lifecycle and focused Go suites. The complete combined-source Windows
`go test ./...`, `go vet ./...` and `npm.cmd run build` also passed; the existing
Vite chunk-size warning remains.

### Partial package failure with a real Tor restart

Ran the **unchanged production helper** inside a private mount namespace. Only
that namespace replaced `apt-get install` with a fixture that restarted the
real `tor@default` service and exited 42. Repository authentication, metadata
refresh, EXIT trap, onion helper, systemd hook and LND were real. Host APT was
byte-identical afterwards; no Tor downgrade was needed.

- Exit 42 was preserved; recovery executed exactly once after repository setup.
- LND PID, advertised onion, configuration, TLS certificate/private key and
  onion key hashes were unchanged. The original onion was registered detached.
- **Limitation:** the already-running independent Tor client did not reconnect
  within the initial approximately 110-second probe window. Further probes also
  timed out. Restarting only this test client reached the original onion in
  12.6 seconds. This is consistent with client/descriptor propagation effects,
  but does not prove that explanation or meet a two-minute existing-client
  recovery guarantee. Native Tor and LND were not restarted for that probe.

### Real authenticated UI upgrade with slow broker activation

A disposable-only systemd override added six seconds before broker activation;
the default configured client budget remained three seconds. Edge clicked the
real upgrade confirmation once. The start returned **HTTP 200 in 6732 ms**,
with exactly one POST. Tor upgraded from 0.4.9.11 to 0.4.9.14 through the official
repository and the actual broker/systemd path. The modal showed recovery and
completion logs, with no onion warning. Final status was healthy, SOCKS/control
ready and `running=false`.

The existing independent Tor process then reached the same LND onion in 12.1
seconds. LND PID and all protected file hashes remained unchanged. After an
explicit restart of **only this unfunded disposable LND**, the actual
`ExecStartPre` release hook consumed the recovery record; LND started without
an address collision and advertised the same onion. Independent connectivity
succeeded in one second. The PID changed only at this intentional LND restart.

Captured journals and helper output were checked in memory for exact private
onion/TLS material and the test UI credential; none was present. This check
complements, rather than replaces, the protocol/output unit tests. Root-only
baselines, logs and results were preserved under
`/var/lib/los0543-tor-validation`. The injected broker delay was removed and
the installed service-user broker self-test passed again. The VM was then
given a normal ACPI shutdown, without deleting disks or test records.

## Required before marking ready

- Investigate or explicitly accept the observed existing-client reconnection
  delay after the partial-failure scenario; do not promise a two-minute bound.
- Real LND with encrypted key, a pre-existing attached descriptor returning
  `250`, custom paths/CLI overrides, and actual listener/target mappings.
- Full fresh `install.sh`, first LOS `install_existing.sh`, and native arm64
  `install_existing_pi.sh`, including Python absent/present, legacy layouts,
  existing services/data/permissions and repeated execution.
- Actual UI upgrade from supported 0.5.42 using its installed updater to this
  candidate, followed by a Tor update. A new helper against old sources does
  not substitute for that transition.
- Concurrent real LND restart, interrupted package steps, full retry and rollback
  under installed unit hardening. A partial failure and a subsequent successful
  real upgrade were exercised above, but not the complete fault matrix.

## Rollback considerations

Keep the standalone helper and release drop-in while a recovered detached onion
may exist. Removing them first can recreate the startup collision. Release the
recorded onion during an operator-scheduled LND restart before removing the
helper/drop-in. Reverting the Manager/broker does not remove these standalone
files. Do not restart Tor or LND in production merely to validate rollback.

Tor restarts outside LightningOS (for example unattended upgrades) are not
automatically covered; the periodic watchdog proposed in #245 remains optional
and is deliberately outside this PR.
