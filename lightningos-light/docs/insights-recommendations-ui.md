# Insights e Recomendações — desenho da interface

Complemento de [insights-recommendations-plan.md](insights-recommendations-plan.md).
Status: **desenho, sem código**. Protótipo visual navegável (PT/EN) publicado
como artefato na sessão de 04/11/2026; este arquivo é a fonte de verdade.

## Princípios

0. **Um nó só.** A tela roda dentro do LOS de cada nó e só fala dos canais
   e do histórico daquele nó. Não há seletor de nó nem rótulo de nó nos cards;
   os exemplos do protótipo vieram de dois nós do operador, mas na tela real
   cada um veria só os seus.
1. **Didática antes de densidade.** Quem abre a tela pela primeira vez entende
   em um minuto de onde vem cada recomendação, o que ela pede e o que ganha.
   Cada número tem a fonte ao lado (qual tela/API) e cada regra tem uma
   explicação em linguagem comum, a um clique.
2. **Só recomenda, nunca executa.** Todo botão leva para a tela que já faz a
   ação (Rebalance Center, Fee Center, Lightning Loop, Lightning Ops, Novos
   Canais) com o canal pré-selecionado. A única chamada externa (cotação do
   Loop) sai por clique explícito.
3. **Bilíngue de origem.** O backend devolve **códigos**, não frases:
   `kind`, `evidence[].key`, `action.tool`, `guards[].code`,
   `invalidate_when.code`. A UI monta título e textos via i18n
   (`insights.*` em `en.json` e `pt-BR.json`). Números com `Intl.NumberFormat`,
   datas com o `locale` já usado nas outras páginas. Nada de texto em
   português vindo do Go.
4. **Visual com significado.** Cor nunca é o único sinal: cada estado tem
   ícone + rótulo (tema atual já exige isso nos gauges). Reutilizar
   `ThemeGauge`, `HealthGauge`, `HeroTile`, `MetricDisclosure` e `recharts`
   (já no bundle) para sparklines.
5. **Confiança visível.** Amostra fina aparece, mas não sobe ao topo e diz por
   quê ("3 dias com venda", "canal com 5 dias").

## Mapa da tela

```
┌──────────────────────────────────────────────────────────────────────────┐
│ Insights                      [Modo didático ●]  Calculado há 12 min  ↻  │
│ "O que fazer hoje para o nó render mais, com base no que já aconteceu."  │
├──────────────┬──────────────┬──────────────┬─────────────────────────────┤
│ Valor em jogo│ Abertas      │ Seguidas 30d │ Capital parado              │
│ +3.2k sat/d  │ 6 (2 altas)  │ 4 · +1.9k/d  │ 15.0M sat em 4 canais       │
├──────────────┴──────────────┴──────────────┴─────────────────────────────┤
│ DE ONDE VÊM OS SINAIS  (mapa: 5 fontes → Insights, com idade de cada)    │
│  AutoFee ●  Interlock ●  Rebalance ●  Ranking ●  Novos canais ○ (sync 3h)│
├──────────────────────────────────────────────────────────────────────────┤
│ Filtros: [Todas] [Repor] [Capacidade] [Parar compra] [Capital] [Abrir]   │
│          [Preço] [Bloqueios]   Confiança: [alta][média][baixa]  🔍 canal │
├──────────────────────────────────────────────────────────────────────────┤
│ ▌R1  Repor e960… via Loop In            ALTA ●●●   +1.1k sat/d   2 dias  │
│ ▌    Por quê · O que fazer · Quando some · [Dispensar] [Feita] [Regra?]  │
│ ▌R3  Parar de comprar BitGo             ALTA ●●●   evita −120/d          │
│ ▌R4  Capital parado em bfx-lnd0 (15M)   MÉDIA ●●○  realocar 1.2k/d       │
│ ▌R5  Abrir canal com <peer>             BAIXA ●○○  +400/d (n=2)          │
├──────────────────────────────────────────────────────────────────────────┤
│ Histórico (fase 2): linha do tempo das seguidas, antes × depois          │
└──────────────────────────────────────────────────────────────────────────┘
   Gaveta lateral "Como funciona": R1…R7 em linguagem comum + diagrama
```

## Blocos

### 1. Cabeçalho

- Título, uma frase do propósito, hora do último cálculo, botão recalcular
  (`POST /api/lnops/insights/recompute`, fase 1, só leitura dos sinais).
- **Modo didático** (toggle, guardado em `localStorage`): ligado, cada card
  abre com os três blocos expandidos e cada número mostra a fonte em
  subtítulo ("venda 7d · Channel Ranking"). Desligado, cards vêm recolhidos
  numa linha só. Começa ligado na primeira visita.

### 2. Resumo (4 `HeroTile`)

| Tile | Número | Dica (hover/didático) |
|---|---|---|
| Valor em jogo | soma de `value_sat_day` das abertas | "Quanto a mais por dia se todas forem feitas; conta só venda já observada" |
| Abertas | total e quantas de prioridade alta | — |
| Seguidas 30d | n e `realized` médio (fase 2; antes, tile mostra "disponível após 7 dias de uso") | "Comparação 7d antes × 7d depois" |
| Capital parado | sats em canais com R4 aberta | "Local ≥ 60% sem venda 30d" |

### 3. Mapa dos sinais (SVG inline)

Cinco caixas (AutoFee, Interlock, Rebalance, Channel Ranking, Novos Canais)
com setas convergindo para "Insights". Cada caixa mostra: ponto de estado
(● ok / ○ sem dado ou > 6h) e idade da última leitura. Clique abre a tela de
origem. Função: ensinar que Insights não inventa nada, só cruza. Em telas
estreitas vira lista vertical.

### 4. Card de recomendação (anatomia)

```
▌[ícone kind] Título (i18n: insights.kind.<kind>.title, {alias})
▌ chips: <kind> · confiança ●●● · +N sat/dia · idade
▌ (a tela é do nó onde o LOS roda; nunca mistura nós — cada instância
▌  calcula só sobre os próprios canais e histórico)
▌
▌ POR QUÊ (evidence[])                O QUE FAZER (action)
▌  local 1% ▂ alvo 30%  [HealthGauge]  1. Abrir no Rebalance Center →
▌  venda 7d 2.500 ppm ▮▮▮▮▮            2. Cotar Loop In (último salto: peer)
▌  custo 7d 1.950 ppm ▮▮▮▮               [Cotar] → margem 550 ppm · 1.250 sat
▌  jobs 24h 45 falhos / 3 parciais     3. Se preferir rebalance: econ 0,9 →
▌  sparkline venda 30d (recharts)         teto 2.250 (margem 10%)
▌
▌ QUANDO SOME: local ≥ alvo por 24h, ou venda 7d sair do top 20%
▌ GUARDAS: ✓ sem compromisso Magma · ✓ não é parked · ✓ venda ≥ custo×1,10
▌ [Dispensar ▾ motivo] [Marcar como feita] [Por que esta regra existe?]
```

- Barra lateral colorida por `kind` (sky repor, emerald capacidade, amber
  parar compra, slate capital, violet abrir, rose preço, fog bloqueio) **e**
  ícone + rótulo; a cor sozinha nunca carrega o significado.
- Confiança: três pontos preenchidos (●●●/●●○/●○○) + palavra.
- Evidence: cada item é `{key, value, unit, source, compare?}`; a UI escolhe
  o widget pelo `key`: percentuais → `HealthGauge` com marcador do alvo;
  pares preço × custo → duas barras na mesma escala; contagens → texto com
  ícone; série → sparkline. Fonte em subtítulo no modo didático.
- Ações: lista numerada; cada passo com link profundo reaproveitando
  `pages/channel-ranking/links.ts` (`moduleHash`, `lightningOpsHash`,
  `graphExplorerHash`) — mover esse arquivo para `ui/src/lib/links.ts` para
  não importar de outra página. Fase 1: só links. Fase 3: botões que
  pré-preenchem (Loop quote, econ override) com confirmação.
- "Cotar" (R1): chama `POST /api/apps/loop/quote` só no clique; mostra
  spinner, depois margem por ppm e valor; erro vira texto no card, nunca toast
  solto.
- Dispensar pede motivo em três opções (já fiz manualmente / não concordo /
  depois) — alimenta a fase 2.
- Recolhido (modo denso): uma linha com ícone, título, confiança, valor e
  seta.

### 5. Filtros

Chips por `kind` (com contagem), por confiança, busca por alias/canal, e
"mostrar dispensadas/expiradas". Estado dos filtros em `localStorage`.

### 6. Gaveta "Como funciona"

Abre por botão no cabeçalho ou pelo link do card. Conteúdo por regra R1–R7:

- nome em linguagem comum (ex.: R1 "Vendedor bom, mas vazio, e o rebalance
  não alcança");
- diagrama de três caixas **Condição → Ação → Quando some** (SVG simples, o
  mesmo componente para todas);
- um exemplo real anonimizado (os casos do plano);
- limites ("não executa", "não vende canal", "não depende de lista externa").

Também explica o cálculo do valor (`volume_dia × (venda − reposição)`) com um
mini-exemplo numérico, e o que significa cada nível de confiança.

### 7. Histórico (fase 2)

Lista de seguidas/dispensadas com data, ação, e **antes × depois** (7d) em duas
barras; agregado no topo (acertou/errou/neutro). Lê `outcome` da tabela
`insights_recommendations`.

### 8. Estados

- **Aquecendo** (`node.ready=false`): nó com menos de 7 dias inteiros de
  histórico no ranking. Painel azul "Aquecendo: o Insights precisa de uma
  semana inteira" com barra N de 7 dias e, no modo didático, o porquê da
  semana; o mapa dos sinais continua embaixo. Nenhuma regra roda.
- **Vazio**: "Nada a recomendar agora — e isso é bom." + lista do que está
  sendo vigiado (as 7 regras com ✓) + último cálculo. Nunca tela branca.
- **Pílula de calibração** no cabeçalho: "Nó médio · equilibrado · 30 dias
  de dados" (classes de `classifyRebalanceNode`, as mesmas do AutoFee e do
  Rebalance Center); o tooltip diz os limiares em sats lidos nessa escala.
- **Sem dado**: fonte com ○ no mapa explica o porquê (ex.: Novos Canais sem
  sync) e quais regras ficam mudas por isso.
- **Carregando/erro**: padrão das outras páginas (`status` em texto no topo).
- **Mobile**: tiles 2×2, mapa vira lista, card empilha POR QUÊ sobre O QUE
  FAZER, botões em linha única rolável.

## Contrato backend → UI (o que a tela precisa)

`GET /api/lnops/insights` →

```json
{
  "computed_at": "…", "sources": [{"id":"autofee","ok":true,"age_sec":420}, …],
  "summary": {"value_sat_day":3200,"open":6,"open_high":2,
              "followed_30d":4,"followed_net_sat_day":1900,"idle_capital_sat":15000000},
  "items": [{
    "id":"…","kind":"repor_vendedor","rule":"R1","priority":0.92,
    "confidence":"high","confidence_reason":{"code":"sales_days","days":7},
    "channel_id":"…","channel_point":"…","peer_alias":"…","peer_pubkey":"…",
    "value_sat_day":1100,"cost_sat":0,
    "evidence":[{"key":"local_pct","value":1.4,"unit":"pct","compare":30,"source":"rebalance"},
                {"key":"sell_ppm_7d","value":2500,"unit":"ppm","source":"ranking"},
                {"key":"rebal_cost_ppm_7d","value":1950,"unit":"ppm","source":"rebalance"},
                {"key":"jobs_24h","value":48,"failed":45,"partial":3,"source":"rebalance"},
                {"key":"sales_series_30d","values":[…],"source":"ranking"}],
    "actions":[{"step":1,"tool":"rebalance_target","params":{}},
               {"step":2,"tool":"loop_in","params":{"last_hop_pubkey":"…","amount_sat":1000000}},
               {"step":3,"tool":"econ_override","params":{"ratio":0.9,"cap_ppm":2250,"margin_pct":10}}],
    "guards":[{"code":"magma_commitment","passed":true},{"code":"not_parked","passed":true},
              {"code":"sell_covers_cost","passed":true}],
    "invalidate_when":{"code":"local_at_target_24h"},
    "status":"open","created_at":"…","last_seen_at":"…"
  }]
}
```

Tudo que vira texto tem chave i18n: `insights.kind.*`, `insights.evidence.*`,
`insights.tool.*`, `insights.guard.*`, `insights.invalidate.*`,
`insights.confidence.*`, `insights.rule.R1..R7.{name,condition,action,gone,example}`.

## Arquivos (fase 1 — UI)

| Arquivo | O quê |
|---|---|
| `ui/src/pages/Insights.tsx` | página: cabeçalho, tiles, mapa, filtros, lista |
| `ui/src/pages/insights/InsightCard.tsx` | card com os três blocos e ações |
| `ui/src/pages/insights/EvidenceWidget.tsx` | escolhe gauge/barras/sparkline por `key` |
| `ui/src/pages/insights/SignalMap.tsx` | SVG das fontes |
| `ui/src/pages/insights/RuleGlossary.tsx` | gaveta "Como funciona" + diagrama |
| `ui/src/pages/insights/types.ts` | tipos do contrato acima |
| `ui/src/pages/insights/links.ts` | links profundos próprios (mesmo padrão de `channel-ranking/links.ts`; nada foi movido para não tocar em página existente) |
| `ui/src/api.ts` | `getInsights`, `recomputeInsights`, `dismissInsight`, `markInsightDone` |
| `ui/src/App.tsx` | rota `insights`, item primeiro no grupo `lightning` |
| `ui/src/i18n/{en,pt-BR}.json` | `nav.insights` + bloco `insights.*` |

Reuso sem alteração: `HeroTile`, `HealthGauge`, `ThemeGauge`,
`MetricDisclosure`, `recharts`, formatadores de `i18n`.

## O que NÃO entra na tela

- Botões que executam swap, abertura ou fechamento direto (fase 3 só
  pré-preenche e manda para a tela da ferramenta).
- Telegram/notificações.
- Qualquer texto fixo em um idioma só.
- Listas externas (clube, allowlists) como critério.

## Ordem de construção (quando a fase 1 for aprovada)

1. `types.ts` + mock JSON local (os 4 exemplos do protótipo) → página renderiza
   sem backend.
2. Card + EvidenceWidget + glossário com i18n completo nos dois idiomas.
3. Mapa dos sinais, filtros, estados vazio/erro, mobile.
4. Ligar na API real; remover mock.
5. Revisão de acessibilidade (contraste, foco, sem cor-só) e de tradução.
