# Plano — Bark Wallet na Loja de Apps

## Status

**Implementado no código em 2026-07-10.** Testes Go completos, validação do
registry e build de produção da UI passaram. A validação final em Linux com
Docker, nos alvos `amd64` e `arm64`, continua pendente antes de classificar o app
além de Beta.

Data de verificação: 2026-07-10.

Documento relacionado:
[30_ARK_PROTOCOL_STUDY_PT_BR.md](./30_ARK_PROTOCOL_STUDY_PT_BR.md).

## Decisão de produto

A primeira versão deve empacotar a **interface oficial `bark-web` da Second**, a
mesma usada pela Bark Wallet no Umbrel, em vez de reconstruir uma carteira
nativa dentro da SPA do LightningOS.

Consequências:

- a experiência será essencialmente a mesma do Umbrel;
- os fluxos e correções upstream serão reaproveitados;
- o LightningOS será responsável por instalação, autenticação, TLS, lifecycle,
  persistência e abertura da aplicação;
- a primeira versão não criará um fork visual;
- tradução pt-BR e identidade visual LightningOS serão uma fase posterior,
  preferencialmente contribuída ao upstream.

O `bark-web` é MIT, foi publicado como implementação de referência e possui
imagens Docker oficiais. A versão verificada no Umbrel na data deste plano é
`0.3.0`.

## Objetivo

Adicionar à Loja de Apps uma entrada **Bark Wallet** que permita ao usuário:

- criar ou importar uma carteira Bark;
- manter saldo ARK autocustodial;
- enviar e receber por ARK, Lightning e Bitcoin on-chain;
- usar QR/BIP-321 e destinos BOLT 11, BOLT 12 e LNURL suportados upstream;
- acompanhar histórico, status e taxas;
- consultar VTXOs e seus prazos;
- configurar refresh automático;
- iniciar e acompanhar emergency exit;
- revelar a seed somente dentro do fluxo protegido da própria Bark Wallet.

## Fora de escopo

- Rodar um operador `captaind` próprio.
- Usar o LND local como gateway da carteira.
- Criar canais Lightning adicionais.
- Integrar o saldo Bark ao saldo nativo do dashboard LightningOS.
- Expor a API `barkd` para terceiros na primeira versão.
- NWC, LNbits ou checkout como integrações automáticas.
- Suporte Arkade/`arkd`.
- Alterar ou reestilizar profundamente o `bark-web`.
- Deletar automaticamente os dados da carteira ao desinstalar o app.

## Interface e experiência

### Interface reutilizada

A UI oficial verificada contém:

- dashboard responsivo com saldo total, off-chain e on-chain;
- histórico unificado com filtros ARK, Lightning e on-chain;
- ações Scan, Send e Receive;
- envio com detecção do tipo de destino e comparação de taxas;
- recebimento por QR combinado/BIP-321 e abas por protocolo;
- verificação Branta quando disponível;
- página de VTXOs;
- settings, tema light/dark/system e discreet mode;
- refresh manual e automático;
- visualização da seed;
- download de diagnóstico;
- fluxo e progresso de emergency exit.

Na versão inicial, a Loja abre essa aplicação em nova aba, como já faz com
LNbits, LNDg, Peerswap e outros apps externos.

### Idioma

O repositório está preparado para i18n, mas na verificação atual contém apenas
`en.json`. A v1 será em inglês para permanecer byte-for-byte próxima das imagens
oficiais.

Uma fase posterior deve:

1. adicionar `pt-BR.json` ao upstream;
2. testar termos sensíveis como refresh, VTXO e emergency exit;
3. manter inglês como fallback;
4. evitar um fork permanente apenas para tradução.

### Identidade

O card da Loja deve usar o nome e ícone oficiais, com descrição clara:

> Carteira autocustodial para pagamentos ARK, Lightning e on-chain, conectada ao
> operador Bark da Second. Não utiliza o LND local.

O card e a tela de instalação devem exibir badges **Beta**, **Mainnet** e
**Operador externo: Second**.

## Arquitetura proposta

O padrão do Umbrel usa três containers. O LightningOS acrescentará um proxy TLS
porque não possui o `app_proxy` do Umbrel e porque QR scanning no navegador exige
secure context.

```text
Browser
  |
  | HTTPS :4004 + login Bark
  v
Caddy proxy
  |-- / e assets ----------> bark-web (nginx/static UI :8080)
  |-- /api/* --------------> bark-web-api :4001
  |-- /barkd-ws/* ---------> barkd :4000
                                |
                                +--> https://ark.second.tech
                                +--> https://mempool.second.tech/api
```

Nenhum container se conecta ao LND local. Certificado, macaroon e wallet do LND
não devem ser montados.

## Containers e versões

Basear o compose no manifesto oficial do Umbrel, mas adaptar a rede e o proxy.

Imagens atualizadas e verificadas em 2026-08-12:

- `secondark/bark-web:0.7.2@sha256:7f2b4469330f287192c981c64557d9469534fb4c4919bc846b829f59e4267655`
- `secondark/bark-web-api:0.7.2@sha256:bf23f4b89e2c759d0d498f2d2a949b5d0f7fee29d8c1cc2b01a2882315363ffc`
- `secondark/bark:0.6.1@sha256:93bf4f806fb66aef06db071f88c8dff13ab44d5cad21bd94a9ab927ded3dcafc`
- `caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d`.

A imagem publicada do daemon contém um wrapper legado de regtest como
entrypoint. O runtime LightningOS nunca o executa: fixa
`/usr/local/bin/barkd`, verifica o checksum oficial por arquitetura antes do
start (`41ca75...efd` em amd64 e `a07849...482` em arm64) e exige a versão exata
`barkd 0.6.1`.

Esses valores não são autorização para atualização automática. Os manifests
`amd64` e `arm64` foram verificados na API do registry durante a implementação;
o pull e a execução reais em ambos os alvos continuam como gate de release.

Não usar `latest`.

## Configuração de runtime

Valores iniciais, seguindo o pacote mainnet oficial:

```text
BARK_NETWORK=mainnet
ARK_SERVER=https://ark.second.tech
CHAIN_SOURCE=https://mempool.second.tech/api
BARKD_URL=http://barkd:4000
PORT=4001
WALLET_DIR=/wallet-data/.bark
UI_AUTH=true
UI_PASSWORD_FILE=/run/lightningos-auth/ui_password
UI_SESSION_SECRET_FILE=/run/lightningos-auth/ui_session_secret
```

A v1 deve fixar mainnet e o operador oficial. Seleção de operador customizado é
uma fase posterior porque endereços são específicos do servidor e um endpoint
malicioso ou incompatível muda o modelo de segurança.

## Portas e rede

- Porta pública da aplicação: **4004/tcp**.
- Scheme informado pela Loja: **HTTPS**.
- Porta interna do `web`: 8080, sem publicação no host.
- Porta interna do `api`: 4001, sem publicação no host.
- Porta interna do `barkd`: 4000, sem publicação no host.
- Somente o Caddy publica `4004:4004`.

A porta 4004 não conflita com as portas registradas atualmente. A validação do
registry continuará sendo a fonte final dessa garantia.

O Caddy segue o padrão já usado pelo RoboSats Gateway, com uma adaptação de
segurança importante:

- `auto_https off`;
- certificado local gerado e preservado pelo broker privilegiado;
- TLS terminado no proxy;
- `/` e assets para `web:8080`;
- `/api/*` diretamente para `api:4001`;
- `/barkd-ws/*` diretamente para `barkd:4000`;
- header `X-Forwarded-Proto: https` preservado na API;
- WebSocket/streaming sem buffering indevido.

O encaminhamento direto de `/api/*` é intencional. O nginx upstream define
`X-Forwarded-Proto` a partir do próprio hop HTTP; passar a API por ele faria o
cookie de login perder o atributo `Secure` mesmo quando o navegador usa HTTPS.

O certificado pode ser autocertificado e produzir a mesma advertência inicial
do dashboard LightningOS. HTTPS é necessário para cookie seguro e câmera/QR.

## Layout de arquivos

```text
/var/lib/lightningos-privileged/apps/bark-wallet/
  docker-compose.yaml
  Caddyfile
  tls/
    server.crt
    server.key

/var/lib/lightningos/apps-data/bark-wallet/
  wallet/
    .bark/
      auth_token
      database/state files managed by barkd
      debug.log
  auth/
    ui_password
    ui_session_secret
```

Permissões implementadas:

- wallet: `65531:65531 0700`;
- snapshot do broker: `root:root 0700`, Compose `root:root 0600`;
- senha/session secret: `root:65530 0640` em diretório `root:65530 0750`;
- Caddyfile, certificado e key TLS: `root:65532 0640`;
- API com acesso somente de leitura à wallet para obter `auth_token` e logs.

## Autenticação e segurança

### Login da Bark Wallet

O `bark-web-api` já oferece um login opcional com:

- cookie HTTP-only;
- `SameSite=Strict`;
- cookie `Secure` quando recebe `X-Forwarded-Proto: https`;
- sessão assinada;
- proteção CSRF para métodos mutáveis;
- rate limiting/backoff de tentativas.

A instalação deve habilitar obrigatoriamente `UI_AUTH=true`.

O broker privilegiado deve:

1. gerar uma senha aleatória forte;
2. gerar previamente um session secret independente;
3. gravar ambos atomicamente como `root:65530 0640`;
4. disponibilizar **somente a senha da UI** pelo mecanismo de “copiar senha do
   app” já existente;
5. nunca retornar session secret, auth token do `barkd`, seed ou banco da
   carteira.

Reset de senha invalida sessões existentes sem reiniciar `barkd`: a API 0.7.2
relê o arquivo a cada verificação e incorpora a senha atual na assinatura da
sessão.

### Seed

O `bark-web` possui fluxo próprio para revelar a mnemonic por meio da API interna
do `barkd`. Para preservar a interface oficial:

- `barkd` e `api` nunca terão porta publicada no host;
- o endpoint só será alcançável através da UI autenticada e do proxy interno;
- o manager do LightningOS não chamará, registrará ou repassará esse endpoint;
- respostas e logs do manager nunca devem conter mnemonic;
- testes não devem usar seeds reais em fixtures ou snapshots.

A auditoria da v0.7.2 confirmou que o proxy genérico bloqueia o caminho da
mnemonic e que a revelação existe apenas em `POST /api/reveal-mnemonic`, com
sessão válida e CSRF; o Caddy adiciona um segundo 404 ao caminho direto. O
upstream não pede novamente a senha nesse POST. Esse comportamento oficial foi
aceito para o app Beta, mas a decisão de exigir fresh reauthentication continua
como gate explícito da revisão final de segurança da 0.5.3.

### Dados e desinstalação

Desinstalar o card deve:

- parar e remover containers e networks;
- remover o snapshot em `/var/lib/lightningos-privileged/apps/bark-wallet` e
  qualquer diretório legado do manager;
- **preservar** `/var/lib/lightningos/apps-data/bark-wallet` por padrão.

Um app de wallet não deve destruir seed e estado off-chain usando a mesma ação
genérica de uninstall. A exclusão definitiva deve acontecer somente dentro do
fluxo “Delete wallet” do `bark-web`, com confirmação explícita, ou em um futuro
endpoint separado de “apagar dados”.

Reinstalar deve detectar a wallet preservada e reutilizá-la.

### Backup e restore

A UI deve manter o alerta upstream para backup da seed e do diretório da wallet.
O plano não adiciona seed ao sistema de backups do manager.

Antes de considerar o app estável, executar um drill documentado:

1. criar wallet de teste;
2. receber fundos e obter VTXOs;
3. parar containers abruptamente;
4. copiar wallet data para máquina limpa;
5. reinstalar a mesma versão;
6. recuperar estado e verificar VTXOs;
7. testar refresh e emergency exit;
8. repetir a partir da seed conforme o mecanismo upstream e registrar o que não
   é reconstruído automaticamente.

## Alterações previstas no backend Go

### Novo handler

Implementado em `lightningos-light/internal/server/apps_bark_wallet.go` usando
`appHandler`:

- `ID`: `bark-wallet`;
- `Name`: `Bark Wallet`;
- `Port`: `4004`;
- `Info`: `Scheme=https`, status do serviço `proxy` e disponibilidade;
- `Install`: preparar Docker, dirs, secrets, TLS, compose, imagens e healthcheck;
- `Start`: revalidar assets/config e executar `compose up -d`;
- `Stop`: `compose stop`;
- `Uninstall`: `compose down --remove-orphans`, removendo apenas app files;
- `AdminPasswordPath`: caminho da senha de UI.

Helpers específicos:

- `barkWalletAppPaths()`;
- `ensureBarkWalletPaths()`;
- `ensureBarkWalletSecrets()`;
- `ensureBarkWalletTLS()`;
- `barkWalletComposeContents()`;
- `barkWalletCaddyfileContents()`;
- `ensureBarkWalletImages()`;
- status agregado pelo `getComposeStatus(..., "proxy")`;
- `ensureBarkWalletUFWAccess()`.

### Registry

Registrar `newBarkWalletApp(s)` em
`lightningos-light/internal/server/apps_registry.go` e validar unicidade de ID e
porta com `TestValidateAppRegistry`.

### Senha administrativa

Generalizar os handlers atuais para permitir `bark-wallet`:

- `GET /api/apps/bark-wallet/admin-password` retorna somente a senha da UI;
- `POST /api/apps/bark-wallet/reset-admin` gera senha nova, grava com segurança
  e reinicia o `api`.

Não criar endpoint novo se o contrato genérico existente puder ser reutilizado.
Atualizar `docs/03_API_SPEC.md` apenas para documentar o novo app suportado pelo
contrato.

## Alterações previstas na UI LightningOS

Em `lightningos-light/ui/src/pages/AppStore.tsx`:

- importar e mapear o ícone oficial em `iconMap`;
- permitir “copiar senha” para `bark-wallet`;
- permitir reset da senha quando o app estiver rodando;
- exibir badges Beta/Mainnet/Second;
- abrir `https://<host>:4004` em nova aba;
- mostrar aviso de certificado local na primeira abertura;
- deixar explícito que o LND local não é usado;
- não criar rota em `internalRoutes` na v1.

Adicionar traduções do card, avisos e ações aos catálogos i18n do LightningOS.
A UI interna da Bark permanece upstream/inglês na primeira versão.

## Fluxo de instalação

1. Usuário seleciona **Install** na Loja.
2. LightningOS mostra confirmação com os fatos:
   - mainnet;
   - hot wallet autocustodial;
   - conexão ao operador e chain source da Second;
   - não usa LND local;
   - necessidade de backup da seed e wallet data.
3. Backend garante Docker/Compose.
4. Cria app dirs e wallet data sem sobrescrever dados existentes.
5. Gera senha e session secret se ainda não existirem.
6. Gera certificado TLS e Caddyfile.
7. Baixa imagens fixadas e verifica sua disponibilidade.
8. Escreve compose determinístico.
9. Executa `compose up -d`.
10. Aguarda healthchecks de `barkd`, `api`, `web` e `proxy`.
11. Abre UFW apenas em `4004/tcp`.
12. Retorna sucesso e habilita Copy Password/Open.

O install e o start devem ser idempotentes.

## Healthchecks

O status agregado implementado usa `docker compose ps` no serviço `proxy`. O
compose oficial upstream não publica healthchecks para os três serviços, então
a validação real de readiness permanece parte do teste Linux antes do release.
Depois de confirmar quais comandos existem em cada imagem, podem ser adicionados:

- `api`: `GET /health`;
- `web`: resposta HTTP interna;
- `barkd`: endpoint autenticado de health/info ou verificação de processo;
- `proxy`: resposta HTTPS local aceitando o certificado gerado.

Uma versão posterior pode fazer a instalação aguardar todos esses checks. Por
enquanto, falhas de criação/start são retornadas por `docker compose up -d`, e
`Info` diferencia proxy rodando, parado ou status desconhecido.

Erros devem distinguir:

- falha ao baixar imagem;
- arquitetura sem manifest;
- permissão no wallet dir;
- auth token ausente;
- API sem acesso ao token;
- operador ARK inacessível;
- chain source inacessível;
- TLS/proxy indisponível.

Indisponibilidade temporária da Second depois da instalação não deve transformar
o app em “not installed”; deve aparecer como erro de conectividade dentro da UI.

## Estratégia de upgrades

- Versões são alteradas somente por PR com constantes e digests revisados.
- `Start` não deve buscar automaticamente uma versão mais nova.
- Antes do bump, testar o par exato `bark-web`/`api`/`barkd`.
- Ler release notes procurando mudanças de banco, protocolo e exits.
- Fazer snapshot do wallet data de teste antes da migração.
- Validar downgrade ou documentar explicitamente quando não for suportado.
- Nunca recriar wallet data para “corrigir” incompatibilidade de versão.

## Testes automatizados

Adicionar `apps_bark_wallet_test.go` cobrindo no mínimo:

- definição, ID, nome, porta e scheme;
- paths sob `appsRoot` e `appsDataRoot`;
- compose usa apenas imagens/digests esperados;
- somente o proxy publica porta no host;
- `barkd` e API não montam `/data/lnd`;
- mainnet, operador e chain source corretos;
- `UI_AUTH=true` obrigatório;
- password e session secret em paths distintos;
- wallet mount read-write apenas no `barkd`;
- API recebe wallet read-only;
- Caddy configura TLS e proxy correto;
- install/start/stop/uninstall idempotentes;
- uninstall preserva wallet data;
- reset de senha não altera wallet data;
- registry sem IDs ou portas duplicadas;
- erros não incluem senha, token ou mnemonic.

## Testes de integração e máquina real

Executar em `amd64` e Raspberry Pi 5/`arm64`:

1. instalação limpa;
2. abertura HTTPS e login;
3. criação de wallet;
4. logout/login e expiração de sessão;
5. restart do app preservando a wallet;
6. stop/start pela Loja;
7. uninstall/reinstall preservando dados;
8. reset da senha invalidando sessão anterior;
9. câmera/QR em secure context;
10. envio e recebimento ARK com valores pequenos;
11. recebimento e pagamento Lightning;
12. entrada e saída on-chain;
13. refresh manual e automático;
14. visualização de VTXOs;
15. emergency exit em ambiente controlado;
16. restore drill;
17. indisponibilidade simulada do operador e do chain source;
18. upgrade entre duas versões suportadas.

Usar fundos mínimos e uma carteira exclusiva de teste na validação mainnet.

## Fases de implementação

### Fase 1 — Empacotamento fiel ao Umbrel

- handler Go;
- quatro containers (`web`, `api`, `barkd`, `proxy`);
- versão/digests fixados;
- data dir persistente;
- mainnet/Second fixos;
- status e lifecycle;
- testes de compose e registry.

### Fase 2 — Integração segura com a Loja

- login obrigatório do `bark-web`;
- geração/cópia/reset de senha;
- TLS e scheme HTTPS;
- ícone, card, badges e avisos;
- healthchecks e UFW;
- testes em máquina real.

### Fase 3 — Hardening e release Beta

- revisão de exposição da mnemonic;
- backup/restore drill;
- testes de crash e upgrades;
- observabilidade sem secrets;
- documentação para usuário;
- release como Beta.

### Fase 4 — Melhorias posteriores

- contribuição pt-BR ao upstream;
- seleção segura de operador Bark;
- NWC;
- integração com LNbits/checkout;
- export de métricas não sensíveis;
- pesquisa do Operator Lab e gateway LND.

## Definition of Done

A Bark Wallet pode sair como Beta quando:

- instala, inicia, para, reinstala e desinstala de forma idempotente;
- usa exatamente versões e digests aprovados;
- não publica `barkd` ou `api` no host;
- exige autenticação e funciona por HTTPS;
- o manager nunca acessa ou retorna seed/auth token;
- uninstall preserva wallet data;
- backup e restore foram testados;
- ARK, Lightning e on-chain foram testados com valores mínimos;
- refresh e emergency exit foram exercitados;
- testes Go, registry e build da UI passam;
- `docs/03_API_SPEC.md`, `docs/10_APP_STORE_SPEC.md` e documentação do usuário
  refletem o novo app;
- limitações Beta, operador externo e ausência de integração com LND estão
  visíveis antes da instalação.

## Referências

- [Bark Wallet no Umbrel](https://apps.umbrel.com/app/bark-wallet)
- [Compose oficial do Umbrel](https://raw.githubusercontent.com/getumbrel/umbrel-apps/master/bark-wallet/docker-compose.yml)
- [bark-web — código MIT](https://gitlab.com/ark-bitcoin/labs/bark-web)
- [bark-web README](https://gitlab.com/ark-bitcoin/labs/bark-web/-/raw/main/README.md)
- [bark-web auth opt-in](https://gitlab.com/ark-bitcoin/labs/bark-web/-/raw/main/docker-compose.auth.yml)
- [Bark — código e releases](https://gitlab.com/ark-bitcoin/bark)
- [Second — lançamento mainnet](https://blog.second.tech/bark-now-on-bitcoin-mainnet/)
- [Estudo ARK do LightningOS](./30_ARK_PROTOCOL_STUDY_PT_BR.md)
