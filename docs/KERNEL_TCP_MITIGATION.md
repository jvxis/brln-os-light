# Temporary IPv4 TCP mitigation in LOS 0.5.50

References, checked 2026-10-30:

- [Ubuntu CVE-2026-98432 status](https://ubuntu.com/security/CVE-2026-98432).
- [Go Security Team advisory](https://seclists.org/oss-sec/2026/q4/437).
- [Go MPTCP defaults](https://go.dev/doc/godebug#go-124).

The underlying bug is in the Linux kernel; disabling MPTCP alone is not a
complete fix. The distribution's patched **running** kernel remains the final
remedy. Do not infer backported patch status from a generic kernel-version
comparison or merely from having installed a package without booting it.

## Scope

The dedicated `ip lightningos_kernel_tcp` table drops TCP/IPv4 with IPv4 header
length greater than five 32-bit words, in prerouting at priority -300. Applying
or repairing the table is an atomic nft transaction. It never flushes the global
ruleset or changes UFW, Docker, SSH, tunnel configuration or other nft tables.
This covers incoming local/forwarded traffic through that hook. It does not
promise protection for an unexamined alternate path or another network namespace.

The filter concerns **IPv4 options**, not TCP options. Legitimate traffic using
IPv4 options is also dropped. Ordinary TCP (including MSS, timestamps and window
scaling), IPv6 and UDP are not matched by this rule.

`lightningos-kernel-tcp.service` checks the table's actual hook, priority, match
expressions and drop verdict. Its timer runs every 30 seconds and repairs a
missing/incorrect table. Startup is ordered after UFW/nftables and before
`network-pre.target`. Package bootstrap temporarily masks nftables.service if
needed so installing the nft tool cannot activate its default global ruleset.
The operator's previous mask is preserved; nftables.service is not enabled.

A later global ruleset flush can create a gap until the next repair. A stopped
timer or failed repair eventually makes the diagnostic stale. A successful
oneshot exit by itself is not treated as proof that the filter still exists.

The Manager reads only a small root-owned status file under
`/run/lightningos-kernel-tcp`; proof older than 90 seconds, future-dated,
malformed, symlinked or writable by another account is not accepted. System
Check shows a warning for missing/unverified/disabled protection. Audit actions:

- `security.kernel_tcp.observed`: verified state observed, including startup.
- `security.kernel_tcp.unverified`: missing, stale or explicitly disabled state.
- `security.kernel_tcp.repaired`: verified filter with an increased repair count.

Only state changes and repairs are recorded, not every timer tick. No packet
payload, address, credential or `GODEBUG` value is included in these events.

LOS Go binaries default to `multipathtcp=0` at build time. Native service drop-ins
append a dedicated EnvironmentFile for `lightningos-manager.service`,
`lnd.service` and `lnd@default.service`. Other configured GODEBUG entries are
preserved. Unsupported environment-file layouts are refused instead of silently
overwriting them. Existing ExecStart, wallet configuration and services are not
replaced or restarted by this helper. Custom LND unit names beyond these two
native layouts require separate operator configuration. A per-socket explicit
MPTCP request can override Go defaults; the packet filter remains independent.

Installers apply the helper directly. The new Manager/broker also reconcile its
digest-pinned asset, covering an upgrade launched by an older installed updater
that does not know the new installer hook. No caller-selected path, helper
content, command or package is accepted in place of the reviewed asset.

## Diagnostics

```sh
sudo /usr/local/libexec/lightningos-kernel-tcp status
sudo nft list table ip lightningos_kernel_tcp
systemctl is-active lightningos-kernel-tcp.timer
sudo journalctl -u lightningos-kernel-tcp.service -n 30 --no-pager
```

The first command reads the current kernel rule rather than a service badge.
The oneshot service can be inactive between checks while the timer is active.
Service drop-ins apply on the next process start; the upgrade does not restart
LND or Bitcoin to change MPTCP. Filter verification does not assert which
protocol an already-running listener uses.

## Explicit removal after a verified kernel fix

After confirming the distribution's patch is in the running kernel:

```sh
sudo /usr/local/libexec/lightningos-kernel-tcp disable
sudo /usr/local/libexec/lightningos-kernel-tcp status
```

This removes only the dedicated table, stops/disables the repair timer and
stores a root-owned opt-out. Repeated installation and upgrades honor that
opt-out. The ordinary-TCP defaults remain; disabling the temporary filter does
not require restoring MPTCP or restarting LND.

To explicitly re-enable:

```sh
sudo rm /etc/lightningos-kernel-tcp/disabled
sudo /usr/local/libexec/lightningos-kernel-tcp install
```

If installation fails, inspect root-owned paths and service configuration,
restore the prerequisite and rerun the installer. A failed atomic nft batch
does not partially replace the existing table. Do not reset a global firewall
or remove wallet data to recover this helper.
