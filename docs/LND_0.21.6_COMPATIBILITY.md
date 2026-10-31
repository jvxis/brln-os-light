# LND 0.21.6-beta compatibility validation

Reviewed on 2026-10-30 for LOS 0.5.51, against release base `2ab37a96`.
Installer/helper test source: `931c8306`.

## Scope and upstream evidence

- [Official release](https://github.com/lightningnetwork/lnd/releases/tag/v0.21.6-beta),
  commit `ac3a47f1a7cf082c11c23b801afac62b59cba836`.
- [Changes since 0.21.5](https://github.com/lightningnetwork/lnd/compare/v0.21.5-beta...v0.21.6-beta)
  are limited to the version, release notes and `godebug multipathtcp=0` in
  `go.mod`. No RPC/protobuf changes or database migrations are included.
- The release disables MPTCP listeners by default to mitigate the demonstrated
  CVE-2026-98432 exploit path. It does not repair the Linux kernel. Retain the
  LOS filter, explicit `GODEBUG` setting and operating-system security updates.
- Upstream binaries use their own Go toolchain; installing them does not
  require changing the Manager's Go version or generated LND RPC stubs.

`install.sh` now selects 0.21.6. `install_regtest.sh` obtains its independently
pinned native binaries from `scripts/regtest-lab/provision.sh`, now also 0.21.6.
Production release bootstrap inherits the selected installer's version.
Existing-node installers adopt native LND rather than replace it; existing LOS
users upgrade LND through the separate authenticated upgrade flow.

## Authentication of the release

The old LOS helper rejected the actual release with four recognized signatures
against its required five, before modifying installed binaries. The candidate
adds only the Boris fingerprint listed in the
[tagged upstream verifier](https://github.com/lightningnetwork/lnd/blob/v0.21.6-beta/scripts/verify-install.sh):
`BCEE34B0F9CD832214CE53005EA98470361ACB4F`.
Actual authentication then passed with five recognized signatures. The threshold
remains five; downloads, manifest verification and executable-version checks are
not bypassed. The privileged broker pins the updated helper's exact SHA-256.

Authenticated Linux amd64 archive SHA-256:
`47db47290df2d183393259a7b313ddcfc7376e9cd941bd691858250246fc763e`.
This is also the regtest provisioner's archive pin.

## Completed validation

Windows amd64: focused baseline LND upgrade/kernel tests, then `go test ./...`
and `go vet ./...` passed. The exact installer-version assertion was updated
from 0.21.5 to 0.21.6 without weakening it.

Ubuntu 24.04 amd64: the existing disposable VM hosted isolated network/mount/PID
fixtures and a private systemd-nspawn OS. Existing OS prerequisite packages and
trusted compiler/download caches were reused, while application/native binaries,
configuration and wallet state were absent for first-install scenarios. This
was not a minimal OS image with every prerequisite absent. No production wallet
was copied. The
owner's active three-node lab was kept separate from these fixtures.

- Real signed artifacts: reproduced the old helper's 4/5 refusal, then installed
  verified 0.21.6 binaries using the candidate helper in private `/usr/local/bin`.
- Full `install.sh`: passed, including authenticated HTTPS setup/login, System
  Check, persisted kernel Audit observation, broker/helper trust and mitigation
  service/timer. No wallet was initialized on this mainnet-profile fixture.
- Real authenticated `POST /api/lnd/upgrade`: Manager, broker and signed helper
  upgraded native LND 0.21.5 to 0.21.6. LND configuration and TLS hashes were
  preserved. Its disconnected Bitcoin backend had only mainnet genesis, no
  peers or funds. LND remained uninitialized; this is not a funded-wallet test.
- Linux `go test ./...`, `go vet ./...` and the four Python kernel-mitigation
  regressions passed with the project's Go 1.26.10 toolchain.
- Full `install_existing.sh`: passed over a live native LND. Its PID,
  configuration, TLS and native service definition were preserved. Authenticated
  setup/login, System Check, Audit observation, helper trust and mitigation
  checks passed again for this installation path.
- Full regtest baseline at `2ab37a96`: installed native 0.21.5, initialized three
  fictitious wallets and a triangle of channels, and passed native/API smoke.
  The actual `lo_regtest_bootstrap.sh --disposable-host`, with
  `LOS_REGTEST_REF=agent/0.5.51-lnd-0216`, then upgraded that lab to 0.21.6.
  All three node identities, channel points, configuration, UI password,
  secrets and TLS hashes were preserved, as was Bitcoin Core's PID. All nodes
  synchronized with two active channels each and no LND MPTCP listeners.
  Both native and authenticated LOS Lightning payments passed for
  Alice-to-Bob, Bob-to-Charlie and Charlie-to-Alice. A 10,000-sat fictitious
  on-chain send confirmed in the destination wallet. Financial API checks
  enforced reauthentication and checked duplicate intent consumption.
- Full fresh `install_regtest.sh --disposable-host` at `931c8306`: passed after
  clearing only the private fixture's fictitious wallets/configuration. All
  three native LNDs started at 0.21.6, synchronized and established two active
  channels each. Native and authenticated API Lightning payments, confirmed
  on-chain send, reauthentication, aliases and automatic mining passed. No LND
  MPTCP listener remained.

Installer regressions: `python3 -m unittest discover -s scripts/regtest-lab
-p 'test_*.py'` (31 tests), `python3 -m unittest discover -s scripts/tests
-p test_regtest_bootstrap.py` (8 tests), and the corresponding
`test_install_bootstrap.py` command (16 tests) passed. `bash -n` passed for the
three production installers, the regtest installer/provisioner, both bootstrap
scripts and the signed LND upgrade helper.

The first fixture attempts exposed harness setup errors, not silently skipped
checks: the existing-node installer initially had no default Bitcoin directory,
and a later empty-host bootstrap run correctly rejected the previous scenario's
dangling `/home/lnd/.lnd` link. Supplying the test backend directory and removing
only the fixture's old link/configuration allowed the unchanged checks to pass.
No safety check or assertion was disabled.

A resource-limited harness wait also timed out during an early regtest attempt,
allowing the next fixture scenario to start prematurely. That attempt was
discarded. The final scenarios run sequentially, with an explicit successful
completion marker required before clearing the private fixture for a fresh
installation. The namespace memory limit was raised from 2000 to 2300 MiB;
no second disposable VM or production service was started/stopped for it.

The private OS fixture was stopped after all five integration/test units exited
successfully. The owner's separate active lab retained its original node
identities, channel points, on-chain balances and configuration/password/TLS/
secrets hashes. All three HTTPS health checks passed with certificate validation,
and its automatic mining timer remained active. Its LND binaries were not
upgraded as part of these isolated tests.

### Native socket and firewall compatibility

The matrix used actual upstream LND executables in a private network namespace,
with verified TLS state requests and uninitialized profiles:

| LND / setting | MPTCP listener | TLS request |
| --- | --- | --- |
| 0.21.5, default | Yes | Passed |
| 0.21.5, LOS `multipathtcp=0` | No | Passed |
| 0.21.6, default | No | Passed |
| 0.21.6, combined `http2client=0,multipathtcp=0` | No | Passed |
| 0.21.6, explicit `multipathtcp=1` positive control | Yes | Passed |

The actual unchanged LOS nftables helper was applied inside that namespace.
It preserved an established HTTPS connection (same TCP source tuple), allowed
new verified HTTPS connections, and dropped harmless TCP packets with IPv4 NOP
options while increasing its drop counter. No global VM firewall or sysctl was
modified for this test. The positive control confirms that explicit opt-in can
override upstream's default; retaining LOS's explicit disable remains useful.

## Limits

Native arm64 testing was explicitly waived by the owner and is not counted as
passed. Production funds-bearing nodes are not regression fixtures and were
not accessed or changed. The owner must install any corrected kernel separately;
this LND update is not evidence that kernel remediation is complete.
