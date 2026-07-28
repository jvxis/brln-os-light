# Magma Inbound Sales — Plano do App

Status: **planejamento** (nenhum código escrito)
Data: 2026-07-28
Base: script Python `amboss_channel_open_bot` do autor (Telegram + lncli + bos)

---

## 1. O que é

App do App Store que conecta o nó à API do Magma (Amboss) para **venda de liquidez
inbound**: monitora pedidos de compra, gera a invoice, aceita a ordem, abre o canal
e confirma o channel point de volta ao Magma.

Três modos de operação (uma única setting, `mode`):

| Modo | Comportamento |
|------|---------------|
| `monitor` | Só observa e avisa (UI + Telegram). Zero ação, zero fundo em risco. |
| `assisted` | Avisa e oferece botões: "Aceitar ordem", "Abrir canal". Cada passo é 1 clique com preview de fee/UTXO. |
| `auto` | Pipeline completo, com policy engine e limites de orçamento. |

O modo é a fronteira de autorização. Instalar + habilitar + configurar policy = consentimento
explícito; o worker não pede senha por ordem (seria incompatível com automação).
Toda decisão vai para trilha de auditoria (`magma_order_events` + audit service).

---

## 2. Fluxo Magma (o que o script já provou funcionar)

```
offer_orders.list[].status
  WAITING_FOR_SELLER_APPROVAL   → seller cria invoice de seller_invoice_amount
                                   e chama sellerAcceptOrder(id, request)
  (comprador paga)
  WAITING_FOR_CHANNEL_OPEN      → seller conecta no peer (account = pubkey do comprador),
                                   abre canal de `size` sats,
                                   chama sellerAddTransaction(id, channel_point)
  → Magma valida e liquida
```

GraphQL endpoint: `https://api.amboss.space/graphql`, header `Authorization: Bearer <token>`.

Queries/mutations usadas hoje pelo script:
- `getUser.market.offer_orders.list { id size status account seller_invoice_amount }`
- `sellerAcceptOrder(id, request)`
- `sellerAddTransaction(id, transaction)`
- `getNode(pubkey).graph_info.node.addresses[].addr` (endereço para connect)

### 2.1 Schema real (introspection, 2026-07-28)

A introspection do Amboss está **aberta e sem autenticação**, então a maior parte da
Fase 0 já está resolvida sem token. O que segue foi extraído direto de
`api.amboss.space/graphql`.

#### `OrderType` — campos completos

```
id  account  offer  offer_account  offer_side  size  status  created_at  updated_at
seller_invoice_amount  buyer_invoice_amount  payment_hash  payment_status  request
amboss_fee_rate  fixed_fee  variable_fee
locked_min_block_length  blocks_until_can_be_closed  closed_blocks_before_min
locked_fee_rate  locked_fee_rate_cap  locked_base_fee  locked_base_fee_cap
fee_above_cap_seconds
channel_id  transaction_id  timeout
buyer_scores(ReputationScore)  sides(OrderSides)  endpoints(OrderEndpoints)
buyer_close_side  seller_close_side  cancellation_reason
is_automated  chat_enabled  messages(MagmaMessage)
```

O script usa **5** desses campos. Os que mudam o desenho do app:

| Campo | Por que importa |
|---|---|
| `locked_min_block_length` | **O compromisso de permanência do canal, em blocos.** Era a incógnita nº 1 do plano. Entra na policy como ppm/dia e na proteção contra fechamento. |
| `blocks_until_can_be_closed` | Contador ao vivo do compromisso — alimenta direto o Close Manager. |
| `closed_blocks_before_min` | Quantos blocos antes do mínimo o canal foi fechado. É o registro da infração. |
| `locked_fee_rate_cap` / `locked_base_fee_cap` | **Teto de fee contratual do canal vendido.** Ver seção 7. |
| `fee_above_cap_seconds` | Tempo acumulado violando o teto. A infração é medida, não é interpretação. |
| `payment_status` | Estado do pagamento do comprador, direto — sem inferir por `status`. |
| `timeout` | Deadline para reagir/abrir. |
| `buyer_scores`, `sides.taker_metrics` | Reputação do comprador **nativa**, sem heurística nossa de grafo. |
| `amboss_fee_rate`, `fixed_fee`, `variable_fee` | Receita **líquida**. O script decide em cima do bruto. |
| `channel_id`, `transaction_id` | Reconciliação: o Magma diz qual canal ele associou à ordem. |
| `cancellation_reason` | `CHANNEL_SIZE_OUT_OF_BOUNDS`, `UNABLE_TO_CONNECT_TO_NODE`, `UNABLE_TO_PAY`. |
| `chat_enabled` / `messages` | Chat com o comprador (mutation `sendMagmaMessage`). |

#### `OrderStatus` — 17 estados, o script trata 2

```
WAITING_FOR_SELLER_APPROVAL      WAITING_FOR_BUYER_PAYMENT
WAITING_FOR_CHANNEL_OPEN         WAITING_FOR_ON_CHAIN_CONFIRMATION
ON_CHAIN_CONFIRMATION            SELLER_OPENED_CHANNEL
SELLER_SENT_TRANSACTION          VALID_CHANNEL_OPENING
INVALID_CHANNEL_OPENING          CHANNEL_MONITORING_FINISHED
SELLER_FAILED_TO_OPEN_CHANNEL    SELLER_FAILED_TO_REACT
SELLER_FAILED_TO_SEND_SWAP       SELLER_REJECTED
BUYER_REJECTED                   BUYER_FAILED_TO_PAY
ADMIN_CLOSED
```

Três consequências diretas:

1. **Existem estados de falha do vendedor** (`SELLER_FAILED_TO_REACT`,
   `SELLER_FAILED_TO_OPEN_CHANNEL`, `INVALID_CHANNEL_OPENING`). Não são cosméticos —
   são o registro de que você furou. Todos precisam virar alerta.
2. **Ignorar ordem indesejada é penalizado.** Existe `SELLER_FAILED_TO_REACT` e existe a
   mutation `sellerRejectOrder(id)`. Então a policy que recusa uma ordem tem de
   **recusar ativamente**, não deixar passar o prazo. O script não chama reject nunca.
3. `CHANNEL_MONITORING_FINISHED` é o fim do compromisso — o gatilho para liberar o canal
   do Close Manager e do clamp de fee.

#### `OrderPaymentStatus` — a invoice é HODL

```
PENDING_PAYMENT  SUCCESSFUL_PAYMENT  PAYMENT_FAILED
PAYMENT_REJECTED_BY_DESTINATION  INVALID_PAYMENT_SECRET
HODL_INVOICE_TIMEOUT  SELLER_INVOICE_EXPIRED
```

`HODL_INVOICE_TIMEOUT` confirma que o Amboss segura o pagamento. Logo o expiry longo do
script (180000s ≈ 50h) não é arbitrário: a invoice tem de sobreviver ao ciclo inteiro
de abertura e confirmação. `SELLER_INVOICE_EXPIRED` é o modo de falha se errarmos isso.

#### Mutations disponíveis

```
sellerAcceptOrder(id, request)      sellerAddTransaction(id, transaction)
sellerRejectOrder(id)               cancelOrder(id, reason)
sendMagmaMessage(orderId, msg)      setOrderCloseSide(orderId, closedBy)
createOffer / updateOffer / toggleOffer(id)
```

#### Caminho de consulta

```
getUser.market {
  enabled  has_active_offers
  pending_seller_orders : Float     ← contador barato
  pending_buyer_orders  : Float
  offer_orders(id: String) : OrderList   ← ordens contra as MINHAS ofertas
  orders(status: OrderStatus) : OrderList ← aceita filtro por status
  offers : OfferList
}
```

`pending_seller_orders` é um contador escalar. **O poller consulta ele primeiro e só
busca a lista completa quando muda.** Isso derruba muito o custo do polling de 90s.
`OrderList` não tem paginação — só `list`.

#### `OfferType` (para a Fase 4)

```
id  status  side  offer_type  account  seller_score
min_size  max_size  total_size  min_block_length
base_fee  base_fee_cap  fee_rate  fee_rate_cap
onchain_multiplier  onchain_priority  amboss_fee_rate  conditions  orders
```

Confirma que os tetos de fee e o `min_block_length` são definidos **na oferta** e
congelados em cada ordem como `locked_*`. E que a oferta declara `onchain_priority` /
`onchain_multiplier` — ou seja, o Magma tem opinião sobre a fee de abertura, que a nossa
policy de fee precisa respeitar em vez de inventar.

### 2.2 Dados reais — 85 ordens (conta de teste, 2023-12 a 2026-01)

Rodado com token de teste. Todas as identidades abaixo batem em **85/85 ordens**.

#### Economia da ordem — resolvida

```
variable_fee          = size * locked_fee_rate / 1e6      (85/85)
fixed_fee             = locked_base_fee                    (85/85)
seller_invoice_amount = variable_fee + fixed_fee           (85/85)
buyer_invoice_amount  = seller_invoice_amount
                        + size * amboss_fee_rate / 1e6     (85/85)
```

> **Correção de uma versão anterior deste plano.** Eu havia escrito que a receita
> líquida era `seller_invoice_amount` **menos** `amboss_fee_rate`/`fixed_fee`/
> `variable_fee`. Está errado e teria feito a policy recusar ordens boas:
> `fixed_fee` e `variable_fee` são os dois **componentes** da receita (a soma é o
> próprio `seller_invoice_amount`), e a taxa do Amboss é cobrada **por cima, do
> comprador**. **`seller_invoice_amount` já é o valor líquido do vendedor.**

Consequência: `locked_fee_rate` e `locked_base_fee` são o **preço da venda**, não a fee do
canal. Só as variantes `_cap` são restrição de routing fee. Nomes parecidos, significados
opostos — fácil de trocar na implementação.

#### Formatos reais

| Campo | Formato observado |
|---|---|
| `size`, `seller_invoice_amount`, `buyer_invoice_amount`, `fixed_fee`, `variable_fee` | string decimal, **sats** |
| `amboss_fee_rate` | string, ppm — só `0`, `500`, `1000`, `2000` |
| `locked_fee_rate` | int, ppm — preço da venda, 1500–11000 |
| `locked_base_fee` | int, **sats** — componente fixo do preço, 568–113884 |
| `locked_fee_rate_cap` | int, ppm — teto de routing fee: 10, 100, 350, 400, **500 (58×)**, **900 (21×)** |
| `locked_base_fee_cap` | int — só **0** (47×) ou **1** (38×) |
| `locked_min_block_length` | int, blocos — 1008(7d), 4320(30d), 8640(60d), 12960(90d), **25920(180d, 45×)** |
| `created_at` / `updated_at` | ISO 8601 UTC com ms |
| `channel_id` | **SCID legível `820617x1293x0`**, não uint64 — converter para casar com `lndclient` |
| `transaction_id` | txid hex puro |
| `account` / `endpoints.destination` | pubkey do comprador; `endpoints.source` = nosso |
| `timeout` | **string vazia em 85/85 — campo morto, não serve de deadline** |
| `buyer_scores` | lista `{metric, score, value}` — ex. `paymentRate` |
| `blocks_until_can_be_closed` | só preenchido em ordens vivas; 0 em todas as encerradas |

O `timeout` vazio derruba a ideia de ler o deadline da ordem direto da API. O prazo de
reação vai ter que ser inferido (`created_at` + janela observada) ou tratado como
"reagir o quanto antes", que é o comportamento seguro de qualquer forma.

#### Violação de teto de fee — o que os dados mostram

Só **6 das 85** ordens registraram `fee_above_cap_seconds`: 299s, 300s, 2695s, 6588s,
18566s (5,2h) e 21267s (5,9h). Detalhe decisivo: **todas as 6 tinham
`locked_fee_rate_cap = 500`**, e a divisão por `locked_base_fee_cap` não discrimina nada
(5 de 47 com cap 0; 1 de 38 com cap 1).

Cruzando com o template do projeto —
[templates/lnd.conf](lightningos-light/templates/lnd.conf) tem `bitcoin.feerate=1` (1 ppm)
e `bitcoin.basefee=1000` (1 sat) — a conclusão é que **a violação não nasce com o canal**.
1 ppm está muito abaixo de qualquer cap observado. A violação veio de a fee ter sido
**elevada depois**, durante o compromisso. Isso valida o desenho da seção 7.

#### Fechamento antecipado — quem fecha é o comprador

Das 68 ordens concluídas, 30 têm `closed_blocks_before_min` preenchido (blocos que
**faltavam** para o mínimo no momento do fechamento — valores como 25870 de 25920
significam canal fechado quase imediatamente). Mas o cruzamento com o lado do fechamento
mostra `seller_close_side = PEER` em 23 delas: **quem fechou cedo foi o comprador**, não o
vendedor.

Isso recalibra a prioridade: proteger o canal contra as nossas próprias automações
continua necessário, mas o fechamento precoce majoritário é do outro lado e está fora do
nosso controle. Não vale construir muita máquina em cima disso — e vale expor na UI que
canal Magma tem ~1/3 de chance de não durar o compromisso, porque isso muda o cálculo de
quanto capital vale a pena imobilizar.

#### Calibração de defaults da policy

Da distribuição real: preço 1500–11000 ppm, tamanhos 1M–6M sats, 180 dias em 45 das 85
ordens, e `locked_fee_rate_cap` de 500 em 58 delas. São os números para ancorar os
defaults do policy engine em vez de chutar.

### 2.3 Contrato de API — derivado do script em produção

**O script é a fonte de verdade.** Ele roda há tempo contra a API real, então a sequência
de chamadas e o uso de cada campo abaixo são fato observado, não inferência. A tabela de
tipos existe para não repetir em Go um erro que o Python esconde: lá tudo é string e vai
direto pro shell; aqui precisa de parse explícito.

#### Sequência exata

| # | Passo | Chamada | Campos usados |
|---|---|---|---|
| 1 | Achar ordem nova | `getUser.market.offer_orders.list` | filtra `status == WAITING_FOR_SELLER_APPROVAL` |
| 2 | Gerar invoice | `lncli addinvoice` | `--amt {seller_invoice_amount}` `--expiry 180000` `--memo "Magma-Channel-Sale-Order-ID:{id}"` |
| 3 | Aceitar | `sellerAcceptOrder(id, request)` | `request` = **bolt11** (`payment_request`), não o hash |
| 4 | Esperar pagamento | — | script dorme 300s; nós usamos `payment_status` |
| 5 | Achar ordem paga | `getUser.market.offer_orders.list` | filtra `status == WAITING_FOR_CHANNEL_OPEN` |
| 6 | Endereço do peer | `getNode(pubkey).graph_info.node.addresses[].addr` | monta `{pubkey}@{addr}`, usa o **primeiro** |
| 7 | Conectar | `lncli connect {pubkey}@{addr} --timeout 120s` | **falha é tolerada** (pode já estar conectado) |
| 8 | Abrir canal | `lncli openchannel` | `--node_key {account}` `--local_amt {size}` `--sat_per_vbyte {fastestFee}` `--utxo …` |
| 9 | Achar channel point | `lncli pendingchannels` | casa `channel_point.split(":")[0] == funding_txid` |
| 10 | Confirmar | `sellerAddTransaction(id, transaction)` | `transaction` = **`channel_point` completo (`txid:vout`)** |

Duas sutilezas do script que são fáceis de perder e quebram a venda:

- **Passo 3 manda o bolt11, não o `r_hash`.** A variável se chama `request` na mutation.
- **Passo 9 existe justamente para descobrir o `vout`.** O script não assume `:0` — ele lê
  o `channel_point` real do `pendingchannels`. Os dados confirmam que isso importa:
  entre as 85 ordens aparecem tanto `:0` quanto `:1`. Assumir `:0` quebraria uma parte
  das vendas.

#### Assinaturas GraphQL (do script, confirmadas na introspection)

```graphql
mutation AcceptOrder($sellerAcceptOrderId: String!, $request: String!) {
  sellerAcceptOrder(id: $sellerAcceptOrderId, request: $request)
}

mutation Mutation($sellerAddTransactionId: String!, $transaction: String!) {
  sellerAddTransaction(id: $sellerAddTransactionId, transaction: $transaction)
}
```

Ambas retornam escalar (não objeto). O script trata sucesso como
`data.sellerAcceptOrder` truthy, e erro como presença de `errors[]` no corpo — **HTTP 200
com `errors` é o caso normal de falha do Amboss**, então checar só o status code não basta.

#### Tipos — o que o Python esconde e o Go precisa tratar

| Campo | JSON | Go | Observação |
|---|---|---|---|
| `size` | `"1000000"` string | `int64` via `strconv.ParseInt` | sats; vai em `--local_amt` |
| `seller_invoice_amount` | `"4068"` string | `int64` | sats; vai em `--amt` da invoice |
| `buyer_invoice_amount`, `fixed_fee`, `variable_fee` | string | `int64` | sats |
| `amboss_fee_rate` | `"500"` string | `int64` | ppm |
| `locked_fee_rate`, `locked_base_fee` | number | `int64` | **preço**: ppm e sats |
| `locked_fee_rate_cap` | number | `int64` | ppm — teto de routing fee |
| `locked_base_fee_cap` | number `0`\|`1` | `int64` | **sats** — teto de base fee |
| `locked_min_block_length` | number | `int64` | blocos |
| `blocks_until_can_be_closed` | number \| null | `*int64` | só em ordem viva |
| `closed_blocks_before_min` | number \| null | `*int64` | blocos que faltavam ao fechar |
| `fee_above_cap_seconds` | string \| null | `*int64` | segundos |
| `payment_status` | string \| null | `*string` | null antes de haver pagamento |
| `channel_id` | `"820617x1293x0"` | string + parser SCID | **não é uint64** |
| `transaction_id` | `"…:1"` 66 chars | string | já vem `txid:vout` |
| `timeout` | `""` sempre | ignorar | campo morto |
| `is_automated`, `chat_enabled` | bool | `bool` | |
| `buyer_scores` | `[{metric,score,value}]` | slice de struct | `score`/`value` são string |

Regra: **todo campo numérico da API entra como `string` ou `null` em algum lugar**. Nada
de `json.Number` implícito ou `int` direto — decodificar em tipos tolerantes e validar,
porque payload de terceiro é entrada não-confiável (seção 8).

### 2.4 Pendências da Fase 0 — fechadas

**1. Unidade de `locked_base_fee_cap`: sats.** Nas ofertas, o mesmo objeto traz `base_fee`
(preço, valores 1136 e 50000) e `base_fee_cap` (só 0 ou 1) — escalas incompatíveis, logo
significados diferentes. Um cap de 1 msat seria absurdo; 1 **sat** é exatamente o default
do LND (`bitcoin.basefee=1000` msat). Então `base_fee_cap = 1` significa "pode manter o
default do LND" e `0` significa "base fee tem de ser zero".

Isso tem consequência direta no passo 2 da seção 7: **47 das 85 ordens têm cap 0**, e o
template do projeto nasce com 1 sat. Ou seja, a correção pontual dispara na **base fee**,
não na fee rate — o oposto do que a distribuição de `fee_above_cap_seconds` sugeria.
(Só 5 das 47 registraram violação, o que indica que `fee_above_cap_seconds` provavelmente
mede apenas a fee rate. Zerar a base fee quando o cap é 0 custa quase nada e elimina a
dúvida.)

**2. Formato de `sellerAddTransaction`: `txid:vout`.** Confirmado duas vezes — é o que o
script manda em produção, e é como o Amboss armazena: `transaction_id` tem 66 caracteres
com sufixo `:0`/`:1` em todas as 74 ordens que chegaram a abrir canal.

**3. Escopo do token: vira verificação em runtime.** O token de teste tem escopo de
vendedor. Não dá para afirmar o mesmo do token de produção sem usá-lo, e não é preciso:
o `Info()` do app faz uma chamada barata a `getUser { market { enabled } }` e, em caso de
401 ou `market` nulo, reporta `unavailable_reason: "token Amboss sem escopo de vendedor"`.
Incerteza vira estado tratado em vez de suposição.

#### Descoberta nova: o token expira

O token do Amboss é um **JWT** (`iss: amboss.tech`) com `iat`/`exp` — o de teste tem
exatamente 30 dias. Se o token de produção em `autofee_config.amboss_token` também for
JWT, ele **expira em silêncio**, e hoje nada no sistema avisa.

Para o Autofee isso degrada de leve (perde o seed do Amboss). Para o Magma é bem pior:
o pior caso possível é o token expirar **entre o passo 8 e o passo 10** — canal aberto,
capital comprometido, venda não confirmada. Então:

- decodificar o `exp` na leitura do token e expor "expira em N dias" na UI;
- alertar via Telegram com antecedência (7 dias);
- tratar 401 como categoria própria (`token_expired`), distinta de erro de rede, levando a
  ordem para `needs_attention` em vez de retry cego;
- **antes de abrir canal (passo 8), checar que o token ainda tem validade folgada.**
  Se estiver perto de expirar, não abrir — segurar a ordem e alertar. Abrir canal que não
  se consegue confirmar é o único erro realmente caro deste app.

Vale abrir isso também para o Autofee, que hoje tem o mesmo ponto cego.

Sem essa fase o resto é chute.

---

## 3. Onde encaixa no código

Modelo mais próximo já existente: **Loop Out BR⚡LN** — app "virtual" (sem Docker, sem
porta, sem systemd), serviço nativo dentro do manager, estado de install/enable no
Postgres, worker próprio, página de UI dedicada.

### Arquivos novos

| Arquivo | Papel |
|---------|-------|
| `internal/server/apps_magma.go` | `appHandler` (Definition/Info/Install/Uninstall/Start/Stop) — espelha [apps_loopout_brln.go](lightningos-light/internal/server/apps_loopout_brln.go) |
| `internal/server/magma_amboss.go` | Cliente GraphQL tipado (queries, mutations, backoff, rate limit) |
| `internal/server/magma_service.go` | Máquina de estados, poller, policy engine, reconciliação |
| `internal/server/magma_handlers.go` | Handlers HTTP finos |
| `internal/server/magma_init.go` | Init preguiçoso com mutex — espelha [loopout_brln_init.go](lightningos-light/internal/server/loopout_brln_init.go) |
| `internal/server/magma_*_test.go` | Stub httptest do Amboss + interface `magmaLND` fake |
| `ui/src/pages/MagmaSales.tsx` | Página do app |
| `ui/src/assets/apps/magma.png` | Ícone |

### Arquivos tocados

- `internal/server/apps_registry.go` — registrar `newMagmaApp(s)`
- `internal/server/routes.go` — grupo `/api/magma/*`
- `internal/server/server.go` — campos `magma*` + `s.magma.Start(runCtx)` no bloco de start
- `ui/src/api.ts`, `ui/src/App.tsx` (nav `apps`), `ui/src/pages/AppStore.tsx` (ícone + rota), i18n

### Reuso direto (nada disso precisa ser reescrito)

| Necessidade | Já existe |
|---|---|
| Abrir canal com UTXOs fixos | `lndclient.OpenChannelWithOutpoints` ([utxo_manager.go:97](lightningos-light/internal/lndclient/utxo_manager.go#L97)) |
| Abrir canal simples | `lndclient.OpenChannelWithPush` |
| Preview de custo do open | `lndclient.PreviewOpenChannel` |
| Conectar no peer | `lndclient.ConnectPeerWithTimeout` |
| Reconciliar pending open | `lndclient.ListPendingChannels`, padrão de `tryPromoteSessionByChannelState` do balanced open |
| Invoice | `lndclient.CreateInvoice` |
| Fee recomendada | `fetchMempoolJSON("https://mempool.space/api/v1/fees/recommended")` |
| Guarda de orçamento on-chain | `ensureBalancedOnchainBudget` (balanced_open_service.go:2845) — generalizar |
| **Desabilitar Autofee por canal** | `autofee_channel_settings (channel_id, enabled)` — [autofee_service.go:2173](lightningos-light/internal/server/autofee_service.go#L2173) |
| Telegram | `sendTelegramMessage` / `sendTelegramMessages` |
| **Token Amboss** | **já existe**: `autofee_config.amboss_token` — ver seção 3.1 |
| POST GraphQL Amboss | `fetchAmbossSeries` (autofee_service.go:12279) e [amboss_health.go](lightningos-light/internal/server/amboss_health.go) |
| Peers Tor alcançáveis | `tor_peer_checker_init.go` |

**Adição pequena necessária no lndclient:** `MinConfs` em `OpenChannelParams`
(o script usa `--min_confs=3`; hoje o struct não expõe).

### 3.1 Token Amboss — já existe no Postgres

O token **não precisa de plumbing novo**. Já está em `autofee_config.amboss_token`
(coluna `text`, linha única `id = autofeeConfigID`), usado pelo Autofee para puxar o seed
de fee via `getNodeMetrics.historical_series`:

- leitura: `fetchAmbossToken` / `cachedAmbossToken` (autofee_service.go:11991)
- consumo: `fetchAmbossSeries` (autofee_service.go:12279) — mesmo endpoint
  `https://api.amboss.space/graphql`, mesmo header `Authorization: Bearer <token>`
- escrita: `PATCH` do autofee config, campo `amboss_token`
- exposição: só o booleano `amboss_token_set` sai pela API; o valor nunca é retornado
  (autofee_service.go:1590). UI em [FeeCenter.tsx:1398](lightningos-light/ui/src/pages/FeeCenter.tsx#L1398).

**Decisão: fonte única, sem duplicar.** O Magma lê o mesmo campo através de um helper
compartilhado (`ambossAPIToken(ctx)`), extraído do `fetchAmbossToken` do autofee. A página
do Magma mostra o estado (`configurado` / `não configurado`) e permite gravar, escrevendo
na mesma coluna — com aviso na UI de que o token é compartilhado com o Fee Center.
Duplicar em `secrets.env` ou numa coluna própria criaria dois lugares para rotacionar e
um deles quebraria em silêncio.

Três consequências que precisam de atenção:

1. **Escopo do token.** O autofee só faz leitura de métricas públicas; o Magma precisa de
   `sellerAcceptOrder` e `sellerAddTransaction`, que movem dinheiro e abrem canal. Item de
   Fase 0: confirmar se o **mesmo** token consulta `getUser.market.offer_orders` e executa
   as mutations, ou se o Amboss emite chaves com escopos distintos. Se emitir escopos
   separados, adicionar coluna opcional `magma_amboss_token` em `magma_settings` que
   **sobrepõe** o do autofee quando preenchida — default continua herdar.
2. **Blast radius.** O token está em plaintext no Postgres e portanto sai nos `pg_dump`.
   Um token read-only de métricas vazado é chato; um token com escopo de vendedor vazado
   permite aceitar ordens em nome do nó. Vale registrar isso explicitamente, e é mais um
   argumento para a coluna separada do item 1 caso o Amboss suporte escopos.
3. **Cache.** `cachedAmbossToken` carrega uma vez por execução do engine (flag
   `ambossTokenLoad`), o que serve para um run curto. O poller do Magma é de vida longa:
   precisa reler o token na troca de settings ou expirar o cache por tempo, senão trocar o
   token pelo Fee Center não surte efeito até reiniciar o manager.

Efeito prático no escopo: some da Fase 1 todo o trabalho de configuração de credencial —
sobra só refletir o estado na UI do app e tratar o caso "token ausente" como
`unavailable_reason` no `appInfo`.

---

## 4. Persistência

Schema inline no serviço via `EnsureSchema` (padrão loopout/balanced open, não há
migrations em arquivo neste repo).

```
magma_settings          (linha única)
  installed, enabled, mode, poll_interval_sec,
  + todos os campos de policy da seção 5,
  updated_at

magma_orders
  order_id            text primary key      -- id do Magma, idempotência
  buyer_pubkey        text
  size_sat            bigint
  revenue_sat         bigint                -- seller_invoice_amount
  magma_status        text                  -- último status visto na API
  local_state         text                  -- máquina abaixo
  decision            text                  -- auto_approved | manual | rejected
  decision_reason     text
  invoice_hash        text
  payment_request     text
  sat_per_vbyte       bigint
  outpoints           jsonb
  funding_txid        text
  channel_point       text
  onchain_fee_sat     bigint
  commitment_until    timestamptz / block   -- prazo de permanência do canal
  attempt_count       int
  next_attempt_at     timestamptz
  last_error          text
  created_at, updated_at, completed_at

magma_order_events      (append-only, trilha de auditoria)
  id, order_id, event_type, detail jsonb, created_at
```

### Máquina de estados (`local_state`)

```
discovered
  → evaluating → rejecting → rejected  (policy negou → sellerRejectOrder, NÃO ignorar)
              → approved
  → invoicing → invoice_accepted        (invoice HODL-compatível + sellerAcceptOrder OK)
  → awaiting_payment                    (payment_status = PENDING_PAYMENT)
  → awaiting_open                       (status = WAITING_FOR_CHANNEL_OPEN)
  → connecting → opening                (write-ahead + check de validade do token, 2.4)
  → open_broadcast                      (funding_txid gravado)
  → confirming                          (sellerAddTransaction)
  → confirmed                           (VALID_CHANNEL_OPENING)
  → monitoring                          (dentro do compromisso: clamp de fee + proteção)
  → completed                           (CHANNEL_MONITORING_FINISHED)
Terminais/laterais: failed, expired, cancelled_by_buyer, rejected_by_buyer,
                    seller_failed, needs_attention
```

O estado `monitoring` é novo em relação ao desenho anterior e existe porque a venda **não
termina no channel point**: o compromisso de permanência e o teto de fee valem até
`CHANNEL_MONITORING_FINISHED`. Enquanto estiver aí, o canal está sob duas restrições
ativas (seção 7).

Mapeamento dos estados de falha do Amboss (todos geram alerta):
`SELLER_FAILED_TO_REACT`, `SELLER_FAILED_TO_OPEN_CHANNEL`, `SELLER_FAILED_TO_SEND_SWAP`,
`INVALID_CHANNEL_OPENING` → `seller_failed`; `BUYER_REJECTED`, `BUYER_FAILED_TO_PAY` →
`cancelled_by_buyer`; `ADMIN_CLOSED` → `needs_attention`.

**Regra dura:** nenhuma ordem em `opening` ou além pode chamar `OpenChannel` de novo sem
antes reconciliar contra `ListPendingChannels`/`ListChannels` procurando canal para aquele
pubkey com aquele tamanho. Write-ahead + reconciliação é o que substitui os arquivos de
lock do script.

---

## 5. Policy engine (modo `auto`)

Cada ordem passa por avaliação determinística; toda rejeição grava motivo legível.

**Econômico** — lembrar que `seller_invoice_amount` **já é líquido** (seção 2.2)
- `min_revenue_sat` — piso absoluto sobre `seller_invoice_amount`
- `min_price_ppm` — usar `locked_fee_rate` direto (é o preço em ppm; observado 1500–11000)
- `min_price_ppm_per_day` — `locked_fee_rate / (locked_min_block_length / 144)`. É o que
  impede aceitar canal travado 180 dias a preço de 7. Relevante na prática: 45 das 85
  ordens reais são de 180 dias.
- `min_fee_rate_cap_ppm` — recusar ordens cujo `locked_fee_rate_cap` seja baixo demais
  para o canal valer a pena depois de aberto. Vender inbound barato **e** ficar preso a
  um teto de fee ruim durante o compromisso é o pior negócio possível, e é invisível se
  só olharmos o preço da ordem. Observado: 10 ppm no pior caso, 500 na mediana.
- `max_onchain_cost_pct` — custo estimado do open sobre a receita. **Default 50%.**
  (O script declara `limit_cost = 0.90` e nunca usa — a checagem real é `fee >= invoice`,
  ou seja, 100%. Bug herdado que não replicamos.)
- `max_sat_per_vbyte` — teto de fee rate

**Capital**
- `min_channel_size_sat` / `max_channel_size_sat`
- `min_onchain_reserve_sat` — saldo on-chain que nunca pode ser cruzado
- `max_concurrent_pending_opens`
- `max_daily_orders`, `max_daily_size_sat`

**Contraparte**
- `buyer_allowlist` / `buyer_denylist` (pubkey)
- `min_buyer_score` — usa `buyer_scores` / `sides.taker_metrics` da própria ordem, que já
  vêm prontos do Amboss; heurística de grafo nossa fica como complemento, não como base
- `max_commitment_blocks` — recusa ordens com `locked_min_block_length` longo demais

**Recusa ativa.** Toda rejeição chama `sellerRejectOrder(id)`. Deixar a ordem morrer de
inanição gera `SELLER_FAILED_TO_REACT`, que é falha registrada contra o vendedor. Este é
provavelmente o maior custo silencioso do script atual, que nunca recusa nada.

**Execução**
- canal sempre **público** (Magma vende inbound público) — fixo, não configurável
- `push_sat = 0`, `min_confs` (default 3)
- `invoice_expiry_seconds` (default 180000)
- `close_address` opcional

### Fee alta: melhoria sobre o script

O script aborta quando a fee estoura o limite e nunca mais tenta. O correto:
ordem fica em `awaiting_open` com `next_attempt_at`, e o poller reavalia a cada tick.
Quando o deadline da ordem se aproxima (`blocks` restantes < limiar), duas opções por
config: abrir mesmo com prejuízo, ou escalar para `needs_attention` + Telegram e deixar
o operador decidir. Default: escalar.

---

## 6. Worker

Uma goroutine, intervalo configurável (default 90s, com jitter), respeitando `s.stop`
e o padrão de ctx do repo (o ctx do job começa quando ele roda). Por tick:

1. Query barata em `getUser.market { pending_seller_orders }`; só busca
   `offer_orders.list` completo se o contador mudou ou se existe ordem local não-terminal
2. Upsert em `magma_orders`; ordens novas → `discovered`
3. Reconciliação: ordens locais não-terminais que sumiram da API ou mudaram de status
4. Drive: cada ordem avança no máximo um passo por tick (evita cascata em caso de erro)
5. Backoff exponencial em 429/5xx (e em `errors[]` com HTTP 200); circuit breaker após N
   falhas seguidas + alerta

Nada de `sleep(300)` bloqueante como no script — o estado fica no banco, o tempo é do
poller. Duas esperas do script, porém, **são reais e ficam**: o intervalo entre abrir o
canal e o `pendingchannels` ver o canal (script usa 10s), e a folga antes de confirmar ao
Amboss. Em vez de `sleep` fixo, viram retry com backoff curto no passo 9 —
`get_channel_point` pode simplesmente ainda não encontrar o txid, e isso é normal, não erro.

---

## 7. Integrações que o script não tem e o nó precisa

**Proteção do compromisso.** O prazo agora tem nome: `locked_min_block_length`, com
contador ao vivo em `blocks_until_can_be_closed` e registro de infração em
`closed_blocks_before_min`. Ao confirmar a venda, marcar o canal como protegido e fazer o
**Close Manager** e o **Node Retirement** respeitarem (mesmo mecanismo do
`EffectiveProtected` do rebalance). Liberar quando a ordem chegar a
`CHANNEL_MONITORING_FINISHED`.

**Autofee — nascer fora dele, com correção pontual se o default furar o teto.**

O canal vendido nasce com a fee default do `lnd.conf`
(`bitcoin.feerate=1` ppm, `bitcoin.basefee=1000` msat no template do projeto), que é um
bom ponto de partida e está muito abaixo de qualquer `locked_fee_rate_cap` observado
(mínimo 10 ppm, mediana 500). Então o comportamento correto é:

1. **Ao confirmar a venda, gravar o canal como desabilitado no Autofee** —
   `autofee_channel_settings.enabled = false` para aquele `channel_id`
   ([autofee_service.go:2173](lightningos-light/internal/server/autofee_service.go#L2173)).
   O mecanismo por canal já existe e é exatamente o que a UI usa; o Magma só precisa
   escrever nele.
2. **Checar uma vez a fee efetiva contra os dois tetos da ordem.** Se a default estiver
   acima de `locked_fee_rate_cap` ou a base acima de `locked_base_fee_cap`, baixar
   imediatamente para logo abaixo do teto.

   Pela seção 2.4, **quem dispara na prática é a base fee**: `locked_base_fee_cap = 0` em
   47 das 85 ordens, contra `bitcoin.basefee=1000` msat (1 sat) do template — o canal
   nasceria violando. Já a fee rate (1 ppm contra cap mínimo de 10) nunca dispara com o
   template atual, mas o `lnd.conf` é editável pela UI de LND Config e um nó com `feerate`
   alto nasceria violando desde o primeiro bloco. Os dois lados precisam do check.
3. **Reabilitar o Autofee quando a ordem chegar a `CHANNEL_MONITORING_FINISHED`**,
   devolvendo o canal ao regime normal.

Os dados reais sustentam esse desenho: das 85 ordens, só 6 acumularam
`fee_above_cap_seconds`, e todas com `locked_fee_rate_cap = 500` — muito acima do 1 ppm de
nascença. Ou seja, **a violação veio de a fee ter sido elevada depois**, que é precisamente
o que o passo 1 impede. O passo 2 é rede de segurança barata para o caso de `lnd.conf`
customizado.

Vale expor `fee_above_cap_seconds` na UI como indicador de saúde: se estiver subindo,
alguma coisa está furando o teto agora — e, pelo histórico, essa coisa é uma automação
nossa.

**UTXOs.** Leases do UTXO Manager já protegem contra o LND gastar moeda reservada, então
o default pode ser deixar o LND selecionar. Seleção manual de outpoints fica como opção
avançada, reusando o preview existente.

**Relatório de vendas.** Fase tardia: receita vs. custo on-chain vs. forwarding real do
canal vendido, para saber se a venda de inbound compensa.

---

## 8. Segurança

- Token Amboss reusa `autofee_config.amboss_token` (seção 3.1). A API continua expondo só
  o booleano, nunca o valor — nem parcial. Atenção ao escopo de vendedor e ao fato de o
  token sair em backup de banco.
- Zero `shell=True`. O script interpola a pubkey vinda da API dentro de um comando de
  shell; nossa versão fala gRPC direto, o que elimina a classe inteira de injeção.
- Validar toda resposta da API antes de usar: pubkey em hex 66 chars, `size`/`revenue`
  positivos e dentro dos limites, status em enum conhecido, numéricos parseados de string
  (tabela de tipos na seção 2.3). Payload do Amboss é entrada não-confiável.
- Rate limit no cliente GraphQL.
- **HTTP 200 com `errors[]` é o modo normal de falha do Amboss** — nunca tratar `2xx`
  como sucesso sem inspecionar o corpo. O script já faz isso; é fácil perder em Go.
- Expiração de token (JWT, seção 2.4): 401 é categoria própria, e nenhum canal é aberto
  com token perto de vencer.
- Todo open registrado no audit service com order_id, valor, fee e decisão.
- Sem senha de confirmação por ordem (incompatível com auto); a autorização é o modo +
  a policy, e isso precisa estar explícito na UI ao ativar `auto`.

---

## 9. Fragilidades do script que não vamos herdar

O que segue é sobre **robustez de execução**, não sobre a semântica da API — nessa parte o
script está certo e é a referência (seção 2.3).


1. Arquivo de log como lock — trava o bot para sempre e exige apagar arquivo na mão.
2. `limit_cost = 0.90` declarado e nunca usado; a checagem real é 100% da receita.
3. `time.sleep(300)` dentro do handler congela o bot inteiro.
4. Processa só uma ordem por execução (`next(...)`).
5. ~~Seleção de UTXO com contabilidade furada.~~ **Retirado — reli e a acumulação está
   correta**: o greedy decrescente subtrai cada UTXO do restante e compara o próximo
   contra `restante + fee`, que é o teste certo; e o `fee_cost` retornado corresponde ao
   número de inputs efetivamente escolhido. O que sobra é uma borda estreita: a guarda
   inicial `total < channel_size` **ignora a fee**, então um saldo que cobre o canal mas
   não a fee passa na guarda, esgota o loop sem `break` e só falha na hora do
   `openchannel`. Correção: comparar contra `channel_size + fee estimada`.
6. Tamanho de input fixo em 57.5 vB — assume Taproot. Com UTXO P2WPKH (~68 vB) a fee sai
   subestimada.
7. Sem tratamento de falhas de open (peer offline, fundos insuficientes, reserva).
8. Sem verificação de confirmação do funding nem tratamento de RBF.
9. Tokens em constantes no código.
10. **Nunca chama `sellerRejectOrder`.** Ordem que não interessa é ignorada até virar
    `SELLER_FAILED_TO_REACT` — falha registrada contra o vendedor.
11. Trata 2 dos 17 status; todos os modos de falha passam despercebidos.
12. Decide em cima da receita **bruta**, ignorando `amboss_fee_rate` / `fixed_fee` /
    `variable_fee`.
13. Ignora `locked_fee_rate_cap` — abre o canal e deixa o Autofee furar o teto contratual
    sem ninguém perceber. **Observado nos dados reais**: 6 ordens acumularam até 5,9h
    acima do teto.
14. Ignora `locked_min_block_length` — nada impede outra automação fechar o canal dentro
    do compromisso.

---

## 10. Fases

| Fase | Entrega | Risco de fundos |
|------|---------|-----------------|
| **0** | **Concluída.** Schema via introspection (2.1) + 85 ordens reais analisadas (2.2). Restam 3 pendências menores que não bloqueiam | nenhum |
| **1** | App no store + poller + tabela de ordens + página de listagem + alertas Telegram. **Modo monitor apenas.** Sem trabalho de credencial — token já existe | nenhum |
| **2** | Modo `assisted`: botão "Aceitar ordem" (invoice + sellerAcceptOrder) e botão "Abrir canal" com preview de fee/UTXO; confirmação automática do channel point; reconciliação e `needs_attention` | controlado, 1 clique por passo |
| **3** | Modo `auto`: policy engine completo, guardas de orçamento, limites diários, backoff de fee | automatizado |
| **4** | Proteção de compromisso no Close Manager + política de Autofee, gestão da oferta pela UI, comando `/magma` no Telegram, relatório de P&L da venda | — |

Cada fase é entregável e commitável sozinha, direto na main no padrão
`0.4.X Beta - Magma Sales - <descrição>`.

---

## 11. Testes

- Stub `httptest` da API Amboss servindo as **fixtures reais das 85 ordens** capturadas na
  Fase 0 → testa o cliente GraphQL e o parsing de todos os status observados, incluindo os
  campos nulos e os numéricos-como-string da seção 2.3.
- Teste de regressão de tipos: decodificar as 85 ordens reais e verificar que nenhum campo
  vira zero silenciosamente por parse falho. É o erro mais provável na tradução do Python.
- Teste do `vout`: fixture com `channel_point` terminando em `:1`, garantindo que o
  `sellerAddTransaction` recebe o outpoint completo e não o txid.
- Teste de token expirado: 401 no meio do fluxo não pode gerar retry cego nem abertura de
  canal.
- Interface `magmaLND` (espelhando `loopOutBRLNLND`) com fake → testa a máquina de estados
  sem LND: crash no meio do `opening`, `sellerAddTransaction` falhando após o canal já
  aberto, ordem cancelada pelo comprador entre ticks, fee estourando o teto.
- Tabela de casos do policy engine (aceita/rejeita + motivo).
- `go test ./internal/server -run TestValidateAppRegistry`.
- Modo dry-run: executa o pipeline inteiro e para antes de `sellerAcceptOrder`/`OpenChannel`,
  logando o que faria. Útil para validar policy em produção sem risco.

---

## 12. Decisões em aberto

1. **ID do app**: `magma-sales` ou `magma`? (sugestão: `magma-sales`, deixa espaço para
   um app de compra depois)
2. Modo `assisted` deve permitir aprovar pelo **Telegram** (botão inline) ou só pela UI?
   Telegram é muito mais prático para quem não está no painel, mas amplia a superfície
   de autorização para fora do painel autenticado.
3. ~~Autofee: excluir o canal vendido ou perfil próprio?~~ **Resolvido:** nasce com
   `autofee_channel_settings.enabled = false`, correção pontual se a default do `lnd.conf`
   furar o teto, reabilita em `CHANNEL_MONITORING_FINISHED` (seção 7).
4. Fase 4 inclui gerenciar a **oferta** (`min_size`/`max_size`/`min_block_length`/
   `fee_rate_cap`/`onchain_priority`) pela UI, ou isso fica no site do Amboss? Os campos
   e as mutations (`createOffer`/`updateOffer`/`toggleOffer`) existem.
5. Token compartilhado com o Fee Center ou coluna própria em `magma_settings`? Default do
   plano: compartilhar, com override opcional.
6. Usar `sendMagmaMessage` para avisar o comprador (ex.: "fee alta, abrindo em X") ou
   deixar o chat fora do escopo?

---

## 13. Fase 0 — encerrada

Executado em 2026-07-28 com token de teste (30 dias, conta separada da de produção):

- schema completo por introspection, que está **aberta e sem auth** (seção 2.1);
- 85 ordens reais perfiladas, identidades econômicas verificadas em 85/85 (seção 2.2);
- contrato de chamadas e tabela de tipos derivados do script em produção (seção 2.3);
- as três pendências fechadas (seção 2.4).

**Nenhuma mutation foi executada** — só leitura. `sellerAcceptOrder` e
`sellerAddTransaction` movem dinheiro e assumem compromisso de canal; ficam para o código,
com teste contra stub.

Fixtures salvas a partir das respostas reais alimentam os testes da Fase 1. Não há mais
bloqueio para começar a implementação.
