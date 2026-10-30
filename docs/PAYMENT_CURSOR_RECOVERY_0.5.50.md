# Payment notification cursor recovery / Recuperação do cursor de pagamentos

Issue: [#356](https://github.com/jvxis/brln-os-light/issues/356).

## English

An outgoing payment can succeed in LND while remaining absent from Notifications
and its Telegram activity mirror if the saved `payments_index` is above the
available LND payment indices. This defect concerns event collection; recovery
does not execute payments, rebalance channels, or modify the LND database.

After an empty successful forward query, the collector can inspect the latest
200 payments. This additional probe is limited to once per five minutes. It
preserves an ordinary up-to-date cursor and does not reset against an empty
history. If the newest available index is below the cursor, a second forward
query checks for concurrent progress before attempting recovery.

The original cursor, recovery timestamp and replay boundary are saved together
with the replacement cursor in one PostgreSQL transaction. A concurrent cursor
change or failed write prevents the rewind. An append-only backup is retained
under a `payments_cursor_recovery:` key; `payments_cursor_recovery` stores the
active historical-alert boundary across Manager restarts.

Recovery reconciles the latest available page and then follows new payments.
This is bounded recovery, not a promise to reconstruct all older missed events.
Deleted LND history cannot be reconstructed by this mechanism. A zero exclusive
offset is possible when the oldest payment in this bounded page has index one;
this is accompanied by the backup and explicit recovery log.

Existing event-key deduplication, rebalance classification, invoice reconciliation
and pending-payment sweeps remain in place. Cursor/query errors do not skip the
independent pending sweep. Failed notification writes retain the forward position
or pending entry for retry. Historical terminal replay is not queued to Telegram.
Payments that were pending and resolve after recovery can still produce their
live completion alert, subject to the existing Telegram settings/startup guards.

The cause of the original index divergence is not established. The correction
does not attribute it to PostgreSQL migration, an upgrade, or fast-path rebalance.

## Português

Um pagamento pode concluir no LND e ficar ausente de Notifications e do seu
espelhamento no Telegram quando `payments_index` está acima dos índices de
pagamentos disponíveis no LND. A recuperação trata da coleta de eventos: não
executa pagamentos, não rebalanceia canais e não altera o banco do LND.

Após uma consulta incremental bem-sucedida e vazia, o coletor pode consultar os
últimos 200 pagamentos. Essa verificação adicional ocorre no máximo uma vez a
cada cinco minutos. Um cursor atualizado permanece como está; histórico vazio
não provoca reset. Quando o maior índice disponível está abaixo do cursor, uma
segunda consulta incremental verifica se houve avanço concorrente.

O cursor original, o horário e o limite de reprocessamento são preservados na
mesma transação PostgreSQL que reposiciona o cursor. Alteração concorrente ou
falha de gravação impede o reposicionamento. Um backup é acrescentado sob uma
chave `payments_cursor_recovery:`; `payments_cursor_recovery` mantém o limite
para alertas históricos mesmo após reiniciar o Manager.

A recuperação reconcilia a página recente disponível e acompanha novos
pagamentos. Não garante reconstruir todos os eventos antigos ausentes. Histórico
apagado do LND não pode ser reconstruído por esse mecanismo. O offset exclusivo
pode ser zero quando o primeiro pagamento da página limitada tem índice um;
nesse caso também há backup e registro explícito da recuperação.

Continuam ativos a deduplicação dos eventos, a classificação dos rebalances,
a reconciliação com invoices e a verificação de pagamentos pendentes. Erros do
cursor/consulta não impedem a verificação independente dos pendentes. Uma falha
ao gravar a notificação mantém a posição ou a pendência para nova tentativa.
Eventos históricos concluídos não entram na fila do Telegram. Um pagamento
pendente que conclui depois da recuperação continua elegível ao alerta de
conclusão, respeitando as configurações e proteções de inicialização existentes.

A origem da divergência ainda não foi estabelecida. A correção não atribui a
causa à migração PostgreSQL, a um upgrade ou ao fast-path de rebalance.

## Validation — 2026-10-30

Platform: disposable Ubuntu 24.04 amd64, native LND 0.21.5-beta and local
Bitcoin Core 31.2 regtest, using an isolated systemd fixture. No production node
was used. Baseline: `1f8435c7`; tested functional source: `10c40c93`.

- Original baseline and final candidate: `go test ./...` and `go vet ./...`
  passed on Linux. Focused cursor, historical Telegram, invoice and rebalance
  tests passed. The new PostgreSQL test was enabled with
  `LOS_PAYMENT_CURSOR_TEST_DSN` pointing to a dedicated
  `los_payment_cursor_test_*` database. Other optional integration suites requiring
  separate DSNs were not enabled.
- The original Manager reproduced the bug: a real fictitious payment succeeded,
  while the synthetic ahead cursor stayed unchanged and its outgoing notification
  remained absent. The candidate recovered that same payment and collected new
  payments without changing LND state.
- Real ordinary circular rebalance and managed fast-path execution both produced
  exactly one settled rebalance notification per successful payment, with amount
  and milllisatoshi fee checked independently against LND. The managed result was
  also checked through the authenticated Notifications API and execution journal.
- Real PostgreSQL tests covered invoice/payment arrival in both orders,
  compare-and-swap rejection, transaction rollback/retry, original-cursor backups,
  actual notification upserts and the Telegram enqueue boundary. Historical
  replay stayed out of that queue, including after recreating the Notifier;
  a pending completion after the recovery boundary remained live and deduplicated.
- A stale `IN_FLIGHT` rebalance plus a late invoice reconciled to one final row
  even while the recovery checkpoint was deliberately unreadable. An injected
  notification-write error held the cursor; removing the error allowed retry of
  the same completed payment without duplication.
- The Manager was stopped after the rewind transaction committed but before
  durable replay. Restart retained the recovery fence and both original-cursor
  backups, completed replay and preserved event counts.
- Final authenticated HTTPS checks passed for all three instances. Native LND
  PIDs, identities, channel sets, on-chain balances, credentials, TLS keys and
  configuration hashes matched the pre-deployment snapshot. Temporary rebalance
  settings were restored and injected SQL triggers removed.

The initial native test used a 55-second observation window, shorter than the
existing 300-second degraded-runtime polling interval. A goroutine capture showed
the collector waiting in its timer; a direct read-only Go RPC probe returned the
pending page correctly. The full scenario was rerun with a 350-second deadline
and passed. Polling/backoff policy was not changed. The circular-payment harness
was also corrected to use the installed native CLI's numeric `scid` and the
REST `outgoing_chan_ids` field, rather than the newer full channel identifier.

No external Telegram bot message was sent; the actual queue boundary and database
behavior were tested without tokens. Native mainnet/other LND versions were not
used as fixtures. Payment execution, rebalance algorithms, invoice cursor logic,
frontend and production installation/upgrade paths are unchanged.

## Validação — 30/10/2026

Os testes foram realizados em Ubuntu 24.04 amd64 descartável, com LND nativo
0.21.5-beta e Bitcoin Core 31.2 local em regtest, sem acessar nodes de produção.
O erro foi reproduzido no Manager original e o mesmo pagamento foi recuperado
pela versão corrigida. Os testes completos do backend e `go vet` passaram.

Foram validados rebalance circular comum, fast-path real, invoices/pagamentos nas
duas ordens, recuperação de `IN_FLIGHT` durante erro de checkpoint, rollback de
transação, falha/repetição de gravação e interrupção depois do reposicionamento
do cursor e antes do reprocessamento. Valores, taxas em milissatoshis e ausência
de duplicação foram conferidos. A fila real do Telegram foi testada sem enviar
mensagens externas: histórico suprimido e conclusão nova de pendência elegível.

A janela inicial de 55 segundos não cobria a cadência degradada de cinco minutos
já existente. A captura mostrou espera no timer; o cenário completo passou ao
observar essa cadência, sem alterá-la. O comando do teste circular também foi
ajustado ao SCID numérico e ao campo REST da versão nativa instalada.

Ao final, as três instâncias passaram pela autenticação HTTPS. Processos LND,
identidades, canais, saldos on-chain, senhas, chaves TLS e configurações foram
preservados; parâmetros temporários foram restaurados e triggers de falha
removidos. Os limites da validação são os mesmos descritos na seção em inglês:
outras integrações opcionais, entrega externa do Telegram e outras versões
nativas/mainnet não foram usadas como prova deste ensaio.
