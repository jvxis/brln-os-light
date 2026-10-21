# AutoTarget: alvo parado sobe por tempo (0.5.48)

## Por quê

O AutoTarget sobe o alvo de quem vende acima da média do nó (`sells_above_node`) e desce o de quem comprou e não vendeu (`fill_and_hold`). A subida exige que o canal seja candidato na rodada, e ser candidato exige déficit acima do deadband (`alvo − local > deadband`). O `fill_and_hold` desce o alvo até perto do saldo local, então o canal rebaixado fica sem déficit, deixa de ser candidato e não sobe mais: a única saída é vender o pouco estoque que tem, rápido o bastante para render a receita mínima da semana.

No Friendspool, em 21/10/2026, 19 canais gerenciados estavam em 5% e 10%, abaixo do mínimo de 20% configurado em 15/10 (o mínimo só limita novas descidas). O operador encheu 18 sinks à mão (9,8M, 14 deles comprados abaixo do preço de venda) e elevou os alvos para 20%. O histórico desde julho tem 235 descidas `fill_and_hold` contra 199 subidas.

O mercado muda; uma descida é uma leitura de uma semana, não um veredito permanente.

## O que muda

`auto_target_idle_up_days` (config do rebalance, padrão **0 = desligado**, 0–90; campo "Subir alvo parado após (dias)" no card do AutoTarget).

Ligado, um canal gerenciado pelo AutoTarget sobe **um degrau** (`auto_target_step_pct`, e no mínimo até `auto_target_min_pct`, sem passar do máximo efetivo) quando:

- não é candidato nesta rodada (candidatos seguem com a regra de sell-through);
- não houve job de rebalance para ele (qualquer origem: autopilot, manual, sonda), movimento aplicado do AutoTarget nem mudança nas configurações do canal há N dias;
- o saldo local está até o alvo + deadband (estoque ainda não vendido fica de fora);
- o canal **não enche sozinho**: a entrada por ele (forwards que entraram pelo canal) na janela de N dias ficou abaixo de 20% da capacidade. Sources e routers ficam de fora mesmo quando estão vazios. Motivo: no Friendspool, 14 dias até 21/10/2026, as oito sources que o operador mantém em 5–10% receberam de 26% a 560% da capacidade, e 38 dos 69 demais canais com rebalance ligado receberam menos de 10%. The Continental e Nodelou estavam em 1% de local porque a fee baixa os esvaziou de propósito; sem esse filtro virariam alvo de compra;
- não está em `no_sale_30d` (o autopilot não compraria para ele de qualquer forma).

A decisão é registrada no histórico do AutoTarget com motivo `idle_up` e `idle_since`. Usa o mesmo orçamento de subidas por ciclo (`auto_target_max_ups_per_cycle`) depois das subidas por venda; quando o orçamento acaba, não grava linha de throttle (o canal continua elegível e entra no próximo ciclo com vaga).

Depois da subida nada muda: o canal disputa a rodada sob ROI, stock gate e `no_sale_30d`, e o `fill_and_hold` pode descê-lo de novo (após quatro intervalos, nunca abaixo do mínimo) se o que comprar não vender. O custo da exploração fica limitado a um lote por canal a cada N dias.

## Como ligar e medir

Valor sugerido: 14 dias. Antes de ligar, medir o ajuste manual de 21/10 (alvos elevados para 20%) em 7 e 30 dias: sell-through dos lotes, custo ppm × venda ppm, giro e líquido por dia, com o BRLN HUB como controle. Ligado, acompanhar as decisões `idle_up` em `/api/rebalance/auto-target/history` e as vendas dos lotes que elas destravarem.

## Testes

- `TestDecideAutoTargetIdleUp`: desligado; sobe um degrau no mínimo; alvo antigo abaixo do mínimo vai ao mínimo; tempo insuficiente; candidato; `no_sale_30d`; atividade desconhecida; canal acima do alvo; dentro do deadband; teto no máximo efetivo; já no máximo; source esvaziada de propósito (entrada 540% da capacidade); entrada logo abaixo e exatamente no limite de 20%.
- `TestNormalizeAutoTargetIdleUpDays`: padrão 0, negativo vira 0, teto 90.
