# Sovereign: estoque sem venda e novas reposições — 0.5.33

## Problema corrigido

O guard anterior procurava apenas o último rebalance que tivesse executado pelo
menos 25% do `target_amount_sat`. Esse campo podia conter todo o déficit do canal,
enquanto o executor comprava apenas `max_amount_sat`. Compras reais menores que
25% do déficit ficavam invisíveis. Além disso, a janela inicial de duas horas e o
bypass de exploração permitiam repetir compras antes de observar venda.

## Nova regra

- Considera o volume realmente enviado por todas as compras Sovereign na janela
  de slow seller (mínimo de 24 horas), incluindo execuções parciais históricas.
- Usa a atribuição FIFO existente: cada forward e sua receita são atribuídos uma
  única vez. Lotes anteriores e compras de outras origens participam do consumo
  FIFO, mas somente lotes Sovereign entram neste guard.
- Mantém os limiares de recuperação, agora por lote: venda de pelo menos 10% do
  volume **ou** recuperação de pelo menos 25% da fee libera aquele lote da
  observação. Isso não significa venda de todo o estoque ou lucro realizado.
- Uma compra nova sem recuperação fica protegida imediatamente. Enquanto houver
  lotes em observação, compra recente impõe quatro horas de espera tanto para a
  seleção normal quanto para exploração. Recuperação material pode liberar antes.
- Após quatro horas, a seleção normal mantém a penalidade de score existente.
  Estoque severamente parado após a janela de atribuição continua bloqueando
  compras normais: venda inferior a 2% e payback inferior a 10%.
- A exploração pode voltar após a espera, com 10% da parcela normal ou o mínimo
  de execução configurado, o que for maior, nunca acima da parcela original.
  Custo, ganho, lucro e ROI são recalculados; orçamento e limites econômicos
  continuam aplicáveis. Um novo sucesso reinicia a espera, não a idade do estoque.
- Sem estoque em observação, a primeira compra exploratória permanece inalterada.
- Falha ao carregar o histórico não significa estoque vazio: a reposição
  Sovereign é pausada naquele scan com motivo explícito.

São dois relógios distintos: a compra Sovereign mais recente controla o intervalo
entre reposições, enquanto o lote mais antigo ainda em observação controla a
persistência da falta de venda. O job passa a gravar a parcela selecionada, não o
déficit estratégico completo; o histórico antigo não precisa de migração.

## Limites e compatibilidade

- Não muda AutoFee, seus pisos econômicos, Automation Interlock, regras de source,
  nem a execução de slots garantidos ou de rebalances manuais do operador.
- O interlock pode aumentar o score de um canal, mas não ultrapassa este guard.
- Não fecha canais e não altera automaticamente a configuração do operador.
- FIFO é atribuição contábil de fluxo, não rastreamento físico dos mesmos sats.
  O saldo mostrado é dos lotes ainda em observação, não todo o saldo local.
- Lotes expiram da observação conforme a janela configurada. Não é um bloqueio
  permanente: a rede pode mudar e a exploração continua possível.
- A seleção normal ainda pode repor durante a fase de penalidade suave. A mudança
  corrige compras invisíveis e repetição rápida; não exige vender 100% antes de
  qualquer reposição.

## Validação após deploy

Comparar janelas equivalentes, sem confundir queda geral de demanda com falha do
agente. Acompanhar volume comprado/vendido por canal, payback FIFO, custo total,
motivos de skip, participação real de exploração e giro após os probes reduzidos.
Os novos campos de decisão são `unsold_paid_sat`, `unsold_oldest_at`,
`unsold_last_paid_at` e `inventory_probe`; a UI mostra estoque em observação e
exploração reduzida. Os testes de regressão usam cenários sintéticos, sem dados
privados dos nodes. Esta PR não faz deploy nem altera parâmetros em produção.
