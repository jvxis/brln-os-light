# Estudo do protocolo ARK e oportunidades para o LightningOS Light

## Status

Estudo técnico e de produto. **Concluído para orientar planejamento; não há
implementação associada.**

Data de verificação: 2026-07-10.

## Conclusão executiva

É possível integrar ARK ao LightningOS Light e oferecer pagamentos entre ARK,
Lightning e Bitcoin on-chain. Entretanto, é importante separar três produtos
com riscos e maturidades diferentes:

1. **Carteira ARK autocustodial:** viável agora usando Bark Wallet e o operador
   mainnet da Second.
2. **Backend de pagamentos ARK/Lightning:** viável com `barkd`, REST e,
   futuramente, Nostr Wallet Connect (NWC).
3. **Operador ARK público com liquidez própria:** tecnicamente possível, mas
   ainda não recomendado como instalação mainnet de um clique.

A recomendação é começar com uma **Bark Wallet** na Loja de Apps e manter o
trabalho de operador em um ambiente separado de pesquisa/signet. ARK deve ser
tratado como complementar à Lightning: simplifica a autocustódia para o usuário
e usa Lightning como rede de interoperabilidade global.

## O que é ARK

ARK é uma segunda camada do Bitcoin baseada em uma arquitetura cliente-servidor
autocustodial. O usuário não precisa abrir canais, selecionar peers, comprar
inbound liquidity ou rebalancear canais. Ele conecta sua carteira a um operador
ARK, que coordena as transações e fornece a liquidez necessária para determinadas
operações.

O saldo do usuário é representado por **VTXOs (virtual UTXOs)**. Um VTXO é um
direito sobre uma saída Bitcoin compartilhada, protegido por uma árvore de
transações pré-assinadas. Em condições normais essas transações permanecem
off-chain. Se o operador deixar de cooperar, o usuário pode publicar seu ramo da
árvore e recuperar os bitcoins on-chain.

O protocolo funciona no Bitcoin atual, sem soft fork ou novos opcodes.

### Fluxo simplificado

1. O operador cria on-chain um batch/round output que agrega muitos usuários.
2. Cada usuário recebe VTXOs correspondentes ao seu saldo.
3. Pagamentos dentro do mesmo operador acontecem off-chain e quase
   instantaneamente.
4. VTXOs são periodicamente gastos ou renovados em novos rounds/batches.
5. Um usuário pode sair cooperativamente para uma saída on-chain.
6. Se o operador estiver indisponível ou não cooperar, o usuário pode realizar
   uma saída unilateral usando as transações pré-assinadas.

### Terminologia essencial

- **Operador/Ark server:** servidor que coordena pagamentos, rounds, batches,
  renovações e saídas e que fornece liquidez quando necessário.
- **VTXO:** UTXO virtual pertencente ao usuário.
- **Boarding:** entrada de fundos on-chain em um domínio ARK.
- **Offboarding:** retirada cooperativa de ARK para Bitcoin on-chain.
- **Round/batch:** agregação de operações em uma única transação Bitcoin.
- **Refresh:** substituição de VTXOs por novos VTXOs com prazo renovado.
- **Arkoor/out-of-round:** pagamento instantâneo fora de um round.
- **Emergency/unilateral exit:** publicação on-chain do ramo de transações que
  protege um VTXO.

## Implementações atuais

ARK não é hoje uma única stack plenamente intercambiável. Existem duas famílias
principais de implementação.

### Second/Bark

A implementação da Second é formada principalmente por:

- `bark`: carteira e bibliotecas;
- `barkd`: carteira daemon com API REST/OpenAPI;
- `captaind`: servidor/operador ARK;
- `watchmand`: acompanhamento e proteção dos estados on-chain.

A Second lançou seu serviço Bark em mainnet em 2026-06-09 e mantém um operador
público em `https://ark.second.tech`. A carteira já consegue enviar e receber por
ARK, Lightning e on-chain a partir de um único saldo.

Isso **não significa** que qualquer instalação independente de `captaind` esteja
pronta para produção. A documentação de operação própria ainda descreve o fluxo
como ambiente de testes e alerta contra uso independente em produção.

### Arkade/arkd

Arkade usa os mesmos pilares gerais — VTXOs, batches e saídas unilaterais — mas
adiciona uma proposta de Virtual Mempool, contratos programáveis, assets e signer
isolado. O repositório `arkd` suporta mainnet, mas continua marcado oficialmente
como software alpha e não recomendado para produção.

### Interoperabilidade entre implementações

Não se deve assumir que endereços, servidores, formatos ou versões de Bark e
Arkade sejam intercambiáveis. O Bark 0.3.0 passou a rejeitar explicitamente
endereços Arkade. Para o LightningOS, cada implementação precisa ser tratada
como um provider separado.

Na primeira integração, a identificação deve ser explícita: **“Bark Wallet — ARK
via Second”**, e não apenas “ARK Wallet”.

## Expiração, renovação e liveness

VTXOs não são permanentes. Cada batch possui uma expiração. Antes dela, o VTXO
precisa ser gasto, renovado ou retirado. Após o vencimento, o operador passa a
ter um caminho para varrer o batch output.

Uma carteira bem construída automatiza a renovação, mas isso cria requisitos de
liveness:

- o operador precisa estar disponível para novos pagamentos e refreshes;
- a carteira, um daemon ou um delegate precisa agir antes da expiração;
- o usuário precisa manter material de recuperação suficiente para uma saída;
- dispositivos móveis podem precisar de renovação delegada porque não ficam
  ativos de forma confiável.

Se o operador ficar offline, novos pagamentos deixam de funcionar. Os VTXOs
existentes continuam protegidos pela saída unilateral enquanto seus prazos e
transações de recuperação permanecerem válidos.

## Modelo de segurança

ARK é autocustodial, mas seu modelo não é idêntico ao da Lightning.

### Garantias

- O operador não deve conseguir gastar unilateralmente um VTXO válido antes do
  caminho de expiração aplicável.
- Cada usuário possui transações pré-assinadas que permitem a saída on-chain.
- Um pagamento pode ser realizado sem entregar ao operador a custódia ordinária
  dos fundos do usuário.
- Misbehavior pode produzir evidência criptográfica, dependendo da variante.

### Confiança temporária em pagamentos out-of-round

No desenho clássico, um VTXO recebido out-of-round assume que remetente e
operador não cooperarão para realizar um double-spend. O recebedor pode fazer um
refresh para levar o saldo a um novo batch e restaurar um modelo mais forte.

Arkade procura reduzir essa superfície com signer separado, TEE, remote
attestation e comunicação criptografada. Isso reduz poderes diretos do operador,
mas acrescenta dependência no hardware e no software do enclave.

### Saída unilateral e mass exit

Uma saída ARK pode exigir várias transações, uma por nível da árvore. O usuário
paga as taxas on-chain de cada etapa. Cadeias mais profundas e feerates altos
podem tornar a saída de pequenos VTXOs antieconômica.

Um mass exit combina dois riscos:

- muitos usuários competindo pelo espaço de bloco ao mesmo tempo;
- pequenos VTXOs cujo custo de recuperação se aproxima ou supera o saldo.

Portanto, a interface precisa mostrar prazo, profundidade e custo estimado de
saída, e não apenas o saldo agregado.

### Backup

Não se deve presumir que somente a seed seja suficiente para todos os cenários
de recuperação off-chain. O produto precisa preservar e testar:

- mnemonic/seed;
- banco e estado da carteira;
- transações pré-assinadas e metadados de VTXOs;
- estado de exits em andamento;
- compatibilidade do backup com a versão instalada.

## Liquidez e economia do operador

ARK elimina a gestão de liquidez **para o usuário**, mas não elimina a necessidade
de liquidez do sistema. Ela é transferida ao operador.

O operador precisa fornecer BTC antecipadamente em operações como:

- refresh de VTXOs;
- offboarding;
- pagamentos Lightning;
- criação de novos batches.

Ao renovar um VTXO, o operador financia imediatamente o novo VTXO, mas pode
precisar aguardar o vencimento do VTXO antigo para recuperar o capital. Isso cria
uma tesouraria temporariamente imobilizada.

Pagamentos ARK dentro do mesmo operador normalmente não exigem nova liquidez;
eles estendem ou substituem estados off-chain existentes.

### Custos que as taxas precisam cobrir

- custo de oportunidade da tesouraria em BTC;
- taxas de mineração dos batches, refreshes e saídas cooperativas;
- routing fees da Lightning;
- canais e liquidez do gateway Lightning;
- alta disponibilidade, armazenamento, backups e monitoramento;
- desenvolvimento, suporte e incident response;
- risco de feerate e picos de demanda.

Cada operador pode definir sua própria política. Ela pode refletir diretamente o
custo de cada operação ou usar subsídios cruzados para oferecer preços mais
previsíveis.

## Comparação ARK versus Lightning

| Critério | ARK | Lightning | Consequência prática |
|---|---|---|---|
| Estrutura | Clientes conectados a um operador | Rede P2P de canais | ARK simplifica o cliente; Lightning distribui o domínio de falha |
| Onboarding | Pode receber sem abrir canal | Autocustódia normalmente exige canal/inbound ou um LSP | ARK oferece UX inicial mais simples |
| Liquidez do usuário | Sem canais para administrar | Inbound, outbound, peers e rebalanceamento | ARK transfere a operação para o servidor |
| Liquidez do sistema | Tesouraria do operador | Distribuída em canais | O operador ARK concentra capital e risco |
| Pagamentos internos | Simples e sem nova liquidez | Dependem de uma rota com saldo | ARK é eficiente dentro do mesmo domínio |
| Alcance global | Normalmente por uma ponte Lightning | Nativo na rede de canais | Lightning continua sendo a rede de interoperabilidade |
| Disponibilidade | Operador único é o domínio de falha | Pode tentar caminhos alternativos | Downtime do operador ARK interrompe pagamentos |
| Expiração | VTXOs precisam ser renovados | Canais não possuem refresh periódico equivalente | ARK exige liveness e automação |
| Saída unilateral | Pode exigir uma cadeia de transações | Force close e sweeps do canal | Ambos dependem do espaço de bloco em falhas |
| Privacidade | Operador coordena atividade; varia por implementação | Onion routing oculta a rota dos intermediários | Lightning possui melhor descentralização de observação |
| Finalidade | Pagamento instantâneo pode ser uma preconfirmação até refresh/batch | HTLC/commitments atualizam o canal atomicamente | A UX “instantânea” esconde garantias diferentes |
| Precisão | Geralmente sats/VTXOs | Millisatoshis internamente | Lightning é mais natural para microvalores abaixo de 1 sat |
| Escala | Batching eficiente, operador pode ser gargalo | Processamento distribuído | ARK ganha eficiência on-chain; Lightning distribui throughput |
| Maturidade | Implementações 0.x e mudanças incompatíveis | Ecossistema BOLT consolidado | ARK exige upgrades e testes mais conservadores |

## Integração ARK e Lightning

### Usar a ponte de um operador existente

Fluxo inicial recomendado:

```text
LightningOS -> Bark Wallet -> operador Second -> gateway Lightning -> Lightning Network
```

O usuário paga e recebe Lightning a partir do saldo ARK sem utilizar o LND local
e sem criar novos canais. É a opção mais simples para a primeira versão.

Ela não transforma o LightningOS em operador ARK e não gera receita de bridge
para o dono do nó.

### Arkade com swaps Boltz

Arkade documenta integração ARK-Lightning por submarine swaps via Boltz. Esse
modelo oferece atomicidade de swap, mas depende da disponibilidade, liquidez e
taxas do provedor.

### Operador próprio ligado ao LND

A stack de referência atual do `captaind` usa Core Lightning com o hold plugin
da Boltz, e não LND. Portanto, o LND nativo do LightningOS não é um backend
drop-in.

Um adaptador LND é tecnicamente possível usando, entre outros:

- `AddHoldInvoice`;
- `SettleInvoice`;
- `CancelInvoice`;
- subscriptions de invoices;
- `SendPaymentV2` e tracking de pagamentos.

Esse adaptador seria um componente financeiro crítico. Ele precisaria garantir:

- atomicidade VTXO-HTLC;
- persistência antes de revelar preimages;
- idempotência e proteção contra replay;
- recuperação após crash em qualquer transição;
- limites e margens de CLTV;
- tratamento de MPP e pagamentos parcialmente em voo;
- reconciliação entre banco ARK e estado do LND.

## Casos de uso para a Loja de Apps

| Produto | Proposta | Maturidade recomendada |
|---|---|---|
| Bark Wallet | Carteira web ARK, Lightning e on-chain conectada à Second | Implementar primeiro, marcada como Beta |
| ARK Payment Backend | `barkd` protegido para lojas, bots e automações | Curto prazo |
| ARK/NWC Gateway | Expor um saldo ARK para apps compatíveis com NWC | Curto/médio prazo |
| Checkout universal | QR/BIP-321 com ARK, Lightning e on-chain | Curto/médio prazo |
| ARK Delegate | Renovação automática para carteiras móveis/offline | Médio prazo |
| ARK Operator Lab | `captaind`, PostgreSQL e Bitcoin Core em signet/regtest | Pode ser oferecido para desenvolvimento |
| Painel de operador | Tesouraria, batches, vencimentos, receitas e custos | Desenvolver primeiro em signet |
| ARK-LND Gateway | Adaptador atômico entre `captaind` e LND | Pesquisa e testes extensivos |
| Operador ARK público | Serviço mainnet com liquidez própria | Somente após maturação e auditoria |
| Arkade Developer Kit | Contratos, assets e swaps Arkade | Experimental |

## Oportunidades de produto e receita

- Carteira autocustodial para usuários que não querem administrar canais.
- Backend Lightning para comerciantes sem um node por estabelecimento.
- Wallet-as-a-service autocustodial via NWC.
- Infraestrutura white-label para carteiras, lojas e fintechs Bitcoin.
- Operador regional com cobrança por pagamentos, refreshes e offboards.
- Liquidity-as-a-service para aplicações ARK.
- Bridge entre usuários ARK e a Lightning Network.

## Recomendação para o LightningOS Light

Ordem recomendada:

1. Bark Wallet Beta, identificada como “Bark/ARK via Second”.
2. Backend local `barkd` e futura integração NWC.
3. ARK Operator Lab restrito a signet/regtest.
4. Pesquisa e validação de um adaptador ARK-LND.
5. Operador mainnet apenas quando houver orientação upstream de produção,
   auditorias, recuperação comprovada, política de upgrades e testes de mass
   exit.

Critérios mínimos antes de oferecer um operador público:

- upstream remover o alerta de uso apenas para testes;
- auditoria de segurança e processo de disclosure/bug bounty;
- upgrade e rollback sem perda de VTXOs;
- restore completo testado em máquina limpa;
- controles e circuit breakers de tesouraria;
- simulação de mass exit em feerates elevados;
- gateway Lightning com estados persistentes e reconciliáveis;
- análise jurídica e operacional específica da jurisdição.

## Referências primárias

- [Ark Protocol — visão geral e implementações](https://ark-protocol.org/)
- [Second — Bark mainnet](https://blog.second.tech/bark-now-on-bitcoin-mainnet/)
- [Second — Bark no Alby Hub](https://blog.second.tech/bark-powers-alby-hub/)
- [Second — introdução ao protocolo](https://docs.second.tech/ark-protocol/intro/)
- [Second — liquidez](https://docs.second.tech/ark-protocol/liquidity/)
- [Second — taxas](https://docs.second.tech/ark-protocol/fees/)
- [Second — operação de um Ark server](https://second.tech/docs/ark-server)
- [Bark — código e releases](https://gitlab.com/ark-bitcoin/bark)
- [Arkade — documentação](https://docs.arkadeos.com/)
- [Arkade — segurança e trust model](https://docs.arkadeos.com/learn/core-concepts/security-and-trust-model)
- [Arkade — ciclo de vida de VTXOs](https://docs.arkadeos.com/learn/core-concepts/vtxo-lifecycle-and-liveness)
- [Arkade — Lightning swaps via Boltz](https://docs.arkadeos.com/contracts/lightning-swaps)
- [arkd — implementação do operador Arkade](https://github.com/arkade-os/arkd)
- [Lightning Labs — visão geral da Lightning](https://docs.lightning.engineering/the-lightning-network/overview)
- [Lightning Labs — gestão de liquidez](https://docs.lightning.engineering/lightning-network-tools/lightning-terminal/channel-liquidity)
- [Lightning Labs — pagamentos e pathfinding](https://docs.lightning.engineering/lightning-network-tools/lnd/payments)
- [LND — AddHoldInvoice](https://lightning.engineering/api-docs/api/lnd/invoices/add-hold-invoice/)
- [LND — SendPaymentV2](https://lightning.engineering/api-docs/api/lnd/router/send-payment-v2/)

