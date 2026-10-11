# 0.5.43: LND onion recovery after Tor restart (#245)

Status: implementation for review; **draft, not ready to merge/release**. Full
installation/upgrade and real LND reachability acceptance remain outstanding.

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

## Required before marking ready

- Real LND without funds: UI-triggered Tor update, externally reachable same
  onion within approximately two minutes, unchanged LND PID, then successful
  `systemctl restart lnd` through the actual drop-in.
- Real LND with encrypted key, a pre-existing attached descriptor returning
  `250`, custom paths/CLI overrides, and actual listener/target mappings.
- Full fresh `install.sh`, first LOS `install_existing.sh`, and native arm64
  `install_existing_pi.sh`, including Python absent/present, legacy layouts,
  existing services/data/permissions and repeated execution.
- Actual UI upgrade from supported 0.5.42 using its installed updater to this
  candidate, followed by a Tor update. A new helper against old sources does
  not substitute for that transition.
- Privileged systemd hook integration, concurrent real LND restart, failed or
  interrupted APT steps, retry and rollback under the installed unit hardening.
- Journal/Manager/broker inspection with the real LND fixture to prove no key
  output, beyond the helper/protocol tests already performed.

## Rollback considerations

Keep the standalone helper and release drop-in while a recovered detached onion
may exist. Removing them first can recreate the startup collision. Release the
recorded onion during an operator-scheduled LND restart before removing the
helper/drop-in. Reverting the Manager/broker does not remove these standalone
files. Do not restart Tor or LND in production merely to validate rollback.

Tor restarts outside LightningOS (for example unattended upgrades) are not
automatically covered; the periodic watchdog proposed in #245 remains optional
and is deliberately outside this PR.
