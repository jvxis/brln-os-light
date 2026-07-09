# Plano — Instalações novas nascerem em modo litd integrated

## Status

Proposta. **Não iniciado.** Este documento descreve a abordagem; não há
implementação associada.

Data: 2026-07-09.

## Objetivo

Permitir que **instalações novas** do LightningOS Light (via `install.sh`)
possam **nascer** em **modo `litd` integrado** — um único processo `litd` que
embarca `lnd` + `tapd` no mesmo binário — em vez do modo padrão standalone
(apenas `lnd` como serviço nativo).

Isso é a **fundação** do "edge node Taproot da comunidade": só no modo integrado
é possível, no futuro, habilitar Taproot Assets sobre Lightning (asset channels).
A interface nativa de Taproot e o fluxo de redeem são fases posteriores e **não**
fazem parte deste plano (ver "Fora de escopo").

A escolha é **opcional e explícita**, via um parâmetro no `install.sh`
(`INSTALL_MODE=integrated`), e vale **apenas para instalações novas**. O modo
standalone continua o padrão.

## Escopo

**Entra:**
- Um parâmetro `INSTALL_MODE` (`standalone` padrão | `integrated`) no `install.sh`.
- Instalação do binário `litd` (e `litcli`) no caminho integrado.
- Um `lit.conf` derivado do `lnd.conf` (ver "Design"), lido pelo `litd`.
- Um serviço systemd que roda `litd` no lugar de `lnd` (mantendo o **nome do unit `lnd`**).
- Uma guarda que **impede** converter um nó standalone existente em integrado.

**Não entra (fases posteriores):**
- Interface nativa de Taproot Assets / asset channels na UI.
- Implementação do redeem (token → sats) — ver `docs`/notas de arquitetura.
- Migração de um nó standalone existente para integrated.
- Loop / Pool / Faraday / UI do Lightning Terminal (escopo mínimo: só `lnd` + `tapd`).

## Fatos verificados na fonte (críticos)

1. **Modo integrado é obrigatório** para Taproot Asset Channels: exige
   `lnd-mode=integrated` **e** `taproot-assets-mode=integrated` no mesmo binário;
   **`lnd` remoto ainda não é suportado** ("Remote mode support will be added in
   the future"). Não há como manter o `lnd` como serviço separado lendo `lnd.conf`
   e ter asset channels.
   Fonte: [Taproot Assets Channels — Builder's Guide](https://docs.lightning.engineering/lightning-network-tools/taproot-assets/taproot-assets-channels).
2. **No modo integrado o `litd` lê um único `lit.conf`** e popula a config do
   `lnd` a partir dele; **não lê `<lnddir>/lnd.conf`**. Cada opção de `lnd` vira
   `lnd.`-prefixada (ex.: `bitcoin.mainnet=1` → `lnd.bitcoin.mainnet=1`).
   Fontes: [config.go do litd](https://github.com/lightninglabs/lightning-terminal/blob/master/config.go),
   [config-lnd-integrated.md](https://github.com/lightninglabs/lightning-terminal/blob/master/docs/config-lnd-integrated.md).
3. **Maturidade:** todas as releases do `litd` são `-alpha` (mais recente na
   escrita deste doc: `v0.17.0-alpha`); Taproot Asset Channels são experimentais
   na mainnet. A versão exata do `litd` precisa ser validada em máquina real
   contra as versões de `lnd`/`tapd` que ela embarca.

## Design

O manager é fortemente acoplado a `/data/lnd/lnd.conf` (constante fixa em
`internal/server/handlers.go`; lido/escrito pela LND Config UI e por parsers de
RPC bitcoind / bitcoin-source / alias / DSN). Como o modo integrado faz o daemon
ler `lit.conf` (e não `lnd.conf`), a estratégia é um **adaptador**, mantendo o
`lnd.conf` como fonte-de-verdade:

- **Fonte-de-verdade:** `/data/lnd/lnd.conf`, editado pelo manager exatamente
  como hoje (a LND Config UI e os parsers **não mudam**).
- **Gerado:** `/data/lnd/lit.conf`, produzido por um **compilador** que:
  (a) transforma cada `key=value` do `lnd.conf` em `lnd.key=value` (headers
  `[Section]` são cosméticos → descartados; comentários/linhas em branco
  ignorados), e (b) anexa um bloco estático do `litd` (modos, portas, `uipassword`,
  `lnd.lnddir`). Contém segredo → permissão `0600`, dono `lnd`.
- **Onde roda:** como `ExecStartPre=` no unit systemd `lnd` (versão integrada).
  Assim, **todo `systemctl restart lnd` já existente** recompila o `lit.conf`
  antes do `litd` subir — sem tocar em nenhum call-site do código Go.
- **Porta:** o `litd` sobe a UI/proxy em loopback numa porta diferente (ex.:
  `127.0.0.1:8444`), porque a `8443` já é do manager.

## O que muda e o que NÃO muda

| Aspecto | Standalone (padrão, inalterado) | Integrado (novo, opt-in) |
|---|---|---|
| Serviço systemd | unit `lnd`, `ExecStart=/usr/local/bin/lnd` | **mesmo unit `lnd`**, `ExecStartPre=`compilador, `ExecStart=/usr/local/bin/litd --configfile=/data/lnd/lit.conf` |
| gRPC lnd :10009 + tls.cert + admin.macaroon em `/data/lnd` | sim | **sim (inalterado)** |
| Fonte de config do lnd | `/data/lnd/lnd.conf` (lido pelo daemon) | `/data/lnd/lnd.conf` (lido pelo **manager**; compilado p/ `lit.conf`) |
| Config lida pelo daemon | `lnd.conf` | **`lit.conf` gerado** |
| LND Config UI / parsers bitcoind / DSN | funcionam | **funcionam sem alteração** |
| Manager (autofee, rebalance, reports, canais…) | funciona | **funciona** (gRPC :10009 idêntico) |
| tapd | app Docker da loja (on-chain) | embarcado no `litd` |

## Garantia de zero regressão para instalações existentes

O upgrade de quem já roda o app é feito **pelo dashboard**, que executa
`internal/server/assets/upgrade-app.sh` — **não** o `install.sh`. Esse script:
- **Faz:** rebuilda o binário do manager + a UI, stage do peerswap, refresh do
  sudoers, e `systemctl restart lightningos-manager`.
- **Não faz:** não roda `install.sh`/`main()`; não chama `copy_templates` (→
  `lnd.conf` intocado); não chama `install_systemd` (→ `lnd.service` intocado);
  não escreve `config.yaml`; não instala `litd`; não roda o compilador; **não
  reinicia o `lnd`**.

**Consequência:** todas as mudanças de `install.sh`/systemd/templates deste plano
**só rodam numa instalação nova** que escolha `INSTALL_MODE=integrated`. A camada
do daemon de um nó standalone existente fica **byte a byte idêntica** após o
upgrade. Qualquer código novo no manager deve ficar guardado por uma flag
`lnd.integrated` com **default `false`** (mesmo padrão seguro do `SharedGRPC`
existente), de modo que, sem a flag, cada caminho caia no comportamento atual.

**Guarda extra:** mesmo que um usuário rode `install.sh` manualmente, o
`INSTALL_MODE` default é `standalone`, e uma verificação deve **abortar** qualquer
tentativa de converter um standalone existente em integrado (detecção por
`config.yaml`/serviço `lnd` existentes; sem depender de `wallet.db`, que não
existe no backend Postgres).

## Componentes (referência para implementação futura)

Descrição de alto nível — a implementação é um passo separado, a ser autorizado
explicitamente.

- `install.sh`: parâmetro `INSTALL_MODE`; função `install_litd`; guarda de
  fresh-install; ramificações em `copy_templates` / `install_systemd` e na
  gravação do `config.yaml`.
- Template de config estático do `litd` (modos, `httpslisten` em loopback,
  `uipassword`, `lnd.lnddir`).
- Unit systemd integrado (mesmo nome `lnd`, `ExecStart=litd`, `ExecStartPre=`
  compilador).
- Script compilador `lnd.conf` → `lit.conf` (helper instalado em
  `/usr/local/sbin`, chamado via `ExecStartPre`).
- Flag `lnd.integrated` (default `false`) em `internal/config/config.go`, gravada
  no `config.yaml` só no modo integrado.

## Decisões a verificar em máquina real (antes de usar)

1. **Versão do `litd`:** escolher a `-alpha` cujas versões embarcadas de `lnd`/`tapd`
   sejam compatíveis com o que o manager espera. Como é fresh-install, o banco é
   criado do zero pelo `litd`, então fica internamente consistente.
2. **Sintaxe do `lit.conf`:** prefixos exatos (`lnd.*`, `taproot-assets.*`),
   `--configfile`, `uipassword` vs. alternativa de desabilitar UI, e confirmar
   que os headers `[Section]` do `lnd.conf` são realmente cosméticos ao prefixar
   `lnd.` (validar o output do compilador contra o `litd`).
3. **Unlock da carteira:** confirmar que o wizard atual (WalletUnlocker via gRPC
   :10009) funciona com o `lnd` embarcado; senão, usar `wallet-unlock-password-file`.
4. **systemd:** confirmar `Type=` correto (o `litd` sinaliza readiness via
   sd_notify?) e revisar as diretivas de sandbox.
5. **Backend do `tapd` embarcado:** sqlite (default, mínimo) vs. Postgres — decidir
   quando a interface de Taproot for de fato usada (fase posterior).

## Fora de escopo (fases posteriores)

- **Interface Taproot Assets nativa** na UI + rotear o `tapcli` para o `tapd`
  embarcado no `litd`.
- **Redeem token → sats:** o modelo escalável é distribuição **on-chain** +
  um **swap operado pelo edge** (submarine swap: hashlock de asset via vPSBT do
  `tapd` + hold-invoice do `lnd`), mantendo os usuários em `tapd`+`lnd`. Detalhes
  registrados nas notas de arquitetura do projeto.

## Referências

- [Taproot Assets Channels — Builder's Guide](https://docs.lightning.engineering/lightning-network-tools/taproot-assets/taproot-assets-channels)
- [Integrating litd — config-lnd-integrated.md](https://github.com/lightninglabs/lightning-terminal/blob/master/docs/config-lnd-integrated.md)
- [config.go do lightning-terminal](https://github.com/lightninglabs/lightning-terminal/blob/master/config.go)
- [Taproot Assets Trustless Swap](https://docs.lightning.engineering/the-lightning-network/taproot-assets/trustless-swap)
