# Retenção própria do histórico de policies (0.5.48)

## Por quê

O histórico de fee de um canal (aba de detalhe, `fee_logs`) é lido da tabela `graph_channel_policy_history`, a mesma que guarda as policies da rede inteira para o Graph Explorer. Essa tabela cresce ~150 MB por dia no Friendspool, então a retenção do grafo é curta (30 dias; 7 no BRLN HUB) e, com ela, some o histórico de fee do próprio nó. Em 20/10/2026 o Friendspool só tinha policies próprias desde 20/09: a relação fee × volume de janeiro a agosto, necessária para avaliar a política de preço com pelo menos um mês de dados, não existia mais em lugar nenhum.

As linhas que este nó anuncia são algumas centenas por dia contra milhões do resto do grafo. Guardá-las por um ano custa megabytes, não gigabytes.

## O que muda

- `graph_explorer_config.own_history_retention_days` (padrão **365**, mínimo = retenção geral, máximo 730). Exposto em `GET/POST /api/lnops/graph-explorer/storage` como `own_history_retention_days` e no card **Armazenamento do grafo** (campo "Retenção das policies deste nó").
- As duas podas da tabela (a do próprio Graph Explorer, a cada 6 h, e a rotina de manutenção do Postgres) passam a usar o mesmo predicado: `captured_at < corte_geral and (advertising_pubkey <> <este nó> or captured_at < corte_próprio)`.
- A pubkey do nó é lida uma vez de `GetStatus` e fica em cache no serviço. Se não puder ser lida e a janela própria for maior que a geral, a poda é **pulada** naquela rodada (`ErrGraphExplorerOwnPubkeyUnknown`), em vez de apagar o histórico próprio.
- O status de armazenamento informa `own_coverage_since` e `own_history_rows`.
- O detalhe do canal lê o `fee_logs` até a retenção própria (antes: 30 dias fixos).

Nada muda para quem não mexer na configuração além de passar a reter as próprias policies por um ano. A retenção geral, o limite em GB e as projeções continuam iguais.

## Testes

- `TestGraphExplorerStorageConfigOwnRetention`: padrão 365 quando não definido, nunca abaixo da retenção geral, teto 730, validação.
- `TestGraphExplorerPolicyHistoryCutoffs`: corte próprio nunca posterior ao geral, teto aplicado.
