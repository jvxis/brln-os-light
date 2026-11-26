# Breez Bridge Probe

O protocolo privado `--bridge-worker` usa USDC nativo na Solana nos dois sentidos. As ações `quote` e `status` recebem JSON via stdin. O worker não executa pagamentos. Ele limita a cotação ao mint oficial da Circle e retorna depósito, valor líquido esperado, validade e taxa do provedor quando informada. A ação `health` verifica carregamento sem exigir uma credencial. A interface pública de linha de comando abaixo reproduz apenas o piloto anterior na Base. [Plano atual da Tesouraria](../../docs/STABLECOIN_TREASURY_APP_PLAN.md)

As ações `sponsor-preflight` e `sponsor-estimate` consultam o Pimlico com uma chave do patrocinador, distinta da chave do Breez. Use `cluster` igual a `devnet` para validação sem fundos reais. A consulta retorna a conta que paga SOL, o destino de cobrança e a taxa em unidades inteiras de USDC. O worker mantém a chave no cabeçalho de autorização. [API de patrocínio Solana](https://docs.pimlico.io/references/paymaster/solana/endpoints)

`solana-proof` consulta o RPC público e exige transação finalizada, identidade da rede, mint oficial, programa SPL Token, dono da conta e crédito líquido igual ao valor cotado. A prova não requer chave do provedor. [Confirmação em Solana](https://solana.com/developers/docs/payments/checkout/backend)

`solana-refund-proof` aplica as mesmas verificações de rede, transação e carteira ao reembolso. O valor confirmado deve ser positivo e menor ou igual ao depósito original.

A resposta privada de `status` inclui `provider_status` quando o provedor informa uma etapa. A etapa ajuda a acompanhar uma ordem pendente. A confirmação do depósito e a entrega continuam registradas em campos próprios. [Estados da ponte](https://sdk-doc-spark.breez.technology/guide/bridging.html#tracking-a-bridge)

Prova técnica isolada da Tesouraria em Stablecoins. Consulta as rotas BTC Lightning → USDC nativo na Base e USDC nativo na Base → BTC Lightning. O comando exibe apenas a contagem de rotas compatíveis por direção. Ele usa a rede Base com chain ID 8453 e o contrato publicado pela Circle. [Contrato do USDC](https://developers.circle.com/stablecoins/usdc-contract-addresses)

A consulta de rotas é o modo padrão. O modo opcional de cotação cria ordens sem movimentar fundos. A consulta envia os destinatários públicos ao provedor. Use destinatários de teste e injete a chave do Breez a partir de seu armazenamento de segredos como variável de ambiente `BREEZ_API_KEY`. [Bridging no SDK Breez](https://sdk-doc-spark.breez.technology/guide/bridging.html)

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

## Cotação sem financiamento

O modo padrão só consulta rotas. Para testar cotações, use a opção abaixo com destinos de teste e endereços de reembolso explícitos. O Breez cria uma ordem por direção e o probe confirma o estado inicial sem pagamento. Os dados impressos incluem valores e validade. As instruções de pagamento e credenciais de consulta ficam fora da saída do probe.

```bash
timeout 45s /tmp/breez-bridge-probe \
  --quote-usdc-micros 10000000 \
  --base-refund "$BASE_REFUND" \
  --lightning-refund "$LIGHTNING_REFUND" \
  "$BASE_RECIPIENT" "$LIGHTNING_RECIPIENT"
```

O modo usa 100 basis points de tolerância solicitada e adiciona a taxa ao valor de depósito. O montante pedido está em milionésimos de USDC. Use o valor de depósito e o recebimento esperado retornados pelo provedor, pois podem divergir do valor solicitado. A validade da cotação regula o preço. O provedor documenta que alguns depósitos on-chain tardios ainda podem ser aceitos por outro valor. [Cotação e acompanhamento no Breez](https://sdk-doc-spark.breez.technology/guide/bridging.html#tracking-a-bridge)

## Empacotamento

Na compilação cruzada com Zig no Windows, a inspeção ELF encontrou `RUNPATH` e `NEEDED` com caminhos absolutos do host de build. O build nativo em Ubuntu 24.04.3 amd64 com Go 1.26.10 gerou `NEEDED` com o nome da biblioteca e `RUNPATH` apontando para o cache de módulos.

No laboratório, o empacotamento abaixo foi validado com `patchelf` 0.18.0. O comando carregou a biblioteca do próprio diretório, sem `LD_LIBRARY_PATH`. Falhou no carregador quando a biblioteca foi retirada do diretório e voltou a funcionar depois de restaurá-la.

```bash
bundle=$(mktemp -d)
module_dir=$(go list -m -f '{{.Dir}}' github.com/breez/breez-sdk-spark-go)
cp /tmp/breez-bridge-probe "$bundle/"
cp "$module_dir/breez_sdk_spark/lib/linux-amd64/libbreez_sdk_spark_bindings.so" "$bundle/"
patchelf --set-rpath '$ORIGIN' "$bundle/breez-bridge-probe"
objdump -p "$bundle/breez-bridge-probe" | grep -E 'NEEDED|RUNPATH'
env -u LD_LIBRARY_PATH "$bundle/breez-bridge-probe" --check-bindings
```

Para distribuição como serviço, a instalação deverá verificar a procedência do executável e da biblioteca e impedir escrita no diretório do bundle pelo usuário do serviço. O Manager não importa esse módulo. [Plano e critérios de liberação](../../docs/STABLECOIN_TREASURY_APP_PLAN.md)

`bash package-linux.sh NEW_OUTPUT_DIRECTORY` monta o bundle nativo Linux amd64, as units systemd e o manifesto SHA-256. A socket aceita apenas conexões do grupo do Manager. Cada pedido roda com usuário dinâmico dedicado e sem acesso às credenciais do LND.
