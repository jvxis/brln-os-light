# Sovereign: trava de estoque e escada de descoberta — 0.5.40

## Diagnóstico que motivou a mudança (Friendspool, 2026-10-02)

A receita de forward quase dobrou depois da 0.5.36 (3,1k → 5,9k sat/dia), mas o
líquido não acompanhou (648 → 906 sat/dia): o rebalance passou a custar 85% da
receita. Olhando os últimos 7 dias por canal:

| Grupo | Custo de rebalance | Fee vendida |
|---|---|---|
| ln.coinos.io | 7.469 | 10.042 |
| Sinks que pagam (PurpleWisteria, WCC, CLB, Garlic, Authenticity...) | 8.527 | 15.154 |
| Orgânico, sem compra (Nova, kappa, Zap, Strike...) | 0 | 7.158 |
| bfx-lnd0, 3 canais | 9.804 | 2.172 |
| 12 canais sem retorno | 6.365 | 60 |

Metade do gasto foi para canais que não venderam. O bfx-lnd0 terminou a semana
com 10,45M de estoque, uns 26 dias da demanda dele.

Causa no código: o guard de estoque não vendido
(`rebalance_unsold_inventory.go`) bloqueia a recompra só nas primeiras 4 horas
depois do último lote pago (`sovereignUnsoldPaidLiquidityHardAge`). Depois disso
o alvo volta a ser comprado com um multiplicador de score, e só volta a ser
bloqueado depois da janela de atribuição se o caso for "severo". A conta é por
canal, então três canais com o mesmo peer acumulam estoque em paralelo.

## Trava de estoque ("vendeu, repõe")

Opt-in. Com `stock_gate_enabled` desligado nada muda.

- `stock_gate_enabled` (padrão false).
- `stock_gate_min_stock_pct` (padrão 10, faixa 1-50): estoque de trabalho. É o
  estoque pago não vendido sempre permitido, em % da capacidade do canal alvo.
- `stock_gate_cover_days` (padrão 3, faixa 0-14): um peer que vende pode manter
  esse número de dias das próprias vendas (média da janela), quando isso for
  maior que o estoque de trabalho. 0 desliga a cobertura.

Como mede (`buildSovereignStockLevels`):

- Usa a mesma atribuição FIFO do guard existente
  (`attributeRebalanceForwardsFIFO`). Desde a 0.5.42 a trava lembra os lotes por
  o dobro da janela slow-seller (14 dias por padrão): com 7 dias, estoque
  comprado há 8 dias sumia da conta enquanto continuava no canal e o autopilot
  comprava de novo em cima dele. A demanda (vendas por dia) continua na janela
  de 7 dias.
- Estoque não vendido = soma do que sobrou de cada lote Sovereign da janela.
  Lotes manuais e lotes de descoberta consomem vendas na fila, mas não contam
  como estoque do autopilot, igual ao guard existente.
- Agregado **por peer**: soma todos os canais com o mesmo nó. O LND encaminha
  por qualquer canal do peer, então a demanda é uma só.
- Limitado ao saldo local atual do peer. Liquidez que saiu por outro caminho
  (pagamento, uso como source) não é mais estoque.
- Demanda = volume de forwards de saída do peer na janela.

Quando trava (`sovereignStockGateBlocks`):

```
permitido = max(min_stock_pct × capacidade do canal alvo,
                cover_days × vendas por dia do peer)
trava se estoque não vendido >= permitido
```

Regra fixa: **nunca trava antes de o peer ter 2 lotes não vendidos**
(`sovereignStockGateMinLots`). Um lote conta como não vendido enquanto mais da
metade dele ainda está lá. Assim um canal drenado sempre recebe os primeiros
refills, e a trava só age quando o canal já recebeu liquidez o suficiente.

O skip aparece como `paid_stock_gate`. A decisão carrega `stock_unsold_sat`,
`stock_allowed_sat` e `stock_demand_sat`, e o detalhe do autopilot mostra
"Estoque pago ainda não vendido: X (compras pausam em Y)". Vale também para
slots de exploração e para canais em manual restart que entram no escopo do
Sovereign. Slot garantido do operador e rebalance manual não passam pela trava.

Se o banco falhar ao carregar os lotes, a trava não bloqueia (o guard existente
já pausa as compras nesse caso).

Ordem de grandeza com os agregados de 7 dias da semana (não é simulação lote a
lote): com o estoque de trabalho em 10%, a trava teria segurado perto de 6k dos
16k gastos nos canais sem retorno; em 5%, perto de 8k.

## Escada de descoberta

Opt-in. Com `discovery_steps = 0` (padrão) nada muda.

A sonda manual de 28/09 mostrou que 11 dos 14 canais `peer_close_risk` tinham
rota, 8 delas acima do teto `fee × econ_ratio`. "Sem rota" era "sem rota a esse
preço". A escada automatiza essa sonda, sem repor o canal.

- `discovery_steps` (padrão 0, faixa 0-4; recomendado 2): degraus acima do
  teto normal.
- `discovery_ceiling_pct` (padrão 150, faixa 110-200): teto do último degrau em
  % do teto normal. Com 2 degraus e 150% as sondas rodam a 125% e 150%.
- `discovery_daily_budget_sat` (padrão 300; 0 desliga): máximo que as sondas
  gastam por dia.

Quem entra (`maybeQueueSovereignDiscovery`):

- A decisão do scan foi barrada por um gate de rota
  (`target_structural_cooldown`, `route_dead_opportunity_below_floor` ou
  `low_success_opportunity_below_floor`).
- O canal tem intent `refill_target` do AutoFee com motivo drained ou
  extreme-drained, visto há mais que `keepalive_refill_after_hours`. Exige o
  Automation Interlock ligado (shadow ou enforce), como o keepalive.
- O fast-path delegado está ligado.

Limites fixos:

- Valor = valor mínimo de rebalance (`min_amount_sat`), nunca abaixo de 20k sats
  (0.5.41): abaixo disso a sonda mede base fee, não o preço da rota (1 sat em
  1.000 sats já são 1.000 ppm).
- Peer com canal paralelo não entra (0.5.41): o fast-path não consegue fixar o
  canal de entrada e a sonda só seria pulada, gastando um degrau.
- 1 sonda por scan e 1 em voo por vez.
- 1 escada por canal a cada 24h: cada job é um degrau, a escada sobe entre
  scans e para no primeiro sucesso.
- O orçamento da descoberta é conferido à parte, então ela roda mesmo com o
  orçamento normal esgotado. O que ela gasta entra no gasto diário (`auto`).

Por que não repete o incidente do keepalive (0.5.37):

- A sonda é um job próprio, `trigger_reason = sovereign-discovery`, só
  fast-path. Se o fast-path não fecha, o job termina como `failed` com
  `discovery: no route up to N ppm` e **não** cai no loop legado, então não
  percorre as sources uma a uma.
- A falha não é `all sources failed`: não conta para o cooldown estrutural do
  alvo.
- Jobs de descoberta ficam fora da quarentena de routeability das sources
  (`loadSourceRouteabilityCooldowns`) e fora do `last_success` do cooldown
  estrutural (`loadSovereignTargetStructuralCooldowns`). Um alvo alcançado só
  acima do teto não é "alcançável" no preço normal, e um sucesso acima do teto
  não zera o cooldown que o alvo ganhou no teto normal.
- Sem estado conhecido (banco indisponível), a sonda não é enfileirada.
- Lote de descoberta não conta como estoque Sovereign nem no realized do
  autopilot.

Depois do sucesso, a cadeia é a que já existe: a fee paga entra no custo de
rebalance de 7d do canal, o interlock publica o piso de fee, o AutoFee sobe a
fee, o teto normal do autopilot sobe junto e o refill normal passa a caber. A
sonda só mede o preço. Com a trava de estoque ligada, esse refill para depois
do estoque de trabalho se o canal não vender.

A decisão carrega `discovery`, `discovery_step` e `discovery_fee_cap_ppm`. O
motivo vira `discovery_queued` (ou `would_discover` em shadow).

## Compatibilidade

- Seis colunas novas em `rebalance_config` via `add column if not exists`; sem
  migração de dados.
- Tudo nasce desligado. Sem opt-in o comportamento é o da 0.5.39.
- Não muda AutoFee nem Automation Interlock.

## Calibração sugerida (Friendspool)

| Knob | Valor |
|---|---|
| stock_gate_enabled | true |
| stock_gate_min_stock_pct | 10 |
| stock_gate_cover_days | 3 |
| discovery_steps | 2 |
| discovery_ceiling_pct | 150 |
| discovery_daily_budget_sat | 300 |
| sovereign_exploration_slot_pct | 15 (era 30) |

## O que acompanhar

- `paid_stock_gate` nos `skip_reasons`: deve aparecer no bfx-lnd0 e nos canais
  sem venda, e sumir de um canal assim que ele vende.
- Custo de rebalance sobre receita: estava em 85%; a meta é voltar para perto
  de 40%.
- Jobs com `trigger_reason = sovereign-discovery`: quantos acham rota, a que
  ppm, e se o canal vende depois que o AutoFee sobe a fee.
- Se um bom vendedor ficar vazio com a trava ativa, subir `cover_days`.

## Fora do escopo

- Anotar no ranking "rota a X ppm" ou "sem rota até Y ppm" por canal.
- Regra "não repor se o piso ficar acima do preço histórico de venda".
- Parking zerar o desconto de entrada.
