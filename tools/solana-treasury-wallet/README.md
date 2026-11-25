# Carteira Solana da Tesouraria

Piloto local para criar e recuperar uma carteira Solana em autocustódia e assinar uma transferência com patrocinador. Use Node.js 22 ou superior e um navegador com WebCrypto Ed25519.

```sh
npm ci
npm test
npm run build
npm run preview
```

Abra `http://127.0.0.1:4176/`. Defina uma senha exclusiva de pelo menos 14 caracteres, crie a carteira e baixe o backup criptografado. Selecione o arquivo baixado e digite a senha para verificar a recuperação. A tela exibe então seu endereço público. Guarde o backup e a senha separadamente.

O navegador gera a chave Ed25519 e protege o backup com AES-256-GCM e PBKDF2-SHA256. O servidor local só entrega arquivos estáticos e a página bloqueia conexões externas. A criação e a recuperação funcionam sem consultar a rede Solana.

Para testar a assinatura, importe uma ordem sintética e confira o depósito, a taxa do patrocinador e o total em USDC. A carteira assina apenas as instruções necessárias ao envio aprovado. O arquivo exportado fica pendente da assinatura do patrocinador para transmitir na Solana. Use chaves de teste enquanto a transmissão e a liquidação estiverem em validação. [Plano da Tesouraria](../../docs/STABLECOIN_TREASURY_APP_PLAN.md)
