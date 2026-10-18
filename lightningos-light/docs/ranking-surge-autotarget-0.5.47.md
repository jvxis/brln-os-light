# 0.5.47: três correções pequenas no ranking, no surge do AutoFee e no AutoTarget

Achados do levantamento diário de 15 a 18/10/2026 no Friendspool e no BRLN HUB.
Cada mudança é estreita, sem knob novo, e mantém o mecanismo existente;
só corrige o caso em que a regra atual dava a resposta errada.

## 1. Ranking: um fio de volume em 30 dias não é movimento

`no_economic_movement_30d` e `rebalance_only_30d` usavam "qualquer sat" como
movimento. Volarte (5M) encaminhou 240k sats a 1 ppm em 30 dias, 1 sat de fee;
UNITEDPLOW teve 1 sat de assistida. Os dois ficaram em `maintain` com score 51
até o operador fechar à mão em 17/10 (12,75M parados nos quatro canais do
teste de fee fixa).

Agora o sinal de 30 dias exige fee ≥ 10 sats **ou** volume ≥ 10% da capacidade
(piso de 100k) em direta ou assistida. Rebalance pago continua contando como
movimento de capital (é o caso `rebalance_only_30d`). O penalty de 7 dias e a
janela de aquecimento (`closeWarmup`) não mudam. Corredores de fee baixa
passam pelo volume (Tennis, Nova, Satway); canais do Clube com assistida
passam pela fee; o que fica para `close` é o que não gira nem rende.

## 2. AutoFee: surge por escassez só com evidência de demanda

O surge (`outRatio < 0,10 → +min(SurgeBumpMax, 0,5 × falta)`) existe para
que o próximo refill caiba no orçamento. Faz sentido quando alguém quis a
liquidez. Em canal **sem nenhuma venda na semana, sem forward nas últimas 24h e
sem pressão de HTLC**, não há comprador para precificar: em 17/10, 1sats
1015 → 1077 e lancrypt 930 → 1050 no FP, e um peer sem alias 338 → 722 no
HUB, em um dia, todos com zero vendas na história. O hold de "sink maduro
vazio" não pegava esses casos porque exige histórico de venda.

`shouldHoldSurgeWithoutDemandEvidence(fwd7d, fwd1d, htlcPressure, htlcForwardHot)`
segura o surge e marca `surge-no-demand-hold`. Canais que venderam na semana
(BitcoinVN, PurpleWisteria) mantêm o surge: com o reteste drenado da 0.5.45 é
assim que o teto anunciado sobe até o preço da rota (a escada mediu BitcoinVN a
1 220 ppm em 17/10 e o surge chegou a 1 225).

## 3. AutoTarget: descer espera quatro intervalos

`fill_and_hold` baixava um degrau a cada intervalo de avaliação (12h no FP, 6h
no HUB). Babylon-4a (HUB) subiu 10 → 35 em dois dias por `sells_above_node` e
caiu 35 → 10 em 18 horas numa semana fraca, antes de o estoque recém-comprado
vender; o alvo colou no saldo local e o canal saiu da elegibilidade. Mesmo
padrão de BitGo, Harry e LQWD-Canada no FP em outubro (40–50% → 10%).

O DOWN agora espera `4 × auto_target_eval_interval_hours` desde o último DOWN
aplicado (`autoTargetDownAllowed`); o UP mantém o intervalo normal. Quatro
degraus passam a levar quatro vezes o intervalo em vez de uma noite. Nenhuma
linha de histórico é gravada enquanto espera; o canal é reavaliado no próximo
intervalo e um UP no meio do caminho continua permitido.

## Compatibilidade

- Sem coluna nova, sem knob novo, sem migração.
- O ranking pode passar a marcar `close` em canais que antes ficavam em
  `maintain`; é só rótulo e o guard de persistência de 30 dias continua valendo.
- O surge fica igual para qualquer canal com uma venda na semana ou HTLC quente.
- O AutoTarget só muda a cadência do DOWN.

## O que acompanhar

- Canais do FP e do HUB que mudarem para `close` na primeira semana: conferir
  que são de fato fee ~0 sem venda e sem assistida relevante.
- Tag `surge-no-demand-hold` nos outcomes do AutoFee: quem foi segurado e se
  algum deles vendeu depois (se vendeu, a regra errou).
- `auto-target/history`: nenhum canal deve ter dois DOWN com menos de
  4 intervalos entre eles.
