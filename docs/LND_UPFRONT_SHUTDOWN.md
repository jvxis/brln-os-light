# Optional cooperative-close destination (0.5.55 candidate)

## Operator guide

LND Config gains one collapsed section: **Cooperative close destination** /
**Destino do fechamento cooperativo**. Nothing is enabled automatically.

- **Do not set automatically:** remove the two global defaults, without removing
  destinations already negotiated for existing channels or per-channel choices.
- **LND wallet:** request a fresh wallet destination when opening channels with
  compatible peers. This remains a hot-wallet destination.
- **External wallet:** use a Bitcoin mainnet destination controlled and backed
  up by the operator. This applies to future incoming/outgoing channels unless
  explicitly overridden per channel. Incompatible peers may refuse new channels.
  Reusing a global address reduces privacy. Cooperative-close proceeds sent
  there are no longer spendable by the LND wallet.

Changing the setting requires password confirmation and restarts LND. The
existing transactional writer verifies readiness and attempts rollback on failure;
it reports incomplete recovery rather than claiming success.

This is a limited protocol safeguard, **not protection of every node fund**:
it does not retroactively protect channels, restrict Lightning payments, change
force-close outputs, or prevent a fully compromised host from performing other
operations. A compromised client can still request different per-channel options
for future channels if its permissions permit. Keep the host and macaroons secure.

## Installation origin

Bitcoin systemd versus Docker does not establish who installed LND. A native LOS
installation can switch Bitcoin sources. Consequently this control never infers
origin from the Bitcoin backend and does not change the existing UI detection.

New `install.sh` installations record `native-v1` in
`/etc/lightningos/installation-origin` only when that installer first creates LND
configuration and no existing LND data directory is present. The marker is
root-owned, mode 0644. Existing config, existing data, a pre-existing marker or
symlink never triggers automatic retroactive classification.

The Manager accepts a root-owned regular marker beside its config YAML, rejects
group/world-writable markers, and recognizes `existing-v1` only as read-only.
Missing, malformed or untrusted markers mean **unknown**, also read-only. No
HTTP endpoint changes installation origin. Old native installations require an
operator review and explicit root-level origin confirmation; do not mark an
install_existing node native to bypass this restriction. There is intentionally
no automatic upgrade migration of this marker.

The dedicated regtest panels remain read-only for LND configuration: the lab's
existing mount and disabled broker are not weakened. A test-only adapter applies
the same config builder/transaction to native Alice and restarts only that lab
service, using regtest addresses. This is not a claim that production mainnet
configuration APIs support regtest or that the regtest UI can edit config.

## Validation record — 2026-11-08

- Baseline: existing focused LND config tests passed before edits (Windows amd64).
- Focused builder tests: all modes, repeat execution, removal, unrelated options,
  network/address validation, malformed/duplicate options and unknown origin.
- `go test -p 1 ./...` and `go vet -p 1 ./...` passed on Windows amd64.
  Linux root tests additionally rejected writable and symlink origin markers.
- Shared config tests cover transactional recovery; the new endpoint is included
  in the own-reauth-scope test. Unknown origin cannot mutate; reads do not expose
  unrelated credential fields.
- Frontend: `npm.cmd ci --ignore-scripts`, `npm.cmd run build`; existing large
  chunk warning. Actual React card tested in Edge with mocked read API in EN/PT,
  390/1280px, collapsed initial state, read-only origin, external acknowledgement,
  acknowledgement reset and minimal wallet-mode payload. Not a full HTTP login
  and confirmation-modal end-to-end test.
- Linux: `los-disposable-regtest`, Ubuntu amd64, native LND 0.21.5-beta. Opt-in
  `TestUpfrontShutdownNativeRegtest` uses local coins only, opens/closes its own
  channels and restores original Alice config on completion. Initial test runs
  hit native close-fee negotiation limits at 2 sat/vB; the test uses 10 sat/vB.
  Application behavior was not changed to hide that failure.
- Final native run passed all four scenarios: wallet, external outgoing,
  external incoming, off. Non-matching close destinations were rejected for the
  expected upfront-shutdown reason; broadcast transaction outputs matched the
  committed address. Pre-existing channel addresses were unchanged. Final
  pending-open/close/force-close lists were empty and original config restored.

### Fresh native installation and actual browser follow-up

On 2026-11-08, `los-disposable-0555-upfront` (Ubuntu 24.04.3 amd64, 4 CPUs,
8 GiB RAM) started without LOS/LND. Source commit `038a34be` was archived with
SHA-256 `1db87bd957bb707b03189be006b04156c55e1500d4c61017ef32fc4733a4daf1`.
The complete `install.sh` ran with `ACCEPT_MIT_LICENSE=1` and
`LIGHTNINGOS_INSTALL_SOURCE=checkout`: exit 0, Manager/UI/broker installed,
broker self-test passed, native marker `root:root:0644` with `native-v1`.
LND 0.21.6-beta came from the installer's authenticated upstream manifest.

The unchanged `copy_templates` function was then exercised in private Linux
mount namespaces: fresh, repeated execution, existing config, existing data,
interruption after config creation, pre-existing origin marker and symlink
marker. Only fresh/repeated new-native initialization produced `native-v1`;
existing operator config and markers were preserved. These focused namespace
tests are not separate full install_existing installations.

For functional UI writes, that disposable alone was adapted temporarily to a
local Bitcoin Core 31.2 regtest backend and a zero-balance LND 0.21.6 wallet.
No production secrets, mainnet wallet, channels or payments were introduced.
The real installed Manager, hardened broker and Edge browser passed:

- Native origin visible; initial mode off; section initially collapsed.
- Missing own-scope reauthentication rejected (428).
- Incorrect confirmation password left the configuration unchanged.
- Correct password saved wallet mode, restarted native LND through the broker,
  and returned successfully; the same UI restored off mode.
- Stale revision rejected (409), invalid external address and absent external
  acknowledgement rejected (400).
- Explicit existing origin made the actual UI read-only and rejected POST (403),
  preserving both the config hash and LND PID. Native marker restored afterward.

Fixture issues corrected before the successful rerun: the installer already
creates a password file, so regtest uses its own separate password file; the
native option is `nobootstrap`, not `no-bootstrap`; an API-created browser session
needs a page reload to refresh the already-mounted auth gate. None required
changing production application code. Initial installer listener checks ran
before HTTPS was ready; subsequent actual HTTPS/browser checks succeeded. UFW
was inactive on the base image and the installer warned about this; this test
does not claim firewall protection.

Original installed LND/Manager configs were restored; private test records and
regtest data were retained. The disposable was shut down normally. No production
node was used and no release was published by this feature work.

Coverage boundaries: no native arm64/full install_existing run (those installers
were not modified and that origin is excluded), no old-Manager upgrade run
(no automatic provenance migration), and no live peer-without-support test.
The compatibility warning follows the upstream LND option contract; this PR
does not modify LND negotiation. Actual channel opening/closing and transaction
output assertions ran on LND 0.21.5; 0.21.6 was validated through real installed
configuration/restart/UI flows, not a repeat of the full financial matrix.

Repeat native test only in the named disposable lab, as root:

```sh
LOS_UPFRONT_REGTEST=1 ./server.test -test.run TestUpfrontShutdown -test.v -test.timeout 12m
```

The test refuses a non-lab hostname/marker or non-regtest RPC. It does not reset
wallets, delete data or close pre-existing channels. On failure inspect any newly
created test channel before retrying; cleanup restores configuration, not wallets.
