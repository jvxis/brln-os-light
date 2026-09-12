# LOS Mesh: plano de implementação para 0.5.28

Status: planejamento; as etapas abaixo ainda não foram implementadas.

Referências: [issue #147](https://github.com/jvxis/brln-os-light/issues/147) e [PR #151](https://github.com/jvxis/brln-os-light/pull/151), destinada a `agent/0.5.28-release`.

## Objetivo e organização das PRs

Dar ao operador respostas claras: qual contato está disponível, o que aconteceu com a solicitação, quem deve agir agora e quando há evidência de pagamento/publicação. Consolidar na #151 as melhorias de interface já feitas. Implementar diagnóstico e correlação em duas PRs adicionais para a mesma release, nessa ordem. Isso permite revisar separadamente alterações de protocolo e de estado financeiro.

Já implementado na #151: abas diferenciadas de ações, intenções Pagar/Receber, recebimento on-chain independente de financiamento, destaque progressivo Verificar/Adicionar contato, solicitações acima do formulário, banner temporário e histórico com rolagem. Essas entregas não implicam que os itens deste plano estejam concluídos.

## Constatações no código

- `internal/mesh/nodes.go`: a lista publica nome, nome curto, ID, último registro e origem MQTT; ainda não publica bateria, SNR e saltos por nó.
- `internal/mesh/bridge.go`: há diagnóstico do rádio e correlação de erros de transporte. SNR global não deve ser apresentado como qualidade de todos os contatos.
- `internal/server/mesh_service.go`: sessões persistem estado, total, confirmações e TXID. Tentativas e momento de envio existem em `meshOutbound`, mas ficam em memória. Reinícios interrompem sessões ativas e resultados financeiros em execução podem ficar incertos.
- `internal/server/mesh_handlers.go`: solicitações, invoices e transações são sessões distintas. A ação de preparar na UI preenche um formulário; falta um vínculo explícito e validado entre a solicitação original e sua resposta.
- `ui/src/pages/LOSMesh.tsx`: os formulários compartilham campos entre redes. O banner compara o array completo de pendências; o backend o monta a partir de um mapa, portanto uma mudança de ordem pode causar um aviso sem novidade real.

## Etapa 1 — PR de diagnóstico e acompanhamento

### Métricas por nó e pelo rádio local

1. Confirmar os campos e unidades nos protobufs oficiais Meshtastic, fixando a revisão usada nos testes. Validar amostras USB/TCP dos firmwares disponíveis.
2. Ampliar o parser de `NodeInfo` e telemetria para valores opcionais de bateria, SNR, saltos e instante da observação. Usar ausência explícita, preservando zero como valor válido. Tratar indicadores especiais de alimentação externa e valores inválidos conforme a definição oficial.
3. Separar último registro no rádio, última resposta autenticada do LOS Mesh e origem MQTT. Não apresentar descoberta como prova de conexão ou identidade.
4. Expor utilização de canal/transmissão somente quando fornecida, com origem e data. Não inferir congestionamento usando SNR nem derivar saltos sem campos suficientes.
5. Mostrar um resumo compacto no contato selecionado em Pagamentos: nome/ID, última resposta e permissões relevantes. Oferecer detalhes expansíveis para métricas; manter paginação e busca em Contatos.
6. Definir frescor por tipo de dado a partir da cadência observada e documentá-lo. Dados antigos continuam visíveis com horário e indicação de desatualização; ausência aparece como “Não informado”.

### Sessões e mensagens de próximo passo

1. Acrescentar colunas opcionais/aditivas às sessões: última atualização, contadores de tentativas, blocos entregues ao bridge, blocos confirmados pelo protocolo e último erro com origem/data. Definir claramente se cada contador é por bloco ou sessão.
2. Atualizar esses dados nos eventos de envio, ACK, resposta, expiração e reinício. Limitar retenção e volume de escrita; não salvar payloads, BOLT11 completos, preimages ou chaves.
3. Separar na API e UI os marcos: aceito pelo bridge, confirmação de transporte quando disponível, confirmação do protocolo LOS Mesh, decisão remota e publicação/liquidação. Um ACK do rádio não comprova pagamento.
4. Exibir o próximo passo e seu responsável: “Aguardando o contato enviar a cobrança”, “Revise em Solicitações recebidas”, “Publicada; aguardando confirmação”. Não inventar estimativas de prazo ou declarar rejeição por ausência de ACK.
5. Preservar estados financeiros incertos e orientar consulta à carteira/TXID. A reconexão não deve sobrescrevê-los como sucesso ou falha definitiva.

Arquivos principais: `internal/mesh/nodes.go`, `bridge.go`, `radio.go`, `internal/server/mesh_service.go`, `mesh_handlers.go`, `ui/src/api.ts` e `pages/LOSMesh.tsx`.

## Etapa 2 — PR de correlação e recuperação de fluxos

### Solicitação → resposta → resultado

1. Criar identificador explícito de solicitação original e relação com a sessão de resposta; propagar da seleção de uma pendência para preview, envio de invoice e envio de transação. Associar no servidor ao contato e ao conteúdo efetivamente revisado, sem confiar apenas no ID enviado pela UI.
2. Persistir a relação, tipo de operação, hash da invoice ou TXID e estado de atendimento. Não relacionar operações por coincidência de valor, horário ou descrição. Não vincular retrospectivamente sessões antigas por heurística.
3. Para correlação entre nós, projetar extensão autenticada do protocolo com negociação de capacidade. Invoices e transações hoje usam payloads próprios: não acrescentar JSON ou bytes a eles sem uma codificação versionada. Medir o impacto no limite de blocos.
4. Com pares antigos, manter o fluxo atual e informar que o acompanhamento vinculado não está disponível. Não anunciar compatibilidade usando apenas a versão exibida do app.
5. Distinguir solicitação respondida de pagamento concluído. Ao enviar a resposta, remover a ação repetida “Preparar” e manter acompanhamento. Somente evidência do LND confirma pagamento Lightning; publicação on-chain é diferente de confirmação em bloco. Uma resposta remota deve ser identificada como tal, sem alegar validação local inexistente.
6. Validar peer, tipo, expiração e coerência da resposta; impedir uma resposta duplicada de disparar novo gasto. Preservar a aprovação local e as reservas/proteções existentes de pagamento e publicação.

### Repetição segura

1. Auditar primeiro a deduplicação atual por sessão, hash e TXID, inclusive após reinício e perda de ACK/resultado.
2. Só então oferecer “Retomar transmissão” nos casos suportados, reutilizando a operação e o conteúdo originais, sem financiar, assinar, criar invoice ou pagar novamente. Respeitar limites de tentativas e expiração.
3. Em resultado financeiro incerto, oferecer consulta/reconciliação antes de uma nova ação. Não fornecer um botão genérico que recrie a operação.
4. Definir recuperação após reinício sem persistir segredos/payloads financeiros no histórico. Quando não houver material seguro suficiente para retomar, explicar a limitação e preservar a consulta do resultado.

Arquivos principais: os da etapa 1, mais `internal/mesh/protocol.go`, `pairing.go`, `internal/server/mesh_pairing.go` e os adaptadores LND quando a reconciliação exigir consulta adicional.

## Refinamentos de UI junto às etapas

- Isolar rascunhos de On-chain/Lightning e de cada intenção em memória. Preservar preenchimento ao navegar; cancelar/inutilizar prévias explicitamente ao alterar seus dados. Não persistir invoices ou dados sensíveis no navegador.
- Comparar atualizações por ID e campos semânticos, com ordenação estável das pendências. Evitar banners por reordenação ou passagem do tempo. Não notificar todo o histórico no primeiro carregamento.
- Mostrar contagem de pendências na aba Pagamentos e limitar também a altura da lista de solicitações quando necessário. Não deslocar foco automaticamente durante a leitura.
- Implementar navegação por teclado das abas, foco visível e anúncios acessíveis sem repetição a cada polling. Manter PT-BR/EN e revisão nos temas existentes e em telas pequenas.

## Critérios de validação

- Parser: métricas presentes, ausentes, zero, antigas, inválidas, campos desconhecidos, MQTT e firmwares com informações parciais. Evidência real de USB/TCP além de fixtures.
- Estado: perda, duplicação e reordenação de pacotes/ACKs; resposta sem ACK; falha do rádio; limite de tentativas; expiração; reinício durante envio e durante pagamento/publicação.
- Correlação: solicitações simultâneas do mesmo valor, peers diferentes, resposta expirada, IDs não autorizados e pares sem a nova capacidade. Nenhuma associação por aproximação.
- Financeiro: duplicatas e novas tentativas não geram novo pagamento/publicação nem aprovação implícita. “Respondida”, “publicada” e “confirmada” permanecem estados distintos.
- UI: próximo passo correto em cada lado, rascunhos separados, banner somente para alteração relevante, ação pendente removida ao ser atendida, scroll e teclado, PT-BR/EN e temas.
- API/documentação: atualizar tipos em `api.ts` e contrato em `docs/03_API_SPEC.md`, READMEs, limites de compatibilidade, retenção e recuperação. Testar migração aditiva em instalação existente e retorno ao binário anterior.
- Executar testes Go focados, `go test ./...`, build UI e cenários de navegador com mutações simuladas. Validar Manager, broker e binário `lightningos-mesh` distribuídos pelo upgrade na mesma revisão.
- Após implementação, testar primeiro em LOS-TEST2 e depois em Friendspool conforme o escopo autorizado para o deploy. Testes financeiros reais ficam sob aprovação do operador; não alterar firmware ou configuração dos rádios.

## Fora desta entrega

Fountain codes, compressão e redundância adicional ficam para experimento separado, condicionado a medições de perda, latência, ocupação e custo de retransmissão. Diagnóstico não depende deles. Não incorporar carteira, custódia, controle remoto de gastos ou alterações de configuração do rádio do Blackbox.

Se algum código externo vier a ser reutilizado, verificar licença e preservar atribuições. Este plano se baseia no escopo da issue e no código do LOS; não depende de copiar a implementação do Blackbox.

## Condição para a release

As etapas são candidatas à 0.5.28. Correlação e repetição só entram após os testes de compatibilidade, persistência e duplicação passarem. Se essa validação não fechar, entregar UI/diagnóstico e manter recuperação/correlação pendentes de forma explícita. Não encerrar a #147 até comprovar seus critérios de diagnóstico com dados reais; o experimento de redundância continua separado.


## Implementation progress ? 2026-09-12

Diagnostic code is in PR #153 (draft); correlated workflows, bounded resume and independent UI drafts are in the dependent workflow branch. Automated parser/protocol, PostgreSQL integration and browser validation are in progress/completed as recorded in each PR. These changes have not been deployed. Real-firmware telemetry and two-radio validation remain release gates; #147 remains open. No fountain-code experiment was added.
