# Rebalance Center vs Ecossistema (LNDG, BOS, Regolancer)

Comparação direta entre o Rebalance Center do LightningOS e os três principais auto-rebalancers do ecossistema. Atualizado em 2026-05-04.

> **TL;DR.** LNDG, BOS e Regolancer são camadas finas e reativas sobre o pathfinding do LND. LightningOS é uma plataforma de decisão multi-objetivo que combina economics, demanda real, aprendizado por par persistente, descoberta dinâmica de capacidade e interlock com AutoFee. Nenhum dos três tem feature algorítmica de execução que melhore a nossa eficiência. Documento existente focado só no LNDG está em [25_REBALANCE_VS_LNDG_COMPARISON.md](25_REBALANCE_VS_LNDG_COMPARISON.md).

## Sobre cada ferramenta

| Tool | Stack | Modelo | URL |
|---|---|---|---|
| **LightningOS** | Go (server) + React (UI), Postgres | Plataforma autônoma (scan + queue + execute, contínuo) | este repo |
| **LNDG** | Python/Django + SQLite/Postgres | Web UI com auto-rebalance integrado (jobs Django) | [github](https://github.com/cryptosharks131/lndg) |
| **BOS** (balanceofsatoshis) | Node.js (CLI) | Single-shot CLI por invocação | [github](https://github.com/alexbosworth/balanceofsatoshis) |
| **Regolancer** | Go (CLI/daemon) | Loop até sucesso ou timeout, single-process | [github](https://github.com/rkfg/regolancer) |

---

## 1. Modelo de execução

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Tipo | Web app + scheduler | CLI single-shot | CLI loop até sucesso/timeout | Plataforma autônoma contínua |
| Auto-loop | Scheduler nativo | Não (envolva com cron) | Sim, in-process | Sim, scan_interval_sec configurável |
| Persistência | Django ORM | Nenhuma | In-memory (cache opcional) | Postgres (jobs, attempts, pair_stats, scan_skips) |
| Concorrência | Worker pool fixo | Sequencial (1 process) | Sequencial | `MaxConcurrent` configurável + semáforo seguro |

## 2. Seleção de targets

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Critério | `auto_rebalance=True AND inbound_can ≥ 1` | Manual (`--in <peer>`) | Manual ou via `--from`/`--to` | `EligibleAsTarget` (deficit > deadband, outgoing > peer, cost gate 1.4) |
| Ordenação | `ORDER BY -inbound_can` | N/A (manual) | N/A (manual) | Score econômico = `expectedGain − estimatedCost` com modelo v1/v2 |
| Fairness | Nenhuma | N/A | Nenhuma | **Bucket de 10%** acima do top score: dentro do bucket, sort por `LastAutoAt` |
| Sinal de demanda | Nenhum | N/A | Nenhum | **DrainRateSatPerHour** (ForwardingHistory 24h) como `velocityMultiplier` no v2 |
| Age boost | Nenhum | N/A | Nenhum | Cresce 0.5/dia além do cooldown, capa em 1.5 |
| Score multi-objetivo | N/A | N/A | N/A | `score × (velocityWeight × velocity + (1−velocityWeight) × ageBoost)` |
| Cooldown probe | Nenhum | N/A | N/A | Probe pequeno (50k sat) em targets sob cooldown |

## 3. Seleção de sources

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Filtro | `percent_outbound ≥ ar_out_target` | Filtra por liquidity, exclui in peer | `getChannelCandidates(FromPerc, ToPerc, Amount)` | `EligibleAsSource` (local pct > floor, payback progress, política C) |
| Estratégia | Lista enviada ao LND, ele escolhe | Manual ou primeiro elegível | Iterate até suceder | **Sort multi-critério** por job: ROI, custo histórico, `MaxSourceSat`, `PendingOutgoingHtlcs` (tiebreaker) |
| Per-source state machine | Não | N/A | Não | Sim — máquina completa por source antes de passar pra próxima |
| Min payback | Não | N/A | Não | `SourceMinPaybackProgress` (default 0.95) |
| Source bypass | Não | N/A | Não | Bypass de filtro 1.4 quando custo histórico < 500 sat |

## 4. Cálculo de fee cap

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Modo proporcional | `local_fee × ar_max_cost/100` | `--max-fee-rate` (ppm, fixo) | Económico via ratio: `(base+amt×rate)×ratio/1e6 - lostProfit` | `econ_ratio × outgoing_fee_ppm` |
| Teto absoluto | `AR-MaxFeeRate` (global) | `--max-fee` (sat fixo) | `feeMsat = amtMsat × ppm / 1e6` (modo fixo) | `econ_ratio_max_ppm` opcional |
| Modo fixo | N/A | `--max-fee-rate` strict | Fixed PPM mode | `fee_limit_ppm` (sobrescreve econ_ratio) |
| Subtrai source fees | Não | Não | Sim (`lostProfitMsat`) | `LostProfit` (opt-in) |
| Per-channel override | Não | N/A | Não | `EconRatioOverride` per canal |
| Cost gate por canal | Não | N/A | Não | `AutoBypassCostGate` per canal |
| Floor de custo histórico | Não | N/A | Não | `RebalanceCostFloorPpm` (default 250) |

## 5. Construção e probe de rota

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Construção | `AddInvoice` + `SendPaymentV2(payment_request)` — LND faz pathfinding | `routeFromChannels` + `payViaRoutes` | `QueryRoutes(UseMissionControl=true)` + `SendToRoute` | `BuildRoute` (cached) ou `QueryRoutes` + `SendToRoute` |
| Probe binary search | Não | `probeDestination(find_max=true)` — binary search HTLC-only | `probeRoute` recursivo via `TEMPORARY_CHANNEL_FAILURE` feedback | **`probeRouteRecursive`** (mesmo padrão do regolancer), invocado em 3 sites |
| Fast path com rota cacheada | Não | Não | Não | **Wave 4.1**: `BuildRoute(amount, source, cached_hops)` antes de QueryRoutes |
| Re-route em falha | Depende do LND | N/A (single shot) | `addFailedRoute` + retry | Refresh explícito via QueryRoutes a cada 2 falhas consecutivas |
| Route cap discovery | Não | `find_max` retorna max routable | `probeRoute` retorna max routable | `maxAmountOnRouteSat` calcula teto real da rota a cada attempt |

## 6. Ramp de amount (RapidFire)

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Multiplicador na sucesso | × 1.21 (linear) | N/A (single shot) | `tryRapidRebalance` reusa rota vencedora com vários amounts | **× 2** dentro do `increase` phase |
| Divisor na falha | / 2 | N/A | / 2 (probe binary search) | / 2 (decrease phase) |
| Estados | Linear: success → bigger; failure → smaller | N/A | tryRebalance → tryRapidRebalance | **Máquina 3 fases**: `increase → steady → decrease → increase` |
| Persistência entre iterações | Cria novo `Rebalancer` no DB cada step | N/A | In-memory loop | Mesmo job loop em memória — sem overhead |
| Floor | 69.420 sats (hardcoded) | `min Rebalance Amount` (validado) | `params.MinAmount` | `MinExecuteSat`/`MinAmountSat` (configurável) |
| Adaptive start | Não | Não | Não | **Wave 1.3**: `max(SuccessAmountSat × 1.2)` antes do loop |
| Random variance | ±variance% no `ar_amt_target` | Não | Não | Não usa randomização |

## 7. MPP / Multi-shard

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Controle | Binário (`max_parts=None` ou `1`) | N/A explícito | Não usa MPP nativamente | Configurável: `MppMaxShards`, `MppParallelism`, `MppMinShardSat`, `MppRoundTimeoutSec`, `MppAutoOnly` |
| Default `MppMinShardSat` | N/A | N/A | N/A | Proporcional a `MinExecuteSat` quando 0 |
| Prepass adaptivo | Não | Não | Não | `rebalanceMppPrepassContext` faz roteamento adaptativo de shards antes do execute |
| Shadow mode | Não | Não | Não | Shadow MPP roda em paralelo pra coletar telemetria sem executar |
| Floor blocked sources | Não | Não | Não | Tracking de sources bloqueadas por dynamic floor |

## 8. Mission Control

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Uso | Implícito via LND | Implícito | `UseMissionControl: true` no QueryRoutes | Implícito + management ativo |
| Reset | Nunca toca | Nunca | Nunca | `tryResetMissionControl(ctx, trigger)` com cooldown global de 5min, dispara em auto + manual restart watch |
| Reinforce on success | Não | Não | Não | **Wave 4.4**: `ImportMissionControl` async com pares da rota vencedora (opt-in) |
| Half-life | N/A | N/A | N/A | `MissionControlHalfLifeSec` configurável (default 600s, era 0=1h LND default) |
| Endpoint admin | Não | Não | Não | `POST /api/rebalance/mission-control/reset` + botão UI |

## 9. Pair history e aprendizado

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Stats por par (source, target) | Não | Não | `failureCache` in-memory, limpo entre rapid iterations | Tabela `rebalance_pair_stats`: success_count, fail_count, last_success_at, last_fail_at, last_fail_reason, success_amount, success_fee_ppm |
| Cached winning route | Não | Não | Rota armazenada local pra rapid (efêmero) | **Wave 4.1**: `last_success_route_hops jsonb` por par, persistido, reusado via BuildRoute |
| Permanent fail score | Não | Não | Não | **Wave 4.2**: half-life 7d, cap 20, threshold 3.0, TTL escalado até 6h |
| Cleanup de stale | Não | N/A | N/A | **Wave 4.3**: cleanup diário de pair_stats sem sucesso há > 60d |

## 10. Cooldowns

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Wait period | `AR-WaitPeriod` (default 30min) entre falhas | N/A | Nenhum entre tries; só `mainCtx` timeout (default 360min) | 4 cooldowns sobrepostos: `target_cooldown`, `target_no_attempt`, `target_failed`, `target_distinct_source` |
| Per-pair cooldown | Não | N/A | failedPairs in-memory | TTL por par baseado em `failCount` e `reason` (estrutural vs temporário) |
| Cooldown probe | Não | N/A | N/A | Probe pequeno reabre oportunidade sob cooldown |

## 11. Budget e ROI

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Budget diário | Não tem conceito explícito | N/A | N/A | `daily_budget_pct` (% de revenue 7d ou 24h), modos `revenue_24h_pct` / `hybrid_revenue` |
| Budget split auto/manual | Não | N/A | N/A | `BudgetAutoOnly` + `ManualReserveEnabled` + `ManualReserveMode/Value` |
| ROI guardrail | Não | N/A | N/A | `ROIMin` (default 1.1) — descarta target onde `expectedROI < min` |
| Profit guardrail | Não | N/A | N/A | Descarta quando `expectedGain < estimatedCost` |
| Critical mode | Não | N/A | N/A | `CriticalReleasePct` + `CriticalMinSources` + `CriticalMinAvailableSats` + `CriticalCycles` |
| Budget skip detalhado | Não | N/A | N/A | `budget_too_low` vs `budget_below_min` (com fitAmount automático) |

## 12. AutoFee Interlock

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Bidirecional | Não (LNDG tem AutoFee mas sem interlock) | N/A | N/A | **6.1 + 6.1b** |
| AutoFee → Rebalance | N/A | N/A | N/A | AutoFee skipa canais com rebalance recente (window 30min, tag `autofee_settling`) |
| Rebalance → AutoFee | Não | N/A | N/A | Rebalance multiplica score × `AutofeeSettlingMultiplier` (default 0.5) quando AutoFee ajustou nas últimas N segundos |
| Reason exposto na UI | N/A | N/A | N/A | `autofee_settling_target` no skip reasons map |

## 13. Observabilidade

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Métricas baseline | Limitado | Não | Não | Endpoint `/api/rebalance/metrics/baseline?days=N` com aggregate + daily |
| Cache de métricas | Não | N/A | N/A | Tabela `rebalance_metrics_daily` populada async no `finishJob` |
| Scan skips persistidos | Não | N/A | N/A | Tabela `rebalance_scan_skips` (retenção 14d) com `expected_gain_sat`, `estimated_cost_sat`, `expected_roi`, `reason` |
| Pair drilldown | Não | N/A | N/A | Endpoint `/api/rebalance/pair-stats?target_channel_id=X` + UI expandível |
| Time to payback | Não | Não | Não | `TimeToPaybackHours` per canal (verde/amarelo/vermelho) |
| Manual restart watch telemetry | N/A | N/A | N/A | `last_manual_restart_queued`, `last_manual_restart_reasons` separados do auto scan |
| Failure stream | `htlc_stream.py` registra falhas | Não | Não | Persistido por attempt em `rebalance_attempts` com `fail_source_index`, `fail_hop_pubkey` |

## 14. Concorrência e estado

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Max concurrent | Worker pool fixo | 1 (single shot) | 1 (sequencial) | `MaxConcurrent` configurável + `resetSemaphore` seguro com jobs em flight |
| Race em scan | N/A | N/A | N/A | Wave 2.2 fix: `lastAutoByTarget` + `criticalActive` lidos sob mesmo mutex |
| Critical handling | Não | N/A | N/A | `CriticalCycles` flexibiliza filtros após N scans falhados |
| Job state machine | Status numérico (1/2/3/...) | N/A | rebalanceResult struct | `rebalanceJobRunner` com `prepare/runMppPrepass/runLegacyLoop/finalize` |

## 15. Decomposição estrutural

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Inner loop modular | Função única | Single function | Funções separadas (`tryRebalance`, `tryRapidRebalance`, `probeRoute`) | `rebalanceJobRunner` struct + `rebalanceMppPrepassContext` struct |
| Builder de candidatos | Inline | N/A | Inline | `buildAndOrderRebalanceCandidates(input) plan` (testável, golden test) |
| Config payload | Lê stored params direto no DB | CLI args | CLI args + flags struct | `applyRebalanceConfigPayload(cfg, payload)` extraído + `validateRebalanceConfigPayload` (400 em campo inválido) |
| Sub-componentes UI | View Django monolítica | N/A | N/A | `MetricDisclosure`, `PairStatsPanel`, `SettingsSubcard`, `types.ts` |

## 16. Validação e qualidade

| Aspecto | LNDG | BOS | Regolancer | LightningOS |
|---|---|---|---|---|
| Tests | Limitados | Limitados (CLI) | Não evidente | Golden test do auto scan (12 canais, valida ordem + skips), unit tests de scoring/fee/clamps/eligibility |
| Validação 400 | Mínima | Validação CLI | Validação CLI | `validateOptionalInt/Int64/Float` em todos os campos de config |
| Config clamps | N/A | N/A | N/A | `normalizeRebalanceConfig` clampa todos os campos pra ranges válidos |


---

## Resumo qualitativo por ferramenta

### LNDG
**Bom** pra operadores casuais — config simples, comportamento reativo, integração tight com Django. **Camada fina** sobre o pathfinding do LND, com persistência mas sem learning.

### BOS
**Excelente CLI tool** com ergonomia forte (tags, fuzzy match, avoid lists ad-hoc). Algoritmo de execução: probe binary search via `find_max` antes de pagar. Não tem auto-loop — pra rodar contínuo, envolva com cron ou tools como regolancer.

### Regolancer
**Loop autonomous-while-running** em Go, com probe binary search via `TEMPORARY_CHANNEL_FAILURE` (mesmo algoritmo do nosso `probeRouteRecursive`). `tryRapidRebalance` reusa rota vencedora. **In-memory only** — perde tudo entre runs. Excelente pra "rebalance one channel now" mas sem aprendizado persistente.

### LightningOS
**Plataforma de decisão multi-objetivo** que trata cada rebalance como problema combinado: economics, demanda real, aprendizado por par persistente, custos históricos, interlock com AutoFee, descoberta dinâmica de capacidade. O que outros resolvem com "tenta de novo daqui 30min" o nosso resolve com state machine + cached routes + permanent fail score com decay.



