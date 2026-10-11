# 0.5.44: Tor recovery on existing LND layouts

Status: focused regressions passed; PR remains draft pending release-path
integration coverage. No production deployment or production service restart.

## Scope and cause

The 0.5.43 helper refused a running native LND without an explicit `--lnddir`.
That is a valid existing-node layout: Linux LND defaults to the account's
`.lnd` directory. The refusal occurred before checking encryption or Tor
publication; the UI's generic encrypted-key guidance was not a diagnosis.

The helper now obtains the running process's UID from `/proc/<pid>/status`
and resolves its account home with `pwd.getpwuid`, not the root helper's HOME.
Ambiguous UIDs, unknown accounts, relative and environment-expanded paths fail
closed. Explicit absolute paths, alternate config files (`--configfile` and
`-C`), configuration-file `lnddir`, CLI precedence and existing directory
symlinks are covered. There is no directory scanning or credential copying.

Upstream semantics checked:

- [LND configuration selection and defaults](https://github.com/lightningnetwork/lnd/blob/v0.21.4-beta/config.go)
- [btcutil Linux account-home resolution](https://github.com/btcsuite/btcd/blob/btcutil/v1.1.5/btcutil/appdata.go)

The modal separately freezes its source/target versions for each operation;
status polling no longer rewrites the completed transition as `new -> new`.
Reopening an already-running operation does not invent its original version.

No code was added to restart LND or modify its unit, configuration, TLS,
permissions, wallet or onion key. Existing recovery/release safeguards remain.
Encrypted keys still require an operator decision; this PR does not decrypt them.

## Baseline and red/green evidence (2026-10-11)

- Base: `25f7298d` (0.5.44 version preparation; product code from 0.5.43).
- Ubuntu 24.04 amd64 disposable lab: original 19 Python recovery tests and
  4 shell-flow tests passed. Eight new runtime-configuration tests against the
  original helper produced four errors with `explicit absolute lnddir required`
  and one unsafe-unexpanded-path assertion failure. The fixed suite, including
  two additional root/short-option cases, passes all **29 tests**.
- Browser regression against the actual `0.5.43-Beta` component failed because
  the modal changed from `v0.4.9.12 -> v0.4.9.14` to `v0.4.9.14 -> v0.4.9.14`.
  The same test passes with this change in Edge/Windows, in English and
  Portuguese. It covers completion, visible recovery warnings, exactly one
  start request, another operation and reopening an active operation.

Commands (backend root unless otherwise noted):

```sh
python3 -B -m unittest discover -s scripts -p test_lnd_onion.py -v
python3 -B scripts/test_tor_upgrade_exit.py -v
LOS_ONION_DISPOSABLE=1 python3 -B scripts/test_lnd_onion_tor.py
go test ./...
go vet ./...
```

Windows amd64 full Go tests/vet passed. A Linux amd64 privileged test binary
was cross-compiled and **executed as root in the disposable VM**: all 10
`TestTor|TestClientTor` tests passed, including installation of the updated
embedded helper and refusal of writable or symlinked privileged targets.
This is native test execution, not a claim based solely on cross-compilation.

UI commands (from `lightningos-light/ui`):

```sh
npm ci --no-audit --no-fund
npm run build
node --test tests/rebalance-eligibility.test.mjs
node --test tests/tor-upgrade.browser.test.mjs
```

Browser test requires Playwright; `LOS_PLAYWRIGHT_MODULE` can point to an
already installed copy. `LOS_BROWSER_CHANNEL` defaults to `msedge`.
`LOS_TOR_COMPONENT_REF=0.5.43-Beta` selects the original component for the red
test. Every API request is intercepted locally; no node is contacted.
Build passed with locked Vite 6.4.3, with the existing large-chunk warning.
An initial build used stale local node_modules; dependencies were subsequently
reinstalled from the lockfile and the build/browser tests repeated successfully.
Eight existing UI eligibility tests passed. Package and lock files unchanged.

## Real LND/private Tor lab

VM: `los-disposable-0542-logs`, Ubuntu 24.04/amd64. Existing system LND and
Manager remained inactive as in the boot baseline. Production and LOS-TEST2
were not changed.

A separate non-root account ran an unfunded LND 0.21.4 with no directory/config
flags, its default account-home `.lnd`, and `nochainbackend`. Balance and open/
pending channel counts were verified zero before the experiment. A private
Tor process provided isolated SOCKS/control ports, with network disabled.

After restarting only that private Tor process, recovery registered the same
onion. A second recovery preserved it, and repeated release was harmless.
The real LND PID, identity, configuration, TLS files and onion key hashes were
unchanged through recovery. Both private child processes were stopped after
the test. No secrets, hashes of private inputs, or public addresses are logged.

The test used real `/proc`, account lookup, configuration parsing, `lncli`,
onion-key reading and the Tor control protocol. Only the systemd fixture PID/
hook metadata and private Tor cookie location were substituted. The retained
fixture is under `/var/lib/los0544-validation`; the local driver is
`D:/Cache/Temp/los0544-native.py`. This is not a real APT/UI upgrade test or a
public-network inbound reachability test.

## Remaining validation / limitations

- Full 0.5.43 -> candidate UI upgrade and subsequent Tor package-update flow
  on the no-`--lnddir` installation, including delivery of the new helper.
- Full fresh/install_existing/ARM installation matrix was not rerun. Installer
  scripts and dependency requirements are unchanged, but they share this helper.
- Real encrypted-key, missing-user and namespace/relative-path installations
  were not tested; unsupported/ambiguous resolution is intentionally refused.
- The helper reconstructs configuration from the current file, as before; it
  cannot prove that an operator has not edited it since starting LND.
- Public-network onion reconnection timing was not measured in this test.

Keep the PR in draft until critical integration gaps are completed or the owner
explicitly decides on this PR's exceptions. Earlier release exceptions are not
carried forward.
