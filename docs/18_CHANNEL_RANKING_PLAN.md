# Channel Ranking Plan

## Status

Implemented.

This document remains as the original design record for the feature.

Este documento define a proposta para um novo modulo de analise chamado `Channel Ranking` / `Ranking de Canais`.

O objetivo e transformar sinais operacionais dispersos em uma recomendacao clara por canal:

- `Expandir`
- `Manter`
- `Monitorar`
- `Fechar`

O modulo deve ajudar o operador a responder, com rapidez:

- este canal gera retorno liquido?
- este peer vale mais capital?
- este canal esta exigindo manutencao demais?
- devo manter, observar, expandir ou preparar fechamento?

## Motivacao

Hoje o projeto ja possui varias pecas soltas do quebra-cabeca:

- `Lightning Ops > Canais` ja exibe metricas economicas 7d por canal:
  - `forward_fee_7d_sat`
  - `rebal_fee_7d_sat`
  - `profit_fee_7d_sat`
  - `out_ppm7d`
  - `rebal_ppm7d`
- `autofee_state` ja classifica canais e guarda contexto de politica.
- `notifications` ja permite medir receita, rebalances e parte da atividade.
- `ListChannels` ja traz:
  - saldo local/remoto
  - pending HTLCs
  - uptime/inatividade recente
  - fees locais e do peer
- O sistema ja tem modulos operacionais correlatos:
  - `Autofee`
  - `Rebalance`
  - `HTLC Manager`
  - `Close Manager`

O problema e que esses dados ainda nao viram uma resposta operacional consolidada.

Hoje o operador precisa inferir manualmente:

- se um canal ruim esta so "novo" ou de fato ineficiente;
- se o problema esta em fee, liquidez, peer ou custo de rebalance;
- quando vale observar mais versus quando vale preparar fechamento.

## Objetivos

- Criar uma tela propria para ranking e analise comparativa de canais.
- Mostrar um score unico por canal, mas com justificativa auditavel.
- Gerar recomendacoes concretas por estado.
- Reaproveitar ao maximo a telemetria ja existente.
- Integrar o modulo com `Lightning Ops`, sem poluir os cards principais.

## Nao objetivos

- Nao substituir o julgamento do operador por automacao opaca.
- Nao executar acoes automaticas de close, rebalance ou fee change no MVP.
- Nao construir um modelo estatistico sofisticado antes de validar o fluxo basico.
- Nao transformar o card de canal do `Lightning Ops` em uma tela analitica completa.

## Posicionamento no produto

### Navegacao

Criar um novo menu logo abaixo de `Lightning Ops`:

- `Channel Ranking` em EN
- `Ranking de Canais` em PT-BR

O `Lightning Ops > Canais` continua sendo a tela operacional do dia a dia.

O novo modulo passa a ser a tela de analise e priorizacao.

### Relacao com os cards de canais

Nos cards atuais de `Lightning Ops > Canais`, mostrar apenas um indicativo leve:

- `Score 74`
- `Expandir`
- ou `Monitorar`

Ao clicar no indicativo:

- abrir o detalhe daquele canal na tela `Ranking de Canais`

Isso preserva a leveza da tela atual e evita UI poluida.

## Modelo mental do modulo

O modulo deve separar tres coisas:

1. `Score`
- um numero para ordenar e comparar canais

2. `Estado`
- a classificacao operacional:
  - `Expandir`
  - `Manter`
  - `Monitorar`
  - `Fechar`

3. `Recomendacoes`
- acoes concretas sugeridas para aquele canal

Exemplo:

- `Score: 71`
- `Estado: Monitorar`
- `Motivos: lucro liquido fraco em 30d, rebalance caro, utilizacao irregular`
- `Recomendacoes: reduzir insistencia em rebalance, revisar autofee, observar mais 7 dias`

## Por que usar `Monitorar` em vez de `Drenar`

`Drenar` e um conceito tecnico valido, mas ruim como rotulo principal de produto.

Na UI, `Monitorar` comunica melhor:

- existe problema ou ineficiencia;
- ainda nao ha evidencia suficiente para recomendar fechamento imediato;
- o operador deve revisar politica, liquidez e tendencia.

Internamente, o motor pode continuar usando submotivos mais precisos, por exemplo:

- `monitor_liquidity`
- `monitor_autofee`
- `monitor_peer`
- `monitor_profitability`

## Estados e significado operacional

### Expandir

Significa:

- canal saudavel e comprovadamente produtivo;
- uso alto e/ou recorrente;
- retorno liquido bom;
- peer estavel;
- capacidade atual possivelmente limitando crescimento.

Recomendacoes tipicas:

- considerar abrir canal complementar ou fazer splice-in;
- preservar prioridade de rebalance;
- manter autofee ativo sem endurecer demais;
- avaliar se existe saturacao recorrente de um dos lados.

### Manter

Significa:

- canal operacionalmente saudavel;
- retorno aceitavel;
- sem sinal claro de subcapitalizacao;
- sem sinal claro de deterioracao persistente.

Recomendacoes tipicas:

- manter politica atual;
- revisar apenas se houver piora de tendencia;
- manter rebalance e autofee dentro do padrao normal.

### Monitorar

Significa:

- o canal mostra ineficiencia, instabilidade ou retorno fraco;
- mas ainda nao ha justificativa suficiente para fechamento imediato.

Recomendacoes tipicas dependem da causa:

- `rebalance_cost_high`
  - reduzir prioridade de rebalance;
  - revisar teto de custo;
  - parar de insistir em rebalance artificial.
- `autofee_misaligned`
  - revisar floor, ceiling, step size e ultima direcao do autofee.
- `capital_idle`
  - testar fee maior;
  - observar se o canal reage sem novas injecoes de liquidez.
- `peer_unstable`
  - observar uptime e desconexoes antes de decidir fechar.
- `channel_too_new`
  - aguardar janela minima antes de penalizar.
- `htlc_failures_elevated`
  - revisar se o problema e liquidez, policy ou qualidade do peer.

### Fechar

Significa:

- o canal apresenta retorno persistentemente ruim;
- ou risco operacional alto;
- ou custo de oportunidade evidente;
- sem sinais de recuperacao via ajustes normais.

Recomendacoes tipicas:

- parar de insistir em rebalance;
- endurecer fees se fizer sentido;
- marcar como candidato a close;
- abrir o detalhe operacional no `Close Manager` quando o fechamento comecar.

## Fontes de dados existentes

O MVP deve se apoiar no que ja existe hoje.

### Ja disponivel

1. `GET /api/lnops/channels`
- saldo local/remoto
- capacidade
- pending HTLC count
- activity flag
- inactive duration
- fee local e fee do peer
- class label do autofee
- receita 7d, rebalance 7d e lucro 7d por canal

2. `notifications`
- forwards por canal
- rebalance por canal
- timestamps de atividade

3. `autofee_state`
- classificacao e contexto de politica

4. `rebalance` database
- custos e frequencia de rebalance

5. `closed channels`
- para futura retroalimentacao de historico, mas nao necessario no MVP

### Parcialmente disponivel

1. `peer stability`
- ha sinal de atividade e inatividade recente no canal
- ha lista de peers conectados, ping e bytes
- mas ainda nao existe um score persistido de estabilidade por peer ao longo de janelas maiores

2. `HTLC failures`
- o projeto ja tem `HTLC Manager`, mas ainda nao ha um agregado simples por canal pronto para ranking

### Telemetria nova recomendada

Para fases posteriores:

- agregados 7d/30d de falhas HTLC por canal
- taxa de reacao do canal a mudancas de fee
- contagem de rebalance attempts falhados por canal
- score de estabilidade do peer em 30d

## Metricas do MVP

O MVP deve evitar complexidade excessiva e usar metricas defensaveis.

### Janela principal

Usar duas janelas:

- `7d` para operacional recente
- `30d` para decisao mais estavel

Se 30d nao estiver disponivel em uma primeira entrega, iniciar com:

- `7d`
- `channel_age_days`

e evoluir para `30d` na fase seguinte.

### Metrica 1: retorno liquido

Formula inicial:

- `net_fee_sat = forward_fee_sat - rebalance_fee_sat`

No MVP, nao ratear custo on-chain por canal.

Motivo:

- ja existe dado robusto;
- e o componente mais legivel para o operador.

### Metrica 2: eficiencia do capital

Formula inicial:

- `capital_efficiency = net_fee_sat / capacity_sat`

Normalizada por faixa para score.

Objetivo:

- evitar favorecer canais grandes so por volume absoluto.

### Metrica 3: utilizacao

Usar proxies simples:

- volume encaminhado 7d relativo a capacidade
- e/ou `pending_htlc_count`
- e/ou saldo recorrente muito concentrado em um lado

Proxy inicial do MVP:

- `local_balance_pct`
- `remote_balance_pct`
- `forwarded_volume_vs_capacity`

### Metrica 4: custo de manutencao

Formula inicial:

- `rebalance_fee_sat`
- `rebalance_fee_ppm`

Objetivo:

- penalizar canais que so "parecem bons" porque precisam de muito custo operacional para continuar vivos.

### Metrica 5: saude operacional

Sinais do MVP:

- `active`
- `inactive_duration_sec`
- `pending_htlc_count`

Objetivo:

- nao premiar demais canal rentavel, mas operacionalmente instavel.

### Metrica 6: maturidade do canal

Sinal:

- idade do canal a partir da abertura se o dado existir, ou fallback por heuristica posterior

No MVP:

- se nao houver idade exata confiavel no backend, aplicar apenas uma protecao simples:
  - canais sem historico economico suficiente nao caem direto em `Fechar`

## Score proposto

Usar score de `0 a 100`.

### Componentes

- `35%` retorno liquido
- `20%` eficiencia do capital
- `15%` utilizacao
- `15%` custo de rebalance
- `10%` saude operacional
- `5%` maturidade/confianca

### Observacao importante

O `estado` nao deve depender apenas do score final.

Exemplo:

- um canal pode ter score medio, mas uptime ruim e falhas elevadas;
- nesse caso a recomendacao pode ir para `Monitorar` ou `Fechar` mesmo sem score baixissimo.

Portanto:

- `score` serve para ordenar;
- `estado` vem de regras de decisao.

## Motor de recomendacao

### Regras de decisao do MVP

#### Expandir

Quando a maioria destas condicoes for verdadeira:

- lucro liquido positivo e forte em 7d
- eficiencia de capital acima da mediana
- canal ativo
- custo de rebalance sob controle
- liquidez frequentemente encostando em extremos operacionais

#### Manter

Quando:

- lucro liquido positivo ou neutro aceitavel
- sem custo excessivo de rebalance
- peer/canal estavel
- sem sinais fortes de oportunidade ou deterioracao

#### Monitorar

Quando houver pelo menos um destes grupos:

- lucro baixo ou inconsistente
- rebalance caro
- canal pouco usado
- atividade oscilante
- liquidez mal posicionada
- politica de fee possivelmente desalinhada

#### Fechar

Quando houver combinacao persistente de:

- lucro liquido muito ruim
- baixa utilizacao
- custo alto de rebalance
- peer/canal instavel
- sem sinal de melhora

No MVP, `Fechar` deve exigir threshold mais conservador que `Monitorar`.

## Recomendacoes automaticas por causa

Cada canal pode ter de 1 a 3 `recommendation items`.

Formato sugerido:

- `code`
- `title`
- `reason`
- `target_module`
- `action_hint`

### Exemplos

#### Para `Monitorar`

- `review_autofee_bounds`
  - revisar floor/ceiling do autofee deste canal
- `reduce_rebalance_priority`
  - custo de rebalance alto para o retorno atual
- `observe_7d_before_close`
  - canal fraco, mas ainda sem historico suficiente para fechar
- `check_peer_stability`
  - peer com inatividade recorrente

#### Para `Expandir`

- `consider_splice_in`
  - canal rentavel e possivelmente limitado por capacidade
- `preserve_rebalance_priority`
  - canal converte liquidez em receita
- `keep_autofee_active`
  - politica atual parece funcionar

#### Para `Fechar`

- `stop_nonessential_rebalances`
  - nao continuar sustentando custo artificial
- `prepare_coop_close`
  - canal candidato a desmobilizacao
- `review_with_close_manager`
  - usar `Close Manager` quando o fluxo de fechamento iniciar

## UX proposta

### Tela principal do modulo

Nome:

- `Channel Ranking`
- `Ranking de Canais`

Blocos principais:

1. `Summary strip`
- total de canais por estado
- capital total em canais `Expandir`, `Monitorar`, `Fechar`
- top 3 oportunidades
- top 3 riscos

2. `Ranking table`
- score
- estado
- peer
- channel point
- capacidade
- lucro 7d
- custo rebalance 7d
- utilizacao
- uptime/saude
- principais motivos

3. `Filters`
- todos
- expandir
- manter
- monitorar
- fechar
- privado/publico
- ativo/inativo

4. `Sorts`
- score
- lucro liquido
- eficiencia de capital
- custo de rebalance
- risco operacional

### Detalhe do canal

Abrir drawer ou painel lateral com:

- score e estado
- tendencia 7d e 30d
- racional detalhado
- recomendacoes
- links para:
  - `Lightning Ops > Canais`
  - `Autofee`
  - `Rebalance`
  - `Close Manager`

### Integração com `Lightning Ops`

Nos cards de canal:

- badge discreto com estado
- score curto
- click ou CTA `Ver ranking`

Nada alem disso no card.

## Arquitetura de backend

### Novo servico

Criar um servico dedicado:

- `ChannelRankingService`

Responsabilidades:

- consolidar score e recomendacoes por canal
- persistir snapshots calculados
- servir API para lista e detalhe

### Persistencia sugerida

Tabela `channel_rankings`

Campos iniciais:

- `channel_id`
- `channel_point`
- `peer_pubkey`
- `peer_alias`
- `score`
- `state`
- `state_reason_codes[]` ou `reasons_json`
- `recommendations_json`
- `capacity_sat`
- `local_balance_sat`
- `remote_balance_sat`
- `forward_fee_7d_sat`
- `rebalance_fee_7d_sat`
- `profit_fee_7d_sat`
- `out_ppm_7d`
- `rebal_ppm_7d`
- `pending_htlc_count`
- `active`
- `inactive_duration_sec`
- `class_label`
- `computed_at`

Opcionalmente:

- `score_7d`
- `score_30d`

### Tabela de snapshots ou historico

Em fase posterior:

- `channel_ranking_history`

para graficos e tendencia real.

## APIs propostas

### Lista

- `GET /api/lnops/channel-ranking`

Resposta:

- status geral
- itens ranqueados
- filtros agregados

### Detalhe

- `GET /api/lnops/channel-ranking/{channel_id}`

Resposta:

- score
- estado
- motivos
- recomendacoes
- breakdown das metricas

### Recompute

- `POST /api/lnops/channel-ranking/recompute`

Uso:

- manual
- admin
- debug

No MVP, tambem pode ser recalculado em polling simples.

## Estrategia de implementacao

### Fase 1 - MVP read-only

Entregas:

- `ChannelRankingService`
- schema `channel_rankings`
- calculo de score com dados ja existentes
- tela nova `Ranking de Canais`
- badge discreto no card de canal em `Lightning Ops`

Dados usados:

- `GET /api/lnops/channels`
- `notifications`
- `autofee_state`

Estados liberados:

- `Expandir`
- `Manter`
- `Monitorar`
- `Fechar`

Recomendacoes:

- apenas texto/acoes sugeridas
- nenhuma automacao

### Fase 2 - tendencia e comparacao

Entregas:

- score 7d vs 30d
- tendencia de piora/melhora
- historico de score
- comparacao entre canais do mesmo peer

### Fase 3 - recomendacoes acionaveis

Entregas:

- deep links prontos para:
  - autofee
  - rebalance
  - close manager
- sugestoes mais especificas por causa
- filtros por "acao sugerida"

### Fase 4 - telemetria operacional avancada

Entregas:

- score de estabilidade do peer 30d
- agregados de falha HTLC por canal
- score de "rebalance dependence"
- feedback loop para validar se recomendacoes melhoraram resultado

## Riscos e cuidados

### Risco 1: score magico demais

Mitigacao:

- sempre mostrar motivos e metricas que formaram o estado

### Risco 2: penalizar canais novos cedo demais

Mitigacao:

- limiar conservador para `Fechar`
- protecao para canais sem historico suficiente

### Risco 3: excesso de ruido no `Lightning Ops`

Mitigacao:

- no card, mostrar apenas badge e score curto

### Risco 4: overfitting a 7 dias

Mitigacao:

- incluir 30 dias em fase seguinte
- separar score de recomendacao

## Testes sugeridos

### Backend

- canal rentavel e estavel cai em `Expandir`
- canal neutro e estavel cai em `Manter`
- canal com lucro fraco e rebalance caro cai em `Monitorar`
- canal persistentemente ruim cai em `Fechar`
- ranking ordena corretamente por score e score tie-break
- recomendacoes mudam conforme a causa principal

### UI

- tela carrega ranking e filtros
- click no badge do card abre o detalhe correto
- ordenacoes e filtros funcionam
- detalhe mostra score, estado, motivos e recomendacoes

## Sequencia recomendada de execucao

1. Implementar backend read-only com score simples e explicavel.
2. Criar a nova tela `Ranking de Canais`.
3. Adicionar apenas badge/score no card do `Lightning Ops`.
4. Validar com dados reais por alguns dias.
5. Ajustar pesos e thresholds antes de adicionar automacoes ou telemetria avancada.

## Recomendacao final de produto

O modulo deve nascer como uma ferramenta de priorizacao operacional, nao como uma "verdade automatica".

Se o MVP entregar bem estas tres coisas, ele ja sera muito util:

- ordenar canais por valor real;
- explicar por que cada canal caiu naquele estado;
- dizer ao operador qual modulo revisar em seguida.

Essa abordagem mantem o escopo sob controle e conversa diretamente com o que o LightningOS ja faz melhor: operacao pratica de node.
