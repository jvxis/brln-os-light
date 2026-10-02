# LND 0.21.4-beta compatibility review

Reviewed on 2026-10-01 against LOS release base `df59a177` (0.5.40).

## Decision and scope

No blocking incompatibility was found in the LOS call sites reviewed or in the
checks below. Update the managed first-install `install.sh` target from
`0.21.3-beta` to `0.21.4-beta`, together with its existing regression assertion.
This is a bounded compatibility assessment, not validation of live channel
operations against the new daemon.

`install_existing.sh` and `install_existing_pi.sh` adopt the user's native LND
and have no LND version pin to synchronize. Both already provision the same
authenticated upgrade helper. Existing installations continue to use the
separate LND upgrade flow; this change does not require rerunning an installer.

## Upstream evidence

- [Published release](https://github.com/lightningnetwork/lnd/releases/tag/v0.21.4-beta):
  published 2026-10-01, not marked as a prerelease, commit `390b2d2`.
- [Release notes at the immutable version tag](https://github.com/lightningnetwork/lnd/blob/v0.21.4-beta/docs/release-notes/release-notes-0.21.4.md).
- [Changes from 0.21.3](https://github.com/lightningnetwork/lnd/compare/v0.21.3-beta...v0.21.4-beta).
- The release page reports no database migrations. The notes include a fix to
  legacy feature decoding during native SQL graph migration; this does not
  replace testing of a particular user's existing database.
- Linux amd64, arm64 and armv7 archives and the signed manifest are published.
  The amd64 archive's authenticated SHA-256 is
  `beffe4949a5e46410d90c0e6d020a1b685468440b0004fc4d59740a6291178fb`.

## LOS compatibility assessment

| Upstream change | LOS usage and assessment |
| --- | --- |
| New `LEGACY` commitment channels are rejected. | Normal, selected-UTXO, batch and balanced openings leave commitment selection at `UNKNOWN`/zero. LND still derives its default from the peers' features. No active LOS caller requests `LEGACY`; enum value zero is not `LEGACY` (one). Existing legacy channels remain supported upstream. |
| Explicit wire `channel_type` is required. | LOS calls the LND RPCs, not the peer wire protocol. LND supplies the explicit negotiated type. Opening with an old peer that omits it may now fail as an intentional upstream restriction. |
| Confirmation-controlled WalletKit leases. | `LeaseOutput` and `FundPsbt` gain optional fields; zero retains timed leases. LOS supplies expiration or fee parameters and leaves the new fields unset. The protocol comparison shows no removal or renumbering of the existing fields used by LOS. |
| Duplicate BOLT11 payment-hash fields are rejected. | LOS delegates invoice decoding to `DecodePayReq`; such invoices now produce a validation error. Standard invoices do not need a LOS change. |
| HTLC replay, AMP, invoice processing, graph sync and breach handling fixes. | These are daemon-side corrections. LOS does not implement an HTLC settling interceptor. Optional external apps using an interceptor were not exercised. |
| New `INVOICE_INTERCEPTOR_ERROR` enum value. | Additive router RPC value. Existing LOS stubs accept unknown protobuf enum numbers; the HTLC failure label can appear as a numeric value until a future stub refresh. No generated files were edited. |
| Deprecated Hop fields / `sat_per_byte`. | Their removal is announced for 0.22, not 0.21.4. LOS opening and PSBT requests already use sat/vbyte. |

Relevant local code: `internal/lndclient/client.go`, `utxo_manager.go`,
`onchain_preview.go`, `mesh_sign.go`, `opreturn.go`, and
`internal/server/balanced_open_service.go` (under `lightningos-light/`).

LND is installed from upstream binaries. The Go version used to build those
binaries does not require changing the Go toolchain used to build the LOS
Manager. This change leaves the LOS module and generated RPC stubs unchanged.

## Validation performed

Environment: disposable VM `los-disposable-0540-fresh`, Ubuntu 24.04, Linux
amd64, Go 1.26.8, 4 GiB RAM. This VM has no initialized wallet or channels.
Candidate source was the release base plus the two changed files.

1. `bash -n` passed for all three installers and the canonical LND helper.
2. `go test -p 2 ./... -count=1` passed, including LND client, server,
   installer, privileged broker, configuration and reporting regressions.
3. `go build -p 2 -o <lab-output>/lightningos-manager
   ./cmd/lightningos-manager` passed.
4. The unmodified authenticated helper with `--version 0.21.4-beta
   --verify-only` downloaded and verified the real release: five pinned
   signatures (bhandras, suheb, ziggie1984, ViktorT-11 and georgetsagk), archive
   SHA-256 and executable version passed. Installed binary hashes stayed equal.
5. The actual `install_lnd` function and its utility functions were extracted
   from the candidate installer and run in a private Linux mount namespace with
   an empty tmpfs over `/usr/local/bin`. It exercised `--install-new` using the
   unchanged helper and real upstream artifacts. Both binaries reported
   `0.21.4-beta`, with root ownership and mode 0755. Repeating `install_lnd`
   reused the installation and preserved both hashes. LND and WalletKit CLI
   help commands also succeeded.
6. After leaving the namespace, the VM's original LND/lncli hashes were
   unchanged; Manager and broker socket were active. No funded node was changed.

The lab used `umask 022`, `GOMAXPROCS=2`, `GOTOOLCHAIN=local`, `GOENV=off` and
`GOWORK=off`. This review exercised the LND installation step, not a full
first-install rerun. Runtime database migration, wallet unlock, payment,
channel opening/closing, optional App Store applications, and ARM execution
were not validated against 0.21.4. These limits must not be reported as passes.
