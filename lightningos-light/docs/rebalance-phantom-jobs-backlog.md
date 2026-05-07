# Rebalance — Phantom Jobs (jobs failed sem nenhuma tentativa)

**Snapshot:** 2026-05-07, version 0.3.24-Beta
**Origem:** análise das últimas falhas em prod identificou que ~37 % dos jobs marcados como `failed` nunca executaram nenhuma tentativa — foram pulados por cache de falha recente.

## Sintoma observado

Dump de 200 jobs do `/api/rebalance/history`:

| Reason | n | Tentou? |
|---|---:|---|
| `all sources failed` | 91 (45,5 %) | ✅ Sim — attempts persistidas |
| `all sources skipped (recent failures)` | **74 (37 %)** | ❌ **Não** — 0 attempts |
| `mpp structural failure` | 25 (12,5 %) | parcial |
| `remaining N sats` | 5 (2,5 %) | parcial sucesso |
| sucesso | ~5 (2,5 %) | ✅ |

Cruzamento com `rebalance_attempts`: **0 dos 21 jobs `skipped` que estão dentro do dump tem qualquer attempt persistida.** Duração: 1-2 segundos. São jobs que entraram, encontraram todas as sources do par cool, e saíram.

## Causa raiz

`runManualRestartWatch` ([rebalance_service.go:1834-1908](../internal/server/rebalance_service.go#L1834-L1908)):

- ✅ Verifica `shouldCooldownTargetRecentFailures` (cooldown de target).
- ❌ **Não verifica** cooldown por **par** (source→target). Cria o job mesmo quando todos os pares para aquele target estão em cache de falha recente.

Quando o job entra no runner ([linha 4336-4347](../internal/server/rebalance_service.go#L4336-L4347)), itera cada source, vê que `shouldSkipPairForRecentFailure` retorna `true` para todos, incrementa `skippedByCache`, sai do loop, e cai em [linha 4520-4524](../internal/server/rebalance_service.go#L4520-L4524):

```go
if !attemptedAny && skippedByCache > 0 {
    s.finishJob(jobID, "failed", "all sources skipped (recent failures)")
    return
}
```

`status='failed'` é uma classificação errada — é literalmente um job que **não falhou**, apenas foi suprimido por cache.

### TTLs do pair cache que alimentam o ciclo

`pairFailureBaseTTL` por reason ([rebalance_service.go:1435](../internal/server/rebalance_service.go#L1435)):

| Reason | TTL base |
|---|---:|
| "no matching outgoing channel" | 45 min |
| "insufficient local balance" | 45 min |
| "unable to find a path" | 20 min |
| "no route" | 20 min |
| "probe returned no amount" | 15 min |
| "mpp structural failure" | 15 min |
| "temporary_channel_failure" | 5 min |
| "timeout" / "deadlineexceeded" | 5 min |

Dobra por `failCount` (cap 1 h via `pairFailTTLMax`). Soma com `permanentFailScoreTTL` (Onda 4.2): `(score - 3 + 1)` horas, cap 6 h via `permanentFailScoreTTLMax`.

`runManualRestartWatchLoop` roda a cada `manualRestartInterval(cfg) = scan_interval_sec` (default 15 min). Em qualquer ciclo dentro dos 5 min - 6 h de cache, ele recria um job para um target cujos pares estão todos cool → `failed: all sources skipped` imediato.

## O que NÃO está contaminado

A SQL de `effectiveness_7d` ([linha 8840-8875](../internal/server/rebalance_service.go#L8840-L8875)) filtra `attempted_count = jobs com >=1 attempt`:

```sql
effectiveness = success_count / attempted_count   -- exclui phantom jobs
effectivenessExecution = successful_attempt_count / attempt_count  -- attempt-level
jobsWithoutAttemptRate = jobs_without_attempt / total
```

→ `effectiveness_7d=10,4 %`, `effectivenessExecution_7d=3,17 %`: **limpas**. As 7 227 attempts/24h também são attempt-level (tabela `rebalance_attempts`); phantom jobs têm 0 attempts → 0 contribuição.

Não há corrupção de cache: o runner não chega a chamar `recordPairFailure`, `permanent_fail_score`, MC, nem `pair_stats`. Phantoms não envenenam estado.

## O que ESTÁ contaminado

1. **Visualmente** — qualquer dashboard que mostre "X jobs failed" inclui phantoms. Operador vê catástrofe quando boa parte é só cache supressão.
2. **Operacionalmente** — DB writes, eventos `RebalanceEvent`, queue churn, log noise. Cada ciclo do watcher pode criar dezenas de jobs vazios.
3. **`scheduleManualRestart` em loop** — `shouldManualRestart` ([linha 5899](../internal/server/rebalance_service.go#L5899)) retorna `true` para `failed`, o que agenda outro restart após `manualRestartInterval`. Em jobs phantom, o restart agendado vai re-skipar (cache TTL 5min-6h ainda ativo). Reentrada de churn.
4. **`jobs_without_attempt_rate_7d=53,2 %`** — métrica existe, mas está enterrada na overview e o operador não tem visualização clara.

## Estamos perdendo oportunidades reais?

**Resposta curta:** marginal. O cache é conservador mas direcionalmente correto.

**Detalhe:**

- O cache é por **par** (source, target), não por target. Sources cujos pares ainda não falharam para aquele target permanecem disponíveis. Em prod hoje, todos os 74 phantoms eram targets cuja **rede inteira** de pares estava em cache simultaneamente — sinal de target estruturalmente quebrado, não de cache hipersensível.
- TTL máximo de 1 h base + 6 h permanente. Se um peer recupera (rebalance interno, fee change, MC clear), perdemos o detect por até ~6 h em casos extremos. Em mercado normal, o detect leva 15-45 min.
- `cooldown-probe` jobs ([linha 144](../internal/server/rebalance_service.go#L144)) JÁ existem para forçar re-teste de targets em cooldown — mas hoje passam pelo MESMO pair cache (Fix C ataca isso), então quando todos os pares estão cool, o probe também skipa.
- Métrica indireta: dos 47 % de jobs que tentam, só 3,17 % das attempts sucedem (`effectivenessExecution_7d`). Mesmo quando tentamos, falhamos. **Cache não é gargalo principal — peer set / mercado é.**

**Estimativa grosseira:** 5-10 % de oportunidades perdidas em mercado normal por causa do cache. Em mercado lento (atual), provavelmente menor (peers genuinamente quebrados continuam quebrados durante o TTL). Para medir com precisão, falta instrumentação: "quantos pares saíram do cache e tiveram sucesso na primeira tentativa pós-TTL?". Item de telemetria a adicionar.

**Conclusão:** o cache não é o problema. O problema são os jobs phantom (criados sabendo que vão skipar) — esses **não custam oportunidade**, custam ruído.

---

## Fix A — Status `skipped` separado de `failed` ✅ FEITO em 2026-05-07

**Status:** ✅ implementado em 0.3.24-Beta+ (não-deployed, aguarda commit)

### Mudanças

1. [rebalance_service.go:4521](../internal/server/rebalance_service.go#L4521): troca `"failed"` → `"skipped"` em `finishJob` quando `!attemptedAny && skippedByCache > 0`.
2. [rebalance_service.go:5483-5488](../internal/server/rebalance_service.go#L5483-L5488): SQL de `loadRecentTargetNoAttemptCooldowns` passa a filtrar `j.status='skipped'` em vez de `status='failed' AND reason='all sources skipped...'`. Adiciona `'skipped'` à IN list (linha 5488) para incluir esses jobs no events CTE.
3. [rebalance_service.go:5899-5907](../internal/server/rebalance_service.go#L5899-L5907): `shouldManualRestart` passa a retornar `false` para `skipped`. Justificativa: re-agendar restart imediato gera mais phantoms — o `runManualRestartWatchLoop` (a cada 15 min) já cobre o re-test natural.
4. [rebalance_service.go:9289, 9331](../internal/server/rebalance_service.go#L9289): adiciona `'skipped'` à IN list das queries de history endpoint. Garante visibilidade dos jobs skipped quando operador consulta.
5. [ui/src/pages/RebalanceCenter.tsx:100](../ui/src/pages/RebalanceCenter.tsx#L100): estende `historyFilter` para incluir `'skipped'`. Adiciona botão de filtro correspondente.
6. [ui/src/pages/RebalanceCenter.tsx:409-420](../ui/src/pages/RebalanceCenter.tsx#L409-L420): `statusClass` tem `case 'skipped'` retornando `'text-sky-300'` (cinza-azulado, distinto de vermelho-failed).

### Não foi alterado nesta entrega

- **`rebalance_metrics_daily`** ([insert na linha 8710](../internal/server/rebalance_service.go#L8710)): mantém colunas atuais `jobs_total/succeeded/partial/failed/cancelled`. Skipped não aparece como bucket próprio na cache table. Consequência: jobs `skipped` não compõem o `jobs_total` da baseline daily (a query [linha 8595-8599](../internal/server/rebalance_service.go#L8595-L8599) tem IN clause sem 'skipped'). Operador continua vendo `jobs_total` reduzido (mais honesto: "jobs que tentamos algo"). Adicionar coluna `jobs_skipped` é sub-task de Fix B.
- **`baseQuery`** ([linha 9285-9291](../internal/server/rebalance_service.go#L9285-L9291)): foi adicionada `'skipped'` à IN list, então jobs phantom continuam visíveis no histórico — só com status correto.
- **Outros filtros `status='failed'`** (`fetchMppShadowTelemetry24h`, `fetchMppStructuralAbortJobs24h`): não tocados, pois o `reason` desses já filtra para tipos específicos que NÃO incluem skipped.

### Validação esperada (após deploy)

- `jobs_failed` em `/api/rebalance/metrics/baseline` cai ~37 %.
- UI mostra os jobs skipped em filter "Todos" com badge cinza-azulada.
- `manual_restart_reasons` no overview agora mostra padrões mais limpos (sem o eco do per-job-restart phantom).
- `attempt_success_rate_24h` permanece igual (era limpo antes).

---

## Fix B — Pre-check de pair cache no watcher

**Status:** ⏳ PENDENTE
**Esforço estimado:** 1 dia (lógica + 3-4 testes + golden update)
**Risco:** médio — muda a lista de jobs que entram em queue, pode mascarar caso de "source acabou de sair de cooldown"

### O que mudar

Em `runManualRestartWatch` ([linha 1873-1907](../internal/server/rebalance_service.go#L1873-L1907)), antes do `startJob`, verificar se há pelo menos uma source cujo par com este target não está em cache:

```go
// Preload pair stats (já feito uma vez por scan, não por target):
pairStats := s.loadAllPairStatsForTargets(ctx, targetIDs)  // batched

// Para cada channel candidato:
sources := s.eligibleSourcesFor(ch, snapshot, ledger)
sourceAvailable := s.computeSourceAvailable(sources, snapshot)

if !hasRebalanceFallbackCandidate(sources, sourceAvailable, pairStats[ch.ChannelID], nil, true, minExecuteSat, scanAt) {
    noteManualSkip("no_pair_route_available")
    continue
}
```

[`hasRebalanceFallbackCandidate`](../internal/server/rebalance_service.go#L1381) já existe — só precisa ser plumbada no watcher (que hoje não tem `sources`/`sourceAvailable` carregados).

### Pré-requisitos

- Extrair função `eligibleSourcesFor(ch, snapshot, ledger) []RebalanceChannel` do que hoje está dentro do `runJob` ([linha ~3000+](../internal/server/rebalance_service.go#L3000)).
- Adicionar query `loadAllPairStatsForTargets(ctx, []uint64) map[uint64]map[uint64]pairStat` para batchear a busca (uma SQL para todos os targets em vez de N).

### Sub-task: coluna `jobs_skipped` em baseline

Aproveitar a janela do Fix B para:
- Migration: `alter table rebalance_metrics_daily add column if not exists jobs_skipped bigint default 0`
- Update insert/upsert no [`snapshotMetricsDay` linha 8710](../internal/server/rebalance_service.go#L8710).
- Update reader struct e UI badge.

### Critério de aceite

1. Test unit: `runManualRestartWatch` não cria job quando todos os pares para o target estão em cache.
2. Test golden: snapshot de cenário "target X com 5 sources, 5 cools" → 0 jobs criados (vs 1 job phantom hoje).
3. Em prod 48h após deploy: `jobs_without_attempt_rate_7d` cai abaixo de 5 % (vs 53 % atual).

### Tradeoffs

- **Reduz drasticamente phantom jobs**: ganho principal.
- **Risco de dead-lock visual**: se pair cache está sempre cool e watcher nunca tenta, operador pode achar que sistema parou. Mitigação: log explícito + tag em `last_manual_restart_reasons` (`no_pair_route_available`).
- **Custo de SQL**: uma query batch a cada 15 min. Pequeno em relação ao trabalho atual.
- **Acoplamento**: o watcher agora precisa do mesmo subset de lógica que `runJob`. Refactoring exige `eligibleSourcesFor` testável independente.

---

## Fix C — Cooldown probe bypassa pair cache

**Status:** ⏳ PENDENTE
**Esforço estimado:** 2-4 horas (lógica + 2 testes)
**Risco:** baixo-médio — probes podem gastar mais; mitigado pelo `targetCooldownProbeMaxSources=2`

### O que mudar

Em [`shouldUseRecentFailureCache`](../internal/server/rebalance_service.go#L1333):

```go
func shouldUseRecentFailureCache(jobSource string, jobReason string) bool {
    jobReason = strings.TrimSpace(strings.ToLower(jobReason))
    if jobReason == targetCooldownProbeReason {
        return false  // probe deve testar, não pular
    }
    jobSource = strings.TrimSpace(strings.ToLower(jobSource))
    if jobSource == "auto" {
        return true
    }
    return jobSource == "manual" && jobReason == "auto-restart"
}
```

### Justificativa

`cooldown-probe` jobs existem para **furar o cache** e detectar recovery de peers. Hoje, com `useRecentFailureCache=true`, o probe respeita o pair cache e skipa — defeating its own purpose. Probes devem tentar 1-2 sources mesmo se o pair cache diz "cool" (cap já existe via `targetCooldownProbeMaxSources=2` em [linha 146](../internal/server/rebalance_service.go#L146)).

### Critério de aceite

1. Test unit: probe job (`reason=cooldown-probe`) não pula sources via pair cache.
2. Em prod: success rate de probe jobs sobe (atualmente são jobs `cooldown-probe` que mostram `all sources failed` — após Fix C, alguns vão tentar e ter chance).
3. Volume total de attempts/24h **não** aumenta significativamente (cap em 2 sources mantém custo controlado).

### Tradeoffs

- **Reabilita detect de recovery**: ganho principal em mercado lento ou após eventos de rede.
- **Custo extra**: ~2 attempts × probe_count por hora. Em prod hoje seria ~10-20 attempts/h adicionais.
- **Pode envenenar pair cache mais rápido**: se probe falha, o pair vai pra cache de novo — mas isso é o estado correto (testou, falhou, marca).

---

## Telemetria que falta (sub-task do Fix B ou separado)

Para responder com precisão "quantas oportunidades estamos perdendo via cache":

1. **Counter `pair_cache_skip_total` por (source, target, reason)** — quantas vezes pulamos cada par no último 7d.
2. **Counter `pair_cache_skip_recovered`** — quantas vezes um par saiu do cache e teve sucesso na primeira tentativa pós-TTL. Razão `recovered/skipped` mostra eficiência do cache.
3. **Histograma de TTL real até primeiro sucesso pós-cache** — distribuição mostra se TTLs estão calibrados.

Esses contadores transformam "cache é conservador" de hipótese em medida.

---

## Ordem sugerida

```
[Fix A — rename status]                ← FEITO
  ↓ (deploy + medir 24h)
[Fix B — pre-check no watcher]
  ↓ (deploy + medir jobs_without_attempt_rate)
[Fix C — probe bypassa cache]
  ↓ (deploy + medir success rate de probes)
[Telemetria de cache] (opcional, ~1d)
```

Cada item independente. Fix B e C não dependem entre si. Telemetria pode preceder ou seguir Fix B/C — provavelmente útil ANTES para baseline.
