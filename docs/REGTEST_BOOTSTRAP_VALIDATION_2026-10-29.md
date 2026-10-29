# Regtest curl bootstrap validation — 2026-10-29

## Scope and baseline

`lo_regtest_bootstrap.sh` is a separate learning/development entry point. It
fetches an official source ref into a private temporary checkout and dispatches
only `install_regtest.sh --disposable-host`. It does not read a release catalog
or change the production bootstrap, production installers, Go or React source.
The already-tested read-only alias mapping from PR #355 is included.

Platform: Ubuntu 24.04 amd64, real systemd and native Bitcoin/LND, in an isolated
Linux namespace inside a disposable VM. Its network was separate from an
existing three-node laboratory. The actual machine scope had a 2000 MiB memory
limit. No production node or mainnet funds were used.

Before editing bootstrap behavior, the existing production bootstrap suite
passed all 16 tests. It passed the same 16 tests after the change. The new
bootstrap suite passed all 8 tests and the regtest helper suite passed all 22:

```bash
python3 -m unittest discover -s scripts/tests -p test_install_bootstrap.py -q
python3 -m unittest discover -s scripts/tests -p test_regtest_bootstrap.py -v
python3 -m unittest discover -s scripts/regtest-lab -q
bash -n lo_regtest_bootstrap.sh
```

The fixture tests cover explicit opt-in, default and exact source refs, repeated
dispatch, ignored production-installer overrides, wrong hostname, missing
systemd, production service refusal, invalid/failing refs and failure cleanup.
Fixture installers only record dispatch; the real installation checks below
provide separate integration evidence. Windows skipped the 8 Linux-only tests.

## Actual curl installations

- **Fresh:** an empty Ubuntu root with Git absent fetched the HTTPS bootstrap
  and piped it to Bash with the disposable flag. The real bootstrap installed
  Git, fetched commit `41b8bb711d82e411c6b08cb6f600d7f92f8ccd3c` and ran the entire
  installer. Go/UI builds, native setup, wallet initialization, funding, native
  channel/payment smoke tests and authenticated LOS payment/send checks passed.
- **Runtime:** Core 31.2, LND 0.21.5-beta and all seven enabled services were
  active. Each of the three nodes had two active channels and confirmed
  fictitious on-chain funds. Verified HTTPS/login/config aliases, wallet balances
  and channel reads matched native RPCs on Alice, Bob and Charlie.
- **Failure:** a real existing-node path was rejected before installation. A
  real nonexistent Git ref failed without changing the installed receipt or
  configuration/credential hashes; private bootstrap checkouts were removed.
- **Repeat/retry:** a second real curl installation from
  `78d3a5eb43070440e15b9dffd23b88bb948a069e` completed successfully after the
  failed-ref attempt. Native identities, LND PIDs, channel sets, configuration,
  passwords and TLS private-key hashes were preserved. Alias/API checks passed
  again and temporary source checkouts were removed.

The second commit only changes installer help relative to the first functional
candidate. Subsequent validation-documentation changes do not change runtime
code. The normal installer intentionally performs small regtest transfers on
each successful execution; balance equality is not the repetition invariant.

## Limits

Native WSL2 and Windows ARM were not exercised. The bilingual WSL2 preparation
guide cites Microsoft documentation and requires Ubuntu 24.04 amd64, WSL2,
systemd and a dedicated distribution. This is not a WSL1 or production
installation claim. No new Go/React behavior or production upgrade path was
introduced, so their build/test matrices were not repeated beyond the actual
lab installer builds. Browser feature workflows remain the separate regtest
validation matrix; this change tests installation and bootstrap dispatch.
