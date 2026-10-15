# Estudo do LayerTree e oportunidades para o LightningOS Light

## Status e decisão

Data da análise: **2026-10-15**.

Estudo técnico e de produto, sem implementação associada. **Recomendação:
acompanhar como pesquisa e reavaliar mediante os critérios abaixo; não priorizar
uma integração mainnet neste momento.**

A [especificação consultada](https://layertree.tech/spec/layertree.md) identifica
Olaoluwa Osuntokun como autor, versão 0.15.2 e status `Draft`, com criação em
2026-09-17. Esta análise não confirma um lançamento comercial da Lightning Labs
nem suporte operacional no LND. As informações descrevem o material disponível
na data da consulta e precisam ser verificadas novamente antes de implementação.

## Proposta técnica

O LayerTree permite que vários participantes compartilhem um único UTXO Bitcoin.
Uma árvore Merkle-sum fora da blockchain registra os participantes e os valores;
o UTXO compromete sua raiz. Covenants verificam provas e obrigam cada transição
a preservar as regras e recriar o saldo compartilhado corretamente.

A proposta permite retirar uma folha em uma transação, apresentando sua prova,
sem depender da assinatura do operador. Essa garantia pressupõe que o
participante disponha dos dados necessários e consiga confirmar a transação.

Na modalidade de **channel leaves**, a folha representa a capacidade de um
canal entre o usuário e uma contraparte, normalmente o operador. Pagamentos
podem passar pelo operador até outros participantes ou a rede Lightning pública.
O funding output do canal só se materializa quando a folha sai da árvore.
Logo, sair da árvore em uma transação não equivale a concluir todo o fechamento
forçado de um canal nessa mesma transação.

Fontes: [visão geral](https://layertree.tech/) e
[pagamentos e channel leaves](https://layertree.tech/components/payment.html).

## Benefícios potenciais para o projeto

As aplicações abaixo são uma avaliação para o LightningOS, não funcionalidades
já integradas ou compromissos de entrega do upstream.

| Área | Benefício potencial | Limite para nossa decisão |
|---|---|---|
| Hub de canais para usuários da comunidade | Atender muitos usuários com canais apoiados em um UTXO compartilhado | Exige infraestrutura de operador e clientes compatíveis |
| Entrada de usuários | Diluir o custo on-chain por meio de entradas em lote | A economia depende da escala e de quem financia os saldos |
| Painel de operação | Administrar árvores, participantes, capacidade, provas, entradas e saídas | Seria um novo subsistema, com persistência e recuperação próprias |
| Autofee, rebalance e ranking | Eventual reutilização de partes da observabilidade e gestão de liquidez | Não foi identificado ganho direto para os algoritmos atuais |
| Canais LND existentes | Possível convivência com o novo serviço | Não foi identificada uma conversão automática dos canais atuais |

O caso mais promissor seria o LightningOS administrar um serviço de canais para
usuários da comunidade. O LayerTree não elimina a necessidade de capital,
liquidez de entrada/saída e gestão dos canais externos do operador.

## Bloqueios e limitações

1. **Mudanças de consenso ainda necessárias.** O próprio projeto informa que
   seus scripts dependem de opcodes não ativados no Bitcoin. Há diferentes
   combinações de requisitos, com capacidades distintas; a variante mínima
   descrita não oferece channel leaves. Atualizar o LND ou instalar um app não
   habilita o protocolo na mainnet.
2. **Disputa pelo UTXO compartilhado.** Cada transição consome e recria o mesmo
   output. Saídas concorrentes, dependências entre transações, política de relay
   e taxas precisam entrar no dimensionamento. A documentação propõe distribuir
   uma implantação entre várias árvores.
3. **Saída individual tem custo.** Compartilhar um UTXO não torna toda operação
   mais barata: uma saída individual carrega uma prova. Os valores divulgados
   são medições/modelos da implementação de referência, não benchmarks de
   produção na mainnet.
4. **Prazos dos canais.** O fechamento forçado precisa primeiro confirmar a
   saída da árvore. A documentação prevê maior margem para HTLCs; essa margem
   precisa ser validada no desenho da integração, sem copiar os parâmetros dos
   nossos canais atuais.
5. **Provas e recuperação.** Verificar os dados antes de financiar uma entrada,
   manter provas utilizáveis e recuperar estados seriam responsabilidades
   essenciais. Não se deve presumir que o SCB do LND cubra esses novos estados.
6. **Maturidade da verificação.** O site descreve provas formais das regras,
   execução de scripts em simulador e comparação da implementação Go. Também
   informa que o modelo P do protocolo off-chain foi projetado, mas ainda não
   construído. Isso não equivale a validação integral do sistema em produção.

Fontes: [opcodes e variantes](https://layertree.tech/components/tiers.html),
[limitações](https://layertree.tech/#limits),
[pagamentos](https://layertree.tech/components/payment.html) e
[escopo da verificação](https://layertree.tech/components/verification.html).

## Relação com a implementação atual

O LightningOS gerencia LND nativo e tem como alvo Bitcoin mainnet. O modelo
LayerTree exigiria integração própria para estados, provas, operação e clientes;
não seria apenas uma nova tela sobre as chamadas de canais existentes.

Já existe abertura de canais em lote no projeto:

- [Rotas do backend](../lightningos-light/internal/server/routes.go):
  `POST /api/lnops/channel/open-batch-preview` e
  `POST /api/lnops/channel/open-batch`.
- [Cliente LND](../lightningos-light/internal/lndclient/client.go):
  `BatchOpenChannel` delega a abertura ao RPC do LND.
- [Estimativa de abertura](../lightningos-light/internal/lndclient/open_channel_preview.go):
  `batchOpenPreviewOutputs` considera um funding output por canal, além do troco
  quando aplicável.
- [Cliente da UI](../lightningos-light/ui/src/api.ts): `openBatchChannels`.

Essa abertura em lote pode diluir custos de uma transação de funding, mas mantém
outputs separados por canal. Ela não implementa o compartilhamento de um único
UTXO proposto pelo LayerTree.

Como contexto de outras alternativas, consultar o
[estudo de ARK](30_ARK_PROTOCOL_STUDY_PT_BR.md). As conclusões daquele documento
têm data própria; este estudo não revalida o estado atual de seus fornecedores.

## Critérios para reavaliar uma integração

- Existência de um caminho concreto de ativação dos requisitos no Bitcoin;
  uso em produção somente após os requisitos estarem efetivamente disponíveis.
- Implementação de operador e clientes com interfaces documentadas e um caminho
  de integração Lightning compatível com nossa stack.
- Evidências de segurança para o protocolo completo, incluindo fluxo off-chain,
  disponibilidade de provas, concorrência e tratamento de falhas.
- Protótipo em ambiente descartável com consenso compatível, sem fundos reais.
  Uma regtest padrão não deve ser presumida suficiente para os novos opcodes.
- Validação de entradas, pagamentos, saída unilateral, fechamento forçado,
  indisponibilidade do operador, reorgs, taxas elevadas e saídas simultâneas.
- Restore em máquina limpa, upgrade e rollback que preservem estados e direitos
  de saída; orçamento de capital e custos operacionais medidos.

Se esses critérios avançarem, elaborar um plano separado para um módulo de
operador, com limites de recursos, API, modelo de persistência, monitoramento e
testes de recuperação. Este estudo não autoriza nem agenda essa implementação.

## Validação deste registro

Análise documental das fontes primárias listadas e conferência das referências
ao código local, realizada em Windows. Não houve alteração de código executável,
instaladores, configuração ou serviços. Não foram executados testes do LayerTree,
simulação econômica ou ensaios de integração/mainnet. As medições e garantias
citadas do upstream não foram reproduzidas por este projeto.
