# Autopilot — AutoTarget (target_outbound_pct adaptativo)

**Snapshot:** 2026-05-22, version 0.4.3-Beta

**Status audit:** 2026-08-29 - implemented; no longer active backlog.

**Implemented 2026-07-09.** See `internal/server/rebalance_auto_target.go`.
Active product work is tracked exclusively in GitHub issues; the design below
is retained only as implementation history.

## v2 (2026-07-10) — dirigido por sell-through, calibrado ao nó

O v1 (limiares absolutos: `success ≥ 0.5`, `drain ≥ 5000`) provou ser uma catraca
de mão única num nó de rota difícil: em prod fez 94 decisões, TODAS DOWN, 0 UP,
achatando 77/97 canais ao piso. Causa: o gate de success era inalcançável (68% das
falhas de rebalance são "no route") e o drain-stall fixo disparava em ~70% dos
canais. Diagnóstico confirmou config íntegro e success rate dentro da faixa
histórica — o problema era só a calibração do AutoTarget.

Correção (v2): trocar `success_rate` por **sell-through** (forward vendido ÷
rebalanceado) como sinal primário, com limiares **relativos ao baseline do nó**
(`autoTargetNodeBaseline`), espelhando `effectiveRebalanceConfig`/native seed.
- **UP** = candidato drenado + `sell_through ≥ baseline × up_factor` (1.1) + revenue
  ≥ min (reason `sells_above_node`); ou drain-first sem histórico. Success vira só
  checagem de rota-morta. Deixa kappa (rota difícil, alto sell-through) subir.
- **DOWN** = só canais que absorveram rebalance (`sent > 0`) e não venderam
  (`sell_through < baseline × down_factor` 0.5) e não faturam (reason `fill_and_hold`).
  Canal sem histórico não é tocado → acaba o achatamento.
- Throttle novo `max_downs_per_cycle` (5). Sell-through por canal via `group by
  target_channel_id` na query de economics. Continua opt-in.

## Desvios do design original (decididos com o operador)

O que foi construído difere deste doc em pontos importantes — este doc fica como
contexto histórico; a implementação é a fonte da verdade:

- **Sem loop separado.** Em vez de `runAutoTargetLoop` horário, a avaliação
  (`evaluateAutoTarget`) roda DENTRO do ciclo do autopilot (`runAutoScan`), sobre
  os candidatos já selecionados da rodada. Um candidato é supply-limited por
  construção (está abaixo do target e drenando), o que elimina de graça o erro
  de jun/2026 (subir target de canal demand-limited).
- **Teto 50%** (`auto_target_max_pct` default 50), não 70.
- **Consciente de capacidade.** `auto_target_max_local_sat` (cap absoluto de
  liquidez local) encolhe o teto efetivo % em canais grandes, para não setar
  target absoluto desproporcional. Mais throttle de UPs por ciclo
  (`auto_target_max_ups_per_cycle`) para não estourar budget.
- **Override manual = re-decide no próximo tick** (sem hold).
- **Sem auto-exclusão por target alto**; opt-out per-channel via
  `auto_target_managed` (default true).
- Toda decisão up/down (e UP throttled) grava em `rebalance_auto_target_history`
  (retenção 90d); holds simples não gravam (mantém a tabela enxuta).
**Origem:** sessão de tuning do sovereign autopilot. Evidência empírica veio do bump manual de target_outbound_pct em 8 canais (Apr 23 → confirmado em 2026-05-22):
- LQWD-France (15→40): 2/2 sucessos, +100% rate
- WoS (20→45): 1 partial em 1.5h pós-bump
- hqq (20→45), Nodelou (15→40): 0/1 cada, drain rate baixo demais para justificar bump

**Conclusão:** ajuste de target_outbound_pct funciona quando calibrado pra perfil real do canal, mas calibrar manualmente 40+ canais não escala. AutoTarget seria o mecanismo automático.

## Conceito

AutoTarget : AutoFee :: target_outbound_pct : fee_rate_ppm.

Mesmo padrão arquitetural do AutoFee:
- Operator define limites (min/max/step)
- Sistema avalia periodicamente cada canal
- Decisão baseada em sinais observados
- Per-channel opt-out flag

Default: OFF (opt-in via config UI).

## Sinais e decisão

### Trigger UP (subir target_outbound_pct +step)

**Caminho A — canal com histórico de rebalance** (success_rate disponível):
1. `drain_rate_24h ≥ auto_target_min_drain_rate` (sat/h) — canal vende ativamente
2. `success_rate_recent ≥ auto_target_up_success_threshold` (default 0.5) — rota viável
3. `revenue_7d_sat ≥ auto_target_min_revenue` — vale alocar capital
4. **Frequency check**: canal esgotou (local_pct < target) ≥ X vezes nas últimas 24h — sinal de subdimensionamento

**Caminho B — drain-first UP (canal sem histórico de rebalance)**:

Necessário porque canais "vendendo bem mas nunca selecionados pelo
autopilot" não acumulam pair_stats — `success_rate_recent` fica vazio e
o Caminho A nunca dispara. Esses são exatamente os canais que mais
precisam de bump no target (estão drenando mas com deficit pequeno
demais pra atrair o sovereign autopilot).

Condições combinadas (TODAS):
1. `rebalance_attempts_7d == 0` — canal nunca foi target de rebalance
2. `drain_rate_24h ≥ auto_target_min_drain_rate × auto_target_drain_first_multiplier`
   (default multiplier=3 — exige drain bem acima do floor pra ter
   certeza que vale apostar capital sem histórico de rota)
3. `revenue_7d_sat ≥ auto_target_min_revenue × 2` — receita robusta o suficiente
   pra justificar locking de capital sem provar a rota antes

Rationale: 3× drain + 2× revenue funcionam como prior forte de
"canal real e ativo", substituindo a confiança que viria de
success_rate_recent.

### Trigger DOWN (descer target_outbound_pct -step)

Condições alternativas (qualquer uma):
1. `drain_rate < auto_target_min_drain_rate / 4` por 24h+ → canal parou de vender
2. `success_rate_recent < auto_target_down_success_threshold` (default 0.25) → rotas difíceis
3. ≥2 `target_structural_cooldown` events no canal nas últimas 24h → forçando demais
4. `time_to_payback > attribution_window × 2` → liquidez não rende no horizonte normal

### Anti-oscilação

- **Hysteresis**: thresholds UP (≥50%) > DOWN (<25%) — gap de 25pp evita flapping
- **Cooldown**: max 1 mudança por canal por `auto_target_eval_interval_hours` (default 6h)
- **Step pequeno**: `auto_target_step_pct` default 5pp
- **Cap absoluto**: `auto_target_max_pct=70`, `auto_target_min_pct=10`
- **Boundary respect**: nunca cruza limites set manualmente via override

## Schema proposto

### RebalanceConfig (novos campos)

```go
AutoTargetEnabled               bool    `json:"auto_target_enabled"`               // default false
AutoTargetMaxPct                int     `json:"auto_target_max_pct"`               // default 70
AutoTargetMinPct                int     `json:"auto_target_min_pct"`               // default 10
AutoTargetStepPct               int     `json:"auto_target_step_pct"`              // default 5
AutoTargetEvalIntervalHours     int     `json:"auto_target_eval_interval_hours"`   // default 6
AutoTargetMinDrainRateSatPerHr  int64   `json:"auto_target_min_drain_rate_sat_per_hr"` // default 5000
AutoTargetMinRevenue7dSat       int64   `json:"auto_target_min_revenue_7d_sat"`    // default 500
AutoTargetUpSuccessThreshold    float64 `json:"auto_target_up_success_threshold"`  // default 0.5
AutoTargetDownSuccessThreshold  float64 `json:"auto_target_down_success_threshold"` // default 0.25
AutoTargetDrainFirstMultiplier  float64 `json:"auto_target_drain_first_multiplier"` // default 3.0 — Caminho B (drain-first UP)
```

### channelSetting (per-channel)

```go
AutoTargetManaged bool  `json:"auto_target_managed"` // default true quando feature ON
```

Operator marca `false` em canais que quer mexer manualmente (ex: canais com swap providers onde target=99% é estratégia, não vai querer o sistema descer).

### Nova tabela `rebalance_auto_target_history`

```sql
create table rebalance_auto_target_history (
  id bigserial primary key,
  channel_id bigint not null,
  channel_point text,
  decided_at timestamptz not null default now(),
  prev_target_pct integer,
  new_target_pct integer,
  direction text, -- 'up'|'down'|'noop'
  trigger_signals jsonb, -- {drain_rate, success_rate, revenue_7d, structural_fails, ...}
  measurement_window_hours integer
);
create index on rebalance_auto_target_history(channel_id, decided_at desc);
```

## Pseudo-código do loop

```go
func (s *RebalanceService) runAutoTargetLoop(ctx context.Context) {
    ticker := time.NewTicker(time.Hour) // tick horário, decide baseado em eval_interval
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            cfg, _ := s.loadConfig(ctx)
            if !cfg.AutoTargetEnabled { continue }
            channels := s.snapshotChannels(ctx)
            for _, ch := range channels {
                if !ch.AutoTargetManaged { continue }
                if !ch.EligibleAsTarget && !ch.EligibleAsManualTarget { continue }
                lastChange := s.lastAutoTargetChange(ch.ChannelID)
                if time.Since(lastChange) < time.Duration(cfg.AutoTargetEvalIntervalHours) * time.Hour {
                    continue
                }
                signals := s.computeAutoTargetSignals(ctx, ch)
                decision := decideAutoTargetAdjustment(ch, signals, cfg)
                if decision.Direction == "noop" { continue }
                newTarget := clamp(ch.TargetOutboundPct + decision.Delta, cfg.AutoTargetMinPct, cfg.AutoTargetMaxPct)
                if newTarget == ch.TargetOutboundPct { continue }
                _ = s.UpdateChannelTargetSettings(ctx, ch.ChannelID, ch.ChannelPoint, &newTarget, nil, nil, nil)
                s.persistAutoTargetHistory(ctx, ch.ChannelID, ch.TargetOutboundPct, newTarget, decision, signals)
            }
        }
    }
}
```

## API/UI

### Endpoint novo: `GET /api/rebalance/auto-target/history`
Query params: `channel_id` (opcional), `limit` (default 100), `since` (RFC3339).

Response:
```json
{
  "items": [
    {
      "channel_id": 1005485791446892545,
      "decided_at": "2026-05-22T15:00:00Z",
      "prev_target_pct": 45,
      "new_target_pct": 50,
      "direction": "up",
      "trigger_signals": {
        "drain_rate_sat_per_hr": 106743,
        "success_rate_recent": 0.67,
        "revenue_7d_sat": 9337,
        "structural_fails_24h": 0
      }
    }
  ]
}
```

### UI no Rebalance Center config

Novo card "AutoTarget" abaixo de "AutoFee":
- Toggle "Enable auto target adjustment" (vinculado a `auto_target_enabled`)
- Inputs: `max_pct`, `min_pct`, `step_pct`, `eval_interval_hours`
- Inputs avançados (collapsible): thresholds de drain/revenue/success
- Activity panel: timeline das mudanças recentes (consome `/auto-target/history`)

Por canal (em `ChannelSettings.tsx` ou `RebalanceCenter.tsx` drawer):
- Toggle "Auto target managed" — exclui canal do sistema

## Esforço estimado

| Item | Esforço |
|---|---|
| Config struct + default + clamp + migration | 2h |
| Handler payload + validator + applyPayload | 1h |
| Service loop + decision logic + signals | 6h |
| Per-channel setting (migration + load/save) | 2h |
| History persistence + endpoint | 3h |
| Testes (golden + hysteresis + clamp + signal calc) | 4h |
| UI config card + activity panel + i18n EN/PT-BR | 8h |
| **Total** | **~3 dias focado** |

## Pré-requisitos / validações

Antes de implementar, validar empiricamente:
1. **Calibrar thresholds com dados reais**: os defaults sugeridos (drain 5k sat/h, revenue 500 sat/7d, success 50%) precisam de tuning baseado no histórico de 14-30 dias. Cruzar com `channel-ranking` para validar quais canais "atualmente certos" passam pelos filtros.
2. **Confirmar com operador quais canais devem ser excluídos por padrão** (swap providers tipicamente em 99% local — não querer o sistema descer eles).
3. **Decidir interação com manual override**: se operador setar manualmente via UI/API, AutoTarget pausa esse canal por X horas? Ou ignora e re-decide no próximo tick?

## Riscos conhecidos

1. **Oscilação**: hysteresis ajuda mas não elimina. Adicionar telemetria de "número de flips no canal por semana" — se >2, indica thresholds mal calibrados.
2. **Interação com AutoFee**: AutoFee já ajusta fees. Se ambos rodando, target↑ + fee↑ pode amplificar pressão de rebalance. Sugestão: nunca rodar AutoTarget durante `autofee_settling_window` (já existe gate).
3. **Custo do "experimento"**: subir target = mais sats em rebalance. Se a tese estiver errada, o sistema descobre via failure rate, mas custou X sats. Limitar via cap `auto_target_max_pct` e step pequeno.
4. **Pares com swap providers**: comportamento atípico — ver "Pré-requisito 2".

## Evidência empírica que motiva (Maio/2026)

Bump manual de 8 canais em 2026-05-22 às ~09:25 UTC. Resultados nas 1.5h seguintes:

| Canal | bump | drain_rate | rate post-bump | conclusão |
|---|---|---|---|---|
| LQWD-France | 15→40 | 58k/h | 2/2 = 100% | ✅ candidato a AutoTarget UP |
| WoS | 20→45 | 106k/h | 1 partial/1 | ✅ candidato a AutoTarget UP |
| RA-KO | 20→45 | 42k/h | 1 partial/1 | ✅ candidato a AutoTarget UP |
| Nodelou | 15→40 | 146k/h | 0/1 fail | ❌ drain alto mas rota difícil — AutoTarget DOWN apropriado |
| hqq | 20→45 | 39k/h | 0/1 fail | ❌ idem |
| Authenticity, Open_Hand, Don Quixote | bumped | — | sem jobs ainda | em cooldown |

AutoTarget calibrado teria subido LQWD/WoS/RA-KO e mantido hqq/Nodelou. Resultado real: a heurística baseada apenas em drain rate (que motivou os bumps manuais) NÃO foi suficiente — `success_rate_recent` é sinal complementar essencial.

## Sequenciamento

- **Imediato (hoje):** este backlog doc + memory pointer
- **Após estabilização (7-10 dias):** revisar dados reais do impacto dos bumps manuais (LQWD vs hqq), calibrar thresholds com dados
- **Implementação:** quando o operador priorizar — feature é opt-in, sem urgência
