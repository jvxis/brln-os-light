# Upgrade logs and existing Tor repositories (0.5.28)

## Scope

Two independent failures were observed on BRLN HUB: journal reads failed only
when the browser's RFC3339 `since` filter was supplied, and the actual Tor
upgrade failed because duplicate official APT sources used different Signed-By
keyring paths. A logging failure alone does not imply an upgrade failure.

The shared journal reader now converts RFC3339 timestamps into an explicit UTC
calendar timestamp understood by older journalctl versions. It preserves the
instant, handles offsets/day boundaries and retains microsecond precision.
Relative and existing journal-native filters remain unchanged. Output formatting
and the bounded line limit are unchanged. Tor's own bootstrap check also uses
the UTC calendar spelling.

## Tor source reconciliation

Only the confirmed Tor upgrade workflow writes configuration. Status remains
read-only (except the existing explicit `force=1` metadata refresh).

1. Download and verify the pinned Tor key fingerprint and signed InRelease.
2. In verify-only mode, exit without touching source/keyring files.
3. Preflight all eligible APT source files, directory ownership and keyring paths.
   Refuse symlinks, hard-linked source files and unsafe writable/owned paths.
4. Plan changes to active official Tor entries for the current OS suite. Support
   both `.list`/`deb-src` and deb822 `.sources`, including field continuations.
   Keep disabled entries, unrelated repositories and other suites unchanged.
   A stanza mixing Tor with unrelated URIs or suites is refused before writes;
   the operator must split that exact stanza manually.
5. Back up every changed source/keyring under a mode-0700 directory named
   `/etc/apt/lightningos-tor-backup-*`, with originals and `manifest.json`.
6. Comment superseded entries and maintain one canonical current-suite Tor
   stanza in `sources.list.d/tor.sources`, preserving unrelated stanzas there.
   Write the authenticated keyring. Files are replaced atomically individually;
   a caught partial-write failure restores files changed by this invocation.
7. Run the existing APT update/install/version checks. Do not downgrade. Only
   restart Tor when an actual package update occurred and restart was authorized.

The reconciler uses Python 3's standard library; it refuses to proceed if Python
3 is unavailable. Its code is embedded in the existing broker-pinned shell
helper, not downloaded or invoked from an untrusted sidecar. Manager and broker
must be released/upgraded together because the helper digest changed.

When APT cannot resolve a candidate due to the exact official Tor Signed-By
conflict, status enables the existing confirmed Upgrade action and explains why.
Other APT errors do not enable this recovery exception. Automatic discovery of
an error never performs the repair by itself.

## Recovery and validation

The journal prints the backup directory before the first write. To undo a
completed reconciliation, an operator should inspect its manifest, restore only
the listed original files/modes and remove only listed newly created files.
Restoring the original conflicting sources will also restore the APT conflict.
Backups are retained; unrelated APT failures after reconciliation do not trigger
an automatic return to a known conflicting configuration. Power loss is not a
multi-file atomic transaction; the retained backup supports manual recovery.

Tests exercise the embedded Python against isolated jammy/noble fixtures,
idempotence, mixed files, refusals, backups, partial-write rollback and path
safety. They never invoke APT/systemctl or modify the host's `/etc`. The full
manager/broker artifact still requires normal release deployment and runtime
validation; a cross-build or fixture is not a production deployment test.

After an approved release, validate log filtering, Tor version/ports, LND sync,
and APT status. Do not infer an upgrade failure solely from a missing log panel.

## Format reference

Ubuntu 22.04's [systemd.time documentation](https://manpages.ubuntu.com/manpages/jammy/man7/systemd.time.7.html)
specifies the UTC calendar spelling and fractional seconds used for compatibility.

APT's [sources.list documentation](https://manpages.debian.org/bookworm/apt/sources.list.5.en.html)
defines line/deb822 formats, disabled entries and consistency requirements for
options such as Signed-By across the same URI and suite.
