# Glossario de Tags do Autofee (PT-BR)

Esta pagina explica, de forma didatica, as tags que aparecem em `Autofee Results`.

Versao em ingles: `AUTOFEE_TAG_GLOSSARY_EN.md`

Notas:
- As tags sao geradas pela logica do backend e podem evoluir.
- Algumas tags sao fixas (exemplo `stagnation`).
- Outras sao dinamicas (exemplo `stagnation-rN`, `seed:vol-15%`, `inb-120`).

## Rotulos centrais do canal
- `sink`: canal usado principalmente para escoar liquidez local (tende a sair sats).
- `source`: canal que costuma fornecer inbound e alimentar outros fluxos.
- `router`: canal com perfil mais neutro de roteamento (nem sink nem source forte).
- `unknown`: sem sinal suficiente para classificar com confianca.
- `trend-up`: tendencia da fee para cima no contexto atual.
- `trend-down`: tendencia da fee para baixo no contexto atual.
- `trend-flat`: tendencia neutra (sem direcao forte).

## Controles de movimento e execucao
- `stepcap`: alvo foi limitado por limite de passo por rodada.
- `stepcap-lock`: alvo pedia mudanca, mas o limite de passo segurou em `same-ppm`.
- `floor-lock`: fee final ficou travada no floor.
- `floor-relax-stall`: floor foi relaxado por estagnacao para destravar convergencia.
- `reversal-guard`: primeira tentativa de inverter direcao foi bloqueada (histerese).
- `reversal-confirmed`: inversao de direcao liberada apos rounds de confirmacao.
- `downcap-general`: limitador geral de queda por rodada foi aplicado (queda mais conservadora).
- `htlc-low-sample-downcap`: limitador mais restrito de queda aplicado por amostra HTLC baixa.
- `hold-small`: mudanca muito pequena foi ignorada para evitar churn.
- `same-ppm`: rodada terminou sem mudanca de fee.
- `cooldown`: mudanca bloqueada por janela de cooldown.
- `cooldown-profit`: bloqueio extra de cooldown para proteger lucro.
- `cooldown-skip`: regra permitiu ignorar cooldown nesse contexto.
- `rebal-recent`: houve rebalance recente (sinal de liquidez ainda em ajuste).
- `rebal-attempt`: houve tentativa recente de rebalance.
- `rebal-recent-noup`: bloqueio de subida por rebalance muito recente.

## Protecoes de lucro e locks
- `neg-margin`: margem estimada do canal esta negativa.
- `negm+X%`: margem negativa agravada por buffer de risco (`X%`).
- `no-down-low`: reducao bloqueada por sinal muito fraco/baixo.
- `no-down-neg-margin`: reducao bloqueada para nao piorar margem negativa.
- `global-neg-lock`: lock global de protecao de margem aplicado.
- `lock-skip-no-chan-rebal`: lock foi pulado por ausencia de rebalance no canal.
- `lock-skip-sink-profit`: lock foi pulado para preservar estrategia de lucro em sink.
- `profit-protect-lock`: lock explicito de protecao de lucro.
- `profit-protect-relax`: lock de lucro foi parcialmente relaxado (queda controlada).

## Anchors de mercado e comportamento de floor
- `sink-floor`: floor especifico para canais com perfil sink.
- `outrate-floor`: floor ancorado na taxa de saida observada (mercado real do canal).
- `revfloor`: floor por receita/historico de forwards (protecao de receita).
- `peg`: floor ancorado em referencia de preco/taxa de mercado.
- `peg-grace`: fase de graca do peg (mantem ancora por mais tempo).
- `peg-demand`: peg reforcado por sinal de demanda.
- `peg-paused-stagnation`: peg pausado porque estagnacao esta dominando a decisao.
- `low-out-slow-up`: canal com pouco outbound, subida fica mais lenta/conservadora.
- `low-out-noflow-cap`: baixa atividade limita o quanto pode subir.
- `no-signal-noup`: sem sinal confiavel, sistema evita subir fee "no escuro".
- `no-signal-floor-relax`: floor relaxado por ausencia de sinal util.
- `seed-liq-down-hold`: seed/no-signal pedia reducao, mas a liquidez local efetiva estava baixa demais para cortar no escuro.
- `min`: `floor_src` quando nao ha referencia local/externa utilizavel e o piso vem apenas de `min_ppm`.
- `rebal-floor-low-volume`: floor de rebalance aplicado com aviso de volume baixo.
- `floor-up-blocked-low-signal`: subida por floor bloqueada por sinal fraco.

## Controles de estagnacao
- `stagnation`: modo de estagnacao ativo.
- `stagnation-pressure`: estagnacao com pressao elevada para normalizar fee.
- `normalize-out`: estagnacao tentando alinhar fee ao outrate.
- `normalize-rebal`: estagnacao tentando alinhar fee ao custo de rebalance.
- `stagnation-floor`: floor especifico de estagnacao aplicado.
- `stagnation-floor-relax`: floor de estagnacao foi relaxado para destravar.
- `stagnation-neg-override`: estagnacao com override para evitar travar em margem.
- `stagnation-rN`: contador de rounds consecutivos em estagnacao.
- `stagnation-cap-<ppm>`: teto/limite de fee aplicado pelo modo de estagnacao.

## Discovery, surge e modos adaptativos
- `discovery`: modo de descoberta ativo (testa convergencia de fee).
- `discovery-hard`: descoberta mais agressiva.
- `explorer`: estrategia exploratoria ativa para buscar ponto de equilibrio.
- `drained-explorer`: exploracao especifica para canal muito drenado e parado.
- `drained-explorer-rN`: contador de rounds no modo `drained-explorer`.
- `drained-explorer-step`: alta pequena aplicada pelo explorador drenado.
- `drained-explorer-cap`: alta limitada pelo cap proprio do `drained-explorer`.
- `surge+...`: aumento temporario por surto de demanda/trafego.
- `surge-hold...`: fase de sustentacao do surto.
- `surge-timeout-release`: fim do hold de surto por timeout.
- `surge-confirmed-rounds`: surto confirmado por rounds consecutivos.
- `circuit-breaker`: freio de seguranca acionado.
- `extreme-drain`: modo agressivo para canal drenado.
- `extreme-drain-unlock`: aliviou temporariamente restricoes do extreme drain.
- `extreme-drain-turbo`: versao turbo do modo extreme drain.
- `super-source`: canal tratado como super fonte de liquidez.
- `super-source-like`: comportamento proximo de super source.
- `top-rev`: canal entre os de maior receita (sinal premium).

## Tags de sinal HTLC
- `htlc-policy-hot`: muitas falhas de policy em janela curta.
- `htlc-liquidity-hot`: muitas falhas de liquidez em janela curta.
- `htlc-forward-hot`: muita pressao de falhas no forward path.
- `htlc-sample-low`: amostra HTLC pequena (baixa confianca estatistica).
- `htlc-neutral-lock`: lock neutro para evitar conclusao forte com sinal misto.
- `htlc-liq+X%`: ajuste incremental pela pressao de liquidez (`X%`).
- `htlc-policy+X%`: ajuste incremental pela pressao de policy (`X%`).
- `htlc-liq-nodown`: bloqueia queda quando sinal de liquidez esta quente.
- `htlc-policy-nodown`: bloqueia queda quando sinal de policy esta quente.
- `htlc-neutral-nodown`: bloqueio conservador de queda em sinal neutro/duvidoso.
- `htlc-step-boost`: boost de passo causado por sinal HTLC forte.

## New inbound e desconto inbound
- `new-inbound`: canal identificado como inbound novo.
- `bootstrap`: fase de bootstrap de fee para canal novo.
- `inb-<n>`: desconto inbound aplicado (`n` ppm).

## Market refill
- `market-refill`: canal sendo precificado no modo global `Market refill`.
- `market-node`: tier base do `market_refill` para o node como um todo.
- `market-low`: tier mais forte para canais com local ratio menor.
- `market-drained`: tier mais forte para canais efetivamente drenados.
- `market-explore`: branch exploratorio do `market_refill`, usado para canais sem fluxo util recente.
- `market-refill-up`: vies explicito de alta no outbound do `market_refill`.
- `market-refill-stepcap`: a alta foi parcelada pelo step cap especifico do modo.
- `market-refill-reversal-bypass`: a alta ignorou o reversal guard padrao por troca clara de regime.
- `market-refill-skew`: o inbound do `market_refill` foi refinado por skew externo da Amboss.
- `market-refill-skew-low`, `market-refill-skew-med`, `market-refill-skew-high`: classificacao do skew externo usado para aproximar inbound e outbound.

## Rescue
- `rescue`: canal entrou em estado temporario de resgate dentro do modo `Balanced`.
- `rescue-enter`: primeira rodada em `rescue`.
- `rescue-exit`: saida normal do `rescue` apos convergencia ou melhora.
- `rescue-expired`: saida do `rescue` por tempo maximo atingido.
- `rescue-rN`: contador de rounds consecutivos em `rescue`.
- `rescue-floor-relax`: o piso foi relaxado para uma banda mais proxima do sinal local.
- `rescue-global-relax`: o lock global por margem negativa foi parcialmente suavizado no `rescue`.
- `rescue-peg-paused`: o `peg` foi impedido de rearmar alta enquanto o canal ainda estava em resgate.
- `rescue-lowliq-block`: entrada em `rescue` bloqueada porque o canal tinha baixa liquidez local efetiva e nenhum sinal local.
- `rescue-lowliq-exit`: estado de `rescue` existente foi limpo quando o guard de baixa liquidez/sem sinal passou a dominar.

## Origem do seed e fallbacks
- `seed:native`: seed veio da referencia local do grafo.
- `seed:native-corrected`: seed nativo usou medias corrigidas do Graph Explorer, reduzindo distorcao por outlier/teto.
- `seed:amboss*`: seed veio da Amboss (ou status de fallback da Amboss).
- `seed:med`: seed calibrado para perfil medio do canal.
- `seed:vol-<n>%`: seed ajustado por volatilidade (`n%`).
- `seed:ratio<factor>`: seed ajustado por multiplicador de razao.
- `seed:outrate`: seed derivado de outrate historico.
- `seed:mem`: seed veio da memoria/historico local.
- `seed:default`: seed caiu no valor default.
- `seed:guard`: seed protegido por guardrail.
- `seed:shock-up` / `seed:shock-down`: seed mudou bruscamente em canal maduro sem sinal local.
- `seed:shock-hold`: primeira rodada de shock segurou no seed anterior.
- `seed:shock-confirmed`: shock repetiu em rodadas suficientes e foi aceito como nova referencia.
- `seed:p95cap`: seed capado por limite estatistico p95.
- `seed:absmax`: seed limitado por teto absoluto.
- `seed:outcap`: seed limitado pelo `out_ppm7d`.
- `seed:rebalcap`: seed limitado pela referencia de rebalance.
- `seed:rebalfloor`: seed limitado levando em conta o floor de rebalance efetivo.
- `liq-low`: target de refresh foi elevado acima do seed porque a liquidez local efetiva estava baixa.
- `liq-high`: target de refresh foi reduzido abaixo do seed porque a liquidez local efetiva estava alta.
- `out-fallback-21d`: fallback de outbound usando janela de 21 dias.
- `rebal-fallback-21d`: fallback de rebalance usando janela de 21 dias.

## Codigos de forecast (sufixo da linha)
- `stable` / `stable-uncertain`: sem direcao clara; aguarda novos sinais.
- `bias_up` / `upward bias`: vies de alta em proximas rodadas.
- `bias_down` / `downward bias`: vies de queda em proximas rodadas.
- `hold_or_up`: manter ou subir para proteger economia/liquidez.
- `reduce`: expectativa de reduzir fee quando guardrails liberarem.
- `idle_reduce`: reduzir por ociosidade com excesso de liquidez local.
- `discovery_fast`: reducao acelerada por modo discovery.
