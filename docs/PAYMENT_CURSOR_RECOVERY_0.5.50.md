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
