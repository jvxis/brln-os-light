# AutoFee adaptativo: plano de entregas (out/2026)

## Por que

Uma semana de operação assistida no Friendspool e no BRLN HUB (02 a 08/10/2026)
mostrou que o AutoFee é bom em **proteger** o preço e fraco em **descobrir** o
preço. Os pisos de custo, o lock global de margem negativa e os holds foram
desenhados para não vender barato. Eles cumprem isso. O que falta é a outra
metade: quando um canal com estoque não vende, o motor segura a fee e nunca
descobre o preço que venderia.

O que ficou demonstrado, com dados dos dois nós:

1. **A seed de mercado não é o preço de compensação.** Ela diz o que os outros
   cobram para chegar no peer; a rota só é escolhida quando somos mais baratos.
   Open_Hand: 30 dias a 713 ppm (seed 644) renderam 33 sats; três minutos a 322
   renderam 399 e esvaziaram o canal. CoinPayments: 448 → 196, vendeu na hora.
2. **Cada canal tem a sua curva, e ela só aparece quando se testa.** Com 30 dias
   de histórico de fee cruzado com forwards (fase 0 abaixo): Authenticity vende
   3,1M/dia a 825 e 0,07M/dia a 1.038; RA⚡KO vende a 321–385 e nada a 400;
   CLB vende até 259 e nada de 278 para cima; Zap-O-Matic vende a 634 e nada a
   734; kappa vende a 1.250 e 1.462; coinos só vendeu em rajada a 2.999.
3. **O que vale é a margem do ciclo, não a margem por sat.** CoinPayments
   comprando a 120 ppm e vendendo a 196 (39%) rende mais que coinos a 2.900 com
   1,9M parados. Preço de venda só faz sentido junto com custo de reposição e
   velocidade de giro.
4. **O tipo de canal muda a regra.** Router e source repõem de graça e podem
   testar preço para baixo. Sink só repõe por rebalance: o preço mínimo é o
   custo da rota mais margem, ou a decisão é fechar. Sink de mão única (peer
   cobra mais que nosso teto) vende uma vez por ciclo.
5. **Alguns fluxos morrem com dezenas de ppm e outros aceitam milhares.**
   speedupln ↔ cyberdyne parou com 25 ppm numa ponta (3,3k/dia de assistida
   perdidos em um dia); coinos vende a 2.999. Não há regra global.
6. **O método que funcionou foi sempre o mesmo:** mudar um canal, deixar um
   parecido como controle, dar sete dias, decidir pelos dados. O motor pode
   fazer isso sozinho.

## Fase 0: dataset preço × resposta (feita em 08/10)

`scripts/analysis/autofee_price_response.py` cruza, por canal, as mudanças de
fee do nosso lado (`/api/lnops/channel/detail`, `fee_logs`) com os forwards de
saída (LND `fwdinghistory`, ou a lista `routed` do detail quando não há acesso
ao LND) e devolve, por nível de fee, dias observados, volume por dia, fee por
dia e forwards por dia. Só entram níveis observados por pelo menos um dia.

Resultado: 43 canais com dois ou mais níveis no Friendspool, 22 no HUB.

Limitações que a fase 2 precisa resolver no motor:

- **Estoque é variável de confusão.** Zero venda num nível pode ser zero
  demanda ou zero saldo local. O dataset não tem histórico de saldo; o motor
  tem (`out_ratio` a cada run) e deve medir "venda por dia com estoque".
- **Níveis curtos.** Muitos degraus do AutoFee duram 1 a 2 dias e os forwards
  vêm em rajadas; um ponto não é uma curva.
- **Rajadas de terceiros.** Open_Hand e CoinPayments venderam em MPP de um
  único rebalanceador. A resposta pode não se repetir.

## Fase 1: parar de segurar o que não vende (0.5.42, em PR)

- **PR #239 `stale_stock_down_enabled`:** canal com estoque pago, sem venda há
  7 dias e fee ≥ 1,25× seed cai 20% uma vez e depois 8%/dia até metade da
  seed, ignorando o lock global e os pisos de rebalance/outrate/peg. Só para
  canais que se repõem sozinhos: router, source, ou com forward de entrada na
  semana. Sink sem entrada mantém o preço e cai no caminho de fechamento do
  ranking.
- **Já existe e basta manter:** o piso do interlock segue o custo de rebalance
  de 7 dias, nos dois sentidos; o surge sobe a fee quando a venda acelera.
- **PR #240:** ranking não marca para fechar canal com menos de 7 dias nem
  canal em compromisso do Magma.

## Fase 2: experimento automático por canal (0.5.43)

Uma máquina de estados por canal, em `autofee_service.go`, com tabela própria
`autofee_price_experiments` (canal, fee de partida, fee de teste, início, fim,
venda/dia e estoque médio antes e durante, veredicto).

- **Gatilho:** estoque ≥ 10% e venda por dia abaixo de X% da mediana do canal
  (ou zero) por 7 dias; sem experimento ativo no canal; no máximo N canais
  por nó em teste ao mesmo tempo (padrão 2); nunca em corredor de loop (fase 3).
- **Corte:** para o maior entre metade da seed e, em sink, custo de reposição
  mais margem mínima. Fee fixa durante o teste, holds e locks suspensos só
  nesse canal.
- **Medição:** 7 dias ou até a venda acumulada passar de uma fração do
  estoque; sempre "venda por dia com estoque disponível", lendo o `out_ratio`
  de cada run para descontar as horas vazias.
- **Veredicto:** vendeu → novo preço vira a referência do canal e o surge
  procura o ponto em que a venda desacelera; não vendeu → volta ao preço
  anterior e o canal entra na lista de "sem demanda", que é o que o ranking
  usa para fechar.
- **Memória:** cada veredicto grava um ponto (fee, venda/dia, estoque) na
  curva do canal. A UI mostra a curva no detalhe do canal.
- **Knobs:** `price_experiments_enabled` (padrão desligado), canais simultâneos,
  dias de teste, piso do corte em % da seed.

### Implementação (0.5.43, `autofee_price_experiments.go`)

Knobs em `autofee_config`: `price_experiments_mode` (`off` | `shadow` |
`enforce`, padrão `off`), `price_experiments_max_active` (1–10, padrão 2),
`price_experiments_days` (3–14, padrão 7), `price_experiments_floor_seed_pct`
(20–90, padrão 50). Tabela `autofee_price_experiments`; leitura em
`GET /api/lnops/autofee/price-experiments`.

Gatilho, por canal e por run do AutoFee, depois do `evaluateChannel` e do
idle refresh:

- fee atual ≥ 20 ppm e acima do mínimo global; não é super source; não é
  corredor de loop; canal com ≥ 7 dias;
- estoque: `out_ratio` ≥ 10%;
- venda: zero forwards de saída em 7 dias, ou volume/dia abaixo de 25% da
  mediana diária do canal nos últimos 30 dias (dias sem venda contam zero,
  medidos desde a primeira venda do canal na janela);
- sem experimento concluído há menos de 14 dias (30 dias depois de
  `no_demand`); vaga livre (`max_active`).

Corte: `max(seed × piso%, mínimo global, mínimo do canal)`. Em sink, também
`custo de rebalance × 1,10`; sink sem custo conhecido e sem entrada na semana
não entra (`sink_no_cost_reference`): vender abaixo de um preço de reposição
desconhecido é prejuízo, e o caminho é o ranking. O corte precisa ser de pelo
menos 15%; senão não há experimento.

Durante a janela, em `enforce`, a decisão do motor é sobrescrita para a fee de
teste (holds, cooldowns e pisos ficam suspensos só nesse canal; o desconto de
entrada decidido pelo motor é mantido). A cada run o experimento acumula
`runs_total`, `runs_stocked` (out_ratio ≥ 10%) e a venda desde o início
(`notifications`, forwards de saída do canal). Termina no fim da janela ou,
em `enforce`, quando a venda passa de metade do saldo inicial.

Veredicto: `sold` quando vendeu ≥ 2% da capacidade **e** a venda por dia com
estoque é ≥ 1,5× a baseline dos 7 dias anteriores; `inconclusive` quando o
canal ficou sem estoque em mais de 70% dos runs; `no_demand` nos demais.
`sold` mantém a fee de teste e grava a memória de outrate nela (o motor segue
dali, e o surge sobe se a venda acelerar). `no_demand` volta à fee de partida.
Canal fechado ou tirado do AutoFee no meio: `aborted`. Mudança de modo com
experimento ativo: `aborted` (a fee volta ao motor).

**Modo sombra** abre o mesmo experimento, com a fee de teste que aplicaria,
mas não muda nada; mede o canal na fee atual. O veredicto sai com prefixo
`shadow_`. Leitura: `shadow_no_demand` confirma que o canal não vendeu sozinho
no preço antigo (o gatilho estava certo); `shadow_sold` diz que teria vendido
de qualquer jeito (o gatilho foi cedo). Uma semana de sombra nos dois nós antes
de ligar `enforce`.

Corredor de loop (antecipa a fase 3): par de canais A↔B em que ≥ 50% do que
sai por B entrou por A e ≥ 50% do que sai por A entrou por B, com ≥ 100k sats
por ponta em 7 dias. Marca a tag `loop-corridor` e nunca entra em
experimento. A política completa por tipo (router/source, sink com rota, sink
de mão única) continua na fase 3.

Fora desta entrega: ponto na curva do canal visível no detalhe (UI); lista
"sem demanda" lida pelo ranking (só faz sentido com veredictos de `enforce`).

## Fase 3: política por tipo de canal (0.5.43)

- **Corredor de loop:** par de canais com volume de entrada e saída cruzado
  (speedupln ↔ cyberdyne) detectado pelos forwards; nunca sobe fee, nunca entra
  em experimento. Hoje isso é um aviso na memória do operador; passa a ser uma
  classe do AutoFee.
- **Router / source:** experimento livre para baixo; piso = margem sobre o
  custo de rebalance quando houver.
- **Sink com rota barata:** fee = custo da última reposição + margem; o piso
  desce quando a rota barateia (CoinPayments: rota a 120, venda a 196).
- **Sink de mão única:** peer cobra mais que o teto; preço de uma viagem por
  ciclo, sem experimento; o ranking recomenda fechar quando o estoque acabar.

## Fase 4: seed própria (0.5.44)

Quando a curva do canal tiver pontos suficientes (≥ 3 níveis com ≥ 2 dias
cada e venda observada), ela substitui a seed de mercado como referência
daquele canal; a seed de mercado vira o fallback para canais novos e para
quem nunca vendeu. A seed v2 continua sendo a referência de mercado.

## Como medir sucesso

| Métrica | Linha de base (semana 01–07/10, Friendspool) | Meta |
|---|---|---|
| Custo de rebalance sobre receita | 46% | ≤ 40% |
| Líquido por dia | 3.206 | 6.667 |
| Estoque pago parado (sem venda em 7d) | ~33M, 23k sats | < 10M |
| Canais com ≥ 400 sats de venda na semana | 23 de 101 | 35 |
| Sell-through maduro | 57% | ≥ 70% |

O HUB roda as mesmas fases com os mesmos knobs e serve de controle: tamanho
e fluxo diferentes, mesmo software.

## Ordem

1. Fase 0 feita; o script entra no repositório com esta nota.
2. Fase 1: merge das PRs #239 e #240 na 0.5.42 e ligar
   `stale_stock_down_enabled` nos dois nós.
3. Fase 2 e 3 na 0.5.43, começando pela tabela e pela máquina de estados sem
   UI, em modo sombra (grava o que faria, não aplica), uma semana de sombra nos
   dois nós, depois ligar.
4. Fase 4 depois de um mês de curvas gravadas.
