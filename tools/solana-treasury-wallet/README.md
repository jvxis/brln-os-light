# Carteira Solana da Tesouraria

Piloto local para criar e recuperar uma carteira Solana em autocustódia e assinar um depósito de USDC. Use Node.js 22.12 ou superior e um navegador com WebCrypto Ed25519.

```sh
npm ci
npm test
npm run build
npm run preview
```

Abra `http://127.0.0.1:4176/`. Defina uma senha exclusiva de pelo menos 14 caracteres, crie a carteira e baixe o backup criptografado. Selecione o arquivo baixado e digite a senha para verificar a recuperação. A tela exibe então seu endereço público. Guarde o backup e a senha separadamente.

O navegador gera a chave Ed25519 e protege o backup com AES-256-GCM e PBKDF2-SHA256. O servidor local só entrega arquivos estáticos e a página bloqueia conexões externas. A criação e a recuperação funcionam sem consultar a rede Solana.

Para o fluxo com pagamento em SOL, gere a ordem a partir de uma cotação vigente de USDC para Lightning. Passe por stdin a identificação local `id` (32 caracteres hexadecimais), `wallet`, `deposit`, `depositMicros`, `expectedSats`, `lightningAddress`, `expiresAt` em milissegundos e, quando disponível, `providerFeeMicros` a `node prepare-direct-order.mjs`. Informe os valores de USDC e sats como strings nas menores unidades. O comando consulta a rede para verificar saldos, estimar o custo em SOL e gerar um JSON com validade de até 60 segundos.

Com o backup e a senha à mão, importe a ordem e confira o destino Lightning, o total em USDC, os sats esperados e o custo em SOL. O arquivo exportado contém a transação assinada. Valide o arquivo com `verifyDirectDeposit` e simule a transação antes de transmitir. [Plano da Tesouraria](../../docs/STABLECOIN_TREASURY_APP_PLAN.md)

O fluxo experimental com patrocinador continua disponível para ordens de teste. Nelas, a carteira exibe a taxa do patrocinador em USDC e exporta uma transação que requer a segunda assinatura.

Para repetir a prova contábil em Linux amd64, instale a dependência de teste e execute o simulador. O teste confirma a criação da conta de destino, o crédito do depósito, a cobrança exata da taxa e o saldo original de zero SOL na carteira usuária.

```sh
npm install --no-save --package-lock=false litesvm@1.6.1
node verify-ledger.mjs
```
