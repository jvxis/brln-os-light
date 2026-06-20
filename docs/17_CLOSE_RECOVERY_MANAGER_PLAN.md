# Close Recovery Manager Plan

## Status

Implemented.

Audit date: 2026-06-20.

The current codebase has `CloseManagerService`, persisted close sessions/events,
status/list/detail/event APIs, recover/force/bump actions, and UI integration in
`LightningOps`. Keep the rest of this document as design history unless a new
gap is added explicitly.

Este documento define a proposta para introduzir um `Close Recovery Manager` no LightningOS. O objetivo e consolidar em uma engine unica toda a logica operacional de fechamento de canais, recovery de `waiting_close`, acompanhamento de HTLCs pendentes, monitoring de outputs timelocked, sweeps pendentes e fee bump de recovery.

## Motivacao

Hoje o projeto ja possui partes importantes do fluxo:

- Fechamento cooperativo/forcado via `POST /api/lnops/channel/close`.
- Monitoramento de `pending channels` e `waiting_close_recovery`.
- Cards de `pending close` e historico de `closed channels` em `LightningOps`.
- Logica de HTLC stuck, `wait` vs `force_close` e retries em `NodeRetirement`.

O problema e que a inteligencia operacional ainda esta fragmentada:

- `LightningOps` mostra partes do estado, mas nao consolida o lifecycle inteiro.
- `NodeRetirement` possui regras de fechamento que nao deveriam existir isoladas.
- Recovery de `waiting_close` existe, mas so cobre um caso especifico.
- Nao ha uma entidade persistida que represente "este canal esta em processo de fechamento e recovery".

O resultado e uma experiencia dificil para o operador em cenarios reais:

- coop close bloqueado por HTLCs por horas ou dias;
- `waiting_close` sem `closing_txid`;
- force close com outputs em limbo;
- sweep atrasado em mempool caro;
- dificuldade de explicar "qual e o proximo passo correto".

## Objetivos

- Centralizar a logica operacional de fechamento em uma engine unica.
- Tratar explicitamente os casos com HTLC pendente e stuck HTLC.
- Consolidar estado, historico de eventos e acao recomendada por canal.
- Reutilizar a mesma engine em `LightningOps` e `NodeRetirement`.
- Permitir fee bump seguro apenas em fluxos suportados pelo sweeper do LND.
- Melhorar observabilidade sem aumentar risco operacional.

## Nao objetivos

- Nao substituir o LND como fonte de verdade do estado de fechamento.
- Nao automatizar `force close` generico sem aprovacao explicita ou politica configurada.
- Nao implementar gerenciamento manual arbitrario de transacoes fora do sweeper do LND.
- Nao transformar `LightningOps` em um wizard pesado para operacoes simples.

## Comportamento atual relevante

### Fechamento normal

- O backend chama `lnd.CloseChannel(...)`.
- O client aguarda updates do stream de close ate observar `closing_txid`, `chan_close` ou encerramento do stream.
- No close cooperativo, isso equivale a seguir o comportamento nativo do LND: se houver HTLCs pendentes, a tentativa pode permanecer aguardando indefinidamente.

### Recovery de `waiting_close`

- O notifier ja detecta `waiting_close` sem `closing_txid`.
- Quando ha `closing_tx_hex`, ele tenta republicar a tx.
- O estado basico de recovery (`attempts`, `last_result`, `last_error`, `last_recovered_txid`) ja e exposto em `LightningOps`.

### Node Retirement

- O runtime ja rastreia `pending_htlc_count`, `pending_htlc_first_seen_at` e `pending_htlc_age_sec`.
- O runtime tambem decide `wait` vs `force_close`, suporta preapproval e reexecuta force close quando necessario.

Esses tres comportamentos devem ser preservados, mas movidos para uma camada unica de orquestracao.

## Proposta: Close Recovery Manager

Criar um servico central chamado `CloseManagerService` no backend, com as seguintes responsabilidades:

- detectar sessoes de fechamento ativas;
- consolidar o estado operacional por `channel_point`;
- persistir timeline de eventos e diagnosticos;
- calcular a proxima acao recomendada;
- executar acoes seguras de recovery;
- expor API para `LightningOps` e `NodeRetirement`.

### Fontes de verdade

O manager nao inventa estado. Ele deriva tudo a partir de:

- `ListChannels`
- `ListPendingChannels`
- `ClosedChannels`
- `PendingSweeps`
- `ListSweeps`
- fee hints (`/api/mempool/fees`)
- notificacoes/eventos internos do app

### Acoes suportadas

- `recover_waiting_close`
- `force_close`
- `bump_fee`
- `refresh_now`

### Acoes deliberadamente fora de escopo

- construir sweep tx manual fora do LND;
- publicar tx arbitraria escolhida pelo usuario;
- trocar automaticamente coop por force close sem politica explicita;
- bump fee em tx nao registrada no sweeper do LND.

## Maquina de estados

Cada canal em fechamento sera representado por uma `close_session`.

Estados propostos:

1. `coop_requested`
2. `coop_blocked_by_htlcs`
3. `waiting_close_no_txid`
4. `closing_tx_seen_unconfirmed`
5. `force_close_requested`
6. `force_close_active`
7. `outputs_timelocked`
8. `sweep_pending`
9. `sweep_stuck`
10. `funds_recovered`
11. `closed_terminal`
12. `failed_manual_attention`

### Semantica dos estados

- `coop_requested`
  - houve tentativa de coop close;
  - ainda nao ha indicacao clara de bloqueio por HTLC nem `closing_txid`.

- `coop_blocked_by_htlcs`
  - o canal continua aberto ou em close cooperativo aguardando;
  - existem HTLCs pendentes;
  - a acao recomendada normalmente e aguardar.

- `waiting_close_no_txid`
  - o LND marcou `waiting_close`, mas ainda nao existe `closing_txid`;
  - o manager tenta recovery quando houver `closing_tx_hex`.

- `closing_tx_seen_unconfirmed`
  - a closing tx existe, mas ainda nao confirmou.

- `force_close_requested`
  - o operador ou uma politica aprovada pediu force close;
  - aguardando reflexo no estado do LND.

- `force_close_active`
  - force close entrou em andamento;
  - pode haver outputs timelocked e anchor handling.

- `outputs_timelocked`
  - existem outputs recuperaveis no futuro, mas ainda nao maduros;
  - a principal UX aqui e mostrar blocos restantes e ETA.

- `sweep_pending`
  - ha sweep tx ou output registrado no sweeper aguardando confirmacao.

- `sweep_stuck`
  - o sweep segue pendente com fee aparentemente inadequada para o mempool atual;
  - candidato a `bump_fee`.

- `funds_recovered`
  - todos os outputs relevantes confirmaram e os fundos ja sairam do limbo.

- `closed_terminal`
  - close concluido sem necessidade de recovery adicional.

- `failed_manual_attention`
  - recovery automatico nao consegue progredir;
  - a proxima acao depende do operador.

## Regras de transicao

### Regras de HTLC pendente

- Se ha tentativa de coop close e `pending_htlc_count > 0`, transitar para `coop_blocked_by_htlcs`.
- Persistir:
  - `pending_htlc_count`
  - `pending_htlc_first_seen_at`
  - `pending_htlc_age_sec`
  - `max_pending_htlc_age_sec` opcional se houver granularidade futura
- Se `pending_htlc_count` cair para zero:
  - voltar para `coop_requested` ou avancar para `closing_tx_seen_unconfirmed`, conforme o estado do LND.
- Se a politica da sessao permitir force close por HTLC stuck:
  - quando `pending_htlc_age_sec >= threshold`, recomendar ou executar `force_close`.

### Regras de `waiting_close`

- Se `status == waiting_close` e nao ha `closing_txid`, transitar para `waiting_close_no_txid`.
- Se houver `closing_tx_hex`, tentar `recover_waiting_close`.
- Se a tx reaparecer, avancar para `closing_tx_seen_unconfirmed`.
- Se apos N tentativas e T tempo nao houver progresso e nao houver `closing_tx_hex`, marcar `failed_manual_attention`.

### Regras de force close

- Quando o force close for pedido, criar evento e mover para `force_close_requested`.
- Quando `ListPendingChannels` refletir `force_closing`, mover para `force_close_active`.
- Se surgirem outputs com maturidade futura, mover para `outputs_timelocked`.
- Quando os outputs forem registrados no sweeper e houver tx de sweep, mover para `sweep_pending`.

### Regras de sweep

- Se o sweep estiver pendente e dentro de parametros aceitaveis, manter `sweep_pending`.
- Se o sweep estiver parado alem do threshold configurado e com fee materially abaixo do alvo, mover para `sweep_stuck`.
- Quando todos os outputs relevantes forem recuperados, mover para `funds_recovered`.

## Modelo de dados

### Tabela `close_sessions`

Proposta inicial:

```sql
create table if not exists close_sessions (
  id bigserial primary key,
  channel_point text not null unique,
  channel_id bigint,
  peer_pubkey text,
  peer_alias text,
  source text not null default 'lightning_ops',
  source_ref text not null default '',
  state text not null,
  action_required text not null default '',
  action_recommended text not null default '',
  decision text not null default '',
  risk_level text not null default 'info',
  close_mode text not null default '',
  close_txid text not null default '',
  close_tx_hex_available boolean not null default false,
  sweep_txid text not null default '',
  limbo_balance_sat bigint not null default 0,
  pending_htlc_count integer not null default 0,
  pending_htlc_first_seen_at timestamptz,
  pending_htlc_age_sec bigint not null default 0,
  blocks_til_maturity integer,
  maturity_eta_at timestamptz,
  sweep_fee_rate_sat_vb bigint not null default 0,
  mempool_target_sat_vb bigint not null default 0,
  last_error text not null default '',
  last_progress_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  closed_at timestamptz
);
```

### Tabela `close_events`

```sql
create table if not exists close_events (
  id bigserial primary key,
  session_id bigint not null references close_sessions(id) on delete cascade,
  event_type text not null,
  severity text not null default 'info',
  payload jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);
```

### Tabela `close_outputs`

Opcional para fase 2 ou 3:

```sql
create table if not exists close_outputs (
  id bigserial primary key,
  session_id bigint not null references close_sessions(id) on delete cascade,
  outpoint text not null,
  output_type text not null default '',
  maturity_height integer,
  blocks_til_maturity integer,
  amount_sat bigint not null default 0,
  sweep_txid text not null default '',
  sweep_state text not null default '',
  last_fee_rate_sat_vb bigint not null default 0,
  updated_at timestamptz not null default now(),
  unique (session_id, outpoint)
);
```

## API proposta

### Overview

- `GET /api/lnops/close-manager/status`
  - resumo agregado: contagens por estado, stuck HTLC, sweep stuck, sessoes com acao manual.

- `GET /api/lnops/close-manager/sessions`
  - lista de sessoes ativas e recentes;
  - filtros por estado, risco, source e peer.

- `GET /api/lnops/close-manager/sessions/{id}`
  - detalhe completo da sessao.

- `GET /api/lnops/close-manager/sessions/{id}/events`
  - timeline operacional da sessao.

- `POST /api/lnops/close-manager/sessions/{id}/recover`
  - tenta `recover_waiting_close`.

- `POST /api/lnops/close-manager/sessions/{id}/force-close`
  - executa force close para a sessao.

- `POST /api/lnops/close-manager/sessions/{id}/bump-fee`
  - pede bump fee ao sweeper do LND.

### Payload de `bump-fee`

```json
{
  "mode": "economico",
  "sat_per_vbyte": 0,
  "force": false
}
```

Regras:

- `mode` pode ser `economico`, `normal` ou `urgente`;
- `sat_per_vbyte` explicito e opcional;
- o backend valida se o output e elegivel para bump;
- o backend registra evento com fee anterior, nova fee e motivo.

## UI proposta

### Navegacao

Adicionar subarea dentro de `Lightning Ops`:

- `Channels`
- `Close Recovery`

`Close Recovery` pode:

- aparecer sempre como aba secundaria; ou
- aparecer somente quando houver sessoes ativas, com badge.

Preferencia inicial: sempre visivel, com badge. Isso evita estados inconsistentes de navegacao e melhora discoverability.

### LightningOps (cards atuais)

Manter o card basico de canais com as informacoes atuais.

Acrescentar apenas:

- badge `close/recovery ativo`;
- `pending_htlc_count` quando relevante;
- link `Abrir Close Recovery`.

Isso preserva a leveza do `LightningOps` como area de operacao rapida.

### Close Recovery

Agrupamentos recomendados:

- `Aguardando coop`
- `Bloqueado por HTLC`
- `Waiting close`
- `Force close / maturidade`
- `Sweep pendente`
- `Sweep possivelmente travado`

Cada item deve mostrar:

- peer alias / pubkey
- channel point
- estado atual
- close mode
- HTLCs pendentes e idade
- closing tx
- sweep tx(s)
- limbo balance
- blocks to maturity
- ultima acao automatica
- proxima acao recomendada
- justificativa curta

### Linha de diagnostico

Cada card de recovery deve conter tres linhas fixas:

- `Diagnostico`
- `Proxima acao`
- `Motivo`

Exemplos:

- `Diagnostico: waiting_close sem closing txid ha 34 min`
- `Proxima acao: continuar monitorando`
- `Motivo: ainda existe chance de rebroadcast automatico com closing_tx_hex`

ou:

- `Diagnostico: sweep pendente ha 11 blocos`
- `Proxima acao: bump fee`
- `Motivo: fee observada abaixo do alvo do mempool para confirmacao rapida`

## Integracao com Node Retirement

O `NodeRetirement` deve se tornar um consumidor do `CloseManagerService`, e nao um segundo implementador da logica de closing.

### Reuso esperado

- Criacao de `close_sessions` com `source = 'node_retirement'`.
- Reuso da deteccao de HTLC pending/stuck.
- Reuso da decisao `wait` vs `force_close`.
- Reuso da timeline de eventos.
- Reuso do monitoring de close e sweep.

### O que permanece no Node Retirement

- definicao da sessao de aposentadoria;
- captura inicial do conjunto de canais;
- politicas do processo:
  - `preapprove_fc_offline`
  - `preapprove_fc_stuck_htlc`
  - `stuck_htlc_threshold_sec`
  - `sweep_min_confs`
  - `sweep_sat_per_vbyte`
- reconciliacao final da aposentadoria;
- transferencia final on-chain para destino do sucessor ou carteira alvo.

### O que sai do Node Retirement ao longo do tempo

- detalhamento de estados de close por canal;
- retries especificos de close/recovery;
- parte do tracking operacional de HTLC stuck;
- parte do tracking de force close retry.

Em resumo:

- `NodeRetirement` vira orquestrador de politica e processo;
- `CloseManagerService` vira engine de lifecycle de fechamento.

## Integracao com notificacoes

O manager deve aproveitar o notifier atual para:

- semear sessoes quando surgir `closing`, `force_closing` ou `waiting_close`;
- registrar eventos de rebroadcast e recovery;
- notificar transicoes importantes:
  - `htlc_stuck_detected`
  - `waiting_close_recover_attempted`
  - `waiting_close_recovered`
  - `force_close_started`
  - `sweep_stuck_detected`
  - `funds_recovered`

## Fee bump: proposta operacional

O fee bump deve ser conservador.

### Principios

- oferecer bump apenas para outpoints/txs elegiveis no sweeper do LND;
- nunca tentar bump em tx confirmada;
- nunca gastar reserva operacional minima do node sem aviso explicito;
- registrar auditoria de cada bump.

### Presets

- `economico`
- `normal`
- `urgente`

Cada preset define:

- alvo de confirmacao;
- tolerancia de custo;
- limite maximo de fee rate.

### Heuristica de `sweep_stuck`

Threshold inicial recomendado:

- sweep pendente por mais de `X` blocos ou `Y` minutos;
- fee atual materialmente abaixo da recomendacao mempool;
- output ainda economicamente vale a pena ser recuperado com bump.

Os thresholds exatos podem ser calibrados depois, mas devem ficar em config.

## Implementacao incremental

### Fase 1: consolidacao read-only

Entregas:

- servico `CloseManagerService` com polling;
- schema `close_sessions` + `close_events`;
- resumo e detalhe por sessao;
- subview `Close Recovery` somente leitura;
- badges nos cards atuais de `LightningOps`.

Sem:

- force close novo;
- bump fee;
- automacoes novas alem das ja existentes.

### Fase 2: recovery operacional

Entregas:

- endpoint de `recover_waiting_close`;
- endpoint de `force_close`;
- migracao do tracking de HTLC stuck do `NodeRetirement` para a engine;
- recomendacoes claras `wait` vs `force_close`.

### Fase 3: sweep tracking

Entregas:

- integracao com `PendingSweeps` e `ListSweeps`;
- tabela `close_outputs` se necessario;
- estados `outputs_timelocked`, `sweep_pending`, `sweep_stuck`;
- detalhes de maturity e ETA.

### Fase 4: fee bump

Entregas:

- endpoint `bump-fee`;
- presets de fee bump;
- auditoria de bumps;
- opcionalmente opt-in para autopilot de bump em sessoes criticas.

## Impacto em arquivos atuais

Arquivos provavelmente afetados:

- `lightningos-light/internal/server/routes.go`
- `lightningos-light/internal/server/handlers.go`
- `lightningos-light/internal/server/notifications.go`
- `lightningos-light/internal/server/node_retirement_runtime.go`
- `lightningos-light/internal/server/*close_manager*.go` novos
- `lightningos-light/ui/src/api.ts`
- `lightningos-light/ui/src/pages/LightningOps.tsx`
- `lightningos-light/ui/src/pages/NodeRetirement.tsx`
- `lightningos-light/ui/src/i18n/en.json`
- `lightningos-light/ui/src/i18n/pt-BR.json`

## Estrategia de testes

### Backend

- close cooperativo com HTLC pendente que drena depois;
- close cooperativo que fica em `waiting_close` sem `closing_txid`;
- recovery com `closing_tx_hex`;
- force close com outputs timelocked;
- sweep pendente que evolui para `sweep_stuck`;
- bump fee em caso elegivel;
- bump fee rejeitado em caso inelegivel;
- `NodeRetirement` criando sessoes com `source = node_retirement`.

### Frontend

- render de grupos de sessoes por estado;
- badges de recovery no `LightningOps`;
- transicoes de CTA `wait`, `force close`, `recover`, `bump fee`;
- mensagens localizadas e fallback de erro.

## Riscos e mitigacoes

### Risco: duplicacao de logica entre Node Retirement e Close Manager

Mitigacao:

- mover primeiro o tracking de estado e recomendacao para a engine;
- depois reduzir gradualmente a logica do `NodeRetirement`.

### Risco: heuristica errada para `sweep_stuck`

Mitigacao:

- iniciar com modo somente leitura;
- expor claramente fee observada vs alvo;
- tornar thresholds configuraveis.

### Risco: fee bump agressivo demais

Mitigacao:

- presets conservadores;
- validacao no backend;
- auditoria completa;
- sem automacao por padrao.

### Risco: UI pesada dentro de LightningOps

Mitigacao:

- manter os cards de canal leves;
- concentrar a analise detalhada na subarea `Close Recovery`.

## Ordem recomendada de execucao

1. Introduzir schema e servico read-only.
2. Criar APIs de listagem/detalhe.
3. Criar subview `Close Recovery`.
4. Integrar badges e links no `LightningOps`.
5. Integrar `NodeRetirement` como criador/consumidor de sessoes.
6. Adicionar tracking de sweeps.
7. Adicionar fee bump.

## Resultado esperado

Ao final, o operador deixa de ver "pedacos soltos" do fechamento e passa a ver um lifecycle unico por canal:

- o que esta acontecendo;
- por que o canal esta parado;
- quanto tempo esta assim;
- qual e o risco;
- qual e a proxima acao correta.

Essa mesma engine passa a atender tanto o fechamento manual em `LightningOps` quanto o fechamento orquestrado de `NodeRetirement`, reduzindo duplicacao e tornando o comportamento do produto mais previsivel.
