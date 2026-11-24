# Breez Bridge Probe

Prova técnica isolada da Tesouraria em Stablecoins. Consulta as rotas BTC Lightning → USDC nativo na Base e USDC nativo na Base → BTC Lightning. O comando exibe apenas a contagem de rotas compatíveis por direção. Ele usa a rede Base com chain ID 8453 e o contrato publicado pela Circle. [Contrato do USDC](https://developers.circle.com/stablecoins/usdc-contract-addresses)

O código chama somente descoberta de rotas. A criação de cotações, ordens e pagamentos fica para uma etapa posterior. A consulta envia os destinatários públicos ao provedor. Use destinatários de teste e injete a chave do Breez a partir de seu armazenamento de segredos como variável de ambiente `BREEZ_API_KEY`. [Bridging no SDK Breez](https://sdk-doc-spark.breez.technology/guide/bridging.html)

## Executar em Linux amd64

A biblioteca Go v0.28.6 usa `cgo` e uma biblioteca nativa compartilhada. Para desenvolvimento a partir deste diretório, use Go, um compilador C e o cache de módulos local. [Bindings Go](https://github.com/breez/breez-sdk-spark-go/tree/v0.28.6)

```bash
go mod download
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 go vet ./...
CGO_ENABLED=1 go build -o /tmp/breez-bridge-probe .
/tmp/breez-bridge-probe --check-bindings
timeout 30s /tmp/breez-bridge-probe "$BASE_RECIPIENT" "$LIGHTNING_RECIPIENT"
```

Preencha `BASE_RECIPIENT` com o endereço público de uma carteira de teste na Base. Para a consulta de retorno, use um destinatário Lightning de teste. Use uma Lightning Address como destinatário de retorno. O SDK v0.28.6 rejeita uma invoice BOLT11 como destinatário do bridge. [Parser do SDK v0.28.6](https://github.com/breez/spark-sdk/blob/5bb1ca5229cd4d6d4e8cf66dfe9d01224db6cd2d/crates/breez-sdk/core/src/sdk/bridging/mod.rs#L407)

`--check-bindings` verifica apenas que o executável carrega a biblioteca e inicializa os bindings. A consulta também exige uma chave válida e rede. A presença de uma rota indica que foi anunciada pelo provedor. Cotação, entrega e recuperação exigem validação própria.

## Empacotamento

O binário de desenvolvimento pode depender de caminhos do cache de Go. Na compilação cruzada com Zig no Windows, a inspeção ELF encontrou tanto `RUNPATH` como uma entrada `NEEDED` com o caminho absoluto da biblioteca no host de build. Copiar apenas o executável e a biblioteca não corrige esse binário. Antes de empacotar um processo separado, validar um binário relocável construído em Linux, conferir dependências ELF e executar o comando em ambiente limpo. O Manager não importa esse módulo. [Plano e critérios de liberação](../../docs/STABLECOIN_TREASURY_APP_PLAN.md)
