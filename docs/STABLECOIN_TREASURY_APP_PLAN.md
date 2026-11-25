# Tesouraria em Stablecoins para LightningOS

A primeira versão proposta para a loja converte manualmente entre sats no LND e USDC nativo na Solana. O operador controla a carteira Solana. O app deve informar valores e custos antes de cada confirmação. O alvo continua sendo a branch `agent/0.5.60-release`, em Linux amd64. A tarefa usa a [PR #446](https://github.com/jvxis/brln-os-light/pull/446).

Em 25 de novembro de 2026, o operador escolheu evitar redes Ethereum. O teste anterior na Base permanece como evidência do provedor. Os fundos daquele teste continuam na carteira Base. O caminho da Base foi retirado do código destinado à loja.

## Configuração e carteira

1. Abra o formulário oficial do Breez, selecione a solicitação de chave do SDK e informe seu contato. A configuração da Tesouraria deve aceitar a chave Breez com confirmação da senha do LightningOS. O Manager guarda a credencial sem retorná-la pela API. [Solicitar uma chave Breez](https://breez.technology/request-api-key/#contact-us-form-sdk)
2. Crie ou recupere uma carteira Solana no assinador isolado. Baixe o backup criptografado e verifique que consegue recuperar o endereço com a senha. Guarde a senha separadamente. O Manager recebe o endereço público. A chave privada, o backup e sua senha ficam no dispositivo do operador. [Piloto local da carteira Solana](../tools/solana-treasury-wallet/README.md)
3. Para receber sats de volta, configure uma Lightning Address vinculada ao LND local. Antes de usar o endereço, obtenha uma invoice sem pagamento, valide os metadados LNURL e confira a identidade do node destinatário. [Integração LNURL do clube](TEST_0.5.55_TOR_LND_REST.md)

## Evidência das rotas

Em 25 de novembro de 2026, o Breez Spark SDK v0.28.6 retornou as duas rotas Lightning ↔ Solana para o mint oficial de USDC da Circle, `EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v`. A descoberta usou um endereço Solana sintético e o Lightning Address autorizado do node de piloto. A resposta inicial das duas cotações era `Created`, sem depósito reconhecido. Nenhum pagamento foi enviado na Solana. [Mint e modelo de pagamentos](https://solana.com/docs/payments/how-payments-work) · [Estados do Breez](https://sdk-doc-spark.breez.technology/guide/bridging.html#tracking-a-bridge)

| Sentido | Depósito cotado | Recebimento esperado | Taxa do provedor já incluída |
| --- | ---: | ---: | ---: |
| Lightning → Solana | 1.937 sats, antes da taxa de roteamento | 1,020000 USDC | 0,117534 USDC |
| Solana → Lightning | 1,065005 USDC, antes do custo de taxa de rede | 1.721 sats | 0,021045 USDC |

Os números são uma amostra para valor solicitado de 1 USDC, com taxa adicionada pelo provedor. As duas cotações usam unidades inteiras nas menores unidades de cada ativo. A validade é curta e o valor de recebimento pode mudar dentro da tolerância indicada. A amostra precisa ser seguida de liquidação financiada e prova de reembolso.

O piloto anterior entregou 1,007394 USDC na Base ao endereço criado pelo usuário, cerca de 11 minutos e 31 segundos após a liquidação de 1.751 sats. A taxa Lightning foi 1.751 msat. O Breez só passou a reconhecer o depósito após cerca de dez minutos. Essa experiência justifica separar débito, reconhecimento do provedor e crédito final. Uma ordem financiada permanece em reconciliação até que a entrega ou o reembolso seja comprovado.

## Fluxo de ida, volta e custos

- **Lightning → USDC.** Vincular a cotação ao destino Solana e ao reembolso em uma invoice sem valor do LND. Exibir o depósito em sats, a taxa do provedor quando discriminada, o teto de roteamento e o máximo a debitar. Conferir rota, hash, valor, rede e expiração antes de executar pelo LND, com reautenticação e autorização de uso único. Verificar na Solana a assinatura da transação, o mint oficial, o dono da conta de token e a diferença exata de saldo.
- **USDC → Lightning.** O operador vê o USDC destinado à conversão, a taxa do provedor, os sats líquidos esperados e a taxa de rede cobrada em USDC, quando houver. Na Solana, um serviço de taxa pode pagar SOL e cobrar em USDC. O operador assina o envio com sua própria carteira. Validar o depósito na Solana e localizar pelo hash a invoice exata liquidada no LND. [Modelo de patrocínio da taxa](https://solana.com/docs/payments/send-payments/payment-processing/fee-abstraction)
- **Total da volta.** Exibir separadamente o valor que chega ao provedor e qualquer taxa do serviço patrocinador, incluindo criação de conta de token quando necessária. Discriminar estimativa e valor efetivo. O custo total em USDC deve ser aprovado antes de assinar. O Pimlico é o primeiro provedor em avaliação. O app deve verificar compatibilidade com o mint oficial e começar pela devnet, sem misturar chaves e valores de mainnet. [Consulta e composição das taxas do Kora](https://solana.com/developers/docs/tools/kora/operators/fees) · [Patrocínio com cobrança em USDC](https://docs.pimlico.io/guides/how-to/solana/token-sponsorship)
- **Sugestão por ganhos.** Usar lucro líquido de roteamento dos Reports no período e timezone escolhidos como referência para preencher um valor manual. Tratar resultado zero, negativo ou indisponível e descontar conversões já reservadas para o mesmo período. [Métricas de roteamento](../lightningos-light/internal/reports/lnd_metrics.go)

## Controles e distribuição

- App interno no Manager, sem porta pública, com runtime de intermediação do Breez separado em socket Unix, identidade temporária e sem dados ou credenciais do LND. Empacotar a biblioteca e o executável pelo commit revisado e verificar o manifesto e a instalação em Linux amd64. A API do Manager exige login, CSRF, reautenticação para gastos e mudanças sensíveis, limites e auditoria sanitizada. [Protocolo do broker](34_PRIVILEGED_BROKER_PROTOCOL.md) · [Pacote experimental do worker Breez](../tools/breez-bridge-probe/README.md)
- A carteira Solana guarda chaves Ed25519 criptografadas. A integração do assinador deve isolar origem e acesso à rede. Antes de assinar, validar explicitamente o fee payer, o programa SPL Token, o mint, a conta de origem, a conta de destino, o valor, a criação de conta de token e a cobrança aprovada. Rejeitar programas e instruções fora do fluxo, delegações e mudanças de autoridade.
- Persistir o pedido e as provas antes de executar. Vincular sessão, direção, destino, ativo, mínimos, limites, expiração e credencial de consulta. Impor idempotência, limite de total por período e transição de execução exclusiva. Timeout ou reinício mantêm a ordem em reconciliação. Valide a prova de reembolso no LND ou na rede Solana. A expiração da cotação não libera um débito incerto.
- O catálogo deve bloquear novos pedidos quando parado. Continuar a consulta de ordens existentes e preservar o histórico na desinstalação. Cobrir interrupção, atualização e rollback. [Ciclo de upgrades do catálogo](APP_STORE_UPGRADE_LIFECYCLE.md) · [Regressão obrigatória](../AGENTS.md)

## Validação para integração

O navegador real validou criação, recuperação em uma sessão nova e assinatura do depósito após a exibição do custo total. Em Ubuntu amd64 descartável, o LiteSVM executou a transação SPL com duas assinaturas. O patrocinador pagou a rede e a criação da conta do destinatário. Foram debitados 1,075005 USDC de teste, com 1,065005 USDC no destino e 0,010000 USDC no endereço da taxa. A carteira usuária continuou com zero SOL.

O teste sintético ainda precisa ser repetido com um patrocinador real em devnet. A versão de catálogo também depende da integração no Manager e das provas de ida, volta e reembolso em mainnet com autorização de fundos própria, além do lifecycle real da loja em Linux amd64.
