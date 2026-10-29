# Bitcoin Core 31.2 Store upgrade validation

## Result and scope

LOS 0.5.50 offers the pinned official Bitcoin Core **31.2** through the existing
Store **Stop → Start** procedure. Updating the LOS catalog does not replace a
running Core container. No production node was used as a regression fixture and
no release was published. Native regtest binaries and production installation
scripts were not changed.

Baseline: `e46fbbe30e3e9eb1d6957572ea96433e651195ae` (Core 31.1).
Core lifecycle functional source: `ac891b73`; UI/docs source: `3790ccf9`;
strengthened verifier tests: `b98adb2f`; lifecycle Audit source: `e23fd9e0`.
All source references are commits in `jvxis/brln-os-light`.

The broker continues to build its own image from official archives, checks the
embedded architecture checksum against the authenticated checksum bundle, and
requires three distinct trusted signatures. Trusted key fingerprints and pinned
Debian base-image digests were preserved. The closed list accepts the previous
local 31.1 runtime for observation and a stopped-runtime transition; arbitrary
images and old running-runtime replacement remain rejected.

Core shutdown uses Docker timeout `-1`: wait for graceful completion, without
scheduling SIGKILL. Request deadlines still bound the caller's wait. Start verifies
mainnet, RPC version 310200 and local RPC/ZMQ endpoints before returning success;
a failed verification must not mark the catalog version applied. The procedure
does not promise automatic binary downgrade or undo database changes.

## Environment and actual integration checks

Ubuntu 24.04 amd64; Docker 29.1.3, Compose 2.40.3; native LND 0.21.5-beta.
One disposable VM was used, with 4 GiB RAM. A dedicated sparse ext4 test volume
exercised the real custom-mounted-storage safeguards; no guard was bypassed.

| Scenario | Evidence/result |
| --- | --- |
| Baseline | Full Linux `go test ./...` and `go vet ./...` passed before changes. The original official 31.1 image was built through the old broker and passed its real RPC-authentication gate. |
| Official 31.2 image | Actual broker download, trusted-key verification, signature quorum, archive checksum, local Docker build, daemon version and generated RPC authentication gate passed. Wrong RPC credentials were rejected. |
| Existing-state Store upgrade | An older-source lifecycle helper started the official 31.1 app against a dedicated mounted volume. The candidate observed it without legacy-migration confirmation, refused replacement while running, then completed actual Stop/Start to 31.2. Mainnet RPC version changed 310100 → 310200; config, stored RPC credentials, volume identity and chain tip matched before/after. This mainnet fixture had networking disabled and no funds. |
| First installation | A separate empty enrolled directory/credential store started 31.2 through the real lifecycle, passed mainnet/version/RPC/ZMQ checks and repeated Stop/Start. No existing wallet or chain was imported. |
| Slow shutdown | A real Compose fixture took 14 seconds to handle SIGTERM. The production Core Stop path waited beyond the former ten-second cutoff and the process exited with code 0. |
| Cancelled request | A shortened request context returned failure while the graceful Docker stop continued. The process subsequently exited with code 0; repeated Stop succeeded. Cancellation did not become a successful upgrade or forced kill. |
| Missing mounted volume | After checking the exact ext4 loop-image identity, the owned test volume was unmounted. Start was refused and no container started. The image/data were retained privately, unmounted. |
| Release-verification failures | The real generated verifier ran with an invalid expected checksum and, separately, an invalid signature fixture. Tests required reaching the intended cryptographic refusal, rather than accepting a download failure as a pass. The existing trusted image ID and attestation were unchanged. |
| Funded regtest and LND | A separate Docker 31.1 regtest chain funded two dedicated native LND processes and an active channel. After replacing only the Docker backend with 31.2, both existing LND processes predated the replacement, regained sync, retained the active channel and completed a new Lightning payment and a confirmed on-chain send. A successful Lightning payment before replacement was independently checked in native payment history. |
| Regtest reconnect timing | A block mined immediately after Core startup preceded subscriber readiness. Mining a fresh block after reconnect restored the regtest sync flags without restarting LND. This laboratory requires manual block generation; no automatic financial retries were used. |
| Browser | Actual Edge, English and Portuguese: installed/available versions, instructions, confirmation/cancel, no automatic application on page load, Stop/Start dispatch, failed startup/retry, CSRF headers and unchanged Stop behavior for another app. These browser tests use routed API fixtures; Docker/lifecycle integration was tested separately, not through a production authenticated browser session. |
| UI Audit | Dedicated PostgreSQL tests verify success/error, HTTP status and target catalog version for install/start/stop, response preservation and exclusion of request passwords, cookies and raw error details. Historical Audit/API security regression also passed. Broker audit remains independent. |
| Final regression | Full Linux backend tests/vet, focused Core tests, frontend `npm ci`/`npm run build`, and the bilingual browser smoke passed. Windows focused tests supplement the Linux checks; Linux-only tests are not claimed as Windows coverage. |

The shutdown regression was reproduced against the original ten-second policy.
An intermediate startup check incorrectly ran after Stop; the real lifecycle
test caught it. A focused regression now verifies that successful shutdown does
not require a running container or startup probe.

The lab's native regtest Core initially conflicted with the Store's fixed ZMQ
ports. A separate Docker-daemon namespace failed to initialize and was abandoned.
For the standard-port mainnet fixture, the native regtest stack was temporarily
paused; systemd dependencies also stopped its LND/panel units. All original units
were restored and checked healthy with their previous balances/channels. The
additional funded test used separate ports and processes and was stopped after
acceptance. No additional VM was started.

An initial 192 MiB limit was too small for a supplementary LND wallet's initial
creation and caused a fixture OOM. Its incomplete, unfunded directory was retained;
a fresh fixture with adequate creation memory completed the test. No user wallet
was reset or treated as disposable fixture state.

## Reproduction commands and limitations

From `lightningos-light/` on an owned disposable Linux machine:

```sh
go test ./...
go vet ./...
LIGHTNINGOS_BITCOIN_RPCAUTH_GATE=1 LIGHTNINGOS_BITCOIN_RPCAUTH_PREPARE_IMAGE=1 \
  go test ./internal/privileged -run '^TestBitcoinCoreGeneratedRPCAuthFunctionalGate$' -count=1 -v
LIGHTNINGOS_BITCOIN_VERIFY_FAILURE_GATE=1 \
  go test ./internal/privileged -run '^TestBitcoinCoreReleaseVerificationFailurePreservesExistingImage$' -count=1 -v
# Use a separate los_audit_test_* database; these integration tests recreate its table.
LOS_AUDIT_TEST_DSN='<dedicated private DSN>' \
  go test ./internal/server -run 'TestBitcoinCoreOperationAudit|TestAuditSecurityHistoricalMigrationAndAPI' -count=1 -v
```

The negative verifier additionally requires the marked owned regtest laboratory;
it must never be enabled on production. The native upgrade/slow-stop/funded tests
were controlled disposable-fixture exercises, not additional public app APIs.

From `lightningos-light/ui/`, after starting Vite on port 5189:

```sh
npm ci
npm run build
node tests/bitcoin-core-store-upgrade-ui-smoke.mjs
```

The browser script uses Playwright/Edge; `PLAYWRIGHT_MODULE`, `LOS_UI_TEST_URL` and
`SMOKE_OUTPUT` can select an external test dependency, dev URL and artifact folder.

Native arm64/32-bit runtime, a fully synced mainnet-sized blockchain, actual
Electrs/Mempool/BTCPay deployments, exhaustive OS/disk/network failures and a
production LOS internal UI upgrade were not exercised here. The installer/shared
upgrade paths are unchanged. Architecture hashes were updated for the three
existing catalog architectures; amd64 is the platform with native integration
evidence. Existing install/legacy-storage/security tests continue to run in the
full suite. This report does not turn skipped opt-in gates into passed tests.

References: [official 31.2 notes](https://github.com/bitcoin/bitcoin/blob/v31.2/doc/release-notes.md),
[official checksums](https://bitcoincore.org/bin/bitcoin-core-31.2/SHA256SUMS),
[user procedure](../README.md#updating-store-managed-bitcoin-core-0550),
[procedimento em português](README-PT-BR.md).

## Português

O upgrade segue **Parar → aguardar → Iniciar** na loja, sem desinstalar. A imagem
31.2 mantém a verificação de assinaturas/checksum e o volume existente. O Core
não é substituído apenas por atualizar o LOS. O encerramento aguarda sem SIGKILL
automático; falhas ou timeouts não equivalem a uma atualização concluída.

Passaram instalação nova, troca real 31.1 → 31.2 com configuração/credenciais/
identidade do volume preservadas, encerramento demorado/cancelado e rejeição real
de assinatura/checksum inválidos. Dois LNDs nativos mantiveram o canal e voltaram
a pagar em Lightning e on-chain com o backend Docker novo. No regtest, foi
necessário um bloco novo após a reconexão. A interface foi exercitada em ambos
os idiomas, e os eventos de instalação/início/parada entram no Audit sem segredos.

Os testes nativos foram em amd64 e ambientes descartáveis. Os limites acima
permanecem explícitos; não houve teste com fundos reais, deploy em produção,
publicação de release ou promessa de downgrade automático.
