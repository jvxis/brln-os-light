# Análise da PR #2 — Interactive UTXO manager + Wallet Flow

> Revisão técnica de `feature/utxo-manager-wallet-flow` → `main` em `pagcoinbr/brln-os-light`.
> Escopo: 27 arquivos, +5.211 / -42 linhas, 2 commits.
> Base: `pagcoinbr/main` está 3 commits à frente de `jvxis/main` (behind 0) — aplica limpo upstream.

---

## TL;DR

Tecnicamente aceitável. **Não quebra compilação**, **não colide com rotas existentes**, e **degrada gracefully** quando Postgres/electrs não estão disponíveis. As features novas (UTXO Manager + Wallet Flow) são bem isoladas em serviços lazy-init com mutex, no mesmo padrão de `closeManager` e `nodeRetirement`.

Pré-port para upstream, sugiro 4 follow-ups (ver seção *Riscos*).

---

## O que muda

### Backend (Go)
- **Novo pacote `internal/electrs`** — cliente Electrum JSON-RPC mínimo, TCP, single-connection. Default `127.0.0.1:50001`, override via `ELECTRUM_RPC_ADDR`.
- **`internal/lndclient/utxo_manager.go`** — `SendCoinsAdvanced`, `OpenChannelWithOutpoints`, `LeaseOutput`/`ReleaseOutput`/`ListLeases` (lease IDs determinísticos via SHA-256 do outpoint, seed `brln-os-utxo-mgr:v1:`).
- **`internal/lndclient/utxo_provenance.go`** — `ListAllTransactions` expondo o proto cru para o serviço de provenance.
- **`PreviewOnchainSend` ganha `outpoints []string`** — quando setado, força LND a fundar o PSBT exatamente dos UTXOs informados (sem cair na coin-selection padrão).
- **`internal/server/utxo_manager_*.go`** — 3 arquivos: service (DB), init (lazy), handlers. Tabelas `utxo_groups` e `utxo_metadata` com FK `ON DELETE SET NULL` e auto-prune de outpoints que não existem mais no wallet.
- **`internal/server/provenance_*.go`** — 3 arquivos. Tabelas `provenance_tx`, `provenance_output`, `provenance_state`. Incremental refresh com lookback de `2<<16` blocks para reorgs; full rebuild via `?full=true`. Enriquecimento opcional de external txs via electrs (batch 200, max 500 por request em `lineage`).
- **12 rotas novas** em `/api/onchain/...` (utxos/metadata, utxos/lock|unlock|bump, utxos/groups[/:id[/assign]], provenance[/status|/health|/rebuild]).

### Frontend (React)
- **`pages/WalletFlow.tsx`** (661 linhas) — grafo ReactFlow + dagre layout + custom Sankey edges.
- **`pages/OnchainHub.tsx`** (+404/-1) — toggle Canvas/Table, default = Canvas. Bulk actions: rename, lock/unlock, group/ungroup, consolidate, send, bump fee, open channel a partir da seleção.
- **Componentes novos**: `UtxoCanvas` (d3-force), `TxNode` (barras proporcionais por sqrt do valor), `SankeyEdge`, `UtxoSpendDialog`, `UtxoBumpDialog`, `UtxoOpenChannelDialog`.
- **`utils/utxoStyles.ts` + `utils/utxoPrivacy.ts`** — paleta compartilhada e avisos de privacidade (common-input ownership, mistura de address types, dust ratio em consolidações).
- **`App.tsx`** — menu item `wallet-flow` aparece dinamicamente quando `/api/onchain/provenance/health` reporta `electrs_available=true` (poll a cada 60s).
- **`vite.config.ts`** — `resolve.dedupe: ['d3-selection', 'd3-transition', 'd3-zoom', 'd3-drag', 'd3-dispatch']` para evitar conflito entre reactflow e recharts.
- **`package.json`** — `reactflow`, `dagre` (+`@types/dagre`), `d3-force` (+`@types/d3-force`).

---

## Verificação de consistência com o código existente

| Item | Status | Notas |
|------|--------|-------|
| Assinatura `PreviewOnchainSend` muda | ✅ | Único caller (`handleWalletSendPreview`) atualizado no mesmo commit |
| `lnrpc.SendCoinsRequest.Outpoints` / `OpenChannelRequest.Outpoints` | ✅ | Confirmados em `lightning.pb.go` linhas 3653 e 7869 |
| `BumpFeeParams.Immediate` | ✅ | Existe em `client.go:7351` |
| `parseOutPoint`, `channelPointString`, `txidFromBytes` | ✅ | Existem em `client.go` |
| `ResolveNotificationsDSN`, `s.db`, `s.logger` | ✅ | Mesmo pool/DSN do sistema de notificações |
| Colisão de rotas em `/api/onchain` | ✅ | Sem colisão com `/utxos` e `/transactions` existentes |
| `previewOnchainSend`/`sendOnchain`/`openChannel` em `api.ts` | ✅ | Novos campos opcionais; callers em `Wallet.tsx` e `LightningOps.tsx` continuam funcionando |
| `clsx` util | ✅ | Existe em `utils/clsx.ts` |
| Compilação Go contra symbols locais | ✅ | Nada órfão |
| Degradação sem Postgres | ✅ | Ambos serviços viram 503 e nada quebra |
| Degradação sem electrs | ✅ | Item de menu Wallet Flow some; UTXO Manager segue funcionando |

---

## Riscos / pontos de atenção

### 1. `BRLN_SKIP_WIZARD=1` em `walletExists()`
Foi adicionado um *escape hatch* dev no caminho mais sensível do app:

```go
if v := strings.TrimSpace(os.Getenv("BRLN_SKIP_WIZARD")); v == "1" || strings.EqualFold(v, "true") {
    return true
}
```

Não é exploração remota, mas em produção (mainnet) com a env setada por engano, a UI navega como se a wallet existisse, podendo mascarar erros de setup. Sugestão: remover, gate via build tag, ou pelo menos logar warning.

### 2. Bump fee sem `confirm_password`
`handleUtxoBump` gasta sats (CPFP) e não exige reautenticação. Hoje no projeto, `handleWalletSend` exige `confirm_password` para envios externos. Inconsistente com a postura atual de segurança. Lock/unlock também não exigem — risco menor por serem reversíveis.

### 3. Latência adicional no `GET /api/onchain/utxos`
`enrichOnchainUtxos` agora chama `ListLeases` + `ListMetadata` + `Prune` em todo request. O OnchainHub faz auto-refresh a cada 30s. Em wallets grandes pode pesar. Considerar cache curto (5–10s) na camada de service.

### 4. Mudança de comportamento sutil do `limit`
Antes: `items = items[:limit]` (corte antes de qualquer trabalho). Depois: enrich completo e depois `enriched[:limit]`. Intencional (o prune precisa da lista completa), mas vale documentar — clientes que passavam `?limit=N` esperando short-circuit não terão isso.

### 5. Dependência implícita em electrs
Default fixo em `127.0.0.1:50001`. Não está documentado em `CLAUDE.md` nem em `configs/config.yaml`. `ELECTRUM_RPC_ADDR` é o override mas precisa entrar na doc. Sugestão: adicionar à tabela de env vars do `CLAUDE.md`.

### 6. Default view do OnchainHub mudou
Passa a abrir em **Canvas** em vez da tabela. Mudança de UX deliberada, mas vale uma decisão consciente — se queremos preservar o comportamento atual, o default vira `'table'`.

### 7. Sem i18n nas strings novas
"Canvas", "Table", "Consolidate all", "Spend…", "Group", "Lock", "Bump fee" — tudo hardcoded em inglês. Resto do app usa i18next com pt-BR/en. Inconsistente com o padrão do projeto.

### 8. `handleProvenanceRebuild` não cancelável
Usa `context.Background()` em goroutine separada com timeout de 5 min. Restart do server no meio do rebuild pode deixar `provenance_state` desatualizado (mitigado pelas transações per-tx, mas a row de state pode ficar para trás). Para um produto que se preocupa com graceful shutdown, ideal seria ouvir um signal de stop.

### 9. Bundle UI cresce
`reactflow` (~150kB gzip), `dagre`, `d3-force`. Mitigado pelo `resolve.dedupe`. Vale observar se o bundle total ficou aceitável.

---

## Itens menores / observações

- Coinbase detection em `markSpentByInputs` / `walkExternalLineage` usa `prevVout > 0x7FFFFFFF`. O valor sentinel real é `0xFFFFFFFF`; o teste é over-inclusive mas seguro (não cria registros falsos para vouts válidos).
- `deriveLeaseID` é determinístico (`SHA-256(seed + outpoint)`). Mudar o seed estranda leases até expirarem — bom comentário já está no código.
- O FK em `utxo_metadata.group_id` é `ON DELETE SET NULL`, mas `DeleteGroup` faz `delete from utxo_groups where id = $1` direto. Funciona; só lembrar que membros viram "sem grupo" automaticamente.
- `enrichExternalInputs` percorre apenas as `provenance_output` rows com `amount_sat=0 AND address='' AND is_ours=false` — bom filtro para evitar reenriquecer.

---

## Recomendação

✅ Aceitar a feature para evoluir o produto, mas tratar os 4 itens prioritários antes de portar para `jvxis/brln-os-light`:

1. Decidir destino do `BRLN_SKIP_WIZARD` (remover ou build-tag)
2. Adicionar `confirm_password` em `/api/onchain/utxos/bump`
3. Documentar `ELECTRUM_RPC_ADDR` + dependência electrs no `CLAUDE.md` e/ou `config.yaml`
4. Decidir default view do OnchainHub e adicionar i18n às strings novas

Itens 3, 5, 7 e 8 podem virar issues de follow-up se preferir mergear primeiro e refinar depois.

---

## Sugestões de correção (com pointers)

### Fix 1 — Remover/proteger `BRLN_SKIP_WIZARD`
**Arquivo:** `lightningos-light/internal/server/handlers.go` (função `walletExists`, dentro do bloco adicionado pela PR)

**Opção A — remover** (preferida para mainnet):
```go
// (remover o bloco inteiro adicionado pela PR)
func walletExists() bool {
    if walletPasswordAvailable() {
        return true
    }
    // ...lógica original
}
```

**Opção B — gate em build tag de dev**: criar `handlers_devhatch.go` com `//go:build devhatch` que define a função de bypass; o build de produção usa a versão sem o hatch. Assim a env var só é honrada em binários compilados explicitamente com `-tags devhatch`.

**Opção C (mínima)** — manter mas reduzir o blast radius: aceitar apenas se `LND_NETWORK != mainnet` (consultar `s.cfg.LND.Network` ou similar), e logar um warning sempre que o bypass for usado.

### Fix 2 — Exigir `confirm_password` em `/api/onchain/utxos/bump`
**Arquivo:** `lightningos-light/internal/server/utxo_manager_handlers.go`, função `handleUtxoBump`.

Padrão a seguir é o de `handleWalletSend` (mesmo arquivo `handlers.go`, ~linha 4246):

```go
var req struct {
    Outpoint        string `json:"outpoint"`
    SatPerVbyte     int64  `json:"sat_per_vbyte"`
    TargetConf      uint32 `json:"target_conf"`
    BudgetSat       int64  `json:"budget_sat"`
    ConfirmPassword string `json:"confirm_password"`
}
// ...
if err := s.requireConfirmPassword(r.Context(), req.ConfirmPassword); err != nil {
    writeError(w, http.StatusUnauthorized, "password confirmation required")
    return
}
```
E em `ui/src/components/UtxoBumpDialog.tsx`, adicionar campo de senha antes do submit, igual ao `UtxoSpendDialog`.

> Vale considerar a mesma exigência em `/utxos/lock` e `/utxos/unlock`? Argumento contra: são reversíveis e não gastam sats. Argumento a favor: alguém com acesso à sessão pode silenciosamente bloquear toda a coin-selection do autopiloto/rebalance. Sugestão: deixar como está, mas adicionar log de auditoria (`s.logger.Printf("utxo lock by %s: %v", session.User, outpoints)`).

### Fix 3 — Cache curto em `enrichOnchainUtxos`
**Arquivo:** `lightningos-light/internal/server/utxo_manager_handlers.go`

`ListLeases` + `ListMetadata` + `Prune` rodam a cada `GET /api/onchain/utxos` (auto-refresh 30s no UI). Sugestão: cache de leases em memória com TTL de 10s (igual ao padrão de `bitcoin_status_cache.go`). Prune continua best-effort, mas só dispara a cada N segundos (ex: 5 min), não a cada request.

### Fix 4 — Documentar dependência (opcional) de electrs
**Arquivo:** `CLAUDE.md` (na tabela "Key Environment Variables")

```diff
 | Variable | Purpose |
 |----------|---------|
 | `REPORTS_TIMEZONE` | Override timezone for daily reports (IANA format) |
 | `REPORTS_RUN_TIMEOUT_SEC` | Timeout for reports job (default: 120s) |
 | `TERMINAL_ENABLED` | Set to `0` to disable GoTTY terminal |
+| `ELECTRUM_RPC_ADDR` | Address of an Electrum-protocol server (default `127.0.0.1:50001`). Enables Wallet Flow (transaction provenance graph) when reachable. |
+| `BRLN_SKIP_WIZARD` | Dev-only: when `1`, pretends the LND wallet is initialized. Do not set in production. |
```
E na seção `## Architecture` → `### Data Flow`, mencionar que provenance é opcional e pode usar electrs OU bitcoind (ver próxima seção).

### Fix 5 — Default view do OnchainHub
**Arquivo:** `lightningos-light/ui/src/pages/OnchainHub.tsx` (linha ~89 do diff)

```diff
-  const [utxoView, setUtxoView] = useState<'canvas' | 'table'>('canvas')
+  const [utxoView, setUtxoView] = useState<'canvas' | 'table'>(() => {
+    const saved = localStorage.getItem('onchainHub.view')
+    return saved === 'canvas' || saved === 'table' ? saved : 'table'
+  })
+  useEffect(() => {
+    localStorage.setItem('onchainHub.view', utxoView)
+  }, [utxoView])
```
Default `'table'` preserva a experiência atual; usuário escolhe canvas conscientemente e a escolha persiste.

### Fix 6 — i18n nas strings novas
**Arquivos:** `lightningos-light/ui/src/i18n/{en,pt-BR}.json` + `OnchainHub.tsx`, dialogs.

Strings a internacionalizar (lista mínima):
- "Canvas", "Table", "Top {n} by amount", "Consolidate all"
- "Spend…", "Consolidate", "Rename", "Group", "Ungroup", "Lock", "Unlock", "Bump fee", "Open channel"
- "Broadcast: {txid}", "Bump submitted.", "Channel opening: {cp}"
- Privacy warnings em `utxoPrivacy.ts` (já recebem texto pronto — precisam virar chaves i18n)

Sugestão de namespace: `onchainHub.utxo.*` e `onchainHub.privacy.*`.

### Fix 7 — Cancelamento de `provenance.RefreshNow`
**Arquivo:** `lightningos-light/internal/server/provenance_handlers.go`, `handleProvenanceRebuild`

Trocar `context.Background()` por contexto derivado de um shutdown channel:
```go
ctx, cancel := context.WithTimeout(s.shutdownCtx, 5*time.Minute)
go func() { defer cancel(); _ = svc.RefreshNow(ctx, fullRebuild) }()
```
(Requer expor um `s.shutdownCtx context.Context` no struct `Server`, cancelado em `Run()`/`Shutdown()`. Verifique se existe um padrão similar já em uso, ex.: outros services lazy-init.)

---

## Alternativa estratégica: substituir electrs por bitcoind RPC

> **Resumo:** O Wallet Flow precisa de exatamente uma capacidade: dado um `txid`, retornar a tx decodificada (vin/vout). O electrs serve isso via `blockchain.transaction.get`, mas **bitcoind também serve via `getrawtransaction <txid> 1`** — desde que o node seja **não-pruned com `txindex=1`**. O projeto já garante essa configuração no app Bitcoin Core da loja.

### Por que isso é viável neste projeto

1. **A app Bitcoin Core da loja já habilita `txindex=1`** em [apps_bitcoincore.go:429](lightningos-light/internal/server/apps_bitcoincore.go#L429) (`"txindex=1"` é seedado no `bitcoin.conf` na instalação).
2. **Já existe um detector de "node pronto para indexação completa"** em [apps_full_index.go:36](lightningos-light/internal/server/apps_full_index.go#L36) — a função `fullIndexAppAvailability(ctx)` retorna `Available=true` exatamente quando: source é `local` + Bitcoin Core instalado + **não-pruned** + `txindex` sincronizado. Essa função já é usada como pré-requisito para instalar electrs e mempool da loja.
3. **Já existe um helper de RPC** em [bitcoin_local.go](lightningos-light/internal/server/bitcoin_local.go): `execBitcoinCLI(ctx, paths, cmd, args...)` invoca `bitcoin-cli` dentro do container Docker do Bitcoin Core e retorna stdout. Não precisa resolver creds manualmente — o `.cookie` já está montado no container.
4. **O schema de `getrawtransaction <txid> 1` é compatível com `electrs.VerboseTx`**: ambos retornam `txid`, `vin[].txid/vout`, `vout[].n`, `vout[].value` (BTC float), `vout[].scriptPubKey.address`/`addresses`, `time`, `confirmations`, `blockhash`. O parser que o PR já escreveu funciona contra os dois sem modificações.

### Implementação sugerida

#### Passo 1 — Definir um backend interface (refator pequeno)

`lightningos-light/internal/txbackend/backend.go` (novo pacote):
```go
package txbackend

import "context"

type VerboseVout struct {
    Value        float64 `json:"value"`
    N            uint32  `json:"n"`
    ScriptPubKey struct {
        Address   string   `json:"address"`
        Addresses []string `json:"addresses"`
        Type      string   `json:"type"`
        Hex       string   `json:"hex"`
    } `json:"scriptPubKey"`
}
type VerboseVin struct { Txid string `json:"txid"`; Vout uint32 `json:"vout"` }
type VerboseTx struct {
    Txid          string        `json:"txid"`
    Hash          string        `json:"hash"`
    BlockHash     string        `json:"blockhash,omitempty"`
    Confirmations uint32        `json:"confirmations,omitempty"`
    Time          int64         `json:"time,omitempty"`
    Vin           []VerboseVin  `json:"vin"`
    Vout          []VerboseVout `json:"vout"`
}

type Backend interface {
    Available(ctx context.Context) (bool, string) // (ok, addr_or_id)
    GetTransaction(ctx context.Context, txid string) (VerboseTx, error)
}
```
`internal/electrs/client.go` já implementa exatamente essa shape — basta a Go interface assertion.

#### Passo 2 — Backend `bitcoind`

`lightningos-light/internal/txbackend/bitcoind.go`:
```go
type BitcoindBackend struct {
    paths bitcoinCorePaths
}

func (b *BitcoindBackend) Available(ctx context.Context) (bool, string) {
    // Reusa o check que já existe — só precisa do server para passar paths
    info, err := fetchBitcoinLocalChainInfo(ctx, b.paths)
    if err != nil || info.Pruned { return false, "" }
    ready, known, _ := bitcoinCoreTxIndexReady(ctx, b.paths, "")
    if !known || !ready { return false, "" }
    return true, fmt.Sprintf("bitcoind+cli://%s", b.paths.ContainerName)
}

func (b *BitcoindBackend) GetTransaction(ctx context.Context, txid string) (VerboseTx, error) {
    out, err := execBitcoinCLI(ctx, b.paths, "getrawtransaction", txid, "1")
    if err != nil { return VerboseTx{}, err }
    var tx VerboseTx
    return tx, json.Unmarshal([]byte(out), &tx)
}
```
(Como `fetchBitcoinLocalChainInfo`, `bitcoinCoreTxIndexReady` e `execBitcoinCLI` vivem no pacote `server`, vale extraí-los para um helper compartilhável — ou injetar via callback no construtor, evitando dependência circular.)

#### Passo 3 — Init com priority

`lightningos-light/internal/server/provenance_init.go`:
```go
func (s *Server) initProvenance() {
    // ...
    var backend txbackend.Backend
    bc := &txbackend.BitcoindBackend{Paths: bitcoinCoreAppPaths()}
    if ok, _ := bc.Available(ctx); ok {
        backend = bc
        s.logger.Printf("provenance: using local bitcoind (txindex+non-pruned)")
    } else {
        ec := electrs.New("")
        if _, err := ec.Ping(ctx); err == nil {
            backend = ec
            s.logger.Printf("provenance: using electrs at %s", ec.Addr())
        }
    }
    if backend == nil {
        s.provenanceErr = "provenance unavailable: no transaction backend (install Bitcoin Core unpruned with txindex, or run electrs)"
        return
    }
    svc := NewProvenanceService(pool, s.logger, s.lnd, backend)
    // ...
}
```
`provenance_service.go` passa a aceitar `txbackend.Backend` em vez de `*electrs.Client`. O resto da lógica (`enrichExternalInputs`, `walkExternalLineage`, `persistExternalTx`) **não muda** — só os tipos.

#### Passo 4 — Health endpoint reflete o backend ativo

`handleProvenanceHealth` retorna o backend escolhido:
```json
{
  "available": true,
  "backend": "bitcoind",   // ou "electrs" ou "none"
  "addr": "bitcoind+cli://core-bitcoin"
}
```
A UI (`App.tsx`) já usa o flag `electrs_available` para mostrar o menu — basta renomear para `provenance_available` (compat: aceitar ambos durante 1 release).

### Quando ainda vale instalar electrs

- Bitcoin source é **`remote`** (default do projeto: `bitcoin.br-ln.com`). Aí não temos `bitcoin-cli` local; só sobra electrs apontando para o remote — ou nada.
- Bitcoin local com **pruning ativado**: `getrawtransaction` falha para txs antigas. Electrs também precisa de full node, então neste caso a Wallet Flow simplesmente não funciona — coerente com o gate atual do `fullIndexAppAvailability`.
- Performance: para usuários muito ativos com milhares de external txs por refresh, `bitcoin-cli` shell-exec pode ser mais lento que electrs JSON-RPC TCP. Mitigação: substituir `execBitcoinCLI` por chamadas HTTP diretas ao `bitcoind` (porta RPC, com cookie auth) — schema idêntico, latência menor. Mas só vale otimizar se medir necessidade.

### Benefícios práticos

- **Usuário típico não precisa instalar electrs** para usar Wallet Flow — basta o Bitcoin Core da loja com txindex (que já é o default).
- **Reduz uma dependência opcional do stack** (electrs é um app Docker pesado, ~1-2 GB extra de DB).
- **Health/discovery automático**: o app detecta a melhor opção sem env var manual.
- **Symmetric com `fullIndexAppAvailability`**: o sistema já comunica ao usuário quando o node está pronto para "full index apps" — Wallet Flow se beneficia desse mesmo sinal sem código novo de detecção.

### Estimativa de esforço

- Backend interface + bitcoind impl: ~150 LOC de Go.
- Refator do `provenance_service` para aceitar interface: troca de tipos, sem mudança de lógica.
- UI: rename de uma flag.
- Testes: o backend electrs continua testável como hoje; bitcoind backend ganha um fake `execBitcoinCLI` para testes unitários.

**Pode ser feito como follow-up depois do merge** (a feature já funciona com electrs como ponto de partida), ou como pré-condição se prefirir embarcar bitcoind-first desde o início.
