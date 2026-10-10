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
