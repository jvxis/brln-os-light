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

Remaining release gates: complete fresh native installer validation of the origin
marker (including retry/interruption and unchanged existing layouts); full
authenticated UI/API integration for a verified native origin; unsupported-peer
behavior and the shipped 0.21.6 runtime. These are not passed tests. Keep the PR
in draft until critical affected paths are exercised. No production node was used
as a regression fixture and no release was published by this feature work.

Repeat native test only in the named disposable lab, as root:

```sh
LOS_UPFRONT_REGTEST=1 ./server.test -test.run TestUpfrontShutdown -test.v -test.timeout 12m
```

The test refuses a non-lab hostname/marker or non-regtest RPC. It does not reset
wallets, delete data or close pre-existing channels. On failure inspect any newly
created test channel before retrying; cleanup restores configuration, not wallets.
