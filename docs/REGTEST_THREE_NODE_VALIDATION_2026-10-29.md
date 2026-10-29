# Three-node developer laboratory validation / Laboratório com três nodes

## English

The regtest installer now provisions persistent Alice, Bob and Charlie native
LND processes and independent LOS panels. Their default aliases are
`LOS-Regtest-Alice`, `LOS-Regtest-Bob` and `LOS-Regtest-Charlie`. Setup creates a
triangle of channels with initial liquidity on both sides, enabling direct and
two-hop payments without a fourth process or another VM.

Functional source: `52b9a4f2df9639d61c1559ae3c8bb1750883a155`.
This work builds on the UTXO inventory correction in PR #351. The Charlie changes
touch the regtest installer, lab helpers and documentation; production installers
and application Go/React code are unchanged relative to that parent.

Validation used Ubuntu 24.04 amd64, native Bitcoin Core 31.1, LND 0.21.5-beta,
real systemd and PostgreSQL. A separate bootable Linux container inside the
disposable VM provided an isolated first-install fixture. It had separate root,
PID, network and database state. Only one disposable VM was running; no physical
network configuration or production node was changed for this work.

| Scenario | Observed result |
| --- | --- |
| First installation through the actual top-level installer | Complete native setup, Manager/React builds, three wallets/databases, HTTPS login, channel triangle and normal payment smokes passed. |
| Repeated installation | Existing wallet identities, channel points, login/database credentials and native process IDs preserved; no duplicate setup channels. Operator-defined Charlie alias preserved. |
| Existing two-panel laboratory with an older Charlie fixture | Original Alice/Bob channels retained; missing triangle edges added; legacy Charlie database credentials adopted without rotation. |
| Real routed payment | Alice paid Charlie through Bob; normal LOS financial intent/password confirmation and native successful two-hop HTLC route independently checked. |
| Three-panel financial smoke | Native and LOS Lightning sends across all three pairs plus on-chain send/mined confirmation passed. These deliberately use fictitious coins. |
| Failure and retry | Dirty checkout rejected before panel disruption. Failed UI deployment restored assets and active panels. Recovery from a representative interrupted-deployment manifest restored all three panels, including Charlie. |
| Actual isolated Linux reboot | All three native nodes unlocked/reconnected and all three panels started automatically; identities, credentials and channels retained. |
| Certificates and concurrent browser sessions | Each node's localhost hostname verified against the lab CA. Reissuing leaf certificates preserved the CA and private keys. Three authenticated browser tabs stayed independent; logging out Bob retained Alice/Charlie sessions. No hosts/DNS edits. |
| Additional API regressions | Signing/native verification, UTXO labels/groups/leases and Audit passed for all three nodes with restoration checks. No leases, pending channels or limbo remained after acceptance. |
| Focused Python suite | Linux: 19 passed. Windows: 8 passed, 11 explicitly skipped because they require Linux/root/native process state. |

Cold certificate generation, migration, idempotence, incorrect-host rejection,
default/custom alias handling, pending-channel reuse and legacy/two-panel recovery
also have focused automated coverage. Existing standard Bitcoin data remains a
preflight refusal; even unused metadata must be inspected and preserved outside
the standard path before an operator retries. The disposable-host flag does not
bypass this guard.

The Linux fixture reused downloaded toolchains/packages for replay runs; this is
not a claim to have tested every prerequisite absence or network/download failure.
No arm64 support, external Telegram delivery, exhaustive failure matrix or new
validation of every Lightning operation is claimed. Existing feature validation
is documented separately in the linked lab guide. Production internal UI upgrade
and production installer paths are outside the scope of this regtest-only change.

## Português

O instalador regtest agora mantém Alice, Bob e Charlie com LND nativo, painel,
carteira, banco e autenticação próprios. Os aliases iniciais são
`LOS-Regtest-Alice`, `LOS-Regtest-Bob` e `LOS-Regtest-Charlie`. Os serviços iniciam
automaticamente. O triângulo de canais permite pagamentos diretos e roteados;
reexecutar o instalador preserva canais existentes e aliases personalizados.

A validação em Ubuntu 24.04 amd64 incluiu instalação real, repetição, migração de
laboratório existente, pagamento Alice → Bob → Charlie comprovado no LND,
pagamentos pelos três painéis, envio on-chain com mineração, recuperação de
deploy com falha/interrompido e reinicialização real do Linux isolado. Identidades,
credenciais e canais anteriores foram preservados. Os 19 testes Python passaram
em Linux; no Windows, oito passaram e 11 foram explicitamente ignorados.

Os endereços `alice.localhost`, `bob.localhost` e `charlie.localhost` permitem
sessões simultâneas independentes. Certificados novos e migrados foram
verificados; a CA e as chaves privadas existentes foram mantidas. Sair de Bob
não desconectou Alice ou Charlie no teste real de navegador.

As alterações do Charlie ficam restritas ao instalador regtest, auxiliares e
documentação. Não houve alteração de instaladores de produção ou do código
Go/React em relação à PR #351, da qual este trabalho depende. Não se afirma
validação arm64, Telegram externo, todos os cenários de falha ou todos os fluxos
Lightning. O ambiente Linux de instalação limpa fica dentro da VM descartável,
sem criar outra VM ou alterar a rede física.

See / Consulte: [laboratory guide](../scripts/regtest-lab/README.md).
