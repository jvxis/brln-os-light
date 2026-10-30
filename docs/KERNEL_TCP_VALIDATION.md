# LOS 0.5.50 kernel TCP mitigation validation

Validation date: 2026-10-30. Operational scope and commands are in
[KERNEL_TCP_MITIGATION.md](KERNEL_TCP_MITIGATION.md).

## Environment and source

Ubuntu 24.04 amd64 disposable VM, Linux 6.14.0-37-generic, Go 1.26.10,
native LND 0.21.5-beta and Bitcoin Core 31.2.0. Privileged installation,
upgrade and packet checks ran in private systemd-nspawn mount/network
namespaces within that VM. Only one full installation fixture ran at a time.
Production nodes and funds-bearing services were not regression fixtures.

The final runtime source is `040e9d3523d308e66bdb5dc0dc05ab7562d18c05`.
Fresh/existing installers use `e772d17ff6651ea75c1c921eacf297fe8e65f87e`,
which adds documentation and a Python regression test without changing runtime
code. The actual old-updater transition used
`f65f296837fa05f095b28c2c4a12cbcdf0a05a13`; its sole runtime difference from
the final source is the subsequent correction of the System Check value to a
boolean. The final scalar contract was checked through authenticated HTTP.

## Baseline and packet behavior

Before mitigation, a benign TCP connection with IPv4 NOP options succeeded.
A standalone Go listener used MPTCP (`SO_PROTOCOL=262`). These are exposure
and compatibility checks, not execution of the memory-corruption exploit.

After applying the helper:

| Scenario | Result |
| --- | --- |
| Existing ordinary TCP connection and new TCP connections | Preserved |
| TCP with benign IPv4 options | Dropped; dedicated rule counter increased |
| TCP options used by normal connections | Ordinary TCP remained functional |
| IPv6 TCP | Preserved |
| UDP carrying IPv4 options | Preserved |
| Restart of the standalone Go test listener | Ordinary TCP (`SO_PROTOCOL=6`) |
| Native regtest payments Alice to Bob and Bob to Alice, 1,000 sat each | Succeeded with filter active |
| Native fixture LND processes and existing channel sets during filter installation | Preserved |

No production LND restart was required to apply the packet filter. Service
environment changes take effect at the next process start. A later cold
namespace/systemd boot waited for the real native LND listeners: two ordinary
TCP listeners and zero MPTCP listeners, with the other existing GODEBUG setting
retained. The mitigation completed before `network-pre.target`; the repair
timer was active. This was not a VM/kernel reboot.

## Installation and upgrade

| Scenario | Result |
| --- | --- |
| Full `install.sh`, initially absent native LOS/LND binaries | Passed; authenticated UI/API, broker, timer and verified filter |
| Native LND installation during that fresh install | Official 0.21.5-beta archive downloaded, authenticated and installed |
| Full `install_existing.sh` over disconnected, unfunded native Core/LND | Passed; original PIDs, configs, unit files and LND TLS key preserved; authenticated diagnostic and Audit verified |
| Actual installed 0.5.49 Manager UI/API upgrade to 0.5.50 | Passed using the original old broker and embedded updater |
| Old updater without the new script hook | New Manager/broker reconciled the pinned helper and installed the absent nft prerequisite |
| New updater with unsafe mitigation runtime directory | Refused before replacing app binaries; Manager/LND PIDs, binaries and configuration preserved |
| New updater after restoring directory permissions | Retry completed; final authenticated scalar diagnostic verified |
| Existing LND during both upgrade paths | PID, configuration, credentials and TLS key preserved |

The old-updater catalog was isolated inside the disposable namespace with a
private trusted CA and local immutable candidate tag. No public release or
source tag was published, and the installed old helper was not replaced to
simulate success. The full scripts performed their normal native Go and UI
builds. Trusted prefilled Go caches were used; these are not claims of empty
compiler caches or fully offline installation.

The minimal Ubuntu fixture needed standard prerequisites (complete Noble
repositories including updates/security, iproute2 and the existing `lnd`
account's home directory) before baseline installation. Those setup failures
were retained in private logs and corrected in the fixture, without weakening
installer checks or modifying production code to accommodate the fixture.
The background existing-node runner also initially omitted the normal root
login `HOME`, leaving Go without a default module-cache location. A native Go
environment probe reproduced that condition; the installer build commands are
unchanged from the baseline. Restoring `HOME=/root` allowed the full installer
to be retried without changing the product or its existing native node data.

## Firewall coexistence and recovery

| Scenario | Result |
| --- | --- |
| Repeated helper installation | Preserved unrelated nft table and other GODEBUG values |
| nft tool absent, general nftables.service previously disabled | Installed tool without enabling vendor service; sentinel table survived |
| Operator's persistent nftables.service mask | Preserved through absent-tool bootstrap and full existing-node installation/retry; unrelated nft table survived |
| Dedicated table deleted | Timer repaired it and the verified repair was audited |
| Invalid real nft transaction | Existing table and rule handles preserved atomically |
| Unsafe symlink destination | Installation refused without modifying the sentinel target |
| Explicit disable, repeated install, re-enable | Own table removed, persistent opt-out honored, explicit re-enable succeeded |
| Restrictive root umask 077 | Root-owned 0755 runtime directory and readable 0644 proof; unprivileged Manager identity read it |
| Active UFW enable/reload | Dedicated filter and ordinary traffic preserved |
| Real Docker published HTTP port from a separate client namespace | Ordinary connection succeeded; IPv4-options TCP dropped before forwarding |
| Real SSH local forwarding to the HTTP service | Succeeded before/after upgrade, with strict host-key verification |

Docker 29.1.3 and a read-only BusyBox 1.37.0 amd64 HTTP container were used in
the disposable network namespace. The fixture did not alter the VM host's
normal Docker services, SSH service PID, interfaces or forwarding sysctl.
Dedicated Docker/client/SSH test processes were stopped after verification.
All installation namespaces and their network helpers were subsequently stopped;
their retained filesystem roots were restored to mode 0700. Final comparison
of the original three-node laboratory confirmed unchanged LND PIDs, identities,
channels, balances, configuration/credential/TLS hashes, chain synchronization
and certificate-validated HTTPS health. The production test VM remained running
and only one disposable VM was running.

## Authenticated diagnostics, audit and automated checks

The real HTTPS API, with certificate validation and normal login/CSRF handling,
returned a security item with a boolean value. Verified proof was green;
stale proof was a warning; repair returned it to green. PostgreSQL-backed Audit
contained observation, unverified and repair events with only the four expected
state/counter fields. A 35-second unchanged interval did not add duplicate timer
events. No environment values, packet payloads, addresses or credentials were
included in these security events.

Commands and results:

```sh
# Linux amd64, final runtime source:
go test ./...
go vet ./...
go build -trimpath -buildvcs=false ./cmd/lightningos-manager
go build -trimpath -buildvcs=false ./cmd/lightningos-privileged

# Linux root, final test source: four tests passed, none skipped.
python3 lightningos-light/scripts/tests/test_kernel_tcp.py
```

The full Go test run initially encountered the existing non-hermetic
`TestLNDgPostgresBackedLNDUsesPrivateChannelDBPlaceholder`: it assumes that
`/data/lnd/data/graph/mainnet/channel.db` is absent, while the live native Bolt
fixture has that file. The same failure was reproduced on the original
pre-mitigation source. The complete suite then passed with an empty `/data/lnd`
bind mount visible **only to the unit-test process's private mount namespace**;
the running LND retained its original database. No test assertion was weakened
or skipped. Optional payment-cursor PostgreSQL tests used a disposable local
database over its Unix socket.

The root-umask regression test failed against the pre-fix helper and passed
against the current helper. Focused Windows Go checks also passed for the
kernel status parser, integration catalog, asset tampering and upgrade-helper
digest. Each full installer/updater built the frontend with `npm run build`;
the existing System Check response contract remained scalar.

## Limits

- Native arm64 installation was explicitly waived by the owner and was not
  executed; amd64 does not establish native arm64 coverage.
- No full VM/kernel reboot, forced SIGKILL during dpkg, Ubuntu 22.04 installation
  or external Tailscale/club gateway path was exercised.
- Locked, unfunded native mainnet fixtures cover installation and process/data
  preservation, not mainnet payments or a fully synchronized mainnet chain.
  Financial compatibility checks used fictitious regtest funds only.
- The dedicated IPv4 prerouting rule does not prove protection for unexamined
  network namespaces or alternate paths. A later global ruleset flush can
  create a gap until the timer repairs it.
- This is temporary mitigation. Neither a green diagnostic nor disabling
  MPTCP proves that the running distribution kernel contains the final fix.
