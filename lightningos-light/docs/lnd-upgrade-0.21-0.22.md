# LND 0.21 e 0.22 — Impacto no LightningOS

Documento de referência para os upgrades do LND, mapeando cada mudança contra o código do `brln-os-light` e indicando o que é seguro, o que melhora, e o que precisa ser adaptado.

Fonte: [release-notes-0.21.0.md](https://github.com/lightningnetwork/lnd/blob/master/docs/release-notes/release-notes-0.21.0.md)

---

## LND 0.21 — Status: seguro para upgrade direto

Nenhum fluxo crítico (abertura, fechamento, pagamento, autofee, rebalance, on-chain send) quebra. Os pontos abaixo são aditivos: melhorias que podem ser adotadas opcionalmente, sem urgência.

### 1. Verificações de compatibilidade já satisfeitas

| Mudança no LND 0.21 | Onde estaríamos vulneráveis | Status atual |
|---|---|---|
| Depreciação de `sat_per_byte` em `OpenChannel`, `CloseChannel`, `SendCoins`, `SendMany`, `walletrpc.BumpFee` (remoção em 0.22) | Qualquer chamada que ainda use `SatPerByte` no request | ✅ Já usamos `SatPerVbyte` em todo o código não-gerado. Zero ocorrências de `.SatPerByte` fora de `lnrpc/` |
| `MinCLTVDelta` aumentou de 18 para 24 — invoices com `cltv_expiry_delta` entre 18-23 são rejeitados | `buildCreateInvoiceRequest` ou qualquer ponto que fixe `Invoice.CltvExpiry` baixo | ✅ Não fixamos `CltvExpiry` em `buildCreateInvoiceRequest` (`internal/lndclient/client.go:1158`). O único `FinalCltvDelta` explícito é `144` em `internal/lndclient/rebalance.go:349`, bem acima de 24 |
| `GetDebugInfo` deixa de retornar log por padrão (precisa `include_log=true`) | Qualquer chamada a `GetDebugInfo` esperando `Log` populado | ✅ Não chamamos `GetDebugInfo` em nenhum ponto (apenas o stub `lnrpc/`) |

### 2. Aditivos com potencial de adoção

Recursos novos que podem melhorar funcionalidades já existentes no app. Adotar conforme prioridade.

#### 2.1 `PendingChannels.WaitingCloseChannel` ganhou dois campos

- `blocks_til_close_confirmed` — confirmações restantes até o canal fechado ser resolvido
- `close_height` — altura do bloco em que a transação de fechamento confirmou

**Onde aplica:** Close Manager.
- `internal/server/close_manager_service.go` — atualmente o serviço calcula tempo de fechamento via heurísticas/`closed_channel_time.go`. Esses campos virão direto do LND, eliminando estimativa.
- UI da página de fechamentos (Close Manager) pode exibir tempo restante real para o canal sair de "waiting close".

**Esforço:** baixo. Adicionar leitura dos campos no fluxo de `PendingChannels` já consumido.

#### 2.2 `GetInfo.wallet_synced`

Indica se o wallet interno do LND está sincronizado (independente do `synced_to_chain`/`synced_to_graph`).

**Onde aplica:** painéis de saúde / status do nó.
- Pode ser exposto na home/health-check para detectar caso o wallet esteja dessincronizado mesmo com a chain ok.

**Esforço:** baixo. Acrescentar campo no resumo de status do nó (`GetInfo` é consumido em vários lugares — `internal/lndclient/client.go:1003`, `:3556`, `:3768`, `:4023`, `:4315`, `internal/server/telegram_backup.go:285`, etc.).

#### 2.3 `SubscribeChannelEvents` agora emite *channel update*

Eventos de mudança de estado do canal (não só open/close), úteis para reagir a alterações de policy/disable.

**Onde aplica:** `chan_status_healer` (`internal/server/chan_status_healer.go`).
- Hoje o healer faz polling. Com o evento, dá pra reduzir latência de detecção de canais que mudaram de estado (ex.: peer disabled).

**Esforço:** médio. Refatorar o loop atual para reagir a eventos em vez de só pollar.

#### 2.4 `routerrpc` HTLC events com falha mais detalhada

Subscribers passam a receber detalhes específicos de falhas de validação a nível de invoice (em vez de `UNKNOWN` ambíguo).

**Onde aplica:** `htlc_manager` (`internal/server/htlc_manager.go:656` lê `linkFail.GetInfo()`).
- Notificações de HTLC failed podem ser mais informativas no Telegram/UI sem mudança de código se já encaminhamos `failure_detail`. Vale revisar se o campo está sendo propagado.

**Esforço:** baixo (cosmético / qualidade da informação).

#### 2.5 `EstimateFee` com seleção explícita de UTXO

Novo campo `inputs` em `EstimateFeeRequest`.

**Onde aplica:** previews de envio on-chain (`internal/lndclient/onchain_preview.go`).
- Útil se um dia oferecermos coin-control. Hoje não é necessário.

**Esforço:** sob demanda.

#### 2.6 `DeleteForwardingHistory` (router sub-server)

Permite purgar eventos de forwarding antigos.

**Onde aplica:** rotina de manutenção do banco/históricos de relatórios.
- Pode ser combinado com `failed_payments_cleaner` para uma limpeza periódica do lado do LND quando o histórico já foi materializado em Postgres.

**Esforço:** baixo, mas avaliar antes se compensa (perde-se reconciliação posterior).

#### 2.7 Coordenação MuSig2 (`MuSig2RegisterCombinedNonce`, `MuSig2GetCombinedNonce`)

RPCs novos para coordenação de assinaturas MuSig2.

**Onde aplica:** nada hoje. Relevante apenas se um dia entrarmos em fluxos de Taproot Channels com co-signing.

**Esforço:** N/A.

---

## LND 0.22 — Adaptações obrigatórias antes do upgrade

A 0.22 ainda não foi lançada (ETA tipicamente 6-9 meses após 0.21). Quando sair, precisamos ter feito as migrações abaixo, pois são campos/opções que o 0.21 marcou como deprecados com remoção planejada.

### 1. Remoção de `sat_per_byte` em RPCs de fee on-chain

RPCs afetados:
- `lnrpc.OpenChannel`
- `lnrpc.OpenChannelSync`
- `lnrpc.CloseChannel`
- `lnrpc.SendCoins`
- `lnrpc.SendMany`
- `walletrpc.BumpFee`

**Status no app:** ✅ Já estamos imunes. Toda a base de código usa `SatPerVbyte`. Nenhuma adaptação necessária.

### 2. Remoção de campos deprecados em `lnrpc.Hop`

Campos que serão removidos:

| Campo deprecado | Substituto |
|---|---|
| `Hop.fee` (sat) | `Hop.fee_msat` |
| `Hop.amt_to_forward` (sat) | `Hop.amt_to_forward_msat` |
| `Hop.chan_capacity` | (sem substituto direto — capacity continua disponível em outras mensagens, ex. `ChannelEdge`) |

**Pontos do código que precisam migrar:**

| Arquivo | Linha | Uso atual | Ação |
|---|---|---|---|
| `internal/lndclient/client.go` | 1591-1592 | `if hop.Fee > 0 { total += hop.Fee * 1000 }` (agregação de fee em rota) | Trocar para `hop.FeeMsat` direto, eliminando o `* 1000` |
| `internal/lndclient/client.go` | 1642-1643 | `finalHop.AmtToForward * 1000` (msat do hop final) | Trocar para `finalHop.AmtToForwardMsat` |
| `internal/lndclient/client.go` | 3347-3358 | `hop.Fee`, `hop.AmtToForward`, `hop.ChanCapacity` em `PaymentRouteHop` summary | Usar `FeeMsat` e `AmtToForwardMsat`. Para capacity: ou remover do summary, ou enriquecer via `DescribeGraph`/`GetChanInfo` |
| `internal/server/notifications.go` | 2228-2306 | `hop.AmtToForward`, `hop.Fee`, `hop.ChanCapacity` em payload de notificações de pagamento | Idem: msat substitui fee/amt; capacity precisa de fonte alternativa ou é removida do payload |

**Estratégia recomendada:** abrir um PR só para essa migração antes de iniciar testes do 0.22. A mudança é mecânica e cabe num único commit. Os testes existentes (`payment_fee_test.go`, `notifications_route_history_test.go`) cobrem os cenários de rota — precisam ser atualizados para popular os campos `_msat` em vez dos sat.

**Sobre `chan_capacity`:** o release note de 0.21 não dá data firme de remoção (segue marcado como "deprecated since 0.7.1"). Em prudência, ao migrar fee/amt já planejar como obter capacity por outra via — geralmente o `chan_id` do hop pode ser cruzado com `DescribeGraph` ou cache local. Se o uso for puramente informativo (telemetria/notificação), avaliar simplesmente remover o campo do payload.

### 3. Não previstas, mas para vigiar nas próximas notas

Campos/RPCs a monitorar nos próximos releases candidates da 0.22:
- `lnrpc.SendRequest` (legacy `SendPayment` síncrono) — pode receber depreciação adicional, mas já usamos `routerrpc.SendPaymentV2` para todos os fluxos.
- Campos `fee` (sat) em outros lugares além de `Hop` (ex.: `Payment`, `HTLCAttempt`) — verificar se ganharam aviso de remoção.

---

## Plano de ação resumido

### Hoje (LND 0.21 disponível)
1. Atualizar binário do LND para 0.21 sem alterar código — testar fluxos críticos (open, close, pay, rebalance, autofee) em staging primeiro.
2. (Opcional, ordem de impacto) Adotar aditivos:
   - `PendingChannels.WaitingCloseChannel.{blocks_til_close_confirmed, close_height}` no Close Manager.
   - `GetInfo.wallet_synced` no health do nó.
   - Eventos extra em `SubscribeChannelEvents` no `chan_status_healer`.

### Antes da 0.22
1. PR único migrando `Hop.fee`/`Hop.amt_to_forward` para variantes `_msat` nos 4 pontos listados.
2. Decidir destino de `Hop.chan_capacity`: remover do payload ou enriquecer via cache de grafo.
3. Atualizar testes (`payment_fee_test.go`, `notifications_route_history_test.go`).
4. Rodar `go test ./...` e validar manualmente um pagamento para confirmar fee/amt nas notificações.
