# Curso Rebalance Center

[Read in English](24_REBALANCE_CENTER_COURSE_EN.md)

O Rebalance Center existe para mover liquidez local para canais onde ela tende a ser vendida com vantagem econômica. Em termos práticos: ele escolhe canais com pouco outbound e fee outbound maior que a fee do peer, busca fontes com liquidez local sobrando, tenta rotas, mede custo, aprende pares bons/ruins e protege canais que ainda não pagaram o rebalance anterior.

## 1. Modelo Mental

Um canal vira **target** quando precisa receber liquidez local. O cálculo básico é:

```text
target_outbound_pct - local_pct = deficit
```

Se o déficit passa o `deadband_pct`, o canal é ativo e `outgoing_fee_ppm > peer_fee_ppm`, ele pode ser target manual. Para virar target automático, ainda precisa passar por filtros econômicos: cost gate, ROI, profit guardrail, cooldowns e orçamento.

Um canal vira **source** quando tem liquidez local acima de `source_min_local_pct`, não está excluído como source e não está protegido por payback. O sistema tenta não usar como source um canal que ainda está no vermelho por rebalance anterior.

## 2. Modos De Operação

**Manual Rebal In:** operador escolhe o canal e inicia. É usado para testar, corrigir um canal específico ou forçar uma ação pontual. Ele usa a elegibilidade manual, mais permissiva que o auto.

**Auto Rebalance:** scanner periódico escolhe candidatos e enfileira jobs automaticamente. É o modo mais econômico e conservador.

**Manual Restart Watch:** monitora canais marcados por canal. Quando um job manual auto-restart falha ou fica parcial, ele tenta novamente depois do intervalo/cooldown. Hoje ele respeita `EligibleAsTarget`, então só ignora o cost gate se o canal estiver com **Bypass cost gate** marcado.

## 3. O Que Observar Primeiro

No topo do Rebalance Center, olhe sempre:

- **Live cost 24h:** quanto o sistema gastou.
- **Eligible sources / Targets:** se ambos estão baixos, o sistema não tem espaço para agir.
- **Last scan status:** mostra se o auto encontrou candidatos, enfileirou ou foi bloqueado.
- **Last scan reasons:** é o diagnóstico mais importante.
- **Effectiveness 7d / Attempts:** mede se o sistema está tentando bem ou desperdiçando tentativas.
- **ROI 7d / Payback progress:** indica se o rebalance está se pagando.
- **Falhas 30m:** mostra se há tempestade de no-route/timeout.
- **MSPR 24h:** mostra se o modo multi-source está ajudando ou gerando aborts.
- **Time-to-payback por canal:** mostra se o custo do rebalance volta em receita.

Para comparação objetiva, use `/api/rebalance/metrics/baseline?days=1` após 24h. Para decisão confiável, use 7 dias.

## 4. Como Ler Um Canal

Na linha do canal, observe nesta ordem:

1. `local_pct / remote_pct`: se local está muito baixo, pode precisar de inbound local.
2. `outgoing ppm - peer ppm = spread`: spread positivo é a base econômica.
3. `spread efetivo`: spread multiplicado pelo econ ratio.
4. `target_outbound_pct`: quanto local você quer manter.
5. `local liquidity to add`: amount necessário.
6. `payback`: se ainda não pagou rebalances anteriores.
7. `ROI est.` e `time-to-payback`: qualidade econômica.
8. `Auto`, `Manual restart`, `Exclude source`, `Bypass cost gate`.

Um canal bom para target costuma ter: demanda real, fee outbound alta, peer fee baixa, spread positivo, local baixo, histórico de receita e payback razoável.

## 5. Parâmetros De Operação

Comece conservador:

- `auto_enabled`: só ligue quando sources/targets estiverem revisados.
- `scan_interval_sec`: default 900s. Use 900-1800s. Menor que isso tende a gerar ruído.
- `max_concurrent`: default 2. Use 1 para diagnóstico, 2 para operação normal, e mais que 2 só se o node for grande e estável.
- `daily_budget_pct`: default 25%. Se o sistema gasta demais, reduza. Se aparece `budget_too_low`, aumente ou reduza limits/amounts.
- `budget_mode`: prefira híbrido, pois mistura média 7d e receita 24h.
- `budget_auto_only`: ligado significa que o orçamento limita o auto, mas não prende manual restart.
- `manual reserve`: útil quando o auto consome orçamento antes dos canais marcados para restart.

## 6. Parâmetros De Target

- `target_outbound_pct`: por canal. Não coloque 50% em tudo por hábito. Canais caros e com demanda podem ficar 30-50%; canais incertos, 20-30%.
- `deadband_pct`: default 5. Aumentar para 8-12 reduz rebalances pequenos. Baixar para 3-5 deixa o sistema mais ativo.
- `min_execute_sat`: default 10k. Suba se houver muito job pequeno sem impacto. Baixe só se quiser microcorreções.
- `max_amount_sat`: 0 sem limite. Use limite em canais onde não quer overfunding.

## 7. Parâmetros Econômicos

- `econ_ratio`: default 0.6. Define quanto da vantagem econômica você aceita gastar em fee. Subir deixa passar mais rotas e aumenta custo; baixar fica mais seletivo.
- `econ_ratio_override` por canal: use para canais estratégicos sem enfraquecer o global.
- `bypass cost gate`: use só por canal. Ele ignora o filtro `spread efetivo > custo esperado`, mas ROI/profit continuam protegendo depois.
- `roi_min`: default 1.1. Se há muitos `roi_guardrail`, o sistema está dizendo que o alvo ainda não compensa.
- `rebalance_cost_floor_ppm`: default 250 ppm. É o custo esperado mínimo quando não há histórico.
- `gain_model_version`: v1 usa receita histórica; v2 usa spread efetivo e velocidade. Ative v2 quando quiser priorizar demanda real.
- `velocity_weight`: default 0.7. Alto prioriza canais com drain rate; baixo devolve peso para fairness/idade.

## 8. Parâmetros De Execução

- `fee_limit_ppm`: override fixo global. Use com cuidado; normalmente prefira `econ_ratio`.
- `fee_ladder_steps`: default 1. Mais degraus exploram fee, mas aumentam tentativas.
- `amount_probe_steps`: default 6. Mais passos refinam amount, mas podem aumentar tempo.
- `attempt_timeout_sec`: default 45.
- `rebalance_timeout_sec`: default 600.
- `fail_tolerance_ppm`: default 500. Ajuda a tolerar pequenas variações de fee.
- `amount_probe_adaptive`: mantenha ligado.

## 9. MSPR / Multi-Source

MSPR divide o rebalance em shards por múltiplas sources.

Defaults atuais:

- `mpp_enabled`: ligado.
- `mpp_auto_only`: ligado.
- `mpp_max_shards`: 6.
- `mpp_parallelism`: 3.
- `mpp_min_shard_sat`: acompanha o mínimo de execução quando não definido.
- `mpp_round_timeout_sec`: 35.

Se houver muitos `mpp_structural_abort`, reduza paralelismo ou shards. Se houver sucesso parcial frequente e pouca colisão, MSPR está ajudando.

## 10. Payback E Proteção De Sources

O sistema protege liquidez que foi comprada por rebalance até ela se pagar.

- `source_min_payback_progress`: default 0.95. Excelente proteção. Evita usar como source canais que ainda não recuperaram o custo.
- `payback mode`: libera quando receita paga custo.
- `time mode`: libera após `unlock_days`, default 7.
- `critical mode`: libera parcialmente quando faltam sources.
- `critical_release_pct`: default 20%.

Não desligue payback globalmente para criar source. Isso costuma piorar o PnL.

## 11. Mission Control

Use o botão **Reset MC** quando houver muitos no-route/route-dead em pouco tempo. Não use a cada falha isolada.

- `mc_half_life_sec`: 0 usa default do LND. Em tempestade de rotas ruins, 600s pode ajudar.
- `mission_control_reinforce`: quando ligado, reforça no LND rotas que deram certo. É avançado; ligue só se o node já estiver estável.

## 12. Como Diagnosticar Motivos De Skip

- `target_not_eligible`: não passou target automático. Veja spread, deadband, cost gate e bypass.
- `roi_guardrail`: ROI estimado abaixo do mínimo. Normalmente não mexa; espere receita ou aumente fee do canal.
- `profit_guardrail`: ganho esperado menor que custo. Não force salvo estratégia clara.
- `budget_too_low`: orçamento restante não cobre o custo estimado.
- `below_execute_min`: amount alvo pequeno demais; normalmente é saudável ignorar.
- `target_cooldown`: muitas falhas recentes; aguarde ou investigue pair stats.
- `channel_busy`: já há job no canal.
- `autofee_settling_target`: AutoFee mexeu recentemente; score foi reduzido temporariamente.

## 13. Rotina Do Node Runner

Diário:

1. Verificar live cost, ROI, effectiveness e falhas 30m.
2. Abrir canais com target baixo e spread alto.
3. Ver se skips são econômicos ou operacionais.
4. Ajustar no máximo um grupo de parâmetros por vez.
5. Comparar 24h depois.

Semanal:

1. Comparar baseline 7d.
2. Revisar canais com payback ruim.
3. Revisar sources excluídas.
4. Ver pair drill-down dos canais que falham sempre.
5. Promover ou reverter `gain_model_version=2` conforme resultado.

## 14. Estratégia De Calibração Recomendada

Comece com:

- Auto ligado apenas em canais escolhidos.
- `scan_interval_sec=900`.
- `max_concurrent=1` ou `2`.
- `daily_budget_pct=10-25`.
- `deadband_pct=5-8`.
- `source_min_local_pct=30-40`.
- `roi_min=1.1`.
- `source_min_payback_progress=0.95`.
- MSPR auto-only ligado.

Depois calibre assim:

1. Se não há jobs: olhe `target_not_eligible`, `no_sources`, `budget_too_low`.
2. Se há jobs mas pouca execução: olhe MC, pair stats, fee cap, route failures.
3. Se executa mas não paga: suba ROI mínimo, reduza econ ratio, revise targets.
4. Se paga bem mas é lento: ative v2, ajuste velocity weight, revise target pct.
5. Se gasta demais: reduza budget, econ ratio, target pct ou max amount.

Regra prática: ajuste **targets por canal primeiro**, depois **sources**, depois **economia**, depois **execução**. Ajustar tudo junto deixa impossível saber o que melhorou ou piorou.
