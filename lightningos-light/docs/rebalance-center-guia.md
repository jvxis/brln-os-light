# Rebalance Center — Guia do Operador

**Versão:** 0.4.4-Beta
**Última revisão:** 2026-05-26

Este documento descreve como o módulo de rebalance do LightningOS funciona,
quais módulos compõem o sistema, e quando usar cada um. Voltado para o operador
do nó que precisa decidir como configurar o Rebalance Center sem mergulhar no
código.

---

## 1. Visão geral

O Rebalance Center é responsável por **mover liquidez entre canais Lightning**
de forma a manter os canais aptos a fazer forwards lucrativos. Ele faz isso
emitindo pagamentos "circulares" (do próprio nó para o próprio nó) que
atravessam um canal *source* (com saldo local sobrando) e desembocam num
canal *target* (que precisa de saldo local).

Objetivos por ordem de importância (na operação real):

1. **Manter o nó em movimento** (volume de forwards = receita)
2. **Cobrir o custo do rebalance** com a fee paga pelos forwards futuros
3. **Não esgotar canais lucrativos** ao usá-los como source
4. **Reabilitar canais drenados** quando vale a pena economicamente

O sistema oferece **três modos operacionais** que podem ser combinados,
e suportes auxiliares (fast-path, exploração, scoring) que serão detalhados
nas seções seguintes.

---

## 2. Os três modos de operação

### 2.1 Auto (per-channel — `setting.AutoEnabled`)

**Quando usar:** canais cuja matemática é positiva (spread cobre custo de rebal).
O sistema vai escolher entre todos os canais marcados como `auto` os mais
lucrativos a cada ciclo de scan.

**Como funciona:**

- Loop `runAutoLoop` roda a cada `scan_interval_sec`
- A cada scan, o sistema computa o `EligibleAsTarget` de cada canal (filtro
  estrito que inclui *cost gate*)
- Candidatos elegíveis entram no **Funil A** (ROI, profit, etc.)
- Candidatos sobreviventes são ranqueados por score econômico
- Top N (`max_concurrent`) são enviados para execução

**Filtros aplicados:**

- `Active && deficit > deadband && outFee > peerFee`
- **Cost gate 1.4:** `(outFee − peerFee) × econ_ratio > expectedCost`
- ROI guardrail: `roi >= roi_min`
- Profit guardrail: `gain >= cost` (suprimido se `roi_min < 1`)
- Cooldowns recentes de falhas estruturais

### 2.2 Manual Restart (per-channel — `setting.ManualRestartEnabled`)

**Quando usar:** canais estratégicos que **precisam** estar líquidos
independente da matemática. Tipo: canal pra serviço parceiro, canal de
recebimento crítico.

**Como funciona:**

- Loop `runManualRestartWatchLoop` roda a cada `scan_interval_sec`
- Filtro mais frouxo: `EligibleAsManualTarget` (SEM cost gate 1.4)
- Sem scoring/ranking — todos os elegíveis viram job
- Após qualquer fail/partial, `scheduleManualRestart` re-agenda um follow-up
  depois de `manualRestartInterval`

**Diferença chave:** sem cost gate, canais "deficitários" são rebalanceados
mesmo quando o spread não cobre o custo histórico. O custo dessa decisão fica
com o operador.

### 2.3 Sovereign Autopilot (`scheduler_mode = sovereign_*`)

**Quando usar:** quase sempre. É a camada moderna que **substitui** os outros
dois quando ativada. Centraliza o scoring multi-fator e o orçamento diário.

**Como funciona:**

- Quando `scheduler_mode == sovereign_live` (ou `sovereign_shadow`):
  - O `runAutoScan` chama `executeSovereignAutopilot` em vez do legacy loop
  - O `runManualRestartWatch` **bail-out** imediatamente (cede prioridade
    ao sovereign)
- Coleta candidatos de:
  - Canais com `AutoEnabled=true`
  - Canais com `ManualRestartEnabled=true` *(quando `sovereign_candidate_scope = auto_and_manual_restart`)*
- Aplica `EligibleAsTarget` estrito (incluindo cost gate) em **todos**
- Score multi-fator: `economic_score × multipliers`
  - SuccessMultiplier, ROIMultiplier, BudgetEfficiencyMultiplier
  - UnsoldLiquidityMultiplier, RealizedEconomicsMultiplier
  - TargetClassMultiplier (slow_high_margin, fast_seller, etc.)
- Ranqueia, aplica exploração (epsilon-greedy), seleciona top `max_jobs_per_cycle`

**Modos:**

- `sovereign_shadow`: **avalia** mas não cria jobs. Telemetria pura, custo zero.
  Usado para baseline antes de habilitar live.
- `sovereign_live`: executa os candidatos selecionados.
- `rules_auto`: desliga sovereign; volta aos loops Auto + Manual Restart
  separados.

### 2.4 Como os modos coexistem

```
                  scheduler_mode = ?

      ┌───────── rules_auto ─────────┐         ┌──── sovereign_live ────┐
      │                              │         │                        │
runAutoScan                runManualRestartWatch        runAutoScan
  → legacy loop              → loose filter              → sovereign autopilot
  → AUTO channels            → MANUAL_RESTART            (unifica os dois)
  → strict cost gate         → no cost gate
  → top-N by score           → all eligible queued
                                                       runManualRestartWatch
                                                          → bail-out
```

**Gotcha importante no sovereign_live:** marcar um canal como
`ManualRestartEnabled` **não bypassa cost gate** (o sovereign aplica em
todos). Para isso, use o flag per-channel `auto_bypass_cost_gate`.

---

## 3. Master switch

`auto_enabled` é o **master kill switch** de todos os loops automatizados.
Quando `false`:

- `runAutoScan` retorna imediatamente
- `runManualRestartWatch` retorna imediatamente
- `scheduleManualRestart` timers retornam imediatamente
- Apenas operações iniciadas pelo operador (botão "Manual Rebal In") rodam

Implementado em três gates ([rebalance_service.go:2710](../internal/server/rebalance_service.go#L2710),
[rebalance_service.go:2627](../internal/server/rebalance_service.go#L2627),
[rebalance_service.go:8217](../internal/server/rebalance_service.go#L8217)).

A UI mostra "Rebalance master OFF" em vermelho no header quando
`auto_enabled=false`, e desabilita os controles dependentes.

---

## 4. Funnel A — onde candidatos são filtrados

Antes de chegar ao scoring/ranking, candidatos passam por uma série de filtros.
A ordem importa porque cada um **eliminação imediata** (`continue`):

| # | Filtro | Critério | Skip reason |
|---|---|---|---|
| 1 | EligibleAsTarget | `Active && deficit > deadband && outFee > peerFee` | (não exposto) |
| 2 | Cost gate 1.4 | `(outFee − peerFee) × econ_ratio > expectedCost` | (não exposto) |
| 3 | Target cooldown | `shouldCooldownTargetRecentFailures` | `target_cooldown` |
| 4 | Below min execute | `targetAmount < minExecuteSat` | `below_execute_min` |
| 5 | ROI guardrail | `ROI < roi_min` (se `roiValid`) | `roi_guardrail` |
| 6 | Profit guardrail | `gain < cost` (só quando `roi_min >= 1` ou `<= 0`) | `profit_guardrail` |
| 7 | Recently attempted | dentro de `scan_interval_sec` | `recently_attempted` |

### Bypass cost gate (per-channel)

A flag `auto_bypass_cost_gate=true` desabilita o filtro 2 (cost gate) para o
canal específico. Útil em três cenários:

1. **Canal novo sem histórico** — `expectedCost` cai no floor de 250 ppm e
   bloqueia canais com spread baixo
2. **Canal com cost histórico inflado** — bad luck recente subiu o
   `rebal_ppm_7d` artificialmente
3. **Canal estratégico** — você sabe que vai dar certo, ignora a matemática

A flag NÃO bypassa ROI/profit guardrail nem demais gates downstream.

---

## 5. Modelo de gain (estimateTargetGain)

`gain` é o valor esperado de fee revenue futura por rebalancear o canal.
Existem três modelos selecionáveis via `gain_model_version`:

### v1 (legacy)

```
gain = revenue_7d_sat × (amount / max(local_balance, capacity))
```

Proxy puramente histórico: se o canal sold X sat em 7d, ele provavelmente
sold proporcional ao quanto refilarmos. Falha em canais novos (revenue=0).

### v2 (`gain_model_version = 2`)

```
gain = amount × outFeePpm × spread_effectiveness
```

Onde `spread_effectiveness = max(0, 1 − peerFee/outFee)`.

Assume que o full amount vai ser forwardado eventualmente — otimista, mas
trata cold-start.

### v3 (atual, `gain_model_version = 3`)

```
demand     = max(historical_revenue_7d, drain_rate × 168h)
theoretical = amount × outFee × spread_effectiveness
gain        = min(demand, theoretical)        se demand > 0
gain        = theoretical × 0.75              se cold-start (demand = 0)
```

Combina v1 (sinal de demanda real) com v2 (teto teórico). Em cold-start,
aplica 75% (de theoretical) para evitar bloquear canais novos no
profit_guardrail. Configurável via `gain_v3_cold_start` no código.

**Trade-offs:**
- v2: mais candidatos passam, mas pode encher canais que não vendem
- v3: filtra canais sem demanda real, pode ser conservador demais em cenários
  de descoberta

---

## 6. Sovereign Autopilot — interno

### 6.1 Multi-fator scoring

Cada candidato recebe um score base (gain − cost ou EV-weighted) que é
multiplicado por vários fatores:

| Multiplicador | Propósito |
|---|---|
| **SuccessMultiplier** | Penaliza canais com taxa de falha alta |
| **ROIMultiplier** | Premia canais com ROI bem acima do `roi_min` |
| **BudgetEfficiencyMultiplier** | Premia profit alto relativo ao budget_cost |
| **UnsoldLiquidityMultiplier** | Penaliza canais que receberam liquidez mas não venderam |
| **RealizedEconomicsMultiplier** | Premia canais com sell-through alto e fee payback alto |
| **TargetClassMultiplier** | Premia `slow_high_margin` e `fast_seller`, penaliza `cold/dead` |

Score final = `economic_score × Π(multipliers)`. Cooldown probes têm score
fixo `-1` para garantir prioridade no head.

### 6.2 Target classes

```
slow_high_margin   - vende devagar mas com margem alta (premiado)
fast_seller        - vende rápido com margem normal (premiado)
cold_or_dead       - não vende ou tem muitas falhas (penalizado)
exploration        - dados insuficientes (neutro)
```

### 6.3 Exploração (epsilon-greedy)

`sovereign_exploration_slot_pct` reserva uma fração dos slots de cada ciclo
para candidatos da cauda do ranking (random tail). Objetivo: descobrir canais
que o score base subestima.

**Cálculo:**
```
slots = max(1, max_jobs × pct / 100)
keep_top = max_jobs − slots         // mantém deterministically
explore = slots                      // random do tail
```

### 6.4 M3 — scarcity bypass

Quando o número de candidatos é pequeno relativo a `max_jobs`, marcar TODOS
como exploração faz sentido (não há "escolha" a fazer — todos seriam
queueados de qualquer forma).

**Regra:** quando `non_probe_candidates <= max_jobs × 2`, todos os candidatos
recebem `ExplorationSlot=true`. Isso bypassa os gates empíricos (structural
cooldown, low success, route dead, unsold liquidity, budget efficiency).

Hard gates (cycle limit, expected_profit_below_min, channel busy, budget
refit) continuam aplicando.

### 6.5 Skip reasons no sovereign

Reasons que aparecem no Funnel A + sovereign:

| Reason | Significado | Gate level |
|---|---|---|
| `roi_guardrail` | ROI < roi_min | Funnel A |
| `profit_guardrail` | gain < cost (com roi_min >= 1) | Funnel A |
| `target_cooldown` | falhas recentes acumuladas | Funnel A |
| `recently_attempted` | dentro do scan_interval | Funnel A |
| `below_execute_min` | amount < min_execute | Funnel A |
| `autofee_settling_target` | autofee mexeu na fee recentemente | Sovereign (dampener) |
| `target_structural_cooldown` | falhas estruturais consecutivas | Sovereign |
| `unsold_paid_liquidity_penalty` | rebal anterior não converteu em forward | Sovereign |
| `route_dead_opportunity_below_floor` | maioria das sources falhando | Sovereign |
| `low_success_opportunity_below_floor` | success rate < min | Sovereign |
| `expected_profit_below_min` | profit < min_expected_profit_sat | Hard |
| `channel_busy` | canal com job em andamento | Hard |
| `cycle_limit` | já atingiu max_jobs do ciclo | Hard |

---

## 7. Delegated Fast-Path

Modo opt-in (`delegated_fast_path_enabled`) que **antes** do legacy loop tenta
uma chamada única ao `SendPaymentV2` do LND nativo passando TODAS as sources
elegíveis como `outgoing_chan_ids`. O LND escolhe a melhor source via Mission
Control interna.

**Por que existe:** o pathfinder do LND tem caminhos otimizados para
self-payment (cenário do rebalance). Iterar source-por-source manualmente
perde essa otimização.

**Comportamento:**

1. Job inicia com fast-path attempt (timeout 180s capado)
2. Se sucede: persiste 1 attempt com source vencedor, opcionalmente
   reinforce MC, finaliza job como `succeeded reason="delegated-fast-path"`
3. Se falha: silenciosa fall-through para o legacy loop (não envenena
   pair-cache)

**Configurações relacionadas:**

- `delegated_fast_path_enabled` — liga/desliga
- `delegated_fast_path_strict_payback` — exige sources com payback OK
- `mission_control_reinforce` — escreve a rota vencedora no MC do LND

---

## 8. Mission Control (MC) do LND

A Mission Control é o "cérebro de pathfinding" do LND. Aprende quais rotas
funcionam e quais falham, mantendo um histórico de tentativas com decay.

### 8.1 mc_half_life_sec

Controla a velocidade do esquecimento. **Maior = MC lembra por mais tempo.**

```
Penalty(t) = Penalty(0) × 0.5^(t / half_life)
```

Valores típicos:
- LND default: 3600s (1 hora)
- Conservador: 1800s (30 min) — usado se você quer retentar falhas rápido
- 600s ou menos: muito agressivo, MC quase sem memória

### 8.2 MC Reinforcement

Quando `mission_control_reinforce=true`, após cada rebalance bem sucedido,
o sistema **escreve a rota vencedora de volta na MC do LND** via
`ImportMissionControl`. Isso acelera o aprendizado e ajuda o fast-path em
chamadas futuras.

### 8.3 Reset de MC

Existem dois caminhos:

- **Manual**: botão na UI ou `POST /api/rebalance/mission-control/reset`.
  Usuário aperta quando MC parece "presa" (rotas viáveis sendo recusadas).
- **Automático**: REMOVIDO em 0.4.4-Beta. Antes, falhas estruturais consecutivas
  disparavam reset, mas isso canibalizava o aprendizado do fast-path.

A regra agora: MC só é resetada quando o operador decide.

---

## 9. Loops e cadência

| Loop | Função | Cadência | Master gate |
|---|---|---|---|
| `runAutoLoop` | Cria jobs auto/sovereign | `scan_interval_sec` | `auto_enabled` |
| `runManualRestartWatchLoop` | Watch dos canais manual_restart | `scan_interval_sec` | `auto_enabled` + `manual_restart_watch` |
| `runPairStatsCleanupLoop` | Limpeza diária | 24h | (sempre on) |
| `runStaleJobsCleanupLoop` | Marca jobs órfãos | 1h | (sempre on) |
| `scheduleManualRestart` | Re-agenda após fail/partial | `manualRestartInterval` | `auto_enabled` + `manual_restart_watch` |

Cleanup loops não geram jobs — apenas manutenção de dados.

---

## 10. Parâmetros principais (UI)

### 10.1 Operation panel

- `auto_enabled` — **MASTER SWITCH** (todos os loops dependem)
- `scan_interval_sec` — cadência de scans (600-900s típico)
- `max_concurrent` — limite de jobs concorrentes
- `manual_restart_watch` — habilita o loop de manual_restart watch

### 10.2 Autopilot panel

- `scheduler_mode` — rules_auto / sovereign_shadow / sovereign_live
- `sovereign_candidate_scope` — auto_only / auto_and_manual_restart
- `max_jobs_per_cycle` — quantos candidatos top entram por ciclo
- `sovereign_min_expected_profit_sat` — floor de profit absoluto (sat)
- `sovereign_low_success_min_rate` — threshold de success rate baixo
- `sovereign_low_success_min_profit_cost_ratio` — margem exigida em canais low-success
- `sovereign_budget_efficiency_min_ratio` — profit / budget_cost mínimo
- `sovereign_route_dead_source_share` — % de sources falhando que dispara gate
- `sovereign_risk_score_floor` — risk score mínimo para entrada
- `sovereign_attribution_window_hours` — janela para atribuir forwards a rebalances
- `sovereign_slow_seller_window_hours` — janela para slow seller detection
- `sovereign_target_source_quarantine_hours` — espera após canal virar source
- `sovereign_structural_cooldown_repeat_hours` — cooldown após falhas estruturais repetidas
- `sovereign_exploration_slot_pct` — % de slots reservado para exploração
- `sovereign_source_opportunity_cost_enabled` ("Protect profitable sources")
- `sovereign_slow_seller_enabled`
- `sovereign_ev_weighted_scoring` (experimental)

### 10.3 Per-channel settings

- `setting.AutoEnabled` — canal entra no pool auto
- `setting.ManualRestartEnabled` — canal entra no pool manual_restart
  *(exclusivos — só um pode estar ligado por canal)*
- `setting.TargetOutboundPct` — % de outbound desejado (default 30)
- `setting.UseDefaultEconRatio` / `EconRatioOverride` — multiplicador no spread
- `setting.AutoBypassCostGate` — bypassa cost gate per canal

### 10.4 Budget & guardrails

- `daily_budget_pct` — % da revenue do dia anterior como budget
- `budget_unlimited` — desliga circuit breaker (cuidado)
- `roi_min` — ROI mínimo (1.0 = break-even; < 1 = aceita loss em ROI)
- `rebalance_cost_floor_ppm` — floor para `expectedCost` quando não há histórico

---

## 11. Decisões operacionais

### 11.1 Como tunar para lucro vs movimento

**Foco em lucro** (cost/rev < 80% consistente):
- `roi_min = 1.0` ou maior
- `sovereign_min_expected_profit_sat = 50+`
- `sovereign_budget_efficiency_min_ratio = 0.25+`
- `sovereign_exploration_slot_pct = 10` (baixo)
- `sovereign_source_opportunity_cost_enabled = true`

**Foco em movimento** (maximizar volume mesmo com prejuízo marginal):
- `roi_min = 0.5`
- `sovereign_min_expected_profit_sat = 5`
- `sovereign_budget_efficiency_min_ratio = 0.15`
- `sovereign_exploration_slot_pct = 30`
- Bypass cost gate em mais canais

**Limite ético**: não baixar `sovereign_min_expected_profit_sat` abaixo de 0.
Jobs com EV negativo na conta são perda real.

### 11.2 Como debugar canais "presos"

Canal não aparece como candidato:

1. **Verifique `EligibleAsTarget`** via channel ranking — se false, ver
   `active`, `deficit`, `outFee > peerFee`
2. **Cost gate**: compare `(outFee − peerFee) × econ_ratio` com `rebal_ppm_7d`
   - Se gate falha mas matemática parece OK, considere `auto_bypass_cost_gate`
3. **ROI guardrail**: ROI = gain/cost. Veja `expected_roi` em
   sovereign-history → decisions
4. **Cooldown**: skip reason `target_cooldown` ou `target_structural_cooldown`
5. **Sovereign não inclui** se canal não é `AutoEnabled` E
   `sovereign_candidate_scope == auto_only`

Canal aparece mas não é selecionado:

1. **Score baixo**: olhe os multipliers no decision (`success_multiplier`,
   etc.) — algum próximo de zero?
2. **Cycle limit**: max_jobs atingido por candidatos com score mais alto
3. **Channel busy**: já tem job em andamento
4. **Empirical gates**: `target_structural_cooldown`, `unsold_paid_liquidity`,
   etc. — exploration bypass pode ajudar

### 11.3 Quando ligar `auto_bypass_cost_gate`

**Sim:**
- Canal novo sem `rebal_ppm_7d` histórico (floor 250 ppm bloqueia spreads baixos)
- Canal com matemática justificável (out_ppm > rebal_ppm) mas econ_ratio
  comendo a margem
- Canal estratégico que precisa estar líquido

**Não:**
- Canal com `out_ppm < rebal_ppm` consistente (perde dinheiro em rebal)
- Em vez disso, suba `outFee` (manual ou via autofee)

### 11.4 sovereign_shadow vs sovereign_live

- Use `shadow` quando: testando config nova, observando comportamento,
  recovering de cenário ruim (canais drenados, MC desorganizada)
- Use `live` quando: configuração estabilizada, métricas saudáveis
- Transição: rode 6-12h em shadow, compare expected_profit médio e candidate
  count com expectativa antes de flipar para live

---

## 12. Anexo — histórico de mudanças relevantes

| Versão | Mudança |
|---|---|
| 0.3.20 | Wave 1: cost gate 1.4, fee floor warm, adaptive start amount |
| 0.3.22 | Wave 3: gain v2 + velocity weight, drain rate cache |
| 0.3.24 | Wave 4: PermanentFailScore, route hops cache |
| 0.3.25 | Delegated fast-path, cooldowns mais frouxos |
| 0.4.0 | Sovereign autopilot + gain v3 (opt-in) |
| 0.4.3 | Exploration slot (epsilon-greedy), configurable structural cooldown |
| 0.4.4 | Master switch real (auto_enabled gates todos os loops); MC auto-reset removido; profit_guardrail respeita roi_min; bumped gain v3 cold-start prior to 0.75 |

---

## 13. Arquivos relevantes no código

- `internal/server/rebalance_service.go` — toda a lógica de scheduling, scan,
  execução, MC, sovereign autopilot (arquivo grande ~13k linhas)
- `internal/server/rebalance_handlers.go` — handlers HTTP
- `internal/lndclient/rebalance.go` — wrappers gRPC LND
  (incluindo `SendPaymentMultiSource` do fast-path)
- `ui/src/pages/RebalanceCenter.tsx` — UI inteira do Rebalance Center
- `ui/src/components/rebalance/` — componentes reusáveis

Para detalhes sobre módulos específicos:

- `docs/lndg-parity-investigation.md` — por que e como o fast-path existe
- `docs/sovereign-autopilot-audit.md` — auditoria do sovereign
- `docs/autofee-backlog.md` — interlock com autofee
- `docs/rebalance-phantom-jobs-backlog.md` — como o sistema lida com jobs
  sem attempts
- `docs/autopilot-autotarget-backlog.md` — proposta de ajuste automático do
  target_outbound_pct (não implementado)
