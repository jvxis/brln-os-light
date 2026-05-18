# Plano de melhorias pós-merge #21 / #22

> Contexto: PR #21 (UTXO Manager) e PR #22 (Wallet Flow) mergeadas em 2026-05-18 em `jvxis/brln-os-light`. Stack atual de provenance: **local electrs → local bitcoind (txindex) → public Electrum BRLN** (servidores próprios `electrum.pagcoin.org`, `electrum.br-ln.com`).

Cada item abaixo é pequeno o suficiente pra virar uma PR isolada (<300 LOC na maioria) com smoke test mínimo.

---

## Fase 1 — Reorganizar prioridade do source chain

### 1.1 — Bitcoind como source primário

**Mudar a ordem da fallback chain de `electrs → bitcoind → public` para `bitcoind → electrs → public`.**

**Por quê:**
- Bitcoin Core já é o app mais comum (default da loja com `txindex=1` seedado em [apps_bitcoincore.go:429](lightningos-light/internal/server/apps_bitcoincore.go#L429)).
- Rodar bitcoind via RPC HTTP é mais leve que sustentar o daemon electrs em paralelo (electrs consome ~1-2 GB extra de DB + sustenta o próprio processo).
- Reduz superfície de ataque e processos em runtime.
- Mantém compat: quem só tem electrs continua funcionando — só perde 1 hop de health probe.

**Mudança técnica:** em `lightningos-light/internal/server/provenance_sources.go`, função `buildProvenanceSourceChain`:

```go
// Antes
local := &electrs.ClientSource{Client: electrs.New(""), Label: "local electrs"}
sources = append(sources, local)
if bitcoindFallback != nil {
    sources = append(sources, bitcoindFallback)
}

// Depois
if bitcoindFallback != nil {
    sources = append(sources, bitcoindFallback)
    notes = append(notes, "local bitcoind (txindex)")
}
local := &electrs.ClientSource{Client: electrs.New(""), Label: "local electrs"}
sources = append(sources, local)
notes = append(notes, "local electrs @ "+local.Client.Addr())
```

Atualizar `README.md` (seção Wallet Flow) para refletir a nova ordem.

**Esforço:** ~30 min + smoke test.

### 1.2 — Marcar app electrs como `legacy` na loja

**Por quê:** com bitcoind cobrindo o caso de uso da feature, electrs vira app de nicho (nodes pruned + electrs remoto, ou usuários que precisam dele pra Sparrow/etc).

**Como:**
- Adicionar campo `Legacy bool` ao `Definition()` do `appHandler` interface (default `false`).
- Setar `Legacy: true` em `apps_electrs.go`.
- UI ([AppStore.tsx](lightningos-light/ui/src/pages/AppStore.tsx)): renderizar badge "Legacy" no card e ordenar legados no fim da lista.
- Adicionar banner no detalhe: "*Recommended: install Bitcoin Core with `txindex=1` instead — see Wallet Flow docs.*"

**Não remover do código** — manter como opção avançada.

**Esforço:** ~1-2h.

### 1.3 — Opt-in para `PROVENANCE_PRIMARY`

**Por quê:** operadores avançados podem querer forçar uma source específica (ex: `bitcoind` exclusivo, sem fallback público).

**Como:** nova env var `PROVENANCE_PRIMARY=chain|bitcoind|electrs` (default `chain`). Quando setado para `bitcoind` ou `electrs`, a chain vira single-source — a feature some se a source escolhida não está OK, em vez de cair pro próximo nível.

**Esforço:** ~1h.

---

## Fase 2 — Itens deferidos na revisão original

### 2.1 — Cache de leases em `enrichOnchainUtxos`

**Problema:** `ListLeases` + `ListMetadata` + `Prune` rodam a cada `GET /api/onchain/utxos`. O OnchainHub faz auto-refresh a cada 30s. Em wallets grandes (>500 UTXOs) pesa.

**Onde:** [utxo_manager_handlers.go](lightningos-light/internal/server/utxo_manager_handlers.go), função `enrichOnchainUtxos`.

**Como:**
- Cache em memória de `ListLeases` com TTL 10s (similar ao padrão de [bitcoin_status_cache.go](lightningos-light/internal/server/bitcoin_status_cache.go)).
- `Prune` move para ticker em background (intervalo 5-10 min), não no path crítico.

**Métrica de sucesso:** p95 latency de `/api/onchain/utxos` < 200ms em wallet com 1000 UTXOs.

**Esforço:** ~2-3h.

### 2.2 — `provenance.RefreshNow` cancelável

**Problema:** `handleProvenanceRebuild` usa `context.Background()` em goroutine. Restart no meio do rebuild pode deixar `provenance_state` desatualizado.

**Onde:** [provenance_handlers.go](lightningos-light/internal/server/provenance_handlers.go) + estrutura `Server` em [server.go](lightningos-light/internal/server/server.go).

**Como:**
- Expor `s.shutdownCtx context.Context` no struct `Server`, cancelado em `Run()`/`Shutdown()`.
- Derivar contextos de todas goroutines em background a partir dele (RefreshNow, enrichExternal, etc).
- Bonus: identificar outros usos de `context.Background()` em init dos lazy services e migrar.

**Esforço:** ~3h + auditoria.

### 2.3 — Doc da mudança de comportamento do `?limit`

**Onde:** [CLAUDE.md](CLAUDE.md), seção "Adding API Endpoints" ou nota separada.

**O quê:** documentar que `GET /api/onchain/utxos?limit=N` agora processa todo o conjunto antes do slice (custo de enrich + prune é proporcional a N total, não N retornado). Clientes que esperavam short-circuit no limit devem ajustar expectativas.

**Esforço:** ~15 min.

### 2.4 — Bundle UI: code-split do WalletFlowView

**Problema:** `reactflow` (~150kB gzip) + `dagre` + `d3-force` entram no bundle inicial mesmo pra quem não usa Wallet Flow.

**Onde:** [App.tsx](lightningos-light/ui/src/App.tsx) e [OnchainHub.tsx](lightningos-light/ui/src/pages/OnchainHub.tsx).

**Como:** `React.lazy(() => import('./components/WalletFlowView'))` + `<Suspense>` no callsite. Tab só carrega o chunk quando selecionada.

**Ganho esperado:** -150 a -200 kB gzip no initial load.

**Esforço:** ~1h.

### 2.5 — Coinbase sentinel exato

**Onde:** [provenance_service.go](lightningos-light/internal/server/provenance_service.go), `markSpentByInputs` e `walkExternalLineage`.

**Mudança:** trocar `prevVout > 0x7FFFFFFF` por `prevVout == 0xFFFFFFFF`.

**Por quê:** o check atual é over-inclusive (filtra qualquer vout com bit alto setado). Seguro hoje (não cria registros falsos), mas exato é melhor. Microcommit.

**Esforço:** 5 min.

---

## Fase 3 — Endurecimento de segurança e observabilidade

### 3.1 — Audit log estruturado em Postgres

**Problema:** hoje, lock/unlock/bump logam em `s.logger.Printf` (stdout/journal). Sem queryability, sem histórico durável.

**Onde:** novo `internal/server/audit_service.go` + tabela:

```sql
create table audit_events (
  id bigserial primary key,
  ts timestamptz not null default now(),
  session_id text not null,
  action text not null,        -- 'utxo.lock', 'utxo.unlock', 'utxo.bump', etc
  target text,                 -- outpoint, group_id, etc
  metadata jsonb,
  ip text
);
create index audit_events_ts_idx on audit_events (ts desc);
create index audit_events_session_idx on audit_events (session_id, ts desc);
```

Reaproveitar para outras ações sensíveis (channel close, autopilot toggle, wallet send).

**Esforço:** ~4-6h (service + schema + integração nos 3 handlers iniciais + UI mínima de visualização).

### 3.2 — Flag opcional `UTXO_LOCK_REQUIRES_REAUTH`

**Por quê:** lock/unlock são reversíveis e não gastam sats — mas operadores paranoicos podem querer reauth mesmo assim (ex: nodes corporativos).

**Como:** env var ou campo em `config.yaml`. Default `false`. Quando `true`, handlers de lock/unlock passam a exigir `confirm_password` igual ao bump.

**Esforço:** ~1h.

### 3.3 — Telemetria do source chain

**Onde:** novo `internal/server/provenance_metrics.go`.

**O quê:** contadores in-memory (ou via `expvar`):
- `provenance_source_hits{source="electrs"|"bitcoind"|"public"}`
- `provenance_source_errors{source=...}`
- `provenance_source_latency_ms_p95{source=...}`
- `provenance_fallthroughs_total` (quantas vezes a chain caiu de uma source pra próxima)

Expor em `/api/onchain/provenance/metrics` ou anexar ao daily report.

**Por quê:** detectar degradação silenciosa do electrs ou abuso do fallback público.

**Esforço:** ~3h.

### 3.4 — Health check do provenance no daily report

**Onde:** [internal/reports](lightningos-light/internal/reports/).

**O quê:** incluir `provenance.last_sync_age_hours` no relatório diário. Alertar (campo no JSON) se >24h.

**Esforço:** ~1h.

---

## Fase 4 — UX polish (oportunista)

### 4.1 — Tooltip rico no TxNode

Click no nó do WalletFlow → modal com vin/vout completos, fee, vbytes, link `mempool.space` (ou explorer configurado).

### 4.2 — Filtro de tempo no WalletFlow

"Últimos 30d / 90d / sempre". Hoje o gráfico mostra desde a primeira tx. Wallets antigas ficam densas.

### 4.3 — Exportar lineage como SVG/PNG

Botão de download no canvas. Útil pra debug e suporte.

### 4.4 — i18n sweep final

Varredura de strings hardcoded no `WalletFlowView` (filtros, empty states, badges) — algumas devem ter ficado.

---

## Cronograma sugerido

| Sprint | Itens | Estimativa | Bloqueador |
|---|---|---|---|
| **Imediato** | 1.1 (bitcoind-first), 2.5 (sentinel) | 1 dia | — |
| **Sprint 1** | 2.1 (cache), 2.2 (cancel), 2.3 (doc), 2.4 (code-split) | 1 semana | — |
| **Sprint 2** | 1.2 (deprecar electrs UI), 3.1 (audit log) | 1 semana | 1.1 deve estar em produção há ≥1 release |
| **Sprint 3** | 1.3, 3.2, 3.3, 3.4 | 1-2 semanas | — |
| **Quando der** | Fase 4 (polish) | 1 dia por item | — |

---

## Métricas que validam o caminho

- **% de instâncias com bitcoind como source ativa** — alvo > 80% pós-Fase 1.1 (visível via 3.3)
- **p95 latency `/api/onchain/utxos`** — alvo < 200ms pós-Fase 2.1
- **Bundle initial gzip size** — alvo redução de 150kB+ pós-Fase 2.4
- **Taxa de fall-through pra public Electrum** — alvo < 5% (se mais que isso, o bitcoind+electrs local estão falhando muito)
- **`provenance.last_sync_age` no daily report** — alvo < 6h em 95% dos dias

---

## Notas operacionais

- Cada item da Fase 1 e 2 cabe numa PR pequena com smoke test isolado.
- Antes de iniciar Fase 1.1, observar 1 release com a ordem atual em produção pra ter baseline de telemetria.
- Fase 1.2 (legacy electrs) só faz sentido após Fase 1.1 estabilizar — não inverter ordem.
- Fase 3.1 (audit log) é a única que toca em outros features (channel close, autopilot) — coordenar com o roadmap geral antes.
