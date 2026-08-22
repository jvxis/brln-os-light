# Rebalance Center — Guia operacional + Backlog

**Versão atual em produção:** 0.4.4-Beta
**Última revisão:** 2026-05-26

Documento consolidado em duas partes:

- **[Parte I — Como o Rebalance Center funciona](#parte-i--como-o-rebalance-center-funciona)** —
  referência operacional para o operador que precisa entender e configurar o
  sistema sem mergulhar no código
- **[Parte II — O que ainda falta implementar](#parte-ii--o-que-ainda-falta-implementar)** —
  backlog priorizado com quick wins primeiro

---

# Parte I — Como o Rebalance Center funciona

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
e suportes auxiliares (fast-path, exploração, scoring) detalhados nas seções
seguintes.

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

- `sovereign_shadow`: **avalia** mas não cria jobs, não cai no rules-auto, não
  enfileira guaranteed slots e não aplica mudanças do AutoTarget. Telemetria
  pura, custo zero. Usado para baseline antes de habilitar live.
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

## 3. Master switch

`auto_enabled` é o **master kill switch** de todos os loops automatizados.
Quando `false`:

- `runAutoScan` retorna imediatamente
- `runManualRestartWatch` retorna imediatamente
- `scheduleManualRestart` timers retornam imediatamente
- Apenas operações iniciadas pelo operador (botão "Rebal In") rodam

### Vaga garantida por canal

O checkbox `Garantir vaga` adiciona o `channel_id` exato a uma fila de
convicção do operador. No início de cada auto scan, antes de `rules_auto` ou
Sovereign, no máximo um canal marcado e abaixo do alvo é enfileirado. Canais
críticos (`outbound < 1,5%`) vêm antes dos altos (`outbound < 5%`); dentro da
mesma faixa vence o que está há mais tempo sem job automático.

Essa fila ignora score e filtros econômicos/históricos, mas continua sujeita
ao budget automático, fee cap, canal ocupado, déficit/mínimo executável e
viabilidade operacional de sources/rota. O ID também é exposto como
`channel_id_str` para não perder precisão no JavaScript e manter separados
canais paralelos do mesmo peer.

Implementado em três gates ([rebalance_service.go:2710](../internal/server/rebalance_service.go#L2710),
[rebalance_service.go:2627](../internal/server/rebalance_service.go#L2627),
[rebalance_service.go:8217](../internal/server/rebalance_service.go#L8217)).

A UI mostra "Rebalance master OFF" em vermelho no header quando
`auto_enabled=false`, e desabilita os controles dependentes.

## 4. Funnel A — onde candidatos são filtrados

Antes de chegar ao scoring/ranking, candidatos passam por uma série de filtros.
A ordem importa porque cada um é **eliminação imediata** (`continue`):

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
profit_guardrail.

**Trade-offs:**

- v2: mais candidatos passam, mas pode encher canais que não vendem
- v3: filtra canais sem demanda real, pode ser conservador demais em cenários
  de descoberta

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

## 9. Loops e cadência

| Loop | Função | Cadência | Master gate |
|---|---|---|---|
| `runAutoLoop` | Cria jobs auto/sovereign | `scan_interval_sec` | `auto_enabled` |
| `runManualRestartWatchLoop` | Watch dos canais manual_restart | `scan_interval_sec` | `auto_enabled` + `manual_restart_watch` |
| `runPairStatsCleanupLoop` | Limpeza diária | 24h | (sempre on) |
| `runStaleJobsCleanupLoop` | Marca jobs órfãos | 1h | (sempre on) |
| `scheduleManualRestart` | Re-agenda após fail/partial | `manualRestartInterval` | `auto_enabled` + `manual_restart_watch` |

Cleanup loops não geram jobs — apenas manutenção de dados.

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
- A receita realizada usa atribuição FIFO por canal de destino: cada forward
  consome primeiro o lote de liquidez paga elegível mais antigo e não pode ser
  contado em dois jobs com janelas sobrepostas

## 12. Histórico de mudanças relevantes

| Versão | Mudança |
|---|---|
| 0.3.20 | Wave 1: cost gate 1.4, fee floor warm, adaptive start amount |
| 0.3.22 | Wave 3: gain v2 + velocity weight, drain rate cache |
| 0.3.24 | Wave 4: PermanentFailScore, route hops cache |
| 0.3.25 | Delegated fast-path, cooldowns mais frouxos |
| 0.4.0 | Sovereign autopilot + gain v3 (opt-in) |
| 0.4.3 | Exploration slot (epsilon-greedy), configurable structural cooldown |
| 0.4.4 | Master switch real (auto_enabled gates todos os loops); MC auto-reset removido; profit_guardrail respeita roi_min; bumped gain v3 cold-start prior to 0.75 |

## 13. Arquivos relevantes no código

- `internal/server/rebalance_service.go` — toda a lógica de scheduling, scan,
  execução, MC, sovereign autopilot
- `internal/server/rebalance_handlers.go` — handlers HTTP
- `internal/lndclient/rebalance.go` — wrappers gRPC LND
  (incluindo `SendPaymentMultiSource` do fast-path)
- `ui/src/pages/RebalanceCenter.tsx` — UI inteira do Rebalance Center
- `ui/src/components/rebalance/` — componentes reusáveis

Docs relacionados:

- `lndg-parity-investigation.md` — por que e como o fast-path existe
- `sovereign-autopilot-audit.md` — auditoria do sovereign
- `autofee-backlog.md` — interlock com autofee (FECHADO)
- `rebalance-phantom-jobs-backlog.md` — phantom jobs (FECHADO)
- `autopilot-autotarget-backlog.md` — design do AutoTarget (R0 deste backlog)

---

# Parte II — O que ainda falta implementar

Itens **não implementados** que emergiram durante o ciclo de tuning de Maio/2026.
Ordenados por **quick wins primeiro**, depois valor estratégico, depois esforço.

## Resumo executivo

Audit date: 2026-06-20.

Use this table as the source of truth for Parte II. Older sections below are
kept as design history and include per-item status notes.

| Status | ID | Item | Notes |
|---|---|---|---|
| Open | R0 | AutoTarget - autopilot calibrando `target_outbound_pct` | No `auto_target_*` config, loop, history, or UI found. |
| Partial | R4 | Source rotation deterministica | Cooldown probes exist; watcher pre-check, batched pair stats, and pair-cache telemetry still open. |
| Partial | R7 | UI polish | Per-channel controls and profiles exist; cost-gate eligibility tags/hierarchy still incomplete. |
| Partial | R8 | AutoFee <-> Rebalance intent interlock | MVP shared intent layer and dedicated Automation Interlock UI are implemented; source preference and explicit upward fee pressure remain open. |
| Done | R1, R2, R3, R5, R6, R9 | Implemented or superseded | Keep sections below as historical context. |

---

## Quick wins (≤ 1 dia, baixo risco)

### R5 — Exploration burnout (track & stop)

**Current status (2026-08-22): done and persisted.** The service marks
exploration jobs in `rebalance_jobs`, restores the 24h failure streak after a
Manager restart, and exposes the marker in queue/history telemetry. A success
starts a fresh streak, so five later failures can still trigger the 12h burnout.
The overview also separates 7d exploration volume, cost, attributed revenue,
net, and sell-through from the total Sovereign result. This classification only
fills after deploying the persisted marker; older jobs cannot be backfilled.
Tests cover both live and restart-recovery behavior. Keep this section as
historical context.

**Esforço:** ~30 min de código + testes
**Risco:** baixo

**O problema:** `sovereign_exploration_slot_pct = 10` reserva 1 slot por scan
pra exploração random tail. Mas se o mesmo canal (ex: kappa, RA⚡KO) é
selecionado pela exploração 5 vezes seguidas e **falha todas**, continua
sendo candidato a próxima exploração. Resultado: gasto repetido de orçamento
em canais dead-end.

**Proposta:** track exploration attempts em janela móvel (24h):

```go
type ExplorationStats struct {
    Attempts    int
    Failures    int
    LastFailAt  time.Time
}

// Se canal X teve 5+ exploration attempts em 24h e 0 sucessos,
// remover do pool de exploração por 12h
if stats.Attempts >= 5 && stats.Failures == stats.Attempts {
    excludeFromExploration(channelID, 12*time.Hour)
}
```

**Tradeoffs:**

- ✅ Para de queimar slots em canais comprovadamente quebrados
- ✅ Outros canais ganham as oportunidades de exploração
- ✅ Estado persistido no DB e recuperado após restart
- ⚠️ Canais que se recuperam após o burnout só voltam após 12h

---

### R7 — UI polish

**Current status (2026-06-20): partially open.** Profiles and per-channel
controls exist, but the cost-gate eligibility tag and clearer per-channel
hierarchy are still UI backlog.

**Esforço:** ~2h
**Risco:** zero (cosmético)

**O que falta:**

1. **Hierarquia per-channel mais clara**: hoje `auto_enabled`,
   `manual_restart_enabled`, `auto_bypass_cost_gate` ficam misturados no
   painel do canal. Idealmente:

   ```
   [ ] Auto rebalance (per channel)
       └─ [ ] Bypass cost gate (override)
   [ ] Manual restart watch (per channel)
   [ ] Exclude as source
   ```

2. **Sinalizar eligibility no Channel Ranking**: quando um canal está
   bloqueado pelo cost gate, mostrar tag visual ("⚠ cost gate") na linha
   dele com tooltip explicando

3. **Presets no painel Autopilot**: "Modo movimento", "Modo lucro",
   "Modo conservador" que aplicam combos de knobs com 1 clique

---

### R2 — `gain_v3_cold_start_pct` configurável

**Current status (2026-06-20): done.** The field is configurable in backend,
schema, UI, and tests. Keep this section as historical context.

**Esforço:** 0.5d (campo + UI + migration + 2 testes)
**Risco:** baixo

**Histórico:** Em 0.4.4 mudamos o cold-start prior do gain v3 de 0.5 → 0.75
hardcoded. Resolveu o problema de candidatos zerados pós-removal do auto-reset.

**O que falta:** expor como `sovereign_gain_v3_cold_start_pct` na config
(range 0.5–0.95, default 0.75). Permite operadores tunarem:

- Mais permissivo (0.85–0.95): mais candidatos passam profit_guardrail
- Mais conservador (0.5–0.65): menos jobs em canais cold-start

**Implementação:** seguir template de outros knobs `sovereign_*` (Field em
`RebalanceConfig`, validador, plumbing pra `estimateTargetGainV3`, UI input,
i18n EN+PT-BR).

---

### R1 — Suavizar `sovereignSuccessScoreMultiplier`

**Current status (2026-06-20): done.** The multiplier is now a continuous curve
with tests. Keep this section as historical context.

**Esforço:** 0.5d (lógica + 2-3 testes)
**Risco:** baixo

**O problema:** em [rebalance_service.go:9649-9658](../internal/server/rebalance_service.go#L9649-L9658):

```go
if stats.RecentStructuralFailures >= targetCooldownMinAttempts {  // 25
    multiplier *= 0.05   // 95% de penalidade
} else if stats.RecentStructuralFailures >= 10 {
    multiplier *= 0.12   // 88% de penalidade
} else if stats.RecentStructuralFailures > 0 {
    pressure := 1 - (float64(stats.RecentStructuralFailures) * 0.10)
    if pressure < 0.30 { pressure = 0.30 }
    multiplier *= pressure
}
```

Penalidade × 0.05 é brutal. Canais com 25+ falhas estruturais ficam com
score 5% do original. Combinado com a removal do MC auto-reset, uma falha
estrutural recente vai impactar o canal por horas/dias.

**Proposta:** curva contínua com piso decente:

```go
multiplier = max(sovereign_risk_score_floor,
                 1 / (1 + 0.05 * RecentStructuralFailures))
```

Ou: piso ainda mais permissivo (0.40) para `ExplorationSlot=true`.

**Tradeoffs:**

- ✅ Canais "punidos" voltam a competir no ranking após falhas isoladas
- ⚠️ Cooldown estrutural (`sovereign_structural_cooldown_repeat_hours`) já
  cobre a proteção via mecanismo separado — não precisa de multiplicador brutal

---

### R6 — Telemetria explícita do fast-path

**Current status (2026-06-20): mostly done.** Fast-path 24h telemetry is exposed
through the rebalance overview. A separate `/fast-path-metrics` endpoint was not
found, but the core visibility exists.

**Esforço:** ~1d
**Risco:** zero (puramente observabilidade)

**O problema:** para saber se o fast-path tá funcionando, hoje só contamos
`reason="delegated-fast-path"` em jobs sucedidos. Não sabemos:

- Taxa de **tentativa** do fast-path
- Se ele tá entregando antes do legacy ou fallbackando
- Histograma de tempo até sucesso

**Proposta:**

```go
type FastPathTelemetry struct {
    AttemptsTotal       int64
    SuccessesTotal      int64
    SuccessTimeMsHist   []float64  // p50, p95, p99
    FallthroughToLegacy int64
    FailReasonCounts    map[string]int64
}
```

Endpoint `GET /api/rebalance/fast-path-metrics` retornando agregado das
últimas N horas. UI: painel pequeno com sucesso% nas últimas 24h.

**Por que importa:** calibrar `mc_half_life_sec`,
`delegated_fast_path_strict_payback`, detectar regressões quando algum knob
é mexido.

---

## Médio esforço (1-3 dias, médio risco)

### R3 — MPP plan diversification

**Current status (2026-06-20): done.** `buildMppShadowPlan` includes
diversification behavior and tests. Keep this section as historical context.

**Esforço:** 1-2d
**Risco:** médio (hot path do MPP)

**O problema:** `buildMppShadowPlan` aloca shards proporcionalmente à
`MaxSourceSat` de cada source. Quando uma source é dominante (ex: Harry
Potter com 10M cap vs outras com 1M), o plano coloca quase todos os shards
nela.

Observado no job #239575: 6 shards, 5 contra Harry Potter, 1 contra Satway.
Resultado: efetivamente 2 sources distintos tentados, todos falharam.

**Proposta:** função `buildMppShadowPlan` com cap de **% máximo por source**:

```go
// Nenhuma source pode receber mais de 40% dos shards
maxShardsPerSource = ceil(plannedShards × 0.4)
```

**Critério de aceite:** sucesso por source distinta sobe (menos
"all sources failed" com 1-2 distintas).

---

### R0 — AutoTarget (autopilot calibrando `target_outbound_pct`) ⭐

**Current status (2026-06-20): open.** No `auto_target_*` config, loop, history
table, API, or UI was found.

**Esforço:** ~3 dias
**Risco:** médio (mexe em parâmetro per-channel que afeta deficit/eligibility)
**Doc de design:** [autopilot-autotarget-backlog.md](autopilot-autotarget-backlog.md)

**Por que é destacado:** maior ganho operacional estratégico. Hoje
`target_outbound_pct` é setado manualmente por canal — operadores com 40+
canais não conseguem calibrar todos. Sessões anteriores provaram empiricamente
que ajustar esse target (ex: LQWD-France 15→40) muda dramaticamente o success
rate.

**Conceito:**

```
AutoTarget : AutoFee :: target_outbound_pct : fee_rate_ppm
```

Sistema avalia periodicamente cada canal e ajusta target_outbound_pct
baseado em:

- **Trigger UP** (subir target): `drain_rate_24h` alta + `success_rate` boa
  + revenue alta + canal esgotou múltiplas vezes em 24h
- **Trigger DOWN** (descer target): drain caiu pra zero por 24h+, success
  rate baixa, ou múltiplos structural_cooldown events

**Hysteresis e safeguards:**

- Threshold UP (≥50% success) > DOWN (<25%) — evita flapping
- Cooldown 6h entre mudanças no mesmo canal
- Step 5pp, range [10, 70]
- Per-channel opt-out
- Nova tabela `rebalance_auto_target_history` pra auditoria

**Schema preview:**

```go
AutoTargetEnabled               bool    // default false
AutoTargetMaxPct                int     // default 70
AutoTargetMinPct                int     // default 10
AutoTargetStepPct               int     // default 5
AutoTargetEvalIntervalHours     int     // default 6
AutoTargetMinDrainRateSatPerHr  int64   // default 5000
AutoTargetMinRevenue7dSat       int64   // default 500
AutoTargetUpSuccessThreshold    float64 // default 0.5
AutoTargetDownSuccessThreshold  float64 // default 0.25
```

---

### R4 — Source rotation determinística

**Current status (2026-06-20): partially open.** Cooldown probes and recent
failure-cache bypasses exist, but the deterministic watcher pre-check,
`loadAllPairStatsForTargets`, `eligibleSourcesFor`, and pair-cache recovery
telemetry remain open.

**Esforço:** 2d
**Risco:** médio

**O problema:** `pair_failure_cache` tem TTL 5min-30min escalável. Após
falhas, source fica bloqueada para aquele target. Mas a "saída" do cache só
acontece naturalmente quando TTL expira. Em cenários onde muitos pairs
falham simultaneamente, o cache pode bloquear sources inteiras por horas
mesmo após a condição que causou a falha já ter passado.

**Proposta:** mecanismo de "segunda chance" determinístico:

- A cada N ciclos (ex: 6), revisar todas as sources bloqueadas pelo cache
- Para sources com mais de M minutos no cache, tentar 1 atempt de probe
- Se sucede: limpar o cache para aquele pair
- Se falha: cache renova TTL

**Sobreposição com R0:** AutoTarget também ajuda indiretamente — subindo
o target_outbound_pct de canais ativos, eles esgotam mais e viram target,
empurrando sources antigas de volta ao pool.

---

## Maior esforço (3+ dias, médio risco)

### R8 — AutoFee ↔ Rebalance interlock bidirecional

**Current status (2026-07-14): MVP implemented, phase 2 open.** Timing-based
settling remains as a fallback. The shared layer now supports persisted,
expiring `refill_target` and `protect_fee_floor` intents with
`off`/`shadow`/`enforce` rollout modes. Both Rules Auto and Sovereign consume the
same target intent after eligibility/economic gates and before final ordering.
Source preference and explicit upward fee-pressure intents remain phase-2 work.
Operational control and intent history live in the dedicated **Automation
Interlock** page; Rebalance Center keeps only a contextual mode/count summary.

**Esforço:** 2-3d
**Risco:** médio (toca em duas máquinas de decisão)

**Estado atual:**

- **AutoFee → Rebalance**: já implementado. Quando autofee mexeu fee de um
  canal, rebalance espera `autofee_settling_window_sec` (2h) e aplica
  `autofee_settling_multiplier` (0.75) no score
- **Rebalance → AutoFee**: já implementado (Wave 6.1). AutoFee skipa canais
  com rebalance recente em janela de 30min

**O que falta:** comunicação bidirecional **inteligente** com *intenção*,
não só timing. Exemplo:

- AutoFee detecta canal há horas saturado em outbound → notifica rebalance
  pra priorizar como source
- Rebalance detecta canal saturado em inbound (vendendo bem) → notifica
  autofee pra subir a fee mais agressivamente

**Por que importa:** hoje os dois sistemas trabalham com snapshots mas não
trocam intenção. Pode ocorrer: "autofee sobe fee → rebalance tira target →
autofee desce fee → rebalance vê deficit → tenta rebalancear canal que
autofee acabou de descer".

**Implementação:** tabela compartilhada `rebalance_autofee_intent` com:

- Channel ID
- Direction (pump-target / drain-source / lock-fee-rate)
- Window válida
- Justificativa

Cada sistema lê o intent do outro antes de tomar decisão.

---

## Fora do escopo Rebalance

### R9 — Wallet Flow Sprint 1 — cache de leases

**Current status (2026-06-20): done.** Lease caching and background prune were
implemented outside the rebalance service. Keep this section as historical
context.

**Esforço:** 2-3h
**Risco:** baixo

**O quê:** cache de `ListLeases` em `enrichOnchainUtxos`
(`utxo_manager_handlers.go`). Hoje cada GET no `/api/onchain/utxos` chama
`ListLeases + ListMetadata + Prune` sincronamente. Em wallets grandes,
latência p95 > 200ms.

**Proposta:**

- TTL ~10s para `ListLeases` (padrão de `bitcoin_status_cache.go`)
- Mover `Prune` para ticker em background (5-10 min), fora do path crítico

---

## Itens já implementados em 0.4.4-Beta (referência)

Para evitar confusão sobre o que ainda falta:

- ✅ M1 — Hard skip budget_efficiency respeita ExplorationSlot
- ✅ M2 — Refit re-gating respeita ExplorationSlot
- ✅ M3 — Scarcity bypass (≤ 2× maxJobs marca todos como exploration)
- ✅ profit_guardrail respeita roi_min < 1
- ✅ gain v3 cold-start prior bumped 0.5 → 0.75
- ✅ Master switch real (auto_enabled gates todos os loops)
- ✅ MC auto-reset removido (só manual reset agora)
- ✅ UI: master OFF badge, controles dependentes desabilitam
- ✅ handleSaveAutopilotConfig inclui auto_enabled e manual_restart_watch

---

## Como retomar este backlog

Em sessão futura:

1. Ler este doc (Parte II)
2. Verificar via API (`/api/rebalance/sovereign-history`, métricas)
   se os sintomas que motivaram cada item ainda existem
3. Escolher próximo item considerando o estado atual do sistema
4. **Não bundlar mais de 2 itens por commit** — cada um precisa de
   janela de medição em prod (mínimo 24h) antes do próximo

Antes de implementar qualquer item, abrir o código atual e validar que
file:line citados ainda batem — esta doc é snapshot do estado 0.4.4-Beta.

**Ordem sugerida de execucao apos audit 2026-06-20:**

```
R0 AutoTarget
  -> R8 intent interlock
  -> R4 deterministic source rotation / pair-cache telemetry
  -> R7 remaining UI polish
```

R1, R2, R3, R5, R6, and R9 are no longer active backlog items. Before starting
any remaining item, re-check the code paths and production symptoms that
motivated it.
