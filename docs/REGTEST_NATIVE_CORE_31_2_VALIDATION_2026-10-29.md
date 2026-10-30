# Native regtest Bitcoin Core 31.2 validation

The developer `install_regtest.sh` profile now provisions **native Bitcoin Core
31.2** for a new laboratory and updates an existing owned laboratory to the same
version. Alice, Bob and Charlie remain separate native LND 0.21.5-beta nodes.
The production installers and Store runtime are not used by this profile.

Functional native source: `6bb04157dcafd8225852bc4885d8eba75567ec02`.
Combined with the integrated PRs #351, #352 and #353:
`1aa07bb3490e6a8a23a4f92b2001afc7fbafb415`.

The official amd64 archive is pinned to SHA-256
`2f38996afaa74dfa469a23bf4727887133dfc09784df1f21ddf1c74d0dc82300`.
Executables are copied to a staging file and atomically renamed into place.
A failed copy preserves the installed executable. Runtime reconciliation compares
the installed bytes with the live `/proc/<pid>/exe`: the Bitcoin process is
restarted when required, not merely when the file inode changes on a rerun.

The Bitcoin service's systemd dependencies also stop LND and LOS panels. The
installer captures the previously active dependent units and restores them after
the controlled Core restart. Unchanged native binaries retain their process IDs.
Wallets, chain data, aliases, configuration and credentials are not reset.

## Evidence

Tests used Ubuntu 24.04 amd64, native Core/LND/systemd/PostgreSQL and an isolated
bootable Linux fixture inside one disposable VM. No additional VM or production
node was used. The fixture had its own root, process/network namespace and
database state; previous fake state was retained privately before first install.
OS prerequisites and download/toolchain caches were reused, not wallet data.

| Scenario | Result |
| --- | --- |
| Baseline | Existing Core RPC version 310100, three identities and existing channel points captured privately; original 19 Linux tests passed. |
| First installation | Actual top-level installer created an empty three-node laboratory with native Core 31.2, new wallets/CA/databases, channel triangle and all normal native/LOS financial smokes. RPC confirmed 310200. |
| Actual Linux reboot | Core remained 31.2; all three LND wallets unlocked, channels reconnected and all three panels started automatically with their original identities. Verified HTTPS/login passed for each panel. |
| Existing 31.1 laboratory → combined candidate | An earlier funded regtest generation was restored while the isolated Linux fixture was stopped. The actual installer then built/deployed the combined source and updated Core to 31.2. The old chain hash at its original height, wallet identities, channel points, configuration, passwords, CA and TLS private keys matched. Core's PID changed and formerly active dependent services recovered. |
| Repeated native preparation | Rerunning `provision.sh` retained all native/panel PIDs and credentials; no duplicate channels, leases, pending channels or limbo remained. |
| Financial/API integration | The actual installer exercised all three native and LOS Lightning payment pairs, fresh financial confirmation and an on-chain send with mined confirmations. All three panels passed authenticated HTTPS and native-balance/channel checks. |
| Focused regression | 21/21 tests passed on Linux, including real `/proc` identity, restoring previously active dependencies, atomic replacement of a live executable and failed-copy preservation. Windows: eight passed, 13 explicit Linux-only skips. |
| Integrated application regression | Full Linux `go test ./...` and `go vet ./...` passed. The top-level installer built the combined Go Manager and React UI through `npm ci` and `npm run build`. |

The first-install run took approximately three minutes, with a 1.6 GiB peak and
no swap; the existing-state integration run took approximately two and a half
minutes, with a 1.2 GiB peak and no swap. Memory/CPU were bounded for the Linux
fixture. It was stopped after acceptance and its test data retained.

The isolated OS initially retained root-only directory permissions that prevented
PostgreSQL's non-root initializer from executing. Correcting only that owned
fixture directory allowed database initialization; no installer guard was relaxed.
An idle regtest chain was refreshed with a manually mined block before baseline.

## Scope and commands

```sh
python3 -m unittest discover -s scripts/regtest-lab -v
sudo bash lightningos-light/install_regtest.sh --disposable-host
sudo python3 /opt/los-regtest/verify-los.py --setup-only
sudo python3 /opt/los-regtest/control.py status
# From lightningos-light/:
go test ./...
go vet ./...
```

This profile supports Ubuntu amd64; native arm64 is not claimed. No production
install/shared-upgrade code changed. The tests do not claim a production UI
upgrade, complete mainnet IBD or revalidation of every Lightning workflow.
The separate Store/Docker validation remains documented in
[the Store report](BITCOIN_CORE_31_2_STORE_VALIDATION_2026-10-29.md).
No release publication is required to use the reviewed development branch.

## Português

O instalador regtest agora cria o laboratório com **Bitcoin Core nativo 31.2**
e atualiza laboratórios existentes para a mesma versão. A troca dos executáveis
é atômica, e o processo Bitcoin antigo é reiniciado quando necessário. Os LNDs e
painéis anteriormente ativos são restaurados após esse reinício.

Passaram instalação nova, reboot real do Linux isolado, atualização de um
laboratório 31.1 com dados existentes, repetição sem reinícios desnecessários,
pagamentos Lightning pelos três nodes e envio on-chain confirmado. Carteiras,
canais, configuração, autenticação, CA e chaves TLS foram preservados. Os 21
testes Linux e as verificações completas de Go passaram. O código integrado
inclui as PRs #351, #352 e #353; os instaladores mainnet não foram alterados.
