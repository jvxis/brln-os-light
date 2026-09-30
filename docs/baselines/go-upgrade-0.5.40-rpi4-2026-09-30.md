# Raspberry Pi 4 upgrade baseline — 2026-09-30

Owner-provided RPi4 running an existing LightningOS installation, with two
active channels. This is a native ARM64 observation, not a disposable fixture
or an executed installation/upgrade regression test. No binaries, services,
firewall rules, wallet state or channel settings were changed.

## Observed baseline

- Architecture `aarch64`; Debian 13.5 (trixie), Raspberry Pi kernel
  `6.12.47+rpt-rpi-v8`; approximately 8 GiB RAM and 37 GiB root storage available.
- LightningOS `0.5.28-Beta`; public release discovery offered `0.5.39-Beta`.
  No upgrade was running or requested.
- System Go `1.24.12 linux/arm64`, Node `26.5.0`, PostgreSQL `18.4`.
- Manager, privileged broker socket and LND active. Manager/LND restart counts
  were zero, with activation timestamps unchanged across observations.
- LND `0.21.1-beta`, wallet unlocked, synchronized to chain and graph;
  two active channels, zero inactive and zero pending.
- Active Bitcoin source remote, RPC reachable. PostgreSQL service active.
- Authenticated health, system-check, upgrade status, wizard status, LND,
  Bitcoin source, PostgreSQL and apps endpoints returned HTTP 200. An initial
  health sample was WARN; subsequent samples were OK without intervention.
  System-check remained ERR: UFW inactive produced a Manager network-exposure
  diagnostic, and Tor reported a newer version available. This does not by
  itself establish Internet exposure. These are baseline findings, not changes
  introduced by PR #211.

The existing trusted SSH host identity was verified. The public HTTPS CA was
retrieved through that authenticated connection; API requests verified TLS.
Credentials, cookies and response bodies containing private node data were not
included in the evidence committed here.

## Old updater compatibility evidence

The repository's published `0.5.28-Beta`, `0.5.33-Beta` and `0.5.39-Beta` tags
contain byte-identical `internal/server/assets/upgrade-app.sh` assets:

```text
6a6d39d79d642d4565aba4778bd381d9f72e24d96d105d1e597b9b2eb6ee1a4c
```

This is the exact legacy helper allowed by the candidate broker exclusively
for the 0.5.40 recovery transition, and exercised by the installed 0.5.39
Manager in the [amd64 lab report](go-upgrade-0.5.40-virtualbox-2026-09-30.md).
The observed system Go satisfies the bridge's retained Go 1.24/toolchain
1.24.12 requirement. Neither observation proves that the installed RPi4
Manager has completed the transition: no updater was extracted from its
binary or executed on this node during this baseline.

## Remaining coverage

The RPi4 provides real ARM64 hardware for an assisted existing-installation
upgrade, after the candidate and transition are ready. Its live channels must
not be used for failure injection or destructive installer fixtures.

Native first-install validation still needs a separate disposable environment,
for example a spare boot SD/SSD on this RPi4 with the current storage disconnected
and preserved. ARM64 emulation can add executable coverage but does not close
the native hardware gate. No first-install script should be rerun on the
current installation merely to validate the internal upgrade.
