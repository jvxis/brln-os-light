# Autofee + Rebalance — Backlog estrutural

**Snapshot original:** 2026-05-07, version 0.3.24-Beta
**Última revisão:** 2026-05-23, version 0.4.4-Beta — **backlog FECHADO** (Items 1, 2, 3 em produção; Item 4 era investigação, sem code change esperado).
**Origem:** análise de desempenho do autofee em prod (jvx-minipc01) cruzada com Rebalance Overhaul Plan (Maio/2026, todas as 8 ondas em produção desde 2026-05-02).

## Estado atual (2026-05-23)

Verificação contra o código atual:

| Item | Status | Onde |
|---|---|---|
| **1** HTLC noisy dampening | ✅ em prod | `htlcClassifiedMinRatio=0.05`, `htlcNoisySampleForwardRateMult=1.5`, `htlcNoisySampleForwardCountMult=1.5` em [autofee_service.go:1052-1054](../internal/server/autofee_service.go#L1052-L1054); telemetria `HTLCNoisySampleApplied` em [linha 385](../internal/server/autofee_service.go#L385); aplicação em [linha 4092](../internal/server/autofee_service.go#L4092) |
| **2** Confidence rebal floor | ✅ em prod | `floorRebalMinSuccessCount=2` em [autofee_service.go:107](../internal/server/autofee_service.go#L107); `hasFloorRebalSignal(rebalAmtSat, capacitySat, successCount)` em [linha 5145](../internal/server/autofee_service.go#L5145); flag `rebalLowConfidence` em [linha 7743](../internal/server/autofee_service.go#L7743) |
| **3** Stepcap por gap | ✅ em prod | Fields `LargeGapStepCapBoost*` no profile struct em [autofee_service.go:565-568](../internal/server/autofee_service.go#L565-L568); defaults por profile (conservative/moderate/aggressive) em [linhas 712-715, 848-849, ...](../internal/server/autofee_service.go#L712) |
| **4** Investigação PermanentFailScore | ⚪ N/A | Item era para "medir antes de mudar". Sem code change esperado. |

**Conclusão:** backlog fechado. Esta página vira referência histórica das decisões.

---

## Contexto observado (revalidar antes de implementar)

Comparativo do estado em prod:

| Métrica | 2026-05-02 (pós-deploy de todas as ondas) | 2026-05-07 (snapshot atual) |
|---|---:|---:|
| success+partial rate | 17,14 % | (a recalcular — `attempt_success_rate_24h=1,34 %`) |
| avg_fee_ppm_paid | 335 | n/d |
| attempts (volume) | 531 jobs/dia | **7 227 attempts/24h, 97 success** |
| sats movidos / sat gasto | 2 986 | n/d |

Houve degradação significativa entre 2026-05-02 e 2026-05-07. Fatores externos prováveis: queda de fluxo de rede; degradação de peers específicos (top dead targets em `route_dead_targets_30m`: BitcoinVN 22, Zireael, lightning honk, CoinPayments, LQWD-England). Antes de implementar qualquer item deste backlog, **medir baseline atual** via `GET /api/rebalance/metrics/baseline?days=7` e comparar com o snapshot de 02/05.

Sintomas que motivam os itens abaixo (snapshot de 2026-05-07 21:42Z, run autofee #N):

- 99 canais ativos: `up=3, down=3, flat=73, same=73` — só 6 canais (~7%) tiveram movimento real.
- `htlc_attempts_total=2198, htlc_forward_fails_total=2172, htlc_classified_total=26` → 98,8 % forward fail, 1,2 % classified.
- `floor_src` distribuição: 40 % outrate, 41 % rebal/rebal-sink, 5 % peg, 4 % seed.
- Casos com `target_gap_pct > 30 %` persistindo várias runs: lnmarkets.com (44 %), 1sats.com (62 %).

---

## Item 1 — HTLC signal dampening dinâmico por classified ratio

**Status:** ✅ EM PRODUÇÃO (verificado 2026-05-23 contra código atual)
**Esforço estimado:** 1-2 dias (lógica + 2-3 testes + golden update)
**Risco:** baixo — adiciona um fator multiplicativo, não muda decisão de fluxo

### O que existe hoje

- `buildHTLCFailureSignals` em [autofee_service.go:3947](../internal/server/autofee_service.go#L3947) separa `policyFails`, `liquidityFails`, `forwardFails`, `unclassifiedFails`.
- Os thresholds de cada signal já são gated separadamente:
  - Policy/liquidity são medidos contra **link-fail population only** ([linhas 3966-3970](../internal/server/autofee_service.go#L3966-L3970)) — comentário in-code explícito.
  - Forward-fail tem soft factors hardcoded em [linhas 1021-1024](../internal/server/autofee_service.go#L1021-L1024): `htlcForwardSoftCountFactor=0.45`, `htlcForwardSoftRateFactor=0.50`.
- Os fatores globais (`htlcGlobalMinCountFactor=0.55`, `htlcGlobalRateFactor=0.75`) são **constantes hardcoded** em [linhas 1015-1016](../internal/server/autofee_service.go#L1015-L1016), aplicados via `applyHTLCGlobalCountFactor`/`applyHTLCGlobalRateFactor`.
- `htlcClassifiedTotal` e `htlcUnclassifiedTotal` são tracked em `htlcSignalMeta` ([linha 3935](../internal/server/autofee_service.go#L3935)), persistidos no resultado e no log diagnóstico.

### O que falta

Não há **feedback dinâmico** do ratio `classifiedTotal/attemptsTotal` para os thresholds. Quando o sample observado está dominado por unclassified failures (caso atual: 1,2 % classified), os thresholds de `forward-hot` continuam nos mesmos níveis baseados em fatores estáticos.

### Por que importa

O sample HTLC atual é majoritariamente "ruído de routing" — provavelmente dominado pelas próprias tentativas de rebalance batendo em peers estruturalmente quebrados. Isso faz `htlc-forward-hot` flagar 26 / 81 canais ativos (32 %), o que ativa `htlc_hot_step_cap_boost` indevidamente e pode contribuir para subidas de fee em canais cujo fail real é externo (rota morta), não local (drain).

### Esboço técnico

Em `buildHTLCFailureSignals`, depois de calcular `classifiedTotal`/`attemptsTotal`, derivar um terceiro fator:

```go
const (
    htlcClassifiedMinRatio = 0.05  // abaixo disso, sample é considerado "noisy"
    htlcNoisySampleForwardRateMult = 1.5
    htlcNoisySampleForwardCountMult = 1.5
)

classifiedRatio := 0.0
if attemptsTotal > 0 {
    classifiedRatio = float64(classifiedTotal) / float64(attemptsTotal)
}
if classifiedRatio > 0 && classifiedRatio < htlcClassifiedMinRatio {
    forwardRateThreshold *= htlcNoisySampleForwardRateMult
    minForwardFails = maxInt(minForwardFails,
        int(math.Ceil(float64(minForwardFails)*htlcNoisySampleForwardCountMult)))
}
```

Importante: aplicar **só ao forward signal**. Policy/liquidity já são imunes (medidos contra link-fail population, não unclassified).

Adicionar campo `meta.NoisySampleApplied bool` para diagnóstico, e tag `htlc-noisy-sample` no resultado por canal afetado.

### Critério de aceite

1. Test unit: ratio < 5 % faz `forwardRateThreshold` e `minForwardFails` subirem; ratio ≥ 5 % mantém valores.
2. Test golden: cenário com 100 attempts, 2 classified e fail rate forward de 10 % NÃO deve produzir `htlc-forward-hot` (atualmente produz).
3. Verificar em prod no snapshot pós-deploy: `htlc_forward_hot` reduz para algo coerente com classification rate (~5-10 % dos canais, não 32 %).

### Tradeoffs

- **Falso negativo possível**: em períodos legítimos de surge de tráfego, ratio classificado também cai temporariamente. Mitigação: o threshold escala (não desliga). Ratio 0,03 ainda permite forward-hot, só exige mais sinal.
- **Não substitui** correção do problema raiz (rebal contra dead targets). É defesa em profundidade.

---

## Item 2 — Confidence-weighted rebal floor

**Status:** ✅ EM PRODUÇÃO (verificado 2026-05-23 contra código atual)
**Esforço estimado:** 2-3 dias (lógica + persistir stats de count + 4-5 testes)
**Risco:** médio — mexe em hot path do floor, afeta 41 % dos canais

### O que existe hoje

- `hasFloorRebalSignal(rebalAmtSat, capacitySat)` em [autofee_service.go:4788](../internal/server/autofee_service.go#L4788) gates pelo **volume agregado**: `rebalAmtSat ≥ floorRebalMinCapFrac × capacitySat`.
- Fallback 21d via `hasRebalFallback21dSignal` ([linha 4386](../internal/server/autofee_service.go#L4386)) quando 7d é fraco.
- `rebalSeedFloorPpm = ceil(perCost × 1.10)` ([linha 7348](../internal/server/autofee_service.go#L7348)) — sink ainda recebe extra margin via `SinkExtraFloorMargin`.
- `rebalFloorPpm = ceil(perCost × 1.10)` em [autofee_service.go:8537](../internal/server/autofee_service.go#L8537) — adotado como floor quando ativo.

### O que falta

A decisão usa apenas **volume agregado** (`rebalAmtSat`). Não há gate por:
- Número de transações de sucesso (`success_count`).
- Taxa de sucesso recente do par (`success_rate`).
- Variância dos custos observados (custo médio pode estar enviesado por 1-2 amostras com fee_limit alto).

Em ambiente atual (1,34 % success rate, 97 sucessos / 99 canais), o `rebal_ppm7d` por canal vem de **0-2 amostras** na maior parte dos casos. Isso vira floor em 41 % dos canais (`floor_src=rebal` ou `rebal-sink`), com base estatística frágil.

### Por que importa

Floor poluído trava preço onde o autofee não consegue baixar, mesmo quando `target_raw` indica queda significativa. Caso concreto observado: `lnmarkets.com` com `target_raw=1171, target_final=1910`, gap de 740 ppm, `floor_src=rebal`.

### Esboço técnico

Estender `hasFloorRebalSignal` para receber também o count de sucessos:

```go
func hasFloorRebalSignal(rebalAmtSat int64, capacitySat int64, successCount int) bool {
    if rebalAmtSat <= 0 || successCount < floorRebalMinSuccessCount {
        return false
    }
    minAmtSat := minFloorRebalSat(capacitySat)
    if minAmtSat <= 0 {
        return true
    }
    return rebalAmtSat >= minAmtSat
}
```

Constante nova: `floorRebalMinSuccessCount = 2` (ou 3, ajustar empiricamente).

Pré-requisito: `rebalStats.ByChannel[channelID]` precisa expor `SuccessCount int` (ou similar). Verificar se já é exposto em `rebalStats` consumido por autofee — pode ser que só `AmtMsat` e `FeeMsat` estejam plumbados. Se não, adicionar query/struct.

Comportamento gracefully-degrading: quando `successCount < threshold`, `rebalFloorSignal=false` → o floor cai pra `outrate` (que tem amostra de tráfego real). Isso já é o comportamento natural se `hasFloorRebalSignal` retornar `false`.

### Critério de aceite

1. Test unit: `hasFloorRebalSignal` retorna `false` quando count<2 mesmo com volume alto.
2. Test golden: canal com 1 rebal sucesso e custo alto NÃO ancora floor em rebal_ppm7d.
3. Verificar em prod: `floor_src=rebal*` cai abaixo de 25 % (versus 41 % atual). Se cair muito (< 10 %), o threshold é alto demais; ajustar.

### Tradeoffs

- **Acelera queda em mercado lento**: que é o que queremos quando o sinal de "custo de rebal" não é confiável.
- **Pode subexpor sinks legítimos**: canais sink com pouco rebal histórico mas alto custo unitário. Mitigação: o `seed-floor` derivado de Amboss + `outrate` cobrem a maior parte. Validar com canais top de receita.
- **Acoplamento maior com sucesso de rebalance**: se rebalance estiver patológico (caso atual), o autofee fica mais permissivo, o que pode criar feedback positivo (fee mais baixo → mais forward → menos necessidade de rebal). Pode ser virtuoso ou colapsar — testar em sub-conjunto.

---

## Item 3 — Stepcap relax por target_gap_pct

**Status:** ✅ EM PRODUÇÃO (verificado 2026-05-23 contra código atual)
**Esforço estimado:** 1 dia (lógica + 2 testes)
**Risco:** baixo — incremental sobre infra existente de stepcap dinâmico

### O que existe hoje

Múltiplos boosts de stepcap, todos em [autofee_service.go:8252-8283](../internal/server/autofee_service.go#L8252-L8283):

- `htlcHotSignal && target > localPpm` → `+HTLCHotStepCapBoost` (0,01-0,03 por profile).
- `outRatio < 0.03` → cap mínimo 0,10.
- `outRatio < 0.05` → cap mínimo 0,07.
- `fwdCount == 0 && outRatio > 0.60` → cap mínimo 0,12.
- `discoveryHit` → `DiscoveryStepCapDown` (0,15-0,25).
- `discoveryHard` → `DiscHarddropCapFrac`.
- `marketRefillMode` → `marketRefillStepCapFrac`.
- Em `globalNegLockApplied` block: `+0,05` no cap.
- `ExtremeDrainStepCap` / `ExtremeDrainTurboStepCap`.

Knobs existentes que **se aproximam mas não cobrem** este caso:

- `StallFloorRelaxGapFrac` ([linha 619/751/883](../internal/server/autofee_service.go#L619)) — relaxa **floor**, não stepcap, e exige stalled rounds.
- `gapBypassFrac` em [linha 5287](../internal/server/autofee_service.go#L5287) — bypass do "hold small move", não cap.
- `ReversalFastTrackGapFrac` em [linha 4875](../internal/server/autofee_service.go#L4875) — confirmação de reversal mais rápida, não cap.
- `AntiFlipStrongGapFrac` em [linha 4925](../internal/server/autofee_service.go#L4925) — extra confirm rounds para anti-flip.

### O que falta

Boost de stepcap dirigido por `target_gap_pct` puro, especialmente útil quando o sistema se reorienta após mudança de regime (mercado lento → mercado rápido ou vice-versa).

### Por que importa

`lnmarkets.com`: gap 44 %, stepcap 10 % → ~6 runs (12h) para fechar. Em "aggressive", isso devia colapsar em 3 runs (~6h). `1sats.com`: gap 62 % → ~9 runs (18h). Em mercado mudando rápido, esse delay vira oportunidade perdida (forwards de competidores) ou rebal desnecessário.

### Esboço técnico

Em [autofee_service.go:8268](../internal/server/autofee_service.go#L8268) (após o block de `outRatio` cases, antes de discovery):

```go
// Boost stepcap quando o gap é grande e a direção é confiável.
// Não aplicar quando reversal está pendente/blocked (evita oscilação)
// nem quando prediction é stable (não há direção a acelerar).
gapPctAbs := math.Abs(calcTargetGapPct(localPpm, target))
if gapPctAbs >= profile.LargeGapStepCapBoostPctMin &&
   !reversalBlocked && !reversalPending &&
   predictionCode != "stable" {
    boost := profile.LargeGapStepCapBoost
    if gapPctAbs >= profile.LargeGapStepCapBoostPctStrong {
        boost = profile.LargeGapStepCapBoostStrong
    }
    capFrac = math.Max(capFrac, e.profile.StepCap+boost)
    tags = append(tags, "large-gap-step-boost")
}
```

Defaults sugeridos por profile:

| Profile | LargeGapStepCapBoostPctMin | Boost | StrongPctMin | StrongBoost |
|---|---:|---:|---:|---:|
| conservative | 30 | +0,02 | 50 | +0,04 |
| moderate | 25 | +0,03 | 45 | +0,06 |
| aggressive | 20 | +0,05 | 40 | +0,10 |

Em "aggressive", gap de 44 % → cap de 0,15 (50 % mais espaço); gap de 62 % → cap de 0,20.

### Critério de aceite

1. Test unit: gap < threshold → cap inalterado; gap ≥ threshold → cap aumenta; gap ≥ strong → boost maior.
2. Test golden: canal com gap 50 % e prediction `bias_down` move passo maior; mesmo canal com `reversal-blocked` mantém cap original.
3. Em prod: tempo até fechar gap > 30 % (medido via `target_gap_pct` ao longo de runs) cai pela metade.

### Tradeoffs

- **Acelera convergência**: principal ganho, especialmente em regime change.
- **Risco de overshoot em sample frágil**: se o `target_raw` está errado (ex.: derivado de seed Amboss desatualizado), passos maiores erram mais. Mitigação: o gate `predictionCode != "stable"` exige consenso entre `bias` e `target`. Reversal logic (já existente) detecta e reverte overshoot na próxima run.
- **Interação com Item 2**: se o floor `rebal` é o que está prendendo o canal acima do target, aumentar stepcap não ajuda — o floor segura. Item 2 desbloqueia esses casos. Implementar 2 antes de 3 amplifica o efeito.

---

## Item 4 (investigação, não feature) — Tuning do PermanentFailScore vs probe interval

**Status:** SEM DEFINIÇÃO — investigação empírica primeiro
**Esforço estimado:** 0,5 dia investigação + tunning iterativo

### Contexto

O sistema **já implementa** [Onda 4.2 do Rebalance Overhaul Plan](#) — `PermanentFailScore` com half-life 7d, threshold 3.0, TTL escalado 1h-6h por step (commit `16c1d32`, 2026-05-02). E os cooldowns clássicos (`targetCooldownMinAttempts=25`, `NoAttempt`/`Failed`/`DistinctSource` thresholds em 3/5/6) também existem.

Apesar disso, em 2026-05-07 observa-se:
- 7 227 attempts/24h com 1,34 % success.
- `route_dead_targets_30m` mostrando peers como BitcoinVN 22 com 26 falhas em 30 min, e mesmo assim continuam recebendo tentativas.
- `last_scan_reasons.target_cooldown_probe: 7` no overview — **probes estão reabilitando peers regularmente**.

### Hipótese a investigar

O loop pode ser:

1. Peer X acumula falhas → `PermanentFailScore` chega a 3.0+ → entra em cooldown.
2. Após `targetCooldownProbeInterval=15min` ou TTL escalado de 1-6h, probe é disparado.
3. Probe falha (peer continua quebrado) → permanece flagged, mas o ciclo se repete.
4. **Cumulativamente**, mesmo com cooldown, cada peer dead consome ~10-30 attempts/hora em probes.

Com 9 dead targets ativos, isso é potencialmente 100-300 attempts/h só de probe — uma fração relevante das 7 227/24h.

### O que medir antes de mudar código

1. Query SQL em `rebalance_attempts` filtrando attempts dos últimos 24h por `job_id` cuja origem é `cooldown-probe` (reason no jobs table). Quantificar o overhead.
2. Distribuição de `PermanentFailScore` no `pair_stats` atual — quantos pares estão acima de 3.0, acima de 10.0, no cap de 20.
3. Para os top-10 dead targets em `route_dead_targets_30m`, verificar trajetória do `permanent_fail_score` ao longo dos últimos 7 dias — ele está crescendo monotonicamente ou oscilando?

### Possíveis ajustes (após medir)

- Aumentar `targetCooldownProbeInterval` quando `PermanentFailScore > 10` (probe menos frequente para casos extremos).
- Reduzir `targetCooldownProbeMaxSources` (atualmente 2) para 1 quando score é alto.
- Adicionar **TTL crescente sem teto** acoplado ao score (atualmente TTL escalado capa em 6h por step).

Não definir solução agora — depende dos números.

### Critério de aceite (uma vez definida a mudança)

- Volume de attempts/24h cai 30-50 % sem queda de success_rate.
- Probes contra peers com `PermanentFailScore > 10` reduzem em ≥ 80 %.

---

## Ordem de implementação sugerida

```
[Investigação 4 — tuning probe]
  ↓ (independente, primeiro pra entender quanto barulho remover)
[Item 1 — HTLC dampening]
  ↓ (limpa sinal autofee)
[Item 2 — confidence floor]
  ↓ (destrava preço onde rebal_ppm7d é frágil)
[Item 3 — stepcap por gap]
  (acelera convergência depois que floor está limpo)
```

Implementar em ondas — não bundle. Cada um precisa de janela de medição em prod (≥ 48h) antes do próximo, para isolar contribuição.

## Pré-requisitos transversais

- **Atualizar `autofee_evaluate_golden_test.go`** para refletir o novo comportamento — modelo a seguir é o approach do commit `5a27115`.
- **Migrações:** Item 1 e 3 não precisam. Item 2 pode precisar persistir success_count em `autofee_outcomes` ou consumir do `rebalance_pair_stats` existente.
- **Telemetria:** todos os itens devem expor um campo de diagnóstico (`htlc_noisy_sample`, `floor_low_confidence_skipped`, `large_gap_step_boost`) no resultado da run para auditar o efeito.
- **Reverter rapidamente:** cada item deve ser gateable por flag de config (similar a `gain_model_version`), ou ao menos por knob numérico que neutralize o efeito (boost=0, threshold infinito).
