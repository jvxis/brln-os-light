# Tesouraria LightningOS

Interface local do piloto para criar uma carteira em autocustódia e recuperar seu endereço público para receber USDC na Base Mainnet. A interface libera o endereço de uma carteira nova depois que o operador baixa e reabre o backup criptografado. A mesma recuperação funciona em uma sessão nova do navegador.

## Executar o piloto

Neste diretório, use Node.js 22.12 ou superior.

```text
npm ci
npm test
npm run build
npm run preview
```

Abra `http://127.0.0.1:4175/` no navegador deste computador. Crie a carteira com uma senha exclusiva, baixe o arquivo JSON e selecione o próprio arquivo baixado para confirmar a recuperação. Compartilhe apenas o endereço público com quem enviará o USDC. O arquivo e a senha permitem recuperar a chave. Guarde-os separadamente.

O piloto serve os arquivos a partir de uma origem local, com política de conteúdo que bloqueia conexões da página. Gera a chave no navegador, usa o formato de keystore V3 com scrypt e guarda o backup somente no arquivo que o operador baixa. O app trata apenas endereço público e backup criptografado na UI. Nenhuma etapa depende da API do Manager ou do Breez. A interface ainda não oferece assinatura de transações ou consulta de saldo. [Keystores do ethers](https://docs.ethers.org/v6/api/wallet/#api-wallet-json) · [Base Mainnet](https://docs.base.org/base-chain/api-reference/ethereum-json-rpc-api/eth_chainId)

[Plano da Tesouraria](../../docs/STABLECOIN_TREASURY_APP_PLAN.md)
