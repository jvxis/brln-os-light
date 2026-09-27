# Sovereign: recuperação de refill e anti-churn — 0.5.36

## Diagnóstico que motivou a mudança (Friendspool, 2026-09-27)

Receita de forward caiu de ~24k sat/dia (abr/2026) para ~3,5k sat/dia (set/2026)
e o movimento de 100%+ da liquidez para 18-23%. Quatro causas, em ordem de peso:

1. **Churn de contrapartes.** Cruzando `lncli closedchannels` com o histórico de
   forwards de 180 dias: 30 canais fechados renderam 810k de fee de saída e 543k
   de entrada (de 1,99M no período). Quase todos `INITIATOR_REMOTE`, com o nosso
   lado a 0,1-0,2M (drenado). Peers fecham canais que ficam vazios do nosso lado.
2. **Espiral orçamento↔receita.** O budget diário é uma % da receita recente.
   Receita cai → budget cai → sinks não são reabastecidos → peers fecham →
   receita cai mais. Não havia piso.
3. **Gate `budget_efficiency` inalcançável.** Para canal sem histórico de custo
   em 7d o custo estimado era 100% do fee cap e o ganho cold-start 85% do
   teórico: eficiência máxima ≈ 13%, sempre abaixo do piso de 20%. Para canal
   com fee no piso do AutoFee (custo × 1,10) o gate exigia fee ≥ 1,176 × custo.
   Resultado: 36 alvos elegíveis, 17 candidatos, 1 selecionado por scan.
4. **Mix de fee** (fora do escopo desta release; ver plano operacional).

## O que muda

### C1 — Piso e janela do orçamento diário

- `daily_budget_min_sat` (padrão 0 = desligado): piso absoluto aplicado sobre o
  orçamento derivado da receita. Quebra a espiral: quando a receita cai, o
  orçamento não encolhe abaixo do piso.
- `daily_budget_base_days` (padrão 7, faixa 7-30): janela da média de receita
  usada no orçamento base (o modo híbrido mistura 70% dessa média com 30% das
  últimas 24h). 7 preserva o comportamento anterior.
- `daily_budget_base_sat` e `daily_budget_short_term_sat` no overview continuam
  mostrando os componentes crus; `daily_budget_sat` já vem com o piso aplicado.

### C2 — Custo cold-start realista e piso de eficiência alinhado ao AutoFee

- Alvo sem histórico de custo confiável (`rebal_amt_7d < amount`) passa a ser
  precificado por `max(histórico, floor, ppm médio do nó 7d × 1,2)`, limitado ao
  fee cap. Antes assumia o fee cap inteiro, o que fazia todo canal novo ou
  parado reprovar em `budget_efficiency` por construção. O ppm médio do nó vem
  de `fetchChannelRebalanceCost7d` agregado (`nodeRebalanceReferencePpm`);
  quando é 0 (nó sem rebalances pagos), mantém o comportamento antigo.
- `sovereign_budget_efficiency_autofee_aligned` (padrão false): quando ligado,
  o piso de eficiência efetivo é `min(configurado, (1 − 1/1,10) ÷ econ_ratio)`,
  o máximo que um canal precificado exatamente no piso econômico do AutoFee
  consegue atingir. O overview expõe `sovereign_budget_efficiency_effective_ratio`
  e `sovereign_budget_efficiency_aligned_ceiling`.

### C3 — Keepalive refill (anti-churn)

- `keepalive_refill_enabled` (padrão false), `keepalive_refill_after_hours`
  (48), `keepalive_refill_pct` (5).
- Um candidato com intent `refill_target` do AutoFee (motivo
  `autofee_drained_target` ou `autofee_extreme_drained_target`) existente há mais
  que `after_hours`, e com saldo local abaixo de `pct` da capacidade, recebe um
  refill de `pct` da capacidade (nunca abaixo do mínimo de execução) que passa
  pelos gates de histórico empírico (`budget_efficiency`, `low_success`,
  `route_dead`), como um slot de exploração. Continuam valendo: cooldown
  estrutural, estoque sem venda, `roi_guardrail` no funil A e EV ≥ 0.
- A decisão carrega `keepalive_refill: true`; a UI mostra o badge no detalhe do
  autopilot. Custa dezenas de sats por sonda; perder um peer que rendia 30k/mês
  custa muito mais.
- Requer o Automation Interlock ligado (shadow ou enforce) para os intents
  existirem.

### C4 — Alerta de risco de fechamento pelo peer (ranking)

- `channelRankingPeerCloseRisk`: canal ativo, público, com estado AutoFee
  `drained`/`extreme-drained` há ≥ 48h, saldo local ≤ 5% e valor econômico
  (fee de saída 30d ≥ 500 sat ou classe `sink`) recebe o motivo
  `peer_close_risk` e a recomendação `keepalive_refill` (módulo rebalance).

## Compatibilidade

- Todos os knobs novos nascem com valor que preserva o comportamento anterior
  (piso 0, janela 7d, alinhamento off, keepalive off). Só C2 (custo cold-start
  por ppm médio do nó) muda comportamento sem opt-in, e apenas quando o nó tem
  rebalances pagos em 7d; o custo continua limitado ao fee cap.
- Colunas novas em `rebalance_config` via `add column if not exists`; sem
  migração de dados.
- Não muda AutoFee, Automation Interlock nem execução de jobs.

## Calibração recomendada para aplicar junto com a release (Friendspool)

| Knob | Antes | Depois |
|---|---|---|
| daily_budget_pct | 70 | 100 |
| manual_reserve_enabled | true | false |
| sovereign_budget_efficiency_min_ratio | 0,20 | 0,10 |
| sovereign_gain_v3_cold_start_pct | 0,85 | 0,95 |
| sovereign_structural_cooldown_repeat_hours | 6 | 3 |
| sovereign_max_jobs_per_cycle / max_concurrent | 4 / 4 | 6 / 6 |
| sovereign_exploration_slot_pct | 20 | 30 |
| econ_ratio | 0,75 | 0,80 |
| daily_budget_min_sat (novo) | — | 5000 |
| daily_budget_base_days (novo) | — | 14 |
| sovereign_budget_efficiency_autofee_aligned (novo) | — | true |
| keepalive_refill_enabled (novo) | — | true |

Mantidos: `roi_min` 1,0 e `sovereign_min_expected_profit_sat` 5 (nunca EV < 0).

## 0.5.37: keepalive limitado e quarentena de source por alcançabilidade

Observado em produção 2h após ligar o keepalive: os 6 sinks drenados sem rota
(Bank The Planet, Alex71btc, Unwetter, Ripio, ln.coinos, 0217…) eram
enfileirados a cada scan, ocupavam os 6 slots, e cada rajada de "all sources
failed" (loop legado percorrendo as 34 sources) acionava a quarentena de
routeability das sources (`shouldQuarantineBroadSourceFailures`, 12 falhas em 4+
alvos → 6h). Resultado: todos os jobs seguintes caíam em "no executable
sources" — inclusive os dos vendedores reais.

Correção:

- Keepalive passa a bypassar **só** os gates econômicos (`budget_efficiency` e
  o mínimo de lucro, com lucro ≥ 0). Os gates de rota (`route_dead`,
  `low_success`) voltam a valer para ele.
- No máximo **1** job keepalive por scan (`sovereignKeepaliveMaxPerCycle`).
- Backoff de **6h** por alvo após falha sem sucesso posterior
  (`sovereignKeepaliveRecentlyFailed`, usa cooldown estrutural e pair stats).

Quarentena de source (`shouldQuarantineBroadSourceFailures`): continua exigindo
12 falhas em 4+ alvos em 6h, mas agora as falhas contam **só contra alvos que
alguma source alcançou na janela** (`reachable_failed_targets`). Falhar em alvo
que ninguém alcança não diz nada sobre a source. Sem isso, uma rajada contra
alvos mortos punia as 34 sources ao mesmo tempo e o nó ficava 6h sem source
executável. Stats antigos sem o campo mantêm o comportamento anterior.

Sem knob novo.

## O que acompanhar na semana

- Orçamento zerando em > 30% dos scans = virou o limitador (bom sinal; subir o
  piso). NET diário negativo 2 dias seguidos = voltar eficiência para 0,15.
  Rebal ppm médio > 1000 = econ 0,80 folgou demais.
- `skip_reasons` no sovereign-history: `budget_efficiency_below_floor` e
  `roi_guardrail` devem cair; `keepalive_refill` deve aparecer nas decisões dos
  sinks a 1% local.
- Ranking: canais com `peer_close_risk` devem sumir da lista conforme recebem
  refill. Se um deles fechar mesmo assim, registrar o peer.
