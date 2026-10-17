# Sovereign: teto pelo preço real de venda e piso do peer só com déficit — 0.5.43

## Diagnóstico (Friendspool, 10/10/2026)

A receita caiu de 5,6–7,1k/dia (04–07/10) para 2,9k e 2,0k (08–09/10) com o
volume roteado igual (84–97M/dia). O que mudou foi onde o volume passou: os
vendedores caros pararam de vender e o fluxo foi para os corredores de 1 ppm.

Dois mecanismos, os dois no Automation Interlock / autopilot:

### 1. O piso `protect_fee_floor` usava a fee do peer mesmo sem déficit

| Canal | Antes | Depois | Causa | Venda depois |
|---|---|---|---|---|
| Strike | 578 | 1250 (07/10 21:04) | peer subiu para 1000 ppm; piso = 1000 / 0,8 | 0 em 3 dias (vendia 130–870/dia) |
| TennisNbtc | 53 | 163 (08/10 23:00) | lote de 130 ppm na janela de 7d | 44 e depois 0 (vendia 170–490/dia a 53) |

Strike estava com 40% local contra alvo de 35%, Tennis com 71% contra 5%.
Nenhum dos dois ia ser reposto. A fee do peer é o preço da **próxima**
reposição; sem déficit não existe próxima reposição, e o piso só serviu para
parar a venda do estoque que já estava lá. A fase 0 do AutoFee adaptativo
(`docs/autofee-adaptive-pricing-plan.md`) já mostrava Tennis com zero venda
a 213 e 363 ppm.

**Mudança (`deriveRebalanceEconomicEnvelope`):** o custo do peer só entra no
piso quando o canal está abaixo do alvo (`needsRefill`). Lote pago na janela
continua protegido pelo próprio custo (Strike: 225 / 0,8 = 282, não 1250).
Quando o canal drena abaixo do alvo, o piso do peer volta.

### 2. O teto de compra usava a fee anunciada, não o preço a que o canal vende

O teto do autopilot é `econ_ratio × fee anunciada`. Quando a fee anunciada
está acima do preço que o canal realmente fecha, o autopilot paga quase o que
o canal vende de verdade. Nos 30 dias até 10/10:

| Nó | Rebalance 30d | Pago acima de 80% do preço real de venda |
|---|---|---|
| Friendspool | 73.677 sats | 43.864 sats (60%) |
| BRLN HUB | 8.295 sats | 1.185 sats (14%) |

| Canal (Friendspool) | Pago (ppm) | Vendeu a (ppm) | Fee 30d | Lucro 30d |
|---|---|---|---|---|
| bfx-lnd0 (7M) | 1559 | 1619 | 5.372 | −5.213 |
| bfx-lnd0 (20M) | 1581 | 1739 | 4.619 | −3.386 |
| LQWD-Australia | 979 | 1139 | 10.997 | +1.945 |
| Authenticity | 723 | 860 | 13.101 | +5.838 |
| Don Quixote Bank | 503 | 609 | 2.453 | +481 |
| bxm | 825 | 1018 | 1.122 | −111 |

É a mesma coisa que o `sovereign_rebalance_cost_7d_ppm` 375 contra
`sovereign_forward_fee_7d_ppm` 370 do painel: o nó compra ao preço a que
vende. O HUB, com os mesmos knobs, paga 574 e vende a 971.

**Mudança (`realized_price_cap_enabled`, opt-in, padrão desligado):** o teto
passa a ser `econ_ratio × min(fee anunciada, preço real de venda)`. Preço real
= ppm de saída dos forwards do canal em 7 dias quando há ≥ 3 forwards, 30 dias
quando há ≥ 5, senão a fee anunciada (canal novo). Vale para o scan, para o
runJob (loop legado e fast-path) e para o orçamento de rota da elegibilidade
(`fee_budget_ppm`). O piso que o Interlock publica continua em termos de fee
anunciada: ele diz quanto a fee precisa subir para a reposição caber.

Ordem de grandeza: bfx-lnd0 a 0,8 × 1619 = 1295 de teto não teria comprado os
lotes de 1559; LQWD-Australia a 911 teria recusado a média de 979 (parte das
rotas abaixo disso continua passando). Menos compras, mais margem por compra.

A decisão expõe `sale_price_ppm`, `sale_price_source` e
`fee_cap_reference_ppm` em `/api/rebalance/channels`.

### 0.5.45: qualquer venda é preço; sem venda em 30 dias não se compra

Dois dias depois de ligar o teto no Friendspool (13/10): 7% do gasto de 7
dias (440 sats) foi para canais **sem nenhuma venda** (BitcoinVN 22 a 1034
ppm, YOLOSpirit a 423), e 4% no HUB. O teto não os alcançava: com menos de 5
forwards em 30 dias não havia preço de venda e o teto caía na fee anunciada;
a trava de estoque só age depois do segundo lote.

- Com `realized_price_cap_enabled`, **um forward em 30 dias já é preço**
  (BitcoinVN vendeu uma vez a 879 → teto 703, não 1107).
- Canal com **mais de 30 dias e zero forwards de saída em 30 dias** sai do
  conjunto de alvos automáticos: `no_sale_30d` nos `skip_reasons` e
  `sale_price_source = "no_sale_30d"` em `/api/rebalance/channels`. O piso do
  Interlock e o envelope continuam na fee anunciada; **Manual Rebal In**
  continua possível. Idade vem do short channel id contra a altura do bloco;
  sem altura (LND indisponível) nada é bloqueado.

### 0.5.45: canal drenado re-mede o preço; escada aceita `low_outbound`

Em 13/10 a escada achou rota pro RA⚡KO a 320 ppm e o AutoFee subiu a fee de
362 para 455 — mas o teto seguiu olhando o preço real de 332 (× 0,8 = 265),
e um canal vazio não vende, logo o preço real não sobe. A escada mediu o
preço e ninguém testou se o canal vende nele; o RA⚡KO, maior vendedor da
semana, ficou a 1% até aparecer rota abaixo de 265.

- **Canal com menos de 10% de saldo local** (`realizedPriceCapDrainedLocalPct`)
  volta a precificar o teto pela **fee anunciada**, mas só para **um lote do
  tamanho do keepalive** (`keepalive_refill_pct` da capacidade, ≥ mínimo de
  execução): o suficiente para medir o preço, não para reestocar a um preço
  que o canal talvez não feche. `sale_price_source = "drained_retest"` e
  `drained_retest: true` em `/api/rebalance/channels`. Se o lote vende, o
  preço real sobe e o teto normal cuida do próximo; se não vende, a trava de
  estoque e o `no_sale_30d` seguram. Vale também para a base da sonda da
  escada (os degraus de 125%/150% partem da fee anunciada no canal drenado).
- **A escada passa a aceitar o intent `autofee_low_outbound_target`** além
  de drained/extreme-drained (`isSovereignDiscoveryIntentReason`). Em 14/10
  PurpleWisteria (6%) e Unwetter (7%) ficaram o dia inteiro em "all sources
  failed" com esse intent e nunca receberam sonda. O keepalive continua só
  com drained/extreme-drained.
- Continua fora da escada: canal com AutoFee desligado (não produz intent) e
  intent com menos de `keepalive_refill_after_hours` (48h).

### 0.5.46: faixa do reteste, lote pequeno não pina, reteste não espera o cost gate

Três efeitos colaterais vistos em 15–16/10 depois de o reteste entrar:

- **Cliff dos 10%.** O lote de reteste levava RA⚡KO, PurpleWisteria e
  CoinPayments de 1–7% para 12–17%; fora da faixa, voltavam ao teto
  realizado (abaixo da rota) e paravam de repor. A faixa passa a ser
  `max(10%, alvo/2)`: canal com alvo 50% é "drenado" até 25%.
- **Lote pequeno pinava o canal inteiro.** BitcoinVN (6M) com um lote de 100k
  a 1034 ppm recebia piso `rebalance_cost_budget` de 1322 no canal todo, onde
  nunca vendeu (vendeu 742k em dois minutos a 923). O custo pago só entra no
  envelope quando a liquidez paga na janela é ≥ 10% da capacidade
  (`rebalancePaidLotFloorMinPct`); abaixo disso o piso fica com o peer (se
  houver déficit) e o preço com a venda real.
- **Reteste × cost gate.** O lote de reteste é limitado pelo tamanho; não faz
  sentido esperar que o custo histórico (o mesmo lote caro) caiba no
  orçamento. Canal em reteste com preço viável é elegível como alvo
  automático (`autoTargetEligibility`); `no_sale_30d` continua vetando.

### 0.5.46: sonda no degrau mínimo (os parciais de volta)

Em 16/10 o loop legado do Friendspool não fechava **nenhum parcial**: com o
teto pelo preço real, a escada de montante (100k → 50k → 25k → 12,5k → 10k)
não achava rota em degrau nenhum, de nenhuma das 30 sources, porque o preço
de mercado da rota (RA⚡KO 320, PW 630, CoinPayments ~180) estava acima de
`econ × preço real` (274, 454, 160). Rota inexistente a 10k sats não é
liquidez — é teto.

A sonda (`rebalanceRouteProbe`) mantém o teto realizado em todos os degraus
e, quando o lote mínimo também falha, repete **uma vez** o lote mínimo com o
teto da **fee anunciada**. O lote é o mínimo de execução (10k por padrão),
então o máximo que ele paga a mais são alguns sats; o orçamento é um lote
normal por job (`min_amount_sat`). Uma sonda que paga registra o preço nos
pair-stats e no custo recente — é assim que o AutoFee e a escada ficam
sabendo quanto a rota custa agora. Só existe com `realized_price_cap_enabled`
e quando o teto realizado de fato baixou a referência daquele alvo; com
`fee_limit_ppm` fixo não há o que sondar. O journal mostra
`realized-cap probe lot source=… amount=10000 fee_limit_ppm=…`.

### 0.5.46: o lote de referência do envelope é o primeiro lote executado

O envelope econômico (custo do peer, orçamento de fee, piso do Interlock e a
elegibilidade "pricing feasible") amortizava as base fees sobre o **menor
degrau** da escada de montante (`min_execute_sat`, 10k por padrão). A 10k,
1 sat de base fee vale 100 ppm — e esse artefato decidia quem podia ser alvo.
Harry Potter em 17/10 no Friendspool: peer 24 ppm + 1 sat, vende a 119 ppm,
11% local contra alvo de 30%, 1,8k sats vendidos em 7 dias. Custo do peer
calculado: 124 ppm; orçamento (119 × 0,8): 95. `eligible_as_target=false`,
nenhum job em dias. O executor, no entanto, compra lotes de 100k
(`min_amount_sat`), em que o mesmo peer custa 34 ppm — a elegibilidade e a
execução não falavam do mesmo lote (issue #184, "valor de referência versus
valor efetivamente executado").

A referência passa a ser `min_amount_sat` (o primeiro lote que o executor
envia), com `min_execute_sat` só como fallback quando não há `min_amount`.
Cada tentativa continua limitada pela fee calculada no **seu** montante
(`calcFeeLimitMsat`), então um degrau de 10k segue pagando no máximo o que
cabe a 10k. Efeito no Friendspool (custo do peer a 100k em vez de 10k):
Harry 124 → 34 (vira alvo), BitGo piso 353 → 263, 8DegreesWO e ifeellonely
passam a ser elegíveis (o ROI decide), e os pisos `peer_fee_budget` do
Interlock caem na mesma proporção nos canais em que o peer cobra base fee.
`economic_reference_amount_sat` na API e `reference_amount_sat` na evidência
do intent mostram o lote usado.

## Compatibilidade

- Uma coluna nova em `rebalance_config` (`add column if not exists`).
- O piso do peer sem déficit muda para todos (não é opt-in): é correção de um
  piso que protegia uma reposição que não ia acontecer. Com déficit, nada muda.
- Com `realized_price_cap_enabled` desligado o teto é o da 0.5.42.

## Calibração sugerida

Friendspool: ligar. HUB: ligar, por paridade; o efeito deve ser pequeno.

## O que acompanhar

- `sovereign_rebalance_cost_7d_ppm` contra `sovereign_forward_fee_7d_ppm`: a
  distância deve abrir para perto de 20% (econ 0,8).
- `realized_net_7d`: de −4,6k para positivo.
- Strike e Tennis voltando à fee do próprio AutoFee e vendendo.
- Se um canal que vendia parar de ser reposto por "fee_cap_zero" ou "sem
  rota": o preço real dele está abaixo do que a rota custa; é a resposta certa
  (não repor), mas vale conferir que o canal não é corredor.
