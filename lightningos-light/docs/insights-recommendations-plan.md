# Insights e Recomendações: plano (nov/2026)

## Status

Desenho. Nenhum código. Este documento propõe a funcionalidade, o catálogo
inicial de regras e a ordem de entrega. As perguntas em aberto estão no fim.

## Por que

A semana de 30/10 a 03/11/2026 no Friendspool e no BRLN HUB mostrou que os
maiores movimentos de resultado não vieram dos motores automáticos. Vieram de
decisões estruturais do operador, tomadas depois de alguém cruzar à mão os
sinais de quatro ou cinco telas:

1. **Canal novo no HUB.** No primeiro dia ele vendeu ~5M a 2.500 ppm, mais
   receita que o HUB inteiro em duas semanas. Depois disso o rebalance passou
   a falhar em todas as tentativas. A rota custava ~1.950 ppm e o teto do
   canal é 2.000 (0,8 × 2.500). Nenhuma tela dizia "este canal vale um Loop In".
2. **Maior vendedor sink do FP.** Um Loop In com último salto custou 1.249 ppm,
   e o lote vendeu a ~2.150 em menos de 24h. Outro canal com o mesmo peer, de
   8M, foi aberto com capital on-chain parado e vendeu 6,4M em um dia.
   O rebalance desse peer não fecha. Além disso, como há vários canais com o
   mesmo peer, ele fica fora do caminho rápido e da escada de descoberta.
3. **Compra sem venda.** Uma opção nova de rebalance (`peer_fee_as_cost_enabled`)
   comprou 8M em canais de demanda fraca. Foram ~5k sats pagos para ~250
   vendidos em 44h. Só uma auditoria manual mostrou isso.
4. **Bloqueio que parece defeito.** O operador viu "o rebalance do HUB não
   acontece". A investigação mostrou três causas: alvos barrados por estoque
   pago ainda não vendido (por desenho), alvos já no alvo, e rotas no teto.
   Só o último caso pedia ação.

Os motores (AutoFee, autopilot, AutoTarget, Interlock) reagem bem a preço e a
estoque. Não preveem peer fechando, demanda súbita nem rota sumindo. Também
não escolhem entre ferramentas que não controlam, como Loop In, abrir canal ou
fechar canal. Essa escolha é humana e continua humana. O que falta é uma tela
que diga, em sats, o que fazer agora e por quê.

## O que já existe

| Fonte | O que entrega | Limite para recomendar |
|---|---|---|
| Channel Ranking (`/api/lnops/channel-ranking`) | estado por canal (expand/keep/monitor/close), score 7d/30d, receita direta e assistida, `recommendations` com códigos como `consider_more_capacity` | diz **o que o canal é**, não **o que fazer agora**; não sabe se o rebalance já falhou no teto, nem quanto vale cada ferramenta |
| Rebalance (`/api/rebalance/channels`) | `autopilot_reason`, `sale_price_ppm`, `fee_budget_ppm`, `econ_ratio_override`, `revenue_7d_sat`, `no_sale_30d` | motivo de um scan, sem contexto de dias; `/api/rebalance/history` lê só 24h, embora o banco guarde tudo |
| Sovereign history (`/api/rebalance/sovereign-history?include_decisions=1`) | decisão por canal em cada scan, inclusive degraus da escada | apagado após 48h |
| Interlock (`/api/lnops/automation-intents`) | `refill_target` (drained, extreme, low_outbound, productive) com `first_seen_at`; `protect_fee_floor` | diz que o canal está vazio, não o que fazer se o rebalance não repõe |
| AutoFee (`/api/lnops/autofee/{outcomes,price-experiments}`) | resposta a cada mudança de fee; vereditos `shadow_sold` / `shadow_no_demand` | demanda por canal, sem decisão de capital |
| Novos Canais (`/api/channel-ranking/open-candidates`) | candidatos a peer novo com `demand_score`, `route_cost_ppm`, razões | não considera o capital disponível nem o que já fazemos com peers existentes |
| Loop (`POST /api/apps/loop/quote`) | cotação Loop In/Out com `last_hop_pubkey` | sob demanda; consulta serviço externo |
| Notificações (`/api/notifications?type=forward|rebalance`) | forwards e rebalances liquidados, retenção de 1 ano | sem tentativas falhas |

Todas as regras abaixo usam só essas fontes. A funcionalidade não cria
heurística de preço própria; cruza o que os motores já calculam.

### Histórico que já existe (não refazer)

| Dado | Retenção | Onde foi feito |
|---|---|---|
| Policies (fee) do próprio nó, `fee_logs` do detalhe do canal | 365 d (`own_history_retention_days`, máx 730) | 0.5.48, `docs/own-policy-history-retention-0.5.48.md` |
| Forwards e rebalances liquidados (notificações) | 1 ano | existente |
| `rebalance_jobs` / `rebalance_attempts`, inclusive falhos | sem poda | existente; só a leitura `/history` filtra 1 dia |
| Lotes pagos do stock gate | 2× a janela de inventário | 0.5.42 |
| Outcomes e experimentos do AutoFee | tabelas próprias, sem janela de 24 h | 0.5.43 |
| `rebalance_sovereign_history` (motivo por canal por scan) | **48 h** | único dado que a fase 1 precisa passar a guardar |

Qualquer item deste plano que pareça "ampliar histórico" deve antes conferir
esta tabela e o código de poda (`delete from <tabela>`).

## Onde fica

Item novo **Insights** (pt-BR: **Insights**) no grupo **Lightning** do menu,
na primeira posição, acima do Fee Center. A tela é o ponto de entrada do
grupo: diz em qual das outras telas agir e com que ferramenta.

Não entra como aba do Channel Ranking por três motivos:

- metade das recomendações não é de um canal: abrir canal novo, capital
  on-chain parado, gasto de rebalance sem venda no nó;
- a ordem é por valor em sats/dia, não por score do canal;
- o Ranking continua sendo a visão "o que este canal é". Insights lê o Ranking
  e manda para lá quando a resposta é "olhe este canal".

O desenho da tela (blocos, anatomia do card, contrato backend → UI, i18n,
arquivos) está em [insights-recommendations-ui.md](insights-recommendations-ui.md).

## Modelo de uma recomendação

```text
id, node-local
kind            repor_vendedor | mais_capacidade | parar_compra | capital_parado
                | abrir_canal | preco_fora | bloqueio_esperado
channel_id      opcional (abrir_canal e capital on-chain não têm)
peer_pubkey     opcional
title           frase curta: "Repor <canal> via Loop In"
evidence[]      números com fonte: venda 7d, local %, falhas 24h, teto, custo
action          { tool: loop_in | econ_override | open_channel | close_channel
                         | rebalance_target | autofee_review | none,
                  params: valor, último salto, teto sugerido }
value_sat_day   estimativa do ganho diário se a ação for feita (ver abaixo)
cost_sat        custo único estimado (swap, abertura, fechamento)
confidence      alta | média | baixa (amostra de venda, idade do canal)
priority        ordem = value_sat_day × confiança, com piso por kind
guards[]        guardas que passaram ou bloquearam
invalidate_when condição que tira a recomendação da lista
created_at, last_seen_at
status          aberta | seguida | dispensada | expirada
outcome         preenchido 7 dias depois (fase 2)
```

Uma recomendação é recalculada a cada ciclo. Enquanto as condições valem, ela
mantém o mesmo `id` e atualiza `last_seen_at`. Quando a condição de
invalidação acontece, expira e guarda o último retrato.

### Valor estimado

O valor parte só do que **já aconteceu**, nunca de previsão:

- venda observada: `revenue_7d / 7` e o `ppm` médio dos forwards de saída 7d;
- custo de reposição observado: `rebalance_cost_7d_ppm` do canal; se não
  houver, a cotação de Loop pedida pelo operador ou o último degrau da escada;
- `value_sat_day = volume_vendido_dia × (ppm_venda − ppm_reposição) / 1e6`,
  limitado pelo volume que a ação consegue repor (tamanho do Loop, capacidade
  do canal novo).

Quando a amostra é fina (menos de 3 dias com venda, canal com menos de 7 dias)
a confiança é baixa e a tela diz isso. A regra de não codar em cima de
correlação fraca vale aqui também: recomendação de baixa confiança aparece,
mas não sobe para o topo.

## Catálogo inicial de regras

Cada regra lista o caso real que ela teria pegado. Na fase 0, a regra precisa
acertar esses casos e não gerar ruído no resto do histórico.

### R1. Vendedor vazio, rebalance preso no teto → repor por outra ferramenta

Condição:
- `revenue_7d` entre os 20% maiores do nó (percentil 80);
- local `≤` alvo e intent `refill_target` ativa há `≥ 12h`;
- jobs de 24h para o canal todos falhos com "all sources failed" / "no route",
  ou `autopilot_reason` em `target_structural_cooldown`,
  `route_dead_opportunity_below_floor` ou `low_success_opportunity_below_floor`.

Ação, na ordem:
1. Se o último custo pago (`rebalance_cost_7d_ppm`) estiver entre 90% e 100%
   do teto, e o preço de venda ainda cobrir o custo pelo mesmo piso do AutoFee
   (`venda ≥ custo × 1,10`; sink: `× (1,10 + margem extra do perfil)`,
   0,03–0,06), sugerir `econ_ratio_override` 0,9 no canal. Mostrar o teto
   novo e a margem que sobra. A margem mínima não é um número novo: é a
   regra de piso que o AutoFee já aplica.
2. Senão, sugerir **Loop In com último salto no peer**. O botão "cotar" chama
   `POST /api/apps/loop/quote` com `last_hop_pubkey`. A cotação só sai por
   clique, porque é chamada a serviço externo. Com a cotação, a tela mostra a
   margem por ppm e o valor.

Casos: canal novo do HUB (teto 2.000, pagava ~1.950; Loop In estimado ~1.250);
maior vendedor sink do FP (Loop In de 1.249 contra venda de ~2.150).

### R2. Esgota rápido → mais capacidade com o peer

Condição: o canal vendeu `≥ 60%` da capacidade em `≤ 2` dias pelo menos duas
vezes em 14 dias, e a venda cobre o custo pelo piso do AutoFee (R1).

Ação: abrir mais capacidade. A tela mostra sempre os dois caminhos, com o
custo de cada um, e o operador escolhe:
- **canal maior que substitui o atual** (fechar depois de esvaziar). Mantém um
  canal só com o peer, então o rebalance segue usando o caminho rápido e a
  escada;
- **segundo canal com o mesmo peer**. É mais rápido, mas o peer passa a ter
  canais paralelos e sai do caminho rápido e da escada. Hoje esse é o
  principal motivo de o maior vendedor sink do FP não ter reposição
  automática. Esse aviso aparece sempre.

O capital disponível vem do saldo on-chain confirmado. Sem capital, a
recomendação vira "liberar capital" e aponta para a R4.

Caso: segundo canal de 8M com o maior vendedor sink do FP.

### R3. Compra sem venda → parar de comprar este alvo

Condição: rebalance pago no canal `≥ N sats` em 72h, e venda posterior de
`≤ 20%` do custo nas mesmas 72h; ou ppm de compra `≥` ppm de venda observado.

Ação: revisar o alvo do canal (`target_outbound_pct`) ou desligar o
rebalance só dele. Não desligar opção global nenhuma: a recomendação é do
canal, e a decisão de opção global vem da auditoria.

Caso: alvo com peer caro comprado a ~1.500 ppm para vender a ≤1.685, com
223 sats de venda em 7 dias.

### R4. Capital parado → candidato a fechar ou realocar

Condição: `no_sale_30d`, ou estado `close` no Ranking por 14 dias, ou
veredito `shadow_no_demand` em dois experimentos seguidos. Além disso, receita
assistida 30d abaixo de um piso. Um canal isca que traz assistida não é
capital parado.

Guardas (bloqueiam):
- compromisso Magma ativo (`/api/apps/magma-sales/commitments`), quando o
  app Magma está instalado;
- canal com menos de 30 dias;
- modo parked não é ação desta regra: parked é só para canal que já vai
  fechar.

Ação: "revisar fechamento" no Lightning Ops (fluxo de fechamento existente,
`POST /api/lnops/channel/close`). Mostrar o capital liberado e para onde ele
iria (R2 ou R5).

### R5. Abrir canal novo

Condição: candidato do Novos Canais com `confidence` alta, e capital on-chain
disponível ou liberável pela R4.

Ação: abrir canal. O botão leva ao fluxo de abertura existente
(`/api/lnops/channel/open-preview` → `open`), com peer e valor preenchidos. A recomendação diz o que o candidato resolve: rota que
pagamos caro (`route_cost_ppm`) ou demanda observada (`demand_score`).

### R6. Preço fora do preço real

Condição: AutoFee ligado, estoque `≥ 30%`, sem venda há 7 dias, e fee
`≥ 1,3 ×` o preço de venda realizado em 21 dias. Antes de sugerir baixar,
conferir a receita assistida do canal: se a assistida 7d for maior que a
direta, o canal é isca e a regra não dispara.

Ação: "revisar no Fee Center" ou reset de referência do AutoFee (já existe).

### R7. Bloqueio esperado (informativo)

Lista recolhida, no fim da tela: canais que parecem parados, mas estão
parados por desenho, com o motivo em uma linha. Exemplos: estoque pago ainda
não vendido (`paid_stock_gate`), no alvo, cooldown após compra. Serve para o
operador não mexer onde não precisa.

Caso: "o rebalance do HUB não acontece" (03/11), em que 2 dos 5 alvos estavam
barrados por estoque pago.

## Guardas gerais

- **Só recomenda.** Nenhuma regra executa ação. Os botões abrem a tela da
  ferramenta preenchida; a confirmação continua lá.
- **Sem chamada externa automática.** Cotação de Loop só por clique.
- **Nada de venda Magma** nas recomendações de lucro: a meta é roteamento.
- **Regra de preço por tipo de canal:** sink sem entrada não recebe sugestão de
  vender abaixo do custo de reposição; se não fecha a conta, a recomendação é
  fechar (R4), não baixar fee.
- **Lucro do nó, não do canal:** antes de R3, R4 e R6 a regra lê a receita
  assistida.
- **Independente de quem opera.** As regras usam só dados do próprio nó e
  dos apps instalados. Nada depende do BR⚡LN, de listas de peers ou de
  configuração externa: o produto é de uso geral. Preferências do operador
  (por exemplo, nunca sugerir fechar certos peers) ficam para uma lista de
  exclusão local, opcional, numa fase posterior.

## Medição do resultado (fase 2)

Cada recomendação seguida ou dispensada recebe, 7 dias depois:

- receita direta e assistida do canal, antes × depois;
- custo da ação (swap, abertura, rebalance);
- `value_sat_day` previsto × realizado.

A tela mostra, por regra, quantas foram seguidas, a taxa de acerto e o erro
médio da estimativa. Regra que erra muito perde prioridade ou sai. É o mesmo
método dos outcomes do AutoFee.

## Fases

### Fase 0: teste no histórico (sem código no produto)

Script em `scripts/analysis/insights_backtest.py` que roda as regras R1 a R4
dia a dia, nos últimos 60 dias dos dois nós, com notificações, ranking e
intents. Mede:

- quantas recomendações cada regra teria gerado por dia (ruído);
- se pegou os casos desta semana;
- para R1 e R2, quanto o operador teria ganho seguindo, usando a venda que de
  fato aconteceu depois.

Dados para janelas longas:

- `rebalance_jobs` e `rebalance_attempts` não têm limpeza: o histórico
  completo, inclusive jobs falhos, está no banco. Só a leitura
  `GET /api/rebalance/history` filtra `completed_at >= now() - 1 day`.
  Pré-requisito da fase 0: aceitar `from`/`to` (ou `days`) nesse endpoint,
  só leitura, com limite de linhas. Assim o HUB, que só tem acesso pela API,
  também entra no teste;
- `GET /api/rebalance/metrics/baseline?days=N` já dá jobs, falhas e custo por
  dia, mas do nó inteiro, sem quebra por canal;
- `GET /api/rebalance/pair-stats?target_channel_id=` dá sucesso e falha
  acumulados por par fonte → alvo (até 60 dias), sem série diária;
- `rebalance_sovereign_history` (decisão e motivo por canal a cada scan) é
  apagado depois de 48h. Para dias anteriores, o motivo do autopilot não é
  recuperável. A fase 1 grava um retrato diário por canal para cobrir isso.

### Fase 1: serviço e tela só de leitura

- `internal/server/insights_service.go` / `_handlers.go` / `_init.go`, no
  padrão do projeto;
- ciclo a cada 30 min, reaproveitando os snapshots que os serviços já mantêm
  (sem chamada nova ao LND além do que o Ranking já faz);
- tabela `insights_recommendations` com o modelo acima;
- `GET /api/lnops/insights`, `POST /api/lnops/insights/{id}/dismiss`;
- tela `ui/src/pages/Insights.tsx` no grupo Lightning, com as regras que
  passaram na fase 0.

### Fase 2: resultado

Tabela `insights_outcomes`, job diário que fecha a medição de 7 dias, e o
quadro de acerto por regra.

### Fase 3: atalhos

- botões preenchidos para Loop In, abertura e fechamento de canal.
- Sem alerta no Telegram: a tela basta (decisão de 03/11).

## Fora do escopo

- execução automática de qualquer ação;
- LLM ou texto livre gerado: o texto vem de modelos fixos com os números;
- recomendações entre nós (FP × HUB). Cada nó roda as suas.

## Decisões (03/11/2026)

- "Vendedor" = percentil 80 de `revenue_7d` do nó. Sem piso absoluto em sats.
- Margem mínima da R1 = piso do AutoFee (custo × 1,10; sink com margem extra
  do perfil). Nenhum limiar novo.
- R2 mostra sempre as duas opções (canal maior que substitui × segundo canal).
- Sem Telegram.
- Nenhuma dependência do clube BR⚡LN ou de listas externas: o app é de uso
  geral.

- Piso de receita assistida da R4 (capital parado): assistida 30d menor que
  10% da receita direta média dos canais do nó. Valor inicial, a revisar na
  fase 0.

## Implementação (04/11/2026)

Fases 0 e 1 feitas na branch `agent/0.5.53-insights`:

- `internal/server/insights_rules.go` — motor puro R1–R7 (`buildInsights`);
  `insights_service.go` — tabela `insights_recommendations` só com a decisão
  do operador; `insights_handlers.go` — `GET /api/lnops/insights`,
  `POST /api/lnops/insights/recompute`, `POST /api/lnops/insights/{id}/status`
  (`open|dismissed|done`, motivo `later|disagree|done_manually`);
  `insights_init.go` — padrão dos outros módulos. Cache de 5 min.
- `insights_fixture_test.go` — a fase 0: roda o motor sobre JSON capturado
  da API de um nó (`INSIGHTS_FIXTURE_DIR=<dir> go test ./internal/server -run
  TestInsightsFixture -v`).
- UI em `ui/src/pages/Insights.tsx` + `pages/insights/*`, item **Insights**
  primeiro no grupo Lightning, namespace `insights` nos dois idiomas com
  teste de paridade (`ui/tests/insights-i18n.test.mjs`).

Calibração com os dois nós do operador em 04/11 (antes → depois):

| Ajuste | Motivo | Resultado |
|---|---|---|
| R5 exige volume 30d ≥ 1M e mostra só os 5 maiores por valor | 30 candidatos no HUB, muitos com centenas de sats de volume | FP 11 → 5, HUB 30 → 5 |
| R6 "acima do preço" só para fee fixada à mão | AutoFee já aplica o teto pelo preço real; apontar os canais dele seria ruído e contraria o desenho | FP 7 → 0 |
| R6 "abaixo do piso" exige folga ≥ 10% do piso | RA⚡KO 372 vs 376 ppm não é decisão | FP 3 → 0 |
| R3 exige custo 7d ≥ 100 sat | lotes de 14–78 sats | HUB 3 → 1 |

Total: FP 37 → 19 recomendações (4 R1, 4 R3, 1 R4, 5 R5, 5 R7), HUB 40 → 13
(2 R1, 1 R3, 4 R4, 5 R5, 1 R7). Os casos reais foram pegos: coinos e kappa em
R1 no FP (Loop In), canal novo do HUB em R1 (sem econ 0,9 porque é sink e o
piso ×1,14 não cobre; Loop In), BitGo e lancrypt em R3, bfx-lnd0 não aparece
em R4 porque vendeu 5k em 30 dias (regra exige zero venda direta).

### Calibração por tamanho do nó e portão de 7 dias (04/11, tarde)

Os insights precisam funcionar em qualquer nó, não só nos dois do operador.
Dois mecanismos, ambos no motor (`insights_rules.go`), sem parâmetro novo:

- **Portão de maturidade.** `insightReadinessFor` lê o maior
  `peer_sample_count_30d` do ranking; abaixo de 7 dias inteiros o motor não
  roda regra nenhuma e a API devolve `node.ready=false` com
  `observed_days/required_days`. A tela mostra "Aquecendo: N de 7 dias" com
  barra de progresso e o mapa dos sinais, em vez do estado vazio. Nó recém
  instalado não recebe recomendação.
- **Escala pelo nó.** `insightCalibrate` passa o ranking pela mesma
  `classifyRebalanceNode` que o Rebalance Center usa (espelho do
  `calibrateNode` do AutoFee) e mapeia a classe nos três limiares que eram
  sats fixos:

  | Classe | Capital parado (R4) ≥ | Volume do candidato (R5) ≥ | Custo do lote (R3) ≥ |
  |---|---|---|---|
  | small / unknown | 250k | 250k | 30 sat |
  | medium | 500k | 500k | 60 sat |
  | large | 1M | 1M | 100 sat |
  | xl | 2M | 2M | 200 sat |

  O limiar de capital parado nunca passa de metade da capacidade média do
  nó (piso 100k), para um nó de canais pequenos enxergar o próprio capital.
  A liquidez muda o corte de saldo local do R4: 60% equilibrado, 70% cheio,
  50% drenado. Percentis e razões (vendedor p80, 20% vendido, 30% de margem,
  piso do AutoFee) são iguais em qualquer nó. `large` mantém os valores da
  calibração acima; no replay FP é `large/balanced` (19, igual) e HUB é
  `medium/balanced` (13, igual). A API expõe tudo em `node` e a tela mostra
  a pílula "Nó médio · equilibrado · 30 dias de dados" com os limiares no
  tooltip e um bloco na gaveta "Como funciona".

Pendente para a 0.5.53: validar a tela num nó de verdade (deploy), fase 2
(resultado antes × depois) fica para depois.

## Perguntas em aberto

Nenhuma. O próximo passo é o plano detalhado da fase 0 (arquivos, o que já
existe, testes de regressão), para aprovação antes de qualquer código.
