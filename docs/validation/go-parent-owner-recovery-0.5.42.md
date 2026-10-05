# Go parent-owner recovery validation — 2026-10-05

Scope: standalone `scripts/recover-go-parent-owner.py`, not invoked by any
installer, updater, Manager or broker. Source base: release branch commit
`7164841fb057902b2fd085d7a4287eca08614699`. Script pinned at
`f541bf35c121311f72bc3bb2d2e9db54d7e21b31`.

## Platform and baseline

- Disposable `los-disposable-0540-upgrade`, Ubuntu 24.04.3, native amd64,
  Python 3.12, systemd; no funded wallet or channels.
- Official 0.5.41 was initially installed, parent root:root 0755.
- Prepared official 0.5.40 from verified checkout
  `eee45ff316382f7f1948b8a0c0dd988718bfa539` with its own helper. This was lab
  setup, not counted as a UI transition.
- Explicitly injected admin:admin ownership on `/opt/lightningos` only. This
  reproduces the known state; it does not establish its historical origin.

## Actual installed updater transition

1. Authenticated to the installed 0.5.40 Manager using the same HTTPS API as
   the UI, verified local CA and CSRF, then started the public 0.5.41 upgrade.
2. Observed `Go preparation failed; application was not replaced.` before
   cutover. Manager PID and hashes of app binaries/version/configuration were
   unchanged. This is the failure baseline.
3. Downloaded the commit-pinned recovery script from the documented URL,
   verified SHA256, ran `--check`: exit 2, ownership unchanged.
4. Ran `--apply`: root:root 0755. Compared owner/group/mode of **all descendants**,
   app/configuration hashes and Manager PID: unchanged. Only the parent changed.
5. Repeated `--check` and `--apply`: successful no-ops.
6. Retried through the installed 0.5.40 Manager/API/broker/helper; upgraded to
   public official 0.5.41 commit `0b19a62a150261c219ff63759b8919c023b28345`.
7. During that real upgrade, `--apply` refused because the updater was active.
8. Final Manager, broker socket and PostgreSQL active; broker self-test passed.
   LND remained uninitialized/auto-restarting and bitcoind inactive, as in the
   unfunded lab baseline. This is not a funded Lightning health test.

No browser clicks were exercised; these were real installed API/updater calls,
not a new helper run against an old source tree.

## Automated scenarios

Command, run only as root in the disposable VM:

```bash
python3 -I scripts/tests/test_go_parent_recovery.py -v
```

Passed: 17 filesystem scenarios in separate private mount namespaces, each
with a tmpfs over `/opt`: legacy check/apply/repeat; already healthy no-op;
failed fchown and retry; foreign owner; wrong group; unsafe target mode;
unsafe ancestor; unsupported version; missing binary; target/UI/version
symlinks; version FIFO; oversized version; real extended POSIX ACL; bind mount;
missing target. Descendant sentinel content, mode and ownership were preserved.

Updater active/failed-query/empty-state guards were additionally tested with
mocked systemctl results, asserting directories were not opened. An actual
running updater and non-root CLI invocation also refused in the lab.

Initial fixture harness failed because its tmpfs hid the script checkout under
`/opt`; loading the module before the mount fixed the harness. Final tests passed
without weakening guards. Python syntax and `git diff --check` passed.

## Boundaries and remaining coverage

- Native arm64, Ubuntu 22 and other distributions not exercised for this helper.
  Validated deployment scope is Ubuntu 24 amd64 and the exact recognized state.
- No fresh installer matrix: no installer/shared helper code or requirements
  changed; this recovery remains entirely opt-in.
- No Manager/backend/UI changes; full Go/UI suites are not asserted for this PR.
- No automatic permission migration, broad rollback or arbitrary target support.
- Private raw evidence remains in `/root/los-go-owner-recovery` on the disposable
  disk; no secrets or raw configuration were published.
