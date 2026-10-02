# Upgrade completion audit — 2026-10-02

This follow-up supplements the [September 30 baseline](go-upgrade-0.5.40-virtualbox-2026-09-30.md).
It does not replace that record or count waived native ARM tests as passed.

## Installed 0.5.33, rollback and browser upgrade

Environment: disposable `los-disposable-0540-upgrade`, Ubuntu 24.04/amd64,
without an initialized wallet or funds. Installed the official 0.5.33 source
at `12eb5b076572edd58fe00e71d172ca4d096fe144` using Go 1.24.12. Its installed
upgrade helper has the same published SHA-256 as 0.5.39:
`6a6d39d79d642d4565aba4778bd381d9f72e24d96d105d1e597b9b2eb6ee1a4c`.
The target application source was PR #211's `26dc9f5b`.

- The actual old Manager's authenticated/CSRF-protected upgrade endpoint
  started its installed helper. A historical snapshot without broker files and
  an inert, noncredential macaroon fixture with mode 0755 caused the intended
  credential failure. Both rollback invocations recovered 0.5.33; hashes of
  Manager, UI version and configuration matched the saved baseline. The new
  broker/socket survived, with recovery still available. PostgreSQL's activation
  timestamp was unchanged.
- After correcting the inert fixture to 0640, Firefox clicked the real upgrade
  confirmation. The backend start request returned HTTP 200, status reported
  running, and the upgrade completed at 0.5.40. The browser reauthenticated after
  Manager restart and observed the completed upgrade. Authentication sessions
  are not preserved across that restart; this is not a claim of silent login
  reconnection.
- Only `wizard/status.wallet_exists` was overridden in the browser to expose
  the dashboard on this uninitialized VM. The upgrade start, status, logs,
  installed helper and backend execution were real. Reloading the new UI
  returned to the real empty-wallet wizard.

As in the previous audit, unpublished releases were represented by a
loopback-only Git/API HTTPS transport with a temporary trusted CA. Source and
helper verification remained enabled. No public release or tag was created.
The fixture's inert macaroon was subsequently archived outside the LND paths.

## Root archive permission regression

The first assisted upgrade attempt on LOS-TEST2 exposed a trusted-checkout
failure before installed binaries or services were replaced. `git archive`
emits group-writable entries by default (`tar.umask=0002`), and root extraction
preserved them. The Go preparer's root-file validation correctly refused the
resulting 0664 asset.

Commit `1ddbdbcac857299bfd9b0dd76042b738100eed1e` makes extraction explicitly
use umask 022 and `tar --no-same-owner --no-same-permissions`, inside the existing
root-owned source directory. The helper's broker digest was updated.

`TestTrustedCheckoutArchiveUsesSafeRootPermissions` runs the actual embedded
extraction block against a real Git archive on Linux as root. Both caller
umasks 022 and 077 failed before the fix (0664/0775) and passed afterward
(0644/0755), including preservation of executable files. No permission check
was weakened.

On the separate disposable Ubuntu/amd64 VM, Go 1.26.8 passed:

```sh
go test -p 2 ./... -count=1
go vet -p 2 ./...
go build -p 2 -o <temporary-output> ./cmd/lightningos-manager
```

Shell syntax checks passed for all three installers and the upgrade helper.
The corrected commit then completed a full trusted-checkout upgrade in the
disposable upgrade VM before it was used for the LOS-TEST2 retry.

## Assisted LOS-TEST2 upgrade

Source: installed 0.5.39, Ubuntu 24.04/amd64, `install.sh` origin, initialized
and unlocked wallet, three active channels, chain/graph synchronized, remote
Bitcoin source. Target: PR #211 commit `1ddbdbca`, 0.5.40-Beta. The owner
authorized completion of these tests. Fault injection was never used here.

Because 0.5.40 was not publicly released, this used the root-only, commit-pinned
`--trusted-checkout` operator path from the official GitHub branch, after the
same commit passed the disposable lab. No GitHub/DNS/CA transport fixture was
installed on LOS-TEST2. The browser remained authenticated on the real dashboard
before launch and observed the transition, reauthenticated after restarts,
confirmed current 0.5.40 with `running=false`, and logged out. This is an
assisted upgrade, not a claim that an unpublished release was discovered through
the public UI. The independent 0.5.33 browser test above covers the UI start.

The corrected upgrade exited zero. Independent postchecks passed:

- Manager and broker socket active; broker self-test passed as the service user.
  Running Manager bytes matched the installed binary. Both Manager and broker
  reported Go 1.26.8 build metadata.
- LND and PostgreSQL retained their exact PID and activation timestamp. `lnd`
  and `lncli` hashes were unchanged. Both configured notification PostgreSQL
  connections authenticated successfully; secrets file bytes were unchanged.
- Real credential convergence returned `status=ready changed=false`, with the
  transaction committed and the existing restricted credential bytes preserved.
  Manager macaroon: root:lightningos 0640; state: root:root 0600; admin macaroon:
  lnd:lnd 0600. This exercises real LND RPC verification and idempotent reuse
  with an initialized wallet. It does **not** claim first-time baking of a new
  credential; the existing credential was deliberately preserved.
- Eight authenticated GET checks returned HTTP 200 with TLS verification.
  Health was `OK`; LND remained unlocked and synchronized to chain and graph,
  with three active, zero inactive and zero pending channels. Bitcoin RPC was
  reachable and the selected source remained remote. Upgrade status showed
  current 0.5.40 and no running upgrade.

The postcheck harness was corrected to query Bitcoin source through its actual
`/api/bitcoin/source` contract and to avoid `go version -m | head` SIGPIPE under
`pipefail`. All underlying preservation checks had already passed; the corrected
complete postchecks exited zero. No application behavior was changed for these
harness corrections.

No payment, channel open/close, LND binary upgrade, PostgreSQL major migration,
or native ARM test was performed. Those are not claimed by these results.

## Evidence handling

Disposable evidence is retained under `/root/los-0540-final-audit`: safe browser
events, confirmation/completion screenshots, the failed transition and retry,
and the full corrected trusted-checkout upgrade log/exit status. LOS-TEST2
baseline, original backup and upgrade logs are under
`/root/los-0540-validation`, accessible only to root. Raw backups/configuration
and credential-bearing files are not copied into this repository.

## Publication boundary

The mandatory bridge still requires a publication arrangement that old,
already-installed Managers understand. They read the original repository's
release list directly. Tests against a private transport do not prove behavior
of a public 0.5.40 release that has not yet been published. The proposed catalog
change is isolated from PR #211; repository organization awaits the owner's
choice. No repository, public tag or release was created during this audit.
