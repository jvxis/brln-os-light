# Bitcoin Core storage reserve on restart

The App Store calls the broker's storage enrollment guard before starting
Bitcoin Core. Previously that guard required 10 GiB free on every call,
including reuse of an already enrolled, populated volume. A pruned node with
8.37 GiB free therefore failed with `bitcoin_storage_failed`, although no new
blockchain copy was being installed.

The storage policy distinguishes two cases:

| Operation | LOS free-space reserve |
| --- | --- |
| Initial enrollment, including an unregistered or incomplete volume | 10 GiB |
| Reuse of an existing enrollment whose volume identity is verified | 1 GiB |

The restart reserve is an LOS operational policy, not a claim that 1 GiB is
enough for every database operation. Bitcoin Core retains its own startup,
periodic and write-specific disk-space checks. The Core 31.2
[startup and periodic checks](https://github.com/bitcoin/bitcoin/blob/v31.2/src/init.cpp)
remain authoritative for its actual operation. Low disk usage alerts still
matter; this fix does not make a nearly full filesystem healthy.

The lower reserve requires all of the following before any volume marker is
created or repaired:

- Existing real directories without symlink components.
- A root-owned, private broker storage metadata directory.
- Root-owned metadata files with mode 0600, a matching data-directory path
  and a valid managed storage ID.
- A real volume marker, owned by root with group 101 and mode 0640, whose ID
  exactly matches the enrollment.

Missing markers, different disks, partial registration, writable metadata,
container-owned markers and symlinks cannot select the lower reserve. Existing
canonical-path and mounted-filesystem checks still run. Pruning does not alter
these identity requirements; the same reuse rule applies to a verified full
node volume.

For an actual low-space rejection, the broker returns the static
`bitcoin_storage_space_low` diagnostic describing both reserves. Unexpected
failures still return the generic sanitized storage error; internal paths,
credentials and arbitrary underlying error text are not exposed.

## Validation

Validated on Ubuntu 24.04 amd64 with Go 1.26.10 and Docker 29.1.3:

- The new ownership/identity regression failed against the original code;
  the corrected root regressions passed without skips. Missing/different
  markers, container-owned or writable files, partial metadata and symlink
  volumes/ancestors were refused.
- A real pruned Core 31.2 container was started and gracefully stopped on an
  isolated 12 GiB tmpfs volume. After remounting that same volume with an
  8 GiB limit, original storage enrollment failed. Corrected enrollment and
  two real Docker stop/start cycles passed, preserving configuration,
  credentials, storage IDs, a data sentinel and the chain tip.
- A 512 MiB existing volume was still refused. Removing its broker enrollment
  at 8 GiB was refused without creating partial metadata. Custom storage
  passed preflight when mounted and was refused at the same path after unmount.
- The official Core archive checksum/signature verification and image
  attestation ran through the production image-build script. The harness ran
  that script within the disposable mount/network namespace rather than
  scheduling it in host PID1. Lifecycle/storage/configuration functions were
  called directly; this does not claim an end-to-end browser test.
- Full Linux root `go test ./...`, `go vet ./...` and native builds passed.
  Focused Windows broker/protocol regressions also passed.

The disposable regtest host lacks production's native Loop user/group. The
original full root suite's two Loop identity failures were reproduced against
unchanged code. Both original Loop checks and the complete corrected suite then
passed with those identities supplied **only in the unit-test process's mount
namespace**. No host account or test assertion was changed. Optional PostgreSQL
integration tests were not enabled.

The Docker node used an isolated, unfunded, disconnected mainnet genesis chain
with pruning enabled; this is not a synced mainnet database or financial test.
The filesystem test used tmpfs limits without allocating gigabytes of RAM.
Native arm64 and a manual App Store browser flow were not exercised. The full
31.1-to-31.2 binary transition was not repeated; image/lifecycle code was not
changed by this fix. Production and funds-bearing services were not fixtures.
