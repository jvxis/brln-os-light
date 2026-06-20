# LNDG Parity Investigation — 2026-05-08

**Versão:** 0.3.25-Beta
**Origem:** usuários reportaram que ao ligar o LNDG depois de rodar o nosso por horas sem sucesso, rebalances aconteciam imediatamente e a custo baixo. Investigação comparativa identificou diferenças arquiteturais que o doc 25 anterior minimizou.

## A diferença que importa

LNDG faz **uma única chamada SendPaymentV2** ao LND nativo:

```python
SendPaymentV2(
    payment_request=invoice,
    fee_limit_msat=...,
    outgoing_chan_ids=chan_ids,        # ← LISTA com TODAS as sources elegíveis
    last_hop_pubkey=target,
    timeout_seconds=timeout-5,         # default 5min
    allow_self_payment=True,
    max_parts=max_parts                # MPP ligado por default
)
```

O LND nativo então faz **sozinho**: pathfinding sobre todas as sources em paralelo, MPP automático, Mission Control próprio (anos de tuning), retry interno entre sources sem voltar pro código do LNDG. Tudo dentro de uma chamada streaming de até 5 min.

Nós iterávamos **source-por-source** com `BuildRoute(cached)` → fallback `QueryRoutes` → `SendToRoute`, com pair-cache, permanent_fail_score, e 4 cooldowns sobrepostos por target. Cada source vira gRPC call separada, sequencial, com fee_limit calculado por source. Sem paralelismo entre sources.

Para self-payment com `allow_self_payment=true`, o LND nativo tem caminhos otimizados (a Lightning Labs construiu MC e pathfinder otimizando exatamente esse caso). Nosso modelo per-source não captura isso.

## Mudanças aplicadas em 0.3.25-Beta

### Option D — Delegated fast-path

Adicionado flag `delegated_fast_path_enabled` em `RebalanceConfig` (default **false**, opt-in):

- Quando ligado, ANTES do legacy loop por source, monta `outgoing_chan_ids=[all eligible]` e chama `SendPaymentMultiSource(...)` em uma única call.
- Em sucesso: persiste 1 attempt agregada (`payment_hash`, `fee_paid_sat`, source da rota vencedora extraída do primeiro htlc), chama `recordPairSuccess` para a source usada (preserva aprendizado por par), reinforce de MC se `mission_control_reinforce` ligado, finaliza job como `succeeded` com reason `"delegated-fast-path"`.
- Em falha: silent fall-through pro `runLegacyLoop` tradicional. Não envenena pair-cache (a falha é "LND não achou rota agora", não "esta source falhou").
- Bypassa pair-cache (LND tem seu próprio Mission Control). Mantém EligibleAsSource e cooldowns target-level.
- Cooldown-probe jobs **excluídos** do fast-path. Avaliação empírica em 2026-05-09 mostrou hit rate baixo (5 %) e cada falha consumindo 10 min de gRPC contra targets estruturalmente mortos. Em ~7h de janela, gerou 27 timeouts vs 3 sucessos — custo-benefício ruim. Probes voltaram ao loop legado (itera 2 sources, falha rápido). Peers em cooldown voltam naturalmente via auto-scan/auto-restart quando `permanentFailScoreTTLMax` (1h) expira. Histórico desta decisão: temporariamente incluiu cooldown-probe entre 0.3.26 e 0.3.27 antes da remoção definitiva.
- **Timeout do fast-path capado em 120 s** (constante `fastPathMaxTimeoutSec` em [rebalance_service.go](../internal/server/rebalance_service.go)). Antes: passava `RebalanceTimeoutSec=600` para `SendPaymentV2`, e o LND nativo, quando não achava rota, ficava explorando até o timeout — saturando gRPC. Em 2026-05-09 09:18, slice de 200 jobs mostrou 197 falhas (101 timeout + 75 lnd unavailable + 6 db unavailable), cascata de gRPC saturado por fast-paths pendurados. Cap de 120s permite MPP exploration mas evita hangs longos. Em falha, cai no legacy loop normalmente.
- **Sub-context dedicado para fast-path** (mesma deploy 2026-05-09): `runDelegatedFastPath` agora cria um `context.WithTimeout(ctx, timeoutSec+10s)` exclusivo para a chamada `SendPaymentMultiSource`. Antes, o ctx do JOB inteiro (600s) era passado direto, e mesmo com cap de 120s no `timeoutSec`, se LND pendurasse, o `Recv()` bloqueava até o ctx do job estourar — deixando legacy loop sem orçamento. Com sub-ctx, fast-path consome no máximo 130s do orçamento total; legacy loop tem ~470s garantidos para rodar RapidFire, partials e pair-cache learning. Esse era o motivo dos partials terem sumido após fast-path virar default.

Implementação:
- [lndclient/rebalance.go:386-470](../internal/lndclient/rebalance.go) — novo `SendPaymentMultiSource` aceitando `[]uint64` de `outgoing_chan_ids`. Mantém `SendPaymentWithConstraints` como wrapper compat.
- [rebalance_service.go:2804-2950](../internal/server/rebalance_service.go) — método `runDelegatedFastPath` no `rebalanceJobRunner`, plumbado em `run()` antes do `runLegacyLoop`.
- Migrations adicionam coluna `delegated_fast_path_enabled boolean default false` em `rebalance_config`.
- UI ([RebalanceCenter.tsx](../ui/src/pages/RebalanceCenter.tsx)) tem checkbox no painel de Mission Control settings.
- i18n PT-BR + EN com explicação completa do tradeoff.

### Option C — Cooldowns mais frouxos

Constantes ajustadas em [rebalance_service.go:34-53](../internal/server/rebalance_service.go):

| Constante | Antes | Agora | Motivação |
|---|---:|---:|---|
| `pairFailTTLMax` | 1h | **30min** | alinha com AR-WaitPeriod do LNDG |
| `permanentFailScoreSkipThreshold` | 3.0 | **5.0** | exige sinal mais consistente antes de pular |
| `permanentFailScoreTTLMax` | 6h | **1h** | reduz janela de skip mesmo em score alto |

`mc_half_life_sec` continua via config (operador subiu pra 1800s ontem como part dos quick wins).

### Test atualizado

`TestShouldSkipPairForRecentFailureUsesPermanentFailScoreTTL` ajustado pra refletir os novos limites — usa `lastFailAt = -30min` (dentro do novo TTL de 1h) e `-2h` (expirado), em vez do delta de horas anterior.

## Por que NÃO implementamos a Opção A (delegate completo)

- Opção D entrega o ganho de pathfinding nativo SEM perder fallback para o nosso loop.
- Se `delegated_fast_path` falhar muito, o legacy loop ainda roda — temos rede de proteção.
- Se `delegated_fast_path` sucede consistentemente, o fallback é raramente exercido — não há prejuízo.
- Opção A (modo único) ficaria como exercício futuro se medirmos que o legacy loop nunca acrescenta valor após D estar maduro.

## O que medir para validar

Após operadores ligarem `delegated_fast_path_enabled`:

| Métrica | Esperativa pré-D | Meta pós-D |
|---|---:|---:|
| `attempt_success_rate_24h` | 1.5 % | **3-8 %** (paridade com LNDG) |
| `success_attempts_24h` | 100-120 | 300+ |
| `success_amount_24h_sat` | 10-13 M | 30 M+ |
| `effectiveness_7d` | 10 % | 30-50 % |
| `effectiveness_execution_7d` | 3.4 % | 8-12 % |
| `roi_7d` | 1.18 | manter ≥ 1.0 |

Se `roi_7d` cair abaixo de 1.0 após ligar D, é sinal de que o fee_limit calculado está aceitando rotas economicamente ruins — recalibrar `econ_ratio` pra baixo.

## Próximos passos pendentes

Audit date: 2026-06-20.

Done since this investigation:

1. **Telemetria de fast-path**: 24h attempts/successes/fallthroughs/durations are now exposed through the rebalance overview.
2. **Default flip**: `delegated_fast_path_enabled` now defaults to `true` in backend schema/config and in the UI fallback.

Still pending:

1. **A/B test interno**: deixar metade dos operadores no flag-on e metade off por 7 dias, comparar métricas.
2. **Reavaliar Política C, ROI guardrail, cost gate 1.4** — se fast-path sucede, alguns desses filtros podem estar sendo conservadores demais. Após dados, revisar thresholds.
3. **Auto-detect**: se `effectiveness_7d` ficar abaixo de threshold por X horas, sugerir ao operador ligar fast-path (notification + link na UI).
