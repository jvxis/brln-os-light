# Carteira Solana da Tesouraria

Piloto local para criar e recuperar uma carteira Solana em autocustódia e assinar um depósito de USDC. Use Node.js 22.12 ou superior e um navegador com WebCrypto Ed25519.

```sh
npm ci
npm test
npm run build
npm run preview
```

Abra `http://127.0.0.1:4176/`. Defina uma senha exclusiva de pelo menos 14 caracteres, crie a carteira e baixe o backup criptografado. Selecione o arquivo baixado e digite a senha para verificar a recuperação. A tela exibe então seu endereço público. Guarde o backup e a senha separadamente.

Para voltar de USDC para sats, envie **0,01 SOL pela rede Solana** como saldo inicial sugerido para o mesmo endereço público. Esse saldo cobre a taxa da transação e, quando necessário, a criação da conta USDC de destino. No piloto, cada volta com criação de conta usou 0,001493440 SOL. Confira o total estimado antes de assinar. [Taxas da Solana](https://solana.com/developers/docs/core/fees)

O navegador gera a chave Ed25519 e protege o backup com AES-256-GCM e PBKDF2-SHA256. O servidor local só entrega arquivos estáticos e a página bloqueia conexões externas. A criação e a recuperação funcionam sem consultar a rede Solana.

Para o fluxo com pagamento em SOL, gere a ordem a partir de uma cotação vigente de USDC para Lightning. Passe por stdin a identificação local `id` (32 caracteres hexadecimais), `wallet`, `deposit`, `depositMicros`, `expectedSats`, `lightningAddress`, `expiresAt` em milissegundos e, quando disponível, `providerFeeMicros` a `node prepare-direct-order.mjs`. Informe os valores de USDC e sats como strings nas menores unidades. O comando consulta a rede para verificar saldos, estimar o custo em SOL e gerar um JSON com validade de até 60 segundos.

Com o backup e a senha à mão, importe a ordem e confira o destino Lightning, o total em USDC, os sats esperados e o custo em SOL. O arquivo exportado contém a transação assinada. Valide o arquivo com `verifyDirectDeposit` e simule a transação antes de transmitir. [Plano da Tesouraria](../../docs/STABLECOIN_TREASURY_APP_PLAN.md)

O piloto local de volta também oferece cotação e transmissão pela tela, em `http://127.0.0.1:4177/`. Inicie com `node serve.mjs <configuração-privada.json>`. A configuração do operador contém `worker`, `apiKeyFile`, `submissionFile`, `wallet`, `recipient`, `quoteMicros`, `maxUsdcMicros`, `minSats` e `maxLamports`. Selecione o backup e informe o Lightning Address de retorno. Gere a cotação e confira o endereço e os custos antes de assinar. Editar o endereço exige outra cotação. Na confirmação, o serviço consulta um blockhash recente para a mesma cotação. Se os custos mudarem, a tela exige nova confirmação. A senha e o backup ficam no navegador. A assinatura para reconciliação é gravada em `submissionFile` antes da transmissão. A assinatura permanece vinculada a essa ordem até que a entrega, o reembolso ou uma falha definitiva do depósito seja verificada. Depois, **Nova conversão** arquiva a submissão e mantém o último endereço com entrega confirmada pela ponte como sugestão editável. Por padrão, a sugestão é mantida em um arquivo privado ao lado de submissionFile, vinculada à carteira Solana. O parâmetro recipientFile permite indicar um caminho privado estável para essa preferência.

O fluxo experimental com patrocinador continua disponível para ordens de teste. Nelas, a carteira exibe a taxa do patrocinador em USDC e exporta uma transação que requer a segunda assinatura.

Ao reabrir o assinador, uma submissão gravada mostra o acompanhamento da ordem. O painel distingue confirmação do depósito, entrega informada pela ponte e reembolso comprovado na Solana. A ordem local continua em conciliação durante uma indisponibilidade de consulta. Em novembro de 2026, duas voltas para o mesmo Lightning Address liquidaram no LND em 3 min 15 s e 1 h 8 min 55 s após os depósitos. A interface avisa que a entrega pode levar cerca de uma hora ou mais e orienta a aguardar o resultado da ordem existente. Um piloto independente na porta 4178 pode ser iniciado com `parallel: true` e um `submissionFile` próprio.

Para repetir a prova contábil em Linux amd64, instale a dependência de teste e execute o simulador. O teste confirma a criação da conta de destino, o crédito do depósito, a cobrança exata da taxa e o saldo original de zero SOL na carteira usuária.

```sh
npm install --no-save --package-lock=false litesvm@1.6.1
node verify-ledger.mjs
```
