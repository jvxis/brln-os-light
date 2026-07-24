# Rebalance Center vs LNDG — Comparação Funcional Completa

Comparação direta entre o Rebalance Center do LightningOS e o rebalancer do [LNDG](https://github.com/cryptosharks131/lndg). Atualizado em 2026-05-08 contra `master` do LNDG.

> **TL;DR (revisado 2026-05-08).** LNDG é uma camada fina sobre o pathfinding do LND, mas essa simplicidade é exatamente sua força em redes onde nosso modelo multi-camada filtra demais. LightningOS continua sendo uma plataforma de decisão multi-objetivo (economics, demanda real, aprendizado por par, interlock com AutoFee), mas a partir de **0.3.25-Beta** oferece um modo **`delegated_fast_path_enabled`** que mimica a abordagem do LNDG: uma única chamada `SendPaymentV2(outgoing_chan_ids=[all-eligible])` com MPP, deixando o pathfinder + Mission Control nativos do LND escolherem. Em sucesso, finaliza o job; em falha, cai no loop tradicional. Operadores que reportaram "ligo o LNDG e os rebalances acontecem imediato" devem ligar esse flag.

> **Diferença arquitetural que o doc original minimizou:** LNDG passa **TODAS as sources elegíveis ao LND em uma única chamada**; o LND nativo então faz pathfinding multi-source com MPP, retry interno, MC nativo. Nós iteramos source-por-source com `BuildRoute`/`QueryRoutes` + `SendToRoute`. Para self-payment com `allow_self_payment=true`, o LND nativo tem otimizações específicas (anos de tuning Lightning Labs) que nosso modelo per-source explícito não captura. Soma-se a isso filtros pré-attempt empilhados (Política C, ROI guardrail, cost gate 1.4, payback progress) e cooldowns sobrepostos (pair_fail_ttl + permanent_fail_score) que LNDG não tem — combinado, eliminamos candidatos viáveis antes de tentar. Veja o backlog `docs/lndg-parity-investigation.md`.

## 1. Seleção e priorização de targets

| Critério | LNDG | LightningOS |
|---|---|---|
| Filtro de targets | `auto_rebalance=True AND inbound_can ≥ 1` | `EligibleAsTarget` (deficit > deadband, outgoing > peer, cost gate 1.4) + `EligibleAsManualTarget` |
| Critério de ordenação | `ORDER BY -inbound_can` (mais vazio primeiro) | Score econômico = `expectedGain − estimatedCost` com modelo v1 (revenue proporcional) ou **v2** (`amount × outgoing_fee × spreadEffectiveness`) |
| Fairness | Nenhuma | **Bucket de 10%** acima do top score: dentro do bucket, sort por `LastAutoAt` (mais antigo primeiro); fora, score puro |
| Sinal de demanda | Nenhum | **DrainRateSatPerHour** (ForwardingHistory 24h) usado como `velocityMultiplier` no score v2 |
| Age boost | Nenhum | Cresce 0.5/dia além do cooldown, capa em 1.5 |
| Score multi-objetivo | N/A | `score × (velocityWeight × velocityMultiplier + (1−velocityWeight) × ageBoost)`; default `velocityWeight=0.7` |
| Cooldown probe | Nenhum | Probe pequeno (50k sat) em targets sob cooldown, pra reabrir oportunidade |

## 2. Seleção e priorização de sources

| Critério | LNDG | LightningOS |
|---|---|---|
| Filtro | `percent_outbound ≥ ar_out_target` | `EligibleAsSource` (local pct > floor, payback progress, política C) |
| Estratégia | Lista enviada como `outgoing_chan_ids` ao LND, que escolhe internamente | **Sort multi-critério** por job: ROI estimado, custo histórico, `MaxSourceSat`, `PendingOutgoingHtlcs` (tiebreaker) |
| Per-source state | Nenhum | Loop de sources independente, cada uma com sua máquina de estados completa |
| Source min payback | Nenhum | `SourceMinPaybackProgress` (default 0.95) — pula source com payback baixo (default ligado) |
| Source bypass | Nenhum | Bypass de filtro 1.4 quando custo histórico < 500 sat (canal "fresco") |

## 3. Cálculo de fee cap

| Critério | LNDG | LightningOS |
|---|---|---|
| Modo proporcional | `local_fee × ar_max_cost/100` | `econ_ratio × outgoing_fee_ppm` |
| Teto absoluto sobre proporcional | `AR-MaxFeeRate` global | `econ_ratio_max_ppm` opcional |
| Modo fixo | N/A | `fee_limit_ppm` (sobrescreve econ_ratio) |
| Subtrai source fees | Não | `LostProfit` (opt-in) — desconta source fee do cap |
| Per-channel override | Não | `EconRatioOverride` per canal |
| Cost gate por canal | Não | `AutoBypassCostGate` per canal (desabilita filtro 1.4) |
| Floor de custo histórico | Não | `RebalanceCostFloorPpm` (default 250) — fallback quando não há histórico |

## 4. Execução da rebalance

| Critério | LNDG | LightningOS |
|---|---|---|
| Construção da rota | `AddInvoice` + `SendPaymentV2(payment_request)` — LND faz pathfinding | `BuildRoute` (cached) ou `QueryRoutes` + `SendToRoute` — controle total |
| Fast path | Não | **Wave 4.1**: `BuildRoute(amount, source, cached_hops)` antes de QueryRoutes; sucesso → reusa; falha → fall-through silencioso |
| Re-route em falha | Depende do LND escolher internamente | Refresh explícito via QueryRoutes a cada 2 falhas consecutivas, com fallback |
| Route cap discovery | Não | `maxAmountOnRouteSat` calcula teto real da rota a cada attempt |
| Per-source loop | Não | Máquina de estados completa por source antes de passar pra próxima |

## 5. Ramp de amount (RapidFire)

| Critério | LNDG | LightningOS |
|---|---|---|
| Multiplicador na sucesso | × 1.21 (linear) | **× 2** (mais agressivo) |
| Divisor na falha | / 2 | / 2 |
| Estados | Linear: success → bigger; failure → smaller | **Máquina 3 fases**: `increase → steady → decrease → increase` (volta a subir após queda) |
| Persistência entre iterações | Cria novo `Rebalancer` no DB cada step | Mesmo job loop em memória — sem overhead |
| Floor | 69.420 sats (hardcoded) | `MinExecuteSat`/`MinAmountSat` (configurável) |
| Adaptive start | Não | **Wave 1.3**: `max(SuccessAmountSat × 1.2)` entre pares com sucesso recente, antes do loop começar |
| Random variance | ±variance% no `ar_amt_target` | Não usa randomização |

## 6. MPP / Multi-shard

| Critério | LNDG | LightningOS |
|---|---|---|
| Controle | Binário (`max_parts=None` ou `1`) | Configurável: `MppMaxShards`, `MppParallelism`, `MppMinShardSat`, `MppRoundTimeoutSec`, `MppAutoOnly` |
| Default `MppMinShardSat` | N/A | Proporcional a `MinExecuteSat` quando 0 |
| Prepass | Não | `rebalanceMppPrepassContext` faz roteamento adaptativo de shards antes do execute |
| Shadow mode | Não | Shadow MPP roda em paralelo pra coletar telemetria sem executar |
| Floor blocked sources | Não | Tracking de sources bloqueadas por dynamic floor durante MPP |

## 7. Mission Control

| Critério | LNDG | LightningOS |
|---|---|---|
| Reset | Nunca toca | `tryResetMissionControl(ctx, trigger)` com cooldown global de 5min, dispara em auto + manual restart watch |
| Reinforce on success | Não | **Wave 4.4**: `ImportMissionControl` async com pares da rota vencedora (opt-in via `MissionControlReinforce`) |
| Half-life | N/A | `MissionControlHalfLifeSec` configurável (default 600s, era 0=1h do LND) |
| Endpoint admin | Não | `POST /api/rebalance/mission-control/reset` + botão UI |

## 8. Pair history e aprendizado

| Critério | LNDG | LightningOS |
|---|---|---|
| Stats por par (source, target) | Não | Tabela `rebalance_pair_stats`: success_count, fail_count, last_success_at, last_fail_at, last_fail_reason, success_amount, success_fee_ppm |
| Cached winning route | Não | **Wave 4.1**: `last_success_route_hops jsonb` por par, reusado via BuildRoute |
| Permanent fail score | Não | **Wave 4.2**: half-life 7d, cap 20, threshold 3.0, TTL escalado até 6h. Increment estrutural=+1, temporário=+0.25 |
| Cleanup de stale | Não | **Wave 4.3**: cleanup diário de pair_stats sem sucesso há > 60d |

## 9. Cooldowns

| Critério | LNDG | LightningOS |
|---|---|---|
| Wait period | `AR-WaitPeriod` (default 30min) entre falhas | 4 cooldowns sobrepostos: `target_cooldown`, `target_no_attempt`, `target_failed`, `target_distinct_source` |
| Per-pair cooldown | Não | TTL por par baseado em `failCount` e `reason` (estrutural vs temporário) |
| Cooldown probe | Não | Probe pequeno reabre oportunidade sob cooldown |

## 10. Budget e ROI

| Critério | LNDG | LightningOS |
|---|---|---|
| Budget diário | Não tem conceito explícito | `daily_budget_pct` (% de revenue 7d ou 24h), modos `revenue_24h_pct` / `hybrid_revenue` |
| Budget split auto/manual | Não | `BudgetAutoOnly` + `ManualReserveEnabled` + `ManualReserveMode/Value` |
| ROI guardrail | Não | `ROIMin` (default 1.1) — descarta target onde `expectedROI < min` |
| Profit guardrail | Não | Descarta quando `expectedGain < estimatedCost` |
| Critical mode | Não | `CriticalReleasePct` + `CriticalMinSources` + `CriticalMinAvailableSats` + `CriticalCycles` — flexibiliza filtros quando saúde do nó está em risco |
| Budget skip detalhado | Não | `budget_too_low` vs `budget_below_min` (com fitAmount automático) |

## 11. AutoFee Interlock

| Critério | LNDG | LightningOS |
|---|---|---|
| Bidirecional | Não | **6.1 + 6.1b** |
| AutoFee → Rebalance | N/A (LNDG tem AutoFee no `af.py`, mas sem interlock) | AutoFee skipa canais com rebalance recente (window 30min, tag `autofee_settling`) |
| Rebalance → AutoFee | Não | Rebalance multiplica score × `AutofeeSettlingMultiplier` (default 0.75) quando AutoFee ajustou nas últimas N segundos (`AutofeeSettlingWindowSec`, default 7200s/2h) |
| Reason exposto na UI | N/A | `autofee_settling_target` no skip reasons map |

## 12. Observabilidade

| Critério | LNDG | LightningOS |
|---|---|---|
| Métricas baseline | Não | Endpoint `/api/rebalance/metrics/baseline?days=N` com aggregate + daily |
| Cache de métricas | Não | Tabela `rebalance_metrics_daily` populada async no `finishJob` |
| Scan skips persistidos | Não | Tabela `rebalance_scan_skips` (retenção 14d) com `expected_gain_sat`, `estimated_cost_sat`, `expected_roi`, `reason` |
| Pair drilldown | Não | Endpoint `/api/rebalance/pair-stats?target_channel_id=X` + UI expandível |
| Time to payback | Não | `TimeToPaybackHours` per canal (verde/amarelo/vermelho) |
| Manual restart watch telemetry | Não | `last_manual_restart_queued`, `last_manual_restart_reasons` separados do auto scan |
| Failure stream | `htlc_stream.py` registra falhas | Persistido por attempt em `rebalance_attempts` com `fail_source_index`, `fail_hop_pubkey` |

## 13. Concorrência e estado

| Critério | LNDG | LightningOS |
|---|---|---|
| Max concurrent | Worker pool fixo | `MaxConcurrent` configurável + `resetSemaphore` seguro com jobs em flight |
| Race em scan | N/A | Wave 2.2 fix: `lastAutoByTarget` + `criticalActive` lidos sob mesmo mutex |
| Critical handling | Não | `CriticalCycles` flexibiliza filtros após N scans falhados |
| Job state machine | Status numérico (1/2/3/...) | `rebalanceJobRunner` com `prepare/runMppPrepass/runLegacyLoop/finalize` |

## 14. Decomposição estrutural

| Critério | LNDG | LightningOS |
|---|---|---|
| Inner loop modular | Função única | `rebalanceJobRunner` struct + `rebalanceMppPrepassContext` struct |
| Builder de candidatos | Inline | `buildAndOrderRebalanceCandidates(input) plan` (testable, golden test) |
| Config payload | Lê stored params direto no DB | `applyRebalanceConfigPayload(cfg, payload)` extraído + `validateRebalanceConfigPayload` (400 em campo inválido) |
| Sub-componentes UI | View Django monolítica | `MetricDisclosure`, `PairStatsPanel`, `SettingsSubcard`, `types.ts` |

## 15. Validação e qualidade

| Critério | LNDG | LightningOS |
|---|---|---|
| Tests | Limitados | Golden test do auto scan (12 canais, valida ordem + skips), unit tests de scoring/fee/clamps/eligibility |
| Validação 400 | Mínima | `validateOptionalInt/Int64/Float` em todos os campos de config |
| Config clamps | N/A | `normalizeRebalanceConfig` clampa todos os campos pra ranges válidos |

## 16. Resultados empíricos (referência)

| Métrica | LNDG (típico) | LightningOS (2026-05-02 vs baseline 12d) |
|---|---|---|
| success_rate | ~3-8% (operadores reportam) | 1,04% → 3,95% (~4×) |
| sats movidos / sat gasto | Não publica | 1.866 → **2.986 (+60%)** |
| avg_fee_ppm_paid | Não publica | 535,8 → **335,0 (−37%)** |


